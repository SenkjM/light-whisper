----------------------------- MODULE OAuthStorage -----------------------------
EXTENDS Naturals
CONSTANT Legacy
VARIABLE o
Init == o = [metadata |-> "Valid", token |-> TRUE, legacy |-> FALSE,
  phase |-> "Idle", loaded |-> FALSE, acknowledgedLogout |-> FALSE,
  error |-> FALSE, orphanAccepted |-> FALSE]
BeginSave(markerOK) ==
  /\ o.phase \in {"Idle", "Committed", "Failed", "LoggedOut"} /\ markerOK \in BOOLEAN
  /\ o' = [o EXCEPT !.phase = IF markerOK THEN "Intent" ELSE "Failed",
    !.metadata = IF markerOK THEN "Tombstone" ELSE @, !.error = ~markerOK,
    !.acknowledgedLogout = IF markerOK THEN FALSE ELSE @]
WriteToken(ok) ==
  /\ o.phase = "Intent" /\ ok \in BOOLEAN
  /\ o' = [o EXCEPT !.phase = IF ok THEN "TokenWritten" ELSE "Failed",
    !.token = IF ok THEN TRUE ELSE @, !.error = ~ok]
CommitMetadata(ok) ==
  /\ o.phase = "TokenWritten" /\ ok \in BOOLEAN
  /\ o' = [o EXCEPT !.phase = IF ok THEN "Committed" ELSE "Failed",
    !.metadata = IF ok THEN "Valid" ELSE @, !.error = ~ok]
Logout(markerOK, deleteOK) ==
  /\ o.phase \in {"Idle", "Committed", "Failed"}
  /\ markerOK \in BOOLEAN /\ deleteOK \in BOOLEAN
  /\ o' = [o EXCEPT !.phase = "LoggedOut", !.loaded = FALSE,
    !.metadata = IF markerOK THEN "Tombstone" ELSE @,
    !.token = IF deleteOK THEN FALSE ELSE @,
    !.acknowledgedLogout = markerOK /\ deleteOK,
    !.error = ~markerOK \/ ~deleteOK]
CorruptMetadata ==
  /\ o.metadata # "Corrupt"
  /\ o' = [o EXCEPT !.metadata = "Corrupt"]
RemoveMetadata ==
  /\ o.metadata # "Missing"
  /\ o' = [o EXCEPT !.metadata = "Missing"]
Restart ==
  /\ LET accepted == o.token /\ (o.metadata = "Valid" \/
       (Legacy /\ o.metadata \in {"Corrupt", "Missing"}))
     IN o' = [o EXCEPT !.loaded = accepted,
       !.orphanAccepted = o.orphanAccepted \/ (accepted /\ o.metadata # "Valid")]
Next == CorruptMetadata \/ RemoveMetadata \/ Restart
  \/ (\E ok \in BOOLEAN: BeginSave(ok) \/ WriteToken(ok) \/ CommitMetadata(ok))
  \/ (\E markerOK, deleteOK \in BOOLEAN: Logout(markerOK, deleteOK))
Spec == Init /\ [][Next]_o
InvalidMetadataCannotRestoreRefreshToken == ~o.orphanAccepted
AcknowledgedLogoutCannotReload == o.acknowledgedLogout => ~o.loaded
FailedSaveCannotCommit == o.phase = "Failed" /\ o.metadata = "Tombstone" => ~o.loaded
=============================================================================
