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
- Dump never includes private transaction comments. Successful Reset/Restore
  fence stale transaction commit and savepoint recovery, so private local or
  role text cannot reappear over the replacement.
- The controls refuse to run while prepared transactions (`PREPARE TRANSACTION`)
  exist; finish them first.
- These are logical snapshots of one in-memory database, not `pg_dump` archives,
  WAL or crash durability.

## Reset

Atomically replaces the catalog with a freshly initialized one: user objects and
data are removed and the built-in `pg_catalog`, `pg_toast`, `public` and
`information_schema` namespaces are recreated. Database identity, roles and their
committed shared comments, live sessions and their settings, existing pools and
non-reusable counters (such as transaction IDs) are kept.

## Dump

Captures a consistent committed snapshot before writing, then emits PostgreSQL SQL:
owner-switching `SET SESSION AUTHORIZATION` directives, `CREATE` statements in
dependency order, `INSERT`s with canonical literals, represented database-local
comments, and sequence state. The output can also be replayed on PostgreSQL.

- Not dumped: roles or their shared comments, passwords, memberships, settings,
  TEMP objects, uncommitted or prepared work, sequence `currval`/`lastval` session
  state, and commit timestamp history.
- Dump fails before writing anything when it cannot serialize faithfully, for
  example explicit relation or column ACLs, invalid indexes, or cyclic view
  dependencies (`stuntgres: cyclic view dump dependencies`).
- User-mapping options are written as stored, including credentials.
- Materialized view contents are carried in `-- stuntgres-matview-v1 <base64>`
  comments (JSON fields `schema`, `name`, `populated`, `rows`). Native Restore
  reloads them; PostgreSQL ignores them, leaving views unpopulated until
  `REFRESH`.
- Installed ISN scalars replay through their shared extension installation before
  consumers. Independent type names, schemas, base/array owners and codec owners
  survive replacement through validated member roles rather than labels.
  Ordinary TYPE OWNER and FUNCTION OWNER statements preserve named owners in
  native Restore and PostgreSQL replay; native owner metadata comments provide
  additional validated member-role reconciliation during native Restore.
- Custom range declarations include their selected subtype class, collation,
  optional diff and generated multirange name. Restore rebuilds destination-owned
  family descriptors and endpoint values rather than retaining source captures.
  Their type-owned constructors are recreated by the type declaration, not
  independently dumped as functions. Recursive constructor SQL preserves exact
  FLOAT4 payloads and symbolic catalog-reference endpoints through the existing
  raw32 cast bootstrap and reference remappers.
  Restore syntax admission recognizes these emitted two-part constructor names
  in the current staged catalog only. Ordinary overload resolution must still
  select a validated type-owned constructor; SQL overloads and forged namespaces
  gain no replay execution authority. Recursive parameter rejection precedes
  strict-NULL evaluation, and failed admission leaves the target unchanged.
  Static recipe shapes remain independently admitted: constructor names such as
  `left` and `right` supplement, rather than shadow, those shape checks.
  The constructor-only selected-winner restriction applies to supplemental
  written replay values, not incumbent stored/default/SQL/PL body calls.
- Custom range families include the range, multirange and both array types.
  Multirange USAGE follows its owning range after array delegation; independent
  companion GRANT/REVOKE is rejected. Ownership changes move all four types, not
  the bootstrap-owned generated functions. Dropping either family schema follows
  the same canonical family dependency closure, including external columns.
  Generated range-to-multirange casts have three INTERNAL dependencies and are
  reconstructed by CREATE TYPE, never emitted as independent CREATE CAST.
  A range owner need not own or have CREATE in either family schema. As with
  domains, the dump records the captured owner, creates under privileged replay
  authority, then transfers the exact fresh range through ordinary ALTER TYPE
  OWNER. Restore admits only the immediate same-kind/name/namespace/target-owner
  pair; it does not grant schema privileges or admit arbitrary owner changes.
- Colliding imports retain target TEMP identities. Actual alias-range bound and
  member constraints activate only genuinely affected endpoint components;
  unrelated uncollided identities retain their originals. Endpoint choices and
  ordinary collisions share one complete, demand-driven capacity plan. It stops
  acquiring allocator outputs as soon as a complete assignment exists, without
  consuming speculative high identities or exhausting future ordinary DDL.
  Replacement reservations use an operation-local cursor of the same central
  allocator. Failed/canceled candidate planning or validation does not issue
  those identities or exhaust the live target. The cursor publishes with the
  catalog only after final fences, including intervening ordinary OID issuance;
  no live allocator is rewound or recycled.
  Stored companion recipes preserve their actual per-datum interval topology,
  independent of written member order; separate carrier elements do not acquire
  cross-element order. Destination subtype providers certify bounds and
  normalization outside the catalog lock before publication. Only genuinely
  unsatisfiable endpoint/ordinary capacity constraints reject atomically:
  dangling numeric identities stay fixed and target TEMP objects never move.
  Explicit unsupported namespace/type ACLs still reject Dump before output;
  this family delegation does not expand ACL serialization.

## Restore

Replays a Dump stream into a private catalog, then atomically **replaces** the
target's contents; it never merges. Empty input clears all user relations.

- It accepts only the statements Dump produces (declarations, data, represented
  database-local comments, owner directives, sequence state); it is not a general
  SQL script runner, and parameters are rejected.
- Roles referenced by owner directives and `regrole` values must already exist in
  the target engine (`regrole` is dumped by name). Provision them before restoring
  into another Driver.
- Any read, parse, execution or cancellation error leaves the target unchanged.
- Stored expressions and routines execute through the ordinary staged engine.
  Foreign scans, writes and schema imports are rejected before capability
  invocation with SQLSTATE `0A000`,
  `restore cannot stage non-retractable effects` (existing more-specific
  admission errors remain intact). Database-local staged effects, including
  routine writes, sequences and large objects, remain allowed. Advisory operations
  and settings changes use the private stage's manager/session and are discarded
  with it; they neither import nor release target-session state.
- This boundary is not a sandbox for registered Go code. Authentic destination
  FDW option validators remain trusted declaration callbacks: they run normally,
  and their original errors abort publication. Their own external effects cannot
  be rolled back. Foreign definitions carry no remote rows or callback state.
- Live target sessions and their settings survive replacement; session settings
  are not imported. Routine-body validation deferral and authenticated comparison
  replay windows are operation-local exceptions, not general SET admission.
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
