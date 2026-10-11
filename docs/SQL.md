# SQL reference

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## TRUNCATE TABLE — change note

```sql
TRUNCATE [TABLE] target [, target ...]
  [CONTINUE IDENTITY] [RESTRICT | CASCADE];
-- target: [schema.]name [*] | ONLY [schema.]name | ONLY ([schema.]name)
TRUNCATE TABLE accounts, entries CONTINUE IDENTITY;
TRUNCATE ONLY parent;
TRUNCATE parent CASCADE;
```

CONTINUE IDENTITY and RESTRICT are the defaults. TRUNCATE and CONTINUE remain
usable as unreserved identifiers. Targets are resolved at execution, in supplied
order, with normal schema visibility and exact table TRUNCATE authority.
Go `PrepareContext` is zero-formal/zero-column and performs no target lookup,
locking or effect. SQL PREPARE's utility-body prohibition is unchanged.
Direct/prepared Exec reports affected0 regardless of removed row count; Query
returns no columns or rows. There is no WHERE, RETURNING or parameter surface.

Without ONLY, real ordinary inheritance descendants and nested RANGE partition
leaves are selected once. ONLY on a plain inherited parent removes only its
physical rows; ONLY on a partitioned root fails42809 with Hint
`Do not specify the ONLY keyword, or use TRUNCATE ONLY on the partitions directly.`
Direct leaf truncation leaves siblings intact. RESTRICT examines FK definitions,
not row population: an external referencer blocks even when empty. A complete
explicit FK group, self-reference or complete cycle can truncate without CASCADE.
CASCADE reaches distinct incoming FK tables in ordered waves and emits
`truncate cascades to table "name"` NOTICE records before reached member
permission/activity checks. It does not truncate outgoing parents or recursively
inherit an ordinary table reached only through FK CASCADE.

Table/attribute/index/constraint identities, ACLs, comments, row-security flags,
defaults, generated expressions and sticky hasindexes remain unchanged. CHECK
and row-policy expressions are not evaluated; whole-table ACL still applies.
CONTINUE preserves SERIAL/IDENTITY generators, reservations and physical-session
currval/lastval/cache state, requires no sequence privilege and draws no values.
Read-only execution rejects even TEMP with25006
`cannot execute TRUNCATE TABLE in a read-only transaction` before name/ACL lookup.
Aborted blocks retain25P02. Valid RESTART IDENTITY is an explicit positioned0A000
native boundary, not a successful no-op; trailing malformed syntax is42601.

