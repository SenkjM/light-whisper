// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of the R2T2 parts of src-tauri/resources/r2t2_asr_server.py and
// hf_cache_utils.py (inherited light-whisper code, GPL-3.0-only): pinned-model
// verification in the Hugging Face cache, runtime loading with CUDA → CPU
// fallback and CUDA warmup, context composition from hot words, the realtime
// session and whole-clip transcription through a fresh segmented session.
// It implements asr.Backend for the engine.
//
// Per PLAN §10.4, logic translated from inherited GPL code is not labelled
// as newly written AGPL code; this file stays GPL-3.0-only (combinable with
// the AGPL-3.0-only engine, PLAN §10.3).

package r2t2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// Model is the pinned R2T2 GGUF (hf_cache_utils.R2T2_MODEL).
var Model = struct {
	RepoID, Filename, Revision, SHA256 string
	Size                               int64
}{
	RepoID:   "davidxifeng/Confucius4-R2T2-gguf",
	Filename: "r2t2-q8_0.gguf",
	Revision: "a8e6b385d7df7eae9519363e07034a209004797a",
	Size:     2_477_512_064,
	SHA256:   "19f5ccd624484bcb5d44301437de41560b0ecc40c430e8850dfeefefbe82ccf5",
}

// EngineID is the config engine id of R2T2.
const EngineID = "confucius4-r2t2"

// Errors returned by the backend.
var (
	// ErrModelMissing: the pinned model is absent or fails verification.
	ErrModelMissing = errors.New("R2T2 Q8 model not downloaded or failed verification")
	// ErrStreamBusy: whole-clip transcription while a live stream is active
	// (Python "stream_busy"; the manager maps it to 409 earlier).
	ErrStreamBusy = errors.New("R2T2 stream session is active")
	// ErrQwen3NotImplemented: the native Qwen3 path arrives in migration step 4.
	ErrQwen3NotImplemented = errors.New("native Qwen3 backend not implemented yet (migration step 4)")
)

// Detector is a SpeechDetector that holds native resources.
type Detector interface {
	SpeechDetector
	Close() error
}

// BackendOptions configure the native R2T2 backend.
type BackendOptions struct {
	// RuntimeDir holds the audio.cpp builds in cuda/ and cpu/ subdirectories
	// (src-tauri/resources/r2t2-native in the Python layout).
	RuntimeDir string
	// LibraryName is the file name inside each backend directory; "" uses
	// audiocpp.dll / libaudiocpp.so / libaudiocpp.dylib.
	LibraryName string
	// DLLDirs are extra dependency directories (Windows), searched after the
	// library's own directory. The Python server adds RuntimeDir's parent.
	DLLDirs []string
	// Threads for the native session (0 = 4).
	Threads int
	// OpenLibrary loads audio.cpp (default: [OpenLibrary]).
	OpenLibrary func(path string, dllDirs []string) (CLib, error)
	// NewDetector creates the VAD (required).
	NewDetector func() (Detector, error)
	// ModelPath, when set, is used as the model file without the pin check
	// (development and tests only; production resolves the pinned model).
	ModelPath string
	// HubCache overrides the Hugging Face cache root used when the load
	// spec has no models dir ("" = HF_HUB_CACHE, HF_HOME/hub, ~/.cache/huggingface/hub).
	HubCache string
}

// Backend is the native asr.Backend for R2T2 (one loaded model, one live
// session slot). Not safe for concurrent use: the engine calls it only from
// the inference thread.
type Backend struct {
	opts     BackendOptions
	spec     asr.LoadSpec
	native   *Runtime
	detector Detector
	stream   *StreamSession
	missing  bool
	verified string // verified model path (hashing 2.5 GB happens once)
}

// NewBackend returns an unloaded backend.
func NewBackend(opts BackendOptions) *Backend {
	if opts.OpenLibrary == nil {
		opts.OpenLibrary = OpenLibrary
	}
	if opts.Threads == 0 {
		opts.Threads = 4
	}
	return &Backend{opts: opts}
}

// DefaultLibraryName is the audio.cpp shared library name on this OS.
func DefaultLibraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "audiocpp.dll"
	case "darwin":
		return "libaudiocpp.dylib"
	}
	return "libaudiocpp.so"
}

func (b *Backend) Name() string { return "native" }

// HubCacheRoot mirrors get_hf_cache_root.
func HubCacheRoot() string {
	if v := os.Getenv("HF_HUB_CACHE"); v != "" {
		return v
	}
	if v := os.Getenv("HF_HOME"); v != "" {
		return filepath.Join(v, "hub")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "huggingface", "hub")
}

