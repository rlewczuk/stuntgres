# Native compatibility limits

[README](../README.md) · [Driver contracts](DRIVER.md) · [Fixture controls](FIXTURES.md) · [Development](DEVELOPMENT.md)

[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/) is the authority
for SQL syntax and standard semantics. Full PostgreSQL compatibility is the goal,
not a claim about the current engine. The boundaries below describe native
implementation limits, not a supported-syntax inventory. A recognized construct,
catalog identity or stored value does not imply that every operation on it exists.

## Server transport

`cmd/stuntgres` uses the same engine through PostgreSQL protocol 3.0 simple
queries. Wire parameters, prepared statements, portals, FunctionCall, streaming
COPY and TLS are unavailable and fail explicitly. This restriction is separate
from engine-owned SQL prepared names and cursors. Only UTF8/UNICODE client
encoding is admitted; there is no wire transcoder. Each allowed network account
is a local administrator of its independently owned in-memory databases.

See [commands and security](../README.md#phase-1-server--change-note) and
[selected server proof](../reference/sessions/README.md#phase-1-server-selected-proof).

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

## Storage and native controls

Databases and prepared transactions are process-local. There is no WAL, crash
recovery, physical backup, replication, subscription execution or physical
checkpoint service. Native sequence `log_cnt` is always zero; cache state does
not promise crash durability. Native controls are logical fixture replacement,
not arbitrary `pg_dump` import or PostgreSQL physical backup.

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

Dump excludes temporary objects, session settings, prepared names, cursors,
role creation, passwords and membership. Arbitrary explicit authorization ACLs
and invalid indexes cannot be serialized and are rejected rather than silently discarded.
Restore has a restricted parameter-free replay boundary, not general script
execution. It does not import temporary objects, transaction controls, shared
role effects or arbitrary CTE/RETURNING history. See [fixture contracts](FIXTURES.md).

## Authentication and authorization

Native connector role admission is a trusted host assertion, not password/SCRAM
authentication. Stored passwords and expiry timestamps do not affect that route.
Physical session limits and login eligibility are enforced independently of
pooled borrowers; see [driver admission](DRIVER.md#trusted-role-admission-and-physical-limits).

Policy definitions and row-security flags do not provide policy-expression
enforcement. Applicable policy expressions fail explicitly instead of being
ignored. Operating-system file/program access, PostgreSQL PID signaling and
reserved connection slots are unavailable. Predefined role identities do not
create these missing capabilities. Table/column grants do not imply sequence or
other-object grants; schema and type/domain grants remain unavailable.

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
query or its modifiers. Recursive/SEARCH/CYCLE, rule/trigger/FDW and hierarchy
provider exclusions remain unchanged. No portable same-row overlap winner or
sibling execution order is promised. See [query ownership](QUERIES.md) and
[PostgreSQL restrictions](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-MODIFYING).


## Tables, hierarchies and materializations

RANGE `ALTER TABLE ... ATTACH PARTITION` accepts an existing ordinary or
partitioned relation, with `FOR VALUES` or `DEFAULT`, including explicit
transactions and savepoints. Ordinary `DETACH PARTITION` retains the child as a
standalone table or virtual root, including transactions/savepoints.
CONCURRENTLY/FINALIZE, other partition hierarchy alterations, update/delete,
row locking and explicit hierarchy locking remain excluded. Foreign keys
involving partition hierarchies retain their existing unsupported boundary.
Conflict arbitration and MERGE cannot target partition
hierarchies. Temporary partition definitions and partition definitions nested in
schema creation are unavailable. Nonunique virtual partition indexes and partition
reindexing are unavailable; partition key/constraint combinations remain bounded.
RANGE keys require an admitted btree ordering provider. Native integer-array
keys currently retain the existing missing-default-operator-class boundary;
ordinary array storage does not add a partition ordering provider.
SQL tableoid reads retain the existing native missing-column boundary; physical
identity remains represented by catalog relations and capture provenance.
The private logical importer can replay NOT VALID CHECK or local UNIQUE/PRIMARY
ADD USING INDEX on an exact captured physical member when dependency ordering
requires it. A deferred virtual local UNIQUE/PRIMARY tree instead uses a captured
conventional constraint definition after its intended plain tree is attached.
Exact operation/definition/table/subtree origins fence this private replay;
PRIMARY replay retains already-present NOT NULL facts. Public hierarchy ADD and
other hierarchy alterations remain excluded; these origins do not authorize user SQL.
A permanent partitioned parent cannot attach an ordinary temporary candidate;
existing temporary namespace/session authority remains the persistence boundary.
A NOT VALID child NOT NULL constraint cannot satisfy a validated parent
requirement, regardless of constraint names or whether the candidate has rows.
ATTACH rejects the mismatch rather than validating or replacing the child fact.
Recursive inheritance update/delete/locking and graph-shape alterations other
than ordinary partition attachment/detachment remain excluded.
Copying a temporary-sequence default into a permanent table is unavailable.

Materialized views support logical indexes and autocommit concurrent refresh.
Explicit transaction refresh and refresh as Restore input remain excluded.
Physical temporary heaps, SPI diff SQL/Context, WAL and crash durability remain
server mechanisms, not native logical publication contracts. Trigger, pending
trigger, foreign-table/FDW and procedural-language execution are unavailable.
Truncation does not provide identity restart.

Adding generated columns and changing their destination type without an explicit
conversion remain excluded. Direct typed-table creation cannot add identity or
generated options. Primary-key creation retains deferrability and temporal-key
exclusions; ordinary key-list attachment through table alteration is not general
primary-key creation. Mixed default-setting/removal action lists remain excluded.

Read-only `pg_catalog.pg_constraint` discovery uses PostgreSQL 18's complete
28-column shape for actual supported table constraint owners (n/c/u/p/f/x).
Metadata includes real relation/namespace/index identities, validity, declaration
deferral, provenance and ordered key/operator/action vectors; inapplicable
vectors/tree values are SQL NULL. Metadata reads do not filter by table SELECT
privilege, and TEMP catalog discovery is distinct from session-scoped unqualified
relation resolution. Domain constraints, constraint triggers, temporal and
NOT ENFORCED definitions are not fabricated or admitted by this projection.
CHECK `conbin` is honest native structural PG_NODE_TREE data rather than
PostgreSQL nodeToString bytes; compare deparsed semantics, not internal bytes.
Catalog mutation and row locking remain unsupported through existing native
readonly-catalog boundaries.


## COPY streams

COPY requires the [pinned native stream helpers](DRIVER.md#pinned-copy-text-streams--change-note),
not ordinary database/sql execution or prepared COPY. File and program endpoints
never perform external I/O. Encoding overrides, freezing, row filtering and
error-skipping/logging options remain excluded. Legacy CSV option spellings are
outside the modern CSV implementation. Physical export of a virtual partition
root is unavailable. Binary providers do not add storage, cast, DDL or catalog
mutation capabilities; `pg_node_tree` binary input is excluded.

## pg_namespace namespace metadata

`pg_catalog.pg_namespace` is a readonly projection of actual namespace
identities, names, owner OIDs and nullable typed ACLITEM arrays. Inaccessible
schemas remain visible in inventory; metadata visibility is not schema USAGE.
Initialized builtin identities/ACLs and supported schema DDL/default-privilege
effects are exact. Real TEMP table/toast metadata is retained after physical
close while objects retire. The toast descriptor does not enable TOAST storage
or own-table authority. Catalog mutation and virtual-relation row locks remain
unsupported; this feature adds no schema GRANT/REVOKE SQL or ACL serialization.
Registered rowtype ownership does not add a `pg_type` SQL source. Native role
rename SQL remains excluded; actual namespace ownership/ACL merge is supported
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
reflect the captured database owner and actual stored ACL, including supported
transactional CREATE grants/options and owner replacement. Initial native ACL is
`{postgres=C*/postgres}`, not PostgreSQL's default NULL. No CONNECT/TEMP privileges
or database creation are added. Ordinary roles can read metadata independently of
database CREATE rights; search-path resolution uses ordinary catalog precedence
and shadowing.

`encoding=6`, `datlocprovider='c'`, `datcollate='C'` and `datctype='C'` declare
bounded native UTF8/C semantics, not host libc/ICU infrastructure. Native
`dathasloginevt=false` reflects absence of installed login event triggers.
The following fields exist with their documented concrete SQL types and typed
NULL values, rather than invented cluster/disk facts:

- `datistemplate BOOL`, `datallowconn BOOL`, `datconnlimit INT4`: no database-owned
  template, mutable admission or connection-limit metadata.
- `datfrozenxid XID`, `datminmxid XID`: no physical freezing/multixact horizons.
- `dattablespace OID`: no physical database placement.
- `datlocale TEXT`, `daticurules TEXT`, `datcollversion TEXT`: inapplicable to
  native C semantics, not fabricated ICU locales/rules or host version strings.

These NULL fields and `datacl` are nullable in native result metadata; the other
known facts are nonnullable. NULL comparisons retain ordinary SQL three-valued
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
  `relrewrite`, `relfrozenxid` and `relminmxid` are typed zero sentinels: native
  relation files, page/maps, TOAST, transient rewrites and freezing/multixact
  horizons do not exist. Zero filenode does not claim PostgreSQL mapped storage.
- `reltuples` is -1 (unknown) for every nonsequence kind, including indexes;
  sequence 1 is fixed logical singleton-generator metadata, with zero native
  pages. Estimates never become count(*), historical version counts or sizes.
  Real PostgreSQL sequence pages and partitioned-index initialization can differ;
  catalog reads do not implement ANALYZE/VACUUM.
- `relam` exposes logical heap=2, btree=403 and hash=405 compatibility identities,
  not PostgreSQL physical heap/index storage. Default partition roots and
  sequence/view/composite relations have method zero.
- The exact represented shared catalog identities 1213/1262/2396 expose shared=true
  and pg_global=1664 metadata. Data is still database-local; this does not enable
  physical user tablespaces. User table/MV/partition replica-identity defaults
  likewise do not implement replication.
- `relhastriggers` is false: native FK execution is logical, not PostgreSQL's
  internal trigger machinery. View `_RETURN` compatibility flags do not add
  general CREATE RULE execution or a trigger catalog.

`relhasindex` and `relhassubclass` are immutable lazy publication histories;
removing the last actual index/child does not clear them. All nineteen represented
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
immutable typed RANGE/default facts. Native output is
`{PartitionBound strategy range default true}` or
`{PartitionBound strategy range default false lower (...) upper (...)}`.
Finite datums are `{type OID typmod N collation OID value "..."}` with captured
key Usage and quoted type-owned output; sentinels are MINVALUE/MAXVALUE.
This is intentionally not PostgreSQL nodeToString or SQL FOR VALUES text.
Index partitions have NULL bounds. Additional partition strategies and hierarchy
alterations, including member RENAME, are not enabled; native retained-bound
witnesses use admitted DROP/Restore while PostgreSQL also exercises real RENAME.

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

- Domain CHECK uses the admitted shared expression providers and conversion
  routes, including existing nested members; subqueries and non-VALUE variables
  are rejected. TRUE/UNKNOWN pass, FALSE reports domain 23514. Fresh NULL is
  checked, while already-domain NULL can bypass conversion in the documented
  outer-join/scalar-subquery contexts. Domain NOT NULL, inherited domain defaults
  and domain alteration remain unavailable. Composite arrays, builtin/catalog
  row execution and composite comparison/hash/key providers remain excluded.
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
- `aclitem` and `aclitem[]` are ordinary stored values, not authorization grants.
  Scalar comparison supplies only `=`; ACL arrays supply equality/inequality
  with shape, lower-bound and NULL-element semantics, but not ordering.
  ACL binary receive/send, catalog ACL projections other than the documented
  `pg_class.relacl`, `pg_database.datacl`, `pg_attribute.attacl` and
  `pg_namespace.nspacl`, GRANT extensions, role-rename SQL and new generic
  grouping/search/hash providers are not added.
  Native Dump requires live identities for every emitted ACL datum or definition
  literal and rejects stale identities before writer I/O. Restore remaps both
  principals to preexisting target roles; ordinary dropped-identity output and
  equality remain available. See [ACL transport](TYPES.md#acl-items).
- Range/multirange storage does not imply general constructors, casts,
  comparison/order/index providers, range arrays or user-defined range creation.
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
  lookup-only. Daterange/tsrange/tstzrange add no range_agg/range_intersect_agg
  providers. Tsrange/tstzrange scalar comparisons reject at analysis with42883,
  including domains,
  contextual unknown operands, NULL data and empty relations.
- Bit storage admits `bit_and`, `bit_or`, and `bit_xor` aggregates and selected
  scalar `bit_count`, `bit_length`, `length`, `octet_length`, `overlay`,
  `position`, and `substring`, including implicit VARBIT inputs. General bit
  casts, scalar operators, get_bit/set_bit, substr alias, comparison/index
  providers and bit arrays remain excluded. Unordered BIT
  aggregate DISTINCT retains the absent equality-provider error; BIT/VARBIT
  local ordering and bitwise windows remain excluded. Ordering by an independent
  integer key does not require bit comparison. Raw internal CHAR declarations
  remain excluded even where real catalog/query-derived values have codecs.
- XID/CID/XID8 are externally supplied scalar identifiers, not generated system
  attributes or integer/OID aliases. XID supports equality/inequality (including
  INT4 only on its right); CID only equality; XID8 unsigned comparisons/order.
  Explicit XID8→XID retains low32 bits. String I/O is admitted, not numeric/OID
  casts. Identifier companion arrays remain lookup-only; no system-column
  population, transaction-ID allocation or epoch synthesis is implied.
- JSONPath storage does not implement SQL/JSON query execution or GIN. JSON has
  no equality provider. Catalog type identities do not imply executable codecs.
- Scalar `IN`/`NOT IN` lists and scalar operators with array-expression
  `ANY`/`SOME`/`ALL` reuse existing scalar comparison providers; array forms
  additionally require a concrete associated array and executable array codec.
  SOME is ANY; every form returns BOOL with SQL NULL semantics.
  Empty lists are syntax errors, unlike empty-array ANY/ALL reductions.
  This does not introduce ordinary array-to-array comparison/order, custom
  operators, new element providers or general §9.19 array operators/functions.
  MONEY/INET/CIDR scalar-list comparisons do not enable their currently
  unsupported array elements or declarations.
- Integer/bigint `generate_series` executes through SELECT-target expansion
  and FROM function sources, including views, CTAS and INSERT SELECT.
  Numeric/temporal overloads inform genuine resolution and ambiguity but are
  not executable providers. INSERT VALUES expansion remains excluded.
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
- Selected scalar/set JSON processing includes `json_typeof`, `jsonb_typeof`,
  `json_array_length`, `jsonb_array_length`, `json_object_keys` and
  `jsonb_object_keys`, each taking its matching JSON family. Kinds/keys return
  TEXT; immediate array lengths return INT4. SQL NULL is strict (NULL scalars,
  empty key sets), distinct from JSON null. JSON keys preserve decoded duplicates
  and source order; JSONB keys use canonical UTF8-byte-length then byte order.
  Wrong-root length/keys use PostgreSQL English 22023 diagnostics with absent
  runtime Position. Key sets reuse target zip/padding and lateral FROM stages;
  scalar stored consumers and query key views retain symbolic calls on replay.
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
  omitted before key conversion. PostgreSQL 18.0 binary strict also omits
  converted root JSON null members after key/value conversion, retaining the
  latest nonnull duplicate; nested nulls are retained. All-omitted input yields
  an empty object; zero admitted rows yield SQL NULL.
  Adjacent operators/functions, generic JSON equality, SQL/JSON object aggregation
  and path/query execution remain excluded.
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
  An element that is itself array-valued through a domain retains the native
  `0A000: unsupported array element type` boundary at reached equality demand;
  PostgreSQL supports that equality, but native ordinary array comparison is
  excluded. Earlier NULL/empty function fast paths remain available.
  Other array operators/functions, ordinary equality outside ACL arrays,
  array ordering/hash, custom overloads and new element providers remain excluded.
- General array operators/functions and NAME-array comparison/order remain
  bounded. Aggregate DISTINCT/order on arrays and private `pg_node_tree`, and
  NAME-array/private-tree extrema, retain native capability exclusions.
- Enum-array results cannot be persisted by query-created tables or materialized
  views. Other admitted enum/domain-array carriers do not remove this exclusion.
- Ordered-set `percentile_cont`, `percentile_disc`, and `mode` support WITHIN
  GROUP sorting and FILTER, not OVER or hypothetical/custom aggregates.
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
  user-table/CAST routes or an invented pg_type catalog.
  Arrays, composite records, ranges/multiranges, bit strings and private trees
  do not acquire general ordering providers. ORDER BY USING, arbitrary collation
  providers, primary-key functional dependency and spill remain excluded.
- Temporal providers retain exclusions for timezone age/finite inquiries,
  epoch `to_timestamp` and the general TIME-to-INTERVAL cast. General named-function
  resolution and custom/generic optimizer-plan switching are not implemented.
- Recursive SELECT CTEs support UNION/UNION ALL worktable iteration and parent
  demand with existing type/equality providers. SEARCH/CYCLE, recursive modifying
  definitions, and general lateral derived sources remain excluded. MERGE rejects
  its own WITH RECURSIVE; a recursive SELECT inside its derived source is admitted.
  Window providers are row_number(), count(*) and
  existing integer/numeric/floating sum(expr); MONEY/INTERVAL SUM, expression-count
  windows, DISTINCT/call-local aggregate ORDER and window aggregate filters remain excluded.
- MERGE supports RETURNING expressions, source/target rows, OLD/NEW aliases and
  lexical `merge_action()` output. Plain star is source-first; target defaults
  to NEW for INSERT/UPDATE and OLD for DELETE, with absent sides SQL NULL.
  Output is eagerly evaluated per accepted action and published atomically.
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
- `pg_tablespace` is a public readonly builtin-only projection:
  `(1663,pg_default,10,NULL)` and `(1664,pg_global,10,NULL)` for named columns
  `oid,spcname,spcowner,spcoptions` (OID/NAME/OID/TEXT[]). Owner 10 is the canonical
  Engine principal, independent of bootstrap role renaming. `spcacl` is omitted,
  not misrepresented as TEXT[]: the additional catalog ACL column/provider is
  outside the ACL item value scope.
  Supported wire origins retain relation 1213 and attributes 1,2,3,5.
  `pg_tables` presents this shared catalog in `pg_global`; that metadata is not
  a physical storage mechanism.

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
absence of system rows remain exact. No information-schema domain codecs are added.
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
with driver metadata, NULL transport through ordinary projections/subqueries,
and input to the six selected array shape inspectors, producing concrete scalar
NULLs without inferring a leaf type.
It remains non-concrete and unavailable as a declared storage, domain, parameter,
cast or generic comparison/ordering type. Catalog mutation and row locks retain
their native `0A000` exclusions. See [selected proof and smoke](../reference/catalogs/README.md#pg_attribute-selected-proof).

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
it neither expands pg_proc SQL rows nor admits execution, CREATE FUNCTION or
CREATE PROCEDURE. Runtime-allocated information_schema/plpgsql routines are an
unavailable native catalog capability, not fabricated fixed OIDs. Schema GRANT/
REVOKE remains unavailable SQL; tests explicitly use native schema ownership
transfer versus PostgreSQL schema USAGE to establish authority. Native ABI,
external procedural runtimes and PostgreSQL physical persistence are not supplied
by routine alias lookup. See [the evidence note](design/regproc-pg18-oracle.md).
REGOPER/REGOPERATOR provide the complete released builtin operator identity
inventory, exact signature lookup and captured symbolic output, including
lookup-only operand type identities. This does not add execution providers,
CREATE/ALTER/DROP OPERATOR, a native pg_operator projection, to_regoper/
to_regoperator, or arbitrary implementation-function execution. Quoted custom
operator DDL/catalog/dependency mechanisms are explicitly PostgreSQL-only
reference phases; native algorithm units cover quoted output and path masking.
REGCONFIG/REGDICTIONARY scalar numeric identities are executable, with lookup-only
companion arrays. Symbolic REGDICTIONARY resolution uses only actual installed
unaccent objects; native does not fabricate builtin `simple`/language dictionaries.
There is no native configuration provider: symbolic REGCONFIG input explicitly
reports `0A000: text search configuration name lookup is not supported`, rather
than claiming a PostgreSQL object is missing. Numeric and `-` config input bypass
that provider boundary. General text-search DDL, full-text execution,
to_regconfig/to_regdictionary and two-argument unaccent remain excluded. Custom
dictionary DDL and schema-USAGE grants in reference scenarios are genuine
PostgreSQL-only mechanisms; native tests use existing ownership/private ACL seams.
Conversion utilities retain the native explicit-transaction DDL exclusion at
Execute, not Prepare. Read-only execution reports `25006` before that exclusion
or object resolution. DROP retires the actual conversion provider atomically;
subsequent statements select fresh defaults while returned results retain theirs.

Index providers are not a general PostgreSQL optimizer or physical access-method
implementation. GiST, SP-GiST, GIN and extension-backed providers are not implied
by logical B-tree/HASH capabilities. HASH retains one-key/no-INCLUDE limits and
excludes standalone partial indexes. Partial concurrent indexes and concurrent
DROP/REINDEX are unavailable. Native index metadata disclosure requires schema
visibility plus readable parent-table authority, unlike PostgreSQL's unfiltered
summary view. See [native index controls](DRIVER.md#b-tree-utility-preparation-and-controls).
