// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/scheduler"
)

type fixture struct {
	m     *Manager
	mock  *asr.Mock
	store *config.Store
	hub   *events.Hub
}

func newFixture(t *testing.T, engineJSON string) *fixture {
	t.Helper()
	p := filepath.Join(t.TempDir(), "engine.json")
	if engineJSON != "" {
		if err := os.WriteFile(p, []byte(engineJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := config.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{mock: asr.NewMock(), store: store, hub: events.NewHub()}
	f.m = New(Options{Store: store, Backend: f.mock, Hub: f.hub,
		IdleTick: 5 * time.Millisecond, IdleUnit: 10 * time.Millisecond})
	t.Cleanup(func() { _ = f.m.Close(context.Background()); f.hub.Close() })
	return f
}

func (f *fixture) patch(t *testing.T, kv map[string]any) config.PatchResult {
	t.Helper()
	snap, _ := f.store.Snapshot()
	p := map[string]json.RawMessage{}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		p[k] = b
	}
	res, err := f.store.Patch(snap.Revision, p)
	if err != nil {
		t.Fatal(err)
	}
	f.m.OnConfigPatched(res)
	return res
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestGPUIdleUnloadAndReloadOnDemand(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b","device":"cuda","gpu_idle_seconds":3}`)
	ctx := context.Background()
	if err := f.m.Load(ctx); err != nil {
		t.Fatal(err)
	}
	// 3 units of 10ms -> unloads after ~30ms of inactivity.
	eventually(t, "idle unload", func() bool { st := f.m.Status(); return !st.ModelLoaded && st.IdleUnloaded })

	// Next request reloads transparently.
	res, err := f.m.Transcribe(ctx, make([]int16, 2*asr.SampleRate), asr.TranscribeOptions{})
	if err != nil || res.Text != "字字" {
		t.Fatalf("transcribe after idle unload: %q %v", res.Text, err)
	}
	if f.mock.Loads.Load() != 2 {
		t.Fatalf("loads = %d", f.mock.Loads.Load())
	}
}

func TestGPUIdleIsLiveAndSkipsCPUAndSessions(t *testing.T) {
	f := newFixture(t, `{"engine":"confucius4-r2t2","device":"cpu","gpu_idle_seconds":1}`)
	ctx := context.Background()
	_ = f.m.Load(ctx)
	time.Sleep(60 * time.Millisecond)
	if !f.m.Status().ModelLoaded {
		t.Fatal("CPU model must not be idle-unloaded")
	}

	// Switch to CUDA (reload key), disable idle (live key), hold a session.
	f.patch(t, map[string]any{"device": "cuda", "gpu_idle_seconds": 0})
	if _, err := f.m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if st := f.m.Status(); st.Device != "cuda" || !st.ModelLoaded {
		t.Fatalf("after reload: %+v", st)
	}
	time.Sleep(40 * time.Millisecond)
	if !f.m.Status().ModelLoaded {
		t.Fatal("idle unload while disabled")
	}
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	f.patch(t, map[string]any{"gpu_idle_seconds": 1}) // live: takes effect without reload
	time.Sleep(60 * time.Millisecond)
	if !f.m.Status().ModelLoaded {
		t.Fatal("unloaded during an active realtime session")
	}
	sess.Cancel()
	eventually(t, "idle unload after session", func() bool { return !f.m.Status().ModelLoaded })
}

func TestReloadAndUnloadRefusedDuringSession(t *testing.T) {
	f := newFixture(t, `{"engine":"confucius4-r2t2"}`)
	ctx := context.Background()
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 8}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second session: %v", err)
	}
	if _, err := f.m.Reload(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("reload: %v", err)
	}
	if err := f.m.Unload(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("unload: %v", err)
	}
	if _, err := sess.Push(ctx, 5, make([]int16, 10)); err == nil {
		t.Fatal("offset mismatch accepted")
	}
	p, err := sess.Push(ctx, 0, make([]int16, asr.SampleRate+10))
	if err != nil || p.Committed != "字" || p.Tentative == "" {
		t.Fatalf("push: %+v %v", p, err)
	}
	res, err := sess.Finish(ctx)
	if err != nil || res.SampleCount != asr.SampleRate+10 || res.Text != "字字" {
		t.Fatalf("finish: %+v %v", res, err)
	}
	if f.m.SessionActive() {
		t.Fatal("slot not released")
	}
	if _, err := f.m.Reload(ctx); err != nil {
		t.Fatalf("reload after session: %v", err)
	}
}

func TestR2T2BatchTranscribe(t *testing.T) {
	f := newFixture(t, `{"engine":"confucius4-r2t2","default_language":"zh"}`)
	ctx := context.Background()
	res, err := f.m.Transcribe(ctx, make([]int16, 2*asr.SampleRate+1), asr.TranscribeOptions{})
	if err != nil || res.Text != "字字字" || res.Language != "zh" {
		t.Fatalf("R2T2 transcribe: %+v %v", res, err)
	}

	// While a live R2T2 session holds the shared model, batch R2T2 is refused
	// (Python: stream_busy) instead of interleaving with the session.
	sess, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Transcribe(ctx, make([]int16, 10), asr.TranscribeOptions{}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("R2T2 batch during session: %v", err)
	}
	sess.Cancel()
	if _, err := f.m.Transcribe(ctx, make([]int16, 10), asr.TranscribeOptions{}); err != nil {
		t.Fatalf("after session: %v", err)
	}
}

// A batch R2T2 job queued behind other work must not run once a live
// session has started in the meantime (realtime goes first; the batch job
// re-checks on the inference thread).
func TestR2T2BatchQueuedThenSessionStarts(t *testing.T) {
	f := newFixture(t, `{"engine":"confucius4-r2t2"}`)
	ctx := context.Background()
	_ = f.m.Load(ctx)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = f.m.Scheduler().Do(ctx, scheduler.Batch, func() error { close(started); <-release; return nil })
	}()
	<-started
	errc := make(chan error, 1)
	go func() {
		_, err := f.m.Transcribe(ctx, make([]int16, 10), asr.TranscribeOptions{})
		errc <- err
	}()
	eventually(t, "batch queued", func() bool { return f.m.Scheduler().Stats().BatchQueued == 1 })
	sessc := make(chan *Session, 1)
	go func() {
		s, err := f.m.StartSession(ctx, asr.StreamOptions{SessionID: 2})
		if err != nil {
			t.Error(err)
		}
		sessc <- s
	}()
	eventually(t, "session slot held", f.m.SessionActive)
	close(release)
	sess := <-sessc
	if err := <-errc; !errors.Is(err, ErrSessionActive) {
		t.Fatalf("queued R2T2 batch ran during session: %v", err)
	}
	if sess != nil {
		sess.Cancel()
	}
}

