// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of SegmentedR2T2 from src-tauri/resources/r2t2_segmented.py
// (inherited light-whisper code, GPL-3.0-only): the outer VAD segmentation
// around the native stream. Audio is processed in native-sized blocks; a
// segment starts when the (≤ 1 s) VAD window contains speech, and ends once
// it is at least 8 s long and the window ends in ≥ 300 ms of silence (no
// capacity cut in production; the native 16 s rolling window owns capacity).
// Each segment restarts the native stream with the same context / language;
// finished segment texts are joined with an ASCII-aware space.
//
// Per PLAN §10.4, logic translated from inherited GPL code is not labelled
// as newly written AGPL code; this file stays GPL-3.0-only (combinable with
// the AGPL-3.0-only engine, PLAN §10.3).

package r2t2

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// Native is the stream runtime SegmentedR2T2 drives ([Runtime] in
// production). Language "" stands for Python's None.
type Native interface {
	Start(context, language string) error
	Feed(pcm []float32) (text, language string, err error)
	Finish() (text, language string, err error)
	Reset() error
}

// Previewer is implemented by runtimes that expose a revisable preview.
// Runtimes without it report the committed text as their preview (legacy
// runtimes in the Python tests).
type Previewer interface {
	PreviewText() string
}

// SpeechDetector finds speech regions (FireRedVAD in production).
type SpeechDetector interface {
	SpeechTimestamps(audio []float32) ([]vad.Segment, error)
}

// SegmentOptions mirror SegmentedR2T2's keyword arguments.
type SegmentOptions struct {
	ChunkSamples      int // block size (the native chunk)
	WindowSamples     int // VAD window
	MinSegmentSamples int // a pause can end a segment only after this many samples
	// MaxSegmentSamples forces a segment end; 0 = None (production).
	MaxSegmentSamples int
	PauseSamples      int // trailing silence that ends a segment
}

// DefaultSegmentOptions are the production values: 1 s VAD window, 8 s
// minimum segment, 300 ms pause, no maximum. ChunkSamples is the native
// chunk (5120 = 320 ms CPU, 2560 = 160 ms CUDA).
func DefaultSegmentOptions(chunkSamples int) SegmentOptions {
	return SegmentOptions{
		ChunkSamples:      chunkSamples,
		WindowSamples:     16000,
		MinSegmentSamples: 128000,
		MaxSegmentSamples: 0,
		PauseSamples:      4800,
	}
}

// Segmented keeps native streaming bounded while preserving its committed
// text (SegmentedR2T2).
type Segmented struct {
	native Native
	vad    SpeechDetector
	opts   SegmentOptions

	active         bool
	context        string
	startLanguage  string
	language       string
	pending        []float32
	vadTail        []float32
	nativeActive   bool
	segmentSamples int
	segmentText    string
	segmentPreview string
	segments       []string
	preview        string
}

// NewSegmented wraps native with VAD segmentation.
func NewSegmented(native Native, detector SpeechDetector, opts SegmentOptions) *Segmented {
	s := &Segmented{native: native, vad: detector, opts: opts}
	s.clearState()
	return s
}

func (s *Segmented) clearState() {
	s.active = false
	s.context = ""
	s.startLanguage = ""
	s.language = ""
	s.pending = nil
	s.vadTail = nil
	s.nativeActive = false
	s.segmentSamples = 0
	s.segmentText = ""
	s.segmentPreview = ""
	s.segments = nil
	s.preview = ""
}

func (s *Segmented) invalidateAfterError() {
	s.clearState()
	_ = s.native.Reset()
}

func (s *Segmented) requireActive() error {
	if !s.active {
		return invalid("segmented stream is not active")
	}
	return nil
}

func (s *Segmented) rememberLanguage(language string) {
	if language != "" {
		s.language = language
	}
}

// PreviewText is the joined text with the current segment's preview.
func (s *Segmented) PreviewText() string { return s.preview }

func (s *Segmented) nativePreview(committed string) (string, error) {
	preview := committed
	if p, ok := s.native.(Previewer); ok {
		preview = p.PreviewText()
	}
	if !strings.HasPrefix(preview, committed) {
		return "", errors.New("native runtime preview regressed")
	}
	return preview, nil
}

func isASCIIAlnum(c rune) bool {
	return ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9')
}

// joinedText joins finished segments and, when includeCurrent and a native
// segment is active, the given current piece.
func (s *Segmented) joinedText(includeCurrent bool, current string) string {
	pieces := append([]string(nil), s.segments...)
	if includeCurrent && s.nativeActive && current != "" {
		pieces = append(pieces, current)
	}
	return JoinPieces(pieces)
}

// JoinPieces joins recognized pieces like SegmentedR2T2._joined_text: a
// space is inserted only where an ASCII letter / digit (or ".!?:;") meets
// an ASCII letter / digit.
func JoinPieces(pieces []string) string {
	if len(pieces) == 0 {
		return ""
	}
	result := pieces[0]
	for _, piece := range pieces[1:] {
		if result != "" && piece != "" {
			last, _ := utf8.DecodeLastRuneInString(result)
			first, _ := utf8.DecodeRuneInString(piece)
			if (isASCIIAlnum(last) || strings.ContainsRune(".!?:;", last)) && isASCIIAlnum(first) {
				result += " "
			}
		}
		result += piece
	}
	return result
}

func (s *Segmented) refreshPreview() {
	s.preview = s.joinedText(true, s.segmentPreview)
}

