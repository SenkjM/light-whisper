// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/r2t2"
	"github.com/SenkjM/light-whisper/engine/internal/scheduler"
)

const sr = asr.SampleRate

func newJobFixture(t *testing.T, engineJSON string, unit int) *fixture {
	t.Helper()
	p := filepath.Join(t.TempDir(), "engine.json")
	if err := os.WriteFile(p, []byte(engineJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{mock: asr.NewMock(), store: store, hub: events.NewHub()}
	f.m = New(Options{Store: store, Backend: f.mock, Hub: f.hub, IdleTick: time.Hour, JobSegmentSamples: unit})
	t.Cleanup(func() { _ = f.m.Close(context.Background()); f.hub.Close() })
	return f
}

// voiced builds audio: for each length, that many voiced samples followed by
// one second of silence.
func voiced(lengths ...int) []int16 {
	var out []int16
	for _, n := range lengths {
		for i := 0; i < n; i++ {
			out = append(out, 1000)
		}
		out = append(out, make([]int16, sr)...)
	}
	return out
}

func waitJob(t *testing.T, m *Manager, id string, st JobState) JobInfo {
	t.Helper()
	var info JobInfo
	eventually(t, "job "+string(st), func() bool {
		var err error
		info, err = m.Job(id)
		return err == nil && info.State == st
	})
	return info
}

func TestPlanSegments(t *testing.T) {
	regions := []asr.Span{{Start: -5, End: 100}, {Start: 150, End: 300}, {Start: 400, End: 1000}, {Start: 1100, End: 1200}, {Start: 1300, End: 1290}}
	got := PlanSegments(regions, 1250, 350)
	want := []asr.Span{{Start: 0, End: 300}, {Start: 400, End: 750}, {Start: 750, End: 1000}, {Start: 1100, End: 1200}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	if PlanSegments(nil, 10, 5) != nil {
		t.Fatal("no speech must give no units")
	}
}

func TestResolvePriorityDefaultsAndConfig(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b"}`)
	check := func(kind, value string, want scheduler.Priority) {
		t.Helper()
		got, err := f.m.ResolvePriority(kind, value)
		if err != nil || got != want {
			t.Fatalf("%s %q: %v %v", kind, value, got, err)
		}
	}
	check(KindVoice, "", scheduler.High)
	check(KindFile, "", scheduler.Normal)
	check(KindVoice, "realtime", scheduler.Realtime)
	check(KindFile, "high", scheduler.High)
	f.patch(t, map[string]any{"default_priority_voice": "normal", "default_priority_file": "high"})
	check(KindVoice, "", scheduler.Normal)
	check(KindFile, "", scheduler.High)
	check(KindVoice, "high", scheduler.High) // explicit wins
	for _, tc := range [][2]string{{KindFile, "realtime"}, {KindVoice, "batch"}, {KindFile, "urgent"}} {
		if _, err := f.m.ResolvePriority(tc[0], tc[1]); !errors.Is(err, ErrBadPriority) {
			t.Fatalf("%v accepted: %v", tc, err)
		}
	}
	if _, err := f.m.Transcribe(context.Background(), make([]int16, 10), asr.TranscribeOptions{}, "batch"); !errors.Is(err, ErrBadPriority) {
		t.Fatal(err)
	}
	if _, err := f.m.SubmitJob(JobSpec{PCM: make([]int16, 10), Priority: "realtime"}); !errors.Is(err, ErrBadPriority) {
		t.Fatal(err)
	}
}

func TestQwen3TranscribePreemptedBySessionAndRequeued(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b"}`)
	ctx := context.Background()
	if err := f.m.Load(ctx); err != nil {
		t.Fatal(err)
	}
	f.mock.WorkPerSecond.Store(int64(200 * time.Millisecond))
	type out struct {
		res asr.Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.m.Transcribe(ctx, make([]int16, 3*sr), asr.TranscribeOptions{}, "")
		done <- out{res, err}
	}()
	eventually(t, "transcribe running", func() bool { return f.mock.Transcribes.Load() == 1 })
	t0 := time.Now()
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if lat := time.Since(t0); lat > 300*time.Millisecond {
		t.Fatalf("session start waited %v behind a 600 ms preemptible clip", lat)
	}
	if f.mock.Interrupted.Load() != 1 {
		t.Fatal("running clip was not interrupted")
	}
	if _, err := sess.Push(ctx, 0, make([]int16, 1600)); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		t.Fatalf("clip finished during the session: %+v", o)
	case <-time.After(20 * time.Millisecond):
	}
	sess.Cancel()
	o := <-done
	if o.err != nil || o.res.Text != "字字字" {
		t.Fatalf("requeued clip: %+v", o)
	}
	if f.mock.Transcribes.Load() != 2 {
		t.Fatalf("transcribes = %d", f.mock.Transcribes.Load())
	}
	if st := f.m.Status().Scheduler; st.Preemptions != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestQwen3SentenceSession(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b","default_language":"zh"}`)
	ctx := context.Background()
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 3})
	if err != nil || sess.Mode() != asr.ModeSentence {
		t.Fatal(err)
	}
	audio := voiced(2*sr, sr/2)
	var commits []string
	for off := 0; off < len(audio); off += 2560 {
		p, err := sess.Push(ctx, uint64(off), audio[off:min(off+2560, len(audio))])
		if err != nil {
			t.Fatal(err)
		}
		if p.Tentative != "" {
			t.Fatalf("sentence mode tentative %q", p.Tentative)
		}
		if len(commits) == 0 || commits[len(commits)-1] != p.Committed {
			commits = append(commits, p.Committed)
		}
	}
	res, err := sess.Finish(ctx)
	if err != nil || res.Text != "字字字" || res.Language != "zh" {
		t.Fatalf("%+v %v", res, err)
	}
	if want := []string{"", "字字", "字字字"}; len(commits) != 3 || commits[1] != want[1] || commits[2] != want[2] {
		t.Fatalf("commits %q", commits)
	}
}

func collectJobEvents(sub *events.Subscription, id string, until JobState, timeout time.Duration) ([]JobInfo, bool) {
	var out []JobInfo
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-sub.C:
			if ev.Type != events.TypeJobProgress {
				continue
			}
			info := ev.Data.(JobInfo)
			if info.ID != id {
				continue
			}
			out = append(out, info)
			if info.State == until {
				return out, true
			}
		case <-deadline:
			return out, false
		}
	}
}

