// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build lwnative && cgo

package r2t2

/*
#cgo linux LDFLAGS: -ldl
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

// Only the audio.cpp C ABI (include/audiocpp.h, ABI 0.3+) crosses this
// boundary: opaque handles, C strings and float buffers. No C++ objects or
// CRT resources are shared, so a MinGW-built cgo binary can drive an
// MSVC-built audiocpp.dll (PLAN §8 risk 2). Every allocation is freed by the
// library that made it. Symbols are resolved at run time, so neither the
// header nor an import library is needed to build.

typedef struct { const char *family_hint, *config_id, *weight_id, *model_spec_override; } lw_model_config;
typedef struct { const char *backend; int device; int threads; } lw_backend_config;

typedef uint32_t    (*lw_fn_abi_version)(void);
typedef const char *(*lw_fn_last_error)(void);
typedef int         (*lw_fn_registry_create)(const char *, void **);
typedef int         (*lw_fn_model_load)(void *, const char *, const lw_model_config *, const void *, void **);
typedef void       *(*lw_fn_options_create)(void);
typedef int         (*lw_fn_options_set)(void *, const char *, const char *);
typedef int         (*lw_fn_session_create)(const void *, const char *, const char *, const lw_backend_config *, const void *, void **);
typedef void       *(*lw_fn_request_create)(void);
typedef int         (*lw_fn_request_set_text)(void *, const char *, const char *);
typedef int         (*lw_fn_stream_start)(void *, const void *);
typedef int         (*lw_fn_stream_push)(void *, const float *, size_t, int, int, int64_t, void **);
typedef int         (*lw_fn_stream_finish)(void *, void **);
typedef int         (*lw_fn_stream_reset)(void *);
typedef const void *(*lw_fn_event_as_result)(const void *);
typedef int         (*lw_fn_result_text)(const void *, const char **, const char **);
typedef int         (*lw_fn_result_preview_text)(const void *, const char **);
typedef void        (*lw_fn_free)(void *);

typedef struct {
#ifdef _WIN32
	HMODULE handle;
	DLL_DIRECTORY_COOKIE cookies[8];
	int ncookies;
#else
	void *handle;
#endif
	lw_fn_abi_version abi_version;
	lw_fn_last_error last_error;
	lw_fn_registry_create registry_create;
	lw_fn_model_load model_load;
	lw_fn_options_create options_create;
	lw_fn_options_set options_set;
	lw_fn_session_create session_create;
	lw_fn_request_create request_create;
	lw_fn_request_set_text request_set_text;
	lw_fn_stream_start stream_start;
	lw_fn_stream_push stream_push;
	lw_fn_stream_finish stream_finish;
	lw_fn_stream_reset stream_reset;
	lw_fn_event_as_result event_as_result;
	lw_fn_result_text result_text;
	lw_fn_result_preview_text result_preview_text;
	lw_fn_free registry_free, model_free, session_free, options_free, request_free, result_free, event_free;
	void *registry, *model, *session;
} lw_lib;

static void lw_set(char *dst, size_t len, const char *src) {
	if (!dst || len == 0) return;
	if (!src) src = "unknown error";
	strncpy(dst, src, len - 1);
	dst[len - 1] = 0;
}

static void lw_detail(lw_lib *l, char *err, size_t len) {
	lw_set(err, len, l->last_error ? l->last_error() : NULL);
}

static void *lw_sym(lw_lib *l, const char *name) {
#ifdef _WIN32
	return (void *)GetProcAddress(l->handle, name);
#else
	return dlsym(l->handle, name);
#endif
}

static void lw_close_handle(lw_lib *l) {
#ifdef _WIN32
	for (int i = 0; i < l->ncookies; i++) RemoveDllDirectory(l->cookies[i]);
	l->ncookies = 0;
#endif
	// The module itself stays loaded, as with Python's ctypes: unloading a
	// library that started CUDA / OpenMP worker threads is not safe.
}

#ifdef _WIN32
static wchar_t *lw_wide(const char *s) {
	int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
	if (n <= 0) return NULL;
	wchar_t *w = (wchar_t *)malloc(sizeof(wchar_t) * n);
	if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	return w;
}
#endif

// lw_open loads the library and resolves only the version and error
// symbols, so an old ABI is rejected before newer symbols are looked up.
static lw_lib *lw_open(const char *path, const char **dirs, int ndirs, char *err, size_t len) {
	lw_lib *l = (lw_lib *)calloc(1, sizeof(lw_lib));
	if (!l) { lw_set(err, len, "out of memory"); return NULL; }
#ifdef _WIN32
	for (int i = 0; i < ndirs && l->ncookies < 8; i++) {
		wchar_t *w = lw_wide(dirs[i]);
		DLL_DIRECTORY_COOKIE c = w ? AddDllDirectory(w) : NULL;
		free(w);
		if (c) l->cookies[l->ncookies++] = c;
	}
	wchar_t *wpath = lw_wide(path);
	l->handle = wpath ? LoadLibraryExW(wpath, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS) : NULL;
	free(wpath);
	if (!l->handle) {
		char buf[64];
		snprintf(buf, sizeof buf, "LoadLibraryExW failed (error %lu)", (unsigned long)GetLastError());
		lw_set(err, len, buf);
		lw_close_handle(l);
		free(l);
		return NULL;
	}
#else
	(void)dirs; (void)ndirs;
	l->handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (!l->handle) { lw_set(err, len, dlerror()); free(l); return NULL; }
#endif
	l->abi_version = (lw_fn_abi_version)lw_sym(l, "audiocpp_abi_version");
	l->last_error = (lw_fn_last_error)lw_sym(l, "audiocpp_last_error");
	if (!l->abi_version || !l->last_error) {
		lw_set(err, len, "missing audiocpp_abi_version / audiocpp_last_error");
		lw_close_handle(l);
		free(l);
		return NULL;
	}
	return l;
}

static uint32_t lw_abi(lw_lib *l) { return l->abi_version(); }

#define LW_BIND(field, name) \
	if (!(l->field = (void *)lw_sym(l, "audiocpp_" name))) { lw_set(err, len, "missing symbol audiocpp_" name); return -1; }

static int lw_bind(lw_lib *l, char *err, size_t len) {
	LW_BIND(registry_create, "registry_create");
	LW_BIND(model_load, "model_load");
	LW_BIND(options_create, "options_create");
	LW_BIND(options_set, "options_set");
	LW_BIND(session_create, "session_create");
	LW_BIND(request_create, "request_create");
	LW_BIND(request_set_text, "request_set_text");
	LW_BIND(stream_start, "stream_start");
	LW_BIND(stream_push, "stream_push");
	LW_BIND(stream_finish, "stream_finish");
	LW_BIND(stream_reset, "stream_reset");
	LW_BIND(event_as_result, "event_as_result");
	LW_BIND(result_text, "result_text");
	LW_BIND(result_preview_text, "result_preview_text");
	LW_BIND(registry_free, "registry_free");
	LW_BIND(model_free, "model_free");
	LW_BIND(session_free, "session_free");
	LW_BIND(options_free, "options_free");
	LW_BIND(request_free, "request_free");
	LW_BIND(result_free, "result_free");
	LW_BIND(event_free, "event_free");
	return 0;
}

// Return codes of the wrappers: >0 audio.cpp status, -1 wrapper error (err set).
static int lw_registry_create(lw_lib *l, char *err, size_t len) {
	int s = l->registry_create(NULL, &l->registry);
	if (s) lw_detail(l, err, len);
	return s;
}

static int lw_model_load(lw_lib *l, const char *path, const char *family, char *err, size_t len) {
	lw_model_config cfg = {family, NULL, NULL, NULL};
	int s = l->model_load(l->registry, path, &cfg, NULL, &l->model);
	if (s) lw_detail(l, err, len);
	return s;
}

static void *lw_options_create(lw_lib *l) { return l->options_create(); }
static void lw_options_free(lw_lib *l, void *o) { l->options_free(o); }

static int lw_options_set(lw_lib *l, void *o, const char *k, const char *v, char *err, size_t len) {
	int s = l->options_set(o, k, v);
	if (s) lw_detail(l, err, len);
	return s;
}

static int lw_session_create(lw_lib *l, const char *backend, int threads, void *o, char *err, size_t len) {
	lw_backend_config cfg = {backend, 0, threads};
	int s = l->session_create(l->model, "asr", "streaming", &cfg, o, &l->session);
	if (s) lw_detail(l, err, len);
	return s;
}

// lw_stream_start: 0, an audio.cpp status, or -1 when the request cannot be
// allocated. which receives the failing call (1 request_set_text, 2 stream_start).
static int lw_stream_start(lw_lib *l, const char *context, const char *language, int *which, char *err, size_t len) {
	void *req = l->request_create();
	if (!req) { lw_set(err, len, "Cannot allocate audio.cpp request"); return -1; }
	*which = 1;
	int s = l->request_set_text(req, context, language);
	if (!s) { *which = 2; s = l->stream_start(l->session, req); }
	if (s) lw_detail(l, err, len);
	l->request_free(req);
	return s;
}

typedef struct {
	int has_event;
	int text_status;
	const char *text;
	const char *language;
	int preview_status;
	const char *preview;
	void *event;
} lw_push_out;

// lw_stream_push pushes one chunk and reads the event's delta and preview.
// The strings stay valid until lw_event_free(out->event).
static int lw_stream_push(lw_lib *l, const float *samples, size_t n, int64_t offset, lw_push_out *out, char *err, size_t len) {
	memset(out, 0, sizeof *out);
	int s = l->stream_push(l->session, samples, n, 16000, 1, offset, &out->event);
	if (s) { lw_detail(l, err, len); return s; }
	if (!out->event) return 0;
	out->has_event = 1;
	const void *res = l->event_as_result(out->event);
	out->text_status = l->result_text(res, &out->text, &out->language);
	if (out->text_status != 0 && out->text_status != 7) { lw_detail(l, err, len); return 0; }
	out->preview_status = l->result_preview_text(res, &out->preview);
	return 0;
}

static void lw_event_free(lw_lib *l, void *ev) { if (ev) l->event_free(ev); }

typedef struct {
	int text_status;
	const char *text;
	const char *language;
	void *result;
} lw_finish_out;

static int lw_stream_finish(lw_lib *l, lw_finish_out *out, char *err, size_t len) {
	memset(out, 0, sizeof *out);
	int s = l->stream_finish(l->session, &out->result);
	if (s) { lw_detail(l, err, len); return s; }
	out->text_status = l->result_text(out->result, &out->text, &out->language);
	if (out->text_status != 0 && out->text_status != 7) lw_detail(l, err, len);
	return 0;
}

static void lw_result_free(lw_lib *l, void *r) { if (r) l->result_free(r); }

static int lw_stream_reset(lw_lib *l, char *err, size_t len) {
	int s = l->stream_reset(l->session);
	if (s) lw_detail(l, err, len);
	return s;
}

static void lw_free_all(lw_lib *l) {
	if (l->session && l->session_free) l->session_free(l->session);
	if (l->model && l->model_free) l->model_free(l->model);
	if (l->registry && l->registry_free) l->registry_free(l->registry);
	l->session = l->model = l->registry = NULL;
	lw_close_handle(l);
	free(l);
}

static const char **lw_strv(int n) { return (const char **)calloc(n > 0 ? n : 1, sizeof(char *)); }
static void lw_strv_set(const char **v, int i, const char *s) { v[i] = s; }
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// NativeAvailable reports whether this build can load audio.cpp.
const NativeAvailable = true

const errLen = 1024

type cLib struct {
	l     *C.lw_lib
	bound bool
}

func errBuf() (*C.char, func() string, func()) {
	buf := (*C.char)(C.calloc(errLen, 1))
	return buf, func() string { return C.GoString(buf) }, func() { C.free(unsafe.Pointer(buf)) }
}

// OpenLibrary loads audio.cpp from path. dllDirs are added to the Windows
// DLL search path for its dependencies (ignored elsewhere; use rpath or
// LD_LIBRARY_PATH).
func OpenLibrary(path string, dllDirs []string) (CLib, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	dirs := C.lw_strv(C.int(len(dllDirs)))
	defer C.free(unsafe.Pointer(dirs))
	for i, d := range dllDirs {
		cd := C.CString(d)
		defer C.free(unsafe.Pointer(cd))
		C.lw_strv_set(dirs, C.int(i), cd)
	}
	buf, msg, free := errBuf()
	defer free()
	l := C.lw_open(cpath, dirs, C.int(len(dllDirs)), buf, errLen)
	if l == nil {
		return nil, fmt.Errorf("r2t2: load %s: %s", path, msg())
	}
	return &cLib{l: l}, nil
}

func (c *cLib) ABIVersion() uint32 { return uint32(C.lw_abi(c.l)) }

func statusErr(call string, status C.int, detail string) error {
	if status == -1 {
		return errors.New(detail)
	}
	return &StatusError{Call: call, Status: int(status), Detail: detail}
}

func (c *cLib) Open(cfg SessionConfig) error {
	buf, msg, free := errBuf()
	defer free()
	if !c.bound {
		if C.lw_bind(c.l, buf, errLen) != 0 {
			return fmt.Errorf("r2t2: %s", msg())
		}
		c.bound = true
	}
	if s := C.lw_registry_create(c.l, buf, errLen); s != 0 {
		return statusErr("registry_create", s, msg())
	}
	cpath := C.CString(cfg.ModelPath)
	defer C.free(unsafe.Pointer(cpath))
	cfam := C.CString(ModelFamily)
	defer C.free(unsafe.Pointer(cfam))
	if s := C.lw_model_load(c.l, cpath, cfam, buf, errLen); s != 0 {
		return statusErr("model_load", s, msg())
	}
	opts := C.lw_options_create(c.l)
	if opts == nil {
		return errors.New("Cannot allocate audio.cpp options")
	}
	defer C.lw_options_free(c.l, opts)
	for _, kv := range cfg.Options {
		k, v := C.CString(kv[0]), C.CString(kv[1])
		s := C.lw_options_set(c.l, opts, k, v, buf, errLen)
		C.free(unsafe.Pointer(k))
		C.free(unsafe.Pointer(v))
		if s != 0 {
			return statusErr("options_set", s, msg())
		}
	}
	cb := C.CString(cfg.Backend)
	defer C.free(unsafe.Pointer(cb))
	if s := C.lw_session_create(c.l, cb, C.int(cfg.Threads), opts, buf, errLen); s != 0 {
		return statusErr("session_create", s, msg())
	}
	return nil
}

func (c *cLib) StreamStart(context, language string) error {
	buf, msg, free := errBuf()
	defer free()
	cctx := C.CString(context)
	defer C.free(unsafe.Pointer(cctx))
	var clang *C.char
	if language != "" {
		clang = C.CString(language)
		defer C.free(unsafe.Pointer(clang))
	}
	var which C.int
	s := C.lw_stream_start(c.l, cctx, clang, &which, buf, errLen)
	if s == 0 {
		return nil
	}
	call := "request_set_text"
	if which == 2 {
		call = "stream_start"
	}
	return statusErr(call, s, msg())
}

func (c *cLib) StreamPush(pcm []float32, offset int64) (PushEvent, error) {
	buf, msg, free := errBuf()
	defer free()
	var ptr *C.float
	if len(pcm) > 0 {
		ptr = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	var out C.lw_push_out
	if s := C.lw_stream_push(c.l, ptr, C.size_t(len(pcm)), C.int64_t(offset), &out, buf, errLen); s != 0 {
		return PushEvent{}, statusErr("stream_push", s, msg())
	}
	defer C.lw_event_free(c.l, out.event)
	ev := PushEvent{HasEvent: out.has_event != 0}
	if !ev.HasEvent {
		return ev, nil
	}
	ev.TextStatus = int(out.text_status)
	switch ev.TextStatus {
	case StatusOK:
		ev.Delta, ev.Language = C.GoString(out.text), C.GoString(out.language)
	case StatusNotAvailable:
	default:
		ev.Detail = msg()
		return ev, nil
	}
	ev.PreviewStatus = int(out.preview_status)
	if ev.PreviewStatus == StatusOK {
		ev.Preview = C.GoString(out.preview)
	}
	return ev, nil
}

func (c *cLib) StreamFinish() (FinishResult, error) {
	buf, msg, free := errBuf()
	defer free()
	var out C.lw_finish_out
	if s := C.lw_stream_finish(c.l, &out, buf, errLen); s != 0 {
		C.lw_result_free(c.l, out.result)
		return FinishResult{}, statusErr("stream_finish", s, msg())
	}
	defer C.lw_result_free(c.l, out.result)
	res := FinishResult{TextStatus: int(out.text_status)}
	switch res.TextStatus {
	case StatusOK:
		res.Text, res.Language = C.GoString(out.text), C.GoString(out.language)
	case StatusNotAvailable:
	default:
		res.Detail = msg()
	}
	return res, nil
}

func (c *cLib) StreamReset() error {
	buf, msg, free := errBuf()
	defer free()
	if s := C.lw_stream_reset(c.l, buf, errLen); s != 0 {
		return statusErr("stream_reset", s, msg())
	}
	return nil
}

func (c *cLib) Close() {
	if c.l != nil {
		C.lw_free_all(c.l)
		c.l = nil
	}
}
