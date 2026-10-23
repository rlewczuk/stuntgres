# Native compatibility limits

[README](../README.md) · [Driver contracts](DRIVER.md) · [Fixture controls](FIXTURES.md) · [Development](DEVELOPMENT.md)

[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/) is the authority
for SQL syntax and standard semantics. Full PostgreSQL compatibility is the goal,
not a claim about the current engine. The boundaries below describe native
implementation limits, not a supported-syntax inventory. A recognized construct,
catalog identity or stored value does not imply that every operation on it exists.

## Relation storage options

SET/RESET table storage parameters perform real logical validation and catalog
publication, not physical maintenance. Page packing/HOT behavior, TOAST
compression or movement, disk truncation, WAL/durability, autovacuum scheduling,
cost sleeps, freeze scans and worker creation are external mechanisms. Storing
an option does not claim any such operation ran.

Native descriptors retain validated documented `toast.*` intent on the owning
table. Only base options appear in its `pg_class.reloptions`; `reltoastrelid`
remains zero. No synthetic TOAST relation, OID, dependencies or statistics are
created. Native validation also checks retained TOAST intent on integer-only
tables. PostgreSQL skips that validation when no physical TOAST relation exists;
this is an explicit logical/physical boundary, not backend parity.

Native Dump/Reset/Restore preserves retained intent and explicit spelling for
tables and accepted materialized views, including the latter's captured contents
and population state through existing native snapshots. Emitted materialized-view
schema SQL remains `WITH NO DATA`: PostgreSQL ignores native snapshot comments,
so actual schema replay there preserves options and owner but leaves views
unpopulated until REFRESH. It does not replay native captured rows.
SQL replay on PostgreSQL preserves TOAST catalog facts only when a real TOAST
relation exists (for example on TEXT-bearing tables or materialized views).
Retained intent on an integer-only native relation cannot become a PostgreSQL
TOAST catalog fact.
Partition parents reject nonempty base options and retain the existing
unsupported partition-shape propagation boundary; options do not authorize
otherwise unsupported mixed shape changes. Leaf and typed/TEMP options preserve
the existing relation-kind and lifetime rules.

## Ordinary VACUUM

Ordinary `VACUUM` accepts the released legacy form and parenthesized option
form, qualified/quoted target lists, ONLY and descendant selection. Duplicate
targets do real repeated work. Options are converted in source order; final
effective values control execution, but an invalid earlier value is not hidden.
Quiet Describe/Prepare does not lock, resolve targets or perform maintenance.
Execution rejects transaction blocks with25001, preserving aborted25P02;
an idle read-only session may perform maintenance. MAINTAIN, effective relation
ownership, superuser and database ownership use ordinary role/ACL authority.
Database ownership does not grant maintenance of shared relations.

Native enabled FULL and ANALYZE report0A000 at Execute; their disabled forms
are accepted. Valid PARALLEL values0..1024 may execute serially. INDEX_CLEANUP,
TRUNCATE, DISABLE_PAGE_SKIPPING, PROCESS_TOAST and BUFFER_USAGE_LIMIT retain
released parsing/validation, but physical page, TOAST, buffer-ring, visibility-map,
FSM, WAL and worker behavior is inapplicable to logical storage. PROCESS_MAIN
false performs no native heap work. ONLY_DATABASE_STATS and SKIP_DATABASE_STATS
control real database-horizon refresh independently of per-relation work.
SKIP_LOCKED also governs the transient selection lock on explicit targets:
an unavailable lock emits warning55P03 and omits that target without waiting or
publishing work/progress. Cancellation and timeout errors are not skip warnings.
All explicit expansions finish before any work. Root permission, ONLY-partition
and selection-lock warnings retain encounter order, including earlier selection
warnings when a later target fails. Nonheap warnings instead occur during work,
after the actual maintenance lock and fresh permission check; they cannot precede
a later selection error. Delivery remains outside engine/manager mutex regions.

Each root expands breadth-first with direct children OID-ordered and deduplicated
only within that expansion. Denied roots still expand descendants; descendants
are not prematurely filtered by ACL or namespace name lookup. Repeated or
overlapping textual roots may maintain a relation repeatedly.
At work, captured identity is opened without resolving its old name, then current
ACL, kind, foreign-TEMP and partition gates precede PROCESS_MAIN. Authorized
nonheaps/partition parents need no heap store simply to survive selection.
Explicit-root unavailable/gone work emits55P03/42P01; anonymous descendants and
omitted-list candidates silently skip unavailable/gone work. Root selection-AS
skip omits its subtree, but a later root-work skip retains queued descendants.
Current same-identity storage/shape is bound only after admission; real native
epoch/replacement and detached-publication fences remain.

Qualified peer-TEMP maintenance lookup checks actual namespace USAGE, not data
access. VACUUM's private path policy also permits an explicitly configured peer
TEMP schema with USAGE, sharing ordinary $user/pg_temp/catalog precedence and
deduplication. Missing USAGE silently omits a configured path entry (normal
fallback or42P01), whereas an explicit qualified lookup reports schema42501.
Authorized foreign-TEMP work silently skips after lock/ACL/kind.
Ordinary native SELECT/DML retains its foreign-TEMP schema denial; no generic
data-access guard is relaxed. Native public LOCK partition setup remains0A000;
VACUUM's partition work-lock/current-ACL behavior is distinct from that utility.

Maintenance retains immutable historical roots and never changes row payload,
row/version identity or Born/End visibility. Other registered same-epoch
snapshots, builders, writers or live lock owners conservatively retain dead
heads. A quiescent pass removes committed matching dead heads and remaps existing
index entries without evaluating expressions or constraints. Freezing records
actual insertion/publisher FullXID provenance, not CommitSeq comparisons.
Foreign logical imports explicitly carry frozen/no-source-XID provenance;
self-replacement preserves ordinary provenance. This is logical bookkeeping,
not physical anti-wraparound/autovacuum or durability.
Even an empty new relation starts at the canonical safe cutoff, including an
older assigned transaction that may later insert under READ COMMITTED.
Every ordinary, prepared and rewritten unfrozen birth keeps the full-width
relation bound no later than its real publisher; low32 wrap is not ordering.
Database-bound calculation starts from that same safe active-publisher cutoff
before minimizing installed relation bounds. It therefore accounts for relations
that can be created after a database-only pass, not just currently installed
heaps. Unknown relation bounds or exhausted canonical cutoff do not publish a
new database bound; SKIP_DATABASE_STATS and database-only no-main-work remain
independent controls.

Heap/MV `relfrozenxid` and database `datfrozenxid` expose actual low32-bit XID
bounds. A completed native heap scan supplies `reltuples`; other unknown estimates
remain -1. Native manual statistics preserve committed DML counters and unchanged
`n_mod_since_analyze`; VACUUM updates live/dead counts, timestamp/count and clears
inserts-since-vacuum. Replacement/import resets scan, vacuum and cumulative
activity history, but initializes current live rows from its committed visible
capture (and current dead rows to zero). Later committed DML applies deltas to
that actual baseline before any VACUUM; imported rows are not invented inserts.
Same-object storage rewrites retain cumulative manual history and committed
counters. Committed-table TRUNCATE resets the current tuple estimate to unknown
(-1), not inferred0 or a stale VACUUM count, and resets live/dead/inserts-since;
actual post-truncate births count once. Refreshed materialized contents get actual
new publisher provenance, not an obsolete freezing bound or fabricated completion.
The three table-statistics views have the released30 columns. Unsupported
scan/I/O/duration/analyze timestamps are typed NULL, not manufactured counters.

Table-statistics per-column native capability (all three projections):

| Columns | Native fact |
|---|---|
| relid, schemaname, relname | Actual catalog identities/names |
| seq_scan, last_seq_scan, seq_tup_read, idx_scan, last_idx_scan, idx_tup_fetch | NULL; no scan/I/O statistics subsystem |
| n_tup_ins, n_tup_upd, n_tup_del | Committed logical publication deltas, not PostgreSQL process-local attempted-work counters |
| n_tup_hot_upd, n_tup_newpage_upd | Zero; no physical HOT/new-page updates |
| n_live_tup, n_dead_tup | Actual current logical live/retained-ended heads |
| n_mod_since_analyze, n_ins_since_vacuum | Committed modification/insert deltas; ordinary VACUUM preserves the former and clears the latter |
| last_vacuum, vacuum_count | Actual successful main-work completion timestamp/count; timestamp NULL before first completion |
| last_autovacuum, last_analyze, last_autoanalyze | NULL; these operations are absent |
| autovacuum_count, analyze_count, autoanalyze_count | Zero; no such event occurs |
| total_vacuum_time, total_autovacuum_time, total_analyze_time, total_autoanalyze_time | NULL; no invented duration accounting |

Progress per-column native capability:

| Columns | Native fact |
|---|---|
| pid, datid, datname | Actual active physical-session/database identity; visible even when details are redacted |
| relid, phase | Actual current target and entered maintenance phase |
| heap_blks_total, heap_blks_scanned, heap_blks_vacuumed | NULL; no physical heap blocks |
| index_vacuum_count, max_dead_tuple_bytes, dead_tuple_bytes, num_dead_item_ids | NULL; no physical index-vacuum cycles/dead-item buffer |
| indexes_total, indexes_processed | Actual attached published logical roots and completed remaps, not workers |
| delay_time | Zero; native work uses no vacuum cost-delay mechanism |

All progress columns except pid/datid/datname are NULL for an unauthorized viewer.

