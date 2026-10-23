# internal/engine/catalog

The catalog object model and its immutable snapshots: descriptors, `State`,
`Read`, the provider registry, validation of the pure core kinds and the
copy-on-write overlay `Candidate`. "Catalog" here means the data, not DDL.

## Imports

Layer L3 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `ids`, `expr`, `authorization`, `largeobject`, `proc`,
`internal/util`, `sqltype`, `parser`, `sqlstate` and the standard library, never
the engine or its sibling `functions`. The lower `proc` package owns immutable
SQL definitions and private frames, not catalog authority.

## What it owns

- **Descriptors.** `Descriptor` is the identity contract (`Address`, `Revision`,
  `Namespace`, `Name`, `Kind`). The kinds are `Namespace`, `Relation`,
  `Database`, `Extension`, `Procedure`, `Operator`, `Parameter`, `Tablespace`,
  `LargeObject`, `Type`, `Collation`, `Conversion`, `TSTemplate`, `TSDictionary`
  and `ForeignObject` (wrappers, servers, user mappings), plus access methods,
  casts, operator classes and families. Their records (columns, constraints,
  defaults, generation, identity, indexes, partitions, inheritance, policies,
  sequence options, foreign tables, default ACL records, ...) live here too.
  Identity fields are `ObjectAddress`, `ObjectNamespace`, `ObjectName` and
  `ObjectRevision`; read them through the `Descriptor` methods.
- **Domain plan stamps.** Domain/companion `Type.DomainPlanStamp` is an analysis
  stamp, not logical identity: `UsesDomainPlanStamp` excludes unrelated arrays,
  and zero falls back to descriptor revision. `NormalizeTypeReplacement` is the
  canonical rule used by staged replacement and ACL candidate installation:
  revised ordinary mutations invalidate; explicit pure identity edits preserve;
  same-revision generation refresh retains its normalized private predecessor.
  Both carrier and domain element are prepared dependencies; preserving a pure
  array rename stamp never hides executable or authority changes on its element.
- **Relation layout.** `Relation` keeps identity, kind, owner, comment and the
  live column shape (`Columns`, `Index`, `Attributes`, `RowType`, `OfType`) at
  the top level, then four sub-records: `ColumnMeta` (`ColumnMetadata`:
  attribute high-water mark, dropped attributes, catalog attribute facts, column
  comments, defaults, generations, identities), `Constraints`
  (`RelationConstraints`: NOT NULL, physical NOT NULL, check, unique, exclusion,
  foreign keys), `Security` (`RelationSecurity`: ACL, column ACLs, policies, RLS
  flags) and `History` (`PublicationHistory`: sticky publication facts). The
  kind payloads (`SQLIndex`, `Partition`, `Membership`, `Inheritance`,
  `Sequence`, `View`, `Rules`, `Foreign`, ...) stay top-level. Sub-records are
  values, never pointers, so copying a `Relation` shares their slices and maps
  exactly like its top-level ones.
- **Partition constraint origins.** CHECK and NOT NULL descriptors carry an
  optional `PartitionOrigin` using the `InheritedConstraint`/`InheritanceOrigin`
  records. Nil means local with no contribution; a present record carries exact
  locality and at most one immediate partition parent, actual parent constraint
  OID and (for NOT NULL) stable parent attribute. It is not ordinary inheritance
  topology or a same-name inference. Candidate edits detach the record/slices
  with `CloneConstraintOrigin`; remapping is parent-qualified, never a global
  OID map.
- **Payload interfaces.** Engine-defined payloads stay behind interfaces that
  expose only what catalog code and the packages above it need: `ViewPayload`,
  `RulesPayload`, `PolicyPayload` and `ExtensionImplementation`. Expression
  payloads hold `expr.Expr`. The readers `RulesPayload.Len`/`Visit` (ordered
  `RuleFact`s: OID, name, event code, INSTEAD, references),
  `ViewPayload.Options`, `ViewPayload.Dependencies` (the frozen dependency
  manifest that `exec` routine admission reads for a grafted recipe) and
  `PolicyPayload.VisitExpression` serve `analyze`, `pgcatalog` and the
  object-identity helpers; the nil-safe `Relation.HasRules`,
  `Relation.VisitRules` and `PolicyDefinition.VisitExpressions` (USING, then
  WITH CHECK) are the usual entry points. Visitors stop at the first error. The
  checked accessors (`stored.ViewOf`, `stored.RulesOf`, `stored.PolicyOf`,
  `extension.PgcryptoProviderOf`, ...) recover the concrete types.
