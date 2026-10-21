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
- **Second round (planned at `c154e14fb`):** F6 (linear item 71) needs
  nothing; run it before `stored` 22b (item 68).

## Goal

`functions` becomes the real home of builtin functions: plain nodes, their own
node operations, bind code that depends on a `functions.Binder` interface, and
a registry instead of the name if-chain. It stays a leaf library: it imports
`expr`, `ids`, `sqltype`, `parser` and `sqlstate`, never `catalog`, `storage`,
`lock`, `visibility` or the root engine.

Transaction identity/age/member and commit-timestamp function ownership now
follows this boundary through `TransactionInquiryRuntime`; canonical allocation,
history, tickets, scope/fences, locks and publication remain in the engine.

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
for fixed receiver storage. Function sources now use explicit source capabilities
and provider-local cursors. SQLJSON now supplies its display/replay template
through `Rendering`; its query/coercion/catalog/capture authority remains in the
engine. Its private renderer matrix, full-byte public smoke, selected pinned18.6
dual-mode proof and quickcheck passed. Remaining checks were waived by the user.

The F2 function-source conversion is complete, including the engine's
`seriesExpression`, `subscriptsExpression` and `unnestExpression` markers.
SQLJSON rendering is also complete. The conditional closed-DEFAULT hook is
implemented with passing selected proof and quickcheck; racecheck/finalcheck
are waived by the user. See the current adoption note below and the
"Later-wave refresh" evidence at the end of Status.

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

The transaction inquiry port uses the shipped Binder without expansion, then
moves private owners and registers both families before shared gates using the
existing parser predicates. The datetime registry owns age/isfinite; XID age
constructs its node from the already analyzed/coerced operand, without the old
engine builder callback. Item8's source/unit cutover and proof status are below.

### F6 — Close step 3 (linear item 71)

Added when the postponed packages were planned (measured at `c154e14fb`).
Step 3 is done when central switches list no function nodes and the Tier A
and Tier B glue files have left the engine. One switch and some glue
remain:

1. **The aggregate provider remap.** `expression_remap.go` (was
   `view_remap.go`; about lines 705–840) remaps stored aggregate calls with
   a type switch over about 29 provider types, from `functions.IntegralSum`
   to `functions.RangeAggregateProvider`.
   Give the aggregate providers a remap hook in `functions` (for example a
   method taking a small `functions.ProviderRemap` value with the usage and
   type mappers and the comparison binders the cases call today) and
   replace the switch with one call. Keep the error texts and the order of
   the binder calls. Do this before 22b moves `expression_remap.go` into
   `stored` (linear item 68 needs this item).
2. **Switches over constants stay.** The other `case functions.` matches
   in the root and in `analyze` switch over operation constants or
   argument intents (`comment_inquiry.go`, `session_identity.go`,
   `scalar_renderer.go`, `view_deparse_composite.go`,
   `analyze/expression.go` and others). They are binder vocabulary, not node operations. List them in
   the Status record, one line per file.
3. **Glue that stays, and why.** The `statementHost` capability bodies
   (`advisory_runtime.go`, `blocking_runtime.go`, `acl_runtime.go`,
   `catalog_inquiry_runtime.go`, `session_inquiry_runtime.go`,
   `transaction_inquiry_runtime.go`, `setting_calls.go`,
   `statement_host_function.go`) implement `functions` capabilities over
   engine state: they stay in the root by design. The installed-node
   validators (`validateInstalled*`) and the function deparse helpers
   (`sqljson_deparse.go`, `subscripts_deparse.go`,
   `datetime_overlaps_render.go`, `datetime_zone_deparse.go`,
   `intagg_presentation.go`) move with `stored` (step 22). Say so in the
   README's "What stays in the engine".
4. Tick F2 and step 3 here and in `ENGINE-SPLIT-STATUS.md`.

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
- [x] F2 — Switches converted (closed by F6): [x] retag [x] remap [x] volatility
  [x] deparse [x] node tree [x] generated admission [x] conditional plan
  [x] join legality [x] stored scalar SQL (session 1: `c6de24e8f` members,
  `144458647` join, `5d7935b7d` volatility, `4c54eaf54` conditional,
  `1fa051e96` generated, `1e6bd8aec` retag, `c703fd8d9` remap, `09fc24b0a`
  math helpers, docs `ec2fba8bd`; session 2: `68cbeefed` node tree,
  `42ec6b7e5` deparse, then the docs commit; session 3: `2e7673441`
  replay protocol, `cca30a78c` stored scalar SQL, then the docs commit)
  [x] grouping identity [x] grouping rewrite and aggregate occurrence
  [x] preflight [x] index key display [x] closed DEFAULT checks
  [x] `Arguments()` [x] view literal capture [x] function sources
  (with series/subscripts/unnest; remaining checks waived) [x] `SQLJSON` rendering
  (selected dual-mode/quickcheck passed; remaining checks, including full
  racecheck/finalcheck, waived by user)
  [x] result nullability hook (racecheck/finalcheck waived at user request)
  [x] conditional closure hook (selected proof and quickcheck passed;
  racecheck/finalcheck waived by user; session 4: `063fe2d1e` identity, `622b3d3ba`
  rewrite, `a187f7f5d` preflight, `39c887998` index, `b2675f4c2`
  substitution, `632814b91` DEFAULT closure, `25fcad768` `Arguments()`,
  then the docs commit)
