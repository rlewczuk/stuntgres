`pg_settings` has focused coverage for all 17 metadata fields, including
descriptions, typed enum arrays and empty results. Session defaults, SET/RESET
provenance, held readers and independent read/SET authority are checked without
advertising unregistered placeholders or unsupported settings.

Routine formal metadata now includes declared OUT and VARIADIC arguments for
represented native builtins, installed pgrowlocks and amcheck routines, owned
aggregates and generated range constructors. Full argument names remain aligned
with formal types and modes; invocation identities remain input-only. Genuinely
all-IN declarations retain typed NULL formal arrays.

Statements that only add types, such as CREATE TABLE, CREATE VIEW and CREATE
TYPE, no longer rewrite every type, rule and trigger in the catalog to reown
type generations; only replacing an existing type's definition does.

Opening a fresh database uses about a fifth less CPU and a tenth less memory:
the builtin domain checks are parsed once per process, and seeding the builtin
catalog no longer walks every descriptor for each OID it allocates.

All 40 PostgreSQL 18 WAL settings expose typed values and complete metadata,
including archive-command current/boot distinctions and automatic WAL-buffer
defaults. Mutable settings honor parameter grants, transactions and captured
session defaults; immutable contexts reject live changes. These are
setting-only simulations, not physical WAL, durability or recovery effects.

`information_schema.collations` exposes installed collation identities for the
current database encoding with exact standard domain metadata and `NO PAD`.
PUBLIC catalog inquiry includes private-schema and inquiry-only collations;
ordinary schema access checks remain unchanged. Held reads follow collation
creation, rename, schema move, ownership changes and removal.

Partitioned constraint/index cancellation tests discard native short-timeout
sessions instead of resetting an armed timer. Held statements are prepared before
arming; exact cancellation diagnostics and post-cancellation atomicity and
eventual-success assertions remain unchanged.

All 28 PostgreSQL 18 vacuum settings now expose typed values and complete
catalog metadata through the shared registry. Ordinary logins can change the
13 user-context controls, including the released-source USERSET eager-freeze
failure rate; the 15 non-live autovacuum controls reject live changes even
with parameter grants. These are setting-only physical simulations, not
worker scheduling, I/O delays, tuple freezing or file truncation. Session
lifetimes and logical constraints remain independent of physical descriptions.

Native catalog allocation reserves all globally allocated descriptor identities,
including routines restored into a fresh Driver, alongside owned definitions.
New CREATE statements cannot reissue those live identities; invalid routine
definitions reach their genuine SQL diagnostics after Restore. Explicit
large-object identities remain local to their catalog class.

Queries of `pg_type`, `pg_proc` and `pg_depend` whose WHERE compares one oid
column with a constant or parameter (`WHERE oid = 16384`,
`WHERE pronamespace = $1`, also inside derived subqueries) read only the
matching rows of the shared catalog capture instead of comparing every row.

`pg_ts_template` exposes all five catalog columns, including source-owned
REGPROC references to the installed template's actual initialization and lexize
helpers. Installed unaccent metadata follows transactions, removal and native
Dump/Reset/Restore; an empty catalog remains fully typed without advertising
unimplemented templates. Foreign or stale source authority is rejected before
projection.

`pg_ts_parser` now has dedicated direct/prepared catalog acceptance coverage for
the exact stock parser identity and five handler links, unsupported-name emptiness,
OID boundaries, held parameter reuse and PostgreSQL diagnostics. Catalog metadata
does not imply module availability or add parser execution.

Text-search dictionaries now support simple normalization and shipped English
stopwords, typed `ts_lexize` results, transactional CREATE/ALTER/DROP, ownership
and dependency retirement. Standalone and installed dictionaries preserve their
names, schemas, ordered options and owners through native Dump/Reset/Restore;
installed unaccent templates retain their genuine lexize provider. The one-argument
unaccent function follows the default dictionary in its current function schema
after dictionary DDL.

