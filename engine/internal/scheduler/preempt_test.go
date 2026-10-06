// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) add(s string) { r.mu.Lock(); r.order = append(r.order, s); r.mu.Unlock() }
func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParsePriority(t *testing.T) {
	for _, p := range []Priority{Realtime, High, Normal} {
		got, err := ParsePriority(p.String())
		if err != nil || got != p {
			t.Fatal(p, got, err)
		}
		b, err := p.MarshalText()
		if err != nil || string(b) != p.String() {
			t.Fatal(string(b), err)
		}
	}
	if _, err := ParsePriority("batch"); err == nil {
		t.Fatal("batch accepted")
	}
}

func TestThreePriorityOrderAndFIFO(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	release, done := block(t, s)
	rec := &recorder{}
	var wg sync.WaitGroup
	submit := func(p Priority, name string) {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Do(context.Background(), p, func() error { rec.add(name); return nil }) }()
	}
	submit(Normal, "n1")
	waitQueued3(t, s, 0, 0, 1)
	submit(High, "h1")
	waitQueued3(t, s, 0, 1, 1)
	submit(Normal, "n2")
	waitQueued3(t, s, 0, 1, 2)
	submit(High, "h2")
	waitQueued3(t, s, 0, 2, 2)
	submit(Realtime, "r1")
	waitQueued3(t, s, 1, 2, 2)
	close(release)
	<-done
	wg.Wait()
	if got, want := rec.get(), []string{"r1", "h1", "h2", "n1", "n2"}; !equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// segmentedTask is a resumable task over n segments; each segment takes
// segDur unless interrupted (then the segment is discarded).
type segmentedTask struct {
	name     string
	n        int
	segDur   time.Duration
	next     int
	attempts int
	rec      *recorder
	started  chan struct{} // closed when the first segment starts
	once     sync.Once
}

