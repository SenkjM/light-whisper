---------------------------- MODULE OAuthLifecycle ----------------------------
EXTENDS Naturals, FiniteSets, TLC
CONSTANT Legacy
Requests == 1..3
VARIABLE s
Init == s = [epoch |-> 0, memory |-> 1, disk |-> 1, sessionEpoch |-> 0,
  logoutEpoch |-> 0, latestLogin |-> 0, pendingLogin |-> FALSE,
  refreshOwner |-> 0, request |-> [i \in Requests |->
    [kind |-> "None", epoch |-> 0, phase |-> "New"]]]

BeginLogin(i, kind) ==
  /\ s.request[i].phase = "New" /\ s.epoch < 4
  /\ kind \in {"Browser", "Device"}
  /\ s' = [s EXCEPT !.epoch = @ + 1, !.pendingLogin = TRUE,
    !.request[i] = [kind |-> kind, epoch |-> s.epoch + 1, phase |-> "Network"]]

BeginRefresh(i) ==
  /\ s.request[i].phase = "New" /\ s.memory # 0
  /\ ~s.pendingLogin /\ s.refreshOwner = 0
  /\ s' = [s EXCEPT !.refreshOwner = i,
    !.request[i] = [kind |-> "Refresh", epoch |-> s.epoch, phase |-> "Network"]]

Logout ==
  /\ s.epoch < 4
  /\ s' = [s EXCEPT !.epoch = @ + 1, !.logoutEpoch = s.epoch + 1,
    !.memory = 0, !.disk = 0, !.pendingLogin = FALSE]

Commit(i) ==
  /\ s.request[i].phase = "Network" /\ s.epoch < 4
  /\ Legacy \/ s.request[i].epoch = s.epoch
  /\ s' = [s EXCEPT !.memory = i + 1, !.disk = i + 1,
    !.sessionEpoch = s.request[i].epoch,
    !.latestLogin = IF s.request[i].kind # "Refresh"
      THEN IF s.request[i].epoch > s.latestLogin THEN s.request[i].epoch
           ELSE s.latestLogin ELSE s.latestLogin,
    !.epoch = @ + 1, !.request[i].phase = "Done",
    !.pendingLogin = FALSE,
    !.refreshOwner = IF s.refreshOwner = i THEN 0 ELSE s.refreshOwner]

Discard(i) ==
  /\ s.request[i].phase = "Network"
  /\ s.request[i].epoch # s.epoch
  /\ s' = [s EXCEPT !.request[i].phase = "Discarded",
    !.refreshOwner = IF s.refreshOwner = i THEN 0 ELSE s.refreshOwner]

Fail(i, fatal) ==
  /\ s.request[i].phase = "Network" /\ fatal \in BOOLEAN
  /\ s' = [s EXCEPT !.request[i].phase = "Failed",
    !.epoch = IF fatal /\ s.request[i].kind = "Refresh"
                 /\ s.request[i].epoch = s.epoch THEN @ + 1 ELSE @,
    !.logoutEpoch = IF fatal /\ s.request[i].kind = "Refresh"
                 /\ s.request[i].epoch = s.epoch THEN s.epoch + 1 ELSE @,
    !.pendingLogin = IF s.request[i].epoch = s.epoch
      THEN FALSE ELSE s.pendingLogin,
    !.memory = IF fatal /\ s.request[i].kind = "Refresh"
                 /\ (Legacy \/ s.request[i].epoch = s.epoch) THEN 0 ELSE s.memory,
    !.disk = IF fatal /\ s.request[i].kind = "Refresh"
                 /\ (Legacy \/ s.request[i].epoch = s.epoch) THEN 0 ELSE s.disk,
    !.refreshOwner = IF s.refreshOwner = i THEN 0 ELSE s.refreshOwner]

Resolve(i) == Commit(i) \/ Discard(i) \/ \E fatal \in BOOLEAN: Fail(i, fatal)
Next == Logout \/ \E i \in Requests:
  (\E kind \in {"Browser", "Device"}: BeginLogin(i, kind))
  \/ BeginRefresh(i) \/ Resolve(i)
Spec == Init /\ [][Next]_s /\ \A i \in Requests: WF_s(Resolve(i))
TypeOK ==
  /\ s.epoch \in 0..5 /\ s.memory \in 0..4 /\ s.disk \in 0..4
  /\ s.refreshOwner \in 0..3
  /\ \A i \in Requests: s.request[i].phase \in
       {"New", "Network", "Done", "Discarded", "Failed"}
LogoutWins == s.memory = 0 \/ s.sessionEpoch >= s.logoutEpoch
NewLoginWins == s.memory = 0 \/ s.sessionEpoch >= s.latestLogin
NewLoginSurvivesStaleFailure == s.latestLogin <= s.logoutEpoch \/ s.memory # 0
MemoryMatchesDisk == s.memory = s.disk
RequestsSettle == \A i \in Requests:
  (s.request[i].phase = "Network") ~> (s.request[i].phase # "Network")
=============================================================================
