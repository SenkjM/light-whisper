------------------------- MODULE RecordingLifecycle -------------------------
EXTENDS Naturals

CONSTANT MaxSessions
ASSUME MaxSessions \in Nat \ {0}

Event(s, r, p) == [sid |-> s, revision |-> r, phase |-> p]
Empty == Event(0, 0, "Idle")
Phases == {"Idle", "Starting", "Recording", "Processing", "Outcome"}

VARIABLES current, slot, snapshot, revision, starting, processing,
          cancelled, pending, delivered, ui
vars == <<current, slot, snapshot, revision, starting, processing,
          cancelled, pending, delivered, ui>>

Init ==
  /\ current = 0
  /\ slot = "None"
  /\ snapshot = Empty
  /\ revision = 0
  /\ starting = {}
  /\ processing = {}
  /\ cancelled = {}
  /\ pending = {}
  /\ delivered = {}
  /\ ui = Empty

\* start_recording_inner reserves the slot and a revision under the recording lock.
Start ==
  /\ current < MaxSessions
  /\ slot = "None"
  /\ LET s == current + 1
         e == Event(s, revision + 1, "Starting")
     IN /\ current' = s
        /\ slot' = "Starting"
        /\ snapshot' = e
        /\ revision' = revision + 1
        /\ starting' = starting \cup {s}
        /\ pending' = pending \cup {e}
  /\ UNCHANGED <<processing, cancelled, delivered, ui>>

\* The capture task can finish after cancellation or after a newer start.
CaptureReady(s) ==
  /\ s \in starting
  /\ starting' = starting \ {s}
  /\ IF s = current /\ slot = "Starting" /\ s \notin cancelled
        THEN LET e == Event(s, revision + 1, "Recording")
             IN /\ slot' = "Recording"
                /\ snapshot' = e
                /\ revision' = revision + 1
                /\ pending' = pending \cup {e}
        ELSE UNCHANGED <<slot, snapshot, revision, pending>>
  /\ UNCHANGED <<current, processing, cancelled, delivered, ui>>

CaptureFailed(s) ==
  /\ s \in starting
  /\ starting' = starting \ {s}
  /\ IF s = current /\ slot = "Starting" /\ s \notin cancelled
        THEN LET e == Event(s, revision + 1, "Outcome")
             IN /\ slot' = "None"
                /\ snapshot' = e
                /\ revision' = revision + 1
                /\ pending' = pending \cup {e}
        ELSE UNCHANGED <<slot, snapshot, revision, pending>>
  /\ UNCHANGED <<current, processing, cancelled, delivered, ui>>

\* stop_recording_inner takes the Starting slot and sets its stop flag.
CancelStarting ==
  /\ slot = "Starting"
  /\ LET e == Event(current, revision + 1, "Idle")
     IN /\ slot' = "None"
        /\ snapshot' = e
        /\ revision' = revision + 1
        /\ pending' = pending \cup {e}
  /\ cancelled' = cancelled \cup {current}
  /\ UNCHANGED <<current, starting, processing, delivered, ui>>

StopRecording ==
  /\ slot = "Recording"
  /\ LET e == Event(current, revision + 1, "Processing")
     IN /\ slot' = "None"
        /\ snapshot' = e
        /\ revision' = revision + 1
        /\ pending' = pending \cup {e}
  /\ processing' = processing \cup {current}
  /\ UNCHANGED <<current, starting, cancelled, delivered, ui>>

\* finalize_recording may complete after another session starts. Its result
\* still exists, but transition_snapshot_if_current rejects its old state.
Finish(s, result) ==
  /\ s \in processing
  /\ result \in {"Idle", "Outcome"}
  /\ processing' = processing \ {s}
  /\ IF s = current /\ slot = "None" /\ snapshot.phase = "Processing"
        THEN LET e == Event(s, revision + 1, result)
             IN /\ snapshot' = IF result = "Idle" THEN Empty ELSE e
                /\ revision' = revision + 1
                /\ pending' = pending \cup {e}
        ELSE UNCHANGED <<snapshot, revision, pending>>
  /\ UNCHANGED <<current, slot, starting, cancelled, delivered, ui>>

\* Tauri delivery can be arbitrarily delayed and reordered. The frontend
\* accepts only a newer (sessionId, revision) pair.
Deliver(e) ==
  /\ e \in pending
  /\ pending' = pending \ {e}
  /\ delivered' = delivered \cup {e}
  /\ ui' = IF e.sid > ui.sid \/ (e.sid = ui.sid /\ e.revision > ui.revision)
            THEN e ELSE ui
  /\ UNCHANGED <<current, slot, snapshot, revision, starting,
                processing, cancelled>>

Next ==
  \/ Start
  \/ \E s \in 1..MaxSessions: CaptureReady(s) \/ CaptureFailed(s)
  \/ CancelStarting
  \/ StopRecording
  \/ \E s \in 1..MaxSessions:
       \E result \in {"Idle", "Outcome"}: Finish(s, result)
  \/ \E e \in pending: Deliver(e)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ current \in 0..MaxSessions
  /\ slot \in {"None", "Starting", "Recording"}
  /\ snapshot \in [sid : 0..MaxSessions, revision : Nat, phase : Phases]
  /\ ui \in [sid : 0..MaxSessions, revision : Nat, phase : Phases]
  /\ starting \subseteq 1..MaxSessions
  /\ processing \subseteq 1..MaxSessions
  /\ cancelled \subseteq 1..MaxSessions
  /\ pending \subseteq [sid : 1..MaxSessions, revision : Nat, phase : Phases]
  /\ delivered \subseteq [sid : 1..MaxSessions, revision : Nat, phase : Phases]

SnapshotBelongsToCurrent == snapshot = Empty \/ snapshot.sid = current
SlotMatchesSnapshot ==
  /\ (slot = "Starting" => snapshot.phase = "Starting")
  /\ (slot = "Recording" => snapshot.phase = "Recording")
CancelledNeverActive == slot = "Recording" => current \notin cancelled
UiIsLatestDelivered ==
  /\ (ui = Empty \/ ui \in delivered)
  /\ \A e \in delivered:
       e.sid < ui.sid \/ (e.sid = ui.sid /\ e.revision <= ui.revision)
=============================================================================
