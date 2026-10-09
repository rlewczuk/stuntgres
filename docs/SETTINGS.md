# Session settings

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Each physical connection has independent typed session-setting state. Hold
`*sql.Conn` to target that session; a `*sql.DB` may dispatch to any physical
connection. Borrower Close returns it to the pool: healthy pool reuse preserves
its settings and identity, without implicit RESET, DISCARD or rollback.
Physical replacement starts with captured defaults and does not erase data.
Canceled statements, canceled pool-reuse checks and normal SQL errors leave
healthy sessions reusable, preserving physical identity and settings.
Explicit physical close drains operations and releases only that session's
owners; runtime abandonment cleanup is nondeterministic best effort.

`SET [SESSION | LOCAL] name {TO | =} value`, `RESET name`, `RESET ALL`, `SHOW name`,
and `SHOW ALL` use the ordinary direct/prepared execution route. Names are
ASCII-case-insensitive, including dotted custom names. SET values use the
existing ordinary/`E'...'`/`U&'...'`/dollar-quoted literal lexer, identifiers, booleans, signed
decimal-digit integer tokens or bare DEFAULT. DEFAULT and RESET restore
the captured physical-session default; quoted 'DEFAULT' is literal text.
SHOW returns one string row with the canonical setting-name TEXT column.
SHOW ALL returns sorted `name,setting,description` TEXT columns for the exact
supported registry, **not** PostgreSQL's entire parameter catalog. Built-in
descriptions match PostgreSQL 18.0's English `pg_settings.short_desc`; custom
settings return their registered description.
SET/RESET and Exec of SHOW affect zero rows. The selected FROM-less calls
`SELECT current_setting(text)` and `SELECT set_config(text,text,boolean)`
return one nullable TEXT row, with affected count one for Exec.
SHOW, SHOW ALL and setting-function rows own immutable anonymous snapshots:
statement completion releases the implicit transaction, not the returned rows.
Later setting changes or physical-session teardown do not change captured values.

Setting-function arguments accept strings, NULL, booleans in the third position,
and positional parameters through the existing binder. NULL current_setting
returns SQL NULL; NULL set_config name fails 22004; NULL value resets;
NULL is_local means false. Same-type repeated parameters work, gaps fail
42P18 and mixed-type repeated parameters remain unsupported 0A000.
Unsigned decimal tokens fitting PostgreSQL's int32 scanner range
are formatted canonically; larger tokens retain their original digits (including
leading zeros), with unary minus prepended and unary plus omitted.
Unquoted radix-prefixed and underscore-separated numeric setting values,
including signed forms, are unsupported (`0A000`) and cannot mutate settings.
Quoted `'0x2a'`, fractions and exponents reach the full typed setting input
rules; quoted `'1_000'` is valid text but invalid numeric configuration input.
Unquoted floating/exponent SET syntax remains unsupported except the bounded
numeric-hour operand of `SET TIME ZONE`; ordinary setting grammar is unchanged.
The application_name value clips original UTF8 to 63 bytes before escaping nonprintable/non-ASCII
bytes as lowercase literal `\xNN`. Clipping succeeds and emits a structured
NOTICE when a connector handler is configured and the session filter permits it.
SET reuses the [literal decoding rules](LEXICAL.md): for example,
`SET application_name TO E'a\nb'` decodes the newline first, then SHOW returns
literal `a\x0ab`. No second setting-specific string decoder is used.

The exact supported builtin registry contains these eighteen settings:

| Name | Default / access | Actual behavior |
| --- | --- | --- |
| application_name | empty / session | Normalized descriptive session value |
| client_min_messages | notice / session | Filters structured connector notices; debug aliases debug2 |
| IntervalStyle | postgres / session | Actual interval input/output; immutable statement presentation capture |
| search_path | `"$user", public` / session | Stored explicit path; effective lookup skips absent/inaccessible schemas and expands current user |
| standard_conforming_strings | on / session | Per-call ordinary-string decoding; E strings always escape |
| bytea_output | hex / session | Selects hex or escape BYTEA output |
| TimeZone | UTC / session | Typed embedded timezone rules for input, output and conversions |
| DateStyle | `ISO, MDY` / session | ISO/SQL/Postgres/German output and MDY/DMY/YMD input order |
| statement_timeout | 0 / session | Millisecond command-admission timer covering parse, Prepare and Execute |
| lock_timeout | 0 / session | Millisecond limit for each queued lock acquisition; 0 disables |
| server_encoding | UTF8 / read-only | Actual UTF8 invariant; writes and individual RESET fail 55P02 |
| log_statement_stats | off / privileged | Deny-only: all writes and individual RESET fail 42501; no logging activation |
| default_transaction_isolation | read committed / session | Isolation default for new transactions |
| default_transaction_read_only | off / session | Read-only default for new transactions |
| default_transaction_deferrable | off / session | Deferrable default for new transactions |
| transaction_isolation | read committed / transaction | Current transaction isolation level |
| transaction_read_only | off / transaction | Current transaction read-only status |
| transaction_deferrable | off / transaction | Current transaction deferrable status |

Prepared literals retain Prepare-time string mode; prepared SHOW/functions read
Execute-time values. To retain a literal prepared before LOCAL within a transaction,
prepare it on that `sql.Tx` and execute the same handle: `Tx.Stmt` may reprepare
a handle created on `sql.Conn` using the transaction's current string mode.
Timeout changes apply to the *next* command. Only the
engine's own timer returns 57014 `canceling statement due to statement timeout`;
caller cancellation/deadlines remain ordinary context errors. Zero disables
the timer. RESET ALL resets session-settable definitions and placeholders,
skipping read-only and privileged definitions.

