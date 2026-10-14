# stuntgres

stuntgres is a process-local, embedded PostgreSQL-compatible Go `database/sql`
driver for unit tests and reusable test fixtures. It lets Go developers exercise
SQL-backed code without starting a database server or container. Production code
uses only the Go standard library. Requires **Go 1.26 or later**.

It implements a deliberately bounded SQL subset, not a complete PostgreSQL server.
The embedded driver remains process-local. The optional `cmd/stuntgres` executable
exposes the same engine through PostgreSQL simple-query protocol, with password
authentication and isolated in-memory databases. Native SQL dumps provide explicit
save/restore, not WAL or crash durability. Use real PostgreSQL for features outside
documented [native compatibility limits](docs/DIALECT.md). Consult the
[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html) for SQL syntax.

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

## Phase 1 server — change note

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
SQLSTATE, primary message, empty Phase1 parameter list, and `SYNTAX`,
`UNSUPPORTED` or `SQL` classification. Required log failures are visible errors.

Protocol3.0 supports whole-input simple-query batches, typed text rows, command
tags, notices, settings status, transactions, cancellation and session cleanup.
Engine-owned transaction/idle-session timeouts deliver FATAL and close the socket,
without a ReadyForQuery, including expiry while no frontend query is arriving.
Passive peer-shutdown monitoring keeps disconnect cleanup live even when the
frontend queue is full: Linux uses epoll, BSD uses kqueue, and Windows uses
FD_CLOSE plus TCP_INFO (Windows10 1703/Server2016 or a compatible provider).
Other platforms/providers log an explicit warning and retain ordinary EOF-based
networking without claiming that passive queued-data monitor. Linux runtime paths
are exercised; non-Linux runtime/ACL proof requires those platform runners.
Wire parameters/prepared statements/portals, FunctionCall, COPY streams and TLS
are explicit capability errors, not fake successes. Engine-supported SQL
`PREPARE`/`EXECUTE` and ordinary SQL cursors remain distinct supported SQL features.
`server_version=18.0` is negotiation metadata, not a full PostgreSQL18 claim.
Dual-mode `TestReferenceServer*` definitions use real subprocesses, actual psql
and sqlmw-wrapped pq alongside pinned PostgreSQL18.0; see
[server verification](reference/sessions/README.md#phase-1-server-selected-proof).

Change note: the selected §9.19 array functions `array_position`,
`array_positions`, `array_remove`, `array_replace` and membership operators
`@>`, `<@`, `&&` reuse the existing typed array and leaf equality providers.
Search/removal/replacement match NULL elements; membership never matches them.
Position uses actual subscripts and an inclusive optional start. Search/removal
require one dimension, while replacement/membership accept rectangular arrays;
containment ignores dimensions, ordering and duplicate multiplicity.
Direct/prepared/held and supported stored-expression routes preserve metadata,
all-child privileges and demand-driven provider/collation errors. This does not
enable general array comparison/order, other §9.19 functions or new leaf providers.
Array-valued domain elements retain an explicit native `0A000` boundary when
equality is demanded; earlier NULL/empty/search fast paths are preserved.
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
§9.25 scalar reduction, distinct from existing §9.24 subquery membership; it
does not add ordinary array-to-array comparison/order or the §9.19 inventory.
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
same typed provider. SQL/JSON `json_objectagg`, unique variants and aggregate
window execution remain outside this support.
Feature smoke: `make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$'`
and `make check RUN='^TestReferenceJSONObjectAggregateFeatureSmoke$' REFERENCE=1`.

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

Change note: `TRUNCATE [TABLE]` atomically empties named tables, ordinary
inheritance descendants and supported RANGE partition leaves, with `ONLY`,
`RESTRICT`/`CASCADE` and unchanged `CONTINUE IDENTITY` state. It uses exact
TRUNCATE rights and transaction-owned ACCESS EXCLUSIVE locks, preserves table
and index definitions, and participates in rollback/savepoints. Stable snapshots
have PostgreSQL's documented non-MVCC TRUNCATE exception; already exported native
Rows remain owned buffers, while a live SQL cursor producer rejects truncation.
Other sessions' temporary inheritance branches are excluded without replacing
their storage or acquiring descendant AX locks; own-session TEMP children remain
included.
RESTART IDENTITY, native triggers/FDW and partition foreign keys are not added.
See [TRUNCATE](https://www.postgresql.org/docs/18/sql-truncate.html) and
[transaction semantics](docs/TRANSACTIONS.md#whole-table-generation-replacement).
Feature smoke: `make check RUN='^TestReferenceTruncateFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceTruncateFeatureSmoke$'`.

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
through `oid,spcname,spcowner,spcoptions`; unsupported `spcacl` is omitted.
WITH is recognized but cannot apply or validate options without physical creation.
See [storage and catalog limits](docs/DIALECT.md#storage-and-native-controls).
Feature smoke: `make check RUN='^TestReferenceTablespaceFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceTablespaceFeatureSmoke$'`.

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
are autocommit-only. DROP OWNED preserves the database but cleans its CREATE ACL.
REASSIGN preserves creator-keyed default privileges; DROP OWNED removes selected
creators' actual policies and selectively cleans selected recipients in others'
policies. Tablespace providers remain absent.
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

Change note: INET and CIDR now store typed IPv4/IPv6 scalar values through
direct/prepared binds, metadata and native Dump/Restore. INET preserves host
bits; CIDR input rejects them, while INET-to-CIDR casts clear them. Canonical
output and prefix-aware ordering distinguish network values from TEXT.
Network arrays, helper functions and containment/arithmetic operators remain
outside this scalar feature. See [network types](https://www.postgresql.org/docs/18/datatype-net-types.html).
Feature smoke: `make check RUN='^TestReferenceINETFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceINETFeatureSmoke$'`.

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

Change note: all sixteen PostgreSQL18 predefined role identities are seeded.
Live INHERIT membership supplies real read/write/schema/sequence capabilities;
`pg_maintain` uses substantive REINDEX and transaction-owned LOCK exclusion.
`pg_database_owner` is implicit for the live canonical attached-database owner and
owns canonical public, whose historical PUBLIC USAGE|CREATE default is retained.
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
and generated definitions exported before a real ALTER TABLE OF. CREATE requires
source USAGE; PostgreSQL18 attachment does not. A target-lock wait can select a
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
Recursive modifying targets, ordinary LOCK and graph-shape ALTER retain their
explicit native capability boundaries.
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
Anonymous constructed results report RECORD; composite arrays and comparisons
remain unsupported.
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
PostgreSQL18 OLD/NEW aliases and `merge_action()` (`INSERT`, `UPDATE`, `DELETE`).
Only accepted actions emit rows. Unqualified target values use NEW for INSERT/
UPDATE and OLD for DELETE; OLD is absent on INSERT and NEW on DELETE. Absent
whole rows are SQL NULL, not present composites with all-NULL members.
Plain `RETURNING *` expands source columns before target columns. Action text
is lexical to MERGE RETURNING, including its expression subqueries, and is not
an ordinary callable function. Output is eagerly completed after each accepted
action, before the next proposal, and exposed only after atomic publication.
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
Triggers, rules, FDWs, target hierarchy writes, writable CTEs, procedural
functions and COPY(MERGE) remain external or adjacent mechanisms.
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
held counts, while Dump excludes live ownership. Transaction-level advisory calls
and lock introspection remain unsupported. See
[session advisory locks](https://www.postgresql.org/docs/18/functions-admin.html).
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
SQL replay on PostgreSQL. Other hierarchy mutation and external storage mechanisms
remain excluded. See [partition graphs](docs/TABLES.md#inheritance-typed-attachments-and-partition-graphs).
Feature smoke: `make check RUN='^TestReferencePartitionAttachFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePartitionAttachFeatureSmoke$'`.

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
Read-only `pg_conversion` exposes its eight actual columns, including derived
REGPROC `conproc` output and OID/TEXT inspection casts. This does not add general
REGPROC input/storage, CREATE FUNCTION, external ABI loading or wire transcoding.
Dump emits exact quoted definitions and collision-safe seed metadata deltas,
including legal duplicate-default replay. Reset restores canonical seeds and
Restore remaps actual owners/providers while preserving live target TEMP.
Conversion DDL retains the native autocommit-only explicit-transaction boundary.
Smoke: `make check RUN='^TestReferenceConversionFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceConversionFeatureSmoke$' REFERENCE=1`.

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

Change note: selected PostgreSQL18 windows support `row_number()`, `count(*)`
and `sum(expr)` with default or explicit RANGE/GROUPS/ROWS frames, typed offsets, partition/order keys
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
integer/numeric/real/double providers; INTERVAL, window FILTER, count(expr),
DISTINCT, call-local aggregate ORDER and other window providers remain excluded.
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
DATE selects TIMESTAMPTZ; TIME converts to INTERVAL only within this selected
call (24:00 remains zero days and24hours). See
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
or bit casts/operators/arrays are introduced. See
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
PREPARE; closed/failed-after-recovery/prior exported entries do not. Notifications
and snapshot export remain excluded. Nonholdable cursors close on detach while
exported HOLD stores survive. See [prepared transactions](docs/TRANSACTIONS.md#prepared-transactions).
Feature smoke: `make check RUN='^TestReferencePreparedTransactionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePreparedTransactionFeatureSmoke$'`.

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
Feature smoke: `make check RUN='^TestReferenceRegtypeFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceRegtypeFeatureSmoke$'`.

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
on successful atomic rebuild. Dump refuses persistent invalid indexes before
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
digits. JSON operators, unselected JSON functions and SQL/JSON constructors remain excluded.
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
retain symbolic calls. JSON remains noncomparable; SQL/JSON constructors and
generic builtin catalog enumeration are unchanged.
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
Composite arrays, arbitrary user JSON casts, SQL/JSON constructors and processing
operators/aggregates are not added by this change.
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
This does not add `jsonb_object_keys`, other JSON processing functions or general
lateral derived queries. See [JSON processing](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING).
Feature smoke: `make check RUN='^TestReferenceJSONKeysFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONKeysFeatureSmoke$'`.

Change note: JSONB storage normalizes whitespace and key order, keeps the last
decoded duplicate key, and preserves exact finite decimals and their display
scale. It rejects numeric overflow, zero Unicode escapes and invalid surrogate
pairs. SQL NULL differs from JSON null. Ordinary comparison, grouping, B-tree
keys, JSON↔JSONB IO casts, metadata and logical native Dump/Restore share the
existing typed providers; JSON remains source-preserving and noncomparable.
See [JSONB types](https://www.postgresql.org/docs/18/datatype-json.html).
Feature smoke: `make check RUN='^TestReferenceJSONBFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONBFeatureSmoke$'`.

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
rules still apply. No adjacent JSON operators, constructors, general SQL/JSON
execution or generic callable catalog is added.
See [JSON processing](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING).
Feature smoke: `make check RUN='^TestReferenceJSONTypeFeatureSmoke$'`;
pinned counterpart: add `REFERENCE=1` to the identical selector.

Change note: numrange columns extend the common range representation with exact
continuous NUMERIC endpoints. `(4,5)` remains nonempty; `[1.0,1.00]` preserves
both scales, and `[1.0,1.00)` becomes empty. Explicit numeric Infinity/NaN
remain distinct from omitted unbounded endpoints. Direct/prepared text/byte
binds, NUMRANGE metadata, full atomic diagnostics and native/domain Dump/Restore
reuse existing ownership and staging. Constructors/operators/casts/arrays and
temporal ranges remain outside this feature.
See [continuous numeric ranges](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceNumericRangeFeatureSmoke$'`;
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
ownership/staging. No datemultirange, timestamp ranges, arrays, constructors,
casts, operators, comparison/index providers or new range aggregates are added.
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
Dump/Reset/Restore reuse existing ownership; no operators, casts, arrays or SQL
comparison/index providers are added. Scalar comparisons reject at admission
with42883, including plain/nested domains; they never treat unequal unions as equal.
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
No scalar range comparison/order/group provider, DISTINCT support, window provider,
temporal family, constructor, operator or cast is added.
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
explicit capability boundaries. No general bit casts, operators, scalar
functions, comparison/index providers or arrays are added.
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
No constructors/operators, new casts, range arrays or
comparison/index providers are added.
See [integer range columns](https://www.postgresql.org/docs/18/rangetypes.html).
Feature smoke: `make check RUN='^TestReferenceIntegerRangeFeatureSmoke$'`;
pinned counterpart: `make check RUN='^TestReferenceIntegerRangeFeatureSmoke$' REFERENCE=1`.

Change note: JSONPath (OID4072, array4073) now has typed literal/bind IO,
full PostgreSQL18 path grammar and input-time ARE/ERE/BRE regex validation.
An immutable parsed representation retains mode, expressions and accessors;
output is canonical, not evaluated. INSERT/UPDATE destinations and explicit
CAST infer bound path types before import. SQL NULL differs from path `null`.
Native metadata is JSONPATH/string; lib/pq reports an empty type name and
interface{} ScanType, with raw bytes. Defaults, views, arrays/domains and native
Dump/Restore reuse existing providers. JSON query functions/operators, path
evaluation, regex matching and GIN remain excluded.
See [JSONPath typed IO](https://www.postgresql.org/docs/18/datatype-json.html).
Feature smoke: `make check RUN='^TestReferenceJSONPathFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'`.



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
Domain constraints, FK/constraint triggers and PostgreSQL physical storage,
WAL and external pg_dump mechanisms are not implemented; network access uses the
bounded [Phase 1 server](#phase-1-server--change-note).
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

Change note: `CREATE DOMAIN [schema.]name [AS] base_type [NULL]` declares a
catalog-owned logical type over a supported scalar or array base.
Domain columns, direct/prepared input, inherited base typmods, operators/functions
and explicit assignment/cast-back retain the distinction between logical identity
and physical codecs. `DROP DOMAIN [IF EXISTS] ... [RESTRICT | CASCADE]` follows
real type/column/stored-expression dependencies: CASCADE removes dependent columns
and views, not unrelated tables or surviving rows. Native Dump/Restore preserves
qualified declarations, chains, owners and data. Domain DEFAULT/CHECK/NOT NULL,
ALTER DOMAIN, array operators/functions, type ACL-management
SQL and transactional domain DDL remain excluded. See [domains](https://www.postgresql.org/docs/18/domains.html).
Feature smokes:
`make check RUN='^TestReferenceDomain(BaseValues|Expressions|Metadata|Arrays)$'`,
`make check RUN='^TestReferenceDomain(Diagnostics|Dependencies|PreparedLifecycle|NativeControls)$'`,
and pinned `make check REFERENCE=1 RUN='^TestReferenceDomain'`.

Change note: the predefined `information_schema.time_stamp` domain accepts
supplied timestamptz values and explicit/bound NULL through the authoritative
catalog/domain path. It retains distinct qualified identity, rounds to precision2,
exports TIMESTAMPTZ metadata and survives native Dump/Reset/Restore.
PostgreSQL's inherited `DEFAULT current_timestamp(2)` remains outside native
support: omitted values use a column default or typed NULL. No sibling-domain,
implicit `_time_stamp` array storage or general information-schema view support
is implied. See [domain contracts](https://www.postgresql.org/docs/18/domains.html).
Feature smoke: `make check RUN='^TestReferenceInfoTimeStampFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceInfoTimeStampFeatureSmoke$'`.

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
Policy expressions remain metadata, not an implemented FILTER/CHECK evaluator.
Native ENABLE/DISABLE/FORCE/NO FORCE ROW LEVEL SECURITY now persist independent
table flags, enforce owner/direct-superuser/direct-BYPASSRLS bypass and real
default deny when no applicable permissive expression can grant the required
use. DISABLE retains definitions; dropping the last policy retains both flags.
Applicable expressions (including TRUE/FALSE/NULL) instead raise Execute `0A000`
`row-security policy expression enforcement is unsupported`, not an invented
allow/deny result. See the [BASE contract](docs/AUTHORIZATION.md#row-security-metadata-and-enforcement-limits).
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
Sequence/other-object direct grants and native explicit-transaction
object utilities remain excluded; explicit ACL Dump fails before output.
Feature smoke (900 seconds each):
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.
Selected revoke smoke:
`make check RUN='^TestReferenceRoleRevokeFeatureSmoke$'`;
`make check REFERENCE=1 RUN='^TestReferenceRoleRevokeFeatureSmoke$'`.

Change note: `ALTER DEFAULT PRIVILEGES` stores global and schema-scoped defaults
for TABLES, SEQUENCES, FUNCTIONS/ROUTINES, TYPES, SCHEMAS and LARGE OBJECTS.
Only subsequently created supported tables/views/materialized views, sequences,
standalone types and explicit schemas consume them. No routine or large-object
creation provider is introduced. Creation uses its actual effective owner, not
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
Membership never synthesizes summary flags. Rename, role settings and
`pg_authid`/`pg_shadow` remain excluded. Object GRANT/REVOKE supports the selected
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
Minimal `pg_catalog.pg_constraint` and `pg_catalog.pg_attrdef` projections expose
real definition identities and immutable structural trees. Compact/pretty
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
in PostgreSQL's BYTEA/BIT overload competition; selected BIT overloads remain an
explicit native capability boundary, not BYTEA reinterpretation.
See [BYTEA access](https://www.postgresql.org/docs/18/functions-binarystring.html).
Feature smoke: `make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'`;
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
Positional TEXT substring and encoding-aware length,
BYTEA DEFAULT functions and direct byteacat remain excluded; the three-TEXT
SQL-SIMILAR and two-TEXT POSIX substring overloads are supported. No new cast edges or byteapos alias are added.
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
row and associated-array catalog identities are supported; composite value
execution remains excluded.
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
NULLS FIRST/LAST, owned built-in COLLATE/operator classes and optional
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
events. GiST/SP-GiST, range/extension providers, physical indexes/WAL and server
pg_dump are not introduced. See [the contract](https://www.postgresql.org/docs/18/ddl-constraints.html)
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
supported; CREATE inheritance works, but graph default ALTER remains an explicit boundary.
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
projection and owner/invoker authority; trigger/rule-based writes remain excluded. View-write smoke:
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
INSTEAD rules/triggers remain external provider boundaries, not native no-ops.
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
ownership on SQL replay. General FUNCTION/text-search DDL, regdictionary/internal
SQL codecs, ALTER/DROP/UPDATE EXTENSION and arbitrary/native-ABI loaders remain
excluded. See [registered extensions](https://www.postgresql.org/docs/18/sql.html)
and [selected proof](reference/catalogs/README.md#registered-extension-selected-proof).

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
For bounded predicate syntax, project an inquiry in a view and filter its BOOL
column; INSERT results are checked by a subsequent SELECT, while UPDATE/DELETE
support RETURNING. Discover backend-local numeric relation identities by projecting
`oid,pg_table_is_visible(oid)` from pg_class under each target search_path and
requiring exactly two distinct positive candidate OIDs, one visible candidate,
and the same identity bag after reversal. No pg_namespace query source or
explicit regclass-to-oid cast is needed.
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
state is empty; actual registered `unaccent` installations supply descriptor-derived
rows. No PL/pgSQL installation is fabricated. PostgreSQL18.0's default database
independently has installed `plpgsql`1.0; available control files are not installed rows.
Normal REGCLASS/search_path/prepared queries and native Dump/Reset/Restore use
the existing relation authority. Fixed relation3079 differs from the reused
allocated native rowtype10074/array10073; no cross-server type allocator promise,
physical system index or TOAST implementation is implied.
Registered unaccent CREATE is supported; unavailable providers fail0A000 at
execution, including after successful Prepare. ALTER/DROP EXTENSION and module
loading retain their unsupported boundaries.
See [exact shape, installed authority and errors](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferencePGExtensionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGExtensionFeatureSmoke$'`.

**Available-extension metadata change note:** `pg_catalog.pg_available_extensions`
and `pg_catalog.pg_available_extension_versions` are readonly PUBLIC SELECT views
over the same immutable embedded registry used by installation. The native
universe is exactly genuine unaccent1.1, with its released comment and
superuser/trusted/relocatable flags; hstore, file_fdw and plpgsql are not invented.
Default/available version and actual installed version are distinct. Control
schema/dependencies remain NULL for unaccent even when installed elsewhere.
Metadata is not filtered by member namespace privileges or TEMP ownership;
ordinary relation admission and SQL predicates still apply.
Both availability views and `pg_extension` share one caller-visible installed-state
frontier per statement, including joins and recursive inputs; peer publication
affects the next execution, not later occurrences in the same statement.
See [fields, visibility and controls](https://www.postgresql.org/docs/18/catalogs.html).
Feature smoke: `make check RUN='^TestReferenceAvailableExtensionsFeatureSmoke$'`;
add `REFERENCE=1` for the pinned counterpart.


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
honor the selected builtin identity. Adjacent regex functions and
positional TEXT substring remain excluded.
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
Eight public PostgreSQL18 regular-expression functions are supported:
`regexp_count`, `regexp_instr`, `regexp_like`, `regexp_match`, `regexp_matches`,
`regexp_replace`, `regexp_split_to_array` and `regexp_split_to_table`.
All released overloads share the same provider; captures are owned `TEXT[]`
values, and start/index arguments count UTF8 characters without slicing away
anchor/lookbehind context. The two set functions compose with `string_to_table`
through one shared target/FROM owner, including mixed lockstep peers, nested
expansion, `ROWS FROM`, `WITH ORDINALITY` and implicit/explicit LATERAL.
Bare scalar attempt timing, typed padding, lexical frames and lock rechecks
retain their distinct contracts. See [function details](https://www.postgresql.org/docs/18/sql.html).
`regexp_substr`, SQL XQuery regex functions and general LATERAL derived/VALUES
sources remain excluded.
Existing non-regexp array ordering/opclass and constrained-domain DDL limits
remain explicit; array-valued regex results retain table/default/generated/
CHECK, grouping, scalar-key index-predicate and unordered-query consumers.
Function-suite smoke: `make check RUN='^TestReferenceRegexpFeatureSmoke$'`;
the pinned counterpart adds `REFERENCE=1`.
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
remain forbidden clauses. Runtime INTERVAL SUM and windows remain excluded;
typed INTERVAL SUM in RETURNING retains42803. Public feature smoke:
`make check RUN='^TestReferenceNumericSumSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceNumericSumSmoke$'`.
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
is restricted to row_number(), count(*) and existing integer/numeric/floating SUM;
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
make finalcheck # after merge: startup benchmark, full native race suite and dependencies
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
runs generation, startup benchmark, full Docker-free native race tests and
dependencies under the agent's external 1800-second deadline. Earlier task-specific
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
Proven FALSE/BOOL NULL consumers skip opaque CTE bodies, including their internal
locks. Existing native stored marked aggregate/blocking frontiers still run when
independently reachable; dead qualification does not bypass their physical lock
settlement.

Recursive/SEARCH/CYCLE, modifying CTE bodies, top-level WITH DML, standalone
VALUES/TABLE bodies and lateral derived queries remain excluded. See
[WITH query contracts](https://www.postgresql.org/docs/18/queries.html) and
[implementation design](docs/design/PG18-QUERY-CTE.md).
Feature smoke: `make check RUN='^TestReferenceQueryCTEFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceQueryCTEFeatureSmoke$'`.
