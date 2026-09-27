--------------------------- MODULE EngineDownload ---------------------------
EXTENDS Naturals

CONSTANT MaxDownloads, MaxConfigChanges
ASSUME MaxDownloads \in Nat \ {0}
ASSUME MaxConfigChanges \in Nat

Task(id, epoch) == [id |-> id, epoch |-> epoch]

VARIABLES nextId, slot, running, exited, cancelRequested, configEpoch
vars == <<nextId, slot, running, exited, cancelRequested, configEpoch>>

Init ==
  /\ nextId = 0
  /\ slot = 0
  /\ running = {}
  /\ exited = {}
  /\ cancelRequested = {}
  /\ configEpoch = 0

\* run_download registers its slot under funasr_lifecycle_op before it uses
\* the selected engine and model directory.
Begin ==
  /\ slot = 0
  /\ nextId < MaxDownloads
  /\ nextId' = nextId + 1
  /\ slot' = nextId + 1
  /\ running' = running \cup {Task(nextId + 1, configEpoch)}
  /\ UNCHANGED <<exited, cancelRequested, configEpoch>>

\* cancel_model_download sends a signal but deliberately keeps the slot until
\* the child has stopped and run_download has cleaned up.
Cancel ==
  /\ slot # 0
  /\ slot \notin cancelRequested
  /\ cancelRequested' = cancelRequested \cup {slot}
  /\ UNCHANGED <<nextId, slot, running, exited, configEpoch>>

Exit(id) ==
  /\ \E task \in running: task.id = id
  /\ running' = {task \in running: task.id # id}
  /\ exited' = exited \cup {id}
  /\ UNCHANGED <<nextId, slot, cancelRequested, configEpoch>>

\* The ID check in clear_download_task prevents an old cleanup from clearing
\* a replacement task. Repeated old cleanup attempts are harmless.
Cleanup(id) ==
  /\ id \in exited
  /\ slot' = IF slot = id THEN 0 ELSE slot
  /\ UNCHANGED <<nextId, running, exited, cancelRequested, configEpoch>>

ChangeConfig ==
  /\ slot = 0
  /\ configEpoch < MaxConfigChanges
  /\ configEpoch' = configEpoch + 1
  /\ UNCHANGED <<nextId, slot, running, exited, cancelRequested>>

Next ==
  \/ Begin
  \/ Cancel
  \/ \E id \in 1..MaxDownloads: Exit(id) \/ Cleanup(id)
  \/ ChangeConfig

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ nextId \in 0..MaxDownloads
  /\ slot \in 0..MaxDownloads
  /\ running \subseteq [id : 1..MaxDownloads, epoch : 0..MaxConfigChanges]
  /\ exited \subseteq 1..MaxDownloads
  /\ cancelRequested \subseteq 1..MaxDownloads
  /\ configEpoch \in 0..MaxConfigChanges

RunningOwnsSlot ==
  \A task \in running: task.id = slot

RunningUsesCurrentConfig ==
  \A task \in running: task.epoch = configEpoch
=============================================================================
