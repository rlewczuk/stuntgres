# Native session configuration

[README](../README.md) · [Driver](DRIVER.md) · [Transactions](TRANSACTIONS.md)

PostgreSQL 18's [configuration documentation](https://www.postgresql.org/docs/18/runtime-config.html)
is the SQL authority. Native implements a bounded typed registry, not PostgreSQL's
entire parameter catalog or all associated server mechanisms.

## Physical-session state and reset defaults

Each physical connection owns independent typed settings. Hold `*sql.Conn` to
target one session; `*sql.DB` can dispatch to any physical connection. Healthy
borrower return and pool reuse preserve settings and identity without implicit
cleanup or rollback. Physical replacement starts with captured connection and
registered defaults and does not erase shared data.

Explicit full session cleanup restores captured defaults through the existing
reset journal rather than boot constants or a second default-provider call.
Immutable definitions and placeholder recognition survive. Session authorization
returns to the authenticated principal and effective identity to its validated
configured reset reference, where present. Authentication/accounting and reset
ownership are unchanged; this is not healthy pool-reuse policy.

Canceled statements, canceled reuse checks and ordinary SQL errors leave healthy
sessions reusable. Physical close drains operations and releases only that
session's owners; abandonment cleanup is nondeterministic best effort.

## Registry and immutable captures

The built-in registry contains actual typed session, presentation, transaction
mode and timeout state. Its bounded listing is not a projection of all PostgreSQL
parameters. Registered custom entries use their registered descriptions; built-in
descriptions follow the pinned PostgreSQL English metadata.

Setting inspection returns owned anonymous snapshots. Completion releases the
implicit transaction, not the returned rows; later mutation or physical teardown
cannot change those values. Native TEXT metadata has string ScanType and known
unbounded length. Declared inspection nullability and computed/provider
nullability retain their distinct contracts.

Prepared literals retain analysis-time string mode. Prepared setting reads use
execution-time values. To retain a literal prepared before a local override,
prepare and execute the same handle on that `sql.Tx`; `Tx.Stmt` can reprepare a
connection-created handle under the transaction's current mode.

Native `application_name` normalization clips original UTF8 before byte escaping
and emits a structured, filterable notice on clipping. There is one shared
literal decoder, not a second setting-specific decoder. Built-in unsupported
names remain errors rather than arbitrary native knobs. Read-only definitions
cannot mutate; privileged custom definitions and logging statistics remain
deny-only. Monitoring-role membership does not grant protected locale/logging
mutation authority, statistics providers or data-directory metadata.

## Custom setting Go API

Register definitions before the first connector/engine is constructed:

```go
d := stuntgres.NewDriver()
err := d.RegisterSetting(stuntgres.SettingDefinition{
    Name: "demo.memory", Description: "Session memory budget",
    Type: stuntgres.SettingInteger, Unit: stuntgres.SettingKilobytes,
    Default: "1024", IntegerMin: 0, IntegerMax: 2147483647,
})
// Handle err, then use d.OpenConnector and sql.OpenDB.
```

Kinds are `SettingBoolean`, `SettingString`, `SettingInteger`,
`SettingFloating` and `SettingEnum`. Access is `SettingSession`,
`SettingReadOnly` or deny-only `SettingPrivileged`. Numeric bounds are inclusive
and explicit: zero/zero means exactly zero. Definitions and enum slices are owned
copies; enum output uses registered canonical spelling.

Units are `SettingUnitNone`, `SettingBytes`, `SettingKilobytes`,
`SettingMegabytes`, `SettingMilliseconds`, `SettingSeconds` and `SettingMinutes`.
Registered numeric input shares typed unit conversion, ties-to-even integer
rounding and six-significant-digit floating display. Defaults use the same input
validation as later values; invalid input publishes no override. Floating
underflow/exactness and finite registered bounds are checked, not silently clamped.

Registration rejects irrelevant numeric bounds, units on nonnumeric kinds,
invalid UTF8/NUL metadata, nonfinite floating bounds and duplicate enum values.
Registry publication is immutable and freezes at first Engine construction,
even without Ping, remaining frozen after pools close. Drivers isolate their
registrations. The globally SQL-registered Driver cannot be configured
retroactively; use `NewDriver`, `RegisterSetting`, `OpenConnector`, `sql.OpenDB`
in that order.

Registered definitions describe configuration storage, not invented planner,
process or server effects. Valid unknown dotted names use session-owned text
placeholders with empty reset defaults, hidden from the bounded registry listing.
Inspection never creates a placeholder; replacement drops placeholder knowledge.
Registration makes a name typed and visible from the outset.

## Transaction and identity journals

Settings use the transaction owner's typed override and original/candidate
journal. Commit preserves session candidates and expires local overrides;
rollback and admitted error recovery restore the applicable originals/savepoint
state. A masked session candidate remains available at commit. Savepoint release
retains ordinary changes and local lifetime until outer end.

Placeholder recognition is session-owned: a name first introduced after a mark
remains recognized with an empty value after rollback if no earlier override
existed. Reset batches mutate only resettable definitions/explicit overrides,
skipping immutable, deny-only and current-transaction mode definitions. Effective
timeout changes publish once under the settings/lifecycle fence.

Role selection uses a separate typed principal-reference journal sharing the
same boundaries, never role names in generic settings. Authenticated and session
identity remain separate from effective-role changes. Restoration uses validated
references without repeating subsequently revoked membership eligibility. The
internal `SessionStart.Defaults` role request is not exposed by native public
connectors and is not a generic registry entry.

