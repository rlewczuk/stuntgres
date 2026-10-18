# reference/type_resolution

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Complete comparison compatibility

`comparison-ledger.md` owns the independent REL_18_6 signatures, direct implicit
coercions, concrete/structural inventory and observed oracle provenance.
`comparison_facts_test.go` enumerates 87 concrete types and classifies all 52,983
ordered pair/spelling cells. The matrix executes all 2,524 admitted and 24
partially denied cells in full and witnesses each of the 7,205 empty pairs (no
released operator) once; see "Negative-space and bound-product sampling" below.
Predicate shards classify 658,503 triples and execute 671 ordering-admitted
triples across six BETWEEN forms and 27 value/NULL assignments: 668 ordinary
triples plus three demand-failing leaf-array triples. The latter independently
require 198 strict-NULL successes and 288 demand errors.
DISTINCT, null/truth, consumer, identity/demand and lifecycle definitions use the
same public dual-mode fixture, not native-derived goldens.

Run each suffix `Numeric`, `Strings`, `Temporal`, `Identifiers`, `BinaryNetwork`,
`NegativeSpace`, `Ranges`, `Arrays` separately in both modes:

```sh
make check RUN='^TestReferenceComparisonMatrixNumeric$' REFERENCE=1
make check RUN='^TestReferenceComparisonMatrixNumeric$'
make check RUN='^TestReferenceComparisonBetweenNumeric$' REFERENCE=1
make check RUN='^TestReferenceComparisonBetweenNumeric$'
make check RUN='^TestReferenceComparisonPredicateErrorsNumeric$' REFERENCE=1
make check RUN='^TestReferenceComparisonPredicateErrorsNumeric$'
make check RUN='^TestReferenceComparisonPredicateErrorsNumeric$' RACE=1
```

Use the corresponding exact suffix for every shard; do not substitute a
representative family. Other bounded selectors cover the remaining contracts:

```sh
make check RUN='^TestReferenceComparison(CoreConversions|UnknownParameters|CompositeDemand|RowEligibility|RecordImageBoundary|StructuralConstruction|PredicateSyntax)$' REFERENCE=1
make check RUN='^TestReferenceComparison(Boundaries|Structural|DistinctParameters|DomainArrayRoutes|CycleRecordArray)$' REFERENCE=1
make check RUN='^TestReferenceComparison(NestedCapabilities|CatalogConversions|NodeTreeLeaf|RecordUnknownFormals|DiagnosticVisibility|CompositeSortConsumer|DeferredRecord)$' REFERENCE=1
make check RUN='^TestReferenceComparison(Distinct|NullTruth|NullOnlyRoutes)$' REFERENCE=1
make check RUN='^TestReferenceComparison(Consumers|ConsumerReductions|BatchSelection)$' REFERENCE=1
make check RUN='^TestReferenceComparison(PreparedLifecycle|StoredControls|StoredConstructor(Lifecycle|Replay|Dependencies)?|AnyarrayCatalog|InstalledQueryInt)$' REFERENCE=1
make check RUN='^TestReferenceComparison(PreparedLifecycle|StoredConstructor(Lifecycle|Replay|Dependencies))$' RACE=1
```

Each changed definition also requires the identical selector without REFERENCE.
Use an external900-second deadline per command. Full quickcheck and post-integration
finalcheck are separate gates. The stored constructor witnesses distinguish erased
outer identities from actual member/nested/whole-value/unused-CTE dependencies,
member VARCHAR typmods, enum rename and replay of actual native-emitted SQL.
Existing enum-DROP and absent codec boundaries are independently asserted, not
implemented as comparison prerequisites.

Standalone smoke uses the actual built `cmd/stuntgres` and a temporary raw/public
client, not a Go test. Its `raw`, `public`, `feature` entrypoints assert explicit
1043 metadata and reused counts, cross-family equality/order, NULL-aware
predicates, composite/row semantics, stored member coercions after outer-type
drop, held current data, node-tree leaves, canonical diagnostics and VARCHAR
window peer equality. The sessions owner documents launch/teardown; the ledger
records evidence provenance.

#### Test-performance repair: finite values and CHAR source capture

Change note: this is a nonfunctional repair to the comparison tests and their
local SQL builders, not a production parser, coercion, catalog or PostgreSQL
algorithm optimization. Package-wide attribution covered all 220 native-executable
definitions (223 inventoried; three require the reference tag), rather than
assuming the new matrix was the only offender.

Two measured mechanisms accounted for the primary costs:

- `comparisonFiniteValue` wrapped even ordinary stored columns in redundant
  `CASE WHEN id=0 THEN NULL ELSE column END`. The fixture already stores NULL at
  id0. Repeating those CASE trees in exhaustive SQL drove parser stack growth,
  CASE binding and allocation/GC, especially in `PredicateErrors`. The builder
  now returns ordinary columns directly. `NullTruth` locates the actual operand
  with `strings.TrimPrefix(value, "(")` for diagnostic positions; it no longer
  assumes every operand starts with CASE.
- After that repair alone, `BetweenStrings` improved only 0.9%, insufficient
  evidence beyond noise. Its real CHAR operands repeatedly opened scalar
  `pg_attribute`/`pg_class` queries across projections, BETWEEN endpoints and
  27 rows. Profiles attributed this to `captureCatalogSource`,
  `openQueryProjectStage` and `pgAttributeRow`, not fixture setup. The final
  repair captures those same catalog values in a singleton derived FROM source
  for each Strings projection needing scalar CHAR.

`comparisonFiniteValue` remains the unbound entry into the single implementation
`comparisonFiniteValueFrom`. Outer NULL gating is retained for arrays (a NULL
array is not `ARRAY[NULL]`), CHAR, `pg_node_tree`, VOID, and node-peer fixed-id1
strings. BPCHAR and BIT retain CASE too: their declared default typmods (5 and 1)
are not interchangeable with CASE's unknown-NULL modifier -1; recursive BPCHAR
leaves retain this exception. The actual conbin source, `pg_sleep(0)`, ordinary
node-peer id1 source and analysis/demand queries are not replaced or cached.

`comparisonFiniteCharSource` still selects `attgenerated` for the real id column
and `relkind` for the real table, yielding CHAR `""`/`"r"`. Its scalar subqueries
remain inside a singleton SELECT, preserving scalar zero/multiple-row behavior
without multiplying the 27 assignments. Only `comparisonBetween`'s Strings
triples containing scalar CHAR bind to `comparison_char_source.low/high`;
CHAR paired with a node tree still uses low for both non-NULL ids. There is no
literal substitution, Go prefetch, cross-query cache or production change.
The native derived stage retains execution-private source rows; PostgreSQL may
flatten the SQL, so no PostgreSQL evaluation-count guarantee is asserted.

The projection limit remains **32 expressions**. Before **each form** is added,
an empty batch resets to the plain FROM; a bound-CHAR form adds the singleton
source. Later ordinary forms keep that batch's source. Automatic flush can split
a six-form triple: the following empty batch must acquire its own source before
its next bound expression. Final partial flush uses its accumulated source;
ordinary-only batches have no extra join. Each direct/held execution owns its
own source; nothing survives a query, backend or scenario boundary.

The finite inventory is unchanged: 87 types, 7,569 ordered pairs, 52,983
pair/spelling cells (2,524 admitted / 50,459 denied), 658,503 classified triples
and 4,103 ordering-admitted triples. Execution samples only the redundant
dimensions described in the next section. The 668 executed ordinary triples
exercise six forms and all 27 assignments (108,216 expression/assignment
outcomes); the three exceptional triples retain 486 assignments, with 198
strict-NULL successes and 288 Execute-demand errors. All nine pair assignments,
WHERE/empty metadata,
populated/all-NULL/empty analysis failures, successful Prepare versus demanded
Execute failures, SQLSTATE/English diagnostics/positions, raw rows/arguments and
applicable structural/consumer/parameter/lifecycle routes remain independently
asserted. Successful checkpoints still run direct once and execute the **same
held statement twice**; analysis denials run direct at execute stage and genuinely
fail Prepare at prepare stage. Fresh case/engine ownership, serialized before/after
resets, 30-second scenario / 15-second reset bounds and closed/joined resources
are unchanged.
Regtype's existing reciprocal narrowing and replay batching, all 40 mixed
creator/order cases, checkpoints, failed late Restore atomicity, second Restore,
fresh defaults and actual emitted replay are untouched.

