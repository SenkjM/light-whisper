-------------------- MODULE EventSubscriptionLifecycle --------------------
EXTENDS Naturals, FiniteSets
CONSTANT IgnoreDisposal
Ids == 1..2
VARIABLE s
Init == s = [mounted |-> TRUE, epoch |-> 1, delivered |-> {}, effects |-> {},
  retiredEffects |-> {},
  subscription |-> [i \in Ids |-> [phase |-> "New", disposed |-> FALSE, unlistens |-> 0]]]
Begin ==
  /\ s.mounted /\ s.subscription[s.epoch].phase = "New"
  /\ s' = [s EXCEPT !.subscription[s.epoch].phase = "Pending"]
Register(i) ==
  /\ s.subscription[i].phase = "Pending"
  /\ s' = [s EXCEPT
    !.subscription[i].phase = IF s.subscription[i].disposed THEN "Cleaned" ELSE "Registered",
    !.subscription[i].unlistens = IF s.subscription[i].disposed THEN @ + 1 ELSE @]
RejectRegistration(i) ==
  /\ s.subscription[i].phase = "Pending"
  /\ s' = [s EXCEPT !.subscription[i].phase = "Failed"]
Unmount ==
  /\ s.mounted
  /\ s' = [s EXCEPT !.mounted = FALSE, !.subscription[s.epoch].disposed = TRUE,
    !.subscription[s.epoch].phase = IF @ = "Registered" THEN "Cleaned" ELSE @,
    !.subscription[s.epoch].unlistens = IF s.subscription[s.epoch].phase = "Registered" THEN @ + 1 ELSE @]
Replay ==
  /\ ~s.mounted /\ s.epoch = 1
  /\ s' = [s EXCEPT !.mounted = TRUE, !.epoch = 2]
Receive(i) ==
  /\ s.subscription[i].phase \in {"Pending", "Registered", "Cleaned"} /\ i \notin s.delivered
  /\ LET accept == IgnoreDisposal \/ ~s.subscription[i].disposed
     IN s' = [s EXCEPT !.delivered = @ \cup {i},
       !.effects = IF accept THEN @ \cup {i} ELSE @,
       !.retiredEffects = IF accept /\ s.subscription[i].disposed THEN @ \cup {i} ELSE @]
Next == Begin \/ Unmount \/ Replay \/ (\E i \in Ids: Register(i) \/ RejectRegistration(i) \/ Receive(i))
Spec == Init /\ [][Next]_s /\ \A i \in Ids: WF_s(Register(i) \/ RejectRegistration(i))
NoDisposedEventEffects == s.retiredEffects = {}
CleanupAtMostOnce == \A i \in Ids: s.subscription[i].unlistens <= 1
RegisteredOwnerIsLive == \A i \in Ids:
  s.subscription[i].phase = "Registered" => ~s.subscription[i].disposed
LateRegistrationsAreCleaned == \A i \in Ids:
  (s.subscription[i].disposed /\ s.subscription[i].phase = "Pending") ~>
    s.subscription[i].phase \in {"Cleaned", "Failed"}
=============================================================================
