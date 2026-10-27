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

SQL and PL/pgSQL procedures use standalone `CALL`, including named, default and
variadic arguments and declared OUT/INOUT result columns. Calls share the
executing statement's atomic effects and cancellation. Native fixture controls
preserve routine declarations and data; see [routine execution](docs/DRIVER.md).

`CREATE EXTENSION tablefunc` provides `normal_rand` and the four documented
`connectby` hierarchy-source overloads. A connectby call in `FROM` supplies its
anonymous record columns: key, parent, integer depth, optional text branch,
and (for ordered calls) final integer serial. Relation/key/parent/order arguments
are raw SQL fragments; quote mixed-case identifiers inside those strings.
Nested SQL follows read-only SPI execution: utility commands such as `SET`,
`CREATE TABLE` and `TRUNCATE` are rejected, while multiple read-only SELECTs
remain valid and the last SELECT supplies children. Unsupported transaction
commands and frontend COPY terminate the nested list without child rows.
Ordered calls sort siblings and assign global depth-first serials; use an outer
`ORDER BY serial` when the result order matters. NULL arguments return no rows;
positive depth limits include that depth, while zero/negative limits are unlimited.
Cycle detection is textual: separators inside keys can produce PostgreSQL's
documented false positives. Native fixture controls preserve installed identities
and caller-owned view definitions. This is bounded tablefunc coverage, not an
implementation of crosstab or every upstream tablefunc member.

Client connection defaults use one typed settings registry, including
`SET XML OPTION DOCUMENT/CONTENT`. Presentation, UTF8/C locale boundaries,
search paths, row security, transaction characteristics and timeouts retain
their real native behavior. Storage/GIN/TOAST, FTS configuration selection,
XML defaults, library/path/preload strings and other absent mechanisms are
explicit **setting-only simulations**, not physical effects. Native
`session_replication_role=replica` never bypasses FK checks, and
`check_function_bodies=off` never admits invalid routines. Postmaster settings
still refuse live mutation; SET grants and restricted reads are independent.
See the [settings boundary inventory](internal/engine/settings/README.md#client-connection-defaults).

All 20 [preset options](internal/engine/settings/README.md#preset-options) are
universally readable and immutable (`55P02` on individual SET/RESET, even for
superusers). Physical presets are explicit setting-only simulations: they do
not allocate WAL, disk blocks, checksums, shared memory, semaphores or huge pages.
Native identity remains truthfully **PostgreSQL 18.0 compatible; embedded
stuntgres**, independently of the pinned PostgreSQL 18.6 comparison fixture.

Aggregate declarations support `COMBINEFUNC`, paired `SERIALFUNC`/`DESERIALFUNC`,
`PARALLEL`, and `SORTOP`. Eligible top-level physical-table aggregate queries
combine independent partial states; builtin `string_agg` transports real opaque
state through its binary codec. Unsupported query shapes and UNSAFE/RESTRICTED
declarations remain serial, as do owned domain-state initializers with SQL CHECK
callbacks. `SORTOP` declares min/max-like semantics, not
transition-input ordering. SQL cannot manufacture or expose `internal` states.
Existing final and moving aggregate support remains independent. See the
[aggregate contracts](reference/aggregates/README.md#testing) for selected proof.

`pg_catalog.pg_amproc` exposes six typed support-membership columns, including
symbolic `regproc` identities and exact operator-family/type associations. Owners
publish implemented comparator/hash routines, genuine internal BRIN minmax
operations, SQL/provider-backed DDL members and installed `cube_cmp`. Copied
sort/equal-image/options/physical callback metadata remains internal, not an
advertised implementation. Catalog reads do not imply PostgreSQL physical
storage or callback ABI support; see the
[operator-family contract](docs/design/PG18-INDEX-OPERATOR-FAMILY.md).

Standalone `ANALYZE`/`ANALYSE` collects actual statement-visible table values
and refreshes metadata consumed by logical `EXPLAIN`, without changing query
results. It supports selected columns, inheritance/partition populations,
materialized snapshots, own temporary tables, `VERBOSE` and `SKIP_LOCKED`.
Prepare/Describe does not collect. Maintenance privilege denial warns and skips;
namespace access errors remain errors. Column distributions are transactional
(including savepoints and prepared transactions); committed relation row
estimates are maintenance facts and survive rollback. Earlier standalone targets
remain refreshed if a later target has a bad column; all relation names resolve
before work starts.
All retained column facts use one metadata root in the existing storage owner,
for heap and partition relations alike. Partition parents never acquire physical
heap storage. New private-object metadata stays private until creation commits.

Native collection scans all visible logical rows with fixed target 100.
Population, NULL counts, supported typed distinct counts and retained MCV counts
are exact at capture, not PostgreSQL physical sample claims. Top-100 MCV selection
and at most 101 residual histogram endpoints are deterministic bounded summaries;
residual/range selectivity and multicolumn independence are approximations.
Unavailable equality/ordering omits the corresponding statistic. Distributions
stay stale after DML and unselected columns retain their facts; empty refresh
retains old column distributions but refreshes row estimates to zero.
`BUFFER_USAGE_LIMIT` is validated but its physical buffer-strategy control is
inapplicable in this in-memory engine. No pages, WAL, sampling workers or
autovacuum are implied. Full `pg_statistic`/`pg_stats` SQL readers are separate
coverage; existing `pg_class` and table-statistics views publish real maintenance
facts. Logical Dump/Restore transports data, not planner statistics.


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
Routine rename, schema and owner behavior is documented by its
[owning contract](internal/engine/routine/README.md#routine-alter-contract) and
[shared PostgreSQL proof](reference/query_core/README.md#routine-alter-proof).

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
Every option can also come from a `STUNTGRES_<OPTION>` environment variable
(for example `STUNTGRES_USERFILE`) or a `--config` YAML/TOML/JSON file keyed by
long option names. `./stuntgres --help` lists all options; see [cmd/stuntgres](cmd/stuntgres/README.md#operation)
for accounts, dumps and logs. Add `-q` (with `-L`) to record every query, its
parameters and the exact responses in `query.log`. Add
`--reference DB=postgres://user@host/db` as well to send database DB's
commands to a real PostgreSQL server too and log its responses with their
differences beside stuntgres's.

A sample `stuntgres.yaml` that compares database `app` with a local
PostgreSQL (other databases in the userfile run on stuntgres alone):

```yaml
listen: 127.0.0.1:5434
userfile: ./users
logdir: ./logs
querylog: true                  # dual-backend mode requires the query log
reference:
  - app=postgres://app_user:app_password@127.0.0.1:5432/app_reference?sslmode=disable
reference-timeout: 30s
```

```sh
./stuntgres serve --config ./stuntgres.yaml
```

The URL password may be omitted; it is then read from `PGPASSFILE` (or
`~/.pgpass`), for example `127.0.0.1:5432:app_reference:app_user:app_password`.
Prefer that for `start`, which passes the URL to the daemon on its command line.

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
