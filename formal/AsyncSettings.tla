---------------------------- MODULE AsyncSettings ----------------------------
EXTENDS Naturals
Contexts == {"A", "B"}
VARIABLE s
Init == s = [context |-> "A", auth |-> TRUE, mounted |-> TRUE,
  generation |-> 0, switches |-> 0, fetches |-> 0, queued |-> FALSE,
  request |-> [i \in 1..2 |-> [phase |-> "New", context |-> "A", generation |-> 0]],
  dataContext |-> "None", loading |-> FALSE, error |-> FALSE,
  acceptedAfterUnmount |-> FALSE]
Schedule ==
  /\ s.mounted /\ s.auth /\ s.fetches < 2 /\ ~s.queued
  /\ s' = [s EXCEPT !.queued = TRUE]
BeginFetch(i) ==
  /\ s.mounted /\ s.auth /\ s.queued /\ s.request[i].phase = "New"
  /\ s.fetches < 2
  /\ s' = [s EXCEPT !.generation = @ + 1, !.fetches = @ + 1,
    !.queued = FALSE, !.loading = TRUE,
    !.request[i] = [phase |-> "Pending", context |-> s.context, generation |-> s.generation + 1]]
ChangeContext(c) ==
  /\ s.mounted /\ c \in Contexts /\ c # s.context /\ s.switches = 0
  /\ s' = [s EXCEPT !.context = c, !.switches = 1,
    !.generation = @ + 1, !.queued = FALSE, !.dataContext = "None",
    !.error = FALSE]
LoseAuth ==
  /\ s.auth /\ s.mounted
  /\ s' = [s EXCEPT !.auth = FALSE, !.generation = @ + 1,
    !.queued = FALSE, !.dataContext = "None", !.loading = FALSE, !.error = FALSE]
Unmount ==
  /\ s.mounted
  /\ s' = [s EXCEPT !.mounted = FALSE, !.generation = @ + 1, !.queued = FALSE]
Current(i) == s.mounted /\ s.auth /\ s.request[i].generation = s.generation
  /\ s.request[i].context = s.context
Finish(i, success) ==
  /\ s.request[i].phase = "Pending" /\ success \in BOOLEAN
  /\ s' = [s EXCEPT !.request[i].phase = "Done",
    !.dataContext = IF Current(i) /\ success THEN s.request[i].context ELSE s.dataContext,
    !.error = IF Current(i) THEN ~success ELSE s.error,
    !.loading = IF Current(i) THEN FALSE ELSE s.loading,
    !.acceptedAfterUnmount = s.acceptedAfterUnmount \/ (~s.mounted /\ Current(i))]
Next == Schedule \/ LoseAuth \/ Unmount
  \/ \E c \in Contexts: ChangeContext(c)
  \/ \E i \in 1..2: BeginFetch(i) \/ \E ok \in BOOLEAN: Finish(i, ok)
Spec == Init /\ [][Next]_s /\ \A i \in 1..2: WF_s(\E ok \in BOOLEAN: Finish(i, ok))
NoCrossProviderData == s.dataContext = "None" \/ s.dataContext = s.context
NoModelsWithoutAuth == s.auth \/ s.dataContext = "None"
NoQueuedWorkAfterUnmount == s.mounted \/ ~s.queued
NoPostUnmountPublication == ~s.acceptedAfterUnmount
RequestsSettle == \A i \in 1..2:
  (s.request[i].phase = "Pending") ~> (s.request[i].phase = "Done")
=============================================================================
