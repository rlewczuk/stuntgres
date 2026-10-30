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

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` passes an explicit
text-search file manifest to `fixture.Main(m)`. `name_external_test.go` and `diagnostics_postgres_test.go`
carry the `reference` build tag (PostgreSQL-only complements).

### Input validity diagnostics

`TestReferenceInputValidityValues`, `NullRecords`, `Typmods`, `HardErrors`,
`Binding`, `Lifecycle` and `Smoke` share independent direct/prepared public SQL
expectations in both modes. They cover BOOL/RECORD/TEXT metadata, strict NULL
versus successful all-null records, typmod-aware conversion, message/detail/hint
and SQLSTATE, declaration/setup and hard producer boundaries, domain checks,
transactions, held target changes, statement atomicity and native stored replay.
Scalar FROM uses the common typed scalar-once cursor, including one expanded
four-NULL row for strict NULL error-info. Named `value`/`type_name` arguments
belong only to error-info; named boolean calls must fail. Retained view rows
use the established BOOL/TEXT metadata contract (unbounded TEXT length), not
REGTYPE-specific length assumptions.
Select the family with
`make check RUN='^TestReferenceInputValidity(Values|NullRecords|Typmods|HardErrors|Binding|Lifecycle|Smoke)$'`
and repeat with `REFERENCE=1`. The bounded feature smoke selector is
`make check RUN='^TestReferenceInputValiditySmoke$'` (also with `REFERENCE=1`).

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
Scalar RECORD cases retain genuine result metadata for lawful typename
shortcuts and distinguish anonymous-composite input errors from missing
functions. UNKNOWN cases retain final TEXT and inferred parameter metadata;
VOID protocol cases distinguish non-NULL empty output from SQL NULL.
The three concrete statistics types have separate `castImplementedNullOnlyTypes`
roles: typed NULL storage, parameters and casts, with exact rejection of all
non-NULL text input. They have no fabricated non-NULL seed or collector source
in the Cartesian inventory. Select the bounded role scenario with
`RUN='^TestReferenceCastImplementedInternalRoles$'` in both modes.
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

The canonical array-compatible `int2vector` and `oidvector` types belong to
`Identifiers`, both with literal seed/output `1 2 3`. Their typed storage,
parameter and text input/output routes participate in `Inventory` and
`MatrixIdentifiers`; `FunctionsIdentifiers` retains only actual released
callable signatures. Vector companions `_int2vector` (1006) and `_oidvector`
(1013) remain metadata-only; their presence does not grant companion codecs.
REGCONFIG[] (3735) and REGDICTIONARY[] (3770) are executable companion roles
in the array cast inventory, including typed storage, parameters and recursive
element coercions. Their expected element spellings distinguish zero OID `-`
from SQL NULL.
The `MatrixIdentifiers/oidvector_codec` public control also checks literal
nonempty/empty/SQL-NULL casts before Normalize: native strings, pinned lib/pq
bytes, and actual OIDVECTOR result metadata on direct and fresh routes.

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
- RegTS reads genuine builtin simple/english_stem/arabic_stem/armenian_stem, standalone dictionary definitions and
  installed unaccent identities through `pg_ts_dict`; alias rows use the local
  `regtsAssertAliasRows`, not the generic fixture type inventory. Dictionary
  scenarios independently check typed TEXT[] output, ordered options, current
  ownership, dependencies, prepared invalidation and emitted native-control SQL.
  Installed member rename/schema changes retain actual identities, template and
  extension dependencies. Native controls preserve explicit admitted Rules and
  owners, including builtin/member name cycles, then replay the actual emitted
  SQL on PostgreSQL. Missing Rules is rejected atomically. Restore collision
  witnesses retain a temporary dictionary at the exact imported dictionary OID
  and check its identity and lexization as imported rows/defaults/views relocate.
  Warmed all-constant lexize statements retain their accepted value independently
  of fresh constant plans and current parameterized-token calls.
  Dictionary TEXT arguments retain the [shared typed-nil byte adapter contract](../strings/README.md):
  native typed-nil bytes become empty TEXT (`{}`), while lib/pq sends SQL NULL.
  Ordinary nil is SQL NULL on both; nonnil empty bytes and empty strings produce
  genuine empty arrays on both. This boundary never changes function selection.
  Snowball cancellation retains native ordinary-context DDL/direct/prepared
  rejection and full Dump preservation. Shared direct/held routes gate on the
  actual parameterized Snowball result, observe the exact advisory waiter and
  blocker independently, then cancel and join before checking dictionary state
  and subsequent lexization on the same physical owner. PostgreSQL requires the
  complete user-request `57014` diagnostic; this witnesses admitted SQL after
  lexization, not interruption inside a particular stemmer instruction. Held
  statements close before interrupted borrowers return to the fixture.
  Synonym scenarios use the shipped `synonym_sample` data, distinguish default
  and sensitive lookup, and retain dictionary-origin prefix flags through query
  conversion while projecting spelling alone in TEXT[] and TSVECTOR. They cover
  ordered duplicate options, held constants versus runtime parameters, actual
  emitted controls, retained same-class TEMP collisions and output-gated
  cancellation. Numeric-OID parameters preserve captured dictionary identity;
  a stale named literal is resolved again after rename or schema movement.
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

Dictionary definitions and lexization: `RUN='^TestReferenceDictionary'`.
Ispell dictionaries and explicit resource lifetimes: `RUN='^TestReferenceIspell'`.
The shared manifest registers and installs the same released bytes, including
the default MySpell affix file paired with `ispell_sample.dict`. Provenance and
license are in `testdata/ispell`; malformed fixtures are explicit manifest bytes.
Malformed-reader regressions: `RUN='^TestReferenceIspellMalformedReaders$'`.
These distinguish initialization-time and deferred long-flag demand, required
MySpell compound operands, and permissive legacy-format ignored lines.
Extension member identity/persistence: `RUN='^TestReferenceDictionaryExtension(Identity|Controls)$'`.
Held all-constant characterization: `RUN='^TestReferenceDictionaryPreparedConstants$'`.
English Snowball morphology, options, identities and native lifecycle:
`RUN='^TestReferenceSnowball'`.
Arabic Snowball morphology, normalization/byte boundaries, language-only stock
identity, explicit English stopwords, full diagnostics and native replay:
`RUN='^TestReferenceArabicSnowball'`. The fixed corpus is independently derived
from REL_18_6; held constants and live parameters distinguish provider changes.
Armenian Snowball morphology (all 191 released suffix witnesses), region/byte
boundaries, language-only stock identity, explicit English stops and native
dictionary/seed replay: `RUN='^TestReferenceArmenianSnowball'`. Corpus goldens
trace the released ending→verb→adjective→noun order independently of production.
Stock synonym data, options, conversion, authority and native lifecycle:
`RUN='^TestReferenceSynonym'`.
Default unaccent caller resolution after dictionary/routine DDL:
`RUN='^TestReferenceDictionaryUnaccentCaller$'`.

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
