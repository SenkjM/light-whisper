---------------------------- MODULE ContextRouting ----------------------------
EXTENDS Naturals
Modes == {"Off", "On", "Auto"}
Choices == {"Yes", "No", "Unknown"}
Override(value, global) == IF value = "Inherit" THEN global
  ELSE IF value = "Enabled" THEN "On" ELSE "Off"
Search(mode, explicit, jev, baseline) ==
  IF mode = "Off" THEN FALSE
  ELSE IF explicit # "Unknown" THEN explicit = "Yes"
  ELSE IF mode = "On" THEN TRUE
  ELSE IF jev # "Unknown" THEN jev = "Yes" ELSE baseline
Screen(mode, explicit, jev) ==
  IF mode = "Off" THEN FALSE
  ELSE IF mode = "On" \/ explicit THEN TRUE
  ELSE jev # "No"
VARIABLE s
Init == \E polish, screen, web \in Modes,
  polishOverride, screenOverride \in {"Inherit", "Enabled", "Disabled"},
  request \in {"Dictation", "Translation", "Edit", "Assistant"},
  screenExplicit, searchBaseline \in BOOLEAN,
  searchExplicit, jevScreen, jevSearch, jevPolish \in Choices:
  s = [polish |-> polish, screen |-> screen, web |-> web,
    polishOverride |-> polishOverride, screenOverride |-> screenOverride, request |-> request, screenExplicit |-> screenExplicit,
    searchExplicit |-> searchExplicit, searchBaseline |-> searchBaseline,
    jevScreen |-> jevScreen, jevSearch |-> jevSearch, jevPolish |-> jevPolish,
    phase |-> "Ready", process |-> FALSE, screenAllowed |-> FALSE,
    searchAllowed |-> FALSE, foreground |-> "A", capturedWindow |-> "None",
    screenUsed |-> FALSE, output |-> "None", audit |-> "Idle"]
Route ==
  /\ s.phase = "Ready"
  /\ s' = [s EXCEPT !.phase = "Routed",
    !.process = s.request # "Dictation"
      \/ Override(s.polishOverride, s.polish) = "On"
      \/ (Override(s.polishOverride, s.polish) = "Auto" /\ s.jevPolish # "No"),
    !.screenAllowed = Screen(Override(s.screenOverride, s.screen), s.screenExplicit, s.jevScreen),
    !.searchAllowed = s.request = "Assistant" /\
      Search(s.web, s.searchExplicit, s.jevSearch, s.searchBaseline)]
MoveForeground ==
  /\ s.foreground = "A"
  /\ s' = [s EXCEPT !.foreground = "B"]
CaptureScreen ==
  /\ s.phase = "Routed" /\ s.screenAllowed /\ s.foreground = "A"
  /\ ~s.screenUsed
  /\ s' = [s EXCEPT !.screenUsed = TRUE, !.capturedWindow = s.foreground]
Complete ==
  /\ s.phase = "Routed"
  /\ s' = [s EXCEPT !.phase = "Done", !.output = IF s.process THEN "Processed" ELSE "Raw"]
StartMeaningAudit ==
  /\ s.phase = "Done" /\ s.output = "Processed" /\ s.audit = "Idle"
  /\ s' = [s EXCEPT !.audit = "Pending"]
FinishMeaningAudit ==
  /\ s.audit = "Pending"
  /\ s' = [s EXCEPT !.audit = "Done"]
Next == Route \/ MoveForeground \/ CaptureScreen \/ Complete
  \/ StartMeaningAudit \/ FinishMeaningAudit
Spec == Init /\ [][Next]_s /\ WF_s(Route) /\ WF_s(Complete) /\ WF_s(FinishMeaningAudit)
ExplicitOperationsExecute == s.phase = "Ready" \/ s.request = "Dictation" \/ s.process
DisabledScreenStaysOff ==
  Override(s.screenOverride, s.screen) # "Off" \/ ~s.screenAllowed
DisabledSearchStaysOff == s.web # "Off" \/ ~s.searchAllowed
ExplicitNoSearchIsHonored == s.searchExplicit # "No" \/ ~s.searchAllowed
UnknownPolishFailsOpen ==
  s.phase = "Ready" \/ Override(s.polishOverride, s.polish) # "Auto"
    \/ s.jevPolish # "Unknown" \/ s.process
UnknownSearchUsesBaseline ==
  s.phase = "Ready" \/ s.web # "Auto" \/ s.searchExplicit # "Unknown"
    \/ s.jevSearch # "Unknown" \/ s.request # "Assistant"
    \/ s.searchAllowed = s.searchBaseline
ScreenUsesRecordedWindow == ~s.screenUsed \/ (s.screenAllowed /\ s.capturedWindow = "A")
SkipPreservesOriginal == s.phase # "Done" \/ s.process \/ s.output = "Raw"
AuditCannotReplaceOutput == s.audit = "Idle" \/ s.output = "Processed"
WorkflowSettles == (s.phase = "Ready") ~> (s.phase = "Done")
=============================================================================
