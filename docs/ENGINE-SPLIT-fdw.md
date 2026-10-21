# Engine split, step 26: `internal/engine/fdw`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-fdw.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 26; §3 rules 6–8
  (`fdw` is an upper feature); §4.1 patterns 1, 2, 3 and 4; the §4 tests
  rule; §7 decision 11 (the driver imports the package directly).
- **Needs:** 26a: nothing. 26b: 26a. 26c: 26b and 20b (the shared `ddl`
  host). 26d: 26c. 26e: 26d, 24d.1 and 24d.2 (`table`'s handler decisions, because
  CREATE/ALTER FOREIGN TABLE reuses the CREATE and ALTER TABLE code). Read
  the catalog notes ("`foreignNamespaces` is replaced in the same Restore
  publication as `state`") and the session notes on the FDW participants
  (13b).
- **Effort:** high for 26a and 26b; xhigh for 26c; high for 26d and 26e.
- **Session size:** one sub-step per session.
- **Linear items:** 26a = item 94, 26b = item 95, 26c = item 96, 26d =
  item 97, 26e = item 98. Related: 20b = 61, 24d.1 = 89a, 24d.2 = 89b,
  29b = 108.
- **Tools:** run from the worktree root.
  - `reach`: `.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir .
    -files '<regexp on file base names>'` prints each declaration as
    `clean` or `DIRTY path`.
  - `frontier`: `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier
    -dir . -files a.go,b.go [-decls Name,Type.method]` prints the set's OUT
    edges (root declarations it uses) and IN edges (outside uses).
  - Rewrite tools: `movex`, `unalias`, `fieldrn`, `meth2fn` in
    `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `expspec`, `movespec`,
    `mv.sh` in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`. Never pipe
    a tool into `head`.
  - Measurements for this file are in
    `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/fdw/`.

## Goal

Foreign data wrappers (the registry of wrappers and validators the driver
installs, WRAPPER/SERVER/USER MAPPING DDL, IMPORT FOREIGN SCHEMA, GRANT on
foreign objects, foreign scans, the foreign write protocol and the foreign
transaction participants) become a feature package. Today the code is split
over two file prefixes, and the `foreign_` prefix collides with the
foreign-key files (`foreign_key*.go`, `foreign_action*.go`). After this
step `fdw_` means foreign data wrappers everywhere.

Foreign DML execution (`executeForeignCommandLocked`,
`executeForeignRowMutation`, `projectForeignReturning`, foreign TRUNCATE and
COPY) belongs to `dml` (step 29), which imports `fdw`.

## Inventory (measured at `c154e14fb`)

15 files, about 2.8k lines, 0.56k clean. `fdw` is an upper feature: it
imports `catalog`, `access`, `exec` and `session`; `dml` and the root import
it; it never imports `dml` or `table` until 26e.

| File | Lines | Clean | Content |
|---|---:|---:|---|
| `foreign_api.go` | 38 | 32 | public definition types (see "Public API") |
| `foreign_registry.go` | 111 | 47 | `ValidateForeignDataWrapper`, `ValidateForeignValidator`, `foreignFunctionName`, `RegistrationError`; `Engine.InstallForeignDefinitions` (dirty) |
| `foreign_options.go` | 46 | 37 | `transformForeignOptions` |
| `foreign_ddl.go` | 494 | 75 | `executeForeignDDL` (45), `stageForeignLocked` (244), `foreignProcedureLocked`; clean helpers `findForeign`, `foreignCatalogClass`, `foreignClass`, `foreignCommandTag`, `foreignLabel`, `foreignValidation`, `runForeignValidations` |
| `foreign_grant.go` | 120 | 6 | `executeForeignGrant` (103), `foreignGrantKind` |
| `foreign_import.go` | 232 | 32 | `executeForeignImport` (128), `foreignImportedTable`; clean `foreignHandlerFor`, `foreignImportExpression`, `importedOptions` |
| `foreign_table.go` | 422 | 64 | `executeForeignTableAlter` (254), `stageForeignTableLocked` (90); clean `foreignAlterAdmission`, `foreignImportError`, `foreignTableDeclaration`, `foreignTableSyntax` |
| `foreign_scan.go` | 101 | 33 | `Engine.captureForeignSourceLocked` (52); clean `foreignRequiredAttributes` |
| `foreign_scan_owner.go` | 31 | 0 | `ownForeignSource`, `finishForeignOwner` (scope methods) |
| `fdw_identity.go` | 81 | 67 | `foreignIdentitySet` and its hash helpers (all clean) |
| `fdw_modify.go` | 239 | 76 | the write callback protocol `foreignModification`; `planForeignModification` (scope) |
| `fdw_transaction.go` | 259 | 35 | `foreignTransaction`, `foreignParticipant`, `rollbackForeign`; `commitForeign`, `foreignPending`, `foreignSavepoint`, `enlistForeign`, `statementHost.TouchForeignScan` |
| `fdw_truncate.go` | 64 | 5 | foreign TRUNCATE (goes to `dml`) |
| `fdw_execute.go` | 505 | 47 | foreign DML execution (goes to `dml`); `foreignTableScope` slot |
| `fdw_hold.go` | 31 | 0 | held-cursor promotions (scope) |

### Dirty declarations, and why

- **Only through `AuthorizationView`** (about 90 lines; 26a cuts them): `foreignRef`, `foreignMappedRole`, `foreignMappingAuthority`,
  `foreignMappingDiagnostic`, `foreignOwnershipEligibility`
  (`foreign_ddl.go`), `foreignMappingFor` (`foreign_import.go`). They read
  the catalog, roles and session state and run `access` checks; an
  `access.View` plus an `access.Environment` is enough.
- **Only through `*CatalogStage`:** `foreignValidatorJob` uses only
  `stage.Base`; it can take a `catalog.Read`.
- **Only through the owner extension:** `commitForeign`, `foreignPending`
  and `foreignSavepoint` take a `*session.TransactionOwner` and reach the
  root `ownerExtension` (`transaction_extension.go`) for its
  `foreign foreignTransaction` field.
- **Only through `*StatementScope`:** `foreignModification.begin`,
  `export`, `returned` and `insertRows` use the scope for
  `enlistForeign`, the `foreignTables` slot, and the input and output
  environments.
- **Orchestration (stays in the root, or its decisions move in Part B):**
  `executeForeignDDL`, `stageForeignLocked`, `foreignProcedureLocked`,
  `executeForeignGrant`, `executeForeignImport`, `foreignImportedTable`,
  `executeForeignTableAlter`, `stageForeignTableLocked`,
  `captureForeignSourceLocked`, `InstallForeignDefinitions`,
  `enlistForeign`, `finishForeignModifications`, `finishForeignStatements`,
  `foreignUnlocked`, `ownForeignSource`, `finishForeignOwner`,
  `discardForeignHoldPromotions`, `statementHost.TouchForeignScan`, and the
  `foreignTables` slot accessors.

### Dispatch sites

`ForeignDDL` → `executeForeignDDL` (`execute.go:34`), which routes foreign
tables to `executeForeignTableAlter` and IMPORT to `executeForeignImport`
(`foreign_ddl.go:137`, `:140`); `GRANT` on a foreign kind →
`executeForeignGrant` (`execute.go:138`); foreign DML →
`executeForeignCommandLocked` (`execute_bound.go:432`). Scans:
`captureForeignSourceLocked` from `view_source.go:31`, `fdw_execute.go:45`,
`fdw_truncate.go:23` and `copy.go:161`. Foreign COPY FROM:
`planForeignModification` (`copy.go:166`).

### State

- **`Engine` fields:** `foreignWrappers`, `foreignValidators`,
  `foreignNamespaces`, `foreignValueImporter`, `foreignValueExporter`.
  Written only by `InstallForeignDefinitions`. Read by `foreign_scan.go`,
  `control.go` (lines 30, 177, 452: Reset re-install and Dump namespaces),
  `principal_remap.go:284` (the Restore stage) and `catalog.go:1476/1536`
  (the Restore publication replaces `foreignNamespaces` together with the
  catalog state). `importTargets` sits next to them but is Restore state,
  not FDW state.
- **Transaction owner:** `ownerExtension.foreign foreignTransaction` (a
  value group of the root owner extension).
- **Statement scope:** `foreignTablesSlot *foreignTableScope` (`owner`,
  `cursor *sqlCursor`, `statements`, `modifications`, `holdPromotions
  []*sqlCursor`). It holds root cursor types, so the slot record stays in
  the root and names `fdw` types.
- **Commit and savepoint call sites:** `transaction_publication.go:124/132`
  and `:431/439` (`foreignPending`, `commitForeign`), `savepoints.go:137`
  and `transaction_finish.go:234` (`foreignSavepoint`),
  `transaction.go:921` (`rollbackForeign`).

### Public API

The repo-root driver package uses the FDW types and validation functions:

- `fdw.go:13–27` aliases `engine.ForeignDataWrapperDefinition`,
  `ForeignValidatorDefinition`, `ForeignImportRequest`,
  `ForeignColumnDefinition`, `ForeignTableDefinition` and
  `ForeignSchemaImporter`;
- `fdw.go:71` and `:92` call `engine.ValidateForeignDataWrapper` and
  `engine.ValidateForeignValidator`; `fdw.go:78–103` and
  `settings.go:64/68` call `engine.RegistrationError`;
- `driver.go:37–38` holds `[]engine.ForeignDataWrapperDefinition` and
  `[]engine.ForeignValidatorDefinition`; `driver.go:71` calls
  `Engine.InstallForeignDefinitions`.

`cmd/stuntgres` does not use them.

## Part A

### 26a — In place

Re-measure first with
`reach -files '^(fdw_|foreign_(api|ddl|grant|import|options|registry|scan|scan_owner|table)\.go)'`.
Then, one commit per numbered item:

1. **Renames (file moves only).** `foreign_api.go` → `fdw_api.go`,
   `foreign_ddl.go` → `fdw_ddl.go`, `foreign_grant.go` → `fdw_grant.go`,
   `foreign_import.go` → `fdw_import.go`, `foreign_options.go` →
   `fdw_options.go`, `foreign_registry.go` → `fdw_registry.go`,
   `foreign_scan.go` → `fdw_scan.go`, `foreign_scan_owner.go` →
   `fdw_scan_owner.go`, `foreign_table.go` → `fdw_table.go`. Rename the
   matching tests the same way (`foreign_test.go` → `fdw_test.go`,
   `foreign_dependency_publication_test.go` →
   `fdw_dependency_publication_test.go`). Leave every `foreign_key*` and
   `foreign_action*` file alone: they are foreign-key code.
2. **Registry record** (pattern 1). Group the five `Engine` fields into one
   `foreignRegistry` struct (`wrappers`, `validators`, `namespaces`,
   `importValue`, `exportValue`) held by value as `Engine.foreign`. Give it
   the methods that touch only it: lookups by handler and validator name,
   `namespaceName(address)`, and `install(...)` for the part of
   `InstallForeignDefinitions` that does not write the catalog. Keep
   `catalog.go`'s Restore publication replacing `namespaces` in the same
   step as the catalog state. No mutex: `Engine.mu` guards it as before.
3. **Authority cuts.** `foreignRef`, `foreignMappedRole`,
   `foreignMappingDiagnostic`, `foreignMappingFor` take `access.View`;
   `foreignMappingAuthority` and `foreignOwnershipEligibility` take
   `(access.View, access.Environment)`. Callers pass `auth.View, auth`.
   `foreignValidatorJob` takes `catalog.Read` instead of `*CatalogStage`.
4. **Participants.** `commitForeign`, `foreignPending` and
   `foreignSavepoint` become methods of `*foreignTransaction`. The root
   keeps thin wrappers that take the owner, lock `t.Mu` where they do
   today, and pass `&ownerExtensionOf(t).foreign`.
5. **Scan request** (pure function). Split `captureForeignSourceLocked`:
   a new `foreignScanRequest(view access.View, principal
   authorization.PrincipalRef, relation *catalog.Relation, uses []int)
   (*catalog.ForeignProcedure, catalog.ForeignRequest, error)` builds the
   handler and request from the catalog and roles; the root method keeps
   the authorization capture, the importer/exporter check,
   `ownForeignSource` and the `exec.Source` construction.
6. **Write protocol capability** (pattern 3). `foreignModification.begin`,
   `export`, `returned` and `insertRows` take a small interface instead of
   `*StatementScope`:

   ```go
   type foreignWriteRuntime interface {
       Enlist(ctx context.Context, wrapper catalog.ForeignDataWrapper, request catalog.ForeignRequest, writing bool) (catalog.ForeignTransaction, error)
       TrackModification(*foreignModification)
       Input() sqltype.InputEnvironment
       Output() sqltype.OutputEnvironment
   }
   ```

   Implement it on `statementHost` (the root adapter of §7 decision 8),
   with a compile-time assertion. `Enlist` is today's
   `StatementScope.enlistForeign` (`fdw_transaction.go:66`).

Re-run `reach`. Every declaration listed under "Move set" below must be
clean. If one is not, stop and record the path under "Notes for later
steps".

### 26b — Create `fdw` and move the clean set

**Move set** (about 0.9k lines after 26a):

- `fdw_api.go`: the public definition types, renamed without the stutter:
  `fdw.WrapperDefinition`, `ValidatorDefinition`, `ImportRequest`,
  `ColumnDefinition`, `TableDefinition`, `SchemaImporter`. Confirm the
  names in this sub-step (§7 decision 12).
- `fdw_registry.go`: `ValidateWrapper`, `ValidateValidator` (today
  `ValidateForeignDataWrapper`, `ValidateForeignValidator`),
  `foreignFunctionName`, and the `foreignRegistry` record as
  `fdw.Registry` with its methods. `RegistrationError` stays in the root:
  settings registration uses it too (§7 decision 11).
- `fdw_options.go`, `fdw_identity.go` (whole files).
- `fdw_modify.go`: `foreignModification` and its methods,
  `foreignModifyAdmission`, `foreignOperationSupported`, the
  `foreignWriteRuntime` interface (exported as `fdw.WriteRuntime`).
- `fdw_transaction.go`: `foreignTransaction` (with the participant
  methods of 26a item 4), `foreignParticipant`, `foreignParticipantKey`,
  `foreignCapabilityError`, `foreignMarkName`, `rollbackForeign`.
- `fdw_ddl.go`: `findForeign`, `foreignCatalogClass`, `foreignClass`,
  `foreignCommandTag`, `foreignLabel`, `foreignValidation`,
  `runForeignValidations`, `foreignValidatorJob`, and the cut helpers
  `foreignRef`, `foreignMappedRole`, `foreignMappingAuthority`,
  `foreignMappingDiagnostic`, `foreignOwnershipEligibility`.
- `fdw_grant.go`: `foreignGrantKind`.
- `fdw_import.go`: `foreignHandlerFor`, `foreignImportExpression`,
  `importedOptions`, `foreignMappingFor`.
- `fdw_scan.go`: `foreignRequiredAttributes`, `foreignScanRequest`.
- `fdw_table.go`: `foreignAlterAdmission`, `foreignImportError`,
  `foreignTableDeclaration`, `foreignTableSyntax`.
- `fdw_truncate.go`: `foreignTruncateGroup` (`dml` uses it later).

**Not in the move set:** `fdw_execute.go` (`foreignIdentityEqual`,
`foreignMutationRow` and `projectForeignReturning` are clean, but they are
foreign DML execution; leave them for `dml` 29b), the scope slot record
`foreignTableScope`, and everything listed as orchestration above.

Steps:

1. Re-run `frontier` on the move set. OUT may name only `access`,
   `authorization`, `catalog`, `exec`, `ids`, `parser`, `session`,
   `sqlstate`, `sqltype` and the standard library, plus `sqlError` (give
   the package its own `errors.go`).
2. Export what the root still uses (`expspec`), then move (`movex`,
   `mv.sh`). Qualify every root reference directly; no aliases.
3. Update the driver: `fdw.go` aliases `fdw.WrapperDefinition` and the
   other public types instead of the `engine.` ones, and calls
   `fdw.ValidateWrapper`/`fdw.ValidateValidator`; `driver.go:37–38` hold
   `[]fdw.WrapperDefinition` and `[]fdw.ValidatorDefinition`.
   `Engine.InstallForeignDefinitions` stays a root method and takes the
   `fdw` types. The repo-root aliases (`stuntgres.ForeignDataWrapperDefinition`
   and so on) are the public API: keep their names unchanged.
4. Move the unit tests that use only moved code (`fdw_identity_test.go`,
   the option and validation cases) into `internal/engine/fdw`. Tests that
   need an engine fixture stay in the root and are rewritten to the new
   names (§4 tests rule).

Suggested commits: (1) exports in place, (2) move with qualified
references, (3) driver imports, (4) tests, (5) docs.

## Part B

### 26c — Refine the host (docs only; linear item 96)

Re-measure `executeForeignDDL`, `stageForeignLocked`,
`foreignProcedureLocked`, `executeForeignGrant` and `executeForeignImport`,
write the host interface, the move set and the commit sequence into this
file, then wait for approval. Three questions were open: (1) does
`fdw.Host` live on the shared `ddl` adapter (20b) or its own; (2) which
part of `executeForeignGrant`, which writes the journal rather than a
stage, is a pure decision; (3) how IMPORT stages its tables through
`stageForeignTableLocked` until 26e.

**26c refine (approved; measured at `3f8d7d950`).** The `frontier` tool
over `fdw_ddl.go`, `fdw_grant.go` and `fdw_import.go` (output in
`.cswd/tmp/PG18-ENGINE-SPLIT-FDW-REFINE/frontier-fdw.txt`):

- **Size.** `fdw_ddl.go` is 343 lines: `stageForeignLocked` (243) reads the
  actor, resolves the owner operand, applies the wrapper superuser gate,
  routes foreign tables to `stageForeignTableLocked`, then stages WRAPPER,
  SERVER and USER MAPPING create, alter and drop; `foreignProcedureLocked`
  (30) finds the handler or validator, checks EXECUTE and the return type.
  `fdw_grant.go` is 120 lines (`executeForeignGrant`), `fdw_import.go` 188
  (`executeForeignImport` 132, `foreignImportedTable` 50).
- **Engine edges (OUT), 43.** Almost all have a `ddl.Host` form already:
  `allocateOIDLocked`/`revisionLocked` are `Allocator`; `stage.Create`,
  `Replace` and `LookupName` are `Stage()`; `stage.Base` is `StageBase()`;
  `schemaNotice` is `ddl.Notice`; the two WARNINGs are `EmitNotice`;
  `auth.Check` is `ddl.Check`; `auth.Roles`, `Catalog` and `State` are
  `AccessView(auth)`; `e.creationNamespace` is
  `ExistingCreationNamespace`; `auth.CheckCreate` is
  `AccessView(auth).CheckCreate(auth, ns)` (not `CheckCreateIn`, which adds
  the trusted Restore exception). `foreignProcedureLocked`'s lookup is
  exactly `Host.LookupFunction` (`lookupNamespaces` plus
  `analyze.ProcedureInNamespaces`). `stage.domainBases` appends are
  `ddlHost.AddDomainBase` (from `typedef.StageHost`) and
  `stage.removeObjectLocked` is `ddlHost.RemoveObject` (from
  `ddl.SchemaHost`), both with the signatures `fdw` needs. The edges left
  are orchestration (`Engine.mu`, `authorizationLocked`, stage begin and
  discard, `replacementMu`, `ValidateCatalogStage`,
  `publishCatalogOperation`, `acquireDefinitionProviders`,
  `stageExpressionDependents`, `catalogChangeLocked`, the scope's
  `catalog` and `catalogAuthority`), `stageForeignTableLocked` (26e) and
  `grantWarning`.
- **Outside uses (IN).** `executeForeignDDL` and `executeForeignGrant`: one
  production caller each (`execute.go`). `stageForeignLocked`: one test
  (`fdw_dependency_publication_test.go`). `stageForeignValidatorJob`: four
  calls in `fdw_table.go`.
- **Not needed** (the draft had them): `HandlerProcedure` (the lookup is
  `LookupFunction`; the EXECUTE and return-type checks are pure);
  `StageExpressionDependents` (the DROP roots go through
  `scope.stageExpressionDependents` after `Engine.mu` is released, so that
  call stays orchestration and the decision returns the roots); `Registry()`
  (no decision reads the definitions).

Decisions:

1. **`fdw.Host` lives on the shared `ddlHost`** (question 1), like
   `routine.DDLHost`, `typedef.StageHost` and `ddl.SchemaHost`; a new root
   `fdw_host.go` holds `var _ fdw.Host = ddlHost{}`. It is `ddl.Host` plus:
   - `AddDomainBase(catalog.Descriptor)` and `RemoveObject(ref
     ids.ObjectRef, namespace bool) error`: already on `ddlHost`, same
     signatures, declared again in `fdw` (table 24c decided there is no
     general removal interface);
   - `StageForeignTable(auth ddl.Authority, s *parser.ForeignDDL, input
     sqltype.InputEnvironment) ([]Validation, []ids.ObjectAddress, error)`
     (question 3): CREATE and DROP FOREIGN TABLE through
     `stageForeignTableLocked`, which takes `(op, input)` instead of the
     scope. 26e removes it.

   `fdw` imports `ddl` for the first time; `ddl` sits below it.
2. **WRAPPER, SERVER and USER MAPPING staging.** `StageDDL(h Host, auth
   ddl.Authority, s *parser.ForeignDDL, input sqltype.InputEnvironment)
   ([]Validation, []ids.ObjectAddress, error)` keeps today's order: actor
   read, owner operand (`access.OwnershipRoleOperand`,
   `access.SchemaOwner`), wrapper superuser gate and its hint, the table
   route, then the unexported `stageDrop` (IF EXISTS notices, owner check,
   `RemoveObject`, roots) and `stageDefinition` (lookup, duplicate and IF
   NOT EXISTS, owner check, OID and revision, the class switch, options,
   rename, ownership change and its same-owner no-op, name conflict,
   validator job, `Create`/`Replace`, the two ALTER WARNINGs). A `nil, nil,
   nil` result still means "nothing to publish".
   `handlerProcedure(h, auth, name, signature)` replaces
   `foreignProcedureLocked` with the same 42883, 42501 and 42804 texts.
   `stageValidatorJob(h, wrapper, kind, options)` is `ValidatorJob(
   h.StageBase(), ...)` plus `h.AddDomainBase`; the root
   `stageForeignValidatorJob` stays for `fdw_table.go` until 26e. The root
   `stageForeignLocked` keeps its signature as a thin wrapper that builds the
   `ddlHost` (the test calls it); `executeForeignDDL` stays orchestration.
3. **GRANT gets no host** (question 2). It holds `Engine.mu` throughout,
   never stages and writes the transaction journal. The pure part is
   `PlanGrant(ctx context.Context, view access.View, s *parser.ObjectGrant,
   pending map[ids.ObjectAddress]catalog.Descriptor, revision func()
   (ids.DefinitionRevision, error)) ([]GrantChange, []sqlstate.Error,
   error)`, following 28a's `objectGrantPlan`: grantor, privilege and
   grantee checks, actor and role closure, then per target the lookup
   (over `pending` and its own earlier changes, so a repeated name sees the
   previous change as today), grant source, 42501, warning, the
   `available == 0` skip, `ChangeObjectACL`, the unchanged skip, the
   revision at the same point, `ValidateACL`. `GrantChange` is the address
   and the new `*catalog.ForeignObject`. Warnings raised before an error
   come back with it. The root keeps the argument check, the lock,
   `authorizationLocked`, the journal clone, emitting the warnings (then the
   error), `catalogChangeLocked` per change in order, and setting
   `scope.catalog` and `scope.catalogAuthority`.
4. **`grantWarning` goes down to `authorization`** as `GrantWarning`: the
   role, database and FDW grants share it and `fdw` cannot import `role`
   (§3 rule 6). It is already pure and `authorization` already imports
   `sqlstate`. It leaves 28b's move set; whichever of 26d and 28b runs first
   moves it.
5. **IMPORT FOREIGN SCHEMA** (question 3). `PlanImport(h Host, auth
   ddl.Authority, s *parser.ForeignDDL) (SchemaImporter, ImportRequest,
   error)`: server and USAGE, creation namespace and CREATE, wrapper
   (`ids.ErrCatalogReplaced`), handler, the importer capability (HV00P),
   mapping, options, role, the request; it records the five bases (server,
   wrapper, mapping, handler procedure, namespace) with `AddDomainBase`.
   `ImportSelected(s *parser.ForeignDDL, name string) bool` is the LIMIT
   TO/EXCEPT selection. `ImportedTable(ctx context.Context, definition
   TableDefinition, s *parser.ForeignDDL) (*parser.ForeignDDL, error)` is
   today's `foreignImportedTable` with the context instead of the scope.
   The root begins the stage before `PlanImport` (`BeginCatalogStage` has no
   side effects; the base is the same `auth.Catalog`), and keeps the
   `replacementMu` release, the importer callback, the per-definition
   context check, relock, re-authorization, epoch check and
   `stageForeignTableLocked`, the `ImportTable` stamp, `RunValidations`,
   `acquireDefinitionProviders`, validation and publication.
6. **Callbacks.** The host never runs one. Validator jobs and the importer
   still run in the root after `Engine.mu` and `replacementMu` are released.
7. **Errors.** Every message, SQLSTATE, hint and notice stays identical and
   in the same order, including the foreign mapping diagnostics and the
   warnings emitted before a later GRANT error.

**Move set (26d; new names).** Into `fdw`:

- `host.go`: the `Host` interface.
- `stage.go`: `StageDDL`, `stageDefinition`.
- `stage_drop.go`: `stageDrop`.
- `handler.go`: `handlerProcedure`, `stageValidatorJob`.
- `grant.go`: `PlanGrant`, `GrantChange` (next to `GrantKind`).
- `import.go`: `PlanImport`, `ImportSelected`, `ImportedTable` (next to
  `HandlerFor`, `ImportExpression`, `ImportedOptions`, `MappingFor`).

Into `authorization`: `GrantWarning`.

**Stays in the root.** `Engine.mu`; `executeForeignDDL` (argument check,
table ALTER and IMPORT routes, authority capture, stage begin and discard,
DROP dependents, `replacementMu` release, validations, publication);
`stageForeignLocked` as a wrapper; `executeForeignGrant` as described;
`executeForeignImport` as described; `stageForeignTableLocked`,
`executeForeignTableAlter` and `stageForeignValidatorJob` (26e);
`schemaNotice` (`fdw_table.go` still uses it).

**Commit sequence (26d).**

1. `grantWarning` → `authorization.GrantWarning`; callers in
   `role_grant_plan.go`, `role_grant_objects.go`, `database_grant.go` and
   `fdw_grant.go`. Done in role 28b (`2492afb3f`); start at 2.
2. Seam: `fdw.Host`, `fdw_host.go` on `ddlHost` with its assertion,
   `StageForeignTable`, and `stageForeignTableLocked` over `(op, input)`.
   Unit test first, observed failing: `StageForeignTable` stages a foreign
   table through the stage and returns its validator jobs.
3. Port in place: root `stageForeignDDL(h, auth, s, input)`,
   `planForeignGrant`, `planForeignImport(h, auth, s)`,
   `foreignImportSelected` and `foreignImportedTable(ctx, ...)`, naming no
   `Engine`, `CatalogStage`, `AuthorizationView` or `StatementScope`.
4. Move them into `fdw` with `movex` and export the entry points; no
   aliases.
5. Docs: the `fdw` README (`Host`, entry points, what stays), this file's
   Status, the §8 row and the status page; `AGENTS.md` unchanged.

Proof for 26d: `^(TestFDW|TestOwnerExtension)` (also `RACE=1`) and
`^Test(ObjectGrant|DatabaseACL)` (the units that reach `GrantWarning`);
`^TestReferenceFDW`, `^TestReferenceDumpRoundTrip$`,
`^TestReferenceObjectGrant` and `^TestReferenceObjectACLDatabase$` in quick
and `REFERENCE=1` mode (the last is order-dependent on `master` too:
compare before reporting); quickcheck.

### 26d — Move the WRAPPER, SERVER, USER MAPPING, IMPORT and GRANT decisions

Follow 26c. The root keeps `executeForeignDDL`, `executeForeignImport` and
`executeForeignGrant` as orchestration: `Engine.mu`, stage begin and
discard, the `replacementMu` release before callbacks,
`ValidateCatalogStage`, `publishCatalogOperation`.

### 26e — Move the foreign-table decisions (after 24d)

CREATE/ALTER FOREIGN TABLE (`stageForeignTableLocked`,
`executeForeignTableAlter`) reuse the CREATE and ALTER TABLE machinery
(`schemaTableLocked`, `alterTypeCandidate`, `addColumn*`,
`dropColumnCandidate`, `cookAlterDefaultLocked`, ...). Once `table` owns
those decisions (24d), the foreign-table option and admission decisions
move to `fdw` and call `table`'s functions; the root keeps the
orchestration. Re-measure first; write a short refine note into this file
if the move set differs from 24d's result. Table 24c: until 26e,
`StageForeignTable` forwards to `table.StageCreateTable`; ALTER FOREIGN
TABLE's candidate calls (`alterTypeCandidate`, `addColumnCandidate`,
`generationCandidate`, `rebindAlterTypeChecks`, `cookAlterDefaultLocked`,
`bindColumnChecks(altering)`) become calls of the 24d.2 functions over an
`alterHost`, and 26e drops the forwarders.