func TestJobSegmentsProgressAndResult(t *testing.T) {
	f := newJobFixture(t, `{"engine":"qwen3-asr-0.6b","default_language":"zh"}`, 3*sr)
	sub := f.hub.Subscribe(256)
	defer sub.Cancel()
	audio := voiced(2*sr, 2*sr, 4*sr+sr/2)
	info, err := f.m.SubmitJob(JobSpec{PCM: audio, Segments: true})
	if err != nil || info.State != JobQueued || info.Priority != "normal" {
		t.Fatalf("%+v %v", info, err)
	}
	evs, ok := collectJobEvents(sub, info.ID, JobCompleted, 3*time.Second)
	if !ok {
		t.Fatalf("no completion: %+v", evs)
	}
	var done []int
	for _, e := range evs {
		if len(done) == 0 || done[len(done)-1] != e.SegmentsDone {
			done = append(done, e.SegmentsDone)
		}
	}
	// units: [0,2s) [3s,5s) [6s,9s) [9s,10.5s)
	if len(done) != 5 || done[4] != 4 || evs[len(evs)-1].Progress != 1 {
		t.Fatalf("progress %v / %+v", done, evs[len(evs)-1])
	}
	got, err := f.m.Job(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text == nil || *got.Text != "字字字字字字字字" || got.Language != "zh" || got.SegmentsTotal != 4 {
		t.Fatalf("%+v", got)
	}
	wantSegs := []JobSegment{{0, 2, "字字"}, {3, 5, "字字"}, {6, 9, "字字字"}, {9, 10.5, "字"}}
	for i, w := range wantSegs {
		if got.Segments[i] != w {
			t.Fatalf("segments %+v", got.Segments)
		}
	}
	if st := f.m.Status(); st.Jobs.Queued != 0 || st.Jobs.Running != 0 {
		t.Fatalf("%+v", st.Jobs)
	}
	if len(f.m.Jobs()) != 1 {
		t.Fatal("job list")
	}
}

func jobText(t *testing.T, f *fixture, audio []int16) string {
	t.Helper()
	info, err := f.m.SubmitJob(JobSpec{PCM: audio})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, f.m, info.ID, JobCompleted)
	return *got.Text
}

