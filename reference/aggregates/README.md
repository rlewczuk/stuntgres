# reference/aggregates

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
