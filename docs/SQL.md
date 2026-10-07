# SQL reference

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Detailed contracts are grouped by topic:

- [Queries and data manipulation](QUERIES.md): INSERT, DELETE, UPDATE, SELECT, expressions and casts.
- [Table definitions and comments](TABLES.md): defaults, constraints, renames and column removal.
- [SQL types and Go values](TYPES.md): codecs, metadata and adapter differences.
- [Identifiers and literals](LEXICAL.md): names, strings and numeric syntax.
- [Roles, authorization and schemas](AUTHORIZATION.md): ownership, schema lookup and search path.
- [Transactions and locks](TRANSACTIONS.md) and [session settings](SETTINGS.md).

The grammar summary below is an overview; the topic guides and [dialect limits](DIALECT.md) describe feature-specific support and exclusions.

```text
CREATE TABLE [IF NOT EXISTS] [schema.]name ([element [, ...]])
element := column type [[CONSTRAINT name] NULL | [CONSTRAINT name] NOT NULL | [CONSTRAINT name] DEFAULT default_expression]...
         | [CONSTRAINT name] NOT NULL column | LIKE [schema.]source
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
target := [schema.]name [*] | ONLY [schema.]name | ONLY ([schema.]name)
INSERT INTO [schema.]name [(column [, ...])] VALUES (insert_value [, ...]) [, ...]
INSERT INTO [schema.]name [(column [, ...])] SELECT [projection_target [, ...]]
       [FROM [schema.]name [WHERE logical_predicate] [ORDER BY column [ASC | DESC]]]
INSERT INTO [schema.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
       ORDER BY (positive_output_ordinal | columnN | +numeric_constant) [ASC | DESC]
INSERT INTO [schema.]name DEFAULT VALUES
insert_value := value | DEFAULT
DELETE FROM [schema.]name [[AS] alias] [WHERE delete_predicate]
SELECT (* | projection_target [, ...]) FROM [schema.]name
       [WHERE logical_predicate] [ORDER BY column [ASC | DESC]]
SELECT null_count_call [, null_count_call ...]
type  := SMALLINT | INT2 | INTEGER | INT | INT4 | BIGINT | INT8 | TEXT | BOOLEAN | BOOL
       | REAL | FLOAT4 | DOUBLE PRECISION | FLOAT8 | FLOAT[(p)]
       | DATE | (TIME | TIMESTAMP) [(precision)] [WITHOUT TIME ZONE] | UUID | BYTEA
       | (CHAR | CHARACTER | BPCHAR | VARCHAR | CHARACTER VARYING) [(length)]
value := signed-64-bit integer | 'string' | E'escape string' | U&'Unicode string' [UESCAPE 'escape'] | $$string$$ | $tag$string$tag$ | TRUE | FALSE | NULL | $n | DATE 'input' | (TIME | TIMESTAMP) [(precision)] [WITHOUT TIME ZONE] 'input'
scalar := unqualified column | value | (scalar) | CAST(scalar AS cast_type) | scalar::cast_type
        | (int4 | int8 | text | bool)(scalar)
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
projection_target := logical_target | null_count_call
call_argument := scalar | null_count_call
null_count_call := (num_nulls | num_nonnulls)(call_argument [, ...])
default_expression := value | scalar comparison_operator scalar | (logical_target)
                    | default_expression IS [NOT] DISTINCT FROM default_expression
```
Conditional expressions compose with the existing scalar consumers, including
INSERT sources, mutation predicates/RHS and stored DEFAULTs. Their common types,
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
SELECT/INSERT positions, UPDATE RHS, DELETE predicates and selected relationless
SELECT targets. Arithmetic and nonidentity casts retain child volatility;
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
transactional without expanding other transactional DDL.

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


## Unsupported surface

Three-part/database qualification, temporary tables (TEMP sequences are supported), SELECT aliases/qualified columns, joins, generic expressions/functions, casts outside the bounded baseline/character-family/selected regclass sets, constraints beyond nullability/defaults, serial/identity, ALTER DEFAULT, primary keys, UPDATE FROM/row-subqueries/CURRENT OF, DELETE USING/CURRENT OF, RETURNING, multiple sort keys, two-phase commit, transactional table/schema DDL or non-sequence COMMENT/role mutation, SERIALIZABLE/SSI, DEFERRABLE safe snapshots, SQL ACL mutation, or SQL catalog projections are not supported. Declared DEFAULT expressions remain bounded and reject sequence calls, including calls with constant NULL arguments; sequence support does not widen that consumer. SQL transaction controls and savepoints, RU/RC/RR modes, read-only access, CHAIN and current/default transactional settings described in [transactions](TRANSACTIONS.md) are supported. Recognized unsupported features fail with SQLSTATE `0A000`; malformed SQL fails with `42601`. Unsupported suffixes and multiple statements fail before any prefix executes.
