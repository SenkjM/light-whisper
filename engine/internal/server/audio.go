// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
)

var errOddPCM = errors.New("PCM payload must be an even number of bytes (s16le)")

// decodePCM converts little-endian s16 bytes to samples.
func decodePCM(b []byte) ([]int16, error) {
	if len(b)%2 != 0 {
		return nil, errOddPCM
	}
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out, nil
}

// decodeWAV accepts only 16 kHz mono 16-bit PCM WAV (the API's audio format).
func decodeWAV(b []byte) ([]int16, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	var fmtOK bool
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(b) {
			if id == "data" { // tolerate streaming writers that leave size unset
				size = len(b) - body
			} else {
				return nil, fmt.Errorf("truncated %q chunk", id)
			}
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("short fmt chunk")
			}
			f := b[body : body+size]
			format := binary.LittleEndian.Uint16(f[0:2])
			channels := binary.LittleEndian.Uint16(f[2:4])
			rate := binary.LittleEndian.Uint32(f[4:8])
			bits := binary.LittleEndian.Uint16(f[14:16])
			if format != 1 || channels != 1 || rate != asr.SampleRate || bits != 16 {
				return nil, fmt.Errorf("unsupported WAV format (format=%d channels=%d rate=%d bits=%d); need PCM 16-bit mono %d Hz",
					format, channels, rate, bits, asr.SampleRate)
			}
			fmtOK = true
		case "data":
			if !fmtOK {
				return nil, errors.New("data chunk before fmt chunk")
			}
			return decodePCM(b[body : body+size-size%2])
		}
		pos = body + size + size%2
	}
	return nil, errors.New("no data chunk")
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	// Fail fast before reading a large body.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxAudioBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
		return
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var pcm []int16
	switch mt {
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		pcm, err = decodeWAV(body)
	case "application/octet-stream", "audio/l16", "audio/L16", "":
		pcm, err = decodePCM(body)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"send PCM s16le 16 kHz mono (application/octet-stream) or WAV (audio/wav)")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "bad_audio", err.Error())
		return
	}
	q := r.URL.Query()
	opts := asr.TranscribeOptions{Language: q.Get("language"), Context: q.Get("context"), HotWords: q["hot_word"]}
	res, err := s.opts.Manager.Transcribe(r.Context(), pcm, opts)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"text":         res.Text,
		"language":     res.Language,
		"sample_count": res.SampleCount,
		"duration":     (time.Duration(res.SampleCount) * time.Second / asr.SampleRate).Seconds(),
	})
}
