// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package scheduler implements the serial inference scheduler of PLAN §4.6:
// a single worker goroutine, locked to one OS thread, runs every task that
// touches the native libraries (audio.cpp, transcribe.cpp, onnxruntime) one
// at a time, ordered by three priorities.
//
//   - Realtime (实时): live dictation. Runs before everything else and
//     preempts a running preemptible task.
//   - High (优先): push-to-talk voice transcription, engine control.
//   - Normal (普通): file transcription jobs.
//
// Same-priority tasks run in submission order (FIFO). Only Realtime
// preempts; High and Normal never interrupt a running task, but a segmented
// task (a file job) checks [Run.ShouldYield] between segments and gives the
// thread to waiting higher-priority work.
//
// Preemption: when Realtime work is queued, or a realtime hold is set (a
// live session holds the realtime slot, see [Scheduler.SetHold]), the
// running task — if it was submitted with [Scheduler.DoPreemptible] and is
// not Realtime itself — has its [Run.Interrupted] channel closed. The task
// is expected to stop at its next safe boundary and return; it is then put
// back at the head of its priority queue (not failed) and its function is
// called again later, resuming from where it stopped (the function keeps
// its own progress). What "next safe boundary" means is up to the task:
// transcribe.cpp polls an abort callback at chunk / decode boundaries, while
// an audio.cpp call or an onnxruntime VAD run in flight always completes.
//
// While a hold is set only Realtime tasks start: the live session owns the
// thread between its frames, and non-realtime work waits until it ends.
//
// Non-inference requests (status, config, events) must not go through the
// scheduler.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
)

// Priority orders queued tasks; lower values run first.
type Priority int

const (
	// Realtime (实时) is live dictation (WS /v1/asr/stream); it preempts.
	Realtime Priority = iota
	// High (优先) is the default for push-to-talk voice transcription and
	// is used for engine control (load / unload / reload).
	High
	// Normal (普通) is the default for file transcription jobs.
	Normal

	numPriorities = 3
)

func (p Priority) String() string {
	switch p {
	case Realtime:
		return "realtime"
	case High:
		return "high"
	case Normal:
		return "normal"
	}
	return fmt.Sprintf("priority(%d)", int(p))
}

// ParsePriority parses the API value ("realtime", "high", "normal").
func ParsePriority(s string) (Priority, error) {
	switch s {
	case "realtime":
		return Realtime, nil
	case "high":
		return High, nil
	case "normal":
		return Normal, nil
	}
	return 0, fmt.Errorf("invalid priority %q (want realtime, high or normal)", s)
}

// MarshalText encodes the API value.
func (p Priority) MarshalText() ([]byte, error) {
	if p < 0 || p >= numPriorities {
		return nil, fmt.Errorf("invalid priority %d", int(p))
	}
	return []byte(p.String()), nil
}

// Errors.
var (
	// ErrClosed is returned for tasks submitted after Close.
	ErrClosed = errors.New("scheduler closed")
	// ErrYield, returned by a preemptible task, puts it back at the head of
	// its priority queue to be resumed later (used between segments when
	// ShouldYield reports waiting higher-priority work).
	ErrYield = errors.New("scheduler: task yielded")
)

// PanicError wraps a panic raised inside a task.
type PanicError struct{ Value any }

func (e *PanicError) Error() string { return fmt.Sprintf("task panicked: %v", e.Value) }

type task struct {
	prio        Priority
	preemptible bool
	ctx         context.Context
	fn          func(*Run) error
	done        chan error
	// guarded by Scheduler.mu
	started   bool
	cancelled bool
}

// Run is handed to a preemptible task for one execution attempt.
type Run struct {
	s         *Scheduler
	t         *task
	intr      chan struct{}
	once      sync.Once
	preempted bool // guarded by s.mu
}

// Context is the submitter's context.
func (r *Run) Context() context.Context { return r.t.ctx }

// Interrupted is closed when the task should stop: it was preempted by
// realtime work, or its context ended (cancelled).
func (r *Run) Interrupted() <-chan struct{} { return r.intr }

