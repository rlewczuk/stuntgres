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

### Physical sessions

Each physical connection has independent `application_name` and
`client_min_messages` state. Hold
`*sql.Conn` to target that session; a `*sql.DB` may dispatch to any physical
connection. Borrower Close returns it to the pool: healthy pool reuse preserves
its settings and identity, without implicit RESET, DISCARD or rollback.
Physical replacement starts with empty application_name and does not erase data.
Canceled statements, canceled pool-reuse checks and normal SQL errors leave
healthy sessions reusable, preserving physical identity and settings.
Explicit physical close drains operations and releases only that session's
owners; runtime abandonment cleanup is nondeterministic best effort.

`SET [SESSION] application_name {TO | =} value` and `SHOW application_name`
use the ordinary direct/prepared execution route. Values are strings using the
existing ordinary/`E'...'`/`U&'...'`/dollar-quoted literal lexer, identifiers, booleans, signed
decimal-digit integer tokens or bare DEFAULT. DEFAULT
restores the captured physical-session default; quoted 'DEFAULT' is literal.
SHOW returns one string row with column application_name and type TEXT.
Both SET and Exec of SHOW affect zero rows; SHOW snapshot cardinality is not a
command count. Unsigned decimal tokens fitting PostgreSQL's int32 scanner range
are formatted canonically; larger tokens retain their original digits (including
leading zeros), with unary minus prepended and unary plus omitted.
Unquoted radix-prefixed and underscore-separated numeric setting values,
including signed forms, are unsupported (`0A000`) and cannot mutate settings.
Quoted `'1_000'` and `'0x2a'` remain literal strings. This setting-specific
boundary does not change the shared lexer's INSERT/SELECT numeric support.
Values clip original UTF8 to 63 bytes before escaping nonprintable/non-ASCII
bytes as lowercase literal `\xNN`. Clipping succeeds and emits a structured
NOTICE when a connector handler is configured and the session filter permits it.
SET reuses the existing literal decoding rules below: for example,
`SET application_name TO E'a\nb'` decodes the newline first, then SHOW returns
literal `a\x0ab`. No second setting-specific string decoder is used.

This is not full PostgreSQL setting syntax: LOCAL, RESET, SHOW ALL, other
built-ins besides client_min_messages, dotted custom GUCs and floating/exponent numbers remain
unsupported. NULL, parameters and unquoted reserved TO are syntax errors;
quoted "to" remains an identifier. Comma lists fail 22023 before setting lookup,
with the supplied name retained in the diagnostic. Unknown undotted scalar
names fail 42704; messages interpolate decoded names between literal double
quotes without Go string escaping. No logical database/admission/role or
transaction features, named SQL prepared resources, cursors, temporary objects
or advisory locks are implied. Native Reset/Restore preserve session settings;
Dump excludes them. Anonymous retained SHOW/SELECT results outlive session end.
Internal immutable setting definitions/default providers and exactly-once
resource Attach/quiescent Detach are extension hooks, not implementations of
these future resources.

### Notices and warnings

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
and silent. Semantic column validation occurs at execution, after the existing
relation check; malformed/unsupported syntax still fails during parsing.
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

`go run ./cmd/notices-smoke` exercises actual notices, filters, ownership,
peer reentry and control round trips; sole successful output is `notices: ok`.


Run `go run ./cmd/session-smoke` for asserted isolation, preparation, physical
reuse/replacement, data survival, diagnostics, cancellation and native controls.
Scoped checks: `go test -run '^TestSession' ./...`,
`go test -race -run '^TestSession' ./...`, and the real pinned PostgreSQL suite
`go test -tags=reference -run '^TestReferenceSession' -count=1 .`.

Session smoke prints only these asserted success labels:

```text
session isolation: session-a session-b
prepared settings: TEXT affected=0
pool reuse: preserved
physical replacement: default restored, data=7
pool reopen: data=7
session failures: 42704 22023 42601
session cancellation and controls: preserved
```

## Supported SQL

```text
CREATE TABLE [public.]name (column type [NULL | NOT NULL] [, ...])
INSERT INTO [public.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
SELECT (* | column [, ...]) FROM [public.]name
       [WHERE column = value] [ORDER BY column [ASC | DESC]]
type  := INTEGER | INT | INT4 | BIGINT | INT8 | TEXT | BOOLEAN | BOOL
value := signed-64-bit integer | 'string' | E'escape string' | U&'Unicode string' [UESCAPE 'escape'] | $$string$$ | $tag$string$tag$ | TRUE | FALSE | NULL | $n
```

