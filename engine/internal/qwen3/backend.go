// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: GPL-3.0-only
//
// Go port of src-tauri/resources/qwen3_asr_server.py and the parts of
// hf_cache_utils.py / server_common.py it relies on (inherited light-whisper
// code, GPL-3.0-only): Hugging Face cache lookup of the pinned Q8 GGUF,
// runtime loading with the CUDA → Vulkan → CPU (or auto → CPU) fallback,
// warmup, the short-clip and VAD speech filters (outer silence trimmed,
// inner pauses kept) and whole-clip recognition. It implements asr.Backend.
//
// Per PLAN §10.4, logic translated from inherited GPL code stays
// GPL-3.0-only (combinable with the AGPL-3.0-only engine, PLAN §10.3). The
// whole-sentence realtime mode (sentence.go) and the C binding (capi*.go)
// are new code under AGPL-3.0-only.

package qwen3

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/vad"
)

// ModelPin is a pinned GGUF in hf_cache_utils.QWEN3_ASR_MODELS.
type ModelPin struct {
	RepoID, Filename, Revision, SHA256 string
	Size                               int64
}

// EngineID is the config engine id of Qwen3-ASR (the 1.7B id is migrated to
// R2T2 by the config store, so only 0.6B remains).
const EngineID = "qwen3-asr-0.6b"

// Model is the pinned Qwen3-ASR 0.6B Q8 GGUF.
var Model = ModelPin{
	RepoID:   "handy-computer/Qwen3-ASR-0.6B-gguf",
	Filename: "Qwen3-ASR-0.6B-Q8_0.gguf",
	Revision: "e4e16599b900eb0cb36e524514756bb92eb092b7",
	Size:     850_423_456,
	SHA256:   "f081b2d5e23bd669d92cc331d722a8a0681943b8e6f34b48996fd5c319b5acd8",
}

const (
	// NCtx is QWEN3_ASR_N_CTX.
	NCtx = 32_768
	// MinClipSamples: clips shorter than 0.5 s return empty text without
	// running VAD or the model (transcribe_audio's duration < 0.5 check).
	MinClipSamples = asr.SampleRate / 2
	// minWeightSize and completeManifest mirror hf_cache_utils.
	minWeightSize    = 1_000_000
	completeManifest = ".light_whisper_complete.json"
)

// ErrModelMissing: the pinned model file is not in the cache.
var ErrModelMissing = errors.New("Qwen3-ASR Q8 model not downloaded: " + Model.Filename)

// Detector is the VAD the backend needs (FireRedVAD in production).
type Detector interface {
	SpeechTimestamps(audio []float32) ([]vad.Segment, error)
	Close() error
}

// BackendOptions configure the native Qwen3 backend.
type BackendOptions struct {
	// LibraryPath is the transcribe.cpp shared library; its directory must
	// also hold the ggml backend modules (libggml-cpu-*.so, ggml-cuda.dll, …).
	LibraryPath string
	// DLLDirs are extra dependency directories (Windows), searched after the
	// library's own directory (e.g. the CUDA runtime directory).
	DLLDirs []string
	// Threads for the session (0 = library default, as in Python).
	Threads int
	// OpenLibrary loads transcribe.cpp (default: [OpenLibrary]).
	OpenLibrary func(path string, dllDirs []string) (CLib, error)
	// NewDetector creates the VAD (required).
	NewDetector func() (Detector, error)
	// ModelPath, when set, is used instead of the Hugging Face cache lookup
	// (development and tests).
	ModelPath string
	// HubCache overrides the cache root used when the load spec has no
	// models dir ("" = HF_HUB_CACHE, HF_HOME/hub, ~/.cache/huggingface/hub).
	HubCache string
	// HasNVIDIA probes for an NVIDIA driver (default: [HasNVIDIAGPU]).
	HasNVIDIA func() bool
}

// Backend is the native asr.Backend for Qwen3-ASR. Not safe for concurrent
// use: the engine calls it only from the inference thread.
type Backend struct {
	opts     BackendOptions
	spec     asr.LoadSpec
	session  Session
	detector Detector
	missing  bool
}

