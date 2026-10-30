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

Relation-location functions use their PostgreSQL 18 typed signatures:
`pg_relation_filenode(regclass) → oid`,
`pg_relation_filepath(regclass) → text`, and
`pg_filenode_relation(oid,oid) → regclass`. Strict NULLs, missing numeric relation
OIDs and storage-less relations (including views and partition roots) yield
typed NULLs. The embedded engine has no PostgreSQL data directory: forward
inquiries for tables, indexes, sequences, materialized views and stored catalogs
raise `0A000`, rather than inventing files or treating a logical OID as a filenode.
Reverse lookup returns NULL because no physical mappings exist; tablespace zero
cannot manufacture one. Native Dump/Restore preserves logical metadata only.
Stock PostgreSQL instead returns actual filenodes and relative first-main-fork
paths; filenodes can change independently of relation OIDs. Temporary relations
have forward mappings on PostgreSQL but reverse lookup is NULL; native temporary
forward inquiries have the same explicit unsupported boundary as other stored
relations. No extra relation SELECT or server-file privilege is required.

Storage and native Go text remain UTF8/Unicode. The optional server supports
bidirectional `client_encoding` through actual registered PostgreSQL conversion
defaults, plus SQL_ASCII. Native byte-stream COPY uses the same encoding
boundary, with a separate explicit `ENCODING` file option; native SQL strings
and typed parameters are not reinterpreted. See [COPY](docs/DRIVER.md#copy).

SQL and PL/pgSQL procedures use standalone `CALL`, including named, default and
variadic arguments and declared OUT/INOUT result columns. Calls share the
executing statement's atomic effects and cancellation. Native fixture controls
preserve routine declarations and data; see [routine execution](docs/DRIVER.md).

Type inquiries use the same direct, prepared and stored expression paths:
`pg_typeof(NULL::integer)` returns the REGTYPE identity `integer`,
`format_type(1700,327686)` returns `numeric(5,2)`, and `pg_basetype` follows
nested domains without replacing nominal `pg_typeof` identity. Native fixture
controls preserve the underlying type graph and stored expressions.

Input diagnostics use `pg_input_is_valid(text, text) → boolean`
and `pg_input_error_info(value text, type_name text) → record(message text,
detail text, hint text, sql_error_code text)`. Only error-info supports the named
inputs `value` and `type_name`; the boolean inputs are unnamed. The target is a SQL type
declaration, including qualified names, arrays and typmods such as
`numeric(7,4)`, not a numeric REGTYPE shortcut. Both calls are strict: either
NULL argument yields SQL NULL. Successful error-info is instead a non-NULL
record with four NULL fields (`(,,,)` as text). Only producer-supported soft
input errors become false or diagnostics; unknown types, invalid declarations,
unsupported hard input routines and exceptions inside domain CHECK expressions
remain ordinary statement errors. Missing detail/hint are NULL; SQLSTATE is
the original five-character code. Direct, prepared and stored calls share the
typed input path; stateless builtins need no separate native dump state.

Session inquiries use ordinary typed SQL: `current_query()` returns the complete
submitted SQL, including comments, batches and prepared placeholders.
`pg_backend_pid()` identifies the embedded physical session, not an OS process.
`pg_postmaster_start_time()` and `pg_conf_load_time()` report cluster startup and
the actual initial settings load as timestamp-with-time-zone values. Session
identity and these cluster event clocks survive native Reset and Restore.

Foreign-table inquiry through `information_schema.foreign_tables` returns each
accessible table's database, schema, name and actual server association through
ordinary direct and prepared queries. Table or column privileges govern
visibility independently of server USAGE. Native fixture controls preserve the
logical foreign definitions and associations; metadata reads do not contact
remote servers or invoke foreign handlers.

Offline `CREATE SUBSCRIPTION ... WITH (connect=false)` registers its main
replication origin in the cluster-shared `pg_catalog.pg_replication_origin`.
Registration follows the subscription's transactional lifetime; it does not
start workers, contact a publisher or track replay progress.

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
library/path/preload strings and other absent mechanisms are
explicit **setting-only simulations**, not physical effects. The effective
`session_replication_role` controls user and foreign-key trigger firing through
their stored enable modes. `check_function_bodies=off` never admits invalid routines. Postmaster settings
still refuse live mutation; SET grants and restricted reads are independent.
See the [settings boundary inventory](internal/engine/settings/README.md#client-connection-defaults).

XML values use native document/content validation, XMLPARSE and XMLSERIALIZE,
with session `xmloption` controlling casts and parameter input. XML results scan
as strings; use character serialization when original declaration spelling is
needed instead of transport-normalized typed XML output.

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

`pg_catalog.pg_opfamily` exposes the five OID/OID/NAME/OID/OID identity
columns from immutable family owners. Method-qualified identity distinguishes
B-tree and hash families with the same name; `pg_opclass`, `pg_amop` and
`pg_amproc` retain their actual family/type/member relationships. Index creation
and removal do not manufacture builtin families. Family metadata is not an
advertisement of unsupported algorithms or callbacks. Preparation of catalog
mutations succeeds, but execution rechecks current authority: reader writes
fail with `42501` (`permission denied for table pg_opfamily`), and native owner
writes fail with `0A000` (`catalog source mutation is unsupported`), including
empty predicates. See the [catalog proof selectors](reference/catalog_objects/README.md#testing).

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
autovacuum are implied. `pg_class` and table-statistics views publish real
maintenance facts. Logical Dump/Restore transports checked extended and
single-column statistics alongside data.

`pg_restore_relation_stats(VARIADIC kwargs "any")` restores exact logical
`pg_class` estimates: `relpages`, `relallvisible` and `relallfrozen` require
`integer`, and `reltuples` requires `real` (use explicit casts). Required
`schemaname` and `relname` selectors are text name/value pairs. Invalid optional
fields warn and are skipped independently, returning false while valid fields
are retained; successful restoration returns true. `pg_clear_relation_stats`
returns nonnull VOID and writes newly-created defaults `(0,-1,0,0)`, without
clearing attribute statistics. Both routines require MAINTAIN or database
ownership; indexes use their parent table's authority. Imports are transactional,
including savepoints and prepared transactions, and never manufacture heap rows
or change physical counters. Logical Dump/Restore persists masked exact values
by symbolic identity, atomically replacing persistent state while retaining
destination TEMP state. Later ANALYZE/VACUUM may replace imported estimates.
TRUNCATE's new-file branch and ordinary heap rewrites reset estimates in
operation order, so later restore/clear survives commit. In-place TRUNCATE on a
heap created or assigned a new file in the same subtransaction preserves its
statistics while emptying rows. Nested savepoints retain that distinction.
Argument names
match ASCII case-insensitively under the C/UTF8 contract; Unicode lookalikes
are unknown names, not aliases for required selectors or `version`.

The fifteen-column read-only `pg_stat_progress_vacuum` view reports real admitted
VACUUM workers, actual phases and completed logical work. Authorized counters
are numeric (including zero); restricted observers retain only worker/database
identity. Rows end with their target, including cancellation and failure.
See [VACUUM progress](docs/DRIVER.md#vacuum-progress) for estimate formulas,
phase-local reset rules, visibility and lifetime.

The six-column read-only `pg_stat_progress_basebackup` view is genuinely empty:
there are no physical base-backup workers, and native logical Dump never
fabricates a progress row, even during blocked output. Ordinary SQL
`BASE_BACKUP` is unsupported. See [physical base-backup progress](docs/DRIVER.md#physical-base-backup-progress)
for the exact schema, read-only diagnostics and upstream visibility/NULL rules.

The sixteen-column read-only `pg_stat_progress_create_index` view reports actual
admitted index work: existing CREATE INDEX variants, supported CONCURRENTLY
writer/snapshot waits, partition builds and ordinary REINDEX. Rows disappear
after success, failure or cancellation; an ordinary initial table-lock wait
does not fabricate a row. Current observer privileges determine whether the
thirteen work-detail columns are visible or NULL. Authorized unknown totals are
zero. Only block counters approximate physical work, using completed logical
128-row-version scan units; native scans/sorts do not pretend to load physical
B-tree pages. See the [index-progress design](docs/design/PG18-VIEW-PG-STAT-PROGRESS-CREATE-INDEX.md)
for phase-local counters, lifetime and deterministic public smoke.

`CREATE STATISTICS` supports single expressions and multivariate combinations
of columns and expressions. Expressions are bound once to the target relation;
stable and volatile functions are permitted, without evaluating them at CREATE.
`ndistinct`, `dependencies` and `mcv` are declaration kinds; expression facts are
automatic. `DROP STATISTICS` supports conditional and dependency modes.
Definitions appear in `pg_statistic_ext`, including genuine `stxexprs` trees;
`pg_get_expr` resolves renamed inputs from their retained attribute identities.
Creation does not collect payloads. `ANALYZE` computes joint ndistinct,
functional dependencies and multivariate MCV from actual NULL-inclusive rows,
including signed expression dimensions. MCV frequencies use full-population
joint counts and products of marginal frequencies before target truncation.
SQL NULL markers are separate from dictionary values. Per-expression
NULL/distinct/MCV/histogram summaries remain genuine `pg_statistic[]` values in
`pg_statistic_ext_data.stxdexpr`, with array subscripting and row-field access.
`ALTER STATISTICS [IF EXISTS] name SET STATISTICS {integer|DEFAULT}` controls
each object's target: zero retains its old payload without rebuilding, and
DEFAULT uses the command's `default_statistics_target`. Positive targets bound
MCV entries without renormalizing frequencies; native ties follow first captured
occurrence deterministically. Native counts cover the full visible population;
PostgreSQL can sample larger populations. Counts are technical
statistics and never approximate substantive SELECT results.
Native versioned comments preserve stale values, target-zero values, absence,
and local/inherited flavors without reanalysis. PostgreSQL safely ignores these
comments when replaying the same definition/target SQL; PostgreSQL18 dumps do
not preserve collected extended statistics. Native replay preserves stale joint
and expression summaries, not a recomputation from restored rows. Typed dictionary
constructors remint destination domain, enum-member and array-element identities;
source-OID-bearing physical bytes never grant destination authority. DATA catalog
SELECT privileges protect distributions independently of table ownership.
Statistics types continue rejecting non-NULL SQL input; catalog-owned typed
output supplies no input authority. These facts do not add a planner consumer.


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
