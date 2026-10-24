information_schema.role_usage_grants now exposes eight domain-typed columns over
real domain, sequence, collation, foreign-wrapper and foreign-server USAGE
origins. Enabled-role visibility and named-grantee grantability refresh across
direct and held prepared reads, preserving raw-NULL versus empty ACL semantics.

information_schema.role_udt_grants now exposes seven domain-typed columns over
actual composite type USAGE grant origins, including represented catalog row
types. Named grantors, grant options and inherited enabled-role visibility
refresh across direct and held prepared reads without leaking unrelated grants.

information_schema.sql_features now supports ordinary direct and prepared reads
of the exact PostgreSQL 18 release conformance matrix, with nullable domain-typed
metadata, PUBLIC readability and standard ACL admission. These release metadata
flags do not add native execution support for the listed SQL language features.

Installed citext now supplies case-insensitive LIKE/ILIKE and regular-expression
operators for citext and text patterns, including negations and explicit ESCAPE.
Embedded regex options retain their ordinary precedence, so (?c) restores
case-sensitive matching. Selected routines and operators survive stored SQL and
native Dump/Reset/Restore without changing input spelling.

Ordinary table CHECKs retain original routine calls, including folded constants,
for current-authority readmission after unique waits and before publication.
Revoked EXECUTE refuses the statement without a row/output prefix or CHECK truth
replay; invoker and trusted referential-owner phases remain independently admitted.

Schema-qualified column expressions now share namespace resolution across SQL
consumers, including DELETE predicates and qualified operands in casts and calls.
Aliases, correlation, clause visibility, column metadata and read privileges
retain their ordinary rules through direct, prepared and server execution.
Rowless assignment and setting-call inputs use the same empty-namespace
diagnostics instead of rejecting column spelling before their owning consumer
resolves visibility. INSERT RETURNING preserves inaccessible same-name relation
diagnostics without exposing those relations as inputs. Unsupported setting-call
signatures retain the final function-name component's diagnostic position after
operand analysis; missing-column diagnostics still precede that shape boundary.

information_schema.role_routine_grants now exposes ten domain-typed columns over
real routine EXECUTE grant origins, preserving named grantors, grant options and
enabled-role visibility across direct and prepared metadata reads.

PL/pgSQL integer FOR supports private integer counters, ascending and REVERSE
ranges, entry-time bounds and positive BY steps, labelled control transfers and
overflow-safe endpoints through direct/prepared calls and native round trips.
Invalid target lists retain declaration-sensitive unknown-variable, constant and
whole-row diagnostics rather than a blanket single-target error.

information_schema.role_table_grants now exposes eight domain-typed columns and
live table ACL origins, including grant options, inherited enabled roles and
enabled-grantor PUBLIC grants. Direct and prepared reads refresh after grant,
membership and identity changes without exposing unrelated origins.

Installed citext now provides its six case-insensitive scalar comparison operators
and backing routines. Comparisons preserve original spelling, fold with database
C ctype independently of input collation, and retain installed identities through
stored expressions and native Dump/Reset/Restore. Equality needs no input collation;
hidden extension operators use ordinary visible text comparisons through implicit casts.
The six backing routines expose their C-language declarations and original source
symbols through information_schema.routines alongside the INTERNAL I/O routines.

Native Restore now rejects indirect foreign scans, writes and schema imports
before provider invocation, including calls reached through stored defaults and
CHECK expressions. Dependency-ordered table, view, SQL-function and data replay retains
local staged effects and trusted FDW definition validation; private-stage
advisory ownership and settings never replace live target-session state.

information_schema.routines now exposes the exact 82 domain-typed columns for
real routines, overload identities, declared returns and provider-owned source,
language and security facts. Captured ownership/EXECUTE filtering protects
definitions; builtin view display follows current naming and quote settings.
PL inline/validator declarations now retain their canonical strictness.

Explicit casts now cover implemented scalar and structured types, including
typed UUID/timestamptz parameters in SERVICE_CERTIFICATES INSERTs. Conversion,
typmods, domains, session settings and prepared identities share the existing
coercion machinery; typename calls resolve real functions before cast shortcuts.
Declared cast methods also govern typename eligibility; numeric-leading temporal
calls remain ordinary calls, and installed cast defaults render current checked
routine namespaces after schema rename and through native replay.
Cast procedure registration also supplies released source/language/security facts.

JSONPath datetime, date, time, time_tz, timestamp and timestamp_tz methods now
execute typed temporal conversions, strict templates and precision rounding
through existing JSONB and SQL/JSON consumers, retaining parsed offsets,
execution-local timezone policy and precision warnings.
Strict template compilation distinguishes quoted literals from invalid unquoted
characters and preserves hard compiler errors separately from recoverable input
mismatches. Unrecognized temporal-input diagnostics retain decoded characters
without Go-style escaping.

