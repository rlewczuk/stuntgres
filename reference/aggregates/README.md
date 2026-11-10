# reference/aggregates

Dual-mode scenarios for aggregate declarations, execution and lifecycle.
Numeric SUM/AVG belongs to [numeric](../numeric/README.md#testing), array
aggregation to [cursors](../cursors/README.md#testing), and string aggregation
to [strings](../strings/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main(m)`.
Family-local helpers live in `*_helpers_test.go`.

- Worker cost profiles are scenario-local. Do not force PostgreSQL workers
  globally: doing so can change the operation whose safety is being asserted.
  A physical-worker claim needs an observed physical plan; native logical EXPLAIN
  does not establish PostgreSQL topology.
- Filter catalog inputs to the owning procedure before joining large inventories.
- Replay actual emitted SQL. Preserve raw EXECUTE ACLs, grantor/options and
  absent-versus-empty state independently of effective rights.
- Accumulation-order-sensitive floats use named SQL tolerance witnesses; other
  values, metadata and diagnostics remain exact. There is no global approximate
  comparator.
- Unordered aggregate fragments are bags with duplicate counts, not portable
  serialized sequences. To observe failure before any PostgreSQL cursor output,
  use a blocking sort rather than assuming eager execution.
- Active cancellation requires the measured worker's real operation witness.
  Sampling alone does not hold its lifetime; cancel/join before releasing handles.
  Provider-internal cancellation belongs to the owning private units.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Declarations: `RUN='^TestReference(CreateAggregate|AggregateFinal|AggregateMoving)'`
- Lifecycle: `RUN='^TestReferenceAggregateLifecycle'`
- Modifiers: `RUN='^TestReferenceAggregateModifiers'`
- Ordered-set: `RUN='^TestReferenceOrderedSetAggregate'`
- User-defined ordered aggregates: `RUN='^TestReferenceAggregateOrdered'`
- Combine support: `RUN='^TestReferenceAggregateCombine'`
