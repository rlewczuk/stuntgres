# Engine split, step 3: `internal/engine/functions`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-functions.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 3; the diagnosis in
  §2.5; dependency rule 5 in §3.
- **Needs:** step 2 through 2b for F1 and F3–F5, and through 2c for F2.
- **Effort:** high for F1 and F4/F5; xhigh for F3 and for the first F2
  switch; high for the later F2 switches.
- **Session size:** F1 and F3 take one session each. F2 takes one or two
  sessions. F4/F5 cover one large family or a few small ones per session.
  This step spans many sessions; the Status section tracks where to resume.

## Goal

`functions` becomes the real home of builtin functions: plain nodes, their own
node operations, bind code that depends on a `functions.Binder` interface, and
a registry instead of the name if-chain. It stays a leaf library: it imports
`expr`, `ids`, `sqltype`, `parser` and `sqlstate`, never `catalog`, `storage`,
`lock`, `visibility` or the root engine.

## Sub-steps

### F1 — Remove generics (needs 2b)

- Each `X[E, C, P]` in `functions` becomes a plain `X` over `expr.Expr`,
  `expr.Context` and `expr.Info`. The context constraints
  (`LiteralContext[E]`, `BinaryContext[E]`, `CurrentDateTimeContext`, ...)
  fold into `expr.Context`. `functions.Info[P]` is replaced by `expr.Info`.
- In the engine, delete the instantiation suffixes (about 1,257 references at
  `a2252c0b1`) and the `functionInfo` conversions. Use tools, not hand edits.
- This is a broad rewrite: do not run it in parallel with another (§8).

### F2 — Adopt the node protocol, one switch per commit (needs 2c)

Step 2c converted the walk switch. Convert the others:

- retag (`expression_retag.go`);
- remap (`view_remap.go`);
- volatility (`expression_volatility.go`);
- deparse (`deparse_renderer.go`, `deparse_function.go`);
- node-tree output;
- generated-column admission (`generated.go`);
- `conditional_plan.go`;
- join legality.

Each switch gains one protocol branch (`case expr.Composite:` or a render
hook) and loses its per-function cases. Function nodes implement the protocol
methods next to their definitions. If a switch case encodes behavior that does
not fit the protocol, give the protocol a small, named hook rather than
keeping a special case, and record it in the `expr` README.

State after the first F2 session: every function node implements
`expr.Composite`, plus the optional hooks in the `expr` README; walk, retag,
remap, volatility, generated admission, conditional planning and join
legality have one protocol branch each.

State after the second F2 session: deparse (`deparse_renderer.go`) and
node-tree output (`deparse_tree.go`) read one `expr.Rendering` value per node
through the `expr.Renderable` hook.

State after the third F2 session: stored scalar SQL (`scalar_function_sql.go`
and the function cases of `default.go`) reads the same `Rendering`, extended
with `Replay`, and its helpers are gone.

State after the fourth F2 session: grouping identity, the grouping rewrite,
aggregate occurrence, preflight, index key display and the closed-DEFAULT
checks have one protocol branch each, and `Arguments()` no longer duplicates
`Children()`. View literal capture subsequently adopted explicit raw-kind
admission and written operands through `expr.Capturable`, with indexed access
for fixed receiver storage. Still listing function nodes: function sources
(the set cursor and classifiers) and explicit `SQLJSON` rendering.

Remaining F2 items (details in "Later-wave refresh" at the end of Status):
function sources, including the engine's `seriesExpression` and
`unnestExpression` markers; `SQLJSON` rendering; and a conditional closure hook
for the closed-DEFAULT check. Function sources remain broad.

The result-nullability cutover asks `expr.ResultNullability` at explicit
SELECT/RETURNING sites, independently of Composite. Nine owners answer beside
their definitions using immutable recipes only. No-claim is no write; source,
grouping/aggregate/window and OLD/NEW/MERGE precedence stays engine-owned.
Deparse/index-property claims remain SELECT-only, and wrappers/literal folding
retain their uncertainty. Item 4 has selected dual-mode, quickcheck and public
smoke proof; full racecheck and post-merge finalcheck were waived by the user.

### F3 — Binder interface and registry, with a pilot family

- Define `functions.Binder`, starting from the methods that function glue uses
  most: `info`, `typ`, `builtinRoutineInfo`, `AnalyzeArgument`,
  `functionSignatureErrorForTypes`, `builtinVisible`, `diagnosticTypeName`,
  `CoerceArgument`, `comparisonOperand`, `coerceConditional` and
  `builtinRoutineReference`. Re-measure. `*expressionBinder` implements it.
- Define `Definition` and `Lookup(name)` over a static table in `functions`,
  with no `init()` registration.
- Engine dispatch (`bindFunctionProvider` in `function.go`, `bindFunction` in
  `composite_expression.go`) consults `Lookup` at the exact point where the
  if-chain tests names today. Then it falls through to the existing chain.
  Resolution precedence must not change: user-defined functions,
  `search_path`, builtin visibility, and overload and ambiguity errors.
- Port one small Tier A family end to end (F4 then F5) in the same session, so
  the registry lands with a real entry.

### F4/F5 — Port in place, then move, family by family

- **F4.** Convert `func (b *expressionBinder) bindX(...)` into
  `func bindX(b functions.Binder, ...)` while it is still in the engine. The
  compiler then proves the interface is enough. Every new `Binder` method is a
  reviewed decision, recorded in the `functions` README: either a genuine
  analyzer capability, or a leak that the plan should remove. Prefer passing
  values to adding methods.
- **F5.** Move the ported family into `functions` and register it. Delete its
  engine branch, its glue file and its now-dead switch cases. Its unit tests
  move with it.
- Extension kernels move with their data files (for example
  `unaccent.rules`, embedded by `unaccent_provider.go`). Extension
  installation and procedure identity stay in the engine; installed functions
  reach their procedure identity through a `Binder` capability.
- Aggregates and windows: signatures, provider selection and state algorithms
  move. FILTER, DISTINCT, ORDER, grouping and window execution stay in the
  engine's grouping code. `aggregate_bind.go` is the largest single item;
  split it over several sessions if needed.
- Tier C (catalog, ACL, xid, advisory-lock and sequence inquiry) waited for
  steps 4, 5 and 7. Its refined plan is the "Tier C refinement" part of the
  Status section: one part per subsystem, each with a narrow capability
  interface in `functions`.

## Invariants

- Results, errors, SQLSTATEs, positions, notices, volatility and
  generated-column admission stay identical.
- Deparsed SQL in views, stored defaults, checks and native Dump output stays
  byte-identical.
- `functions` never reaches engine state except through `Binder` or
  `expr.Context`.

## Targeted tests

- The unit tests of the family or switch being changed. Grep for them.
- F2: reference selections for views, stored defaults and checks, generated
  columns and native Dump/Restore, run with `REFERENCE=1` and in quick mode.
- F3–F5: the family's reference tests, run with `REFERENCE=1` and in quick
  mode. Pick names by grepping `reference/`; never edit it.
- `make bench` after F1 and after any family on the startup path.

## Documentation

- Rewrite `internal/engine/functions/README.md`. Today it describes the
  shallow split. The rewrite covers: the package contract, the `Binder` method
  set with the reason for each method, the registry, how to add a builtin, the
  node protocol duties, what stays in the engine, and `## Testing` selectors
  moved from `internal/engine/README.md`.
- Design notes that name moved glue files or the generic contract:
  `PG18-ARCH-EXPRESSION-ANALYSIS.md`, `PG18-ARCH-ROUTINE-EXECUTION.md`,
  `PG18-FUNC-*.md`. Grep for more.
- `AGENTS.md`: once F3 lands, the shared subpackage bullet may say in a few
  words that new builtins go into the `functions` registry, pointing to its
  README. Remove the text this replaces, so the file does not grow.

## Done when

No generic instantiations remain. `function.go` dispatch is a registry lookup.
The central switches contain no function-node cases. The Tier A and Tier B
glue files are gone from the engine, and aggregates follow the split above.
Tier C has a refined plan.

## Status

- [x] F1 — Generics removed (`69aeb6167` generics, `5ec01573f`
  `functionInfo`, docs `937962c4f`)
- [ ] F2 — Switches converted: [x] retag [x] remap [x] volatility
  [x] deparse [x] node tree [x] generated admission [x] conditional plan
  [x] join legality [x] stored scalar SQL (session 1: `c6de24e8f` members,
  `144458647` join, `5d7935b7d` volatility, `4c54eaf54` conditional,
  `1fa051e96` generated, `1e6bd8aec` retag, `c703fd8d9` remap, `09fc24b0a`
  math helpers, docs `ec2fba8bd`; session 2: `68cbeefed` node tree,
  `42ec6b7e5` deparse, then the docs commit; session 3: `2e7673441`
  replay protocol, `cca30a78c` stored scalar SQL, then the docs commit)
  [x] grouping identity [x] grouping rewrite and aggregate occurrence
  [x] preflight [x] index key display [x] closed DEFAULT checks
  [x] `Arguments()` [x] view literal capture [ ] function sources
  (with `seriesExpression` and `unnestExpression`) [ ] `SQLJSON` rendering
  [x] result nullability hook (racecheck/finalcheck waived at user request)
  [ ] conditional closure hook (session 4: `063fe2d1e` identity, `622b3d3ba`
  rewrite, `a187f7f5d` preflight, `39c887998` index, `b2675f4c2`
  substitution, `632814b91` DEFAULT closure, `25fcad768` `Arguments()`,
  then the docs commit)
