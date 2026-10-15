# reference/query_order

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Ntile window buckets

The eight `TestReferenceQueryWindowNtile` definitions share direct and actual
prepared SQL contracts, with independently specified rows, INT4 metadata, full
logical diagnostics and affected counts. Execution covers divisible and
nondivisible counts, larger buckets first, excess/maximal buckets, singleton,
empty/LIMIT0/EXISTS demand, NULL partitions, descending order, peer bags, named
windows and grouped/scalar consumers. ArgumentDemand distinguishes NULL retry
with the full denominator from eager or repeated bucket evaluation, checks
separate call state and exact sequence effects without fixing call scheduling.
Frames covers ROWS/RANGE/GROUPS and every exclusion, standalone eliminated
offsets and demanded frame-sensitive siblings.

Diagnostics asserts prepare/execute stages, Unicode character carets, signature
and child-analysis precedence, unpositioned22014 and portal parameter Context.
Metadata checks descriptors before iteration, raw INT4 cells and sql.NullInt64.
Lifecycle holds one statement through2/3/NULL/0/-1/2, proves late-partition INSERT
atomicity, exact affected counts and cancellation/recovery. Views covers
bucket-only and scalar-child dependency/authorization, rename/deparse replay,
pristine native Dump/Restore, source/destination Reset, retained cursors and
failed-Restore preservation. Restore intentionally rejects INSERT query sources
with0A000 before evaluating ntile; the existing live INSERT SELECT witness
separately demands late22014. A subsequent valid dump restores executable ntile
views. Native control phases do not invent PG control APIs.

```sh
make check RUN='^TestReferenceQueryWindowNtile' REFERENCE=1
make check RUN='^TestReferenceQueryWindowNtile'
make check RUN='^TestQueryWindowNtile' RACE=1
make check RUN='^TestReferenceQueryWindowNtileFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceQueryWindowNtileFeatureSmoke$'
```

Use an external900-second deadline for each selected command. FeatureSmoke
actually checks five-row delayed-NULL labels NULL,1,1,1,2; six-row B4 labels
1,1,2,2,3,4 with an empty excluded frame; same-held22014 recovery; unchanged
sentinel91 after a late-partition failure; and persisted view/control replay.
True signature/parser/state/graph/remap/unwind/cancellation units supplement
these public scenarios. Runtime state uses INT8 counters with conceptual
large-partition tests rather than allocating billions of rows.
Use `make quickcheck` for whole-project Docker-free verification.

### Window value functions

The seven `TestReferenceQueryWindowValue` definitions share direct and actual
prepared SQL bodies with independently asserted rows/types/counts/diagnostics.
They cover default-frame last_value versus whole partition; RESPECT NULLS,
current-row one-based nth ordinals, NULL/out-of-range/invalid ordinals; all
currently admitted frame/exclusion membership; selected-member demand and
volatile execution; grouping, scalar/correlated children, ORDER/DISTINCT/LIMIT;
empty/nonempty metadata and concrete types; held recovery, atomic INSERT,
cancellation, child-only dependencies/permissions and actual view replay.
Native-only phases exercise pristine Dump/Restore, source/destination Reset,
retained cursors and destination-owned domain/enum descriptors. They do not
invent a PostgreSQL Reset API.

```sh
make check RUN='^TestReferenceQueryWindowValue' REFERENCE=1
make check RUN='^TestReferenceQueryWindowValue'
make check RUN='^TestQueryWindowValue' RACE=1
make check RUN='^TestReferenceQueryWindowValueFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceQueryWindowValueFeatureSmoke$'
```

Use an external900-second deadline for each selected command. Smoke checks the
seeded default/whole-partition vectors, discontiguous EXCLUDE CURRENT ROW tuples,
SAME held ordinal100/NULL/0/-1/1 recovery, full22016/static42P20 diagnostics,
empty/nonempty INT4 metadata, volatile call versus upper-target identity, and
persisted named views/native controls. A single executing first_value(nextval)
produces1..4 and consumes4; identical SELECT labels under ORDER a or ORDER b
project that retained output and consume4. Without a retained equal whole owner,
identical calls under ORDER id execute independently, as do ORDER a,b and no
outer ORDER. These cases and genuinely different arguments consume8, with the
combined draw set1..8 exact without assuming inter-call pair scheduling.
All expectations precede backend comparison. Public OID identities retain native
int64 versus pq byte/string representations independently. Whole-project
Docker-free verification remains `make quickcheck`, not a selected proof substitute.

