// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package vad

import (
	"encoding/binary"
	"math"
	"os"
	"slices"
	"testing"
)

func TestKissFFTFactors(t *testing.T) {
	// kf_factor(256): radix 4 throughout.
	if got := newKissFFT(256).factors; !slices.Equal(got, []int{4, 64, 4, 16, 4, 4, 4, 1}) {
		t.Fatalf("factors(256) = %v", got)
	}
	if got := newKissFFT(8).factors; !slices.Equal(got, []int{4, 2, 2, 1}) {
		t.Fatalf("factors(8) = %v", got)
	}
}

// TestKissFFTMatchesC compares against kiss_fftr built from the C sources
// (testdata/gen_kissfft_ref.c): the port must be bit-identical.
func TestKissFFTMatchesC(t *testing.T) {
	b, err := os.ReadFile("testdata/kissfft_ref.bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 4*512+8*257 {
		t.Fatalf("kissfft_ref.bin: %d bytes", len(b))
	}
	f := func(i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:])) }
	in := make([]float32, 512)
	for i := range in {
		in[i] = f(i)
	}
	out := make([]cpx, 257)
	newKissFFTR(512).forward(in, out)
	for k := range out {
		want := cpx{f(512 + 2*k), f(513 + 2*k)}
		if math.Float32bits(out[k].r) != math.Float32bits(want.r) || math.Float32bits(out[k].i) != math.Float32bits(want.i) {
			t.Fatalf("bin %d: got %v, C kissfft %v", k, out[k], want)
		}
	}
}

// TestKissFFTMatchesDFT checks the radix-2 path as well (n = 2·4^k·… sizes)
// against a float64 DFT.
func TestKissFFTMatchesDFT(t *testing.T) {
	for _, n := range []int{8, 16, 64, 512} {
		in := make([]float32, n)
		seed := uint32(7)
		for i := range in {
			seed = seed*1664525 + 1013904223
			in[i] = float32(int32(seed>>8)%2000) / 1000
		}
		out := make([]cpx, n/2+1)
		newKissFFTR(n).forward(in, out)
		for k := range out {
			var re, im float64
			for j, x := range in {
				ph := -2 * math.Pi * float64(j*k) / float64(n)
				re += float64(x) * math.Cos(ph)
				im += float64(x) * math.Sin(ph)
			}
			if math.Abs(float64(out[k].r)-re) > 1e-3 || math.Abs(float64(out[k].i)-im) > 1e-3 {
				t.Fatalf("n=%d bin %d: got %v, DFT (%g,%g)", n, k, out[k], re, im)
			}
		}
	}
}
