// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of FireRedVadOptions and FireRedVad._timestamps_from_probabilities
// from src-tauri/resources/firered_vad.py (inherited from the upstream
// light-whisper project, GPL-3.0-only; that file is itself adapted from
// FireRedTeam/FireRedVAD, Apache-2.0 — see THIRD_PARTY_NOTICES.md and
// src-tauri/resources/FireRedVAD-LICENSE.txt).
//
// Per PLAN §10.4, logic translated from inherited GPL code is not labelled
// as newly written AGPL code; this file stays GPL-3.0-only (combinable with
// the AGPL-3.0-only engine, PLAN §10.3).

package vad

import "fmt"

// Options mirrors FireRedVadOptions.
type Options struct {
	Threshold            float32
	SmoothWindowFrames   int
	MinSpeechDurationMs  int
	MinSilenceDurationMs int
	SpeechPadMs          int
}

// DefaultOptions are the values used by the Python engines.
func DefaultOptions() Options {
	return Options{
		Threshold:            0.5,
		SmoothWindowFrames:   5,
		MinSpeechDurationMs:  150,
		MinSilenceDurationMs: 300,
		SpeechPadMs:          120,
	}
}

// Validate rejects values Python would never be configured with.
func (o Options) Validate() error {
	if o.MinSpeechDurationMs < 0 || o.MinSilenceDurationMs < 0 {
		return fmt.Errorf("vad: durations must be >= 0")
	}
	if o.Threshold != o.Threshold { // NaN
		return fmt.Errorf("vad: threshold is NaN")
	}
	return nil
}

// Smooth applies the causal moving average used by Python:
// np.convolve(p, ones(w)/w, "full")[:n], with the first w-1 values replaced
// by the mean of the available prefix. float32 throughout.
func Smooth(probs []float32, window int) []float32 {
	window = max(1, window)
	out := make([]float32, len(probs))
	if window == 1 {
		copy(out, probs)
		return out
	}
	kernel := float32(1) / float32(window)
	for i := range probs {
		if i < window-1 {
			var sum float32
			for _, p := range probs[:i+1] {
				sum += p
			}
			out[i] = sum / float32(i+1)
			continue
		}
		var acc float32
		for _, p := range probs[i-window+1 : i+1] {
			acc += float32(p * kernel)
		}
		out[i] = acc
	}
	return out
}

// TimestampsFromProbabilities turns per-frame probabilities into padded,
// merged speech segments in samples (audioLength bounds the last end).
func TimestampsFromProbabilities(probs []float32, audioLength int, opts Options) []Segment {
	if len(probs) == 0 {
		return []Segment{}
	}
	smoothed := Smooth(probs, opts.SmoothWindowFrames)
	minSpeech := max(1, opts.MinSpeechDurationMs/10)
	minSilence := max(1, opts.MinSilenceDurationMs/10)
	pad := max(0, opts.SpeechPadMs*SampleRate/1000)

	type span struct{ start, end int }
	var spans []span
	candidate, speech, silence := -1, -1, -1
	for i, p := range smoothed {
		isSpeech := p >= opts.Threshold
		if speech < 0 {
			if isSpeech {
				if candidate < 0 {
					candidate = i
				}
				if i-candidate+1 >= minSpeech {
					speech = candidate
					silence = -1
				}
			} else {
				candidate = -1
			}
			continue
		}
		if isSpeech {
			silence = -1
			continue
		}
		if silence < 0 {
			silence = i
			continue
		}
		if i-silence+1 >= minSilence {
			spans = append(spans, span{speech, silence})
			speech, candidate, silence = -1, -1, -1
		}
	}
	if speech >= 0 {
		spans = append(spans, span{speech, len(probs)})
	}

	out := []Segment{}
	for _, s := range spans {
		start := max(0, s.start*FrameShiftSamples-pad)
		end := min(audioLength, s.end*FrameShiftSamples+pad)
		if end <= start {
			continue
		}
		if n := len(out); n > 0 && start <= out[n-1].End {
			out[n-1].End = max(out[n-1].End, end)
		} else {
			out = append(out, Segment{Start: start, End: end})
		}
	}
	return out
}
