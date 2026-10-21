# Engine split, step 20: `internal/engine/ddl`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-ddl.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 20; §3 (layer L8,
  rules 6 and 7); §4.1 patterns 3 and 4; §7 decisions 8 and 16.
- **Needs:** 20a (linear item 60): —. 20b (item 61): 20a. 20c (item 62):
  20b. 20d (item 63): 20c. Later Part B refines (23c, 24c, 26c, 27c, 28c:
  items 84, 88, 96, 101, 105) build on 20b.
- **Effort:** high for 20a and 20b (mechanical, but three packages and the
  root change together); high for 20c (docs only); high for 20d.
- **Session size:** one sub-step per session.
- **Linear items:** 20a = 60, 20b = 61, 20c = 62, 20d = 63.
- **Tools:** `reach` (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach
  -dir . -files '<regexp>'`) and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir .
  -files a.go,b.go [-decls X,Y]`), run from the worktree root; never pipe
  them into `head`. Interface renames: `ifacern` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-SEAM-CUTS/`; qualification and
  alias removal: `movex`, `unalias`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`.

## Goal

Every package that stages catalog changes needs the same few things from
the engine: an opaque statement authority, a catalog stage, a creation
namespace, OID and revision allocation, notices. Today `routine`, `typedef`
and `extension` each declare their own `Authority` and `Stage` with nearly
the same methods, and the root keeps two adapters with the same fields
(`schemaHost` and `typedefHost`, a defined type over it). Every later
feature would add one more copy.

`ddl` holds that contract once: `ddl.Authority`, `ddl.Stage`, the common
`ddl.Host`, and the small helpers built on them. The feature hosts embed it
and keep only their own methods; the root keeps one staging adapter. Then
`ddl` takes the schema-level DDL that has no other owner: the decisions of
CREATE/ALTER/DROP SCHEMA and CREATE TABLESPACE.

`ddl` sits at L8: above `session` (it names `session.NoticeLevel`), below
the lower features that embed it. It imports `access`, `authorization`,
`catalog`, `ids`, `parser`, `session`, `sqlstate`, `sqltype` and the
standard library. It never imports `analyze`, a feature package or the
root.

## Inventory (measured at `c154e14fb`)

### The three contracts today

- `routine/host.go`: `Authority` (`access.Environment` plus the marker
  `RoutineAuthority()`), `Stage` (`Create`, `Replace`, `UpdateDescriptor`,
  `Get`, `LookupName`) and `DDLHost` (21 methods). Root adapter:
  `schemaHost` (`schema_host.go`, 151 lines; fields `e`, `op`, `stage`).
- `typedef/stage_host.go`: `Authority` (`access.Environment` plus
  `TypedefAuthority()`), `Stage` (`Create`, `Replace`, `Get`, `LookupName`)
  and `StageHost` (32 methods), plus the helpers `check`, `objectRef` and
  `notice`. Root adapter: `typedefHost` (`typedef_host.go`, 192 lines),
  declared as `type typedefHost schemaHost` because same-named methods take
  each package's own `Authority` and `Stage` (15d).
