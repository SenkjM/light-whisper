// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package config owns the Go engine's settings stored in engine.json.
//
// Compatibility rules with the current (Rust/Python) app:
//   - engine.json stays a flat JSON object in the app data directory.
//   - Keys the engine does not own (e.g. glm_endpoint, alibaba_region,
//     alibaba_model written by the Rust shell) are preserved verbatim.
//   - Legacy values are read leniently exactly like src-tauri/src/utils/paths.rs
//     (engine alias qwen3-asr-1.7b, gpu_idle_seconds as number or string,
//     clamped to MaxGPUIdleSeconds, garbage -> default).
//   - A "schema_version" key is added on write.
//
// The file on disk is the source of truth: every Snapshot/Patch re-reads it,
// so the revision always reflects what is persisted.
package config

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// SchemaVersion is the engine.json schema version written by this engine.
const SchemaVersion = 1

// MaxGPUIdleSeconds mirrors MAX_GPU_IDLE_SECONDS in paths.rs (7 days).
const MaxGPUIdleSeconds = 7 * 24 * 60 * 60

// Apply describes when a changed key takes effect.
type Apply string

const (
	ApplyLive   Apply = "live"
	ApplyReload Apply = "reload"
)

// Engine identifiers (same strings as the existing app).
const (
	EngineQwen3  = "qwen3-asr-0.6b"
	EngineR2T2   = "confucius4-r2t2"
	EngineGLM    = "glm-asr"
	EngineAliyun = "alibaba-asr"
)

//go:embed schema_v1.json
var schemaV1 []byte

// SchemaJSON returns the JSON Schema served at GET /v1/config/schema.
func SchemaJSON() []byte { return append([]byte(nil), schemaV1...) }

// Values is the typed view of the engine-owned settings.
type Values struct {
	Engine          string `json:"engine"`
	ModelsDir       string `json:"models_dir"`
	Device          string `json:"device"`
	GPUIdleSeconds  uint64 `json:"gpu_idle_seconds"`
	DefaultLanguage string `json:"default_language"`
	LogLevel        string `json:"log_level"`
	// DefaultPriorityVoice is the priority of voice transcription
	// (POST /v1/asr/transcribe) without an explicit priority.
	DefaultPriorityVoice string `json:"default_priority_voice"`
	// DefaultPriorityFile is the priority of file transcription jobs
	// (POST /v1/jobs) without an explicit priority.
	DefaultPriorityFile string `json:"default_priority_file"`
}

// IsOnlineEngine reports whether the engine is a cloud engine handled by the shell.
func IsOnlineEngine(engine string) bool { return engine == EngineGLM || engine == EngineAliyun }

type field struct {
	key   string
	apply Apply
	// lenient parses a value read from disk; ok=false means "use default".
	lenient func(json.RawMessage) (any, bool)
	// strict validates a value from PATCH.
	strict func(json.RawMessage) (any, error)
	def    any
	get    func(*Values) any
	set    func(*Values, any)
}

var fields = []field{
	{
		key: "engine", apply: ApplyReload, def: EngineQwen3,
		lenient: func(raw json.RawMessage) (any, bool) {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return nil, false
			}
			if s == "qwen3-asr-1.7b" {
				return EngineR2T2, true
			}
			return s, isEngine(s)
		},
		strict: func(raw json.RawMessage) (any, error) {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil || !isEngine(s) {
				return nil, fmt.Errorf("must be one of %s, %s, %s, %s", EngineQwen3, EngineR2T2, EngineGLM, EngineAliyun)
			}
			return s, nil
		},
		get: func(v *Values) any { return v.Engine },
		set: func(v *Values, x any) { v.Engine = x.(string) },
	},
	{
		key: "models_dir", apply: ApplyReload, def: "",
		lenient: func(raw json.RawMessage) (any, bool) {
			var s string
			return s, json.Unmarshal(raw, &s) == nil
		},
		strict: func(raw json.RawMessage) (any, error) {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, errors.New("must be a string")
			}
			if s != "" && !filepath.IsAbs(s) {
				return nil, errors.New("must be an absolute path or empty")
			}
			return s, nil
		},
		get: func(v *Values) any { return v.ModelsDir },
		set: func(v *Values, x any) { v.ModelsDir = x.(string) },
	},
	enumField("device", ApplyReload, "auto", []string{"auto", "cpu", "cuda", "vulkan"},
		func(v *Values) *string { return &v.Device }),
	{
		key: "gpu_idle_seconds", apply: ApplyLive, def: uint64(0),
		lenient: func(raw json.RawMessage) (any, bool) {
			return ParseGPUIdleSeconds(raw), true
		},
		strict: func(raw json.RawMessage) (any, error) {
			// Decode into any so a quoted "10" is rejected (json.Number
			// would accept it).
			var x any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			_ = dec.Decode(&x)
			n, isNum := x.(json.Number)
			if !isNum {
				return nil, errors.New("must be an integer")
			}
			u, err := strconv.ParseUint(n.String(), 10, 64)
			if err != nil || u > MaxGPUIdleSeconds {
				return nil, fmt.Errorf("must be an integer between 0 and %d", MaxGPUIdleSeconds)
			}
			return u, nil
		},
		get: func(v *Values) any { return v.GPUIdleSeconds },
		set: func(v *Values, x any) { v.GPUIdleSeconds = x.(uint64) },
	},
	{
		key: "default_language", apply: ApplyLive, def: "",
		lenient: func(raw json.RawMessage) (any, bool) {
			var s string
			if json.Unmarshal(raw, &s) != nil || validateLanguage(s) != nil {
				return nil, false
			}
			return s, true
		},
		strict: func(raw json.RawMessage) (any, error) {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, errors.New("must be a string")
			}
			if err := validateLanguage(s); err != nil {
				return nil, err
			}
			return s, nil
		},
		get: func(v *Values) any { return v.DefaultLanguage },
		set: func(v *Values, x any) { v.DefaultLanguage = x.(string) },
	},
	enumField("log_level", ApplyLive, "info", []string{"debug", "info", "warn", "error"},
		func(v *Values) *string { return &v.LogLevel }),
	// PLAN §3.2 / §4.6: 实时 / 优先 / 普通. Live dictation is always
	// realtime; file jobs cannot be realtime (they would interleave with a
	// live R2T2 session).
	enumField("default_priority_voice", ApplyLive, "high", []string{"realtime", "high", "normal"},
		func(v *Values) *string { return &v.DefaultPriorityVoice }),
	enumField("default_priority_file", ApplyLive, "normal", []string{"high", "normal"},
		func(v *Values) *string { return &v.DefaultPriorityFile }),
}