- [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot family:
  UUID extraction (`60dc1cedd` seam, `cd4a88387` port, `5d59e5737` move,
  docs `9bca9b2b6`)
- [ ] F3 aliases removed — `routine_declaration.go` still holds the temporary
  `routineProvider = functions.RoutineProvider` alias and 23 provider
  constants (about 1,290 uses at `75fe0d710`); broad rewrite (see "Later-wave
  refresh" below)
- [x] F4/F5 Tier A: [x] math (`3078b889f` seam, `d5dc34c59` port,
  `0f3c7e5a2` move, docs `96e11f662`) [x] string [x] LIKE/SIMILAR/regex
  [x] datetime (datetime: `5c5928497` seam, `5a293c0b3` port, `4acb5e42b`
  move; pattern: `ba236a2b8` seam, `1807e7ee9` port, `34f7300d1` move;
  string: `69133565c` seam, `7c28e5c79` port, `76d0201d9` move,
  `db9694a8f` string_to_table and variadic bookkeeping; then the docs
  commit) [x] JSON/JSONB/SQL-JSON
  (function calls: `059a86924` seam, `94b71bb40`
  port, `a6c399c2b` move, `b1099500b` slot binder, then the docs commit;
  operators, SQL/JSON constructors and `IS JSON`: `13aba123e` diagnostics
  seam, `d7103073a` port and `06d03de3c` move of the operators, `d4b19bb66`
  port and `b3e056f62` move of SQL/JSON, then the docs commit) [x] arrays [x] ranges (`a46ee7c5c` seam,
  `48d987da7`/`62f610cb1` arrays, `050805a1e`/`a5be023f9` ranges, then the
  docs commit) [x] UUID [x] format
  [x] binary (`6e15eb725` seam, `8e4f3177b` port, `0d43b5592` move, then
  the docs commit) [x] numeric formatting (`b6dc6c486` move; regexp and variadic
  string registration: `2dbf3742f` seam, `7c8508c0b`, `528ac1aeb`; then the
  docs commit) [x] pgcrypto [x] fuzzystrmatch
  [x] unaccent (`a4e02ba94` seam, `c037c89d9` port, `56271aa34` move,
  then the docs commit) [x] intarray (nothing to port) [x] intagg
  (`50ab66515` seam, `a59940644` port, `208390a37` move, then the docs
  commit; the aggregate path stays with the aggregates item) [x] uuid-ossp (UUID, format and
  uuid-ossp: `e29efd342` seam, `6bcd3fb43` port, `0a04835ae` UUID move,
  `36411c4fd` format move, `0a80f30a0` uuid-ossp move, then the docs commit)
- [x] F4/F5 Tier B: [x] random [x] current time and clocks [x] sleep
  (with intagg; clock FILTER fix `920fafff4`) [x] settings reads (the bind
  half of statement-level `current_setting`/`set_config`: `4014a7726` port,
  `e030789b6` move, then the docs commit)
- [x] F4/F5 aggregates and windows (`f4ee7801c` seam, `d9d8e6482` intagg
  prelude, `8066e02e9` simple aggregates, `ff28e5996` JSON aggregates,
  `a18766bcc` ordered-set and hypothetical-set, `c4a15e187` window
  functions, then the docs commit; `int_array_aggregate` keeps its engine
  branch)
- [ ] Tier C refined (after steps 4, 5 and 7; plan in "Tier C refinement"
  below): [x] ACL SQL function nodes (`b3ce43139` seam, `574de18f1` port,
  `436322070` move, `75d906ea8` protocol hooks, then the docs commit)
  [ ] catalog inquiry (with session identity) [ ] transaction identity and
  commit timestamps [ ] advisory locks (with blocking inquiry) [ ] sequence
  calls

`Binder` methods added beyond the F3 set: `AnalyzeFilter` (F3, for the
pilot's shared call envelope; FILTER analysis is used by 27 glue files, so it
is a genuine analyzer capability); `DefaultConsumer`, `ArrayType`,
`CommonUsage` and `ComparisonInfo` (math); `CollatedInfo`,
`RetainUntypedParameter`, `NoteCatalogText` and `InstalledRoutine` (UUID
generation, format, uuid-ossp); `BuiltinOperatorRoutineInfo` and
`CaptureComparisonInput` (arrays and ranges); `Literal`, `Context`,
`AdmitSetReturning` and `CaptureTypeInput` (JSON calls); `ConstantValue`
(datetime), `BuiltinOperatorVisible` and `CallSignatureError` (pattern) and
`FinishBinary` (string, a leak the binary port removed); `LiteralNull` and
`NoteVisibilitySnapshot` (binary); `JSONReturningType` and the
`AggregateBinder` methods `AnalyzeAggregateFilter`, `FinishAggregate`,
`DomainTypeName`, `CollationName`, `CommonArgumentUsage` and
`CollationRelabel` (aggregates and windows). The JSON operators and SQL/JSON
port added none; `RetainUntypedParameter` now reports whether it retained. The pgcrypto, fuzzystrmatch and unaccent
port added none, nor did the intagg, random, clock and sleep port. Reasons
for every method are in the `functions` README.

Outstanding aliases: F3 moved `RoutineProvider` and its 23 constants to
`functions/routine_provider.go`; the engine keeps `routineProvider` and
`<family>RoutineProvider` aliases in `routine_declaration.go` (1,373
references in 91 files, mostly declaration tables). Remove them in a later
alias-removal pass, which counts as a broad rewrite (§8). The step 2b aliases
in `functions/expr_aliases.go` are still listed in `ENGINE-SPLIT-expr.md` for
2d. Math added `functionResolutionErrorForTypes = functions.ResolutionError`
(41 engine call sites) and `functionCastHint = functions.SignatureHint` (4);
remove them with the other aliases. The datetime, pattern and string ports
added no aliases: the callers of the moved `operatorExpressionError` (now
`functions.OperatorError`) and `datetimeFormattingTypeName` (now
`functions.DiagnosticNameOrUnknown`) were repointed.

F1 record, measured at its start on `b12c7ee57`:

- `functions` had 82 generic types in 50 files and ~106 generic functions.
  All 28 context constraints were subsets of `expr.Context`, so they and
  `Context`, `Expression`, `LiteralContext` and `Info[P]` were deleted. The
  engine had 1,808 suffix references in 184 files and 284 `functionInfo`
  references. A throwaway `go/ast` rewriter stripped the `functions` type
  parameters; a regex stripped the engine suffixes.
- Kept: the private `randomInteger[S randomIntegerSource]` sampler. It is a
  numeric kernel generic, not part of the expression contract.
- `StringVariadic[E, C, P, O]` carried the engine's `quoteOrigin` as `O`. Its
  `Origin` is now an opaque `any`; the engine reads it only through
  `quoteOriginOf`, which maps an unset origin to the zero `quoteOrigin`.
- `ArgumentCoercer`/`ArgumentAnalyzer` are plain interfaces; bind helpers that
  took `B ArgumentCoercer[E]` now take the interface (bind time only).
- `functionInfo` was removed in its own commit. It also cleared
  `UnknownNull` and `RegclassRef`; only literal nodes set them, and no inlined
  site receives a literal's info.
- F1 also removes the 2a hazard where the two-parameter `TransactionTime`,
  `VersionInformation` and `CurrentDateTime` instantiations compiled with any
  context: a type switch case can no longer silently miss.

Tests in F1: no test moved or was deleted. `functions` unit fakes were adapted:
fake arguments embed `expr.Info`; fake contexts embed the nil
`unitCapabilities` (`expr.Context`) from the new `unit_capabilities_test.go`,
which also holds `unitArgs` to widen fake argument slices. Tests that used
`bool`, `IntrinsicPropertyRule` or integers as `P` now use `expr.Properties`
with `expr.PropertiesMatch`, keeping each assertion. Fake args stored in
`Args` are now read through a type assertion. One test passed an untyped
`nil` context; it now passes the typed nil pointer it meant.

Verification: `make quickcheck` passed after each commit; bench median
1.53 ms/op with 7,753 allocs/op, against 1.60 ms and 7,753 before (startup
was already above the 1 ms target on master). Function-family
`REFERENCE=1` selection (feature smoke, stored and control tests, 196 tests)
passed except three failures that reproduce on unmodified master, listed
below. Racecheck skipped at the user's request.

F3 record, measured at its start on `1c0f87518`:

- 115 engine files import `functions`. Files using each binder method:
  `info` 78, `typ` 74, `builtinRoutineInfo` 47, `AnalyzeArgument` 45,
  `functionSignatureErrorForTypes` 41, `builtinVisible` 41, `CoerceArgument`
  32, `diagnosticTypeName` 30, `bindAggregateFilter` 27, `comparisonOperand`
  20, `coerceConditional` 20, `collatedInfo` 17,
  `functionResolutionErrorForTypes` 13, `builtinRoutineReference` 12.
- `Binder` methods: `AnalyzeArgument`, `CoerceArgument`, `Type`, `Info`,
  `BuiltinVisible`, `BuiltinRoutineInfo`, `BuiltinRoutineReference`,
  `DiagnosticTypeName`, `SignatureError`, `AnalyzeFilter`. The engine
  implements them as thin adapters in `function_binder.go` over its private
  methods; the private names stay (`typ` alone has 690 call sites), and
  ported code uses the interface names.
- Corrections to the plan: `comparisonOperand` and `coerceConditional` need no
  methods, because `AnalyzeArgument(ComparisonArgument)` and
  `CoerceArgument(ConditionalArgument)` already cover them. `Type` returns
  `*sqltype.Descriptor`, not a usage, because glue compares descriptors.
  `bindFunction` in `composite_expression.go` has no name chain: it wraps
  `bindFunctionProvider` for the composite projection fallback, so only
  `function.go` dispatches by name.
- `Lookup` scans a static table with `strings.EqualFold`, matching the
  replaced `parser.UUIDExtractFunctionName`; `Bind` owns exact spelling.
  `bindFunctionProvider` consults it first. Precedence is unchanged because
  chain branches test disjoint names and the UUID branch already sat before
  the shared label and `ResolutionOnly` gates.
- Pilot: `uuid_extract_version` and `uuid_extract_timestamp`. The shared
  envelope moved with it as `functions.ValidateCallSyntax` (was
  `validateRangeInspectSyntax`), `CallDecorationError` (was
  `rangeInspectDecorationError`) and `NamedArgumentType` (was
  `functionNamedType`); 45 engine files now call them. The UUID checkbox
  stays open: `uuidv4`/`uuidv7` generation remains in the engine.

Tests in F3: added `TestLookupClaimsRegisteredNames` and
`TestDefinitionsAreWellFormed` in `functions`. The engine's
`uuid_extract_test.go` stays: its tests exercise engine switches (group
identity, retag, preflight, renderer, DEFAULT admission) that F2 converts.
`TestUUIDExtractScalarLifecycle` now binds through `bindFunctionProvider`.
No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. `REFERENCE=1` and
quick mode passed for `^TestReference(UUIDExtract|RangeInspect)`, and
`REFERENCE=1` passed for the diagnostics, resolution and smoke tests of the
other families that share the moved call-syntax helpers (ACL values, catalog
relations, datetime age/isfinite, object address, transaction identity).
Bench median 1.73 ms/op with 7,753 allocs/op (allocations unchanged; startup
code untouched, time within run-to-run noise and still above the 1 ms target
as on master). Racecheck skipped at the user's request.

F4/F5 math record, measured at its start on `85de31810`:

- `math_expression.go` (200 lines) used, beyond the F3 set, the DEFAULT
  consumer flag, `randomSignature` (shared with random and deparse inquiry),
  the pure 42725 builder, and for `width_bucket(operand, thresholds)`
  `comparisonOperandPolicy(thresholdArrayInput)`, `arraySearchTypeName`,
  `selectCommonUsage` over fake literal operands, `definitionArray` and
  `comparisonInfo`.
- Resolved with four `Binder` methods (above), one analysis intent value
  (`ThresholdArrayArgument`) and pure moves: `ResolutionError`,
  `DirectResolutionError` (was `randomSignature`), `ArrayTypeName` (was
  `arraySearchTypeName`; its 9 engine callers now use it), `SelectedLogPower`
  (the engine's `selectedLogPowerName` duplicated it), `MathWrapperContext`.
- Correction to the F3 note on gates: instead of reproducing the shared gates
  inside `Bind` (which would need the engine's diagnostic binders as `Binder`
  methods), `Definition` declares `ExactSpelling` and `SharedGates` and the
  engine applies the gates (`sharedCallGates` in `function.go`). Math sat in
  the final `switch name`, so it sets both. No earlier chain predicate claims
  a math name (the parser's name lists are admission lists, not dispatch).
- Engine switches still reference math through exported helpers:
  conditional planning (`SelectedLogPower`) and retag (`MathWrapperContext`).
  F2 removes those references.

Tests in F4/F5 math: `TestLookupClaimsRegisteredNames` gained exact-spelling
and gate cases and `TestDefinitionsAreWellFormed` checks exact names are
lowercase (both failed first). The engine's `math_*_test.go` and
`random_test.go` stay: they bind through the analyzer and exercise engine
switches (grouping identity, conditional simplification, preflight, consumers,
privileges). No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. Quick mode and
`REFERENCE=1` passed for `^TestReferenceMath`, `^TestReferenceSearchPath`,
`^TestReferenceDeparseConstraintsReview(Numeric|Random)` and
`^TestReferenceArray` (the array callers of `ArrayTypeName`). Bench: two
branch runs had medians of 2.15 ms and 1.73 ms against 1.97 ms on master
`85de31810` on the same machine, with 7,769 allocs/op on both
(allocations unchanged; time within run-to-run noise and still above the 1 ms
target as on master). Racecheck skipped at the user's request.

F4/F5 UUID generation, format and uuid-ossp record, measured at its start on
`604ee2be6`:

- `uuid_generation.go` `bindUUIDGeneration` (final `switch name`, plus
  pgcrypto's `gen_random_uuid` when `pg_catalog` wins) needed only F3 methods.
  The label gate exempted `uuidv7` by name, so `Definition` gained
  `OwnsLabels`: shared gates then skip the label gate and `Bind` owns label
  diagnostics. `uuidv4` and `uuidv7` are two definitions (exact spelling,
  shared gates; `uuidv7` also `OwnsLabels`). `BindUUIDGeneration` is exported
  for pgcrypto.
- `format_expression.go` `bindFormat` sat after the gates, claimed only exact
  `format`, and sent other spellings (`"FORMAT"`) to
  `formatDiagnosticSignatureError`. Correction to the gate rule: shared gates
  on a case-insensitive definition now treat other spellings as
  resolution-only, so `format` registers case-insensitively and the engine
  keeps no format-specific clause. Beyond F3 it needed `CollatedInfo`,
  `RetainUntypedParameter` (the `anyParameters` retention) and
  `NoteCatalogText` (`regclassConcat`). The glue file is deleted.
- `uuid_ossp_expression.go` `bindInstalledUUIDOssp` is the first extension
  family to move. `InstalledRoutine(name, Extension, signature)` resolves the
  search path first, so lookup errors precede signature errors as before. It
  returns an opaque handle whose `Validate` and `Attach` keep the manifest
  check and the dependency/identity attachment in their old order. The
  wrapper node `installedUUIDOsspFunctionExpression`, `validShape`,
  `validate` and its ~20 switch cases stay in the engine (procedure
  identity), so that file remains with only identity code.
- `CallLabelError` was extracted from `ValidateCallSyntax`; UUID generation
  and uuid-ossp used private copies of it.
- No earlier chain predicate claims `uuidv4`, `uuidv7`, `format` or
  `uuid_generate_v4`. `closedUUIDDefault` (DEFAULT admission) and the UUID
  clock/entropy context stay in `uuid_generation.go`.

Tests in F4/F5 UUID generation, format and uuid-ossp: added
`TestCallLabelErrorOrder`; `TestLookupClaimsRegisteredNames` gained UUID
generation, format and uuid-ossp cases (each failed first) and no longer
lists `uuidv7` as unclaimed; `TestDefinitionsAreWellFormed` checks that
`OwnsLabels` implies `SharedGates`. `uuid_ossp_expression_test.go` now binds
through `bindFunctionProvider` (three call sites). The engine's
`uuid_generation*_test.go`, `uuid_ossp_*_test.go` and `format_*_test.go`
stay: they exercise engine switches (DEFAULT closure, grouping, retag,
remap, stored identity, views). No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. Quick mode
passed for
`^TestReference(Format|UUID|PgcryptoRandomUUID|ObjectGrantUUIDOssp|NumericFormattingUUIDDefaults|SearchPath)`;
with `REFERENCE=1` everything passed except the `/postgres` subtests of
`TestReferenceUUIDGeneration{StoredConsumersAndControls,FeatureSmoke,GuardDiagnostics}`,
which fail identically on master `604ee2be6` (the 18.6 `uuidv7` shift drift
below; `GuardDiagnostics` is a third witness of it). Bench median 1.83 ms/op
with 7,769 allocs/op (allocations unchanged; startup code untouched; still
above the 1 ms target as on master). Racecheck skipped at the user's request.

F4/F5 arrays and ranges record, measured at its start on `604ee2be6` and
rebased onto `684513b33` (after UUID generation, format and uuid-ossp):

- Ported: array inspect (6 names), random (2), transform (8) and search (4),
  and range inspection/hull (7 names). All sat before the shared gates and
  matched case-insensitively, so they register with neither `ExactSpelling`
  nor `SharedGates`. No earlier chain predicate claims these names (checked
  against every predicate before the old branches).
- The glue used `comparisonOperandPolicy(projectionContext)`,
  `coerceConditional`, `selectCommonUsage` with no label and
  `definitionArray`; these became `AnalyzeArgument(ComparisonArgument)`,
  `CoerceArgument(ConditionalArgument)`, `CommonUsage` and `ArrayType`. The
  inline FILTER/count/label envelope and decoration checks were the shared
  `ValidateCallSyntax` and `CallDecorationError`; array search and the `*`
  decoration of transforms keep their own messages. Two new `Binder` methods
  (above) cover operator routine identity and array_sort's comparison input
  capture. Collation derivation and array_to_string's catalog-text demand
  reuse `CollatedInfo` and `NoteCatalogText`, which the UUID/format port added
  meanwhile (this branch had added the same two under other names and
  dropped them at the rebase).
- Shared with operators: `finishArrayTransform` (the `||` array branch) and
  `newArraySearch` (`@>`/`<@`/`&&` membership) moved as the exported
  `FinishArrayTransform` and `ArraySearchNode`; the engine's array operator
  glue is now `array_operator.go`.
- Deviation: `lower`/`upper` are not registered. They analyze operands with
  string intents and finish in the engine's `finishStringFunction`, which
  hands range operands to `functions.FinishRangeInspect`. Their engine branch
  (`bindLowerUpper` in `string_expression.go`) sits where the range branch
  sat; the string port registers them.
- Kept out: `unnest` and `generate_subscripts` (set-returning; engine node and
  query-level admission state), array/range operators and array/range
  aggregates.
- Moved for deletion of the glue files, still called by engine switches:
  the array nodes' `map*Children`/`*SQL` helpers (`array_node_sql.go`, used
  by remap, conditional planning, preflight and deparse) and
  `FunctionArrayRandomOwner` (now in `expression.go`). F2 removes the
  helpers.

Tests in F4/F5 arrays and ranges: `TestLookupClaimsRegisteredNames` gained
array and range claims and non-claims (`lower`, `upper`, `unnest`,
`generate_subscripts`, aggregates); both additions failed first. Engine tests
stay, as for math: `TestArrayInspect*`, `TestArrayTransform*`,
`TestArrayRelational*`, `TestRangeInspect*` and `TestRangeMoreBinding` now
bind through `bindFunctionProvider` instead of the deleted binder methods;
`TestArraySearchViewCoercions`, `TestRangeInspectBindingUsesSelectedAuthority`
and `TestRangeMoreBinding` call `functions.ArraySearchNode` and
`functions.FinishRangeInspect`; remap/deparse helper calls use the exported
names. No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages) except
`TestReferenceServerCopyCancelInput/server/invalid_record` (57014 instead of
22P02), a COPY-cancellation timing flake unrelated to this change: it passed
4 of 5 isolated reruns on the branch and failed 1 of 20 runs on master.
`make deps` lists only module paths; `functions` imports no engine state
packages. Quick mode and `REFERENCE=1` passed for `^TestReferenceArray` and
`^TestReference(RangeInspect|RangeMore|RangeOperator|Multirange|SearchPath|DeparseConstraintsReview|Collation|Expression)`.
`^TestReferenceString` (the `lower`/`upper` text path) passed in quick mode;
with `REFERENCE=1` only the `/postgres` subtests of four string witnesses
fail, identically on master (18.6 oracle drift, below). Bench median
1.86 ms/op against 1.92 ms on master, both with 7,769 allocs/op (allocations
unchanged; startup code untouched; still above the 1 ms target as on
master). Racecheck skipped at the user's request.

Notes for later steps:

- `InstalledRoutine` covers the shared lookup of every installed family
  (pgcrypto, fuzzystrmatch, unaccent, intarray, intagg): add an `Extension`
  value and an engine handle per family. pgcrypto's `gen_random_uuid` also
  needs the per-namespace walk that prefers `pg_catalog`; give the handle
  lookup that ordering rather than a second method.
- The string port should register `lower`/`upper` and call
  `FinishRangeInspect` directly; `bindLowerUpper` then goes away.
- Set-returning builtins (`unnest`, `generate_subscripts`, `generate_series`,
  `string_to_table`, regexp sets) share query-level admission
  (`executeParameters`, policy frames, `stringSetAllowed`,
  `queryHasTargetSet`); port them together behind one admission capability.
- Families whose engine branch sits after the shared label and
  `ResolutionOnly` gates in `bindFunctionProvider` must reproduce those gates
  in `Bind` when they register, because `Lookup` runs first.
- `CommonUsage` wraps the engine's `selectCommonUsage`. Its usage-only core
  could move to `sqltype` when CASE/VALUES/IN typing gets an owner; the
  method would then become a plain function.
- `Lookup` scans the table linearly (11 definitions, 85 names after arrays
  and ranges) on every function call before the chain. Re-measure bind cost
  when more families register; a sorted static table would keep it
  `init()`-free.
- `functions.qualifiedName` duplicates the engine's `objectNameText` (a
  dotted join used for the 42809 message). Consolidate them when the engine's
  name helpers get an owner.

F4/F5 JSON function-call record, measured at its start on `604ee2be6` and
rebased onto `7cb919447` (UUID, format, uuid-ossp, arrays and ranges):

- Scope (chosen by the user): every name-dispatched JSON/JSONB call, in ten
  engine glue files (~1,900 lines). JSON operators (`->`, `@>`, `?`, `||`, `-`,
  `#-`; ~600 lines), SQL/JSON constructors and `IS JSON` (engine nodes
  referenced from ~19 switch files; they need RETURNING type resolution and
  coercion-node construction) stay for a later session, so the checkbox stays
  open. JSON aggregates belong to the aggregates item.
- Beyond the existing `Binder`, the glue built engine literals, read `b.ctx`,
  admitted SRFs through `stringSetAllowed` plus target-set flags, appended
  `anyParameters`, set `regclassConcat`, read a parameter's inferred record
  type, captured diagnostic/element input and toggled `jsonbRecordBase`.
  Resolved with four methods (`Literal`, `Context`, `AdmitSetReturning`,
  `CaptureTypeInput`), the UUID/format session's `RetainUntypedParameter`
  and `NoteCatalogText`, and two intent values (`RecordBaseArgument`,
  `RecordFormalArgument`); see the README.
- Registry: the chain's JSON predicates mix `EqualFold`, `strings.ToLower`
  and exact matching, so a name list compared with `EqualFold` would claim
  spellings the chain did not (for example `to_jſon`). `Definition.Claims`
  keeps the chain's predicate; one definition (`bindJSONCall`) replays the
  chain's JSON branch order, and `json_object`/`jsonb_object` use
  `ExactSpelling` and `SharedGates`. A one-off check confirmed no earlier
  chain branch claims a registered JSON name.
- Engine leftovers: function column definitions moved to
  `function_column_definitions.go`; JSONB record evaluation input, domain
  checks, star expansion and cast annotation are in `jsonb_record_columns.go`;
  the extraction operator is in `json_extraction_operator.go`. Stored-SQL
  renderers, `MapJSONExtractChildren` and `JSONBPathQueryName` are exported
  from `functions/json_sql.go` for the engine switches; F2 removes those uses.
- The four labeled-slot binders were near-identical copies; `bindSlots` now
  owns their shared sequence.

Tests in F4/F5 JSON: `TestLookupClaimsRegisteredNames` gained JSON claim
cases (folded, lowercased and exact spellings, `ſ` variants, SQL/JSON and
aggregate names not claimed) and `TestDefinitionsAreWellFormed` checks that
listed names satisfy `Claims` (both failed first). `array_to_json` left the
arrays session's not-claimed list, since the JSON definition now claims it. Engine tests that called
the bind methods directly (`json_create_expression_test.go`,
`json_strip_nulls_test.go`, `jsonb_insert_test.go`,
`jsonb_path_query_array_test.go`, `jsonb_path_query_test.go`,
`jsonb_set_decorations_test.go`, `jsonb_set_test.go`) now bind through
`bindFunctionProvider`; `json_extract_expression_test.go` and
`jsonb_extract_expression_test.go` call the exported renderers. They stay in
the engine because they use the real analyzer and engine switches. No test
moved or was deleted.

Verification: `make quickcheck` passed; `make deps` lists only module paths;
`functions` imports no engine state packages. Quick mode passed and
`REFERENCE=1` matched the master baseline for
`^(TestReference(JSON|SQLJSON)|TestReferenceObjectGrant.*JSON|TestReferenceSearchPath)`
(the only failures are the two `json_strip_nulls` oracle-drift tests below,
in `/postgres` only, as on master). Bench skipped: bind path only, no startup
code changed. Racecheck skipped at the user's request.

F4/F5 datetime, LIKE/SIMILAR/regex and string record, measured at its start
on `604ee2be6`. The three families ran in one session on
`split/functions-tier-a`, in parallel with F2 on `split/functions`, and were
rebased over the UUID/format, array/range and JSON merges:

- Datetime used, beyond the shared set, engine literal construction
  (`make_*` defaults) and reads (constant templates), `b.ctx`, the
  `datetimeZoneOperands` flag, `identityExpression(age(xid))` and
  `overlapsInlineSafe`. The flag is read only by `bindCast`, which coercion
  finalization cannot reach for the zone family's operands, so scoping it to
  operand analysis (`ZoneArgument`) is equivalent. Hand-built 42725 errors in
  age, extract, trunc and formatting were byte-identical to
  `ResolutionError(..., true)`. `make_*` sat in the final switch but was
  exempt from the label gate; it uses `OwnsLabels`, which removes the gate's
  last exemptions (only the advisory names remain).
- Pattern: the LIKE operator node `likeExpression` is engine-private and has
  about 20 central switch cases (F2's territory), so its binder is ported onto
  `Binder` in place but not moved. `similar_to_escape` is dispatched by
  `SimilarEscapeFunctionSyntax`, not by name; registering the name would claim
  ordinary calls that fail with 42883 today. `regexp_*` checks its own labels
  before the shared gates and wraps set-returning calls; it keeps an engine
  branch.
- The regexp and variadic string 42883 paths used the unnoted builder and keep
  `ResolutionError(..., false)`; noting them would change the composite
  fallback decision.
- String: bytea/bit overloads finish through `FinishBinary`. `stringNode`'s
  catalog-output flag and `capturePolymorphicBody` act only for `||` (and
  quote_literal/quote_nullable on anyelement), so `functions.StringNode`
  leaves them to the engine's operator wrapper. `bindLowerUpper` (added by
  the range port) moved with the family; `lower`/`upper` and `parse_ident`
  register without gates, as they sat before them.
- Engine state needed at one point is passed as a value: the `age(xid)` node
  builder, the regexp placement check and the variadic query frame.
- Files: 22 new `functions/*_bind.go` files plus `operator_error.go`; 14
  engine glue files deleted. `expression_similar.go` became
  `regex_execution.go` (the regex executor capability), `datetimeMakeSQL`
  moved to `datetime_make_render.go`, and `string_variadic_capture.go` gained
  the variadic wrapper. Switch helpers stay in place for F2: `regexpSQL`,
  `mapRegexpChildren`, `preflightRegexpCall`, `sameRegexpCall`,
  `stringSpecialSQL`, `trimSyntaxSQL`, `datetimeMakeSQL`, the
  overlaps/extract render files.

Tests in F4/F5 datetime/pattern/string: `TestLookupClaimsRegisteredNames`
gained the families' exact names, the `make_*` `OwnsLabels` case, the
ungated `parse_ident` and `lower`/`upper` spellings, and unclaimed names
(engine-dispatched calls and other spellings); it failed before
registration. Engine unit tests stay, because they exercise engine switches.
Adapted call sites: the `datetime_*_test.go`, `numeric_formatting_test.go`
and `transaction_identity_test.go` tests bind through `bindFunctionProvider`,
and `TestDatetimeFormattingBindingAuthority`'s hidden case uses a
`public.`-qualified name instead of `visible=false`; the `bindStringFunction`
callers in `bitstring_functions_test.go`, `range_inspect_test.go`,
`string_case_test.go`, `string_replace_test.go` and `string_slice_test.go`
call `functions.BindString`. No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages) on the rebased branch;
`make deps` lists only module paths; `functions` imports no engine state
packages. Quick mode passed for each family's reference selection, and
`REFERENCE=1` passed for the datetime, timezone, numeric formatting,
transaction identity, LIKE/SIMILAR/POSIX/regexp, collation, string, range,
binary/bit, SRF, search-path and object-grant selections except the six
`/postgres`-only drift tests listed below, which fail identically on master.
Bench median 1.91 ms/op with 7,769 allocs/op, against 2.91 ms and 7,770 on
master `604ee2be6` on the same machine (allocations unchanged; time within
run-to-run noise and still above the 1 ms target). Racecheck skipped at the
user's request.

Notes for later steps (datetime, pattern, string):

- `Lookup` now holds 23 definitions and 207 names plus the JSON and variadic
  string predicates; a throwaway benchmark measured 0.6 to 1 µs per miss with
  no allocations. A sorted static table would keep it `init()`-free.
- The binary port removes `FinishBinary` (done).
- Still engine-dispatched over exported entry points: `age`/`isfinite`
  (Tier C), `overlaps` (an F2 inline-safety hook) and `similar_to_escape`
  (needs a syntax-keyed definition). `regexp_*`, the variadic string family
  and `to_number` registered in the follow-up below.
- LIKE's binder and node move together once F2 has converted the switches
  that name `likeExpression`.

F4/F5 follow-up (regexp, variadic string, `to_number`), on `ec3fcadd4`. A
second session (`split/functions-string-regex-datetime`) ported the same three
families in parallel; after the tier-a merge it rebased only what master still
dispatched by name chain onto a new branch and dropped the rest:

- `regexp_*` and the variadic string family received engine state as values
  (a placement callback, set wrapping, the query frame, body capture in an
  engine wrapper), which kept their chain branches. Four `Binder` methods
  replace the values so both families register: `Frame` and
  `CapturePolymorphicBody` (genuine), `TargetSet` (genuine, the bookkeeping
  half of `AdmitSetReturning`) and `RegexpSetPlacement` (a leak: the regexp
  placement policy, to fold into `AdmitSetReturning`). The variadic family
  claims through `StringVariadicFunctionName`, which lowercases rather than
  folds (`concat_wſ` stays unclaimed); `regexp_*` uses `ExactSpelling`
  without gates. A throwaway check confirmed no earlier chain predicate claims
  either family's names.
- `to_number` (numeric formatting) moved into `numeric_formatting_bind.go` and
  registers by exact spelling behind the shared gates; `numericFormattingOperands`
  became private to `functions`.

Tests in the follow-up: `TestLookupClaimsRegisteredNames` gained the regexp,
variadic and `to_number` claims and dropped them from the unclaimed list
(failed before each registration). `numeric_formatting_test.go`'s `to_number`
case binds through `bindFunctionProvider`. No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. `REFERENCE=1`
passed for `^TestReference(Regexp|Similar|Posix|Like)`,
`^TestReference(SearchPath|Format)` and the numeric formatting tests; in
`^TestReferenceString` and `^TestReferenceDatetimeFormatting` the only
failures are the `/postgres`-only drift tests listed below, which fail
identically on master. Racecheck skipped at the user's request.

Notes for later steps (JSON):

- `Lookup` now evaluates the JSON `Claims` predicate (about 40 name
  comparisons) for every function call before the chain. Re-measure bind cost
  when more families register.
- The JSON operator binders can reuse `BuiltinOperatorRoutineInfo` (arrays
  and ranges) but still need the engine's operator diagnostics
  (`arrayMembershipError`, `comparisonError`) before they can port (done in
  the JSON operators record below).

F4/F5 binary record, measured on `7af01d85b` (after the datetime, pattern,
string and JSON ports; an earlier attempt from `684513b33` was redone on it,
because the string and pattern ports had already moved the substring routes)
and rebased onto `ec3fcadd4` (regexp, variadic string and `to_number`
registration) and `f7860fa60` (F2 session 1):

- Ported: `bindBinaryFunction` and `finishBinary` (`binary_expression.go`,
  deleted) for 18 names in the final `switch name`: `convert`,
  `convert_from`, `convert_to`, `encode`, `decode`, `substring`, `substr`,
  `crc32`, `crc32c`, `sha224`..`sha512`, `bit_count`, `get_bit`, `get_byte`,
  `set_bit`, `set_byte`. They register with `ExactSpelling` and
  `SharedGates`; `bindBinary` checks builtin visibility after the gates, as
  the switch did. No earlier predicate or registered definition claims these
  names (the parser's `binary*Function` lists are admission lists; the
  registry test checks the claims).
- Beyond the existing `Binder` it needed the engine's literal-NULL predicate
  (`randomLiteralNull`) and the visibility-snapshot demand of `convert*`:
  `LiteralNull` and `NoteVisibilitySnapshot`. Everything else already
  existed (`BuiltinOperatorRoutineInfo` for `||`, `CollatedInfo`,
  `DefaultConsumer`, `SupplyBinaryTypes`, and the string and pattern ports'
  `FinishString`, `BindPosixSubstring` and `BindSimilarSubstring`).
- `FinishBinary` is now a plain function: `functions.BindString` and the
  engine's `||` binder call it, and the `Binder.FinishBinary` leak is gone.
  `BindBinary` takes a precomputed visibility, like `BindUUIDGeneration`.
- Kept in the engine: `renderBinaryCall` (`binary_render.go`, deparse; F2).

Tests in F4/F5 binary: `TestLookupClaimsRegisteredNames` gained the 18
binary claims; `substring`/`substr` left its unclaimed list, which gained
other spellings (`SUBSTRING`, `Substr`, `Encode`) and near names (`sha1`,
`get_bits`, `convert_to_text`, `pg_size_bytes`). It failed before
registration. Engine tests stay: `binary_access_test.go`,
`bitstring_functions_test.go`, `bitstring_operators_test.go` and
`string_slice_test.go` call `functions.BindBinary(b, raw, true)` and
`functions.FinishBinary(b, ...)` instead of the deleted binder methods. No
test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. Quick mode and
`REFERENCE=1` passed for
`^TestReference(Binary|BitAccess|BitString|BitOperators|StringSlice|StringCore|StringPad|Similar|Posix|LikeBinary|Bytea|Pgcrypto|SearchPath|Conversion)`,
except the `/postgres` subtests of `TestReferenceStringCoreNormalization` and
`TestReferenceStringCoreValuesSourceConsumerRoutes`, which fail identically
on master (18.6 oracle drift, below). Bench median 1.52 ms/op on the branch
and on master `7af01d85b` in back-to-back runs, both with 7,769 allocs/op
(allocations unchanged; startup code untouched; still above the 1 ms target
as on master). During the redo, two full `quickcheck` runs on a heavily
loaded machine failed only timing witnesses unrelated to binding
(`TestReferenceTimeoutChainPrepared/aborted-ROLLBACK/memory`,
`TestDatetimeSleepCleanup`); both pass in isolation, and the
`reference/session_state` package passed 2 of 2 on master too. Racecheck
skipped at the user's request.

Notes for later steps (binary):

- `Lookup` now scans 24 definitions with 225 names, plus the JSON and
  variadic string predicates.

- Oracle drift on the pinned PostgreSQL 18.6 image, failing on master too,
  in the `/postgres` subtests only: `TestReferenceJSONStripNullsFeatureSmoke`
  and `TestReferenceJSONStripNullsStable` (expect 42P17, get success) and
  `TestReferenceUUIDGenerationStoredConsumersAndControls` /
  `TestReferenceUUIDGenerationFeatureSmoke` /
  `TestReferenceUUIDGenerationGuardDiagnostics` (18.6 rejects out-of-range
  `uuidv7` shifts that 18.0 accepted). Needs a separate compatibility task.
  The string witnesses `TestReferenceStringCaseReviewSigma`,
  `TestReferenceStringCoreNormalization`,
  `TestReferenceStringCoreValuesSourceConsumerRoutes` (Unicode case and
  normalization) and `TestReferenceStringSplitLocking` (locked-row results)
  also fail in `/postgres` only, on master too, as do
  `TestReferenceDatetimeExtractCanonicalDefinitions` (18.6 quotes
  mixed-case EXTRACT fields in deparse) and
  `TestReferenceDatetimeFormattingReviewCollationDemand` (XX000 "cache
  lookup failed for collation 0" instead of 42P22).

F4/F5 pgcrypto, fuzzystrmatch and unaccent record, measured at its start on
`d27303cc6`:

- Scope. The task also named numeric formatting, but that was already done:
  `to_number` moved in `b6dc6c486`, numeric `to_char` binds through the
  datetime family, and only F2 switch cases remain.
- Ported binders (each matched `EqualFold` and sat before the shared gates):
  - `bindInstalledCrypto`.
  - The three near-identical `bindInstalled{Levenshtein,Metaphone,Soundex}`,
    now one table-driven `bindFuzzystrmatch`.
  - `bindInstalledFunction` (unaccent and `daitch_mokotoff`), now
    `bindInstalledText`.

  They register case-insensitively with neither `ExactSpelling` nor
  `SharedGates`. A throwaway check confirmed that no earlier chain predicate
  or registered definition claims the 20 names.
- No `Binder` method was added; `InstalledRoutine` covered every lookup:
  - New `Extension` values: `PgcryptoExtension`, `FuzzystrmatchExtension`,
    `UnaccentExtension`. One engine handle (`installed_routine.go`) replaces
    uuid-ossp's own. A procedure from another provider yields nil. This is
    exact, because no other creator makes procedures with these names and
    signatures.
  - pgcrypto's lookup keeps its per-namespace walk. It returns the
    `functions.CatalogBuiltin` value when `pg_catalog` precedes the installed
    `gen_random_uuid`, as the arrays/ranges notes asked.
  - The pgcrypto 1.4 manifest table moved to `functions` as plain data
    (`PgcryptoMembers`, `PgcryptoLookup`). Bind code reads results and
    volatility from it, because procedure descriptors are never mutated after
    staging; the engine's stage, validate, owner-import and DEFAULT-closure
    code read the same table.
- unaccent:
  - Evaluation moved to the new `functions.InstalledText`. The engine's
    `installedFunctionExpression` now embeds it.
  - Its switch cases changed only field names (`arg` → `Arg`,
    `expressionInfo` → `Info`). `InstalledText` is not an `expr.Composite`,
    so the wrapper cannot fall into protocol branches.
  - `unaccent.rules` and its embed moved to `functions` (`StockUnaccent`).
    `Attach` captures the dictionary and template and sets the rules from
    the installed dictionary's provider, as evaluation read them before.
  - A probe showed that implicit coercion to TEXT admits no type with a cast
    routine (bool, inet and cidr need explicit or assignment casts). So the
    planned `DirectFormalArgument` coercion builds the same node as the old
    inline coercion, and no new request flag was needed.
- Parity details kept on purpose:
  - Levenshtein, Metaphone and Soundex append their dependency without
    deduplication, while pgcrypto, unaccent and `daitch_mokotoff` use the
    deduplicating `dependency`; `Attach` keeps both.
  - unaccent rejects decorations as a missing signature, while
    `daitch_mokotoff` reports 42809 after finalization.
- Internal-error ordering: the "missing installed unaccent dictionary/template"
  errors now come after `CollatedInfo` instead of before. Both are
  corrupted-catalog invariants.

Tests: `TestLookupClaimsRegisteredNames` gained the 20 claims (folded,
upper-case, `ſ` and Kelvin-sign spellings) and near-miss non-claims
(`unaccent_init`, `unaccent_lexize`, `pgp_sym_encrypt`, ...). It failed before
registration. `TestExtensionRules` reads the embedded `StockUnaccent` rather
than the old file path. Engine tests that called the deleted binder methods
now bind through `bindFunctionProvider`:
- `extension_authority_test.go`, `extension_owners_test.go`,
  `extension_provider_test.go`, `extension_temporary_test.go`;
- `fuzzystrmatch_provider_test.go`, `levenshtein_expression_test.go`,
  `metaphone_expression_test.go`, `soundex_expression_test.go`;
- `pgcrypto_provider_test.go`, `pgcrypto_password_test.go`.

Two tests use the renamed wrapper field. No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists
only module paths; `functions` imports no engine state packages. Quick mode
passed for:
- `^TestReference(Pgcrypto|Levenshtein|Metaphone|Soundex|DaitchMokotoff)`;
- `^TestReference(Extension|AvailableExtensions|MaterializedAlterExtension)`;
- `^TestReference(ObjectGrant(Levenshtein|Metaphone|Soundex|PGCrypto|UUIDOssp)|UUIDGeneration|SearchPath|RegProc|RoleOwnership|QueryInt|DomainCheck)`.

With `REFERENCE=1`, every stuntgres subtest passed. The only failures are
`/postgres` subtests, which fail identically on master:
- the three known `UUIDGeneration` drift tests below;
- new drift, `TestReferenceLevenshteinCosts` and
  `TestReferenceLevenshteinBounds`. 18.6 added a levenshtein int32 overflow
  guard ("levenshtein distance out of range", 22003, or saturated results)
  where the 18.0 oracle expects wrapped values. Like the other drift, this
  needs a separate compatibility task.

Bench median 1.53 ms/op with 7,769 allocs/op (allocations unchanged; the
unaccent rules compile at `functions` package init instead of the engine's;
still above the 1 ms target as on master). Racecheck skipped at the user's
request.

Notes for later steps:

- intarray and intagg can reuse `installedExtensionRoutine` by adding an
  `Extension` value and an `Attach` case.
- `Lookup` now scans 27 definitions with 245 names, plus the JSON and
  variadic string predicates.

F4/F5 intarray, intagg, random, clock and sleep record, measured at its start
on `101168762` (after pgcrypto, fuzzystrmatch and unaccent):

- intarray: nothing to port. The extension installs only the `query_int`
  type and its `bqarr_in`/`bqarr_out` I/O procedures, which no call binds;
  installation and the type codec stay in the engine and `sqltype`.
- intagg: `int_array_enum` and `int_array_aggregate` called as functions
  sat before the shared gates (`EqualFold`) and register case-insensitively
  without gates. Resolution goes through `InstalledRoutine` with the new
  `IntaggExtension` on the shared `installedExtensionRoutine`. Its `Attach`
  takes the new `IntaggEnumeration` handoff node, applies the set-returning
  admission the old binder did after its decoration check (EXECUTE
  parameters, policy frames, `AdmitSetReturning`) and returns the engine's
  `unnestExpression`. `AnalyzeIntaggArguments` and `ResolveIntagg` are
  exported for the engine's aggregate and window binders, which read the
  procedure identity from the handle (`resolveIntaggAggregate`). The
  dependency is still appended without deduplication. Path lookup and the
  pure procedure lookup now happen together before the shape check, which
  keeps the error order (as for uuid-ossp).
- random and sleep sat in the final switch after the gates and
  `builtinVisible`; they register with `ExactSpelling` and `SharedGates`, and
  `Bind` checks visibility first. Random's NULL-literal fold uses `Literal`
  (same usage and immutable properties). Sleep keeps the unnoted
  `ResolutionError(..., false)`. Session RNG state (`random.go`),
  `FunctionRandomOwner` and the `EvalContext` clock and sleep methods stay;
  the sleep methods moved into `transaction_time.go`, whose bind code left.
- Clocks: the five `TransactionTimeFunctionName` names register
  case-insensitively without gates. Their 42883 names come from the new
  `functions.ArgumentTypeNames`, a copy of `functionArgumentTypeNames` over
  `AnalyzeArgument(ProjectionArgument)`. `current_date` and the other SQL
  value functions are syntax-dispatched: `BindCurrentDateTime` returns the
  precision warning as a value, and the engine's syntax switch delivers it
  under the old held-description condition. `TransactionTimeSQL` and
  `CurrentDateTimeSQL` moved to `functions` for the deparse and DEFAULT
  switches (F2 removes those uses); `closedTransactionTimeDefault` stays.
- Compatibility fix (approved at the session start): the clock binder
  analyzed FILTER with `bindBoolean` in the enclosing clause, so
  `now() FILTER (WHERE count(*) > 0)` reported the 42809 decoration error (or
  42883 for `now(1)`), and an SRF reported 42804. PostgreSQL 18.6 analyzes
  FILTER as `EXPR_KIND_FILTER` first: 42803, 0A000 and 42P20 "... are not
  allowed in FILTER". It now uses `AnalyzeFilter`, with the new
  `TestReferenceDatetimeTxTimesFilterContext`.
- A throwaway check confirmed that no earlier chain predicate (catalog
  relation, ACL value, object address, access inquiry, version information,
  commit timestamp, transaction identity, network format, blocking inquiry,
  identity, namespace inquiry, advisory, or the fixed `pg_get_acl`,
  `generate_series`, `generate_subscripts`, `unnest`, `current_schemas`
  names) claims any of the 13 names.

Tests: `TestLookupClaimsRegisteredNames` gained the 13 claims (folded,
upper-case and `ſ` spellings for the case-insensitive ones) and near-miss
non-claims (`RANDOM`, `Pg_Sleep_For`, `current_date`, `int_agg_state`,
`query_int`, ...); `random`, `pg_sleep` and `now` left the unclaimed list.
It failed before registration. Engine tests that called deleted methods now
use `bindFunctionProvider` (`intagg_expression_test.go`),
`functions.BindCurrentDateTime` (`current_datetime_test.go`) or the
exported renderers (`current_datetime_test.go`, `transaction_time_test.go`).
No test moved or was deleted.

Verification: `make quickcheck` passed (44 packages) except
`TestReferenceServerProtocol/server/malformed_messages/unknown_tag` (EOF),
a TCP timing witness unrelated to binding that passed 5 of 5 isolated reruns.
`make deps` lists only module paths; `functions` imports no engine state
packages. Quick mode and `REFERENCE=1` passed, on both backends, for
`^TestReference(Datetime(TxTimes|Current|Sleep)|Random|DeparseConstraintsReviewRandom|Intagg|QueryInt|ObjectGrantIntagg|PGAggregateIntagg|ComparisonInstalledQueryInt|SearchPath|ArrayRelationalRandom|PgcryptoRandom)`.
Bench skipped: bind path only, no startup code changed. Racecheck skipped
at the user's request.

Notes for later steps (intagg, random, clocks, sleep):

- `version_information.go` analyzes FILTER with `bindBoolean` like the clock
  binder did, so `version() FILTER (WHERE count(*) > 0)` likely shows the
  same divergence. Fix it with a reference witness when that family ports.
- Set-returning admission (EXECUTE parameters, policy frame,
  `stringSetAllowed`, target set) now also lives in the intagg `Attach`; fold
  it into the shared admission capability with the other set-returning
  builtins.
- `Lookup` now scans 31 definitions with 258 names, plus the JSON and
  variadic string predicates.

F4/F5 settings reads, aggregates and windows record, measured at its start on
`101168762` and rebased onto `5b986e558` (intagg, random, clocks, sleep) and
`a3a2f0548` (catalog 5b):

- Settings reads (scope chosen by the user). `current_setting` and
  `set_config` exist only as the statement form `SELECT current_setting(...)`
  (`parser.SettingCall`); no expression-level settings family exists, so
  nothing registers. `describeSettingCall`'s binding (visibility, then each
  argument in assignment context, then routine identity) moved to
  `functions.BindSettingCall`. The `AssignmentFormalArgument` intent binds the
  unanalyzed literal or parameter in assignment context, which
  `DirectFormalArgument` (function-argument context) does not.
  `binder.finish()`, execution and settings locking stay in `setting_calls.go`.
- Aggregates are not name-chain calls: they arrive as `parser.AggregateExpr`
  with folded names. Two exact-match static tables replace the engine's
  if-chains: `aggregateDefinitions` (15 definitions, 55 names) and
  `windowFunctions` (5 definitions, 11 names). Engine dispatch consults them
  where the chains tested names, after the same gates. The names are
  disjoint, so precedence is unchanged. An unregistered aggregate name still
  binds as count.
- `AggregateBinder` (scalar `Binder` plus six methods, listed in the
  `functions` README) keeps aggregate-only capabilities off the scalar
  interface. `AggregateCall` carries the function-owned part of the engine's
  `aggregateCall`. `FinishAggregate` adds ORDER BY, DISTINCT, merging and
  registration.
- Corrections to the plan:
  - The duplicated INSERT ... VALUES namespace prelude (sum, `bit_*` and
    intagg) became one engine helper. Definitions select it with
    `ValuesNamespace`, a value instead of a method.
  - Every aggregate is now owned in dispatch. The JSON aggregates owned
    themselves inside bind. `BindReturning` now returns their RETURNING usage
    and the engine applies the coercion after ownership, in the same order as
    before.
  - count's argument binding needs the new `BareProjectionArgument` intent.
    `ProjectionArgument` marks unknown NULLs, which would change deparse and
    grouping identity.
- Window binding asks the window registry, then `ResolveWindowAggregate`
  (registered `Window` entries, including count), for every signature. Its
  operation mapping, formals, frames and execution stay in the engine, as do
  `windowValueAliases` (now in `window_value_query.go`), `rejectOrdinaryWithin`
  (`aggregate_dispatch.go`) and `rewriteOrderedSetDirect` (`query_bind.go`).
- Providers that moved:
  - money/interval SUM: `MoneySum`, `IntervalSum`;
  - JSON object: `JSONObjectAggregate`, completed by `FinishAggregate`;
  - WITHIN GROUP: `OrderedSet`, `HypotheticalSet` behind `OrderedAggregate`.
  The executor keeps sorting and replay. It lends the sorted group as an
  `OrderedInput` whose `Compare` closes over the call's sort keys (one
  closure per ordered finalization). `AggregateOrdering` and the SQL/JSON
  RETURNING helpers (`JSONReturningCoercion`, `ValidateJSONOutputFormat`,
  `JSONReturningPseudoType`) became exported and the engine callers were
  repointed. No aliases were added.
