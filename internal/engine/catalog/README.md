# internal/engine/catalog

The catalog object model and immutable snapshots: descriptors, `State`, `Read`,
provider registration, validation and the copy-on-write `Candidate`. Catalog
means data and identity, not DDL orchestration or execution authority.

## Imports

The package sits below analysis and feature decisions. It never imports core or
the root engine. Lower routine/host payloads own immutable declarations and
registered tokens, not catalog authority.

## What it owns

- **Descriptors** expose identity through `Address`, `Revision`, `Namespace`,
  `Name` and `Kind`. Feature payload interfaces keep checked immutable definitions
  behind the catalog model rather than introducing alternate object registries.
- **`State` and `Read`** own snapshots, descriptor/name indexes and typed lookup.
  The write API is for unpublished states only. Derived indexes follow the same
  canonical setters, clone and remap paths as descriptors.
- **`Owner` and providers** bind reads to one engine and register kind-specific
  validation. Metadata presence, names and numeric identifiers do not confer
  executable provider authority.
- **`Store`** owns committed state and allocation frontiers. Seed construction and
  bulk load build private roots before publication.
- **`Candidate`** records a pure copy-on-write overlay and exact touched
  predecessors. Publication helpers validate dependencies, bindings and identity
  without installing a prefix.
- **Definition readers and name claims** retain coherent definition and namespace
  observations for later validation. Diagnostic-name helpers are shared with
  admission and DDL packages.
- **Shared projections** prepare affected database roots before installation;
  core owns the final atomic publication.
- **Callback request records** describe immutable typed metadata using standard Go
  values. Optional capability absence means decline, not SQL rejection. Runtime
  handles, callbacks, samples and private plans are not persistent catalog state.

Relation layout keeps stable physical attributes separate from dense evaluation
slots and kind payloads. Descriptor copies share immutable slices/maps until
changed. Logical storage locators identify incarnations, not files or paths;
metadata-only changes preserve them and real rewrites replace them.

Routine metadata separates immutable declaration/display facts from executable
capability and checked user programs. Catalog projection never compiles or
executes a body. Installed origin, current extension membership and mutable SQL
names are independent facts. Validation of retained structural metadata is
separate from current executable admission.

## Invariants

- Published `State` is immutable. Writers build private states; publication swaps
  complete roots. Clones share backing and copy only touched maps/indexes.
- The package has no mutex. `Engine.Mu` guards all Store fields and unpublished
  states shared by goroutines. Revision frontiers reserve nothing speculatively
  and commit only at successful installation.
- Reads carry an exact `Owner`; validators compare its domain, not an engine
  pointer guessed from identifiers. Zero, stale and foreign references reject.
- Names and numeric equality never establish identity. Surviving attributes keep
  their numbers, retired numbers are never reused, and replacement fences old
  references instead of retargeting them by name or OID.
- Analysis stamps are not object identity. Pure identity edits and executable
  definition changes retain their distinct invalidation rules.
- ACL absence differs from explicit empty. Historical initial-privilege records
  own full object/subobject addresses and ordered principal entries independently
  of current ownership/ACLs. Their immutable comparison baseline is provenance,
  not live inventory or permission authority.
- Dependency and ownership metadata retain exact endpoints; remapping never
  substitutes same-name or global-number inference for qualified identity.

## What stays in the engine

DDL commands, catalog stages, lock windows, allocator installation, storage/index
fences, validators and atomic publication belong to core and statement families.
Replacement reowns identities and principals through that same validation path.

## Testing

Package units exercise reads, private writes, COW candidates, indexes, providers,
identity remapping and seeds without an engine. Core tests cover publication and
replacement; public SQL belongs to reference packages under
[docs/TESTING.md](../../../docs/TESTING.md).
