-------------------------- MODULE WindowLifecycle --------------------------
EXTENDS Naturals
VARIABLE w
Init == w = [session |-> 0, generation |-> 0, recording |-> FALSE,
  exists |-> FALSE, visible |-> FALSE, interactive |-> FALSE,
  show |-> [i \in 1..2 |-> [phase |-> "New", generation |-> 0]],
  hide |-> [i \in 1..2 |-> [phase |-> "New", generation |-> 0]],
  staleShow |-> FALSE, staleHide |-> FALSE, mainVisible |-> TRUE,
  exiting |-> FALSE, monitor |-> FALSE, child |-> FALSE]
BeginSession ==
  /\ ~w.exiting /\ ~w.recording /\ w.session < 2 /\ w.generation < 4
  /\ w' = [w EXCEPT !.session = @ + 1, !.generation = @ + 1,
      !.recording = TRUE, !.interactive = FALSE, !.show[w.session + 1] = [phase |-> "Pending", generation |-> w.generation + 1]]
FinishSession ==
  /\ w.recording
  /\ w' = [w EXCEPT !.recording = FALSE, !.hide[w.session] = [phase |-> "Pending", generation |-> w.generation]]
Show(i, ok) ==
  /\ w.show[i].phase = "Pending" /\ ok \in BOOLEAN
  /\ LET current == ~w.exiting /\ w.recording /\ i = w.session /\ w.show[i].generation = w.generation
     IN w' = [w EXCEPT !.show[i].phase = "Done",
       !.exists = IF current /\ ok THEN TRUE ELSE @,
       !.visible = IF current /\ ok THEN TRUE ELSE @,
       !.staleShow = w.staleShow \/ (current /\ ok /\
         (w.exiting \/ ~w.recording \/ i # w.session \/ w.show[i].generation # w.generation))]
Hide(i, ok) ==
  /\ w.hide[i].phase = "Pending" /\ ok \in BOOLEAN
  /\ LET current == w.hide[i].generation = w.generation /\ i = w.session /\ ~w.recording
     IN w' = [w EXCEPT !.hide[i].phase = "Done",
       !.visible = IF current /\ ok THEN FALSE ELSE @,
       !.interactive = IF current /\ ok THEN FALSE ELSE @,
       !.staleHide = w.staleHide \/ (current /\ ok /\ (w.recording \/ i # w.session))]
Interact(i, ok) ==
  /\ i = w.session /\ w.hide[i].generation = w.generation /\ ~w.recording /\ ~w.exiting
  /\ w.visible /\ ok \in BOOLEAN
  /\ w' = [w EXCEPT !.interactive = ok]
MainVisibility(visible) ==
  /\ ~w.exiting /\ visible \in BOOLEAN /\ visible # w.mainVisible
  /\ w' = [w EXCEPT !.mainVisible = visible]
StartBackground ==
  /\ ~w.exiting /\ ~w.child /\ ~w.monitor
  /\ w' = [w EXCEPT !.child = TRUE, !.monitor = TRUE]
Exit ==
  /\ ~w.exiting
  /\ w' = [w EXCEPT !.exiting = TRUE, !.recording = FALSE, !.monitor = FALSE,
      !.visible = FALSE, !.interactive = FALSE]
ReapChild ==
  /\ w.exiting /\ w.child
  /\ w' = [w EXCEPT !.child = FALSE]
CreateWindow ==
  /\ ~w.exiting /\ ~w.exists
  /\ w' = [w EXCEPT !.exists = TRUE]
ManualShow(ok) ==
  /\ ~w.exiting /\ w.generation < 4 /\ ok \in BOOLEAN
  /\ w' = [w EXCEPT !.generation = @ + 1,
    !.exists = IF ok THEN TRUE ELSE @, !.visible = IF ok THEN TRUE ELSE @]
ManualHide(ok) ==
  /\ ~w.exiting /\ ok \in BOOLEAN
  /\ w' = [w EXCEPT !.visible = IF ok THEN FALSE ELSE @,
    !.interactive = IF ok THEN FALSE ELSE @]
Next == CreateWindow \/ (\E ok \in BOOLEAN: ManualShow(ok) \/ ManualHide(ok)) \/ BeginSession \/ FinishSession \/ StartBackground \/ Exit \/ ReapChild
  \/ \E i \in 1..2, ok \in BOOLEAN: Show(i, ok) \/ Hide(i, ok) \/ Interact(i, ok)
  \/ \E visible \in BOOLEAN: MainVisibility(visible)
Spec == Init /\ [][Next]_w /\ WF_w(ReapChild)
  /\ (\A i \in 1..2: WF_w(\E ok \in BOOLEAN: Show(i, ok)))
  /\ (\A i \in 1..2: WF_w(\E ok \in BOOLEAN: Hide(i, ok)))
NoStaleShow == ~w.staleShow
NoStaleHide == ~w.staleHide
InteractiveRequiresWindow == w.interactive => (w.exists /\ w.visible /\ ~w.recording)
ExitStopsCapture == w.exiting => ~w.recording /\ ~w.monitor
ChildEventuallyReaped == w.exiting ~> ~w.child
=============================================================================
