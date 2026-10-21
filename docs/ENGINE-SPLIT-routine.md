# Engine split, step 14: `internal/engine/routine`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-routine.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 14; §4.1 (two parts,
  measured clean sets); §3 rules 6 and 7.
- **Needs:** Part A: the `PG18-ROUTINE-CORE` feature work merged into
  `master` (on `implement-part/PG18-ROUTINE-CORE` when this was written). It
  adds `internal/engine/proc` and `sql_routine*.go`, and touches
  `function.go`, `function_binder.go`, `operator_binding.go`, `catalog/` and
  the `functions` call shapes. Part B: step 13d.
- **Effort:** high for 14a and 14b; xhigh for 14c; high for 14d.
- **Session size:** one sub-step per session.
- **Second round (planned at `c154e14fb`):** 14e (linear item 80) needs `dump`
  18b and `stored` 22a (items 56, 67).

## Goal

Builtin routine and operator inventories, their lookups and validation, and
operator DDL support become a feature package. Call resolution is not part of
it (it is binding, step 12), nor is routine call admission (execution, step
11), nor SQL routine definitions (`proc`, L1).

`routine` and `proc` are different packages:

- `proc` (from `PG18-ROUTINE-CORE`) owns immutable SQL routine definitions
  and invocation frames. It imports only `parser`, `sqltype` and `sqlstate`,
  and `catalog` holds its definitions.
- `routine` owns the builtin inventories, operator metadata and routine and
  operator DDL. It imports `catalog` and sits at L10.

## Inventory (re-measured by 14a at `6eff87c89`, after `PG18-ROUTINE-CORE`; rebased onto `69ed33382`)

Measured with the appendix tool: a declaration is clean when its transitive
package-level references reach none of `Engine`, `EvalContext`,
`expressionBinder`, `StatementScope`, `CatalogStage`, `Operation`, `Session`,
`transactionOwner` or `catalogChanges`. Line counts are after 14a.

**Clean files** (every declaration is clean):

| File | Lines | Content |
|---|---:|---|
| `routine_inventory.go` | 1,270 | `builtinRoutineDeclarations` (`[...]catalog.BuiltinRoutineDeclaration`, not OID-sorted), `builtinRoutineByOID`, `indexBuiltinRoutineDeclarations` |
| `operator_inventory.go` | 811 | `builtinOperators` |
| `operator_metadata.go` | 588 | `operatorMetadata`, `supportedOperatorMetadata`, `releasedOperatorEstimatorResult` |
| `operator_routines.go` | 562 | `builtinOperatorRoutineAssociations` |
| `operator_dump.go` | 305 | `operatorDumpDeclaration`, `operatorDumpEvents`, `operatorDumpLinks`, `operatorSQLName`, `operatorSQLSignature`, `operatorShellGenerator`, `orphanOperatorShellDump`, ... |
| `operator_options.go` | 227 | CREATE/ALTER OPERATOR option parsing and validation (split out of `operator_ddl.go` by 14a) |
| `routine_body.go` | 146 | `builtinRoutineBodyEdges`, `builtinRoutineBodyLinks`, `attachBuiltinRoutineBodies`, `walkRoutineBody`, `bodyCastMatches`, `embeddedCastOID`, `polymorphicBodyArgument` |
| `operator_catalog.go` | 122 | `operatorProvider` (`ClassID`, `Validate`), `operatorSeeded`, `builtinOperatorDescriptors` |
| `routine_catalog.go` | 85 | `builtinRoutineKey`, `builtinRoutineByDeclaration`, `indexBuiltinRoutineFormals`, `lookupBuiltinRoutineDeclaration`, `builtinProcedures` |
| `routine_declaration.go` | 24 | `grantRoutineKindMatches`, `procedureReturnsSet` |
| `operator_index.go` | 16 | `seededOperatorNames` |
| `routine_address_kind.go` | 13 | `objectRoutineKind` |

Engine declarations the clean set still uses (14b exports or moves them):
`sqlError` and `objectNameText` (`operator_options.go`); the dump records
`dumpDomain`, `dumpDeclaration`, `dumpEventKey`, `dumpOperatorLinks` and
`catalogTypeDeclaration` (`operator_dump.go`, from `domain_dump.go` and
`view_dump.go`, now `dump_graph.go`); `builtinOperatorRoutineByDeclaration`
(`operator_routine_binding.go`) and `routineIdentityByOID`
(`regproc_lookup.go`), both used by `operator_dump.go`; `stockIntagg` and
`intaggProviderOf` (`routine_declaration.go`). Census output:
`.cswd/tmp/PG18-ENGINE-SPLIT-ROUTINE-IN-PLACE/census-after.txt`.

