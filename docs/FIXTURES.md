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
`information_schema` namespaces are recreated. Database identity and attachment
owner/ACL authority, roles and their committed shared comments and role/database
defaults, live sessions and their captured settings, existing pools and
non-reusable counters (such as transaction IDs) are kept.
Shared memberships, their stable row OIDs and the nonreusable membership
reservation frontier are retained by both Reset and Restore. Restore's private
staging capture preserves these values without replacing destination shared
authority; Dump never serializes membership identities or GRANT commands.
Persisted defaults remain destination cluster authority during Restore; private
replay does not apply or import them.
Parameter ACL authority is cluster-shared: Reset clears it in every attached
database, including future attachments. Local data in other databases survives.
Pending parameter writes cannot cross replacement, including a first grant
whose parameter was absent before and after replacement. Unrelated peer-local
work retains its ordinary transaction lifecycle.

## Dump

Captures a consistent committed snapshot before writing, then emits PostgreSQL SQL:
owner-switching `SET SESSION AUTHORIZATION` directives, `CREATE` statements in
dependency order, `INSERT`s with canonical literals, represented database-local
comments, and sequence state. The output can also be replayed on PostgreSQL.

- XML literals replay the exact accepted original spelling with explicit
  XMLPARSE DOCUMENT or CONTENT, preserving the originally requested mode.
  They do not use typed transport output, so declaration normalization and
  output warnings cannot change the dump. Restore is independent of the
  destination `xmloption`; a failed XML input leaves the old catalog intact.
- Not dumped: roles or their shared comments, passwords, memberships, settings,
  TEMP objects, uncommitted or prepared work, sequence `currval`/`lastval` session
  state, and commit timestamp history.
- Text-search template declarations preserve optional INIT, mandatory LEXIZE and
  actual callback dependencies before dictionary declarations. Seed and installed
  template relabels replay without duplicate CREATE or invented ownership.
  Restore remaps template/dictionary links together and recompiles admitted real
  dictionary resources rather than copying initialized execution state.
- Native collected extended statistics retain their stale construction state,
  including joint MCV population/marginal counts and true NULL markers, in strict
  typed comments. Restore remints current type, enum-member and nested array
  identities without collecting restored rows; records without built MCV remain
  absent. Structural-tree dictionaries use a private retained-statistics
  constructor, not SQL input authority or PostgreSQL node syntax. Genuine
  destination TEMP statistics survive replacement under existing TEMP retention;
  malformed or canceled reconstruction cannot publish a partial replacement.
- Dump fails before writing anything when it cannot serialize faithfully, for
  example invalid indexes, invalid raw ACL authority or cyclic view
  dependencies (`stuntgres: cyclic view dump dependencies`).
- SQL routine declarations preserve OUT/INOUT order, TABLE mode and SETOF
  cardinality, with actual quoted or checked standard bodies. Named result and
  formal child types precede their routines; anonymous output records are
  reconstructed from formals, never emitted as independent named types.
  Replacement maps each original record occurrence through the same context as
  stored calls/views, preserving immutable old rows and current child ownership.
  Volatility, strictness, security mode, parallel/cost/rows metadata and ordered
  routine-local configuration are declaration state and survive replacement.
  FROM CURRENT is saved as its captured value, not recaptured during replay.
  Identifier-list settings such as `search_path` replay as separate SQL SET
  arguments, preserving commas and quotes within each member rather than
  turning the entire canonical list into one schema name.
  Invocation identities, SAVE frames and live session settings are never dumped;
  restored definer calls resolve the current mapped owner and EXECUTE authority.
  Supported PL trigger/event-trigger functions restore this same declaration
  state; firing installs the current mapped body owner and local configuration
  without inventing a new firing-time EXECUTE check. Native intrinsic providers
  cannot import ignored security/configuration state through catalog validation.
  Declaration normalization uses the ordinary lock-safe callback handoff during
  replay too; invocation frames and callback results are not persistent authority.
  Accepted intrinsic-provider parallel/cost/rows patches use the existing
  provider identity replay, preserving real input signatures and owner ordering.
  Generated constructor metadata follows its actual type declaration; seeded
  handlers and installed providers retain their existing installation prerequisites.
  Fresh native destinations require matching genuine foreign registrations;
  replay does not fabricate native callbacks or serialize execution authority.
- Parameter ACLs roundtrip by canonical name through strict typed catalog repairs,
  preserving explicit empty arrays, ordered grantors and grant options. Restore
  replaces shared parameter authority across all destination database attachments,
  allocating identities beyond destination cluster frontiers without importing
  source OIDs. Dump excludes private grants and session setting values. Destination
  database identity/owner/ACLs, roles and peer-local data survive replacement.
  Invalid/cancelled input or a Parameter-authority change racing the retained
  input predecessor changes no shared roots or replacement-reserved frontier.
  Unrelated subscription/statistics/local-object edits during reader I/O retain
  the ordinary replacement behavior; existing principal and live-TEMP validation
  errors keep their precedence.
