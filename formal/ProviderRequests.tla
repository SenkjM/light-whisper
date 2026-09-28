-------------------------- MODULE ProviderRequests --------------------------
EXTENDS Naturals
CONSTANT Legacy
Providers == {"A", "B"}
VARIABLE p
Init == p = [active |-> "A", cachedKey |-> "A", phase |-> "New",
  captured |-> "None", key |-> "None", endpoint |-> "None", wrongRecipient |-> FALSE]
Switch ==
  /\ p.active = "A"
  /\ p' = [p EXCEPT !.active = "B"]
SyncCache == p' = [p EXCEPT !.cachedKey = p.active]
Begin ==
  /\ p.phase = "New"
  /\ p' = [p EXCEPT !.phase = "Auth", !.captured = p.active,
    !.key = IF Legacy THEN p.cachedKey ELSE p.active]
FinishAuth ==
  /\ p.phase = "Auth"
  /\ LET recipient == IF Legacy THEN p.active ELSE p.captured
     IN p' = [p EXCEPT !.phase = "Sent", !.endpoint = recipient,
       !.wrongRecipient = p.key # recipient]
FailAuth ==
  /\ p.phase = "Auth"
  /\ p' = [p EXCEPT !.phase = "Failed"]
Next == Switch \/ SyncCache \/ Begin \/ FinishAuth \/ FailAuth
Spec == Init /\ [][Next]_p /\ WF_p(FinishAuth \/ FailAuth)
CredentialOnlyGoesToItsProvider == ~p.wrongRecipient
RequestUsesCapturedEndpoint == p.phase # "Sent" \/ p.endpoint = p.captured
RequestsSettle == p.phase = "Auth" ~> p.phase \in {"Sent", "Failed"}
=============================================================================