- **State and Read.** `State` is one catalog snapshot: the epoch, namespace
  generation, descriptor and name maps, operator/procedure name indexes, default
  ACL records, dump priorities and the replacement-only translations. `Read` is
  a view of a state with its `Owner`. The fields are private; there is no other
  access to the maps.
- **Read API** (`read.go`). Typed accessors defined once on `*State` and
  delegated by `Read`: `Kind(a)` is the comma-ok type assertion and
  `MustKind(a)` the single-value one, which panics like a failed assertion.
  `Get`/`Find`, `NameAddress`/`LookupName`, the `Descriptors` and `Names`
  iterators and the counts are the map indexes. None guards a nil state;
  `Descriptor`, `Lookup`, `LookupType` and `LookupArray` keep a nil guard.
  `state_access.go` reads the other fields (`Epoch`, `DefaultACL`,
  `DumpPriority`, ...).
- **Write API** (`state_write.go`, setters in `state_access.go`). Only for a
  state that is still being built: the bootstrap seed, a clone (`CloneState`), a
  candidate's merged state or a replacement before publication. `NewState` and
  `NewRead` construct.
- **Storage** (`state_maps.go`). Descriptors and name bindings live in
  persistent maps (`util.PersistentMap`). `CloneState` and a plain struct copy
  share both maps in O(1); `Candidate.State` applies only the candidate's
  changes, so a stage read or a publication costs the size of the change, not of
  the catalog. Writing to a copy never changes the original, and
  `Descriptors`/`Names` iterate the version current when they are called, so a
  loop may write to the state it iterates and still visits every original entry
  once. Writers that set most entries (the bootstrap seed, whole-catalog
  rewrites such as `RemapPrincipals` and the replacement remap) bracket the work
  with `BeginBulkLoad`/`EndBulkLoad`: entries live in Go maps meanwhile and the
  persistent maps are built in one pass at the end. A state in a bulk load is
  private to its writer; copying it panics. The name-index edits copy their
  bucket maps with `maps.Clone` on first change.
- **Owner and providers** (`owner.go`). `Owner` is the identity every `Read` of
  one engine carries: the provenance `ids.Domain`, the principal domain and ACL
  authority that stored values must belong to, the `Provider` registry keyed by
  class OID and the extension implementations. Its address identifies the
  engine's catalog.
- **Core-kind validation.** `NamespaceProvider`, `DatabaseProvider`,
  `TablespaceProvider`, `CollationProvider`, `ConversionProvider` and
  `ForeignCatalogProvider` validate their kinds against a `Read`, with the
  builtin collation, conversion and tablespace inventories they check.
  `LargeObjectProvider` validates installed identity, owner provenance and
  sparse payload bounds.
- **Candidate** (`candidate.go`). The pure copy-on-write overlay of a catalog
  change over an immutable base `Read`: created and replaced descriptors and
  their names, touched name slots, removals, vacated names and default ACL
  changes. All seven backing maps are private; `CatalogStage` embeds the same
  overlay without gaining writable map access. `Create` and `Replace` retain
  name/conflict and sticky relation-history checks. `UpdateDescriptor`
  completes caller-owned private copies without replaying those operations;
  `RenameStagedDescriptor` preserves actual former/new keys for reminted
  objects. `RecordRemoval` retains the original base reference and retires
  rebound private descriptors/bindings; `ForgetDescriptor` deletes only a
  private descriptor. `LookupName` resolves the candidate, while `Get`/`Find`,
  `NameBinding`, observations, counts and iterators expose **overlay entries
  only**. Reads do not merge, clone, scan the base or return writable maps.
  Iterators share immutable payloads and require a candidate read phase, not
  concurrent mutation. `State` explicitly merges into a new unpublished state
  without changing Base. `ObserveDefault` records original Base input once,
  including observed absence; `SetDefaultACL` stages output separately, with
  present nil meaning removal. Delta-only TEMP cleanup does not create a read
  observation. `ApplyDefaultACLs` projects onto the current publication source;
  `DefaultRecordsUnchanged` checks original inputs without exporting their map.
- **Name keys and operator index.** `DescriptorNameKey`, `OperatorNameIndex`,
  `OperatorIndexEdit` and `RebuildOperatorNames`; default ACL helpers
  (`OverlayDefaultACLs`, `PruneNamespaceDefaults`, `RemapDefaultSchemas`,
  `ValidateDefaultACLs`, ...).