Current transaction characteristics resolve the same owner as Go controls.
Their snapshot/query timing restrictions remain owner-monotonic, including
savepoints. Query-backed configuration assignment establishes a query snapshot;
it cannot bypass prequery characteristic admission. Go `BeginTx` inherits the
deferrability default because `sql.TxOptions` has no deferrability field. Its
explicit `ReadOnly` flag is a separate supplied override.

Go Commit of aborted work restores originals and returns the ordinary
`pq: could not complete operation in a failed transaction` adapter error;
SQL end recovery restores the same state. Native Reset/Restore preserves live
settings, uses default-on parsing and exports no session settings in Dump.

## Timeout behavior

Statement timeout covers command admission, parse, Prepare and Execute. Lock
timeout limits each queued acquisition independently. Caller cancellation and
deadlines preserve ordinary Go context error identity; only native SQL timers
produce their corresponding structured SQL timeout diagnostics.

Transaction timeout covers the entire active implicit/explicit transaction,
including parse admission and lock waits. A provisional command budget transfers
once at actual transaction birth without inventing an early transaction identity
or resetting the deadline. Ending or detaching the owner retires that budget;
a detached prepared entry has no session timer and survives origin termination.

Positive-to-positive assignments retain an active deadline. Zero stops it; a
subsequent explicit positive assignment arms a stopped budget from assignment
time. Transaction/savepoint restoration is not an assignment and never rearms
it. Full/child error recovery stops the budget. Reset publication stops an
obsolete budget before a stale callback can terminate the owner; resetting only
idle settings must not rearm transaction timing.

A nonzero transaction timeout no longer than statement timeout suppresses that
statement timer at admission. The comparison also suppresses idle-in-transaction
timing after error recovery has stopped the transaction budget. Lock timing stays
independent. At ready boundaries only the applicable inside/outside-transaction
idle category runs. Query, Prepare, held Execute, Ping and Go controls are
activity; retaining eager Rows is not executing work. A pooled idle physical
owner can expire without another request.

Expiry invokes canonical asynchronous physical termination. FATAL diagnostics are
`25P04` for transaction timeout, `25P03` for idle-in-transaction timeout and
`57P05` for idle-session timeout. Cleanup rolls back private effects and releases
real locks, TEMP, session-prepared and session resources. Peer permanent data and
detached prepared entries survive. Accepted native work returns the structured
fatal, never replay-permitting `ErrBadConn`. See
[driver termination](DRIVER.md#timeouts-and-terminal-physical-owners) and
[transaction timers](TRANSACTIONS.md#transaction-timeout-lifecycle).

## Presentation capture and native environment limits

Execution owns one immutable temporal/locale/presentation environment. Prepared
temporal constants retain analysis-time interpretation until explicit plan
cleanup invalidates their complete private capture group. Parameters and runtime
conversions use execution capture; retained rows never consult later settings.
Native finite timestamp transport is `time.Time`, independently of textual style.

Timezone authority uses embedded rules and stock abbreviation tables, not custom
installation files or runtime filesystem loading. Native defaults to UTC, while
the pinned fixture uses Etc/UTC; these names are intentionally distinct. Invalid
updates leave typed policy and display unchanged. Reset, savepoints and physical
replacement restore the captured policy as a unit. Interval presentation affects
real input/output and remains session-local, not a descriptive no-op.

The database remains UTF8. Client encoding configuration does not provide wire
transcoding or a second server encoding. Recognized non-UTF8 choices retain the
native exclusion, not a successful no-op. Locale categories use deterministic
C/POSIX policies, with no host locale inventory, libc dependency or process-wide
`setlocale`. Message-locale permission uses the current role's actual superuser
flag; validation cannot bypass that gate. ICU validation configuration is a
reporting policy, not an ICU provider or additional collation.

Identifier quoting and BYTEA output feed captured presentation. Parameterless
held quote consumers retain a successfully captured immutable scalar group from
first execution, not Prepare. Close/deallocation/session end releases it;
explicit plan cleanup lazily recollects it. Failed rebinding/folds publish no
partial replacement. Parameterized executions instead own fresh local groups,
including lowered definitions, with no global SQL-text/settings cache.

This is not PostgreSQL custom/generic cost-based plan selection: native has no
cost selection counters or `plan_cache_mode`. Live consumers retain their own
execution behavior. Stored native SQL and Dump remain reparsable and independent
of session quote policy. Float textual precision is captured per execution;
scalar Go transport preserves stored binary values and canonical Dump always
uses shortest output, preserving payload bits on replay. Monetary Restore keeps
live policy and does not claim arbitrary-locale dump portability.

## Testing

Public setting definitions use `make check RUN='^TestReferenceSettings[A-Za-z0-9_]*$'`
and the same selector with `REFERENCE=1`. Typed native registrations and genuine
PostgreSQL configuration witnesses remain separate; arbitrary PostgreSQL
placeholders do not validate native typed registration.

Public timeout workloads keep actual direct Execute, Prepare and held statements.
Active-owner cleanup uses Go Rollback rather than recovery commands submitted
through an expired deadline. Retained native eager rows and PostgreSQL streaming
rows have different cleanup requirements. Focused commands:

```sh
make check RUN='^TestReferenceSettingsPublicTimeout(Stages|RollbackCleanup)$'
make check REFERENCE=1 RUN='^TestReferenceSettingsPublicTimeout(Stages|RollbackCleanup)$'
```

Monitoring and timezone witnesses independently check genuine server-only
mechanisms without claiming native parity. See [testing](TESTING.md).
