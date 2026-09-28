--------------------------- MODULE HotkeyLifecycle ---------------------------
EXTENDS Naturals, FiniteSets

CONSTANT MaxRegistrations, MaxSessions, MaxModeSwitches, MaxEvents, EventKind

ASSUME MaxRegistrations \in Nat \ {0}
ASSUME MaxSessions \in Nat \ {0}
ASSUME MaxModeSwitches \in Nat
ASSUME MaxEvents \in Nat
ASSUME EventKind \in {"Dictation", "Translation", "Assistant"}

Kinds == {"Dictation", "Translation", "Assistant"}
Labels == {"F2", "F3", "CapsLock"}
Modes == {"Hold", "Toggle"}
Backends == {"RegisterHotKey", "LowLevelHook"}
Phases == {"Unused", "Starting", "Recording", "Processing", "Finished"}
StopCauses == {"release", "registration", "unregister", "mode", "late"}
StateIds == 1..MaxRegistrations
SessionIds == 1..MaxSessions
SystemOccupied == {"F2"}

VARIABLES mode, nextRegistration, nextSession, nextEvent, eventsUsed,
          registrations, stateExists, stateKind, stateLabel, stateForceLow,
          stateBackend, statePressed, stateToggleActive, stateOwner,
          acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
          sessionState, createdSessions, currentSession, stopRequests,
          stopResults, rejectedConflicts, systemConflicts, duplicatePresses,
          duplicateReleases, toggleReleaseNoops, lateReleases, modeSwitches

vars == <<mode, nextRegistration, nextSession, nextEvent, eventsUsed,
           registrations, stateExists, stateKind, stateLabel, stateForceLow,
           stateBackend, statePressed, stateToggleActive, stateOwner,
           acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
           sessionState, createdSessions, currentSession, stopRequests,
           stopResults, rejectedConflicts, systemConflicts, duplicatePresses,
           duplicateReleases, toggleReleaseNoops, lateReleases, modeSwitches>>

DesiredBackend(m, forceLow) ==
  IF m = "Toggle" /\ ~forceLow THEN "RegisterHotKey" ELSE "LowLevelHook"

ActualBackend(m, label, forceLow) ==
  IF DesiredBackend(m, forceLow) = "RegisterHotKey"
        /\ label \in SystemOccupied
  THEN "LowLevelHook"
  ELSE DesiredBackend(m, forceLow)

ForceLow(label) == label = "CapsLock"

CurrentStateIds == {registrations[k] : k \in Kinds} \ {0}

CurrentLabel(k) ==
  IF registrations[k] = 0 THEN "None" ELSE stateLabel[registrations[k]]

Conflicting(k, label) ==
  \E other \in Kinds : other # k /\ CurrentLabel(other) = label

\* The bounded suite uses one representative sequence for the three slots
\* and one replacement. RejectInternalConflict still explores the representative
\* duplicate candidate, so conflict handling is not reduced to the happy path.
PlannedRegister(n, k, label) ==
  \/ n = 0 /\ k = EventKind /\ label = "F2"
  \/ n = 1 /\ k = (IF EventKind = "Translation" THEN "Dictation" ELSE "Translation") /\ label = "F3"
  \/ n = 2 /\ k = (IF EventKind = "Assistant" THEN "Dictation" ELSE "Assistant") /\ label = "CapsLock"
  \/ n = 3 /\ k = EventKind /\ label = "F3"

