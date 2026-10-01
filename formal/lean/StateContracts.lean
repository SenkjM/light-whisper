import Std.Tactic

namespace LightWhisper

/- These are unbounded decision/state contracts. Implementation correspondence
   is recorded separately; the theorems do not translate Rust or TypeScript. -/

structure OwnedSlot (α : Type) where
  epoch : Nat
  value : Option α

def commit (s : OwnedSlot α) (observed : Nat) (v : α) : OwnedSlot α :=
  if observed = s.epoch then { s with value := some v } else s

def logout (s : OwnedSlot α) : OwnedSlot α :=
  { epoch := s.epoch + 1, value := none }

theorem staleCommitIsInert (s : OwnedSlot α) (n : Nat) (v : α)
    (h : n ≠ s.epoch) : commit s n v = s := by
  simp [commit, h]

theorem logoutRejectsEarlierCompletion (s : OwnedSlot α) (n : Nat) (v : α)
    (h : n ≤ s.epoch) : commit (logout s) n v = logout s := by
  apply staleCommitIsInert
  simp only [logout]
  omega

def restoreClipboard (current written snapshot : Nat) : Nat :=
  if current = written then snapshot else current

theorem externalClipboardIsPreserved (current written snapshot : Nat)
    (h : current ≠ written) : restoreClipboard current written snapshot = current := by
  simp [restoreClipboard, h]

def collectAudio (references leases : Nat) : Bool :=
  decide (references = 0 ∧ leases = 0)

theorem leasedAudioIsRetained (references leases : Nat) (h : leases > 0) :
    collectAudio references leases = false := by
  simp [collectAudio]
  omega

theorem referencedAudioIsRetained (references leases : Nat) (h : references > 0) :
    collectAudio references leases = false := by
  simp [collectAudio]
  omega

def appendSamples (cap used incoming : Nat) : Nat := min cap (used + incoming)

theorem samplesStayBounded (cap used incoming : Nat) :
    appendSamples cap used incoming ≤ cap := by
  exact Nat.min_le_left _ _

inductive CaptureOwner where
  | waiting | native | fallback | stopped
  deriving DecidableEq

def nativeMayWrite (owner : CaptureOwner) : Bool := decide (owner = .native)

theorem lateNativeCannotWriteIntoFallback : nativeMayWrite .fallback = false := by
  decide

def saveAudioAllowed (history audio : Bool) : Bool := history && audio

theorem savingAudioRequiresConsent (history audio : Bool)
    (h : saveAudioAllowed history audio = true) : history = true ∧ audio = true := by
  cases history <;> cases audio <;> simp_all [saveAudioAllowed]

inductive ProcessingMode where
  | off | on | auto
  deriving DecidableEq

def executeProcessing (mode : ProcessingMode) (explicit : Bool)
    (autoDecision : Option Bool) : Bool :=
  if explicit then true else
    match mode with
    | .off => false
    | .on => true
    | .auto => autoDecision.getD true

theorem explicitProcessingAlwaysExecutes (mode : ProcessingMode) (decision : Option Bool) :
    executeProcessing mode true decision = true := by
  simp [executeProcessing]

theorem uncertainAutoFailsOpen : executeProcessing .auto false none = true := by
  rfl

def skipPolish (original : String) : String := original

theorem skippedPolishPreservesExactText (original : String) : skipPolish original = original := by
  rfl

inductive CorrectionSource where
  | user | ai
  deriving DecidableEq

def keepCorrection (source : CorrectionSource) (reviewRejects : Bool) : Bool :=
  decide (source = .user) || !reviewRejects

theorem userCorrectionSurvivesReview (rejects : Bool) : keepCorrection .user rejects = true := by
  simp [keepCorrection]

def replacementAllowed (version source window : Bool) : Bool := version && source && window

theorem replacementRequiresAllThreeMatches (version source window : Bool)
    (h : replacementAllowed version source window = true) :
    version = true ∧ source = true ∧ window = true := by
  cases version <;> cases source <;> cases window <;> simp_all [replacementAllowed]

def acceptPress (alreadyPressed : Bool) : Bool := !alreadyPressed

theorem repeatedPressIsIgnored : acceptPress true = false := by rfl

structure ProfileVersions where
  current : Nat
  pending : Option Nat
  disk : Nat

def updateProfile (s : ProfileVersions) : ProfileVersions :=
  { s with current := s.current + 1, pending := some (s.current + 1) }

def publishProfile (s : ProfileVersions) : ProfileVersions :=
  match s.pending with
  | some version => if version = s.current then
      { s with pending := none, disk := version } else s
  | none => s

def ValidProfile (s : ProfileVersions) : Prop :=
  s.disk ≤ s.current ∧ ∀ version, s.pending = some version → version = s.current

