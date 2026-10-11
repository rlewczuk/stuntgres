# PostgreSQL 18 hash equality indexes

## Owned surface and source authority

Ordinary `CREATE INDEX ... USING hash` owns one simple column or immutable
same-table scalar expression key, on existing admitted stored usages. Omitted
USING remains B-tree, including every UNIQUE backing constraint. Hash has no
UNIQUE, INCLUDE, multicolumn, explicit ASC/DESC or NULLS FIRST/LAST capability.
No physical PostgreSQL pages, WAL, buckets/splits, index-only scan, planner,
range/order/NULL search, new stored types or new comparison operators are added.

Released behavior is grounded in PostgreSQL REL_18_0:

- [hash.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/hash/hash.c)
  defines method capabilities;
  [hashutil.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/hash/hashutil.c)
  omits NULL index keys.
- [pg_opclass.dat](https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_opclass.dat)
  and [indexcmds.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/indexcmds.c)
  establish method-specific classes, defaults, binary compatibility and admission
  precedence.
- [reloptions.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/common/reloptions.c)
  establishes complete-list validation, integer bounds, duplicates/namespaces and
  physical-option semantics.
- [amutils.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/amutils.c)
  establishes known-false direction properties when canorder is false, rather
  than NULL. Unknown properties and invalid ordinals remain NULL.

The pinned PG18.0 oracle independently supplied actual hash pg_opclass OIDs and
full source-derived public contracts before native implementation. Discovery was
an intentional visible failing test, not a fabricated successful observation;
its temporary test and temporary native-excluding filters were removed. Genuine
PG-only stored internal CHAR/REGCLASS and full relam/vector metadata controls
remain explicitly separate from native compatibility assertions.

## Concrete identity and providers

`indexDefinition.method` is a small B-tree/hash discriminator, not a plugin
registry. Class lookup/default selection, definition validation, TYPE
reconstruction, semantic equality, root build/merge and deparse use the actual
method. UNIQUE constructors explicitly retain B-tree identity.

`sqltype.IndexClass` distinguishes actual hash identities from B-tree identities.
`IndexHash` binds eagerly, even on empty/all-NULL inputs, and delegates hash and
class equality to the existing grouping/datum providers. A locally normalized
Value retains the original stored datum untouched; the same class-input
normalization is used for both hash and equality. TEXT/VARCHAR/BPCHAR classes,
unsigned oid_ops on INTEGER and canonical REGCLASS, domains and enum generations
follow existing directional binary compatibility. Ordinary implicit widening
never authorizes int8_ops on INTEGER. Signed zero, NaN, NUMERIC scale/zero,
trailing-space BPCHAR and equal interval representations share equality buckets.
Provider binding is immutable and reused on unchanged row-publication semantics.

Owned scalar defaults/classes cover bool, bytea, internal char, name, int2/int4/
int8, text/varchar/bpchar, oid/regclass, float4/float8, numeric, date/time/timestamp/
timestamptz/timetz/interval, uuid and enums, plus existing supported domains.
Released text/varchar/bpchar pattern classes are explicit providers. Provider
binding does not expand CREATE TABLE type admission.

The independently observed OIDs are array10001, bool10048, bpchar10005,
bpchar_pattern10058, bytea10049, char10008, cid10054, date10011, enum10070,
float4/float8 10013/10014, int2/int4/int8 10019/10020/10021, interval10023,
name10029, numeric10030, oid10031, text10037, text_pattern10056, tid10055,
time10039, timestamp10046, timestamptz10040, timetz10042, uuid10066,
varchar10045, varchar_pattern10057 and xid10051. System identities support
released diagnostics only; no tid/xid/cid codec/storage implementation is added.

ARRAY default/explicit array_ops and compatibility are recognized, then native
Execute returns unpositioned ERROR/ERROR0A000 `index operator class is unsupported`.
PG really succeeds. Native ordinary array `=` is not owned, so grouping's array
support alone does not authorize an index. JSON has no default hash class and
returns42704 with the complete PostgreSQL Hint; unowned classes/types remain
exclusions or type incompatibilities, never invented defaults.

## Real immutable buckets and one publication owner

A hash root owns a uint64-to-bucket map, its eagerly bound provider and captured
storage identity/generation/prefix/end root. Each non-NULL entry contains the
original typed key, RowID, VersionID and physical prefix position. Duplicate
keys retain separate identities. Hash collisions are guarded by class equality.

