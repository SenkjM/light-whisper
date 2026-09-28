------------------------- MODULE RequestProtocols -------------------------
EXTENDS Naturals
CONSTANT LegacyIDAccept
VARIABLE p
Init == p = [request |-> 0, pending |-> FALSE, publishedRequest |-> 0,
  staleAccepted |-> FALSE, session |-> 0, active |-> FALSE,
  samples |-> 0, text |-> 0, closed |-> TRUE, badStreamAccepted |-> FALSE]
Send ==
  /\ ~p.pending /\ p.request < 2
  /\ p' = [p EXCEPT !.request = @ + 1, !.pending = TRUE]
AbortOrTimeout ==
  /\ p.pending
  /\ p' = [p EXCEPT !.pending = FALSE]
Reply(id) ==
  /\ p.pending /\ id \in {0, 1, 2}
  /\ LET accept == id = p.request \/ (id = 0 /\ LegacyIDAccept)
     IN p' = [p EXCEPT !.pending = IF accept THEN FALSE ELSE @,
       !.publishedRequest = IF accept THEN id ELSE @,
       !.staleAccepted = p.staleAccepted \/ (accept /\ id # p.request)]
StartStream ==
  /\ p.session < 2
  /\ p' = [p EXCEPT !.session = @ + 1, !.active = TRUE,
    !.samples = 0, !.text = 0, !.closed = FALSE]
FeedStream(session, offset, validPcm, extendsText) ==
  /\ session \in 0..2 /\ offset \in 0..2
  /\ validPcm \in BOOLEAN /\ extendsText \in BOOLEAN
  /\ p.active /\ ~p.closed /\ p.samples < 2
  /\ IF session = p.session /\ offset = p.samples /\ validPcm THEN
       IF extendsText THEN
         p' = [p EXCEPT !.samples = @ + 1, !.text = @ + 1]
       ELSE p' = [p EXCEPT !.active = FALSE, !.closed = TRUE]
     ELSE UNCHANGED p
ValidateAck(identity, count, finality, prefix, tentative) ==
  /\ ~p.closed
  /\ identity \in BOOLEAN /\ count \in BOOLEAN /\ finality \in BOOLEAN
  /\ prefix \in BOOLEAN /\ tentative \in BOOLEAN
  /\ LET valid == identity /\ count /\ finality /\ prefix /\ ~tentative
     IN p' = [p EXCEPT !.closed = ~valid, !.active = IF valid THEN @ ELSE FALSE,
       !.badStreamAccepted = p.badStreamAccepted \/
         (valid /\ (~identity \/ ~count \/ ~finality \/ ~prefix \/ tentative))]
FinishOrCancel(session) ==
  /\ p.active /\ session \in 0..2
  /\ IF session = p.session THEN
       p' = [p EXCEPT !.active = FALSE, !.closed = TRUE]
     ELSE UNCHANGED p
Next == Send \/ AbortOrTimeout \/ StartStream
  \/ (\E id \in 0..2: Reply(id) \/ FinishOrCancel(id))
  \/ (\E session \in 0..2, offset \in 0..2, pcm \in BOOLEAN, prefix \in BOOLEAN:
      FeedStream(session, offset, pcm, prefix))
  \/ (\E identity, count, finality, prefix, tentative \in BOOLEAN:
      ValidateAck(identity, count, finality, prefix, tentative))
Spec == Init /\ [][Next]_p /\ WF_p(AbortOrTimeout)
NoReplyWithoutIdentity == ~p.staleAccepted
NoInvalidStreamPublication == ~p.badStreamAccepted
ClosedStreamCannotRemainActive == p.closed => ~p.active
SamplesStayBounded == p.samples <= 2
RequestsSettle == p.pending ~> ~p.pending
=============================================================================
