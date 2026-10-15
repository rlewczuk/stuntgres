# reference/aggregates

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Correlation, covariance and regression finalizers

The untagged `correlation_regression*_test.go` definitions cover exactly `corr`,
`covar_pop`, `covar_samp`, `regr_avgx`, `regr_avgy`, `regr_count`,
`regr_intercept` and `regr_r2`. Arguments are Y then X, independently coerced to
FLOAT8; only complete non-NULL pairs contribute. Seven results are nullable
FLOAT8; `regr_count` is exact INT8 and returns nonnullable zero for no pairs.

`Values` specifies known/negative/large-offset vectors, singleton and constant
axes, excluded pairs, empty metadata, all six numeric input coercions, full-pair
DISTINCT, FILTER/ALL, grouping/HAVING, quoted/qualified names and SAME-held
unknown parameters. `Boundaries` specifies imperfect-relation tolerance,
released nonfinite branch ordering, unused-axis average overflow and count
independence from huge/nonfinite moment payloads. `Diagnostics` checks complete
portable error fields, character positions, prepare/execute staging, generated
expression and native window boundaries, argument demand and FILTER rejection.
`Lifecycle` and `FeatureSmoke` share bounded acceptance/control helpers: all
eight known results, empty zero-count, both constant axes, SAME-held freshness,
successful INSERT affected counts, native retained results and symbolic-view
Dump/Reset/Restore, then late-overflow INSERT atomicity. Inherited statistical
cancellation and replacement machinery remains in its existing scenarios.

The known `(3,1),(5,2),(7,3)` vector in the order above is
`1,4/3,2,2,5,3,1,1`. No pairs yield seven NULLs and exact count0.
Constant X makes correlation/intercept/r² NULL; varying X with constant Y makes
intercept equal Y and r²1, while correlation remains NULL. Named nonexact
mathematical witnesses use SQL intervals with
`abs(actual-expected) <= 1e-12*max(1,abs(expected))`; other raw numeric cells,
NULL, counts, metadata and diagnostics retain exact fixture comparisons.
No global approximate comparator or floating accumulation-order promise is added.

Root runs these selected commands under an external 900-second deadline,
oracle-first during discovery and both modes for completed definitions:

```sh
make check RUN='^TestReferenceCorrelationRegression(Values|Boundaries|Diagnostics|Lifecycle|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceCorrelationRegression(Values|Boundaries|Diagnostics|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceCorrelationRegressionFeatureSmoke$'
make check RUN='^TestReferenceCorrelationRegressionFeatureSmoke$' REFERENCE=1
```

The metadata helper's INT8 branch also requires the existing statistical
definitions to run in both modes after this change:
`make check RUN='^TestReferenceStatisticalAggregate'` and the same command with
`REFERENCE=1`. Native controls remain explicit native-only phases; real
PostgreSQL has independently expected SQL outcomes, not invented control APIs.

Bounded private supplements reuse the existing statistical suites:
`TestStatisticalAggregateRegressionFinalizers` checks released finalizer branch
precedence and repeatable nonmutating results;
`TestStatisticalAggregateRegressionCount` checks both descriptor owners, typed
NULL admission, exact increments above2^53 and through MaxInt64, full overflow
diagnostics/state retention, cancellation and allocation-free Advance.
`TestStatisticalAggregateSlotsAndMetadata`, `TestStatisticalAggregateViewRemapping`,
`TestStatisticalAggregateWindowSignatures` and
`TestStatisticalAggregateEagerCancellation` cover the new result owner and
nonnullable metadata, destination reconstruction, signature-only window boundary
and deterministic Advance/Finalize/prepublication cancellation.
Root's selected private proof:
`make check RUN='^TestStatisticalAggregate'`; selected race proof:
`make check RUN='^(TestStatisticalAggregateRegressionCount|TestStatisticalAggregateEagerCancellation|TestReferenceCorrelationRegressionLifecycle)$' RACE=1`.

### SQL/JSON object aggregation

The untagged `sqljson_object_aggregate_*_test.go` definitions independently cover
VALUE/colon syntax, default NULL and ABSENT policies, text transition versus
binary finalization uniqueness (including omitted members), key/value and
FORMAT conversion, exact default-family selection, compatible RETURNING types,
full diagnostics/stages/Unicode positions, parameters and before-Next metadata.
Consumers and lifecycle definitions cover grouping, HAVING, scalar wrappers,
CTE/derived input, CTAS, atomic INSERT SELECT and SAME-held error recovery.
Controls round-trip actual symbolic views, domain/typmod result wrappers and
retained cursors, then replay the actual emitted native dump on PostgreSQL.
The same physically pinned builtin-only held statement reanalyzes after native
replacement. Preheld catalog RETURNING/parameter signatures retain their exact
0A000 result/parameter-type fences; newly prepared catalog statements remain
usable across failed and canceled replacement attempts. PostgreSQL executes
the ordinary held SQL phases without inventing a host replacement API.
`CorrelatedOwnership` contrasts outer-owned, outer-grouped, genuinely inner-owned
and both mixed key/value ownership directions through scalar subqueries with
RETURNING conversion, exact cardinalities and before-Next JSONB metadata.
`ConsumerAtomicity` rejects a late row-dependent FORMAT conversion and a late
RETURNING assignment-length failure on direct and SAME-held modifying routes.
Complete diagnostics, nil result/count publication, unchanged sentinel data
and subsequent healthy execution are independently asserted.
Cancellation observes active writer and root-sort admission before cancellation,
joins each worker, checks no destination publication and recovers the same owner.
It preloads 32768 unique integer rows once and uses a single ordered,
parameter-bounded scan for direct/prepared modifying execution. This avoids
materializing the former 64³ cross product before writer admission under a loaded
full-race run, while retaining real writer/root-sort observation rather than
treating workload size or elapsed time as admission. The ordinary 30-second
fixture and helper admission/join deadlines remain unchanged. Each canceled
execution must leave the destination empty; the SAME held statement then uses
limit1, inserts exactly `{"1": 1}`, and clears that row before the next route.
PostgreSQL still requires observed server admission and the complete 57014
diagnostic, not a transport-racing context cancellation.
Boundaries preserve ordinary object/array siblings, the absent jsonb_objectagg
alias, qualified/quoted grammar and PG-positive/native-unsupported windows.

Root runs these selected definitions in both modes with an external900-second
deadline per command; none of these commands is a claim of exercised proof:

```sh
make check RUN='^TestReferenceSQLJSONObjectAggregate(Values|NullUnique|Timing|Conversion|Returning)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregate(Values|NullUnique|Timing|Conversion|Returning)$'
make check RUN='^TestReferenceSQLJSONObjectAggregate(Grammar|Diagnostics|Precedence|Parameters|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregate(Grammar|Diagnostics|Precedence|Parameters|Metadata)$'
make check RUN='^TestReferenceSQLJSONObjectAggregate(Consumers|Lifecycle|Controls|Cancellation|Boundaries)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregate(Consumers|Lifecycle|Controls|Cancellation|Boundaries)$'
make check RUN='^TestReferenceSQLJSONObjectAggregate(CorrelatedOwnership|ConsumerAtomicity)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregate(CorrelatedOwnership|ConsumerAtomicity)$'
make check RACE=1 RUN='^TestReferenceSQLJSONObjectAggregate(Lifecycle|Controls|Cancellation)$'
make check RUN='^TestSQLJSONObjectAggregate'
make check RUN='^TestReferenceSQLJSONObjectAggregateFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregateFeatureSmoke$'
```

