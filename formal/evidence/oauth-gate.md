# OAuth coordinator implementation gate

Root decision: ACCEPT the compiled contract slice at Git object
`495dc01a55edf4e50757d2db8e5f4dbb2e62fa31` against baseline `d29cd45`.
The independent security reviewer inspected both revisions and accepted the
second after deterministic ownership, positive refresh, and RAII tests were
added. Local review substitutes for external Jev because this is a security
boundary and source-level judgment is required.

The new API is INTERFACE_PENDING. The declaration scaffold compiles all 14
ordinary tests; its todo panics are not behavioral RED. Negative behavioral
evidence is the explicitly labeled legacy source abstraction and the
`OAuthLifecycleLegacy.cfg` logout counterexample. The original provider paths
were separately audited: they publish unversioned sessions after network
awaits, and Grok refresh failure unconditionally logs out the current account.

Implementation may complete the scaffold and wire the actual provider call
paths. Accepted tests are immutable. Final acceptance requires targeted GREEN,
source-path review of both providers, storage failures and startup behavior;
the coordinator tests alone do not establish implementation correspondence.

## Delivery status

The interface-pending paragraph above records the initial frozen test gate.
The scaffold is now fully implemented and wired into both OAuth providers,
device challenges, refresh, logout and startup restore. Frozen tests remain at
the accepted Git object; all 16 ordinary coordinator tests pass. Current storage
ordering and remaining platform assumptions are recorded in
oauth-implementation.md and the final delivery results.