### RANGE window coverage

RANGE window coverage selects the complete `^TestReferenceQueryWindow` family
in both `make check RUN='^TestReferenceQueryWindow'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryWindow'`
(900 seconds each). Feature smoke uses `^TestReferenceQueryWindowRangeFeatureSmoke$` in both modes.
Named volatile-offset identity is covered by
`^TestReferenceQueryWindowRangeNamedOffsetIdentity` in both modes: same name
advances nextval once; distinct names advance it twice and give the final pair
{2,3}, without assuming evaluation order. The stored-view witness cycles offsets
1/2 to retain independent membership through repeated export/replay and native
Dump/Restore. Actual clone/remap units preserve query-local named identity while
checking shared sort groups. Successful support binding performs neither source
diagnostic formatting nor class-input diagnostic lookup.
Provider units use ` RUN='^TestWindowRange'`;
parser/stage units use `RUN='^TestQueryWindow'` in their respective packages.
Independent membership/type/English diagnostic constants are asserted before
comparison. Pinned discovery establishes that count inside EXISTS still demands
its frame, row_number/LIMIT0/unused frames eliminate runtime offsets, NULL fails
on empty input, and negative/NaN can avoid support demand on empty/all-NULL input.
Constant input division errors retain22012 before NULL frame validation.
RANGE offset diagnostics use the binder's coherent visible-type policy: visible
domains are unqualified and hidden/shadowed domains retain their required schema
spelling; descriptor SQL declarations are not diagnostic goldens. Relation ACL
failures occur at Execute in both native and PostgreSQL, including freshly
prepared SELECTs; do not broaden native Prepare authority to fit a wrong stage.
The public persistent-definition and four-row2/4/3/2 overlay witnesses migrated
from native window units; actual staged cancellation/descriptor mechanics remain
package units. Native Dump/Restore phases have no invented PostgreSQL API.

### GROUPS window coverage

GROUPS coverage uses `make check RUN='^TestReferenceQueryWindowGroups'` and
`make check REFERENCE=1 RUN='^TestReferenceQueryWindowGroups'` (900 seconds each).
Focused smoke selects `^TestReferenceQueryWindowGroupsFeatureSmoke$` in both modes.
Shared scenarios independently specify complete peer rows/types, bigint coercion,
start/end diagnostics/stages/carets, empty/all-NULL demand, volatile named identity,
authorization, prepared recovery, atomicity and cancellation. GROUPS negatives
raise22013 even for empty/all-NULL demanded frames; do not reuse RANGE's relaxed
support-demand expectations. Native-control phases use actual pristine Dump/Restore
and retained cursors, not PostgreSQL script replay as a substitute. Native units
select `^TestQueryWindowGroups` (also `RACE=1`) for safe peer endpoints, actual
offset/validation/sort/peer/group/count/output/prepublication cancellation,
immutable graph remap/identity, exact descriptors and INSERT Finish veto.
Because the shared representation also carries RANGE, final selected proof includes
`^TestReferenceQueryWindow` in both modes and `^TestQueryWindow` native units.

### ROWS window coverage

ROWS coverage selects `^TestReferenceQueryWindowRows` in both native and real
pinned modes, with `^TestReferenceQueryWindowRowsFeatureSmoke$` for focused smoke.
Independent constants cover physical endpoints/empty frames, no-order bags,
shared tie order, bigint assignment typing, exact diagnostic fields/carets,
demand on empty/all-NULL/EXISTS, named volatile identity, metadata, ACLs and write
atomicity. Actual native controls reuse pristine Dump/Restore and retained-cursor
assertions. Internal `^TestQueryWindowRows` units (also `RACE=1`) cover MaxInt64
safe physical endpoint clamping, graph/domain remap, source authority, actual
offset/validation/sort/physical-frame/count/output/prepublication cancellation,
descriptor lifetime/release and Finish veto. Do not issue overflow-inducing
MaxInt64 FOLLOWING endpoints to PostgreSQL; moderate100 endpoints are portable.
Because sorting/offset storage is shared, final selected proof includes
`^TestReferenceQueryWindow` in both modes and `^TestQueryWindow` native units.

### EXCLUDE window coverage

