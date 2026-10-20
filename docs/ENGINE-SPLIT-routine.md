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
`view_dump.go`); `builtinOperatorRoutineByDeclaration`
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
  `closedSQLRoutineDefault` and the dump directives; the DDL, resolution and
  scope code reaches `Engine`, `expressionBinder` or `StatementScope`. The
  call-resolution and invocation parts belong to steps 12 and 11; the DDL
  and its dump are Part B (14c).

**Binder file:** `function_routine_binding.go` (91, from 14a) holds the six
`expressionBinder` methods that capture builtin routine declarations and
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

### 14d — Move the DDL handlers

Follow 14c. Remove the remaining aliases.

## Invariants

- Builtin OIDs, names, signatures, provider assignments, operator metadata
  and installation order stay identical; `make bench` shows no startup
  regression.
- Operator DDL validation messages and SQLSTATEs stay identical.
- Operator dump output stays byte-identical.

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
DDL handlers live in `routine`. The checks in §8.3 pass.

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
- [ ] 14c — Host refined (docs only; after 13d)
- [ ] 14d — DDL handlers moved

Outstanding aliases: none (14b qualified every reference; no type aliases).

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
- 14c/14d: the exported option helpers (`OperatorOptionName`,
  `ValidateOperatorOptions`, ...) exist for `operator_ddl.go`; when the
  staging moves into `routine`, unexport the ones nothing else uses.
  `operator_dump.go` and `routineIdentityByOID` are candidates for the same
  move once a dump seam exists.
- The F3 provider aliases are removed from `routine_declaration.go` in the
  verified functions Item6 cutover. Its alias-removal prerequisite for 14b is
  satisfied: latest-base semantic and public smoke proof passed in tests-12.
  Build, selected native/real, quickcheck, full race and dependency/import passes
  in tests-10/tests-11 describe the preceding base; complete-candidate gates
  refresh before review. Startup results retain their recorded qualifications.
  Post-integration finalcheck alone was
  waived by the user, not run or passed. This does not extract routine inventories,
  satisfy 14b's other prerequisites or complete 14a/b.