func TestEngineKindRules(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b"}`)
	ctx := context.Background()
	if _, err := f.m.StartSession(ctx, asr.StreamOptions{}); !errors.Is(err, ErrWrongEngine) {
		t.Fatalf("stream on qwen3 must be refused (no silent fallback): %v", err)
	}
	f.patch(t, map[string]any{"engine": "glm-asr"})
	if _, err := f.m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	st := f.m.Status()
	if st.LocalEngine || st.ModelLoaded {
		t.Fatalf("cloud engine must not load a local model: %+v", st)
	}
	if _, err := f.m.Transcribe(ctx, make([]int16, 10), asr.TranscribeOptions{}); !errors.Is(err, ErrNoLocalEngine) {
		t.Fatalf("transcribe with cloud engine: %v", err)
	}
	if err := f.m.Load(ctx); !errors.Is(err, ErrNoLocalEngine) {
		t.Fatalf("load with cloud engine: %v", err)
	}
}

func TestPendingReloadAndEvents(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b"}`)
	sub := f.hub.Subscribe(64)
	defer sub.Cancel()
	ctx := context.Background()
	_ = f.m.Load(ctx)

	f.patch(t, map[string]any{"engine": "confucius4-r2t2", "default_language": "zh"})
	if st := f.m.Status(); len(st.PendingReload) != 1 || st.PendingReload[0] != "engine" || st.Kind != asr.KindQwen3 {
		t.Fatalf("pending before reload: %+v", st)
	}
	applied, err := f.m.Reload(ctx)
	if err != nil || len(applied) != 1 || applied[0] != "engine" {
		t.Fatalf("reload applied %v err %v", applied, err)
	}
	if st := f.m.Status(); st.Kind != asr.KindR2T2 || len(st.PendingReload) != 0 {
		t.Fatalf("after reload: %+v", st)
	}

	sawConfig := false
	timeout := time.After(time.Second)
	for !sawConfig {
		select {
		case ev := <-sub.C:
			if ev.Type == events.TypeConfigChanged {
				d := ev.Data.(map[string]any)
				if d["pending_reload"].([]string)[0] != "engine" {
					t.Fatalf("config_changed payload %v", d)
				}
				sawConfig = true
			}
		case <-timeout:
			t.Fatal("no config_changed event")
		}
	}
}

func TestLoadFailureReported(t *testing.T) {
	f := newFixture(t, `{"engine":"qwen3-asr-0.6b"}`)
	f.mock.FailLoad.Store(true)
	if err := f.m.Load(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	if st := f.m.Status(); st.LastError == "" || st.ModelLoaded {
		t.Fatalf("status %+v", st)
	}
	if err := f.m.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.m.Status(); st.LastError != "" || !st.ModelLoaded {
		t.Fatalf("status %+v", st)
	}
}
