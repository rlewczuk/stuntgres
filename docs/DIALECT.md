# SQL dialect boundaries

This document records logical SQL versus external-mechanism ownership boundaries.
Other feature contracts remain in [DRIVER.md](DRIVER.md), [FIXTURES.md](FIXTURES.md)
and the owning reference package documentation.

## Activity monitoring

`pg_catalog.pg_stat_activity` is a typed, read-only 22-column system view of real
logical sessions across one Driver cluster. All columns are nullable; native
endpoint/leader values stay NULL without corresponding physical endpoints/workers.
No OS identities, background rows or socket ClientRead waits are simulated.
The current-role INHERIT mask leaves identity/application and XID/xmin public;
other fields are NULL except the exact `<insufficient privilege>` query text.
Login OIDs are immutable, but `usename` is joined from the current role catalog:
renames are visible through retained activity cuts and a dropped role has a NULL
name without erasing the cached backend or failing unrelated rows. Superuser and
inherited stats-reader authority survive that absence; ordinary observers stay masked.
Activity membership/state/query/times/XIDs retain an independent transaction cut,
but actual lock/sleep waits are live on each execution. Cancellation unwinds waits;
physical termination removes fresh membership. Original source is UTF8-byte
clipped to 1023 bytes, query IDs come only from genuine existing computation,
and source-less native transaction controls invent no SQL source. See the full
[Driver activity contract](DRIVER.md#session-activity-monitoring), including
disabled tracking, canonical horizons and complete mutation diagnostics.

## Archiver monitoring

`pg_catalog.pg_stat_archiver` exposes one cluster-wide, read-only row with the
seven PostgreSQL 18 columns: `archived_count` (bigint), `last_archived_wal`
(text), `last_archived_time` (timestamptz), `failed_count` (bigint),
`last_failed_wal` (text), `last_failed_time` (timestamptz), and `stats_reset`
(timestamptz). All attributes have nullable catalog metadata. The two technical
counter estimates use the deterministic formula **zero in the absence of a
physical archiver/event producer**; SQL reads and DML never count as archival
activity. Both last-WAL names and both last-event times stay NULL without actual
events. A filename is not an archive-completeness or filename-order guarantee.

Reset metadata is exact: initialized clusters record their genuine startup
instant, and authorized `pg_stat_reset_shared('archiver')` or NULL/default-all
resets record one genuine instant across databases. New databases inherit this
authority; database-local native Reset/Restore preserves it. Reads never
initialize a missing reset timestamp. Dump serializes no monitoring history.

Current view/selected-column SELECT and backing `pg_stat_get_archiver()`
EXECUTE are independently required on each execution. The backing catalog
descriptor has default PUBLIC EXECUTE and supports ordinary GRANT/REVOKE.
Permitted ordinary roles, `pg_monitor` and `pg_read_all_stats` see the same fields,
without session-style masking; these roles do not inherently receive reset
EXECUTE. No callable archiver getter or physical WAL facility is introduced.
The existing cumulative
Reader supplies none/current, cache/family and snapshot/all-family cuts;
clear and transaction completion release cuts without resetting counters.
Reset does not invalidate a retained cut or detached Rows. Mutations reject
through ordinary non-updatable-view diagnostics, including WHERE FALSE, and no
portable row order exists without ORDER BY.

## WAL monitoring

`pg_catalog.pg_stat_wal` exposes exactly one cluster-wide read-only row:
`wal_records` (bigint), `wal_fpi` (bigint), `wal_bytes` (numeric),
`wal_buffers_full` (bigint), `stats_reset` (timestamptz), in that order.
All five catalog attributes are nullable. The four technical estimates use
the deterministic formula **zero when the corresponding physical WAL facility
is absent**; they are not physical measurements or estimates derived from DML,
IO timing or keyed WAL operations. NUMERIC bytes remain exact, exported as
native decimal string `"0"` without floating-point conversion. No files,
workers or last-event names/times are fabricated. A genuinely absent reset
stamp remains NULL.

Initialized clusters record their genuine startup instant. Authorized
`pg_stat_reset_shared('wal')` and NULL/omitted all-family resets publish one
genuine cluster instant, including across rollback; unrelated resets preserve it.
Late databases inherit current authority and local native Reset/Restore preserve
the destination's authority and peers. Dump contains no monitoring history.
The existing none/cache/snapshot Reader modes retain ordinary lifetime:
clear or transaction completion refreshes a cut, not the statistics themselves;
peer reset does not mutate retained Rows.

PUBLIC SELECT and backing `pg_stat_get_wal()` EXECUTE are separate current
permissions, rechecked for held statements and column-only reads. Ordinary,
monitor and read-all-stats roles see the same fields; reset requires its own
grantable EXECUTE privilege. The backing declaration supplies metadata and ACL
authority, not a callable getter. Ordinary non-updatable-view diagnostics reject
mutations even with WHERE FALSE. No portable row order exists without ORDER BY.


## Subscription monitoring

`pg_catalog.pg_stat_subscription` is the eleven-column, read-only PostgreSQL 18
relation. Its shared subscription OID/NAME identities are visible across databases,
including distinct same-name subscriptions. With no actual apply worker, the
released LEFT JOIN retains one identity row and nine typed NULL worker/status
fields. Desired Enabled state, ordinary session PIDs, counters and replication
origin registrations do not establish execution. External publisher connections,
replication slots and apply transport retain their existing command boundaries;
no worker activity, received LSN/time or substantive apply conflict is fabricated.

Public identity/status visibility is not owner-masked. Protected connection
information remains in the separately protected `pg_subscription.subconninfo`
catalog column and is never copied into this view. Current view/column SELECT
applies on every execution, including held statements. The backing
`pg_stat_get_subscription(oid)` separately requires EXECUTE when evaluated;
constant-false queries retain SELECT admission but do not invoke the getter.
Its released definition and native `_RETURN` metadata do not grant a callable
statistics getter or executable rule. Existing shared-provider subscription
counter/lifetime ownership is unchanged; status reads do not create snapshots,
keys or counter events. Rename preserves identity, drop removes it, and native
Restore remints definitions without importing monitoring history. Detached native
Rows retain their original typed values across later changes. Mutations, including
WHERE FALSE, reject through normal full non-updatable-view diagnostics; TRUNCATE
and COPY FROM also reject. No row order is portable without ORDER BY.

## Heap surgery

The genuine `pg_surgery` provider implements forced freeze and kill on canonical
exact-version TIDs. Kill publishes immutable dead-slot authority, not DELETE:
it bypasses DELETE triggers and FK actions and leaves existing index membership
untouched. Completed logical block groups survive rollback and later failures.
Native heap-all-indexed amcheck reports retained killed targets as a logical
XX002 finding; it does not claim PostgreSQL's physical HOT-parent diagnostic.

Actual pages/line pointers, cleanup buffer pins, physical HOT/pruning, WAL/crash
recovery and shared-library C ABI loading are external mechanisms. Logical
controls preserve killed-row absence and persistent SQL identities, not physical
corruption or live session/transaction resources.

## Table block monitoring

`pg_catalog.pg_statio_all_tables`, `pg_catalog.pg_statio_sys_tables` and
`pg_catalog.pg_statio_user_tables` have exactly the eleven released PostgreSQL
18 columns, typed OID/NAME/NAME followed by eight INT8 counters, with nullable
attribute metadata. Membership and identities are exact represented r/t/m
relations, not a guessed physical bootstrap inventory; ordinary partition leaves,
unlogged, materialized and other-session TEMP objects are included. The view is
PUBLIC-readable by default with its own ordinary ACL, no underlying table/schema
privilege filtering or RLS redaction, and normal non-updatable-view diagnostics.

Only block counters are approximate: published heap fetched/hit F/H projects to
`F-H,H`; actual nonempty source Opens currently add one resident logical request
to both, hence `0,N`. Table index counts sum existing real index provider facts,
not generic heap requests. No indexes means NULL SUMs; existing unused indexes
mean numeric zero. Absent native TOAST means SQL NULL for both direct counters
and empty TOAST index sums, never invented storage identities. Only a valid
relation with no statistics defaults to zero. Physical disk/cache, byte/page
sizing, WAL and TOAST storage are not implemented by this monitoring projection.
Shared counts policy, publication and transaction Reader modes remain unchanged.
SYS selects exact `pg_catalog`, exact `information_schema`, or case-sensitive
`pg_toast` prefixes from ALL with identical values and NULLs under one Reader.
USER selects the exact case-sensitive complement, including TEMP namespaces
and quoted `PG_TOAST`/`Pg_Catalog`, but excluding every lowercase `pg_toast`
prefix. Named ALL/USER rows agree in all eleven cells under one snapshot;
SYS excludes those user identities. Own-view SELECT is live for held statements;
denied underlying objects remain visible without redaction. SYS/USER I/U/D names
underlying ALL; TRUNCATE/COPY retain the selected view name.

## Index monitoring

`pg_catalog.pg_stat_all_indexes` exposes exactly the PostgreSQL 18 nine-column
view: OID parent/index identities, NAME schema/table/index names, INT8 searches,
nullable TIMESTAMPTZ last scan, and INT8 returned entries/simple live fetches.
All view attributes have nullable metadata. Missing activity means zero counters
and SQL NULL last scan, not a fabricated zero timestamp. Ordinary PUBLIC SELECT
applies to the view; there is **no parent table/schema privilege filtering or
per-field redaction**, including for users without `pg_read_all_stats`.
INSERT/UPDATE/DELETE reject with 55000 even for WHERE FALSE; TRUNCATE and COPY
FROM reject with 42809 using ordinary view diagnostics.

Rows enumerate actual current-database index descriptors whose parents are
ordinary, represented TOAST, or materialized relations. Native bootstrap
`HasIndexes` flags are not an inventory of physical PostgreSQL bootstrap/TOAST
indexes, so no fake index rows/OIDs are created. Partitioned parents, foreign
tables, views and sequences are excluded; actual leaf, TEMP, unlogged and
unready/invalid existing indexes remain represented. System/user predicates are
`schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'`
and its complement, not additional index-statistics views. No order is promised
without ORDER BY.

Counters follow **actual reached execution**, never SELECT count or planner
preference. Current eligible implicit hash searches count one search (including
non-NULL misses), equality-accepted returned entries, and live heap candidates
before residual recheck. Logical bulk bloom counts searches/lossy candidates,
but per-index fetches stay zero (bitmap-style ownership, not physical bitmap
execution). Retained output replay, mere capture/Prepare, full scans, unsupported
AND hash predicates, explicit/private/historical fallback, and B-tree presence
alone do not create native index events. No B-tree scan, bitmap access method or
index-only scan is added; projection coverage never implies avoided heap fetches.
PostgreSQL may choose a different real path; settings are preferences, not proof.

`track_counts` gates collection, not results. The existing cumulative provider,
idle/terminal publication throttle and shared `stats_fetch_consistency`
none/cache/snapshot Reader own visibility. Clear discards the read cut; it neither
flushes pending work nor resets counters. A published positive-search batch stamps
the latest completed top-level transaction stop available at publication, not
scan start or flush wall time; read/fetch-only batches cannot advance last scan.
Reached read events survive failure/rollback. Drop retires exact qualified keys,
rename/schema movement preserves history, and recreate starts fresh. Dump stores
definitions/data, never monitoring history. Restore/Reset retain already-published
destination live TEMP index history under the new epoch, but imported persistent
indexes start at zero/NULL and old-generation pending work is rejected.

## Embedded GSSAPI monitoring

`pg_stat_gssapi` projects real embedded physical-session lifetime and current
privilege filtering. Native connections do not perform GSS authentication,
transport encryption or credential delegation: flags are false and the dependent
principal is NULL, never derived from a role name or endpoint approximation.
The view adds no network authentication mechanism or backing activity routine.
See [the driver contract](DRIVER.md#gssapi-session-monitoring) for visibility,
snapshot refresh and physical-versus-pooled close semantics.

## Passwordcheck activation

`Driver.RegisterPasswordCheck` enables the stock role-password hook and
`passwordcheck.min_password_length` before connector construction. ROLE/USER
password effects use ordinary settings, parameter ACLs and atomic role
publication. There is no SQL extension package or serialized activation.
PostgreSQL server preload, C ABI/hook chaining, process/loader resources and
optional CrackLib builds remain external mechanisms; native startup registration
replaces only activation. See the [Driver contract](DRIVER.md#password-strength-startup-registration).

## pgcrypto OpenPGP

The installed text symmetric wrappers implement AES128 for the effective data
and S2K ciphers, uncompressed data, iterated salted SHA1, direct derived keys and
integrity-protected CFB packets. Explicit S2K counts round upward to the RFC byte
count; default salt, count and prefix material comes from execution entropy.
An explicit S2K cipher affects the packet header and key derivation even without
a separately encrypted session key. Options never silently substitute an
unavailable effective encryption profile. Decryption follows packet algorithms,
not otherwise valid encryption preferences supplied as options.

The checked packet reader accepts definite old headers and new definite/partial
framing, with a 16 MiB bound on each declared chunk rather than its aggregate.
After authenticating the complete prefix and MDC, inner framing also accepts
old context lengths and concatenates validated literal payloads. Any non-`t`/`u`
literal format reports `Not text data` only after complete structural validation;
malformed structure or bad MDC reports generic corruption without publishing
text. Compression diagnostics likewise follow authentication. Invocation-owned key,
derivation and plaintext scratch is cleared on exit. Immutable caller-owned
password strings cannot promise secure erasure. Ordinary user-authored SQL
literals/defaults remain ordinary SQL in native logical snapshots.

OpenSSL provider ABI/loading and recovery of historical provider-failure
ciphertext, native keyrings, shell programs, networking and physical durability
are external mechanisms, not successful embedded substitutes. The bounded
symmetric path uses standard-library crypto and no shell crypto. Native controls
preserve extension relationships, stored ciphertext and SQL consumers, never
live cipher/provider/session handles. UTF8 text transport rejects embedded NUL
before option parsing; BYTEA packet bytes remain opaque.

## Tablespaces

Supported logical lifecycle syntax:

```sql
ALTER TABLESPACE name RENAME TO new_name;
ALTER TABLESPACE name OWNER TO role;
DROP TABLESPACE [IF EXISTS] name;
```

Names are single, unqualified identifiers with PostgreSQL identifier folding,
quoting and byte-safe truncation notices. OWNER recipients include typed
`CURRENT_USER`, `CURRENT_ROLE` and `SESSION_USER`; quoted spellings are literal
role names. ALTER IF EXISTS, LOCATION, qualified targets, DROP name lists,
CASCADE and RESTRICT are not accepted forms. ALTER TABLESPACE SET/RESET options
are outside this supported subset. LOCATION is not a PostgreSQL ALTER TABLESPACE
operation.

The native catalog contains only the real builtins, initially `pg_default`
(OID 1663) and `pg_global` (OID 1664). Rename changes their live binding, not
identity, owner, ACL or physical placement. Old names cease to resolve; object
address/identity/description APIs, pg_tablespace and shared pg_tables summaries
use current names. Stored default_tablespace/temp_tablespaces text is not
rewritten. A stale default name falls back to the database default at use;
interactive temporary settings reject missing names. Transaction-cached temporary
OIDs survive a rename, while the next transaction resolves the stored text anew.

Rename requires inherited ownership or superuser authority; CREATE privilege
alone is insufficient. Missing source wins before destination validation;
nonowner denial is `42501 permission denied for tablespace NAME`. Lowercase
`pg_` destinations are reserved (`42939`, with the prefix Detail), then duplicate
names fail with `42710`, including self-rename. Quoted uppercase `PG_` is not the
reserved lowercase prefix.

OWNER resolves the recipient before the object. An existing same owner succeeds
without changing the descriptor, ACL, revision or publication frontier, including
for an unrelated caller. Actual transfer requires ownership and ability to SET
ROLE to the recipient, with a superuser bypass and no extra CREATE or recipient
superuser requirement. Existing ACL grantor/grantee references are rebased to the
new owner, preserving raw absent/explicit states and grant options.

Both ALTER forms are transactional: commit publishes shared immutable metadata
to existing attached databases; later attachments inherit current names/authority.
Rollback, savepoint recovery and canceled native publication do not publish a
partial image. Retained rows keep their earlier captured values. Prepare describes
zero result columns without resolving live object names/authority; execution
resolves them afresh.

DROP is standalone. Read-only `25006` refusal precedes transaction-block `25001`,
then lookup, owner and pinned checks. Missing names yield `42704`; missing IF
EXISTS succeeds with zero affected rows and exactly one execution-time NOTICE
`00000 tablespace "NAME" does not exist, skipping`. Prepare emits no such notice.
Nonowners get `42501 must be owner of tablespace NAME`. Both fixed native OIDs
remain pinned after rename or owner transfer: even their actual owner or a
superuser gets `42501 permission denied for tablespace NAME`, and IF EXISTS does
not bypass that guard. An error in an explicit transaction requires ordinary
ROLLBACK/savepoint recovery before later commands.

### Physical limitations and independent PostgreSQL proof

Native CREATE TABLESPACE validates inputs but refuses physical locations with
`0A000 CREATE TABLESPACE physical locations are unsupported in embedded mode`;
it allocates no ordinary descriptor, placement or dependency. There is no native
ordinary tablespace registration, filesystem probing, relocation or deletion.
Builtin locations remain empty strings, not occupancy information. Native index
TABLESPACE clauses and ALTER INDEX SET TABLESPACE retain their existing
`0A000 index tablespaces are unsupported` boundary. Supported ALTER TABLE
same-default selection is not movement; crossing into pg_global is rejected with
`22023 only shared relations can be placed in pg_global tablespace`. The existing
physical relocation refusal remains intact, but no public native ordinary
setup exists to witness a distinct ordinary destination.

Consequently native pinned `42501` is **not** ordinary DROP dependency/nonempty
validation. Real pinned PostgreSQL 18.6 phases independently create ordinary
physical tablespaces and prove no-storage table/index dependencies (`2BP01`),
physical table/index/database occupancy (`55000`), dependency-before-occupancy
precedence, remote-database count Detail, empty removal and distinct-destination
relocation. Settings-only strings are not permanent dependencies. These are
PostgreSQL-only physical proofs, not fabricated embedded state.

See [reference selectors](../reference/catalog_privileges/README.md#testing) for
`TestReferenceTablespaceLifecycle`, its permanent feature smoke and the pure
`TestTablespaceLifecycle` units. Run changed reference definitions in both modes.
Native Dump/Reset/Restore semantics and the safe PG builtin rollback phases are
documented in [FIXTURES.md](FIXTURES.md).

## External SQL provider ownership

The native engine consumes a host-registered external SQL capability through its
existing GO foreign-handler and transaction contracts. Registration, captured SQL
caller/mapping authority, result/error ownership, cancellation and joined physical
session cleanup are engine responsibilities. Remote authentication, conninfo
interpretation, sockets, PostgreSQL wire/libpq behavior, TLS, SCRAM/GSS delegation
and native ABI/process resources are host mechanisms, not embedded equivalents.
An absent capability is an ordinary typed unavailable error, never a successful
empty query or an in-process remote substitute.

Root Driver DSNs remain exact opaque engine keys. External endpoint/credential
requests are separate data; no root DSN becomes a PostgreSQL connection string.
The reference host uses actual pinned PostgreSQL and pgconn only in explicit
physical phases. Its PostgreSQL comparison consumer is the server's stock
`postgres_fdw`; its native consumer is a genuinely registered GO handler using
the owned external capability. This boundary does not install stock
postgres_fdw handler/validator implementations or add dblink connection/query/
async/cursor SQL routines. Current installed dblink constructor/pkey identities
remain ordinary persistent extension objects.

Persistent wrapper/server/mapping/table/view identities and local data use native
Dump/Restore; remote rows and live connections/listeners/transactions do not.
Remote notifications retain real PostgreSQL sender PIDs separately from native
logical session IDs. Raw external transactions are independent unless an actual
FDW participant enlists through the existing local transaction owner.