SQL functions support ordered OUT/INOUT records, RETURNS TABLE and scalar,
record and named-composite SETOF results through direct/prepared calls and
ordinary FROM/target-list sources. Composite inputs, member access, exact output
metadata, empty-result boundaries and positional result diagnostics share the
existing routine resolver and statement lifecycle. Native Dump/Restore preserves
these declarations, named child types and dependent stored queries.
Routine catalog output metadata includes ordered all-formal type/mode arrays;
result-type SETOF spelling remains contextual rather than reserving an identifier.
Named result diagnostics use captured search-path visibility; SQL set calls
retain target occurrence and shared placement rules. Datetime sleep functions
also admit scalar-once VOID table-source execution with ordinary cancellation.

`pg_ts_dict` now has dedicated native/PostgreSQL coverage for exact installed
unaccent identity, template linkage, initialization options and six-column
metadata, including transactional install/removal, held queries and immutable
native captures. Available modules remain distinct from installed dictionaries;
empty catalog reads and ordinary binding diagnostics are independently specified.
Dictionary captures reject foreign/stale source authority and foreign type
generations before projection, including empty catalogs, without publishing rows.

`information_schema.check_constraint_routine_usage` now exposes direct routine
dependencies of actual table and domain CHECK constraints, with six nullable
`sql_identifier` columns and routine-owner role visibility. Same-name constraints
retain their source identities while identical projected rows are deduplicated;
domain `pg_depend` routine edges use the same semantic expression authority,
including typed regproc/regprocedure literal references.

Short-option SQL counterparts now include eight planner-method booleans, fsync
and three stage-statistics flags with typed values and release metadata.
All four statistics flags honor effective superuser/parameter SET authority;
explicit assignments enforce statement/stage exclusion before publication,
while cached resets preserve PostgreSQL's check-hook bypass semantics.
These are setting-only simulations, not physical planner, logging or fsync effects.
Mixed session-default/LOCAL assignments retain exact typed defaults and restore
session provenance on commit without altering rollback/savepoint originals.

`pg_lsn` columns, literals and parameters preserve the full unsigned 64-bit WAL
position range with canonical slash-hex results and metadata. Comparisons and
ordering use unsigned magnitude; differences and numeric byte offsets are exact,
including completed-result rounding and PostgreSQL range/special-value diagnostics.
Native Dump/Reset/Restore retains values and stored expressions without accumulating
redundant casts around typed `pg_lsn` view literals.

`CREATE EXTENSION ltree` also installs `ltxtquery` with its `ltxtq_in` and
`ltxtq_out` text codecs. Declared columns, literal and bound assignments, NULLs
and result metadata preserve canonical Boolean label queries through native
Dump/Reset/Restore, type rename/move and SQL extension recreation. Parsing
preserves precedence and suffix modifiers and enforces released storage bounds;
matching operations are not part of scalar storage.

Replication settings now expose typed SQL values and metadata, context restrictions,
and separate examination and parameter SET permissions. The 25 additions are
setting-only simulations without physical replication or plugin-loading effects;
existing commit-timestamp tracking remains operative.

`CREATE EXTENSION ltree` also installs the `ltree` scalar and text codecs.
Declared columns, literal/prepared/bound input, empty paths and NULLs retain exact
path spelling and result metadata through native Dump/Reset/Restore and SQL
extension recreation. Label grammar and path bounds follow PostgreSQL 18 UTF8/C.
Ltree and lquery typed literals, including schema-qualified names, resolve through
the shared binder for direct and prepared queries.

All twenty PostgreSQL 18 preset options now expose immutable, universally readable
typed metadata and text presentation, including 8kB segment units, octal directory
mode and distinct boot/initialized encoding. Physical values are explicit
setting-only simulations, not disk, WAL, checksum, shared-memory, huge-page,
semaphore, SSL-library or standby effects. Context-first 55P02 refusals and fixed
provider defaults preserve logical state; wire status reads the same truthful
native 18.0-compatible identity, independently of the PostgreSQL 18.6 oracle.

Five lock-management settings now expose typed PostgreSQL 18 metadata, validation,
context restrictions and effective parameter SET permissions. They are
setting-only simulations: no deadlock scheduling, lock-table sizing, lock budget
or predicate-lock tracking/promotion mechanism is changed.

`CREATE EXTENSION ltree` installs the `lquery` scalar and its text codecs.
Declared columns and literal/prepared/bound values preserve canonical wildcard,
quantifier, alternative, negation and modifier syntax, typed NULLs and result
metadata through native Dump/Reset/Restore and SQL extension recreation. Held
literals rebind to a recreated installation without retaining retired type data.

