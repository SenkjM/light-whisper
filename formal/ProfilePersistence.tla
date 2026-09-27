------------------------- MODULE ProfilePersistence -------------------------
EXTENDS Naturals

CONSTANT MaxUpdates, AtomicQueue
ASSUME MaxUpdates \in Nat \ {0}
ASSUME AtomicQueue \in BOOLEAN

Empty == [generation |-> 0, value |-> 0]
Save(g, v) == [generation |-> g, value |-> v]

VARIABLES memory, generation, unscheduled, reserved, pending, timers, disk
vars == <<memory, generation, unscheduled, reserved, pending, timers, disk>>

Init ==
  /\ memory = 0
  /\ generation = 0
  /\ unscheduled = {}
  /\ reserved = {}
  /\ pending = Empty
  /\ timers = {}
  /\ disk = 0

\* The corrected update publishes its snapshot to the debounce slot before
\* releasing the profile lock. The slot's generation and value change together.
UpdateAndQueue ==
  /\ AtomicQueue
  /\ memory < MaxUpdates
  /\ memory' = memory + 1
  /\ generation' = generation + 1
  /\ pending' = Save(generation + 1, memory + 1)
  /\ timers' = timers \cup {generation + 1}
  /\ UNCHANGED <<unscheduled, reserved, disk>>

\* Previously, profile mutation, generation allocation, and slot publication
\* were three separately scheduled operations.
UpdateLegacy ==
  /\ ~AtomicQueue
  /\ memory < MaxUpdates
  /\ memory' = memory + 1
  /\ unscheduled' = unscheduled \cup {memory + 1}
  /\ UNCHANGED <<generation, reserved, pending, timers, disk>>

Reserve(v) ==
  /\ ~AtomicQueue
  /\ v \in unscheduled
  /\ generation' = generation + 1
  /\ unscheduled' = unscheduled \ {v}
  /\ reserved' = reserved \cup {Save(generation + 1, v)}
  /\ UNCHANGED <<memory, pending, timers, disk>>

Publish(save) ==
  /\ ~AtomicQueue
  /\ save \in reserved
  /\ reserved' = reserved \ {save}
  /\ pending' = save
  /\ timers' = timers \cup {save.generation}
  /\ UNCHANGED <<memory, generation, unscheduled, disk>>

Fire(g) ==
  /\ g \in timers
  /\ timers' = timers \ {g}
  /\ IF g = generation /\ pending.generation = g
        THEN /\ disk' = pending.value
             /\ pending' = Empty
        ELSE UNCHANGED <<disk, pending>>
  /\ UNCHANGED <<memory, generation, unscheduled, reserved>>

Next ==
  \/ UpdateAndQueue
  \/ UpdateLegacy
  \/ \E v \in unscheduled: Reserve(v)
  \/ \E save \in reserved: Publish(save)
  \/ \E g \in timers: Fire(g)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ memory \in 0..MaxUpdates
  /\ generation \in 0..MaxUpdates
  /\ unscheduled \subseteq 1..MaxUpdates
  /\ reserved \subseteq [generation : 1..MaxUpdates, value : 1..MaxUpdates]
  /\ pending \in [generation : 0..MaxUpdates, value : 0..MaxUpdates]
  /\ timers \subseteq 1..MaxUpdates
  /\ disk \in 0..MaxUpdates

LatestPendingWhenQueued ==
  (unscheduled = {} /\ reserved = {} /\ pending # Empty)
    => (pending.generation = generation /\ pending.value = memory)

SettledDiskMatchesMemory ==
  (unscheduled = {} /\ reserved = {} /\ timers = {}) => disk = memory
=============================================================================
