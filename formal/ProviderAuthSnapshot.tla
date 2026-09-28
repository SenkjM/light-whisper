------------------------ MODULE ProviderAuthSnapshot ------------------------
EXTENDS Naturals
CONSTANTS LegacyMode, LegacyAccount
VARIABLE s
Init == \E mode \in {"OAuth", "ApiKey", "Default"}:
  s = [mode |-> mode, session |-> 0, phase |-> "New", capturedMode |-> "None",
    usedMode |-> "None", keySession |-> 2, catalogSession |-> 2, wrongPair |-> FALSE]
EditMode == s.mode # "ApiKey" /\ s' = [s EXCEPT !.mode = "ApiKey"]
ReplaceSession == s.session = 0 /\ s' = [s EXCEPT !.session = 1]
Logout == s.session # 2 /\ s' = [s EXCEPT !.session = 2]
Begin == s.phase = "New" /\ s' = [s EXCEPT !.phase = "Auth", !.capturedMode = s.mode]
Resolve ==
  /\ s.phase = "Auth"
  /\ LET mode == IF LegacyMode THEN s.mode ELSE s.capturedMode
     IN s' = [s EXCEPT
       !.phase = IF mode = "OAuth" /\ s.session = 2 THEN "Failed" ELSE "Resolved",
       !.usedMode = mode,
       !.keySession = IF mode = "ApiKey" THEN 2 ELSE s.session,
       !.catalogSession = IF mode = "ApiKey" THEN 2 ELSE s.session]
Fail == s.phase = "Auth" /\ s' = [s EXCEPT !.phase = "Failed"]
Send ==
  /\ s.phase = "Resolved"
  /\ LET catalog == IF LegacyAccount /\ s.keySession # 2 THEN s.session ELSE s.catalogSession
     IN s' = [s EXCEPT !.phase = "Sent", !.catalogSession = catalog,
       !.wrongPair = catalog # s.keySession]
Next == EditMode \/ ReplaceSession \/ Logout \/ Begin \/ Resolve \/ Fail \/ Send
Spec == Init /\ [][Next]_s /\ WF_s(Resolve \/ Fail) /\ WF_s(Send)
CapturedAuthModeIsUsed == s.phase \notin {"Resolved", "Sent"} \/ s.usedMode = s.capturedMode
CatalogAndKeyShareSession == ~s.wrongPair
RequestsSettle == s.phase \in {"Auth", "Resolved"} ~> s.phase \in {"Sent", "Failed"}
=============================================================================