**Mixed files** (clean declarations listed; the rest stays or is Part B):

- `routine_reference.go`: clean `currentRoutineDeclaration`,
  `expressionRecipeDescriptor`, `expressionRoutineDescriptor`,
  `sameRoutineDeclaration`, `validateRoutineDescriptor`,
  `validateStoredRoutineDescriptor`; dirty
  `EvalContext.selectExpressionRecipe` (admission, step 11).
- `operator_ddl.go` (669): staging on `Engine` (`stageCreateOperatorLocked`,
  `stageAlterOperatorLocked`, `stageDropOperatorLocked`,
  `resolveOperatorLink`, `stageResolvedOperatorLink`,
  `collectOperatorOptions`, `operatorEstimator`, `operatorRoutine`,
  `findUtilityOperator`, `operatorArgument`, `resolveOperatorArgument`,
  `checkOperatorArgumentUsage`), `operatorOptionsSyntax`,
  `stageOperatorDescriptor`; all dirty (Part B).
- `operator_dependencies.go`: clean `operatorDependencyAddresses`,
  `operatorDependsOn`; dirty `dropProvider.clearOperatorLinks` (drop
  orchestration).
- `routine_program.go`: clean records `routineAdmission`,
  `routineAdmissionProgram`; their methods are execution (step 11).
- `sql_routine*.go` (from `PG18-ROUTINE-CORE`, 1,495 lines in 8 files): 23
  clean declarations, among them `sqlRoutineExpression` and its node
  methods, `sqlCallable`, `selectSQLCallables`, `appendSQLCallable`,
  `validateSQLRoutineResult`, `sqlRoutineDefaultClosure`,
  `closedSQLRoutineDefault` (both in `analyze` since 12d.3b) and the dump
  directives; the DDL, resolution and
  scope code reaches `Engine`, `expressionBinder` or `StatementScope`. The
  call-resolution and invocation parts belong to steps 12 and 11; the DDL
  and its dump are Part B (14c).

**Binder file:** `function_routine_binding.go` (91, from 14a) holds the six
`expressionBinder` (since 12d.6 `analyze.Binder`) methods that capture builtin routine declarations and
their SQL-body closure (`builtinRoutineInfo`, `builtinRoutineReference`,
`selectedBuiltinRoutineInfo`, `captureBuiltinRoutineDeclaration`,
`capturePolymorphicBody`, `captureRoutineBody`); call resolution, step 12.

**Not routine code** (stays elsewhere): `routine_admission.go`,
`routine_body_admission.go`, `routine_program.go` methods and
`routine_inquiry.go` (call admission under an `EvalContext`: step 11);
`routine_dependencies.go` (stored-SQL rendering); `routine_presentation.go`
(binder and view-rendering captures); `function.go`,
`function_binder.go`, `function_column_definitions.go`,
`function_from_filter.go`, `operator_binding.go`,
`operator_routine_binding.go` (call resolution: step 12).

Builtin seeding is on the startup path: `newEngine` calls
`installBuiltinProcedures` and `installBuiltinOperators`
(`builtin_catalog.go`), which install what the pure constructors
`builtinProcedures` (bodies attached by `attachBuiltinRoutineBodies`) and
`builtinOperatorDescriptors` return.

## Part A

### 14a — In place

1. After the `PG18-ROUTINE-CORE` merge, re-run the appendix tool and update
   the inventory above.
2. Turn the three `initialize*` methods into pure constructors that return
   the descriptors to install; `newEngine` installs them (the catalog write
   API stays in the engine). Keep installation order and OIDs.
3. Split `operator_ddl.go` into `operator_options.go` (the clean option
   validation) and the staging methods.
4. Move the four routine binder methods out of `routine_catalog.go` into a
   binder file (`function_routine_binding.go`), so file names match owners.
5. If step 13b has not made the operator and routine stage state a slot, do
   it here (pattern 2).

### 14b — Create `routine` and move the clean set

1. Export in place what the engine still calls (the inventories' lookups,
   `OperatorProvider`, `Seeded`, the option validators, the dump helpers, the
   body-edge walker, the descriptor validators, the seed constructors).
