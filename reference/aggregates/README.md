# reference/aggregates

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
