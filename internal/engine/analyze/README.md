# internal/engine/analyze

Statement binding. Today it holds the bound form of a statement (`Binding`),
its per-kind payloads and the capture groups that views, rules, policies and
prepared statements read, plus the vocabulary the binder and its host speak:
the binding authority, the held description's inputs, name-lookup kinds and
selections, and the retargeting of captured identifier inputs. It also
holds the clean analysis helpers that the binder and engine code share
(coercion and collation derivation, input error positions, the held
signature check). The expression binder itself, its field groups and the `analyzeHost` seam still live in the
engine; they move here in step 12d.6. Since 12d.2 the binder names no engine
type for its authority, prepared inputs or captured inputs. The package sits at layer L8 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`pgcatalog`, the Part A content of `sequence` and `typedef` (§3 rule 6),
`parser`, `sqlstate`, `sqltype` and the standard library, and never the
engine or `session`. See
[ENGINE-SPLIT-analyze.md](../../../docs/ENGINE-SPLIT-analyze.md) (step 12).

## What it owns

- **`Binding`** (`binding.go`): the header every statement kind may use
  (target relation, storage and ref, view write, targets, used columns, the
  query, CTEs and query sources, snapshot demands, CURRENT OF, predicate,
  RETURNING, dependencies, parameters and expressions), the kind payloads
  and the capture groups. `ParameterInfo` is the inferred parameter list;
  the root package re-exports it to the driver. `*Binding` implements
  `plan.CTEMutation`, so a data-modifying CTE carries its binding.
- **Kind payloads** (`binding_groups.go`, `payloads.go`, `create_table.go`):
  `InsertBinding`, `DeleteBinding`, `CreateTableBinding` (held by value),
  `UpdateBinding` with its `AssignmentGroup`s, `MergeBinding` with its
  `MergeArm`s, `Returning`, and the CREATE TABLE records
  (`ColumnDeclaration`, `InheritanceInput`, `LikeInput` with `LikeMask`,
  `UniqueConstraint`).
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture` (the
  rendering captures keyed by `ViewLiteralPath`, with view columns,
  namespaces and composite occurrences), `PreparedCaptures` (temporal,
  composite, sequence and regtype constants a prepared statement
  re-resolves), `ResolverInputs` (the statement input resolvers, captured by
  `NewResolverInputs`), `RuleBinding`, and `DefaultOrigin` (where a default
  expression came from).
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under. The binder reads only its catalog (`ReadCatalog`) and
  hands it back to the host for names, privileges, definition reads and
  captures. The engine implements it over its authorization view (a stored
  authority) and over the same view read through the operation's current
  definition reader (a binding authority, from `BindingAuthority`).
- **`Expected`** (`expected.go`): what a held statement description gives its
  re-analysis (fixed parameters and results, catalog owner and epoch,
  temporal, composite, sequence and catalog-identifier captures, plan owner).
  The binder holds it by value; `Held` marks a held description.
- **Lookup vocabulary** (`lookup.go`): `LookupKind`, the provider family a
  search-path lookup serves, and `NamespaceSelection`, the namespaces a name
  lookup searches.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget`
  and the `RegtypeRetarget`, `OperatorRetarget`, `RoutineRetarget` and
  `RegTSRetarget` interfaces, which point the engine's captured resolvers in
  an input environment at one literal's position and sinks.
- **ON CONFLICT clause** (`insert_conflict.go`): `InsertConflict`, the bound
  clause (inference `ConflictKey`s and predicate or named constraint, read
  manifests, the DO UPDATE action and its RETURNING), held by
  `InsertBinding.Conflict`. `Clone` copies the header for one command, as
  rule actions need; the analyzed expressions stay shared.
- **Slots** (`binding_groups.go`): `RuleCTEsSlot` (the rule commands of a rule
  action's CTEs) and `ViewWriteSlot` (the view-write lowering; `Base` is the
  base relation the write lands on). The engine owns these values and
  recovers them with one checked helper each (`binding_slots.go`).
- **Shared statement helpers**, used by the binder and by engine DDL:
  `create_table_columns.go` (CREATE TABLE column declarations, column index,
  NOT NULL and unique constraints, `LikeConstraint`, LIKE options) and
  `statement_checks.go` (view output checks, INSERT targets and value errors,
  generated-column and inheritance conflicts, `TableKindError`,
  `GuardedCatalogRelation`, MERGE reachability). `statement_errors.go` holds
  the package's own `sqlError` and `insertError`.
- **Held signature** (`signature.go`): `Signature` and `CheckSignature`, the
  0A000 cached-plan check of a held description's parameters, results and
  catalog lifetime; `Expected.CheckLifetime` applies it to a replaced
  catalog owner or epoch during re-analysis. `StatementCatalogType`,
  `SameStatementUsage`, `SameStatementResultUsage` and `RefreshableTemporal`
  (which captured temporals a refresh re-inputs) belong to it.
