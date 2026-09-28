---------------------------- MODULE HistoryRecords ----------------------------
EXTENDS Naturals, Sequences

CONSTANT MaxRecords, MaxLeaseCount, MaxRetentionDays

ASSUME MaxRecords \in Nat \ {4}
ASSUME MaxLeaseCount \in Nat \ {0}
ASSUME MaxRetentionDays \in Nat

RecordIds == 1..MaxRecords
AudioIds == {"A", "B", "C"}
AudioRefs == AudioIds \cup {"None"}
Workflows == {"dictation", "assistant", "edit"}
ReprocessKinds == {"asr", "polish"}
ReprocessPhases == {"Idle", "Leased", "Ready", "Inserted", "Failed"}
ExportPhases == {"Idle", "Reading", "Written", "Failed", "Cancelled"}
IOStatuses == {"Idle", "Success", "Failure"}

Update(f, key, value) == [f EXCEPT ![key] = value]

References(s, audio) ==
  \E record \in s.records: s.recordAudio[record] = audio

AudioSetForRecords(s, rows) ==
  {audio \in AudioIds: \E record \in rows: s.recordAudio[record] = audio}

NoTransaction(s) == s.deleteTarget = 0 /\ ~s.retentionActive

AudioExists(s, audio) == IF audio = "None" THEN FALSE ELSE s.audioPresent[audio]
LeaseAvailable(s, audio) == IF audio = "None" THEN TRUE ELSE s.leases[audio] < MaxLeaseCount
LeaseHeld(s, audio) == IF audio = "None" THEN TRUE ELSE s.leases[audio] > 0

InitialAudio ==
  [record \in RecordIds |->
    IF record \in {1, 2} THEN "A"
    ELSE IF record \in {3, 4} THEN "B"
    ELSE "None"]

InitialText ==
  [record \in RecordIds |->
    IF record = 1 THEN "one"
    ELSE IF record = 2 THEN "two"
    ELSE IF record = 3 THEN "three"
    ELSE IF record = 4 THEN "assistant"
    ELSE ""]

InitialOriginal ==
  [record \in RecordIds |->
    IF record = 1 THEN "raw one"
    ELSE IF record = 2 THEN "raw two"
    ELSE IF record = 3 THEN "raw three"
    ELSE IF record = 4 THEN "assistant source"
    ELSE ""]

InitialWorkflow ==
  [record \in RecordIds |-> IF record = 4 THEN "assistant" ELSE "dictation"]

InitialConsent == [record \in RecordIds |-> record \in {1, 2, 3, 4}]
InitialZero == [record \in RecordIds |-> 0]
InitialNoneAudio == [record \in RecordIds |-> "None"]
InitialFalseAudio == [audio \in AudioIds |-> FALSE]
InitialLeases == [audio \in AudioIds |-> 0]

VARIABLE state
vars == <<state>>

Init ==
  state = [
    records             |-> {1, 2, 3, 4},
    expired             |-> {},
    nextRecord          |-> 4,
    recordAudio         |-> InitialAudio,
    recordText          |-> InitialText,
    recordOriginal      |-> InitialOriginal,
    recordWorkflow      |-> InitialWorkflow,
    recordFrom          |-> InitialZero,
    originAudio         |-> InitialAudio,
    originText          |-> InitialOriginal,
    recordConsent       |-> InitialConsent,
    audioPresent        |-> Update(Update(InitialFalseAudio, "A", TRUE), "B", TRUE),
    leases              |-> InitialLeases,
    gcCandidates        |-> {},
    gcFailures          |-> {},
    deleteAttempts      |-> 0,
    deleteTarget        |-> 0,
    deleteAudio         |-> "None",
    retentionActive     |-> FALSE,
    retentionTargets    |-> {},
    retentionAudio      |-> {},
    historyEnabled      |-> FALSE,
    requestedSaveAudio  |-> FALSE,
    saveAudioEffective  |-> FALSE,
    settingsUpdated     |-> FALSE,
    retentionDays       |-> 1,
    retentionAttempted  |-> FALSE,
    reprocessPhase      |-> "Idle",
    reprocessReadAttempted |-> FALSE,
    reprocessKind       |-> "polish",
    reprocessSource     |-> 0,
    snapshotAudio       |-> "None",
    snapshotText        |-> "",
    snapshotOriginal    |-> "",
    snapshotWorkflow    |-> "dictation",
    snapshotConsent     |-> FALSE,
    insertedRecord      |-> 0,
    readStatus          |-> "Idle",
    exportStatus        |-> "Idle",
    exportStarted       |-> FALSE
  ]

