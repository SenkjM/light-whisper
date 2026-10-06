// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRaw(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func patch(kv map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		b, _ := json.Marshal(v)
		out[k] = b
	}
	return out
}

func TestDefaultsWhenMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"missing": "", "corrupt": "{not json", "array": "[1,2]"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name+".json")
			if content != "" {
				write(t, p, content)
			}
			s, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			snap, err := s.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if snap.Values != Defaults() {
				t.Fatalf("got %+v want defaults", snap.Values)
			}
			if snap.SchemaVersion != SchemaVersion {
				t.Fatalf("schema_version %d", snap.SchemaVersion)
			}
		})
	}
}

// Legacy engine.json written by the current Rust app must be read the same
// way paths.rs reads it.
func TestLegacyEngineJSONCompat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "engine.json")
	write(t, p, `{"engine":"qwen3-asr-1.7b","gpu_idle_seconds":" 180 ","models_dir":"/m",
		"glm_endpoint":"domestic","alibaba_region":"international","alibaba_model":"qwen3-asr-flash"}`)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v := s.Desired()
	if v.Engine != EngineR2T2 || v.GPUIdleSeconds != 180 || v.ModelsDir != "/m" || v.Device != "auto" {
		t.Fatalf("unexpected values %+v", v)
	}

	cases := map[string]uint64{`0`: 0, `-5`: 0, `"nope"`: 0, `true`: 0, `999999999`: MaxGPUIdleSeconds, `"60"`: 60, `1.5`: 0}
	for in, want := range cases {
		if got := ParseGPUIdleSeconds(json.RawMessage(in)); got != want {
			t.Errorf("ParseGPUIdleSeconds(%s) = %d want %d", in, got, want)
		}
	}

	write(t, p, `{"engine":"something-else","device":"tpu","log_level":7}`)
	v = s.Desired()
	if v.Engine != EngineQwen3 || v.Device != "auto" || v.LogLevel != "info" {
		t.Fatalf("garbage should fall back to defaults, got %+v", v)
	}
}

func TestPatchRevisionAndPersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "engine.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, p, `{"engine":"qwen3-asr-0.6b","glm_endpoint":"domestic","alibaba_model":"x"}`)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot()
	rev0 := snap.Revision

	if _, err := s.Patch("", patch(map[string]any{"gpu_idle_seconds": 30})); !errors.Is(err, ErrMissingRevision) {
		t.Fatalf("want ErrMissingRevision, got %v", err)
	}
	if _, err := s.Patch("stale", patch(map[string]any{"gpu_idle_seconds": 30})); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("want ErrRevisionMismatch, got %v", err)
	}

	res, err := s.Patch(rev0, patch(map[string]any{"gpu_idle_seconds": 30, "engine": EngineR2T2}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision == rev0 {
		t.Fatal("revision did not change")
	}
	if !reflect.DeepEqual(res.Applied, []string{"gpu_idle_seconds"}) {
		t.Fatalf("applied = %v", res.Applied)
	}
	if !reflect.DeepEqual(res.PendingReload, []string{"engine"}) {
		t.Fatalf("pending = %v", res.PendingReload)
	}
	sort.Strings(res.Changed)
	if !reflect.DeepEqual(res.Changed, []string{"engine", "gpu_idle_seconds"}) {
		t.Fatalf("changed = %v", res.Changed)
	}

	// Old revision is now stale.
	if _, err := s.Patch(rev0, patch(map[string]any{"log_level": "debug"})); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale revision accepted: %v", err)
	}

	raw := readRaw(t, p)
	if raw["glm_endpoint"] != "domestic" || raw["alibaba_model"] != "x" {
		t.Fatalf("shell-owned keys not preserved: %v", raw)
	}
	if raw["schema_version"] != float64(1) || raw["gpu_idle_seconds"] != float64(30) || raw["engine"] != EngineR2T2 {
		t.Fatalf("persisted %v", raw)
	}

	// Persistence across reopen; reload keys are active immediately on a fresh start.
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	snap2, _ := s2.Snapshot()
	if snap2.Revision != res.Revision || snap2.Values.Engine != EngineR2T2 || len(snap2.PendingReload) != 0 {
		t.Fatalf("reopen: %+v", snap2)
	}

	// Effective keeps the old reload value until Activate.
	if eff := s.Effective(); eff.Engine != EngineQwen3 || eff.GPUIdleSeconds != 30 {
		t.Fatalf("effective before activate %+v", eff)
	}
	s.Activate(s.Desired())
	if eff := s.Effective(); eff.Engine != EngineR2T2 || len(s.Pending()) != 0 {
		t.Fatalf("effective after activate %+v pending %v", eff, s.Pending())
	}

	// null resets to default and removes the key; models_dir "" removes too.
	res2, err := s.Patch(res.Revision, map[string]json.RawMessage{"gpu_idle_seconds": json.RawMessage("null")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readRaw(t, p)["gpu_idle_seconds"]; ok {
		t.Fatal("null did not remove key")
	}
	res3, err := s.Patch(res2.Revision, patch(map[string]any{"models_dir": filepath.Join(t.TempDir(), "models")}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patch(res3.Revision, patch(map[string]any{"models_dir": ""})); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRaw(t, p)["models_dir"]; ok {
		t.Fatal("empty models_dir not removed")
	}
}

func TestExternalEditChangesRevision(t *testing.T) {
	p := filepath.Join(t.TempDir(), "engine.json")
	s, _ := Open(p)
	snap, _ := s.Snapshot()
	write(t, p, `{"gpu_idle_seconds":99}`)
	if _, err := s.Patch(snap.Revision, patch(map[string]any{"log_level": "warn"})); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("external edit not detected: %v", err)
	}
}

func TestPatchValidation(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "engine.json"))
	snap, _ := s.Snapshot()
	_, err := s.Patch(snap.Revision, patch(map[string]any{
		"engine":           "whisper",
		"device":           "tpu",
		"gpu_idle_seconds": MaxGPUIdleSeconds + 1,
		"models_dir":       "relative/dir",
		"default_language": "zh CN",
		"log_level":        "trace",
		"glm_endpoint":     "domestic", // shell-owned: not patchable here
	}))
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	for _, k := range []string{"engine", "device", "gpu_idle_seconds", "models_dir", "default_language", "log_level", "glm_endpoint"} {
		if _, ok := verr.Fields[k]; !ok {
			t.Errorf("missing validation error for %s", k)
		}
	}
	for _, bad := range []any{-1, 1.5, "10"} {
		if _, err := s.Patch(snap.Revision, patch(map[string]any{"gpu_idle_seconds": bad})); !errors.As(err, &verr) {
			t.Errorf("gpu_idle_seconds=%v accepted", bad)
		}
	}
	// Nothing was written.
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid patch wrote the file")
	}
}

// The served JSON Schema must describe exactly the owned keys with the same
// apply modes and defaults.
func TestSchemaMatchesFields(t *testing.T) {
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           map[string]struct {
			Apply   Apply `json:"x-apply"`
			Default any   `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(SchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != len(fields) {
		t.Fatalf("schema has %d properties, code has %d", len(schema.Properties), len(fields))
	}
	defs, _ := json.Marshal(Defaults())
	var defMap map[string]any
	_ = json.Unmarshal(defs, &defMap)
	for _, f := range fields {
		p, ok := schema.Properties[f.key]
		if !ok {
			t.Errorf("schema missing %s", f.key)
			continue
		}
		if p.Apply != f.apply {
			t.Errorf("%s: schema x-apply %q, code %q", f.key, p.Apply, f.apply)
		}
		if !reflect.DeepEqual(p.Default, defMap[f.key]) {
			t.Errorf("%s: schema default %v, code %v", f.key, p.Default, defMap[f.key])
		}
	}
}