func (st *segmentedTask) run(r *Run) error {
	st.attempts++
	for st.next < st.n {
		if st.next > 0 && r.ShouldYield() {
			return ErrYield
		}
		st.once.Do(func() { close(st.started) })
		select {
		case <-time.After(st.segDur):
		case <-r.Interrupted():
			st.rec.add(st.name + "!" + itoa(st.next))
			return errors.New("interrupted")
		}
		st.rec.add(st.name + itoa(st.next))
		st.next++
	}
	return nil
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestRealtimePreemptsAndTaskResumesFromInterruptedSegment(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "j", n: 3, segDur: 200 * time.Millisecond, rec: rec, started: make(chan struct{})}
	jobDone := make(chan error, 1)
	go func() { jobDone <- s.DoPreemptible(context.Background(), Normal, job.run) }()
	<-job.started
	// Realtime arrives in the middle of segment 0.
	time.Sleep(20 * time.Millisecond)
	t0 := time.Now()
	if err := s.Do(context.Background(), Realtime, func() error { rec.add("rt"); return nil }); err != nil {
		t.Fatal(err)
	}
	if lat := time.Since(t0); lat > 100*time.Millisecond {
		t.Fatalf("realtime waited %v behind a preemptible segment", lat)
	}
	if err := <-jobDone; err != nil {
		t.Fatalf("preempted job failed: %v", err)
	}
	want := []string{"j!0", "rt", "j0", "j1", "j2"}
	if got := rec.get(); !equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if job.attempts != 2 {
		t.Fatalf("attempts = %d", job.attempts)
	}
	if st := s.Stats(); st.Preemptions != 1 || st.Completed != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestPreemptedTaskKeepsHeadOfItsQueue(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "a", n: 2, segDur: 150 * time.Millisecond, rec: rec, started: make(chan struct{})}
	jobDone := make(chan error, 1)
	go func() { jobDone <- s.DoPreemptible(context.Background(), Normal, job.run) }()
	<-job.started
	otherDone := make(chan error, 1)
	go func() {
		otherDone <- s.Do(context.Background(), Normal, func() error { rec.add("b"); return nil })
	}()
	waitQueued3(t, s, 0, 0, 1)
	if err := s.Do(context.Background(), Realtime, func() error { rec.add("rt"); return nil }); err != nil {
		t.Fatal(err)
	}
	<-jobDone
	<-otherDone
	want := []string{"a!0", "rt", "a0", "a1", "b"}
	if got := rec.get(); !equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestSegmentedTaskYieldsToHighBetweenSegments(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "j", n: 3, segDur: 60 * time.Millisecond, rec: rec, started: make(chan struct{})}
	jobDone := make(chan error, 1)
	go func() { jobDone <- s.DoPreemptible(context.Background(), Normal, job.run) }()
	<-job.started
	if err := s.Do(context.Background(), High, func() error { rec.add("h"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-jobDone; err != nil {
		t.Fatal(err)
	}
	// High does not preempt: segment 0 completes, then High runs.
	want := []string{"j0", "h", "j1", "j2"}
	if got := rec.get(); !equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if st := s.Stats(); st.Preemptions != 0 || st.Yields != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestNonPreemptibleTaskRunsToCompletion(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Do(context.Background(), High, func() error {
			close(started)
			time.Sleep(80 * time.Millisecond)
			rec.add("whole")
			return nil
		})
	}()
	<-started
	if err := s.Do(context.Background(), Realtime, func() error { rec.add("rt"); return nil }); err != nil {
		t.Fatal(err)
	}
	<-done
	if got := rec.get(); !equal(got, []string{"whole", "rt"}) {
		t.Fatalf("order %v", got)
	}
}

func TestHoldGatesNonRealtimeAndPreempts(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "j", n: 2, segDur: 200 * time.Millisecond, rec: rec, started: make(chan struct{})}
	jobDone := make(chan error, 1)
	go func() { jobDone <- s.DoPreemptible(context.Background(), Normal, job.run) }()
	<-job.started
	s.SetHold(true) // a live session starts
	highDone := make(chan error, 1)
	go func() { highDone <- s.Do(context.Background(), High, func() error { rec.add("h"); return nil }) }()
	for i := 0; i < 3; i++ { // the session's frames
		if err := s.Do(context.Background(), Realtime, func() error { rec.add("rt"); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	waitQueued3(t, s, 0, 1, 1)
	if st := s.Stats(); st.Running || !st.RealtimeHold {
		t.Fatalf("non-realtime work ran during the hold: %+v", st)
	}
	s.SetHold(false) // session ends
	<-highDone
	if err := <-jobDone; err != nil {
		t.Fatal(err)
	}
	// The preempted job resumes at the head of Normal; High runs first.
	want := []string{"j!0", "rt", "rt", "rt", "h", "j0", "j1"}
	if got := rec.get(); !equal(got, want) {
		t.Fatalf("order %v, want %v", rec.get(), want)
	}
}

func TestCancelRunningPreemptibleTask(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "j", n: 5, segDur: time.Second, rec: rec, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.DoPreemptible(ctx, Normal, job.run) }()
	<-job.started
	t0 := time.Now()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if time.Since(t0) > 200*time.Millisecond {
		t.Fatal("cancel did not interrupt the running segment")
	}
	if got := rec.get(); !equal(got, []string{"j!0"}) {
		t.Fatalf("order %v", got)
	}
	if st := s.Stats(); st.Preemptions != 0 || st.NormalQueued != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestCancelPreemptedTaskWhileRequeued(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	rec := &recorder{}
	job := &segmentedTask{name: "j", n: 2, segDur: 200 * time.Millisecond, rec: rec, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.DoPreemptible(ctx, Normal, job.run) }()
	<-job.started
	s.SetHold(true)
	waitQueued3(t, s, 0, 0, 1) // preempted and parked behind the hold
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	s.SetHold(false)
	_ = s.Do(context.Background(), Normal, func() error { return nil }) // barrier
	if got := rec.get(); !equal(got, []string{"j!0"}) {
		t.Fatalf("cancelled job resumed: %v", got)
	}
}

func TestRealtimeTaskIsNeverPreempted(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.DoPreemptible(context.Background(), Realtime, func(r *Run) error {
			close(started)
			select {
			case <-r.Interrupted():
				return errors.New("preempted")
			case <-time.After(50 * time.Millisecond):
				return nil
			}
		})
	}()
	<-started
	if err := s.Do(context.Background(), Realtime, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