- `extension/host.go` and `extension/stage.go`: `Authority` (`Check` and
  `Effective`, implemented by the root's `AuthorizationView`), `Host`
  (8 methods) and `Stage` (10 methods, provider-facing). Root adapter:
  `extensionHost` (`extension_host.go`, 103 lines; fields `engine`, `op`,
  `auth`, `stage`). It wraps the stage on purpose: never a defined type over
  `CatalogStage` (16c, §7 decision 8).
- References: `Authority` appears 30 times in 6 `routine` files, 32 times
  in 8 `typedef` files, 5 times in 4 `extension` files; the root names the
  three packages' `Authority`/`Stage` in 39 production lines. The adapters
  are constructed in `schema.go`, `schema_host.go`, `typedef_host.go`,
  `domain_alter.go`, `enum_ddl.go` and 10 test files.

### Method comparison

"—" means the package has no such method. "Same" means the signatures
agree once `Authority` and `Stage` are the `ddl` types.

| Method | `routine.DDLHost` | `typedef.StageHost` | `extension` | Proposed in `ddl` |
|---|---|---|---|---|
| `AccessView` | `(Authority) access.View` | same | handlers get `access.View` as a parameter | `Host` |
| `Recapture` | `(Authority, catalog.Read) Authority` | — | — | `Host` |
| `CreationNamespace` | `(Authority, parser.ObjectName) (ids.ObjectRef, Authority, error)` | same | `Host.CreationPath() ([]ids.ObjectAddress, error)` | `Host`; extension keeps `CreationPath` |
| `ExistingCreationNamespace` | — | `(Authority, parser.ObjectName) (ids.ObjectRef, error)` | — | `Host` |
| `CheckCreateIn` | `(Authority, ids.ObjectRef) error` | same | — | `Host` |
| `OperatorNamespaces` / `SearchNamespaces` | `OperatorNamespaces(Authority, parser.ObjectName) ([]ids.ObjectAddress, error)` | `SearchNamespaces(Authority, parser.ObjectName, ids.OID)` (types, conversions) | — | `Host.SearchNamespaces`, adding `ids.OperatorClassID`; `OperatorNamespaces` goes |
| `LookupFunction` | `(Authority, parser.ObjectName, catalog.ProcedureSignature) (*catalog.Procedure, error)` | same | `Stage.ScriptFunctions` (different) | `Host` |
| `ResolveType` | `(Authority, parser.TypeName) (ids.OID, error)` (regtype input) | `(parser.TypeName) (sqltype.Usage, error)` (DDL annotation) | — | not shared: routine's becomes `ResolveRegtype` |
| `DependencyObject` | `(Authority, ids.ObjectAddress) string` (ignores the path error) | `(Authority, ids.ObjectAddress) (string, error)` | — | `Host`, `(string, error)`; routine callers keep using the string and ignore the error, as today |
| `Stage` | `() routine.Stage` | `() typedef.Stage` | `Host.BeginStage(namespace) Stage` | `Host.Stage() ddl.Stage` |
| `StageBase` | `() catalog.Read` | — | `Stage.Read()` | `Host` |
| `StageRead` | `() catalog.Read` | same | — | `Host` |
| `CreationACL` | `(owner, schema, class) authorization.ACLValue` | — | — | `Host` |
| `CreationTypeACL` | — | `(owner, schema) (authorization.ACLValue, bool)` | `Stage`, same | `TypeStaging` |
| `DisplaceAssociatedArray`, `AssociatedArrayName` | — | yes | `Stage`, same | `TypeStaging` |
| `DropTarget` | `(ids.ObjectAddress)` | same | — | `Host` |
| `RecordDependencyPath` | `(Authority)` | same | — | `Host` |
| `AllocateOID`, `Revision` | yes | same | `Host` and `Stage`, same | `Allocator` (embedded in `Host`) |
| `EmitNotice` | `(session.NoticeLevel, sqlstate.Error)` | same | `Host.Notice(sqlstate.Error)` (NOTICE only) | `Host`; extension's `Notice` becomes a helper |
| `Context` | — | `() context.Context` | `Host`, same | `Host` |

Package-specific, staying in their package's host:
- `routine`: `DefinitionType`, `DefinitionLookup`, `SelectFunction`,
  `ResolveRegtype` (renamed), `BindSQLRoutine`.
- `typedef`: `TrustedRestoreDestination`, `AllocateEnumMember`,
  `ReplaceUnsynchronized`, `AddDomainBase`, `RemoveDomainCheck`,
  `NamedType`, `FindCollation`, `ResolveType`, `ResolveDomainBase`,
  `ResolveColumnCollation`, `BindDomainDefault`, `BindDomainChecks`,
  `NewCompositeRelation`, `StageRowType`.
- `extension`: `CreationPath`, `ReplayingTableDestination`,
  `BootstrapSuperuser`, `BeginStage`; on its `Stage`: `Read`,
  `LookupName`, `Create`, `Replace`, `ScriptFunctions`.

### The authorities

- `routine.Authority` and `typedef.Authority` are the same shape with
  different markers. The markers existed only because each package
  declared its own type; the root recovers the view with
  `routineAuthorityOf` and `typedefAuthorityOf`, which panic on a foreign
  kind.
- `extension.Authority` has `Check(access.Requirement) error` and
  `Effective(ids.ObjectRef, bool) (authorization.Privileges, error)`.
  The root implements them on `AuthorizationView` as
  `v.View.Check(v, req)` and `v.View.Effective(v, ref, grant)`
  (`authorization.go:72-75`), and every call site passes `auth.View` as the
  handler's `view` parameter (`extension_install.go:90`,
  `extension_update.go:30`, `extension_drop.go:24`). So
  `auth.Check(req)` equals `view.Check(auth, req)` exactly, and
  `auth.Effective(ref, g)` equals `view.Effective(auth, ref, g)`.
  `extension` uses them in four places (`extension_create.go:83`,
  `extension_drop.go:31`, `extension_update.go:25` and `:59`).