##### Negative-space and bound-product sampling

The exhaustive negative space made this package the long pole of quickcheck
(220s native). 7,205 of the 7,569 ordered pairs are *empty*: no released
operator in any of the six slots. Each empty pair ran 126 analysis queries that
differed only by operator spelling, a WHERE filter that cannot suppress an
analysis error, the route, or the DISTINCT/BETWEEN form. BETWEEN additionally
executed the full lower × upper product per left identity.

`comparison_negative_plan_test.go` now owns a pure, DB-free witness plan. An
empty pair's rotating ordinal (its index among the left's empty rights, offset
by the left index) selects:

- Matrix: one spelling and one of the populated/all-NULL/empty filters, on
  both direct execution and failed Prepare.
- PredicateErrors: one DISTINCT form and one BETWEEN form at **each** endpoint,
  each with one filter and one route. Endpoint priority (an unorderable x/x
  failure wins at the upper endpoint), canonical operators and NOT positions
  are unchanged.

Before any fixture is allocated, every family asserts that its plan covers all
spelling × filter, DISTINCT form × filter × route and BETWEEN form × endpoint ×
filter × route combinations, so a rotation gap fails loudly. Admitted cells and
partially admitted pairs (XID/integer, XID, CID, ACLITEM) still execute every
spelling, form, filter, endpoint and route. Every empty pair keeps its own
witnesses; no pair is dropped.

BETWEEN lowers to independent `x >= lower` and `x <= upper` comparisons. Per
left, execution pairs each ordering-eligible identity with itself and with the
next eligible identity (deduplicated), so every eligible identity is both a
lower and an upper bound, alone and mixed: 671 of 4,103 admitted triples.

Matrix admitted cells batch their nine-row results and empty BOOL metadata into
the existing 32-expression projections; the WHERE-only-true and leaf-demand
routes stay per cell. Matrix and Consumers bind catalog CHAR values once per
query through `comparisonFiniteCharSource`, as BETWEEN already did. Consumers
alternate the released grammar synonyms ANY/SOME by cell, while ALL always runs.
Matrix, PredicateErrors and BETWEEN each own one scenario per left identity;
the former eight-right PredicateErrors windows and per-lower BETWEEN fixtures
are gone. Consumers keeps its two-right windows: PostgreSQL re-runs the catalog
node-tree source inside every correlated route and needs that bound. The duplicate
`TestReferenceRegtypeFeatureSmoke` aggregator, which re-ran 26 definitions of
this package, was removed; select `^TestReferenceRegtype` instead.

The whole package then ran in 44s native on a lightly loaded 32-CPU host, versus
220s before. The remaining costs are per-query engine work (goyacc union copies
in `Parse`, `inheritanceChildren` map scans), not fixture setup.

##### Matched observations, not a production performance claim

Before/after used base `d61c345b81d73af8e4511609a51ecf41953e6641`,
Go 1.27.1 linux/amd64 on the same 32-logical-CPU AMD host, `GOMAXPROCS=2`,
otherwise default Go runtime settings, warm compiler/image caches, and one fresh
process with `-test.count=1` per sample. A temporary Go overlay restored only the
two old helper files for baseline builds; production and runner sources were
identical. Root checks did not overlap, but the shared host had external
contention. The following are three-sample **wall-second medians [min–max]**
from compiled test binaries, excluding generation/compilation:

| `TestReferenceComparison` suffix | Native before | Native after | Native + pinned 18.6 before | Native + pinned 18.6 after |
|---|---:|---:|---:|---:|
| PredicateErrors | 127.678 [127.669–127.914] | 97.871 [92.879–128.055] | 166.423 [156.919–180.722] | 118.388 [118.120–130.681] |
| BetweenStrings | 30.850 [30.810–30.893] | 2.743 [2.532–3.153] | 93.316 [86.984–93.645] | 35.737 [35.647–39.082] |
| MatrixArrays | 24.603 [24.184–26.122] | 18.246 [17.045–18.522] | 32.623 [30.740–50.559] | 23.462 [22.815–35.227] |
| Consumers | 15.848 [15.790–16.794] | 13.694 [13.599–15.967] | 132.795 [127.868–169.521] | 126.440 [120.967–126.579] |
| BetweenNumeric | 4.715 [4.695–4.717] | 3.109 [2.932–3.654] | 63.299 [60.067–63.334] | 12.692 [12.490–13.105] |
| BetweenIdentifiers | 11.529 [11.510–11.549] | 7.666 [7.138–8.502] | 46.026 [43.866–46.550] | 29.398 [29.115–30.028] |

Native median reductions are respectively 23.3%, 91.1%, 25.8%, 13.6%, 34.1%
and 33.5%; overlapping ranges (notably PredicateErrors/Consumers) expose host
noise rather than a promise about every invocation. A suspected BetweenArrays
regression in noninterleaved samples was noise: seven interleaved pairs yielded
before 2.704–2.850s versus after 2.584–2.629s. Already-fast callers are correctness
coverage, not evidence of a useful speedup.

Selected native race medians [ranges] were PredicateErrors
789.784 [768.013–860.071] → 573.207 [558.126–611.752]s and BetweenStrings
120.391 [117.158–121.331] → 13.591 [12.448–13.649]s.
DistinctParameters was 6.511 [6.339–12.912] → 6.300 [6.237–7.420]s:
no meaningful improvement claim. Race instrumentation is a separate workload,
not a multiplier to apply to normal execution.

Matched sampled allocation-space profiles corroborated the mechanisms:
PredicateErrors total 150.95 → 114.00 GiB, parser `Parse` flat 84.66 → 55.33 GiB,
`bindCase` cumulative 14.48 → 6.21 GiB; BetweenStrings total 24,981.89 MiB →
approximately 4.62 GiB, `pgAttributeRow` 19,555.48 MiB → approximately 0.79 GiB.
Cumulative costs overlap. These are sampled whole-workload allocated bytes,
**not B/op or live heap**. Sampled CPU seconds were 152.13 → 110.80 and
35.45 → 3.83 respectively; profiled runs are not the unprofiled timing samples.

One matched compiled whole-package native run per version was 297.298 →
190.779s, not three package samples. The earlier quickcheck measurement
(type_resolution 340.444s, total wall 381.15s) used an older base/default
parallelism and is not a matched speedup. `make check` additionally regenerates
parser/Unicode data and compiles; warm caches do not eliminate those phases.
Real mode includes **both native and PostgreSQL**, container startup/configuration,
serial resets and teardown; a baseline no-match startup was 1.453s, not an exact
subtractable constant. Process user/sys measurements omit PostgreSQL server CPU.
Neither the combined-mode table nor the native repair establishes a PostgreSQL
algorithm or overall production-performance improvement.

##### Exact affected-helper proof and public smoke

The changed finite-value helper has **28 top-level callers**: eight Matrix,
eight Between, eight PredicateErrors, Consumers, Distinct, DistinctParameters
and NullTruth. Use these **25 exact bounded selector groups**, not one
representative shard or a whole-package selector:

```text
^TestReferenceComparisonMatrixNumeric$
^TestReferenceComparisonMatrixStrings$
^TestReferenceComparisonMatrixTemporal$
^TestReferenceComparisonMatrixIdentifiers$
^TestReferenceComparisonMatrixBinaryNetwork$
^TestReferenceComparisonMatrixNegativeSpace$
^TestReferenceComparisonMatrixRanges$
^TestReferenceComparisonMatrixArrays$
^TestReferenceComparisonBetweenNumeric$
^TestReferenceComparisonBetweenStrings$
^TestReferenceComparisonBetweenIdentifiers$
^TestReferenceComparisonBetweenArrays$
^TestReferenceComparisonBetween(Temporal|BinaryNetwork|NegativeSpace|Ranges)$
^TestReferenceComparisonConsumers$
^TestReferenceComparisonDistinct$
^TestReferenceComparisonDistinctParameters$
^TestReferenceComparisonNullTruth$
^TestReferenceComparisonPredicateErrorsNumeric$
^TestReferenceComparisonPredicateErrorsStrings$
^TestReferenceComparisonPredicateErrorsTemporal$
^TestReferenceComparisonPredicateErrorsIdentifiers$
^TestReferenceComparisonPredicateErrorsBinaryNetwork$
^TestReferenceComparisonPredicateErrorsNegativeSpace$
^TestReferenceComparisonPredicateErrorsRanges$
^TestReferenceComparisonPredicateErrorsArrays$
```