- Event triggers replay in the final DDL section, after saved data/defaults and
  attribute metadata, initially disabled and activated only after all definitions.
  Owner, function references, ordered TAG filters and O/D states survive logical
  replacement without running callbacks over the replay prefix. An event-trigger
  owner demoted from superuser is unrepresentable unchanged portable SQL; Dump
  rejects that state before writing, without changing the live role or trigger.
- Retained partition row triggers beneath a statement-level parent replay as real
  child CREATE TRIGGER statements. A paired `stuntgres-trigger-clone-v1` comment
  restores their historical parent link only in native Restore; PostgreSQL
  ignores the comment and recreates independent child triggers with the same
  firing behavior, not the native drop/detach dependency link.
- User trigger enable modes replay after every trigger definition, using ONLY to
  retain independently stored partition-clone modes. Complete four-slot
  `stuntgres-fk-trigger-mode-v2` records form a deterministic native footer with
  logical owning constraint/slot identities and a SHA256 seal of the exact SQL
  bytes preceding it; no allocated RI name or OID enters the footer. Native
  Restore applies modes after all FK declaration paths have completed. Only a
  complete snapshot whose seal matches the entire SQL stream receives its
  per-constraint data-retention waiver, preserving saved replica/disabled-trigger
  orphan rows. Edited or appended SQL loses that waiver and undergoes ordinary FK
  validation with its normal diagnostics; NOT VALID state remains unchanged.
  Valid edited SQL can restore successfully without a matching seal. Marker-looking
  text within SQL literals is ordinary data, never a decoded footer record or a
  byte range excluded from hashing.
  The seal is deterministic provenance, not cryptographic authentication.
  Malformed, incomplete, duplicate or unresolved metadata rejects the isolated
  restore without changing target data, modes or held prepared handles.
- Pending partition detachments reconstruct their attached SQL graph and saved
  executable bound CHECK, followed by one
  `-- stuntgres-partition-detach-pending-v1 <base64>` line per immediate edge.
  Standard padded base64 carries UTF-8 JSON with exactly five nonempty string
  fields, in order: `parent_schema`, `parent_table`, `child_schema`,
  `child_table`, `strategy` (`r`, `l`, or `h`). Export orders decoded name
  components, not OIDs or flattened names; records precede the sealed FK footer.
  Only genuine parser line comments are decoded. Reserved-prefix bad versions,
  malformed base64/JSON, unknown/duplicate/missing fields, wrong types and
  trailing tokens fail native Restore. Literal marker text remains ordinary data.
  The last isolated restore hook validates imported owners, exact reciprocal
  membership, strategy, absence of DEFAULT/other pending edges, and an existing
  valid enforced bound-implying RANGE/LIST CHECK before one checked publication.
  Stronger CHECKs qualify; HASH needs no replacement CHECK. No CHECK is created
  by repair. Duplicate/unresolved/invalid records, cancellation and I/O failure
  leave the destination and held handles untouched. PostgreSQL ignores the
  comment and receives attached reconstruction. Transient snapshots, cohorts,
  lock tickets and admission-proof pointers are never serialized.
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
- Installed routine EXECUTE ACLs are preserved by validated stable-slot owner
  metadata during native Restore, including owner-only defaults, named/PUBLIC
  grants, grant options and explicit-empty ACLs. These native metadata comments
  do not apply ACL changes when the SQL stream is replayed on PostgreSQL.
- Supported SQL routines/procedures/aggregates, retained languages, foreign
  wrappers/servers, schemas, types, retained tablespaces, relations and columns
  preserve raw ACLs, named grantors/grantees, grant options and absent versus
  explicit-empty/default-equivalent arrays. Generated type companions retain
  their own raw ACLs independently of effective type-USAGE delegation.
  Exact captured schema/tablespace authority is installed before object recipes;
  type ACLs follow their identity's declaration and precede dependent consumers.
  Final bounded administrative catalog UPDATE repairs reconcile owner moves.
  Routine/type/relation locators use destination-resolved qualified
  regprocedure/regtype/regclass identities and exact column names, never
  allocated source OIDs or dense column offsets. Retained language, namespace
  and tablespace owner changes use named regrole repairs.
  Provision every named role before Restore. These generated repairs also work
  under administrative PostgreSQL replay; ordinary native UPDATE remains
  unsupported for these catalogs.
  Empty ACL repairs use a fixed lossless empty-ACL constructor rather than an
  ordinary `'{}'::aclitem[]` literal, whose zero-dimensional shape is not valid
  for PostgreSQL privilege checks. Empty remains distinct from absent NULL.
  Database registrations and destination attachment owners/ACLs are never
  serialized or changed by Restore/Reset; provision destination authority
  independently.
- Relation replay preserves physical attribute numbers, dropped gaps and the
  next-column highwater. Inert generated INT4 or policy-bearing TEXT declarations
  are paired with ordinary drops; inheritance layouts can use staged CREATE/ADD/DROP statements
  without renumbering surviving columns. Erased inheritance provenance uses
  ordinary generated helper tables that are detached and removed before rows.
  Restore admits only complete fresh-constructor/retirement/teardown pairs and
  publishes no helper objects. Column ACL locators remain exact live names.
  Typed tables and partitions retain ordinary pre-attachment attribute gaps
  through their existing attachment paths, with captured keys, bounds and routing.