// NewBackend returns an unloaded backend.
func NewBackend(opts BackendOptions) *Backend {
	if opts.OpenLibrary == nil {
		opts.OpenLibrary = OpenLibrary
	}
	if opts.HasNVIDIA == nil {
		opts.HasNVIDIA = HasNVIDIAGPU
	}
	return &Backend{opts: opts}
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

// FindSnapshotFile mirrors find_hf_snapshot_file: the snapshot named by
// refs/main first, then every other snapshot; the file must be at least
// 1 MB and, when the snapshot has a completion manifest, match its size.
// Like Python, the SHA-256 is not re-hashed on every load.
func FindSnapshotFile(cacheRoot, repoID, filename string) (string, bool) {
	repoDir := filepath.Join(cacheRoot, "models--"+strings.ReplaceAll(repoID, "/", "--"))
	snapshots := filepath.Join(repoDir, "snapshots")
	if st, err := os.Stat(snapshots); err != nil || !st.IsDir() {
		return "", false
	}
	var names []string
	if ref, err := os.ReadFile(filepath.Join(repoDir, "refs", "main")); err == nil {
		names = append(names, strings.TrimSpace(string(ref)))
	}
	entries, _ := os.ReadDir(snapshots)
	for _, e := range entries {
		if len(names) == 0 || e.Name() != names[0] {
			names = append(names, e.Name())
		}
	}
	normalized := filepath.FromSlash(filename)
	for _, name := range names {
		snap := filepath.Join(snapshots, name)
		candidate := filepath.Join(snap, normalized)
		st, err := os.Stat(candidate)
		if err != nil || st.IsDir() || st.Size() < minWeightSize {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(snap, completeManifest)); err == nil {
			var manifest struct {
				Files []struct {
					Path string `json:"path"`
					Size *int64 `json:"size"`
				} `json:"files"`
			}
			if json.Unmarshal(data, &manifest) == nil {
				ok := false
				for _, f := range manifest.Files {
					if f.Path == filename {
						ok = f.Size != nil && *f.Size == st.Size()
						break
					}
				}
				if !ok {
					continue
				}
			}
			// An unreadable / invalid manifest counts as a legacy cache.
		}
		return candidate, true
	}
	return "", false
}

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
	return FindSnapshotFile(root, Model.RepoID, Model.Filename)
}

// PreferredBackends mirrors _load_runtime's order: with an NVIDIA driver
// (Python's device "cuda") CUDA, then Vulkan, then CPU; otherwise
// transcribe.cpp's "auto" choice, then CPU. The engine's device setting
// narrows this: "cpu" loads only the CPU backend and "vulkan" skips CUDA.
func PreferredBackends(device string, hasNVIDIA bool) []string {
	switch device {
	case "cpu":
		return []string{"cpu"}
	case "vulkan":
		return []string{"vulkan", "cpu"}
	case "cuda":
		return []string{"cuda", "vulkan", "cpu"}
	}
	if hasNVIDIA {
		return []string{"cuda", "vulkan", "cpu"}
	}
	return []string{"auto", "cpu"}
}

func (b *Backend) loadRuntime(modelPath, device string) (Session, error) {
	lib, err := b.opts.OpenLibrary(b.opts.LibraryPath, append([]string{filepath.Dir(b.opts.LibraryPath)}, b.opts.DLLDirs...))
	if err != nil {
		return nil, fmt.Errorf("transcribe.cpp unavailable: %w", err)
	}
	var errs []error
	for _, backend := range PreferredBackends(device, b.opts.HasNVIDIA()) {
		s, err := lib.Open(modelPath, backend, SessionConfig{NThreads: b.opts.Threads, KVType: "f16", NCtx: NCtx})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", backend, err))
			continue
		}
		return s, nil
	}
	return nil, fmt.Errorf("Qwen3-ASR runtime unavailable: %w", errors.Join(errs...))
}

// warmupAudio is one second of faint Gaussian noise (the Python warmup uses
// numpy's generator; the exact samples do not matter, the output is dropped).
func warmupAudio() []float32 {
	r := rand.New(rand.NewPCG(0, 0))
	out := make([]float32, asr.SampleRate)
	for i := range out {
		out[i] = float32(r.NormFloat64()) * 0.002
	}
	return out
}

// Load resolves the model, loads transcribe.cpp and the VAD, and warms
// both up (initialize). Warmup failures are logged by Python and ignored;
// here they are ignored too.
func (b *Backend) Load(spec asr.LoadSpec) error {
	if spec.Kind != asr.KindQwen3 {
		return asr.ErrWrongKind
	}
	if b.session != nil && b.spec == spec {
		return nil
	}
	b.closeRuntime()
	b.spec = spec
	modelPath, ok := b.resolveModelPath(spec.ModelsDir)
	if !ok {
		b.missing = true
		return ErrModelMissing
	}
	b.missing = false
	if b.opts.NewDetector == nil {
		return errors.New("qwen3: no VAD configured")
	}
	session, err := b.loadRuntime(modelPath, spec.Device)
	if err != nil {
		return err
	}
	// Idle unload keeps the VAD (_suspend_gpu_runtime); reuse it.
	if b.detector == nil {
		det, err := b.opts.NewDetector()
		if err != nil {
			session.Close()
			return fmt.Errorf("FireRedVAD: %w", err)
		}
		b.detector = det
	}
	b.session = session
	if w, ok := b.detector.(interface{ Warmup() error }); ok {
		_ = w.Warmup()
	}
	_, _, _ = session.Run(warmupAudio())
	return nil
}

func (b *Backend) closeRuntime() {
	if b.session != nil {
		b.session.Close()
		b.session = nil
	}
}

