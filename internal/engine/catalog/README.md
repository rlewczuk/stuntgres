# internal/engine/catalog

The catalog object model and its immutable snapshots: descriptors, `State`,
`Read`, the provider registry, validation of the pure core kinds and the
copy-on-write overlay `Candidate`. "Catalog" here means the data, not DDL.

## Imports

Layer L3 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `ids`, `expr`, `authorization`, `largeobject`, `proc`,
`internal/util`, `sqltype`, `parser`, `sqlstate` and the standard library, never
the engine or its sibling `functions`. `proc` owns immutable SQL definitions and
private frames, not catalog authority.

## What it owns

- **Descriptors.** `Descriptor` is the identity contract (`Address`, `Revision`,
  `Namespace`, `Name`, `Kind`) of every kind (namespaces, relations, types,
  procedures, operators, collations, conversions, extensions, foreign objects,
  large objects, access methods, casts, operator classes and families, ...) and
  their records. Read identity through the `Descriptor` methods.
- **Relation layout.** `Relation` keeps identity and the live column shape at the
  top level, the sub-records `ColumnMeta`, `Constraints`, `Security` and
  `History`, and the kind payloads (`SQLIndex`, `Partition`, `Sequence`, `View`,
  ...). Sub-records are values, never pointers, so copying a `Relation` shares
  their slices and maps exactly like its top-level ones.
- **Payload interfaces.** Engine-defined payloads stay behind `ViewPayload`,
  `RulesPayload`, `PolicyPayload` and `ExtensionImplementation`, which expose only
  what `analyze`, `pgcatalog` and object-identity code read. Visitors stop at the
  first error; checked accessors (`stored.ViewOf`, `stored.RulesOf`,
  `stored.PolicyOf`, ...) recover the concrete types.
- **State and Read** (`read.go`, `state_access.go`). `State` is one catalog
  snapshot (epoch, namespace generation, descriptor and name maps,
  operator/procedure name indexes, default ACL records, dump priorities,
  replacement translations); `Read` is a state plus its `Owner`. Fields are
  private. Typed accessors live once on `*State` and `Read` delegates: `Kind(a)`
  is the comma-ok assertion, `MustKind(a)` panics like a failed one;
  `Get`/`Find`, `LookupName`, the `Descriptors`/`Names` iterators and the counts
  read the maps. Only `Descriptor`, `Lookup`, `LookupType` and `LookupArray`
  guard a nil state.
- **Write API** (`state_write.go`, setters in `state_access.go`), only for a state
  still being built: the bootstrap seed, a `CloneState`, a candidate's merged
  state or a replacement before publication.
- **Storage** (`state_maps.go`). Descriptors and names live in
  `util.PersistentMap`s. `CloneState` and struct copies share them in O(1), and
  `Candidate.State` applies only the change, so publication costs the size of the
  change. `Descriptors`/`Names` iterate the version current at the call, so a
  loop may write the state it iterates. Whole-catalog writers (bootstrap seed,
  `RemapPrincipals`, replacement remap) bracket the work with
  `BeginBulkLoad`/`EndBulkLoad`; a state in a bulk load is private to its writer
  and copying it panics.
- **Owner and providers** (`owner.go`). `Owner` is the identity every `Read` of
  one engine carries: the provenance `ids.Domain`, principal domain and ACL
  authority, the `Provider` registry keyed by class OID and the extension
  implementations. The core-kind providers here (`NamespaceProvider`,
  `DatabaseProvider`, `TablespaceProvider`, `CollationProvider`,
  `ConversionProvider`, `ForeignCatalogProvider`, `LargeObjectProvider`) validate
  against a `Read`.
- **Candidate** (`candidate.go`). The pure copy-on-write overlay of a catalog
  change over an immutable base `Read`; `CatalogStage` embeds it. `Create` and
  `Replace` run name-conflict and sticky relation-history checks;
  `UpdateDescriptor` completes caller-owned private copies without replaying
  them; `RecordRemoval` keeps the base reference; `ForgetDescriptor` deletes only
  a private descriptor. `LookupName` resolves through the candidate, while
  `Get`/`Find`, bindings, observations, counts and iterators expose **overlay
  entries only**; reads never merge, clone, scan the base or return writable
  maps. `State` merges into a new unpublished state without changing the base.
  For default ACLs, `ObserveDefault` records the base input once (including
  absence), `SetDefaultACL` stages output (present nil means removal) and
  `DefaultRecordsUnchanged` checks the observed inputs.
- **Derived name indexes** (`operator_index.go`, `procedure_index.go`).
  `OperatorNameIndex` and `ProcedureNameIndex` map names to addresses; they are
  membership, not callable or ACL authority. Edits (`OperatorIndexEdit`,
  `ProcedureIndexEdit`) detach the touched buckets once. The seeded indexes are
  shared read-only by every engine in the process, so an index is never written
  in place. Reset clears them and wholesale import/remap rebuilds them before
  binders run. Binder definition readers enumerate the indexed bucket plus staged
  deltas, never the full base inventory.
