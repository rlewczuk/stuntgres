# stuntgres

stuntgres is a PostgreSQL-compatible database that runs inside your Go test
process. It is a `database/sql` driver: code under test uses it like a PostgreSQL
connection pool, but no database server, container or network is involved.

It is meant for Go developers who test code that talks to PostgreSQL and want
those tests to be fast, isolated and free of external setup. Databases are cheap
to create, so each test can have its own, and fixtures can be built once and
restored for every test.

- **Compatibility.** The goal is full PostgreSQL 18 compatibility. The
  [PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html)
  describes the SQL that stuntgres accepts; any observable difference is a bug.
- **In memory.** Databases live in memory. There is no WAL, crash durability or
  replication; physical storage details (files, pages, sizes) are simulated.
- **Small footprint.** The library depends only on the Go standard library and
  requires Go 1.26 or later.
- **Optional server.** The `cmd/stuntgres` executable serves the same engine over
  the PostgreSQL wire protocol for `psql` and other clients.

`CREATE EXTENSION pgcrypto` installs the two- and three-argument text
`pgp_sym_encrypt` / `pgp_sym_decrypt` calls. The symmetric packet path uses
AES128, uncompressed UTF8 text, iterated salted SHA1 key derivation and a full
OpenPGP integrity check. Default options and explicit supported options work
through direct and prepared calls; encryption returns opaque BYTEA.
Decryption authenticates the complete packet before returning any text.
Native Dump/Restore preserves installed identities, stored packet bytes and
ordinary SQL consumers; live cryptographic scratch and entropy sources are not
serialized. See [OpenPGP ownership boundaries](docs/DIALECT.md#pgcrypto-openpgp).

## Installation

The module path is `stuntgres`. Until it is published under a public path,
clone the repository next to your project and point your module at it:

```sh
git clone https://github.com/rlewczuk/stuntgres.git ../stuntgres
go mod edit -require=stuntgres@v0.0.0 -replace=stuntgres=../stuntgres
```

## A first test

```go
package people_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestPersonName(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector("people")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })

	ctx := t.Context()
	if _, err := db.ExecContext(ctx,
		"CREATE TABLE people (id integer PRIMARY KEY, name text NOT NULL)"); err != nil {
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

Run it with `go test`. What happens:

- `stuntgres.NewDriver()` creates a driver that owns its own in-memory databases.
  Nothing is shared with other drivers, so a fresh driver per test means a fresh,
  isolated database.
- `OpenConnector("people")` opens the database named by the key `"people"`. The
  key is an opaque name, not a connection URL; the same key on the same driver
  reaches the same data.
- `sql.OpenDB` turns the connector into an ordinary `*sql.DB`. From here on it is
  plain `database/sql` with PostgreSQL SQL and `$1`-style parameters, so you can
  pass `db` to the code under test exactly as you would a real PostgreSQL pool.
- `t.Cleanup` closes the pool when the test ends, whether it passed or failed.

Importing the package also registers the driver for `sql.Open("stuntgres", key)`,
which uses one shared default driver.

## A reusable fixture

Most test suites share a schema and seed data. Build it once, save it with
`stuntgres.Dump`, and give each test its own copy with `stuntgres.Restore`:

```go
package people_test

import (
	"bytes"
	"context"
	"database/sql"
	"sync"
	"testing"

	"stuntgres"
)

const peopleSchema = `
CREATE TABLE people (id integer PRIMARY KEY, name text NOT NULL);
INSERT INTO people (id, name) VALUES (1, 'Ada'), (2, 'Grace');
`

// peopleSeed is the fixture: the schema and seed rows, built once and
// saved as a native snapshot.
var peopleSeed = sync.OnceValues(func() ([]byte, error) {
	connector, err := stuntgres.NewDriver().OpenConnector("seed")
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, peopleSchema); err != nil {
		return nil, err
	}
	var seed bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &seed); err != nil {
		return nil, err
	}
	return seed.Bytes(), nil
})

