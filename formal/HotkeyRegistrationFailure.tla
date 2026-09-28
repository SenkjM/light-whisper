---------------------- MODULE HotkeyRegistrationFailure ----------------------
EXTENDS Naturals
CONSTANT LegacyRollback, IgnoreLifecycleError
VARIABLE h
Init == h = [phase |-> "Stable", current |-> 1, currentNative |-> 1,
             retired |-> {}, native |-> {1}, cleanup |-> {},
             failed |-> FALSE, reported |-> FALSE,
             rollbackOk |-> TRUE, finished |-> FALSE]

Begin ==
  /\ h.phase = "Stable" /\ ~h.finished
  /\ h' = [h EXCEPT !.phase = "Setup", !.retired = {1},
                    !.current = 2, !.currentNative = 2, !.native = {2}]
Ready ==
  /\ h.phase = "Setup"
  /\ h' = [h EXCEPT !.phase = "Stable", !.finished = TRUE]
Fail ==
  /\ h.phase = "Setup"
  /\ h' = [h EXCEPT !.phase = "Rollback", !.failed = TRUE,
                    !.reported = ~IgnoreLifecycleError]

\* Retirement and queueing exact-ID cleanup precede rebuilding old settings.
\* A successful restore receives a new gate/native ID; a failed restore leaves
\* the affected slots empty and reports the original and restoration failures.
\* OS cleanup is deferred. A queued command retains ownership of a retired ID
\* until the backend drains it. Restoration may select the hook fallback or
\* register natively and subsequently fail another lifecycle operation.
Restore(ok, installed) ==
  /\ h.phase = "Rollback" /\ ok \in BOOLEAN /\ installed \in BOOLEAN
  /\ h' = [h EXCEPT !.phase = "Stable", !.finished = TRUE,
       !.rollbackOk = ok,
       !.retired = IF LegacyRollback THEN @ ELSE @ \cup {2},
       !.current = IF LegacyRollback THEN 1 ELSE IF ok THEN 3 ELSE 0,
       !.currentNative = IF LegacyRollback THEN 1
                        ELSE IF ok /\ installed THEN 3 ELSE 0,
       !.native = IF LegacyRollback THEN @
                  ELSE @ \cup (IF installed THEN {3} ELSE {}),
       !.cleanup = IF LegacyRollback THEN @
                   ELSE @ \cup {2} \cup (IF ~ok /\ installed THEN {3} ELSE {})]
DrainCleanup(id) ==
  /\ id \in h.cleanup
  /\ h' = [h EXCEPT !.cleanup = @ \ {id}, !.native = @ \ {id}]
Next == Begin \/ Ready \/ Fail
        \/ (\E ok, installed \in BOOLEAN: Restore(ok, installed))
        \/ (\E id \in 1..3: DrainCleanup(id))
Spec == Init /\ [][Next]_h /\ WF_h(Ready \/ Fail)
        /\ WF_h(\E ok, installed \in BOOLEAN: Restore(ok, installed))
        /\ (\A id \in 1..3: WF_h(DrainCleanup(id)))
TypeOK ==
  /\ h.phase \in {"Stable", "Setup", "Rollback"}
  /\ h.current \in 0..3 /\ h.currentNative \in 0..3
  /\ h.retired \subseteq 1..3 /\ h.cleanup \subseteq h.native
  /\ h.native \subseteq 1..3 /\ h.failed \in BOOLEAN
  /\ h.reported \in BOOLEAN /\ h.rollbackOk \in BOOLEAN
  /\ h.finished \in BOOLEAN
NoRetiredGatePublished == h.phase = "Stable" => h.current \notin h.retired
NoOrphanNativeRegistration ==
  h.phase = "Stable" => h.native \subseteq
    h.cleanup \cup (IF h.currentNative = 0 THEN {} ELSE {h.currentNative})
LifecycleFailureIsReported == h.failed => h.reported
SetupSettles == h.phase = "Setup" ~> h.phase = "Stable"
RetiredNativeIdsDrain == \A id \in 1..3: id \in h.cleanup ~> id \notin h.native
=============================================================================