SP-GiST indexes now retain exact logical memberships for all seven shipped
operator classes, including network, ranges, text, both point classes, boxes
and polygons. Method/class catalogs, distance capabilities, exclusion constraints,
options, transactional maintenance, copies and native replay share the ordinary
index lifecycle. TEXT pattern comparison operators and their callable routines
use strict bytewise semantics independently of collation.
Type changes reject a missing destination operator class with the declared
method's diagnostic before replacing retained index providers or memberships.
Custom SP-GiST ordering members recognize the class input on either operand.
Handler aliases and user families/classes preserve their own identities and
configuration through native replacement and PostgreSQL SQL replay.

Quoted internal `"char"` columns now use ordinary type resolution, distinct from
SQL `char(n)`, with one-byte input, prepared parameters, empty versus NULL values,
precise encoding errors and native Dump/Restore round trips.

Developer settings expose the pinned release's 24 available parameters with
typed metadata, preserved string validation, context-first refusals and current
parameter SET authority; eleven compile-conditional debug names remain unknown.
These are settings-only simulations, not physical storage, WAL, recovery, JIT,
worker, tracing, signal or authentication-delay effects.
The standalone `current_setting(text,boolean)` overload now supports missing-name
NULL results and strict NULL arguments through the shared typed settings path,
with its own routine EXECUTE privilege.

Table queries, writes, commits and index publication no longer scan every
routine, operator and type in the catalog to find a table's inheritance
children, partial indexes or unique indexes; they visit relations only.

A WHERE condition that reads only the left input of an inner join is evaluated
on that input before the join, as in PostgreSQL. It now raises its error (for
example division by zero) for rows without a join partner, and catalog joins
such as `pg_proc JOIN pg_roles ... WHERE proname = ...` no longer build every
joined row first.

Repeated `pg_type`, `pg_proc` and `pg_depend` reads between catalog changes
reuse one captured row set instead of rebuilding the whole catalog per
statement.

Connection and authentication settings now expose all 38 genuine parameters
through the typed registry, with context-first refusals, restricted-read filtering,
octal socket-permission presentation and stored numeric TCP values. These are
explicit setting-only simulations: no listener, socket, TLS, authentication,
password/warning, file, loader or admission mechanism is configured. Transactional
settings, physical-session reuse/replacement and native controls retain their
existing semantics; the four explanatory ssl_ciphers anchors remain unknown.

Compatibility settings now cover all nine names through the typed registry,
including hidden backslash_quote aliases, sighup mutation precedence and
lo_compat_privileges parameter SET authority. Seven newly exposed switches are
explicit setting-only simulations, not array/lexer/security/rewrite/scan or
configuration-file effects. Existing standard-string parsing and identifier
quoting consumers remain active; settings retain transactional and physical
session lifecycle semantics.

ALTER TABLE DROP COLUMN now enforces stored SQL routine body and default
dependencies through the shared recursive DROP closure. RESTRICT preserves the
column and all dependents; CASCADE retires only the reached routines and their
dependents, with per-column notices and ordinary transaction/savepoint rollback.
Composite fields, inherited columns and native Dump/Restore use the same stable
attribute dependencies without changing the snapshot format.
Surviving standard-routine actions, including MERGE, foreign-target writes and
modifying CTEs, use the same frozen source-layout bridge, so removing an unused
source column does not shift matching or assigned values. Actual child bindings
retain immutable source provenance; independent SELECT-CTE owners select and
bridge their own inputs before effects. Foreign inputs retain genuine scan
opening below the layout bridge, and normal returned-source cleanup remains
authoritative through checks and RETURNING.
Main and modifying-CTE presentations jointly retain their complete source
manifest, including sources reached only by a child command.
Frozen modifying-CTE targets receive their own real admission grants and command
contexts without resolving their retained names again.
Actual mutation FROM/USING/MERGE CTE sources retain executable source demand,
while genuinely unused SELECT CTEs remain unopened.
Foreign routine targets retain their real lock admission without entering local
heap capture; their local query sources retain ordinary data-view ownership.
Genuine FROM/USING literals and modifying-CTE RETURNING scans retain their own
checked presentations through source compaction. Foreign conversion errors in
surviving routines preserve the original column alias and SQL-function context.
Captured generated-column effects now cook and project each surviving owner once,
without recursively rediscovering retired providers. Earlier staged CHECK pruning
retains its metadata effects without changing the captured physical predecessor
or double-incrementing its guarded revision. Column-owned sequence descendants
participate in the same complete preflight; whole-provider drops retain their
separate owned-sequence phase and intermediate index layouts. Local index/sequence
payload retirement follows metadata pruning. Attached trigger payload identities
remain retirement targets even for automatic-only extension dependencies.
Mixed DEFAULT/CHECK diagnostics use real dependent
identities while preserving CHECK declaration order. Low-level staged column
effects do not invent SQL lock admission; admitted SQL keeps its full grants and
publication fences.

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

