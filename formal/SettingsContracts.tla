------------------------- MODULE SettingsContracts -------------------------
EXTENDS Naturals, FiniteSets
CONSTANT IgnoreStorageFailure
Providers == {"BuiltIn", "Custom"}
Fields == {"Sound", "Input", "Prompt", "Translation", "FastMode", "Reasoning",
  "Vision", "Selection", "Jev", "Structure", "History", "AppRules", "R2T2"}
VARIABLE s
Init == s = [preferences |-> [f \in Fields |-> 0], version |-> 0,
  providers |-> Providers, active |-> "Custom", assistant |-> "Custom",
  selection |-> "Custom", validation |-> "Custom", vision |-> "Custom",
  keyDisk |-> [p \in Providers |-> FALSE], keyRuntime |-> [p \in Providers |-> FALSE],
  error |-> FALSE, failedKeyWasReported |-> TRUE, edits |-> 0,
  urlOpened |-> "None", invalidUrlOpened |-> FALSE, readVersion |-> 0,
  invalidPreferenceChanged |-> FALSE]
Preference(field, value, valid) ==
  /\ field \in Fields /\ value \in 0..2 /\ valid \in BOOLEAN /\ s.edits < 2
  /\ LET nextValue == IF valid THEN value ELSE s.preferences[field]
     IN s' = [s EXCEPT !.edits = @ + 1, !.error = ~valid,
    !.preferences[field] = nextValue,
    !.invalidPreferenceChanged = s.invalidPreferenceChanged \/ (~valid /\ nextValue # s.preferences[field]),
    !.version = IF valid THEN @ + 1 ELSE @]
RemoveProvider ==
  /\ "Custom" \in s.providers
  /\ s' = [s EXCEPT !.providers = @ \ {"Custom"},
    !.active = "BuiltIn", !.assistant = "BuiltIn",
    !.selection = "BuiltIn", !.validation = "BuiltIn", !.vision = "None"]
ProviderEdit(valid) ==
  /\ valid \in BOOLEAN /\ s.edits < 2
  /\ s' = [s EXCEPT !.edits = @ + 1, !.error = ~valid,
    !.version = IF valid THEN @ + 1 ELSE @]
KeyWrite(provider, value, ok) ==
  /\ provider \in Providers /\ value \in BOOLEAN /\ ok \in BOOLEAN /\ s.edits < 2
  /\ s' = [s EXCEPT !.edits = @ + 1,
    !.keyDisk[provider] = IF ok THEN value ELSE @,
    !.keyRuntime[provider] = IF ok \/ IgnoreStorageFailure THEN value ELSE @,
    !.error = ~ok /\ ~IgnoreStorageFailure,
    !.failedKeyWasReported = s.failedKeyWasReported /\ (ok \/ ~IgnoreStorageFailure)]
OpenUrl(purpose, scheme, host, parseOK) ==
  /\ purpose \in {"Source", "Release", "Provider"}
  /\ scheme \in {"Http", "Https", "File", "Javascript", "Other"}
  /\ host \in {"Github", "Other"} /\ parseOK \in BOOLEAN
  /\ LET allowed == parseOK /\
       IF purpose = "Release" THEN scheme = "Https" /\ host = "Github"
       ELSE scheme \in {"Http", "Https"}
     IN s' = [s EXCEPT !.urlOpened = IF allowed THEN purpose ELSE @,
       !.error = ~allowed,
       !.invalidUrlOpened = s.invalidUrlOpened \/ (allowed /\ scheme \in {"File", "Javascript"})]
ReadSnapshot == s' = [s EXCEPT !.readVersion = s.version]
Next == RemoveProvider \/ ReadSnapshot
  \/ (\E field \in Fields, value \in 0..2, valid \in BOOLEAN: Preference(field, value, valid))
  \/ (\E valid \in BOOLEAN: ProviderEdit(valid))
  \/ (\E provider \in Providers, value, ok \in BOOLEAN: KeyWrite(provider, value, ok))
  \/ (\E purpose \in {"Source", "Release", "Provider"},
       scheme \in {"Http", "Https", "File", "Javascript", "Other"},
       host \in {"Github", "Other"}, parseOK \in BOOLEAN: OpenUrl(purpose, scheme, host, parseOK))
Spec == Init /\ [][Next]_s
ResolvedProvidersExist ==
  s.active \in s.providers /\ s.assistant \in s.providers
  /\ s.selection \in s.providers /\ s.validation \in s.providers
  /\ (s.vision = "None" \/ s.vision \in s.providers)
CredentialFailureIsReported == s.failedKeyWasReported
FailedStorageDoesNotPublishKey == s.keyDisk = s.keyRuntime
NoExecutableUrl == ~s.invalidUrlOpened
NoInvalidPreferencePublication == ~s.invalidPreferenceChanged
SnapshotsDoNotPredictFuture == s.readVersion <= s.version
=============================================================================
