---------------------- MODULE ModelDirectoryMigration ----------------------
EXTENDS Naturals
VARIABLE d
Init == d = [stage |-> "New", sourceExists |-> TRUE, destinationComplete |-> FALSE,
  config |-> "Source", runtime |-> "Source", warning |-> FALSE,
  unsafeDelete |-> FALSE, invalidCopy |-> FALSE, switchedBack |-> FALSE]
Prepare(nested, ok) ==
  /\ d.stage = "New" /\ nested \in BOOLEAN /\ ok \in BOOLEAN
  /\ LET allowed == ~nested
     IN d' = [d EXCEPT !.stage = IF allowed /\ ok THEN "Copied" ELSE "Failed",
       !.destinationComplete = allowed /\ ok,
       !.invalidCopy = d.invalidCopy \/ (allowed /\ nested)]
Commit(ok) ==
  /\ d.stage = "Copied" /\ ok \in BOOLEAN
  /\ d' = [d EXCEPT !.stage = IF ok THEN "Committed" ELSE "Failed",
    !.config = IF ok THEN "Destination" ELSE @]
UpdateRuntime(ok) ==
  /\ d.stage = "Committed" /\ ok \in BOOLEAN
  /\ d' = [d EXCEPT !.stage = IF ok THEN "Ready" ELSE "Warning",
    !.runtime = IF ok THEN "Destination" ELSE "Unavailable", !.warning = ~ok]
SwitchBack ==
  /\ d.stage = "Ready" /\ ~d.switchedBack
  /\ d' = [d EXCEPT !.config = "Source", !.runtime = "Source", !.switchedBack = TRUE]
Cleanup(ok) ==
  /\ d.stage = "Ready" /\ ok \in BOOLEAN
  /\ LET allowed == d.config = "Destination" /\ d.runtime = "Destination"
         deleted == allowed /\ ok
     IN d' = [d EXCEPT !.stage = "Done", !.sourceExists = IF deleted THEN FALSE ELSE @,
       !.warning = allowed /\ ~ok,
       !.unsafeDelete = d.unsafeDelete \/
         (deleted /\ (~d.destinationComplete \/ d.config # "Destination" \/ d.runtime # "Destination"))]
Next == SwitchBack \/ (\E nested, ok \in BOOLEAN: Prepare(nested, ok))
  \/ (\E ok \in BOOLEAN: Commit(ok) \/ UpdateRuntime(ok) \/ Cleanup(ok))
Spec == Init /\ [][Next]_d
NoDeleteBeforeSafeCommit == ~d.unsafeDelete
NoNestedCopy == ~d.invalidCopy
FailureRetainsSource == d.stage \in {"Failed", "Warning"} => d.sourceExists
DestinationNeededForDeletion == ~d.sourceExists => d.destinationComplete
RuntimeFailureIsVisible == d.stage = "Warning" => d.warning
=============================================================================
