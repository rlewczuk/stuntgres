# internal/engine/catalog

The catalog object model and its immutable snapshots: descriptors, `State`,
`Read`, the provider registry, validation of the pure core kinds and the
copy-on-write overlay `Candidate`. "Catalog" here means the data, not DDL.
The package sits at layer L3 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `ids`, `expr`, `authorization`, `sqltype`, `parser` and the
standard library, and never the engine or its sibling `functions`.

## What it owns

- **Descriptors.** `Descriptor` is the identity contract (`Address`,
  `Revision`, `Namespace`, `Name`, `Kind`). The kinds are `Namespace`,
  `Relation`, `Database`, `Extension`, `Procedure`, `Operator`, `Parameter`,
  `Tablespace`, `Type`, `Collation`, `Conversion`, `TSTemplate`,
  `TSDictionary` and `ForeignObject` (wrappers, servers, user mappings). Their
  records (columns, constraints, defaults, generation, identity, indexes,
  partitions, inheritance, policies, sequence options, foreign tables,
  default ACL records, ...) live here too. Identity fields are
  `ObjectAddress`, `ObjectNamespace`, `ObjectName` and `ObjectRevision`; read
  them through the `Descriptor` methods.
- **Payload interfaces.** Engine-defined payloads stay behind interfaces
  that expose only what catalog code needs: `ViewPayload`, `RulesPayload`,
  `PolicyPayload` and `ExtensionImplementation`. Expression payloads hold
  `expr.Expr`. The engine's checked accessors (`viewOf`, `rulesOf`,
  `policyExpressionOf`, `pgcryptoProviderOf`, ...) recover the concrete
  types.
- **State and Read.** `State` is one catalog snapshot: the epoch, namespace
  generation, descriptor and name maps, operator name index, default ACL
  records, dump priorities and the replacement-only translations. `Read` is
  a view of a state with its `Owner`. The fields are private; there is no
  other access to the maps.
- **Read API** (`read.go`). Typed accessors defined once on `*State` and
  delegated by `Read`: `Kind(a)` is the comma-ok type assertion and
  `MustKind(a)` the single-value one, which panics like the assertion it
  replaced. `Get`/`Find`, `NameAddress`/`LookupName`, the `Descriptors` and
  `Names` iterators and the counts are the map indexes. None guards a nil
  state; the older `Descriptor`, `Lookup`, `LookupType` and `LookupArray`
  keep their nil guard. `state_access.go` reads the other fields
  (`Epoch`, `DefaultACL`, `DumpPriority`, ...).
- **Write API** (`state_write.go`, setters in `state_access.go`). Only for a
  state that is still being built: the bootstrap seed, a clone
  (`CloneState`), a candidate's merged state or a replacement before
  publication. `NewState` and `NewRead` construct.
- **Owner and providers** (`owner.go`). `Owner` is the identity every `Read`
  of one engine carries: the provenance `ids.Domain`, the principal domain
  and ACL authority that stored values must belong to, the `Provider`
  registry keyed by class OID and the extension implementations. Its address
  identifies the engine's catalog.
- **Core-kind validation.** `NamespaceProvider`, `DatabaseProvider`,
  `TablespaceProvider`, `CollationProvider`, `ConversionProvider` and
  `ForeignCatalogProvider` validate their kinds against a `Read`, with the
  builtin collation, conversion and tablespace inventories they check.
- **Candidate** (`candidate.go`). The pure copy-on-write overlay of a catalog
  change over an immutable base `Read`: created and replaced descriptors and
  their names, touched name slots, removals, vacated names and default ACL
  changes. `Create`, `Replace`, `RecordRemoval` and `LookupName` stage
  changes; `State` merges them into a new unpublished state. It never writes
  the base. Its fields are exported because the engine's `CatalogStage`
  embeds it and still writes them directly.
- **Name keys and operator index.** `DescriptorNameKey`, `OperatorNameIndex`,
  `OperatorIndexEdit` and `RebuildOperatorNames`; default ACL helpers
  (`OverlayDefaultACLs`, `PruneNamespaceDefaults`, `RemapDefaultSchemas`,
  `ValidateDefaultACLs`, ...).

## Invariants

- A published `State` is immutable. Writers use the write API only on states
  they are building; publication swaps the whole state.
- Copy-on-write sharing: clones and merged states copy the descriptor and
  name maps and share the rest (default ACL records, operator index) until
  they change. No deep copies on publication or capture.
- The package has no mutex. The engine mutex guards the published state and
  every unpublished state the engine shares between goroutines.
- Provenance: a `Read` belongs to the engine whose `Owner` it carries;
  validators compare domains through the owner, never through the engine.

## What stays in the engine

DDL commands, `CatalogStage` and its publication coordination (storage row
shapes, index candidates, domain validation, materialized replacement, role
candidates), publication itself, the `catalogStore` allocators, seeding, the
transaction catalog journal, and the validators that need engine services:
relation, type, extension and procedure, operator and parameter validation
(stored-SQL rendering, expression nodes, the operator inventory, settings
definitions, extension implementations). Those register through the same
`Provider` registry. The engine reaches the moved declarations through
temporary aliases in `catalog_aliases.go` until step 5f removes them.

## Testing

Follow the [shared testing guide](../../../docs/TESTING.md). Run from the
repository root.

- Read and write API: `make check RUN='^TestCatalog(ReadTypedAccessors|ReadUntypedAndNameAccessors|ReadAccessorsRequirePopulatedRead|StateWritesStayOnTheirState)$'`.
- Engine-side catalog seams (registry, payload accessors, operator index,
  publication): `make check RUN='^Test(CatalogAllocatorAndProviderBoundaries|CatalogProviderRegistersBeforeInitialization|CatalogPayloadAccessors|UserOperatorIndex)'`.
