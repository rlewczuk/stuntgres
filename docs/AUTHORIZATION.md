# Roles, authorization and schemas

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Each engine starts with the trusted native `postgres` principal (LOGIN,
superuser, identity10) and the sixteen PostgreSQL18 predefined roles below.
`OpenConnector` uses the bootstrap principal. Role identities are opaque,
engine-owned and never reused after DROP, Reset or Restore. PUBLIC is an ACL
grantee meaning all existing and future principals, not a role or membership endpoint.

Persistent views retain normal owner/ACL descriptors. At CREATE, the creator
needs destination CREATE and source-schema lookup USAGE, not source SELECT.
CREATE OR REPLACE chooses only the creation namespace's target, not a later
search_path match. Qualified target creation/replacement requires no target
namespace USAGE; body source schemas still undergo ordinary lookup checks.
At read, the caller needs named-view SELECT; each ordinary definition checks
underlying relation columns as that view's current owner. Invoker definitions
use the original statement invoker, not an outer view owner's substituted
relation principal. Prepared executions recheck current grants and owners.
Bound base reads do not repeat name lookup or source-schema USAGE checks; the
initial named view lookup still requires its schema's USAGE. Relation-principal
substitution never changes session identity or sequence/function authority.
Native table and column GRANT/REVOKE use this same central ACL authority.
The native prepared witness retains an allowed statement description across
revocation and regrant: the same handle must deny42501 without rows after revoke
and resume reads after regrant, including an owner view above an invoker view.

Replacement and ALTER require view ownership. All ordinary owner and ACL checks
follow live INHERIT paths transitively, independently of SET authority and role
attributes. Each hop needs a surviving INHERIT=true grant from some origin;
SET-only/disabled edges do not grant ownership or object privileges. DROP VIEW
also accepts the inherited schema owner without granting that actor ALTER or
replacement rights.
Replacement needs destination CREATE; relation rename needs containing-schema
CREATE, while column rename does not. Schema moves need destination CREATE.
Owner transfer separately follows SET-authorized edges to the target role and
requires that role's CREATE on the current schema; superusers bypass those
transfer restrictions. Relation and column ACL owner references are rewritten
without discarding reader grants or conflating NULL and explicitly empty ACLs.
Subsequent reads immediately use the new owner or original security invoker.


Isolated native Restore uses its validated authenticated-bootstrap lookup
authority for every CREATE VIEW source occurrence, including aliases and joins,
both before a relation-lock wait and when revalidating the grant. This requires
the private import marker and initialized import catalog; ordinary CREATE VIEW
keeps its normal schema checks. The named directive's owner and session identity
remain unchanged, and imported views check current owner/invoker read privileges
rather than retaining bootstrap read authority.

```text
CREATE {ROLE | USER | GROUP} name [WITH] [attribute ...]
    [IN {ROLE | GROUP} names] [{ROLE | USER} names] [ADMIN names]
ALTER {ROLE | USER} {name | CURRENT_ROLE | CURRENT_USER | SESSION_USER}
    [WITH] [attribute ...]
GRANT roles TO members [WITH option {TRUE | FALSE | OPTION} [, ...]]
    [GRANTED BY role]
REVOKE [{ADMIN | INHERIT | SET} OPTION FOR] roles FROM members
    [GRANTED BY role] [CASCADE | RESTRICT]
ALTER GROUP role {ADD | DROP} USER members
DROP {ROLE | USER | GROUP} name [, ...]
SET [SESSION] AUTHORIZATION {name | 'exact role name' | DEFAULT}
RESET SESSION AUTHORIZATION
SET [SESSION | LOCAL] ROLE {name | 'exact role name' | NONE}
SET [SESSION | LOCAL] ROLE {TO | =} DEFAULT
RESET ROLE
SELECT current_catalog,current_database(),current_role,current_user,session_user,user,system_user
```

ROLE/GROUP default NOLOGIN; USER defaults LOGIN. Duplicate options fail `42601`
at execution, duplicate names `42710`, missing DROP targets `42704`. DROP is
statement-atomic across its list, rejects owner/ACL/persistent dependencies
(`2BP01`), and removes membership endpoints through the same immutable authority.
CREATE requires effective SUPERUSER or direct CREATEROLE. ALTER ordinarily requires
direct CREATEROLE and ADMIN on the target; ADMIN traversal is independent of
INHERIT and SET. An ordinary creator automatically gets ADMIN=true, INHERIT=false,
SET=false on its new role, atomically with its attributes and initial memberships.
PASSWORD-only changes to effective self are allowed without that general authority;
adding LOGIN or VALID UNTIL requires full ALTER authority. Ordinary actors cannot
alter superuser targets or either SUPERUSER toggle. Positive CREATEDB, REPLICATION
and BYPASSRLS on CREATE, and either direction on ALTER, require the actor's matching
direct attribute. There is no blanket replication-target restriction.
Native DROP and COMMENT ON ROLE retain their existing actual-SUPERUSER gates.
CREATEROLE plus creator ADMIN does not authorize native DROP. PostgreSQL's broader
CREATEROLE/target-ADMIN authority and creator current/session55006 precedence are
backend-specific supplements, not native parity. Privileged native DROP checks
every name before pinned or ordinary dependencies. `pg_user` grants
PUBLIC SELECT on every LOGIN role's direct summary, including unrelated users;
membership does not synthesize its flags. Its password is always `********`.
`pg_roles` grants PUBLIC SELECT on every role, including unrelated NOLOGIN,
privileged, expired and zero-limit roles. All flags are direct stored attributes;
membership never synthesizes them. The password is always `********` and reachable
configuration is typed NULL TEXT[]. `pg_authid`/`pg_shadow`, role rename and
ALTER ROLE SET/RESET remain excluded. Table/column object grants are supported
separately from membership.
Membership GRANT/REVOKE and ALTER GROUP aliases use ADMIN authority, with separate
checks for SUPERUSER targets and explicit grantor provenance.
Valid `DROP {ROLE | USER | GROUP} IF EXISTS name [, ...]` is likewise excluded:
it fails positioned `0A000` at `IF` before any script statement executes,
including when every target is absent. Ordinary unquoted or quoted role names
`if` and `exists` remain valid. Malformed conditional drops and `CASCADE`
(which is not PostgreSQL role-drop syntax) fail `42601`, not a capability error.
Role names reuse PostgreSQL's ordinary column-name keyword categories;
nonreserved `local`, `session` and `role` remain names. Reserved `WITH` is
only an optional option introducer, not a role name or a repeated option.