- 23 engine glue files are deleted. These are the 11 simple aggregate binders,
  `money_sum.go`, `interval_sum.go`, the three JSON aggregate files, the four
  ordered-set and hypothetical-set files, and the three window binder files.
- Deviation: each family slice is one "move" commit rather than separate
  port and move commits. The bind code was written against the interface
  directly in `functions`, where the compiler enforces it more strictly than
  in the engine.

Tests in F4/F5 settings reads, aggregates and windows:
- Added `TestAggregateDefinitionsAreWellFormed`,
  `TestLookupAggregateClaimsRegisteredNames` (failed before registration) and
  `TestLookupWindowFunctionClaimsRegisteredNames` (added with the window
  registry).
- Moved to `functions/money_interval_sum_test.go`, adapted to builtin
  descriptor generations: `TestMoneySumProvider`,
  `TestMoneySumProviderCancellation`, `TestIntervalSumProvider` and
  `TestIntervalSumProviderCancellation`. The view-remap tests stay in the
  engine (`money_interval_sum_remap_test.go`).
- Adapted engine tests:
  - `sum_test.go` and `ordered_set_aggregate_test.go` bind through
    `bindRegisteredAggregate`.
  - The bitwise and statistical window tests have test-local mappings in
    place of the deleted helpers.
  - The JSON object tests use the provider's constructor and accessors.
  - The ordered-set and hypothetical-set tests use `NewOrderedSet`,
    `NewHypotheticalSet` and accessors, where they had set fields, and lend
    input through the executor's `borrowed`. Their frame-name suffixes now
    name `(*OrderedSet)` and `(*HypotheticalSet)`.
  - Operation constants and `AggregateOrdering` are now referenced from
    `functions`.
