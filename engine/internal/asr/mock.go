// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package asr

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
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
	// WorkPerSecond simulates inference time per second of audio in
	// Transcribe (nanoseconds; 0 = instant). The simulated work stops early
	// with ErrInterrupted when TranscribeOptions.Interrupt closes, like the
	// native Qwen3 abort callback.
	WorkPerSecond atomic.Int64
	// Transcribes counts Transcribe calls; Interrupted counts the ones that
	// stopped on Interrupt.
	Transcribes atomic.Int64
	Interrupted atomic.Int64
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

func (m *Mock) work(n int, interrupt <-chan struct{}) error {
	d := time.Duration(m.WorkPerSecond.Load()) * time.Duration(n) / SampleRate
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-interrupt: // nil channel: never
		m.Interrupted.Add(1)
		return fmt.Errorf("%w: mock", ErrInterrupted)
	}
}

func (m *Mock) Transcribe(pcm []int16, opts TranscribeOptions) (Result, error) {
	if !m.loaded {
		return Result{}, ErrNotLoaded
	}
	m.Transcribes.Add(1)
	if err := m.work(len(pcm), opts.Interrupt); err != nil {
		return Result{}, err
	}
	switch m.spec.Kind {
	case KindQwen3:
		return Result{Text: MockText(len(pcm)), Language: opts.Language, SampleCount: len(pcm)}, nil
	case KindR2T2:
		// Same path as the native R2T2 batch: push the clip through a
		// fresh session in chunks, then finish (tail rounded up).
		st := &mockStream{opts: StreamOptions{Language: opts.Language}}
		const chunk = SampleRate * 320 / 1000
		for i := 0; i < len(pcm); i += chunk {
			if _, err := st.Push(pcm[i:min(i+chunk, len(pcm))]); err != nil {
				return Result{}, err
			}
		}
		return st.Finish()
	}
	return Result{}, ErrWrongKind
}

func (m *Mock) NewStream(opts StreamOptions) (Stream, error) {
	if !m.loaded {
		return nil, ErrNotLoaded
	}
	switch m.spec.Kind {
	case KindR2T2:
		return &mockStream{opts: opts}, nil
	case KindQwen3:
		return &mockSentenceStream{opts: opts}, nil
	}
	return nil, ErrWrongKind
}

// MockSpeechSegments is the mock VAD: runs of non-zero samples, merged
// across gaps shorter than 300 ms.
func MockSpeechSegments(pcm []int16) []Span {
	const gap = SampleRate * 300 / 1000
	var out []Span
	start := -1
	lastVoiced := -1
	for i, v := range pcm {
		if v == 0 {
			continue
		}
		if start >= 0 && i-lastVoiced-1 >= gap {
			out = append(out, Span{Start: start, End: lastVoiced + 1})
			start = -1
		}
		if start < 0 {
			start = i
		}
		lastVoiced = i
	}
	if start >= 0 {
		out = append(out, Span{Start: start, End: lastVoiced + 1})
	}
	return out
}

func (m *Mock) SpeechSegments(pcm []int16) ([]Span, error) {
	if !m.loaded {
		return nil, ErrNotLoaded
	}
	return MockSpeechSegments(pcm), nil
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

// mockSentenceStream mimics the Qwen3 whole-sentence mode: a sentence is a
// run of audio containing non-zero samples followed by at least 500 ms of
// zeros; it commits MockText of the sentence length (at least one "字").
// Tentative is always empty.
type mockSentenceStream struct {
	opts      StreamOptions
	samples   int
	sentence  int // samples in the open sentence (from its first voiced sample)
	silence   int // trailing zero samples
	committed string
	done      bool
}

func (s *mockSentenceStream) commitSentence() {
	n := s.sentence - s.silence
	if n <= 0 {
		return
	}
	text := MockText(n)
	if text == "" {
		text = "字"
	}
	s.committed += text
	s.sentence, s.silence = 0, 0
}

func (s *mockSentenceStream) Push(pcm []int16) (Partial, error) {
	if s.done {
		return Partial{}, fmt.Errorf("mock: stream finished")
	}
	s.samples += len(pcm)
	for _, v := range pcm {
		switch {
		case v != 0:
			s.sentence++
			s.silence = 0
		case s.sentence > 0:
			s.sentence++
			s.silence++
		}
	}
	if s.sentence > 0 && s.silence >= SampleRate/2 {
		s.commitSentence()
	}
	return Partial{Committed: s.committed}, nil
}

func (s *mockSentenceStream) Finish() (Result, error) {
	s.done = true
	s.commitSentence()
	return Result{Text: s.committed, Language: s.opts.Language, SampleCount: s.samples}, nil
}

func (s *mockSentenceStream) Close() { s.done = true }
