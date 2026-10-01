------------------------- MODULE GpuIdleLifecycle -------------------------
EXTENDS Naturals
CONSTANTS Timeout, StartupEnabled
ASSUME Timeout > 0 /\ StartupEnabled \in BOOLEAN
VARIABLE s
Init == s = [enabled |-> StartupEnabled, loaded |-> TRUE, ready |-> TRUE,
  busy |-> FALSE, stream |-> FALSE, age |-> 0, window |-> StartupEnabled,
  unsafeUnload |-> FALSE]
Enable ==
  /\ ~s.enabled
  /\ s' = [s EXCEPT !.enabled = TRUE, !.age = 0, !.window = TRUE]
Disable ==
  /\ s.enabled
  /\ s' = [s EXCEPT !.enabled = FALSE, !.age = 0, !.window = FALSE]
BeginCommand ==
  /\ ~s.busy /\ ~s.stream
  /\ s' = [s EXCEPT !.busy = TRUE, !.loaded = TRUE]
StartStream ==
  /\ ~s.busy /\ ~s.stream
  /\ s' = [s EXCEPT !.busy = TRUE, !.stream = TRUE, !.loaded = TRUE]
FinishCommand ==
  /\ s.busy
  /\ s' = [s EXCEPT !.busy = FALSE, !.age = 0]
FinishStream ==
  /\ s.stream /\ ~s.busy
  /\ s' = [s EXCEPT !.stream = FALSE, !.age = 0]
Tick ==
  /\ s.enabled /\ s.loaded /\ s.window /\ ~s.busy /\ ~s.stream /\ s.age < Timeout
  /\ s' = [s EXCEPT !.age = @ + 1]
CanUnload == s.enabled /\ s.loaded /\ ~s.busy /\ ~s.stream /\ s.age = Timeout
Unload ==
  /\ CanUnload
  /\ s' = [s EXCEPT !.loaded = FALSE,
    !.unsafeUnload = s.busy \/ s.stream \/ ~s.enabled]
Next == Enable \/ Disable \/ BeginCommand \/ StartStream \/ FinishCommand
  \/ FinishStream \/ Tick \/ Unload
Spec == Init /\ [][Next]_s /\ WF_s(Tick) /\ WF_s(Unload) /\ WF_s(FinishCommand)
TypeOK == s.age \in 0..Timeout /\ s.enabled \in BOOLEAN /\ s.loaded \in BOOLEAN
  /\ s.ready \in BOOLEAN /\ s.busy \in BOOLEAN /\ s.stream \in BOOLEAN
EnabledHasIdleWindow == ~s.enabled \/ s.window
ActiveRuntimeLoaded == (~s.busy /\ ~s.stream) \/ s.loaded
SafeUnload == ~s.unsafeUnload
ReadySurvivesIdle == s.ready
IdleEventuallyUnloads ==
  (s.enabled /\ s.loaded /\ ~s.busy /\ ~s.stream)
    ~> (~s.loaded \/ s.busy \/ s.stream \/ ~s.enabled)
CommandsSettle == s.busy ~> ~s.busy
=============================================================================
