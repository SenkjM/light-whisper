# Coverage audit

This is an audit of the TLA+ checks against the 120 Tauri commands registered in
`src-tauri/src/lib.rs`. An action being reachable in TLC says only that its
abstract transition can occur. It does not show that every command, branch, or
Rust/TypeScript implementation path is represented.

| Product area | Current formal coverage | Missing behavior |
| --- | --- | --- |
| Recording and output | Start/cancel/stop, ASR result/failure, four modes, Jev skip or LLM path, display, paste and clipboard restoration. `RecordingLifecycle` checks two sessions. | Microphone device selection and level monitor, native/CPAL fallback, audio samples, streaming ASR hypotheses, hotkey press/release and toggle-mode transitions. |
| Engine setup | Local download/cancel, cloud readiness and one engine switch; `EngineDownload` checks task/config ownership. | Four concrete engine implementations, model-directory changes, endpoint/model selection, restart/start failures, online key-slot changes. |
| Settings and personalization | One abstract settings version and debounced-save race in `ProfilePersistence`. | Profile import/export, hotword add/remove, correction learning/review, per-app rules, provider/model discovery and the independent setting combinations. |
| Authentication | A Boolean API-key availability flag. | OpenAI Codex and Grok Build browser/device-code login, refresh, logout, keyring/disk persistence and their concurrent interleavings. |
| Assistant and selection | Assistant result/conversation, one enabled web-search path, selection versions/actions, target-window and source-text checks. `TaskOwnership` checks cancellation slots. | Web-search Off/Auto and explicit no-search routing, search-source validation, assistant retry, selection overlay/window lifecycle and selection screenshot context. |
| History and subtitles | Consent, one record, query/export/delete, audio reprocess lease, one subtitle session and stale/final event handling. | Record contents, retention cleanup, multiple records, subtitle window show/hide generations and detailed stable/tentative text behavior. |
| Updates and UI | Check/update-page state. | URL validation, network updater behavior, UI layout, settings-page state, theme and accessibility. |

The existing nine `AppWorkflow` configurations cover *selected* combinations of
these areas. They do not enumerate the cross product of all settings, engines,
accounts, windows and requests. All 57 modeled actions are reachable, but this
is not full feature coverage or an implementation refinement proof. There are
no liveness properties, so the model also does not establish that operations
eventually finish. Treat the passing result as evidence for the listed safety
properties under the stated bounds, not as certification of the entire app.

During this audit, removing the late-interim guard from `InterimSubtitle` still
passed the old `SubtitleFinalStaysFinal` invariant. The invariant now records
that a final subtitle was seen and requires it to remain final in that session;
the same mutation produces a TLC counterexample. The production subtitle guard
and its regression test remain in place.