See [atomic generations and non-MVCC snapshots](TRANSACTIONS.md#truncate-generations--change-note),
[hierarchy authority](TABLES.md#truncate-hierarchy-consumer--change-note),
[exact grants](AUTHORIZATION.md#truncate-table-authority) and
[both-mode proof](TESTING.md#truncate-selected-proof).

## Math constants and special functions — change note

`cbrt(x)`, `degrees(x)`, `radians(x)`, `erf(x)`, `erfc(x)`, `gamma(x)`
and `exp(x)` support DOUBLE PRECISION scalar inputs/results; `pi()` is a
zero-argument DOUBLE PRECISION constant. Unary NULL inputs return typed NULL.
Projection/order/group, held Go statements and SQL PREPARE use the existing
math provider. Filter function outputs through derived/stored projection columns;
selected calls remain excluded as direct WHERE predicate leaves. Immutable
stored views/generated expressions/indexes
retain `pg_catalog.pi()` through native Dump/Restore and descriptor remapping;
generic function defaults remain excluded. NUMERIC exp is unchanged.
See [IEEE/range/platform contract](DIALECT.md#math-constants-and-special-functions)
and [selected reference proof](TESTING.md#math-constants-selected-proof).

## DISCARD

```sql
DISCARD ALL;
DISCARD PLANS;
DISCARD SEQUENCES;
DISCARD TEMP;       -- TEMPORARY is an alias
```

No target names, IF EXISTS, category lists or combinations are accepted. Prepare
has no cleanup effect; execution returns affected0 and no columns. See the
[category matrix](DIALECT.md#discard--change-note) and
[transaction distinctions](TRANSACTIONS.md#discard-transaction-boundaries).

## Typed tables and standalone composite lifecycle

`CREATE TABLE name OF composite [(column [WITH OPTIONS] constraints, ...)]`
uses the live source shape and existing table-local defaults/constraints.
`ALTER TABLE name OF composite` attaches an exactly compatible live ordered shape;
`ALTER TABLE name NOT OF` removes only that attachment. `pg_class.reloftype OID`
projects the same identity used by the real NORMAL whole-table dependency.
Standalone `ALTER TYPE` accepts ADD/DROP/ALTER ATTRIBUTE actions with individual
RESTRICT/default or CASCADE and separate RENAME ATTRIBUTE; grouped DROP TYPE
supports IF EXISTS and modes. See [full contract and boundaries](TABLES.md#typed-tables--change-note).

Detailed contracts are grouped by topic:

- [Queries and data manipulation](QUERIES.md): INSERT, MERGE insertion/matched and BY SOURCE UPDATE/DELETE, DELETE, UPDATE, SELECT, expressions and casts.
- [Table definitions and comments](TABLES.md): defaults, constraints, renames and column removal.
  CHECK supports complete-row TRUE/NULL acceptance, false-only INSERT/UPDATE
  rejection, TYPE dependency reparse/final-row validation, named identity, local
  NO INHERIT and real native-control persistence. ALTER CHECK violations have
  a contains-row diagnostic without DML's failing-row Detail.
- [SQL types and Go values](TYPES.md): codecs, metadata and adapter differences.
- [Identifiers and literals](LEXICAL.md): names, strings and numeric syntax.
- [Roles, authorization and schemas](AUTHORIZATION.md): CREATE/ALTER role attributes,
  ownership, role membership GRANT/REVOKE options and origins, CREATE initial
  memberships, ALTER GROUP, schema lookup and search path.
- [Transactions and locks](TRANSACTIONS.md) and [session settings](SETTINGS.md).

The grammar summary below is an overview; the topic guides and [dialect limits](DIALECT.md) describe feature-specific support and exclusions.

## Variadic concatenation and quoting — change note

```sql
SELECT concat(true,false);                    -- tf
SELECT concat_ws('|',VARIADIC '{1,NULL,2}'::integer[]); -- 1|2
SELECT quote_ident('a"b');                    -- "a""b"
SELECT quote_literal(true);                  -- 'true'
SELECT quote_nullable(NULL);                 -- text NULL
SELECT pg_client_encoding();                 -- NAME UTF8
```

`concat(value[, ...])` and `concat_ws(text,value[, ...])` use original type
OUTPUT and ignore individual NULLs, not a common-type TEXT cast. A NULL
separator or whole VARIADIC array returns SQL NULL; empty/all-NULL arrays return
empty TEXT. Ordinary arrays remain one operand; explicit final VARIADIC borrows
the existing row-major elements, including domains and nondefault bounds.
`quote_ident(text)` is strict, and `quote_literal(text|anyelement)` is strict;
`quote_nullable(text|anyelement)` returns the four-character text `NULL` for
NULL. ANYELEMENT quote overloads use explicit TEXT conversion, so BOOL is
`true`/`false` and scalar BPCHAR loses trailing padding. Literal quoting doubles
apostrophes/backslashes and uses uppercase `E` when needed, independently of
standard_conforming_strings. Identifier quoting uses PostgreSQL keyword classes
and quote_all_identifiers. Encoding lookup canonicalizes the physical session's
client_encoding, not server_encoding or the retained SHOW alias.
Selected function inputs reuse the existing internal `"char"` literal codec:
concat preserves its original OUTPUT, while uncast quote_literal/quote_nullable
inputs report PostgreSQL's ambiguous-overload diagnostic. This does not admit
general internal-CHAR casts. In particular nested `'x'::"char"::text` still
reports native `0A000` at the inner type; PostgreSQL accepts that explicit cast.

All six support direct/prepared public routes and the existing stored-expression
consumers and native controls. Parameterless held statements retain their first
successful Execute-planning immutable quote results. Every native parameterized
execution uses fresh operation-local quote constants, including DEFAULT and
view expressions; none are retained in the statement description. PostgreSQL
custom plans reevaluate constants, while generic plans can retain them. This is
an explicit native planner boundary, not claimed generic-plan parity; native
implements no cost selection, planner counters or plan_cache_mode. Parameters
and stable concat/format/encoding reevaluate. Effective quote immutability follows the
selected TEXT cast: integer/BYTEA generated columns and expression indexes are
accepted, temporal-output-dependent expressions are not. Missing signatures,
unresolved ANY parameters and nonarray explicit VARIADIC inputs retain the
PostgreSQL diagnostics rather than inferring SQL types from Go argument values.
Duplicate named arguments and positional arguments following named arguments
report `42601` at their retained source positions; these functions do not gain
named formal parameters.
No array_to_string, new array/type capability or non-UTF8 transport is added.
See [detailed contracts](DIALECT.md#variadic-concatenation-and-quoting--change-note).
Smoke: `make check RUN='^TestReferenceStringVariadicFeatureSmoke$'`; pinned
PostgreSQL mode uses the identical selector with `REFERENCE=1`.
Policy/review smoke selects
`make check RUN='^TestReferenceStringVariadic(DeparseRelations|BoundFormat|ViewWrites|MergeDefaults|Parameterized)$'`,
also with `REFERENCE=1`, asserting exact independent backend outcomes.

## JSONPath typed IO — change note

```sql
CREATE TABLE paths(id INT,p jsonpath);
INSERT INTO paths VALUES(1,jsonpath 'strict $.a[0]?(@.x >= 1)');
SELECT p FROM paths; -- strict $."a"[0]?(@."x" >= 1)
SELECT pg_catalog.jsonpath '$a.b'; -- $"a"."b"
SELECT CAST('$.a'::text AS jsonpath); -- $."a"
```

Prepared `INSERT INTO paths VALUES($1,$2)`, `UPDATE paths SET p=$1 WHERE id=$2`
and `SELECT CAST($1 AS jsonpath)` infer path inputs before execution. String,
owned byte and protocol scalar inputs use the destination codec; nil/typed-nil
bytes are SQL NULL, distinct from nonnull path `null`. Complete PG18 syntax and
input-time ARE/ERE/BRE validation produce canonical output, without evaluating
paths or introducing JSON query functions/operators. Explicit character IO
casts reuse ordinary truncation/padding; typed character assignment remains denied.
See [syntax, diagnostics, metadata and persistence](TYPES.md#jsonpath-typed-input--change-note).
Smoke: `make check RUN='^TestReferenceJSONPathFeatureSmoke$'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'`.

## Roles and attributes

```text
CREATE {ROLE | USER | GROUP} name [WITH] [attribute ...]
  [IN {ROLE | GROUP} names] [{ROLE | USER} names] [ADMIN names]
ALTER {ROLE | USER} {name | CURRENT_ROLE | CURRENT_USER | SESSION_USER}
  [WITH] [attribute ...]
attribute := SUPERUSER | NOSUPERUSER | CREATEDB | NOCREATEDB
  | CREATEROLE | NOCREATEROLE | LOGIN | NOLOGIN | INHERIT | NOINHERIT
  | REPLICATION | NOREPLICATION | BYPASSRLS | NOBYPASSRLS
  | CONNECTION LIMIT signed_int4 | PASSWORD string_or_NULL
  | ENCRYPTED PASSWORD string | VALID UNTIL string
```

ROLE/GROUP default NOLOGIN and USER LOGIN; other flags default false except
INHERIT=true, limit=-1, NULL password and absent expiry. ALTER preserves every
omission, including ALTER USER's LOGIN state. Initial memberships remain CREATE-only
and publish atomically with the role and ordinary creator's ADMIN-only edge.
Role attributes use direct effective authority, not membership inheritance;
membership still provides independent ADMIN, object INHERIT and identity SET paths.
New membership INHERIT defaults from its member's attribute without changing old
edges. See [attribute and membership authority](AUTHORIZATION.md#role-attributes).

PASSWORD stores real fixed-default SCRAM metadata or recognized encoded values;
NULL/empty clears it. VALID UNTIL is password-expiry timestamptz metadata, not
trusted admission expiry. LOGIN and CONNECTION LIMIT govern physical admission
against the original authenticator, with idle counting, reuse without extra slots,
physical-close release, superuser limit exemption and NOLOGIN FATAL28000.
See [physical admission](DRIVER.md#trusted-role-admission-and-physical-limits).
Native explicit-transaction role utilities remain0A000, and read-only Execute
uses25006 before lookup. Native DROP and COMMENT retain actual-SUPERUSER gates;
CREATEROLE/creator ADMIN do not expand native DROP authority. Predefined identities and capabilities are
documented in [the role contract](AUTHORIZATION.md#predefined-postgresql18-roles--change-note). Rename,
ALTER ROLE SET/RESET, password handshake/settings, pg_authid and SQL
object grants remain excluded; integrated role GRANT/REVOKE is unchanged.

## Period overlap

```sql
SELECT (DATE '2001-01-01', DATE '2001-01-03')
       OVERLAPS (DATE '2001-01-02', DATE '2001-01-04'); -- true
SELECT (TIMESTAMP '2001-01-01', TIMESTAMP '2001-01-02')
       OVERLAPS (TIMESTAMP '2001-01-02', TIMESTAMP '2001-01-03'); -- false
SELECT (TIME '02:00', TIME '02:00')
       OVERLAPS (TIME '02:00', TIME '03:00'); -- true: single instant at start
SELECT (TIMESTAMP '2001-01-03', INTERVAL '-2 days')
       OVERLAPS (TIMESTAMP '2001-01-02', TIMESTAMP '2001-01-02'); -- true
SELECT (NULL::timestamp, TIMESTAMP '2001-01-02')
       OVERLAPS (TIMESTAMP '2001-01-01', TIMESTAMP '2001-01-03'); -- true
SELECT (TIMESTAMP '2001-01-01', TIME '01:00')
       OVERLAPS (TIMESTAMP '2001-01-01', TIME '02:00'); -- true: local clock duration
```

Use `($1,TIMESTAMP '2001-01-03') OVERLAPS
($2,TIMESTAMP '2001-01-04')` for inferred timestamp start parameters, or explicitly
type all four parameters when the signature must be fixed. All-UNKNOWN inputs
are ambiguous. Ordinary `pg_catalog.overlaps(a,b,c,d)` uses the same overloads.
See [normalization, NULL and bounded resolution](DIALECT.md#period-overlap--change-note).

## Selected age and finite predicate calls

```sql
SELECT age(TIMESTAMP '2004-06-01', TIMESTAMP '2004-04-30'); -- 1 mon 1 day
SELECT age(TIMESTAMP '0001-01-01 AD', TIMESTAMP '0001-01-01 BC'); -- 1 year
SELECT age(CURRENT_DATE::timestamp); -- zero at transaction-local midnight
SELECT isfinite(DATE '2000-01-01'), isfinite(TIMESTAMP 'infinity'),
       isfinite(INTERVAL '-infinity'), isfinite(NULL::interval);
-- true, false, false, NULL
```

Use explicit selected input types for unary calls: untyped NULL, string literals
and parameters are ambiguous in PostgreSQL's wider overload set. Prepared calls
can use `age($1::timestamp,$2::timestamp)` and `isfinite($3::interval)`;
an exact TIMESTAMP binary argument also infers the other UNKNOWN argument.
These positional calls have no declared argument labels. See
[symbolic semantics and bounded resolution](DIALECT.md#age-and-finite-predicates--change-note).

## Fixed-stride datetime bins

```sql
SELECT date_bin(INTERVAL '15 minutes',
                TIMESTAMP '2000-01-01 00:02:29.999999',
                TIMESTAMP '2000-01-01 00:02:30'); -- 1999-12-31 23:47:30
SELECT date_bin(INTERVAL '30 minutes',
                TIMESTAMP '2024-02-01 15:00',
                TIMESTAMP '2024-02-01 17:00'); -- 2024-02-01 15:00
SELECT date_bin('1 day', DATE '2000-01-02', DATE '2000-01-01');
-- TIMESTAMPTZ at session-local midnight
```

The TIMESTAMP and TIMESTAMPTZ overloads floor-align positive elapsed strides
around the origin. Days mean24hours, not calendar-day DST adjustment; nonzero
months/years are rejected. Mixed timestamp variants promote to TIMESTAMPTZ.
Use `date_bin($1::interval,$2::timestamp,$3::timestamp)` to fix the civil
overload; all-unknown `date_bin($1,$2,$3)` infers INTERVAL/TIMESTAMPTZ/TIMESTAMPTZ.
Calls have no declared argument labels. Results are strict typed NULL without
hiding literal/parameter input errors and do not inherit precision modifiers.
See [resolution, infinity and diagnostics](DIALECT.md#datetime-binning--change-note).

## Selected datetime constructor calls

`make_date`, `make_time`, `make_timestamp`, `make_timestamptz` and
`make_interval` accept ordinary positional calls and named `label => expression`
or `label := expression` calls. Labels use SQL identifier folding; quoted
lowercase labels match, while quoted uppercase labels do not. Named arguments
can be reordered, but every positional argument must precede all named ones.
Duplicate labels are rejected; unknown labels, positional/name collisions,
missing required arguments and VARIADIC do not resolve a constructor signature.
Every interval component has its own zero default; explicit NULL is never zero.
Date/time/timestamp required slots have no defaults, and six/seven-argument
timestamptz are distinct strict overloads. The timestamp day label is `mday`;
interval uses plural `mins`/`secs`, while time uses singular `min`/`sec`.

```sql
SELECT make_date(day => 15, year => 2013, month => 7);
SELECT make_interval(1, days => 2, secs := 1.5);
SELECT make_timestamptz(2013,7,15,12,0,0,'America/New_York');
```

These calls compose through existing scalar expression and stored-definition
routes, preserving written labels/order and omitted defaults in definition
presentation. General named argument binding for other functions is not added;
`=>`/`:=` do not become binary SQL operators. See
[constructor signatures and semantics](DIALECT.md#datetime-constructors--change-note).


Domain declarations use represented scalar/domain or supported array bases.
NULL is the default; explicit NULL is accepted. DEFAULT/CHECK/NOT NULL,
ALTER DOMAIN, array operators/functions/constructors/subscripts, native type
ACL-management SQL and explicit-transaction domain DDL remain excluded.
Duplicate domain names are diagnosed before the proposed
base type. Namespace CREATE, represented base USAGE and owner-only DROP use current
authority. RESTRICT reports actual type/column/stored-expression dependencies;
CASCADE removes dependent columns/types/expressions while preserving unrelated
tables, surviving attributes and row data. DROP SCHEMA includes domain closure.
Native Dump/Restore replays qualified domain declarations before dependents.
See [logical/base identities and metadata](TYPES.md#domains--change-note).


Explicit table locking: `LOCK [TABLE] target [, target ...]
[IN lockmode MODE] [NOWAIT]`, with target `name`, `name *`, `ONLY name` or
`ONLY(name)`. Modes are ACCESS SHARE, ROW SHARE, ROW EXCLUSIVE, SHARE UPDATE
EXCLUSIVE, SHARE, SHARE ROW EXCLUSIVE, EXCLUSIVE and ACCESS EXCLUSIVE (default).
Execution requires an explicit transaction, affects zero rows and processes
targets in order. Views recursively lock bound sources in the same mode;
ordinary parent LOCK rejects the inheritance recursion boundary, while ONLY locks
the named physical table. Prepare is
metadata-only. See [privileges, conflict matrix and savepoint lifetime](TRANSACTIONS.md#relation-locks).

```text
CREATE COLLATION [IF NOT EXISTS] [schema.]name FROM [schema.]source
CREATE COLLATION [IF NOT EXISTS] [schema.]name (option = value [, ...])
ALTER COLLATION [schema.]name REFRESH VERSION
ALTER COLLATION [schema.]name RENAME TO name
ALTER COLLATION [schema.]name SET SCHEMA schema
ALTER COLLATION [schema.]name OWNER TO role
DROP COLLATION [IF EXISTS] [schema.]name [, ...] [RESTRICT | CASCADE]
CREATE DOMAIN [schema.]name [AS] base_type [COLLATE [schema.]collation] [NULL]
DROP DOMAIN [IF EXISTS] [schema.]name [, ...] [RESTRICT | CASCADE]
CREATE [TEMP | TEMPORARY] TABLE [IF NOT EXISTS] [schema.]name ([element [, ...]])
  [ON COMMIT PRESERVE ROWS | ON COMMIT DELETE ROWS | ON COMMIT DROP]
TRUNCATE [TABLE] target [, ...] [CONTINUE IDENTITY] [RESTRICT | CASCADE]
CREATE [OR REPLACE] [TEMP | TEMPORARY] VIEW [schema.]name [(column [, ...])]
  [WITH (security_barrier [= boolean] [, security_invoker [= boolean]])] AS SELECT ...
CREATE INDEX [name] ON [schema.]table [USING btree]
  (key [COLLATE [schema.]collation] [[schema.]opclass]
    [ASC|DESC] [NULLS FIRST|LAST] [, ...]) [INCLUDE (column [, ...])]
CREATE INDEX IF NOT EXISTS name ON [schema.]table [USING btree]
  (key [COLLATE [schema.]collation] [[schema.]opclass]
    [ASC|DESC] [NULLS FIRST|LAST] [, ...]) [INCLUDE (column [, ...])]
CREATE INDEX [name] ON [schema.]table USING hash
  (key [COLLATE [schema.]collation] [[schema.]opclass])
CREATE INDEX IF NOT EXISTS name ON [schema.]table USING hash
  (key [COLLATE [schema.]collation] [[schema.]opclass])
element := column type [COLLATE [schema.]name] [[CONSTRAINT name] NULL | [CONSTRAINT name] NOT NULL | [CONSTRAINT name] DEFAULT default_expression | check_constraint]...
         | [CONSTRAINT name] NOT NULL column | check_constraint
         | LIKE [schema.]source [{INCLUDING | EXCLUDING} like_option]...
like_option := COMMENTS | COMPRESSION | CONSTRAINTS | DEFAULTS | GENERATED
             | IDENTITY | INDEXES | STATISTICS | STORAGE | ALL
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
INSERT INTO [schema.]name [AS alias] [(column [, ...])] VALUES (insert_value [, ...]) [, ...]
       [insert_conflict]
INSERT INTO [schema.]name [AS alias] [(column [, ...])] SELECT [projection_target [, ...]]
       [FROM from_list [WHERE logical_predicate] [GROUP BY logical_target [, ...]]
        [HAVING logical_predicate] [ORDER BY ordering_key [, ...]]] [query_bounds]
       [insert_conflict]
INSERT INTO [schema.]name [AS alias] [(column [, ...])] VALUES (value [, ...]) [, ...]
       [ORDER BY ordering_key [, ...]] [query_bounds] [insert_conflict]
INSERT INTO [schema.]name [AS alias] DEFAULT VALUES [insert_conflict]
insert_value := value | DEFAULT
insert_conflict := ON CONFLICT [conflict_target] DO NOTHING
                 | ON CONFLICT conflict_target DO UPDATE SET update_assignment [, ...]
                   [WHERE logical_predicate]
conflict_target := (column_or_expression [COLLATE collation] [opclass] [, ...])
                   [WHERE index_predicate]
                 | ON CONSTRAINT constraint_name
MERGE INTO [schema.]name [[AS] alias] USING joined_table ON expression
      merge_arm [merge_arm ...]
merge_arm := WHEN MATCHED [AND boolean_expression]
             THEN UPDATE SET update_assignment [, ...]
           | WHEN NOT MATCHED [BY TARGET] [AND boolean_expression]
                    THEN INSERT [(column [, ...])]
                         [OVERRIDING (SYSTEM | USER) VALUE]
                         VALUES (insert_value [, ...])
                  | WHEN NOT MATCHED [BY TARGET] [AND boolean_expression]
                    THEN INSERT DEFAULT VALUES
DELETE FROM [schema.]name [[AS] alias] [USING from_list] [WHERE delete_predicate]
       [RETURNING [WITH (returning_alias [, ...])] projection_target [, ...]]
returning_alias := (OLD | NEW) AS name
SELECT [projection_target [, ...]] FROM from_list
       [WHERE logical_predicate] [GROUP BY logical_target [, ...]]
       [HAVING logical_predicate] [ORDER BY ordering_key [, ...]]
       [query_bounds] [locking_clause ...]
locking_clause := FOR (KEY SHARE | SHARE | NO KEY UPDATE | UPDATE)
                  [OF unqualified_relation_or_alias [, ...]] [NOWAIT | SKIP LOCKED]
query_bounds := [OFFSET scalar_expression [ROW | ROWS]]
                [LIMIT scalar_expression | LIMIT ALL
                 | FETCH (FIRST | NEXT) [count] (ROW | ROWS) ONLY]
                -- OFFSET and LIMIT/FETCH may reverse; the entire locking list may precede bounds.
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

Selected current-time ordinary scalar calls are `now()`,
`transaction_timestamp()`, `statement_timestamp()`, `clock_timestamp()` and
`timeofday()`, including `pg_catalog` qualification. They take no arguments or
precision modifiers. The first four return TIMESTAMPTZ; the last returns TEXT.
Transaction aliases are STABLE, statement time is STABLE within one execution,
and wall-clock/timeofday calls are VOLATILE at demand. Existing scalar casts,
comparisons, DML, closed selected defaults, CHECK and views compose them.
Aggregate/window decorations are rejected, not implemented capabilities.
See [time semantics and presentation](DIALECT.md#transaction-and-statement-time--change-note).


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
temporary/system destinations and automatic row types are not added. B-tree/hash
indexes, including expression definitions, move with their table.
See the [dialect boundaries](DIALECT.md) for independently tested PostgreSQL-only
controls. Smoke: `make check RUN='^TestAlterSchemaSmoke$'`.

No-FROM projections use the ordinary local one-row input and existing scalar
argument rules, without a selected-call witness or an implicit relation namespace.
Random calls also use existing table-backed SELECT and INSERT SELECT projection
paths. Bounded random resolves
INTEGER, BIGINT or NUMERIC, not floating-point bounds; normal and seed arguments
coerce implicitly to DOUBLE PRECISION. Numeric literals and typed table columns,
plus target-context bindings, supply these types without expanding the existing
typed-literal or general explicit-cast boundaries.
Random calls are excluded from DEFAULT, INSERT VALUES, WHERE and settings.
See [random behavior and diagnostics](DIALECT.md#random-functions--change-note).

Selected string expressions use ordinary table and no-FROM queries with
existing scalar children and nested selected calls. The local input also admits
`SELECT 1` and existing arithmetic, but adds no function families.
Unquoted `normalize` uses a form keyword; qualified or quoted ordinary calls
accept a TEXT form value. SQL TRIM lowers to btrim/ltrim/rtrim, not a `trim()`
function alias. See [core strings](DIALECT.md#core-string-functions--change-note).
The four unary case calls return TEXT with inherited collation. Scalar/column
COLLATE supports default/C/POSIX, pg_c_utf8/pg_unicode_fast and registered user
collation identities; see the core
string rules for simple/full Unicode16 mapping, PostgreSQL18.0's original-input
sigma-prefix boundary and the Unicode ILIKE boundary. Stored expression/CHECK
deparsing retains TEXT conversion and required identifier quoting; its returned
compact/pretty SQL can be executed.


The FROM productions above are precedence notation: explicit joins bind tighter
than comma lists and associate left; parentheses preserve boundaries. ON sees
only its operands. Aliases hide original qualifiers; a whole-join alias hides
operand qualifiers. A special USING AS alias exposes only merged keys without
hiding operands and admits no column-alias list. Query qualification applies to
SELECT/INSERT SELECT, UPDATE FROM and DELETE USING consumers. Mutation RETURNING
exposes target/OLD/NEW and visible FROM/USING sources. DELETE USING WHERE uses query
qualification; the no-USING DELETE qualification guard remains unchanged.
Raw guards remain at DEFAULT, VALUES and settings-call scalar entry points. Previously
admitted conditional arguments
retain their consumer diagnostics, including metadata-only utility Prepare
followed by DEFAULT validation at execution. ORDER keys use the shared
expression/output-reference syntax above; qualified and compound expressions
resolve through the input namespace, while bare output labels take priority.
No-FROM SELECT has no same-level relation namespace. Qualified references must
resolve a legal enclosing query frame or retain ordinary missing-entry diagnostics;
an INSERT target is not part of its SELECT source namespace.
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


## Materialized views — change note

```sql
CREATE MATERIALIZED VIEW [IF NOT EXISTS] name [(column_names)]
  AS query [WITH [NO] DATA];
REFRESH MATERIALIZED VIEW name [WITH [NO] DATA];
ALTER MATERIALIZED VIEW [IF EXISTS] name RENAME TO new_name;
ALTER MATERIALIZED VIEW [IF EXISTS] name RENAME [COLUMN] old_name TO new_name;
ALTER MATERIALIZED VIEW [IF EXISTS] name OWNER TO role;
ALTER MATERIALIZED VIEW [IF EXISTS] name SET SCHEMA schema;
ALTER MATERIALIZED VIEW name [NO] DEPENDS ON EXTENSION extension;
DROP MATERIALIZED VIEW [IF EXISTS] name [, ...] [RESTRICT | CASCADE];
```

Permanent SELECT/VALUES producers reuse the ordinary typed query pipeline.
Default/WITH DATA executes once and atomically publishes the complete stored
bag; WITH NO DATA analyzes without evaluating the producer. Prepare publishes
nothing. Prefix column aliases, frozen star expansion, output types/typmods,
nullable columns, ownership and dependency identities are retained.
Frozen analysis captures literal, column/frame and namespace occurrences across
the existing SELECT/VALUES, join, grouping, window, set-operation and limit
producers. This capture is independent of parameter policy: typed bound
parameters still fail0A000 after analysis, before any definition publication.
Population evaluates with operation-local `pg_catalog,pg_temp` search_path,
after binding sources in the caller's original namespace. Caller settings
are unchanged, including after failure.

Reads require the materialization's privileges, not renewed source privileges,
and do not evaluate its definition. Unpopulated demanded reads fail55000 with
`materialized view "<name>" has not been populated` and the PostgreSQL REFRESH
hint. REFRESH default/WITH DATA reruns the frozen definition and atomically
replaces the complete stored bag; repeated refresh never appends rows. WITH NO
DATA clears storage without running or locking the producer's sources. REFRESH
has affected count zero and no result columns. Protocol Prepare is syntax-only;
execution reacquires the target and current MAINTAIN authority. Population runs
as the MV owner with restricted search_path, while SESSION_USER and caller
settings are unchanged. Native eager cursors retain their captured old rows and
metadata across DATA and NO DATA. Producer failure/cancellation before publication
preserves prior rows/population. Valid-owner INSERT/UPDATE/DELETE fail42809
`cannot change materialized view "<name>"`; semantic, ACL and read-only errors
retain their ordinary precedence. Row-lock requests fail42809.
Grouped DROP preflights every target before one publication. RESTRICT/CASCADE
retain ordinary frozen-definition dependency behavior without dropping sources.

Selected metadata ALTER returns affected0/no columns and retains stored rows,
population, frozen definition, attributes, rowtype and rewrite identities.
Protocol Prepare is syntax-only; Execute takes ACCESS EXCLUSIVE and rechecks
grant-time owner authority. IF EXISTS applies only to rename/owner/schema, not
dependency forms. OWNER TO accepts CURRENT_USER, CURRENT_ROLE and SESSION_USER
and atomically substitutes owner ACL authority on the MV, attached indexes and
owned rowtype/array. SET SCHEMA moves the complete group with collision preflight.
PostgreSQL's materialized column-rename callback also admits ordinary tables,
views and indexes; renaming an index label does not change its bound parent key.
Other selected forms require a materialized target; ordinary ALTER VIEW stays
view-only. MAINTAIN is insufficient for metadata ALTER.

Existing CREATE [UNIQUE] INDEX forms accept materializations and retain real
parent attribute bindings, INCLUDE payload and current-name deparse. Refresh
rebuilds logical roots from the candidate stored rows; uniqueness failure retains
the complete predecessor. WITH NO DATA clears roots without evaluating sources.
DEPENDS/NO DEPENDS resolves the genuine installed unaccent provider even for
duplicate/absent edges; absent providers fail42704. Immutable whole-object
AUTO_EXTENSION edges project as pg_depend deptype='x', distinct from ordinary
definition references and provider members. Provider retirement through DROP
SCHEMA automatically retires attached MVs, but ordinary dependents of those MVs
still block RESTRICT. DROP MV never retires the provider.

No runtime parameters, temporary MV/source, OR REPLACE, other MV ALTER forms,
MV storage options/access methods/tablespaces, second extension provider or DROP
EXTENSION are added. Existing producer and explicit-transaction utility exclusions
remain. See [lifecycle and persistence](TABLES.md#materialized-views--change-note).
Nonconcurrent refresh takes target ACCESS EXCLUSIVE and producer ACCESS SHARE
locks. Native explicit-transaction refresh retains the utility0A000 boundary;
read-only25006 precedes target lookup. PostgreSQL transactional refresh/rollback
is a separately tested server behavior. CONCURRENTLY is an explicit native0A000
capability boundary, not a successful no-op. WAL, heap/relfilenode/index swap,
crash durability and pg_dump remain external PostgreSQL mechanisms.

## Views

`CREATE [OR REPLACE] [TEMP | TEMPORARY] VIEW` stores an immutable, identity-bound definition analyzed by the
ordinary SELECT binder. The body admits the current scalar/filter/group/HAVING/
expression and multi-key ordering repertoire and table, sequence, view and
bounded catalog sources. It is not executed by CREATE or Prepare; subsequent reads evaluate it
against current visible data. Explicit names rename an output prefix; fewer
names than outputs are legal, more fail42601, and duplicate final names fail42701.
Unknown-only outputs become TEXT. Creation-time `*` expansion and output types
are fixed; zero-column views retain real source occurrences and count semantics.
CREATE has affected count zero and no result columns or runtime parameters.

### Temporary views — change note

Explicit TEMP/TEMPORARY views live in the existing physical-session temporary
namespace. An ordinary view referencing a temporary relation anywhere in its
bound query becomes temporary automatically, even for unused or empty sources,
nested views, joins, derived queries, expression subqueries or set operands.
Conversion emits exactly one NOTICE00000 `view "<name>" will be a temporary view`
with no Detail/Hint/Position, before destination/collision rejection. Explicit
TEMP and ordinary creation in own pg_temp over persistent-only sources are quiet.
SELECT analysis and excessive output aliases fail before conversion. After the
inference notice, output-collation validation precedes destination resolution.
Once the destination/target is resolved, fresh CREATE validates options before
duplicate output names; an existing replacement checks output names/prefix
before options. These checks share one execution binding and never mutate raw SQL.
Expression-only TEMP regclass/nextval dependencies do not infer TEMP in PostgreSQL18.
Native continues to reject nonportable persistent TEMP-sequence dependencies with
0A000 `temporary views are unsupported`; explicit/inferred TEMP definitions admit
same-physical-owner sequence dependencies without evaluating them at CREATE.

Owner TEMP lookup shadows permanent names, subject to explicit search_path
positioning. Own pg_temp/actual TEMP destinations are allowed; a non-TEMP
destination fails42P16 `cannot create temporary relation in non-temporary schema`.
Unqualified peer access fails42P01 and genuinely unauthorized foreign schema access
fails42501. Native qualified peer-TEMP lookup retains its42501 boundary; PostgreSQL
superuser catalog/qualified view access is not a promise of hidden catalog facts.
Two sessions may independently define the same TEMP view name.

The ordinary dynamic evaluator and logical metadata/ACL/dependency rules apply.
Views have no physical row storage or ON COMMIT action. DELETE/PRESERVE base actions
leave their definitions dynamic; real base DROP/CASCADE removes the transitive
view/rule/rowtype closure. Physical close removes owner TEMP, pooled return does not.
CREATE/replace/ALTER/DROP VIEW inside an explicit native transaction remain0A000;
read-only CREATE retains25006 before source/destination analysis.
These execution exclusions do not reject CREATE VIEW at Prepare: utility Describe
has no source analysis or result signature, including inside read-only/explicit blocks.

Native persistent Dump excludes all TEMP definitions and associated artifacts.
Reset/Restore retains exact live TEMP-only chains and builtin generations used
throughout their query trees, not just output columns. Unchanged pinned catalog
providers retain exact predecessor authority; invalidated persistent table/view
providers reject replacement atomically with ordinary native control errors.
Retained catalog providers include every column's builtin generation, even unused
columns, so repeated replacement keeps the certified immutable provider itself.
Same-name imports never repair a lost provider. Explicit TEMP/own-TEMP Restore
definitions fail0A000 `restore cannot stage non-retractable effects` before target
publication. Held prepared reads and already-returned eager rows retain their
normal logical metadata/snapshot contracts. No general old-epoch reference waiver
is introduced.
Existing automatically-updatable TEMP views retain their supported INSERT/UPDATE/
DELETE mappings across lawful replacement, including independent stored subquery
sources. Their writes still use current ACLs and the exact retained base identities.

Smoke: `make check RUN='^TestReferenceTemporaryViewFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceTemporaryViewFeatureSmoke$'`.


### Replacement and alteration — change note

`CREATE OR REPLACE VIEW` creates an absent target or replaces an existing view
atomically. The view and `_RETURN` rule keep their OIDs, owner, ACLs, comments,
existing attribute identities and inbound dependents. The new definition replaces
all outgoing dependencies; a former source is no longer retained merely because
an older definition used it. Existing outputs must be a positional prefix with
the same names, type identities, typmods and collation identities. Trailing
columns may be appended; dropping, renaming, reordering or changing the type or
collation of existing columns fails42P16 without publication. Scalar COLLATE
annotations support registered user descriptors and executable seeded identities
in projections, predicates and ordering; identity is separate from the immutable
provider capability, not value conversion. Existing dependent views retain their frozen
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
Prepare has zero utility metadata and no body binding, source locks, namespace
allocation, publication or notices. Semantic CREATE VIEW checks occur at Execute.
Prepared reads rebind their original SQL.
A same-shape replacement is visible on the next execution; appended outputs
make a held SELECT * fail0A000 `cached plan must not change result type`.
Already-returned eager rows retain their prior values and metadata.
SQL PREPARE utility DDL is not added. CREATE/replace/ALTER/DROP VIEW retain the
native explicit-transaction exclusion and read-only utility tags.


A legal scalar definition may store repeated adjacent FOR KEY SHARE/FOR SHARE/
FOR NO KEY UPDATE/FOR UPDATE clauses with per-clause OF and NOWAIT/SKIP LOCKED.
CREATE remains row-lock-free; immutable occurrence targets and policies survive
identity remapping, current-alias deparse, Dump and Restore. Ordinary reads
enforce stored marks only on selected physical occurrences in that subtree.
Inherited/stored strength and wait merge independently; strongest mode and
NOWAIT win independently, not as a combined option pair. Unmarked aggregation
over a marked view locks qualifying input before grouping/HAVING, even if output
is empty. A clause reaching grouped/aggregate output is mode-specific0A000.
An enclosing clause reaching a nullable outer-join input rejects with
mode-specific0A000 on Execute after successful Prepare. Repeated clauses use the
normalized strongest mode for that nullable occurrence's diagnostic; OF target
insertion order, not physical table identity, chooses between illegal occurrences.
Unmarked outer queries
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
skipped/returned rows; an outer LIMIT0 does not produce an inner stage.
Inherited marks also settle before a bounded view's own OFFSET/count, so SKIP
refills its original candidate population rather than locking a pretruncated bag.
Plain scalar views still defer marks to enclosing qualification.
Source ACL and bound type errors remain mandatory for0/past-end results. Scalar
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
| `pg_catalog.pg_class` | `oid OID`, `relname NAME`, `relnamespace OID`, `relowner OID`, `relkind CHAR`, `relnatts INT2`, `relhasrules BOOL`, `relispopulated BOOL` |
| `pg_catalog.pg_rewrite` | `oid OID`, `rulename NAME`, `ev_class OID`, `ev_type CHAR`, `ev_enabled CHAR`, `is_instead BOOL` |
| `pg_catalog.pg_views` | `schemaname NAME`, `viewname NAME`, `viewowner NAME`, `definition TEXT` |
| `pg_catalog.pg_matviews` | `schemaname NAME`, `matviewname NAME`, `matviewowner NAME`, `tablespace NAME`, `hasindexes BOOL`, `ispopulated BOOL`, `definition TEXT` |
| `pg_catalog.pg_tables` | `schemaname NAME`, `tablename NAME`, `tableowner NAME`, `tablespace NAME`, `hasindexes BOOL`, `hasrules BOOL`, `hastriggers BOOL`, `rowsecurity BOOL` |
| `pg_catalog.pg_indexes` | `schemaname NAME`, `tablename NAME`, `indexname NAME`, `tablespace NAME`, `indexdef TEXT` |
| `pg_catalog.pg_sequence` | `seqrelid OID`, `seqtypid OID`, `seqstart INT8`, `seqincrement INT8`, `seqmax INT8`, `seqmin INT8`, `seqcache INT8`, `seqcycle BOOL` |
| `pg_catalog.pg_sequences` | `schemaname NAME`, `sequencename NAME`, `sequenceowner NAME`, `data_type REGTYPE`, `start_value INT8`, `min_value INT8`, `max_value INT8`, `increment_by INT8`, `cycle BOOL`, `cache_size INT8`, `last_value INT8` |
| `pg_catalog.pg_user` | `usename NAME`, `usesysid OID`, `usecreatedb BOOL`, `usesuper BOOL`, `userepl BOOL`, `usebypassrls BOOL`, `passwd TEXT`, `valuntil TIMESTAMPTZ`, `useconfig TEXT[]` |
| `pg_catalog.pg_roles` | `rolname NAME`, `rolsuper BOOL`, `rolinherit BOOL`, `rolcreaterole BOOL`, `rolcreatedb BOOL`, `rolcanlogin BOOL`, `rolreplication BOOL`, `rolconnlimit INT4`, `rolpassword TEXT`, `rolvaliduntil TIMESTAMPTZ`, `rolbypassrls BOOL`, `rolconfig TEXT[]`, `oid OID` |
| `pg_catalog.pg_group` | `groname NAME`, `grosysid OID`, `grolist OID[]` |

Each user view has one `_RETURN` rule (`ev_type='1'`, `ev_enabled='O'`,
`is_instead=true`) tied to its real relation OID. OIDs are engine-local, not
numeric replicas of an independent PostgreSQL cluster. `pg_views.definition`
and Dump share identity-aware reconstruction after renames; PostgreSQL pretty
printing byte equality is not promised. Unsupported columns, including
`reloptions`, `ev_action` and `ev_qual`, give ordinary42703; no fake pg_node_tree.
Built-in source output is derived, never persisted as table data.

### pg_user safe user view — change note

`pg_catalog.pg_user`, also available by ordinary unqualified catalog lookup,
exposes exactly the nine ordered legacy columns above. PUBLIC SELECT includes
all LOGIN roles, not just self or membership-reachable roles. NOLOGIN roles are
absent. Expiry, missing passwords and connection limit zero do not filter rows.
The four flags are direct stored role attributes, never effective inherited
privileges. `usesysid` is the real represented role identity, not row position.
`passwd` is non-NULL `********` for administrators and ordinary readers, including
after absent/replaced passwords. The provider never consults credential material.

`valuntil` is typed NULL when absent, otherwise the stored TIMESTAMPTZ instant or
infinity. Session timezone controls rendering at execution, not at Prepare.
`useconfig` is typed NULL TEXT[] for currently reachable roles: role-setting
persistence and ALTER ROLE SET/RESET are not implemented by this view. It is not
an empty array or fabricated settings. Native database type names are
NAME/OID/BOOL/BOOL/BOOL/BOOL/TEXT/TIMESTAMPTZ/_TEXT; all view columns have declared
nullable metadata and typmod−1. NAME and useconfig carry C collation950, passwd
default collation100, and remaining columns are noncollatable.

Each execution uses one saved immutable role root, shared with nested stored
views and repeated source openings. Held prepared reads refresh on later
executions; already-open native rows and descriptors remain detached across
ALTER, DROP/recreation and local Reset/Restore. Roles survive native controls,
while Dump emits local stored-view SQL, not cluster roles, credentials or
pg_user rows. `pg_views` classifies pg_user as a view with pinned release
definition text; pg_tables does not classify it as a table. Its metadata mention
of pg_shadow does not make pg_shadow executable.

Supported mutations undergo normal assignment/WHERE/RETURNING/parameter analysis
before rejection. INSERT/UPDATE modifying the resolved passwd column report
0A000 with `cannot insert into column "passwd" of view "pg_user"` or
`cannot update column "passwd" of view "pg_user"` and the constant-column Detail.
Other INSERT/UPDATE/DELETE report55000 naming pg_shadow, with PostgreSQL's
non-updatable-view Detail and operation-specific INSTEAD OF trigger/rule Hint,
even when WHERE is false. No new role utilities, settings or locking surface
is added. Independent public coverage and feature smoke use
`make check RUN='^TestReferencePGUser'`
and the same command with `REFERENCE=1`; each invocation has a900-second deadline.

### pg_roles safe role view — change note

`pg_catalog.pg_roles`, also available by ordinary unqualified catalog lookup,
exposes exactly the thirteen ordered columns above. PUBLIC SELECT includes every
represented role, including unrelated, NOLOGIN, privileged, expired and zero-limit
roles. PUBLIC itself is an ACL grantee, not a role row. Flags, including rolinherit,
are direct stored attributes: membership ADMIN/INHERIT/SET grants and revokes
never synthesize effective privilege flags. CREATE and ALTER are reflected on
the next execution of the same held prepared statement.

`rolpassword` is always non-NULL TEXT `********`, independent of credential absence,
format or replacement, for administrators and ordinary readers. The provider
never consults credential material. `rolvaliduntil` is typed NULL when absent,
otherwise the stored finite/infinite TIMESTAMPTZ instant; rendering uses execution
timezone rather than preparation timezone. Expiry does not hide roles.
`rolconfig` is typed NULL TEXT[] for currently reachable roles, not an empty array
or session-derived settings. Role settings and ALTER ROLE SET/RESET remain excluded.

Native database type names are NAME/BOOL/BOOL/BOOL/BOOL/BOOL/BOOL/INT4/TEXT/
TIMESTAMPTZ/BOOL/_TEXT/OID. All view columns have declared nullable metadata and
typmod−1. NAME and rolconfig use C collation950, rolpassword default collation100;
other columns are noncollatable. OID is the actual represented role identity.
Generated view relation identities are engine-local; existing row/array type
identities are linked to the real view, not pinned to another server's allocator.

Each statement uses one saved immutable role root across repeated and nested
source openings. Held prepared reads refresh; already-open native rows and
metadata remain detached across ALTER, DROP/recreation and Reset/Restore.
Native controls preserve cluster roles and membership while Dump emits actual
local stored-view SQL, not role DDL, credentials or virtual catalog rows.
`pg_views` includes pg_roles with the pinned PostgreSQL18.0 reconstruction;
`pg_tables` excludes it. Metadata references to pg_authid/pg_db_role_setting
do not make those catalogs executable.

Normal target/assignment/WHERE/RETURNING/parameter analysis precedes readonly
rejection. Valid INSERT/UPDATE/DELETE fail55000 naming pg_roles, including password
assignments and WHERE FALSE, with PostgreSQL's non-single-table/view Detail and
operation-specific INSTEAD OF trigger/rule Hint. Native virtual-catalog row locking
retains its explicit0A000 boundary. No new role utilities, settings or locks.
Feature smoke: `make check RUN='^TestReferencePGRolesFeatureSmoke$'`, then the same
command with `REFERENCE=1`; each invocation has an external900-second deadline.

### pg_group legacy group view — change note

`pg_catalog.pg_group` exposes exactly three ordered columns: `groname NAME`,
`grosysid OID` and `grolist OID[]`. Ordinary unqualified lookup and schema
qualification follow the existing search_path rules; qualification bypasses a
real shadow table. Rows are roles whose **current LOGIN attribute is false**,
not roles chosen by their creation spelling. CREATE ROLE/GROUP defaults NOLOGIN,
CREATE USER defaults LOGIN, and explicit overrides or ALTER determine visibility.
Other privilege flags, INHERIT, expiry and connection limits do not filter rows.
There are no extra role-flag or credential columns.

`grosysid` equals that role's actual `pg_roles.oid`. Every direct membership record
contributes its member's actual OID to `grolist`: LOGIN and NOLOGIN members count,
ADMIN/INHERIT/SET options do not filter, and independent grantors preserve duplicate
member OIDs. Option-only revocation retains the member; removing an origin removes
only its occurrence. Nested membership does not synthesize transitive elements.
Neither row order without ORDER BY nor array element order is promised. Zero
members means a non-NULL, zero-dimensional empty OID array `'{}'`, never SQL NULL
or `'{NULL}'`; nonempty lists have lower bound one and non-NULL OID elements.

PUBLIC SELECT and catalog USAGE disclose unrelated and privileged NOLOGIN roles
and their members to ordinary readers without a membership or privilege filter.
PUBLIC is an implicit ACL grantee, not a role/group row. Passwords and credential
verifiers are never consulted or projected. NAME uses C collation950; the other
columns are noncollatable. Database type names are NAME/OID/_OID, typmods are−1,
and native declared nullable metadata is true/known for all three columns.
Generated relation identities are local; existing row/array types link to the
actual view. `pg_class` reports relkind v/relnatts3, `pg_views` includes the exact
pinned PostgreSQL18.0 reconstruction, and `pg_tables` excludes the view.

One immutable captured role root serves every source opening in a statement,
including repeated aliases, nested stored views, derived sources and joins.
Direct and held prepared executions immediately observe role/edge publication;
already-open native rows, membership arrays and metadata retain the old capture
across GRANT/REVOKE, LOGIN changes, drop/recreation and Reset/Restore. A recreated
same-name group has a distinct identity; old-OID queries do not retarget it.
Native Reset/Restore preserve cluster roles and memberships; Dump omits role DDL,
credentials and virtual-row INSERTs while retaining symbolic stored consumers.
Actual emitted user SQL can replay against PostgreSQL with its own role identities.

Ordinary target/assignment/WHERE/RETURNING/parameter diagnostics precede native
write rejection. INSERT/UPDATE/DELETE, including WHERE FALSE, are excluded with
0A000 / `catalog source mutation is unsupported` and empty Detail/Hint. Native
catalog row locking remains0A000 / `row locking is not supported for virtual
catalog relations`. These are native capability boundaries, not fabricated55000
PostgreSQL parity: the PostgreSQL single-base-relation view has some updatable
base columns. No system-catalog writes, pg_authid/pg_auth_members exposure, new
role utilities, role settings or array syntax are added.
Feature smoke: `make check RUN='^TestReferencePGGroupFeatureSmoke$'`, then repeat
with `REFERENCE=1`; each invocation requires an external900-second deadline.

### Table and view summaries — change note

`pg_tables` and `pg_views` expose current real names, schemas and owners without
filtering underlying objects by search_path, schema USAGE or table/column SELECT.
The summary source's own normal SELECT authorization still applies. A denied
underlying read can report42501 while its summary remains visible. This matches
PostgreSQL's unfiltered views and does **not** change the intentionally stricter
native `pg_indexes` disclosure policy.

Ordinary user tables and stored views participate; sequences and indexes do not.
User tables have NULL default tablespace and false rule/trigger flags.
`rowsecurity` reports the actual enabled bit, independently of FORCE and policies.
`hasindexes` is sticky publication history:
UNIQUE backing, ordinary and concurrent initial index publication set it; dropping
the last index does not clear it. Supported rename/schema/column edits, view-owner
changes, real descriptor ownership replacement and REINDEX retain it; a newly
recreated table starts false. TRUNCATE preserves this history and all definitions;
native ALTER TABLE OWNER remains an0A000 boundary without metadata changes.
Failed ordinary expression
index builds before publication leave it unchanged; failed concurrent work after
initial invalid-index publication retains true. This adds no VACUUM/ANALYZE.

The represented builtin table inventory is `pg_cast`, `pg_extension`, `pg_sequence`, `pg_depend`,
`pg_description`, `pg_shdescription`, `pg_constraint`, `pg_attrdef`, `pg_class`,
`pg_rewrite`. Their released compatibility metadata has hasindexes=true and
hasrules/hastriggers/rowsecurity=false; tablespace is NULL except
`pg_shdescription`'s `pg_global`. These flags do not invent native system backing
indexes or physical storage. The represented builtin views are `pg_views`,
`pg_prepared_xacts`, `pg_indexes`, `pg_tables`, `pg_matviews`, `pg_user`, `pg_roles`,
`pg_group`, `pg_available_extensions` and `pg_available_extension_versions`.
Classification is local to these summaries, not a broader
`pg_class` schema/kind expansion or full catalog inventory.

User `pg_views.definition` reconstructs the actual immutable stored SELECT using
current identities and output labels, preserving frozen stars/aliases and without
evaluating the body. Its structural formatting is not a PostgreSQL whitespace
golden. Builtin definitions instead expose complete PostgreSQL18 compatibility
SELECT text sourced from the
[REL_18_0 system_views.sql CREATE VIEW sections](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/system_views.sql)
and pinned18.0 reconstruction. They may reference native-unimplemented catalogs
and functions and are **not promised executable in native**; in particular the
released `pg_indexes` text does not describe native's stricter ACL policy.

Neither summary exposes persistence, access method, OID or any physical size
metric: no physical approximation is introduced. Extra columns give ordinary
42703 PostgreSQL English diagnostics, including analysis on empty-result queries.
Native Dump persists owning objects, never summary rows. Logical Restore
normalizes history-only hasindexes=true to false when no index remains; live and
UNIQUE backing indexes restore true. Actual descriptor replacement retains history
and normalizes a supplied false flag to true when an index is represented, in its
private immutable candidate without changing retained source descriptors.
Captured native rows/descriptions remain immutable across mutation/Reset/Restore;
held statements see fresh metadata on each execution. Native catalog queries retain
the existing latest-committed plus own-overlay frontier rather than PostgreSQL's
repeatable-read catalog MVCC snapshot.

`pg_get_viewdef` accepts TEXT/OID alone, TEXT/OID plus BOOL, or OID plus INT4
wrap width; all return strict nullable TEXT and terminate the command with a
semicolon. Plain top-level VALUES remains VALUES regardless of pretty/width;
renamed VALUES outputs use a current-label SELECT projection and target layout.
TEXT is a relation name resolved against the current statement authority, never
a numeric OID shortcut. Returned forms can create dynamic view copies without
evaluating the original. Pretty/width affects presentation, not frozen identities,
current output labels, duplicates or NULLs. `pg_get_indexdef(oid)` and
`pg_get_indexdef(oid, integer, boolean)` reconstruct supported index descriptors;
column zero gives CREATE INDEX without a terminator, valid columns give only keys,
invalid nonzero columns give empty TEXT, and missing/wrong-kind OIDs give NULL.
See [deparse contracts](QUERIES.md#index-and-view-deparsing--change-note).

Owned sequence inquiry has exactly `pg_get_serial_sequence(text,text) -> text`.
Its strict STABLE provider returns the actual qualified, safely quoted AUTO or
INTERNAL sequence name for serial, manual OWNED BY or supported identity
metadata, and typed TEXT NULL for an unowned column.
Existing system attributes (`ctid`, `xmin`, `cmin`, `xmax`, `cmax`, `tableoid`)
on supported non-view relations are unowned and return NULL. An absent system name
on a view still raises42703; declared columns take precedence over this
relation-kind-aware existence check. No system-column projection is added.
A nextval default is neither necessary nor sufficient: changing/dropping the
default preserves association,
whereas OWNED BY NONE removes it. The first TEXT parses qualified relation names
and folds unquoted identifiers; the second is the literal case-sensitive column
name. A numeric first argument is a name rather than an OID. Matching
current-database qualification is accepted without supporting cross-database
lookup. Bare and pg_catalog-qualified calls share the sole signature;
`pg_get_owned_sequence` is not a SQL alias.

Argument binding precedes strict NULL behavior. Runtime name/schema/column
diagnostics have no offsets into embedded TEXT. Execution uses the captured
transaction-visible catalog and stable attribute identities, so a retained
statement observes ownership changes and preceding-column compaction correctly.
The inquiry does not draw numbers or infer ownership from generator/default
state. Nullable TEXT metadata is retained for empty/all-NULL results. Stored
views/defaults/CHECK remain symbolic across existing native Dump/Restore, which
preserves actual sequence names, ownership and future draws.
See [owned sequence introspection](DIALECT.md#owned-sequence-introspection--change-note).


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

### Automatically updatable view targets

INSERT/UPDATE/DELETE accept a simple view over one ordinary table or another
such view. Mapping follows creation-bound attribute identities, not current
column names or value matching. Logical view columns retain their namespaces,
parameter types and UPDATE/DELETE RETURNING metadata. Source aliases, including
the frozen-layout aliases emitted by native Dump and recreated by Restore, do
not change a single ordinary table/view source into a read-only shape. Existing
target aliases,
UPDATE FROM, DELETE USING, scalar subqueries, partial array assignments and
INSERT VALUES/SELECT/DEFAULT VALUES/ON CONFLICT DO NOTHING use the real base
producer. Every omitted physical column uses the base default, identity,
generation or typed NULL. Explicit NULL differs from DEFAULT.
The existing identity-order simple-view conflict-update producer participates
in completed-NEW check-option enforcement; this does not add general conflict
action remapping through reordered/computed view outputs.

Computed output assignment rejects 0A000 even for NULL/DEFAULT or an empty
producer. Different-type casts and explicit COLLATE are computed; a same-type
no-op cast remains writable. Repeated aliases are individually writable, but
assigning the same physical column twice rejects 42601 (except compatible
partial array composition). Mixed views remain writable; simple computed-only
and zero-column views allow DELETE. Restricted query shapes reject 55000 with
the operation-specific reason and trigger/rule hint, not arbitrary base writes.

UPDATE/DELETE qualify only rows satisfying every nested view WHERE and user
predicate; latest predecessors are rechecked after contention. INSERT may add
an invisible row and UPDATE may leave the view, returning its completed NEW
values. Unused computed projections are not demanded. Failure/cancellation
discards the whole private statement stage, including eager RETURNING output.
The authoritative base owns RX/row locks and constraints; views store no rows.

The caller needs logical view write rights and SELECT on referenced view
columns. Each ordinary layer checks its current owner's mapped base rights;
security_invoker uses the original caller even underneath an ordinary owner.
Main-source SELECT follows actual logical read demand: INSERT-only, literal
UPDATE and constant DELETE do not require it merely for a view-only predicate.
Independent stored subquery occurrences retain SELECT requirements under their
defining layer's current owner or original invoker, even when they read the same
physical relation as the main source. Child views retain their own authority and
applicable row marks. Demanded sources select live versions and own changes
before bridging frozen attribute layouts. Held statements bind those sources at
Prepare without executing ACL checks, then recheck current ACLs and mappings at
Execute. INSERT conversion preflight uses coherent logical destination columns;
physical completed-row constraints remain base-owned. Native Dump/Restore
serializes current definitions and committed base rows, rebuilding mappings
for subsequent writes; it does not serialize output/history or PostgreSQL
physical tuple metadata. INSERT RETURNING exposes logical view outputs with the same authority.

### View check options — change note

```sql
CREATE [OR REPLACE] [TEMP | TEMPORARY] VIEW name [(columns)]
  [WITH (security_barrier=boolean, security_invoker=boolean,
         check_option=local|cascaded)]
  AS query [WITH [LOCAL | CASCADED] CHECK OPTION];
ALTER VIEW name SET (check_option=local|cascaded);
ALTER VIEW name RESET (check_option);
```

The suffix and reloption are equivalent; specifying both is a duplicate option
error22023. Values match complete LOCAL/CASCADED strings case-insensitively;
quoted surrounding whitespace is not trimmed. Omitted suffix mode is CASCADED.
CREATE OR REPLACE uses its newly supplied options; omitting the mode removes it.
ALTER modifies only named options and failed/canceled DDL preserves the old mode.
Declaration requires a locally automatic shape with at least one direct writable
output; a simple outer view can declare a mode over a restricted inner view, but
later DML still rejects that inner restriction. CREATE validates this shape before
enum options; ALTER validates options first; replacement-prefix compatibility
precedes enum validation. Ordinary binding/type errors remain unchanged.

Every admitted finalized NEW row is checked after defaults/identity/stored
generation and base constraints/uniqueness, before RETURNING/publication. FALSE
and NULL reject44000 `new row violates check option for view "name"`; the entire
statement's private rows/output are discarded. Ordinary SELECT/DELETE, base writes
and preexisting invisible rows are unchanged. A conflict-update action checks its
final NEW row, not a discarded excluded proposal; WHERE false and DO NOTHING
produce no row to check. Empty UPDATE retains its logical empty RETURNING schema.
The diagnostic interpolates the stored view name literally between double quotes,
including embedded quotes or backslashes, rather than escaping it as a string.

LOCAL checks its own WHERE plus lower explicitly checked layers. CASCADED adds
all descendant WHERE predicates, even through a no-WHERE or LOCAL layer. Checked
layers run innermost first; the first failing view's current name is reported.
Predicates see physical NEW base attributes, including hidden defaults/generated
columns and correlated subqueries; stored sources see the statement snapshot and
own prestatement overlay, not partially proposed rows. Held execution reselects
current sources/modes and checks current owner/original-invoker ACLs.
Virtual generated predicate columns use the current destination recipe and row
layout, including after source-column renames or dropped/added columns. Independent
subquery generated columns retain their own SELECT occurrence semantics.

44000 Detail uses physical base column order and actual caller SELECT rights plus
modified columns: full `Failing row contains (values).`, partial
`Failing row contains (columns) = (values).`, or no Detail if no values can be
disclosed. Hidden owner-readable columns are not leaked. Portable object/position
fields remain empty for ordinary check failures; PostgreSQL C locations are not
fabricated. Logical counts, values, column names/types and UPDATE RETURNING remain
those of the requested view. pg_views.definition/ViewCopy expose SELECT only;
they do not prove check-mode persistence. Native Dump emits canonical check_option
reloptions and Restore remints the stored dependencies for future writes.

INSTEAD OF triggers, INSTEAD rules and BEFORE triggers are external PostgreSQL
rewrite-provider mechanisms. Oracle witnesses cover their distinct cutoff,
suppression and NEW-modification semantics; native definitions explicitly fail
without installing providers or changing base rows.

CREATE RECURSIVE syntax, ALTER VIEW column defaults, type-changing ALTER actions,
policy-expression enforcement, UDFs and optimizer introspection remain unsupported.
RESET of an absent check_option is harmless.
Public GRANT/REVOKE use the existing central relation/column ACL paths.
Smokes: `make check RUN='^TestReferenceView(Rows|Barrier|SmokeCatalogReplayControls)$'` and
`make check RUN='^TestReferenceViewReplace(Definition|Diagnostics|Prepared|SmokeCycleReplayControls)$'`;
dual-mode replacement union: `make check RUN='^TestReference(ViewReplace[A-Za-z0-9_]*|DropViewIndexWrongKind[A-Za-z0-9_]*)$'`;
same-definition oracle: `make check REFERENCE=1 RUN='^TestReference(ViewReplace[A-Za-z0-9_]*|DropViewIndexWrongKind[A-Za-z0-9_]*)$'`.
The true selection includes separately retained PostgreSQL-required companions.
The source union includes all18 original replacement functions and the
non-prefix DROP VIEW/index-kind consumer. Actual setup, typed relation/rule IDs,
direct/prepared/SAME held utilities, quiet absent-target Prepare and retained
layout/dependency/control witnesses remain beside recorded companions.
Read-only utility teardown rolls back before Conn.Close on failure as well as
success; no PostgreSQL host Reset/Restore is invented.


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
json114, pg_node_tree194, float4 700, float8 701, unknown705,
bpchar1042, varchar1043, date1082, time1083,
timestamp1114, timestamptz1184, interval1186, timetz1266, numeric1700,
regclass2205, void2278 and uuid2950. Represented `_name`1003 has no description.
No pg_toast namespace, routine descriptor inventory, database/tablespace shared
model or COMMENT ON TYPE support is added. The separate readonly description
projection below includes represented collation/conversion release seeds without
widening the inquiry catalog strings or the legacy lookup candidate classes.

Fresh-baseline Reset restores builtin namespace descriptions; Dump omits
unchanged seeds but records overrides and explicit NULL/empty removals.
Restore replays those differences atomically while retaining target shared role
comments. See [comment mutation and native controls](TABLES.md#object-comments)
for the remaining DDL/transaction boundaries.

Feature stream: `make check RUN='^TestReferenceCommentInquiry$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceCommentInquiry$'`.
Dual-mode native union: `make check RUN='^TestReferenceCommentInquiry[A-Za-z0-9_]*$'`;
pinned independent reference: `make check REFERENCE=1 RUN='^TestReferenceCommentInquiry[A-Za-z0-9_]*$'`.
Captured old table/shared comments and fresh same-object inquiries preserve actual
catalog identity, not a hardcoded PostgreSQL bootstrap roleOID.

### pg_description catalog — change note

`pg_catalog.pg_description` (relation OID2609, namespace11) uses the existing
normal SELECT/direct/prepared/stored-view pipeline. Its four columns, in order:

| Column | Type | Typmod | Collation | Native nullability |
|---|---|---|---|---|
| objoid | OID26 | -1 | 0 | known NOT NULL |
| classoid | OID26 | -1 | 0 | known NOT NULL |
| objsubid | INT4 23 | -1 | 0 | known NOT NULL |
| description | TEXT25 | -1 | C950 | known NOT NULL |

Column origins are relation2609/attributes1–4. pq exposes unknown nullability.
Virtual-source NOT NULL is declared result metadata, not a fabricated physical
constraint inventory. Descriptor validation retains type/identity/live-attribute
and comment invariants; physical tables still require matching NOT NULL records.
There is no row `oid`; selecting it gives42703 `column "oid" does not exist`.
The identity key is `(objoid,classoid,objsubid)`: class1259 for tables/sequences
and their supported columns,2615 schemas,1247 types,3456 collations,2607
conversions. Whole objects use subid0; columns use stable live attribute IDs,
not dense output positions. Empty/NULL COMMENT removes the row; whitespace is
retained. Numeric OIDs may collide across classes. Unknown/wrong-class/subobject
addresses produce empty results, not errors. Without ORDER BY results are bags.

The fresh native catalog has163 exact PostgreSQL18.0 description seeds:
2 namespaces,26 represented scalar types,7 collations and128 conversions.
Collation identities100/950/951/962/963/811/6411 and conversion identities
4402–4531 except4450/4451 use exact release `descr` literals, not generated names.
Represented arrays, uncommented user types and unsupported objects have no
invented descriptions. Real PostgreSQL contains many more objects;163 is a
native represented-inventory count, not a whole-server reference expectation.
The reference fixture recreates public, so its current namespaceOID is discovered
rather than pinned to the fresh native bootstrap2200.

PUBLIC SELECT exposes numeric descriptions without target SELECT/schema USAGE.
Fresh private schemas/tables already deny PUBLIC USAGE/SELECT; the reference
witness uses these defaults, not unsupported privilege-mutation setup.
Shared ROLE/DATABASE/TABLESPACE descriptions are excluded; supported shared role
inquiries remain unchanged. COMMENT target support is not expanded, nor are
`obj_description` catalog routing and the legacy candidate inventory.
Native catalog writes fail0A000 `catalog source mutation is unsupported`;
row locking fails0A000 `row locking is not supported for virtual catalog relations`.
These are explicit virtual-source limits, not claims about physical PostgreSQL
superuser catalog operations.
Invalid bound OID text reports22P02 with the full portal Context
`unnamed portal parameter $1 = '...'` on both current adapters.

Each statement shares its immutable comment frontier with inquiry expressions,
including nested view providers. Already returned Rows retain captured text
across comment replacement/drop/native controls; held prepared execution captures
fresh metadata next time. Sequence transactional/private metadata uses the
existing overlay. TEMP sequence name/access authority remains session-private,
but numeric description metadata is visible to peers; catalog reading does not
open the described sequence. Native Dump excludes TEMP objects and their comments.
Dump emits owning COMMENT statements, never projected rows or unchanged seeds.
Reset restores163 baseline descriptions; Restore replays named
local comments atomically and preserves shared roles. After dropped columns,
logical Restore reconstructs declarations and can remint survivor attribute3 as2;
old allocator OIDs and historical attribute gaps are not promised round-trip.

Feature smoke in both modes:
`make check RUN='^TestReferencePGDescriptionFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferencePGDescriptionFeatureSmoke$'`.
Complete changed reference selection:
`make check RUN='^TestReference(PGDescription|CommentInquiryBuiltins)'`,
once in quick mode and once with `REFERENCE=1`, each under900 seconds.

### pg_shdescription catalog — change note

`pg_catalog.pg_shdescription` is the readonly shared-comment source, relation
OID2396 in namespace11, with exactly these ordered fields:

| Column | Type | Typmod | Collation | Native nullability |
|---|---|---|---|---|
| objoid | OID26 | -1 | 0 | known NOT NULL |
| classoid | OID26 | -1 | 0 | known NOT NULL |
| description | TEXT25 | -1 | C950 | known NOT NULL |

Origins are relation2396/attributes1–3; pq nullability is unknown. There is no
row `oid` or `objsubid`. The key is `(objoid,classoid)`, not an object name.
Current supported shared commands produce ROLE comments only: objoid is the
actual principal identity and classoid is1260 (`pg_authid`). Shared roles remain
separate from database-local `pg_description`; local table/column/schema/
sequence and builtin type/collation/conversion descriptions never appear here.
No DATABASE/TABLESPACE command, role catalog, shared allocator or physical
cluster storage is added.

Fresh native roles have no invented comments, so this source initially has
zero rows. PostgreSQL can have genuine shared bootstrap descriptions; there is
no whole-server row-count parity promise. COMMENT replacement changes one row,
NULL/empty text removes it, whitespace is retained, DROP removes it, and
same-name recreation obtains a distinct identity without inheriting text.
Principal storage remains uint64: only exactly representable uint32 identities
are projected, with no truncation or high/low identity alias.

PUBLIC SELECT permits ordinary readers to inspect comments without COMMENT
authority. Direct/prepared execution, views and relational consumers share the
existing query pipeline. Projection and `shobj_description(objoid,'pg_authid')`
use the same immutable statement role snapshot, including nested provider
openings; `obj_description(objoid,'pg_authid')` remains NULL. Returned rows and
saved metadata survive replacement/removal/drop; a held statement captures fresh
shared comments on its next execution. This does not introduce role MVCC.

Unknown columns and malformed OIDs retain normal SQLSTATE/English diagnostics.
The `objsubid` error additionally suggests `pg_shdescription.objoid` on both
current adapters with the same complete spelling Hint.
Native catalog mutations and row locking retain their existing0A000
virtual-source limits. Shared reads work in transactions, but native writable
transaction ROLE COMMENT, including semantic Prepare, remains0A000 and aborts
the block (subsequent25P02). Read-only Execute remains25006; PostgreSQL instead
supports transactional shared COMMENT commit/rollback.

Native Reset and Restore preserve shared roles, identities and comments outside
local replay. Dump emits local definitions/comments, not ROLE comments or
synthetic shared-catalog INSERTs. Restore rejects shared COMMENT targets with
0A000 `restore accepts only database-local COMMENT targets`; failed/canceled
controls leave prior shared/local state intact. Already returned snapshots stay
usable across successful replacement.

Feature smoke:
`make check RUN='^TestReferencePGShDescriptionFeatureSmoke$'`
and the same command with `REFERENCE=1`. Complete reference selection:
`RUN='^TestReferencePGShDescription'`, both modes under separate900-second
deadlines. Role identities are learned independently from a helper relation's
owner, never copied from the catalog under test.


Catalog visibility's seven-DDL boundary corpus retains the original SQL while
respecting incoming domain support: exact CREATE DOMAIN d AS integer succeeds
through Query with an empty utility result, then DROP and quiet Prepare prove
no early publication (missing type42704/Position12). SAME held Execute returns
count0, and typed INT4/BOOL consumers read7/true. Both source and recorded phases
retain all six other native DDL negatives and the default-function exclusion,
with independent PostgreSQL outcomes. This witness adds no adjacent capability.



### pg_sequence catalog — change note

`pg_catalog.pg_sequence` is a readonly normal query source (relation OID2224,
namespace11), also available through ordinary unqualified catalog resolution.
Its eight columns appear in the order listed above. All are known NOT NULL,
typmod -1 and collation0; native origins are relation2224/attributes1–8,
including empty results. pq reports unknown nullability. The two OID columns
export native int64/pq decimal bytes; five option columns export int64 and
`seqcycle` exports bool. `seqtypid` is the sequence's SMALLINT21, INTEGER23 or
BIGINT20 identity, not the type of the five always-INT8 option columns.

Each represented standalone, serial-owned or identity-owned sequence has one
definition row. `seqrelid` links to its `pg_class` OID; rename, schema move and
owner change preserve that identity, while drop/recreate obtains a new identity.
Missing numeric OIDs, table OIDs and NULL predicates yield no rows. Unordered
results are bags. There is no row `oid`, `last_value`, `is_called` or physical
counter. Selecting nonexistent columns raises42703 with the ordinary English
diagnostic and character position.

Rows read immutable definition options, never generators or reserved caches.
START is configured `seqstart`; nextval, setval, currval and RESTART do not alter
it. ALTER START and other option changes project the existing DDL authority
exactly, including AS type-extreme bounds and retained explicit bounds. Even
CACHE9223372036854775807 is readable without a draw or proportional allocation.
PUBLIC catalog SELECT does not require schema/sequence privileges on the
represented object. Other-session TEMP definition rows are visible by numeric
OID without opening its generator or recording physical temporary access.

Read committed executions, including held prepared statements, capture fresh
definitions. Repeatable read retains committed definitions and the row bag at
the existing transaction snapshot acquisition point; current own CREATE/ALTER/
DROP overlays remain visible, and savepoint rollback restores the private
overlay without discarding the transaction snapshot. The pinned oracle observes
Prepare inside BEGIN acquiring this snapshot (start15/cache9 retained after a
peer ALTER), whereas Prepare before BEGIN leaves the first execution to capture
start21/cache4. Serializable uses its existing snapshot/candidate lifecycle.
The sequence catalog and sequence view consume the retained catalog pointer;
other catalog and Describe/Execute timing policies are unchanged.

Already-returned cursors keep their captured row and fixed schema after
ALTER/DROP/native replacement. Native Dump emits actual sequence definitions,
not virtual catalog INSERTs; Reset empties their rows and Restore remints
relational identities while reproducing definition properties. Failed/canceled
operations retain existing atomicity. Native catalog writes and row locking
remain0A000 virtual-source boundaries, not PostgreSQL physical catalog mutation.
No additional `pg_attribute`/`pg_depend` expansion is implied.

Feature smoke: `make check RUN='^TestReferencePGSequenceFeatureSmoke$'`,
and the identical command with `REFERENCE=1` for pinned PostgreSQL.

### pg_sequences view — change note

Readonly relation12048 has row type12050/array12049 and the eleven ordered fields
listed above. NAME carries C collation; data_type is genuine REGTYPE, presented
as bigint/integer/smallint. View columns have native known nullable metadata,
typmod-1 and stable origins. There is no public oid/log_cnt/is_called.

Definitions match CREATE/ALTER and use the existing sequence snapshot plus own
DDL/savepoint overlay. Private schemas and unreadable sequences retain rows;
own physical TEMP appears, peer TEMP does not, independent of schema spelling
or superuser. Persistent same-name sequences are not shadowed in this view.
The current exact-identity helper exposes logical reserved high-water, not
currval or durable disk/WAL state. CACHE10 A.next1 exposes10, B.next11 exposes20,
A.cached2 still exposes20. Fresh, setval(false) and RESTART are NULL until called;
setval(true) exposes its value without nextval. ALTER START affects definition
only. SELECT or USAGE permits last_value; UPDATE alone leaves it NULL.

Every admitted provider row takes transaction-owned RowExclusive on its sequence
before readability/called checks, including definition/count, stored-view and
join consumers. Safe definition qualifiers precede completion, including through
ordinary non-CTE derived direct-column projections; FALSE and rejected names take
none. Admission does not cross LIMIT, aggregate, DISTINCT, set-operation or CTE
boundaries, computed projections, or unknown/volatile expressions; last_value
predicates remain after completion. Reads are allowed in read-only transactions
and never allocate, consume cached numbers or define currval/lastval. Prepare and
Rows.Next do not invoke the helper. Returned rows/types remain frozen through
nextval, DDL and native controls; Rows/Stmt.Close does not end explicit transaction
locks.
RR retains joined definitions but reads live generator/ACL state. A peer-dropped
original identity yields XX000 `could not open relation with OID <original>`;
the next command sees ordinary25P02, never a recreated-name substitution.

INSERT/UPDATE/DELETE fail55000 during Prepare with readonly-view diagnostics.
FOR UPDATE succeeds through represented expanded-view semantics without
inventing physical catalog tuple locks. SQL marks remain available for binding,
admission and clause validation; effective physical marks exclude only the
pg_sequences occurrence, including through stored/derived views and joins.
Ordinary table occurrences in the same query retain their actual row locks and
UPDATE authorization checks. Admitted helper RX remains independent; FALSE
does not invoke it.
Dump serializes canonical persistent definitions/setval, not view rows; Reset/
Restore preserve destination exact TEMP identity/state. Invalid or canceled
Restore remains atomic. No disk/WAL/standby emulation is provided.

Feature proof: `make check RUN='^TestReferencePGSequences'` and the identical
selector with `REFERENCE=1`; see [test contract](TESTING.md#sequence-value-view).

## pg_extension catalog — change note

`pg_catalog.pg_extension` is readonly relation3079 in namespace `pg_catalog`11,
owned by bootstrap role10, with PUBLIC SELECT. Ordinary direct/prepared queries,
views, joins, filters and aggregates use the normal catalog source pipeline.
Its eight ordered columns have typmod-1 and origins relation3079/attributes1–8:

| Column | Type (OID) | Native nullable | Collation |
|---|---|---|---|
| oid | OID (26) | no | 0 |
| extname | NAME (19) | no | C950 |
| extowner | OID (26) | no | 0 |
| extnamespace | OID (26) | no | 0 |
| extrelocatable | BOOL (16) | no | 0 |
| extversion | TEXT (25) | no | C950 |
| extconfig | OID[] (1028; `_OID` metadata) | yes | 0 |
| extcondition | TEXT[] (1009; `_TEXT` metadata) | yes | C950 |

Nullability is known natively even for empty results. `extconfig` is OID[],
not REGCLASS[]. NULL configuration arrays mean no configuration entries, not
empty arrays. Versions are arbitrary text, not parsed semantic versions.
Extension names identify database-wide extension objects; `extnamespace` denotes
their exported objects' schema, not placement of the extension object itself.

**Fresh native installed state is truthfully empty.** Registered `unaccent`
installation publishes real descriptors, projected by the single live catalog
source. There is no PL/pgSQL interpreter; available files and existing type
identities do not install extensions. PostgreSQL18.0's default database instead
contains installed `plpgsql`: generated positive row OID, owner10, schema11,
nonrelocatable, version `1.0`, NULL extconfig/extcondition. Native deliberately
does not fabricate this tuple. Arbitrary package loading, module loading
and ALTER/DROP EXTENSION remain outside support.
See [the PostgreSQL18 catalog contract](https://www.postgresql.org/docs/18/catalog-pg-extension.html).

Normal relation resolution, REGCLASS and explicit search_path shadowing apply;
qualification remains stable when a user table is named `pg_extension`.
Relation3079 and namespace11 are fixed PostgreSQL identities. Existing native
rowtype10074/array10073 are reused with reciprocal ownership; those type numbers
are allocated pristine-initdb inventory identities, not release-fixed OIDs or
cross-server allocator promises. No physical catalog indexes, TOAST or storage
are introduced.

Native writes fail with0A000 / `catalog source mutation is unsupported`, even
for zero-match UPDATE/DELETE; row locks fail with0A000 /
`row locking is not supported for virtual catalog relations`.
These are not PostgreSQL superuser physical-catalog write/lock semantics.
The command diagnostics below use ERROR/ERROR, with native optional fields absent
except the stated Position:

| Native command | SQLSTATE / primary | Position |
|---|---|---|
| CREATE EXTENSION pgext_missing_control | 0A000 / extension "pgext_missing_control" is not available | absent |
| ALTER EXTENSION pgext_missing_control UPDATE | 0A000 / statement is unsupported | 1 |
| DROP EXTENSION pgext_missing_control | 0A000 / statement is unsupported | 1 |
| LOAD 'pgext_missing_library' | 42601 / syntax error at or near "LOAD" | 1 |

Missing-provider CREATE parses and prepares successfully on both backends, then
fails at execution with0A000 / `extension "pgext_missing_control" is not available`.
PostgreSQL additionally supplies the pinned Hint
`The extension must first be installed on the system where PostgreSQL is running.`
with no Detail or Position; native has no system-file availability or fabricated Hint.
The other unsupported commands above fail at direct execute or prepared prepare.
Shared semantic errors bind even when the source is empty: missing columns
give42703; invalid/range-exceeding OID literals give22P02/22003.

Dump serializes actual registered persistent installations once before their
consumers, never catalog tuples. Native ownership metadata preserves checked
extension/member principals. TEMP installations are not portable Dump input.
Reset clears persistent installed state; Restore reconstructs generation-owned
descriptors from actual input while destination-owned TEMP follows its physical
lifetime. Held prepared statements reanalyze and retained captures/metadata stay
stable. Failed staged Restore publishes nothing.
Feature smoke: `make check RUN='^TestReferencePGExtensionFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGExtensionFeatureSmoke$'`.

## Available extension views — change note

Both readonly `pg_catalog` views use ordinary direct/prepared SELECT, predicates,
joins and stored views. PUBLIC SELECT does not grant installation or member-call
permission. Package metadata is not filtered by database CREATE, target-schema
USAGE, ownership, search_path or foreign-TEMP ownership; relation lookup/ACLs
still apply. Installed state uses the caller-visible catalog: private transaction
changes are visible to self, peers see committed state, rollback removes private
installation, and held prepared statements capture fresh status each execution.
Both availability views and `pg_extension` retain the same caller-visible installed
catalog before input opening, including repeated, stored and derived inputs.
Peer publication between openings cannot mix NULL installed_version with an
installed version row or a newly present pg_extension row in that statement.
The next execution captures fresh state; this is not transaction-wide freezing
or a shared-frontier guarantee for every other catalog source.

Ordered fields of `pg_available_extensions` are `name NAME`, `default_version TEXT`,
`installed_version TEXT`, `comment TEXT`. Ordered fields of
`pg_available_extension_versions` are `name NAME`, `version TEXT`, `installed BOOL`,
`superuser BOOL`, `trusted BOOL`, `relocatable BOOL`, `schema NAME`, `requires NAME[]`,
`comment TEXT`. Every native view attribute has known nullable metadata, including
empty results; pq nullability is unknown. NAME/schema/requires use C950;
default_version/version/comment use default100; installed_version inherits C950.
`pg_class` classifies both as views; `pg_views` supplies exact released
reconstruction text and bootstrap ownership; neither appears in `pg_tables`.

Availability comes only from initialized genuine embedded registrations, not
installed descriptors or the host filesystem. Today the entire native universe
is unaccent1.1 with comment `text search dictionary that removes accents`,
superuser/trusted/relocatable all true, NULL control schema and NULL requires.
Trusted does not imply superuser=false. Fixed control schema is not the installed
target namespace; requires denotes extension names, not schemas. Missing optional
metadata is SQL NULL, not an empty array/string. Version1.0 is not available merely
because an update-from1.0 script exists. PostgreSQL genuinely ships other packages;
native does not fabricate hstore, file_fdw or plpgsql availability.

The first view preserves actual installed version text verbatim or NULL; the
second marks installed only for an exact name/version match. An unknown installed
version creates no availability row. Native install/update/catalog-mutation
boundaries are unchanged; the unknown-version runtime witness is a rolled-back
PostgreSQL-only oracle transaction, supplemented by a pure native projection unit.
Reset clears persistent installation but preserves registry availability; Restore
reconstructs actual installed state/providers. Dump serializes installations, not
availability tuples. Already returned native Rows retain original values/types.
Invalid columns bind even for FALSE/absent-package predicates and expose full42703
English diagnostics before Query/Prepare returns any handle.
Feature smoke: `make check RUN='^TestReferenceAvailableExtensionsFeatureSmoke$'`;
add `REFERENCE=1` for pinned PostgreSQL18.0. See
[selected proof](TESTING.md#available-extension-views-selected-proof).

## pg_depend catalog — change note

`pg_catalog.pg_depend` (relation2608, namespace11) uses the existing normal
direct/prepared SELECT pipeline. Its columns, in order, are `classid OID`,
`objid OID`, `objsubid INT4`, `refclassid OID`, `refobjid OID`,
`refobjsubid INT4`, `deptype CHAR`. All are known NOT NULL, with origins
relation2608/attributes1–7, including empty results. CHAR is PostgreSQL's internal
`"char"`, not CHAR(n). pq descriptors expose unknown nullability. There is no row
`oid`: selecting it reports42703 `column "oid" does not exist`, Position8.
Unordered results are bags, retaining independently constructed multiplicities.

Each row identifies dependent `(classid,objid,objsubid)` to referenced
`(refclassid,refobjid,refobjsubid)`. Whole objects use subid0; live relation columns
use stable attribute numbers, not dense evaluation slots. tableoid uses -6.
Generated OIDs are backend-local identities, never allocator-parity promises.

| Represented construction | Exact edge flavors |
|---|---|
| Table/view/sequence | Whole relation to actual namespace `n`; declared user column type `n` |
| Typed table | Whole table to standalone source type `n`; own row type remains independently owned `i` |
| Enum/domain | Declaration to namespace `n`; domain to immediate unpinned base `n` |
| Domain array | Actual array to element domain `i`, no independent namespace edge |
| NOT NULL | Real constraint to owner column `a` |
| CHECK | Constraint to conkey columns (or table0 if none) `a`, separately to expression references `n` |
| UNIQUE | Constraint to key columns `a`; backing index to constraint `i` |
| Default | Real attrdef to owner column `a`, expression references `n` |
| STORED generation | Real attrdef to generated column `i`, input/external references `n` |
| SERIAL / plain OWNED BY | Sequence to owner column `a`; SERIAL attrdef also to sequence `n` |
| Identity | Sequence to owner column `i`, no synthetic ordinary attrdef |
| View | Real `_RETURN` rule to view `i`, actual query references `n` |
| Ordinary index | Distinct simple key/INCLUDE and parent expression columns `a`, external references `n` |
| Expression-only index | Additionally parent table0 `a` when there are no simple key/INCLUDE columns |
| Registered installed extension | Installation to actual target namespace `n`, separately from member placement |
| Installed procedure/template/dictionary | Each genuine member to extension `e`; member to actual unpinned namespace `n` |
| Installed template/dictionary helpers | Template to init/lexize procedures `n`; dictionary to template `n` |
| Stored unaccent consumer | Actual procedure reference `n` through the existing attrdef, CHECK or view-rule construction |

CHECK can expose both `a` and `n` to the same column; these are not collapsed.
Expression collections deduplicate repeated addresses and omit a redundant
whole-object reference when that collection references its columns. Separate
construction passes remain separate, notably expression-index parent0 ownership.
Domain-valued Vars reference columns, not their result type; user constants and
non-function coercion targets can reference types. Dependencies retain analyzed
dead CASE branches rather than reflecting runtime expression simplification.
Index predicate-only edges are not part of this native projection, even though
owning analyzed predicate manifests participate in real DROP/column lifecycle,
copy admission and native controls. PostgreSQL exposes those edges. Shared
inquiries retain independent adapter expectations without skipping behavior.

Named and quoted numeric scalar REGCLASS constants capture a real existing
relation identity. NULL, nonexistent quoted numeric constants and plain OIDs
have no fabricated relation reference. PostgreSQL runtime integer-to-REGCLASS
casts likewise add no Const edge; native preserves its existing42804
`incompatible source and destination types` boundary for these casts.
Domain-over-REGCLASS explicit casts retain the existing native0A000 primitive
cast boundary, not a new cast implementation. REGCLASS-array payloads are not
recursively scanned for scalar relation dependencies. Supported BOOL consumers
such as `'p'::regclass IS NOT NULL` exercise every stored expression owner
without adding REGCLASS column storage or REGCLASS-to-TEXT casts in defaults.
Existing TEXT concatenation consumers remain supported. Rename follows captured
identity, not later name lookup. Same-name replacement never retargets an
obsolete provider.

Pinned builtin type/function/operator/collation/opclass/conversion references
and pg_catalog namespace11 are omitted, not synthetic `p` rows. public is not
pinned; discover its actual OID because the reference fixture recreates it.
Automatic rowtypes, enum arrays, pg_shdepend owner/ACL/membership relationships,
partitions, triggers and unsupported physical objects are not invented. Registered
installed extensions and their actual member/helper/namespace/consumer edges,
domain arrays, view rules and FK child/parent/key dependencies are represented.
Unsupported extension/object families do not acquire fabricated dependencies.
PUBLIC SELECT requires no endpoint SELECT/schema USAGE and applies no per-edge
privilege filtering. Native mutation reports0A000
`catalog source mutation is unsupported`; row locking reports0A000
`row locking is not supported for virtual catalog relations`.
Schema permission denial preserves native utility source-route positioning:
`DROP TABLE pgdep_private.secret` reports42501
`permission denied for schema pgdep_private`, Position12 (the schema token).
PostgreSQL18 reports the same fields with an empty Position; both complete
diagnostics are specified independently, not described as positioning parity.

Native general view expressions do not expose `tableoid`; their existing42703
boundary remains explicit while generated-column expressions retain supported
system-attribute references. Query frame coverage uses supported `MAX`,
correlation, joins, grouping, derived frames and supported UNION ALL arms,
including their analyzed coercion expressions. Expression
owners are declared in supported complete CREATE TABLE statements, not
unsupported ALTER ADD CHECK/generated-column routes.

Existing feature owners remain authoritative for RESTRICT/CASCADE. External
default removal preserves the column/table; CHECK removal preserves its table;
generation removal redirects to the generated column; index/view owners are
removed as such. Direct identity-sequence and UNIQUE-backing-index drops retain
their2BP01 internal-owner diagnostics and hints. Ordinary RESTRICT uses2BP01
`cannot drop ... because other objects depend on it`, exact dependent Detail and
`Use DROP ... CASCADE to drop the dependent objects too.` Rename preserves
identities; TYPE reconstruction can remint index/definition identities.

Reads derive detached rows from one supplied catalog frontier, including supported
private sequence changes. Returned rows/metadata survive later DDL and native
Reset/Restore; another held statement execution captures the new frontier.
Catalog projection does not expand transactional CREATE TABLE/ALTER DEFAULT or
TEMP-table creation. Existing supported sequence CREATE/ALTER/DROP provides
transaction/savepoint/peer-visibility witnesses. Native Dump serializes owning
DDL, never pg_depend rows; Restore derives relationships from destination
identities, including foreign-Driver replay. Logical replay can remint OIDs and
historical column gaps. Failed/canceled replacement preserves the prior catalog.
Supported transactional sequence CASCADE redirects external generated-column
dependencies through the existing column-shape projection seam. Nested traversal
through a generated column's OWNED BY sequence retains one candidate and one
private storage/change authority, including multiple tables and repeated
projections of sibling columns in the same table. Accumulated own index roots,
pending inserts/deletes and repeatable-read snapshot roots are projected from
their correct prior layout; descriptors, storage and saved layouts publish
atomically. Savepoint rollback restores them together, peers retain the committed
frontier until publication, and final publication preserves concurrent committed
rows outside the transaction's stable snapshot.
Overlapping direct and nested paths consume each original stable attribute
identity once against the effective staged descriptor. A target removed by an
earlier valid cascade is not dropped or projected again. This does not change
the42703 error for a genuinely missing user-requested DROP COLUMN.
The immutable generated consumer can be BOOL (`'s'::regclass IS NOT NULL`);
REGCLASS-to-TEXT output is stable and is not admitted as an immutable generated
expression. Dump orders expression-provider objects before their index consumers.
Dump can defer already-admitted immutable scalar defaults with captured REGCLASS
providers, alongside fixed early-bound `nextval` defaults, to break legal
default-only cycles. One eligibility decision controls ordering, inline omission,
deferred ALTER emission and ordinary Restore binding. CHECK/generated edges
remain mandatory; cycles that cannot be resolved this way fail before output.

Complete reference family: `RUN='^TestReferencePGDepend'` with `make check`,
once natively and once with `REFERENCE=1`, each under an external900-second deadline.
Feature smoke uses the identical modes with
`RUN='^TestReferencePGDependFeatureSmoke$'`; it covers actual construction
bags, domain arrays, stable attributes, retained/held queries, scalar REGCLASS
RESTRICT/CASCADE and explicit native controls plus actual emitted SQL PG replay.

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
Input-boundary stream: `make check RUN='^TestReferenceSequenceRegclassSourceInputStream$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSequenceRegclassSourceInputStream$'`.

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

Temporary tables share the owner namespace/resolver with TEMP sequences.
Ordinary CREATE TABLE in own `pg_temp`/actual TEMP or its leading explicit path
becomes temporary; another owner's namespace rejects42P16. Owner lookup shadows
permanent relations and explicit persistent qualification bypasses it.
ON COMMIT acts only at successful outer commit; physical end removes TEMP,
while native persistent controls preserve live owner TEMP and exclude it from Dump.
LIKE INCLUDING DEFAULTS preserves original sequence linkage. Same-session TEMP
destinations accept TEMP sequence defaults, including new copies of retained
definitions after native replacement. Constant/permanent-sequence defaults from
TEMP sources also copy into permanent destinations. A permanent copied DEFAULT
capturing a TEMP sequence retains native Execute0A000
`temporary sequence DEFAULT is unsupported` with no copied source Position;
PostgreSQL permits it. Raw DEFAULT binding preserves its actual call Position.
Fresh copied identity sequences follow destination namespace and persistence,
not the source token or consumed state. Acquisition may use a current-epoch lock
reference only after existing bounded retained TEMP authority proves the original
reference; stored expression/manifests and generic ObjectRef validity are unchanged.
Permanent transactional CREATE/DROP and GLOBAL/LOCAL remain excluded; TEMP-source
view inference follows [temporary views](#temporary-views--change-note).
Smoke: `make check RUN='^TestReferenceTemporaryTableFeatureSmoke$'`.


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

Feature commands: `make check RUN='^TestReferenceSequenceSourceConsumerStream$'`,
`make check REFERENCE=1 RUN='^TestReferenceSequenceSourceConsumerStream$'`,
`make check RUN='^TestReferenceSequence[A-Za-z0-9_]*$'`,
and `make check REFERENCE=1 RUN='^TestReferenceSequence[A-Za-z0-9_]*$'`.
The sequence families include an exact concurrent CACHE1 bag, CACHE7
distinctness, CHAIN/failed-commit/physical-end/replacement cleanup, the full
USAGE/SELECT/UPDATE matrix, operation-specific wrong-kind records, and
control failure atomicity. The public smoke exercises physical-end TEMP
cleanup as well as normal SQL and native-control round trips.
Prepared regression commands:
`make check RUN='^TestReferenceSequencePrepared[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceSequencePrepared[A-Za-z0-9_]*$'`.
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
Stored expression-generated columns support `GENERATED ALWAYS AS (expression) STORED`.
INSERT omission/DEFAULT and UPDATE base-column writes recompute stored values;
UPDATE DEFAULT is admitted only for generated destinations. Ordinary/identity
UPDATE DEFAULT remains unsupported. See [stored generated columns](TABLES.md#stored-generated-columns).


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
LATERAL joins, CTE/function FROM, generic expressions/functions,
casts outside the bounded baseline/character-family/selected regclass/datetime sets,
constraints beyond nullability/defaults/CHECK/identity/UNIQUE, ALTER ADD CHECK/VALIDATE,
non-column serial type uses, default-containing ALTER lists, primary keys,
UPDATE row assignments (named-cursor UPDATE/DELETE CURRENT OF is supported),
whole-row UPDATE/DELETE records and generic ROW/field operators,
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
`SET [SESSION | LOCAL] NAMES {value | DEFAULT}` lowers to the same ordinary
assignment AST as client_encoding. UTF8 aliases, four bounded C/POSIX locale
categories, ICU reporting policy and float textual precision use the existing
SHOW/RESET/current_setting/set_config routes and transactional setting owner.
See [session settings](SETTINGS.md#format-encoding-and-locale-settings--change-note)
for native exclusions, current-role lc_messages authority and canonical output.
No timezone(...) alias or timezone catalog views are added. The five
[SQL current date/time producers](DIALECT.md#sql-current-date-and-time--change-note)
are supported with transaction-start values and distinct DATE/TIME/TIMETZ/
TIMESTAMP/TIMESTAMPTZ result types.


## Ordinary B-tree indexes

CREATE INDEX builds on populated ordinary tables with at least one simple or
same-table scalar expression key. The limit is 32 total key plus INCLUDE
attributes; repeated keys/payload are legal. Default/explicit btree supports
per-key direction, NULL placement, registered collation overrides and operator
classes. INCLUDE has storage/projection semantics only, is not compared and does
not affect uniqueness. Its type needs no ordering provider. Names are unqualified
and belong to the target table's schema, not the first creation schema in
search_path. Omitted names derive from all key/payload labels. IF NOT EXISTS
emits a duplicate-relation NOTICE without changing an existing object, but does
not bypass bad column/expression/collation/class/INCLUDE binding. Class-parameter
provider validation is later than duplicate-name/IF NOT EXISTS handling. Prepare
is syntax-only; semantic diagnostics occur at Execute, with zero affected rows
and no result columns.
Keys may use function-call syntax such as `lower(v)` or parenthesized scalar
syntax such as `(n+1)`. The shared binder/type providers/evaluator compute stored
derived keys; functions/operators must be immutable after constant branch
simplification. Aggregates/subqueries are prohibited with positioned 42803/
0A000; surviving stable/volatile expressions fail unpositioned 42P17.
All original branches are bound before pruning. Failed builds or row-key
evaluation publish no partial descriptor, root or rows.
Included expressions, including schema-qualified or quoted qualified function
calls, fail semantically with 0A000 without binding their arguments.

Equality/range/IS NULL and explicit ORDER BY use the ordinary query pipeline,
independently of index choice. Unordered results remain bags. Actual sorted
logical keys, including duplicates/NULLs, are maintained by common committed
row publication. See [B-tree design](design/PG18-INDEX-BTREE.md) and
[clause/provider design](design/PG18-INDEX-CLAUSES.md). Native Dump/Restore includes
actual canonical definitions with current names, explicit providers and INCLUDE.
TYPE of a dependent key or included column reconstructs the whole definition,
even for the same type with or without USING. Old-default classes and
inherited-equivalent collations become implicit; genuine overrides remain.

Writable explicit CREATE INDEX transactions are excluded natively (0A000),
while PostgreSQL supports transactional creation/rollback. Read-only execution
returns25006 first. Standalone CREATE UNIQUE INDEX supports simple/composite keys
and trailing NULLS DISTINCT/NULLS NOT DISTINCT, existing-data validation and the
same immediate write/transaction-key enforcement as table UNIQUE. Directions,
NULL ordering, compatible classes/collations and INCLUDE retain their meaning.
Ordinary unique expressions and BPCHAR classes on TEXT/VARCHAR fail explicit0A000;
ordinary support is unchanged. Unowned methods and providers, class parameters,
storage parameters and tablespaces remain excluded.
Concurrent creation inherits the supported nonunique hash surface and additionally
supports full computed/provider-aware B-tree UNIQUE tuples and NULLS NOT DISTINCT
through the same enforcement; see
[concurrent index publication](design/PG18-INDEX-CONCURRENT.md).
Hash equality indexes and ordinary maintenance are described below.
No unique-index attachment, planner, physical index-only scan or physical tree
claim is made. Admitted collations are default, C, POSIX, pg_c_utf8,
pg_unicode_fast and user descriptors backed by the same registered semantics;
class inventory and directional binary compatibility remain unchanged.

## Hash equality indexes

`CREATE INDEX [name] ON table USING hash (key)` admits exactly one simple or
immutable same-table scalar expression key on existing supported usages. Owned
defaults and explicit hash classes cover BOOL, BYTEA, NAME, integer widths,
TEXT/VARCHAR/BPCHAR, OID, floats, NUMERIC, temporal types, INTERVAL, UUID, enums
and their existing admitted domains. Released text/varchar/bpchar pattern classes
are explicit choices. Provider coverage of internal CHAR/REGCLASS does not add
stored-type admission. Hash array_ops is recognized, but native Execute returns
unpositioned0A000 `index operator class is unsupported`; no array `=` is added.
JSON has no default hash class (42704 with PostgreSQL's complete hint).

Buckets contain non-NULL keys and distinct row/version identities, not physical
PostgreSQL pages. Shared publication maintains duplicate membership atomically.
Only a proven current implicit single-source whole WHERE predicate using owned
strict `=` on a bare indexed column and same-Usage literal/imported parameter
uses bucket candidates. Commuted equality is supported; class equality guards
collisions and the original SQL comparison remains the residual. NULL probes
match no rows. Mixed/provider/coercion mismatches, compound predicates,
expression matching, joins/derived/views, row marks, explicit/private/old views
and unproved storage generations use the ordinary evaluator. IS NULL, range and
ordering never claim hash search/order; normal sorting retains query semantics.

Hash UNIQUE/INCLUDE/multiple-key requests and explicit ASC/DESC/NULLS options get
method-specific PostgreSQL diagnostics at Execute after quiet Prepare. After
existing read-only/explicit and target/owner gates, validation checks AM shape,
the complete reloptions list, key/class/type/collation/direction, name collision/
IF NOT EXISTS and class parameters in that order. Only then does a PG-valid
physical request return positioned0A000 `index storage parameters are unsupported`
at its WITH token, before allocating/building. Hash fillfactor defaults to75 in
PostgreSQL and valid bounds are10–100; no native physical option is accepted as
a no-op. Hash ALTER SET rejects deduplicate_items; valid SET/RESET retain the
existing physical-control exclusion.
Native HASH WHERE is a separate Execute-time capability exclusion after quiet
Prepare: unpositioned0A000 `partial indexes are unsupported`, including syntactic
WHERE TRUE. Existing HASH UNIQUE/INCLUDE/multikey errors take precedence; the
WHERE guard runs before predicate binding, reloptions and root construction.

Dump and pg_get_indexdef emit USING hash without ASC/NULLS decorations. TYPE
reselects old-default hash classes; rename/drop/schema movement, REINDEX,
Reset/Restore and self/foreign replacement use the common atomic owners.
Metadata retains released hash class OIDs and zero indoption without adding SQL
catalog surfaces. Hash whole-index clusterable is false; scan/bitmap/backward
properties are true. Key direction/order/return/array/NULL-search properties are
false; unknown/out-of-range results remain NULL. See
[the hash design](design/PG18-INDEX-HASH.md) for independent contracts and smoke.


### Index maintenance

```sql
DROP INDEX [IF EXISTS] [schema.]name [, ...] [CASCADE|RESTRICT];
ALTER INDEX [IF EXISTS] [schema.]name RENAME TO destination;
REINDEX INDEX [schema.]name;
REINDEX TABLE [schema.]name;
```

Every utility returns zero affected rows/no result columns and Prepare is
syntax-only. DROP preflights every target before waiting/publishing, deduplicates
repeated addresses, and removes selected standalone indexes, unique or ordinary.
IF EXISTS emits complete missing-index/schema notices. Attached UNIQUE indexes cannot be dropped
directly even with CASCADE: use DROP CONSTRAINT. Rename retains root/key/OID
identities; backing rename also changes its parent constraint name atomically.
Table/sequence/view rename aliases use the shared relation rename contract.

REINDEX INDEX publishes one freshly built logical root, while TABLE rebuilds every
implemented simple/expression/UNIQUE root together from grant-time committed live
rows. Empty-index TABLE returns zero plus a NOTICE. Descriptor/table/version
identities stay unchanged and failures preserve all predecessor roots.
All implemented explicit/binary-compatible/pattern classes, effective collations,
repeated INCLUDE attributes and non-orderable payloads survive maintenance.
Enum/domain indexes retain existing declaration-order providers. Internal metadata
uses actual central IndexClass identity: total attribute counts include payloads,
while class/collation/options cover keys only. It is not a default-class substitute.
DROP needs index or schema ownership; rename needs ownership plus schema CREATE;
rebuild needs parent MAINTAIN. Existing lock/cancellation/epoch authority applies.

Native writable explicit execution is0A000 before lookup or waits; native
read-only execution is25006 first, intentionally differing from PostgreSQL's
REINDEX lookup/lock-before-read-only precedence. Valid physical SET/RESET requests
are0A000 `index storage parameters are unsupported` without mutation, after
target/kind/ACL and ordered option validation. Missing ALTER IF EXISTS skips those
semantic option checks. REINDEX controls use whitespace, not '=': after the
intentional native transaction gates, the complete malformed-control list is
validated before target/schema lookup, kind/ACL checks or waits. Unknown/parallel
controls and malformed Boolean values are42601; an earlier valid control cannot
mask a later invalid one. Quiet Prepare stays syntax-only. Valid options retain
normal target admission before their explicit capability diagnostics, as do
concurrent operations, TABLESPACE/global targets and statistics at their existing
phases. No meaningful option is discarded.

Compact full/column/pretty pg_get_indexdef uses original analyzed keys and current
names, including correct expression parentheses and actual collations. Dump
retains canonical replay rendering. The internal pg_index-shaped projection
remains internal; `pg_catalog.pg_indexes` is a readonly normal query source.
It emits each actual index once, including constraint-backed and retained
ready-invalid concurrent indexes. Access method is inside `indexdef`, not an
extra column. Definitions are compact logical SQL: the parent is always
schema-qualified, identifiers are minimally quoted, default ordering collapses,
and no trailing semicolon or CONCURRENTLY clause appears. Native tablespace is
NULL; unsupported physical storage options are never invented.

Native pg_indexes disclosure requires schema USAGE and parent table SELECT or
SELECT on any live column, even a non-indexed column. Search-path absence alone
is not a privilege denial. This is intentionally stricter than PostgreSQL18's
unfiltered pg_indexes view. Index ACL and INSERT/DELETE/MAINTAIN alone do not
grant disclosure; ordinary owner ACL and superuser semantics still apply.
The original operation invoker controls disclosure, including through views.

Prepared queries capture current definitions and ACLs at each execution; eager
rows and their metadata retain their prior values through rename/drop and native
Reset/Restore. Generic native catalog reads see latest committed definitions
plus their own supported transaction overlay, not PostgreSQL's repeatable-read
catalog snapshot. Dump/Restore serializes owning indexes, not pg_indexes rows.
Feature smoke: `make check RUN='^TestReferencePGIndexesFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGIndexesFeatureSmoke$'`.
See [the design](design/PG18-INDEX-MAINTENANCE.md).
Smoke: `make check RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders)'`;
repeat the same selector with `REFERENCE=1`.


The optional WHERE clause occurs once: a second WHERE is a syntax error at that
keyword, not a replacement predicate. Parsing rejects the complete malformed
input before any preceding statement can take effect.

Ordinary partial B-tree indexes support same-table immutable Boolean WHERE predicates, independent
of key/payload columns. TRUE alone qualifies; NULL/FALSE exclude before derived
key evaluation. Builds, committed maintenance and private statement preflight use
that same contract. Original predicates/dependencies participate in current-name
deparse, DROP/TYPE and destination Restore rebuilds. Exact unresolved immutable
conjunction proof may supply a coherent committed sequential-scan membership
rejection mask; full residual WHERE and sort remain authoritative. Parameter atoms
cannot prove fixed predicates. Private/historical/stale views fall back to ordinary
scanning. No key traversal or cost planner is added. Partial UNIQUE constrains only
TRUE members through the shared key reservations and committed/private/prefix
checks, with ordinary DISTINCT or NND null policy. Leaving members retain old-key
reservations until transaction outcome; outside rows reserve no partial keys.
Predicate-only TYPE validates final rebound membership immediately, including own
writes/deletes. Analyzed standalone Boolean TRUE becomes no stored predicate and
deparses without WHERE; other definitions remain partial despite execution folding.
CREATE TABLE LIKE INCLUDING INDEXES (or ALL) copies these admitted partial B-tree
and partial UNIQUE definitions through the same index owner. Both cooked predicate
trees remap row references to destination attributes; analyzed external
REGCLASS/REGTYPE dependencies remain unchanged even if execution folds them away.
Copied indexes start with fresh empty roots and preserve TRUE-only qualification
before derived-key evaluation.
Only effective nonpartial unique keys contribute strong UPDATE locks/history.
Partial replacement/control witnesses retain supported TEMP sequences (including
cached values and destination identity collisions) and eager selected rows across
Reset/Restore; they do not require or enable TEMP-table DDL.
Ordinary HASH is unchanged. Native HASH with WHERE, including WHERE TRUE,
retains the unsupported0A000 boundary at Execute; PostgreSQL accepts it.
Native concurrent WHERE retains unpositioned Execute0A000
`partial concurrent indexes are unsupported`, including syntactic WHERE TRUE.
Ordinary concurrent and nonconcurrent partial creation remain supported; no
concurrent-predicate snapshot classification or computed-partial-unique scope is added.

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

