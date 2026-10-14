# reference/query_core

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