For **each** selector set `S` to that exact line and run, oracle first:

```sh
GOMAXPROCS=2 make check RUN="$S" REFERENCE=1
GOMAXPROCS=2 make check RUN="$S"
```

After all 25 selector pairs, run each of the eight denial shards in native race
mode, plus the two retained mutable/lifecycle/capture callers (ten commands):

```sh
for F in Numeric Strings Temporal Identifiers BinaryNetwork NegativeSpace Ranges Arrays; do
    GOMAXPROCS=2 make check RUN="^TestReferenceComparisonPredicateErrors${F}$" RACE=1
done
GOMAXPROCS=2 make check RUN='^TestReferenceComparisonDistinctParameters$' RACE=1
GOMAXPROCS=2 make check RUN='^TestReferenceComparisonBetweenStrings$' RACE=1
```

The obsolete unsharded definition has no compatibility alias. Apply an external
900-second deadline to each invocation. Real mode uses the fixture's pinned
18.6 image and UTF8/C policy; Docker failures must remain visible. These commands
are the required final proof recipe, **not a claim that final selected make
checks, quickcheck, review or post-integration finalcheck have passed**.

The expanded actual-helper smoke and the former denial-window smoke were rerun
together on the b1 base: pinned-18.6 first (266.894s), then native (190.359s),
both passing. Temporary bridges and the timing probe were removed. Earlier
source-only smoke observations were 36.478s / 2.781s on the older base and are
not substituted for this fresh execution. The combined smoke exercised the
actual finite helpers through `fixture.RunStep`/checkpoint routes: nine INT4
pairs, 27 triples, independent analysis and leaf-demand failures, NULL-array
versus array-of-NULL, BPCHAR/BIT and node-peer exceptions, raw CHAR `""`/`"r"`
with CHAR metadata, bound CHAR NULL/low/high and node-peer rows, all six forms
over 27 independent SQL-three-valued outcomes, WHERE/empty BOOL metadata, and
the actual full `comparisonBetween(t, "Strings")` to cross mixed 32-column
boundaries. Expected truth uses separate endpoint comparisons and SQL3VL:
false dominates NULL in AND, true dominates NULL in OR, NOT preserves NULL.
It is not an assertion that any-NULL always means NULL.

To reproduce the helper-specific smoke, use a temporary untagged
`reference/type_resolution/performance_public_smoke_test.go` in
`package reference_test`, importing `testing` and `stuntgres/reference/fixture`,
with `TestReferenceTypeResolutionPerformancePublicSmoke(t *testing.T)`.
Inside it, find type indices by `comparisonConcrete[i].name`, create scenarios
with `comparisonFiniteScenario`, and send actual helper-generated SQL through
`comparisonCheckpointRoutes(t,p,b,fixture.Select(...))`. In the CHAR scenario
use `comparisonFiniteValueFrom(charIndex,"x",-1,"comparison_char_source")` and
`" FROM comparison_values x CROSS JOIN "+comparisonFiniteCharSource`:

- Project source low/high: expect one `("", "r")` row, CHAR/CHAR.
- Project `x.id`, bound `IS NULL`, bound `= source.low`, bound `= source.high`,
  ordered by id: expect `(0,true,NULL,NULL)`, `(1,false,true,false)`,
  `(2,false,false,true)`, with INT4/BOOL/BOOL/BOOL metadata.
- With node-tree peer expect `(0,true,NULL)`, `(1,false,true)`, `(2,false,true)`
  for id/IS NULL/equality to low. Do not replace the node's real conbin source.
- Cross x/l/h with the same singleton source, build each of
  `comparisonBetweenForms` using actual bound helpers, and independently assert
  all 27 rows, true-only WHERE rows and empty BOOL metadata. Include
  x=1/lo=2/hi=0: ordinary BETWEEN is false, not NULL.
- Retain the unbound INT4 pair/triple, analysis, leaf-demand and special-source
  controls listed above; finish with
  `t.Run("ActualBetweenStrings", func(t *testing.T) { comparisonBetween(t,"Strings") })`
  rather than copying the batching algorithm.

The helpers live in `_test.go` and require `testing.T`; an ordinary `go run`
client cannot import them. This bridge is actual public SQL exercised by a
temporary test client, not the standalone command smoke described above.
From repository root, with external 900-second deadlines:

```sh
go test -c -tags reference -o /tmp/type-resolution-public-smoke.test ./reference/type_resolution
(cd reference/type_resolution && GOMAXPROCS=2 /tmp/type-resolution-public-smoke.test -test.run '^TestReferenceTypeResolutionPerformancePublicSmoke$' -test.count=1 -test.timeout=0 -reference=true)
(cd reference/type_resolution && GOMAXPROCS=2 /tmp/type-resolution-public-smoke.test -test.run '^TestReferenceTypeResolutionPerformancePublicSmoke$' -test.count=1 -test.timeout=0 -reference=false)
```

Expect exit 0 only after independently specified rows, raw/metadata, diagnostic
stages and same-held reuse assertions pass. Remove the temporary bridge and
binary before final gates. Keep the normal full quickcheck (900 seconds) and
post-integration finalcheck (1800 seconds) separate from these selected proofs.

### Regcollation and regnamespace

The shared scenarios independently specify symbolic/numeric identity, SQL
identifier folding/quoting, unsigned boundaries, NULL and declared metadata.
Namespace input is a single schema identifier, independent of search path and
USAGE. Collation identity uses encoding-aware lookup and visibility, including
shadowing, implicit pg_catalog, explicit qualification and schema permission
failures; identity lookup does not grant ICU/libc comparison execution.
Diagnostic scenarios cover direct/prepared literals, direct/held parameters and
direct/held runtime text casts with full English diagnostics and character
positions. Consumer lifecycle scenarios cover held constants, eager retained
spelling, rename/move, stored dependencies versus opaque rows and supported
domain/array values.

The initial pinned run observed an empty pq `DatabaseTypeName` for REGCOLLATION,
while REGNAMESPACE retains its name. Tests preserve this transport distinction
separately from each alias's string/native and byte/pq raw codec contract.
DROP/recreate invalidates held literals for both aliases: their next execution
must identify the fresh named referents, not the old allocator OIDs.
The next pinned discovery run also observed held source reanalysis after catalog
rename: the old collation spelling fails with42704 rather than retaining success.
This is distinct from the stable symbolic presentation of already-returned Rows.
Retained-row mutation uses a second connection because pq cannot process another
query while the first connection has an open result stream.

Root's pinned oracle discovery passed the shared SQL definitions, and root then
observed the native failing regression before production changes. No discovery
backend filter or native-control deferral remains. Completed unfiltered selected
proof is required in both modes, including native controls and actual emitted
PostgreSQL replay; discovery alone does not prove those phases.
Completed native phases exercise real Dump/Reset/Restore with allocator
displacement, class-aware identity witnesses and stable redump; PostgreSQL
replays the actual emitted native stream in the same shared scenario.

The opaque-row DROP witness checks the original OID before and after deletion.
Before stable-redump controls, that row is replaced with the independently
unknown max sentinel4294967295; both spelling and bits are checked on source,
restored native values and actual PostgreSQL replay. A dropped allocator OID
can legitimately collide with a reminted live referent, changing symbolic
spelling while retaining bits; tests do not reserve IDs or suppress live output.

