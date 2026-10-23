# PostgreSQL 18.6 comparison contract ledger

Evidence behind the comparison tests in this package (`comparison_*_test.go`).
Selectors and test-writing rules are in the [README](README.md#testing).

## Method

- Facts come from the REL_18_6 catalogs `pg_operator.dat`, `pg_cast.dat` and
  `pg_type.dat` and the parser sources `parse_oper.c` (`binary_oper_exact`,
  `oper_select_candidate`, `make_op`), `parse_func.c` (`func_select_candidate`)
  and `parse_coerce.c` (`can_coerce_type`, `find_coercion_pathway`,
  `check_generic_type_consistency`). The pinned oracle resolves observable values,
  metadata and diagnostics.
- `comparison_facts_test.go` encodes every identity (name, fixed OID, source
  samples) and classifies every ordered pair as an index into the released
  signature/routine vectors. Vector positions are `=, <>, <, <=, >, >=`; `!=` uses
  `<>`. A zero entry is an explicit released no-match fact, never a default for a
  missing classification. `comparisonSignature` rejects incomplete dimensions and
  invalid indices.
- The facts are test evidence, never native dispatch. No native operator or cast
  projection, provider gate or comparison result produces an expectation.
- Resolution as encoded: an exact signature wins; otherwise only direct implicit
  conversions, domain rules, exact-position and preferred-category filtering and
  generic consistency apply. There is no transitive coercion search and no
  assignment or explicit I/O fallback. Each vector records the selected formals;
  a source type that differs from its formal requires the released implicit
  conversion. Formals are source-derived; public metadata need not expose them.

## Inventory

| Quantity | Count |
| --- | ---: |
| Concrete identities (56 non-array, 31 builtin arrays) | 87 |
| Ordered pairs | 7,569 |
| Pair/spelling cells | 52,983 |
| Lookup-admitted / analysis-denied cells | 2,524 / 50,459 |
| Denied cells of partially admitted pairs (XID/integer, XID, CID, ACLITEM), executed in full | 24 |
| Empty pairs (no released operator in any slot) | 7,205 |
| Equality-admitted pairs | 364 |
| Wire contract: non-null-decodable sources | 86 |
| Wire contract: cells (admitted / denied) | 51,772 (2,447 / 49,325) |
| Classified BETWEEN triples (87³) | 658,503 |
| Ordering-admitted triples | 4,103 |
| Executed triples (668 ordinary, 3 leaf-demand) | 671 |
| Executed triple/form/assignment outcomes (6 forms × 27 assignments) | 108,216 + 486 |
| Released operator rows in the supported inventory | 552 |

The three leaf-demand triples (JSON[], JSONPATH[], ACLITEM[]) contribute 198
strict-NULL successes and 288 execution-demand errors. Executed BETWEEN triples
pair each ordering-eligible identity with itself and with the next eligible
identity, so every identity is both a lower and an upper bound.

## Construction routes

`comparisonFiniteSetup` builds typed columns with low/high/NULL rows
independently of comparator availability; expression-only arrays use their real
constructors. Identities without a declarable column use real sources:

- internal CHAR: `pg_attribute.attgenerated` and `pg_class.relkind`;
- `pg_node_tree`: `pg_constraint.conbin` and `pg_attrdef.adbin`, compared only
  with themselves or predictable text bounds, never copied node serialization;
- VOID: `pg_sleep(0)`;
- deferred RECORD: `NULL::record`;
- canonical ANYARRAY: `pg_attribute.attmissingval` NULLs.

User enums, domains and domain chains, composites, arrays of domains and
occurrence shapes (CYCLE `RECORD[]`, `information_schema.time_stamp`) are
parameterized rules, not finite identities; their OIDs are backend-owned, never
hardcoded. The 87-identity corpus is fixed by the facts file; comparison
witnesses for types outside it (geometry, MACADDR, text search, query_int) live
in [reference/types](../types/README.md#testing).

## Coverage per definition

- `Matrix` shards: every admitted or partially admitted pair/spelling with nine
  low/high/NULL combinations, empty BOOL metadata, WHERE-only-true, analysis
  errors on populated/all-NULL/empty inputs and array leaf demand; each empty pair
  once per analysis route.
- `Between` shards: all triples classified before setup; executed triples run six
  forms over 27 assignments. Lower and upper bounds resolve independently.
- `PredicateErrors` shards: each denied ordered pair at each endpoint.
- `Distinct`, `DistinctParameters`, `NullTruth`, `NullOnlyRoutes`: distinctness
  over all pairs; null and truth predicates over every value route.
- `Consumers`, `ConsumerReductions`, `BatchSelection`: every spelling cell
  through row, ANY/SOME, ALL, array constructors and IN/NOT IN; common-type IN
  selection versus pairwise fallback.
- `Boundaries`, `Structural*`, `Record*`, `RowEligibility`, `Composite*`,
  `NodeTreeLeaf`, `AnyarrayCatalog`, `InstalledQueryInt`, `DomainArrayRoutes`,
  `CycleRecordArray`: value extremes, structural, record and array rules and the
  special routes above.
- `PreparedLifecycle`, `StoredControls`, `StoredConstructor*`: held reanalysis,
  stored views/defaults/checks, and native Dump/Reset/Restore with PostgreSQL
  replay of the emitted SQL.