Build scans committed live storage outside the final installation boundary.
Detached build captures retain their live storage origin, including the
first-index path where the prior catalog has no index for the parent. This
provenance is separate from the frozen generation/prefix/end tuple: a root
must prove the published storage identity rather than the detached copy's
identity before the real query route may retain it.
Batch merge derives exact predecessor endings and additions from the existing
rowPublication. It copies the top-level map once when a bucket changes, shares
untouched immutable slices and copies each touched bucket once. NULL-only
membership changes can share the entire immutable map. Replacement positions
match the actual row publication's single-slot/slot-vector/append layout.

`index_publication.go` and `transaction_publication.go` remain the atomic owners.
Prepared rows/end roots/hash roots and catalog descriptors are installed only
under existing incarnation/generation/catalog/root/end/index-set fences,
including the first-index nil-catalog-change fence. Final installation has no
failing provider/expression callbacks. Cancellation or late provider/derived-key
failure installs neither partial rows nor partial roots. Explicit private rows
stay private; savepoint/full rollback discard them and commit publishes the
common prepared roots. No second transaction/index owner is introduced.

The integrated publication owner's validation-only phase still binds and
evaluates hash keys but constructs no installation root map, buckets, copied
heads or slot/end manifests. Hash dispatch shares that phase with existing
B-tree UNIQUE validation; materialization alone derives replacement buckets
and row positions. `TestHashIndexValidationOnlyPublication` covers real
multirow UPDATE predecessors with both retained and rebuilt hash providers.

TYPE, REINDEX and self/foreign replacement rebuild roots from destination rows
and identities. Actual method and class/default reconstruction survive rename,
column compaction, table/schema movement and dependency/drop closure. Source
roots remain immutable during destination replacement/mutation.

## Actual equality candidates and original residual

Visibility-prefix capture retains compatible immutable hash roots while holding
the same engine mutex. Capture proves a current implicit view, no private rows/
deletions/catalog, current storage incarnation, matching generation/prefix/end
root, current frontier and matching actual definition semantics. Explicit/private/
old or otherwise unproved views retain ordinary scans. After capture, probing
never rereads live storage generation or a later root.

Only a top-level single-source whole WHERE comparison may probe: an owned strict
same-type `=` between a bare indexed column and a same-Usage literal or freshly
imported parameter, including the commuted form. No row-side coercion,
class-coarser equality/provider mismatch, compound predicate, join, derived/view/
subquery matching, row mark or expression-key matching is admitted. Execution
constructs eligibility only for the top-level filter; nested execution does not
inherit it. This is a bounded concrete route, not an optimizer.

The real filter consumes a hashCandidateSelection. It evaluates the probe once
for this execution, selects the actual bucket, checks identity/position and class
equality, translates physical positions through the captured sorted visibility
mapping when needed, and evaluates the original SQL comparison for every retained
candidate. A NULL strict probe produces no matches. Position recovery uses only
captured positions/binary search, not a whole-table row map or a scan to find
candidate identities. Range, IS NULL and ordinary ORDER BY do not claim hash
search/order; original sorting and residual/evaluation semantics remain intact.

`TestHashIndexLookupFeatureSmoke` inspects the actual captured root and real filter
candidate state, asserts the two distinct duplicate identities from the five-row
seed and asserts candidate consumption. Separate units force a collision and a
false-positive key to independently exercise class equality and the original
residual, without public adapter mocks or exported telemetry.

## Admission, reloptions and inquiries

Quiet Prepare retains typed CREATE options and their WITH token position, plus
UNIQUE syntax for method diagnostics. Existing native read-only/explicit gates
and target/owner-before-Share admission are unchanged. Integrated standalone
B-tree UNIQUE support retains its bool policy, NULL policy and optional genuine
constraint attachment; hash UNIQUE gets its real method error.
The parser retains one optional WITH clause after INCLUDE/NULLS policy and
before optional TABLESPACE and WHERE. Concrete suffix stages reuse the same
CreateIndex pointer; no second clause can overwrite typed options or positions.
Malformed multiplicity/order fails complete syntax validation before semantic
options, collision notices or native capability exclusions.

CREATE phases are deliberately separate:

1. existing transaction/read-only and ordinary target/owner admission;
2. early method UNIQUE/INCLUDE/multikey capability checks;
3. the complete reloptions list, before key/class/direction diagnostics;
4. key, type, collation, opclass and explicit direction/NULL validation;
5. relation-name collision/IF NOT EXISTS;
6. builtin opclass parameters;
7. remaining PG-valid physical option exclusion at retained WITH Position,
   before OID allocation/build/publication.