The shared `^TestReferenceQueryWindowExclude` family covers all four modes after
ROWS/RANGE/GROUPS membership, three-row peer count/SUM vectors, partial peers,
absent current rows, orderless/NULL/multikey partitions, empty0/NULL identities,
typed nullable SUM results, grouped/volatile argument ownership, direct/prepared
and held recovery, exact English diagnostics/Unicode carets, child-column ACLs,
late failure/write atomicity, cancellation and native persisted-view controls.
Native Dump/Restore uses a fresh public Driver and preserves exported cursors;
PostgreSQL streaming errors do not promise native eager cursor publication.
The remedy regressions distinguish retained no-exclusion transitions from real
exclusion replay, semantic SUM target equality from independent execution slots,
and member-major volatile evaluation from call-major replay.
Scalar-target regressions cover repeated DISTINCT/ORDER expressions and equal
duplicate labels, unequal child literals, equal correlated children, and
child-local versus outer-correlated coordinates. Child allocation identities are
not target semantics; outer correlation ownership is not normalized away.

Run each selected check with an external900-second deadline:

```sh
make check RUN='^TestReferenceQueryWindowExclude(NoOthersTransition|SumTargetResolution|VolatileReplaySchedule)$'
make check RUN='^TestReferenceQueryWindowExclude(NoOthersTransition|SumTargetResolution|VolatileReplaySchedule)$' REFERENCE=1
make check RUN='^TestReferenceQueryWindowExcludeScalar(DistinctOrder|DuplicateLabel|UnequalTargets|CorrelationCoordinates)$'
make check RUN='^TestReferenceQueryWindowExcludeScalar(DistinctOrder|DuplicateLabel|UnequalTargets|CorrelationCoordinates)$' REFERENCE=1
make check RUN='^TestReferenceQueryDistinctOnDuplicateSubqueryLabels$'
make check RUN='^TestReferenceQueryDistinctOnDuplicateSubqueryLabels$' REFERENCE=1
make check RUN='^TestReferenceQueryWindow'
make check RUN='^TestReferenceQueryWindow' REFERENCE=1
make check RUN='^TestReferenceRegexpSetPlacement$'
make check RUN='^TestReferenceRegexpSetPlacement$' REFERENCE=1
make check RUN='^TestQueryWindow'
make check RUN='^TestExclusion'
make check RUN='^TestQueryWindowExclude' RACE=1
make check RUN='^TestSumProviders$'
make check RUN='^TestReferenceNumericSum(Arithmetic|EmptyAndMetadata)$'
make check RUN='^TestReferenceNumericSum(Arithmetic|EmptyAndMetadata)$' REFERENCE=1
make check RUN='^TestReferenceQueryWindowExclude(FeatureSmoke|NoOthersTransition|SumTargetResolution|VolatileReplaySchedule|Scalar(DistinctOrder|DuplicateLabel|UnequalTargets|CorrelationCoordinates))$'
make check RUN='^TestReferenceQueryWindowExclude(FeatureSmoke|NoOthersTransition|SumTargetResolution|VolatileReplaySchedule|Scalar(DistinctOrder|DuplicateLabel|UnequalTargets|CorrelationCoordinates))$' REFERENCE=1
```

Smoke independently expects full-fixture CURRENT sums50/40/30, GROUP30/30/30,
TIES40/50/60 and NO OTHERS60/60/60; current-only GROUP gives count0/SUMNULL.
It also exercises metadata, held NULL/negative offset failure and recovery,
malformed/missing-bound/ACL diagnostics and actual native view Dump/Restore.
Expanded smoke starts a sequence at1: no-exclusion full frames return6/6/6 and
growing frames1/3/6, consuming only3 values (held second executions return
15/15/15 and4/9/15). Repeated SUM ORDER targets resolve legally without another
executing occurrence; volatile full TIES returns6/15/24 with last_value9.
Two independent full TIES SUM calls return unordered pairs9/12,27/30,45/48,
consuming18 values; held second pairs63/66,81/84,99/102 consume through36.
Scalar smoke expects repeated DISTINCT sum((SELECT 1)) to return one INT8 row1
and equal duplicate labels to return three(1,1) rows. Different child literals
retain42P10/42702 with exact Unicode carets and independently return three(1,2)
rows under distinct labels. Equal correlated arguments return10/20/30 or their
duplicate pairs; child-local versus outer-correlated arguments retain42P10/42702
and separately return(10,10),(10,20),(10,30). Matching ORDER occurrences stay
nonexecuting while their analyzed child graphs retain authorization/dependencies.
Internal mechanics units supplement the public contracts; quickcheck remains
whole-project Docker-free verification, not a substitute for selected real proof.
SUM is limited to existing integer/numeric/floating providers. count(expr),
FILTER, DISTINCT, call-local aggregate ORDER and MONEY/INTERVAL SUM retain their
exclusions. Value-window coverage is documented separately above.

