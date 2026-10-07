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
cast_type := INTEGER | INT | INT4 | BIGINT | INT8 | BOOLEAN | BOOL | TEXT
           | (CHAR | CHARACTER | BPCHAR | VARCHAR | CHARACTER VARYING) [(length)]
comparison := scalar (= | <> | != | < | <= | > | >=) scalar
logical_target := scalar | comparison | NOT logical_target
                | logical_target AND logical_target | logical_target OR logical_target
                | (logical_target)
logical_predicate := logical_target
delete_predicate := logical_predicate with optional target/alias qualification on columns
projection_target := logical_target | null_count_call
call_argument := scalar | null_count_call
null_count_call := (num_nulls | num_nonnulls)(call_argument [, ...])
default_expression := value | comparison | (logical_target)
                    | default_expression IS [NOT] DISTINCT FROM default_expression
```

## Unsupported surface

Three-part/database qualification, temporary-object creation, SELECT aliases/qualified columns, joins, generic expressions/functions, casts outside the bounded baseline and character-family sets, constraints beyond nullability/defaults, serial/identity, ALTER DEFAULT, primary keys, UPDATE FROM/row-subqueries/CURRENT OF, DELETE USING/CURRENT OF, RETURNING, multiple sort keys, savepoints, two-phase commit, transactional DDL/COMMENT/role mutation, SERIALIZABLE/SSI, DEFERRABLE safe snapshots, SQL ACL mutation, or SQL catalog projections are not supported. SQL transaction controls, RU/RC/RR modes, read-only access, CHAIN and current/default transactional settings described in [transactions](TRANSACTIONS.md) are supported. Recognized unsupported features fail with SQLSTATE `0A000`; malformed SQL fails with `42601`. Unsupported suffixes and multiple statements fail before any prefix executes.