### Role attributes

Supported attributes are SUPERUSER/NOSUPERUSER, CREATEDB/NOCREATEDB,
CREATEROLE/NOCREATEROLE, LOGIN/NOLOGIN, INHERIT/NOINHERIT,
REPLICATION/NOREPLICATION, BYPASSRLS/NOBYPASSRLS, CONNECTION LIMIT signed_int4,
PASSWORD string/NULL, ENCRYPTED PASSWORD string and VALID UNTIL string.
Defaults are all negative flags except INHERIT, limit -1 (unlimited), NULL password
and absent expiry; USER alone defaults LOGIN. ALTER USER is an ALTER ROLE alias,
not a LOGIN reset. Every omitted attribute, password verifier, expiry, identity,
comment and existing membership is retained. Empty options and bare WITH are valid.
Attributes themselves never follow membership paths; INHERIT edges still supply
object privileges/owner authority and SET edges still control identity selection.

Plain passwords become SCRAM-SHA-256 verifiers with 4096 iterations, a random
16-byte salt and derived stored/server keys, using PostgreSQL-style SASLprep or
original-byte fallback. Recognized preencoded MD5/SCRAM values are retained;
NULL/empty passwords clear metadata, with corresponding notices/warnings.
VALID UNTIL uses captured timestamptz input settings and preserves finite/infinite
values separately from absence. It is password expiry, not role expiry: trusted
native admission ignores passwords and expiry. No password-verification handshake,
password_encryption/scram_iterations setting or pg_authid projection is
introduced. LOGIN and CONNECTION LIMIT still govern new physical admission; see
[driver admission](DRIVER.md#trusted-role-admission-and-physical-limits).
CREATE/ALTER and other cluster-role utilities remain native explicit-transaction
0A000 boundaries; read-only Execute rejects modifying utilities with25006 before
lookup or authority. Prepared execution resolves fresh authority without caching it.

### Predefined PostgreSQL18 roles — change note

The immutable role authority seeds these exact names/identities. Each has
INHERIT=true, NOLOGIN, all other attribute flags false, CONNECTION LIMIT -1,
NULL password/expiry and no comment. Membership never copies SUPERUSER or
BYPASSRLS attributes onto an ordinary actor.

| Role | Fixed identity | Native capability or boundary |
|---|---:|---|
| `pg_checkpoint` | 4544 | CHECKPOINT explicitly fails0A000. |
| `pg_create_subscription` | 6304 | CREATE SUBSCRIPTION explicitly fails0A000. |
| `pg_database_owner` | 6171 | Implicit native database owner; canonical public owner. |
| `pg_maintain` | 6337 | MAINTAIN for eligible relations; real REINDEX/LOCK authority. |
| `pg_monitor` | 3373 | Ordinary INHERIT/SET membership in the next three roles. |
| `pg_read_all_settings` | 3374 | Supported settings remain readable; no protected SET authority. |
| `pg_read_all_stats` | 3375 | Identity/membership only: no native statistics provider. |
| `pg_stat_scan_tables` | 3377 | Identity/membership only: no native scan extension/provider. |
| `pg_read_all_data` | 6181 | Schema USAGE and table/view/materialization/column SELECT; sequence SELECT. |
| `pg_write_all_data` | 6182 | Schema USAGE and table/view/materialization/column INSERT/UPDATE/DELETE; sequence UPDATE. |
| `pg_read_server_files` | 4569 | No filesystem access; COPY excluded and file functions absent. |
| `pg_write_server_files` | 4570 | No filesystem writes; COPY excluded. |
| `pg_execute_server_program` | 4571 | No command execution; COPY PROGRAM excluded. |
| `pg_signal_autovacuum_worker` | 6392 | No workers/PIDs or signal functions. |
| `pg_signal_backend` | 4200 | No PostgreSQL PID signaling functions. |
| `pg_use_reserved_connections` | 4550 | No PostgreSQL reserved-slot tier; ordinary LOGIN/role limits still apply. |

`pg_monitor` starts with three ordinary `(role, member, grantor)` edges:
settings/stats/scan to monitor, grantor bootstrap10, ADMIN=false,
INHERIT=true and SET=true. They can be revoked normally and are not reseeded by
Reset/Restore. All fifteen non-database-owner roles support normal explicit
membership, options, provenance and SET ROLE.

The fixed single-engine database owner is bootstrap10. Its implicit MEMBER,
INHERIT and SET successor is `pg_database_owner`, including when an ordinary
membership traversal reaches the database owner; no persisted membership row or
direct ADMIN option is invented. GRANT cannot give `pg_database_owner` explicit
members or make it a member of any role (0A000), including initial CREATE clauses.
ALTER GROUP's reserved target guard remains42939. Plain REVOKE retains normal
member-first absent-grant WARNING01000 diagnostics. CREATE/ALTER reserved role
guards remain42939. After authorization/name preflight, DROP of any predefined
role fails2BP01 with primary `cannot drop role NAME because it is required by the database system`
and empty DETAIL; mixed batches publish nothing.

Capabilities supplement ordinary ACL bits through the same live INHERIT closure,
including held prepared execution and column requirements. They do not grant
schema CREATE, TRUNCATE, REFERENCES, TRIGGER, grant options, ownership, owner-only
ALTER/DDL, or write access to protected catalog/provider relations. Sequence
UPDATE authorizes nextval/setval but not SELECT/currval by itself; SELECT does
not authorize nextval/setval. Session-local currval still requires prior nextval.
MAINTAIN uses actual index-root rebuilding and existing transaction-owned AX LOCK
exclusion, not an owner bypass or a successful no-op. VACUUM, ANALYZE, CLUSTER and
REFRESH remain native0A000. The selected missing file/signal/scan functions retain
the native parser boundary0A000 `functions are unsupported` at position8; PostgreSQL
missing functions retain independently specified42883 diagnostics and their Hint.
Absent native statistics relations remain42P01. Granted role names never
manufacture unavailable mechanisms.

Native RLS execution is absent: ALTER TABLE RLS actions fail0A000 without changing
rows. PostgreSQL-only policy-less RLS witnesses show that these data roles do not
bypass RLS; they are not native policy-engine parity evidence. Protected locale/
logging SET gates likewise stay unchanged for monitoring roles.

Canonical public namespace ownership and owner/grantor/grantee ACL provenance use
6171. Native keeps its historical PUBLIC USAGE|CREATE convenience ACL, unlike fresh
PostgreSQL18's PUBLIC USAGE-only default. pg_catalog/information_schema retain
bootstrap10 ownership. Dump emits real namespace ownership deltas against those
baselines, including renamed/cyclic bootstrap schemas; Reset restores canonical
local metadata but keeps the shared role authority/graph. Restore and independent
engine catalog imports remap validated predefined owner/ACL identities without
serializing or importing memberships or regranting revoked monitor edges.

See [selected dual-mode definitions and feature smokes](TESTING.md#predefined-role-oracle-first-contracts).


### Membership options and provenance

Membership identity is `(role, member, grantor)`: independent origins survive
revocation of another origin. New edges default ADMIN=false, SET=true and INHERIT
to the member role's current INHERIT attribute (normally true). Explicit edge
INHERIT overrides that default; changing the role attribute never rewrites old
edges. Updating an existing origin preserves every omitted option; OPTION
means true and repeated GRANT options use the last value. Identical GRANT emits
NOTICE00000; missing attributed REVOKE emits WARNING01000. Utilities affect zero
rows and protocol Prepare neither resolves roles nor changes authority.

IN ROLE makes the new role a member of each listed role; ROLE/USER makes each
listed role a member of the new role; ADMIN adds those members with ADMIN=true.
Distinct CREATE clause categories coexist; repeated categories fail42601 at
execution. The new role and every initial edge publish atomically.

Ordinary ADMIN holders can delegate without inheriting object privileges or
being able to SET the granted role. Inferred grantors use the nearest eligible
INHERIT-reachable direct ADMIN source; actual superusers record bootstrap
provenance. Explicit GRANT requires actor privileges of the grantor and that
grantor's direct ADMIN; explicit REVOKE can clean up its grants after ADMIN loss.
Role/member cycles ignore options. Separate ADMIN support cycles reject
backgrants without independent support (0LP01); PUBLIC is never a member.

REVOKE INHERIT/SET OPTION only clears that flag. Loss of the last direct ADMIN
source rejects dependent grants under default RESTRICT (2BP01, CASCADE hint);
CASCADE removes only recursively unsupported origins. ADMIN-only CASCADE keeps
the source edge with ADMIN=false. GRANT ADMIN FALSE is an option update, not a
dependent-revoke request. ALTER GROUP preserves its own authorization diagnostics.

GRANT targets use the current private candidate for authority and grantor
selection, including preceding targets' option changes. REVOKE instead uses
statement-entry authority and original rows across the entire target list:
PostgreSQL's AddRoleMems increments the command counter; DelRoleMems does not.
The next separate statement sees committed removals. Both operations publish
their private mutations only once, and failure publishes none.
For each GRANT target, all recipients' role/member cycles are checked before
ADMIN support and before same-target notices. REVOKE plans recursive dependencies
per recipient against immutable original rows with fresh actions per target:
RESTRICT stops immediately, duplicate members still match, and an unreached
recipient emits no warning. A later target that updates or deletes an original
tuple already changed by an earlier target fails XX000, `tuple already updated
by self`; repeated target names alone do not fail. Missing rows still warn,
and already-false ADMIN revocation is a quiet no-op. INHERIT/SET revocation
plans an update even when that flag is already false, matching PostgreSQL.
Notices legitimately emitted by an earlier completed target or reached recipient
remain delivered when a later error rolls back the graph.

Inherited owners retain ownership and implicit grant options, but explicitly
empty object ACLs deny ordinary use. Prepared reads check fresh memberships;
already returned rows retain their values and metadata. Membership utilities
remain autocommit-only natively: explicit transactions reject0A000, with
read-only execution25006 taking precedence. PostgreSQL supports rollback/commit.
Reset/Restore retain cluster roles/memberships; Dump never serializes them and
Restore does not import membership DDL.


SET/RESET SESSION AUTHORIZATION change session_user and current_user together. Only the **originally
authenticated** superuser may select another principal (including NOLOGIN);
an ordinary authenticated role may select itself or reset/default only.
Failed switches preserve both identities and application_name. Identity
projections have NAME metadata, exact lowercase labels and one string row;
prepared projections read execution-time identity. Ordinary NAME columns follow
the [NAME value contract](TYPES.md#name). Exec of identity SELECT reports one selected row;
role and authorization commands affect zero rows.

SET ROLE changes only effective current_user/current_role/user. Eligibility is
checked against session_user: self, a session superuser, or an all-SET path through
live membership edges. ADMIN/INHERIT alone confer no SET authority.
NONE selects session_user; RESET (and TO/= DEFAULT) selects the configured startup
role, or session_user when none is configured. Bare SET ROLE DEFAULT is syntax
error; quoted/string `default` is a role name. SESSION selection persists on
commit and restores on rollback; LOCAL selection expires on either boundary.
SESSION followed by LOCAL retains the SESSION candidate on commit, and savepoint
rollback restores both current role and its lifetime journal. These commands are
legal in read-only transactions. Outside a transaction LOCAL warns with25P01
`SET LOCAL can only be used in transaction blocks`, validates the target and has
no lasting role effect. Missing targets fail22023 `role "<name>" does not exist`;
unauthorized selections fail42501 `permission denied to set role "<name>"`.
RESET and transaction/savepoint restoration use already validated role references,
not a new membership check: revoking SET blocks fresh selection, not restoration.
Native public connectors start with current_user=session_user; the special internal
SessionStart.Defaults role hook validates configured startup/reset identity before
physical-session publication, but is not a public connector option or generic GUC.
SET ROLE does not apply the target's session-variable defaults. SECURITY DEFINER
prohibition remains contingent on a real routine invocation contract, which the
engine does not yet implement; definer views are not routine frames.

Identity keyword expressions use the common typed scalar pipeline, including
predicates, aliases, defaults and views. Six identities return NAME; system_user
returns TEXT SQL NULL because the host assertion is not PostgreSQL authentication.
Ordinary zero-argument catalog functions exist for current_database, current_user,
session_user and system_user. Bare identity keywords reject parentheses; qualified
and quoted catalog calls resolve normally. current_catalog/current_role/user are
syntax aliases, not catalog functions. Prepared execution captures fresh identity;
retained rows keep their execution snapshot. Defaults/views preserve syntax and
observe execution identity, not definition identity. Reset/Restore preserve the
physical session's naming and authorization; no identity state is dumped.

```go
d := stuntgres.NewDriver()
// First create LOGIN roles on the same exact opaque DSN using an admin pool.
connector, err := d.OpenConnectorAs("my fixture", "application_owner")
if err != nil { /* handle host assertion error */ }
ownerPool := sql.OpenDB(connector)
defer ownerPool.Close()
```

`OpenConnectorAs` is a **trusted host identity assertion, not password
authentication**. It does not parse credentials or change DSN naming. The role
argument is an exact decoded stored name (no SQL folding or truncation).
Every physical Connect resolves and LOGIN-validates the requested role;
empty/missing/NOLOGIN assertions return ordinary Go adapter errors, not
network SQLSTATE. Healthy borrower reuse retains selected identity/settings;
physical replacement starts with the connector's requested authenticator and
fresh settings. Use held `*sql.Conn` handles for principal-specific operations.
Two connectors with the same Driver/exact DSN share data, never session state.

## Table and column grants — change note

```sql
GRANT {privilege [(column, ...)] [, ...] | ALL [PRIVILEGES] [(column, ...)]}
  ON [TABLE] relation [, ...] TO grantee [, ...]
  [WITH GRANT OPTION] [GRANTED BY role];
REVOKE [GRANT OPTION FOR]
  {privilege [(column, ...)] [, ...] | ALL [PRIVILEGES] [(column, ...)]}
  ON [TABLE] relation [, ...] FROM grantee [, ...]
  [GRANTED BY role] [CASCADE | RESTRICT];
```

`ON ALL TABLES IN SCHEMA schema [, ...]` expands currently existing tables,
views and materialized views, not sequences, indexes, composite backing relations
or future objects. A schema owner does not automatically own its tables.
Grantees include PUBLIC, optional GROUP before a role specification, CURRENT_USER,
CURRENT_ROLE and SESSION_USER. The decoded exact lowercase spelling `public`
denotes PUBLIC even when quoted; quoted `"PUBLIC"` is a distinct role name.
PUBLIC includes future roles but cannot receive grant options (`0LP01`).
REVOKE GRANT OPTION FOR FROM PUBLIC is legal and preserves its ordinary use.
Table privileges are SELECT, INSERT, UPDATE, DELETE,
TRUNCATE, REFERENCES, TRIGGER and MAINTAIN; column privileges are SELECT, INSERT,
UPDATE and REFERENCES. ALL expands the applicable mask, never ownership.

Absent table ACLs give owners ordinary use rights; absent column ACLs are empty.
The first authorized table mutation materializes owner defaults, including an
option-only request with no removable stored options. Repeating an explicit
effect-free request does not publish a new authorization revision. Revoking an
owner's ordinary privileges does not remove ownership or implicit grant options.
Table privileges OR matching column privileges authorize column operations:
column-only REVOKE cannot negate a table grant. Whole-table checks never combine
arbitrary column grants into a table grant.

Grants retain independent `(grantor, grantee)` origins. All-INHERIT paths supply
ordinary rights and grant authority, while SET-only/disabled paths do not.
Delegation selects one reachable direct option source, maximizing the requested
bits rather than fabricating a union across multiple source roles. Actual
superuser/inherited-owner grants use the owner origin. PostgreSQL18.0 object
GRANTED BY must name the exact current user, even for superusers or inherited
owners; another existing role fails `0A000`, and a missing role fails `42704`
before object lookup.
An owner or superuser revoking a delegate-origin grant directly does not remove
that origin: revoke as the delegate, or revoke its upstream support with CASCADE.

WITH GRANT OPTION grants use plus delegation rights. Partial table requests
warn `01007`; table ALL silently grants an eligible nonempty subset. Column
requests merge by stable attribute identity: a final mask containing all four
column privileges suppresses partial warnings, whether spelled ALL or enumerated
across separate privilege items. REVOKE applies the same distinction with
`01006`. A zero-option request still warns even for a full mask. Having ordinary
rights but no requested options yields a warning and no grant, not a permission
token. No rights can fail `42501`. Warning masks describe authority, not the
number of changed ACL entries: fully authorized repeated or wrong-origin
revocations are quiet.

Plain REVOKE removes the selected source's use and options; GRANT OPTION FOR
removes only its options, preserving use. Default/explicit RESTRICT protects
unsupported dependent privileges with `2BP01` and Hint
`Use CASCADE to revoke them too.` CASCADE recursively removes only the bits
whose delegation support was lost, preserving other bits, independent origins
and alternative inherited grant-option support. Ordinary alternate use or PUBLIC
access does not protect delegation, but can still authorize a downstream reader.
Circular grant options fail `0LP01`. Dependency checks stay within each table or
individual column ACL, not a combined graph. Table options participate only in
column grantor selection: revoking an upstream table option can preserve a
distinct-origin downstream column grant; a same-column dependent chain is
protected by RESTRICT and removed by CASCADE. Table REVOKE also processes the
corresponding SELECT/INSERT/UPDATE/REFERENCES grants to its targeted recipients
in each column, selecting each source from the original requested mask and
original table ACL, not its already reduced result.

Utilities affect zero rows. Autocommit Go Prepare resolves no objects/roles and
emits no notices; Execute resolves fresh identities and publishes one atomic
catalog candidate. Late missing targets/columns/grantees, option errors,
RESTRICT failures and native cooperative cancellation publish no earlier ACL
changes. The native adapter's pre-canceled context guarantee does not imply that
lib/pq must reject a fast PostgreSQL utility before it completes.
Reached warnings remain observable before a later failure. Explicit and implicit merged
column requests traverse ascending signed stable attribute numbers, not textual
request order or column-name order; duplicate column mentions warn only once.
Table REVOKE emits its table warning before system attributes
`tableoid,cmax,xmax,cmin,xmin,ctid`, followed by live columns in attribute order.
This does not add system-column query support.

Real consumers are SELECT/source expressions, assigned INSERT/UPDATE columns,
DELETE, RETURNING, row-locking SELECT, explicit LOCK, and MAINTAIN-based REINDEX
and nonconcurrent REFRESH MATERIALIZED VIEW. REFRESH requires caller MAINTAIN,
not target SELECT or schema ownership. DATA's frozen producer executes as MV
owner, including nested security_invoker sources, identity expressions and live
sequence authority; SESSION_USER remains caller. Owner/current-schema context
is operation-local and restored on success/failure before caller publication
fences. Caller source/sequence rights cannot rescue denied owner rights.
WITH NO DATA needs no producer/source/sequence rights or draws.
TRUNCATE supplies an existing LOCK alternative; REFERENCES/TRIGGER are stored
and delegated without inventing FK/trigger commands. Table INSERT does not
grant sequence rights: SERIAL defaults can still fail sequence `42501`, whereas
IDENTITY's internal generation remains supported. Direct sequence grants,
including TABLE syntax resolving a sequence, remain native `0A000`; schema/type/
routine grants and defaults are outside this surface.
Object utilities remain unsupported in native explicit read-write transactions.
Read-only execution first fails `25006` for GRANT/REVOKE, before object lookup.
Explicit relation/column ACLs make native Dump fail before output; Restore does
not accept GRANT/REVOKE, and Reset removes object ACLs while retaining roles/membership.

Feature smoke (900 seconds per invocation):
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`
and
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.
The revoke counterpart selects `TestReferenceRoleRevokeFeatureSmoke` with the
same native and `REFERENCE=1` commands. See [testing](TESTING.md) for the bounded
family selectors and root-owned standalone revoke smoke.

New tables belong to current_user and have owner use rights, with no PUBLIC
table privileges. Central checks cover schema CREATE/USAGE, all actually used
SELECT projection/predicate/order columns, assigned INSERT/UPDATE columns, and UPDATE RHS/WHERE reads.
When a SELECT or INSERT column manifest is empty, table privileges or the
corresponding privilege on **any live column** suffice. Empty/filtered INSERT
sources still require source SELECT authorization; grants on dropped columns
never qualify. UPDATE needs SELECT only on actual RHS/WHERE reads, not on
columns merely retained.
All four SELECT row-locking clauses additionally need UPDATE privilege on at
least one live column of each OF-selected physical source (all applicable sources
when OF is absent), including unprojected targets and empty/filtered results.
Unselected siblings still need used-column SELECT but no new UPDATE privilege.
FROM VALUES aliases have no physical UPDATE/grant requirement. Selection is per
SQL occurrence, including repeated aliases of the same table. This does not
require UPDATE on projected/predicate columns. Prepare checks namespace/shape;
Execute reevaluates current authority, including held prepared statements.
Sequence row marks first require sequence SELECT and UPDATE privileges; a
SELECT-only actor receives42501 `permission denied for sequence s` before the
42809 relation-kind error. With both rights (or owner/superuser authority),
locking a sequence is still prohibited at Execute. No sequence column-ACL
alternative is invented, and Prepare checks neither executor ACL nor row-mark kind.
An unselected sequence sibling of a marked query still rejects42809 during
reference-rowmark kind validation, without an invented UPDATE requirement,
tuple grant or latest refresh on it. This is distinct from selecting a sequence
for a physical rowmark. NOWAIT/SKIP do not weaken any ACL or relation-kind check.

For OF-selected marked view descendants, the extra base UPDATE check uses that
boundary's current owner, or the original invoker for security_invoker definitions
even below an ordinary owner view. Stored/inherited occurrence targets combine
without granting UPDATE requirements to unselected siblings. Named-view checks
remain unchanged; an ordinary read's stored mark does not invent named-view UPDATE.
An UPDATE grant on an unprojected live base column suffices. Prepare performs no
executor ACL checks; Execute rechecks before capture and after view preflight.
CREATE VIEW stores clauses without executing the body or requiring base SELECT/
UPDATE. These relation principals never change function/sequence evaluation
identity. Inner qualification still precedes outer error-producing expressions;
locking adds no predicate pushdown or security-barrier reorder.
Schema USAGE is checked during relation lookup, including preparation: qualified
references to an inaccessible schema fail with `42501` / `permission denied for
schema public` before column/input binding. Unqualified lookup omits inaccessible
public, so an otherwise unresolved name fails with `42P01`; implicit pg_catalog
lookup is preserved. Ordinary table/column ACLs remain execution-time checks:
SELECT/INSERT/UPDATE preparation does not cache permission, and execution reevaluates
the current principal, returning `42501` / `permission denied for table secret`
without synthetic diagnostic object fields. Explicit empty ACLs remove ordinary
owner use rights, not owner authority or implicit grant options.

Explicit LOCK uses table/view-level ANY alternatives: MAINTAIN, UPDATE, DELETE
or TRUNCATE permits all eight modes; INSERT permits ACCESS SHARE/ROW SHARE/ROW
EXCLUSIVE; SELECT permits ACCESS SHARE only. Native table ACL values represent
MAINTAIN/TRUNCATE on tables/views, never columns/sequences. PUBLIC/default owner
rights apply, but explicit empty owner ACLs deny use; superusers remain allowed.
Named view denials say `permission denied for view v`. Bound view sources use
ordinary-view owner or original security_invoker identity without extra schema
lookup. Direct sequence LOCK rejects kind before ACL. Public SQL table/column
GRANT/REVOKE is supported; no public direct ACL mutation API is exposed.
Shared ACL validation rejects grant options for PUBLIC at mutation, staging and
import boundaries.
After successful namespace lookup, parse/binding literal type input and typed
parameter input precede executor table permission checks, as at PostgreSQL's
Parse/Bind boundaries.
Immutable integer assignment casts fold during execution planning, after successful preparation
but before executor permission checks: out-of-range INT4 constants yield
`22003` (`integer out of range`), and invalid typed input can yield `22P02`
before permission denial. Valid nonowner input is denied `42501` without
mutation; executor checks precede protected row reads and runtime constraints.

ALTER COLUMN TYPE retains owner-only table authority and unchanged table/column
ACLs. Supported CHECKs are rebound to current destination types and provider/
attribute identities; subsequent INSERT/UPDATE checks the current definition.
TYPE validation uses the statement's coherent provider/comment snapshot, not
fresh per-row metadata. An ALTER CHECK violation identifies Schema/Table/Constraint
without failing-row Detail; it must not acquire DML's privilege-filtered row
detail. Explicit-ACL native Dump remains rejected rather than losing grants.

DROP dependency DETAIL preserves object and column identities, PostgreSQL
relation-name quoting, raw decoded column names and object-identity order
(not alphabetical description order). Failed batches preserve all roles and
membership endpoints, including when a later target has an object dependency.
Owned indexes follow their parent table's shared owner dependency; role DROP
reports the table rather than redundant index ownership entries.
Retained identity Rows keep their
original NAME metadata and values across session switches and native Reset/Restore,
while new execution observes the current session identity.
NAME identity results use the same catalog-owned typed values and retained
snapshots as ordinary NAME columns (builtin OID19, C collation950); stored NAME
values preserve spelling rather than applying role/identifier normalization.

**Bootstrap divergence from fresh PostgreSQL 18:** canonical native public is
owned by `pg_database_owner` and has an explicit host-convenience PUBLIC
USAGE|CREATE ACL; pg_catalog has PUBLIC USAGE only. The CREATE default is not
PostgreSQL's fresh default and not a generic default-allow. The reference harness recreates public, so ordinary-owner
fixtures explicitly run `GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC` on
the privileged PostgreSQL setup connection only.

The dual-mode `TestReferenceRole` family checks direct/prepared aliases and
diagnostics, NAME metadata, held prepared statements across switches,
owner/nonowner isolation, distinct trusted authenticators, reuse/replacement,
dependency failures and owner-preserving native controls.
`TestReferenceRoleMigratedSmokeControls` preserves the former `TestRoleSmoke`
owner restoration, legacy control acceptance, failed-Restore atomicity and role
cleanup assertions; creation/authentication/ACL/opaque connector assertions are
preserved by the corresponding existing reference families. The obsolete SQL
smoke copy is removed; its command helper remains for unrelated legacy callers.

Focused nonSQL ACL/unit checks use
`make check RUN='^Test(Role|Authorization)'`.
Focused role SQL checks use
`make check RUN='^TestReferenceRole[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceRole[A-Za-z0-9_]*$'`.
The true-mode family also selects genuine tagged membership/authority companions.
Native trusted connectors and PostgreSQL SQL authentication are independent
public routes, not interchangeable login substitutes.
Trusted connector admission/opaque-key isolation and same-pool physical
replacement are cohesive separate definitions:
`TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation` and
`TestReferenceRoleTrustedConnectorIsolationAndReplacement`. Session identity
authentication/forms and held freshness likewise remain selected together by
`^TestReferenceSessionIdentity[A-Za-z0-9_]*$`, including
`TestReferenceSessionIdentityFormsAndAuthentication` and
`TestReferenceSessionIdentityFormsAndFreshness`. These splits retain the complete
original assertion union rather than replacing lifecycle checks with setup.

Source-consumer phases also retain original identifier DB.Exec/Query/Prepare
and held statements, pinned prepared role setup, QueryRow identity/sentinel
reads and Tx.Exec schema boundaries beside recorded Context companions.
PostgreSQL setup grants the source role the required schema USAGE before testing
input-versus-permission precedence; a missing relation is not an equivalent
diagnostic. Register tracked native DROP ROLE only after successful CREATE ROLE;
native DROP ROLE IF EXISTS remains unsupported. PostgreSQL role removal follows
fixture/schema teardown. These are original route and cleanup witnesses, not
new authentication or authority capabilities.


## Schemas and search path

`CREATE SCHEMA name [AUTHORIZATION role]`, authorization-only name inference,
`IF NOT EXISTS`, `ALTER SCHEMA name RENAME TO name`, `ALTER SCHEMA name OWNER TO role`,
and comma-list `DROP SCHEMA [IF EXISTS] ... [RESTRICT|CASCADE]` are supported.
CREATE SCHEMA can contain contiguous CREATE TABLE elements (no intervening
semicolons). All elements publish atomically and belong to the designated
schema owner. Other nested commands are not supported. Rename preserves
identities and storage; RESTRICT leaves dependents intact and CASCADE removes
the selected namespaces and their contained supported tables.
Conditional schema notices retain PostgreSQL diagnostics: CREATE IF NOT EXISTS
uses NOTICE 42P06; DROP IF EXISTS for a missing schema uses NOTICE 00000.
CREATE TABLE destination errors retain the target's one-based AST character
position, including an empty effective path and schema CREATE denial.


Qualified CREATE TABLE, SELECT and INSERT keep quoted name components distinct.
`search_path` is a physical-session setting, default `"$user", public`, shared
by SET/SHOW/RESET/current_setting/set_config. Missing and inaccessible entries
are skipped, decoded `$user` expands current_user, duplicate namespaces are
ignored, and pg_catalog is implicit first unless explicitly positioned.
`SELECT current_schema` and `SELECT current_schema()` return one NAME value,
NULL when no usable explicit namespace exists. Empty paths still permit
qualified references but unqualified creation fails 3F000. SQL SET renders each
string list element as a minimally quoted identifier: `SET search_path TO 'sp1,'`
stores `"sp1,"`, a single schema name, and `SET search_path TO ''` stores `""`,
an unusable empty name. Raw `set_config('search_path','',false)` instead stores
an empty path. Malformed raw identifier lists, such as `set_config` input `sp1,`,
fail 22023 without changing the previous value. Renaming a schema does not
rewrite stored search_path text.

New schemas have no PUBLIC rights. Owners have schema USAGE/CREATE; relation
SELECT/INSERT/UPDATE is independently checked. Qualified creation requires CREATE,
not USAGE. Unqualified creation chooses the first usable explicit namespace,
then checks CREATE without fallback. Database CREATE grants are not implemented:
standalone schema provisioning and rename require effective superuser authority.
The fixed native error label `stuntgres` in `permission denied for database
stuntgres` is diagnostic-only, not an attached database or DSN interpretation.
Native public retains the existing PUBLIC USAGE|CREATE convenience divergence.
Owner transfer updates the schema owner and its explicit ACL, not contained table ownership.
Pinned PostgreSQL 18.0 permits superuser transfer to ordinary NOLOGIN roles
without database CREATE and same-owner no-ops; this observed behavior takes
precedence over the ALTER SCHEMA manual's destination-CREATE wording.

Standalone ordinary-table `ALTER TABLE [IF EXISTS] target SET SCHEMA destination`
requires source USAGE, table ownership (not a schema-owner substitute) and
destination CREATE. Unlike table rename, it does not require source CREATE;
destination USAGE is not required. Same-schema no-ops still check CREATE.
Prepare performs no lookup/authorization/locking and emits no notices. Missing
conditional sources skip before destination checks. Table and all owned sequences
publish atomically with unchanged owner/ACL/column ACL identities; dependent
sequences do not introduce synthetic extra owner checks.

Schema owner transfer also substitutes explicit ACL grantor/grantee ownership
references and merges colliding grants atomically, without changing retained
snapshots, unrelated grants or absent versus explicit-empty ACL semantics.
Held INSERT rebinding from INT4 to TEXT retains its original INT4 input
parameter and assignment-coerces the value into TEXT on both backends
(held Exec9 succeeds with text `9`). Native Reset/Restore remints provider
descriptors without changing that immutable SQL input contract.

Prepared handles retain ASTs and frozen result/parameter signatures. Path
changes re-resolve; incompatible result rebinding fails0A000 before importing
parameters or publishing writes, while inputs retain their original SQL types.
This is not PostgreSQL's complete cached-plan
invalidation model: unchanged-path shadow creation and cached namespace-USAGE
caveats are not reproduced. Retained Rows keep original values and metadata
through rename/drop/Reset/Restore. Explicit transactions reject schema utilities
before effects. Absent pg_temp references do not allocate; a pending temporary
creation destination fails 0A000 rather than falling through to public.

Within a native SELECT/INSERT/UPDATE or semantic Prepare, current authorized name
resolution must equal the exact relation whose AS/RX lock was acquired. If an
earlier search-path schema gains a same-shaped shadow while acquisition waits,
native rejects with the ordinary `stuntgres: catalog replaced before publication`
conflict before returning metadata, importing parameters, reading or writing.
The caller may submit a new statement; the engine does not retry SQL. This is an
explicit difference from observed pinned PostgreSQL: direct SELECT/INSERT and
semantic SELECT Prepare reacquire the new shadow's lock; a warmed held SELECT
continues using the originally locked relation. Native held handles also reject
the within-statement mismatch; frozen signatures alone do not authorize an
unlocked relation. No PostgreSQL outcome is asserted here for held INSERT or
semantic INSERT Prepare in this shadow-creation sequence.

Dump writes every persistent user schema, including empty schemas, before
owner-grouped qualified tables and rows. Restore preserves independent schema
and table owners through private stage-only CREATE TABLE destination authority.
Private Restore INSERT uses captured bootstrap authority without stamping owners
or changing named owner directives/session identity; ordinary SQL INSERT retains
normal USAGE/table ACL checks. No ACL grants are synthesized or retained.
Unsupported explicit namespace,
table or column ACLs fail Dump before any output. Invalid Restore leaves the
target catalog untouched. Bootstrap-only public dump bytes remain unchanged.
Feature witnesses: `make check RUN='^TestSchemaSmoke$'`,
`make check RUN='^TestReference(Schema|SearchPath)[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReference(Schema|SearchPath)[A-Za-z0-9_]*$'`.
The migrated families retain resolved namespace/OID identities, NAME raw codecs,
actual held consumers and owner-bearing native control replay. After dropping a
peer's selected PostgreSQL role, the full invalid-role-OID record and SAME-peer
authorization reset are distinct from native retained identity semantics.




## Ordinary CREATE INDEX authority

CREATE INDEX resolves the target under existing namespace USAGE/search_path
rules and requires table ownership, even if the caller owns its schema or has
SELECT/INSERT/MAINTAIN privileges. Ownership is checked before the ordinary
Share wait and revalidated at grant. Namespace CREATE is separately required
for the initiating effective role. The index inherits parent schema/owner; its
name is not created in a different first search_path namespace.
Prepared handles retain no successful authorization or target snapshot.
These target checks apply after native read-only and explicit-transaction
eligibility; excluded explicit CREATE INDEX does not resolve a target or request
Share. Independent PostgreSQL controls use real GRANT/REVOKE and role changes
to prescribe owner success, data-grantee/schema-owner denial, namespace CREATE
denial, fresh held-Prepare authority and ownership rejection before writer waits.

Native Restore's private, authenticated-superuser/import-marker-validated
destination authority also replays index definitions for independent schema and
table owners, preserving named owner directives. This does not synthesize grants
or bypass ordinary CREATE INDEX authority.


## Row-security policy definitions — change note

Policy definitions are table metadata, not ACL grants or native row-security
enforcement. Names are unique per table; identical names on different tables
are independent. CREATE defaults to PERMISSIVE, ALL and PUBLIC. SELECT/DELETE
policies accept USING only; INSERT policies accept WITH CHECK only; ALL/UPDATE
accept both. Absent conditions stay absent, including an omitted ALL/UPDATE
WITH CHECK. ALTER replaces only supplied roles or conditions; rename preserves
identity and all fields. AS/FOR are fixed until drop/recreate.

CREATE, ALTER and rename require table ownership; existing-policy DROP requires
relation ownership. Superusers and live inherited owner membership qualify.
Data grants, schema ownership, CREATEROLE and SET-only membership do not.
The target uses ordinary namespace USAGE/search_path lookup and an
AccessExclusive lock. Authority is checked again after a lock wait and at
publication. Prepare caches neither a target nor successful authority and emits
no notices; held execution resolves current objects, identities and authority.
Native read-only execution fails `25006` before lookup; explicit transaction
policy utilities retain the native `0A000` boundary. PostgreSQL policy DDL is
transactional.

TO accepts named roles, PUBLIC, CURRENT_USER, CURRENT_ROLE and SESSION_USER.
Identity forms resolve at execution and store the actual role identities, not
session-dependent syntax. Duplicate named targets are retained, including
duplicate role-dependency detail lines. PUBLIC short-circuits later targets:
when mixed with other targets it emits WARNING `22023`,
`ignoring specified roles other than PUBLIC`, with Hint
`All roles are members of the PUBLIC role.` An invalid role encountered before
PUBLIC still fails. ALTER TO or policy DROP releases superseded target
dependencies; roles are not serialized or created by Dump/Restore.

Conditions must be Boolean under ordinary shared coercion. Scalar supported
functions, session-dependent expressions and supported subqueries are stored
without evaluating conditions or scanning source rows. Aggregates owned by the
policy frame fail `42803`, including calls syntactically inside a subquery whose
arguments refer only to the policy frame. Child-local aggregates remain valid;
signature and same-level nesting errors retain their ordinary precedence.
Policy-level windows fail `42P20`. Parameters, including nested parameters,
fail `42P02`. Definition errors use the PostgreSQL policy diagnostic context.

DROP IF EXISTS emits NOTICE `00000` for a missing policy, table or schema;
a missing policy is skipped before owner checking. Existing-policy DROP still
requires ownership. **Rename differs:** release18 accepts
`ALTER POLICY IF EXISTS ... RENAME TO ...`, but its handler does not suppress
missing-table/policy errors or emit a skipping notice. Selective ALTER does
not accept IF EXISTS. Rename checks ownership and destination conflicts before
the old-policy lookup.

Policies have outgoing dependencies on their owning table, used columns and
represented expression providers, plus shared TO-role dependencies. Dropping
the owning table removes its policies automatically. Dropping a referenced
column, external relation/view or represented type/domain/collation/provider
requires RESTRICT/CASCADE handling; CASCADE removes dependent policies, not
their surviving owner tables. Dropping a policy leaves its providers intact.
Relation retirement includes its owned row type and array identities: a policy
referencing either through a REGTYPE value participates in the same captured
CASCADE closure, while independent sibling policies and owner data survive.
Dependency-driven policy removal acquires AccessExclusive on each surviving
owner table before staging removal; a canceled wait preserves providers,
policies and rows.
Relation/column rename retains identities and updates exported spelling.
Changing the type of a referenced column fails `0A000`,
`cannot alter type of a column used in a policy definition`.
Policies are not inherited by child tables.

Dump captures coherent persistent definitions before external writer I/O and
emits owner-grouped policies after their persistent providers. Restore replays
parameter-free CREATE POLICY through the real binder and atomic private stage.
Named owners and target roles must already exist; final installation preserves
principal provenance rather than adopting a recreated same-name role.
Trusted private creation authority covers ownership and provider access without
changing policy TO semantics or caller session identity. CURRENT_USER and
CURRENT_ROLE resolve to the actual effective stage-session role; SESSION_USER
resolves to its actual session role. A named SET SESSION AUTHORIZATION directive
therefore makes all three targets that named owner, not the bootstrap importer.
Reset removes persistent policies and their dependencies while retaining live
TEMP policies with their tables. Incompatible live TEMP dependencies prevent
replacement; persistent policies depending on TEMP objects cannot be exported.
Failed I/O, binding, cancellation or final authority/provenance checks preserve
the original catalog. Explicit-ACL Dump remains unsupported.
Provider-only TEMP policies also fence replacement, even without a relational
subquery. Persistent domain/collation/sequence and REGTYPE identities cannot be
silently rebound to same-name replacements. Supported TEMP collation, sequence
and row-type providers retain their identities through Reset/Restore; TEMP
domain creation remains outside the native surface.

**No native filtering, proposed-row checks, enablement/force toggles or
default-deny enforcement is provided.** The separate pinned PostgreSQL
default-deny witness uses an enabled table and a nonowner; native metadata
round trips do not certify that enforcement.