- Column storage/compression policies replay through qualified `ALTER TABLE ONLY`
  completions. Differing duplicate index attributes and pre-rename index labels
  use late authenticated `pg_attribute` repairs with the storage/compression
  literal pair, optionally followed by a literal name, or a single literal name.
  Each repair selects one live index attribute by qualified regclass/positive
  attribute number; temporary labels avoid name collisions during SQL replay.
  These preserve logical metadata, not physical TOAST representation, and grant
  no ordinary catalog UPDATE access.
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

## Restore

Replays a Dump stream into a private catalog, then atomically **replaces** the
target's contents; it never merges. Empty input clears all user relations.

- It accepts only the statements Dump produces (declarations, data, represented
  database-local comments, owner directives, sequence state); it is not a general
  SQL script runner, and parameters are rejected.
- Roles referenced by owner directives and `regrole` values must already exist in
  the target engine (`regrole` is dumped by name). Provision them before restoring
  into another Driver.
- Native declaration and row-literal type lookup uses authenticated privileged
  authority only inside the isolated restore stage. Captured owners need not
  retain schema USAGE to replay explicitly typed composite cells, including NULLs.
  Named owners, schema ACLs and destination-resolved type identities remain intact.
  Row-literal lookup authority ends at routine invocation: lazy routine bodies
  and dynamic source SQL use ordinary caller namespace permissions. An ambient
  insertion marker outside the isolated stage grants no lookup authority.
- Any read, parse, execution or cancellation error leaves the target unchanged.
- Stored expressions and routines execute through the ordinary staged engine.
  Installed ltree array expressions remap their selected routine/operator roles
  and exact scalar/companion generations recursively, including typed NULL array
  elements. Stored rank and NULL-element values remain legal data; invocation
  validation failures during staged execution abort replacement normally.
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
  The SQL-body directive preserves quoted SQL source even when its soft names
  no longer bind. Complete syntax, closed formals, declarations and persistent
  structure still validate; checked standard bodies and hard declaration
  defaults retain validated, remapped identities. Catalog-dependent quoted
  analysis and result checks belong to genuine invocation, including defaults
  reached by replay INSERTs. Such execution errors abort replacement normally.
  Empty or comment-only non-VOID bodies are storable in that window but fail
  when invoked, including by replay data; empty VOID bodies remain valid.
- Foreign data wrappers referenced by the dump need matching registrations on the
  target Driver (see [DRIVER.md](DRIVER.md#foreign-data-wrappers)).
- Commit timestamp history is cleared by Reset and Restore; the startup
  `track_commit_timestamp` mode is kept.

## Routine identity replay

Routine and aggregate rename/schema/owner changes preserve hard captured
identities. Owned SQL declarations and aggregate definitions render current
qualified names and owners, actual body/support forms and checked dependencies,
never evaluated results, frames or granted authority.
SQL and PL procedure definitions retain all formal modes, input defaults,
variadic arrays, actual language and declared output tuple types. A sole
procedure OUT remains a RECORD declaration; functions keep scalar-one-OUT
policy. Replacement shares one original-occurrence type/collation remap memo
between formals, output tuples and retained calls, never remapping an already
mapped result or serializing invocation values.
Moving aggregate definitions retain separate moving state types, forward,
inverse and final identities, external seeds and option flags. Native replacement
remaps user-defined moving states along with stored window views and frozen
routine calls; executor frames and progress are never serialized.
Quoted source remains soft/original; standard bodies retain checked identities.
The private SQL-body window does not bind quoted source merely to reconstruct
its declaration or finish replay. Lawful rename/drop/type changes can leave that
source unusable until current names/types are repaired. Direct, fresh-prepared
and held consumers use ordinary invocation checks; failed analysis does not
publish a reusable successful binding or authority.
Changed released/installed providers emit genuine ALTER FUNCTION after required
schema/extension creation and before dependent definitions. Free destination
keys move first; cycles or blocked intermediate keys may need a checked
temporary name. Stable extension slots plus validated current schema/name/signature
identify owner records, not a mutable member name in the extension target schema.

Provider-only streams need no SQL-body replay directive. Restore rebuilds
canonical destination providers before changes, remaps checked captures and
retains destination-configured foreign capabilities. Unchanged provider streams
remain unchanged. Roles are destination prerequisites; Reset preserves them.
Replaying emitted SQL on PostgreSQL also requires destination schema USAGE for
the owner to resolve declared support routines; fixture schema recreation does
not imply the bootstrap public-schema grants.
SQL-routine and aggregate ACLs roundtrip exactly rather than dropping grants.
Unrelated relation/column/type/namespace/default-privilege restrictions remain.
Read errors, cancellation or late invalid identity/owner/ACL replay publish
nothing; writer failures remain ordinary I/O failures.

The [shared routine ALTER proof](../reference/query_core/README.md#routine-alter-proof)
checks repeated native replay, a fresh independent Driver, retained results and
the actual emitted SQL on pinned PostgreSQL. These controls are native logical
replacement, not PostgreSQL physical persistence.