The bounded ArrayDependencies scenario independently requires opaque
REGCOLLATION[]/REGNAMESPACE[] DEFAULT and VIEW Const payloads not to block DROP.
It checks retained OIDs and fresh default execution after referent deletion.
Root's pinned run passed this witness and quick mode exposed the native default
provider rejection before the remedy. Dependency filtering follows exact Const
shape; existing REGTYPE recursive dependency behavior remains unchanged.

The bounded ObjectBoundaryStringCasts and ObjectBoundaryByteParameters scenarios
cover both aliases through the same direct/prepared SQL and direct/held parameter
paths. Independent expectations include TEXT/BPCHAR/VARCHAR/NAME IO, character
padding and explicit truncation, NULL propagation, Boolean/unrelated-alias
rejection, and valid -> nonnil-empty error -> nil/typed-nil NULL -> valid held
recovery. Root's pinned PostgreSQL-only discovery passed these definitions;
native-before rejected BPCHAR/NAME casts and treated typed-nil bytes as invalid
name syntax. Completed post-remedy proof is still required in both modes.

MixedRoutinePersistence exercises the shared routine-container consumer with
REGPROCEDURE, REGCOLLATION and REGNAMESPACE in composites and unconstrained
composite domains. Executable composite arrays share the logical codecs. Independent named-OID
equalities cover saved rows, a constant view and replayed defaults. Native replay
uses a fresh target's retained TEMP namespace/relation/rowtype allocations to
displace both imported object classes; permanent spacers alone are replaced.
Failure diagnostics report both original/restored OID pairs without weakening
the requirement that both differ.
PostgreSQL executes the actual emitted native stream. Full dual-mode selection
is required for replay; a postgres-only selector cannot supply that native stream.
The rebase union and this witness require fresh proof and independent review.
The sql-contract subtest independently exercises pinned SQL expectations without
native controls; logical-replay separately requires the actual emitted stream.
Routine spelling is projected as TEXT while OID equality still checks identity.


Use an external 900-second deadline per invocation, oracle first:

```sh
make check RUN='^TestReferenceReg(Collation|Namespace)(Input|Visibility|Diagnostics|Prepared|StoredControls|ArrayDependencies)$' REFERENCE=1
make check RUN='^TestReferenceReg(Collation|Namespace)(Input|Visibility|Diagnostics|Prepared|StoredControls|ArrayDependencies)$'
make check RUN='^TestReferenceRegObjectBoundary(StringCasts|ByteParameters)$' REFERENCE=1
make check RUN='^TestReferenceRegObjectBoundary(StringCasts|ByteParameters)$'
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence/postgres/sql-contract$' REFERENCE=1
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence$' REFERENCE=1
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence$'
make check RUN='^TestReferenceRegCollationFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceRegCollationFeatureSmoke$'
make check RUN='^Test(RegObjectAlias(Datum|Codec|CapturedAuthority|Persistence|ComparisonBoundary)|AnyValueProviderDatums|MaterializedViewRegtypePresentation|PGDependRegtypeValueReferences|PGDependExpressionReferences)$'
```

The existing PGDependExpressionReferences units cover structural domain literals
whose declared Usage supplies the datatype dependency before a datum is
materialized. Alias value traversal must add no referent edge for that absent
payload while preserving the declared domain type edge.

Feature smoke reuses all eleven independent scenario bodies. Expected completed
observations include C950, namespace11, dash/unknown/NULL output, quoted-dot
namespace acceptance, shadow qualification, exact missing/ACL diagnostics,
held recovery including typed-nil bytes, string-category sizing and cast
boundaries, retained spelling, stored dependencies, mixed routine/object named
identity replay and logical control
roundtrip. Root records actual results; selected checks do not replace
whole-project quickcheck or post-integration finalcheck.

### Text-search OID aliases (RegTS)

The shared `TestReferenceRegTS...` scenarios specify scalar REGCONFIG (3734)
and REGDICTIONARY (3769), zero (`-`) versus SQL NULL, unsigned numeric input,
octal parsing, direct/held parameters, raw strings versus pq bytes, empty
metadata, integral carrier casts, real installed unaccent dictionary identity,
quoted namespaces, search-path visibility, authorization, retained cursors and
logical persistence. The companion array identities remain lookup-only natively.
Expected rows and full English diagnostics are independent contracts, not
values copied from native output.

Pinned oracle discovery passed all seven selected scenario groups in the root's
second discovery round. The temporary backend filter and replay early return
have been removed. Every available backend now runs the shared scenario, and
real-mode persistence requires nonempty actual native dump bytes for PostgreSQL
replay. Discovery success is not native, emitted-stream replay or control proof;
the filter-free native regression was observed before production implementation.
The first pinned oracle run reported normalized missing configuration name
`123` for input ` 123 ` (42704, Position 8), not a primary retaining whitespace.
The contract now uses the parsed name for both aliases. Retained alias rows use
a feature-local raw/metadata assertion instead of the generic fixture's closed
type-family inventory; the shared fixture has not been expanded.

Native symbolic configurations have no provider and must report 0A000
`text search configuration name lookup is not supported`; PostgreSQL missing
configurations report 42704. Native dictionaries use only installed real
descriptors, never a fabricated `simple` dictionary. PostgreSQL-only custom
dictionary DDL characterizes quoted names, first-path (not ambiguous) lookup
and held-plan path invalidation. It does not authorize native text-search DDL,
configuration objects, two-argument unaccent, full-text execution, filesystem
rules, native ABI loading, or configuration DDL even in these scenarios.
The competing-name witnesses first require distinct nonzero qualified dictionary
OIDs, then put both schemas on the path in left/right and right/left order.
Unqualified input must select the first dictionary without ambiguity. Numeric
output of the winner is unqualified; the shadowed dictionary's output retains
its schema. The held-literal and TEXT-parameter phase likewise keeps both
candidates visible while reversing their order, preserving the literal
invalidation assertions and independently checking each parameter's selected OID.
Native Reset/Dump/Restore and binary COPY public controls are explicit native
phases; common SQL independently specifies their logical rows. Persistence
asserts qualified dump references, same-engine replay, real displaced destination
OID allocation, default/generated/view identities, retained rows, failure/cancellation
atomicity and stable redump. The final PostgreSQL phase replays actual emitted SQL.
The fresh-target displacement phase reserves an actual owner-held TEMP sequence
whose publicly observed OID equals the captured source dictionary OID, then
requires the restored dictionary OID to differ. It also checks that the TEMP
sequence keeps its identity and next value, while dictionary data and definition
literals follow the new descriptor. No allocator sequence is pinned: ordinary
target tables are discarded by Restore and cannot establish this collision.
The mixed routine/dictionary composite scenario separately checks persistent data,
stored DEFAULT consumption by a new INSERT, and a view literal. Every routine
member must retain the pinned `abs(integer)` identity (1397); every live dictionary
member must equal the actual destination installation's descriptor OID. A dropped
source table prevents allocator parity from masking numeric container output.
Native replay uses the same retained TEMP-collision destination, and PostgreSQL
replays the actual native dump bytes, never a hand-authored substitute. Root's
pinned and native failing-before runs passed source setup but observed mixed data
retaining the source dictionary OID instead of the destination descriptor.
The routine-container scalar leaf now uses the same qualified dictionary formatter
as ordinary compound emission, without changing routine fragments or field quoting.
Fresh post-remedy native and pinned proof is required. No discovery filter or
replay skip is present in this scenario.
The partition persistence scenario creates a range parent and matching ordinary
child before installing unaccent, then attaches the child with the live dictionary
as its finite exclusive upper bound. The namespace exists before the relations,
so a missing dictionary predecessor is not confused with missing-schema ordering.
Only a zero-valued row remains at Dump: no live dictionary data/default reference
can mask the missing bound dependency. The actual stream must install the extension
before the finite partition declaration, restore natively, and replay on PostgreSQL.
Routing runs once after each destination replay, using the actual dictionary OID:
its immediate predecessor belongs to the finite child, while the named descriptor and its successor
belong to the default partition. Dropping that populated child avoids unsupported
partition DELETE. Without the default partition, the named upper
endpoint must report the full 23514 diagnostic and roll back a preceding valid
row in the same INSERT. Root's pinned and native failing-before runs passed the
supported zero-only source setup, then actual native Restore and PostgreSQL
emitted-byte replay both failed with 42704 because the qualified dictionary bound
was consumed before extension installation. The full catalog-alias reference
walker now visits real dictionary references after the error-checked operator walk,
adding the missing dump predecessor without changing exact-Const collation/namespace
dependency filtering. Root then observed successful native and PostgreSQL replay;
the remaining native endpoint Detail printed a numeric dictionary OID while the
unchanged PostgreSQL diagnostic expectation passed with its qualified name.
Diagnostic output demand now captures the existing coherent catalog/path environment
for RegTS columns and typed partition keys, including derived keys over primitive
columns. `TestReferenceRegTSPartitionDiagnostics` permanently exercises direct
dictionary-column and derived `k::regdictionary` range keys over an OID column.
Both direct and prepared multirow INSERTs must retain the qualified symbolic
dictionary in the complete English 23514 diagnostic, omit unrelated logical
fields, and publish neither row; a subsequent interior insert must still route.
The derived case also checks the expression's REGDICTIONARY result metadata.
No derived-key failure before the typed-output-demand fix is claimed. Root's pinned
validation established the independently specified label `((k::regdictionary))`;
native retained the symbolic dictionary but initially grouped the cast's operand
instead. Partition diagnostics now use the existing pretty renderer for explicit
RegTS cast keys and group the whole expression, without changing global cast
deparse or relaxing the full diagnostic contract. Fresh selected native/pinned
proof remains required. Numeric partition-bound structural output stays separate
from SQL and diagnostics.
Dictionary identity discovery uses the existing installation's
`pg_catalog.pg_extension.extnamespace` and numerically filters actual
`pg_catalog.pg_ts_dict` rows; named alias input must equal that descriptor OID.
Scalar/companion identities are independently fixed through public REGTYPE/OID
casts (3734/3769/3735/3770). Shared bounded pg_type metadata assertions retain
genuine release identities, raw NAME/CHAR properties and existing namespace
relationships; native units independently check fixed descriptors, numeric
category and four-byte inline codecs. No NAME/CHAR-to-TEXT casts are added.
Stored CHECK, DEFAULT and generated-expression publication validates
real dictionary dependencies under the existing owner/epoch fences. Integral
carrier casts cover both implicit/assignment and explicit admission, and typed-nil
byte parameters remain SQL NULL for both text-search aliases.

