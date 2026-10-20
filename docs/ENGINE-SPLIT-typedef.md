# Engine split, step 15: `internal/engine/typedef`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-typedef.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 15; §4.1 (two parts,
  measured clean sets); §3 rules 6 and 7.
- **Needs:** Part A: 14b (`routine`), because operators and types reference
  each other. Part B: 13d.
- **Effort:** high for 15a and 15b; xhigh for 15c; high for 15d.
- **Session size:** one sub-step per session.

## Goal

User-defined type support (domains, enums, composites, ranges, collations
and conversions) becomes a feature package: first its records, validation,
dump and remap helpers (Part A), then its DDL handlers (Part B). The name
`typedef` avoids confusion with `go/types` and `sqltype` (§7 decision 6).

## Inventory (re-measured in 15a)

First measured at `75fe0d710`; re-measured with the reach tool at
`d27607c9c` and after 15a. After 15a: 56 files, about 10.5k lines
(`domain*`, `enum*`, `composite*`, `range_*`, `collation*`, `conversion*`,
`typed_*`, `type_*`, `types.go`); 157 of 299 declarations are clean. The
domain-default files (`PG18-DDL-DOMAIN-DEFAULT`) are mixed:
`domain_default.go` is 5 of 11 clean (general column-default records, not
domain code); `domain_default_restore.go` is 16 of 20 clean (the replay
codec, Dump/Restore helpers), but `captureDomainReplayNode` needs
`literalExpression` (step 10).

**Clean files:** `collation_dump.go` (72), `collation_remap.go` (35),
`collation_validation.go` (61),
`conversion_dump.go` (153), `domain_alter_discovery.go` (156),
`domain_alter_index_outputs.go` (74), `domain_remap.go` (544), and from 15a
`conversion_routines.go`, `conversion_probe.go`, `domain_builtin_seeds.go`,
`enum_order.go`, `type_provider_validation.go`, `typed_shape.go`. The slot
structs in `domain_alter_stage.go`, `typed_stage.go` and
`catalog_type_journal.go` are clean; their `CatalogStage`/`catalogChanges`
accessors stay in the engine.
Step 9b moved `conversion_descriptions.go`, `builtinCollationVersion` and
`collationActualVersion` into `pgcatalog`.

**Clean declarations in mixed files:**

- `collation.go`: `collationName`, `collationSQLName`;
  `collation_names.go`: `collationDisplayName`; `collation_seed_dump.go`:
  `collationSeedDumpStatements`;
- `conversion_execution.go`: `conversionDefault`;
- `domain_dependencies.go`:
  `domainDependency`, `usageDependsOn`; `domain_program.go`:
  `domainCheckIdentity`, `domainCheckPhase`, `domainCheckProgram`;
  `domain_dump.go`: everything except `domainDumpStatement` (15 of 16:
  `catalogColumnDeclaration`, `catalogLiteral*`, `catalogRegclassOutput`,
  `catalogTypeDeclaration`, `catalogTypeDeclarationAt`, `defaultDomainACL`,
  `dumpDomain`, `domainReplayNames` and its methods); `domain_alter_dump.go`:
  `restoreDomainAlter`, `restoreDomainOwnerBoundary`, `restoredDomainOwner`;
  `domain_input.go`: `captureDomainInputProviders`,
  `validateCapturedInputBinding`;
- `enum_generation.go`:
  `enumReownVersions`, `enumSafety`;
- `composite.go`: `compositeOpenError`, `containsRowType`,
  `rowRewriteError`; `composite_dump.go`: `compositeDumpStatement`;
- `typed_inheritance.go` (323 of 338 lines): `finishTypedInheritance`,
  `typedClosure`, `typedInheritedActions`, `typedPropagationOrder`,
  `typedShapeActions`, `typedSortedAddresses`, `typedTransformAttributes`;
  `typed_mutation.go`:
  `typedColumnAction`, `typedRelationMutation`; `typed_control.go`:
  `temporaryTypedReplacementError`;
- `types.go` (after 15a only the provider and helpers):
  `canonicalDeclarationTypeName`, `relationTypeNameOccupied`,
  `storedArrayElement`, `typeDiagnostic`, `typeProvider` (type and
  `ClassID`), `unsupportedColumnType`.

**Belongs to other steps** (do not move here):

- Expression nodes (`fieldExpression`, `rowExpression`, `unnestExpression`,
  `domainValueExpression`): step 10.
