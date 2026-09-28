------------------------- MODULE RuntimeConfiguration -------------------------
EXTENDS Naturals
Engines == {"Qwen", "R2T2", "GLM", "Alibaba"}
Cloud == {"GLM", "Alibaba"}
Regions == {"International", "Domestic"}
Slots == {"GLM", "AlibabaInternational", "AlibabaDomestic"}
Slot(e, r) == IF e = "Alibaba" THEN
  IF r = "Domestic" THEN "AlibabaDomestic" ELSE "AlibabaInternational" ELSE "GLM"
VARIABLE s
Init == s = [engine |-> "Qwen", region |-> "International", directory |-> 0,
  epoch |-> 0, serverGeneration |-> 0, readyGeneration |-> 0,
  status |-> "Idle", startEpoch |-> 0, startPending |-> FALSE,
  starts |-> 0, downloads |-> 0, download |-> "Idle", downloadEpoch |-> 0,
  input |-> "Idle", inputUsed |-> FALSE, asrEngine |-> "Qwen",
  asrSlot |-> "GLM", keys |-> [k \in Slots |-> FALSE], runtimeKey |-> FALSE,
  keySlot |-> "GLM", saveTarget |-> "GLM", saveValue |-> FALSE,
  savePending |-> FALSE, saves |-> 0, rejected |-> FALSE]
CanChange == s.input = "Idle" /\ s.download = "Idle"
ChangeEngine(e, success) ==
  /\ e \in Engines /\ e # s.engine /\ success \in BOOLEAN /\ s.epoch < 2
  /\ CanChange /\ s.serverGeneration < 3
  /\ s' = IF success THEN [s EXCEPT !.engine = e, !.epoch = @ + 1,
    !.serverGeneration = @ + 1,
    !.status = IF e \in Cloud THEN "Ready" ELSE "Idle",
    !.keySlot = Slot(e, s.region), !.runtimeKey = s.keys[Slot(e, s.region)]]
    ELSE [s EXCEPT !.rejected = TRUE]
ChangeDirectory(d, success) ==
  /\ d \in 0..1 /\ d # s.directory /\ success \in BOOLEAN
  /\ s.epoch < 2 /\ CanChange /\ s.serverGeneration < 3
  /\ s' = IF success THEN [s EXCEPT !.directory = d, !.epoch = @ + 1,
    !.serverGeneration = @ + 1,
    !.status = IF s.engine \in Cloud THEN s.status ELSE "Idle"]
    ELSE [s EXCEPT !.rejected = TRUE]
ChangeRegion(r) ==
  /\ r \in Regions /\ r # s.region /\ s.epoch < 2
  /\ s' = [s EXCEPT !.region = r, !.keySlot = Slot(s.engine, r),
    !.runtimeKey = s.keys[Slot(s.engine, r)]]
RejectBusyConfig ==
  /\ ~CanChange /\ ~s.rejected
  /\ s' = [s EXCEPT !.rejected = TRUE]
BeginStart ==
  /\ s.engine \notin Cloud /\ ~s.startPending /\ s.status # "Ready"
  /\ s.starts < 2
  /\ s' = [s EXCEPT !.starts = @ + 1, !.startPending = TRUE,
    !.startEpoch = s.serverGeneration, !.status = "Starting"]
FinishStart(success) ==
  /\ s.startPending /\ success \in BOOLEAN
  /\ s' = [s EXCEPT !.startPending = FALSE,
    !.status = IF s.startEpoch = s.serverGeneration
      THEN IF success THEN "Ready" ELSE "Failed" ELSE s.status,
    !.readyGeneration = IF s.startEpoch = s.serverGeneration /\ success
      THEN s.startEpoch ELSE @]
BeginDownload ==
  /\ s.engine \notin Cloud /\ s.download = "Idle" /\ s.downloads < 1
  /\ s' = [s EXCEPT !.download = "Running", !.downloads = @ + 1,
    !.downloadEpoch = s.epoch]
CancelDownload ==
  /\ s.download = "Running"
  /\ s' = [s EXCEPT !.download = "Cancelling"]
FinishDownload ==
  /\ s.download # "Idle"
  /\ s' = [s EXCEPT !.download = "Idle"]
QueueKeySave(k, value) ==
  /\ k \in Slots /\ value \in BOOLEAN /\ s.saves < 2 /\ ~s.savePending
  /\ s' = [s EXCEPT !.saveTarget = k, !.saveValue = value,
    !.savePending = TRUE, !.saves = @ + 1]
CommitKeySave ==
  /\ s.savePending
  /\ s' = [s EXCEPT !.keys[s.saveTarget] = s.saveValue,
    !.savePending = FALSE,
    !.runtimeKey = IF s.saveTarget = Slot(s.engine, s.region)
      THEN s.saveValue ELSE s.runtimeKey]
BeginInput ==
  /\ ~s.inputUsed /\ s.input = "Idle" /\ s.status = "Ready"
  /\ (s.engine \notin Cloud \/ s.runtimeKey)
  /\ s' = [s EXCEPT !.input = "Recording", !.inputUsed = TRUE]
BeginAsr ==
  /\ s.input = "Recording"
  /\ s' = [s EXCEPT !.input = "Asr", !.asrEngine = s.engine,
    !.asrSlot = Slot(s.engine, s.region)]
FinishAsr ==
  /\ s.input = "Asr"
  /\ s' = [s EXCEPT !.input = "Idle"]
Restart ==
  /\ s.input = "Idle" /\ s.status = "Ready" /\ s.serverGeneration < 3
  /\ s' = [s EXCEPT !.status = IF s.engine \in Cloud THEN "Ready" ELSE "Idle",
    !.serverGeneration = IF s.engine \in Cloud THEN @ ELSE @ + 1]
Next ==
  \/ \E e \in Engines, ok \in BOOLEAN: ChangeEngine(e, ok)
  \/ \E d \in 0..1, ok \in BOOLEAN: ChangeDirectory(d, ok)
  \/ \E r \in Regions: ChangeRegion(r)
  \/ RejectBusyConfig \/ BeginStart
  \/ \E ok \in BOOLEAN: FinishStart(ok)
  \/ BeginDownload \/ CancelDownload \/ FinishDownload
  \/ \E k \in Slots, v \in BOOLEAN: QueueKeySave(k, v)
  \/ CommitKeySave \/ BeginInput \/ BeginAsr \/ FinishAsr \/ Restart
Spec == Init /\ [][Next]_s
  /\ WF_s(CommitKeySave) /\ WF_s(FinishDownload) /\ WF_s(FinishAsr)
  /\ WF_s(\E ok \in BOOLEAN: FinishStart(ok))
TypeOK == s.engine \in Engines /\ s.region \in Regions /\ s.directory \in 0..1
  /\ s.epoch \in 0..2 /\ s.input \in {"Idle", "Recording", "Asr"}
  /\ s.status \in {"Idle", "Starting", "Ready", "Failed"}
ActiveKeyComesFromActiveSlot ==
  s.keySlot = Slot(s.engine, s.region) /\ s.runtimeKey = s.keys[s.keySlot]
DownloadPinsConfiguration == s.download = "Idle" \/ s.downloadEpoch = s.epoch
AsrPinsEngine == s.input # "Asr" \/ s.asrEngine = s.engine
NoStaleStartPublication ==
  s.engine \in Cloud \/ s.status # "Ready" \/ s.readyGeneration = s.serverGeneration
BackgroundSettles ==
  /\ s.savePending ~> ~s.savePending
  /\ s.startPending ~> ~s.startPending
  /\ (s.download # "Idle") ~> (s.download = "Idle")
=============================================================================