Init ==
  /\ mode = "Hold"
  /\ nextRegistration = 0
  /\ nextSession = 0
  /\ nextEvent = 0
  /\ eventsUsed = 0
  /\ registrations = [k \in Kinds |-> 0]
  /\ stateExists = {}
  /\ stateKind = [s \in StateIds |-> "None"]
  /\ stateLabel = [s \in StateIds |-> "None"]
  /\ stateForceLow = [s \in StateIds |-> FALSE]
  /\ stateBackend = [s \in StateIds |-> "LowLevelHook"]
  /\ statePressed = [s \in StateIds |-> FALSE]
  /\ stateToggleActive = [s \in StateIds |-> FALSE]
  /\ stateOwner = [s \in StateIds |-> 0]
  /\ acceptedPresses = [s \in StateIds |-> 0]
  /\ acceptedReleases = [s \in StateIds |-> 0]
  /\ sessionPhase = [s \in SessionIds |-> "Unused"]
  /\ sessionKind = [s \in SessionIds |-> "None"]
  /\ sessionState = [s \in SessionIds |-> 0]
  /\ createdSessions = {}
  /\ currentSession = 0
  /\ stopRequests = {}
  /\ stopResults = {}
  /\ rejectedConflicts = {}
  /\ systemConflicts = {}
  /\ duplicatePresses = {}
  /\ duplicateReleases = {}
  /\ toggleReleaseNoops = {}
  /\ lateReleases = {}
  /\ modeSwitches = {}

\* A new state replaces only the selected kind. A pressed old state emits a
\* delayed stop request carrying the old session owner.
Register(k, label) ==
  /\ k \in Kinds
  /\ label \in Labels
  /\ nextRegistration < MaxRegistrations
  /\ PlannedRegister(nextRegistration, k, label)
  /\ ~Conflicting(k, label)
  /\ LET new == nextRegistration + 1
         old == registrations[k]
         oldOwner == IF old = 0 THEN 0 ELSE stateOwner[old]
         oldPressed == old # 0 /\ statePressed[old]
         desired == DesiredBackend(mode, ForceLow(label))
         actual == ActualBackend(mode, label, ForceLow(label))
         request == [id |-> nextEvent + 1,
                     state |-> old,
                     session |-> oldOwner,
                     cause |-> "registration"]
     IN
       /\ nextRegistration' = new
       /\ registrations' = [registrations EXCEPT ![k] = new]
       /\ stateExists' = stateExists \cup {new}
       /\ stateKind' = [stateKind EXCEPT ![new] = k]
       /\ stateLabel' = [stateLabel EXCEPT ![new] = label]
       /\ stateForceLow' = [stateForceLow EXCEPT ![new] = ForceLow(label)]
       /\ stateBackend' = [stateBackend EXCEPT ![new] = actual]
       /\ statePressed' = [s \in StateIds |->
             IF s = new THEN FALSE
             ELSE IF oldPressed /\ s = old THEN FALSE ELSE statePressed[s]]
       /\ stateToggleActive' = [s \in StateIds |->
             IF s = new THEN FALSE
             ELSE IF oldPressed /\ s = old THEN FALSE ELSE stateToggleActive[s]]
       /\ stateOwner' = [s \in StateIds |->
             IF s = new THEN 0 ELSE stateOwner[s]]
       /\ acceptedPresses' = [s \in StateIds |->
             IF s = new THEN 0 ELSE acceptedPresses[s]]
       /\ acceptedReleases' = [s \in StateIds |->
             IF s = new THEN 0
             ELSE IF oldPressed /\ s = old
                  THEN acceptedReleases[s] + 1
                  ELSE acceptedReleases[s]]
       /\ stopRequests' =
             IF oldPressed /\ oldOwner # 0
             THEN stopRequests \cup {request}
             ELSE stopRequests
       /\ nextEvent' = nextEvent + IF oldPressed /\ oldOwner # 0 THEN 1 ELSE 0
       /\ systemConflicts' =
             IF desired = "RegisterHotKey" /\ label \in SystemOccupied
             THEN systemConflicts \cup {[kind |-> k, label |-> label]}
             ELSE systemConflicts
       /\ UNCHANGED <<mode, nextSession, eventsUsed,
                      sessionPhase, sessionKind, sessionState, createdSessions,
                      currentSession, stopResults, rejectedConflicts,
                      duplicatePresses, duplicateReleases, toggleReleaseNoops,
                      lateReleases, modeSwitches>>