`CREATE EXTENSION hstore` installs scalar hstore columns with canonical text
input/output, literal and bound values, NULL-aware result metadata and atomic
malformed-input rejection. Native Dump/Reset/Restore preserves values and
rebuilds installed type authority, including retained readers and metadata.
Held literal assignments retain values across type rename/schema movement;
recreated consumers bind original literals under the new installed authority.

User-defined ordered-set and hypothetical aggregates retain direct versus row
arguments through CREATE, binding, execution and native Dump/Restore. Real
transition/final routines receive the declared vectors, including typed NULL
FINALFUNC_EXTRA inputs; ordered support routines own sorting. Legal VARIADIC ANY
declarations invoke genuine native ordered transition and rank-family final
supports with execution-private internal state and current support privileges.
SQL support STRICT declarations retain their execution semantics and native
replay flags; frozen ordered calls preserve the declared direct/row partition.
Hypothetical declarations retain a true option across later false options, and
support lookup preserves canonical variadic declaration arity.
Ordering diagnostics precede pair-collation errors, and scalar support lookup
does not manufacture SQL INTERNAL state.
INTERNAL state creation requires the current role to be a superuser. Transferred
non-superuser owners retain live support checks; native replay legally creates
the aggregate as the authenticated administrator before restoring its owner.
Ordered declarations default both final modification modes to READ_WRITE.
Ordinary/moving strict declaration checks use the first canonical argument,
while runtime seeding remains row-based. Moving-final lookup retains canonical
direct arguments and complete canonical EXTRA inputs just like ordinary-final
lookup.
Stored views retain aggregate support captures for execution without treating
them as direct DROP dependencies; explicitly written support calls keep their
own dependency edges.

GiST indexes now retain exact logical roots for all nine built-in operator
classes, including geometric nearest-neighbor queries and geometric exclusion
constraints. GiST buffering/fillfactor and per-key tsvector siglen options retain
their configuration through metadata inquiry, maintenance, LIKE/partition copies
and Dump/Restore. Built-in class display remains unqualified when visible;
ambiguous compatible range defaults do not masquerade as exact-type conflicts.

File-location settings now expose deterministic, explicitly simulated startup
labels through SHOW, current_setting and pg_settings, including exact nullable
boot/reset metadata. Effective-user read privileges filter aggregate rows and
are rechecked by held prepared readers. Individual assignments retain the
startup-only refusal; no files, storage placement, authentication rules or
process lifecycle are changed.

BRIN min/max indexes now own immutable logical storage-slot range summaries,
incremental maintenance, lossy consistency and method-specific catalog identities.
`pages_per_range` and `autosummarize` retain configured intent with effective
build-width semantics, transactional publication and native CREATE-plus-ALTER replay.
Vacuum and same-ID domain-generation transport preserve produced callable/partial
truth without SQL replay; admitted native replacement repairs destination summaries
before locked remint. Baseline transport reuses the shared row-position map.

Table, sequence, column and any-column privilege inquiries support all thirty
released typed overloads, current-role ACLs, grant options and strict nullable
BOOL results. Native Dump/Restore preserves raw relation and column ACLs with
destination-resolved identities and retains dropped attribute gaps, future column
numbers and inherited provenance through source-shaped logical replay.
Typed and partition attachment replay retains pre-attachment attribute gaps and
genuine keys, bounds and routing; ordinary transient construction helpers are
removed before data.

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