## Invariants

- **Callbacks never run under `Engine.mu` or `replacementMu`.** Scan,
  modify, validator and importer callbacks run after `foreignUnlocked` or
  the `replacementMu` release. Foreign rollback uses
  `context.WithoutCancel`.
- **Commit order.** `commitForeign` runs after the fences pass, with
  `Engine.mu` and the session mutex released, before the local install. A
  later conflict returns `catalog.Conflict` (`remoteSettled`). A failed
  remote commit is never retried.
- **Publication.** `foreignNamespaces` (`Registry.namespaces`) is replaced
  in the same Restore publication as the catalog state (`catalog.go`), and
  Reset re-installs the same definitions (`control.go:30`).
- **Registration.** The driver's registration order, duplicate checks and
  error texts (`RegistrationError`) are unchanged; the registry is frozen
  after engine start.
- **Errors.** Every message, SQLSTATE, detail and hint stays identical,
  including foreign mapping diagnostics.
- **Dump.** SQL/MED objects are rejected by Dump today; that stays so.

## Targeted tests

- Engine units: `fdw_*_test.go` (6 files), `foreign_test.go`,
  `foreign_dependency_publication_test.go`. Grep `*_test.go` for
  `foreignWrappers`, `foreignValidators`, `foreignNamespaces`,
  `InstallForeignDefinitions`, `foreignModification`, `commitForeign`,
  `foreignSavepoint`. Add `RACE=1` for `fdw_transaction_test.go` and the
  concurrency selection below.