- No test was deleted.

Verification:
- `make quickcheck` passed (44 packages). `make deps` lists only module
  paths, and `functions` imports no engine state packages.
- Quick mode passed for each slice's selection (settings, simple, JSON,
  ordered-set and window aggregates; the selectors are in the `functions`
  README).
- `REFERENCE=1` passed for
  `^TestReference(Settings|ObjectGrantSettingCallAuthority|SearchPath|TimestampSettingArguments|PreparedRebind|FormatEncoding)`,
  for the simple and JSON aggregate selections, and for the ordered-set,
  hypothetical, window, grouping-sets, view-replace, deparse and intagg
  selections. The only failure is the new
  `TestReferenceHypotheticalAggregateMultiKey/postgres` drift listed below,
  which fails identically on master.
- Bench skipped: no startup code changed.
- Racecheck skipped at the user's request.

Notes for later steps (aggregates and windows):

- `int_array_aggregate` can register once `AggregateCall` carries an
  installed-routine handle for the procedure identity that merging
  (`sameIntaggIdentity`) and view remap read.
- `AnalyzeFilter` could become a wrapper of `AnalyzeAggregateFilter`.
- `bindWindow` still maps names to window operations and formals, and the
  partition consumer gate in `expression.go` keeps its own name list (window
  functions minus the rank family). Move them into `WindowFunction` when
  window planning gets an owner.
