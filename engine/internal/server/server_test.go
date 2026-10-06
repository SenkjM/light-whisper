// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/manager"
)

const token = "test-token-0123456789"

type env struct {
	ts   *httptest.Server
	path string
	mgr  *manager.Manager
}

func newEnv(t *testing.T, engineJSON string) *env {
	t.Helper()
	p := filepath.Join(t.TempDir(), "engine.json")
	if engineJSON != "" {
		if err := os.WriteFile(p, []byte(engineJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := config.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	mgr := manager.New(manager.Options{Store: store, Backend: asr.NewMock(), Hub: hub})
	srv := New(Options{Token: token, Store: store, Manager: mgr, Hub: hub,
		AllowedOrigins: []string{"http://localhost:1420"}})
	ts := httptest.NewServer(srv) // listens on 127.0.0.1
	t.Cleanup(func() {
		hub.Close()
		ts.Close()
		_ = mgr.Close(context.Background())
	})
	return &env{ts: ts, path: p, mgr: mgr}
}

func (e *env) do(t *testing.T, method, path string, body io.Reader, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func errCode(t *testing.T, b []byte) string {
	return decode[errorBody](t, b).Error.Code
}

func TestAuthRejectsBadToken(t *testing.T) {
	e := newEnv(t, "")
	for name, h := range map[string]string{
		"missing":   "",
		"wrong":     "Bearer nope",
		"prefix":    "Bearer " + token[:5],
		"basic":     "Basic " + token,
		"no-scheme": token,
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := e.do(t, "GET", "/health", nil, map[string]string{"Authorization": h})
			if resp.StatusCode != http.StatusUnauthorized || errCode(t, b) != "unauthorized" {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
		})
	}
	// Every route is protected, including WebSockets.
	for _, p := range []string{"/v1/engine/status", "/v1/config", "/v1/config/schema", "/v1/events", "/v1/asr/stream"} {
		resp, _ := e.do(t, "GET", p, nil, map[string]string{"Authorization": ""})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	_, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/v1/events", nil)
	if err == nil {
		t.Fatal("websocket without token accepted")
	}

	resp, b := e.do(t, "GET", "/health", nil, nil)
	h := decode[map[string]any](t, b)
	if resp.StatusCode != 200 || h["status"] != "ok" || h["api_version"] != float64(1) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	e := newEnv(t, "")
	req, _ := http.NewRequest("GET", e.ts.URL+"/health", nil)
	req.Host = "evil.example:1234" // DNS rebinding
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host: %d", resp.StatusCode)
	}

	resp, b := e.do(t, "GET", "/health", nil, map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden || errCode(t, b) != "origin_not_allowed" {
		t.Fatalf("foreign Origin: %d %s", resp.StatusCode, b)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header emitted")
	}
	resp, _ = e.do(t, "GET", "/health", nil, map[string]string{"Origin": "http://localhost:1420"})
	if resp.StatusCode != 200 {
		t.Fatalf("allowed origin: %d", resp.StatusCode)
	}
}

func TestConfigAPI(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b","glm_endpoint":"domestic"}`)
	resp, b := e.do(t, "GET", "/v1/config", nil, nil)
	snap := decode[config.Snapshot](t, b)
	if resp.StatusCode != 200 || snap.Revision == "" || resp.Header.Get("ETag") != `"`+snap.Revision+`"` {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if snap.Apply["engine"] != config.ApplyReload || snap.Apply["gpu_idle_seconds"] != config.ApplyLive {
		t.Fatalf("apply map %v", snap.Apply)
	}

	body := `{"gpu_idle_seconds":60,"engine":"confucius4-r2t2"}`
	resp, b = e.do(t, "PATCH", "/v1/config", strings.NewReader(body), nil)
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match: %d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "PATCH", "/v1/config", strings.NewReader(body), map[string]string{"If-Match": `"bogus"`})
	if resp.StatusCode != http.StatusPreconditionFailed || errCode(t, b) != "revision_mismatch" {
		t.Fatalf("stale If-Match: %d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "PATCH", "/v1/config", strings.NewReader(`{"device":"tpu","x":1}`), map[string]string{"If-Match": snap.Revision})
	eb := decode[errorBody](t, b)
	if resp.StatusCode != 400 || eb.Error.Code != "invalid_config" {
		t.Fatalf("invalid: %d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "PATCH", "/v1/config", strings.NewReader(`[1]`), map[string]string{"If-Match": snap.Revision})
	if resp.StatusCode != 400 {
		t.Fatalf("non-object body: %d", resp.StatusCode)
	}

	resp, b = e.do(t, "PATCH", "/v1/config", strings.NewReader(body), map[string]string{"If-Match": `"` + snap.Revision + `"`})
	pr := decode[config.PatchResult](t, b)
	if resp.StatusCode != 200 || pr.Revision == snap.Revision || len(pr.Applied) != 1 || pr.PendingReload[0] != "engine" {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}

	// Persisted, shell-owned keys preserved.
	raw, _ := os.ReadFile(e.path)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["glm_endpoint"] != "domestic" || m["gpu_idle_seconds"] != float64(60) || m["schema_version"] != float64(1) {
		t.Fatalf("file: %s", raw)
	}

	_, b = e.do(t, "GET", "/v1/engine/status", nil, nil)
	st := decode[manager.Status](t, b)
	if len(st.PendingReload) != 1 || st.GPUIdleSeconds != 60 {
		t.Fatalf("status %s", b)
	}
	resp, b = e.do(t, "POST", "/v1/engine/reload", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("reload %d %s", resp.StatusCode, b)
	}
	_, b = e.do(t, "GET", "/v1/config", nil, nil)
	if s := decode[config.Snapshot](t, b); len(s.PendingReload) != 0 {
		t.Fatalf("still pending %s", b)
	}

	resp, b = e.do(t, "GET", "/v1/config/schema", nil, nil)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"x-apply"`)) {
		t.Fatalf("schema %d", resp.StatusCode)
	}
}

func wsURL(e *env, path string) string { return "ws" + strings.TrimPrefix(e.ts.URL, "http") + path }

func dial(t *testing.T, e *env, path string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(e, path), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("unexpected message type %v", typ)
	}
	return decode[map[string]any](t, b)
}

func TestEventsDelivered(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b"}`)
	c := dial(t, e, "/v1/events")
	first := readJSON(t, c)
	if first["type"] != events.TypeEngineStatus || first["seq"] != float64(0) {
		t.Fatalf("snapshot %v", first)
	}

	_, b := e.do(t, "GET", "/v1/config", nil, nil)
	rev := decode[config.Snapshot](t, b).Revision
	resp, b := e.do(t, "PATCH", "/v1/config", strings.NewReader(`{"log_level":"debug"}`), map[string]string{"If-Match": rev})
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	newRev := decode[config.PatchResult](t, b).Revision
	var lastSeq float64
	for {
		ev := readJSON(t, c)
		seq := ev["seq"].(float64)
		if seq <= lastSeq {
			t.Fatalf("seq not increasing: %v after %v", seq, lastSeq)
		}
		lastSeq = seq
		if ev["type"] == events.TypeConfigChanged {
			d := ev["data"].(map[string]any)
			if d["revision"] != newRev || d["applied"].([]any)[0] != "log_level" {
				t.Fatalf("payload %v", d)
			}
			break
		}
	}
}

func frame(offset uint64, samples int) []byte {
	b := make([]byte, StreamFrameHeader+2*samples)
	binary.LittleEndian.PutUint64(b, offset)
	return b
}

func sendJSON(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func TestStreamSession(t *testing.T) {
	e := newEnv(t, `{"engine":"confucius4-r2t2"}`)
	c := dial(t, e, "/v1/asr/stream")

	if err := c.Write(context.Background(), websocket.MessageBinary, frame(0, 10)); err != nil {
		t.Fatal(err)
	}
	if m := readJSON(t, c); m["code"] != "no_session" {
		t.Fatalf("%v", m)
	}

	sendJSON(t, c, map[string]any{"type": "start", "session_id": 42, "language": "zh"})
	if m := readJSON(t, c); m["type"] != "started" || m["session_id"] != float64(42) {
		t.Fatalf("%v", m)
	}

	// Only one realtime session: a second connection is refused (409).
	resp, b := e.do(t, "GET", "/v1/asr/stream", nil, map[string]string{"Connection": "Upgrade", "Upgrade": "websocket",
		"Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="})
	if resp.StatusCode != http.StatusConflict || errCode(t, b) != "realtime_session_active" {
		t.Fatalf("second stream: %d %s", resp.StatusCode, b)
	}
	// Reload is refused while streaming.
	if resp, _ := e.do(t, "POST", "/v1/engine/reload", nil, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("reload during session: %d", resp.StatusCode)
	}

	half := asr.SampleRate / 2
	_ = c.Write(context.Background(), websocket.MessageBinary, frame(0, half))
	if m := readJSON(t, c); m["type"] != "partial" || m["committed"] != "" || m["tentative"] != "…" {
		t.Fatalf("%v", m)
	}
	_ = c.Write(context.Background(), websocket.MessageBinary, frame(0, half)) // wrong offset
	if m := readJSON(t, c); m["code"] != "offset_mismatch" || m["expected_offset"] != float64(half) {
		t.Fatalf("%v", m)
	}
	_ = c.Write(context.Background(), websocket.MessageBinary, frame(uint64(half), half))
	if m := readJSON(t, c); m["committed"] != "字" || m["tentative"] != "" {
		t.Fatalf("%v", m)
	}
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte{1, 2, 3})
	if m := readJSON(t, c); m["code"] != "bad_frame" {
		t.Fatalf("%v", m)
	}
	sendJSON(t, c, map[string]any{"type": "finish"})
	m := readJSON(t, c)
	if m["type"] != "result" || m["text"] != "字" || m["sample_count"] != float64(asr.SampleRate) || m["language"] != "zh" {
		t.Fatalf("%v", m)
	}
	if e.mgr.SessionActive() {
		t.Fatal("slot held after finish")
	}

	// A new session on the same connection, then disconnect mid-session.
	sendJSON(t, c, map[string]any{"type": "start", "session_id": 43})
	if m := readJSON(t, c); m["type"] != "started" {
		t.Fatalf("%v", m)
	}
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for e.mgr.SessionActive() {
		if time.Now().After(deadline) {
			t.Fatal("slot not released after disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStreamRefusedWithoutR2T2(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b"}`)
	_, resp, err := websocket.Dial(context.Background(), wsURL(e, "/v1/asr/stream"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %v %v", resp, err)
	}
}

func wav(samples int, rate uint32, channels uint16) []byte {
	var b bytes.Buffer
	data := samples * 2 * int(channels)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, channels)
	_ = binary.Write(&b, binary.LittleEndian, rate)
	_ = binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*2)
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	b.Write(make([]byte, data))
	return b.Bytes()
}

func TestTranscribe(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b","default_language":"en"}`)
	resp, b := e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader(make([]byte, 2*3*asr.SampleRate)),
		map[string]string{"Content-Type": "application/octet-stream"})
	m := decode[map[string]any](t, b)
	if resp.StatusCode != 200 || m["text"] != "字字字" || m["duration"] != float64(3) || m["language"] != "en" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "POST", "/v1/asr/transcribe?language=zh", bytes.NewReader(wav(asr.SampleRate, 16000, 1)),
		map[string]string{"Content-Type": "audio/wav"})
	m = decode[map[string]any](t, b)
	if resp.StatusCode != 200 || m["text"] != "字" || m["language"] != "zh" {
		t.Fatalf("wav: %d %s", resp.StatusCode, b)
	}
	for name, body := range map[string][]byte{"stereo": wav(10, 16000, 2), "44k": wav(10, 44100, 1), "garbage": []byte("nope")} {
		resp, _ = e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader(body), map[string]string{"Content-Type": "audio/wav"})
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	resp, _ = e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader([]byte{1, 2, 3}), nil)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("odd PCM: %d", resp.StatusCode)
	}

}

func TestTranscribeWithR2T2(t *testing.T) {
	e := newEnv(t, `{"engine":"confucius4-r2t2"}`)
	resp, b := e.do(t, "POST", "/v1/asr/transcribe?language=zh", bytes.NewReader(wav(asr.SampleRate*3/2, 16000, 1)),
		map[string]string{"Content-Type": "audio/wav"})
	m := decode[map[string]any](t, b)
	if resp.StatusCode != 200 || m["text"] != "字字" || m["language"] != "zh" || m["sample_count"] != float64(asr.SampleRate*3/2) {
		t.Fatalf("r2t2 transcribe: %d %s", resp.StatusCode, b)
	}

	// During a live R2T2 session the shared model is busy: 409.
	c := dial(t, e, "/v1/asr/stream")
	sendJSON(t, c, map[string]any{"type": "start", "session_id": 1})
	if m := readJSON(t, c); m["type"] != "started" {
		t.Fatalf("%v", m)
	}
	resp, b = e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader(make([]byte, 20)), nil)
	if resp.StatusCode != http.StatusConflict || errCode(t, b) != "realtime_session_active" {
		t.Fatalf("r2t2 batch during session: %d %s", resp.StatusCode, b)
	}
	sendJSON(t, c, map[string]any{"type": "cancel"})
	if m := readJSON(t, c); m["type"] != "cancelled" {
		t.Fatalf("%v", m)
	}
	resp, _ = e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader(make([]byte, 20)), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("after session: %d", resp.StatusCode)
	}
}

func TestTranscribeRefusedForCloudEngine(t *testing.T) {
	e := newEnv(t, `{"engine":"glm-asr"}`)
	resp, b := e.do(t, "POST", "/v1/asr/transcribe", bytes.NewReader(make([]byte, 20)), nil)
	if resp.StatusCode != http.StatusConflict || errCode(t, b) != "no_local_engine" {
		t.Fatalf("cloud engine transcribe: %d %s", resp.StatusCode, b)
	}
}