// Preempted reports whether realtime work preempted this attempt.
func (r *Run) Preempted() bool {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.preempted
}

// ShouldYield reports whether the task should give up the thread at its
// next segment boundary: it was interrupted, or higher-priority work is
// waiting and allowed to start.
func (r *Run) ShouldYield() bool {
	select {
	case <-r.intr:
		return true
	default:
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.higherWaitingLocked(r.t.prio)
}

func (r *Run) interrupt() { r.once.Do(func() { close(r.intr) }) }

// Stats is exposed through /v1/engine/status.
type Stats struct {
	RealtimeQueued int    `json:"realtime_queued"`
	HighQueued     int    `json:"high_queued"`
	NormalQueued   int    `json:"normal_queued"`
	Running        bool   `json:"running"`
	RunningKind    string `json:"running_kind,omitempty"`
	RealtimeHold   bool   `json:"realtime_hold"`
	Completed      uint64 `json:"completed"`
	Preemptions    uint64 `json:"preemptions"`
	Yields         uint64 `json:"yields"`
}

// Scheduler is a serial, priority-aware task runner.
type Scheduler struct {
	mu          sync.Mutex
	cond        *sync.Cond
	queues      [numPriorities][]*task
	current     *Run // running attempt, nil when idle
	hold        bool
	completed   uint64
	preemptions uint64
	yields      uint64
	closed      bool
	exited      chan struct{}
}

// New starts the worker goroutine.
func New() *Scheduler {
	s := &Scheduler{exited: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	go s.loop()
	return s
}

// runnableLocked reports whether priority p may start now.
func (s *Scheduler) runnableLocked(p Priority) bool { return p == Realtime || !s.hold }

func (s *Scheduler) higherWaitingLocked(p Priority) bool {
	for q := Realtime; q < p; q++ {
		if !s.runnableLocked(q) {
			continue
		}
		for _, t := range s.queues[q] {
			if !t.cancelled {
				return true
			}
		}
	}
	return false
}

// preemptLocked interrupts the running attempt if realtime work wants the
// thread and the running task allows it.
func (s *Scheduler) preemptLocked() {
	r := s.current
	if r == nil || !r.t.preemptible || r.t.prio == Realtime || r.preempted {
		return
	}
	r.preempted = true
	s.preemptions++
	r.interrupt()
}

func (s *Scheduler) nextLocked() *task {
	for p := Realtime; p < numPriorities; p++ {
		if !s.runnableLocked(p) {
			continue
		}
		for len(s.queues[p]) > 0 {
			t := s.queues[p][0]
			s.queues[p] = s.queues[p][1:]
			if !t.cancelled {
				return t
			}
		}
	}
	return nil
}

func (s *Scheduler) idleLocked() bool {
	if s.current != nil {
		return false
	}
	for p := range s.queues {
		for _, t := range s.queues[p] {
			if !t.cancelled {
				return false
			}
		}
	}
	return true
}

func (s *Scheduler) loop() {
	// All native calls happen on this one OS thread (cgo libraries with
	// thread-local state, CUDA contexts).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.exited)
	for {
		s.mu.Lock()
		var t *task
		for {
			t = s.nextLocked()
			if t != nil || (s.closed && s.idleLocked()) {
				break
			}
			s.cond.Wait()
		}
		if t == nil {
			s.mu.Unlock()
			return
		}
		t.started = true
		r := &Run{s: s, t: t, intr: make(chan struct{})}
		s.current = r
		if t.preemptible && t.prio != Realtime && (s.hold || s.higherRealtimeLocked()) {
			s.preemptLocked()
		}
		s.mu.Unlock()

		var stop func() bool
		if t.preemptible {
			stop = context.AfterFunc(t.ctx, r.interrupt)
		}
		err := call(t.fn, r)
		if stop != nil {
			stop()
		}

		s.mu.Lock()
		s.current = nil
		requeue := false
		if t.preemptible && t.ctx.Err() == nil && !s.closed {
			if errors.Is(err, ErrYield) {
				requeue = true
				s.yields++
			} else if err != nil && r.preempted {
				requeue = true
			}
		}
		if requeue {
			// Head of its own queue: resume before same-priority work
			// submitted later.
			t.started = false
			s.queues[t.prio] = append([]*task{t}, s.queues[t.prio]...)
		} else {
			s.completed++
		}
		s.mu.Unlock()
		s.cond.Broadcast()
		if !requeue {
			if t.preemptible && t.ctx.Err() != nil && err != nil {
				err = t.ctx.Err()
			} else if errors.Is(err, ErrYield) {
				err = ErrClosed // yielded while closing
			}
			t.done <- err
		}
	}
}

func (s *Scheduler) higherRealtimeLocked() bool {
	for _, t := range s.queues[Realtime] {
		if !t.cancelled {
			return true
		}
	}
	return false
}

func call(fn func(*Run) error, r *Run) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v}
		}
	}()
	return fn(r)
}