RejectInternalConflict(k, label) ==
  /\ k \in Kinds
  /\ label \in Labels
  /\ k # EventKind
  /\ label = "F2"
  /\ Conflicting(k, label)
  /\ [kind |-> k, label |-> label] \notin rejectedConflicts
  /\ rejectedConflicts' = rejectedConflicts \cup
       {[kind |-> k, label |-> label]}
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, eventsUsed,
                 registrations, stateExists, stateKind, stateLabel,
                 stateForceLow, stateBackend, statePressed, stateToggleActive,
                 stateOwner, acceptedPresses, acceptedReleases, sessionPhase,
                 sessionKind, sessionState, createdSessions, currentSession,
                 stopRequests, stopResults, systemConflicts, duplicatePresses,
                 duplicateReleases, toggleReleaseNoops, lateReleases,
                 modeSwitches>>

\* RegisterHotKey probes a system reservation before attempting the standard
\* backend. The model records that diagnostic separately from the LLKH
\* fallback selected by ActualBackend.
ObserveSystemConflict(k) ==
  /\ k \in Kinds
  /\ registrations[k] # 0
  /\ mode = "Toggle"
  /\ LET s == registrations[k]
         conflict == [kind |-> k, label |-> stateLabel[s]]
     IN
       /\ stateLabel[s] \in SystemOccupied
       /\ DesiredBackend(mode, stateForceLow[s]) = "RegisterHotKey"
       /\ conflict \notin systemConflicts
       /\ systemConflicts' = systemConflicts \cup {conflict}
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, eventsUsed,
                 registrations, stateExists, stateKind, stateLabel,
                 stateForceLow, stateBackend, statePressed, stateToggleActive,
                 stateOwner, acceptedPresses, acceptedReleases, sessionPhase,
                 sessionKind, sessionState, createdSessions, currentSession,
                 stopRequests, stopResults, rejectedConflicts,
                 duplicatePresses, duplicateReleases, toggleReleaseNoops,
                 lateReleases, modeSwitches>>

\* Unregistering is a replacement with no new state. The old release remains
\* asynchronous because the audio stop is dispatched to another task.
Unregister(k) ==
  /\ k \in Kinds
  /\ registrations[k] # 0
  /\ LET old == registrations[k]
         oldOwner == stateOwner[old]
         oldPressed == statePressed[old]
         request == [id |-> nextEvent + 1,
                     state |-> old,
                     session |-> oldOwner,
                     cause |-> "unregister"]
     IN
       /\ registrations' = [registrations EXCEPT ![k] = 0]
       /\ statePressed' = [statePressed EXCEPT ![old] = FALSE]
       /\ stateToggleActive' = [stateToggleActive EXCEPT ![old] = FALSE]
       /\ acceptedReleases' = [acceptedReleases EXCEPT
             ![old] = acceptedReleases[old] + IF oldPressed THEN 1 ELSE 0]
       /\ stopRequests' =
             IF oldPressed /\ oldOwner # 0
             THEN stopRequests \cup {request}
             ELSE stopRequests
       /\ nextEvent' = nextEvent + IF oldPressed /\ oldOwner # 0 THEN 1 ELSE 0
       /\ UNCHANGED <<mode, nextRegistration, nextSession, eventsUsed,
                      stateExists, stateKind, stateLabel, stateForceLow,
                      stateBackend, stateOwner, acceptedPresses, sessionPhase,
                      sessionKind, sessionState, createdSessions, currentSession,
                      stopResults, rejectedConflicts, systemConflicts,
                      duplicatePresses, duplicateReleases, toggleReleaseNoops,
                      lateReleases, modeSwitches>>