func enumField(key string, apply Apply, def string, allowed []string, ptr func(*Values) *string) field {
	ok := func(s string) bool {
		for _, a := range allowed {
			if s == a {
				return true
			}
		}
		return false
	}
	return field{
		key: key, apply: apply, def: def,
		lenient: func(raw json.RawMessage) (any, bool) {
			var s string
			if json.Unmarshal(raw, &s) != nil || !ok(s) {
				return nil, false
			}
			return s, true
		},
		strict: func(raw json.RawMessage) (any, error) {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil || !ok(s) {
				return nil, fmt.Errorf("must be one of %s", strings.Join(allowed, ", "))
			}
			return s, nil
		},
		get: func(v *Values) any { return *ptr(v) },
		set: func(v *Values, x any) { *ptr(v) = x.(string) },
	}
}

func isEngine(s string) bool {
	switch s {
	case EngineQwen3, EngineR2T2, EngineGLM, EngineAliyun:
		return true
	}
	return false
}

func validateLanguage(s string) error {
	if len(s) > 32 {
		return errors.New("must be at most 32 characters")
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("must not contain whitespace or control characters")
		}
	}
	return nil
}

// ParseGPUIdleSeconds mirrors parse_gpu_idle_seconds in paths.rs.
func ParseGPUIdleSeconds(raw json.RawMessage) uint64 {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return 0
	}
	var text string
	switch t := v.(type) {
	case json.Number:
		text = t.String()
	case string:
		text = strings.TrimSpace(t)
	default:
		return 0
	}
	u, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0
	}
	if u > MaxGPUIdleSeconds {
		return MaxGPUIdleSeconds
	}
	return u
}

// Defaults returns the default values.
func Defaults() Values {
	var v Values
	for _, f := range fields {
		f.set(&v, f.def)
	}
	return v
}

// Keys returns the engine-owned keys in declaration order.
func Keys() []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.key
	}
	return out
}

// ApplyMap returns key -> apply mode.
func ApplyMap() map[string]Apply {
	m := make(map[string]Apply, len(fields))
	for _, f := range fields {
		m[f.key] = f.apply
	}
	return m
}

func fieldByKey(key string) (field, bool) {
	for _, f := range fields {
		if f.key == key {
			return f, true
		}
	}
	return field{}, false
}

// Errors returned by Patch.
var (
	ErrRevisionMismatch = errors.New("config revision mismatch")
	ErrMissingRevision  = errors.New("If-Match revision required")
)

// ValidationError lists invalid keys in a patch.
type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return "invalid config: " + strings.Join(parts, "; ")
}

// Snapshot is the GET /v1/config payload.
type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	Revision      string           `json:"revision"`
	Values        Values           `json:"values"`
	Apply         map[string]Apply `json:"apply"`
	PendingReload []string         `json:"pending_reload"`
}

// PatchResult is the PATCH /v1/config payload.
type PatchResult struct {
	Revision      string   `json:"revision"`
	Applied       []string `json:"applied"`
	PendingReload []string `json:"pending_reload"`
	// Changed lists every key whose persisted value changed.
	Changed []string `json:"-"`
}

// Store reads and writes engine.json. It is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	// desired is what is persisted on disk.
	desired Values
	// active holds the values of reload keys the running engine uses.
	active Values
}

