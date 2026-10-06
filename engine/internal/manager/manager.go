// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package manager ties the config store, the serial scheduler, the ASR
// backend and the event hub together: model load / unload / reload, GPU idle
// unload, the single realtime session slot, and status reporting.
package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/config"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/scheduler"
)

// Errors mapped to HTTP status codes by the server.
var (
	// ErrBusy: a realtime session is active (reload / unload refused, 409).
	ErrBusy = errors.New("a realtime session is active")
	// ErrSessionActive: another realtime session already holds the slot (409).
	ErrSessionActive = errors.New("another realtime session is active")
	// ErrNoLocalEngine: the configured engine is a cloud engine (409).
	ErrNoLocalEngine = errors.New("configured engine is not a local engine")
	// ErrWrongEngine: the request needs a different engine kind (409).
	ErrWrongEngine = errors.New("operation not available for the active engine")
)

// Options configure a Manager.
type Options struct {
	Store   *config.Store
	Backend asr.Backend
	Hub     *events.Hub
	Logger  *slog.Logger
	// LogLevel, if set, is updated live from config.log_level.
	LogLevel *slog.LevelVar
	// IdleTick is how often the GPU idle check runs (default 500 ms, like Python).
	IdleTick time.Duration
	// IdleUnit is the unit of gpu_idle_seconds (default 1s; tests shrink it).
	IdleUnit time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Status is the GET /v1/engine/status payload.
type Status struct {
	Backend               string          `json:"backend"`
	ConfiguredEngine      string          `json:"configured_engine"`
	LocalEngine           bool            `json:"local_engine"`
	Loading               bool            `json:"loading"`
	asr.Info                              // engine, kind, device, model_loaded, gpu_*, missing_models
	PendingReload         []string        `json:"pending_reload"`
	RealtimeSessionActive bool            `json:"realtime_session_active"`
	GPUIdleSeconds        uint64          `json:"gpu_idle_seconds"`
	IdleUnloaded          bool            `json:"idle_unloaded"`
	Scheduler             scheduler.Stats `json:"scheduler"`
	LastError             string          `json:"last_error,omitempty"`
}

// Manager is safe for concurrent use.
type Manager struct {
	opts  Options
	sched *scheduler.Scheduler
	log   *slog.Logger

	mu           sync.Mutex
	info         asr.Info // cached from the inference thread
	loading      bool
	lastErr      string
	lastActivity time.Time
	idleUnloaded bool
	sessionHeld  bool

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New creates a manager and starts its scheduler and idle watcher.
func New(opts Options) *Manager {
	if opts.IdleTick <= 0 {
		opts.IdleTick = 500 * time.Millisecond
	}
	if opts.IdleUnit <= 0 {
		opts.IdleUnit = time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := &Manager{
		opts:  opts,
		sched: scheduler.New(),
		log:   opts.Logger,
		info:  asr.Info{MissingModels: []string{}},
		stop:  make(chan struct{}),
	}
	m.lastActivity = opts.Now()
	m.applyLogLevel(opts.Store.Effective())
	m.wg.Add(1)
	go m.idleLoop()
	return m
}

// Scheduler exposes the scheduler (tests, stats).
func (m *Manager) Scheduler() *scheduler.Scheduler { return m.sched }

// Close stops the idle watcher, unloads the model and stops the scheduler.
func (m *Manager) Close(ctx context.Context) error {
	m.stopOnce.Do(func() { close(m.stop) })
	m.wg.Wait()
	_ = m.sched.Do(ctx, scheduler.Batch, func() error { return m.opts.Backend.Unload() })
	return m.sched.Close(ctx)
}

func (m *Manager) applyLogLevel(v config.Values) {
	if m.opts.LogLevel == nil {
		return
	}
	var lvl slog.Level
	switch v.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	m.opts.LogLevel.Set(lvl)
}

func specFor(v config.Values) (asr.LoadSpec, bool) {
	kind, ok := asr.KindForEngine(v.Engine)
	if !ok {
		return asr.LoadSpec{}, false
	}
	return asr.LoadSpec{Engine: v.Engine, Kind: kind, ModelsDir: v.ModelsDir, Device: v.Device}, true
}

// --- inference-thread helpers (only call inside sched.Do) -------------------

func (m *Manager) refreshInfoLocked() {
	info := m.opts.Backend.Info()
	if info.MissingModels == nil {
		info.MissingModels = []string{}
	}
	m.mu.Lock()
	m.info = info
	m.mu.Unlock()
}

// ensureLoaded loads the active engine if needed (after idle unload or at
// first use). Runs on the inference thread.
func (m *Manager) ensureLoaded() error {
	if m.opts.Backend.Info().ModelLoaded {
		return nil
	}
	eff := m.opts.Store.Effective()
	spec, ok := specFor(eff)
	if !ok {
		return ErrNoLocalEngine
	}
	return m.loadOnThread(spec)
}

func (m *Manager) loadOnThread(spec asr.LoadSpec) error {
	m.setLoading(true)
	err := m.opts.Backend.Load(spec)
	m.mu.Lock()
	m.loading = false
	if err != nil {
		m.lastErr = err.Error()
	} else {
		m.lastErr = ""
		m.idleUnloaded = false
	}
	m.mu.Unlock()
	m.refreshInfoLocked()
	m.publishStatus()
	return err
}

func (m *Manager) setLoading(v bool) {
	m.mu.Lock()
	m.loading = v
	m.mu.Unlock()
	m.publishStatus()
}

func (m *Manager) touch() {
	m.mu.Lock()
	m.lastActivity = m.opts.Now()
	m.mu.Unlock()
}

// --- public operations -------------------------------------------------------

// Load loads the active engine (no-op if loaded). Used at startup and by
// POST /v1/engine/load.
func (m *Manager) Load(ctx context.Context) error {
	return m.sched.Do(ctx, scheduler.Batch, func() error {
		err := m.ensureLoaded()
		m.touch()
		return err
	})
}

// Unload unloads the model. Refused while a realtime session is active.
func (m *Manager) Unload(ctx context.Context) error {
	if m.SessionActive() {
		return ErrBusy
	}
	return m.sched.Do(ctx, scheduler.Batch, func() error {
		err := m.opts.Backend.Unload()
		m.refreshInfoLocked()
		m.publishStatus()
		return err
	})
}

// Reload activates pending reload keys: unload, then load with the persisted
// config. Refused (ErrBusy) while a realtime session is active.
func (m *Manager) Reload(ctx context.Context) (applied []string, err error) {
	if m.SessionActive() {
		return nil, ErrBusy
	}
	err = m.sched.Do(ctx, scheduler.Batch, func() error {
		desired := m.opts.Store.Desired()
		applied = m.opts.Store.Pending()
		wasLoaded := m.opts.Backend.Info().ModelLoaded
		if err := m.opts.Backend.Unload(); err != nil {
			return err
		}
		m.opts.Store.Activate(desired)
		m.refreshInfoLocked()
		spec, ok := specFor(desired)
		if !ok || (!wasLoaded && len(applied) == 0) {
			m.publishStatus()
			return nil
		}
		return m.loadOnThread(spec)
	})
	if applied == nil {
		applied = []string{}
	}
	return applied, err
}

// OnConfigPatched must be called after a successful PATCH /v1/config: it
// applies live keys and publishes config_changed.
func (m *Manager) OnConfigPatched(res config.PatchResult) {
	m.applyLogLevel(m.opts.Store.Effective())
	m.opts.Hub.Publish(events.TypeConfigChanged, map[string]any{
		"revision":       res.Revision,
		"changed":        nonNil(res.Changed),
		"applied":        nonNil(res.Applied),
		"pending_reload": nonNil(res.PendingReload),
	})
	m.publishStatus()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Transcribe runs whole-clip recognition with the active local engine
// (Qwen3 or R2T2) at batch priority.
//
// The whole clip is one scheduler job, so it never interleaves with realtime
// chunks: a live session that starts meanwhile waits until the job finishes
// (running jobs are not pre-empted). R2T2 batch work shares the single
// loaded R2T2 model with live sessions, so — like the Python R2T2 server's
// "stream_busy" — it is refused with ErrSessionActive while a realtime
// session holds the slot. Qwen3 batch jobs simply queue behind realtime work.
func (m *Manager) Transcribe(ctx context.Context, pcm []int16, opts asr.TranscribeOptions) (asr.Result, error) {
	eff := m.opts.Store.Effective()
	kind, ok := asr.KindForEngine(eff.Engine)
	if !ok {
		return asr.Result{}, ErrNoLocalEngine
	}
	if kind == asr.KindR2T2 && m.SessionActive() {
		return asr.Result{}, fmt.Errorf("%w: R2T2 batch transcription shares the model with the live session", ErrSessionActive)
	}
	if opts.Language == "" {
		opts.Language = eff.DefaultLanguage
	}
	var res asr.Result
	err := m.sched.Do(ctx, scheduler.Batch, func() error {
		defer m.touch()
		// Re-check on the inference thread: a session may have started
		// while this job was queued (its start job ran first).
		if kind == asr.KindR2T2 && m.SessionActive() {
			return fmt.Errorf("%w: R2T2 batch transcription shares the model with the live session", ErrSessionActive)
		}
		if err := m.ensureLoaded(); err != nil {
			return err
		}
		var err error
		res, err = m.opts.Backend.Transcribe(pcm, opts)
		return err
	})
	return res, err
}

// Session is one realtime R2T2 session holding the single realtime slot.
type Session struct {
	m        *Manager
	stream   asr.Stream
	samples  uint64
	released bool
}

// StartSession acquires the realtime slot and opens a stream. Only R2T2 may
// stream; there is no silent fallback to Qwen3 (PLAN §4.4).
func (m *Manager) StartSession(ctx context.Context, opts asr.StreamOptions) (*Session, error) {
	if err := m.CheckRealtimeAvailable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.sessionHeld {
		m.mu.Unlock()
		return nil, ErrSessionActive
	}
	m.sessionHeld = true
	m.mu.Unlock()
	if opts.Language == "" {
		opts.Language = m.opts.Store.Effective().DefaultLanguage
	}
	s := &Session{m: m}
	err := m.sched.Do(ctx, scheduler.Realtime, func() error {
		defer m.touch()
		if err := m.ensureLoaded(); err != nil {
			return err
		}
		st, err := m.opts.Backend.NewStream(opts)
		s.stream = st
		return err
	})
	if err != nil {
		m.releaseSession()
		return nil, err
	}
	m.publishStatus()
	return s, nil
}

// CheckRealtimeAvailable reports whether the active engine can stream.
func (m *Manager) CheckRealtimeAvailable() error {
	eff := m.opts.Store.Effective()
	kind, ok := asr.KindForEngine(eff.Engine)
	if !ok {
		return ErrNoLocalEngine
	}
	if kind != asr.KindR2T2 {
		return fmt.Errorf("%w: realtime streaming needs %s", ErrWrongEngine, config.EngineR2T2)
	}
	return nil
}

func (m *Manager) releaseSession() {
	m.mu.Lock()
	m.sessionHeld = false
	m.lastActivity = m.opts.Now()
	m.mu.Unlock()
	m.publishStatus()
}

// SessionActive reports whether the realtime slot is held.
func (m *Manager) SessionActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionHeld
}

// OffsetError reports a PCM frame whose offset does not continue the session.
type OffsetError struct{ Expected, Got uint64 }

func (e *OffsetError) Error() string {
	return fmt.Sprintf("offset mismatch: expected %d, got %d", e.Expected, e.Got)
}

// Samples returns the number of samples accepted so far.
func (s *Session) Samples() uint64 { return s.samples }

// Push feeds one frame at realtime priority. offset must equal the number of
// samples already accepted (same check as R2T2StreamSession).
func (s *Session) Push(ctx context.Context, offset uint64, pcm []int16) (asr.Partial, error) {
	if offset != s.samples {
		return asr.Partial{}, &OffsetError{Expected: s.samples, Got: offset}
	}
	var p asr.Partial
	err := s.m.sched.Do(ctx, scheduler.Realtime, func() error {
		defer s.m.touch()
		var err error
		p, err = s.stream.Push(pcm)
		return err
	})
	if err == nil {
		s.samples += uint64(len(pcm))
	}
	return p, err
}

// Finish flushes the stream, returns the result and releases the slot.
func (s *Session) Finish(ctx context.Context) (asr.Result, error) {
	var r asr.Result
	err := s.m.sched.Do(ctx, scheduler.Realtime, func() error {
		defer s.m.touch()
		var err error
		r, err = s.stream.Finish()
		s.stream.Close()
		return err
	})
	s.release()
	return r, err
}

// Cancel closes the stream without a result and releases the slot.
func (s *Session) Cancel() {
	if s.released {
		return
	}
	// Use a fresh context: the slot must be released even if the caller's
	// context is already done.
	_ = s.m.sched.Do(context.Background(), scheduler.Realtime, func() error {
		s.stream.Close()
		return nil
	})
	s.release()
}

func (s *Session) release() {
	if s.released {
		return
	}
	s.released = true
	s.m.releaseSession()
}

// Status returns the current status (never touches the inference thread).
func (m *Manager) Status() Status {
	eff := m.opts.Store.Effective()
	desired := m.opts.Store.Desired()
	m.mu.Lock()
	defer m.mu.Unlock()
	_, local := asr.KindForEngine(desired.Engine)
	return Status{
		Backend:               m.opts.Backend.Name(),
		ConfiguredEngine:      desired.Engine,
		LocalEngine:           local,
		Loading:               m.loading,
		Info:                  m.info,
		PendingReload:         nonNil(m.opts.Store.Pending()),
		RealtimeSessionActive: m.sessionHeld,
		GPUIdleSeconds:        eff.GPUIdleSeconds,
		IdleUnloaded:          m.idleUnloaded,
		Scheduler:             m.sched.Stats(),
		LastError:             m.lastErr,
	}
}

func (m *Manager) publishStatus() {
	m.opts.Hub.Publish(events.TypeEngineStatus, m.Status())
}

// --- GPU idle unload -----------------------------------------------------------

func (m *Manager) idleLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.opts.IdleTick)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.idleTick()
		}
	}
}

// shouldIdleUnload mirrors gpu_idle_should_unload: enabled, a GPU model is
// loaded, no realtime session, nothing queued or running, and the last
// activity is older than the timeout.
// onThread is true when called from inside the unload job itself (which
// counts as "running").
func (m *Manager) shouldIdleUnload(onThread bool) bool {
	idle := m.opts.Store.Effective().GPUIdleSeconds
	if idle == 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionHeld || m.loading || !m.info.ModelLoaded || m.info.Device == "" || m.info.Device == "cpu" {
		return false
	}
	st := m.sched.Stats()
	if st.RealtimeQueued > 0 || st.BatchQueued > 0 || (st.Running && !onThread) {
		return false
	}
	return m.opts.Now().Sub(m.lastActivity) >= time.Duration(idle)*m.opts.IdleUnit
}

func (m *Manager) idleTick() {
	if !m.shouldIdleUnload(false) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = m.sched.Do(ctx, scheduler.Batch, func() error {
		// Re-check on the inference thread: a session may have started.
		if !m.shouldIdleUnload(true) {
			return nil
		}
		if err := m.opts.Backend.Unload(); err != nil {
			m.log.Warn("GPU idle unload failed", "err", err)
			return err
		}
		m.mu.Lock()
		m.idleUnloaded = true
		m.mu.Unlock()
		m.refreshInfoLocked()
		m.log.Info("GPU model unloaded after idle timeout")
		m.publishStatus()
		return nil
	})
}
