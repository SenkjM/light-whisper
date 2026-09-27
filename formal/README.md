# App state models

`AppWorkflow.tla` is one bounded state machine for the app's user-visible
workflow. Its `app` record connects settings and credentials, engine readiness,
recording, ASR, Jev/LLM routing, context, results, optional storage, input,
selection, assistant, history, subtitles, and updates. The nine configurations
explore different feature combinations while checking the same `TypeOK` and
`Safety` invariants:

| Workloads | Behaviors exercised together |
| --- | --- |
| `Mixed` | Local dictation, settings, download, selection, foreground changes, history/audio, screen context, and clipboard paste. |
| `Speech`, `CloudSpeech` | All four recording modes; local/cloud readiness; Jev skip and LLM success/failure; assistant conversation; consent and output paths. |
| `EngineSpeech` | Download and engine-switch requests interleaved with recording; busy requests are rejected without changing the engine configuration. |
| `Selection` | Two selection versions, three actions, cancellation, result acceptance, replace/copy/search, and target-window changes. |
| `AssistantWeb` | Assistant recording with screen context and enabled web search, including source and failure outcomes. |
| `Subtitles` | Recording, interim/final subtitle delivery, and ignored stale or late interim events. |
| `History` | An existing record, query/export/delete, audio reprocessing, and an audio lease surviving source deletion. |
| `Management` | Settings saves, local model download/cancel, engine switch, cloud credential, and update check/open. |

The integrated transitions correspond to these implementation paths:

| State path | Main implementation |
| --- | --- |
| Settings, credentials, engine and download | `src-tauri/src/services/profile_service.rs`, `src-tauri/src/commands/profile.rs`, `src-tauri/src/commands/funasr.rs`, `src-tauri/src/services/download_service.rs` |
| Recording, ASR, Jev, AI processing, screen context and input | `src-tauri/src/commands/audio.rs`, `src-tauri/src/services/audio_service/finalize.rs`, `src-tauri/src/services/assistant_service.rs`, `src-tauri/src/commands/clipboard.rs` |
| Subtitle events and assistant conversation | `src/pages/SubtitleOverlay.tsx`, `src-tauri/src/commands/assistant.rs`, `src/hooks/useRecording.ts` |
| Selection actions | `src-tauri/src/commands/selection.rs`, `src-tauri/src/services/selection_service.rs`, `src/pages/SelectionOverlay.tsx` |
| History and audio lease | `src-tauri/src/commands/history.rs`, `src-tauri/src/services/history_service.rs` |
| Updates | `src-tauri/src/commands/updater.rs` |

The model checks consent for history, audio, screenshots and assistant search;
screen/selection target identity; no assistant auto-paste; conditional clipboard
restoration; download/configuration pinning; engine-switch rejection; audio-file
retention under a reprocessing lease; and settings version ordering. TLC's action
coverage is checked across all nine workloads so a disconnected action cannot
silently pass every invariant.

The smaller models explore specific interleavings more deeply:

| Model | App code | Safety properties |
| --- | --- | --- |
| `RecordingLifecycle` | `commands/audio.rs`, `state/app_state.rs`, `services/audio_service/finalize.rs`, `hooks/useRecording.ts` | A cancelled start cannot become active; stale sessions cannot replace the current snapshot; delivered state events cannot move the UI backward. |
| `TaskOwnership` | `commands/selection.rs`, `commands/assistant.rs` | The newest installed request owns its cancellation slot; an old completion cannot clear it. |
| `ProfilePersistence` | `services/profile_service.rs`, `state/app_state.rs` | A queued save contains the newest profile snapshot and generation; after timers settle, disk reflects the latest update. |
| `EngineDownload` | `services/download_service.rs`, `commands/funasr.rs` | A running download retains its slot and engine/model-directory configuration until it exits. |

`TaskOwnershipLegacy.cfg` reproduces an old interleaving: request 1 reserves a
generation, request 2 reserves and installs a newer generation, then request 1
installs and cancels request 2. `ProfilePersistenceLegacy.cfg` reproduces two
updates whose generation reservations and pending-save publications cross, so
an older snapshot replaces the newest pending save. These two configurations
are expected to fail and are not CI gates. The ordinary `.cfg` files model the
fixed operations and must pass.

Run with Java 11+ and the official [TLA+ tools release](https://github.com/tlaplus/tlaplus/releases/tag/v1.8.0):

```sh
curl -fsSL https://github.com/tlaplus/tlaplus/releases/download/v1.8.0/tla2tools.jar -o /tmp/tla2tools.jar
echo 'ab4694601923fd5ac06452abbf847c366a5054a3d739552085edd6ed986c29ec  /tmp/tla2tools.jar' | sha256sum --check
for model in RecordingLifecycle TaskOwnership ProfilePersistence EngineDownload; do
  java -XX:+UseParallelGC -jar /tmp/tla2tools.jar \
    -noGenerateSpecTE -metadir "/tmp/light-whisper-tlc-$model" \
    -config "formal/$model.cfg" "formal/$model.tla"
done
for workload in Mixed Speech CloudSpeech Selection Management EngineSpeech AssistantWeb Subtitles History; do
  config="formal/AppWorkflow${workload}.cfg"
  if [ "$workload" = Mixed ]; then config=formal/AppWorkflow.cfg; fi
  java -XX:+UseParallelGC -jar /tmp/tla2tools.jar \
    -noGenerateSpecTE -coverage 1 \
    -metadir "/tmp/light-whisper-app-$workload" \
    -config "$config" formal/AppWorkflow.tla \
    > "/tmp/light-whisper-app-$workload.log"
done
python scripts/check_tla_action_coverage.py /tmp/light-whisper-app-*.log
```

The component configurations use two sessions, requests, updates, or downloads
and up to two engine configuration changes. `AppWorkflow` uses one recording,
up to two selection versions, one settings change, and one engine switch per
workload. These are finite-state checks, not an unbounded proof. Recording entry
abstracts hotkeys and buttons; configuration versioning abstracts hotwords,
correction and app-specific rules; credential availability abstracts API keys
and account login. Audio samples, ASR/LLM output quality, provider protocols,
network and disk failures, UI layout, and progress are outside the state model.
TLC verifies the specified transitions; Rust/TypeScript conformance is supported
by the mapped code paths and tests, not proved by TLC alone.