Root runs each selected command from the worktree with an external 900-second
deadline. Oracle discovery and failing-before native regression are recorded;
the completed implementation now requires fresh native and pinned proof:

```sh
make check RUN='^TestReferenceRegTS(ValuesMetadata|Diagnostics|Namespace|NamespaceAuthorization|PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence|PartitionDiagnostics|FeatureSmoke)$'
make check RUN='^TestReferenceRegTS(ValuesMetadata|Diagnostics|Namespace|NamespaceAuthorization|PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence|PartitionDiagnostics|FeatureSmoke)$' REFERENCE=1
make check RUN='^Test(RegTS(ScalarCodecs|CastsDomains|ProviderResolution|PersistenceRemap|PreparedCapture|CapturedNamespaceACL|StoredDependencyProviders)|AnyValueProviderDatums)$'
make check RUN='^Test(RegTS(PreparedCapture|CapturedNamespaceACL)|ReferenceRegTS(PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence))$' RACE=1
```

Root recorded F2 failures with these selected commands; rerun them after the remedy:

```sh
make check RUN='^TestReferenceRegTSMixedRoutinePersistence$' REFERENCE=1
make check RUN='^TestReferenceRegTSMixedRoutinePersistence$'
```

Root recorded F3 failures with these selected commands; rerun them after the remedy:

```sh
make check RUN='^TestReferenceRegTSPartitionPersistence$' REFERENCE=1
make check RUN='^TestReferenceRegTSPartitionPersistence$'
```

The supplemental diagnostic scenario requires independent pinned expectation
proof first, followed by native proof (no pre-fix derived failure is claimed):

```sh
make check RUN='^TestReferenceRegTSPartitionDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceRegTSPartitionDiagnostics$'
```

The internal units exercise actual type codecs and captured catalog/ACL/remap
seams, not public SQL or source-wiring counts. Algorithm-only dictionary
snapshots are cloned from installed typed descriptors and never publish extra
providers. Shared public scenarios remain the acceptance proof for direct/held
driver behavior, metadata and actual native emitted-byte PostgreSQL replay.

Repeat both modes after scenario/implementation changes. Feature smoke uses
`make check RUN='^TestReferenceRegTSFeatureSmoke$'` and the same command with
`REFERENCE=1`; expected exit zero and assertions for all named RegTS scenarios,
not a readiness message. Whole-project `make quickcheck` remains a separate
post-edit gate; no checks or formatters were run while writing discovery tests.

### Regrole

The untagged shared scenarios independently assert role-name folding/quoting,
numeric OID boundaries (unknown IDs admitted), direct/fresh/held prepared input,
captured spelling, metadata/raw payloads, exact English diagnostic fields and
stored scalar Const restriction. Numeric conversion trees, runtime casts, typed
NULL and opaque arrays are distinguished from prohibited scalar children.
Native phases exercise real Dump/Reset/Restore, cross-Driver target-role remapping,
retained metadata/cursors, failure/cancellation atomicity and actual emitted-dump
replay on PostgreSQL18.0. Role provisioning is fixture setup, not an extension of
authentication/ACL/role DDL scope.
CHECK-grid array equality uses shared ordinary array providers and preserves the
exact scalar-Const ban; native-only missing-array-operator assertions are removed.
Existing domain-constraint admission boundaries remain independently asserted.
DEFAULT/VIEW shapes exercise native numeric/runtime/opaque-array distinctions.
Unknown-role Restore failures assert full independent diagnostics against the
corresponding pinned SQL replay: ERROR/ERROR, exact42704 primary, character
Position in the complete script, absent Context and all other optional fields.
The actual-dump missing-role case identifies the first opaque array DEFAULT in
`rr_recursive`; earlier runtime-text defaults do not resolve names at CREATE.

Run each small set both without and with `REFERENCE=1`, serially, with an external
900-second deadline:

```sh
make check RUN='^TestReferenceRegrole(Values|Parameters|Diagnostics|Metadata)$'
make check RUN='^TestReferenceRegrole(Values|Parameters|Diagnostics|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceRegrole(StoredConstants|StoredShapes|PreparedIdentity)$'
make check RUN='^TestReferenceRegrole(StoredConstants|StoredShapes|PreparedIdentity)$' REFERENCE=1
make check RUN='^TestReferenceRegrole(AtomicityCancellation|NativeControls)$'
make check RUN='^TestReferenceRegrole(AtomicityCancellation|NativeControls)$' REFERENCE=1
make check RUN='^TestRegrole(Datum|Codec|StoredLiteral|CapturedAuthority)$'
make check RUN='^TestReferenceRegrole(PreparedIdentity|AtomicityCancellation|NativeControls)$' RACE=1
```

Feature smoke also requires a real throwaway public API Go program, not tests
alone. Run `go run /tmp/regrole-feature-smoke/main.go` from the worktree after
materializing the session-provided source outside the repository. It independently
checks direct/held prepared quoted and unknown numeric values, REGROLE metadata,
full0A000 stored diagnostic, legal runtime-default data and NULL view, actual
Dump/Reset/Restore, retained eager rows and identical redump. Expected exit0 and
empty stderr; its observation lines report direct/prepared values, stored error,
nonempty dump, retained/restored values and identical redump only after validation.
Remove the temporary program after recording evidence; never commit its source.
Selected proofs do not replace whole-project quickcheck or post-merge finalcheck.

### Operator OID aliases

The `TestReferenceRegOperator...` definitions cover name-only and exact ordered
signature resolution, prefix NONE, complete released builtin identities, direct
and held parameters, metadata, complete diagnostics, and real private namespace
and operand-type authorization. Independent expectations/provenance live in
`docs/design/regoperator-pg18-inventory.tsv` and `regoperator-pg18-oracle.md`.
Custom operator DDL, execution, live catalog rows, ownership, dependencies and
logical replay are shared native/PostgreSQL scenarios, not a PostgreSQL-only phase.