- The grouping switches in `aggregate_bind.go` (`sameGroupExpression`,
  `rewriteGroup`, `aggregateOccurrence`) list function nodes; they belong to
  the F2 follow-up list.
- New oracle drift, failing on master `5b986e558` too, `/postgres` only:
  `TestReferenceHypotheticalAggregateMultiKey` (18.6 reports "aggregates
  cannot have more than 99 arguments" at the 100-operand boundary).

F2 session 1 record, measured at its start on `604ee2be6`:

- Function cases removed per switch: walk 21 (plus 35 in 2c), join legality
  55, volatility 49, conditional planning 94 (simplify 52, constant inputs
  42), generated 125 (admission 42, `tableoid` 42, stored validation 41),
  retag 55, remap 55. Each branch is the switch's last case, after the
  installed-routine wrapper cases, which stay because their catalog identity
  is engine state.
- Protocol membership: the 24 remaining nodes (the derived-input nodes listed
  above, `Timezone`, `Format`, `NullCount`, `Random` and the leaves
  `CurrentDateTime`, `VersionInformation`, `TransactionTime`). `Rebuild` keeps
  the inputs derived at binding; only `Retag` replaces them. The uuid-ossp
  wrapper overrides `Rebuild` like the 2c wrappers.
- Hooks (reasons in the `expr` README): `Intrinsic`, `Folding`,
  `StrictFolding`, `StoredProvider`, `Retaggable` with the `Retag`
  environment, `Annotated`. The two engine-state folds (OVERLAPS inline
  route, quote capture) are fold kinds the engine dispatches in
  `conditional_fold.go`, as decided at the session start.
- Corrections to this prompt: retag needs the engine's change bit, not only
  an environment, because cases gate differently (for example `String`
  revalidates but ignores a text-usage-only change, `Binary` rejects a nil
  argument even when unchanged). Generated admission was a copy of the
  volatility knowledge and now reads `Intrinsic`. `conditionalConstantInputs`
  is not "intrinsic immutable": engine tests pin a STABLE `enum_first` with
  literal arguments as constant inputs and STABLE JSON creation as not, so the
  calls planning never evaluates are `FoldNever`. Stored-plan validation
  calls `Validate()` only on the six nodes it validated before, through the
  distinct `ValidateStoredProvider`; a generic `Validate()` hook would have
  reached eight more nodes. The remap switch copies every node, so its
  branch reports every child as replaced.
- `Timezone` children follow evaluation order (zone, value). The planner and
  PostgreSQL 18.6 use that order (`(1/0)` value and `(2147483647+1)::text`
  zone fail with "integer out of range" on both); the lexical walk visited
  the value first and keeps an explicit case for it (see notes).
- `generationHasTableOID` now also walks the children of `random`,
  `pg_sleep`, the JSON set functions and `RegexpSet`, which it skipped. None
  can appear in a generated column or index expression; only the partition
  CHECK recheck on trusted import with an owner change could observe it, and
  then it rechecks more, not less.
- `mapRegexpChildren` was deleted (retag was its only caller). The engine no
  longer calls `functions.SelectedLogPower` or `functions.MathWrapperContext`;
  they are unexported.

Tests in F2 session 1: added `functions/composite_derived_test.go`
(`TestCompositeTimezoneChildrenFollowMode`,
`TestCompositeRetainedPlanKeepsTrailingSlots`,
`TestCompositeStringKeepsTextUsage`, `TestCompositeLeafRebuildTakesMetadata`,
`TestCompositeProviderlessRebuildOwnsChildren`,
`TestIntrinsicVolatilityExcludesChildren`, `TestPlanningHooks`,
`TestRetagReportsDerivedInputChanges`,
`TestStringVariadicAnnotationRoundTrip`) and the engine's
`TestInstalledUUIDOsspRebuildKeepsWrapper`. No test moved, was adapted or was
deleted.

Verification: `make quickcheck` passed after each commit and after each
rebase (44 packages; final base `ec3fcadd4`, where the regexp, string and
view-replace selections below were rerun with `REFERENCE=1`); `make deps` lists only module
paths; `expr` and `functions` import only `ids`, `parser`, `sqlstate` and
`sqltype`. `REFERENCE=1` selections passed on the stuntgres side:
`^TestReferenceView`, `^TestReferenceGenerated`,
`^TestReference(Default|Check)`, `^TestReferenceDeparseConstraints`,
Dump/Restore names, `StoredConsumers` names and
`^TestReference(Datetime|Regexp|Regex|Similar|Posix|Timezone|AtTimeZone|String)`.
Their only failures are the `/postgres` subtests already listed as 18.6 drift
(the two `CheckReview` connection tests, `UUIDGenerationStoredConsumersAndControls`,
`DatetimeExtractCanonicalDefinitions`, `DatetimeFormattingReviewCollationDemand`,
`StringCaseReviewSigma`, `StringCoreNormalization`,
`StringCoreValuesSourceConsumerRoutes`, `StringSplitLocking`; the last two
were rechecked on master with the same failing subtests). A throwaway probe
compared master and the branch on generated-column admission (immutable,
STABLE, volatile, set-returning, `unicode_version()`, `pi()`, dead CASE arms)
and the AT TIME ZONE ordering cases: identical output. Bench: medians
1.70 ms/op on master and 1.77 ms/op on the branch, 7,769 allocs/op on both
(the 2c note on allocating `Children` did not bite: these switches are
plan-time; startup is still above the 1 ms target as on master). Racecheck
skipped at the user's request.

Notes for later steps (F2):

- Compatibility bug, outside the split: `SELECT a AT TIME ZONE b FROM t GROUP
  BY c` reports `t.a` where PostgreSQL 18.6 reports `t.b`, and nested
  aggregates inside AT TIME ZONE report the value's aggregate first. Fix by
  dropping the walk's explicit `Timezone` case (the Composite branch already
  visits the zone first) together with reference coverage.