Relation names accept one or two identifier components. Explicit `public.name` and unqualified `name` resolve to the same relation; unqualified references check `pg_catalog` before the built-in `public` namespace. SQL creation is supported only in `public`. Namespace and relation descriptors carry engine-local epoch-scoped identities, while rows remain stored by relation identity. Internal object references also retain a stable engine-owner token: coincident numeric identities in separate instances are never interchangeable. Reset/Restore preserve that owner while atomically replacing the catalog lifetime, and retained result snapshots remain usable. Built-in namespace identities are internal infrastructure, not SQL catalog tables.

Unquoted names fold ASCII uppercase and truncate to 63 UTF-8-boundary bytes; quoted names preserve case and decode doubled quotes. A quoted identifier containing a dot is one component, e.g. `public."a.b"`. Ordinary strings use doubled apostrophes and literal backslashes. `E'...'` (or lowercase `e`) additionally decodes escaped apostrophes/backslashes, `\b\f\n\r\t\v`, one-to-three octal digits, `\x` with one-to-two hex digits, and `\uXXXX`/`\UXXXXXXXX` Unicode scalars, including adjacent surrogate pairs. Unknown backslash escapes retain the following character. Octal/hex escapes produce bytes, and the complete decoded token must be valid UTF8 without NUL. Adjacent bare quoted segments concatenate only across whitespace containing LF or CR (line comments allowed, block comments excluded), inheriting the first segment's ordinary/escape mode; UTF8 bytes may span segments, surrogate pairs may not. Whitespace, line comments and nested block comments are accepted elsewhere. Parameters are 1-based `$n`, inferred from insert destinations or equality columns. Repeated parameters of one canonical destination type share a binding; gaps fail with `42P18`, mixed destination types with `0A000`. Named arguments and other parameter dialects are not supported.

Dollar-quoted strings preserve their bodies exactly: apostrophes, backslashes, newlines, comments, semicolons and `$1` are text, not escapes, statements or parameters. `$$...$$` uses an empty tag; nonempty tags follow unquoted identifier spelling except that `$` is forbidden in the tag. Delimiters match case-sensitively, without folding or the identifier length limit. Separate a dollar literal from a preceding identifier/keyword with whitespace or punctuation: `VALUES$$v$$` is one identifier, not `VALUES` plus a string. A complete opener without an identical closer reports `42601`, `unterminated dollar-quoted string at or near "..."`, quoting the raw suffix and pointing to the opener's character position. An incomplete nonparameter opener such as `$tag` is instead syntax near `$`; digit-prefixed `$n` remains a parameter.