### Set operations

The set-operation feature smoke exercises all nine operator/modifier bags and
Exec counts, NULL multiplicities, precedence/parentheses, VALUES, local/global
bounds, pairwise unknown/REAL typing, SAME held parameters, derived/scalar/EXISTS
and correlated consumers, dedup-before-INSERT assignment, width/UTF8/ORDER/lock
diagnostics, stored views, foreign Restore and retained results. It executes the
actual native Dump stream on PostgreSQL; it never substitutes reconstructed SQL.
Native controls explicitly cover invalid trailing and EOF-canceled set-view
restoration, two foreign restores, destination-owned enum/domain/array grouping
after source Reset, and retained rows/schema across destination Reset/Restore.

```sh
make check RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
make check RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$'
```

Each invocation has a900-second deadline. The complete family selector is
`RUN='^TestReferenceQuerySetOps'` in both modes; smoke does not replace it or
ordinary quickcheck/post-merge finalcheck. Lifecycle checks row-dependent late
source/assignment failures, zero-demand runtime suppression with all-arm analysis
and argument admission, ordinary native pre-cancellation, genuine observed
PostgreSQL server admission/cancellation, and SAME owner/preheld set recovery.
PostgreSQL cancellation uses the existing admitted pg_sleep helper, not a new
native function or a timing-only assertion that a fast set query was interrupted.
Pinned PostgreSQL18.0 observations ground the additional consumer rows, exact
late source/assignment diagnostics and all-arm zero-demand analysis positions.
Successful discovery does not replace completed dual-mode acceptance runs.

Stored set-view lock-boundary regression smoke covers all four modes on catalog
and sequence sources, reader-owned security_invoker/nested catalog views, and
inline-derived rejection. PostgreSQL fixture-only GRANT controls additionally
exercise ordinary SELECT-only base readers, local stored marks and revoked SELECT
under LIMIT0; native private ACL units exercise the same authority boundary
alongside shared public table/column grants. Run each command within900 seconds:

```sh
make check RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$'
make check RUN='^TestQuerySetStored(InheritedMarks|LockAuthority|ReferenceKinds|RelationModes)$'
```

The private units retain local wait/strength/diagnostic recipes, ordinary-view
inheritance, all-arm admission, sequence reference-kind checks and actual acquired
relation modes. They do not duplicate public adapter scenarios. The stored-set
barrier changes inherited marks only, never ordinary SELECT/dependency traversal.

### Grouping sets regression-first contract

The shared untagged `TestReferenceGroupingSets` families independently assert
ROLLUP/CUBE/nested expansion, ALL/DISTINCT multiplicities, placeholder NULLs and
GROUPING bits, query ownership, EXISTS, limits, full diagnostics, consumers,
metadata, statement atomicity, cancellation and stored-definition/native controls.
`TestReferenceCountExpressionCapabilityBoundaries` now treats grouping constructs
as shared success contracts; `TestReferenceQueryAggregateExclusionPrefixEffects`
uses malformed grouping suffixes while retaining the unrelated window boundary.
The pinned tests-only contract and native-red checkpoints precede production.
The original discovery filter was removed; final scenarios must visit both backends.
Final family selectors (900 seconds each):

```sh
make check REFERENCE=1 RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$'
make check RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$'
make check REFERENCE=1 RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
make check RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
```

Root removed the initial PostgreSQL-only discovery loop before native red.
Pinned discovery supplies positions, prepared phases and raw descriptor flags;
new or changed witnesses remain unobserved until their next pinned run. Full
exposed diagnostics are asserted rather than stripped. Discovery established
position8 for an ungrouped `a` projection with legal `GROUP BY ()`, and absent
positions for explicit-empty rowmarks and over-limit CUBE Cartesian products.

An empty-call syntax-position override applies only to malformed42601 calls,
not a legal empty grouping clause. Atomicity Query witnesses divide by zero on
every group, while INSERT retains late-failing expressions and unchanged-state
assertions. An original late SELECT returning from `QueryContext` successfully
does not establish completed SQL success or NULL results: pq may return a
cursor before a subsequent server error. Native eager late SELECTs separately
assert the stronger nil-cursor contract; it is not fabricated as pq parity.