\* One accepted press reserves a recording session. This abstracts the
\* recording lock in start_recording_inner before capture is spawned.
AcceptPress(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ registrations[k] # 0
  /\ eventsUsed < MaxEvents
  /\ nextSession < MaxSessions
  /\ currentSession = 0
  /\ LET s == registrations[k]
         session == nextSession + 1
     IN
       /\ statePressed[s] = FALSE
       /\ statePressed' = [statePressed EXCEPT ![s] = TRUE]
       /\ stateToggleActive' = [stateToggleActive EXCEPT
             ![s] = mode = "Toggle"]
       /\ stateOwner' = [stateOwner EXCEPT ![s] = session]
       /\ acceptedPresses' = [acceptedPresses EXCEPT
             ![s] = acceptedPresses[s] + 1]
       /\ nextSession' = session
       /\ sessionPhase' = [sessionPhase EXCEPT ![session] = "Starting"]
       /\ sessionKind' = [sessionKind EXCEPT ![session] = k]
       /\ sessionState' = [sessionState EXCEPT ![session] = s]
       /\ createdSessions' = createdSessions \cup {session}
       /\ currentSession' = session
       /\ eventsUsed' = eventsUsed + 1
       /\ UNCHANGED <<mode, nextRegistration, nextEvent, registrations,
                      stateExists, stateKind, stateLabel, stateForceLow,
                      stateBackend, acceptedReleases,
                      stopRequests, stopResults, rejectedConflicts,
                      systemConflicts, duplicatePresses, duplicateReleases,
                      toggleReleaseNoops, lateReleases, modeSwitches>>