Database, schema, type and tablespace privilege inquiries support all name/OID
overloads with nullable BOOL metadata, current ACL authority, comma-list ANY
and grant options. Type names use regtype resolution and array/range delegation;
schema inquiries honor predefined data roles and physical-session TEMP rights.
Native Dump/Restore preserves schema/type/tablespace owners and exact raw ACLs
with destination-local identities and dependency-ordered ACL replay, without
replacing database attachment authority.

The `stuntgres` server's command line now uses cobra and viper. Every option
can also be set with a `STUNTGRES_<OPTION>` environment variable or a
`--config` YAML/TOML/JSON file (flag, then environment, then file, then default),
and `--help` is generated per command. Long options require two dashes
(`--listen`); the single-dash long forms are no longer accepted.

The `stuntgres` server's new `-q`/`--querylog` option (requires `-L`) writes
`query.log`: one JSONL record per command cycle with the query, parameters,
frontend steps and every backend message as exact base64 bytes plus a readable
decoded form, for comparison with real PostgreSQL responses.

SQL-standard routine dependencies now distinguish written aggregate calls from
their execution supports. Catalog edges, DROP diagnostics and dump ordering
retain explicit support calls without flattening aggregate implementations into
callers. Complete original support captures and live owner EXECUTE checks survive
rename, schema moves and native replacement. Support startup checks final EXECUTE
before transition EXECUTE, including empty inputs. Typed declaration replay
prerequisites and containing-owner DROP ordering remain intact. Checked regproc
and regprocedure literals retain their own original definition captures even
when the referenced routine also supplies aggregate support; those references
do not require invocation EXECUTE. Catalog projection preserves separate
declaration and checked body/default recording passes without exposing runtime
support captures. Stored ordinary/moving window providers preserve original
forward, inverse and final support authority.

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

Parameterless prepared definitions retain owned immutable typed folds across
Go, SQL and repeated protocol Bind uses while keeping binding and authority
current. Relevant executable dependencies, effective environment, DISCARD and
native replacement retire folds; Describe preserves definition ownership and
already-bound portals keep independent planning values. Supported DML and
modifying-CTE SELECT now plan demanded constants at Bind without effects; Execute
retains those values and selected relation occurrences after statement
close/replacement, path changes or DISCARD while checking current authority,
native provenance and data. Lazy private-catalog journal creation in the same
transaction no longer preempts current rights diagnostics or replaces an
accepted portal; exact dependency and planning-context fences remain in force.
Unchanged immutable NaN operands now reuse their accepted prepared folds,
including nonplain protocol portals. Fold identity preserves physical NaN
payloads, FLOAT4 words and signed zero rather than applying floating equality.
Prepared analysis, definition/binding lifecycle and final fold publication now
share one core owner; accepted bindings retain their actual private resources
without a parallel root registry or inspection-only cache.

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

Database-local event triggers support transactional DDL start/end, actual
sql_drop and semantic table_rewrite phases, TAG filters, ordered event-function
frames and bounded ENABLE/DISABLE/DROP lifecycle. Callback and DDL effects share
one private statement journal; native logical replay installs saved triggers
only in its final section. Same-identity SQL/PL function replacement preserves
OID, owner and ACL while validating the new body.
Nested SQL utilities use the same pre-resolution boundary; shape projection and
drop cleanup preserve earlier callback row effects.
Materialized-refresh callbacks isolate parent admission scratch, and direct/no-op
USING expressions retain their non-rewriting event semantics.
Finished statement leases no longer supply Restore catalog/role authority.
Phased ALTER retains original fences and demands final providers after physical
work; metadata-only updates preserve heaps and ready-invalid index roots.
Prospective ALTER names remain unlocked across reentrant providers until Finish,
without rebasing collision observations. Semantic TYPE work rejects late stored
row consumers, and general authority fences the actual published role root.
DISCARD ALL consumes its temporary cleanup journal exactly once. Typed TYPE
admission preserves existing stored-row diagnostics before generation transfer,
and stale generated-expression descriptors retain their conflict diagnostic.
Inherited partition ALTER and replay retain their admitted topology through
private successors; typed projections preserve canonical nested composite values.

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
