# PostgreSQL 18 operator metadata admission evidence

## Provenance and status

The companion `pg-operator-pg18-supported.tsv` is an independently constructed
expected inventory, **546 admitted records out of 799 released identities**;
253 remain identity-only. Of the admitted records, 3552 is executable through
the bounded exclusion provider rather than general scalar SQL. The inventory
has a header and exactly15 raw catalog columns,
ordered numerically by OID. Root confirmed the initial531-row metadata against
pinned PostgreSQL18.0 before production. Subsequent rebases synchronize existing
TID/INET and ACL-inclusion providers as documented below. The latest complete
546-row definitions require fresh root-owned proof; source facts are not a pass.
No native query output, production metadata, or local PostgreSQL 20devel catalog
was used to construct metadata values. Existing eight-column REGOPERATOR evidence
and all 799 alias identities are unchanged.

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

The current545 admitted bag is source-derived closed over every nonzero
commutator/negator target: no genuine absent-target builtin witness exists here.
The reference scenario exhaustively resolves symbolic targets against the
independent799-alias evidence rather than fabricating an absent target or
removing a working row. This does not impose closure on future operator parts.
The genuine routine distinction is `int4pl`177: alias lookup/implementation
reference works while native pg_proc has no SQL row for that identity.

## All-family partition

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
| Array membership and concat | 6 | `internal/engine/array_search.go:finishArrayMembership` plus `array_transform.go:selectArrayConcat`. Three anyarray membership records; anycompatiblearray concat, append and prepend. No per-element synthetic identities. JSON[] element failure does not remove generic anyarray admission. |
| ACL item inclusion | 1 | `internal/engine/array_search.go:finishArrayMembership` selects fixed ACLITEM[]1034/ACLITEM1033 before generic array membership; `acl_values.go:aclValueContains` matches authority, grantee/grantor and rights/options inclusion. Released968 is concrete, not a synthetic anyarray record. |
| JSON/JSONB | 21 | `json_extract_expression.go`, `jsonb_exists.go`, `jsonb_containment.go`, `jsonb_concat_delete.go`, `jsonb_path_delete.go` in internal/engine. Twelve extraction records (JSON/JSONB, TEXT/INT4 keys or TEXT[] path, document/TEXT result); JSONB existence three, containment two, concat one, deletion TEXT/INT4 two, path-delete one. SMALLINT keys coerce to INT4. **Released3398 JSONB-TEXT[] deletion excluded**: current selectJSONBDelete selects TEXT or INT4 only. |
| Range/multirange noncomparison | 42 | `internal/engine/range_operator.go:bindRangeShared` and `internal/sqltype/range_operator.go:RangeOperatorResult/BindRangeOperator`. Released same-family set operations; declared range/multirange cross-shape relationships, adjacency and ordering; generic element contains/contained-by. Intersect the physical helper with released records: never manufacture every shape the helper could admit. |
| Excluded identities | 254 | Remaining release signatures have no exact executable routed provider. Geometry, unsupported XID symbols, full-text, root/absolute-value, network containment, pattern-order symbols, record scalar comparison, JSONPath operators and JSONB-TEXT[] deletion remain aliases only. |

Total 288+128+27+26+6+6+1+21+42 = **545**. Retain all **799** aliases.

## Exact partition audit

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
control/replay assertions. Private TestPGOperatorProviderAgreement enumerates
all799 independent released identities through the **actual binder routing**,
using typed column operands and physical polymorphic witnesses; it inspects
selected/coerced child and result usages rather than treating binding success
as exact admission. No helper extraction or parallel resolver was needed.
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

The supplemental private relationship proof now runs from
`TestPGOperatorProviderAgreement`. Real typed-column binder calls cover
SMALLINT[]/INTEGER[] concatenation in both orders, promotion of the array and
element in append/prepend, selected/coerced child usages and INTEGER[] results.
Fixed released declarations remain5078/5077 rather than concrete signatures.
TEXT/INTEGER nonarray concatenation is distinguished from TEXT/TEXT[] array
prepend and the reversed append boundary by actual bound-node family and
selected operand/result types. Invalid simple-array element families, distinct
genuine enums, incompatible range-element and unrelated range-family authorities
must be rejected. The original full799-candidate partition remains intact.

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
`@>(aclitem[],aclitem)`→BOOL, selected as `aclValueExpression`. This is catalog
synchronization to existing integrated behavior, not a new operator/provider.

Current `array_search.go:finishArrayMembership` routes known ACLITEM[]/ACLITEM
to its dedicated ACL branch before ordinary array/array membership.
`acl_values.go:aclContains` declares exactly1034/1033 and BOOL; its evaluator
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
`TestPGOperatorProviderAgreement` exercises the actual exclusion binder and
predicate for3552 separately from the scalar binder. The shared partition
exclusion Metadata scenario exercises persistent ordinary INET-overlap metadata
and conflicts. New definitions require fresh selected native and pinned proof;
no verification is claimed by this implementation note.