func (s *Scheduler) submit(ctx context.Context, p Priority, preemptible bool, fn func(*Run) error) error {
	if p < 0 || p >= numPriorities {
		return fmt.Errorf("invalid priority %d", p)
	}
	t := &task{prio: p, preemptible: preemptible, ctx: ctx, fn: fn, done: make(chan error, 1)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.queues[p] = append(s.queues[p], t)
	if p == Realtime {
		s.preemptLocked()
	}
	s.mu.Unlock()
	s.cond.Broadcast()

	select {
	case err := <-t.done:
		return err
	case <-ctx.Done():
		s.mu.Lock()
		if !t.started {
			t.cancelled = true
			s.mu.Unlock()
			s.cond.Broadcast()
			return ctx.Err()
		}
		s.mu.Unlock()
		// Running: a preemptible task was interrupted by AfterFunc and
		// returns at its next boundary; others run to completion.
		return <-t.done
	}
}

// Do queues fn and waits for it. fn is not preemptible: once started it
// runs to completion. If ctx ends while fn is still queued, fn is dropped
// and ctx.Err() returned; once started, Do waits for it and returns its
// error.
func (s *Scheduler) Do(ctx context.Context, p Priority, fn func() error) error {
	return s.submit(ctx, p, false, func(*Run) error { return fn() })
}

// DoPreemptible queues a preemptible task and waits until it completes,
// fails or is cancelled. fn may be called several times: each time it is
// preempted (returns any error after Run.Preempted became true) or returns
// ErrYield, it is re-queued at the head of its priority and called again
// later with a fresh Run, and must resume from its own saved progress.
// When ctx ends, a queued task is dropped and a running one is interrupted;
// Do returns ctx.Err() unless fn completed anyway.
func (s *Scheduler) DoPreemptible(ctx context.Context, p Priority, fn func(*Run) error) error {
	return s.submit(ctx, p, true, fn)
}

// SetHold sets or clears the realtime hold. While it is set, only Realtime
// tasks start, and setting it preempts a running preemptible task. The
// manager holds it for the lifetime of a live session.
func (s *Scheduler) SetHold(on bool) {
	s.mu.Lock()
	s.hold = on
	if on {
		s.preemptLocked()
	}
	s.mu.Unlock()
	s.cond.Broadcast()
}

// Stats returns a snapshot of the queue state.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Running: s.current != nil, RealtimeHold: s.hold, Completed: s.completed, Preemptions: s.preemptions, Yields: s.yields}
	counts := [numPriorities]*int{&st.RealtimeQueued, &st.HighQueued, &st.NormalQueued}
	for p := range s.queues {
		for _, t := range s.queues[p] {
			if !t.cancelled {
				*counts[p]++
			}
		}
	}
	if s.current != nil {
		st.RunningKind = s.current.t.prio.String()
	}
	return st
}

// Idle reports whether nothing is running or queued.
func (s *Scheduler) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idleLocked()
}

// Close stops accepting tasks, lets already-queued tasks finish (the hold is
// released so they can), and waits for the worker to exit or ctx to end.
func (s *Scheduler) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.hold = false
	s.mu.Unlock()
	s.cond.Broadcast()
	select {
	case <-s.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
