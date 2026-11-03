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
