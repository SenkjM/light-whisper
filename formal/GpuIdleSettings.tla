--------------------------- MODULE GpuIdleSettings ---------------------------
EXTENDS Naturals
VARIABLE s
Init == s = [readPending |-> TRUE, edited |-> FALSE, saving |-> FALSE,
  display |-> 0, saved |-> 0, pending |-> 0, writes |-> 0]
BeginSave(value) ==
  /\ ~s.saving /\ value \in 0..2 /\ s.writes < 2
  /\ s' = [s EXCEPT !.edited = TRUE, !.saving = TRUE,
    !.pending = value, !.writes = @ + 1]
FinishSave(success) ==
  /\ s.saving /\ success \in BOOLEAN
  /\ s' = [s EXCEPT !.saving = FALSE,
    !.saved = IF success THEN s.pending ELSE @,
    !.display = IF success THEN s.pending ELSE @]
FinishRead ==
  /\ s.readPending
  /\ s' = [s EXCEPT !.readPending = FALSE,
    !.display = IF ~s.edited THEN 0 ELSE @]
Next == FinishRead \/ (\E v \in 0..2: BeginSave(v))
  \/ \E ok \in BOOLEAN: FinishSave(ok)
Spec == Init /\ [][Next]_s /\ WF_s(FinishRead)
  /\ WF_s(\E ok \in BOOLEAN: FinishSave(ok))
DisplayMatchesLastSave == s.display = s.saved
SavesSettle == s.saving ~> ~s.saving
=============================================================================
