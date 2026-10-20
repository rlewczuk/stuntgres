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

## Inventory (measured at `75fe0d710`)

47 files, about 9.5k lines (`domain*`, `enum*`, `composite*`, `range_*`,
`collation*`, `conversion*`, `typed_*`, `type_*`, `types.go`); 2,796 of
8,574 declaration lines are clean. `PG18-DDL-DOMAIN-DEFAULT` (`f4fbfb978`)
landed on `master` right after this measurement: it adds
`domain_default.go` (219 lines) and `domain_default_restore.go` (608) and
changes the domain files. Re-measure first; the restore half is a Dump/Restore
helper (Part A if clean, otherwise `control`).

**Clean files:** `collation_dump.go` (72), `collation_remap.go` (35),
`collation_validation.go` (61),
`conversion_dump.go` (153), `domain_alter_discovery.go` (156),
`domain_alter_index_outputs.go` (74), `domain_remap.go` (544).
Step 9b moved `conversion_descriptions.go`, `builtinCollationVersion` and
`collationActualVersion` into `pgcatalog`.

**Clean declarations in mixed files:**

- `collation.go`: `collationName`, `collationSQLName`;
  `collation_names.go`: `collationDisplayName`; `collation_seed_dump.go`:
  `collationSeedDumpStatements`;
- `conversion_routines.go`: `builtinConversionRoutines`,
  `conversionRoutineName`; `conversion_execution.go`: `conversionDefault`;
  `conversion_ddl.go`: `conversionProbeResult`;
- `domain.go`: `validateTypeProviders`; `domain_dependencies.go`:
  `domainDependency`, `usageDependsOn`; `domain_program.go`:
  `domainCheckIdentity`, `domainCheckPhase`, `domainCheckProgram`;
  `domain_dump.go`: `catalogColumnDeclaration`, `catalogRegclassOutput`,
  `catalogTypeDeclaration`, `catalogTypeDeclarationAt`, `defaultDomainACL`,
  `domainReplayNames` and its methods; `domain_alter_dump.go`:
  `restoreDomainAlter`, `restoreDomainOwnerBoundary`, `restoredDomainOwner`;
  `domain_input.go`: `captureDomainInputProviders`,
  `validateCapturedInputBinding`;
- `enum_ddl.go`: `enumInsertionOrder`; `enum_generation.go`:
  `enumReownVersions`, `enumSafety`;
- `composite.go`: `compositeOpenError`, `containsRowType`,
  `rowRewriteError`; `composite_dump.go`: `compositeDumpStatement`;
- `typed_inheritance.go` (323 of 338 lines): `finishTypedInheritance`,
  `typedClosure`, `typedInheritedActions`, `typedPropagationOrder`,
  `typedShapeActions`, `typedSortedAddresses`, `typedTransformAttributes`;
  `typed_table.go`: `standaloneComposite`, `typedDirectActionsGuard`,
  `typedShape`, `validateTypedAttachment`; `typed_mutation.go`:
  `typedColumnAction`, `typedRelationMutation`; `typed_control.go`:
  `temporaryTypedReplacementError`;
- `types.go`: `canonicalDeclarationTypeName`, `refreshableTemporalCapture`,
  `relationTypeNameOccupied`, `storedArrayElement`, `typeDiagnostic`,
  `typeProvider` (type and `ClassID`), `unsupportedColumnType`.

**Belongs to other steps** (do not move here):

- Expression nodes (`fieldExpression`, `rowExpression`, `unnestExpression`,
  `domainValueExpression`): step 10.
- Binders (`composite_expression.go`, `composite_capture.go`,
  `enum_function.go`, `enum_diagnostics.go` binder methods, `range_more.go`
  and `range_operator.go` binders, `collation_names.go`'s binder method,
  `domain_input.go`'s `decodeLiteralInput`, the `Engine.resolve*Type`
  methods in `types.go`): step 12.
- Domain check evaluation (`EvalContext.CheckDomain`, `prepareDomainChecks`,
  `evaluateDomainChecks`, ... in `domain_check.go`; `domain_alter.go`'s
  `evaluateSelectedDomainCheck`, `validateSelectedDomainRows`): step 11.
- `StatementDescription` (`types.go`): part of the public facade; step 12 or
  13 (§7 decision 11).
- Drop orchestration (`dropProvider.dependencyClosure`,
  `expressionDependencies`, `compositeFieldChecks`, ...): the postponed
  `table` package.

**Blockers to note:**

- `typeProvider.Validate` is dirty through `intarrayProvider.
  ValidateExtension` and `validateDomainCheckDefinitions` (stored-SQL
  rendering, `scalarRenderer.sql`). It is the catalog validator that 5e left
  in the engine ("Notes for later steps" in the catalog prompt file).
- `domain_dump.go`'s `catalogLiteralWithACLAt` reaches the regrole and
  aclitem output formatters; `domainDumpStatement` reaches `scalarRenderer`.

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

- [ ] 15a — In place
- [ ] 15b — `typedef` created and clean set moved
- [ ] 15c — Host refined (docs only; after 13d)
- [ ] 15d — DDL handlers moved

Outstanding aliases: none yet.

Notes for later steps:

- Step 16 (`extension`): `intarray` validation runs through
  `typeProvider.Validate`; keep that seam usable from `extension`.
- Moving `typeProvider.Validate` itself needs a stored-SQL rendering seam
  (catalog prompt file, notes for later steps).
