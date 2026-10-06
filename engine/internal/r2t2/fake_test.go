// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package r2t2

import (
	"errors"
	"math"
	"slices"

	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// fakeLib is a scriptable CLib: onPush decides each push's event.
type fakeLib struct {
	abi       uint32
	openErr   error
	opened    []SessionConfig
	starts    [][2]string
	pushes    []fakePush
	onPush    func(pcm []float32, offset int64) (PushEvent, error)
	finish    FinishResult
	finishErr error
	finishes  int
	resets    int
	resetErr  error
	startErr  error
	closed    int
	calls     []string
}

type fakePush struct {
	offset int64
	pcm    []float32
}

func newFakeLib() *fakeLib { return &fakeLib{abi: 0x0400} }

func (f *fakeLib) ABIVersion() uint32 { f.calls = append(f.calls, "abi_version"); return f.abi }

func (f *fakeLib) Open(cfg SessionConfig) error {
	f.calls = append(f.calls, "open")
	f.opened = append(f.opened, cfg)
	return f.openErr
}

func (f *fakeLib) StreamStart(context, language string) error {
	f.calls = append(f.calls, "stream_start")
	f.starts = append(f.starts, [2]string{context, language})
	return f.startErr
}

func (f *fakeLib) StreamPush(pcm []float32, offset int64) (PushEvent, error) {
	f.calls = append(f.calls, "stream_push")
	f.pushes = append(f.pushes, fakePush{offset, slices.Clone(pcm)})
	if f.onPush == nil {
		return PushEvent{}, nil
	}
	return f.onPush(pcm, offset)
}

func (f *fakeLib) StreamFinish() (FinishResult, error) {
	f.calls = append(f.calls, "stream_finish")
	f.finishes++
	return f.finish, f.finishErr
}

func (f *fakeLib) StreamReset() error {
	f.calls = append(f.calls, "stream_reset")
	f.resets++
	return f.resetErr
}

func (f *fakeLib) Close() { f.calls = append(f.calls, "close"); f.closed++ }

func (f *fakeLib) pushedSamples() int {
	n := 0
	for _, p := range f.pushes {
		n += len(p.pcm)
	}
	return n
}

// activeRuntime builds a started Runtime over lib without running Open.
func activeRuntime(lib *fakeLib, chunkSamples int, batch bool) *Runtime {
	return &Runtime{lib: lib, backend: "cuda", rolling: true, chunkSamples: chunkSamples,
		batchInitialAudio: batch, open: true, active: true}
}

// recordingNative mirrors RecordingNative in test_r2t2_segmented.py.
type recordingNative struct {
	events        []string
	startCalls    [][2]string
	feedInputs    [][]float32
	finishCalls   int
	resetCalls    int
	feedResults   [][2]string
	finishResults [][2]string
	feedErrors    []error
	previews      []string // optional per-feed previews (PreviewNative)
	preview       string
}

func (n *recordingNative) Start(context, language string) error {
	n.events = append(n.events, "start")
	n.startCalls = append(n.startCalls, [2]string{context, language})
	return nil
}

func (n *recordingNative) Feed(pcm []float32) (string, string, error) {
	n.events = append(n.events, "feed")
	n.feedInputs = append(n.feedInputs, slices.Clone(pcm))
	if len(n.feedErrors) > 0 {
		err := n.feedErrors[0]
		n.feedErrors = n.feedErrors[1:]
		return "", "", err
	}
	if len(n.feedResults) == 0 {
		return "", "", errors.New("recordingNative: no feed result left")
	}
	r := n.feedResults[0]
	n.feedResults = n.feedResults[1:]
	if len(n.previews) > 0 {
		n.preview = n.previews[0]
		n.previews = n.previews[1:]
	}
	return r[0], r[1], nil
}

func (n *recordingNative) Finish() (string, string, error) {
	n.events = append(n.events, "finish")
	n.finishCalls++
	if len(n.finishResults) == 0 {
		return "", "", errors.New("recordingNative: no finish result left")
	}
	r := n.finishResults[0]
	n.finishResults = n.finishResults[1:]
	return r[0], r[1], nil
}

func (n *recordingNative) Reset() error {
	n.events = append(n.events, "reset")
	n.resetCalls++
	return nil
}

// previewNative adds PreviewText (PreviewNative in test_r2t2_preview.py).
type previewNative struct{ recordingNative }

func (n *previewNative) PreviewText() string { return n.preview }

// markerVAD returns one region spanning the samples > 0.5 (MarkerVad).
type markerVAD struct{ inputs [][]float32 }

func (m *markerVAD) SpeechTimestamps(pcm []float32) ([]vad.Segment, error) {
	m.inputs = append(m.inputs, slices.Clone(pcm))
	first, last := -1, -1
	for i, x := range pcm {
		if x > 0.5 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil, nil
	}
	return []vad.Segment{{Start: first, End: last + 1}}, nil
}

// fixedVAD always returns the same regions.
type fixedVAD struct{ regions []vad.Segment }

func (f fixedVAD) SpeechTimestamps([]float32) ([]vad.Segment, error) { return f.regions, nil }

func audio(values ...float32) []float32 { return values }

func ones(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }
