// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// fakeLib / fakeSession stand in for transcribe.cpp.
type fakeLib struct {
	mu      sync.Mutex
	opens   []string // backends tried
	configs []SessionConfig
	fail    map[string]error
	text    string
	lang    string
	// blockUntilAbort makes Run wait for the abort flag (or 2 s).
	blockUntilAbort bool
	sessions        []*fakeSession
}

func (l *fakeLib) Version() string { return "0.1.3" }

func (l *fakeLib) Open(modelPath, backend string, cfg SessionConfig) (Session, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.opens = append(l.opens, backend)
	l.configs = append(l.configs, cfg)
	if err := l.fail[backend]; err != nil {
		return nil, err
	}
	s := &fakeSession{lib: l, backend: backend}
	if backend == "auto" {
		s.backend = "cpu"
	}
	l.sessions = append(l.sessions, s)
	return s, nil
}

type fakeSession struct {
	lib     *fakeLib
	backend string
	inputs  [][]float32
	abort   atomic.Bool
	aborts  atomic.Int32 // times the flag was raised
	closed  bool
}

func (s *fakeSession) Backend() string { return s.backend }

func (s *fakeSession) Run(pcm []float32) (string, string, error) {
	s.inputs = append(s.inputs, append([]float32(nil), pcm...))
	if s.lib.blockUntilAbort {
		deadline := time.Now().Add(2 * time.Second)
		for !s.abort.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	if s.abort.Load() {
		return "", "", &StatusError{Op: "transcribe_run", Status: StatusAborted, Message: "aborted"}
	}
	return s.lib.text, s.lib.lang, nil
}

func (s *fakeSession) SetAbort(on bool) {
	if on {
		s.aborts.Add(1)
	}
	s.abort.Store(on)
}

func (s *fakeSession) Close() { s.closed = true }

// fakeVAD returns fixed chunks (like the Python FakeVad), or, when chunks
// is nil and energy is set, the runs of non-zero samples.
type fakeVAD struct {
	chunks []vad.Segment
	energy bool
	calls  [][]float32
	closed bool
	warm   int
}

func (v *fakeVAD) SpeechTimestamps(audio []float32) ([]vad.Segment, error) {
	v.calls = append(v.calls, audio)
	if !v.energy {
		return v.chunks, nil
	}
	var out []vad.Segment
	start := -1
	for i, x := range audio {
		switch {
		case x != 0 && start < 0:
			start = i
		case x == 0 && start >= 0:
			out = append(out, vad.Segment{Start: start, End: i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, vad.Segment{Start: start, End: len(audio)})
	}
	return out, nil
}

func (v *fakeVAD) Warmup() error { v.warm++; return nil }
func (v *fakeVAD) Close() error  { v.closed = true; return nil }