### Schema-level DDL (measured)

- `schema.go` (656 lines; clean only `schemaReserved`, `schemaNotice`):
  - `stageSchemaLocked` (321 lines) captures authority, begins the stage and
    dispatches namespace-object DDL to the `routine` and `typedef` handlers;
    its own cases are CREATE SCHEMA (about 60 lines, including the
    embedded `CREATE TABLE` elements), ALTER SCHEMA (about 45) and DROP
    SCHEMA (about 105), plus three closures (`databaseGate`, `lookup`,
    `own`, about 35 lines).
  - `executeSchema` (118 lines): locking, admission and publication around
    `stageSchemaLocked`.
  - `schemaTableLocked` (159 lines) and `newTableDescriptorLocked` (24):
    table code (step 24).
- DROP SCHEMA also uses `namespaceChildren` (`namespace_children.go`, 30
  clean lines), `stageSchemaDomainsLocked` (`domain_dependencies.go`, 63
  lines), `relationDiagnosticKind`/`relationDiagnosticName`,
  `CatalogStage.removeObjectLocked`, `stage.domainTargets`,
  `stage.dependencyPath` and `stage.schemaCascadeNames`.
- `tablespace.go` (82 lines): `executeCreateTablespace` (37 lines; it
  validates and then always fails with 0A000, it never stages) and the clean
  validators `tablespaceLocation`, `tablespaceName`,
  `tablespacePhysicalUnsupported`. `tablespace_catalog.go`:
  `initializeTablespaces` (seeding) and `resolveGrantTablespace` (GRANT).

### Where each piece of schema-level DDL goes

The second planning round gave schema-level DDL no package of its own
(ENGINE-SPLIT §6). This table is the map later sessions follow.