- **Access methods** (`access_method.go`) are global immutable class 2601
  descriptors. `Read.AccessMethod`, `LookupAccessMethod` and `AccessMethods` are
  the sole live authority. Finite native handler selectors bind heap versus
  index operation authorities; bootstrap facts serve seeding/pinning only.
  Validation requires an actual installed and validated Procedure capability,
  never lookup-only handler signature metadata.
- **Operator classes, families and members** (`operator_family*.go`,
  `operator_class*.go`). Operator classes are immutable class 2616 descriptors
  whose name keys include the exact method OID; namespace-independent default
  keys participate in candidate fences, and compiled facts are bootstrap inputs
  only. Family/amop/amproc descriptors use namespace+method family names and
  typed member keys. `Read.OperatorFamily`, `Read.FamilyOperator` and
  `Read.FamilySupport` read the same descriptor/name authority as Candidate
  publication. The compiled seeds share immutable member payloads and name keys
  between engines. Insertion-time hard/loose dependencies and initdb pinning are
  separate facts; metadata-only routine result facts never grant execution.
- **Cast pairs** (`cast.go`, `cast_coercion.go`). Immutable class 2605
  declarations and the ordered-pair index, with no owner/ACL/namespace. Snapshot
  clones share unchanged indexes; candidate overlays, removals, Reset and
  replacement remaps keep COW isolation. `CastDependencies` is the common
  pg_depend/DROP/dump authority, including binary signature adapters. Fixed
  sqltype facts are protected virtual rows rather than mutable descriptors. Pair
  reads allocate nothing. `SelectCoercion` and `CanCoerce` are the effect-free
  coercion authority shared by the analyzer and routine feasibility probes:
  declared direct pairs take precedence and a context-ineligible pair blocks
  generic fallback; there is no transitive graph search. Domain/base and array
  recursion retain outer typmod/collation on element usages. Refusals use shared
  SQLSTATE errors without source positions. Binding and semantic IR stay in
  `analyze`/`plan`.
- **Aggregate binary recipes.** Declared implicit-binary aggregate adapters
  retain immutable `AggregateBinaryRecipe` evidence independently of the
  originating cast row. `AggregateConversion` captures supported physical datum
  operations from that evidence; `AggregateCanCoerce` supplies the same edges to
  retained polymorphic specialization, with catalog-free fixed feasibility as
  fallback. A retained owner with no recipe is fixed-only and never borrows
  current cast rows; dropping or replacing the origin cast selects no new
  conversion.
- **Derived procedure membership** (`procedure_index.go`). `ProcedureNameIndex`
  maps namespace/decoded name to descriptor addresses and carries a dynamic-name
  count for SQL routines and owned aggregates on the negative-call fast path. It
  is not callable or ACL authority. `ProcedureIndexEdit` follows the operator
  COW pattern: detach the membership map and touched buckets once;
  metadata-only replacements share unchanged membership. Both seeded indexes
  are shared read-only by every engine in the process, so an index is never
  written in place. Writers apply edits on publication/private overlays;
  `Candidate.State` includes direct replacements and removals. Reset clears the
  index, and wholesale import/remapping rebuilds it before expression binders
  run. Binder definition readers enumerate the indexed base bucket plus only
  real staged deltas; ordinary, aggregate-spelled and function-cast-spelled
  calls never scan the full base descriptor inventory to discover routine
  candidates. Owned aggregate procedure/state/support dependencies publish
  atomically with the derived membership.
- **Builtin routine identities** (`builtin_routine_identities.go`,
  `regproc_lookup.go`). The released `pg_proc` identities are lookup-only
  `RoutineIdentity` facts: `BuiltinRoutineIdentities` returns a copy,
  `BuiltinRoutineIdentityByOID`, `BuiltinRoutineIdentitiesNamed` (a copy) and
  `BuiltinRoutineIdentityBySignature` look them up. The same private rows and
  OID index own the result OIDs, exposed by `BuiltinRoutineResultByOID` for
  family signature admission; unsupported result types stay numeric metadata.
  These are not descriptors, providers or SQL `pg_proc` rows and never grant
  execution. There is no family-specific result allowlist.
- **Extension versions.** Extension implementations expose detached base-version
  and directed script inventories. `ExtensionVersions` owns filename-safety
  validation, shortest directed paths and base-reachable installability; graph
  knowledge alone is not install support. Path search walks declared outgoing
  edges directly, without rebuilding or sorting the vertex set per pair.
  Registration validates executable coverage separately, and typed update
  staging stays in the engine.