The15-column progress view describes actual synchronous work only, never a
queued lock wait or completed operation. Backend startup authenticated identity,
inherited role visibility and pg_read_all_stats control redaction: unauthorized
readers retain pid/dat id/name but see columns relid through delay_time as NULL.
Phases and processed-index counts describe real work; physical heap/dead-item
metrics are NULL. Logical VERBOSE emits INFO00000 with the existing
QuoteIdentifier convention: each qualified component is always SQL-quoted.
`vacuuming "public"."t" (native logical storage): ... live rows, ... retained dead versions, ... dead versions removed, ... versions frozen`.
It does not emit fabricated pages, duration, DETAIL, HINT or CONTEXT.
See [reference contracts](../reference/catalogs/README.md#vacuum-oracle-first-contract).

## PATH geometric boundary

PATH provides nonempty ordered point-list IO, open/closed identity, typed
transport and five scalar point-count predicates (`=`, `<`, `>`, `<=`, `>=`).
Coordinates and closure do not affect these predicates. There is no PATH
`<>`/`!=` or `~=` signature; unsupported calls report42883.
Scalar equality does not establish default sorting/grouping/hash/index classes.
Companion `_path` is lookup-only, not an admitted POINT array or subscript
provider. Published receive/send identities do not enable binary codecs.
`path(polygon)` creates a closed path retaining all vertices. Other PostgreSQL
geometric functions/operators, arrays and physical geometric indexes remain
native provider boundaries, not claims of PostgreSQL absence.
Native logical Dump/Reset/Restore preserves coordinates and closure but is
not a PostgreSQL host-control API or physical durability mechanism.
See [PATH proof](../reference/types/README.md#path-scalar-contract).

## Earthdistance domain boundary

Earthdistance1.2 provides the real nullable earth domain over installed cube,
its three ordinary CHECK constraints, text transport and exact native logical
replay. Cube must already be installed; its authentic missing-prerequisite
diagnostic/hint does not enable general CREATE EXTENSION CASCADE.
Earth permits point dimensions at most three, uses strict relative1e-6 surface
tolerance and preserves released cube NaN distance behavior. No coordinate padding
or global finite-value filter is applied.

The provider does not install the full PostgreSQL procedure/operator manifest:
earth(), cube_dim/cube_is_point/cube_distance, ll_to_earth, latitude/longitude,
earth_distance/earth_box, geo_distance/<@>, spatial/index operations, binary
codecs and SQL earth/cube arrays remain unavailable. Internal truthful CHECK
rendering is not callable helper authority. Domain catalog input/receive identities
do not grant binary transport; physical results retain CUBE metadata.
See [value contract](TYPES.md#earth-domain) and
[selected proof](../reference/types/README.md#earth-domain-contract).

## LINE geometric boundary

LINE coefficient/endpoint IO, typed table/bind/result transport and actual scalar
`=` use ordinary type and routine authority. Equality does not supply default
sort/group/index classes or absent scalar comparison operators. Coefficients
retain scale; endpoint input uses released slope construction and tolerance.

Ordinary native prepared statements retain analysis identity, not PostgreSQL's
custom/generic executable-plan cache. In the pinned LINE routine-authority
scenario, direct/new-prepared strict-NULL inputs fold to BOOL NULL on both
backends despite revoked EXECUTE. Repeated PostgreSQL held execution transitions
to a generic plan and reports42501 even for NULL parameters; native folds strict
NULL each execution and returns NULL. All nonnull callable/operator executions,
including the same held handles, enforce revocation and recover after grant.

PostgreSQL also supplies spatial line operators and other constructors; these
remain native implementation limits, not PostgreSQL-absent features.
`line(point,point)` is admitted by the bounded geometric conversion family;
it does not enable coordinate subscripting, companion arrays, binary receive/send,
native ABI, physical geometric indexes/storage or WAL. Published catalog IO
identities alone do not make routines callable. Native Reset/Dump/Restore is
logical host replacement, not a fabricated PostgreSQL control API.
See [LINE proof](../reference/types/README.md#line-scalar-contract).

## LSEG scalar boundary

`lseg` provides ordered endpoint-pair storage/IO, six genuine strict scalar
comparisons and zero-based POINT endpoint reads/writes. Equality compares
corresponding endpoints; the four inequalities compare epsilon-adjusted lengths.
These scalar operators do not enable ORDER BY, DISTINCT/GROUP BY, default
B-tree/hash indexes or serialized-text fallback comparisons.
`lseg(box)` and `lseg(point,point)` admit raw diagonals and ordered/coincident
endpoints. `_lseg` remains lookup-only. Other spatial operators/functions,
adjacent geometry conversions and SQL LSEG arrays are outside this provider.
In particular `~=` and `&&` have no LSEG overload and report42883.
Public database/sql text bindings/results and native logical Dump/Reset/Restore
use the existing value and control paths. Binary input/output is explicitly
unsupported (0A000); catalog receive/send identities are not codec authority.
See [type semantics](TYPES.md#lseg) and
[reference proof](../reference/types/README.md#lseg-oracle-first-contract).

## BOX geometric boundary

BOX storage/IO, five area comparisons (`=`, `<`, `<=`, `>`, `>=`), coordinate
sameness (`~=`), and two zero-based raw POINT components use ordinary typed
SQL/driver paths. Raw point assignment does not renormalize stored corners.
BOX has no `<>`/`!=` operator and no default B-tree/hash comparison authority:
ORDER BY, DISTINCT/GROUP BY and reached composite comparison leaves cannot
invent serialized-text comparisons or index classes.

POINT's coordinate/predicate boundary is documented separately below.

Native Dump/Restore is logical host-authorized replacement, not PostgreSQL
physical storage, WAL or crash durability. Its isolated INSERT replay preserves
raw inverted BOX pairs; replaying those same textual literals as ordinary
PostgreSQL INSERT input normalizes them. No PostgreSQL Reset/Dump/Restore API is
invented by the reference scenarios. See [BOX proof](../reference/types/README.md#box-scalar-contract).

## POINT geometric boundary

POINT provides binary64 coordinate IO, typed columns/binds/results, eight
point–point predicates with their aliases and callable routines, and zero-based
FLOAT8 coordinate reads/writes. `<>`/`!=` is genuine inequality; it does not
provide ordinary equality `=` or `< <= > >=`, default ordering/grouping/hash
authority or an index class. Unsupported calls fail rather than comparing
serialized text. Geometric tolerance is not exact datum identity.

The [twenty-one selected geometric constructors/conversions](TYPES.md#geometric-constructors-and-conversions)
are explicit exceptions to the remaining constructor/cross-geometry limits.
Other arithmetic/distance and cross-geometry operations, companion SQL arrays,
binary receive/send, physical geometric indexes/storage, WAL and a native
PostgreSQL ABI remain outside this provider. Released catalog identities
alone do not make those mechanisms callable. Native Dump/Reset/Restore is
logical host replacement, not an invented PostgreSQL control API.
See [type contracts](TYPES.md#point) and
[reference proof](../reference/types/README.md#point-oracle-first-contract).


## Circle scalar

Builtin `circle` is concrete; `_circle` remains lookup-only. Circle-only
`= <> < > <= >= ~= << >> &< &> <<| |>> &<| |&> @> <@ && <->` and
`radius(circle)` execute through selected signatures, including strict NULL.
The six ordinary predicates compare fuzzy areas and do not provide default
equality/ordering capabilities: ORDER BY, DISTINCT/GROUP BY and default
B-tree/hash operator classes remain unavailable. NaN area predicates are all
false, including `<>`. The
[twenty-one selected constructors/conversions](TYPES.md#geometric-constructors-and-conversions)
and [eleven geometric measurements](TYPES.md#geometric-measurements) are admitted
separately, including POINT-valued centers, area/diameter, default twelve-point
or explicit-count circle-to-polygon sampling, LSEG-valued `diagonal(box)`,
`width(box)`, `height(box)` and `slope(point,point)`.
Other adjacent-geometry constructors/conversions, transformations, circle arrays
and spatial indexes remain unavailable. Path closure/length/count and unary
LINE/LSEG slope overloads remain outside the selected measurement family.
Text and 24-byte binary codecs, metadata and native logical controls share the
[type-owned circle contract](TYPES.md#circle).

## Polygon scalar

Builtin `polygon`604 is concrete and distinct from `path`602;
`_polygon`1027 remains lookup-only. Ordered vertex input/output and exactly
`polygon ~= polygon`, `polygon @> point`, `point <@ polygon` execute, including
strict typed NULL and selected routine authority. `~=` is cyclic/reverse vertex
sameness, not area equality; containment includes interior and boundary points.
Closed PostgreSQL paths also support interior containment: no contrary path
behavior or new path execution is asserted here.

PostgreSQL itself has no ordinary polygon `= <> != < <= > >=` overloads and no
`area(polygon)`. Such calls fail rather than comparing serialized strings;
ORDER BY, DISTINCT/GROUP BY and default equality/order/hash/index classes are
unavailable. `box(polygon)`, `circle(polygon)`, `path(polygon)`, `point(polygon)`,
`polygon(box)`, `polygon(circle)`, `polygon(integer,circle)` and `polygon(path)`
are selected exceptions in the [twenty-one-signature family](TYPES.md#geometric-constructors-and-conversions).
BOX corners and closed PATH vertices retain their order/count; open PATH fails,
and circle sampling preserves unsnapped residuals with inclusive fuzzy-zero
radius rejection before count/size/arithmetic errors.
Other polygon spatial operators/functions and conversions exist in PostgreSQL
but remain unimplemented. Polygon arrays, subscripting and binary codecs remain
outside this bounded provider.

Defaults, CHECKs, views and typed data survive native logical Dump/Reset/Restore,
including full float precision despite low display precision. This is in-engine
logical replacement, not PostgreSQL physical disk/WAL/crash durability, spatial
index access, network/wire binary transport, replication or native ABI/process
integration. Receive/send catalog identities do not grant executable codecs.
See [polygon values](TYPES.md#polygon) and
[reference proof](../reference/types/README.md#polygon-scalar-contract).

## Partition navigation

The sole signatures are `pg_partition_tree(regclass) -> setof record`,
`pg_partition_ancestors(regclass) -> setof regclass`, and
`pg_partition_root(regclass) -> regclass`. Tree fields are exactly `relid
REGCLASS`, `parentrelid REGCLASS`, `isleaf BOOL`, and `level INT4`; ancestors'
FROM output is named `relid`. Tree accepts `rootrelid` and ancestors accepts
`partitionid` as named arguments; root has no named formal. Ordinary REGCLASS
coercion includes explicit TEXT names, without an alternate relation resolver.

Trees include the input, reset levels to zero at that input, and retain its
actual immediate parent. Only a genuine hierarchy root has a NULL parent.
Leaf status follows relation kind, so a childless partitioned table or index is
not a leaf. Ancestors include self; root follows parents to the true root.
Index ancestry uses actual attached-index edges, not the indexed table edge.
Existing UNIQUE/PRIMARY and manual UNIQUE ONLY/ATTACH indexes supply these
hierarchies, including incomplete partitioned indexes.

Ordinary standalone tables/indexes, ordinary inheritance, views, materialized
views, sequences and missing numeric OIDs yield empty tree/ancestors and NULL
root. SQL NULL retains typed empty sets or a typed scalar NULL. Missing textual
names instead fail ordinary REGCLASS input. No SRF traversal order is promised.
No table SELECT privilege is required; ordinary function EXECUTE authority
applies. Pinned PostgreSQL 18.6 eliminates scalar root NULL before EXECUTE
admission (constant and parameterized, direct and held), but not SRF NULL calls.

Each execution uses its retained catalog generation, including transactional
attachments and savepoint rollback. Held statements do not cache topology;
already opened native results retain their captured values. Detaching a
partitioned subtree creates a new root; detaching a physical leaf makes that
leaf ineligible. Logical Dump/Restore preserves actual table/index edges and
symbolic stored inquiries, not serialized function results. This does not
extend partition DDL or add nonunique partitioned indexes or physical storage.
See [selected proof and smoke](../reference/catalogs/README.md#partition-navigation).

## Tablespace inquiries

The sole signatures are `pg_tablespace_location(oid) -> text` and
`pg_tablespace_databases(oid) -> setof oid`. Location returns empty text for
`pg_default` (1663), `pg_global` (1664), and zero, which maps to native's only
supported default, pg_default. This logical default invariant does not change
`pg_database.dattablespace`'s existing unknown/NULL metadata.

Native pg_default occupancy is exactly the current attached database descriptor's
OID once, including fresh/reset databases: represented nonshared system catalog
relations already occupy that logical database. It is independent of user table
existence, table row count, SELECT access and RLS. Other opaque DSNs or independent
Drivers are not cluster databases and are never enumerated. PostgreSQL instead
scans physical database directories, including its template databases; empty
tables have files, and dropped relations may leave observable directory contents
until deferred physical cleanup. Native makes no disk-equivalence or new
DROP TABLESPACE promise.

Location of any nonbuiltin OID fails with 0A000,
`pg_tablespace_location physical directory inspection is unsupported in embedded mode`.
It does not inspect the host filesystem. PostgreSQL can return real custom paths,
fails absent links with 58P01 and inaccessible directories with 42501. Those
physical errors are not fabricated natively; physical CREATE/placement remains
outside the engine.

Databases on pg_global warns `global tablespace never has databases` and returns
no rows. Zero or any other unknown OID warns `N is not a tablespace OID` and
returns no rows (zero is not remapped for this function). Both warnings are
WARNING/01000 and honor `client_min_messages`; they arise only on demanded
invocations. Neither inquiry requires tablespace CREATE, database CONNECT,
monitor membership or table SELECT. Ordinary routine EXECUTE admission applies.
Strict scalar NULL arguments can remove the location call before EXECUTE
admission: both backends return TEXT NULL even after location EXECUTE revocation
on the tested NULL routes. Non-NULL location and the databases SRF enforce
revoked EXECUTE on both backends, including strict NULL databases invocations.

Strict location NULL produces one TEXT NULL; strict databases NULL produces an
empty typed OID set, not a NULL row. Scalar FROM NULL still has one row;
LEFT LATERAL/ROWS FROM can independently synthesize NULL padding. There is no
portable ordering for database OIDs without ORDER BY. Prepared recipes and stored
expressions remain symbolic; every execution captures its own catalog authority,
and native Dump/Reset/Restore needs no new tablespace serialization.

## Text-search scalar boundaries

TSVECTOR/TSQUERY storage, input, canonical output and selected algebra use the
ordinary type, binder, parameter, metadata and logical-control paths. Query
`&&`, `||`, prefix `!!`, `<->`, `@>`, `<@`, two/three-argument `tsquery_phrase`,
and vector `||` execute through their genuine strict immutable routine providers.
Containment is exact lexeme-set containment, not Boolean implication or prefix
matching. Phrase distance defaults to1 and accepts0–16384. Vector concat offsets
right positions by the global left maximum, caps positions at16383/256 retained
entries, and keeps the first newly saturated position's weight. NULL and empty
are distinct; operations do not decode stored operands or repeat input notices.
Normalization, dictionaries/configuration, matching (`@@`), ranking, constructors,
FTS arrays, generalized comparison/grouping and FTS index execution remain
excluded. Fixed IO/companion identities do not promise callable IO or binary codecs.

The codec reproduces the released fixed32 pending-operator stack separately
for every parenthesis frame. Parsing/rendering are iterative: native legal
32767/32769-node flat queries do not imitate the pinned server's machine
C-stack54001. There is no invented32768-node cap or native max_stack_depth
setting/Hint. Oversized query operands enforce the real released byte limit;
the pinned pq adapter exposes an ordinary malformed ErrorResponse transport
error for these megabyte messages. Reference proof obtains the actual server
54000/full primary through PostgreSQL-only `pg_input_error_info`, while native
asserts its complete logical error. That oracle mechanism does not implement
the inquiry function natively or turn a transport failure into SQLSTATE parity.

Selected unknown/typed literal and typed-bind empty-query notices match their
actual input phases. Explicit runtime TEXT casts keep existing native
per-execution evaluation, whereas PostgreSQL folds a held constant cast at its
first execution. This is an optimizer/evaluation boundary, not notice state
on values, a successful no-op conversion or a second prepared cache.
The [scalar contract](TYPES.md#text-search-scalar-values) documents exact
position conversion, limits and NULL/empty behavior; the
[reference inventory](../reference/types/README.md#text-search-scalar-io)
includes independently asserted metadata, diagnostics and native controls.

## Extension removal

`DROP EXTENSION` accepts installed extension name lists, IF EXISTS, and
RESTRICT/CASCADE through the existing catalog/transaction authority. It removes
actual provider manifests, including automatic type aliases; it neither loads
unavailable providers nor broadens their bounded member inventories. Extension
ownership authorizes removal independently of member or schema ownership.
RESTRICT rejects external stored dependencies; CASCADE removes their closure,
not unrelated objects or installation schemas. Transaction rollback restores
the original callable member identities. Native Dump/Reset/Restore retains only
surviving persistent installations, plus the existing authentic live TEMP
provenance rules. ALTER EXTENSION remains outside this boundary; arbitrary
module loading has the separate [LOAD capability boundary](#load-native-library-boundary).
See [removal proof](../reference/catalogs/README.md#extension-removal).

## LOAD native-library boundary

`LOAD` accepts a decoded SQL string filename. Protocol/`database/sql`
preparation is effect-free: authorization and filename checks occur when the
statement executes, using the current role's actual SUPERUSER attribute rather
than session identity or inherited membership. SQL `PREPARE ... AS LOAD` is
not admitted, matching PostgreSQL's preparable-statement grammar.

Non-superusers require the exact case-sensitive `$libdir/plugins/` prefix
and no further `/` in its suffix. This is a lexical restriction, not path
normalization; backslashes are ordinary filename characters under the pinned
Linux contract. Denied names return `42501` with PostgreSQL's English primary.
Authorized slash-containing names beginning `$` accept only the `$libdir`
macro; invalid macros return `42602`. A leading `$` without any slash is not
itself an invalid macro.

After those checks, native always returns `0A000`:
`LOAD native libraries are unsupported in embedded mode`.
It never inspects host files, searches directories, expands paths, loads process
code, invokes initialization functions or publishes a library/extension record.
This includes installed names such as `plpgsql` and repeated attempts. Missing
libraries on PostgreSQL can instead return real `58P01`; native does not invent
that filesystem diagnosis or claim a successful no-op. LOAD is permitted in
explicit/read-only transactions before its ordinary execution diagnostic;
errors retain normal failed-transaction and savepoint recovery behavior.

See [independent outcomes and selected proof](../reference/catalogs/README.md#load-native-library-boundary).


## UPDATE assignment-group boundaries

Row constructors and multi-output subqueries are admitted for ordinary UPDATE,
not MERGE or ON CONFLICT actions. These adjacent native actions retain their
explicit row-assignment capability error, including groups after scalar SET items.
Non-domain ordinary UPDATE DEFAULT retains its missing-provider boundary even
inside a constructor group. Domain-target DEFAULT uses the existing column
override, domain fallback or fresh domain NULL input; an explicit column DEFAULT
NULL masks a domain recipe, while supplied NULL requests no recipe.
Generated-column DEFAULT keeps its existing admission. DEFAULT does not become
an ordinary scalar/ROW value.

As in PostgreSQL, actual rule NEW substitution cannot consume a current-level
grouped-subquery output. This is not a blanket rejection of grouped UPDATE when
a rule exists: constructor NEW recipes, OLD and unaffected NEW columns remain
ordinary recipes, and a stored rule action may itself perform a grouped UPDATE.
Native dumps persist live resulting rows and stored grouped rule definitions,
not assignment history or execution scratch. See [selected contracts](../reference/dml/README.md#row-valued-update-assignments).

## User-defined ordinary aggregates

The selected `CREATE AGGREGATE` surface is `SFUNC`, `STYPE`, optional `INITCOND`
and optional `SSPACE`, for ordinary fixed zero/N concrete inputs. Existing
callable polymorphic support routines may specialize to a concrete state and
input signature; the aggregate declaration itself is not polymorphic. Support
selection uses real overload ranking with an exact declared shape, no omitted
defaults or variadic expansion, and binary-compatible transition/result checks.
The state is the result: no final, combine, serialize/deserialize, moving or
inverse helpers, ordered/hypothetical declarations, parallel aggregation,
variadic/defaulted aggregate declarations or `CREATE OR REPLACE AGGREGATE` are
added. Existing SQL-function attributes are not expanded by this feature.

Ordinary-input collation and collatable result metadata use existing derivation
rules, excluding ordering-only and FILTER expressions.
The state/result collation is a fallback only without a collatable ordinary
input; a domain's declared collation does not compete with POSIX/default inputs.
Quoted aggregate SQL support receives effective input collation implicitly, with collation-sensitive
description validity; frozen SQL-standard bodies remain creation-analyzed.
This selected support annotation does not expand generic scalar SQL-function
collation behavior or add another collation provider.

Strict and non-strict execution, original extracted seed text, signed int32
estimates and the single syntax example are documented in the
[execution contract](QUERIES.md#user-defined-ordinary-aggregates). Actual procedure,
aggregate and dependency identities participate in metadata, authorization and
native replay. Support EXECUTE is checked as aggregate owner without changing the
SQL body's ordinary invocation authority. Native default-ACL owner replay requires
roles to exist in the receiving environment. Explicit aggregate or SQL-routine
ACLs remain unsupported by Dump, which rejects them before output rather than
silently dropping permissions. This does not add prerequisite SQL-routine ACL
serialization. See [selected proof](../reference/aggregates/README.md#create-aggregate-contract).

## Cast declarations and native representations

CREATE CAST supports real function, INOUT and WITHOUT FUNCTION declarations,
with explicit-only (default), assignment or implicit admission. Cast identity is
the ordered endpoint pair: no cast schema, owner or ACL is invented. Ownership
of either endpoint and USAGE on both are required; binary declarations also
require superuser and PostgreSQL physical/category checks. Domain declarations
warn and remain catalog objects but do not override domain/base coercion.

Function casts execute their selected real routine with the source evaluated
once, target typmod/-1 and explicit-context flag. Nonstrict functions receive
NULL; EXECUTE and own-write/rollback semantics remain ordinary routine semantics.
INOUT invokes descriptor output/input in the current statement environments.
Declared context refusal blocks generic IO fallback; unknown literals still
enter through target input. ARRAY constructor children bind element conversions
before an array-level declaration can apply; typed arrays retain their separate
whole-array/elementwise path. No arbitrary transitive cast search is introduced.

Native binary casts deliberately support proven word, byte and ordered-geometry
families, not a PostgreSQL heap ABI emulator. Equal PostgreSQL size/alignment
alone is insufficient: unsupported representation pairs fail0A000 before
publication; genuinely malformed payloads fail0A000 atomically at evaluation.
FLOAT4 raw32 words, including signaling NaNs, remain exact through relabel,
binary transport and native persistence. Ordinary arithmetic creates fresh bits.

Existing aggregate SFUNC and strict-seed admission accepts only direct implicit
binary compatibility, not assignment/explicit edges or function/INOUT conversion.
Initialized aggregates retain the admitted physical adapter after DROP/replacement.
Strict aggregates without INITCOND additionally recheck current binary feasibility
at executor startup, including held/stored calls and empty input; loss of that
compatibility raises42P13 without choosing a replacement conversion.

Constructor replay preserves distinct bounds inside composite/array carriers.
A non-one-based multidimensional constructor still needs its own bounded INSERT
target; a nested value without such a destination cannot be dumped through this
recipe. Closed snapshot SQL admits only the generated array-fill/concatenation
forms with genuine builtin providers, not custom functions sharing their names.

Stored expressions keep selected routine/IO/relabel meaning after DROP CAST;
native held analysis instead notices changes to relevant pairs (including an
observed absence), not every DDL publication. PostgreSQL may keep the already
cached executable choice; the lifecycle reference scenario asserts native77
and PostgreSQL41 independently after the relevant edge is recreated.
Qualified dump declarations and real xid→real bootstrap CREATE/DROP statements
preserve raw32 leaves and the final cast catalog; Restore uses isolated ordinary
DDL and publishes only after complete validation/cancellation checks.

## Scalar SQL routines

SQL-language `CREATE FUNCTION` accepts named/anonymous IN, OUT, INOUT and
array VARIADIC formals, input defaults, and supported scalar/VOID results.
A single OUT/INOUT implies its scalar result when RETURNS is omitted; an
explicit result must agree. Multiple-output record execution is separate.
Scalar SQL functions also use the ordinary single-row FROM source adapter.
A sole OUT/INOUT name labels that source column and survives a table alias;
this does not enable multiple-output record execution.
`CREATE PROCEDURE` retains its existing IN declarations/body validation without
a RETURNS clause; procedures are catalog objects, not scalar functions.
Procedure parameter arrangements and SQL CALL execution remain separate.
Quoted bodies use complete semicolon-separated SQL scripts, including normal
comments/quoting. Standard bodies use `RETURN expression` or
`BEGIN ATOMIC statement; ... END`, defaulting to LANGUAGE SQL when omitted.
Named, `routine.argument` and `$n` references use the same private typed slots.
Columns win over unqualified/qualified formal fallback, and real column
ambiguity is not hidden by a formal. Positional formals are closed even with
zero inputs; overflow fails with 42P02.

Namespace visibility, signature masking, exact/implicit/unknown argument
selection and EXECUTE checks use the existing callable authorities. Aggregate-
and type-like spellings are not reserved from SQL routines: the selected actual
signature determines scalar versus aggregate execution. Exact typed calls precede
the existing type-name cast shortcut; unknown literals and external parameters
retain their distinct resolution rules. Standard CAST syntax is unchanged.

Function actuals support positional, named (`=>`/`:=`) and positional-first
mixed notation, including omitted default gaps. Pure OUT parameters are excluded
from input identity, actuals and `$n`; INOUT participates in both projections.
Expanded VARIADIC requires elements unless an actual declaration default permits
omission. Explicit VARIADIC passes the declared array without unpacking empty,
NULL, nondefault bounds, multidimensional shape or NULL elements.
All-formal declaration names are retained separately from input names.

Declaration defaults are checked immutable scalar recipes using Assignment
conversion; actual arguments still use Implicit conversion. Defaults capture
selected identities and hard dependencies even with quoted bodies. CREATE,
Prepare and losing candidates do not evaluate them. Each omitted default is
evaluated once per execution with fresh private input slots.

CREATE validates quoted bodies by default, without effects; standard bodies
are analyzed immediately and retain genuine checked object/overload captures
and hard dependencies. Invocation prepares each reached quoted command in
sequence, refreshing own-write/delete visibility. Standard invocation refreshes
checked recipes with current admission/ACLs without reparsing SQL names.
The actual nonvoid final command must be SELECT or supported
INSERT/UPDATE/DELETE/MERGE RETURNING, with one Assignment-compatible column.
Eligible final SELECT uses first-row demand; every DML and every nonterminal or
VOID SELECT completes its required evaluation/effects. No rows yield typed
NULL. SQL VOID yields NULL and discards all body outputs.

All commands borrow the caller's one statement/transaction; a later error or
cancellation rolls back transactional prefix writes. Standard ATOMIC creates no
new transaction and RETURN is not procedural early exit. Transaction control
is rejected when reached in quoted functions; standard utilities are rejected
at definition. Unsupported utility/provider paths retain their native boundary,
not successful no-ops. Direct/mutual recursion shares the native 128-active-body
safety bound (54001), not PostgreSQL's byte-valued `max_stack_depth` setting.

Calls are VOLATILE, nonstrict, invoker-authorized and parallel-unsafe, with fresh
data and permissions and existing prepared-analysis invalidation. Scalar results
use assignment conversion, distinct from implicit argument conversion. Existing
DEFAULT/stored-expression consumers retain symbolic checked routine identities;
CREATE/default validation does not evaluate bodies.

`DROP FUNCTION`, `DROP PROCEDURE` and `DROP ROUTINE` support qualified names,
overload input identity, omitted versus empty signatures, grouped targets,
`IF EXISTS`, `RESTRICT` (default) and `CASCADE`. DROP argument names are noise;
IN/INOUT/VARIADIC types identify inputs and OUT types are ignored without lookup.
Selection never applies call-site coercion/default expansion. Generic routine
selection includes installed aggregate/window/procedure kinds; FUNCTION rejects
aggregates and PROCEDURE rejects explicit function signatures. System and
extension members retain their protection even with CASCADE/IF EXISTS.

Deletion resolves and authorizes every root before one atomic publication.
Existing dependency owners remove the smallest dependent object; a dependent
default/check does not remove its table or rows. Incidental quoted-body calls
create no hard callee edges: surviving callers can fail until recreation.
Declaration-default dependencies are hard edges regardless of body form.
Native replay retains those unresolved bodies only for a structured terminal
missing callable; other errors still abort replacement, and deferred semantic
checks run at invocation once resolution succeeds. Closed formals always validate.
DROP retains read-only-first admission and the native autocommit-only schema-DDL
boundary. CALL, trigger providers, aggregate creators and external language
execution remain separate capabilities. OR REPLACE, CALL, ALTER routine lifecycle
DDL, PL/pgSQL, security/other attributes and set/composite returns remain
unsupported. Declaration DDL retains existing read-only/autocommit boundaries.
This is not an external ABI, general procedural runtime or complete
`pg_proc`/`pg_language` projection. Supported pg_proc columns include genuine
routine kind and SQL formal names.
See [logical replay](FIXTURES.md#sql-routine-declarations-and-callers) and
[selected evidence and smoke](../reference/query_core/README.md#scalar-sql-routine-contract).
Function-parameter selectors and smoke are documented in
[reference/operators](../reference/operators/README.md#routine-parameter-binding).

## Comparison resolution and predicates

Executable type/shape admission is separate from operator admission. Comparison
binding uses released exact signatures, direct implicit conversions and generic
identity rules, not transitive cast searches or assignment-only conversions.
CID and ACLITEM remain equality-only. XID/INT4 has its released one-way equality
signature, not reverse or ordering overloads. JSON and JSONPATH do not gain natural
comparison.
The [reference-owned ledger](../reference/type_resolution/comparison-ledger.md)
records the concrete universe, formal signatures, structural rules and unavailable
non-comparison construction/codec routes.

Typed VARCHAR compared with NAME selects an existing NAME/TEXT or TEXT/NAME
provider without clipping the VARCHAR or changing its prepared OID1043.
CHAR→TEXT, PG_NODE_TREE→TEXT and TIME→INTERVAL use shared implicit coercion
providers. Structural node output remains the existing native symbolic
representation; immutable output is computed lazily and retained per payload.

Logical composite comparisons permit different named outer identities but demand
identical reached field types and their own providers. Nonnull NULL fields are
equal and sort after nonnull fields; whole SQL NULL remains strict. Row
constructors retain field-wise three-valued semantics and multi-field B-tree
eligibility. Boolean domains use ordinary Boolean coercion for truth predicates;
undecorated NULL distinctness tests whole-value nullness, not row-field nullness.
These comparisons do not grant absent storage, grouping/hash or index providers.

Quantified named ROW constructors follow PostgreSQL's field extraction; domain
wrappers and scalar-subquery composites remain whole values. Stored views freeze
the analyzed member coercions and actual dependencies, not the erased outer
constructor type. Dropping/recreating that outer type does not reinterpret the
view; retained enum/domain/nested/whole-value identities remain authoritative.

Canonical catalog ANYARRAY NULLs fail unresolved polymorphic comparisons with
42804, while incompatible concrete-array contexts retain42883 no-match.
This does not grant ANYARRAY input, storage or general parameter codecs.
NULLIF and simple CASE preserve this same generic-enforcement diagnostic rather
than relabeling it as missing operator lookup; ordinary lookup errors retain
their own source positions and explicit-cast hints.

PostgreSQL's six record-image operators `*=`, `*<>`, `*<`, `*<=`, `*>`, `*>=`
compare physical Datum/tuple bytes. Native logical values have no equivalent
layout; these operators retain the explicit `invalid comparison operator`
boundary rather than a logical-equality or Go-memory fallback.

## SQL version and Unicode inquiries

The zero-argument `version()`, `unicode_version()` and `icu_unicode_version()`
functions return TEXT with ordinary direct/prepared result metadata.
`version()` is STABLE and returns exactly
`PostgreSQL 18.0 compatible; embedded stuntgres`. This is an embedded
compatibility declaration, not a PostgreSQL process, compiler or platform banner.
It does not change `server_version`, `server_version_num` or wire startup metadata.

The IMMUTABLE Unicode inquiry returns `16.0`, derived from the same embedded
Unicode16.0.0 data used by normalization, case conversion, assignment and regex
providers. Native execution has no ICU provider, so the IMMUTABLE ICU inquiry
returns a concrete TEXT SQL NULL, not an empty string. The pinned PostgreSQL18.6
oracle independently reports Unicode16.0 and ICU15.0; these providers need not
match each other. No host Go Unicode or runtime ICU version is substituted.
These stateless functions need no serialized provider state; supported stored
views and DEFAULT expressions replay their symbolic calls.
See [selected proof and smoke](../reference/sessions/README.md#sql-version-and-unicode-inquiries).

## Text slicing

Text `left`/`right` and positional `substring`/`substr` use INT4 codepoint
indexes/counts, with one-based substring positions. Positions below one clip
after calculating the end from the original start; omitted count means to-end.
Negative substring counts raise22011 `negative substring length not allowed`;
strict NULL input suppresses that provider error. Negative left/right counts
drop characters from the opposite end. The pinned18.0 INT_MIN exception is
explicit: `right` retains its source, while `left` returns empty TEXT.

No implicit BIGINT/numeric/float/TEXT-to-INT4 cast is added. Unknown parameters
select existing overloads during analysis: two-argument `substring` prefers the
POSIX TEXT pattern, three-argument prefers SIMILAR TEXT pattern/escape; use
integer type information for positional slicing. `substr` has no regex
overload. Output is unrestricted TEXT and does not normalize or combine
graphemes. BYTEA slicing keeps byte units and its existing DEFAULT boundary;
BIT slicing keeps logical bit units. Native controls replay symbolic selected
calls, not an expanded arbitrary SQL import language.
See [selected proof and smoke](../reference/strings/README.md#text-slicing-selected-proof).

## Logarithms, roots, powers and buckets

Selected NUMERIC logarithms and roots use decimal kernels with release-selected
display scales, not FLOAT8 round trips. FLOAT8 finite transcendental last bits
can vary with libm; exact domains, IEEE categories and zero signs remain part of
the contract. Binary `log` has no FLOAT8 signature; `lgamma` has no NUMERIC
signature. Existing implicit casts may still select an admitted formal type.

Four-argument `width_bucket` follows pinned PostgreSQL18.0: any NaN operand or
bound is rejected, bounds must be finite and unequal, and count must be positive.
Maximum INT4 count is valid until an actual count-plus-one result overflows.
The array overload uses existing comparable types and comparison/collation
authority. Arrays must be one-dimensional and contain no NULL elements.
**Ascending thresholds are a caller precondition, not a validated condition.**
Unsorted thresholds yield PostgreSQL's binary-search behavior, not a sortedness
error or mathematical bucket guarantee. Equal thresholds are allowed; physical
array lower bounds do not change bucket numbering.
The selected threshold argument can consume existing enum-associated arrays,
including typed NULL/empty/bound arrays and symbolic stored calls. This narrow
argument role does not enable enum-array columns, domain bases, general SELECT
casts or unrelated function arguments. Existing enum descriptor ownership,
association and logical-rank comparison remain authoritative.
See [selected tests](../reference/numeric/README.md#logarithms-roots-powers-and-buckets).

## Identifier strings

`parse_ident(str text, strict boolean DEFAULT true)` splits qualified identifier
text into TEXT[], without resolving objects or truncating names. Its optional
mode controls suffix rejection, not SQL NULL strictness: either supplied NULL
returns typed NULL. Names `str` and `strict` may be supplied in either written
order. Unquoted folding changes ASCII uppercase only; UTF8 bytes are preserved.
Only the six ASCII scanner whitespace bytes separate components. Comments,
Unicode whitespace and nested SQL identifier syntax are not interpreted.
Nonstrict mode ignores suffixes after a complete component, but never accepts a
missing/empty/unclosed component after a consumed dot. This does not expand the
general function resolver, cast graph, SQL identifier lexer or catalog SQL rows.
See [selected proof and smoke](../reference/strings/README.md#identifier-parsing-selected-proof).

## Catalog linkage and role names

`pg_catalog.pg_get_catalog_foreign_keys()` returns the complete PostgreSQL18.0
**219-link** catalog metadata bag as
`(fktable REGCLASS, fkcols TEXT[], pktable REGCLASS, pkcols TEXT[],
is_array BOOL, is_opt BOOL)`. Ordered source/target column arrays describe
catalog annotations, not user foreign-key constraints or scans of pg_constraint.
`is_array` applies to the final source column; `is_opt` permits zero references,
not SQL NULLs. All six returned fields are nonnull. Traversal order is not a SQL
ordering guarantee; use explicit ORDER BY where order matters.

The complete linkage inventory does not create adjacent SQL catalog providers
or phantom queryable columns. Existing captured REGCLASS presentation resolves
supported descriptors through the current search path; unavailable descriptors
retain ordinary numeric spelling. Cast table fields to OID for portable full-bag
comparisons rather than filtering links through the partial native pg_class.

`pg_catalog.pg_get_userbyid(oid) -> NAME` returns the exact raw role name,
without identifier quoting, for LOGIN and NOLOGIN roles alike. Missing IDs,
including zero, produce `unknown (OID=<unsigned decimal>)`; NULL produces typed
NAME NULL. Existing implicit integer/OID-alias coercions remain available;
typed TEXT is not a new overload and manual `role` is not a named input.
Lookup uses the statement's immutable authorization root and needs no
pg_authid SELECT privilege. New executions of held statements see current role
publication, while already returned native rows retain captured names/types.

Both routines are strict STABLE, parallel-safe declarations on the existing
typed expression path. Record aliases/expansion, ordinality, ROWS FROM and
ordinary public driver metadata use existing query infrastructure. Stored
view/default calls remain symbolic through Dump/Restore; the stateless linkage
inventory has no separate serialized state and roles retain existing separate
provisioning requirements. See [selected proof and actual feature smoke](../reference/catalogs/README.md#catalog-relations-oracle-first-contract).

## Transaction identity information

The six selected PostgreSQL18 signatures execute with genuine typed XID/XID8
values: `age(xid) -> INT4`, `mxid_age(xid) -> INT4`, current and if-assigned
zero-argument inquiries `-> XID8`, `pg_xact_status(xid8) -> TEXT`, and
`pg_get_multixact_members(multixid xid) -> SETOF RECORD(xid xid, mode text)`.
Current-ID requests assign even in READ ONLY; if-assigned/age/status/member
inquiries and preparation never assign. The first demanded XID age anchor is
stable through the top transaction; MultiXact age reads the live next counter.
Both ages use signed32 modular subtraction and their PostgreSQL special IDs.

The full64 allocator skips reserved low32 values and refuses exhaustion or live
low32 aliasing before publication. Low32 lock/SSI/timestamp carriers remain
intentional mirrors, not independent allocators. Status retains actual assigned
commits/aborts, checks real active/prepared ownership, and returns NULL below the
replacement horizon; timestamp tracking is not a prerequisite.

MultiXacts are immutable bags produced by installed physical row locks and actual
modifying events, not a projection of current grant bits. Predecessor action and
successor state have distinct native version identities. A successor copies the
original raw member ID and entire bag, not the newly computed action ID or a
live-only key-share reconstruction. Member-set reuse is transaction-local and
bounded to 256 cached IDs, including sets learned through member inquiry.
Modes are keysh/sh/fornokeyupd/forupd/nokeyupd/upd; the same XID can have multiple
lock statuses, with at most one modifying member. Queues, failed conditional
acquisition, empty scans and rejected preheap CHECKs do not create members.
Genuine lock-before-qual and pre-index actions can survive a later SQL failure
as retained history. Output ordering is unspecified.

History lasts for the engine lifetime until successful Reset/Restore advances
retention horizons without resetting counters. Failed replacement changes none
of it; Dump contains symbolic stored expressions, not live identities/history.
There is no physical CLOG/VACUUM truncation, WAL, xmax/system-column support or
separate PostgreSQL subtransaction-XID allocation. Savepoints preserve the top
identity and restore live contribution provenance without rewriting old bags.
See [transaction ownership](TRANSACTIONS.md#transaction-identity-and-retention)
and [selected proof/smoke](../reference/locking_transactions/README.md#transaction-identity-oracle-first-contract).

## Installed pgrowlocks

The nontrusted, superuser-required, relocatable pgrowlocks1.2 provider installs
only `pgrowlocks(relname text) RETURNS SETOF record`, STRICT/VOLATILE/PARALLEL SAFE.
Its six named outputs are `locked_row tid`, `locker xid`, `multi boolean`,
`xids xid[]`, `modes text[]`, `pids integer[]`. It uses the shared expression/SRF
routes, installed procedure dependencies and current EXECUTE privilege; NULL
returns zero rows/full descriptors without resolving or locking a relation.

The text argument names a relation, including quoted, qualified and actual TEMP
names; numeric text names a numeric-named table, never an OID. Only that relation
receives temporary AccessShare admission: AccessExclusive blocks, row locks do
not, and function completion removes only its own added contribution. Native
queued admission rejects replaced descriptor/storage/lexical identities rather
than retargeting DROP/recreate. PostgreSQL's queued name invalidation can resolve
differently; this is an explicit native identity boundary.

Partitioned parents fail42809 with the partition detail; views, materialized
views, sequences, indexes and foreign tables fail42809 before table ACL checks.
All represented native ordinary storage uses the registered heap2 authority;
custom TABLE definitions do not add table-method selection. The independently
exercised PostgreSQL nonheap0A000 case does not advertise an unsupported native
storage algorithm.
Superuser, table-level SELECT or inherited pg_stat_scan_tables privileges allow
the scan, but do not bypass namespace USAGE/TEMP checks. Column SELECT is not
enough. Ordinary SELECT predicates, RLS and descendant expansion do not filter
the function's direct storage scan.

The scan retains the active statement's immutable MVCC prefixes, RR roots and own
earlier-command overlays, then joins full ObjectRef/RowID/VersionID identities to
current physical lock state. Own modified predecessors are suppressed; another
uncommitted updater's predecessor can appear. A compatible survivor can keep an
RR predecessor reportable after the updater commits. MultiXact locker IDs and
aligned member arrays remain raw: ended members retain their mode and get PID0,
as do prepared owners. Waiters never become fake owner rows. Inherited singleton
headers report For Key Share independently of the original raw mode; actual
prior FOR UPDATE followed by non-key UPDATE reports Update, whereas a fresh
non-key UPDATE reports No Key Update.

`locked_row` is a checked logical native version TID, not a physical PostgreSQL
heap coordinate. Version v maps through n=v-1, block=n/65535, offset=n%65535+1,
only for 1<=v<=4294967295*65535. Zero/overflow gives the ordinary native error
`stuntgres: logical row version exceeds tid range`; no truncation or hashing.
One version stays stable across release/reacquire/rollback; an UPDATE successor
differs even with the same RowID. Restore may remint versions. This adds neither
`table.ctid` access nor portable physical coordinates.

Savepoints retain the inherited native top-level XID/contribution authority:
PostgreSQL child-XID owner-array/PID0 distinctions are not claimed or invented.
Named COMMIT/ROLLBACK behavior uses real top-level transactions. Dump/Restore
preserves provider identities and stored calls, never live locks, XIDs, PIDs,
MultiXact history or materialized results. Native pg_proc keeps its established
nine-column projection; full released argument/retset metadata is PostgreSQL-only
corroboration, while native units validate the actual provider and OUT shape.
See [selected proof and smoke](../reference/locking_transactions/README.md#installed-pgrowlocks).

## Commit timestamp retention

Commit-time inquiries track actual successful assigned-XID transaction
completions only when the Driver was constructed with tracking enabled.
Read-only/empty unassigned transactions, inquiry evaluation, PREPARE and aborts
do not fabricate commits. Prepared completion uses the original public XID;
last completion is shared across sessions and follows completion order, not
the greatest allocated identity. Ordinary local origin is OID 0.
Potential writer admission alone does not assign a public XID: empty DELETE,
UPDATE, SELECT FOR UPDATE and COPY FROM leave last completion unchanged.
Accepted row writes and actual physical-row locks do assign; rolling those
writes back to a savepoint does not erase the top-level assignment before COMMIT.

Native history remains available for the engine lifetime until successful
Reset/Restore retires it. There is no physical VACUUM retention horizon, WAL,
standby recovery or replication-origin administration in this feature.
Full identities advance through low32 wrap, with sparse timestamp reservations
keyed by the actual full assignment. Input XID resolves only its newest candidate
epoch, never an older committed alias when the newest assignment aborted.
See [startup configuration](DRIVER.md#commit-timestamp-startup-configuration)
and [replacement semantics](FIXTURES.md#commit-timestamp-history).

## Server transport

`cmd/stuntgres` exposes the existing engine through PostgreSQL simple and extended
query protocols3.0/3.1/3.2, with release negotiation for newer3.x minors and
version-dependent opaque cancellation keys. Parse, Bind, Describe, Execute,
Close, Flush and Sync use engine-owned prepared names, typed values, portals
and transaction boundaries. Wire names share SQL PREPARE/EXECUTE/DEALLOCATE
authority; portals retain their own snapshot, execution position and lifetime.
Finite Execute resumes without repeating effects. Flush does not commit work;
errors skip ordinary extended messages through the next Sync.
Retained typed rows do not freeze wire presentation: resumed destinations use
current formatting settings and catalog-valued spelling without query replay.
ParameterStatus is emitted at startup and the ready boundary, not each statement
or Flush; intermediate changes coalesce and rollback reports restored values.

Exhausted row/store portals remain fetchable with zero-count completion; completed
non-row commands cannot execute again. A pre-savepoint failed portal retains its
name after rollback-to, not its executor, until Close or actual lifetime retirement.
Simple-query creation also replaces the unnamed portal on CTAS no-data/skip paths.
Pending wire cancellation preserves real COPY conversion errors and consumed-row
context before application; it does not change ordinary embedded Go cancellation.
Native wire input installs its first-record decoding hold before the Start
callback can publish CopyInResponse, with cleanup covering failed publication
and callback resume. This selects a permitted scheduling boundary, not a claim
that PostgreSQL's response universally acknowledges passage through its later
pre-row interrupt checkpoint; reference proof observes PostgreSQL's actual
ClientRead wait. Ordinary embedded cancellation and terminal causes are unchanged.

Wire SELECT preparation uses the existing static-operator planning phase before
starting its real output destination. Planning failures therefore precede row
metadata; runtime failures can follow metadata or already-streamed rows, without
replaying effects. This protocol execution policy does not change ordinary
embedded arithmetic's documented row-owned empty-scan boundary. Undemanded EXISTS
targets and statically unreachable branches remain outside evaluation; actual
predicates and imported-parameter, domain and volatile demand barriers retain
their authority.

Simple SQL EXECUTE is an outer utility: its portal creation precedes underlying
lookup/planning, and SELECT rows use the canonical store-backed utility strategy,
not the direct plain-SELECT streaming strategy. A wire-prepared COPY instead
retains its actual protocol stream through SQL EXECUTE and extended wrappers.
Wire-published EXECUTE wrappers can themselves be invoked through SQL EXECUTE;
canonical invocation preserves each utility's own formals and once-only actual
evaluation through the underlying command. An unused formal does not introduce
a nested utility parameter namespace: its unbound `$1` still raises42P02.

Text/binary parameters and results use shared codecs and actual ordered metadata,
not a second transport conversion registry. Available codecs do not make
lookup-only descriptors, unsupported casts/storage, anonymous-record text input or
missing binary providers available. FunctionCall invokes supported routines by
OID with normal namespace/routine permissions; catalog lookup alone does not
make a routine callable. The selected large-object SQL wrappers are executable.
COPY uses the streaming executor described [below](#copy-streams). Committed notifications are delivered
to idle network listeners without an extra query.

Wire result RowDescription uses the physical domain base type and base typmod;
logical domain identity/checks and ParameterDescription remain intact. Binary
NULL arguments still require a declared receive provider. Ordinary binary rows
initialize send providers on their first delivered tuple, including all-NULL
tuples, but empty rowsets do not initialize them. This is distinct from COPY's
destination stage and FunctionCall's strict NULL-result shortcut.

Only UTF8/UNICODE client encoding and cleartext password authentication are
implemented. TLS, GSS encryption and additional authentication mechanisms remain
unavailable; SSL/GSS requests are refused. Each allowed network account is a
local administrator of its independently owned in-memory databases, subject to
the unchanged userfile cross-database gate. Native SQL dumps remain logical
save/restore, not WAL, replication, crash durability or arbitrary pg_dump import.
Transport support is not a promise of every PostgreSQL SQL/catalog/extension
feature or every client.

See [commands and security](../README.md#phase-2-server--change-note) and
[selected server proof](../reference/sessions/README.md#phase-2-server-selected-proof).

## Session notifications

Native `LISTEN`, `NOTIFY` (including a literal payload), `UNLISTEN channel`, and
`UNLISTEN *` support local notifications between physical sessions in the same
Engine. Receive with the public native
[`WaitForNotification`](DRIVER.md#local-session-notifications) helper, not notice
callbacks. Separate Driver instances and distinct exact DSNs are isolated.

Subscription actions and outgoing events commit atomically with row/catalog
effects; rollback and rolled-back savepoints discard them. Identical channel/
payload pairs fold within a transaction, retaining first occurrence order.
Different transactions retain duplicates and commit order. An active/aborted
recipient withholds new server-pending events until the outer idle boundary;
CHAIN and internal COMMIT→BEGIN messages do not create an intermediate scan.
Filtering uses committed subscriptions at that boundary; a first LISTEN never
backfills history. Already client-ready events survive UNLISTEN and DISCARD ALL.

Names use the shared identifier folding, quoting and UTF8-safe 63-byte clipping
notice. Payloads are at most 7999 UTF8 bytes; 8000 bytes fail with `22023`,
`payload string too long`. Actual driver preparation is effect-free, including
payload validation deferred to execution; SQL PREPARE still only accepts DML.
These utilities are allowed READ ONLY. Pending notification actions/events forbid
PREPARE TRANSACTION; see [transactions](TRANSACTIONS.md#prepared-transactions).

Network sessions consume the same committed per-database queue through actual
NotificationResponse frames containing notifier wire PID, channel and payload.
Delivery is serialized with notices/settings/errors/rows and occurs only outside
an active or aborted listener transaction, including while idle without a query.
Already-ready local events are preserved rather than dequeued through a new
transaction; native `WaitForNotification` retains its separate pinned-connection
receiver contract. Sender retirement does not erase queued PID provenance.
Rule-action execution, `pg_notify`, `pg_listening_channels`, and
`pg_notification_queue_usage` are not introduced by wire delivery.
See [selected proof and executable smoke](../reference/sessions/README.md#session-messages)
and PostgreSQL's [LISTEN](https://www.postgresql.org/docs/18/sql-listen.html),
[NOTIFY](https://www.postgresql.org/docs/18/sql-notify.html), and
[UNLISTEN](https://www.postgresql.org/docs/18/sql-unlisten.html) contracts.

## SQL cursor metadata

Native `pg_cursors` describes supported SQL DECLARE cursors only, not PostgreSQL
wire/SPI portals. Standalone `pg_cursor()` is not implemented. All native entries
have is_binary=false; BINARY cursor syntax is unavailable.
Explicit SCROLL/NO SCROLL reflects the actual supported motion. Omitted SCROLL
remains forward-only natively, whereas PostgreSQL chooses scrollability according
to its query plan. Catalog observation does not change that choice or cursor
position. It is physical-session-local, not a role-global cursor inventory.
Healthy driver ResetSession is a health check, not metadata cleanup; use explicit
DISCARD ALL or physical connection teardown. Native Reset/Restore are catalog
replacement/fencing controls, not session reset.
See [selected cursor contracts and smoke](../reference/cursors/README.md#testing).

## Live lock state

`pg_locks` is a live PUBLIC view of the existing engine's actual ownership,
not an ACL-filtered table inventory. All its public backends are visible to
ordinary roles, including complete resource OIDs, PID, mode and waiting status.
One native Engine represents one logical database; this is not a cross-Driver,
cross-DSN or external PostgreSQL cluster aggregator.

Native rows cover all eight ordinary relation modes, enum/type object locks,
both advisory key spaces with counted session and idempotent transaction ownership, live VXIDs,
genuinely assigned public XIDs, actual completion waits and retained relation
SSI reads. Prepared transactions have NULL PID, retain originating owner text
and assigned-XID/object ownership, and no longer own a live VXID. Committed
SSI evidence can retain original PID/owner text while overlap remains.
The native XID is the canonical transaction assignment, not visibility-order
or diagnostic identity. Savepoints retain it; native savepoints do not allocate
additional PostgreSQL subtransaction XIDs.

Logical row/key grants have no physical page/tuple target and produce no
durable tuple or spectoken rows. Pending logical contention projects one actual
conflicting holder's XID, or its genuine VXID before assignment; if only queue
order blocks grant, it projects an actual incompatible predecessor. Releasing
that target can retarget the observation to a remaining real blocker without
changing the enqueue timestamp or the internal multi-blocker graph. Index
completion waits use a live target's VXID even after assignment; a prepared
target has retired that VXID and is waited on through its actual assigned XID.
Exclusion waits use the assigned XID when present.
Completed internal concurrent-index publication phases retire their XID mirror
at the existing phase-reset boundary, preserving the live VXID and exact ticket
association for the next genuine assignment. Final/ordinary/prepared completion
retains manager ownership until actual manager release, including the brief
publication-to-notification interval.

All native `fastpath` values are false: no native fast-path mechanism exists.
Page, tuple, extend, frozenid, spectoken, applytransaction, physical index-page
predicate locks and worker lock groups have no native authority and are not
fabricated. `pg_lock_status()` itself is not an implemented function.
Ordinary manager capture is owned and side-effect-free; separately captured
SSI evidence is not a globally atomic cross-manager snapshot. No row ordering
is promised. Live PID overflow is an ordinary range error, not wrapping or
silent omission; private maintenance owners are not public backends.

Held statements and stored view recipes capture fresh state at execution.
Reset/Restore fence old ordinary and transaction-advisory ownership, waits and SSI evidence; real
session-advisory holds survive and use current database identity. Dump never
serializes live ownership or waits. Native admitted context cancellation remains
ordinary `context.Canceled`; actual PostgreSQL server cancellation retains57014.
See [selected contracts and smoke](../reference/sessions/README.md#live-lock-state).

## Storage and native controls

Databases and prepared transactions are process-local. There is no WAL, crash
recovery, physical backup, replication, subscription execution or physical
checkpoint service. Native sequence `log_cnt` is always zero; cache state does
not promise crash durability. Native controls are logical fixture replacement,
not arbitrary `pg_dump` import or PostgreSQL physical backup.

### Transactional large-object bytes

Seven SQL signatures are supported: `lo_create(oid)`, `lo_creat(int4)`,
`lo_from_bytea(oid,bytea)`, `lo_get(oid)`, `lo_get(oid,int8,int4)`,
`lo_put(oid,int8,bytea)` and `lo_unlink(oid)`. They are strict, VOLATILE and
parallel unsafe. NULL does not create, authorize, mutate or delete an object.
`lo_creat` ignores its non-NULL mode. Put returns VOID; unlink returns INT4 one.

Bytes are sparse2048-byte pages with signed64-bit offsets. Missing portions
below EOF read as zero; overwrite preserves suffixes. Empty writes validate
offset/authority without extending EOF. Maximum seek is4398046509056; writes
must fit within that bound. Materialized reads over1073741819 bytes fail54000
without allocating the result. Bounded reads beyond4GiB do not allocate holes.

Content and metadata share class2995 identities and ordinary transaction,
statement-error and savepoint rollback/publication. Peer sessions cannot read
private writes. Get uses its active query's READ view: direct nested
`lo_get(lo_from_bytea(...))` can fail42704 and rolls back the creation; a
following statement in the creating transaction sees the object. A non-inline
VOLATILE SQL function introduces a fresh body-query snapshot and can see its
argument's creation/update. Returning or failing restores the outer query's
snapshot; body visibility does not leak into subsequent outer direct reads.
Numeric equality with a relation/TEMP OID is legal and remains so after Restore.

Only object SELECT/UPDATE ACLs authorize byte reads/writes; put needs both.
`pg_catalog.pg_largeobject_metadata` exposes exactly `oid OID`, `lomowner OID`
and `lomacl ACLITEM[]` for real objects. PUBLIC can read every row and its complete
raw ACL even without object-byte privileges; there is no per-object metadata
filter or ACL redaction. NULL, explicit-empty and ordered populated ACLs remain
distinct; `pg_get_acl` returns the same raw state. Held metadata queries observe
create, grant/revoke, unlink and same-OID recreation at each execution. Unlink
requires ownership, not merely UPDATE.

`pg_largeobject` projects genuine stored sparse pages as
`(loid oid,pageno int4,data bytea)`, all nonnullable. Empty objects and
missing pages have no rows; short pages and binary bytes are preserved exactly.
The relation has no PUBLIC SELECT: table/column SELECT GRANT/REVOKE is supported
only for this protected catalog, independently of LO SELECT/UPDATE/ownership.
There is no per-object row filter after ordinary table admission. Held queries
recheck current grants, while returned rows and byte buffers stay owned.
Page scans use their active query frontier, including fresh VOLATILE body
captures, without exposing same-command effects to an outer scan. Native
Dump/Reset/Restore reconstructs pages through existing LO replay; arbitrary
catalog-grant persistence is not promised. Durability, descriptor client APIs,
import/export, truncate and standalone ALTER OWNER are not added.


Qualified TEMP namespace/REGCLASS lookup follows normal captured role authority;
it is not permission to read or mutate another physical session's stored TEMP data.
Native stored-source/target admission rejects that access at execution with
`0A000 cannot access temporary tables of other sessions`, without a position.
This includes recursive stored-view sources, DML, table COPY and explicit TRUNCATE.
Metadata-only analysis/Prepare and identity lookup remain valid; ordinary schema
denials still precede storage admission and retired missing children remain42P01.
View owner/invoker source ACL denials also precede physical admission. Table COPY
checks column ACLs and direction-specific read-only25006 before this boundary,
without consuming input or producing output for any denied admission.
PostgreSQL's small foreign-TEMP scan can independently return an empty local-buffer
cursor; native neither fabricates that cursor nor exposes the owner's actual rows.

Schema/TEMP identity inquiries do not widen that storage boundary.
`pg_is_other_temp_schema` classifies actual physical ownership of both TEMP and
TEMP TOAST descriptors, including retained descriptors after the owner exits;
it is not a live-backend test. `pg_my_temp_schema` never initializes TEMP itself.
Schema inquiries resolve pending explicit `pg_temp` only on demanded non-NULL
evaluation, with first-use namespace publication/rollback owned by the existing
statement and transaction machinery. Driver preparation and strict NULL do not
create a namespace. Persistent view recipes replay these typed functions rather
than capturing a source session's OID; TEMP state remains excluded from Dump.


`CREATE TABLESPACE name [OWNER role] LOCATION 'directory' [WITH (...)]` supports
syntax and PostgreSQL 18 ordered validation, not physical creation. Direct
effective superuser authority precedes OWNER lookup, then canonical POSIX path
quote/absolute/970-byte checks, reserved lowercase `pg_` names and existing-name
validation. OWNER identity keywords retain current/session identity semantics.
Every otherwise admissible native location, even an existing directory, returns
0A000 `CREATE TABLESPACE physical locations are unsupported in embedded mode`.
No filesystem probes, directories, symlinks, user tablespace rows or OIDs are
created. WITH is structurally recognized, but option-value validation/application
is unavailable. SQL cannot reach a native nonreserved duplicate because native
creation never succeeds; internal validation and genuine PostgreSQL creation
cover 42710. Builtin names fail reserved-prefix validation before duplicate.
Reset/Restore retain builtin identity metadata; Dump never emits physical creation.
Ordinary `CREATE TABLE (...) TABLESPACE ...` is outside the existing native
declaration grammar and rejects 42601 at TABLESPACE. CREATE TABLE AS and CREATE
INDEX retain their existing explicit 0A000 placement guards. Object CREATE grants
on builtin tablespaces do not change any of these physical-provider boundaries.

Named ALTER TABLE tablespace assignment admits the represented unchanged
pg_default placement only, after normal live owner/kind checks and ACCESS
EXCLUSIVE admission. It preserves reltablespace 0, summary NULL, descriptor and
storage identities, held result metadata and byte-identical canonical Dump.
It works on represented ordinary/partitioned tables, indexes and materialized
views without demanding storage for a partitioned parent; it never moves children
or table indexes. Supported mixed actions use existing atomic staged ALTER;
mixed partition hierarchy alterations retain their existing 0A000 boundary.
Nonunique partitioned-index creation remains unsupported, not a new ALTER prerequisite.
Action-kind and original-column TYPE preparation follow written action order,
including destination lookup/CREATE before duplicate-action detection and the
database-default CREATE exemption. Nonshared pg_global assignment fails 22023
at movement execution, not destination lookup. Companion preparation errors
precede it; without a rewrite, old-row CHECK/NOT NULL failures also precede it.
With a rewrite, movement is checked before evaluating rewritten rows. All
failures discard the staged statement.

Bulk ALTER resolves typed OWNED BY identities, source and destination in order,
rejects either pg_global endpoint with 22023, then checks destination authority.
pg_default to itself returns without a notice, ownership selection or relation
locks, including NOWAIT. It is not evidence of native nonempty owner filtering,
distinct-space empty notices, bulk NOWAIT lock failures or cancellation during
relocation: no legal distinct nondefault pair exists natively. Genuine PostgreSQL
reference witnesses cover those physical paths. No file relocation, extra
tablespace descriptor or replay history is fabricated; CREATE and Restore
boundaries above remain unchanged.

Dump excludes temporary objects, session settings, prepared names, cursors,
role creation, passwords and membership. Arbitrary explicit authorization ACLs
and invalid indexes cannot be serialized and are rejected rather than silently discarded.
Restore has a restricted parameter-free replay boundary, not general script
execution. It does not import temporary objects, transaction controls, shared
role effects or arbitrary CTE/RETURNING history. See [fixture contracts](FIXTURES.md).

## Routine sequence usage

`information_schema.routine_sequence_usage` has nine ordered nullable
`information_schema.sql_identifier` domain columns over NAME:
`specific_catalog`, `specific_schema`, `specific_name`, `routine_catalog`,
`routine_schema`, `routine_name`, `sequence_catalog`, `sequence_schema`,
`sequence_name`. The PostgreSQL 18 manual's `schema_catalog` spelling is a typo;
the released SQL and pinned 18.6 runtime use `sequence_catalog`. No alias is
provided. All three catalog fields identify the current database.

Rows represent distinct direct stored routine/sequence identity pairs.
SQL-standard RETURN/ATOMIC functions and procedures retain genuine hard edges.
Whole-sequence references and valid sequence-column references contribute the
same containing sequence identity; multiple columns or mixed direct calls/column
reads produce only one pair. Quoted bodies do not gain edges even after
invocation. Ordinary tables and their columns, callees, text resembling a sequence
name, NULLs and transitive sequence use do not produce sequence rows.
No dependencies yields zero rows with full metadata,
never a NULL placeholder. Specific names preserve the full decimal OID suffix,
clipping the routine-name prefix on a UTF8 boundary to fit 63 bytes.

Only immediately enabled sequence-owner roles control visibility: self,
superuser and inheritable owner membership qualify. A sequence ACL grant alone
does not; SET-only membership qualifies only after SET ROLE. Routine ownership,
routine EXECUTE, schema USAGE and search-path visibility are not additional
filters. Each execution uses the paired immutable catalog/role capture, including
held prepared reads. Existing rename/move/owner/drop and native logical replay
determine current dependency identities and names; retained native rows stay
unchanged. No separate dependency registry or recursive expansion is introduced.

Parameter-default declarations, OR REPLACE and ALTER routine lifecycle remain
unsupported as described under [scalar SQL routines](#scalar-sql-routines).
The constant-folded `nextval(NULL::regclass)` standard-body capture boundary is
unchanged; a typed formal invoked with NULL exercises NULL sequence input without
requiring that unrelated binder extension.
See [selected proof and actual API smoke](../reference/catalogs/README.md#routine-sequence-usage).

## Information-schema user mappings

`information_schema.user_mappings` has exactly `authorization_identifier`,
`foreign_server_catalog`, and `foreign_server_name`, all genuine nullable
`information_schema.sql_identifier` domains over NAME. Valid mapping rows have
three non-NULL identity values; no mappings or no accessible servers yield zero
rows with the same metadata. PUBLIC is spelled uppercase. Server names follow
supported `ALTER SERVER ... RENAME` and transactional changes; role names resolve
the current captured role descriptor, not the mapping's diagnostic name.
Native `ALTER ROLE ... RENAME` SQL remains unsupported (`0A000`, `statement is
unsupported`, Position 1); PostgreSQL performs that rename.

Inherited server-owner authority OR effective server USAGE (direct, inherited or
PUBLIC) exposes every mapping on that server, not just the caller's mapping.
Mapping ownership and wrapper USAGE are not independent read gates. SET-only
membership counts only after the effective role changes. Held prepared execution
recaptures authority and rows; no permissions or row cache is introduced.

There are no option columns: secret-bearing mapping options do not affect the
projection. Existing `pg_catalog.pg_user_mappings` option redaction is separate
and unchanged; `information_schema.user_mapping_options` is outside this feature.
Reads use actual SQL/MED metadata without invoking a provider or promising remote
connectivity. Native SQL/MED graph persistence is described below.
The lifecycle tests also pin the existing notice boundary: server CASCADE retires
actual mappings on both backends, but native emits no CASCADE notices on this
SQL/MED path. PostgreSQL emits the complete dependency NOTICE (a single PUBLIC
mapping in these scenarios). This read-view feature does not change notice emission.
See [selected proof and smoke](../reference/catalogs/README.md#information-schema-user-mappings).

## Information-schema key column usage

`information_schema.key_column_usage` reads actual primary, unique and foreign
keys on ordinary and partitioned tables through the normal metadata/query path.
Its exact columns are `constraint_catalog`, `constraint_schema`,
`constraint_name`, `table_catalog`, `table_schema`, `table_name`, `column_name`,
`ordinal_position`, and `position_in_unique_constraint`. The first seven are
nullable `sql_identifier` domains over NAME; the last two are nullable
`cardinal_number` domains over INT4. Catalog names use `current_database()`.
Valid rows populate the first eight fields; p/u unique positions are NULL.
Foreign-key unique positions follow the actual referenced supporting index,
including reversed references and standalone unique-index providers.

Constrained-key order is independent of physical attribute numbers. Duplicate
constraint names on different tables remain separate occurrences. Owners and
inherited owners see their keys; otherwise effective SELECT, INSERT, UPDATE or
REFERENCES on each constrained column qualifies that member. Partial keys keep
their original ordinals. DELETE/TRUNCATE/TRIGGER/MAINTAIN alone does not qualify.
Schema USAGE, search path, RLS and referenced-table privileges add no visibility
gate. Invisible objects return zero rows, not permission errors. Own TEMP is
visible; peer TEMP is excluded. Held statements recapture current authority.

Existing constraint DDL, transactions and native logical controls update this
projection. CHECK, NOT NULL, exclusion constraints, INCLUDE payloads, standalone
indexes and unconstrained data do not independently create rows. No referenced
table/column or unique-constraint-name columns are added; adjacent information
schema constraint views and future definition families remain separate work.
Dropping a supporting unique index CASCADE removes its actual dependent FK rows
while preserving unrelated same-name constraints and child data. The existing
native index-CASCADE path does not emit PostgreSQL's dependency NOTICE, even
with client_min_messages=notice; this view does not expand notice delivery.
See [selected checks and smoke](../reference/catalogs/README.md#information-schema-key-column-usage).

## Information-schema character sets

`information_schema.character_sets` has exactly eight ordered, nullable
`information_schema.sql_identifier` domain columns over NAME:
`character_set_catalog`, `character_set_schema`, `character_set_name`,
`character_repertoire`, `form_of_use`, `default_collate_catalog`,
`default_collate_schema`, `default_collate_name`. Public `database/sql` type
names are NAME, not TEXT or the logical domain name.

The single native UTF8/C database contributes
`(NULL,NULL,'UTF8','UCS','UTF8',current_database(),'pg_catalog','C')` initially.
The attachment's actual database name is retained, including an empty NAME.
NULL predicates, false predicates and bound NULL equality produce ordinary
singleton or empty results with unchanged eight-column metadata.
This view does not enumerate every supported codec or available collation.

Each execution selects actual collations whose COLLATE and CTYPE both equal
the current database's C locale. Candidate names rank by Unicode character
length descending, then C NAME order ascending. Neither encoding/provider nor
ownership, schema USAGE or search path adds a candidate filter. Thus a longer
matching user alias, even in a private schema, replaces C. POSIX and
locale-only builtin descriptors do not match C/C. Identical names across
namespaces have no promised tie winner. Create, rename, schema move and drop
are reflected by fresh captures through the same held statement.
Native collation utilities remain unsupported inside an explicit transaction:
CREATE returns `0A000` (`statement is unsupported in an explicit transaction`),
the same held read then reports aborted-transaction `25P02`, and ROLLBACK
restores the unchanged row without creating an alias. The PostgreSQL lifecycle
phase independently checks actual transactional alias creation and rollback.

PUBLIC SELECT has no role-dependent row predicate; a normal reader sees the
same identity while existing protected catalogs retain their own permissions.
No new database/encoding configuration or collation population is enabled.
Following released PostgreSQL SQL rather than the manual's no-match sentence,
an absent matching collation leaves only default schema/name NULL; the database
catalog name remains populated. The ordinary seeded C configuration always
has a match; the no-match boundary is covered with an immutable package fixture.
See [selected proof and smoke](../reference/catalogs/README.md#information-schema-character-sets).

## Information-schema constraint column usage

`information_schema.constraint_column_usage` has exactly seven ordered nullable
`information_schema.sql_identifier` domain columns over NAME: `table_catalog`,
`table_schema`, `table_name`, `column_name`, `constraint_catalog`,
`constraint_schema`, `constraint_name`. Both catalogs are `current_database()`.
Real relationships have seven non-NULL identities; absent or invisible objects
produce zero rows, never NULL placeholders. There are no ordinal, referenced-key
position, child-table identity or unique-constraint-ID columns.

Actual admitted CHECK dependencies, NOT NULL constraints and primary/unique
members describe constrained columns. FKs instead describe referenced target
columns, with the child constraint's schema/name. CHECK reference repetition
collapses; separate key constraints preserve multiplicity, even for identical
seven-field FK tuples from different children. Dropped attributes, constant
CHECKs, physical-only NOT NULL proofs, domain constraints, standalone indexes,
INCLUDE payloads, exclusions and unconstrained fields supply no independent rows.
Only ordinary and partitioned projected tables participate.

Visibility is enabled projected-table owner-role inheritance: owner, inherited
owner and superuser qualify. SET-only membership does not until SET ROLE changes
the effective principal. Table/column SELECT, INSERT, UPDATE, REFERENCES and
PUBLIC ACLs are not substitutes; schema USAGE, search path, RLS and FK child
ownership add no gate. Unlike `key_column_usage`, qualifying peer-TEMP metadata
is included. The view itself is PUBLIC-readable; invisible metadata is a
successful empty result, not a table permission error.

Held statements recapture current catalog and role authority. Existing
rename/move/drop/add, transaction and Dump/Reset/Restore owners update rows;
retained native Rows keep their original complete bag. This view does not expand
CHECK admission: native CREATE/ALTER CHECK expressions referring to `tableoid`
still fail with42703, whereas PostgreSQL exposes that system-column dependency.
No analyzer/parser or adjacent information-schema view is added. Existing native
index-CASCADE notice delivery remains unchanged.
See [selected checks and smoke](../reference/catalogs/README.md#information-schema-constraint-column-usage).

## Foreign metadata and scans

SQL/MED wrapper/server/user-mapping/foreign-table metadata, ordered column/table
options, ownership/ACL/dependencies and IMPORT use the transactional catalog.
SELECT requires an actual startup-registered Go provider with all seven scan/
planning callbacks. Optional IMPORT support returns real typed definitions;
missing handler/import support fails explicitly. No stored handler name, URL or
default transport fabricates rows. Registration is not PostgreSQL C ABI loading
or a native postgres_fdw network client; providers own their source semantics.

Wrapper handler/validator/options ALTER requires nonempty alteration content and
superuser authority. RENAME and OWNER are separate forms; they cannot be combined
with function-option changes. Ordinary RENAME retains inherited-owner authority.
Wrapper/server GRANT and REVOKE accept USAGE/ALL, distinguish column-qualified
requests from invalid object privileges, and preserve PostgreSQL's object-specific
English diagnostics and unknown-privilege category.

Local quals/rechecks, typed projection, NULL/EOF, true correlated ReScan and
owned scratch conversion use the existing eager pipeline. Public Rows.Next/Close
does no provider work. Logical SQL cursors retain live scans across finite eager
FETCH/MOVE batches; HOLD drains/Ends before promotion. Cleanup runs unlocked,
exactly once, with primary errors preserved and End-only terminal failures
reported before otherwise successful publication. Reset/Restore preserve the
existing deferred stale-cursor owner: replacement itself does not sweep other
sessions; a subsequent request/rollback/CLOSE/session end releases its scan.
Already finalized batches remain readable.

Registered foreign INSERT/UPDATE/DELETE/RETURNING, COPY FROM and grouped TRUNCATE
use independent optional callbacks and genuine provider transactions. Missing
operation support fails explicitly before source demand/mutation. Transactional
rollback capability is mandatory; provider savepoint support gates SQL savepoints.
Full callback rows determine affected counts and RETURNING; opaque original
identity selects UPDATE/DELETE targets independently of NEW keys. Both batch
callbacks and positive size are required; RETURNING and view CHECK OPTION use
singles. View checks validate the actual returned INSERT/UPDATE tuple before
RETURNING, not proposed NEW; violations roll back actual remote effects. COPY accepts
terminal input before remote mutation and uses its distinct insertion lifecycle.
Foreign CHECK/NOT NULL are not locally enforced; STORED generation is computed
locally and VIRTUAL fields are sent NULL. Remote constraints remain real provider
errors. Scans share enlisted transactions, not independently committed connections.
Remote-first ordered commit and final local fences do not provide distributed
atomicity; later failure can leave partial/unknown remote completion, never a
successful command or compensating rollback claim.
Foreign MERGE/ON CONFLICT, row locks, index/UNIQUE/PK/FK/trigger callbacks,
hierarchy expansion and arbitrary remote pushdown remain unsupported. No generic
has_server_privilege/has_foreign_data_wrapper_privilege SQL execution is added
merely because static PostgreSQL identities exist. Actual GRANT/REVOKE and common
authorization/stored-ACL inquiries use the new descriptor classes. Native Dump
preserves the supported SQL/MED graph, ordered options and owners, including
sensitive mapping bytes; it never copies remote rows or executable code. Restore
requires target registrations and roles, permits authentic validators but no
IMPORT/scan/modify callbacks, and atomically reconstructs through ordinary DDL.
Supported captured-reference/REGCLASS foreign defaults replay after every
provider/name phase through qualified, single-column SET DEFAULT repairs, with
post-bind eligibility validation and an authority reset. Self/mutual references
and later-created providers do not require physical foreign storage.
Unsupported explicit ACL state fails before output; arbitrary foreign ALTER/DROP,
IMPORT and writes remain excluded from Restore. COMMENT ON FOREIGN TABLE and
foreign-column comments preserve actual metadata; COMMENT ON TABLE retains its
ordinary wrong-kind diagnostic. See
[embedding](DRIVER.md#foreign-provider-registration) and
[catalog contracts](TABLES.md#foreign-metadata-and-dependencies).

## Object privilege grants

Direct GRANT/REVOKE supports sequence SELECT/UPDATE/USAGE, schema USAGE/CREATE,
type/domain USAGE, genuine routine EXECUTE, database CTc, parameter sA and builtin
tablespace CREATE. NULL/default ACLs are not interchangeable with explicit empty
or explicit default-equivalent state. ALL IN SCHEMA selects current objects of
its named class; it is neither a future-object default nor capability promotion.
Type GRANT materializes raw explicit ACL provenance even when its effective
USAGE payload matches the synthetic creation baseline; subsequent revocation or
regrant never silently converts that explicit state back to raw NULL.
Function expansion includes actual aggregates and windows, while procedures
remain their distinct class. Foreign-data wrapper/server grants use the genuine
USAGE/ALL paths described above. Wrapper/server defaults retain owner USAGE when
first materialized; owner
self-GRANT makes raw NULL explicit even when effective rights are unchanged.
Registered foreign handlers and validators are genuine FUNCTION/ROUTINE grant
and schema-expansion targets, with strict, VOLATILE, parallel-unsafe metadata.
Their callback tokens do not provide standalone SQL execution. Internal FDW
dispatch remains independent of ordinary EXECUTE ACLs. Generic
has_function_privilege SQL execution remains unavailable; raw ACL inquiries and
the existing authorization hooks expose actual stored authority.
Valid LANGUAGE targets retain `0A000 LANGUAGE privileges are unsupported in
embedded mode`. LARGE OBJECT numeric targets support SELECT/UPDATE and ordinary
grant-option/revoke/cascade rules. Invalid masks, malformed syntax and actual
role/grantor diagnostics remain separate.

Ordinary REVOKE removes selected privileges and their grant options only from the
selected grantor's origin; GRANT OPTION FOR preserves ordinary use. Default and
explicit RESTRICT reject dependent grants with `2BP01 dependent privileges exist`
and Hint `Use CASCADE to revoke them too.`, without publishing a prefix. CASCADE
removes downstream grants when their grant-option support disappears, not when
an independent ordinary grant still permits the recipient's use. Surviving
PUBLIC, inherited and independent origins remain additive; ownership retains
implicit grant authority even after an owner's ordinary privilege is revoked.

The held schema reader in the PostgreSQL18.6 revoke scenario also denies access
after its final USAGE origin is removed. Type/domain USAGE removal denies new
persistent dependencies, not existing datum reads or enum literal casts. Database
CONNECT removal denies fresh admission without evicting admitted sessions.
ALL IN SCHEMA revokes current matching objects only, preserving outside-schema
objects and later explicitly granted objects. Native procedures remain a genuinely
empty expansion; no procedure runtime or native `pg_proc.proacl` column is added.

Partial named sequence SELECT,UPDATE revocation emits WARNING01006 first for the
relation and then for columns tableoid, cmax, xmax, cmin, xmin, ctid, last_value,
log_cnt and is_called. ALL suppresses the relation's partial warning but retains
those nine column warnings. An already-effective owner revoke is not itself a
reason to emit a warning.

Valid plain, option-only and CASCADE language revocations retain the same native
unavailable error above. ALTER SYSTEM still has no native file-write
mechanism. Physical tablespace placement remains unavailable natively; pg_default
default placement is not a CREATE-privilege denial witness, and pg_global retains
its separate shared-relations restriction. Supported SQL/MED USAGE revocation
denies new server/foreign-table/user-mapping creation where applicable, without
inventing a remote provider or a new USAGE gate on existing foreign scans.

Native prepared analysis retains identities, not executable permission decisions.
Genuine selected providers check fresh EXECUTE before actual folding or surviving
execution admission, including stored consumers. Known released lookup-only
routine signatures reject `0A000 routine privileges are unsupported for
unimplemented routines in embedded mode`; genuine conversion/codec ownership
still supports metadata ACLs without a direct-call provider. Basic scalar SQL
routine creation is supported separately; security-definer frames, implicit
arbitrary codec permission gates and missing providers are not introduced.

Existing SQL invoker wrappers retain their finite released body associations,
including real function-method casts, not only quote TEXT delegation. This adds
no SQL interpreter, standalone cast/escape provider, or arbitrary C-helper gate.
Description bodies initialize on first actual non-NULL entry; sleep's exercised
empty-source case initializes eagerly. OVERLAPS outer denial precedes partial
constant addition overflow. Strict NULL/dead arms retain actual pruning.

Fresh native execution checks current authority even for held statements.
Independently observed PostgreSQL old inlined quote plans and warm constant index
keys can retain earlier results, while warm constant index predicates deny after
revocation. Native callable index planning uses original recipes on first real
tuple demand: constant keys plan before predicate truth, nonconstant keys remain
predicate-dependent, and zero-source INSERT does not initialize index recipes.
Consumed original calls survive folding for graph-only post-wait admission,
without replay or PostgreSQL executable-cache emulation.
The same distinction applies to independently observed warmed PostgreSQL
partition-key relation-cache programs during populated DEFAULT validation:
an inlined SQL wrapper can disappear there while its child still needs EXECUTE.
Native retains fresh original-wrapper and child authority instead of emulating
that executable normalization cache.
Pinned PostgreSQL18.6 discovery also distinguishes already-warmed all-constant,
non-NULL calls to `abbrev(inet)`, `abbrev(cidr)`, `host(inet)`, `text(inet)`,
`family(inet)`, `masklen(inet)`, `broadcast(inet)` and
`inet_same_family(inet,inet)`: the same PostgreSQL statement retains its folded
result after EXECUTE revocation, although direct and newly prepared calls deny.
Native deliberately checks current EXECUTE on that held route rather than
caching an executable result or permission. This bounded existing architectural
difference is independently asserted in the selected formatting test contract;
it does not exempt row-dependent, runtime-NULL, empty-source, view or cast routes
from their actual routine-demand checks.
This fresh-statement rule is distinct from an already initialized SQL cursor:
ordinary FETCH and HOLD materialization retain DECLARE's routine authority.
New declarations still check current EXECUTE; original identity/revision,
source and publication fences remain active.

The one-input `current_setting` adapter supports fresh bound-NULL strict
elimination after import; `set_config` remains nonstrict. Explicit cast nodes in
these special setting-call argument lists retain `0A000 unsupported setting
function signature`, as does the unavailable two-input `current_setting`.

BIT &,|,#,~,<<,>> use the six existing genuine bit-string providers and released
BIT/INTEGER signatures; VARBIT inputs do not create invented VARBIT routines.

Existing array random/unnest, JSON/JSONB extraction and set-returning providers,
and existing window rankings retain their released full polymorphic, variadic or
zero-argument signatures. JSONB insertion uses its one four-formal declaration
with the released false default, not a separate three-input overload.
Expanded JSON path elements form one non-NULL TEXT[] formal even when an element
is NULL; an explicit VARIADIC NULL array is instead a strict NULL argument.
This distinction does not add multirange casts, missing overloads or providers.
Installed Metaphone and Levenshtein calls likewise retain their genuine extension
member descriptors for central current-EXECUTE admission, stored consumers and
catalog remapping; this does not install absent fuzzystrmatch routines.

The genuine address/BIT access, numeric-series, JSON-record, intagg and bounded
pgcrypto members also retain current selected EXECUTE authority. Addressed objects
and reused C algorithms add no extra SQL-call permission edge. Numeric short
series keeps its own two-IN identity; populate record grants use their released
three-IN `(anyelement,json,boolean)` identity with the false default, without
adding explicit third-argument native calls. Strict SRFs and RECORD initialization
are not scalar NULL exemptions. A proven-empty FROM can remove actual invocation
demand without removing original dependency/schema/lifetime/lock traversal.
An independently imported NULL may discard a sibling SQL domain coercion, but
actual typed DOMAIN input validation and per-argument barriers still apply.

Database and foreign-data wrapper/server grants retain their existing
explicit-transaction exceptions. Other object grants reject
`0A000 statement is unsupported in an explicit transaction`.
Effect-free Prepare still succeeds for valid utility syntax;
execution read-only 25006 precedes target lookup. Native Dump refuses
unrepresentable explicit/altered ACLs before writer callbacks; Restore does not
replay GRANT/REVOKE or silently discard their authority.


## Authentication and authorization

Native connector role admission is a trusted host assertion, not password/SCRAM
authentication. Stored passwords and expiry timestamps do not affect that route.
Physical session limits and login eligibility are enforced independently of
pooled borrowers; see [driver admission](DRIVER.md#trusted-role-admission-and-physical-limits).

Ordinary SELECT/INSERT/UPDATE/DELETE enforce supported bound policy expressions,
including nested queries, with role applicability and true-only permissive/
restrictive command groups. MERGE/ON CONFLICT action expressions retain an
explicit capability boundary. Operating-system file/program access, PostgreSQL PID signaling and
reserved connection slots are unavailable. Predefined role identities do not
create these missing capabilities. Table/column grants do not imply sequence or
other-object grants. Sequence grants support SELECT/UPDATE/USAGE,
ALL and grant options, including implicit/TABLE and ALL SEQUENCES IN SCHEMA targets.
Schema grants support USAGE/CREATE; type/domain grants support USAGE for new
persistent dependencies, including builtins. Existing values and casts remain usable.

All six `pg_has_role` NAME/OID signatures and both
`row_security_active(text|oid)` signatures are executable strict STABLE BOOL
inquiries, including direct/prepared expressions and stored-view recipes.
The former separates MEMBER, inherited USAGE, SET and ADMIN; raw role OIDs are
SQL inquiry inputs, not trusted principal handles. The latter is metadata-only:
it does not enforce policies or require table SELECT. The native `row_security`
setting is separate: on applies ordinary row-policy runtime; off raises
42501 for RLS-applicable queries, never bypasses policies, and leaves the inquiry
unchanged. Relation-name schema access remains separate. The existing pg_settings
provider exposes all actual registered definitions, exact base-unit values and
captured reset provenance. It remains native read-only (55000); PostgreSQL's UPDATE
rewrite rule is not implemented. Missing statistics collectors remain unsupported,
not inert settings or zero counters. See [settings](SETTINGS.md#row-security-and-pg_settings)
and [authorization](AUTHORIZATION.md#membership-provenance-and-publication).

`pg_get_acl(classid oid, objid oid, objsubid integer) -> aclitem[]` is a strict
STABLE inquiry into **stored** catalog ACLs, not effective permissions, inherited
rights or synthesized defaults. Direct/newly prepared/held calls share the typed
expression path; held calls borrow current execution-time catalog and authority.
No SELECT privilege on the queried object is required. Name casts such as
REGCLASS still use their normal schema/name resolution.

Supported existing descriptors expose relation, namespace, type, procedure,
database, foreign-wrapper/server and large-object ACLs. Recognized classes without
native instances produce missing-object NULLs; no language ACL is fabricated.
Recognized ACL-less classes return NULL before object lookup. Unknown class IDs,
including pg_attribute1249 and pg_parameter_acl6243, raise XX000 rather than
being confused with missing objects. `(0,0,subid)` returns NULL.

Column addresses use pg_class1259, not pg_attribute. Nonzero relation subids
enter the column path and narrow to signed int16 AttrNumber before lookup:
65537/-65535 wrap to attribute1, while 65536 does not become whole-object lookup.
Missing/dropped/system-without-ACL attributes return NULL. Stable attribute
numbers survive rename and retain drop gaps. Nonrelation subids are ignored.
Table grants are not merged into column ACLs. Stored absence returns NULL;
explicit empty remains a typed `{}`; explicit entries retain grantor, grantee,
rights and grant options.

Type effective USAGE baselines retain creation-time raw-ACL provenance: a
synthetic default is raw NULL, while a real explicit ACL, even baseline-equal,
is not. SQL TYPE/DOMAIN USAGE grants mutate that real ACL authority separately
from this read-only inquiry. Initial database raw ACLs are NULL on both backends;
effective defaults do not become stored ACL entries until a legal ACL edit.

The inquiry creates no authorization state. Stored inquiry recipes and returned
ACLITEM[] **data** use existing native Dump/Restore, including role-name remapping;
raw numeric OIDs are not remapped dependencies. Explicit authorization grants
remain unsupported serialization and reject before output, and Restore-GRANT
remains atomic/rejected. Real explicit type ACLs cannot be silently normalized
back to a synthetic baseline during recipe serialization.

## Transactions and retained handles

Many modifying utilities remain excluded from explicit native transactions;
selected table alterations, temporary-table operations and sequence utilities
are bounded exceptions, not general transactional DDL. Role, policy, domain and
collation utilities retain their explicit-transaction exclusions. Query-created
tables, materialized refresh and ordinary index creation also retain native
explicit-block boundaries; concurrent index creation rejects explicit blocks.
`CREATE TABLESPACE` rejects explicit and implicit multi-statement blocks with
25001; read-only admission returns 25006 first. Driver preparation of valid
utility syntax defers these runtime checks and rechecks effective authority on Exec.

Native catalog replacement and changed name resolution during an admitted lock
wait fence retained owners with `ErrCatalogReplaced`; they do not retry, retarget
or replay.
Catalog-owned prepared parameter/result identities cannot be recovered by
recreating a type with the same name. See [prepared invalidation](DRIVER.md#prepared-catalog-and-path-invalidation).

Native eager rows, early result-set skipping, metadata and context cancellation
are not promises about lib/pq streaming or transport disposal. Adapter differences
remain explicit in [driver contracts](DRIVER.md).

## Statistical aggregates

The six variance/stddev names and twelve bivariate statistics names are ordinary
aggregates; see [query execution](QUERIES.md#blocking-stages-bounds-and-shared-producers).
Integer/NUMERIC unary moments use exact sums and squares, released decimal
division precision and same-scale square-root rounding, not binary64 conversion.
Floating unary and bivariate moments follow REL_18_0 Youngs–Cramer evaluation
order; this does not import newer constant-axis corrections or promise an
unordered floating accumulation order. Finite float overflow raises22003
`value out of range: overflow`; an unrepresentable final NUMERIC raises22003
`value overflows numeric format`. Admitted nonfinite inputs yield NaN moments,
subject to empty/sample-singleton and axis-specific regression rules.
Regression inputs are Y first, X second, one DOUBLE PRECISION signature.
The bivariate set includes `regr_slope`, `regr_sxx`, `regr_sxy`, `regr_syy`,
`corr`, `covar_pop`, `covar_samp`, `regr_avgx`, `regr_avgy`, `regr_count`,
`regr_intercept` and `regr_r2`. Both inputs independently coerce to FLOAT8 and
only complete non-NULL pairs contribute. Floating averages retain both axes'
moment overflow checks. Count instead uses checked exact INT8 arithmetic without
moment evaluation, returning zero for no pairs. Other empty outputs are NULL;
sample covariance requires two pairs, correlation requires nonzero variance on
both axes, and intercept/r² require nonzero X variance. With varying X and
constant Y, intercept is Y and r² is1.
`stddev` and `variance` are sample aliases with their own labels. Metadata is
typmod -1 with zero physical origin; only pair count is known nonnullable.
Named approximate floating witnesses use relative/absolute tolerance
`1e-12*max(1,abs(expected))`, never approximate counts, NULLs or diagnostics.
Native restoration remaps input/result ownership. Statistical grouping remains
serial and statistical windows remain outside the existing native window capability.

## Parallel execution

Parallel work is limited to eager top-level read-only SELECT over one persistent
ordinary physical sequential table source, optional filtering/sorting and
audited row-local scalar projections. Unsupported plans transparently retain
their serial executor. Writes (including SELECT write sources), row locks,
SQL cursors/suspended execution, nested queries, temporary/catalog/sequence/view
sources, inheritance/partition append, index candidates, joins, grouping,
distinct, windows, CTEs, LIMIT/OFFSET, virtual generated reads and unaudited
functions do not dispatch parallel workers. Stored generated datums can be read.
Sequence/session effects and restricted random functions remain serial; volatility
alone is not a PostgreSQL parallel-safety classification.

`max_parallel_workers_per_gather` is a session integer ceiling (default 2,
0..1024), captured at command admission, with ordinary RESET/LOCAL/transaction
semantics. Actual workers are additionally bounded by selected input and native
resources; ceilings 0/1 and fewer than two occurrences stay serial. No optimizer
cost controls, forced plans, global worker pool or PostgreSQL worker processes
are supplied. Gather promises no row order; supported ORDER BY merges sorted
worker runs with the serial typed comparator. Cancellation joins all workers
and discards unexposed output, preserving the primary error.

## Rewrite rules

`CREATE [OR REPLACE] RULE`, `ALTER RULE ... RENAME TO` and `DROP RULE`
support identity-bound INSERT/UPDATE/DELETE query rewrites, including ALSO,
INSTEAD, NOTHING, boolean qualifications, OLD/NEW and action lists. Expansion
precedes execution, uses alphabetical same-event order and detects recursive
relation/event ancestry before effects. It is not procedural row-trigger execution.
Actions use current event-relation-owner authority while the original command
retains caller authorization, parameter/result contracts and atomic publication.
Original RETURNING is the sole published result when present, including its empty
schema; auxiliary SELECT actions still execute and participate in atomic failure.
Without original RETURNING, direct/held SELECT-action transport retains the
separately documented driver behavior. Unqualified UPDATE/DELETE actions without
OLD/NEW demand can execute even when the original command affects zero rows.
Their rule-added sources use the current event owner, including owner changes
observed by the same held statement; only retained original sources keep caller
or explicitly effective source authority.
That authority also survives execution-time recollection through security-invoker
views; the action owner's grants cannot replace required caller input grants.

All demanded original/action/normalized-CTE producers prepare before effects.
Rule-program supplied parameters specialize as operation-private typed values;
ordinary parameter-demand policy and row/domain/volatile runtime barriers remain.
Unused pure derived payloads and dead literal conditional branches are not evaluated.
Conflict execution state is command-private, including every stored action-owned
modifying CTE body; target/EXCLUDED frame layouts and ACL manifests follow stable
attributes rather than saved dense slots. Repeated execution, failure recovery,
concurrent physical sessions and restored future writes do not share conflict
bookkeeping; intra-command cardinality checks remain enforced.
Native rule export retains explicit inference collation and operator-class
restrictions, current attribute names and each modifying CTE's own query
presentation. Restoration does not broaden future unique-index inference.
Provider RESTRICT also checks ordinary DML rules attached to views, independently
of the SELECT definition. CASCADE retires dependent rules while preserving the
event view and unrelated rules. OLD/NEW cast inputs use the existing rule lexical
namespace and canonical type/coercion pipeline, not a separate scalar evaluator.
Named conflict targets capture their constraint object, not a creation-time
spelling or an inferred index dependency. RESTRICT/CASCADE and `pg_depend` use
that normal constraint edge. Export resolves its current canonical name, and
native definition reminting propagates identity through immediate and modifying
CTE actions; renaming or reusing the old spelling never selects another provider.

SELECT rules bridge only an existing ordinary view's unconditional single
INSTEAD SELECT `_RETURN`, preserving its exact output layout; modifying WITH in
that SELECT rule action is rejected. Explicit DML
rules are consulted at each reached view layer before automatic write lowering;
fresh action targets retain their own CHECK OPTION enforcement. Recursion,
ON CONFLICT and RETURNING restrictions use PostgreSQL 18 diagnostics rather
than partial execution. Top action WITH bodies, and an INSERT source SELECT's
own WITH bodies, cannot reference OLD/NEW; nested correlated WITHs are not
blanket-banned.

Native rule DDL is autocommit-only, including prepared execution; this differs
from PostgreSQL transactional DDL. MERGE events/actions, procedural triggers
and rule-action NOTIFY remain unavailable. Persistent rules participate
in dependency cleanup and post-data logical Dump/Restore.
DO UPDATE actions implicitly capture dependencies on every target column present
in their initial EXCLUDED targetlist, independently of actual column ACL reads.
Later-added unreferenced columns can be dropped; initial captured columns require
ordinary RESTRICT/CASCADE handling. DO NOTHING has no such implicit EXCLUDED capture.
See [rule tests](../reference/views/README.md#rewrite-rules) and
[CREATE RULE](https://www.postgresql.org/docs/18/sql-createrule.html).

## Data-modifying CTEs

Nonrecursive modifying WITH uses the installed INSERT/UPDATE/DELETE/MERGE
providers and can feed later CTEs or any installed primary command via typed
RETURNING. Modifiers run once to completion even unused or behind executing
LIMIT 0/false consumers; materialization hints do not authorize folding/replay.
SELECT-only CTE demand remains lazy. All components share one physical statement
snapshot and one atomic publication; only primary output/count is exposed.

Nested modifying WITH and referenced no-RETURNING output are rejected. Ordinary
views reject modifying WITH at execution, including held statements; materialized
views and cursors reject it at held preparation. Existing CTAS/SELECT INTO remain
admitted consumers. CTAS WITH NO DATA creates empty schema without executing the
query or its modifiers. Recursive modifying definitions, native triggers and
hierarchy provider exclusions remain unchanged; admitted commands may read
real foreign sources and modify registered transactional foreign targets.
Rule expansion must leave exactly one
admitted auxiliary query; zero/multiple expansions retain the documented
INSTEAD NOTHING/conditional/ALSO/multi-statement restrictions. A single SELECT
replacement remains lazy when unused. No portable same-row overlap winner or
sibling execution order is promised. See [query ownership](QUERIES.md) and
[PostgreSQL restrictions](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-MODIFYING).


## Tables, hierarchies and materializations

Source-local WHERE filters support conservative metadata-driven RANGE/LIST/HASH
branch omission and physical inheritance CHECK exclusion. Retained rows still
run the original residual; NULL/default ownership, duplicate bags, metadata and
diagnostics remain the SQL contract. Captured admission/locks may cover omitted
branches. Held statements recompute selection from each execution's imported
values and captured topology; native Dump/Restore serializes objects/data, not
pruning state.

No EXPLAIN costs, physical PostgreSQL plan shape, arbitrary Boolean evaluation
order, new planner settings or FDW pushdown are promised. This does not emulate
PostgreSQL disk access, WAL/durability, network transport or native provider ABI.
Unsafe/unsupported proof shapes retain branches instead of rejecting legal SQL.
Existing row-lock capability errors remain. Ordinary source policies execute
before residual user predicates; pruning never substitutes for their evaluation.

RANGE `ALTER TABLE ... ATTACH PARTITION` accepts an existing ordinary or
partitioned relation, with `FOR VALUES` or `DEFAULT`, including explicit
transactions and savepoints. Ordinary `DETACH PARTITION` retains the child as a
standalone table or virtual root, including transactions/savepoints.
CONCURRENTLY/FINALIZE, unrelated partition hierarchy alterations, update/delete,
row locking and explicit hierarchy locking remain excluded. Foreign keys
involving partition hierarchies retain their existing unsupported boundary.
Conflict arbitration and MERGE cannot target partition
hierarchies. Temporary partition definitions and partition definitions nested in
schema creation are unavailable. Nonunique virtual partition indexes and partition
reindexing are unavailable; partition key/constraint combinations remain bounded.

Partition CHECK/NOT NULL inheritance supports recursive ADD CHECK, named ADD
NOT NULL, column SET/DROP NOT NULL and named DROP of these two constraint kinds.
Only whole lists of these selected actions are admitted; mixed column/key/type
or other hierarchy actions remain0A000 with no selected prefix publication.
CHECK identity uses name plus the bound expression's structural identity, not
SQL text or algebraic equivalence. A matching child CHECK becomes nonlocal;
NOT NULL merges by mapped column and preserves an existing local child fact.
Each merge stops only its own action's recursion. Existing SET localizes the
current NOT NULL fact without localizing grandchildren.

ONLY DROP removes the root contribution and localizes the immediate child;
grandchildren keep their immediate parent contribution. Fresh ONLY ADD/SET with
children fails42P16; childless ONLY and existing ONLY SET remain supported.
CREATE child explicit unnamed NOT NULL gets its child-generated name, and an
explicit different name is retained. Direct ALTER ADD with a different existing
NOT NULL name fails55000 rather than silently renaming or reusing it.
Descriptors retain exact local/count provenance and distinct own constraint OIDs;
CHECK/NOT NULL conparentid and backing-index OIDs remain zero. Leaf validation,
current owner admission and cancellation use the single atomic ALTER stage.
Logical native Dump/Restore and unchanged emitted-SQL replay preserve final
facts, nullability, rows and enforcement; no new durability or PostgreSQL native
control API is implied. See
[selected proof](../reference/tables/README.md#inherited-partition-constraints).

RANGE and LIST keys require an admitted btree ordering provider. Native integer-array
keys currently retain the existing missing-default-operator-class boundary;
ordinary array storage does not add a partition ordering provider.

LIST definitions admit exactly one column or immutable expression key, typed
`IN` bounds, a distinct NULL owner, DEFAULT as the live complement and nested
partition trees. Equal repeated entries within one IN list normalize; equal
values or NULL claimed by different siblings fail atomically. Bound expressions
evaluate once at CREATE, including volatile expressions; routing and replay use
their frozen typed values. Parent INSERT routes to actual physical descendants;
direct leaf INSERT checks every ancestor, parent SELECT includes descendant
rows and ONLY parent is empty. An uncovered key raises23514 at the failing level.
No new scalar type, collation or operator-class provider is implied. Public
LIST ATTACH remains0A000 `list partition attachment is unsupported`; native
Dump/Restore may use private, validated LIST attachment to restore captured state.

HASH definitions use PostgreSQL 18 seeded extended hash support, ordered NULL-skipping
key combination and unsigned modulus selection. Positive int32 moduli and valid
remainders admit sparse coverage and mixed divisibility chains; DEFAULT is forbidden.
The existing concrete scalar hash classes are supported, including numeric, JSONB,
enum/domain and catalog-reference keys. Arrays and unowned/custom/external providers
remain excluded; deterministic owned collations retain their normal authority.
Internal CHAR retains its full signed-byte hash provider but not user-column or
general-cast admission: those preexisting SQL paths still raise 0A000. Supporting
a hash class does not expand the underlying scalar storage or expression dialect.
INSERT, direct leaf constraints, parent reads and native logical replay share the
existing hierarchy paths. Public HASH ATTACH is not enabled.
Native Restore remints actual enum/reference identities and reroutes imported rows.
An uncovered reminted residue or incompatible destination leaf-local constraint
rejects replacement atomically, rather than retaining incorrectly placed rows.
Rows retained in the same physical leaf preserve existing-row NOT VALID semantics:
an unrelated unchanged old violation is not retroactively validated. Material
changes to a CHECK's referenced datum, stored literal or owner-sensitive input
still require validation; descriptor-only reownership does not. Rows entering a
different leaf and future INSERTs retain full destination constraint enforcement.

SQL tableoid reads retain the existing native missing-column boundary; physical
identity remains represented by catalog relations and capture provenance.
The private logical importer can replay NOT VALID CHECK or local UNIQUE/PRIMARY
ADD USING INDEX on an exact captured physical member when dependency ordering
requires it. A deferred virtual local UNIQUE/PRIMARY tree instead uses a captured
conventional constraint definition after its intended plain tree is attached.
Exact operation/definition/table/subtree origins fence this private replay;
PRIMARY replay retains already-present NOT NULL facts. Public hierarchy ADD
EXCLUDE and selected constraint DROP use their own bounded action admission;
other hierarchy ADD/alterations remain excluded. Private origins do not authorize user SQL.
A permanent partitioned parent cannot attach an ordinary temporary candidate;
existing temporary namespace/session authority remains the persistence boundary.
A NOT VALID child NOT NULL constraint cannot satisfy a validated parent
requirement, regardless of constraint names or whether the candidate has rows.
ATTACH rejects the mismatch rather than validating or replacing the child fact.
Ordinary inherited UPDATE/DELETE recurse by default, with ONLY exclusion and
physical INSERT. Existing ordinary column/default/NN/CHECK/generated ALTER
actions propagate according to their action; identity, RLS and physical keys
remain nonrecursive. Ordinary `ALTER TABLE ... INHERIT` and `NO INHERIT`
support compatible ordinary-table endpoints, sparse live parent ordinals,
locality/counter maintenance and atomic mixed lists. Ordinary LOCK,
provider-CASCADE graph-shape changes and new partition ALTER families remain
excluded. UPDATE DEFAULT, explicit-transaction SET/DROP DEFAULT, explicit ADD
NOT NULL NO INHERIT and NOT ENFORCED modifiers are not introduced.
Relationship parent observations precede waits, but lookup/ownership errors and
parent grants occur at the corresponding action, preserving mixed-list error
precedence. NO INHERIT on a typed or partitioned table with an existing ordinary
nonparent reports the missing relationship; actual partitions remain rejected.
This boundary correction does not support mixed physical partition ALTER.
Retained TEMP histories survive native replacement and detach under contextual
old-reference authority, not general permission to reuse an old catalog epoch.
Supported ONLY SET NOT NULL preserves its actual local NoInherit fact; PK on
that fact is rejected rather than silently promoting it. Dump emits the exact
named validated NN plus immediate paired genuine catalog SQL before descendants.
Real PostgreSQL replay therefore requires superuser catalog-update authority;
ordinary native catalog UPDATE stays forbidden. Generated replay uses genuine
typed-NULL declarations only inside isolated staging when captured parent recipes
diverge, then real ONLY SET recipes and validated recursive CHECK definitions
before data; no placeholder becomes the delivered catalog.
Execution merge stops are action-specific, not a generic exemption from
preparatory locks or visited-child ownership. Existing valid SET NOT NULL does
not visit children; ONLY cannot complete an invalid inheritable NN above existing
children. Locality, live inheritance and retained historical constraint support
remain separate facts. Historical replay uses narrowly paired archival
pg_constraint SQL, not fake live providers/localization or a public catalog
write capability; PostgreSQL script replay requires superuser authority.
Actual TYPE cleanup may retain a physical not-null flag after its historical
nonlocal NN catalog row disappears. This does not create a replacement NN fact.
Trusted logical replay narrowly removes the actual constructor's NN row and
selected dependency edges while preserving physical enforcement; ordinary
catalog writes and arbitrary constraint-row deletion remain unsupported.
The canonical script requires PostgreSQL superuser catalog-write authority;
native acceptance is a single-use exact fresh-import constructor pairing, not a
public catalog-DML exception. Keyless flags use genuine ONLY SET then no-PK
cleanup; an unrelated PK is constructed afterwards without removing its real NN.
Captured separate index shapes use genuine USING INDEX. Deferred-primary self-FKs
retain exact local/referenced key order and supporting provider, including
permutations and actual competing UNIQUE/index dependencies, through existing
renderers/binders and strictly paired import receipts. This does not add FK/index
capabilities or relax ordinary permissions/validation.
Valid ancestor-to-descendant FKs are emitted after their actual source/target
rows and supporting provider, using genuine validated qualified ONLY ADD FK
under that same trusted superuser script context. Native acceptance is bounded
to exact fresh ordinary inheritance identities and captured key/provider/owner
observations, not general valid ALTER input. The normal pending binder retains
REFERENCES/type/data diagnostics and all-or-nothing Restore publication; no
NOT VALID surrogate, public permission bypass or role/ACL import is introduced.
Copying a temporary-sequence default into a permanent table is unavailable.

Materialized views support logical indexes and autocommit concurrent refresh.
Explicit transaction refresh and refresh as Restore input remain excluded.
Physical temporary heaps, SPI diff SQL/Context, WAL and crash durability remain
server mechanisms, not native logical publication contracts. Trigger, pending
trigger and procedural-language execution are unavailable. Registered foreign
write/truncate callbacks retain their explicit transaction contract above.
TRUNCATE RESTART IDENTITY resets owned sequences transactionally to configured
START, including selected virtual roots and supported inheritance/CASCADE closure.
It does not reset unowned default references, undo ordinary nextval/setval
allocations, clear local currval/lastval on rollback, or enable trigger or
partition-FK providers.

Adding generated columns and changing their destination type without an explicit
conversion remain excluded. Direct typed-table creation cannot add identity or
generated options. Primary-key creation retains deferrability and temporal-key
exclusions; ordinary key-list attachment through table alteration is not general
primary-key creation. Mixed default-setting/removal action lists remain excluded.

Read-only `pg_catalog.pg_constraint` discovery uses PostgreSQL 18's complete
28-column shape for actual supported table constraint owners (n/c/u/p/f/x) and
own domain CHECKs. Metadata includes real relation/domain/namespace/index
identities, validity, declaration deferral, provenance and ordered
key/operator/action vectors; inapplicable vectors/tree values are SQL NULL.
Domain rows have conrelid=0, actual contypid, own CHECK OIDs and connoinherit=false;
derived domains do not receive copies of ancestor constraints. Metadata reads
do not filter by table SELECT privilege, and TEMP catalog discovery is distinct
from session-scoped unqualified relation resolution. Constraint triggers,
temporal and NOT ENFORCED definitions are not fabricated by this projection.
CHECK `conbin` is honest native structural PG_NODE_TREE data rather than
PostgreSQL nodeToString bytes; compare deparsed semantics, not internal bytes.
Catalog mutation and row locking remain unsupported through existing native
readonly-catalog boundaries.

Read-only `pg_catalog.pg_inherits` has exactly `inhrelid OID`, `inhparent OID`,
`inhseqno INT4` and `inhdetachpending BOOL`, all nonnullable, with no row `oid`.
It projects direct ordinary-table inheritance, table partition membership and
real partition-index ancestry from the current catalog generation. Ordinary
parent order starts at one; detach retains surviving ordinals and attach uses
one above the greatest live ordinal. Partition and index edges use one. Index edges
remain visible when their parent index is incomplete/invalid. No endpoint SELECT
filter, transitive closure, index-to-indexed-table link or reverse duplicate is
added. LIKE copies, typed-table membership alone, views and local/standalone
indexes do not create rows.

DDL owners already govern transaction-private visibility, savepoint rollback,
ATTACH/DETACH, DROP and native logical replay. Fresh/held execution observes the
current generation; captured results survive later changes. Qualified catalog
lookup remains available through name shadowing, and numeric TEMP discovery is
distinct from another session's unqualified name lookup. `inhdetachpending` is
false for all implemented edges: this does not enable concurrent DETACH/FINALIZE.
Native catalog mutation and row locking retain `0A000` boundaries; PostgreSQL
ordinary-role mutation instead uses its real catalog permission checks.



## COPY streams

Embedded callers use the [pinned native stream helpers](DRIVER.md#pinned-copy-text-streams--change-note),
not ordinary embedded database/sql execution or prepared COPY. Network clients
can use simple or extended COPY FROM STDIN and table/query COPY TO STDOUT,
including lib/pq CopyIn and psql. Text, CSV and binary share real codecs,
constraints and atomic staging; packet boundaries are not row boundaries.
Text end markers still wait for protocol CopyDone, and CopyFail can veto success;
binary trailers reject trailing data. Late conversion failure, cancellation or
disconnect does not publish a valid input prefix. Output can have an irreversible
prefix on failure, but receives no successful completion for failed work.
Plain SELECT query COPY initializes its real plan/metadata/authority before CopyOutResponse,
then demands rows through the canonical producer. Binary output resolves its
actual send provider after that response but before binary signature/data,
including empty results: missing providers can therefore produce H then E
without any data or successful completion. No provider is invented by transport.
Binary input initializes declared receivers for every live physical attribute,
including omitted COPY columns, before CopyInResponse. A domain's generic
receiver is available at that stage: an empty stream can finish without invoking
its base receiver; a NULL record resolves the base on invocation, after the
response, and preserves actual COPY row/column error context.

File and PROGRAM endpoints fail explicitly and never perform external I/O.
Encoding overrides and freezing remain excluded. COPY FROM WHERE uses existing
expression providers over the whole destination row, including omitted defaults;
only TRUE inserts. Conversion and demanded defaults precede the filter, while
generation, routing and table constraints follow it. Text/CSV ON_ERROR IGNORE
skips supported fresh-input conversion errors, not framing, encoding, default,
predicate, table-constraint, I/O or cancellation errors. Domain-own constraints
may be soft; errors executing their CHECK expressions remain hard. REJECT_LIMIT
permits exactly N rejected rows and fails atomically at N+1. LOG_VERBOSITY and
client_min_messages govern row/summary notices. Binary supports WHERE/STOP,
not IGNORE; no new expression or binary provider is implied.
Array text input keeps individual bound/rank checks early, but a cumulative
declared dimension product does not shield demanded element conversions.
Child input precedes closing-extent validation, so a hard domain CHECK exception
still aborts IGNORE atomically even for a short value declaring large dimensions.
Only actual parsed elements drive storage growth; final checked construction
retains the existing size and shape guards.
Recoverability follows the actual provider's error-save boundary, not broad
SQLSTATE classes: array dimensions, range reversal, JSONPath grammar/regex,
BIT width, JSONB Unicode NUL, FTS, installed query_int and ACL input-owned errors
may recover. LINE arithmetic and JSONPath numeric/recursive helpers remain hard,
as do regtype parser/typmod and predicate/default failures. Native domain CHECK
evaluation does not currently capture
catalog authority for a nested regclass cast (`regclass input lacks catalog authority`).
This ordinary unsupported path is not softened or implemented by COPY recovery;
primitive domain CHECK expressions and the scoped hard-origin boundary remain
independently covered.
Native explicit text-to-int4range casts in WHERE or domain CHECK binding remain
unsupported (`0A000 explicit casts are supported only for int4, int8, text and bool`).
Direct range COPY input/recovery is fully supported independently of that cast;
no cast provider is added to implement conversion-error policy.
Legacy CSV option spellings are outside the modern CSV implementation.
Physical export of a virtual partition root is unavailable.
Binary providers do not add storage, cast, DDL or catalog mutation capabilities;
`pg_node_tree` binary input is excluded.

## pg_namespace namespace metadata

`pg_catalog.pg_namespace` is a readonly projection of actual namespace
identities, names, owner OIDs and nullable typed ACLITEM arrays. Inaccessible
schemas remain visible in inventory; metadata visibility is not schema USAGE.
Initialized builtin identities/ACLs and supported schema DDL/default-privilege
effects are exact. Real TEMP table/toast metadata is retained after physical
close while objects retire. The toast descriptor does not enable TOAST storage
or own-table authority. Direct catalog mutation and virtual-relation row locks
remain unsupported. Schema GRANT/REVOKE uses the owning immutable ACL authority;
general ACL serialization remains unavailable.
Registered rowtype ownership is exposed through the bounded `pg_type` source.
Native role rename SQL remains excluded; namespace ownership/ACL merge is supported
through its existing producer. TEMP absence observations use real catalog
relationships, not execution of lookup-only namespace/relation inquiry functions.

## pg_database current attachment metadata

`pg_catalog.pg_database` exposes all eighteen PostgreSQL 18 columns in released
order through the ordinary readonly catalog query path. Native inventory is
exactly the current attached database, not a cluster or enumeration of a Driver's
other engines. `datname` equals `current_database()` and `current_catalog`: the
established UTF8-validated NAME display, clipped to a complete prefix of at most
63 bytes. The complete opaque DSN remains the attachment key; spaces, case and
URL-looking strings are not parsed, and colliding NAME displays do not merge keys.

`oid` is the existing engine-local database identity16383; `datdba` and `datacl`
reflect the captured owner and actual stored ACL, including transactional CTc
grants/options and owner replacement. Initial datacl is NULL, with owner CTc and
PUBLIC Tc defaults. Explicit empty ACLs do not regain those defaults. CONNECT
controls fresh authenticated physical admission, not retroactive disconnection;
TEMP controls current-role namespace allocation/CREATE, including the actual
schema diagnostic after an allocated namespace loses CREATE. No database creation
or physical cluster storage is added. Metadata visibility remains independent of
database CREATE rights and uses ordinary catalog precedence/shadowing.

`encoding=6`, `datlocprovider='c'`, `datcollate='C'` and `datctype='C'` declare
bounded native UTF8/C semantics, not host libc/ICU infrastructure. Native
`dathasloginevt=false` reflects absence of installed login event triggers.
The following fields exist with their documented concrete SQL types and typed
NULL values, rather than invented cluster/disk facts:

- `datistemplate BOOL`, `datallowconn BOOL`, `datconnlimit INT4`: no database-owned
  template, mutable admission or connection-limit metadata.
- `datminmxid XID`: no physical multixact horizon.
- `dattablespace OID`: no physical database placement.
- `datlocale TEXT`, `daticurules TEXT`, `datcollversion TEXT`: inapplicable to
  native C semantics, not fabricated ICU locales/rules or host version strings.

`datfrozenxid XID` instead exposes the actual logical database freezing bound.
It retains its nullable column contract; it is not a physical cluster horizon.

These NULL fields, `datfrozenxid` and `datacl` are nullable in native result
metadata; the other known facts are nonnullable. NULL comparisons retain ordinary SQL three-valued
semantics. The relation's shared pg_global metadata does not imply a database
placement. No template rows, backing indexes or TOAST relations are fabricated.
Already returned native Rows retain their captured owner/ACL and types; fresh
executions, including held statements, capture current authority. Reset/Restore
preserve destination attachment identity/owner/ACL, and Dump does not serialize
the source database or its catalog row.

## pg_class logical relation metadata

`pg_catalog.pg_class` has the PostgreSQL 18 34-column order and documented types.
The first 31 fields are known nonnullable natively; `relacl`, `reloptions` and
`relpartbound` are nullable. Native adapter type names include OID, NAME, internal
CHAR, INT2, INT4, FLOAT4, BOOL, XID, _ACLITEM, _TEXT and PG_NODE_TREE.
Exact addresses, namespace, owner, kind, row types, method identities, attribute
high-water, CHECK counts, stored ACLs/options, RLS and population belong to the
existing relation features. No absent foreign-table or TOAST row is invented.
Discovery is database-wide, not restricted to search_path or table privileges.

The native physical fields are explicit boundaries, not approximated storage:

- `relfilenode`, `relpages`, `relallvisible`, `relallfrozen`, `reltoastrelid`,
  `relrewrite` and `relminmxid` are typed zero sentinels: native relation files,
  page/maps, TOAST, transient physical rewrites and multixact do not exist.
  Zero filenode does not claim PostgreSQL mapped storage. Heap/MV
  `relfrozenxid` instead carries genuine logical freezing bounds.
- `reltuples` is -1 (unknown) until an ordinary heap/MV VACUUM scan supplies
  its logical live-row estimate; other nonsequence kinds remain unknown.
  Sequence1 is fixed logical singleton metadata with zero native pages.
  Neither unknown estimates nor physical pages are synthesized from count(*).
  Enabled ANALYZE remains an explicit native capability boundary.
- `relam` exposes logical heap=2, btree=403, hash=405 and bounded GiST=783
  compatibility identities, not PostgreSQL physical heap/index storage. Default
  partition roots and sequence/view/composite relations have method zero.
- The exact represented shared catalog identities 1213/1262/2396 expose shared=true
  and pg_global=1664 metadata. Data is still database-local; this does not enable
  physical user tablespaces. User table/MV/partition replica-identity defaults
  likewise do not implement replication.
- `relhastriggers` is false: native FK execution is logical, not PostgreSQL's
  internal trigger machinery. `relhasrules` describes installed user rules and
  view `_RETURN` definitions; query rewriting does not introduce a trigger catalog.

`relhasindex` and `relhassubclass` are lazy publication histories; ordinary
index/child removal preserves them. VACUUM may clear `relhasindex` only when
no attached indexes remain. All nineteen represented
table catalogs initialize exact released indexed-table facts on their descriptors,
independently of the bounded table-summary inventory. Represented views retain
false. These facts survive Reset/Restore; they do not fabricate physical
backing-index rows or expand the table-summary scope.

Stored ACL projection keeps original identities, masks, grant options, order,
PUBLIC and NULL versus explicit empty arrays; it is not an effective-grant summary.
Native catalog-source GRANT/REVOKE SQL remains unsupported even though owning
internal ACL publication and current-permission checks exist. PostgreSQL SQL
privilege witnesses and native actual provider ACL transitions are independently
covered.

`reloptions` represents only installed supported view options, including explicit
false and validated lexical strings. SET retains unmatched options before
appending touched keys in statement order; RESET removes keys; replacement replaces
the full installed list. No table/index fillfactor or other physical option is
invented. Dump emits only installed options. Existing arbitrary explicit relation
ACL Dump rejection remains unchanged; reading relacl does not widen that serializer.

Table-member `relpartbound` is a genuine PG_NODE_TREE structural carrier of
immutable typed RANGE/LIST/HASH facts. Native RANGE output is
`{PartitionBound strategy range default true}` or
`{PartitionBound strategy range default false lower (...) upper (...)}`.
Finite datums are `{type OID typmod N collation OID value "..."}` with captured
key Usage and quoted type-owned output; sentinels are MINVALUE/MAXVALUE.
This is intentionally not PostgreSQL nodeToString or SQL FOR VALUES text.
HASH bounds use `{PartitionBound strategy hash default false modulus N remainder R}`.
LIST bounds use `{PartitionBound strategy list default true}` or
`{PartitionBound strategy list default false values (...)}`. Every value carries
the sole key's Usage; SQL NULL is an unquoted `value NULL`, distinct from text
`value "NULL"`. For INT4 IN(1,NULL), exact native output is
`{PartitionBound strategy list default false values ({type 23 typmod -1 collation 0 value "1"} {type 23 typmod -1 collation 0 value NULL})}`.
Index partitions have NULL bounds. Additional hierarchy alterations, including
member RENAME, remain unavailable; native retained-bound witnesses use admitted
DROP/Restore while PostgreSQL also exercises real RENAME.

## intagg installed wrappers

The relocatable, superuser-required, nontrusted intagg1.1 provider installs only
`int_array_aggregate(integer) RETURNS integer[]` and
`int_array_enum(integer[]) RETURNS SETOF integer`, the documented
[PostgreSQL18 compatibility wrappers](https://www.postgresql.org/docs/18/intagg.html).
Upstream internal `int_agg_state`/`int_agg_final_array` are not callable native
members. They have distinct extension-local lookup-only identities owned by the
actual installed aggregate, so `pg_aggregate` definitions, REGPROC/REGPROCEDURE
values and symbolic persistence do not substitute builtin kernel OIDs. These
aliases are not additional `pg_proc` rows or callable extension members. Removing
the owner retires them; native replacement remaps their data/default/view
references with the owner. There is no second array type or arbitrary loader.

Fixed INT4/INT4[] signatures use ordinary installed namespace resolution and
shared implicit coercion, including unknown NULL/parameters and SMALLINT-to-INT4.
They do not inherit the polymorphic builtin unknown-input ambiguity or accept
arbitrary BIGINT/TEXT/numeric narrowing. Missing/off-path/incorrectly qualified
calls fail normal function resolution, not a builtin fallback.

The aggregate preserves duplicates and NULL inputs; zero input or a filter
excluding every row yields one SQL NULL global result. Explicit aggregate
ORDER BY controls element order; without it only element multiplicities are
specified. DISTINCT, FILTER, grouping and INSERT SELECT reuse ordinary aggregates.
INSERT VALUES rejects a resolved aggregate with42803; argument, signature and
FILTER-child diagnostics retain their ordinary precedence before placement.
The enumerator supports targets, scalar wrappers and FROM/lateral/ordinality:
NULL elements are present rows; whole NULL/empty inputs emit zero rows with INT4
metadata. All dimensions flatten stored elements, regardless of lower bounds.
Explicit unary VARIADIC array spelling does not introduce a variadic overload.

Stored views preserve both wrapper names and genuine procedure dependencies.
Dump/Reset/Restore and independent-driver replay remap installation identities;
held statements reanalyze while already returned rows retain their original data.
Exact same-session TEMP installations/views retain their captured procedure
dependencies across Reset and compatible baseline Restore. This contextual TEMP
authority does not make an ordinary stale or foreign ObjectRef valid, and TEMP
installations remain nonportable dump sources.
Valid aggregate OVER signatures reach the existing native window capability
boundary: unfiltered calls report `window function provider is unsupported`,
filtered calls `window aggregate FILTER is unsupported`. PostgreSQL supports
those windows; native does not implement another array/window runtime.

## Daitch–Mokotoff installed function

The fuzzystrmatch provider has a real1.1 base with nine selected overloads:
four INT4 Levenshtein signatures, three TEXT Metaphone signatures and two
Soundex signatures. Its real1.1→1.2 update creates
`daitch_mokotoff(text) RETURNS text[]`, yielding ten selected overloads,
not the complete upstream extension. CREATE defaults to1.2 and executes the
same base/update path; VERSION1.1 deliberately has no DM function.
ALTER EXTENSION UPDATE [TO version] runs through the same private validated
catalog stage as CREATE, preserving extension/old member identities and ACLs.
Owner and actual database CREATE authority apply; trusted elevation owns only
the new member. Version and members publish atomically or not at all.
Dump/Restore represents the installed version and genuine selected manifest.

Availability distinguishes default, installable and installed versions.
`pg_extension_update_paths(name)` reports directed shortest script-count paths
as TEXT source/target/path records, including NULL for unreachable pairs and
zero rows for strict NULL input. Fuzzy graph-only1.0→1.1 records a shipped
parallel-safety script, not an executable action: no base reaches1.0, so it is
not installable/importable. Other native providers have explicit current-only
base inventories and no update edges. This is not an OS file inventory.
No arbitrary script execution, library loading, historical adjacent-provider
transitions, ALTER EXTENSION SET SCHEMA/ADD/DROP member commands or undocumented
text_soundex alias is implied. Installed-extension DROP has its own dependency
and removal contract.
The excluded ALTER EXTENSION SET SCHEMA and ADD/DROP member routes retain
the native parser boundary `0A000`, `statement is unsupported`, positioned at
ALTER; supporting UPDATE does not enable relocation or membership changes.
Installation is required; missing/off-path calls use normal function resolution.
The immutable, strict, parallel-safe function emits all alternative six-digit
codes in release order, with significant array element order and no truncation.
NULL, empty, punctuation/digit-only and unsupported-only strings yield SQL NULL,
not an empty array. Vowel-only A yields `{000000}`; long input is not limited to
255 characters.

The release scanner's Latin1 table and special Ą/Ę/Ţ/Ț rules are deliberate
compatibility limits. There is no general transliteration, Unicode normalization
or Cyrillic mapping. Unsupported codepoints and punctuation skip within phonemes;
ß is one S. Installed procedure identity, namespace/search-path authority,
transactional installation and native ownership/remapping use the existing
extension registry. Generated columns may consume this genuinely immutable
function; a volatile child is still volatile. No dictionary/helper TEMP exclusion
is inherited from unaccent. Availability's stock comment does not promise
the undocumented `text_soundex` alias or arbitrary loaders.

### Soundex

Installed `soundex(text) RETURNS text` and `difference(text,text) RETURNS int4`
are immutable, strict and parallel safe. Ann/Anne both yield A500 and score4;
Anne/Andrew score2. Agreement counts matching positions, not spelling equality
or edit distance. The C-locale byte kernel preserves original-byte adjacency,
including H/W separators and raw-digit collisions (BHB B100, B1B B000).
Empty/no-ASCII-letter inputs yield empty TEXT, with difference4 between two such
inputs; there is no255-byte limit. Multibyte encodings, including UTF8, work
poorly as documented: Émile M400 differs from Emile E540. No Unicode approximation
is used. See [PostgreSQL F.16.1](https://www.postgresql.org/docs/18/fuzzystrmatch.html#FUZZYSTRMATCH-SOUNDEX).

### Metaphone and Double Metaphone

Installed `metaphone(text,integer)`, `dmetaphone(text)` and
`dmetaphone_alt(text)` return TEXT with immutable/strict/parallel-safe properties.
Metaphone caps output independently from the input: nonempty source must fit255
bytes, requested length must be1–255, input length errors precede cap errors.
Empty source bypasses cap validation; NULL suppresses only the kernel after
demanded argument evaluation. Nonletters may produce empty (not NULL) output.
Double Metaphone has no255-byte input limit; primary and alternate codes are
independently truncated to4 characters without padding or alternate fallback.
Both algorithms preserve PostgreSQL18.0 bytewise C-locale behavior, including
punctuation-sensitive lookaround and limited multibyte behavior, not Unicode
normalization/transliteration. Named formals, extra overloads and SRFs are absent.

### Levenshtein edit distance

`levenshtein(text,text)` and `levenshtein(text,text,int,int,int)` compute
edit distance; the optional weight triple is insertion/deletion/substitution.
`levenshtein_less_equal(text,text,int)` and
`levenshtein_less_equal(text,text,int,int,int,int)` take a final maximum bound.
All four return INT4 and are immutable, strict and parallel-safe. Default costs
are one. For ordinary nonnegative costs, bounded results are exact within the
bound and greater than it outside; no particular sentinel is guaranteed.
Negative bounds disable pruning.

Exact Unicode codepoints are compared, without case folding, normalization or
grapheme segmentation. Two nonempty operands exceeding255 characters fail22023
`levenshtein argument exceeds maximum length of 255 characters`. Empty source
or target returns the corresponding weighted insertion/deletion length before
limit or bound checks, so an empty/256-character pair is accepted.

Release compatibility follows the pinned PostgreSQL 18.6 arithmetic: signed
costs, including negative values, are accepted, intermediates are int64 and do
not wrap, and a final distance outside INT4 fails 22003 `levenshtein distance
out of range` (18.0 wrapped it). Bounded pruning remains release-specific with
pathological costs; these are not recommended mathematical distance metrics. Strict NULL retains INT4 metadata, while
required sibling errors still execute. Genuine installed identities and all
arguments survive stored rendering, generated/default/CHECK/view consumers and
native Dump/Reset/Restore; no always-on builtin aliases are installed.


## Installed pgcrypto hashing, bcrypt passwords, raw AES/BF and random data

Registered pgcrypto1.4 installs fourteen scalar overloads: digest(TEXT,TEXT),
digest(BYTEA,TEXT), hmac(TEXT,TEXT,TEXT), hmac(BYTEA,BYTEA,TEXT),
crypt(TEXT,TEXT), gen_salt(TEXT), gen_salt(TEXT,INT4),
encrypt/decrypt(BYTEA,BYTEA,TEXT), encrypt_iv/decrypt_iv(BYTEA,BYTEA,BYTEA,TEXT),
gen_random_bytes(INT4), gen_random_uuid() and fips_mode().
The selected members are PARALLEL SAFE and STRICT except the zero-argument UUID
wrapper, which is non-STRICT. Digest/HMAC results
are raw BYTEA for md5, sha1, sha224, sha256, sha384 and sha512; TEXT input is
exact UTF8 octets and BYTEA input is arbitrary binary. Unknown data/key operands
prefer TEXT; known mixed TEXT/BYTEA HMAC has no overload. Salt count uses ordinary
implicit INT4 coercion, not BIGINT/NUMERIC narrowing. Password results are TEXT.
Namespace lookup and schema USAGE are
actual installed authority, not always-on builtins. Observed SHA hyphen aliases,
SHA2-256 and ASCII case aliases identify only these six algorithms.

Strict NULL returns typed BYTEA or TEXT NULL after required child evaluation/constant
simplification; it does not mask division-by-zero siblings. Digest/HMAC algorithm names
fold ASCII case and truncate at63 bytes on a UTF8 boundary without trimming or
notices; unsupported names raise22023. HMAC streams keys larger than the hash
block before passing a bounded effective key to the standard-library provider.
Installed immutable consumers and native controls preserve procedure signatures,
owners and executable expressions. Availability includes pgcrypto and the selected
uuid-ossp provider beside unaccent, two-wrapper intagg, pgrowlocks1.2,
selected-type intarray and selected-function fuzzystrmatch.

Password hashing implements DES, xdes, MD5-crypt, bcrypt and SHA256/SHA512-crypt
with per-call state.
DES consumes eight password bytes with high bits discarded; xdes folds the full
byte string. MD5-crypt takes up to eight salt bytes, stopping at `$`; empty and
nonalphabet salts are accepted. Stored checksum/suffix bytes are ignored.
Only exact `$2a$`/`$2x$` heads select bcrypt, costs04..31 and the first72 password
bytes; short keys cycle through terminating NUL. 2x retains signed-octet expansion.
Noncanonical final bcrypt salt padding normalizes away. `$2b$`/`$2y$` and other
unrecognized settings use DES fallback, not additional bcrypt variants.
DES settings require two bytes, xdes nine; invalid alphabet digits decode zero.
Malformed recognized settings raise22023 `invalid salt`; xdes zero count and
`$2$` raise39000 `crypt(3) returned NULL`. Nonzero even xdes crypt counts work.
Exact SHA `$5$`/`$6$` prefixes hash all password UTF8 octets using standard-library
SHA256/SHA512. Salt scanning consumes at most16 raw bytes, stops at a dollar
after nonempty decoded salt, and preserves PostgreSQL's distinct raw prefix for
hashing leading-dollar salts. Embedded `$5$`/`$6$` or `rounds=` anywhere in the
salt suffix fails before ordinary character validation, even beyond16 bytes.
Omitted rounds selects5000 and omits the output option; explicit rounds prints
the normalized count. Parsing follows pinned64-bit C signed64 saturation then
int32 narrowing, not SQL INT4 input; signs/leading whitespace are accepted.
Direct crypt clamps below1000 or above999999999 with exact22003 NOTICE before
salt validation. Malformed rounds reports42601; bad salt characters report22023
with the complete offending UTF8 character; reserved suffixes reportXX000.
gen_salt names are ASCII-case-insensitive without trimming. Omitted/zero counts
select des25, xdes725, md5 1000, bf6 and sha256crypt/sha512crypt5000.
Explicit counts accept des25, md5 1000, xdes odd1..16777215, bf4..31 or SHA
1000..999999999; invalid counts/names raise22023.
Strong entropy uses des2/xdes3/md5 6/bf16/SHA16 bytes, independent of SQL random.
Salt lengths are2/9/11/29 for DES/xdes/MD5/bcrypt. SHA always prints rounds and
16 salt characters (31 at default5000,36 at999999999), with no trailing dollar;
MD5 generation also has no trailing `$`.
NULL/validation precedes entropy; cancellation remains a context error and is
polled while hashing/folding without an artificial lower cost ceiling.
crypt is immutable; gen_salt is intrinsically volatile, including after stored
expression reconstruction. Defaults/views may generate fresh salts; generated
columns/expression indexes may not. Native controls preserve stored salt/hash
data, exact fourteen-member identities and executable recipes.

gen_random_bytes returns raw BYTEA of1..1024 bytes from strong entropy, independent
of SQL random()/setseed(). Bounds are checked before allocation or entropy;
invalid INT4 counts report39000 `Length not in range` with no invented detail or
position. NULL count returns typed BYTEA NULL without entropy. Unknown input and
SMALLINT use ordinary INT4 coercion; BIGINT/NUMERIC/TEXT/BOOL do not silently narrow.
Entropy failure reportsXX000 `could not generate a random number`, separately
from range errors; cancellation remains an ordinary context error.

The installed gen_random_uuid() is the documented core V4 alias semantically,
returns UUID with V4 version and RFC variant bits, and preserves its actual
installed procedure identity/dependency. Core pg_catalog.gen_random_uuid() remains
available without installation. Implicit pg_catalog wins on an ordinary path;
an explicit installed namespace before pg_catalog selects the wrapper. Qualified
calls select the named namespace, and stored installed recipes render that
qualification rather than rewriting it to the core function.

fips_mode() returns nonnull BOOL from the actual Go crypto/fips140.Enabled() mode.
It does not parse environment settings at query time, claim certification or
report OpenSSL state; no OpenSSL installation is required. PostgreSQL's own
OpenSSL mode can legitimately differ. Process-startup GODEBUG=fips140=off/on
witnesses are documented in the catalog reference Testing section.
These three functions remain VOLATILE after reconstruction: defaults/views may
use them, generated columns/expression indexes may not. Native controls preserve
stored random values without regeneration, installed dependency addresses and
fresh executable recipes, including independent-driver procedure relocation.
Full OpenSSL inventory, other pgcrypto functions, provider configuration and
arbitrary shared-library loading remain excluded.
Pgcrypto members must carry only their genuine provider identity; simultaneous
crypto/fuzzy markers are rejected by both manifest and descriptor admission.

Raw encryption implements AES CBC/ECB and Blowfish CBC/ECB/CFB64, with default
CBC. Observed rijndael spellings denote AES; bf/blowfish spellings select BF,
including explicit -cbc/-ecb/-cfb aliases. Raw slots infer BYTEA, not TEXT, and
keys/IVs contain uninterpreted octets. AES keys choose16/24/32 bytes, zero-padding
short keys and clipping beyond32; AES IVs choose the first16 bytes/right-zero-fill.
BF keys cycle unsigned octets, including NUL, without a terminating byte, and
clip beyond56 bytes; an empty key equals one zero byte. BF IVs use the first8
bytes/right-zero-fill. Omitted IVs are zero. ECB ignores a nonnull IV, but a NULL
IV remains strict; demanded siblings still evaluate before NULL suppression.

CBC/ECB PKCS padding adds1..block-size bytes, including a complete block for
aligned/empty plaintext. Nopad preserves suffix bytes and requires alignment.
Padded decryption validates every byte of the actual padding run, not preceding
data. Invalid lengths/padding raise39000 with the actual function's English
primary and no partial result. CFB64 accepts arbitrary lengths, including empty
and partial tails; valid default/pkcs/none padding choices are no-ops.

Cipher type strings use ASCII folding and UTF8-safe63-byte clipping without trim
or notices. All options parse before cipher lookup, empty slash segments are
accepted and the final pad value wins. Malformed/unknown options and unknown
ciphers retain22023. Recognized AES-CFB, DES/3DES and CAST5 raw cipher inventory
remains native0A000, not a false PostgreSQL unknown-algorithm claim.
BF reuses bcrypt's actual fixed Blowfish primitive/constants, with distinct raw
key packing and reverse-round block decryption; password semantics are unchanged.
AES/BF loops keep fixed per-call scratch and one owned output, poll context at
most64KiB apart and retain no schedule/key/IV/mode in stored expression plans.
Raw AES/BF supplies no authentication: wrong keys/IVs and corrupted CFB ciphertext
are not guaranteed to fail. Test-only oracle legacy-provider configuration is
not a native provider option or an external production crypto dependency.
See [PostgreSQL raw encryption](https://www.postgresql.org/docs/18/pgcrypto.html#PGCRYPTO-RAW-ENC-FUNCS).

## Installed uuid-ossp version 4

The uuid-ossp1.1 provider installs exactly ten native members returning UUID:
`uuid_generate_v4()`, `uuid_generate_v1()`, `uuid_generate_v1mc()`, `uuid_nil()`,
`uuid_ns_dns()`, `uuid_ns_url()`, `uuid_ns_oid()`, `uuid_ns_x500()`, and
`uuid_generate_v3(namespace uuid,name text)` /
`uuid_generate_v5(namespace uuid,name text)`. Use quoted `CREATE EXTENSION "uuid-ossp"`
with an optional installation schema. Installation uses the existing trusted
extension/database-CREATE authority: the installer owns the extension, while
superuser-required members installed by a nonsuperuser belong to the bootstrap
administrator. Function resolution enforces actual schema visibility/search_path;
this is not an always-visible builtin alias.

The strict, parallel-safe, VOLATILE zero-argument overload uses standard-library
`crypto/rand` entropy, preserving122 random bits with version4 and RFC variant
bits set. Each demanded result is non-NULL UUID, canonical lowercase text through
database/sql and16 raw bytes through existing binary COPY and wire codecs.
Computed metadata does not acquire a special nonnull proof, and the extension
adds no separate embedded binary-bind API.
There is no NULL input to a zero-argument overload: `uuid_generate_v4(NULL)`
fails ordinary overload resolution rather than returning NULL.

Closed defaults and views retain fresh demand-time generation and actual
qualified installed recipes through Dump/Reset/Restore; stored UUID data is
preserved exactly. Generated columns/expression indexes reject the volatility.
The five constants are IMMUTABLE STRICT PARALLEL SAFE, with no entropy, clock or
generation-state demand. Their values are respectively
`00000000-0000-0000-0000-000000000000`,
`6ba7b810-9dad-11d1-80b4-00c04fd430c8`,
`6ba7b811-9dad-11d1-80b4-00c04fd430c8`,
`6ba7b812-9dad-11d1-80b4-00c04fd430c8`, and
`6ba7b814-9dad-11d1-80b4-00c04fd430c8`. Nil UUID is an ordinary non-NULL UUID.
They support generated columns and expression indexes while preserving genuine
installed identity, current EXECUTE checks and symbolic qualified control recipes.
The original eight overloads have zero arguments; passing NULL to them is a
missing overload. The two name-based overloads are IMMUTABLE STRICT PARALLEL
SAFE and propagate SQL NULL from either operand. UUID namespaces may be arbitrary,
including nil. MD5(v3) / SHA1(v5) hash16 network-order namespace bytes then the
verbatim UTF8 name; v5 discards the final four digest bytes, and both set UUID
version/RFC variant bits after hashing. No trimming, case folding, Unicode
normalization, DNS syntax checking or appended NUL is performed. Empty text is
valid. Named/reordered arguments are supported; explicit text is not implicitly
UUID and integer is not implicitly text. No extra overload is supplied.
Immutable DEFAULT/generated/index/view/group consumers retain both expression
children and genuine installed identity/EXECUTE authority through controls.
Volatile children remain volatile and are rejected by immutable consumers.
Name generation does not access entropy, clocks, V1/session generation state or
external libraries. Arbitrary shared-library loading remains excluded.

V1 and v1mc are VOLATILE STRICT PARALLEL SAFE time UUID generators. Their shared
physical-session allocator is nontransactional and survives rollback/Reset/Restore,
but is never serialized. V1 uses a cryptographically seeded stable random fallback
node with multicast/local bits; v1mc uses a fresh random node with both bits per
demand. Neither acquires a real host MAC or calls libuuid/OSSP/uuidd/clock files.
The Gregorian timestamp uses100ns units and ten slots per observed microsecond.
Exhaustion waits cancellation-aware outside the private allocator lock, without
synthetic future drift. Backward observed time increments the14-bit sequence;
wrap stages a fresh stable fallback node. Entropy failure/cancellation commits
no state. Across random node epochs, sessions, Drivers/processes and restarts,
uniqueness is probabilistic, not durable or OS-synchronized.

## citext extension type

The registered citext1.8 provider supplies scalar columns, literal and inferred
bound string/byte input, typed NULL, and genuine dynamic identity. Original
spelling, whitespace, empty strings, Unicode and decomposition remain unchanged;
storage does not lowercase or normalize. UTF8/NUL checks and declared collation
lookup, encoding and capability validation use the shared authorities.

TEXT/VARCHAR assignment into citext preserves text; BPCHAR assignment strips
right padding. BOOL assignment uses true/false, and INET uses network_show,
including full-width masks. These preferred casts precede string-category I/O
fallback for other supported scalar assignment/explicit inputs. Citext implicitly
casts to TEXT/VARCHAR; BPCHAR requires assignment/explicit context and retains
the ordinary sizing rules. Explicit outgoing scalar I/O is supported, not
ordinary assignment to INTEGER/BOOL or implicit incoming TEXT conversion.

Native metadata is CITEXT/string, with actual column nullability and unknown
length/precision, even before reading empty/NULL-only results. The generated
PostgreSQL OID is unknown to lib/pq: empty database type name, interface{} scan
type, non-NULL []byte and unknown nullability/length/precision. Catalog metadata
is collatable, nonpreferred category S, with actual allocated I/O identities.

The native selected manifest is the scalar and two strict immutable text hooks,
citextin(cstring) and citextout(citext). Its reciprocal _citext is lookup-only
with an internal dependency, not general array storage. Binary receive/send
remain absent. Advanced comparison, sorting, pattern matching, operators,
indexes/opclasses and locale machinery are not introduced by scalar storage.
Native controls recreate and remap installed identities while old readers retain
their original immutable values/metadata. Own physical TEMP placement fails
through actual staged script lookup; authorized foreign TEMP placement follows
the namespace owner's physical lifetime, not the installer's. TEMP Dump remains
nonportable.

## cube extension type

The registered cube1.5 provider supplies scalar columns, contextual literal and
bound text/byte input, typed NULL, same-type assignment and explicit TEXT/VARCHAR
I/O casts. Cube-to-character assignment is supported; typed character assignment
into cube and implicit character function-argument conversion are not.
Coordinates retain their entered corner orientation, binary64 precision and
signed zero. Coincident corners collapse using IEEE equality (NaN never equals
NaN); zero through100 dimensions are accepted. Output uses the shared float8
formatter and current extra_float_digits; native dump literals remain exact.
Input diagnostics follow completed grammar productions: required punctuation
precedes dimension checks, which precede float8 conversion; semantic errors in a
completed production can precede trailing-garbage errors. Dimension-mismatch
details preserve numeric token spelling but discard inter-token ASCII whitespace.

The actual native selected manifest is cube plus strict immutable text hooks
cube_in(cstring) and cube_out(cube). The real associated _cube catalog identity
is lookup-only, with an internal dependency, not array storage. Native receive/
send metadata is zero: PostgreSQL's actual cube_recv/cube_send are deliberately
not advertised or executable here. Constructors, other functions/operators,
comparison/sorting, indexes/opclasses, array input/binding and binary COPY are
outside this provider.

Native results expose CUBE, string ScanType, known declared nullability and
unknown length/precision even for empty/NULL-only results. lib/pq does not know
the dynamically allocated PostgreSQL cube OID: its type name is empty, ScanType
is interface{}, non-NULL values are []byte, and nullability/length/precision are
unknown. Reset/Dump/Restore remaps actual scalar, codec and owner identities;
pending readers retain their original immutable values, metadata and display.
Availability includes cube and citext alongside the six other registered providers.

## query_int extension type

The registered intarray provider supplies selected query_int scalar storage,
canonical text input/output, contextual literal/bound input, NULL, same-type
assignment, and explicit text/varchar I/O casts. Assignment from query_int to
admitted character targets uses canonical output, including typed NULL.
Typed character assignment into query_int and implicit/function-argument
character conversion remain rejected. Type identity is allocated by the installed
extension, not a fixed builtin OID. Native controls serialize CREATE EXTENSION
before consumers and remap actual type/codec/owner identities on restore.

The automatically associated _query_int type is lookup-only. This provider does
not add query_int array storage, integer-array storage or operations, @@/~~,
querytree, evaluation, sorting/comparison of queries, indexes or opclasses.
Lookup-only is a codec/storage boundary, not bootstrap pinning: the associated
array retains its real internal pg_depend dependency on the installed element.
The availability-view control description is PostgreSQL's stock intarray
description; it does not promise the stock extension's complete native object
inventory. Arbitrary internal/C function loaders remain unavailable. The real
PostgreSQL helper-ABI collision witness is consequently PG-only; native units
exercise genuine published type conflicts and complete provider validation.

Native query_int results expose QUERY_INT, string ScanType and known declared
nullability, including empty and NULL-only results; length/decimal precision
remain unknown. lib/pq's static OID map does not know PostgreSQL's generated
query_int OID: its type name is empty, ScanType is interface{}, nullability and
length/precision are unknown, non-NULL cells are []byte and NULL cells are nil.
SQL declaration nonnumeric modifiers retain the existing generic-type parser
boundary: a string modifier can fail at syntax analysis earlier than PostgreSQL's
catalog-driven “type modifier is not allowed” rejection. Numeric modifiers
retain the PostgreSQL type-modifier diagnostic. This is separate from parsing
the Boolean query text inside a value.

Reference expectations use the pinned PostgreSQL18.0 UTF8/C oracle for unchanged
release semantics. No newer devel overflow guard, artificial parenthesis/OR/node
limit, PostgreSQL18.6 runtime claim, or unmeasured named-TEMP script diagnostic
is implied. TEMP component validation/retirement units cover actual native
session lifetimes, not fabricated PostgreSQL helper-script parity.

## Types, expressions and catalogs

- Domain RENAME/OWNER/SET SCHEMA retain logical domain, companion-array and CHECK
  identities. Rename requires domain ownership, not source-schema CREATE; move
  checks destination actor CREATE before ownership, including same-schema moves.
  Only DROP also accepts the containing-schema owner. IF EXISTS suppresses missing
  domains or qualified schemas, not wrong kinds or permissions. RESTRICT and
  complete target-list failures are atomic; CASCADE follows real dependency
  ownership, dropping dependent attributes without dropping their whole tables.
  Identity edits do not rescan historical CHECK/default truth. Actual driver
  utilities Prepare quietly and enforce read-only/explicit-transaction boundaries
  at Execute. Native replay uses current qualified declarations and captured
  expressions; physical OIDs and external pg_dump interoperability are not promised.
- CREATE DOMAIN DEFAULT/NULL/NOT NULL use the existing declaration and coercion
  machinery. Defaults are variable-free; column/VALUE references, parameters and
  subqueries are rejected. A column recipe overrides the effective domain/base
  recipe; an explicit NULL recipe is a real mask. Supplied NULL and casts do not
  request defaults. Domain recipes are cooked against the immediate base without
  evaluating its constraints; demand preserves that inner conversion identity.
  NOT NULL is enforced on fresh conversions before CHECK truth, after existing
  CHECK initialization. Its23502 diagnostic names the conversion target and has
  empty Constraint even for a named declaration. Child NULL does not waive an
  ancestor's NOT NULL. Own `pg_type.typnotnull` is distinct from effective ancestry
  and driver Nullable knowledge. Logical view defaults precede physical base
  column defaults. Existing INSERT/UPDATE DEFAULT, MERGE insert, COPY and ADD COLUMN
  routes share the selected recipe; failures retain statement atomicity.
  Composite-field default dependencies use the actual backing relation and stable
  attribute, including inherited recipes. Attribute RESTRICT/CASCADE follows
  those dependencies without pruning a surviving sibling's written value.
  Native domain-default replay projects retired constructor fields away; restored
  deparse normalizes to the current row shape, not the source's historical arity.
  ALTER DOMAIN SET/DROP DEFAULT changes only the target's effective recipe.
  Previously created children retain their copied recipe/text, even after the
  parent's recipe changes or disappears; DROP clears, never re-inherits.
  SET/DROP NOT NULL changes own nullability, not ancestor or column constraints.
  New own SET validates existing direct/derived physical uses by NULLness only,
  without recasting or executing unrelated CHECK/default recipes. Failed,
  canceled or conflicted publication installs no partial metadata/storage roots.
  It uses the existing TypeExclusive/physical Share lock admission outside the
  engine mutex; readers remain admitted. Repeated own SET is a no-op/no rescan;
  DROP then SET rescans. Stored array/composite carriers block validating SET
  even when empty, but do not block default edits, DROP or repeated own SET.
  Real named NOT NULL definitions participate in pg_constraint, dependencies,
  deparse, object addresses, naming/OID reservations and native replay.
  CREATE's existing own NOT NULL syntax now constructs the same real metadata.
  Native dump pairs each domain declaration with a strict authenticated default
  snapshot record, preserving independent recipes/result usages without extra
  ancestor conversions. Ordinary external replay ignores these host records and
  is not promised identical CHECK demand or third-party pg_dump compatibility.
  Native held analysis uses current recipes/coercions; PostgreSQL may retain a
  rewritten old omitted default or an eliminated child-domain view coercion.
  These precise prepared-cache boundaries do not weaken direct/fresh behavior.
  All four forms retain the read-only and explicit-transaction utility boundary.
  Native Restore reconstructs these states with CREATE and paired snapshots;
  its ALTER DOMAIN allow-list remains limited to captured named NOT VALID CHECK
  additions, not arbitrary default/nullability utilities in input dump scripts.
- Domain CHECK uses the admitted shared expression providers and conversion
  routes, including existing nested members; subqueries and non-VALUE variables
  are rejected. TRUE/UNKNOWN pass, FALSE reports domain 23514. Fresh NULL is
  checked, while already-domain NULL can bypass conversion in the documented
  outer-join/scalar-subquery contexts. Domain CHECK ADD/DROP/VALIDATE is supported in
  autocommit, including NOT VALID's old-data/new-input distinction and selected
  validation. Direct/derived physical uses are scanned once; stored array,
  composite and rowtype containers reject validating changes even when empty.
  Unstored composite/view definitions alone do not block. Repeated VALIDATE
  rescans, including already-valid CHECKs, as in PostgreSQL18.0. ALTER's NO INHERIT
  modifier is accepted and ignored; CREATE's rejection remains unchanged.
  Validation uses real relation ShareLock admission, blocks writers but not
  ordinary readers, and atomically publishes through the existing catalog owner.
  Relevant incoming-use/storage drift returns a native publication conflict
  without CHECK-truth replay; PostgreSQL's complete concurrency schedule is not
  promised. Failed/canceled validation leaves its previous flag and roots intact.
  Executable composite arrays support storage and textual I/O. Logical composite
  comparison is supported; builtin/catalog row execution and generic composite
  hash/key providers remain separate boundaries.
  Pending conversions are not evaluated by JOIN/MERGE capability proofs or
  conflict inference. Retained expression recipes still initialize before rows
  are demanded; existing ADD COLUMN uses real per-row conversion, and existing
  BY SOURCE MERGE retains its separate target action-classification ON stage.
  Stored generated/partition/index recipes capture catalog/input authority;
  constant recipe errors can reject an empty declaration. Actual index events
  produce membership/keys once per genuine phase, not once per row value:
  ON CONFLICT preserves separate precheck, insertion and existing-key events.
  COMMIT, slot-only projection and trusted replacement transport produced entries
  without replaying SQL or caching CHECK truth. Physical rewrites and affected
  restored owner-dependent expressions execute under their real statement owner.
- Structural ROW-constructor comparisons use scalar field providers: equality
  and inequality combine three-valued field results; ordering stops at the first
  unequal or NULL pair. `IS [NOT] DISTINCT FROM` always returns Boolean, including
  empty constructors. Constructor expansion also applies to row IN/NOT IN and
  quantified subqueries. Composite-valued fields use their ordinary logical
  comparator, not row-constructor NULL rules; physical record-image operators
  remain unavailable.
- `aclitem` and `aclitem[]` are ordinary stored values, not authorization grants.
  Scalar comparison supplies only `=`; ACL arrays supply equality/inequality
  with shape, lower-bound and NULL-element semantics, but not ordering.
  `makeaclitem`, all thirteen `acldefault` object codes and the named-OUT
  `aclexplode` SRF execute through the typed expression path.
  Containment is `aclitem[] @> aclitem`, not scalar/scalar: one matching entry
  must cover both masks. Expansion and containment reject non-rank-one ACLs
  before NULL elements. The rank-one empty column default survives typed
  storage/replay but differs from rank-zero `'{}'::aclitem[]` text input.
  ACL binary receive/send, catalog ACL projections other than the documented
  `pg_class.relacl`, `pg_database.datacl`, `pg_attribute.attacl`,
  `pg_namespace.nspacl` and `pg_default_acl.defaclacl`, GRANT extensions,
  role-rename SQL and new generic
  grouping/search/hash providers are not added.
  Numeric constructors accept missing and zero identities as inert data.
  Native Dump requires live nonzero grantors for emitted ACL data/definition
  literals and rejects stale identities or zero grantors before writer I/O.
  Restore remaps principals to preexisting target roles and preserves rank-one
  empty ACLs using a typed default expression, including composite/domain
  carriers. Ordinary zero/dropped-identity output and equality remain available.
  See [ACL transport](TYPES.md#acl-items).
- Range/multirange storage does not imply general constructors, casts,
  ordering/grouping/hash/index providers, arrays or user-defined range creation.
  Selected scalar inspection (`lower`, `upper`, `isempty`, the four
  inclusivity/infinity predicates) executes for int4range, int8range, numrange,
  daterange, tsrange, tstzrange and the three integer/numeric multiranges.
  Binary `range_merge` requires one concrete range family; unary merge accepts
  an executable multirange and returns its companion range, filling gaps.
  Containment, overlap, all five positional/adjacency operators, scalar
  comparisons and union/intersection/difference execute for all six ranges and
  the three concrete integer/numeric multiranges. Predicates admit matching
  range/multirange cross operands and subtype-element containment. Set operations
  and comparisons require one physical family; mixed range/multirange set or
  comparison operands and cross-family promotion remain unavailable. Domains
  unwrap to their physical family; set results use the base descriptor.
  Empty sets are contained by every range/multirange; overlap and all five
  positional/adjacency predicates return false if either set is empty. Typed SQL
  NULL returns typed NULL. Disconnected range union/difference raises22000;
  multirange operations preserve disconnected results. Scalar comparisons order
  empty first and compare endpoints/members, not textual output; they do not
  supply ORDER BY or default-opclass/key admission.
  Typed columns/destination-contextual INSERT inputs and a known binary range
  argument supply family authority. Explicit range/multirange casts and general
  constructors remain unavailable. The selected constructor exception is unary
  `multirange(anyrange)` for int4range/int8range/numrange, returning the
  corresponding multirange; `unnest(anymultirange)` expands those three companions
  in ascending canonical order. Typed NULL construction is NULL; empty input is
  `{}`. NULL/empty expansion emits no rows, retaining range result metadata.
  Target/FROM, lateral, ordinality, explicit ROWS FROM zip and scalar/SRF nesting
  reuse existing query composition. Named-family/multiple-range constructors
  and tsvector unnest remain excluded. Array unnest and multiargument FROM
  shorthand use the selected array-relational behavior described below.
  A selected RANGE-valued DEFAULT still cannot be authored: default expressions
  do not gain column references, parameters, subqueries or explicit range casts.
  Native temporal constructor capability boundaries report 0A000; they are not
  PostgreSQL missing-function claims for valid upstream signatures. No excluded
  provider is supplied as a fallback.
  Carrier traversal is shared, but no default capability is fabricated.
  Daterange columns store civil DATE bounds with finite day canonicalization and
  explicit infinity distinct from omission. Tsrange columns use continuous
  TIMESTAMP WITHOUT TIME ZONE bounds without successor canonicalization; finite
  output is quoted, valid written zones do not rotate civil fields, and explicit
  infinity remains distinct from omission. DateStyle and relative input follow
  the timestamp subtype, while Dump emits ISO. Tstzrange uses continuous UTC
  instants ordered after timezone conversion; input/output uses session
  TimeZone/DateStyle, analyzed literals freeze and binds decode afresh. Dump
  emits ISO UTC offsets. Explicit infinity retains bounds distinct from omission.
  Temporal multiranges (including datemultirange) and multirange arrays remain
  lookup-only. Their upstream operators cannot execute without the absent
  temporal codecs/companions; native declaration/construction boundaries remain
  explicit, not PostgreSQL missing-operator claims. Daterange/tsrange/tstzrange
  add no range_agg/range_intersect_agg providers. Scalar comparisons work for all
  six ranges, including domains, contextual unknowns, NULL data and empty relations.
- Bit storage admits `bit_and`, `bit_or`, and `bit_xor` aggregates and selected
  scalar `bit_count`, `bit_length`, `length`, `octet_length`, `overlay`,
  `position`, `substring`, `get_bit` and `set_bit`, including implicit VARBIT inputs. Scalar `||`
  returns unrestricted VARBIT; `&`, `|`, `#`, `~`, `<<`, `>>` return
  unrestricted BIT. AND/OR/XOR require equal actual lengths (22026); concat
  and comparisons permit unequal widths. Shifts preserve width, reverse for
  negative counts and zero-fill at/beyond the width, unlike integral masking.
  Scalar `=`, `<>`/`!=`, `<`, `<=`, `>`, `>=` compare logical digits lexically,
  including leading zeroes and prefix lengths; NULL is strict. Bit access uses
  leftmost-zero INT4 indexes (BYTEA remains LSB-first with INT8 indexes).
  `get_bit` returns INT4; `set_bit` returns unrestricted immutable BIT, leaving
  its source unchanged. Logical bounds fail with 2202E before invalid new bits
  fail with 22023; NULL does not bypass input/signature/authority checks.
  Selected access calls work in defaults/generated/CHECK/view consumers and
  symbolic native replay. General bit casts, substr alias, ordering/index/grouping
  providers and bit arrays remain excluded. Unordered BIT aggregate DISTINCT retains the
  absent equality-provider error; BIT/VARBIT local ordering and bitwise windows
  remain excluded. Ordering by an independent integer key does not require bit
  comparison. Raw internal CHAR declarations
  remain excluded even where real catalog/query-derived values have codecs.
- MACADDR stores six-octet addresses, accepts the seven documented forms and
  PostgreSQL18.6 scanf edges, and emits canonical lowercase colon-separated text.
  Equality and ordering compare unsigned octets in original order; NULL is
  strict. Public textual direct/prepared transport, MACADDR metadata and logical
  replay of tables/data/defaults/CHECKs/views share the scalar codec. Invalid
  syntax22P02 and out-of-range octets22003 retain English primaries and
  phase-specific positions/context. Companion1040 remains lookup-only;
  MAC functions, bitwise/truncation operators and binary transport are excluded.
  No wire server, physical durability or OS networking is implied.
- MACADDR8 stores eight unsigned network-order octets inline, accepts six/eight
  octets with consistent optional separators and surrounding C whitespace, and
  inserts FF:FE into six-octet inputs without a modified-EUI bit flip. Its
  PostgreSQL18.6 pair scanner preserves released trailing-character edges.
  Canonical lowercase colon output, strict scalar comparisons, textual driver
  transport and typed defaults/CHECKs/views/logical replay share one codec.
  Malformed input returns22P02 with the original English primary and established
  source positions/context. Native DatabaseTypeName is MACADDR8; lib/pq is empty.
  Companion775 stays lookup-only. Arrays, binary transport, MACADDR conversions,
  helpers, default ordering/grouping/index providers remain excluded.
- INET `~`, `&`, `|`, address-plus-BIGINT in either order, and
  address-minus-BIGINT return INET; address-minus-address returns INT8.
  CIDR/domain inputs reuse implicit INET coercion. Full32/128-bit operations
  retain host bits; NOT/offset preserve the prefix and AND/OR use its maximum.
  Offset arithmetic may cross subnets. Mapped IPv6 remains family6.
  Family mismatches fail22023; offset range and rejected differences fail22003
  with exact PostgreSQL English diagnostics. Strict NULL does not bypass
  input/signature analysis or child errors. Immutable row-independent network
  constants and available parameters preflight at Execute, including FALSE
  and empty sources; row-dependent values remain lazy. Stored operators reuse
  existing default/generated/view validation and native reconstruction.
  Pinned release arithmetic edges and diagnostic Context are documented under
  [network transport](TYPES.md#textual-structured-and-network-transport).
  Selected network functions add `abbrev(inet/cidr)`, `host`, `text(inet)`,
  `family`, `masklen`, `inet_same_family` and `broadcast`, using the existing
  scalar, coercion, routine-ACL and stored-recipe paths. CIDR abbreviation is
  distinct from INET display, host omits prefixes, and text always includes
  them. Family/masklen return INT4, same-family returns BOOL, broadcast returns
  INET with host bits set for either family. All are strict and immutable.
  `hostmask`/`netmask` return full-width INET, `network`/`inet_merge` canonical
  CIDR, and `set_masklen(inet/cidr,integer)` preserves its selected result type.
  INET rewriting retains host bits; CIDR rewriting clears excluded bits.
  Both accept -1 as full width and reject other invalid lengths with22023.
  Merge requires the same address family. These strict immutable signatures
  share ordinary metadata, exact overload ACLs and symbolic stored replay.
  INET `<<`, `<<=`, `>>`, `>>=` and `&&` compare network bits only and return
  BOOL: strict containment excludes equal prefixes, inclusive containment
  includes them, and overlap accepts either direction. CIDR promotes to INET;
  mixed families return false and NULL returns typed BOOL NULL. These operators
  and their original `pg_catalog.network_sub`, `network_subeq`, `network_sup`,
  `network_supeq` and `network_overlap` routines share exact EXECUTE authority,
  dependencies and symbolic stored replay. Unsupported known signatures fail
  42883; malformed literals/binds retain the existing22P02 input diagnostics.
  This adds no INET XOR/integer shifts, network arrays, grouping/hash providers
  or OS networking.
- XID/CID/XID8 are distinct scalar identifiers, not integer/OID aliases or
  generated system attributes. XID supports equality/inequality (including INT4
  only on its right); CID only equality; XID8 unsigned comparisons/order.
  Explicit XID8→XID retains low32 bits. String I/O is admitted, not numeric/OID
  casts. The XID companion1011 is executable through the existing array codec
  and owned-value seams for pgrowlocks' genuine `xids` output. CID/XID8 companions
  remain lookup-only; transaction-identifier array table declarations retain
  their existing storage boundary. Scalar input itself does not allocate;
  transaction-information functions own actual assignment, full epochs and
  retention, without populating system columns.
- JSONPath supports `jsonb_path_query_array`, the four query-family consumers,
  JSONB `@?`/`@@` and standard SQL/JSON query syntax, not GIN. JSON has no equality
  provider. Catalog type identities do not imply executable codecs.
- Scalar `IN`/`NOT IN` lists and scalar operators with array-expression
  `ANY`/`SOME`/`ALL` reuse existing scalar comparison providers; array forms
  additionally require a concrete associated array and executable array codec.
  SOME is ANY; every form returns BOOL with SQL NULL semantics.
  Existing array-valued-domain RHS elements retain their original concrete array
  codec and element authority when the resolved physical element type matches.
  Inner arrays are comparison operands, not flattened outer elements; this
  does not add an array-of-array type or storage provider.
  Empty lists are syntax errors, unlike empty-array ANY/ALL reductions.
  Ordinary same-actual-type array comparisons separately use row-major elements,
  then count/rank/all lengths/all bounds. NULL elements equal each other and sort
  last; whole-array NULL is strict. Element equality and ordering are separate,
  demand-driven capabilities; non-NULL collatable leaf pairs demand collation.
  Fresh builtin-only comparison consumers select the actual namespace/signature
  winner, not a fallback based on spelling or builtin visibility. An executable
  custom winner fails analysis with `0A000: custom operators are unsupported in
  this comparison context`, including list/quantified comparisons, NULLIF,
  simple CASE, distinctness, fresh ROW fields and USING/NATURAL equality.
  Explicit `OPERATOR(pg_catalog.=)` and nonmatching custom overloads retain their
  genuine builtin behavior. Scalar-array validation checks nonarray RHS first,
  then selection errors/shells, then the selected result's Boolean requirement,
  before rejecting an executable Boolean custom winner.
  IN first partitions Var/non-Var items and attempts common-type scalar-array
  batching. Eligible named-composite constructor batches use structural equality,
  including equality of matching NULL fields. Only residual constructor pairs
  (including one-item lists and anonymous RECORD) select fresh field operators.
  Frozen residual comparisons retain coerced member casts and field NULL rules
  through view capture and logical replay. Ordinary named-composite scalar equality
  and stored composite-column IN remain structural. Array/record leaves, grouping,
  extrema, CYCLE and retained provider reconstruction never reselect scalar field
  operators by search_path.
  Ordinary custom scalar operators, including BETWEEN's lowered operators,
  still execute their actual providers. This adds no custom quantified runtime,
  missing element providers, array-key ordering/index opclasses or general §9.19
  array operators/functions.
  MONEY/INET/CIDR scalar-list comparisons do not enable their currently
  unsupported array elements or declarations.
- Integer/bigint/numeric/timestamp/timestamptz `generate_series` executes through SELECT-target expansion
  and FROM function sources, including views, CTAS and INSERT SELECT.
  Numeric steps are exact; start/step (not stop) determine display scale.
  Results remain unconstrained NUMERIC. NULL is empty; nonnull zero/nonfinite
  inputs have PostgreSQL diagnostics. Unreturned successors can exceed packed
  numeric limits without failing. Plain timestamp uses iterative month/day/time
  interval arithmetic, not timezone/DST stepping. NULL is empty; zero/net-zero
  and infinite steps fail22023. Infinite endpoints are legal; successor overflow
  fails22008 before emission even at an equal endpoint under LIMIT1. Mixed-sign
  steps may stall/reverse and have no progress guard. Timestamptz uses the
  executing session or explicit fourth TEXT zone for calendar months/days,
  while literal hours remain elapsed time. Held calls recapture settings;
  explicit named rules retain DST. Empty metadata remains TIMESTAMPTZ.
  Fixed abbreviations retain offsets and dynamic abbreviations use full owners.
  Zone lookup precedes nonnull step validation; any NULL formal is empty.
  Native symbolic views and persisted instants survive logical replay, without
  serializing settings/cursors. INSERT VALUES expansion remains excluded.
  FROM aliases, ROWS FROM zip/padding, bigint ordinality and implicit/explicit
  lateral references to preceding sources use the existing function-source lane.
  Window PARTITION/ORDER keys are legal; window frames and scalar restricted
  owners retain exact SRF placement diagnostics.
- `generate_subscripts(anyarray, integer [, boolean])` uses the same bounded
  target and FROM lanes and existing view/CTAS/INSERT SELECT consumers. It reads
  one dimension's stored bounds, returning INT4 without element inspection.
  INSERT VALUES expansion remains excluded. Simple anyarray requires a known
  array identity; the fixed result does not infer an untyped first operand.
  Existing array storage limits are unchanged. Native bounds arithmetic safely
  terminates at INT32_MIN in reverse, unlike the observed pinned18.0 defect;
  see [the release exception](QUERIES.md#array-target-subscripts).
- JSONB `@>` and reverse `<@` are strict immutable `(jsonb,jsonb)->boolean`
  operators. Objects match same-depth subsets; arrays ignore order and duplicate
  multiplicity, reusing individual nested matches without merging objects.
  Primitive membership searches immediate array elements only; array-valued
  object fields cannot match scalar-valued fields. Numbers compare exactly
  ignoring scale. SQL NULL produces NULL; JSON null remains a JSONB value.
  Direct/prepared/stored expressions and symbolic native replay are supported.
  Known TEXT/JSON/SQL-array operands do not implicitly convert to JSONB.
  No JSON containment, JSONPath or GIN provider is implied; existing SQL-array
  containment keeps its own operand family.
- Selected scalar/set JSON processing includes `json_typeof`, `jsonb_typeof`,
  `json_array_length`, `jsonb_array_length`, `json_object_keys` and
  `jsonb_object_keys`, each taking its matching JSON family. Kinds/keys return
  TEXT; immediate array lengths return INT4. SQL NULL is strict (NULL scalars,
  empty key sets), distinct from JSON null. JSON keys preserve decoded duplicates
  and source order; JSONB keys use canonical UTF8-byte-length then byte order.
  Wrong-root length/keys use PostgreSQL English 22023 diagnostics with absent
  runtime Position. Key sets reuse target zip/padding and lateral FROM stages;
  scalar stored consumers and query key views retain symbolic calls on replay.
  `jsonb_pretty(jsonb)` is a strict immutable JSONB-to-TEXT scalar. It formats
  the canonical payload with four-space indentation, LF, escaped JSON strings,
  exact numeric scale and multiline empty containers, without a trailing LF.
  SQL NULL stays NULL; JSON null renders `null`. Stored expressions retain the
  symbolic call on native replay. Typed JSON/TEXT inputs require explicit
  JSONB conversion; no JSON pretty counterpart or formatter options are added.
  `json_each(json)` and `json_each_text(json)` return paired key/value records:
  SELECT targets expose RECORD, while FROM exposes key TEXT and value JSON/TEXT.
  Raw JSON spelling, internal nested whitespace and duplicate occurrences remain
  intact; text strings decode and JSON null becomes SQL NULL only in the text
  variant. Empty objects/SQL NULL return zero rows with full schema. Both validate
  nested Unicode during processing and use exact array/scalar 22023 diagnostics.
  Aliases, whole rows, multi-column ROWS FROM padding/ordinality, target-set zip,
  function-only lateral sources and stored/native replay share existing stages.
  These signatures admit `from_json` named notation.
  `jsonb_array_elements(jsonb)` and `jsonb_array_elements_text(jsonb)` expand
  immediate array elements in sequence without flattening nested containers.
  Results remain scalar JSONB/TEXT: FROM exposes the named OUT column `value`
  even under a table alias; explicit column aliases override it. Target labels
  remain function names. JSONB results retain canonical values and numeric
  scale; TEXT decodes strings and maps JSON null to SQL NULL. Empty arrays and
  SQL NULL produce zero rows with complete metadata. Object/scalar roots raise
  22023 `cannot extract elements from an object` / `cannot extract elements from
  a scalar`, with no error position. Both use the existing target zip, lateral,
  ROWS FROM/ordinality, cancellation and symbolic view/native replay paths.
  `jsonb_each(jsonb)` and `jsonb_each_text(jsonb)` return key TEXT/value JSONB
  or TEXT records through the same target/FROM, alias, ordinality, lateral and
  stored/native replay paths as JSON each. Canonical decoded-key byte-length
  then byte order and last-duplicate-wins normalization come from JSONB.
  Numeric scale and nested containers are retained; text strings decode and
  JSON null becomes SQL NULL only in text mode. Empty objects/SQL NULL yield
  zero rows with full schema; all non-object roots raise 22023
  `cannot call jsonb_each on a non-object` / `cannot call jsonb_each_text on a
  non-object`, with absent runtime Position. Named `from_json` is admitted.
  JSON record conversion includes `json_populate_record`, `json_populate_recordset`,
  `json_to_record` and `json_to_recordset`. Named
  composites and explicit typed AS lists support recursive field coercion,
  rectangular arrays, typmods, domains and existing scalar/array/composite domain
  constraints. Domain defaults are not requested by JSON conversion.
  Missing fields preserve a non-NULL base or become SQL NULL; explicit JSON null
  overrides a base. Physical table constraints/defaults are not row-type defaults.
  Only recordsets are SRFs; scalar whole-NULL FROM calls still supply one expanded
  row. Typed AS lists work with/without aliases and per ROWS FROM member; put
  definitions inside ROWS FROM when adding WITH ORDINALITY. Populate accepts
  base/from_json names; to-record calls do not accept from_json named notation.
  Anonymous non-NULL scalar results without a descriptor fail at execution,
  while strict SQL NULL can remain an unresolved RECORD NULL. Computed metadata
  is available even for empty results. Calls remain STABLE and use existing
  stored-expression/native replay, cancellation and atomic-publication paths.
  JSONB record conversion adds `jsonb_populate_record`, `jsonb_populate_recordset`,
  `jsonb_to_record`, `jsonb_to_recordset` and `jsonb_populate_record_valid`.
  Named/table composite bases and explicit typed-AS layouts retain complete
  member usages, including nested composites, arrays, typmods and existing
  domain CHECKs. Missing keys preserve a non-NULL base member; explicit JSON
  null replaces it. Every recordset row uses the original base. Typed-AS
  definitions support the selected ROWS FROM/ordinality and lateral lanes.
  Demanded recordsets validate privately before publishing their first row;
  unopened LIMIT0 invocations remain unopened. These functions are STABLE;
  only `jsonb_to_record` is catalog-strict. Validity returns false for typed
  conversion errors, not setup/import/cancellation failures. With SQL NULL
  JSONB, valid returns NULL for a NULL base and true for a non-NULL base.
  General table functions remain excluded; no implicit JSON-to-JSONB input
  conversion is added.
  JSON-only extraction includes `(json,text/int4) -> json/text` via `->`/`->>`,
  `(json,text[]) -> json/text` via `#>`/`#>>`, and immutable variadic
  `json_extract_path` / `json_extract_path_text`. Object keys and integer array
  indices remain distinct; negative indices count backward. Missing/wrong shapes
  and out-of-range indices return SQL NULL. JSON slices retain source spelling;
  TEXT decodes strings and maps JSON null to SQL NULL. Path arrays flatten in
  row-major order regardless of rank/lower bounds; any NULL element returns
  SQL NULL before document processing. Empty paths use explicit VARIADIC arrays.
  Named calls use `from_json` followed by final `VARIADIC path_elems`.
  Processing validates Unicode in discarded branches too. PostgreSQL 18.0's
  traversal retains an earlier final match across a later duplicate intermediate
  key with no final match; it also converts negative path indices on unrelated
  arrays at the same depth. Thus `{"a":{"b":1},"a":{}} #> '{a,b}'` yields 1,
  and `{"x":[0],"a":[10,20,30]} #> '{a,-1}'` yields 10, unlike chained lookup.
  Empty paths trim container outer whitespace; raw root scalars retain leading
  whitespace but drop trailing whitespace.
  `json_array_elements(json)` and `json_array_elements_text(json)` expand
  immediate array members in source order as scalar JSON/TEXT rows. Nested
  containers retain source spelling/whitespace/duplicates/numbers. Raw strings
  keep quotes/escapes; TEXT strings decode, and JSON null maps to SQL NULL only
  in TEXT output. Empty arrays and SQL NULL yield no rows with full metadata.
  Raw expansion never decodes escaped Unicode; text expansion validates all
  strings, even nested ones, before exposing a row. Wrong object/scalar roots
  use PostgreSQL English 22023 diagnostics without runtime Position. FROM
  uses scalar OUT label `value` even under a table-only alias; SELECT labels
  use the function name. `from_json`, ordinality, target zip/nesting, lateral
  sources and symbolic stored/native replay share existing stages. JSONB
  expansion remains excluded.
  JSONB extraction includes the same four operators and immutable variadic
  `jsonb_extract_path` / `jsonb_extract_path_text`, returning JSONB/TEXT.
  Key TEXT and array INT4 RHS remain distinct; INT2 promotes but INT8/numeric
  do not implicitly narrow. Unknown RHS of known JSONB key/index operators is
  TEXT; unknown left operands remain ambiguous between JSON and JSONB.
  Missing/wrong shapes, out-of-range indices, NULL arguments and NULL path
  elements return typed SQL NULL. Integer operators expose scalar roots at
  indices 0 and -1, but nonempty scalar paths return NULL.
  TEXT[] paths flatten row-major regardless of rank/lower bounds. Empty paths
  select the whole document (functions require explicit VARIADIC for this).
  Array path indices accept leading C whitespace, signs and leading zeros;
  trailing junk/whitespace, invalid syntax and int32 overflow return NULL.
  Object path strings always remain exact keys. Raw results preserve canonical
  JSONB structure/output; TEXT strings decode, JSON null becomes SQL NULL and
  other nodes render canonically. Duplicate-last-key and unrelated-sibling
  negative paths follow the canonical tree, not JSON's released scanner state.
  JSONB input conversion occurs before extraction, even with NULL path elements.
  All six forms support defaults/generated/check/index/view expressions and
  symbolic native replay, retaining named arguments and final VARIADIC syntax.
  `json_strip_nulls(target json, strip_in_arrays boolean DEFAULT false)` returns
  JSON after recursively removing null-valued object fields. Default false
  retains array nulls; true removes them recursively. Bare JSON null and empty
  containers survive; SQL NULL in either formal returns SQL NULL. Processing
  compacts punctuation and decodes/re-escapes keys/strings, preserving surviving
  duplicate members, source order and raw numeric tokens without changing stored
  input. Named/reordered/default calls retain JSON metadata and symbolic replay.
  Explicit scalar-final VARIADIC is accepted without array expansion; a named
  final written argument must map to the last supplied formal position.
  It is STRICT IMMUTABLE, as in PostgreSQL 18.1 and later (18.0 marked it
  STABLE): generated columns and expression/predicate indexes accept it, as do
  DEFAULT/CHECK/view consumers. No `jsonb_strip_nulls` or JSON equality
  provider is added.
  JSON array aggregates `json_agg`, `jsonb_agg`, their `_strict` variants and
  `json_arrayagg` use ordinary grouping/FILTER/local ordering. SQL NULL is included
  by ordinary variants and omitted by strict/default ABSENT ON NULL; JSON null is
  retained. All-NULL strict input produces `[]`, but zero admitted rows produce
  SQL NULL. SQL/JSON array aggregation admits input FORMAT JSON, NULL/ABSENT ON
  NULL and applicable RETURNING JSON/JSONB, UTF8 BYTEA, existing string carriers
  and unconstrained domains with assignment typmods. Domain constraints/defaults/
  NOT NULL and aggregate-window execution retain their existing boundaries.
  Ordinary object aggregates `json_object_agg`, `jsonb_object_agg` and their
  `_strict` variants use two independent inputs, scalar key text conversion and
  the same grouping/FILTER/local ordering pipeline. NULL keys are errors before
  strict omission. JSON preserves member order/duplicates; JSONB canonicalizes
  keys and retains the latest admitted duplicate. Strict SQL NULL values are
  omitted before key conversion in the nonunique strict variants. PostgreSQL 18.0
  binary strict also omits converted root JSON null members after key/value conversion, retaining the
  latest nonnull duplicate; nested nulls are retained. All-omitted input yields
  an empty object; zero admitted rows yield SQL NULL.
  `json_object_agg_unique`, `jsonb_object_agg_unique` and their `_strict` variants
  use the same two independent ANY inputs, fixed JSON/JSONB results and
  ORDER BY/DISTINCT/ALL/FILTER pipeline. They reject duplicate converted keys
  with22030, including keys whose SQL NULL values are omitted by strict mode.
  UNIQUE STRICT validates/converts and reserves these keys instead of skipping
  key conversion. Text JSON checks during transition and names the JSON-quoted
  duplicate key; JSONB checks at finalization before root null compaction and
  reports no key suffix. NULL keys always fail, and outer uniqueness does not
  propagate to nested objects. All-omitted and no-input results are unchanged.
  SQL/JSON `json_objectagg` accepts one key VALUE value or key:value pair,
  NULL/ABSENT ON NULL (default NULL), WITH/WITHOUT UNIQUE [KEYS], formatted JSON
  input and applicable RETURNING JSON/JSONB, UTF8 BYTEA, strings and supported
  domains. Unspecified RETURNING selects JSONB for an exact JSONB key/value,
  otherwise JSON; output FORMAT JSON selects a JSON intermediate before the
  final assignment conversion. Unique keys include omitted SQL-NULL members:
  JSON checks during transition; JSONB checks at finalization before null
  compaction. Nested value objects do not inherit outer key uniqueness.
  NULL keys always fail. Stored views and native dump replay retain all options.
  `jsonb_objectagg` is not an alias. SQL/JSON local ORDER BY, DISTINCT/ALL and
  multiple pairs remain excluded; object aggregate window execution remains
  unsupported natively for both ordinary and SQL/JSON forms.
  `jsonb_path_query_array(target jsonb,path jsonpath,vars jsonb DEFAULT '{}',
  silent boolean DEFAULT false)` is an immutable, strict, parallel-safe scalar
  JSONB provider. It supports ordered navigation, wildcards/recursive descent,
  variables, strict/lax wrapping and structural checks, three-valued filters,
  comparisons, exists, starts-with and PostgreSQL regex flags `ismxq`.
  As in PostgreSQL, `x` is unsupported unless quote flag `q` overrides it;
  `q` retains `i` but ignores `s`, `m` and `x`.
  Negative subscripts are not counted backward; `last` names the final element.
  No match returns `[]`, JSON null/unknown returns `[null]`, and SQL NULL in any
  formal returns SQL NULL after ordinary child processing. Nonobject vars and
  demanded missing variables are unsuppressible errors. Silent strict structural
  errors stop traversal and preserve the preceding prefix, unlike lax misses;
  predicate structural failures become unknown. Defaults are real children,
  named/mixed calls retain written order, and GROUP identity distinguishes
  written labels/order and omitted versus explicit defaults. Stored defaults,
  generated/CHECK/view/index expressions and native replay use ordinary routine
  ACL/dependency and descriptor authority. Existing strict-NULL pruning and
  runtime-parameter optimizer boundaries remain unchanged. `type()` preserves
  arrays; `size()` counts array elements, returns1 for lax nonarrays and raises
  22039 for strict nonarrays. `boolean()`, `string()` and `keyvalue()` unwrap one
  array level only in lax mode; invalid conversions still raise22036/2203C in
  lax and strict modes, independently of silent suppression. Boolean accepts
  unpadded unique string prefixes or numeric output valid as signed int32
  (1.0 fails, 1e2 succeeds). String retains exact numeric display scale and
  spells booleans true/false. Keyvalue emits id/key/value objects with numeric,
  per-invocation/base identity: source revisits agree, equal distinct occurrences
  differ, and generated wrappers have fresh bases. No absolute ID or state
  survives a call or enters persistence. Demanded arithmetic and remaining
  methods raise native `0A000` through silent/predicate layers; unreachable
  branches are not eagerly rejected. `_tz` routines and GIN remain
  unsupported. JSONB `@?`/`@@` use the same exists/match consumers
  with silent evaluation and no variables: suppressed structural/subscript
  errors do not hide malformed path input, missing variables, caller cancellation
  or demanded native unsupported arithmetic/methods. Match can retain a singleton
  Boolean prefix before a suppressed error; strict exists still demands its tail.
  `jsonb_path_exists`, `jsonb_path_match`, `jsonb_path_query_first` and
  `jsonb_path_query` reuse this evaluator, signature/defaults and immutable,
  strict, parallel-safe routine authority. Exists returns BOOL for any item,
  not its truth value; strict traversal is complete, lax traversal may stop on
  its first item. Silent structural error returns SQL NULL even after a hit.
  Match fully traverses, requires exactly one boolean/null item, and raises
  `22038` otherwise (SQL NULL if silent). A silent structural stop retains a
  singleton boolean prefix for match. First fully traverses but retains only
  the first JSONB item; no match is SQL NULL, selected JSON null is JSONB null.
  Query yields ordered JSONB rows; no match or any SQL NULL formal means zero
  rows. It collects one invocation atomically before target/FROM iteration,
  permits an explicitly silent structural prefix, and discards results on
  ordinary error/cancellation. Existing aliases, ROWS FROM padding, lateral
  reopening and INT8 ordinality apply; this does not promise whole-read
  atomicity across invocations. Scalar stored consumers and symbolic query
  views retain ordinary rendering/dependencies. Restore scalar replay admits
  the three new scalar calls; Restore INSERT query sources remain excluded.
  `jsonb_set` and `jsonb_set_lax` return typed immutable JSONB through ordinary
  direct/prepared and stored scalar consumers. TEXT[] paths require existing
  intermediate parents; only a missing final item is created by the default
  true `create_if_missing`. Negative array indices count from the end;
  out-of-range final indices prepend/append without gap filling, and false
  preserves the target. Empty container paths preserve the root; scalar roots
  error. Reached NULL/noninteger path elements use full PostgreSQL diagnostics;
  unreached elements are not inspected. Set is SQL-NULL strict. Lax SQL NULL
  target/path/create also yields SQL NULL; its SQL NULL replacement supports
  exact `raise_exception`, `use_json_null` (default), `delete_key` and
  `return_target` modes. JSON null is not SQL NULL. Catalog formal names are
  `jsonb_in`, `path`, `replacement`, `create_if_missing`, `null_value_treatment`;
  named/default calls preserve written syntax and ordinary child demand.
  Explicit final VARIADIC selects the same scalar formal, not array expansion.
  Native immutable parameter calls retain the existing optimizer boundary:
  parameters remain runtime-owned, not general PostgreSQL custom-plan constant
  folding. The shared strict-scalar NULL rule can eliminate an invocation from an
  already imported typed NULL, after required child planning; it preserves original
  parameter/type captures and does not apply to row NULLs, domain barriers or
  SRF/window initialization.
  A selected strict comparison does not make its NULL-aware conditional,
  DISTINCT predicate or scalar-array reduction strict as a whole; those native
  expressions retain their own NULL and empty-input semantics.
  Native Dump/Restore keeps symbolic calls and descriptor ownership, and
  cancellation/failure does not publish partial JSONB or persistent state.
  `jsonb_insert(jsonb_in jsonb,path text[],replacement jsonb,
  insert_after boolean DEFAULT false)` uses the same immutable JSONB expression
  and persistence path. A missing final object key is inserted; any existing
  key (including JSON null or an identical value) raises `22023 cannot replace
  existing key`, with Hint `Try using the function jsonb_set to replace key value.`
  Arrays insert one value before the selected element, or after when true.
  Negative indices count backward; out-of-range negatives prepend and
  nonnegatives append regardless of the flag, without filling gaps or flattening
  replacement arrays. Missing intermediate parents/wrong shapes remain unchanged.
  Empty paths preserve containers; scalar roots still error. Reached-only
  path/rank/index diagnostics and all-four-operand strict SQL NULL behavior
  apply; explicit NULL after is not the false default. Computed direct/prepared/
  held results expose JSONB metadata, and stored default/generated/CHECK/view/
  index consumers retain symbolic calls and named/default/VARIADIC presentation
  through native replay. Failed writes/Restore and cancellation publish no
  partial state.
  Public `jsonb #- text[]` separately removes the selected nested field/item,
  with negative array indices and absent/out-of-range paths as no-ops.
  Empty paths preserve containers; scalar roots (including JSON null) error.
  Whole SQL NULL operands yield JSONB NULL. Reached NULL components error,
  unreached components are not inspected, and rank >1 paths fail with `2202E`
  rather than flattening. Stored/default/generated/CHECK/view/index expressions
  and logical Dump/Restore retain the symbolic operator and immutable inputs.
  Top-level `jsonb || jsonb` merges objects with whole right-hand values winning
  duplicate keys; otherwise it concatenates immediate array members, wrapping
  non-arrays as single members. `jsonb - text` removes an exact object key or
  every matching immediate array string, without touching nested strings.
  `jsonb - integer` removes one zero-based element; negatives count backward.
  Absent keys and out-of-range indices preserve the value. All three signatures
  return JSONB and are strict/immutable. Scalar deletion (including JSON null)
  raises `22023 cannot delete from scalar`; integer deletion from any object
  raises `22023 cannot delete from object using integer index`. These runtime
  diagnostics have no source Position. INT2 may widen to INT4; INT8/NUMERIC do
  not implicitly narrow. Unknown RHS deletion selects TEXT. An unknown left
  operand also selects deletion when the known key implicitly coerces to TEXT,
  including VARCHAR/BPCHAR and their domains; BPCHAR padding is trimmed by the
  existing cast. Unknown-left/integer subtraction remains numeric, while
  both-unknown subtraction remains ambiguous. Known JSONB concat coerces an
  unknown counterpart to JSONB, while known TEXT/JSONB mixed concat remains TEXT.
  Stored consumers and native replay retain symbolic operators
  and immutable inputs. Bound immutable calls under FALSE retain the existing
  native runtime-parameter demand boundary, unlike PostgreSQL custom-plan folding.
  `jsonb_delete_path(...)`, named concat/delete functions, TEXT[] top-level
  deletion, subscripting, generic JSON equality and JSON path/query execution
  remain excluded.
- SQL/JSON `JSON_EXISTS`, `JSON_QUERY` and `JSON_VALUE` support ordered PASSING,
  applicable RETURNING, query wrappers/quotes and ON EMPTY / ON ERROR. EXISTS
  tests item presence, including JSON false/null; VALUE requires one scalar;
  QUERY requires one item unless wrapped. Conditional wrapping leaves every
  singleton unchanged, and zero matches still select ON EMPTY. JSON null is
  distinct from SQL NULL; VALUE maps JSON null to SQL NULL without ON EMPTY.
  RETURNING conversion belongs inside ON ERROR; outer casts do not. Defaults
  are bound with all dependencies but evaluated only on their selected branch.
  ERROR ON EMPTY and failures of a selected DEFAULT do not recurse into ON ERROR.
  Document/path parsing, missing demanded variables, cancellation and ordinary
  provider errors escape suppression. Strict structural errors discard the
  entire selected result rather than publishing a preceding prefix.
  PASSING evaluates every written binding, with the first duplicate name winning.
  Temporal bindings retain typed comparisons with timezone use enabled; time to
  timetz comparison resolves the offset for the current local date and supplied
  clock time. VALUE uses SQL scalar output followed by destination input, not a
  direct temporal cast; QUERY and JSON/JSONB VALUE encode temporal JSON strings.
  Passed timestamptz JSON uses UTC independently of the live session timezone.
  Stored-expression volatility is path-sensitive: dynamic paths and mixed zoned/
  unzoned temporal comparisons are STABLE, including filter `@`, filter outputs
  and lax scalar array access. Strict array access clears temporal status;
  recursive access preserves it only when its range starts at depth zero.
  Same-zone comparisons and bare temporal variables are not blanket STABLE.
  The shared evaluator supports the JSON item methods documented
  above; demanded methods on typed temporal items and remaining unsupported
  methods/arithmetic raise native `0A000`, without eager rejection of unreachable
  branches.
- `JSON_TABLE` supports root/PASSING, typed/FORMAT JSON/EXISTS columns, column
  ON EMPTY/ERROR, named recursive nested paths and INT4 level ordinality. It is
  an implicitly lateral relational source, not a grantable ordinary routine.
  Parent columns precede nested output columns in the static schema regardless
  of written placement. Nested children outer-join their immediate parent;
  siblings concatenate rather than multiply or zip. Artificial missing subtrees
  bypass defaults, EXISTS and domain validation and receive typed NULLs.
  Row-path EMPTY ON ERROR discards the complete failing path prefix, including
  nested paths; ERROR throws. Neither policy suppresses column/document errors.
  At each parent transition, all immediate sibling paths initialize left-to-right
  before any descendant path or active column; suppression empties only that
  path, while a hard initializer error prevents subsequent column effects.
  Unnamed roots reserve `json_table_path_0` in the shared column/path namespace;
  explicit root names replace that entry, not additionally reserve it.
  Ordinary scalar columns use VALUE; JSON/array/composite or explicitly formatted/
  wrapper/quotes columns use QUERY. EXISTS converts JSON boolean spelling except
  INT4 (and its domains), which uses 0/1. Selected DEFAULT failures are terminal.
  For ordinary supported scalar DEFAULTs, binding precedes enclosing shape,
  full-tree column-reference restriction, set detection and RETURNING fallback
  coercion. Operators/functions may contain parameters through transparent
  wrappers, including specialized regclass syntax; bare parameters remain invalid.
  Actual scalar/typmod/array-element cast functions count as function expressions;
  IO/relabel/domain wrappers alone do not legalize an invalid shape. Accepted
  shapes containing columns report `DEFAULT expression must not contain column
  references` at the transformed expression location (operator start/function
  name); bare columns and special shapes retain generic shape rejection.
  Ordinary unknown-column/routine diagnostics precede these restrictions.
  Existing aggregate/grouping/subquery/window raw preguards remain before analysis;
  this does not change competing-error precedence inside those forbidden families
  or standalone SQL/JSON admission.
  Ordered PASSING runs before the document and again for each active ordinary/
  EXISTS column; parent columns are evaluated for each expanded output row.
  Complete demanded invocation materialization preserves late errors under
  LIMIT 1 and unprojected columns. LIMIT 0 may avoid row demand but not independent
  constant preflight. Static metadata survives empty/SQL-NULL documents.
  Native LEFT JOIN metadata retains known nonnull source keys and known nullable
  outer-extended JSON_TABLE columns; pq reports nullability unavailable instead.
  With both source SELECT and a default-volatility DEFAULT routine's EXECUTE
  revoked, native reports the function's 42501 first while PostgreSQL reports
  the table's 42501 first. Isolated denials and held revoke/regrant retain current
  authority on both backends; this feature does not change general admission
  ordering or add ALTER FUNCTION rename support.
  Existing SELECT, CTE, subquery, CTAS, views, INSERT SELECT, UPDATE FROM,
  DELETE USING and MERGE source routes share this source lane. PLAN and newer
  nested INNER/CROSS variants, advanced path methods, `_tz` and GIN are excluded.
- SQL/JSON constructors `JSON_ARRAY`, `JSON_OBJECT`, `JSON`, `JSON_SCALAR`,
  `JSON_SERIALIZE` and `IS [NOT] JSON` predicates support FORMAT JSON,
  applicable RETURNING, NULL/ABSENT ON NULL and UNIQUE KEYS. Query ARRAY uses
  admitted one-column queries, including correlation; it is not a scalar
  subquery with a one-row limit. Legacy text-array object overloads retain
  ordinary qualified/quoted function resolution.
  Those legacy calls cannot carry constructor clauses, including FORMAT JSON,
  in expressions, FROM or ROWS FROM; syntactically valid calls retain native
  scalar-table-provider limits. Nonempty member objects admit null/uniqueness
  policies and RETURNING; empty objects admit RETURNING only, and stored
  recipes preserve that empty syntax through Dump/Restore and SQL replay.
  Stored recipes distinguish constructor RETURNING from an enclosing assignment
  cast; JSON/JSON_SCALAR accept no RETURNING, and text-object construction is not
  replaced by direct JSONB construction. Predicates without UNIQUE validate JSON
  syntax without decoding Unicode escapes. WITH UNIQUE returns false on decoding
  failures instead of throwing; cancellation still propagates. Throwing input and
  constructor conversions retain their separate diagnostics.
  Output ENCODING on constructors and SQL/JSON aggregates requires exact
  BYTEA identity, excluding domains over BYTEA, and supports only UTF8.
  VALUE-style object keys accept parenthesized expressions, including casts
  and predicates; unparenthesized postfix casts, collation and arithmetic are
  not VALUE-key syntax. Grouped scalar expressions retain ordinary casts,
  collation, subscripts and field selection.
  Grouping does not turn predicates into arithmetic operands. Thus `(id<2)+1`,
  `DELETE FROM t WHERE (v IS NULL)+1=2` and `num_nulls((v IS NULL))` remain
  outside native admission. Selected calls do share predicate/comparison leaves:
  `(num_nulls(v)) IS NULL` is admitted, while bare nonboolean calls in
  WHERE/AND/OR/NOT fail with construct-specific42804 diagnostics. Boolean
  set-returning results remain forbidden. Explicit grouped casts and VALUE keys
  do not widen scalar arithmetic or the bounded selected-call argument language.
  Native JSONB serialization intentionally emits valid canonical JSON rather
  than the defective bytes of the pinned PostgreSQL 18.0 two-member witness.
  See the [upstream report](https://www.postgresql.org/message-id/03583FE7-A5EE-407C-907D-67865F3F8BFC%40gmail.com)
  and [followup](https://www.postgresql.org/message-id/CAGRkXqQXwh_gJmQE3PZj0v13omMPgFGBbb8TJoBFH8ECsAFCUg%40mail.gmail.com).
- JSONB existence operators `jsonb ? text`, `jsonb ?| text[]` and
  `jsonb ?& text[]` return BOOL and search only top-level object keys, immediate
  array string elements or a primitive string, using exact decoded strings.
  Object values and nested containers are not searched. Whole SQL NULL is strict;
  request-array NULL elements are skipped. Empty/all-NULL requests yield false
  for `?|` and true for `?&`; dimensions, bounds and duplicates do not matter.
  Both unknown operands infer the fixed signature for `?` and `?&`. For `?|`,
  one side must be typed: two unknown operands produce PostgreSQL 42725 because
  of its built-in signature collision, without enabling geometric execution.
  Invalid signatures use 42883; JSON and known TEXT do not implicitly become
  JSONB. Stored consumers retain operator syntax through deparse/Dump/Restore.
  Adjacent JSONB operators and SQL-callable `jsonb_exists*` remain excluded.
- Selected array-relational functions are `array_sample(anyarray, integer)`,
  `array_shuffle(anyarray)` and unary array `unnest(anyarray)`. Random operations
  use complete first-axis slices, retain element/domain identities without leaf
  equality or output providers, and are always VOLATILE. Shuffle retains shape
  and bounds; sample resets first lower bound to 1, preserves trailing bounds
  and returns canonical empty for count 0. Invalid counts raise 22023 against
  first-axis length, not cardinality. Whole NULL arguments produce typed NULL.
  A separate physical-session entropy stream is independent of `setseed` and
  `random()`; no deterministic array sequence or new parallel admission is promised.
  Unary unnest is valid in FROM, targets and scalar wrappers, flattening storage
  order including NULLs. Unqualified, undecorated multiargument FROM spelling is
  normalized to zipped unary `pg_catalog.unnest` calls, with shorter inputs padded
  by typed NULLs. Qualified/non-FROM/decorated multiargument calls do not acquire
  an overload. Per-function typed definitions block rewriting; top-level typed
  definitions retain PostgreSQL precedence. Unary explicit VARIADIC is accepted
  where the ordinary selected signature matches; no named formals are added.
  Already executable composite/domain-composite array elements expand their
  member layout in FROM; present NULL composites remain present member-NULL rows.
  Target output keeps one logical composite column. This adds no RECORD provider.
- Selected array transformations are `array_append`, `array_prepend`, `array_cat`,
  `array_fill`, `array_reverse`, `array_sort`, `trim_array`, and `array_to_string`.
  The three array `||` overloads share compatible leaf typing with push/cat;
  known array plus unknown selects cat, including eager unknown array input.
  Push accepts empty/one-dimensional arrays and retains their lower bound.
  Cat permits equal rank or a rank difference of one, requiring exact trailing
  dimensions/bounds. NULL/empty cat inputs retain the other input; push inserts
  NULL elements. Fill uses the value's actual associated array, checked INT4[]
  dimensions and optional bounds, not a fabricated array-of-array identity.
  Reverse/sort move whole first-axis slices, retaining all bounds; trim removes
  first-axis slices and rebases every retained bound to 1, even for count zero.
  Sort defaults to ascending/NULL last; descending defaults to NULL first, with
  an independent third flag. Flags are strict; multidimensional inner NULLs use
  ordinary array comparison. Empty/singleton sort does not demand ordering.
  Composite ordering retains its existing native 0A000 exclusion; composite
  shape-only transforms, scalar output, and empty/singleton sort do not add a
  comparator. WITHIN GROUP on these selected functions reaches missing-signature
  diagnostics without enabling an ordered-set transform overload.
  Only two/three-argument sort has named formals (`"array"`, `descending`,
  `nulls_first`), evaluated in formal order and retained in written replay slots.
  To-string flattens storage order and outputs raw scalar elements, omitting
  NULLs unless a non-NULL replacement is supplied; an empty replacement still
  emits separators. It is always STABLE and uses coherent captured output
  settings/catalog spelling; the other seven operations are IMMUTABLE.
  Shape-only operations add no comparison/output provider. Existing stored
  scalar consumers and native replacement owners retain exact type generations.
- Selected scalar array inspection includes `array_dims(anyarray)` returning
  TEXT, `array_ndims(anyarray)` and `cardinality(anyarray)` returning INT4,
  and `array_length/lower/upper(anyarray, integer)` returning INT4. These read
  outer shape only, with exact multidimensional/non-1 bounds and NULL elements
  included in cardinality. Empty arrays have NULL dimensions/rank/length/bounds
  and cardinality zero; whole NULL, NULL dimensions and nonpositive/missing
  dimensions return typed NULLs. The first operand requires a known array
  identity or the exact canonical catalog ANYARRAY typed-NULL carrier; unknown
  strings/NULL/parameters cannot infer it. Explicit VARIADIC decoration is
  accepted on an otherwise matching ordinary signature, without expansion.
  Qualified/unqualified calls group by the same resolved builtin identity.
  Arguments/FILTER transform before function namespace resolution; malformed
  named notation reports syntax errors at the actual label/operand, rather than
  a missing signature. These builtins still have no named formal parameters.
  Stored scalar consumers and native controls reuse existing owners.
  No leaf equality/output provider, new codec or overload subsystem is added.
- Selected §9.19 functions `array_position`, `array_positions`, `array_remove`,
  `array_replace` and operators `@>`, `<@`, `&&` consume existing array codecs
  and leaf equality providers. Functions are NULL-aware; membership is strict
  for whole arrays and does not match NULL elements. Search/removal require
  one dimension; replacement/membership accept multiple dimensions. Membership
  ignores bounds, shape, order and duplicate multiplicity. Function arguments
  select a common compatible leaf; operators require the same actual array type
  after outer-domain unwrapping. JSON has no equality provider: fast paths can
  avoid that demand, but non-NULL membership demands equality even for empties.
  Elements that are array-valued through domains reuse ordinary array equality,
  including shape/bounds and NULL-element handling. Earlier NULL/empty function
  fast paths remain available. Other array operators/functions, array-key
  ordering/index opclasses, custom overloads and new element providers remain
  excluded.
- General array operators/functions remain bounded. Aggregate DISTINCT/order
  on arrays and private `pg_node_tree`, and
  NAME-array/private-tree extrema, retain native capability exclusions.
  Native PG_NODE_TREE query grouping uses structural semantic-tree equality and
  hashing, including private recursive array carriers, outside released callable
  routine ACLs. This does not provide a SQL pg_node_tree array type or constructor:
  native ARRAY[adbin] rejects0A000 `unsupported array element type`.
  PostgreSQL plain pg_node_tree DISTINCT is independently supported through its
  textual equality and can require texteq EXECUTE; PostgreSQL ARRAY[pg_node_tree]
  rejects42704 because the array type is absent. Native does not fabricate
  texteq/array_eq authority for its private structural path, nor exempt ordinary
  admitted PostgreSQL arrays from their actual equality requirements.
- Enum-array results cannot be persisted by query-created tables or materialized
  views. Other admitted enum/domain-array carriers do not remove this exclusion.
- Ordered-set `percentile_cont`, `percentile_disc`, and `mode` support WITHIN
  GROUP sorting and FILTER, not OVER or custom aggregates.
  Continuous scalar/array results support FLOAT8 and INTERVAL; discrete arrays
  require an executable associated-array codec. Scalar discrete/mode inputs use
  all admitted ordering providers, including networks, enums, domains and scalar
  REGCOLLATION/REGNAMESPACE/REGCONFIG/REGDICTIONARY aliases.
  NAME, REGCLASS, REGTYPE, REGROLE, REGPROC, REGPROCEDURE, REGCOLLATION and
  REGNAMESPACE result arrays use existing executable codecs and actual catalog
  output authority. Internal CHAR, MONEY, REGOPER/REGOPERATOR,
  REGCONFIG/REGDICTIONARY, network and transaction-ID arrays retain their existing
  codec/catalog-carrier boundaries. Text-search scalar admission does not enable
  unsupported native name lookup or its lookup-only arrays. Internal CHAR uses
  admitted `pg_catalog.pg_class.relkind` expressions/providers, not unsupported
  user-table/CAST routes. Catalog metadata does not grant additional cast edges.
  Arrays, composite records, ranges/multiranges, bit strings and private trees
  do not acquire general ordering providers. ORDER BY USING, arbitrary collation
  providers, primary-key functional dependency and spill remain excluded.
- Hypothetical-set `rank`, `dense_rank`, `percent_rank`, and `cume_dist` support
  matching positive-length direct/order lists, pairwise common-type coercion
  and collation selection, FILTER, grouping and stored views. NULL tuples are
  retained; all four results are nonNULL even for empty global groups.
  Direct expressions use final grouped keys, once per executed call/group.
  The aggregate argument cap is 99 across both lists (PostgreSQL 18.6), without
  a smaller tuple limit. These aggregate overloads remain distinct from ranking
  windows. Custom aggregates and expanded ordering families remain outside this
  feature.
- Temporal providers retain exclusions for timezone age/finite inquiries and
  epoch `to_timestamp`.
  General named-function resolution and custom/generic optimizer-plan switching
  are not implemented.
- `CREATE RECURSIVE VIEW` requires explicit nonempty output aliases and an
  admitted SELECT body. It is syntax sugar over ordinary stored views and
  recursive CTEs, not a new executor or materialized relation. Short lists
  project outputs while retaining hidden recursive keys and dependencies.
  Existing TEMP/OR REPLACE combinations retain ordinary view rules; recursive
  CHECK OPTION suffixes are rejected. See [recursive views](QUERIES.md#recursive-views).
- Recursive SELECT CTEs support UNION/UNION ALL worktable iteration and parent
  demand with existing type/equality providers. SEARCH supplies typed breadth
  RECORD or depth _RECORD sort values, without controlling evaluation order.
  CYCLE supplies branch-local ordered-key record[] paths, closing rows and typed
  marks. Anonymous generated records/paths cannot be persistent columns (project
  seq::text/path::text instead). Occurrence record sorting uses existing lazy
  field comparison; general array/named-composite sorting/indexes remain excluded.
  Recursive modifying definitions and general lateral derived sources remain
  excluded. MERGE rejects its own WITH RECURSIVE; a recursive SELECT inside its
  derived source is admitted.
  Window providers are row_number(), rank(), dense_rank(), percent_rank(),
  cume_dist(), count(*), ntile(integer), existing integer/numeric/floating
  sum(expr), first_value/last_value/nth_value, and lead/lag with optional INT4
  offset and per-row default. Ranking uses full-partition ORDER peers, independent
  of frames/exclusions: rank retains gaps, dense_rank counts peer groups,
  percent_rank returns (rank-1)/(N-1) or zero for N=1, and cume_dist returns the
  inclusive peer-end position/N. Rank outputs are nonnullable INT8; ratios are
  nonnullable FLOAT8. Ranking-only runtime frame offsets are not demanded.
  Ntile returns nullable INT4 balanced full-partition buckets, larger buckets
  first, independent of frames/exclusions. Initial NULL bucket arguments retry;
  after positive initialization later arguments are not evaluated. Reached
  nonpositive buckets raise22014. Value functions retain concrete input types
  and PostgreSQL RESPECT NULLS; nth_value uses a current-row one-based INT4 ordinal.
  Lead/lag select in the complete partition regardless of frame/exclusion;
  written frame bounds still validate. Zero/negative offsets are legal, NULL
  offsets suppress value/default evaluation, and only absent targets use the
  current-row default. One/two arguments use anyelement; three use anycompatible
  value/default resolution. Unknown-only value input needs the third overload.
  MONEY/INTERVAL SUM, expression-count windows, explicit null-treatment/FROM LAST,
  DISTINCT/call-local aggregate ORDER and window aggregate filters remain excluded.
- MERGE supports RETURNING expressions, source/target rows, OLD/NEW aliases and
  lexical `merge_action()` TEXT labels INSERT/UPDATE/DELETE. An outside SELECT
  WHERE call errors; WHERE in a RETURNING expression subquery is permitted.
  Plain star is source-first; target defaults to NEW for INSERT/UPDATE and OLD
  for DELETE, with absent sides SQL NULL. Output is eagerly evaluated per
  accepted action and published atomically.
  All three match classes support DO NOTHING: first TRUE selects one action,
  FALSE/NULL fall through, and unconditional same-class successors are unreachable.
  Selected no-ops do not count, project output, evaluate mutation defaults or
  acquire mutation row grants; typed empty metadata remains available. Specifying
  a no-op requires SELECT on any live target column, plus all actual read demands.
  Query/multiple-row insertion actions, partial insertion targets and writable
  target views remain excluded. Triggers/rules/FDWs, target
  hierarchy writes, procedural functions and COPY(MERGE) are not
  supplied by this feature. Positioned mutation of a view is unavailable.
- Catalog sources are bounded projections, not complete `pg_type`, `pg_index`,
  `information_schema` or authentication catalogs.
  Native virtual catalog mutation and row locking are unavailable. Installed
  extension metadata does not provide arbitrary loading, installation or updates.
- Object identity/address inquiries implement the documented PostgreSQL18
  signatures of `pg_identify_object`, `pg_identify_object_as_address`,
  `pg_get_object_address`, and `pg_describe_object` through typed STABLE, STRICT
  expressions and released OUT records. Authoritative kinds include represented
  relations/columns/types, namespaces, collations/conversions, extension members,
  roles/current attachment database, owned constraints/defaults/generation
  recipes/rules/policies, builtin tablespaces/casts/operator classes, complete
  fixed routine/operator aliases and families already referenced by operator
  classes. Address-only metadata does not advertise new execution, DDL or SQL
  catalog providers. Objects without an owned identity are not manufactured.
  Numeric inquiry has no owner/table-SELECT requirement; qualified symbolic
  lookup retains schema access checks. Numeric stale results are kind-specific;
  a dropped attribute still describes its PostgreSQL tombstone, while a dropped
  relation yields `relation` plus NULL identity fields. Missing symbolic names
  error. Structured arrays preserve raw components; machine SQL-type spelling is
  distinct from identifier quoting (for example `json` versus name `"json"`).
  Human descriptions use visibility-sensitive spelling and raw column names.
  Unary operator output omits the absent operand in its argument array;
  explicitly passing `NONE` as an inverse type argument is a syntax error.
  Temporary schema canonical identity `pg_temp` is not itself accepted by the
  schema inverse lookup. Native replay preserves symbolic recipes and ordinary
  TEXT/TEXT[] values, not an automatic interpretation/remap of raw OID data.
  DEFAULT/CHECK inquiries request their consuming statement's catalog snapshot.
  Scalar describe with a constant NULL eliminates sibling runtime effects after
  argument analysis/planning, including FROM calls with their analyzed aliases,
  one NULL row and ordinality intact. RECORD calls and nonconstant NULL retain
  reached argument evaluation. This does not fold or cache non-NULL STABLE results.
  Inverse type-argument arrays check NULL and parse syntax per element in order,
  before later length validation and type resolution.
  Native prepared statements retain the existing catalog-replacement fence;
  fresh preparation resolves restored authority while retained cursor values
  and metadata remain unchanged.
  See the [object-address contracts and selected proof](../reference/object_address/README.md#testing).
- `pg_collation` is a PUBLIC read-only projection of installed collation
  descriptors with twelve PostgreSQL18 columns: `oid`, `collname`,
  `collnamespace`, `collowner`, `collprovider`, `collisdeterministic`,
  `collencoding`, `collcollate`, `collctype`, `colllocale`, `collicurules`,
  `collversion`. The first seven fields are nonnullable; the five TEXT fields
  preserve exact configured strings versus unavailable/inapplicable SQL NULL.
  NAME and TEXT attributes use catalog C collation950. Fixed seed OIDs are exact;
  generated identities are checked through their actual namespace/owner
  relationships, not PostgreSQL allocator parity. Reads do not filter by search
  path, encoding applicability or collation ownership. Fresh held executions
  reflect CREATE/ALTER/DROP; already returned native rows retain their snapshot.
  Native `unicode.collversion` is NULL because ICU version data is unavailable.
  Recorded version metadata need not equal the current provider version, and
  an empty configured version is not NULL. Catalog readability does not supply
  ICU/arbitrary-libc execution or import the host's collation inventory.
- Collation management exposes strict, VOLATILE
  `pg_collation_actual_version(oid)` and
  `pg_database_collation_actual_version(oid)` returning TEXT, and strict,
  VOLATILE `pg_import_system_collations(regnamespace)` returning INT4.
  Version inspection is read-only: recorded `collversion`/`datcollversion`
  strings are not actual provider versions and are not refreshed by these calls.
  Default collation100 uses the attached database's libc/C authority, just like
  database-version inspection; both return NULL. Implemented builtin providers
  return `1` independently of recorded versions; unavailable native ICU version
  data yields TEXT NULL. This inquiry-only result does not admit ICU execution,
  creation or system import, which remain unsupported.
  Missing numeric collation/database OIDs report42704. Numeric version reads
  do not add ownership or schema-USAGE gates.
  Import NULL returns typed INT4 NULL without authorization or provider work.
  Demanded non-NULL import checks the consuming statement's effective superuser
  attribute (not inherited membership), then numeric namespace existence:
  ordinary roles receive42501 `must be superuser to import system collations`;
  superusers with absent numeric namespaces receive3F000
  `schema with OID N does not exist`. Symbolic REGNAMESPACE conversion can fail
  first. A valid native import reports0A000
  `system collation import is unsupported in embedded mode` without changing
  catalog rows. No OS/ICU enumeration, fake successful zero, or synthetic
  imported definitions are supplied. Prepare and undemanded expressions do not
  import. Real PostgreSQL coverage exercises genuine provider rows, returned
  counts, skipped repeats and single-row replacement. See
  [selected proof and smoke](../reference/patterns_encoding/README.md#collation-management-functions).
- `pg_tablespace` is a PUBLIC readonly builtin-only projection per Engine:
  `(1663,pg_default,10,NULL,NULL)` and `(1664,pg_global,10,NULL,NULL)` initially,
  for named columns `oid,spcname,spcowner,spcacl,spcoptions`
  (OID/NAME/OID/ACLITEM[]/TEXT[]). Owner 10 is the canonical Engine principal,
  independent of bootstrap role renaming or the current reader. Ordinary roles
  see both complete rows. Both arrays are initially typed SQL NULL, not empty
  arrays: NULL `spcacl` denotes default tablespace privileges, not PUBLIC CREATE.
  CREATE grants materialize genuine descriptor ACLs in `spcacl` without physical
  placement; an explicit default-equivalent ACL remains distinct from raw NULL.
  The catalog relation's explicit PUBLIC SELECT ACL is separate.
  Supported wire origins retain relation 1213 and canonical attributes 1,2,3,4,5;
  `spcacl` precedes `spcoptions`, not an appended shim. Both arrays are nullable.
  NAME and TEXT[] metadata use catalog C collation950; ACLITEM[] is uncollated.
  Native catalog mutation remains unsupported, not a claim that PostgreSQL
  universally forbids catalog writes.
  `pg_tables` presents this shared catalog in `pg_global`; that logical metadata
  is not physical storage or cross-Engine aggregation. No user tablespaces,
  placement, filesystem inquiries or options validation are provided.
- `pg_default_acl` is the read-only relation826 with five nonnullable columns:
  `oid OID`, `defaclrole OID`, `defaclnamespace OID`, `defaclobjtype CHAR`,
  `defaclacl ACLITEM[]`. It projects only surviving implemented default policies:
  tables `r`, sequences `S`, routines `f`, types `T`, schemas `n`, large objects
  `L`. Namespace0 means global; scoped rows identify a real namespace. The creator
  is not replaced by schema ownership or inherited membership. Baseline policies
  have no row; a nonbaseline empty global ACL is `{}`, never NULL.
  Stable producer-owned OIDs survive ACL edits, recipient cleanup and principal/
  namespace remapping; deletion followed by recreation obtains a new identity.
  PUBLIC catalog SELECT exposes the whole inventory without a schema-USAGE or
  creator/grantee row filter. Ordinary permission admission still applies to
  empty and held queries. Only default-catalog presentation sorts ACL items by
  released numeric grantee/grantor/mask order; generic object ACL order is unchanged.
  `pg_class`, `pg_attribute`, reciprocal row/array types and bounded `pg_tables`
  describe this indexed, database-local table without physical index/TOAST rows.
  Native DML and row locks remain unsupported. Dump rejects stored defaults before
  output, unsupported Restore is atomic, and Reset clears policies. This catalog
  alone adds no routine creation, catalog GRANT SQL or general serializer capability.
  Large-object creation consumes its global defaults as documented above.
  See [selected proof](../reference/catalogs/README.md#pg_default_acl-selected-proof).


## Data type privilege descriptors

`information_schema.data_type_privileges` exposes five columns:
`object_catalog`, `object_schema`, `object_name`, `object_type`, and
`dtd_identifier`. It describes real type usages, not an inventory of types.
Supported arms are TABLE columns (tables, ordinary views and partitions),
DOMAIN/1, and USER-DEFINED TYPE attributes of standalone composites. Unused
enums, empty composites, table row types, materialized views, indexes and
sequences do not create standalone descriptor rows. Routine parameter/return
descriptor capability is not exposed natively; no ROUTINE inventory is invented.

TABLE rows require owner-role inheritance or any SELECT, INSERT, UPDATE or
REFERENCES privilege on that column (including table-level rights). DELETE,
TRUNCATE, TRIGGER and MAINTAIN alone do not qualify. Domains/composites require
owner-role inheritance or type USAGE. Ownership remains independent of revoked
ordinary privileges. PUBLIC and INHERIT grants apply; SET-only membership does
not until SET ROLE. No schema USAGE, search-path or underlying-type USAGE filter
is added. Other physical sessions' TEMP TABLE descriptors are excluded; a role
switch does not change the owning physical session.

Descriptor numbers are stable positive attribute numbers, retaining DROP gaps;
domains use `1`. Typmods, arrays and array rank do not multiply rows or generate
`a`-prefixed element identifiers. The five genuine information-schema builtin
domains retain their seeded PUBLIC USAGE visibility by stable catalog identity,
including after a schema rename; output uses the namespace's current name.
Result metadata owns native
NAME/NAME/NAME/VARCHAR/NAME primitive carriers, nullable with source origins;
it does not claim PostgreSQL's declared information-schema domain identities.
Fresh direct and prepared executions capture current catalog/role authority;
already returned native rows remain immutable across later DDL and controls.

## pg_attribute column metadata

`pg_catalog.pg_attribute` has the released PostgreSQL 18 25-column shape,
including nullable INT2 `attstattarget` and the nullable ANYARRAY `attmissingval`.
It describes only actually represented relation fields; omitted neighboring
catalog fields are not fabricated dropped columns. Builtin attribute numbers
follow their released catalog headers even when a bounded source's SELECT order
differs. User and dropped rows use their own relation OID and stable attribute
number, not query/index source-column provenance.

The existing reserved native `information_schema.table_privileges` source owns
primitive NAME/VARCHAR columns, with VARCHAR(3) for its two yes/no fields.
Its attribute rows truthfully expose those native usages, not fabricated
SQL_IDENTIFIER/CHARACTER_DATA/YES_OR_NO domain identities. The shared reference
case independently checks PostgreSQL's actual domain identities and this named
native representation boundary; names/ordinals, C collation, false NOT NULL and
absence of system rows remain exact. Executable user-declared
`information_schema.sql_identifier` and `information_schema.yes_or_no` columns
do not migrate these provider fields.
All 28 represented `pg_constraint` fields retain their released physical/type
facts. Its first 20 fields are nonnullable; the seven array fields and `conbin`
in its variable-width tail are nullable.

Explicit array brackets retain declared rank independently of the array type
OID and stored value dimensions. Initial view, CTAS, materialized-view, LIKE,
inherited and typed-table copies use rank zero; merged inherited declarations
also use zero. A partition copies rank zero from its parent's explicit declaration.
Existing explicit typed ALTER/ADD CASCADE declarations publish their requested
rank on both source and typed table, including same-type rank-only changes.
Ordinary views and indexes do not infer NOT NULL constraints from their sources;
index INCLUDE attributes have catalog collation zero.
Native array btree keys retain their existing `42704` operator-class exclusion;
a supported scalar key with an array INCLUDE column still exposes that column's
declared rank. Partitioned index creation retains its `0A000` exclusion. Shared
reference cases preserve PostgreSQL's complete rows for both excluded definitions
and assert unchanged native parent metadata with no failed-index catalog residue.

Tables, partitioned tables, materialized views, sequences and represented catalog
tables expose the six negative system rows; views, standalone composites and
indexes (including partitioned indexes) do not. This metadata does not implement
CTID, TABLEOID or transaction-ID row access. Catalog reads use the existing
statement-selected catalog and role authorities, including transaction-private
visibility. Both backends expose committed TEMP descriptors to catalog readers
in other sessions; another session still cannot resolve or execute the TEMP
table by its unqualified name.

DROP retains scalar physical/declaration facts without retaining the removed
type's dependency. Native Dump/Restore remains logical: active declarations and
rank survive, while deleted-column history and old attribute holes are not replayed.
Typed tables with nonzero active rank are reconstructed as ordinary tables and
attached through the existing ALTER TABLE OF route. This preserves an altered
rank3 table alongside a later-created rank0 table without globally replaying
ALTER TYPE and changing unrelated declarations.
An independently declared partition with nonzero active rank likewise uses
ordinary CREATE followed by ATTACH, not the rank-zero PARTITION OF copy route.
Inherited/merged columns reconstruct with rank zero. Where an active inherited
rank differs, Dump appends genuine `UPDATE pg_catalog.pg_attribute SET attndims=...`
SQL under `SET SESSION AUTHORIZATION DEFAULT`, after all owner SQL. Native
Restore accepts only that bounded normalized AST against its isolated staged
catalog: one nonnegative INT2 rank literal, a qualified regclass relation literal
and a live column-name literal. It changes no type, locality, inheritance count,
default, ACL or data, and failures/cancellation preserve the old catalog.
This preserves independently aged descendants, local ADD merges and typed-source
divergence without globally replaying historical source alterations.
**PostgreSQL replay requires an authenticated superuser/catalog-update authority**
for that final repair block; ordinary table ownership is not sufficient.
Public native catalog UPDATE remains unsupported, including the same SQL.
The native parser normalizes `RESET SESSION AUTHORIZATION`, `SET SESSION
AUTHORIZATION DEFAULT` and its preexisting native-only `SET AUTHORIZATION DEFAULT`
alias to the same administrative-reset AST. PostgreSQL 18 rejects that short alias
with `42601` at `AUTHORIZATION`; the dump emits the PostgreSQL-valid long form.
Restore therefore accepts all three, resetting only its isolated authenticated
administrator—not live callers, cluster roles or restored object ownership/ACLs.
Named short `SET AUTHORIZATION 'role'` and role DDL remain outside Restore's
owner-directive whitelist; failed late input still publishes nothing.
Native ADD default fills are materialized, so `atthasmissing=false` and
`attmissingval=NULL`; PostgreSQL may retain real fast-default physical history.
Statistics targets, column options and foreign-data-wrapper options remain NULL
unless already owned by a supported feature; new setters and physical storage
machinery are not supplied.
SQL REVOKE that removes the final column privilege restores NULL `attacl`; the
private explicit-empty ACL representation remains distinct and projects `{}`.
Held statements containing old resolved regclass literals retain the existing
catalog-replacement fence after Reset/Restore. A held numeric-OID parameter
reader refreshes against the current catalog instead; eager captured rows stay
unchanged. No catalog fence is relaxed for metadata queries.

ANYARRAY support is limited to the exact canonical typed-NULL catalog result,
with driver metadata and NULL transport through ordinary projections/subqueries.
The six shape inspectors produce concrete scalar NULLs. Reverse, sort and trim
preserve the ANYARRAY NULL result; to-string produces TEXT NULL without inferring
a leaf. Compatible push/cat and fill do not acquire a pseudo-array association.
It remains non-concrete and unavailable as a declared storage, domain, parameter,
cast or generic comparison/ordering type. Catalog mutation and row locks retain
their native `0A000` exclusions. See [selected proof and smoke](../reference/catalogs/README.md#pg_attribute-selected-proof).

## pg_language supported language metadata

`pg_catalog.pg_language` is relation2612, with these nine columns in order:

| Column | SQL type | Native nullable |
|---|---|---|
| oid | oid | no |
| lanname | name | no |
| lanowner | oid | no |
| lanispl | boolean | no |
| lanpltrusted | boolean | no |
| lanplcallfoid | oid | no |
| laninline | oid | no |
| lanvalidator | oid | no |
| lanacl | aclitem[] | yes |

The complete native inventory is exactly
`(12,'internal',10,false,false,0,0,2246,NULL)`. Fixed owner10 is the bootstrap
role, named `postgres` natively. NAME uses C collation950; every column has
typmod−1. The initialized row/array types retain ordinary catalog ownership and
relationships; `pg_class.reltype` identifies the row type through REGTYPE.
The bounded `pg_type` source exposes its actual row/array links. The language
relation appears as an indexed catalog table in `pg_class`
and `pg_tables`, not `pg_views`; this does not synthesize physical indexes or
TOAST storage.

Handler, inline-handler and validator columns are OID, not REGPROC. Zero handler
OIDs are nonnull metadata. Validator2246 identifies `fmgr_internal_validator`
through existing lookup-only routine identity support; it does not add an
executable validator or an executable `pg_proc` row. NULL `lanacl` means default
language privileges, not an empty ACL and not a grant to execute internal
functions. PUBLIC SELECT on the catalog relation is a separate table privilege.

The native language catalog still has no `c`, `sql` or `plpgsql` rows. The bounded
[scalar SQL routine facility](#scalar-sql-routines) does not fabricate additional
language metadata or supply a general procedural runtime. Language DDL, DO,
handler/validator execution and external native ABI loading remain unavailable.
Catalog INSERT/UPDATE/DELETE fail with
`0A000` / `catalog source mutation is unsupported`, even for zero-match
predicates. Row locks fail with `0A000` /
`row locking is not supported for virtual catalog relations`. PostgreSQL itself
uses table permissions for ordinary-reader writes; its catalogs are not claimed
universally immutable. Held native results retain their row/types across
Reset/Restore, while held statements read the replacement catalog.
See [selected proof and smoke](../reference/catalogs/README.md#pg_language-selected-proof).

## pg_transform transform metadata

`pg_catalog.pg_transform` is ordinary catalog relation3576, namespace11,
bootstrap owner10. Its five nonnullable columns are `oid OID`, `trftype OID`,
`trflang OID`, `trffromsql REGPROC`, `trftosql REGPROC`, in that order, with
typmod−1, no collation or dimensions. The direction columns retain REGPROC24,
not OID26, TEXT or REGPROCEDURE. Native empty results expose int64 carriers for
OID and string carriers for REGPROC, with known nonnullability.

The complete implemented transform inventory is **empty**, including after
unaccent installation and ordinary SQL routine creation. Casts/codecs, routines,
extension types and internal-language availability are not transforms.
No transform provider or CREATE/DROP TRANSFORM, procedural-language runtime,
handler or external ABI support is introduced. PostgreSQL defines a transform
by its exact type/language pair; zero function identity means default conversion,
not NULL. There are no native rows on which to exercise populated directions.
Existing FK metadata declares trftype→pg_type.oid, trflang→pg_language.oid and
optional trffromsql/trftosql→pg_proc.oid; empty joins do not prove populated
identity fidelity.

The relation appears as an indexed ordinary table in pg_class and pg_tables,
not pg_views; no physical index rows are fabricated. Existing native row10103
and array10102 have reciprocal catalog-owned links, refreshed by Reset/Restore.
These row/array numbers are native allocated identities, not release-fixed OIDs.
PUBLIC reads work directly, through fresh/held statements and stored views.
Saved empty-result descriptors survive catalog replacement.

Missing-column diagnostics are `42703` / `column "missing" does not exist`.
Ordinary-role INSERT/UPDATE/DELETE report `42501` /
`permission denied for table pg_transform`; TRUNCATE reports `42501` /
`permission denied: "pg_transform" is a system catalog`. Current authority is
checked even for zero-row predicates and held statements. Native superuser writes
remain `0A000` / `catalog source mutation is unsupported`; this is not a claim
that PostgreSQL superusers cannot modify catalogs. Native row locking reports
`0A000` / `row locking is not supported for virtual catalog relations`.
See [selected proof](../reference/catalogs/README.md#pg_transform-selected-proof).

## pg_ts_config configuration metadata

`pg_catalog.pg_ts_config` is relation3602, namespace11, bootstrap owner10.
Its exact column order is `oid OID`, `cfgname NAME`, `cfgnamespace OID`,
`cfgowner OID`, `cfgparser OID`. All five columns are nonnullable with typmod−1;
NAME has C collation950, and the OID columns have no collation. It is an indexed
catalog table in `pg_class`/`pg_tables`, not a `pg_views` entry. This logical flag
does not manufacture physical index objects. Rowtype10064 and array10063 retain
catalog-owned reciprocal links and are refreshed on catalog replacement.

The complete native inventory is **empty**, including after actual unaccent
installation. No configuration provider exists: scalar TSVECTOR/TSQUERY codecs
and dictionary descriptors are not configurations. The pinned PostgreSQL oracle
independently checks builtin `(3748,'simple',11,10,3722)` and its namespace,
owner and default-parser links; that row is not seeded natively.
The existing foreign-key metadata declares cfgnamespace→pg_namespace.oid,
cfgowner→pg_authid.oid and cfgparser→pg_ts_parser.oid without inventing target
parser rows.

PUBLIC SELECT works directly, through fresh/held prepared queries and views.
Empty results still expose exact descriptors. Native Dump/Reset/Restore retains
the builtin relation, old result metadata and fresh held-statement reads.
Ordinary-role DML, writable CTEs and MERGE fail with `42501` /
`permission denied for table pg_ts_config`; TRUNCATE reports `42501` /
`permission denied: "pg_ts_config" is a system catalog`. Authority is checked
at execution even for zero-row predicates and held statements. Native superuser
mutations fail with `0A000` / `catalog source mutation is unsupported`;
PostgreSQL superusers are not claimed universally unable to write catalogs.
Native row locking remains `0A000` /
`row locking is not supported for virtual catalog relations`.

Configuration creation/use, pg_ts_parser, pg_ts_config_map, normalization,
matching and symbolic REGCONFIG lookup remain outside this catalog support.
See [selected proof](../reference/catalogs/README.md#pg_ts_config-selected-proof).

## pg_type supported type metadata

`pg_catalog.pg_type` is relation1247 in pg_catalog11, owner10, rowtype71 and
array210. It is an indexed catalog table in pg_class/pg_tables with32 columns,
not a physical implementation of PostgreSQL index2703/2704 or TOAST storage.
Its released order and database types are:

```
oid OID, typname NAME, typnamespace OID, typowner OID,
typlen INT2, typbyval BOOL, typtype CHAR, typcategory CHAR,
typispreferred BOOL, typisdefined BOOL, typdelim CHAR, typrelid OID,
typsubscript REGPROC, typelem OID, typarray OID,
typinput REGPROC, typoutput REGPROC, typreceive REGPROC, typsend REGPROC,
typmodin REGPROC, typmodout REGPROC, typanalyze REGPROC,
typalign CHAR, typstorage CHAR, typnotnull BOOL, typbasetype OID,
typtypmod INT4, typndims INT4, typcollation OID,
typdefaultbin PG_NODE_TREE, typdefault TEXT, typacl _ACLITEM
```

The first29 attributes are nonnullable; default/tree/ACL are nullable. Catalog
NAME, typdefaultbin and typdefault use C950; typacl has declared array rank1.
The private PG_NODE_TREE carrier accepts genuine collation usage authority,
including C950 on catalog attributes; this does not enable tree text input,
user storage declarations or additional cast/codec routes.
Captured metadata preserves typed OID/REGPROC values, NULL versus explicit
empty/custom ACLs and actual owner principals. Zero REGPROC displays `-`.
PUBLIC reads expose the inventory independently of schema/type USAGE grants.

Families deliberately opt into metadata: implemented scalar/alias/temporal/
JSON/numeric/network/bit/UUID/ACL families, circle718, polygon604, the six executable ranges and three
executable integer/numeric multiranges, unknown/void/record/anyarray, actual
enum/domain/composite/installed-query_int definitions, and real represented
catalog/user-relation rowtypes. Every admitted element retains its genuine
canonical companion array; array typrelid is0, including relation-row arrays.
Nested domains retain the immediate base and declaration typmod/dimensions;
bootstrap array typmod callbacks and NAME's char element/handler are preserved.
Installed query_int uses actual installation-owned I/O procedure addresses.
Domains resolve complete immediate-base facts through that same captured
catalog before applying the shared family inheritance rules. This includes
domains and nested domains over installed query_int: typoutput is the actual
installation-owned bqarr_out, typinput2597/typreceive2598/typsend0 and immediate
typbasetype remain exact. Canonical companions use the completed element facts.
A same-numbered but foreign immediate-base generation is not interchangeable;
retained roots keep their own installation while fresh publication/reinstallation
uses current authority. This metadata composition adds no codec or SQL capability.

This inventory is deliberately narrower than REGTYPE lookup. Remaining
unimplemented xml/geometry/full-text/refcursor/vector/pseudo families and unrelated
lookup-only relation identities are absent. BOX/POINT, LSEG601, circle718 and polygon604 scalar and
companion metadata are admitted; companion catalog visibility does not enable
their SQL array codecs. Temporal multiranges4533/4534/4535
and arrays6152/6153/6155 remain absent even though pg_range preserves their
released identity links. Existing concrete and lookup-only companion codec
flags are unchanged: catalog visibility never enables storage, input, casts,
constructors or invocation of identity-only I/O routines.

The seeded information_schema.time_stamp domain retains the pinned18.6 metadata
default `CURRENT_TIMESTAMP(2)` and SQLVALUEFUNCTION op4/type1184/typmod2/location-1
inventory tree. Its cooked executable default and descendants use the existing
transaction-time provider. User-domain defaults expose genuine native `plan.BoundTree`
structural output rather than PostgreSQL nodeToString layout or physical OIDs.
Native Dump/Restore preserves own default declarations and copies inherited
recipes through base-first creation, without adding a new inner conversion.
Legal already-domain NULLs persist through one narrow host-authorized native
captured-cell representation, including nested composite/array carriers and
exact bounds. Its versioned comment must pair immediately with the actual
single-row INSERT and match live declarations in the isolated superuser import.
Ordinary SQL ignores it; new NULL inputs and all non-NULL conversions retain
their normal constraint behavior. This is not PostgreSQL dump compatibility.

The predefined `information_schema.yes_or_no` accepts consumer-declared columns
through the ordinary type resolver and catalog. It is a nullable VARCHAR(3)/C
domain, not a Boolean or unconstrained string alias. One genuine validated
`yes_or_no_check` recipe enforces `VALUE IN ('YES','NO')`; NULL passes UNKNOWN.
Qualified pg_catalog introspection preserves domain identity, immediate base1043,
base typmod7, C collation950, column typmod-1 and the actual constraint linkage.
Generated constraint identities are not promised equal to another server's OIDs.
Direct/prepared result metadata uses the physical base VARCHAR/string/length3.
Assignment `YESx` fails22001 before CHECK; `abc`, lowercase `yes`, empty string
and in-range `NO ` fail23514 with the domain/schema/constraint identity.
Excess assignment spaces after YES are trimmed by character sizing; explicit
casts can truncate YESx to YES before the same CHECK. Failed multirow INSERT
publishes no prefix. Native Dump/Reset/Restore retains the qualified declaration,
values/NULL, base metadata and both validations. Its implicit array remains
lookup-only; other predefined domains and general information_schema.columns/
domains providers are not enabled by this selected support. The existing native
table_privileges source keeps its documented primitive usages.

The predefined `information_schema.cardinal_number` likewise supports
consumer-declared columns, using the ordinary checked-domain path over INT4
(OID23, typmod-1, collation0). Its genuine validated
`cardinal_number_domain_check` binds the release expression `VALUE >= 0`;
0/7/2147483647 and NULL pass, negative values fail23514 with the qualified
domain and constraint diagnostic. Malformed integer text fails22P02 and
overflow fails22003; a failed multirow INSERT publishes no prefix.
Logical metadata retains the domain, while physical database/sql metadata is
INT4/int64 with unknown Length/DecimalSize, including empty/all-NULL results.
Native domain13243/namespace13229/companion13242 are allocated inventory
identities, not fixed release OIDs. Qualified native Dump/Reset/Restore retains
values, metadata and CHECK enforcement without recreating the builtin domain.
Its companion stays lookup-only; executable domain arrays, general
information_schema.columns/domains providers and migration of existing provider
carriers remain outside this selected support.

The predefined `information_schema.character_data` supports consumer-declared
columns as a nullable, unconstrained VARCHAR domain (base OID1043, typmod-1,
C collation950), without a CHECK or default. Direct/prepared input preserves
empty strings, significant trailing spaces, Unicode, long values and NULL.
Qualified regtype/pg_type/pg_attribute introspection retains the domain identity
and its base and companion relationships; allocated domain OIDs are not portable
constants. Physical results expose VARCHAR/Go string or nil, unknown DecimalSize,
native Length(MaxInt64,true) and declared Nullable(true,true); pq exposes
Length(-5,true) and Nullable(false,false), including empty/all-NULL selections.
Bound NUL and invalid UTF8 fail22021 with exact English encoding diagnostics;
failed multirow INSERT publishes no prefix. Qualified native Dump/Reset/Restore
retains values, NULL, metadata and input rejection without CREATE DOMAIN for
the builtin. Its companion remains lookup-only; general information-schema
views and existing provider carriers remain unchanged.

Direct/prepared/held reads follow existing catalog publication, transaction,
physical TEMP-session and native Dump/Reset/Restore lifecycles. Dumps contain
user DDL/data, not pg_type rows. Unknown columns retain42703/English messages
and character positions; OID syntax/overflow retain22P02/22003. Ordinary roles
cannot mutate pg_type (42501, `permission denied for table pg_type`).
Native privileged mutation remains0A000 `catalog source mutation is unsupported`;
virtual row locking remains0A000
`row locking is not supported for virtual catalog relations`. NAME/CHAR-to-TEXT
cast exclusions remain independent of catalog availability.

See [selected proof and feature smoke](../reference/catalogs/README.md#type-catalog-metadata-and-proof).

## pg_range supported range metadata

`pg_catalog.pg_range` is relation3541 in pg_catalog, owner10 (`postgres`
natively). It has exactly seven nonnullable columns in PostgreSQL18 order:
OID `rngtypid`, `rngsubtype`, `rngmultitypid`, `rngcollation`, `rngsubopc`,
then REGPROC `rngcanonical`, `rngsubdiff`. Each has typmod−1, collation0 and
four-byte type width. Row type10101 and array10100 retain reciprocal ownership.
It is an indexed catalog table in pg_class/pg_tables, not a view; this reports
released index metadata without synthesizing physical native index relations.
The bounded pg_type source exposes actual executable range families and arrays.

The complete inventory is one row per executable builtin base range:

| Range | Type | Subtype | Companion | Collation | Subtype class | Canonical | Difference |
|---|---:|---:|---:|---:|---:|---:|---:|
| int4range | 3904 | 23 | 4451 | 0 | 1978 | 3914 | 3922 |
| numrange | 3906 | 1700 | 4532 | 0 | 3125 | 0 | 3924 |
| tsrange | 3908 | 1114 | 4533 | 0 | 3128 | 0 | 3929 |
| tstzrange | 3910 | 1184 | 4534 | 0 | 3127 | 0 | 3930 |
| daterange | 3912 | 1082 | 4535 | 0 | 3122 | 3915 | 3925 |
| int8range | 3926 | 20 | 4536 | 0 | 3124 | 3928 | 3923 |

Zero collation/canonicalization is a typed nonnull zero, never SQL NULL;
REGPROC zero displays `-`. Discrete integer/date canonical routines correspond
to actual stored canonical bounds. Continuous numeric/timestamp families retain
their bounds and have no canonical routine. Every difference routine identifies
two subtype arguments and float8 result through existing REGPROCEDURE lookup.
The linked canonical/subdiff names remain lookup-only natively: they do not
create executable named functions or pg_proc provider rows. The class link is
the subtype's existing btree class, not a range index class.

Temporal companions4533/4534/4535 retain their released nonzero identities but
remain lookup-only; catalog metadata grants no codec/storage capability.
Arrays, multiranges, domains over ranges and range-bearing columns do not
acquire separate pg_range rows. The bounded logical GiST `range_ops` provider
supports range exclusion overlap. User-defined range DDL, constructors, named
canonical/subdiff execution and general range GiST/SP-GiST/hash/btree providers
are not added.

PUBLIC SELECT covers direct, prepared and held reads, including empty/NULL
parameters. Unordered results are bags. Native INSERT/UPDATE/DELETE reject
even false predicates with `0A000` / `catalog source mutation is unsupported`;
row locks reject with `0A000` /
`row locking is not supported for virtual catalog relations`. PostgreSQL
ordinary-role writes instead raise `42501` / `permission denied for table pg_range`.
Missing columns raise `42703` / `column "missing" does not exist` (Position8,
or14 after `/*é*/`). Invalid OID literal `bad` raises `22P02` /
`invalid input syntax for type oid: "bad"`; literal4294967296 raises `22003` /
`value "4294967296" is out of range for type oid`, Position8 in the selected
literal projection. Prepared analysis errors occur at prepare; native mutation
UPDATE rejects at prepare, INSERT/DELETE and row locks at execute.

Native catalog rows regenerate after Reset/Restore and never appear as user
INSERTs in logical dumps. Held numeric statements read the fresh generation;
already populated unread cursors retain their original rows/types. Ordinary
context cancellation remains context.Canceled, not a fabricated SQLSTATE.
See [independent selected proof and actual feature smoke](../reference/catalogs/README.md#pg_range-selected-proof).

## pg_aggregate existing aggregate definitions

`pg_catalog.pg_aggregate` is PUBLIC-readable relation2600, a persistent table
with logical index metadata, not a view or physical PostgreSQL storage. It has
no row `oid`: `aggfnoid` identifies the owning aggregate signature. It reuses
the existing native row/array types and ordinary SELECT/prepared/view pipeline.

The complete column order/types are:

| Columns | SQL types |
|---|---|
| aggfnoid, aggkind, aggnumdirectargs | REGPROC, internal CHAR, INT2 |
| aggtransfn, aggfinalfn, aggcombinefn, aggserialfn, aggdeserialfn | REGPROC each |
| aggmtransfn, aggminvtransfn, aggmfinalfn | REGPROC each |
| aggfinalextra, aggmfinalextra | BOOL each |
| aggfinalmodify, aggmfinalmodify | internal CHAR each |
| aggsortop, aggtranstype, aggtransspace | OID, OID, INT4 |
| aggmtranstype, aggmtransspace | OID, INT4 |
| agginitval, aggminitval | nullable TEXT each |

The first20 fields are NOT NULL; seed TEXT uses C collation950. Optional
routine0 is a nonnull REGPROC rendered `-`; NULL seeds differ from empty text
and text `0`. Definitions preserve released kind/direct-argument counts,
moving/final/parallel helper identities, modification modes, state types/sizes,
sort operators and seeds. Such metadata does not add execution of those helpers,
moving/parallel aggregation or new aggregate input types.

Builtin inventory contains exactly144 already implemented signatures across
count, average/sum/extrema, Boolean/bitwise, array/string/range/JSON, statistics,
regression, ordered-set and hypothetical-set families. Polymorphic declarations
contribute one row each, not one per concrete invocation type. Unsupported
overloads, scalar/window functions and helper routines contribute no rows.
Future aggregate support is not a prerequisite for catalog access. Existing
builtin `pg_proc` SQL projection remains incomplete; catalog capture does not
fabricate callable descriptors to make every builtin join succeed.

Installing intagg adds exactly its actual `int_array_aggregate(integer)` owner
row, with its allocated `int_agg_state(internal,integer)` and
`int_agg_final_array(internal)` metadata identities and no extra moving/combine/
serialization declarations. Those helpers are distinct from builtin array
kernels; the enum is not an aggregate. Off-path/quoted namespaces affect symbolic
spelling, not metadata visibility or permission to execute helpers.
Actual intagg `pg_proc`, qualified routine aliases and `aggfnoid` share identity.

`CREATE AGGREGATE` adds its actual owned ordinary definition, with the same
`pg_proc`/`aggfnoid` identity, real transition routine, concrete state type,
original extracted seed text and int32 space estimate. Unused optional helpers
remain routine0; no callable helper descriptor is invented for a catalog join.

Ordinary-role INSERT/UPDATE/DELETE and existing MERGE/writable-CTE routes fail
with42501 `permission denied for table pg_aggregate`; TRUNCATE has42501
`permission denied: \"pg_aggregate\" is a system catalog`. Held commands recheck
the effective role at execution; false predicates still deny, before row
expressions cause side effects. PostgreSQL superusers can mutate system tables,
but native virtual storage remains immutable and rejects superuser mutation
with0A000 `catalog source mutation is unsupported`. This bounded distinction
does not change mutation policy for other catalogs. Row locking retains its
separate native unsupported boundary.

Semantic binding, parameter admission and existing effect-free planning/coercion
preflight precede this rejection: invalid nested constant or imported-frame
conversions retain their errors before ordinary permission denial. Authority is
refreshed after planning; volatile row expressions remain unevaluated on denial.

Captured Rows/types/helper spelling remain stable across owner removal and
Reset/Restore. Held readers recapture current owners. Symbolic native dumps
serialize actual provider ownership and admitted stored values/defaults/views,
never INSERTs into virtual `pg_aggregate`; restored helper identities remap with
their real aggregate and receiving namespace. Failed/canceled controls preserve
the prior catalog and user data.
See [selected proof and smoke](../reference/catalogs/README.md#pg_aggregate-oracle-first-contract).

## pg_am supported access methods

`pg_catalog.pg_am` (relation2601, row10015/array10014) exposes four nonnull
fields in PostgreSQL order: `oid OID`, `amname NAME`, `amhandler REGPROC`,
`amtype` internal `"char"` (not SQL CHAR(1)). NAME uses C collation950;
all typmods are -1. `pg_class`, `pg_attribute` and `pg_tables` describe the
represented permanent indexed catalog table, without inventing backing-index
relations. It is not a `pg_views` entry.

The native bootstrap inventory is heap2/handler3/type`t`, btree403/330/`i`,
hash405/331/`i` and GiST783/332/`i`; user-defined methods extend the live catalog.
Ordinary symbolic bootstrap handler output is `heap_tableam_handler`, `bthandler`,
`hashhandler`, `gisthandler`; numeric `amhandler::oid` retains the same identities.
The existing REGPROC output
authority resolves names per statement; cells are not preformatted TEXT.
Methods exist before any user index and remain after indexes are dropped.
GiST represents existing INET/range exclusion execution, not a physical tree.
GIN2742, SP-GiST4000 and BRIN3580 remain absent until their features exist.

PUBLIC SELECT works for ordinary roles. Valid mutations, including false
predicates and held commands prepared before SET ROLE, check the current
effective actor after effect-free planning. Ordinary DML returns42501
`permission denied for table pg_am`; TRUNCATE returns42501
`permission denied: "pg_am" is a system catalog`. Native superuser mutation
returns0A000 `catalog source mutation is unsupported`; virtual row locking
retains its existing0A000 boundary. Earlier column/type failures retain their
SQLSTATE, English message and diagnostic fields.

Direct, freshly prepared and held queries use the existing query pipeline,
including typed empty/NULL results. Captures retain generation-owned values
and descriptors; held readers rebind after native Reset/Restore. Logical dumps
contain real user data/views, never catalog INSERTs. Failed/canceled staged
Restore preserves the prior catalog and user data. Native controls do not
claim PostgreSQL physical durability, storage layout or pg_dump equivalence.
Handler identities do not make handlers scalar-callable or add physical
PostgreSQL heap/btree/hash/GiST algorithms.
See [selected proof and smoke](../reference/catalogs/README.md#pg_am-selected-proof).

`CREATE ACCESS METHOD name TYPE INDEX|TABLE HANDLER [schema.]function` and
`DROP ACCESS METHOD [IF EXISTS] name [RESTRICT|CASCADE]` use a database-global
superuser-only definition, not a schema/owner/ACL object. Exact `(internal)`
signature and INDEX/TABLE result-kind validation precede finite native capability
selection. Stock btree/hash/GiST handlers select existing logical index mechanics;
heap_tableam_handler resolves the existing ordinary heap's concrete visibility
operations over actual storage versions, distinct from index mechanics.
Ordinary source selection binds captured heap2/handler3 once, outside its row
loop; default heap visibility uses the same single liveness rule.
No arbitrary C-ABI loader is supplied.
Valid but unregistered native handlers return0A000
`access method handler has no registered native implementation`.

Custom B-tree defaults/classes/families are separate SQL identities: builtin
int4_ops cannot stand in for a custom-method class. Empty family/class trees
auto-drop under RESTRICT; actual index consumers block, and CASCADE preserves
the parent table/data. Dump quotes custom method names and emits no AM owner
directive. Restore rebinds actual destination handler/member capabilities.
Capability diagnostics retain the declared custom method name while restrictions
dispatch on physical mechanics. Classless custom GiST ordering deliberately
retains native early0A000; PostgreSQL first returns the missing-default42704 and
standard Hint. Registered descriptors take precedence over unsupported gin/brin
spellings in exclusion admission; only unresolved native names retain that boundary.

TABLE definitions support catalog/dump/restore/drop only: `CREATE TABLE USING`,
`ALTER TABLE SET ACCESS METHOD`, `default_table_access_method` and custom TABLE
DML are not added. Ordinary table relam remains2. TABLE-as-INDEX is rejected
before operation dispatch with native55000; pinned PG18.6 instead reportsXX000
from its attempted index-handler struct interpretation. AM property inquiries
read current descriptors: missing native GIN/SP-GiST/BRIN identities return NULL,
not static capabilities for absent methods. Existing builtin B-tree/hash/GiST
property values remain unchanged.


## pg_opclass supported operator classes

`pg_catalog.pg_opclass` is a read-only PUBLIC catalog with the released nine
nonnull columns: OID `oid`, OID `opcmethod`, NAME `opcname`, OID
`opcnamespace`, OID `opcowner`, OID `opcfamily`, OID `opcintype`, BOOL
`opcdefault`, OID `opckeytype`. Relation2616 reuses row type10013; NAME has
C collation950. Captured results retain their generation, while held numeric
parameter queries read fresh owned descriptors after native replacement.

The bootstrap inventory contains63 implemented classes:31 btree403,30 hash405
and two feature-local GiST783 classes inet_ops/range_ops, independent of user
indexes. Historical seed identities were grounded on PostgreSQL18.0 UTF8/C;
class DDL and its current diagnostics are grounded on pinned PostgreSQL18.6.
VARCHAR aliases use TEXT25 input and are nondefault; pattern aliases are nondefault. Btree name_ops
has keytype cstring2275, versus hash keytype0. Concrete enums share anyenum3500
input; domains and object aliases reuse their original provider classes.
Existing JSONB hash uses class10089/family4034, distinct from btree10088/4033.

Recognized diagnostic-only array/record/range/multirange/bit/varbit/pg_lsn/
tsquery/tsvector classes are not advertised without executable method providers.
Hash array inputs and btree NAME[] inputs (including domains) retain native0A000
after collation validation and before class lookup, without diagnostic-class
fallback. Other btree arrays retain missing-default42704. No
gin/spgist/brin provider, direct catalog writes or
pg_index.indclass/vector projection is added. Existing bounded GiST exclusion
providers and the represented pg_am source do not claim physical index storage.
Full diagnostic and lifecycle expectations and actual feature smoke selectors
are in [the catalog proof](../reference/catalogs/README.md#pg_opclass-selected-proof).

TID scalar values own real default tid_ops btree10050/family2789 and
hash10055/family2227 providers. Values canonicalize as unsigned `(block,offset)`
and compare by numeric unsigned components. This supports declared TID keys, not
physical `ctid` source population or system-column indexing. TID arrays,
binary COPY and development-only TID functions remain outside this scalar scope.

## Operator families and registered members

The implemented btree403, hash405 and GiST783 methods support operator-family
CREATE/ALTER/DROP, member ADD/DROP, rename, owner and schema changes. Family
names are keyed by namespace and method; an empty or incomplete family is legal.
CREATE and member ALTER require superuser; other mutations use ordinary owner,
schema and role authority. Native family utilities remain excluded in explicit
transactions and reject execution in read-only transactions.

The read-only PUBLIC `pg_opfamily`, `pg_amop` and `pg_amproc` sources expose
the released five/nine/six columns. Support references are REGPROC, not OID;
formal routine arguments and associated member types are distinct. The compiled
PostgreSQL18.6 subset has49 families,292 operator rows and185 support rows,
including all observed cross-type and optional registrations. Btree support
numbers are1–6, hash1–3 and GiST1–12; strategy limits are5/1/32767.

Original bootstrap objects are pinned even when their dependency classification
is loose. New classless members have AUTO family/implementation dependencies;
family removal preserves the underlying operators and routines. ADD-time
signature checks do not impose full opclass completeness or callback execution.
Existing class consumers bind registered support once; held metadata reads
fresh rows after member replacement, while retained rows and roots stay
immutable. There is no blanket warmed-opclass/root invalidation promise.
CREATE/ALTER/DROP OPERATOR CLASS now consumes this same family authority.
Explicit/default selection uses current descriptors and exact access-method OID,
not only the physical enum or a compiled class name. DEFAULT is unique across
namespaces per method/input type. Implicit families are created or reused in the
class creation namespace; STORAGE=input normalizes to0. Full-list admission and
member storage publish atomically. Incomplete class definitions are admitted,
but actual indexes require real registered executable support. ALTER changes
name/schema/owner without moving the family. Index→class NORMAL and member→class
INTERNAL dependencies drive RESTRICT/CASCADE through the common graph; dropping
a class preserves its family and underlying operators/routines.

Native class Dump/Reset/Restore retains actual relationships, seed rename/schema/
owner deltas and current names. Foreign Restore remaps user identities and rebuilds
support from destination reads, never source captures. Real user enum column
remapping is distinct from the polymorphic anyenum3500 class input/member types.
Custom support binding does not expose new scalar SQL functionality or arbitrary
user callback execution. See [operator-class design](design/PG18-INDEX-OPCLASS.md)
and [selected proof](../reference/indexes/README.md#operator-class-definitions).

Native Dump/Restore uses ordinary dependency events, current names and fresh
destination identities for custom families/members. See the
[design](design/PG18-INDEX-OPERATOR-FAMILY.md) for the authority split and proof.

## Collation, encoding and indexes

REGCOLLATION identifies existing encoding-applicable collation catalog objects,
including represented providers whose comparisons remain unavailable. Symbolic
output uses the effective collation path and visibility; alias lookup is not a
comparison-provider capability grant. REGNAMESPACE identifies one schema name,
independently of search path or USAGE, and does not expand `pg_temp`.
Both accept unsigned numeric identities without requiring an existing referent.
Explicit string-category IO covers TEXT, CHARACTER, VARCHAR and NAME, not
internal CHAR or unrelated object aliases. Character sizing and cast strengths
remain unchanged. Nil byte parameters are SQL NULL; nonnil empty bytes are
malformed symbolic input.
See [catalog-valued types](TYPES.md#catalog-valued-and-supporting-types).

Collation execution uses deterministic C/POSIX and registered fixed builtin
providers. Arbitrary libc locale data, ICU, nondeterministic equality and fallback
providers are unavailable. Unicode-collation ILIKE retains its native exclusion;
ASCII behavior must not silently stand in for that provider. Encoding conversion
uses registered real release routines and current catalog defaults, not arbitrary
pathfinding or an implicit UTF8 pivot; the database encoding remains UTF8.
CREATE/ALTER/DROP CONVERSION, ownership, schema moves and directed default lookup
are supported. Arbitrary function definitions/external conversion ABIs and wire
transcoding remain excluded. REGPROC and REGPROCEDURE admit named/signature input,
numeric identities, symbolic output and ordinary storage through the existing
type owners. Their complete source-fixed identity inventory is lookup-only:
it neither expands pg_proc SQL rows nor authorizes execution. Basic scalar SQL
CREATE FUNCTION is separate; CREATE PROCEDURE remains unavailable.
Runtime-allocated information_schema/plpgsql routines are an
unavailable native catalog capability, not fabricated fixed OIDs. Schema USAGE
grants are supported independently of schema ownership. Native ABI,
external procedural runtimes and PostgreSQL physical persistence are not supplied
by routine alias lookup. See [the evidence note](design/regproc-pg18-oracle.md).
REGOPER/REGOPERATOR provide the complete released builtin operator identity
inventory, exact signature lookup and captured symbolic output, including
lookup-only operand type identities. The `pg_catalog.pg_operator` projection
exposes all 15 released columns for 545 scalar-executable exact overloads plus
one exclusion-only INET `&&` provider, not all 799 aliases. Operand/result
declarations remain exact, including generic
polymorphic identities; implementation/estimator fields use REGPROC and preserve
zero `-` and valid identity-only references without requiring SQL pg_proc rows.
See [released admission evidence](design/pg-operator-pg18-oracle.md).
CREATE/ALTER/DROP OPERATOR use the same staged catalog, namespace and ownership
rules as other schema utilities. Binary and prefix definitions bind an exact
existing function signature and execute that function's real registered provider;
the symbolic spelling is not an arithmetic implementation. Forward commutator
and negator references create catalog shells that can later be completed without
changing identity. Operators support owner/schema changes, reciprocal links,
selectivity metadata and enable-only HASHES/MERGES options. Selectivity metadata
does not add optimizer execution, opclasses or physical index access methods.
Definition options retain explicit presence and argument kinds. Every recognized
CREATE element is extracted in source order before valid duplicates use their
last value; superseded valid names are not looked up. String names and type names
are single literal components, not SQL to reparse (`'pg_catalog.int4'` does not
qualify `int4`). HASHES/MERGES accept bare flags, integer 0/1 and exact
case-insensitive true/false/on/off, not Boolean-input abbreviations or yes/no.
ALTER first extracts every non-NULL supplied value as a name, so numeric flag
arguments are invalid there. Bare/unquoted NONE clears RESTRICT/JOIN, enables a
flag, and is invalid for COMMUTATOR/NEGATOR; quoted/literal none is a real name.
CREATE requires FUNCTION before operand lookup, resolves both surviving operand
identities before missing/postfix checks, and checks type USAGE afterward.
Schema rename and operator SET SCHEMA preserve stored-definition dependencies.
Known allocated operator aliases replay by qualified exact signature, including
mixed nested alias containers; fixed builtin and unknown numeric identities keep
their bits. Native dumps reconstruct definitions, owners, links and shells before
dependent expressions. An orphan shell uses transient genuine generator DDL;
if no compatible real provider exists, Dump fails before writer I/O.
There is no ALTER OPERATOR RENAME, to_regoper/to_regoperator implementation or
physical planner storage. Scalar SQL routines do not supply an external operator ABI.
Built-in operator mutation remains an explicit native 0A000 boundary.
The existing read-only-first and explicit-transaction schema-DDL boundaries apply.
Arbitrary symbolic and explicitly qualified `OPERATOR(schema.symbol)` expressions
admit selected function operands. This does not promote standalone selected
calls into restricted INSERT cells, direct casts or ordinary arithmetic operands.
Those consumers retain their existing grouping and nested-call boundaries; use
explicit `OPERATOR(schema.+)` for a custom `+` with direct selected-call operands.
The unquoted word `operator` remains an ordinary identifier outside operator DDL
and the qualified operator opener.
See [user-operator testing contracts and selected proof](../reference/operators/README.md#testing).
REGCONFIG/REGDICTIONARY scalar numeric identities are executable, with lookup-only
companion arrays. Symbolic REGDICTIONARY resolution uses only actual installed
unaccent objects; native does not fabricate builtin `simple`/language dictionaries.
There is no native configuration provider: symbolic REGCONFIG input explicitly
reports `0A000: text search configuration name lookup is not supported`, rather
than claiming a PostgreSQL object is missing. Numeric and `-` config input bypass
that provider boundary. General text-search DDL, full-text execution,
to_regconfig/to_regdictionary and two-argument unaccent remain excluded. Custom
dictionary DDL remains PostgreSQL-only; schema-USAGE grants are supported natively.
Conversion utilities retain the native explicit-transaction DDL exclusion at
Execute, not Prepare. Read-only execution reports `25006` before that exclusion
or object resolution. DROP retires the actual conversion provider atomically;
subsequent statements select fresh defaults while returned results retain theirs.

Index providers are not a general PostgreSQL optimizer or physical access-method
implementation. The bounded logical GiST provider supports exclusion constraints;
general GiST, SP-GiST, GIN and extension-backed providers are not implied by the
logical index capabilities. HASH retains one-key/no-INCLUDE limits and
excludes standalone partial indexes. Partial concurrent indexes and concurrent
DROP/REINDEX are unavailable. Native index metadata disclosure requires schema
visibility plus readable parent-table authority, unlike PostgreSQL's unfiltered
summary view. See [native index controls](DRIVER.md#b-tree-utility-preparation-and-controls).
