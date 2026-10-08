# SQL reference

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Detailed contracts are grouped by topic:

- [Queries and data manipulation](QUERIES.md): INSERT, DELETE, UPDATE, SELECT, expressions and casts.
- [Table definitions and comments](TABLES.md): defaults, constraints, renames and column removal.
  CHECK supports complete-row TRUE/NULL acceptance, false-only INSERT/UPDATE
  rejection, named identity, local NO INHERIT and real native-control persistence.
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
           | from_item [INNER] JOIN from_item ON logical_predicate
           | from_item [INNER] JOIN from_item USING (column [, ...]) [AS merged_alias]
           | from_item NATURAL [INNER] JOIN from_item
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

### Table namespace moves

`ALTER TABLE [IF EXISTS] target SET SCHEMA schema` is a standalone ordinary-table
utility: the destination is one identifier and comma action lists are syntax
errors before any effects. Target markers do not add hierarchy support. Execution
requires source schema USAGE, table ownership and destination CREATE, even for a
same-schema no-op; destination USAGE and source CREATE are not required.
Missing conditional targets skip before resolving the destination, with one notice.
Prepare is effect-free and does not resolve, authorize or lock.

The table and all column-owned sequences move in one catalog publication under
ACCESS EXCLUSIVE locks. Unowned sequences and referencing views stay in place.
Rows, identities, NOT NULL names, defaults, comments, ACLs and sequence generators
and physical-session caches survive. Held SQL rebinds original names; old qualified
names fail after a move, while compatible unqualified plans can find the new schema
through search_path. Stored views retain dependencies and deparse current names.
Dump/Restore preserves current persistent definitions/data rather than ALTER history.
SET SCHEMA in explicit transactions remains excluded. Non-table generic moves,
temporary/system destinations, indexes and automatic row types are not added.
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


The FROM productions above are precedence notation: explicit joins bind tighter
than comma lists and associate left; parentheses preserve boundaries. ON sees
only its operands. Aliases hide original qualifiers; a whole-join alias hides
operand qualifiers. A special USING AS alias exposes only merged keys without
hiding operands and admits no column-alias list. Query qualification applies to
SELECT/INSERT SELECT consumers. DELETE and UPDATE RETURNING use their
target/OLD/NEW namespaces, including qualified supported cast children; DELETE
WHERE and UPDATE SET/WHERE cast/qualification guards are not widened. Raw guards
remain at DEFAULT, VALUES and settings-call scalar entry points. Previously
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
left-order common names, with no common name equivalent to CROSS JOIN. Results
preserve all matching occurrences and promise no unordered traversal sequence.
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

`CREATE VIEW` stores an immutable, identity-bound definition analyzed by the
ordinary SELECT binder. The body admits the current scalar/filter/group/HAVING/
expression and multi-key ordering repertoire and table, sequence, view and
bounded catalog sources. It is not executed by CREATE or Prepare; subsequent reads evaluate it
against current visible data. Explicit names rename an output prefix; fewer
names than outputs are legal, more fail42601, and duplicate final names fail42701.
Unknown-only outputs become TEXT. Creation-time `*` expansion and output types
are fixed; zero-column views retain real source occurrences and count semantics.
CREATE has affected count zero and no result columns or runtime parameters.

A legal scalar definition may store one trailing FOR KEY SHARE/FOR SHARE/
FOR NO KEY UPDATE/FOR UPDATE clause. CREATE remains row-lock-free; the immutable
mark survives identity remapping, canonical deparse (after ORDER BY), Dump and
Restore. Ordinary reads enforce stored marks on their physical base subtree.
An outer clause combines using the strongest SQL mode; unrelated sources are
not marked by a definition. Ordinary aggregation over a marked view is legal
and locks qualifying input before grouping/HAVING, even if output is empty.
A clause reaching a grouped/aggregate view is instead mode-specific0A000.
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
NATURAL becomes its creation-time explicit USING key list (or CROSS JOIN when
empty), so current names cannot broaden the join. Merged values and implicit
common-type conversions are recreated by the ordinary USING binder, not explicit
casts. Stored ORDER preserves frozen input-slot identity through qualified
physical or merged namespaces, while output references are frozen separately
as ordinals. Each key retains its direction and NULL policy. Render-private
USING aliases preserve synthetic merged usage without substituting a physical
operand or allowing a later output-name override to retarget the key.

Views reuse the supported OID scalar and BETWEEN lowering, including nested
predicate-valued endpoints. Canonical replay uses the same scalar CASE bridge
as stored defaults for grouped predicate operands; it does not broaden ordinary
comparison syntax or evaluate definitions during serialization.

Stored literal occurrences retain their analyzed value and type usage, including
temporal precision, independently typed BETWEEN occurrences and each JOIN ON clause.
Definition reconstruction and native replay use type-owned canonical literals,
not creation-time spellings such as `today`. Temporal input and supported
DATE/TIME/TIMESTAMP/TIMETZ/TIMESTAMPTZ casts use the captured session environment;
native DateStyle and TimeZone settings are supported. Canonical replay remains
independent of presentation settings.

Replacement, recursive/TEMP views, view writes/check options, general ALTER/
DROP VIEW, subqueries, public GRANT, RLS, UDFs and optimizer
introspection are separate unsupported capabilities. Native ACL unit controls
and PostgreSQL-only GRANT witnesses do not imply a public native ACL API.
Smoke: `make smoke RUN='^TestViewSmoke$' VERBOSE=0`;
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
nullability/named-NOT-NULL/DROP COLUMN action lists. Unrelated transactional DDL
is not enabled.

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

Feature commands: `make smoke RUN='^TestSequenceSmoke$' VERBOSE=1`,
`make test RUN='^TestSequence' VERBOSE=1`, and
`make reference RUN='^TestReferenceSequence' VERBOSE=1`.
The sequence families include an exact concurrent CACHE1 bag, CACHE7
distinctness, CHAIN/failed-commit/physical-end/replacement cleanup, the full
USAGE/SELECT/UPDATE matrix, operation-specific wrong-kind records, and
control failure atomicity. The public smoke exercises physical-end TEMP
cleanup as well as normal SQL and native-control round trips.
Prepared regression commands: `make test RUN='^TestSequencePrepared' VERBOSE=1`
and `make reference RUN='^TestReferenceSequencePrepared' VERBOSE=1`.
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
SERIAL, expression-generated columns and UPDATE DEFAULT remain unsupported.


UPDATE admits `RETURNING [WITH (OLD AS o, NEW AS n)] output [, ...]` using
incumbent scalar expressions and mixed stars. Ordinary/target-qualified outputs
read NEW; OLD reads the actual predecessor. Explicit aliases hide corresponding
defaults and do not enter SET/WHERE. Both versions retain declared column metadata.
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
outer/LATERAL joins, derived/subquery/CTE/function FROM, generic expressions/functions,
casts outside the bounded baseline/character-family/selected regclass/datetime sets,
constraints beyond nullability/defaults/CHECK/identity, ALTER ADD CHECK/VALIDATE,
serial, primary keys,
UPDATE FROM/row-subqueries/CURRENT OF, DELETE USING/CURRENT OF,
INSERT RETURNING, whole-row RETURNING records/scalar subqueries,
general COLLATE, two-phase commit, table/schema/view DDL in explicit
transactions outside the admitted atomic ALTER actions, non-sequence COMMENT/role
mutation, SERIALIZABLE/SSI, DEFERRABLE safe snapshots, SQL ACL mutation,
or catalog columns beyond the bounded view providers
are not supported. Declared DEFAULT expressions remain bounded and reject sequence
calls, including calls with constant NULL arguments.

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

