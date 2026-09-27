# Recording lifecycle model

`RecordingLifecycle.tla` is a bounded TLA+ model of the recording lifecycle and
the `recording-state` event fence. It follows these implementation boundaries:

| Model action | App implementation |
| --- | --- |
| `Start`, `CaptureReady`, `CaptureFailed` | `src-tauri/src/commands/audio.rs` (`start_recording_inner`) |
| `CancelStarting`, `StopRecording` | `src-tauri/src/commands/audio.rs` (`stop_recording_inner`) |
| `Finish` | `src-tauri/src/services/audio_service/finalize.rs` and `RecordingState::transition_snapshot_if_current` |
| `Deliver` | `src/hooks/useRecording.ts` (`recording-state` listener) |

The model allows capture to finish after cancellation, finalization to finish
after a later session starts, and events to arrive in any order. TLC checks that
the snapshot belongs to the current session, a cancelled start cannot become
active, the slot agrees with its snapshot, and the UI keeps the greatest
delivered `(sessionId, revision)` pair.

Run with Java 11+ and the official [TLA+ tools release](https://github.com/tlaplus/tlaplus/releases/tag/v1.8.0):

```sh
curl -fsSL https://github.com/tlaplus/tlaplus/releases/download/v1.8.0/tla2tools.jar -o tla2tools.jar
echo 'ab4694601923fd5ac06452abbf847c366a5054a3d739552085edde6ed986c29ec  tla2tools.jar' | sha256sum --check
java -XX:+UseParallelGC -jar tla2tools.jar -config formal/RecordingLifecycle.cfg formal/RecordingLifecycle.tla
```

The checked configuration has two sessions, enough to exercise old/new session
races. The model abstracts away audio samples, transcription, window rendering,
hotkey gating, and runtime scheduling. TLC exhaustively checks the model's
reachable states; it does not prove the Rust and TypeScript implementations
conform to the model or establish liveness.
