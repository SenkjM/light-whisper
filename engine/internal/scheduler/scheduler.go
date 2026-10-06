// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package scheduler implements the serial inference scheduler from PLAN §4.6:
// a single worker goroutine, locked to one OS thread, runs every job that
// touches the native libraries (audio.cpp, transcribe.cpp, onnxruntime) one
// at a time. Realtime jobs always run before queued batch jobs; a running job
// is never pre-empted. Non-inference requests (status, config, events) must
// not go through the scheduler.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
)

// Priority orders queued jobs.
type Priority int

const (
	// Realtime is used by R2T2 streaming sessions.
	Realtime Priority = iota
	// Batch is used by Qwen3 transcription / jobs and engine (re)loads.
	Batch
)

func (p Priority) String() string {
	switch p {
	case Realtime:
		return "realtime"
	case Batch:
		return "batch"
	}
	return fmt.Sprintf("priority(%d)", int(p))
}

// ErrClosed is returned for jobs submitted after Close or dropped by Close.
var ErrClosed = errors.New("scheduler closed")

// PanicError wraps a panic raised inside a job.
type PanicError struct{ Value any }

func (e *PanicError) Error() string { return fmt.Sprintf("job panicked: %v", e.Value) }

type job struct {
	fn   func() error
	done chan error
	// state guarded by Scheduler.mu
	started   bool
	cancelled bool
}

// Stats is exposed through /v1/engine/status.
type Stats struct {
	RealtimeQueued int    `json:"realtime_queued"`
	BatchQueued    int    `json:"batch_queued"`
	Running        bool   `json:"running"`
	RunningKind    string `json:"running_kind,omitempty"`
	Completed      uint64 `json:"completed"`
}

// Scheduler is a serial, priority-aware job runner.
type Scheduler struct {
	mu          sync.Mutex
	cond        *sync.Cond
	queues      [2][]*job
	running     bool
	runningKind Priority
	completed   uint64
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

func (s *Scheduler) loop() {
	// All native calls happen on this one OS thread (cgo libraries with
	// thread-local state, CUDA contexts).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.exited)
	for {
		s.mu.Lock()
		var j *job
		var kind Priority
		for {
			for p := Realtime; p <= Batch; p++ {
				for len(s.queues[p]) > 0 {
					cand := s.queues[p][0]
					s.queues[p] = s.queues[p][1:]
					if !cand.cancelled {
						j, kind = cand, p
						break
					}
				}
				if j != nil {
					break
				}
			}
			if j != nil || s.closed {
				break
			}
			s.cond.Wait()
		}
		if j == nil { // closed and drained
			s.mu.Unlock()
			return
		}
		j.started = true
		s.running = true
		s.runningKind = kind
		s.mu.Unlock()

		err := run(j.fn)

		s.mu.Lock()
		s.running = false
		s.completed++
		s.mu.Unlock()
		s.cond.Broadcast()
		j.done <- err
	}
}

func run(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r}
		}
	}()
	return fn()
}

// Do queues fn and waits for it. If ctx ends while fn is still queued, fn is
// dropped and ctx.Err() returned. Once fn has started, Do waits for it to
// finish (native calls cannot be interrupted) and returns its error.
func (s *Scheduler) Do(ctx context.Context, p Priority, fn func() error) error {
	if p != Realtime && p != Batch {
		return fmt.Errorf("invalid priority %d", p)
	}
	j := &job{fn: fn, done: make(chan error, 1)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.queues[p] = append(s.queues[p], j)
	s.mu.Unlock()
	s.cond.Broadcast()

	select {
	case err := <-j.done:
		return err
	case <-ctx.Done():
		s.mu.Lock()
		if !j.started {
			j.cancelled = true
			s.mu.Unlock()
			return ctx.Err()
		}
		s.mu.Unlock()
		return <-j.done
	}
}

// Stats returns a snapshot of the queue state.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Running: s.running, Completed: s.completed}
	for _, j := range s.queues[Realtime] {
		if !j.cancelled {
			st.RealtimeQueued++
		}
	}
	for _, j := range s.queues[Batch] {
		if !j.cancelled {
			st.BatchQueued++
		}
	}
	if s.running {
		st.RunningKind = s.runningKind.String()
	}
	return st
}

// Idle reports whether nothing is running or queued.
func (s *Scheduler) Idle() bool {
	st := s.Stats()
	return !st.Running && st.RealtimeQueued == 0 && st.BatchQueued == 0
}

// Close stops accepting jobs, lets already-queued jobs finish, and waits for
// the worker to exit or ctx to end.
func (s *Scheduler) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cond.Broadcast()
	select {
	case <-s.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