func TestJobPreemptedResumesFromInterruptedSegment(t *testing.T) {
	for _, engine := range []string{"qwen3-asr-0.6b", "confucius4-r2t2"} {
		t.Run(engine, func(t *testing.T) {
			f := newJobFixture(t, `{"engine":"`+engine+`"}`, 3*sr)
			audio := voiced(2*sr, 2*sr, 2*sr)
			reference := jobText(t, f, audio)
			base := f.mock.Transcribes.Load()

			f.mock.WorkPerSecond.Store(int64(100 * time.Millisecond)) // 200 ms per unit
			sub := f.hub.Subscribe(256)
			defer sub.Cancel()
			info, err := f.m.SubmitJob(JobSpec{PCM: audio})
			if err != nil {
				t.Fatal(err)
			}
			// Wait until unit 0 is done and unit 1 has started.
			eventually(t, "unit 1 running", func() bool {
				j, _ := f.m.Job(info.ID)
				return j.SegmentsDone == 1 && f.mock.Transcribes.Load() == base+2
			})
			time.Sleep(20 * time.Millisecond)
			ctx := context.Background()
			t0 := time.Now()
			sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 9})
			if err != nil {
				t.Fatal(err)
			}
			if lat := time.Since(t0); lat > 150*time.Millisecond {
				t.Fatalf("session start latency %v", lat)
			}
			for off := 0; off < 3*2560; off += 2560 {
				if _, err := sess.Push(ctx, uint64(off), make([]int16, 2560)); err != nil {
					t.Fatal(err)
				}
			}
			j, _ := f.m.Job(info.ID)
			if j.State != JobQueued || j.SegmentsDone != 1 || j.Preemptions != 1 {
				t.Fatalf("during session: %+v", j)
			}
			if _, err := sess.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			got := waitJob(t, f.m, info.ID, JobCompleted)
			if *got.Text != reference {
				t.Fatalf("resumed text %q, uninterrupted %q", *got.Text, reference)
			}
			// 3 units + 1 discarded attempt of unit 1.
			if n := f.mock.Transcribes.Load() - base; n != 4 || f.mock.Interrupted.Load() != 1 {
				t.Fatalf("transcribes %d interrupted %d", n, f.mock.Interrupted.Load())
			}
			evs, ok := collectJobEvents(sub, info.ID, JobCompleted, time.Second)
			sawRequeue := false
			for i := 1; i < len(evs); i++ {
				if evs[i-1].State == JobRunning && evs[i].State == JobQueued && evs[i].Preemptions == 1 {
					sawRequeue = true
				}
			}
			if !ok || !sawRequeue {
				t.Fatalf("job_progress events %+v", evs)
			}
		})
	}
}