Pinned oracle discovery is complete in the root's tests-1 through tests-4 reports;
tests-5 records the failing native-before-production regression. No discovery
filter/logging remains. Both aliases' typed-nil byte binds are SQL NULL. The old
native CREATE OPERATOR syntax-error boundary has been superseded by shared
`ExternalBoundary` assertions and the [user-operator definitions](../operators/README.md#testing).

Review-remedy discovery is recorded in tests-13/14: pinned complete competing
diagnostics and NBSP records preceded the observed native/parser/prepared failures.
`ParserEdges` preserves all six literal/parameter/runtime routes, while
`PreparedAnalysis` covers public mixed-alias values, held recovery and snapshots.
Internal `TestRegOperatorPrepared...` units additionally use real typed catalog
descriptors to prove ordered operand manifests, direct shared-cell validation,
atomic group publication and unchanged captures under unrelated role changes.
Those internal fixtures now use legitimate owned shells without fake providers.

Root verifies the identical completed definitions with an external 900-second
deadline for each command:

```sh
make check RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory|ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory|ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$'
make check RUN='^TestRegOperator'
make check RUN='^TestRegOperator(Signature|Prepared)'
make check RACE=1 RUN='^Test(RegOperatorPrepared|ReferenceRegOperator(PreparedLifetime|PreparedAnalysis|Persistence))'
make check RUN='^TestReferenceRegOperatorFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRegOperatorFeatureSmoke$'
```

Persistence replays the actual emitted native dump on PostgreSQL, not constructed
SQL. Smoke covers unique/ambiguous names, exact ordered signatures, unary NONE,
held parameter failure/recovery, metadata, private-schema denial, numeric dump
roundtrips, retained strings and failed-tuple atomicity. These definitions and
implementation are not passing evidence until root exercises the gates.

### User-defined operators

See [the owning operator package's Testing guidance](../operators/README.md#testing)
for user-defined operator scenarios and `TestReferenceRegOperatorExternalBoundary`.

### Regproc / regprocedure definitions and evidence

The original eight `TestReferenceRegProc` definitions preserve the independently specified
PostgreSQL18.0 contract. Root Verification1 exposed diagnostic/quoting corrections;
Verification2 confirmed the corrected oracle SQL contract, with native-generated
dump phases still deferred at that test-only stage. Root removed the PostgreSQL-only
filter and both nil-stream guards before Verification3's genuine failing native
regression. Verification8 subsequently passed the original eight definitions and
changed standalone REGTYPE definition in native/pinned modes, quickcheck and public
smoke after captured type-authority reuse, including actual emitted-stream replay.
Review1 identified additional numeric/dash signature-type and mixed routine/REGROLE
serialization defects outside those exercised paths. Verification12 completed
native/pinned proof for both new regression definitions, including restored-default
INSERT and actual emitted-stream replay. Prior passes alone did not establish
their remedies. No discovery filters or control deferrals remain in the twelve
RegProc definitions; completed selected native/pinned proof now includes R5.

Witnesses cover direct/fresh-prepared/same-held values, empty-result metadata,
string/textual-byte/typed-nil/NULL frames, exact fixed identities and signatures,
six complete diagnostic routes, casts, all-path overload visibility, real unaccent
authority, expression versus data-only dependencies, atomic late-row failure,
tables/defaults/views/domains/arrays, retained outputs and native replacement.
Schema authority setup deliberately uses real PostgreSQL USAGE grants versus
supported native schema ownership transfer; it does not admit native GRANT/REVOKE
SQL or transfer bootstrap-owned tables.
`DROP EXTENSION` is likewise an existing native utility boundary, not delivered
by routine aliases. PreparedAuthority and Persistence assert its exact native
`0A000` / `statement is unsupported` / Position `1` diagnostic and unchanged
alias values, then continue supported path/role authority and persistence phases.
Real PostgreSQL retains the complete drop/reinstall, dangling data identity,
held-literal retention and dependency Detail/Hint assertions. Private real catalog
removal supplements native identity-lifetime coverage without claiming SQL support.

Signature witnesses include canonical aliases, schema-qualified types, modifiers,
arrays, zero arity, aggregate sum and
the quoted mixed-case RI builtin. Complete release extraction validates all 3397
source-fixed identities and 116 argument-type symbols; see
[the evidence note](../../docs/design/regproc-pg18-oracle.md).

`SignatureTypeSyntax` distinguishes decoded argument syntax from whole-alias input:
numeric/dash arguments must use the existing type parser and diagnostics, while
whole REGTYPE/REGPROC/REGPROCEDURE numeric and dash shortcuts remain valid.
`MixedRolePersistence` covers a supported composite carrying REGPROCEDURE and a
named REGROLE, preserving symbolic role serialization in rows/defaults/views,
displaced native Restore and exact emitted-stream PostgreSQL replay. Its role OID
must resolve to the receiving catalog's named role, not retain the source OID.
Scalar REGROLE stored constants remain unsupported; executable composite arrays
use the same recursive logical replay path.

Review2's distinct mixed routine/operator capture coverage gap is addressed by
`reference/type_resolution/regproc_mixed_operator_test.go` /
`TestReferenceRegProcMixedOperatorPersistence`: the admitted composite contains
REGPROCEDURE, REGOPERATOR and REGTYPE, with independently expected builtin OIDs
1397/551/23 and symbolic output. Direct and held string/byte/NULL frames,
row/default/view persistence, actual emitted native Dump replay on PostgreSQL,
restored-default INSERT and retained native output/metadata across Reset/Restore
preserve existing operator behavior without adding operator functionality.
The private `TestRoutineMixedOperatorCompoundCapture` supplements public values
with real descriptor publication, exact ordered fresh and retained operand
dependencies at the same occurrence, cell classifications, actual operand revision
invalidation/recapture and qualified operand-authority invalidation/recovery.
Completed root native/pinned checks confirm the public scenario, and the real
private mixed-capture lifecycle selector passes. Verification12 remains historical;
the later proof exercises the exact mixed owner and restored-view remedy.

```sh
make check RUN='^TestRoutineMixedOperatorCompoundCapture$'
make check RUN='^TestReferenceRegProcMixedOperatorPersistence$'
make check RUN='^TestReferenceRegProcMixedOperatorPersistence$' REFERENCE=1
```

The public selector is also the bounded R4 feature smoke: expect exact symbolic
values and OIDs, restored-default evaluation, actual PostgreSQL replay, and
retained native output/metadata to pass, not merely successful setup.

Review3's materialized retained-input finding has a public regression in
`regproc_materialized_test.go` / `TestReferenceRegProcMaterializedPersistence`.
It independently specifies fixed builtin OIDs1397/551/23 and receiving-catalog
named-role identity in a nonpublic opaque composite. Direct and nested definitions
must retain native saved stale population1 after Dump/Reset/Restore, refresh to2,
then refresh to2/3. Actual emitted SQL creates PostgreSQL views WITH NO DATA:
complete55000 diagnostics precede explicit refresh to2, then2/3. Native unread
rows and metadata remain retained across replacement and refresh.
Verification16 records oracle-only SQL discovery success and corrected native
Restore failure `type "mixed" does not exist`, not passing materialized proof.
The source remedy reuses shared retained input traversal under the population
owner before frozen preflight, qualifies dumped composite casts through the
existing renderer, and validates emitted closed snapshot concatenation through
the existing import owner. Completed native and pinned replay proof now confirms
this consumer; Verification15 remains the historical earlier-consumer evidence.

```sh
make check RUN='^TestReferenceRegProcMaterializedPersistence$' REFERENCE=1
make check RUN='^TestReferenceRegProcMaterializedPersistence$'
```

This dedicated R5 smoke expects exit0/PASS for current symbolic values, exact
builtin identities, relational role identity, direct/prepared fresh metadata,
stale-versus-refreshed populations, retained native results, and actual stream
replay. Root removed discovery filters and missing-stream deferrals before the
genuine native failure; the fixture preserves Reset's existing role catalog.
The modified private owner proof is selected separately:

```sh
make check RUN='^Test(MaterializedRefreshOwnerContext|MaterializedRefreshAuthorization|MaterializedViewSnapshotLosslessCoercions|RoutineMixedOperatorCompoundCapture)$'
```

It expects genuine retained coercion1397, owner-authorized restricted input,
unchanged caller session identity, and input/principal restoration after success
or producer failure. Full quickcheck and fresh review remain separate root gates.
The snapshot unit admits the generated closed concatenation and rejects nested
function, identity, column and binary-arithmetic expressions.

`Persistence` and `FeatureSmoke` use actual native Dump bytes, native Reset/Restore,
displaced independent engine identities, retained rows and failed/canceled Restore
atomicity. Real mode replays that exact emitted stream through ordinary PostgreSQL
SQL; no manually reconstructed stream or simulated PostgreSQL control API is used.

Selected root proof commands, each with an external 900-second deadline:

```sh
make check RUN='^TestReferenceRegProc(ValuesMetadata|Signatures|SignatureTypeSyntax|Diagnostics|Casts)$'
make check RUN='^TestReferenceRegProc(ValuesMetadata|Signatures|SignatureTypeSyntax|Diagnostics|Casts)$' REFERENCE=1
make check RUN='^TestReferenceRegProc(SearchPath|PreparedAuthority|Persistence|MixedRolePersistence|FeatureSmoke)$'
make check RUN='^TestReferenceRegProc(SearchPath|PreparedAuthority|Persistence|MixedRolePersistence|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceRegProcFeatureSmoke$'
make check RUN='^TestReferenceRegProcFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceRegProc(SignatureTypeSyntax|MixedRolePersistence)$'
make check RUN='^TestReferenceRegProc(SignatureTypeSyntax|MixedRolePersistence)$' REFERENCE=1
make check RUN='^TestReferenceRegProc(PreparedAuthority|Persistence)$' RACE=1
make check RUN='^TestReferenceRegtypeAliasModifierSyntax$' REFERENCE=1
make check RUN='^TestReferenceRegtypeAliasModifierSyntax$'
```

Smoke expects exact builtin OIDs1242/1397/1644, REGPROC/REGPROCEDURE metadata,
full ambiguity/missing records, same-held invalid-to-valid recovery, no partial
INSERT rows, native persistent roundtrip and actual emitted-stream oracle replay.
Expected success is test PASS/exit0, not an invented console banner. Private parser,
codec, release-inventory, candidate-mask, remap and retained-presentation units
supplement these public scenarios; they do not replace selected dual-mode proof,
whole-project quickcheck or post-integration finalcheck. Neither readiness nor
source extraction is passing native smoke evidence.

Changed preexisting definition: `TestReferenceRegtypeAliasModifierSyntax` now
adds the shared generic decoded-type keyword boundary (NONE on all six routes)
and preserves admitted quoted/qualified domain and builtin/modifier paths.
Its REGTYPE-specific route expectations use the existing independent error owner;
Verification8 confirmed these standalone fields in both native and pinned modes.
The two review regressions also pass completed native/pinned proof, including
six-route numeric/dash rejection and mixed composite row/default/view restoration
with receiving-catalog role identity and actual emitted-stream PostgreSQL replay.

Complete added/modified dual-definition inventory (`A` = first RegProc selector
above, `B` = second, `C` = standalone Regtype selector, `D` = dedicated R4
MixedOperatorPersistence selector, `E` = dedicated R5 MaterializedPersistence
selector; repeat each in both modes):

| Path in this package | Exact definition | Membership | Reason |
| --- | --- | --- | --- |
| `regproc_values_test.go` | `TestReferenceRegProcValuesMetadata` | A | New alias values, raw transport, metadata and storage |
| `regproc_values_test.go` | `TestReferenceRegProcSignatures` | A | New exact signatures, released OIDs and quoting |
| `regproc_signature_type_syntax_test.go` | `TestReferenceRegProcSignatureTypeSyntax` | A and remedy smoke | Numeric/dash argument syntax across six routes, valid held recovery and preserved whole-input shortcuts |
| `regproc_diagnostics_test.go` | `TestReferenceRegProcDiagnostics` | A | New complete six-route diagnostics |
| `regproc_values_test.go` | `TestReferenceRegProcCasts` | A | New casts, comparisons and assignment boundaries |
| `regproc_authority_test.go` | `TestReferenceRegProcSearchPath` | B | New path/namespace authority and qualified output |
| `regproc_authority_test.go` | `TestReferenceRegProcPreparedAuthority` | B | Held identity/input lifecycle, supported path/role recovery and explicit native DROP boundary; full PG drop/reinstall |
| `regproc_persistence_test.go` | `TestReferenceRegProcPersistence` | B | Native controls, owned REGPROC/REGPROCEDURE retained raw cells, remap/dependencies, DROP boundary and actual stream replay |
| `regproc_persistence_test.go` | `TestReferenceRegProcFeatureSmoke` | B and exact smoke | New feature acceptance smoke |
| `regproc_mixed_controls_test.go` | `TestReferenceRegProcMixedRolePersistence` | B and remedy smoke | Supported routine/role composite row/default/view serialization, displaced target-role identity and actual emitted-stream replay |
| `regproc_mixed_operator_test.go` | `TestReferenceRegProcMixedOperatorPersistence` | D and R4 smoke | Actual mixed routine/operator/type capture, row/default/view persistence, restored-default INSERT, retained output and emitted-stream replay |
| `regproc_materialized_test.go` | `TestReferenceRegProcMaterializedPersistence` | E and R5 smoke | Direct/nested materialized frozen definitions, routine/operator/type/role authority, actual controls and refresh, stale/retained rows and actual emitted-stream replay |
| `regtype_alias_syntax_test.go` | `TestReferenceRegtypeAliasModifierSyntax` | C | Modified shared decoded-type keyword admission proof |

The eight RegProc definitions were authored and oracle-corrected in the prior
test-only stage; Stage C preserves their full expectations. No public definition
was migrated, and no other preexisting public definition was modified.

### Transaction and command identifiers

The shared XID definitions independently assert canonical unsigned input/output,
distinct scalar/companion identities and metadata, exact diagnostics, asymmetric
XID/INT4 equality, CID equality-only and unsigned XID8 comparisons/order. They
exercise documented low32 XID8→XID and string I/O casts, direct/held parameters,
NULL/typed-nil bytes, raw byte ownership, retained metadata and persistent
defaults/CHECKs/views. Native controls replay the actual emitted dump on the
pinned PostgreSQL oracle; native array/system-column capability boundaries do
not claim PostgreSQL lacks those mechanisms.
Identifier array annotations reject at type admission, before typed NULL or datum
construction. Prepared native CREATE TABLE validates this capability at execution;
SELECT cast annotations reject at Prepare. Persistent identifier view literals
retain explicit scalar casts without accumulating redundant wrappers across
Dump/Restore cycles.
ConditionalEquality, ExtremaOrdering and PublicDeparse regressions cover existing
consumer cutover: NULLIF/simple CASE directional equality and unknown operands;
XID/CID extrema rejection at execute with absent Position/Hint even empty/all-NULL;
unsigned XID8 extrema; and exact public default/CHECK deparse annotations before
and after native Restore. Direct and actual held routes share independent
expectations confirmed first on pinned PostgreSQL, not native-derived goldens.

Oracle-first discovery established pinned PostgreSQL18.0 outcomes before native
implementation. No discovery filters, deliberate observation failures or control
deferral guards remain. The existing-provider XID8 opclass identities have narrow
real-catalog assertions; metadata keeps native XID8 despite pq's empty name.
Shared `regtype` lookup proves the fixed scalar/companion identities without
claiming native `pg_type` relation support. Physical widths4/4/8 remain checked
at the owned descriptor/binary seams; genuine `pg_type` projection is an explicit
PostgreSQL-only catalog boundary. The xid8→xid cast uses fixed function5071 and
explicit/function metadata; generated cast row identities stay backend-owned.
Root must verify the completed identical definitions in both modes, each with a
900-second external deadline:

```sh
make check RUN='^TestReferenceXID(ValuesMetadata|InputDiagnostics|Operators|Casts|BindsAndNulls|DumpReplay|FeatureSmoke)$'
make check RUN='^TestReferenceXID(ValuesMetadata|InputDiagnostics|Operators|Casts|BindsAndNulls|DumpReplay|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceXID(ConditionalEquality|ExtremaOrdering|PublicDeparse)$'
make check RUN='^TestReferenceXID(ConditionalEquality|ExtremaOrdering|PublicDeparse)$' REFERENCE=1
make check RUN='^TestReferencePGCast(Metadata|Exclusions|Controls|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGCast(Metadata|Exclusions|Controls|Diagnostics|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceXIDFeatureSmoke$'
make check RUN='^TestReferenceXIDFeatureSmoke$' REFERENCE=1
```

Smoke expects max-width/NULL direct and held values, distinct metadata/raw bytes,
admitted comparisons/cast results, exact unsupported operator/cast diagnostics,
native persistent roundtrip and actual emitted-dump replay. Selected checks do
not replace whole-project quickcheck or post-merge finalcheck. Test definitions
are not passing evidence until root records successful execution.


### Regtype completed definitions

The `^TestReferenceRegtype` shared-body family encodes pinned PostgreSQL 18.0
diagnostics, metadata, raw transport, warning timing and held literal identity.
Every released/initialized name and array is exercised through regtype SQL
without requiring a native pg_type projection. The complete identity facts and
fixed-versus-allocated provenance are described in
[the inventory handoff](../../docs/design/regtype-pg18-oracle.md).

The completed input and authorization scenarios preserve generic qualified
varchar modifier witnesses and use a superuser-owned denied table in an
accessible reader-owned schema. `TestReferenceRegtypeAliasModifierSyntax`
preserves rejected string/parenthesized alias modifiers across all six routes,
including held runtime input. `TestReferenceRegtypeHeldLiterals` and
`TestReferenceRegtypeWarnings` cover authority-sensitive identity capture and
literal-versus-parameter warning timing. No discovery filters, observation
logging or deliberate discovery failures remain.

`TestReferenceRegtypeOutputAuthority` and its held/atomic/native-control companions
exercise selected VARCHAR IO, TEXT/VARCHAR assignment, domain sources, stored
defaults and admitted ALTER-column conversions with actual Dump replay and
retained string metadata. The unrelated-role
family requires unchanged held OIDs and no repeated warnings after unrelated
CREATE/DROP ROLE, while its qualified-authority control requires literal/fresh
Prepare 42501 Position8, frame 42501 with portal Context, and owner-role recovery.
Run the completed review-remedy witnesses in both modes before review, each with
a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
make check REFERENCE=1 RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
```

Validate the same completed definitions in both modes before review; each
quickcheck invocation has a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype'
make check REFERENCE=1 RUN='^TestReferenceRegtype'
```

The regtype definitions independently assert name/OID conversion, builtin and user
row identities, qualification/shadowing, held input recovery, metadata,
persistent scalar/domain/array values and native control atomicity. Native
controls must replay the actual emitted native stream on PostgreSQL; never
substitute a manually reconstructed stream or an invented PostgreSQL
Dump/Reset/Restore API. The unchanged cohesive fixture's `checkRaw` hook supplies
independent REGTYPE/array transport checks through the small regtype helper.
These selectors do not replace ordinary quickcheck or the post-merge finalcheck
under its single 1800-second deadline. Completed definitions are not passing
verification until the root records successful runs.

Post-merge replay reliability retains all 40 mixed creator/order cases (five
creator kinds, four unlike counterparts, both orders) in the same catalog,
every checkpoint, late invalid Restore rollback, second Restore and fresh
default writes for both named elements of every pair. Expectation OID probes
are batched once per catalog checkpoint; positivity and pairwise distinct
identities, display/raw carriers/metadata and actual direct/prepared checks
remain unchanged. Identity results are refreshed after catalog replacement,
not cached across generations. Shared checker migration also includes
ArrayDisplacementControls.

The shared reciprocal `pg_type` probes put their existing single-element OID
restriction inside a derived left operand selecting `oid,typarray`, before the
self-join. This is still one captured statement with the same two catalog
sources, not separate lookups or a cache. The catalog probe retains both
`a.typelem=e.oid` and `a.oid=e.typarray`; the logical probe retains the actual
reciprocal and independently resolved array-identity booleans. Every matching
row, projection, raw carrier, expected count and direct/prepared route remains
required. No LIMIT, expected-derived catalog data, production optimizer change
or relaxed fixture/external deadline is used to avoid whole-catalog pair scans.

Exact transitive consumers of these two helper query definitions are
`TestReferenceRegtypeArrayDisplacement`, `...Collisions`, `...Atomicity`,
`...Controls` and `...MixedReplay`. Atomicity's catalog helper remains its
existing PostgreSQL-only phase; its native assertions are unchanged.
`regtypeDisplacementForeignReplay` belongs to MixedReplay, although its helper
definition is located after the unrelated NamedDomainReplay test. Executable
and NamedDomainReplay are unchanged preservation siblings, not extra callers.

MixedReplay alone gives each cohesive phase a fresh 30-second context rooted
in `t.Context()`: backend borrowing/schema/search-path setup, creator-pair
setup, identity batch, each complete case check, each data witness, Dump,
replacement/retained-cursor observation, and actual emitted PostgreSQL replay.
The test locally repeats the shared wrapper's exact setup so native-first work
cannot exhaust the original context before PostgreSQL borrowing even begins;
schema cleanup registration, backend order and notice provenance are unchanged.
Foreign replay keeps the same target fixture,
spacer-before-Restore, pools, recorder and borrowed physical connection through
setup, full validation, invalid Restore, validation, redump, second Restore,
validation and fresh default writes. Only a shallow local Pair view's `Ctx`
changes; no fixture-global deadline or API is changed. Each phase cancels on
exit, and retained rows are read/closed after replacement but before their
phase cancels or the unrelated full matrix resumes. Failure output includes
the phase label, elapsed time and context error.

The intentionally invalid late Restore must report 22P02 with the exact
`invalid input syntax for type integer: "bad"` cause while its context remains
live; a deadline is not evidence of failure atomicity. The full checks still
follow that failure and both successful restores, and PostgreSQL still executes
the actual emitted native dump, never a reduced hand-written script.

For the phase-lifetime remedy and feature smoke, run the complete MixedReplay
definition, oracle first, with an external 900-second deadline per invocation:

```sh
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$'
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' RACE=1
```

Expected observations are the complete 40-case source and restored identity,
logical name, direct/prepared metadata/data and native retained-row contracts,
unchanged state after the independently diagnosed invalid Restore, successful
redump/second Restore and two rows after every fresh default INSERT. Real mode
also validates actual emitted SQL on pinned PostgreSQL. These are required
outcomes, not a claim that the root has already verified this remedy.

The complete affected shared-checker proof includes the small displacement,
collision and atomicity group, plus Controls and the full MixedReplay. Run each
selector natively, on pinned PostgreSQL and with the race detector, under the
unchanged external900-second deadline per command:

```sh
make check RUN='^TestReferenceRegtypeArrayDisplacement(Collisions|Atomicity)?$'
make check RUN='^TestReferenceRegtypeArrayDisplacement(Collisions|Atomicity)?$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacement(Collisions|Atomicity)?$' RACE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementControls$'
make check RUN='^TestReferenceRegtypeArrayDisplacementControls$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementControls$' RACE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$'
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' RACE=1
```

These focused observations do not replace whole-project quickcheck or the
post-merge finalcheck and its unchanged1800-second external deadline. Prior
timeouts remain failures; the rewritten definitions are not passing evidence.


### Regtype control lifetime

Regtype control witnesses retain a supported TEMP SEQUENCE across Reset/Restore
to fence physical-session ownership and allocator collisions. Persistent
domains supply the user-defined TypeClassID remapping witnesses. Temporary
domain creation remains an adjacent native 0A000 boundary, even in an already
initialized namespace; the path scenario asserts that boundary natively and
uses PostgreSQL-only temporary-domain setup for additional oracle context.
