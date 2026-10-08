# Table definitions and object comments

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## Object comments

COMMENT stores one immutable UTF8 text per TABLE, COLUMN, SCHEMA or ROLE.
Another command replaces it; NULL and the empty string remove it, while
whitespace remains text. It affects zero rows and returns no columns or
parameters. Names and literals use the ordinary positioned parser, including
quoted dots, E/dollar/Unicode strings and parse-time string settings. Values
cannot be expressions or parameters. COMMENT remains an unreserved identifier.

TABLE/COLUMN lookup uses current search_path and schema USAGE. Missing columns
are resolved before owner checks. Owners and effective superusers may comment
local objects; ordinary grants do not substitute for ownership. Nonowner TABLE
uses `must be owner of table t`, COLUMN `must be owner of relation t`, and
SCHEMA `must be owner of schema s` (42501). ROLE uses real shared role authority:
ordinary users cannot comment even themselves (CREATEROLE required); a superuser
target requires SUPERUSER. This native role surface has no CREATEROLE/ADMIN SQL.
Prepare caches syntax, never target identity or ownership; Execute resolves anew.
Qualified TABLE/COLUMN targets in a missing schema fail `3F000`,
`schema "s" does not exist`; a missing relation in an existing schema fails
`42P01`. Schema-USAGE denial precedes relation/column lookup and has no Position
field for COMMENT, unlike positioned DML analysis diagnostics.
Serialized COMMENT waiters refresh immutable metadata at the original captured
relation identity after lock grant; a prior comment does not count as replacement.
Reset/Restore and drop/recreate still fence the old epoch/address, never retargeting
a waiting command to an object with the same name.

