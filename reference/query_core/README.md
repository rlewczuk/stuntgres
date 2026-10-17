# reference/query_core

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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

