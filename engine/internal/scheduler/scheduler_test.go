// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobsRunSeriallyOnOneThread(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	var active, maxActive atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		p := Batch
		if i%3 == 0 {
			p = Realtime
		}
		go func(p Priority) {
			defer wg.Done()
			err := s.Do(context.Background(), p, func() error {
				n := active.Add(1)
				for {
					m := maxActive.Load()
					if n <= m || maxActive.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(200 * time.Microsecond)
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(p)
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent jobs = %d, want 1", maxActive.Load())
	}
	if got := s.Stats().Completed; got != 50 {
		t.Fatalf("completed = %d", got)
	}
}

// block occupies the worker until release is closed.
func block(t *testing.T, s *Scheduler) (release chan struct{}, done chan error) {
	t.Helper()
	started := make(chan struct{})
	release = make(chan struct{})
	done = make(chan error, 1)
	go func() {
		done <- s.Do(context.Background(), Batch, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	return release, done
}

func waitQueued(t *testing.T, s *Scheduler, rt, batch int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Stats()
		if st.RealtimeQueued == rt && st.BatchQueued == batch {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue never reached rt=%d batch=%d: %+v", rt, batch, s.Stats())
}

func TestRealtimeRunsBeforeQueuedBatch(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	release, done := block(t, s)

	var mu sync.Mutex
	var order []string
	rec := func(name string) func() error {
		return func() error { mu.Lock(); order = append(order, name); mu.Unlock(); return nil }
	}
	var wg sync.WaitGroup
	submit := func(p Priority, name string) {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Do(context.Background(), p, rec(name)) }()
	}
	submit(Batch, "b1")
	waitQueued(t, s, 0, 1)
	submit(Batch, "b2")
	waitQueued(t, s, 0, 2)
	submit(Realtime, "r1")
	waitQueued(t, s, 1, 2)
	if st := s.Stats(); !st.Running || st.RunningKind != "batch" {
		t.Fatalf("stats %+v", st)
	}
	close(release)
	<-done
	wg.Wait()
	want := []string{"r1", "b1", "b2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestCancelWhileQueuedDropsJob(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	release, done := block(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Bool
	errc := make(chan error, 1)
	go func() { errc <- s.Do(ctx, Batch, func() error { ran.Store(true); return nil }) }()
	waitQueued(t, s, 0, 1)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if s.Stats().BatchQueued != 0 {
		t.Fatal("cancelled job still counted")
	}
	close(release)
	<-done
	_ = s.Do(context.Background(), Batch, func() error { return nil }) // barrier
	if ran.Load() {
		t.Fatal("cancelled job ran")
	}
}

func TestCancelAfterStartWaitsForCompletion(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	want := errors.New("job result")
	errc := make(chan error, 1)
	go func() {
		errc <- s.Do(ctx, Realtime, func() error {
			close(started)
			time.Sleep(20 * time.Millisecond)
			return want
		})
	}()
	<-started
	cancel()
	if err := <-errc; !errors.Is(err, want) {
		t.Fatalf("got %v, want job's own error", err)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	s := New()
	defer s.Close(context.Background())
	var pe *PanicError
	if err := s.Do(context.Background(), Batch, func() error { panic("boom") }); !errors.As(err, &pe) {
		t.Fatalf("got %v", err)
	}
	if err := s.Do(context.Background(), Batch, func() error { return nil }); err != nil {
		t.Fatalf("scheduler dead after panic: %v", err)
	}
}

func TestCloseDrainsQueueThenRejects(t *testing.T) {
	s := New()
	release, done := block(t, s)
	var ran atomic.Bool
	errc := make(chan error, 1)
	go func() { errc <- s.Do(context.Background(), Batch, func() error { ran.Store(true); return nil }) }()
	waitQueued(t, s, 0, 1)
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	close(release)
	<-done
	if err := <-errc; err != nil || !ran.Load() {
		t.Fatalf("queued job not drained: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := s.Do(context.Background(), Batch, func() error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}
