# reference/query_core

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### External routine language boundary

The six `TestReferenceRoutineExternalLanguages*` scenarios share one body across
native and pinned PostgreSQL18.6 backends. Oracle-first discovery established the
PostgreSQL diagnostic fields and stages before the native failing checkpoint;
the temporary PG-only filter is removed. Run selected proof in both modes after
any affected implementation or test change:

```sh
make check RUN='^(TestReferenceRoutineExternalLanguages(Diagnostics|NativeBoundary|HandlerDeclarations|Atomicity|Preservation|FeatureSmoke)|TestRoutineExternal(ASItems|HandlerSyntax))$'
make check RUN='^(TestReferenceRoutineExternalLanguages(Diagnostics|NativeBoundary|HandlerDeclarations|Atomicity|Preservation|FeatureSmoke)|TestRoutineExternal(ASItems|HandlerSyntax))$' REFERENCE=1
```

| Scenario | Independent expected behavior |
| --- | --- |
| Diagnostics | Missing Tcl/Perl/Python/custom/historical languages `42704`; namespace/CREATE before language and language before unknown types; ordinary C/internal `42501`; invalid body forms `42P13`; full English diagnostics and prepare/execute stages |
| NativeBoundary | Valid internal alias yields PostgreSQL `5/INT4`; valid C library/symbol declarations succeed there. Native reports explicit `0A000`, never fabricated library/symbol lookup errors or installed signatures |
| HandlerDeclarations | Ordinary role `42501` before handler lookup; real PG handler declaration succeeds versus native `0A000`; rejected custom language remains missing natively; malformed whole input is rejected |
| Atomicity | Sentinel `7` and SQL `5` survive failures; failed signature `42883`, corrective same-signature SQL succeeds; native Dump bytes unchanged and failed Restore preserves its target after a valid prefix |
| Preservation | SQL `5/INT4`, direct/prepared/held calls and table defaults survive rejection and native replay; PostgreSQL PL/pgSQL returns `5` versus native's existing explicit unavailable-runtime error |
| FeatureSmoke | Compact absent-language/authority/native-mechanism/custom-handler boundaries, missing signature, SQL values/metadata and native sentinel/default roundtrip |

The native language inventory remains exactly `internal`, not an executable
registry. The PostgreSQL fixture inventory is independently `c`, `internal`,
`plpgsql`, `sql`; optional interpreters are not provisioned. Explicit custom
languages are dropped through deferred cleanup because resetting `public` does
not remove them. Handler pseudo-types are not invoked as scalar values.

Private `TestRoutineExternalASItems` and `TestRoutineExternalHandlerSyntax`
assert decoded AST content, explicit-empty versus omitted second AS string,
source origins, qualified handler names and flags, not source/wiring structure.
Public SQL assertions remain exclusively in the shared reference scenarios.
Existing scalar metadata helpers assert carrier types, alias `v`, `INT4`, one
row and EOF, unknown scalar nullability and absent length/precision.

Standalone durable feature smoke:

```sh
make check RUN='^TestReferenceRoutineExternalLanguagesFeatureSmoke$'
make check RUN='^TestReferenceRoutineExternalLanguagesFeatureSmoke$' REFERENCE=1
make quickcheck
```

Expected smoke is successful completion only when the new boundaries and missing
signature are asserted, SQL remains `5/INT4`, and sentinel `7`/default `5` survive
native controls. No PL/pgSQL native execution is claimed: its adjacent declaration,
compiler and interpreter runtime remain unavailable. No loader, shell, external
interpreter, native ABI or custom-handler registration is supplied by these tests.

### Scalar SQL routine contract

`TestReferenceRoutineCore*` owns consumer-facing anonymous positional
`CREATE FUNCTION ... RETURNS scalar LANGUAGE SQL AS 'SELECT ...'`, expression
calls and native persistence controls. Expectations were established with pinned
PostgreSQL18.6 UTF8/C before native implementation; no discovery filter remains.
Ordinary qualified/unqualified names include aggregate- and type-like spellings,
not a list of specially permitted routine names.

The selected scenarios cover:

- Creation, duplicate identity, zero/multiple arguments, nested calls, quoted
  names, both clause orders and direct/prepared/held parameter isolation.
- Namespace masking, exact/implicit/unknown matching, live EXECUTE/invoker
  authority and missing/wrong/ambiguous signatures.
- Existing SELECT bodies, no-row typed NULL, nonstrict NULL handling,
  assignment-only result conversion, FLOAT8/BYTEA ownership, first-row demand,
  ordinary aggregate/sort semantics and validation without evaluation.
- Modifying CTE effects across sibling calls, outer-error rollback and retained
  root FK completion obligations; prepared invalidation, current data,
  cancellation and held result lifetime.
- Full expected diagnostic fields/stages for malformed declarations/bodies,
  invalid returns and runtime failures. Native runtime context is statement 1;
  PostgreSQL may report its actual SQL-function inlining context.
- Aggregate/type-name call selection, scalar ALL acceptance, literal cast versus
  external unknown-parameter resolution and DISTINCT/FILTER rejection.
- Native Dump/Reset/Restore of functions, a view, defaults, data and a
  text-body/table-default cycle; invalid/read-failed/canceled Restore atomicity.
  Real mode additionally executes actual native dump bytes on PostgreSQL.

`TestReferenceRoutineStoredArgumentIdentity` distinguishes a stored view using an
implicitly selected bigint formal (`1001/INT8`) from a fresh call using a later
integer overload (`2001/INT8`). Native Dump/Reset/Restore must preserve both
outcomes, including the stored call's original argument conversion.