Feature smoke exercises new pair syntax and null/unique/FORMAT/RETURNING behavior,
held recovery, atomic late failure, stored options and real emitted dump replay.
Expected observations are exact JSON/JSONB/bytea results and metadata, family-specific
22030/NULL-key diagnostics, malformed syntax and assignment-length rejection;
success is quiet package exit0 after assertions, never a custom readiness label.


### Regression moments, variance and standard deviation

The untagged `statistical_aggregate*_test.go` scenarios independently specify
`regr_slope`, `regr_sxx`, `regr_sxy`, `regr_syy` (Y first, both inputs coerced
to FLOAT8), and the six unary names `var_pop`, `var_samp`, `variance`,
`stddev_pop`, `stddev_samp`, `stddev`. Each unary name admits SMALLINT, INTEGER,
BIGINT, NUMERIC, REAL and DOUBLE PRECISION; exact inputs return NUMERIC and
floating inputs return FLOAT8. `variance` and `stddev` retain their own labels
while aliasing the sample operations.

| Scenario suffix | Independent contract |
| --- | --- |
| `Regression` | Acceptance slope/Sxx/Sxy/Syy = 2/2/4/8, negative relation, symmetric pair-NULL exclusion and full-pair DISTINCT. |
| `Overloads` | All 36 unary signatures, `(1,3)` variance/stddev and aliases, finite singleton/global-NULL/grouped-empty results, explicit floating tolerance and direct/prepared metadata before Next. |
| `Boundaries` | Empty/singleton/constant axes, numeric scale and huge intermediate cancellation, special cardinality, released FLOAT8 transition and whole-axis overflow. |
| `Binding` / `Diagnostics` | Unknown and held typed parameters, modifiers/grouping/consumers, full portable diagnostic fields and direct-execute versus prepared-prepare staging. |
| `Coercion` | Nonexact stored binary32 REAL widened without decimal reconstruction; BIGINT beyond 2^53 and large NUMERIC regression conversion/roundoff; range failure in either argument. |
| `ArgumentEvaluation` | Row-dependent division errors in either argument even when its sibling is NULL; FALSE/NULL/row-rejected FILTER companions avoid evaluation. |
| `ParserWindow` | All ten quoted-qualified PostgreSQL window results beside the explicit native unsupported-provider boundary, invalid window signatures and plain/quoted/qualified generated-column rejection. |
| `Lifecycle` / `Smoke` | SAME-held freshness, persistent source/view/result ownership, retained output/metadata, cancellation and late-error publication atomicity. |

Empty input and excluded pairs yield typed NULL; finite population singleton is
zero and sample singleton is NULL. Regression sums are zero for finite singleton
inputs, and slope is NULL when X has zero variance. Exact numeric accumulation
does not pack intermediate sums/squares; regression intentionally retains the
released FLOAT8 rounding, not exact NUMERIC regression or newer constant-axis
corrections. Native window execution and parallel grouping remain excluded.
Dump/Reset/Restore and canceled replacement witnesses are explicit native-only
phases in the shared scenarios, not fabricated PostgreSQL controls.

For every unary input type, `Overloads` independently expects the `(1,3)` vector
`var_pop,var_samp,variance,stddev_pop,stddev_samp,stddev` =
`1,2,2,1,sqrt(2),sqrt(2)` and finite singleton vector
`0,NULL,NULL,0,NULL,NULL`. NUMERIC results retain exact decimal strings and scale;
FLOAT8 acceptance values 0/1/2 remain exact. For REAL and DOUBLE PRECISION,
additional direct/prepared SQL interval witnesses require both sample stddev
spellings to lie within absolute tolerance `1e-14` of the independent binary64
sqrt(2) center `1.4142135623730951` (with FLOAT8 endpoint rounding). Both boolean
results must be true; raw aggregate rows and existing released-order/coercion
witnesses still use exact fixture comparisons.

The expanded acceptance smoke is
`make check RUN='^TestReferenceStatisticalAggregateOverloads$'` and the same
command with `REFERENCE=1`. Expect all six types to satisfy the pair, singleton,
empty/all-NULL and metadata assertions through both direct and prepared routes,
plus two true tolerance cells for each floating type.

Follow the shared oracle-first workflow above. Root runs each selected command
under an external 900-second deadline; completed definitions require both modes:

```sh
make check RUN='^TestReferenceStatisticalAggregate(Regression|Overloads|Boundaries|Binding|Diagnostics|Lifecycle|Smoke|Coercion|ArgumentEvaluation|ParserWindow)$'
make check RUN='^TestReferenceStatisticalAggregate(Regression|Overloads|Boundaries|Binding|Diagnostics|Lifecycle|Smoke|Coercion|ArgumentEvaluation|ParserWindow)$' REFERENCE=1
make check RUN='^TestStatisticalAggregate'
make check RUN='^TestNumeric(Sum|Avg)'
make check RUN='^TestStatisticalAggregate|^TestReferenceStatisticalAggregateLifecycle$' RACE=1
make check RUN='^TestReferenceStatisticalAggregateSmoke$'
make check RUN='^TestReferenceStatisticalAggregateSmoke$' REFERENCE=1
```

Private units supplement public proof: `WindowSignatures` checks result types
without registering group/window state; `NumericCoefficientReuse` checks signed
finite coefficient reuse, both exponent-alignment directions, zero/NULL scale
tracking and retained immutable results. Existing SUM/AVG units protect the
private accumulation seam. Smoke exercises selected values/metadata, changing
held inputs, native symbolic-view restoration and overflow INSERT atomicity.
Selected proof is not whole-project verification: root also runs `make quickcheck`
and post-integration `make finalcheck` under the shared guide.

### Hypothetical-set aggregates

The shared `hypothetical_*_test.go` bodies cover `rank`, `dense_rank`,
`percent_rank` and `cume_dist` WITHIN GROUP forms. Expected INT8/INT8/FLOAT8/FLOAT8
rows are independently specified: before/peer/between/after, nonstrict NULLs,
empty global versus keyed groups, mixed-direction multi-key peers, pairwise
coercion/collation, grouped direct arguments and all four full diagnostics.
Direct/prepared Query and Exec errors preserve portable fields, Hint, Detail
and Unicode character locations. Bad held parameters preserve the observed
PostgreSQL wire-Bind context on both adapters; recovery reuses the same held
object.
Precedence combines competing errors across distinct pairs for all four names:
all pair coercions precede ordering-capability lookup, and all ordering lookups
precede pair collation assignment. Exact records/stages include Unicode positions,
later incompatible/coercion errors and later unsortable JSON keys.

