# reference/type_resolution

Dual-mode tests for type and operator resolution: the comparison-operator
resolution matrix (pairs, BETWEEN triples, DISTINCT, null/truth predicates,
consumers, structural and stored routes), explicit casts, NAME/OID values and
the type core, diagnostic field records, the OID alias types (`regclass`,
`regcollation`, `regnamespace`, `regconfig`, `regdictionary`, `regrole`,
`regoperator`, `regproc`/`regprocedure`, `regtype`) and `xid`/`cid`/`xid8`.
User-defined operators and `TestReferenceRegOperatorExternalBoundary` are in
[reference/operators](../operators/README.md#testing); `pg_cast` is in
[reference/catalog_objects](../catalog_objects/README.md#testing) and other catalog
inventories are in [reference/catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. `name_external_test.go` and `diagnostics_postgres_test.go`
carry the `reference` build tag (PostgreSQL-only complements).

### Comparison matrix

- [comparison-ledger.md](comparison-ledger.md) records the evidence method and
  inventory; `comparison_facts_test.go` encodes the released facts. Never derive
  an expectation from native output.
- `Matrix`, `Between` and `PredicateErrors` are sharded by the suffixes
  `Numeric`, `Strings`, `Temporal`, `Identifiers`, `BinaryNetwork`,
  `NegativeSpace`, `Ranges` and `Arrays`. Run each shard selector on its own
  (`RUN='^TestReferenceComparisonMatrixNumeric$'`); after changing a shared helper,
  run every calling shard, not a representative one.
- `comparison_negative_plan_test.go` is a pure plan that witnesses each empty pair
  (no released operator) once per route with a rotating spelling, form and filter.
  Each family asserts full rotation coverage before allocating a fixture.
  Admitted and partially admitted pairs execute every combination.
- Sizing: Matrix, Between and PredicateErrors own one scenario per left identity;
  Consumers uses windows of two right identities because PostgreSQL reruns the
  catalog node-tree source in every correlated route. Batched projections hold
  at most 32 expressions. Successful checkpoints run directly once and through
  the same held statement twice (`comparisonCheckpointRoutes`); analysis denials
  fail at execute directly and at Prepare when prepared.
- `comparisonFiniteValue`/`comparisonFiniteValueFrom` return plain columns (the
  fixture stores NULL at id 0). Only arrays, CHAR, `pg_node_tree`, VOID,
  node-peer strings, BPCHAR and BIT keep an outer NULL-gating CASE (the last two
  for their default typmods). `comparisonFiniteCharSource` binds the catalog
  CHAR values once per query through a singleton derived source; a batch acquires
  it before its first bound CHAR expression.
- Expected BETWEEN truth comes from independent endpoint comparisons under SQL
  three-valued logic.
- Stored-constructor scenarios distinguish member, whole-value and unused-CTE
  dependencies through container removal, exact RESTRICT diagnostics and CASCADE
  retirement of retained types, companions and views.

### Cast coverage

The cast inventory and released directional facts are independent expected data.
Runtime families use deterministic provider, direct-edge, generic-rule and
diagnostic witnesses rather than Cartesian route products. `DirectEdges` batches
typed column conversions by source and input; family `Matrix` and `Functions`
definitions cover distinct codecs and actual signatures. Focused cases retain
context, sizing, domains, shapes, environments, atomicity and lifecycle behavior.
`ProtocolRoles` shares one real server fixture for nonstorage/input roles.
The SERVICE_CERTIFICATES public and server regressions retain all five arguments,
repeated/NULL/error frames and protocol metadata independently of matrix sizing.
Focused review regressions retain numeric-leading temporal callable resolution
versus precision literals (`NumericTemporalCalls`) and actual installed citext
default rendering across schema rename, old-name shadowing and native replay
(`InstalledCastRendering`). `DeclaredTypeNameAuthority` lives in
`reference/operators` to reuse declared builtin-pair cleanup; it distinguishes
function, binary and INOUT authority and preserves the established explicit
PostgreSQL-held/native-reanalysis lifetime expectations.
Its stored binary typename view retains the selected method after the current
pair is replaced by INOUT; this is not a fresh-call or dumped-row surrogate.

### OID aliases and identifiers

- Normalized `oid` cells are int64 natively and strings through lib/pq. lib/pq
  reports an empty `DatabaseTypeName` for REGCOLLATION and XID8; assert raw codecs
  independently of the name.
- lib/pq cannot run another query on a connection with an open result stream:
  mutate rows behind a retained cursor through a second connection.
- A dropped OID can be reminted for a new referent, changing the symbolic spelling
  of retained bits; never reserve OIDs or suppress live output.
- Restore discards ordinary target tables, so allocator-displacement witnesses
  keep an owner-held TEMP object (sequence, namespace or rowtype) alive on the
  target; persistent domains supply user TypeClassID remapping witnesses.
- Replay phases need the full dual-mode selection: a `/postgres` selector cannot
  supply the native emitted stream.
- RegTS reads dictionary identities from the installed `unaccent` extension
  (`pg_extension.extnamespace`, `pg_ts_dict`) and asserts alias rows with the
  local `regtsAssertAliasRows`, not the generic fixture type inventory.
- Regtype catalog probes restrict a derived left operand before the reciprocal
  `pg_type` self-join and batch expectation OID probes once per catalog
  checkpoint, refreshed after catalog replacement; never use LIMIT, cached OIDs
  or expectation-derived catalog data. REGTYPE/array transport is checked through
  `Step.CheckRaw`.
- `TestReferenceRegtypeArrayDisplacementMixedReplay` gives each phase a fresh
  30-second context rooted in `t.Context()` through a shallow local Pair view
  (`regtypeDisplacementPhase`); `...ArrayDisplacementControls` gives each of its
  five kind lifecycles its own fixture. Their invalid late Restore must fail with
  22P02 while the context is still live.
- Identity evidence: [regtype](../../docs/design/regtype-pg18-oracle.md),
  [regproc](../../docs/design/regproc-pg18-oracle.md) and
  [regoperator](../../docs/design/regoperator-pg18-oracle.md) (with their
  `.tsv` inventories in `docs/design/`). `fixture/regoperator.go` is shared with
  `reference/operators`.

### Selectors

Add `REFERENCE=1` for the oracle.

- Comparison shards (each alone): `RUN='^TestReferenceComparison{Matrix,Between,PredicateErrors}<Suffix>$'`
- Distinct, null/truth, consumers: `RUN='^TestReferenceComparison(Distinct|Null|Consumer|BatchSelection)'`
- Structural, record and row comparisons: `RUN='^TestReferenceComparison(Composite|Row|Record|Structural|Nested|NodeTree|Deferred|Cycle|DomainArray)'`
- Conversions, boundaries, diagnostics: `RUN='^TestReferenceComparison(Core|Catalog|Unknown|Boundaries|Diagnostic|PredicateSyntax|Anyarray|InstalledQueryInt)'`
- Comparison lifecycle and stored routes: `RUN='^TestReferenceComparison(PreparedLifecycle|Stored)'`
- Explicit casts: `RUN='^TestReferenceCastExplicit'`
- Cast families (expand to one suffix): `RUN='^TestReferenceCastImplemented(Matrix|Functions)Numeric$'`
- Cast rules and lifecycle: `RUN='^TestReferenceCastImplemented(Contexts|GenericShapes|Typmods|UnknownParameters|Metadata|Environments|Consumers|Lifecycle)$'`
- Cast protocol and reported INSERT: `RUN='^TestReferenceCastImplemented(ProtocolRoles|ServiceCertificatesSmoke)$'`
- Cast review regressions: `RUN='^TestReferenceCastImplemented(DeclaredTypeNameAuthority|NumericTemporalCalls|InstalledCastRendering)$'`
- NAME, OID, type core: `RUN='^TestReference(Name[A-Z]|OID|TypeCore)'`
- Diagnostic field records: `RUN='^TestReference(ErrorFields|ConditionalErrorFields|PermissionAndCancellationFields)'`
- `regclass`: `RUN='^TestReferenceRegclass'`
- `regcollation`, `regnamespace`: `RUN='^TestReferenceReg(Collation|Namespace|ObjectBoundary)'`
- `regconfig`, `regdictionary`: `RUN='^TestReferenceRegTS'`
- `regrole`: `RUN='^TestReferenceRegrole'`
- <a id="operator-oid-aliases"></a>`regoperator`: `RUN='^TestReferenceRegOperator'`
- `regproc`, `regprocedure`: `RUN='^TestReferenceRegProc'`
- `regtype`: `RUN='^TestReferenceRegtype'`
- `xid`, `cid`, `xid8`: `RUN='^TestReferenceXID'`

The shard row is a template: expand it to one exact name, for example
`RUN='^TestReferenceComparisonBetweenStrings$'`.
