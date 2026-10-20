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

Internally, each Engine constructs one immutable `settings.Registry`
([internal/engine/settings](../internal/engine/settings/README.md)) before
publication. Its definitions and sorted names are borrowed read-only; typed
`BootValue` access preserves exact defaults and shared immutable decoded paths
without reparsing display text. This metadata is separate from physical-session
values, captured reset defaults, LOCAL journals and timeout state. Registry reads
never normalize values, invoke default providers or create placeholders.

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

## Row security and pg_settings

`row_security` is a session/user-settable Boolean, boot default `on`, description
`Enables row security.`. Ordinary users may set either value; invalid Boolean
input raises 22023. Each admitted statement captures the effective value once.
Fresh Prepare and held execution check current applicability: `off` raises
42501 when policies would affect the query, including default deny and empty
inputs. It is an error guard, not a policy bypass. Direct superuser/BYPASSRLS
and non-FORCE owners still bypass; inherited ownership participates, but
BYPASSRLS attributes are not inherited. Views retain their existing definer/
invoker principal chain. `row_security_active` remains setting-independent.
`on` preserves native default-deny and the explicit unsupported
policy-expression enforcement error; no evaluator or physical PostgreSQL
planner/storage mechanism is implied.

Auto-updatable view mutations check the physical target while retaining the
logical view's definer/invoker authority. Rule actions use their event owner's
authority; all applicable off guards precede executor table ACLs. Nonempty
rule replacements retain the caller's original relation checks even when
the original write is suppressed. Unconditional INSTEAD NOTHING leaves no
executable command, so no original off guard or executor ACL check runs.
These setting checks do not define rule-change invalidation for cached plans.

The read-only typed `pg_catalog.pg_settings` virtual view currently publishes
**only row_security**, with all 17 PostgreSQL columns. It does not fabricate
metadata for other settings or implement UPDATE rules/pg_show_all_settings.
SHOW ALL still exposes the entire native registry. The row reports canonical
on/off, context `user`, vartype `bool`, category
`Client Connection Defaults / Statement Behavior`, boot_val `on`, and the
physical session's captured default as reset_val. Unit/bounds/enumvals and
sourcefile/sourceline are NULL; pending_restart is false.

Source reflects provenance, not equality to boot text: absent SQL override is
`default`; any SQL override is `session`, even explicit SET on. A default
supplied by native SessionDefaultProvider maps to `client` until overridden;
RESET/DEFAULT restores that captured default and source without another
callback. This is an explicit native startup-API provenance mapping, not a
PostgreSQL Go callback or configuration-file claim. Existing transaction and
savepoint journals restore override presence and therefore source. Captured
rows are immutable; prepared handles never cache GUC or principal authority.

## Parameter ACLs

`GRANT SET` / `GRANT ALTER SYSTEM ON PARAMETER` uses genuine sparse class-6243
catalog authority with SET/ALTER SYSTEM (`sA`) bits, role inheritance, grant
options and origin-aware CASCADE. Canonical registered aliases and dotted custom
names resolve atomically; a late missing/invalid target publishes no prefix.
Default-equivalent sparse entries can disappear; explicit-empty authority stays
distinct. Owner/grantor/grantee references participate in DROP OWNED, DROP ROLE,
native remap validation and pre-output Dump rejection.

Parameter validation and canonicalization consume the same immutable registry
as session initialization and SHOW ALL, not a second definitions map. Parameter
providers borrow a pointer to the Engine-owned registry value without replacing
or mutating it. Descriptor identity still comes from the actual sparse catalog;
registered settings are not enumerated into parameter ACL rows.

For actual registered protected settings such as lc_messages, SET, RESET and
set_config check SET authority; ALTER SYSTEM alone does not permit those calls.
USERSET application_name stays ordinarily mutable. ACL metadata does not
implement absent settings, privileged deny-only custom/statistics providers or
readonly mutation. Unrepresented work_mem/sort_mem grant targets remain42704,
not invented native knobs.