- Reference selections, quick mode and `REFERENCE=1` (never edit
  `reference/` or `smoke/`):
  - `make check RUN='^TestReferenceFDW'` (43 tests: scans, writes,
    imports, grants, mappings, transactions, validators, registration);
  - `make check RUN='^TestReferenceDumpRoundTrip$'` (Dump still rejects
    SQL/MED objects).

  Compare any `REFERENCE=1` failure with `master` before reporting it.
- Build the driver and `cmd/stuntgres` after 26b (`make build`); the
  public FDW aliases must keep compiling.

## Documentation

- Create `internal/engine/fdw/README.md` in 26b: what the package owns,
  the callback and commit rules above, what stays in the root (DDL
  orchestration, registration install, commit and savepoint call sites,
  the scope slot) and why, what goes to `dml`, and a `## Testing` section
  with the selectors above. Move the matching selectors out of
  `internal/engine/README.md`.
- `docs/DRIVER.md` and `README.md` name `ForeignDataWrapperDefinition`
  (the public alias); change them only where they name the engine type.
- `AGENTS.md`: add `fdw` to the brace list of the shared subpackage
  bullet. The file must not grow.

## Done when

Part A: the `fdw_` prefix is used only for foreign data wrappers; the
registry record, option and identity helpers, the write protocol, the
participants and the DDL helpers live in `fdw`; the driver imports `fdw`
for the public types. Part B: the WRAPPER, SERVER, USER MAPPING, IMPORT,
GRANT and foreign-table decisions live there too; `Engine.mu`, callbacks,
commit call sites and publication stay in the root. The checks in §8.3
pass.

