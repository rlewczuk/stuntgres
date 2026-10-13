# reference/aggregates

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