The read-only `pg_catalog.pg_parameter_acl` catalog projects the owner's actual
sparse records: allocated entry `oid`, canonical TEXT `parname`, and typed
`paracl ACLITEM[]`. Registered parameters and valid dotted custom names use the
same authority; SET/SHOW alone never creates a row. Returning to the owner-only
`sA` baseline removes the row, while an explicit empty ACL remains nonnull `{}`.
PUBLIC catalog SELECT allows an ordinary non-grantee to see all entries, subject
to the usual current table/column access checks, not represented SET privileges.
Fresh direct/prepared/held reads see completed changes; already opened output
retains its ACL and role display snapshot.

There is no native has_parameter_privilege SQL provider. Native ALTER SYSTEM
remains42601 at SYSTEM (Position7); storing its ACL bit does not add
filesystem/configuration-file execution. Parameter grants in an explicit
transaction and native catalog GRANT remain unsupported. Dump rejects parameter
ACLs before output; Reset clears them without removing catalog availability.


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

The three `default_transaction_*` parameters select isolation, read-only and
deferrable modes at each new transaction boundary. Changing them inside a block
does not change that block's corresponding `transaction_*` modes. SQL `BEGIN`
inherits all three effective defaults; another physical session is unaffected.
`SHOW` reflects the current/default distinction for each dimension.
`SHOW TRANSACTION ISOLATION LEVEL` reads the same current isolation setting as
`SHOW transaction_isolation`, with one TEXT column named `transaction_isolation`.
Direct execution and held prepared statements read the current transaction at
execution, not a value captured at preparation; SHOW does not establish a query
snapshot or prevent a subsequent legal SET TRANSACTION.

All three defaults participate in the same SESSION/LOCAL journal. LOCAL changes
expire at outer commit or rollback; SESSION followed by LOCAL commits the masked
session candidate, while full rollback restores the original. `ROLLBACK TO`
restores both effective values and candidates, including across duplicate marks
and repeated rollback; `RELEASE` does not expire local defaults.

`RESET`, `SET TO DEFAULT` and `set_config` with a NULL value restore captured
reset defaults. A local function reset preserves the session commit candidate.
Override presence matters, not just displayed text: after a SESSION candidate,
LOCAL DEFAULT followed by RESET ALL can leave that candidate available at commit,
whereas explicitly assigning the same baseline value locally before RESET ALL
can replace it. RESET ALL processes eligible explicit overrides rather than
creating overrides for absent settings.

The current `transaction_isolation`, `transaction_read_only` and
`transaction_deferrable` parameters cannot be reset, including by SET DEFAULT or
NULL `set_config`; these routes report `0A000`. RESET ALL skips all three current
modes even after a query, while resetting eligible defaults. Current assignments
use SET TRANSACTION's timing and subtransaction restrictions: illegal late
changes report `25001`, and subsequent ordinary commands in that failed block
report `25P02` until recovery.

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

Physical-owner checks and operation admission observe elapsed transaction/idle
deadlines before admitting work or stopping the previous idle budget. A delayed
runtime callback cannot give a new command a live-owner window after expiry.
The observation uses the same authority and retirement rules as callbacks;
already-pending statement/lock ERROR retains priority over transaction FATAL.
This is not earliest-deadline arbitration between every timeout reason.

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

Parameter catalog definitions and positive lifecycle smoke live in
[reference/catalogs Testing](../reference/catalogs/README.md#pg_parameter_acl-selected-proof).
Their eight `TestReferencePGParameterACL` definitions run with the same selector
in native and pinned `REFERENCE=1` modes; component capture, ownership, snapshot,
authorization and cancellation proofs use
`make check RUN='^TestPGParameterACL(ShapeAndOwnership|Capture|Snapshot|Authorization|Cancellation)$' RACE=1`.

Immutable registry ownership and the preconstruction normalizer/publication
witness have focused private selectors in
[internal/engine Testing](../internal/engine/README.md#immutable-settings-registry);
pure definition behavior has its own in
[internal/engine/settings Testing](../internal/engine/settings/README.md#testing).
The [settings split status](ENGINE-SPLIT-settings.md#status) distinguishes source
ownership changes from verification and startup-performance evidence.

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