\* Hold-mode duplicate key-downs are consumed by the compare_exchange gate.
DuplicateHoldPress(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ mode = "Hold"
  /\ registrations[k] # 0
  /\ LET s == registrations[k] IN
       /\ statePressed[s]
       /\ s \notin duplicatePresses
       /\ eventsUsed < MaxEvents
       /\ duplicatePresses' = duplicatePresses \cup {s}
       /\ eventsUsed' = eventsUsed + 1
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, registrations,
                 stateExists, stateKind, stateLabel, stateForceLow,
                 stateBackend, statePressed, stateToggleActive, stateOwner,
                 acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
                 sessionState, createdSessions, currentSession, stopRequests,
                 stopResults, rejectedConflicts, systemConflicts,
                 duplicateReleases, toggleReleaseNoops, lateReleases,
                 modeSwitches>>

HoldRelease(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ mode = "Hold"
  /\ registrations[k] # 0
  /\ LET s == registrations[k]
         owner == stateOwner[s]
         request == [id |-> nextEvent + 1,
                     state |-> s,
                     session |-> owner,
                     cause |-> "release"]
     IN
       /\ statePressed[s]
       /\ eventsUsed < MaxEvents
       /\ statePressed' = [statePressed EXCEPT ![s] = FALSE]
       /\ stateToggleActive' = [stateToggleActive EXCEPT ![s] = FALSE]
       /\ acceptedReleases' = [acceptedReleases EXCEPT
             ![s] = acceptedReleases[s] + 1]
       /\ stopRequests' =
             IF owner # 0 THEN stopRequests \cup {request} ELSE stopRequests
       /\ nextEvent' = nextEvent + IF owner # 0 THEN 1 ELSE 0
       /\ eventsUsed' = eventsUsed + 1
       /\ UNCHANGED <<mode, nextRegistration, nextSession, registrations,
                      stateExists, stateKind, stateLabel, stateForceLow,
                      stateBackend, stateOwner, acceptedPresses, sessionPhase,
                      sessionKind, sessionState, createdSessions, currentSession,
                      stopResults, rejectedConflicts, systemConflicts,
                      duplicatePresses, duplicateReleases, toggleReleaseNoops,
                      lateReleases, modeSwitches>>

\* A second toggle press is the intentional toggle-off event. Its stop
\* request still carries the session that the first press reserved.
ToggleSecondPress(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ mode = "Toggle"
  /\ registrations[k] # 0
  /\ LET s == registrations[k]
         owner == stateOwner[s]
         request == [id |-> nextEvent + 1,
                     state |-> s,
                     session |-> owner,
                     cause |-> "release"]
     IN
       /\ stateToggleActive[s]
       /\ eventsUsed < MaxEvents
       /\ statePressed' = [statePressed EXCEPT ![s] = FALSE]
       /\ stateToggleActive' = [stateToggleActive EXCEPT ![s] = FALSE]
       /\ acceptedReleases' = [acceptedReleases EXCEPT
             ![s] = acceptedReleases[s] + 1]
       /\ stopRequests' =
             IF owner # 0 THEN stopRequests \cup {request} ELSE stopRequests
       /\ nextEvent' = nextEvent + IF owner # 0 THEN 1 ELSE 0
       /\ eventsUsed' = eventsUsed + 1
       /\ UNCHANGED <<mode, nextRegistration, nextSession, registrations,
                      stateExists, stateKind, stateLabel, stateForceLow,
                      stateBackend, stateOwner, acceptedPresses, sessionPhase,
                      sessionKind, sessionState, createdSessions, currentSession,
                      stopResults, rejectedConflicts, systemConflicts,
                      duplicatePresses, duplicateReleases, toggleReleaseNoops,
                      lateReleases, modeSwitches>>

DuplicateRelease(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ mode = "Hold"
  /\ registrations[k] # 0
  /\ LET s == registrations[k] IN
       /\ ~statePressed[s]
       /\ s \notin duplicateReleases
       /\ eventsUsed < MaxEvents
       /\ duplicateReleases' = duplicateReleases \cup {s}
       /\ eventsUsed' = eventsUsed + 1
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, registrations,
                 stateExists, stateKind, stateLabel, stateForceLow,
                 stateBackend, statePressed, stateToggleActive, stateOwner,
                 acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
                 sessionState, createdSessions, currentSession, stopRequests,
                 stopResults, rejectedConflicts, systemConflicts,
                 duplicatePresses, toggleReleaseNoops, lateReleases,
                 modeSwitches>>

\* Release messages are ignored entirely in toggle mode.
ToggleReleaseNoOp(k) ==
  /\ k \in Kinds
  /\ k = EventKind
  /\ mode = "Toggle"
  /\ registrations[k] # 0
  /\ LET s == registrations[k] IN
       /\ s \notin toggleReleaseNoops
       /\ eventsUsed < MaxEvents
       /\ toggleReleaseNoops' = toggleReleaseNoops \cup {s}
       /\ eventsUsed' = eventsUsed + 1
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, registrations,
                 stateExists, stateKind, stateLabel, stateForceLow,
                 stateBackend, statePressed, stateToggleActive, stateOwner,
                 acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
                 sessionState, createdSessions, currentSession, stopRequests,
                 stopResults, rejectedConflicts, systemConflicts,
                 duplicatePresses, duplicateReleases, lateReleases,
                 modeSwitches>>

\* A queued callback from a state no longer installed in its kind can arrive
\* after re-registration. It reuses that state's original session owner.
LateRelease(s) ==
  /\ s \in stateExists
  /\ stateKind[s] = EventKind
  /\ s \notin CurrentStateIds
  /\ stateOwner[s] # 0
  /\ s \notin lateReleases
  /\ eventsUsed < MaxEvents
  /\ LET request == [id |-> nextEvent + 1,
                     state |-> s,
                     session |-> stateOwner[s],
                     cause |-> "late"]
     IN
       /\ lateReleases' = lateReleases \cup {s}
       /\ stopRequests' = stopRequests \cup {request}
       /\ nextEvent' = nextEvent + 1
       /\ eventsUsed' = eventsUsed + 1
  /\ UNCHANGED <<mode, nextRegistration, nextSession, registrations,
                 stateExists, stateKind, stateLabel, stateForceLow,
                 stateBackend, statePressed, stateToggleActive, stateOwner,
                 acceptedPresses, acceptedReleases, sessionPhase, sessionKind,
                 sessionState, createdSessions, currentSession, stopResults,
                 rejectedConflicts, systemConflicts, duplicatePresses,
                 duplicateReleases, toggleReleaseNoops, modeSwitches>>

\* The mode flag is changed before backend migration. Every installed state
\* is released and reclassified, including states whose backend label happens
\* to remain lowLevelHook.
SwitchMode(newMode) ==
  /\ newMode \in Modes
  /\ newMode # mode
  /\ Cardinality(modeSwitches) < MaxModeSwitches
  /\ LET currentStates == CurrentStateIds
         needsStop == currentSession # 0
         request == [id |-> nextEvent + 1,
                     state |-> 0,
                     session |-> currentSession,
                     cause |-> "mode"]
     IN
       /\ mode' = newMode
       /\ modeSwitches' = modeSwitches \cup
             {[from |-> mode, to |-> newMode]}
       /\ statePressed' = [s \in StateIds |->
             IF s \in currentStates THEN FALSE ELSE statePressed[s]]
       /\ stateToggleActive' = [s \in StateIds |->
             IF s \in currentStates THEN FALSE ELSE stateToggleActive[s]]
       /\ acceptedReleases' = [s \in StateIds |->
             IF s \in currentStates /\ statePressed[s]
             THEN acceptedReleases[s] + 1 ELSE acceptedReleases[s]]
       /\ stateBackend' = [s \in StateIds |->
             IF s \in currentStates
             THEN ActualBackend(newMode, stateLabel[s], stateForceLow[s])
             ELSE stateBackend[s]]
       /\ stopRequests' =
             IF needsStop THEN stopRequests \cup {request} ELSE stopRequests
       /\ nextEvent' = nextEvent + IF needsStop THEN 1 ELSE 0
       /\ UNCHANGED <<nextRegistration, nextSession, eventsUsed,
                      registrations, stateExists, stateKind, stateLabel,
                      stateForceLow, stateOwner, acceptedPresses, sessionPhase,
                      sessionKind, sessionState, createdSessions, currentSession,
                      stopResults, rejectedConflicts, systemConflicts,
                      duplicatePresses, duplicateReleases, toggleReleaseNoops,
                      lateReleases>>

