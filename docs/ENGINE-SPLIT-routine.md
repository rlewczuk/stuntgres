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

## Inventory (measured at `75fe0d710`, before `PG18-ROUTINE-CORE`)

Re-measure after the merge.

**Clean files** (reach no god struct):

| File | Lines | Content |
|---|---:|---|
| `routine_inventory.go` | 1,268 | `builtinRoutineDeclarations` (`[...]catalog.BuiltinRoutineDeclaration`), `builtinRoutineByOID`, `indexBuiltinRoutineDeclarations` |
| `operator_inventory.go` | 811 | `builtinOperators` |
| `operator_metadata.go` | 588 | `operatorMetadata`, `supportedOperatorMetadata`, `releasedOperatorEstimatorResult` |
| `operator_routines.go` | 562 | `builtinOperatorRoutineAssociations` |
| `operator_dump.go` | 305 | `operatorDumpDeclaration`, `operatorDumpEvents`, `operatorDumpLinks`, `operatorSQLName`, `operatorSQLSignature`, `operatorShellGenerator`, `orphanOperatorShellDump`, ... |
| `routine_declaration.go` | 40 | `grantRoutineKindMatches`, `procedureReturnsSet`, and the temporary F3 aliases `routineProvider = functions.RoutineProvider` plus 23 provider constants, about 1,290 uses (removed by the functions item "F3 aliases removed") |
| `operator_index.go` | 9 | `seededOperatorNames` |
| `routine_address_kind.go` | 7 | `objectRoutineKind` |

**Mixed files** (clean declarations listed; the rest stays or is Part B):

- `operator_catalog.go`: clean `operatorProvider` (`ClassID`, `Validate`),
  `operatorSeeded`; dirty `Engine.initializeOperators`.
- `routine_catalog.go`: clean `builtinRoutineKey`,
  `builtinRoutineByDeclaration`, `indexBuiltinRoutineFormals`,
  `lookupBuiltinRoutineDeclaration`; dirty `Engine.initializeBuiltinRoutines`
  and four binder methods (`builtinRoutineInfo`, `builtinRoutineReference`,
  `selectedBuiltinRoutineInfo`, `captureBuiltinRoutineDeclaration`; step 12).
- `routine_body.go`: clean `builtinRoutineBodyEdges`, `walkRoutineBody`,
  `bodyCastMatches`, `embeddedCastOID`, `polymorphicBodyArgument`; dirty
  `Engine.initializeBuiltinRoutineBodies` and two binder methods.
- `routine_reference.go`: clean `currentRoutineDeclaration`,
  `expressionRecipeDescriptor`, `expressionRoutineDescriptor`,
  `sameRoutineDeclaration`, `validateRoutineDescriptor`,
  `validateStoredRoutineDescriptor`; dirty
  `EvalContext.selectExpressionRecipe`.
- `operator_ddl.go` (876): clean `checkOperatorLinkReplacement`,
  `operatorAlterOption`, `operatorBoolean`, `operatorEstimatorVariant`,
  `operatorInputName`, `operatorOptionError`, `operatorOptionName`,
  `operatorOptionType`, `validOperatorName`,
  `validateOperatorEstimatorMetadata`, `validateOperatorOptions`; dirty
  staging on `Engine` (`stageCreateOperatorLocked`,
  `stageAlterOperatorLocked`, `stageDropOperatorLocked`,
  `resolveOperatorLink`, `stageResolvedOperatorLink`,
  `collectOperatorOptions`, `operatorEstimator`, `operatorRoutine`,
  `findUtilityOperator`, `operatorArgument`, `resolveOperatorArgument`,
  `checkOperatorArgumentUsage`), `operatorOptionsSyntax`,
  `stageOperatorDescriptor`.
- `operator_dependencies.go`: clean `operatorDependencyAddresses`; dirty
  `dropProvider.clearOperatorLinks` (drop orchestration).
- `routine_program.go`: clean records `routineAdmission`,
  `routineAdmissionProgram`; their methods are execution (step 11).

**Not routine code** (stays elsewhere): `routine_admission.go`,
`routine_body_admission.go`, `routine_program.go` methods and
`routine_inquiry.go` (call admission under an `EvalContext`: step 11);
`routine_dependencies.go` (stored-SQL rendering); `function.go`,
`function_binder.go`, `function_column_definitions.go`,
`function_from_filter.go`, `operator_binding.go`,
`operator_routine_binding.go` (call resolution: step 12).

Builtin seeding is on the startup path: `newEngine` calls
`initializeBuiltinRoutines`, `initializeOperators` and
`initializeBuiltinRoutineBodies`.

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

- [ ] 14a — In place (after `PG18-ROUTINE-CORE`)
- [ ] 14b — `routine` created and clean set moved
- [ ] 14c — Host refined (docs only; after 13d)
- [ ] 14d — DDL handlers moved

Outstanding aliases: none yet.

Notes for later steps:

- Step 15 (`typedef`): operators and types reference each other (type
  providers, range and enum operators). 14b settles which side owns the
  shared helpers; record the decision here.
- The F3 provider aliases in `routine_declaration.go` should be gone before
  14b (functions item "F3 aliases removed"); if not, remove them first.