// Open loads (or initialises in memory) the store at path. A missing or
// corrupt file yields defaults, like the Rust reader; nothing is written
// until the first Patch.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	v, _, err := s.readDisk()
	if err != nil {
		return nil, err
	}
	s.desired = v
	s.active = v
	return s, nil
}

// Path returns the engine.json path.
func (s *Store) Path() string { return s.path }

func (s *Store) readDisk() (Values, map[string]json.RawMessage, error) {
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return Values{}, nil, fmt.Errorf("read %s: %w", s.path, err)
	default:
		if json.Unmarshal(data, &raw) != nil || raw == nil {
			// Valid JSON that is not an object, or invalid JSON: treat as empty
			// (same as engine_json_object_or_empty in paths.rs).
			raw = map[string]json.RawMessage{}
		}
	}
	v := Defaults()
	for _, f := range fields {
		if r, ok := raw[f.key]; ok {
			if x, ok := f.lenient(r); ok {
				f.set(&v, x)
			}
		}
	}
	return v, raw, nil
}

// refresh re-reads disk; must hold mu.
func (s *Store) refresh() (map[string]json.RawMessage, error) {
	v, raw, err := s.readDisk()
	if err != nil {
		return nil, err
	}
	s.desired = v
	return raw, nil
}

// Revision computes the revision token for values.
func Revision(v Values) string {
	b, _ := json.Marshal(struct {
		S int `json:"s"`
		V Values
	}{SchemaVersion, v})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (s *Store) pendingLocked() []string {
	out := []string{}
	for _, f := range fields {
		if f.apply == ApplyReload && f.get(&s.desired) != f.get(&s.active) {
			out = append(out, f.key)
		}
	}
	return out
}

// Snapshot returns the current persisted values.
func (s *Store) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.refresh(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		SchemaVersion: SchemaVersion,
		Revision:      Revision(s.desired),
		Values:        s.desired,
		Apply:         ApplyMap(),
		PendingReload: s.pendingLocked(),
	}, nil
}

// Effective returns the values the running engine should use: live keys from
// disk, reload keys as last activated.
func (s *Store) Effective() Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.refresh() // keep last known values on read error
	eff := s.desired
	for _, f := range fields {
		if f.apply == ApplyReload {
			f.set(&eff, f.get(&s.active))
		}
	}
	return eff
}

// Desired returns the persisted values (including not-yet-activated reload keys).
func (s *Store) Desired() Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.refresh()
	return s.desired
}

// Activate marks the given desired values as active (after a successful reload).
func (s *Store) Activate(v Values) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = v
}

// Pending returns reload keys whose persisted value is not active yet.
func (s *Store) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.refresh()
	return s.pendingLocked()
}

// Patch applies a merge patch (key -> value, null = reset to default) if
// ifMatch equals the current revision. Unknown keys are rejected.
func (s *Store) Patch(ifMatch string, patch map[string]json.RawMessage) (PatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ifMatch == "" {
		return PatchResult{}, ErrMissingRevision
	}
	raw, err := s.refresh()
	if err != nil {
		return PatchResult{}, err
	}
	if ifMatch != Revision(s.desired) {
		return PatchResult{}, ErrRevisionMismatch
	}

	verr := &ValidationError{Fields: map[string]string{}}
	next := s.desired
	type op struct {
		key    string
		remove bool
		val    any
	}
	var ops []op
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r := patch[k]
		f, ok := fieldByKey(k)
		if !ok {
			verr.Fields[k] = "unknown or not engine-owned key"
			continue
		}
		if bytes.Equal(bytes.TrimSpace(r), []byte("null")) {
			f.set(&next, f.def)
			ops = append(ops, op{key: k, remove: true})
			continue
		}
		x, err := f.strict(r)
		if err != nil {
			verr.Fields[k] = err.Error()
			continue
		}
		f.set(&next, x)
		ops = append(ops, op{key: k, val: x})
	}
	if len(verr.Fields) > 0 {
		return PatchResult{}, verr
	}

	var changed, applied []string
	for _, f := range fields {
		if f.get(&next) != f.get(&s.desired) {
			changed = append(changed, f.key)
			if f.apply == ApplyLive {
				applied = append(applied, f.key)
			}
		}
	}
	for _, o := range ops {
		// Resetting to default removes the key, like write_models_dir(None).
		if o.remove || (o.key == "models_dir" && o.val == "") {
			delete(raw, o.key)
			continue
		}
		b, _ := json.Marshal(o.val)
		raw[o.key] = b
	}
	sv, _ := json.Marshal(SchemaVersion)
	raw["schema_version"] = sv
	if err := writeAtomic(s.path, raw); err != nil {
		return PatchResult{}, err
	}
	s.desired = next
	if applied == nil {
		applied = []string{}
	}
	return PatchResult{
		Revision:      Revision(next),
		Applied:       applied,
		PendingReload: s.pendingLocked(),
		Changed:       changed,
	}, nil
}

func writeAtomic(path string, raw map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("encode engine.json: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.Rename replaces the target atomically (MoveFileEx with
	// MOVEFILE_REPLACE_EXISTING on Windows).
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
