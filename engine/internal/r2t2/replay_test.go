// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package r2t2

import "testing"

// TestReplayPythonTraces drives the Go backend (session, segmentation,
// runtime, context composition, PCM conversion) with audio.cpp and the VAD
// answered from Python reference traces, and requires the identical native
// call sequence and identical responses, final text and transcription.
func TestReplayPythonTraces(t *testing.T) {
	traces := loadTraces(t, "testdata")
	if len(traces) == 0 {
		t.Fatal("no testdata/*.trace.json (run testdata/gen_reference.py)")
	}
	for _, dir := range extraTraceDirs() {
		traces = append(traces, loadTraces(t, dir)...)
	}
	for _, tr := range traces {
		t.Run(tr.Name, func(t *testing.T) {
			c := &checker{}
			b := newTraceBackend(t, c, tr.Backend, tr.Threads)
			defer b.Unload()
			if got := b.native.ChunkSamples(); got != tr.ChunkSamples {
				t.Fatalf("chunk %d, Python %d", got, tr.ChunkSamples)
			}
			s := runStream(t, b, c, tr, 1)
			x := runTranscribe(t, b, c, tr)
			t.Logf("stream: %d responses, %d pushes, %d VAD calls, final %q; transcribe: %d pushes, %d VAD calls",
				s.responses, s.pushes, s.vadCalls, tr.Stream.Final.Text, x.pushes, x.vadCalls)
		})
	}
}
