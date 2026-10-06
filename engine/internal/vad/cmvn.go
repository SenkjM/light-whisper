// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package vad

import (
	"encoding/json"
	"fmt"
	"os"
)

// CMVN holds the global mean / inverse standard deviation used to normalize
// features (fireredvad_cmvn.json, converted from FireRedVAD's cmvn.ark).
type CMVN struct {
	Mean       [NumMelBins]float32
	InverseStd [NumMelBins]float32
}

type cmvnFile struct {
	Mean       []float64 `json:"mean"`
	InverseStd []float64 `json:"inverse_std"`
}

// ParseCMVN decodes the JSON asset. Values are rounded to float32 like the
// Python runtime's np.asarray(..., dtype=float32).
func ParseCMVN(data []byte) (*CMVN, error) {
	var f cmvnFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("vad: parse cmvn: %w", err)
	}
	if len(f.Mean) != NumMelBins || len(f.InverseStd) != NumMelBins {
		return nil, fmt.Errorf("vad: cmvn must contain %d-dim mean and inverse_std (got %d, %d)",
			NumMelBins, len(f.Mean), len(f.InverseStd))
	}
	c := &CMVN{}
	for i := range NumMelBins {
		c.Mean[i] = float32(f.Mean[i])
		c.InverseStd[i] = float32(f.InverseStd[i])
	}
	return c, nil
}

// LoadCMVN reads the JSON asset from disk.
func LoadCMVN(path string) (*CMVN, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vad: %w", err)
	}
	return ParseCMVN(data)
}

// Apply normalizes features in place: (x - mean) · inverse_std, in float32.
func (c *CMVN) Apply(features []float32) {
	for i := range features {
		d := i % NumMelBins
		features[i] = float32(features[i]-c.Mean[d]) * c.InverseStd[d]
	}
}
