----------------------- MODULE HotkeyRegistrationEpoch -----------------------
EXTENDS Naturals, FiniteSets
CONSTANT ReuseModeGate, ReuseNativeId
VARIABLE r

Init ==
  /\ r = [epoch |-> 1, gate |-> 1, nativeId |-> 1,
           backend |-> "LowLevelHook", changes |-> {}, queue |-> {},
           captured |-> {}, delivered |-> {}, badDispatch |-> FALSE,
           badNative |-> FALSE]
  \/ r = [epoch |-> 1, gate |-> 1, nativeId |-> 1,
           backend |-> "RegisterHotKey", changes |-> {}, queue |-> {},
           captured |-> {}, delivered |-> {}, badDispatch |-> FALSE,
           badNative |-> FALSE]

Queue(i, transport) ==
  /\ i \in 1..2 /\ i \notin r.captured
  /\ transport \in {"Dispatch", "Native"}
  /\ transport = "Native" => r.backend = "RegisterHotKey"
  /\ r' = [r EXCEPT !.captured = @ \cup {i},
       !.queue = @ \cup {[token |-> i, transport |-> transport,
                          origin |-> r.epoch, gate |-> r.gate,
                          nativeId |-> r.nativeId]}]

\* The configuration lock makes replacement and mode publication one abstract
\* transition relative to dispatch. A same-backend mode change also gets a new
\* gate. Native IDs are never reused, including after backend-thread restart.
Change(reason) ==
  /\ reason \in {"Mode", "Shortcut"} /\ reason \notin r.changes
  /\ reason = "Mode" => r.backend = "LowLevelHook"
  /\ r.epoch < 3
  /\ LET next == r.epoch + 1
     IN r' = [r EXCEPT !.epoch = next, !.changes = @ \cup {reason},
          !.gate = IF reason = "Mode" /\ ReuseModeGate THEN @ ELSE next,
          !.nativeId = IF ReuseNativeId THEN @ ELSE next]

Deliver(i) ==
  /\ i \in 1..2
  /\ \E e \in r.queue:
       /\ e.token = i
       /\ LET gate == IF e.transport = "Dispatch" THEN e.gate
                      ELSE IF e.nativeId = r.nativeId THEN r.gate ELSE 0
              stale == gate = r.gate /\ e.origin # r.epoch
          IN r' = [r EXCEPT !.queue = @ \ {e}, !.delivered = @ \cup {i},
               !.badDispatch = @ \/ (stale /\ e.transport = "Dispatch"),
               !.badNative = @ \/ (stale /\ e.transport = "Native")]

Next == (\E i \in 1..2: \E t \in {"Dispatch", "Native"}: Queue(i, t))
        \/ (\E reason \in {"Mode", "Shortcut"}: Change(reason))
        \/ (\E i \in 1..2: Deliver(i))
Spec == Init /\ [][Next]_r /\ (\A i \in 1..2: WF_r(Deliver(i)))
TypeOK ==
  /\ r.epoch \in 1..3 /\ r.gate \in 1..3 /\ r.nativeId \in 1..3
  /\ r.backend \in {"LowLevelHook", "RegisterHotKey"}
  /\ r.changes \subseteq {"Mode", "Shortcut"}
  /\ r.captured \subseteq 1..2 /\ r.delivered \subseteq r.captured
  /\ r.queue \subseteq [token: 1..2, transport: {"Dispatch", "Native"},
                          origin: 1..3, gate: 1..3, nativeId: 1..3]
  /\ r.badDispatch \in BOOLEAN /\ r.badNative \in BOOLEAN
NoStaleDispatchPress == ~r.badDispatch
NoStaleNativePress == ~r.badNative
QueuedEventsSettle == \A i \in 1..2: i \in r.captured ~> i \in r.delivered
=============================================================================
