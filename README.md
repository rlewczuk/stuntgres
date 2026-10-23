# stuntgres

stuntgres is a process-local, embedded PostgreSQL-compatible Go `database/sql`
driver for unit tests and reusable test fixtures. It lets Go developers exercise
SQL-backed code without starting a database server or container. Production code
uses only the Go standard library. Requires **Go 1.26 or later**.

It implements a deliberately bounded SQL subset, not a complete PostgreSQL server.
The embedded driver remains process-local. The optional `cmd/stuntgres` executable
exposes the same engine through PostgreSQL simple and extended queries, prepared
statements, portals and streaming COPY, with password authentication and isolated
in-memory databases. Native SQL dumps provide explicit save/restore, not WAL or
crash durability. Use real PostgreSQL for features outside
documented [native compatibility limits](docs/DIALECT.md). Consult the
[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html) for SQL syntax.

## User-defined ordinary aggregates — change note

`CREATE AGGREGATE` now supports fixed zero/N-input ordinary aggregates with
`SFUNC`, `STYPE`, `INITCOND` and `SSPACE`. Typed transitions preserve strict NULL
and unseeded-state behavior, real support-routine identity and separate caller,
aggregate-owner and SQL-body authority. Ordinary-input/result collation and
quoted SQL-support input inheritance survive stored consumers and native replay;
frozen SQL-standard bodies retain creation-analyzed semantics. Owned definitions
and dependencies participate in logical replay; explicit routine ACLs retain
the documented Dump rejection. See the [execution contract](docs/QUERIES.md#user-defined-ordinary-aggregates),
[compatibility boundary](docs/DIALECT.md#user-defined-ordinary-aggregates) and
[selected proof](reference/aggregates/README.md#create-aggregate-contract).

## Geometric constructors and conversions — change note

Seventeen selected BOX/CIRCLE/LINE/LSEG/PATH/POINT signatures execute through typed
providers, including ordered/coincident segments, raw box diagonals, closed polygon
paths, coordinate copies and checked centers (polygon centers compute the full
circle conversion, including radius failures). The family also includes inscribed
circle-to-box, enclosing box-to-circle and two-point infinite-line construction.
Negative constructor radii are valid typed CIRCLE values; ordinary negative text/binary input stays invalid.
Native logical replay uses a closed constructor recipe for those values.
See [type semantics](docs/TYPES.md#geometric-constructors-and-conversions) and
[selected proof](reference/types/README.md#geometric-constructor-conversion-contract).

## Ordinary VACUUM — change note

Ordinary `VACUUM` now performs snapshot-safe logical dead-version retirement,
canonical-XID freezing bookkeeping and attached-index position maintenance.
Direct and held commands use MAINTAIN/ownership checks, real relation locks,
cancellation, logical statistics and authenticated progress visibility.
`pg_stat_all_tables`, `pg_stat_sys_tables`, `pg_stat_user_tables` and
`pg_stat_progress_vacuum` expose their PostgreSQL 18 column contracts.
SKIP_LOCKED applies both to explicit-target selection and maintenance locking.
Diagnostics follow selection then work phases; descendants retain captured
identities and receive fresh maintenance checks after their actual work lock.
Database and new-table freezing bounds account for already-assigned older
publishers, including tables created after a database-only pass. Logical import
initializes current live cardinality independently of reset scan, vacuum and
cumulative activity history.
Enabled FULL and ANALYZE remain explicit native capability errors; physical
pages, WAL, TOAST, buffer work and parallel workers are not fabricated.
See [native boundaries](docs/DIALECT.md#ordinary-vacuum) and
[selected proof and feature smoke](reference/catalogs/README.md#vacuum-oracle-first-contract).

## Domain default and nullability changes — change note

`ALTER DOMAIN SET/DROP DEFAULT` and `SET/DROP NOT NULL` update real domain
metadata in autocommit. Old child defaults remain independent creation-copied
snapshots; DROP clears the target rather than inheriting its parent's current
default. New NOT NULL validates stored NULLness atomically and owns a genuine
constraint identity, also used by CREATE and native replay. Repeated own SET
does not rescan legal already-domain NULLs.
Native logical replay preserves copied/cleared recipes and NN identities/names
with reminted OIDs. PostgreSQL's retained rewritten defaults and eliminated
child-view coercions are not native executable-cache semantics.
See [type contracts](docs/TYPES.md),
[prepared boundaries](docs/DRIVER.md#prepared-catalog-and-path-invalidation) and
[selected proof](reference/types/README.md#domain-default-and-nullability-alterations).

## POINT coordinates and predicates — change note

Builtin `point` accepts `(x,y)` and `x,y`, preserves binary64 coordinates and
displays `(x,y)` through the existing typed bind/result paths. Eight geometric
predicates include genuine `<>`/`!=`, but do not invent `=` or default ordering.
Zero-based coordinates return FLOAT8; immutable coordinate updates, exact
metadata and full-precision native logical replay use the shared engine seams.
See [type contracts](docs/TYPES.md#point),
[native boundaries](docs/DIALECT.md#point-geometric-boundary) and
[selected proof and smoke](reference/types/README.md#point-oracle-first-contract).

## Polygon vertex lists — change note

Builtin `polygon` now has immutable ordered float8 vertices, four documented
input forms, closed `((x,y),...)` output, typed NULL, string/byte binds and exact
metadata. `polygon ~= polygon` tests cyclic/reverse vertex sameness, not equal
area; `polygon @> point` and `point <@ polygon` include interior and boundary.
Unlike circle, PostgreSQL polygon has no ordinary equality/ordering predicates
or `area(polygon)` overload. Path remains a distinct concrete scalar; polygon
adds no path functionality.
Native logical Dump/Restore preserves values, defaults, CHECKs and views; this
does not implement physical storage, spatial indexes, arrays or binary codecs.
See [value contracts](docs/TYPES.md#polygon),
[native boundaries](docs/DIALECT.md#polygon-scalar) and
[selected proof and smoke](reference/types/README.md#polygon-scalar-contract).

## SHA password hashing — change note

Installed pgcrypto `crypt` now supports SHA256-crypt (`$5$`) and SHA512-crypt
(`$6$`), using standard-library SHA with bounded per-call scratch and cooperative
cancellation. `gen_salt('sha256crypt'|'sha512crypt'[, rounds])` defaults to5000
and accepts1000..999999999; maximum-round proof generates salts without hashing.
Direct crypt rounds clamp with PostgreSQL notices, unlike generator errors.
Existing overload identities, stored consumers and native controls are unchanged.
See [selected proof and smoke](reference/catalogs/README.md#sha-password-oracle-first-checkpoint).

## Earth domain storage — change note

`CREATE EXTENSION cube; CREATE EXTENSION earthdistance;` installs the ordinary
`earth` domain over the actual installed cube identity. Text values, bound
strings/bytes, nullable columns and native logical replay preserve cube shape,
signed zero, NaN and full precision. Three genuine ordered domain CHECKs admit
point dimensions at most three and the strict released relative surface tolerance.
This does not supply geographic helper functions or spatial operations.
See [earth values](docs/TYPES.md#earth-domain),
[native boundary](docs/DIALECT.md#earthdistance-domain-boundary) and
[selected proof](reference/types/README.md#earth-domain-contract).

## LSEG ordered endpoints — change note

Builtin `lseg` stores two ordered POINT endpoints. The four documented input
forms display as `[(x0,y0),(x1,y1)]` without sorting or BOX normalization.
Six strict scalar comparisons distinguish endpoint equality from epsilon-length
comparison; zero-based endpoint reads and writes retain ordinary NULL behavior.
Text binds/results, exact metadata and native logical replay share existing
typed-value paths. Spatial operators, default sort/hash/index authority,
companion arrays and binary codecs are not supplied.
See [value contracts](docs/TYPES.md#lseg),
[native boundaries](docs/DIALECT.md#lseg-scalar-boundary) and
[selected proof and smoke](reference/types/README.md#lseg-oracle-first-contract).

## Recursive views — change note

`CREATE RECURSIVE VIEW v(columns) AS SELECT ...` uses the existing recursive
CTE engine and ordinary stored-view lifecycle. The explicit nonempty column
list projects public outputs without removing hidden recursive UNION keys.
Definitions retain live physical dependencies across renames and native replay;
CREATE does not evaluate the body. Recursive CHECK OPTION suffixes are rejected.
See [query contracts](docs/QUERIES.md#recursive-views) and
[selected proof and smoke](reference/views/README.md#recursive-view-contract).

## PATH geometric scalar — change note

PATH columns, typed literals, casts and held string/byte/NULL binds retain an
immutable nonempty ordered point sequence and its open/closed flag. Canonical
output uses square brackets for open paths and parentheses for closed paths.
The five released scalar comparisons compare point counts only; they do not
grant generic sorting, grouping or indexes. Native logical Dump/Restore retains
full coordinate precision independently of display settings.
See [value contracts](docs/TYPES.md#path-transport),
[native limits](docs/DIALECT.md#path-geometric-boundary) and
[selected proof](reference/types/README.md#path-scalar-contract).

## Circle scalar — change note

Builtin `circle` columns, casts, string/byte binds, typed NULL, metadata and
binary COPY share a fixed center/radius datum and canonical `<(x,y),r>` output.
`radius(circle)` and the circle-only geometric predicates/distance retain their
released scalar signatures. The six ordinary comparisons compare area, not
center/radius identity; `~=` tests geometric sameness. These operators do not
enable default sorting, grouping, hashing or B-tree/hash indexes.
POINT, other geometry and circle arrays remain outside this provider.
See [value contracts](docs/TYPES.md#circle) and
[selected proof and smoke](reference/types/README.md#circle-scalar-contract).

## Citext scalar transport — change note

`CREATE EXTENSION citext` installs the registered 1.8 scalar provider. Columns,
literal/direct/held prepared inputs, typed NULL and metadata use the shared type
system. Original spelling, whitespace and Unicode decomposition survive output
and native Dump/Reset/Restore; input rejects invalid UTF8 and NUL, and declared
collations are validated normally. Assignment and explicit casts retain the
released string-category rules, including BOOL and INET's preferred conversions.
This does not implement case-insensitive operators, indexes or citext arrays.
See [native limits](docs/DIALECT.md#citext-extension-type) and
[selected proof and smoke](reference/types/README.md#citext-storage-and-value-round-trips).

## LINE geometric scalar — change note

LINE retains binary64 coefficients `{A,B,C}` or constructs them from two
distinct points through the ordinary typed SQL/bind/result pipeline. Coefficient
input preserves scale and signed zero; endpoint input uses PostgreSQL's slope
construction. Real scalar `=` compares proportional coefficients with released
geometric tolerance, without granting generic sort/group/index authority.
Typed NULL, LINE metadata and full-precision native logical Dump/Restore share
the existing adapters and storage. See [typed transport](docs/TYPES.md#line-transport),
[native limits](docs/DIALECT.md#line-geometric-boundary) and
[selected proof](reference/types/README.md#line-scalar-contract).

## BOX geometric scalar — change note

BOX stores real binary64 opposite corners, normalizes ordinary input to
upper-right/lower-left coordinates, and exposes two real POINT components.
Its five scalar comparisons compare area with PostgreSQL's geometric tolerance;
`~=` compares coordinates. These operators do not grant generic ordering,
grouping, indexing, or `<>`/`!=`. Component assignment retains raw pair order
without renormalization, including through native Dump/Reset/Restore.
See [typed transport](docs/TYPES.md#box-and-point-transport),
[native boundaries](docs/DIALECT.md#box-geometric-boundary), and
[selected proof and smoke](reference/types/README.md#box-scalar-contract).

## Large-object page catalog — change note

`pg_catalog.pg_largeobject` exposes genuine stored `(loid,pageno,data)` rows.
Pages are2048 bytes at most; empty objects and missing sparse pages contribute
no rows, and short stored pages retain their exact binary length and bytes.
The catalog is protected by default: LO ownership or SELECT does not grant
table access. Explicit table/column SELECT grants use ordinary catalog ACL
authority independently of object privileges, including held-query rechecks.
Reads retain their active query snapshot; fresh VOLATILE SQL bodies can see
argument-side LO effects without changing the outer snapshot. Native replay
reconstructs pages from the existing LO state, not a second serialized catalog.
See [selected proof and smoke](reference/catalogs/README.md#pg_largeobject-page-projection).

## Tablespace inquiries — change note

`pg_tablespace_location(oid)` returns empty text for the builtin tablespaces
and the native default (`0`); `pg_tablespace_databases(oid)` reports the genuine
current attached database's logical occupancy. Typed NULLs, held statements,
set-returning composition, warnings and ordinary EXECUTE admission use the
existing typed execution path. External directory inspection remains an explicit
non-engine boundary, not an invented path or cluster database inventory.
See [native limits](docs/DIALECT.md#tablespace-inquiries) and
[selected proof](reference/catalogs/README.md#tablespace-inquiry-selected-proof).

## Partition navigation — change note

`pg_partition_tree(regclass)`, `pg_partition_ancestors(regclass)` and
`pg_partition_root(regclass)` navigate the captured table or attached-index
hierarchy through the ordinary typed SQL path. Subtrees retain their actual
immediate parent while resetting levels; childless partitioned objects are not
leaves. Held statements see fresh attachment/detachment state, and logical
Dump/Restore preserves the hierarchy rather than cached inquiry results.
See [semantics](docs/DIALECT.md#partition-navigation) and
[selected proof and smoke](reference/catalogs/README.md#partition-navigation).


## Partition pruning and constraint exclusion — change note

Source-local filters conservatively omit irrelevant RANGE/LIST/HASH physical
branch intervals and inheritance branches refuted by validated CHECK/NOT NULL
facts. Selection uses captured typed metadata and each execution's imported
parameters; held statements do not cache it. Retained candidates still run the
original predicate, preserving duplicate bags, NULL/default semantics, result
metadata and errors. Admission/locks can continue to cover the full hierarchy.
Boolean proof search has a fixed allowance shared across the whole selection;
exhaustion retains unproved branches and still applies the original predicate,
rather than introducing a SQL error or extending exponential backtracking.
There is no EXPLAIN shape/cost, Boolean evaluation-order, new planner-setting,
FDW pushdown or physical PostgreSQL persistence promise.
See [table contracts](docs/TABLES.md#inheritance-typed-attachments-and-partition-graphs),
[native boundaries](docs/DIALECT.md#tables-hierarchies-and-materializations)
and [selected proof/smoke](reference/tables/README.md#partition-pruning-and-constraint-exclusion).

## Partitioned CHECK and NOT NULL constraints — change note

Partition CHECK/NOT NULL facts retain exact local and immediate inherited
provenance through creation, selected ALTER and logical Dump/Restore. Recursive
ADD CHECK, ADD NOT NULL, SET/DROP NOT NULL and named CHECK/NOT NULL DROP use one
atomic statement stage across RANGE, LIST and HASH trees. Matching structural
CHECKs merge as nonlocal facts; NOT NULL merges by column identity and retains
an existing local child declaration. A merged action stops at that node without
stopping other actions in the list. Existing SET NOT NULL localizes its fact.
ONLY DROP localizes immediate surviving child facts; fresh ONLY ADD/SET with
children is rejected. Different explicit CREATE child NOT NULL names are retained,
while incompatible different ALTER ADD names fail with55000. The same shared
ADD NOT NULL naming rule now applies to ordinary tables: a different explicit
name cannot silently reuse an existing constraint. Existing NO INHERIT and
NOT VALID compatibility errors retain their diagnostic precedence.
Virtual roots and intermediates stay storage-free in the common ALTER pipeline;
each visited physical descendant retains its own historical-row validation and
storage publication fences in the same statement journal.

Physical leaves enforce CHECK FALSE and NOT NULL independently, including routed
and prepared inserts. Failed validation, ownership admission or cancellation
publishes no prefix. Native controls are logical process-local replacement, not
PostgreSQL disk/WAL durability. Generic mixed hierarchy ALTER, column/type/rename
propagation, UPDATE/DELETE and explicit hierarchy locking are not added.
See [native limits](docs/DIALECT.md#tables-hierarchies-and-materializations) and
[selected proof and smoke](reference/tables/README.md#inherited-partition-constraints).

## Installed pgrowlocks — change note

`CREATE EXTENSION pgrowlocks` installs the superuser-required, relocatable 1.2
provider. `pgrowlocks(text)` returns real row-version lock observations:
`locked_row TID`, `locker XID`, `multi BOOL`, `xids XID[]`, `modes TEXT[]`,
and `pids INT4[]`. Direct/prepared calls, FROM, target SRFs and stored views
use the ordinary installed-routine identity and EXECUTE authority. NULL is
strict and returns no rows with the complete result shape.

The scan joins the active MVCC-visible versions to actual lock history, not
waiters or synthetic rows. Raw MultiXact bags can retain ended members with
PID zero. Logical TIDs identify native versions, not PostgreSQL heap locations
or a new `table.ctid` system column. See the [native boundaries](docs/DIALECT.md#installed-pgrowlocks)
and [selected proof and smoke](reference/locking_transactions/README.md#installed-pgrowlocks).

## Transactional large-object storage — change note

`lo_create`, `lo_creat`, `lo_from_bytea`, both `lo_get` overloads, `lo_put`
and `lo_unlink` operate on real process-local sparse bytes. Fixed OIDs are
class-local, gaps read as zero, writes preserve existing suffixes, and object
metadata/content publish together through transactions and savepoints.
Reads require object SELECT; writes require SELECT and UPDATE; unlink requires
ownership. Table grants do not authorize large-object bytes. Native
Dump/Reset/Restore preserves fixed identity, owner, sparse bytes and exact raw
ACLs, including NULL versus empty arrays and entry/grantor/option ordering.
There is no durability, descriptor client API, import/export, truncate or
standalone ALTER LARGE OBJECT OWNER support.
See [native limits](docs/DIALECT.md#transactional-large-object-bytes),
[authorization](docs/AUTHORIZATION.md#large-object-authority),
[replay](docs/FIXTURES.md#large-object-snapshots), and
[selected proof/smoke](reference/object_grants/README.md#transactional-large-object-byte-storage).

## Routine sequence usage — change note

`information_schema.routine_sequence_usage` exposes one row per actual direct
SQL-standard routine/sequence dependency, with nine nullable `sql_identifier`
columns and no NULL placeholder rows. Repeated references, multiple sequence
columns and mixed direct calls/column reads collapse to one containing-sequence
pair; quoted SQL bodies and transitive calls do not manufacture hard sequence
edges. Rows use current routine/sequence names and suffix-preserving specific
identities.
Visibility follows enabled sequence-owner roles only, not routine EXECUTE,
schema USAGE or sequence ACLs. Fresh direct and prepared executions reflect
rename, schema move, ownership and dependency retirement through existing catalog
and role captures. This adds no adjacent usage views, parameter defaults,
OR REPLACE or ALTER routine support.
See [native boundaries](docs/DIALECT.md#routine-sequence-usage) and
[selected proof](reference/catalogs/README.md#routine-sequence-usage).

## Information-schema user mappings — change note

`information_schema.user_mappings` exposes actual SQL/MED mapping identities
through three nullable `sql_identifier` domain columns: authorization identifier,
current database and foreign server. Inherited server owners and effective
server-USAGE grantees see all mappings on that server, including other roles and
uppercase `PUBLIC`; mapping ownership alone grants no visibility. Each execution
uses current captured metadata and authority, including held prepared queries.
No options or secrets appear in this view, and existing `pg_user_mappings` option
redaction is unchanged. Metadata-only reads require no foreign provider or remote
connection; this does not implement `user_mapping_options`.
Supported server rename remains shared lifecycle coverage; native role-rename
SQL remains unsupported, and native SQL/MED CASCADE retires dependencies without
PostgreSQL's dependency notices. Current role-descriptor names still resolve by identity.
See [native boundaries](docs/DIALECT.md#information-schema-user-mappings) and
[selected proof and smoke](reference/catalogs/README.md#information-schema-user-mappings).

## Information-schema key column usage — change note

`information_schema.key_column_usage` exposes the documented nine columns with
genuine nullable identifier/cardinal domains and NAME/INT4 public result types.
Actual primary/unique/foreign-key definitions supply rows; foreign-key positions
follow their selected supporting index, preserving composite order and duplicate
constraint names across tables. Owner inheritance or qualifying per-column
privileges controls visibility without renumbering partially visible keys.
Existing constraint DDL, transactions and logical replay update the view.
No adjacent constraint views or extra referenced-object columns are implied.
See [native contract](docs/DIALECT.md#information-schema-key-column-usage) and
[selected proof](reference/catalogs/README.md#information-schema-key-column-usage).

```sh
make check RUN='^TestReferenceInfoKeyColumnUsageFeatureSmoke$'
make check RUN='^TestReferenceInfoKeyColumnUsageFeatureSmoke$' REFERENCE=1
```

## LIST partition definitions — change note

Single-key `PARTITION BY LIST` supports typed `IN` values, NULL ownership,
DEFAULT and nested trees, with real direct/prepared INSERT routing and parent
scans. Repeated values within one bound normalize; sibling overlap, occupied
DEFAULT and uncovered keys have PostgreSQL diagnostics and atomic failure.
Native metadata and logical Dump/Reset/Restore preserve typed ownership, frozen
bound values and descendant data. Public LIST ATTACH remains unavailable;
private validated replay may use it to reconstruct captured leaf-local state.
No new scalar provider, hierarchy ALTER or physical persistence is implied.
See [native boundaries](docs/DIALECT.md#tables-hierarchies-and-materializations),
[table contracts](docs/TABLES.md#inheritance-typed-attachments-and-partition-graphs),
and [selected checks and smoke](reference/tables/README.md#list-partition-definitions).

## Scalar SQL routines — change note

`CREATE FUNCTION` accepts named/anonymous IN, scalar OUT/INOUT and array VARIADIC
parameters, defaults, scalar/VOID results, quoted multi-command SQL and
definition-analyzed RETURN/BEGIN ATOMIC:

```sql
CREATE FUNCTION add1(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$;
SELECT add1(4); -- 5, INT4
```

Direct/prepared calls use ordinary signature resolution and live invoker/EXECUTE
checks. Named, routine-qualified and positional formals share private typed
slots, after real column lookup. Commands execute sequentially under the caller's
one atomic statement owner. Only the actual final SELECT or DML RETURNING supplies
a nonvoid result; terminal DML completes every write before returning the first
row. VOID completes all required output evaluation and yields SQL NULL.
CREATE validates without effects. Quoted bodies prepare reached commands at
invocation; standard bodies retain checked identities, selected overloads and
hard dependencies through existing relation/view renames and native replay.

Function calls support positional, named and positional-first mixed notation,
including named default gaps. Identity, `$n` and private frames count only
inputs; a pure OUT is omitted from actuals and INOUT belongs to both projections.
One OUT/INOUT implies its scalar result type. Expanded VARIADIC builds an owned
array; explicit VARIADIC preserves empty/NULL arrays, bounds and element NULLs.
Checked declaration defaults use Assignment conversion, capture hard dependencies
even with quoted bodies, and evaluate freshly only when omitted by a selected
call—not at CREATE/Prepare or while considering losing overloads.

`DROP FUNCTION`, `DROP PROCEDURE` and generic `DROP ROUTINE` select exact catalog
input signatures (or an unambiguous bare name), with grouped atomic deletion,
`IF EXISTS` notices and default `RESTRICT` / explicit `CASCADE`. Dependency
removal preserves unrelated overloads, rows and containing tables while removing
dependent views, defaults and constraints. DROP authority is routine or schema
ownership, not EXECUTE. Procedure declarations are supported; CALL remains separate.

Incidental quoted-body calls do not acquire hard dependencies on callees:
dropping a callee leaves the caller installed but unresolved until recreation.
Declaration-default captures remain hard dependencies regardless of body form.
Dump/Restore preserves that state; private replay defers only a structured terminal missing
call, never arbitrary SQLSTATE 42883. Parsing and closed formal bounds still
validate; checks prevented by the missing call run when invocation can bind it.

SQL procedure declarations share validation/catalog/persistence but cannot be
called as expressions; CALL execution remains separate. Native Dump/Restore
preserves body form, names, dependencies, callers and data, including text-body
default cycles. Multiple-output records, SQL CALL and PL copyback belong to
separate capabilities. OR REPLACE, ALTER routine lifecycle DDL, PL/pgSQL,
attributes/security clauses and set/composite returns remain excluded.
The bounded `pg_language` inventory is unchanged.
See [native boundaries](docs/DIALECT.md#scalar-sql-routines),
[fixture replay](docs/FIXTURES.md#sql-routine-declarations-and-callers), and
[selected proof and smoke](reference/query_core/README.md#scalar-sql-routine-contract).
Function-parameter proof and smoke are in
[the callable-resolution package](reference/operators/README.md#routine-parameter-binding).

## Ordinary inheritance maintenance — change note

`ALTER TABLE ... INHERIT` and `NO INHERIT` maintain ordinary direct ancestry
without moving rows, replacing compatible child facts, or copying physical
indexes. Compatibility uses actual column, NOT NULL and CHECK definitions;
detach preserves surviving contributions and localizes the last contribution.
Live parent ordinals may contain gaps. Mixed lists publish atomically through
the existing ALTER stage, with exact child/parent admission across lock waits.
Parent spellings, including absence, are observed before waits; lookup,
ownership and parent grants are consumed at each action's execution position,
so a later missing or unauthorized parent cannot preempt an earlier failure.
Historical NN/CHECK facts retained after a parent column drop survive last
detach and former-parent removal without keeping that parent alive, including
TEMP history retained across native replacement. Dump/Restore carries this
provenance separately from live ancestry.
See [dialect boundaries](docs/DIALECT.md#tables-hierarchies-and-materializations),
[descriptor and replay contracts](docs/TABLES.md#inheritance-typed-attachments-and-partition-graphs),
and [selected reference coverage](reference/tables/README.md).

## Inheritance catalog metadata — change note

`pg_catalog.pg_inherits` exposes the four PostgreSQL 18 fields for actual direct
ordinary-table, table-partition and partition-index ancestry. Numeric identities
match `pg_class`; ordinary parent ordinals preserve declaration order. Reads
follow transactional DDL, savepoints, drops and logical native replay without a
separate relationship registry. Held statements refresh on execution, while
already captured rows retain their values. No transitive links or physical
catalog indexes are invented; concurrent detach remains unsupported.
See [native boundaries](docs/DIALECT.md#tables-hierarchies-and-materializations),
[descriptor lifecycle](docs/TABLES.md#inheritance-catalog-projection), and
[selected proof and smoke](reference/catalogs/README.md#inheritance-metadata-contract).

## Non-table object revocation — coverage change note

Focused PostgreSQL18.6 reference scenarios now cover the existing object REVOKE
path: grantor-scoped ordinary versus grant-option removal, RESTRICT atomicity,
CASCADE, and surviving PUBLIC/inherited/independent grants. Same-held sequence
and parameterized routine consumers prove effective-access transitions; schema,
type/domain, database, parameter, SQL/MED and tablespace witnesses exercise real
uses. No new revoke engine or external provider is introduced. Language execution
and physical tablespace placement retain their native bounds.
See [native contracts](docs/DIALECT.md#object-privilege-grants) and
[selected proof and smoke](reference/object_grants/README.md#non-table-object-revocation).

## Text-search scalar storage — change note

`tsvector` and `tsquery` now use immutable typed scalar payloads for columns,
unknown/typed literals, string/textual-byte parameters, NULL and native logical
Dump/Restore. Vector output sorts/deduplicates lexemes and preserves positions
and weights; query output preserves Boolean/phrase structure, masks and prefix
flags without normalization or matching. Empty query input uses the ordinary
operation NOTICE channel. Query `&&`, `||`, prefix `!!`, `<->`, `@>` and `<@`,
both `tsquery_phrase` overloads, vector `||` and their genuine underlying
routines use immutable, cancellation-aware typed providers. Containment compares
exact lexeme sets, ignoring Boolean structure/weights/prefix flags. Phrase
distance defaults to1 and accepts0–16384; vector concat shifts right positions
by the global left maximum with released caps and first-saturation weight.
SQL NULL remains distinct from empty. Companion arrays, matching, constructors,
configuration, ranking and index execution remain outside this surface.
See [scalar contract](docs/TYPES.md#text-search-scalar-values),
[explicit boundaries](docs/DIALECT.md#text-search-scalar-boundaries) and
[selected proof and feature smoke](reference/types/README.md#text-search-scalar-io).

## Domain defaults and nullability — change note

`CREATE DOMAIN` supports variable-free `DEFAULT`, compatibility `NULL`, and
named or unnamed `NOT NULL`. Omitted/defaulted column values select the column
recipe, then the domain's inherited recipe, then fresh NULL; explicit DEFAULT
NULL masks an inherited default, while supplied NULL never requests a default.
Fresh conversions enforce ancestor NOT NULL with domain SQLSTATE23502 and empty
Constraint/Detail, including named declarations. Already-domain NULL retains the
documented conversion bypass. The existing `information_schema.time_stamp`
default uses transaction `CURRENT_TIMESTAMP(2)` and is inherited normally.
Native Dump/Restore preserves executable recipes, nullability and legally stored
already-domain NULLs, including nested carrier values and exact array bounds.
New defaulted inserts and fresh NULL enforcement still apply after restore.
Own/inherited composite-field defaults retain real attribute dependencies:
RESTRICT is atomic, CASCADE retires consuming objects, and surviving sibling
defaults keep their selected values. Native replay normalizes historical row
constructors to the current live shape without changing source metadata.
Composite ALTER dependency diagnostics retain the utility's captured namespace
visibility for the root, dependent details and cascade notices.
ALTER DOMAIN default/nullability changes and PostgreSQL pg_dump/physical
interoperability remain outside this feature.
See [domain semantics](docs/TYPES.md) and
[selected proof and smoke](reference/types/README.md#domain-defaults-and-nullability).

## Installed cube scalar storage — change note

`CREATE EXTENSION cube` installs the selected cube1.5 scalar text provider.
Declared columns, literal and held prepared bindings, NULL and CUBE/string
metadata use the shared type system. Text preserves corner orientation,
collapses coincident corners and supports zero through100 binary64 dimensions.
Native Dump/Reset/Restore preserves exact values independently of display
precision. Arrays, binary codecs and advanced cube operations remain excluded.
See [cube limits](docs/DIALECT.md#cube-extension-type) and
[selected proof](reference/types/README.md#cube-scalar-oracle-first-contract).

## Row-valued UPDATE assignments — change note

Ordinary UPDATE accepts constructor and subquery assignment groups. All members
read the old target row; a correlated source SELECT supplies its complete tuple
once per target occurrence. Zero source rows assign NULL to every member;
multiple source rows fail with SQLSTATE21000 without partial writes or RETURNING.
Constructor literals/parameters use destination contexts, whereas SELECT outputs
are typed independently. Prepared reuse, writable views, stored grouped rule
actions and native live-row Dump/Restore retain their normal ownership contracts.
PostgreSQL's restriction on substituting grouped-subquery outputs into rule NEW
consumers is preserved; constructors, OLD and unaffected NEW columns are distinct.
Native MERGE/ON CONFLICT row assignment and non-domain ordinary UPDATE DEFAULT
remain excluded. Domain-target DEFAULT uses the existing column override,
domain fallback or fresh domain NULL input, including constructor groups after
scalar SET items; supplied NULL does not request a default. Generated-column
DEFAULT admission is unchanged.
See [row UPDATE contracts and selected proof](reference/dml/README.md#row-valued-update-assignments).

## IP address formatting, masks and networks — change note

`abbrev(inet/cidr)`, `host`, `text(inet)`, `family`, `masklen`,
`inet_same_family`, `broadcast`, `hostmask`, `netmask`, `network`,
`inet_merge` and both `set_masklen(inet/cidr,integer)` overloads use the
existing typed network expression and routine-authorization paths. Masks
return full-width INET; network and merge return canonical CIDR. Prefix
rewriting preserves INET host bits but clears excluded CIDR bits, with -1
selecting the full address width. IPv4, IPv6 and mapped IPv6 preserve their
family semantics. Stored expressions replay symbolically through native
Dump/Restore. INET `<<`, `<<=`, `>>`, `>>=` and `&&` compare network prefixes,
ignoring host bits: strict containment excludes equal prefixes, inclusive
containment includes them, and overlap accepts either containment direction.
Mixed families return false; CIDR promotes implicitly to INET. XOR/integer
shifts, network arrays and OS networking remain excluded.
See [network transport](docs/TYPES.md#textual-structured-and-network-transport),
[native authority boundaries](docs/DIALECT.md) and
[selected proof and standalone smoke](reference/types/README.md#inet-mask-network-and-merge-contract).


## Comparison resolution and predicates — change note

Comparisons select PostgreSQL's operator-specific signatures and direct implicit
conversions without changing prepared source parameter identities. NAME/VARCHAR
catalog predicates retain full VARCHAR contents and explicit wire OID1043.
Ordinary composite comparisons now use logical field identity and NULL-field
ordering, distinct from row-constructor three-valued comparisons. Explicit
ASYMMETRIC, Boolean-domain truth tests and NULL-aware distinctness share the
existing expression and occurrence owners. Stored quantified ROW casts preserve
analyzed member coercions and real logical dependencies without retaining an
erased outer typename. Physical record-image operators remain outside the native
representation.
See [native boundaries](docs/DIALECT.md#comparison-resolution-and-predicates),
[the independent comparison ledger](reference/type_resolution/comparison-ledger.md)
and [real-wire proof](reference/sessions/README.md#comparison-resolution-and-fixed-wire-formals).

## SQL version and Unicode inquiries — change note

`version()`, `unicode_version()` and `icu_unicode_version()` are zero-argument
TEXT inquiries through the ordinary expression path. Native `version()` returns
`PostgreSQL 18.0 compatible; embedded stuntgres`, identifying compatibility
without claiming a native PostgreSQL server build. Unicode reports `16.0` from
the embedded Unicode16 provider; ICU reports SQL NULL because native execution
does not use ICU. Stored calls remain symbolic through Dump/Restore.
Machine-readable version settings and wire version reporting are unchanged.
See [compatibility boundaries](docs/DIALECT.md#sql-version-and-unicode-inquiries)
and [selected proof and smoke](reference/sessions/README.md#sql-version-and-unicode-inquiries).

## Catalog linkage and role names — change note

`pg_get_catalog_foreign_keys()` exposes the complete 219-link PostgreSQL18.0
catalog relationship bag with typed REGCLASS/TEXT[] fields and array/optional
flags, independently of the smaller set of queryable native catalog providers.
`pg_get_userbyid(oid)` resolves raw role names from the statement's captured
role authority, with typed NAME NULLs and PostgreSQL's unknown-OID spelling.
Held statements refresh on execution; stored calls remain symbolic through
native logical replay. See [catalog contract](docs/DIALECT.md#catalog-linkage-and-role-names)
and [selected proof and smoke](reference/catalogs/README.md#catalog-relations-oracle-first-contract).

## Transaction defaults and read-only temporary data — change note

The six current/default transaction settings share transaction and savepoint
ownership: LOCAL overrides expire at outer end, masked SESSION candidates survive
commit, and rollback-to restores the selected candidate. RESET ALL resets eligible
defaults without changing current modes; current-mode reset routes report `0A000`.
`SHOW TRANSACTION ISOLATION LEVEL` now aliases `SHOW transaction_isolation`,
including its canonical TEXT metadata and live current value through held
prepared handles; it introduces no separate setting state.
Read-only transactions now permit already-supported MERGE against their own
temporary tables, including through held prepared handles, while permanent MERGE
retains its existing error precedence and `25006` rejection.
SERIALIZABLE READ ONLY DEFERRABLE continues to use actual SSI safe snapshots,
not a settings-only guarantee. See [settings](docs/SETTINGS.md#transaction-and-identity-journals),
[read-only admission](docs/TRANSACTIONS.md#admission-and-transactional-utility-limits)
and [selected proof and smoke](reference/locking_transactions/README.md#transaction-settings-and-read-only-temporary-merge).

## Collation version inspection — change note

`pg_collation_actual_version(oid)` and
`pg_database_collation_actual_version(oid)` inspect the implemented provider's
actual version without refreshing recorded catalog versions. The default
collation follows the current database's provider. Native builtin versions are
`1`; the native libc/C database has no version. Unavailable native ICU version
inquiry returns TEXT NULL; ICU execution remains unsupported.
`pg_import_system_collations(regnamespace)` is strict and checks the current
effective superuser before numeric namespace existence. Valid native import
reports an explicit unsupported-provider error, never a fabricated zero count or
host inventory. See [native limits](docs/DIALECT.md#types-expressions-and-catalogs) and
[selected tests and smoke](reference/patterns_encoding/README.md#collation-management-functions).

## Transaction identity information — change note

`age(xid)`, `mxid_age(xid)`, `pg_current_xact_id()`,
`pg_current_xact_id_if_assigned()`, `pg_xact_status(xid8)` and
`pg_get_multixact_members(xid)` use actual transaction and physical-row lock/action
authorities. Read-only inquiry never allocates; explicit current-ID requests assign
a stable full top-level XID8, including within savepoints. Status and immutable
six-mode member bags survive ordinary completion and abort independently of
optional commit timestamp tracking. Native retention lasts until successful
engine replacement, not physical VACUUM/WAL truncation; no system columns or
PostgreSQL child-XID allocator are added.
See [compatibility boundaries](docs/DIALECT.md#transaction-identity-information)
and [selected proof and smoke](reference/locking_transactions/README.md#transaction-identity-oracle-first-contract).

## Partitioned exclusions — change note

Partitioned EXCLUDE declarations use virtual parent indexes and actual leaf
providers. Each declaration covers its target's and descendants' partition keys
with the partition family's actual equality operator and collation. Independently
local child exclusions do not acquire unrelated ancestor-key requirements.
The bounded logical GiST provider admits explicit INET `inet_ops WITH =` and
range `range_ops WITH &&`; it is not a physical GiST tree or `btree_gist`.
CREATE, selected EXCLUDE ADD/DROP, RANGE ATTACH/DETACH, public ancestry/operator
metadata and native Dump/Restore share the existing atomic hierarchy machinery.
PostgreSQL18.0 attachment clones fresh exclusion providers rather than adopting
equivalent local constraints; explicit exclusion index ATTACH fails42P17.
Native replay also preserves ONLY-created provider gaps and invalid virtual
indexes, including complete-invalid and childless states, without strengthening
the captured constraints.
Hierarchy UPDATE/DELETE, row movement, arbitration/MERGE and native LIST syntax
remain outside this feature. See [selected proof and smoke](reference/tables/README.md#partitioned-exclusions).


## Session messages — change note

Native `LISTEN`, `NOTIFY` (including payloads), and `UNLISTEN` now provide
transactional, same-engine local notifications. Receive on a pinned native
`*sql.Conn` with `stuntgres.WaitForNotification(ctx, connection)`: committed events
carry the full logical sender PID, channel and payload. Rollback drops outgoing
events; subscription changes apply at commit and physical retirement clears the
recipient. Notices are not repurposed. Network sessions receive committed
notifications through the same queue, including delivery while idle without an
extra query; wire delivery waits until the listener is outside a transaction.
See the [native boundary](docs/DIALECT.md#session-notifications),
[receiver lifetime](docs/DRIVER.md#local-session-notifications), and
[selected proof and standalone smoke](reference/sessions/README.md#session-messages).

## Aggregate definition catalog — change note

`pg_catalog.pg_aggregate` exposes all 22 PostgreSQL 18 definition fields for
exactly 144 implemented builtin aggregate signatures, plus the actual installed
intagg aggregate when present. REGPROC fields retain routine identity and symbolic
output; optional zero routines are `-`, while absent seeds are SQL NULL.
Catalog access does not require future aggregate implementations or a complete
builtin `pg_proc` projection. Installed intagg state/final helper identities are
extension-local lookup metadata, not new callable functions.
Ordinary-role DML reports PostgreSQL 42501/English diagnostics under the current
effective role; native superuser mutation remains explicitly unsupported.
See [native limits](docs/DIALECT.md#pg_aggregate-existing-aggregate-definitions)
and [selected proof and smoke](reference/catalogs/README.md#pg_aggregate-oracle-first-contract).

## Installed intagg — change note

`CREATE EXTENSION intagg` installs the two documented compatibility wrappers:
`int_array_aggregate(integer)` and `int_array_enum(integer[])`. They reuse
existing array aggregation and stored-element expansion, retaining duplicates,
NULL elements and installed procedure identity through native replay. Installation
is required; neither name is a builtin alias. No callable helper routines or new
array type are exposed. Metadata-only state/final routine identities belong to
the installed aggregate. Aggregate windows remain outside native execution.
See [native limits](docs/DIALECT.md#intagg-installed-wrappers) and
[selected proof and smoke](reference/binary_json_arrays/README.md#intagg-installed-wrappers).

## Live lock state — change note

`pg_catalog.pg_locks` exposes the PostgreSQL 16-column lock view through live
direct and prepared queries. Rows come from actual relation/type/advisory
ownership, transaction VXID/assigned-XID lifetimes, pending acquisitions and
retained relation-level SSI reads. Reentry is aggregated by resource and mode;
wait timestamps describe enqueue, not observation. PUBLIC SELECT includes
other roles' resource identities and wait details even without source-table
SELECT, matching PostgreSQL rather than `pg_stat_activity` redaction.
Prepared owners retain original virtualtransaction text with NULL PID and no
live VXID hold. Native logical row/key contention exposes one real current
transaction dependency; it never invents physical tuple coordinates or key hashes.
Native fastpath is always false. Engine/Driver/opaque-DSN isolation is unchanged.
See [native mechanism boundaries](docs/DIALECT.md#live-lock-state) and
[selected proof and smoke](reference/sessions/README.md#live-lock-state).

## JSONB record conversion — change note

`jsonb_populate_record`, `jsonb_populate_recordset`, `jsonb_to_record`,
`jsonb_to_recordset` and `jsonb_populate_record_valid` convert JSONB objects
into named composites or explicit typed records. Nested composites, rectangular
arrays, member typmods and existing domain CHECKs use the ordinary type codecs.
Missing fields retain a supplied base; explicit JSON null replaces it with
typed SQL NULL. Every recordset row starts from the original base.
Validity uses the same conversion, without swallowing setup or cancellation
errors. Typed source definitions retain every member type through prepared
execution, stored views and native Dump/Restore. JSON-text population functions
and general scalar table functions are not added.
See [contracts and feature smoke](reference/binary_json_arrays/README.md#jsonb-record-conversion).

## Imported foreign definitions and logical replay — change note

`IMPORT FOREIGN SCHEMA` uses authentic provider catalog discovery, exact
LIMIT TO/EXCEPT selection and ordinary atomic foreign-table validation.
Provider import options retain ordered occurrences; the real reference reader
consumes `import_not_null` and discovers source column mappings and typmods.
Native Dump/Restore preserves the supported wrapper/server/mapping/table graph,
ordered options, owners, defaults/CHECKs and foreign table/column comments.
Destination handlers/validators must be authentically registered beforehand;
existing target roles are required. No executable code or remote rows are copied,
and replay never invokes importer/scan/modify callbacks. Validators may run.

Dump includes exact mapping credential bytes and is sensitive host-authorized
output; protect it. SQL catalog redaction is unchanged. Existing unsupported
explicit ACL states fail before bytes, and reconstruction grants no lasting
privileges. Logical live columns retain order/types/options; dropped physical
attribute numbers are not durable identities. See
[the embedding contract](docs/DRIVER.md#foreign-provider-registration) and
[independent import/replay proof](reference/tables/README.md#imported-foreign-definitions-and-native-graph-replay).

## Foreign metadata and scans — change note

SQL/MED wrappers, servers, user mappings and foreign tables now use the common
transactional catalog, ownership/ACL and dependency machinery. Ordered table and
column options are validated atomically. Foreign SELECT executes only through
an explicitly registered Go provider, with real planning, typed local rechecks,
restartable scans and terminal cleanup; a handler name or URL is not executable
code. Optional IMPORT callbacks return actual typed table definitions.

Raw `pg_user_mapping` preserves exact mapping/server/role OIDs (PUBLIC is role0)
and ordered TEXT[] options, with nullable `umoptions` in both result metadata
and `pg_attribute`. No-options NULL is distinct from an empty-string option.
Ordinary raw SELECT ACLs protect secrets, including empty queries and held
statements. Native catalog-source GRANT/REVOKE SQL remains unsupported with0A000
and unchanged ACLs; PostgreSQL's explicit grants are exercised as oracle behavior,
not added native admission. The companion `pg_user_mappings`
view independently masks options by current-user/server authority, not mere
membership in another mapped role. Metadata reads do not establish connections.
Focused metadata/security proof and the `TestReferencePGUserMappingMetadata/Smoke`
feature smoke are documented in the [SQL/MED test contract](reference/tables/README.md#foreign-metadata-and-scans).

Retirement also fences late committed incoming dependencies, including registered
handler/validator identities and providers discovered by CASCADE. If a concurrent
session creates a surviving dependent after DROP captured its closure, native
publication fails with ordinary `stuntgres: catalog publication conflict` rather
than committing dangling metadata. This optimistic fence does not promise
PostgreSQL's blocking schedule or automatically retry the transaction.

Ordinary results finish their foreign resources before publication. Named SQL
cursors instead retain their live producer across finite eager FETCH/MOVE batches;
public `Rows.Next`/`Close` never perform foreign I/O. This is not a PostgreSQL C
ABI, built-in postgres_fdw transport or connection-pooling feature.
See [embedding API](docs/DRIVER.md#foreign-provider-registration),
[catalog/control limits](docs/TABLES.md#foreign-metadata-and-dependencies), and
[selected dual-mode proof](reference/tables/README.md#foreign-metadata-and-scans).
Feature smoke: `make check RUN='^TestReferenceFDWScan$'`, and the same command
with `REFERENCE=1`; Rescan and Import smoke selectors are documented alongside it.

### Registered foreign writes

The same registered provider may independently implement `ForeignInserter`,
`ForeignUpdater`, `ForeignDeleter` and `ForeignTruncater`. UPDATE/DELETE request
separate provider-defined original row identity through `ForeignUpdateTargetProvider`
and `ForeignIdentityScan`; changing a key does not change the selected identity.
Successful callbacks return actual full tuples and presence, not proposed-row
echoes or attempted-row counts. RETURNING uses those actual tuples.
View CHECK OPTION validates the imported actual returned tuple before RETURNING,
not proposed NEW; failure rolls back the real provider statement.

A writer also implements `ForeignTransactionProvider` with genuine statement
and transaction rollback. Unsupported atomicity rejects before mutation (`0A000`);
there is no irreversible auto-commit mode or compensating DML. Optional planning,
Begin/End and COPY-specific BeginForeignInsert/EndForeignInsert release execution
resources, not transactions. Both batch methods are required for batching;
RETURNING and applicable view CHECK OPTION use singles. COPY FROM stages
accepted text/CSV/binary input before remote mutation.
TRUNCATE groups selected tables once per actual server and passes identity/cascade flags.

For an application-owned concrete provider, registration remains the existing API:

```go
func OpenWritableForeign(name string, source interface {
	stuntgres.ForeignDataWrapper
	stuntgres.ForeignTransactionProvider
	stuntgres.ForeignInserter
}) (*sql.DB, error) {
	d := stuntgres.NewDriver()
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{
		HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "writer_handler"},
		Wrapper: source,
	}); err != nil {
		return nil, err
	}
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}
```

Create SQL/MED wrapper/server/mapping/table metadata naming `writer_handler` as
with the scan example. The [real SQL writer fixture](reference/tables/fdw_writer_test.go)
implements actual source SQL transactions, savepoints and mutations.

Participants belong to the existing session transaction owner. Foreign scans and
writes share the enlisted transaction; separate observers cannot see its uncommitted
effects. Commits are remote-first in enlistment order, local publication last,
with final local fences retained. **This is not distributed atomicity:** a later
commit or final local fence can fail after an earlier remote commit; a commit
error can mean an unknown outcome. The command fails, already committed effects
are not undone, and no callback is retried. Do not blindly retry. PREPARE TRANSACTION
is rejected after foreign participation. See [provider contracts](docs/DRIVER.md#foreign-writes-and-transactions).

Foreign CHECK/NOT NULL remain unenforced locally. STORED values are computed
before the callback; VIRTUAL values are sent as NULL and computed on read.
postgres_fdw independently sends DEFAULT for generated fields, so its ordinary
remote-column fixture differs from the native provider that stores callback inputs.
Foreign MERGE/ON CONFLICT, row locking, partitions, local indexes/keys and native
procedural triggers remain outside this surface.

Write smoke: `make check RUN='^TestReferenceFDWWriteActions/memory/Smoke'`;
pinned oracle: `make check RUN='^TestReferenceFDWWriteActions/postgres/Smoke' REFERENCE=1`.


## Type catalog — change note

`pg_catalog.pg_type` exposes all32 PostgreSQL18 metadata columns for implemented
families, genuine registered/user-relation rowtypes and canonical companion
arrays. OID/name/schema/owner, physical facts, immediate domain bases/typmods,
collation, numeric/symbolic I/O routines, raw defaults and ACLs come from their
existing immutable authorities. Installed query_int retains its actual dynamic
routine identities, including inheritance through domains and nested domains.
Immediate-base facts are completed through the same captured catalog before
domain composition; no routine addresses are inferred from names or flattened
base types. PUBLIC inventory reads do not depend on type/schema USAGE.

Metadata admission is not codec admission: broader REGTYPE lookup, lookup-only
companions, unsupported casts/routines and unimplemented temporal multiranges
retain their existing boundaries. Seeded time_stamp default metadata does not
enable inherited-default execution. Native mutation/row locking remain explicit
0A000 boundaries; ordinary mutation is denied42501. Captures follow existing
transaction, TEMP-session and Dump/Reset/Restore publication without dump rows.

See the [bounded contract](docs/DIALECT.md#pg_type-supported-type-metadata) and
[selected proof](reference/catalogs/README.md#type-catalog-metadata-and-proof).
Feature smoke: `make check RUN='^TestReferencePGTypeFeatureSmoke$'`, also with
`REFERENCE=1`, under an external900-second deadline.

## Range catalog — change note

`pg_catalog.pg_range` exposes the seven PostgreSQL18 metadata columns for the
six executable builtin range types. Subtype, multirange companion, subtype
btree class and canonical/difference routine identities are exact; zero
canonical REGPROC is nonnull and displays `-`. Ordinary direct/prepared/held
queries and PUBLIC reads use the existing immutable catalog/control lifecycle.
Procedure links and temporal multirange companions remain lookup-only; this
does not enable named routines, range indexes or additional range DDL.
See [native contract](docs/DIALECT.md#pg_range-supported-range-metadata) and
[selected proof and feature smoke](reference/catalogs/README.md#pg_range-selected-proof).

## Installed UUID version 4 — change note

`CREATE EXTENSION "uuid-ossp"` installs ten `RETURNS uuid` members,
version1.1: eight zero-argument functions (`uuid_generate_v4()`,
`uuid_generate_v1()`, `uuid_generate_v1mc()` and the five constants below),
plus `uuid_generate_v3(namespace uuid,name text)` and
`uuid_generate_v5(namespace uuid,name text)`.
The generator uses Go's
`crypto/rand` source and the shared UUID codec to return non-NULL random
version4/RFC-variant values. The function is VOLATILE: held calls, row defaults
and views demand fresh values; generated columns and expression indexes reject it.
Schema visibility and real installed procedure identities survive native
Dump/Reset/Restore, including grouped views projecting UUID calls beside
aggregates. Legitimate group-stage metadata is retained without relaxing closed
DEFAULT restrictions.
Its installed identity also participates in current-invoker EXECUTE grants:
held calls, views and demanded DEFAULT writes deny after revocation, without
borrowing a table/view owner's function authority or publishing partial writes.
Canonical text remains distinct from the16-byte binary COPY/wire UUID encoding;
the extension adds no separate embedded binary-bind API. See the
[contract and feature smoke](reference/catalogs/README.md#installed-uuid-ossp-version-4).

The IMMUTABLE STRICT PARALLEL SAFE constants are `uuid_nil()` =
`00000000-0000-0000-0000-000000000000`, `uuid_ns_dns()` =
`6ba7b810-9dad-11d1-80b4-00c04fd430c8`, `uuid_ns_url()` =
`6ba7b811-9dad-11d1-80b4-00c04fd430c8`, `uuid_ns_oid()` =
`6ba7b812-9dad-11d1-80b4-00c04fd430c8`, and `uuid_ns_x500()` =
`6ba7b814-9dad-11d1-80b4-00c04fd430c8`. Nil UUID is not SQL NULL.
These installed calls retain current-invoker EXECUTE authority and qualified
symbolic recipes through controls; immutable generated columns and expression
indexes accept them. Ordinary UUID storage, textual bindings and NULL semantics
are unchanged. None accepts a NULL argument or any other argument.

The time generators are VOLATILE STRICT PARALLEL SAFE version1/RFC-variant
UUID functions. One nontransactional physical-session allocator couples a
cryptographically seeded fallback node, time and14-bit sequence. V1 uses that
stable random multicast/local node; v1mc draws a fresh random node with both bits
set on each demand. There is no host-MAC discovery, C UUID library, uuidd,
clock file or durable cross-process coordination. Equal microsecond buckets
have ten100ns slots; exhaustion waits cancellation-aware outside the allocator
lock for observed clock progress. Clock rollback increments the sequence; wrap
rotates the stable random fallback node. Failed entropy/canceled calls publish
no allocation state. Rollback and native catalog controls do not rewind the
allocator. Uniqueness across random node epochs, physical sessions, Drivers and
restarts is probabilistic, not a persisted coordination guarantee.
See [time-generator proof and smoke](reference/catalogs/README.md#installed-uuid-ossp-time-generators).

The name-based generators are IMMUTABLE STRICT PARALLEL SAFE: v3 hashes with
MD5, v5 with SHA1. They hash the namespace's16 network-order bytes followed by
the exact UTF8 name bytes, retaining16 digest bytes and setting version/RFC
variant bits. Empty names and arbitrary/nil namespaces are valid; case,
whitespace and Unicode composition are not normalized. Either SQL NULL input
returns SQL NULL. Named/reordered namespace/name arguments use the same sole
UUID,text overload. These calls require no entropy, clock or generation state.
Stored defaults, row-dependent generated columns, expression indexes, views and
grouping retain both operands and actual installed identity through native
controls and current-invoker EXECUTE checks. See
[name-generator proof and smoke](reference/catalogs/README.md#installed-uuid-ossp-namespacename-generation).

## Language catalog — change note

`pg_catalog.pg_language` exposes the nine PostgreSQL language metadata columns
through ordinary direct/prepared queries, catalog discovery and PUBLIC reads.
Native availability is deliberately limited to `internal`: OID12, owner10,
`lanispl=false`, `lanpltrusted=false`, call/inline handler OIDs0,
validator OID2246 and NULL language ACL. The validator is metadata, not an
executable validation service. The separate bounded scalar SQL routine facility
does not add language catalog rows. C/PL/pgSQL runtimes, language DDL, general
procedural execution and external handlers remain unavailable.
See [native limits](docs/DIALECT.md#pg_language-supported-language-metadata) and
[selected proof and feature smoke](reference/catalogs/README.md#pg_language-selected-proof).

## Transform catalog — change note

`pg_catalog.pg_transform` exposes the five released columns in order:
`oid OID`, `trftype OID`, `trflang OID`, `trffromsql REGPROC`, and
`trftosql REGPROC`. The complete native inventory is empty: casts, codecs,
SQL routines and installed extensions do not implement procedural-language
transforms. PUBLIC reads, direct/prepared queries, catalog discovery and native
Dump/Reset/Restore retain the exact typed metadata without fabricating rows.
Ordinary-role writes report PostgreSQL permission errors; native superuser
catalog writes remain unsupported. No transform or language DDL is enabled.
See [native limits](docs/DIALECT.md#pg_transform-transform-metadata) and
[selected proof](reference/catalogs/README.md#pg_transform-selected-proof).

## Text-search configuration metadata — change note

`pg_catalog.pg_ts_config` exposes the five released columns in order:
`oid OID`, `cfgname NAME`, `cfgnamespace OID`, `cfgowner OID`, and `cfgparser OID`.
The complete native inventory is empty: scalar FTS values and installed unaccent
dictionaries do not implement configurations. The relation supports PUBLIC reads,
direct/prepared queries, catalog discovery and native Dump/Reset/Restore without
fabricating `simple`, parsers or owners. Ordinary-role writes report PostgreSQL
permission errors; native superuser writes remain unsupported. This does not
enable configuration DDL/use or symbolic REGCONFIG lookup.
See [native limits](docs/DIALECT.md#pg_ts_config-configuration-metadata) and
[selected proof](reference/catalogs/README.md#pg_ts_config-selected-proof).


## Text slicing — change note

`left`, `right`, positional `substring` and `substr` slice TEXT by Unicode
codepoints, not bytes or grapheme clusters. Negative left/right counts omit
characters from the opposite end; omitted substring counts run to the end.
Explicit negative substring counts raise22011. Results are unconstrained TEXT;
CHAR padding is trimmed by its TEXT conversion, while VARCHAR spaces remain.
The pinned PostgreSQL18.0 boundary `right(text,-2147483648)` retains the source,
whereas `left` with that count is empty. Existing POSIX/SIMILAR substring
overloads retain their distinct matching and collation contracts.
Calls compose in query/write/stored expressions and native replay.
See [native limits](docs/DIALECT.md#text-slicing) and
[selected proof and feature smoke](reference/strings/README.md#text-slicing-selected-proof).

## Access-method catalog — change note

`pg_catalog.pg_am` exposes exact PostgreSQL access-method identities and the
four documented fields, including genuine REGPROC handler values. The complete
bootstrap inventory is heap2/handler3, btree403/330, hash405/331 and bounded logical
GiST783/332. User-defined methods extend that captured inventory. It is PUBLIC-readable and
recreated by native Reset/Restore. Ordinary-role writes preserve PostgreSQL
permission diagnostics; native superuser writes remain unsupported.
These are implemented logical methods, not physical PostgreSQL heap or index
algorithms. GIN, SP-GiST and BRIN are not advertised; handler metadata does not
make handlers callable.
See [native limits](docs/DIALECT.md#pg_am-supported-access-methods) and
[selected proof and feature smoke](reference/catalogs/README.md#pg_am-selected-proof).

Change note: `CREATE/DROP ACCESS METHOD` supports database-global INDEX and TABLE
definitions backed by genuine registered native handlers. Custom B-tree methods
retain their own SQL identity and require a genuine method-specific operator
class before index CREATE/INSERT maintenance. Catalogs, dependency-aware drop
and logical Dump/Restore preserve that identity. TABLE definitions select the
existing heap authority but do not add custom TABLE selection or DML routes.
Arbitrary PostgreSQL C-ABI/shared-library handlers remain external; a valid but
unregistered native handler returns an explicit0A000 capability boundary.
See [the design](docs/design/PG18-INDEX-ACCESS-METHOD.md) and
[selected proof](reference/indexes/README.md#access-method-definitions).


## Operator-class catalog — change note

`pg_catalog.pg_opclass` exposes the nine PostgreSQL operator-class metadata
columns for implemented btree/hash and bounded GiST classes, with exact released
seed OIDs and current method/family/input/default/storage/namespace/owner facts.
The catalog is PUBLIC-readable; ordinary class DDL changes real descriptors,
not system-table rows. `CREATE OPERATOR CLASS` supports DEFAULT, explicit or
implicit FAMILY, STORAGE and real method-specific member declarations.
`ALTER` supports rename, owner and schema; `DROP` uses ordinary dependencies.
Custom classes select their real registered comparison/hash support for index
build and maintenance, without a static builtin-name/default fallback.
See [native limits](docs/DIALECT.md#pg_opclass-supported-operator-classes) and
[selected proof and feature smoke](reference/catalogs/README.md#pg_opclass-selected-proof).

## JSONB containment — change note

JSONB `@>` and reverse `<@` return BOOL for structural containment. Object
subsets match at the same depth; arrays ignore ordering and duplicate counts
without flattening nested arrays or merging separate objects. An array can
contain an equal immediate primitive, but that exception does not convert
array-valued object fields into scalar fields. Numeric comparison is exact
and ignores scale. SQL NULL remains NULL, distinct from JSON null.
Direct, prepared and stored expressions share the immutable JSONB provider
and symbolic native Dump/Restore. No JSON containment, SQL-array dependency,
JSONPath execution or GIN implementation is added.
See [the contract and feature smoke](reference/binary_json_arrays/README.md#jsonb-containment).

## JSONB concatenation and top-level deletion — change note

`jsonb || jsonb` combines object keys with right-hand precedence, without a
recursive merge. Otherwise it concatenates arrays, wrapping non-array operands
as single elements. `jsonb - text` removes an object key or every matching
top-level array string; `jsonb - integer` removes one array element, with negative
indices counting from the end. Missing keys and out-of-range indices are no-ops.
These strict immutable operators retain canonical JSONB metadata, exact numeric
scale and symbolic stored/native replay. Scalar deletion raises `22023 cannot
delete from scalar`; integer deletion on objects raises `22023 cannot delete
from object using integer index`. SQL NULL is distinct from JSON null.
Text-array deletion and named concat/delete functions are not added; existing
`#-` path deletion is unchanged. See [contracts and feature smoke](reference/binary_json_arrays/README.md#jsonb-concatenation-and-top-level-deletion).

## Commit timestamp information — change note

The three commit-time inquiries use retained real transaction completions:
`pg_xact_commit_timestamp(xid)`, `pg_xact_commit_timestamp_origin(xid)` and
`pg_last_committed_xact()`. Tracking defaults off; opt in once with
`NewDriverWithConfig(DriverConfig{TrackCommitTimestamp: true})`.
SQL cannot change this startup setting. Local commits report origin 0.
Empty modifying/row-lock scans and empty COPY do not fabricate completions;
genuine assignment survives rollback of staged writes to a savepoint.
See the [enabled contract and smoke](reference/locking_transactions/README.md#commit-timestamp-information),
[default-off diagnostics](reference/sessions/README.md#commit-timestamp-default-off-contract),
and [native retention boundary](docs/FIXTURES.md#commit-timestamp-history).

## JSONB pretty output — change note

`jsonb_pretty(jsonb)` returns TEXT with PostgreSQL-compatible four-space
indentation, LF line breaks and JSON escaping. It renders the actual canonical
JSONB value, retaining numeric scale and last-wins keys rather than source
whitespace. Empty containers are multiline; SQL NULL remains SQL NULL, while
JSON null produces the text `null`. The strict immutable scalar supports
prepared calls, stored expressions and symbolic native Dump/Restore.
No JSON pretty function or formatting options are added.
See [the contract and feature smoke](reference/binary_json_arrays/README.md#jsonb-pretty-output).

## Range and multirange operators — change note

Containment (`@>`, `<@`), overlap (`&&`), positional operators (`<<`, `>>`,
`&<`, `&>`), adjacency (`-|-`), scalar comparisons and set operations (`+`, `*`,
`-`) execute on all six built-in range families and the three concrete
integer/numeric multiranges. Predicates include matching range/multirange cross
operands and subtype-element containment. Set operations and comparisons require
the same physical family; they do not implicitly convert ranges to multiranges.

Empty values are sets, not NULL: every range/multirange contains an empty set,
while overlap and positional/adjacency predicates are false if either set is
empty. SQL NULL propagates. Range union or difference that would need disconnected
pieces raises22000; multiranges retain those pieces successfully. Comparisons use
canonical endpoints and lexicographic multirange members, with empty first and
NUMERIC scale ignored for equality. Scalar comparison does not enable ORDER BY,
grouping, hashing or index-key providers.

Typed binds, plain/nested domains, CHECK/generated/view recipes and native
Dump/Reset/Restore reuse existing ownership and remapping. Temporal multiranges
remain lookup-only; no temporal companion codec, cast, constructor or unnest
provider is added. General range casts, range arrays and user-created range
families remain excluded. See [type contracts](docs/TYPES.md#range-and-multirange-operators)
and [selected proof](reference/types/README.md#range-and-multirange-operator-selected-proof).
Feature smoke: `make check RUN='^TestReferenceRangeOperatorFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the same selector.

## JSON and JSONB extraction — change note

JSON supports key/index `->` and `->>`, TEXT[] paths `#>` and `#>>`, and
`json_extract_path` / `json_extract_path_text`. JSON results retain source
fragments; TEXT results decode strings and turn JSON null into SQL NULL.
Missing keys, wrong shapes and out-of-range indices produce typed NULL.
Negative indices count backward. Path functions accept ordinary TEXT components
or an explicit VARIADIC TEXT[]; empty paths require the array form.

Extraction is immutable and supports stored expressions and symbolic native
replay. Processing validates Unicode throughout the document, even discarded
branches, except strict NULL inputs/NULL path elements. PostgreSQL 18.0's
released duplicate-path retention and sibling-array index state are preserved;
path extraction is not always equivalent to repeated `->`.
JSONB supports the same four operators plus `jsonb_extract_path` and
`jsonb_extract_path_text`, traversing its canonical immutable tree. Raw results
are JSONB; container TEXT uses canonical output, strings decode and JSON null
becomes SQL NULL. Paths flatten any TEXT[] rank/bounds; invalid array-path
indices and NULL components return SQL NULL. Empty paths return the whole value.
Integer `->`/`->>` selects a scalar root at 0 or -1; nonempty paths on scalars
return NULL. Unlike JSON, duplicate-intermediate and unrelated-sibling paths
use the canonical selected tree, without retained scanner matches/index state.
All six JSONB forms support stored expressions and symbolic native replay.
JSONPath execution and JSON equality are not added.
See [JSON contracts](reference/binary_json_arrays/README.md#json-extraction)
and [JSONB contracts and smoke](reference/binary_json_arrays/README.md#jsonb-extraction).

## Object identity and address — change note

`pg_identify_object(oid,oid,integer)`, `pg_identify_object_as_address(oid,oid,integer)`,
`pg_get_object_address(text,text[],text[])`, and `pg_describe_object(oid,oid,integer)`
are STRICT, STABLE typed expressions. The three record functions expose their
released OUT labels/types in scalar, member/expanded and FROM calls. Structured
names are raw array components, not SQL text split on dots; table columns use
class1259 and stable attribute numbers. Renames change display, not identity.

Inquiries use existing catalog, owned-definition and fixed alias authorities,
including lookup-only routines/operators and already referenced operator families.
They do not grant execution or add object providers/SQL catalogs. Numeric inquiry
does not require ownership or table SELECT; qualified symbolic lookup retains
schema access checks. Missing symbolic names error, while missing numeric
identities retain class-specific kind/NULL behavior. Dropped column addresses
retain PostgreSQL's tombstone name until the parent relation disappears.
Machine identities quote names; human column descriptions preserve PostgreSQL's
raw attribute wording rather than universally quoting every component.

Symbolic stored views, DEFAULT/CHECK recipes and returned TEXT/TEXT[] data use
existing native replay. Persisted inquiries request the consuming statement's
catalog snapshot. Scalar describe with a constant NULL is simplified without
evaluating volatile siblings; RECORD calls and nonconstant NULL retain reached
argument effects. Type-argument arrays report syntax/NULL errors sequentially.
Raw numeric OID data is not automatically a remapped reference; no new dump
section is needed for these stateless functions.
See [authority, exceptions and selected proof](reference/object_address/README.md#testing).
Feature smoke: `make check RUN='^TestReferenceObjectAddressFeatureSmoke$'`,
also with `REFERENCE=1`.

## Object ACL inquiry — change note

`pg_get_acl(classid oid, objid oid, objsubid integer)` is a strict STABLE
`aclitem[]` expression. It returns the stored object or column ACL, not effective
permissions or synthesized defaults. A column address uses class1259 (`pg_class`)
and its stable attribute number; column grants leave table/other-column ACLs
unchanged. Missing objects and recognized ACL-less kinds return NULL; unknown
class IDs error. NULL ACLs remain distinct from explicit empty arrays.

Held calls read current metadata, without requiring SELECT on the queried table.
Native type authorization's synthetic USAGE baseline does not become a raw type
ACL. Initial database raw ACLs are NULL on both backends; effective owner CTc and
PUBLIC Tc defaults remain separate from stored ACL data. Returned ACLITEM[] data and stored inquiry recipes
use existing native lifecycle support; arbitrary explicit authorization ACLs
still reject Dump/Restore rather than being silently discarded.

See [contracts, selected proof and standalone smoke](reference/catalogs/README.md#object-acl-inquiry-contract).
Feature smoke: `make check RUN='^TestReferenceObjectACLFeatureSmoke$'`
and the same command with `REFERENCE=1`.

## Role membership and row-security inquiries — change note

`pg_has_role([user,] role, privilege)` supports all six PostgreSQL NAME/OID
signatures, defaults the omitted user to `current_user`, and returns BOOL.
MEMBER, inherited USAGE and SET-role paths are independent; comma-separated
privileges are ORed after every token is validated. All six WITH ADMIN OPTION /
WITH GRANT OPTION spellings inquire about ADMIN alone. PUBLIC is not a role
member, unknown names error, and raw OIDs preserve PostgreSQL's self/superuser
inquiry semantics even for absent role identities without granting authority.

`row_security_active(text|oid)` returns BOOL from current metadata and expression
identity: enabled RLS applies unless the effective user has direct superuser /
BYPASSRLS or inherited owner authority without FORCE. Policies and table SELECT
permission are not consulted. The native `row_security` setting defaults to on;
off rejects affected queries rather than bypassing RLS, and never changes this
inquiry's answer. Both functions are strict, STABLE, typed
expressions; held calls re-evaluate live metadata, and stored views retain real
recipes through native Dump/Restore without serializing role or inquiry state.
See [inquiry contracts and selected proof](reference/roles/README.md#role-membership-and-row-security-inquiries).
Feature smoke: `make check RUN='^TestReferenceAccessRoleFeatureSmoke$'`
and the same command with `REFERENCE=1`.

### Client row_security setting — change note

Ordinary users can SET `row_security` on or off. Off raises `42501` when RLS
applies, including empty/dead inputs and fresh Prepare; held handles use the
current statement setting. Owners bypass unless FORCE applies, while direct
superuser/BYPASSRLS bypass even FORCE. Forced owners receive PostgreSQL's
NO FORCE hint. On applies ordinary row-policy expressions, permissive OR,
restrictive AND, command-class conjunction and true-only NEW checks.
SESSION/LOCAL, rollback and savepoints use the existing settings journal.
The typed `pg_catalog.pg_settings` provider exposes all registered settings,
including `row_security`; SHOW ALL consumes the same immutable registry.
See [settings and provenance](docs/SETTINGS.md#row-security-and-pg_settings).
Feature smoke: `make check RUN='^TestReferenceRowSecurityFeatureSmoke$'`
and the same command with `REFERENCE=1`.

### Runtime settings metadata — change note

`pg_settings` now captures all33 builtins and host-registered custom definitions
coherently, with exact types, units, contexts, categories, bounds, descriptions,
current values and captured reset provenance. Numeric metadata uses base units:
SHOW statement_timeout=`1s` corresponds to setting=`1000`, unit=`ms`.
Prepared reads recapture current state; opened native rows remain owned snapshots
through SET, physical retirement and native Reset/Restore. The native view stays
read-only; PostgreSQL's UPDATE rewrite rule is an intentional boundary.
No cumulative statistics collectors, inert controls or fake metrics are added.
See [settings](docs/SETTINGS.md#row-security-and-pg_settings) and
[selected proof](reference/session_state/README.md#runtime-settings-metadata).
Feature smoke: `make check RUN='^TestReferenceStatisticsSettingsFeatureSmoke$'`,
also with `REFERENCE=1`.



## Blocking session information — change note

`pg_blocking_pids(integer)` and `pg_safe_snapshot_blocking_pids(integer)` return
typed INT4 arrays from current engine state. Both are strict and VOLATILE:
NULL input produces NULL, while missing or nonwaiting targets produce `{}`.
Ordinary inquiry reports conflicting holders and earlier conflicting queued
requests; safe inquiry reports only actual SERIALIZABLE READ ONLY DEFERRABLE
snapshot acquisition waits, not ordinary read-only transactions or predicate
dependencies. Prepared owners contribute zero; distinct owners retain duplicate
zeros. Arrays are unordered bags, not sorted or deduplicated PID sets.

Held statements and stored views retain live call recipes through Dump/Restore.
No wait queues, SSI candidates or separate builtin identity state are serialized;
user-stored integer PID values remain ordinary table data.
Native physical session identities remain uint64: positive INT4 targets compare
exactly, without truncation. A live blocker above INT4 range produces an ordinary
`stuntgres: blocking session identity exceeds integer range` error rather than a
wrapped PID, omission or prepared-owner zero. Private maintenance sessions are
not public blockers. This feature adds no `pg_backend_pid()` or identity API.
Native parallel scans use goroutines, not PostgreSQL child backend lock groups;
live inquiries retain serial fallback rather than inventing worker PIDs.

See [blocking contracts, selected proof and smoke](reference/sessions/README.md#blocking-session-information).

## Schema and temporary namespace identity — change note

`current_schema` / `current_schema()` return the first searchable explicit
search-path schema as NAME, or NULL for an empty effective explicit path.
`current_schemas(boolean)` returns ordered NAME[] paths, including implicit
system/TEMP entries only when requested; NULL input returns typed NULL.
A pending `pg_temp` creation destination initializes the physical session's
TEMP/TOAST namespace pair when a non-NULL schema inquiry is actually evaluated,
not during driver preparation. First-use state follows statement/transaction
publication and rollback.

`pg_my_temp_schema()` returns OID zero before TEMP initialization.
`pg_is_other_temp_schema(oid)` recognizes another physical session's TEMP and
TEMP TOAST namespaces, including retained namespaces after physical retirement;
own, ordinary and missing namespaces return false, and NULL input returns NULL.
Identity inquiry does not grant access to peer TEMP contents. Dropping the last
TEMP object, DISCARD TEMP and returning a pooled borrower retain physical identity.
Stored views evaluate these functions against the destination execution session;
Dump excludes TEMP objects and serializes no session identity state.

See [namespace contracts and selected proof](reference/sessions/README.md#schema-and-temporary-namespace-identity).

## Bit-string scalar operators — change note

BIT/VARBIT support concatenation `||`, equal-actual-width `&`, `|`, `#`,
unary `~`, length-preserving `<<`/`>>`, and scalar comparisons. Concatenation
returns unrestricted VARBIT; bitwise operations return unrestricted BIT.
Negative shift counts reverse direction, and shifting by the width or farther
fills with zeroes rather than applying integral modulo-count rules.
Comparison is lexical over every digit, with shorter prefixes first; leading
zeroes and different widths remain significant. NULL operands yield typed NULL.
Stored defaults/generated expressions/checks/views use the existing replay path.
Bit casts, arrays, ordering/index/grouping providers and aggregate DISTINCT
remain outside this scalar feature.
See [contracts and selected smoke](reference/binary_json_arrays/README.md#bit-string-scalar-operators).

## UUID extraction — change note

`uuid_extract_timestamp(uuid)` returns TIMESTAMPTZ for version 1 and version 7
UUIDs; all other versions, including version 6, return typed NULL.
Version 1 Gregorian 100-ns ticks are truncated to microseconds before epoch
conversion. Version 7 uses Unix milliseconds and ignores submillisecond bits.
`uuid_extract_version(uuid)` returns the complete version nibble, 0 through 15,
as SMALLINT (INT2), not a whitelist of standardized versions.
Both functions accept exactly the RFC variant with fourth-group leading digit
8, 9, a or b; other variants and NULL inputs produce typed NULL.

These strict immutable scalars reuse ordinary UUID input, stored expressions,
prepared UUID parameter inference and session timestamp presentation. They do
not generate UUIDs or consume a clock or entropy. See
[selected dual-mode proof and feature smoke](reference/types/README.md#uuid-extraction-selected-proof).

## Lead and lag windows — change note

`lead(value [, offset [, default]]) OVER (...)` and `lag` select relative
to the current row in its complete partition, respecting NULL members.
Omitted offset is1; zero selects the current row and negative offsets reverse
direction. A NULL offset returns NULL without evaluating value/default.
An out-of-partition target evaluates the default on the current row; an
in-partition NULL value does not use that default. Value expressions evaluate
only on selected members, without a cross-row cache.

One-/two-argument overloads retain the concrete value type and reject an
unconstrained unknown value. Three arguments resolve value/default to one
implicitly compatible type, including domain identity when both agree.
Offsets infer INT4; smallint coerces implicitly, bigint/numeric/known text do not.
Frames and exclusions do not change selected members, but written frame bounds
are still validated. Outputs are known nullable with unsized result typmods.
Default-only dependencies and permissions survive stored-view replay and native
controls. See [window contracts](docs/QUERIES.md) and
[selected proof](reference/query_order/README.md#lead-and-lag-windows).
Feature smoke: `make check RUN='^TestReferenceQueryWindowOffsetFeatureSmoke$'`
and `make check RUN='^TestReferenceQueryWindowOffsetFeatureSmoke$' REFERENCE=1`.

## Ntile window buckets — change note

`ntile(integer) OVER (...)` returns nullable INT4 bucket labels across each
complete ordered partition, distributing extra rows to the earliest buckets.
Frames and EXCLUDE do not alter these labels; tied peers may be split. The first
positive bucket argument initializes that actual call once per partition, so
later argument expressions are not evaluated. Initial NULLs return NULL and
retry on the next row without shrinking the full-partition denominator.
Reached nonpositive arguments raise22014; empty input and LIMIT0 do not evaluate
them. Smallint and unknown literals/parameters use the existing INT4 coercion.

Same-held executions recover after errors and start fresh. Stored views retain
bucket-only dependencies and permissions through deparse and native controls.
See [window contracts](docs/QUERIES.md) and
[ntile selected proof](reference/query_order/README.md#ntile-window-buckets).
Feature smoke: `make check RUN='^TestReferenceQueryWindowNtileFeatureSmoke$'`
and `make check RUN='^TestReferenceQueryWindowNtileFeatureSmoke$' REFERENCE=1`.

## Array comparisons — change note

Ordinary `=`, `<>`/`!=`, `<`, `<=`, `>`, and `>=` compare existing executable
arrays of the same actual array type. Elements are visited in row-major order;
the first difference wins, then element count, rank, all dimension lengths,
and all lower bounds break ties. Equality requires identical shape and contents.
NULL elements compare equal to one another and sort after non-NULL elements;
a whole-array NULL gives a BOOL NULL.

Element equality and ordering remain separate capabilities. Equality checks
shape before demanding its element provider; ordering demands its provider even
for empty arrays. String collation is demanded only for a real non-NULL leaf
pair. Existing nested array-valued domain search/membership consumers reuse
equality. This does not add missing leaf providers, array-key ordering/index
opclasses, or new containment operations. ANY/SOME/ALL keep their SQL
three-valued reductions and empty-array identities.
An existing RHS whose elements are array-valued domains retains its concrete
array identity: quantified comparisons compare the inner arrays and reduce the
results without flattening them or requiring a new array-of-array type.

Feature smoke: `make check RUN='^TestReferenceArrayOrderFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceArrayOrderFeatureSmoke$'`.
See [array ordering coverage](reference/binary_json_arrays/README.md#ordinary-array-comparisons).

## Array construction and transformation — change note

`array_append`, `array_prepend`, `array_cat` and the three array `||` overloads
select a compatible element type through existing catalog array associations.
NULL arrays act as empty inputs for element insertion; concatenation retains
the other array for NULL or empty inputs. A known array beside an unknown `||`
operand selects array concatenation, not element insertion.

`array_fill` constructs checked rectangular shapes with optional lower bounds.
`array_reverse` and `array_sort` move complete first-axis slices and preserve
all bounds. Sorting supports descending and independent NULL placement; inner
NULL elements of multidimensional slices follow ordinary array comparison.
`trim_array` removes first-axis items and resets every retained lower bound to 1.
Inputs remain immutable, including shared no-op results.

`array_to_string` flattens storage order, omits NULL elements unless a non-NULL
replacement is supplied, and uses each element's actual scalar output under
the coherent statement settings/catalog environment. It is always STABLE;
the other seven functions and array concatenation are IMMUTABLE. Named sort
arguments evaluate in formal order while stored SQL retains their written slots.
No new array-of-array identities, leaf providers or array index opclasses are added.

Feature smoke: `make check RUN='^TestReferenceArrayTransformFeatureSmoke$'`
and `make check RUN='^TestReferenceArrayTransformFeatureSmoke$' REFERENCE=1`.
See [selected contracts](reference/binary_json_arrays/README.md#array-construction-and-transformation-contracts).

## Array sample, shuffle and unnest — change note

`array_sample(anyarray, integer)` selects first-axis items without replacement;
`array_shuffle(anyarray)` permutes those items. A multidimensional item is an
entire slice, not one flattened element. Shuffle retains every bound; sample
resets the first lower bound to 1 and retains trailing bounds. Zero samples are
canonical empty arrays. Counts outside 0..first-axis length report PostgreSQL
22023 diagnostics. Random results are checked by invariants, never a sequence.
Their separate session entropy does not perturb `random()` or promise
reproducibility after `setseed`.

Unary array `unnest` expands storage order, including NULL elements, in FROM or
the target list and scalar wrappers. Unqualified multiargument FROM `unnest`
zips independently typed inputs and pads shorter inputs with typed NULLs.
Qualified or non-FROM multiargument calls remain missing functions; no new
overload or general SRF framework is introduced. Existing multirange expansion,
lateral sources, ordinality, composite/domain elements, stored views and native
replacement ownership share the existing query lane.

Feature smoke: `make check RUN='^TestReferenceArrayRelationalFeatureSmoke$'`
and `make check RUN='^TestReferenceArrayRelationalFeatureSmoke$' REFERENCE=1`.
See [selected coverage](reference/binary_json_arrays/README.md#array-sample-shuffle-and-unnest).

## Array dimensions and cardinality — change note

The selected PostgreSQL §9.19 scalar functions `array_dims(anyarray)`,
`array_ndims(anyarray)`, `cardinality(anyarray)` and
`array_length/array_lower/array_upper(anyarray, integer)` inspect the existing
immutable outer array shape without reading or formatting elements. Bounds,
including non-1 and multidimensional bounds, remain exact; cardinality includes
NULL elements. Empty arrays have NULL dimensions/rank/length/bounds and
cardinality zero. Whole NULL gives a typed scalar NULL, as does a NULL,
nonpositive or absent dimension.

Inputs require a known array identity (including domains); neither an unknown
first operand nor a Go parameter value infers the simple polymorphic type.
The exact catalog ANYARRAY typed-NULL carrier is also accepted without admitting
pseudo-type storage, casts, parameters or nonnull values. Otherwise matching
nonvariadic calls accept explicit VARIADIC decoration. Results are TEXT for
`array_dims` and INT4 otherwise, including empty/all-NULL result metadata.
Default/generated/CHECK/view/index, group/conditional and native-control routes
reuse existing expression and catalog owners; no new codec, equality provider
or overload subsystem is introduced.
Grouping matches resolved builtin identity, so unqualified and `pg_catalog`
spellings are equivalent. Argument and FILTER transformation precede function
namespace lookup; malformed named notation retains syntax-error ownership and
character positions, while well-formed named calls remain missing signatures.

Feature smoke: `make check RUN='^TestReferenceArrayInspectFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceArrayInspectFeatureSmoke$'`.
See [selected coverage](reference/binary_json_arrays/README.md#array-dimensions-and-cardinality).

## Window value functions — change note

`first_value`, `last_value`, and `nth_value` select from the admitted window
frame, after existing ROWS/RANGE/GROUPS boundaries and exclusions. The default
ordered frame ends at the current row's last peer, so `last_value` is not
automatically the partition tail. Use an explicit whole-partition frame for that
behavior. NULL-valued members count toward the one-based `nth_value` ordinal;
NULL or out-of-range ordinals return NULL, while nonpositive ordinals raise22016.
Each actual call evaluates its ordinal on the current row and its value on the
selected member. Upper sort-target aliases can read an already produced output;
there is no cross-row value cache or eager evaluation of unselected arguments.
See [window contracts](docs/QUERIES.md) and
[selected proof](reference/query_order/README.md#window-value-functions).
Feature smoke: `make check RUN='^TestReferenceQueryWindowValueFeatureSmoke$'`
and `make check RUN='^TestReferenceQueryWindowValueFeatureSmoke$' REFERENCE=1`.

## Composite attribute evolution — change note

`ALTER TYPE` supports ADD/DROP/RENAME ATTRIBUTE and ALTER ATTRIBUTE
`[SET DATA] TYPE`, including COLLATE and typed-table CASCADE/RESTRICT.
Existing composite scalar/array values map stable attributes: additions are NULL,
renames preserve identity, and dropped/re-added names never resurrect old values.
Executable composite arrays retain bounds, NULL elements and nested/domain codecs.
Descriptor-only TYPE needs no assignment cast; actual typed-table rows still do.
Stored scalar/array/domain/nested consumers, including materialized views, block
explicit TYPE even for identity changes or empty/all-NULL storage. Nonstored views
are not physical consumers. Relation and domain field CHECKs obey real DROP
RESTRICT/CASCADE; typed descendant pruning preserves unrelated inherited CHECKs.
Held dot/functional field and expanded statements retain original slots/names;
dropped slots return typed NULL, including after same-name re-add. Named results
use current shape; eager cursors retain their captured shape.
Native Dump/Restore emits the final live declarations and actual data.
Catalog replacement mints destination composite codecs and remaps reciprocal
arrays/domains and payloads, preserving stable attributes, bounds, NULLs and
retained source roots.
Comparison/hash/key providers and lookup-only builtin/catalog rows remain excluded.
Selected proof: `make check RUN='^TestReference(CompositeAlter|TypedTableCompositeAlter)'`,
and the same command with `REFERENCE=1` for pinned PostgreSQL and unchanged
native-dump replay. See [types](docs/TYPES.md#arrays-composites-enums-and-domains).

## Multirange constructor and expansion — change note

Unary `multirange(range)` constructs the corresponding integer/numeric
multirange; `unnest(multirange)` expands its canonical members in ascending
order. Both use the existing int4/int8/numeric companions, including domains,
typed binds, target/FROM composition, stored recipes and native controls.
Empty range construction returns `{}`; SQL NULL construction returns NULL.
Empty or NULL expansion returns zero rows with the companion range metadata.
Named-family constructors, multiple constructor arguments, tsvector unnest,
temporal multiranges and general range casts remain excluded. Array unnest is
covered by the separate array-relational change note above.
See [typed range contracts](docs/TYPES.md) and
[selected proof](reference/types/README.md#multirange-constructor-and-expansion-selected-proof).

## Range and multirange inspection — change note

`lower`, `upper`, `isempty`, `lower_inc`, `upper_inc`, `lower_inf`, `upper_inf`
inspect all six executable range families and the three integer/numeric
multiranges. `range_merge` encloses two same-family ranges or all members of one
multirange, including disconnected inputs. Bounds retain their concrete subtype;
empty and omitted bounds return typed NULL, while explicit subtype infinity
remains a real bound. Existing string `lower`/`upper` are unchanged.
Use declared range columns and destination-contextual INSERT literals/binds;
inspection does not add range casts or arrays. Range operators are documented above.
The selected unary generic multirange constructor is documented above; temporal
multiranges remain outside execution. Stored CHECK/generated/view recipes and native
Dump/Restore preserve typed results and held snapshots.
See [typed range contracts](docs/TYPES.md) and
[selected proof](reference/types/README.md#range-inspection-selected-proof).
Feature smoke: `make check RUN='^TestReferenceRangeInspectFeatureSmoke$'`;
pinned counterpart:
`make check RUN='^TestReferenceRangeInspectFeatureSmoke$' REFERENCE=1`.

## Rewrite rules — change note

`CREATE [OR REPLACE] RULE`, `ALTER RULE ... RENAME TO` and `DROP RULE`
install identity-bound, pre-execution query rewrites for INSERT/UPDATE/DELETE.
ALSO, INSTEAD, NOTHING, conditions and ordered action lists compose existing
typed queries and mutations; OLD/NEW are query recipes, not row-trigger callbacks.
SELECT `_RETURN` replacement on an existing ordinary view reuses its view
definition and fixed output layout. Same-event rules expand alphabetically,
with current event-owner authorization and ordinary dependency/prepared fences.
Rewritten DML has one atomic publication, including data-modifying CTE consumers;
persistent rules restore after table data so fixture imports do not fire them.
Demanded original/action/CTE producers prepare before effects; supplied parameters
specialize privately per execution without evaluating unused pure projections.
Conflict actions, including stored action-owned modifying CTEs, own fresh
command-local state and retain stable target/EXCLUDED column identities across
metadata changes. Independent unqualified UPDATE/DELETE actions retain current
event-owner source authority even with zero event rows; inherited input sources
keep caller authority, including security-invoker view bases during mutation
source recollection. Native rule export retains explicit conflict-inference
collation/operator-class restrictions and modifying-CTE source presentation.
Original RETURNING suppresses auxiliary SELECT outputs, not action execution
or errors.
Provider RESTRICT includes ordinary rules attached to views; CASCADE retires
dependent rules without dropping their event view or unrelated ordinary rules.
Named conflict actions retain the actual constraint identity for dependency
retirement and current-name export; rename and old-name reuse do not retarget
restored rules.
Native rule DDL remains autocommit-only. Procedural triggers, MERGE rule
events/actions and NOTIFY transport are not introduced.

See [native rule boundaries](docs/DIALECT.md#rewrite-rules),
[prepared results](docs/DRIVER.md#prepared-catalog-and-path-invalidation),
[fixture controls](docs/FIXTURES.md), [atomic publication](docs/TRANSACTIONS.md#rewrite-rule-publication),
and the owning [rule](reference/views/README.md#rewrite-rules) and
[CTE](reference/dml/README.md#data-modifying-cte-contracts) testing inventories.
Feature smoke: `make check RUN='^TestReferenceRuleFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRuleFeatureSmoke$'`.

## Sequence object privileges — change note

Sequence `GRANT` and `REVOKE` support SELECT, UPDATE, USAGE,
class-specific ALL and grant-option delegation/cascade. Explicit, implicit/TABLE
and ALL SEQUENCES IN SCHEMA targets use the same sequence authority.
Sequence rights remain independent of table INSERT; identity-generated insertion retains its separate
implicit authority. ACL changes preserve generator state and physical-session
currval memory, while held consumers recheck current permissions.
See [authorization](docs/AUTHORIZATION.md#sequence-object-acls) and
[selected proof](reference/object_grants/README.md#sequence-object-grants).

Schema grants independently control USAGE and CREATE. Type/domain grants control
USAGE when creating persistent dependencies, including builtin types; existing
values and ordinary casts remain usable after revocation. Ordinary schemas have
no PUBLIC rights, whereas ordinary types default to PUBLIC USAGE. See
[schema/type authority](docs/AUTHORIZATION.md#schema-and-type-object-acls).

## Routine, database and metadata object privileges — change note

Genuine scalar, aggregate, window, SRF and integrated extension providers retain
exact released formal identities and fresh caller EXECUTE admission. Held
statements and actual stored DEFAULT/generated/CHECK/index/view consumers do not
cache grants; pruned/strict-NULL demand and the documented PostgreSQL old-inlined
quote-wrapper boundary remain explicit. ALL schema expansion covers current
genuine objects, not later installations or lookup-only names.
The finite genuine SQL invoker-body closure documented in
[authorization](docs/AUTHORIZATION.md) and four existing embedded function-method
casts retain original dependency authority, including nonconstant
domain CHECKs and original constant index recipes. Constant keys plan before
predicate truth; actual consumed calls survive folding for fresh post-wait
admission without provider replay. Description bodies remain non-NULL-demanded,
and invoker versus RI-owner domain phases remain distinct. The six integrated BIT
bitwise/shift providers also carry their exact released routine associations;
this adds no runtime, standalone cast function, or SQL-body interpreter.
The seven implemented INET bitwise/arithmetic operators likewise retain exact
routine identities, including commuted addition's real SQL-body call. Fresh
network constants preserve typed-NULL demand before EXECUTE admission. ALTER
TYPE USING keeps each transformation's original manifest and demanded catalog
snapshot through lazy body evaluation; failed rewrites publish no shape/data prefix.
The six existing TID comparisons and equality-derived consumers retain their
released routine identities too; ordinary ordering support remains independent
of scalar EXECUTE grants. No physical ctid or type-codec capability is added.
Existing ACL value constructors, expansion and ACL array/item containment retain
their four released routine identities. Scalar strict NULL can remove a call;
surviving RECORD/SETOF expansion still requires EXECUTE at initialization.
Reading stored ACL values does not replay constructors or require their grants.
Existing HASH/RANGE expression partition keys initialize their original routine
recipes on the first actual tuple, including direct-leaf membership and populated
DEFAULT validation. Stored reads and zero-input writes do not replay key calls;
the current caller needs EXECUTE on surviving outer and SQL-body routines.

The attached database supports CREATE/TEMPORARY/CONNECT (CTc), including fresh
authenticated CONNECT and current-role TEMP allocation/CREATE. Parameter SET/ALTER
SYSTEM metadata (sA) and builtin tablespace CREATE use actual descriptor ACLs and
delegation/cleanup; they do not add missing settings, ALTER SYSTEM execution,
has_parameter_privilege or physical tablespaces. The read-only pg_parameter_acl
catalog exposes actual parameter ACL descriptors. Native unsupported external
grant families have explicit class boundaries, and unrepresentable ACLs fail Dump
before output.

See [authorization](docs/AUTHORIZATION.md#routine-identity-and-execution-admission),
[native boundaries](docs/DIALECT.md#object-privilege-grants) and the
[owning proof inventory](reference/object_grants/README.md#genuine-routine-grants-and-admission).
Feature smoke: `make check RUN='^TestReferenceObjectGrantFeatureSmoke$'`;
pinned counterpart:
`make check RUN='^TestReferenceObjectGrantFeatureSmoke$' REFERENCE=1`.


## Namespace catalog metadata — change note

`pg_catalog.pg_namespace` exposes the PostgreSQL 18 four-column schema:
`oid`, `nspname`, `nspowner` and nullable `nspacl`. Rows are actual namespace
descriptors, including inaccessible user schemas and physical TEMP pairs.
Builtin identities and initialized ACLs are exact; pristine `public` grants
PUBLIC USAGE, not CREATE. TEMP namespaces belong to bootstrap role10 while
their objects retain the creating role, and physical close retains both empty
namespace identities after removing their objects.
Captured ACL principals must exist in their paired role inventory. Physical TEMP
retirement preserves the empty namespace pair but removes its children and scoped
default directives. First TEMP views retain staged physical authority through
execution. Existing isolated Restore reconstructs named-owner domains/enums/composites
without granting ordinary PUBLIC CREATE or changing emitted streams.
Qualified TEMP namespace use respects the captured effective superuser, separately
from physical own-session creation: retired absent children yield42P01 for bootstrap
superusers and schema42501 for ordinary peers. Lookup never adopts retired tokens.
Foreign physical TEMP storage remains unavailable: stored reads and mutation targets
fail at execution with0A000 after valid namespace/role checks, without exposing peer
rows or synthesizing PostgreSQL local-buffer results.
See [namespace metadata](docs/TABLES.md#namespace-catalog-projection) and
[selected proof](reference/catalogs/README.md#pg_namespace-selected-proof).
Feature smoke: `make check RUN='^TestReferencePGNamespaceFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGNamespaceFeatureSmoke$'`.

## Builtin function package split — change note

`internal/engine/functions` owns builtin-specific signature and argument rules,
immutable scalar call carriers, evaluation semantics and aggregate provider state
algorithms. It reuses `internal/sqltype` plans, codecs and values.
The engine retains generic binding, catalog/ACL/dependency and parameter/result
contracts, physical-session state, transactions and relational execution.
This is a package cutover, not a SQL compatibility expansion; see the
[functions package boundary and testing notes](internal/engine/functions/README.md).

## Constraint catalog metadata — change note

`pg_catalog.pg_constraint` exposes the PostgreSQL 18 28-column catalog shape for
actually created NOT NULL, CHECK, UNIQUE, PRIMARY KEY, FOREIGN KEY and admitted
exclusion constraints. Identity, namespace, validity, declaration deferral,
inheritance provenance, stable key attributes and actual supporting-index/operator
relationships follow their feature owners' transactional catalog generation.
Rename preserves identity; drop and rollback reflect actual owner publication.
Standalone indexes do not invent constraint rows. Unsupported domain/trigger,
temporal and unenforced declarations are not enabled by metadata discovery.
Exclusion metadata retains canonical class-selected operator identities,
including CIDR/INET, admitted OID aliases, VARCHAR and enums, without changing
their original equality or storage semantics.
CHECK `conbin` retains a typed native structural tree, not PostgreSQL internal
nodeToString bytes; deparsed semantics remain available.
See [catalog lifecycle](docs/TABLES.md#constraint-catalog-projection) and
[selected proof](reference/catalogs/README.md#constraint-metadata-contract).
Feature smoke: `make check RUN='^TestReferencePGConstraintFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGConstraintFeatureSmoke$'`.

Change note: ordered-set `percentile_cont`, `percentile_disc`, and `mode` execute
`WITHIN GROUP (ORDER BY expression [ASC|DESC])` through the ordinary grouping
pipeline. Percentiles accept scalar FLOAT8 or shaped FLOAT8-array fractions;
continuous results are FLOAT8 or INTERVAL, while discrete/mode results retain
the admitted sortable input type. NULL inputs are ignored. Direct fractions run
once per executed group, including an empty global group, and must use grouped
columns. Mode ties choose the first sorted value. Interval interpolation retains
calendar components, rather than normalizing equivalent representations.
Percentile terminal decisions and fraction validation precede sorting, without
skipping admitted input evaluation or the once-per-group direct expression.
Existing ordering/array-codec capability limits remain explicit; see
[ordered-set contracts](docs/QUERIES.md#ordered-set-aggregates) and
[selected proof](reference/aggregates/README.md#ordered-set-aggregates).
Feature smoke: `make check RUN='^TestReferenceOrderedSetAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceOrderedSetAggregateFeatureSmoke$' REFERENCE=1`.

Change note: hypothetical-set `rank`, `dense_rank`, `percent_rank`, and
`cume_dist` accept matching direct and `WITHIN GROUP (ORDER BY ...)` lists.
They compare the hypothetical tuple with the admitted input tuples, retaining
NULLs and applying each key's direction, NULL placement and pairwise collation.
Direct arguments run once per group; empty global groups return `(1,1,0,1)`.
These aggregate overloads remain distinct from the zero-argument ranking windows;
neither feature expands the admitted ordering capabilities.
See [hypothetical-set contracts](docs/QUERIES.md#hypothetical-set-aggregates) and
[selected proof](reference/aggregates/README.md#hypothetical-set-aggregates).
Feature smoke: `make check RUN='^TestReferenceHypotheticalAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceHypotheticalAggregateFeatureSmoke$' REFERENCE=1`.

Change note: `rank()`, `dense_rank()`, `percent_rank()` and `cume_dist()` now
execute as peer-aware full-partition windows. Rank retains gaps after ties;
dense rank counts peer groups. Percent rank is `(rank-1)/(partition_size-1)`
(zero for a singleton), while cumulative distribution includes all current peers.
Rank outputs are nonnullable bigint; ratios are nonnullable double precision.
Frames/exclusions do not change these results or demand runtime frame offsets
unless a frame-sensitive sibling needs them. Same-named hypothetical aggregates
still require `WITHIN GROUP`. See [ranking tests](reference/query_order/README.md#ranking-windows).
Feature smoke: `make check RUN='^TestReferenceQueryWindowRankFeatureSmoke$'`
and `make check RUN='^TestReferenceQueryWindowRankFeatureSmoke$' REFERENCE=1`.

Change note: supported read-only SELECT scans now execute concurrently in-process
over one immutable statement snapshot. `max_parallel_workers_per_gather` defaults
to 2 (range 0..1024); zero disables dispatch, and the setting is a ceiling rather
than a worker-count promise. Unordered results remain bags; supported ORDER BY
uses sorted worker runs and an ordered merge. Workers own evaluator state and
independent snapshot pins, and all workers join before success or error returns.
Writes, row locks, SQL cursors and unsafe/unsupported execution graphs remain
serial. This is not PostgreSQL background-worker scheduling or parallel
index/join/aggregate machinery. See [query contracts](docs/QUERIES.md#parallel-select-scans)
and [selected proof](reference/query_core/README.md#parallel-select-scan-contract).

## query_int value storage — change note

`CREATE EXTENSION intarray` installs the selected `query_int` base type and its
codec identities through the existing extension registry. Declared columns,
literal and bound values, SQL NULL, explicit text/varchar I/O casts, and
same-type assignments retain typed integer Boolean-query values. Assignment
from query_int to text/varchar stores canonical output; typed character values
require an explicit cast into query_int. Stored order, duplicates and query
structure are not simplified.
Native Dump/Reset/Restore recreates the extension before its consumers and
preserves pending result snapshots and their metadata.

This is selected type support, **not the complete intarray extension**:
integer-array operations, query evaluation, operators, indexes and query_int
array storage are not added. Native query_int columns expose QUERY_INT/string
metadata; lib/pq treats PostgreSQL's generated extension OID as an unknown type.
See [native limits](docs/DIALECT.md#query_int-extension-type) and
[selected proof and smoke](reference/types/README.md#query_int-selected-proof).

## Column catalog metadata — change note

`pg_catalog.pg_attribute` exposes PostgreSQL 18's exact 25-column schema for
represented relation columns and eligible system attributes. User rows retain
their own relation identity, stable attribute numbers, declared type/typmod,
syntactic array rank, NOT NULL/default/identity/generated facts, inheritance
provenance and stored column ACLs. DROP preserves scalar attribute history rather
than renumbering survivors. Views and indexes have no system rows; catalog
metadata does not enable executable system-column access.

The nullable `attmissingval` result remains **ANYARRAY**, not TEXT, while native
materialized fills report no PostgreSQL fast-default physical history. Statistics,
storage-option setters and generic pseudo-type execution are not added.
Native dumps preserve active declaration rank, including attached partitions and
altered inherited descendants, and logical definitions—not deleted physical
history. Inherited-rank repair uses genuine catalog SQL in the dump; PostgreSQL
replay requires superuser catalog-update authority. Public native catalog sources
remain readonly. See [native boundaries](docs/DIALECT.md#pg_attribute-column-metadata)
and [selected proof](reference/catalogs/README.md#pg_attribute-selected-proof).
Feature smoke: `make check RUN='^TestReferencePGAttributeSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGAttributeSmoke$'`.

## Session cursor metadata — change note

`pg_catalog.pg_cursors` exposes the physical session's supported SQL DECLARE
cursors: canonical name, complete original declaration input, holdability,
binary/scroll flags and declaration statement-start `creation_time`.
Metadata reads do not fetch, move or produce cursor rows. FETCH/MOVE and HOLD
promotion preserve the tuple; CLOSE, DISCARD ALL and physical teardown remove it.
Healthy pool reuse preserves it. See [driver cursor contracts](docs/DRIVER.md#sql-cursors--change-note)
and [native limits](docs/DIALECT.md#sql-cursor-metadata).
Feature smoke: `make check RUN='^TestReferencePGCursorsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGCursorsFeatureSmoke$'`.
See [cursor selected proof and smoke](reference/cursors/README.md#testing).

## Integer target series — change note

`generate_series(integer, integer [, integer])` and the corresponding bigint
overloads expand SELECT targets through the existing query SRF stages. Result
metadata stays INT4 or INT8, including empty results. Default step is +1;
reachable endpoints are inclusive, direction mismatches and NULL inputs are
empty, and zero step fails with PostgreSQL `22023`. Successor overflow ends the
series normally. LIMIT stops provider demand before eager result publication.

Calls can be nested or zipped with other target SRFs, with typed NULL padding.
Held statements retain inferred widths but open fresh invocations each time.
Views retain symbolic calls; native Dump/Reset/Restore preserves those views and
literal persisted query results, not generator state. Numeric overloads now use
the same lanes with exact decimal arithmetic (see below). Plain timestamp and
timezone-aware timestamptz series also execute; INSERT VALUES expansion remains
excluded. FROM source support is described below. See
[query contracts](docs/QUERIES.md#integer-target-series) and
[selected proof and feature smoke](reference/numeric/README.md#integer-target-series-selected-proof).

### Numeric series — change note

`generate_series(numeric, numeric [, numeric])` executes exact decimal steps:
`generate_series(0.1,0.3,0.1)` yields `0.1,0.2,0.3`, without floating-point drift.
The seed retains start's display scale; subsequent rows retain the greater of
start and step scales, never stop's scale. Results are unconstrained NUMERIC,
including empty results and calls with constrained numeric inputs.
NULL inputs are empty; nonnull NaN/infinite start, stop or step and zero step
have role-specific PostgreSQL `22023` diagnostics. An unreturned successor may
exceed numeric's packed format limit and still terminate normally past stop.
Direct, held, target/FROM, zip/ordinality/lateral and native persistent-state
consumers reuse existing ownership and statement atomicity.
See [numeric series contracts](docs/QUERIES.md#numeric-series) and
[selected proof and smoke](reference/numeric/README.md#numeric-series-selected-proof).

### Timestamp series — change note

`generate_series(timestamp,timestamp,interval)` advances civil timestamps by
months (clamping month ends), then days, then microseconds. Month stepping is
iterative: January31 → February28 → March28. No timezone or DST rotation applies.
NULL arguments produce typed empty results; zero/net-zero and infinite steps
raise `22023`. Finite mixed-sign steps use PostgreSQL's 30-day-month sign and
may reverse or stall. Infinite timestamp endpoints are legal; use bounded demand
or cancellation for nonterminating calls. Successor overflow raises `22008`
before emitting the current row, including an equal endpoint under LIMIT1.
Existing target/FROM, held statement, ACL, view and native replay lanes are reused.
See [timestamp series proof](reference/numeric/README.md#timestamp-series-selected-proof).

### Timezone-aware series — change note

`generate_series(timestamptz,timestamptz,interval[,text])` advances calendar
months and days in the executing session's TimeZone, or in the explicit fourth
argument's zone. Literal hours remain elapsed time: across New York's March2024
spring transition, daily noon emits17:00Z,16:00Z,16:00Z, while24-hour steps emit
17:00Z each day. Explicit named zones keep their full DST rules; fixed
abbreviations retain their offsets, and dynamic abbreviations use their full
owning zone. Zone lookup uses the invocation's session/abbreviation environment.

Held executions capture current settings afresh, independently of output
presentation. Empty results still expose TIMESTAMPTZ metadata. Strict NULL,
zero/infinite step diagnostics, legal infinite endpoints, mixed-sign behavior
and advance-before-emission overflow follow the existing timestamp cursor.
Native views retain symbolic three-/four-argument calls across Dump/Reset/Restore;
persisted query results retain actual instants, not session settings or cursors.
Existing atomic publication, cancellation and routine EXECUTE authority apply.
See [timezone series contracts](docs/QUERIES.md#timezone-aware-series) and
[selected proof and smoke](reference/numeric/README.md#timezone-aware-series-selected-proof).


Change note: scalar REGOPER and REGOPERATOR retain unsigned operator OIDs through
direct/held values, distinct metadata and native logical Dump/Restore. Name-only
input requires uniqueness; signature input preserves exact operand order and
prefix NONE. All PostgreSQL 18 builtin operator identities are available without
implying execution providers. CREATE/ALTER/DROP OPERATOR now register owned binary
and prefix definitions against exact existing function signatures; execution uses
the actual registered function provider. Schema moves/rename, shell links, stored
dependencies and native logical replay preserve operator identity. Known custom
aliases dump by qualified signature; fixed builtin and unknown OIDs remain
lossless numeric identities. Companion arrays remain lookup-only declarations.
See [user-operator selected proof and smoke](reference/operators/README.md#testing).

Change note: `pg_catalog.pg_operator` exposes all 15 released metadata columns
for 545 scalar-executable exact overloads plus the exclusion-only INET `&&`
provider. These 546 records include concrete mixed-width and declared polymorphic
identities, typed implementation/estimator references, PUBLIC reads and captured
prepared/control lifetimes. This executable metadata subset is distinct from all
799 REGOPER/REGOPERATOR aliases; lookup-only references do not add operator
behavior or SQL `pg_proc` rows. See the
[release admission evidence](docs/design/pg-operator-pg18-oracle.md) and
[selected checks and smoke](reference/catalogs/README.md#pg_operator-selected-proof).

Change note: scalar REGCONFIG (3734) and REGDICTIONARY (3769) retain unsigned
four-byte OIDs through direct/held input, distinct result metadata and native
logical Dump/Restore. Both print zero as `-`, preserve absent numeric identities,
and distinguish typed SQL NULL. REGDICTIONARY name lookup and symbolic output
use actual installed unaccent dictionary descriptors, with SQL quoting, schema
authority and non-TEMP search-path visibility; no default `simple` dictionary is
fabricated. Native symbolic REGCONFIG lookup has no configuration provider and
reports `0A000: text search configuration name lookup is not supported`.
Held literals reuse occurrence-keyed catalog analysis; parameters resolve in
each execution's captured environment, and returned rows retain their spelling.
Logical dumps qualify live dictionary names and remap data/definition identities
to destination allocations. Companion arrays remain lookup-only. This does not
enable text-search DDL, full-text execution or two-argument unaccent.
See [type contracts](docs/TYPES.md) and
[selected proof](reference/type_resolution/README.md#text-search-oid-aliases-regts).

## Array target subscripts — change note

`generate_subscripts(anyarray, integer [, boolean])` expands SELECT targets into
the valid INT4 subscripts of one array dimension, including stored lower bounds
and NULL elements. Reverse=true walks those indices backwards. Any NULL input,
an empty array, or an absent/nonpositive dimension emits no rows while retaining
INT4 result metadata. Array elements are not inspected or coerced.

Calls share existing target-SRF nesting, zip/typed-NULL padding, held statement,
view, CTAS and INSERT SELECT consumers. Each invocation owns constant-space
bounds state; LIMIT stops demand. Native Dump/Reset/Restore retains symbolic
views and materialized rows, not a live cursor. INSERT VALUES expansion remains
a native exclusion. FROM source support is described below. See
[query contracts](docs/QUERIES.md#array-target-subscripts) and
[selected proof and feature smoke](reference/numeric/README.md#array-target-subscripts-selected-proof).

## Set-returning functions in FROM — change note

Existing integer/bigint/numeric series and array subscript providers share the
function-source lane with supported string, JSON, `jsonb_path_query` and regexp providers. Sources
retain output aliases and empty-result metadata. `ROWS FROM` zips members to the
longest output, padding exhausted members with their own typed NULLs; comma
sources form a Cartesian product. `WITH ORDINALITY` appends one-based INT8 values
before filtering and resets on every invocation, including held execution and
implicit or explicit lateral references to preceding sources.

Views retain symbolic arguments and renamed dependency identities; CTAS and
INSERT SELECT retain ordinary statement atomicity. Native Dump/Reset/Restore
retains supported views and persisted rows, never live generator state, without
widening Restore's whitelist. General scalar FROM functions, new overloads and
lateral derived queries remain outside this cutover. See
[query contracts](docs/QUERIES.md#function-sources) and
[selected proof and feature smoke](reference/numeric/README.md#from-srf-selected-proof).


## Phase 2 server — change note

Build and create an account with explicit database grants:

```sh
go build -o stuntgres ./cmd/stuntgres
./stuntgres passwd -u ./users --databases app,other alice
./stuntgres serve -u ./users -l 127.0.0.1:5434 -d ./dumps -L ./logs
```

`passwd` prompts twice without echo on Linux terminals. New accounts require
`--databases`; an existing account keeps its grants when the flag is omitted.
The private userfile uses PBKDF2-SHA256 (600000 iterations), a random16-byte salt
and a32-byte key. Changes take effect after restart. Each permitted account is a
database-local `SUPERUSER LOGIN`; the userfile remains the cross-database gate.
SQL-created roles do not become network accounts automatically.
On Unix, the userfile must belong to the serving user with no group/other access
(normally0600). On Windows, its actual DACL may grant access only to the serving
user, SYSTEM and local Administrators; chmod-style mode bits do not replace ACLs.
Linux password prompting also restores terminal state on catchable SIGINT/SIGTERM.

**Authentication sends cleartext passwords; TLS is not available.** Listen defaults
to `127.0.0.1:5434`; a bare port also binds loopback. Non-loopback binding logs a
prominent warning. Use only loopback or an independently protected trusted network.
Connect using `psql` or `lib/pq`, with `sslmode=disable` (SSL-prefer falls back).
Keep passwords in a0600 PGPASSFILE or another client credential facility, not argv.

Native wire COPY installs its first-record decoding hold and cleanup before
`CopyInResponse` can be published. Immediate frontend cancellation cannot hide
an actual input-conversion error; successfully converted rows still fail with
cancellation before application. Failed publication releases the hold, while
ordinary embedded cancellation and terminal causes retain their existing behavior.
See [COPY cancellation proof](reference/sessions/README.md#phase-2-server-selected-proof).

`start` and `serve -b` reexecute in a Linux daemon session and wait for genuine
startup readiness. Short options have descriptive long aliases; `-l` means listen,
`-L` means log directory, and `-p` selects the PID file (default launching
directory's `stuntgres.pid`). `--help` lists all commands and limits.

```sh
./stuntgres start -u ./users -l 5434 -d ./dumps -L ./logs -p ./server.pid
./stuntgres dump -p ./server.pid
./stuntgres stop -p ./server.pid
./stuntgres start -u ./users -l 5434 -d ./dumps -L ./logs -p ./server.pid -r
```

`dump` signals an asynchronous save; it does not promise that files are already
published when the command returns. `stop` waits for connection teardown, rollback
of open transactions and a final dump. Lifetime PID locks and executable/start
identity checks reject stale or conflicting records. Unix daemon/signal/PID and
password-update locking controls are Linux-specific; foreground networking remains
portable, with an explicit warning where PID controls are unavailable.

Dumps are atomically published0600 `<database>--<UTC YYYYMMDDhhmmss>.sql` files.
Repeated dumps do not overwrite earlier files. `-r` requires `-d` and restores
the newest valid file for every database before listening, including dump-only
databases without implicitly granting access. Corrupt newest files or missing
named owners fail startup; there is no older-file or ownership fallback.
Restored engines seed known userfile principals for retained ownership even after
a database grant is revoked; the network allowlist still denies that account.
Native export limitations remain visible. These are native SQL dumps, not arbitrary
pg_dump archive import or complete PostgreSQL backup compatibility.

Operational `server.log` and SQL `error.log` are JSONL, mirrored to stdout.
Without `-L`, both streams go only to stdout. SQL records include exact query,
SQLSTATE, primary message and `SYNTAX`, `UNSUPPORTED` or `SQL` classification.
Prepared errors retain the original source and associated Bind parameters:
ordinal, resolved OID/type where available, text/binary format, NULL and canonical
text or explicitly base64-encoded undecoded bytes. Simple-query records retain
`params:[]`. Authentication passwords and cancellation keys are not parameters.
Operational and SQL events retain separate file sinks; required log failures
are visible errors.

The server accepts PostgreSQL protocol3.0,3.1 and3.2; newer3.x minors negotiate
3.2. Protocol3.0/3.1 cancellation keys are4 bytes and3.2 keys are32 opaque bytes.
Simple batches and extended Parse/Bind/Describe/Execute/Close/Flush/Sync share
engine transaction and prepared-statement authority. Text and binary parameters
and results use existing type codecs, including NULL/empty and zero-row metadata.
Result RowDescription exposes domain base OIDs and typmods; parameter descriptions
retain the actual formal domain identity.
Finite Execute suspends and resumes a portal without replaying its effects;
Sync ends implicit extended work but preserves explicit transactions.
Wire prepared names interoperate with SQL PREPARE/EXECUTE/DEALLOCATE. Portals
have their own execution position and lifetime, not SQL cursor motion semantics.
COPY FROM STDIN and table/query COPY TO STDOUT stream text, CSV and binary through
the real engine, including actual lib/pq CopyIn and psql clients. Failed input
does not publish a valid prefix. SQL EXECUTE of a wire-prepared COPY retains the
same streaming exchange, including extended EXECUTE wrappers. Query output can
retain a real data prefix before an error, but never reports successful completion
for failed work. FunctionCall dispatches genuinely executable
routine OIDs with normal permissions and shared codecs; lookup-only routines
do not become callable merely because a catalog identity exists.
LISTEN/NOTIFY delivers channel, payload and notifier PID while the listener is
idle, with commit/rollback, ordering and listener-transaction delay preserved.
Notices, settings, errors, rows and notifications use one serialized writer.
Engine-owned transaction/idle-session timeouts deliver FATAL and close the socket,
without a ReadyForQuery, including expiry while no frontend query is arriving.
Passive peer-shutdown monitoring keeps disconnect cleanup live even when the
frontend queue is full: Linux uses epoll, BSD uses kqueue, and Windows uses
FD_CLOSE plus TCP_INFO (Windows10 1703/Server2016 or a compatible provider).
Other platforms/providers log an explicit warning and retain ordinary EOF-based
networking without claiming that passive queued-data monitor. Linux runtime paths
are exercised; non-Linux runtime/ACL proof requires those platform runners.
TLS, GSS encryption and additional authentication methods remain unavailable;
SSL/GSS negotiation is refused explicitly. Only cleartext password authentication
is implemented. Wire transport does not add absent SQL/type/routine providers,
server-file/PROGRAM I/O, WAL, replication or physical durability.
`server_version=18.0` is negotiation metadata, not a full PostgreSQL18 claim or
a promise that every client, catalog or extension feature is supported.
Dual-mode `TestReferenceServer*` definitions use real subprocesses, actual psql
and lib/pq alongside the current pinned PostgreSQL18.6 external oracle; historical
18.0 observations remain evidence from18.0, not newly observed18.6 results.
See [oracle policy](reference/fixture/README.md#postgresql-oracle-policy-and-provenance).
The standalone smoke separately
exercises parameterized/held statements, COPY formats, idle notification,
cancellation/recovery, JSONL records and dump/stop; see
[server verification](reference/sessions/README.md#phase-2-server-selected-proof).

Change note: the selected §9.19 array functions `array_position`,
`array_positions`, `array_remove`, `array_replace` and membership operators
`@>`, `<@`, `&&` reuse the existing typed array and leaf equality providers.
Search/removal/replacement match NULL elements; membership never matches them.
Position uses actual subscripts and an inclusive optional start. Search/removal
require one dimension, while replacement/membership accept rectangular arrays;
containment ignores dimensions, ordering and duplicate multiplicity.
Direct/prepared/held and supported stored-expression routes preserve metadata,
all-child privileges and demand-driven provider/collation errors. Nested
array-valued domain elements now reuse ordinary array equality; NULL/empty/search
fast paths remain intact. No new leaf providers or adjacent §9.19 functions
are implied.
See [array search testing](reference/binary_json_arrays/README.md#array-search-and-membership).
Feature smoke: `make check RUN='^TestReferenceArraySearchFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceArraySearchFeatureSmoke$'`.

Change note: scalar `IN`/`NOT IN` lists and scalar comparison operators with
array-valued `ANY`/`SOME`/`ALL` use the existing typed comparison and array
providers; array forms require an executable associated array/codec, not just a
scalar comparator. Results preserve SQL NULL semantics: empty arrays yield false for
ANY/SOME and true for ALL, even with a NULL left operand; a NULL array yields
NULL. Direct/prepared queries and supported stored-expression consumers retain
Boolean metadata and all-child authorization/dependencies. This is PostgreSQL
§9.25 scalar reduction, distinct from existing §9.24 subquery membership and
ordinary array-to-array comparisons; it does not add the §9.19 inventory.
See [selected array comparison proof](reference/binary_json_arrays/README.md#scalar-list-and-array-comparisons).
Feature smoke: `make check RUN='^TestReferenceArrayCompareFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceArrayCompareFeatureSmoke$'`.

Change note: `json_object_agg`, `jsonb_object_agg`, `json_object_agg_strict`
and `jsonb_object_agg_strict` collect key/value pairs through the ordinary
aggregate pipeline. Scalar keys use JSON text spelling; SQL NULL keys are
errors even when strict mode would omit the value. Local ORDER BY fixes member
input order. JSON preserves duplicate members; JSONB canonicalizes keys and
retains the latest admitted duplicate. Strict variants omit SQL NULL values;
PostgreSQL 18.0 binary strict also omits converted root JSON null members,
retaining the latest nonnull duplicate without removing nested nulls.
Zero admitted rows produce SQL NULL; admitted but wholly omitted input produces
an empty object. Prepared calls, stored views and native dump replay share the
same typed provider. Aggregate window execution remains outside this support.
Feature smoke: `make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$' REFERENCE=1`.

Change note: `json_object_agg_unique`, `jsonb_object_agg_unique`,
`json_object_agg_unique_strict` and `jsonb_object_agg_unique_strict` use the same
two-input aggregate pipeline with outer-key uniqueness. Duplicate converted keys
fail with SQLSTATE22030, including keys whose SQL NULL values strict mode omits.
NULL keys always fail; UNIQUE STRICT still validates and reserves omitted keys.
JSON reports duplicates during transition with the JSON-quoted key in its
message; JSONB checks at finalization before root null compaction, without a key
suffix. Nested objects retain ordinary JSON/JSONB semantics. Fixed result types,
local ORDER BY, pairwise DISTINCT, FILTER, independent ANY inference, held
statements and stored-view dump replay use the existing provider.
Feature smoke: `make check RUN='^TestReferenceJSONObjectUniqueAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceJSONObjectUniqueAggregateFeatureSmoke$' REFERENCE=1`.

Change note: SQL/JSON `json_objectagg(key VALUE value)` also accepts `key:value`,
NULL/ABSENT ON NULL (default NULL), and WITH/WITHOUT UNIQUE [KEYS].
Unique keys include SQL-NULL values omitted by ABSENT: text JSON checks each
transition, while JSONB checks at finalization before removing null members.
NULL keys always fail. RETURNING selects JSON/JSONB, UTF8 BYTEA or existing
compatible string/domain carriers with assignment length/domain checks. Without
RETURNING, an exact JSONB key/value selects JSONB; otherwise the result is JSON.
Input FORMAT JSON embeds JSON instead of quoting strings; output FORMAT JSON
selects a JSON intermediate even for RETURNING jsonb. Stored views and native
dump replay retain these clauses. There is no `jsonb_objectagg` alias; call-local
ORDER BY, DISTINCT/ALL, multiple pairs and aggregate windows remain excluded.
Feature smoke: `make check RUN='^TestReferenceSQLJSONObjectAggregateFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceSQLJSONObjectAggregateFeatureSmoke$'`.

Change note: SQL/JSON constructors and predicates support argument and ordered
correlated query arrays, VALUE/colon object members, FORMAT JSON, RETURNING,
NULL/ABSENT policies and recursive decoded-key uniqueness. Native
`JSON_SERIALIZE(jsonb)` returns correct canonical JSON text, intentionally not
the defective pinned PostgreSQL 18.0 bytes for the bounded two-member witness
([report](https://www.postgresql.org/message-id/03583FE7-A5EE-407C-907D-67865F3F8BFC%40gmail.com),
[followup](https://www.postgresql.org/message-id/CAGRkXqQXwh_gJmQE3PZj0v13omMPgFGBbb8TJoBFH8ECsAFCUg%40mail.gmail.com)).
Object keys must be scalar, not arrays, composites or JSON values. Domain
operands need an explicit admitted base-type cast for `IS JSON`; legacy
one- and two-array `json_object` calls remain ordinary function overloads.
They reject constructor clauses instead of discarding them; empty SQL/JSON objects
accept RETURNING only. Output ENCODING consistently requires exact BYTEA (not
a domain over BYTEA) and UTF8 across constructors and SQL/JSON aggregates.
Stored recipes keep column-assignment casts outside constructors; these are not
RETURNING clauses and do not change text-object construction into JSONB construction.
Predicates without UNIQUE validate syntax without decoding Unicode escapes;
WITH UNIQUE returns false for decoding failures, while throwing input/conversion
routes retain their diagnostics and cancellation still propagates.
Feature smoke: `make check RUN='^TestReferenceSQLJSONFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceSQLJSONFeatureSmoke$'`.

Change note: standard SQL/JSON `JSON_EXISTS`, `JSON_QUERY` and `JSON_VALUE`
share the JSONPath evaluator with ordered, typed PASSING bindings. Query
wrappers and scalar-string quotes, RETURNING conversions, and lazy ON EMPTY /
ON ERROR behaviors retain their syntax through stored expressions and native
Dump/Restore. JSON null is a present item (VALUE yields SQL NULL); no match
selects ON EMPTY even with a wrapper. Document/path input errors, demanded
missing variables, cancellation and native unsupported path operations are not
hidden by ON ERROR. Temporal PASSING retains SQL types for predicates and VALUE
conversion; JSON encoding of passed timestamptz is UTC, while VALUE text observes
the live output environment. Stored admission preserves temporal categories
through filter-current, filter output and lax array access; strict array access
clears them. Mixed-zone comparisons are STABLE, while same-zone and bare-variable
controls remain immutable. Advanced path methods, `_tz` routines and GIN
remain outside native support.
Feature smoke: `make check RUN='^TestReferenceSQLJSONQueryFeatureSmoke$'` and
`make check RUN='^TestReferenceSQLJSONQueryFeatureSmoke$' REFERENCE=1`.

Change note: `JSON_TABLE` is an implicitly lateral relational source with typed,
formatted JSON and EXISTS columns, INT4 ordinality, ordered PASSING, recursive
outer nested paths and union-style siblings. Missing subtrees receive typed NULL
without evaluating their column policies. A demanded invocation materializes all
rows and columns before publishing any row, including under LIMIT 1. Column
conversion/default policies reuse the SQL/JSON query providers; table ON ERROR
controls row paths only. Static schema, held authority/dependencies, existing DML
source consumers, stored views and native Dump/Reset/Restore use the ordinary
query lane. This does not add PLAN clauses, advanced JSONPath methods or GIN.
Immediate sibling row paths initialize left-to-right before descendant paths or
column effects. DEFAULT permits parameterized operators/functions, including
through specialized regclass wrappers, not bare parameters hidden by transparent
casts. Accepted shapes containing columns get the distinct column-reference
diagnostic; bare columns/special shapes remain generic errors. Ordinary scalar
binding precedes these restrictions; existing forbidden-family preguards remain.
An unnamed root reserves
`json_table_path_0` in the shared path/column namespace; an explicit root name
replaces that reservation.
Feature smoke: `make check RUN='^TestReferenceJSONTable(FeatureSmoke|ReviewRegressions|DefaultCastShapes|DefaultRegclassConstant|DefaultWrapperReferences)$'` and the
same selector with `REFERENCE=1`.


Change note: `json_agg`, `jsonb_agg`, `json_agg_strict`, `jsonb_agg_strict`
and SQL/JSON `json_arrayagg` collect values through the ordinary aggregate
pipeline. Local ORDER BY fixes element order; ordinary variants include SQL NULL,
strict variants omit it, and JSON null remains an element. Strict/default-ABSENT
all-NULL input yields `[]`; zero admitted rows yield SQL NULL.
`json_arrayagg` supports NULL/ABSENT ON NULL, formatted JSON input and applicable
RETURNING JSON/JSONB, UTF8 BYTEA, existing strings and unconstrained domains,
including assignment length checks. JSON retains embedded source representation;
JSONB canonicalizes objects without reordering aggregate arrays. Stored views,
held statements and native dumps retain the symbolic clauses. Existing domain
constraint/default/NOT NULL and aggregate-window capability boundaries remain.
Feature smoke: `make check RUN='^TestReferenceJSONArrayAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceJSONArrayAggregateFeatureSmoke$' REFERENCE=1`.

Change note: `json_array_length(json)` and `jsonb_array_length(jsonb)` return
the top-level array element count as INTEGER. Nested arrays/objects count as
one element, JSON null elements count, and SQL NULL inputs produce SQL NULL.
Object roots fail with `22023` / `cannot get array length of a non-array`;
other non-array roots fail with `22023` / `cannot get array length of a scalar`,
without an invented SQL error position. Each function accepts its own typed JSON
family or contextually inferred input; the opposite family requires an explicit
cast. Projection, predicates, aggregate consumers and stored default/generated/
CHECK/view/expression-index definitions reuse the ordinary expression pipeline.
Native Dump/Reset/Restore preserves symbolic calls and retained integer cursors.
This does not add extraction, operators, constructors or scalar-function FROM.
Feature smoke: `make check RUN='^TestReferenceJSONArrayLengthFeatureSmoke$'`
and `make check RUN='^TestReferenceJSONArrayLengthFeatureSmoke$' REFERENCE=1`.

Change note: `jsonb_set(jsonb_in jsonb, path text[], replacement jsonb,
create_if_missing boolean DEFAULT true)` and `jsonb_set_lax` immutably replace
an existing JSONB field/array element, or add a missing final item when enabled.
Missing intermediate parents are never invented. Negative array indices count
from the end; an out-of-range final index prepends when negative and appends
when nonnegative, without filling gaps. Empty paths preserve container roots;
scalar roots fail with `22023`. Path elements are validated only when reached.
`jsonb_set` is strict for SQL NULL. With SQL NULL replacement, lax defaults to
`use_json_null`; `delete_key`, `return_target` and `raise_exception` remain
distinct, case-sensitive treatments. JSON null is an ordinary JSONB value.
Named calls use catalog names `jsonb_in`, `path`, `replacement`,
`create_if_missing` and `null_value_treatment`; all written children bind and
evaluate through the ordinary expression pipeline. Typed JSONB preserves exact
numbers, canonical key order and shared immutable subtrees. Direct/held results,
stored default/generated/CHECK/view/index consumers and native replay retain
JSONB types and symbolic calls; failed writes/Restore and canceled work do not
publish partial state. Public path deletion is separately available through
`jsonb #- text[]`, and top-level deletion through `jsonb - text/integer`.
Text-array deletion and subscripting remain excluded; JSONPath array queries
are described in the navigation change note below.
Feature smoke: `make check RUN='^TestReferenceJSONBSetFeatureSmoke$'` and
`make check RUN='^TestReferenceJSONBSetFeatureSmoke$' REFERENCE=1`.

Change note: `jsonb_insert(jsonb_in jsonb, path text[], replacement jsonb,
insert_after boolean DEFAULT false)` inserts without overwriting object keys.
An existing final key raises `22023 cannot replace existing key`, with the hint
to use `jsonb_set` for replacement. Arrays insert before the selected element
by default, or after it when true; negative indices count from the end.
Out-of-range negative/nonnegative final indices prepend/append without gaps,
regardless of the after flag. Array/object replacements remain a single item.
Intermediate parents must exist, path validation is reached-only, and all four
SQL NULL operands are strict (JSON null remains a real value). Direct, prepared
and held results retain JSONB metadata and immutable input ownership. Stored
default/generated/CHECK/view/index recipes survive symbolic native Dump/Restore;
failed or canceled operations do not publish partial state. Final scalar
VARIADIC retains the ordinary formal slot, not array expansion.
Feature smoke: `make check RUN='^TestReferenceJSONBInsertFeatureSmoke$'` and
`make check RUN='^TestReferenceJSONBInsertFeatureSmoke$' REFERENCE=1`.

Change note: `jsonb #- text[]` removes a nested object field or array element
without modifying its input. Negative indices count from the array end;
missing paths and out-of-range indices preserve the document, with no parent
creation. Empty paths preserve containers. Whole SQL NULL operands produce
typed JSONB NULL; reached NULL path elements error, while unreached elements
are not inspected. Multidimensional paths fail with `2202E` rather than flatten;
scalar roots, including JSON null, fail with `22023`. Stored expressions,
native logical Dump/Restore and ordinary parameter inference use the same
operator. This does not expose `jsonb_delete_path(...)`.
Feature smoke: `make check RUN='^TestReferenceJSONBPathDeleteFeatureSmoke$'`
and the same command with `REFERENCE=1`.

Change note: `TRUNCATE [TABLE]` atomically empties named tables, ordinary
inheritance descendants and supported RANGE partition leaves, with `ONLY`,
`RESTRICT`/`CASCADE`, unchanged `CONTINUE IDENTITY` state and owned-sequence
`RESTART IDENTITY`. It uses exact TRUNCATE rights and transaction-owned ACCESS EXCLUSIVE locks, preserves table
and index definitions, and participates in rollback/savepoints. Stable snapshots
have PostgreSQL's documented non-MVCC TRUNCATE exception; already exported native
Rows remain owned buffers, while a live SQL cursor producer rejects truncation.
Other sessions' temporary inheritance branches are excluded without replacing
their storage or acquiring descendant AX locks; own-session TEMP children remain
included.
RESTART resets only sequences owned by selected columns (SERIAL, identity or
explicit OWNED BY), to their configured START rather than always1. Table/index
roots and fresh sequence bindings commit or roll back together; ordinary
nextval/setval allocations on an unchanged committed binding remain consumed.
Reset alone preserves session
currval/lastval, and rollback retains values locally observed on a reverted
replacement. Reset discards the initiating session's unused cache tail.
Restart requires sequence ownership and holds sequence ACCESS EXCLUSIVE until
transaction end. Native triggers and partition foreign keys remain unavailable;
registered foreign providers may supply grouped transactional truncation.
See [TRUNCATE](https://www.postgresql.org/docs/18/sql-truncate.html) and
[transaction semantics](docs/TRANSACTIONS.md#whole-table-generation-replacement).
Feature smoke: `make check RUN='^TestReferenceTruncateRestartFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceTruncateRestartFeatureSmoke$'`;
see [selected proof and standalone smoke](reference/dml/README.md#truncate-restart-identity-oracle-first-contracts).

Change note: FLOAT8 `cbrt`, `degrees`, `radians`, `erf`, `erfc`, `gamma`
and zero-argument `pi()` reuse direct/prepared and stored-expression consumers.
Unary NULL is strict; pi is immutable and row-independent. Gamma poles are
overflow errors, nonzero tails remain accepted, and erfc tail zero is valid.
Existing FLOAT8 and exact NUMERIC exp behavior remains unchanged. Finite libm
accuracy and boundary behavior can vary by platform; see
[math constants](https://www.postgresql.org/docs/18/functions-math.html)
and [selected proof](reference/numeric/README.md#math-constants-selected-proof).
Feature smoke: `make check RUN='^TestReferenceMathConstantsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceMathConstantsFeatureSmoke$'`.

Change note: readonly `pg_catalog.pg_sequences` exposes all eleven typed sequence
definition/value fields. `last_value` is logical reserved high-water (including
cache tails), NULL when uncalled or lacking SELECT/USAGE. Definitions remain
visible without schema/sequence privileges; own physical TEMP is included, peer
TEMP excluded. Every qualifier-admitted row takes transaction-owned sequence RX,
even definition/count/stored/join reads; FALSE/name-rejected rows do not.
See [sequence view contract](https://www.postgresql.org/docs/18/catalogs.html).

Change note: `CREATE TABLESPACE name [OWNER role] LOCATION 'path'` now parses
and validates live direct-superuser authority, owner, canonical POSIX path and
reserved name in PostgreSQL 18 order. Physical creation always fails explicitly
with 0A000; no directory probes, data placement or user catalog row are performed.
Read-only `pg_tablespace` exposes builtin identities and canonical owner OID 10
through `oid,spcname,spcowner,spcacl,spcoptions` (OID/NAME/OID/ACLITEM[]/TEXT[]).
Both arrays are initially typed SQL NULL; CREATE grants materialize genuine
descriptor ACLs in `spcacl` without enabling physical placement. Ordinary roles
see both complete builtin rows.
WITH is recognized but cannot apply or validate options without physical creation.
See [storage and catalog limits](docs/DIALECT.md#storage-and-native-controls).
Feature smoke: `make check RUN='^TestReferenceTablespaceFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceTablespaceFeatureSmoke$'`.

Change note: `ALTER TABLE ... SET TABLESPACE pg_default` validates live ownership,
relation kind and destination while taking ACCESS EXCLUSIVE, even for unchanged
placement. Tables, own TEMP tables, partitioned parents, indexes and materialized
views retain default placement and metadata without storage rewrites. Assignment
never recurses; mixed supported actions retain statement atomicity. Root action
preparation preserves written-order kind, original-column TYPE and destination
checks; execution-only movement checks do not preempt companion preparation or
nonrewrite CHECK/NOT NULL validation. The standalone
`ALL IN TABLESPACE ... OWNED BY ... SET TABLESPACE ... NOWAIT` validates roles
and endpoints before its same-default-space return, without selecting or locking
relations. Named nonshared pg_global and every bulk pg_global endpoint fail
explicitly. There is still no user tablespace or physical relocation provider.
See [storage limits](docs/DIALECT.md#storage-and-native-controls).
Feature smoke: `make check RUN='^TestReferenceAlterTablespaceFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceAlterTablespaceFeatureSmoke$'`.

Change note: individual `ALTER ... OWNER TO`, `REASSIGN OWNED` and
`DROP OWNED` now operate on represented schemas, relations, user types,
collations, the attached database and registered extension/function/dictionary
roots. Individual transfer uses inherited ownership and SET-role successor
authority; bulk source/target authority uses INHERIT. Associated indexes, row
types/arrays and owned sequences follow their canonical parent. Cleanup really
removes objects, dependent definitions and selected grant provenance atomically.
Canonical public remains a transferable/droppable bulk schema root, even after
renaming. Catalog namespaces remain protected. Bulk sources reject the canonical
initial principal and predefined roles before effects; a familiar role name or
ordinary superuser attribute is not initial-principal protection.
Sequence OWNER retains its existing transaction support; new ownership utilities
are autocommit-only. DROP OWNED preserves shared database/tablespace objects
while cleaning actual ACL provenance, including database CTc.
REASSIGN preserves creator-keyed default privileges; DROP OWNED removes selected
creators' actual policies and selectively cleans selected recipients in others'
policies. Tablespace ACL ownership does not add physical creation or placement.
Complete default creator/recipient unions remain atomic natively; the documented
pinned18.0 stale-default-address defect is not imitated.
See [ownership and cleanup](docs/AUTHORIZATION.md#ownership-and-bulk-cleanup).
Feature smoke: `make check RUN='^TestReferenceRoleOwnershipFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRoleOwnershipFeatureSmoke$'`.

Change note: native `CopyFrom` and `CopyTo` on a pinned `*sql.Conn` stream
PostgreSQL text, CSV and binary COPY through real type codecs and atomic DML staging.
CSV preserves quoted empty strings versus unquoted NULL, embedded separators/
quotes/newlines, HEADER MATCH, custom QUOTE/ESCAPE and column-selective FORCE
options. Table FROM STDIN, table/query TO STDOUT, ordered columns, defaults,
ACLs and transactions share existing authorities. Caller I/O runs without
engine/owner mutexes; same-owner commands wait on a cancellable reservation.
Binary uses PostgreSQL18 network-order framing and descriptor-owned scalar/nested
providers, preserving NULL/empty and exact numeric/temporal values. File/PROGRAM,
encoding overrides and error-skipping remain explicit capability boundaries.
Nullable constraint-free domain scalars/element arrays are actual receive/export
paths, distinct from excluded constraint DDL. Canonical numeric infinity bounds
retain valid native range/multirange values through typed reads and Dump/Restore.
Catalog CHAR and transaction identifier scalars supply real binary output.
Raw internal CHAR table declarations remain excluded.
See [COPY streams](docs/DRIVER.md#pinned-copy-text-streams--change-note).
Binary feature smoke: `make check RUN='^TestReferenceCopyBinaryFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceCopyBinaryFeatureSmoke$'`.
Review-regression proof: `make check RUN='^TestReferenceCopyBinaryReviewRegressions$'`
and `make check REFERENCE=1 RUN='^TestReferenceCopyBinaryReviewRegressions$'`.

Change note: externally supplied XID/CID unsigned32 and XID8 unsigned64 scalars now
support direct/held prepared values, typed NULL, fresh canonical decimal byte
results and distinct metadata. XID has equality/inequality (also INT4 on its
right), CID equality only, and XID8 unsigned comparisons/order. Explicit XID8→XID
retains low32 bits; string I/O casts do not add numeric/OID conversion edges.
Native Dump/Restore preserves scalar data, defaults, CHECKs and views; the shared
reference scenario replays the actual native dump on pinned PostgreSQL.
Existing NULLIF/simple CASE, unsigned XID8 GREATEST/LEAST and public default/CHECK
deparsing retain identifier authority. XID/CID extrema reject at execution rather
than borrowing their internal equality comparator as SQL ordering permission.
Identifier arrays remain lookup-only. These values do not synthesize epochs,
allocate PostgreSQL transaction/WAL/MVCC IDs, populate xmin/xmax/cmin/cmax/ctid or
add crash durability. Existing native transaction/prepared providers retain their
owned behavior. See [type contracts](docs/TYPES.md) and
[identifier reference proof](reference/type_resolution/README.md#transaction-and-command-identifiers).
Feature smoke: `make check RUN='^TestReferenceXIDFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceXIDFeatureSmoke$' REFERENCE=1`.


Change note: expression comparisons retain their resolved collation capability,
including hidden inputs of BOOL results and native stored-expression remapping.
C/POSIX ordering and equality remain bytewise; explicit annotations override
implicit conflicts, while reached ambiguous comparisons fail42P22.
Domain coercion preserves PostgreSQL's structural COLLATE precedence. Stored
legality and nested source opening share executable output demand while retaining
the complete frozen authorization and dependency manifests.
Hashed membership builds its RHS before LHS demand and retains it per actual
execution/source occurrence across unrelated parent rescans; a new Execute is fresh.
See [expression collation](https://www.postgresql.org/docs/18/collation.html)
and [selected proofs](reference/patterns_encoding/README.md#expression-collation-selected-proof).
Feature smoke: `make check RUN='^TestReferenceExprCollationFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceExprCollationFeatureSmoke$'`.

Change note: PostgreSQL18 `parse_ident(str text, strict boolean DEFAULT true)`
returns owned TEXT[] identifier components. Quoted names unescape doubled
quotes; unquoted names fold ASCII uppercase only, with no identifier truncation.
Nonstrict mode ignores suffix data only after a complete component; a consumed
dot still requires another valid identifier. Both catalog argument names work in
either written order, including retained/deparsed views and native Restore.
NULL in either supplied argument returns typed NULL before scanning. Malformed
input reports full English22023 diagnostics without a statement position.
See [identifier parsing proofs](reference/strings/README.md#identifier-parsing-selected-proof).
Feature smoke: `make check RUN='^TestReferenceStringIdentFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceStringIdentFeatureSmoke$' REFERENCE=1`.

Change note: PostgreSQL18 `split_part(text,text,int4)` returns a selected field,
and `string_to_array(text,text[,text])` returns generation-owned TEXT[].
`string_to_table` supplies real TEXT rows in targets and bounded FROM sources,
including function-only LATERAL correlation, WITH ORDINALITY and ROWS FROM.
Empty/NULL inputs, negative indexes, codepoint splitting and null-field markers
retain distinct scalar, array and set semantics. Target sets zip with NULL
padding and nest by level; stored read views and native controls retain them.
General function FROM and lateral derived SELECT/VALUES remain excluded.
See [string splitting](https://www.postgresql.org/docs/18/functions-string.html).
Feature smoke: `make check RUN='^TestReferenceStringSplitFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceStringSplitFeatureSmoke$'`.

Change note: MACADDR now owns typed six-octet values with all seven documented
forms, PostgreSQL18.6 scanner edges, canonical lowercase colon output and
unsigned byte-order comparisons. Direct/prepared textual parameters, nullable
metadata, defaults/CHECKs/views and native logical Dump/Restore share one codec.
Malformed syntax and octet ranges retain PostgreSQL English diagnostics.
Executable MAC address arrays, MAC helper functions, bitwise/truncation
operators and independent binary transport remain outside these scalar features.
See [MAC address contracts](docs/TYPES.md#mac-addresses) and
[selected reference proof](reference/types/README.md#macaddr-scalar-contract).

Change note: MACADDR8 now owns inline eight-octet values (OID774), accepts six-
and eight-octet spellings with consistent optional separators and surrounding
C whitespace, and emits canonical lowercase colon text. Six-octet inputs insert
FF:FE without flipping the modified-EUI bit. Released PostgreSQL18.6 scanner
edges, exact22P02 diagnostics, six strict unsigned scalar comparisons, textual
driver transport and typed logical replay share one codec. Its array identity775
stays lookup-only; default ordering/index classes, binary codecs and helpers are
not provided. Native metadata is MACADDR8; lib/pq reports an empty type name.

Change note: INET and CIDR now store typed IPv4/IPv6 scalar values through
direct/prepared binds, metadata and native Dump/Restore. INET preserves host
bits; CIDR input rejects them, while INET-to-CIDR casts clear them. Canonical
output and prefix-aware ordering distinguish network values from TEXT.
INET `~`, `&` and `|` return INET, as do address-plus-BIGINT (either order)
and address-minus-BIGINT; address-minus-address returns BIGINT. Operations use
the full IPv4/IPv6 address width, preserve host bits and enforce family/range
boundaries. CIDR operands implicitly convert to INET. Network-prefix predicates
`<<`, `<<=`, `>>`, `>>=` and `&&` return BOOL and ignore host bits; different
families return false. Network arrays and unlisted helper functions remain
outside this scalar feature.
See [network operators](https://www.postgresql.org/docs/18/functions-net.html#CIDR-INET-OPERATORS-TABLE)
and [network transport and boundaries](docs/TYPES.md#textual-structured-and-network-transport).
Storage smoke: `make check RUN='^TestReferenceINETFeatureSmoke$'`;
operator smoke: `make check RUN='^TestReferenceINETOperatorFeatureSmoke$'`.
Run each again with `REFERENCE=1` for the pinned PostgreSQL counterpart.

Change note: externally supplied TID tuple identifiers now retain scalar type
authority, canonical `(block,offset)` text and unsigned block/offset comparison.
PostgreSQL18's permissive input forms (including empty components) canonicalize
through the same codec for direct/prepared binds, stored expressions and native
Dump/Restore. TID exports and ScanType are string; typed NULL stays NULL.
Declared TID keys use real btree/hash providers. This does not populate `ctid`,
invent physical row locations, add development functions, execute TID arrays or
provide binary COPY. See [TID transport](docs/TYPES.md#tuple-identifiers) and
[selected proof](reference/types/README.md#tuple-identifier-selected-proof).
Feature smoke: `make check RUN='^TestReferenceTIDFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceTIDFeatureSmoke$' REFERENCE=1`.

Change note: `aclitem` and `aclitem[]` now use checked typed storage, ordinary
direct/prepared textual binds and captured canonical string results. PUBLIC,
SQL NULL, zero rights and grant options remain distinct data. All fifteen
PostgreSQL18 privilege letters are preserved; stars apply to the preceding
letter (`wr**` becomes `r*w`). Missing grantors default to bootstrap identity10
with PostgreSQL warnings. Scalar comparison supplies only equality; ACL-array
equality/inequality retain bounds, dimensions and NULL elements without adding
ordering or scalar binary I/O. Prepared literals retain role identity, while
returned rows retain captured spelling through replacement. Native Dump/Restore
remaps live grantee/grantor names into preexisting target roles; stale identities
reject Dump before writer I/O rather than being retargeted.
See [ACL item transport and limits](docs/TYPES.md#acl-items).
Feature smoke: `make check RUN='^TestReferenceACLItemSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceACLItemSmoke$'`.

Change note: ACL value functions now include `makeaclitem`, all thirteen
`acldefault` object kinds and `aclexplode` with OID/OID/TEXT/BOOL OUT fields.
Array/item containment (`aclitem[] @> aclitem`) checks a single entry's two
masks and endpoints, without unioning entries or adding scalar containment.
Numeric constructors retain zero/missing identities as inert values, not
authorization grants. Rank-one empty column ACL defaults survive typed
storage and lossless native replay; text `{}` remains rank zero. Dump rejects
zero grantors as well as stale identities before writer I/O.
Feature smoke: `make check RUN='^TestReferenceACLValuesSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceACLValuesSmoke$'`.


Change note: all sixteen PostgreSQL18 predefined role identities are seeded.
Live INHERIT membership supplies real read/write/schema/sequence capabilities;
`pg_maintain` uses substantive REINDEX and transaction-owned LOCK exclusion.
`pg_database_owner` is implicit for the live canonical attached-database owner and
owns initialized public, whose exact PUBLIC default grants USAGE but not CREATE.
Monitoring names do not unlock protected SET, and roles never synthesize missing
statistics, filesystem/program, checkpoint, subscription or signaling providers.
See [predefined role capabilities and limits](docs/AUTHORIZATION.md#predefined-role-capabilities)
and [small dual-mode proof groups](reference/roles/README.md#predefined-role-oracle-first-contracts).
Public test smokes:
`make check RUN='^TestReferencePredefined(DataFeatureSmoke|MaintainFeatureSmoke|BoundaryFeatureSmoke)$'`;
add `REFERENCE=1` for the same pinned PostgreSQL definitions. Executable smoke
evidence remains a separately run public Go program, not a test exit.

Change note: `DISCARD ALL|PLANS|SEQUENCES|TEMP|TEMPORARY` explicitly cleans
implemented physical-session resources without replacing the connection or
resetting durable data. PLANS preserves prepared definitions and invalidates
retained analysis; SEQUENCES forgets only owner memories/cache tails; TEMP uses
rollbackable dependency cleanup. ALL resets settings/authorization, SQL names,
cursors, TEMP, sequence memories and session advisory holds, and is prohibited
inside transaction blocks. Healthy automatic pool reuse is unchanged: issue
`DISCARD ALL` before returning a borrower when explicit cleanup is required.
See [cleanup categories](https://www.postgresql.org/docs/18/sql-discard.html) and
[physical ownership](docs/DRIVER.md#explicit-discard-and-pool-reuse).
Feature smoke: `make check RUN='^TestReferenceDiscardFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDiscardFeatureSmoke$'`.

Change note: parameterless direct `ExecContext` and `QueryContext` accept complete
multi-command SQL messages. A message parses before any effect, executes eagerly
in order, and retains its implicit transaction until the segment ends. Supported
utility definitions, roles/ACLs and DML publish together or roll back together.
`Exec` reports the final command count; `Query` retains every completed row set,
its own metadata, and any later execution error. Advancing result sets never
executes SQL. Parameterized direct calls and prepared definitions still require
one command (`42601` for a syntactically valid multi-command definition).
Concurrent materialized refresh participates in this existing retained-message
publication: multiple MV/index replacements publish together, BEGIN promotion
keeps them private, and a late error or cancellation rolls them all back. A
producer reading a concurrently refreshed source MV retains one coherent old
statement snapshot across its source waits; fresh statements see the new rows.
See [direct SQL messages](docs/DRIVER.md#direct-sql-messages--change-note)
and [message transaction boundaries](docs/TRANSACTIONS.md#retained-direct-message-transactions).
Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceDriverBatchFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceDriverBatchFeatureSmoke$'`.

Change note: Go handles and SQL prepared bodies retain analyzed relation identities
until referenced definitions, effective search_path, namespace callbacks or explicit
DISCARD PLANS invalidate them. Creating an earlier same-name table in an existing
path schema alone does not redirect a held SELECT or write target; fresh queries
see that shadow. Reanalysis retains original parameter/result signatures, refreshes
temporal/sequence/regtype captures atomically and keeps current data/ACLs/allowed
metadata. Native logical typed compounds retain their existing identity fences.
Statement-boundary retention does not bypass native ErrCatalogReplaced when a
new shadow changes original-name resolution during an admission wait.
See [prepared invalidation](docs/DRIVER.md#prepared-catalog-and-path-invalidation)
and [selected proof](reference/sessions/README.md#prepared-rebind-selected-proof).

Change note: `CREATE TABLE ... OF standalone_composite` copies live column names
and concrete types while keeping local defaults/constraints and a distinct row
type. `ALTER TABLE ... OF/NOT OF` changes only the source attachment. Standalone
`ALTER TYPE` attribute changes propagate atomically through typed tables and their
existing inheritance descendants with CASCADE; DROP TYPE follows real dependency
edges. Native Dump/Restore retains true attachment, including ordinary identity
and generated definitions exported before a real ALTER TABLE OF. CREATE and
attachment both require source USAGE (PostgreSQL 18.6). Source `ALTER TYPE ...
CASCADE` also requires ownership of every typed table and descendant it reaches.
A target-lock wait can select a
new earlier-path source; attachment retains that actual source's grant and fails
atomically on source timeout/cancellation.
See [typed tables](https://www.postgresql.org/docs/18/sql-createtable.html).
Selected native and pinned proofs: `make check RUN='^TestReferenceTypedTable'`
and `make check REFERENCE=1 RUN='^TestReferenceTypedTable'`.

Change note: SQL `PREPARE name [(types)] AS statement`, `EXECUTE name [(expressions)]`
and `DEALLOCATE [PREPARE] name|ALL` now use the existing typed binder and executor
for currently supported SELECT/VALUES, INSERT, UPDATE, DELETE and MERGE forms.
Definitions belong to the physical session, independently of Go statement handles
and transaction rollback; each EXECUTE imports a fresh parameter frame.
See [SQL prepared lifecycle](docs/DRIVER.md#sql-prepared-statements--change-note)
and [parameter rules](https://www.postgresql.org/docs/18/sql-prepare.html).
Feature family (900 seconds per invocation):
`make check RUN='^TestReferenceSQLPrepared'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceSQLPrepared'`.

Change note: CREATE TABLE FOREIGN KEY/REFERENCES supports single/composite
keys, MATCH SIMPLE/FULL and all five ON DELETE/UPDATE actions: NO ACTION,
RESTRICT, CASCADE, SET NULL and SET DEFAULT. DELETE SET actions accept a
stable referencing-column subset; UPDATE SET lists remain unsupported.
Canonical transaction-owned grants and one atomic command stage settle action
chains and local constraints before publication. Defaults use current child-owner
authority, without changing the invoking session. Supporting-index equality and
datum-image change detection have distinct roles. Native Dump/Restore retains
actions, selected attributes, forward references/cycles and live TEMP graphs;
invalid imports are rejected, not repaired by cascades.
Stable-isolation actions crosscheck both new/current and retained matching
children; post-snapshot conflicts are `40001`, including an originally empty
child root. Action settlement repeats exclusion completion only for newly
admitted event work, preserving unrelated deferred obligations without a
redundant pass.
See [foreign keys](https://www.postgresql.org/docs/18/ddl-constraints.html).
Smoke: `make check RUN='^TestReferenceForeignActionsFeatureSmoke'`; pinned oracle:
`make check REFERENCE=1 RUN='^TestReferenceForeignActionsFeatureSmoke'`.

Change note: `CREATE TABLE ... INHERITS (parent, ...)` merges compatible parent
columns, defaults, inheritable CHECK and NOT NULL constraints into a real,
identity-based graph. UNIQUE/indexes, identity ownership and foreign/primary keys
do not propagate. Native Dump/Restore retains ordered parents and local declarations;
DROP CASCADE removes descendants. SELECT includes descendants by default (also `p*`);
`ONLY p` / `ONLY(p)` retain the named physical table. Bag scans map parent columns
and authority, with physical descendant row locks and parent VIRTUAL recipes.
Generated physical-table recipes retain exact owner/type structural identity through
inheritance remapping; postwait parent frames are refreshed before recheck/projection.
UPDATE and DELETE use that same deduplicated descendant closure by default;
ONLY excludes descendants, and INSERT always targets exactly its named physical
table. Named-parent columns, ACL/RLS, RETURNING and diagnostic disclosure remain
separate from physical-child generation, constraints, indexes and row locks.
Supported ordinary ALTER actions propagate atomically according to their action:
columns/defaults, inheritable NN/CHECK and generated recipes recurse; identity,
security and physical key/index definitions do not. A parent PK can require
inherited NOT NULL without copying its key. Compatible ordinary INHERIT and
NO INHERIT update direct links without moving rows. Ordinary LOCK,
provider-CASCADE graph-shape mutation and new partition ALTER families retain
their explicit boundaries.
Native metadata-only DEFAULT and column/constraint rename admit additive
descendant growth before their one catalog stage, preserving current owners,
stable attributes and constraint origins. Genuine definition replacement,
catalog-lifetime replacement and cancellation still fence atomic publication.
Provider CASCADE uses the staged graph: a parent left standalone by complete child
retirement retains ordinary dependency projection. This includes schema-parent
retirement and a shared child's newly discovered removal through its other parent,
before classifying surviving column, DEFAULT or CHECK mutations.
Restore also makes imported descriptors yield to retained TEMP definition identities,
including local EXCLUDE constraints, without changing the retained TEMP graph.
See [table inheritance](https://www.postgresql.org/docs/18/ddl-inherit.html).
Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceInheritanceFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceInheritanceFeatureSmoke$'`.
Inherited-query smoke (900 seconds per invocation):
`make check RUN='^TestReferenceInheritanceQueryFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceInheritanceQueryFeatureSmoke$'`.
Inherited mutation/schema smokes (900 seconds per invocation):
`make check RUN='^TestReferenceInheritance(DML|Alter)FeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceInheritance(DML|Alter)FeatureSmoke$'`.
Change note: inherited recipe locality is independent of attribute locality.
Native Dump/Restore preserves nonlocal DEFAULT overrides/absence and retained
generated recipes with genuine ordered physical ALTER statements. Divergent
multi-parent generated recipes use compatible typed-NULL declarations only in
isolated replay, restoring every actual recipe before CHECKs and data. Captured
validated ONLY SET NOT NULL NoInherit facts use exact names and a narrowly paired
catalog repair before child declarations; PostgreSQL replay requires superuser
catalog-update authority. Public native catalog UPDATE remains denied.
Definition preparation acquires each action's real required grants before the
sole ALTER stage. Compatible merges/no-ops stop execution recursion without
erasing independently required preparatory locks or inline NN/CHECK propagation.
Retained child constraints after parent-column DROP preserve actual historical
contributions separately from live parent providers. Dump reconstructs those
counts from exact archival identities after genuine constraint completion, not
from fabricated local facts or current compacted attribute numbers.

Change note: `CREATE TYPE name AS (...)` and supported user table/view row types
now share concrete, immutable composite codecs. Named backing relations are
nonstorage authorities; member constraints remain confined to physical source
tables. Text input/output, nesting, plain domains, retained row-shape generations
and native mixed-type/table/view Dump/Restore are supported. Composite expressions
now add ROW constructors, stable field selection/expansion, whole-row references
and ordered partial INSERT/UPDATE/MERGE UPDATE targets, including mixed array/
field paths. INSERT VALUES expansion preserves every expanded member, including
prepared inputs and identity-bearing destinations. Stored computed expansions
retain domain/typmod conversions through native Dump/Restore. Generated columns
and expression indexes can select an immutable constructor field without
executing an unused volatile sibling; whole-constructor analysis and immutable
preflight remain required. Correlated RETURNING whole rows retain their lexical
OLD/NEW owner and nearer-name shadowing. Admitted scalar function-source aliases
remain scalar; ordinality/multiple-call aliases retain genuine tuple presence.
Anonymous constructed results report RECORD. Structural constructor predicates
support field-wise `=`, `<>`, lexical `<`, `<=`, `>`, `>=`, total Boolean
`IS [NOT] DISTINCT FROM`, and row IN/NOT IN/quantified subqueries, including
constructor field expansion. Ordinary composite comparisons use the separate
logical record provider described in the comparison change note.
Executable named-composite arrays support storage and textual I/O.
Named assignments preserve descriptor identity: equal-shaped distinct named
types are not interchangeable. Direct UPDATE/INSERT VALUES may contextually
construct anonymous ROW values; projected RECORD in INSERT SELECT requires an
explicit named conversion. Empty VALUES expansions retain ordinary width errors.
See [composites](https://www.postgresql.org/docs/18/rowtypes.html).
Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceComposite(FeatureSmoke|MixedControls|ControlLifetime)$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceComposite(FeatureSmoke|MixedControls|ControlLifetime)$'`.
Composite-expression smoke (900 seconds each):
`make check RUN='^TestReferenceCompositeExprFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceCompositeExprFeatureSmoke$'`.
See [expression behavior](https://www.postgresql.org/docs/18/rowtypes.html).

Change note: MONEY (OID790) stores exact signed eight-byte minor units and uses
captured C/POSIX/empty-as-C monetary policy for dollar output with two fraction
digits. Exact integer/NUMERIC assignment casts, checked monetary arithmetic,
integer truncating division and MONEY/MONEY FLOAT8 division reuse the existing
typed engine, metadata and atomic native Dump/Restore. MONEY exports Go string,
not a float; monetary I/O/casts are STABLE. Arrays, aggregates, host locales and
general function-style casts are not added. Native minimum-money/-1 integer
division safely rejects22003 rather than executing PostgreSQL18.0's unguarded
release-source division; no backend crash is asserted.
See [MONEY](https://www.postgresql.org/docs/18/datatype-money.html) and
[operators](https://www.postgresql.org/docs/18/functions-math.html).
Feature smoke: `make check RUN='^TestReferenceMoneyFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceMoneyFeatureSmoke$'`.

Change note: PostgreSQL18 transaction and physical idle timeout settings now
perform real cancellation, rollback, lock/resource release and terminal session
cleanup. They share existing SET/LOCAL/function/reset journals, SQL/Go controls,
savepoint nonrearm rules, fresh CHAIN budgets and timer-free prepared detach.
Accepted native fatal work is never replayed through ErrBadConn; an expired idle
pool owner can be discarded before a new command. No delay SQL or network server
is added. See [timeout behavior](https://www.postgresql.org/docs/18/runtime-config.html).
Feature smoke (900 seconds per invocation):
`timeout 900s make check RUN='^TestReferenceTimeoutFeatureSmoke$'`;
pinned counterpart:
`timeout 900s make check REFERENCE=1 RUN='^TestReferenceTimeoutFeatureSmoke$'`.
The additional root-owned standalone public Go smoke runs with
`timeout 60s go run ./.timeout-feature-smoke` from an ephemeral source directory,
then removes that directory after recording evidence. It asserts full native
diagnostics and counts, old-owner rejection, private/blocked8 rollback and AX
release, replacement identity/all five defaults0, and healthy INSERT9 affected1
with exact final INT4 rows7,9 and no99/private8/blocked8 replay. Reference selectors
do not replace this executable gate; see [testing](docs/TESTING.md).

Change note: permanent `CREATE/DROP MATERIALIZED VIEW` stores a typed query
result, including duplicate and NULL occurrences, independently of later base
writes. Nonconcurrent `REFRESH MATERIALIZED VIEW [WITH DATA]` reruns the frozen
producer as MV owner and atomically replaces rows; caller MAINTAIN is required.
`WITH NO DATA` clears population without source evaluation; reads fail55000.
Failure/cancellation before publication retains old rows, and native eager
cursors retain captured values/metadata. Public DML fails42809. Native
Dump/Restore preserves the exact last-refreshed stale rows/population through a
Restore-only comment snapshot, never query recomputation or writable MVs.
PostgreSQL replay restores definitions unpopulated; explicit refresh computes
the replayed base. Native explicit utility transactions remain excluded.
Logical MV index roots rebuild with refresh; heap/WAL/physical index swap,
crash durability and pg_dump are server-only.
See [materialized views](https://www.postgresql.org/docs/18/sql-creatematerializedview.html).
Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'`.
The root also runs a temporary standalone public Go program; these selectors
provide contract coverage, not a substitute executable proof.

Change note: autocommit `REFRESH MATERIALIZED VIEW CONCURRENTLY` admits ordinary
readers while producing data and serializes refreshes of the same object. The
target must already be populated and have a valid UNIQUE index over simple
columns, without a WHERE clause; INCLUDE and either NULL-distinct policy are
eligible. Missing eligibility fails55000 with PostgreSQL's index hint,
unpopulated targets fail0A000, and CONCURRENTLY plus NO DATA fails42601.
Concurrent refresh matches all eligible index keys and complete row images:
matched old rows survive once, matched new occurrences are suppressed, and
unmatched new occurrences are inserted. Thus matching nullable payload
duplicates can retain one old row; unmatched duplicates still obey uniqueness.
Full non-NULL image duplicates fail21000; remaining key conflicts fail23505.
Selected-key NULL groups bypass impossible old/new matches without collapsing
producer multiplicity or weakening NULLS NOT DISTINCT uniqueness. Duplicate
preflight retains the earliest producer-row diagnostic and stops once row zero
is established.
Rows, population and all logical index roots publish at one frontier; failures
and cancellation preserve prior data. RC statements retain coherent captures,
and RR readers retain their actual old rows across later concurrent publications.
Refresh fences the complete existing index descriptor set without treating that
observation as a REINDEX request; ordinary maintenance still requires its own
explicit rebuild intents.
Native Dump/Restore preserves the resulting stale rows and index eligibility.
PostgreSQL's generated SPI diff SQL/Context and physical temporary heaps are
server-only, not fabricated native diagnostics.
Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceMaterializedRefreshConcurrentFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentFeatureSmoke$'`.
The root also runs a temporary standalone public Go program; these selectors
are contract coverage, not executable-proof substitutes.

Change note: autocommit `ALTER MATERIALIZED VIEW` supports relation/column
rename, OWNER TO, SET SCHEMA, and DEPENDS/NO DEPENDS ON EXTENSION for genuinely
installed `unaccent`. Metadata changes retain rowtype/rule/attribute identities,
stored rows/population and frozen definitions. Existing CREATE INDEX forms also
accept MVs; owner/schema changes keep attachments coherent, and refresh/storage
and uniqueness-checked logical roots publish together. Native controls preserve
current metadata, AUTO_EXTENSION edges and exact stale rows, never ALTER history.
See [syntax](https://www.postgresql.org/docs/18/sql-creatematerializedview.html).
Feature smoke: `make check RUN='^TestReferenceMaterializedAlterFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceMaterializedAlterFeatureSmoke$'`.

Change note: readonly `pg_catalog.pg_tables` and `pg_catalog.pg_views` expose the
documented eight/four columns with exact current schema/name/owner identities.
Both disclose underlying objects independent of privileges and search_path, unlike
native's deliberately stricter `pg_indexes` policy. User definitions reconstruct
stored SELECTs without evaluation; represented builtin definitions are sourced
PostgreSQL18 compatibility text, not native-executable catalog implementations.
Index history remains true after dropping the last index; logical SQL Restore
normalizes history-only flags while real descriptor replacement preserves them.
No physical metrics or approximations are exposed.
See [table/view summaries](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'`.

Change note: read-only `pg_catalog.pg_timezone_names` and
`pg_catalog.pg_timezone_abbrevs` expose typed installed-provider lookup facts:
TEXT labels, INTERVAL offsets east of UTC and BOOL daylight flags. Names use the
captured transaction-start instant; abbreviations use that instant plus the
execution's TimeZone and Default/Australia/India table. Enumeration uses the
existing embedded IANA2025b provider, not host files or arbitrary accepted POSIX
expressions. See [timezone lookup views](https://www.postgresql.org/docs/18/sql-createview.html).
Smoke: `make check RUN='^TestReferenceTimezoneViewsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceTimezoneViewsFeatureSmoke$'`.

Change note: PostgreSQL18 `INSERT ... ON CONFLICT ... DO UPDATE SET ... [WHERE ...]`
supports the existing VALUES, DEFAULT VALUES and query producers, with an explicit
inferred or named unique arbiter and optional `AS` target alias. `excluded` is the
completed proposal; SET expressions share the unchanged target/proposal frame.
Mixed inserts and updates publish atomically, and affected counts include inserts
and TRUE updates, not FALSE/NULL filtered conflicts. Actual row identity governs
same-command cardinality, including command-inserted rows. Native hierarchy,
writable-view, exclusion-arbiter and deferrable UNIQUE DDL
boundaries remain explicit. Native rechecks current rights after conflict waits;
pinned PostgreSQL retains its statement-start ACL decision.
See [conflict update](https://www.postgresql.org/docs/18/sql-insert.html) and
[selected proof](reference/dml/README.md#insert-conflict-update-acceptance).

Change note: `INSERT ... RETURNING [WITH (OLD AS o, NEW AS n)] ...` exposes
ordinary direct/prepared query rows and fixed metadata for every successful
insert or conflict update. Unqualified and NEW values are the completed target
row; OLD is SQL NULL for insertion and the actual predecessor for conflict
updates. Skipped actions emit no rows. Supported stars, scalar expressions,
correlated subqueries and bounded bare named whole-row values use the shared
typed output path, including existing view and partition producers. Output
errors and missing SELECT rights abort the whole statement; Exec also evaluates
ignored output and keeps exact affected counts, never LastInsertId.
Native Dump/Restore retains persistent objects/data, rejects RETURNING in import
scripts and preserves already eager output across replacement. Generic ROW/field
operators, triggers/rules/FDWs, wire tuple metadata and physical durability are
not added. See [INSERT RETURNING](https://www.postgresql.org/docs/18/sql-insert.html)
and [selected proof](reference/dml/README.md#insert-returning-oracle-first-contracts).

Change note: PostgreSQL18 `MERGE` supports ordered conditional
`WHEN MATCHED THEN UPDATE SET` or `DELETE`,
`WHEN NOT MATCHED BY SOURCE THEN UPDATE SET` or `DELETE`,
and `WHEN NOT MATCHED [BY TARGET] THEN INSERT`. All three classes admit `DO NOTHING`.
Target-only candidates include every unmatched target occurrence, even with an empty source; their conditions,
assignment bounds and RHS see only the original target. Membership freezes before
row waits. Latest-row restarts preserve the target-only class; a formerly matched
pair can act on its surviving target then insert its retained source. Specifying
both outer classes requires the existing FULL-join legality rules. Mixed actions
share constraints and one atomic publication, including native Dump/Restore;
only accepted INSERT/UPDATE/DELETE mutations contribute to the native count.
Both missing-side families retain MERGE-local constant/parameter ON carriers:
nonzero integers and non-NULL TEXT match, while zero/NULL do not, including domains.
This does not relax ordinary JOIN or WHEN Boolean typing.
MERGE also supports `RETURNING` output expressions, source and target rows,
PostgreSQL18 OLD/NEW aliases and `merge_action()` (TEXT labels `INSERT`, `UPDATE`,
`DELETE`).
Only accepted actions emit rows. Unqualified target values use NEW for INSERT/
UPDATE and OLD for DELETE; OLD is absent on INSERT and NEW on DELETE. Absent
whole rows are SQL NULL, not present composites with all-NULL members.
Plain `RETURNING *` expands source columns before target columns. Action text
is lexical to MERGE RETURNING, including WHERE in its expression subqueries;
an outside SELECT WHERE call errors. It is not an ordinary callable function.
Output is eagerly completed after each accepted action, before the next proposal,
and exposed only after atomic publication.
Ignored Exec output is still evaluated; native affected counts remain exact,
while lib/pq reports zero for MERGE. Prepared output metadata is fixed even
when no arm is selected. Source/default/generated/identity values, current
predecessors, output-only SELECT demand and native Dump/Restore use the
existing mutation authorities.
Ordered WHEN clauses select the first TRUE condition in the eligible class;
FALSE/NULL fall through. An unconditional clause makes later same-class clauses
unreachable (42601), including interleaved and implicit/explicit BY TARGET clauses.
Selected DO NOTHING is terminal but acquires no mutation row grant and evaluates
no payload/default/identity/generated value or RETURNING expression. Its result
retains typed empty metadata. Genuine mutation waits can restart into omission;
a lost-ON BY SOURCE no-op still permits the retained BY TARGET source action.
Every specified no-op requires SELECT on at least one live target column;
actual references and specified mutation arms retain their startup privileges.
Native controls preserve omitted rows and successful mutations. Public PostgreSQL
row-wait proof and native private pending-resource proof distinguish causal
restarts from the native public timeout/recovery witness.
BY SOURCE INSERT and writable target views remain excluded.
Triggers, rules, FDWs, target hierarchy writes, procedural functions and
COPY(MERGE) remain external or adjacent mechanisms.
See [MERGE matching and actions](https://www.postgresql.org/docs/18/sql-merge.html).
MERGE RETURNING feature smoke (900 seconds per invocation; both modes):
`make check RUN='^TestReferenceMergeReturningFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceMergeReturningFeatureSmoke$'`.
MERGE omission feature smoke (900 seconds per invocation; both modes):
`make check RUN='^TestReferenceMergeNothingFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceMergeNothingFeatureSmoke$'`.
Feature smoke (900 seconds per invocation; run each in both modes):
`make check RUN='^TestReferenceMerge(Delete|SourceUpdate)FeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceMerge(Delete|SourceUpdate)FeatureSmoke$'`.
The complete implicit INSERT smoke union now lives in the external shared
`TestReferenceInsertImplicitShort` family, including
`TestReferenceInsertImplicitShortFeatureSmoke`; no native-only duplicate remains.

Change note: the thirteen session advisory lock signatures support bigint and
integer-pair keys, shared/exclusive modes, counted reentry, try and explicit unlock.
Ownership belongs to the physical connection: rollback and borrower/statement
close do not release it; actual session close does. Native Reset/Restore retain
held counts, while Dump excludes live ownership. The eight transaction advisory
signatures (`pg_advisory_xact_lock[_shared]` and
`pg_try_advisory_xact_lock[_shared]`, with bigint or integer-pair keys) share
conflicts with session locks but release automatically on transaction end and
rollback to the acquiring savepoint. There is no transaction unlock operation.
Bigint and pair key spaces remain disjoint; same-session requests can reenter
across lifetimes even with a queued competitor. PREPARE retains transaction
ownership, but rejects holding both lifetimes on the same key, even in different
modes. `pg_locks` aggregates same-process/key/mode grants across lifetimes and
shows detached prepared holders with NULL PID. Native replacement retires
transaction holds, not session holds. See
[advisory locks](https://www.postgresql.org/docs/18/functions-admin.html) and
[transaction advisory proof and consumer smoke](reference/locking_transactions/README.md#transaction-advisory-lock-reference-contract).
Feature smoke: `make check RUN='^TestReferenceAdvisorySessionFeatureSmoke'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionFeatureSmoke'`.

Change note: RANGE table definitions now retain typed, lexicographic tuple bounds,
MINVALUE/MAXVALUE, DEFAULT complements and nested partition keys. INSERT routes
through real physical leaves; SELECT and views capture the whole hierarchy.
Inherited defaults and CHECKs retain admitted identities, while fresh child
expressions and inherited user types retain their ordinary authorization checks.
Key collation identities, comparison capabilities and dependency closure remain
aligned across native Restore, provider DROP and TEMP-provider retirement.
Physical leaves may own enforced local UNIQUE/PRIMARY KEY/EXCLUDE constraints and indexes.
Named-target partition errors disclose supplied or SELECT-authorized columns,
not inaccessible omitted defaults. Bounds transform each finite datum, lower tuple
then upper; failed definitions retain reached sequence draws and select
positions/overlap witnesses by typed bound order. Native mutation, row-locking,
nonunique virtual-index/REINDEX and unsafe hierarchy alteration boundaries remain explicit.
Existing schema-wide table GRANT/REVOKE includes virtual roots and intermediates;
missing-key details use PostgreSQL expression and identifier quoting after privilege checks.
See [RANGE partitions](https://www.postgresql.org/docs/18/ddl-partitioning.html).
Feature smoke: `make check RUN='^TestReferenceRangePartitionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRangePartitionFeatureSmoke$'`.

Change note: HASH table definitions use PostgreSQL 18 extended scalar hashing,
positive modulus/remainder bounds and exact mixed-modulus factor/overlap validation.
Direct/prepared INSERT routes into real leaves; parent reads and result metadata
use the existing hierarchy paths. All concrete scalar hash classes remain available,
including numeric, JSONB, enum/domain and reference identities; no Go/FNV hash is used.
Hash provider coverage does not expand scalar storage: internal CHAR columns and
general casts retain their preexisting native 0A000 boundary.
Native Dump/Restore reroutes reminted identities and atomically rejects uncovered
residues or incompatible destination leaf constraints. RANGE behavior is unchanged;
LIST, public HASH ATTACH and unowned/external hash providers remain excluded.
Selected proof commands are in [table reference testing](reference/tables/README.md#testing).

Change note: RANGE `ALTER TABLE ... ATTACH PARTITION` retains an existing ordinary
or partitioned candidate's rows, physical column order, defaults and local keys.
Named schema, typed bounds, existing candidate/default rows and populated index
backings are validated before atomic membership publication. Parent SHARE UPDATE
EXCLUSIVE and candidate/default ACCESS EXCLUSIVE use the existing lock authority;
validated CHECK/NOT NULL proof can elide scans and proven DEFAULT descendant locks.
Explicit transactions/savepoints retain private topology. Validation observes
current committed rows without advancing a transaction's ordinary stable snapshot;
an already admitted waiting SELECT keeps its old branch topology.
Native controls preserve mapped layouts and provenance through ordinary CREATE
plus genuine ATTACH when PARTITION OF is insufficient, with unchanged logical
SQL replay on PostgreSQL. Other hierarchy mutation except ordinary DETACH and
external storage mechanisms remain excluded. See
[partition graphs](docs/TABLES.md#inheritance-typed-attachments-and-partition-graphs).
Feature smoke: `make check RUN='^TestReferencePartitionAttachFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePartitionAttachFeatureSmoke$'`.

Change note: ordinary `ALTER TABLE ... DETACH PARTITION` removes an immediate
partition edge without moving or deleting child rows, row types, defaults, local
constraints, indexes, ownership or privileges. Detached virtual intermediates
retain their own subtrees. Parent scans and held statements rebind to the reduced
tree; detached child scans remain readable. Parent ACCESS EXCLUSIVE, incumbent
DEFAULT and candidate/descendant admission use the existing lock authority.
Transactions and savepoints restore topology, inherited index/constraint edges
and private writes together. Cancellation and native replacement do not retarget
or partially publish an admitted detach.

Stored invalid virtual-index flags survive DETACH and table reattachment; only
genuine index attachment completion promotes validity. Native Dump/Restore
reconstructs standalone children and preserves invalid-but-complete virtual
indexes through executable ordinary SQL, including collision-safe empty replay
partitions only for childless invalid indexes. CONCURRENTLY/FINALIZE and hierarchy
foreign keys retain explicit native boundaries.

Physical and virtual local UNIQUE/PRIMARY backings retain distinct captured trees:
private replay uses physical USING INDEX or a conventional virtual definition
after the intended plain tree is attached, without enabling public hierarchy ADD.

Feature smoke: `make check RUN='^TestReferencePartitionDetachFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePartitionDetachFeatureSmoke$'`.

Change note: RANGE UNIQUE/PRIMARY KEY constraints cover every simple partition key;
real immediate-parent constraint/index ancestry connects virtual, storage-free
indexes to enforced physical leaf keys. UNIQUE NULLS NOT DISTINCT, primary NOT NULL,
future clones, recursive creation, ON ONLY and ALTER INDEX ATTACH PARTITION retain
their actual identities and nested validity. Selected attachment and key/index
drop support transaction overlays, savepoints and dependency-aware CASCADE.
Native Dump/Restore preserves renamed, manual, partial and nested trees and
collision-safe reminted references; emitted logical SQL replays on PostgreSQL.
Dependency-ordered declarations retain source identity priority across actual
retained-TEMP collisions without changing unrelated identities or declaration order.
Automatic same-stem child keys retain their declaration-order NULL policies and
immediate parents, including inherited keys before nested local keys.
Table-level PRIMARY KEY declarations on PARTITION OF use the same local key and
NOT NULL authorities. Stored scalar and opaque regclass references to renamed
virtual backings retain their identities through logical replay and byte-stable
redumps, including views, inherited inline definitions, late defaults and row values.
See [partitioned keys](https://www.postgresql.org/docs/18/ddl-partitioning.html).
Feature smoke: `make check RUN='^TestReferencePartitionConstraintsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePartitionConstraintsFeatureSmoke$'`.

Change note: readonly `pg_catalog.pg_indexes` exposes actual index schema,
table/name, NULL default tablespace and compact reconstructable definition.
Access method appears in CREATE INDEX text, not a sixth field. Native visibility
requires schema USAGE plus parent table SELECT or any live column SELECT,
intentionally stricter than PostgreSQL's unfiltered view. Held executions
refresh definitions/ACLs; eager rows retain their own prior metadata and values.
See [indexes](https://www.postgresql.org/docs/18/sql-createindex.html).
Feature smoke: `make check RUN='^TestReferencePGIndexesFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGIndexesFeatureSmoke$'`.

Change note: PostgreSQL UUID generation supports `gen_random_uuid()`, `uuidv4()`,
`uuidv7()` and `uuidv7(interval shift)`, returning typed UUID values. Version4
uses cryptographic random bits; version7 combines current UNIX milliseconds,
submillisecond time and cryptographic randomness. Successive unshifted/zero-shift
v7 calls on one physical session are strictly ordered, including across rollback
and native catalog replacement. Closed UUID defaults execute per inserted row;
views, CHECK, prepared calls and native Dump/Restore retain symbolic providers.
Finite shifts reuse session-zone calendar arithmetic and must fit the exact
48-bit timestamp guard; native follows PostgreSQL18.6 guards, while the pinned
18.0 oracle lacks those guards. See [UUID generation](https://www.postgresql.org/docs/18/datatype-uuid.html).
Feature smoke: `make check RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`.

Change note: seven physical-session presentation settings add UTF8
client_encoding aliases and SET NAMES, four bounded C/POSIX locale categories,
ICU validation reporting policy and extra_float_digits. Numeric and temporal masks
consume captured locale policies; SQL float textual consumers capture execution
precision, while exact native scalar transport and shortest canonical Dump remain
unchanged. Only lc_messages mutation consults the current role's real superuser
flag; unrelated privileged settings remain deny-only.
See [session settings](https://www.postgresql.org/docs/18/runtime-config.html).
Feature smokes: `make check RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'`.

Change note: numeric `to_char(integer|bigint|numeric|real|double precision,text)`
and `to_number(text,text)` use strict STABLE typed providers. Exact decimal and
integer values retain their precision; binary floats retain their rounding and
signed zero. Numeric masks support global `FM`, signs, fixed C decimal/group
and currency-space tokens, Roman numerals, ordinals, `V` and scientific output.
Parsing is template-driven and permissive, not a whole-input numeric cast.
Invalid constant masks fail only when execution demands a non-NULL call.
Closed UUID-generator DEFAULT trees compose through both numeric providers;
symbolic Dump/Restore keeps them execution-owned, without admitting unrelated
volatile defaults.
See [numeric formatting](https://www.postgresql.org/docs/18/functions-formatting.html).
Feature smoke: `make check RUN='^TestReferenceNumericFormattingFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceNumericFormattingFeatureSmoke$'`.

Change note: `convert(bytea,name,name)`, `convert_from(bytea,name)` and
`convert_to(text,name)` use PostgreSQL18's 42 encoding identities and 128 direct
conversion pairs, with a fixed UTF8 database. `encode(bytea,text)` and
`decode(text,text)` support exactly base64, escape and hex. Conversion validates
the entire source before dispatch; there is no automatic UTF8 pivot for missing
pairs. All five are strict; convert functions are STABLE, formats IMMUTABLE.
Existing BYTEA-function DEFAULT exclusion and integer-cast boundaries remain.
See [binary conversion](https://www.postgresql.org/docs/18/functions-binarystring.html).
Smoke: `make check RUN='^TestReferenceBinaryConversionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceBinaryConversionFeatureSmoke$'`.

Change note: CREATE [DEFAULT], ALTER and DROP CONVERSION manage independently
owned conversion objects backed by PostgreSQL18's 84 registered real routines.
CREATE validates raw encoding names, the exact six-argument integer-result
signature, EXECUTE authority and a real empty-input probe before publishing.
Nonempty conversion uses the current statement-owned default catalog/search
path; nondefaults never repair missing dispatch. CREATE checks one default per
schema/directed pair, while legal SET SCHEMA moves may leave multiple defaults.
Rename, move and ownership changes retain identity; system seeds retain pins.
Read-only `pg_conversion` exposes its eight actual columns, including symbolic
REGPROC `conproc` output. Routine aliases also admit ordinary input/storage as
described below; conversion controls do not load external ABIs or add wire transcoding.
Dump emits exact quoted definitions and collision-safe seed metadata deltas,
including legal duplicate-default replay. Reset restores canonical seeds and
Restore remaps actual owners/providers while preserving live target TEMP.
Conversion DDL retains the native autocommit-only explicit-transaction boundary.
Smoke: `make check RUN='^TestReferenceConversionFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceConversionFeatureSmoke$' REFERENCE=1`.

Change note: REGPROC and REGPROCEDURE carry unsigned four-byte routine identities.
REGPROC requires a unique visible name; REGPROCEDURE selects the exact input-type
signature, including schema-qualified types, canonical aliases, arrays and validated
modifiers. Both accept fixed numeric OIDs, zero/`-` and unknown numeric identities
as whole inputs; signature arguments require decoded SQL type syntax instead.
Symbolic output quotes SQL identifiers and uses statement-owned path/catalog
authority; retained rows never consult a later catalog. Lookup includes all 3397
source-fixed PostgreSQL18.0 routine identities without expanding pg_proc SQL rows
or authorizing execution. Installed unaccent identities remain actual provider
objects. Direct/held parameters, casts, columns, domains, arrays, defaults and view
constants use existing value/storage/analysis owners. Dump preserves overloaded
identities through qualified signatures and Restore remaps real extension identities.
Schema USAGE and CREATE can be granted independently of ownership. Routine alias
lookup remains distinct from execution and does not supply a routine runtime.
Basic scalar SQL CREATE FUNCTION is supported separately; CREATE PROCEDURE,
runtime-allocated information_schema/plpgsql routines and external procedural/
native-ABI providers remain unavailable native capabilities.
Smoke commands: `make check RUN='^TestReferenceRegProcFeatureSmoke$'` and
`make check RUN='^TestReferenceRegProcFeatureSmoke$' REFERENCE=1`. Selected native
and pinned checks also cover numeric/dash signature-type rejection and mixed
routine/REGROLE composite persistence, including receiving-catalog role identity,
restored-default evaluation and actual emitted-stream replay. Standalone public
API smoke independently exercised those reviewed remedies. See
[routine alias evidence](docs/design/regproc-pg18-oracle.md).

Change note: SQL `(start,end) OVERLAPS (start,end)` and start-plus-INTERVAL
pairs normalize reversed endpoints and use half-open periods, with equal
endpoints representing a single instant. Thirteen TIME/TIMESTAMP/TIMESTAMPTZ/
TIMETZ signatures share existing arithmetic and ordering; non-strict NULL
can establish overlap only from a known endpoint strictly inside a complete
period. DATE selection, Execute-time zones, stored definitions and native
Dump/Restore use the existing typed seams. TIME durations are converted only
inside OVERLAPS, not through a new global cast.
See [period overlap](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'`.

Change note: selected PostgreSQL18 windows support `row_number()`, `rank()`,
`dense_rank()`, `percent_rank()`, `cume_dist()`, `count(*)`, `sum(expr)`, `ntile`,
`lead`/`lag` and `first_value`/`last_value`/`nth_value` with default or explicit
RANGE/GROUPS/ROWS frames, typed offsets, partition/order keys
and inherited named `WINDOW` definitions. GROUPS requires window ORDER BY and
measures bigint offsets in complete peer groups: keys1,1,2,3,3 with1PRECEDING
give counts2,2,3,3,3. Bounds are inclusive and preserve NULL peers; numbering
restarts in each partition and ignores the frame.
ROWS measures physical positions, needs no window ORDER, and CURRENT ROW selects
only that occurrence. Three ordered rows with1PRECEDING count1,2,2; reversed
same-category bounds can give empty frames/count0. Offsets use bigint assignment
coercion and reject demanded NULL/negative values, even on empty input.
Windows run after WHERE/group/HAVING and before DISTINCT, outer
ORDER and bounds. Only outer ORDER promises presentation order. Prepared queries,
INSERT SELECT, captured views and pristine native Dump/Restore share this pipeline.
All four EXCLUDE modes mask completed membership: CURRENT ROW removes the current
occurrence, GROUP its ORDER peers, TIES its other peers, and NO OTHERS removes none.
Without window ORDER all partition rows are peers, including for ROWS masks.
Empty count is nonnullable bigint0; empty SUM is nullable NULL. SUM reuses existing
integer/numeric/real/double providers; MONEY and INTERVAL, window FILTER, count(expr),
DISTINCT, call-local aggregate ORDER and providers beyond the named functions remain excluded.
See [window queries](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQueryWindowRangeFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryWindowRangeFeatureSmoke$'`.
GROUPS feature smoke: `make check RUN='^TestReferenceQueryWindowGroupsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryWindowGroupsFeatureSmoke$'`.
ROWS feature smoke: `make check RUN='^TestReferenceQueryWindowRowsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryWindowRowsFeatureSmoke$'`.
EXCLUDE feature smoke: `make check RUN='^TestReferenceQueryWindowExcludeFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryWindowExcludeFeatureSmoke$'`.


Change note: `CREATE TEMP` / `CREATE TEMPORARY TABLE` creates physical-session
owned tables using the ordinary table/type/default/CHECK/identity/index path.
Owner `pg_temp` lookup shadows persistent relations; peers have independent
same-name tables. Default/`ON COMMIT PRESERVE ROWS` retains data,
`ON COMMIT DELETE ROWS` clears rows and index membership without restarting
owned sequences, and `ON COMMIT DROP` removes the dependency closure.
Creation/drop and actions compose with rollback/savepoints; physical close
removes TEMP, while returning a connection to its pool does not.
Native Dump excludes TEMP; Reset/Restore preserves the exact live owner objects.
Permanent transactional CREATE/DROP and GLOBAL/LOCAL spellings remain excluded.
See [temporary tables](https://www.postgresql.org/docs/18/ddl.html).
Feature smoke: `make check RUN='^TestReferenceTemporaryTableFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceTemporaryTableFeatureSmoke$'`.

Change note: `ascii(text)`, `chr(integer)`, `to_ascii(text[,integer|name])`,
`unistr(text)` and `unicode_assigned(text)` reuse the immutable, strict string
providers. `ascii` returns the first UTF8 codepoint (empty text returns0);
`chr` rejects NUL and invalid Unicode scalars. Explicit legacy `to_ascii`
conversion transliterates bytes, not Unicode accents; omitted encoding uses
UTF8 and reports unsupported conversion even for empty input. `unistr` decodes
four runtime escape forms and surrogate pairs; assignment uses vendored
Unicode16 categories rather than the host Unicode version. Stored consumers,
metadata and native Dump/Restore retain the selected NAME/INT4 overload.
See [string encoding](https://www.postgresql.org/docs/18/functions-string.html).
Feature smoke: `make check RUN='^TestReferenceStringEncodingFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceStringEncodingFeatureSmoke$'`.

Change note: PostgreSQL 18 `regclass` identifies tables, views, indexes and
sequences using quoted names, the effective relation search path or unsigned OID
input. Direct scalar/array columns, domains, supported numeric/text casts and
`ARRAY[typed_expression, ...]` constructors now compose with writes, stored
defaults, CHECKs, views and logical native Dump/Restore. Stored scalar input
constants retain identity and DROP dependencies; ordinary prepared SQL
reanalyzes its source spelling after invalidation. Numeric coercion expressions retain their
numbers, not cross-catalog identity promises. Constructor parameters infer
from regclass context or peers. Recursive view cascades remove their dependent
defaults/CHECKs, including same-owner DROP COLUMN and replaced-view chains;
replay keeps inline consumer prerequisites, including opaque array payloads
inside BOOL defaults without manufacturing DROP dependencies. Failing UPDATE
RETURNING rows use captured symbolic output without widening privilege
disclosure. See
[regclass relation identifiers](https://www.postgresql.org/docs/18/datatype-oid.html).
Feature smoke (900-second deadline per invocation):
`make check RUN='^TestReferenceRegclassFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceRegclassFeatureSmoke$'`.

Change note: `lpad(text,integer[,text])`, `rpad(text,integer[,text])`,
`repeat(text,integer)` and `reverse(text)` reuse the selected immutable string
providers. Padding counts UTF8 codepoints, repeats fill from its beginning and
truncates the source on the right; omitted fill is one space. Negative widths
and repeat counts produce empty text. `reverse(bytea)` reverses raw octets,
including NUL and invalid UTF8, rather than decoding them as text.
Strict NULL, character conversions, retained metadata and native Dump/Restore
use the existing typed seams. Oversized outputs report PostgreSQL54000 with
the two-argument pad wrapper Context preserved.
Feature smoke: `make check RUN='^TestReferenceStringPadFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceStringPadFeatureSmoke$'`.
See [padding and reversal](https://www.postgresql.org/docs/18/functions-string.html).

Change note: PostgreSQL 18 `age(timestamp[,timestamp])` returns symbolic
intervals with earlier-month borrowing, BC/AD and infinity semantics;
`isfinite(date|timestamp|interval)` returns a strict BOOL predicate.
Unary age uses transaction-current local midnight, not Prepare time.
See [age and finite predicates](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeAgeFiniteFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeAgeFiniteFeatureSmoke$'`.

Change note: PostgreSQL 18 `make_date`, `make_time`, `make_timestamp`,
`make_timestamptz` and `make_interval` construct exact typed temporal values.
These selected calls accept positional or named `=>`/`:=` arguments; interval
components independently default to zero. Negative years mean BC, TIME retains
24:00, and microseconds round to nearest/even. Timestamptz construction uses an
explicit zone or the Execute-time session zone; output uses the captured session.
The constructors compose in queries, writes and stored definitions, including
native Dump/Restore. General named-function binding remains excluded.
See [datetime constructors](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeMakeFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeMakeFeatureSmoke$'`.

Change note: `justify_days(interval)`, `justify_hours(interval)` and
`justify_interval(interval)` normalize 30-day months, 24-hour days and combined
mixed signs respectively. They preserve NULL and return unrestricted INTERVAL;
checked component overflow reports `22008`. See
[interval justification](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'`.

Change note: `EXTRACT(field FROM source)` and `date_part(text, source)` cover
DATE, TIME, TIMETZ, TIMESTAMP, TIMESTAMPTZ and INTERVAL. EXTRACT returns exact
NUMERIC with PostgreSQL display scales and division rounding; date_part returns
FLOAT8. Calendar/ISO fields, component-preserving intervals, signed infinities,
strict NULL and Execute-time zone fields share query and stored-definition paths.
The DATE date_part overload is a midnight TIMESTAMP wrapper, not DATE extraction.
See [date/time extraction](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeExtractFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeExtractFeatureSmoke$'`.

Change note: `date_trunc(text,timestamp|timestamptz|interval)` and
`date_trunc(text,timestamptz,text)` preserve typed temporal results, with fields
from microseconds through millennium. Session/explicit zones preserve each
fold occurrence for hour truncation and recompute day-or-larger boundaries.
DATE selects TIMESTAMPTZ; TIME uses the shared implicit INTERVAL conversion
(24:00 remains zero days and24hours). See
[datetime truncation](https://www.postgresql.org/docs/18/functions-datetime.html) for
the explicit naive-input timezone boundary and unsupported interval week.
Feature smoke: `make check RUN='^TestReferenceDatetimeTruncFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeTruncFeatureSmoke$'`.

Change note: `date_bin(interval,timestamp,timestamp)` and the matching
TIMESTAMPTZ signature floor-align fixed strides around an origin, including
microsecond boundaries and exact bins before the origin. Days mean24elapsed
hours; stored months/years are rejected. Mixed timestamp variants promote to
TIMESTAMPTZ; DATE/unknown selection follows PostgreSQL's exact/preferred rules.
Results preserve the selected type without inheriting input precision.
See [datetime binning](https://www.postgresql.org/docs/18/functions-datetime.html) for strict
NULL, infinity/error precedence, coercion stability and native stored replay.
Feature smoke: `make check RUN='^TestReferenceDatetimeBinFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeBinFeatureSmoke$'`.



Change note: PostgreSQL BIT and BIT VARYING/VARBIT scalar storage preserves
leading zeroes, empty values and NULL through direct/prepared statements,
textual parameters, metadata and native Dump/Restore. Raw B/X literals and
assignment enforce exact fixed widths or varying maxima; no padding/truncation
or bit casts/arrays are introduced by storage. Scalar operators are described
in the [operator contracts](reference/binary_json_arrays/README.md#bit-string-scalar-operators). See
[bit strings](https://www.postgresql.org/docs/18/datatype-bit.html).
Expanded feature smoke (including stored-format replay, modifiers and parameter lifecycle): `make check RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'`.

Change note: SQL cursors support `DECLARE [NO SCROLL|SCROLL] CURSOR
[WITHOUT HOLD|WITH HOLD]`, signed-count/ALL FETCH and MOVE with
forward/backward/absolute/relative motion, and CLOSE/name/ALL.
Default/WITHOUT HOLD requires an explicit transaction; WITH HOLD also works in
autocommit. DECLARE retains visibility without drawing targets; FETCH publishes
detached eager batches under fresh execution authority. MOVE has no result schema
and reports the corresponding FETCH count. Explicit SCROLL retains execution
across EOF; rewind rescans node-specific demand while preserving ORDER caches.
Omitted mode remains conservatively forward-only.
Creating commit drains NO SCROLL HOLD's unfetched remainder; SCROLL HOLD rewinds
the retained tree, materializes the full typed result and restores its exact
position/endpoints. Export releases producer/source pins and preserves names
across later transactions. Stored FETCH/MOVE never re-evaluates query targets.
Promotion is per portal, not atomic with later drain/publication failure.
Transaction end closes unexported ordinary/newly aborted cursors; physical end and
CLOSE release stores. Savepoints do not rewind surviving movement or undo CLOSE.
Native prepared FETCH uses the current schema, not lib/pq's stale description.
See [SQL cursor ownership](docs/DRIVER.md#sql-cursors--change-note).
Feature smoke: `make check RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`.
HOLD smoke: `make check RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`.

Change note: `DELETE FROM target WHERE CURRENT OF cursor_name` deletes only the
physical occurrence most recently visited by FETCH or MOVE on a live updatable SQL cursor.
Bounded FETCH or MOVE positions on its last visited row; duplicate values and omitted
projected keys do not change identity. DELETE does not move the cursor; repeating
it after successful deletion reports zero. Prepare is quiet and each execution
resolves the current named incarnation. USING/RETURNING retain their normal
scopes, metadata and atomic publication. Savepoint rollback restores data without
rewinding older cursor movement; full transaction end closes ordinary names.
Exported HOLD has no live physical source and CURRENT OF reports24000.
See [positioned DELETE](https://www.postgresql.org/docs/18/sql-delete.html).
Feature smoke: `make check RUN='^TestReferenceDeleteCurrentOfFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDeleteCurrentOfFeatureSmoke$'`.

Change note: `UPDATE target SET a=b,b=a WHERE CURRENT OF cursor_name` updates only
the last visited live physical occurrence, including MOVE, never projected-value matches or a re-run
cursor query. Repeated/same-value updates count one and use the latest old row;
SET is simultaneous, FROM/RETURNING keep their existing contracts, and UPDATE
does not move the cursor. Normal row grants and cancellation-sensitive private
publication apply. Rollback restores data without rewinding older cursor motion.
Committed Dump/Restore preserves changed rows, not cursor/private state.
Transparent cursor views remain eligible for base targets; CURRENT OF on a view
UPDATE target rejects with PostgreSQL0A000 at analysis/Prepare.
See [positioned UPDATE](https://www.postgresql.org/docs/18/sql-update.html).
Smoke: `make check RUN='^TestReferenceUpdateCurrentOfFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceUpdateCurrentOfFeatureSmoke$'`.



Change note: `PREPARE TRANSACTION 'gid'`, `COMMIT PREPARED 'gid'` and
`ROLLBACK PREPARED 'gid'` support process-local two-phase transaction state.
Preparation detaches private writes and transaction-owned locks from the session; physical
origin close does not discard them. Only the exact preparing principal or a
current superuser can finish them. `pg_catalog.pg_prepared_xacts` exposes
`transaction XID, gid TEXT, prepared TIMESTAMPTZ, owner NAME, database NAME`.
Native preparation is always enabled per Engine, without crash persistence,
cluster-wide databases or a capacity setting. Reset/Restore refuse while entries
exist; Dump remains committed-only. Executed TEMP table/sequence access prohibits
preparation. A healthy current-transaction unexported HOLD cursor also prohibits
PREPARE; closed/failed-after-recovery/prior exported entries do not. Transactions
that executed LISTEN/UNLISTEN/NOTIFY cannot prepare; existing subscriptions alone
are allowed. Snapshot export remains excluded. Nonholdable cursors close on detach while
exported HOLD stores survive. See [prepared transactions](docs/TRANSACTIONS.md#prepared-transactions).
Feature smoke: `make check RUN='^TestReferencePreparedTransactionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePreparedTransactionFeatureSmoke$'`.

Change note: `REGROLE` (OID4096, array4097) resolves role names through the
existing immutable role catalog, folds unquoted identifiers and quotes output
when required (including `quote_all_identifiers`). Exact numeric input accepts
unknown unsigned OIDs; zero and `-` display `-`. Digit input uses PostgreSQL's
OID scanner, including `010` →8 and invalid octal `08`. Role aliases do not add
authentication, ACL operations, role rename or `pg_authid` projection.
Only a non-NULL scalar REGROLE Const is prohibited in stored expressions:
`0A000 constant of the type regrole cannot be used here`, without a position.
Typed NULL, runtime TEXT→REGROLE, numeric coercion trees, column values and opaque
REGROLE[] constants remain legal; wrapping a forbidden scalar in another cast,
CASE or ARRAY constructor does not hide its child. Unknown symbolic input still
fails resolution first. Held literals retain their analyzed OID until ordinary
analysis invalidation, including `DISCARD PLANS`; held input parameters and
runtime text casts use execution authority. Retained rows keep captured spelling.
Native Dump writes live role names and unknown numeric bits, never CREATE ROLE.
Provision referenced roles in a different Driver before Restore; symbolic data
resolves destination identities. These are logical process-local APIs, not
cluster-role export, binary protocol/native ABI, password/network authentication
or disk durability. See [type contracts](docs/TYPES.md#catalog-valued-and-supporting-types)
and [selected proof and public-program smoke](reference/type_resolution/README.md#regrole).

Change note: `REGTYPE` (OID2206) and its array support PostgreSQL type-name
input, unsigned OID casts, symbolic output and stored scalar/domain/array values.
Lookup covers all 621 released/initialized type identities and real table/view
row types without admitting their unsupported value codecs. Standalone input
uses the shared grammar, qualified/current-database names and captured type path.
Held literal identity follows effective lookup authority, not referent revision;
retained rows own their old output snapshot. Stored definitions retain TypeClassID
dependencies; native Dump/Restore uses qualified names and atomic identity remapping.
Reciprocal associated-array REGTYPE constants replay as qualified `element[]`.
Logical replay may remint OIDs and generated internal array names; it preserves
exact logical type identity, relationships, values and declared metadata, not
historical allocator aliases. Live displacement and retained output stay exact.
Standalone identifier clipping emits the PostgreSQL NOTICE, including input and
parameter context. Internal row/array types share their relation's ownership
dependency; catalog allocation skips type identities after native replacement.
Persistent replacement rebinds seeded lookup-only domain/array bases to retained
builtin generations without granting value codecs or replacing imported catalog ownership.
Selected REGTYPE IO coercions capture symbolic output authority for VARCHAR and
TEXT/VARCHAR assignment, including domain sources and stored defaults. Unrelated
role creation/drop does not retarget held literals or repeat their analysis notices;
qualified schema lookup still enforces current USAGE after ownership changes.
See [REGTYPE contracts](https://www.postgresql.org/docs/18/datatype-oid.html) and
[design/inventory](docs/design/PG18-TYPE-REG-TYPE.md).
Feature selector: `make check RUN='^TestReferenceRegtype'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRegtype'`.

Change note: `REGCOLLATION` (OID4191, array4192) and `REGNAMESPACE`
(OID4089, array4090) support unsigned OID identities, symbolic input/output,
stored scalar/domain/array values and logical native replay. Namespace input
is exactly one folded/quoted schema identifier, independent of search path and
USAGE. Collation lookup accepts schema/current-database qualification and uses
encoding applicability, the effective collation path and schema permissions.
Hidden identities display qualified names; represented ICU/libc identities
remain lookup-capable without enabling unsupported comparison execution.
Exact digits use OID input; zero/`-` display `-`, unknown OIDs remain decimal.
Stored scalar literals retain class-aware dependencies; opaque rows do not.
Native replay remaps named identities by catalog class rather than preserving
allocator numbers. Relevant DROP/recreate and rename reanalyze held literal
source spelling; already-returned Rows retain their original presentation.
Explicit TEXT/CHARACTER/VARCHAR/NAME casts preserve captured lookup/display;
character typmods use normal explicit truncation or assignment sizing.
Untyped nil and typed-nil byte parameters are SQL NULL; nonnil empty bytes
remain invalid name input, and held statements recover after errors.
See [type contracts](docs/TYPES.md#catalog-valued-and-supporting-types) and
[selected dual-mode proof](reference/type_resolution/README.md#regcollation-and-regnamespace).
Feature smoke (900-second external deadline per invocation):
`make check RUN='^TestReferenceRegCollationFeatureSmoke$'`;
pinned counterpart:
`make check RUN='^TestReferenceRegCollationFeatureSmoke$' REFERENCE=1`.

Change note: `UNION`, `INTERSECT` and `EXCEPT` support default/explicit
`DISTINCT` and `ALL`, full-tuple NULL equality and multiset counts. Parentheses
preserve local ORDER/bounds; INTERSECT binds tighter than UNION/EXCEPT.
Corresponding columns resolve common types pairwise, with leftmost output labels;
unordered results have no implicit sequence guarantee. Derived/scalar/EXISTS,
INSERT sources and stored views share these semantics, including native
Dump/Restore. TABLE and LATERAL are not added by the set-operation change;
SELECT CTEs are described in [WITH queries](https://www.postgresql.org/docs/18/queries.html).
See [set operations](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQuerySetOpsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsFeatureSmoke$'`.

Change note: ordinary `CREATE TABLE [IF NOT EXISTS] name [(columns)] AS query
[WITH [NO] DATA]` and top-level `SELECT ... INTO [TABLE] name` create independent
typed tables using the current query pipeline. Populated commands execute the
producer once and return its stored-row count, never its result columns.
`WITH NO DATA` analyzes the query and imports parameters without planning or
running it; an existing `IF NOT EXISTS` target likewise skips runtime work.
Schema and initial rows publish atomically. Native explicit-transaction DDL
exclusion and early read-only admission precede safe-snapshot acquisition; temporary/storage options and
new query providers are not added. Native Dump/Restore reconstructs ordinary
definitions/data, including already supported catalog output types, without
exposing additional raw declaration types.
See [query-created tables](https://www.postgresql.org/docs/18/sql-createtable.html)
and [query consumer phases](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQueryTableCreateFeatureSmoke'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryTableCreateFeatureSmoke'`.

Native `InspectIndex(ctx, db, schema, name)` observes detached `Ready`, `Valid`
and `Live` flags using exact decoded names and an unwrapped native pool.
`WaitingForOlderSnapshots` additionally reports a live builder's actual admitted
older-snapshot cohort, after validation snapshot ownership is dropped.
`WaitingForWriters` reports a frozen granted-writer cohort: `Ready=false`
identifies the first capture and `Ready=true` the second. Both observations
clear on phase exit; neither promises individual waiter registration.
It shares existing descriptor metadata authority; it does not add SQL index
catalogs or a query-plan promise. See [driver controls](docs/DRIVER.md#b-tree-utility-preparation-and-controls).

Change note: `CREATE [UNIQUE] INDEX CONCURRENTLY` now publishes real logical
B-tree roots through nonready, ready-invalid and valid descriptor phases.
Captured writer cohorts and database-wide endangered snapshot owners use the
existing lock/deadlock authority; later ordinary writers remain admissible.
Ready unique indexes enforce full computed/provider-aware tuples before Valid.
Failed/canceled builds retain their last phase; ordinary REINDEX repairs only
on successful atomic rebuild. Dump refuses persistent invalid physical indexes before
writing, and valid standalone UNIQUE definitions replay through the normal parser
and existing isolated Restore authority, including NULLS NOT DISTINCT.
Explicit concurrent blocks fail25001; ordinary public standalone UNIQUE remains
supported. Partial indexes and concurrent DROP/REINDEX remain excluded.
See [concurrent index design](docs/design/PG18-INDEX-CONCURRENT.md).
Feature smoke: `make check RUN='^TestReferenceConcurrentIndexFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceConcurrentIndexFeatureSmoke$'`.

Change note: PostgreSQL 18 index capability inquiries support
`pg_indexam_has_property(oid,text)`, `pg_index_has_property(regclass,text)` and
`pg_index_column_has_property(regclass,integer,text)` with strict nullable BOOL
results. Existing index definitions determine direction, NULL ordering and
INCLUDE applicability; fixed builtin AM inquiries do not enable unowned AM DDL.
Unknown/inapplicable properties and invalid numeric objects/columns yield NULL.
Existing native Dump/Restore preserves definitions without extra builtin state.
See [index capability inquiries](docs/design/PG18-FUNC-INFO-INDEX-PROPERTIES.md).
Feature smoke: `make check RUN='^TestReferenceIndexPropertiesFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceIndexPropertiesFeatureSmoke$'`.

Change note: PostgreSQL date/time arithmetic supports calendar date and timestamp
operators, elapsed subtraction, offset-preserving clocks and component-exact
interval arithmetic/scaling. Zoned calendar arithmetic observes embedded DST
rules and remains STABLE even for a zero interval. Existing comparisons, metadata,
prepared execution and native Dump/Restore retain their owning contracts.
See [date/time arithmetic](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeArithmeticFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeArithmeticFeatureSmoke$'`.

Change note: SQL `CURRENT_DATE`, `CURRENT_TIME[(p)]`,
`CURRENT_TIMESTAMP[(p)]`, `LOCALTIME[(p)]` and `LOCALTIMESTAMP[(p)]` derive
from the top-level transaction start, with microsecond precision and session
TimeZone presentation. Held prepared expressions remain dynamic across transactions;
savepoints do not restart their clock. Precision0–6 rounds, while unsigned higher
precision clamps to6 with PostgreSQL's analysis-time warning. Zoned/local result
types remain distinct. See [current date/time](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeCurrentFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeCurrentFeatureSmoke$'`.

Change note: `now()` and `transaction_timestamp()` are identical transaction-start
TIMESTAMPTZ providers; `statement_timestamp()` captures each command's start.
`clock_timestamp()` and TEXT `timeofday()` sample the wall clock when demanded.
All five are zero-argument calls, with microsecond precision, execution-time
TimeZone presentation and symbolic DEFAULT/CHECK/view/native replay.
See [transaction and statement time](https://www.postgresql.org/docs/18/functions-datetime.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'`.


Change note: JSON scalar storage now validates syntax while retaining exact
source whitespace, key order, duplicate keys, numbers and escape spelling.
SQL NULL and JSON `null` remain distinct. Direct/prepared columns, bound values,
typed literals, explicit TEXT↔JSON casts, metadata and native Dump/Restore use
the existing typed seams, including generic JSON arrays and domains. JSON has
no ordinary comparison/grouping/index-key provider, but supports INCLUDE payloads.
Missing default classes report42704 even during atomic ALTER TYPE reconstruction.
Unicode-escape physical EOF reports an invalid token, distinct from present nonhex
digits. Unselected JSON operators and functions remain excluded.
See [JSON types](https://www.postgresql.org/docs/18/datatype-json.html).
Feature smoke: `make check RUN='^TestReferenceJSONFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONFeatureSmoke$'`.

Change note: `json_object(text[])` and `json_object(text[], text[])` construct
ordered JSON objects from alternating flat pairs, two-column arrays, or parallel
one-dimensional key/value arrays. Bounds are ignored, duplicate keys remain in
order, values are JSON strings (or JSON null for NULL elements), and whole-array
SQL NULL is strict. Malformed shapes/counts and NULL keys use PostgreSQL diagnostics.
Qualified explicit final `VARIADIC` array syntax selects these same signatures;
it does not add a variadic overload. Stored consumers and native Dump/Restore
retain symbolic calls. JSON remains noncomparable; generic builtin catalog
enumeration is unchanged.
See [JSON construction](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING).
Feature smoke: `make check RUN='^TestReferenceJSONObjectFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONObjectFeatureSmoke$'`.

Change note: `to_json`, `to_jsonb`, `array_to_json`, `row_to_json`,
`json_build_array`, `jsonb_build_array`, `json_build_object`, and
`jsonb_build_object` recursively convert admitted typed scalars, domains, arrays
and composite rows. Unary/container conversion is strict; builder NULL arguments
become JSON null. Container pretty flags insert only top-level line breaks.
Ordinary builder array arguments remain nested; explicit `VARIADIC` flattens the
array in storage order, with a whole NULL array producing SQL NULL.
JSON retains embedded source spelling and duplicate keys; JSONB validates
embedded JSON, canonicalizes objects and keeps the last coerced duplicate key.
Object keys must be non-NULL scalars. Dates/timestamps use ISO/XSD spelling
independent of DateStyle; timestamptz and ordinary scalar output use live session
settings. All eight functions are STABLE, including empty builders: defaults,
checks and views retain symbolic calls, while immutable-only index/generated
consumers reject them. Native Dump/Restore preserves typed stored operands.
Arbitrary user JSON casts and unselected processing operators/aggregates are not
added by this change. Composite arrays use the
existing recursive conversion providers.
Feature smoke (900 seconds each):
`make check RUN='^TestReferenceJSONCreateFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceJSONCreateFeatureSmoke$'`.

Change note: `jsonb_object(text[])` and `jsonb_object(text[], text[])` use the
same flat/two-column/parallel pairing and strict NULL rules, but construct genuine
structural JSONB objects. Keys normalize by UTF8 byte length then bytes, and the
last duplicate wins, including a final NULL value. Nonnull values always remain
JSON strings: numeric, Boolean and JSON-looking text is not parsed. NULL keys
and malformed shapes/counts retain PostgreSQL English diagnostics. Direct and
prepared calls infer TEXT[] parameters; supported defaults, generated columns,
CHECKs, views, expression indexes and native Dump/Reset/Restore retain symbolic
calls and normalized JSONB metadata, equality and grouping.
Feature smoke: `make check RUN='^TestReferenceJSONBObjectFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceJSONBObjectFeatureSmoke$' REFERENCE=1`.

Change note: `json_object_keys(json)` returns top-level keys as TEXT rows,
preserving duplicate occurrences and decoding escaped names. Empty objects and
SQL NULL produce no rows; arrays and JSON scalars use PostgreSQL diagnostics.
Processing validates every string, including ignored nested keys and values,
so zero Unicode escapes and invalid surrogates accepted by JSON storage are
rejected when processed. Numeric spelling/range and retained JSON text are
unchanged; processing does not convert to JSONB. Targets and bounded function
FROM share expansion, LATERAL, ROWS FROM and WITH ORDINALITY machinery, including
held prepared statements, metadata for empty results and stored-view/native replay.
General lateral derived queries remain excluded.
See [JSON processing](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING).
Feature smoke: `make check RUN='^TestReferenceJSONKeysFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONKeysFeatureSmoke$'`.

Change note: `json_each(json)` and `json_each_text(json)` expand top-level
object members into paired key/value records. SELECT targets expose one RECORD
column; FROM exposes key TEXT and value JSON or TEXT. JSON output retains exact
value spelling (including nested whitespace, numeric exponents and escapes);
text output decodes strings and maps JSON null to SQL NULL. Both preserve
duplicate member occurrences, decode keys and validate all strings, including
nested strings returned as raw source. SQL NULL and empty objects produce no
rows with complete metadata; arrays/scalars use PostgreSQL English 22023
diagnostics without runtime cursor positions. Function aliases, record fields,
target zip/padding, multi-column ROWS FROM padding, ordinality, lateral sources,
held statements and stored/native replay use the ordinary query pipeline.
Only named argument `from_json` is admitted. General table functions remain
excluded.
Feature smoke: `make check RUN='^TestReferenceJSONEachFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `json_populate_record`, `json_populate_recordset`, `json_to_record`
and `json_to_recordset` convert JSON objects into named composites or explicitly
typed FROM records. Missing members retain a non-NULL base's values, otherwise
become typed SQL NULL; these are base-value defaults, not table DEFAULTs.
Explicit JSON null overrides a base. Decoded keys match exactly, the final
duplicate wins, and nested objects/rectangular SQL arrays use destination codecs,
typmods and supported domain CHECKs. Physical table constraints are not inherited
by its row type. Scalar calls retain one composite cell/expanded FROM row;
recordsets yield zero rows for SQL NULL or empty arrays. Typed AS lists retain
every member type and work per member inside ROWS FROM, including ordinality.
Held statements, metadata before iteration, stored expressions and native owned
state use the ordinary query and replay paths. The four functions are STABLE,
not immutable generated-column/index expressions. JSONB counterparts and general
table functions remain outside this selection.
Feature smoke: `make check RUN='^TestReferenceJSONRecordFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `jsonb_each(jsonb)` and `jsonb_each_text(jsonb)` expand immediate
object members through the same paired RECORD/key-value query machinery.
Unlike JSON expansion, JSONB uses canonical decoded-key byte-length/byte order
and retains only the last duplicate key. Values preserve canonical JSONB and
numeric scale; the text variant decodes strings and maps JSON null to SQL NULL.
Nested containers remain single values. Empty objects and SQL NULL yield no
rows with complete metadata. Every non-object root raises 22023
`cannot call jsonb_each on a non-object` (or `jsonb_each_text`), without a
runtime position. Both support `from_json`, aliases, ordinality, target zip,
lateral correlation, held statements, atomic INSERT SELECT and native view replay.
Typed JSON/TEXT still require explicit JSONB conversion.
Feature smoke: `make check RUN='^TestReferenceJSONBEachFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `json_array_elements(json)` and `json_array_elements_text(json)`
expand immediate array members into scalar JSON or TEXT rows in source order.
Nested values retain their exact internal spelling, whitespace, duplicates and
numeric exponents. Raw JSON strings retain quotes/escapes; the text variant
decodes immediate strings and emits SQL NULL for a JSON null member. Empty
arrays and SQL NULL inputs yield no rows with full metadata. Raw expansion
does not decode escaped Unicode; text expansion validates every string,
including nested strings, before returning any member.
SELECT labels use the function name; FROM columns use the scalar OUT name
`value`, retained under a table-only alias and overridden by a column alias.
Both signatures admit `from_json` named notation and ordinary direct/prepared,
target/FROM/lateral/ordinality and symbolic stored/native-control routes.
Wrong object/scalar roots report exact English 22023 diagnostics without a
runtime position. Record-population remains excluded.
Feature smoke: `make check RUN='^TestReferenceJSONElementsFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `jsonb_array_elements(jsonb)` and
`jsonb_array_elements_text(jsonb)` expand top-level arrays into scalar JSONB/TEXT
sets, preserving order, duplicate occurrences, canonical nested values and numeric
scale. TEXT decodes strings and maps JSON null to SQL NULL; JSONB keeps JSON null.
SQL NULL and empty arrays yield zero rows with full metadata. Non-array roots
raise exact English 22023 object/scalar errors without a position. FROM's named
OUT column is `value`, including under a table alias; explicit column aliases
win, while target labels remain function names. Existing SRF zip/padding,
LATERAL, ordinality, prepared recovery, atomic writes and symbolic replay apply.
Feature smoke: `make check RUN='^TestReferenceJSONBElementsFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to that selector.


Change note: JSONB storage normalizes whitespace and key order, keeps the last
decoded duplicate key, and preserves exact finite decimals and their display
scale. It rejects numeric overflow, zero Unicode escapes and invalid surrogate
pairs. SQL NULL differs from JSON null. Ordinary comparison, grouping, B-tree
keys, JSON↔JSONB IO casts, metadata and logical native Dump/Restore share the
existing typed providers; JSON remains source-preserving and noncomparable.
See [JSONB types](https://www.postgresql.org/docs/18/datatype-json.html).
Feature smoke: `make check RUN='^TestReferenceJSONBFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONBFeatureSmoke$'`.

Change note: JSONB `?`, `?|`, `?&` return BOOL for exact decoded top-level
object keys, immediate array strings and primitive strings, never nested values.
Whole SQL NULL is strict; NULL request elements are skipped, with empty ANY false
and ALL true regardless of request bounds/shape. Unknown `?`/`?&` infer their
fixed signatures; `?|` needs a typed side or reports 42725. Stored consumers,
native replay and bounded structural cancellation use the immutable typed carrier.
Feature smoke: `make check RUN='^TestReferenceJSONBExistsFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceJSONBExistsFeatureSmoke$' REFERENCE=1`.

Change note: `json_typeof(json)` / `jsonb_typeof(jsonb)` return top-level kind
TEXT, and `json_array_length(json)` / `jsonb_array_length(jsonb)` return immediate
array counts as INT4. SQL NULL yields SQL NULL; JSON null yields nonnull kind
`null` and is rejected as a scalar by length/key functions.
`json_object_keys(json)` / `jsonb_object_keys(jsonb)` return SETOF TEXT:
JSON retains decoded duplicate names in source order, while JSONB emits
deduplicated canonical keys ordered by UTF8 byte length then bytes. SQL NULL and
empty objects produce zero rows. WITH ORDINALITY exposes INT8 positions;
target-list sets zip and NULL-pad through the existing query stages.
Wrong-root arguments use exact PostgreSQL shape diagnostics without a runtime
Position. Scalar stored consumers and key query views retain symbolic calls
through native Dump/Restore; existing namespace and actual-column authorization
rules still apply. No adjacent JSON operators, path/query execution or generic
callable catalog is added.
See [JSON processing](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING).
Feature smoke: `make check RUN='^TestReferenceJSONTypeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `json_strip_nulls(target json, strip_in_arrays boolean DEFAULT false)`
recursively removes null-valued object fields. The default retains array nulls;
true removes null array elements recursively, but never removes bare JSON null
or empty containers. SQL NULL in either argument returns SQL NULL. Processing
emits compact JSON, decodes/re-escapes strings and keys, and preserves surviving
duplicate members, member order and raw numeric spelling. Original JSON storage
remains unchanged. Named/reordered calls and direct/held prepared parameters
retain JSON result metadata and ordinary column authorization. An explicit
final scalar VARIADIC marker does not expand arrays; with named arguments its
last written argument must map to the last supplied formal position.
PostgreSQL 18.0 classifies the function STRICT STABLE, not immutable:
DEFAULT/CHECK/view consumers work, while generated columns, expression indexes
and index predicates reject it. Native Dump/Reset/Restore retains symbolic
calls, written defaults/labels and detached results. This adds neither
`jsonb_strip_nulls` nor generic JSON equality or SQL/JSON execution.
Feature smoke: `make check RUN='^TestReferenceJSONStripNullsFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: numrange columns extend the common range representation with exact
continuous NUMERIC endpoints. `(4,5)` remains nonempty; `[1.0,1.00]` preserves
both scales, and `[1.0,1.00)` becomes empty. Explicit numeric Infinity/NaN
remain distinct from omitted unbounded endpoints. Direct/prepared text/byte
binds, NUMRANGE metadata, full atomic diagnostics and native/domain Dump/Restore
reuse existing ownership and staging. Scalar operators/comparisons are documented
above; general constructors, casts and arrays remain excluded.
See [continuous numeric ranges](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceNumericRangeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: tsrange columns extend the common range datum with continuous
TIMESTAMP WITHOUT TIME ZONE bounds. `(2000-01-01,2000-01-02]` preserves both
endpoints and flags; an open adjacent-microsecond interval stays nonempty.
Finite bounds are quoted, explicit infinity/inclusivity remains distinct from
omitted bounds, empty and NULL, and timestamp input owns rounding and errors.
Valid written zones are ignored without rotating civil fields, while invalid
offsets still fail. Session DateStyle/relative input, frozen prepared literals
and fresh binds reuse timestamp capture; Dump always emits canonical ISO.
Direct/prepared text/byte binds, TSRANGE metadata, nested-domain contextual
replay, named-composite fields and binary COPY use existing ownership/staging.
Actual Dump/Reset/Restore preserves held readers and rejects staged failures
atomically. Temporal multiranges, range arrays, general constructors/casts,
ordering/index providers and new temporal range aggregates remain excluded.
Scalar tsrange operators and comparisons, including plain/nested domains and
contextual unknown/NULL operands, use the six-family range providers above.
See [continuous timestamp ranges](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceTimestampRangeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: tstzrange columns extend the same continuous range datum with
TIMESTAMP WITH TIME ZONE endpoints, ordered by their UTC instant. Written zones
and session-local inputs reuse the scalar timezone provider; live output uses
the captured TimeZone/DateStyle. Prepared literals freeze complete instants,
while binds decode afresh. Inclusive/exclusive flags, adjacent-microsecond
intervals, explicit infinity, omitted endpoints, empty and NULL remain distinct.
Native TSTZRANGE metadata and string transport, plain/nested domains, composite
fields, binary COPY and actual Dump/Reset/Restore reuse existing ownership.
Dump always emits ISO UTC endpoints, independently of source/target sessions.
Scalar operators/comparisons use the range providers above. General constructors,
casts, arrays, temporal multiranges and new temporal aggregates remain excluded.
Feature smoke:
`make check RUN='^TestReferenceTimestamptzRangeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: daterange columns extend the common range datum with civil DATE
days. Finite bounds canonicalize to [), advancing exclusive lower/inclusive
upper bounds by one day; maximum-day successor fails with22008 rather than
becoming infinity. Explicit DATE infinities retain inclusivity and remain
distinct from omitted endpoints, empty and SQL NULL. Existing DATE DateStyle,
written civil clock/zone rules and transaction-relative input govern endpoints;
prepared contextual literals retain analyzed days while binds decode afresh.
Live reads use session presentation, but native Dump emits canonical ISO.
Direct/prepared text/byte binds, native string/DATERANGE metadata, nested domains,
named-composite fields, binary COPY and actual Dump/Reset/Restore reuse existing
ownership/staging. Scalar operators/comparisons use the six-family providers above;
datemultirange, arrays, general constructors/casts, ordering/index providers and
new temporal range aggregates remain excluded.
See [date ranges](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceDateRangeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector. The standalone
public-API executable smoke is supplied as temporary session material; its root
argv and expected observations are in the part plan, not a committed wrapper.


Change note: int4multirange/int8multirange/nummultirange columns now store typed
canonical unions through existing range codecs. Members sort, empty ranges vanish,
and overlaps/adjacency merge: `{[1,3),[2,4),empty,[8,9)}` becomes
`{[1,4),[8,9)}`. Nonnull `{}`, universal `{(,)}` and SQL NULL remain distinct.
Exact NUMERIC boundaries retain continuous inclusivity and selected display scale.
Direct/prepared text/byte binds, metadata, nested domains and atomic native
Dump/Reset/Restore reuse existing ownership. Scalar operators and comparisons are
documented above; general casts, arrays and ordering/index providers remain excluded.
See [multirange columns](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceMultirangeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `range_agg` unions and `range_intersect_agg` intersects non-NULL
range or multirange inputs in every existing int4/int8/numeric family.
Range union returns its multirange companion; range intersection returns a range;
multirange input returns the same base multirange for either operation.
Disconnected union/intersection components are preserved, not replaced by a hull.
Empty/all-NULL input gives typed NULL; nonnull empty input gives an empty set.
Direct/prepared groups, FILTER, admitted integer-key local ORDER, INSERT SELECT
and stored-view native controls share the typed eager aggregate pipeline.
Scalar range operators are separate from this aggregate pipeline. Aggregate
DISTINCT/window support, temporal aggregate families, general constructors and
casts remain excluded.
See [range aggregates](https://www.postgresql.org/docs/18/functions-aggregate.html).
Feature smoke: `make check RUN='^TestReferenceRangeAggregateFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: `bit_and`, `bit_or`, and `bit_xor` now fold non-NULL SMALLINT,
INTEGER, BIGINT and BIT inputs through the shared eager aggregate pipeline.
Integer results retain their signed input width; implicit VARBIT and domain
inputs resolve to the corresponding base overload. Aggregate BIT metadata has
unspecified width while the actual contents preserve leading zeroes and any
admitted length, including non-NULL `B''`. Singleton input is returned unchanged;
empty/all-NULL input produces typed NULL rather than an identity value.
For inputs12,10,3 the three results are0,15,5; for10101,11000,00111 they are
00000,11111,01010. Unequal bit lengths fail with22026 and operation-specific
English diagnostics, even after AND reaches zero or OR reaches all ones.
Direct/prepared/held queries, groups/HAVING, integer DISTINCT/ALL/FILTER/local
ordering, INSERT SELECT atomicity and stored-view native replacement share the
existing typed paths. BIT ordering by an independent integer key is admitted;
BIT DISTINCT retains42883, BIT/VARBIT local ordering and bitwise windows retain
explicit capability boundaries. General bit casts, grouping/index providers
and arrays remain excluded; scalar operators/functions have separate contracts.
INSERT VALUES still forbids these aggregates with42803 after child, FILTER and
signature analysis; invalid statements publish no rows, including a valid first
row followed by a forbidden aggregate.
See [bitwise aggregates](https://www.postgresql.org/docs/18/functions-aggregate.html).
Feature smoke: `make check RUN='^TestReferenceBitwiseAggregateFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: int4range/int8range columns now use typed integer endpoints and
bound flags with discrete canonical text: `[1,4]` becomes `[1,5)`.
Empty, unbounded and SQL NULL remain distinct; malformed bounds, endpoint/
successor overflow and reversal retain PostgreSQL English diagnostics.
Direct/prepared text and byte binds, exact INT4RANGE/INT8RANGE metadata and
native Dump/Reset/Restore use existing codec/catalog ownership, preserving
retained readers and atomic failed replacement. Already-admitted range-backed
domains replay canonical row text and NULL through their declared destinations.
Scalar operators/comparisons are documented above. General constructors/casts,
range arrays and ordering/index providers remain excluded.
See [integer range columns](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceIntegerRangeFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceIntegerRangeFeatureSmoke$' REFERENCE=1`.

Change note: JSONPath (OID4072, array4073) now has typed literal/bind IO,
full PostgreSQL18 path grammar and input-time ARE/ERE/BRE regex validation.
An immutable parsed representation retains mode, expressions and accessors;
output is canonical. INSERT/UPDATE destinations and explicit CAST infer bound
path types before import. SQL NULL differs from path `null`. Native metadata
is JSONPATH/string; lib/pq reports an empty type name and interface{} ScanType,
with raw bytes. Defaults, views, arrays/domains and native Dump/Restore reuse
existing providers. The array-query, query-family consumers and JSONB path
operators below execute parsed paths; GIN remains excluded.
See [JSONPath typed IO](https://www.postgresql.org/docs/18/datatype-json.html).
Feature smoke: `make check RUN='^TestReferenceJSONPathFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'`.

Change note: `jsonb_path_query_array(target jsonb, path jsonpath,
vars jsonb DEFAULT '{}', silent boolean DEFAULT false)` returns one immutable,
strict JSONB array. Navigation preserves ordered matches and duplicates, with
root/current/last scope, variables, strict/lax accessors, recursive descent,
three-valued conditions, filters, exists and PostgreSQL regex matching.
No match is `[]`; selected JSON null or unknown is `[null]`; a SQL NULL operand
gives SQL NULL. Silent strict structural failure stops traversal and retains
the preceding result prefix; missing variables, invalid vars shape and ordinary
SQL errors are not suppressed. Named/default calls retain their written form
through stored expressions and native replay, and use the real routine's ACL
and dependency authority. GROUP matching distinguishes named/positional and
omitted/explicit-default forms. The five shape/scalar methods described below
share this evaluator. Demanded arithmetic and remaining methods raise native
`0A000`, even with silent enabled; unreachable branches remain undemanded.
`_tz`, SQL/JSON query syntax and GIN remain excluded.
See [navigation contracts](reference/binary_json_arrays/README.md#jsonpath-navigation-and-array-queries).
Feature smoke: `make check RUN='^TestReferenceJSONPathNavigateFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceJSONPathNavigateFeatureSmoke$' REFERENCE=1`.

Change note: `jsonb_path_exists`, `jsonb_path_match`, `jsonb_path_query_first`
and set-returning `jsonb_path_query` share that same navigation evaluator and
four formals/defaults, genuine strict immutable routines and ordinary ACLs.
Existence counts any item (including JSON false/null); strict existence demands
the whole path while lax existence may stop at its first hit. Match requires a
single boolean or JSON null (`22038` otherwise); silent structural failure still
uses a retained singleton boolean prefix. First selects the first JSONB item but
traverses the complete path; no match is SQL NULL, unlike selected JSON null.
Query returns ordered JSONB rows, zero on no match or any SQL NULL formal, and
materializes each invocation before publishing rows. Silent structural failure
can retain its prefix; ordinary errors and cancellation never publish that
invocation's partial collection. Target-list and FROM calls reuse the existing
set lane, including lateral reopening, zip/padding and INT8 ordinality. This is
not whole-query read atomicity across separate invocations. Stored scalar calls
and symbolic query views use existing persistence; Restore INSERT query-source
restrictions and advanced arithmetic/method/timezone boundaries remain unchanged.
Feature smoke: `make check RUN='^TestReferenceJSONPathQueryFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceJSONPathQueryFeatureSmoke$' REFERENCE=1`.
See [query-family contracts](reference/binary_json_arrays/README.md#jsonpath-query-consumer-contracts).

Change note: JSONB `@?` and `@@` reuse the same evaluator with silent evaluation
and no variables. `@?` tests item presence; `@@` returns a singleton Boolean or
SQL NULL, including its retained Boolean prefix before a suppressed path error.
Invalid path input and missing variables still raise their actual diagnostics.
Documented structural and supported subscript errors are suppressed; demanded
arithmetic and remaining scalar/datetime methods retain the native `0A000`
boundary. Held statements, actual operator-routine ACLs and symbolic native
replay use the ordinary expression paths. See [operator contracts and smoke](reference/binary_json_arrays/README.md#jsonb-jsonpath-operators).

Change note: JSONPath `type()`, `size()`, `boolean()`, `string()` and `keyvalue()`
now execute through every existing path consumer, including `@?`/`@@`.
Type preserves arrays; size counts arrays and returns1 for lax nonarrays
(strict nonarrays raise22039). Boolean/string/keyvalue unwrap one array level
only in lax mode. Their invalid conversions still raise in lax; silent can
terminate traversal with a retained prefix, while predicates yield unknown.
Boolean accepts unpadded unique Boolean-string prefixes or signed-int32 lexical
numeric output: numeric1.0 is invalid, numeric1e2 is true. String conversion
preserves exact numeric display scale and spells booleans true/false.
Keyvalue emits canonical id/key/value objects; IDs identify source occurrences
within one invocation/base, not portable absolute counters. Equal distinct
objects differ, revisits agree, and generated wrappers establish fresh bases.
Prepared reuse, stored recipes and native replay keep no persistent ID state.
See [method contracts](reference/binary_json_arrays/README.md#jsonpath-shape-and-scalar-methods).
Feature smoke: `make check RUN='^TestReferenceJSONPathMethodsFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceJSONPathMethodsFeatureSmoke$' REFERENCE=1`.




Change note: PostgreSQL 18 datetime templates support `to_char(timestamp,text)`,
`to_char(timestamptz,text)`, `to_char(interval,text)`, `to_date(text,text)` and
`to_timestamp(text,text)`. Templates use PostgreSQL tokens, not Go layouts or
ordinary casts; prepared executions capture the current session zone. The five
strict STABLE functions compose in queries, writes, DEFAULT/CHECK and views,
including native Dump/Restore. Epoch `to_timestamp` remains unsupported;
numeric overloads are covered by [numeric formatting](https://www.postgresql.org/docs/18/functions-formatting.html).
See [datetime formatting](https://www.postgresql.org/docs/18/functions-formatting.html).
Feature smoke: `make check RUN='^TestReferenceDatetimeFormattingFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceDatetimeFormattingFeatureSmoke$'`.

Change note: ordinary `DROP INDEX`, `ALTER INDEX ... RENAME TO`,
`REINDEX INDEX` and `REINDEX TABLE` operate on substantive logical B-tree/hash roots.
Grouped drops preflight atomically; backing-index rename retains constraint/index
identities and ongoing UNIQUE enforcement. Rebuilds replace selected real roots,
not descriptors or table rows. Native execution is excluded in writable explicit
transactions, and read-only rejection precedes lookup/locks. Physical options,
tablespaces, statistics and concurrent/global rebuild requests remain explicit
capability boundaries, not no-op successes. Internal coherent metadata uses
central actual method-specific class identities and key/INCLUDE vectors, including admitted
explicit providers and enum domains; no new SQL catalog source is added.
See [maintenance contracts](https://www.postgresql.org/docs/18/sql-reindex.html) and
[the design](docs/design/PG18-INDEX-MAINTENANCE.md).
Feature smokes:
`make check RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders)'`;
`make check REFERENCE=1 RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders)'`.

Change note: standalone `ALTER TABLE [IF EXISTS] [ONLY] target [*] RENAME
CONSTRAINT old TO new` supports represented NOT NULL, CHECK, UNIQUE, PRIMARY KEY
and EXCLUDE definitions. Owner-only metadata rename needs no namespace CREATE,
preserves table/constraint/backing-index identity and storage, and reports the
current constraint name on violations. Definition invalidation reanalyzes prepared
handles without changing unchanged result labels/types. Writable transactions,
savepoints, rollback and failing-later-statement atomicity use the existing
catalog overlay. Canonical native Dump/Restore retains current definitions,
rows and representable table/column comments, not rename history.
Graph CHECK/NOT NULL ALTER remains an explicit boundary; admitted local
UNIQUE/PRIMARY KEY/EXCLUDE graph definitions retain their existing allowance.
ALTER DOMAIN default/nullability changes, FK/constraint triggers and PostgreSQL
physical storage, WAL and external pg_dump mechanisms are not implemented; network access uses the
bounded [Phase 2 server](#phase-2-server--change-note).
Selected proof: `make check RUN='^TestReferenceConstraintRename'`;
repeat with `REFERENCE=1` for the pinned oracle.
See [the rename design](docs/design/PG18-DDL-ALTER-CONSTRAINT-RENAME.md).


Change note: floating `+`, unary `+`, `-`, unary `-`, `*`, `/` and `^`
use PostgreSQL operand resolution and fixed REAL/DOUBLE PRECISION widths.
Known integer/NUMERIC operands mixed with REAL widen to DOUBLE; unknown
inputs retain REAL. Power is left-associative (`2^3^2=64`) and FLOAT8-selected
integer power is supported. IEEE specials, checked range/domain diagnostics,
stored expressions and native replacement use the existing execution seams.
Floating `%` remains unavailable; exact NUMERIC operators are documented below.
See [floating arithmetic](https://www.postgresql.org/docs/18/functions-math.html).
Feature smokes: `make check RUN='^TestReferenceFloatingArithmetic(Smoke|ControlsSmoke)$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceFloatingArithmetic(Smoke|ControlsSmoke)$'`.

Change note: NUMERIC `+`, unary `+`, `-`, unary `-`, `*`, `/`, `%` and `^`
use immutable exact decimal values, retained display scales and PostgreSQL
rounding. Integer and fractional power share guarded decimal computation,
not binary FLOAT8 results; power remains left-associative. Direct/prepared
inputs, computed NUMERIC metadata, stored grouping and native Dump/Restore
reuse existing binding, execution and atomic publication.
See [numeric arithmetic](https://www.postgresql.org/docs/18/functions-math.html) and
[the design](docs/design/PG18-FUNC-MATH-ARITHMETIC-NUMERIC.md).
Feature smoke: `make check RUN='^TestReferenceNumericArithmeticFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceNumericArithmeticFeatureSmoke$'`.

Change note: index/view introspection supports all seven PostgreSQL 18 signatures:
`pg_get_indexdef(oid)` and `(oid, integer, boolean)`;
`pg_get_viewdef(text)`, `(oid)`, `(text, boolean)`, `(oid, boolean)` and
`(oid, integer)`. Results are strict nullable TEXT; omitted pretty means false,
and integer view width selects pretty rendering. Reconstruction traverses current
bound descriptors without evaluating views. Index column zero gives CREATE INDEX,
other valid columns give only the key expression or included column name, and
invalid columns give empty text. Text view arguments resolve relation names, not
numeric OIDs. Ordinary views and existing simple, expression-key and UNIQUE
backing indexes are covered, including ordinary indexes with INCLUDE payload.
Materialized-view metadata ALTER, logical indexes and concurrent refresh are
supported. Partial index predicates use the same current-name index introspection
and control paths.
Feature smoke:
`make check RUN='^TestReferenceDeparseRelations'`
and `make check REFERENCE=1 RUN='^TestReferenceDeparseRelations'`.

Change note: `pg_get_serial_sequence(text,text) -> text` finds actual AUTO-owned
serial/manual sequences and INTERNAL-owned supported identity sequences, not
nextval defaults or inferred names. It returns safely quoted, always-qualified
TEXT or NULL without drawing a number. First-argument qualified-name parsing
folds unquoted identifiers; the second is an exact literal column name. Strict
NULL, binding/runtime diagnostics, held execution-time catalog lookup and known
nullable TEXT metadata reuse existing typed providers. OWNED BY NONE removes
the association; native Dump/Restore and symbolic views/defaults/CHECK retain
existing ownership and generator state. No new DDL or pg_get_owned_sequence
alias is added. See [owned sequence introspection](https://www.postgresql.org/docs/18/functions-info.html).
Feature smoke: `make check RUN='^TestReferenceSerialIdentityFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSerialIdentityFeatureSmoke$'`.


Change note: arrays now provide catalog-owned storage and literal input/output
over the supported array scalar families and domains (MONEY remains scalar-only), including arrays of
domains, domains over arrays and nested array-valued domains. `type[]`, repeated
brackets and standard `type ARRAY[n]` declarations resolve the element before its
associated array; declared bounds do not constrain stored shape. Native values
are canonical array-literal strings, with owned immutable dimensions/elements,
typed NULLs, element typmods and statement-owned temporal/REGCLASS presentation.
Domain publication/drop and native Dump/Restore retain both association halves
and recursively reown stored identities. Typed constructors and access extend
this immutable storage through the existing expression consumers, as described
in the array expression change note below.
See [arrays](https://www.postgresql.org/docs/18/arrays.html).
Feature smoke: `make check RUN='^TestReferenceArraySmoke$'`;
independent oracle: `make check REFERENCE=1 RUN='^TestReferenceArraySmoke$'`.

Change note: PostgreSQL 18 typed `ARRAY[...]` constructors support nested
rectangles, explicit typed empties, common element types and existing array
children. Element fetches honor stored lower bounds and return typed NULL for
NULL/out-of-range/wrong-rank input. Slices use global colon semantics, clip
overlap, return empty outside, and reset fetched lower bounds to one.
Partial INSERT/UPDATE targets compose in SQL order; UPDATE RHS values and bounds
see the old row. INSERT partial targets start from typed NULL, not DEFAULT.
One-dimensional assignments can enlarge with NULL gaps; existing multidimensional
arrays cannot enlarge. Slice RHS cells are consumed row-major regardless of RHS
shape, with insufficient input rejected and excess ignored. Prepared/stored
consumers and logical native controls share the same typed nodes.
Slice validation preserves dimension/error order before source sufficiency on
nonempty originals. Empty/NULL originals construct explicit spans: zero yields
canonical empty, negative/overflow fails with SQLSTATE `54000`, and final bounds
remain checked.
No ARRAY(SELECT), partial MERGE targets, new array operators/functions or new
expression consumers are added. Feature smoke (900-second deadline each):
`make check RUN='^TestReferenceArrayExpressionFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceArrayExpressionFeatureSmoke$'`.

Full array definitions: run the same commands with `RUN='^TestReferenceArray'`.

Change note: Serializable transactions now use stable snapshots plus nonblocking
read/write dependency tracking. Unsafe write skew fails with SQLSTATE `40001`
without publishing the loser's writes; callers retry the entire transaction.
SQL `SERIALIZABLE READ ONLY DEFERRABLE` waits for a safe snapshot at first data
analysis (including actual Prepare), with statement timeout and ordinary
cancellation/epoch fencing. Predicates are conservative whole-relation footprints;
continuous overlap retains dependency evidence until the cohort drains.
`sql.LevelSerializable`, current/default deferrable settings and savepoint/CHAIN
characteristic restoration use the existing transaction owner.
See [transactions](docs/TRANSACTIONS.md#serializable-snapshot-isolation).
Feature smokes:
`make check RUN='^TestReferenceSerializable'`
and `make check REFERENCE=1 RUN='^TestReferenceSerializable'`.

Change note: `CREATE DOMAIN [schema.]name [AS] base_type` declares a
catalog-owned logical type over a supported scalar or array base, with optional
DEFAULT, NULL/NOT NULL and named or unnamed CHECK clauses.
Domain columns, direct/prepared input, inherited base typmods, operators/functions
and explicit assignment/cast-back retain the distinction between logical identity
and physical codecs. `DROP DOMAIN [IF EXISTS] ... [RESTRICT | CASCADE]` follows
real type/column/stored-expression dependencies: CASCADE removes dependent columns
and views, not unrelated tables or surviving rows. Native Dump/Restore preserves
qualified declarations, chains, owners, default recipes, nullability,
named/generated CHECKs and data. Array operators/functions, ALTER DOMAIN
default/nullability changes and transactional domain DDL remain excluded.
See [domains](https://www.postgresql.org/docs/18/domains.html).
Feature smokes:
`make check RUN='^TestReferenceDomain(BaseValues|Expressions|Metadata|Arrays)$'`,
`make check RUN='^TestReferenceDomain(Diagnostics|Dependencies|PreparedLifecycle|NativeControls)$'`,
and pinned `make check REFERENCE=1 RUN='^TestReferenceDomain'`.

Change note: `ALTER DOMAIN ... RENAME TO`, `OWNER TO`, and `SET SCHEMA`
preserve logical domain, companion-array and CHECK identities. Stored columns,
derived domains and nested values keep their coercion rules; subsequent failures
name the current domain. Actual driver-prepared utilities are quiet at Prepare
and check current authority at Execute. Held typed consumers, including checked
and unchecked companion-array inputs written as `d[]` or `_d`, retain identity
through rename/move without losing bounds, NULLs or element CHECK enforcement.
Search-path reanalysis still resolves the original SQL.
DROP admits the containing-schema owner as well as the domain owner, and
qualified missing schemas obey IF EXISTS notice semantics. RESTRICT is atomic;
CASCADE removes the real dependent columns/definitions, not their containing
tables or unrelated data. Native Dump/Restore emits final qualified names.
Explicit-transaction, ALTER default/nullability, arbitrary-ACL dump and external
pg_dump boundaries remain unchanged. See the
[identity contract and selected proof](reference/types/README.md#domain-identity-lifecycle-contract).


Change note: `ALTER DOMAIN` supports CHECK additions (including NOT VALID),
selected DROP CONSTRAINT and VALIDATE CONSTRAINT. NOT VALID preserves old
violators but checks every new conversion. Validation scans direct and derived
domain columns in physical tables/materializations, evaluates only the selected
CHECK, and publishes the flag atomically. Stored containers reject validating
ADD/VALIDATE even when empty or NULL, matching PostgreSQL18.0; repeated VALIDATE
rescans an already-valid CHECK. ALTER accepts but ignores NO INHERIT, unlike
CREATE's rejection. Held statements retain logical IDs while observing current
rules; old eager rows remain immutable. Own domain CHECKs appear in
`pg_constraint` and public deparse. Native Dump emits invalid CHECKs after data,
preserving false flags, owners and new-input enforcement through Restore.
Domain utilities remain autocommit-only. Native structural dependency/storage
fences may reject concurrent graph changes rather than reproduce every server
lock schedule; no successful validation certifies a stale captured graph.
Feature smoke: `make check RUN='^TestReferenceDomainAlterConstraintLifecycle$'`
and the same command with `REFERENCE=1`. Full selected groups are under
[type testing](reference/types/README.md#domain-alter-check-selected-proof).

Change note: domain CHECK expressions bind `VALUE` to the immediate base type.
TRUE and UNKNOWN pass; FALSE raises 23514 with the conversion target's schema/type
and the failing constraint name, without a table failing-row detail. Names are
explicit or generated namespace-wide; checks run alphabetically within each
domain, with all ancestors before descendants. Existing shared expression/input
providers enforce checks during casts, base assignments, parameters, COPY and
nested member input. Operators downcast to the base; cast-back rechecks.
Expression conversions retain per-row CHECK truth instead of being folded into
once-only input: false/empty consumers do not consume truth, while CHECK recipe
initialization can still expose constant errors before rows are demanded.
Planning-only JOIN/MERGE facts and conflict inference do not consume candidate
truth. Retained CASE/COALESCE conversions still initialize their recipes.
Existing ADD COLUMN materializes checked defaults and omitted fresh NULL per
actual old row, without a once-only checked-value cache. Existing MERGE with
BY SOURCE also consumes ON at its target action-classification stage.
Stored generated, partition and index definitions capture their real catalog and
input authority for recipe initialization and row execution. Index insertion
produces immutable membership/keys; COMMIT transports those entries without
replaying CHECK SQL. ON CONFLICT retains its distinct precheck/insertion events
and genuine existing-tuple key recheck. Slot-only projection and trusted snapshot
replacement transport produced data, while genuine value rewrites rebuild under
their admitted SQL owner. No synthetic COMMIT evaluator or CHECK-truth cache is used.
Fresh NULL input is checked, but already-domain NULL from an unmatched outer
join or an empty scalar subquery can bypass conversion. This is not domain
NOT NULL. CHECK-owned provider CASCADE retires only the dependent check when
there is no separate base dependency, preserving the domain and stored data.
Native Dump/Restore retains recorded names, recipes and post-restore enforcement;
external pg_dump/pg_restore and procedural runtimes remain separate boundaries.
Feature smokes:
`make check RUN='^TestReferenceDomainCheck(Truth|ConversionsNull|Ancestors)$'`,
`make check RUN='^TestReferenceDomainCheckNativeControls$'`,
`make check RUN='^TestReferenceDomainCheck(Demand|DemandWrappers)$'`,
`make check RUN='^TestReferenceDomainCheck(PlanningDemand|RecipeInitialization|ConflictProofDemand|AddColumnDemand|StoredDemand)$'`,
`make check RUN='^TestReferenceDomainCheckIndex(RuntimeDemand|LifecycleDemand|FailureDemand|ConflictDemand)$'`.
Run each selected counterpart with `REFERENCE=1` for the pinned oracle; see
[type testing](reference/types/README.md#domain-check-selected-proof) for all groups.

Change note: the predefined `information_schema.time_stamp` domain accepts
supplied timestamptz values and explicit/bound NULL through the authoritative
catalog/domain path. It retains distinct qualified identity, rounds to precision2,
exports TIMESTAMPTZ metadata and survives native Dump/Reset/Restore.
Its inherited `DEFAULT CURRENT_TIMESTAMP(2)` executes through the transaction-time
provider and can be masked by an explicit column constant or NULL. No sibling-domain,
implicit `_time_stamp` array storage or general information-schema view support
is implied. See [domain contracts](https://www.postgresql.org/docs/18/domains.html).
Feature smoke: `make check RUN='^TestReferenceInfoTimeStampFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceInfoTimeStampFeatureSmoke$'`.

Change note: user-declared `information_schema.sql_identifier` columns now use
the predefined domain over `pg_catalog.name`. Qualified domain identity and
inherited C collation remain distinct from NAME result metadata. Values retain
case/spelling; text silently clips at a complete UTF8 boundary within 63 bytes.
NULL and empty text remain distinct. Full input validation precedes clipping:
NUL or malformed UTF8 raises 22021, including invalid suffixes beyond byte 63,
and a failed multirow INSERT publishes no rows. Native Dump/Reset/Restore retains
the qualified builtin domain and values without recreating the domain.
This does not enable implicit domain-array storage, other sibling domains,
binary NAME transport or migration of existing information-schema view carriers.
See [type contracts](docs/TYPES.md#arrays-composites-enums-and-domains).
Feature smoke: `make check RUN='^TestReferenceInfoSQLIdentifierFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceInfoSQLIdentifierFeatureSmoke$' REFERENCE=1`.

Change note: consumer columns can declare the predefined
`information_schema.yes_or_no` domain. Its existing qualified identity wraps
VARCHAR(3) with C collation and the genuine named `yes_or_no_check`, accepting
YES, NO and NULL through ordinary direct/prepared input. Physical result metadata
is VARCHAR/string/length3 while pg_catalog retains domain/base/constraint links.
Assignment overlength fails22001 before CHECK; invalid in-range values fail23514
with PostgreSQL English/schema/type/constraint diagnostics and no partial INSERT.
Native Dump/Reset/Restore retains the declaration, values, metadata and validation.
This does not enable sibling domains, its implicit array, general information-schema
views or migrate the existing table_privileges provider's primitive yes/no fields.
Feature smoke: `make check RUN='^TestReferenceInfoYesOrNoFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceInfoYesOrNoFeatureSmoke$' REFERENCE=1`.

Change note: consumer columns can declare the predefined
`information_schema.cardinal_number`, a nullable nonnegative INT4 domain with
the genuine `cardinal_number_domain_check` (`VALUE >= 0`). Direct/prepared
input accepts 0 through 2147483647 and NULL. Logical catalog identity remains
the qualified domain; physical results expose INT4/Go int64 without length or
decimal-size metadata and with no collation. Negative input fails23514;
malformed integer text and overflow retain22P02/22003. Failed multirow INSERT
publishes no prefix. Qualified native Dump/Reset/Restore preserves values,
metadata and CHECK enforcement, without emitting CREATE DOMAIN for the builtin.
The companion remains lookup-only and character_data remains unsupported;
general information-schema views and existing provider carriers are unchanged.
Feature smoke: `make check RUN='^TestReferenceInfoCardinalNumberFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceInfoCardinalNumberFeatureSmoke$' REFERENCE=1`.

Change note: `CREATE POLICY`, selective `ALTER POLICY`, policy rename and
`DROP POLICY [IF EXISTS]` now define immutable, table-attached policy metadata.
Definitions support PERMISSIVE/RESTRICTIVE, ALL/SELECT/INSERT/UPDATE/DELETE,
resolved role/PUBLIC targets and Boolean USING/WITH CHECK conditions, including
supported nested queries. Omitted ALTER clauses retain existing fields;
ownership, role dependencies, column/provider dependencies and native
Dump/Reset/Restore follow the existing catalog and atomic-control paths.
Restore resolves policy CURRENT_USER/CURRENT_ROLE/SESSION_USER targets against
the actual named stage session, separately from trusted import authorization;
all three targets after a named owner directive name that owner. Dump retains
resolved target multiplicity, and Restore does not change caller identity.
Ordinary SELECT/INSERT/UPDATE/DELETE evaluate the original bound policy
expressions, including supported correlated queries and runtime providers.
Applicable PUBLIC/direct/inherited role policies use the current effective
identity: permissive policies OR, restrictive policies AND, with at least one
permissive grant and separate required command/SELECT groups. FALSE/NULL hide
OLD rows; rejected NEW rows raise42501 (a failing restrictive policy is named).
Policy evaluation errors propagate unchanged. INSERT RETURNING and target-reading
UPDATE additionally check SELECT visibility of NEW. Source-local guards precede
user work, joins and aggregates; mutations recheck refreshed OLD rows after waits.
Owner/direct-superuser/direct-BYPASSRLS and FORCE semantics remain independent
of ordinary ACLs. Held execution selects fresh policy metadata and authority.
MERGE/ON CONFLICT action-specific expression enforcement retains its explicit
0A000 capability boundary; bypass/default-deny and ordinary source filtering
remain supported. See [authorization](docs/AUTHORIZATION.md#row-security-metadata-and-enforcement-limits).
Release18 accepts `ALTER POLICY IF EXISTS ... RENAME TO ...`, but its rename
handler still errors on a missing table or policy; selective ALTER has no
IF EXISTS form. Native policy utilities remain autocommit-only and reject
read-only execution; PostgreSQL supports transactional policy DDL.
See [policy contracts](docs/AUTHORIZATION.md#row-security-metadata-and-enforcement-limits)
and [dialect limits](https://www.postgresql.org/docs/18/ddl-rowsecurity.html).
Feature smoke: `make check RUN='^TestReferenceRLSPolicyFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRLSPolicyFeatureSmoke$'`.

Change note: table RLS flag ALTERs are transaction-supported, owner-authorized,
named/nonrecursive AccessExclusive metadata changes. Ordinary ACLs remain
independent; genuine `pg_read_all_data`/`pg_write_all_data` do not bypass RLS.
`pg_tables.rowsecurity` reports the enabled bit. Native Dump captures protected
raw data and replays flag ALTERs only after all rows/policies; Restore preserves
all four enabled/forced states atomically. Feature smoke:
`make check RUN='^TestReferenceRLSBaseFeatureSmoke$'`; add `REFERENCE=1`
for the same pinned PostgreSQL definition.



Session identity uses typed scalar expressions: `current_catalog`,
`current_database()`, `current_role`, `current_user`, `session_user`, `user`
return NAME; embedded trust-style `system_user` returns TEXT SQL NULL. Initially
`SELECT current_catalog=current_database(), current_role=current_user, session_user=user`
returns three true values. SET/RESET ROLE changes effective identity without
changing session_user. SESSION role changes persist on commit and restore on
rollback; LOCAL expires at transaction end, and savepoints restore both effective
role and the masked SESSION candidate. Prepared executions observe fresh identity
while retained rows keep their snapshots. The exact opaque DSN is the database key;
its NAME display silently clips to a complete UTF8 prefix of at most 63 bytes,
without URL parsing, trimming or merging distinct keys. See
[authorization](docs/AUTHORIZATION.md) for forms and authority boundaries.

Change note: [`pg_catalog.pg_database`](https://www.postgresql.org/docs/18/catalog-pg-database.html)
now exposes the full PostgreSQL 18 eighteen-column schema through ordinary
direct/prepared queries. Native inventory is exactly the current attachment;
`datname` matches both current identity expressions, while `oid`, `datdba` and
`datacl` expose its existing logical identity, captured owner and stored CREATE
ACL/options. Owner/grant changes are transaction-visible, fresh held executions
see publication, and already returned native Rows retain old values.
UTF8/C encoding/provider/locale fields are semantic declarations, not host facts.
Unavailable template/admission/connection-limit/freeze/multixact/placement fields
are typed NULL, as are C-inapplicable locale/rules/version fields. No multi-database
or physical storage model, CONNECT/TEMP grants, template rows or catalog backing
indexes are invented. Reset/Restore preserve destination identity/owner/ACL.
See the [precise field boundaries](docs/DIALECT.md#pg_database-current-attachment-metadata).
Feature smoke: `make check RUN='^TestReferencePGDatabaseSmoke$'` and the same
command with `REFERENCE=1` for pinned PostgreSQL18.0.

Change note: [`pg_catalog.pg_db_role_setting`](https://www.postgresql.org/docs/18/catalog-pg-db-role-setting.html)
is readable through ordinary direct and prepared queries with the exact
three-column schema: NOT NULL `setdatabase OID`, NOT NULL `setrole OID`, and
nullable `setconfig TEXT[]`. Relation2964 is an indexed, cluster-shared catalog
table in `pg_global`, not a view; it has no row `oid` column. PUBLIC SELECT uses
the normal authorization checks even when no rows are present.

The native catalog is currently empty because no persistent per-role/per-database
settings owner exists. Session SET/SET LOCAL, custom placeholders, registered
defaults and connection defaults never manufacture stored settings rows.
ALTER ROLE/DATABASE SET and RESET population, stored scope arrays and login
application remain settings/command-feature work; this read capability does not
claim those effects. Reset/Restore retain catalog availability and typing, held
statements recapture the empty source, and Dump omits catalog rows/default commands.
Feature smoke: `make check RUN='^TestReferencePGDBRoleSettingSmoke$'`; repeat with
`REFERENCE=1` for pinned PostgreSQL18.0. See the
[selected proof and owner boundary](reference/catalogs/README.md#pg_db_role_setting-selected-proof).

Change note: [`pg_catalog.pg_parameter_acl`](https://www.postgresql.org/docs/18/catalog-pg-parameter-acl.html)
now exposes actual sparse parameter ACL records as `oid OID NOT NULL`,
`parname TEXT NOT NULL`, and nullable `paracl ACLITEM[]`. Relation6243 is an
indexed, cluster-shared table in `pg_global`, with bootstrap ownership and
ordinary PUBLIC SELECT. Reads include every actual granted entry, including
canonical dotted custom names and explicit empty `{}` ACLs; merely changing a
setting does not create a row. GRANT/REVOKE owns stable identity, ACL provenance
and default-equivalent removal, not the catalog reader.

Direct, prepared and held executions recapture completed changes while opened
rows retain their typed ACLs and captured role display names. Normal current
table/column SELECT checks apply even for empty/FALSE/LIMIT0 reads; parameter
SET privileges neither filter rows nor confer catalog access. Native catalog
GRANT, explicit-transaction parameter grants, missing setting providers,
has_parameter_privilege, external ALTER SYSTEM execution and positive ACL
Dump/Restore remain outside this read capability. Dump still rejects parameter
ACLs before output; Reset clears entries but preserves catalog metadata.
Feature smoke: `make check RUN='^TestReferencePGParameterACLFeatureSmoke$'`;
repeat with `REFERENCE=1`. See [selected proof](reference/catalogs/README.md#pg_parameter_acl-selected-proof).

Change note: [`pg_catalog.pg_default_acl`](https://www.postgresql.org/docs/18/catalog-pg-default-acl.html)
reads actual default-privilege policies through five nonnullable columns:
`oid OID`, `defaclrole OID`, `defaclnamespace OID`, `defaclobjtype CHAR`,
and `defaclacl ACLITEM[]`. Relation826 is an indexed, database-local catalog
table. Policies retain real producer-owned identities through edits and recipient
cleanup; returning to baseline deletes the row, and recreation has a new identity.
Namespace0 denotes global defaults; scoped records use the actual namespace OID.
The creator remains independent of schema ownership and role membership.

Ordinary roles see the complete policy inventory, including inaccessible schemas
and unrelated NOLOGIN creators, subject to normal catalog SELECT authorization.
ACL arrays are nonnull (including stored `{}`), preserve exact grantors/options,
and use PostgreSQL's numeric ACL-item ordering. Reads do not infer baseline rows,
effective object grants, or unsupported categories. Direct/prepared/held queries
recapture policy changes; already-open rows retain their owned values. Native
catalog writes and locks remain unsupported. Default-bearing Dump still rejects
before output; failed Restore preserves policies and Reset removes them. This
adds no default-privilege effects or serialization.
See [selected proof and standalone smoke](reference/catalogs/README.md#pg_default_acl-selected-proof).


Change note: table/column GRANT and REVOKE now retain independent grantor
origins, owner implicit options, PUBLIC and inherited role authority. All eight
table privileges and four column privileges are represented; real supported
consumers use the same ACL checks, including MAINTAIN REINDEX, TRUNCATE TABLE
and the existing TRUNCATE alternative for explicit LOCK.
Column-only revoke cannot subtract a table grant. ALL TABLES IN SCHEMA expands
existing tables/views/materialized views, never future tables or sequences.
REVOKE GRANT OPTION FOR removes delegation without removing use; CASCADE removes
unsupported dependent grant bits, while default/explicit RESTRICT is atomic.
Independent grants and inherited options survive; PUBLIC option-only revocation
is legal and owner implicit grant authority remains. Release18.0
GRANTED BY/current-user and per-ACL dependency semantics are documented in
[authorization](docs/AUTHORIZATION.md#table-and-column-acl-authority).
Merged column requests follow stable attribute order, including duplicate and
reversed lists. Enumerating all four column privileges has the same partial-option
warning suppression as column ALL; zero-option requests still warn.
Sequence, schema and type/domain direct grants are supported. These native object
utilities remain excluded in explicit transactions; nonserializable ACL changes
make Dump fail before output.
Feature smoke (900 seconds each):
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.
Selected revoke smoke:
`make check RUN='^TestReferenceRoleRevokeFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceRoleRevokeFeatureSmoke$'`.

Change note: `ALTER DEFAULT PRIVILEGES` stores global and schema-scoped defaults
for TABLES, SEQUENCES, FUNCTIONS/ROUTINES, TYPES, SCHEMAS and LARGE OBJECTS.
Subsequently created supported tables/views/materialized views, sequences,
standalone types, explicit schemas and large objects consume their class defaults.
Creation uses its actual effective owner, not
inherited roles' defaults; schema grants add to global grants, and schema REVOKE
cannot subtract global or hardwired rights. Owner grant authority remains
implicit even when ordinary owner use is revoked. Stored global empty ACLs remain
real policy/dependency state, although an empty creation merge falls back to
hardwired rights; revoking all privileges on an actual object remains truly empty.
`information_schema.table_privileges` exposes seven ordinary table privilege
bits per visible grantor/grantee origin using existing NAME/VARCHAR carriers,
not effective-rights summaries or a new ACLITEM/domain implementation.
Read those carriers uncast; no additional TEXT casts or enum-array storage are
introduced. Ordinary namespace rights for an owning physical session's TEMP
schema survive actor switches, without bypassing table ACLs or peer isolation.
Default utilities retain native explicit-transaction boundaries; nonempty
default policy makes Dump fail before output, Restore rejects the utility, and
Reset clears policy while retaining roles/membership. See
[default privilege contracts](docs/AUTHORIZATION.md#creation-time-default-policy).
Feature smoke: `make check RUN='^TestReferenceRoleDefaultPrivilegesFeatureSmoke$'`
and the same command with `REFERENCE=1` (900 seconds each).

Change note: `information_schema.data_type_privileges` now projects actual
privilege-qualified TABLE column, DOMAIN and standalone composite attribute
descriptors. Stable attribute numbers survive DROP/ADD and renames; typmods and
arrays do not invent extra descriptors. Owner inheritance, per-column/table ACLs
and type USAGE govern visibility without an extra schema/search-path filter.
The five genuine information-schema domains are visible with their seeded
PUBLIC USAGE. Native result carriers remain NAME/NAME/NAME/VARCHAR/NAME rather
than information-schema domain usages; executable user-declared sql_identifier
columns do not change those carriers. Routine parameter/return descriptor arms
remain outside native capability. See
[descriptor details](docs/DIALECT.md#data-type-privilege-descriptors).
Smoke: `make check RUN='^TestReferenceDataTypePrivilegesFeatureSmoke$'`
and the same command with `REFERENCE=1` (external 900-second deadlines).


Change note: role membership GRANT/REVOKE supports ADMIN/INHERIT/SET options,
independent grantor origins, source-aware CASCADE/RESTRICT and cycle prevention.
CREATE initial IN ROLE/ROLE/ADMIN and ALTER GROUP ADD/DROP USER publish atomically.
INHERIT paths supply real object privileges and owner authority, including fresh
prepared reads after revocation. CREATE/ALTER ROLE and USER support SUPERUSER,
CREATEDB, CREATEROLE, LOGIN, INHERIT, REPLICATION, BYPASSRLS, CONNECTION LIMIT,
PASSWORD and VALID UNTIL. Defaults, omitted ALTER retention and direct (never
inherited) attribute authority follow PostgreSQL18; ordinary creators receive
ADMIN without INHERIT/SET. Passwords are meaningful fixed-default SCRAM metadata,
not a native authentication handshake; expiry does not block trusted admission.
SCRAM derivation observes cancellation, including encoded high-iteration
empty-password checks; cancellation publishes no attribute change and releases
the physical-session operation. Contextual attribute words remain valid member
names in initial memberships and ALTER GROUP lists.
Physical connections count against their original authenticator's limit, including
idle pool entries; reuse adds no slot and physical close releases one. Superusers
bypass limits, not NOLOGIN (FATAL28000). Role utilities remain autocommit-only
natively; Reset/Restore preserve roles and membership while Dump omits cluster-role
DDL. Public-safe `pg_user` exposes the nine-column LOGIN-role summary; `pg_roles`
exposes a thirteen-column summary of all roles' direct attributes, with PUBLIC SELECT,
a constant eight-star password mask and typed NULL role configuration. Legacy
`pg_group` exposes exactly NAME groname, OID grosysid and OID[] grolist for current
NOLOGIN roles. Direct membership records contribute one member OID each, including
duplicates across grantors and all-false options; no recursion or array order is
promised. Empty lists are non-NULL `'{}'`. Unrelated ordinary readers see groups
and member identities, never credentials. Fresh held executions observe role/edge
changes while already-open native rows remain captured. Native controls preserve
roles/membership and omit them from Dump; group writes remain native0A000 catalog
mutation exclusions, not PostgreSQL55000 non-updatable-view claims.
Membership never synthesizes summary flags. Rename, role-setting population
commands and `pg_authid`/`pg_shadow` remain excluded. Reading `pg_db_role_setting`
is supported with the empty-owner boundary described above.
Object GRANT/REVOKE supports the selected
table/column privileges through the existing ACL authority. Native DROP and
COMMENT retain their existing actual-SUPERUSER gates; broader PostgreSQL DROP
authority stays a backend-specific diagnostic supplement, not a native permission
expansion. See the exact [pg_roles](https://www.postgresql.org/docs/18/catalogs.html)
and [pg_group](https://www.postgresql.org/docs/18/catalogs.html) contracts.
Group smoke: `make check RUN='^TestReferencePGGroupFeatureSmoke$'`; repeat with
`REFERENCE=1` for pinned PostgreSQL18.0, each with an external900-second deadline.
Observed public smoke exercised creator ADMIN, ALTER USER omission, expired trusted
admission, limit release, NOLOGIN and held authority refresh. Repeatable smoke:
`make check RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'`;
add `REFERENCE=1` for the pinned PostgreSQL counterpart.
Ordered GRANT targets use preceding private changes for authority; REVOKE uses
statement-entry authority and rows, then publishes once. Repeated REVOKE targets
that change the same original tuple fail XX000 and roll back the statement;
missing rows warn and already-false ADMIN option revocation remains a no-op.
Feature smoke: `make check RUN='^TestReferenceRoleMembershipFeatureSmoke$'`
and the same command with `REFERENCE=1` for pinned PostgreSQL18.0.

Feature smoke: `make check RUN='^TestSessionIdentitySmoke$'`.

CREATE TABLE supports named and unnamed column/table CHECK constraints, including
`NO INHERIT`. Constraints see the completed new row after defaults/coercions:
TRUE and NULL pass; FALSE rejects INSERT/UPDATE with PostgreSQL-style `23514`
constraint identity and privilege-filtered failing-row details. A late failure
rolls back all row writes in the statement. Definitions survive native
Dump/Restore and column/table rename; CHECK does not change declared nullability.
See [CHECK constraints](https://www.postgresql.org/docs/18/ddl-constraints.html).
Feature smoke: `make check RUN='^TestCheckSmoke$'`.

Constraint and stored-expression introspection supports all four PostgreSQL
signatures: `pg_get_constraintdef(oid [, boolean])` and
`pg_get_expr(pg_node_tree, oid [, boolean])`, returning TEXT or NULL.
The full `pg_catalog.pg_constraint` projection and bounded `pg_catalog.pg_attrdef`
projection expose real definition identities and immutable structural trees. Compact/pretty
decompilation traverses bound definitions without evaluating them, uses current
column names, and resolves Vars against the supplied relation's stable attributes.
Both modes retain nonassociative grouping, current search-path provider visibility,
implicit function-argument annotations and raw preassignment/precision metadata.
Returned SQL consumes bounded same-type and existing directional numeric casts;
literal NULL::unknown retains provenance without adding a concrete UNKNOWN type.
Change note: synthetic numeric comparison annotations use the base type, never the opposite
column's precision/scale; genuinely explicit sizing casts remain executable.
Identity keywords and ordinary calls remain symbolic under role/path changes;
reserved ordinary names retain quotes, without capturing authority values.
Consumer fixtures distinguish conditional NULL/branch behavior, controlled comment
state, session paths/zones and per-backend seeded random effects. Exact renderer
strings or one successful insertion alone are not behavioral compatibility proof.
Native tree output is not PostgreSQL's physical node serialization; non-NULL
external tree input is rejected. See [deparsing](https://www.postgresql.org/docs/18/functions-info.html).
Feature smoke: `make check RUN='^TestReferenceDeparseConstraints'`.

Change note: `pg_catalog.pg_attrdef` exposes `oid OID`, `adrelid OID`,
`adnum INT2`, and `adbin PG_NODE_TREE`. All four native descriptors are known
NOT NULL even for an empty result. Supported ordinary defaults and stored
generation expressions carry real definition/owner identities and stable
attribute numbers across rename/drop; identity columns have no default row.
Domain-coerced NULL pruning remains a native DDL/storage limitation, not a
catalog projection fix. Dedicated lifecycle/control smoke:
`make check RUN='^TestReferenceAttrdefControlsSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceAttrdefControlsSmoke$'`.

Time-zone-aware `TIMESTAMPTZ` and `TIMETZ` values support per-physical-session
TimeZone/DateStyle, named IANA zones, abbreviations, fixed offsets and
`AT TIME ZONE` / `AT LOCAL` and all nine `timezone(...)` overloads.
Timestamptz stores an instant; timetz retains
its fixed offset. Rules are embedded from the pinned PostgreSQL 18.0 image,
not the host timezone installation. See [temporal types](https://www.postgresql.org/docs/18/datatype-datetime.html)
and [session settings](https://www.postgresql.org/docs/18/runtime-config.html).
Native logical Dump/Restore preserves these values independently of session
presentation. `date_add` / `date_subtract` accept timestamptz plus interval,
with an optional explicit zone for calendar months/days; elapsed microseconds
remain instant arithmetic. The five SQL current-date/time producers are supported
separately. This does not add timezone catalog views.
Change note: `timezone_abbreviations` now selects embedded stock Default,
Australia or India for all temporal input consumers, with session-zone priority.
It does not change output labels or load customized installation files.
DateStyle/IntervalStyle and native UTC defaults retain their existing contracts.
Dual-mode feature smoke:
`make check RUN='^TestReferenceTimezoneAbbreviationsFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceTimezoneAbbreviationsFeatureSmoke$'`.
Public feature smoke: `make check RUN='^TestReferenceDatetimeZoneFeatureSmoke$'`;
pinned oracle: add `REFERENCE=1` to the same command.

Datetime delays support `pg_sleep(double precision)`, `pg_sleep_for(interval)`
and `pg_sleep_until(timestamp with time zone)`, including inferred prepared
parameters. Successful non-NULL calls return an empty, non-NULL VOID value;
strict NULL calls return NULL without waiting. Fractional waits are never
intentionally shortened. Interval months/days use the captured session TimeZone
and calendar arithmetic; absolute deadlines use the execution-time wall clock.
Zero, negative finite floats, NaN, negative infinity and past deadlines complete
without a delay; positive infinity and huge positive values remain cancellable.
Existing transaction/table/session advisory locks are **not released by sleep**.
SQL statement timeout reports57014; native caller cancellation preserves ordinary
Go context errors and existing transaction rollback/owner cleanup.
See [delay contracts](https://www.postgresql.org/docs/18/functions-datetime.html).
Dual-mode smoke: `make check RUN='^TestReferenceDatetimeSleepFeatureSmoke$'`,
then the same command with `REFERENCE=1`. The standalone public source and
root-only materialization/run instructions are in
[testing](reference/datetime/README.md#datetime-delay-selected-proof).


SELECT and INSERT SELECT support comma products, CROSS JOIN and inner or
LEFT/RIGHT/FULL [OUTER] joins with ON, USING or NATURAL, including nested joins,
FROM/column aliases and the special USING alias. Unmatched operands receive
typed NULLs; ON determines matches before WHERE filters output. Duplicate
occurrences survive (the documented fixture yields LEFT6/RIGHT6/FULL8).
Prepared executions freshly bind every source, and eager results survive native
Reset/Restore. See [join semantics and limits](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQuery(OuterJoins|JoinsPreservation)'`;
pinned reference: `make check REFERENCE=1 RUN='^TestReferenceQuery(OuterJoins|JoinsPreservation)'`.

Alias diagnostic fidelity includes suggestions using exposed renamed columns,
Unicode character positions, fuzzy-tie and distant-qualifier suppression.
Exact inaccessible columns take precedence with PostgreSQL's singular/plural
Detail rather than misleading spelling suggestions. Hidden within-entry exact
duplicates retain positioned ambiguous-column errors, even for distant qualifiers.
Output labels stay invisible to sibling targets and WHERE; bare ORDER labels
prefer outputs while compound/qualified ORDER uses inputs. This corrects
missing-name diagnostics without changing resolution or physical privileges.
See [query labels and relation aliases](https://www.postgresql.org/docs/18/queries.html).
Public feature smoke: `make check RUN='^TestReferenceQueryAliases'`.

DELETE also accepts the supported query `USING` source trees, including joins,
derived SELECT/VALUES, views and self-joins. WHERE/RETURNING share target/source
scope; the target is inaccessible inside source JOIN ON and derived queries.
Each matched physical target is deleted/counts/returns once despite repeated
source matches; identical target rows remain distinct. Source rows and qualifying
pair identities stay captured through row waits; only the same target predecessor
and predicate are refreshed. No arbitrary source winner or output order is promised.
See [DELETE USING](https://www.postgresql.org/docs/18/sql-delete.html).
Feature smoke: `make check RUN='^TestReferenceDeleteUsingSmoke'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDeleteUsingSmoke'`.

DELETE supports `RETURNING *`, scalar expressions and PostgreSQL18 OLD/NEW
output aliases. Deleted occurrences return their actual pre-deletion values
(including committed updates encountered after a row wait); ordinary NEW columns
are typed NULL. Direct and held prepared Query return eagerly finalized rows,
including full metadata for no matches; Exec still evaluates output and reports
the actual count. Output failure or prepublication cancellation discards all
transactional deletions, while sequence draws retain their usual nonrollback
semantics. Native Dump/Restore records surviving objects/data, not result history.
See [DELETE RETURNING](https://www.postgresql.org/docs/18/sql-delete.html).
Migrated public routes: `make check RUN='^TestReferenceDeleteUsingMigrated'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDeleteUsingMigrated'`.

UPDATE supports PostgreSQL18 `RETURNING` expressions, mixed stars and output-only
OLD/NEW aliases. Ordinary outputs read the completed new row; OLD reads the actual
predecessor, including after a row wait. Same-value matches and duplicates each
return one occurrence; no matches retain full metadata. Query owns eager values,
and Exec still evaluates outputs before reporting its count. Output errors roll
back staged mutations, not sequence draws, under existing transaction/savepoint
rules. See [UPDATE RETURNING](https://www.postgresql.org/docs/18/sql-update.html).
Feature smokes: `make check RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)'`; pinned mode: `make check REFERENCE=1 RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)'`.

UPDATE also accepts AS/bare target aliases and the supported inner/outer `FROM` trees using
the existing query namespace. Source-qualified SET/WHERE/RETURNING expressions
read one coherent captured source view; ordinary target outputs read NEW.
Multiple eligible source pairs may evaluate SET, but each physical target is
replaced/counts/returns once with its winning source pair, without a promised
winner or traversal order. After a row wait only that target predecessor and
predicate are refreshed; source rows and candidate identities stay frozen.
See [UPDATE FROM](https://www.postgresql.org/docs/18/sql-update.html).
Feature smoke: `make check RUN='^TestReferenceUpdateFromSmoke$'`.
Pinned PostgreSQL witnesses: `make check REFERENCE=1 RUN='^TestReferenceUpdateFrom'`.

Schema search paths are physical-session settings: missing/inaccessible entries
are ignored, `$user` follows the current role, qualification ignores the path,
and creation selects the first usable explicit schema without CREATE-denial
fallback. The common namespace resolver routes relations, types and supported
builtin functions/operators; `pg_catalog` qualification and
`OPERATOR(pg_catalog.+)` preserve their provider boundaries.
The selected `min_scale`, `round`, `scale`, `trim_scale`, `trunc`, `sign`, `mod`,
`abs`, `ceil`, `ceiling`, `div`, `exp`, `factorial`, `floor`, `gcd`, `lcm`, `acos`, `acosd`,
`asin`, `asind`, `atan`, `atand`, `atan2`, `atan2d`, `cos`, `cosd`, `cot`, `cotd`,
`sin`, `sind`, `tan` and `tand` providers use the same
namespace checks. `abs` preserves integer/REAL widths; decimal division and
factorial are exact, and decimal exponential preserves independently selected
precision (up to 1000 fractional places). `gcd` and `lcm` preserve the selected
INTEGER/BIGINT/NUMERIC result width, return nonnegative exact results, and retain
the greater numeric input scale. Trig uses FLOAT8 signatures,
with exact notable degree angles, direct-function poles and atan2(y,x)
quadrant/IEEE semantics. See the
[mathematical signatures and limits](https://www.postgresql.org/docs/18/functions-math.html).
Selected public checks: `make check RUN='^TestReferenceMathScalar'`,
`make check RUN='^TestReferenceMathTrigDirectFeatureSmoke$'` and
`make check RUN='^TestReferenceMathGcdLcm'`, each also with `REFERENCE=1`.

Change note: `lgamma`, `ln`, `log`, `log10`, `power`, `sqrt` and `width_bucket`
use the existing expression, namespace and stored-definition owners. Logarithms,
roots and powers retain their documented NUMERIC/FLOAT8 overloads; binary `log`
is NUMERIC-only and `lgamma` is FLOAT8-only (log of the absolute gamma value).
Decimal logarithms/roots compute decimal digits directly rather than formatting
a binary64 approximation. Four-argument buckets support ascending and reversed
finite bounds with lower-inclusive, upper-exclusive buckets. The threshold-array
overload accepts existing comparable element types and counts duplicate equal
thresholds through the rightmost match. Thresholds must be sorted ascending:
PostgreSQL does not validate sortedness, and unsorted inputs have unexpected
results. Native stored/default/generated/CHECK/view expressions and symbolic
Dump/Reset/Restore retain selected overloads and all four bucket arguments.
See [mathematical functions](https://www.postgresql.org/docs/18/functions-math.html)
and [selected proof](reference/numeric/README.md#logarithms-roots-powers-and-buckets).
Feature smoke: `make check RUN='^TestReferenceMathLogPowerFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceMathLogPowerFeatureSmoke$'`.

Change note: explicit BYTEA→TEXT (`v::text`, `CAST(v AS text)`, `text(v)`)
uses the statement's `bytea_output`: lowercase hex by default, or PostgreSQL
escape/octal output. Both textual input formats remain accepted in either mode.
Raw BYTEA results and inferred binds remain owned raw `[]byte`/octets; this adds
no implicit or reverse TEXT→BYTEA cast. Held column and bound casts capture the
mode at execution; constant-only expressions retain existing immutable folding.
SHOW/SET/RESET, ordinary-user access and transaction/LOCAL/savepoint restoration
reuse the session setting contract. Dump stays canonical hex, independent of
live mode; Reset/Restore preserve live settings and retained results.
See [BYTEA](docs/TYPES.md#bytea). Selected witnesses:
`make check RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`.

Change note: BYTEA `bit_count`, `get_bit`, `get_byte`, `set_bit` and `set_byte`
share the immutable raw-octet provider with `reverse`. Byte offsets are zero-based;
bit offsets are INT8 and number each byte's bits least-significant first.
Getters return INT4, count returns INT8, and setters return independent BYTEA
values. New bits must be 0/1; new bytes retain the low eight bits of any INT4.
Bounds fail with 2202E before new-bit validation (22023). NULL suppresses body
errors, not input, signature or authority checks. Unknown operands participate
in PostgreSQL's BYTEA/BIT overload competition; BIT access uses logical
leftmost-first indexing rather than reinterpreting BYTEA payloads.
See [BYTEA access](https://www.postgresql.org/docs/18/functions-binarystring.html).
Feature smoke: `make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.

Change note: BIT/VARBIT support `bit_count`, `bit_length`, `length`,
`octet_length`, `overlay`, `position` and `substring`. Measurements count logical
bits; octet length rounds up to bytes. Count returns INT8; other measurements
and position return INT4. Slices and overlays return unrestricted BIT (typmod -1),
independently of input width; destination assignment still enforces its own width.
These immutable functions are strict. Negative overlay counts can duplicate bits,
substring positive INT4 end overflow clips to the end, and empty-source position
returns zero, even for an empty needle. Stored calls remain symbolic through
Dump/Restore. The `substr(bit)` alias remains excluded;
scalar operators have their [own contract](reference/binary_json_arrays/README.md#bit-string-scalar-operators).
See [PostgreSQL bit functions](https://www.postgresql.org/docs/18/functions-bitstring.html).
Feature smoke: `make check RUN='^TestReferenceBitStringFunctionsFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.

Change note: BIT/VARBIT `get_bit` and `set_bit` use INT4 indexes with the
leftmost logical bit at index zero, unlike BYTEA's LSB-first INT8 indexes.
The getter returns INT4; the setter returns unrestricted BIT, preserving all
other digits and the original immutable source. Bounds fail with 2202E
(`bit index N out of valid range (0..U)`) before replacement validation;
non-0/1 replacements fail with 22023 (`new bit must be 0 or 1`). Both are strict:
NULL suppresses body errors, not input, signature or authorization checks.
Defaults, generated columns, CHECKs and views preserve symbolic calls through
Dump/Reset/Restore and actual emitted SQL replay on PostgreSQL. General bit
casts, arrays and ordering/index/grouping providers remain separate boundaries.
See [bit access testing](reference/binary_json_arrays/README.md#bit-string-access).
Feature smoke: `make check RUN='^TestReferenceBitAccessFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.

BYTEA supports raw `||`, `bit_length`, `octet_length`, `length`, two-argument
`btrim`/`ltrim`/`rtrim`, and byte-indexed `overlay`, `position`, `substring`/`substr`.
SQL `OVERLAY(... PLACING ... FROM ... [FOR ...])`, `POSITION(needle IN source)`,
`SUBSTRING(... FROM ... [FOR ...])` (also reversed keywords and FOR-only), and
all BOTH/LEADING/TRAILING TRIM forms use the same strict raw-octet providers.
Offsets and counts are INT4 bytes, not characters; POSITION returns one-based
INT4 or zero when absent (an empty needle returns one). Negative substring
counts fail; negative overlay counts are valid and can duplicate source bytes.
Trim removes endpoint members of a byte set, preserving interior NUL/high bytes.
Known BYTEA operands infer unknown byte arguments; inferred Go binds remain raw
octets. Selected functions share query/write/CHECK/view consumers and native
replay, preserving SQL-syntax provenance in compact/pretty CHECK deparsing.
Encoding-aware BYTEA length, BYTEA DEFAULT functions and direct byteacat remain
excluded. Positional TEXT substring and the three-TEXT SQL-SIMILAR/two-TEXT
POSIX overloads are supported. No new cast edges or byteapos alias are added.
Core strings provide TEXT trim/concatenation siblings through the shared dispatch.
`length`, `char_length`/`character_length`, `octet_length` and `bit_length`
also measure strings; `strpos(source,needle)` and SQL POSITION search them.
Character counts and search positions count Unicode codepoints, not UTF8 bytes.
Exact CHAR octet counts include padding; its bit counts and search operands use
the trimmed TEXT conversion. Results are strict INT4, with zero for not found and
one for an empty needle. Selected string functions compose in DEFAULT/CHECK/views
and native replay. See [String length and search](https://www.postgresql.org/docs/18/functions-string.html).

`format(format_text[,arguments...])` supports PostgreSQL `%s`, `%I`, `%L`,
`%%`, positional arguments, sequential arguments and literal/asterisk widths.
Widths count Unicode codepoints in the final quoted spelling, never truncate,
and negative dynamic widths left-align. `%s` uses the original datatype's output;
`%I` quotes identifiers and `%L` quotes literals (including backslash `E` spelling).
The stable, nonstrict TEXT result preserves NULL and child-evaluation semantics.
Argument analysis transforms children once before the100-argument syntax limit,
then resolves namespaces/signatures; invalid children win over both failures,
including quoted format spellings and scoped missing nested calls.
Explicit `VARIADIC` consumes any already-supported typed array or domain over it,
including multidimensional, lower-bound, NULL and empty arrays. It borrows the
existing immutable array payload; format adds no array types or syntax. The shared expression works in
query/write/RETURNING, DEFAULT/CHECK/views and native replay.
See [String formatting](https://www.postgresql.org/docs/18/functions-string.html).
Feature smoke: `make check RUN='^TestReferenceFormatSmoke$'`;
the independent pinned PostgreSQL counterpart adds `REFERENCE=1`.

Change note: `concat(value[, ...])`, `concat_ws(separator,value[, ...])`,
`quote_ident(text)`, `quote_literal(text|anyelement)`,
`quote_nullable(text|anyelement)` and `pg_client_encoding()` are supported
unqualified or through `pg_catalog`, including direct/prepared queries,
DEFAULT/CHECK/views, writes/RETURNING and native Dump/Restore.
Concatenation uses each original datatype's OUTPUT (BOOL `t`/`f`, padded BPCHAR),
ignores individual NULLs and returns empty TEXT for all-NULL inputs.
`concat_ws` inserts its TEXT separator only between non-NULL values; a NULL
separator returns SQL NULL after evaluating every child. Explicit final
`VARIADIC` borrows one existing array in row-major order: empty/all-NULL arrays
produce empty TEXT, while a NULL array produces SQL NULL. Ordinary arrays
remain one canonical array-output operand.
Quoting converts ANYELEMENT to TEXT first (BOOL `true`/`false`, scalar BPCHAR
padding removed), then doubles identifier double-quotes or literal apostrophes
and backslashes; literal quoting uses uppercase `E` iff a backslash occurs.
`quote_ident` and `quote_literal` are strict; `quote_nullable(NULL)` is text
`NULL`. Identifier quoting uses all PostgreSQL keyword categories and obeys
`quote_all_identifiers` (default off), also used by `format('%I',...)` and the
supported deparse inquiries. Parameterless held statements capture successful
immutable physical quote output at first execution, not Prepare. Under the
approved bounded native policy, every parameterized execution instead uses a
fresh operation-local group, including DEFAULT and view expressions. PostgreSQL
may retain constants in generic plans; native deliberately has no cost-based
custom/generic planner or counters. Parameters and stable concat/format remain
live. `pg_client_encoding()` returns canonical NAME `UTF8`, including when SHOW
retains the alias `UNICODE`; it adds no transport encoding.
Feature smoke: `make check RUN='^TestReferenceStringVariadicFeatureSmoke$'`;
the independently asserted pinned PostgreSQL counterpart adds `REFERENCE=1`.
Review-policy smoke: `make check RUN='^TestReferenceStringVariadic(DeparseRelations|BoundFormat|ViewWrites|MergeDefaults|Parameterized)$'`,
also with `REFERENCE=1`; it independently asserts each backend's planner boundary.
See [variadic concatenation and quoting](https://www.postgresql.org/docs/18/functions-string.html).


String replacement supports `overlay(text,text,int4[,int4])` and SQL OVERLAY,
literal `replace` and codepoint-wise `translate`. Overlay counts characters,
including combining marks separately: zero count inserts, negative count can
duplicate a source interval, and a start beyond the end appends without padding.
Omitted count is the trimmed replacement's character length. Empty replace
search leaves the source unchanged; translation uses the first occurrence in
its `from` set and deletes characters with no corresponding `to` character.
`starts_with(text,text)` and `text ^@ text` are equivalent strict BOOL prefix
operations. All textual arguments use the existing BPCHAR-to-TEXT trim, while
TEXT/VARCHAR tails remain. These providers compose through queries, writes,
DEFAULT/CHECK/views and native Dump/Restore, retaining TEXT/BOOL metadata.
See [replacement and prefix rules](https://www.postgresql.org/docs/18/functions-string.html).
Independent native/reference witnesses:
`make check RUN='^(TestReferenceStringReplace(Overlay|Replace|Translate|Prefix|Signatures|Diagnostics|Collation|Metadata|Consumers|Atomicity|Stored|Controls|Cancellation)|TestReferenceBinarySliceCapabilityBoundaries)$'`;
the pinned PostgreSQL counterpart uses the same command with `REFERENCE=1`.
See [BYTEA](docs/TYPES.md#bytea). Feature smoke:
`make check RUN='^TestReferenceBinarySlice'`; independent pinned reference:
`make check REFERENCE=1 RUN='^TestReferenceBinarySlice'`.

BYTEA checksums and hashes operate on the raw octets, including NUL and high
bytes: `crc32`/`crc32c` return unsigned 32-bit checksums in INT8, `md5(bytea)`
returns 32 lowercase hexadecimal TEXT characters, and `sha224`/`sha256`/`sha384`/
`sha512` return raw BYTEA digests of 28/32/48/64 bytes. All are strict; empty
input has a nonnull digest. CRC/SHA infer BYTEA for unknown literals and binds;
MD5 unknown literals and parameters prefer TEXT; known BYTEA still selects
raw-octet hashing. TEXT MD5 hashes exact UTF8 bytes after implicit TEXT conversion
(BPCHAR padding is removed, VARCHAR spaces retained). MD5-first inference fixes
TEXT and a later SHA call fails `sha256(text)`; SHA-first fixes BYTEA. Hashes
compose in queries, writes, CHECKs and views. Parameter-free calls are admitted
in native Restore INSERTs; BYTEA hash DEFAULT functions remain excluded, while
TEXT MD5 DEFAULT is supported. Cryptographic hashes use bounded scratch and
check cancellation without payload-sized temporary copies. See the
[hash contract](https://www.postgresql.org/docs/18/functions-binarystring.html).
Feature smoke: `make check RUN='^TestReferenceBinaryHashSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceBinaryHash'`.

Integer `to_bin`, `to_oct` and `to_hex` accept exactly INTEGER or BIGINT and
return TEXT. Negative values use the selected 32/64-bit two's-complement width,
without a sign or radix prefix; hex is lowercase. SMALLINT and unknown input
are ambiguous (42725), requiring an explicit INTEGER/BIGINT cast. Typed NULL
returns NULL. These immutable functions and TEXT MD5 compose through existing
DEFAULT/CHECK/view/generated/index and Dump/Restore paths. Inferred TEXT MD5
binds retain the inherited adapter boundary: native typed nil `[]byte` is empty
TEXT, whereas pq sends NULL; ordinary nil is NULL on both.
Feature smoke in both modes:
`make check RUN='^TestReferenceStringNumericBaseFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceStringNumericBaseFeatureSmoke$'`.

Size conversion supports `pg_size_bytes(text) -> bigint` and
`pg_size_pretty(bigint|numeric) -> text` with exact binary units through PB.
Parsing scales before rounding to integral bytes, with ties away from zero and
signed BIGINT range checks. Pretty keeps values below 10240 in bytes:
`pg_size_pretty(1024::bigint)` is `1024 bytes`, not `1 kB`. Numeric bytes retain
fractional scale, and numeric pretty accepts values beyond BIGINT and NaN/infinity.
SMALLINT/INTEGER/unknown pretty inputs are ambiguous; cast explicitly.
Strict NULLs, stored consumers, native controls and symbolic selected overloads
reuse the existing typed path. See the
[size contract](https://www.postgresql.org/docs/18/functions-admin.html).
Feature smoke in both modes:
`make check RUN='^TestReferenceSizeFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceSizeFeatureSmoke$'`.

Change note: core strings support `||` string overloads, `btrim`/`ltrim`/`rtrim`,
SQL `TRIM`, `normalize` and `IS [NOT] [form] NORMALIZED`. Character operands
convert to TEXT, removing BPCHAR trailing spaces; mixed concatenation uses the
existing scalar type's text output and captured session output settings.
Normalization uses pinned Unicode16 data and PostgreSQL18.0 release semantics,
including its Hangul TBASE boundary and normalized-predicate quickcheck.
Stored string DEFAULT/CHECK expressions support compact/pretty SQL deparsing,
typed native structural carriers and syntax-preserving Dump/Restore. Ordinary
calls display resolved TEXT conversions; special syntax hides implicit casts.
Explicit-set BYTEA SQL TRIM keeps its special spelling through CHECK replay
without adding BYTEA DEFAULT or cast support. Parameter-free Restore INSERTs
accept the selected string functions, and trim checks cancellation during both
endpoint and set traversal without allocating a replacement string.
See [syntax and boundaries](https://www.postgresql.org/docs/18/functions-string.html).
Feature smoke: `make check RUN='^TestStringCoreSmoke$'`.

Change note: `lower`, `upper`, `casefold` and `initcap` accept one character
input and return unconstrained TEXT, retaining its collation. Default/C/POSIX
map ASCII only; `pg_c_utf8` uses simple Unicode16 mappings and
`pg_unicode_fast` uses full Unicode16 mappings, including expansions and
original-input final sigma with PostgreSQL18.0's leading-ignorable-prefix
boundary. Folding is not lowercasing or normalization.
BPCHAR padding is removed before conversion; TEXT/VARCHAR tails remain.
Collated unknown parameters are inferred as TEXT before collation lookup;
each occurrence retains its own collation without changing the held input type.
These functions compose through existing query/write/default/CHECK/view paths
and native Dump/Restore. Native encoding remains read-only UTF8; arbitrary
libc/ICU locales and Unicode-collation ILIKE remain explicit exclusions.
See [case rules](https://www.postgresql.org/docs/18/functions-string.html) and
[offline data generation](https://www.postgresql.org/docs/18/datatype.html).
Feature smoke: `make check RUN='^TestReferenceStringCaseSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceStringCaseSmoke$'`.

Change note: `CREATE TYPE [schema.]name AS ENUM ('sad','ok','happy')` creates
an immutable, owner-controlled logical scalar. Exact case/whitespace-significant
labels (at most 63 UTF8 bytes) compare and sort in declaration order; different
enums remain different types even when labels coincide. Unknown strings and binds
take contextual enum input; typed TEXT requires an explicit cast. NULL preserves
enum metadata identity. Native export/ScanType is string, DatabaseTypeName is
empty; pinned pq exports raw bytes with interface{} ScanType for enum OIDs.
Dump emits enums before dependent domains/tables/stored consumers, preserving
label order and owners. Fresh prepares after Reset/Restore use new identities;
retained eager rows remain owned. `ALTER TYPE ... ADD VALUE [IF NOT EXISTS]`
with optional BEFORE/AFTER and `ALTER TYPE ... RENAME VALUE` preserve existing
member identity, stored dependencies and held analyzed enum literals.
CREATE/ALTER enum utilities are transactional/savepoint-aware; demanded new
members on existing types fail55P04 until top-level commit. Same-type mutations
use actual catalog type locks; peers see committed generations only.
Dump/Restore remaps fresh imported member identities, including same-OID nested
values and stored literals. DROP TYPE, other ALTER TYPE forms and general direct
enum[] syntax/storage remain excluded.
See [enum types](https://www.postgresql.org/docs/18/datatype-enum.html).
Feature smoke: `make check RUN='^TestReferenceEnumSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceEnumSmoke$'`.
Enum alteration proof: `make check RUN='^TestReferenceEnumAlter.*$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceEnumAlter.*$'`.

Change note: `pg_catalog.pg_enum` exposes readonly typed member definitions as
`oid OID, enumtypid OID, enumsortorder FLOAT4, enumlabel NAME`. Owner IDs come from
the actual enum type; empty enums, domains and associated arrays add no rows.
Initial positions are 1..n; ADD supports negative/zero/fractional metadata and
dense-gap renumbering, preserving existing member OIDs. RENAME preserves owner,
OID and position. PUBLIC metadata discovery does not grant enum execution.
Native generated member OIDs are unique nonzero uint32-valued odd IDs, not a
promise to reproduce PostgreSQL's allocator numbers/parity. Ordinary catalog
transaction, prepared-query, view and retained-result authorities apply.
Logical recreation remints IDs and normalizes positions to 1..n.
See [pg_enum](https://www.postgresql.org/docs/18/catalog-pg-enum.html).
Feature smoke: `make check RUN='^TestReferencePGEnumFeatureSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferencePGEnumFeatureSmoke$'`.

Change note: `enum_first`, `enum_last` and both `enum_range` overloads use the
actual enum descriptor and declaration order. Unary calls ignore the datum,
including typed NULL; bounded ranges are inclusive and NULL endpoints select
boundaries. Ranges are actual computed enum arrays with canonical output, not
general enum[] syntax/storage admission. Calls are non-strict and STABLE, with
direct/prepared metadata and native stored-definition remapping.
Surviving calls reject generated columns and index expressions/predicates with
42P17; eliminated branches retain ordinary immutable admission. Computed ranges
and ordinary views remain supported, but enum-array CTAS/materialized storage
is excluded with or without data. PostgreSQL supports that storage; this is a
native capability boundary, not a PostgreSQL diagnostic.
Feature smoke: `make check RUN='^TestReferenceEnumFunctionsFeatureSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceEnumFunctionsFeatureSmoke$'`.

Qualified temporal declarations such as `pg_catalog.time(3)` and
`pg_catalog.timestamp(3)` retain precision through native Dump/Restore;
bounded temporal explicit casts and same-type precision annotations are supported.
`SELECT current_schemas(false)` returns a genuine NAME[] (`_NAME`)
as a native string, or NULL for a NULL boolean argument, through the shared
array storage/literal codec. CREATE TYPE forms other than AS ENUM and
function/operator DDL remain excluded. TEMP tables and sequences share physical
session ownership; introspection does not allocate an absent temporary namespace.
Native ORDER on NAME[] is explicitly unsupported at analysis, including empty
sources; PostgreSQL's array comparator remains outside the bounded native surface.
Feature smoke: `make check RUN='^TestSearchPathSmoke$'`.

Change note: standalone `ALTER TABLE [IF EXISTS] target SET SCHEMA destination`
moves ordinary tables, their B-tree/hash indexes and every column-owned sequence atomically, preserving
rows, named NOT NULL constraints, defaults, comments, ACLs and stored-view
dependencies. It requires table ownership, source USAGE and destination CREATE
(not source CREATE or destination USAGE). Held unqualified prepared SQL on both
backends retains its revalidated moved source while the effective search_path is
unchanged: creating a source-schema shadow alone does not redirect it. Referenced
definition/path/namespace invalidation reanalyzes original names; fresh direct and
newly prepared queries see the shadow. Native Dump/Restore records current
qualified definitions, not move history.
For a view, `ALTER TABLE view SET SCHEMA` aliases `ALTER VIEW SET SCHEMA` and
moves only the view's namespace, leaving its sources and dependents in place.
Explicit-transaction SET SCHEMA, generic standalone sequence/index moves and
TEMP/system destinations remain bounded native exclusions. Automatic table/view
row and associated-array catalog identities are supported; executable user
row/composite values use the existing logical codecs.
Feature smoke: `make check RUN='^TestAlterSchemaSmoke$'`.

Change note: ordinary `CREATE INDEX ... USING hash` supports one simple or
immutable same-table expression key, owned hash operator classes and existing
scalar/domain/enum usages. Real immutable buckets retain duplicate row/version
identities and omit NULL keys. A current, proven single-source strict `=` against
a compatible literal or freshly imported parameter uses actual bucket candidates
and the original SQL predicate; commuted equality is supported. Mixed types,
row-side coercions, class-coarser equality, joins/derived/views, explicit
transactions and unproved snapshots retain ordinary scans. No range, ordering,
NULL-search, index-only or physical PostgreSQL hash-page promise is made.
Hash rejects UNIQUE, INCLUDE, multiple keys and explicit direction/NULL options.
Invalid hash reloptions get PostgreSQL diagnostics; valid physical options remain
positioned Execute-time capability errors after semantic/collision checks.
Dump/Restore, TYPE, REINDEX and replacement preserve the actual method.
See [hash contracts](https://www.postgresql.org/docs/18/sql-createindex.html) and
[the design](docs/design/PG18-INDEX-HASH.md).
Feature smoke (each invocation has a900-second deadline):
`make check RUN='^TestReferenceHashIndexFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceHashIndexFeatureSmoke$'`.
Actual candidate-route witness:
`make check RUN='^TestHashIndexLookupFeatureSmoke$'`.

Change note: ordinary `CREATE INDEX [name] ON [schema.]table [USING btree]`
supports multicolumn simple or same-table scalar expression keys with ASC/DESC,
NULLS FIRST/LAST, current catalog COLLATE/operator classes and optional
`INCLUDE (column, ...)`. The 32-attribute limit counts keys plus included columns,
including repeats. INCLUDE is projectable table payload, never a search/comparison
or uniqueness key; it needs no ordering provider. Explicit classes select actual
owned semantics, including unsigned `oid_ops` on INTEGER and trailing-space-
insensitive `bpchar_ops` on TEXT. Expression keys, including `lower(text_column)`
and parenthesized operators, reuse the shared binder/evaluator and must be
immutable after constant branch simplification. Aggregate/subquery keys fail
at Execute after quiet Prepare.
Definitions share the relation namespace; immutable sorted logical key entries
stay current through committed batches and supported table lifecycle changes.
B-tree query correctness still uses ordinary snapshot scan/filter/sort, not an optimizer
or physical index-only promise; unordered SELECT has no new ordering guarantee.
Dump/Restore serializes actual current definitions and rebuilds entries. TYPE
reconstruction, including same-type key/payload TYPE with or without USING,
reselects old-default classes and revalidates retained nondefaults.
Native ordinary CREATE INDEX remains excluded in explicit transactions.
Ordinary standalone UNIQUE simple/composite keys are supported.
Concurrent creation additionally admits computed/provider-aware UNIQUE tuples,
including NULLS NOT DISTINCT, but not concurrent maintenance.
See [clause design](docs/design/PG18-INDEX-CLAUSES.md).
Feature smoke: `make check RUN='^TestReferenceIndexClausesFeatureSmoke$'`;
use the identical RUN with `REFERENCE=1` for the pinned PostgreSQL oracle.

Change note: `CREATE`, `ALTER` and `DROP OPERATOR FAMILY` support the implemented
btree, hash and GiST methods, including ordered ADD/DROP member lists, rename,
owner/schema changes and IF EXISTS notices. `pg_opfamily`, `pg_amop` and
`pg_amproc` expose exact released family/member identities, associated types,
strategy/support numbers and REGPROC support references. Incomplete families
are legal; ADD admission is not complete operator-class validation.
Classes select registered family support, captured immutably with their roots.
Loose family edits refresh metadata, not every warmed root. Operator-class DDL
adds real custom family→class→new-index association, including index construction
over existing rows and maintained INSERT. Class/default lookup uses exact method
OID independently of physical implementation. Incomplete definitions are legal;
executable index binding still requires genuine available native support.
Dump/Restore serializes classes, families and loose members with real dependencies,
current names and fresh destination support. Bootstrap objects remain pinned.
See [operator-family design](docs/design/PG18-INDEX-OPERATOR-FAMILY.md).
Feature smoke: `make check RUN='^TestReferenceOperatorFamilySmoke$'`;
use the identical RUN with `REFERENCE=1` for pinned PostgreSQL18.6.

Operator-class feature smoke:
`make check RUN='^TestReferenceOperatorClassFeatureSmoke$'`;
use the identical RUN with `REFERENCE=1` for pinned PostgreSQL18.6.
The comparison anti-fallback witness is
`make check RUN='^TestReferenceOperatorClassComparisonConsumption$'` (also
`REFERENCE=1`): a genuine subtraction support routine can raise22003 atomically,
where ordinary `btint4cmp` succeeds. No new scalar function, physical method,
arbitrary SQL callback ABI or btree query optimizer is introduced.
See [operator-class design](docs/design/PG18-INDEX-OPCLASS.md).


Ordinary B-tree partial indexes accept same-table immutable Boolean `WHERE` predicates. Only
TRUE rows enter the root; FALSE and NULL rows skip even derived-key evaluation.
Common write publication maintains qualifying/excluded transitions atomically.
Exact immutable conjunction implication can supply a sequential-scan membership
rejection mask; complete WHERE evaluation and sorting remain authoritative.
Unresolved parameter atoms cannot prove a fixed predicate, while independent
exact conjuncts can. Private/historical/stale views always fall back to scanning.
Standalone HASH WHERE remains excluded, including syntactic WHERE TRUE, with
Execute-time0A000 `partial indexes are unsupported`; EXCLUDE's local HASH predicate
is supported without widening ordinary CREATE INDEX.
Partial CONCURRENTLY creation also remains excluded at Execute with unpositioned
0A000 `partial concurrent indexes are unsupported`; ordinary concurrent support
is unchanged, including its computed/provider-aware UNIQUE and nonunique HASH.
This is not an index traversal, cost or inequality optimizer. Dump/Restore,
rename, dependency drops and TYPE use original predicate definitions and rebuild
destination membership. Partial UNIQUE uses the shared uniqueness owner: only
TRUE members reserve/check keys, including private rows and batch prefixes.
Default NULLS DISTINCT and explicit NULLS NOT DISTINCT apply within that subset.
Predicate-only TYPE validates final rebound membership at Execute. Analyzed
standalone Boolean TRUE normalizes to no predicate/no WHERE and ordinary strong
key locks; retained predicates stay partial even if execution simplifies TRUE.
Ordinary HASH remains supported, but its existing native `WHERE` exclusion
returns `0A000` at Execute, including syntactic `WHERE TRUE`; PostgreSQL admits
these partial HASH definitions.
Feature smoke: `make check RUN='^TestReferencePartialIndex(FeatureSmoke|UniqueFeatureSmoke|TypeGeneration|WindowDiagnostics)$'`;
use the identical RUN with `REFERENCE=1` for the pinned PostgreSQL oracle.
Shared smokes: `make check RUN='^TestReferenceBtreeIndex(Regression|Controls|Lifecycle)$'`;
use the same RUN with `REFERENCE=1` for independent PostgreSQL18.0 outcomes.
Expression regression family: `make check RUN='^TestReferenceExpressionIndex'`;
use the same RUN with `REFERENCE=1` for independently expected PostgreSQL18.0 outcomes.

Change note: CREATE TABLE column/table UNIQUE supports ordered simple keys and
`NULLS DISTINCT` (default) or `NULLS NOT DISTINCT`. Each retained constraint owns
a real immutable logical B-tree; INSERT and UPDATE enforce typed equality
immediately, with atomic failures and transaction-owned key claims. Constraint
and backing-index identities survive renames and schema moves, rebuild on key
TYPE, and disappear on DROP CONSTRAINT/key column/table. Dump/Restore emits the
constraint once and reconstructs its backing index. CREATE deferrability remains
excluded; ALTER ADD UNIQUE follows the existing-data contract below.
See [UNIQUE constraints](https://www.postgresql.org/docs/18/ddl-constraints.html) and
[the design](docs/design/PG18-DDL-UNIQUE.md).
Feature smoke: `make check RUN='^TestReferenceUniqueFeatureSmoke$'`.

Change note: CREATE TABLE column/table `PRIMARY KEY` supports scalar and composite
ordered keys. Every primary key owns a real unique B-tree and separate NOT NULL
identities for its key columns. Duplicate and NULL keys fail with23505/23502;
failed multirow writes and TYPE conversion are atomic. `pg_constraint` exposes
kind `p` and canonical PRIMARY KEY deparsing. Existing renames, schema moves,
REINDEX, LIKE INCLUDING INDEXES/ALL and native Dump/Restore preserve the attachment.
Dropping the primary key leaves its NOT NULL identities; live key columns cannot
drop NOT NULL. Key TYPE changes rebuild affected constraint/index/NOT NULL identities.
ADD COLUMN PRIMARY KEY, CREATE deferrability, WITHOUT OVERLAPS and index
parameters remain excluded. ALTER ADD key lists and foreign keys follow the
existing-data constraint contract below.
See [primary-key constraints](https://www.postgresql.org/docs/18/ddl-constraints.html).
Feature smoke: `make check RUN='^TestReferencePrimaryKeyFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePrimaryKeyFeatureSmoke$'`.

Change note: `ALTER TABLE ... ADD [CONSTRAINT name] UNIQUE | PRIMARY KEY
USING INDEX index` attaches an eligible existing unique B-tree without rebuilding
its data or changing its index identity. Named attachments rename the backing
index with a notice; failed multi-action ALTER leaves the whole original group
and rows intact, while notices survive failure. Primary keys add only missing
key NOT NULL identities; INCLUDE remains nullable payload. INCLUDE TYPE/DROP,
LIKE, ownership, prepared consumers and native Dump/Restore use the actual backing
definition. Immediate ALTER properties are admitted; actual deferred constraints,
partial/expression indexes and nondefault key behavior are not eligible.
See [existing-index attachments](https://www.postgresql.org/docs/18/sql-altertable.html).
Feature smoke: `make check RUN='TestReferenceUsingIndexFeatureSmoke'`;
pinned counterpart: `make check RUN='TestReferenceUsingIndexFeatureSmoke' REFERENCE=1`.

Change note: `ALTER TABLE ... ADD [CONSTRAINT name]` supports CHECK, NOT NULL,
UNIQUE, PRIMARY KEY, FOREIGN KEY and the bounded EXCLUDE forms. Each valid
attachment validates the effective existing committed/private rows atomically.
CHECK/NOT NULL/FOREIGN KEY accept NOT VALID: old violations remain, but real
subsequent writes are enforced. Other kinds reject NOT VALID with0A000. Index
attachments/builds precede historical CHECK validation; competing index builds
retain their declaration order. Fresh primary keys and USING INDEX reject an
existing invalid NOT NULL definition through their distinct55000 diagnostics.
Native Dump emits invalid definitions after ordinary data and Restore replays
them as real eligible ADD statements; it never bypasses INSERT validation.
Historical catalog projections are not fresh DML. FK equality suppression uses
the actual committed predecessor; a prior own version must still be checked.
See [existing-data ADD constraints](https://www.postgresql.org/docs/18/sql-altertable.html).
Feature smoke: `make check RUN='^TestReferenceAlterAddConstraintFeatureSmoke$'`;
use the identical RUN with `REFERENCE=1` for the pinned PostgreSQL18.0 oracle.

Change note: CREATE TABLE and existing ALTER TABLE ADD CONSTRAINT support
`EXCLUDE [USING btree|hash] (... WITH =)` over owned scalar operator-family
equality. Original-type operator resolution, commutativity and actual family
membership govern admission; binary index compatibility alone is insufficient.
Immutable expressions, supported classes/collations, B-tree decorations/INCLUDE,
and optional TRUE-only predicates reuse the existing index definitions.
HASH retains its existing single-key/no-INCLUDE shape, with constraint-local
predicates. Any NULL key makes that pair nonconflicting.
Immediate and deferred checks share typed key claims, actual dirty row/version
projections and existing transaction-completion waits. DEFERRABLE,
INITIALLY IMMEDIATE/DEFERRED and SET CONSTRAINTS affect selective recheck events,
not transaction READ DEFERRABLE. Omitted/named ON CONFLICT DO NOTHING can select
nondeferrable exclusions; unique inference cannot. Selected deferrable arbiters
fail55000 only when a row is demanded. UNIQUE deferral remains unsupported.
Native Dump/Reset/Restore preserves definitions and data, not transient modes or
events. The bounded logical GiST provider supports INET equality/overlap and
range overlap for exclusion constraints. General GiST/SP-GiST and extension-backed
providers, physical indexes/WAL and server pg_dump are not introduced.
See [the contract](https://www.postgresql.org/docs/18/ddl-constraints.html)
and [design](docs/design/PG18-DDL-EXCLUSION.md).
Feature smoke: `make check RUN='^TestReferenceExclusionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceExclusionFeatureSmoke$'`.

Change note: INSERT supports `ON CONFLICT DO NOTHING`, with an omitted target,
column/expression inference plus optional partial predicate, or named constraint.
Every matching installed unique provider participates; omitted/named targets also
select supported nondeferrable exclusion providers. Selected collisions skip
individual proposals while unselected violations and earlier row validation
still reject the whole statement. Typed equality, current privileges, held
rebinding, cancellation, key claims and stable-snapshot visibility use their
existing owners. DO UPDATE and RETURNING use their shared action/output contracts.
Partial inference preserves nested TRUE/FALSE/NULL values and released conservative
proof behavior. Stable selected collisions recover visibility through checked
immutable index/storage positions, without an added per-duplicate storage scan.
See [the contract](https://www.postgresql.org/docs/18/sql-insert.html) and
[design](docs/design/PG18-DML-INSERT-CONFLICT.md).
Feature smoke: `make check RUN='^TestReferenceInsertConflictFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceInsertConflictFeatureSmoke$'`.

Change note: `CREATE UNIQUE INDEX` supports simple/composite keys, default or
explicit `NULLS DISTINCT`, and trailing `NULLS NOT DISTINCT`. Existing duplicates
reject the build atomically with23505; writes use the same immediate typed
enforcement, transaction-owned key claims and current liveness as table UNIQUE.
Directions, NULL ordering, compatible owned classes/collations and INCLUDE remain
substantive; INCLUDE never participates in equality. Unique expression keys and
BPCHAR classes on TEXT/VARCHAR are explicit0A000 boundaries; ordinary indexes
retain those existing capabilities. A standalone unique index has no synthetic
pg_constraint row: rename preserves its identity and updates diagnostics, DROP
frees enforcement, TYPE revalidates converted rows, and Dump emits it exactly once
after table/data. Build and runtime key disclosure both require current SELECT
on the table or every key column, even for owners. Native explicit writable
utility transactions remain excluded; read-only25006 precedes lookup.
Feature smoke: `make check RUN='^TestReferenceUniqueIndexFeatureSmoke$'`;
repeat with `REFERENCE=1` for the pinned PostgreSQL oracle.



Change note: `ALTER TABLE target ALTER [COLUMN] name SET DEFAULT expression`
and `DROP DEFAULT` atomically change future omitted/DEFAULT inserts without
rewriting existing rows. Held prepared INSERTs use the current definition;
DROP of an absent default succeeds and restores implicit NULL. ALTER accepts
general supported Boolean expressions without CREATE DEFAULT's extra parentheses.
Dump/Restore preserves the current definition, not ALTER history.
Standalone `ONLY`, `ONLY(...)`, qualified/quoted targets and trailing `*` are
supported; ordinary graph defaults recurse unless ONLY selects the named table.
Action lists and ALTER TABLE IF EXISTS for SET/DROP DEFAULT, UPDATE DEFAULT and
explicit-transaction SET/DROP DEFAULT remain excluded. Selected ADD COLUMN/
TYPE/nullability/DROP COLUMN lists support explicit transactions.
See [column defaults](https://www.postgresql.org/docs/18/ddl.html).
Feature smoke: `make check RUN='^TestAlterDefaultSmoke$'`.

Change note: `ALTER TABLE target ADD [COLUMN] [IF NOT EXISTS] name type`
atomically appends metadata and old-row values, with supported NULL/NOT NULL,
DEFAULT and column CHECK declarations. Missing defaults backfill typed NULL;
Immutable/Stable defaults evaluate once at ADD (even for an empty table), while
the existing fixed-nextval default evaluates per live row. Future INSERTs use
the retained expression, not the captured old-row value. Validation failure
preserves schema/data; already demanded sequence draws are not rolled back.
IF NOT EXISTS notices and skips before semantic declaration binding.
Local standalone-table behavior only: no descendant propagation, serial/identity/
generated ADD declarations, new default functions or mixed SET/DROP DEFAULT lists.
Logical Dump/Restore preserves final definitions/data, not ADD history or physical
PostgreSQL tuple storage. Feature smoke:
`make check RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$'`.


`CREATE [OR REPLACE] [TEMP | TEMPORARY] VIEW` definitions use the ordinary typed SELECT pipeline,
including admitted joins, repeated self-join occurrences, nested views, explicit
output names and `security_invoker` / `security_barrier` options. Every source
occurrence retains its frozen attribute layout and read-attribute identities across table,
column and schema renames; dependencies block restrictive drops, including
columns used only by a second source's join condition. Each execution checks
current privileges for every source, even for empty results. Bounded, queryable
`pg_class`, `pg_rewrite`, `pg_views` and `pg_indexes` sources expose actual catalog
state, with OID and PostgreSQL internal CHAR metadata. Native Dump/Restore
persists definitions, not their result rows. See [views](https://www.postgresql.org/docs/18/sql-createview.html).
CREATE OR REPLACE preserves view/rule identity, ownership, ACLs and dependents,
allows trailing outputs and rejects changed existing names/types/typmods/collations.
ALTER VIEW supports relation/column rename, owner/schema changes and security/
check-option SET/RESET; grouped DROP VIEW supports RESTRICT/CASCADE. Cyclic replacement is
allowed, while recursive reads and native cyclic Dump explicitly fail.
Feature smoke: `make check RUN='^TestReferenceView(Rows|Barrier|SmokeCatalogReplayControls)$'`.
Replacement smoke: `make check RUN='^TestReferenceViewReplace(Definition|Diagnostics|Prepared|SmokeCycleReplayControls)$'`.

Change note: `pg_catalog.pg_class` now exposes the
[PostgreSQL 18 34-column schema](https://www.postgresql.org/docs/18/catalog-pg-class.html)
in released order, rather than the former native ten-column projection.
Positional `SELECT *` consumers must update Scan destinations; named projections
remain the recommended interface. Existing relation-creating features own the
rows, including actual automatic sequences and backing indexes. Identity,
namespace, ownership, kind, row-type/typed-table links, attribute high-water,
CHECK counts, RLS and population come from installed descriptors, not row counts.
Represented builtin view classification does not change their execution sources.
All twenty-two represented table catalogs own audited indexed-table metadata,
including `pg_proc`, `pg_collation`, `pg_enum`, `pg_conversion`, `pg_ts_dict`
and `pg_ts_template`, together with `pg_attribute`, `pg_namespace`, shared
`pg_database` and `pg_db_role_setting`; represented views report no indexes.
These facts survive
Reset/Restore without inventing missing native backing-index rows.

Stored `relacl` preserves grantor/grantee identities, PUBLIC, privilege/grant-option
masks, order and NULL versus empty arrays. Reading metadata does not grant access
to its described tables. `relhasindex` and `relhassubclass` preserve immutable
lazy publication history after the last index/child disappears. Fresh executions
of retained catalog readers use current catalog/role snapshots; native eager
rows retain captured values and metadata.

Installed view options preserve presence, explicit false, validated lexical
values and order. SET retains unmatched old options then appends touched keys;
RESET removes keys; CREATE OR REPLACE replaces the installed list while retaining
its existing identity/ownership/ACL contract. Dump emits only installed options,
so replay no longer injects omitted false defaults. Table partition bounds use
typed immutable structural carriers. These reads do not add physical storage,
trigger catalogs, hierarchy alteration or catalog GRANT/REVOKE SQL; see the
[native boundaries](docs/DIALECT.md#pg_class-logical-relation-metadata).
Feature smoke: `make check RUN='^TestReferencePgClassSmoke$'`, also with
`REFERENCE=1`; complete [selected proof](reference/catalogs/README.md#pg_class-selected-proof).

Change note: simple one-table (including nested) views now accept INSERT,
UPDATE and DELETE through stable base-column identities. Reordered/renamed
outputs, omitted base defaults, identity/generated columns, existing UPDATE FROM,
DELETE USING and UPDATE/DELETE RETURNING retain their ordinary DML behavior.
Computed outputs can be read but not assigned; computed-only and zero-column
simple views can DELETE. View predicates guard UPDATE/DELETE and are rechecked
after row waits; INSERT and escaping UPDATE do not imply CHECK OPTION.
Writes use the real base's locks, constraints and atomic publication. Current
view-owner or original-invoker rights are checked at every execution.
Main-target SELECT follows logical read demand, not a view-only predicate;
independent stored subqueries retain SELECT rights under their defining layer.
Their frozen attribute layouts consume live selected rows, including own changes.
Native Dump/Restore retains definitions and committed base rows, with future
view writes remapped after Restore. INSERT RETURNING uses the logical view
projection and owner/invoker authority; explicit query rules now precede automatic
lowering, while procedural trigger writes remain excluded. View-write smoke:
`make check RUN='^TestReferenceViewUpdatableSmoke$'` (also with `REFERENCE=1`).

Change note: automatically updatable views support `WITH [LOCAL | CASCADED]
CHECK OPTION` (omitted mode means CASCADED), `WITH(check_option=local|cascaded)`
and ALTER VIEW SET/RESET. Checks evaluate finalized NEW base rows after base
constraints/uniqueness, rejecting FALSE or NULL with SQLSTATE44000 without
publishing a statement prefix. Nested LOCAL checks its own predicate plus lower
explicit checks; CASCADED also checks every lower predicate, including beneath a
no-WHERE layer. The innermost failing checked view is named; diagnostic row detail
uses the actual actor's base-column disclosure rights. Held writes observe
current modes/sources, and native Dump/Restore preserves modes and future writes.
INSTEAD query rules invalidate affected inherited checks; each fresh action
target still enforces its own checks. Procedural triggers remain an external
provider boundary, not native no-ops.
Check-option smoke: `make check RUN='^TestReferenceViewCheckFeatureSmoke$'`
(also with `REFERENCE=1`).

Change note: explicit temporary views and views whose bound query references a
temporary relation use the physical owner's existing TEMP namespace. Implicit
conversion emits `view "<name>" will be a temporary view`; expression-only
TEMP sequence/regclass dependencies do not cause PostgreSQL18 inference.
Same-name views in two sessions have independent definitions, shadow persistent
relations, and disappear at physical session end, not pooled return. Queries
remain dynamic with frozen logical result metadata; CREATE never evaluates rows.
Native Dump excludes TEMP. Reset/Restore retains exact TEMP-only view chains and
lawful unchanged pinned catalog providers, but atomically rejects invalidated
persistent providers rather than rebinding names. VIEW utilities remain excluded
inside explicit transactions. See [temporary views](https://www.postgresql.org/docs/18/sql-createview.html).
Feature smoke: `make check RUN='^TestReferenceTemporaryViewFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceTemporaryViewFeatureSmoke$'`.

Change note: scalar/EXISTS, IN/NOT IN, ANY/SOME/ALL and row comparison
subqueries now use the shared typed query pipeline, with legal lexical
correlation and non-lateral derived SELECT. Ordinary no-FROM SELECT uses a
local one-row input. Full child binding/admission remains independent of
runtime demand; eligible EXISTS may discard target-only output (see the linked
eligibility contract). Scalar cardinality,
NULL/empty reductions, coherent mutation consumers and stored-view dependencies
retain their PostgreSQL contracts. The selected `string_to_table`, JSON processing
and regexp families admit function FROM and function-only LATERAL; general lateral
derived queries and unrelated table functions remain excluded.
Nonrecursive SELECT CTEs use the same query pipeline.
Direct/prepared reference cases independently specify scalar NULL-left IN/NOT IN
on nonempty input and SOME empty/NULL reductions.
See [subqueries](https://www.postgresql.org/docs/18/queries.html).
Shared verification commands:
`make check RUN='^TestReferenceQuerySubquery'` and
`make check REFERENCE=1 RUN='^TestReferenceQuerySubquery'`.

Change note: registered `CREATE EXTENSION unaccent [IF NOT EXISTS]
[WITH SCHEMA schema] [VERSION '1.1']` installs the real stock unaccent dictionary,
template and four procedure identities atomically. `SELECT unaccent('Hôtel')`
returns TEXT `Hotel`; NULL remains typed TEXT NULL. The embedded PostgreSQL
18.0 stock rules preserve expansion, deletion, punctuation and non-Latin input;
no filesystem, shared library or generic package loader is involved.
Qualified calls and USAGE-filtered search paths resolve installed objects, not
an always-on builtin. Extension names are database-wide; repeat installation
errors42710, or emits the exact skipping NOTICE with IF NOT EXISTS.
Installation participates in transactions/savepoints and read-only admission.
Trusted ordinary installers need actual current-database CREATE, not CREATEDB
or caller schema CREATE; their extension is caller-owned and owned members are
bootstrap-owned. Narrow `GRANT/REVOKE CREATE ON DATABASE current_name` uses the
existing role/ACL authority; it does not add database creation or routing.
Dump emits the installation before stored CHECK/view consumers, with native
named-owner metadata preserving member owners even when destination role
attributes differ. Reset/Restore retains destination database identity/ACLs.
An installer cannot install into its own actual TEMP namespace: real template
helper lookup excludes that namespace and fails42883 atomically. An authorized
foreign installer can target a committed actual TEMP namespace; the installation
then follows that namespace's physical-session lifetime, not the installer's.
Qualified owner calls work; own TEMP is omitted from unqualified function lookup,
while authorized foreign installed-provider paths work without widening general
TEMP object visibility. Transactional DISCARD TEMP and physical close remove the
whole installation and dependent views/defaults/CHECKs, preserving surviving
tables/rows. Supported DROP SCHEMA uses the normal extension dependency and the
same closure. Native close retires its namespace; PostgreSQL retains an empty shell.
Committed TEMP installations make Dump fail before writing any bytes. Native
Reset/Restore preserves exact live destination TEMP installations and owned TEMP
consumers; a conflicting imported global extension name or foreign TEMP transfer
fails atomically rather than substituting session authority.
PostgreSQL ignores native ownership comments and uses ordinary installation
ownership on SQL replay. General FUNCTION/text-search DDL, internal SQL codecs,
other ALTER EXTENSION operations and arbitrary/native-ABI loaders remain
excluded; bounded version UPDATE and installed-extension DROP are supported.
See [registered extensions](https://www.postgresql.org/docs/18/sql.html)
and [selected proof](reference/catalogs/README.md#registered-extension-selected-proof).

Change note: `CREATE EXTENSION fuzzystrmatch [WITH SCHEMA schema]
[VERSION '1.1' | VERSION '1.2']` installs the released selected definition.
Version1.1 has nine functions: four Levenshtein overloads, three Metaphone
signatures and two Soundex signatures. `ALTER EXTENSION fuzzystrmatch UPDATE
[TO '1.2']` genuinely creates `daitch_mokotoff(text) RETURNS text[]` and advances
the installed version atomically, preserving the extension and old member
identities, ownership, ACLs and stored consumers. Version1.2 is the default;
explicit/default1.2 CREATE executes the same base1.1→1.2 transition privately.
Failure, cancellation or a competing publication leaves no partial update.
Updates require ownership and, for an ordinary trusted updater running a script,
actual database CREATE. New trusted-script members are bootstrap-owned; old
member owners remain unchanged. Same-version UPDATE emits a NOTICE without
requiring a new database CREATE grant. Dump records the installed VERSION,
not update history; both1.1 and1.2 restore genuine definitions before consumers.
This selected immutable, strict, parallel-safe provider preserves every six-digit
alternative in PostgreSQL's element order: John returns `{160000,460000}` and
Bierschbach returns all eight published codes. NULL, empty and unencodable-only
input yield SQL NULL; an encodable vowel-only name can yield `{000000}`.
The release chart has narrow Latin1 and specific Polish/Romanian letter mappings,
not general Unicode transliteration. Unsupported characters and punctuation are
skipped even inside phonemes; ß maps to one S. There is no 255-character limit.
Direct/prepared calls use installed search-path identity and `_TEXT` metadata.
Views, text[] defaults/CHECKs and STORED generated columns retain actual immutable
procedure dependencies; native controls remap providers and owned array elements
while already returned rows keep their old arrays/metadata. Availability reports
the real selected provider, not full upstream fuzzystrmatch member coverage.
New array operators and general extension loaders are not added.
This provider has no dictionary helpers, so unaccent's own-TEMP
helper-resolution restriction does not apply.
See [selected proof](reference/catalogs/README.md#daitch-mokotoff-selected-proof).

**Soundex — change note:** installed fuzzystrmatch1.2 exposes exactly
`soundex(text) RETURNS text` and `difference(text,text) RETURNS int4`, immutable,
strict and parallel safe. Ann and Anne are different spellings with the same
A500 code; their difference score is4, while Anne/Andrew is2. Difference counts
matching code positions, not edit distance. The C-locale byte algorithm preserves
original-byte adjacency: Ashcraft A226, BHB B100, B1B B000. Empty/no-ASCII-letter
inputs produce empty TEXT; two such inputs have difference4. There is no255-byte
input limit. PostgreSQL explicitly cautions that Soundex works poorly with
multibyte encodings, including UTF8: Émile is M400, not Emile's E540; no Unicode
normalization or transliteration is substituted. Direct/prepared and stored
expressions retain real installed procedure identities through native controls.
See [Soundex selected proof](reference/catalogs/README.md#soundex-selected-proof)
and the [PostgreSQL manual](https://www.postgresql.org/docs/18/fuzzystrmatch.html#FUZZYSTRMATCH-SOUNDEX).

**Levenshtein edit distance — change note:** installed fuzzystrmatch1.2 adds
`levenshtein(text,text)`, `levenshtein(text,text,int,int,int)`,
`levenshtein_less_equal(text,text,int)` and
`levenshtein_less_equal(text,text,int,int,int,int)`, each returning INT4.
Weights are insertion/deletion/substitution, defaulting to one; the final
bounded argument is the maximum distance. Kitten→sitting is3. Within a
nonnegative bound the ordinary nonnegative-cost result is exact; outside it
only a value greater than the bound is promised. Negative bounds compute the
unbounded distance. Comparisons use exact Unicode codepoints without case
folding or normalization. Two nonempty operands are limited to255 characters,
not bytes; an empty operand bypasses that restriction and the bound. Strict
NULL produces typed INT4 NULL after demanded siblings, not suppressed errors.
Installation, qualified/search-path resolution, stored expressions and native
control remapping retain genuine provider identities; DM remains available.
See [Levenshtein selected proof](reference/catalogs/README.md#levenshtein-selected-proof).

**Metaphone — change note:** installed fuzzystrmatch1.2 provides
`metaphone(text,integer)`, `dmetaphone(text)` and `dmetaphone_alt(text)`,
all immutable, strict, parallel-safe TEXT functions. Metaphone GUMBO with
cap4 returns `KM`; Double Metaphone gumbo returns `KMP` in both variants.
Smith has primary `SM0` and alternate `XMT`; Schmidt has `XMT` and `SMT`.
Metaphone's nonempty input and requested output are limited to255 **bytes**;
the output cap must be positive. Empty input returns empty even for an invalid
non-NULL cap; NULL propagates after demanded arguments. Double Metaphone has
no255-byte input limit and returns independently truncated, unpadded codes of
at most4 bytes. These release algorithms use ASCII/C-locale byte rules, not
Unicode transliteration; punctuation breaks phonemes and multibyte input can
give surprising results. Installed identity, privileges, stored consumers and
native restore use the same provider authority as DM/Levenshtein.
See [Metaphone selected proof](reference/catalogs/README.md#metaphone-selected-proof).


Change note: read-only `pg_catalog.pg_collation` exposes all twelve PostgreSQL18
identity fields from installed collation descriptors: names, namespace/owner,
provider, determinism, encoding, libc categories, provider locale, ICU rules and
recorded version. Its NAME and five TEXT attributes use catalog C collation950;
ordinary user TEXT retains its database-default collation. Unavailable or
inapplicable locale/version facts remain SQL NULL, distinct from a configured
empty recorded version. Native `unicode` recorded version is unavailable, not
copied from the PostgreSQL fixture's ICU library. PUBLIC reads include another
owner's off-path identities; catalog observation does not grant provider execution
or make those identities visible to name lookup. Held executions see fresh DDL
metadata, while already returned native rows retain their captured values.
Fixed release OIDs are exact; generated namespace/owner/collation identities are
relational, not allocator-parity promises. This does not import system collations
or add ICU/arbitrary-libc comparison semantics.
See [pg_collation selected proof](reference/catalogs/README.md#pg_collation-selected-proof).

Change note: search-path visibility inquiries `pg_table_is_visible(oid)`,
`pg_type_is_visible(oid)`, `pg_collation_is_visible(oid)` and
`pg_conversion_is_visible(oid)` return strict nullable BOOL. Missing identities
return NULL; existing off-path or shadowed identities return false. Relation
visibility includes supported tables, views, sequences, ordinary B-tree/hash indexes
and catalog relations. Index identities share the relation namespace, so an
earlier same-named table, view or sequence can shadow an index and vice versa.
The subject's SELECT privilege is irrelevant; searchable schema USAGE still
filters the path. Held prepared executions see fresh catalog/path state, while
already returned results remain immutable.
Argument analysis precedes the 100-argument guard, then namespace/signature
lookup and OID inference/coercion. Missing child columns therefore take precedence
over missing explicit schemas, including in over-limit direct/prepared calls.
Native inquiries cover all25 seeded types, seven fixed PostgreSQL18 collations
and all128 seeded conversions. Comparison supports default/C/POSIX plus
`pg_c_utf8`/`pg_unicode_fast` and independently owned user collations backed by
the same registered libc/builtin semantics. Raw `ucs_basic`/`unicode` seeds remain
inquiry-only. CREATE/ALTER/DROP COLLATION,
read-only pg_collation and pg_collation_actual_version expose truthful metadata.
Dump emits legal pinned-seed metadata deltas and lossless user definitions;
Reset restores canonical seed metadata and genuine bootstrap ownership. Restore
rebuilds actual owners/capabilities, including independent Driver imports.
Explicit pg_temp collation creation follows physical session lifetime; durable
consumers cascade atomically at physical owner end, not pool return. Temporary
definitions are omitted from portable dumps; durable temporary-provider references
fail before output. Controls preserve live target TEMP, including imported OID
collisions; SET SCHEMA into/out of TEMP remains0A000.
Inquiry catalog/path capture is demand-driven, including nested views,
RETURNING and transformed ALTER CHECK validation; unrelated statements do not
retain a visibility snapshot. Stored inquiry CHECKs support compact/pretty
pg_get_constraintdef/pg_get_expr output and behavioral replay.
Selected Boolean-valued calls, including all four visibility inquiries, are
shared expression leaves: bare WHERE predicates and grouped NOT/AND/OR in WHERE
or SELECT need no view workaround or `= TRUE`. Nonboolean call results receive
construct-specific type errors; set-returning calls remain forbidden in Boolean
contexts. This does not expand scalar arithmetic or the bounded call-argument
language. The unchanged original psql relation-list query retains both catalog
LEFT JOINs, CASE/IN/regex and ordinal ordering and is supported through embedded
direct/prepared execution and the rebuilt simple-query server with actual psql.
The prerequisite simple-CASE internal `"char"` equality-resolution defect is
resolved by its owning conditional implementation, not by rewriting the query.
Wire prepared statements remain excluded. See the owning
[catalog](reference/catalogs/README.md#boolean-call-predicates) and
[server](reference/sessions/README.md#boolean-call-relation-list-smoke) Testing
sections for the required selected native/current-oracle proof.
Independent identity witnesses use qualified regclass/regtype/regcollation casts
and pg_get_object_address for conversions, never visibility-under-test or fixed
allocator values. INSERT results are checked by a subsequent SELECT, while
UPDATE/DELETE support RETURNING.
CREATE TYPE forms other than AS ENUM or AS (...), CREATE CONVERSION and foreign
table creation remain excluded; permanent materialized views have a
[bounded supported surface](https://www.postgresql.org/docs/18/sql-creatematerializedview.html), without
temporary materializations/sources. Concurrent refresh retains the documented
population and eligible-index restrictions.
See [visibility inquiries](https://www.postgresql.org/docs/18/functions-info.html).
Feature smoke: `make check RUN='^TestReferenceCollationLegacyVisibility$'`.
Index identity/path/control witnesses:
`make check RUN='^TestVisibilityInquiryIndexRelations$'`;
`make check REFERENCE=1 RUN='^TestReferenceVisibilityInquiryIndexRelations$'`.

Object ACL inquiry `pg_get_acl(oid,oid,integer) -> aclitem[]` supports raw object
and `pg_class` column addresses, named arguments and typed NULLs. See the
[object ACL contract](reference/catalogs/README.md#object-acl-inquiry-contract).

Object comment inquiries support `col_description(oid,integer)`,
`obj_description(oid,name)`, legacy `obj_description(oid)` and
`shobj_description(oid,name)`, all returning strict nullable TEXT. Local
relation/column/schema/type descriptions and shared role descriptions stay
separate. Legacy collision lookup chooses an unspecified matching local comment
consistently within a statement, with no portable catalog precedence. Matching
fixed signatures accept VARIADIC decoration; ordinary type/NULL analysis remains.
Each execution captures immutable comment metadata; held prepared statements
refresh on their next execution. Views preserve strict constant-NULL inquiry
syntax without runtime sibling demand. Native builtin namespace/type descriptions
and explicit namespace removals round-trip through native controls.
See [semantics and boundaries](https://www.postgresql.org/docs/18/functions-info.html).
Feature stream: `make check RUN='^TestReferenceCommentInquiry$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceCommentInquiry$'`.

**Catalog change note:** `pg_catalog.pg_description` is a readonly normal query
source with exactly `objoid OID`, `classoid OID`, `objsubid INT4` and
`description TEXT` (all NOT NULL; no row `oid`). Its key is object/catalog/stable
attribute identity, not a name or a dense column position. Supported table,
column, schema and sequence comments follow replacement and drop automatically;
shared role descriptions remain excluded. Exact PostgreSQL18.0 seeds cover the
represented26 scalar types (including JSON114 and PG_NODE_TREE194),7 collations,
128 conversions and2 bootstrap namespaces. This does not expand COMMENT targets
or the narrower inquiry catalog routing. Native Reset reseeds163 descriptions;
logical Restore can remint surviving column numbers after a dropped column.
See [catalog contract](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGDescriptionFeatureSmoke$'`,
and the same command with `REFERENCE=1` for pinned PostgreSQL.
**Shared-comment catalog change note:** `pg_catalog.pg_shdescription` now reads
existing shared ROLE comments through the normal direct/prepared/view pipeline.
Its three NOT NULL columns are `objoid OID`, `classoid OID`, `description TEXT`;
relation2396 has no row `oid` or subobject field. Actual principal identities
use class1260 and remain separate from local `pg_description`. Replacement,
empty/NULL removal, drop and same-name recreation follow existing role effects.
No bootstrap descriptions, DATABASE/TABLESPACE commands or new role catalog
are invented; uint64 principals outside uint32 range are never truncated.
Shared comments survive native Reset/Restore and stay outside local Dump.
See [shared catalog contract](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGShDescriptionFeatureSmoke$'`,
and the same command with `REFERENCE=1` for pinned PostgreSQL18.0.
**Dependency catalog change note:** `pg_catalog.pg_depend` is a readonly normal
query source (relation2608) with seven NOT NULL object/subobject/dependency-type
columns and no row `oid`. It derives exact `n`, `a` and `i` edges from represented
owner definitions, including real domain arrays and view rewrite rules; builtin
pinned references, automatic rowtypes and enum arrays are not fabricated.
Scalar REGCLASS constants capture existing relation identities, including dead
CASE branches; runtime integer-to-REGCLASS casts and array payloads do not invent
relation edges. Existing feature owners govern rename/drop and native replay.
See [dependency contract](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGDependFeatureSmoke$'`,
and the same command with `REFERENCE=1` for pinned PostgreSQL18.0.


**Sequence catalog change note:** `pg_catalog.pg_sequence` exposes the exact
eight readonly definition fields, known NOT NULL native metadata and logical
sequence identity. `seqtypid` is SMALLINT/INTEGER/BIGINT identity; numeric options
always have INT8 output metadata. START/options are distinct from current value,
RESTART and cache reservations. PUBLIC catalog reads include private/peer TEMP
definitions without generator access. RC held prepared scans refresh; RR uses
the existing transaction snapshot plus own DDL overlay, and old cursors remain
stable through DDL/native controls. The separate `pg_sequences` view adds current
readable high-water state with different TEMP and lock semantics.
See [catalog contract](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGSequenceFeatureSmoke$'`,
and the same command with `REFERENCE=1`.

**Cast catalog change note:** `pg_catalog.pg_cast` is readonly relation2605 with
six known NOT NULL fields: four OIDs (`oid`, `castsource`, `casttarget`, `castfunc`)
and two internal CHARs (`castcontext`, `castmethod`). The coercion implementation
owner publishes exactly69 complete-context numeric/MONEY/OID/temporal/
character/BOOL/typmod/JSON edges with exact fixed type/function identities and
stable local generated row IDs. This is not a complete conversion inventory:
automatic string I/O, domain/base conversions and ordinary identities have no
rows, and partial NAME/REGCLASS/BIT/internal-CHAR providers remain omitted.
No new cast execution or CREATE/DROP CAST is added. Native Dump omits virtual
rows; Reset/Restore reconstruct them while retained results remain usable.
See [bounded owner metadata and omissions](docs/design/PG18-CAT-PG-CAST.md).
Feature smoke: `make check RUN='^TestReferencePGCastFeatureSmoke$'`,
and the same command with `REFERENCE=1` for pinned PostgreSQL18.0.

**Installed-extension catalog change note:** `pg_catalog.pg_extension` is readonly
relation3079 with eight exact OID/NAME/BOOL/TEXT/OID[]/TEXT[] fields, known native
nullability and C950 collations on collatable columns. Fresh native installed
state is empty; actual registered `unaccent`, selected-type `intarray`,
selected-function `fuzzystrmatch` and bounded `pgcrypto` installations supply descriptor-derived
rows. No PL/pgSQL installation is fabricated. PostgreSQL18.0's default database
independently has installed `plpgsql`1.0; available control files are not installed rows.
Normal REGCLASS/search_path/prepared queries and native Dump/Reset/Restore use
the existing relation authority. Fixed relation3079 differs from the reused
allocated native rowtype10074/array10073; no cross-server type allocator promise,
physical system index or TOAST implementation is implied.
Registered provider CREATE is supported; unavailable providers fail0A000 at
execution, including after successful Prepare. ALTER EXTENSION and module
loading retain their unsupported boundaries.

**Extension removal change note:** `DROP EXTENSION [IF EXISTS] name [, ...]`
supports default/explicit RESTRICT and CASCADE. Extension ownership (including
inherited ownership or superuser authority), not installation-schema or member
ownership, authorizes removal. Authentic provider members and automatic aliases
retire together. External stored consumers block RESTRICT with2BP01; CASCADE
removes only their dependency closure, pruning defaults/checks or typed columns
while preserving surviving tables, data, schemas and unrelated providers.
Lists are atomic; missing IF EXISTS targets emit notices. Rollback/savepoint
rollback retain original member identities and callable bindings, not reinstalled
same-name replacements. Dump after committed removal contains only genuine
survivors; Reset/Restore remains replacement and does not resurrect removed
providers. This applies to the existing bounded unaccent, intarray, intagg,
fuzzystrmatch, pgcrypto and uuid-ossp implementations, not arbitrary module loading.
See [selected removal proof and smoke](reference/catalogs/README.md#extension-removal).
See [exact shape, installed authority and errors](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGExtensionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGExtensionFeatureSmoke$'`.

**Available-extension metadata change note:** `pg_catalog.pg_available_extensions`
and `pg_catalog.pg_available_extension_versions` are readonly PUBLIC SELECT views
over the same immutable embedded registry used by installation. The native
universe has nine providers: citext1.8, cube1.5, unaccent1.1, selected-type intarray1.5,
selected-function fuzzystrmatch1.1/1.2, intagg1.1, pgcrypto1.4, pgrowlocks1.2 and uuid-ossp1.1,
with released control comments and superuser/trusted/relocatable flags. The
comments do not promise complete upstream member inventories. hstore, file_fdw
and plpgsql are not invented. The default view has nine rows; the versions view
has ten. Default, available and actual installed versions are distinct.
Control schema/dependencies remain NULL for all nine providers even when
installed elsewhere.
Metadata is not filtered by member namespace privileges or TEMP ownership;
ordinary relation admission and SQL predicates still apply.
Both availability views and `pg_extension` share one caller-visible installed-state
frontier per statement, including joins and recursive inputs; peer publication
affects the next execution, not later occurrences in the same statement.
See [fields, visibility and controls](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferenceAvailableExtensionsFeatureSmoke$'`;
add `REFERENCE=1` for the pinned counterpart.

`pg_catalog.pg_extension_update_paths(name)` is a STABLE, STRICT record SRF
with TEXT source/target/path columns. The fuzzy graph has1.0→1.1→1.2 and all
six ordered distinct pairs; unreachable paths are NULL. Graph-only1.0 records
the shipped parallel-safety script but has no base installation and cannot
be installed/restored. Only1.1→1.2 is executable from an admitted fuzzy
manifest. Other native providers explicitly expose only their current base
and no edges; this is an embedded inventory, not an OS package scan. In
particular real PostgreSQL has additional historical unaccent graph metadata.
There is no empty-action fallback, arbitrary SQL-script interpreter, old
provider-file provisioning or shared-library loading.
Selected proof: [extension versions](reference/catalogs/README.md#extension-version-selected-proof).


**Pgcrypto change note:** installed pgcrypto1.4 supplies fourteen PARALLEL SAFE
scalar overloads: immutable digest(TEXT,TEXT), digest(BYTEA,TEXT),
hmac(TEXT,TEXT,TEXT), hmac(BYTEA,BYTEA,TEXT) return raw BYTEA; immutable
crypt(TEXT,TEXT) and volatile gen_salt(TEXT[,INT4]) return TEXT.
Immutable encrypt/decrypt(BYTEA,BYTEA,TEXT) and
encrypt_iv/decrypt_iv(BYTEA,BYTEA,BYTEA,TEXT) return BYTEA using raw AES CBC/ECB
or Blowfish CBC/ECB/CFB64. CBC is the default; CBC/ECB use PKCS padding by default
or explicit `pad:none`. CFB64 accepts arbitrary lengths and valid padding choices
are no-ops. Unknown data/key/IV operands infer BYTEA; known TEXT needs explicit
casts. AES keys right-zero-pad to16/24/32 bytes and clip beyond32, with16-byte IVs.
BF cycles unsigned raw key octets including NUL, clips beyond56 bytes and treats
an empty key like one zero octet; its IV is8 bytes. IVs right-zero-pad or clip,
default to zero and are ignored by ECB (but NULL IV remains strict). CBC/ECB add
a complete PKCS block to aligned/empty plaintext; nopad requires alignment.
Malformed padding/length raises operation-specific39000. Invalid cipher/options
raise22023; AES-CFB, DES/3DES and CAST5 raw modes remain native0A000 boundaries.
BF and bcrypt share one standard-library-only Blowfish primitive, not AES or an
external crypto provider; each invocation owns its schedule and feedback state.
Raw encryption supplies no integrity protection; see
[PostgreSQL raw encryption functions](https://www.postgresql.org/docs/18/pgcrypto.html#PGCRYPTO-RAW-ENC-FUNCS).
Volatile gen_random_bytes(INT4), gen_random_uuid() and fips_mode() return BYTEA,
UUID and BOOL respectively. All selected members are STRICT except the
zero-argument installed UUID wrapper, matching its PostgreSQL declaration.
Supported digest/HMAC algorithms are md5, sha1, sha224, sha256, sha384 and sha512,
including observed hyphenated SHA aliases and SHA2-256. TEXT hashes exact UTF8
octets; BYTEA hashes arbitrary raw bytes. Builtin md5 still returns hexadecimal
TEXT. Any NULL argument skips hashing after demanded child expressions; a
constant failing sibling is not suppressed. Unsupported digest/HMAC algorithms report22023
with the normalized name (ASCII case-folded, UTF8-safe63-byte truncation, no trim).
Standard-library streaming bounds scratch memory, including oversized HMAC keys.
Installed namespace/USAGE, exact overload identities, defaults/CHECK/generated
columns/index/view consumers and native Dump/Reset/Restore use the existing
catalog/control paths. Old native Rows retain their bytes and metadata; failed
replay is atomic and named owners must exist in the destination. Selected pgcrypto
validation rejects mixed crypto/fuzzy provider identities rather than allowing a
different provider's dispatch branch to admit a crypto member.
Password hashing supports DES, extended DES (xdes), MD5-crypt, bcrypt and
SHA256/SHA512-crypt using only Go's standard library and fixed per-call state.
DES uses the first eight
password bytes (discarding their high bits); xdes folds all password bytes.
MD5-crypt uses at most eight salt bytes, stopping at `$`, including empty salts.
Stored hashes may be reused as settings; trailing checksum/suffix bytes are ignored.
Exact `$2a$`/`$2x$` heads retain PostgreSQL bcrypt, costs04..31 and the first72
password bytes, even within UTF8. `$2b$`/`$2y$` use DES fallback, not new bcrypt
variants. `$2$` retains39000. Exact `$5$`/`$6$` heads select SHA-crypt, using all
password UTF8 bytes and up to16 raw salt bytes. Missing rounds means5000 and
omits the option in output; explicit rounds are normalized and retained.
Direct crypt clamps outside1000..999999999 with22003 NOTICE, before later salt
validation; generators reject out-of-range counts with22023. PostgreSQL's raw
versus decoded salt and signed rounds parsing are preserved.
gen_salt recognizes case-insensitive names without trimming:
des defaults to25 and accepts only25; md5 defaults to1000 and accepts only1000;
xdes defaults to725 and accepts odd1..16777215; bf defaults to6 and accepts4..31.
sha256crypt/sha512crypt default to5000 and accept1000..999999999.
Omitted/zero counts select defaults. DES/MD5/xdes/bcrypt entropy requests are
2/6/3/16 bytes, independent of SQL random; their salt lengths are2/11/9/29.
MD5 salts have no trailing `$`. Validation/strict NULL handling precedes entropy.
SHA salts consume16 entropy bytes and always print `rounds=N$` plus16 salt
characters, with no trailing `$` (31 characters at5000,36 at999999999).
Expensive hashing polls cancellation without imposing a lower iteration ceiling.
Volatile salts are valid defaults/views, not generated columns or expression
indexes. Stored salts/hashes and installed identities survive native replay.
gen_random_bytes accepts1..1024 bytes, validates bounds before allocation/entropy,
and returns typed BYTEA NULL for a NULL count. Invalid counts report39000
`Length not in range`; unknown inputs use ordinary INT4 coercion and diagnostics.
The installed UUID wrapper reuses core V4 generation and retains its own procedure
dependency. Core pg_catalog.gen_random_uuid remains available without the extension.
Implicit pg_catalog lookup wins by default; explicit
`search_path=pgcrypto_part,pg_catalog` selects an installed wrapper in pgcrypto_part.
fips_mode reports actual active Go crypto mode via crypto/fips140.Enabled(), not
algorithm availability or a deployment certification claim. No OpenSSL installation
is required; PostgreSQL reports its own OpenSSL mode, which can legitimately differ.
All three new members are volatile defaults/view producers, never immutable
generated-column/index expressions. Stored random bytes/UUIDs and executable
installed recipes retain their values and identities through native replay.
Arbitrary OpenSSL algorithms, PGP, provider configuration and
shared-library loading remain excluded. Random/FIPS smoke:
`make check RUN='^TestReferencePgcryptoRandomFeatureSmoke$'`;
add `REFERENCE=1` for pinned PostgreSQL. Run the native actual-mode witness in
separate processes with `env GODEBUG=fips140=off make check RUN='^TestReferencePgcryptoRandomFIPSMode$'`
and `env GODEBUG=fips140=on make check RUN='^TestReferencePgcryptoRandomFIPSMode$'`.
Raw AES smoke: `make check RUN='^TestReferencePgcryptoAESFeatureSmoke$'`;
raw BF smoke: `make check RUN='^TestReferencePgcryptoBFFeatureSmoke$'`.
Add `REFERENCE=1` for pinned PostgreSQL (catalog oracle activates both default
and legacy OpenSSL providers, test-only). Password smoke:
`make check RUN='^TestReferencePgcryptoPasswordFeatureSmoke$'`;
add `REFERENCE=1` for the pinned PostgreSQL counterpart. Digest/HMAC smoke remains
`make check RUN='^TestReferencePgcryptoFeatureSmoke$'`.


LIKE/NOT LIKE and ILIKE/NOT ILIKE support whole-string `%`/`_` matching,
keyword ESCAPE and symbolic `~~`/`!~~`/`~~*`/`!~~*` operators. TEXT wildcards
advance UTF8 code points; existing BYTEA LIKE advances octets. BPCHAR left
operands retain physical padding. Supported default/C/POSIX ILIKE folds ASCII
only; C and POSIX remain distinct collation identities. Scalar COLLATE also
supports `pg_c_utf8`/`pg_unicode_fast` and user collations registered with the
same truthful provider capabilities, with case-sensitive LIKE but a positioned
0A000 ILIKE boundary for Unicode providers. Explicit C on either operand
overrides implicit Unicode collation and permits ASCII ILIKE. Unavailable
libc/ICU locales remain excluded; POSIX matching is documented below.
Collatable wrappers retain identity and demanded conflicts; indeterminate TEXT
view outputs are rejected before publication, while BOOL matching views replay.
See [matching semantics](https://www.postgresql.org/docs/18/functions-matching.html).
Feature smoke: `make check RUN='^TestReferenceLikeLegacyMigration$'`.

`SIMILAR TO` and `NOT SIMILAR TO` independently implement full-string SQL
regular expressions: `%`/`_`, alternation, groups, quantifiers, brackets,
registered provider collation classes, escapes and separator capture/backreferences.
SQL punctuation `.`, `^`, `$` is literal outside brackets. Wildcards advance
UTF8 code points and include newlines. BPCHAR predicate sources retain padding.
`substring(s SIMILAR p ESCAPE e)`, `substring(s FROM p FOR e)` and the
three-TEXT ordinary/qualified `substring(s,p,e)` return the separator capture,
the entire matching source without separators, or TEXT NULL for no match/NULL.
Extraction's inner matcher uses database-default C collation; predicate classes
honor the selected builtin identity. Positional TEXT substring uses the separate
INT4 character-slicing overload; this does not expand the regex grammar.
Hostile patterns can consume substantial time and memory. Set a context deadline;
native execution polls cancellation and checks a 64 MiB IR/scratch/state ceiling,
not a PostgreSQL-identical complexity threshold or universal linear-time promise.
See [SQL regex details](https://www.postgresql.org/docs/18/functions-matching.html).
Feature smoke: `make check RUN='^TestReferenceSimilarFeatureSmoke$'`;
add `REFERENCE=1` to exercise the same assertions against pinned PostgreSQL18.0.
POSIX matching: `~`, `~*`, `!~`, `!~*` search anywhere, with default ARE
syntax and case-sensitive/insensitive matching before negation. Embedded
options/directors select ARE/ERE/BRE/quoted syntax, newline and case behavior.
Two-TEXT `substring(source,pattern)` (also `substring(source FROM pattern)` and
`pg_catalog.substring`) returns the first numbered capture, otherwise the whole
first match; absent captures/no match return NULL and participating empty captures
return empty TEXT. It uses effective argument collation, unlike the three-TEXT
SQL wrapper. Both frontends share the checked execution-local PostgreSQL regex
provider, including multiple captures, backreferences and variable-width
lookaround. Use `statement_timeout` or a caller deadline for hostile patterns.
Nine public PostgreSQL18 regular-expression functions are supported:
`regexp_count`, `regexp_instr`, `regexp_substr`, `regexp_like`, `regexp_match`,
`regexp_matches`, `regexp_replace`, `regexp_split_to_array` and
`regexp_split_to_table`. All released overloads share the same provider;
array-valued captures are owned `TEXT[]`
values, and start/index arguments count UTF8 characters without slicing away
anchor/lookbehind context. The two set functions compose with `string_to_table`
through one shared target/FROM owner, including mixed lockstep peers, nested
expansion, `ROWS FROM`, `WITH ORDINALITY` and implicit/explicit LATERAL.
Bare scalar attempt timing, typed padding, lexical frames and lock rechecks
retain their distinct contracts. See [function details](https://www.postgresql.org/docs/18/sql.html).
SQL XQuery regex functions and general LATERAL derived/VALUES sources remain excluded.
Existing non-regexp array ordering/opclass and constrained-domain DDL limits
remain explicit; array-valued regex results retain table/default/generated/
CHECK, grouping, scalar-key index-predicate and unordered-query consumers.
Function-suite smoke: `make check RUN='^TestReferenceRegexpFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.
`regexp_substr(string,pattern[,start[,N[,flags[,subexpr]]]])` has five strict
TEXT-returning overloads. Omitted start/N default to1, flags to empty and subexpr
to0; supplied NULL returns NULL rather than applying a default. N selects
nonoverlapping whole matches; start counts UTF8 characters in the original
source. Unlike two-TEXT substring, the default returns the whole match, not its
first capture. Positive subexpr selects a capture; unmatched/out-of-range groups
and no match return NULL, while a participating empty group returns empty TEXT.
PostgreSQL18.0's no-group fallback is preserved: subexpr1 returns the whole match,
subexpr2 or higher returns NULL. The global flag `g` is rejected; N supplies
occurrence selection. Stored expressions and native replay share scalar binding.
See [selected proof and standalone smoke](reference/patterns_encoding/README.md#regexp_substr-selection-and-lifecycle).
Feature smoke: `make check RUN='^TestReferencePosixFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.


Standalone `VALUES (1,'a'),(1,'a'),(2,NULL)` and parenthesized VALUES in FROM are
supported queries. Optional relation/column aliases expose computed `columnN`
outputs with independent common typing (unknown-only becomes TEXT). Duplicate
bags, local ordering/counts, inner/cross joins, INSERT SELECT and stored views
share existing binding, execution, metadata and native replay paths. VALUES
cells are non-lateral and reject DEFAULT and aggregates; lateral VALUES remains
excluded. The selected string/regexp function sources admit function-only
LATERAL. Derived SELECT uses the same source pipeline. Ordinary INSERT VALUES retains destination-context
typing. See [VALUES contracts and change note](https://www.postgresql.org/docs/18/queries.html).
Independent reference: `make check REFERENCE=1 RUN='^TestReferenceQueryFromValues'`.

SELECT, shared INSERT SELECT and decorated VALUES support expression ORDER BY,
output labels/ordinals, multiple keys and per-key ASC/DESC/NULLS FIRST/LAST.
Referenced output values are evaluated once before sort and reused. With a
count decoration, cheap immutable outputs run below Sort; unrelated volatile
outputs run after Sort but before OFFSET/LIMIT. Unordered queries and complete
sort peers have no portable relative order. General COLLATE remains excluded.
See [ordering semantics](https://www.postgresql.org/docs/18/queries.html).
Public feature smoke: `make check RUN='^TestReferenceQueryOrder'`.

Change note: ordinary SELECT DISTINCT now eliminates duplicate complete projected
tuples, with NULL-equal type-owned keys and no implicit ordering promise. Recursive
arrays/domains retain structural equality; supported tree carriers retain their
binary-compatible structural text equality. Projection is evaluated once before
deduplication; ORDER must reference projected outputs, and locking DISTINCT is
rejected. Views/native replay and INSERT SELECT share source-typed deduplication
before destination assignment. Ordinary array operators/order and new FROM forms
remain excluded.
See [distinct projected rows](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQueryDistinctSmoke$$'`.

Change note: `SELECT DISTINCT ON (keys)` selects the first ordered input occurrence
per typed, NULL-equal key tuple. Keys may be hidden; payloads need not have equality.
Resolved ON keys match the initial effective ORDER targets in any permutation;
proper partial prefixes and repeated targets are supported. Explicit total ordering
selects deterministic winners; incomplete peers and unordered winners are not pinned.
Views, held queries, INSERT SELECT and native Dump/Restore retain ON semantics.
See [distinct on groups](https://www.postgresql.org/docs/18/queries.html).
Feature smoke: `make check RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'`;
run the same selector with `REFERENCE=1` for the pinned PostgreSQL contract.

SELECT, shared INSERT SELECT and decorated VALUES support LIMIT/OFFSET and
FETCH FIRST/NEXT ROW/ROWS ONLY or WITH TIES. WITH TIES requires local ORDER BY,
retains complete-key peers (including equal NULLs), and forbids SKIP LOCKED.
Finite ties consumes the first nonpeer lookahead; its effects/locks can occur
without returning that row. Counts use BIGINT assignment conversion; zero
suppresses producing children without bypassing typing, authorization or bound
errors. Nonblocking views/filters/joins produce only demanded occurrences;
sorting/grouping remain blockers. Local rowmarks settle at effect frontiers,
not across a local count boundary. See [count and demand](https://www.postgresql.org/docs/18/queries.html).
Feature checks: `make check RUN='^TestReferenceQueryTies(FeatureSmoke|PersistenceSmoke)$'`
and `make check RUN='^TestReferenceQueryLimitMigrated(Smoke|LockSmoke)$'`.
Run these same selectors with `REFERENCE=1` for the pinned PostgreSQL oracle.

Change note: SELECT (including existing joins, persistent scalar views and INSERT
SELECT sources) accepts repeated adjacent `FOR KEY SHARE`, `FOR SHARE`,
`FOR NO KEY UPDATE` and `FOR UPDATE` clauses, each with optional `OF` targets and
`NOWAIT` or `SKIP LOCKED`. The complete list goes before or after bounds, after
ORDER BY. OF selects relation occurrences/aliases, not projected columns;
unselected siblings retain ordinary reads and need no extra UPDATE privilege.
Strength and wait policy merge independently, with UPDATE and NOWAIT strongest.
NOWAIT reports row-lock-not-available55P03; SKIP drops an unavailable whole join
candidate and counts only survivors toward OFFSET/LIMIT. Already acquired
contributors remain held. These policies do not bypass ordinary table-lock waits.
Stored clauses retain targets/policies through rename, Dump/Restore and replay.
Plain view marks remain deferred to enclosing qualification; a bounded view
settles inherited marks before its own survivor count. Nullable-side selected
marks reject on Execute even at LIMIT0; preserved-side/local stored marks remain
legal. Synthetic FROM VALUES has no physical grants. LIMIT0 locks none, and
unmarked SELECT remains a nonblocking MVCC read.
READ COMMITTED lockers recheck
the current version after contention; REPEATABLE READ KEY SHARE retains snapshot
values only if no UNIQUE key datum changed since its snapshot. Restoring a key or
later changing only payload does not erase a committed key transition. Stronger
modes and deletions fail. UPDATE uses NO KEY UPDATE unless a UNIQUE key datum
changes, when it uses UPDATE; DELETE uses UPDATE. See
[row locking](docs/TRANSACTIONS.md#relation-and-row-resource-manager) for the compatibility
matrix, privileges, eager execution and bounded syntax.
Feature smoke: `make check RUN='^TestReferenceQueryLockFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryLockFeatureSmoke$'`.
Complete locking/migrated-count coverage uses `RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)'` in both modes.

Selected `ALTER TABLE` nullability and named NOT NULL operations compose atomic
action lists with `DROP COLUMN`, including explicit transactions/savepoints.
Validation includes fresh grant-time committed NULLs and own writes; SET/DROP
alone does not copy stored values. Private metadata, row projections and DML
publish together at Commit; Dump excludes private changes.
Feature smokes: `make check RUN='^TestAlterNullability(Smoke|ControlsSmoke)$'`.

Identity columns support `GENERATED ALWAYS` and `GENERATED BY DEFAULT AS IDENTITY`
on SMALLINT, INTEGER and BIGINT, using an internally owned sequence rather than a
stored DEFAULT. INSERT omission/DEFAULT generates; SYSTEM and USER overrides,
identity ALTER lists, transactions/savepoints and native Dump/Restore reuse the
existing sequence/session authorities. Identity is NOT NULL, not inherently unique.
Existing CHECK calls can use the same declaration's identity sequence without
definition-time allocation. Dump orders identity-sequence view/CHECK dependents
after their owning table, including retained TEMP collision remaps.
Stored expression-generated columns support `GENERATED ALWAYS AS (expression) STORED`
through the normal typed INSERT/UPDATE path. Omission and explicit DEFAULT compute
the stored value; other explicit generated-column writes fail428C9. See
[generated columns](https://www.postgresql.org/docs/18/ddl-generated-columns.html). PostgreSQL18 VIRTUAL
and omitted-kind generation compute lazily when read, using the same typed binder.
Virtual definitions admit built-in types/providers only, preserve independent
column SELECT grants and are evaluated on writes only when constraints demand them.
Change note: `ALTER TABLE ... ALTER [COLUMN] b SET EXPRESSION AS (expression)`
replaces the existing recipe without changing column kind, shape or declared type.
STORED values are recomputed atomically for effective live rows, including the
transaction's own writes; virtual replacement stays lazy except for NOT NULL.
`DROP EXPRESSION [IF EXISTS]` makes a STORED destination ordinary and retains its
cells. VIRTUAL DROP and PostgreSQL18.0 virtual SET on tables with CHECK remain
explicit boundaries. Prepared statements and preexisting virtual views use the
queried logical recipe captured for their admitted source, including inputs added after
view creation; already returned rows retain their original cells and metadata.
Native ordinary UPDATE DEFAULT and unique expression indexes retain their existing
capability boundaries. ADD generated and generated-column
or generation-dependency TYPE changes remain excluded.
Feature proof: `make check RUN='^TestReferenceAlterGeneratedExpression'`,
also with `REFERENCE=1`; see [bounded proofs](docs/TESTING.md).
Feature smokes: `make check RUN='^TestIdentity(Smoke|ControlsSmoke|CheckControlsSmoke)$'`.
The ALTER lifecycle is shared reference coverage:
`make check RUN='^TestReferenceAlterIdentity'`;
`make check REFERENCE=1 RUN='^TestReferenceAlterIdentity'`.

Change note: CREATE TABLE LIKE accepts ordered INCLUDING/EXCLUDING options,
including ALL and last-option precedence. Existing defaults, CHECKs, STORED/VIRTUAL
generation, independent identity sequences, freshly named btree/hash/unique/
expression/partial indexes and column comments copy through their real owning providers.
Plain LIKE still copies only shape and NOT NULL. Existing view and composite-type
sources are admitted; composites require type USAGE rather than backing-relation
SELECT. Copied expressions retain real external dependencies while row references
target the clone. Source data, owner, ACL and table comment never copy.
Partial B-tree and partial UNIQUE copies remap both cooked predicate trees,
retain analyzed external dependencies, and start with fresh empty membership
roots. TRUE alone qualifies, before derived-key evaluation; partial HASH and
partial concurrent creation retain their existing native exclusions.
Same-session TEMP sources and copies compose with these providers. Cooked TEMP
sequence defaults retain their original authority through native controls; a
permanent destination preserves native's precise `0A000` TEMP-default boundary,
unlike PostgreSQL. Constant/permanent-sequence defaults remain copyable. Fresh
identity sequences follow destination persistence, never source state or token.
Copies made after native replacement remap retained TEMP local row/tableoid
references to current destination authority; external literals and DEFAULT
manifests remain unchanged under their existing contextual authority.
Compression/storage/extended-statistics and other absent providers remain absent;
an option with no applicable descriptor has no effect. See
[table definition copies](https://www.postgresql.org/docs/18/sql-createtable.html).
Shared family: `RUN='^TestReference(Table(Like|Core)|SchemaTableIntegration|IdentitySerialAndNativeBoundaries|NotNullExternalInheritance)'` in both modes.
Feature smoke: `make check RUN='^TestReferenceTableLikeFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceTableLikeFeatureSmoke$'`.


SMALLSERIAL/SERIAL/BIGSERIAL (and serial2/4/8) expand CREATE columns to
SMALLINT/INTEGER/BIGINT NOT NULL, ordinary nextval defaults and AUTO-owned
sequences. Supplied values do not advance the sequence or imply uniqueness;
explicit NULL fails NOT NULL. Omission/DEFAULT consumes once per demanded row,
requires sequence privileges and can leave gaps after failed rows/rollback.
Owning column/table drops remove sequences; direct sequence RESTRICT protects
the default, while CASCADE removes only that default. Native Dump/Restore retains
integer columns, actual sequence names/options/state and bound references.
The shared DEFAULT surface admits only fixed early-bound nextval leaves and
assignment/cast wrappers, not general volatile expressions.
Feature smoke: `make check RUN='^TestSerialSmoke$'`.

Change note: `ALTER TABLE target ALTER [COLUMN] c [SET DATA] TYPE type
[COLLATE name] [USING expression]` joins those atomic action lists. Assignment
conversion and USING evaluate current live rows atomically; every USING reads
the original tuple, including columns dropped in the same command. Defaults
are independently coerced; dependent CHECKs are reparsed from their original
definitions against the final types. Each completed transformed row passes
NOT NULL and affected CHECK validation before the next row is converted, with
no partial publication on failure. Dependent views block the change.
Column COLLATE supports default/C/POSIX, `pg_c_utf8`/`pg_unicode_fast` and
registered user identities under deterministic UTF8 byte/codepoint ordering.
Native Dump/Restore preserves provider properties and independent definitions.
Fresh results expose the new type/typmod; held prepared results reject changed
type/typmod/collation while eager rows retain their original metadata and values.
Identity TYPE also migrates the existing internal sequence to the integral target,
including same-type AS/cache replacement; plain OWNED BY sequences are unchanged.
Private sequence state and row changes publish together, while caller currval and
already-discarded cache tails remain physical-session effects on rollback.
See [ALTER TYPE semantics and boundaries](https://www.postgresql.org/docs/18/sql-altertable.html).
Feature smokes: `make check RUN='^TestAlterType(Smoke|ControlsSmoke|IdentitySmoke|IdentityControlsSmoke)$'`.


The [supported types](docs/TYPES.md) include NAME values: mixed-case spelling
is preserved, textual input clips at a63-byte UTF8 boundary, and comparisons use
C byte order. NAME has distinct metadata from TEXT and round-trips through
native Dump/Restore; it does not change SQL identifier folding or add general
name casts/functions. Public feature smoke:
`make check RUN='^TestNameSmoke$'`.

OID is a distinct unsigned four-byte scalar with exact native int64 results,
unsigned ordering, PostgreSQL integer bitcasts/checked INT8 conversion and
native schema/data/default Dump/Restore. It does not populate metadata catalogs
or add object lookup, arithmetic or ordinary SELECT-without-FROM support.
Public feature smoke: `make check RUN='^TestOIDSmoke$'`.

Integral bitwise `&`, `|`, `#`, unary `~`, `<<` and `>>` support SMALLINT,
INTEGER and BIGINT in existing scalar consumers, including replayable defaults
and views. Width, signed results, typed NULL and prepared parameter inference
are preserved; shifts use an INTEGER count. See [integral bitwise operators](https://www.postgresql.org/docs/18/functions-math.html).
Catalog-qualified OPERATOR spellings use the same provider and namespace checks,
including canonical defaults and view replay.
Public feature smoke: `make check RUN='^TestBitwiseSmoke$'`.

Scalar comparison types also support inclusive `BETWEEN`/`NOT BETWEEN` and
both `SYMMETRIC` forms in existing expression consumers. Symmetric predicates
accept reversed bounds; NULL follows SQL three-valued logic rather than a
strict three-argument rule. See [BETWEEN predicates](https://www.postgresql.org/docs/18/queries.html)
for grouped endpoints, contextual prepared typing, replayable grouped/nested
defaults and bounded syntax. Public feature smoke:
`make check RUN='^TestBetweenSmoke$'`.

Supported transaction-owned relation and logical row locks include deadlock
detection: compatible queue reordering resolves avoidable soft cycles; an
unavoidable cycle returns `40P01` / `deadlock detected` to an unspecified cycle
member. Without a savepoint, statement completion rolls back that victim and
releases its locks before explicit ROLLBACK. With recoverable savepoints,
pre-mark effects and holdings survive while failed-level relation/row lock
contributions are released before client recovery. See
[transaction recovery and logical diagnostics](docs/TRANSACTIONS.md).

Explicit `LOCK [TABLE]` supports ordered relation lists, per-target ONLY/star,
all eight table modes (default ACCESS EXCLUSIVE), NOWAIT, documented ANY-rights
privileges and recursive view locking. CREATE inheritance takes parent SUE;
ordinary graph-parent LOCK is rejected, while ONLY is physical. SQL GRANT is not introduced.
Prepare is metadata-only; Execute
requires an explicit block and affects zero rows. Savepoint rollback restores
earlier held modes, including incomparable modes, without replacing the ticket.
Feature checks use the same untagged reference definitions in both modes:
`make check RUN='^TestReferenceLockModes'` (Docker-free)
and `make check REFERENCE=1 RUN='^TestReferenceLockModes'` (native and pinned PostgreSQL).
See the [LOCK TABLE command change note](docs/TRANSACTIONS.md#relation-and-row-resource-manager).

Selected mathematical functions include `random()`, inclusive INTEGER/BIGINT/
NUMERIC bounded `random(lower,upper)`, `random_normal([mean[,stddev]])` and
`setseed(seed)`. Exact NUMERIC sampling preserves the greater input scale.
Seeds and draws belong to the physical connection and survive rollback and
pool return; use a pinned `sql.Conn` for repeatable series. These are
pseudorandom, not cryptographic, and do not promise PostgreSQL's exact PRNG
sequence. See [the capability/change note](https://www.postgresql.org/docs/18/functions-math.html).
Public feature smoke: `make check RUN='^TestRandomSmoke$$'`.

Exact scale and rounding functions include `min_scale`, `round`, `scale`,
`trim_scale`, `trunc`, `sign` and `mod`, with PostgreSQL's selected NUMERIC,
FLOAT8 and integer overloads. NUMERIC ties round away from zero; negative
scales and exact remainder never pass through floating point. See
[the signature and behavior note](https://www.postgresql.org/docs/18/functions-math.html).
Selected public coverage: `make check RUN='^TestReferenceMath(Signatures|NumericRoundScale|SignModulo|Diagnostics|Consumers)$'`
and the same command with `REFERENCE=1`.

Inverse trigonometric functions `acos`, `acosd`, `asin`, `asind`, `atan`, `atand`,
`atan2` and `atan2d` each return FLOAT8, with implicit numeric widening and fixed
prepared inference. Radian functions have principal ranges `[0,π]` (acos),
`[-π/2,π/2]` (asin/atan) and `[-π,π]` (atan2); degree counterparts use
`[0,180]`, `[-90,90]` and `[-180,180]`. Binary argument order is **y,x**.
Degree special angles are exact, including all axis/quadrant angles.
NULL is strict and typed; NaN propagates. acos/acosd/asin/asind reject inputs
outside[-1,1], including infinities, with `22003`, `input is out of range`, and
no execution Position. atan/atand accept infinities. Signed zero and subnormal
operation order are preserved without an underflow error. Go math and PostgreSQL's
host C libm can differ in ordinary last bits; these are not arbitrary-precision
trig overloads. See [the capability/change note](https://www.postgresql.org/docs/18/functions-math.html).
Public feature smoke: `make check RUN='^TestReferenceMathTrigFeatureSmoke$'`
and the same command with `REFERENCE=1`; full independent coverage selects
`RUN='^TestReferenceMathTrig'` in both modes, with900-second external deadlines.
The additional root-owned temporary public Go program runs with
`timeout 60s go run ./.math-trig-feature-smoke` and is removed after recording
its observed angles/quadrants/NULL/diagnostics/reuse result.

Direct trigonometric functions `cos`, `cot`, `sin` and `tan` consume radians;
`cosd`, `cotd`, `sind` and `tand` consume degrees. All eight are strict unary
FLOAT8→FLOAT8 providers with implicit numeric widening and fixed prepared
inference. Degree evaluation reduces finite arguments before conversion and
preserves exact special angles: sind(30)=.5, cosd(60)=.5, tand(45)=cotd(45)=1.
Degree poles return signed infinities, not division-by-zero errors; radian
approximations of π and π/2 are not snapped to ideal poles.
NaN propagates, NULL remains typed, and either infinite input fails with `22003`,
English primary `input is out of range`, without execution Position or portal
Context. Signed zeros follow PostgreSQL's degree operation order: sind(-0)=-0,
tand(-0)=+0, cotd(-0)=-Infinity. Tiny degree results may round to zero without
an underflow error. Go math and PostgreSQL's host C libm can vary in ordinary
last bits and boundary behavior; no arbitrary-precision or host-errno emulation
is implied. See [the direct-trig change note](https://www.postgresql.org/docs/18/functions-math.html).
Public feature smoke selects `RUN='^TestReferenceMathTrigDirectFeatureSmoke$'`
with `make check` and `make check REFERENCE=1`; complete direct and inverse
regression proof selects `RUN='^TestReferenceMathTrig'` in both modes
(900-second external deadlines). The root-owned temporary public Go smoke runs
as `timeout 60s go run ./.math-trig-direct-feature-smoke` after materializing
the living plan's Appendix A source, and is removed after evidence capture.

Hyperbolic functions `sinh`, `cosh`, `tanh`, `asinh`, `acosh` and `atanh`
are strict unary double-precision providers with implicit numeric widening and
FLOAT8 prepared-parameter inference. `acosh(x < 1)` and `atanh(x)` outside
[-1,1] fail with SQLSTATE `22003`, English primary `input is out of range`,
without execution Position or portal Context. `atanh(±1)` returns signed
Infinity. Finite `sinh`/`cosh` overflow also returns Infinity, not an SQL
overflow error; both remain finite at ±710. NaN propagates, NULL retains
FLOAT8 metadata, and the odd functions preserve signed zero.
The [PostgreSQL hyperbolic-function contract](https://www.postgresql.org/docs/18/functions-math.html#FUNCTIONS-MATH-HYP-TABLE)
allows host-C-library accuracy and boundary variation. Native Go math can differ
in ordinary last bits; pinned PostgreSQL18.0 Linux reference proof checks tiny
subnormals and `acosh(+Infinity)`, but does not imply portable C errno emulation
or bit identity across libm implementations. Wrong finite/Infinity categories
at710, zero signs, domains and logical diagnostics are not variance exemptions.
See [numeric selected proof and smoke](reference/numeric/README.md#hyperbolic-functions).

INTERVAL retains independent months/days/microseconds, permitted field ranges
and fractional precision, verbose/SQL/ISO input, SQL comparison/grouping and
canonical native Dump/Restore. Each physical session owns its IntervalStyle;
prepared literals freeze input meaning and retained rows capture output style.
See [interval values](https://www.postgresql.org/docs/18/datatype-datetime.html) and
[IntervalStyle](https://www.postgresql.org/docs/18/runtime-config.html).
Public feature smoke: `make check RUN='^TestIntervalSmoke$'`.



## Minimal example: one unit test

In a checkout of this module, save the following as `example_test.go` and run
`make check RUN='^TestPersonName$'`. In a consuming project, add
stuntgres to your module dependencies and run the test with `go test`.

```go
package stuntgres_test

import (
    "context"
    "database/sql"
    "testing"

    "stuntgres"
)

func TestPersonName(t *testing.T) {
    ctx := context.Background()
    connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
    if err != nil {
        t.Fatal(err)
    }
    db := sql.OpenDB(connector)
    t.Cleanup(func() { db.Close() })

    if _, err := db.ExecContext(ctx,
        "CREATE TABLE people (id INTEGER NOT NULL, name TEXT NOT NULL)"); err != nil {
        t.Fatal(err)
    }
    if _, err := db.ExecContext(ctx,
        "INSERT INTO people (id, name) VALUES ($1, $2)", 1, "Ada"); err != nil {
        t.Fatal(err)
    }

    var name string
    if err := db.QueryRowContext(ctx,
        "SELECT name FROM people WHERE id = $1", 1).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Ada" {
        t.Fatalf("name = %q, want Ada", name)
    }
}
```

The test uses ordinary `database/sql` execution, PostgreSQL-style `$1` parameters,
and `Scan`. Pass `db` to the code under test just as you would a PostgreSQL pool.
A fresh `Driver` gives this test its own database ownership; cleanup closes the
pool without affecting other tests.

Importing stuntgres also registers the driver for `sql.Open("stuntgres", name)`.
The name is an exact opaque string, not a connection URL: identical names share
data within the same Driver, even after pools close. Prefer a fresh `NewDriver`
per fixture when tests must be isolated. See [driver lifecycle](docs/DRIVER.md)
for connection pooling and lifetime details.

## Reusable fixture: two unit tests

Use a helper to create an isolated database, apply the schema, seed data, and
register cleanup. Save this example in a separate `fixture_example_test.go` file;
run it with `make check RUN='^TestPeopleFixture'` in this checkout,
or `go test` in a consuming project.

```go
package stuntgres_test

import (
    "context"
    "database/sql"
    "testing"

    "stuntgres"
)

func newPeopleFixture(t *testing.T) *sql.DB {
    t.Helper()
    connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
    if err != nil {
        t.Fatal(err)
    }
    db := sql.OpenDB(connector)
    t.Cleanup(func() { db.Close() })

    ctx := context.Background()
    for _, query := range []string{
        "CREATE TABLE people (id INTEGER NOT NULL, name TEXT NOT NULL)",
        "INSERT INTO people (id, name) VALUES (1, 'Ada'), (2, 'Grace')",
    } {
        if _, err := db.ExecContext(ctx, query); err != nil {
            t.Fatal(err)
        }
    }
    return db
}

func TestPeopleFixtureLookup(t *testing.T) {
    t.Parallel()
    db := newPeopleFixture(t)

    var name string
    if err := db.QueryRowContext(context.Background(),
        "SELECT name FROM people WHERE id = $1", 2).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Grace" {
        t.Fatalf("name = %q, want Grace", name)
    }
}

func TestPeopleFixtureDelete(t *testing.T) {
    t.Parallel()
    db := newPeopleFixture(t)
    ctx := context.Background()

    result, err := db.ExecContext(ctx, "DELETE FROM people WHERE id = $1", 1)
    if err != nil {
        t.Fatal(err)
    }
    affected, err := result.RowsAffected()
    if err != nil {
        t.Fatal(err)
    }
    if affected != 1 {
        t.Fatalf("deleted %d rows, want 1", affected)
    }

    var name string
    err = db.QueryRowContext(ctx, "SELECT name FROM people WHERE id = $1", 1).Scan(&name)
    if err != sql.ErrNoRows {
        t.Fatalf("lookup after delete: %v, want sql.ErrNoRows", err)
    }
    if err := db.QueryRowContext(ctx,
        "SELECT name FROM people WHERE id = $1", 2).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Grace" {
        t.Fatalf("surviving name = %q, want Grace", name)
    }
}
```

Each helper invocation owns a fresh Driver and seeded database, so the tests can
run in parallel without sharing mutations. `t.Helper` attributes setup failures
to the calling test; `t.Cleanup` closes the pool even when an assertion fails.
For larger fixtures, [native Reset, Dump and Restore](docs/FIXTURES.md) can clear
or replace database contents. Those controls require a native stuntgres pool.

Table queries also support scalar/multiple `GROUP BY` keys, output ordinals,
`HAVING`, `count(*)` and `count(expression)` (both `INT8` results). Ordinary
count evaluates its argument per source row and counts only non-NULL values.
`bool_and`, `bool_or` and `every` accept BOOL arguments and return nullable BOOL;
`every` is the AND operation with its own output label. They ignore NULL inputs
and return NULL for empty/all-NULL input. Arguments still evaluate after a
decisive false AND or true OR, so late errors veto the entire result.
`avg(INTERVAL)` returns nullable INTERVAL, ignoring NULL and returning NULL for
empty/all-NULL input. Its checked month/day/microsecond sums retain physical
representation; fractional months cascade at 30 days and fractional days at 24
hours, with PostgreSQL floating-point nearest-even rounding. For example,
averaging `1 mon` and zero yields `15 days`, not a normalized duration.
The result has unrestricted interval precision.
`avg(SMALLINT/INTEGER/BIGINT/NUMERIC)` returns nullable, unrestricted NUMERIC
with an exact sum and PostgreSQL's base-10000 division scale (at most 1000
fractional digits). The packed sum can overflow even when the mean would fit.
`avg(REAL/DOUBLE PRECISION)` returns FLOAT8; REAL is widened from binary32.
PostgreSQL's full Youngs–Cramer transition is retained, so variance-state
overflow can reject a finite mean. All overloads ignore NULL and return typed
NULL for empty/all-NULL input. For `1,NULL,3`, the mean is numeric
`2.0000000000000000` or floating `2`. No unordered floating accumulation order
is promised. Public AVG feature checks use independent assertions in both modes:
`make check RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`
and `make check REFERENCE=1 RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`.

Change note: `var_pop`, `var_samp`, `variance`, `stddev_pop`, `stddev_samp`
and `stddev` accept SMALLINT, INTEGER, BIGINT, NUMERIC, REAL and DOUBLE PRECISION.
The first four input types return unrestricted NUMERIC; the floating inputs
return FLOAT8. `variance` is `var_samp` and `stddev` is `stddev_samp`, retaining
their own output labels. NULLs are omitted. Empty input returns typed NULL;
population moments of a finite singleton are zero, sample moments are NULL.
Exact numeric moments retain unpacked sums and squares until finalization,
choose PostgreSQL's division scale (up to 1000 fractional digits), and take
standard deviation from that rounded variance at the same scale. Equal finite
numeric inputs return plain `0`. Floating moments retain the released
Youngs–Cramer transition and finite-overflow checks rather than naive squares.
`regr_slope(Y,X)`, `regr_sxx(Y,X)`, `regr_sxy(Y,X)` and `regr_syy(Y,X)`
have one DOUBLE PRECISION signature and return FLOAT8; other numeric inputs
coerce to that signature. A pair is omitted if either axis is NULL. For
X=1,2,3 and Y=2,4,6 the results are respectively 2,2,4,8. Empty input returns
NULL; a finite singleton has zero sums and NULL slope. Zero X variance gives
NULL slope; zero Y variance with varying X gives zero slope. Floating roundoff
is not clamped, and the six-field REL_18_0 transition checks both axes even if
only one component is requested. These ten names use ordinary grouping,
FILTER/DISTINCT/ALL/in-call ordering, direct/prepared execution and stored-view
restoration, with nullable typmod -1 metadata and atomic eager publication.
They do not add native window or parallel aggregate execution.
Feature smoke: `make check RUN='^TestReferenceStatisticalAggregateSmoke$'`
and `make check RUN='^TestReferenceStatisticalAggregateSmoke$' REFERENCE=1`;
these assert moments, prepared inputs, metadata, native restore and atomic errors.

Change note: `corr`, `covar_pop`, `covar_samp`, `regr_avgx`, `regr_avgy`,
`regr_count`, `regr_intercept` and `regr_r2` extend the same ordinary aggregate
pipeline. All take Y first, X second and independently coerce numeric inputs to
DOUBLE PRECISION; only complete non-NULL pairs contribute. Seven return nullable
FLOAT8; `regr_count` returns nonnullable INT8, including exact zero for no pairs.
Count uses checked integer arithmetic independent of floating moments, so huge
finite or nonfinite payloads do not cause moment overflow in a count-only call.
All have typmod -1 and no physical origin; unknown parameters infer FLOAT8.
For `(Y,X)=(3,1),(5,2),(7,3)`, results in the order above are
`1,4/3,2,2,5,3,1,1`. No pairs yield seven NULLs and count0. Sample covariance
needs two pairs. Correlation needs nonzero variance on both axes; intercept and
r² need nonzero X variance. With varying X and constant Y, intercept is Y and
r² is1. Floating averages still check both axes for transition overflow.
Named approximate mathematical witnesses use
`abs(actual-expected) <= 1e-12*max(1,abs(expected))`; counts, NULLs, metadata and
diagnostics stay exact. No unordered floating accumulation order is promised.
Native statistical windows and parallel grouping remain unsupported.
Feature smoke: `make check RUN='^TestReferenceCorrelationRegressionFeatureSmoke$'`
and the same command with `REFERENCE=1`; it exercises all eight outputs,
empty/constant cases, held-statement freshness, native restore and atomic errors.

Change note: `sum(SMALLINT/INTEGER)` returns INT8; `sum(BIGINT/NUMERIC)`
returns unrestricted NUMERIC; `sum(REAL)` returns FLOAT4 and
`sum(DOUBLE PRECISION)` returns FLOAT8. All six ignore NULL, return typed NULL
for empty/all-NULL input, and give `4` for `1,NULL,3`. Results are nullable,
typmod -1 and have no physical origin. BIGINT accumulation is exact beyond
signed64 and binary64 limits. NUMERIC preserves maximum finite input display
scale, including zero/cancellation, and checks range only when packing the final
finite sum; NaN/opposite infinities yield NaN and one infinity sign dominates.
Floating SUM seeds from the first non-NULL value (retaining negative zero) and
rounds each addition in its input precision. Finite overflow fails immediately
with22003, `value out of range: overflow`; explicit infinities/NaN follow IEEE
arithmetic. No unordered transition order is promised. Direct/prepared calls,
FILTER/DISTINCT/ALL/in-call ordering and stored views use the ordinary eager
aggregate pipeline. Unknown unary arguments give42725; invalid signatures,
including zero/star/multiple arguments, give42883. UPDATE/VALUES/RETURNING
remain forbidden clauses. MONEY and INTERVAL windows remain excluded.
Public numeric feature smoke:
`make check RUN='^TestReferenceNumericSumSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceNumericSumSmoke$'`.
Change note: ordinary `sum(MONEY)` returns MONEY and `sum(INTERVAL)` returns
unrestricted INTERVAL. Both omit SQL NULL and return one typed NULL for global
empty/all-NULL input; empty grouped input returns no rows. MONEY accumulates
exact signed minor units (`12.34 + 56.78` gives `$69.12` under C monetary locale)
and rejects overflow with22003 `money out of range`. INTERVAL keeps months,
days and microseconds distinct (`1 mon + 30 days` gives `1 mon 30 days`,
and `1 day + 24 hours` gives `1 day 24:00:00`) without AVG scaling or binary64
rounding. Component overflow, reserved infinity tuples and opposite infinity
signs fail with22008 `interval out of range`; one infinity sign does not bypass
finite-transition range checks. FILTER, DISTINCT, local ORDER, grouping,
direct/prepared parameters, stored views and native control round trips use the
existing ordinary aggregate pipeline. Results are nullable, base-typed,
typmod -1 and origin-free. No unordered transition order is promised.
Both new ordinary overloads remain excluded from numeric-only native windows.
See [independent SUM coverage](reference/aggregates/README.md#money-and-interval-sum).
Feature smoke: `make check RUN='^TestReferenceMoneyIntervalSumFeatureSmoke$'`
and `make check RUN='^TestReferenceMoneyIntervalSumFeatureSmoke$' REFERENCE=1`.
`any_value(anyelement)` returns an arbitrary non-NULL input of its concrete type,
or typed NULL for empty/all-NULL input. It preserves the datum representation
and collation, but not input typmod or physical origin; metadata is nullable.
No stable winner is promised when candidates differ. Explicitly type unknown
arguments, for example `any_value($1::integer)`. Later argument errors still
veto all output after a candidate is retained. Public direct/prepared, view,
HAVING/ORDER and INSERT SELECT witnesses share one independent contract:
`make check RUN='^TestReferenceAnyValue'`
and `make check REFERENCE=1 RUN='^TestReferenceAnyValue'`.
Change note: `min(value)` and `max(value)` ignore NULL and return the smallest
or largest eligible datum, or typed NULL for empty/all-NULL global input.
Supported overloads cover integers, OID, floating/exact numeric, TEXT/BPCHAR,
BYTEA and the implemented date/time/interval types. VARCHAR, NAME and internal
CHAR select TEXT; REGCLASS selects OID; domains resolve through their base type.
Unknown strings, NULL and untyped parameters infer TEXT. Results have unrestricted
typmod, nullable metadata and no physical origin; the selected datum is not
rounded or reconstructed. `SELECT min(v),max(v) FROM grouped_rows` uses the same
eager pipeline as grouped/HAVING/ORDER queries, views and INSERT SELECT.
Conflicting implicit string collations fail only when a second eligible value
requires comparison (`42P22`); use a final explicit COLLATE to resolve them.
BOOL/UUID have no overload (`42883`); NAME[] and private PG_NODE_TREE remain
explicit native capability boundaries, without adding arrays or general casts.
Feature smoke: `make check RUN='^TestReferenceExtremaSmoke$'`;
independent contracts: `make check REFERENCE=1 RUN='^TestReferenceExtrema'`.
Change note: `string_agg(text,text)` and `string_agg(bytea,bytea)` return nullable
TEXT/Go string and BYTEA/raw Go []byte. NULL values are ignored; a NULL delimiter
adds no separator. Each later surviving value uses its own row's delimiter;
the first delimiter is ignored and empty values still count as values.
Empty/all-NULL global input returns typed NULL. Use call-local
`ORDER BY` with a unique tiebreaker to guarantee concatenation order; outer
ordering only orders result groups. Each call can use different source-key order.
Direct/prepared queries, held statements, INSERT SELECT and persistent views/native
Dump/Restore use the same eager pipeline. All existing aggregates admit
FILTER, DISTINCT, default-equivalent ALL and call-local ORDER BY. Window execution
is restricted to row_number(), rank(), dense_rank(), percent_rank(), cume_dist(),
count(*), existing integer/numeric/floating SUM, ntile, lead/lag,
and first_value/last_value/nth_value;
window FILTER, DISTINCT and call-local aggregate ORDER BY remain excluded.
Feature smoke: `make check RUN='^TestReferenceStringAggSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceStringAgg'`.

Change note: both `array_agg` overloads use the same eager aggregate engine.
Scalar input includes every NULL and returns its actual associated array type;
no admitted rows return SQL NULL. Array input concatenates matching nonempty
arrays into one higher dimension, preserving inner bounds and element NULLs.
Whole NULL arrays, empty arrays, mismatched shapes/bounds and rank overflow are
SQL errors. Use call-local ORDER BY with a unique key to specify element order.
Scalar DISTINCT is supported; array DISTINCT/array-valued ordering and window
execution retain the existing native capability boundaries.
Feature smoke: `make check RUN='^TestReferenceArrayAggFeatureSmoke$'`;
pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceArrayAgg'`.

Equal typed keys, including NULL keys, form one group per set occurrence; empty global
count returns zero. Empty input emits only empty-set groups, not keyed groups. Group order is not
promised. The same eager pipeline serves direct/prepared queries and
`INSERT ... SELECT`; public rows never evaluate aggregates during `Next`.
See [query grouping](https://www.postgresql.org/docs/18/functions-aggregate.html) for examples,
diagnostics and deliberate exclusions.

Change note: `GROUPING SETS`, `ROLLUP`, `CUBE`, explicit `GROUP BY ()` and
`GROUP BY ALL`/`DISTINCT` use the same eager grouping pipeline. Missing keys
become typed NULLs; `GROUPING(a,b)` returns nonnullable INT4 bits (rightmost
argument is bit0), distinguishing placeholders from source NULLs. Default/ALL
retain duplicate sets; DISTINCT removes duplicate sets, not duplicate rows.
Empty sets emit a group even on empty input. Keys/aggregate arguments are
evaluated once per source occurrence across levels. Expansion is limited to4096
raw sets before DISTINCT, CUBE to12 units and GROUPING to31 arguments. PostgreSQL
18's correlated outer-Var operation is supported; correlated computed arguments
retain its42803 boundary. Views/native controls, consumers and post-group windows
share this contract; adjacent aggregate/window/type restrictions are unchanged.
ROLLUP/CUBE accept ordinary expression units and indivisible sublists, not empty
or nested grouping-set children. Supplied parameter identity is structural;
held non-NULL/NULL reuse preserves included versus omitted indicator bits.
See [grouping sets](https://www.postgresql.org/docs/18/queries.html).
Smoke: `make check RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'`;
oracle: `make check REFERENCE=1 RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'`.

Change note: aggregate modifiers share one input pipeline across COUNT, Boolean,
AVG, any_value, min/max, string_agg, array_agg and range-set providers. FILTER admits only TRUE and runs
before arguments and local keys. DISTINCT compares the complete typed argument
tuple (including string_agg's delimiter), with NULL-equal keys. Strict providers
skip NULL values; scalar array_agg includes them and array-input array_agg rejects
whole NULL arrays. Admitted arguments/keys evaluate before duplicate or NULL
admission. Local ORDER uses source expressions, not output aliases/ordinals,
and DISTINCT keys must match complete arguments (implicit coercion permitted,
explicit casts retained). Matching volatile argument/order targets evaluate once
within each call; volatile calls never share state. Final ORDER only sorts groups.
Native aggregate DISTINCT/order on arrays and PG_NODE_TREE has an explicit0A000
capability boundary; PostgreSQL supports those carriers. Explicit tree-to-TEXT
casts use existing TEXT support. No ordinary array/tree ordering is added.
Smoke: `make check RUN='^TestReferenceAggregateModifiersFeatureSmoke$'`;
oracle: `make check REFERENCE=1 RUN='^TestReferenceAggregateModifiersFeatureSmoke$'`.

## Documentation

- [SQL dialect and compatibility limits](docs/DIALECT.md): check support before
  using PostgreSQL-specific behavior.
- [SQL reference](https://www.postgresql.org/docs/18/sql-commands.html): topic guides for queries, table definitions,
  sequences, expressions, types, identifiers, and literals.
- [Driver lifecycle and notices](docs/DRIVER.md), [session settings](docs/SETTINGS.md),
  and [transactions and locks](docs/TRANSACTIONS.md).
- [Roles, authorization and schemas](docs/AUTHORIZATION.md).
- [Native fixture controls](docs/FIXTURES.md).

## Building, testing and contributing

Use Go 1.26 or later and GNU `timeout`. From the repository root:

```sh

make build      # compile production packages
make check RUN='^TestReferenceFixtureResetIsolation$' # selected proof
make quickcheck # full Docker-free verification after edits, before review/integration
make racecheck  # same suite with race detection
make finalcheck # after merge: startup benchmark, socket-free native race suite and dependencies
```

Use `check RUN='...'` only for individual tests or small selected sets; add
`REFERENCE=1` for pinned PostgreSQL or `RACE=1` for race detection. Changed/new
reference definitions require the same selected tests in both modes before review.
Targets regenerate parser and Unicode data, keep output quiet and impose no deadline
(`-timeout=0`). Agents set external deadlines: 900 seconds for check, quickcheck,
racecheck and bench; 1800 seconds for finalcheck. `refcheck` is human-only; agents
MUST NEVER invoke it. Commit post-merge failure remedies separately.

The migrated query-read, scalar-type, INSERT SELECT, UPDATE, DELETE/RETURNING,
identity, arithmetic, bitwise, mathematical and scalar expression scenarios,
DATE/TIME/TIMESTAMP interactions, transactions, savepoints, relation/row locks
and BYTEA/short-INSERT pilots in `reference/` also run without Docker through
the quickcheck target:

```sh
make check # quick: real stuntgres + expected data
make check REFERENCE=1 # pinned PostgreSQL 18.0 + legacy/migrated
```

Query-read migration moves 67 original SQL-interaction tests into 80 untagged
definitions covering projection, NULL counts, aggregates, joins, ordering and
LIMIT. The DML migration carries all 61 assigned root tests into complete
reference assertion unions for INSERT SELECT, UPDATE and DELETE/RETURNING.
Identity migration likewise merges all 28 assigned root interaction tests with
existing reference assertions in untagged `TestReferenceIdentity*` definitions.
The arithmetic migration carries 61 original tests into 81 untagged definitions,
including every bitwise width/shift/signature vector and mathematical metadata,
parameter and lifecycle case. The scalar expression migration unions all 62
assigned root tests with existing reference assertions in 93 untagged definitions
covering BETWEEN, explicit casts, null/truth/distinct predicates, ordinary
comparisons, conditional expressions, expression core and logical operators.
The scalar-type migration merges all 63 assigned root SQL-interaction tests from
25 files with existing reference coverage into 89 untagged definitions in the
Character, Float, Name, Numeric, OID, Smallint, TypeCore and UUID families. They
preserve raw values and descriptors, complete diagnostics, affected counts,
ownership, actual held frames, concurrency, cancellation and retained native
controls. Adapter differences are independently asserted; the NAME
physical/binary complement remains a tagged PostgreSQL-only definition.
The temporal migration carries all 51 assigned root DATE, TIME and
timestamp-without-time-zone interaction tests into complete reference assertion
unions, including accepted/rejected input corpora, precision, held clocks, owned
raw rows and retained metadata. Native Dump/Reset/Restore phases use the common
scenario's public control pool; true mode also replays its unchanged SQL on
PostgreSQL.
The transaction/locking migration carries all 111 assigned original SQL tests
from 38 root files into 130 untagged external definitions, merging their complete
owning reference assertions, including the same-family row-lock ViewInsert case.
Native controls and raw Begin-context lifetime remain distinct capability phases;
actual PostgreSQL server cancellation and physical lock witnesses are independent
phases, not native queue or raw-context parity. Pure non-SQL terminal reasons and
raw reuse mechanics remain root units. This is test relocation/deduplication, not
new SQL support or a production API change.
Together these families retain raw and command/RETURNING metadata, genuinely
reused and concurrent held statements, atomicity, cancellation and native
controls. The BYTEA and short-INSERT pilots remain available alongside them.
Each selected backend must satisfy independently hardcoded expectations, with
explicit phases for backend-specific metadata, diagnostics, controls and lock
witnesses; parity alone is not proof. Startup failure never falls back to native
execution.
See the [development guide](docs/DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
for the migrated families, temporal adapter differences and complete selection
commands.

Change note: the final driver/session/catalog cutover relocates all372 remaining
root SQL scenarios into651 shared untagged reference definitions and44 separately
selected companions:39 PostgreSQL-only and five PostgreSQL-required differential
definitions. All131 obsolete helper dispositions are accounted for. Actual
original DB/Tx/Stmt and pinned-Conn producers, typed/metadata/raw consumers,
SAME held statements, native controls and failure-safe cleanup survive alongside
the recorded Context assertion union.
No root SQL helper/simulation remains;13 focused nonSQL units and the startup
benchmark remain. This changes test placement, not production SQL behavior.
See [whole-root cutover commands](docs/DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover).

The `reference` build tag still includes untouched legacy PostgreSQL tests and
retained PostgreSQL-only edges; it does not select runtime mode. A tagged
selection restricted to mode-aware migrated tests with `-reference=false`
remains Docker-free, but selecting a legacy test lazily starts its package's pinned
container even when the flag is absent or false. Whole-root cutover does not
convert every PostgreSQL-only ACL, inheritance, keyed-row, role-lifetime or
rules/trigger edge to native coverage. `TestReferenceControlTransactionsDiagnostics`
is now an untagged dual-mode scenario, not a legacy Docker-only test.
`make check RUN='...' REFERENCE=1` enables the `reference` build tag and
`-args -reference=true` only for reference test packages discovered recursively
under `./reference/...`, with `-p 1` serializing independent package/container
lifecycles. Ordinary selectors run packages containing matching top-level test
names; slash-containing subtest selectors conservatively retain every test
package. A no-match selector still starts the fixture package's pinned oracle.
The reference tree has at most 30 packages including the importable common
`reference/fixture` and selector command, each at most 1,000,000 bytes across all
files. Explicit true eagerly starts each selected package's oracle before test
execution. After merge, `make finalcheck`
runs generation, startup benchmark, socket-free Docker-free native race tests and
dependencies under the agent's external 1800-second deadline. The `noservertests`
tag excludes real-TCP server checks only from finalcheck; they remain enabled in
quickcheck, racecheck and selected `make check` runs. Earlier task-specific
aggregate bounds are historical evidence, not current Makefile behavior.


Start with the [dialect contract](docs/DIALECT.md) and
[development guide](docs/DEVELOPMENT.md). Root packages expose the driver and
native APIs; `internal/parser`, `internal/engine`, and `internal/sqltype` own
parsing, execution, and SQL value handling. Public SQL scenarios live in
feature-group packages under `reference/`; shared compatibility fixtures live
in the importable `reference/fixture` package.

For behavior changes, add a failing behavioral regression first and ground
compatibility expectations in PostgreSQL 18. Edit `internal/parser/grammar.y`,
not generated parser output, and run `make generate` after grammar changes.
Keep production dependencies limited to the standard library and this module.
Run the relevant public scenario and full verification before submitting changes.
The [development guide](docs/DEVELOPMENT.md) documents the pinned reference image,
additional targets, and verification conventions; [design notes](docs/design)
provide architectural background.

## Support and issues

Report bugs and feature requests through this repository's issue tracker.
Include a minimal reproducer, Go and stuntgres versions, the SQL and parameters,
and the expected and actual results. For compatibility reports, include the
behavior observed on PostgreSQL 18 and any SQLSTATE diagnostic.

### SELECT CTEs — change note

Nonrecursive `WITH name [(columns)] AS [MATERIALIZED | NOT MATERIALIZED]
(SELECT ...)` supports dependent and reused virtual relations, lexical shadowing,
positional column aliases and existing SELECT consumers. Definitions never
create catalog objects. Eligible single-use definitions fold by default;
`MATERIALIZED` preserves the evaluation boundary, while `NOT MATERIALIZED`
folds eligible repeated references. Whole-definition VOLATILE expressions
prevent folding. Shared results are produced lazily once per owning query
invocation, not cached on prepared statements.
Proven FALSE/BOOL NULL consumers skip opaque SELECT CTE bodies, including their internal
locks. Existing native stored marked aggregate/blocking frontiers still run when
independently reachable; dead qualification does not bypass their physical lock
settlement.

Standalone VALUES/TABLE CTE bodies and lateral derived queries remain excluded. See
[WITH query contracts](https://www.postgresql.org/docs/18/queries.html) and
[implementation design](docs/design/PG18-QUERY-CTE.md).
Feature smoke: `make check RUN='^TestReferenceQueryCTEFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryCTEFeatureSmoke$'`.

### Recursive CTEs — change note

`WITH RECURSIVE` supports anchor/recursive-term `UNION [ALL | DISTINCT]`
queries with an invocation-local worktable. Each pass reads only the preceding
generation. Positive-width UNION deduplicates complete typed tuples, including
NULLs, against all accepted generations; ALL allocates no deduplication state.
PostgreSQL18's zero-column recursive UNION also retains occurrences like ALL.
Parent LIMIT can stop an otherwise infinite stream; blocking consumers may drain it.
Traversal order is not a portable promise.

Anchor output establishes the recursive names and exact type/typmod/collation
contract before the recursive arm binds. Forward siblings are topologically bound
with lexical shadowing; mutual recursion and prohibited references/terms receive
PostgreSQL diagnostics. Genuine recursion never folds, including NOT MATERIALIZED.
Prepared/correlated executions own fresh generations, parameters and seen sets.
Cancellation and late errors veto result/write publication. Stored views retain
symbolic recursive definitions and physical dependencies, not runtime worktables.

SELECT, INSERT, UPDATE and DELETE accept the recursive modifier. MERGE rejects
its own `WITH RECURSIVE`, but can consume a derived recursive SELECT.
Recursive modifying definitions remain excluded.
See [recursive query design](docs/design/PG18-QUERY-CTE-RECURSIVE.md) and
[selected dual-mode proof](reference/query_core/README.md#recursive-cte-verification).
Feature smoke: `make check RUN='^TestReferenceQueryRecursiveCTEFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryRecursiveCTEFeatureSmoke$'`.

### Recursive cycle detection — change note

`CYCLE keys SET mark [TO constant DEFAULT constant] USING path` adds the
mark and a `record[]` ancestry path after the original recursive columns.
Keys form one ordered record; NULL fields compare equal. Paths are branch-local,
and the cycle-closing row is emitted with its complete path. Expansion retains
only parents for which SQL `mark <> cycle-value` is TRUE, including custom or
NULL marks. UNION DISTINCT includes the generated mark and full path.
Self-reference stars omit generated columns, while explicit references and
external stars expose them. Prepared executions start fresh; raw/text paths
retain their public result type across key-field-only type changes.
Stored recipes may project original columns, marks or `path::text`; persistent
`record[]` result columns are rejected. Generic composite grouping is unchanged.
See [cycle design](docs/design/PG18-QUERY-CTE-RECURSIVE-CYCLE.md) and
[selected proof](reference/query_core/README.md#recursive-cycle-verification).
Feature smoke: `make check RUN='^TestReferenceQueryCycleFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryCycleFeatureSmoke$'`.

### Recursive SEARCH ordering — change note

SEARCH generates real ordering values: BREADTH FIRST exposes RECORD containing
zero-based INT8 depth and current keys; DEPTH FIRST exposes _RECORD containing
the current branch's key-record ancestry. ORDER BY the generated column obtains
breadth/depth order; recursive evaluation order itself is not promised. NULL keys
are typed fields inside non-NULL records, and equal keys remain ties.
The complete generated tuple participates in recursive UNION identity. Combined
SEARCH/CYCLE output is originals, sequence, mark, path; existing CYCLE closing
rows and stop semantics are unchanged. Comparison uses retained key types and
collations lazily, including execution-time failure for reached JSON keys.
Prepared executions start fresh and preserve public RECORD/_RECORD result types
across key-field reanalysis without relaxing internal shapes or catalog fences.
Persist ordinary scalar outputs or seq::text; anonymous record/path columns are
rejected. Views and native Dump/Restore retain symbolic SEARCH, not frontiers.
See [SEARCH design](docs/design/PG18-QUERY-CTE-RECURSIVE-SEARCH.md) and
[selected proof](reference/query_core/README.md#recursive-search-verification).
Feature smoke: `make check RUN='^TestReferenceQuerySearchFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQuerySearchFeatureSmoke$'`.



### Data-modifying CTEs — change note

Nonrecursive WITH can attach to SELECT, INSERT, UPDATE, DELETE or MERGE and can
contain the four installed data-modifying commands. RETURNING exposes a typed
virtual relation to later CTEs and the primary command. Each modifier executes
once and to completion, even unused, without RETURNING, or behind an executing
main query's LIMIT 0, false qualification or partial consumption. Materialization
hints never fold or repeat a modifier; ordinary SELECT CTEs remain lazy.

All components share the statement-entry physical snapshot. Physical scans see
prior transaction work but not sibling statement effects; RETURNING is the
communication channel for changed rows. Native scheduling is sequential, not a
portable sibling-order guarantee. Modifying the same physical row twice remains
unsupported/unpredictable. Only the primary command supplies public rows,
metadata and affected count; auxiliary counts are not summed.

One statement publication covers every target. Failure, producer-close error or
cancellation before publication discards all provisional rows and effects.
Sequence draws remain nontransactional. Cancellation of an already successful
eager result does not retract committed writes. Prepare never executes a body or
draws a sequence; each held execution imports fresh arguments, checks current
rights and owns a fresh invocation. CTE names/output never become catalog objects
or native Dump history; Reset/Restore preserves persistent definitions/data only.

Modifying WITH must attach at the top level, and a referenced modifier needs
RETURNING. CREATE VIEW rejects it at execution, including held statements;
materialized views and DECLARE CURSOR reject it during held preparation.
Existing CREATE TABLE AS and SELECT INTO can consume it atomically. CREATE TABLE
AS WITH NO DATA does not execute its query at all: the destination is empty and
auxiliary targets remain unchanged. This is not an exception to modifier
completion inside an executing LIMIT 0 query.

Trigger/FDW/hierarchy providers retain their existing exclusions.
Rule rewrites preserve original auxiliary identity and atomic completion when
they leave one executable query; zero/multiple expansion retains PostgreSQL's
INSTEAD NOTHING/conditional/ALSO/multi-statement diagnostics. PostgreSQL separately
prohibits recursive DML self-reference.
See [PostgreSQL modifying WITH](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-MODIFYING)
and [selected proof and standalone smoke](reference/dml/README.md#data-modifying-cte-contracts).
