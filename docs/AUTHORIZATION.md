# Roles, authorization and schemas

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Each engine starts with the trusted native `postgres` principal (LOGIN,
superuser). `OpenConnector` uses it. Role identities are opaque, engine-owned
and never reused after DROP, Reset or Restore. PUBLIC is an ACL grantee meaning
all existing and future principals, not a role or membership endpoint.

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
Native SQL object GRANT remains absent; object ACL tests use the central internal seam.
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
SET [SESSION] ROLE {name | 'exact role name' | NONE}
SET [SESSION] ROLE {TO | =} DEFAULT
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
DROP and COMMENT ON ROLE retain their existing superuser gates. SQL object GRANT,
role metadata catalogs, role rename and ALTER ROLE SET/RESET remain excluded.
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
password_encryption/scram_iterations setting or pg_roles/pg_authid projection is
introduced. LOGIN and CONNECTION LIMIT still govern new physical admission; see
[driver admission](DRIVER.md#trusted-role-admission-and-physical-limits).
CREATE/ALTER and other cluster-role utilities remain native explicit-transaction
0A000 boundaries; read-only Execute rejects modifying utilities with25006 before
lookup or authority. Prepared execution resolves fresh authority without caching it.

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
error; quoted/string `default` is a role name. Role selection is autocommit-only:
explicit transactions and SET LOCAL ROLE fail native `0A000` without mutation.
PostgreSQL supports transactional/local role lifetime; native does not claim parity.

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
without synthetic diagnostic object fields. Explicit internal empty ACLs remove
ordinary owner use rights, not owner authority or implicit grant options.

Explicit LOCK uses table/view-level ANY alternatives: MAINTAIN, UPDATE, DELETE
or TRUNCATE permits all eight modes; INSERT permits ACCESS SHARE/ROW SHARE/ROW
EXCLUSIVE; SELECT permits ACCESS SHARE only. Native internal ACL values represent
MAINTAIN/TRUNCATE on tables/views, never columns/sequences. PUBLIC/default owner
rights apply, but explicit empty owner ACLs deny use; superusers remain allowed.
Named view denials say `permission denied for view v`. Bound view sources use
ordinary-view owner or original security_invoker identity without extra schema
lookup. Direct sequence LOCK rejects kind before ACL. SQL GRANT and public ACL
mutation APIs remain unsupported.
Shared ACL validation rejects grant options for PUBLIC at mutation, staging and
import boundaries. SQL ACL mutation is not exposed.
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
Retained identity Rows keep their
original NAME metadata and values across session switches and native Reset/Restore,
while new execution observes the current session identity.
NAME identity results use the same catalog-owned typed values and retained
snapshots as ordinary NAME columns (builtin OID19, C collation950); stored NAME
values preserve spelling rather than applying role/identifier normalization.

**Bootstrap divergence from fresh PostgreSQL 18:** native public has an
explicit host-convenience PUBLIC USAGE|CREATE ACL; pg_catalog has PUBLIC USAGE
only. This is not PostgreSQL's fresh public CREATE default and not a generic
default-allow. The reference harness recreates public, so ordinary-owner
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
`make quickcheck PACKAGES='./internal/engine' RUN='^Test(Role|Authorization)'`.
Focused role SQL checks use
`make quickcheck PACKAGES=./reference RUN='^TestReferenceRole[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRole[A-Za-z0-9_]*$' VERBOSE=0`.
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
Feature witnesses: `make quickcheck PACKAGES=./smoke RUN='^TestSchemaSmoke$'`,
`make quickcheck PACKAGES=./reference RUN='^TestReference(Schema|SearchPath)[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Schema|SearchPath)[A-Za-z0-9_]*$' VERBOSE=0`.
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