## Status

- [x] 26a — In place (renames, registry record, authority cuts,
  participants, scan request, write capability) (`6cb327eb1`, `c3a7cf7e1`, `1ba975174`, `0c991e6c8`, `d7405f486`, `a02ccecb1`, then the docs commit).
  `reach` after 26a: 77 clean (48 before); every declaration in the 26b move
  set is clean. Verified with `^(TestFDW|TestOwnerExtension)` (also
  `RACE=1`), `^TestReferenceFDW` and `^TestReferenceDumpRoundTrip$` in quick
  and `REFERENCE=1` mode, and quickcheck; racecheck/finalcheck skipped at
  user request. Tests renamed: `foreign_test.go` → `fdw_test.go`,
  `foreign_dependency_publication_test.go` →
  `fdw_dependency_publication_test.go`; `fdw_modify_test.go` passes
  `s.foreignWrites()`. No test moved or deleted.
- [x] 26b — `fdw` created and clean set moved; the driver imports `fdw`
  (`6bce27045`, `f2df07054`, `fe35300c3`, `5e3147354`, then the docs commit). 14 files (about 1.4k lines with
  tests) in `internal/engine/fdw`; movex qualified 34 engine files; no
  aliases. Names settled (§7 decision 12): `WrapperDefinition`,
  `ValidatorDefinition`, `ImportRequest`, `ColumnDefinition`,
  `TableDefinition`, `SchemaImporter`, `ValidateWrapper`,
  `ValidateValidator`, `FunctionName`, `Registry`, `TransformOptions`,
  `IdentitySet`, `IdentityComparable`, `Modification`, `WriteRuntime`,
  `ModifyAdmission`, `OperationSupported`, `TruncateGroup`,
  `Transaction`, `Participant`, `ParticipantKey`, `Rollback`,
  `MarkName`, `CapabilityError`, `Find`, `Class`, `IsCatalogClass`,
  `Label`, `CommandTag`, `ObjectRef`, `GrantKind`, `MappedRole`,
  `MappingAuthority`, `MappingDiagnostic`, `OwnershipEligibility`,
  `MappingFor`, `Validation`, `ValidatorJob`, `RunValidations`,
  `HandlerFor`, `ImportExpression`, `ImportedOptions`, `ImportError`,
  `CheckTableDeclaration`, `TableSyntax`, `AlterAdmission`,
  `ScanRequest`, `RequiredAttributes`. Deviations: `fdw` keeps a private
  `registrationError` with the `RegistrationError` prefix (the settings
  precedent; `RegistrationError` stays in the engine);
  `foreignIdentityEqual` moved with the identity set (private
  `identityEqual`), since the set needs it; `targetRows` became the
  engine function `foreignTargetRows`; the driver change shares the move
  commit so every commit builds. Tests moved: `fdw_identity_test.go`
  and `TestFDWModifyIdentity` (`fdw/identity_test.go`),
  `TestFDWOptionsTransform` (`fdw/options_test.go`), the validation
  cases of `TestFDWRegistry` as `TestFDWValidateDefinitions`
  (`fdw/registry_test.go`). Rewritten: the engine tests that poked
  `foreignTransaction` fields (`fdw_transaction_test.go`,
  `fdw_hold_test.go`, `fdw_chain_test.go`, `fdw_modify_test.go`,
  `transaction_extension_test.go`) use `Enlist`, `Participant`,
  `HasParticipants`, `retireForeign`/`TakeCleanup` and
  `ownerHasForeign`, with the same assertions. No test deleted.
  Verified with `^(TestFDW|TestOwnerExtension)` (also `RACE=1`),
  `^TestReferenceFDW` and `^TestReferenceDumpRoundTrip$` in quick and
  `REFERENCE=1` mode, and quickcheck; racecheck/finalcheck skipped at
  user request; bench not run (no startup or hot-path change).