// newPeopleDB gives each test its own database restored from the fixture.
func newPeopleDB(t *testing.T) *sql.DB {
	t.Helper()
	seed, err := peopleSeed()
	if err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("people")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })

	if err := stuntgres.Restore(t.Context(), db, bytes.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPeopleLookup(t *testing.T) {
	t.Parallel()
	db := newPeopleDB(t)

	var name string
	if err := db.QueryRowContext(t.Context(),
		"SELECT name FROM people WHERE id = $1", 2).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Grace" {
		t.Fatalf("name = %q, want Grace", name)
	}
}

func TestPeopleDelete(t *testing.T) {
	t.Parallel()
	db := newPeopleDB(t)
	ctx := t.Context()

	if _, err := db.ExecContext(ctx, "DELETE FROM people WHERE id = $1", 1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM people").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}
```

How it fits together:

- `peopleSeed` runs the schema script once per test binary (`sync.OnceValues`)
  and keeps the result of `Dump`: a logical snapshot of the database's schema and
  rows. However large the schema grows, it is executed only once.
- `newPeopleDB` creates a new driver for every test and loads the snapshot with
  `Restore`. Both tests open the key `"people"`, but on different drivers, so they
  get separate databases.
- Because nothing is shared, the tests can call `t.Parallel()`: the delete in
  `TestPeopleDelete` is invisible to `TestPeopleLookup`.
- `t.Helper()` reports setup failures at the calling test's line.

`stuntgres.Reset` empties a database in place when you want to reuse one pool.
Dump, Restore and Reset work only on native stuntgres pools; see
[native fixture controls](docs/FIXTURES.md) for their rules.

## Further reading

- [Driver API](docs/DRIVER.md): opening databases, pooling and sessions,
  transactions, value mapping, errors, notices, COPY, settings and other Go APIs.
- [Native fixture controls](docs/FIXTURES.md): Reset, Dump and Restore.
- [SQL dialect boundaries](docs/DIALECT.md): where an embedded engine has to
  differ from a PostgreSQL server.
- [cmd/stuntgres](cmd/stuntgres/README.md): the wire-protocol server.

## Running the server

`cmd/stuntgres` serves the engine to `psql`, lib/pq and other PostgreSQL
clients. Build it, create an account with access to the databases it may use,
and start serving:

```sh
go build -o stuntgres ./cmd/stuntgres
./stuntgres passwd -u ./users --databases app alice
./stuntgres serve -u ./users -l 127.0.0.1:5434 -d ./dumps -L ./logs
psql "host=127.0.0.1 port=5434 dbname=app user=alice sslmode=disable"
```

**Passwords are sent in cleartext and TLS is not available**, so use the server
only on loopback (the default) or a protected network. `./stuntgres --help` lists
all commands and options; [cmd/stuntgres](cmd/stuntgres/README.md#operation)
covers background mode, dumps, logs, configuration files and the query log.

## Development

Building and testing need Go 1.26 or later and `make`. Tests marked
`REFERENCE=1` also need Docker, which runs a pinned PostgreSQL 18 server.

```sh
make build                         # compile production packages
make check RUN='^TestName$'        # run selected tests (RUN is required)
make check REFERENCE=1 RUN='...'   # the same tests against real PostgreSQL 18
make quickcheck                    # full suite without Docker
make generate                      # regenerate the parser and Unicode tables
```

Most tests are *reference tests*: one test body runs against stuntgres and against
real PostgreSQL 18, and both must give the same results. New features and bug
fixes start with such a test whose expectations come from PostgreSQL; then the
engine is changed until the test passes natively.

Where to start:

- [AGENTS.md](AGENTS.md): package map and development rules.
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): workflow, make targets, generated code.
- [docs/TESTING.md](docs/TESTING.md): how to write and place tests.
- Each package's `README.md`: its responsibility and contract.
- [CHANGES.md](CHANGES.md): change history.

## Support

- **Bugs and feature requests:** open an issue at
  <https://github.com/rlewczuk/stuntgres/issues>. Please include a minimal
  reproducer (SQL and parameters, or a Go test), the expected and actual results,
  and your Go and stuntgres versions. For compatibility bugs, add what PostgreSQL 18
  returns, including any SQLSTATE.
- **Questions and discussion:** _to be decided._
- **Security issues:** _to be decided; please do not report them in public issues._
- **License:** _to be decided._
