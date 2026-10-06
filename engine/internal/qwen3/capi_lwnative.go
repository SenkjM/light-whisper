// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package qwen3

/*
#cgo linux LDFLAGS: -ldl
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

// Only the transcribe.cpp C ABI crosses this boundary: opaque handles, C
// strings, plain-old-data parameter structs and float buffers. No C++
// objects or CRT resources are shared, so a MinGW-built cgo binary can drive
// an MSVC-built transcribe.dll (PLAN §8 risk 2). Symbols are resolved at run
// time; the structs below are checked against transcribe_abi_struct_size
// before use, the same self-check the Python binding (_abi.py) performs.

typedef struct { uint64_t struct_size; int backend; int gpu_device; } lwt_model_load_params;
typedef struct { uint64_t struct_size; int n_threads; int kv_type; int32_t n_ctx; } lwt_session_params;
typedef struct {
	uint64_t struct_size;
	int task, timestamps, pnc, itn;
	const char *language, *target_language;
	bool keep_special_tags;
	void *family;
	int32_t spec_k_drafts;
} lwt_run_params;

typedef bool (*lwt_abort_fn)(void *);

typedef const char *(*lwt_fn_version)(void);
typedef int         (*lwt_fn_init_backends)(const char *);
typedef const char *(*lwt_fn_status_string)(int);
typedef size_t      (*lwt_fn_abi_struct_size)(int);
typedef void        (*lwt_fn_model_load_params_init)(lwt_model_load_params *);
typedef int         (*lwt_fn_model_load_file)(const char *, const lwt_model_load_params *, void **);
typedef void        (*lwt_fn_session_params_init)(lwt_session_params *);
typedef int         (*lwt_fn_session_init)(void *, const lwt_session_params *, void **);
typedef void        (*lwt_fn_run_params_init)(lwt_run_params *);
typedef int         (*lwt_fn_run)(void *, const float *, int, const lwt_run_params *);
typedef const char *(*lwt_fn_text)(void *);
typedef void        (*lwt_fn_set_abort_callback)(void *, lwt_abort_fn, void *);
typedef void        (*lwt_fn_free)(void *);

typedef struct {
#ifdef _WIN32
	HMODULE handle;
	DLL_DIRECTORY_COOKIE cookies[8];
	int ncookies;
#else
	void *handle;
#endif
	lwt_fn_version version;
	lwt_fn_init_backends init_backends;
	lwt_fn_status_string status_string;
	lwt_fn_abi_struct_size abi_struct_size;
	lwt_fn_model_load_params_init model_load_params_init;
	lwt_fn_model_load_file model_load_file;
	lwt_fn_session_params_init session_params_init;
	lwt_fn_session_init session_init;
	lwt_fn_run_params_init run_params_init;
	lwt_fn_run run;
	lwt_fn_text full_text, detected_language, model_backend;
	lwt_fn_set_abort_callback set_abort_callback;
	lwt_fn_free session_free, model_free;
} lwt_lib;

typedef struct {
	lwt_lib *lib;
	void *model, *session;
	int abort_flag; // accessed with __atomic builtins only
} lwt_session;

static void lwt_set(char *dst, size_t len, const char *src) {
	if (!dst || len == 0) return;
	if (!src) src = "unknown error";
	strncpy(dst, src, len - 1);
	dst[len - 1] = 0;
}

static void *lwt_sym(lwt_lib *l, const char *name) {
#ifdef _WIN32
	return (void *)GetProcAddress(l->handle, name);
#else
	return dlsym(l->handle, name);
#endif
}

#ifdef _WIN32
static wchar_t *lwt_wide(const char *s) {
	int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
	if (n <= 0) return NULL;
	wchar_t *w = (wchar_t *)malloc(sizeof(wchar_t) * n);
	if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	return w;
}
#endif

#define LWT_BIND(field, name) \
	if (!(l->field = (void *)lwt_sym(l, "transcribe_" name))) { lwt_set(err, len, "missing symbol transcribe_" name); return -1; }

static int lwt_bind(lwt_lib *l, char *err, size_t len) {
	LWT_BIND(version, "version");
	LWT_BIND(init_backends, "init_backends");
	LWT_BIND(status_string, "status_string");
	LWT_BIND(abi_struct_size, "abi_struct_size");
	LWT_BIND(model_load_params_init, "model_load_params_init");
	LWT_BIND(model_load_file, "model_load_file");
	LWT_BIND(session_params_init, "session_params_init");
	LWT_BIND(session_init, "session_init");
	LWT_BIND(run_params_init, "run_params_init");
	LWT_BIND(run, "run");
	LWT_BIND(full_text, "full_text");
	LWT_BIND(detected_language, "detected_language");
	LWT_BIND(model_backend, "model_backend");
	LWT_BIND(set_abort_callback, "set_abort_callback");
	LWT_BIND(session_free, "session_free");
	LWT_BIND(model_free, "model_free");
	return 0;
}

// lwt_open loads the library (Windows: the library's own directory and the
// given dependency directories are searched for its ggml / CUDA DLLs) and
// binds the symbols. The module is never unloaded, as with Python's ctypes:
// unloading a library that started GPU / OpenMP threads is not safe.
static lwt_lib *lwt_open(const char *path, const char **dirs, int ndirs, char *err, size_t len) {
	lwt_lib *l = (lwt_lib *)calloc(1, sizeof(lwt_lib));
	if (!l) { lwt_set(err, len, "out of memory"); return NULL; }
#ifdef _WIN32
	for (int i = 0; i < ndirs && l->ncookies < 8; i++) {
		wchar_t *w = lwt_wide(dirs[i]);
		DLL_DIRECTORY_COOKIE c = w ? AddDllDirectory(w) : NULL;
		free(w);
		if (c) l->cookies[l->ncookies++] = c;
	}
	wchar_t *wpath = lwt_wide(path);
	l->handle = wpath ? LoadLibraryExW(wpath, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS) : NULL;
	free(wpath);
	if (!l->handle) {
		char buf[64];
		snprintf(buf, sizeof buf, "LoadLibraryExW failed (error %lu)", (unsigned long)GetLastError());
		lwt_set(err, len, buf);
		free(l);
		return NULL;
	}
#else
	(void)dirs; (void)ndirs;
	l->handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (!l->handle) { lwt_set(err, len, dlerror()); free(l); return NULL; }
#endif
	if (lwt_bind(l, err, len) != 0) { free(l); return NULL; }
	return l;
}

static const char *lwt_version(lwt_lib *l) { return l->version(); }
static const char *lwt_status(lwt_lib *l, int s) { return l->status_string(s); }
static int lwt_init_backends(lwt_lib *l, const char *dir) { return l->init_backends(dir); }

// lwt_check_abi compares our struct sizes with the library's.
static int lwt_check_abi(lwt_lib *l, char *err, size_t len) {
	size_t want[3] = {sizeof(lwt_model_load_params), sizeof(lwt_session_params), sizeof(lwt_run_params)};
	const char *names[3] = {"transcribe_model_load_params", "transcribe_session_params", "transcribe_run_params"};
	for (int id = 0; id < 3; id++) {
		size_t got = l->abi_struct_size(id);
		if (got != want[id]) {
			char buf[160];
			snprintf(buf, sizeof buf, "%s: native size %zu != binding size %zu", names[id], got, want[id]);
			lwt_set(err, len, buf);
			return -1;
		}
	}
	return 0;
}

static bool lwt_abort_cb(void *ud) {
	return __atomic_load_n((int *)ud, __ATOMIC_ACQUIRE) != 0;
}

static void lwt_set_abort(lwt_session *s, int on) {
	__atomic_store_n(&s->abort_flag, on, __ATOMIC_RELEASE);
}

// Return codes: 0 OK, >0 transcribe.cpp status, -1 wrapper error (err set).
static int lwt_session_open(lwt_lib *l, const char *path, int backend, int n_threads, int kv_type, int n_ctx,
                            lwt_session **out, char *err, size_t len) {
	*out = NULL;
	lwt_session *s = (lwt_session *)calloc(1, sizeof(lwt_session));
	if (!s) { lwt_set(err, len, "out of memory"); return -1; }
	s->lib = l;
	lwt_model_load_params mp;
	memset(&mp, 0, sizeof mp);
	l->model_load_params_init(&mp);
	mp.backend = backend;
	int st = l->model_load_file(path, &mp, &s->model);
	if (st) { free(s); return st; }
	if (!s->model) { free(s); lwt_set(err, len, "model load returned a null handle"); return -1; }
	lwt_session_params sp;
	memset(&sp, 0, sizeof sp);
	l->session_params_init(&sp);
	sp.n_threads = n_threads;
	sp.kv_type = kv_type;
	sp.n_ctx = n_ctx;
	st = l->session_init(s->model, &sp, &s->session);
	if (st || !s->session) {
		l->model_free(s->model);
		free(s);
		if (!st) { lwt_set(err, len, "session init returned a null handle"); return -1; }
		return st;
	}
	l->set_abort_callback(s->session, lwt_abort_cb, &s->abort_flag);
	*out = s;
	return 0;
}

static const char *lwt_model_backend(lwt_session *s) { return s->lib->model_backend(s->model); }

// lwt_run: task transcribe, timestamps none, auto language, family defaults
// (transcribe_run_params_init), exactly like Session.run(audio, timestamps="none").
static int lwt_run(lwt_session *s, const float *pcm, int n) {
	lwt_run_params rp;
	memset(&rp, 0, sizeof rp);
	s->lib->run_params_init(&rp);
	rp.task = 0;
	rp.timestamps = 0;
	rp.language = NULL;
	rp.target_language = NULL;
	rp.keep_special_tags = false;
	rp.family = NULL;
	rp.spec_k_drafts = -1;
	return s->lib->run(s->session, pcm, n, &rp);
}

static const char *lwt_full_text(lwt_session *s) { return s->lib->full_text(s->session); }
static const char *lwt_detected_language(lwt_session *s) { return s->lib->detected_language(s->session); }

static void lwt_session_close(lwt_session *s) {
	if (!s) return;
	// Sessions are freed before their model (transcribe.h contract).
	if (s->session) s->lib->session_free(s->session);
	if (s->model) s->lib->model_free(s->model);
	free(s);
}
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// NativeAvailable reports whether this build can load transcribe.cpp.
const NativeAvailable = true

const errLen = 512

var (
	libMu    sync.Mutex
	libCache = map[string]*cLib{}
)

type cLib struct {
	l       *C.lwt_lib
	path    string
	version string
}

func cErr(buf []C.char) string { return C.GoString(&buf[0]) }

func baseVersion(v string) string {
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 4)
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ".")
}

// OpenLibrary loads transcribe.cpp from path, checks its base version and
// struct layout, and registers the ggml backend modules found next to it
// (transcribe_init_backends(<library dir>), as the Python binding does for
// its artifact directory). dllDirs are extra Windows dependency directories.
// A library is loaded and initialised once per process.
func OpenLibrary(path string, dllDirs []string) (CLib, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	libMu.Lock()
	defer libMu.Unlock()
	if l, ok := libCache[abs]; ok {
		return l, nil
	}
	cpath := C.CString(abs)
	defer C.free(unsafe.Pointer(cpath))
	cdirs := make([]*C.char, len(dllDirs))
	for i, d := range dllDirs {
		cdirs[i] = C.CString(d)
		defer C.free(unsafe.Pointer(cdirs[i]))
	}
	var dirsPtr **C.char
	if len(cdirs) > 0 {
		dirsPtr = (**C.char)(C.malloc(C.size_t(len(cdirs)) * C.size_t(unsafe.Sizeof(cdirs[0]))))
		defer C.free(unsafe.Pointer(dirsPtr))
		copy(unsafe.Slice(dirsPtr, len(cdirs)), cdirs)
	}
	ebuf := make([]C.char, errLen)
	l := C.lwt_open(cpath, dirsPtr, C.int(len(cdirs)), &ebuf[0], errLen)
	if l == nil {
		return nil, fmt.Errorf("load %s: %s", abs, cErr(ebuf))
	}
	version := C.GoString(C.lwt_version(l))
	if baseVersion(version) != LibraryVersion {
		return nil, fmt.Errorf("transcribe.cpp %s at %s: need base version %s", version, abs, LibraryVersion)
	}
	if C.lwt_check_abi(l, &ebuf[0], errLen) != 0 {
		return nil, fmt.Errorf("transcribe.cpp ABI layout check failed: %s", cErr(ebuf))
	}
	cdir := C.CString(filepath.Dir(abs))
	defer C.free(unsafe.Pointer(cdir))
	if st := C.lwt_init_backends(l, cdir); st != 0 {
		return nil, fmt.Errorf("no usable compute backend: transcribe_init_backends: %s (status %d)",
			C.GoString(C.lwt_status(l, st)), int(st))
	}
	lib := &cLib{l: l, path: abs, version: version}
	libCache[abs] = lib
	return lib, nil
}

func (c *cLib) Version() string { return c.version }

var backendIDs = map[string]C.int{"auto": 0, "cpu": 1, "metal": 2, "vulkan": 3, "cpu_accel": 4, "cuda": 5}
var kvTypeIDs = map[string]C.int{"auto": 0, "f32": 1, "f16": 2}

func (c *cLib) status(op string, st C.int, ebuf []C.char) error {
	if st == -1 {
		return fmt.Errorf("%s: %s", op, cErr(ebuf))
	}
	return &StatusError{Op: op, Status: int(st), Message: C.GoString(C.lwt_status(c.l, st))}
}

func (c *cLib) Open(modelPath, backend string, cfg SessionConfig) (Session, error) {
	bid, ok := backendIDs[backend]
	if !ok {
		return nil, fmt.Errorf("unknown transcribe.cpp backend %q", backend)
	}
	kv := cfg.KVType
	if kv == "" {
		kv = "auto"
	}
	kid, ok := kvTypeIDs[kv]
	if !ok {
		return nil, fmt.Errorf("unknown kv_type %q", kv)
	}
	cpath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cpath))
	ebuf := make([]C.char, errLen)
	var s *C.lwt_session
	if st := C.lwt_session_open(c.l, cpath, bid, C.int(cfg.NThreads), kid, C.int(cfg.NCtx), &s, &ebuf[0], errLen); st != 0 {
		return nil, c.status(fmt.Sprintf("loading model %q on %s", modelPath, backend), st, ebuf)
	}
	return &nativeSession{lib: c, s: s}, nil
}

type nativeSession struct {
	lib *cLib
	mu  sync.Mutex // guards s against Close racing SetAbort
	s   *C.lwt_session
}

func decode(p *C.char) string {
	if p == nil {
		return ""
	}
	return strings.ToValidUTF8(C.GoString(p), "\uFFFD")
}

func (n *nativeSession) Backend() string {
	if n.s == nil {
		return ""
	}
	return decode(C.lwt_model_backend(n.s))
}

func (n *nativeSession) Run(pcm []float32) (string, string, error) {
	if n.s == nil {
		return "", "", fmt.Errorf("transcribe.cpp session is closed")
	}
	var ptr *C.float
	if len(pcm) > 0 {
		ptr = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	st := C.lwt_run(n.s, ptr, C.int(len(pcm)))
	runtime.KeepAlive(pcm)
	if st != 0 {
		return "", "", n.lib.status("transcribe_run", st, nil)
	}
	return decode(C.lwt_full_text(n.s)), decode(C.lwt_detected_language(n.s)), nil
}

func (n *nativeSession) SetAbort(on bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.s == nil {
		return
	}
	v := C.int(0)
	if on {
		v = 1
	}
	C.lwt_set_abort(n.s, v)
}

func (n *nativeSession) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.s != nil {
		C.lwt_session_close(n.s)
		n.s = nil
	}
}
