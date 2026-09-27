--------------------------- MODULE TaskOwnership ---------------------------
EXTENDS Naturals

CONSTANT MaxRequests, AtomicBegin
ASSUME MaxRequests \in Nat \ {0}
ASSUME AtomicBegin \in BOOLEAN

VARIABLES generation, reserved, installed, owner
vars == <<generation, reserved, installed, owner>>

Init ==
  /\ generation = 0
  /\ reserved = {}
  /\ installed = {}
  /\ owner = 0

\* The corrected begin operation allocates a generation and replaces the
\* cancellation slot while holding the same mutex.
BeginAtomic ==
  /\ AtomicBegin
  /\ generation < MaxRequests
  /\ generation' = generation + 1
  /\ installed' = installed \cup {generation + 1}
  /\ owner' = generation + 1
  /\ UNCHANGED reserved

\* The old implementation allocated before locking the cancellation slot.
Reserve ==
  /\ ~AtomicBegin
  /\ generation < MaxRequests
  /\ generation' = generation + 1
  /\ reserved' = reserved \cup {generation + 1}
  /\ UNCHANGED <<installed, owner>>

Install(g) ==
  /\ ~AtomicBegin
  /\ g \in reserved
  /\ reserved' = reserved \ {g}
  /\ installed' = installed \cup {g}
  /\ owner' = g
  /\ UNCHANGED generation

Cancel ==
  /\ owner # 0
  /\ owner' = 0
  /\ UNCHANGED <<generation, reserved, installed>>

\* Completion from an older request must not clear the newer owner.
Clear(g) ==
  /\ g \in installed
  /\ owner = g
  /\ owner' = 0
  /\ UNCHANGED <<generation, reserved, installed>>

Next ==
  \/ BeginAtomic
  \/ Reserve
  \/ \E g \in reserved: Install(g)
  \/ Cancel
  \/ \E g \in installed: Clear(g)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ generation \in 0..MaxRequests
  /\ reserved \subseteq 1..MaxRequests
  /\ installed \subseteq 1..MaxRequests
  /\ owner \in 0..MaxRequests

NewestInstalledOwnsSlot ==
  owner = 0 \/ \A g \in installed: g <= owner
=============================================================================
