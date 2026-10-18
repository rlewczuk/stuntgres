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
Ordinary roles need schema CREATE through direct/default grants or ordinary owner
rights to CREATE; catalog visibility never supplies that privilege. pg_catalog,
pg_toast and information_schema retain bootstrap ownership10; pg_toast's ACL
is NULL. Reset restores initialized namespace metadata while retaining the
shared role graph; imports remap predefined owner/ACL identities without
importing memberships.

Actual TEMP table/toast namespaces have owner10 and NULL ACLs, independent of
the role owning their objects. The admitted physical session's exact active table
namespace always supplies own-TEMP USAGE; CREATE follows its current database
TEMP privilege. The toast companion and abandoned tokens do not receive this
special authority. Bulk ownership/drop roots exclude both TEMP members and
protected catalog namespaces.
Qualified lookup still checks captured role authority and real schema ACLs,
including explicit USAGE granted on another physical TEMP namespace. Namespace
USAGE does not expose foreign TEMP storage, grant foreign creation or adopt a
retired token; the separate native storage boundary remains in effect.

Conversions have real creating-user ownership and normal schema/role
dependencies. CREATE requires schema CREATE and the registered conversion
routine's EXECUTE privilege; registered routines have genuine PUBLIC EXECUTE
defaults through the same native ACL hook. ALTER/DROP check conversion ownership;
owner transfer uses current SET eligibility and target-owner schema CREATE.

