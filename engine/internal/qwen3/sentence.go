// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package qwen3

import (
	"errors"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/r2t2"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// SentenceOptions tune the whole-sentence realtime mode (PLAN §3.0.2).
// Qwen3 has no native streaming: the stream is cut into sentences by the
// VAD and each sentence is recognized once, as a whole.
type SentenceOptions struct {
	// WindowSamples is the VAD window over the newest audio (1 s, like the
	// R2T2 segmenter).
	WindowSamples int
	// PauseSamples is the trailing silence (VAD padding included) that
	// ends a sentence.
	PauseSamples int
	// MinSentenceSamples: a pause ends a sentence only once the pending
	// audio is at least this long.
	MinSentenceSamples int
	// MaxSentenceSamples forces a cut during uninterrupted speech so
	// captions keep coming and one recognition stays bounded.
	MaxSentenceSamples int
	// PrerollSamples of audio are kept while no speech is detected, so the
	// start of a sentence the VAD flags late is not lost (≥ WindowSamples).
	PrerollSamples int
}

// DefaultSentenceOptions: 1 s window, 500 ms pause, 1 s minimum, 30 s
// maximum, 1 s preroll.
func DefaultSentenceOptions() SentenceOptions {
	return SentenceOptions{
		WindowSamples:      asr.SampleRate,
		PauseSamples:       asr.SampleRate / 2,
		MinSentenceSamples: asr.SampleRate,
		MaxSentenceSamples: 30 * asr.SampleRate,
		PrerollSamples:     asr.SampleRate,
	}
}

// Recognizer recognizes one sentence (the whole-clip path: short-clip and
// VAD filters, then one transcribe.cpp run).
type Recognizer func(audio []float32) (text, language string, err error)

// SentenceStream implements asr.Stream for Qwen3. Partial.Committed grows
// by whole sentences; Partial.Tentative is always empty. Every method runs
// on the inference thread, so the recognition of a finished sentence
// happens inside the Push that ended it (PLAN §4.6: no VAD / inference
// overlap).
type SentenceStream struct {
	opts      SentenceOptions
	vad       Detector
	recognize Recognizer

	pending   []float32 // audio not yet recognized (current sentence + preroll)
	inSpeech  bool
	sentences []string
	language  string
	samples   int
	done      bool
}

// NewSentenceStream starts an empty whole-sentence session.
func NewSentenceStream(detector Detector, recognize Recognizer, opts SentenceOptions) *SentenceStream {
	if opts.PrerollSamples < opts.WindowSamples {
		opts.PrerollSamples = opts.WindowSamples
	}
	return &SentenceStream{opts: opts, vad: detector, recognize: recognize}
}

func trailingSilence(window int, regions []vad.Segment) int {
	last := 0
	for _, r := range regions {
		last = max(last, min(window, r.End))
	}
	return window - last
}

// Committed is the text of every recognized sentence, joined like the R2T2
// segments (a space only between ASCII words).
func (s *SentenceStream) Committed() string { return r2t2.JoinPieces(s.sentences) }

func (s *SentenceStream) commit() error {
	audio := s.pending
	s.pending = nil
	s.inSpeech = false
	if len(audio) == 0 {
		return nil
	}
	text, language, err := s.recognize(audio)
	if err != nil {
		return err
	}
	if text != "" {
		s.sentences = append(s.sentences, text)
	}
	if language != "" && language != "unknown" {
		s.language = language
	}
	return nil
}

// Push appends audio, runs the VAD over the newest window and, when a
// sentence has ended (trailing pause after enough audio, or the maximum
// length), recognizes it and appends it to Committed.
func (s *SentenceStream) Push(pcm []int16) (asr.Partial, error) {
	if s.done {
		return asr.Partial{}, errors.New("qwen3: sentence stream finished")
	}
	s.samples += len(pcm)
	s.pending = append(s.pending, vad.PCM16ToFloat(pcm)...)
	window := s.pending[max(0, len(s.pending)-s.opts.WindowSamples):]
	regions, err := s.vad.SpeechTimestamps(append([]float32(nil), window...))
	if err != nil {
		return asr.Partial{}, err
	}
	if !s.inSpeech {
		if len(regions) == 0 {
			if extra := len(s.pending) - s.opts.PrerollSamples; extra > 0 {
				s.pending = append(s.pending[:0:0], s.pending[extra:]...)
			}
			return asr.Partial{Committed: s.Committed()}, nil
		}
		s.inSpeech = true
	}
	ended := trailingSilence(len(window), regions) >= s.opts.PauseSamples && len(s.pending) >= s.opts.MinSentenceSamples
	if ended || len(s.pending) >= s.opts.MaxSentenceSamples {
		if err := s.commit(); err != nil {
			return asr.Partial{}, err
		}
	}
	return asr.Partial{Committed: s.Committed()}, nil
}

// Finish recognizes whatever is pending and returns the full text.
func (s *SentenceStream) Finish() (asr.Result, error) {
	if s.done {
		return asr.Result{}, errors.New("qwen3: sentence stream finished")
	}
	s.done = true
	if err := s.commit(); err != nil {
		return asr.Result{}, err
	}
	language := s.language
	if language == "" {
		language = "unknown"
	}
	return asr.Result{Text: s.Committed(), Language: language, SampleCount: s.samples}, nil
}

// Close drops the session.
func (s *SentenceStream) Close() {
	s.done = true
	s.pending = nil
}
