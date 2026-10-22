# internal/engine/catalog

The catalog object model and its immutable snapshots: descriptors, `State`,
`Read`, the provider registry, validation of the pure core kinds and the
copy-on-write overlay `Candidate`. "Catalog" here means the data, not DDL.
The package sits at layer L3 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `ids`, `expr`, `authorization`, `proc`, `sqltype`, `parser`,
`sqlstate` and the standard library, and never the engine or its sibling `functions`. The lower
`proc` package owns immutable SQL definitions and private frames, not catalog authority.

## What it owns

- **Descriptors.** `Descriptor` is the identity contract (`Address`,
  `Revision`, `Namespace`, `Name`, `Kind`). The kinds are `Namespace`,
  `Relation`, `Database`, `Extension`, `Procedure`, `Operator`, `Parameter`,
  `Tablespace`, `LargeObject`, `Type`, `Collation`, `Conversion`, `TSTemplate`,
  `TSDictionary` and `ForeignObject` (wrappers, servers, user mappings). Their
  records (columns, constraints, defaults, generation, identity, indexes,
  partitions, inheritance, policies, sequence options, foreign tables,
  default ACL records, ...) live here too. Identity fields are
  `ObjectAddress`, `ObjectNamespace`, `ObjectName` and `ObjectRevision`; read
  them through the `Descriptor` methods.
  Domain/companion `Type.DomainPlanStamp` is an analysis stamp, not logical
  identity: `UsesDomainPlanStamp` excludes unrelated arrays, and zero falls back
  to descriptor revision. `NormalizeTypeReplacement` is the
  canonical rule used by staged replacement and ACL candidate installation.
  Revised ordinary mutations invalidate; explicit pure identity edits preserve;
  same-revision generation refresh retains its normalized private predecessor.
  Both carrier and domain element are prepared dependencies; preserving a pure
  array rename stamp never hides executable or authority changes on its element.
- **Relation layout.** `Relation` keeps identity, kind, owner, comment and
  the live column shape (`Columns`, `Index`, `Attributes`, `RowType`,
  `OfType`) at the top level, then four sub-records: `ColumnMeta`
  (`ColumnMetadata`: attribute high-water mark, dropped attributes, catalog
  attribute facts, column comments, defaults, generations, identities),
  `Constraints` (`RelationConstraints`: NOT NULL, physical NOT NULL, check,
  unique, exclusion, foreign keys), `Security` (`RelationSecurity`: ACL,
  column ACLs, policies, RLS flags) and `History` (`PublicationHistory`:
  sticky publication facts). The kind payloads (`SQLIndex`, `Partition`,
  `Membership`, `Inheritance`, `Sequence`, `View`, `Rules`, `Foreign`, ...)
  stay top-level. Sub-records are values, never pointers, so copying a
  `Relation` shares their slices and maps exactly like its top-level ones.
- **Payload interfaces.** Engine-defined payloads stay behind interfaces
  that expose only what catalog code and the packages above it need:
  `ViewPayload`, `RulesPayload`, `PolicyPayload` and
  `ExtensionImplementation`. Expression payloads hold `expr.Expr`. The
  readers `RulesPayload.Len`/`Visit` (ordered `RuleFact`s: OID, name, event
  code, INSTEAD, references), `ViewPayload.Options`,
  `ViewPayload.Dependencies` (the frozen dependency manifest that `exec`
  routine admission reads for a grafted recipe) and
  `PolicyPayload.VisitExpression` serve `analyze`, `pgcatalog` and the
  object-identity helpers; the nil-safe `Relation.HasRules`,
  `Relation.VisitRules` and `PolicyDefinition.VisitExpressions` (USING, then
  WITH CHECK) are the usual entry points. Visitors stop at the first error.
  The checked accessors (`stored.ViewOf`, `stored.RulesOf`, `stored.PolicyOf`,
  `extension.PgcryptoProviderOf`, ...) recover the concrete types.
- **State and Read.** `State` is one catalog snapshot: the epoch, namespace
  generation, descriptor and name maps, operator/procedure name indexes, default ACL
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
  publication. `NewState`, `NewStateSized` (a seed of known size) and
  `NewRead` construct. `CloneState`, `Candidate.State` and the name-index
  edits copy whole maps with `maps.Clone`, never by re-inserting entries:
  every publication copies the full catalog, built-ins included.
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
  changes. All seven backing maps are private; `CatalogStage` embeds the same
  overlay without gaining writable map access. `Create` and `Replace` retain
  name/conflict and sticky relation-history checks. `UpdateDescriptor` completes
  caller-owned private copies without replaying those operations;
  `RenameStagedDescriptor` preserves actual former/new keys for reminted objects.
  `RecordRemoval` retains the original base reference and retires rebound private
  descriptors/bindings; `ForgetDescriptor` deletes only a private descriptor.
  `LookupName` resolves the candidate, while `Get`/`Find`, `NameBinding`,
  observations, counts and iterators expose **overlay entries only**. Reads do
  not merge, clone, scan the base or return writable maps. Iterators share
  immutable payloads and require a candidate read phase, not concurrent mutation.
  `State` explicitly merges into a new unpublished state without changing Base.
  `ObserveDefault` records original Base input once, including observed absence;
  `SetDefaultACL` stages output separately, with present nil meaning removal.
  Delta-only TEMP cleanup does not create a read observation. `ApplyDefaultACLs`
  projects onto the current publication source; `DefaultRecordsUnchanged` checks
  original inputs without exporting their map.
