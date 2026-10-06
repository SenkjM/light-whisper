// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"errors"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

func TestInterruptAbortsRunAndClearsFlag(t *testing.T) {
	h := newHarness(t, []vad.Segment{{Start: 0, End: 16000}})
	if err := h.b.Load(spec); err != nil {
		t.Fatal(err)
	}
	s := h.lib.sessions[0]
	h.lib.blockUntilAbort = true
	intr := make(chan struct{})
	go func() { time.Sleep(30 * time.Millisecond); close(intr) }()
	t0 := time.Now()
	_, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{Interrupt: intr})
	if !errors.Is(err, asr.ErrInterrupted) || time.Since(t0) > time.Second {
		t.Fatalf("got %v after %v", err, time.Since(t0))
	}
	if s.abort.Load() || s.aborts.Load() != 1 {
		t.Fatal("abort flag left raised")
	}
	// The next (realtime) call is not affected.
	h.lib.blockUntilAbort = false
	if res, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{}); err != nil || res.Text != "测试文本" {
		t.Fatal(res, err)
	}
	// An interrupt raised before the call skips the run entirely.
	closed := make(chan struct{})
	close(closed)
	runs := len(s.inputs)
	if _, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{Interrupt: closed}); !errors.Is(err, asr.ErrInterrupted) || len(s.inputs) != runs {
		t.Fatal(err)
	}
	// A run that completes before the interrupt keeps its result.
	late := make(chan struct{})
	if res, err := h.b.Transcribe(make([]int16, 16000), asr.TranscribeOptions{Interrupt: late}); err != nil || res.Text == "" {
		t.Fatal(res, err)
	}
	close(late)
	time.Sleep(5 * time.Millisecond)
	if s.abort.Load() {
		t.Fatal("late interrupt leaked into the session")
	}
}

func TestStatusErrorIsAborted(t *testing.T) {
	if !errors.Is(&StatusError{Status: StatusAborted}, ErrAborted) || errors.Is(&StatusError{Status: 1}, ErrAborted) {
		t.Fatal()
	}
}