- [x] 26c — Host refined (docs only; the docs commit). Measured at
  `3f8d7d950`: 43 engine edges, nearly all covered by `ddl.Host` and two
  existing `ddlHost` methods. `fdw.Host` = `ddl.Host` + `AddDomainBase`,
  `RemoveObject` and one new `StageForeignTable`, on `ddlHost`; the draft's
  `HandlerProcedure`, `StageExpressionDependents` and `Registry()` are not
  needed. GRANT is a pure `PlanGrant` with no host; `grantWarning` goes to
  `authorization`; IMPORT is `PlanImport`, `ImportSelected` and
  `ImportedTable`. 26d move set and commit sequence written.
- [x] 26d — WRAPPER, SERVER, USER MAPPING, IMPORT and GRANT decisions moved
  (`743eb4d47`, `6fe2aeaa2`, `e942c6231`, then the docs commit). Step 1
  (`GrantWarning`) was already done by role 28b (`2492afb3f`). `fdw.Host`
  (`host.go`) on `ddlHost` (`fdw_host.go`); `stageForeignTableLocked` takes
  `(op, input)` instead of the scope. Moved into `fdw`: `StageDDL` with
  `stageDefinition` (`stage.go`) and `stageDrop` (`stage_drop.go`), which
  share an unexported `stageTarget` (class, name, mapping role, server and
  wrapper); `handlerProcedure` and `stageValidatorJob` (`handler.go`);
  `PlanGrant` and `GrantChange` with `GrantKind` (`grant.go`); `PlanImport`,
  `ImportSelected` and `ImportedTable` (`import.go`). The root keeps
  `executeForeignDDL`, `stageForeignLocked` (a one-line wrapper over
  `fdw.StageDDL`), `executeForeignImport` (it now begins the stage before
  `PlanImport`; `BeginCatalogStage` and `authorizeLocked` have no side
  effects) and `executeForeignGrant` (journal clone, warnings, then the
  error, `catalogChangeLocked` per change). `PlanGrant` reads the journal's
  descriptors and keeps its own earlier changes, so a repeated name still
  sees the previous change. No aliases. String literals unchanged (checked
  as a multiset). Tests added: `TestFDWHostStageForeignTable`
  (`fdw_host_test.go`, the seam, observed failing first) and
  `TestFDWImportSelected` (`fdw/import_test.go`); none moved or deleted.
  Verified with `^(TestFDW|TestOwnerExtension)` (also `RACE=1`),
  `^Test(ObjectGrant|DatabaseACL)`, `^TestReferenceFDW`,
  `^TestReferenceDumpRoundTrip$`, `^TestReferenceObjectGrant` and
  `^TestReferenceObjectACLDatabase$` in quick and `REFERENCE=1` mode, and
  quickcheck; racecheck/finalcheck skipped at user request; bench not run
  (no startup or hot-path change).