\* set_history_settings stores save_audio only when history itself is enabled.
SetHistorySettings(enabled, requested, days) ==
  /\ days \in 0..MaxRetentionDays
  /\ ~state.settingsUpdated
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.historyEnabled = enabled,
       !.requestedSaveAudio = requested,
       !.saveAudioEffective = enabled /\ requested,
       !.settingsUpdated = TRUE,
       !.retentionDays = days,
       !.readStatus = "Idle"
     ]

\* finalize.rs can persist text with no audio, or audio only after both consent
\* gates are true. New audio uses a fresh abstract identity C.
PersistRecord(audio) ==
  LET newId == state.nextRecord + 1
      consent == audio # "None"
  IN /\ state.historyEnabled
     /\ newId \in RecordIds
     /\ audio \in {"None", "C"}
     /\ (audio = "None" \/ state.saveAudioEffective)
     /\ state' = [state EXCEPT
          !.records = state.records \cup {newId},
          !.nextRecord = newId,
          !.recordAudio = Update(state.recordAudio, newId, audio),
          !.recordText = Update(state.recordText, newId, "persisted"),
          !.recordOriginal = Update(state.recordOriginal, newId, "persisted raw"),
          !.recordWorkflow = Update(state.recordWorkflow, newId, "dictation"),
          !.recordFrom = Update(state.recordFrom, newId, 0),
          !.originAudio = Update(state.originAudio, newId, audio),
          !.originText = Update(state.originText, newId, "persisted raw"),
          !.recordConsent = Update(state.recordConsent, newId, consent),
          !.audioPresent = IF audio = "None"
                            THEN state.audioPresent
                            ELSE Update(state.audioPresent, audio, TRUE),
          !.readStatus = "Idle"
        ]

\* get_for_reprocess_with_connection performs the row read and lease increment
\* in one IMMEDIATE transaction. The snapshot survives later row deletion.
AcquireReprocess(record, kind) ==
  LET audio == state.recordAudio[record]
      sourceText == IF kind = "asr"
                    THEN state.recordOriginal[record]
                    ELSE IF state.recordOriginal[record] # ""
                         THEN state.recordOriginal[record]
                         ELSE state.recordText[record]
      nextLeases == IF audio = "None"
                    THEN state.leases
                    ELSE Update(state.leases, audio, state.leases[audio] + 1)
  IN /\ record \in {1, 4, 5} \cap state.records
     /\ kind \in ReprocessKinds
     /\ state.reprocessPhase = "Idle"
     /\ ~state.reprocessReadAttempted
     /\ NoTransaction(state)
     /\ LeaseAvailable(state, audio)
     /\ state' = [state EXCEPT
          !.leases = nextLeases,
          !.reprocessPhase = "Leased",
          !.reprocessKind = kind,
          !.reprocessSource = record,
          !.snapshotAudio = audio,
          !.snapshotText = sourceText,
          !.snapshotOriginal = state.recordOriginal[record],
          !.snapshotWorkflow = state.recordWorkflow[record],
          !.snapshotConsent = state.recordConsent[record],
          !.insertedRecord = 0,
          !.reprocessReadAttempted = TRUE,
          !.readStatus = "Success"
        ]

\* Database/query failures return before changing rows or leases.
AcquireQueryFailure(record) ==
  /\ record \in {1, 4}
  /\ state.reprocessPhase = "Idle"
  /\ ~state.reprocessReadAttempted
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.reprocessReadAttempted = TRUE,
       !.readStatus = "Failure"]

\* A missing source is the normal None result of get_for_reprocess.
AcquireMissing(record) ==
  /\ record \in {1, 5} \ state.records
  /\ state.reprocessPhase = "Idle"
  /\ ~state.reprocessReadAttempted
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.reprocessReadAttempted = TRUE,
       !.readStatus = "Failure"]

RejectNonDictation ==
  /\ state.reprocessPhase = "Leased"
  /\ state.snapshotWorkflow # "dictation"
  /\ state' = [state EXCEPT
       !.reprocessPhase = "Failed",
       !.readStatus = "Failure"
     ]

PreparePolish ==
  /\ state.reprocessPhase = "Leased"
  /\ state.reprocessKind = "polish"
  /\ state.snapshotWorkflow = "dictation"
  /\ state' = IF state.snapshotText = ""
                 THEN [state EXCEPT
                        !.reprocessPhase = "Failed",
                        !.readStatus = "Failure"]
                 ELSE [state EXCEPT
                        !.reprocessPhase = "Ready",
                        !.readStatus = "Success"]

