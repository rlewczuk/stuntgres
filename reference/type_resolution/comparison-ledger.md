# PostgreSQL 18.6 comparison contract ledger

## Evidence provenance and status

Authority: PostgreSQL18 [9.2](https://www.postgresql.org/docs/18/functions-comparison.html),
[9.25](https://www.postgresql.org/docs/18/functions-comparisons.html),
[10.2](https://www.postgresql.org/docs/18/typeconv-oper.html), and the released
[REL_18_6 catalogs/parser/ADT sources](https://github.com/postgres/postgres/tree/REL_18_6/src).
The fixture pins `postgres:18.6-bookworm@sha256:afc7e2d441324c0388fa80c3d24f733b4194a4eb7f47dd8ee2b08eb1a24a647c`,
UTF8/C/C/C, server_version_num180006. Local20devel is research-only.

Independent release facts were exercised against pinned18.6 before meaningful
native regressions and production remedies. Root verification reports retain
that history. The completed source-fresh round passes full quickcheck, all17
selected native/reference groups and two selected race groups; no private
discovery gates remain. Independent review, integration and post-merge finalcheck
are separate delivery gates, not claims made by this ledger.

The finite contract is **87 identities,7,569 ordered pairs,52,983 spelling cells**:
2,524 lookup-admitted and50,459 analysis-denied cells. The wire contract is86
nonnull-decodable sources,51,772 cells,2,447 admitted and49,325 denied, with fresh
nonnull values, canonical source outputs and compatible-path reanalysis.
All eight finite and BETWEEN shards, exceptional/structural/predicate/consumer
definitions, stored controls and original1043 application pass both modes.

Real supported operator inventory contains552 rows, including genuine RECORD
operators2988–2993 and their released routine/commutator/negator associations.
No synthetic NAME/VARCHAR or PG_NODE_TREE input-pair operator is published.
The three newly supplied cast rows are `18,25,946,i,f`, `194,25,0,i,b` and
`1083,1186,1370,i,f`; unchanged bounded cast inventory is not broadened.
Public OID fields remain PostgreSQL strings versus native int64, independently
asserted rather than changing adapter codecs to obtain parity.

Observed latest executable smoke uses the built authenticated TCP CLI, raw
Parse/Describe/Bind clients, public lib/pq and embedded adapters, SQL PREPARE,
stored constructors, node-tree leaf, exact diagnostic classes and VARCHAR
window ordering; independent psql returns `t|t|t`. Graceful stop joins exit0.

## Finite facts, selected formals and coercions

`comparison_facts_test.go` explicitly encodes all87 names/fixed OIDs/source
samples and every ordered pair as an index into51 released signature/routine
vectors. The six vector positions are `=, <>, <, <=, >, >=`; `!=` aliases `<>`.
A zero is an explicit no-match fact, not a default for missing classification.
`comparisonSignature` rejects incomplete dimensions or invalid indices.

Facts originate in `pg_operator.dat`, `pg_cast.dat`, `pg_type.dat` and
`parse_oper.c:binary_oper_exact/oper_select_candidate/make_op`,
`parse_func.c:func_select_candidate`,
`parse_coerce.c:can_coerce_type/find_coercion_pathway/check_generic_type_consistency`.
They are test evidence, never native dispatch. No native operator/cast projection,
provider gate, comparison success, or native result produces an expectation.

Exact signatures win; then only direct implicit eligible conversions, domain
rules, exact-position/preferred-category filtering and generic consistency.
There is no transitive graph search or assignment/explicit IO fallback.
Source/routine formals are recorded in each vector; a source type differing from
its selected formal requires the released implicit occurrence conversion.
Examples: NAME/VARCHAR selects NAME/TEXT, not clipped NAME/NAME; reverse selects
TEXT/NAME. VARCHAR/BPCHAR selects BPCHAR/BPCHAR by exact-position filtering.
Internal CHAR and PG_NODE_TREE can select TEXT. TIME/INTERVAL selects INTERVAL.
OID/integral/cross-alias signatures use OID. Selected float conversions genuinely
round, while exact integer/NUMERIC operations retain values beyond2^53.

CID385 and ACLITEM974 have `=` alone. XID/int4 has353/3316; reverse/order is not
inferred from a commutator. Generic array/enum/range/multirange/record lookup is
separate from generic identity consistency and callable leaf demand. Public
BOOL/source metadata does not expose every internal formal: vectors are
**source-derived**, with oracle observable values corroborating behavior, not
claimed wire descriptions of internal conversions.

## Executable routes and exclusions

The87 identities are56 nonarray +31 concrete builtin arrays. Inventory sources:
`internal/sqltype/type.go` builtin constructors and concrete/value authority;
`builtin_identities.go` explicit concrete versus lookup-only associations;
`internal/engine/types.go` declaration/cast/contextual storage rules.
`comparisonFiniteSetup` constructs typed columns/low-high-NULL rows independently
of comparator availability. Expression-only arrays use their actual constructors.

Special non-declaration routes:

* Internal CHAR: real `pg_attribute.attgenerated` / `pg_class.relkind`; contextual
  explicit wire18 is separate. No new raw CHAR declaration/cast is assumed.
* PG_NODE_TREE: actual `pg_constraint.conbin` / `pg_attrdef.adbin`. Compare against
  self or independently predictable text boundaries, never copied physical node
  serialization; `NodeTreeLeaf` uses actual default/check catalog payloads inside
  RECORD fields, not `pg_node_tree_in`.
* VOID: balanced zero-duration `pg_sleep(0)` is nonnull; not excluded as pseudo.
* Deferred RECORD: real `NULL::record` and Parse2249, no fake zero-field value.
* Canonical ANYARRAY: real `pg_attribute.attmissingval` NULL. No generic input or
  codec is invented, and NULL success proves no nonnull leaf comparator.
* User enums/domains/chains, enum arrays, named/nested composites and arrays,
  whole-array versus element domains, information_schema.time_stamp, CYCLE
  occurrence RECORD[] are parameterized rules, not additional finite stock IDs.
  User OIDs are backend-owned rather than hardcoded allocator sequences.
* Integrated grouped comparisons, null/truth predicates and nested parameters
  yield the existing BOOL identity, not an88th family. Existing BetweenGrammar,
  BetweenGroupedDefaults/PredicateDefaults and CompareNull parameter witnesses
  independently exercise these upstream routes in both modes.

Construction-only tests establish domain-over-MONEY/TID/range array routes.
XID/CID/XID8 domain setup natively reports ordinary `invalid array descriptor`,
before comparison; this observed non-comparison publication prerequisite does
not exclude their scalar comparison cells. General enum-array annotation/storage
may be unavailable while expression ARRAY construction is executable.

Excluded stock companions: temporal multiranges; MONEY/TID/network/transaction/
operator-alias/regconfig/regdictionary/CHAR/bit/range arrays lacking concrete
builtin authority/codecs. This does **not** exclude separately constructible
arrays of domains or occurrence shapes. Other lookup-only identities (geometry,
MAC/XML/text-search/LSN/snapshot/vector/handler/internal/refcursor and unpromoted
information_schema domains) lack actual value/input providers; an OID naming a
type is not a value of that type. Installed query_int is an executable
parameterized negative-space class; no extension is installed to inflate T.
Missing comparison/coercion on an admitted route is never an exclusion.

## Complete enumeration and independent outcomes

`comparison_matrix_test.go`: every pair/spelling, nine low/high/NULL combinations,
direct plus genuinely held statement twice, empty BOOL metadata, WHERE-only-true,
empty/populated/NULL analysis errors and array leaf demand. English42883 hint and
character position are independent; `!=` uses canonical `<>` diagnostics.

Remainder definitions:

* Eight `Between{Numeric,Strings,Temporal,Identifiers,BinaryNetwork,NegativeSpace,Ranges,Arrays}`
  shards classify87³=658,503 triples. Released ordering lookup admits4,103
  ordered triples (sum of each left identity's ordering degree squared),
  through six forms =24,618 triple/forms. Of these,4,100 successful triples
  project27 low/high/NULL assignments each (664,200 triple/form/assignments).
  Three same-type triples (JSON[],JSONPATH[],ACLITEM[]) require unavailable leaf
  ordering. All3×6×27=486 assignments pass both modes:198 strict-NULL successes
  and288 execution-demand errors,11/16 respectively per27/form. Prepare succeeds;
  direct execution and a genuinely held statement executed twice fail42883
  without hint/position when a nonnull pair first demands array_cmp's leaf.
  NULL x or two NULL bounds avoid demand; UNKNOWN does not short-circuit the
  other AND/OR child. Case names retain lower-first versus upper-first demand.
  Thus664,686 admitted triple/form/assignments are actually exercised.
  Lower/upper selection is independent, not a global common type.
  PredicateErrors covers each denied ordered pair at each endpoint across
  six forms and empty/populated/NULL inputs.
  Classification is not execution: all658,503 triples receive released-rule
  classification, while denied triple behavior is represented by the independently
  enumerated failed pair/endpoint/error-stage witnesses, not falsely reported as
  executing every denied triple. Before setup, each left classifies all7,569
  lower/upper combinations; only ordering-eligible (left,lower) windows allocate
  fixtures, each retaining every eligible upper, six forms and27 assignments.
  No context/deadline increase or coverage sampling; projections remain32-wide
  direct plus the same held statement executed twice.
* Distinct visits all7,569 pairs;364 released equality-admitted pairs, both total
  Boolean forms, nine assignments; leaf demand versus whole SQL NULL is explicit.
  CID/ACL use `=` only, never a fabricated `<>` provider.
* NullTruth visits all87 actual value routes: four null predicates and six truth
  predicates. BOOL low/high/NULL and existing Boolean-domain tests cover truth
  tables; every nonboolean route has full42804 diagnostics. NullOnlyRoutes adds
  canonical ANYARRAY/deferred RECORD/time_stamp; unknown42P18 remains separate.
* AnyarrayCatalog uses actual catalog attmissingval NULLs, not NULL::anyarray,
  input/codec invention or zero-element data. Seven symbols on two canonical
  ANYARRAY operands, and either-side unknown NULL, fail generic enforcement
  42804 `cannot determine element type of "anyarray" argument` without hint or
  position. Known integer[] contexts instead fail42883 `operator does not exist:
  anyarray OP integer[]` (reverse preserves source order), with standard no-match
  hint and operator/predicate location. Candidate filtering by
  `can_coerce_type`/`check_generic_type_consistency` rejects differing known array
  identities (REL_18_6 parse_coerce.c:1782–1789) before make_op can reach generic
  enforcement at2375–2389. Pinned observations distinguish candidate no-match
  from generic enforcement before strict NULL execution; completed definitions
  pass both modes, including concrete-array operand order and error locations.
  Both distinctness forms and six BETWEEN forms retain this analysis stage;
  raw-NULL DISTINCT rewrites and four NULL aliases are separate successes.
  Six nonboolean truth diagnostics retain42804 and expression-start position.
  NULLIF and simple CASE on actual attmissingval operands preserve that same
  generic42804 record, including absent hint/position, through direct analysis
  and Prepare. Conditional equality does not re-format a central resolver error
  as operator no-match; ordinary conditional/JOIN lookup controls remain separate.
* InstalledQueryInt installs only the existing intarray1.5 fixture/type owner.
  Its seven scalar symbols deny same/different values, either typed NULL, actual
  empty/populated/all-NULL stored inputs, with canonical !=/<> English42883,
  hint and position at analysis/Prepare. Six BETWEEN forms, typed distinctness,
  truth tests, IN/NOT IN and all seven ANY/SOME/ALL subquery symbols retain
  independent operator/analysis errors. Raw-NULL distinctness and NULL aliases
  succeed without proving equality admission. The automatic query_int[] identity
  has no admitted ordinary native storage/array codec (the existing
  reference/types/query_int_metadata_test.go distinguishes non-storable array
  identity); this does not exempt scalar or subquery comparisons. No extension
  operations or finite identities are added. Completed definitions pass both modes.
  Native route evidence: `internal/engine/intarray_provider.go:47` constructs the
  companion with `sqltype.LookupArray`; validation at97–99 requires that array
  to be non-Concrete. Scalar input/output remains the installed bqarr_in/out
  owner at64–68. Released `contrib/intarray/intarray--1.2.sql` defines query_int
  and @@/~~ consumers, not standard query_int comparison overloads.
* Consumers visits all52,983 spelling cells and executes each of2,524
  lookup-admitted spellings through one-field row, ANY/SOME/ALL subquery,
  applicable concrete-array constructors and IN/NOT IN where the spelling is
  applicable. Empty subquery reductions include NULL lhs. Dedicated reductions
  distinguish NULL container/element, decisive match/miss, duplicate preservation
  and row first-decisive/3VL. Array capability is determined by source inventory,
  not comparison success; array operands do not become arbitrary nested arrays.
  Execution windows contain at most two applicable right identities for each
  left; all609 right/spelling cells per left are visited before fixture setup.
  Excluded rights create no dead fixture contexts. BatchSelection distinguishes
  successful common-type array selection from pairwise fallback, including both
  TIME/INTERVAL orientations and asymmetric XID/integer, with concrete true BOOLs.
  Released `parse_expr.c:transformAExprIn` passes NULL error context to
  `select_common_type`; failed selection/verification falls back to pairwise OR,
  rather than emitting the incorrectly predicted IN42804. This also permits
  distinct named-composite multi-item IN via the existing row/record path.
* Boundaries fixes exact >2^53/integer extrema versus float rounding, NaN,
  infinities/signed zero, full NAME tails/UTF8 clipping, BPCHAR padding, unsigned
  OIDs/aliases/transaction IDs, bit lengths, temporal infinities/offsets/interval,
  network/UUID, array rank/lower bounds/NULL fields, canonical ranges/multiranges
  and JSONB. Structural retains domain chains/enum identity and enum-domain
  denial, whole-array/element domains, distinct composite names and row-null rules.
  Structural ordinary-composite ANY/SOME/ALL and NOT IN subqueries use a scalar
  subquery on the left to retain one composite-valued operand against one column.
  Released `transformSubLink` instead expands a transformed RowExpr into fields;
  the original mismatched route is retained as exact42601/ANY-position denial.
  Explicit supported ordinary-composite versus field-wise-row NULL witnesses
  distinguish true from NULL equality and true from NULL ordering. Good nested
  records, composite domains and composite arrays join the structural seven-
  spelling/predicate/consumer witnesses, all independently exercised in both modes.
* PreparedLifecycle retains real statements over fresh arguments/NULL, compatible
  table reanalysis/current data and TimeZone recapture. StoredControls exercises
  views/defaults/checks and symbolic enum/domain/composite identity with an
  explicit native Dump/Reset/Restore/redump phase after shared SQL expectations.
* StoredConstructor/Lifecycle/Replay/Dependencies normalize quantified named ROW
  casts into analyzed members through the existing stored-syntax/capture owner.
  The erased outer type can be dropped/recreated without retargeting the view;
  actual member, nested, whole-value and unused-CTE identities remain dependencies.
  VARCHAR(2) coercions survive scalar/star sources, outer-type drop and replay.
  Enum declaration order and renamed-label identity survive native logical
  roundtrip and PostgreSQL replay of actual emitted SQL. Prepared original-source
  manifests remain unchanged. Native direct enum DROP is an independently
  asserted existing0A000 non-comparison DDL limit; real pg_depend edges and values
  are still required, not skipped.
* NodeTreeLeaf/RecordUnknownFormals/DiagnosticVisibility cover catalog node leaf
  eligibility, unknown literal versus NULL RECORD formals and visible/nonvisible/
  shadow-schema demanded names. Builtin diagnostics retain SQL aliases such as
  integer/bigint/json, not raw catalog names or identifier quoting.
* DistinctParameters retains typed scalar source formals across low/high/NULL
  direct and held calls for every eligible input-codec pair. Catalog-only
  CHAR/node-tree/VOID and array occurrences retain their finite actual routes.
  DomainArrayRoutes exercises every spelling and NULL-element order for the
  separately admitted MONEY/TID/range domain arrays, plus all six BETWEEN forms
  over nonnull and NULL-element arrays. CycleRecordArray compares actual
  occurrence-owned CYCLE paths
  rather than a fake stock RECORD[] codec.

Records use exact reached field identities, NULL-field equality/last ordering,
whole-SQL-NULL strictness and early decisive outer frontiers; reached nested
record/array typcache eligibility can reject recursively before inner value
shortcuts. Arrays retain shape-first equality versus comparator-first ordering.
Row constructors retain scalar coercions/3VL and multi-field B-tree restrictions.
Physical record-image symbols are a separate non-engine boundary: native ordinary
`stuntgres: invalid comparison operator`, no SQLSTATE. Go memory/logical equality
is not PostgreSQL Datum/detoasted-byte equivalence.

Predicate diagnostics use the entire original spelling as their source-position
marker: NOT for all three NOT BETWEEN forms, BETWEEN otherwise. Released
`gram.y` gives the A_Expr the first keyword location, and
`parse_expr.c:transformAExprBetween` passes `a->location` to every lowered operator.
Both direct analysis and Prepare preserve independently asserted English,
SQLSTATE, source character positions and empty/populated/NULL error stages.

## Reproduction and verification limits

Exact bounded selectors and standalone commands live in the owning
[README Testing section](README.md#testing); common fixture/lifecycle rules stay
in `docs/TESTING.md`. Root uses external900s selected/quick/race deadlines and
1800s post-integration finalcheck; no refcheck.

Remaining backend-specific branches assert actual OID representations, missing
non-comparison construction/codecs, record-image ABI rejection and native
Dump/Reset/Restore controls. None is an oracle-discovery bypass or permission to
skip an executable comparison route.

| Changed owning package | Go source bytes |
|---|---:|
| reference/type_resolution | 840,897 |
| reference/sessions | 912,755 |

All35 changed functional files are at most363 lines, below750; no new reference
package was created. Capacity counts cover source, not generated identities or
executed SQL coverage. Source-fresh whole-project verification and actual smoke
precede these final evidence/documentation updates.