\* A second stop path (for example the public stop command or the explicit
\* toggle-to-hold mode stop) may clear the current slot before a queued hotkey
\* stop task runs. This is the interleaving needed for the ownership check.
ManualStop ==
  /\ currentSession # 0
  /\ sessionPhase[currentSession] \in {"Starting", "Recording"}
  /\ LET s == currentSession IN
       /\ currentSession' = 0
       /\ sessionPhase' = [sessionPhase EXCEPT
             ![s] = IF sessionPhase[s] = "Starting" THEN "Finished"
                    ELSE "Processing"]
       /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent,
                      eventsUsed, registrations, stateExists, stateKind,
                      stateLabel, stateForceLow, stateBackend, statePressed,
                      stateToggleActive, stateOwner, acceptedPresses,
                      acceptedReleases, sessionKind, sessionState,
                      createdSessions, stopRequests, stopResults,
                      rejectedConflicts, systemConflicts, duplicatePresses,
                      duplicateReleases, toggleReleaseNoops, lateReleases,
                      modeSwitches>>

StartReady(s) ==
  /\ s \in createdSessions
  /\ sessionPhase[s] = "Starting"
  /\ currentSession = s
  /\ sessionPhase' = [sessionPhase EXCEPT ![s] = "Recording"]
  /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, eventsUsed,
                 registrations, stateExists, stateKind, stateLabel,
                 stateForceLow, stateBackend, statePressed, stateToggleActive,
                 stateOwner, acceptedPresses, acceptedReleases, sessionKind,
                 sessionState, createdSessions, currentSession, stopRequests,
                 stopResults, rejectedConflicts, systemConflicts,
                 duplicatePresses, duplicateReleases, toggleReleaseNoops,
                 lateReleases, modeSwitches>>

StartFailed(s) ==
  /\ s \in createdSessions
  /\ sessionPhase[s] = "Starting"
  /\ LET state == sessionState[s]
         resetGate == currentSession = s
     IN
       /\ sessionPhase' = [sessionPhase EXCEPT ![s] = "Finished"]
       /\ currentSession' = IF currentSession = s THEN 0 ELSE currentSession
       /\ statePressed' = [statePressed EXCEPT
             ![state] = IF resetGate THEN FALSE ELSE statePressed[state]]
       /\ stateToggleActive' = [stateToggleActive EXCEPT
             ![state] = IF resetGate THEN FALSE ELSE stateToggleActive[state]]
       /\ acceptedReleases' = [acceptedReleases EXCEPT
             ![state] = acceptedReleases[state] +
                        IF resetGate /\ statePressed[state] THEN 1 ELSE 0]
       /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent,
                      eventsUsed, registrations, stateExists, stateKind,
                      stateLabel, stateForceLow, stateBackend, stateOwner,
                      acceptedPresses, sessionKind, sessionState,
                      createdSessions, stopRequests, stopResults,
                      rejectedConflicts, systemConflicts, duplicatePresses,
                      duplicateReleases, toggleReleaseNoops, lateReleases,
                      modeSwitches>>

