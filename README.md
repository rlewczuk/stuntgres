# stuntgres

A process-local, embedded PostgreSQL-compatible Go `database/sql` driver for a deliberately bounded SQL subset. Production code uses only the Go standard library; no server, network, container, filesystem storage, or durability is involved. Requires Go 1.26 or later.

```go
import (
    "context"
    "database/sql"
    _ "stuntgres"
)

func example() error {
    db, err := sql.Open("stuntgres", "example")
    if err != nil { return err }
    defer db.Close()
    _, err = db.ExecContext(context.Background(), "CREATE TABLE people (id INTEGER NOT NULL, name TEXT)")
    return err
}
```

## Database names and lifetime

The DSN is an **exact opaque string**, not a libpq connection string or network address. Identical strings (including the empty string) within the same Driver share an engine; different strings or Driver instances are isolated. The registered `stuntgres` driver uses one default Driver. Engines live as long as their Driver, even after every connection or pool closes. Use `NewDriver().OpenConnector(name)` with `sql.OpenDB` for independently owned fixtures. Nothing is shared between processes or persisted across process exit.

## Supported SQL

```text
CREATE TABLE [public.]name (column type [NULL | NOT NULL] [, ...])
INSERT INTO [public.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
SELECT (* | column [, ...]) FROM [public.]name
       [WHERE column = value] [ORDER BY column [ASC | DESC]]
type  := INTEGER | INT | INT4 | BIGINT | INT8 | TEXT | BOOLEAN | BOOL
value := signed-64-bit integer | 'string' | TRUE | FALSE | NULL | $n

Relation names accept one or two identifier components. Explicit `public.name` and unqualified `name` resolve to the same relation; unqualified references check `pg_catalog` before the built-in `public` namespace. SQL creation is supported only in `public`. Namespace and relation descriptors carry engine-local epoch-scoped identities, while rows remain stored by relation identity; Reset/Restore replace the authority atomically and retained result snapshots remain usable. Built-in namespace identities are internal infrastructure, not SQL catalog tables.

Unquoted names fold ASCII uppercase and truncate to 63 UTF-8-boundary bytes; quoted names preserve case and decode doubled quotes. A quoted identifier containing a dot is one component, e.g. `public."a.b"`. Strings use doubled apostrophes, literal backslashes and newline-separated adjacent-string concatenation. Whitespace, line comments and nested block comments are accepted. Parameters are 1-based `$n`, inferred from insert destinations or equality columns. Repeated parameters of one canonical destination type share a binding; gaps fail with `42P18`, mixed destination types with `0A000`. Named arguments and other parameter dialects are not supported.

Types are int4/int8/text/bool. Values returned to Go are `int64`, `string`, `bool`, or nil. INSERT coerces unknown strings and supported bound values through PostgreSQL-style textual input; typed integer/bool literals can assign to text but not each other. WHERE typed comparisons do not use those assignment casts. Parameters support nil, integer, string, bytes and bool after standard `database/sql` conversion, not floating point or timestamps. NULL equality is never true. ASC uses NULLS LAST; DESC uses NULLS FIRST. Text ordering uses C-locale bytewise comparison. INSERT is all-or-nothing, including multirow errors and cancellation before publication.

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted supported columns receive NULL (the supported DDL has no declared-default expressions). Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Conversion, arity and NOT NULL failures publish no rows from the statement. The public feature smoke is `go run ./cmd/insert-implicit-smoke`.

No configurable `search_path`, `CREATE SCHEMA`, schema-qualified creation outside `public`, three-part/database qualification, aliases, joins, expressions, functions, casts, constraints beyond nullability, defaults, serial, primary keys, IF NOT EXISTS, UPDATE/DELETE, RETURNING, multiple sort keys, explicit transactions, ACLs, or SQL catalog projections are supported. Recognized unsupported features fail with SQLSTATE `0A000`; malformed SQL fails with `42601`. Unsupported suffixes and multiple statements are never silently ignored. Ordinary driver operations accept at most one statement; empty Exec succeeds and empty Query returns an empty cursor. Query may execute CREATE/INSERT; Exec may execute SELECT and reports selected count. LastInsertId is unsupported.

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

## Native fixture controls

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity and existing pools. Dump writes deterministic, schema-then-relation-sorted PostgreSQL-compatible plain CREATE/INSERT SQL, with separately quoted schema/relation components, declaration-ordered columns and canonical literals. It is not complete `pg_dump` output, COPY, or catalog export.

Restore accepts supported CREATE/INSERT scripts without parameters, builds a staging engine, then atomically **replaces** the descriptors, identity-keyed row storage and epoch, never merges. Empty input clears user relations. Read, parse, execution and cancellation errors leave original data untouched. Reset/Restore publication supersedes earlier completed writes. Existing SELECT snapshots remain valid across inserts and catalog replacement. Writer errors do not change the database.

## Catalog-part agent workflow

OMP discovers `/implement-part <part-symbol>` from `.omp/commands`, with coordinator/review skills and dedicated scout, implementer and reviewer roles. Planning reuses OMP's native `/plan` instructions and auto-approval via `--plan-yolo`, rather than a custom planner agent/system prompt. This is workflow infrastructure, not an implementation of the future SQL catalog. It reads existing ignored `.cswd/spec_parts.json` and `.cswd/spec_status.json`; only a selected part with completed prerequisites may start.

Invoke from a clean named integration branch. Research and a frozen `.cswd/parts/<symbol>.md` plan precede an owned Git worktree. The helper runs generation, all unit/race/reference-tagged tests, public smoke, standard-library dependency checks and the planned feature smoke. Exact-tree review must route through externally configured `@csw-review-2`; critical/high findings require fixes and fresh verification/review. Implementer `prewalk: true` uses configured `@smol` without changing model settings.

The planning adapter `.omp/bin/plan_part.py` runs native planning non-interactively and exports the existing four-field planner JSON contract. Native auto-approval permits only authoring/export of that plan, not implementation: a trusted extension permits read-only tools plus canonical session-local plan writes/proposal, even after approval. A per-invocation safety overlay disables native plan autosave; user/project settings and model mappings are untouched. Native event/guard evidence is retained beside the result, and absent approval, stale/invalid output or attempted forbidden execution fails closed. The coordinator then ingests the exported plan through the lifecycle helper before creating the implementation worktree.

Only verified, approved content is committed and fast-forwarded to the latest integration tip. Rebase/conflict resolutions require full reverification. Status becomes `done` only after ancestry proof; failures retain worktrees, branches and journals under the Git common directory's `implement-part/`. Use helper `resume` for interrupted runs; never manually reset/stash the integration checkout. The coordinator skill documents all helper actions, evidence schemas and recovery rules.

Infrastructure regression proof uses disposable Git repositories and a tiny Go fixture, not real catalog parts or PostgreSQL coverage:

```sh
python3 -m unittest discover -s .omp/bin -p 'test_*.py' -v
python3 .omp/bin/implement_part.py select --repo "$PWD" PG18-ARCH-CATALOG-IDENTITY
```

`select` is read-only. A real `begin` refuses uncommitted user changes, including an edited `.gitignore`. No real part is executed during infrastructure installation.

## Development and verification

```sh
make generate   # pinned goyacc; checked-in parser generation
make test       # Docker-free behavioral tests
make reference  # real pinned PostgreSQL differential fixtures
make smoke      # actual public database/sql and controls scenario
make bench      # fresh engine + connector + pool + Ping + Close, five runs
go test -race ./...
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
```

Every nonblank production dependency entry must be `stuntgres` or start with `stuntgres/`. goyacc is a generation tool, not a runtime dependency. lib/pq, sqlmw and testcontainers are test-only. Benchmark startup target is below 1,000,000 ns/op; report observed allocations and timings, not a timing-sensitive test assertion.

The public smoke verifies rows, metadata, affected counts, Dump/Reset/Restore and a second already-open pool, then prints exactly:

```text
1 Ada true
2 O'Brien false
reset: 42P01
restore: 2 rows
```

The explicitly enabled reference suite requires working Docker and image/module access. It uses one PostgreSQL 18.0 container pinned to `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C locale, serialized fixtures and public-schema resets. sqlmw records both direct and prepared paths; native memory handles remain available for controls. Fixtures assert independently specified outcomes in addition to parity. Reference startup failures are errors, not silent skips or mock fallbacks.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
