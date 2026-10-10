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
`make quickcheck PACKAGES=./smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.

## Trusted role admission and physical limits

`OpenConnectorAs(dsn, role)` asserts an exact trusted native principal; it does
not parse credentials or perform a password/SCRAM handshake. Missing or miscased
role names retain ordinary host errors without SQLSTATE. An existing NOLOGIN
principal instead rejects new physical connections with FATAL, unlocalized FATAL,
SQLSTATE28000, `role "name" is not permitted to log in`. Superusers are not exempt
from LOGIN. Already admitted connections remain usable after NOLOGIN.

CONNECTION LIMIT counts physical sessions against the immutable original
authenticated role, not current_user or session_user after SET ROLE/SESSION
AUTHORIZATION. Limit -1 is unlimited; 0 denies ordinary admission; positive limits
bound it. Superusers bypass limits. Denial is FATAL/unlocalized FATAL53300,
`too many connections for role "name"`; these admission diagnostics have no other
detail/hint/position fields. Lowering a limit does not terminate incumbents.
An idle pooled connection still counts. Returning `sql.Conn` to its pool does
not release a slot, and borrowing/reusing it does not consume another.
Raw physical `driver.Conn.Close`, or actual pool disposal, releases the slot once.
Failed initialization consumes none; final admission rechecks current attributes.

PASSWORD and VALID UNTIL are meaningful stored SQL metadata but trusted native
admission ignores both, including NULL passwords and expired timestamps.
PostgreSQL password-authenticated fixture connections are an independent route,
not evidence of a native password handshake. Native Reset/Restore retain roles,
attributes, membership and live admission accounting; Dump emits no role/password
DDL. Role utilities remain excluded from native explicit transactions.
Observed public smoke exercised creator ADMIN, omitted ALTER USER retention,
expired trusted admission, physical limit release, NOLOGIN and held authority
refresh. Its repeatable reference selector is
`RUN='^TestReferenceRoleAttributesFeatureSmoke$'` in both quickcheck modes.

## Forward-only SQL cursors — change note

`DECLARE name [NO SCROLL] CURSOR [WITHOUT HOLD] FOR query` requires an explicit
transaction on a pinned `sql.Conn` or `sql.Tx`. DECLARE analyzes the complete
query and captures its data snapshot and owned parameters without producing
target values. `FETCH [FORWARD] count FROM|IN name` and `FETCH [FORWARD] ALL`
continue the retained query in result order. Each successful FETCH publishes a
fully eager, detached batch with the live cursor's complete schema, including
empty/exhausted and zero-column results. Ordinary SELECT remains fully eager.
Blocking sorts, aggregates, joins and views may demand input before a batch;
there is no universal one-expression-draw-per-returned-row promise.

The first effective FETCH captures selected stable identity and statement-time
expression data; before-first FETCH0 does not start demand. Every FETCH uses a
fresh operation/context, current cancellation and lock policy, transaction
authority and sequence acquisition. DECLARE's source snapshot is never rebound
or reopened. Already returned batches remain immutable after SQL CLOSE,
transaction/session end or native catalog replacement.

The final successful cursor publication preflight is the completion boundary:
later cancellation cannot retract a delivered FETCH batch, installed DECLARE or
completed CLOSE/CLOSE ALL in statement Finish. Cancellation or an error during
demand or before that preflight returns no partial batch and leaves the executing
cursor failed, not rewound. Finish still advances the command and releases statement
admission/resources; successful cursor and SQL-name utilities skip the private-stage merge.
Ordinary statements retain their required row/catalog publication.

Runtime REGTYPE casts route parser notices and modifier warnings through the
current FETCH operation and its captured `client_min_messages` policy, never the
DECLARE operation. A demanded TEXT `timestamp(7)` cast emits WARNING/22023
`TIMESTAMP(7) precision reduced to maximum allowed, 6` when that policy permits it.
Quiet description/DECLARE and before-first FETCH0 emit no runtime warning;
bounded FETCH does not project an extra row's warning, and EOF emits none.

`CLOSE name` and `CLOSE ALL` dispose named execution. COMMIT, ROLLBACK, CHAIN,
full abort, PREPARE TRANSACTION detach and physical session end dispose nonholdable
cursors. ROLLBACK TO removes declarations after its mark but does not rewind
older cursor motion, undo CLOSE or heal an executing cursor failure. Recovered
failed cursors report55000 `portal "name" cannot be run`; missing names give34000.
Backward/negative and on-row zero FETCH give55000 `cursor can only scan forward`
with the SCROLL hint. SCROLL, WITH HOLD and ABSOLUTE/RELATIVE movement remain
independent unsupported capabilities.

Preparing DECLARE analyzes parameters/dependencies but installs nothing;
preparing FETCH/CLOSE performs no movement/name-error lookup. A native held
FETCH always returns the current entry's actual names/types/values, even after
same-name replacement or missing-at-Prepare declaration. Only FETCH is exempt
from ordinary immutable prepared SELECT-result signatures. lib/pq independently
retains its old wire description: absent descriptions can expose zero-width
occurrences, and same-width replacements can expose old aliases. Type-changing
replacement can fail in pq's stale binary decoder; native never emulates that
corruption. Closing Go Rows or Stmt does not close or rewind the named cursor.
Dump excludes names, positions and retained query execution; successful native
Reset/Restore fences the next admitted use rather than recapturing sources.

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceSQLCursorFeatureSmoke$' VERBOSE=0`;
pinned counterpart: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSQLCursorFeatureSmoke$' VERBOSE=0`.