- [ ] 26e — Foreign-table decisions moved (after 24d)

Outstanding aliases: none yet.

### Notes for later steps

- Role 28c, for 28d: `OwnershipEligibility` goes down to `access` as
  `ForeignOwnershipEligibility` (REASSIGN OWNED needs it and `role` cannot
  import `fdw`); `stage.go` calls it there and the `fdw` README drops it.
- 26a deviations from the plan above, for 26b:
  - `foreignRegistry` has no lookups by handler or validator name: nothing
    reads the definitions except re-install. Its methods are `install`,
    `definitions` (Reset and the Restore stage call
    `InstallForeignDefinitions(e.foreign.definitions())`), `addNamespace`,
    `adoptNamespaces` (the Restore publication), `namespaceName`,
    `namespaceEntries` (Dump) and `codecs` (scans). `dumpSource` holds the
    registry value.
  - `foreignValidatorJob` also recorded the validator procedure in
    `stage.domainBases`, not only reading `stage.Base`. It now takes a
    `catalog.Read` and returns the procedure; the root wrapper
    `stageForeignValidatorJob` keeps the append. Only the pure function moves.
  - `foreignWriteRuntime` reuses `statementHost`'s existing `exec.Host`
    methods `EnlistForeign` and `Environments`; only
    `TrackForeignModification` is new. `StatementScope.foreignWrites()` returns
    the runtime.
  - `foreignTransaction` also gained `participant`, `enlist`,
    `hasParticipants`, `retire` and `takeCleanup`, so no root code reads its
    fields (`ownerHasForeign` replaces the `len(...participants)` checks). The
    root wrappers `commitForeign`, `foreignPending` and `foreignSavepoint` copy
    `ownerExtensionOrZero(t).foreign`; they never allocate an extension.
  - `foreignOwnershipEligibility`'s recipient check keeps the actor's
    `access.Environment`: `access.View.Check` reads it only for type
    diagnostics and temporary namespaces, never for a wrapper.