ReadAudioSuccess ==
  /\ state.reprocessPhase = "Leased"
  /\ state.reprocessKind = "asr"
  /\ state.snapshotWorkflow = "dictation"
  /\ state.snapshotAudio # "None"
  /\ AudioExists(state, state.snapshotAudio)
  /\ state' = [state EXCEPT
       !.reprocessPhase = "Ready",
       !.readStatus = "Success"
     ]

ReadAudioFailure ==
  /\ state.reprocessPhase = "Leased"
  /\ state.reprocessKind = "asr"
  /\ state.snapshotWorkflow = "dictation"
  /\ state.snapshotAudio # "None"
  /\ state' = [state EXCEPT
       !.reprocessPhase = "Failed",
       !.readStatus = "Failure"
     ]

ReadAudioMissing ==
  /\ state.reprocessPhase = "Leased"
  /\ state.reprocessKind = "asr"
  /\ state.snapshotWorkflow = "dictation"
  /\ state.snapshotAudio = "None"
  /\ state' = [state EXCEPT
       !.reprocessPhase = "Failed",
       !.readStatus = "Failure"
     ]

\* The insert uses the captured snapshot even if reprocessSource is no longer
\* present in the rows table.
InsertReprocessed ==
  LET newId == state.nextRecord + 1
  IN /\ state.reprocessPhase = "Ready"
     /\ newId \in RecordIds
     /\ state' = [state EXCEPT
          !.records = state.records \cup {newId},
          !.nextRecord = newId,
          !.recordAudio = Update(state.recordAudio, newId, state.snapshotAudio),
          !.recordText = Update(state.recordText, newId, "reprocessed"),
          !.recordOriginal = Update(state.recordOriginal, newId, state.snapshotText),
          !.recordWorkflow = Update(state.recordWorkflow, newId, "dictation"),
          !.recordFrom = Update(state.recordFrom, newId, state.reprocessSource),
          !.originAudio = Update(state.originAudio, newId, state.snapshotAudio),
          !.originText = Update(state.originText, newId, state.snapshotText),
          !.recordConsent = Update(state.recordConsent, newId, state.snapshotConsent),
          !.insertedRecord = newId,
          !.reprocessPhase = "Inserted",
          !.readStatus = "Success"
        ]

InsertReprocessFailure ==
  /\ state.reprocessPhase = "Ready"
  /\ state' = [state EXCEPT
       !.reprocessPhase = "Failed",
       !.readStatus = "Failure"
     ]

ReleaseReprocessLease ==
  LET audio == state.snapshotAudio
      nextLeases == IF audio = "None"
                    THEN state.leases
                    ELSE Update(state.leases, audio, state.leases[audio] - 1)
  IN /\ state.reprocessPhase \in {"Inserted", "Failed"}
     /\ LeaseHeld(state, audio)
     /\ state' = [state EXCEPT
          !.leases = nextLeases,
          !.reprocessPhase = "Idle",
          !.reprocessSource = 0,
          !.snapshotAudio = "None",
          !.snapshotText = "",
          !.snapshotOriginal = "",
          !.snapshotWorkflow = "dictation",
          !.snapshotConsent = FALSE,
          !.insertedRecord = 0,
          !.readStatus = "Idle"
        ]

\* delete() reads the audio identity and deletes the row in one IMMEDIATE
\* transaction. GC is deliberately a later operation.
BeginDelete(record) ==
  /\ record \in {1, 2} \cap state.records
  /\ state.deleteAttempts < 1
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.deleteAttempts = state.deleteAttempts + 1,
       !.deleteTarget = record,
       !.deleteAudio = state.recordAudio[record],
       !.readStatus = "Success"
     ]

DeleteQueryFailure(record) ==
  /\ record \in {1, 2} \cap state.records
  /\ state.deleteAttempts < 1
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.deleteAttempts = state.deleteAttempts + 1,
       !.readStatus = "Failure"]

CommitDelete ==
  LET audio == state.deleteAudio
      candidates == IF audio = "None"
                    THEN state.gcCandidates
                    ELSE state.gcCandidates \cup {audio}
  IN /\ state.deleteTarget # 0
     /\ state' = [state EXCEPT
          !.records = state.records \ {state.deleteTarget},
          !.expired = state.expired \ {state.deleteTarget},
          !.gcCandidates = candidates,
          !.deleteTarget = 0,
          !.deleteAudio = "None",
          !.readStatus = "Idle"
        ]

AbortDelete ==
  /\ state.deleteTarget # 0
  /\ state' = [state EXCEPT
       !.deleteTarget = 0,
       !.deleteAudio = "None",
       !.readStatus = "Failure"
     ]