Execution/Consumers cover input FILTER demand, every direct child's once/group
left-to-right effects, empty-input errors, hidden HAVING/order calls, sibling
percentiles/count, grouping sets, CTE/subqueries, CTAS and INSERT atomicity.
Lifecycle checks metadata before Next, retained rows versus fresh same-held
execution and global/keyed empty output. PlainRoutes invokes actual DB/Tx plain
and Context methods, not simulated plain Conn routes. Grammar/Privileges retain
clause owners, outer ownership and later-child permissions.

Controls restores actual emitted native SQL and replays that exact dump on the
pinned PostgreSQL backend; domain/enum keys, later direct arguments and contextual
unknown casts survive descriptor replacement, column rename, same-held recovery,
independent Driver restore and stable redump. Native control phases remain
explicitly native-only. FeatureSmoke is a substantive compact SQL scenario,
including actual emitted dump replay, not a wrapper invoking baseline tests.
Contextual direct enum literals and typed NULLs reuse their owning leaf
annotation on replay; nested explicit enum casts and domain/base conversions
remain present rather than accumulating another annotation on every Restore.
Cancellation observes the exact native worker inside `openAggregateNode`
(aggregate execution before child drain), or actual PostgreSQL query activity.
The fixture retains its fixed 15-second admission deadline, rejects unpublished
output and bounds worker joining. The same four-form, two-key query uses a
64³-row active input and recovers on the same held statement with a small,
independently specified 2³-row input. Public admission does not wait for the
later aggregate sort after all input evaluation/retention; deterministic private
units separately prove cancellation inside sorting, placement, dense peers,
direct children, finalization and prepublication.

The PostgreSQL 18.0 fixture **crashed** when executing 50 direct/order pairs.
Release `nodeAgg.c` allocates `LOCAL_FCINFO(..., FUNC_MAX_ARGS)` but requests
the transition state plus all 100 SQL operands for an extra-argument finalizer;
fixed final-call storage overflow is a source-derived inference, not a verified
crash diagnosis. Public proof safely executes 49 independent pairs, checks
50-pair prepare/close on both adapters without Query or Exec, and checks
51-pair54023. Private native binder/provider execution proves all four outputs
`(1,1,0,1)` with exact INT8/INT8/FLOAT8/FLOAT8 result types at 50 keys, without
reducing the 100-operand native cap or introducing a tuple limit.

Private `TestHypotheticalAggregate...` units supplement public SQL proof with
typed peer equality, NULL/direction comparison, formulas, buffer immutability,
50-key layouts/execution, all-child structural identity/copy-on-change rewrite,
destination-owned remapping, evaluation counters and deterministic cancellation
inside sort comparison, placement, dense peer comparison, direct children,
preparation/finalization and prepublication.

Selected commands (external900-second deadline each; run both modes):

```sh
make check RUN='^TestReferenceHypotheticalAggregate(Values|Nulls|MultiKey|Types|Collations|Binding|Diagnostics|Precedence)$'
make check RUN='^TestReferenceHypotheticalAggregate(Values|Nulls|MultiKey|Types|Collations|Binding|Diagnostics|Precedence)$' REFERENCE=1
make check RUN='^TestReferenceHypotheticalAggregate(Execution|Consumers|Lifecycle|Controls|Cancellation|PlainRoutes|Grammar|Privileges)$'
make check RUN='^TestReferenceHypotheticalAggregate(Execution|Consumers|Lifecycle|Controls|Cancellation|PlainRoutes|Grammar|Privileges)$' REFERENCE=1
make check RUN='^TestHypotheticalAggregate'
make check RUN='^TestHypotheticalAggregate' RACE=1
make check RUN='^TestReferenceHypotheticalAggregateFeatureSmoke$'
make check RUN='^TestReferenceHypotheticalAggregateFeatureSmoke$' REFERENCE=1
```

The shared direct-list/finalizer/comparator cutover also requires the existing
ordered-set selected proof below in both modes, and focused ordinary aggregate
modifier Ordering/MatchingCoercions/TupleDistinct/Ownership/ConsumersAtomicity/
Lifecycle/Dependencies/FilterDemand regressions. Root runs whole-project
`make quickcheck` after edits; command readiness is not passing evidence.

### Ordered-set aggregates