Function modes, declaration defaults, named/mixed/variadic calls and scalar
OUT/INOUT are covered by
[reference/operators](../operators/README.md#routine-parameter-binding), which
also owns named provider competition and candidate-specific path masking.
The superseded native-default-rejection case is removed from CoreDiagnostics;
all other existing routine/body scenarios retain their original contract.

Metadata checks are independent of value parity. INT4/INT8/FLOAT8 PostgreSQL
ScanTypes are int32/int64/float64; native integer ScanTypes are int64.
Length/decimal metadata is unknown. PostgreSQL nullable metadata is unknown.
Native scalar calls likewise have unknown nullability, whereas the existing
COUNT owner reports non-null/known and stored-view columns nullable/known.
The affected witnesses assert these distinctions explicitly before and after
replacement rather than applying scalar-call expectations to source columns.

Run from the repository root, with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceRoutineCore(CreationAndCalls|Resolution|BodyAndResults|Diagnostics|Lifetime|Persistence|FeatureSmoke|CallableKinds)$'
make check REFERENCE=1 RUN='^TestReferenceRoutineCore(CreationAndCalls|Resolution|BodyAndResults|Diagnostics|Lifetime|Persistence|FeatureSmoke|CallableKinds)$'
make check RUN='^TestReferenceRoutineStoredArgumentIdentity$'
make check REFERENCE=1 RUN='^TestReferenceRoutineStoredArgumentIdentity$'
make check RACE=1 RUN='^TestSQLRoutine(FrameIsolation|FrameResultOwnership|CallableSelection|StagedLookup|FirstRowDemand|ValidationNoExecution|BodyAnalysisRetention|RestoreStaging|FrozenPresentation|StoredDefaultValidation)$'
```

The internal selections establish proc frame/result ownership, catalog COW name
membership, query demand, shared analysis retention, isolated Restore validation,
frozen occurrence provenance and DEFAULT structural/property invariants.
The whole-project Docker-free gate remains `make quickcheck`, with an external
1800-second deadline; selected checks retain 900 seconds and do not substitute
for the whole-project gate.

Permanent feature smoke:

```sh
make check RUN='^TestReferenceRoutineCoreFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRoutineCoreFeatureSmoke$'
```

Expected: direct add1(4)=5/INT4; held inputs 4/9 produce 5/10; empty body produces
typed NULL; the first outer projection over an ordered subquery returns 10;
FLOAT8 conversion returns 5; restored add1=5 and cycle-default sum=14; missing
function returns 42883. A separate public `database/sql`/stuntgres-control
`go run` smoke observed those same native results with temporary, untracked source.
The ordinary `SELECT 10/x ... ORDER BY x DESC` negative control still divides by
zero: routine demand does not relocate projection below/above required sorts.

See [native scope](../../docs/DIALECT.md#scalar-sql-routines),
[replay contract](../../docs/FIXTURES.md#sql-routine-declarations-and-callers) and
[proc ownership](../../internal/engine/proc/README.md).

### Routine deletion contract

`TestReferenceRoutineDrop*` exercises exact input identity rather than call
coercion, bare-name ambiguity and namespace masking, domain/base distinction,
mode/name/OUT target syntax, complete notices/errors and quiet utility Prepare.
Direct/new/held prepared survivors retain independently expected104/INT8; a
deleted/recreated integer target yields14/INT4 instead of executing stale code.
Default/explicit RESTRICT keeps views, defaults, checks and data live; CASCADE
removes precisely their dependent components, leaving table row7 and the unrelated
overload/view. Grouped errors publish no early deletion. Operator dependencies
use real SQL routine identities, not kernel aliases.

Owner/schema-owner checks, EXECUTE-only denial, seeded/extension protection,
read-only-first admission and the native explicit-transaction boundary are
independently asserted. Procedure declarations and SQL-standard bodies are covered
by the ordered-body scenarios below; CALL/trigger creation remains separate.
Quoted callers survive callee deletion without invented body edges. Native
Dump/Reset/Restore and exact emitted PostgreSQL replay preserve unresolved callers;
callee recreation restores14/INT4. Missing operator/table/type, ambiguity,
denied namespace/type lookup, malformed/result mismatch and closed-formal failures
must not be mistaken for replay-deferrable missing calls. The permission negative
uses an existing private function namespace denied to the routine owner, so42501
precedes missing-call resolution. CREATE/Restore binding does not check source
SELECT ACLs; execution retains those live invoker checks.

Missing quoted-body callees retain the inherited routine diagnostic coordinate
boundary: native reports body-relative Position8 and statement1 Context;
PostgreSQL reports empty outer Position, InternalPosition8, the original body
InternalQuery and its actual inlining Context. Both full records independently
retain42883, the missing-callee message and the explicit-cast Hint. Routine DROP
does not change the prerequisite's general SQL-body coordinate mapping.

`TestReferenceRoutineDropBuiltinFamilyQuotedBody` covers deleted zero-argument
`public.num_nulls` and `public.num_nonnulls` overloads:7/INT4 before deletion,
full missing-call diagnostics and an installed caller through native replay and
exact emitted PostgreSQL replay, then17/INT4 after recreation. Structured
missing-call recording is independent of the builtin family's existing
composite-field recovery policy; no operator/ambiguity/capability diagnostic is
classified by numeric SQLSTATE.

Held calls reuse the same complete scalar metadata callback as direct/new
prepared calls, including nullability/length/decimal flags. Native negative
Restore controls assert complete independently specified diagnostics, not only
SQLSTATE, plus byte-equivalent target preservation. The corresponding
`TestReferenceRoutineDropReplayDiagnostics` SQL witnesses ground real CREATE
diagnostics while explicitly preserving native final-binding coordinates and
Context, including retained LOWR6 rather than weakening fields.

Selected proof (each under an external 900-second deadline):

```sh
make check RUN='^TestReferenceRoutineDrop(Identity|Diagnostics|Dependencies|Atomicity|OperatorDependency)$'
make check RUN='^TestReferenceRoutineDrop(Identity|Diagnostics|Dependencies|Atomicity|OperatorDependency)$' REFERENCE=1
make check RUN='^TestReferenceRoutineDrop(AuthorityAndAdmission|QuotedBody|Persistence|FeatureSmoke)$'
make check RUN='^TestReferenceRoutineDrop(AuthorityAndAdmission|QuotedBody|Persistence|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceRoutineDrop(BuiltinFamilyQuotedBody|ReplayDiagnostics)$'
make check RUN='^TestReferenceRoutineDrop(BuiltinFamilyQuotedBody|ReplayDiagnostics)$' REFERENCE=1
make check RUN='^TestReferenceRoutineReviewClosedFormals$'
make check RUN='^TestReferenceRoutineReviewClosedFormals$' REFERENCE=1
make check RUN='^TestRoutineDrop(Parser|Selection|CatalogIsolation|ReplayClassification|UnnotedResolution)$' RACE=1
```

Permanent public feature smoke:

```sh
make check RUN='^TestReferenceRoutineDropFeatureSmoke$'
make check RUN='^TestReferenceRoutineDropFeatureSmoke$' REFERENCE=1
```

Expect RESTRICT2BP01, precise CASCADE, missing NOTICE00000, procedure wrong-kind
42809, surviving held104/INT8, dangling-caller replay and recreation14/INT4.
These are proof requirements, not a claim that an author ran them.

### Ordered SQL routine bodies

`TestReferenceRoutineSQLBodies*` extends the scalar contract with shared
function/procedure declarations, named/qualified/positional IN formals, VOID,
quoted ordered scripts and checked SQL-standard RETURN/BEGIN ATOMIC bodies.
CALL and ALTER routine lifecycle DDL remain separate unsupported capabilities;
procedures cannot execute as scalar expressions.

The independent witnesses assert:

- Held DML→SELECT calls see their own writes and return 15 then 20/INT4.
  Final INSERT/UPDATE/DELETE/MERGE RETURNING completes every required write,
  including rejection/rollback of a later invalid row.
- Columns and real aliases precede formal fallback, including genuine ambiguous
  columns. Nested/CTE formals retain the same closed typed slot boundary.
- Standard RETURN defaults LANGUAGE SQL; nonterminal RETURN does not terminate
  an ATOMIC body. Empty VOID bodies are accepted. SQL VOID has a NULL cell,
  unknown nullable/length/decimal flags, oracle ScanType `interface {}` and native
  ScanType `string` (the existing native VOID codec); both return a NULL cell.
  full discarded nextval output advances three times.
- Default CREATE validation never executes effects. After a quoted body source
  changes from integer to text, the first nextval runs before terminal recheck
  fails with 42P13 statement 2, even on empty input.
- COMMIT/SAVEPOINT fail at reached quoted execution; standard utilities fail at
  declaration. Full primary/detail/position/context and prepare/execute stages
  are specified independently, not copied from native output.
- Prefix writes roll back on late/outer errors and active cancellation; explicit
  transactions remain private and rollback cleanly on the same physical owner.
- Checked table/view captures survive existing rename/shadow routes; hard DROP
  dependencies and selected bigint overload1001 differ from a fresh integer
  overload2001. Quoted source names remain soft and can rebind after DROP.
- Named function/procedure/body forms and dependencies survive two native
  Dump/Reset/Restore rounds. Real mode replays actual native bytes unchanged on
  pinned PostgreSQL; invalid/canceled replacement preserves live state.
- Review regressions `FrozenAuthority`, `MergeCaptures`, and `NestedPersistence`
  specify current invoker privileges for held standard callers, captured MERGE
  ON/WHEN/action/RETURNING and unexecuted arms, source/target rename and overload
  retention, independently expected dependency edges, and nested formal queries
  in SELECT and mutation recipes across both replacement rounds and emitted SQL.
  Their oracle discovery and native-red evidence precede the production remedy.
  `FrozenAuthority` expects1008,42501,1008 across each revoke/restore and a
  fresh integer-overload result2008. `NestedPersistence` expects SELECT5,
  mutation result9/stored5, a column1-only normal dependency and soft-source
  rebinding23→29 after each round. `MergeCaptures` expects scalar1001 and sorted
  target `(1,15),(2,20),(3,17),(5,50)`. Its dormant callee appears only in an
  unreachable UPDATE action; a separate exact-key MERGE supplies BY SOURCE
  DELETE/DO NOTHING without requiring new full-join predicate support.
  RESTRICT/pg_depend are checked per replacement; destructive dormant CASCADE
  follows the final native round and each independent emitted PostgreSQL replay.

Root-run selected proof, external 900-second deadline per command:

```sh
make check RUN='^TestReferenceRoutineSQLBodies(Statements|Arguments|Standard|Void|Validation|Restrictions|Atomicity|Dependencies|Persistence|ProcedureDeclarations|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRoutineSQLBodies(Statements|Arguments|Standard|Void|Validation|Restrictions|Atomicity|Dependencies|Persistence|ProcedureDeclarations|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRoutineSQLBodies(FrozenAuthority|MergeCaptures|NestedPersistence)$'
make check RUN='^TestReferenceRoutineSQLBodies(FrozenAuthority|MergeCaptures|NestedPersistence)$'
make check RUN='^TestRoutine(FormalRemapBoundary|NestedRemapExactlyOnce|AddressProjectionRemapBoundary|ProgramCaptureValidation|MergeCaptureValidation|ActionValidationBoundary)$' RACE=1
make check RUN='^TestViewPartsRoundTrip$'
make check RUN='^TestReferenceRoutineCoreDiagnostics$'
make check REFERENCE=1 RUN='^TestReferenceRoutineCoreDiagnostics$'
```

The feature smoke suffix checks held results15/20, RETURN5, and a VOID update
leaving balance21. Initial bounded oracle discovery was run before production
edits and its PG-only filter removed before observed native-red. Selectors
describe required proof, not a claim that the completed candidate passed.


### Internal char conditional equality

Change note: simple CASE now resolves existing catalog-owned internal `"char"`
operands and unknown WHEN literals through the existing equality comparison
provider. This also supplies the shared NULLIF resolver's missing exact signature;
it adds no cast, user-column type, catalog provider or predicate syntax.

`TestReferenceConditionalInternalCharEquality` independently checks an ordinary
table and view's `pg_class.relkind`: matching and nonmatching arms, first-match
selection, omitted ELSE, typed WHEN operands, NULL nonmatches and a nested typed
NULL test. Direct and prepared execution assert exact names, NAME/TEXT/INT4/CHAR
database types and rows. CHAR ScanType is string natively and interface{} through
pq; returned non-NULL CHAR cells remain strings on both adapters.

Run the selected proof in both modes with an external 900-second deadline:

```sh
make check RUN='^TestReferenceConditionalInternalCharEquality$' REFERENCE=1
make check RUN='^TestReferenceConditionalInternalCharEquality$'
make check RUN='^TestReferenceConditional(Results|Parameters|Diagnostics|TypeMatrix)$'
make check RUN='^TestReferenceConditional(Results|Parameters|Diagnostics|TypeMatrix)$' REFERENCE=1
make check RUN='^TestConditional(EqualitySignatures|RuntimeDemand)$'
```

The current pinned PostgreSQL18.6 oracle established the catalog fixture's
expectations before the native regression exposed the missing equality signature.
A separate standalone public `database/sql` smoke then observed direct execution
and the same prepared statement twice: names `name/kind/omitted/unequal`, types
`NAME/TEXT/TEXT/CHAR`, and table/view rows `(char_smoke_table,table,table,NULL)` and
`(char_smoke_view,other,NULL,v)`. That throwaway program is not a tracked test
deliverable; use the permanent selector above for reproducible feature proof.
The unchanged psql relation-list query belongs to a separate, unintegrated
Boolean-predicate part and is not claimed as verified by this revision.


### Parallel SELECT scan contract

The shared `TestReferenceQueryParallel*` scenarios specify exact occurrence bags
(including duplicate and NULL multiplicities), empty/singleton/partition-boundary
inputs, direct and held prepared execution, hidden/expression sort keys,
execution-time worker settings, serial safety effects, eager native failure versus
PostgreSQL streaming failure, cancellation and retained output ownership.
Expectations are independently specified, not learned from native output.
SQL parity does not prove that either backend launched workers: private engine
instrumentation must establish actual concurrent evaluation, exact-once ranges,
snapshot/pin isolation, sorted-run merge and joined cancellation separately.

Run these selected batches with an external 900-second deadline, in both modes
after every affected change:

```sh
make check RUN='^TestReferenceQueryParallel(Bags|MergeOrder|Settings)$'
make check REFERENCE=1 RUN='^TestReferenceQueryParallel(Bags|MergeOrder|Settings)$'
make check RUN='^TestReferenceQueryParallel(Safety|Errors|Cancellation|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryParallel(Safety|Errors|Cancellation|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceSettingsBuiltinRegistry$'
make check REFERENCE=1 RUN='^TestReferenceSettingsBuiltinRegistry$'
make check RUN='^TestReference(TimezoneBuiltinRegistryDescriptions|FormatEncodingSettings|SettingsShowAllPlaceholdersAndLifecycle)$'
make check REFERENCE=1 RUN='^TestReference(TimezoneBuiltinRegistryDescriptions|FormatEncodingSettings|SettingsShowAllPlaceholdersAndLifecycle)$'
```

Complete native registry consumers independently include the new default2 and
released description, retaining exact cardinality, ordering and completeness
checks. Genuine PostgreSQL pg_settings projections include the same definition;
host-registration lifecycle phases remain explicitly native-only.
The settings registry definitions are owned by
[session_state](../session_state/README.md#testing); their name-based selectors
above are unchanged.


Initial oracle discovery follows the shared testing guide's temporary PG-only
backend filter; the completed definitions run both available backends.
The PostgreSQL oracle is the existing pinned 18.0 UTF8/C fixture. No cost settings,
EXPLAIN assertion or PostgreSQL background-worker scheduling claim is involved.
Worker configuration is a ceiling, not a guarantee of dispatch.

Private proofs gate two workers inside real filter/target Eval (not elapsed time
or goroutine counts), verify disjoint occurrences and independent snapshot pins,
publish peer writes while workers are paused, and prove primary-error cancellation
joins blocked siblings. Sorted singleton-run comparison failure distinguishes
leader merge from worker-local sorting.

```sh
make check RACE=1 RUN='^TestQueryParallel(Partition|ConcurrentExecution|Snapshot|Eligibility|Cancellation|ErrorIdentity|Merge|Ownership)$'
make check RUN='^TestReferenceQueryParallelFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQueryParallelFeatureSmoke$'
```

`TestReferenceQueryParallelFeatureSmoke` is the committed public smoke scenario.
It independently checks held settings/parameters, duplicate/NULL bags, ordered
IDs, late division failure and subsequent execution, and LOCAL/RESET restoration.
`TestReferenceQueryParallelSafety` covers lazy sequence cursor draws;
`TestReferenceQueryParallelLifecycle` covers retained output through peer mutations.
Actual concurrent worker evaluation is established by the private gated proofs above.



### Subquery regression and instrumentation coverage

`reference/query_core/query_subquery_*_test.go` contains the shared direct/prepared,
held-handle, mutation, metadata, admission, locking and stored-view scenarios.
Both backends execute the same callback without a discovery-only backend filter:

```sh
make check RUN='^TestReferenceQuerySubquery'
make check REFERENCE=1 RUN='^TestReferenceQuerySubquery'
```

The integration handoff additionally supplies the exact selector for migrated
consumer/no-FROM families; those families are not replaced by the new prefix.
Each selective invocation uses a900-second deadline, in both modes before review.

Internal `TestQuerySubquery*` cases instrument actual bound plans, typed
comparison providers, lexical frame lookup, admission manifests and borrowed
stage cleanup. Sentinels prove ignored EXISTS targets versus required ORDER
keys/derived projections and normal target demand. They are not duplicate
public SQL feature fixtures and do not use fake database APIs. Parser
`TestQuerySubqueryAST` / `TestQuerySubquerySourceWalk` check structural syntax,
positions, source occurrence order and child-specific rowmark ownership.
Run those private units with
`make check RUN='^TestQuery(Subquery|DerivedSubquerySyntax)'`.

### Structural row comparison coverage

The untagged `row_compare_*_test.go` scenarios retain independent direct/prepared
values, expanded constructors, full diagnostics, BOOL metadata, held parameters,
and stored recipe/native replay assertions. Run both modes with an external
900-second deadline per invocation:

Held NULL parameter frames must preserve the row comparison's structural pairs.
Preflight may replace their operand conversions, but must not independently
fold an inner strict comparison into a scalar NULL and erase the row's
NULL-aware or lexicographic frontier. `TestReferenceRowCompareHeld` exercises
the same prepared handle across those frames and retains its BOOL contract.

```sh
make check RUN='^TestReferenceRowCompare(Values|Subquery|Constructors|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceRowCompare(Values|Subquery|Constructors|Diagnostics)$'
make check RUN='^TestReferenceRowCompare(Metadata|Held|Stored)$'
make check REFERENCE=1 RUN='^TestReferenceRowCompare(Metadata|Held|Stored)$'
make check RUN='^TestRowComparison(DistinctPairs|Analysis|StoredRemap)$'
make check RUN='^TestExprCollation(RowOperandDemand|RetagAuthority)$'
```

The session-only `local://row-compare-smoke.go` handoff is a standalone public-API
program, run from the worktree using `go run /absolute/session/path/row-compare-smoke.go`.
It checks row equality/NULL ordering, total DISTINCT, fresh held parameters,
subquery membership, arity recovery and stored native Dump/Reset/Restore.
The root records its actual argv and observations; it is not a permanent program
or a replacement for either selected reference mode.

### SELECT CTE verification

The untagged `reference/query_core/query_cte_*_test.go` definitions independently
specify dependent/reused bags, scopes, genuine folding/fences, executable
volatility, shared lazy demand, invocation ownership, complete diagnostics,
stored consumers, authority/snapshots, native lifecycle controls and feature smoke.
Every changed definition requires both selected modes; do not retain an
oracle-discovery backend filter. Catalog pg_class and transient-name assertions
are shared; pg_type is a PostgreSQL-only supplement to the existing native
absence boundary, not an added native provider.

Run bounded batches from the selected worktree, with an external 900-second
deadline for each invocation:

```sh
make check RUN='^TestReferenceQueryCTE(Dependencies|Reuse|Scope|Parenthesized|Diagnostics|Catalog)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Dependencies|Reuse|Scope|Parenthesized|Diagnostics|Catalog)$'
make check RUN='^TestReferenceQueryCTE(Folding|Volatility|Demand|Invocation)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Folding|Volatility|Demand|Invocation)$'
make check RUN='^TestReferenceQueryCTE(Stored|Authority|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Stored|Authority|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceQueryCTE(RowLocks|DeadDemand|PhysicalFrontier)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(RowLocks|DeadDemand|PhysicalFrontier)$'
make check RUN='^Test(RowLockViewAggregateComposition|ReferenceRowLockViewAggregateComposition)$'
make check REFERENCE=1 RUN='^TestReferenceRowLockViewAggregateComposition$'
make check RUN='^Test(QueryCTE|CTEParser|InsertSelectBoundaries)'
make check RACE=1 RUN='^TestQueryCTE(SharedFrontierAndReaderLifetime|StickyFailureAndCloseOnce|InvocationIsolationAndInheritance|ProjectDispatcherOwnsInvocation)$'
```

The existing stored aggregate composition expectations are unchanged: native's
marked hidden/grouped FALSE frontier retains physical grants/conflicts, whereas
the pinned PostgreSQL constant-gating control has none. The CTE physical-frontier
definition separately checks opaque lock bodies and independently reachable
marked aggregate siblings. Function units prove branch-local owner/mark inspection,
correct nested input mapping and identity-only/opaque boundaries without SQL.

Parser generation is root-owned after grammar edits. Agents do not run
generation, builds, checks or formatters. Root whole-project Docker-free
verification uses quickcheck, not a selected-test substitute.

Static folding eligibility includes unused nested WITH volatility, independently
of executable ACL/source/demand pruning. The enclosing default/NOT MATERIALIZED
division witness expects22012, followed by an unused-sequence draw1. Stored view
sources must establish invocation ownership through universal project dispatch,
without changing inherited row-mark settlement; the direct-dispatch unit checks
lazy sharing and owner-driven close-once lifetime.

Actual feature smoke also requires the complete standalone public Go source
retained in the implementation session as `local://cte-smoke.go`. The root
materializes it outside the checkout as `$SESSION_TMP/cte-smoke.go` and records
the resolved argv, worktree cwd, exit, stdout and stderr:

```sh
timeout 900s go run "$SESSION_TMP/cte-smoke.go"
```

This program uses only public stuntgres and standard-library APIs and fails on
any independent metadata/bag/diagnostic/count mismatch. It exercises dependent
direct/prepared results, real fold-versus-materialization division witnesses,
shared VOLATILE draws and fresh held Execute, lazy unused/LIMIT0/LIMIT1 demand,
enclosing-reader sharing, correlated local isolation, static unused-nested-WITH
folding fences with no draw, stored view invocation ownership, transient scope,
full duplicate/malformed diagnostics and persistent Dump invariance. The review
supplement asserts full explicit CTE-alias OF diagnostics with positive untargeted
locking, materialized FALSE/NULL no-draw and folded false divisor gates.
It also checks direct/prepared opaque marked CTE bodies leave peer DELETE free
for all hints while an independently reachable marked aggregate sibling retains
native's mandatory conflict frontier, then releases grants on transaction end.
The program is a temporary session artifact, not a committed scaffold. Permanent
`TestReferenceQueryCTEFeatureSmoke` additionally runs in both selected modes.

### Recursive CTE verification

`TestReferenceQueryRecursiveCTEWorktable`, `Equality`, `Scope`, `Diagnostics`,
`Demand`, `Lifecycle`, `Stored`, `FeatureSmoke` and `ReviewDiagnostics` share independently specified
bags, metadata, counts, whole diagnostic records and lifecycle assertions across
the native and pinned PostgreSQL18.0 fixtures. Ordering is required only where SQL
provides a complete ORDER BY. Zero-column recursive UNION retains occurrences like
ALL; positive-width recursive keys still include columns omitted by the parent.
MERGE's own recursive modifier is rejected. Local recursive SELECT consumers use
the established MERGE count codec convention (native real count/lib-pq zero), with
exact persisted rows proving INSERT/UPDATE effects on both backends.

`ReviewDiagnostics` specifies body-WITH self-reference precedence over recursive
decorations, SubLink context overriding anchors/nullable joins/prohibited set
operands, and subselect-before-testexpr reference positions. It checks complete
42P19 records with ASCII/UTF-8 character positions across direct/prepared Query
and Exec, unchanged sentinel rows after errors, and harmless body-WITH/shadow
controls with bags and raw metadata. Ordinary source-admission order is unchanged.

Run each command from the assigned worktree with an external900-second deadline:

```sh
make check RUN='^TestReferenceQueryRecursiveCTE(Worktable|Equality|Scope|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceQueryRecursiveCTE(Worktable|Equality|Scope|Diagnostics)$'
make check RUN='^TestReferenceQueryRecursiveCTE(Demand|Lifecycle|Stored|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryRecursiveCTE(Demand|Lifecycle|Stored|FeatureSmoke)$'
make check RUN='^TestReferenceQueryRecursiveCTEReviewDiagnostics$'
make check REFERENCE=1 RUN='^TestReferenceQueryRecursiveCTEReviewDiagnostics$'
make check RUN='^Test(QueryRecursiveCTE|CTEParserRecursive)'
make check RACE=1 RUN='^TestQueryRecursiveCTE(CurrentGenerationAndSharing|DemandCancellationAndClose)$'
make check RUN='^TestReferenceQueryRecursiveCTEFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQueryRecursiveCTEFeatureSmoke$'
```

Private tests exercise lexical identity/prewalk, anchor Usage, opaque graph
edges, current-generation buffers, replay sharing, typed collision resolution,
zero-width occurrence handling, live-pass cancellation/close ownership and
immutable rewrite/foreign remap and exact stored namespace-node identity.
Synthetic enum outputs use unknown nullability, not stored-column nullability.
Transaction failure scenarios precreate their target outside BEGIN and prove
rollback of staged rows, preserving native utility-in-transaction boundaries.
Public scenarios retain running server/native
cancellation, prepared argument/dependency freshness, failure atomicity, catalog
invariance and native persistent/retained-output controls.

The part handoff additionally supplies a session-only standalone public
`recursive-cte-smoke.go`. Its argv is
`go run \"$SESSION_TMP/recursive-cte-smoke.go\"` with an external900-second deadline.
It is not a tracked script or a replacement for selected dual-mode proof.
The smoke also checks body-WITH self-reference before a competing ORDER BY,
anchor SubLink context and IN subselect-before-testexpr exact diagnostics, plus
harmless body-WITH and local-shadow positive bags. These supplement, not replace,
the shared `ReviewDiagnostics` scenario.

### Recursive CYCLE verification

The nine `TestReferenceQueryCyclePaths`, `Marks`, `Binding`, `Metadata`,
`Diagnostics`, `Prepared`, `Stored`, `Lifecycle` and `FeatureSmoke` definitions
share public SQL/API scenarios across native and pinned PostgreSQL 18.0.
The fixed graph a→b→a with branch a→c has exactly four rows: unmarked
`a/{(a)}`, `b/{(a),(b)}`, `c/{(a),(c)}`, and marked
`a/{(a),(b),(a)}`. Other bags distinguish branch-local ordered record ancestry,
NULL fields, augmented UNION DISTINCT, generated-column visibility, typed custom
marks, held statement freshness, persistent text-path recipes, late failure and
genuine running cancellation. Raw metadata is inspected before iteration.

Binding additionally pins contained sibling stars to original columns only;
explicit sibling mark/path projections remain available. External `r AS t(x)`
stars expose `x,cyc,path`, while `r AS t(x,y,z)` fails analysis with unpositioned
42P10: only the original column is available for reference aliases. The same
original-width boundary applies to self references and stored SQL rendering.

The completed definitions are unfiltered: both backends execute the same cases.
Goldens encode the two pinned oracle runs, including `_RECORD` descriptors,
TEXT Length `(MaxInt64,true)` on both adapters (the preexisting documented
contract), escaped record-array cells, full diagnostics and failure phases.
The existing fixture raw-array validator recognizes `_RECORD`: PostgreSQL cells
must be `[]byte`, native cells strings, before exact canonical text comparison.
Common mark metadata preserves numeric(4,2), character(2) and varchar(3)
modifiers. Unconstrained NUMERIC truthfully exposes pq's precision/scale
65535/65531 versus native unknown precision/scale; raw decimal values retain
their observed scale and character values retain padding.
Transient coverage retains shared pg_class absence checks, native unchanged
Dump and retained generated rows/metadata. Both backends additionally assert no
pg_type pollution through the genuine catalog source. Private ownership units
ensure occurrence descriptors do not replace catalog RECORD/RECORD[] identities.


Held raw `record[]` and `path::text` both survive an INT4→INT8 key-field
change. This public stable result identity does not relax native internal
descriptor ownership/generation validation; the ordinary scalar-result control
still rejects a changed result type. Ambiguous RHS references remain separate
from constant-RHS generated-name/body-error precedence cases. The width witness
points at the first RHS expression (character57), not SELECT. VIEW/CTAS/MV
pseudo-type checks require successful Prepare followed by execution rejection.
`TestReferenceQueryCycleViewReplacePrecedence` separately proves replacement
rename/type diagnostics precede path pseudo-type rejection, appended paths still
reject, and failed direct/held replacements preserve the original view. Run
`make check RUN='^TestReferenceQueryCycleViewReplacePrecedence$'` in quick mode
and again with `REFERENCE=1`.
Malformed staged SQL reports full-script character88 on both backends; later
Restore binding likewise retains full-script position136 on both backends.
No discovery providers or observation probes remain in the original nine
definitions. Pinned discovery and the root-observed native-red checkpoint
precede production implementation.
The following selectors are required final proof, not claims of passing checks.

Root-owned selected commands (external900-second deadline, cwd assigned worktree):

```sh
timeout 900s make check RUN='^TestReferenceQueryCycle(Paths|Marks|Binding|Metadata)$'
timeout 900s make check RUN='^TestReferenceQueryCycle(Diagnostics|Prepared|Stored)$'
timeout 900s make check RUN='^TestReferenceQueryCycle(Lifecycle|FeatureSmoke)$'
```

For the real pinned oracle, repeat each command with `REFERENCE=1`.
Before review, run all three selectors and the review-regression selector below
in both modes on the same completed definitions; finalcheck does not substitute
for selected dual-mode evidence.
Selected lifecycle race proof is
`make check RACE=1 RUN='^TestReferenceQueryCycle(Lifecycle|Prepared|Stored)$'`.

Review regressions add `TestReferenceQueryCycleStoredTypedMarks` and
`TestReferenceQueryCycleConflictPrecedence`. The former preserves asymmetric
NUMERIC source-modifier conversions in both TO and DEFAULT through native
Dump/Reset/Restore, foreign restoration and actual emitted SQL replay on
PostgreSQL. The latter checks mark-first conflict precedence when original
columns are ordered path-before-mark, including complete diagnostics, UTF8
position and Query/Exec/Prepare rejection phases.

```sh
timeout 900s make check RUN='^TestReferenceQueryCycle(StoredTypedMarks|ConflictPrecedence)$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceQueryCycle(StoredTypedMarks|ConflictPrecedence)$'
```

The two review regressions are unfiltered and use independently pinned outcomes:
`numeric(3,1) '1.25'` yields `1.3` whether it is TO or DEFAULT, while common
output NUMERIC remains unconstrained. Initial, restored and replayed views
assert raw cells plus complete metadata before iteration, including native
stored-view Nullable `(true,true)` versus pq `(false,false)`. The competing
name witness reports the mark conflict at UTF8 character73 across all three
rejection phases. Root recorded pinned observations and native red before
authorizing the remedies; fresh selected proof in both modes is still required.

`TestReferenceQueryCycleFeatureSmoke` is the permanent public smoke definition:
exact closing row/path bag and metadata, a/b/NULL/a held seeds, NULL multikey,
full conflict/incompatible-mark diagnostics, stored text path and real native
Dump/Reset/Restore/foreign owner, late statement failure and joined running
cancellation followed by same-owner recovery. The implementation handoff
supplies a session-only standalone public consumer `cycle-smoke.go`; root
records its actual resolved path as `CYCLE_SMOKE_GO` and runs
`timeout 900s go run "$CYCLE_SMOKE_GO"`. It is not a tracked temporary script.

Package-function proof (no public SQL duplication):
`make check RUN='^TestCTECycle(Grammar|Legality)$'` and
`make check RUN='^TestQueryCycle(Equality|Parent|Ownership|Lifecycle|Signature)$'`.
These cover positioned syntax/legality, retained equality/hash authorities,
actual parent slots, immutable occurrence descriptors and memoized remap,
cancellation/sticky failure/close ownership and public versus private signatures.

### Recursive SEARCH verification

The ten `TestReferenceQuerySearchOrdering`, `Keys`, `Metadata`, `Diagnostics`,
`ReviewDiagnostics`, `Prepared`, `CycleInterop`, `Stored`, `Lifecycle` and
`FeatureSmoke` definitions share public SQL/API bodies and independently specified
expectations. The
scrambled seven-node tree must sort to DFS `1,2,3,5,4,10,11` versus BFS
`1,2,10,3,4,11,5`. Generated values are actual record ancestry arrays (DFS) or
depth/current-key records (BFS), not traversal scheduling or output ordinals.
NULL fields, multiple/reordered keys, tied values, augmented UNION identity,
lazy JSON comparison demand, original-column alias/star boundaries and installed
CYCLE interoperability have separate witnesses.

The definitions are unfiltered and encode Main's pinned PostgreSQL 18.6
discovery observations. Both raw BFS `RECORD` and DFS `_RECORD` expose pq
`ScanType interface{}` and `[]byte` cells versus native string descriptions and
cells. Original INT4 uses pq int32 versus native int64. Nullable is unknown,
record Length is unknown, and DecimalSize is unknown; TEXT Length is MaxInt64.
Metadata is asserted before iteration, including empty anchors and LIMIT0.
Escaped NULL/empty/literal-NULL/comma/quote/backslash keys have exact codec goldens.
Held raw seq and seq::text survive INT4→INT8 nested key changes in both modes;
the exposed scalar control rejects execution with unpositioned 0A000.
Full diagnostics include optional absence and rune-count positions. Analysis
errors reject Prepare; raw pseudo-output persistence and reached JSON comparator
errors prepare successfully and fail execution without publishing Rows/results.
JSON errors have no Position or Hint. Independent semantic expectations remain
the authority for DFS/BFS ordering and positive behavior. Root recorded native
feature-red at SEARCH syntax before authorizing production. Final selected
dual-mode proof is required after implementation; readiness is not verification.

`ReviewDiagnostics` preserves the review's combined-error boundaries in both
modes, with ASCII/Unicode prefixes, used/unused items and all diagnostic routes:
body width/type/source errors before SELECT-arm expandability, arm form before
SEARCH names, names before the top-level-self rewrite restriction, and excess
nonrecursive aliases before the nonrecursive SEARCH guard. Ordinary nested
recursive forms and installed CYCLE closing rows remain positive controls.

Root-owned selectors, each under an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceQuerySearch(Ordering|Keys|Metadata|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceQuerySearch(Prepared|CycleInterop)$'
make check REFERENCE=1 RUN='^TestReferenceQuerySearch(Stored|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQuerySearchReviewDiagnostics$'
```

Repeat all four completed selectors without `REFERENCE=1` as well. Stored
scenarios reject raw pseudo-type outputs but preserve scalar/text views,
CTAS/MV/INSERT data, symbolic native Dump/Reset/Restore, foreign-owner replay and
actual native SQL replay into PostgreSQL. Lifecycle scenarios preserve sentinels,
CTAS absence and old MV contents on late failure, and genuinely cancel infinite
constant-key recursion before asserting same-owner/same-held recovery.

The permanent feature smoke composes both exact tree orders, NULL values, repeated
held inputs, positioned key/name errors, persistent text projections and native
replay, late failure atomicity and active cancellation. Its final selectors are
`make check RUN='^TestReferenceQuerySearchFeatureSmoke$'` and the same command
with `REFERENCE=1`. The implementation handoff supplies session-only standalone
public source `local://search-smoke.go`; root resolves `SEARCH_SMOKE_GO` and
runs `timeout 900s go run "$SEARCH_SMOKE_GO"` separately. It also checks the two
original R1 repros and R2 aliases in both modes and Unicode positions, with full
logical diagnostics and no published Rows/statement on direct/Prepare failure.
Neither smoke substitutes for the selected dual-mode definitions.


Supplementary package-function selectors (no duplicated public SQL):

```sh
make check RUN='^(TestCTESearch(Grammar|Legality)|TestSearchOccurrenceOrdering|TestQuerySearch(Shape|Parent|Identity|Remap|Cancellation|Signature))$'
make check RACE=1 RUN='^TestQuerySearch(Parent|Remap|Cancellation)$'
make check RUN='^TestQueryCycle(Equality|Parent|Ownership|Lifecycle|Signature)$'
make check RUN='^TestOrderingBtreeIdentity$'
```

Installed CYCLE interoperability regression selectors are
`^TestReferenceQueryCycle(Metadata|Prepared|Stored|Lifecycle|FeatureSmoke)$`,
each in native and real pinned modes. After edits root owns generation/formatting,
selected proof, standalone smoke, whole-project quickcheck and independent review.

### SQL routine resolution review regressions

`routine_resolution_review_test.go` shares direct and prepared definitions for
same-name SQL overload coexistence with polymorphic `array_append`, variadic
`concat` and defaultable `parse_ident`. Both actual SQL and builtin winners are
required, including qualified calls, public parameters, explicit pg_catalog
placement, later exact signatures and per-signature namespace masking.

Unknown-position coverage includes nonstring category conflicts in both orders,
string preference, preferred numeric/string types, unanimous known/unknown input
selection, and the case where all-position category filtering has no survivor.
Ambiguities require complete 42725 fields (position8 and the standard cast hint),
direct execute failure and prepared analysis failure. Root observed the initial
independent winners and diagnostics against pinned PostgreSQL18.6 in tests46/47.
The added SQL/builtin `concat(NULL,NULL)` ambiguity independently requires42725,
not successful builtin fallback, and needs fresh selected oracle proof. Earlier
oracle evidence does not establish a post-remedy native pass.

`OriginalRecipes` additionally preserves the existing native null-count
arithmetic boundary with a same-name SQL overload, signed-literal admission and
actual SQL winners. Direct and held builtin calls advance a sequence exactly
once per execution; preparation and current-value probes do not advance it.
These added expectations require fresh selected oracle/native proof.

Root-owned selected proof, with an external900-second deadline for each command:

```sh
make check RUN='^TestReferenceRoutineResolution(ProviderCoexistence|UnknownPositions|OriginalRecipes)$'
make check RUN='^TestReferenceRoutineResolution(ProviderCoexistence|UnknownPositions|OriginalRecipes)$' REFERENCE=1
make check RUN='^TestSQLRoutine(ProviderCallShapes|CallableShapeMasking|UnknownCategorySelection|OriginalArgumentRecipes)$'
```

The first two also serve as feature smoke: builtin array result `{1,2}`, concat
`ab`, defaulted identifier array `{foo,bar}`, independently fixed SQL/path winner
values, and order-independent42725 ambiguity. Internal package-function coverage
checks family applicability without duplicating public SQL witnesses.

### SQL overload and existing window-provider coexistence

`routine_window_review_test.go` adds `RoutineWindowCoexistence`,
`RoutineWindowPolymorphic` and `RoutineWindowDiagnostics`. Direct and prepared
calls require builtin `row_number() OVER ()` to return1/INT8 before and after a
same-name SQL integer overload, while the actual SQL call `row_number(4)`
returns104/INT4. Qualified controls, explicit search paths and zero-input
signature masking distinguish namespaces from whole-name shadowing.

Existing first_value and lag providers retain their polymorphic argument and
default-value shapes alongside nonmatching SQL overloads. Window names, database
types and cells use the ordinary fixture contract; SQL scalar-only raw scan and
nullability assumptions are not imposed on existing window descriptors.
Actual scalar SQL OVER winners and missing OVER retain complete42809 diagnostics
with direct execution/prepared-analysis stages. Root observed the original three
definitions against pinned PostgreSQL18.6 in tests64; native reached the genuine
missing-window-kind regression, not a passing candidate.

Additional independent controls require same-query nested windows to report
42P20, including a window wrapped in SQL-call actuals, and reject a window in
WHERE. A window in a child query and a window actual of a real scalar SQL winner
remain valid. These additions and the remedy require fresh selected oracle and
native proof; earlier evidence does not establish their passing result.

Root-owned proof/smoke commands, each with an external900-second deadline:

```sh
make check RUN='^TestReferenceRoutineWindow(Coexistence|Polymorphic|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceRoutineWindow(Coexistence|Polymorphic|Diagnostics)$'
make check RUN='^TestSQLRoutine(ProviderCallShapes|WindowCallShapeTargets)$' RACE=1
make check RUN='^TestQueryWindow(ValueBindingUnwind|Review4ArgumentOwnerRestoration|MetadataAndOverlayOwnership|OffsetGraph)$'
```

Expected feature observations are builtin1/INT8, SQL104/INT4, first_value7,
lag default9 and typedNULL, correct per-signature path winners, scalar-over42809
and nested/WHERE42P20 without bypassing the existing window owner.

### Routine prebinding and rule qualification review regressions

`routine_nesting_review_test.go` adds `RoutineNestingDiagnostics` and
`RoutineNestingBoundaries`. Direct and prepared ordinary aggregate and aggregate
window calls retain nesting checks before and after installing nonmatching SQL
overloads of sum/count. Qualified builtin calls and windows wrapped in SQL-call
actuals must retain the complete 42803/42P20 diagnostic fields and analysis
stages. Child-query windows, callee-body windows and legitimate scalar SQL
winners remain valid. Known-argument reuse must preserve the existing owner's
window-occurrence bookkeeping even when a signature discards its bound input.
Legal COUNT(expr) OVER calls preserve their existing native 0A000 capability
boundary (PostgreSQL returns 1), checked directly and prepared; supported numeric
SUM window calls retain their values. Illegal nested-window arguments require
42P20 before that capability check.

`routine_rule_review_test.go` adds `RoutineRuleQualificationPersistence`.
Existing INSERT rules use both a direct boolean SQL call and a call nested
under NOT as qualifications. Actual inserts of -2, 0 and 3 independently require
input rows -2/0/3, a positive log containing 3, and a nonpositive log containing
-2/0. Native Dump/Reset/Restore preserves these rows and subsequent inserts -4/5
must update the appropriate logs. In real mode the exact native-emitted stream
is replayed on pinned PostgreSQL, its data is independently checked, and new
inserts -6/7 must execute the restored rules correctly.

Qualification presentation freezes the binder-owned normalized syntax together
with its matching checked routine captures. It does not mutate the parsed rule
condition or relax symbolic-rendering validation. Existing
`TestSQLRoutineFrozenPresentation` covers source immutability and rejected missing,
extra, unmarked and foreign routine captures.

Root-owned selected proof and feature smoke, with an external 900-second
deadline for each command:

```sh
make check RUN='^TestReferenceRoutine(Nesting(Diagnostics|Boundaries)|RuleQualificationPersistence)$' REFERENCE=1
make check RUN='^TestReferenceRoutine(Nesting(Diagnostics|Boundaries)|RuleQualificationPersistence)$'
make check RUN='^TestSQLRoutineFrozenPresentation$' RACE=1
```

Both selected modes are required after the remedies; discovery-only oracle
results and native pre-remedy failures are not passing verification.

### Selected-provider late child composition

`routine_late_child_review_test.go` adds `RoutineLateChildSelection` and
`RoutineLateChildUnwind`. FILTER and aggregate ORDER BY expressions containing
SQL scalar calls must continue to return `1/INT8` before and after an unrelated
SQL `sum(boolean)` overload is installed. Qualified SUM controls and nested
SQL/builtin `abs` competition distinguish actual child resolution from fallback.
The real scalar SQL winner still returns `104/INT4`.

The same late-child expressions occur in a SQL routine body and a stored view.
Native Dump/Reset/Restore must preserve both consumers and their independently
specified `1/INT8` results; native controls are a bounded phase of the shared
SQL scenario, not an invented PostgreSQL API.

Missing children require complete 42883 diagnostics, with the standard hint and
positions29/23 for FILTER/ORDER BY. A bigint FILTER requires 42804 at position29.
Each direct failure occurs at execution and each prepared failure at analysis.
Subsequent sibling SQL calls retain sequence actuals exactly once across direct,
prepared and held execution; Prepare and current-value probes do not advance it.

The selected occurrence consumes one re-entry suppression bit before its
provider binds children. Known-argument reuse remains active for the full
provider frame, preserving original recipes, coercion and window-owner evidence.
Children retain ordinary callable selection; no decorated-call bypass,
speculative prebinding or source-key identity matcher is used.

Root-owned selected proof and feature smoke, each under an external900-second
deadline, must run the same complete definitions in both modes:

```sh
make check RUN='^TestReferenceRoutineLateChild(Selection|Unwind)$' REFERENCE=1
make check RUN='^TestReferenceRoutineLateChild(Selection|Unwind)$'
```

Expected smoke observations are aggregate1/INT8, SQL104/INT4, restored body/view1,
unchanged full error fields and sibling sequence values1 through8.