inductive ProfileReachable : ProfileVersions → Prop where
  | initial : ProfileReachable ⟨0, none, 0⟩
  | update (s) : ProfileReachable s → ProfileReachable (updateProfile s)
  | publish (s) : ProfileReachable s → ProfileReachable (publishProfile s)

theorem updatePreservesProfile (s : ProfileVersions) (h : ValidProfile s) :
    ValidProfile (updateProfile s) := by
  simp only [ValidProfile, updateProfile, Option.some.injEq] at *
  constructor
  · omega
  · intro version hv
    omega

theorem publishPreservesProfile (s : ProfileVersions) (h : ValidProfile s) :
    ValidProfile (publishProfile s) := by
  cases hp : s.pending with
  | none => simpa [publishProfile, hp] using h
  | some version =>
    have hv := h.2 version hp
    simp [publishProfile, hp, hv, ValidProfile]

theorem reachableProfilesHaveNoStalePending (s : ProfileVersions)
    (reachable : ProfileReachable s) : ValidProfile s := by
  induction reachable with
  | initial => simp [ValidProfile]
  | update s _ ih => exact updatePreservesProfile s ih
  | publish s _ ih => exact publishPreservesProfile s ih

inductive UrlScheme where
  | http | https | file | javascript | other
  deriving DecidableEq

def sourceSchemeAllowed (scheme : UrlScheme) : Bool :=
  decide (scheme = .http ∨ scheme = .https)

theorem executableSourceSchemeIsRejected : sourceSchemeAllowed .javascript = false := by decide

theorem fileSourceSchemeIsRejected : sourceSchemeAllowed .file = false := by decide

inductive ThemeMode where
  | light | dark | system
  deriving DecidableEq

def darkTheme (mode : ThemeMode) (system : Bool) : Bool :=
  if mode = .system then system else decide (mode = .dark)

theorem explicitLightIgnoresSystem (system : Bool) : darkTheme .light system = false := by
  simp [darkTheme]

theorem explicitDarkIgnoresSystem (system : Bool) : darkTheme .dark system = true := by
  simp [darkTheme]

theorem systemThemeTracksSystem (system : Bool) : darkTheme .system system = system := by
  simp [darkTheme]

def firstApplicable : List Bool → Option Nat
  | [] => none
  | true :: _ => some 0
  | false :: rest => (firstApplicable rest).map Nat.succ

theorem firstApplicableIsMatch (xs : List Bool) (i : Nat)
    (h : firstApplicable xs = some i) : xs[i]? = some true := by
  induction xs generalizing i with
  | nil => simp [firstApplicable] at h
  | cons b xs ih =>
    cases b with
    | true =>
      simp [firstApplicable] at h
      subst i
      rfl
    | false =>
      cases hp : firstApplicable xs with
      | none => simp [firstApplicable, hp] at h
      | some j =>
        have hi : j + 1 = i := by simpa [firstApplicable, hp] using h
        subst i
        simpa using ih j hp

theorem earlierRulesDoNotMatch (xs : List Bool) (i j : Nat)
    (h : firstApplicable xs = some i) (before : j < i) : xs[j]? = some false := by
  induction xs generalizing i j with
  | nil => simp [firstApplicable] at h
  | cons b xs ih =>
    cases b with
    | true =>
      simp [firstApplicable] at h
      omega
    | false =>
      cases hp : firstApplicable xs with
      | none => simp [firstApplicable, hp] at h
      | some k =>
        have hi : k + 1 = i := by simpa [firstApplicable, hp] using h
        subst i
        cases j with
        | zero => rfl
        | succ j =>
          have before' : j < k := by omega
          simpa using ih k j hp before'

def gpuMayUnload (timeout age : Nat) (busy streaming : Bool) : Bool :=
  decide (0 < timeout ∧ timeout ≤ age) && !busy && !streaming

theorem busyGpuNeverUnloads (timeout age : Nat) (streaming : Bool) :
    gpuMayUnload timeout age true streaming = false := by
  simp [gpuMayUnload]

theorem streamingGpuNeverUnloads (timeout age : Nat) (busy : Bool) :
    gpuMayUnload timeout age busy true = false := by
  simp [gpuMayUnload]

theorem disabledGpuNeverUnloads (age : Nat) (busy streaming : Bool) :
    gpuMayUnload 0 age busy streaming = false := by
  simp [gpuMayUnload]

theorem idleWindowWaitsForTimeout (timeout age : Nat) (h : age < timeout) :
    gpuMayUnload timeout age false false = false := by
  simp [gpuMayUnload, Nat.not_le.mpr h]

def applyGpuIdleRead (edited : Bool) (current snapshot : Nat) : Nat :=
  if edited then current else snapshot

theorem editedGpuIdleReadIsInert (current snapshot : Nat) :
    applyGpuIdleRead true current snapshot = current := by
  simp [applyGpuIdleRead]

end LightWhisper
