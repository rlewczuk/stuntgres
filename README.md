# stuntgres

stuntgres is a process-local, embedded PostgreSQL-compatible Go `database/sql`
driver for unit tests and reusable test fixtures. It lets Go developers exercise
SQL-backed code without starting a database server or container. Production code
uses only the Go standard library. Requires **Go 1.26 or later**.

It implements a deliberately bounded SQL subset, not a complete PostgreSQL server.
There is no network service, filesystem storage, or durability; data disappears
when the process exits. Use real PostgreSQL for integration tests that depend on
features outside the [supported dialect](docs/DIALECT.md).

SELECT and INSERT SELECT support comma products, CROSS JOIN and inner joins
with ON, USING or NATURAL, including nested joins, FROM/column aliases and the
special USING alias. Qualified columns and mixed qualified stars preserve
duplicate occurrences; unordered results are bags, not a traversal-order promise.
Prepared executions freshly bind every source, and eager results survive native
Reset/Restore. See [join semantics and limits](docs/QUERIES.md#inner-and-cross-joins--change-note).
Public feature smoke: `make smoke RUN='^TestQueryJoinsSmoke' VERBOSE=0`.

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

SELECT, shared INSERT SELECT and decorated VALUES support expression ORDER BY,
output labels/ordinals, multiple keys and per-key ASC/DESC/NULLS FIRST/LAST.
Referenced output values are evaluated once before sort and reused; unrelated
outputs retain after-sort evaluation. Unordered queries and complete sort peers
have no portable relative order. LIMIT/OFFSET and general COLLATE remain excluded.
See [ordering semantics](docs/QUERIES.md#expression-ordering--change-note-changelog).
Public feature smoke: `make smoke RUN='^TestQueryOrderSmoke$' VERBOSE=0`.

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
pre-mark effects survive but native locks conservatively persist until the
outer transaction ends; PostgreSQL subtransaction lock rollback is excluded. See
[transaction recovery and logical diagnostics](docs/TRANSACTIONS.md).

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
`HAVING`, and the initial aggregate `count(*)` (an `INT8` result). Equal typed
keys, including NULL keys, form one group; empty global count returns one row
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
make test       # uncached Docker-free unit and public smoke tests
make smoke      # public API scenarios only
make race       # Docker-free tests with the race detector
make reference  # compatibility tests against pinned PostgreSQL; requires Docker
make test-all   # all suites, including reference, with the race detector
```

Test targets enforce a 300-second wall-clock deadline and are quiet by default.
Use `VERBOSE=1` for diagnostics, `RUN='^TestName$'` for selection, and
`PACKAGES=./internal/parser` to scope ordinary or race tests.

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
