# internal/engine/index

Index records, immutable keys/roots, binding, enforcement, publication transport,
DDL decisions and dump presentation below core. Statement orchestration belongs
to `indexcmds`; core owns stages, waits, storage and final installation.

## What it owns

Typed index method/class/family selection and key programs; COW physical roots;
private row/version membership; unique/exclusion probes and accepted candidates;
constraint timing/events; snapshot/remint transport and logical verification.
Physical representations are method-owned, not interchangeable by shape.

## The DDL host

`Host` embeds `ddl.Host` and supplies removal, diagnostics, current catalog and
staging. `KeyHost` is the narrower binding seam; other focused hosts separate
prepare, build and maintenance needs. Locked metadata decisions never wait or
publish; orchestration runs expression/provider work outside the engine mutex
and readmits exact captured predecessors afterwards.

## The write host

`WriteHost` exposes bounded engine-owned capture/acceptance windows and unlocked
claim/wait capabilities. Captures return write authority with the same paired
catalog view; no per-row authority adapter is retained. Pure decisions consume
captured values and accepted versions, not a live mutable engine root.

## State slots and registries

Wait/exclusion registries are runtime-owned. Journal, stage and scope slots carry
private roots, constraint modes/events and savepoint snapshots. Their owners
recover slots through checked helpers; mutable slot state clones before writes.
PREPARE transfers the same genuine roots/events rather than replaying them.

## Invariants

- Roots and produced key backing are immutable. Predicate qualification precedes
  key evaluation, and row/index installation shares the Finish/commit frontier.
  Failed batches, waits, builds or publication preserve predecessors.
- Class identity requires actual catalog method/family/member authority, not a
  physical method enum or structural equality. Installed support routines retain
  their genuine declaration and current validation.
- Membership, positions and dirty probes refer to exact RowID/VersionID and
  storage provenance. Equal payloads or row numbers do not establish ancestry.
- Definition changes, metadata-only transport, produced-key reuse and genuine
  rebuild intent are separate. Transport never silently reevaluates prior writes
  or erases construction history/debt.
- Constraint events record actual potential conflicts; transient modes/events
  are transaction state, never serialized catalog defaults.
- Partitioned roots own metadata; physical leaf providers enforce entries.
  Coverage/adoption preserves genuine constraint/backing identity.
- Summary/bitmap candidates are conservative and require ordinary SQL recheck.
  Unsupported optimization evidence falls back rather than proving absence.
- Independent remint assigns destination identities from genuine captured tuples;
  source allocation order and equal values are not replacement authority.

## What stays in the engine

Index statement phases, transaction/ticket lifetime, live admission and authority,
row publication, provider callbacks and final catalog/storage fences. This
package owns no engine mutex or alternate publication path.

## Testing

Pure binding/root/probe/transport units live here; engine fixtures in `enginetests`
and core cover accepted roots and journal/publication lifetime. `indexcmds` tests
cover utility orchestration; public SQL belongs to reference index packages.
