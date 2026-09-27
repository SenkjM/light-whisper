# Core state models

These bounded TLA+ models check the app's asynchronous ownership and persistence
rules. Each model maps to a specific implementation boundary:

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
    -metadir "/tmp/light-whisper-tlc-$model" \
    -config "formal/$model.cfg" "formal/$model.tla"
done
```

The configurations use two sessions, requests, updates, or downloads and up to
two engine configuration changes. This exercises old/new interleavings; it is
not an unbounded proof. The models abstract away audio samples, ASR and LLM
outputs, operating-system scheduling, actual disk and network failures, UI
rendering, and progress guarantees. TLC checks the reachable states of these
models, but does not prove that Rust and TypeScript conform to them.