- 26d: follow the 26c refine above. The decisions use the moved helpers
  (`fdw.Find`, `fdw.MappingAuthority`, `fdw.ValidatorJob`, ...). Role 28b
  already moved `grantWarning` to `authorization.GrantWarning`.
- 26e: `fdw.Host.StageForeignTable` and the root `stageForeignValidatorJob`
  exist only for the foreign-table path (`fdw.stageValidatorJob` is the
  host form of the latter); 26e replaces both when
  `stageForeignTableLocked` and `executeForeignTableAlter` move, and drops
  `schemaNotice`'s last FDW users.
- 29b: `foreignTargetRows` (`fdw_execute.go`) is the engine function that
  replaced `foreignModification.targetRows`; it moves to `dml` with the
  rest of foreign DML execution and reads `fdw.Modification.Source` and
  `Request`.

- `dml` (29a/29b): foreign DML execution (`fdw_execute.go`,
  `fdw_truncate.go`'s scope methods, foreign COPY FROM in `copy.go`) moves
  to `dml` and imports `fdw` for the write protocol and participants.
  Today `fdw` and `dml` code call each other (`evaluateInsertQuery`,
  `evaluateUpdateAssignments` and `truncateScope` from the FDW side;
  `captureForeignSourceLocked`, `planForeignModification` and
  `foreignHandlerFor` from the DML side); after 26b the dependency points
  one way only.
- `view` (27) opens foreign scans through `captureForeignSourceLocked`
  (`view_source.go:31`); that stays a root method.
- `importTargets` is Restore state, not `fdw`'s; control 30b keeps it a
  root `Engine` field on the restore stage.
