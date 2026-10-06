// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
)

// voicedPCM: n voiced samples then one second of silence, as s16le bytes.
func voicedPCM(lengths ...int) []byte {
	var b bytes.Buffer
	for _, n := range lengths {
		for i := 0; i < n; i++ {
			_ = binary.Write(&b, binary.LittleEndian, int16(1000))
		}
		b.Write(make([]byte, 2*asr.SampleRate))
	}
	return b.Bytes()
}

func pollJob(t *testing.T, e *env, id, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, b := e.do(t, "GET", "/v1/jobs/"+id, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		m := decode[map[string]any](t, b)
		if m["state"] == state {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, state)
	return nil
}

func TestJobsAPI(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b","default_language":"zh"}`)
	resp, b := e.do(t, "POST", "/v1/jobs?segments=true&priority=high", bytes.NewReader(voicedPCM(2*asr.SampleRate, asr.SampleRate)),
		map[string]string{"Content-Type": "application/octet-stream"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	m := decode[map[string]any](t, b)
	id, _ := m["id"].(string)
	if id == "" || m["priority"] != "high" || resp.Header.Get("Location") != "/v1/jobs/"+id {
		t.Fatalf("%v %v", m, resp.Header)
	}
	done := pollJob(t, e, id, "completed")
	segs, _ := done["segments"].([]any)
	if done["text"] != "字字字字" || done["language"] != "zh" || done["progress"] != float64(1) || len(segs) != 1 {
		// The default 20 s unit merges both regions (and the pause) into one segment.
		t.Fatalf("%v", done)
	}

	// JSON form with a WAV path; default priority comes from the config.
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(path, wav(asr.SampleRate, 16000, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"path": path})
	resp, b = e.do(t, "POST", "/v1/jobs", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	m = decode[map[string]any](t, b)
	if m["priority"] != "normal" {
		t.Fatalf("%v", m)
	}
	// The WAV is silent: no speech, empty text, no segments.
	if done := pollJob(t, e, m["id"].(string), "completed"); done["text"] != "" || done["segments_total"] != float64(0) {
		t.Fatalf("%v", done)
	}

	resp, b = e.do(t, "GET", "/v1/jobs", nil, nil)
	if list := decode[map[string][]map[string]any](t, b)["jobs"]; resp.StatusCode != 200 || len(list) != 2 || list[0]["id"] != id {
		t.Fatalf("%s", b)
	}
	if resp, _ := e.do(t, "DELETE", "/v1/jobs/"+id, nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, b := e.do(t, "GET", "/v1/jobs/"+id, nil, nil); resp.StatusCode != http.StatusNotFound || errCode(t, b) != "not_found" {
		t.Fatalf("after delete: %d %s", resp.StatusCode, b)
	}

	for name, tc := range map[string]struct {
		path, ctype, body string
		status            int
		code              string
	}{
		"realtime job":  {"/v1/jobs?priority=realtime", "application/octet-stream", "\x00\x00", 400, "invalid_priority"},
		"bad priority":  {"/v1/jobs?priority=urgent", "application/octet-stream", "\x00\x00", 400, "invalid_priority"},
		"relative path": {"/v1/jobs", "application/json", `{"path":"a.wav"}`, 400, "bad_request"},
		"unknown field": {"/v1/jobs", "application/json", `{"path":"/x.wav","engine":"r2t2"}`, 400, "bad_request"},
		"missing file":  {"/v1/jobs", "application/json", `{"path":"/nonexistent/x.wav"}`, 400, "bad_audio"},
		"bad wav":       {"/v1/jobs", "audio/wav", "nope", 415, "bad_audio"},
	} {
		resp, b := e.do(t, "POST", tc.path, strings.NewReader(tc.body), map[string]string{"Content-Type": tc.ctype})
		if resp.StatusCode != tc.status || errCode(t, b) != tc.code {
			t.Errorf("%s: %d %s", name, resp.StatusCode, b)
		}
	}
}

func TestTranscribePriorityParam(t *testing.T) {
	e := newEnv(t, `{"engine":"qwen3-asr-0.6b"}`)
	for _, p := range []string{"realtime", "high", "normal"} {
		resp, b := e.do(t, "POST", "/v1/asr/transcribe?priority="+p, bytes.NewReader(make([]byte, 2*asr.SampleRate)),
			map[string]string{"Content-Type": "application/octet-stream"})
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, b)
		}
	}
	resp, b := e.do(t, "POST", "/v1/asr/transcribe?priority=batch", bytes.NewReader(make([]byte, 2)),
		map[string]string{"Content-Type": "application/octet-stream"})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_priority" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	resp, b = e.do(t, "GET", "/v1/engine/status", nil, nil)
	st := decode[map[string]any](t, b)
	sch, _ := st["scheduler"].(map[string]any)
	if _, ok := sch["high_queued"]; !ok || st["jobs"] == nil {
		t.Fatalf("status %s", b)
	}
}
