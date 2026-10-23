# Native fixture controls

Three package functions clear, save and replace a database's contents, so a test
fixture can be built once and reused:

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

```go
var seed bytes.Buffer
if err := stuntgres.Dump(ctx, templateDB, &seed); err != nil {
    t.Fatal(err)
}
// Later, for each test database (same or another Driver):
if err := stuntgres.Restore(ctx, db, bytes.NewReader(seed.Bytes())); err != nil {
    t.Fatal(err)
}
```

## Common rules

- `db` must be a native stuntgres pool (from `sql.OpenDB` on a stuntgres connector
  or `sql.Open("stuntgres", ...)`). Foreign and middleware-wrapped pools are
  rejected. The controls are host-authorized: they do not depend on the role of
  any borrowed connection.
- No engine lock is held during reader or writer I/O. Rows already returned to
  callers stay valid after Reset or Restore.
- The controls refuse to run while prepared transactions (`PREPARE TRANSACTION`)
  exist; finish them first.
- These are logical snapshots of one in-memory database, not `pg_dump` archives,
  WAL or crash durability.

## Reset

Atomically replaces the catalog with a freshly initialized one: user objects and
data are removed and the built-in `pg_catalog`, `pg_toast`, `public` and
`information_schema` namespaces are recreated. Database identity, roles, live
sessions and their settings, existing pools and non-reusable counters (such as
transaction IDs) are kept.

## Dump

Captures a consistent committed snapshot before writing, then emits PostgreSQL SQL:
owner-switching `SET SESSION AUTHORIZATION` directives, `CREATE` statements in
dependency order, `INSERT`s with canonical literals, and sequence state. The output
can also be replayed on PostgreSQL.

- Not dumped: roles, passwords, memberships, settings, TEMP objects, uncommitted
  or prepared work, sequence `currval`/`lastval` session state, and commit
  timestamp history.
- Dump fails before writing anything when it cannot serialize faithfully, for
  example explicit relation or column ACLs, invalid indexes, or cyclic view
  dependencies (`stuntgres: cyclic view dump dependencies`).
- User-mapping options are written as stored, including credentials.
- Materialized view contents are carried in `-- stuntgres-matview-v1 <base64>`
  comments (JSON fields `schema`, `name`, `populated`, `rows`). Native Restore
  reloads them; PostgreSQL ignores them, leaving views unpopulated until
  `REFRESH`.

## Restore

Replays a Dump stream into a private catalog, then atomically **replaces** the
target's contents; it never merges. Empty input clears all user relations.

- It accepts only the statements Dump produces (declarations, data, owner
  directives, sequence state); it is not a general SQL script runner, and
  parameters are rejected.
- Roles referenced by owner directives and `regrole` values must already exist in
  the target engine (`regrole` is dumped by name). Provision them before restoring
  into another Driver.
- Any read, parse, execution or cancellation error leaves the target unchanged.
- Foreign data wrappers referenced by the dump need matching registrations on the
  target Driver (see [DRIVER.md](DRIVER.md#foreign-data-wrappers)).
- Commit timestamp history is cleared by Reset and Restore; the startup
  `track_commit_timestamp` mode is kept.

## Routine identity replay

Routine rename/schema/owner changes preserve hard captured identities. Owned
SQL declarations render current qualified names and owners, actual body forms
and checked dependencies, never evaluated results, frames or granted authority.
Quoted source remains soft/original; standard bodies retain checked identities.
Changed released/installed providers emit genuine ALTER FUNCTION after required
schema/extension creation and before dependent definitions. Free destination
keys move first; cycles or blocked intermediate keys may need a checked
temporary name. Stable extension slots plus validated current schema/name/signature
identify owner records, not a mutable member name in the extension target schema.

Provider-only streams need no SQL-body replay directive. Restore rebuilds
canonical destination providers before changes, remaps checked captures and
retains destination-configured foreign capabilities. Unchanged provider streams
remain unchanged. Roles are destination prerequisites; Reset preserves them.
Explicit SQL-routine/aggregate ACL serialization still fails before output,
rather than dropping grants. Read errors, cancellation or late invalid
identity/owner replay publish nothing.

The [shared routine ALTER proof](../reference/query_core/README.md#routine-alter-proof)
checks repeated native replay, a fresh independent Driver, retained results and
the actual emitted SQL on pinned PostgreSQL. These controls are native logical
replacement, not PostgreSQL physical persistence.