The untagged `ordered_set_*_test.go` scenarios specify all seven percentile/mode
overloads independently of native output. They cover ranks and direction, shaped
fraction arrays, NULL/empty precedence, interval/IEEE interpolation, ties and all
admitted scalar/executable array carriers. Explicit PG-positive/native capability
witnesses distinguish unavailable ordering and array codecs; NAME, REGCLASS,
REGTYPE, REGROLE, REGPROC, REGPROCEDURE, REGCOLLATION and REGNAMESPACE arrays use
existing supported codecs. REGCOLLATION, REGNAMESPACE, REGCONFIG and REGDICTIONARY
also have admitted scalar discrete/mode providers. REGCONFIG/REGDICTIONARY arrays
remain lookup-only, alongside MONEY, internal CHAR, operator-alias, network and
transaction-ID array boundaries. Text-search alias witnesses use admitted zero
identities, not unsupported native name lookup or an adjacent codec expansion.
Carrier rows and actual cursor metadata
do not depend on incidental `pg_typeof` introspection; internal CHAR uses the
existing `pg_class.relkind` route and provider units. Enum raw values use the
shared exact PQ []byte/native string assertions, and PQ's unknown XID8 database
type name is asserted separately from native XID8 identity.
REGCOLLATION scalar and array metadata preserve the empty PostgreSQL adapter type
name separately from exact raw alias payloads; REGNAMESPACE names remain visible
in the [pinned lib/pq type map](https://raw.githubusercontent.com/lib/pq/v1.12.3/oid/types.go).
Diagnostics compare complete portable fields and character locations, including actual Prepare versus
parameter-execution stages. Array, all six range, all three admitted multirange,
and BIT/VARBIT ordering boundaries use existing typed table-column routes rather
than incidental explicit-cast admission. Registry-array raw extensions validate
exact native strings/PQ byte payloads on both scans and recorder streams.
Private-tree ordering uses a scenario-specific `pg_attrdef.adbin` catalog carrier,
not unsupported text input or PostgreSQL's version-dependent serialized tree.

Direct/group ownership, all-child privileges, held parameters/rights, retained
rows versus fresh executions, metadata before Next, view deparse/rebinding,
consumer atomicity, actual cancellation admission and native Dump/Reset/Restore
are separate contracts. Controls replay only actual emitted native SQL on PG;
FeatureSmoke reuses substantive scenarios, including that control body.
ActiveCancellation keeps the ordinary 30-second scenario deadline and requires
observed native aggregate-sort/worker or PostgreSQL query admission before cancel.
It seeds only 64 rows and reuses three ordered, parameter-bounded inputs: limit64
creates 262144 nonconstant sums for direct/prepared active cancellation. After
each bounded worker join, the same owner independently verifies the seed and the
same held statement uses limit2, yielding eight inputs with median4.5. Recovery
does not repeat a large successful sort merely to demonstrate connection reuse.
Controls also retains nested/direct/computed fraction-array casts and typed NULLs
through emitted-SQL replay and stable redump, without stripping real conversions.
PlainRoutes uses actual DB/Tx plain methods and separate Context companions,
including quiet held preparation, parameter errors/recovery on the same statement,
typed global/keyed empty schemas, shaped arrays, global-empty/keyed-empty INSERT
SELECT counts and late-error atomicity. Tx failures are isolated by existing
savepoints, not hidden by fresh statements or simulated Conn plain methods.
Metadata asserts native and PQ ScanTypes for all seven overloads on populated,
global-empty and grouped-empty cursors. CustomMetadata applies one complete
before-Next callback to populated, all-NULL, global-empty and keyed-empty enum/domain
scalar and array results, with exact raw adapter checks. Private units freeze one
binding catalog and assert complete result Usage/pointer, owner/namespace/principal
and associated-array leaf relationships, both comparisons' full input-collation
authority, provenance and computed origins.
Controls restores/replays NAME, symbolic registry-array, enum and domain query-view
results with retained NULL fraction cells, including REGCOLLATION/REGNAMESPACE
scalar/array values bound from stored symbolic inputs. The collation uses the
actual restored alias object; namespace output remains symbolic. No enum-array
materialization or text-search array codec is enabled.
Precedence includes standalone/INSERT VALUES and INSERT/UPDATE RETURNING failures,
complete diagnostics on direct/prepared Query/Exec, and unchanged destination rows.
DirectArguments pits admitted row-dependent input division errors against NULL or
invalid scalar/array fractions; FALSE/NULL FILTER companions preserve direct
validation and empty-input array precedence. FeatureSmoke reuses these bodies.

GrammarContexts covers the three additional WITHIN shift decisions: attaching a
clause before reducing a selected scalar projection, a completed aggregate call,
or an index function. The parser-only direct index-function witness uses qualified
unquoted `pg_catalog.overlaps`, which retains OVERLAPS rather than the index lexer's
CHECK_FUNC rewrite. Public index witnesses use PostgreSQL-required parentheses;
the broader direct native parser route does not claim PostgreSQL acceptance.
Wrong-object diagnostics and zero created indexes accompany nested/FILTER/OVER
calls and preserved WITHIN/MODE identifiers. Non-public parser units assert the
actual lexical and AST routes; no generated-state file is kept.
Qualified wrong-object diagnostics retain the complete supplied callee name.

Run each selected command with an external900-second deadline:

```sh
make check RUN='^TestReferenceOrderedSetAggregate(Values|Sorting|Types|Metadata|CustomMetadata|Arrays|Mode|Intervals|NullEmpty|Boundaries|CarrierBoundaries|CarrierArrays)$'
make check RUN='^TestReferenceOrderedSetAggregate(Values|Sorting|Types|Metadata|CustomMetadata|Arrays|Mode|Intervals|NullEmpty|Boundaries|CarrierBoundaries|CarrierArrays)$' REFERENCE=1
make check RUN='^TestReferenceOrderedSetAggregate(DirectArguments|Diagnostics|DiagnosticRoutes|Precedence|Parameters|PlainRoutes|GrammarContexts|Ownership|Collations)$'
make check RUN='^TestReferenceOrderedSetAggregate(DirectArguments|Diagnostics|DiagnosticRoutes|Precedence|Parameters|PlainRoutes|GrammarContexts|Ownership|Collations)$' REFERENCE=1
make check RUN='^TestReferenceOrderedSetAggregate(Views|Consumers|Privileges|HeldRights|Lifecycle|Cancellation|ActiveCancellation|Controls)$'
make check RUN='^TestReferenceOrderedSetAggregate(Views|Consumers|Privileges|HeldRights|Lifecycle|Cancellation|ActiveCancellation|Controls)$' REFERENCE=1
make check RUN='^TestReferenceOrderedSetAggregate(Cancellation|ActiveCancellation|Lifecycle|Controls)$' RACE=1
make check RUN='^TestOrderedSetAggregate'
make check RUN='^TestReferenceOrderedSetAggregateFeatureSmoke$'
make check RUN='^TestReferenceOrderedSetAggregateFeatureSmoke$' REFERENCE=1
```

Provider/borrowed-buffer/group-frame units exercise non-public functions only,
including terminal pre-sort decisions with a real comparator-entry probe, exact
result authority, cancellation at real preparation, sort, mode, direct, array and
percentile phases, and source release with no published rows/count. Preparation
precedes sort allocation/comparison after input evaluation and one direct
evaluation; it validates original array order once, with no fraction workspace.
Selected proof is not
whole-project verification; root also runs `make quickcheck` and post-integration
`make finalcheck` under the shared testing guide.


### MONEY and INTERVAL SUM

The untagged `money_interval_sum_*_test.go` scenarios specify ordinary
`sum(money)` and `sum(interval)` independently of native output. MONEY retains
exact signed minor units; INTERVAL retains separate calendar and time components
without AVG division. Both return typed NULL for empty/all-NULL input and
base-family nullable results with unrestricted typmod. Native windows remain
numeric-only: MONEY and INTERVAL window SUM are explicitly unsupported.

| Scenario suffix | Independent contract |
| --- | --- |
| `Values` | Exact minor units and interval components, values above 2^53, cancellation to non-NULL zero, empty/all-NULL/grouped results, FILTER/DISTINCT/local ORDER and unchanged numeric overloads. |
| `Metadata` | Before-Next labels/types/raw carriers/ScanType/nullable/unsized metadata, global and grouped empty results, unrestricted result typmod and base-domain inputs. |
| `Boundaries` | Both money endpoints and overflow directions, interval month/day/time endpoints and overflow, reserved physical tuples, infinity signs and finite overflow after infinity. |
| `Diagnostics` | Full portable diagnostic records, Unicode positions, invalid family/signature/arity, child/FILTER/nesting/context precedence, forbidden VALUES with no prefix publication and namespace resolution. |
| `QuotedFieldFallback` | Exact-case quoted SUM/Sum MONEY/INTERVAL composite-field projection, typed-NULL projection and missing scalar-signature diagnostics; quoted case variants are not aliases for the aggregate. |
| `QuotedArity` | Quoted SUM/Sum with 100/101 MONEY/INTERVAL arguments, direct/prepared complete diagnostics and Unicode positions: 100 yields missing signature, 101 yields54023 without Hint, and missing children precede the limit. |
| `Parameters` | Direct/prepared/SAME-held success, NULL, range and typed-input failure, healthy recovery and repeated positional parameters. |
| `Consumers` | Repeated SUM versus AVG, scalar/HAVING/ORDER/CTE/derived/correlated consumers, stored grouped views, INSERT SELECT cardinality and failed-write atomicity. |
| `Lifecycle` | Retained old cursor versus fresh SAME-held execution, live IntervalStyle, empty/all-NULL/range/recovery and native ordinary cancellation. |
| `Controls` | Native Dump/Reset/Restore/redump, failed/canceled replacement atomicity, retained cursor, rebound held view, independent Driver restore and PostgreSQL replay of actual native-emitted SQL; missing emitted SQL is a failure, never substituted portable setup. |
| `Authorization` | Real argument/FILTER/ORDER and hidden reads even on empty input, unrelated-column denial, public grants/revokes and held recovery. |
| `WindowBoundary` | PostgreSQL window results beside the unchanged native unsupported MONEY/INTERVAL window boundary and a positive numeric window control. |
| `FeatureSmoke` | Direct/prepared exact sums, empty and above-2^53 results, calendar distinctions, SAME-held NULL/error/recovery and complete money/interval overflow diagnostics. |

Root runs these small selectors with an external 900-second deadline:

```sh
make check RUN='^TestReferenceMoneyIntervalSum(Values|Metadata|Boundaries|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceMoneyIntervalSum(Parameters|Consumers|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferenceMoneyIntervalSum(Controls|Authorization|WindowBoundary|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceNumericSumAuthorization$' REFERENCE=1
make check RUN='^TestReferenceMoneyIntervalSum(QuotedFieldFallback|QuotedArity)$' REFERENCE=1
```

Run the same selectors without `REFERENCE=1` for selected native proof; both
modes are required after any affected change. Private provider/type units
supplement, never replace, the public scenarios:

```sh
make check RUN='^Test(MoneySumProvider|IntervalSumProvider|IntervalAggregateSumResult)'
make check RUN='^TestSumQuotedResolution$'
make check RUN='^TestReferenceMoneyIntervalSumFeatureSmoke$'
make check RUN='^TestReferenceMoneyIntervalSumFeatureSmoke$' REFERENCE=1
```

The migrated `TestReferenceNumericSumAuthorization` includes the complete
initially empty `sum_secret` assertion set alongside its populated and SAME-held
authorization phases. Public GRANT/REVOKE replaces the superseded private-ACL
SQL test. Controls replay only actual native-emitted SQL, not a portable stand-in.

### JSON object aggregates

The untagged `json_object_aggregate_*_test.go` scenarios specify the four ordinary
`json_object_agg`, `jsonb_object_agg`, `json_object_agg_strict` and
`jsonb_object_agg_strict` functions with two independent ANY inputs.
Root's pinned PostgreSQL 18.0 discovery confirmed the portable expectations;
the discovery gates are removed. Native controls require actual native-emitted
SQL, not the portable view setup used during initial oracle discovery.

| Scenario suffix | Contract |
| --- | --- |
| `Values` | Forward/reverse input order, JSON duplicate retention versus canonical JSONB latest-wins, independent operation slots, scalar key spelling/escaping/domains, recursive typed values, SQL/JSON null distinctions, no-input/all-omitted groups, FILTER and pairwise DISTINCT. PG window execution is positive evidence beside the explicit native unsupported-provider boundary. |
| `Diagnostics` | Full portable English diagnostics and Unicode positions, direct/prepared Query/Exec stages, NULL/structured keys, child/FILTER/signature/context precedence, namespaces/quoted field fallback, equality/ordering boundaries and invalid losing-duplicate conversion. |
| `Parameters` | SAME held statement with both argument slots, good/SQL NULL/NULL-key error/recovery, plain and Context routes, ANY inference order, retained cursors and live TimeZone/bytea/float/catalog spelling. |
| `ModifierParameters` | DISTINCT and matching local ORDER infer UNKNOWN key/value inputs as TEXT, including combined modifiers and both independent parameters; SAME held success/NULL/error/recovery, exact Unicode occurrence errors, later explicit casts and typed-first reuse. ORDER resolves only its first matching input; DISTINCT resolves both. |
| `Metadata` | Before-Next types/labels/aliases/ScanType/Length/DecimalSize/Nullable, actual raw carriers, populated/global-empty/grouped-empty/filtered results, NOT NULL and domain inputs, stored-view adapter metadata. |
| `Consumers` | CTE/derived/HAVING, grouped views, INSERT SELECT empty cardinality, CTAS/materialized results, late NULL-key/conversion failure atomicity, forbidden definitions/no publication, both inputs/ORDER/FILTER authorization, held revoke/regrant and precancellation. |
| `Controls` | Actual native Dump/Reset/Restore/redump, failed/canceled replacement atomicity, retained old cursor versus rebound held execution, independent restored Driver and PG replay of the actual native dump. |
| `FeatureSmoke` | Shared four-family order/null/metadata/full-key-diagnostic/held/atomicity witnesses, focused modifier-inferred value direct/prepared/SAME-held recovery, native controls and actual emitted replay. |

Root verification uses these small selected sets, each with an external
900-second deadline:

```sh
make check RUN='^TestReferenceJSONObjectAggregate(Values|Diagnostics|Parameters|Metadata)$'
make check RUN='^TestReferenceJSONObjectAggregate(Values|Diagnostics|Parameters|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceJSONObjectAggregate(Consumers|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceJSONObjectAggregate(Consumers|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$'
make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceJSONObjectAggregateModifierParameters$'
make check RUN='^TestReferenceJSONObjectAggregateModifierParameters$' REFERENCE=1
make check RUN='^TestJSONObjectAggregate'
```

Independent expected results include ordinary JSON
`{ \"b\" : 1, \"a\" : 2, \"a\" : null }` versus JSONB `{\"a\": null, \"b\": 1}`;
strict results omit the SQL NULL value. Global no-input results are SQL NULL,
while admitted all-omitted input is `{  }` for JSON or `{}` for JSONB.
NULL keys fail even with strict NULL values: JSON uses22004
`null value not allowed for object key`; JSONB uses22023
`field name must not be null`, with absent runtime Position.

PostgreSQL 18.0 binary strict also omits converted root JSON null values.
Key conversion still occurs before this omission (structured keys fail22023).
Null candidates are removed before selecting the latest surviving nonnull
duplicate, including with interleaved canonical keys; nested object/array nulls
and the JSON string `\"null\"` remain. Text JSON strict retains JSON null members.
These release-specific cases are independent oracle contracts, not native
output-derived goldens.

Modifier inference is occurrence-local, not blanket key-to-TEXT inference.
`json_object_agg('a',$1 ORDER BY $1)` treats the value as TEXT, whereas a prior
`$1::int` makes its value an integer. An explicit integer cast after modifier
resolution consumes the inferred TEXT without changing the aggregate value.
An earlier separate unresolved ANY occurrence still raises42P08 at that earlier
Unicode position. With `$1` in both inputs, ORDER chooses the first equal input
only and the second remains an occurrence mismatch; DISTINCT completes both.
These expectations were pinned on PostgreSQL18.0 before the production remedy.

The private parser/engine/sqltype `TestJSONObjectAggregate...` units supplement
public SQL assertions with descriptor generations, two-slot remapping, immutable
plans/borrowed values, group isolation, sealed/idempotent state, sticky failures,
root-only null compaction, live environments and deterministically reached
key/value/sort/compaction/output cancellation without partial query/DML publication.
The existing JSONB compaction cancellation unit also exercises its unchanged
default non-skipping behavior after the explicit root-mode signature change.
SQL/JSON `json_objectagg`, unique variants and native aggregate windows remain
outside this feature.

### JSON array aggregate oracle-first definitions

The untagged `json_array_aggregate_*_test.go` definitions specify independently
reasoned PostgreSQL 18.0 contracts for `json_agg`, `jsonb_agg`,
`json_agg_strict`, `jsonb_agg_strict` and `json_arrayagg`. Root observed both
selected oracle groups passing after correcting release-specific hypotheses.
The temporary PG-only discovery filter has been removed; the shared loop now
visits every available backend. This oracle checkpoint does not establish
native support or final dual-mode verification.

| Scenario | Contract |
| --- | --- |
| `Values` | All five names, independent operation slots, local/multikey/reverse order, DISTINCT, SQL NULL versus JSON null, strict/default omission, all-NULL versus no-input/FILTER, empty grouped/HAVING, recursive arrays/composites/domains and representative scalar conversion; actual PostgreSQL ordered-window results versus explicit existing native provider boundary. |
| `Returning` | Default JSON/JSONB/domain/unknown inference, input FORMAT, UTF8 BYTEA, NULL policies and empty output for each family, JSON intermediate to JSONB, applicable character/name/unconstrained-domain outputs and assignment length. Constrained-domain phase retains real PostgreSQL validation but independently asserts native CREATE DOMAIN0A000/no publication; it does not implement prerequisite domain constraints. |
| `Diagnostics` | Complete English SQLSTATE/portable fields, Unicode character positions, direct/prepared Query/Exec stages, arity/star/unknown, child/FILTER/nesting/namespace/order/DISTINCT, special grammar and FORMAT/ENCODING/RETURNING, malformed literal/bound JSON and JSONB overwritten-member errors. |
| `Parameters` | SAME held value/NULL/value and formatted valid/error/NULL/recovery, JSON null parameters, retained old cursor versus fresh execution, plain and Context Query/QueryRow routes, live TimeZone/bytea/float output and identifier scope. |
| `Metadata` | Before-Next populated/global-empty/grouped-empty/FILTER names, aliases, type/ScanType/Length/DecimalSize/Nullable, typmods/domains and NOT NULL source; stored-view ColumnTypes plus private fixed JSON OID/typmod/noncollation/zero-origin descriptor assertions, without adjacent executable catalogs. |
| `Consumers` | CTE/derived/HAVING, INSERT SELECT including global/grouped empty, CTAS/materialized view, late-group atomicity, forbidden contexts/no prefix effects, source/order/FILTER ACL and held revoke/grant, ordinary precancellation. |
| `Controls` | Symbolic stored view after publication, actual native Dump/Reset/Restore/redump and failed/canceled replacement, retained cursor/held refresh, independent Driver restore and actual emitted native SQL replay on PostgreSQL. |
| `FeatureSmoke` | Shared core/null/order/metadata/RETURNING/FORMAT/diagnostic/held/consumer witnesses plus controls-first actual dump replay. |

Root executes from the worktree with an external 900-second deadline:

```sh
make check RUN='^TestReferenceJSONArrayAggregate(Values|Returning|Diagnostics|Parameters|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceJSONArrayAggregate(Consumers|Controls|FeatureSmoke)$' REFERENCE=1
```

Root's first pinned oracle round failed and supplied concrete corrections:
JSON DISTINCT requires equality with no Detail/Hint; invalid RETURNING casts
have absent Position without FORMAT and FORMAT Position when explicitly supplied;
FORMAT XML rejects FORMAT; malformed literal FORMAT has no Position;
input UTF16/UTF32 raises 22023 `invalid source encoding name`, unlike output
encoding0A000; pq BYTEA Length is MaxInt64/known; DEFAULT diagnoses say
`DEFAULT expressions`. Open-cursor metadata checks now precede exhaustion,
not incidental closed-Rows behavior. Subsequent oracle rounds confirmed
literal FORMAT and input UTF16/UTF32 errors occur at Execute after successful
Prepare, with the explicitly pinned full diagnostic fields.
The oracle-discovery checkpoint exercised portable stored-view/reader behavior,
not native controls or a native dump. Final Controls and FeatureSmoke now require
the actual native dump for PostgreSQL replay and fail if that artifact is absent.
The same two selectors without `REFERENCE=1` must establish native red before
production work. Final proof repeats both selectors in both modes, and runs smoke:

```sh
make check RUN='^TestReferenceJSONArrayAggregateFeatureSmoke$'
make check RUN='^TestReferenceJSONArrayAggregateFeatureSmoke$' REFERENCE=1
```

Expected quiet success requires exact independently specified rows/types/counts,
metadata and complete diagnostics, held error recovery, unchanged destination
after failure and actual native control/replay observations.
Additional window, constrained-domain and malformed UTF8 formatted-BYTEA witnesses require fresh root
selected proof in both modes; the earlier oracle checkpoint does not cover
those changed definitions.

Private `TestJSONArrayAggregate...` units in parser/engine/sqltype supplement
the public definitions: positioned syntax and generation restrictions, sealed
per-group state/NULL presence, original generations and remapping, recursive
conversion and SQL/JSON assignment/input plans, live output environments,
symbolic RETURNING replay and deterministic cancellation reached in actual
conversion/tree/output/eager INSERT paths with no partial publication.

JSON aggregate projections expose computed/unknown Nullable metadata, not a
source-column assertion. Quoted case variants do not alias lowercase builtin
aggregates: scalar inputs retain complete absent-signature diagnostics, while
normal unqualified one-argument resolution permits an exact composite-field
projection. The independently pinned `QuotedFieldCollision` definition covers upper/mixed
variants of all four ordinary names, distinct fixed fields, NULL and bound
composites, direct/prepared routes, and scalar/missing-field absence.
The parser retains exact ordinary FunctionExpr identity without ResolutionOnly;
its CompositeCandidate diagnostic flag does not prohibit field fallback.
RETURNING BYTEA constructs a real raw-octet value, not a string-tagged substitute. Private units
read JSON through its descriptor output API, use the real BYTEA constructor,
and retain the admitted execution context for reached-path cancellation.

Stored views deliberately normalize their source schema to nullable/known in
native mode (`bindViewOutput`); pq reports false/unknown. This is asserted
separately from computed aggregate projections and retained before cursor
exhaustion, after native restore, and through an independent restored Driver.
Failed constrained declarations are witnessed by subsequent type-resolution
42704, not an unavailable pg_type query. Range conversion uses existing typed
table storage and INSERT input, not an excluded explicit range cast.

`json_arrayagg` retains its existing non-reserved column-name category, including
table declarations, column references and aliases. FORMAT/ENCODING lexer scope
begins only after the grammar confirms an actual aggregate call head; a
declaration such as `CREATE TABLE json_arrayagg (format JSON)` does not start
SQL/JSON clause parsing. Focused parser regressions retain genuine and nested
formatted calls alongside these identifier contexts.

The stored-view metadata witness uses aggregate-local `ORDER BY id` for all five
calls, so its exact array values do not invent a portable unordered scan order.

```sh
make check RUN='^TestJSONArrayAggregate'
make check RUN='^TestReferenceJSONArrayAggregate(Parameters|Consumers|Controls)$' RACE=1
make check RUN='^TestReferenceJSONArrayAggregate(Parameters|Consumers|Controls)$' REFERENCE=1 RACE=1
```

Root regenerates the parser through the selected targets. Agents have not run
generation, builds, tests or formatters; these edits are not verification.


### Bitwise aggregate selected proof

The untagged `bitwise_aggregate_*_test.go` scenarios use one shared backend
body and existing fixture raw-cell, middleware, metadata and diagnostic checks.
Independent expectations cover `bit_and`, `bit_or`, and `bit_xor` over SMALLINT,
INTEGER, BIGINT and BIT, including implicit VARBIT input and base-domain results.
Root observed all seven scenarios passing the initial pinned PostgreSQL oracle
round before removing the temporary discovery gate. The completed shared loop
now visits both available backends. That oracle checkpoint does not establish
native support or final dual-mode verification.

| Scenario | Independent contract and actual consumers |
| --- | --- |
| `Values` | All five input carriers, fold/singleton/NULL/empty, grouped bags/HAVING, ALL/FILTER/integer-key ordering, DISTINCT multiplicity and repeated operation slots; direct and prepared Query. |
| `Metadata` | Base and domain SMALLINT/INTEGER/BIGINT/BIT/VARBIT, NOT NULL input, empty global/keyed and filtered output; before-iteration names/types/ScanType/Length/DecimalSize/Nullable, including source widths5/9 versus result-1. |
| `Diagnostics` | Complete portable fields/empty fields, exact English/SQLSTATE/character positions, invalid and unknown signatures, schemas/visible and hidden domain names, ORDER BY Hint, children/FILTER/nesting/clause/window precedence; direct/prepared Exec and Query, successful width Prepare followed by SAME held failure/recovery; generated-column42803/no table creation; INSERT VALUES42803, child/signature/FILTER precedence, Unicode positions and no prefix effects/unchanged target. |
| `Boundaries` | Signed minima/maxima, B'' non-NULL,9/65-bit preservation, absorbing-state width errors and FILTER/NULL exclusion; PG positive BIT/VARBIT DISTINCT/order/window results versus explicit native equality42883/order0A000/window0A000 boundaries, including DISTINCT+order precedence. |
| `Callers` | CTE/derived queries and multi-key integer ordering, actual plain and Context direct/prepared methods, SAME held typed7/NULL/9 and input-failure recovery, destination-bound BIT/VARBIT input, INSERT SELECT affected counts/empty keyed and late-group atomicity with exact unchanged bags; live source-column ACL checks even WHERE FALSE and SAME held revoke/grant recovery. |
| `Controls` | Integer/BIT/VARBIT stored view after ordinary catalog publication; native actual Dump/Reset/Restore, failed replacement/no effects, old cursor values/metadata, held statement reuse, independent Driver restore, ordinary precancellation and actual emitted SQL replay on PG. |
| `FeatureSmoke` | Reuses all the above witnesses and consumers, controls first to admit actual emitted replay; no reduced smoke goldens. |

Root runs the same selected definitions in both modes from this checkout, with an
external900-second deadline for each invocation:

```sh
make check RUN='^TestReferenceBitwiseAggregate(Values|Metadata|Diagnostics|Boundaries)$'
make check RUN='^TestReferenceBitwiseAggregate(Callers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceBitwiseAggregate(Values|Metadata|Diagnostics|Boundaries)$'
make check REFERENCE=1 RUN='^TestReferenceBitwiseAggregate(Callers|Controls|FeatureSmoke)$'
```

Root recorded native missing-function red before authorizing production. The
additional generated-column witness independently passed pinned PostgreSQL and
exposed native0A000 before the narrow lexer classification fix. Those checkpoints
do not establish final verification: both selected modes must pass again after
affected edits, followed by whole-project `make quickcheck`.

The INSERT VALUES review remedy preserves child42703 and FILTER42804 diagnostics
before the valid-signature VALUES42803 restriction, including original Unicode
positions, empty fields and unchanged destinations. Its witnesses are shared by
Diagnostics and FeatureSmoke. Supplemental SUM analysis consumers retain their
existing signatures and ordinary resolved-source assignment diagnostics:

```sh
make check RUN='^TestSumAnalysis'
make check RUN='^TestReferenceNumericSumReview'
make check REFERENCE=1 RUN='^TestReferenceNumericSumReview'
```


Feature smoke (each with an external900-second deadline):

```sh
make check RUN='^TestReferenceBitwiseAggregateFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBitwiseAggregateFeatureSmoke$'
```

Expected quiet success requires exact folds0/15/5 and00000/11111/01010,
base nullable result metadata, preserved9/65-bit and empty strings, NULL versus
empty keyed output, full diagnostics, held argument/failure recovery, INSERT
atomicity and stored view replay/retained native readers. The initial oracle-only
round could not exercise native controls or replay native output; their final
execution is required after implementation, not replaced by PG setup.

Private provider, binding/remapping and parser provenance units share this
bounded selector. They supplement, never replace, the public acceptance proofs.
Units exercise zero-allocation integral transitions, BIT truth tables/owned
sealed payloads, distinct operation IDs, generation rebinding, ordinary invalid
state/datum errors and cancellation inside the actual BIT fold and eager runner.

```sh
make check RUN='^TestBitwiseAggregate'
make check RUN='^TestReferenceBooleanAggregate'
make check REFERENCE=1 RUN='^TestReferenceBooleanAggregate'
make check RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
make check REFERENCE=1 RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
make check RUN='^TestReferenceBitwise'
make check REFERENCE=1 RUN='^TestReferenceBitwise'
make check RACE=1 RUN='^TestReferenceBitwiseAggregate(Controls|Callers)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceBitwiseAggregate(Controls|Callers)$'
make quickcheck
```

The separate public-only source `local://bitwise-agg-feature-smoke.go` remains
in the OMP session. Root materializes `.bitwise-agg-feature-smoke/main.go`,
runs `timeout 60s go run ./.bitwise-agg-feature-smoke`, and removes only that
temporary materialization before review. Quiet exit0 requires the exact native
folds/types/width/NULL/B''/full diagnostics, actual held recovery, INSERT affected1,
integer/BIT/VARBIT stored view replacement, old live cursor metadata/values and
independent Driver restore. This program does not claim a PostgreSQL oracle.


### Range aggregate selected proof

Permanent untagged `reference/aggregates/range_aggregate_*_test.go` definitions
independently assert both operations/four overloads across int4/int8/numeric,
NULL versus empty, disconnected sets, exact numeric boundary/scale selection,
raw metadata/base-domain result identity, complete diagnostics, live table-column
authority, grouped/held/write consumers and actual native replacement/remapping.
The root observed the initial pinned oracle before removing discovery filters
and recording native missing-function red. Those observations are not final
production verification. New modifier boundary definitions separately assert
native absent comparison/group/order/window providers and real PostgreSQL success.

Root runs each bounded group in native mode and again with `REFERENCE=1`,
under an external900-second deadline per invocation:

```sh
make check RUN='^TestReferenceRangeAggregate(Values|MultirangeValues|NumericBoundaries|DiscreteBoundaries|Metadata)$'
make check RUN='^TestReferenceRangeAggregate(Diagnostics|DiagnosticPrecedence|SourceACL|ModifierBoundaries)$'
make check RUN='^TestReferenceRangeAggregate(Callers|Controls|FeatureSmoke)$'
make check RUN='^TestRange(SetUnion|SetIntersection|AggregateProvider|AggregateRemap)$'
make check RACE=1 RUN='^TestReferenceRangeAggregate(Controls|Callers)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceRangeAggregate(Controls|Callers)$'
make check RUN='^TestMultirange'
make check RUN='^TestReferenceMultirange'
make check REFERENCE=1 RUN='^TestReferenceMultirange'
```

The first three public groups require both selected modes after final edits;
quickcheck/finalcheck do not replace them. Typed private units exercise owned
normalization/intersection scratch, operation/generation fences, sealed outputs,
actual-loop cancellation and eager later-operand error/no-publication behavior.
Native Controls replays its actual emitted SQL on pinned PostgreSQL, never an
invented server Dump/Reset/Restore API.

The separate public source `local://range-agg-feature-smoke.go` stays in the OMP
session. Root materializes `.range-agg-feature-smoke/main.go`, runs
`timeout 60s go run ./.range-agg-feature-smoke` from this checkout and removes
only that materialization before review. Quiet exit0 requires independent
values/types/NULL/empty/numeric/group/write/diagnostic/authority and native
held-reader/view-replacement assertions. Root records actual argv/cwd/exit/output.
Whole-project Docker-free proof remains `make quickcheck`; post-merge finalcheck
uses its external1800-second deadline. Never use `make refcheck`.

### NULL count smoke migration

The superseded `TestNullCountSmoke` is removed. Its literal/table counts, raw
INT4 metadata and argument-count sums, nested calls, held parameter reuse and
empty-result metadata, Exec counts and complete diagnostic/nil-output contracts
remain in the shared `TestReferenceNullCount*` families. Former no-FROM rejection
cases now assert shared success; `TestReferenceNullCountInsertConsumerRoutes`
retains the original INSERT Query/Prepare routes and exact resulting state.
Malformed suffixes retain whole-input/no-prefix-effects rejection coverage,
while actual unsupported array/function-provider forms remain boundaries.

### Numeric SUM reference contract

The untagged `TestReferenceNumericSum*` family covers all six numeric overloads,
typed NULL and nullable/promoted metadata, exact integral and decimal boundaries,
same-precision ordered floating transitions, signed zero and specials, full
diagnostics, held parameter recovery, modifiers and consumers, eager write
atomicity, authorization, cancellation and stored view control round trips.
Native control phases additionally restore into an independent Driver and replay
the actual emitted SQL on PostgreSQL; they do not invent PostgreSQL controls.
Column GRANT is supported; explicit-ACL native Dump remains the control boundary.

Run the identical completed definitions in both modes, each under a900-second
deadline. The broad migration/risk selector covers every changed behavioral
definition plus retained RETURNING restrictions and shared AVG-state regressions;
the separate SSI selector covers its comment-only maintenance. Avoid interior
`$` anchors in `RUN`: make consumes them even inside shell single quotes.

```sh
make check RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)'
make check REFERENCE=1 RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)'
make check RUN='^TestReferenceNumericSumSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericSumSmoke$'
make check RUN='^TestReferenceSerializableWriteSkew$'
make check REFERENCE=1 RUN='^TestReferenceSerializableWriteSkew$'
```

Numeric SUM followed the oracle-first order above: root recorded the pinned
PostgreSQL contract before authorizing production after native behavioral red.
Discovery filters are removed from the completed definitions; never reintroduce
them or adapt independently observed expectations to native output. FLOAT4 pq
ScanType is float32 while native exports float64. A later-group overflow
opening witness uses ORDER BY aggregate result, then grouping key, to force a
blocking sort before any PostgreSQL cursor rows can be published.

The `TestReferenceNumericSumReview*` regressions preserve complete direct/prepared
Exec/Query diagnostics and Unicode positions. INSERT VALUES resolves children,
FILTER and signatures before rejecting a valid numeric SUM; a valid first VALUES
row remains unpublished when a later SUM expression fails. Domain signatures
distinguish visible unqualified names from hidden qualified names, while valid
numeric-domain input returns exact NUMERIC4.656 with promoted metadata. Missing
signatures with multiple in-call ORDER BY keys use the misplaced-ORDER-BY Hint;
ambiguity, child/FILTER precedence and successfully resolved ordered calls retain
their own contracts. Root validated these new cases on pinned PostgreSQL before
removing their discovery filter and observing native behavioral red:

```sh
make check RUN='^TestReferenceNumericSumReview'
make check REFERENCE=1 RUN='^TestReferenceNumericSumReview'
```

Both completed-mode runs require separate900-second deadlines after remedies;
neither the oracle/red checkpoint nor prior family passes establishes closure.

SUM child analysis uses the empty VALUES source namespace, not the destination
table's columns. The destination remains an inaccessible diagnostic candidate:
`sum(i)` reports42703 with the independently observed destination-column Detail,
not a successful source read. FILTER type errors beneath CAST and nested scalar
wrappers retain their own complete records instead of becoming target-assignment
errors. An unrelated BOOLEAN-to-INTEGER assignment control retains its ordinary
column-type message, rewrite/cast Hint and operand position. These additions
followed a separate pinned oracle/native-red round; unrelated scalar admission
and assignment diagnostics keep their existing boundaries.

Private units supplement the public contract with shared exact-state/AVG
regressions, zero-allocation integral transitions, same-precision/signed-zero
transitions, old/new-generation remapping, actual provider cancellation checks,
late/prepublication eager cancellation and exact column-ACL manifests:

```sh
make check RUN='^Test(NumericSum|NumericAvg)'
make check RUN='^Test(Sum|NumericSum|NumericAvg|Avg|UpdateReturningMalformedSumShape)'
make check RUN='^Test(Sum|NumericSum|UpdateReturningAggregate)'
make quickcheck
```

Focused smoke expects direct/prepared six outputs4, promoted nullable metadata,
typed empty/all-NULL results, exact INT8 max-pair NUMERIC, decimal4.656/0.0000,
ordered REAL0, negative zero, held parameter recovery, full diagnostics/no
partial effects, global INSERT affected1 versus empty keyed0, and SUM view
control replay with retained metadata. Root runs selective checks under separate
900-second deadlines after generation/formatting, and finalcheck only after merge
under one1800-second deadline.