Installed earthdistance now exposes geo_distance(point,point) and POINT <@> POINT
for longitude-first great-circle distances in statute miles, including NULL and
nonfinite inputs, prepared calls, complete installation-collision diagnostics
and identity-preserving native controls.

CREATE EXTENSION lo now installs an OID-backed domain for declared reference
columns. Text input, direct/prepared binds, typed NULLs and physical OID result
metadata preserve unsigned values exactly, including duplicate and dangling
references. Dump/Reset/Restore retain logical type identity, owners and numeric
payloads through replacement and extension recreation.

CREATE/DROP ACCESS METHOD and CREATE/ALTER/DROP OPERATOR FAMILY and OPERATOR CLASS
now share SQL BEGIN and Go BeginTx with autocommit and retained messages.
Definitions, members, dependencies and consuming indexes remain owner-private
until commit and restore together at savepoints or rollback. Held execution uses
current authority. Concurrent distinct DEFAULT classes remain distinct; implicit
selection reports ambiguity only for multiple exact defaults and missing-default
when compatible defaults have no unique winner. Explicitly selected and existing
indexes retain their actual class. Exact ambiguity names the domain's base type
using current catalog/search-path visibility; missing-default diagnostics retain
the original requested type.

PL/pgSQL FOREACH now iterates array elements and trailing-dimension slices,
preserving storage order, NULL elements and slice bounds. Existing scalar,
row, record, field and list destinations retain ordinary assignment behavior,
loop control and FOUND semantics through stored calls and native replay.
SLICE remains usable as an identifier; FOREACH rejects subscript destinations
and whole row/record members in scalar target lists at definition admission.
SLICE integer constants support radix prefixes and digit separators within the
nonnegative signed-32-bit range, including unreachable clauses.

Opt-in foreign scans under UNION ALL now progress independent requests through
the optional asynchronous FDW protocol, controlled by enable_async_append.
Cancellation joins requests before scan cleanup; finite cursor motions retain
owned rows rather than request contexts. Native persistence keeps declarations
and options, never live requests.
Repeated expression and correlated-input invocations reuse their synchronous
scan frontier without accumulating async requests or remote cursors.

Shipped function, aggregate and operator definitions and routine identity changes
now share SQL BEGIN and Go BeginTx with autocommit and retained messages.
Private callable programs, overload names, dependencies and rows publish or roll
back together, including savepoints, held consumers and provider/name conflicts.

Geometric +, -, *, / now transform POINT, BOX, PATH and CIRCLE by POINT,
including checked complex arithmetic and circle radius scaling. Open PATH + PATH
preserves vertex order and duplicates; a closed operand returns NULL. Operator
metadata, prepared execution and stored expression replay retain the same signatures.

The installed earthdistance provider now exposes earth(), ll_to_earth(),
latitude(), longitude(), gc_to_sec() and sec_to_gc(), including strict NULL
handling, released floating boundaries and stored expressions through native
Reset/Dump/Restore and routine identity changes. SQL-body errors present the
current routine name, including calls stored before rename.

Table/type/domain/database OWNER, view/materialized-view OWNER and REASSIGN/DROP
OWNED now share SQL and Go transactions with autocommit and retained protocol
messages. Ownership and ACL/default/policy/membership cleanup see earlier private
changes, restore at savepoints or rollback and publish together at commit.
Sole-target policy deletion retains AccessExclusive on its surviving containing
table until transaction end or rollback to an earlier savepoint, without blanket
partial-policy or ACL-survivor locking.

CREATE/ALTER/DROP COLLATION and CONVERSION and CREATE/DROP CAST now share
autocommit, retained messages, SQL BEGIN and Go BeginTx. Supported consumers use
owner-private definitions immediately; identities, settings and dependencies
commit or roll back with rows, including savepoints and held prepared execution.
Conflicting definitions and provider retirement use transaction-lifetime locks
and current authority. ALTER fences its original identity rather than retargeting
a search-path fallback; native replacement failures require full rollback before
fresh transaction use. Collation CREATE handles semantic checks and serial
same-encoding duplicate/skip cases before catalog lock admission, with protected
cross-encoding checks and exact-key insertion conflicts afterward.
Candidate-only TEMP namespace authority is preserved through both admitted and
direct collation staging without granting peer sessions access.

CREATE, ALTER (including rename) and DROP POLICY now participate in SQL BEGIN
and Go BeginTx as well as autocommit and retained messages. Private definitions,
role bindings, dependencies and rows commit or roll back together, with savepoint
recovery, transaction-lifetime table locking and fresh existing RLS consumers.