- **Name keys and operator index.** `DescriptorNameKey`, `OperatorNameIndex`,
  `OperatorIndexEdit` and `RebuildOperatorNames`; default ACL helpers
  (`OverlayDefaultACLs`, `PruneNamespaceDefaults`, `RemapDefaultSchemas`,
  `ValidateDefaultACLs`, ...).
- **Derived procedure membership** (`procedure_index.go`). `ProcedureNameIndex`
  maps namespace/decoded name to descriptor addresses and carries a SQL-name
  count for the negative-call fast path. It is not callable or ACL authority.
  `ProcedureIndexEdit` follows the existing operator COW precedent: detach the
  membership map and touched buckets once; metadata-only replacements share
  unchanged membership. Both seeded indexes are shared read-only by every
  engine in the process, so an index is never written in place. Writers apply edits on publication/private overlays;
  `Candidate.State` includes direct replacements and removals. Reset clears the
  index, and wholesale import/remapping rebuilds it before expression binders run.
  Binder definition readers enumerate the indexed base bucket plus only real
  staged deltas, resolving candidates through current descriptor/removal owners.
  The shared SQL-name gate includes staged additions and removals; ordinary,
  aggregate-spelled and function-cast-spelled calls never scan the full base
  descriptor inventory merely to discover routine candidates.
- **Builtin routine identities** (`builtin_routine_identities.go`,
  `regproc_lookup.go`). The released REL_18_0 `pg_proc` identities as
  lookup-only `RoutineIdentity` facts: `BuiltinRoutineIdentities` returns a
  copy, `BuiltinRoutineIdentityByOID`, `BuiltinRoutineIdentitiesNamed` (a
  copy) and `BuiltinRoutineIdentityBySignature` look them up. They are not
  descriptors and never grant execution; the engine's regproc lookup and the
  binder's unavailable-function diagnostic read them.
