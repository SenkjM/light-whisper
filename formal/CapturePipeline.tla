--------------------------- MODULE CapturePipeline ---------------------------
EXTENDS Naturals, Sequences

CONSTANT MaxSessions, MaxSamples, PrimeSamples

ASSUME MaxSessions \in Nat \ {0}
ASSUME MaxSamples \in Nat \ {0}
ASSUME PrimeSamples \in Nat \ {0}

CapturePhases == {"Idle", "Starting", "Recording", "Stopped", "Failed"}
CPALPhases == {"None", "Starting", "Active"}
WorkerPhases == {"None", "Starting", "Ready", "Accepted"}
NativeOutcomes == {
  "None", "Ready", "Unavailable", "TimedOut", "Disconnected",
  "AcceptanceFailed", "Skipped"
}
SelectedDevices == {"None", "Preferred", "Missing"}
ResolvedDevices == {"Preferred Microphone", "Default Microphone"}
Sources == {"prime", "native", "cpal"}
MonitorPhases == {"Off", "Starting", "Active"}
MeterValues == {0, 500, 1000}
FallbackOutcomes == {"Unavailable", "TimedOut", "Disconnected", "AcceptanceFailed", "Skipped"}

MinNat(a, b) == IF a < b THEN a ELSE b

Contains(sequence, value) == \E i \in 1..Len(sequence): sequence[i] = value

ResolveDevice(choice) ==
  IF choice = "Preferred" THEN "Preferred Microphone" ELSE "Default Microphone"

PrimeSequence(n) == [i \in 1..n |-> "prime"]

AppendSamples(existing, source, requested) ==
  LET room == MaxSamples - Len(existing)
      amount == MinNat(room, requested)
  IN existing \o [i \in 1..amount |-> source]

VARIABLES
  sessionCounter, session, capturePhase, sessionDeviceChoice,
  nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
  nativeDevice, acceptedSession, handoffCount, priming, samples,
  cpalPhase, cpalDevice, lateWorkers, latePrimed,
  selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
  monitorMeter

vars == <<
  sessionCounter, session, capturePhase, sessionDeviceChoice,
  nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
  nativeDevice, acceptedSession, handoffCount, priming, samples,
  cpalPhase, cpalDevice, lateWorkers, latePrimed,
  selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
  monitorMeter
>>

Init ==
  /\ sessionCounter = 0
  /\ session = 0
  /\ capturePhase = "Idle"
  /\ sessionDeviceChoice = "None"
  /\ nativeDisabled = FALSE
  /\ nativeWorkerSession = 0
  /\ nativeWorkerPhase = "None"
  /\ nativeOutcome = "None"
  /\ nativeDevice = "None"
  /\ acceptedSession = 0
  /\ handoffCount = 0
  /\ priming = <<>>
  /\ samples = <<>>
  /\ cpalPhase = "None"
  /\ cpalDevice = "None"
  /\ lateWorkers = {}
  /\ latePrimed = {}
  /\ selectedDevice = "None"
  /\ monitorPhase = "Off"
  /\ monitorDeviceChoice = "None"
  /\ monitorDevice = "None"
  /\ monitorMeter = 0

\* set_input_device stores the requested name. Resolution happens at each
\* native/CPAL/monitor start, so a missing preferred device falls back to the
\* current default input device.
SetSelectedDevice(choice) ==
  /\ choice \in SelectedDevices
  /\ selectedDevice' = choice
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       monitorPhase, monitorDeviceChoice, monitorDevice, monitorMeter
     >>

\* start_recording_inner stops any active microphone monitor before reserving
\* the session and snapshots the selected device for the capture worker.
BeginRecording ==
  /\ capturePhase \in {"Idle", "Stopped", "Failed"}
  /\ sessionCounter < MaxSessions
  /\ session' = sessionCounter + 1
  /\ sessionCounter' = sessionCounter + 1
  /\ capturePhase' = "Starting"
  /\ sessionDeviceChoice' = selectedDevice
  /\ nativeDisabled' = nativeDisabled
  /\ IF nativeDisabled
        THEN /\ nativeWorkerSession' = 0
             /\ nativeWorkerPhase' = "None"
             /\ nativeOutcome' = "Skipped"
             /\ nativeDevice' = "None"
        ELSE /\ nativeWorkerSession' = sessionCounter + 1
             /\ nativeWorkerPhase' = "Starting"
             /\ nativeOutcome' = "None"
             /\ nativeDevice' = ResolveDevice(selectedDevice)
  /\ acceptedSession' = 0
  /\ handoffCount' = 0
  /\ priming' = <<>>
  /\ samples' = <<>>
  /\ cpalPhase' = "None"
  /\ cpalDevice' = "None"
  /\ lateWorkers' = lateWorkers
  /\ latePrimed' = latePrimed
  /\ selectedDevice' = selectedDevice
  /\ monitorPhase' = "Off"
  /\ monitorDeviceChoice' = monitorDeviceChoice
  /\ monitorDevice' = "None"
  /\ monitorMeter' = 0

