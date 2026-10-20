# internal/engine/analyze

Statement binding. Today it holds the bound form of a statement (`Binding`),
its per-kind payloads and the capture groups that views, rules, policies and
prepared statements read, plus the vocabulary the binder and its host speak:
the binding authority, the held description's inputs, name-lookup kinds and
selections, and the retargeting of captured identifier inputs. The expression
binder itself, its field groups and the `analyzeHost` seam still live in the
engine; they move here in step 12d.6. Since 12d.2 the binder names no engine
type for its authority, prepared inputs or captured inputs. The package sits at layer L8 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
the Part A content of `sequence` (§3 rule 6), `parser`, `sqltype` and the
standard library, and never the engine or `session`. See
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
- **Slots** (`binding_groups.go`): `InsertConflictSlot` (the bound ON CONFLICT
  clause with its per-execution arbiter and write state), `RuleCTEsSlot` (the
  rule commands of a rule action's CTEs) and `ViewWriteSlot` (the view-write
  lowering). The engine owns these values and recovers them with one checked
  helper each (`binding_slots.go`).

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap and graft, MERGE arms, view-write INSERT).
  Payloads and groups are values; `UpdateBinding`, `MergeBinding`,
  `Returning` and the slot values are shared pointers, as before the move.
- **Nil slots mean absent.** The engine never stores a nil pointer in a slot,
  so `b.ViewWrite != nil` and `b.Insert.Conflict != nil` keep their meaning.
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
`bindingAuthorityView`, `authorityOf`, `authorizationOf`), `expectedOf` and
`checkExpectedLifetime` (`expected_inputs.go`) and the captured input
resolvers with their retarget methods (`captured_input_target.go`); rule rewriting (`rule_*.go`) and its CTE commands;
view-write lowering (`viewWrite`); the INSERT conflict clause and its
execution state (`boundInsertConflict`); and the execution-side helpers over
the binding types: `physicalBinding`, `bindingDefaultOrigin`,
`evalBindingDefault`, `applyDefaultOrigin`, `capturedDomainReplayCell`,
`mergeHasVirtualReads`, `selectMergeArm`, `validateCompositeOccurrence` and
`remapLikeExpression`. Until 12e the engine names the moved types through the
aliases in `analyze_aliases.go`.

## Testing

The package has no unit tests of its own yet: the binding types are built by
the engine's binder, so their tests stay in the engine. The seams have engine
unit tests: slots, binding authorities, held inputs and captured input
retargeting:

```sh
make check RUN='^(TestBindingSlotsKeepNilAbsent|TestBindingAuthorityOverlaysOperationDefinition|TestExpectedOfCopiesHeldInputs|TestCapturedInputsRetargetOnlyTheirSinks)$'
```

Engine units that build bindings: grep `internal/engine/*_test.go` for
`binding{`, `describe(` and the payload names. Public proof is the reference
selection of step 12, in quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Update|Delete|Merge|Inheritance|TableLike|TypedTable|Rule|View|Prepared|CTEDML|Polic|DumpRound|Restore)'
```
