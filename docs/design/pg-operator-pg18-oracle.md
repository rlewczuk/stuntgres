# PostgreSQL 18 operator metadata admission evidence

## Provenance and status

The companion `pg-operator-pg18-supported.tsv` is an independently constructed
expected inventory of admitted records from799 released identities. The original
554-record checkpoint included3552 through the bounded exclusion provider;
subsequent feature admissions are recorded below. The inventory
has a header and exactly15 raw catalog columns,
ordered numerically by OID. Root confirmed the initial531-row metadata against
pinned PostgreSQL18.0 before production. Subsequent rebases synchronize existing
TID/INET, ACL-inclusion, RECORD and exclusion providers.
The current completed definitions require fresh root-owned proof; source facts are not a pass.
No native query output, production metadata, or local PostgreSQL 20devel catalog
was used to construct metadata values. Existing eight-column REGOPERATOR evidence
and all 799 alias identities are unchanged.

### BOX geometric operator admission

PG18-TYPE-GEOM-BOX adds only the six released BOX scalar operators:
499 (`~=`),501 (`>=`),502 (`>`),503 (`=`),504 (`<`),505 (`<=`).
Their independent15-field rows come from
[REL_18_6 pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_operator.dat)
and procedure identities from
[REL_18_6 pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat),
not from native projection output. Namespace11, owner10, binary kind and BOOL16
are fixed; all six have false merge/hash flags. Coordinate sameness and area
equality have no negator. Area inequalities retain their released reciprocal
commutator/negator relationships, procedures126–130, and areasel139/
areajoinsel140; sameness uses186 and eqsel101/eqjoinsel105.

The six rows extend the pre-BOX564-row file to570, while all799 alias identities
remain unchanged. Historical554-row checkpoints below remain historical.
Production SupportedOperatorMetadata is the existing owner of catalog row
publication, separate from actual operator-routine associations. Both must agree
with genuine providers. This promotion adds no BOX default B-tree/hash class,
spatial operators, POINT comparisons, or companion-array execution.

Selected proof must include all consumers of the independent inventory: direct/
prepared catalog inventory, provider agreement, complete relationships, permissions,
polymorphic identity, lifecycle and FeatureSmoke, in both native and pinned modes.
Fresh proof for these additions remains root-owned.

### Circle scalar operator admission

PG18-TYPE-GEOM-CIRCLE admits exactly19 circle×circle signatures:
1500–1515,1520,2589 and2590. Their complete15-field expected rows are transcribed
from [REL_18_6 pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_operator.dat)
and defaults, with implementation and estimator OIDs resolved independently from
[REL_18_6 pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat).
All have namespace11, owner10, binary kind and false merge/hash flags.
The result is BOOL16 except distance1520, which returns FLOAT8 701.
Area ordering and overlap use areasel139/areajoinsel140; position predicates
use positionsel1300/positionjoinsel1301; containment uses contsel1302/contjoinsel1303.
Area equality/inequality and sameness retain their released equality estimators.
Missing commutator/negator/estimator fields remain zero, not inferred from
geometric symmetry. No native output or production table generated these facts.
The existing unfiltered inventory, symbolic-reference and real-binder admission
proofs consume this expansion unchanged. Circle scalar operators do not imply
default B-tree/hash classes, grouping or sorting. Root must observe both selected
native and pinned PostgreSQL modes on these completed definitions.

### JSONB JSONPath operator admission

PG18-FUNC-JSONB-PATH-OPS adds only JSONB/jsonpath operators 4012 `@?` and
4013 `@@`, raising the preexisting 552-record TSV to 554 records. Their complete
15-field rows are grounded in
[REL_18_6 pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_operator.dat)
and catalog defaults: namespace11, owner10, binary kind, BOOL16, no merge/hash,
no commutator/negator, procedures4010/4011 and matchingsel5040/matchingjoinsel5041.
The routines execute through the actual existing evaluator consumers, with
strict silent suppression; this does not admit text-search or adjacent
arithmetic/datetime execution.

`routine.SupportedOperatorMetadata` initializes each seeded descriptor's result,
implementation and estimator relationships and determines its pg_operator row
admission. Routine associations alone do not publish that metadata. Existing
inventory, relationship and lifecycle scenarios consume the independent TSV
without a second feature-specific catalog inventory. Root-owned selected proof
must cover the revised rows in both modes; the historical checkpoints and
partition lists below are not new 18.6 observations.

### User-operator cutover

PG18-FUNC-OPERATOR-USER supersedes the earlier builtin-only mutable-catalog
boundary. The fixed554 supported seed rows and all799 alias identities remain
distinct; custom definitions and shells add live rows without inventing pg_proc
rows or broadening lookup-only seed execution. Selected calls bind the actual
routine/provider and retain operator identity for dependencies and live SQL names.
RESTRICT/JOIN are truthful released metadata, not optimizer invocation. New
reference selectors and native emitted-script replay are documented in
`reference/operators/README.md`; verification remains root-owned.

Shell completion replaces owner and all supplied/omitted definition fields at
the original OID; omitted commutator/negator links become zero, even when peers
still point to that completed operator. A third operator cannot steal an
already-populated reciprocal field. These are source-grounded
[REL_18_6 OperatorCreate/OperatorUpd rules](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/pg_operator.c),
not a universal symmetric-link invariant or a claim of observed oracle results.
The dedicated shared shell-completion regression and fresh root-owned pinned
proof cover this edge. Estimator validation units use lookup metadata only:
no estimator callable declaration, public pg_proc row or optimizer is fabricated.

Dump reconstructs the captured **final state**, not operator allocation history.
For each link kind independently, an asymmetric link to a defined target stays
in CREATE and orders its source declaration before target completion. Reciprocal
defined-target links (including self-commutators) are omitted from CREATE and
restored with ordinary ALTER OPERATOR after their exact endpoint declarations.
One per-source link-completion event gates semantic consumers; structural
asymmetric and endpoint edges remain raw declarations, so valid reciprocal and
cross-kind cycles are not normal dependency cycles. Normalization is reapplied
after default retries and expanded replay phases. No live descriptor is modified
and no persisted creation history or global all-operators barrier is introduced.
The partition replay source-order chain contains only nonoperator declarations;
operator declarations still participate in real dependency/late-phase propagation,
but never turn an unrelated type declaration into an artificial link consumer.
Unfilled-shell links remain forward CREATE references, including both link kinds;
orphan shells retain their genuine transient provider-bound generator and DROP.
CREATE/link replay uses bootstrap authorization while ordinary OWNER postludes
preserve independent definition and shell owners, including owners without
schema privileges. `TestReferenceUserOperatorReplayGraph` covers both allocation
orders, actual emitted-byte replay, separate owners and equivalent redump.