func (s *Segmented) vadWindow(block []float32) []float32 {
	observed := make([]float32, 0, len(s.vadTail)+len(block))
	observed = append(observed, s.vadTail...)
	observed = append(observed, block...)
	limit := s.opts.WindowSamples
	if s.opts.MaxSegmentSamples != 0 {
		limit = min(limit, s.opts.MaxSegmentSamples)
	}
	if len(observed) > limit {
		observed = observed[len(observed)-limit:]
	}
	return append([]float32(nil), observed...)
}

func trailingSilence(windowSize int, regions []vad.Segment) int {
	if len(regions) == 0 {
		return windowSize
	}
	lastEnd := 0
	for _, r := range regions {
		lastEnd = max(lastEnd, min(windowSize, r.End))
	}
	return max(0, windowSize-lastEnd)
}

func (s *Segmented) observe(block []float32) ([]float32, []vad.Segment, int, error) {
	observed := s.vadWindow(block)
	regions, err := s.vad.SpeechTimestamps(append([]float32(nil), observed...))
	if err != nil {
		return nil, nil, 0, err
	}
	s.vadTail = observed
	return observed, regions, trailingSilence(len(observed), regions), nil
}

func (s *Segmented) nativeFeed(pcm []float32) error {
	previous := s.segmentText
	text, language, err := s.native.Feed(append([]float32(nil), pcm...))
	if err == nil && !strings.HasPrefix(text, previous) {
		err = errors.New("native runtime text regressed")
	}
	var preview string
	if err == nil {
		preview, err = s.nativePreview(text)
	}
	if err != nil {
		s.invalidateAfterError()
		return err
	}
	s.segmentText = text
	s.segmentPreview = preview
	s.rememberLanguage(language)
	s.refreshPreview()
	return nil
}

func (s *Segmented) startSegment(initial []float32) error {
	s.nativeActive = true
	s.segmentSamples = len(initial)
	s.segmentText = ""
	s.segmentPreview = ""
	if err := s.native.Start(s.context, s.startLanguage); err != nil {
		s.invalidateAfterError()
		return err
	}
	return s.nativeFeed(initial)
}

func (s *Segmented) finishSegment() error {
	previous := s.segmentText
	text, language, err := s.native.Finish()
	if err != nil {
		s.invalidateAfterError()
		return err
	}
	if !strings.HasPrefix(text, previous) {
		s.invalidateAfterError()
		return errors.New("native runtime text regressed")
	}
	s.rememberLanguage(language)
	if text != "" {
		s.segments = append(s.segments, text)
	}
	s.nativeActive = false
	s.segmentSamples = 0
	s.segmentText = ""
	s.segmentPreview = ""
	s.vadTail = nil
	s.refreshPreview()
	return nil
}

func (s *Segmented) processBlock(block []float32) error {
	var (
		observed        []float32
		regions         []vad.Segment
		trailingSilence int
	)
	if s.nativeActive && s.segmentSamples+len(block) < s.opts.MinSegmentSamples {
		// No pause can end this segment yet. Preserve the exact VAD window
		// for the first eligible boundary without recomputing its features.
		s.vadTail = s.vadWindow(block)
		observed = s.vadTail
	} else {
		var err error
		if observed, regions, trailingSilence, err = s.observe(block); err != nil {
			return err
		}
	}
	if !s.nativeActive {
		if len(regions) == 0 {
			return nil
		}
		initial := observed
		if s.opts.MaxSegmentSamples != 0 && len(initial) > s.opts.MaxSegmentSamples {
			initial = initial[len(initial)-s.opts.MaxSegmentSamples:]
		}
		if err := s.startSegment(initial); err != nil {
			return err
		}
	} else {
		if err := s.nativeFeed(block); err != nil {
			return err
		}
		s.segmentSamples += len(block)
	}
	if (s.opts.MaxSegmentSamples != 0 && s.segmentSamples >= s.opts.MaxSegmentSamples) ||
		(s.segmentSamples >= s.opts.MinSegmentSamples && trailingSilence >= s.opts.PauseSamples) {
		return s.finishSegment()
	}
	return nil
}

// Start resets the native runtime and begins a new segmented stream.
func (s *Segmented) Start(context, language string) error {
	s.clearState()
	if err := s.native.Reset(); err != nil {
		return err
	}
	s.active = true
	s.context = context
	s.startLanguage = language
	s.language = language
	return nil
}

// Feed processes every complete block and returns the joined committed text
// and the latest language.
func (s *Segmented) Feed(pcm []float32) (string, string, error) {
	if err := s.requireActive(); err != nil {
		return "", "", err
	}
	combined := make([]float32, 0, len(s.pending)+len(pcm))
	combined = append(combined, s.pending...)
	combined = append(combined, pcm...)
	full := len(combined) / s.opts.ChunkSamples * s.opts.ChunkSamples
	for start := 0; start < full; start += s.opts.ChunkSamples {
		if err := s.processBlock(combined[start : start+s.opts.ChunkSamples]); err != nil {
			return "", "", err
		}
	}
	s.pending = append([]float32(nil), combined[full:]...)
	return s.joinedText(true, s.segmentText), s.language, nil
}

// Finish processes the incomplete tail block, ends the active segment and
// returns the joined text.
func (s *Segmented) Finish() (string, string, error) {
	if err := s.requireActive(); err != nil {
		return "", "", err
	}
	pending := s.pending
	s.pending = nil
	if len(pending) != 0 {
		if err := s.processBlock(pending); err != nil {
			return "", "", err
		}
	}
	if s.nativeActive {
		if err := s.finishSegment(); err != nil {
			return "", "", err
		}
	}
	text := s.joinedText(false, "")
	language := s.language
	s.clearState()
	return text, language, nil
}

// Reset drops all state and resets the native runtime.
func (s *Segmented) Reset() error {
	s.clearState()
	return s.native.Reset()
}