- Binders (`composite_expression.go`, `composite_capture.go`,
  `enum_function.go`, `enum_diagnostics.go` binder methods, `range_more.go`
  and `range_operator.go` binders, `collation_names.go`'s binder method,
  `domain_input.go`'s `decodeLiteralInput`, the `Engine.resolve*Type`
  methods, now in `type_resolution.go`): step 12.
- Domain check evaluation (`EvalContext.CheckDomain`, `prepareDomainChecks`,
  `evaluateDomainChecks`, ... in `domain_check.go`; `domain_alter.go`'s
  `evaluateSelectedDomainCheck`, `validateSelectedDomainRows`): step 11.
- `StatementDescription` with `temporalConstant` and
  `refreshableTemporalCapture` (`statement_description.go` since 15a): part
  of the public facade; step 12 or 13 (§7 decision 11).
- Drop orchestration (`dropProvider.dependencyClosure`,
  `expressionDependencies`, `compositeFieldChecks`, ...): the postponed
  `table` package.

**Blockers to note:**

- `typeProvider.Validate` is dirty through `intarrayProvider.
  ValidateExtension`, `validateDomainCheckDefinitions` (stored-SQL
  rendering, `scalarRenderer.sql`) and, since the domain defaults,
  `validateDomainDefault` → `sameGroupExpression` → `expressionBinder`. It
  is the catalog validator that 5e left in the engine ("Notes for later
  steps" in the catalog prompt file).
- `domainDumpStatement` reaches `scalarRenderer`. `catalogLiteralWithACLAt`
  no longer reaches a god struct, but still uses engine formatters (below).
- Frontier of the clean set (engine declarations it uses, measured with the
  14b frontier tool): `sqlError` (copy it, as in `routine`); `insertError`
  (`typeDiagnostic`); `databaseCollationActualVersion`; the inheritance and
  ALTER TABLE records `inheritanceChildren`, `inheritanceColumnConflict`,
  `addedColumn`, `columnTransformation` (typed inheritance and typed
  mutation, postponed `table`/`inheritance` packages); `literalExpression`,
  `coercionExpression` and `EvalContext` (step 10); `definitionReader` (now
  `catalog.DefinitionReader`, 12d.2),
  `definitionDescriptorAt`, `definitionArrayAt` (now `catalog.DescriptorAt`
  and `catalog.DefinitionArrayAt`) and the reg*/aclitem/regrole
  output and persistence formatters (`catalogLiteralWithACLAt`,
  `catalogTypeDeclarationAt`); `chooseAssociatedArrayName`,
  `compositeMembers` (`row_type.go`); `restoreScalar` (`control.go`);
  `routineAdmissionProgram` (`domainCheckPhase`); `pgIdentifier`
  (`collationDisplayName`); `quoteOrigin`, `structuralNull` (the column
  default records). 15b moves only what clears this frontier.

**After 15b** (measured at `75fd0ec29`): a fixed-point frontier pass left
73 declarations, which moved (see Status). The clean set above was too
generous in these places, which now stay in the engine:

- shared records, not type code: `dumpDomain` (dump graph),
  `domainDependency` (the general dependency record), `conversionDefault`
  (a `StatementScope` presentation cache), `remapRegclassDatum` (reg*
  persistence) and `collationDisplayName` (deparse; it needs `pgIdentifier`);
- blocked by the frontier: `collationDumpStatements`,
  `conversionDumpStatements` and `compositeDumpStatement` (`dumpDomain`);
  `catalogTypeDeclaration*`, `catalogColumnDeclaration`, `catalogLiteral*`,
  `catalogRegclassOutput` and `validateUsageCollation*` (definition reader,
  reg* formatters); `validateStoredCollationDependency`
  (`walkBoundExpression`); `reownCatalogScalarTypes` (reg* remaps);
  `reownDomainIndexOutputs` (index helpers); `restoreDomainAlter`
  (`restoreScalar`); `domainCheckPhase` and `domainCheckProgram`
  (`routineAdmissionProgram`); `enumReownVersions`; `typeProvider` (dirty
  `Validate`) and `validateTypeProviders`; `typedClosure`,
  `typedInheritedActions`, `typedPropagationOrder`, `typedRelationMutation`
  and `typedTransformAttributes` (inheritance and ALTER TABLE records); the
  column-default records of `domain_default.go`; `captureDomainReplayNode`
  and `domainReplayCapture` (`literalExpression`).

## Part A

### 15a — In place

1. Re-measure (domain defaults). Update the inventory.
2. Builtin seeding (`initializeBuiltinDomainChecks`,
   `initializeConversionRoutines`) becomes pure constructors installed by
   `newEngine`.
3. Separate clean helpers from `Engine` staging where a file mixes them
   (`collation_ddl.go`, `conversion_ddl.go`, `domain.go`, `enum_ddl.go`,
   `typed_table.go`), so the clean half can move as files.
4. Move `StatementDescription` out of `types.go` into a statement file
   (`statement_description.go`); it is not a type feature.
5. If 13b has not done it, turn `CatalogStage`'s domain and typed-table
   fields and the journal's domain and enum fields into slots (pattern 2).
6. With 14b in place, re-check the operator/type references and record which
   side owns shared helpers.

### 15b — Create `typedef` and move the clean set

1. Export in place, then move the clean set with the `movex` approach.
2. Check imports: `catalog`, `storage` (domain index outputs), `expr`,
   `routine` only if 15a recorded it, `authorization`, `ids`, `parser`,
   `sqltype`, `sqlstate` and the standard library.
3. Remove the aliases in the same sub-step if they are few; otherwise record
   them for 15d.

## Part B

### 15c — Refine the host (docs only), after 13d

Measure the DDL handlers' reach and write the host interface and the move
set: `stageCreate/Alter/Drop*Locked` for collations, conversions, domains,
enums and composites; `executeAlterDomain`, `executeEnumDDL`,
`executeAlterComposite`; `typed_mutation.go`, `alterTableOfLocked`,
`acquireTypedSources`; `collation_temporary.go`; `type_generation.go`;
domain index generations. Wait for approval.

### 15d — Move the DDL handlers

Follow 15c. Remove the remaining aliases.

## Invariants

- Type OIDs, array types, enum sort orders, domain check order and messages,
  collation versions and warnings stay identical.
- Dump and remap output stays byte-identical (`domain_remap.go`,
  `collation_remap.go`, the dump helpers).
- Typed-table propagation order is unchanged.

## Targeted tests

- Engine units: grep `*_test.go` for the moved identifiers and
  `domainRemap`, `collationDump`, `typedInheritance`.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  CREATE/ALTER/DROP DOMAIN, TYPE (enum, composite, range), COLLATION,
  CONVERSION, typed tables (`OF`), domain defaults and checks, enum
  diagnostics, and native Dump/Restore.
- `make bench` after 15a (builtin seeding).

## Documentation

- Create `internal/engine/typedef/README.md` (15b).
- `docs/TYPES.md` names internal files (`domain_check`, `enum_ddl`, ...);
  update it where it does. Also `docs/DIALECT.md` and the root `README.md`
  if they name moved files.
- `AGENTS.md`: add `typedef` to the shared subpackage bullet.

## Done when

Part A: the clean set lives in `typedef` and mixed files are split by owner.
Part B: the type DDL handlers live in `typedef`. The checks in §8.3 pass.

## Status

- [x] 15a — In place (`1045960f6`, `9e4924bbd`, `0add6a48b`, `92fd3299a`,
  `ed21b3bf3`, then the docs commit) *(racecheck/finalcheck skipped at user
  request)*
  - Re-measured with the reach tool at `d27607c9c` and after the work; the
    inventory above is current.
  - `1045960f6` (seeding): `builtinConversionProcedures(owner)` is a pure
    constructor; `builtinDomainCheckSeeds` and `builtinDomainDefaultSeeds`
    (`domain_builtin_seeds.go`) parse the `information_schema` CHECK and
    `time_stamp` DEFAULT seeds. `installBuiltinConversionRoutines` and
    `installBuiltinDomainChecks` in `builtin_catalog.go` install them at the
    old positions in `newEngine`, so order and OIDs are unchanged. Deviation:
    binding the domain seeds stays in the engine (it needs the expression
    binder, step 12); only the seed values are pure. One helper
    (`bootstrapAuthorization`) replaces the two duplicated bootstrap
    `AuthorizationView` literals.
  - `9e4924bbd` (splits): `conversionProbeResult` → `conversion_probe.go`,
    `validateTypeProviders` → `type_provider_validation.go`,
    `enumInsertionOrder` → `enum_order.go`, the typed-table shape checks →
    `typed_shape.go`, all unchanged. `collation_ddl.go` needed no split: 9b
    had already moved its clean half into `pgcatalog`.
  - `0add6a48b`: `StatementDescription`, `temporalConstant` and
    `refreshableTemporalCapture` → `statement_description.go` (statement
    state, not type code; the old clean list named
    `refreshableTemporalCapture` wrongly). The `Engine.resolve*Type` methods
    → `type_resolution.go`.
  - `92fd3299a` (stage slots, pattern 2): `domainAlterStage` (operation
    context, validation fence, index roots; `domain_alter_stage.go`) and
    `typedStage` (source type, observed closure; `typed_stage.go`) replace
    five `CatalogStage` fields. One accessor allocates each slot
    (`domainAlterState`, `typedState`); nil-safe getters make a nil slot the
    old zero state. `domainBases` (80 uses in 29 files) and `domainTargets`
    (written by collation, conversion, operator, drop and schema DDL) are
    shared dependency mechanisms, not domain state: they stay in the header.
  - `ed21b3bf3` (journal slots): `domainJournal` (validation fences,
    committed index roots) and `enumJournal` (created types, unsafe members)
    in `catalog_type_journal.go` replace four `catalogChanges` fields;
    `cloneCatalogChanges` clones each slot as a unit; preflight keeps its
    check order. `domainChanged` stays (derived from `domainTargets`).
    `catalog_transaction_storage.go` copies a journal by value, so writers
    replace a slot with a private copy first (`domainWritable`,
    `enumWritable`), which keeps the old copy-on-write behavior.
  - Operator/type references (item 6): the typedef clean set uses nothing
    from `routine`. The only `routine` use in type files is
    `routine.OperatorDependsOn` in drop orchestration (postponed `table`).
    Range and enum operator code is binder code (step 12). Type-declaration
    rendering (`catalogTypeDeclaration*`, used by 12 engine files including
    `operator_dump.go`) is typedef-owned; `routine` never imports `typedef`.
    15b therefore needs no `routine` import.
  - Tests: new seam tests `TestBuiltinConversionProcedures`,
    `TestBuiltinDomainSeeds` (`builtin_type_seed_test.go`) and
    `TestCatalogTypeJournalSlotsCopyOnWrite` (`catalog_type_journal_test.go`),
    written first and observed failing. Ten engine tests were rewritten
    mechanically to the slot paths; none moved or deleted.
  - Verification: build, vet (only the existing `composites`, `copylocks`
    and `lostcancel` findings), 183 targeted engine unit tests, a 625-test
    reference selection (domains, enums, composites, ranges, collations,
    conversions, typed tables, CREATE/ALTER/DROP TYPE, Dump/Restore) in quick
    mode and with `REFERENCE=1`, `make quickcheck` and `make deps` passed, on
    the base `3745e9011`; after rebasing on `e8598c73e` (pgcatalog 9c) and
    then `d1b32cd07` (sequence 17a, which also slotted `CatalogStage`; one
    struct-field conflict) build, the targeted units and `make quickcheck`
    passed again. With `REFERENCE=1` only
    `TestReferenceCompositeExprReturning/postgres` and
    `TestReferenceCompositeExprCorrelatedReturning/postgres` fail; they fail
    the same way on a clean `3745e9011` snapshot (PostgreSQL-side RETURNING
    drift; every stuntgres subtest passes). Startup bench, interleaved 8 runs
    each on a loaded host (load 32–55): median 1,229 µs/op before, 1,185
    µs/op after; about 5,108 allocs/op before and 5,026 after (the conversion
    routines are one slice instead of 84 allocations), 1.64 MB/op on both.
    Samples above 1 ms come from host contention (an unloaded run earlier
    measured 945 µs before and 930 µs after). Racecheck and finalcheck
    skipped at user request.
- [x] 15b — `typedef` created and clean set moved (`d4d99a162`,
  `5d3f4af95`, `b67b62d22`, then the docs commit) *(racecheck/finalcheck
  skipped at user request)*
  - Re-measured at `75fd0ec29` with the 15a frontier tool, iterated to a
    fixed point: a declaration stays if it uses any engine declaration
    outside the set except `sqlError`/`insertError`. 73 declarations moved;
    "After 15b" in the inventory lists what stays and why.
  - `d4d99a162` (split): the moving declarations of 23 mixed files were
    moved unchanged into whole files (for example `domain_replay_codec.go`,
    `type_journal_slots.go`, `typed_inheritance_plan.go`), so the move takes
    whole files.
  - `5d3f4af95` (export in place): the `movex` member mode capitalized the
    123 names, fields and methods that engine code uses, otherwise unchanged.
  - `b67b62d22` (move): 30 files (about 2.1k lines) moved into
    `internal/engine/typedef`; `movex` qualified the engine references in
    117 files. `typedef` imports `authorization`, `catalog`, `expr`, `ids`,
    `storage`, `parser`, `sqlstate`, `sqltype` and the standard library; no
    `routine`. `sqlError` and `insertError` got private copies in
    `errors.go`, as in `routine`. Beyond the 15a table: the pure row-type
    helpers `ChooseAssociatedArrayName` and `CompositeMembers` moved (the
    replay names and `ReownDomainTypes` need them). Cleanups while moving:
    the seed record fields are `ID` (not `Id`), the seed record types are
    exported because exported constructors return them, and the private
    conversion table is read through the constant `BuiltinConversionCount`
    (the engine only needed its length).
  - Tests: moved `TestBuiltinConversionProcedures` and
    `TestBuiltinDomainSeeds` (`typedef/builtin_seeds_test.go`),
    `TestDomainDefaultReplayPairingInternal`
    (`typedef/domain_replay_codec_test.go`) and `TestPGEnumSortInsertion`
    (`typedef/enum_order_test.go`), unchanged except for qualification. The
    other engine tests were only qualified mechanically; none deleted. No
    new seam tests: the move adds no seam.
  - Verification: build, vet (only the existing `composites`, `copylocks`
    and `lostcancel` findings), 127 targeted engine and `typedef` unit tests
    after every commit, the 625-test 15a reference selection in quick mode
    and with `REFERENCE=1`, `make quickcheck` and `make deps` passed on the
    base `75fd0ec29`. With `REFERENCE=1` only the known oracle-side drift
    `TestReferenceCompositeExprReturning/postgres` and
    `TestReferenceCompositeExprCorrelatedReturning/postgres` fail (as in
    15a; every stuntgres subtest passes). Layering: `go list` shows no
    engine subpackage importing the engine, and `typedef` does not import
    `routine`. Startup bench, interleaved 8 runs each on a loaded host (load
    18–23): median 980 µs/op before, 1,037 µs/op after, with samples on both
    sides from 852 to 1,476 µs/op; 5,025 allocs/op and about 1.64 MB/op on
    both. The move does not change allocations; the time difference is host
    noise. Racecheck and finalcheck skipped at user request.
- [ ] 15c — Host refined (docs only; after 13d)
- [ ] 15d — DDL handlers moved

Outstanding aliases: none (15b qualified every reference; no type aliases).

Notes for later steps:

- Step 16 (`extension`): `intarray` validation runs through
  `typeProvider.Validate`; keep that seam usable from `extension`.
- Moving `typeProvider.Validate` itself needs a stored-SQL rendering seam
  (catalog prompt file, notes for later steps).
- The slot structs now live in `typedef` (`typedef.DomainAlterStage`,
  `TypedStage`, `DomainJournal`, `EnumJournal`, with exported fields); their
  accessors on `CatalogStage` and `catalogChanges` (`domainAlterState`,
  `typedState`, `domainWritable`, `enumWritable`) stay in the engine until
  13d, or become typedef functions taking the slot.
- 15c/15d: the collation, conversion and composite dump statements,
  `domainDumpStatement` and type-declaration rendering need a dump seam (a
  dump record below the engine, and the definition reader and reg*
  formatters behind an interface); `operator_dump.go` waits for the same
  seam (routine prompt file). `typeProvider` waits for the stored-SQL
  rendering seam.
- 13a/13b: `conversionDefault` stayed in the engine as the
  `StatementScope` presentation cache record; move it with that slot.
- When the DDL handlers move, unexport the `typedef` names that nothing
  outside the package uses any more.
- 13b: the domain, typed-table and enum slots exist; skip them. The journal
  fence list of 13b item 2 must keep `domainJournal.validations` first in
  `catalogReadPreflightLocked`.
- `reference/type_resolution/comparison-ledger.md` still names
  `internal/engine/types.go` for the declaration/cast storage rules, which
  are now in `type_resolution.go`; `reference/` is read-only during the
  split, so fix it in a reference-owning task.