- Compatibility bug, outside the split: `GENERATED ALWAYS AS
  (unicode_version()) STORED` (also VIRTUAL, and `icu_unicode_version()`)
  fails with "unsupported generation plan provider
  functions.VersionInformation"; PostgreSQL accepts these immutable calls.
  Admission accepts them, stored-plan validation does not. The fix is a
  `ValidateStoredProvider` on `VersionInformation` plus reference coverage.
- More engine switches still list function nodes (case lines at the end of
  session 1): `aggregate_bind.go` 159 (grouping identity
  `sameGroupExpression` and aggregate argument binding), preflight in
  `expression.go` 67 (`preflightCoercionsWithPolicy`, which still uses the
  exported `Map*Children` helpers), `string_table_bind.go` 36,
  `index_control.go` 30, `default.go` 15, `string_set_query.go` 12,
  `view_literals.go` 11 and a few smaller ones. They are not in this
  prompt's list; convert them after the render hooks, reusing the existing
  hooks where they fit.

F2 session 2 record (deparse and node tree; stored scalar SQL was left for a
later session by agreement), measured at its start on `ae1fc88b1`:

- Function-node cases removed: node tree 55 (`appendBoundTree`), deparse 55
  (`pgRenderer.sql`, about 60 case lines) plus the coercion case's
  `simpleFunctionCall` type switch. Each switch has one final
  `case expr.Renderable:` branch after the installed-routine wrapper cases.
- One hook, `expr.Renderable`, returns one `expr.Rendering` value (see the
  `expr` README). Node tree reads `Tree`/`Inputs`; the deparser reads the SQL
  form through `deparse_rendering.go`. The value encodes every per-node
  difference the old cases had: four name-quoting rules (only under
  quote_all_identifiers, verbatim, `r.identifier`, `pgIdentifier`), three
  infix grouping rules (ordinary precedence; ArraySearch membership groups by
  precedence in every mode with plain operand parents; ArrayTransform `||`
  always), per-argument implicit-cast display, function-argument position,
  unknown-text flags that replace the inherited one only for `format` and
  JSON creation, display casts with three bare-argument rules (String
  textual, OVERLAPS local clock, pg_size_pretty), and `SimpleCast`.
- `Rendering.Inputs` is not `Children`: AT TIME ZONE lists the value first,
  and SQL-syntax EXTRACT and the written-slot calls list their inputs in tree
  order, which differs from written order.
- `Render` runs the union of the validation the two renderers ran, so node
  tree now also validates `ArrayTransform`, `JSONExtract`, `DatetimeSleep`
  and the written-slot calls, and deparse also validates `CurrentDateTime`.
  This only rejects corrupted nodes; one integrity message changed
  (a non-literal NORMALIZE form is now "invalid deparse syntax keyword").
- Deleted: `datetimeOverlapsPublicSQL`. Kept for stored scalar SQL:
  `renderBinaryCall`, `regexpSQL`, `datetimeMakeSQL`, `stringSpecialSQL`,
  `versionInformationSQL`, `functions.*SQL` and `CurrentDateTimeSQL`/
  `TransactionTimeSQL`. `functions.Binary.Operator` is new; the engine's
  `jsonbContainmentName`-style predicates stay for binding and stored SQL.
- Byte identity: a throwaway probe (not committed) bound 332 expressions
  covering every node and form into views, CHECK constraints, expression
  indexes, generated columns and defaults, and compared pg_get_viewdef
  (plain and pretty), pg_get_constraintdef, pg_get_expr, pg_get_indexdef and
  `conbin`/`adbin` text with quote_all_identifiers off and on: identical on
  master and after each commit. Views mostly deparse from raw trees; the
  CHECK, index and node-tree paths exercise the bound renderer.

Verification in F2 session 2: `make quickcheck` passed after each commit;
quick-mode and `REFERENCE=1` selections
`^TestReference(View|Generated|Default|Check|Deparse)`,
`^TestReference(Datetime|Regexp|Regex|Similar|Posix|Timezone|AtTimeZone|String)`,
`^TestReference(Binary|Array|Range|JSON|Math|Format|UUID|Network|Enum|Random|NullCount)`
and `^TestReference.*(Dump|Restore|StoredConsumers)` passed on the stuntgres
side. Their only failures were `/postgres` subtests where PostgreSQL 18.6
differs from expectations written for 18.0: the drift listed in session 1
plus `JSONStripNullsStable`, `JSONStripNullsFeatureSmoke`,
`UUIDGenerationFeatureSmoke` and `UUIDGenerationGuardDiagnostics`. Racecheck
skipped at the user's request; no bench (deparse is not on the startup path).

Tests in F2 session 2: added `functions/render_test.go`
(`TestRenderTimezoneTreeKeepsWrittenOrder`,
`TestRenderBinaryTreeMarksTrimSyntax`, `TestRenderArrayTransformValidates`,
`TestRenderLeafValidatesIntrinsicProperties`, `TestRenderBinaryForms`,
`TestRenderStringSpecialSyntax`, `TestRenderWrittenSlotsKeepLabels`,
`TestRenderFormatOwnsUnknownTextFlags`). No test moved, was adapted or was
deleted.

Notes for later steps (F2):

- Stored scalar SQL is the next switch. It should read the same
  `Rendering`, adding what replay needs: `pg_catalog.` names and
  `OPERATOR(pg_catalog.op)` spellings, replay variants where display and
  replay differ (POSIX `SUBSTRING ... FROM`, EXTRACT's field literal,
  `SIMILAR TO ... ESCAPE`, the lower-case SQL value functions), per-argument
  CAST transport (NAME, base conversions, ANY-typed literals in `format`,
  `concat` and JSON creation) and the snapshot flags. Then the helpers kept
  above can go.
- `Arguments()` still duplicates `Children()` on several nodes; preflight in
  `expression.go` and `aggregate_bind.go` still call it.

F2 session 3 record (stored scalar SQL), measured at its start on
`423c4a9da` and rebased onto `5fdfd0f0c`:

- Function-node cases removed: 37 in `scalarFunctionSQL` and 16 in
  `scalarRenderer.sql` (plus 34 function types from its shared case list).
  `sql` routes a final `case expr.Renderable:` to `scalarFunctionSQL`, whose
  explicit SQLJSON, installed-wrapper and engine-node cases precede its own
  protocol branch; `scalar_rendering.go` interprets the `Rendering`.
  `scalarFunctionSQL` lost its unused usage parameter.
- Protocol: `Rendering.Replay` and three `RenderArg` fields (see the `expr`
  README). `Replay.Validate` is lazy: the stored checks rebind providers
  (String, Math, Enum, Binary, ranges, arrays) and are stricter than display.
  Running them inside `Render` panicked on partially built unit-test nodes
  and could have added deparse failures, so display never calls them and
  deparse and node-tree output are unchanged.
- Corrections to the session 2 notes: replay renders every argument in
  order before assembling the form, because the POSITION and TRIM templates
  reference arguments out of order and NORMALIZE's form literal is still
  captured though not printed (CHECK-literal `$n` numbering and dependency
  order depend on it). Replay always quotes labels (`catalog.QuoteIdentifier`), while
  display quotes them when needed. Operator schemas differ by node: array and
  range operators always quote a written schema, POSIX operators quote it when
  needed, and binary and string operators always write
  `OPERATOR(pg_catalog.op)`; those two now state the `pg_catalog` schema,
  which display already shows bare. Set-returning nodes that the old
  `default.go` list left out (RegexpSet, JSONBPathQuery, the JSON key, each
  and element sets, JSONBRecord) refuse through `Replay.Validate` with the
  old "unsupported stored default node". SQLJSON keeps its explicit case, as
  the JSON operators record expected.
- Messages changed only for corrupted nodes: a foreign pg_sleep clock type is
  now "foreign stored function type", and nodes whose `Render` already
  validated report that error before the replay checks.
- Deleted: `binary_render.go` (`renderBinaryCall`),
  `datetime_make_render.go`, `datetime_extract_render.go`, `regexpSQL`,
  `stringSpecialSQL`, `trimSyntaxSQL`, `stringArgumentSQL`,
  `versionInformationSQL`, `datetimeOverlapsLogicalSQL`, `rangeOperatorSQL`
  and the `functions` stored-SQL renderers (`JSONStripNullsSQL`,
  `JSONExtractSQL`, the `JSONBPath*SQL`, `JSONBSetSQL`, `ArrayInspectSQL`,
  `ArrayRandomSQL`, `ArrayTransformSQL`, `ArraySearchSQL`). The remaining
  child mappers moved to `json_extract_children.go` and
  `array_node_children.go`. `CurrentDateTimeSQL` and `TransactionTimeSQL` are
  private. Raw view deparse spells SQL-standard calls through the exported
  `functions.SyntaxPieces` and `TrimPieces` and the engine's `templateSQL`.
- Byte identity: a throwaway probe (not committed) put 334 expressions,
  covering every node and form (labels, VARIADIC, the SQL-standard
  syntaxes, NORMALIZE and IS NORMALIZED, SIMILAR with and without ESCAPE,
  EXTRACT, OVERLAPS, `^@`, schema-qualified operators, NULL uuidv7 shifts,
  format/concat/JSON creation literals, regclass arguments, pg_size_pretty,
  num_nulls field notation and the set-returning refusals) into DEFAULTs,
  column and literal CHECKs, STORED and VIRTUAL generated columns,
  expression indexes and domain CHECKs, then after `ALTER COLUMN TYPE`. It
  rendered every stored definition with plain, flag-collecting, owner,
  CHECK-literal and replay-precision renderers, printing text, error,
  dependency order and the comment, visibility and alias flags (11,520
  successful renders), plus native Dump output. Master and the branch
  matched on both bases, except one unreachable path: a SIMILAR whose
  normalizer the planner folded away fails with the same error but no
  longer records the left operand's dependencies first; stored SQL never
  renders planned forms.

Tests in F2 session 3: added `functions/render_replay_test.go`
(`TestRenderReplayForms`, `TestRenderReplayNames`,
`TestRenderReplayOperatorSchemas`, `TestRenderReplayTransport`,
`TestRenderReplaySnapshots`, `TestRenderReplayFaults`), which failed before
the nodes stated their replay form. Adapted, because they called deleted
helpers: the position check in `TestBitStringFunctionsBindingAndReownership`
and `TestRangeOperatorRetagAndStoredAuthority` now render through
`scalarFunctionSQL` (the latter asserts the real, quoted
`OPERATOR("pg_catalog".+)`), and the array_sample, JSON extraction, current
datetime and transaction time checks read `Render`. Ten call sites in eight files dropped
the usage argument. No test moved or was deleted; no reference test changed.

Verification in F2 session 3: `make quickcheck` passed before and after the
rebase (44 packages); `make deps` lists only module paths; `functions` and
`expr` import no engine state packages. `REFERENCE=1` selections
`^TestReference(View|Generated|Default|Check|Deparse)`,
`^TestReference(Datetime|Regexp|Regex|Similar|Posix|Timezone|AtTimeZone|String)`,
`^TestReference(Binary|Array|Range|JSON|Math|Format|UUID|Network|Enum|Random|NullCount)`
and `^TestReference.*(Dump|Restore|StoredConsumers)` passed on both backends
with no failures. Racecheck skipped at the user's request; no bench (stored
SQL is not on the startup path).

F4/F5 JSON operators, SQL/JSON constructors and `IS JSON` record, measured
at its start on `91357248c` (after every other Tier A family):