func TestHighVoiceRunsBetweenJobSegments(t *testing.T) {
	f := newJobFixture(t, `{"engine":"qwen3-asr-0.6b"}`, 3*sr)
	ctx := context.Background()
	_ = f.m.Load(ctx)
	f.mock.WorkPerSecond.Store(int64(100 * time.Millisecond))
	info, err := f.m.SubmitJob(JobSpec{PCM: voiced(2*sr, 2*sr, 2*sr)})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "unit 0 running", func() bool { return f.mock.Transcribes.Load() == 1 })
	res, err := f.m.Transcribe(ctx, make([]int16, sr), asr.TranscribeOptions{}, "")
	if err != nil || res.Text != "字" {
		t.Fatal(res, err)
	}
	j, _ := f.m.Job(info.ID)
	if j.State == JobCompleted || j.SegmentsDone == 0 {
		t.Fatalf("high voice transcription should run at the next unit boundary: %+v", j)
	}
	got := waitJob(t, f.m, info.ID, JobCompleted)
	st := f.m.Status().Scheduler
	if got.Preemptions != 0 || st.Preemptions != 0 || st.Yields == 0 || f.mock.Interrupted.Load() != 0 {
		t.Fatalf("high must not preempt: %+v %+v", got, st)
	}
}

func TestJobCancel(t *testing.T) {
	f := newJobFixture(t, `{"engine":"qwen3-asr-0.6b"}`, 3*sr)
	ctx := context.Background()
	_ = f.m.Load(ctx)

	// Queued behind a High task.
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = f.m.Scheduler().Do(ctx, scheduler.High, func() error { close(started); <-release; return nil })
	}()
	<-started
	queued, _ := f.m.SubmitJob(JobSpec{PCM: voiced(sr)})
	eventually(t, "job queued", func() bool { return f.m.Scheduler().Stats().NormalQueued == 1 })
	if info, err := f.m.CancelJob(ctx, queued.ID); err != nil || info.State != JobCancelled {
		t.Fatalf("%+v %v", info, err)
	}
	close(release)

	// Running: the unit in flight is interrupted.
	f.mock.WorkPerSecond.Store(int64(5 * time.Second))
	running, _ := f.m.SubmitJob(JobSpec{PCM: voiced(2 * sr)})
	eventually(t, "job running", func() bool {
		j, _ := f.m.Job(running.ID)
		return j.State == JobRunning && f.mock.Transcribes.Load() == 1
	})
	t0 := time.Now()
	info, err := f.m.CancelJob(ctx, running.ID)
	if err != nil || info.State != JobCancelled || time.Since(t0) > time.Second {
		t.Fatalf("%+v %v %v", info, err, time.Since(t0))
	}
	if f.mock.Interrupted.Load() != 1 || f.m.Status().Scheduler.Preemptions != 0 {
		t.Fatal("cancel must interrupt, not count as a preemption")
	}
	// Deleting a finished job removes it.
	if _, err := f.m.CancelJob(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Job(running.ID); !errors.Is(err, ErrJobNotFound) {
		t.Fatal(err)
	}
	if _, err := f.m.CancelJob(ctx, "nope"); !errors.Is(err, ErrJobNotFound) {
		t.Fatal(err)
	}
}

func TestR2T2JobWaitsForLiveSession(t *testing.T) {
	f := newJobFixture(t, `{"engine":"confucius4-r2t2"}`, 3*sr)
	ctx := context.Background()
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 4})
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.m.SubmitJob(JobSpec{PCM: voiced(2*sr, sr)})
	if err != nil {
		t.Fatal(err) // no 409: segmented jobs queue and yield instead
	}
	if _, err := sess.Push(ctx, 0, make([]int16, 2560)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if j, _ := f.m.Job(info.ID); j.State != JobQueued || f.mock.Transcribes.Load() != 0 {
		t.Fatalf("R2T2 job ran during the live session: %+v", j)
	}
	sess.Cancel()
	got := waitJob(t, f.m, info.ID, JobCompleted)
	if want := r2t2.JoinPieces([]string{"字字", "字"}); *got.Text != want {
		t.Fatalf("%q", *got.Text)
	}
}