| Piece (file) | Destination | Step |
|---|---|---|
| CREATE/ALTER/DROP SCHEMA decisions (`schema.go`) | `ddl` | 20d |
| CREATE TABLESPACE decisions (`executeCreateTablespace`) | `ddl` | 20d |
| `tablespaceLocation`, `tablespaceName` (`tablespace.go`) | `catalog` (done: `ValidateTablespaceLocation`, `ValidateTablespaceName`) | 5i |
| Namespace-object dispatch (`stageSchemaLocked`'s other cases), `executeSchema` | root | — |
| `schemaTableLocked`, `newTableDescriptorLocked` (`schema.go`) | `table` | 24a rename, 24d |
| SET SCHEMA (`alter_schema.go`, `alterSchemaLocked`) | `table` | 24 |
| ALTER TABLE action preparation (`alter_tablespace.go`: `prepareAlterTableActions`, `alterTablePreparation`) | `table` | 24a |
| RENAME (`rename.go`, `renameRelationLocked`) | `table` | 24 |
| Search path (`schema_path.go`: `referencePath`, `creationNamespace`, `lookupNamespaces`) | root | — |
| COMMENT (`comment.go`, `comment_inquiry.go`) | root | — |
| Seed comments (`comment_builtin.go`: `builtinTypeComment`, `namespaceCommentBaseline`) | `catalog` or `pgcatalog` | 20d decides (5i left them) |
| DROP pipeline (`drop_provider.go`, `dropProvider`, 37 methods) | root; a `ddl` removal interface when a feature needs it | 24c |
| Object identity (`object_identity.go`, clean part of `object_address_lookup.go`) | `regobj` | 21b |
| `object_address.go`, `lookupObjectAddress` | root | — |
| Definition identity, clean part (done: `catalog.DefinitionOIDUsed`, `ReserveDefinitionOIDs`, `ValidateDefinitionOIDs`; `structuralNull` is `plan.StructuralNull`) | `catalog`, `plan` | 5i |
| `assignDefinitionOIDsLocked` | root | — |
| `remapDefinitionOIDsLocked` | `control` | 30 |
| `definition_reader.go` (`stageDefinitionReader`, the `AuthorizationView.definition*` wrappers) | root | — |
| `namespaceChildren` (`namespace_children.go`) | `catalog` (done: `NamespaceChildren`) | 5i |
| `namespace_inquiry.go`, `parameter_input.go` | root (function and protocol glue) | — |
| `builtin_catalog.go`, `initializeTablespaces` | root (seeding) | — |
| `parameter_catalog.go` (`parameterProvider`, GRANT ON PARAMETER) | root; 5i moved `installParameterGrant` to `catalog.InstallParameterGrant` | 5i |
| `resolveGrantTablespace` | `role` | 28 |

## Part A

### 20a — Create `ddl` and adopt it (linear item 60)

1. Re-measure: re-read the three host files and the three root adapters,
   and update the comparison table above if anything changed.
2. **Create `internal/engine/ddl`** with:
   - `Authority`: `access.Environment` plus the marker `DDLAuthority()`.
     It is an opaque value: a host method that changes it returns a new one
     and leaves its argument alone (copy the doc comment from
     `routine/host.go`).
   - `Stage`: `Create`, `Replace`, `UpdateDescriptor`, `Get`,
     `LookupName` (the `routine` set; `typedef`'s is a subset).
   - `Allocator`: `AllocateOID`, `Revision`.
   - `TypeStaging`: `DisplaceAssociatedArray`, `AssociatedArrayName`,
     `CreationTypeACL`.
   - `Host`: `Allocator` plus every row of the table marked `Host`. Its
     contract: every method runs with `Engine.mu` held, never takes or
     releases it and never waits on the lock manager.
   - Helpers from `typedef/stage_host.go`, generalized: `Check(h, auth,
     req)`, `ObjectRef(h, auth, a)`, `Notice(h, code, message)`.
   - Seam unit tests first, observed failing: `Check` passes the authority
     as the environment; `Notice` emits NOTICE level; a fake host proves
     `Recapture` leaves its argument unchanged.
3. **Adopt it, one package per commit:**
   - `routine`: `Authority` and `Stage` become `ddl.Authority` and
     `ddl.Stage` (delete the local types; rewrite the names with the
     rename tools); `DDLHost` embeds `ddl.Host` and keeps its own methods;
     `OperatorNamespaces` becomes `SearchNamespaces(...,
     ids.OperatorClassID)`; `ResolveType` becomes `ResolveRegtype`;
     `DependencyObject` returns `(string, error)` and its callers keep
     today's behavior (use the string, ignore the error).
   - `typedef`: the same; `StageHost` embeds `ddl.Host` and
     `ddl.TypeStaging`; its `check`, `objectRef` and `notice` helpers
     become calls to the `ddl` helpers.
   - `extension`: `Authority` becomes `ddl.Authority`. The four
     `auth.Check(req)`/`auth.Effective(ref, g)` calls become
     `view.Check(auth, req)`/`view.Effective(auth, ref, g)` (identical; see
     the inventory). `Host` embeds `ddl.Allocator` and keeps its other
     methods; `Stage` embeds `ddl.Allocator` and `ddl.TypeStaging`.
     `Host.Notice` stays as it is unless `EmitNotice` fits without changing
     the notice level; do not widen `extensionHost`.
4. **Root.** `AuthorizationView` gets one `DDLAuthority()` marker; delete
   `RoutineAuthority` and `TypedefAuthority`; one `ddlAuthorityOf` replaces
   `routineAuthorityOf` and `typedefAuthorityOf` (same panic text pattern).
   `schemaHost` and `typedefHost` now implement identical shared methods;
   keep both types for this sub-step (20b merges them). Update the
   compile-time assertions.
5. Check imports: `ddl` must not import `analyze`, `routine`, `typedef`,
   `extension` or the root. `go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/engine/...`.

Open questions for 20a (stop and ask if the answer is not clear from the
code): does any handler rely on the marker to reject an authority built for
another package? (Measured at `c154e14fb`: no; each adapter only ever
receives `AuthorizationView`.) Does `extension.Host.Notice` need a level
other than NOTICE anywhere? (Measured: no.)

### 20b — One root staging adapter (linear item 61)

1. Rename `schemaHost` to `ddlHost` (it now serves every staging package,
   not only schema statements). Move `typedefHost`'s methods onto it and
   delete `typedef_host.go`'s type; `types := typedefHost(host)` in
   `schema.go` and the constructions in `domain_alter.go` and
   `enum_ddl.go` use the one adapter. One compile-time assertion per
   consumer: `routine.DDLHost`, `typedef.StageHost`, `ddl.Host`.
2. Same-named methods must now have one body. Where the two bodies differed
   (only `DependencyObject` after 20a; check), keep the behavior each
   caller had: return the string and the error; callers decide.
3. `extensionHost` stays a separate type (it captures one view and wraps
   the stage; 16c). It reuses the shared root functions; it does not embed
   `ddlHost`.
4. Adapt the tests that build `schemaHost{...}` or `typedefHost{...}`
   mechanically (10 test files listed in the inventory).
5. Docs: the routine and typedef READMEs name the adapter; update them.
   Record for later Part B steps: a feature host interface embeds
   `ddl.Host`; its root adapter embeds `ddlHost` (Go promotes the shared
   methods) or `ddlHost` implements the new interface directly. The refine
   of each step chooses; never copy a shared method body.

## Part B

### 20c — Refine schema-level DDL (docs only; linear item 62)

Measure `stageSchemaLocked`'s three schema cases, `executeCreateTablespace`
and their helpers again, and write the result into this file. The draft
below is the starting point.

**Host sketch (`ddl.SchemaHost`, on `ddlHost`).** `ddl.Host` plus:
- `DatabaseCreateAllowed(Authority) error`: the `databaseGate` closure
  (CREATE on the current database, the 42501 text with the unnamed-engine
  fallback name `stuntgres`).
- No `Actor` method: read the role descriptor through
  `AccessView(auth).Roles.Descriptor(...)` (an `authorization.RoleDescriptor`),
  as `stageSchemaLocked` does today.
- `StageSchemaTables(auth Authority, namespace ids.ObjectAddress, owner
  authorization.PrincipalRef, tables []*parser.CreateTable) error`: the
  embedded `CREATE TABLE` elements, which run `schemaTableLocked` as the
  schema owner with the new namespace first in the path. The authority
  changes (`State.CurrentUser`, `pathPrefix`) stay local to the root
  callback.
- `StageSchemaDependents(auth, schemas, relations, others, cascade bool)
  error`: today's `stageSchemaDomainsLocked` branch.
- `RemoveObject(ref ids.ObjectRef, namespace bool) error`: today's
  `CatalogStage.removeObjectLocked`. This is the smallest removal method;
  the general removal interface (dependents, cascade notices, prepared
  drops) is designed by 24c.
- `AddCascadeNotice(string)`: `stage.schemaCascadeNames`.
- `RelationDiagnostic(Authority, *catalog.Relation) (kind, name string)`:
  `relationDiagnosticKind` and `relationDiagnosticName`.

**Decisions that move (20d):** reserved-name and duplicate checks, owner
resolution (`access.SchemaOwner`), the SET ROLE rule, the IF [NOT] EXISTS
notices, ALTER SCHEMA's rename and owner rules (`access.OwnershipSuccessor`,
`access.OwnershipDatabaseCreate`, `authorization.SchemaACLOwner`), DROP
SCHEMA's target selection, child classification (which descriptors are
dependents, which cascade with their owner, which are unsupported) and its
2BP01 error and detail text; CREATE TABLESPACE's superuser and owner checks
and the final 0A000, calling the `catalog` validators (5i).

