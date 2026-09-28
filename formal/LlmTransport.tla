--------------------------- MODULE LlmTransport ---------------------------
EXTENDS Naturals
VARIABLE r
Init == r = [phase |-> "Ready", overloadRetries |-> 0, strategy |-> 0,
  strippedTokens |-> FALSE, streaming |-> FALSE, content |-> 0,
  total |-> 0, idle |-> 0, published |-> FALSE, emptySuccess |-> FALSE,
  heartbeatResetsProgress |-> FALSE]
Dispatch ==
  /\ r.phase = "Ready"
  /\ r' = [r EXCEPT !.phase = "Awaiting"]
Response(kind) ==
  /\ r.phase = "Awaiting"
  /\ kind \in {"Success", "Overload429", "TokenLimit", "Reasoning", "Fatal"}
  /\ CASE kind = "Success" -> r' = [r EXCEPT !.phase = "Stream", !.streaming = TRUE]
    [] kind = "Overload429" /\ r.overloadRetries < 2 ->
        r' = [r EXCEPT !.phase = "Ready", !.overloadRetries = @ + 1]
    [] kind = "TokenLimit" /\ ~r.strippedTokens ->
        r' = [r EXCEPT !.phase = "Ready", !.strippedTokens = TRUE]
    [] kind = "Reasoning" /\ r.strategy < 3 ->
        r' = [r EXCEPT !.phase = "Ready", !.strategy = @ + 1, !.strippedTokens = FALSE]
    [] OTHER -> r' = [r EXCEPT !.phase = "Error"]
Token ==
  /\ r.phase = "Stream" /\ r.content < 2 /\ r.total < 4 /\ r.idle < 2
  /\ r' = [r EXCEPT !.content = @ + 1, !.idle = 0]
Heartbeat ==
  /\ r.phase = "Stream"
  /\ LET nextIdle == r.idle
     IN r' = [r EXCEPT !.idle = nextIdle,
       !.heartbeatResetsProgress = r.heartbeatResetsProgress \/ (r.idle # nextIdle)]
Tick ==
  /\ r.phase = "Stream" /\ r.total < 4
  /\ r' = [r EXCEPT !.total = @ + 1, !.idle = @ + 1,
    !.phase = IF r.total + 1 >= 4 \/ r.idle + 1 >= 2 THEN "Error" ELSE @]
EndStream(ok) ==
  /\ r.phase = "Stream" /\ ok \in BOOLEAN
  /\ LET success == ok /\ r.content > 0
     IN r' = [r EXCEPT !.phase = IF success THEN "Done" ELSE "Error",
       !.published = success, !.emptySuccess = r.emptySuccess \/ (success /\ r.content = 0)]
Cancel ==
  /\ r.phase \in {"Ready", "Awaiting", "Stream"}
  /\ r' = [r EXCEPT !.phase = "Cancelled"]
Next == Dispatch \/ Token \/ Heartbeat \/ Tick \/ Cancel
  \/ (\E kind \in {"Success", "Overload429", "TokenLimit", "Reasoning", "Fatal"}: Response(kind))
  \/ (\E ok \in BOOLEAN: EndStream(ok))
Spec == Init /\ [][Next]_r /\ WF_r(Tick) /\ WF_r(Dispatch)
  /\ WF_r(\E kind \in {"Success", "Overload429", "TokenLimit", "Reasoning", "Fatal"}: Response(kind))
NoEmptySuccess == ~r.emptySuccess
FiniteCapabilityProbing == r.overloadRetries <= 2 /\ r.strategy <= 3
HeartbeatCannotExtendBudget == ~r.heartbeatResetsProgress
PublishedOnlyAfterSuccess == r.published => r.phase = "Done" /\ r.content > 0
RequestsSettle == r.phase \in {"Ready", "Awaiting", "Stream"} ~> r.phase \in {"Done", "Error", "Cancelled"}
=============================================================================
