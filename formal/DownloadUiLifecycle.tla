------------------------ MODULE DownloadUiLifecycle ------------------------
EXTENDS Naturals
CONSTANTS IgnoreCancellation, IgnoreDisposal
Ids == 1..2
VARIABLE s
Init == s = [mounted |-> TRUE, generation |-> 0, changes |-> 0, allowed |-> TRUE,
  queued |-> FALSE, timerGeneration |-> 0, listenerEpoch |-> 1, replays |-> 0,
  starts |-> 0, startedWithoutPermission |-> FALSE, publications |-> 0,
  staleCompletions |-> 0,
  retiredEventApplied |-> FALSE,
  request |-> [i \in Ids |-> [phase |-> "New", generation |-> 0,
    staleAtFinish |-> FALSE, before |-> 0, after |-> 0]]]
Schedule ==
  /\ s.mounted /\ s.allowed /\ ~s.queued /\ s.starts < 2
  /\ s' = [s EXCEPT !.queued = TRUE, !.timerGeneration = s.generation]
Fire(i) ==
  /\ s.queued /\ s.request[i].phase = "New" /\ s.starts < 2
  /\ LET accept == s.mounted /\ (IgnoreCancellation \/
         (s.allowed /\ s.timerGeneration = s.generation))
     IN s' = [s EXCEPT !.queued = FALSE,
       !.starts = IF accept THEN @ + 1 ELSE @,
       !.request[i] = IF accept THEN [phase |-> "Pending", generation |-> s.generation,
         staleAtFinish |-> FALSE, before |-> 0, after |-> 0] ELSE @,
       !.startedWithoutPermission = @ \/ (accept /\ (~s.allowed \/ s.timerGeneration # s.generation))]
Current(i) == s.mounted /\ (IgnoreCancellation \/ s.request[i].generation = s.generation)
Finish(i, ok) ==
  /\ s.request[i].phase = "Pending" /\ ok \in BOOLEAN
  /\ LET retry == Current(i) /\ ~ok /\ s.starts < 2
     IN s' = [s EXCEPT !.request[i].phase = "Done",
       !.request[i].staleAtFinish = ~s.mounted \/ s.request[i].generation # s.generation,
       !.request[i].before = s.publications,
       !.request[i].after = s.publications + IF Current(i) THEN 1 ELSE 0,
       !.publications = s.publications + IF Current(i) THEN 1 ELSE 0,
       !.staleCompletions = @ + IF ~s.mounted \/ s.request[i].generation # s.generation THEN 1 ELSE 0,
       !.queued = IF retry THEN TRUE ELSE @,
       !.timerGeneration = IF retry THEN s.generation ELSE @]
Cancel ==
  /\ s.mounted /\ s.changes < 2
  /\ s' = [s EXCEPT !.allowed = FALSE, !.generation = @ + 1, !.changes = @ + 1,
    !.queued = IF IgnoreCancellation THEN @ ELSE FALSE]
Retry ==
  /\ s.mounted /\ s.changes < 2
  /\ s' = [s EXCEPT !.allowed = TRUE, !.generation = @ + 1, !.changes = @ + 1,
    !.queued = FALSE]
Unmount ==
  /\ s.mounted /\ s.replays < 2
  /\ s' = [s EXCEPT !.mounted = FALSE, !.generation = @ + 1, !.queued = FALSE]
Replay ==
  /\ ~s.mounted /\ s.replays < 1
  /\ s' = [s EXCEPT !.mounted = TRUE, !.listenerEpoch = @ + 1, !.replays = @ + 1]
Event(epoch) ==
  /\ epoch \in 1..s.listenerEpoch
  /\ LET accept == s.mounted /\ (IgnoreDisposal \/ epoch = s.listenerEpoch)
     IN s' = [s EXCEPT !.retiredEventApplied = @ \/ (accept /\ epoch # s.listenerEpoch)]
Next == Schedule \/ Cancel \/ Retry \/ Unmount \/ Replay
  \/ (\E epoch \in 1..2: Event(epoch))
  \/ (\E i \in Ids: Fire(i) \/ (\E ok \in BOOLEAN: Finish(i, ok)))
Spec == Init /\ [][Next]_s /\ \A i \in Ids: WF_s(\E ok \in BOOLEAN: Finish(i, ok))
NoRetryAfterCancellation == s.allowed \/ ~s.queued
AutoStartsHavePermission == ~s.startedWithoutPermission
OldRequestCannotPublish == \A i \in Ids:
  s.request[i].phase = "Done" /\ s.request[i].staleAtFinish => s.request[i].before = s.request[i].after
DisposedListenerCannotAct == ~s.retiredEventApplied
NoTimerAfterUnmount == s.mounted \/ ~s.queued
RequestsSettle == \A i \in Ids: s.request[i].phase = "Pending" ~> s.request[i].phase = "Done"
=============================================================================