Current transaction isolation, read-only and deferrable settings resolve the same
transaction owner as SQL and Go controls. Current RESET, DEFAULT and set_config
with NULL fail 0A000 `parameter \"<name>\" cannot be reset`; RESET ALL skips these
three current modes but resets their session defaults transactionally. Isolation
cannot change after the first snapshot; late read-only to read-write fails 25001.
Changing DEFERRABLE after the first query fails 25001 even for the same value;
setting it in a child block fails 25001 independently. The query-backed
set_config route establishes a query snapshot before assignment and therefore
cannot set the current DEFERRABLE mode. Use prequery SET/SET LOCAL or SET
TRANSACTION instead. Savepoints restore DEFERRABLE alongside isolation and access
mode. It controls safe-snapshot acquisition only for Serializable read-only
transactions; see [transactions](TRANSACTIONS.md#serializable-snapshot-isolation).
Go BeginTx inherits default_transaction_deferrable: database/sql.TxOptions has
no deferrability field, so its omission is not an explicit NOT DEFERRABLE request.
Unlike the explicitly supplied Go ReadOnly flag, this default is preserved for
both default and explicitly selected Serializable isolation.

Settings participate in transactions and named savepoint marks. SESSION (also
plain SET) persists on commit and restores its pretransaction value on rollback
or an abort without marks. ROLLBACK TO and recoverable errors restore the saved
typed overrides and SESSION/LOCAL original/candidate journal; RELEASE keeps
ordinary settings, including LOCAL until outer end. LOCAL expires on either
outer end. SESSION followed by LOCAL commits the masked SESSION value; a
subsequent SESSION or RESET supersedes that candidate. Placeholder recognition
is session-owned: a name first set after a mark remains recognized with an
empty value after rollback when no earlier override existed.
Go `sql.Tx.Commit` on an aborted transaction restores originals and returns
the existing ordinary `pq: could not complete operation in a failed transaction`
error, not a SQLSTATE diagnostic. SQL COMMIT/END succeeds by rollback while
restoring the same original settings.
RESET ALL operates on current explicit, resettable overrides: LOCAL DEFAULT
is absent and skipped, whereas explicit text equal to the default is reset.
`SET [SESSION | LOCAL] SCHEMA 'schema'` sets search_path to one quoted schema
element; it does not check existence. Prepare is metadata-only.
SQL LOCAL outside an explicit transaction warns with 25P01 before semantic
validation and its effective value expires at implicit completion. Validated
dotted placeholder names remain recognized in that physical session.
Function set_config with true outside a transaction returns its transient,
normalized value without that warning; the next command sees the original.
NAMES and client_encoding remain unsupported.

With `standard_conforming_strings=off`, a `U&'...'` string opener immediately
fails with 0A000 `unsafe use of string constant with Unicode escapes`, before
reading its body or UESCAPE clause. The Detail explains the off-mode restriction;
Position identifies the initial U in characters, not UTF8 bytes. Unicode escaped
identifiers (`U&"..."`) remain allowed. E and dollar strings are independent of
this mode. Preparing a Unicode string while on captures its decoded value:
switching off before Execute does not reparse or reject that prepared literal.

Before the first `OpenConnector`, register custom typed session configuration:

```go
d := stuntgres.NewDriver()
err := d.RegisterSetting(stuntgres.SettingDefinition{
    Name: "demo.memory", Description: "Session memory budget",
    Type: stuntgres.SettingInteger, Unit: stuntgres.SettingKilobytes,
    Default: "1024", IntegerMin: 0, IntegerMax: 2147483647,
})
// Handle err, then use d.OpenConnector and sql.OpenDB.
```

`SettingBoolean`, `SettingString`, `SettingInteger`, `SettingFloating`, and
`SettingEnum` use shared input/default/display rules. Access is
`SettingSession`, `SettingReadOnly`, or deny-only `SettingPrivileged`.
Numeric bounds are inclusive and explicit: zero/zero means exactly zero.
Enums copy `EnumValues` and display registered canonical spellings.
Numeric units are `SettingUnitNone`, `SettingBytes`, `SettingKilobytes`,
`SettingMegabytes`, `SettingMilliseconds`, `SettingSeconds`, `SettingMinutes`.
Memory inputs accept case-sensitive B/kB/MB/GB/TB, time inputs us/ms/s/min/h/d.
Integer fractions use ties-to-even rounding with PostgreSQL unit conversion;
floating display uses six significant digits. Registry publication is immutable
and frozen at first Engine construction, even without Ping, and stays frozen
after pools close. Different Drivers isolate registrations.
Registration rejects irrelevant numeric bounds, units on nonnumeric kinds,
invalid UTF8/NUL metadata, nonfinite floating bounds and duplicate enum values.
Definitions and enum slices are owned copies. The globally sql-registered Driver
cannot be configured retroactively; use NewDriver, RegisterSetting, OpenConnector
and sql.OpenDB in that order for custom definitions.

Numeric text follows PostgreSQL's prefix rules: integer `010` is octal8, while
`010.5` falls back to decimal input and rounds to10; `08.5` and `0x1p0` do not
trigger integer fallback. Exact hexadecimal subnormals such as `0x1p-1074` and
exact decimal subnormals are accepted for floating settings: decimal digits of
`5^1023` followed by `e-1023` represent exactly `2^-1023` and display as
`1.11254e-308`. Inexact subnormal rounding, nonzero values rounded to zero,
NaN and overflow reject; defaults follow the same exactness rule.
Infinity reaches the registered finite bounds and reports a range diagnostic
using PostgreSQL's canonical `Infinity` / `-Infinity` spelling, regardless of
the accepted input alias (`inf`, `Infinity`, or case variants).
Invalid numeric input never publishes an override. The native BOOL argument
importer reports 22P02 with empty Position/Context; real PostgreSQL Bind adds its
portal-parameter Context. Literal BOOL errors instead identify the SQL literal.

Unknown valid dotted SET/set_config names create session-local text placeholders
with empty reset defaults, hidden from SHOW ALL. RESET can create a placeholder;
SHOW/current_setting never create one. Physical replacement drops placeholder
knowledge. Registered custom definitions are typed from the outset and visible
in SHOW ALL; they express session configuration storage, not invented planner
or process effects.

Settings restore on whole-transaction end and immediately when an explicit
transaction aborts. They add no role/grant authority, file defaults, ALTER SYSTEM,
reload, server-start or shell-option effects. Existing role/ACL capabilities
remain separate.
Unsupported builtin names (including work_mem/random_page_cost) remain 42704.
The two selected setting calls do not introduce general functions, casts, aliases
or expressions. NULL/parameters remain invalid **SET scalar syntax**; use
set_config for bound values. Comma lists are supported for search_path; other settings reject multiple values with 22023.
The nonreserved name local remains a parameter name in `SET local TO value`
and `SET SESSION local = value`, rather than silently becoming LOCAL scope.
Native Reset/Restore preserve live settings and use default-on parsing;
Dump excludes settings. Retained SHOW/SELECT snapshots outlive session end.

Migrated settings SQL uses the same external definitions in both modes:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceSettings[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSettings[A-Za-z0-9_]*$' VERBOSE=0`.
The reference suite separately executes real PostgreSQL numeric/enum settings,
non-superuser privilege denial and pg_sleep timeout; arbitrary custom PG
placeholders are never claimed to validate native typed registrations.
The shared reference corpus also asserts SESSION/LOCAL ordering, commit and
rollback restoration, transactional RESET ALL and aborted-transaction recovery on
the same physical connection, plus read-only/deny-only access for bootstrap
postgres and an ordinary authenticated role. These assertions do not add
privilege-grant mechanisms.

The reference corpus also asserts exact decimal subnormal handling and underflow
rejection, typed boolean/string/integer/floating/enum settings, normalized units
and bounds, SET/RESET/SHOW/SHOW ALL/current_setting/set_config behavior,
prepare-time parser mode, healthy sessions after 57014 timeouts, 55P02/42501
access failures, transactional local scope, retained settings on reuse,
and replacement defaults with surviving data.

The public1ms timeout corpus keeps direct Execute, actual Prepare and execution
of statements held before arming, including all four original workload sizes.
Implicit attempts keep the owner armed and prove committed sentinel7 through
an independent peer after each attempt. The separate same-owner active-transaction
phase uses public Go Rollback, restoring timeout0 and sentinel7; it does not
issue SQL RESET/ROLLBACK through an already expired command deadline.
Full diagnostics come from the actual timeout operation, not a generic fallback.
PostgreSQL consumes the preexisting same-owner stream before further commands;
native eager retained rows remain live. Focused commands:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceSettingsPublicTimeout(Stages|RollbackCleanup)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSettingsPublicTimeout(Stages|RollbackCleanup)$' VERBOSE=0
```

TEXT metadata checks include empty results, string ScanType and unbounded
Length=(MaxInt64,true). Native SHOW has known nonnullable metadata; native
selected function/table results and pq unknown nullability retain their own
complete descriptor expectations, rather than treating known length as fixed.
Session/LOCAL source witnesses keep original QueryRowContext scalar and peer
reads after EACH binding/sign/quoted vector, direct baseline and retained SET,
physical identities, raw native ExecerContext reset setup, quiet held Prepare
and direct aborted-transaction diagnostics/recovery. No Context recorder record
is fabricated for plain methods. Existing1ms SQL workloads/deadlines remain
unchanged by the task's external900second aggregate override.



## Temporal settings

`SET [SESSION | LOCAL] TIME ZONE` accepts a quoted name/POSIX specification,
numeric hours (including fractions/exponents), a timezone-only INTERVAL atom,
LOCAL or DEFAULT. `SHOW TIME ZONE`, `RESET TIME ZONE` and the ordinary
`timezone` name use the same typed setting owner as current_setting/set_config.
Numeric hours are east-positive: `5.5` means +05:30. POSIX names are west-positive:
`UTC-2` means +02:00. INTERVAL offsets reject months/days; HOUR,
HOUR TO MINUTE and MINUTE qualifiers are accepted. This bounded utility operand
does not provide INTERVAL arithmetic. Numeric/interval SET offsets
are bounded to 167:59:59; literal datetime offsets to 15:59:59.
The bounded interval decoder uses PostgreSQL 18's aliases for its existing
fields, attached or separated (`1hr` / `1 hr`, `hrs`, `min` / `mins`,
`sec` / `secs`, `msec` / `msecs` / `msecond` / `mseconds`,
`usec` / `usecs` / `usecond` / `useconds`, and full field names).
Long unit tokens use PostgreSQL's first-ten-byte matching, including
`millisecond(s)` and `microsecond(s)`. Calendar aliases such as `c` / `cent`,
`dec` / `decs`, `mil` / `mils` and `yr` / `yrs` still decode to months and
are rejected; week/day aliases still fail the day restriction. This vocabulary
does not add quarter/timezone extraction fields or INTERVAL arithmetic.
Marker-only intervals such as `@` or `@ ago` fail with 22007; an actual zero
field such as `0 seconds` is valid. Quoted textual `INTERVAL '...'` values
through ordinary SET or set_config preserve interval-input diagnostics (22007
for malformed input, 22015 for overflow). Unknown zone names and rejected
months/days/ranges retain 22023 setting diagnostics. Every failure leaves the
previous typed TimeZone and displayed setting unchanged.

DateStyle combines one style and one order. Partial updates preserve the other
component; German implies DMY unless order is explicit. DEFAULT uses captured
session defaults, and SHOW emits canonical strings such as `ISO, MDY`.
For ambiguous `01/02/03`, MDY yields 2003-01-02, DMY 2003-02-01 and
YMD 2001-02-03. ISO input remains unambiguous. Conflicting components and malformed
lists fail atomically. RESET, RESET ALL, transactions, savepoints and LOCAL
restore typed rules/style and displayed text together; healthy pool reuse retains
both, while physical replacement starts with captured defaults.
For DATE `1997-12-17`, Postgres style emits `12-17-1997` with MDY and
`17-12-1997` with DMY (not ISO date output).

Execution captures one immutable temporal environment. Held rows keep that
presentation even after SET. Prepared typed temporal constants freeze at analysis;
parameter input, column casts/comparisons and AT LOCAL use execution settings.
TIME/TIMETZ clock output is independent of DateStyle. For finite TIMESTAMP Go
time.Time exports, use an explicit SQL TEXT cast to observe DateStyle formatting.

## IntervalStyle — change note

IntervalStyle is a session enum with default `postgres`; admitted values are
`postgres`, `postgres_verbose`, `sql_standard`, `iso_8601`, case-insensitively.
Diagnostics and SHOW column/name presentation retain PostgreSQL's `IntervalStyle`
casing. It controls actual interval input/output, not a descriptive no-op.
For components(-14months,+3days,-4:05:06), output respectively is
`-1 years -2 mons +3 days -04:05:06`,
`@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago`,
`-1-2 +3 -4:05:06`, `P-1Y-2M3DT-4H-5M-6S`.

Under sql_standard, a leading minus without later explicit signs applies to all
fields: `-1 2:03:04` means negative days and negative time. Other styles retain
independent field signs. Prepared typed/contextual literals freeze Prepare-time
input; parameters read Execute-time input. Query output captures Execute-time
style, never a live session lookup from Next. Retained rows survive later settings,
physical close and native replacement. Pooled peer sessions share data, not style.
SET/RESET/DEFAULT and SET LOCAL rollback/savepoint behavior reuse the ordinary
session setting lifecycle. Dump/Restore neither stores nor changes IntervalStyle.

Invalid input is22023 `invalid value for parameter "IntervalStyle": "invalid"`
with hint `Available values: postgres, postgres_verbose, sql_standard, iso_8601.`
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestIntervalSmoke$' VERBOSE=0`.