- [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot family:
  UUID extraction (`60dc1cedd` seam, `cd4a88387` port, `5d59e5737` move,
  docs `9bca9b2b6`)
- [x] F3 aliases removed — engine-local provider type/23 constants and both
  deferred math diagnostic aliases removed. Latest-base semantic/public smoke
  proof passed in tests-12; tests-10/tests-11 build, selected native/real,
  quickcheck, full racecheck and dependency/import passes describe the preceding
  base. Complete-candidate gates refresh before review; startup qualifications
  are recorded below.
  Post-integration finalcheck explicitly waived by the user, not run or passed.
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
- [x] Tier C refined (after steps 4, 5 and 7; plan in "Tier C refinement"
  below): [x] ACL SQL function nodes (`b3ce43139` seam, `574de18f1` port,
  `436322070` move, `75d906ea8` protocol hooks, then the docs commit)
  [x] catalog inquiry (with session identity; verified ownership cutover,
  record below) [x] transaction identity and commit timestamps
  [x] advisory locks (with blocking inquiry; integrated as `a85e7d747`)
  [x] sequence calls (Group A Item 10, record below)
- [x] F6 — Step 3 closed: aggregate provider remap hook, remaining glue
  recorded, F2 and step 3 ticked (linear item 71; `c3ba6773d` seam,
  `95f4feb13` engine cutover, then the docs commit; record in "F6 — step 3
  closed" below)

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

F3 alias ownership: engine callers now use the existing
`functions.RoutineProvider`, 23 family constants, `ResolutionError` and
`SignatureHint` directly. Latest-base root semantic proof (tests-12) establishes
1291 exact-owner uses: 4 type, 1283 constant, 2 pure-builder and 2 Hint uses;
26 declarations removed across 28 Go files (17 production, 11 private units).
The reload conserved 1,065,774 non-target bindings with zero loaded type errors.
Preceding-base tests-10 proved1293 uses/30Go/1,066,520 other bindings; original
evidence proved1306/39Go/1,075,900. Those counts remain historical: upstream
catalog/session, transaction/commit-timestamp and advisory/blocking extraction
now owns the moved binders in `functions`; engine implementations stay removed.
The distinct `expressionBinder.functionResolutionErrorForTypes` method retains
its eight current callers, missing-only signature notes and composite fallback.
`functions.RoutineProvider = expr.RoutineProvider` remains the intentional
catalog/expr layering seam, not a temporary engine alias.

Item6's implementation is verified; no compatibility shim,
new registry, runtime work, inventory extraction or public reference-body change
was introduced. Unrelated step 2b aliases in `functions/expr_aliases.go` remain
listed in `ENGINE-SPLIT-expr.md` for 2d. The datetime, pattern and string ports
added no aliases: callers of the moved `operatorExpressionError` (now
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
- Still engine-dispatched over exported entry points: `overlaps` (an F2
  inline-safety hook) and `similar_to_escape` (needs a syntax-keyed definition).
  age/isfinite now register with datetime; XID age no longer takes an engine
  node-builder callback. `regexp_*`, the variadic string family and `to_number`
  registered in the follow-up below.
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
  installed-wrapper and engine-node cases precede its own protocol branch;
  `scalar_rendering.go` interprets the `Rendering`. SQLJSON initially retained
  an explicit case, removed by the later SQLJSON rendering cutover below.
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
  old "unsupported stored default node". SQLJSON initially kept its explicit
  case; the later SQLJSON rendering cutover removes that exception.
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
  `JSON_ARRAY(SELECT ...)`. SQLJSON initially retained explicit deparse and
  stored-SQL cases because formatted-input source selection and RETURNING
  remained engine-owned. The later SQLJSON rendering cutover supplies these
  through bounded protocol facts while keeping both authorities in the engine.
  Formatted input reuses the `JSONFormattedArgument` intent; `SQLJSONInput`
  also replaces the aggregates'
  `formatJSONInput`, which was the `format != nil` case of the same code.
  `JSON_OBJECT` keys keep an untyped parameter unfinalized only when it is a
  parameter, so `RetainUntypedParameter` now returns whether it retained one
  (the argument kind alone cannot tell a parameter from other untyped nodes).
- Switch cases removed (now protocol branches): walk, join legality,
  volatility, conditional simplify and constant inputs, generated admission
  (three), retag, remap, node tree and the IS JSON deparse case. The old
  conditional, retag and remap cases matched the protocol defaults: fold as
  a constant only when immutable with literal children, never strict, rebuild
  on change. Later F2 switches adopted the remaining protocols. The SQLJSON
  rendering cutover removes the concrete deparse/stored-SQL/classifier
  exceptions, not engine-owned query, coercion or capture boundaries.
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

- F2 SQLJSON rendering now shares `functions.SQLJSONPieces` between the bound
  protocol and raw view syntax. `sqljson_deparse.go` retains only engine-owned
  protocol/coercion boundary helpers and `jsonPredicateRecipe`, which reuses
  `functions.JSONPredicateSuffix`; duplicate bound constructor renderers are gone.
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
  Function sources subsequently adopted the explicit source capabilities and
  by-value cursor documented in `internal/engine/expr/README.md`.
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
- Function sources are now converted: explicit admission/cardinality, OUT names,
  occurrence identity and invocation opening use the expression protocol.
  Primitive opening remains allocation-free; complex providers add one native
  pointer-state allocation per invocation, without per-cell adapters or a union.
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
  per family. Bind code uses Binder; registry entries preserve each original
  predicate and shared-gate position, not a blanket before-gates rule.
- **What stays in the engine** for every part: capability implementations,
  live authority, set-returning admission, function scan scheduling/layout,
  actual descriptor consumers, raw/bound view literal pairing, closed-DEFAULT
  admission and statement input/output adapters such as aclitem and regrole
  resolvers/formatters. Node-local source opening/cursor, ResultNullability,
  capture and closure hooks now exist and move with their owners; callers retain
  explicit engine-wrapper and lexical-boundary precedence rather than concrete
  transaction inquiry cases. The historical no-source-hook and
  central-nullability-table claims are obsolete.
- **Parts**, one session each, can run in parallel: they touch the same
  switch files, `function.go` and these docs, so rebase early.
  - ACL SQL function nodes: done (record below).
  - Catalog inquiry: all ten owners now live in functions as CatalogUserName,
    CatalogForeignKeys, ObjectAddress, NamespaceInquiry, CurrentSchemas,
    VisibilityInquiry, IndexProperty, CommentInquiry, Deparse and Identity.
    CatalogInquiryRuntime/SessionInquiryRuntime borrow statement authority;
    selected dual-mode, full quick/race and actual public replay proof passed.
  - Transaction identity and commit timestamps: private
    `transactionIdentityExpression` and `commitTimestampExpression` now live in
    `functions`, including age(xid), binders and source/protocol operations.
    `TransactionInquiryRuntime` retains engine visibility/lock authority;
    `BindDatetimeAgeFinite` has no engine node-builder callback. Source/unit
    cutover and selected dual-mode, quickcheck, full race and smoke proof passed.
  - Advisory locks: `advisoryExpression` over the `lock` package's advisory
    manager: a lock capability.
  - Sequence calls: done. `functions.SequenceCall` and `BindSequenceCall`
    over `SequenceRuntime`; prepared-literal capture and DEFAULT TEMP rules stay
    behind two Binder methods (record below).
  - `blockingInquiryExpression` (`pg_blocking_pids` and friends) goes with
    the advisory part (decided at the later-wave refresh: it reads the lock
    manager's state, like advisory locks).
  - Session identity is included in catalog inquiry, with distinct keyword and
    ordinary-call binding/capture. Its snapshots, path caches and expression
    authority remain engine-owned.

Item7 catalog/session ownership record:

- All ten binders and immutable node operations are in the leaf, with existing
  registry/shared-gate/SQL-callable precedence. Namespace alone OwnsSpelling;
  keyword identity uses BindIdentity/CaptureIdentity. Binder.NoteCatalogSnapshot
  separates analysis demand from execution NoteCatalogText; deparse known-child
  BareFormalArgument honors PreserveProperties without cast-routine provenance.
- Generic protocols replace selected concrete visitor branches. SnapshotDemander
  preserves caller-specific demand and unconditional stored deparse manifests;
  CallCoster keeps 10/100 policy; grouping-only IndexProperty/Deparse properties
  merge STABLE and rewritten children without forced stage. Seven owners retain
  analyzed volatility, while username/linkage/address merge physical children.
  SELECT-only deparse/index nullability and current_schema-only identity claims
  are unchanged.
- Source companions and the single attributed 219-link inventory move with
  username/linkage/address. Payloads are borrowed for equivalent substitution,
  rebuilt for descriptor retag, with private cursors and typed NULL versus EOF.
  Engine keeps scheduling, admission, layout, ordinality and catalog authority.
- Runtime capabilities take typed values/IDs/descriptors, never engine structures.
  Locks, authorization, role/catalog/comment/path snapshots, pending TEMP
  staging/publication and FK/rule/view authority remain engine-owned, as do
  address resolution, index interpretation, lazy comment-body admission, actual
  deparse/tree engines and serial ownership lookup. All seven visibility kinds
  (including three collation-management kinds) and twelve deparse signatures
  (including pg_get_serial_sequence) are included. No sequence execution or
  extension-update-path provider moves.
- Pure inventory/cursor, AM-name, malformed-node, metadata and property units
  move; actual analyzer/authority/stored/serial integrations remain engine-owned.
  The obsolete object-address private sql helper callback-to-NULL assertion was
  removed, not replaced with mock echo. Actual public StoredBoundaries/StoredDemand
  and real scalar renderer/roundtrip tests retain behavior coverage.
- Root observed production build, focused linkage/capture units and strengthened
  public direct/SAME-held role/path, typed NULL/error recovery, full retained
  serial metadata, six-field linkage and same/fresh-driver replay smoke passing.
  All35 bounded public batches passed quick mode and pinned18.6 mode; focused
  private/race selections, full quickcheck, module-only deps/import layering and
  full GOMAXPROCS=8 racecheck passed. Default-CPU racecheck hit two30s fixture
  deadlines under contention; the unchanged suite passed with bounded CPU/package
  concurrency, without test/deadline/coverage changes. Earlier compile failures
  and the failed race run remain in the part's verification evidence.
  Five default-CPU after startup samples measured920030–1165897ns/op, with5152
  steady allocations (first cold sample5161); three exceeded1ms. Baseline samples
  were1008174–1703888ns/op with5152 steady allocations (first5160). Shared-machine
  timings do not establish a speedup; no startup-allocation regression observed.
  Independent review, integration and post-integration finalcheck are separate
  lifecycle gates, not covered by neighboring waivers.


Current advisory/blocking cutover implementation (quickcheck-only verification):
the real `Advisory` and `BlockingInquiry` nodes, Binder-based family binding,
static registry entries and immutable protocols now live in `functions`.
The actual edit sequence introduced value capabilities and engine adapters,
switched the original engine nodes to those seams, ported binding against the
existing Binder while still in place, then moved the families and removed
their concrete central-switch cases. Advisory retains shared gates with label
ownership; blocking retains its early all-child analysis before shared gates.
Engine adapters retain canonical lock/SSI authority, live scope/ticket choice,
notices and the ordinary-read versus safe-snapshot mutex boundary. No other
Tier C family, provider-alias cleanup or SQL admission surface is completed.
The later-wave concrete consumer inventory below is historical for these two
families; blocking nullability was already a node hook before this move.

Private node demand/protocol units move with the implementation; integrated
analyzer, CHECK context, lock ownership, replacement and SSI units remain at
their seams. Public reference/smoke goldens are unchanged. See the functions
README Testing section and living part plan for selected native/real/race,
outside-tests smoke and unchanged actual-node allocation comparison commands.
Root observed all 13 bounded cohorts passing in native and pinned PostgreSQL
18.6 modes, selected race/private units and the complete public smoke passing,
and full quickcheck/build/layering/dependencies passing. Eval allocations stayed
0/3/1/1. Startup missed <1ms before (1.047–1.284ms/op) and after
(3.639–16.814ms/op under substantial host CPU pressure).

Historical full racecheck attempts exceeded the 900-second deadline or failed
unchanged scenario contexts; none is reclassified as passing. The user
subsequently waived racecheck and the race-based finalcheck, requesting only
quickcheck. Fresh synchronized quickcheck/public smoke, independent review and
safe integration remain required before completing Item 9.


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
  `Replay.RelationOnly` would change its message). This records that session's
  retained cases: later capture work removed its view-literal case, and the
  conditional-closure adoption below replaces its concrete DEFAULT case.
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
- The conditional closure hook proposed here is implemented by the current
  adoption below: scalar ACL kinds require closed children and `aclexplode`
  refuses. Selected post-adoption proof and quickcheck passed; full racecheck
  timed out and racecheck/finalcheck are explicitly waived by the user.
- `reference/types/README.md` lists `TestACLValues*` selectors, some of
  which now run in `functions` and one (`EmptyArray`) names no test; fix it
  in a task that may edit `reference/`.
- `has_*_privilege` is catalog identity only; executing it is a feature,
  not part of the split.

Later-wave refresh, measured at `75fe0d710` while planning steps 8–17
(docs only; no code changed):

The inventory below is historical, before result-nullability/source protocols
and Item7/Item8 ownership implementation. `query_bind.go` and `returning.go`
consume node-owned hooks rather than enumerate catalog/session/blocking inquiry
kinds. The ten catalog/session nodes and transaction identity/timestamp nodes,
including their source hooks, now live in functions; their central concrete
cases are removed. Advisory/blocking and sequence calls have since moved too
(records above and below), so no Tier C engine-node move remains.

- **Historical central caller inventory** (not remaining Item7/Item8 concrete cases):
  the same 12 central
  switch files: `aggregate_bind.go`, `conditional_plan.go`, `default.go`,
  `deparse_renderer.go`, `deparse_tree.go`, `expression.go`,
  `expression_retag.go`, `index_control.go`, `query_expression_walk.go`,
  `query_join_legality.go`, `view_literals.go`, `view_remap.go`. Extra
  files per part:
  - catalog inquiry callers now use protocols: view_write.go,
    datetime_overlaps_context.go, routine_inquiry.go, serial_sequence.go,
    expression_volatility.go, generated.go, scalar_function_sql.go,
    string_set_query.go, string_table_bind.go and transaction_time.go.
    Actual catalog/session runtime operations stay in engine; node recipes,
    source hooks and site-specific nullability stay beside functions owners;
  - transaction identity and commit timestamps: superseded by Item8 below;
    these nodes use generic consumers and explicit source capabilities, not
    the historical concrete-switch inventory;
  - advisory locks: the core 12 only; `blockingInquiryExpression` adds
    `query_bind.go` and `returning.go`;
  - sequence calls: `default_replay.go`, `expression_volatility.go`,
    `insert_binding.go`, `serial.go`, `view_write_insert.go`,
    `expression_references.go`.
- **State authority retained by runtime capabilities** (historical Eval reads):
  username borrows captured roles/catalog; namespace/current_schemas borrow
  physical session token, paths and rule/FK authority; Identity borrows statement
  identity/current-schema authority. These are engine implementations of the
  functions capabilities, not engine function nodes.
  Transaction inquiry nodes now read `TransactionInquiryRuntime`; its engine
  adapter reads catalog/visibility under `Engine.mu`, or the manager with the
  actual live scope ticket outside that mutex. Timestamp/last reads snapshot
  canonical tracking/history together; `advisoryExpression` reads
  `Engine.locks` and `Session.advisory`; `blockingInquiryExpression` reads
  `Engine.mu` and the session; `sequenceCallExpression` reads
  `Engine.catalog`, the session's sequence memories and the transaction
  owner.

- **F2 capture completed:** `captureNormalizedViewLiteralTree` now reads the
  explicit written-operands protocol; SQLJSON query/RETURNING, coercions,
  occurrence maps and installed identities stay engine-owned. The original
  1,481-line file/71-reference measurement is historical, not remaining work.
- **F2 function-source cutover completed:** typed admission,
  cardinality, OUT names, occurrence comparison and opening now use the small
  `expr` source capabilities. Provider-local named callbacks own advancement
  and release through the safe by-value `expr.SourceCursor`; the old central
  cursor tags/union and argument inventories are removed. Series, subscripts
  and unnest participate in Composite/Substitutable and marker visitor hooks.
  Equivalent lexical rewrites retain selected providers. Engine scheduling,
  layout, Tier C state and installed-intagg identity remain engine-owned.
  Protocol/lifetime contracts and selected proof commands live in the `expr`,
  `functions` and engine READMEs. Quickcheck, 51 bounded dual-mode selections,
  selected lifecycle races and genuine same/fresh-driver replay smoke passed.
  Native cancellation synchronization follows `SourceCursor.Next`; public
  expected outcomes are unchanged. Further tests except quickcheck, including
  full racecheck and post-integration finalcheck, were waived by the user.
  The latest quickcheck retry failed the unchanged LIKE cancellation observer
  before cancellation was issued; the user explicitly waived that failure for
  integration. Earlier post-rebase quickcheck passed; the failed retry remains
  recorded rather than represented as passing.
- **F2 SQLJSON rendering completed:**
  `SQLJSON.Render` supplies a template with unchanged Tree/Inputs. The small
  capabilities are `RenderArg.JSONFormattedSource`, `Rendering.JSONConstructor`
  and `JSONReturning`, and an optional `Piece.Returning` prefix/type/suffix slot.
  Both generic interpreters consume it; ordinary calls, including the quoted
  legacy `"json_object"`, cannot acquire constructor identity by spelling.
  Only `IsJSONFormattedInput()` unwraps one source and only `IsJSONReturning()`
  supplies RETURNING usage. ARRAY/OBJECT/SERIALIZE accept the slot; JSON/SCALAR
  never do. `HideImplicit:true` preserves old `pgRenderer.sql` behavior, which
  cleared the inherited flag before rendering constructor operands; `Listed`
  preserves pretty-mode nested cast/COLLATE position.
  Query construction, conversions/Rebind validation, catalog type identity,
  typmods/domains, typed-TEXT transport, quoting and raw occurrence/capture maps
  remain engine-owned. No Eval path changed.
  Added `TestSQLJSONRendererBoundaries` against real constructors/coercions,
  published domain and volatile operand. Adapted `TestSQLJSONProtocol` only by
  deleting its obsolete FormNone conjunct; tree/children/rebuild/volatility
  assertions remain. No tests moved or were deleted, and reference assertions
  are unchanged. The private matrix passed before and after cutover; the
  identical public database/sql probe passed with full stdout byte equality
  for actual definitions/Dump plus independent direct/prepared rows/types,
  retained cursor metadata, Restore/redump and restored-default insertion.
  Pre-cutover test corrections (Integer/VarChar APIs, published domain authority
  and visibility, keyword quoting, typed-NULL array transport and bare bound
  BYTEA versus genuine raw-view cast) are fixture fixes, not production
  compatibility changes. Selected pinned18.6 dual-mode, build, quickcheck,
  selected race and deps gates passed. Three whole-project race attempts hit
  the required 900-second deadline without reporting an assertion failure;
  a fourth was cancelled when the user requested only quickcheck. All remaining
  non-quickcheck gates, including post-integration finalcheck, were explicitly
  waived. No standalone benchmark was needed for this rendering-only change.
  An earlier documentation-only quickcheck rerun failed in the unchanged
  `TestReferenceRegexpActualCancellation` provider-observation witness. The user
  explicitly waived that specific failure for integration; no clean final
  quickcheck is claimed and no regexp test or behavior was changed.
- **F3 aliases — verified implementation.** Latest-base root tests-12 established
  1291 exact-owner uses, 26 absent engine-local declarations, 28 qualification-only
  Go source changes (17 production, 11 private units), 1,065,774 conserved
  non-target bindings, zero loaded type errors, the retained diagnostic
  method/eight callers and intentional functions/expr alias.
  Preceding-base tests-10 proved1293/30Go/1,066,520 other bindings; original-base
  evidence proved1306/39Go/ten callers. These remain historical, not census
  constraints after upstream extraction. No permanent tool/count test,
  compatibility shim or runtime computation was added.
  Actual public database/sql smoke passed again in tests-12, including reused
  prepared metadata/results, exact diagnostics, recovery, stored view/default
  and native Dump/Reset/Restore. Build passed on the preceding base in tests-10.
  On that preceding base, tests-11 passed all three private batches and all ten
  selected public provider/authority/metadata/stored groups in native and pinned
  PostgreSQL18.6 modes, full quickcheck (147.74s), full racecheck (595.62s),
  dependencies and the downward import graph. Latest complete-candidate gates
  refresh before review; these earlier passes are not latest-base runtime proof.
  Public reference bodies and expectations remain unchanged.
  Latest-base tests-13 subsequently passed build, all U1–U3/P0–P9 native/real
  commands, public smoke, dependencies/import graph, full quickcheck (90.38s)
  and full racecheck (410.44s). Review1 nevertheless found acceptance-C proof
  incomplete: P2's network operators use `OperatorRoutineProvider`, not
  `NetworkRoutineProvider`. The added P10 selection,
  `TestReferenceINETFormatMetadataAndBinds|TestReferenceINETFormatDiagnostics`,
  passed in native and real modes in root tests-14, closing that evidence gap.
  See functions README Testing for argv and independent expectations.
  No production defect or changed reference expectation was found; fresh
  complete-candidate quickcheck/smoke and independent review remain required.
  Tests-13 startup samples were1.026520/0.870124/0.867502/0.845360/0.903488ms,
  with5159 then5152allocs/op: first above1ms, remaining four below; this is
  neither an all-five target pass nor a clean causal comparison.
  Five preceding-base startup samples were1.227197/1.100162/1.016517/0.868481/0.892106ms:
  the first three exceeded1ms, the last two did not. Historical baseline samples
  were0.833–0.867ms on the older base. Upstream extraction and shared-host
  contention prevent a clean causal comparison; do not claim a speedup or a
  harmless regression. Exact bytes/allocations and prior failed runs are retained
  in functions README Testing and the root reports.
  Item6 alone is ticked; this does not complete Step3, Tier C or routine14a/b.
  Step14b still needs its other prerequisites. Independent review/integration
  remain separate gates. User explicitly waived post-integration finalcheck;
  it was not run or passed.
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

### Conditional closed-DEFAULT adoption

Group A Item 5 adds `expr.ConditionalClosure.ClosedDefault` beside the retained
`ClosureTransparent` marker. `ACLValue` owns scalar all-written-child checking
and unconditional set-kind refusal; it inspects its inline argument storage,
without calling `Children`, Eval, Rebuild or privilege validation, and retains
no callback or mutable state. Transaction-time closure dispatches the hook after
dependency guards and explicit leaf/engine-wrapper cases, before transparency
and stable fallback. No new SQL/default capability is introduced.

The measured consumer count is three per predicate: raw DEFAULT admission in
`default.go`, stored scalar rendering in that file, and the fallback in
`sql_routine_default.go`. Those consumers are unchanged. UUID closure does not
adopt this hook: ACL uses its old root-volatility fallback, selected UUID shift
arguments still require at-most-stable closure, and the UUID-only calendar
traversal remains distinct from the time policy. SQL-routine fallback still
requires a genuine SQL routine before granting its separate default allowance.

Six real-provider package-function characterizations and the unchanged selected
public ACL, UUID consumer/feature-smoke and datetime/sleep definitions passed
before adoption (root `tests-2.md`); the corrected direct/held/default-bypass and
actual native replay smoke passed before adoption (`tests-1.md`). Evidence is in
`.cswd/parts/PG18-ENGINE-SPLIT-FUNCTIONS-CONDITIONAL-CLOSURE/`.
Post-adoption build, focused internal selections, selected public dual-mode
proof, actual external smoke and full quickcheck passed (`tests-3.md`);
`make deps` passed with module/stdlib-only production dependencies (`tests-4.md`).
Full racecheck attempts failed by timeout, with an interrupted attempt also
retained (`tests-3.md` through `tests-5.md`). The user's subsequent instruction,
“Please skip all checks except quickcheck”, explicitly waives remaining
racecheck and finalcheck; neither is claimed passing. Only quickcheck is now
authorized for further verification. Selected Item 5 is ticked on this evidence
and waiver; parent F2 and remaining neighboring work stay open. Root's fresh
quickcheck passed (`tests-7.md`), and the independent review completed with PASS
and no blocking findings (`review-1.md`). That review's low stale-handoff prose
finding is corrected here; subsequent rebase/remedy verification is root-owned.
See [functions Testing](../internal/engine/functions/README.md#testing) for the
retained selectors, independent outcomes and exercised smoke argv.
SQLJSON rendering and the engine-owned catalog/xid cases remain separate work;
the adjacent result-nullability protocol is now integrated as described above.

### Transaction inquiries — Group A Item8 source/unit cutover

Both private families own binders, immutable nodes/Eval, validating Rebuild,
provider-preserving Substitute, source opening and applicable render/replay,
grouping, volatility/folding/preflight, capture and conditional-closure hooks in
`functions`. The four former engine owner/source files, callback and concrete
cases in all 17 affected caller/routing files are removed without aliases or
exported concrete-node switches. Existing generic consumers keep engine-wrapper
and lexical-boundary precedence. Members remain nonreflexive source occurrences;
no strict-folding, nullability, group-leaf or transparent-closure hook was added.

`TransactionInquiryRuntime` on `*EvalContext` preserves distinct demand order:
identity context initialization before child errors/NULL and scope; timestamp
strict NULL/xid0/bootstrap/frozen before runtime/tracking. Scalar member misuse
checks scope before invocation-required diagnostics. Canonical domain/epoch
fences, allocator/history, visibility publication and actual manager tickets
remain engine-owned. Member rows reuse the sole manager snapshot clone and are
packed into owned values outside locks; last XID/entry is one locked snapshot.
No Binder expansion, per-evaluation closure, transfer-member buffer, production
fixture API, new dependency or leaf import of engine state was introduced.

No existing whole test definition was deleted. Mixed engine
`TestTransactionIdentityBinding` and `TestCommitTimeExpression` retain real
catalog/provider/dependency, traversal/render/group, remap/retag and lifecycle
assertions; the latter still maps actual column SubID1 to2 and slot0 to3 without
mutating the original. `TestSourceProtocolOccurrence` uses real binding and
retains member nonreflexivity. Canonical allocation/publication/MultiXact and
source-lifetime units stay at their owners. New genuine leaf units are
`TestTransactionInquiryIdentityDemand`, `TestTransactionInquiryTimestampDemand`
and `TestTransactionInquiryImmutableProtocols`; exact commands and assertion
boundaries are in the functions README's Testing section. Public goldens remain
unchanged, including temporal age/isfinite and EXECUTE-authority demand.

Observed after source edits: root-owned `make build` followed by the existing
temporary standalone public-driver Go smoke passed exit0 (combined24.12s).
The smoke exercised direct/same-held identity and member behavior, exact disabled
diagnostics, actual enabled commit timestamps and byte-identical symbolic native
roundtrip (583 observed bytes, not a size expectation). All eight bounded
PostgreSQL18.6/native selections, moved/retained units, focused races, full
quickcheck, build and module-only dependency checks passed. Full racecheck passed
with `GOMAXPROCS=2 GOFLAGS=-p=16` in526.84s after earlier oversubscribed/deadline
failures; coverage and900s deadlines were unchanged. Five startup samples in that
runtime environment measured1.151–1.310ms, above the <1ms target, with steady
5152allocs/op unchanged from baseline; timings are not an isolated performance
comparison. Review and post-integration proof are separate workflow gates, not
claimed by these implementation measurements. No temporary smoke source is a
repository deliverable.

### Sequence calls — Group A Item 10

`SequenceCall` (`nextval`/`currval`/`setval`/`lastval`), its `Eval`,
`BindSequenceCall` and node protocols live in `functions`
(`sequence_call.go`, `sequence_protocol.go`). `parser.CallExpr` keeps its own
grammar route, so the engine dispatch calls `BindSequenceCall` directly; there is
no registry entry and, as before, no shared gates. `SequenceRuntime`
(`SequenceAuthority`, `Sequence(SequenceRequest)`) is implemented on
`*EvalContext` in `sequence_runtime.go`, carrying the former Eval body unchanged
in order. `SequenceAuthority` uses the engine's own evaluation prelude, so an
active domain-check input frame is still installed before arguments run.

Binder gains two reviewed capabilities: `SequenceArgument` (regclass input with
prepared sequence-literal capture; DEFAULT literal-provider rule) and
`SequenceTemporary` (captured TEMP provenance; DEFAULT validation and capture
end). Everything else maps onto existing methods with exact equivalence:
`CoerceArgument` with an empty-`Argument` `DirectFormalArgument` binds int8/bool in
function-argument context; `ConstantValue` (not `LiteralNull`, which looks through
wrappers) detects the strict NULL; the signature diagnostic uses the pure
`ResolutionError`, so it still does not note the signature.

Measured at `4d3568a9d`, 22 production references in 17 files (the historical
inventory above named `view_write_insert.go`, which has none, and omitted the
twelve core switch files). Twelve concrete cases moved to generic branches
(walk, join legality, retag, conditional simplification, preflight, grouping
identity/rewrite/occurrence, index function syntax, node tree, deparse, view
literal capture). Eight catalog-identity consumers keep explicit
`functions.SequenceCall` cases: `default.go` (TEMP DEFAULT validation and stored
SQL with catalog/epoch/dependency checks), `default_replay.go`, `serial.go`,
`expression_references.go`, `view_remap.go` (Ref remap), `expression_volatility.go`
(COPY nextval) and the identity constructor in `insert_binding.go`. Regclass
input and the prepared-literal cache logic stay in
`sequence_expression.go`/`sequence_prepared.go`; the literal records
(`sequence.Constant`, `sequence.Analysis`) moved in step 17b.

Behavior notes. Preflight now passes the caller's `decisiveDefaults` to
sequence-call children (the old case hard-coded false); DEFAULT admits only
`nextval(<regclass literal>)`, and the CHECK probes below were unchanged. The
view-literal arm guards on `expr.Capturable`, so a strictly folded NULL call
keeps its original mismatch diagnostic. `generationHasTableOID` now recurses
into sequence-call children, but only admitted immutable generation plans reach
it, which never contain sequence calls.

Tests: new leaf units `TestSequenceCall(RuntimeDemand|IdentityResult|MissingRuntime|Protocols)`;
`TestCaptureOperandsWrittenSlots` gained two sequence cases. Adapted to the
exported type and fields, with assertions unchanged: `serial_test.go`,
`serial_identity_remap_test.go`, `query_parallel_test.go`,
`table_like_temporary_test.go`, `table_inheritance_test.go`, `table_like_test.go`;
`TestIdentityOwnCheckBindingAuthority` calls `functions.BindSequenceCall`. No test
moved or was deleted; reference goldens are unchanged.

Verification: see the functions README "Sequence call cutover" Testing section.
A throwaway public `database/sql` probe produced byte-identical output on master
and the branch (141 lines). It covered results and errors for all four calls
(including signature, NULL, OID/parameter input, bounds and read-only cases), a
prepared literal across a rename, serial/identity inserts, TEMP providers,
privileges, views, DEFAULTs and CHECKs (with `quote_all_identifiers` off and on),
node trees, dependencies, grouping/CASE/join/ORDER BY shapes and native
Dump→Restore→redump. Racecheck and finalcheck were skipped at the user's request.

Notes for later steps (Item 10):

- Existing bug, unrelated to the move and reproduced on `4d3568a9d`:
  `ALTER TABLE t ADD CONSTRAINT c CHECK (x <> setval('s', 10, (true OR x IS NULL)))`
  panics with a nil descriptor in `captureTypeDiagnosticInput`
  (`enum_diagnostics.go`), via `nullTest` inside a sequence-call argument bound
  for `bindColumnChecks`. Recovery then deadlocks in `prepareTemporaryCommit`.
  It needs reference coverage and a fix in its own task.
- `nextval('s'::text::regclass)` reports 42883 `function nextval(regclass) does
  not exist`; PostgreSQL accepts it. Existing gap; needs reference coverage first.

### F6 — step 3 closed (linear item 71)

Measured at `351822746`; file names as of `1bf7d5e83` (after stored 22a). `functions.RemappableProvider`
(`aggregate_remap.go`) replaces both provider switches in `expression_remap.go` (formerly
`view_remap.go`):
the 24-type aggregate switch and the window switch over `IntegralSum`,
`NumericSum` and `FloatSum`. Window SUM is the only provider a `WindowCall`
carries, so the shared hook leaves window behavior unchanged. Each `Remap`
holds the old case body. Usage and type mapping and binder calls keep their
order, and the first usage (collation) error returns before any binder runs,
as the old `err == nil` guards did. The type check comes before the shared
error check, so "unpersistable view aggregate" keeps its precedence.
"inconsistent remapped range aggregate result" is byte-identical. `Binder`
is unchanged.

The prompt expected the other `case functions.` matches to be constant
switches only. Several are node-type cases. Each one is a deliberate,
already-documented exception, not a node-operation switch left over from F2.

Operation constants and argument intents (binder vocabulary):

- `comment_inquiry.go` — `CommentInquiryKind`.
- `session_identity.go` — `IdentityKind`.
- `view_deparse_composite.go` — `NullCountKind`.
- `analyze/expression.go` — argument intents (`ComparisonArgument` …
  `ExactFormalArgument`).
- `analyze/operator_binding.go` — JSON operator-name predicates.
- `analyze/query_bind.go`, `analyze/query_expression_walk.go` —
  `AggregateOperation` sets.
- `analyze/installed_routine.go` (first switch) — `Extension`.

Node-type cases kept, with the reason:

- `expression_remap.go`, `expression_references.go`, `exec/context_runtimes.go` —
  `SequenceCall` catalog identity: Ref remap, the pg_depend edge, COPY
  nextval volatility ("Sequence calls — Group A Item 10" above).
- `scalar_renderer.go` — stored-default SQL: `SequenceCall` checks, and the guard
  that rejects `ObjectACL`/`AccessInquiry` outside a relation-owned
  definition (README "What stays in the engine").
- `plan/expression_walk.go`, `analyze/aggregate_bind.go` (RewriteGroup,
  AggregateOccurrence) — `Timezone`'s value-first lexical walk. This is the
  known divergence in the F2 notes. Fix all three together with reference
  coverage, outside the split.
- `analyze/installed_routine.go` (Attach) — wraps installed extension nodes
  in their `plan.Installed*` procedure-identity wrappers.
- `analyze/default_closure.go` — closed DEFAULT admission predicates
  (sequence, transaction time, sleep, UUID, datetime).
- `analyze/overlaps.go` — the OVERLAPS inline-cost table.
- `analyze/retained_input.go` — retained diagnostic input for `Enum` and
  `JSONBRecord`.
- `view_write.go` — the writable-view demand flag for non-immutable `Binary`.
- `routine/body.go` — polymorphic SQL-body arguments of fixed builtin OIDs.
- `view_deparse_functions.go` — the column-definition shape of JSON record
  function tables in view SQL.

The glue that stays in the root, and why, is in the README's "What stays in
the engine". The `statementHost` capability bodies stay by design. The
`validateInstalled*` validators and the function deparse helpers move with
`stored` (step 22).

Tests: new leaf units `TestAggregateProviderRemap(MapsEveryDescriptor|ReturnsUsageError|OrderedSetArgument|RangeInconsistentResult)`
(a mutation that skips one mapping fails them). Engine remap units and
reference goldens are unchanged. Proof and selectors: README "Aggregate
provider remap" Testing section. A throwaway public `database/sql` probe ran
on master and the branch and produced byte-identical output (271 lines). It
covered 14 views across every persisted provider family and window SUM, over
domain, enum and collated columns; enum ADD/RENAME VALUE, domain constraint
and collation rename; and native Dump→Restore→redump.

Notes for later steps (F6), existing gaps seen by the probe, identical on
master:

- `max(enum)` reports 42883; PostgreSQL has `max(anyenum)`.
- `ORDER BY` over json or a multirange column fails ("could not identify an
  ordering operator for type json"; "invalid ordering type" with no
  SQLSTATE).
- `ALTER DOMAIN … RENAME TO`, `ALTER DOMAIN … SET DEFAULT` and
  `ALTER TYPE … RENAME TO` are syntax errors.
- After `ALTER COLLATION mycoll RENAME TO mycoll2`, `COLLATE mycoll2` reports
  42704 "collation … for encoding "UTF8" does not exist".

Each needs PostgreSQL 18 reference coverage first, in its own task.