**Stays in the root:** `Engine.mu`, authority capture, stage begin,
publication (`executeSchema`, the CREATE TABLESPACE lock), the
namespace-object dispatch, the search path, and `schemaTableLocked` (until
24d).

Write the method list, the move set and the commit sequence, then wait
for approval.

### 20d — Move the schema-level decisions (linear item 63)

Follow 20c. Typical commits: seam (host methods on `ddlHost`, unit tests
first); port in place (`stageCreateSchema(h ddl.SchemaHost, auth, s)` and
friends in the root, naming no `Engine`, `CatalogStage` or
`AuthorizationView`); move into `ddl` with `movex`; unexport what nothing
else uses; docs.

## Invariants

- Diagnostics (codes, messages, details, hints, notice order) of schema,
  tablespace, operator, function, type, collation, conversion, domain and
  extension DDL stay byte-identical.
- Authority stays an opaque per-call value: a helper that changes it gets a
  new value back and the caller's value is unchanged (14c, 15c rule).
- Every host method runs with `Engine.mu` held, never takes or releases it,
  and never waits on the lock manager.
- `extensionHost` never becomes a defined type over `CatalogStage`; neither
  does `ddlHost` (`catalog.Candidate`'s unchecked `Create`/`Replace` must
  not be promoted).
- OID and revision allocation order is unchanged, so Dump output stays
  byte-identical.