CREATE validates SETOF on **each** LEFTARG/RIGHTARG element during the ordered
option pass, before a duplicate overwrites its value. Valid repeated arguments
and FUNCTION/PROCEDURE retain last-value semantics; superseded valid argument
names do not acquire extra type lookup. `OrderedArgumentValidation` asserts both
invalid earlier argument cases, no operator/shell publication, sentinel survival
and a real int4pl repeated-argument definition returning5.

Review3 R5–R7 replace the lossy option carrier with explicit presence and
NoArgument/Type/Name/String/Integer/Float/None kinds. PostgreSQL
[definition-element extraction](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/commands/define.c)
is local to operator DDL: every recognized CREATE element extracts before
last-valid-value retention; strings remain one literal component, including empty
strings, dots and SQL-looking text. String type names bypass SQL alias
canonicalization, not catalog identity resolution. Unknown CREATE warnings and
obsolete merge attributes keep their previous ignored-value rules.
The existing operator-only lexer context maps reserved RHS keywords such as
NULL/DEFAULT to String arguments, without broadening ordinary identifiers or
type input. CURRENT_SCHEMA retains its TypeFunctionName/TypeName route.
Type components retain original generic
names such as int4 even where the operand declaration name is canonicalized;
actual SQL type aliases instead retain canonical pg_catalog components.
Generic option typmods survive parsing for extraction; name extraction ignores
them, while supplied operand types use existing typmod validation.

Definition Booleans accept exact case-insensitive true/false/on/off, bare flags
and Integer-node values0/1, not GUC/Boolean-input prefixes. NumericOnly classifies
the unsigned int32 token before applying its sign; decimal/exponent/out-of-int32
tokens remain Float. ALTER maps only bare/unquoted NONE to NULL and first extracts
every other supplied value as a name, even immutable/unknown/Boolean attributes.
Thus numeric ALTER flags fail name extraction, while bare/NONE flags enable and
bare/NONE RESTRICT/JOIN clear. Quoted/literal none must undergo real name lookup.

CREATE keeps namespace CREATE and ordered extraction first, then requires the
implementation option, resolves surviving left/right identities, diagnoses
missing operands/postfix, checks operand type USAGE, then looks up/admit the
implementation and checks result type USAGE. Next it resolves only the surviving
restriction estimator, then join estimator, validates the new operator name,
validates parameter combinations, and only then detects a duplicate definition
or checks ownership of an existing shell. CREATE then resolves/creates and
authorizes the commutator, followed by the negator, and rejects self-negation.
Only after both peer checkpoints succeed does it stage the source definition
and check/update reciprocal commutator then negator fields. A reciprocal
commutator conflict therefore cannot hide an earlier invalid, inaccessible or
self-referential negator. CREATE accepts legitimate forward shells; ALTER's
shell-reference rejection does not apply to this phase.
This is the boundary between
[REL_18_6 DefineOperator](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/commands/operatorcmds.c)
and
[OperatorCreate/OperatorValidateParams](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/catalog/pg_operator.c):
a missing estimator beats an invalid name, duplicate or shell-owner error, and
invalid parameters beat duplicate/shell-owner errors. Superseded valid names are
never eagerly resolved. The existing combined resolve/authorize helper remains
for utility signature callers only; no provider or authorization authority changes.

Shared parameter validation follows two complete source passes, not interleaved
per-option tests. For a nonbinary signature, check commutator, join selectivity,
MERGES, then HASHES. Only afterward, for a non-Boolean result, check negator,
restriction selectivity, join selectivity, MERGES, then HASHES. Thus a unary
integer operator with NEGATOR and HASHES fails `only binary operators can hash`
in both CREATE and ALTER, before any reciprocal mutation.

ALTER retains its different post-owner order: surviving restriction/join
estimators; commutator/negator lookup and ownership including self-negation;
protected commutator/negator; protected MERGES/HASHES; parameter validation; then
private reciprocal/source updates. CREATE ordering must not be imposed on ALTER.
The public `CreationCheckpoints` and `ParameterCheckpoints` regressions specify
full diagnostics and unchanged graph/owners/providers/sentinel state; matching
pure units inspect actual catalog stages and published predecessors. Fresh
dual-mode proof remains root-owned, as do the retained option, ACL and replay
regressions. Source explanations and readiness are not passing evidence.

Review6 R12 separates peer setup from reciprocal updates without live rollback:
all writes remain in the ordinary private catalog stage, discarded on failure.
An unstaged new source's self-reference uses its reserved identity rather than
creating a duplicate shell. Reciprocal updates read the latest staged peer, so
COMMUTATOR and NEGATOR can both target one existing operator or one forward
shell without losing either link. `TestReferenceUserOperatorPeerSetupCheckpoints`
retains the root's four independent diagnostic/graph/provider/sentinel cases;
`TestUserOperatorPeerSetupStagedRoots` inspects actual same-peer forward shells,
stable completion with self-commutation, immutable predecessors, and discard
after a later negator conflict follows a private commutator update.