MarkExpired(record) ==
  /\ record \in {3, 4} \cap state.records
  /\ record \notin state.expired
  /\ state' = [state EXCEPT !.expired = state.expired \cup {record}]

\* cleanup_expired_with_connection selects distinct audio identities and then
\* deletes every expired row in one transaction before running post-commit GC.
BeginRetention ==
  /\ NoTransaction(state)
  /\ state.expired # {}
  /\ state.retentionDays > 0
  /\ ~state.retentionAttempted
  /\ state' = [state EXCEPT
       !.retentionActive = TRUE,
       !.retentionAttempted = TRUE,
       !.retentionTargets = state.expired,
       !.retentionAudio = AudioSetForRecords(state, state.expired),
       !.readStatus = "Success"
     ]

RetentionQueryFailure ==
  /\ NoTransaction(state)
  /\ state.expired # {}
  /\ ~state.retentionAttempted
  /\ state' = [state EXCEPT
       !.retentionAttempted = TRUE,
       !.readStatus = "Failure"]

CommitRetention ==
  /\ state.retentionActive
  /\ state' = [state EXCEPT
       !.records = state.records \ state.retentionTargets,
       !.expired = state.expired \ state.retentionTargets,
       !.gcCandidates = state.gcCandidates \cup state.retentionAudio,
       !.retentionActive = FALSE,
       !.retentionTargets = {},
       !.retentionAudio = {},
       !.readStatus = "Idle"
     ]

AbortRetention ==
  /\ state.retentionActive
  /\ state' = [state EXCEPT
       !.retentionActive = FALSE,
       !.retentionTargets = {},
       !.retentionAudio = {},
       !.readStatus = "Failure"
     ]

\* A successful GC removes an audio file only after both reference and lease
\* queries say zero. A query failure leaves the candidate for retry.
GC(audio) ==
  /\ audio \in state.gcCandidates
  /\ state.audioPresent[audio]
  /\ ~References(state, audio)
  /\ state.leases[audio] = 0
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.audioPresent = Update(state.audioPresent, audio, FALSE),
       !.gcCandidates = state.gcCandidates \ {audio},
       !.gcFailures = state.gcFailures \ {audio},
       !.readStatus = "Idle"
     ]

GCQueryFailure(audio) ==
  /\ audio \in state.gcCandidates
  /\ state.audioPresent[audio]
  /\ audio \notin state.gcFailures
  /\ NoTransaction(state)
  /\ state' = [state EXCEPT
       !.gcFailures = state.gcFailures \cup {audio},
       !.readStatus = "Failure"
     ]

BeginExport ==
  /\ state.exportStatus = "Idle"
  /\ ~state.exportStarted
  /\ state' = [state EXCEPT
       !.exportStatus = "Reading",
       !.exportStarted = TRUE]

ExportSuccess ==
  /\ state.exportStatus = "Reading"
  /\ state' = [state EXCEPT !.exportStatus = "Written"]

ExportFailure ==
  /\ state.exportStatus = "Reading"
  /\ state' = [state EXCEPT !.exportStatus = "Failed"]

ExportCancelled ==
  /\ state.exportStatus = "Reading"
  /\ state' = [state EXCEPT !.exportStatus = "Cancelled"]

Next ==
  \/ \E enabled \in BOOLEAN, requested \in BOOLEAN,
          days \in 0..MaxRetentionDays:
       SetHistorySettings(enabled, requested, days)
  \/ \E audio \in {"None", "C"}: PersistRecord(audio)
  \/ \E record \in RecordIds, kind \in ReprocessKinds:
       AcquireReprocess(record, kind)
  \/ \E record \in RecordIds: AcquireQueryFailure(record) \/ AcquireMissing(record)
  \/ RejectNonDictation
  \/ PreparePolish
  \/ ReadAudioSuccess
  \/ ReadAudioFailure
  \/ ReadAudioMissing
  \/ InsertReprocessed
  \/ InsertReprocessFailure
  \/ ReleaseReprocessLease
  \/ \E record \in RecordIds: BeginDelete(record) \/ DeleteQueryFailure(record)
  \/ CommitDelete
  \/ AbortDelete
  \/ \E record \in RecordIds: MarkExpired(record)
  \/ BeginRetention
  \/ RetentionQueryFailure
  \/ CommitRetention
  \/ AbortRetention
  \/ \E audio \in AudioIds: GC(audio) \/ GCQueryFailure(audio)
  \/ BeginExport
  \/ ExportSuccess
  \/ ExportFailure
  \/ ExportCancelled

Spec == Init /\ [][Next]_vars