Hash recognizes fillfactor only, with PG default75 and bounds10–100. Unknown,
namespace, duplicate, quoted-'=' and malformed values receive full PostgreSQL
logical error fields. Valid native physical requests remain0A000, never no-op
successes. A valid fillfactor cannot hide a bad key/class/direction, collision or
class parameter error. IF NOT EXISTS skips later physical/parameter work only
at its genuine collision/notice phase. ALTER method-aware validation rejects
hash deduplicate_items; valid SET/RESET retains the existing physical exclusion.

Dump and pg_get_indexdef render USING hash without B-tree ASC/NULLS decorations,
with current names, expression trees, collation and genuine explicit class.
Native emitted SQL is actually replayed on PG, not replaced by a handwritten
oracle stream. Detached projectIndexMetadata supplies real hash OIDs, zero
IndOption and correct key/expression/parent identities on the existing carrier;
no new SQL pg_class/pg_index/pg_opclass family is added.

Hash clusterable=false; whole-index scan/bitmap/backward=true. Key asc/desc/
nulls_first/nulls_last/orderable/distance_orderable/returnable/search_array/
search_nulls=false. Existing AM405 can_order/can_unique/can_multi_col/can_include
are false and can_exclude is true. Unknown/out-of-range values retain NULL.

## Independent contract and verification commands

Shared reference definitions are HashIndexLookup, Maintenance, Types,
Diagnostics, Options, Snapshots, Lifecycle, Controls, BuildAdmission,
StandaloneUniqueBoundaries, RepeatedWithDiagnostics and FeatureSmoke. They cover Context/plain,
direct/prepared/true held consumers as
applicable, independent rows/types/counts/full logical diagnostics, current versus
private/RR/RC visibility, malformed/valid physical-option precedence, atomic
late failures, TYPE/lifecycle and actual native controls/PG replay. Controls
retain TEMP allocation and eager metadata/rows across replacement, distinguish
foreign destination mutation from source state and reject invalid late Restore
without changing the destination. Existing control error/cancellation definitions
remain authoritative; they are not replaced by fake hash-specific success paths.

The repeated-WITH regression requires a full syntax diagnostic at the second
WITH, nil malformed Prepare handles, no collision notice or state change, and
continued use of separately held valid SELECT/DML handles. Parser units preserve
the integrated UNIQUE/NULL policy and enforce suffix multiplicity/order without
changing valid partial-index/tablespace capability diagnostics.

The generic index grammar now preserves a WHERE AST, but HASH retains its native
partial-index exclusion, including syntactic WHERE TRUE. The existing method
admission owner returns unpositioned0A000 `partial indexes are unsupported` at
Execute, after UNIQUE/INCLUDE/multikey checks and before options, predicate/key
binding or root construction. Prepare remains quiet. PostgreSQL-valid controls,
full diagnostics, unchanged native state and same-name ordinary-HASH recovery
are covered by `TestReferencePartialIndexHashBoundary`. No HASH predicate
build/merge/lookup consumer is introduced.

`TestHashIndexLookupCancellation` calibrates actual duplicate-bucket probing
and residual consumption, requires terminal cancellation rather than a finite
successful result, preserves retained source/root identities and recovers on a
fresh execution. `TestHashIndexMergeCancellation` calibrates real touched-bucket
preparation and cancels that phase through the existing row-publication owner.
It requires unchanged committed rows/end roots/generation/index roots, immutable
predecessor buckets and a successful subsequent mutation. Test contexts reuse
existing Err polling/phase-stack seams, not mocked providers or production hooks.

Every changed reference definition must run in both modes before review or
integration. Each selected check invocation has one900-second deadline:

```sh
make check RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
make check REFERENCE=1 RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
make check RUN='^Test(HashIndex|Index|BtreeIndex|ExpressionIndex|Grouping|Distinct|SequenceTemporaryForeignReplacement)'
make check RUN='^TestReferenceHashIndexFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceHashIndexFeatureSmoke$'
make check RUN='^TestHashIndexLookupFeatureSmoke$'
```

The central fixture.HeldStatement keeps its existing three-field layout and
Context-only consumers. Its plain methods delegate to actual underlying DB/Tx
Prepare/Exec/Query, routing the matched query through the held SQL statement.
Unsupported underlying plain routes are explicit fixture misuse errors, not
Context fallbacks. This focused shared fixture extension requires additional
identical all-reference risk selection, each invocation under900 seconds:

```sh
make check RUN='^TestReference'
make check REFERENCE=1 RUN='^TestReference'
```

Parser generation/formatting and all verification are root-owned after edits.
Feature smoke must show actual candidates as well as public results. It does
not replace ordinary quickcheck or the post-merge finalcheck under one1800-second
deadline. Verification readiness is not a claim that those checks ran.