Released authorities (all `REL_18_0`, not the moving development branch):
- [pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_operator.dat): every operator identity and explicit field.
- [pg_operator.h](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_operator.h): namespace pg_catalog=11, owner POSTGRES=10, kind b, flags f, commutator/negator 0, estimator '-' defaults and 15-field ordering.
- [pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat): type OIDs and generated companion-array IDs (`array_type_oid`).
- [pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat): procedure OIDs resolved by **exact ordered argument signature** for implementations, not ambiguous bare-name lookup. Estimator names each identify a unique released procedure. Explicit signature-qualified routine references are matched as signatures.
- [Catalog semantics](https://www.postgresql.org/docs/18/catalog-pg-operator.html), [operator resolution](https://www.postgresql.org/docs/18/typeconv-oper.html), and [polymorphic declarations](https://www.postgresql.org/docs/18/extend-type-system.html).

Construction parsed quoted Perl-style catalog records (braces inside quoted
descriptions are not record delimiters), applied header defaults, mapped type and
array names, resolved commutator/negator by exact operator/name/ordered operands,
and resolved implementation routines by ordered declared argument types. All 799
release rows resolved successfully before admission; every nonzero reference
exists in those released authorities. The expected TSV is not a runtime input or
a shared production generator. Procedure identity does not promise SQL pg_proc
presence or arbitrary execution; commutator/negator identity does not promise
supported catalog membership. Prefix rows use left=0, right=operand and kind=l.

The historical545 admitted bag was source-derived closed over every nonzero
commutator/negator target: no genuine absent-target builtin witness exists here.
The reference scenario exhaustively resolves symbolic targets against the
independent799-alias evidence rather than fabricating an absent target or
removing a working row. This does not impose closure on future operator parts.
The genuine routine distinction is `int4pl`177: alias lookup/implementation
reference works while native pg_proc has no SQL row for that identity.

## Historical all-family partition (545-row checkpoint)

Review authority is existing source routing in `internal/engine/expression.go`
(`OperatorExpr`, `bindComparisonOperands`, `finishArithmetic`) and the owners
below. Counts are disjoint: generic six-way comparison records belong to the
comparison bucket, not their other family buckets. All counts include only
released exact signatures whose selected operands and result match; coercing a
call to another provider does not create a second row.

| Bucket | Count | Existing admission authority and boundary |
|---|---:|---|
| Scalar and generic comparisons | 288 | `internal/engine/array_comparison.go:resolveComparisonTypes/comparisonCoercionTypes`; `internal/sqltype/type.go:CheckComparisonOperator/BindComparisonPredicate/bindComparisonTypes`. Six ordinary symbols only. Same-width supported scalar records, including all six TID comparisons, integer/float widths, exact NAME/TEXT and all released date/timestamp/timestamptz ordered pairs. ACLITEM only =; XID only =/<> for xid/xid and xid/int4; CID only =; XID8 all six. OID/integer coerces to OID/OID; no extra ordered input signature. BIT/VARBIT cross-width coerces to VARBIT, while released same-width BIT and VARBIT comparisons remain. Six each for anyarray, anyenum, anyrange, anymultirange, with actual physical witnesses rather than binding pseudo descriptors. |
| Arithmetic, unary, temporal, money, INET | 128 | `internal/sqltype/arithmetic_resolution.go:arithmeticCandidates/ResolveArithmetic` and `arithmetic.go:BindArithmetic`, actual engine routing. Exact mixed integer/float arithmetic, NUMERIC, released temporal/interval and money signatures. Remainder only same-width integer/NUMERIC. Power FLOAT8/NUMERIC. Unary + and - numeric widths; unary - INTERVAL. MONEY unary negative remains excluded despite aliases. Current candidates also include INET+INT8, INT8+INET, INET-INT8 and INET-INET→INT8, executed by `network_operators.go:Arithmetic.networkBinary`. |
| Integer, BIT and INET bitwise | 27 | `internal/engine/bitwise.go:finishBitwise`, `internal/sqltype/bitwise.go`, `bitstring_operators.go`, `network_operators.go:BindNetworkBitwise`. Integer same-width &, pipe, #, unary ~ and shifts (right INT4); mixed integers promote to an existing width. BIT providers selected even for VARBIT inputs: no invented VARBIT rows. INET admits unary ~ and binary &/pipe; CIDR inputs coerce to INET rather than creating CIDR overloads. |
| LIKE/ILIKE/POSIX | 26 | `internal/engine/expression_like.go:bindLike`; `internal/engine/functions/expression_posix.go:CoercePatternArguments/PatternAllowed`. NAME/BPCHAR/TEXT left, TEXT right, all four LIKE/ILIKE spellings and all four regex spellings; BYTEA only LIKE/not-LIKE. Unicode collation ILIKE boundary unchanged. |
| String/binary concat and prefix | 6 | `internal/engine/string_expression.go:bindStringConcat/bindStringPrefix`; `functions/string_expression.go:SelectStringConcat`. TEXT/TEXT, TEXT/anynonarray, anynonarray/TEXT, BYTEA/BYTEA, VARBIT/VARBIT concat; TEXT/TEXT ^@. Array/JSONB priority remains distinct. |
| Array membership and concat | 6 | `internal/engine/array_operator.go:finishArrayMembership/selectArrayConcat`. Three anyarray membership records; anycompatiblearray concat, append and prepend. No per-element synthetic identities. JSON[] element failure does not remove generic anyarray admission. |
| ACL item inclusion | 1 | `internal/engine/array_operator.go:finishArrayMembership` selects fixed ACLITEM[]1034/ACLITEM1033 before generic array membership; `internal/engine/functions/acl_items.go:aclContains` matches authority, grantee/grantor and rights/options inclusion. Released968 is concrete, not a synthetic anyarray record. |
| JSON/JSONB | 21 | `json_operator_bind.go` in internal/engine/functions (formerly five engine files). Twelve extraction records (JSON/JSONB, TEXT/INT4 keys or TEXT[] path, document/TEXT result); JSONB existence three, containment two, concat one, deletion TEXT/INT4 two, path-delete one. SMALLINT keys coerce to INT4. **Released3398 JSONB-TEXT[] deletion excluded**: current SelectJSONBDelete selects TEXT or INT4 only. |
| Range/multirange noncomparison | 42 | `internal/engine/range_operator.go:bindRangeShared` and `internal/sqltype/range_operator.go:RangeOperatorResult/BindRangeOperator`. Released same-family set operations; declared range/multirange cross-shape relationships, adjacency and ordering; generic element contains/contained-by. Intersect the physical helper with released records: never manufacture every shape the helper could admit. |
| Excluded identities | 254 | Remaining release signatures have no exact executable routed provider. Geometry, unsupported XID symbols, full-text, root/absolute-value, network containment, pattern-order symbols, record scalar comparison, JSONPath operators and JSONB-TEXT[] deletion remain aliases only. |

Total 288+128+27+26+6+6+1+21+42 = **545**. Retain all **799** aliases.

## Historical exact partition audit (545-row checkpoint)

These disjoint OID lists make the full source-review partition inspectable,
including negative space. They are documentation, not a second resolver. Stage C
provider-agreement units must consume actual existing selectors; they must not
simply duplicate these lists or implement a generic parallel resolver.

### comparison (288)

```text
15,36,37,58,59,76,80,82,85,91,92,93,94,95,96,97
98,254,255,256,257,258,259,260,261,262,263,264,265,352,353,385,387,402
410,411,412,413,414,415,416,417,418,419,420,430,518,519,520,521
522,523,524,525,531,532,533,534,535,536,537,538,539,540,541,542
543,607,608,609,610,611,612,620,621,622,623,624,625,630,631,632
633,634,643,660,661,662,663,664,665,666,667,670,671,672,673,674
675,900,901,902,903,904,905,974,1054,1057,1058,1059,1060,1061,1070,1071
1072,1073,1074,1075,1093,1094,1095,1096,1097,1098,1108,1109,1110,1111,1112,1113
1120,1121,1122,1123,1124,1125,1130,1131,1132,1133,1134,1135,1201,1202,1203,1204
1205,1206,1320,1321,1322,1323,1324,1325,1330,1331,1332,1333,1334,1335,1550,1551
1552,1553,1554,1555,1694,1695,1752,1753,1754,1755,1756,1757,1784,1785,1786,1787
1788,1789,1804,1805,1806,1807,1808,1809,1862,1863,1864,1865,1866,1867,1868,1869
1870,1871,1872,1873,1955,1956,1957,1958,1959,1960,2060,2061,2062,2063,2064,2065
2345,2346,2347,2348,2349,2350,2358,2359,2360,2361,2362,2363,2371,2372,2373,2374
2375,2376,2384,2385,2386,2387,2388,2389,2534,2535,2536,2537,2538,2539,2540,2541
2542,2543,2544,2545,2799,2800,2801,2802,2860,2861,2862,2863,2864,2865,2972,2973,2974,2975,2976,2977
3240,3241,3242,3243,3244,3245,3315,3316,3516,3517,3518,3519,3520,3521,3882,3883
3884,3885,3886,3887,5068,5072,5073,5074,5075,5076
```

### arithmetic (128)

```text
439,484,514,526,527,528,529,530,544,545,546,547,550,551,552,553
554,555,556,557,558,559,584,585,586,587,588,589,591,592,593,594
684,685,686,687,688,689,690,691,692,693,694,695,818,819,820,821
822,823,824,825,843,844,845,906,907,908,909,912,913,914,915,916
917,918,965,1038,1076,1077,1099,1100,1101,1116,1117,1118,1119,1126,1127,1128
1129,1327,1328,1329,1336,1337,1338,1360,1361,1363,1366,1399,1583,1584,1585,1751
1758,1759,1760,1761,1762,1800,1801,1802,1803,1849,1916,1917,1918,1919,1920,1921
2066,2067,2068,2551,2552,2553,2554,2555,2637,2638,2639,2640,3346,3347,3349,3825
```

### bitwise (27)

```text
1791,1792,1793,1794,1795,1796,1874,1875,1876,1877,1878,1879,1880,1881,1882,1883
1884,1885,1886,1887,1888,1889,1890,1891,2634,2635,2636
```

### pattern (26)

```text
639,640,641,642,1055,1056,1207,1208,1209,1210,1211,1212,1226,1227,1228,1229
1234,1235,1625,1626,1627,1628,1629,1630,2016,2017
```

### concat-prefix (6)

```text
654,1797,2018,2779,2780,3877
```

### array (6)

```text
349,374,375,2750,2751,2752
```

### json (21)

```text
3206,3211,3212,3213,3246,3247,3248,3249,3250,3284,3285,3286,3287,3477,3481,3962
3963,3964,3965,3966,3967
```

### range (42)

```text
2866,2867,2868,2869,2870,2871,2872,2873,2874,2875,2876,2877,3585,3888,3889,3890
3891,3892,3893,3894,3895,3896,3897,3898,3899,3900,4035,4142,4179,4180,4198,4392
4393,4394,4395,4396,4397,4398,4399,4400,4539,4540
```

### ACL inclusion (1)

```text
968
```

### excluded (254)

```text
433,473,485,486,487,488,489,490,491,492,493,494,495,496
497,498,499,500,501,502,503,504,505,506,507,508,509,510,511,512
513,517,590,595,596,597,606,613,614,615,616,617,618,644,645,646
647,648,649,682,706,707,708,709,712,713,731,732,733,734,735,736
737,738,739,755,756,757,758,759,760,761,762,763,773,784,792,793
794,795,796,797,798,799,800,801,802,803,804,805,806,807,808,809
931,932,933,934,966,967,969,971,1220,1221,1222,1223,1224,1225,1383
1420,1500,1501,1502,1503,1504,1505,1506,1507,1508,1509,1510,1511,1512,1513,1514
1515,1516,1517,1518,1519,1520,1521,1522,1523,1525,1526,1527,1528,1529,1535,1536
1537,1538,1539,1546,1547,1548,1549,1557,1558,1559,1567,1577,1578,1586,1587,1588
1589,1590,1591,1611,1612,1613,1614,1615,1616,1617,1763,2314,2315,2317,2318,2326
2327,2329,2330,2570,2571,2572,2573,2574,2575,2576,2577,2589,2590
2988,2989,2990,2991,2992,2993,3147,3148
3149,3188,3189,3190,3191,3192,3193,3222,3223,3224,3225,3226,3227,3228,3276,3289
3291,3362,3363,3364,3365,3366,3367,3368,3369,3370,3398,3552,3627,3628,3629,3630
3631,3632,3633,3636,3637,3660,3661,3674,3675,3676,3677,3678,3679,3680,3681,3682
3693,3694,3762,3763,4012,4013,4161,4162,5005,5025,5026,5027
```

## Root-owned oracle checkpoint

The initial public scenarios compiled without future production symbols and
used the documented temporary PG-only discovery filter. Root removed that filter
after completing the corrected oracle run, before genuine native red. No
discovery filter remains in the final shared definitions.

Root's PG-only discovery passed Shape/Metadata/CatalogLinks/Inventory and
Diagnostics/Permissions/HeldExecution. The resolution group exposed two test
expectation issues: missing JSONB3802 mapping and pq's unmapped concrete
multirange result OID. Root observed operator4392's actual public
`DatabaseTypeName` as the empty string (`artifact://27`), not INT4MULTIRANGE.
The shared witness now asserts that pq-specific empty name only for the
multirange representative; native must retain INT4MULTIRANGE. Declared catalog
result remains anymultirange4537 on both backends. Root subsequently observed
the corrected resolution group pass (package1.507s), removed the filter and
observed genuine native Shape failure: `relation "pg_catalog.pg_operator" does
not exist` (`artifact://30`). Production was authorized only after that failure.

Initial selected proof:

```sh
make check RUN='^TestReferencePGOperator(Shape|Metadata|CatalogLinks|Inventory)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(ResolverAgreement|PolymorphicIdentity|References|Boundaries|Diagnostics|Permissions|HeldExecution)$' REFERENCE=1
```

External deadline: 900 seconds. Root observed CHAR public spelling, pq unknown
ScanType for OID/NAME/CHAR/REGPROC, complete numeric reference fields and the
specified English diagnostic/permission/held-query contracts. Native inventory
reads remain unfiltered and require all545 rows exactly once; only PG filters
to the independent admitted OIDs. Stage C adds real native Dump/Reset/Restore,
failed/canceled staged replacement, retained symbolic Rows and saved metadata,
and actual native user-dump replay on PG. Those new phases need fresh root proof.

The feature smoke selects `^TestReferencePGOperatorFeatureSmoke$` in both modes
and reuses permanent scenario entrypoints for actual metadata, signatures,
references, boundaries, permissions, held queries, diagnostics and native
control/replay assertions. At the original checkpoint, private
`TestPGOperatorProviderAgreement` enumerated all799 released identities through
actual binder routing and inspected selected/coerced operand and result usages.
That historical inventory-partition audit has been removed, not repinned.
Current `TestReferenceLsegOperatorCatalog` instead checks six independently
specified LSEG catalog rows and real typed-column operator results together,
including direct, prepared and held consumers. It detects a provider that
executes while its catalog row is unpublished without asserting internal wiring.
Capture contains no provider discovery/binding and preserves existing alias
identity ownership. The expected TSV is not parsed at runtime.
No build, test, formatter or smoke was run by the implementer.

## Selected-proof route corrections

Root's post-rebase private ownership/provider-agreement units passed. Root also
reported shape/metadata/links/inventory passing in both modes, and real-mode
diagnostics/permissions/held/lifecycle, builtin index history and the existing
799-alias regression group passing. These observations do not substitute for
fresh complete proof after the test corrections below.

The resolution/reference group exposed test-route errors (`artifact://44`),
not missing operator providers. Existing `StoredScalarCastType` excludes range
and BIT explicit casts. Range/multirange union now executes real typed table
columns populated through existing destination-bound inputs; direct, prepared
and actual held routes compare nonnull raw result values, public type names and
native string/pq byte carriers without inventing casts. BIT/VARBIT coercion is
likewise exercised through typed columns with a real BIT result, not forbidden
BIT/VARBIT casts or BIT-to-TEXT conversion.

Native custom `CREATE TYPE operator_range AS RANGE(subtype=integer)` remains
the existing parser boundary:0A000, `CREATE TYPE syntax is unsupported`,
Position31 at RANGE; direct fails execute and new Prepare fails prepare.
PostgreSQL performs real custom-range creation/execution in its explicit phase.
Both backends still create and execute a genuine enum and exercise builtin
range containment through typed columns, with the complete admitted catalog
unchanged. No custom-range DDL or scalar cast provider was added.

The same root failure output independently showed both backends render
zero REGOPERATOR as **`0`**, not `-`. Exhaustive commutator/negator expectations
now use that observed spelling. Zero REGPROC remains **`-`** and its existing
shape/reference assertions are unchanged. All nonzero symbolic target
expectations still come from the separate released799-alias inventory.

## Review 1 coverage remediation

Review 1 identified two medium evidence gaps, not incorrect production records:
homogeneous polymorphic substitution did not prove compatible promotion or
invalid authority relationships, and several public operator families lacked
actual reused-held execution with uncast result metadata.

The supplemental private relationship proof runs independently as
`TestPGOperatorPolymorphicRelationships`. Real typed-column binder calls cover
SMALLINT[]/INTEGER[] concatenation in both orders, promotion of the array and
element in append/prepend, selected/coerced child usages and INTEGER[] results.
Fixed released declarations remain5078/5077 rather than concrete signatures.
TEXT/INTEGER nonarray concatenation is distinguished from TEXT/TEXT[] array
prepend and the reversed append boundary by actual bound-node family and
selected operand/result types. Invalid simple-array element families, distinct
genuine enums, incompatible range-element and unrelated range-family authorities
must be rejected. The full799-candidate partition was historical checkpoint
coverage, not a currently retained test. Public operator inventories and focused
catalog/evaluation consumers provide the current publication contract.

The new shared `TestReferencePGOperatorPolymorphicRelationships` independently
checks those nonnull mixed/common-type and dispatch results, catalog pseudo IDs
and unchanged full inventory. Complete negative diagnostics/stages are proposed
before oracle observation; its temporary PG-only discovery filter must be
removed after root observes them, before native/final proof. This is not a
passing claim.

The common execution-route helper now runs each representative's **uncast**
result directly, through a new Prepare and twice through one actual held
statement. Every route checks concrete names/types, raw carriers and supported
ScanType expectations. It covers arithmetic/temporal/money/prefix, LIKE/regex,
text/BYTEA concatenation, integer/BIT bitwise, JSON/JSONB extraction/operations,
arrays, enum comparisons and range/multirange operations. Text formatting is
not substituted for result-type evidence. Feature smoke invokes the new
relationship definition as well as the existing scenarios.

Root's first bounded oracle argv for the new diagnostics:

```sh
make check RUN='^TestReferencePGOperatorPolymorphicRelationships$' REFERENCE=1
```

After observation/filter removal, the completed ResolverAgreement,
PolymorphicIdentity and PolymorphicRelationships definitions require fresh
selected proof in both modes, the provider unit, feature smoke and root's
whole-project gates. No production provider, metadata record, cast, DDL or alias
behavior changed for this remediation.

## First rebased provider synchronization (historical544)

After the integration rebase, root's full actual-binder proof exposed13 working
exact signatures absent from the historical531 expected bag (`artifact://89`).
Current authority has six TID comparisons (387,402,2799–2802), three INET
bitwise records (2634–2636), and four INET arithmetic records (2637–2640).
The earlier source reads lacked those providers; this change synchronizes the
catalog to the existing rebased implementation, not an operator feature addition.

Grounding: `internal/sqltype/type.go` owns TID's physical descriptor/input/output
and lexicographic block/offset comparison; existing comparison routing admits
the six ordinary symbols. `arithmetic_resolution.go` declares the four exact
INET signatures, `arithmetic.go` dispatches to `Arithmetic.networkBinary`, and
`network_operators.go` implements address arithmetic. Engine `finishBitwise`
selects INET operands/results through `BindNetworkBitwise`; CIDR is coercion,
not another exact catalog signature.

The additional15-field expected rows were independently resolved again from
REL_18_0 pg_operator.dat/pg_type.dat/pg_proc.dat, applying header defaults,
ordered operator references and exact procedure argument signatures. They are
not copied from native catalog output. TID procedure IDs are1292/1265/2791/2790/
2793/2792; INET procedure IDs are2627–2633. Nonzero references remain valid and
the expanded bag stays closed over commutator/negator targets.

At that rebase the disjoint partition was comparison288, arithmetic128,
bitwise27, patterns26, concat/prefix6, array6, JSON21, range42:544/255.
The fixed production metadata and independent expected bag add exactly those13
rows, preserving all799 aliases. Public resolver/smoke now execute all13 added
signatures using real typed TID/INET/INT8 columns, with uncast metadata and
direct/newly prepared/reused-held values. No provider code was changed.

Root also observed pq BIT ScanType as string (`artifact://93`). The uncast
metadata expectation now asserts string for BIT/VARBIT on both backends while
retaining independent raw-cell checks. The new polymorphic relationship oracle
passed and root removed its discovery filter before native proof. Those amended
544-row definitions required fresh selected native and pinned PostgreSQL proof;
the545-row synchronization below supersedes that inventory, not its history.

## Post-review2 ACL inclusion synchronization (current545)

Root approved Review2 on the544-row candidate, then performed the mandatory
rebase onto63296f382f5e765879c0c4ce944174b1241a75f9. The exhaustive actual-binder
proof exposed one newly available exact signature:968
`@>(aclitem[],aclitem)`→BOOL, selected as `functions.ACLValue` (kind `ACLContains`). This is catalog
synchronization to existing integrated behavior, not a new operator/provider.

Current `array_operator.go:finishArrayMembership` routes known ACLITEM[]/ACLITEM
to its dedicated ACL branch before ordinary array/array membership.
`functions.BindACLContains` (`acl_value_bind.go`) declares exactly1034/1033 and BOOL; its evaluator
requires matching ACL authority/grantee/grantor and rights/options inclusion
within one stored entry. A known array RHS still selects generic membership.

The independent REL_18_0 source row resolves all15 fields as:

```text
968 @> 11 10 b f f 1034 1033 16 0 0 1037 0 0
```

`pg_operator.dat` names `aclcontains`; exact released `pg_proc.dat` arguments
`_aclitem aclitem` resolve procedure1037. Header defaults supply kind/flags/zero
references. Production metadata and the independent expected TSV add only968,
leaving all799 identity aliases and every existing provider unchanged.

The current partition adds a separate concrete ACL-inclusion1 bucket to the
previous544 admitted records:545 admitted/254 excluded. The resolver and shared
feature smoke populate ACLITEM[]/ACLITEM columns through existing
`makeaclitem(0,0,...)` calls. SELECT+UPDATE with grant options contains a SELECT
entry with its grant option (true), but not a DELETE entry (false). Both
nonnull uncast BOOL witnesses run direct, newly prepared and twice through an
actual held statement, with public result metadata checked on every route.

The545-row bag/provider/reference/control/smoke definitions require fresh root
proof and Review3. No checks or formatters were run by the implementer.

## Partitioned exclusion provider metadata

The logical GiST INET provider additionally owns operator3552,
`&&(inet,inet)`, including ordinary local exclusion declarations. Its released
metadata is BOOL, binary, nonmerge/nonhash, self-commutator3552, no negator,
procedure3551 (`network_overlap`), restriction3560 (`networksel`) and
join3561 (`networkjoinsel`). These fields come from the released
[`pg_operator.dat`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_operator.dat)
and [`pg_proc.dat`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat).
Existing lookup-only procedure identities suffice: this change adds neither
callable network routines nor general scalar INET overlap admission.

This adds one bounded exclusion-provider record to the preceding545 scalar
records; historical partition audits below describe their original snapshots.
`TestPGOperatorExclusionPredicate` exercises actual exclusion overlap evaluation
for3552 without maintaining another provider inventory. The shared partition
exclusion Metadata scenario exercises persistent ordinary INET-overlap metadata
and conflicts. New definitions require fresh selected native and pinned proof;
no verification is claimed by this implementation note.

## User-operator startup and grammar validation distinction

Released operator metadata and callable routine declarations remain separate
authorities. Supported seed `?(jsonb,text)` is operator3247 with result BOOL16
and routine identity4047. Its real JSONB expression provider does not require
a callable `catalog.Procedure` for4047. Seed validation checks the immutable
released result/function facts and existing operand/result types; normal
catalog ACL validation still checks its owner. Custom defined operators must
instead have a genuine function descriptor with the exact ordered signature
and result. No routine provider or pg_proc row is fabricated from metadata.
Unprojected builtin identities retain zero result/function without becoming
custom forward shells. Existing protected builtin ALTER/DROP boundaries are
unchanged; this part's full mutation support is for user-defined operators.

When custom overloads force catalog selection, a selected seed uses its real
existing expression binder with already-bound/coerced operands. This preserves
lookup-only routine identities without turning them into invocation providers.
`TestReferenceUserOperatorLookupOnlySeedOverload` independently expects custom
integer `?` to return5 while released JSONB `?` still returns true/false/NULL.
`TestUserOperatorCatalogLookupOnlySeedValidation` rejects altered seed return/
function facts and a custom operator borrowing lookup-only routine4047.

Operator option values share the `type_name '.'` prefix for types, functions
and symbolic references. The remaining schema-name keyword alternatives are
disjoint from `type_name`; this avoids a competing `name` reduction on every
identifier before '.'. Existing lookahead separates the `OPERATOR(...)`
introducer from the DDL OPERATOR keyword using `QUALIFIED_OPERATOR`, rather
than adding precedence/conflict suppression or an alternate SQL parser.
Expression prefix/infix/quantified uses retain the generic OPERATOR precedence
tier. Parser units cover option types/functions/symbols, quoted and keyword
schemas, source positions, DDL/identifier contexts and qualified precedence.

The root-reported startup panic and167/129 grammar conflict totals are failure
evidence, not rerun here. Root must regenerate/measure the resulting grammar and
run selected native and pinned reference proof; no passing result is claimed.

## User-operator exact lifecycle lookup and raw reference witnesses

An operator name key includes its catalog class as well as namespace, symbolic
name and ordered operand signature. Temporary lookup descriptors must therefore
carry OperatorClassID even before allocating an object OID. CREATE duplicate/
shell completion, ALTER/DROP exact lookup and reciprocal/self-link selection
use the same descriptor key as publication; a class-zero probe cannot identify
a published operator. `TestUserOperatorCatalogExactUtilityLifecycle` exercises
these direct staged paths without public SQL or fabricated routine providers.

Authorization views retain paired catalog and role provenance. Replacing the
catalog for staged peer ownership checks or a post-publication unit fixture
uses `Recapture`, not assignment to only the catalog field.

The shared catalog-row witness uses `OidReferenceRows` to validate native int64
and PostgreSQL decimal-byte transport independently before comparing numeric
OID identities. Public namespace OIDs are captured from each installation, not
assumed to be2200. Authenticated raw login pools have no recording interceptor;
their successful rows are asserted directly, while full diagnostic/stage checks
remain through the shared fixture. No native outcome supplies expected logical
values or PostgreSQL diagnostics.

Ordinary math DEFAULT admission is unchanged: grouping can admit syntax while
the existing native math consumer still rejects the call. The operator DEFAULT
witness instead invokes its actual selected int4pl implementation on literals.
This does not enable standalone math defaults or implement a user routine runtime.

Operator discovery and retirement are separate phases of the shared dependency
closure. OperatorClassID must enter the final whole-object removal phase as
well as discovery: collecting a root alone does not remove its descriptor or
clear surviving reciprocal links. The pure drop-closure unit covers duplicate
multi-root selection, shell retirement, exact-name removal, peer-link clearing
and immutable predecessor preservation through actual publication.

The selected-operator wrapper deliberately has no routine carrier of its own,
so admission visits the real provider once. Stored recipe collection must
likewise reach that provider via `recordStoredRoutine`, including its body
manifest, before rendering the wrapper's original symbolic operands. The
stored-manifest unit checks both original and remapped/retagged calls and
ordinary stored routine admission; a bare operator reference is not execution
authority. Existing DEFAULT/CHECK collection and restore remapping remain the
owners of the resulting routine references.

The feature smoke keeps its schema-USAGE grant and unauthorized ALTER in a
separate permission schema, dropped before native Dump. Replay therefore
exercises genuinely portable state without adding namespace ACL serialization,
changing ownership to evade admission, or weakening the permission assertion.
These are implementation explanations, not passing verification claims; root
owns fresh selected native/pinned proof, feature smoke and whole-project checks.

## Fresh comparison consumers and structural authority (R13)

Released [parse_oper.c, make_scalar_array_op](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/parser/parse_oper.c)
selects the actual operator before checking its result and retaining its function.
The native builtin-only consumers share ordinary catalog candidate collection,
namespace/signature masking and `sqltype.ResolveOperator` on already-analyzed
descriptors. An executable custom winner is not silently replaced by a builtin:
analysis returns `0A000: custom operators are unsupported in this comparison context`.
Ordinary scalar custom calls and BETWEEN's ordinary lowered operators continue
to use their real providers; this is not a custom quantified-runtime extension.

Scalar-array order is RHS-array validation, actual selection errors, selected
custom shell42883, selected non-Boolean result42809, then Boolean custom0A000.
Explicit builtin qualification and exact builtin winners over nonmatching custom
overloads preserve existing provider capabilities. Seed metadata is not a new
executable provider. Original lexical positions survive; synthesized USING/NATURAL
equality has no Position and resolves original key types before coercion.

[parse_expr.c, transformAExprIn](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/parser/parse_expr.c)
first partitions non-Var items and attempts common-type scalar-array batching.
When the verified common type is not RECORD and has an admitted array type,
named-composite constructors participate in that structural branch. Two equal
typed constructors yield true for IN and false for NOT IN even under int4 field
operator shadowing; matching NULL members have the same structural results.
Only residual transformed RowExpr pairs compare fresh fields: one-item cast ROW
lists remain fieldwise, and anonymous RECORD NULL fields remain unknown.
Raw rows retain unknown field occurrences until field selection; admitted
non-domain cast constructors reuse already-coerced fields without rebinding.
Frozen residual syntax normalizes to ROW source associations and captures member
usages through the shared constructor capture helper. Synthetic member casts,
expansions and NULL semantics survive view presentation and logical replay;
numeric5.9 cast into an integer member remains6 rather than an uncoerced5.9.
Ordinary typed composite equality and actual composite-column IN remain
structural, as do array/record leaves, fixed families, grouping, extrema, CYCLE
and stored provider reconstruction. These do not perform fresh lexical scalar
lookup.

Root's five ComparisonConsumer reference definitions own independent PostgreSQL
outcomes and direct/prepared native boundaries. Tests-30 records the original
oracle-first proof; tests-34 adds PostgreSQL-first batching/stored-constructor
proof and native red for R14/R15. Fresh both-mode verification, actual emitted
SQL replay and complete review remain root-owned; this note claims no passing
implementation checks.

## Immutable operator membership lookup

Catalog roots share a derived operator-only `(namespace, name)` address index.
The descriptor/name maps remain authoritative: scalar selection and alias-group
capture dereference addresses in the **same captured root**. Buckets contain no
descriptor pointers, principals, signatures, ACL decisions, winners or providers.
All fixed seed address buckets are shared read-only across engines; descriptors
and their principal provenance remain engine-owned. Bootstrap attaches the index
before builtin-domain binding.

Stage reads, committed publication and private transaction overlays apply final
membership deltas once. Clones share the immutable index; a membership-changing
batch copies its map once and only touched bucket arrays. Owner, revision,
signature, result, options and shell completion at an unchanged address/name/
namespace retain the index but expose the current descriptor. Namespace or
address moves remove old membership and insert new membership. Unrelated DDL
does not copy the index.

Persistent replacement removes TEMP membership with its descriptors. Collision
reconstruction rebuilds membership before imported expressions, exclusions or
generated definitions can bind, and the final retained TEMP overlay applies its
own membership deltas before later validation. Principal-only remapping shares
the address index while replacing descriptors. No lazy repair, full-scan lookup
fallback, permission cache, additional epoch or alternative resolver is used.

The lookup still applies current authorization, path order, unary/binary
filtering and identical ordered-signature masking before the existing type
resolver and provider boundary. `TestUserOperatorIndex` units compare every
bucket against an independent descriptor projection and retain predecessor/
sibling roots, including a shared bucket with spare capacity. This bounded
implementation removes heterogeneous catalog scans from operator candidate and
alias-group collection; it makes no measured speedup or startup-target claim.

### Ephemeral seed-comparison availability

Binary spellings recognized by the existing `comparisonOperator` use a
presence probe after the same fresh binding authorization and namespace lookup.
It reads address buckets and current descriptors from that captured catalog,
ignoring unary descriptors. If no matching-arity custom descriptor occurs
anywhere in the resolved path, the candidate set carries only its catalog and
availability: no descriptor/signature slice or seen map is materialized.
Availability means an arity-matching descriptor exists, not that its inputs are
viable, its provider is executable, or its use is authorized beyond lookup.

Any matching-arity custom descriptor, including one hidden by an earlier
identical signature, conservatively falls back to the existing full ordered
materializer on that same path/root. Only this materializer determines surviving
custom candidates after masking. Unary and every noncomparison spelling retain
full materialization. Ordinary comparison and scalar-array consumers test
availability rather than materialized slice length before their unchanged static
type resolver/error handling. No path/winner/permission cache, extra catalog
capture, resolver, provider or per-row lookup is introduced.

The existing StructuralControls public scenario independently guards seed-only
ordinary/ANY equality, qualified operator absence in an existing namespace,
genuine unary int4um execution and wrong-arity exclusion, and a matching custom
masked by pg_catalog-first lookup. The seeded qualified `+` ANY witness retains
the actual non-Boolean-result42809 diagnostic; it cannot take the comparison-only
probe. Root observed these new guards passing on pinned PostgreSQL18.6 and native
before authorizing the performance remedy. Existing result, shell, RHS-first,
custom-boundary and full structural matrices remain unchanged. Existing index
units cover current metadata, staged/private/reconstructed roots and immutable
predecessors; no allocation-count, wiring or timing unit replaces semantic proof.

The companion root-owned inheritance traversal remedy filters descriptor keys by
RelationClassID before loading values in `catalog.InheritanceChildren`. Actual parent
edges in the supplied `catalog.Read`, safe relation assertions and ObjectID sorting
remain authoritative; `hasSubclasses` is not absence proof. It introduces no
inheritance cache or new SQL capability. Existing diamond ordering and late
staged/current-root admission witnesses remain its semantic guards.

The owning operator README provides exact selected both-mode and index normal/
race selectors. Root owns fresh after-edit proof, unchanged complete predicate
and array race matrices, fixed public CPU/allocation profiles, actual smoke,
quickcheck and cumulative review. These bounded allocation/traversal changes
claim neither restored deadlines nor a startup-target result before observation.


## R17 whole-signature generic viability

Released REL_18_6 `can_coerce_type` invokes `check_generic_type_consistency`
across the complete signature, both during initial operator candidate matching
and after final unknown-assume-known substitution. Independent per-position shape
checks cannot distinguish array concatenate from append/prepend when both actual
inputs are arrays. `operatorImplicitInputs` now owns those two-slot relationships
in the existing sqltype resolver; candidate indices, exact/domain shortcuts,
preferred/category scoring and actual provider dispatch remain unchanged.

Simple ANYELEMENT/NONARRAY/ENUM relationships use exact logical element identity.
Outer array/range/multirange domains flatten for container matching, but an
array-of-domain retains its logical element. Existing range/subtype and
multirange/member links constrain the same simple state. Compatible scalar/array
formals contribute scalar/element leaves instead, preserving identical domains,
otherwise selecting a common base candidate and verifying every known input's
implicit coercibility. Initial unknowns supply no constraints. RECORD remains
an independent shape check, not a shared identity variable.

`sqltype.CommonTypeCategory`, `CommonTypePreferred` and `CommonTypeCandidate`
are the single owner of the existing descriptor common-choice policy. Engine
common expression binding retains its Usage/domain/typmod/collation handling,
diagnostic positions and downstream conversion phase; the pair fold's Boolean
means category compatibility, not guaranteed coercibility. In particular ordinary
TIME/TIMESTAMP VALUES still fail late with 42846. Generic viability alone verifies
all compatible leaves. Operator equality scoring and series-specific preferences
retain their different existing policies.

State is fixed to the two operator inputs, with no new candidate collection,
catalog lookup, synthetic expression or provider. Identity-first leaf checks
preserve identical lookup-only facts without invalid Usage probes; missing array
elements and temporal-multirange links are guarded. Canonical ANYARRAY remains
outside concrete-array shape admission, with unresolved comparison diagnostics
still owned by the existing comparison binder. No absent compatible-range family,
lookup codec, general function runtime or result instantiation is introduced.

The owning operator README records the public regression and exact selected
normal/race/reference commands. Root tests-46 supplies PostgreSQL-first and actual
native-before evidence, not remedy approval. Pure resolver/common-choice units
supplement the unchanged R16/R17 public bodies. The internal TIME/TIMESTAMP SQL
loop was removed in favor of already complete shared
`TestReferenceInsertSelectValuesDiagnostics`; its pure common-type matrix remains.
Fresh dual-mode proof, full quickcheck, smoke and complete cumulative review
remain root-owned.


## LSEG scalar publication consumer contract

The six genuine ordered-endpoint/length comparison providers require their
released pg_operator rows, not merely REGOPERATOR lookup identities:
1535 (`=`),1586 (`<>`),1587 (`<`),1588 (`<=`),1589 (`>`),1590 (`>=`).
All are binary601×601→BOOL, nonmerge and nonhash. Equality/inequality use
procedures999/1482 and estimators101/105 and102/106. The four length comparisons
use procedures1483–1486 and **zero** restriction/join estimators; no ordinary
scalar inequality estimator is invented. Commutators/negators remain the exact
released links in the independently maintained expected TSV.
Source: [REL_18_6 pg_operator.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_operator.dat)
and [pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat).

`TestReferenceLsegOperatorCatalog` checks every exposed numeric field, symbolic
procedure/estimator rendering, and direct/prepared/held typed-column evaluations
joined to the corresponding real catalog row. Existing unfiltered native
`TestReferencePGOperatorInventory` and its lifecycle/permission/reference
consumers use the extended independent expected TSV. These are behavior
contracts, not counts of implementation declarations or a parallel resolver.
Root observed the new regression pass pinned PostgreSQL oracle discovery and
then fail natively with six missing catalog rows. Its temporary filter is
removed. Earlier discovery/baseline observations do not establish post-fix proof;
fresh native and real-mode selected checks remain required.

