Client connection defaults now cover the complete 46-setting family through
the existing typed registry, with released metadata, boot/startup distinctions,
catalog-aware AM/tablespace validation, parameter SET authority, independent
restricted reads, keyword/preload list semantics and SET XML OPTION syntax.
Absent physical mechanisms are explicitly setting-only simulations: no files
or libraries are loaded, no compression/GIN/placement/FTS/XML engine is implied,
and postmaster settings still reject live changes. Native foreign-key and final
routine validation remain enforced even under replica/check_function_bodies
settings. Physical reuse retains settings; native controls exclude them.
XML OPTION preserves ordinary XML identifier/type syntax; pq-initialized
DateStyle and encoding metadata retain their actual client source.

File-location settings now expose deterministic, explicitly simulated startup
labels through SHOW, current_setting and pg_settings, including exact nullable
boot/reset metadata. Effective-user read privileges filter aggregate rows and
are rechecked by held prepared readers. Individual assignments retain the
startup-only refusal; no files, storage placement, authentication rules or
process lifecycle are changed.

The `stuntgres` server's new `--reference DB=URL` option (repeatable,
requires `-q`) runs database DB in dual-backend mode: every command is also
sent to the real PostgreSQL server at URL, clients still receive stuntgres's
responses, and each `query.log` record adds `response_ref`, `error_ref` and,
when the responses differ, a readable `response_diff`. `--reference-timeout`
bounds the wait for the reference; an unreachable reference never blocks
clients.

Owned aggregates support moving transition, inverse and final routines for window
frames, including real frame replay when an inverse returns NULL. Moving options,
selected-support privileges, result metadata and owned definitions survive native
Dump/Restore with independent ordinary, moving-state and result identities.
Stored windows retain their actual owned aggregate identity, and repeated domain
support signatures expose one declaration dependency edge per referenced object,
without collapsing independently captured body/default dependency rows.

INT2VECTOR and OIDVECTOR columns now share type-owned space-separated input,
canonical output, binding, zero-based array bounds and ordering, including empty
vectors distinct from NULL. Native logical Dump/Restore preserves these values.
Catalog routine signatures and index key/class/collation/option vectors use the
same typed representation.

The `stuntgres` server's command line now uses cobra and viper. Every option
can also be set with a `STUNTGRES_<OPTION>` environment variable or a
`--config` YAML/TOML/JSON file (flag, then environment, then file, then default),
and `--help` is generated per command. Long options require two dashes
(`--listen`); the single-dash long forms are no longer accepted.

The `stuntgres` server's new `-q`/`--querylog` option (requires `-L`) writes
`query.log`: one JSONL record per command cycle with the query, parameters,
frontend steps and every backend message as exact base64 bytes plus a readable
decoded form, for comparison with real PostgreSQL responses.

Ordinary queries expose OID-valued tableoid through source aliases, lexical
correlation and outer joins without changing wildcard columns. Inherited and
partition scans report the actual containing relation. SQL-standard bodies
retain the named relation's system-attribute dependency through native
Dump/Restore; system reads preserve live table SELECT and RLS admission.

Routine declaration defaults now retain exact named-composite and table-row
attribute dependencies for quoted and SQL-standard bodies. Captured defaults
preserve their original authority through rename, schema moves and native
Dump/Reset/Restore, including independent Driver ownership.

Routine, language, foreign-data-wrapper and server privilege inquiries support
all name/OID overloads, strict NULLs, grant options and current execution-role
ACLs in direct and prepared SQL. Native logical Dump/Restore preserves their
raw ACLs, including named grantors, owner identities and absent versus empty
ACLs, with destination-local object and role identities. Temporary routines
and their ACL repairs are excluded together from logical dumps. Empty ACL
repairs retain the representation required by PostgreSQL privilege checks.

Installed amcheck 1.5 provides verify_heapam with ordinary named/default
arguments, record-set metadata, live EXECUTE authority and native logical
row/version consistency findings. Logical findings preserve exact row/version
identity and stable attributes; stopping finishes the first invalid version.
Physical heap, visibility-map and TOAST options report explicit unavailability.
Stored and prepared calls retain installed identity and exact installed-member
EXECUTE ACLs through native controls. Named boolean predicates use the shared
argument grammar without changing positional or VARIADIC syntax admission.
Numeric runtime targets also enforce TEMP table/sequence session isolation.
Admitted TEMP attempts retain PostgreSQL's sticky PREPARE footprint even when
foreign storage is rejected; EXECUTE denial occurs before that accounting.
Native ACL replay preserves delegated nonowner grantors, cascading grant-option
revocation and PUBLIC execution authority across independent role domains.

Installed isn now provides genuine UPC columns with compact twelve-digit
output, retained invalid markers, exact metadata and literal/string/byte/NULL
input. Checked EAN13-to-UPC casts and UPC-to-EAN13 assignment preserve the packed
flag through prepared execution, extension recreation and native controls.
UPC-to-EAN13 relabeling does not legalize a bare SQL/JSON DEFAULT parameter;
the checked reverse conversion remains a cast-function expression.

Installed isn now provides independently identified issn13 columns with short
or long textual input and always-long canonical output. Literal and bound
values retain check-digit validity, invalid markers, issue digits and NULLs
through prepared execution, extension recreation and native Reset/Dump/Restore.

Installed isn now provides independently identified issn columns with literal
and bound string/byte input, canonical short or lossless long output, and ISSN
result metadata. Check digits, invalid markers, nonzero issue digits and NULLs
survive prepared execution, extension recreation and native Reset/Dump/Restore.

