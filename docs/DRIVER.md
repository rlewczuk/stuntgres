# Driver and connection lifecycle

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## Database names and lifetime

The DSN is an **exact opaque string**, not a libpq connection string or network address. Identical strings (including the empty string) within the same Driver share an engine; different strings or Driver instances are isolated. The registered `stuntgres` driver uses one default Driver. Engines live as long as their Driver, even after every connection or pool closes. Use `NewDriver().OpenConnector(name)` with `sql.OpenDB` for independently owned fixtures. Nothing is shared between processes or persisted across process exit.

Random functions share one generator per physical connection. Pin a `sql.Conn`
when calling `setseed` and reading a repeatable series: interchangeable pool
queries need not use the same session. Returning a borrower to the pool preserves
the stream; closing/replacing the physical session discards it without losing
database tables. Draws and successful reseeding are nontransactional, including
savepoint rollback, and survive native catalog Reset/Restore. Prepared calls
describe types without drawing; each execution uses its current physical session.
No generator state is serialized into Dump. Feature smoke:
`make smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.

## Notices and warnings

```go
base, err := stuntgres.NewDriver().OpenConnector("example")
if err != nil { return err }
configured := base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
    // n is an owned value; retain it if needed. Optional fields remain conditional.
    fmt.Println(n.Severity, n.Code, n.Message)
})
db := sql.OpenDB(configured)
defer db.Close()
```

WithNoticeHandler returns an immutable copy; nil disables delivery. Configuration
applies to newly created physical connections only, never other connectors sharing
the same database. Ordinary sql.Open is silent (no default stderr logging).
CREATE TABLE IF NOT EXISTS emits NOTICE/42P07 when the exact creation destination
already exists, leaving its definition and data unchanged. Fresh creation is real
and silent. Semantic column/type/LIKE validation occurs at execution, after the
existing relation check; malformed syntax still fails during parsing. An existing
target skips unknown types, missing LIKE sources and valid but unsupported LIKE
options, without claiming support for the skipped declaration.
Current schema CREATE authorization is checked before either creation or an
existing-relation skip, including a skip after a competing publication. A skip
does not require ownership or ordinary table privileges and never changes the
existing owner. Denied CREATE emits no skip notice. Session-authorization
switching and reset preserve `client_min_messages`; notice-handler connector
copies preserve the asserted authenticated role.
Cancellation detected before a same-name publication-conflict decision remains
an ordinary context error, not duplicate-table failure or successful skipping;
the winning table remains intact and the canceled loser emits no notice.
Long application_name emits NOTICE/42622 with original and clipped text.

SET/SHOW client_min_messages uses the same physical settings route, default
notice. Case-insensitive values are debug5, debug4, debug3, debug2, debug1, log,
notice, warning, error; debug canonicalizes to debug2 and info is also accepted.
INFO always passes; errors remain returned errors at every threshold, never
callbacks. Invalid values return 22023 with an available-values Hint without
changing the setting. DEFAULT restores the captured physical default.

Callbacks run synchronously in producer order after all engine locks and the
operation lease are released, before command return; payload strings are immutable
owned Go data. User code is not context-preemptible: cancellation during a callback
does not undo committed execution or replace its result. Reenter through another
already held physical connection or a separate pool with free capacity, **not**
the same sql.Conn or an exhausted originating pool. Preparation emits no execution
notices. Native controls do not serialize or deliver staging notices.
No native DO/RAISE, warning-producing procedural SQL, COPY or asynchronous
LISTEN/NOTIFY support is implied.

`go test -count=1 -run '^TestNoticesSmoke$' ./smoke` exercises actual notices,
filters, ownership, peer reentry and control round trips; Go test reports the result.


Run `go test -count=1 -run '^TestSessionSmoke$' ./smoke` for asserted isolation, preparation, physical
reuse/replacement, data survival, diagnostics, cancellation and native controls.
Scoped checks: `go test -run '^TestSession' ./...`,
`go test -race -run '^TestSession' ./...`, and the real pinned PostgreSQL suite
`go test -tags=reference -run '^TestReferenceSession' -count=1 ./reference`.

Session smoke asserts distinct session settings, prepared TEXT metadata and zero
affected counts, preserved settings on pool reuse, restored defaults on physical
replacement with surviving data, data retention after pool reopening,
42704/22023/42601 diagnostics, and cancellation/control preservation.
Go test reports the result.

## SQL errors and diagnostics

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

Native SQL failures supply both severity fields as `ERROR`, including parser, missing-object and duplicate-object failures; naming an object in the primary message does not imply an object diagnostic field. The error-fields feature smoke is `go test -count=1 -run '^TestErrorFieldsSmoke$' ./smoke`: it checks exact syntax and engine diagnostics, direct/prepared NOT NULL failures, statement atomicity, wrapping, ordinary context cancellation and native control round trips. Go test reports the result. Reference-only permission, procedural and SQL cancellation witnesses exercise PostgreSQL diagnostic transport expectations, not native implementations of those unsupported features.

Shared generic SQL diagnostics construct only severity, SQLSTATE and primary message; they never infer object identities. In particular, input/range conversion errors (`22P02`/`22003`) do not infer Schema, Table or Column from a destination or WHERE predicate. Condition-specific NOT NULL producers (`23502`) supply schema/table/column: INSERT includes complete failing-row Detail; UPDATE includes only modified targets and effectively SELECT-readable columns, in physical order. Without relation-level SELECT, UPDATE prints `(names) = (values)`, even when column grants cover every column; table SELECT, default owner authority and superuser retain the complete unlabelled row. Protected retained values are neither formatted nor disclosed, and retaining them does not require SELECT. Captured output settings and UTF8-safe field truncation are unchanged. SET/ADD existing-row validation has no Detail. Even a named constraint leaves the Constraint field empty. This distinction follows PostgreSQL's [conditional error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html); clients must not infer the presence of one object field from another.

Invalid boolean strings retain the original decoded input in the PostgreSQL `22P02` primary message (for example, `invalid input syntax for type boolean: "o"`). Boolean WHERE string-literal failures carry the one-based character Position of the source opener, including actual preparation and empty tables. Escapes and continued strings do not change that provenance. Typed parameter input failures have no query Position and carry `unnamed portal parameter $n = '...'` at the actual failing import index (`''` for empty text). Only failed SQL input diagnostics are copied and annotated; ordinary Go errors and later expression/preflight conversion errors remain unchanged. No destination object identities are inferred.

Encoding-input failures (`22021`) carry only the actual parameter index, because decoded parameter text is unavailable. Codec errors on decoded empty text retain ` = ''`; other decoded text retains ` = '...'`.

This bounded diagnostic change does not replace boolean parsing or native `database/sql` values: accepted aliases/unique prefixes, case/ASCII whitespace, TRUE/FALSE/NULL, bool/nil returns, and atomic INSERT and Dump/Reset/Restore behavior remain baseline regressions. Run `go test -count=1 -run '^TestBooleanDiagnosticsSmoke$' ./smoke` for complete native diagnostic, direct/prepared parameter, atomicity and control witnesses; Go test reports the result. The independent pinned-reference fixture is `go test -tags reference -run '^TestReferenceBooleanDiagnostics$' -count=1 -v ./reference`.

Unterminated comments and quoted tokens report PostgreSQL-style English primary messages containing the raw input from the offending opener (`at or near "..."`), with a separate one-based character Position. Newline-separated string segments form one token, so an unterminated later segment points to the first literal opener and includes the continued raw suffix. A zero-length delimited identifier instead includes only its raw `""` token, not trailing SQL. Diagnostic text is not escaped as a Go string; multibyte characters do not turn Position into a byte offset.

INSERT target-resolution and assignment diagnostics interpolate decoded identifiers between literal double quotes, matching PostgreSQL diagnostic prose rather than Go escaping or SQL identifier quoting. The feature smoke includes an embedded-quote column mismatch and checks its exact diagnostic and complete sentinel preservation.

Decoded E-string NUL/invalid UTF8 reports `22021` with no Position; malformed Unicode reports `22025` with a format Hint, while invalid Unicode values/surrogate pairs and unterminated literals report `42601` with original-source character positions. Raw SQL NUL/invalid UTF8 intentionally retains native positioned `42601` preflight diagnostics, distinct from decoded bytes. Bound text is not SQL escape source; string-family input rejects NUL/malformedUTF8 with22021 byte diagnostics and no native wire Context. B/X/N strings remain unsupported; `standard_conforming_strings` is configurable as documented in [session settings](SETTINGS.md). Dumps use ordinary strings, preserving decoded controls and literal backslashes.

The focused public proof is `go test -count=1 -run '^TestStringLiteralsSmoke$' ./smoke`; it asserts direct/prepared escape and dollar values, parameter separation, exact diagnostics, statement atomicity, shared-pool Dump/Reset/Restore, retained cursors and the raw SQL boundary. Go test reports the result.

Pinned real-reference feature fixtures are `go test -tags reference -run '^TestReferenceStringLiterals$' -count=1 -v ./reference` and `go test -tags reference -run '^TestReferenceStringLiteralDumpRoundTrip$' -count=1 -v ./reference`.

## B-tree utility preparation and controls

Direct Exec and database/sql prepared Execute support ordinary CREATE INDEX;
Prepare retains syntax and empty utility description, not target/row/owner
visibility. Each Execute binds current target, role, search_path and keys.
Utilities return zero affected rows. Missing objects/keys and duplicate names
fail at Execute with full PostgreSQL18.0 diagnostic records (missing key has no
Position); lexical/syntax failures still occur at Prepare.

Native Dump records actual canonical CREATE INDEX definitions with direction
and NULL options/current parent/key names. Restore uses isolated replay and one
atomic replacement, rebuilding keys with destination identities. A bad late
index definition leaves destination rows/definitions unchanged. These controls
require the unwrapped native DB, not a foreign or sqlmw-wrapped pool.
The same emitted SQL can be replayed against PostgreSQL.
Writable explicit CREATE INDEX transactions remain a native 0A000 boundary,
independently of read-only 25006 utility rejection.