ProcessOneStop ==
  \E request \in stopRequests :
    LET before == currentSession
        applies == before = request.session
                    /\ before # 0
                    /\ sessionPhase[before] \in {"Starting", "Recording"}
        after == IF applies THEN 0 ELSE before
        result == [id |-> request.id,
                   requested |-> request.session,
                   before |-> before,
                   after |-> after,
                   cause |-> request.cause]
    IN
      /\ stopRequests' = stopRequests \ {request}
      /\ stopResults' = stopResults \cup {result}
      /\ currentSession' = after
      /\ IF applies
         THEN sessionPhase' = [sessionPhase EXCEPT
               ![before] = IF sessionPhase[before] = "Starting"
                           THEN "Finished" ELSE "Processing"]
         ELSE UNCHANGED sessionPhase
      /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent,
                     eventsUsed, registrations, stateExists, stateKind,
                     stateLabel, stateForceLow, stateBackend, statePressed,
                     stateToggleActive, stateOwner, acceptedPresses,
                     acceptedReleases, sessionKind, sessionState,
                     createdSessions, rejectedConflicts, systemConflicts,
                     duplicatePresses, duplicateReleases, toggleReleaseNoops,
                     lateReleases, modeSwitches>>

FinishOne ==
  \E s \in SessionIds :
    /\ sessionPhase[s] = "Processing"
    /\ sessionPhase' = [sessionPhase EXCEPT ![s] = "Finished"]
    /\ UNCHANGED <<mode, nextRegistration, nextSession, nextEvent, eventsUsed,
                   registrations, stateExists, stateKind, stateLabel,
                   stateForceLow, stateBackend, statePressed,
                   stateToggleActive, stateOwner, acceptedPresses,
                   acceptedReleases, sessionKind, sessionState,
                   createdSessions, currentSession, stopRequests, stopResults,
                   rejectedConflicts, systemConflicts, duplicatePresses,
                   duplicateReleases, toggleReleaseNoops, lateReleases,
                   modeSwitches>>

StartReadyAny == \E s \in SessionIds : StartReady(s)
StartFailedAny == \E s \in SessionIds : StartFailed(s)

Next ==
  \/ \E k \in Kinds : \E label \in Labels : Register(k, label)
  \/ \E k \in Kinds : \E label \in Labels : RejectInternalConflict(k, label)
  \/ \E k \in Kinds : ObserveSystemConflict(k)
  \/ \E k \in Kinds : Unregister(k)
  \/ \E k \in Kinds : AcceptPress(k)
  \/ \E k \in Kinds : DuplicateHoldPress(k)
  \/ \E k \in Kinds : HoldRelease(k)
  \/ \E k \in Kinds : ToggleSecondPress(k)
  \/ \E k \in Kinds : DuplicateRelease(k)
  \/ \E k \in Kinds : ToggleReleaseNoOp(k)
  \/ \E s \in StateIds : LateRelease(s)
  \/ \E newMode \in Modes : SwitchMode(newMode)
  \/ ManualStop
  \/ \E s \in SessionIds : StartReady(s) \/ StartFailed(s)
  \/ ProcessOneStop
  \/ FinishOne

Spec ==
  /\ Init
  /\ [][Next]_vars
  /\ WF_vars(ProcessOneStop)
  /\ WF_vars(StartReadyAny)
  /\ WF_vars(StartFailedAny)
  /\ WF_vars(FinishOne)