- Seam. The operator binders used the engine's `comparisonError` and
  `arrayMembershipError`. Both are pure over existing `Binder` methods
  (`DiagnosticTypeName`, `ArrayTypeName`; `qualifiedName` equals the engine's
  `objectNameText`), so they moved as `functions.ComparisonError` and
  `functions.ArrayOperatorError`; their 11 engine callers outside the JSON
  files (array comparison and membership, ranges, comparison) were
  repointed. No method was added.
- Operators (`json_operator_bind.go`). They are syntax-dispatched, so nothing
  registers: the builtin operator binder calls `BindJSONExtraction`,
  `BindJSONBExistence` and `BindJSONBPathDelete`; the `||`, `-` and
  `@>`/`<@` selectors call `SelectJSONBConcat`, `SelectJSONBDelete` and
  `BindJSONBContainment` with their analyzed operands, converted by the
  engine's new `operandArgument`. The three name predicates are exported for
  dispatch, stored scalar SQL and index inquiry. `ArrayType` replaces the
  direct `LookupArray(text)`; for the builtin text type both return the same
  descriptor. Five engine glue files are deleted.
- SQL/JSON constructors and `IS JSON`. `functions.SQLJSON` and
  `functions.JSONPredicate` replace the engine's `sqlJSONExpression` and
  `jsonPredicateExpression` and implement `expr.Composite`, `Intrinsic`
  (SQLJSON: its bound volatility, which the old volatility and generated
  cases read; IS JSON: immutable), `StoredProvider` (the stored-plan
  validator's protocol branch requires it; the old cases validated only the
  children) and `Renderable`. IS JSON's SQL uses the template form with
  hidden implicit casts and no parent, which is what the old `child()` call
  rendered. `Rebuild` now checks the child count (an integrity check only a
  corrupted call reaches).
- Corrections to the plan: RETURNING stays an engine coercion, as for the
  JSON aggregates. `BindSQLJSON` returns the RETURNING usage and the engine
  wrapper applies it in the old order (capture, plan, `coercionInfo`). The
  engine also keeps the invalid-kind check and the subquery form
  `JSON_ARRAY(SELECT ...)`. `SQLJSON` has no SQL `Rendering`: its arguments
  display through the engine's formatted-input coercion unwrap
  (`sqlJSONSource`), and its RETURNING comes from the enclosing coercion. So
  the deparse and stored-SQL cases stay explicit. Formatted input reuses the
  `JSONFormattedArgument` intent; `SQLJSONInput` also replaces the aggregates'
  `formatJSONInput`, which was the `format != nil` case of the same code.
  `JSON_OBJECT` keys keep an untyped parameter unfinalized only when it is a
  parameter, so `RetainUntypedParameter` now returns whether it retained one
  (the argument kind alone cannot tell a parameter from other untyped nodes).
- Switch cases removed (now protocol branches): walk, join legality,
  volatility, conditional simplify and constant inputs, generated admission
  (three), retag, remap, node tree and the IS JSON deparse case. The old
  conditional, retag and remap cases matched the protocol defaults: fold as
  a constant only when immutable with literal children, never strict, rebuild
  on change. Explicit cases on the exported types remain in the switches that
  have no protocol branch: `aggregate_bind.go` (grouping identity, rewrite,
  walk), preflight in `expression.go`, `default.go`, `scalar_function_sql.go`,
  `index_control.go`, the DEFAULT closures in `transaction_time.go` and
  `uuid_generation.go`, `view_literals.go`, the deparser's SQLJSON and
  RETURNING-coercion cases and `sqljson_deparse.go`.
- Byte identity. A throwaway probe (not committed) bound 42 expressions:
  every constructor form with FORMAT/ENCODING/RETURNING/ABSENT/UNIQUE, every
  IS JSON form, and every JSON operator, including error cases. It put them
  in views, CHECK constraints, expression indexes, STORED and VIRTUAL
  generated columns and defaults. It compared direct and parameterized
  results, `pg_get_viewdef` (plain and pretty), `pg_get_constraintdef`,
  `pg_get_indexdef` and `pg_get_expr` with `quote_all_identifiers` off and
  on, and native Dump output. Output was identical on master and the branch,
  except for `now()` values.

Tests: added `functions/sqljson_expression_test.go` (`TestSQLJSONProtocol`,
`TestJSONPredicateProtocol`), which failed before the nodes moved. No
engine test referenced the moved binders or nodes, so none was adapted. No
test moved or was deleted.