- **Access methods, operator classes and families, casts** (`access_method.go`,
  `operator_class*.go`, `operator_family*.go`, `cast*.go`). Immutable global
  descriptors read through `Read.AccessMethod`, `Read.OperatorFamily`, ..., the
  same authority Candidate publication uses; compiled seeds are bootstrap inputs
  shared between engines. `CastDependencies` is the shared pg_depend/DROP/dump
  authority. `SelectCoercion` and `CanCoerce` are the effect-free coercion
  authority of the analyzer and routine feasibility probes: declared direct pairs
  take precedence, a context-ineligible pair blocks generic fallback, and there
  is no transitive search. Aggregate binary adapters keep their own
  `AggregateBinaryRecipe` and never borrow current cast rows.
- **Builtin routine identities** (`builtin_routine_identities.go`,
  `regproc_lookup.go`): lookup-only `RoutineIdentity` facts and result OIDs of
  the released `pg_proc` rows. They are not descriptors or SQL rows and never
  grant execution.
- **Definition reader** (`definition_reader.go`, `definition_reads.go`).
  `DefinitionReader` is the sparse read that definition cooking uses to see a
  statement's unpublished, ordered DROP effects; the engine implements it over a
  catalog stage. It is never persisted and is not a permission cache.
  `DescriptorAt` and the typed `Definition*At` reads serve the binder and DDL.
- **Diagnostic names** (`diagnostic_names.go`): `QuoteIdentifier` always quotes;
  `Identifier` quotes only when the grammar needs it (deparse, dump);
  `DependencyIdentifier` follows PostgreSQL `quote_identifier`;
  `TypeDiagnosticNameAt`/`TypeDiagnosticNamer` name a type bare when the search
  path finds it first. The engine and `access` share them.
- **Record helpers** shared by DDL, query admission and restore: inheritance and
  partition reads (`inheritance.go`, `partition.go`), constraint naming
  (`constraint_names.go`), definition OIDs (`definition_identity.go`), relation
  history (`relation_history.go`: the sticky `HasSubclasses`/`HasIndexes` flags,
  set on replacement and cleared only by VACUUM's own publication), principal
  remap (`principal_remap.go`: `RemapPrincipals` over an unpublished candidate;
  ownerless kinds stay unchanged, unknown kinds fail), reg\* references
  (`alias_references.go`), extension version paths (`extension_versions.go`) and
  `AdvanceRevisionFrontier`.

## Invariants

- A published `State` is immutable. Writers use the write API only on states they
  are building; publication swaps the whole state.
- Structural sharing: clones and merged states share maps and indexes until they
  change; a change copies only what it touches. No deep copies on publication or
  capture.
- The package has no mutex. `Engine.mu` guards the published state and every
  unpublished state shared between goroutines. Revision frontiers are pure and
  saturating; the engine store commits them under `Engine.mu` only at successful
  installation.
- Provenance: a `Read` belongs to the engine whose `Owner` it carries; validators
  compare domains through the owner, never the engine.
- Identity is never inferred from names or numbers. Relation identities, sparse
  stable attributes and dense evaluation slots are distinct: compaction never
  renumbers surviving attributes and a retired attribute number is never reused.
  Removal/recreation and Reset/Restore fence the old address instead of
  retargeting by name or OID; numeric inequality is not a replacement proof.
- `Type.DomainPlanStamp` is an analysis stamp, not identity.
  `NormalizeTypeReplacement` is the one rule (revised mutations invalidate, pure
  identity edits preserve), and a preserved array rename stamp never hides
  changes on its element.
- Partition constraint origins (`PartitionOrigin`) record exact locality and at
  most one immediate parent constraint; remapping is parent-qualified, never a
  global OID map or a same-name inference.
- `DefaultACLRecord` owns its unique persistent row OID: the catalog allocator
  assigns it only to final new records, updates and remaps preserve it, and reads
  never allocate.

## What stays in the engine

DDL commands, `CatalogStage` and publication, the `catalogStore` allocators,
seeding, the transaction catalog journal, the registration of providers that
live in their own packages (`role.ParameterProvider`, `table.RelationProvider`,
`stored.TypeProvider`, `stored.ProcedureProvider`, `extension.CatalogProvider`,
`routine.OperatorProvider`), definition-OID assignment (the Restore remap is
`control.RemapDefinitionOIDs`) and the `DefinitionReader` implementations.
Callers name the declarations here directly, without aliases.

## Testing

Package units cover the read/write API, persistent storage and bulk loads,
Candidate ownership, cast pair indexes, operator class and family identity,
principal and address remaps, diagnostic names, tablespace validators and the
builtin routine inventory. `testdata/builtin_routine_results_pg18_6.tsv` is the
independent record of every builtin routine identity and result, extracted from
the pinned PostgreSQL 18.6 oracle; `TestBuiltinRoutineResultInventory` checks it.
Engine-side seams (provider registry, payload accessors, operator index,
publication) are units in `internal/engine`.

```sh
make check RUN='^Test(Catalog(Read|StateWrites|CopiesStay|DescriptorsVisit|BulkLoad)|Candidate|BuiltinRoutineResultInventory)'
make check RACE=1 RUN='^Test(CastDefinition(PairIndex|CandidateIsolation)|.*PrincipalRemap|OperatorClass(Catalog|Value))'
make check RUN='^Test(CatalogAllocatorAndProviderBoundaries|CatalogProviderRegistersBeforeInitialization|CatalogPayload|UserOperatorIndex)'
```
