# OAuth session coordinator test slice

## Scope

Owned test files:

- `src-tauri/src/state/oauth_session_tests.rs`
- `formal/evidence/oauth-tests.md`

The tests use only the frozen `OAuthSessionState` API and non-sensitive string
sessions. No production implementation was changed in this slice.

Covered requirements:

- default/restore and refresh snapshots are absent while a login is pending;
- dropping the current login operation exposes the prior session for refresh,
  while dropping an old login does not clear a newer pending login;
- logout before a late login commit prevents resurrection and skips persistence;
- logout before a late refresh commit prevents resurrection and skips
  persistence;
- a newer login wins over an older login;
- a stale fatal refresh cannot invalidate a newer account;
- a current refresh commit persists and updates memory;
- a current token-scoped invalidate invokes clear and clears memory;
- failed persistence leaves the current in-memory session intact;
- logout clears memory and invalidates pending operations even when deletion fails;
- barrier-controlled commit/logout storage callbacks are serialized, with
  `inner.try_lock()` ownership probes inside both callbacks;
- the second refresh future remains `Pending` until the first guard releases;
- holding the refresh guard does not block session-state access.

The file also contains an ignored, test-only abstraction of the audited legacy
unversioned late-login publication. It is explicitly not an invocation of the
original service runtime; its isolated RED run records the old contract failure.

## Evidence

- Source baseline: `d29cd45db8876bd7bcfa915ae642af3b0fcc4bbe`.
- Test file Git object hash: `495dc01a55edf4e50757d2db8e5f4dbb2e62fa31`.
- Test file SHA-256: `84C190AD4C63D70AB3490FA46C96E2B88E8552582492C79303F5842D2896AFCA`.
- Narrow command:

  ```text
  cargo test --manifest-path src-tauri/Cargo.toml --lib state::oauth_session --locked
  ```

- Compile/collection: PASS. Cargo finished the test build and collected 15
  tests (`running 15 tests`), including one ignored legacy test. Only the
  scaffold's dead-code warnings appeared.
- Runtime result: RED, classified `INTERFACE_PENDING`. The declaration-only
  scaffold in `src-tauri/src/state/oauth_session.rs` still contains `todo!()`
  bodies, so the 14 ordinary tests panic before exercising their assertions;
  the legacy abstraction remains ignored in this ordinary run.

Exact failure evidence from the command:

```text
running 15 tests
test state::oauth_session::tests::legacy_unversioned_late_login_publication_violates_logout_contract ... ignored, intentional legacy RED; run explicitly for audit evidence
test state::oauth_session::tests::default_is_empty_and_restore_replaces_session ... FAILED
...
thread 'state::oauth_session::tests::default_is_empty_and_restore_replaces_session' ...
panicked at src\\state\\oauth_session.rs:36:39:
not yet implemented
...
test result: FAILED; 0 passed; 14 failed; 1 ignored; 0 measured; 560 filtered out
```

The failure is scaffold evidence, not a regression result against the original
baseline, which did not contain this coordinator module. The barrier tests
assume `commit` and `invalidate` serialize storage callbacks while preserving
the stale-token no-callback rule; all timing gates are local `Barrier` or
manual `Future::poll`/`Waker` synchronization, with no sleeps. The callback
probes deliberately depend on the scaffold's private `inner` mutex from its
child test module so the test can verify the required atomic critical section.

Intentional legacy RED command:

```text
cargo test --manifest-path src-tauri/Cargo.toml --lib state::oauth_session::tests::legacy_unversioned_late_login_publication_violates_logout_contract --locked -- --ignored
```

Exact failure evidence:

```text
running 1 test
test state::oauth_session::tests::legacy_unversioned_late_login_publication_violates_logout_contract ... FAILED

panicked at src\\state\\oauth_session_tests.rs:434:5:
assertion `left == right` failed: legacy late publication resurrected a session after logout
  left: Some("late-old-session")
 right: None
test result: FAILED; 0 passed; 1 failed; 0 ignored; 0 measured; 574 filtered out
```

This intentional failure is limited to the small legacy abstraction in the
test module. It demonstrates the audited unversioned publication hazard, not
the behavior of the original service at runtime and not a failure of the new
coordinator API. The ordinary positive target remains blocked only by the
declaration-only scaffold.

## Blocker

The test slice is ready for the coordinator implementation. Re-run the same
command after the scaffold bodies are implemented; classify any assertion
failure then as semantic RED/GREEN evidence separately from this pending
interface result.

## Delivery status

The blocker above is historical gate evidence. The production interface is now
complete, the accepted test object is unchanged, and all 16 ordinary coordinator
tests pass. The intentional legacy-failure test remains ignored in normal runs.
See oauth-implementation.md and results.json for final provider/storage checks.
