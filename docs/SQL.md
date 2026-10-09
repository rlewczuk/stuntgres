# SQL reference

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Detailed contracts are grouped by topic:

- [Queries and data manipulation](QUERIES.md): INSERT, DELETE, UPDATE, SELECT, expressions and casts.
- [Table definitions and comments](TABLES.md): defaults, constraints, renames and column removal.
  CHECK supports complete-row TRUE/NULL acceptance, false-only INSERT/UPDATE
  rejection, TYPE dependency reparse/final-row validation, named identity, local
  NO INHERIT and real native-control persistence. ALTER CHECK violations have
  a contains-row diagnostic without DML's failing-row Detail.
- [SQL types and Go values](TYPES.md): codecs, metadata and adapter differences.
- [Identifiers and literals](LEXICAL.md): names, strings and numeric syntax.
- [Roles, authorization and schemas](AUTHORIZATION.md): ownership, schema lookup and search path.
- [Transactions and locks](TRANSACTIONS.md) and [session settings](SETTINGS.md).

The grammar summary below is an overview; the topic guides and [dialect limits](DIALECT.md) describe feature-specific support and exclusions.

Explicit table locking: `LOCK [TABLE] target [, target ...]
[IN lockmode MODE] [NOWAIT]`, with target `name`, `name *`, `ONLY name` or
`ONLY(name)`. Modes are ACCESS SHARE, ROW SHARE, ROW EXCLUSIVE, SHARE UPDATE
EXCLUSIVE, SHARE, SHARE ROW EXCLUSIVE, EXCLUSIVE and ACCESS EXCLUSIVE (default).
Execution requires an explicit transaction, affects zero rows and processes
targets in order. Views recursively lock bound sources in the same mode;
standalone table markers do not add unsupported inheritance. Prepare is
metadata-only. See [privileges, conflict matrix and savepoint lifetime](TRANSACTIONS.md#relation-locks).

```text
CREATE TABLE [IF NOT EXISTS] [schema.]name ([element [, ...]])
CREATE VIEW [schema.]name [(column [, ...])]
  [WITH (security_barrier [= boolean] [, security_invoker [= boolean]])] AS SELECT ...
CREATE INDEX [name] ON [schema.]table [USING btree]
  (column [ASC|DESC] [NULLS FIRST|LAST] [, ...])
CREATE INDEX IF NOT EXISTS name ON [schema.]table [USING btree]
  (column [ASC|DESC] [NULLS FIRST|LAST] [, ...])
element := column type [COLLATE [schema.]name] [[CONSTRAINT name] NULL | [CONSTRAINT name] NOT NULL | [CONSTRAINT name] DEFAULT default_expression | check_constraint]...
         | [CONSTRAINT name] NOT NULL column | check_constraint | LIKE [schema.]source
check_constraint := [CONSTRAINT name] CHECK (scalar_expression) [NO INHERIT]
DROP TABLE [IF EXISTS] [schema.]name [, ...] [RESTRICT | CASCADE]
COMMENT ON TABLE [schema.]name IS string_literal | NULL
COMMENT ON COLUMN [schema.]table.column IS string_literal | NULL
COMMENT ON SCHEMA name IS string_literal | NULL
COMMENT ON ROLE name IS string_literal | NULL
ALTER TABLE [ONLY] [schema.]name DROP [COLUMN] [IF EXISTS] column [RESTRICT | CASCADE]
ALTER TABLE target ALTER [COLUMN] column SET NOT NULL
ALTER TABLE target ALTER [COLUMN] column DROP NOT NULL
ALTER TABLE target ADD [CONSTRAINT name] NOT NULL column
ALTER TABLE target DROP CONSTRAINT [IF EXISTS] name [RESTRICT | CASCADE]
ALTER TABLE target ALTER [COLUMN] column [SET DATA] TYPE type [COLLATE [schema.]name] [USING expression]
ALTER TABLE target ALTER [COLUMN] column SET DEFAULT default_expression
ALTER TABLE target ALTER [COLUMN] column DROP DEFAULT
ALTER TABLE [IF EXISTS] target SET SCHEMA schema
target := [schema.]name [*] | ONLY [schema.]name | ONLY ([schema.]name)
INSERT INTO [schema.]name [(column [, ...])] VALUES (insert_value [, ...]) [, ...]
INSERT INTO [schema.]name [(column [, ...])] SELECT [projection_target [, ...]]
       [FROM from_list [WHERE logical_predicate] [GROUP BY logical_target [, ...]]
        [HAVING logical_predicate] [ORDER BY ordering_key [, ...]]] [query_bounds]
INSERT INTO [schema.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
       [ORDER BY ordering_key [, ...]] [query_bounds]
INSERT INTO [schema.]name DEFAULT VALUES
insert_value := value | DEFAULT
DELETE FROM [schema.]name [[AS] alias] [WHERE delete_predicate]
       [RETURNING [WITH (returning_alias [, ...])] projection_target [, ...]]
returning_alias := (OLD | NEW) AS name
SELECT [projection_target [, ...]] FROM from_list
       [WHERE logical_predicate] [GROUP BY logical_target [, ...]]
       [HAVING logical_predicate] [ORDER BY ordering_key [, ...]]
       [query_bounds] [FOR KEY SHARE | FOR SHARE | FOR NO KEY UPDATE | FOR UPDATE]
query_bounds := [OFFSET scalar_expression [ROW | ROWS]]
                [LIMIT scalar_expression | LIMIT ALL
                 | FETCH (FIRST | NEXT) [count] (ROW | ROWS) ONLY]
                -- OFFSET and LIMIT/FETCH may reverse; one locking clause may precede bounds.
ordering_key := (scalar_expression | output_label | output_ordinal)
                [ASC | DESC] [NULLS FIRST | NULLS LAST]
from_list := from_item [, from_item ...]
from_item := [schema.]name [[AS] alias [(column [, ...])]]
           | (from_list) [[AS] alias [(column [, ...])]]
           | from_item CROSS JOIN from_item
           | from_item join_kind JOIN from_item ON logical_predicate
           | from_item join_kind JOIN from_item USING (column [, ...]) [AS merged_alias]
           | from_item NATURAL join_kind JOIN from_item
join_kind := [INNER] | LEFT [OUTER] | RIGHT [OUTER] | FULL [OUTER]
SELECT selected_call [, selected_call ...]
type  := SMALLINT | INT2 | INTEGER | INT | INT4 | BIGINT | INT8 | TEXT | BOOLEAN | BOOL
       | REAL | FLOAT4 | DOUBLE PRECISION | FLOAT8 | FLOAT[(p)]
       | DATE | (TIME | TIMESTAMP) [(precision)] [WITH | WITHOUT TIME ZONE]
       | (TIMETZ | TIMESTAMPTZ) [(precision)] | UUID | BYTEA
       | (CHAR | CHARACTER | BPCHAR | VARCHAR | CHARACTER VARYING) [(length)]
value := signed-64-bit integer | 'string' | E'escape string' | U&'Unicode string' [UESCAPE 'escape'] | $$string$$ | $tag$string$tag$ | TRUE | FALSE | NULL | $n | DATE 'input' | (TIME | TIMESTAMP) [(precision)] [WITH | WITHOUT TIME ZONE] 'input' | (TIMETZ | TIMESTAMPTZ) [(precision)] 'input'
scalar := query_column | value | (scalar) | CAST(scalar AS cast_type) | scalar::cast_type
        | (int4 | int8 | text | bool)(scalar)
        | scalar AT TIME ZONE scalar | scalar AT LOCAL
        | CASE [logical_target] WHEN logical_target THEN logical_target
          [WHEN logical_target THEN logical_target ...] [ELSE logical_target] END
        | (COALESCE | GREATEST | LEAST)(logical_target [, ...])
        | NULLIF(logical_target, logical_target)
        | scalar || scalar | string_call
string_call := (btrim | ltrim | rtrim | is_normalized)(scalar [, scalar])
             | [schema.] (lower | upper | casefold | initcap)(scalar)
             | normalize(scalar [, normalization_form])
             | [schema.] "normalize"(scalar [, scalar])
             | TRIM([BOTH | LEADING | TRAILING] [scalar] FROM scalar)
             | TRIM([BOTH | LEADING | TRAILING] scalar [, scalar])
normalization_form := NFC | NFD | NFKC | NFKD
cast_type := INTEGER | INT | INT4 | BIGINT | INT8 | BOOLEAN | BOOL | TEXT
           | (CHAR | CHARACTER | BPCHAR | VARCHAR | CHARACTER VARYING) [(length)]
comparison_operator := = | <> | != | < | <= | > | >=
comparison := scalar comparison_operator scalar
            | between_predicate comparison_operator between_expression
            | scalar comparison_operator between_predicate
between_target := scalar | (logical_target)
between_predicate := between_target [NOT] BETWEEN [SYMMETRIC] lower_endpoint AND between_target
between_expression := between_target | between_predicate
lower_endpoint := between_target | scalar comparison_operator scalar
                | lower_endpoint IS [NOT] DISTINCT FROM lower_endpoint
logical_target := between_expression | comparison | NOT logical_target
                | logical_target AND logical_target | logical_target OR logical_target
                | (logical_target)
                | scalar IS [NOT] [normalization_form] NORMALIZED
logical_predicate := logical_target
delete_predicate := logical_predicate with optional target/alias qualification on columns
query_column := column | qualifier.column | schema.table.column
projection_target := (logical_target | selected_call) [[AS] label]
                   | * | qualifier.* | schema.table.*
call_argument := scalar | selected_call
selected_call := null_count_call | random_call
null_count_call := (num_nulls | num_nonnulls)(call_argument [, ...])
random_call := random() | random(call_argument,call_argument)
             | random_normal([call_argument [,call_argument]]) | setseed(call_argument)
default_expression := value | scalar comparison_operator scalar | (logical_target)
                    | default_expression IS [NOT] DISTINCT FROM default_expression
```

SET/DROP DEFAULT are standalone utilities: no comma action lists, TYPE/DEFAULT
mixtures or explicit transactions. Selected TYPE/nullability/DROP COLUMN lists
are supported in explicit transactions; their pass order is DROP, TYPE, SET NOT
NULL, ADD NOT NULL. DEFAULT is not an action in those passes.

### Table namespace moves

`ALTER TABLE [IF EXISTS] target SET SCHEMA schema` is a standalone relation
utility for ordinary tables and views: the destination is one identifier and
comma action lists are syntax errors before any effects. Target markers do not
add hierarchy support. Execution requires source schema USAGE, relation ownership
and destination CREATE, even for a same-schema no-op; destination USAGE and source
CREATE are not required.
Missing conditional targets skip before resolving the destination, with one notice.
Prepare is effect-free and does not resolve, authorize or lock.

The table and all column-owned sequences move in one catalog publication under
ACCESS EXCLUSIVE locks. Unowned sequences and referencing views stay in place.
For a view, the same spelling aliases ALTER VIEW SET SCHEMA and moves only that
view's namespace; its sources and dependents retain their namespaces.
Rows, identities, NOT NULL names, defaults, comments, ACLs and sequence generators
and physical-session caches survive. Held SQL rebinds original names; old qualified
names fail after a move, while compatible unqualified plans can find the new schema
through search_path. Stored views retain dependencies and deparse current names.
Dump/Restore preserves current persistent definitions/data rather than ALTER history.
Explicit transactions exclude this standalone SET SCHEMA utility, not the supported
TYPE/nullability/DROP COLUMN lists. Generic moves of sequences or indexes,
temporary/system destinations and automatic row types are not added. Simple
B-tree indexes move with their table.
See the [dialect boundaries](DIALECT.md) for independently tested PostgreSQL-only
controls. Smoke: `make smoke RUN='^TestAlterSchemaSmoke$' VERBOSE=0`.

The selected-call-only no-FROM form produces one singleton row; its arguments
are literals, parameters, nested selected calls and previously admitted casts/
conditionals, without columns or extra clauses. Random calls also use existing
table-backed SELECT and INSERT SELECT projection paths. Bounded random resolves
INTEGER, BIGINT or NUMERIC, not floating-point bounds; normal and seed arguments
coerce implicitly to DOUBLE PRECISION. Numeric literals and typed table columns,
plus target-context bindings, supply these types without expanding the existing
typed-literal or general explicit-cast boundaries.
Random calls are excluded from DEFAULT, INSERT VALUES, WHERE and settings.
See [random behavior and diagnostics](DIALECT.md#random-functions--change-note).

Selected string expressions also form a bounded singleton exception, with
existing scalar children and nested selected calls. This does not admit ordinary
`SELECT 1`, arbitrary mathematical expression singletons or new function families.
Unquoted `normalize` uses a form keyword; qualified or quoted ordinary calls
accept a TEXT form value. SQL TRIM lowers to btrim/ltrim/rtrim, not a `trim()`
function alias. See [core strings](DIALECT.md#core-string-functions--change-note).
The four unary case calls return TEXT with inherited collation. Scalar/column
COLLATE supports default/C/POSIX, pg_c_utf8 and pg_unicode_fast; see the core
string rules for simple/full Unicode16 mapping, PostgreSQL18.0's original-input
sigma-prefix boundary and the Unicode ILIKE boundary. Stored expression/CHECK
deparsing retains TEXT conversion and required identifier quoting; its returned
compact/pretty SQL can be executed.


The FROM productions above are precedence notation: explicit joins bind tighter
than comma lists and associate left; parentheses preserve boundaries. ON sees
only its operands. Aliases hide original qualifiers; a whole-join alias hides
operand qualifiers. A special USING AS alias exposes only merged keys without
hiding operands and admits no column-alias list. Query qualification applies to
SELECT/INSERT SELECT and UPDATE FROM consumers. DELETE and UPDATE RETURNING
retain target/OLD/NEW namespaces; UPDATE additionally exposes its FROM sources.
UPDATE SET RHS/WHERE use query qualification; DELETE WHERE's guard is unchanged.
Raw guards remain at DEFAULT, VALUES and settings-call scalar entry points. Previously
admitted conditional arguments
retain their consumer diagnostics, including metadata-only utility Prepare
followed by DEFAULT validation at execution. ORDER keys use the shared
expression/output-reference syntax above; qualified and compound expressions
resolve through the input namespace, while bare output labels take priority.
No-FROM SELECT/INSERT SELECT retain their prior projection qualification
capability guard (`0A000` at a raw dot), rather than gaining a FROM namespace.
Grouped scalar qualifiers retain their syntax boundary; existing selected
singleton calls and zero-width INSERT SELECT admission are unchanged.
USING emits list-order keys, then remaining left/right columns; NATURAL derives
left-order common names. With no common name the condition is TRUE: inner joins
are equivalent to CROSS JOIN, outer joins retain preservation. Results preserve
all duplicate occurrences, extend missing operands with typed NULLs before WHERE,
and promise no unordered traversal sequence.
See [the join examples and ownership contract](QUERIES.md#inner-and-cross-joins--change-note).

Conditional expressions compose with the existing scalar consumers, including
INSERT sources, mutation predicates/RHS, DELETE and UPDATE RETURNING and stored
DEFAULTs. Their common types,
NULLIF equality signatures, result metadata and analysis-versus-execution demand
are described in [Conditional expressions](QUERIES.md#conditional-expressions).
BETWEEN includes endpoints; SYMMETRIC handles either bound order. Lower
endpoints exclude unparenthesized logical syntax; parentheses admit nested
predicates/Boolean expressions. Cast/arithmetic bind above BETWEEN; a single
ordinary comparison binds below BETWEEN, then IS/outer NOT/AND/OR. ASYMMETRIC and ungrouped
BETWEEN chaining remain excluded. Typing and NULL behavior are composed from
independent existing binary comparisons, not a common triple type.
Grouped/nested BETWEEN defaults round-trip through native Dump/Restore using
the existing scalar CASE syntax to encode predicate-valued comparison operands.
Serialization does not evaluate defaults or widen ordinary grouped-comparison
admission; restored defaults retain typed NULL and deferred errors.


## Views

`CREATE [OR REPLACE] VIEW` stores an immutable, identity-bound definition analyzed by the
ordinary SELECT binder. The body admits the current scalar/filter/group/HAVING/
expression and multi-key ordering repertoire and table, sequence, view and
bounded catalog sources. It is not executed by CREATE or Prepare; subsequent reads evaluate it
against current visible data. Explicit names rename an output prefix; fewer
names than outputs are legal, more fail42601, and duplicate final names fail42701.
Unknown-only outputs become TEXT. Creation-time `*` expansion and output types
are fixed; zero-column views retain real source occurrences and count semantics.
CREATE has affected count zero and no result columns or runtime parameters.

### Replacement and alteration — change note

`CREATE OR REPLACE VIEW` creates an absent target or replaces an existing view
atomically. The view and `_RETURN` rule keep their OIDs, owner, ACLs, comments,
existing attribute identities and inbound dependents. The new definition replaces
all outgoing dependencies; a former source is no longer retained merely because
an older definition used it. Existing outputs must be a positional prefix with
the same names, type identities, typmods and collation identities. Trailing
columns may be appended; dropping, renaming, reordering or changing the type or
collation of existing columns fails42P16 without publication. Scalar COLLATE
annotations support only the existing `pg_catalog.default`, `"C"` and `"POSIX"`
identities in projections, predicates and ordering; they do not convert values or
introduce new comparison behavior. Existing dependent views retain their frozen
attribute layout while reading the current replacement definition, even when its
row has appended columns.
Visibility inquiry demand follows the current definitions throughout the view
source graph. Adding, removing or restoring an inquiry in an inner replacement
does not require rebinding its dependent views; all nested inquiries share the
statement's original-invoker catalog and search-path snapshot.

Replacement selects its target only in the creation namespace, never a
same-named object later in search_path. An absent target there is created even
when a later schema contains another kind or owner. Qualified replacement
requires destination CREATE and existing-view ownership, not target-schema
USAGE; body sources retain their normal independent lookup requirements.

`ALTER VIEW [IF EXISTS] name` supports `RENAME TO new_name`,
`RENAME [COLUMN] old_column TO new_column`, `SET SCHEMA schema`,
`OWNER TO {role | CURRENT_ROLE | CURRENT_USER | SESSION_USER}`,
`SET (security_barrier=boolean, security_invoker=boolean)` and
`RESET (option [, ...])`. Names, attributes, rules and dependents retain identity.
Existing ALTER TABLE column/relation rename and SET SCHEMA spellings also work
for views. A view schema move changes only the view namespace, not its sources
or dependents. ALTER SET merges supplied flags; RESET removes named flags and
ignores unknown/duplicate names, but rejects values. OR REPLACE replaces the
entire option set, so omitting WITH clears both flags.

`DROP VIEW [IF EXISTS] name [, ...] [RESTRICT | CASCADE]` preflights the whole
group and publishes all removals together. RESTRICT is the default; dependencies
between explicitly listed targets do not block their grouped removal. CASCADE
removes dependent views and their rules, never their underlying sources.
IF EXISTS emits NOTICE for absent targets, but wrong object kinds still fail42809.
The removal hint names the actual kind, including `Use DROP INDEX to remove an
index.` for an index; a grouped wrong-kind failure removes no targets.
Owner and namespace authority, cancellation and lock waits use ordinary catalog
publication fences. View DDL affects zero rows and returns no result columns.

Cyclic replacement definitions may be published. Reads, including prepared and
row-marked reads, fail42P17 `infinite recursion detected in rules for relation "v"`
when expansion revisits the active view. Shared acyclic occurrences remain legal;
repairing the definition restores reads. Explicit view locking and cyclic
CASCADE/grouped drops terminate without recursively expanding rows.
Native Dump rejects self-referential and mutually recursive view graphs with
`stuntgres: cyclic view dump dependencies` before emitting any dump bytes.

Protocol-prepared DDL uses current authority and state at Execute; utility
Prepare has no publication effects. Prepared reads rebind their original SQL.
A same-shape replacement is visible on the next execution; appended outputs
make a held SELECT * fail0A000 `cached plan must not change result type`.
Already-returned eager rows retain their prior values and metadata.
SQL PREPARE utility DDL is not added. CREATE/replace/ALTER/DROP VIEW retain the
native explicit-transaction exclusion and read-only utility tags.


A legal scalar definition may store one trailing FOR KEY SHARE/FOR SHARE/
FOR NO KEY UPDATE/FOR UPDATE clause. CREATE remains row-lock-free; the immutable
mark survives identity remapping, canonical deparse (after ORDER BY), Dump and
Restore. Ordinary reads enforce stored marks on their physical base subtree.
An outer clause combines using the strongest SQL mode; unrelated sources are
not marked by a definition. Ordinary aggregation over a marked view is legal
and locks qualifying input before grouping/HAVING, even if output is empty.
A clause reaching a grouped/aggregate view is instead mode-specific0A000.
An enclosing clause reaching a nullable outer-join input rejects with
mode-specific0A000 on Execute after successful Prepare. Unmarked outer queries
may consume locally marked stored inputs on either side: only demanded real
source rows can lock; absence is explicit and never a physical RowID.
Native preserves left-major demand rather than PostgreSQL's physical hash-build
frontier, including independently tested extra scanned locks on empty inputs.
FULL join capability likewise checks on Execute, before any producer opens,
including LIMIT0/empty inputs; a simplified Boolean constant or supported
opposite-input equality conjunct is required.
Native retains its eager input frontier even for constant HAVING FALSE or an
upper WHERE FALSE. PostgreSQL can gate those constants before consuming the
marked child and therefore acquire no input grants. Runtime aggregate-dependent
HAVING count(*)=0 and count-output predicates separately exercise empty-output
retention after genuine input consumption.

Explicit SELECT target `AS label` and eligible bare labels preserve view output
names, including after base-column renames; CREATE VIEW's explicit column list
then overrides that output prefix. FROM aliases, alias column lists, qualified
columns and mixed stars use the ordinary joined SELECT namespace. ORDER resolves
output labels and ordinals; WHERE/HAVING do not expose output labels, and GROUP
BY retains its input-name priority and existing ordinal rules.

LIMIT/OFFSET/FETCH bounds use the typed shared producer, not a view-specific
executor. A view-local count fixes its input population and effects before an
outer predicate runs. A positive count settles pending marks before consumed
skipped/returned rows; an outer LIMIT0 does not produce an inner stage. Source
ACL and bound type errors remain mandatory for0/past-end results. Scalar
qualification does not become predicate pushdown across security barriers.
Canonical definitions retain bounds and their routine/sequence dependencies
through rename, deparse and native Dump/Restore. See
[count and demand](QUERIES.md#limit-offset-and-fetch--change-note-changelog).


Reads and INSERT SELECT share normal relation admission, dynamic ACL checks,
locking and eager query finalization. The caller needs SELECT on the named view.
Both consumers use the same marked projection/arbitration frontier, including
stored marks and joined/nested provenance. INSERT assembles destination rows
directly from that indexed stage only after source lock/recheck; any source
failure publishes no destination rows.
Ordinary definitions check underlying relation reads as the current view owner;
`security_invoker=true` uses the original statement invoker, including beneath
an ordinary outer view. CREATE needs namespace CREATE/lookup USAGE, not base
SELECT or UPDATE. Marked descendants additionally need base UPDATE on any live
column under these same principals, checked at Execute, not Describe/Prepare.
These principals change relation authority only, not session identity or
sequence/function execution authority. Each view boundary finishes its inner
qualification before outer expressions: no predicate pushdown, even without
security_barrier. Rejected rows never reach outer error-producing casts.
Scalar pending marks flow through enclosing qualification, but settle at a
consuming Sort, aggregate, local count or outer publication frontier. Security-
barrier expression order is retained without predicate pushdown. A stored-local
mark settles before an outer Sort on both engines: changed immutable values
supply its new outer rank, and outer volatile keys evaluate after lower recheck.
This supersedes the previous native local-stored delayed-Sort qualification.
Inherited-only marks over ordinary nested ordered views retain their separate
native/PostgreSQL rank qualification. Direct same-query ORDER followed by FOR
still retains its pre-wait rank and refreshes referenced volatile cache on replacement.

Dependencies include hidden filter/order/group attributes. Base and column
renames retain identity, and reuse of an old source name cannot retarget a view.
DROP RESTRICT reports2BP01 with dependency Detail/Hint; existing CASCADE paths
remove dependent definitions and their derived rule/catalog facts together.
Dropping an unrelated column preserves the frozen definition layout.
Dependency diagnostics use current identity and search_path visibility, including
shadowing and identifier quoting, for both targets and dependent views. A moved
view outside the effective path is schema-qualified; a visible identity is not.

Queryable catalog sources have this deliberately bounded exact schema:

| Source | Columns and PostgreSQL logical types |
|---|---|
| `pg_catalog.pg_class` | `oid OID`, `relname NAME`, `relnamespace OID`, `relowner OID`, `relkind CHAR`, `relnatts INT2`, `relhasrules BOOL` |
| `pg_catalog.pg_rewrite` | `oid OID`, `rulename NAME`, `ev_class OID`, `ev_type CHAR`, `ev_enabled CHAR`, `is_instead BOOL` |
| `pg_catalog.pg_views` | `schemaname NAME`, `viewname NAME`, `viewowner NAME`, `definition TEXT` |

Each user view has one `_RETURN` rule (`ev_type='1'`, `ev_enabled='O'`,
`is_instead=true`) tied to its real relation OID. OIDs are engine-local, not
numeric replicas of an independent PostgreSQL cluster. `pg_views.definition`
and Dump share identity-aware reconstruction after renames; PostgreSQL pretty
printing byte equality is not promised. Unsupported columns, including
`reloptions`, `ev_action` and `ev_qual`, give ordinary42703; no fake pg_node_tree.
Built-in source output is derived, never persisted as table data.

These native catalog rows are derived and have no durable physical row identity.
Direct, nested, stored or empty marked reads fail0A000 `row locking is not
supported for virtual catalog relations`, without Position/Detail/Hint or partial
physical grants. Ordinary catalog reads remain supported. PostgreSQL physical
catalog row locking is a separate release-pinned control, not native parity.

Joined definitions expand every star at creation. Reconstruction uses current
relation identities with full positional alias lists mapping surviving attribute
IDs to frozen namespace labels; unrelated compaction cannot retarget a prefix
alias, and added attributes get noncolliding labels. Table/self-join and whole-join
aliases retain their visibility boundaries. Explicit USING remains USING, and
NATURAL becomes its creation-time explicit USING key list (or CROSS JOIN for
empty inner keys, the same outer kind ON TRUE for empty outer keys), so current
names cannot broaden the join. LEFT/RIGHT/FULL kinds, merged values and implicit
common-type conversions are recreated by the ordinary USING binder, not explicit
casts. Stored ORDER preserves frozen input-slot identity through qualified
physical or merged namespaces, while output references are frozen separately
as ordinals. Each key retains its direction and NULL policy. Render-private
USING aliases preserve synthetic merged usage without substituting a physical
operand or allowing a later output-name override to retarget the key.

Views reuse the supported OID scalar and BETWEEN lowering, including nested
predicate-valued endpoints. Canonical replay preserves grouped predicate
comparison operands directly through the common nonassociative comparison
grammar; it never evaluates definitions during serialization.

Stored literal occurrences retain their analyzed value and type usage, including
temporal precision, independently typed BETWEEN occurrences and each JOIN ON clause.
Definition reconstruction and native replay use type-owned canonical literals,
not creation-time spellings such as `today`. Temporal input and supported
DATE/TIME/TIMESTAMP/TIMETZ/TIMESTAMPTZ casts use the captured session environment;
native DateStyle and TimeZone settings are supported. Canonical replay remains
independent of presentation settings.

CREATE RECURSIVE/TEMP syntax, view writes/check options, ALTER VIEW column
defaults, type-changing ALTER actions, subqueries, public GRANT, RLS, UDFs and
optimizer introspection remain unsupported. Setting check_option fails0A000
`view check options are unsupported`; RESET of an absent check_option is harmless.
Native ACL unit controls and PostgreSQL-only GRANT witnesses do not imply a
public native ACL API.
Smokes: `make smoke RUN='^TestViewSmoke$' VERBOSE=0` and
`make smoke RUN='^TestViewReplaceSmoke$' VERBOSE=0`;
independent oracle: `make reference RUN='^TestReferenceView' VERBOSE=0`.

## Object comment inquiries — change note

The ordinary typed SELECT pipeline now supports these PostgreSQL 18 signatures:

| Function | Result |
|---|---|
| `col_description(oid, integer)` | TEXT |
| `obj_description(oid, name)` | TEXT |
| `obj_description(oid)` (legacy) | TEXT |
| `shobj_description(oid, name)` | TEXT |

All four are STRICT: NULL input gives typed TEXT NULL after argument analysis.
Absent addresses or descriptions also give NULL, not missing-object errors.
Matching fixed signatures accept VARIADIC decoration on the last argument without
array expansion. Arity and ordinary argument type/NULL analysis still apply;
wrong-arity calls fail with the normal function-not-found SQLSTATE42883.
Calls above PostgreSQL's 100-argument limit fail with SQLSTATE54023 after child
analysis; child errors retain precedence. Direct analysis and Prepare expose the
same positioned diagnostic.
Column numbers are stable attribute identities, not dense output slots: dropping
column2 does not renumber surviving column3. Column0 reads the whole-relation
comment; negative, dropped and absent attributes normally have no description.
Catalog arguments are exact NAME values, not identifiers or search_path lookups.
`pg_class`, `pg_namespace` and `pg_type` address represented local metadata;
`pg_authid` addresses shared role metadata only. Case changes, qualification,
quotes or whitespace in those catalog strings do not match. NAME input retains
case and clips at a63-byte UTF8 boundary.

The one-argument legacy form returns one matching local whole-object description.
If numeric OIDs collide across catalog classes, the winning candidate is
unspecified but equal inputs return the same candidate throughout a statement.
There is no portable catalog ordering or guaranteed winner across executions;
it is not an ambiguity error or a shared/column comment. Prefer the two-argument
form. Numeric inquiry requires neither ownership nor schema USAGE;
symbolic REGCLASS conversion and actual source-table reads retain their ordinary
resolution and privilege checks.

Each execution retains one coherent immutable local/shared comment snapshot,
including supported transaction-private sequence metadata. Retained results keep
their captured text; a held prepared statement captures fresh metadata on each
execution. Views persist inquiry syntax and typed children, including strict
constant-NULL calls and CASE result children, never cached comment results.
Constant NULL eliminates runtime sibling demand, not argument analysis or
structural persistence. OID inputs accept existing implicit integer conversions
and the direct REGCLASS-to-OID conversion; NAME inputs accept TEXT/BPCHAR/VARCHAR.
This does not create an implicit TEXT-to-OID or transitive TEXT-to-REGCLASS-to-OID path.

Native builtin descriptions are the exact PostgreSQL18.0 release descriptions
for existing namespace `pg_catalog`11 and `public`2200, and existing types:
bool16, bytea17, char18, name19, int8 20, int2 21, int4 23, text25, oid26,
float4 700, float8 701, unknown705, bpchar1042, varchar1043, date1082, time1083,
timestamp1114, timestamptz1184, interval1186, timetz1266, numeric1700,
regclass2205, void2278 and uuid2950. Represented `_name`1003 has no description.
No pg_toast namespace, routine descriptor inventory, database/tablespace shared
model, COMMENT ON TYPE support or additional catalog projections are added.
The bounded queryable catalog schema above is unchanged.

Fresh-baseline Reset restores builtin namespace descriptions; Dump omits
unchanged seeds but records overrides and explicit NULL/empty removals.
Restore replays those differences atomically while retaining target shared role
comments. See [comment mutation and native controls](TABLES.md#object-comments)
for the remaining DDL/transaction boundaries.

Feature smoke: `make smoke RUN='^TestCommentInquirySmoke$' VERBOSE=0`.
Pinned independent reference: `make reference RUN='^TestReferenceCommentInquiry' VERBOSE=0`.


## Sequences

`CREATE [TEMPORARY | TEMP | UNLOGGED] SEQUENCE [IF NOT EXISTS] name` supports
`AS SMALLINT|INTEGER|BIGINT`, `INCREMENT [BY] n`, `[NO] MINVALUE`,
`[NO] MAXVALUE`, `START [WITH] n`, bare `RESTART` or `RESTART [WITH] n`,
`CACHE n`, `[NO] CYCLE` and `OWNED BY table.column|NONE`.
ALTER SEQUENCE supports these options, `RENAME TO`,
`OWNER TO role|CURRENT_ROLE|CURRENT_USER|SESSION_USER`, `SET SCHEMA`, and
`SET LOGGED|UNLOGGED`; DROP supports grouped targets, IF EXISTS and
RESTRICT/CASCADE. Sequence and table names share one relation namespace.
OWNED BY is a stable-column AUTO dependency: dropping its column or table
also drops the sequence, even under RESTRICT. Direct sequence drop never
drops its table. COMMENT ON SEQUENCE is database-local metadata. Quoted owner
spellings designate literal role names rather than session identity operands.
Same-owner OWNER TO does not require renewed schema CREATE; superuser ownership
transfers bypass destination schema CREATE. Ordinary genuine transfers retain
destination-role and schema authority checks. An OWNED BY-linked sequence cannot
change owners independently of its table; successful transfers remap owner ACL
identities while preserving unrelated grants.

CREATE/ALTER `OWNED BY table.column` acquires transaction-owned AccessShare on
the referenced table before final validation of its exact identity, column,
owner and schema. It waits behind table AccessExclusive and retains the hold
until transaction end; cancellation or lock timeout publishes no new sequence
or association. OWNED BY NONE and skipped IF NOT EXISTS/IF EXISTS commands
do not acquire dependency-table locks. ALTER locks the sequence before the table,
and participates in the same relation/row deadlock graph. Compatible queue
reordering avoids soft-cycle aborts; unavoidable cycles return 40P01 with an
unspecified cycle-member victim whose Finish rolls back before releasing holds.
Failed OWNED BY reassociation preserves the original column dependency and
discards the victim's earlier staged catalog changes. A surviving column drop
can complete before victim ROLLBACK and still cascades through that original
dependency; ROLLBACK then restores the victim's ability to issue new commands.

Generic `ALTER TABLE sequence_name RENAME TO name` also renames sequences and
invalidates held sequence source analysis just like the dedicated rename form.
Renaming any fixed sequence column, with or without COLUMN, is rejected with
42809 and the sequence-kind Detail before ordinary column lookup.

START configures the value used by a later bare RESTART; RESTART sets the current
uncalled value without changing that configured START. For example, CREATE
SEQUENCE s START 5 RESTART 9 first returns 9, while a later ALTER SEQUENCE s
RESTART first returns 5. Bare CREATE RESTART uses the configured START too.
ALTER START alone does not reset the current generator. The default ascending
BIGINT sequence returns 1 then 2; descending defaults start at the type's
maximum descending bound (-1). Bounds and CYCLE use MINVALUE/MAXVALUE, not START.
CREATE and ALTER share option validation, including START/restart bounds and
conflicting or redundant options. CREATE initializes an uncalled generator;
RESTART 9 and RESTART WITH 9 select the same first value.

currval before local nextval and lastval before any local nextval fail with
55000. Two-argument or true setval makes the next nextval advance and updates
local currval; false setval makes nextval return the exact supplied value without
changing currval. setval does not select a different sequence for lastval.

`nextval(regclass)`, `currval(regclass)`, `lastval()`,
`setval(regclass,bigint)` and `setval(regclass,bigint,boolean)` return INT8,
including for smallint/integer sequences. They work in existing scalar
SELECT/INSERT positions, UPDATE RHS/RETURNING, DELETE predicates/RETURNING and selected
relationless SELECT targets. Arithmetic and nonidentity casts retain child volatility;
filtered or empty projections do not allocate or read session sequence memory,
while imported argument errors still precede demanded effects.
The selected explicit `::regclass` / `CAST(... AS regclass)` syntax does
not introduce general casts. Preparation consumes no values. Prepared sequence
SQL retains a coherent group of analyzed regclass literals until a dependency
or effective namespace path changes; then the existing binder analyzes the
original source again. Rename/move can therefore fail with42P01 without
allocating, and DROP/recreation can resolve a new same-name sequence. Nonnull
regclass parameters resolve to OIDs at each execution's parameter-import boundary,
before zero-row evaluation, runtime NULL checks or nested function effects.
Symbolic input errors retain portal parameter Context without a literal Position.
Exact nonempty ASCII digit strings and exact `-` remain valid without an existing
relation; retained numeric regclass/OID identity follows rename/move and never
adopts a recreated name. Surrounding whitespace instead selects symbolic lookup.
Regclass text uses qualified-name input rules, not SQL tokenization. Punctuation
and comment-looking bytes belong to unquoted names: `s--suffix` and `s/*suffix*/`
never select the prefix `s`. Dots and ASCII whitespace separate components;
quoted components preserve case and embedded dots, and doubled quotes decode
to one quote. Names use ASCII folding and UTF8-safe 63-byte clipping.
Input-boundary smoke: `make smoke RUN='^TestSequenceRegclassInputSmoke$' VERBOSE=0`.

Standalone selected regclass casts have column label `regclass` and export
the visible relation name, or a schema-qualified name when shadowed/off-path,
with PostgreSQL identifier quoting. Missing nonzero OIDs export decimal text;
OID zero exports `-`. Internal datums remain typed OIDs, and result presentation
is captured before Rows is returned, so later DDL cannot change retained output.
Missing overloads report PostgreSQL's positioned signature and cast hint.
Ordinary nextval/currval/lastval/setval and newly created relation shadows alone
do not invalidate a held literal. Sequence options (even unchanged explicit
values), actual owner/persistence transitions and relation ACL publication do.
OWNED BY-only and same-owner/same-persistence commands do not. Bare ALTER SEQUENCE
without an action or option is a syntax error.
Equivalent search_path text preserves the analyzed identity when its effective
namespace identities and implicit flags are unchanged. Namespace catalog events
(including unrelated schema publication, namespace ACL changes or a peer's first
committed temporary namespace insertion) instead
reanalyze the held sequence group even if that effective path is unchanged.
Creating another sequence in an existing temporary namespace or rolling back
its first creation emits no committed namespace event. Physical-session end
retires native session-owned temporary objects without a namespace callback or
global epoch fence; PostgreSQL backend cleanup preserves its temporary namespace
tuple for reuse.
Reanalysis preserves
temporal captures, parameter contracts, result metadata and returned cursors.
This bounded native reanalysis preserves existing Prepare-time dynamic temporal
captures (for example `TIMESTAMP 'now'`). PostgreSQL full-query reanalysis may
recapture such a scalar at the reanalysis transaction's start; independent
fixtures assert that distinction rather than false dynamic-value parity.
Native persistent Reset/Restore remains an epoch fence, not PostgreSQL
DROP+CREATE reanalysis; only exact live caller-owned temporary provenance can
refresh across that fence.
An already-running call waiting for RX retains its analyzed OID: queued rename
continues on the original sequence, while queued DROP fails its opening with
XX000/no Position. The **next** held execution reanalyzes the original missing
name and fails42P01 at its literal. Argument evaluation is never replayed.
RX is demand-driven, not acquired for filtered/zero-row targets or a strict
NULL call. A known constant NULL eliminates these four strict builtin calls,
including volatile sibling arguments; runtime column NULL instead preserves
once-only nested argument effects and skips the outer sequence opening.

Ordinary allocation and setval are immediately visible and never rolled
back, even if a later scalar error prevents row publication. currval and
lastval belong to the physical session, as do cached reservations.
Returning a connection to its pool preserves them; physical close loses
unused reserved values. Peer setval does not discard already reserved
tails. Transactional ALTER/restart publishes a fresh generation at commit
and blocks peer sequence functions (including cache hits); rollback keeps
the old global generator. CREATE/DROP and sequence comments are likewise
transactional through the shared catalog overlay, alongside selected ALTER TABLE
TYPE/nullability/named-NOT-NULL/DROP COLUMN action lists. Unrelated transactional
DDL is not enabled.

TEMP sequences shadow persistent names only for their creating physical
session, accept its `pg_temp` alias, and disappear at physical end.
Native Reset/Restore retain live temporary sequence identities, local values and
pending cached reservations.
They are excluded from Dump. LOGGED/UNLOGGED preserve SQL options but do
not claim WAL, replication or disk durability in this in-process engine.
Sequence creation resolves explicit namespace identities and checks CREATE before
validating persistence. TEMP accepts its actual caller-owned temporary namespace;
ordinary logged creation there becomes temporary, while UNLOGGED is rejected.
IF NOT EXISTS does not bypass these checks. SET SCHEMA cannot move a sequence into
or out of a temporary namespace; a foreign temporary destination is rejected by
creation authority before the move restriction.

Temporary namespaces remain sequence-only. Ordinary CREATE TABLE into an
initialized `pg_temp` search path or an actual own/peer temporary namespace
rejects `0A000` before IF NOT EXISTS can skip a target, without creating storage.
Smoke: `make smoke RUN='^TestSequenceTemporaryTableBoundarySmoke$' VERBOSE=0`.


Reading a sequence returns one captured nonnull row with
`last_value INT8, log_cnt INT8, is_called BOOL`; native log_cnt is always0
because there is no physical WAL prelogging. last_value is the reserved
high-water mark, not necessarily the last number returned. Dump records
all effective options, association, owner, comment and called state;
Restore atomically installs new persistent generator identities and never
serializes session currval/lastval or pending cache tails.

Central ACL rules use USAGE **or** UPDATE for nextval, SELECT **or** USAGE
for currval/lastval, UPDATE for setval and SELECT for the sequence row.
Every execution rechecks rights, including held prepared/cache paths.
Owners/schema owners and explicit/absent ACLs follow existing catalog rules.

Schema CREATE denial for a valid sequence utility occurs at execution with no
Position, not during Prepare. Ordinary CREATE validates options first;
IF NOT EXISTS checks namespace authority before skipping or validating options.
Literal regclass schema USAGE lookup remains an analysis/Prepare check with the
input's character Position. Both `::regclass` and `CAST(... AS regclass)` use
the shared cast routing alongside the bounded int4/int8/text/bool and character
explicit-cast matrix. Unsupported type pairs retain their documented positioned
diagnostics; sequence support does not widen that matrix.

Feature commands: `make smoke RUN='^TestSequenceSmoke$' VERBOSE=0`,
`make test PACKAGES=./reference RUN='^TestReferenceSequence[A-Za-z0-9_]*$' VERBOSE=0`,
and `make reference RUN='^TestReferenceSequence[A-Za-z0-9_]*$' VERBOSE=0`.
The sequence families include an exact concurrent CACHE1 bag, CACHE7
distinctness, CHAIN/failed-commit/physical-end/replacement cleanup, the full
USAGE/SELECT/UPDATE matrix, operation-specific wrong-kind records, and
control failure atomicity. The public smoke exercises physical-end TEMP
cleanup as well as normal SQL and native-control round trips.
Prepared regression commands:
`make test PACKAGES=./reference RUN='^TestReferenceSequencePrepared[A-Za-z0-9_]*$' VERBOSE=0`
and `make reference RUN='^TestReferenceSequencePrepared[A-Za-z0-9_]*$' VERBOSE=0`.
These distinguish analyzed SQL from retained identity and exercise rename,
schema/path/shadow/ACL/options, numeric drop/recreation, rollback and queued
authority, coherent multi-literal groups and native temp-only replacement.

## Identity columns — change note

```sql
CREATE TABLE events (
  id INTEGER GENERATED ALWAYS AS IDENTITY (START 7 INCREMENT BY 3 CACHE 1),
  payload TEXT
);
INSERT INTO events(payload) VALUES('first'); -- id 7
INSERT INTO events OVERRIDING SYSTEM VALUE VALUES(99,'explicit');
INSERT INTO events OVERRIDING USER VALUE VALUES(99,'generated'); -- id 10
ALTER TABLE events ALTER id SET GENERATED BY DEFAULT;
ALTER TABLE events ALTER id SET INCREMENT BY 2 RESTART WITH 20;
INSERT INTO events DEFAULT VALUES; -- id 20
ALTER TABLE events ALTER id DROP IDENTITY;
```

Identity differs from SERIAL: no ordinary DEFAULT and a protected internal owned
sequence. DROP identity leaves old data and NOT NULL. Direct sequence DROP,
reassociation and removal of the identity column's NOT NULL are rejected.
ADD identity on an existing NOT NULL column starts a new sequence without rewriting
old values. Actual sequence names/options/state survive renames and native controls;
held INSERTs freshly bind current generation/options. SMALLINT/INTEGER identity
datums narrow to column width even if direct ALTER SEQUENCE widened the generator.
Ordinary supplied integer assignment remains checked.
`ALTER TABLE events ALTER id TYPE BIGINT` runs AS on the existing internal
sequence, preserving its identity/options except migrated type-extreme bounds.
The same-type form also replaces sequence/cache generation; a plain OWNED BY
sequence is unaffected. Original identity TYPE must remain integral (22023);
dropping identity in that same action list does not permit a TEXT target.
Private USING/CHECK demand and scalar state publish atomically with rewritten
rows, but physical-session currval/lastval and the caller's discarded AS cache
tail survive statement failure or rollback. A post-TYPE savepoint does not rewind
demand within that same private generator.
Expression-generated columns and UPDATE DEFAULT remain unsupported.


UPDATE admits `RETURNING [WITH (OLD AS o, NEW AS n)] output [, ...]` using
incumbent scalar expressions and mixed stars. Ordinary/target-qualified outputs
read NEW; OLD reads the actual predecessor. Explicit aliases hide corresponding
defaults and do not enter SET/WHERE. Both versions retain declared column metadata.
UPDATE accepts AS/bare target aliases and the supported inner/outer FROM trees with the
existing table/view alias and join forms. Bare SET destinations name physical
target columns; RHS/WHERE/RETURNING can qualify visible target and sources.
RETURNING `*` expands target NEW then exposed source columns. Multiple matches
evaluate eligible SET pairs but only one successful pair replaces/counts/returns
each physical target, using the same source row in RETURNING. Target row waits
refresh/recheck that predecessor, never source/candidate membership.
See [UPDATE FROM](QUERIES.md#update-from--change-note).
Query returns eagerly owned values even after rollback/native replacement;
Exec evaluates/discards outputs and returns the actual update count.
RETURNING aggregate calls remain errors, not output aggregate providers. COUNT
argument/name/signature diagnostics precede placement: `count()` reports `42809`,
`count(id,id)` reports the missing signature (`42883`), and a valid `count(id)`
reports aggregates not allowed in RETURNING (`42803`). This shared rule also
applies to DELETE RETURNING; non-RETURNING consumer guards are unchanged.
See [UPDATE RETURNING](QUERIES.md#update-returning--change-note).

## Unsupported surface

Database-qualified relation names, temporary tables (TEMP sequences are supported),
LATERAL joins, derived/subquery/CTE/function FROM, generic expressions/functions,
casts outside the bounded baseline/character-family/selected regclass/datetime sets,
constraints beyond nullability/defaults/CHECK/identity/UNIQUE, ALTER ADD CHECK/VALIDATE,
non-column serial type uses, default-containing ALTER lists, primary keys,
UPDATE row-subqueries/CURRENT OF, DELETE USING/CURRENT OF,
INSERT RETURNING, whole-row RETURNING records/scalar subqueries,
general COLLATE, two-phase commit, transactional table DDL outside selected
TYPE/nullability/identity/DROP COLUMN/constraint lists, transactional schema/view DDL,
non-sequence COMMENT/role mutation, SERIALIZABLE/SSI, DEFERRABLE safe snapshots,
SQL ACL mutation, or catalog columns beyond the bounded view providers
are not supported. Declared DEFAULT expressions admit fixed early-bound nextval
leaves with assignment/cast wrappers for SERIAL and native replay; other sequence
calls/composition and NULL/dynamic targets remain unsupported.

## Temporal zone conversion

`AT TIME ZONE` takes a TEXT zone operand; `AT LOCAL` uses session TimeZone.
TIMESTAMP produces TIMESTAMPTZ and vice versa; TIMETZ produces TIMETZ using
the destination offset at transaction start. Strict NULL preserves result
metadata. Temporal-to-TEXT casts observe DateStyle; selected datetime casts
use session-local civil/instant interpretation. See [types](TYPES.md#time-zone-aware-values).

`SET [SESSION | LOCAL] TIME ZONE {name | numeric_hours | INTERVAL 'input' | LOCAL | DEFAULT}`
and SHOW/RESET TIME ZONE use the existing settings utility. Optional interval
precision and HOUR/HOUR TO MINUTE/MINUTE qualifiers apply only to this
timezone-setting atom, not general INTERVAL expressions.
No timezone(...) alias, CURRENT_* expansion or timezone catalog views are added.


## Ordinary simple-key B-tree indexes

CREATE INDEX builds on populated ordinary tables, with 1–32 unqualified simple
column keys (repeated keys are legal), default/explicit btree, per-key direction
and NULL placement. Names are unqualified and belong to the target table's
schema, not the first creation schema in search_path. Omitted names use
PostgreSQL-compatible generated names; IF NOT EXISTS emits a duplicate-relation
NOTICE without changing an existing object, but does not bypass bad-key binding.
Prepare is syntax/description-only; semantic diagnostics occur at Execute.
The command returns zero affected rows and no result columns.

Equality/range/IS NULL and explicit ORDER BY use the ordinary query pipeline,
independently of index choice. Unordered results remain bags. Actual sorted
logical keys, including duplicates/NULLs, are maintained by common committed
row publication. See [the design](design/PG18-INDEX-BTREE.md) for representation,
publication and lifecycle invariants. Native Dump/Restore includes current
canonical CREATE INDEX definitions.

Writable explicit CREATE INDEX transactions are excluded natively (0A000),
while PostgreSQL supports transactional creation/rollback. Read-only execution
returns 25006 first. Standalone CREATE UNIQUE INDEX, expression/partial/concurrent
indexes, non-btree methods, opclasses/collations, INCLUDE, storage parameters/
tablespaces and DROP/ALTER INDEX remain excluded; CREATE TABLE UNIQUE constraints
are supported with owned backing B-trees. No planner, index-only scan or physical tree
claim is made.

## Whole-string pattern predicates

`lhs [NOT] LIKE pattern [ESCAPE escape]` and
`lhs [NOT] ILIKE pattern [ESCAPE escape]` return BOOL, preserving NULL.
Symbolic `~~`/`!~~`/`~~*`/`!~~*` and qualified OPERATOR spellings are supported.
Keyword predicates have BETWEEN-level precedence; symbols use generic operator
precedence. ESCAPE is only a keyword-form suffix. TEXT/NAME/BPCHAR left operands
and TEXT patterns are admitted (VARCHAR converts implicitly); existing BYTEA
has LIKE/NOT LIKE only. See [the matching contract](DIALECT.md#like-and-ilike--change-note)
for physical character, collation, escape and diagnostic behavior.
Pattern and escape operands retain ordinary explicit-cast syntax, including
`b LIKE $1::bytea ESCAPE $2::bytea`. Escape overload selection precedes the
outer operator: unknown pattern/escape arguments select TEXT independently
of a BYTEA lhs, so select the binary normalization signature with typed inputs.
LIKE and ILIKE retain contextual projection-label spelling: `SELECT x ilike
FROM t` names the column `ilike`, while a following pattern operand selects
the predicate. Bare LIKE/ILIKE labels apply to complete scalar or grouped bases,
not ungrouped completed predicates: `(v BETWEEN 'a' AND 'z') LIKE` is a label,
while `v BETWEEN 'a' AND 'z' LIKE` fails at LIKE. This does not admit incomplete
WHERE predicates or trailing input.
Keyword predicates also use the shared SELECT/UPDATE WHERE operand path,
including column patterns/escapes and held parameters; failed demanded escape
normalization does not publish earlier UPDATE row changes.
Collatable casts, conditional result arms and supported string outputs preserve
collation identity and indeterminacy independently of runtime branch choice.
Existing scalar COLLATE annotations override implicit conflicts, including an
explicit default annotation. Different explicit annotations fail analysis and
Prepare with 42P21 at the second conflicting COLLATE; equal identities combine.
A demanded implicit C/POSIX comparison in NULLIF/GREATEST/LEAST raises 42P22
before an enclosing LIKE;
NULL controls retain their normal lazy comparison behavior. Views exposing
indeterminate collatable results reject Execute with 42P22, without publishing
the view; Prepare still succeeds. BOOL views containing matching predicates
remain valid and report matching errors only when their operands are demanded.

