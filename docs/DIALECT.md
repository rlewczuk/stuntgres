# SQL dialect boundaries

This document records the tablespace lifecycle boundary. Other feature contracts
remain in [DRIVER.md](DRIVER.md), [FIXTURES.md](FIXTURES.md) and the owning
reference package documentation.

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