TypeOK ==
  /\ mode \in Modes
  /\ nextRegistration \in 0..MaxRegistrations
  /\ nextSession \in 0..MaxSessions
  /\ nextEvent \in Nat
  /\ eventsUsed \in 0..MaxEvents
  /\ registrations \in [Kinds -> 0..MaxRegistrations]
  /\ stateExists \subseteq StateIds
  /\ stateKind \in [StateIds -> (Kinds \cup {"None"})]
  /\ stateLabel \in [StateIds -> (Labels \cup {"None"})]
  /\ stateForceLow \in [StateIds -> BOOLEAN]
  /\ stateBackend \in [StateIds -> Backends]
  /\ statePressed \in [StateIds -> BOOLEAN]
  /\ stateToggleActive \in [StateIds -> BOOLEAN]
  /\ stateOwner \in [StateIds -> 0..MaxSessions]
  /\ acceptedPresses \in [StateIds -> 0..(MaxEvents + MaxRegistrations + MaxModeSwitches)]
  /\ acceptedReleases \in [StateIds -> 0..(MaxEvents + MaxRegistrations + MaxModeSwitches)]
  /\ sessionPhase \in [SessionIds -> Phases]
  /\ sessionKind \in [SessionIds -> (Kinds \cup {"None"})]
  /\ sessionState \in [SessionIds -> 0..MaxRegistrations]
  /\ createdSessions \subseteq SessionIds
  /\ currentSession \in 0..MaxSessions
  /\ stopRequests \subseteq
       [id : Nat, state : 0..MaxRegistrations,
        session : 0..MaxSessions, cause : StopCauses]
  /\ stopResults \subseteq
       [id : Nat, requested : 0..MaxSessions,
        before : 0..MaxSessions, after : 0..MaxSessions,
        cause : StopCauses]
  /\ rejectedConflicts \subseteq [kind : Kinds, label : Labels]
  /\ systemConflicts \subseteq [kind : Kinds, label : Labels]
  /\ duplicatePresses \subseteq StateIds
  /\ duplicateReleases \subseteq StateIds
  /\ toggleReleaseNoops \subseteq StateIds
  /\ lateReleases \subseteq StateIds
  /\ modeSwitches \subseteq [from : Modes, to : Modes]

RegistrationSlotsAreTyped ==
  \A k \in Kinds :
    registrations[k] = 0
    \/ (registrations[k] \in stateExists /\ stateKind[registrations[k]] = k)

RegisteredLabelsAreUnique ==
  \A k1, k2 \in Kinds :
    k1 # k2 /\ registrations[k1] # 0 /\ registrations[k2] # 0
      => CurrentLabel(k1) # CurrentLabel(k2)

BackendMatchesClassification ==
  \A k \in Kinds :
    registrations[k] # 0
      => stateBackend[registrations[k]] =
           ActualBackend(mode, stateLabel[registrations[k]],
                         stateForceLow[registrations[k]])

GateStateIsCoherent ==
  /\ \A s \in stateExists : stateToggleActive[s] => statePressed[s]
  /\ \A k \in Kinds :
       registrations[k] # 0 /\ mode = "Toggle"
         => stateToggleActive[registrations[k]] = statePressed[registrations[k]]
  /\ \A k \in Kinds :
       registrations[k] # 0 /\ mode = "Hold"
         => ~stateToggleActive[registrations[k]]

AcceptedEventsMatchGate ==
  \A s \in stateExists :
    statePressed[s] = (acceptedPresses[s] > acceptedReleases[s])

\* A stop request whose expected session is stale must leave a newer current
\* session untouched. Removing the `before = request.session` guard produces
\* a counterexample in the negative mutation run.
OldReleaseCannotStopNewRecording ==
  \A result \in stopResults :
    result.before # result.requested => result.after = result.before

NoStopRequestTargetsAnUnownedSession ==
  \A request \in stopRequests :
    request.session # 0

PendingStopRequestsDrain ==
  [] (stopRequests # {} => <> (stopRequests = {}))

PendingStartsSettle ==
  \A s \in SessionIds :
    [] ((s \in createdSessions /\ sessionPhase[s] = "Starting")
        => <> (sessionPhase[s] # "Starting"))

ProcessingSessionsFinish ==
  \A s \in SessionIds :
    [] ((s \in createdSessions /\ sessionPhase[s] = "Processing")
        => <> (sessionPhase[s] # "Processing"))

=============================================================================
