# internal/engine/view

Uses of frozen queries below core: view/rule/policy/trigger DDL decisions,
rewriting, write lowering, structural capabilities, source/dependency/lock walks,
RLS selection and materialized-refresh records. Frozen payloads/rendering/remap
belong to `stored`; runtime execution and publication belong to core.

## What it owns

Focused CreateHost/Host/StageHost contracts; immutable definition decisions;
rule/CTE rewrite programs and recursion checks; target/source write mappings;
semantic privilege/row-security walkers; structural updatability; trigger clone
synchronization; dependency/drop readers; refresh/snapshot import decisions.

StageCreate takes the narrow construction capability and writes only its supplied
stage. Query/source walkers preserve original occurrence/frame, defining manifest
and owner authority instead of treating substituted calls as fresh permissions.
Structural metadata is independent of a viewer's current DML privileges.

## Invariants

- Decisions own no mutex, engine state, wait or stage publication. Host methods
  run under caller-owned Engine.Mu and never acquire/release it. Live-storage
  rewrite/normalization readers also require that caller lock.
- Source/view restrictions apply at semantic admission before expansion. Rule
  handoff and automatic write lowering retain distinct ownership; namespace,
  owner or spelling does not grant an exemption.
- Rewrite ordering, recursion/event ancestry, command bounds and RETURNING
  ownership are explicit. Actions execute under the genuine rule owner and
  restore caller authority for residual work.
- Stored call lifetime belongs to its original definition/presentation slot;
  NEW substitution never manufactures a new defining manifest or invocation grant.
- Trigger synchronization preserves exact Parent/OID lineage and rejects
  descendant collisions before publication. It uses the canonical stage path,
  not a second trigger registry or catalog sweep for unrelated publications.
- Concurrent refresh checks genuine eligible key coverage, duplicates and source
  lineage. Snapshot import admits only emitted bounded typed shapes and authentic
  providers under isolated-import authority, never arbitrary privileged SQL.
- Diagnostics, first-error order and canonical serialization are contracts.

## What stays in the engine

`viewcmds` owns statement orchestration/hosts. Core owns row/source execution,
rule command lifetime, RLS evaluation, trigger frames/effects, locks, checkpoints,
matview generation and final catalog/storage/index publication.

## Testing

Pure decision/slot/source-walk units live here. Engine-host fixtures live in
viewcmds, schemacmds and core. Public SQL belongs to reference view/role/operator
packages under [docs/TESTING.md](../../../docs/TESTING.md).
