# stuntgres

stuntgres is a process-local, embedded PostgreSQL-compatible Go `database/sql`
driver for unit tests and reusable test fixtures. It lets Go developers exercise
SQL-backed code without starting a database server or container. Production code
uses only the Go standard library. Requires **Go 1.26 or later**.

It implements a deliberately bounded SQL subset, not a complete PostgreSQL server.
There is no network service, filesystem storage, or durability; data disappears
when the process exits. Use real PostgreSQL for integration tests that depend on
features outside the [supported dialect](docs/DIALECT.md).

CREATE TABLE supports named and unnamed column/table CHECK constraints, including
`NO INHERIT`. Constraints see the completed new row after defaults/coercions:
TRUE and NULL pass; FALSE rejects INSERT/UPDATE with PostgreSQL-style `23514`
constraint identity and privilege-filtered failing-row details. A late failure
rolls back all row writes in the statement. Definitions survive native
Dump/Restore and column/table rename; CHECK does not change declared nullability.
See [CHECK constraints](docs/TABLES.md#check-constraints--change-note).
Feature smoke: `make smoke RUN='^TestCheckSmoke$' VERBOSE=0`.

Time-zone-aware `TIMESTAMPTZ` and `TIMETZ` values support per-physical-session
TimeZone/DateStyle, named IANA zones, abbreviations, fixed offsets and
`AT TIME ZONE` / `AT LOCAL`. Timestamptz stores an instant; timetz retains
its fixed offset. Rules are embedded from the pinned PostgreSQL 18.0 image,
not the host timezone installation. See [temporal types](docs/TYPES.md#time-zone-aware-values)
and [session settings](docs/SETTINGS.md#temporal-settings).
Native logical Dump/Restore preserves these values independently of session
presentation. This does not add timezone catalog views, general INTERVAL arithmetic,
or the CURRENT_* producer family.
Public feature smoke: `make smoke RUN='^TestTimezoneSmoke$' VERBOSE=0`.

SELECT and INSERT SELECT support comma products, CROSS JOIN and inner joins
with ON, USING or NATURAL, including nested joins, FROM/column aliases and the
special USING alias. Qualified columns and mixed qualified stars preserve
duplicate occurrences; unordered results are bags, not a traversal-order promise.
Prepared executions freshly bind every source, and eager results survive native
Reset/Restore. See [join semantics and limits](docs/QUERIES.md#inner-and-cross-joins--change-note).
Public feature smoke: `make smoke RUN='^TestQueryJoinsSmoke' VERBOSE=0`.

DELETE supports `RETURNING *`, scalar expressions and PostgreSQL18 OLD/NEW
output aliases. Deleted occurrences return their actual pre-deletion values
(including committed updates encountered after a row wait); ordinary NEW columns
are typed NULL. Direct and held prepared Query return eagerly finalized rows,
including full metadata for no matches; Exec still evaluates output and reports
the actual count. Output failure or prepublication cancellation discards all
transactional deletions, while sequence draws retain their usual nonrollback
semantics. Native Dump/Restore records surviving objects/data, not result history.
See [DELETE RETURNING](docs/QUERIES.md#delete-returning--change-note).
Feature smokes: `make smoke RUN='^TestDeleteReturning(Smoke|ControlsSmoke)$' VERBOSE=0`.

UPDATE supports PostgreSQL18 `RETURNING` expressions, mixed stars and output-only
OLD/NEW aliases. Ordinary outputs read the completed new row; OLD reads the actual
predecessor, including after a row wait. Same-value matches and duplicates each
return one occurrence; no matches retain full metadata. Query owns eager values,
and Exec still evaluates outputs before reporting its count. Output errors roll
back staged mutations, not sequence draws, under existing transaction/savepoint
rules. See [UPDATE RETURNING](docs/QUERIES.md#update-returning--change-note).
Feature smokes: `make smoke RUN='^TestUpdateReturning(Smoke|ControlsSmoke)$' VERBOSE=0`.

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
Feature smoke: `make smoke RUN='^TestMathScalarSmoke$' VERBOSE=0`.
Qualified temporal declarations such as `pg_catalog.time(3)` and
`pg_catalog.timestamp(3)` retain precision through native Dump/Restore;
temporal explicit casts remain excluded whether qualified or unqualified.
`SELECT current_schemas(false)` returns a genuine result-only NAME[] (`_NAME`)
as a native string, or NULL for a NULL boolean argument. General arrays and
SQL type/function/operator DDL remain excluded. TEMP remains sequence-only;
introspection does not allocate an absent temporary namespace.
Native ORDER on NAME[] is explicitly unsupported at analysis, including empty
sources; PostgreSQL's array comparator is outside this result-only surface.
Feature smoke: `make smoke RUN='^TestSearchPathSmoke$' VERBOSE=0`.

Change note: standalone `ALTER TABLE [IF EXISTS] target SET SCHEMA destination`
moves ordinary tables and every column-owned sequence atomically, preserving
rows, named NOT NULL constraints, defaults, comments, ACLs and stored-view
dependencies. It requires table ownership, source USAGE and destination CREATE
(not source CREATE or destination USAGE). Native prepared SQL rebinds its original
names at Execute; unlike PostgreSQL's held unchanged-search_path plan, an unqualified
native SELECT sees a newly created source-schema shadow after a move (see
[the dialect boundary](docs/DIALECT.md)). Native Dump/Restore records current
qualified definitions, not move history.
SET SCHEMA in explicit transactions, generic non-table moves, TEMP/system destinations,
indexes and automatic row types remain bounded native exclusions.
Feature smoke: `make smoke RUN='^TestAlterSchemaSmoke$' VERBOSE=0`.

Change note: `ALTER TABLE target ALTER [COLUMN] name SET DEFAULT expression`
and `DROP DEFAULT` atomically change future omitted/DEFAULT inserts without
rewriting existing rows. Held prepared INSERTs use the current definition;
DROP of an absent default succeeds and restores implicit NULL. ALTER accepts
general supported Boolean expressions without CREATE DEFAULT's extra parentheses.
Dump/Restore preserves the current definition, not ALTER history.
Standalone `ONLY`, `ONLY(...)`, qualified/quoted targets and trailing `*` are
supported; inheritance remains unsupported (release recursion is reference-only).
Action lists, ALTER TABLE IF EXISTS for these actions, UPDATE DEFAULT and these
default actions in explicit transactions remain excluded. See [column defaults](docs/TABLES.md#column-defaults).
Feature smoke: `make smoke RUN='^TestAlterDefaultSmoke$' VERBOSE=0`.


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
Feature smoke: `make smoke RUN='^TestViewSmoke$' VERBOSE=0`.

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
Feature smoke: `make smoke RUN='^TestCommentInquirySmoke$' VERBOSE=0`.

SELECT, shared INSERT SELECT and decorated VALUES support expression ORDER BY,
output labels/ordinals, multiple keys and per-key ASC/DESC/NULLS FIRST/LAST.
Referenced output values are evaluated once before sort and reused. With a
count decoration, cheap immutable outputs run below Sort; unrelated volatile
outputs run after Sort but before OFFSET/LIMIT. Unordered queries and complete
sort peers have no portable relative order. General COLLATE remains excluded.
See [ordering semantics](docs/QUERIES.md#expression-ordering--change-note-changelog).
Public feature smoke: `make smoke RUN='^TestQueryOrderSmoke$' VERBOSE=0`.

SELECT, shared INSERT SELECT and decorated VALUES support LIMIT/OFFSET and
FETCH FIRST/NEXT ROW/ROWS ONLY. Counts use BIGINT assignment conversion; zero
suppresses producing children without bypassing typing, authorization or bound
errors. Nonblocking views/filters/joins produce only demanded occurrences;
sorting/grouping remain blockers. Local rowmarks settle at effect frontiers,
not across a local count boundary. See [count and demand](docs/QUERIES.md#limit-offset-and-fetch--change-note-changelog).
Feature smoke: `make smoke RUN='^TestQueryLimit(Smoke|LockSmoke)$' VERBOSE=0`.

SELECT (including existing inner/cross joins, persistent scalar views and INSERT
SELECT sources) supports one trailing `FOR KEY SHARE`, `FOR SHARE`,
`FOR NO KEY UPDATE` or `FOR UPDATE`. Locks belong to contributing physical base
RowIDs, not view output values or the entire table. Stored view clauses also apply
to otherwise ordinary reads; unmarked SELECT remains a nonblocking MVCC read.
READ COMMITTED lockers recheck
the current version after contention; REPEATABLE READ KEY SHARE retains snapshot
values after compatible keyless updates, while stronger modes and deletions fail.
Native keyless UPDATE uses NO KEY UPDATE; DELETE uses UPDATE. See
[row locking](docs/TRANSACTIONS.md#row-locks--change-note) for the compatibility
matrix, privileges, eager execution and bounded syntax.
Feature smoke: `make smoke RUN='^TestRowLock(Smoke|JoinSmoke|VersionSmoke|ViewSmoke|OrderedCacheSmoke)$' VERBOSE=0`.

Selected `ALTER TABLE` nullability and named NOT NULL operations compose atomic
action lists with `DROP COLUMN`, including explicit transactions/savepoints.
Validation includes fresh grant-time committed NULLs and own writes; SET/DROP
alone does not copy stored values. Private metadata, row projections and DML
publish together at Commit; Dump excludes private changes.
Feature smokes: `make smoke RUN='^TestAlterNullability(Smoke|ControlsSmoke)$' VERBOSE=0`.

Identity columns support `GENERATED ALWAYS` and `GENERATED BY DEFAULT AS IDENTITY`
on SMALLINT, INTEGER and BIGINT, using an internally owned sequence rather than a
stored DEFAULT. INSERT omission/DEFAULT generates; SYSTEM and USER overrides,
identity ALTER lists, transactions/savepoints and native Dump/Restore reuse the
existing sequence/session authorities. Identity is NOT NULL, not inherently unique.
Existing CHECK calls can use the same declaration's identity sequence without
definition-time allocation. Dump orders identity-sequence view/CHECK dependents
after their owning table, including retained TEMP collision remaps.
SERIAL and expression-generated columns remain excluded.
Feature smokes: `make smoke RUN='^TestIdentity(Smoke|LifecycleSmoke|ControlsSmoke|CheckControlsSmoke)$' VERBOSE=0`.


The [supported types](docs/TYPES.md) include NAME values: mixed-case spelling
is preserved, textual input clips at a63-byte UTF8 boundary, and comparisons use
C byte order. NAME has distinct metadata from TEXT and round-trips through
native Dump/Restore; it does not change SQL identifier folding or add general
name casts/functions. Public feature smoke:
`make smoke RUN='^TestNameSmoke$' VERBOSE=0`.

OID is a distinct unsigned four-byte scalar with exact native int64 results,
unsigned ordering, PostgreSQL integer bitcasts/checked INT8 conversion and
native schema/data/default Dump/Restore. It does not populate metadata catalogs
or add object lookup, arithmetic or ordinary SELECT-without-FROM support.
Public feature smoke: `make smoke RUN='^TestOIDSmoke$' VERBOSE=0`.

Integral bitwise `&`, `|`, `#`, unary `~`, `<<` and `>>` support SMALLINT,
INTEGER and BIGINT in existing scalar consumers, including replayable defaults
and views. Width, signed results, typed NULL and prepared parameter inference
are preserved; shifts use an INTEGER count. See [integral bitwise operators](docs/TYPES.md#integral-bitwise-operators).
Catalog-qualified OPERATOR spellings use the same provider and namespace checks,
including canonical defaults and view replay.
Public feature smoke: `make smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.

Scalar comparison types also support inclusive `BETWEEN`/`NOT BETWEEN` and
both `SYMMETRIC` forms in existing expression consumers. Symmetric predicates
accept reversed bounds; NULL follows SQL three-valued logic rather than a
strict three-argument rule. See [BETWEEN predicates](docs/QUERIES.md#between-predicates)
for grouped endpoints, contextual prepared typing, replayable grouped/nested
defaults and bounded syntax. Public feature smoke:
`make smoke RUN='^TestBetweenSmoke$' VERBOSE=0`.

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
Feature smokes: `make smoke VERBOSE=0 RUN='^TestLockModesSmoke(Matrix|OrderViews|Savepoints)$$'`.

Selected mathematical functions include `random()`, inclusive INTEGER/BIGINT/
NUMERIC bounded `random(lower,upper)`, `random_normal([mean[,stddev]])` and
`setseed(seed)`. Exact NUMERIC sampling preserves the greater input scale.
Seeds and draws belong to the physical connection and survive rollback and
pool return; use a pinned `sql.Conn` for repeatable series. These are
pseudorandom, not cryptographic, and do not promise PostgreSQL's exact PRNG
sequence. See [the capability/change note](docs/DIALECT.md#random-functions--change-note).
Public feature smoke: `make smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.

Exact scale and rounding functions include `min_scale`, `round`, `scale`,
`trim_scale`, `trunc`, `sign` and `mod`, with PostgreSQL's selected NUMERIC,
FLOAT8 and integer overloads. NUMERIC ties round away from zero; negative
scales and exact remainder never pass through floating point. See
[the signature and behavior note](docs/DIALECT.md#scale-and-rounding-functions--change-note).
Public feature smoke: `make smoke RUN='^TestMathSmoke$' VERBOSE=0`.

INTERVAL retains independent months/days/microseconds, permitted field ranges
and fractional precision, verbose/SQL/ISO input, SQL comparison/grouping and
canonical native Dump/Restore. Each physical session owns its IntervalStyle;
prepared literals freeze input meaning and retained rows capture output style.
See [interval values](docs/TYPES.md#interval--change-note) and
[IntervalStyle](docs/SETTINGS.md#intervalstyle--change-note).
Public feature smoke: `make smoke RUN='^TestIntervalSmoke$' VERBOSE=0`.



## Minimal example: one unit test

In a checkout of this module, save the following as `example_test.go` and run
`make test PACKAGES=. RUN='^TestPersonName$'`. In a consuming project, add
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
run it with `make test PACKAGES=. RUN='^TestPeopleFixture'` in this checkout,
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
make test       # uncached Docker-free unit, public smoke and dual-mode pilot tests
make smoke      # public API scenarios only
make race       # Docker-free tests with the race detector
make reference  # compatibility tests against pinned PostgreSQL; requires Docker
make test-all   # quick and full reference suites with race; one 300s deadline
```

Test targets enforce a 300-second wall-clock deadline and are quiet by default.
Use `VERBOSE=1` for diagnostics, `RUN='^TestName$'` for selection, and
`PACKAGES=./internal/parser` to scope ordinary or race tests.

The four migrated `reference/` scenarios also run without Docker through the
ordinary test binary:

```sh
go test ./reference                         # quick: real stuntgres + expected data
go test ./reference -args -reference=false  # explicitly select the same quick path
go test ./reference -args -reference=true   # also test real pinned PostgreSQL 18.0
go test -tags reference ./reference -args -reference=true # full legacy + pilot
```

The pilot covers query projection/bags/metadata, ORDER BY diagnostic positions,
BYTEA binds/raw metadata/ownership, and short INSERT diagnostics/atomicity. Both
backends must independently satisfy the same hardcoded scenario expectations;
reference mode never substitutes parity for expected results or falls back on
startup failure. See the development guide for the exact migrated test names.

The `reference` build tag still includes all untouched legacy PostgreSQL tests;
it does not select the pilot's runtime mode. A tagged pilot-only selection with
`-reference=false` remains Docker-free, but selecting a legacy test lazily starts
the same pinned container even when the flag is absent or false. This is a small
pilot, not a whole-suite quick migration. `make reference` enables the flag only
for `./reference`. `make test-all` runs ordinary native/smoke/quick tests with
race detection, then the full tagged reference package with race detection,
chained under one aggregate 300-second wall-clock deadline.


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