- **Diagnostic names** (`diagnostic_names.go`). `QuoteIdentifier` always
  quotes; `Identifier` quotes only when the supported grammar needs it
  (`parser.IdentifierNeedsQuotes`; deparse and dump spellings use it);
  `DependencyIdentifier` quotes like PostgreSQL 18
  `quote_identifier` (every keyword except unreserved ones, independent of
  the parser's smaller grammar); `TypeDiagnosticNameAt` names a type bare
  when the search path finds it first and schema-qualified otherwise;
  `ObjectNameText` (`object_names.go`) joins a parsed name's parts. The
  engine and `access` share them.
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
- **Pure record helpers** (5i). Helpers that read only catalog records,
  shared by DDL, query admission and restore: the hierarchy reads
  `InheritanceChildren`, `InheritanceHasChildren`, `InheritanceParticipant`,
  `CloneInheritance`, `InheritanceQueryLayout`, `HierarchyRelation` and
  `SamePartitionColumnUsage` (`inheritance.go`); constraint naming
  `ConstraintNames`, `RelationConstraintNameExists` and
  `GeneratedConstraintName` (`constraint_names.go`);
  `RelationDefiningAuthorityCompatible` (`relation_compare.go`);
  `PhysicalPrimaryForAttribute` and `CapturePhysicalPrimarySupport`
  (`physical_primary_support.go`); `ValidateTablespaceLocation` and
  `ValidateTablespaceName` (`tablespace_validate.go`, with the private
  `sqlError` copy in `sql_error.go`); `ExtensionMembership` and
  `NamespaceChildren`; `InstallParameterGrant`; the definition-OID checks
  `DefinitionOIDUsed`, `ReserveDefinitionOIDs` and `ValidateDefinitionOIDs`
  (`definition_identity.go`).
- **Principal remap** (`principal_remap.go`). `RemapPrincipals` rewrites the
  owners, ACLs, policy roles and default ACL records of an unpublished
  private candidate through a `PrincipalRemap` (principal and seeded-owner
  mappers, an operator-seeded predicate). The engine builds the mappers and
  validates the default ACLs afterwards.
- **reg\* alias references** (`alias_references.go`): `AliasAddress` and the
  walkers `WalkAliasReferences(At)` and `WalkAliasDependencies(At)` visit the
  existing objects a reg* value names, for dependency recording and replay.

## Invariants

- A published `State` is immutable. Writers use the write API only on states
  they are building; publication swaps the whole state.
- Copy-on-write sharing: clones and merged states copy the descriptor and
  name maps and share the rest (default ACL records, operator/procedure indexes) until
  they change. No deep copies on publication or capture.
- The package has no mutex. The engine mutex guards the published state and
  every unpublished state the engine shares between goroutines.
- Provenance: a `Read` belongs to the engine whose `Owner` it carries;
  validators compare domains through the owner, never through the engine.
- `DefaultACLRecord` owns its nonzero, unique persistent row OID together with
  its immutable ACL. `DefaultACLKey` remains the creator/schema/class unique key.
  Validation admits only the six implemented default classes and real creator/
  namespace/ACL authority. The engine's sole catalog allocator assigns identities
  only to final new policies; updates/remaps/recipient cleanup preserve them,
  baseline deletion removes them, and reads never allocate identities.
- Revision frontier calculation remains store-owned in the engine, separate
  from catalog payloads and live sequence scalar state. It is pure and
  saturating; only successful installation commits its speculative scalar under
  `Engine.mu`. Descriptor-local ALTER moves use the same calculation without
  reserving the global allocator.
- Large objects use class2995-local OIDs: equal relation/type/TEMP numbers are
  legal. Their namespace is empty and synthetic name is the decimal OID.
  `LargeObjectProvider` validates installed identity, owner provenance and sparse
  payload bounds. Owner/raw ACL and immutable content share one descriptor,
  journal publication and principal remap. Ordinary role dependency collection
  reuses `access.DescriptorACL`, including every raw grantor/grantee.



## What stays in the engine

DDL commands, `CatalogStage` and its publication coordination (storage row
shapes, index candidates, domain validation, materialized replacement, role
candidates), publication itself, the `catalogStore` allocators, seeding, the
transaction catalog journal, and the validators that need engine services:
extension, procedure, operator and parameter validation (relation and type
validation are clean and move to `table` 24e and `stored` 22e),
the physical NOT NULL validators (their origin and primary-support checks
and `SameRelationRowShape` are [`index`](../index/README.md) code), definition-OID
assignment and remapping (the remap moves to `control` in 30e), the
restore principal mapper, the
`DefinitionReader` implementations
(stored-SQL rendering, expression nodes, the operator inventory, settings
definitions, extension implementations). Those register through the same
`Provider` registry. The engine names the moved declarations directly
(`catalog.Relation`, `catalog.Read`, ...); step 5f removed the aliases.

## Testing

Follow the [shared testing guide](../../../docs/TESTING.md). Run from the
repository root.

Extension implementations expose detached explicit base-version and directed
script inventories. `ExtensionVersions` owns filename-safety validation,
shortest directed paths and base-reachable installability; graph knowledge
alone is not install support. Engine-side registration validates executable
coverage separately, and typed update staging remains in the engine.
Path search walks the declared outgoing edges directly; it does not rebuild
or sort the vertex set for each source/target pair. Pair projection owns one
sorted vertex list, while declared-edge tie order is internal only.
The owning graph/manifest/publication proof is
`make check RUN='^TestExtensionVersion(Graph|Manifest|Publication|PathsConsumers)$'` with an
external900-second deadline; public dual-mode proof is documented in
`reference/catalogs/README.md#extension-version-selected-proof`.


- Read and write API: `make check RUN='^TestCatalog(ReadTypedAccessors|ReadUntypedAndNameAccessors|ReadAccessorsRequirePopulatedRead|StateWritesStayOnTheirState)$'`.
- Diagnostic names: `make check RUN='^Test(DependencyIdentifierQuotesLikeQuoteIdentifier|IdentifierQuotesOnlyWhenNeeded|TypeDiagnosticNameAtQualifiesHiddenTypes)$'`.
- Engine-side catalog seams (registry, payload accessors and readers,
  operator index, publication): `make check RUN='^Test(CatalogAllocatorAndProviderBoundaries|CatalogProviderRegistersBeforeInitialization|CatalogPayload(Accessors|Readers)|UserOperatorIndex)'`.
- Descriptor payload readers: `make check RUN='^TestPayloadReadersOnDescriptors$'`.
- Candidate ownership: `make check RUN='^TestCandidate(PrivateUpdates|RenameRemovalReuse|DefaultObservations|RetainedState)$'`.
- Revision frontiers: `make check RUN='^Test(CatalogStoreRevisionFrontier|AlterDropColumnRevisionFrontier(Failures|Terminal)?|AlterSchema(WholeCandidate|OverflowAndSkip))$'`.
- Retained policy/index authority: `make check RUN='^Test(DefaultPrivileges(CloneStage|RetainedMessage|SchemaLifecycle|CreationFence)|SQLRoutineStagedLookup|CatalogCopiesStayIndependent|UserOperatorIndex(CopyOnWrite|PublicationAndOverlay)|BuiltinProcedureNamesCopyOnEdit)$'`.
- Tablespace validators: `make check RUN='^TestTablespace(NameValidation|CanonicalPaths)$'`.
