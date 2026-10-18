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

State after 2c: 35 function nodes and the four installed-routine wrappers
implement `expr.Composite`; the walk switch keeps explicit cases for the rest.
Still to design here:

- the render hooks for deparse and node-tree output (deferred from 2c);
- protocol membership for nodes whose `Reconstruct` takes engine-derived
  inputs: `Math`, `String`, `ArrayTransform`, `ArraySearch`, `Posix`,
  `Similar`, `Regexp`/`RegexpSet` (`RegexpCall`), `UUIDGeneration`,
  `DatetimeSleep` and the retained-descriptor datetime nodes (`Justify`,
  `Extract`, `Overlaps`, `Bin`, `Trunc`, `ZoneCalendar`, `AgeFinite`), plus
  `Timezone`, `Format`, `NullCount` and `Random`, which have no `Reconstruct`;
- removing `Arguments()` where it duplicates `Children()`, once no switch
  calls it.

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
- Tier C (catalog, ACL, xid, advisory-lock and sequence inquiry) waits for
  steps 4, 5 and 7. Refine it then: narrow capability interfaces in
  `functions`, or homes in the owning subsystem.

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
- [ ] F2 — Switches converted: [ ] retag [ ] remap [ ] volatility
  [ ] deparse [ ] node tree [ ] generated admission [ ] conditional plan
  [ ] join legality
- [x] F3 — `Binder`, `Definition`/`Lookup` and engine dispatch; pilot family:
  UUID extraction (`60dc1cedd` seam, `cd4a88387` port, `5d59e5737` move,
  docs `9bca9b2b6`)
- [ ] F4/F5 Tier A: [x] math (`3078b889f` seam, `d5dc34c59` port,
  `0f3c7e5a2` move, docs `96e11f662`) [x] string [x] LIKE/SIMILAR/regex
  [x] datetime (datetime: `5c5928497` seam, `5a293c0b3` port, `4acb5e42b`
  move; pattern: `ba236a2b8` seam, `1807e7ee9` port, `34f7300d1` move;
  string: `69133565c` seam, `7c28e5c79` port, `76d0201d9` move,
  `db9694a8f` string_to_table and variadic bookkeeping; then the docs
  commit) [ ] JSON/JSONB/SQL-JSON
  (function calls done: `059a86924` seam, `94b71bb40`
  port, `a6c399c2b` move, `b1099500b` slot binder, then the docs commit;
  operators, SQL/JSON constructors and `IS JSON` remain) [x] arrays [x] ranges (`a46ee7c5c` seam,
  `48d987da7`/`62f610cb1` arrays, `050805a1e`/`a5be023f9` ranges, then the
  docs commit) [x] UUID [x] format
  [ ] binary [ ] numeric formatting [ ] pgcrypto [ ] fuzzystrmatch
  [ ] unaccent [ ] intarray [ ] intagg [x] uuid-ossp (UUID, format and
  uuid-ossp: `e29efd342` seam, `6bcd3fb43` port, `0a04835ae` UUID move,
  `36411c4fd` format move, `0a80f30a0` uuid-ossp move, then the docs commit)
- [ ] F4/F5 Tier B: [ ] random [ ] current time and clocks [ ] sleep
  [ ] settings reads
- [ ] F4/F5 aggregates and windows
- [ ] Tier C refined (after steps 4, 5 and 7)

`Binder` methods added beyond the F3 set: `AnalyzeFilter` (F3, for the
pilot's shared call envelope; FILTER analysis is used by 27 glue files, so it
is a genuine analyzer capability); `DefaultConsumer`, `ArrayType`,
`CommonUsage` and `ComparisonInfo` (math); `CollatedInfo`,
`RetainUntypedParameter`, `NoteCatalogText` and `InstalledRoutine` (UUID
generation, format, uuid-ossp); `BuiltinOperatorRoutineInfo` and
`CaptureComparisonInput` (arrays and ranges); `Literal`, `Context`,
`AdmitSetReturning` and `CaptureTypeInput` (JSON calls); `ConstantValue`
(datetime), `BuiltinOperatorVisible` and `CallSignatureError` (pattern) and
`FinishBinary` (string, a leak the binary port removes). Reasons for every
method are in the `functions` README.

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

- `Lookup` now holds about 140 names plus the JSON predicate.
- The binary port removes `FinishBinary`.
- Still engine-dispatched over exported entry points: `age`/`isfinite`
  (Tier C), `overlaps` (an F2 inline-safety hook), `regexp_*` (placement
  callback and set wrapping; `AdmitSetReturning` does not cover the regexp
  placement rules), the variadic string family (polymorphic body capture,
  query frame) and `similar_to_escape` (needs a syntax-keyed definition).
- LIKE's binder and node move together once F2 has converted the switches
  that name `likeExpression`.

Notes for later steps (JSON):

- `Lookup` now evaluates the JSON `Claims` predicate (about 40 name
  comparisons) for every function call before the chain. Re-measure bind cost
  when more families register.
- The JSON operator binders can reuse `BuiltinOperatorRoutineInfo` (arrays
  and ranges) but still need the engine's operator diagnostics
  (`arrayMembershipError`, `comparisonError`) before they can port.

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
