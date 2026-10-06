// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Command lw-engine is the standalone Go engine (PLAN §3, §4).
//
// Startup handshake: once listening, it prints exactly one JSON line to
// stdout:
//
//	{"event":"ready","port":53817,"token":"…","api_version":1,"version":"…","pid":1234}
//
// With --token-file the token is written to that file (mode 0600) instead
// and omitted from stdout. Logs go to stderr.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/manager"
	"github.com/SenkjM/light-whisper/engine/internal/server"
	"github.com/SenkjM/light-whisper/engine/internal/version"
)

// appIdentifier matches APP_IDENTIFIER in src-tauri/src/utils/paths.rs.
const appIdentifier = "com.light-whisper.app"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "lw-engine:", err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// defaultDataDir mirrors get_data_dir() in paths.rs: LIGHT_WHISPER_DATA_DIR,
// else dirs::data_dir()/com.light-whisper.app.
func defaultDataDir() string {
	if d := os.Getenv("LIGHT_WHISPER_DATA_DIR"); d != "" {
		return d
	}
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("APPDATA")
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, "Library", "Application Support")
		}
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			if h, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(h, ".local", "share")
			}
		}
	}
	if base == "" {
		return ".light-whisper"
	}
	return filepath.Join(base, appIdentifier)
}

func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --listen %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--listen must be a loopback IP address (e.g. 127.0.0.1:0), got %q", addr)
	}
	return nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("lw-engine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:0", "loopback listen address (port 0 = random)")
	dataDir := fs.String("data-dir", "", "app data directory (default: same as the Tauri app)")
	configPath := fs.String("config", "", "engine.json path (default: <data-dir>/engine.json)")
	backendName := fs.String("backend", "mock", "inference backend: mock | native")
	tokenFile := fs.String("token-file", "", "write the startup token here (0600) instead of stdout")
	exitOnStdin := fs.Bool("exit-on-stdin-close", false, "exit when stdin reaches EOF (parent process died)")
	noAutoload := fs.Bool("no-autoload", false, "do not load the configured local engine at startup")
	var origins stringList
	fs.Var(&origins, "allow-origin", "allowed Origin for dev-mode frontends (repeatable); default none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkLoopback(*listen); err != nil {
		return err
	}

	level := new(slog.LevelVar)
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	if *dataDir == "" {
		*dataDir = defaultDataDir()
	}
	if *configPath == "" {
		*configPath = filepath.Join(*dataDir, "engine.json")
	}
	store, err := config.Open(*configPath)
	if err != nil {
		return err
	}

	var backend asr.Backend
	switch *backendName {
	case "mock":
		backend = asr.NewMock()
	case "native":
		if backend, err = asr.NewNative(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --backend %q", *backendName)
	}

	token, err := newToken()
	if err != nil {
		return err
	}
	hub := events.NewHub()
	defer hub.Close()
	mgr := manager.New(manager.Options{Store: store, Backend: backend, Hub: hub, Logger: logger, LogLevel: level})
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = mgr.Close(cctx)
	}()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: server.New(server.Options{
			Token: token, Store: store, Manager: mgr, Hub: hub, Logger: logger, AllowedOrigins: origins,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	ready := map[string]any{
		"event":       "ready",
		"port":        ln.Addr().(*net.TCPAddr).Port,
		"api_version": version.APIVersion,
		"version":     version.Version,
		"pid":         os.Getpid(),
		"backend":     backend.Name(),
	}
	if *tokenFile != "" {
		if err := os.WriteFile(*tokenFile, []byte(token), 0o600); err != nil {
			_ = srv.Close()
			return fmt.Errorf("write token file: %w", err)
		}
	} else {
		ready["token"] = token
	}
	line, _ := json.Marshal(ready)
	if _, err := fmt.Fprintf(stdout, "%s\n", line); err != nil {
		_ = srv.Close()
		return err
	}
	logger.Info("engine listening", "addr", ln.Addr().String(), "config", *configPath, "backend", backend.Name())

	if !*noAutoload {
		go func() {
			if err := mgr.Load(ctx); err != nil && !errors.Is(err, manager.ErrNoLocalEngine) && !errors.Is(err, context.Canceled) {
				logger.Warn("initial model load failed", "err", err)
			}
		}()
	}

	stdinEOF := make(chan struct{})
	if *exitOnStdin {
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			close(stdinEOF)
		}()
	}

	select {
	case <-ctx.Done():
	case <-stdinEOF:
		logger.Info("stdin closed; shutting down")
	case err := <-serveErr:
		return err
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hub.Close() // ends /v1/events streams so Shutdown does not wait on them
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
	}
	return nil
}