\* WindowsSpeechCapture::prime has received audio and the worker reports a
\* successful startup. It still cannot write into the shared buffer until the
\* watchdog path accepts this worker.
NativeReady ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Starting"
  /\ nativeDisabled = FALSE
  /\ nativeWorkerPhase' = "Ready"
  /\ nativeOutcome' = "Ready"
  /\ priming' = PrimeSequence(PrimeSamples)
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeDevice, acceptedSession,
       handoffCount, samples, cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

NativeUnavailable ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Starting"
  /\ nativeDisabled = FALSE
  /\ nativeWorkerSession' = 0
  /\ nativeWorkerPhase' = "None"
  /\ nativeOutcome' = "Unavailable"
  /\ nativeDevice' = "None"
  /\ priming' = <<>>
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, acceptedSession, handoffCount, samples,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

NativeDisconnected ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Starting"
  /\ nativeDisabled = FALSE
  /\ nativeWorkerSession' = 0
  /\ nativeWorkerPhase' = "None"
  /\ nativeOutcome' = "Disconnected"
  /\ nativeDevice' = "None"
  /\ priming' = <<>>
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, acceptedSession, handoffCount, samples,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* The two-second watchdog returns CPAL control without joining a worker that
\* may still be blocked in WASAPI/COM. Closing accept_tx makes the worker's late
\* handoff a no-op; the process-local circuit breaker skips native on later
\* recordings.
NativeTimedOut ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Starting"
  /\ nativeDisabled = FALSE
  /\ nativeDisabled' = TRUE
  /\ nativeWorkerSession' = 0
  /\ nativeWorkerPhase' = "None"
  /\ nativeOutcome' = "TimedOut"
  /\ nativeDevice' = "None"
  /\ acceptedSession' = 0
  /\ handoffCount' = 0
  /\ priming' = <<>>
  /\ lateWorkers' = lateWorkers \cup {session}
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       samples, cpalPhase, cpalDevice, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* A ready worker can still lose the acceptance send if its thread exits in
\* the handoff window. This is a fallback edge distinct from startup failure.
NativeAcceptanceFailed ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Ready"
  /\ nativeOutcome = "Ready"
  /\ nativeWorkerSession' = 0
  /\ nativeWorkerPhase' = "None"
  /\ nativeOutcome' = "AcceptanceFailed"
  /\ nativeDevice' = "None"
  /\ priming' = <<>>
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, acceptedSession, handoffCount, samples,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* The true acceptance send performs the one and only priming transfer before
\* the native worker enters its live capture loop.
AcceptNative ==
  /\ capturePhase = "Starting"
  /\ nativeWorkerSession = session
  /\ nativeWorkerPhase = "Ready"
  /\ nativeOutcome = "Ready"
  /\ handoffCount = 0
  /\ nativeWorkerPhase' = "Accepted"
  /\ capturePhase' = "Recording"
  /\ acceptedSession' = session
  /\ handoffCount' = handoffCount + 1
  /\ samples' = AppendSamples(samples, "prime", Len(priming))
  /\ priming' = <<>>
  /\ UNCHANGED <<
       sessionCounter, session, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeOutcome, nativeDevice,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* CPAL is used after native startup error, timeout, disconnect, acceptance
\* loss, or a process-local native circuit break.
StartCPAL ==
  /\ capturePhase = "Starting"
  /\ nativeOutcome \in FallbackOutcomes
  /\ cpalDevice' = ResolveDevice(sessionDeviceChoice)
  /\ cpalPhase' = "Starting"
  /\ capturePhase' = capturePhase
  /\ acceptedSession' = 0
  /\ priming' = <<>>
  /\ UNCHANGED <<
       sessionCounter, session, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, handoffCount, samples, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

CPALReady ==
  /\ capturePhase = "Starting"
  /\ cpalPhase = "Starting"
  /\ nativeOutcome \in FallbackOutcomes
  /\ cpalPhase' = "Active"
  /\ capturePhase' = "Recording"
  /\ UNCHANGED <<
       sessionCounter, session, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
       cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* CPAL device/config/stream creation or its outer startup watchdog can fail.
