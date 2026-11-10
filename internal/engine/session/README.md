# internal/engine/session

Session/operation state, transaction ownership, settings journals, protocol and
lifecycle records below core. It imports lower owners, never core or the root;
engine orchestration is reached only through `Host`.

## What it owns

- `Session` owns lifecycle, leases, resources, settings and transaction attachment;
  `Operation` owns one admitted command and its effective context/presentation.
- `TransactionOwner` serializes admission and owns private rows, catalog journal,
  snapshot roots, effect slots and chronological savepoint/internal marks.
  Prepared ownership transfers those same immutable roots to the 2PC registry.
- Settings stores/actions and balanced local-userid/routine environments separate
  physical values, transaction overlays, effective activation and reset/defaults.
- Protocol codecs/cycles, timeout and notification records are session-owned;
  classification helpers are pure over syntax and owner values.
- `Journal` records private catalog/storage/index/role and typed metadata deltas,
  exact predecessors, removals and publication fences. Data-view/capture helpers
  expose immutable roots, not a parallel committed store.
- Opaque slots keep feature-owned state below core without importing upper-layer
  implementations. Engine Host adapters own cross-subsystem transitions.

## Invariants

- Session/owner/admission/settings mutexes follow
  [`lock`](../lock/README.md#mutex-ownership-and-lock-order). Code here never takes
  Engine.Mu. External work, callbacks and terminal joins remain outside locks.
- Statement admission serializes writers; core validates/publishes under Engine.Mu.
  Shared journals originate from `NewJournal`; clone/capture serializes mutable
  projection-cache fields while source maps/slots retain stable-owner protection.
- A statement writes a clone, savepoints retain old roots, and Clone copies maps/
  mutable slots while sharing immutable fences/backing. Definitions and rows use
  the same journal; nontransactional generator effects remain separate.
- Nil slot means absent. One checked owner helper stores/recovers it; never store
  a typed nil. In-place slot writers deep-copy on Clone or replace before writing.
- Physical rewrite intent, preserving-copy/maintenance provenance and actual
  storage pointers are separate. Metadata transport cannot manufacture lineage
  or erase observations needed at final publication.
- Internal procedural marks share chronological checkpoint ownership but are not
  SQL-named savepoints. Statement stage/effect restoration remains core's job.
- Fences validate by kind at fixed publication boundaries, preserving recording
  order and deterministic first failure. Captures are not publication grants.
- Session/operation extensions install before sharing and remain pointer-stable.
  Physical resources and RNG/clock ownership are not rewound by logical rollback
  or imported from native controls.

## What stays in the engine

Statement dispatch, authority capture, catalog/storage/visibility orchestration,
lock windows, combined publication, replacement, checkpoint restoration and
physical-session cleanup. Session records never drive an alternative transaction
or commit protocol themselves.

## Testing

Pure journal/settings/protocol/lifecycle helpers are tested here. Core tests cover
real admitted owner transitions, callbacks and publication. Use genuine immutable
roots and deterministic joined lifecycle controls; public SQL belongs to reference
packages under [docs/TESTING.md](../../../docs/TESTING.md).