\* Fairness is attached only to successful GC I/O. The liveness property in the
\* cfg is conditional on a candidate remaining unreferenced, unleased, and
\* outside an SQL transaction forever.
FairSpec == Spec /\ (\A audio \in AudioIds: WF_vars(GC(audio)))

CleanupEventually ==
  \A audio \in AudioIds:
    (<>[](
       audio \in state.gcCandidates
       /\ state.audioPresent[audio]
       /\ ~References(state, audio)
       /\ state.leases[audio] = 0
       /\ NoTransaction(state)
     ))
     => <> (audio \notin state.gcCandidates \/ ~state.audioPresent[audio])

TypeOK ==
  /\ state.records \subseteq RecordIds
  /\ state.expired \subseteq state.records
  /\ state.nextRecord \in 0..MaxRecords
  /\ state.recordAudio \in [RecordIds -> AudioRefs]
  /\ state.recordText \in [RecordIds -> STRING]
  /\ state.recordOriginal \in [RecordIds -> STRING]
  /\ state.recordWorkflow \in [RecordIds -> Workflows]
  /\ state.recordFrom \in [RecordIds -> 0..MaxRecords]
  /\ state.originAudio \in [RecordIds -> AudioRefs]
  /\ state.originText \in [RecordIds -> STRING]
  /\ state.recordConsent \in [RecordIds -> BOOLEAN]
  /\ state.audioPresent \in [AudioIds -> BOOLEAN]
  /\ state.leases \in [AudioIds -> 0..MaxLeaseCount]
  /\ state.gcCandidates \subseteq AudioIds
  /\ state.gcFailures \subseteq AudioIds
  /\ state.deleteTarget \in ({0} \cup RecordIds)
  /\ state.deleteAttempts \in 0..1
  /\ state.deleteAudio \in AudioRefs
  /\ state.retentionActive \in BOOLEAN
  /\ state.retentionTargets \subseteq RecordIds
  /\ state.retentionAudio \subseteq AudioIds
  /\ state.historyEnabled \in BOOLEAN
  /\ state.requestedSaveAudio \in BOOLEAN
  /\ state.saveAudioEffective \in BOOLEAN
  /\ state.settingsUpdated \in BOOLEAN
  /\ state.retentionDays \in 0..MaxRetentionDays
  /\ state.retentionAttempted \in BOOLEAN
  /\ state.reprocessPhase \in ReprocessPhases
  /\ state.reprocessReadAttempted \in BOOLEAN
  /\ state.reprocessKind \in ReprocessKinds
  /\ state.reprocessSource \in ({0} \cup RecordIds)
  /\ state.snapshotAudio \in AudioRefs
  /\ state.snapshotText \in STRING
  /\ state.snapshotOriginal \in STRING
  /\ state.snapshotWorkflow \in Workflows
  /\ state.snapshotConsent \in BOOLEAN
  /\ state.insertedRecord \in ({0} \cup RecordIds)
  /\ state.readStatus \in IOStatuses
  /\ state.exportStatus \in ExportPhases
  /\ state.exportStarted \in BOOLEAN

SaveAudioGate ==
  state.saveAudioEffective = (state.historyEnabled /\ state.requestedSaveAudio)

PersistedAudioHasConsent ==
  \A record \in state.records:
    state.recordAudio[record] # "None" => state.recordConsent[record]

ReferencedOrLeasedAudioRetained ==
  \A audio \in AudioIds:
    (References(state, audio) \/ state.leases[audio] > 0)
      => state.audioPresent[audio]

GCFaultRetainsAudio ==
  \A audio \in state.gcFailures: state.audioPresent[audio]

DeleteTransactionIsAtomic ==
  /\ state.deleteTarget # 0
       => /\ state.deleteTarget \in state.records
          /\ state.deleteAudio = state.recordAudio[state.deleteTarget]
  /\ state.retentionActive => state.retentionTargets \subseteq state.records

ReprocessLeaseProtectsSnapshot ==
  state.reprocessPhase \in {"Leased", "Ready", "Inserted", "Failed"}
    /\ state.snapshotAudio # "None"
    => state.leases[state.snapshotAudio] > 0

DeletedSourceStillLeased ==
  /\ state.reprocessPhase \in {"Leased", "Ready", "Inserted", "Failed"}
  /\ state.reprocessSource # 0
  /\ state.reprocessSource \notin state.records
  /\ state.snapshotAudio # "None"
  => state.leases[state.snapshotAudio] > 0

ReprocessedRowsUseSnapshot ==
  \A record \in state.records:
    state.recordFrom[record] # 0
      => /\ state.recordAudio[record] = state.originAudio[record]
         /\ state.recordOriginal[record] = state.originText[record]

=============================================================================
