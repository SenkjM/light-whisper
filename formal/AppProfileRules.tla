--------------------------- MODULE AppProfileRules ---------------------------
EXTENDS Naturals
VARIABLE a
Init == \E matches \in [1..2 -> BOOLEAN], values \in [1..2 -> 0..2], baseline \in 0..2:
  a = [matches |-> matches, values |-> values, baseline |-> baseline,
    selected |-> 0, result |-> 0, resolved |-> FALSE,
    capturedWindow |-> "A", foreground |-> "A"]
First == IF a.matches[1] THEN 1 ELSE IF a.matches[2] THEN 2 ELSE 0
Resolve ==
  /\ ~a.resolved
  /\ a' = [a EXCEPT !.selected = First, !.resolved = TRUE,
    !.result = IF First = 0 \/ a.values[First] = 0 THEN a.baseline ELSE a.values[First]]
MoveForeground ==
  /\ a.foreground = "A"
  /\ a' = [a EXCEPT !.foreground = "B"]
Next == Resolve \/ MoveForeground
Spec == Init /\ [][Next]_a /\ WF_a(Resolve)
FirstMatchingRuleWins == ~a.resolved \/ a.selected = First
InheritUsesBaseline == ~a.resolved \/
  ((a.selected = 0 \/ a.values[a.selected] = 0) => a.result = a.baseline)
ExplicitRuleWins == ~a.resolved \/ a.selected = 0 \/ a.values[a.selected] = 0
  \/ a.result = a.values[a.selected]
CapturedContextIsStable == a.capturedWindow = "A"
EventuallyResolves == ~a.resolved ~> a.resolved
=============================================================================
