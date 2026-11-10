# internal/engine/extension

Installed-extension providers, control data, registration, staging and identity
remapping below the engine. Providers describe genuine released declarations;
core and statement families own execution and publication.

## Imports

The package depends on catalog/DDL/access and lower scalar/recipe owners, never
core or the root. It adds no engine state or mutex.

## What it owns

- Immutable providers implement control/version metadata and catalog validation.
  `Control` and `Versions` return detached copies.
- Registration owns provider identity and version/update paths. Every installed
  member retains its genuine implementation pointer and sealed declaration.
- `Stage`, installer actions and create/update/drop decisions build one private
  catalog candidate through the supplied Host. Complete preflight precedes effects.
- Owner codecs and remappers use symbolic installed identities with checked
  destination provider/type/routine authority, not source callbacks or OIDs.
- `ProcedureSymbol`, installed binding names and member-type roles authenticate
  actual declaration identity. Mutable names, namespace and membership are
  independent metadata, not executable capability.

## Invariants

- Catalog reads/stages are explicit inputs; providers own no publication registry
  or alternate extension lifecycle.
- Locked stage/import/update decisions run under caller-owned `Engine.Mu` and
  never reacquire it. Final provider validation runs outside that mutex.
- Genuine provider pointer, family/signature and companion-type validation remain
  required through copies, rename, detach and remapping. Names never repair a
  missing capability or create callable members.
- Speculative work reserves no identity. Accepted member ordering and diagnostic
  precedence are defined independently of dynamic allocator values.
- Dump SQL, owner metadata, messages and remap semantics are compatibility
  contracts. Failure leaves the private candidate and predecessor roots intact.

## What stays in the engine

`schemacmds` owns `extensionHost`, lock windows, stage lifetime and utility
orchestration. Core owns physical-session initialization, runtime authority,
resources and atomic catalog/storage publication. Provider validation does not
execute application workload while metadata locks are held.

## Testing

Pure registration/provider/staging units live here; engine-seeded provider tests
live in `enginetests` and composed family/core tests. Use genuine installations
and descriptors for identity/remap fixtures. Public SQL belongs to reference
extension packages under [docs/TESTING.md](../../../docs/TESTING.md).
