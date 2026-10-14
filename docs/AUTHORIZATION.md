# Native authorization and principal ownership

[README](../README.md) · [Driver](DRIVER.md) · [Native values](TYPES.md)

PostgreSQL 18's [role](https://www.postgresql.org/docs/18/user-manag.html) and
[privilege](https://www.postgresql.org/docs/18/ddl-priv.html) documentation is the
SQL authority. Native authorization below describes implemented authority,
publication and compatibility boundaries, not full PostgreSQL compatibility.

## Trusted admission and principal identity

Each engine starts with the trusted native `postgres` bootstrap principal
(identity 10) and sixteen predefined PostgreSQL roles. `OpenConnector` uses that
bootstrap principal. Principal identities are opaque, engine-owned and never
reused after role deletion, Reset or Restore. PUBLIC is an ACL grantee, not a
principal or membership endpoint.

```go
d := stuntgres.NewDriver()
// Provision a login-capable role using an admin pool on the same exact DSN.
connector, err := d.OpenConnectorAs("my fixture", "application_owner")
if err != nil { /* handle host assertion error */ }
ownerPool := sql.OpenDB(connector)
defer ownerPool.Close()
```

`OpenConnectorAs` is a trusted host identity assertion, not password
authentication. It does not parse credentials or change DSN naming. Its role
argument is an exact decoded stored name. Each physical Connect resolves and
login-validates that role; admission failures are ordinary Go adapter errors,
not network SQLSTATE diagnostics. Login and physical connection limits apply to
new admission. Password metadata and expiry do not authenticate native sessions.

Healthy pool reuse retains selected identity and settings; physical replacement
starts with the connector's authenticator and captured defaults. Hold `*sql.Conn`
for principal-specific work. Connectors sharing a Driver and exact opaque DSN
share data, never session state.

## Immutable role authority

Role attributes are direct stored properties, not inherited copies. Membership
has independent inheritance, identity-selection and administration authority.
Live inherited paths supply object privileges and owner authority; identity
selection follows its own eligible paths. Prepared execution resolves current
authority rather than caching a successful decision.

Native password metadata uses SCRAM-SHA-256 verifiers with 4096 iterations,
random 16-byte salt and derived stored/server keys, with PostgreSQL-style
SASLprep or original-byte fallback. Recognized encoded MD5/SCRAM metadata is
retained. Expiry uses captured temporal input authority and distinguishes absent,
finite and infinite values. There is no native password handshake or
password-encryption/iteration configuration provider.

Role and membership utilities remain excluded from genuine native explicit
transactions (`0A000`); modifying read-only execution rejects with `25006` before
lookup. Native role deletion and role comments retain actual-superuser gates,
not PostgreSQL's broader role-administrator eligibility. Conditional role deletion,
role rename, role-specific setting defaults and `pg_authid`/`pg_shadow` remain
excluded. Public role summaries expose direct attributes with masked passwords;
no inherited attribute flags or password verifier projection is synthesized.

### Package ownership

The internal [authorization package](../internal/engine/authorization/README.md)
owns immutable role descriptors and membership graphs, predefined identities,
attribute/password algorithms, ACL grant/revoke leaves and creation-default
privilege leaves. It does not import the engine or own locks, sessions, catalogs,
identity allocation or publication.

The engine embeds an inert provenance domain and retains each child role root
by value in its private immutable publication record. Authorization captures
retain that exact record and its paired catalog, including the actual attached
database owner; stamps describe the captured publication, not later engine state.
Read capture copies neither records nor backing graphs. Private candidates return
root values, and only the engine installs or advances their publication revision.
Catalog namespaces/default-policy storage, object/column checks, typed summaries,
session journals and class-specific cleanup remain with their existing owners.
This package cutover adds no SQL syntax, provider or authentication capability.

For `pg_group`, child `Read.GroupRole` validates the already-enumerated role's
NOLOGIN identity without another descriptor lookup; `Read.GroupMember` checks a
live, same-domain, nonzero, OID-representable endpoint and matching descriptor.
The engine uses those same scalar rules in the parent loop and both direct-edge
passes. Typed NAME/OID/member arrays, origin multiplicities, count limits,
per-entry cancellation and returned-row snapshots remain engine-owned. Private
malformed-root units exercise these production rules, not a test-side filter.

## Predefined role capabilities

The role authority seeds fixed identities. Membership never copies superuser or
row-security-bypass attributes. Capability checks use the same live inherited
closure as ordinary ACLs, including held prepared execution.

| Role | Identity | Native boundary |
| --- | ---: | --- |
| `pg_checkpoint` | 4544 | No checkpoint mechanism |
| `pg_create_subscription` | 6304 | No subscription provider |
| `pg_database_owner` | 6171 | Implicit attached-database owner authority |
| `pg_maintain` | 6337 | Actual represented maintenance and lock authority |
| `pg_monitor` | 3373 | Ordinary membership in settings/stats/scan roles |
| `pg_read_all_settings` | 3374 | Reads existing settings; no protected mutation authority |
| `pg_read_all_stats` | 3375 | Identity only; no statistics provider |
| `pg_stat_scan_tables` | 3377 | Identity only; no scan extension/provider |
| `pg_read_all_data` | 6181 | Represented schema/data read authority |
| `pg_write_all_data` | 6182 | Represented schema/data write authority |
| `pg_read_server_files` | 4569 | No filesystem access |
| `pg_write_server_files` | 4570 | No filesystem writes |
| `pg_execute_server_program` | 4571 | No command execution |
| `pg_signal_autovacuum_worker` | 6392 | No workers/PIDs or signal functions |
| `pg_signal_backend` | 4200 | No backend signaling |
| `pg_use_reserved_connections` | 4550 | No reserved-slot tier |

The monitor's three initial edges record bootstrap provenance and are revocable;
Reset/Restore does not reseed them. Database-owner authority follows the actual
attached-database descriptor without a persisted membership row or invented
administration option. Predefined roles cannot be retired; failed mixed batches
publish no changes.

Data-role capabilities grant neither ownership nor grant options and do not
bypass native row security or protected catalog/provider writes. Maintenance
rebuilds actual index roots under existing transaction-owned exclusion, not a
successful no-op or an owner bypass. A granted role name never manufactures
unavailable server mechanisms.

Canonical pristine public has owner identity6171 (`pg_database_owner`) and
stored owner UC followed by PUBLIC USAGE, without stored owner grant options.
Ordinary roles need schema ownership or supported default-schema grants to
CREATE; catalog visibility never supplies that privilege. pg_catalog,
pg_toast and information_schema retain bootstrap ownership10; pg_toast's ACL
is NULL. Reset restores initialized namespace metadata while retaining the
shared role graph; imports remap predefined owner/ACL identities without
importing memberships.

Actual TEMP table/toast namespaces have owner10 and NULL ACLs, independent of
the role owning their objects. Only the admitted physical session's exact
active table namespace receives own-TEMP USAGE/CREATE; the reserved toast
companion and abandoned tokens do not. Bulk ownership/drop roots exclude both
TEMP members and protected catalog namespaces.
Qualified lookup still applies ordinary captured role authority: an effective
superuser may use another live or retained table-TEMP namespace, while SET ROLE
to an ordinary actor denies that access even with a bootstrap authenticator.
Namespace USAGE neither grants foreign TEMP creation nor adopts a retired token.

Conversions have real creating-user ownership and normal schema/role
dependencies. CREATE requires schema CREATE and the registered conversion
routine's EXECUTE privilege; registered routines have genuine PUBLIC EXECUTE
defaults through the same native ACL hook. ALTER/DROP check conversion ownership;
owner transfer uses current SET eligibility and target-owner schema CREATE.

Conversion and routine owners use the extracted authorization domain's opaque
principal references. Catalog `conowner` exposes only their numeric IDs; those
IDs cannot establish ownership or authorize replacement. Restore validates
captured and live principal authority, and ACL publication uses the same paired
catalog/role read as other installed objects.

Pinned seeds retain immutable routine/pair/default capability through legal
metadata changes. SQL schema/routine GRANT/REVOKE surfaces remain bounded:
reference PostgreSQL ACL setup is real, while native public schema setup uses
explicit schema-owner transfers and private ACL units exercise the real hook.
Catalog `conowner`/`proowner` describe actual principals, not provider data or
successful-execution authority cached by Prepare.


## Membership provenance and publication

Membership identity is `(role, member, grantor)`. Independent origins survive
revocation of another origin. Existing option updates retain omitted fields;
changing a role attribute does not rewrite historical edge options.
Administration traversal is independent of inheritance and identity selection.
Role/member cycles and administration-support cycles are separate checks.

Every statement mutates one private immutable candidate and publishes once, or
not at all. Grant target authority uses that evolving candidate; revoke planning
uses statement-entry authority and original rows across the target list. Recursive
dependency planning gets fresh actions per recipient. A later mutation of an
already changed original tuple can fail without publishing the graph. Notices
already reached remain delivered after a later failure.

Prepared reads check fresh memberships; already returned values and metadata do
not change. Reset/Restore retains cluster roles and memberships. Dump never
serializes them and Restore does not import membership utilities.

## Session identity lifecycle

Authenticated identity, session identity and effective relation principal are
separate authorities. Failed identity switches preserve session state.
Effective-role selection uses a typed principal-reference journal, not generic
setting strings. Commit, rollback, savepoints and physical teardown share its
existing lifetime machinery. Restoration uses already validated references;
revoked eligibility prevents fresh selection, not rollback/reset restoration.

Native public connectors initially align session and effective identity. The
internal `SessionStart.Defaults` role hook validates configured startup/reset
identity before physical publication; it is not a public connector option or a
generic setting. Identity selection does not apply the target's session defaults.
Routine security-definer invocation frames are not implemented; definer views
are not routine frames.

Identity projections use the common typed pipeline and execution-time capture.
Retained rows keep their original NAME metadata and values. Native host assertion
is not PostgreSQL authentication, so system authentication identity remains NULL.
Reset/Restore preserves physical-session identity; Dump exports none of it.

## Table and column ACL authority

All represented relation/column consumers use central ACL authority. Catalog
ACLs distinguish absent from explicitly empty values, retain independent
`(grantor, grantee)` origins and stable attribute identities, and merge ownership
collisions without discarding unrelated grants. Ownership and delegation remain
separate from ordinary use rights.

Delegation selects one reachable direct option source, not a union from different
roles. Dependency cleanup selectively removes unsupported origin bits while
retaining independent support. Whole-table checks do not synthesize a table
privilege from arbitrary column grants. Dropped attributes cannot authorize a
live consumer. ACL analysis remains mandatory even for empty sources/results.

Go Prepare performs no utility mutation, object lookup or notice delivery.
Execute resolves current identities and publishes one catalog candidate. Late
missing objects, dependency refusal, option errors or cooperative cancellation
publish no ACL prefix. Reached warnings remain observable. Held prepared readers
must deny after revocation and resume after regrant on the same handle.

Native rechecks complete binding authority after row/conflict waits; an ACL-only
descriptor replacement is not physical-storage replacement. A late revoke can
reject the whole statement, unlike PostgreSQL's admitted-command startup ACL
decision. Successful statement authority remains valid through commit.
Namespace and input validation retain their existing phase order before executor
ACL checks; permission errors do not invent diagnostic object fields.

Explicit relation/column ACLs make Dump fail before writer I/O; Restore does not
accept their mutation utilities. Reset replaces persistent ACLs while retaining
roles, membership and live TEMP objects/ACLs. Direct sequence/schema/type/routine
ACL mutation is not implied by represented table/column authority. Trigger
privilege metadata does not add a trigger executor.

## Creation-time default policy

Defaults are separate immutable catalog policy, not retroactive object grants.
Creation providers consume their own class, while generated row types and arrays
follow canonical parent associations. Ownership transfer rewrites dependent ACL
origins without consulting successor creation defaults.
Default-created schema entries are ordered by grantee OID (PUBLIC0 first),
then grantor OID without changing masks or mutating immutable default records.
This creation normalization does not reorder initialized builtin ACLs or
retroactively change existing objects.

Default creator/grantor/recipient references are real role dependencies even
before object creation. Scoped policy uses stable namespace identity; schema
retirement removes it and same-name recreation cannot resurrect it. Live TEMP
materialized ACLs survive replacement, while source TEMP-scoped directives are
stripped from persistent import projection.
Physical TEMP retirement also removes namespace-scoped directives while retaining
the table/toast namespace metadata; eager catalog/default snapshots stay unchanged.

Each operation captures one paired catalog/role authority, mutates one private
candidate and advances authorization only for a changed publication. Retained
messages expose earlier private policy to later creation and discard both on
failure. No routine/large-object creation provider is invented by stored defaults.

Default utilities retain native explicit-transaction exclusions. Dump rejects
nonbaseline stored policy, including stored-empty or missing-provider records,
before output. Restore excludes default-policy utilities; Reset clears persistent
policy while retaining live TEMP materialized ACLs. Native bulk cleanup does not
imitate PostgreSQL 18.0's stale queued-default-record defect.

The bounded table-privilege information-schema provider uses one coherent
catalog/roles/actor snapshot. It projects stored origins, not invented inherited
rows; held statements refresh and returned rows remain stable. Its native NAME
and VARCHAR transport differs from pq's information-schema carriers.

## Views and operation-local principals

Persistent views retain owner and ACL descriptors. Underlying relation checks
use each ordinary view's current owner or the original invoker for invoker views,
even below an outer owner view. Bound base reads do not repeat source-schema
lookup. Principal substitution never changes session identity or sequence/function
authority. Held prepared executions recheck live grants and owners.

Materialized refresh evaluates its frozen producer under the materialized-view
owner with operation-local schema context, restored before caller publication
fences. Caller privileges cannot rescue denied owner rights. An omitted producer
needs no source authority or runtime draws.

Isolated Restore uses validated authenticated-bootstrap lookup authority and a
private import marker/catalog for source occurrences, including post-wait
revalidation. Imported definitions keep named owners/session identity and use
current owner/invoker authority on later reads, never retained import elevation.

## Namespace resolution and prepared bindings

Physical-session search-path state and current principal determine authorized
lookup. Prepared handles retain ASTs and frozen input/result signatures, not
successful authorization or future data views. Path changes re-resolve; an
incompatible result binding fails before parameter import or writes. This is not
PostgreSQL's complete cached-plan invalidation model.

Within a native statement or semantic Prepare, current authorized resolution must
identify the exact relation whose lock was acquired. A shadow introduced while
waiting causes `stuntgres: catalog replaced before publication`, before metadata,
input import or data access. Native does not retry SQL or act on an unlocked
same-shaped shadow; pinned PostgreSQL may reacquire a differently resolved target.

Schema ownership is separate from contained-object ownership. Native standalone
schema provisioning/rename remains superuser-gated. Table namespace moves publish
with owned-sequence associations and unchanged owner/ACL identities atomically.
Restore preserves independent schema/table owners through private validated
creation/input authority without synthesizing ACLs. Dump rejects unsupported
explicit namespace ACLs before any output.

## Ownership and bulk cleanup

Ownership transfer follows canonical roots, row types, arrays, backing relations,
indexes and column-owned sequences in one publication. Unchanged ownership does
not allocate replacement ACLs. Independent inheritance/partition children retain
their own owners. Rows, codecs, comments, policies and generator state survive.
Direct catalog/backing-object mutation remains excluded.

Bulk reassignment and cleanup preflight complete principal unions and dependencies,
then install catalog/storage and optional membership candidates together.
Cleanup includes real default records, selected grant origins and policy role
references, without retiring roles or all endpoint memberships. Independent
inherited option support preserves downstream grants and their dependencies.
The canonical bootstrap principal is protected by identity, not a familiar name;
canonical public is not pinned against supported ownership cleanup.

The attached database has its own ownership and CREATE ACL authority. It is not
a multi-database cluster and is never deleted by bulk cleanup. Database ownership
and most ownership utilities remain autocommit-only; sequence ownership and
database CREATE ACL changes retain their existing transactional behavior.
Extension cleanup follows actual member/provider edges, not a generic ownership
shortcut; independently owned members require the complete extension/member union.

Every role snapshot carries its matching immutable catalog. Publication captures
the exact committed database predecessor separately from private database
authority. Finish and outer commit veto an owner-only replacement even with no
role revision or catalog epoch change. Candidate originals and savepoints retain
that fence. Manual sequence/enum producers reuse checked entry or post-wait
authority, never restamp an earlier successful check after refreshing metadata.

## Row-security metadata and enforcement limits

Policies are retained table metadata with real provider, stable-column and role
dependencies. Their expressions are bound but not evaluated natively. Policy
mutations use owner authority and the existing exclusive relation lock, rechecking
after waits and before publication; genuine explicit policy utilities remain
excluded. Dependency removal locks surviving owner tables before staging and
removes dependent policies without deleting surviving owner data.

Enablement and force flags are independent immutable table metadata with
transaction/savepoint/publication fences. Ordinary ACL checks remain separate
and first. Each view layer uses its owner/original invoker for ACL and row-security
authority without changing expression session identity. Materialized stored rows
are not re-filtered. Data-role capabilities do not bypass row security.

Native implements metadata-provable default deny, preserving normal empty-input
query processing and rejecting demanded write proposals before constraints.
When applicable permissive expressions require actual enforcement, execution
fails `0A000`, `row-security policy expression enforcement is unsupported`, even
for constant expressions. There are no constant shortcuts, filter/check evaluator
or full PostgreSQL row-policy parity. Held execution recaptures current authority
and flags; eager results retain their old capture.

Dump captures raw protected rows, persistent definitions and flags coherently;
Restore stages them under validated authority without changing target-role
provenance. Named owners and target roles must already exist. Live TEMP policies
and provider identities fence replacement; same-name provider recreation cannot
silently rebind them. Failed I/O, binding, cancellation or provenance preflight
leaves the catalog unchanged. Explicit-ACL export exclusions still apply.

## Foreign-key authority and disclosure

Definition admission captures declaring authority; later enforcement does not
substitute the final import owner or replay elevation. Existing foreign-key proof
is not invalidated retroactively by later ACL/comment changes.

Internal child actions run under affected child-owner authority and bypass child
row-security enforcement without changing initiating session identity or parent
selection authority. That enforcement bypass must not leak into error-detail
disclosure: reporting uses its own invoker/child-owner visibility and row-security
applicability. Key labels/values are withheld where required; generated action
Context is distinct from after-event diagnostics.

## Testing

Role/authorization mechanics use `make check RUN='^Test(Role|Authorization)'`.
Public role scenarios use `make check RUN='^TestReferenceRole[A-Za-z0-9_]*$'`
and the same command with `REFERENCE=1`. Session identity, trusted admission,
opaque-key isolation, pool replacement, ACL revocation/regrant and failed-Restore
atomicity are distinct witnesses, not interchangeable authentication routes.
Schema/path scenarios use `^TestReference(Schema|SearchPath)[A-Za-z0-9_]*$` in
both modes. See [testing](TESTING.md) and
[predefined-role oracle contracts](../reference/roles/README.md#predefined-role-oracle-first-contracts).
