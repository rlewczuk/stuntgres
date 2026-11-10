# internal/engine/partition

Partition records and decisions below core: typed bounds/routing, key recipes,
attachment/detachment, constraints, pruning, inquiries and dump graph facts.
It sits above index/table decisions; statement orchestration is `partitioncmds`.

## What it owns

Bounds and routing use actual catalog topology and immutable typed values.
Recipe evaluation shares the ordinary expression runtime; definition helpers
build private candidates. Constraint proofs may avoid scans only when the
captured checked facts establish the required bound. Dump/replay facts use the
same dependency graph and identity owners as ordinary DDL.

## Host and lock contract

The package owns no engine state or mutex. Metadata Host methods enter with
caller-owned Engine.Mu and never acquire/release it or wait. Recipe evaluation
and detached pruning run unlocked on captured inputs; core owns admission,
snapshot selection and publication fences.

## Query branch refutation

`CapturePruner` borrows admitted immutable topology/layouts. `Select` evaluates
only explicitly admitted proof inputs through the current evaluation context;
it does not rerun volatile or arbitrary provider work. One bounded search budget
covers the whole selection. Exhaustion/unknown evidence retains branches.
Pruning never relaxes query locks, source authority or tuple visibility.

## Invariants

- Virtual roots/intermediates own descriptors, not physical heaps. Native and
  foreign leaves retain their genuine independent storage/handler authority.
- Routing is deterministic for typed keys and reuses command scratch rather than
  allocating per row. Row generation and write movement retain DML's order.
- Stable attributes, partition membership and constraint origins retain exact
  immediate-parent provenance, not same-name/global-OID inference.
- Lock orchestration captures original identities, waits unlocked in the prescribed
  parent/candidate/default/descendant order, then rechecks authority before effects.
- Adoption preserves genuine provider/backing identities and complete coverage;
  unsupported or unproven constraints cannot waive row validation.
- Dump ordering and serializer bytes are deterministic; replay parsers stay with
  their emitters and confer no authority on ordinary SQL.

## What stays in the engine

Physical lock admission, pending-edge snapshot selection, data capture/routing
orchestration, stage lifetime, repair trust and final publication. Readers derive
effective pending topology without rewriting canonical catalog membership.

## Testing

Pure bounds/proof/pruning/inquiry units live here. Composed engine tests live in
`partitioncmds` and core; public SQL belongs to `reference/partitions` and
`reference/inheritance` under [docs/TESTING.md](../../../docs/TESTING.md).
