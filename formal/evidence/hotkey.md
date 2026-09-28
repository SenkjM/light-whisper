# Hotkey contract evidence

The initial formal-only slice found a source/model correspondence gap: queued
start/release and gate replacement did not carry an explicit session owner.
That was INTERFACE_PENDING at baseline, not a production proof. The delivery
now closes that interface with HotkeyStartOwner and the recording-lock bind.

## Final models and bounds

HotkeyLifecycle runs separate Dictation, Translation and Assistant event-kind
configurations (two registrations/sessions, four events, one mode switch), plus
a four-registration/one-session/zero-event registration/conflict workload.
It checks role/label uniqueness, RegisterHotKey/low-level-hook classification,
occupied-shortcut fallback, duplicate hold events, toggle behavior, retirement,
mode release and delayed session-owned stop requests. Planned registration
permutations are representative finite sequences, not all physical key labels.

HotkeyPendingStart separately explores release before bind and obsolete failure
completion. Positive counts and hashes come from results.json. Negative controls
remove the exact-session stop guard and pending-intent guard; they must violate
OldReleaseCannotStopNewRecording and ReleasedOrSupersededStartCannotBind.

HotkeyRegistrationEpoch explores two queued native/dispatch events, three epochs,
one same-backend mode change and one shortcut replacement. Its positive run has
118 states; mode-gate reuse and native-ID reuse independently violate
NoStaleDispatchPress and NoStaleNativePress in the required negative controls.

HotkeyRegistrationFailure checks successful setup, lifecycle failure, fresh
restoration and restoration failure. Stable slots never contain a retired gate;
every native registration is current or owned by queued exact-ID cleanup. Native
cleanup is deferred and drains under backend scheduling fairness. Restoration
can use the hook fallback or register natively before a later failure. Three
negative controls independently expose retired-gate restore, orphan native IDs
and suppressed lifecycle errors.

## Production correspondence

- UnifiedHookState owns an Arc gate and per-gate HotkeyStartOwner.
- Accepted press begins the intent synchronously before spawning startup.
- start_recording_with_start_owner binds the intent while holding recording's
  mutex. Release either invalidates pending intent or carries that exact SID to
  stop_recording_inner, whose existing SID+trigger check remains required.
- Old registration retirement rejects queued presses. Async start failure keeps
  the original gate Arc, and a transition mutex orders owner/flag reset with
  press/release. Mode switches retire and replace gates even without backend
  migration. A configuration mutex serializes dispatch with mode publication
  and registration changes; native callbacks only queue immutable Arc events.
- Each native registration owns a fresh Windows application ID. IDs are never
  reused during the process (including backend-thread restart). Retirement
  unregisters the old state's exact ID. Exhaustion fails closed and selects
  the existing low-level-hook fallback; obsolete register commands are rejected.
- Lifecycle failure cleans every affected new registration before rebuilding
  previous settings with fresh gates/IDs. If restoration also fails, the affected
  slots are empty and the command reports both failures. Mode rollback restores
  the previous mode before rebuilding. Native-success and mode-change paths
  propagate lifecycle errors; two static source regressions reproduced the old
  retired-Arc restore and swallowed-error paths. Native failure injection remains
  a platform assumption; these source checks are not native execution evidence.
- Five frozen owner tests compiled against legacy-semantics scaffolding:
  one passed, four intended assertions failed. All five pass after implementation.
  This scaffold RED is not a runtime reproduction of the original Wry path.
- The final native-ID regression compiled against the actual fixed-ID function
  and failed with both IDs equal to 1; it passes with the shared allocator. The
  same-backend regression is explicitly a static source correspondence check,
  not a simulated native keyboard event. It failed before the conditional-only
  replacement was removed. The exhaustion test checks the 0xBFFF boundary.

Windows hook details, AltGr/injected-event filtering, native timing/clock and
physical delivery remain platform assumptions with existing implementation tests.
The dispatch channel is ordered; queued events retain their registration Arc.
This is reviewed correspondence plus tests, not Rust compiler refinement.

Final independent read-only review accepted the repaired registration, mode,
fallback and rollback call paths. Root's final Rust suite and Clippy pass; no
Windows failure injection or live physical-key test is claimed.

The ID range and thread-queue behavior follow Microsoft's
[RegisterHotKey contract](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registerhotkey).