Verification: `make quickcheck` passed (44 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. Quick mode and
`REFERENCE=1` passed, on both backends with no failures, for
`^TestReference(JSON|SQLJSON|ObjectGrant.*JSON|Array|Range(Operator|More)|Enum|Comparison|StringConcat|View|Generated|Default|Check|Deparse)`.
Bench skipped: bind path only, no startup code changed. Racecheck skipped at
the user's request.

Notes for later steps (JSON operators and SQL/JSON):

- F2 stored scalar SQL: `SQLJSON` needs a rendering that knows the
  formatted-input coercion and RETURNING, or the engine keeps
  `sqljson_deparse.go`. `jsonPredicateRecipe` now reuses
  `functions.JSONPredicateSuffix`.
- The `JSON_ARRAY(SELECT ...)` subquery form and the constructor's RETURNING
  coercion stay with the engine's query and coercion nodes.

F2 session 4 record (the remaining switches; the requested stored scalar SQL
sub-step was already done), measured at its start on `50d842db3` and rebased
onto `5cc6fe6e5`:

- Function-node cases removed: grouping identity 57
  (`sameGroupExpressionIn`), grouping rewrite 53 (`rewriteGroup`), aggregate
  occurrence 53 (`aggregateOccurrence`), preflight 54
  (`preflightCoercionNode`, now `preflight_composite.go`), index key display
  41 (`pgIndexFunctionExpression`) and 38 from each closed-DEFAULT check
  (`closedUUIDDefault`, `closedTransactionTimeDefault`). Each switch has one
  final protocol branch after the installed-routine wrapper cases.
- Hooks (reasons in the `expr` README): `GroupIdentity`, `GroupLeaf`,
  `Preflighting` with `ConstantTemplate`, `Substitutable`,
  `EvaluationOrdered`, `FunctionSyntax` and `ClosureTransparent`, plus
  `expr.MapSubstituted` and `expr.Substitute`. `sqltype.SameRecordUsage` and
  `OccurrenceRecordArray` replace the engine's pure record-shape helpers so
  the record nodes can compare usages.
- Corrections to the plan: `Rebuild` is not a drop-in for the old in-place
  child writes. Preflight produced a child usage the provider would not bind
  again (`pg_size_bytes('1 MB'::name)` failed `TestReferenceSizeSignatures`
  with "invalid math argument usage" until `b2675f4c2`), so preflight and the
  grouping rewrite substitute children through `Substitutable`. The grouping
  rewrite left the three leaves unstaged but staged zero-argument `random()`
  and `gen_random_uuid()`; no volatility rule reproduces that
  (`clock_timestamp()` is volatile and unstaged), hence `GroupLeaf`.
  `ArrayTransform` preflights in formal-slot order while its `Children` keep
  written order, hence `EvaluationOrdered`. The two closed-DEFAULT checks
  differ on `date_trunc`, `date_bin` and the zone calendar calls (only the uuid
  check looks through them), which stay explicit there.
- View literal capture was deferred in session 4 because raw syntax kind,
  written/default operand correspondence and display annotations differ from
  runtime children and `Render`. The subsequent capture conversion uses
  `expr.Capturable` and allocation-free indexed access to fixed node storage;
  the engine still owns structural paths, coercions and catalog identities.
  Function sources remain deferred: their execution cursor still needs an
  `expr` cursor interface.
- Deleted: `functions.MapArray*Children` (`array_node_children.go`),
  `MapJSONExtractChildren` (`json_extract_children.go`),
  `JSONBRecord.MapChildren`, `mapNetworkFormatChildren`,
  `mapRangeOperatorChildren`, `preflightRegexpCall`, `sameRegexpCall`,
  `expr.MapEvaluated` (renamed before landing) and the 17 `Arguments()`
  methods.
- Byte identity: a throwaway probe (not committed) ran 214 statements direct
  and prepared on master and the branch: GROUP BY matching and its errors
  over every node family, aggregates nested in calls in WHERE and HAVING,
  DISTINCT ON, preflight folds with literal NULLs, templates and named
  arguments, 25 stored DEFAULTs with `pg_get_expr`, CHECK constraints, 19
  expression indexes with `pg_get_indexdef`, a grouped view and set-returning
  calls in FROM. Output was identical.

Tests in F2 session 4: added `functions/group_identity_test.go`
(`TestGroupIdentityPrecisionOnlyUnderDefinitionEquality`,
`TestGroupIdentityIgnoresPresentation`,
`TestGroupIdentityTimezoneLocalIgnoresZone`,
`TestGroupLeafMarksOnlyChildlessLeaves`), `functions/preflight_test.go`
(`TestPreflightFolds`, `TestPreflightTemplateNeedsLiteral`,
`TestArrayTransformEvaluatesFormalSlots`, `TestSubstituteKeepsTheProvider`),
`functions/function_syntax_test.go` (`TestFunctionSyntaxFollowsSpelling`,
`TestClosureTransparencyExcludesGenerators`) and in `expr`
`TestMapSubstitutedFollowsEvaluationOrderAndKeepsPositions` and
`TestSubstitutePrefersTheHook`. Adapted, because they called deleted
helpers: the child-mapping checks of `TestArrayInspectStoredRemap` (now
`expr.Map`; the changed-child case maps a same-generation child, because
`Rebuild` revalidates and rejects the other generation's type), the
unchanged-mapping check of `TestArrayRelationalExpressionOwners` (now
`expr.Map`), the checks in `TestArrayTransformResolverOwners` and
`TestArrayTransformNamedAndConcatSelection` (now `expr.MapSubstituted`,
still pinning formal-slot order), the copy-on-write checks of
`TestJSONExtractInferenceAndLifecycle` and
`TestJSONBExtractInferenceAndLifecycle` (now `expr.Map`) and five engine
tests that read `Arguments()` (now `Children()`).
No test moved or was deleted; no reference test changed.

Verification in F2 session 4: `make quickcheck` passed (it caught the
`Rebuild` regression fixed in `b2675f4c2`) and passed again on the rebased
branch; `make deps` lists only module paths; `expr` and `functions` import no
engine state packages. Quick mode and `REFERENCE=1` passed on both backends
with no failures for
`^TestReference(Group|Aggregate|Having|Window|Distinct|QueryAggregate|QueryWindow|GroupingSets)`,
`^TestReference(View|Generated|Default|Check|Deparse|Index)`,
`^TestReference(Datetime|Regexp|Regex|Similar|Posix|Timezone|AtTimeZone|String|JSON|SQLJSON|Array|Range|Binary|Math|Format|UUID|Enum|NullCount|Random|Size)`
and `^TestReference.*(Dump|Restore|StoredConsumers|SetReturning|FunctionScan|RowsFrom)`.
Racecheck skipped at the user's request; no bench (plan-time and
statement-preparation paths, not startup).

Notes for later steps (F2 session 4):

- View literal capture is now converted. The historical 67-reference count
  had grown to 71 before this cutover. Raw-kind admission, borrowed slice or
  bounded inline operands, and indexed fixed storage preserve written order
  without evaluating or rendering nodes. Engine wrappers retain precedence.
  Focused internal and pinned PostgreSQL18.6 witnesses, byte-identical public
  deparse/Dump smoke and quickcheck passed; remaining checks were waived at
  user request. Package READMEs document the protocol and exact selectors.
- Function sources: `isQuerySet`, `validFunctionSourceCall`,
  `functionCallArguments`, `withFunctionCallArguments`,
  `sameQuerySetOccurrence` and `openFunctionCall` need a set-source hook and a
  cursor seam that keeps the typed cursors allocation-free.
- Conditional simplification maps `ArrayTransform` children in written order
  since session 1 (`4c54eaf54`), while preflight keeps formal-slot order.
  With named arguments in reverse order and two failing constant arguments
  the reported error could differ from PostgreSQL's positional evaluation;
  check against 18.6 and use `expr.MapSubstituted`-style ordering if so.
- Compatibility gaps seen in the probe, identical on master: a stored
  `DEFAULT pg_size_bytes('1 MB'::name)` fails with "invalid stored math
  provider" (PostgreSQL accepts it); `CREATE INDEX ON t (substring(a from
  'b'))` and `(extract(year from ...))` are syntax errors, although
  PostgreSQL's `index_elem` accepts `func_expr_common_subexpr` without extra
  parentheses; `pg_get_indexdef` fails with "unsupported deparse constant type
  4072" for an index over `jsonb_path_query_first(j, '$.k')`. Each needs
  reference coverage first.
- `pgIndexFunctionExpression` answers false for installed extension calls
  (for example an index on fuzzystrmatch `soundex(a)`), which PostgreSQL
  prints bare; check before giving the wrappers `FunctionSyntax`.

Tier C refinement, measured on `5cc6fe6e5` with the ACL part done first:

- **Pattern.** A Tier C family moves like a Tier A family (seam, port in
  place, move), with one addition for evaluation: a narrow capability
  interface in `functions` (`ACLRuntime` is the first), which nodes recover
  from `expr.Context` by type assertion and the engine's `*EvalContext`
  implements with a compile-time assertion. The interface takes values
  (OIDs, names, text), and the engine method keeps the lock, authorization
  recapture and error order of the old `Eval`. `expr.Context` does not grow
  per family. Bind code uses `Binder`; registry entries keep the chain's
  position (these branches sit before the shared gates).
- **What stays in the engine** for every part: the capability
  implementation, set-returning admission and function scans (the
  function-source switches have no set-source hook yet), result nullability
  (`query_bind.go`, `returning.go`), view literal pairing, the
  closed-DEFAULT check, and statement input/output adapters such as the
  aclitem and regrole resolvers and formatters.
- **Parts**, one session each, can run in parallel: they touch the same
  switch files, `function.go` and these docs, so rebase early.
  - ACL SQL function nodes: done (record below).
  - Catalog inquiry: `catalogUserNameExpression`,
    `catalogForeignKeysExpression`, `objectAddressExpression`,
    `namespaceInquiryExpression`, `currentSchemasExpression`,
    `visibilityInquiryExpression`, `indexPropertyExpression`,
    `commentInquiryExpression` and `deparseExpression`. They read the
    statement's catalog snapshot and search path: a catalog inquiry
    capability.
  - Transaction identity and commit timestamps:
    `transactionIdentityExpression`, `commitTimestampExpression` and
    `age(xid)` (`BindDatetimeAgeFinite` then loses its node-builder value).
    They read `visibility` state: a transaction capability.
  - Advisory locks: `advisoryExpression` over the `lock` package's advisory
    manager: a lock capability.
  - Sequence calls: `sequenceCallExpression`, with its prepared-literal and
    DEFAULT rules from `AGENTS.md`; the most constrained part, last.
  - `blockingInquiryExpression` (`pg_blocking_pids` and friends) goes with
    the advisory part (decided at the later-wave refresh: it reads the lock
    manager's state, like advisory locks).
  - `identityExpression` (`session_identity.go`: `current_user`,
    `session_user`, `current_schema` and friends) was not listed; it reads
    the statement identity snapshot and goes with the catalog inquiry part
    (decided at the later-wave refresh).

Tier C ACL record, measured at its start on `5cc6fe6e5` and rebased onto
`6f71c048e` (F2 session 4 and the access-names cleanup):

- Scope. Three engine nodes: `aclValueExpression` (`acldefault`,
  `makeaclitem`, `aclexplode` and the `aclitem[] @> aclitem` operator),
  `objectACLExpression` (`pg_get_acl`) and `accessInquiryExpression`
  (`pg_has_role`, `row_security_active`), in `acl_values.go`,
  `acl_explode.go`, `object_acl.go` and `access_inquiry.go` (about 1,090
  lines). `has_*_privilege` exists only as catalog routine identities; no
  node binds it, so nothing ported.
- Seam. `functions.ACLRuntime`: `ACLAuthority` (also `ACLAuthoritySource`),
  `ObjectACL`, `RoleMembership` and `RowSecurityActive`. The engine
  implementation is `acl_runtime.go`. The aclitem algorithms (privilege
  names, default masks, array validation, containment, the explode cursor)
  moved to `functions/acl_items.go` as private helpers; `aclDefaultMasks`
  keeps its own copy of the masks, as the authorization A1 notes asked.
- Port. The binders needed no new `Binder` method, but one coercion intent:
  `pg_get_acl` and the access inquiries finalize each argument and then
  build a bare implicit coercion at the raw argument without a cast routine
  identity; `ExactFormalArgument` would attach one (and so change stored
  dependencies) and returns the planning error instead of the missing
  signature. `BareFormalArgument` covers it. Their shared envelope is
  `inquiry_call.go`.
- Registry. The chain matched all three families case-insensitively before
  the shared gates (`ACLValueFunctionName`, `EqualFold("pg_get_acl")`,
  `AccessInquiryFunctionName`), so they register by `Names` with no fields;
  `pg_get_acl`'s branch preceded the access inquiries, whose predicate also
  names it. No earlier chain predicate claims these names.
- Nodes. `ACLValue`, `ObjectACL` and `AccessInquiry` implement `Children`,
  `Rebuild`, `IntrinsicVolatility`, `PlanningFold` (`FoldNever`, as the old
  constant-inputs case and the default branch decided), `Render` with
  replay, `PreflightFold` (`PreflightChildren`), `SameGroup` and
  `LooksLikeFunction`; `ACLValue` alone is a `StoredProvider`. The display
  forms needed no new `Rendering` field: the always-qualified
  `pg_catalog.acldefault` is a verbatim name with `Replay.Name`, the labels
  are verbatim, and `@>` is a template, which keeps the always-quoted
  `OPERATOR("pg_catalog".@>)` in display and replay.
- Switch cases removed: walk, join legality, retag, remap, conditional
  simplify and constant inputs, volatility, generated admission, `tableoid`
  and stored validation, node tree, deparse, stored scalar SQL, index key
  syntax, grouping identity, grouping rewrite, aggregate walk and preflight.
  Kept (now naming the exported types): function-source switches
  (`string_table_bind.go`, `string_set_query.go`, `string_set_lower.go`),
  nullability (`query_bind.go`, `returning.go`), `view_literals.go`, the
  closed-DEFAULT case (`transaction_time.go`; `ClosureTransparent` cannot
  exclude `aclexplode`) and the stored-default owner guard (`default.go`;
  `Replay.RelationOnly` would change its message).
- Corrections. Three protocol defaults differ from the old cases only on
  paths no consumer distinguishes, and the probe below matched: planned
  volatility of `pg_get_acl` and the access inquiries is now STABLE merged
  with the planned children instead of the analyzed properties (they differ
  only as STABLE against VOLATILE, and every consumer compares with
  IMMUTABLE or VOLATILE through children that stay volatile); conditional
  simplification keeps the analyzed properties instead of recomputing them
  from the simplified children, as for every function node; the `tableoid`
  walk of generated columns now visits their children (they are never
  admitted to generated columns).
- Deviations. The aclitem helpers moved with the nodes rather than in the
  seam commit, so no temporary exports were needed; the F2 session 4 hooks
  landed during the session and were adopted in a fourth commit.
- Byte identity. A throwaway probe (not committed) ran 33 expressions over
  every form (the four kinds, `acl =>`, `OPERATOR(pg_catalog.@>)`, named and
  reordered `pg_get_acl`, the OID/name overloads, int8/int2 implicit
  coercions, both `row_security_active` overloads) through direct and
  prepared results, views (plain and pretty), CHECK constraints and
  `conbin` node trees, expression indexes, STORED and VIRTUAL generated
  columns, defaults, domain CHECKs, GROUP BY, DISTINCT ON and HAVING, and
  native Dump, with `quote_all_identifiers` off and on. Output was identical
  on master and after every commit.

Tests: moved to `functions/acl_items_test.go` and
`acl_items_cancellation_test.go` (adapted to a fake `ACLRuntime`):
`TestACLValuesDefaults`, `TestACLValuesPrivileges`,
`TestACLValuesContainment` (gained foreign-item, foreign-array and
missing-authority cases), `TestACLValuesInvocation` and
`TestACLValuesScanCancellation`. Added `functions/acl_protocol_test.go`
(`TestACLValueEvaluatesThroughRuntime`, `TestObjectACLEvaluatesSlots`,
`TestAccessInquiryEvaluatesRoles`, `TestACLNodesProtocol`,
`TestACLNodesRender`); `TestLookupClaimsRegisteredNames` gained the ACL
claims and near-miss non-claims and failed before registration. Adapted:
`acl_values_test.go` (`TestACLValuesZeroIdentity` checks the foreign
authority through the containment node; `TestACLValuesStoredRecipe`),
`acl_value_routine_authority_test.go`, `object_acl_test.go` (the named
render check reads `Render`) and `access_inquiry_test.go` use the exported
types; `pg_parameter_acl_test.go` keeps its own copy of the counting scan
context. No test was deleted.

Verification: `make quickcheck` passed (48 packages); `make deps` lists only
module paths; `functions` imports no engine state packages. Quick mode and
`REFERENCE=1` passed, on both backends with no failures, for
`^TestReference(ACLValues|ACLItem|ObjectACL|AccessRole|ObjectGrant(AccessInquiry|ACLGetter|ACLValue|TypeRawACL)|ObjectAddressRoutineFacts)`,
`^TestReference(View|Generated|Default|Check|Deparse|SearchPath)` and
`^TestReference.*(Dump|Restore|StoredConsumers)`. Bench skipped: bind and
evaluation paths only, no startup code changed. Racecheck skipped at the
user's request.

Notes for later steps (Tier C ACL):

- The result-nullability protocol cutover now removes those concrete cases from
  `query_bind.go` and `returning.go`. A set-source hook
  remains separate work (the function-source switches, see the F2 session 4 notes).
- A conditional closure hook (closed when every child is, unless the node
  says otherwise) would let `aclexplode` drop its closed-DEFAULT case.
- `reference/types/README.md` lists `TestACLValues*` selectors, some of
  which now run in `functions` and one (`EmptyArray`) names no test; fix it
  in a task that may edit `reference/`.
- `has_*_privilege` is catalog identity only; executing it is a feature,
  not part of the split.

Later-wave refresh, measured at `75fe0d710` while planning steps 8–17
(docs only; no code changed):

The historical inventory below predates the result-nullability protocol cutover.
`query_bind.go` and `returning.go` now ask node-owned hooks rather than enumerate
the catalog/session/blocking inquiry kinds.

- **Tier C nodes still in the engine** (all 14 listed in the refinement,
  plus `identityExpression`). Every one is named by the same 12 central
  switch files: `aggregate_bind.go`, `conditional_plan.go`, `default.go`,
  `deparse_renderer.go`, `deparse_tree.go`, `expression.go`,
  `expression_retag.go`, `index_control.go`, `query_expression_walk.go`,
  `query_join_legality.go`, `view_literals.go`, `view_remap.go`. Extra
  files per part:
  - catalog inquiry: `query_bind.go` and `returning.go` (nullability, for
    `namespaceInquiryExpression`, `currentSchemasExpression`,
    `indexPropertyExpression`, `deparseExpression`); `view_write.go`,
    `datetime_overlaps_context.go`, `routine_inquiry.go`,
    `serial_sequence.go` (`deparseExpression.evalSerialSequence`);
    `expression_volatility.go`, `generated.go`, `scalar_function_sql.go`,
    `string_set_query.go`, `string_table_bind.go` and `transaction_time.go`
    for `catalogUserNameExpression`, `catalogForeignKeysExpression` and
    `objectAddressExpression`;
  - transaction identity and commit timestamps: the same six files as the
    catalog relations nodes (`expression_volatility.go`, `generated.go`,
    `scalar_function_sql.go`, `string_set_query.go`, `string_table_bind.go`,
    `transaction_time.go`);
  - advisory locks: the core 12 only; `blockingInquiryExpression` adds
    `query_bind.go` and `returning.go`;
  - sequence calls: `default_replay.go`, `expression_volatility.go`,
    `insert_binding.go`, `serial.go`, `view_write_insert.go`,
    `pg_depend_expression.go`.
- **What each `Eval` reads** (appendix tool; the capability each part needs):
  `catalogUserNameExpression` reads `Engine.roles` and the scope's comment
  catalog and roles; `namespaceInquiryExpression` and
  `currentSchemasExpression` read `Engine.mu`, the session token and the
  scope's search-path results and rule/FK authorities; `identityExpression`
  reads the scope's identity snapshot and current-schema result;
  `transactionIdentityExpression` reads `Engine.catalog`, `locks`, `mu`,
  `visibility` and the scope's ticket and TxRef; `commitTimestampExpression`
  reads `Engine.mu` and `visibility`; `advisoryExpression` reads
  `Engine.locks` and `Session.advisory`; `blockingInquiryExpression` reads
  `Engine.mu` and the session; `sequenceCallExpression` reads
  `Engine.catalog`, the session's sequence memories and the transaction
  owner.
- **F2 capture completed:** `captureNormalizedViewLiteralTree` now reads the
  explicit written-operands protocol; SQLJSON query/RETURNING, coercions,
  occurrence maps and installed identities stay engine-owned. The original
  1,481-line file/71-reference measurement is historical, not remaining work.
- **F2 leftovers still present:** `isQuerySet`, `validFunctionSourceCall`, `functionCallArguments`,
  `withFunctionCallArguments` (`string_table_bind.go`, 45 references);
  `sameQuerySetOccurrence` (`string_set_lower.go`); `openFunctionCall` and
  `functionSetCursor` (`string_set_query.go:205` and `:88`; the cursor has 97
  uses in 3 files). The engine's `seriesExpression` (`series_expression.go`,
  a retained target marker) and `unnestExpression` (`range_more.go`) are set
  sources handled by the same switches; the function-sources item covers
  them.
- **F3 aliases.** `routine_declaration.go` keeps `type routineProvider =
  functions.RoutineProvider` and 23 constant aliases, "temporary aliases for
  the provider vocabulary that moved to functions in F3". They have about
  1,290 uses in production code. Remove them with the 5f/6c alias tool
  before step 14b (`routine`) moves the builtin inventories that use them.
- **Effect on later steps.** Step 10 (`plan`) excludes the Tier C nodes from
  its move set; each Tier C part that lands removes their cases from the
  central switches before those switches move.

The PostgreSQL 18.6 oracle drift listed in these records (UUIDv7 shifts,
`json_strip_nulls`, EXTRACT quoting, final sigma, Hangul TBASE, lock rechecks
of function scans, the 99-argument aggregate limit, levenshtein overflow and
the two `CheckReview` provider-wait tests) was fixed on `fix/pg186-drift`
(`89606a6d5` through `0af65c1f6`). The `to_date` TM collation failure is an
upstream regression recorded as a release difference; see
[POSTGRES_REGRESSION.md](POSTGRES_REGRESSION.md).