BEFORE row trigger CREATE/REPLACE now shares SQL and Go transactions with existing
rename, DROP and extension dependencies, including private firing, savepoint
recovery and atomic definition/row publication. CREATE/REPLACE retains
ShareRowExclusive locks on partition descendants through transaction end.

CREATE/OR REPLACE RULE, ALTER RULE RENAME and DROP RULE now participate in SQL
and Go transactions as well as autocommit. Private definitions govern owner DML
immediately; rule changes, dependencies and action rows commit or roll back
together, including savepoints and held prepared execution.

Ordinary and concurrent REFRESH MATERIALIZED VIEW now participate in SQL BEGIN
and Go BeginTx as well as autocommit. Source writes, stored rows, indexes and
population commit or roll back together, including savepoint recovery, with
transaction-lifetime reader/refresh locking.

Domain and composite creation, domain rule changes, composite attribute evolution
and type/domain rename, schema move and DROP now share SQL and Go transactions
with autocommit and retained messages. Definitions, consuming rows and logical keys
commit or roll back together, with savepoint recovery and exact source authority.

Supported object GRANT/REVOKE and ALTER DEFAULT PRIVILEGES now participate in
SQL and Go transactions and retained messages. Private ACLs and creator defaults
compose with object creation, rows, dependencies and savepoints, and publish
atomically at commit. Named relation grants honor AccessShare admission;
independent ACL/default targets avoid false statement-authority and
role-publication conflicts without changing ordinary lock fast-path allocation.
Completed ACL effects preserve compatible unrelated role publications, while
pending statements retain their separate authority fence. These ACL/default
directives do not widen existing role, ownership or ALTER SYSTEM policies.

View and materialized-view definitions now participate in SQL and Go transactions
as well as autocommit, with private definitions and stored rows, identity-preserving
alterations, savepoints and atomic commit or rollback. REFRESH remains separate.

Ordinary DROP INDEX and ALTER INDEX rename/SET/RESET now participate in SQL,
Go and retained protocol transactions, with private metadata/dependencies,
savepoint recovery and atomic index/row publication. Logical index options
preserve identities and key semantics and survive native Dump/Restore, including
constraint-backed indexes. Completed metadata-only maintenance permits compatible
peer DML and unrelated role changes without retaining stale statement authority
at commit. Pending query-derived producers and materialized replacements retain
their separate role-read publication guard; actual role writes keep their own
baseline conflict fence. REINDEX and concurrent-operation boundaries remain.

Existing role creation, attributes, retirement and membership options now support
SQL and Go transactions, including private visibility, savepoints and atomic
cluster-wide publication. Session authorization rolls back with its physical
owner; role conflicts share the canonical lock and deadlock lifecycle.

PL/pgSQL RAISE supports severity levels, formatted messages, SQLSTATE/condition
forms and structured diagnostic fields. ASSERT evaluates messages only on failure
and honors the session's live plpgsql.check_asserts setting.
Successful nested messages retain their enclosing PL and SQL caller context.

PL/pgSQL static query FOR loops assign existing RECORD and row variables,
preserve query ordering and duplicates, retain VOID query-result members,
shape empty results as NULL fields, stream SELECT demand across CTE lifetimes,
and complete RETURNING effects before loop bodies while retaining statement
atomicity and cancellation cleanup.

PL/pgSQL supports unconditional LOOP, labeled EXIT/CONTINUE and WHEN conditions,
including nested block/cursor transfers, cancellation and stored-definition replay.
Quoted label diagnostics preserve literal identifier contents.

Existing table, column, foreign-table, schema and role comments now participate
in SQL and Go transactions, with private visibility, savepoints and atomic commit
or rollback. Sequence comment and counter behavior is preserved.

`information_schema.tables` exposes privilege-filtered table, view, foreign-table
and session-local temporary identities with typed-table metadata, structural
insertability and current results on held prepared reads.

Trigger rename, extension dependencies and table-qualified DROP preserve installed
definitions and partition clone identities, participate in transactions, and
survive native Dump/Restore.

Ordinary CREATE INDEX, including UNIQUE and shipped access methods/options, now
participates in SQL and Go transactions on existing or transaction-private
tables. Index metadata, keys and rows commit atomically or roll back together;
savepoints restore private work and release postmark locks. Ordinary builds
retain SHARE through transaction end; CONCURRENTLY and standalone maintenance
keep their existing transaction boundaries.

Permanent table/schema DDL now participates in SQL and Go transactions, including
private schema/table chains, query-derived tables, savepoints and atomic
definition/data commit or rollback.

Please DO NOT modify this file.

There is no need to record changes anymore.

DO NOT add anything to changelog anywhere in this project.