CatalogRead.Comment exposes local metadata at stable class/object/SubID addresses;
RoleRead exposes shared text through RoleDescriptor.Comment. SQL inquiries use
the same immutable metadata; see [object comment inquiries](SQL.md#object-comment-inquiries--change-note).
They do not add queryable `pg_description` or `pg_shdescription` projections.
Legacy whole-object collision results remain consistent within each statement,
without promising a portable catalog ordering. Persisted inquiry views retain
typed syntax even when strict constant-NULL inputs eliminate execution demand.
Dropping/recreating an object clears text, while schema rename/owner transfer
preserves it. Plain LIKE does not copy comments. COMMENT ON TYPE, DATABASE and
other unsupported object kinds remain excluded. Table/schema COMMENT in an
explicit driver transaction returns native 0A000; supported sequence COMMENT
uses transaction-private metadata. PostgreSQL permits transactional COMMENT.

Dump captures local comments with the same immutable snapshot as rows: schemas
in name order, then table/column text after CREATE in declaration order. Escape
literals preserve apostrophes, backslashes, newlines and Unicode independently
of destination string settings. Fresh native `pg_catalog` and `public` carry
`system catalog schema` and `standard public schema`, respectively. Unchanged
bootstrap descriptions add no dump bytes; overrides emit text and explicit
removals emit `COMMENT ON SCHEMA ... IS NULL`. Reset restores this fresh baseline,
not overrides or removals. Restore replaces local state atomically and replays
those differences, including removals. Both retain role comments, which are
deliberately excluded from database Dump and Restore.


## Column defaults

CREATE TABLE stores structurally immutable, bound column `DEFAULT` definitions;
this does not require the expression's volatility to be Immutable. Supported
closed, parameter-free, row-independent Immutable and Stable trees are admitted,
including temporal casts, `AT TIME ZONE`/`AT LOCAL`, comparisons, CASE and NULLIF.
Session-dependent temporal operations remain Stable, not folded constants.
Existing literal/NULL, typed DATE/TIME/TIMESTAMP/TIMESTAMPTZ/TIMETZ, baseline and
character-family CAST/`::` casts, baseline function-style casts, INT4/INT8
unary/binary arithmetic, scalar comparisons and IS [NOT] DISTINCT FROM are admitted
at the outer level. Volatile producers and execution-stage-dependent nodes remain
unsupported defaults.
CREATE DEFAULT Boolean NOT/AND/OR and null/truth tests require parentheses, e.g.
`DEFAULT (NOT NULL)` or `DEFAULT (NULL IS NULL)`. Column references and positional
parameters are prohibited even inside casts or decisive logical branches.
General SQL functions, subqueries, arithmetic beyond existing INT4/INT8 operators,
new cast families and serial/identity remain outside this surface.
Restrictions apply as ordinary recursive binding reaches each occurrence; earlier
child Boolean, input and operator diagnostics retain their PostgreSQL precedence.
The existing null, truth and distinct predicates retain their ordinary typed
semantics and fully parenthesized unevaluated Dump representation in defaults.
Named DEFAULT syntax is accepted but does not create a named constraint object.
Plain LIKE copies shape and NOT NULL, **not defaults**.

`ALTER TABLE target ALTER [COLUMN] name SET DEFAULT expression` and `DROP DEFAULT`
change only the selected immutable default descriptor. Existing rows and column
nullability remain unchanged; future omitted/DEFAULT requests, including held
prepared INSERTs, use the current definition. Dropping an absent default succeeds
without a new descriptor revision. No explicit default means implicit typed NULL,
which still fails a retained NOT NULL constraint when consumed.
ALTER uses the general supported expression grammar: `TRUE OR FALSE`, `NOT FALSE`
and `NULL IS NULL` do not require extra parentheses. Assignment binding reuses
CREATE's type providers without evaluating a value: unknown invalid literal input
fails immediately; typed conversion, integer narrowing and division by zero can
fail only on subsequent demand. Failed SET preserves the old definition.
Semantic ALTER default diagnostics have no source Position; syntax errors retain
their character positions and CREATE diagnostics retain their existing positions.
Owner-only execution takes the existing AX grant and publishes one fenced
metadata-only catalog stage. Qualified/quoted targets, optional COLUMN, ONLY,
ONLY(...) and trailing `*` work for standalone tables. Native INHERITS is still
unsupported, so there are no supported descendants; PostgreSQL-only reference
tests specify recursive SET/DROP and ONLY behavior without claiming native parity.
Action lists and ALTER TABLE IF EXISTS for these actions remain unsupported.
Native explicit transactions exclude ALTER COLUMN SET/DROP DEFAULT;
read-only ALTER of these actions fails 25006.
Dump writes the current defaults in CREATE and old rows explicitly; Restore does
not accept arbitrary ALTER scripts. UPDATE DEFAULT remains outside this surface.
Feature smoke: `make smoke RUN='^TestAlterDefaultSmoke$' VERBOSE=0`.

Each INSERT row evaluates declared defaults for omitted columns or per-cell
`DEFAULT`; absent declarations yield typed NULL. Explicit supplied values,
including NULL, bypass defaults. Targetless `DEFAULT VALUES` inserts one all-default row;
an explicit target list before DEFAULT VALUES is a syntax error (42601).
Stable defaults use the admitted INSERT's captured TimeZone and DateStyle.
Changing settings between executions of the same prepared INSERT can therefore
change actual default values without changing the stored definition. CREATE and
Dump serialize/validate the expression tree without evaluating it; typed literal
leaves still freeze at CREATE analysis, while supported session-dependent parent
operations are evaluated only when consumed by INSERT.
Ordinary INSERT supplied values retain the existing scalar/cast surface;
SELECT and ORDER-decorated VALUES sources retain their existing query surface.
Defaults do not change declared result types, ScanTypes or nullable metadata.
CREATE resolves each column's type before validating its NULL/NOT NULL and
DEFAULT declarations in source order, before proceeding to later elements.
The second DEFAULT fails with 42601 at that token. Earlier type/nullability
errors still win; all declaration structure and NOT NULL merging complete before
any default expression is bound, so a later duplicate can precede an earlier
default-input error. Restore uses the same binder and preserves the existing
catalog and data on failure.
Utility Prepare remains semantic-analysis-free; CREATE binds input/type errors
on execution, while destination narrowing and typmod sizing remain deferred to
INSERT. For example `INT DEFAULT 2147483648` creates successfully but fails
on use with 22003. Every INSERT preflights actually consumed declared defaults'
immutable conversions and closed integer arithmetic at execution, after supplied-value
or query-source preflight and before executor authorization and capture. For query
INSERT, even an empty source can report demanded division-by-zero or overflow errors.
This bounded default-only planning also covers ordinary omissions, per-cell requests,
undecorated parenthesized VALUES and DEFAULT VALUES; it visits the left
Boolean operand first.
A resulting concrete TRUE on OR, or FALSE on AND, makes the whole execution
expression a private BOOL literal without demanding the discarded right branch.
Nested closed constant arithmetic/comparisons/predicates and NOT use their existing
typed operations after required children are handled. NULL/nondecisive left operands
still demand the right; a bad left operand is not rescued by a decisive right.
CREATE still analyzes both operands and rejects prohibited references or eager
input errors. Stored definitions remain unfolded, including discarded casts.
Explicitly supplied values, including NULL, bypass defaults. A default is
planned only when some ordinary row requests or omits that destination, or
a query omits it; a column supplied in every row is never default-planned.
Mixed rows retain one private schedule but evaluate remaining runtime expressions
only in cells consuming the default, never for a zero-row source.
This is not a general runtime short-circuit or broader optimizer guarantee;
ordinary query/DELETE/UPDATE/supplied-value preflight and logical evaluation are unchanged.
Relative temporal input
such as `'now'` captures CREATE's transaction input environment, not a volatile
per-row SQL function.

Every statement stages complete rows before publication. Later default evaluation,
conversion, NOT NULL or cancellation failure leaves existing rows unchanged.
Descriptor owner/comment/NOT NULL changes preserve definitions; dropping a column
removes only its stable attribute's default. Dump preserves canonical expression
structure and captured temporal leaves without evaluating defaults; stored data,
including explicit NULL, remains explicit. Restore uses the same public syntax
and binder, atomically replacing definitions/data or preserving the target on failure.
Existing integer arithmetic defaults retain operator structure, grouping and operand
widths through native controls; CREATE and Dump never evaluate them. Destination
BIGINT does not widen an INT4 operation to avoid overflow. Explicit character CAST
typmods retain Unicode truncation and BPCHAR-to-TEXT trimming; destination assignment
still reports overlength rather than silently truncating. UPDATE DEFAULT remains
outside this INSERT-only default consumer.
The rounded finite TIMESTAMP endpoint retains its type-owned `TIMESTAMP(0)`
encoding exactly once, including temporal leaves inside comparison defaults.
Explicit casts retain their actual coercion mode and canonical CAST structure.
Known TEXT children stay typed, so `CAST('oops'::text AS INT)` remains an
execution-demanded conversion through empty/populated controls rather than
becoming a CREATE-time unknown-input error. Recursive DATE leaves retain DATE
introducers, including BC comparisons whose civil ordering differs from TEXT.
Root BYTEA defaults reuse the existing canonical hex codec and return raw octets;
no typed BYTEA compound syntax or new cast family is added.

Feature smoke: `make smoke RUN='^TestDefaultSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses: `make reference RUN='^TestReferenceDefault' VERBOSE=1`.
Sequences/identity and volatile SQL functions are adjacent unsupported SQL
capabilities, separately labeled in reference coverage. PostgreSQL disk/WAL
durability, wire networking, native ABI extensions and external pg_dump processes
or custom archives are non-engine mechanisms, not successful no-op promises.

## Ordinary table definitions and removal

`CREATE TABLE z ()` is a real zero-column relation: direct/prepared `SELECT *`
returns no columns and no rows for the newly created table. `LIKE z` is legal.
There is no dummy column. Public `INSERT ... DEFAULT VALUES` inserts one row,
including into a zero-column table; `INSERT ... VALUES ()` is invalid syntax. Ordinary `oid` is a legal column;
`ctid`, `xmin`, `cmin`, `xmax`, `cmax` and `tableoid` conflict with system names.

Plain LIKE copies column names and supported concrete types in declaration
order, and named NOT NULL identities at the LIKE element's position. Copied
constraints are processed in source constraint-name order, as in PostgreSQL.
Multiple/interleaved LIKE clauses are allowed; duplicate expanded names fail `42701`. The destination
has fresh relation/attribute identities, current-user ownership, no copied rows
or ACL, and no persistent dependency on its source. LIKE requires relation-level
SELECT; column-only SELECT is insufficient.

Qualified LIKE sources use that schema's USAGE checks; unqualified sources follow
the effective search path independently of the destination. Nested CREATE TABLE
elements in CREATE SCHEMA support the same ordered elements and zero-column shape.
They bind against preceding staged tables as the enclosing schema owner, with that
schema prepended to a local path. The caller's identity and stored path do not
change; inaccessible or non-SELECT sources fail the entire schema construction.

For example:

```sql
CREATE TABLE source (i INTEGER NOT NULL, s TEXT, f BOOLEAN);
CREATE TABLE clone (lead BIGINT, LIKE source, tail TEXT);
DROP TABLE source RESTRICT; -- clone remains independent and writable
CREATE TABLE empty_shape ();
```

Valid `INCLUDING`/`EXCLUDING` option names (COMMENTS, COMPRESSION, CONSTRAINTS,
DEFAULTS, GENERATED, IDENTITY, INDEXES, STATISTICS, STORAGE, ALL) are parsed, but
new-table execution returns positioned `0A000`, `LIKE options are unsupported`.
Neither INCLUDING nor EXCLUDING is an inert successful option. PostgreSQL's
EXCLUDING CONSTRAINTS still copies NOT NULL; native support is plain LIKE only.
Unknown syntactically valid type names prepare successfully and fail execution
with `42704`, `type "name" does not exist`, at the type's character position.
Known single-name types outside INT2/INT4/INT8/NAME/TEXT/BPCHAR/VARCHAR/BOOL/FLOAT4/FLOAT8/DATE/TIME/TIMESTAMP/TIMETZ/TIMESTAMPTZ/NUMERIC/UUID/BYTEA fail
execution with `0A000`. Excluded multiword families (BIT VARYING and qualified
INTERVAL fields), unsupported qualified type names, arrays and modifiers other than FLOAT(p),
numeric precision/scale, character length, temporal precision and admitted BYTEA/NAME modifiers
remain parser-level `0A000` boundaries. DATE, BYTEA, TEXT and NAME modifier syntax is admitted only
to reject it semantically with `42601`, `type modifier is not allowed for type
"date"`, `"bytea"`, `"text"` or `"name"`, at the type token, before validating modifier operands.
CREATE Prepare is metadata-only and succeeds; direct or prepared execution rejects
the modifier without publishing a table. TIME/TIMESTAMP precision has its own bounded integer grammar.
Even an existing IF NOT EXISTS target cannot bypass excluded syntax forms;
failure-only guards construct no executable family AST. DOUBLE PRECISION and
valid FLOAT(p) are supported.

Type keyword aliases are not quoted catalog names: `"smallint"`, `"integer"`,
`"bigint"` and `"boolean"` fail with `42704`, while `"int2"`, `"int4"`, `"int8"`,
`"text"` and `"bool"` select the supported concrete types. Quoted case is significant (`"INT2"` or `"INT4"` is
missing), and diagnostic names retain decoded quotes and backslashes without
SQL or Go re-escaping. NAME and quoted `"name"` are supported distinct from TEXT;
quoted `"NAME"` is missing. Lowercase serial pseudo names, including quoted
`"serial"`, remain a `0A000` CREATE-utility boundary rather than nonexistent
types; uppercase quoted `"SERIAL"` is missing. No serial sequence or default
is implemented.

DROP preflights the entire explicit target group before one atomic catalog
publication. A later missing or unauthorized target leaves every table intact.
Duplicate resolved targets such as `a, public.a, a` remove one identity once.
IF EXISTS emits per-missing-target NOTICE/00000 (`table "name" does not exist,
skipping`, or `schema "name" does not exist, skipping`). DROP requires table
owner, schema owner or superuser authority, not ordinary table privileges, and
preserves current schema lookup/USAGE authorization.

The supported inbound table-definition dependency set is explicitly **empty**.
Default/explicit RESTRICT and CASCADE therefore remove the same explicit tables
and their contained columns, storage and relation/column ACL records. Namespace,
builtin types, owner roles and unrelated tables survive. Real DROP ROLE ownership
and ACL blockers disappear with the removed definitions. LIKE clones, prepared
statements and held cursors are not persistent dependents; held cursors retain old
rows/metadata, while prepared executions rebind (absent table `42P01`,
incompatible recreation `42703`). Foreign keys, views, indexes and sequences are
external-only dependency controls, not native graph compatibility or a fabricated
native `2BP01` table blocker. Explicit-transaction DDL remains unsupported.

Dump emits expanded canonical declarations, including `()`, independently of a
dropped LIKE source. Restore uses the same binder in isolated staging; failed
LIKE/type/duplicate/option declarations preserve the target. DROP remains
excluded from Restore scripts. Existing explicit-ACL Dump rejection is unchanged.
The independent reference recorder preserves genuine zero-column cursor
metadata as an empty result, distinct from no query result; consumed rows and
cursor closure are checked for both native and PostgreSQL executions.

## NOT NULL constraints

Both column `a INT [CONSTRAINT nn] NOT NULL` and table-form
`[CONSTRAINT nn] NOT NULL a` are supported (the table form has no parentheses).
Table constraints can precede their columns. `NULL`, including named NULL, is
nullable syntax, not a stored constraint. Repeated compatible declarations
merge into one identity; an explicit name replaces a pending generated name.
Conflicting NULL/NOT NULL declarations within one column definition fail
`42601` at the conflicting token. Column-local declarations are validated before
table constraints: differently named column NOT NULL duplicates reproduce the
pinned PostgreSQL 18.0 `XX000`, even when a table constraint precedes the column.
Table-level NOT NULL can enforce a column declared NULL in either element order;
only NOT NULL identities participate in that merge. Different names in that
merge use `42601`. Duplicate explicit names on different columns fail `42710`.
Named/unnamed CREATE table constraints targeting a system column, such as ctid,
fail `0A000`, `cannot add not-null constraint on system column "ctid"`, rather
than reporting a missing ordinary column.

An unnamed constraint gets a PostgreSQL-style UTF8-safe 63-byte
`table_column_not_null` name, with numeric suffixes avoiding every constraint
name in its namespace, including preceding nested CREATE SCHEMA tables and
already-selected names in the current command. Allocation follows the original
column/table/LIKE element order after merging later declarations for the same
column; future explicit names are not reserved. For example,
`CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)` fails
`23505` for `pg_constraint_conrelid_contypid_conname_index`, with Schema
`pg_catalog`, Table `pg_constraint`, and Constraint equal to that index name.
Detail is `Key (conrelid, contypid, conname)=(OID, 0, t_a_not_null) already exists.`,
where OID is the actual locally allocated candidate relation ID, not a fixed
cross-server value. No table is published. Reversing these column declarations
succeeds and generates `t_a_not_null1` for `a`.
Explicit names are relation-local and can be reused on another table. Names
stay attached to stable attribute identities through schema renames and dense
DROP COLUMN compaction. DROP COLUMN removes only its own NOT NULL identity.
LIKE copies the names onto fresh destination attributes; source ALTER/DROP
never weakens the clone.

Single-action SET/DROP NOT NULL and ADD/DROP CONSTRAINT use owner-authorized
ACCESS EXCLUSIVE execution; Prepare has no locks, validation scans, notices or
name allocation. SET/ADD validates visible committed rows first. Old NULL gives
`23502`, `column "a" of relation "t" contains null values`, with schema/table/
column fields and no failing-row Detail or Constraint field. INSERT NULL gives
`23502`, `null value in column "a" of relation "t" violates not-null constraint`,
with a failing-row Detail and the same object fields even for named constraints.
Multirow failure publishes no rows. Failed ALTER leaves descriptor, names,
nullability and data intact; successful ALTER changes metadata only, retaining
row storage and immutable snapshots.

Compatible repeated SET/ADD preserves the original name and is a count-zero
no-op. DROP NOT NULL is also a no-op when already nullable. DROP CONSTRAINT
removes that identity and makes its column nullable. Missing names give `42704`;
IF EXISTS emits `00000` NOTICE `constraint "name" of relation "t" does not exist,
skipping`, after authorization, without allocating a revision. Every selected
utility returns zero affected rows. Retained prepared SELECT and parameterized
INSERT handles use current nullability across SET/ADD/DROP without a
cached-result-type failure: bound nil fails while enforced and succeeds after
DROP. Native metadata reports known nullability and INT4's int64 ScanType;
the pinned pq adapter reports int32 ScanType but no Nullable capability.
Table/schema owner distinctions, namespace USAGE and final
context/session/epoch/revision/row-length fences still apply.

Dump emits canonical quoted `CONSTRAINT name NOT NULL` declarations, including
generated names, not ALTER history. Isolated Restore uses the common CREATE/
INSERT binder, preserving names/data/nullability/ownership/comments; failed
declarations or NULL insertion preserve the target. Explicit-ACL Dump rejection
and ordinary context/I/O errors are unchanged.

Inheritance has no native hierarchy/merge mechanism; NOT NULL's NOT VALID,
VALIDATE and NO INHERIT remain explicit boundaries, not successful no-ops.
Scalar UPDATE enforces NOT NULL and CHECK after destination coercion.
Native failure-only INHERITS recognition accepts one complete parent list after
either CREATE TABLE form, including qualified/quoted/multiple parents, and
rejects it at INHERITS. IF NOT EXISTS cannot bypass that boundary. Empty,
unclosed, malformed or repeated clauses and extra tails are `42601`; complete
input rejection prevents earlier statements in the same script taking effect.
Primary keys, domains and unrelated ALTER actions remain excluded.
Selected ALTER COLUMN SET/DROP NOT NULL, ADD NOT NULL, DROP CONSTRAINT and
DROP COLUMN compose atomic comma-separated lists in implicit and explicit
transactions. PostgreSQL pass order is DROP, original SET, transformed ADD;
validation follows all actions in final physical column order. Fresh grant-time
NULL scans include own writes/deletions even in RR; ordinary RR queries retain
their snapshot. Savepoints restore private descriptors, projected row shapes and
own DML together. Pure nullability changes never rewrite stored values.
PostgreSQL-only tests still witness inherited OR nullability, inherited-drop
rejection, NOT NULL NO INHERIT and deferred validation. CHECK unknown acceptance
and UPDATE enforcement are shared native/reference witnesses.

Public feature smokes:

```sh
make smoke RUN='^TestNotNullSmoke$' VERBOSE=1
make smoke RUN='^TestNotNullControlsSmoke$' VERBOSE=1
```

## Identity-preserving table and column renames

`ALTER TABLE [IF EXISTS] t RENAME [COLUMN] old TO new` and
`ALTER TABLE [IF EXISTS] t RENAME TO new` relabel existing objects without moving
or rewriting rows. Both table and column targets accept `t*`, `ONLY t` and
`ONLY(t)` for standalone tables; these markers do not add hierarchy semantics.
Destinations are one identifier, not schema-qualified. Collisions, including
renaming to the current name, fail atomically (`42701` column, `42P07` relation).
Missing columns fail
`42703`, `column "old" does not exist`; fixed system columns cannot be renamed.
New names resolve immediately and old names fail with undefined-name diagnostics.

Both forms require the table owner or effective superuser, not merely the schema
owner. Table rename additionally requires CREATE on its containing schema;
column rename does not. Execution acquires ACCESS EXCLUSIVE with the existing
lock timeout, cancellation, grant-time identity and publication fences. Prepare
does not resolve targets, authorize, lock, emit notices or mutate data. IF EXISTS
applies only to the relation: missing targets (including absent qualified schemas)
return count zero and one `00000` NOTICE naming the unqualified relation.
It does not suppress missing-column or collision errors.

Relation OID/namespace and sparse attribute/origin identities remain stable;
only the immutable definition revision and selected label/name change. Row
storage, NOT NULL constraint names and attribute links, ownership, ACLs and
table/column comments remain attached. Retained Rows preserve original labels
and values. Retained prepared SQL is reanalyzed, not textually rewritten:
renamed explicit columns fail `42703`, old table names fail `42P01` with the
original relation-name character Position (15 in `SELECT * FROM t`), and a
renamed output label in SELECT star fails `0A000`,
`cached plan must not change result type`. Unchanged projections and omitted-target
INSERT survive column relabeling; fresh prepares expose current result labels.

Dump serializes current qualified CREATE/INSERT/COMMENT names and unchanged
named NOT NULL constraints, never ALTER history. Reset/Restore round trips values,
metadata and comments through existing native controls; explicit ACL serialization
remains rejected rather than silently losing grants. These are process-local
controls, not pg_dump, physical storage durability or network protocol support.
Foreign keys, indexes, row types and inheritance have no native stored-definition
descriptors. CHECK and supported views have real descriptors/dependencies;
PostgreSQL-only witnesses cover unsupported FK/hierarchy/transactional DDL.
Native explicit-transaction table/column RENAME and RENAME CONSTRAINT remain unsupported.

```sh
make smoke RUN='^TestAlterRenameSmoke$' VERBOSE=1
```

## Single-action column removal

`ALTER TABLE t DROP [COLUMN] [IF EXISTS] name [RESTRICT | CASCADE]`
removes one live column and its stored cells atomically. Ordinary `t`, `t*`,
`ONLY t` and `ONLY(t)` operate on supported standalone tables. No action lists,
inheritance or partition definitions are supported.
The table's effective owner or superuser is required; unlike DROP TABLE, schema
ownership alone does not authorize ALTER. Execution takes ACCESS EXCLUSIVE and
waits for existing readers/writers; utility Prepare is metadata-only. Cancellation
removes a waiting request without changing the table. A committed preceding INSERT
is retained, and later INSERTs use the new dense visible shape.
Queued SELECT, INSERT and subsequent DROP bind the current definition for the
exact originally acquired relation reference after their lock grants. A definition
revision alone is not replacement; actual removal/drop-recreate or Reset/Restore
still fences the old identity, without substituting another table by name.

Surviving columns keep their catalog attribute/origin identities; visible result
slots compact without renumbering those identities. Retained Rows keep old values
and metadata.
Surviving column grants and role-dependency diagnostics resolve those stable
attribute identities back to live column names, not dense slot numbers.
New direct/prepared queries omit the dropped column. Previously
prepared explicit survivor queries rebind successfully; dropped-column queries
fail `42703`, and a changed-width prepared star fails the existing `0A000`,
`cached plan must not change result type` signature boundary. DROP COLUMN,
including mixed nullability lists, participates in the existing private catalog
overlay and combined row/catalog Commit. Savepoint and full rollback restore
the prior width, values and stable identities; no public prefix is installed.
Missing expression-column diagnostics interpolate decoded quoted identifiers
literally, including embedded quotes/backslashes, with character-based positions
when direct or retained/new prepared queries rebind after DROP.

The supported dependent set includes named NOT NULL constraints, defaults,
column ACL/comments, owner-local CHECKs and supported view dependencies.
DROP removes local CHECKs referencing the selected stable attribute under both
RESTRICT and CASCADE, while survivor expressions retain attributes and remap
dense evaluation slots. Independent columns/tables and plain LIKE clones survive.
Supported inbound view dependencies retain the existing RESTRICT/CASCADE policy.
Canonical Dump renders current names and survivor metadata; Reset/Restore
preserves definitions/data even though replay allocates fresh attribute IDs.
Indexes, FK, inheritance and partition dependencies remain unsupported.

Missing columns fail `42703`, `column "missing" of relation "t" does not exist`;
IF EXISTS emits a `00000` NOTICE ending `, skipping` with no descriptor or row
change, after ownership checks. Fixed system names ctid/xmin/cmin/xmax/cmax/tableoid
fail `0A000`, `cannot drop system column "ctid"`, even with IF EXISTS. Modern `oid`
is an ordinary user column. All failures leave the original shape and data intact.

Dump serializes the final live CREATE/INSERT shape, never ALTER history. Dropping
every column retains row multiplicity: CREATE TABLE `()` is followed by one
canonical `INSERT INTO ... DEFAULT VALUES;` per empty row. Restore and public
INSERT share this syntax and execution path; nonzero-column relations use their
declared defaults or typed NULL. `VALUES ()` remains a syntax error.
ALTER TABLE is not admitted to Restore scripts.
Explicit ACL Dump rejection remains unchanged.

Owner-bearing feature controls exercise an ordinary owner of both a nonpublic
schema and its table, direct/prepared DROP, canonical owner directives, restored
owner authority and replay of the same dump on PostgreSQL. A separate distinct
schema/table-owner Restore fixture checks preserved ownership and schema-owner
denial after compaction. An ordinary distinct table owner still needs namespace
USAGE for qualified public SQL: native GRANT is not added, and Dump does not
silently discard explicit ACLs. PostgreSQL-only fixture grants provide its real
destination authority for replaying the distinct-owner dump.

Executable public feature witnesses (Go test reports actual pass/failure):

```sh
go test -count=1 -v -run '^TestAlterDropColumnSmoke$' ./smoke
go test -count=1 -v -run '^TestAlterDropColumnZeroColumnControlsSmoke$' ./smoke
go test -tags reference -count=1 -v -run '^TestReferenceAlterDropColumn' ./reference
```

Duplicate-relation `42P07` errors preserve decoded names inside literal outer
quotes, including embedded quotes and backslashes, both for an existing target
and for a competing publication. Failed CREATE preserves the winning definition
and rows; utility preparation succeeds before the execution-time failure.

Executable feature proof (Go test reports actual assertions):

```sh
go test -count=1 -v -run '^TestTableCore' ./internal/parser ./internal/engine .
go test -count=1 -v -run '^TestCatalogDrop' ./internal/engine
go test -tags reference -count=1 -v -run '^TestReferenceTableCore' ./reference
go test -count=1 -v -run '^TestTableCoreSmoke$' ./smoke
```

## CHECK constraints — change note

```sql
CREATE TABLE checked (
  a INT DEFAULT 1 CONSTRAINT only_one CHECK (a=1) NO INHERIT,
  b INT,
  CONSTRAINT ordered CHECK (a<b)
);
```

Column/table CHECKs bind the complete new row, including later columns.
`a`, `checked.a` and `public.checked.a` resolve the same stable attribute.
Every supported scalar expression uses the existing binder and providers.
TRUE or NULL passes; FALSE rejects with `23514`, the table/constraint identity
and PostgreSQL English failing-row Detail. CHECK does not declare NOT NULL:
`CHECK (a IS NOT NULL)` still leaves Nullable metadata true.

NOT NULL runs first, then CHECKs in alphabetical constraint-name order.
All INSERT routes and UPDATE enforce after defaults/coercions and completed
simultaneous assignments, before publishing any row write. Failed multirow
statements retain original rows and consume the existing transaction abort/
savepoint policy. Supported volatile scalar effects are evaluated once when
reached and are not rewound by row-write rollback.
Comment inquiries (`col_description`, either `obj_description` signature and
`shobj_description`) share the immutable statement-owned catalog/role comment
snapshot on every INSERT route and UPDATE. A later statement sees committed comment
changes; capture is not repeated per row or when a query source opens. These
functions remain excluded from DEFAULT. Table/column COMMENT in explicit transactions
retains its existing unsupported boundary; PostgreSQL private-comment behavior is
independently covered as a release-only control.

Names use distinct referenced columns rather than syntactic placement:
one attribute yields `table_column_check`, otherwise `table_check`, with
namespace-aware suffixing and UTF8-safe63-byte clipping. Names remain unchanged
on rename. Named table clauses, actual scalar trees and NO INHERIT survive
native Dump/Restore; NO INHERIT enforces locally without adding inheritance.
DROP CONSTRAINT removes a CHECK. DROP COLUMN removes local dependent CHECKs
and remaps survivors. Plain LIKE copies shape/NOT NULL, not CHECK.
CHECK removal composes with the selected atomic ALTER action lists. Inside an
explicit transaction, successful removal and column compaction affect only the
private descriptor/row roots until Commit; Rollback and savepoints restore
CHECK enforcement together with the original layout and DML. Peer operations
retain the existing ACCESS EXCLUSIVE wait rules.

Persistent CHECK creation holds each referenced relation with ACCESS SHARE
through its single catalog publication; sequence DROP takes ACCESS EXCLUSIVE
and scans the grant-time dependent set. Current-root publication also fences
new external CHECK edges against provider removal, including lower-level catalog
stages. This is a native no-orphan safety boundary, not identical PostgreSQL
concurrency scheduling: pinned PostgreSQL 18.0 permits CREATE referencing a
sequence while another session's DROP remains private. Native CREATE instead
waits; rollback permits the original provider identity, while commit rejects
its disappearance without publishing a table fragment. Cancellation releases
the contender's holds through the existing transaction ticket.
Restrictive sequence diagnostics merge every supported CHECK and view edge by
relation creation address, with CHECK declaration ordinals within each owner.
They retain complete ordered Detail, including transitive view dependencies;
alphabetical row enforcement remains independent. Declaration ordinals are
nonnegative and unique per table; dropping constraints or columns may leave gaps.

Canonical Dump replays tables and views in common creation-address order among
objects whose sources are ready, preserving ordinary SQL-created multi-table and
mixed CHECK/view diagnostics across Restore. Table data and table/column comments
remain with their CREATE; owner directives switch as needed. The incumbent view
topology takes precedence when native replacement remaps a source address above
its dependent: replay remains executable, but that exceptional remapped catalog
does not promise the original allocator-relative diagnostic ordering. No separate
persistent ordering authority is maintained.

Sequence CREATE/options precede table definitions in Dump so CHECK bindings can
resolve real providers; ownership and captured setval follow table data.
Restore genuinely reevaluates volatile CHECKs while inserting staged rows.
History-dependent conditions may therefore reject Restore, preserving the
destination; no CHECK bypass or fake sequence identity is used.

Parameters/aggregate/outside-row references fail before table publication.
ctid/xmin/cmin/xmax/cmax are forbidden CHECK system references; PostgreSQL permits
tableoid but native system-column projection remains outside this surface.
Subqueries/windows/SRFs, other CHECK modifiers, inheritance, ALTER ADD CHECK
and VALIDATE are not supported. The shared row validator is available to later
existing-data validation work; this change does not implement that traversal.

Public feature smoke: `make smoke RUN='^TestCheckSmoke$' VERBOSE=0`.