2. Move the clean set with the `movex` approach. Leave aliases for types.
3. Check imports: `catalog`, `expr`, `functions` (provider constants),
   `authorization` (if grants need it), `ids`, `parser`, `sqltype`,
   `sqlstate` and the standard library. Not `proc` unless the merged code
   needs it; never `storage`, `visibility`, `lock`, `plan` or above.
4. Remove the aliases in the same sub-step if there are fewer than about 50
   uses; otherwise record them for 14d.

## Part B

### 14c — Refine the host (docs only), after 13d

Measure the staging methods' reach (`CatalogStage`, `Operation`, the
statement scope), write the host interface and the move set (operator DDL
staging, `sql_routine_ddl.go` and its dump), and wait for approval.

**14c refine (approved; measured at `1f606f52d`).** The `frontier` tool
(`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/`; outputs in
`.cswd/tmp/PG18-ENGINE-SPLIT-ROUTINE-REFINE/`), with `operator_ddl.go` and
`sql_routine_ddl.go` as the set, then the two dump files:

- **The staging is small and reaches no scope.** `operator_ddl.go` (672
  lines) is all staging: CREATE/ALTER/DROP OPERATOR, link resolution,
  argument, utility-operator, implementation and estimator lookup, and
  `operatorOptionsSyntax`. `sql_routine_ddl.go` (160) holds
  `stageCreateFunctionLocked` (about 100 lines), the dispatch wrapper
  `executeCreateFunction`, `bindSQLRoutineLocked` and
  `validateSQLRoutineResult`. Outside uses: the `schema.go` dispatch (four
  calls), `execute.go` (`executeCreateFunction`, `validateSQLRoutineResult`)
  and the engine tests (`operator_option_test.go` 13,
  `operator_user_test.go` 7, `operator_estimator_test.go` 1).
- **What it reaches.** `AuthorizationView` (`Check`, `CanUse`,
  `CheckCreate`, `Recapture`, `definitionType`, `definitionLookup`, the
  embedded `access.View`); `CatalogStage` (`Create` and `Replace` with their
  lifecycle checks, `creationACL`, `domainTargets`, `dependencyPath`, and the
  promoted `catalog.Candidate` `Base`, `Get`, `UpdateDescriptor`,
  `LookupName`); `stageRead`; `allocateOIDLocked` and `revisionLocked`;
  `collationCreationNamespaceLocked` and `conversionCheckCreate`;
  `lookupNamespaces` (which returns `analyze.NamespaceSelection`),
  `selectRoutine` and `analyze.ProcedureInNamespaces`;
  `captureRegtypeInput` with `resolveParsedTypeName`; `referencePath` with
  `dependencyObject`; notices; `operationReplayOrZero(op).sqlBodies`;
  `stageDefinitionReader`; and `bindSQLRoutineLocked`, which calls
  `analyze.Describe`. No `StatementScope` outside the dispatch wrapper and
  the binding; of the operation only notices, the replay flag and the
  definition reader.
- **Layering.** `analyze` imports `routine`, so `routine` never imports
  `analyze` (§3 rule 6). Namespace selection, routine selection, type-name
  resolution and SQL body binding stay in the root behind the host.
- **Authority is a value.** The handlers take `AuthorizationView` by value,
  and `collationCreationNamespaceLocked(&auth, …)` recaptures only the
  caller's copy: inside `resolveOperatorLink` the recapture never reaches
  the caller, and the peer check uses its own `peerAuth.Recapture(view)`
  copy. A host that held one mutable view would change these diagnostics.
- **Object refs need no host.** `auth.View.Owner` is the engine's catalog
  owner, so `e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)` is
  `view.Owner.Domain.Ref(view.Catalog.Epoch(), a)`.
- **Dumps.** `operator_dump.go`, `sqlRoutineDumpEvents` and
  `sqlRoutineDumpStatement` use the root dump graph (`dumpEventKey`,
  `dumpDomain`, `dumpPredecessor`, `catalogColumnDeclaration`,
  `commentLiteral`); `validateRestoredSQLRoutines` is Restore orchestration
  under `Engine.mu`. Clean: `rebindSQLRoutineDefinitions` (a pure
  `catalog.State` transform, used by `temporary_preservation.go`) and
  `sqlRoutineReplayDirective` with `sqlRoutineRestoreDirective` (a pure
  parser check, used by `control.go`).

Decisions:

1. **One scope-free root adapter.** `routine` declares `Authority`,
   `DDLHost` and `Stage`. The root implements `DDLHost` on a new adapter
   `schemaHost` (`schema_host.go`: engine, operation and catalog stage, one
   compile-time assertion), built once per statement in `stageSchemaLocked`.
   Schema staging runs under `Engine.mu` without a `StatementScope`, so it
   is a scope-free receiver like 12c's `bindingHost`, in the shape of 16c's
   `extensionHost` (§7 decision 8). Unlike `extensionHost` it captures no
   authorization view (decision 2). It is not a defined type over
   `CatalogStage`.
2. **`Authority`** is `access.Environment` plus the marker
   `RoutineAuthority()`; `AuthorizationView` implements it with value
   receivers, and one checked helper recovers it. Handlers keep it by
   value. Host methods that change an authority return a new value and
   never change their argument, which keeps the copy semantics above.
3. **`DDLHost`** (draft; 14d may rename). Every method runs with
   `Engine.mu` held and never takes or releases it.
   - Authority: `AccessView(auth) access.View` (catalog, roles, state and
     owner, so handlers build refs and call `access.SchemaOwner` and
     `access.OwnershipEligibility` themselves); `Check(auth,
     access.Requirement)`, which also covers `CanUse` and `CheckCreate`;
     `Recapture(auth, catalog.Read) Authority`; `DefinitionType(auth,
     sqltype.ID)`; `DefinitionLookup(auth, ids.NameKey)`.
   - Names and types: `CreationNamespace(auth, name) (ids.ObjectRef,
     Authority, error)`, which may stage implicit namespaces;
     `CheckCreateIn(auth, namespace)` (CREATE with the trusted Restore
     exception); `OperatorNamespaces(auth, name) ([]ids.ObjectAddress,
     error)`; `LookupFunction(auth, name, signature) (*catalog.Procedure,
     error)` (function path, exact signature); `SelectFunction(auth, name,
     signature) (catalog.RoutineIdentity, int, error)` (`selectRoutine`);
     `ResolveType(auth, parser.TypeName) (ids.OID, error)`;
     `DependencyObject(auth, address) string`.
   - Stage: `Stage() Stage`, where `Stage` is `Create`, `Replace`,
     `UpdateDescriptor`, `Get` and `LookupName` (`*CatalogStage` satisfies
     it, so `Create` and `Replace` keep their lifecycle checks);
     `StageBase()` and `StageRead()` (`catalog.Read`); `CreationACL(owner,
     schema, class)`; `DropTarget(address)`; `RecordDependencyPath(auth)`;
     `AllocateOID()`; `Revision()`.
   - Operation: `EmitNotice(session.NoticeLevel, sqlstate.Error)`;
     `BindSQLRoutine(auth, d, input) error`, which returns nil during
     Restore SQL body replay and otherwise binds the body with the stage's
     definition reader in place. The 42P02 position mapping stays in the
     handler.
4. **Move set (14d).** The operator staging, split by responsibility into
   `routine/operator_ddl.go` (create, alter, drop), `operator_links.go`
   (link resolution and staging) and `operator_lookup.go` (arguments,
   utility operator, implementation, estimator); `operatorOptionsSyntax`
   joins `operator_options.go`. `stageCreateFunctionLocked` goes to
   `routine/function_ddl.go`; `rebindSQLRoutineDefinitions` and the restore
   directive go to `routine/function_restore.go`. `routine` adds the
   imports `access`, `proc` and `session`, all below L10; never `analyze`,
   another feature package or the root.
5. **Stays in the root.** `executeCreateFunction`, `bindSQLRoutineLocked`
   and `validateSQLRoutineResult` (`sql_routine_ddl.go` becomes
   `sql_routine_binding.go`, so the name matches the owner); `schemaHost`;
   `operator_dump.go` and `sql_routine_dump.go`, which wait for a dump seam
   (postponed `control`); `sql_routine.go` and `sql_routine_scope.go`
   (invocation).
6. **Aliases.** None exist and none are needed: only functions move.

### 14d — Move the DDL handlers

Follow 14c. One session, not broad. Commits:

1. **Host, port in place.** Add `routine.Authority`, `DDLHost` and `Stage`,
   and the root `schemaHost` with its assertion. Turn the staging methods
   into engine-package functions over `(h DDLHost, auth Authority, ...)`
   that name no `Engine`, `AuthorizationView`, `CatalogStage` or
   `Operation`; adapt the engine tests mechanically. Re-run `frontier`: the
   set's OUT is only the host.