ALTER AGGREGATE supports exact-signature rename, owner transfer and schema
relocation without changing identity or checked consumers. DROP AGGREGATE
supports IF EXISTS and dependency-aware RESTRICT/CASCADE through the shared
routine lifecycle, including prepared execution and native logical replay.
Checked permanent and temporary view definitions retain real routine dependency
locks so cancellation while aggregate retirement waits cannot publish a partial
drop. Ordered identity signatures recognize equivalent parsed pg_catalog type
names without substituting call-site type coercion.

Installed cube supports cube_subset, cube_union, cube_inter and cube_enlarge
through shared typed binding, prepared calls and stored expressions. Repeated
dimension selection, disjoint crossed intersections and radius-driven dimension
changes preserve binary64 coordinates through native Dump/Reset/Restore.
Coincident subset boxes expose geometric point text and inquiry results.

Tsvector utilities now support lexeme counts, weight assignment, stripping,
exact deletion, weight filtering and canonical text-array conversion, including
typed internal-char arrays with generic storage, bounds, NULLs and native
Dump/Reset/Restore, collated text-array selectors with byte-exact matching,
and ordinary prepared/stored expression replay.

Bounded random and random_normal now plan required immutable children before
strict literal-NULL elimination, preserving arithmetic errors and current-actor
EXECUTE checks. Discarded runtime-only siblings do not consume random or sequence
state; held typed parameters still import before child admission.

Ordinary aggregates execute selected FINALFUNC routines with distinct state and
result types, typed NULL EXTRA arguments and declared modification modes.
Direct/prepared results, metadata, stored consumers and native Dump/Restore keep
the final result identity. Native aggregate FINALFUNC output exposes unknown
computed nullability; aggregates without a final routine and stored view columns
retain known nullable metadata. Private group frames
and current-owner support authorization preserve cancellation, statement
atomicity and result ownership. SHAREABLE/READ_WRITE window calls reject at
execution startup, including empty input, rather than during Prepare. Their
diagnostics use the selected declaration's visibility-aware routine identity,
including quoted identifiers and multi-input signatures.

Installed isn now provides independently identified ismn13 columns, legacy M
and long-form input, canonical long ISMN output and ISMN13 result metadata.
Corrected check digits, retained invalid markers and NULLs survive prepared
execution, extension recreation and native Reset/Dump/Restore.

Installed citext now supplies case-insensitive replace, split_part, strpos and
translate overloads with TEXT/INT4 results, strict NULL propagation and ordinary
overload and caller-privilege resolution. Stored calls retain installed identity
through routine rename, schema movement and native Dump/Reset/Restore.

The default text-search parser is executable through both text-name and OID
overloads of ts_parse and ts_token_type, with typed set results, all 23 token
descriptors, preserved document spelling and ordered URL/hyphen overlaps.
pg_ts_parser exposes the stock parser and internal handler identities; pg_proc
also exposes truthful set-returning flags. Parser inputs and materialized rows
use ordinary native Dump/Reset/Restore without serializing builtin state.

Installed isn now provides independently identified ismn columns, literal and
bound string/byte input, canonical short M output and ISMN result metadata.
Check digits, retained invalid markers and NULLs survive prepared execution,
extension recreation and native Reset/Dump/Restore.
The pg_proc source projection exposes stored routine source declarations,
including immutable installed codec symbols.

Installed isn now provides independently identified isbn13 columns, literal and
bound string/byte input, canonical long ISBN output and ISBN13 result metadata.
ISBN10 expansion, corrected check digits, retained invalid markers and NULLs
survive prepared execution, extension recreation and native Reset/Dump/Restore.

information_schema.sql_parts now supports direct and prepared reads of eleven
fixed SQL-standard part records through five nullable domain-typed columns.
All parts remain NO because no entire standard part is fully implemented;
verification is NULL and comments are non-NULL empty strings matching PostgreSQL.
The physical table preserves default owner/superuser access without PUBLIC
SELECT, with normal schema/relation admission and current-identity rechecks.

information_schema.routine_privileges now exposes ten nullable domain-typed
columns over actual routine EXECUTE grant origins. PUBLIC origins remain visible
to unrelated observers, while named grantors/grantees, inherited membership and
grantability refresh across direct and held prepared reads. The shared projector
preserves role_routine_grants' enabled-origin visibility.

information_schema.sql_sizing now supports direct and prepared reads through four
nullable domain-typed columns. Its standardized inventory reports enforced
native limits, zero for unlimited or undetermined sizes, and NULL for unsupported
sizing items, with PUBLIC readability and ordinary schema/relation admission.

information_schema.role_usage_grants now exposes eight domain-typed columns over
real domain, sequence, collation, foreign-wrapper and foreign-server USAGE
origins. Enabled-role visibility and named-grantee grantability refresh across
direct and held prepared reads, preserving raw-NULL versus empty ACL semantics.

Aggregate slot reuse now preserves selected declaration identity for bool_and/every,
variance/var_samp and stddev/stddev_samp, including modifiers and outer-owned calls.
Repeated immutable calls to one declaration still share; distinct declarations
retain independent original proofs and current-invoker EXECUTE admission.

information_schema.sql_implementation_info now exposes twelve stable product
characteristics through five nullable domain-typed columns, including truthful
stuntgres identity, the existing compatibility version banner and initial C
collation. Direct and prepared reads preserve NULL, empty-string and zero
values with normal PUBLIC SELECT and schema/relation admission.

Installed citext supplies its ten documented regexp scalar and set overloads.
Captures, replacements and split results retain original spelling, default to
case-insensitive matching and honor explicit flags. Named parameters, installed
SQL declaration metadata, current caller privileges and stored scalar/set
invocation share the existing routine and native-control machinery.

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

The isn extension now supports ISBN columns, short and long text input,
canonical output and marked-invalid values, with independent type metadata and
identity preservation through prepared statements and native Dump/Reset/Restore.

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
