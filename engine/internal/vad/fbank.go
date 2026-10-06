// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only
//
// Pure-Go Kaldi filterbank features with the exact configuration the Python
// FireRedVAD runtime passes to kaldi-native-fbank 1.22.3. The computation
// follows kaldi-native-fbank's feature-window.cc, feature-fbank.cc,
// mel-computations.cc and feature-functions.cc step by step (float32
// arithmetic in the same order) so the features agree to float32 rounding
// noise. kaldi-native-fbank is Copyright (c) 2022 Xiaomi Corporation and
// licensed under the Apache License 2.0; only its algorithm is reproduced
// here (no code is linked or vendored).
//
// The real FFT is a float32 port of KISS FFT (kissfft.go, BSD-3-Clause), the
// library kaldi-native-fbank uses, and matches a plain build of it bit for
// bit. kaldi-native-fbank's GCC/Clang builds compile kissfft with
// -ffast-math, whose rounding depends on the compiler, so the features of the
// Linux wheel still differ from this port by float32 rounding noise.

package vad

import "math"

const (
	paddedWindowSize = 512 // round_to_power_of_two(400)
	numFFTBins       = paddedWindowSize / 2
	preemphCoeff     = float32(0.97)
	lowFreq          = float32(20)
	// float epsilon used by Kaldi's log flooring (std::numeric_limits<float>::epsilon()).
	floatEpsilon = float32(1.1920929e-07)
)

// Fbank computes 80-bin log-mel filterbank features with the FireRedVAD
// options: 16 kHz, 25 ms window, 10 ms shift, dither 0, remove_dc_offset,
// preemphasis 0.97, povey window, snip_edges, 512-point FFT, power spectrum,
// mel bins over [20 Hz, 8 kHz] (Kaldi mel scale), natural log, no energy.
type Fbank struct {
	window [FrameLengthSamples]float32
	bins   [NumMelBins]melBin
	fft    *kissFFTR
	spec   [numFFTBins + 1]cpx
}

type melBin struct {
	offset  int
	weights []float32
}

// NewFbank precomputes the window and mel filters.
func NewFbank() *Fbank {
	f := &Fbank{fft: newKissFFTR(paddedWindowSize)}
	// GetWindow("povey"): computed in double, stored as float.
	a := 2 * math.Pi / float64(FrameLengthSamples-1)
	for i := range FrameLengthSamples {
		f.window[i] = float32(math.Pow(0.5-0.5*math.Cos(a*float64(i)), 0.85))
	}
	f.initMelBanks()
	return f
}

func melScale(freq float32) float32 {
	// 1127.0f * logf(1.0f + freq / 700.0f)
	return 1127 * logf(1+freq/700)
}

func logf(x float32) float32 { return float32(math.Log(float64(x))) }

// initMelBanks mirrors MelBanks::InitKaldiMelBanks (vtln_warp = 1, htk_mode = false).
func (f *Fbank) initMelBanks() {
	sampleFreq := float32(SampleRate)
	nyquist := 0.5 * sampleFreq
	highFreq := nyquist // high_freq = 0 → nyquist + 0
	fftBinWidth := sampleFreq / paddedWindowSize
	melLow := melScale(lowFreq)
	melHigh := melScale(highFreq)
	melDelta := (melHigh - melLow) / float32(NumMelBins+1)

	for bin := range NumMelBins {
		left := melLow + float32(float32(bin)*melDelta)
		center := melLow + float32(float32(bin+1)*melDelta)
		right := melLow + float32(float32(bin+2)*melDelta)
		var this [numFFTBins]float32
		first, last := -1, -1
		for i := range numFFTBins {
			freq := fftBinWidth * float32(i)
			mel := melScale(freq)
			if mel > left && mel < right {
				var w float32
				if mel <= center {
					w = (mel - left) / (center - left)
				} else {
					w = (right - mel) / (right - center)
				}
				this[i] = w
				if first == -1 {
					first = i
				}
				last = i
			}
		}
		if first == -1 {
			panic("vad: empty mel bin") // impossible with the fixed options
		}
		f.bins[bin] = melBin{offset: first, weights: append([]float32(nil), this[first:last+1]...)}
	}
}

// NumFrames is the frame count for n samples with snip_edges = true.
func NumFrames(n int) int {
	if n < FrameLengthSamples {
		return 0
	}
	return 1 + (n-FrameLengthSamples)/FrameShiftSamples
}

// Compute returns features (frames × NumMelBins, row-major) for samples that
// are already int16-scaled (see [ScaleToPCM]).
func (f *Fbank) Compute(pcm []float32) ([]float32, int) {
	frames := NumFrames(len(pcm))
	out := make([]float32, frames*NumMelBins)
	var buf [paddedWindowSize]float32
	var power [numFFTBins + 1]float32
	for fr := range frames {
		for i := range buf {
			buf[i] = 0
		}
		copy(buf[:FrameLengthSamples], pcm[fr*FrameShiftSamples:fr*FrameShiftSamples+FrameLengthSamples])
		f.processWindow(buf[:FrameLengthSamples])
		f.powerSpectrum(&buf, &power)
		row := out[fr*NumMelBins : (fr+1)*NumMelBins]
		for b := range NumMelBins {
			bin := &f.bins[b]
			var e float32
			for k, w := range bin.weights {
				e += float32(w * power[bin.offset+k])
			}
			if e < floatEpsilon {
				e = floatEpsilon
			}
			row[b] = logf(e)
		}
	}
	return out, frames
}

// powerSpectrum mirrors Rfft::Compute + ComputePowerSpectrum: kissfft real
// FFT in float32, then re²+im² (bin 0 and N/2 from their real parts only).
func (f *Fbank) powerSpectrum(in *[paddedWindowSize]float32, out *[numFFTBins + 1]float32) {
	f.fft.forward(in[:], f.spec[:])
	out[0] = float32(f.spec[0].r * f.spec[0].r)
	for k := 1; k < numFFTBins; k++ {
		c := f.spec[k]
		out[k] = float32(c.r*c.r) + float32(c.i*c.i)
	}
	out[numFFTBins] = float32(f.spec[numFFTBins].r * f.spec[numFFTBins].r)
}

// processWindow mirrors ProcessWindow: remove DC, preemphasis, window.
func (f *Fbank) processWindow(d []float32) {
	var sum float32
	for _, x := range d {
		sum += x
	}
	mean := sum / float32(len(d))
	for i := range d {
		d[i] -= mean
	}
	for i := len(d) - 1; i > 0; i-- {
		d[i] -= float32(preemphCoeff * d[i-1])
	}
	d[0] -= float32(preemphCoeff * d[0])
	for i := range d {
		d[i] *= f.window[i]
	}
}
