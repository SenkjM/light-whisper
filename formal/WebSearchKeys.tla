--------------------------- MODULE WebSearchKeys ---------------------------
EXTENDS Naturals
CONSTANT Legacy
Ids == 1..2
VARIABLE s
Init == s = [disk |-> 1, cache |-> 1, owner |-> 0, stalePublished |-> FALSE,
  op |-> [i \in Ids |-> [phase |-> "New", kind |-> "Save", value |-> 0, read |-> 0]]]
Begin(i, kind, value) ==
  /\ s.op[i].phase = "New" /\ (Legacy \/ s.owner = 0)
  /\ kind \in {"Save", "Load"} /\ value \in 0..2
  /\ s' = [s EXCEPT !.owner = i,
    !.op[i] = [phase |-> "IO", kind |-> kind, value |-> value, read |-> 0]]
IO(i, ok) ==
  /\ s.op[i].phase = "IO" /\ ok \in BOOLEAN
  /\ LET value == IF s.op[i].kind = "Save" THEN s.op[i].value ELSE s.disk
     IN s' = [s EXCEPT !.op[i].phase = IF ok THEN "Publish" ELSE "Done",
       !.op[i].read = value,
       !.disk = IF ok /\ s.op[i].kind = "Save" THEN value ELSE @,
       !.owner = IF ~ok /\ @ = i THEN 0 ELSE @]
Publish(i) ==
  /\ s.op[i].phase = "Publish"
  /\ s' = [s EXCEPT !.cache = s.op[i].read, !.op[i].phase = "Done",
    !.owner = IF @ = i THEN 0 ELSE @,
    !.stalePublished = @ \/ (s.op[i].kind = "Load" /\ s.op[i].read # s.disk)]
Next == \E i \in Ids:
  (\E kind \in {"Save", "Load"}, value \in 0..2: Begin(i, kind, value))
  \/ (\E ok \in BOOLEAN: IO(i, ok)) \/ Publish(i)
Spec == Init /\ [][Next]_s /\ \A i \in Ids: WF_s((\E ok \in BOOLEAN: IO(i, ok)) \/ Publish(i))
QuiescentKeysAgree == (\A i \in Ids: s.op[i].phase \in {"New", "Done"}) => s.disk = s.cache
NoStaleReadPublication == ~s.stalePublished
PendingOperationOwnsGate == \A i \in Ids: s.op[i].phase \in {"IO", "Publish"} => s.owner = i
OperationsSettle == \A i \in Ids: s.op[i].phase \in {"IO", "Publish"} ~> s.op[i].phase = "Done"
=============================================================================