\* The recording start then publishes an error and leaves no active stream.
CPALFailure ==
  /\ capturePhase = "Starting"
  /\ cpalPhase = "Starting"
  /\ nativeOutcome \in FallbackOutcomes
  /\ cpalPhase' = "None"
  /\ cpalDevice' = "None"
  /\ capturePhase' = "Failed"
  /\ acceptedSession' = 0
  /\ UNCHANGED <<
       sessionCounter, session, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, handoffCount, priming, samples, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

NativeSamples ==
  \E count \in 1..PrimeSamples:
    /\ capturePhase = "Recording"
    /\ nativeWorkerPhase = "Accepted"
    /\ acceptedSession = session
    /\ cpalPhase = "None"
    /\ samples' = AppendSamples(samples, "native", count)
    /\ UNCHANGED <<
         sessionCounter, session, capturePhase, sessionDeviceChoice,
         nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
         nativeDevice, acceptedSession, handoffCount, priming,
          cpalPhase, cpalDevice, lateWorkers, latePrimed,
         selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
         monitorMeter
       >>

CPALSamples ==
  \E count \in 1..PrimeSamples:
    /\ capturePhase = "Recording"
    /\ cpalPhase = "Active"
    /\ samples' = AppendSamples(samples, "cpal", count)
    /\ UNCHANGED <<
         sessionCounter, session, capturePhase, sessionDeviceChoice,
         nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
         nativeDevice, acceptedSession, handoffCount, priming,
          cpalPhase, cpalDevice, lateWorkers, latePrimed,
         selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
         monitorMeter
       >>

\* A detached native worker may finish after CPAL has become the active stream.
\* These actions model its late prime/read and return without touching samples.
LateNativePrime(worker) ==
  /\ worker \in lateWorkers
  /\ worker \notin latePrimed
  /\ latePrimed' = latePrimed \cup {worker}
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
        cpalPhase, cpalDevice, lateWorkers,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

LateNativeReturn(worker) ==
  /\ worker \in lateWorkers
  /\ lateWorkers' = lateWorkers \ {worker}
  /\ latePrimed' = latePrimed \ {worker}
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
        cpalPhase, cpalDevice,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

StopRecording ==
  /\ capturePhase = "Recording"
  /\ capturePhase' = "Stopped"
  /\ cpalPhase' = "None"
  /\ nativeWorkerSession' = 0
  /\ nativeWorkerPhase' = "None"
  /\ UNCHANGED <<
       sessionCounter, session, sessionDeviceChoice,
       nativeDisabled, nativeOutcome, nativeDevice, acceptedSession,
       handoffCount, priming, samples, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice,
       monitorMeter
     >>

\* Microphone monitoring uses the same device-resolution rule but has its own
\* stream and bounded level publication. Starting a recording stops it first.
BeginMonitor ==
  /\ monitorPhase = "Off"
  /\ capturePhase \in {"Idle", "Stopped"}
  /\ monitorPhase' = "Starting"
  /\ monitorDeviceChoice' = selectedDevice
  /\ monitorDevice' = ResolveDevice(selectedDevice)
  /\ monitorMeter' = 0
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
        cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice
      >>

RestartMonitor ==
  /\ monitorPhase = "Active"
  /\ capturePhase \in {"Idle", "Stopped", "Failed"}
  /\ monitorPhase' = "Starting"
  /\ monitorDeviceChoice' = selectedDevice
  /\ monitorDevice' = ResolveDevice(selectedDevice)
  /\ monitorMeter' = 0
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
       cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice
     >>

MonitorReady ==
  /\ monitorPhase = "Starting"
  /\ monitorPhase' = "Active"
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
        cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorDeviceChoice, monitorDevice, monitorMeter
     >>

MonitorEmit ==
  \E level \in MeterValues:
    /\ monitorPhase = "Active"
    /\ monitorMeter' = level
    /\ UNCHANGED <<
         sessionCounter, session, capturePhase, sessionDeviceChoice,
         nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
         nativeDevice, acceptedSession, handoffCount, priming, samples,
          cpalPhase, cpalDevice, lateWorkers, latePrimed,
         selectedDevice, monitorPhase, monitorDeviceChoice, monitorDevice
       >>

