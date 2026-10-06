// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of R2T2StreamSession from src-tauri/resources/r2t2_stream.py
// (inherited light-whisper code, GPL-3.0-only): one contiguous stream at a
// time, session id and offset validation, committed text that only grows,
// tentative = preview − committed, empty finish without a native finish, and
// reset-and-invalidate after any runtime error.
//
// Per PLAN §10.4, logic translated from inherited GPL code is not labelled
// as newly written AGPL code; this file stays GPL-3.0-only (combinable with
// the AGPL-3.0-only engine, PLAN §10.3).

package r2t2

import (
	"errors"
	"strings"
)

// Response mirrors R2T2StreamSession's response dict.
type Response struct {
	SessionID     uint64 `json:"session_id"`
	Text          string `json:"text"`
	TentativeText string `json:"tentative_text"`
	Language      string `json:"language,omitempty"`
	SampleCount   int    `json:"sample_count"`
	Final         bool   `json:"final"`
}

// StreamSession owns one contiguous stream of a runtime at a time
// (Segmented in production).
type StreamSession struct {
	runtime       Native
	lastSessionID uint64
	activeID      uint64 // 0 = none
	text          string
	language      string
	sampleCount   int
}

// NewStreamSession wraps runtime.
func NewStreamSession(runtime Native) *StreamSession {
	return &StreamSession{runtime: runtime}
}

func (s *StreamSession) clearActive() {
	s.activeID = 0
	s.text = ""
	s.language = ""
	s.sampleCount = 0
}

func (s *StreamSession) requireActive(sessionID uint64) error {
	if sessionID == 0 {
		return invalid("invalid session_id")
	}
	if sessionID != s.activeID {
		return invalid("session is not active")
	}
	return nil
}

func (s *StreamSession) invalidateAfterError() {
	s.clearActive()
	_ = s.runtime.Reset()
}

func (s *StreamSession) rememberLanguage(language string) {
	if language != "" {
		s.language = language
	}
}

func (s *StreamSession) previewSuffix(committed string) (string, error) {
	preview := committed
	if p, ok := s.runtime.(Previewer); ok {
		preview = p.PreviewText()
	}
	if !strings.HasPrefix(preview, committed) {
		return "", errors.New("native runtime preview regressed")
	}
	return preview[len(committed):], nil
}

// Active reports whether a stream is open.
func (s *StreamSession) Active() bool { return s.activeID != 0 }

// ActiveID is the open session's id (0 = none).
func (s *StreamSession) ActiveID() uint64 { return s.activeID }

// Start opens session sessionID (a new id, larger than every previous one),
// replacing any active stream.
func (s *StreamSession) Start(sessionID uint64, context, language string) (Response, error) {
	if sessionID == 0 || sessionID <= s.lastSessionID {
		return Response{}, invalid("session_id must be a new positive integer")
	}
	s.lastSessionID = sessionID
	if s.activeID != 0 {
		s.clearActive()
		if err := s.runtime.Reset(); err != nil {
			return Response{}, err
		}
	}
	s.activeID = sessionID
	s.text = ""
	s.language = language
	s.sampleCount = 0
	if err := s.runtime.Start(context, language); err != nil {
		s.invalidateAfterError()
		return Response{}, err
	}
	return Response{SessionID: sessionID, Language: language}, nil
}

// Feed pushes pcm at offset (which must equal the accepted sample count).
func (s *StreamSession) Feed(sessionID uint64, pcm []float32, offset int) (Response, error) {
	if err := s.requireActive(sessionID); err != nil {
		return Response{}, err
	}
	if offset < 0 {
		return Response{}, invalid("offset must be a non-negative integer")
	}
	if len(pcm) == 0 || !finite(pcm) {
		return Response{}, invalid("pcm must be a non-empty finite float32 vector")
	}
	if offset != s.sampleCount {
		return Response{}, invalid("offset must equal the accepted sample count")
	}
	previous := s.text
	text, language, err := s.runtime.Feed(append([]float32(nil), pcm...))
	if err == nil && !strings.HasPrefix(text, previous) {
		err = errors.New("native runtime text regressed")
	}
	var tentative string
	if err == nil {
		tentative, err = s.previewSuffix(text)
	}
	if err != nil {
		s.invalidateAfterError()
		return Response{}, err
	}
	s.text = text
	s.rememberLanguage(language)
	s.sampleCount += len(pcm)
	return Response{SessionID: sessionID, Text: s.text, TentativeText: tentative, Language: s.language, SampleCount: s.sampleCount}, nil
}

// Finish ends the session and returns the final text.
func (s *StreamSession) Finish(sessionID uint64) (Response, error) {
	if err := s.requireActive(sessionID); err != nil {
		return Response{}, err
	}
	sampleCount := s.sampleCount
	language := s.language
	if sampleCount == 0 {
		s.clearActive()
		if err := s.runtime.Reset(); err != nil {
			return Response{}, err
		}
		return Response{SessionID: sessionID, Language: language, Final: true}, nil
	}
	previous := s.text
	text, nativeLanguage, err := s.runtime.Finish()
	if err != nil {
		s.invalidateAfterError()
		return Response{}, err
	}
	if !strings.HasPrefix(text, previous) {
		s.invalidateAfterError()
		return Response{}, errors.New("native runtime text regressed")
	}
	s.rememberLanguage(nativeLanguage)
	language = s.language
	s.clearActive()
	return Response{SessionID: sessionID, Text: text, Language: language, SampleCount: sampleCount, Final: true}, nil
}

// Cancel drops the session without a result.
func (s *StreamSession) Cancel(sessionID uint64) (Response, error) {
	if err := s.requireActive(sessionID); err != nil {
		return Response{}, err
	}
	sampleCount := s.sampleCount
	language := s.language
	s.clearActive()
	if err := s.runtime.Reset(); err != nil {
		return Response{}, err
	}
	return Response{SessionID: sessionID, Language: language, SampleCount: sampleCount, Final: true}, nil
}

// Close drops an active session (no-op otherwise). It does not close the
// runtime.
func (s *StreamSession) Close() error {
	if s.activeID == 0 {
		return nil
	}
	s.clearActive()
	return s.runtime.Reset()
}