## SQL prepared statements — change note

SQL `PREPARE`, `EXECUTE` and `DEALLOCATE` own named definitions in the physical
session. They are neither database/sql statement handles nor `PREPARE TRANSACTION`.
Hold a `sql.Conn` (or a `sql.Tx` on that owner) for a named lifecycle: an arbitrary
pool call may use another physical session. Names use normal identifier folding
and quoted spelling. Returning and reborrowing the same physical connection
retains definitions; physical close/replacement clears them without deleting
committed database data or a peer session's definitions.

`PrepareContext("PREPARE p(int) AS ...")` describes a utility without resolving
declarations or analyzing/installing its body. Its first Exec installs the SQL
name; a second valid Exec reports duplicate42P05. Preparing DEALLOCATE has no
effect until Exec. Preparing EXECUTE describes the current name's result without
evaluating actual expressions; an absent name can have an empty description,
then fail26000 on execution. Native EXECUTE returns its current underlying result
metadata, rather than emulating lib/pq's stale wire-description decoder behavior.
Go `Stmt.Close` does not deallocate a SQL name; detached eager Rows remain usable
after deallocation and physical session end.
The retained-row witness closes the public driver.Conn inside sql.Conn.Raw,
returning nil, then consumes the already eager sql.Rows. It does not force
database/sql's borrowed-connection close via ErrBadConn while those Rows still
own that wrapper's release; wrapper close/wait/cancellation is a separate API
lifetime from the detached engine result.

The wrapper has zero outer adapter parameters. `EXECUTE p($1)` does not bind Go
arguments to the SQL definition: supplied adapter arguments fail as ordinary Go
count errors; with no supplied arguments a nonzero-formal target reports42P02
at `$1`. A zero-formal target ignores SQL actual expressions instead.

Named definitions are session effects, not transactional rows: PREPARE survives
ROLLBACK/ROLLBACK TO, and those commands do not undo DEALLOCATE. EXECUTE runs the
underlying statement in the same admission, command clock, snapshot/lock and
Finish boundary; it inherits normal read-only restrictions, permissions,
cancellation, statement atomicity and explicit-transaction abort behavior.
Final session publication preflight protects installation/removal against late
cancellation, just like cursor session effects. Only successful session utilities
skip private-stage merging; normal EXECUTE writes still publish normally.

Native Dump omits names. Reset/Restore preserves live physical-session owners
and definitions, with existing catalog-generation/signature/reanalysis fences
on their next use. Restore scripts reject SQL lifecycle utilities atomically.
No generalized prepared-plan cache/invalidation architecture is introduced.