StopMonitor ==
  /\ monitorPhase \in {"Starting", "Active"}
  /\ monitorPhase' = "Off"
  /\ monitorDevice' = "None"
  /\ monitorMeter' = 0
  /\ UNCHANGED <<
       sessionCounter, session, capturePhase, sessionDeviceChoice,
       nativeDisabled, nativeWorkerSession, nativeWorkerPhase, nativeOutcome,
       nativeDevice, acceptedSession, handoffCount, priming, samples,
        cpalPhase, cpalDevice, lateWorkers, latePrimed,
       selectedDevice, monitorDeviceChoice
     >>

Next ==
  \/ \E choice \in SelectedDevices: SetSelectedDevice(choice)
  \/ BeginRecording
  \/ NativeReady
  \/ NativeUnavailable
  \/ NativeDisconnected
  \/ NativeTimedOut
  \/ NativeAcceptanceFailed
  \/ AcceptNative
  \/ StartCPAL
  \/ CPALReady
  \/ CPALFailure
  \/ NativeSamples
  \/ CPALSamples
  \/ \E worker \in 1..MaxSessions: LateNativePrime(worker) \/ LateNativeReturn(worker)
  \/ StopRecording
  \/ BeginMonitor
  \/ RestartMonitor
  \/ MonitorReady
  \/ MonitorEmit
  \/ StopMonitor

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ sessionCounter \in 0..MaxSessions
  /\ session \in 0..MaxSessions
  /\ capturePhase \in CapturePhases
  /\ sessionDeviceChoice \in SelectedDevices
  /\ nativeDisabled \in BOOLEAN
  /\ nativeWorkerSession \in 0..MaxSessions
  /\ nativeWorkerPhase \in WorkerPhases
  /\ nativeOutcome \in NativeOutcomes
  /\ nativeDevice \in ResolvedDevices \cup {"None"}
  /\ acceptedSession \in 0..MaxSessions
  /\ handoffCount \in 0..1
  /\ priming \in Seq({"prime"})
  /\ samples \in Seq(Sources)
  /\ cpalPhase \in CPALPhases
  /\ cpalDevice \in ResolvedDevices \cup {"None"}
  /\ lateWorkers \subseteq 1..MaxSessions
  /\ latePrimed \subseteq lateWorkers
  /\ selectedDevice \in SelectedDevices
  /\ monitorPhase \in MonitorPhases
  /\ monitorDeviceChoice \in SelectedDevices
  /\ monitorDevice \in ResolvedDevices \cup {"None"}
  /\ monitorMeter \in 0..1000

WorkerIdentity ==
  /\ (nativeWorkerPhase = "None") = (nativeWorkerSession = 0)
  /\ nativeWorkerSession # 0 => nativeWorkerSession = session

RecordingOwnsMonitor ==
  capturePhase \in {"Starting", "Recording"} => monitorPhase = "Off"

NativeDeviceIsSnapshotted ==
  nativeWorkerPhase \in {"Starting", "Ready", "Accepted"}
    => nativeDevice = ResolveDevice(sessionDeviceChoice)

CPALDeviceIsResolved ==
  cpalPhase # "None" => cpalDevice = ResolveDevice(sessionDeviceChoice)

MonitorDeviceIsResolved ==
  /\ monitorPhase # "Off" => monitorDevice = ResolveDevice(monitorDeviceChoice)
  /\ monitorPhase = "Off" => monitorDevice = "None"

FallbackOwnsBuffer ==
  cpalPhase = "Active" => /\ acceptedSession = 0
                /\ nativeWorkerPhase = "None"
                /\ nativeOutcome \in FallbackOutcomes

HandoffIsAcceptedOnce ==
  /\ handoffCount \in 0..1
  /\ handoffCount = 1 =>
       /\ acceptedSession = session
       /\ nativeWorkerPhase \in {"Accepted", "None"}
       /\ priming = <<>>

NativeSamplesRequireAcceptedWorker ==
  (Contains(samples, "prime") \/ Contains(samples, "native"))
    => /\ acceptedSession = session
       /\ handoffCount = 1

PrimingPrecedesLiveNative ==
  \A i, j \in 1..Len(samples):
    /\ samples[i] = "native"
    /\ samples[j] = "prime"
    => j < i

SampleCapNeverExceeded == Len(samples) <= MaxSamples

LateWorkersCannotBeAccepted ==
  \A worker \in lateWorkers: worker # acceptedSession

TimeoutDetachesNative ==
  nativeOutcome = "TimedOut"
    => /\ nativeDisabled
       /\ nativeWorkerPhase = "None"
       /\ nativeWorkerSession = 0

=============================================================================
