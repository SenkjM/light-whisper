// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only
//
// Writes kissfft_ref.bin: 512 float32 inputs followed by 257 complex float32
// outputs of kiss_fftr(512), for TestKissFFTMatchesC. Build against kissfft
// febd4caeed32e33ad8b2e0bb5ea77542c40f18ec without -ffast-math:
//
//   gcc -O2 -I$KISSFFT gen_kissfft_ref.c $KISSFFT/kiss_fftr.c $KISSFFT/kiss_fft.c -lm
//   ./a.out > kissfft_ref.bin
#include <stdio.h>

#include "kiss_fftr.h"

int main(void) {
  float in[512];
  unsigned s = 12345;
  for (int i = 0; i < 512; i++) {
    s = s * 1103515245u + 12345u;
    in[i] = ((float)((s >> 8) & 0xffff) - 32768.0f) * 0.37f;
  }
  kiss_fftr_cfg cfg = kiss_fftr_alloc(512, 0, 0, 0);
  kiss_fft_cpx out[257];
  kiss_fftr(cfg, in, out);
  fwrite(in, 4, 512, stdout);
  fwrite(out, 8, 257, stdout);
  kiss_fftr_free(cfg);
  return 0;
}
