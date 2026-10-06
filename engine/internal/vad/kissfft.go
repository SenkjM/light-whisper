// SPDX-FileCopyrightText: 2003-2010 Mark Borgerding
// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: BSD-3-Clause
//
// Float32 port of the forward real FFT from KISS FFT
// (github.com/mborgerding/kissfft @ febd4caeed32e33ad8b2e0bb5ea77542c40f18ec,
// the revision kaldi-native-fbank 1.22.3 builds against): kiss_fftr,
// kiss_fft's kf_work / kf_factor and the radix-2/4 butterflies. Ported so
// the Go fbank reproduces kaldi-native-fbank's float32 rounding. Only the
// forward transform for power-of-two sizes is implemented.
//
// Copyright (c) 2003-2010 Mark Borgerding . All rights reserved.
//
// Redistribution and use in source and binary forms, with or without modification,
// are permitted provided that the following conditions are met:
//
// 1. Redistributions of source code must retain the above copyright notice,
// this list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright notice,
// this list of conditions and the following disclaimer in the documentation
// and/or other materials provided with the distribution.
//
// 3. Neither the name of the copyright holder nor the names of its contributors
// may be used to endorse or promote products derived from this software without
// specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE
// USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package vad

import "math"

type cpx struct{ r, i float32 }

// Every product is wrapped in float32() so the Go compiler never fuses it
// into an FMA (C builds of kissfft for generic x86-64 do not fuse either).
func cmul(a, b cpx) cpx {
	return cpx{
		r: float32(a.r*b.r) - float32(a.i*b.i),
		i: float32(a.r*b.i) + float32(a.i*b.r),
	}
}

type kissFFT struct {
	nfft     int
	twiddles []cpx
	factors  []int
}

func newKissFFT(nfft int) *kissFFT {
	st := &kissFFT{nfft: nfft, twiddles: make([]cpx, nfft)}
	const pi = 3.141592653589793238462643383279502884197169399375105820974944
	for i := range nfft {
		phase := -2 * pi * float64(i) / float64(nfft)
		st.twiddles[i] = cpx{float32(math.Cos(phase)), float32(math.Sin(phase))}
	}
	// kf_factor: powers of 4, then 2, then odd primes.
	n, p := nfft, 4
	floorSqrt := math.Floor(math.Sqrt(float64(n)))
	for {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if float64(p) > floorSqrt {
				p = n
			}
		}
		n /= p
		st.factors = append(st.factors, p, n)
		if n <= 1 {
			break
		}
	}
	return st
}

func (st *kissFFT) work(out []cpx, in []cpx, inOff, fstride int, factors []int) {
	p, m := factors[0], factors[1]
	if m == 1 {
		for k := range p {
			out[k] = in[inOff+k*fstride]
		}
	} else {
		for k := range p {
			st.work(out[k*m:], in, inOff+k*fstride, fstride*p, factors[2:])
		}
	}
	switch p {
	case 2:
		st.bfly2(out, fstride, m)
	case 4:
		st.bfly4(out, fstride, m)
	default:
		panic("vad: kissfft port supports radix 2 and 4 only")
	}
}

func (st *kissFFT) bfly2(f []cpx, fstride, m int) {
	for k := range m {
		t := cmul(f[m+k], st.twiddles[k*fstride])
		f[m+k] = cpx{f[k].r - t.r, f[k].i - t.i}
		f[k] = cpx{f[k].r + t.r, f[k].i + t.i}
	}
}

func (st *kissFFT) bfly4(f []cpx, fstride, m int) {
	for k := range m {
		s0 := cmul(f[k+m], st.twiddles[k*fstride])
		s1 := cmul(f[k+2*m], st.twiddles[k*fstride*2])
		s2 := cmul(f[k+3*m], st.twiddles[k*fstride*3])
		s5 := cpx{f[k].r - s1.r, f[k].i - s1.i}
		f[k] = cpx{f[k].r + s1.r, f[k].i + s1.i}
		s3 := cpx{s0.r + s2.r, s0.i + s2.i}
		s4 := cpx{s0.r - s2.r, s0.i - s2.i}
		f[k+2*m] = cpx{f[k].r - s3.r, f[k].i - s3.i}
		f[k] = cpx{f[k].r + s3.r, f[k].i + s3.i}
		f[k+m] = cpx{s5.r + s4.i, s5.i - s4.r}
		f[k+3*m] = cpx{s5.r - s4.i, s5.i + s4.r}
	}
}

// kissFFTR is kiss_fftr for a real input of even length n.
type kissFFTR struct {
	sub           *kissFFT
	superTwiddles []cpx
	packed, tmp   []cpx
}

func newKissFFTR(n int) *kissFFTR {
	ncfft := n / 2
	r := &kissFFTR{
		sub:           newKissFFT(ncfft),
		superTwiddles: make([]cpx, ncfft/2),
		packed:        make([]cpx, ncfft),
		tmp:           make([]cpx, ncfft),
	}
	for i := range ncfft / 2 {
		phase := -3.14159265358979323846264338327 * (float64(i+1)/float64(ncfft) + .5)
		r.superTwiddles[i] = cpx{float32(math.Cos(phase)), float32(math.Sin(phase))}
	}
	return r
}

// forward writes ncfft+1 complex bins of the real input into out.
func (r *kissFFTR) forward(in []float32, out []cpx) {
	ncfft := r.sub.nfft
	for i := range ncfft {
		r.packed[i] = cpx{in[2*i], in[2*i+1]}
	}
	r.sub.work(r.tmp, r.packed, 0, 1, r.sub.factors)
	tdc := r.tmp[0]
	out[0] = cpx{tdc.r + tdc.i, 0}
	out[ncfft] = cpx{tdc.r - tdc.i, 0}
	for k := 1; k <= ncfft/2; k++ {
		fpk := r.tmp[k]
		fpnk := cpx{r.tmp[ncfft-k].r, -r.tmp[ncfft-k].i}
		f1k := cpx{fpk.r + fpnk.r, fpk.i + fpnk.i}
		f2k := cpx{fpk.r - fpnk.r, fpk.i - fpnk.i}
		tw := cmul(f2k, r.superTwiddles[k-1])
		out[k] = cpx{float32((f1k.r + tw.r) * 0.5), float32((f1k.i + tw.i) * 0.5)}
		out[ncfft-k] = cpx{float32((f1k.r - tw.r) * 0.5), float32((tw.i - f1k.i) * 0.5)}
	}
}