Feature family: `make quickcheck PACKAGES=./reference RUN='^TestReferenceSQLPrepared' VERBOSE=0`;
pinned counterpart: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSQLPrepared' VERBOSE=0`.
See [parameter and diagnostic rules](DIALECT.md#sql-prepared-statements--change-note).

## Command and transaction clocks

Each native public SQL request captures a microsecond-quantized command sample
before session/settings and transaction admission waits. A fresh implicit
transaction reuses it; an existing explicit transaction retains its own birth.
BeginTx, SQL BEGIN and AND CHAIN obtain fresh transaction starts. Pin a sql.Conn
or sql.Tx to observe stability across statements and independent physical peers.

Held statements retain analysis/type contracts, not clock samples. Direct and
held Execute both start a new command scope. Native prepared autocommit has
now() = transaction_timestamp() = statement_timestamp(); pq may start a
transaction at Bind before Execute, so portable extended-protocol assertions use
now() <= statement_timestamp(). Native does not emulate transport phases.
clock_timestamp()/timeofday() sample only when demanded. Already-open native rows
retain values and presentation settings across later commands and native controls.
Four selected temporal providers export time.Time/TIMESTAMPTZ; timeofday exports
string/TEXT. Its fixed English six-digit layout is independent of DateStyle.


## MONEY transport and prepared signatures

MONEY/OID790 exports native monetary strings (`$1,234.50`, `-$1.24`) and
string ScanType, with unknown Length/DecimalSize even before iteration or for
empty/all-NULL results. Pinned pq independently exports []byte/interface{}
ScanType. Scan into string or []byte preserves the monetary output; NULL is nil.
Declared native column nullability and existing expression metadata conventions
remain unchanged; pq does not provide Nullable metadata.

Inferred MONEY parameters use captured monetary textual input for string,
owned nonnil bytes, int64 and float64. Bytes are currency text, not binary raw
minor units; nil and typed-nil bytes are NULL. Held executions refresh captured
session presentation and parameter frames; Go kinds never resolve operators.
Use `$1::numeric::money` for exact numeric conversion instead of interpreting
Go float64 as exact decimal data. Literal input errors keep source character
Position (including Prepare); bound input errors have no query Position and
carry the actual `unnamed portal parameter $n = '...'` Context. Arithmetic and
cast execution errors are not relabeled as parameter input failures.
Native failed writes/Restore publish no partial state; retained eager values and
metadata remain owned across Reset/Restore. See [MONEY](TYPES.md#money--change-note).

## Timeouts and terminal physical owners

The five timeout settings use the same physical Session and transaction authority
as SQL/Go controls. Transaction expiry and both idle categories terminate that
owner asynchronously, including when no new request arrives. Canonical cleanup
waits for admitted leases to drain, then rolls back private work and releases
resources outside lifecycle locks; the first termination reason wins. Returning
a sql.Conn or closing Rows/Stmt is not physical termination. Retained eager native
Rows continue to own already-materialized values; they do not suspend idle expiry.

Native accepted Query/Prepare/held Execute/Ping/control work reports an exact
structured FATAL25P04/25P03/57P05 with unlocalized severity and independently
specified primary message. Relation-analysis25P04 can carry its relation Position;
the persisted owner fatal remains location-free. Incomplete native parsing,
including CPU Prepare, has no fabricated semantic-analysis Position.
Caller context cancellation is
still an ordinary context error. A fatal accepted command is never ErrBadConn,
which would permit database/sql to replay work. After that fatal is reported,
subsequent pre-admission use is unusable; Validator/ResetSession discard the owner
before a new pool command, which may safely acquire a fresh default0 session.
Unpublished accepted INSERT work is rolled back, not retried or published twice.

The pinned pq transport intentionally maps FATAL to ErrBadConn (Conn-held
Execute can surface ErrConnDone), so those API errors are not portable SQL
diagnostic fields. Reference tests separately correlate PID, FATAL, SQLSTATE and
exact message from bounded public real-container logs. No native wire-protocol
phases, network service or manufactured pq diagnostics are introduced.
See [settings runtime rules](SETTINGS.md#timeout-behavior--change-note).

## Enum values and prepared signatures

Enums retain catalog-owned logical identity while native transport exports string
values and string ScanType with empty DatabaseTypeName. Length and DecimalSize
are unknown; NULL exports nil and stored nullable columns report true/known.
Pinned pq independently exports raw []byte/interface{} ScanType and unknown
Nullable for enum OIDs. Labels are not trimmed, folded or ordered lexically.
Context-inferred parameters accept exact enum labels and nil; typed TEXT requires
an explicit cast. Invalid bound input uses the existing unnamed portal parameter
Context, whereas literal failures retain source character Position.
Reset/Restore cannot retarget old enum parameter/result signatures by name.
Current source lookup and argument arity still precede cached-contract guards;
fresh prepares work and eager precontrol rows/metadata remain owned.
See [enums](TYPES.md#enums--change-note).

## Composite values and retained row shapes

Named composites and supported user table/view row values export canonical record
strings and string ScanType, with empty DatabaseTypeName and unknown Length/
DecimalSize. NULL is nil; a zero-member nonNULL value is `()`. Domains retain their
logical wrapper and expose the same physical composite metadata. Native nullable
columns report true/known; pinned pq returns raw []byte/interface{} ScanType and
unknown Nullable. Metadata is available before iteration and for empty results.

Inferred bound parameters and unknown literals use the shared record codec;
typed TEXT needs an explicit cast. Child conversion failures remain child-owned.
Enum errors inside records/domains use the input statement's captured search_path
visibility, just like scalar enum input, without adapter-specific message rewriting.
Retained defaults/generation plans share that capture when their admitted text IO
requires it; a computed default error does not acquire unrelated parameter Context.
Literal Position points to the source opener; decoded bound values use opaque
portal Context, never an invented rendering of the raw parameter. Record output
recursively uses the captured presentation/catalog environment, including for
held results after source rename or catalog replacement.

Supported source shape changes retain stable attributes. Current prepared
parameter/result generations see the new shape; previously captured literals
and stored values map retained attributes and append typed NULL members.
Held eager cursors retain the old shape and metadata. Reset/Restore cannot remap
old prepared logical IDs by matching a new spelling; use fresh descriptions.
See [composites](TYPES.md#composites--change-note).

## Domain values and prepared signatures

Domain results retain a distinct logical engine identity but export their ultimate
base values and metadata, including inherited numeric precision/scale or character
length on empty/all-NULL scalar results. Native array/domain-array values and
ScanType are strings with the actual uppercase array DatabaseTypeName; pinned
lib/pq instead exposes raw []byte and interface{} ScanType, and reports no name for
dynamic user-domain array OIDs. Array Length is unknown; NUMERIC[] DecimalSize
tracks its element typmod (unsized native is unknown, pinned pq is65535/65531).
Native domain columns declare nullable true; pq does not
provide Nullable metadata. Ordinary base-owned conversion/input errors are not
suppressed or normalized between adapters.

For BYTEA-domain binary-buffer parity, bind through `$1::bytea` before assignment.
Native inferred BYTEA-domain []byte input is owned binary data; pq's inferred
domain parameter may be textual and reject embedded NUL with22021.
Execution freshly resolves current objects/arity before validating retained
logical parameter and result contracts. A dropped old domain parameter identity
fails native0A000 `cached plan must not change parameter type`; same-name recreation
does not remap that ID. Result-type changes retain the existing0A000 result guard.
Missing targets/columns and arity errors preserve their earlier diagnostics.
After native Restore, use fresh prepared descriptions; retained eager rows still
own their old values and metadata. PostgreSQL old-OID cache failures are independent
oracle controls, not a promised native cache implementation.
See [domains](TYPES.md#domains--change-note).


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
Ordinary SQL identifier clipping emits lexical NOTICE/42622 records with the
original and UTF8-boundary-clipped name, without Position or synthetic Context.
They are produced while parsing direct commands or preparing statements and use
the same captured notice filter/delivery route. Executing an already prepared
statement does not repeat lexical clipping notices.

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

`make quickcheck PACKAGES=./smoke RUN='^TestNoticesSmoke$'` exercises actual notices,
filters, ownership, peer reentry and control round trips; Go test reports the result.


Run `make quickcheck PACKAGES=./smoke RUN='^TestSessionSmoke$'` for asserted isolation, preparation, physical
reuse/replacement, data survival, diagnostics, cancellation and native controls.
Scoped SQL checks use external definitions:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceSession[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSession[A-Za-z0-9_]*$' VERBOSE=0`.
The true-mode family also selects retained tagged authority companions.
Focused nonSQL root mechanics use
`make quickcheck PACKAGES=. RUN='^TestSession(PhysicalIdentityMechanics|ResetMechanics|AdapterMechanics)$' VERBOSE=0`.
Native race coverage uses the same external session-family RUN with
`make quickcheck RACE=1 PACKAGES=./reference`; root mechanics can also use
`make quickcheck RACE=1 PACKAGES=. RUN='^TestSession(PhysicalIdentityMechanics|ResetMechanics|AdapterMechanics)$'`.

Session smoke asserts distinct session settings, prepared TEXT metadata and zero
affected counts, preserved settings on pool reuse, restored defaults on physical
replacement with surviving data, data retention after pool reopening,
42704/22023/42601 diagnostics, and cancellation/control preservation.
Go test reports the result.

## SQL errors and diagnostics

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

Native SQL failures supply both severity fields as `ERROR`, including parser, missing-object and duplicate-object failures; naming an object in the primary message does not imply an object diagnostic field. The error-fields feature smoke is `make quickcheck PACKAGES=./smoke RUN='^TestErrorFieldsSmoke$'`: it checks exact syntax and engine diagnostics, direct/prepared NOT NULL failures, statement atomicity, wrapping, ordinary context cancellation and native control round trips. Go test reports the result. Reference-only permission, procedural and SQL cancellation witnesses exercise PostgreSQL diagnostic transport expectations, not native implementations of those unsupported features.

Shared generic SQL diagnostics construct only severity, SQLSTATE and primary message; they never infer object identities. In particular, input/range conversion errors (`22P02`/`22003`) do not infer Schema, Table or Column from a destination or WHERE predicate. Condition-specific NOT NULL producers (`23502`) supply schema/table/column: INSERT includes complete failing-row Detail; UPDATE includes only modified targets and effectively SELECT-readable columns, in physical order. Without relation-level SELECT, UPDATE prints `(names) = (values)`, even when column grants cover every column; table SELECT, default owner authority and superuser retain the complete unlabelled row. Protected retained values are neither formatted nor disclosed, and retaining them does not require SELECT. Captured output settings and UTF8-safe field truncation are unchanged. SET/ADD existing-row validation has no Detail. Even a named constraint leaves the Constraint field empty. This distinction follows PostgreSQL's [conditional error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html); clients must not infer the presence of one object field from another.

Immediate UNIQUE failures (`23505`) supply Schema, Table and Constraint, with no
synthetic Column or Position. Detail contains all key labels and typed values
only when the current effective authority has table SELECT or SELECT on every
key attribute; partial key readability suppresses runtime Detail. Protected
values are not formatted. Index-build/type-rewrite duplicate errors use
`could not create unique index "name"` and, for readable keys,
`Key (...)=(...) is duplicated.` instead of the ordinary write primary and
`already exists.` suffix. Unreadable build keys produce the generic Detail
`Duplicate keys exist.` without disclosing key labels or values.

Invalid boolean strings retain the original decoded input in the PostgreSQL `22P02` primary message (for example, `invalid input syntax for type boolean: "o"`). Boolean WHERE string-literal failures carry the one-based character Position of the source opener, including actual preparation and empty tables. Escapes and continued strings do not change that provenance. Typed parameter input failures have no query Position and carry `unnamed portal parameter $n = '...'` at the actual failing import index (`''` for empty text). Only failed SQL input diagnostics are copied and annotated; ordinary Go errors and later expression/preflight conversion errors remain unchanged. No destination object identities are inferred.

Encoding-input failures (`22021`) carry only the actual parameter index, because decoded parameter text is unavailable. Codec errors on decoded empty text retain ` = ''`; other decoded text retains ` = '...'`.

This bounded diagnostic change does not replace boolean parsing or native `database/sql` values: accepted aliases/unique prefixes, case/ASCII whitespace, TRUE/FALSE/NULL, bool/nil returns, and atomic INSERT and Dump/Reset/Restore behavior remain baseline regressions. Run `make quickcheck PACKAGES=./smoke RUN='^TestBooleanDiagnosticsSmoke$'` for complete native diagnostic, direct/prepared parameter, atomicity and control witnesses; Go test reports the result. The independent pinned-reference fixtures use `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBooleanDiagnostics[A-Za-z0-9_]*$' VERBOSE=0`; the same RUN with `make quickcheck PACKAGES=./reference` exercises native without Docker.

Unterminated comments and quoted tokens report PostgreSQL-style English primary messages containing the raw input from the offending opener (`at or near "..."`), with a separate one-based character Position. Newline-separated string segments form one token, so an unterminated later segment points to the first literal opener and includes the continued raw suffix. A zero-length delimited identifier instead includes only its raw `""` token, not trailing SQL. Diagnostic text is not escaped as a Go string; multibyte characters do not turn Position into a byte offset.

INSERT target-resolution and assignment diagnostics interpolate decoded identifiers between literal double quotes, matching PostgreSQL diagnostic prose rather than Go escaping or SQL identifier quoting. The feature smoke includes an embedded-quote column mismatch and checks its exact diagnostic and complete sentinel preservation.

Decoded E-string NUL/invalid UTF8 reports `22021` with no Position; malformed Unicode reports `22025` with a format Hint, while invalid Unicode values/surrogate pairs and unterminated literals report `42601` with original-source character positions. Raw SQL NUL/invalid UTF8 intentionally retains native positioned `42601` preflight diagnostics, distinct from decoded bytes. Bound text is not SQL escape source; string-family input rejects NUL/malformedUTF8 with22021 byte diagnostics and the actual `unnamed portal parameter $n` Context without a decoded-value suffix. B/X/N strings remain unsupported; `standard_conforming_strings` is configurable as documented in [session settings](SETTINGS.md). Dumps use ordinary strings, preserving decoded controls and literal backslashes.

The focused public proof is `make quickcheck PACKAGES=./smoke RUN='^TestStringLiteralsSmoke$'`; it asserts direct/prepared escape and dollar values, parameter separation, exact diagnostics, statement atomicity, shared-pool Dump/Reset/Restore, retained cursors and the raw SQL boundary. Go test reports the result.

Pinned real-reference feature fixtures use `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringLiterals$' VERBOSE=0` and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringLiteralDumpRoundTrip$' VERBOSE=0`; the same RUN with `make quickcheck PACKAGES=./reference` exercises native without Docker.

Driver and diagnostic assertion unions are now untagged external scenarios.
Use the same definition in both modes, for example:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|ErrorFields[A-Za-z0-9_]*|DriverCanceledHandles|DriverRowsBoundary|NativeCursorEOFAndCancellation)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|ErrorFields[A-Za-z0-9_]*|DriverCanceledHandles|DriverRowsBoundary|NativeCursorEOFAndCancellation)$' VERBOSE=0
```

Zero-argument pq simple-query errors, prepared parameter inference and native
ordinary argument errors retain independent records and nil-output checks.
Native eager retained rows and detached Columns slices are not pq streaming or
Columns-copy promises. Native precanceled calls and admitted PostgreSQL server
cancellation are separate phases; full57014 and same-owner recovery remain
asserted without timing retries or fabricated transport retirement.
Original source consumers additionally run real DB.Exec/Query/Prepare and
Stmt.Exec/Query, preserving complete diagnostic stages/nil publications, setup
and every sentinel. SAME held replacement consumers and the eight joined
concurrent-insert workers retain their original object/snapshot ownership.
Recorded Context companions remain separate, not substitutes for plain calls.
Notice reentry workers are joined before normal teardown; unjoined failures
remain visibly failed with bounded cleanup. Normal cleanup accepts sql.ErrConnDone
only for a borrower explicitly closed by the test, not other close failures.



## B-tree utility preparation and controls

Direct Exec and database/sql prepared Execute support ordinary CREATE INDEX;
Prepare retains syntax and empty utility description, not target/row/owner
visibility. Each Execute binds current target, role, search_path, keys, included
attributes and provider identities. Utilities return zero affected rows.
Semantic clause errors occur at Execute with full PostgreSQL18.0 diagnostic
records and absent Position; lexical/syntax failures retain character locations
and occur at Prepare. Class-option validation follows name admission, so a
duplicate name wins over unsupported class parameters and IF NOT EXISTS notices.

Native Dump records actual canonical CREATE INDEX definitions with direction,
NULL policies, explicit `"pg_catalog"."collation"`/`"pg_catalog"."opclass"`
identities and current parent/key/INCLUDE names. Restore uses isolated replay and
one atomic replacement, rebuilding key-only roots with destination identities.
A bad late included column/expression/provider leaves destination rows and
definitions unchanged. TYPE reconstruction rebinds old-default classes and
revalidates retained nondefaults rather than freezing obsolete explicit defaults.
These controls require the unwrapped native DB, not a foreign or sqlmw-wrapped pool.
`InspectIndex(ctx, db, schema, name)` returns detached `IndexState` flags
(`Ready`, `Valid`, `Live`) from the current index descriptor. Names are exact
decoded identifiers, not SQL expressions; lookup never uses session search_path.
`WaitingForOlderSnapshots` is a separate live coordinator observation: its older
cohort and xmin boundary have been captured and the builder's validation XID and
snapshot dropped. It is not persisted history or a waiter count, and clears on
phase exit, cancellation and epoch replacement. Ready alone does not prove this
admission boundary.
`WaitingForWriters` uses the same exact owner/kind coordinator record after the
granted writer cohort is frozen. Combine it with Ready to distinguish first
(`false`) from second (`true`) writer admission. Descriptor publication alone
does not prove capture. The two cohort Booleans are mutually exclusive and
false after successful completion or cancellation.
Missing schemas, missing indexes and non-index relations return ordinary control
errors, respectively `stuntgres: index schema "schema" does not exist`,
`stuntgres: index "schema"."name" does not exist`, and
`stuntgres: relation "schema"."name" is not an index` (identifiers are quoted).
Nil databases, canceled contexts and foreign/wrapped pools retain the same
native-control boundaries as Reset. Existing completed ordinary/backing indexes
report all three flags true. This API does not introduce a SQL catalog or promise
an index-specific query plan.
Concurrent builds expose false/false/true initially and true/false/true after
readiness, retaining that last committed phase on failure/cancellation.
Persistent invalid indexes make Dump fail with
`stuntgres: dump cannot serialize invalid index "schema"."name"` before its first
writer call. Ordinary public standalone UNIQUE/NND retains its bounded
simple-key/provider surface. Valid standalone UNIQUE SQL replays through existing
authenticated isolated Restore destination authority, which bypasses only ordinary
provider-admission policy and still performs substantive shared tuple/root validation.
This preserves computed concurrent definitions without widening public ordinary
provider boundaries or enabling concurrent maintenance.
Native replacement fences old completion/snapshot owners without resurrection.
See [concurrent index authority](design/PG18-INDEX-CONCURRENT.md).
The same emitted SQL can be replayed against PostgreSQL.
Writable explicit ordinary CREATE INDEX transactions remain a native 0A000 boundary;
concurrent creation instead reports25001 in explicit blocks,
independently of read-only 25006 utility rejection.


## REGTYPE input and held descriptions

Native REGTYPE/_REGTYPE exports are strings with string ScanType. Scalar domains
report REGTYPE, domain-over-array reports _REGTYPE, and array-of-domain uses its
actual uppercase name. Pinned pq independently supplies nonnull []byte,
interface{} ScanType and unknown Length/DecimalSize/Nullable; dynamic
array-of-domain DatabaseTypeName is empty. Empty/all-NULL cursors keep metadata.

Parameters are imported eagerly through one captured type lookup authority,
before zero-row evaluation and later executor table rights. Input errors retain
decoded parser positions/invalid-type Context and the existing portal Context.
Runtime `$1::text::regtype` is ordinary expression IO, not portal input.
Literal parser errors transpose Position to the outer literal while preserving
the decoded invalid-type Context. Modifier warnings use the same notice API:
literal analysis once per unchanged held authority, frame warnings on each
Execute with actual slot Context, runtime TEXT warnings without portal Context.

A held literal's OID is not refreshed merely because its referent disappears
or is recreated. Effective lookup-path/role authority/database changes trigger
complete successful reanalysis before publishing the private literal group.
Returned rows retain captured output names even after rename, Reset or Restore;
fresh execution formats against its own snapshot. This does not change existing
prepared parameter/result identity guards or sequence-regclass lifecycle.

