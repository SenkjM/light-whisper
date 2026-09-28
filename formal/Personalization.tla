---------------------------- MODULE Personalization ----------------------------
EXTENDS Naturals, FiniteSets
Keys == {"a", "b"}
VARIABLE s
Init == s = [words |-> {}, blocked |-> {},
  corrections |-> [k \in Keys |-> "None"], protected |-> {},
  review |-> "Idle", reviewSnapshot |-> {}, version |-> 0,
  exportedVersion |-> 0, imported |-> FALSE,
  exported |-> [words |-> {}, blocked |-> {}, corrections |-> [k \in Keys |-> "None"],
    version |-> 0]]
AddWord(k) ==
  /\ k \in Keys /\ s.version < 3
  /\ s' = [s EXCEPT !.words = @ \cup {k}, !.blocked = @ \ {k}, !.version = @ + 1]
RemoveWord(k) ==
  /\ k \in Keys /\ s.version < 3
  /\ s' = [s EXCEPT !.words = @ \ {k}, !.blocked = @ \cup {k}, !.version = @ + 1]
LearnWord(k) ==
  /\ k \in Keys /\ k \notin s.blocked /\ s.version < 3
  /\ s' = [s EXCEPT !.words = @ \cup {k}, !.version = @ + 1]
LearnCorrection(k, source) ==
  /\ k \in Keys /\ source \in {"User", "AI"} /\ s.version < 3
  /\ s' = [s EXCEPT !.corrections[k] =
      IF source = "User" \/ s.corrections[k] = "User" THEN "User" ELSE "AI",
    !.protected = IF source = "User" THEN @ \cup {k} ELSE @,
    !.version = @ + 1]
RemoveCorrection(k) ==
  /\ k \in Keys /\ s.version < 3
  /\ s' = [s EXCEPT !.corrections[k] = "None", !.protected = @ \ {k}, !.version = @ + 1]
BeginReview ==
  /\ s.review = "Idle"
  /\ s' = [s EXCEPT !.review = "Pending",
    !.reviewSnapshot = {k \in Keys: s.corrections[k] = "AI"}]
FinishReview(invalid) ==
  /\ s.review = "Pending" /\ invalid \subseteq s.reviewSnapshot
  /\ s' = [s EXCEPT !.review = "Done",
    !.corrections = [k \in Keys |->
      IF k \in invalid /\ s.corrections[k] # "User" THEN "None"
      ELSE s.corrections[k]]]
ImportProfile(words, blocked, corrections) ==
  /\ ~s.imported /\ s.version < 3
  /\ words \subseteq Keys /\ blocked \subseteq Keys
  /\ corrections \in [Keys -> {"None", "User", "AI"}]
  /\ s' = [s EXCEPT !.words = words \ blocked, !.blocked = blocked,
    !.corrections = corrections, !.protected = {k \in Keys: corrections[k] = "User"},
    !.imported = TRUE, !.version = @ + 1]
ExportProfile ==
  /\ s.exportedVersion # s.version
  /\ s' = [s EXCEPT !.exportedVersion = s.version,
    !.exported = [words |-> s.words, blocked |-> s.blocked,
      corrections |-> s.corrections, version |-> s.version]]
Next ==
  \/ \E k \in Keys: AddWord(k) \/ RemoveWord(k) \/ LearnWord(k) \/ RemoveCorrection(k)
  \/ \E k \in Keys, source \in {"User", "AI"}: LearnCorrection(k, source)
  \/ BeginReview \/ \E invalid \in SUBSET Keys: FinishReview(invalid)
  \/ \E words, blocked \in SUBSET Keys, corrections \in [Keys -> {"None", "User", "AI"}]:
       ImportProfile(words, blocked, corrections)
  \/ ExportProfile
Spec == Init /\ [][Next]_s /\ WF_s(\E invalid \in SUBSET Keys: FinishReview(invalid))
TypeOK == s.words \subseteq Keys /\ s.blocked \subseteq Keys
  /\ s.corrections \in [Keys -> {"None", "User", "AI"}] /\ s.version \in 0..3
BlockedWordsCannotBePromoted == s.words \cap s.blocked = {}
UserRulesSurviveAutomaticChanges == \A k \in s.protected: s.corrections[k] = "User"
ExportContainsNoCredential ==
  DOMAIN s.exported = {"words", "blocked", "corrections", "version"}
ExportIsSnapshot == s.exportedVersion <= s.version
ReviewSettles == (s.review = "Pending") ~> (s.review = "Done")
=============================================================================
