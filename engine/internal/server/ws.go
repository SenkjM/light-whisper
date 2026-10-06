// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/manager"
)

const (
	writeTimeout = 10 * time.Second
	// StreamFrameHeader is the binary frame header size: uint64 LE sample offset.
	StreamFrameHeader = 8
	maxStreamFrame    = 1 << 20
)

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	// Origin was already checked against the allowlist in ServeHTTP.
	return websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
}

func writeWS(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

// --- WS /v1/events ----------------------------------------------------------------

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	sub := s.opts.Hub.Subscribe(256)
	defer sub.Cancel()
	ctx := c.CloseRead(r.Context()) // we never expect client messages

	// Initial snapshot so the client starts in sync (seq 0 = snapshot).
	if err := writeWS(ctx, c, events.Event{Seq: 0, Type: events.TypeEngineStatus, Time: time.Now().UTC(), Data: s.opts.Manager.Status()}); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				if sub.Dropped() {
					c.Close(websocket.StatusPolicyViolation, "event consumer too slow; resync and reconnect")
				} else {
					c.Close(websocket.StatusGoingAway, "engine shutting down")
				}
				return
			}
			if err := writeWS(ctx, c, ev); err != nil {
				return
			}
		}
	}
}

// --- WS /v1/asr/stream ----------------------------------------------------------------

type streamControl struct {
	Type      string   `json:"type"`
	SessionID uint64   `json:"session_id"`
	Language  string   `json:"language,omitempty"`
	Context   string   `json:"context,omitempty"`
	HotWords  []string `json:"hot_words,omitempty"`
}

type streamError struct {
	Type      string  `json:"type"` // "error"
	SessionID uint64  `json:"session_id,omitempty"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	Expected  *uint64 `json:"expected_offset,omitempty"`
}

func errFrame(sid uint64, code, msg string) streamError {
	return streamError{Type: "error", SessionID: sid, Code: code, Message: msg}
}

func managerErrCode(err error) string {
	switch {
	case errors.Is(err, manager.ErrSessionActive):
		return "realtime_session_active"
	case errors.Is(err, manager.ErrNoLocalEngine):
		return "no_local_engine"
	case errors.Is(err, manager.ErrWrongEngine):
		return "wrong_engine"
	}
	return "engine_error"
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// PLAN §4.4: refuse the connection when R2T2 is not the active engine
	// (never silently fall back to Qwen3) or when a session is already active.
	if err := s.opts.Manager.CheckRealtimeAvailable(); err != nil {
		writeManagerError(w, err)
		return
	}
	if s.opts.Manager.SessionActive() {
		writeManagerError(w, manager.ErrSessionActive)
		return
	}
	c, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(maxStreamFrame)
	ctx := r.Context()

	var sess *manager.Session
	var sid uint64
	defer func() {
		if sess != nil {
			sess.Cancel()
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if sess == nil {
				_ = writeWS(ctx, c, errFrame(0, "no_session", "send start before audio"))
				continue
			}
			if len(data) < StreamFrameHeader || (len(data)-StreamFrameHeader)%2 != 0 {
				_ = writeWS(ctx, c, errFrame(sid, "bad_frame", "binary frame = 8-byte LE sample offset + PCM s16le"))
				continue
			}
			offset := binary.LittleEndian.Uint64(data[:StreamFrameHeader])
			pcm, _ := decodePCM(data[StreamFrameHeader:])
			p, err := sess.Push(ctx, offset, pcm)
			var oe *manager.OffsetError
			switch {
			case errors.As(err, &oe):
				e := errFrame(sid, "offset_mismatch", oe.Error())
				e.Expected = &oe.Expected
				_ = writeWS(ctx, c, e)
				continue
			case err != nil:
				_ = writeWS(ctx, c, errFrame(sid, "engine_error", err.Error()))
				continue
			}
			if err := writeWS(ctx, c, map[string]any{
				"type": "partial", "session_id": sid, "committed": p.Committed, "tentative": p.Tentative,
			}); err != nil {
				return
			}
			continue
		}

		var msg streamControl
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = writeWS(ctx, c, errFrame(sid, "bad_message", "control messages are JSON objects with a type field"))
			continue
		}
		switch msg.Type {
		case "start":
			if sess != nil {
				_ = writeWS(ctx, c, errFrame(sid, "session_already_started", "finish or cancel the current session first"))
				continue
			}
			ns, err := s.opts.Manager.StartSession(ctx, asr.StreamOptions{
				SessionID: msg.SessionID, Language: msg.Language, Context: msg.Context, HotWords: msg.HotWords,
			})
			if err != nil {
				_ = writeWS(ctx, c, errFrame(msg.SessionID, managerErrCode(err), err.Error()))
				continue
			}
			sess, sid = ns, msg.SessionID
			_ = writeWS(ctx, c, map[string]any{"type": "started", "session_id": sid})
		case "finish":
			if sess == nil {
				_ = writeWS(ctx, c, errFrame(0, "no_session", "no active session"))
				continue
			}
			res, err := sess.Finish(ctx)
			sess = nil
			if err != nil {
				_ = writeWS(ctx, c, errFrame(sid, "engine_error", err.Error()))
				continue
			}
			_ = writeWS(ctx, c, map[string]any{
				"type": "result", "session_id": sid, "text": res.Text, "language": res.Language, "sample_count": res.SampleCount,
			})
		case "cancel":
			if sess != nil {
				sess.Cancel()
				sess = nil
			}
			_ = writeWS(ctx, c, map[string]any{"type": "cancelled", "session_id": sid})
		default:
			_ = writeWS(ctx, c, errFrame(sid, "bad_message", "unknown type "+msg.Type))
		}
	}
}
