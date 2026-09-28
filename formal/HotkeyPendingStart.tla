------------------------- MODULE HotkeyPendingStart -------------------------
EXTENDS Naturals
CONSTANT Legacy
VARIABLE h
Init == h = [generation |-> 0, active |-> FALSE, session |-> 0,
  requests |-> [i \in 1..2 |-> [phase |-> "New", generation |-> 0]],
  badBind |-> FALSE, badFailure |-> FALSE]
Begin(i) ==
  /\ h.requests[i].phase = "New" /\ h.generation < 4
  /\ h' = [h EXCEPT !.generation = @ + 1, !.active = TRUE, !.session = 0,
    !.requests[i] = [phase |-> "Queued", generation |-> h.generation + 1]]
Release ==
  /\ h.active
  /\ h' = [h EXCEPT !.active = FALSE, !.session = 0]
Bind(i) ==
  /\ h.requests[i].phase = "Queued"
  /\ LET current == h.active /\ h.requests[i].generation = h.generation /\ h.session = 0
         accepted == Legacy \/ current
     IN h' = [h EXCEPT !.requests[i].phase = "Done",
       !.session = IF accepted THEN i ELSE @,
       !.badBind = h.badBind \/ (accepted /\ ~current)]
Fail(i) ==
  /\ h.requests[i].phase = "Queued"
  /\ LET current == h.active /\ h.requests[i].generation = h.generation
         cleared == Legacy \/ current
     IN h' = [h EXCEPT !.requests[i].phase = "Done",
       !.active = IF cleared THEN FALSE ELSE @,
       !.session = IF cleared THEN 0 ELSE @,
       !.badFailure = h.badFailure \/ (cleared /\ h.active /\ ~current)]
Next == Release \/ (\E i \in 1..2: Begin(i) \/ Bind(i) \/ Fail(i))
Spec == Init /\ [][Next]_h /\ (\A i \in 1..2: WF_h(Bind(i) \/ Fail(i)))
ReleasedOrSupersededStartCannotBind == ~h.badBind
OldFailureCannotClearNewIntent == ~h.badFailure
RequestsSettle == \A i \in 1..2: h.requests[i].phase = "Queued" ~> h.requests[i].phase = "Done"
=============================================================================
