------------------------- MODULE MicrophoneMonitor -------------------------
EXTENDS Naturals
CONSTANT Legacy
VARIABLE m
Init == m = [epoch |-> 0, recording |-> FALSE, slot |-> 0,
  request |-> [i \in 1..2 |-> [phase |-> "New", epoch |-> 0]],
  badPublication |-> FALSE]
Begin(i) ==
  /\ m.request[i].phase = "New" /\ ~m.recording /\ m.epoch < 4
  /\ m' = [m EXCEPT !.epoch = @ + 1, !.slot = 0,
    !.request[i] = [phase |-> "Starting", epoch |-> m.epoch + 1]]
Stop ==
  /\ m.epoch < 4
  /\ m' = [m EXCEPT !.epoch = @ + 1, !.slot = 0]
Recording(active) ==
  /\ active \in BOOLEAN /\ active # m.recording
  /\ m' = [m EXCEPT !.recording = active]
Finish(i, ok) ==
  /\ m.request[i].phase = "Starting" /\ ok \in BOOLEAN
  /\ LET valid == m.request[i].epoch = m.epoch /\ ~m.recording
         publish == ok /\ (Legacy \/ valid)
     IN m' = [m EXCEPT !.request[i].phase = "Done",
       !.slot = IF publish THEN i ELSE @,
       !.badPublication = m.badPublication \/ (publish /\ ~valid)]
Next == Stop \/ (\E active \in BOOLEAN: Recording(active))
  \/ (\E i \in 1..2: Begin(i) \/ (\E ok \in BOOLEAN: Finish(i, ok)))
Spec == Init /\ [][Next]_m
  /\ (\A i \in 1..2: WF_m(\E ok \in BOOLEAN: Finish(i, ok)))
OnlyCurrentIdleStartPublishes == ~m.badPublication
RequestsSettle == \A i \in 1..2:
  m.request[i].phase = "Starting" ~> m.request[i].phase = "Done"
=============================================================================
