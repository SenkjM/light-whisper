// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package asr

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Mock is a deterministic stand-in for the native libraries. It is used for
// contract tests and for running the shell against the engine before the
// cgo bindings exist. Output depends only on the number of samples pushed.
type Mock struct {
	spec   LoadSpec
	loaded bool
	// Loads / Unloads count calls (read concurrently by tests).
	Loads   atomic.Int64
	Unloads atomic.Int64
	// FailLoad makes the next Load fail (tests).
	FailLoad atomic.Bool
}

// NewMock returns an unloaded mock backend.
func NewMock() *Mock { return &Mock{} }

func (m *Mock) Name() string { return "mock" }

func (m *Mock) Load(spec LoadSpec) error {
	if m.FailLoad.Swap(false) {
		return fmt.Errorf("mock: injected load failure")
	}
	m.spec = spec
	m.loaded = true
	m.Loads.Add(1)
	return nil
}

func (m *Mock) Unload() error {
	if m.loaded {
		m.Unloads.Add(1)
	}
	m.loaded = false
	return nil
}

func (m *Mock) device() string {
	if m.spec.Device == "" || m.spec.Device == "auto" {
		return "cpu"
	}
	return m.spec.Device
}

func (m *Mock) Info() Info {
	info := Info{MissingModels: []string{}, ModelLoaded: m.loaded}
	if m.loaded {
		info.Engine = m.spec.Engine
		info.Kind = m.spec.Kind
		info.Device = m.device()
		if info.Device != "cpu" {
			info.GPUName = "Mock GPU"
		}
	}
	return info
}

// MockText is the deterministic transcript for n samples: one "字" per full
// second of audio.
func MockText(n int) string { return strings.Repeat("字", n/SampleRate) }

func (m *Mock) Transcribe(pcm []int16, opts TranscribeOptions) (Result, error) {
	if !m.loaded {
		return Result{}, ErrNotLoaded
	}
	if m.spec.Kind != KindQwen3 {
		return Result{}, ErrWrongKind
	}
	return Result{Text: MockText(len(pcm)), Language: opts.Language, SampleCount: len(pcm)}, nil
}

func (m *Mock) NewStream(opts StreamOptions) (Stream, error) {
	if !m.loaded {
		return nil, ErrNotLoaded
	}
	if m.spec.Kind != KindR2T2 {
		return nil, ErrWrongKind
	}
	return &mockStream{opts: opts}, nil
}

type mockStream struct {
	opts    StreamOptions
	samples int
	done    bool
}

// Push: committed = one "字" per full second; tentative = "…" while a
// partial second is pending.
func (s *mockStream) Push(pcm []int16) (Partial, error) {
	if s.done {
		return Partial{}, fmt.Errorf("mock: stream finished")
	}
	s.samples += len(pcm)
	p := Partial{Committed: MockText(s.samples)}
	if s.samples%SampleRate != 0 {
		p.Tentative = "…"
	}
	return p, nil
}

func (s *mockStream) Finish() (Result, error) {
	s.done = true
	// The native finish pads 320 ms of zeros; the mock rounds the tail up.
	text := MockText(s.samples)
	if s.samples%SampleRate != 0 {
		text += "字"
	}
	return Result{Text: text, Language: s.opts.Language, SampleCount: s.samples}, nil
}

func (s *mockStream) Close() { s.done = true }