2. **Move.** Move the set with `movex`; `schema.go` calls
   `routine.StageCreateOperator` and its siblings. Move
   `TestUserOperatorOptionExtraction` into `routine` if it no longer needs
   an engine.
3. **Unexport.** Unexport the 14b exports that only `routine` now uses:
   `CheckOperatorLinkReplacement`, `OperatorEstimatorVariant`,
   `OperatorOptionError`, `ValidateOperatorEstimatorMetadata`,
   `ValidateOperatorOptions`, and the option parsers
   (`OperatorAlterOption`, `OperatorBoolean`, `OperatorOptionName`,
   `OperatorOptionType`) if no engine test still needs them.
4. **Docs.**

Check with `go list` that `routine` imports neither `analyze` nor a feature
package. Verify with build, the targeted units, the 14a reference selection
in quick mode and with `REFERENCE=1`, quickcheck and deps. The change is off
the startup path; run `make bench` only if seeding code changes.

### 14e — Operator and SQL routine dumps (linear item 80)

Added when the postponed packages were planned (measured at `c154e14fb`).
The dump seam the 14b and 14c notes waited for is step 18 (`dump`):

1. After 18b, `operator_dump.go` (292 clean lines), `sqlRoutineDumpEvents`
   and `sqlRoutineDumpStatement` (`sql_routine_dump.go`) move into
   `routine`. They already use `dump.Event` and `dump.Statement` (18b);
   they take a `dump.Presenter` (type declarations with a nil reader,
   routine identity) and a `dump.Resolver` instead of the root's
   `catalogTypeDeclaration`, `routineIdentityByOID` and `dumpPredecessor`.
   The root adapters are `dumpPresenter` and `dumpResolver`.
2. The root passes its presenter (after step 21, the one in `regobj`).
3. Dump output stays byte-identical: run the Dump/Restore reference
   selections in both modes.

Not here: `routine_dependencies.go`, `routine_presentation.go` and
`scalar_function_sql.go` hold `scalarRenderer` and `viewRenderer` methods
and move with `stored` (22b); `procedureProvider` imports `routine` and
`extension` and goes with `stored` too; the routine admission files go to
`exec` (11e).

## Invariants

- Builtin OIDs, names, signatures, provider assignments, operator metadata
  and installation order stay identical; `make bench` shows no startup
  regression.
- Operator DDL validation messages and SQLSTATEs stay identical.
- Operator dump output stays byte-identical.
- Part B: operator and CREATE FUNCTION checks run in the same order with the
  same diagnostics, positions and notices; OIDs and revisions are allocated
  in the same order; an authority changed inside a helper (implicit
  namespace, peer recapture) stays local to that helper, as today. The host
  adds no lock.

## Targeted tests

- Engine units: grep `*_test.go` for `builtinRoutine`, `builtinOperators`,
  `operatorMetadata`, `operatorDump`, `validateOperatorOptions`.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  CREATE/ALTER/DROP OPERATOR, `pg_operator`, `pg_proc`, `regproc`,
  `regprocedure`, `regoperator`, operator resolution, and the routine-core
  selectors in the `proc` README after the merge.
- `make bench` after 14a and 14b.

## Documentation

- Create `internal/engine/routine/README.md` (14b): what it owns, how it
  differs from `proc`, what stays in the engine, `## Testing`.
- `docs/design/PG18-ARCH-ROUTINE-EXECUTION.md` where it names moved files.
- `AGENTS.md`: add `routine` to the shared subpackage bullet.

## Done when

Part A: the clean set lives in `routine`, builtin seeding is a pure
constructor, and file names match their owners. Part B: operator and routine
DDL handlers live in `routine` behind `DDLHost`; the dumps wait for a dump
seam (14c). The checks in §8.3 pass.

## Status

