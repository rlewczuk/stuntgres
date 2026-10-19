# Native compatibility limits

[README](../README.md) · [Driver contracts](DRIVER.md) · [Fixture controls](FIXTURES.md) · [Development](DEVELOPMENT.md)

[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/) is the authority
for SQL syntax and standard semantics. Full PostgreSQL compatibility is the goal,
not a claim about the current engine. The boundaries below describe native
implementation limits, not a supported-syntax inventory. A recognized construct,
catalog identity or stored value does not imply that every operation on it exists.

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
make a routine callable or add large-object storage. COPY uses the streaming
executor described [below](#copy-streams). Committed notifications are delivered
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
Partitioned-index creation remains unsupported, not a new ALTER prerequisite.
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

Foreign writes, row locks, index/UNIQUE/PK/FK/trigger/truncation callbacks,
hierarchy expansion and arbitrary remote pushdown are unsupported. No generic
has_server_privilege/has_foreign_data_wrapper_privilege SQL execution is added
merely because static PostgreSQL identities exist. Actual GRANT/REVOKE and common
authorization/stored-ACL inquiries use the new descriptor classes. Dump rejects
user SQL/MED metadata before bytes; ordinary Reset/Restore retain installed
callables, and foreign replay is explicitly rejected atomically. See
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
Valid LANGUAGE and LARGE OBJECT targets retain class-specific unavailable native mechanisms:
`0A000 LANGUAGE privileges are unsupported in embedded mode`, and the analogous
upper-case LARGE OBJECT family prefix. Invalid privilege masks, malformed syntax
and actual role/grantor diagnostics remain separate.

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

Valid plain, option-only and CASCADE language/large-object revocations retain the
same native unavailable errors above. ALTER SYSTEM still has no native file-write
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
still supports metadata ACLs without a direct-call provider. No routine creator,
security-definer execution frame, implicit arbitrary codec permission gate or
missing provider is introduced.

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

Policy definitions and row-security flags do not provide policy-expression
enforcement. Applicable policy expressions fail explicitly instead of being
ignored. Operating-system file/program access, PostgreSQL PID signaling and
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
it does not enforce policies, require table SELECT, or add a `row_security`
setting. Relation-name schema access remains separate. See
[authorization inquiry contracts](AUTHORIZATION.md#membership-provenance-and-publication).

`pg_get_acl(classid oid, objid oid, objsubid integer) -> aclitem[]` is a strict
STABLE inquiry into **stored** catalog ACLs, not effective permissions, inherited
rights or synthesized defaults. Direct/newly prepared/held calls share the typed
expression path; held calls borrow current execution-time catalog and authority.
No SELECT privilege on the queried object is required. Name casts such as
REGCLASS still use their normal schema/name resolution.

Supported existing descriptors expose relation, namespace, type, procedure,
database, foreign-wrapper and server ACLs. Recognized classes without native
instances produce missing-object NULLs; no language/large-object ACL is fabricated.
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
query or its modifiers. Recursive modifying definitions, trigger and
foreign-write/hierarchy provider exclusions remain unchanged; admitted local
consumers may read real foreign SELECT sources. Rule expansion must leave exactly one
admitted auxiliary query; zero/multiple expansions retain the documented
INSTEAD NOTHING/conditional/ALSO/multi-statement restrictions. A single SELECT
replacement remains lazy when unused. No portable same-row overlap winner or
sibling execution order is promised. See [query ownership](QUERIES.md) and
[PostgreSQL restrictions](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-MODIFYING).


## Tables, hierarchies and materializations

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
RANGE keys require an admitted btree ordering provider. Native integer-array
keys currently retain the existing missing-default-operator-class boundary;
ordinary array storage does not add a partition ordering provider.

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
remain nonrecursive. Ordinary LOCK, changing attachment with INHERIT/NO INHERIT,
provider-CASCADE graph-shape changes and new partition ALTER families remain
excluded. UPDATE DEFAULT, explicit-transaction SET/DROP DEFAULT, explicit ADD
NOT NULL NO INHERIT and NOT ENFORCED modifiers are not introduced.
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
trigger, foreign-write callbacks and procedural-language execution are unavailable.
TRUNCATE RESTART IDENTITY resets owned sequences transactionally to configured
START, including selected virtual roots and supported inheritance/CASCADE closure.
It does not reset unowned default references, undo ordinary nextval/setval
allocations, clear local currval/lastval on rollback, or enable trigger/FDW/
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
parent order starts at one; partition and index edges use one. Index edges
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
Encoding overrides, freezing, row filtering and error-skipping/logging options
remain excluded. Legacy CSV option spellings are outside the modern CSV
implementation. Physical export of a virtual partition root is unavailable.
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
HASH bounds use `{PartitionBound strategy hash default false modulus N remainder R}`.
Index partitions have NULL bounds. LIST and additional hierarchy alterations,
including member RENAME, are not enabled; native retained-bound witnesses use
admitted DROP/Restore while PostgreSQL also exercises real RENAME.

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

The fuzzystrmatch1.2 provider installs exactly ten overloads:
`daitch_mokotoff(text) RETURNS text[]`, four INT4 Levenshtein signatures,
three TEXT Metaphone signatures and the two Soundex signatures below,
not the complete upstream extension.
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


## Installed pgcrypto hashing, bcrypt passwords, raw AES and random data

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
uuid-ossp provider beside unaccent, two-wrapper intagg, selected-type intarray and
selected-function fuzzystrmatch.

Password hashing implements bf only, with a fixed-size stdlib-only Blowfish
schedule. Only exact `$2a$`/`$2x$` heads select bcrypt, with costs04..31 and
the first72 password bytes; short keys cycle through their terminating NUL.
2x retains historical signed-octet expansion. Stored hash/salt suffix bytes
after29 are ignored, and noncanonical final salt padding bits normalize away.
Malformed recognized bf salts raise22023 `invalid salt`; other crypt algorithms
and `$2b$`/`$2y$` dispatch are explicit native0A000 boundaries, while `$2$`
retains39000 `crypt(3) returned NULL`. High costs remain cancellable each round.
gen_salt recognizes bf case-insensitively without trimming, with omitted/zero
cost6 and allowed4..31. Its sixteen strong random bytes are independent of SQL
random state. Unknown names and invalid bf counts retain22023 diagnostics;
documented non-bf generators are native0A000 boundaries.
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

Raw encryption implements AES CBC/ECB only, with default CBC/PKCS and explicit
`pad:none`; observed rijndael spellings denote these same AES modes. Raw slots
infer BYTEA, not TEXT, and keys/IVs contain uninterpreted octets. Keys choose
16/24/32 bytes, zero-padding short keys and clipping beyond32; IVs choose the
first16 bytes with zero suffixes when short. ECB ignores a nonnull IV.
Strict NULL still evaluates demanded siblings. A complete PKCS block is added
to aligned/empty plaintext; nopad never strips data and requires block alignment.
Invalid lengths/padding raise39000 with the actual function's English primary.
Cipher type strings use ASCII folding and UTF8-safe63-byte clipping without trim
or notices. Malformed/unknown options and unknown ciphers retain22023; recognized
CFB, Blowfish, DES/3DES and CAST5 cipher inventory is native0A000, not a false
PostgreSQL unknown-algorithm claim. These exclusions do not expand native modes.
The stdlib AES block loop uses fixed key/IV/block scratch, one owned result and
bounded cancellation polling. Each frame reconstructs its own key and mode.
Raw AES provides no authentication: wrong keys/IVs are not guaranteed to fail.
See [PostgreSQL raw encryption](https://www.postgresql.org/docs/18/pgcrypto.html#PGCRYPTO-RAW-ENC-FUNCS).

## Installed uuid-ossp version 4

The uuid-ossp1.1 provider installs exactly one native member:
`uuid_generate_v4() RETURNS uuid`. Use quoted `CREATE EXTENSION "uuid-ossp"`
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
Other uuid-ossp generators, namespaces/constants, and arbitrary shared-library
loading remain excluded; PostgreSQL's complete upstream inventory is larger.

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
  outer-join/scalar-subquery contexts. Domain NOT NULL and inherited domain
  defaults remain unavailable. Domain CHECK ADD/DROP/VALIDATE is supported in
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
  `pg_class.relacl`, `pg_database.datacl`, `pg_attribute.attacl` and
  `pg_namespace.nspacl`, GRANT extensions, role-rename SQL and new generic
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
  This adds no INET XOR/shifts, containment, network arrays, grouping/hash
  providers or OS networking.
- XID/CID/XID8 are distinct scalar identifiers, not integer/OID aliases or
  generated system attributes. XID supports equality/inequality (including INT4
  only on its right); CID only equality; XID8 unsigned comparisons/order.
  Explicit XID8→XID retains low32 bits. String I/O is admitted, not numeric/OID
  casts. Identifier companion arrays remain lookup-only. Scalar input itself
  does not allocate; the selected transaction-information functions above own
  actual assignment, full epochs and retention, without populating system columns.
- JSONPath supports `jsonb_path_query_array` and the four query-family consumers
  described below, not SQL/JSON query syntax or GIN. JSON has no equality provider.
  Catalog type identities do not imply executable codecs.
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
- Integer/bigint/numeric `generate_series` executes through SELECT-target expansion
  and FROM function sources, including views, CTAS and INSERT SELECT.
  Numeric steps are exact; start/step (not stop) determine display scale.
  Results remain unconstrained NUMERIC. NULL is empty; nonnull zero/nonfinite
  inputs have PostgreSQL diagnostics. Unreturned successors can exceed packed
  numeric limits without failing. Temporal overloads inform genuine resolution
  but are not executable providers. INSERT VALUES expansion remains excluded.
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
  rectangular arrays, typmods, plain domains and existing scalar/array/composite
  domain CHECKs. Domain NOT NULL/default DDL retains its documented boundary.
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
  runtime-parameter optimizer boundaries remain unchanged. Demanded arithmetic
  and all scalar methods raise native `0A000` through silent/predicate layers;
  unreachable branches are not eagerly rejected. JSONPath operators, `_tz`,
  SQL/JSON query forms and GIN remain unsupported.
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
  return `1` independently of recorded versions; ICU inspection reports
  `0A000 ICU is not supported in this build`.
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

Native `c`, `sql`, `plpgsql` and other external procedural languages are absent:
bounded builtin/extension providers do not supply a general language runtime.
Language/routine DDL, DO, handler/validator execution and external native ABI
loading remain outside this catalog. Catalog INSERT/UPDATE/DELETE fail with
`0A000` / `catalog source mutation is unsupported`, even for zero-match
predicates. Row locks fail with `0A000` /
`row locking is not supported for virtual catalog relations`. PostgreSQL itself
uses table permissions for ordinary-reader writes; its catalogs are not claimed
universally immutable. Held native results retain their row/types across
Reset/Restore, while held statements read the replacement catalog.
See [selected proof and smoke](../reference/catalogs/README.md#pg_language-selected-proof).

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
JSON/numeric/network/bit/UUID/ACL families, the six executable ranges and three
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

This inventory is deliberately narrower than REGTYPE lookup. Unimplemented
xml/geometry/full-text/refcursor/vector/pseudo families and unrelated
lookup-only relation identities are absent. Temporal multiranges4533/4534/4535
and arrays6152/6153/6155 remain absent even though pg_range preserves their
released identity links. Existing concrete and lookup-only companion codec
flags are unchanged: catalog visibility never enables storage, input, casts,
constructors or invocation of identity-only I/O routines.

The seeded information_schema.time_stamp domain exposes the pinned18.6 stored
metadata default `CURRENT_TIMESTAMP(2)` and exact SQLVALUEFUNCTION
op4/type1184/typmod2/location-1 tree. This is metadata, not inherited-default
execution or new CREATE DOMAIN DEFAULT/NOT NULL support.

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

Native inventory is exactly144 already implemented builtin signatures across
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

The complete native inventory is heap2/handler3/type`t`, btree403/330/`i`,
hash405/331/`i` and GiST783/332/`i`. Ordinary symbolic handler output is
`heap_tableam_handler`, `bthandler`, `hashhandler`, `gisthandler`; numeric
`amhandler::oid` retains the same identities. The existing REGPROC output
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
Handler identities do not add callable handlers, access-method DDL, full
builtin pg_proc population, or physical heap/btree/hash/GiST algorithms.
See [selected proof and smoke](../reference/catalogs/README.md#pg_am-selected-proof).

## pg_opclass supported operator classes

`pg_catalog.pg_opclass` is a read-only PUBLIC catalog with the released nine
nonnull columns: OID `oid`, OID `opcmethod`, NAME `opcname`, OID
`opcnamespace`, OID `opcowner`, OID `opcfamily`, OID `opcintype`, BOOL
`opcdefault`, OID `opckeytype`. Relation2616 reuses row type10013; NAME has
C collation950. Captured results retain their generation, while held numeric
parameter queries read fresh owned descriptors after native replacement.

The inventory contains62 implemented classes:30 btree403,30 hash405 and the two
feature-local GiST783 classes inet_ops/range_ops, independent of user indexes.
Names/identities/family/input/default/storage facts match the pinned
PostgreSQL18.0 UTF8/C oracle's supported subset. VARCHAR aliases use
TEXT25 input and are nondefault; pattern aliases are nondefault. Btree name_ops
has keytype cstring2275, versus hash keytype0. Concrete enums share anyenum3500
input; domains and object aliases reuse their original provider classes.
Existing JSONB hash uses class10089/family4034, distinct from btree10088/4033.

Recognized diagnostic-only array/record/range/multirange/bit/varbit/pg_lsn/
tsquery/tsvector classes are not advertised without executable method providers.
The existing native hash-array0A000 and btree-array42704 boundaries remain;
no gin/spgist/brin provider, class DDL, catalog writes, pg_opfamily source or
pg_index.indclass/vector projection is added. Existing bounded GiST exclusion
providers and the represented pg_am source do not claim physical index storage.
Full diagnostic and lifecycle expectations and actual feature smoke selectors
are in [the catalog proof](../reference/catalogs/README.md#pg_opclass-selected-proof).

TID scalar values own real default tid_ops btree10050/family2789 and
hash10055/family2227 providers. Values canonicalize as unsigned `(block,offset)`
and compare by numeric unsigned components. This supports declared TID keys, not
physical `ctid` source population or system-column indexing. TID arrays,
binary COPY and development-only TID functions remain outside this scalar scope.

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
There is no ALTER OPERATOR RENAME, CREATE FUNCTION runtime, invented user-routine
frame, to_regoper/to_regoperator implementation or physical planner storage.
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
