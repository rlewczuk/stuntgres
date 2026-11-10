# internal/engine/partitioncmds

Partition attachment/detachment and native partition/inheritance replay
orchestration. `partition` and `table` own decisions; core owns runtime authority.

## Layering

Imports core, tablecmds' shared hosts and permitted indexcmds orchestration,
never the root. Composition imports this package. Table creation/inheritance
utilities remain in tablecmds; routing, pruning, physical lock admission and
private-import trust checks remain in core.

## Entry points

`AttachPartitionLocked` and `DetachPartitionLocked` enter with Engine.Mu held and
the parent admitted, and release it on every return. Waits run on the statement's
real ticket outside locks; post-wait checks validate original identities, names,
ownership and topology before one staged change. Bound binding is dormant;
actual bound/row evaluation uses the current unlocked invocation context.

Concurrent detach owns an irreversible pending publication before waiting its
frozen parent-holder cohort. Cancellation after that publication leaves pending
state, not background success. A later phase reopens exact saved references,
never a replacement found under an old name. Finalization uses ordinary
transactional teardown and canonical virtual-completion/snapshot waits. Readers
derive effective pending topology without mutating canonical catalog membership.

Restore hooks run only on the authenticated isolated import. Lifecycle captures
retain genuine destination references, owners, revisions and completed effects;
reserved names alone grant no teardown or ownership authority. Repairs reuse
ordinary dependency-aware execution and final catalog validation. Failure and
run disposal retire transient statement leases.

## Testing

External `partitioncmds_test` tests use composed engines and `enginetest`.
Test-only accessors stay with the owner; public SQL is in `reference/partitions`
and `reference/inheritance` under [docs/TESTING.md](../../../docs/TESTING.md).
