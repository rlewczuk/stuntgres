# stuntgres

stuntgres is a process-local, embedded PostgreSQL-compatible Go `database/sql`
driver for unit tests and reusable test fixtures. It lets Go developers exercise
SQL-backed code without starting a database server or container. Production code
uses only the Go standard library. Requires **Go 1.26 or later**.

It implements a deliberately bounded SQL subset, not a complete PostgreSQL server.
There is no network service, filesystem storage, or durability; data disappears
when the process exits. Use real PostgreSQL for integration tests that depend on
features outside the [supported dialect](docs/DIALECT.md).

Change note: floating `+`, unary `+`, `-`, unary `-`, `*`, `/` and `^`
use PostgreSQL operand resolution and fixed REAL/DOUBLE PRECISION widths.
Known integer/NUMERIC operands mixed with REAL widen to DOUBLE; unknown
inputs retain REAL. Power is left-associative (`2^3^2=64`) and FLOAT8-selected
integer power is supported. IEEE specials, checked range/domain diagnostics,
stored expressions and native replacement use the existing execution seams.
Exact NUMERIC-only arithmetic and floating `%` remain unavailable.
See [floating arithmetic](docs/DIALECT.md#floating-arithmetic--change-note).
Feature smokes: `make quickcheck PACKAGES=./reference RUN='^TestReferenceFloatingArithmetic(Smoke|ControlsSmoke)$' VERBOSE=0`;
pinned counterpart: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFloatingArithmetic(Smoke|ControlsSmoke)$' VERBOSE=0`.

Change note: index/view introspection supports all seven PostgreSQL 18 signatures:
`pg_get_indexdef(oid)` and `(oid, integer, boolean)`;
`pg_get_viewdef(text)`, `(oid)`, `(text, boolean)`, `(oid, boolean)` and
`(oid, integer)`. Results are strict nullable TEXT; omitted pretty means false,
and integer view width selects pretty rendering. Reconstruction traverses current
bound descriptors without evaluating views. Index column zero gives CREATE INDEX,
other valid columns give only the key expression, and invalid columns give empty
text. Text view arguments resolve relation names, not numeric OIDs. Ordinary
views and existing simple, expression-key and UNIQUE backing indexes are covered.
Materialized-view DDL, partial indexes and INCLUDE remain excluded; this part
adds no index DDL capability.
Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDeparseRelations' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeparseRelations' VERBOSE=0`.

Change note: `CREATE DOMAIN [schema.]name [AS] base_type [NULL]` declares a
catalog-owned logical type over a supported scalar or bounded `NAME[]` base.
Domain columns, direct/prepared input, inherited base typmods, operators/functions
and explicit assignment/cast-back retain the distinction between logical identity
and physical codecs. `DROP DOMAIN [IF EXISTS] ... [RESTRICT | CASCADE]` follows
real type/column/stored-expression dependencies: CASCADE removes dependent columns
and views, not unrelated tables or surviving rows. Native Dump/Restore preserves
qualified declarations, chains, owners and data. Domain DEFAULT/CHECK/NOT NULL,
ALTER DOMAIN, general arrays, type ACL-management SQL and transactional domain DDL
remain excluded. See [domains](docs/TYPES.md#domains--change-note).
Feature smokes:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDomain(BaseValues|Expressions|Metadata|Arrays)$' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDomain(Diagnostics|Dependencies|PreparedLifecycle|NativeControls)$' VERBOSE=0`,
and pinned `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDomain' VERBOSE=0`.

Session identity uses typed scalar expressions: `current_catalog`,
`current_database()`, `current_role`, `current_user`, `session_user`, `user`
return NAME; embedded trust-style `system_user` returns TEXT SQL NULL. Initially
`SELECT current_catalog=current_database(), current_role=current_user, session_user=user`
returns three true values. Autocommit SET/RESET ROLE changes effective identity
without changing session_user; prepared executions observe fresh values and
retained rows keep their snapshots. SET LOCAL ROLE and role commands in explicit
transactions remain native exclusions. The exact opaque DSN is the database key;
its NAME display silently clips to a complete UTF8 prefix of at most 63 bytes,
without URL parsing, trimming or merging distinct keys. See
[authorization](docs/AUTHORIZATION.md) for forms and authority boundaries.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSessionIdentitySmoke$'`.

CREATE TABLE supports named and unnamed column/table CHECK constraints, including
`NO INHERIT`. Constraints see the completed new row after defaults/coercions:
TRUE and NULL pass; FALSE rejects INSERT/UPDATE with PostgreSQL-style `23514`
constraint identity and privilege-filtered failing-row details. A late failure
rolls back all row writes in the statement. Definitions survive native
Dump/Restore and column/table rename; CHECK does not change declared nullability.
See [CHECK constraints](docs/TABLES.md#check-constraints--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCheckSmoke$' VERBOSE=0`.

Constraint and stored-expression introspection supports all four PostgreSQL
signatures: `pg_get_constraintdef(oid [, boolean])` and
`pg_get_expr(pg_node_tree, oid [, boolean])`, returning TEXT or NULL.
Minimal `pg_catalog.pg_constraint` and `pg_catalog.pg_attrdef` projections expose
real definition identities and immutable structural trees. Compact/pretty
decompilation traverses bound definitions without evaluating them, uses current
column names, and resolves Vars against the supplied relation's stable attributes.
Both modes retain nonassociative grouping, current search-path provider visibility,
implicit function-argument annotations and raw preassignment/precision metadata.
Returned SQL consumes bounded same-type and existing directional numeric casts;
literal NULL::unknown retains provenance without adding a concrete UNKNOWN type.
Change note: synthetic numeric comparison annotations use the base type, never the opposite
column's precision/scale; genuinely explicit sizing casts remain executable.
Identity keywords and ordinary calls remain symbolic under role/path changes;
reserved ordinary names retain quotes, without capturing authority values.
Consumer fixtures distinguish conditional NULL/branch behavior, controlled comment
state, session paths/zones and per-backend seeded random effects. Exact renderer
strings or one successful insertion alone are not behavioral compatibility proof.
Native tree output is not PostgreSQL's physical node serialization; non-NULL
external tree input is rejected. See [deparsing](docs/QUERIES.md#constraint-and-expression-deparsing).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceDeparseConstraints' VERBOSE=0`.

Time-zone-aware `TIMESTAMPTZ` and `TIMETZ` values support per-physical-session
TimeZone/DateStyle, named IANA zones, abbreviations, fixed offsets and
`AT TIME ZONE` / `AT LOCAL`. Timestamptz stores an instant; timetz retains
its fixed offset. Rules are embedded from the pinned PostgreSQL 18.0 image,
not the host timezone installation. See [temporal types](docs/TYPES.md#time-zone-aware-values)
and [session settings](docs/SETTINGS.md#temporal-settings).
Native logical Dump/Restore preserves these values independently of session
presentation. This does not add timezone catalog views, general INTERVAL arithmetic,
or the CURRENT_* producer family.
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestTimezoneSmoke$' VERBOSE=0`.

SELECT and INSERT SELECT support comma products, CROSS JOIN and inner or
LEFT/RIGHT/FULL [OUTER] joins with ON, USING or NATURAL, including nested joins,
FROM/column aliases and the special USING alias. Unmatched operands receive
typed NULLs; ON determines matches before WHERE filters output. Duplicate
occurrences survive (the documented fixture yields LEFT6/RIGHT6/FULL8).
Prepared executions freshly bind every source, and eager results survive native
Reset/Restore. See [join semantics and limits](docs/QUERIES.md#inner-and-cross-joins--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|JoinsPreservation)' VERBOSE=0`;
pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|JoinsPreservation)' VERBOSE=0`.

Alias diagnostic fidelity includes suggestions using exposed renamed columns,
Unicode character positions, fuzzy-tie and distant-qualifier suppression.
Exact inaccessible columns take precedence with PostgreSQL's singular/plural
Detail rather than misleading spelling suggestions. Hidden within-entry exact
duplicates retain positioned ambiguous-column errors, even for distant qualifiers.
Output labels stay invisible to sibling targets and WHERE; bare ORDER labels
prefer outputs while compound/qualified ORDER uses inputs. This corrects
missing-name diagnostics without changing resolution or physical privileges.
See [query labels and relation aliases](docs/QUERIES.md#query-labels-and-relation-aliases--diagnostic-fidelity-change-note).
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryAliasesSmoke$' VERBOSE=0`.

DELETE supports `RETURNING *`, scalar expressions and PostgreSQL18 OLD/NEW
output aliases. Deleted occurrences return their actual pre-deletion values
(including committed updates encountered after a row wait); ordinary NEW columns
are typed NULL. Direct and held prepared Query return eagerly finalized rows,
including full metadata for no matches; Exec still evaluates output and reports
the actual count. Output failure or prepublication cancellation discards all
transactional deletions, while sequence draws retain their usual nonrollback
semantics. Native Dump/Restore records surviving objects/data, not result history.
See [DELETE RETURNING](docs/QUERIES.md#delete-returning--change-note).
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestDeleteReturning(Smoke|ControlsSmoke)$' VERBOSE=0`.

UPDATE supports PostgreSQL18 `RETURNING` expressions, mixed stars and output-only
OLD/NEW aliases. Ordinary outputs read the completed new row; OLD reads the actual
predecessor, including after a row wait. Same-value matches and duplicates each
return one occurrence; no matches retain full metadata. Query owns eager values,
and Exec still evaluates outputs before reporting its count. Output errors roll
back staged mutations, not sequence draws, under existing transaction/savepoint
rules. See [UPDATE RETURNING](docs/QUERIES.md#update-returning--change-note).
Feature smokes: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)' VERBOSE=0`; pinned mode: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)' VERBOSE=0`.

UPDATE also accepts AS/bare target aliases and the supported inner/outer `FROM` trees using
the existing query namespace. Source-qualified SET/WHERE/RETURNING expressions
read one coherent captured source view; ordinary target outputs read NEW.
Multiple eligible source pairs may evaluate SET, but each physical target is
replaced/counts/returns once with its winning source pair, without a promised
winner or traversal order. After a row wait only that target predecessor and
predicate are refreshed; source rows and candidate identities stay frozen.
See [UPDATE FROM](docs/QUERIES.md#update-from--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateFromSmoke$' VERBOSE=0`.
Pinned PostgreSQL witnesses: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateFrom' VERBOSE=0`.

Schema search paths are physical-session settings: missing/inaccessible entries
are ignored, `$user` follows the current role, qualification ignores the path,
and creation selects the first usable explicit schema without CREATE-denial
fallback. The common namespace resolver routes relations, types and supported
builtin functions/operators; `pg_catalog` qualification and
`OPERATOR(pg_catalog.+)` preserve their provider boundaries.
The selected `min_scale`, `round`, `scale`, `trim_scale`, `trunc`, `sign`, `mod`,
`abs`, `ceil`, `ceiling`, `div`, `exp`, `factorial` and `floor` providers use the same
namespace checks. `abs` preserves integer/REAL widths; decimal division and
factorial are exact, and decimal exponential preserves independently selected
precision (up to 1000 fractional places). See the
[mathematical signatures and limits](docs/DIALECT.md#scale-and-rounding-functions--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestMathScalarSmoke$' VERBOSE=0`.

BYTEA supports raw `||`, `bit_length`, `octet_length`, `length`, two-argument
`btrim`/`ltrim`/`rtrim`, and byte-indexed `overlay`, `position`, `substring`/`substr`.
SQL `OVERLAY(... PLACING ... FROM ... [FOR ...])`, `POSITION(needle IN source)`,
`SUBSTRING(... FROM ... [FOR ...])` (also reversed keywords and FOR-only), and
all BOTH/LEADING/TRAILING TRIM forms use the same strict raw-octet providers.
Offsets and counts are INT4 bytes, not characters; POSITION returns one-based
INT4 or zero when absent (an empty needle returns one). Negative substring
counts fail; negative overlay counts are valid and can duplicate source bytes.
Trim removes endpoint members of a byte set, preserving interior NUL/high bytes.
Known BYTEA operands infer unknown byte arguments; inferred Go binds remain raw
octets. Selected functions share query/write/CHECK/view consumers and native
replay, preserving SQL-syntax provenance in compact/pretty CHECK deparsing.
TEXT/regex substring, encoding-aware length, BYTEA DEFAULT functions and direct
byteacat remain excluded; no new cast edges or byteapos alias are added.
Core strings provide TEXT trim/concatenation siblings through the shared dispatch.
`length`, `char_length`/`character_length`, `octet_length` and `bit_length`
also measure strings; `strpos(source,needle)` and SQL POSITION search them.
Character counts and search positions count Unicode codepoints, not UTF8 bytes.
Exact CHAR octet counts include padding; its bit counts and search operands use
the trimmed TEXT conversion. Results are strict INT4, with zero for not found and
one for an empty needle. Selected string functions compose in DEFAULT/CHECK/views
and native replay. See [String length and search](docs/DIALECT.md#string-length-and-search--change-note).

String replacement supports `overlay(text,text,int4[,int4])` and SQL OVERLAY,
literal `replace` and codepoint-wise `translate`. Overlay counts characters,
including combining marks separately: zero count inserts, negative count can
duplicate a source interval, and a start beyond the end appends without padding.
Omitted count is the trimmed replacement's character length. Empty replace
search leaves the source unchanged; translation uses the first occurrence in
its `from` set and deletes characters with no corresponding `to` character.
`starts_with(text,text)` and `text ^@ text` are equivalent strict BOOL prefix
operations. All textual arguments use the existing BPCHAR-to-TEXT trim, while
TEXT/VARCHAR tails remain. These providers compose through queries, writes,
DEFAULT/CHECK/views and native Dump/Restore, retaining TEXT/BOOL metadata.
See [replacement and prefix rules](docs/DIALECT.md#string-replacement-and-prefix--change-note).
Independent native/reference witnesses:
`make quickcheck PACKAGES=./reference RUN='^(TestReferenceStringReplace(Overlay|Replace|Translate|Prefix|Signatures|Diagnostics|Collation|Metadata|Consumers|Atomicity|Stored|Controls|Cancellation)|TestReferenceBinarySliceCapabilityBoundaries)$' VERBOSE=0`;
the pinned PostgreSQL counterpart uses the same command with `REFERENCE=1`.
See [BYTEA](docs/TYPES.md#bytea). Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceBinarySlice' VERBOSE=0`; independent pinned reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinarySlice' VERBOSE=0`.

BYTEA checksums and hashes operate on the raw octets, including NUL and high
bytes: `crc32`/`crc32c` return unsigned 32-bit checksums in INT8, `md5(bytea)`
returns 32 lowercase hexadecimal TEXT characters, and `sha224`/`sha256`/`sha384`/
`sha512` return raw BYTEA digests of 28/32/48/64 bytes. All are strict; empty
input has a nonnull digest. CRC/SHA infer BYTEA for unknown literals and binds;
MD5 requires an already-known BYTEA operand. Native MD5 TEXT/unknown calls
remain missing-function boundaries (42883), independently tested against
PostgreSQL's TEXT overload. Hashes compose in queries, writes, CHECKs and views,
and parameter-free hash calls are admitted in native Restore INSERTs; DEFAULT
functions remain excluded. Cryptographic hashes use bounded scratch; all
providers check cancellation without payload-sized temporary copies. See the
[hash contract](docs/DIALECT.md#bytea-checksums-and-hashes--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryHashSmoke$' VERBOSE=0`;
pinned counterpart: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryHash' VERBOSE=0`.

Change note: core strings support `||` string overloads, `btrim`/`ltrim`/`rtrim`,
SQL `TRIM`, `normalize` and `IS [NOT] [form] NORMALIZED`. Character operands
convert to TEXT, removing BPCHAR trailing spaces; mixed concatenation uses the
existing scalar type's text output and captured session output settings.
Normalization uses pinned Unicode16 data and PostgreSQL18.0 release semantics,
including its Hangul TBASE boundary and normalized-predicate quickcheck.
Stored string DEFAULT/CHECK expressions support compact/pretty SQL deparsing,
typed native structural carriers and syntax-preserving Dump/Restore. Ordinary
calls display resolved TEXT conversions; special syntax hides implicit casts.
Explicit-set BYTEA SQL TRIM keeps its special spelling through CHECK replay
without adding BYTEA DEFAULT or cast support. Parameter-free Restore INSERTs
accept the selected string functions, and trim checks cancellation during both
endpoint and set traversal without allocating a replacement string.
See [syntax and boundaries](docs/DIALECT.md#core-string-functions--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestStringCoreSmoke$' VERBOSE=0`.

Change note: `lower`, `upper`, `casefold` and `initcap` accept one character
input and return unconstrained TEXT, retaining its collation. Default/C/POSIX
map ASCII only; `pg_c_utf8` uses simple Unicode16 mappings and
`pg_unicode_fast` uses full Unicode16 mappings, including expansions and
original-input final sigma with PostgreSQL18.0's leading-ignorable-prefix
boundary. Folding is not lowercasing or normalization.
BPCHAR padding is removed before conversion; TEXT/VARCHAR tails remain.
Collated unknown parameters are inferred as TEXT before collation lookup;
each occurrence retains its own collation without changing the held input type.
These functions compose through existing query/write/default/CHECK/view paths
and native Dump/Restore. Native encoding remains read-only UTF8; arbitrary
libc/ICU locales and Unicode-collation ILIKE remain explicit exclusions.
See [case rules](docs/DIALECT.md#core-string-functions--change-note) and
[offline data generation](docs/TYPES.md#unicode16-normalization-data).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceStringCaseSmoke$' VERBOSE=0`;
pinned oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringCaseSmoke$' VERBOSE=0`.

Qualified temporal declarations such as `pg_catalog.time(3)` and
`pg_catalog.timestamp(3)` retain precision through native Dump/Restore;
bounded temporal explicit casts and same-type precision annotations are supported.
`SELECT current_schemas(false)` returns a genuine bounded NAME[] (`_NAME`)
as a native string, or NULL for a NULL boolean argument. Its codec also supports
the selected domain-base input/storage/cast path; general arrays and
CREATE TYPE/function/operator DDL remain excluded. TEMP remains sequence-only;
introspection does not allocate an absent temporary namespace.
Native ORDER on NAME[] is explicitly unsupported at analysis, including empty
sources; PostgreSQL's array comparator remains outside the bounded native surface.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSearchPathSmoke$' VERBOSE=0`.

Change note: standalone `ALTER TABLE [IF EXISTS] target SET SCHEMA destination`
moves ordinary tables, their simple B-tree indexes and every column-owned sequence atomically, preserving
rows, named NOT NULL constraints, defaults, comments, ACLs and stored-view
dependencies. It requires table ownership, source USAGE and destination CREATE
(not source CREATE or destination USAGE). Native prepared SQL rebinds its original
names at Execute; unlike PostgreSQL's held unchanged-search_path plan, an unqualified
native SELECT sees a newly created source-schema shadow after a move (see
[the dialect boundary](docs/DIALECT.md)). Native Dump/Restore records current
qualified definitions, not move history.
For a view, `ALTER TABLE view SET SCHEMA` aliases `ALTER VIEW SET SCHEMA` and
moves only the view's namespace, leaving its sources and dependents in place.
Explicit-transaction SET SCHEMA, generic standalone sequence/index moves,
TEMP/system destinations and automatic row types remain bounded native exclusions.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterSchemaSmoke$' VERBOSE=0`.

Change note: ordinary `CREATE INDEX [name] ON [schema.]table [USING btree]
(key [ASC|DESC] [NULLS FIRST|LAST], ...)` supports 1–32 simple or same-table scalar
expression keys, including `lower(text_column)` and parenthesized operators,
including duplicates and NULLs on already populated tables. Definitions share the
relation namespace; immutable sorted logical entries stay current through all
committed INSERT/UPDATE/DELETE batches and supported table lifecycle changes.
Query correctness still uses the ordinary snapshot scan/filter/sort path, not
an optimizer promise; unordered SELECT has no new ordering guarantee. Dump/Restore
serializes actual current index definitions and rebuilds entries. Native CREATE
INDEX is excluded in explicit transactions, unlike PostgreSQL; standalone CREATE
UNIQUE INDEX, partial and concurrent indexes remain excluded. Expression keys
use the shared binder/evaluator and must be immutable after constant branch
simplification; aggregate/subquery keys fail at Execute after quiet Prepare.
Shared smokes: `make quickcheck PACKAGES=./reference RUN='^TestReferenceBtreeIndex(Regression|Controls|Lifecycle)$'`;
use the same RUN with `REFERENCE=1` for the independent pinned PostgreSQL oracle.
Expression regression family: `make quickcheck PACKAGES=./reference RUN='^TestReferenceExpressionIndex'`;
use the same RUN with `REFERENCE=1` for independently expected PostgreSQL18.0 outcomes.

Change note: CREATE TABLE column/table UNIQUE supports ordered simple keys and
`NULLS DISTINCT` (default) or `NULLS NOT DISTINCT`. Each retained constraint owns
a real immutable logical B-tree; INSERT and UPDATE enforce typed equality
immediately, with atomic failures and transaction-owned key claims. Constraint
and backing-index identities survive renames and schema moves, rebuild on key
TYPE, and disappear on DROP CONSTRAINT/key column/table. Dump/Restore emits the
constraint once and reconstructs its backing index. Deferrability, ALTER ADD
UNIQUE and standalone CREATE UNIQUE INDEX remain excluded.
See [UNIQUE constraints](docs/TABLES.md#unique-constraints--change-note) and
[the design](docs/design/PG18-DDL-UNIQUE.md).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUniqueFeatureSmoke$'`.


Change note: `ALTER TABLE target ALTER [COLUMN] name SET DEFAULT expression`
and `DROP DEFAULT` atomically change future omitted/DEFAULT inserts without
rewriting existing rows. Held prepared INSERTs use the current definition;
DROP of an absent default succeeds and restores implicit NULL. ALTER accepts
general supported Boolean expressions without CREATE DEFAULT's extra parentheses.
Dump/Restore preserves the current definition, not ALTER history.
Standalone `ONLY`, `ONLY(...)`, qualified/quoted targets and trailing `*` are
supported; inheritance remains unsupported (release recursion is reference-only).
Action lists and ALTER TABLE IF EXISTS for SET/DROP DEFAULT, UPDATE DEFAULT and
explicit-transaction SET/DROP DEFAULT remain excluded. Selected ADD COLUMN/
TYPE/nullability/DROP COLUMN lists support explicit transactions.
See [column defaults](docs/TABLES.md#column-defaults).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterDefaultSmoke$' VERBOSE=0`.

Change note: `ALTER TABLE target ADD [COLUMN] [IF NOT EXISTS] name type`
atomically appends metadata and old-row values, with supported NULL/NOT NULL,
DEFAULT and column CHECK declarations. Missing defaults backfill typed NULL;
Immutable/Stable defaults evaluate once at ADD (even for an empty table), while
the existing fixed-nextval default evaluates per live row. Future INSERTs use
the retained expression, not the captured old-row value. Validation failure
preserves schema/data; already demanded sequence draws are not rolled back.
IF NOT EXISTS notices and skips before semantic declaration binding.
Local standalone-table behavior only: no descendant propagation, serial/identity/
generated ADD declarations, new default functions or mixed SET/DROP DEFAULT lists.
Logical Dump/Restore preserves final definitions/data, not ADD history or physical
PostgreSQL tuple storage. Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$' VERBOSE=0`.


Persistent `CREATE VIEW` definitions use the ordinary typed SELECT pipeline,
including admitted joins, repeated self-join occurrences, nested views, explicit
output names and `security_invoker` / `security_barrier` options. Every source
occurrence retains its frozen attribute layout and read-attribute identities across table,
column and schema renames; dependencies block restrictive drops, including
columns used only by a second source's join condition. Each execution checks
current privileges for every source, even for empty results. Bounded, queryable
`pg_class`, `pg_rewrite` and `pg_views` sources expose actual catalog
state, with OID and PostgreSQL internal CHAR metadata. Native Dump/Restore
persists definitions, not their result rows. See [views](docs/SQL.md#views).
CREATE OR REPLACE preserves view/rule identity, ownership, ACLs and dependents,
allows trailing outputs and rejects changed existing names/types/typmods/collations.
ALTER VIEW supports relation/column rename, owner/schema changes and security
SET/RESET; grouped DROP VIEW supports RESTRICT/CASCADE. Cyclic replacement is
allowed, while recursive reads and native cyclic Dump explicitly fail.
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceView(Rows|Barrier|SmokeCatalogReplayControls)$' VERBOSE=0`.
Replacement smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceViewReplace(Definition|Diagnostics|Prepared|SmokeCycleReplayControls)$' VERBOSE=0`.

Change note: search-path visibility inquiries `pg_table_is_visible(oid)`,
`pg_type_is_visible(oid)`, `pg_collation_is_visible(oid)` and
`pg_conversion_is_visible(oid)` return strict nullable BOOL. Missing identities
return NULL; existing off-path or shadowed identities return false. Relation
visibility includes supported tables, views, sequences, ordinary B-tree indexes
and catalog relations. Index identities share the relation namespace, so an
earlier same-named table, view or sequence can shadow an index and vice versa.
The subject's SELECT privilege is irrelevant; searchable schema USAGE still
filters the path. Held prepared executions see fresh catalog/path state, while
already returned results remain immutable.
Argument analysis precedes the 100-argument guard, then namespace/signature
lookup and OID inference/coercion. Missing child columns therefore take precedence
over missing explicit schemas, including in over-limit direct/prepared calls.
Native inquiries cover all25 seeded types, seven fixed PostgreSQL18 collations
and all128 fixed conversions. Comparison supports default/C/POSIX plus
`pg_c_utf8`/`pg_unicode_fast`; `ucs_basic`/`unicode` remain inquiry-only,
and no encoding conversion executes.
Dump/Restore rebuilds these immutable identities without inventing owners or
ACLs, including empty restores and imports into an independent Driver.
Inquiry catalog/path capture is demand-driven, including nested views,
RETURNING and transformed ALTER CHECK validation; unrelated statements do not
retain a visibility snapshot. Stored inquiry CHECKs support compact/pretty
pg_get_constraintdef/pg_get_expr output and behavioral replay.
For bounded predicate syntax, project an inquiry in a view and filter its BOOL
column; INSERT results are checked by a subsequent SELECT, while UPDATE/DELETE
support RETURNING. Discover backend-local numeric relation identities by projecting
`oid,pg_table_is_visible(oid)` from pg_class under each target search_path and
requiring exactly two distinct positive candidate OIDs, one visible candidate,
and the same identity bag after reversal. No pg_namespace query source or
explicit regclass-to-oid cast is needed.
CREATE TYPE/COLLATION/CONVERSION, automatic row types and materialized/foreign
table creation remain excluded. See [visibility inquiries](docs/QUERIES.md#search-path-visibility-inquiries).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestVisibilityInquirySmoke$' VERBOSE=0`.
Index identity/path/control witnesses:
`make quickcheck PACKAGES=. RUN='^TestVisibilityInquiryIndexRelations$' VERBOSE=0`;
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceVisibilityInquiryIndexRelations$' VERBOSE=0`.

Object comment inquiries support `col_description(oid,integer)`,
`obj_description(oid,name)`, legacy `obj_description(oid)` and
`shobj_description(oid,name)`, all returning strict nullable TEXT. Local
relation/column/schema/type descriptions and shared role descriptions stay
separate. Legacy collision lookup chooses an unspecified matching local comment
consistently within a statement, with no portable catalog precedence. Matching
fixed signatures accept VARIADIC decoration; ordinary type/NULL analysis remains.
Each execution captures immutable comment metadata; held prepared statements
refresh on their next execution. Views preserve strict constant-NULL inquiry
syntax without runtime sibling demand. Native builtin namespace/type descriptions
and explicit namespace removals round-trip through native controls.
See [semantics and boundaries](docs/SQL.md#object-comment-inquiries--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCommentInquirySmoke$' VERBOSE=0`.

LIKE/NOT LIKE and ILIKE/NOT ILIKE support whole-string `%`/`_` matching,
keyword ESCAPE and symbolic `~~`/`!~~`/`~~*`/`!~~*` operators. TEXT wildcards
advance UTF8 code points; existing BYTEA LIKE advances octets. BPCHAR left
operands retain physical padding. Supported default/C/POSIX ILIKE folds ASCII
only; C and POSIX remain distinct collation identities. Scalar COLLATE also
supports `pg_c_utf8`/`pg_unicode_fast`, with case-sensitive LIKE but a positioned
0A000 ILIKE boundary. Explicit C on either operand overrides implicit Unicode
collation and permits ASCII ILIKE. Arbitrary locales, regex and SIMILAR TO remain excluded.
Collatable wrappers retain identity and demanded conflicts; indeterminate TEXT
view outputs are rejected before publication, while BOOL matching views replay.
See [matching semantics](docs/DIALECT.md#like-and-ilike--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestLikeSmoke$' VERBOSE=0`.

Standalone `VALUES (1,'a'),(1,'a'),(2,NULL)` and parenthesized VALUES in FROM are
supported queries. Optional relation/column aliases expose computed `columnN`
outputs with independent common typing (unknown-only becomes TEXT). Duplicate
bags, local ordering/counts, inner/cross joins, INSERT SELECT and stored views
share existing binding, execution, metadata and native replay paths. VALUES
cells are non-lateral and reject DEFAULT and aggregates; derived SELECT and
LATERAL remain excluded. Ordinary INSERT VALUES retains destination-context
typing. See [VALUES contracts and change note](docs/QUERIES.md#standalone-and-from-values--change-note-changelog).
Independent reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryFromValues' VERBOSE=0`.

SELECT, shared INSERT SELECT and decorated VALUES support expression ORDER BY,
output labels/ordinals, multiple keys and per-key ASC/DESC/NULLS FIRST/LAST.
Referenced output values are evaluated once before sort and reused. With a
count decoration, cheap immutable outputs run below Sort; unrelated volatile
outputs run after Sort but before OFFSET/LIMIT. Unordered queries and complete
sort peers have no portable relative order. General COLLATE remains excluded.
See [ordering semantics](docs/QUERIES.md#expression-ordering--change-note-changelog).
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryOrderSmoke$' VERBOSE=0`.

SELECT, shared INSERT SELECT and decorated VALUES support LIMIT/OFFSET and
FETCH FIRST/NEXT ROW/ROWS ONLY. Counts use BIGINT assignment conversion; zero
suppresses producing children without bypassing typing, authorization or bound
errors. Nonblocking views/filters/joins produce only demanded occurrences;
sorting/grouping remain blockers. Local rowmarks settle at effect frontiers,
not across a local count boundary. See [count and demand](docs/QUERIES.md#limit-offset-and-fetch--change-note-changelog).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryLimit(Smoke|LockSmoke)$' VERBOSE=0`.

SELECT (including existing inner/cross joins, persistent scalar views and INSERT
SELECT sources) supports one trailing `FOR KEY SHARE`, `FOR SHARE`,
`FOR NO KEY UPDATE` or `FOR UPDATE`. Locks belong to contributing physical base
RowIDs, not view output values or the entire table. Stored view clauses also apply
to otherwise ordinary reads; unmarked SELECT remains a nonblocking MVCC read.
Enclosing rowmarks over an outer join reject on Execute, including LIMIT0;
Prepare still describes a valid query. An unmarked outer query may consume locally
marked views on either input. Native locks real source rows at its left-major
demand frontier, never absent NULL extensions; PostgreSQL may scan extra rows
through a different physical plan. LIMIT0 locks none.
READ COMMITTED lockers recheck
the current version after contention; REPEATABLE READ KEY SHARE retains snapshot
values only if no UNIQUE key datum changed since its snapshot. Restoring a key or
later changing only payload does not erase a committed key transition. Stronger
modes and deletions fail. UPDATE uses NO KEY UPDATE unless a UNIQUE key datum
changes, when it uses UPDATE; DELETE uses UPDATE. See
[row locking](docs/TRANSACTIONS.md#row-locks--change-note) for the compatibility
matrix, privileges, eager execution and bounded syntax.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestRowLock(Smoke|JoinSmoke|VersionSmoke|ViewSmoke|OrderedCacheSmoke)$' VERBOSE=0`.

Selected `ALTER TABLE` nullability and named NOT NULL operations compose atomic
action lists with `DROP COLUMN`, including explicit transactions/savepoints.
Validation includes fresh grant-time committed NULLs and own writes; SET/DROP
alone does not copy stored values. Private metadata, row projections and DML
publish together at Commit; Dump excludes private changes.
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestAlterNullability(Smoke|ControlsSmoke)$' VERBOSE=0`.

Identity columns support `GENERATED ALWAYS` and `GENERATED BY DEFAULT AS IDENTITY`
on SMALLINT, INTEGER and BIGINT, using an internally owned sequence rather than a
stored DEFAULT. INSERT omission/DEFAULT generates; SYSTEM and USER overrides,
identity ALTER lists, transactions/savepoints and native Dump/Restore reuse the
existing sequence/session authorities. Identity is NOT NULL, not inherently unique.
Existing CHECK calls can use the same declaration's identity sequence without
definition-time allocation. Dump orders identity-sequence view/CHECK dependents
after their owning table, including retained TEMP collision remaps.
Stored expression-generated columns support `GENERATED ALWAYS AS (expression) STORED`
through the normal typed INSERT/UPDATE path. Omission and explicit DEFAULT compute
the stored value; other explicit generated-column writes fail428C9. See
[stored generated columns](docs/TABLES.md#stored-generated-columns). Virtual columns
and generated-expression ALTER lifecycle remain excluded.
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestIdentity(Smoke|ControlsSmoke|CheckControlsSmoke)$' VERBOSE=0`.
The ALTER lifecycle is shared reference coverage:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAlterIdentity' VERBOSE=0`;
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterIdentity' VERBOSE=0`.

SMALLSERIAL/SERIAL/BIGSERIAL (and serial2/4/8) expand CREATE columns to
SMALLINT/INTEGER/BIGINT NOT NULL, ordinary nextval defaults and AUTO-owned
sequences. Supplied values do not advance the sequence or imply uniqueness;
explicit NULL fails NOT NULL. Omission/DEFAULT consumes once per demanded row,
requires sequence privileges and can leave gaps after failed rows/rollback.
Owning column/table drops remove sequences; direct sequence RESTRICT protects
the default, while CASCADE removes only that default. Native Dump/Restore retains
integer columns, actual sequence names/options/state and bound references.
The shared DEFAULT surface admits only fixed early-bound nextval leaves and
assignment/cast wrappers, not general volatile expressions.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSerialSmoke$' VERBOSE=0`.

Change note: `ALTER TABLE target ALTER [COLUMN] c [SET DATA] TYPE type
[COLLATE name] [USING expression]` joins those atomic action lists. Assignment
conversion and USING evaluate current live rows atomically; every USING reads
the original tuple, including columns dropped in the same command. Defaults
are independently coerced; dependent CHECKs are reparsed from their original
definitions against the final types. Each completed transformed row passes
NOT NULL and affected CHECK validation before the next row is converted, with
no partial publication on failure. Dependent views block the change.
Column COLLATE supports real pg_catalog default/C/POSIX and
`pg_c_utf8`/`pg_unicode_fast` identities under deterministic UTF8 byte/codepoint
ordering; native Dump/Restore retains them.
Fresh results expose the new type/typmod; held prepared results reject changed
type/typmod/collation while eager rows retain their original metadata and values.
Identity TYPE also migrates the existing internal sequence to the integral target,
including same-type AS/cache replacement; plain OWNED BY sequences are unchanged.
Private sequence state and row changes publish together, while caller currval and
already-discarded cache tails remain physical-session effects on rollback.
See [ALTER TYPE semantics and boundaries](docs/DIALECT.md#alter-column-type--change-note).
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestAlterType(Smoke|ControlsSmoke|IdentitySmoke|IdentityControlsSmoke)$' VERBOSE=0`.


The [supported types](docs/TYPES.md) include NAME values: mixed-case spelling
is preserved, textual input clips at a63-byte UTF8 boundary, and comparisons use
C byte order. NAME has distinct metadata from TEXT and round-trips through
native Dump/Restore; it does not change SQL identifier folding or add general
name casts/functions. Public feature smoke:
`make quickcheck PACKAGES=./smoke RUN='^TestNameSmoke$' VERBOSE=0`.

OID is a distinct unsigned four-byte scalar with exact native int64 results,
unsigned ordering, PostgreSQL integer bitcasts/checked INT8 conversion and
native schema/data/default Dump/Restore. It does not populate metadata catalogs
or add object lookup, arithmetic or ordinary SELECT-without-FROM support.
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestOIDSmoke$' VERBOSE=0`.

Integral bitwise `&`, `|`, `#`, unary `~`, `<<` and `>>` support SMALLINT,
INTEGER and BIGINT in existing scalar consumers, including replayable defaults
and views. Width, signed results, typed NULL and prepared parameter inference
are preserved; shifts use an INTEGER count. See [integral bitwise operators](docs/TYPES.md#integral-bitwise-operators).
Catalog-qualified OPERATOR spellings use the same provider and namespace checks,
including canonical defaults and view replay.
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.

Scalar comparison types also support inclusive `BETWEEN`/`NOT BETWEEN` and
both `SYMMETRIC` forms in existing expression consumers. Symmetric predicates
accept reversed bounds; NULL follows SQL three-valued logic rather than a
strict three-argument rule. See [BETWEEN predicates](docs/QUERIES.md#between-predicates)
for grouped endpoints, contextual prepared typing, replayable grouped/nested
defaults and bounded syntax. Public feature smoke:
`make quickcheck PACKAGES=./smoke RUN='^TestBetweenSmoke$' VERBOSE=0`.

Supported transaction-owned relation and logical row locks include deadlock
detection: compatible queue reordering resolves avoidable soft cycles; an
unavoidable cycle returns `40P01` / `deadlock detected` to an unspecified cycle
member. Without a savepoint, statement completion rolls back that victim and
releases its locks before explicit ROLLBACK. With recoverable savepoints,
pre-mark effects and holdings survive while failed-level relation/row lock
contributions are released before client recovery. See
[transaction recovery and logical diagnostics](docs/TRANSACTIONS.md).

Explicit `LOCK [TABLE]` supports ordered relation lists, per-target ONLY/star,
all eight table modes (default ACCESS EXCLUSIVE), NOWAIT, documented ANY-rights
privileges and recursive view locking. Native tables remain standalone; no
inheritance or SQL GRANT is introduced. Prepare is metadata-only; Execute
requires an explicit block and affects zero rows. Savepoint rollback restores
earlier held modes, including incomparable modes, without replacing the ticket.
Feature checks use the same untagged reference definitions in both modes:
`make quickcheck VERBOSE=0 PACKAGES=./reference RUN='^TestReferenceLockModes'` (Docker-free)
and `make quickcheck REFERENCE=1 PACKAGES=./reference VERBOSE=0 RUN='^TestReferenceLockModes'` (native and pinned PostgreSQL).
See the [LOCK TABLE command change note](docs/TRANSACTIONS.md#lock-table-command--change-note).

Selected mathematical functions include `random()`, inclusive INTEGER/BIGINT/
NUMERIC bounded `random(lower,upper)`, `random_normal([mean[,stddev]])` and
`setseed(seed)`. Exact NUMERIC sampling preserves the greater input scale.
Seeds and draws belong to the physical connection and survive rollback and
pool return; use a pinned `sql.Conn` for repeatable series. These are
pseudorandom, not cryptographic, and do not promise PostgreSQL's exact PRNG
sequence. See [the capability/change note](docs/DIALECT.md#random-functions--change-note).
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.

Exact scale and rounding functions include `min_scale`, `round`, `scale`,
`trim_scale`, `trunc`, `sign` and `mod`, with PostgreSQL's selected NUMERIC,
FLOAT8 and integer overloads. NUMERIC ties round away from zero; negative
scales and exact remainder never pass through floating point. See
[the signature and behavior note](docs/DIALECT.md#scale-and-rounding-functions--change-note).
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestMathSmoke$' VERBOSE=0`.

INTERVAL retains independent months/days/microseconds, permitted field ranges
and fractional precision, verbose/SQL/ISO input, SQL comparison/grouping and
canonical native Dump/Restore. Each physical session owns its IntervalStyle;
prepared literals freeze input meaning and retained rows capture output style.
See [interval values](docs/TYPES.md#interval--change-note) and
[IntervalStyle](docs/SETTINGS.md#intervalstyle--change-note).
Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestIntervalSmoke$' VERBOSE=0`.



## Minimal example: one unit test

In a checkout of this module, save the following as `example_test.go` and run
`make quickcheck PACKAGES=. RUN='^TestPersonName$'`. In a consuming project, add
stuntgres to your module dependencies and run the test with `go test`.

```go
package stuntgres_test

import (
    "context"
    "database/sql"
    "testing"

    "stuntgres"
)

func TestPersonName(t *testing.T) {
    ctx := context.Background()
    connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
    if err != nil {
        t.Fatal(err)
    }
    db := sql.OpenDB(connector)
    t.Cleanup(func() { db.Close() })

    if _, err := db.ExecContext(ctx,
        "CREATE TABLE people (id INTEGER NOT NULL, name TEXT NOT NULL)"); err != nil {
        t.Fatal(err)
    }
    if _, err := db.ExecContext(ctx,
        "INSERT INTO people (id, name) VALUES ($1, $2)", 1, "Ada"); err != nil {
        t.Fatal(err)
    }

    var name string
    if err := db.QueryRowContext(ctx,
        "SELECT name FROM people WHERE id = $1", 1).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Ada" {
        t.Fatalf("name = %q, want Ada", name)
    }
}
```

The test uses ordinary `database/sql` execution, PostgreSQL-style `$1` parameters,
and `Scan`. Pass `db` to the code under test just as you would a PostgreSQL pool.
A fresh `Driver` gives this test its own database ownership; cleanup closes the
pool without affecting other tests.

Importing stuntgres also registers the driver for `sql.Open("stuntgres", name)`.
The name is an exact opaque string, not a connection URL: identical names share
data within the same Driver, even after pools close. Prefer a fresh `NewDriver`
per fixture when tests must be isolated. See [driver lifecycle](docs/DRIVER.md)
for connection pooling and lifetime details.

## Reusable fixture: two unit tests

Use a helper to create an isolated database, apply the schema, seed data, and
register cleanup. Save this example in a separate `fixture_example_test.go` file;
run it with `make quickcheck PACKAGES=. RUN='^TestPeopleFixture'` in this checkout,
or `go test` in a consuming project.

```go
package stuntgres_test

import (
    "context"
    "database/sql"
    "testing"

    "stuntgres"
)

func newPeopleFixture(t *testing.T) *sql.DB {
    t.Helper()
    connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
    if err != nil {
        t.Fatal(err)
    }
    db := sql.OpenDB(connector)
    t.Cleanup(func() { db.Close() })

    ctx := context.Background()
    for _, query := range []string{
        "CREATE TABLE people (id INTEGER NOT NULL, name TEXT NOT NULL)",
        "INSERT INTO people (id, name) VALUES (1, 'Ada'), (2, 'Grace')",
    } {
        if _, err := db.ExecContext(ctx, query); err != nil {
            t.Fatal(err)
        }
    }
    return db
}

func TestPeopleFixtureLookup(t *testing.T) {
    t.Parallel()
    db := newPeopleFixture(t)

    var name string
    if err := db.QueryRowContext(context.Background(),
        "SELECT name FROM people WHERE id = $1", 2).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Grace" {
        t.Fatalf("name = %q, want Grace", name)
    }
}

func TestPeopleFixtureDelete(t *testing.T) {
    t.Parallel()
    db := newPeopleFixture(t)
    ctx := context.Background()

    result, err := db.ExecContext(ctx, "DELETE FROM people WHERE id = $1", 1)
    if err != nil {
        t.Fatal(err)
    }
    affected, err := result.RowsAffected()
    if err != nil {
        t.Fatal(err)
    }
    if affected != 1 {
        t.Fatalf("deleted %d rows, want 1", affected)
    }

    var name string
    err = db.QueryRowContext(ctx, "SELECT name FROM people WHERE id = $1", 1).Scan(&name)
    if err != sql.ErrNoRows {
        t.Fatalf("lookup after delete: %v, want sql.ErrNoRows", err)
    }
    if err := db.QueryRowContext(ctx,
        "SELECT name FROM people WHERE id = $1", 2).Scan(&name); err != nil {
        t.Fatal(err)
    }
    if name != "Grace" {
        t.Fatalf("surviving name = %q, want Grace", name)
    }
}
```

Each helper invocation owns a fresh Driver and seeded database, so the tests can
run in parallel without sharing mutations. `t.Helper` attributes setup failures
to the calling test; `t.Cleanup` closes the pool even when an assertion fails.
For larger fixtures, [native Reset, Dump and Restore](docs/FIXTURES.md) can clear
or replace database contents. Those controls require a native stuntgres pool.

Table queries also support scalar/multiple `GROUP BY` keys, output ordinals,
`HAVING`, `count(*)` and `count(expression)` (both `INT8` results). Ordinary
count evaluates its argument per source row and counts only non-NULL values.
`bool_and`, `bool_or` and `every` accept BOOL arguments and return nullable BOOL;
`every` is the AND operation with its own output label. They ignore NULL inputs
and return NULL for empty/all-NULL input. Arguments still evaluate after a
decisive false AND or true OR, so late errors veto the entire result.
`avg(INTERVAL)` returns nullable INTERVAL, ignoring NULL and returning NULL for
empty/all-NULL input. Its checked month/day/microsecond sums retain physical
representation; fractional months cascade at 30 days and fractional days at 24
hours, with PostgreSQL floating-point nearest-even rounding. For example,
averaging `1 mon` and zero yields `15 days`, not a normalized duration.
The result has unrestricted interval precision.
`avg(SMALLINT/INTEGER/BIGINT/NUMERIC)` returns nullable, unrestricted NUMERIC
with an exact sum and PostgreSQL's base-10000 division scale (at most 1000
fractional digits). The packed sum can overflow even when the mean would fit.
`avg(REAL/DOUBLE PRECISION)` returns FLOAT8; REAL is widened from binary32.
PostgreSQL's full Youngs–Cramer transition is retained, so variance-state
overflow can reject a finite mean. All overloads ignore NULL and return typed
NULL for empty/all-NULL input. For `1,NULL,3`, the mean is numeric
`2.0000000000000000` or floating `2`. No unordered floating accumulation order
is promised. Public AVG feature checks use independent assertions in both modes:
`make quickcheck PACKAGES=./reference RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$' VERBOSE=0`.
`any_value(anyelement)` returns an arbitrary non-NULL input of its concrete type,
or typed NULL for empty/all-NULL input. It preserves the datum representation
and collation, but not input typmod or physical origin; metadata is nullable.
No stable winner is promised when candidates differ. Explicitly type unknown
arguments, for example `any_value($1::integer)`. Later argument errors still
veto all output after a candidate is retained. Public direct/prepared, view,
HAVING/ORDER and INSERT SELECT witnesses share one independent contract:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAnyValue' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAnyValue' VERBOSE=0`.
Change note: `min(value)` and `max(value)` ignore NULL and return the smallest
or largest eligible datum, or typed NULL for empty/all-NULL global input.
Supported overloads cover integers, OID, floating/exact numeric, TEXT/BPCHAR,
BYTEA and the implemented date/time/interval types. VARCHAR, NAME and internal
CHAR select TEXT; REGCLASS selects OID; domains resolve through their base type.
Unknown strings, NULL and untyped parameters infer TEXT. Results have unrestricted
typmod, nullable metadata and no physical origin; the selected datum is not
rounded or reconstructed. `SELECT min(v),max(v) FROM grouped_rows` uses the same
eager pipeline as grouped/HAVING/ORDER queries, views and INSERT SELECT.
Conflicting implicit string collations fail only when a second eligible value
requires comparison (`42P22`); use a final explicit COLLATE to resolve them.
BOOL/UUID have no overload (`42883`); NAME[] and private PG_NODE_TREE remain
explicit native capability boundaries, without adding arrays or general casts.
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceExtremaSmoke$' VERBOSE=0`;
independent contracts: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceExtrema' VERBOSE=0`.
Change note: `string_agg(text,text)` and `string_agg(bytea,bytea)` return nullable
TEXT/Go string and BYTEA/raw Go []byte. NULL values are ignored; a NULL delimiter
adds no separator. Each later surviving value uses its own row's delimiter;
the first delimiter is ignored and empty values still count as values.
Empty/all-NULL global input returns typed NULL. Use call-local
`ORDER BY` with a unique tiebreaker to guarantee concatenation order; outer
ordering only orders result groups. Each call can use different source-key order.
Direct/prepared queries, held statements, INSERT SELECT and persistent views/native
Dump/Restore use the same eager pipeline. DISTINCT/FILTER/ALL/OVER, ordered sibling
aggregates, no-FROM aggregates and array_agg remain excluded.
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceStringAggSmoke$' VERBOSE=0`;
pinned oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringAgg' VERBOSE=0`.
Equal typed keys, including NULL keys, form one group; empty global count returns one row
containing zero, while empty grouped input returns no rows. Group order is not
promised. The same eager pipeline serves direct/prepared queries and
`INSERT ... SELECT`; public rows never evaluate aggregates during `Next`.
See [query grouping](docs/QUERIES.md#grouping-and-count--change-note) for examples,
diagnostics and deliberate exclusions.

## Documentation

- [SQL dialect and compatibility limits](docs/DIALECT.md): check support before
  using PostgreSQL-specific behavior.
- [SQL reference](docs/SQL.md): topic guides for queries, table definitions,
  sequences, expressions, types, identifiers, and literals.
- [Driver lifecycle and notices](docs/DRIVER.md), [session settings](docs/SETTINGS.md),
  and [transactions and locks](docs/TRANSACTIONS.md).
- [Roles, authorization and schemas](docs/AUTHORIZATION.md).
- [Native fixture controls](docs/FIXTURES.md).

## Building, testing and contributing

Use Go 1.26 or later and GNU `timeout`. From the repository root:

```sh

make build      # compile production packages
make quickcheck # uncached Docker-free unit, public smoke and quick reference tests
make fullcheck  # after merge: generate/build, quick + native/reference race, bench/deps
```

Each quickcheck invocation enforces a 900-second wall-clock deadline and is quiet by default.
Final fullcheck runs after merge under one 1800-second total deadline; commit failure remedies
separately. Use `VERBOSE=1` for diagnostics, `RUN='^TestName$'` for selection,
`PACKAGES=./internal/parser` for package scope, and `RACE=1` for the race detector.
Every changed/new reference definition requires selected native and real pinned PostgreSQL
validation before review/integration; it cannot wait for final fullcheck:
`make quickcheck PACKAGES=./reference RUN='...'` and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='...'`.

The migrated query-read, scalar-type, INSERT SELECT, UPDATE, DELETE/RETURNING,
identity, arithmetic, bitwise, mathematical and scalar expression scenarios,
DATE/TIME/TIMESTAMP interactions, transactions, savepoints, relation/row locks
and BYTEA/short-INSERT pilots in `reference/` also run without Docker through
the quickcheck target:

```sh
make quickcheck PACKAGES=./reference # quick: real stuntgres + expected data
make quickcheck REFERENCE=1 PACKAGES=./reference # pinned PostgreSQL 18.0 + legacy/migrated
```

Query-read migration moves 67 original SQL-interaction tests into 80 untagged
definitions covering projection, NULL counts, aggregates, joins, ordering and
LIMIT. The DML migration carries all 61 assigned root tests into complete
reference assertion unions for INSERT SELECT, UPDATE and DELETE/RETURNING.
Identity migration likewise merges all 28 assigned root interaction tests with
existing reference assertions in untagged `TestReferenceIdentity*` definitions.
The arithmetic migration carries 61 original tests into 81 untagged definitions,
including every bitwise width/shift/signature vector and mathematical metadata,
parameter and lifecycle case. The scalar expression migration unions all 62
assigned root tests with existing reference assertions in 93 untagged definitions
covering BETWEEN, explicit casts, null/truth/distinct predicates, ordinary
comparisons, conditional expressions, expression core and logical operators.
The scalar-type migration merges all 63 assigned root SQL-interaction tests from
25 files with existing reference coverage into 89 untagged definitions in the
Character, Float, Name, Numeric, OID, Smallint, TypeCore and UUID families. They
preserve raw values and descriptors, complete diagnostics, affected counts,
ownership, actual held frames, concurrency, cancellation and retained native
controls. Adapter differences are independently asserted; the NAME
physical/binary complement remains a tagged PostgreSQL-only definition.
The temporal migration carries all 51 assigned root DATE, TIME and
timestamp-without-time-zone interaction tests into complete reference assertion
unions, including accepted/rejected input corpora, precision, held clocks, owned
raw rows and retained metadata. Native Dump/Reset/Restore phases use the common
scenario's public control pool; true mode also replays its unchanged SQL on
PostgreSQL.
The transaction/locking migration carries all 111 assigned original SQL tests
from 38 root files into 130 untagged external definitions, merging their complete
owning reference assertions, including the same-family row-lock ViewInsert case.
Native controls and raw Begin-context lifetime remain distinct capability phases;
actual PostgreSQL server cancellation and physical lock witnesses are independent
phases, not native queue or raw-context parity. Pure non-SQL terminal reasons and
raw reuse mechanics remain root units. This is test relocation/deduplication, not
new SQL support or a production API change.
Together these families retain raw and command/RETURNING metadata, genuinely
reused and concurrent held statements, atomicity, cancellation and native
controls. The BYTEA and short-INSERT pilots remain available alongside them.
Each selected backend must satisfy independently hardcoded expectations, with
explicit phases for backend-specific metadata, diagnostics, controls and lock
witnesses; parity alone is not proof. Startup failure never falls back to native
execution.
See the [development guide](docs/DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
for the migrated families, temporal adapter differences and complete selection
commands.

Change note: the final driver/session/catalog cutover relocates all372 remaining
root SQL scenarios into651 shared untagged reference definitions and44 separately
selected companions:39 PostgreSQL-only and five PostgreSQL-required differential
definitions. All131 obsolete helper dispositions are accounted for. Actual
original DB/Tx/Stmt and pinned-Conn producers, typed/metadata/raw consumers,
SAME held statements, native controls and failure-safe cleanup survive alongside
the recorded Context assertion union.
No root SQL helper/simulation remains;13 focused nonSQL units and the startup
benchmark remain. This changes test placement, not production SQL behavior.
See [whole-root cutover commands](docs/DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover).

The `reference` build tag still includes untouched legacy PostgreSQL tests and
retained PostgreSQL-only edges; it does not select runtime mode. A tagged
selection restricted to mode-aware migrated tests with `-reference=false`
remains Docker-free, but selecting a legacy test lazily starts the same pinned
container even when the flag is absent or false. Whole-root cutover does not
convert every PostgreSQL-only ACL, inheritance, keyed-row, role-lifetime or
rules/trigger edge to native coverage. `TestReferenceControlTransactionsDiagnostics`
is now an untagged dual-mode scenario, not a legacy Docker-only test.
`make quickcheck REFERENCE=1 PACKAGES=./reference` enables the `reference` build tag
and `-args -reference=true` only for `./reference`. After merge, `make fullcheck` runs
generation/build, quick native tests, race-enabled native and real pinned PostgreSQL
reference tests, bench and deps under one aggregate 1800-second default deadline.
This final-cutover task retains its user-authorized external900second bound:
`timeout --signal=KILL 900s make fullcheck VERBOSE=0`; it does not change the
incoming Makefile's1800second fullcheck or900second quickcheck defaults.


Start with the [dialect contract](docs/DIALECT.md) and
[development guide](docs/DEVELOPMENT.md). Root packages expose the driver and
native APIs; `internal/parser`, `internal/engine`, and `internal/sqltype` own
parsing, execution, and SQL value handling. Public scenarios live in `smoke/`;
PostgreSQL compatibility fixtures live in `reference/`.

For behavior changes, add a failing behavioral regression first and ground
compatibility expectations in PostgreSQL 18. Edit `internal/parser/grammar.y`,
not generated parser output, and run `make generate` after grammar changes.
Keep production dependencies limited to the standard library and this module.
Run the relevant public scenario and full verification before submitting changes.
The [development guide](docs/DEVELOPMENT.md) documents the pinned reference image,
additional targets, and verification conventions; [design notes](docs/design/)
provide architectural background.

## Support and issues

Report bugs and feature requests through this repository's issue tracker.
Include a minimal reproducer, Go and stuntgres versions, the SQL and parameters,
and the expected and actual results. For compatibility reports, include the
behavior observed on PostgreSQL 18 and any SQLSTATE diagnostic.