- [x] 14a — In place (`f4645bc19`, `87bf1455e`, `f33847363`, then the docs
  commit) *(racecheck skipped at user request)*
  - `f4645bc19`: `builtinProcedures` and `builtinOperatorDescriptors` are
    pure constructors; `attachBuiltinRoutineBodies` resolves body edges
    through `builtinRoutineBodyLinks`, computed once per process (the
    declaration table is not OID-sorted). `installBuiltinProcedures` and
    `installBuiltinOperators` in `builtin_catalog.go` do the catalog writes at
    the old positions in `newEngine`, so order and OIDs are unchanged.
  - `87bf1455e`: the clean option helpers moved unchanged from
    `operator_ddl.go` to `operator_options.go`.
  - `f33847363`: the four binder methods from `routine_catalog.go` and the
    two body-capture binder methods from `routine_body.go` moved unchanged
    to `function_routine_binding.go`.
  - Slots (item 5): nothing to do. `CatalogStage`, `catalogChanges`,
    `Operation` and `transactionOwner` hold no operator or routine stage
    state. The `StatementScope` reg*/operator output caches are the
    "presentation caches" slot of 13a.
  - Verification: build, targeted engine units, selected reference tests
    (all of `reference/operators`, regproc/regoperator in
    `reference/type_resolution`, `pg_proc` users in `reference/catalogs`,
    RoutineCore/RoutineReview; 126 tests) in quick mode and with
    `REFERENCE=1`, `make quickcheck`, `make deps` and `make bench` passed.
    Startup bench, interleaved 10 runs each on a shared host (load ~18):
    median 966 µs/op before, 962 µs/op after; 5,100 allocs/op and about
    1.64 MB/op on both. Some samples on both sides exceed 1 ms because of
    host contention; allocations are unchanged.
    Racecheck and finalcheck skipped at user request.
  - No tests moved, adapted or deleted.
