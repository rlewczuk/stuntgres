# stuntgres

stuntgres is an embedded, process-local PostgreSQL-compatible `database/sql`
driver for Go unit tests and reusable test fixtures. Code under test talks to it
like a PostgreSQL pool, but no database server or container is started. Production
code depends only on the Go standard library. Requires **Go 1.26 or later**.

The goal is full PostgreSQL 18 compatibility; the
[PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html) is the
reference for SQL syntax and behavior, and any observable difference is a bug.
Databases live in memory: there is no WAL, crash durability or replication.
Native [Reset, Dump and Restore](docs/FIXTURES.md) give explicit logical snapshots
for fixtures; they are not `pg_dump` archives. The optional `cmd/stuntgres`
executable serves the same engine over the PostgreSQL wire protocol.

## Using stuntgres in unit tests

Each `stuntgres.NewDriver()` owns independent in-memory clusters.
`OpenConnector(name)` opens a cluster's default database by an exact opaque key
(not a connection URL); identical keys on the same Driver share data, even after
pools close. SQL `CREATE DATABASE` creates additional databases in that cluster.
Use `OpenDatabaseConnector(key, database)` to select one by name. Importing the
package also registers the driver for `sql.Open("stuntgres", key)`. Create a fresh
Driver per fixture when tests must be isolated. See the [driver guide](docs/DRIVER.md)
for selection, pooling, sessions, transactions, value mapping, errors and Go APIs.

### Minimal example: one unit test

In a checkout of this module, save the following as `example_test.go` and run
`make check RUN='^TestPersonName$'`. In a consuming project, add
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

### Reusable fixture: two unit tests

Use a helper to create an isolated database, apply the schema, seed data, and
register cleanup. Save this example in a separate `fixture_example_test.go` file;
run it with `make check RUN='^TestPeopleFixture'` in this checkout,
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

## Running the server

`cmd/stuntgres` exposes the engine to `psql`, lib/pq and other PostgreSQL clients
(protocol 3.0–3.2, simple and extended queries, COPY). Build it, create an account
with explicit database grants, and serve:

```sh
go build -o stuntgres ./cmd/stuntgres
./stuntgres passwd -u ./users --databases app,other alice
./stuntgres serve -u ./users -l 127.0.0.1:5434 -d ./dumps -L ./logs
psql "host=127.0.0.1 port=5434 dbname=app user=alice sslmode=disable"
```

Run it in the background, trigger a dump, stop it, and restore the newest dumps on
the next start with `-r`:

```sh
./stuntgres start -u ./users -l 5434 -d ./dumps -L ./logs -p ./server.pid
./stuntgres dump -p ./server.pid
./stuntgres stop -p ./server.pid
./stuntgres start -u ./users -l 5434 -d ./dumps -L ./logs -p ./server.pid -r
```

**Passwords are sent in cleartext and TLS is not available.** The server listens on
loopback by default; use it only on loopback or an independently protected network.
`./stuntgres --help` lists all options; see [cmd/stuntgres](cmd/stuntgres/README.md#operation)
for accounts, dumps and logs.

## Building from source and contributing

From the repository root, with Go 1.26 or later:

```sh
make build                       # compile production packages
make check RUN='^TestPersonName$' # run selected tests (RUN is required)
make quickcheck                  # full Docker-free verification
```

`make check REFERENCE=1 RUN='...'` runs the same selected reference tests against a
real, pinned PostgreSQL 18 container (requires Docker).

Contributions follow a test-first workflow: write a failing dual-mode reference test
whose expectations are taken from real PostgreSQL 18, then make it pass natively.
Edit `internal/parser/grammar.y` (never the generated parser) and run `make generate`.
Production dependencies are limited to the standard library. Start with
[AGENTS.md](AGENTS.md) for the package map and rules,
[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the workflow and make targets, and
[docs/TESTING.md](docs/TESTING.md) for writing tests. Feature history is in
[CHANGES.md](CHANGES.md).

Report bugs and feature requests through this repository's issue tracker. Include a
minimal reproducer, Go and stuntgres versions, the SQL and parameters, and the
expected and actual results. For compatibility reports, include the behavior
observed on PostgreSQL 18 and any SQLSTATE.