Identifier classification uses the complete PostgreSQL 18 release keyword inventory, including keywords whose commands are not implemented. Unqualified relation names, first relation components and column names accept unreserved and column-name keywords (`abort`, `by`, `insert`, `between`, `char`); reserved and type/function-name keywords require quoting in those positions (`"select"`, `"join"`). A relation component after the dot accepts every keyword category, so `public.select`, `public.authorization` and `public.from` are legal names and resolve identically to their quoted counterparts. Keyword identity is separate from supported type spelling: `bool`, `int4`, and `int8` are ordinary identifiers and retain their existing built-in type meaning in type position. The independent bare-label property does not imply alias support: valid labels such as `check` or `authorization`, and labels after `AS`, still fail with `0A000` / `projection aliases are unsupported`; malformed bare labels such as `char` and `day` fail with `42601`. Quoting, ASCII folding, UTF8 clipping, qualifications and original-source diagnostic positions are unchanged. See [PostgreSQL's keyword categories](https://www.postgresql.org/docs/18/sql-keywords-appendix.html).

IF and EXISTS retain their release unreserved and column-name categories, respectively, even when their syntax tokens implement `CREATE TABLE IF NOT EXISTS`. Both remain usable as unquoted identifiers and bare labels in the corresponding contexts; dedicated syntax recognition does not change their keyword metadata.

INSERT query inputs remain unsupported, including parenthesized SELECT inputs. Their bounded grammar recognition does not execute a query or publish data: a recognized query produces `0A000` only after the whole script is syntactically valid. In `INSERT INTO t (select) VALUES (1)`, `(select)` is a parenthesized zero-target SELECT rather than a target column, and the following raw `VALUES` is the syntax error (`42601`, character position 24).

Generic function-name recognition follows the distinct IDENT/unreserved/type-or-function-name rule, not column-name legality: `between(x)` is malformed, while ordinary/U/T call names and separately recognized built-in constructs such as COALESCE remain unsupported (`0A000`). No function, cast or special expression is executed by this recognition. DEFAULT is a valid bare projection label but still an unsupported alias. ISNULL/NOTNULL are recognized null-test expressions rather than malformed bare labels, and explicit `ORDER BY x NULLS FIRST/LAST` is recognized but unsupported; quoted operator spellings remain ordinary names/labels. All these failure boundaries retain complete positioned diagnostics without global keyword-to-error promotion.

The same failure-only call guard applies at every named projection position: `SELECT x, abs(x) FROM t` and later COALESCE or quoted/type-function-name calls remain recognized unsupported features, while a later unquoted `between(x)` is malformed at its opening parenthesis. Recognition retains the original callee token and never reconstructs category identity from normalized projection strings or executes the call.

NULLS ordering is admitted only when the next actual unquoted keyword token is FIRST or LAST (comments and case folding use the existing scanner). A one-token filter caches that token without rescanning. Invalid or quoted alternatives, or end of input after NULLS, leave the original keyword as the syntax error: `ORDER BY x NULLS bogus` reports `42601` at raw NULLS, not at bogus. The filter preserves original lexemes, character positions and scanner-error precedence; it neither changes keyword metadata nor enables explicit NULL ordering.

Run `go run ./cmd/identifier-smoke` for direct/prepared contextual names, complete diagnostics and native control round trips. It prints exactly `identifiers: categories, contexts, diagnostics and round trips OK` only after all assertions succeed. Pinned real-reference fixtures are `go test -tags reference -run '^TestReferenceIdentifier.*$' -count=1 -v .`; alias-valid queries deliberately assert PostgreSQL rows and native unsupported diagnostics separately.

Unicode escaped constants `U&'\0041'` and quoted identifiers `U&"\0041"` decode to `A`, preserving quoted-name case. Four hexadecimal digits or `+` and six digits specify a scalar; adjacent high/low surrogate escapes combine (for example `U&'\D83D\DE00'` is 😀). Doubling the escape character yields it literally. Optional `UESCAPE '!'` selects exactly one byte other than hexadecimal digits, `+`, either quote or scanner whitespace; normally decoded E/dollar strings are also valid arguments. Bare newline-separated string segments concatenate before Unicode decoding. Names clip decoded UTF8 at 63 bytes without splitting a character. Standard-conforming strings are fixed behavior here, not a configurable GUC; B/X/N strings remain unsupported.

U& malformed digits, invalid scalar/surrogate values and invalid UESCAPE arguments report `42601`; malformed digits include Hint `Unicode escapes must be \XXXX or \+XXXXXX.` This differs from malformed E-string Unicode's `22025`. Decoder positions follow PostgreSQL's dequoted/concatenated-body arithmetic, so doubled quotes or removed continuation separators can make the cursor precede the raw escape. Raw lookahead lexical failures and UESCAPE argument validation take precedence over body decoding.

Run `go run ./cmd/unicode-escapes-smoke` for direct/prepared Unicode values/names, complete diagnostics, two-pool atomicity, cancellation and native-control round trips. It prints only:

```text
Unicode escaped constants and identifiers: ok
```

Types are int4/int8/text/bool. Values returned to Go are `int64`, `string`, `bool`, or nil. INSERT coerces unknown strings and supported bound values through PostgreSQL-style textual input; typed integer/bool literals can assign to text but not each other. WHERE typed comparisons do not use those assignment casts. Parameters support nil, integer, string, bytes and bool after standard `database/sql` conversion, not floating point or timestamps. NULL equality is never true. ASC uses NULLS LAST; DESC uses NULLS FIRST. Text ordering uses C-locale bytewise comparison. INSERT is all-or-nothing, including multirow errors and cancellation before publication.

The internal typed foundation uses catalog-owned immutable descriptors (BOOL OID 16,
INT8 20, INT4 23, TEXT 25; analysis-only UNKNOWN 705), concrete nullable values,
and bound assignment/comparison providers. The parser retains raw type syntax;
the engine resolves it and publishes parameter/result descriptions with column
origins. Prepared types come from destination columns, never the Go argument
kind. Same-type repeated parameters share one inferred slot; gaps fail `42P18`
and mixed-destination reuse remains the bounded native `0A000` limitation,
including INT4/INT8 reuse. Each execution imports an owned private typed frame
against the current coherent binding. Driver adapters detach bytes and export
INT4/INT8 as `int64`, TEXT as `string`, BOOL as `bool`, and typed NULL as nil.
NULL still retains its concrete metadata identity.
Normalized integer parameters targeting INT4/INT8 and Boolean parameters targeting
BOOL use checked scalar imports without successful-value formatting or reparsing.
They retain the inferred descriptor and cancellation checks; INT4 overflow keeps
the textual-input diagnostic rather than the assignment-narrowing diagnostic.

Integer textual input (quoted constants and string/byte parameters) accepts
optional signs, leading/trailing C whitespace, decimal leading zeros, radix
prefixes and single digit separators, including `0x_FF`, `0o17`, `0b10_10` and
`1_000`. Malformed input is `22P02`; width overflow is `22003`. Parsing order
matters: `2147483648x` is malformed integer input, but a sufficiently long
overflowing digit prefix can fail range before trailing junk. Non-ASCII
whitespace is not C whitespace. Boolean input supports case-insensitive unique
prefixes of true/yes/on and false/no/off, plus 1/0. Boolean codec text output is
t/f; Boolean assignment to TEXT is true/false, independently of driver output.
Integer WHERE literals compare across widths without assignment narrowing:
`int4_column = 2147483648` is valid and matches nothing, while a parameter in
that predicate retains INT4 input and rejects the same overflowing bound text.

`go run ./cmd/type-core-smoke` exercises public direct/prepared inference,
codecs, exact diagnostics and phases, metadata, byte ownership, multirow
atomicity, cancellation and four-kind native round trips with retained raw
cursors. It prints exactly:

```text
typed core: inference, codecs, metadata, atomicity and native round trips OK
```

Focused independent witnesses are `go test -run '^TestTypeCore.*$' -count=1 -v .`,
`go test ./internal/sqltype ./internal/engine`, and the pinned real-reference
`go test -tags reference -run '^TestReferenceTypeCore.*$' -count=1 -v .`.
Dump uses descriptor-owned canonical SQL literals, and staged Restore uses the
same binder/codecs before replacing the catalog once; existing snapshots retain
their old values, types and column metadata.

Integer SQL constants accept decimal and case-insensitive binary `0b`, octal `0o`, or hexadecimal `0x` prefixes. Single underscores may separate digits; radix constants may also have one underscore immediately after the prefix (`0B_1_010`, `0O_1_2`, `0X_A` all equal 10). Decimal leading zeros remain decimal (`010` is 10, `08` is 8). Unary signs may be separated from the number by whitespace/comments. Values must fit signed int64, including `-0x8000_0000_0000_0000`; int4 assignment additionally checks its own range. Malformed digits, separators and trailing identifier junk fail the entire statement with PostgreSQL English `42601` diagnostics pointing to the number's first character (not its sign).

This is not PostgreSQL's full numeric inference: lexically valid constants outside signed int64 retain the native `22003` / `integer literal out of range` capability boundary, while PostgreSQL can infer exact `numeric` values. Fractional/exponent numeric constants and bit-string constants remain unsupported (`0A000`). SQL integer constants and integer textual input have distinct lexical/input diagnostics, though both support the baseline radix/separator forms. Dump emits canonical decimal integers, and Restore accepts the supported integer spellings. Run `go run ./cmd/numeric-literals-smoke` for the public feature contract; it prints exactly `numeric literals: ok` only after its assertions succeed.

Lexical errors take precedence over the unsupported-valid-exponent boundary: `1e3` and `1e+3` remain unsupported, but `1e3foo`, `1e3_`, `1_000e3__0` and `1e+3foo` fail with `42601`, quoting the complete malformed numeric token.

Radix and separated constants follow the same prepared assignment contract as decimal integers: `INSERT INTO t VALUES (1),(0x8000_0000)` into an INT4 column prepares successfully, then execution fails with `22003` / `integer out of range` and publishes neither row. The numeric feature smoke checks this boundary through both direct and prepared execution.

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted supported columns receive NULL (the supported DDL has no declared-default expressions). Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Conversion, arity and NOT NULL failures publish no rows from the statement. The public feature smoke is `go run ./cmd/insert-implicit-smoke`.

Preparation checks assignment compatibility; typed integer literals assigned to INT4 are range-checked during execution, before NOT NULL checks. Thus preparing a short INSERT containing `2147483648` succeeds, but executing it fails with `22003` (`integer out of range`) without publishing rows. Unknown quoted numeric strings still run the destination input function during preparation and retain their input-specific diagnostic and cursor position.

That feature smoke exercises both direct execution and an actual prepared short INSERT, checks declaration-order metadata and statement atomicity, and retains a cursor across Reset/Restore. It also checks deterministic full-row Dump/Restore, successful hand-authored short-row Restore, and catalog preservation when a staged short INSERT omits a NOT NULL tail. Its single success line is printed only after those public API assertions pass.

No configurable `search_path`, `CREATE SCHEMA`, schema-qualified creation outside `public`, three-part/database qualification, aliases, joins, expressions, functions, casts, constraints beyond nullability, defaults, serial, primary keys, UPDATE/DELETE, RETURNING, multiple sort keys, explicit transactions, ACLs, or SQL catalog projections are supported. Recognized unsupported features fail with SQLSTATE `0A000`; malformed SQL fails with `42601`. Unsupported suffixes and multiple statements fail before any prefix executes.

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

Native SQL failures supply both severity fields as `ERROR`, including parser, missing-object and duplicate-object failures; naming an object in the primary message does not imply an object diagnostic field. The error-fields feature smoke is `go run ./cmd/error-fields-smoke`: it checks exact syntax and engine diagnostics, direct/prepared NOT NULL failures, statement atomicity, wrapping, ordinary context cancellation and native control round trips, and prints only `error fields: ok` after those checks succeed. Reference-only permission, procedural and SQL cancellation witnesses exercise PostgreSQL diagnostic transport expectations, not native implementations of those unsupported features.

Shared generic SQL diagnostics construct only severity, SQLSTATE and primary message; they never infer object identities. In particular, input/range conversion errors (`22P02`/`22003`) do not infer Schema, Table or Column from a destination or WHERE predicate. Condition-specific producers explicitly own their additional fields: the supported NOT NULL failure (`23502`) supplies its schema/table/column and failing-row Detail. This distinction follows PostgreSQL's [conditional error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html); clients must not infer the presence of one object field from another.

Invalid boolean strings retain the original decoded input in the PostgreSQL `22P02` primary message (for example, `invalid input syntax for type boolean: "o"`). Boolean WHERE string-literal failures now carry the one-based character Position of the source opener, as INSERT already does, including during actual preparation and on empty tables. Escapes and continued strings do not change that provenance. Bound parameters have no query Position or native portal Context; real PostgreSQL Bind errors independently retain lib/pq's parameter Context. No destination object identities are inferred.

This bounded diagnostic change does not replace boolean parsing or native `database/sql` values: accepted aliases/unique prefixes, case/ASCII whitespace, TRUE/FALSE/NULL, bool/nil returns, and atomic INSERT and Dump/Reset/Restore behavior remain baseline regressions. Run `go run ./cmd/boolean-diagnostics-smoke` for complete native diagnostic, direct/prepared parameter, atomicity and control witnesses; it prints only `boolean diagnostics: ok` after all assertions succeed. The independent pinned-reference fixture is `go test -tags reference -run '^TestReferenceBooleanDiagnostics$' -count=1 -v .`.

Unterminated comments and quoted tokens report PostgreSQL-style English primary messages containing the raw input from the offending opener (`at or near "..."`), with a separate one-based character Position. Newline-separated string segments form one token, so an unterminated later segment points to the first literal opener and includes the continued raw suffix. A zero-length delimited identifier instead includes only its raw `""` token, not trailing SQL. Diagnostic text is not escaped as a Go string; multibyte characters do not turn Position into a byte offset.

INSERT target-resolution and assignment diagnostics interpolate decoded identifiers between literal double quotes, matching PostgreSQL diagnostic prose rather than Go escaping or SQL identifier quoting. The feature smoke includes an embedded-quote column mismatch and checks its exact diagnostic and complete sentinel preservation.

Decoded E-string NUL/invalid UTF8 reports `22021` with no Position; malformed Unicode reports `22025` with a format Hint, while invalid Unicode values/surrogate pairs and unterminated literals report `42601` with original-source character positions. Raw SQL NUL/invalid UTF8 intentionally retains native positioned `42601` preflight diagnostics, distinct from decoded bytes. Bound text is not SQL escape source and retains existing type-input behavior. B/X/N strings and configurable string GUCs remain unsupported. Dumps use ordinary strings, preserving decoded controls and literal backslashes.

The focused public proof is `go run ./cmd/string-literals-smoke`; after checking direct/prepared escape and dollar values, parameter separation, exact diagnostics, statement atomicity, shared-pool Dump/Reset/Restore, retained cursors and the raw SQL boundary it prints exactly:

```text
strings: direct/prepared escape round trips
strings: diagnostics and positions
strings: statement atomicity
strings: dump/reset/restore shared pools
strings: raw SQL boundary preserved
strings: dollar quotes, tags and parameter separation
```

Pinned real-reference feature fixtures are `go test -tags reference -run '^TestReferenceStringLiterals$' -count=1 -v .` and `go test -tags reference -run '^TestReferenceStringLiteralDumpRoundTrip$' -count=1 -v .`.

## Native fixture controls

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity and existing pools. Dump writes deterministic, schema-then-relation-sorted PostgreSQL-compatible plain CREATE/INSERT SQL, with separately quoted schema/relation components, declaration-ordered columns and canonical literals. It is not complete `pg_dump` output, COPY, or catalog export.

Restore accepts supported CREATE/INSERT scripts without parameters, builds a staging engine, then atomically **replaces** the descriptors, identity-keyed row storage and epoch, never merges. Empty input clears user relations. Read, parse, execution and cancellation errors leave original data untouched. Reset/Restore publication supersedes earlier completed writes. Existing SELECT snapshots remain valid across inserts and catalog replacement. Writer errors do not change the database.

`go run ./cmd/smoke` additionally checks two opaque DSNs with two simultaneously open pools and held physical connections per instance. It verifies qualified/unqualified INT4 results, same-instance sharing, independent-instance isolation, another instance's Reset/Restore leaving a held cursor unchanged, same-Driver reconnect retention and independent-Driver isolation before printing the documented four lines.

## Catalog-part agent workflow

OMP discovers `/implement-part <part-symbol>` from `.omp/commands`, with coordinator/review skills and dedicated scout, planner, implementer and reviewer agents. `part-planner` uses configured `@slow` in the same OMP process; it can exchange questions with the coordinator and scouts, and spawn follow-up scouts as evidence gaps arise. This is workflow infrastructure, not an implementation of the future SQL catalog. It reads existing ignored `.cswd/spec_parts.json` and `.cswd/spec_status.json`; only a selected part with completed prerequisites may start.

Invoke from a clean named integration branch. Initial research and an accepted `.cswd/parts/<symbol>.md` plan precede an owned Git worktree. The coordinator persists supplementary research and ingests the planner's four-field JSON result through the lifecycle helper; planning agents do not implement or manage lifecycle. No external planning process, native auto-approval or guard extension is needed.

The helper runs generation, all unit/race/reference-tagged tests, public smoke, standard-library dependency checks and planned feature smoke. Exact-tree review uses configured `@csw-review-2`; medium/high/critical findings require fixes and fresh verification/review. Model mappings remain unchanged.

Only verified, approved content is committed and fast-forwarded to the latest integration tip. Rebase/conflict resolutions require full reverification. Status becomes `done` only after ancestry proof; failures retain journals/evidence under `.cswd/tmp/<symbol>/runs/<uuid>` and owned worktrees under `.cswd/work/<symbol>/<uuid>`. Use helper `resume` for interrupted runs; never manually reset/stash the integration checkout. The coordinator skill documents helper actions, evidence schemas and recovery rules.

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

Implicit INSERT diagnostic fixtures independently specify preparation versus execution failure stages, not just backend agreement. Native engine tests also use a test-owned context to deterministically cancel after row staging, during constraint checks, and immediately before publication, checking complete sentinel preservation and subsequent engine usability without sleeps or production hooks.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