## Targeted tests

- Tests follow the code (ENGINE-SPLIT §4): seam tests go to `ddl`; root
  tests that build the adapters are rewritten mechanically; nothing stays in
  the root because a test calls it. Never modify `reference/` or `smoke/`.
- Engine and package units: grep `*_test.go` for `schemaHost`,
  `typedefHost`, `extensionHost`, `RoutineAuthority`, `TypedefAuthority`,
  `stageSchemaLocked`, `executeCreateTablespace`; run the `routine`,
  `typedef` and `extension` package tests.
- Reference selections (`make check RUN='...'` and with `REFERENCE=1`):
  - 20a/20b: `^TestReference(Routine|UserOperator|Domain|Enum|Composite|Collation|Conversion|Extension)`;
  - 20d: `^TestReference(Alter)?Schema` and `Tablespace`;
  - after every sub-step, the Dump/Restore round trips (`DumpRound`,
    `Restore`).
  Some `REFERENCE=1` selections drift on master too (oracle drift, for
  example MERGE under REPEATABLE READ): compare with master before
  blaming the change.
- No `make bench`: startup is not touched.

## Documentation

- Create `internal/engine/ddl/README.md` (20a): the contract (authority
  semantics, stage lifecycle checks, the lock rule), who embeds it, the
  root adapter, what stays in the root, and `## Testing`.
- Update the `routine`, `typedef` and `extension` READMEs (their host
  sections) in 20a/20b; ENGINE-SPLIT §6/§7 decision 16/§8; the status page.
- `AGENTS.md`: add `ddl` to the brace list of engine subpackages. The file
  must not grow.

## Done when

`routine`, `typedef` and `extension` declare no `Authority` or `Stage` of
their own; the root has one staging adapter (`ddlHost`) plus
`extensionHost`; the CREATE/ALTER/DROP SCHEMA and CREATE TABLESPACE
decisions live in `ddl`; `ddl` imports nothing above `session`; the checks
in §8.3 pass.

## Status

- [x] 20a — `ddl` created; `routine`, `typedef` and `extension` adopt it
  (`3ec2314b9`, `19701cafd`, `578ed8479`, `201947f6d`, then the docs
  commit; linear item 60). Re-measured at `351822746`: the inventory and
  the method table above still matched. `ddl` holds `Authority` (marker
  `DDLAuthority`), `Stage`, `Allocator`, `TypeStaging`, `Host` and the
  helpers `Check`, `ObjectRef` and `Notice`; it imports `access`,
  `authorization`, `catalog`, `ids`, `parser`, `session`, `sqlstate` and
  the standard library. `routine.DDLHost` embeds `ddl.Host`
  (`OperatorNamespaces` became `SearchNamespaces(..., ids.OperatorClassID)`,
  `ResolveType` became `ResolveRegtype`, its one `DependencyObject` caller
  ignores the new error); `routine`'s own `ddlCheck`/`ddlRef`
  (`ddl_authority.go`, not listed in the inventory) were deleted for the
  `ddl` helpers. `typedef.StageHost` embeds `ddl.Host` and
  `ddl.TypeStaging`; `check`, `objectRef` and `notice` were deleted.
  `extension.Host` embeds `ddl.Allocator`, `extension.Stage` embeds
  `ddl.Allocator` and `ddl.TypeStaging`, the handlers take a
  `ddl.Authority` and call `view.Check(auth, ...)`/`view.Effective(auth,
  ...)`; `Host.Notice` is unchanged. Root: one `DDLAuthority` marker and
  `ddlAuthorityOf` (panic text "unexpected ddl authority");
  `schemaHost` implements `ddl.Host` (gained `ExistingCreationNamespace`,
  `Context` and the class-mapping `SearchNamespaces`; `DependencyObject`
  renders the text even when the path fails and returns the error beside
  it). Deviation: `typedefHost`'s `ddl.Host` methods delegate to
  `schemaHost(h)` instead of keeping duplicate bodies; both types stay for
  20b. Tests: new `ddl` seam tests (`Check` uses the authority as the
  environment, `ObjectRef` stamps the epoch, `Notice` emits NOTICE level),
  each observed failing first. The prompt's fake-host `Recapture` test
  would only echo the fake, so `TestSchemaHostRecaptureLeavesArgument`
  (new, root) pins the real adapter instead. Adapted:
  `typedef_host_test.go` (`ddlAuthorityOf`; the class map gained the
  operator class). No test moved or deleted. Selected unit and dual-mode proof (308 family tests and
  `DumpRoundTrip`) and quickcheck passed; under `REFERENCE=1` only
  `CompositeExpr(Correlated)Returning` failed, in their `/postgres`
  subtests, identically on master (oracle drift); no bench (startup
  untouched); racecheck/finalcheck skipped at user request