Operators likewise have real creating-user ownership, schema/type/function
dependencies and role ownership references, including unfilled forward shells.
CREATE requires namespace CREATE, operand/result type USAGE and implementation
EXECUTE. There is no grantable operator ACL: demanded invocation admits the
actual function with fresh EXECUTE authority, including held prepared statements;
strict NULL and skipped demand follow the existing routine admission contract.
CREATE's ordered option extraction and required-function check precede operand
identity lookup; both supplied operand identities resolve before missing/postfix
checks, then operand USAGE is checked left-to-right before function lookup.
Qualified-namespace USAGE remains part of identity lookup. ALTER finds the exact
target, extracts every option (including preliminary name extraction for supplied
non-NULL values), then checks ownership before resolving surviving option names.
Malformed values can therefore precede ownership errors, but a valid extracted
nonexistent estimator or invalid symbolic reference cannot bypass ownership.
ALTER/DROP require ownership, linked-operator updates check the peer owner, and
OWNER TO uses existing SET ROLE eligibility and target-schema CREATE checks.
SET SCHEMA checks destination CREATE before operator ownership. REASSIGN OWNED,
DROP OWNED and DROP ROLE use the shared ownership/dependency lifecycle.
Commutator/negator are links rather than normal dependency edges; retiring a peer
clears reciprocal fields without cascading the surviving definition.
See [operator authorization scenarios and selected proof](../reference/operators/README.md#testing).


Conversion and routine owners use the extracted authorization domain's opaque
principal references. Catalog `conowner` exposes only their numeric IDs; those
IDs cannot establish ownership or authorize replacement. Restore validates
captured and live principal authority, and ACL publication uses the same paired
catalog/role read as other installed objects.

Pinned seeds retain immutable routine/pair/default capability through legal
metadata changes. SQL schema and routine GRANT/REVOKE use genuine ACL authority
independently of ownership; routine targets remain limited to actual owning providers.
Catalog `conowner`/`proowner` describe actual principals, not provider data or
successful-execution authority cached by Prepare.

Foreign-data wrapper and server ACLs have USAGE-only owner defaults, without
PUBLIC USAGE or stored owner grant options. The first legal ACL edit materializes
that baseline, including an effective no-op owner self-GRANT. Raw NULL and
explicit-empty ACLs remain distinct; revoking ordinary owner USAGE denies
foreign-object creation, while implicit owner grant authority permits recovery.
Registered foreign handlers and validators retain ordinary routine EXECUTE ACLs
and grant-option provenance. Internal SQL/MED dispatch does not consult those
ordinary EXECUTE ACLs; a foreign scan does not acquire a new wrapper/server
USAGE or callback EXECUTE gate.
`DROP OWNED` removes received wrapper/server grants and unsupported dependent
origins through the same immutable ACL cleanup as other objects. Independent
grant origins and administrator-owned foreign metadata/options remain intact.


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

`pg_has_role` uses this same immutable authority, not an inquiry-owned graph.
Its six NAME/OID overloads distinguish unrestricted MEMBER reachability,
all-INHERIT USAGE and all-SET identity selection. The optional user defaults to
the expression's current user, including stored-view expression identity rather
than the view's relation-access owner. ADMIN follows unrestricted intermediate
membership paths to a terminal ADMIN edge; self is not implicitly ADMIN.
All WITH ADMIN OPTION / WITH GRANT OPTION aliases ask that ADMIN question alone.
Comma tokens are case-insensitive with outer ASCII whitespace trimming, not
interior whitespace normalization; all tokens are validated before predicates.

The strict BOOL inquiry treats names literally and reports unknown names,
including PUBLIC, as `42704`. SQL raw OIDs are not capabilities: absent unequal
endpoints return false, absent equal endpoints retain MEMBER/USAGE/SET self
truth (including zero), and a live actual superuser has all four predicates.
The narrow SQL boundary never passes invalid refs to trusted authorization
APIs or weakens their zero/foreign/stale-ref checks.

The access inquiries are scalar functions: DISTINCT, call-local ORDER BY and
FILTER produce analysis-time `42809` after the applicable child/type-resolution
checks. Matching fixed scalar VARIADIC notation is accepted, not treated as
array expansion. They do not supply aggregate semantics.


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
roles, membership and live TEMP objects/ACLs. Direct sequence, schema, type/domain
and genuine routine ACL mutation is supported as described below. Lookup-only
routine signatures do not acquire descriptors or execution through grants.
Trigger privilege metadata does not add a trigger executor.

## Sequence object ACLs

`GRANT` and `REVOKE ... ON SEQUENCE` use the existing immutable relation ACL
planner with a sequence-specific SELECT/UPDATE/USAGE mask. ALL grants exactly
these rights; new ordinary sequences have no PUBLIC rights. USAGE or UPDATE
permits nextval, USAGE or SELECT permits currval/lastval, UPDATE permits setval,
and SELECT permits reading the sequence relation. Table INSERT does not grant
sequence rights; identity-generated insertion retains its existing exemption.
Implicit and TABLE targets can also select sequences. Table-only privilege bits
are warned and stripped for those targets, while explicit SEQUENCE targets reject
invalid bits. ALL SEQUENCES IN SCHEMA selects only installed sequences, not tables
or future creations; ALL TABLES remains sequence-free. Expansion checks schema
USAGE separately from per-object grant authority and publishes no prefix on error.

Implicit/TABLE sequence targets also support SELECT on individual existing
columns. Whole SELECT takes priority; otherwise selected columns must each have
SELECT, and a constant/count projection needs SELECT on any live real column.
Column SELECT grants no nextval/currval/setval authority. Unsupported column
bits, including those in ALL(columns), warn0LP01 once per privilege item and are
stripped to SELECT; explicit ON SEQUENCE column lists remain invalid0LP01.
Whole SELECT/ALL revocation also removes applicable column grants/options through
the existing dependency graph. Fresh execution of the same held reader observes
revoke/regrant without changing the generator.

Column-grant preliminary authority excludes whole-object-only rights: sequence
USAGE or table DELETE alone cannot turn an unauthorized column GRANT/REVOKE into
a warning. A column-applicable whole UPDATE right can, without granting options.
Mixed whole/column diagnostics retain whole-first ordering; any later target
error leaves all earlier grant/revoke effects unpublished.

Delegation, grant-option-only revocation and selective CASCADE use the same
origin-aware ACL authority as table grants. Publication changes the sequence
planning stamp without advancing or resetting its generator or physical-session
sequence memories. The same held consumer denies after revoke and resumes
after regrant. Sequence object grants remain unsupported in explicit native
transactions; read-only rejection precedes resolution. Explicit sequence ACLs
retain the existing pre-output Dump rejection and Restore utility boundary.

## Schema and type object ACLs

Schema USAGE controls qualified lookup and search-path accessibility; CREATE
controls creation without granting rights on existing tables. Ordinary schemas
start without PUBLIC privileges; initialized public/catalog ACLs remain distinct.
Schema ACL publication advances namespace generation, and late missing targets
leave every earlier target unchanged.

TYPE and DOMAIN targets use USAGE, including their class-specific ALL. TYPE may
target a domain, while DOMAIN rejects non-domains. Types default to PUBLIC USAGE.
True arrays cannot receive their own grants; array dependency admission checks
the element's authority. Builtin types have genuine bootstrap ownership and ACL
principal references, just like user types.

TYPE/DOMAIN grant targets are ordinary catalog names, not declaration types.
`INTEGER` therefore names an actual object named `integer` through search_path;
`pg_catalog.int4` is the builtin identity. Quoted names retain their case.
`INTEGER[]` is not target syntax; an actual array catalog name still receives the
released array-ACL rejection. Routine signature arguments instead share valid
declaration aliases, array/modifier and compound-type builders. Names and IN,
OUT, INOUT or VARIADIC modes are retained, with OUT-only arguments excluded from
the input identity. This does not expose a routine creator or missing provider.

USAGE is checked when creating persistent table/domain/composite dependencies,
not while reading existing values or performing ordinary scalar casts. Grants,
options and DROP OWNED cleanup retain actual provenance. Native replacement
remaps builtin ownership and ACL principals; Reset rebuilds type defaults.
Nonbaseline type ACLs reject Dump before output, and Restore does not accept GRANT
utilities. Schema/type object grants retain the native explicit-transaction limit.

## Routine identity and execution admission

Successful native selectors retain the exact released declaration independently
of physical argument usages: polymorphic, variadic and defaulted formals are not
reconstructed from written arity, result type or a routine name. Genuine builtin
descriptors occupy one engine-owned backing array; immutable addresses remain
stable while ACL and owner changes publish ordinary copy-on-write descriptors.
An opaque descriptor pointer is selected-call metadata, never permission authority.

The finite native declaration inventory records genuine existing provider
selection, not every released name. It retains exact released OID, formal input
vector, result, kind, strictness, volatility, parallel, set-returning, default and
variadic metadata. Count-star versus count-expression, scalar/array array_agg,
defaulted normalize/make_interval, and specialized to_char inputs retain their
selected formals before physical lowering loses that information. Typed operator
and SQL-lowered calls retain actual oprcode identities, not name/arity guesses.
Implicit codec I/O, SQL value-function leaves and private support algorithms do
not automatically become ordinary caller-EXECUTE calls.

FUNCTION targets and ALL FUNCTIONS include actual scalar, aggregate and window
declarations; PROCEDURE selects procedures; ROUTINE includes both. ALL IN SCHEMA
selects current genuine owning descriptors only, not later installed extensions
or lookup-only neighbours. A known released but unimplemented target rejects
`0A000 routine privileges are unsupported for unimplemented routines in embedded
mode`. Missing/ambiguous targets keep resolution errors. Genuine conversion,
dictionary and extension-codec descriptors support metadata ACLs even where a
direct-call provider is absent.

Serial admission borrows each original binding's immutable reference/revision
manifest and a coherent catalog/role capture. CTE command activation retains that
original manifest. The capture remains live through held-cursor materialization,
foreign actions and index publication; terminal Finish clears it on every exit.
Native prepared statements retain analysis identity, not an executable plan or
permission-result cache. Invocation checks current authority before genuine
planning-time provider computation and before surviving executable calls.

SQL DECLARE initializes its executor under the declaration's coherent routine
authority. An existing ordinary cursor or HOLD producer retains that authority
through FETCH and promotion even after EXECUTE is revoked; a new DECLARE still
checks current permissions. Current original-reference/revision, relation-source,
prepared-program, DOMAIN/RI and publication fences remain separate. Producer
teardown releases the retained graph and authority; promoted values are data only.

The special one-input `current_setting` adapter decides strict NULL demand after
typed parameter import. A fresh bound NULL needs no outer invocation; a surviving
non-NULL call checks both current-authority fences. `set_config` is nonstrict and
still requires EXECUTE for NULL inputs. Existing explicit-cast argument and
two-input `current_setting` syntax boundaries are not expanded.

Writable-view grafts carry their defining immutable recipe on original stored
nodes. Their evaluator-local admission selects that exact recipe's dependencies;
user replacements resume the original statement manifest instead of inheriting
the enclosing view's authority. Routine EXECUTE remains the statement invoker's
permission. Existing relation/subquery owner rules and local RI-owner execution
contexts remain distinct, and no lazy consumer overwrites the statement identity.
Opaque stored-source execution instead borrows its already-validated immutable
defining relation on the existing captured source. Its evaluator uses that
definition's original dependency manifest, not the enclosing statement's unrelated
manifest. The pointer conveys neither current permission nor relation-owner
substitution; closing/detaching the source releases it.

SQL EXECUTE actual expressions likewise own their original binding manifest.
Their preflight, surviving-call admission and one evaluation occur during import,
not Prepare, without replacing the prepared body's statement manifest. Consumed
actual calls move into the existing command programme collection before their
local context ends. Both these unkeyed programmes and keyed lazy inquiry bodies
retain original call identities/actors for fresh readmission, not permission results.


Rule relation-owner authority likewise does not replace routine caller authority.
When NEW grafts a stored DEFAULT into an action query, only callable references
from that actual, validated column recipe are loaned into the command's original
manifest. The source relation certifies any retained-TEMP epoch bridge; a current
address/revision lookup cannot manufacture a replacement lifetime. Refreshed
physical virtual-column recipes use their current stable slots rather than
being translated through a view's older frozen layout twice.
Virtual generated reads explicitly enter the original generation recipe during
serial demand admission. Its own immutable dependencies establish provenance;
the current routine invoker, not table or view ownership, supplies EXECUTE.
Captured logical sources keep their own generation manifest and stable attribute
mapping. Empty/pruned query demand follows ordinary initialization rules rather
than a new zero-output permission waiver.

DEFAULT preparation is demanded even for an empty INSERT source. Generated and
CHECK recipes prepare on first actual tuple demand. Domain CHECK preparation
admits every surviving VALUE-dependent graph before publishing its command-local
programme or evaluating truth. Its existing map retains actual invoker/RI-owner
phase provenance and original consumed references; an initialized owner phase
cannot certify an invoker phase or vice versa. The live scope alone owns that
map, including a new FETCH or held-cursor precommit scope. TxRef/CommandID are
phase coordinates, not valid rights: coherent input/authority recapture and
post-wait publication readmit actual consumed calls without evaluating CHECK
truth again. An ordinary borrowed input/view capture is not an RI-owner phase.
Table and domain CHECK truth retain the same original recipe borrowed by their
preflight. Selecting those fields on the current evaluator does not add per-row
EXECUTE checks or change the actual invoker/RI phase; this preserves provenance
when an inquiry's first non-NULL body is demanded only during truth.


Index constant-key planning begins on the first real tuple, before predicate
truth, using the original callable/domain recipe. A false predicate still prunes
nonconstant key initialization and value evaluation, not an already demanded
non-NULL constant computation. Literal NULL/dead arms and zero-row inputs keep
their separate lack of demand. Pure no-call/no-domain structural programmes keep
their established cooked fast path. Current-command insertion/unique records or
local build/projection/control-repair own the programme and lazy consumed-call
footprint, never immutable index/hash roots or prepared statements. Actual blocking
UNIQUE/completion waits readmit it under fresh authority before catalog drift can
discard/rebind the old programme. Reconciliation also checks it when the predicate
prevented key readiness; no provider replay, whole-dependency ACL sweep, or per-row
privilege lookup occurs.
Compatible catalog-drift rebinding transfers only actually consumed original
call references, actors and diagnostic context into the matching existing private
programme. Original parent/index lifetime, revision and predicate/key recipe must
remain compatible; no prepared flags, executable values or permission result is
transferred. A second wait therefore cannot erase the first phase's footprint.
Callable predicate/key recipes reuse tracked insertion outputs before row-dependent
RETURNING, so publication consumes the produced values without a second provider
evaluation. Plain-column indexes retain their existing fast path. A constant
RETURNING planning error still precedes tuple/index demand; an actual index
EXECUTE denial precedes row-dependent RETURNING evaluation and publishes no row.
Multi-key aggregate DISTINCT uses caller-checked tuple equality; single-key
DISTINCT and ordinary sorting do not borrow that permission rule. Aggregate
support ownership does not create direct-call providers for lookup-only helpers.

NULLIF, simple CASE and distinct predicates retain the actual selected equality
declaration rather than a fictitious NULLIF/CASE procedure. Nonconstant empty
sources still initialize checked equality; strict literal NULL and eliminated
branches preserve their independently observed lack of provider demand. Grouping,
deduplication and checked window equality remain distinct from ordinary sorting,
BTORDER row ordering, aggregate-owner support, array search and implicit I/O.
Recursive CYCLE UNION retains user-key and mark equality identities separately:
the seed expressions contain only user columns, while the mark has its own
bound expression. Admission does not rewrite tuple layouts or turn the ancestry
path's recursive comparison into an invented ordinary SQL call.
Native structural PG_NODE_TREE grouping, including private recursive array
carriers, is not a fabricated PostgreSQL routine invocation or a general
exemption for real PostgreSQL equality families. Those private carriers do not
register a SQL pg_node_tree array type: ARRAY[adbin] remains an analysis-time
native0A000 unsupported-array-element boundary, versus PostgreSQL42704.

Genuine SQL invoker bodies are not limited to quote wrappers. The finite 40-root
closure covers padding, text/BYTEA/BIT bit length, numeric round/truncation and
unary numeric log/log10, date_part/age, temporal arithmetic, three-argument
similar substring, polymorphic quoting/concatenation, sleep, duration OVERLAPS,
four description inquiries, and commuted INET addition's genuine `inetpl` call.
Ordered body declaration links live on existing immutable procedure descriptors.
Binding/rendering/remap retain original references before folding or relocation;
current authority never comes from a raw address or captured ACL.
The four function-method conversions timestamp(date), float8(numeric), text(bool)
and text(inet) (also binary-relabelled CIDR) have existing embedded providers.
Instance-selected calls are checked; ordinary I/O helpers are not fabricated
EXECUTE dependencies, and no standalone provider is added by their metadata.

The seven implemented INET bitwise/arithmetic operators retain their released
routine identities. CIDR inputs use the existing INET coercion; INET subtraction
has two INET formals even though its result is INT8. Only `int8pl_inet` has a
SQL-body edge, to `inetpl`; C algorithms add none. Network constant planning
first preserves imported strict-NULL elimination, then admits the surviving call
before computation. This adds no standalone function dispatcher or IP algorithm.

The six implemented TID comparisons use their exact `(tid,tid)→bool` declarations.
Equality-derived grouping/conditional consumers retain `tideq`; NOT IN uses its
real `tidne` negator. Plain ordering keeps internal support authority rather than
acquiring a scalar comparison EXECUTE gate. These INTERNAL routines add no
SQL-body, I/O, physical-ctid or new type/runtime capability. Existing type USAGE
still governs new dependencies, not previously stored values.

The existing ACL value providers retain `acldefault("char",oid)`,
`makeaclitem(oid,oid,text,bool)`, `aclexplode(aclitem[])` and operator-backed
`aclcontains(aclitem[],aclitem)`. The expansion's four OUT fields are not formal
inputs; it remains strict/stable/SETOF RECORD. The three scalar providers are
strict/immutable. Constructors remain symbolic so explicit child authority and
stored provenance survive planning. Genuine scalar NULL elimination differs
from live RECORD/SETOF initialization; genuinely pruned FROM sources need no
invocation. Reading existing ACL data does not replay its constructor. These
INTERNAL declarations add no SQL-body/C-helper edge, standalone containment
dispatcher, ACL algorithm or I/O capability.

ALTER TYPE USING retains each transformation's original manifest through lazy
Eval and consumes the existing binder's catalog-snapshot demand once during
locked binding. Current row/invoker and prepared domain programs remain local;
no enclosing or sibling transformation can supply missing original provenance.

HASH/RANGE derived partition keys select each key's own original manifest and
owning descriptor at actual tuple demand. Statement-local prepared expressions
and consumed call graphs follow the existing actor/command/RI-phase and post-wait
readmission fences; they are not cached permission results. Parent routing,
direct-leaf membership, populated DEFAULT scans and changed-row replacement
repair share this boundary. Lazy SQL-body evaluation keeps the same recipe and
current invoker. Empty declarations/zero-input writes initialize no key program;
reading stored data does not replay it. LIST remains unavailable natively.

Immutable imported scalar arguments that are independent of rows, stages and
domains are execution-frame constants for SQL-body error context. This does not
evaluate arguments again or cache executable results or permissions. PostgreSQL
can retain a parameter-free numeric log/log10 body after inlining removed its
outer wrapper from a warm prepared plan; native admission retains the original
wrapper's current EXECUTE requirement. Fresh calls and parameterized custom
plans retain their independently checked current-authority behavior.

Execution-known typed NULL parameters can remove a strict scalar call after
its required child preflight. This is a narrow command-private proof, not general
parameter constant folding: original parameter/result/recipe metadata and lifetime
manifests remain. It does not sample row NULLs or domain VALUE, bypass domain
conversion barriers, or erase SRF/window initialization. Uninitialized parameter
slots supply no proof; ordinary immutable sibling errors still precede pruning,
and unused volatile siblings are not evaluated to discover NULL.
An independent imported NULL can establish that proof even when a different
argument contains a domain coercion. The outer expression's aggregate domain
property is not itself an argument barrier: discarded SQL coercions need not
run, while actual typed DOMAIN parameters are validated at input before planning.

Window initialization follows surviving window occurrences, not every analyzed
call slot. A window removed by a constant CASE arm has no callable EXECUTE
demand; command-private copies retain its original slot and lifetime metadata
without mutating the prepared definition. Independently lowered expressions
remain governed by their own surviving graph demand: removing a window call
does not erase a separately scheduled ordering expression or its errors. A
surviving window still initializes EXECUTE authority over an empty WHERE input.
Demand follows the current physical input width and owning query frame, including
writable-rule grafts; retained original call coordinates are identity metadata,
not the grafted result layout. Ordinary aggregate initialization is unchanged.

Strict NULL/dead-arm rules govern actual body demand. Nullable quoting still
demands its TEXT primitive while a NULL can remove its strict BOOL/INET cast.
OVERLAPS admits the outer wrapper before partial constant pair computation, then
the actually demanded addition; one NULL pair does not remove its nonstrict
endpoint body. Sleep body initialization is eager in the exercised empty-source
case. Description inquiries initialize their body only on first actual non-NULL
entry, using the existing statement owner serialized by transaction admission,
including lazy/cursor consumption. Each outer occurrence validates its own original
body lifetime before programme borrowing. Actual invoker and trusted RI-owner
entries remain distinct even for an equal outer descriptor and stored recipe;
fresh phase admission is not replaced by a same-root permission cache.

Native held execution always uses fresh authority. Independently observed
PostgreSQL old inlined quote plans and warm constant-key index plans can retain
earlier results; warm constant-predicate index use still requires body authority.
Those precise backend differences do not authorize native executable caching.
The six genuine BIT bitwise/shift operators retain released BIT formals (including
VARBIT coercion), unary (0,BIT), and INTEGER shift counts, separate from integral
operators and without a new runtime.

Address inquiries require EXECUTE on their selected inquiry, not on the routine
or other object whose identity is being described. Their three OUT-record
functions retain record initialization; strict scalar `pg_describe_object` can
instead disappear on a proven NULL. Function-FROM pruning consumes only the
existing proven-empty-input footprint after child preflight. It never removes
original dependency, schema, lifetime, relation-permission or lock traversal.
Target expressions, LIMIT0 and runtime-NULL calls retain their distinct demand.
BIT `get_bit` and `set_bit` use INTEGER offsets and canonical BIT formals,
including physical VARBIT values; BYTEA overloads retain their different offsets.

The two numeric `generate_series` declarations retain their separate two- and
three-NUMERIC identities. Omitted step1 is private computation, not a third
formal or a SQL call to the other overload. Strict SRF NULL/empty output still
requires surviving initialization authority; it is not scalar NULL folding.

JSON record calls likewise retain released full identities. The genuine
two-argument native populate calls select
`json_populate_record(anyelement,json,boolean)` and its recordset counterpart,
including the released legacy default-false third formal in GRANT metadata.
This does not add explicit third-argument execution. `json_to_record(json)` and
`json_to_recordset(json)` keep RECORD results and their scalar/SETOF distinction;
the former is strict, the latter nonstrict. Shape completion preserves original
selected Info, recipe and manifest rather than replacing formal identities with
a physical composite type. Internal conversion adds no codec/helper SQL ACL edge.

The five existing JSONB record providers retain separate released identities.
Populate/recordset and `jsonb_populate_record_valid` take exactly
`(anyelement,jsonb)`, with no legacy BOOLEAN default; to-record variants take
`(jsonb)`. Validity has a nonstrict BOOL result, not a fabricated RECORD shape.
Selected authority is captured after successful existing construction and remains
current through held/stored-view/write consumers, including false/NULL validity.
These associations add no JSONB runtime or stored-scalar serialization capability.

Installed intagg aggregation and enumeration are independent aggregate/function
identities, not EXECUTE aliases for core `array_agg` or `unnest`. Enumeration
remains SETOF during NULL planning, retagging and source admission. Both members
retain their original references and may have independently valid owners through
the existing ownership/import machinery; no new ALTER ROUTINE syntax is implied.
Installed pgcrypto cipher, byte-random, UUID and FIPS members use the same current
member authority. The installed UUID identity is separate from core3432 despite
shared computation; no sibling or C-helper EXECUTE edge is invented.


## Database, parameter and tablespace object ACLs

The attached database has CREATE, TEMPORARY and CONNECT (`CTc`) authority. A NULL
ACL has owner CTc and PUBLIC Tc; explicit-empty/owner-only ACLs do not recover
PUBLIC defaults. CONNECT checks the immutable authenticated principal only when
admitting a new physical session. Revocation neither disconnects an admitted
session nor makes SET ROLE a new connection. TEMP follows current effective role:
before namespace allocation, denial names the database; after allocation, lost
CREATE names the actual own TEMP schema. Existing own objects remain usable.
Database grants retain the existing database-only explicit-transaction exception.

Parameter SET/ALTER SYSTEM (`sA`) ACLs use sparse genuine class-6243 descriptors
and the existing role/delegation/dependency machinery. Canonical registered
aliases and dotted custom names share one target; late invalid targets publish
no prefix. Default-equivalent sparse state can disappear, while explicit empty
authority remains meaningful. SET/RESET/set_config consult the actual setting
registry and SET right; USERSET application_name does not become protected.
The read-only pg_parameter_acl relation projects actual allocated entries and
typed ACL provenance, including PUBLIC, grant options and explicit empty `{}`.
Default PUBLIC SELECT exposes all rows to non-grantees; ordinary current catalog
table/column SELECT checks apply independently of represented parameter rights,
including empty/FALSE/LIMIT0 and held execution. Captured catalog/role roots keep
already opened output immutable while fresh executions see completed grants.
ALTER SYSTEM metadata does not implement that native command, an unknown setting
or a has_parameter_privilege provider. Native catalog GRANT and explicit-
transaction parameter grants remain unsupported; catalog reads add neither.

Builtin tablespaces pg_default/pg_global have owning descriptors and CREATE ACLs.
The readonly pg_tablespace projection includes nullable spcacl before spcoptions.
Metadata grants do not add physical directories, user tablespaces or placement
execution. Parameter/tablespace principals participate in DROP OWNED, DROP ROLE,
ownership/remap validation and pre-output native Dump rejection. Shared database
and tablespace objects are not deleted merely to clean their ACLs.

These contracts are grounded in PostgreSQL
[REL_18_0 grant grammar](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/gram.y),
[procedure declarations](https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_proc.dat),
[operator associations](https://github.com/postgres/postgres/blob/REL_18_0/src/include/catalog/pg_operator.dat),
[executor expression admission](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/executor/execExpr.c),
[group equality](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/executor/execGrouping.c),
[aggregate authority](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/executor/nodeAgg.c)
and [TEMP namespace masks](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/namespace.c).
Documentation is not verification evidence; selected native and pinned real
proofs are listed in the owning reference inventory.

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

The attached database has its own ownership and CTc ACL authority. It is not a
multi-database cluster and is never deleted by bulk cleanup. Database ownership
and most ownership utilities remain autocommit-only; sequence ownership and
database object ACL changes retain their existing transactional behavior.
Extension cleanup follows actual member/provider edges, not a generic ownership
shortcut; independently owned members require the complete extension/member union.
The represented fuzzystrmatch members retain independent, typed owner
provenance just like pgcrypto members; transferring one function does not transfer
its sibling overloads or invalidate their unchanged provider semantics.


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

`row_security_active(text|oid)` shares the enforcement owner's metadata-only
eligibility predicate, stopping before policy selection or expression demand.
It observes enablement, FORCE, live inherited ownership and direct effective
superuser/BYPASSRLS attributes. Policy presence/applicability/content and table
SELECT permission do not change its answer or trigger unsupported enforcement.
TEXT uses execution-time relation-name parsing/search path and schema access;
numeric-looking text remains a name. Missing TEXT names error; missing OIDs
and disabled non-table/catalog relations return false. NULL returns BOOL NULL.
Native has no `row_security` setting; PostgreSQL's off-setting does not alter
this metadata inquiry. Stored recipes reevaluate in the destination execution
context; Dump serializes neither current answers nor cluster membership.


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

Parameter ACL catalog proof is documented under
[reference/catalogs Testing](../reference/catalogs/README.md#pg_parameter_acl-selected-proof).
`TestPGParameterACLAuthorization` publishes genuine relation/column ACL changes
through CatalogStage and checks current execution, without adding native catalog
GRANT. The shared public definitions retain independent PostgreSQL catalog-grant
denial/column-grant witnesses in their explicit PostgreSQL-only phase.