- **Shared analysis helpers** (`coercion.go`, `errors.go`, `record_types.go`,
  `overlaps.go`, `operator_signature.go`): coercion properties and Info,
  collation derivation (`CollatedInfo`), the domain-cast annotation pair
  (`DomainCoercionInput`, `RestoreDomainCastCollations`), storage
  destinations, `ComparisonOperator`, literal and reg* input error positions
  (`InputErrorAt`, `RegtypeInputError`), `RecordTypeMapper`, the OVERLAPS
  inline cost model and `OperatorSignature`. Engine DDL, retag, remap and
  preflight code call them too; they read only their arguments.

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap and graft, MERGE arms, view-write INSERT).
  Payloads and groups are values; `UpdateBinding`, `MergeBinding`,
  `Returning` and the slot values are shared pointers, as before the move.
- **Nil slots mean absent.** The engine never stores a nil pointer in a slot,
  so `b.ViewWrite != nil` keeps its meaning; `b.Insert.Conflict` is a plain
  pointer, nil without ON CONFLICT.
- **One clause per command.** Execution writes the normalized predicate and
  the preflighted DO UPDATE action into the binding's clause, so a stored
  rule action clones it per command; arbiter and write state never live in
  the clause.
- **Capture maps** keep their contents and iteration rules: stored SQL
  (views, rules, policies, defaults, checks) deparses byte-identically.
- **No mutexes and no engine state.** The types are plain records; binding
  runs under `Engine.mu` in the engine.
- **Authorities.** A nil `Authority` means none is stored; the engine never
  stores a nil pointer. Only the host interprets a binding authority: engine
  code recovers stored authorities with `authorizationOf`, which rejects
  binding authorities. A retarget returns a copy; a nil sink in the target
  keeps the resolver's sink.

## What stays in the engine

The expression binder (`expressionBinder`, its groups in `binder_groups.go`,
`newExpressionBinder`, `analyzeHost`/`bindingHost`), `describe` and the
statement-kind binders; the authority adapters (`analysis_authority.go`:
`bindingAuthorityView`, `authorityOf`, `authorizationOf`), `expectedOf`
(`expected_inputs.go`), `signatureOf`/`checkStatementSignature` (the
`StatementDescription` wrappers of `CheckSignature`), the retag walker the
host lends the binder (`MapBoundExpression`), preflight
(`expression_preflight.go`) and the captured input
resolvers with their retarget methods (`captured_input_target.go`); rule rewriting (`rule_*.go`) and its CTE commands;
view-write lowering (`viewWrite`); the ON CONFLICT execution state
(`insertConflictState`: selected arbiters, unique-key checkpoints, the
affected count, the EXCLUDED frame and the write overlay, built per command
over the binding's clause); and the execution-side helpers over
the binding types: `physicalBinding`, `bindingDefaultOrigin`,
`evalBindingDefault`, `applyDefaultOrigin`, `capturedDomainReplayCell`,
`mergeHasVirtualReads`, `selectMergeArm`, `validateCompositeOccurrence` and
`remapLikeExpression`. Until 12e the engine names the moved types through the
aliases in `analyze_aliases.go`.

## Testing

`TestInsertConflictCloneOwnsTheClauseHeader` covers `InsertConflict.Clone`
and `TestExpectedCheckLifetimeFencesCatalogTypes` the held lifetime check.
The binding types are otherwise built by the engine's binder, so their tests
stay in the engine. The seams have engine unit tests: slots, binding
authorities, held inputs, captured input retargeting and the host's rule
check and expression mapping:

```sh
make check RUN='^(TestInsertConflictCloneOwnsTheClauseHeader|TestExpectedCheckLifetimeFencesCatalogTypes|TestBindingSlotsKeepNilAbsent|TestViewWriteSlotReportsBaseRelation|TestAnalysisHostReportsRelationRules|TestAnalysisHostMapsBoundExpressions|TestBindingAuthorityOverlaysOperationDefinition|TestExpectedOfCopiesHeldInputs|TestCapturedInputsRetargetOnlyTheirSinks)$'
```

ON CONFLICT execution state over the clause: `TestRuleConflictInstantiation`,
`TestRuleCTEConflictInstantiation` and the `TestInsertConflict` engine units.

Engine units that build bindings: grep `internal/engine/*_test.go` for
`binding{`, `describe(` and the payload names. Public proof is the reference
selection of step 12, in quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Update|Delete|Merge|Inheritance|TableLike|TypedTable|Rule|View|Prepared|CTEDML|Polic|DumpRound|Restore)'
```