- **Diagnostic names** (`diagnostic_names.go`). `QuoteIdentifier` always quotes;
  `Identifier` quotes only when the supported grammar needs it
  (`parser.IdentifierNeedsQuotes`; deparse and dump spellings use it);
  `DependencyIdentifier` quotes like PostgreSQL `quote_identifier` (every keyword
  except unreserved ones, independent of the parser's grammar);
  `TypeDiagnosticNameAt` names a type bare when the search path finds it first
  and schema-qualified otherwise, and `TypeDiagnosticNamer` freezes that naming
  over a catalog read and a reference path for input environments;
  `ObjectNameText` (`object_names.go`) joins a parsed name's parts. The engine
  and `access` share them.
- **Definition reader** (`definition_reader.go`). `DefinitionReader` is the
  sparse read that definition cooking uses to see a statement's unpublished,
  ordered DROP effects (descriptors, name lookup, procedures by name, SQL
  procedure names). It is not a catalog generation, an execution route or a
  permission cache, and a reader is never persisted. The engine implements it
  over a catalog stage, inheritance preparation and prepared drops.
  `DescriptorAt` reads through a reader or the catalog, and
  `definition_reads.go` adds the typed reads (`DefinitionTypeAt`,
  `DefinitionArrayAt`, `DefinitionNamespaceAt`, `DefinitionRelationAt`,
  `DefinitionLookupAt`, `DefinitionProceduresNamedAt`,
  `IndexedDefinitionProcedures`) that the binder and engine DDL share.
- **Pure record helpers** shared by DDL, query admission and restore: the
  hierarchy reads `InheritanceChildren`, `InheritanceHasChildren`,
  `InheritanceParticipant`, `CloneInheritance`, `InheritanceQueryLayout`,
  `HierarchyRelation` and `SamePartitionColumnUsage` (`inheritance.go`);
  constraint naming `ConstraintNames`, `RelationConstraintNameExists` and
  `GeneratedConstraintName` (`constraint_names.go`);
  `RelationDefiningAuthorityCompatible` (`relation_compare.go`);
  `PhysicalPrimaryForAttribute` and `CapturePhysicalPrimarySupport`
  (`physical_primary_support.go`); `ValidateTablespaceLocation` and
  `ValidateTablespaceName` (`tablespace_validate.go`, with the private
  `sqlError` copy in `sql_error.go`); `ExtensionMembership` and
  `NamespaceChildren`; `InstallParameterGrant`; the definition-OID checks
  `DefinitionOIDUsed`, `ReserveDefinitionOIDs` and `ValidateDefinitionOIDs`
  (`definition_identity.go`); `ForeignLabel` (the SQL/MED object kind in
  diagnostics, `foreign_catalog.go`); `PartitionKeyDependsOn` (`partition.go`),
  which DROP shares with `fdw` and `partition`; `AdvanceRevisionFrontier`
  (`revision_frontier.go`).
- **Relation history** (`relation_history.go`). `VisitSubclassParents` visits a
  relation's owned subclass edges (inheritance parents, partition membership, an
  attached index's partition parent). `NormalizeReplacementSubclassHistory` and
  `NormalizeReplacementIndexHistory` set the sticky `HasSubclasses` and
  `HasIndexes` flags in a replacement candidate's private state without mutating
  retained source descriptors or clearing a true flag; only VACUUM's separate
  publication may clear `HasIndexes`, after confirming no attached indexes. The
  stage markers that allocate revisions stay in the engine. `SharedRelation`
  centralizes shared catalog identities for both pg_class and maintenance
  authorization.
- **Principal remap** (`principal_remap.go`). `RemapPrincipals` rewrites the
  owners, ACLs, policy roles and default ACL records of an unpublished private
  candidate through a `PrincipalRemap` (principal and seeded-owner mappers, an
  operator-seeded predicate). The engine builds the mappers and validates the
  default ACLs afterwards. Access methods, casts and operator-family members are
  ownerless: remapping retains them unchanged, and their object/handler/endpoint
  identities go through the separate typed address remapper. Unknown descriptor
  kinds fail. Shared dependency type labels use SQL spellings (`integer`, not
  `int4`) for builtin aliases and path-sensitive qualification for custom types.
- **reg\* alias references** (`alias_references.go`): `AliasAddress` and the
  walkers `WalkAliasReferences(At)` and `WalkAliasDependencies(At)` visit the
  objects a reg\* value names, for dependency recording and replay.

## Invariants

- A published `State` is immutable. Writers use the write API only on states
  they are building; publication swaps the whole state.
- Structural sharing: clones and merged states share the persistent descriptor
  and name maps and the rest (default ACL records, operator/procedure indexes)
  until they change; a change copies only what it touches. No deep copies on
  publication or capture.
- The package has no mutex. The engine mutex guards the published state and
  every unpublished state the engine shares between goroutines.
- Provenance: a `Read` belongs to the engine whose `Owner` it carries;
  validators compare domains through the owner, never through the engine.
- Identity is never inferred from names or numbers. Relation identities, sparse
  stable attributes and dense evaluation slots are distinct: compaction never
  renumbers surviving attributes and a relation never reuses a retired
  attribute number. Removal/recreation and Reset/Restore fence the old address
  instead of retargeting by name or numeric OID; numeric inequality is not a
  replacement proof, since allocators can reuse numbers.
- `DefaultACLRecord` owns its nonzero, unique persistent row OID together with
  its immutable ACL. `DefaultACLKey` is the creator/schema/class unique key.
  Validation admits only the six implemented default classes and real
  creator/namespace/ACL authority. The engine's sole catalog allocator assigns
  identities only to final new policies; updates/remaps/recipient cleanup
  preserve them, baseline deletion removes them, and reads never allocate.
- Revision frontiers are pure and saturating (`AdvanceRevisionFrontier`); the
  engine's store commits the speculative scalar under `Engine.mu` only at
  successful installation, separately from catalog payloads and live sequence
  state. Descriptor-local ALTER moves use the same calculation without
  reserving the global allocator.
- Large objects use class 2995-local OIDs: equal relation/type/TEMP numbers are
  legal. Their namespace is empty and the synthetic name is the decimal OID.
  Owner/raw ACL and immutable content share one descriptor, journal publication
  and principal remap; role dependency collection reuses `access.DescriptorACL`,
  including every raw grantor/grantee.

## What stays in the engine

DDL commands, `CatalogStage` and its publication coordination (storage row
shapes, index candidates, domain validation, materialized replacement, role
candidates), publication itself, the `catalogStore` allocators, seeding, the
transaction catalog journal, parameter validation (`parameterProvider`, over the
settings registry) and the registration of the other providers, which live in
their packages (relations `table.RelationProvider`, whose validator slots the
engine fills from `view` and `partition`; types `stored.TypeProvider`;
procedures `stored.ProcedureProvider`; extensions `extension.CatalogProvider`;
operators `routine.OperatorProvider`; the physical NOT NULL validators are
`table` code, their origin and primary-support checks and
`SameRelationRowShape` are [`index`](../index/README.md) code). Also
definition-OID assignment (the Restore remap is `control.RemapDefinitionOIDs`),
the restore principal mapper and the `DefinitionReader` implementations
(stored-SQL rendering, expression nodes, the operator inventory, settings
definitions, extension implementations). Callers name the declarations here
directly (`catalog.Relation`, `catalog.Read`, ...), without aliases.

## Testing

Package units cover the read/write API, persistent storage and bulk loads,
Candidate ownership, cast pair indexes, operator class identity, principal and
address remaps, diagnostic names, tablespace validators and the builtin routine
inventory. `testdata/builtin_routine_results_pg18_6.tsv` is the independent
record of every builtin routine identity and its result, extracted from the
pinned PostgreSQL 18.6 oracle; `TestBuiltinRoutineResultInventory` checks it.
Engine-side seams (provider registry, payload accessors, operator index,
publication, revision frontiers) are engine units.

```sh
make check RUN='^Test(Catalog(Read|StateWrites|CopiesStay|DescriptorsVisit|BulkLoad)|Candidate|BuiltinSeedNames)'
make check RACE=1 RUN='^Test(CastDefinition(PairIndex|CandidateIsolation)|LargeObject|.*PrincipalRemap|OperatorClass(Catalog|Value))'
make check RUN='^Test(DependencyIdentifier|IdentifierQuotes|TypeDiagnosticNameAt|Tablespace|BuiltinRoutineResultInventory)'
make check RUN='^Test(CatalogAllocatorAndProviderBoundaries|CatalogProviderRegistersBeforeInitialization|CatalogPayload|UserOperatorIndex|ExtensionVersion)'
```
