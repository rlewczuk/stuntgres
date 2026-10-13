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

## Storage and native controls

Databases and prepared transactions are process-local. There is no WAL, crash
recovery, physical backup, replication, subscription execution or physical
checkpoint service. Native sequence `log_cnt` is always zero; cache state does
not promise crash durability. Native controls are logical fixture replacement,
not arbitrary `pg_dump` import or PostgreSQL physical backup.

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
role creation, passwords and membership. Arbitrary explicit ACLs and invalid
indexes cannot be serialized and are rejected rather than silently discarded.
Restore has a restricted parameter-free replay boundary, not general script
execution. It does not import temporary objects, transaction controls or shared
role effects. See [fixture contracts](FIXTURES.md).

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

## Tables, hierarchies and materializations

Partition hierarchy update, delete, row locking, alteration and explicit hierarchy
locking remain excluded. Conflict arbitration and MERGE cannot target partition
hierarchies. Temporary partition definitions and partition definitions nested in
schema creation are unavailable. Nonunique virtual partition indexes and partition
reindexing are unavailable; partition key/constraint combinations remain bounded.
Recursive inheritance update/delete/locking and graph-shape alterations remain
excluded. Copying a temporary-sequence default into a permanent table is unavailable.

Materialized views do not provide native indexes or concurrent refresh. Explicit
transaction refresh and refresh as Restore input remain excluded. Trigger, pending
trigger, foreign-table/FDW and procedural-language execution are unavailable.
Truncation does not provide identity restart.

Adding generated columns and changing their destination type without an explicit
conversion remain excluded. Direct typed-table creation cannot add identity or
generated options. Primary-key creation retains deferrability and temporal-key
exclusions; ordinary key-list attachment through table alteration is not general
primary-key creation. Mixed default-setting/removal action lists remain excluded.

## COPY streams

COPY requires the [pinned native stream helpers](DRIVER.md#pinned-copy-text-streams--change-note),
not ordinary database/sql execution or prepared COPY. File and program endpoints
never perform external I/O. Encoding overrides, freezing, row filtering and
error-skipping/logging options remain excluded. Legacy CSV option spellings are
outside the modern CSV implementation. Physical export of a virtual partition
root is unavailable. Binary providers do not add storage, cast, DDL or catalog
mutation capabilities; `pg_node_tree` binary input is excluded.

## Types, expressions and catalogs

- Domain constraints, inherited domain defaults and domain alteration are
  unavailable. Composite arrays, constrained composite domains, builtin/catalog
  row execution and composite comparison/hash/key providers remain excluded.
- Range/multirange storage does not imply general constructors, casts,
  comparison/order/index providers, temporal range storage, range arrays or
  user-defined range creation. Temporal multiranges and multirange arrays remain
  lookup-only.
- Bit storage admits `bit_and`, `bit_or`, and `bit_xor` aggregates, including
  implicit VARBIT inputs, but does not provide general bit casts, scalar
  operators/functions, comparison/index providers or bit arrays. Unordered BIT
  aggregate DISTINCT retains the absent equality-provider error; BIT/VARBIT
  local ordering and bitwise windows remain excluded. Ordering by an independent
  integer key does not require bit comparison. Raw internal CHAR/XID declarations
  remain excluded even where real catalog/query-derived values have codecs.
- JSONPath storage does not implement SQL/JSON query execution or GIN. JSON has
  no equality provider. Catalog type identities do not imply executable codecs.
- General array operators/functions and NAME-array comparison/order remain
  bounded. Aggregate DISTINCT/order on arrays and private `pg_node_tree`, and
  NAME-array/private-tree extrema, retain native capability exclusions.
- Enum-array results cannot be persisted by query-created tables or materialized
  views. Other admitted enum/domain-array carriers do not remove this exclusion.
- Temporal providers retain exclusions for timezone age/finite inquiries,
  epoch `to_timestamp` and the general TIME-to-INTERVAL cast. General named-function
  resolution and custom/generic optimizer-plan switching are not implemented.
- Recursive CTEs, modifying CTE bodies, top-level CTE DML and general lateral
  derived sources are excluded. Window providers remain limited; frame
  exclusions, expression-count windows and window aggregate filters are excluded.
- MERGE retains exclusions for returning rows, no-op actions, query/multiple-row
  insertion actions, partial insertion targets and writable target views.
  Positioned mutation of a view is unavailable.
- Catalog sources are bounded projections, not complete `pg_type`,
  `pg_attribute`, `pg_index`, `information_schema` or authentication catalogs.
  Native virtual catalog mutation and row locking are unavailable. Installed
  extension metadata does not provide arbitrary loading, installation or updates.
- `pg_tablespace` is a public readonly builtin-only projection:
  `(1663,pg_default,10,NULL)` and `(1664,pg_global,10,NULL)` for named columns
  `oid,spcname,spcowner,spcoptions` (OID/NAME/OID/TEXT[]). Owner 10 is the canonical
  Engine principal, independent of bootstrap role renaming. `spcacl` is omitted,
  not misrepresented as TEXT[]: native ACL item carriers are unavailable.
  Supported wire origins retain relation 1213 and attributes 1,2,3,5.
  `pg_tables` presents this shared catalog in `pg_global`; that metadata is not
  a physical storage mechanism.

## Collation, encoding and indexes

Collation execution uses deterministic C/POSIX and registered fixed builtin
providers. Arbitrary libc locale data, ICU, nondeterministic equality and fallback
providers are unavailable. Unicode-collation ILIKE retains its native exclusion;
ASCII behavior must not silently stand in for that provider. Encoding conversion
uses the release registry's direct pairs, not arbitrary pathfinding or an implicit
UTF8 pivot; the database encoding remains UTF8.

Index providers are not a general PostgreSQL optimizer or physical access-method
implementation. GiST, SP-GiST, GIN and extension-backed providers are not implied
by logical B-tree/HASH capabilities. HASH retains one-key/no-INCLUDE limits and
excludes standalone partial indexes. Partial concurrent indexes and concurrent
DROP/REINDEX are unavailable. Native index metadata disclosure requires schema
visibility plus readable parent-table authority, unlike PostgreSQL's unfiltered
summary view. See [native index controls](DRIVER.md#b-tree-utility-preparation-and-controls).