- [x] 20b — One root staging adapter (`ddlHost`) (`b0d3e53b7`,
  `d25058ea5`, then the docs commit; linear item 61). Re-measured at
  `205d59485`: the shared methods already had one body (`typedefHost`'s 15
  `ddl.Host` methods were delegates, `DependencyObject` already returned
  `(string, error)`), and no routine-only method name clashes with a
  typedef-only one, so one type serves both. `schemaHost` was renamed
  `ddlHost` (`schema_host.go` → `ddl_host.go`); the delegates and
  `type typedefHost` were deleted, and the typedef methods (with
  `ddl.TypeStaging`) became `ddlHost` methods in `typedef_host.go`. The
  routine-only methods moved from `ddl_host.go` to a new `routine_host.go`,
  so each consumer's methods and its one compile-time assertion
  (`ddl.Host`, `routine.DDLHost`, `typedef.StageHost`) sit in one file.
  `stageSchemaLocked` passes `host` to the typedef handlers (no
  `types := typedefHost(host)`); `domain_alter.go` and `enum_ddl.go` build a
  `ddlHost`. `extensionHost` is unchanged: it already calls the shared root
  functions (`allocateOIDLocked`, `revisionLocked`,
  `displaceAssociatedArrayLocked`, `associatedArrayName`,
  `creationTypeACL`) and does not embed `ddlHost`. Tests: the three
  `TestTypedefHost*` tests and `TestSchemaHostRecaptureLeavesArgument` are
  `TestDDLHost*` in `ddl_host_test.go` (`typedef_host_test.go` and
  `schema_host_test.go` are gone; bodies unchanged); ten engine test files
  build `ddlHost{...}` instead of `schemaHost{...}`/`typedefHost{...}`. No
  test deleted. No new seam test: the change adds no semantics, and the
  compile-time assertions pin the three consumers. Selected unit and
  dual-mode proof and quickcheck passed; no bench (startup untouched);
  racecheck/finalcheck skipped at user request
- [ ] 20c — Schema-level DDL refined (docs only)
- [ ] 20d — Schema and tablespace decisions moved

Outstanding aliases: none yet.

### Notes for later steps

- 20c/20d: `ddl.SchemaHost` goes on `ddlHost`; put its schema-only
  methods in their own root file (as `routine_host.go` and
  `typedef_host.go` do) with one assertion.
- 23c–28c (Part B of the upper features): a feature host embeds `ddl.Host`;
  do not declare another `Authority` or `Stage`. Its root adapter embeds
  `ddlHost` (Go promotes the shared methods) or `ddlHost` implements the new
  interface directly, in a `<feature>_host.go` file with its assertion; the
  refine of each step chooses. Never copy a shared method body.
- 24c designs the general removal interface (dependents, cascade notices,
  prepared drops over `dropProvider`); 20d adds only `RemoveObject`.
- 5i moved `tablespaceLocation`, `tablespaceName` and `namespaceChildren`
  into `catalog` (`ValidateTablespaceLocation`, `ValidateTablespaceName`,
  `NamespaceChildren`); 20d calls them there.
- Step 28 takes `resolveGrantTablespace`.
