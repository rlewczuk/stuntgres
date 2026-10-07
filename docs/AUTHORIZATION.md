# Roles, authorization and schemas

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Each engine starts with the trusted native `postgres` principal (LOGIN,
superuser). `OpenConnector` uses it. Role identities are opaque, engine-owned
and never reused after DROP, Reset or Restore. PUBLIC is an ACL grantee meaning
all existing and future principals, not a role or membership endpoint.

```text
CREATE {ROLE | USER | GROUP} name [WITH] [LOGIN | NOLOGIN]
DROP {ROLE | USER | GROUP} name [, ...]
SET [SESSION] AUTHORIZATION {name | 'exact role name' | DEFAULT}
RESET SESSION AUTHORIZATION
SELECT {session_user | current_user} [, ...]
```

ROLE/GROUP default NOLOGIN; USER defaults LOGIN. Duplicate options fail `42601`
at execution, duplicate names `42710`, missing DROP targets `42704`. DROP is
statement-atomic across its list, rejects owner/ACL/persistent dependencies
(`2BP01`), and removes membership endpoints through the internal authority.
SQL role membership, SET ROLE, ALTER ROLE/GROUP, passwords, special role
attributes and GRANT/REVOKE are not implemented; recognized adjacent syntax
fails `0A000`, never successful no-ops. Role administration requires the
effective superuser in this bounded foundation.
Valid `DROP {ROLE | USER | GROUP} IF EXISTS name [, ...]` is likewise excluded:
it fails positioned `0A000` at `IF` before any script statement executes,
including when every target is absent. Ordinary unquoted or quoted role names
`if` and `exists` remain valid. Malformed conditional drops and `CASCADE`
(which is not PostgreSQL role-drop syntax) fail `42601`, not a capability error.
Role names reuse PostgreSQL's ordinary column-name keyword categories;
nonreserved `local`, `session` and `role` remain names. Reserved `WITH` is
only an optional option introducer, not a role name or a repeated option.

SET/RESET change session_user and current_user together. Only the **originally
authenticated** superuser may select another principal (including NOLOGIN);
an ordinary authenticated role may select itself or reset/default only.
Failed switches preserve both identities and application_name. Identity
projections have NAME metadata, exact lowercase labels and one string row;
prepared projections read execution-time identity. Ordinary NAME columns follow
the [NAME value contract](TYPES.md#name). Exec of identity SELECT reports one selected row;
role and authorization commands affect zero rows.

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
Schema USAGE is checked during relation lookup, including preparation: qualified
references to an inaccessible schema fail with `42501` / `permission denied for
schema public` before column/input binding. Unqualified lookup omits inaccessible
public, so an otherwise unresolved name fails with `42P01`; implicit pg_catalog
lookup is preserved. Ordinary table/column ACLs remain execution-time checks:
SELECT/INSERT/UPDATE preparation does not cache permission, and execution reevaluates
the current principal, returning `42501` / `permission denied for table secret`
without synthetic diagnostic object fields. Explicit internal empty ACLs remove
ordinary owner use rights, not owner authority or implicit grant options.
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

`go test -count=1 -run '^TestRoleSmoke$' ./smoke` checks direct/prepared aliases
and diagnostics, NAME metadata, held prepared statements across switches,
owner/nonowner isolation, genuinely distinct trusted authenticators,
reuse/replacement, dependency failures and owner-preserving native controls.
It asserts role creation defaults, owner access versus 42501 rejection,
owner restoration, legacy control acceptance, atomic failures and successful
role cleanup. Go test reports the result.

Focused checks: `go test -run '^Test(Role|Authorization)' ./...`,
`go test -count=1 -run '^TestRoleSmoke$' ./smoke`, and the genuine pinned suite
`go test -tags reference -run '^TestReferenceRole' -count=1 -v ./reference`.

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

Schema owner transfer also substitutes explicit ACL grantor/grantee ownership
references and merges colliding grants atomically, without changing retained
snapshots, unrelated grants or absent versus explicit-empty ACL semantics.
Held INSERT rebinding from INT4 to TEXT is an explicit bounded difference:
native rejects `0A000 cached plan must not change parameter type` before
parameter import/write; pinned PostgreSQL retains the INT4 parameter and
assignment-coerces the value into TEXT (held Exec9 succeeds with text `9`).

Prepared handles retain ASTs and frozen result/parameter signatures. Compatible
path changes re-resolve; incompatible rebinding fails 0A000 before importing
parameters or publishing writes. This is not PostgreSQL's complete cached-plan
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
Feature witnesses: `go test -count=1 -run '^TestSchemaSmoke$' ./smoke` and
`go test -tags reference -count=1 -run '^TestReferenceSchema' ./reference`.



