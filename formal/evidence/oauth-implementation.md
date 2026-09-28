# OAuth coordinator implementation evidence

## Scope

Implemented the accepted slice from `oauth-gate.md` against the frozen test
object `495dc01a55edf4e50757d2db8e5f4dbb2e62fa31`. Production ownership is
limited to:

- `src-tauri/src/state/oauth_session.rs`
- OAuth fields/accessors in `src-tauri/src/state/app_state.rs`
- `src-tauri/src/services/codex_oauth_service.rs`
- `src-tauri/src/services/grok_build_oauth_service.rs`
- `src-tauri/src/commands/codex_oauth.rs`
- `src-tauri/src/commands/grok_build_oauth.rs`

The frozen test file was not changed. Its Git object hash remains
`495dc01a55edf4e50757d2db8e5f4dbb2e62fa31`.

Supplemental tests were added outside the frozen file for pending device
challenge ownership and provider metadata disk helpers.

## Red to green

The mandated red replay was:

```text
cargo test --manifest-path src-tauri/Cargo.toml --lib state::oauth_session --locked
```

Before implementation it compiled and collected 15 tests, then reported
`0 passed; 14 failed; 1 ignored`; the ordinary failures stopped in the
declaration scaffold's `todo!()` bodies. The ignored test is the intentional
legacy publication counterexample.

After the coordinator and challenge implementation, the exact command
reported:

```text
running 17 tests
test result: ok. 16 passed; 0 failed; 1 ignored
```

The supplemental RED replay first failed to compile with missing
`publish_challenge`, `claim_challenge`, `read_session_meta_at`, and
`write_metadata_at` APIs. Those failures were replaced by the green run above.

The coordinator now keeps state epoch, session, and login ownership under one
`parking_lot::Mutex`. Synchronous persistence and runtime publication run in
the same critical section. Refresh network work is serialized by the per-state
Tokio gate, and each refresh takes its snapshot after acquiring that gate.

## Regression guards

The existing OAuth guards passed:

```text
cargo test --manifest-path src-tauri/Cargo.toml --lib services::codex_oauth_service --locked
5 passed; 0 failed

cargo test --manifest-path src-tauri/Cargo.toml --lib services::grok_build_oauth_service::storage_tests --locked
2 passed; 0 failed

cargo test --manifest-path src-tauri/Cargo.toml --lib services::grok_build_oauth_tests --locked
24 passed; 0 failed

cargo test --manifest-path src-tauri/Cargo.toml --lib services::codex_oauth_prewarm_tests --locked
5 passed; 0 failed
```

`git diff --check` reported no whitespace errors. `rustfmt` was run only on
owned production Rust files and the new supplemental test module; the frozen
test hash stayed unchanged. Targeted runs emitted only the unrelated
`AppState` read-key dead-code warning from the root LLM routing slice.

## Storage and runtime limits

Logout now invalidates the coordinator before attempting deletion, clears
runtime memory even when keyring or metadata deletion fails, treats keyring
`NoEntry` as success, and returns other deletion errors through both service
and command APIs. Login and refresh publish runtime state only after the
storage callback succeeds; a stale commit does not invoke storage or return
its stale token/API key. Device start reserves an owner before requesting the
challenge, stores a server-side binding of the actual device identifier and
user code, and completion must claim that binding before polling. Logout or a
new browser/device login removes the pending challenge before any late poll or
persist. Storage commits write a logout intent, then the new refresh token,
then delete the legacy entry, and publish valid metadata last.

The keyring token, metadata JSON, and legacy entry do not form a transaction.
All metadata writes use the existing atomic write helper. A commit or logout
first persists a `logged_out` tombstone, then changes the refresh key, then
writes valid metadata last. Startup rejects malformed/unreadable metadata and
never revives an orphan refresh key; an absent metadata file only permits a
complete legacy session migration. Logout honors the tombstone while deletion
is incomplete and removes it only after all deletion steps succeed. If the
tombstone write itself fails, logout returns an error and only the in-process
runtime clear is guaranteed; cross-restart recovery then requires a later
repair/retry.

Startup restoration reserves a login epoch before reading storage and commits
the loaded session only if logout or a newer login has not invalidated it.
This closes the in-process late-restore race. Live browser/device OAuth
round-trips and platform keyring fault injection were not run because they
require external credentials or a separate storage fault harness. The
supplemental disk tests cover malformed/empty metadata and atomic-write target
failure; cross-restart behavior is source-reviewed but not exercised against a
fault-injected keyring.