// Unload frees the model and session but keeps the CPU VAD, like
// _suspend_gpu_runtime (idle unload); Close also frees the VAD.
func (b *Backend) Unload() error {
	b.closeRuntime()
	return nil
}

// Close releases everything, including the VAD.
func (b *Backend) Close() error {
	b.closeRuntime()
	if b.detector != nil {
		err := b.detector.Close()
		b.detector = nil
		return err
	}
	return nil
}

// Info reports the loaded state.
func (b *Backend) Info() asr.Info {
	info := asr.Info{MissingModels: []string{}}
	if b.missing {
		info.MissingModels = []string{EngineID}
	}
	if b.session != nil {
		info.Engine = b.spec.Engine
		info.Kind = asr.KindQwen3
		// transcribe.cpp names the backend it actually runs on ("CPU",
		// "CUDA0", "Vulkan0"…); Python reported the requested name instead.
		info.Device = strings.ToLower(b.session.Backend())
		info.ModelLoaded = true
	}
	return info
}

// FilterSpeech mirrors _filter_speech: no VAD region → no speech; otherwise
// the audio from the first region's start to the last region's end, so
// pauses between regions are preserved. It returns the trimmed audio, the
// number of regions and the [start, end) bounds.
func FilterSpeech(detector Detector, audio []float32) ([]float32, int, [2]int, error) {
	chunks, err := detector.SpeechTimestamps(audio)
	if err != nil {
		return nil, 0, [2]int{}, err
	}
	if len(chunks) == 0 {
		return nil, 0, [2]int{}, nil
	}
	start := max(0, chunks[0].Start)
	end := min(len(audio), chunks[len(chunks)-1].End)
	if end <= start {
		return nil, 0, [2]int{}, nil
	}
	return audio[start:end], len(chunks), [2]int{start, end}, nil
}

// recognize is transcribe_audio after decoding: the short-clip filter, the
// VAD filter, one run, text stripped, language "unknown" when not detected.
// interrupt, when closed, aborts the native run at its next chunk / decode
// boundary (asr.ErrInterrupted).
func (b *Backend) recognize(audio []float32, interrupt <-chan struct{}) (text, language string, err error) {
	if len(audio) < MinClipSamples {
		return "", "", nil
	}
	speech, n, _, err := FilterSpeech(b.detector, audio)
	if err != nil {
		return "", "", err
	}
	if n == 0 {
		return "", "unknown", nil
	}
	text, language, err = b.run(speech, interrupt)
	if err != nil {
		return "", "", err
	}
	text = strings.TrimSpace(text)
	if language == "" {
		language = "unknown"
	}
	return text, language, nil
}

// run calls Session.Run with an optional interrupt watcher. The abort flag
// is raised only while this run is in flight and cleared before returning,
// so a late interrupt cannot leak into the next (realtime) call.
func (b *Backend) run(audio []float32, interrupt <-chan struct{}) (string, string, error) {
	s := b.session
	if interrupt == nil {
		return s.Run(audio)
	}
	select {
	case <-interrupt:
		return "", "", asr.ErrInterrupted
	default:
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-interrupt:
			s.SetAbort(true)
		case <-done:
		}
	}()
	text, language, err := s.Run(audio)
	close(done)
	wg.Wait()
	s.SetAbort(false)
	if errors.Is(err, ErrAborted) {
		return "", "", fmt.Errorf("%w: %v", asr.ErrInterrupted, err)
	}
	return text, language, err
}

// Transcribe is whole-clip recognition (transcribe_audio). The language,
// context and hot-word options are ignored, as in the Python server.
func (b *Backend) Transcribe(pcm []int16, opts asr.TranscribeOptions) (asr.Result, error) {
	if b.session == nil {
		return asr.Result{}, asr.ErrNotLoaded
	}
	text, language, err := b.recognize(vad.PCM16ToFloat(pcm), opts.Interrupt)
	if err != nil {
		return asr.Result{}, err
	}
	return asr.Result{Text: text, Language: language, SampleCount: len(pcm)}, nil
}

// SpeechSegments runs the VAD over a whole file (file jobs).
func (b *Backend) SpeechSegments(pcm []int16) ([]asr.Span, error) {
	if b.detector == nil {
		return nil, asr.ErrNotLoaded
	}
	regions, err := b.detector.SpeechTimestamps(vad.PCM16ToFloat(pcm))
	if err != nil {
		return nil, err
	}
	out := make([]asr.Span, len(regions))
	for i, r := range regions {
		out[i] = asr.Span{Start: r.Start, End: r.End}
	}
	return out, nil
}

// NewStream opens a whole-sentence realtime session (sentence.go).
func (b *Backend) NewStream(opts asr.StreamOptions) (asr.Stream, error) {
	if b.session == nil {
		return nil, asr.ErrNotLoaded
	}
	return NewSentenceStream(b.detector, func(audio []float32) (string, string, error) {
		return b.recognize(audio, nil)
	}, DefaultSentenceOptions()), nil
}