// ModelPath is where the pinned model lives under a hub cache root.
func ModelPath(hubCache string) string {
	return filepath.Join(hubCache, "models--"+strings.ReplaceAll(Model.RepoID, "/", "--"),
		"snapshots", Model.Revision, Model.Filename)
}

// resolveModelPath mirrors _resolve_model_path: size and SHA-256 must match
// the pin; a verified path is cached.
func (b *Backend) resolveModelPath(modelsDir string) (string, bool) {
	if b.opts.ModelPath != "" {
		st, err := os.Stat(b.opts.ModelPath)
		return b.opts.ModelPath, err == nil && st.Mode().IsRegular()
	}
	root := modelsDir
	if root == "" {
		root = b.opts.HubCache
	}
	if root == "" {
		root = HubCacheRoot()
	}
	candidate := ModelPath(root)
	if b.verified == candidate {
		if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
			return candidate, true
		}
	}
	st, err := os.Stat(candidate)
	if err != nil || st.Size() != Model.Size {
		return "", false
	}
	f, err := os.Open(candidate)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), Model.SHA256) {
		return "", false
	}
	b.verified = candidate
	return candidate, true
}

// loadRuntime mirrors _load_runtime: CUDA first, then CPU; CUDA pays its
// first setup cost (one silent chunk, then reset) before reporting ready.
// Unlike Python, device "cpu" in the load spec skips the CUDA attempt.
func (b *Backend) loadRuntime(modelPath, device string) (*Runtime, error) {
	preferred := []string{"cuda", "cpu"}
	if device == "cpu" {
		preferred = []string{"cpu"}
	}
	name := b.opts.LibraryName
	if name == "" {
		name = DefaultLibraryName()
	}
	var errs []error
	for _, backend := range preferred {
		dir := filepath.Join(b.opts.RuntimeDir, backend)
		lib, err := b.opts.OpenLibrary(filepath.Join(dir, name), append([]string{dir}, b.opts.DLLDirs...))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", backend, err))
			continue
		}
		chunkMs := 320
		if backend == "cuda" {
			// Paired paced playback improved preview cadence on CUDA; keep
			// the larger batch for slower CPU inference.
			chunkMs = 160
		}
		rt, err := NewRuntime(lib, modelPath, RuntimeOptions{Backend: backend, ChunkMs: chunkMs, Threads: b.opts.Threads, Rolling: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", backend, err))
			continue
		}
		if backend == "cuda" {
			// Reset even on failure; warmup must not enter a recording.
			err := rt.Start("", "")
			if err == nil {
				_, _, err = rt.Feed(make([]float32, rt.ChunkSamples()))
			}
			if rerr := rt.Reset(); err == nil {
				err = rerr
			}
			if err != nil {
				rt.Close()
				errs = append(errs, fmt.Errorf("%s warmup: %w", backend, err))
				continue
			}
		}
		return rt, nil
	}
	return nil, fmt.Errorf("R2T2 native runtime unavailable: %w", errors.Join(errs...))
}

// Load verifies the model, loads the native runtime and the VAD, and builds
// the live segmented session (initialize).
func (b *Backend) Load(spec asr.LoadSpec) error {
	if spec.Kind != asr.KindR2T2 {
		return ErrQwen3NotImplemented
	}
	if b.native != nil && b.spec == spec {
		return nil
	}
	b.closeAll()
	b.spec = spec
	modelPath, ok := b.resolveModelPath(spec.ModelsDir)
	if !ok {
		b.missing = true
		return ErrModelMissing
	}
	b.missing = false
	if b.opts.NewDetector == nil {
		return errors.New("r2t2: no VAD configured")
	}
	native, err := b.loadRuntime(modelPath, spec.Device)
	if err != nil {
		return err
	}
	detector, err := b.opts.NewDetector()
	if err != nil {
		native.Close()
		return fmt.Errorf("FireRedVAD: %w", err)
	}
	b.native = native
	b.detector = detector
	b.stream = NewStreamSession(NewSegmented(native, detector, DefaultSegmentOptions(native.ChunkSamples())))
	return nil
}

func (b *Backend) closeAll() {
	if b.stream != nil {
		_ = b.stream.Close()
		b.stream = nil
	}
	if b.native != nil {
		b.native.Close()
		b.native = nil
	}
	if b.detector != nil {
		_ = b.detector.Close()
		b.detector = nil
	}
}

