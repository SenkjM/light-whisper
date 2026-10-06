// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/native"
)

type ready struct {
	Event      string `json:"event"`
	Port       int    `json:"port"`
	Token      string `json:"token"`
	APIVersion int    `json:"api_version"`
}

func start(t *testing.T, args ...string) (ready, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- run(ctx, args, strings.NewReader(""), pw, io.Discard); pw.Close() }()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		cancel()
		t.Fatalf("no handshake: %v", err)
	}
	go io.Copy(io.Discard, pr)
	var r ready
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatalf("handshake %q: %v", line, err)
	}
	return r, cancel, done
}

func get(t *testing.T, port int, tok, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestHandshakeAndShutdown(t *testing.T) {
	dir := t.TempDir()
	r, cancel, done := start(t, "--data-dir", dir)
	if r.Event != "ready" || r.Port == 0 || len(r.Token) != 64 || r.APIVersion != 1 {
		t.Fatalf("handshake %+v", r)
	}
	if code, _ := get(t, r.Port, "wrong", "/health"); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	code, m := get(t, r.Port, r.Token, "/health")
	if code != 200 || m["status"] != "ok" {
		t.Fatalf("health %d %v", code, m)
	}
	// Autoload of the default local engine (mock).
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, st := get(t, r.Port, r.Token, "/v1/engine/status")
		if st["model_loaded"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("model never loaded: %v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not shut down")
	}
}

func TestTokenFile(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "token")
	r, cancel, done := start(t, "--data-dir", dir, "--token-file", tf, "--no-autoload")
	defer func() { cancel(); <-done }()
	if r.Token != "" {
		t.Fatal("token printed to stdout despite --token-file")
	}
	b, err := os.ReadFile(tf)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, r.Port, string(b), "/health"); code != 200 {
		t.Fatalf("token file token rejected: %d", code)
	}
}

func TestRejectsNonLoopbackListen(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.10:0", "localhost:0"} {
		err := run(context.Background(), []string{"--listen", addr, "--data-dir", t.TempDir()}, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "invalid") {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

func TestExitOnStdinClose(t *testing.T) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"--data-dir", t.TempDir(), "--exit-on-stdin-close", "--no-autoload"}, pr, io.Discard, io.Discard)
	}()
	time.Sleep(100 * time.Millisecond)
	pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not exit on stdin EOF")
	}
}

func TestNativeBackendUnavailable(t *testing.T) {
	if native.Available {
		t.Skip("native build: covered by internal/native and internal/r2t2 tests")
	}
	err := run(context.Background(), []string{"--backend", "native", "--data-dir", t.TempDir()}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "lwnative") {
		t.Fatalf("got %v", err)
	}
}