- [x] 14b — `routine` created and clean set moved (`aaf6759a4`,
  `d6becc9d6`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
  - Re-measured at `baaa1572e` with a frontier tool (go/types; outside uses
    of the set, and engine declarations the set uses).
  - `aaf6759a4` (export in place): the clean declarations of the mixed files
    were split into whole files first; the engine keeps
    `EvalContext.selectExpressionRecipe` (`routine_reference.go`),
    `dropProvider.clearOperatorLinks` (`operator_dependencies.go`),
    `procedureReturnsSet` (now `routine_returns_set.go`) and the binder
    methods of `operator_routine_binding.go`. The 14a `movex` tool exported
    the names the engine uses (capitalized, otherwise unchanged). The private
    maps are read through lookups: `BuiltinRoutine` (was
    `builtinRoutineByOID[id]`), `OperatorRoutine` and the iterator
    `OperatorRoutines` (was `builtinOperatorRoutineByDeclaration`; both
    range loops were already map-ordered), `SeededOperatorNames()` and the
    constant `BuiltinOperatorCount`.
  - `d6becc9d6` (move): 14 files and the seam test moved into
    `internal/engine/routine` (`inventory.go`, `procedures.go`, `body.go`,
    `descriptor.go`, `grant_kind.go`, `address_kind.go`, `operator_*.go`,
    ~4.2k lines); `movex` qualified the engine references in 60 files.
    `routine` imports `authorization`, `catalog`, `expr`, `functions`, `ids`,
    `parser`, `sqltype`, `sqlstate` and the standard library. `sqlError` and
    `objectNameText` got private copies in `errors.go`, as in `visibility`,
    `lock` and `access`. Four engine locals named `routine` were renamed.
  - Moved beyond the 14a table: `builtinOperatorRoutineKey`,
    `builtinOperatorRoutineByDeclaration` and their index (now
    `operator_routine_index.go`), a pure join of the two inventories; moving
    it avoided exporting anonymous-struct fields.
  - Not moved: `operator_dump.go` needs the engine's dump graph
    (`dumpEventKey`, `dumpPhase`, `dumpDomain`), type-declaration rendering
    (`catalogTypeDeclaration` → `definitionReader`, now `catalog.DefinitionReader` since 12d.2) and
    `routineIdentityByOID`; it waits for a dump seam. `routineAdmission*`
    records cannot move without their execution methods (step 11). The clean
    `sql_routine*.go` declarations are expression nodes or resolution (steps
    10–12) or DDL (14c/14d).
  - Tests: new seam tests `TestBuiltinRoutineLookup`,
    `TestOperatorRoutineLookup`, `TestSeededOperatorNamesIsOneIndex` (written
    first, observed failing). Moved to `routine/operator_estimator_test.go`:
    `TestUserOperatorCatalogEstimatorVariants` and
    `TestUserOperatorCatalogEstimatorMetadata` (local `qualifiedName`
    helper). `TestUserOperatorOptionExtraction` stays: it also checks the
    engine's `operatorOptionsSyntax`. Engine tests were only qualified
    mechanically; none deleted.
  - Verification: build, vet (only the existing `composites`/`copylocks`
    findings), 90 targeted unit tests, the 126-test 14a reference selection
    in quick mode and with `REFERENCE=1`, `make quickcheck` and `make deps`
    passed. Startup bench, interleaved 10 runs each on a loaded host (load
    10–15): median 979 µs/op before, 982 µs/op after; 5,106–5,109 allocs/op
    and about 1.64 MB/op on both. Racecheck and finalcheck skipped at user
    request.
- [x] 14c — Host refined (docs only; the commit that records this text)
  *(measured at `1f606f52d`; quickcheck passed; racecheck/finalcheck
  skipped at user request)*
  - Decisions: `routine.DDLHost` on a scope-free root `schemaHost`,
    authority as an opaque `routine.Authority` value, dumps wait for a dump
    seam, 14d is one session with no aliases.
- [x] 14d — DDL handlers moved (`df37c5dfb`, `193c703f7`, `5604d3ccf`,
  then the docs commit) *(racecheck/finalcheck skipped at user request)*
  - Re-measured at `9a68139e7`; started from `204022e05`. 14c's reach still
    held, and `routine` → `access`/`proc`/`session` makes no cycle (only
    `analyze` imports `routine`).
  - `df37c5dfb` (host, port in place): `routine/host.go` declares
    `Authority`, `Stage` and `DDLHost`; the root `schema_host.go` holds
    `schemaHost` (engine, operation, catalog stage), its assertion, the
    `RoutineAuthority` marker on `AuthorizationView` and the checked
    `routineAuthorityOf`. `stageSchemaLocked` builds one per statement. The
    staging became functions over `(h DDLHost, auth Authority, ...)`, split
    into the files they move to; `sql_routine_ddl.go` became
    `sql_routine_binding.go`. `frontier` OUT was only `sqlError`, which
    `routine` already copies.
  - Host refinements against the 14c draft: no host `Check`.
    `AuthorizationView.Check`, `CanUse` and `CheckCreate` are exactly
    `v.View.Check(v, ...)`, so handlers call
    `AccessView(auth).Check(auth, ...)` through `ddlCheck`, always pairing a
    view with its own authority; the peer check pairs both with
    `Recapture`'s result and keeps the ref on the caller's epoch, as before.
    `LookupFunction` and `SelectFunction` each resolve the function path;
    the estimator's second `SelectFunction` repeats a deterministic lookup
    under `Engine.mu` where the old code reused one path. `ResolveType`
    replaces `resolveOperatorArgument`. `RecordDependencyPath` and
    `DependencyObject` read the reference path in the root.
  - `193c703f7` (move): `routine/operator_ddl.go` (create, alter, drop,
    option collection), `operator_links.go`, `operator_lookup.go`,
    `ddl_authority.go` (`ddlCheck`, `ddlRef`), `function_ddl.go`;
    `operatorOptionsSyntax` joined `operator_options.go`. Entry points
    `StageCreateOperator`, `StageAlterOperator`, `StageDropOperator`,
    `StageCreateFunction`. `routine/function_restore.go` holds
    `RestoreDirective`, `ReplayDirective` (`control.go`) and
    `RebindSQLDefinitions` (`temporary_preservation.go`), moved from
    `sql_routine_dump.go`. `routine` imports `access`, `proc` and `session`
    as well; never `analyze`, a feature package or the root (`go list`).
  - `5604d3ccf` (unexport): the nine helpers of step 3, all used only in
    `routine` after the move. `ValidOperatorName` and
    `ReleasedOperatorEstimatorResult` stay exported (engine tests);
    `OperatorRoutineKey` stays (14b's lookup key type).
  - Tests: `TestUserOperatorOptionExtraction` moved to
    `routine/operator_options_test.go` with copies of
    `operatorParsedOptions` and `operatorOptionDiagnostic`. Engine tests
    call the `routine.Stage*` entry points through `schemaHost`. Adapted:
    the three exact lookups of a published `public.op(int4, int4)`
    (`TestUserOperatorCatalogExactUtilityLifecycle` and the reciprocal-peer
    test in `operator_option_test.go`) read the catalog by name
    (`operatorUnitLookup`); the exact lookup itself stays covered by the
    same tests' ALTER and DROP. Rewritten:
    `TestUserOperatorCatalogEstimatorReleasedIdentity` stages CREATE
    OPERATOR with `restrict=eqsel, join=eqjoinsel` and checks the staged
    identities (101, 105), their result type and that neither is a callable
    declaration, instead of calling the private estimator lookup. None
    deleted.
  - Verification (rebased onto `25b589df5`): build, vet (only the existing
    `composites`/`copylocks`/`lostcancel` findings), 54 targeted unit tests
    after each commit, the 129-test reference selection (14b's 126 plus
    `RoutineNesting{Diagnostics,Boundaries}` and `FDWRoutineGrants`) in
    quick mode and with `REFERENCE=1`, `make quickcheck` and `make deps`
    passed. No `make bench`: seeding and startup are untouched. Racecheck
    and finalcheck skipped at user request.
- [ ] 14e — Operator and SQL routine dumps moved (linear item 80)

Outstanding aliases: none (14b qualified every reference; no type aliases;
14d moved only functions).

Notes for later steps:

- 14b: the clean set is the "Clean files" table plus the clean declarations
  of the mixed files. Export the seed constructors (`builtinProcedures`,
  `builtinOperatorDescriptors`) and keep the `install*` methods in the
  engine. Settle the engine helpers the clean set uses (listed under the
  inventory): `sqlError`/`objectNameText` can go to `sqlstate`/`catalog` or
  get a local copy, and the dump records (`dumpDomain`, `dumpEventKey`, ...)
  decide whether `operator_dump.go` moves in 14b or waits for a dump seam.
- `EvalContext.selectExpressionRecipe` (`routine_reference.go`) and
  `dropProvider.clearOperatorLinks` (`operator_dependencies.go`) stay in the
  engine; 14a left them in their files because 14b moves declarations, not
  files.

- Step 15 (`typedef`): operators and types reference each other (type
  providers, range and enum operators). Decision (14b): `routine` owns the
  operator inventory, metadata, `OperatorSeeded`, `OperatorProvider` and
  option validation; `typedef` imports `routine` for them, never the
  reverse. Type-declaration rendering (`catalogTypeDeclaration`) is type
  code; operator dump rendering stays in the engine with it until a dump
  seam exists.
- 14d: the exported option helpers (`OperatorOptionName`,
  `ValidateOperatorOptions`, ...) exist for `operator_ddl.go`; when the
  staging moves into `routine`, unexport the ones nothing else uses (list
  in 14d step 3). `OperatorSeeded` and `OperatorInputName` stay exported
  (`analyze` and the engine use them).
- 15d (`typedef` on `schemaHost`): `routine.DDLHost` and the planned
  `typedef.StageHost` share method names whose parameter or result types
  differ (`AccessView`, `Recapture`, `CreationNamespace`, `CheckCreateIn`,
  `ResolveType`, `DependencyObject`, `RecordDependencyPath`, `Stage`), since
  each package declares its own `Authority` and `Stage`. One struct cannot
  implement both. Either give `typedef` a defined type over the same fields
  (`type typedefHost schemaHost`, sharing the root functions), or move
  `Authority` and `Stage` down into one shared package. Note also that
  15c's `StagedCreationNamespace` is 14d's `CreationNamespace`, and 14d has
  no host `Check` (see 14d above). 15d took the defined type
  (`typedefHost`) and reused 14d's `CreationNamespace` name.
- Dump seam (postponed `control`): `operator_dump.go`,
  `sqlRoutineDumpEvents`, `sqlRoutineDumpStatement` and
  `routineIdentityByOID` move once the dump graph (`dumpEventKey`,
  `dumpDomain`, `dumpPredecessor`, type-declaration rendering) has a seam.
- Steps 15c and 17c: schema DDL handlers run without a scope. 16c's
  staging-host shape (one root adapter over engine, operation and stage)
  applies; where handlers copy or recapture the authorization view per
  helper, as routine's do, pass authority as an opaque per-package value
  (14c decision 2) instead of capturing one view.
- The F3 provider aliases are removed from `routine_declaration.go` in the
  verified functions Item6 cutover. Its alias-removal prerequisite for 14b is
  satisfied: latest-base semantic and public smoke proof passed in tests-12.
  Build, selected native/real, quickcheck, full race and dependency/import passes
  in tests-10/tests-11 describe the preceding base; complete-candidate gates
  refresh before review. Startup results retain their recorded qualifications.
  Post-integration finalcheck alone was
  waived by the user, not run or passed. This does not extract routine inventories,
  satisfy 14b's other prerequisites or complete 14a/b.