// Unload frees the native model and the VAD. The manager refuses to unload
// while a realtime session holds the slot (like _suspend_gpu_runtime).
func (b *Backend) Unload() error {
	b.closeAll()
	return nil
}

// Info reports the loaded state.
func (b *Backend) Info() asr.Info {
	info := asr.Info{MissingModels: []string{}}
	if b.missing {
		info.MissingModels = []string{EngineID}
	}
	if b.native != nil {
		info.Engine = b.spec.Engine
		info.Kind = asr.KindR2T2
		info.Device = b.native.Backend()
		info.ModelLoaded = true
	}
	return info
}

// NormalizeLanguage mirrors _normalize_language ("" = None).
func NormalizeLanguage(language string) string { return strings.TrimSpace(language) }

// ComposeContext mirrors _compose_context: the topic context, then
// "Hotwords: a, b" on its own line.
func ComposeContext(context string, hotWords []string) string {
	var parts []string
	if c := strings.TrimSpace(context); c != "" {
		parts = append(parts, c)
	}
	var words []string
	for _, w := range hotWords {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	if len(words) != 0 {
		parts = append(parts, "Hotwords: "+strings.Join(words, ", "))
	}
	return strings.Join(parts, "\n")
}

// PCM16ToFloat mirrors _decode_stream_audio's int16 → float32 scaling.
func PCM16ToFloat(pcm []int16) []float32 { return vad.PCM16ToFloat(pcm) }

// Transcribe mirrors transcribe_audio: the clip goes in native-sized chunks
// through a fresh segmented session sharing the resident runtime.
func (b *Backend) Transcribe(pcm []int16, opts asr.TranscribeOptions) (asr.Result, error) {
	if b.native == nil {
		return asr.Result{}, asr.ErrNotLoaded
	}
	if b.stream.Active() {
		return asr.Result{}, ErrStreamBusy
	}
	context := ComposeContext(opts.Context, opts.HotWords)
	language := NormalizeLanguage(opts.Language)
	text, detected, err := TranscribeClip(b.native, b.detector, PCM16ToFloat(pcm), context, language)
	if err != nil {
		return asr.Result{}, err
	}
	if detected != "" {
		language = detected
	}
	return asr.Result{Text: text, Language: language, SampleCount: len(pcm)}, nil
}

// TranscribeClip runs audio through a new Segmented session over native.
func TranscribeClip(native *Runtime, detector SpeechDetector, audio []float32, context, language string) (string, string, error) {
	chunk := max(1, native.ChunkSamples())
	seg := NewSegmented(native, detector, DefaultSegmentOptions(chunk))
	text, detected, err := func() (string, string, error) {
		if err := seg.Start(context, language); err != nil {
			return "", "", err
		}
		for start := 0; start < len(audio); start += chunk {
			if _, _, err := seg.Feed(audio[start:min(start+chunk, len(audio))]); err != nil {
				return "", "", err
			}
		}
		return seg.Finish()
	}()
	if err != nil {
		_ = seg.Reset()
		return "", "", err
	}
	return text, detected, nil
}

// NewStream opens the live session (stream_start).
func (b *Backend) NewStream(opts asr.StreamOptions) (asr.Stream, error) {
	if b.native == nil {
		return nil, asr.ErrNotLoaded
	}
	context := ComposeContext(opts.Context, opts.HotWords)
	language := NormalizeLanguage(opts.Language)
	if _, err := b.stream.Start(opts.SessionID, context, language); err != nil {
		return nil, err
	}
	return &liveStream{sess: b.stream, id: opts.SessionID}, nil
}

// liveStream adapts StreamSession to asr.Stream.
type liveStream struct {
	sess    *StreamSession
	id      uint64
	samples int
}

func (s *liveStream) Push(pcm []int16) (asr.Partial, error) {
	resp, err := s.sess.Feed(s.id, PCM16ToFloat(pcm), s.samples)
	if err != nil {
		return asr.Partial{}, err
	}
	s.samples = resp.SampleCount
	return asr.Partial{Committed: resp.Text, Tentative: resp.TentativeText}, nil
}

func (s *liveStream) Finish() (asr.Result, error) {
	resp, err := s.sess.Finish(s.id)
	if err != nil {
		return asr.Result{}, err
	}
	return asr.Result{Text: resp.Text, Language: resp.Language, SampleCount: resp.SampleCount}, nil
}

// Close cancels the session if it is still the active one (stream_cancel).
func (s *liveStream) Close() {
	if s.sess.ActiveID() == s.id {
		_, _ = s.sess.Cancel(s.id)
	}
}