Grouped-query PostgreSQL cancellation prepares before an ACCESS EXCLUSIVE peer
lock, observes the actual relation waiter/blocker and exact active query, server
cancels, joins with a deadline, asserts nil cursor/full57014, then reruns the
same held owner. This proves admitted-query cancellation, not interruption of
group execution loops. Native precancellation likewise only proves publication
and recovery. Package units cover structural AST/source traversal/whole input,
canonical expansion/raw limits, masks/outer frames/no argument evaluation,
occurrence state/modifier evaluation, actual fanout/family/finalization/merge
cancellation, column ACL manifests, physical output origins and Restore whitelist.
No production hook or pg_sleep substitute satisfies active-loop obligations.

Internal selectors for root verification:
`make check RUN='^Test(GroupingSets|QueryAggregate|AggregateModifiers)'`.
Native control branches remain in shared public scenarios. The typed lifecycle
uses admitted unconstrained domains; illegal RETURNING grouping uses the admitted
UPDATE route; INSERT RETURNING independently enforces the same clause restriction.

#### Blocking-review regression contracts

`TestReferenceGroupingSetsReview.*` supplies illegal producer-child syntax and
whole-input mutation-prefix rejection, ordinary nested unreserved function-name
ownership, typed supplied keys, compound parameter keys, equal-value distinct
parameter identities, held non-NULL/NULL reuse, grouped INSERT counts and
NOT NULL atomicity with same-owner held recovery. Exact bags, types and metadata
are independent expectations. Root's pinned Review checkpoint passed these new
diagnostics, character positions, supplied-parameter rows and metadata.

The temporary new-family-only PG discovery helper was removed and every caller
migrated to `groupingSetsScenario` before native red. No grouping discovery filter
remains. Native red found the producer-child/function admission bug; parameter,
identity and INSERT scenarios passed. The grammar remedy uses expression-only
ROLLUP/CUBE units, preserving implicit sublists and recursive GROUPING SETS.
Root must freshly validate the completed definitions in both modes, under a
separate900-second deadline per invocation:

```sh
make check REFERENCE=1 RUN='^TestReferenceGroupingSetsReview.*$'
# Both backends execute shared definitions; no temporary discovery helper.
make check RUN='^TestReferenceGroupingSetsReview.*$'
```

Final complete-family selectors above include every changed/new definition.
The review smoke adds a valid composite producer, forbidden empty/SETS children
with prefix atomicity, supplied held7/NULL/7 masks and metadata, and a prepared
parameterized grouped INSERT. Ordinary nested cube/rollup calls retain the
existing native generic-call0A000 boundary versus PostgreSQL42883 with Hint;
this does not admit new scalar functions or syntactically reject their names.
Parser units supplement public contracts with positioned expression-unit ASTs,
legal nested SETS/producers and bare names, full malformed-child diagnostics,
whole-input zero-statement publication and inner ordinary-call capability ownership.
These new units and the final grammar remedy require root verification; the
historical oracle pass/native red are not final passing evidence.

### DISTINCT ON reference contracts and feature smoke

The untagged `reference/query_order/query_distinct_on_*_test.go` family shares fixture/public
API bodies in both modes. Latest-row/NULL grouping, visible/hidden metadata,
direct/fresh/held routes, resolved-prefix diagnostics, typed hidden keys,
group/window/FETCH/subquery consumers, INSERT source typing, physical authority,
real cancellation and actual ON view/native-control replay have independent
expectations. No oracle-discovery backend filter remains.

The pinned18.0 oracle distinguishes analysis target identity from physical output
sharing: ON(y) random x/y yields four shared draws and continuation5; ON(1,2)
requires eight interleaved draws and continuation9. Cheap immutable losing
payloads still fail22012. The unrelated volatile sequence payload executes only
for winner2, returns INT8 payload1 and leaves nextval2 after independent resequencing.
Do not replace these expectations with ordinary DISTINCT's eager paired golden.

Run the complete family and executable feature smoke in both modes, each command
under its own900-second deadline:

```sh
timeout 900s make check RUN='^TestReferenceQueryDistinctOn'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOn'
timeout 900s make check RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
```

Smoke requires seven events→ids3/5/7, hidden payload metadata, NULL grouping,
empty/nil/repeated held frames, ordinal/duplicate ORDER first-policy resolution,
bounds→id5, complete42P10 original ON caret, INSERT count3/exact winners and actual
ON view export/native successful/invalid/canceled Restore with sentinel17 and
retained nonempty results. Broader typed/window/volatile/ACL/cancellation witnesses
live in the complete family. Internal key/cache/collision/pin/manifest probes
supplement these public scenarios; they are not substitute PostgreSQL evidence.
