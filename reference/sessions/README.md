# reference/sessions

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### SQL version and Unicode inquiries

The seven untagged `TestReferenceVersionInformation` scenarios exercise direct
and actually held-prepared TEXT inquiries, canonical names, empty-result metadata,
typed ICU NULL, Unicode16 assignment/normalization, full English diagnostic
fields and positions at Query/Prepare, and unchanged sentinel rows after misuse.
The native banner is exactly `PostgreSQL 18.0 compatible; embedded stuntgres`;
the real oracle must identify PostgreSQL18.6 without a fabricated portable build
suffix. Native Unicode is16.0 with no ICU; the pinned oracle reports Unicode16.0
and ICU15.0. These expectations are independent, not blanket parity normalization.

Stored view and DEFAULT calls remain symbolic across native Dump/Reset/Restore,
including separate-destination replay and new defaulted inserts. Cancellation
checks ordinary native `context.Canceled` without inventing deterministic
precanceled pq behavior for a constant inquiry. These controls/cancellation
phases live inside the same shared scenarios, not separate SQL implementations.

Run from the repository root with an external900-second deadline per invocation:

```sh
make check RUN='^TestReferenceVersionInformation(Values|Metadata|UnicodeProvider|Diagnostics|StoredControls|Cancellation|Smoke)$'
make check RUN='^TestReferenceVersionInformation(Values|Metadata|UnicodeProvider|Diagnostics|StoredControls|Cancellation|Smoke)$' REFERENCE=1
make check RUN='^TestVersionInformation(Carrier|Binding|Remap)$'
make check RUN='^TestReferenceVersionInformationSmoke$'
make check RUN='^TestReferenceVersionInformationSmoke$' REFERENCE=1
```

Smoke executes real new inquiry values and held reuse, Unicode operation witnesses,
full representative misuse diagnostics, stored recipes and native logical replay.
Expected passing output is the quiet package summary/exit0, not banner stdout.
Every changed definition requires both selected modes before review; the internal
carrier/binding/remap units supplement, rather than replace, public SQL proof.

### Transaction status with default tracking

`TestReferenceTransactionIdentityStatusDefaultTracking` independently requires
`track_commit_timestamp=off`, unassigned read-only inquiry, and actual
in-progress/committed/aborted status through direct and held prepared calls.
Status retention must not depend on optional commit timestamp tracking.

```sh
make check RUN='^TestReferenceTransactionIdentityStatusDefaultTracking$' REFERENCE=1
make check RUN='^TestReferenceTransactionIdentityStatusDefaultTracking$'
```

Use external 900-second deadlines. The complementary tracking-ON definitions
and all-six-function smoke live in `reference/locking_transactions`.


### Session messages

The six untagged `TestReferenceSessionMessages` definitions exercise the same
public SQL/receiver body on native and pinned PostgreSQL18:

* `Delivery`: direct and actually prepared utilities, effect-free preparation,
  zero affected counts, empty Query results, peer/self/default/Unicode events,
  full positive sender PID and same-physical recovery.
* `Transactions`: committed-only publication, rollback, exact per-transaction
  dedup/order, both savepoint restoration paths, RELEASE, READ ONLY and implicit
  message failure versus an explicitly committed prefix.
* `RecipientBoundaries`: committed current-at-scan subscriptions, registered
  add-channel history versus first-registration no-backfill, outer commit/abort
  release, CHAIN/internal COMMIT→BEGIN withholding and irrevocable ready data.
* `Diagnostics`: complete portable English fields, shared identifier clipping/
  empty-name positions, 7999/8000 UTF8-byte payload limits, oversized actual
  Prepare then failing execution, and unchanged SQL PREPARE utility rejection.
* `Lifecycle`: borrower versus physical retirement, SAME physical timeout/reuse,
  sender retirement, DISCARD subsets, sticky history, native Reset/Restore/Dump,
  Driver/exact-DSN isolation and native cancellation precedence.
* `Publication`: shared notification/row/catalog commit FIFO, concurrent listeners
  agreeing on sender order, complete-or-none failure and concurrent public wait/
  peer commit. Deterministic admitted waiter/lease-drain proof is internal, not
  a runtime-stack observation or an unsupported public concurrent Close promise.

The native adapter calls exported `stuntgres.WaitForNotification`; the PostgreSQL
adapter remains inside one bounded `sql.Conn.Raw` callback on the fixture's pgx
connection. Neither raw receiver pointer escapes and notices remain diagnostic.
The [driver guide](../../docs/DRIVER.md#local-session-notifications) documents
cancellation, Raw serialization and logical PID lifetime.

Select each small group in **both** modes, with an external900-second deadline
per invocation. Changed definitions require fresh native and real proof:

```sh
make check RUN='^TestReferenceSessionMessages(Delivery|Diagnostics)$'
make check RUN='^TestReferenceSessionMessages(Delivery|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceSessionMessages(Transactions|RecipientBoundaries)$'
make check RUN='^TestReferenceSessionMessages(Transactions|RecipientBoundaries)$' REFERENCE=1
make check RUN='^TestReferenceSessionMessages(Lifecycle|Publication)$'
make check RUN='^TestReferenceSessionMessages(Lifecycle|Publication)$' REFERENCE=1
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$'
make check RUN='^TestReferencePreparedTransactionSessionBoundaries$' REFERENCE=1
make check RUN='^TestNotification(PublicationAtomicity|PublicationRoots|SavepointRoots|IdentityAndAssignment|SessionLifecycle|ReceiveOwnership|BatchOwnership|PhysicalCloseWake)$'
make check RUN='^TestNotification(SessionLifecycle|ReceiveOwnership|BatchOwnership|PhysicalCloseWake)$' RACE=1
make check RUN='^TestReferenceSessionMessages(Lifecycle|Publication)$' RACE=1
make quickcheck
```

Internal supplements use actual engine owners/operations: both publisher branches
reject stale captures and retry without overwriting a concurrently replaced ready
root, failed preflights install neither catalog nor messages, subscriptions do not
assign XID but outgoing events do, wide session IDs are not narrowed, savepoint
roots cap retained append capacity, payloads are shared with private recipient
links, and cancellation/dequeue cannot double-consume. RequestEnd reasons and
actual physical driver Close cancel admitted receive before lease drain.
Initialization unwind and once-only quiescent detach retire only their own owner.
The [prepared-transaction session boundary](../locking_transactions/README.md#session-message-prepare-restrictions)
proves sticky-history restrictions and failed-PREPARE cleanup; the independent
[rule-action NOTIFY rejection](../views/rule_authority_test.go) is unchanged.

#### Server transport boundary

Wire notifications use the same committed per-database queue as native receivers.
The actual-server witnesses cover successful idle NotificationResponse delivery,
commit/rollback, duplicate folding/order, listener transaction delay, UNLISTEN,
database isolation and disconnect cleanup. See the current
[Phase-2 contract and selected proof](#phase-2-server-selected-proof), including
`TestReferenceServerNotifications`, `NotificationsOrdering` and
`NotificationsLifetime`; the standalone pq listener smoke exercises idle delivery.

Run their documented native/reference selectors under external900-second
deadlines. LISTEN is supported on the wire; there is no separate unsupported
notification transport boundary.

The existing `TestReferenceServerCancellation` cancellation case now observes an
actual relation-lock wait before sending one CancelRequest on either backend.
Native wire cancellation keys are not logical `pg_locks` PIDs, so its serialized,
private-table witness selects the unique ungranted RowExclusiveLock instead.
This removes a pre-admission rendezvous race while retaining exact57014/message,
aborted ReadyForQuery and same-session rollback/recovery assertions.

```sh
make check REFERENCE=1 RUN='^TestReferenceServerCancellation$'
make check RUN='^TestReferenceServerCancellation$'
```

#### Standalone public executable smoke

Save the complete source below outside the repository (for example a fresh
`/tmp/session-messages-smoke.go`), run `go run /tmp/session-messages-smoke.go` from
the repository root under an external900-second deadline, then remove only that
temporary file. It uses no sockets, fixture or PostgreSQL container. Independently
expected stdout is exactly:

```text
before-commit: withheld
received: smoke committed sender-positive=true
rollback: discarded
unlisten: cleared
```

Every unexpected result or cleanup error exits nonzero. The first timed wait and
later successful receive use the same pinned native connection.

```go
package main

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "os"
    "time"
    "stuntgres"
)

func run() (err error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    connector, err := stuntgres.NewDriver().OpenConnector("notification-smoke")
    if err != nil { return err }
    db := sql.OpenDB(connector)
    defer func() { err = errors.Join(err, db.Close()) }()
    a, err := db.Conn(ctx)
    if err != nil { return err }
    defer func() { err = errors.Join(err, a.Close()) }()
    b, err := db.Conn(ctx)
    if err != nil { return err }
    defer func() { err = errors.Join(err, b.Close()) }()
    exec := func(c *sql.Conn, q string) error {
        _, err := c.ExecContext(ctx, q)
        return err
    }
    absent := func() error {
        wait, stop := context.WithTimeout(ctx, 50*time.Millisecond)
        defer stop()
        event, err := stuntgres.WaitForNotification(wait, b)
        if !errors.Is(err, context.DeadlineExceeded) || event != (stuntgres.Notification{}) {
            return fmt.Errorf("expected withheld event: %+v, %v", event, err)
        }
        return nil
    }
    if err := exec(b, "LISTEN smoke"); err != nil { return err }
    if err := exec(a, "BEGIN"); err != nil { return err }
    if err := exec(a, "NOTIFY smoke, 'committed'"); err != nil { return err }
    if err := absent(); err != nil { return err }
    fmt.Println("before-commit: withheld")
    if err := exec(a, "COMMIT"); err != nil { return err }
    wait, stop := context.WithTimeout(ctx, 2*time.Second)
    event, receiveErr := stuntgres.WaitForNotification(wait, b)
    stop()
    if receiveErr != nil { return receiveErr }
    if event.PID == 0 || event.Channel != "smoke" || event.Payload != "committed" {
        return fmt.Errorf("unexpected committed event: %+v", event)
    }
    fmt.Printf("received: %s %s sender-positive=%t\n", event.Channel, event.Payload, event.PID > 0)
    if err := exec(a, "BEGIN"); err != nil { return err }
    if err := exec(a, "NOTIFY smoke, 'rolled-back'"); err != nil { return err }
    if err := exec(a, "ROLLBACK"); err != nil { return err }
    if err := absent(); err != nil { return err }
    fmt.Println("rollback: discarded")
    if err := exec(b, "UNLISTEN *"); err != nil { return err }
    if err := exec(a, "NOTIFY smoke, 'removed'"); err != nil { return err }
    if err := absent(); err != nil { return err }
    fmt.Println("unlisten: cleared")
    return nil
}

func main() {
    if err := run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
```


### Live lock state

`view_locks*_test.go` defines shared, independently expected PostgreSQL/native
scenarios for all16 names/types, zero-row direct/held metadata, catalog ownership,
the eight relation modes, conversion/reentry/savepoints, live pending/granted/end
transitions, PUBLIC visibility without protected-table SELECT, both advisory
key spaces/count lifetimes, VXID/XID ownership, index/exclusion completion
purposes (prepared-before-index and live-writer PREPARE retargeting), row/unique
dependencies, type-object resources, prepared owners and
retained SSI predicates. Cancellation observes real admission before canceling;
native caller-context errors remain ordinary, while PostgreSQL's real server
cancel preserves complete57014 diagnostics. Deadlock witnesses join both
outcomes, identify the victim by error and validate actual graph identities.

Generated resource OIDs come from catalogs, PostgreSQL PIDs from the public
fixture inquiry and native physical IDs from their actual TEMP namespace.
XID relationships are backed by canonical-assignment units; prepared identities
also use `pg_prepared_xacts.transaction`. Native logical row/key waits never
pretend to supply physical tuple coordinates. The oracle may genuinely expose
physical tuple/fast-path/subtransaction state unavailable natively.
The shared metadata path relates `pg_class.reltype` to public `regtype` identity.
PostgreSQL's additional `pg_type` SQL source supplies its reciprocal row/array
catalog phase; native reciprocity uses actual catalog-provider APIs in
`TestPGLocksProjectionModes`, not a new adjacent SQL source. Enum identity uses
public `regtype::oid` on both backends. The exclusion completion witness uses
the existing supported btree equality constraint, not a physical GiST mechanism.
Live-to-prepared completion retargeting keeps the native original enqueue time;
PostgreSQL's waitstart is independently bounded, not assumed unchanged across tags.

Snapshot units race canonical first-XID assignment with already queued logical
dependencies. Each ordinary owner identity is sampled once per capture and
reused: all its waits agree on that identity and have the matching captured
exclusive owner, without nesting Engine and manager synchronization. Savepoint
coverage checks exact root-XID membership while the child is active and after
rollback, allows genuine PostgreSQL child XIDs, and independently verifies that
child data does not survive rollback or the subsequent commit.

Selected proof (external900-second deadline per command), each selector in
both modes after affected changes:

```sh
make check RUN='^TestReferenceViewLocks(Metadata|RelationModes|Transitions|PublicVisibility)$'
make check REFERENCE=1 RUN='^TestReferenceViewLocks(Metadata|RelationModes|Transitions|PublicVisibility)$'
make check RUN='^TestReferenceViewLocks(Advisory|TransactionOwnership|CompletionWaits|RowKeyWaits|Object)$'
make check REFERENCE=1 RUN='^TestReferenceViewLocks(Advisory|TransactionOwnership|CompletionWaits|RowKeyWaits|Object)$'
make check RUN='^TestReferenceViewLocks(Prepared|Predicates|Cancellation|Deadlock|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceViewLocks(Prepared|Predicates|Cancellation|Deadlock|Controls)$'
make check RUN='^TestPGLocks(ProjectionModes|TransactionLifetime|WaitDependencies|SnapshotOwnership|PredicateLifetime)$'
make check RACE=1 RUN='^TestPGLocks(WaitDependencies|SnapshotOwnership)$'
make check RACE=1 RUN='^TestReferenceViewLocks(Transitions|Cancellation|Deadlock)$'
```

Feature smoke uses public database/sql calls, including an actual held acquisition,
unrelated-role observer, exact16-column resource/NULL fields, bounded enqueue
timestamp, cancellation cleanup, restart, grant, release, negative advisory key
identity and actual row-XID contention:

```sh
make check RUN='^TestReferenceViewLocksFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceViewLocksFeatureSmoke$'
```

Native-only control phases additionally check owned snapshots, fresh held
execution after Reset/Restore, ordinary/wait/SSI fencing, retained real advisory
ownership and exclusion from Dump. PostgreSQL has no fake replacement-control
counterpart. Whole-project Docker-free verification remains `make quickcheck`.

### Blocking session information

`blocking_pids*_test.go` covers both `(integer) -> integer[]` signatures with
independent expectations, not backend parity alone. Direct, prepared and held
calls preserve strict NULL versus nonnull empty arrays, exact `_INT4` metadata,
full portable diagnostics and source character positions. Generated PIDs are
related to independently discovered physical owners: PostgreSQL uses the shared
fixture PID observer; native setup parses its real TEMP namespace suffix before
BEGIN. TEMP is never accessed inside a transaction intended for PREPARE.

Hard-owner, soft-predecessor and compatible-holder cases observe exact blocker
bags before release/cancellation. Prepared owners retain `{0,0}` then `{0}`;
origin exit or reuse does not substitute a historical PID. Safe acquisition
tests distinguish query/actual-Prepare waits from NOT DEFERRABLE, idle and
nonserializable state, including prepared safe-owner zero. Cancellation and
physical end clear actual waits; native Reset/Restore separately prove epoch
replacement, without a fictional PostgreSQL control equivalent. Workers are
bounded, canceled and joined before statement/connection or prepared-GID cleanup.
Stored views and held consumers re-evaluate recipes; Dump/Restore into a separate
engine does not restore waits or share captured array storage.
Session-advisory direct/held waits also expose the actual owner, clear canceled
transaction provenance while the holder remains alive, and permit a fresh wait
and exactly one transferred acquisition on the same physical session.

```sh
make check RUN='^TestReferenceBlockingPids(Signatures|Diagnostics|HardSoft|Prepared|SafeSnapshot|Lifecycle|StoredControls|Advisory)$'
make check RUN='^TestReferenceBlockingPids(Signatures|Diagnostics|HardSoft|Prepared|SafeSnapshot|Lifecycle|StoredControls|Advisory)$' REFERENCE=1
make check RUN='^TestBlockingPids(BindingAndIdentity|OrdinaryAuthority|SafeAuthority|Lifecycle|TraversalReplay|AdvisoryAuthority)$' RACE=1
```

Run selected proof with an external 900-second deadline. Whole-project
Docker-free verification remains `make quickcheck`; it does not replace either
selected backend run. Physical-domain/private/overflow identity, authoritative
queue eligibility, strict demand, live reevaluation, descriptor remapping and
traversal/replay units supplement the public scenarios.

The feature smoke is an ordinary temporary public Go executable, supplied for
the owning part's verification as `blocking-pids-smoke.go`, not a Go test or
permanent example binary. From the repository root, after materializing it at
`/tmp/pg18-blocking-pids-public-smoke.go`, run:

```sh
timeout --signal=KILL 60s go run /tmp/pg18-blocking-pids-public-smoke.go
```

It must observe an actual ordinary blocker before releasing it, an actual
safe-only blocker before rolling back its writer, subsequent empty arrays and
typed NULL/nonwaiting results. Only checked observations produce its three
summary lines. Native session allocation is not constrained to INT4; an
unrepresentable live blocker returns the documented ordinary range error.
Native scan goroutines are not PostgreSQL child sessions/lock groups: serial
fallback remains unchanged, and no fake worker identity or parallel contention
witness is claimed.

### Commit timestamp default-off contract

This package retains startup tracking off. The three selected inquiries report
`55000`, `could not get commit timestamp data`, with the PostgreSQL configuration
Hint for ordinary XIDs/last completion. Strict NULL inputs bypass tracking;
XID 0 instead raises `22023`; bootstrap/frozen XIDs 1/2 return unavailable
without consulting tracking. Unavailable origin results are present `(,)`
records, whereas NULL input is a whole SQL-NULL record.

Resolution coverage checks unary/zero-input arity, concrete wrong types,
origin's sole named input `xid`, absent names, qualification, quoted spelling,
literal/bound conversion errors, full portable diagnostic fields/positions
and actual direct/Prepare/Execute stages. Empty results preserve the declared
OUT schema without invoking the disabled runtime inquiry.

Selected proof, with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceCommitTime(Disabled|SpecialIDs|Resolution|DisabledSmoke)$'
make check RUN='^TestReferenceCommitTime(Disabled|SpecialIDs|Resolution|DisabledSmoke)$' REFERENCE=1
```

Standalone smoke observes off, rejects a SQL enable attempt with restart-only
`55P02`, checks strict/special-ID bypass and the exact disabled errors:

```sh
make check RUN='^TestReferenceCommitTimeDisabledSmoke$'
make check RUN='^TestReferenceCommitTimeDisabledSmoke$' REFERENCE=1
```

`TestReferenceSettingsUtilitySnapshotOwnership` migrates the settings utility's
public SHOW/current_setting/set_config values, typed NULL, result counts,
metadata and direct/prepared routes into this fixture. SHOW ALL independently
checks every native builtin and the same selected PostgreSQL rows, including
descriptions and order; PostgreSQL's additional server settings remain genuine
oracle output. Only private detached-result ownership/completion invariants
remain in the engine unit.

```sh
make check RUN='^TestReferenceSettings(UtilitySnapshotOwnership|ShowAllPlaceholdersAndLifecycle|ShowAllCurrentOverrides)$'
make check RUN='^TestReferenceSettings(UtilitySnapshotOwnership|ShowAllPlaceholdersAndLifecycle|ShowAllCurrentOverrides)$' REFERENCE=1
```

### Schema and temporary namespace identity

`namespace_identity*_test.go` uses public direct/prepared SQL and independently
specified NAME/_NAME/OID/BOOL results, including strict NULL and empty-result
metadata. PostgreSQL OID text-byte exports normalize to decimal strings; native
OID exports remain int64, without changing SQL metadata or inserting casts.
Coverage includes pending-TEMP initialization by each schema function, Prepare
without side effects, read-only first use, transaction/savepoint rollback and
publication, failed/canceled demand, actual TEMP/TOAST descriptor ownership,
role-independent identity, held getter reuse, drop/DISCARD lifetime, pooled
borrower reuse, physical retirement and destination-session view/Dump replay.
Special-path SQL assertions migrated from the mixed engine provider-default
unit retain empty/quoted/comma paths, failure atomicity and independent sessions;
private provider capture/parser units remain beside the engine.

Run each selected batch in both modes, with an external 900-second deadline:

```sh
make check RUN='^TestReferenceNamespaceIdentity(Signatures|Diagnostics|SpecialPaths)$'
make check RUN='^TestReferenceNamespaceIdentity(Signatures|Diagnostics|SpecialPaths)$' REFERENCE=1
make check RUN='^TestReferenceNamespaceIdentity(PendingTemp|FeatureSmoke)$'
make check RUN='^TestReferenceNamespaceIdentity(PendingTemp|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceNamespaceIdentity(Ownership|Retirement|StoredControls)$'
make check RUN='^TestReferenceNamespaceIdentity(Ownership|Retirement|StoredControls)$' REFERENCE=1
make check RUN='^TestReferenceSearchPath(CurrentSchemas|PendingTempBoundary|Diagnostics|StoredViewRuntime)$'
make check RUN='^TestReferenceSearchPath(CurrentSchemas|PendingTempBoundary|Diagnostics|StoredViewRuntime)$' REFERENCE=1
make check RUN='^TestNamespaceIdentity'
```

Feature smoke:

```sh
make check RUN='^TestReferenceNamespaceIdentityFeatureSmoke$'
make check RUN='^TestReferenceNamespaceIdentityFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceNamespaceIdentity(FeatureSmoke|PendingTemp|Ownership|Retirement)$' RACE=1
```

Expected observations are passing independent fresh-zero, strict-NULL,
separately demanded schema initializers, exact ordered path arrays, real
catalog-discovered own/peer TEMP+TOAST classification and empty-path NULL.
Generated OIDs are relational, not pinned allocator numbers. Namespace
classification never permits peer TEMP content reads. Whole-project native
verification remains `make quickcheck`; selected dual-mode proof is separate.

### Phase 2 server selected proof

Untagged `reference/sessions/server_*_test.go` definitions use one shared body
against an actual `cmd/stuntgres` subprocess and pinned PostgreSQL18.0. The opt-in
`reference/fixture.NewServerFixture` preserves the existing container/reset
ownership and does not add a server backend to unrelated embedded scenarios.
Direct calls retain sqlmw/pq and notice recording; wire controls use independent
raw connections, and client witnesses invoke actual `psql -X -w -At` with a private
PGPASSFILE. No oracle-discovery filter remains in the fixture.

Coverage retains the simple-query/accounts/database-isolation/process/persistence
baseline and adds real Parse/Bind/Describe/Execute/Close/Flush/Sync, named and
unnamed lifecycle, SQL prepared-name interoperability, typed text/binary inputs
and outputs, zero/all-NULL metadata, current ACL/reanalysis and portal retention.
Plain SELECT captures its snapshot at Bind and evaluates finite demand without
an N+1 lookahead: exactly N remaining rows still suspend, then an empty fetch
completes with count zero. Statement Close does not destroy an initialized portal.
SQL cursors and wire portals share name-collision arbitration, not execution policy.
RETURNING executes its writes once and reports the completing fetch's count.

Savepoint witnesses retire post-mark wire portals through both rollback paths,
without rewinding older portal positions or leaving their resource locks behind.
Failed unnamed Parse invalidates its previous definition; named duplicate errors
follow required analysis, preserving the original definition. Simple Query replaces
the unnamed portal only at actual creation: empty/parse/analysis/planning failures
preserve it, while runtime failure retires it. Genuine live SELECT output separates
planning EZ, runtime TEZ and partial-row TDEZ; volatile effects are not replayed.
Aborted Describe resolves missing names first and rejects retained row descriptors
before metadata, while NoData definitions remain describable. Multi-fault receive
cases independently pin consumed-prefix validation, argument diagnostic context
and lookup/ACL/abort precedence without whole-message suffix prevalidation.

SimplePlanning independently pins the wire-only static-operator planning phase:
unused EXISTS targets, statically pruned CASE/COALESCE branches and strict NULL
do not raise a hidden division error; demanded predicates and constant targets
still fail before metadata. Ordinary embedded empty-scan arithmetic retains its
separate row-owned boundary. Each wire case also proves same-physical42 recovery.

PortalCompletion distinguishes fetchable exhausted SELECT/RETURNING stores
from completed non-row DML, COPY and controls: the latter reject another Execute
with55000. FailedPortalIdentity keeps an older failed name after rollback-to,
while disposing its executor:55000 on reuse,42P03 on duplicate Bind, then explicit
Close permits the name again. WrappedPortalCounts asserts completing-fetch and
exhausted-zero counts from actual SQL EXECUTE results, not saved store totals.
UnnamedCreationFastPaths uses retained implicit work to pin CTAS WITH NO DATA
and existing-destination skips, no producer demand, named sibling preservation
and separate error/commit publication arms.

CopyCancelInput observes complete real input and preserves conversion precedence:
invalid input remains primary22P02 despite pending user cancellation; valid split
input raises57014 with its actual consumed-record context before CopyDone.
CopyCancelTerminal uses PostgreSQL's real administrator backend termination;
native instead stops its whole actual process through the public CLI and restores
the original committed table. Their FATAL/EOF versus EOF expectations are
independent host-control boundaries, not a native SQL termination provider.

PreparedCopy covers SQL EXECUTE of wire-created COPY and extended EXECUTE
wrappers across FROM/TO and all three formats, terminal failure atomicity,
completed-portal55000 and unused formal count/type/evaluate-once boundaries.
PreparedCopyQuery requires real H, a10-newline data prefix, then22012 with no
CopyDone/C and exactly two volatile demands. CopyBinaryProvider pins H then
42883 before binary signature/data even for an empty ACLITEM query.
CopyBinaryReceive pins declared input-provider initialization before G for empty,
NULL and omitted-ACLITEM-column streams in simple/extended routes, no publication
and recovery. CopyBinaryDomainReceive distinguishes declared domain_recv from
base invocation: empty streams get G/COPY0; NULL input gets G then42883 with real
COPY line/column context. COPY output's H-before-send-initialization is distinct.
SimpleExecuteTiming distinguishes outer utility portal creation/store execution
from direct plain SELECT: lookup/planning replace unnamed state, runtime errors
precede T, and an older named sibling retains its independent position.

NullableBinaryProviders separates binary NULL input availability at Bind, binary
send initialization at the first actual tuple and empty-result noninitialization.
DomainMetadata pins base VARCHAR1043/length-1/typmod11 through a domain chain in
simple/statement/portal descriptions, preserving origin, formats and logical
formal identity. FunctionCallReceivePrecedence pins arity before selector count,
its exact format-count template and signed invalid-size diagnostics with
ordinary F recovery in implicit and explicit transactions.
FrontendEncoding pins fourteen normal-protocol text boundaries: Parse names and
source, Bind/Describe/Close/Execute names, invalid continuation/truncated UTF8 and
name-before-later-field precedence. Exact22021 diagnostics precede publication;
valid case-sensitive Unicode names over63 bytes work, the old unnamed definition
survives decode failure, skipped messages stay absent and the same owner recovers.

NestedPreparedExecute invokes wire-published EXECUTE wrappers through the shared
SQL name, in simple and extended routes. COPY retains its real stream and
runtime-error prefix; SELECT retains its outer utility store and finite-fetch
counts without replay. Each utility retains its own explicit unused formals and
evaluates its actuals once; those values do not promote `$1` into a nested
utility's lexical parameter namespace (42P02 after the outer evaluation).

PortalPresentation separates retained typed values/position from current wire
rendering. Live REGCLASS recaptures qualification after search-path changes;
stored SQL EXECUTE DATE rows use current DateStyle on each destination. Borrowed
spelling maps recycle per row; immutable embedded result environments are intact.
ParameterStatusBoundaries pins startup reporting, final-value coalescing after
all simple-query completions, no status on extended Flush/Describe, status at
Sync, and restored values after error/rollback. Settings use the existing ready
boundary, not per-statement output.

MixedCycles independently covers unsynced bound/completed/suspended work followed
by FunctionCall, single/multiple/empty Query, implicit/explicit transaction state,
peer publication, portal existence, subsequent Sync and recovery suppression.
COPY witnesses use actual pq.CopyIn, psql and raw simple/extended streams in text,
CSV and binary formats, including fragmented/multiline records, NULL/empty,
late conversion/CopyFail/EOF atomicity, cancellation and output backpressure.
Text marker tails are discarded through CopyDone, but CopyFail still vetoes
publication; binary trailer tails are rejected. FunctionCall exercises actual
abs OID1397 and length OID1317, not lookup-only implementation-name aliases.
Idle notifications assert channel/payload/notifier PID without another query,
commit/rollback/order/folding, listener transaction delay, database isolation,
UNLISTEN and sender retirement. Protocol3.0/3.2 opaque-key cancellation and
3.3-to-3.2 negotiation accompany released recoverable/fatal/EOF framing checks.

The codec matrix uses explicit Parse OID hints with `SELECT $1`, so an executable
wire codec does not accidentally require unrelated SQL CAST syntax. Catalog-owned
type identities are obtained through public regtype authority, never copied into
byte goldens. Successful table storage is asserted only where admitted. Internal
CHAR has raw codecs but no user-column admission. Lookup-only MONEY,
REGOPER/REGOPERATOR and REGCONFIG/REGDICTIONARY arrays explicitly fail native
Parse, while PostgreSQL exercises real providers. TID has no native binary
provider; ACLITEM/query_int lack binary output in both native and pinned PG.
Zero/numeric text-search aliases remain usable without promoting absent native
named configuration/dictionary providers. No universal SQL/client/type claim follows.

Structured SQL error records retain original prepared source and associated
typed/formatted NULL/empty/raw parameters in error.log. Both SQL and operational
sinks remain valid JSONL without credentials or cancellation secrets; server.log
is not required to duplicate SQL errors. ACL-only and FunctionCall fixtures
restart their existing actual native child with optional dumps disabled before
SQL setup: builtin ACL mutation is not a serializable native logical-Dump input,
and unsupported transactional ACL utilities are not enabled for test setup.
They still restore effective PostgreSQL builtin authority and clean up real
ordinary roles/relations/schemas. Independent persistence tests retain default
dump/restore behavior, including restored-grant denial.

The cancellation scenario retains one lock-owner connection across subtests.
Each owner-using subtest renews the same bounded20-second socket deadline before
its first owner operation; preceding subtests do not consume that scenario's
allowance. Dial, observer, cancellation and worker-join bounds are unchanged.
Actual wire57014, transaction state, atomicity and continuation remain required.

Only `make finalcheck` enables the negative `noservertests` build tag. It excludes
all `server_*_test.go` socket scenarios/helpers here and the Linux TCP PID
lifecycle test/helper in `cmd/stuntgres/process_socket_test.go`. Default selected
checks, quickcheck and racecheck still include real server sockets. Explicit
selected PostgreSQL checks still run the pinned oracle; this tag is not a general
no-network mode and does not change production builds. Server fixture helpers
remain compiled without automatically starting a server.

The migrated `TestServerPIDSocketLifecycle/DumpAndStopActualChild` preserves the
real listener/readiness, TCP dial, dump signal/output, stop/join and PID retirement
witness. Its subprocess entry point is `TestServerSocketProcessHelper`. Socket-free
`TestServerPID`, `TestServerStartupFailure` and their failure helper remain enabled
under `noservertests`, along with CLI/users/PTY, byte-backed framing, logging,
persistence-file and startup-parameter mechanics.

Run each selector in quick mode and again with `REFERENCE=1`, under an external
900-second deadline per invocation:

```sh
make check RUN='^TestReferenceServer(Protocol|Clients|ExtendedLifecycle)$'
make check RUN='^TestReferenceServerExtended(Codecs|Metadata)$'
make check RUN='^TestReferenceServerPortal(Snapshot|Resume|Lifetime|Atomicity)$'
make check RUN='^TestReferenceServerExtended(Transactions|Pipeline|ACL|Rebind)$'
make check RUN='^TestReferenceServerMixedCycles$'
make check RUN='^TestReferenceServer(PortalSavepoints|FailedUnnamedParse|UnnamedPortalQueryLifecycle)$'
make check RUN='^TestReferenceServer(AbortedDescribe|ParsePrecedence|ReceivePrecedence)$'
make check RUN='^TestReferenceServerSimplePlanning$'
make check RUN='^TestReferenceServer(PortalCompletion|FailedPortalIdentity|UnnamedCreationFastPaths)$'
make check RUN='^TestReferenceServer(WrappedPortalCounts|CopyCancelInput|CopyCancelTerminal)$'
make check RUN='^TestReferenceServer(PreparedCopy(Query)?|CopyBinaryProvider|SimpleExecuteTiming)$'
make check RUN='^TestReferenceServer(NullableBinaryProviders|DomainMetadata|FunctionCallReceivePrecedence)$'
make check RUN='^TestReferenceServerNestedPreparedExecute$'
make check RUN='^TestReferenceServer(PortalPresentation|ParameterStatusBoundaries)$'
make check RUN='^TestReferenceServerCopyBinary(Receive|DomainReceive|Provider)$'
make check RUN='^TestReferenceServerFrontendEncoding$'
make check RUN='^TestReferenceServerCopy(RoundTrip|Atomicity|Recovery|Cancellation|Backpressure)$'
make check RUN='^TestReferenceServerFunctionCall$'
make check RUN='^TestReferenceServerNotifications(Ordering|Lifetime)?$'
make check RUN='^TestReferenceServer(ExtendedMalformed|VersionCancellation|Negotiation)$'
make check RUN='^TestReferenceServer(PreparedLogs|Phase2Lifetime|Phase2Smoke)$'
make check RUN='^TestReferenceServer(Authentication|Queries|Transactions|Persistence|RestoredGrants|Logs|Daemon)$'
make check RUN='^TestReferenceServer(Cancellation|OutputCancellation|PipelineLifetime|FatalSession)$'
make check RUN='^TestServer(CLI|Users|Framing|PID|PIDSocketLifecycle|StartupFailure|PersistenceFiles|Logging|StartupParameters)$'
make check RUN='^TestServerUsers'
```

Selected race proof uses `RACE=1` in both modes for PortalLifetime,
PortalAtomicity, ExtendedPipeline, CopyCancellation, CopyBackpressure,
NotificationsLifetime, Phase2Lifetime, PortalSavepoints, UnnamedPortalQueryLifecycle,
AbortedDescribe, FailedPortalIdentity, UnnamedCreationFastPaths, CopyCancelInput,
CopyCancelTerminal and the existing cancellation/persistence/restored-grants/daemon/
output/pipeline/fatal selectors. Race-enabled fixtures also
build the actual subprocess server with `-race`; instrumenting only the parent test process
would not prove server goroutine safety. Required psql, Docker or pinned-image
failures fail visibly, never skip or fall back. Mechanical CLI/verifier/framing/
PID/terminal/file-permission tests stay in `cmd/stuntgres`, without public SQL.
Non-Linux foreground builds and Windows DACL tests require their platform runners
for runtime proof; a cross-build alone establishes compilation, not ACL behavior.

Focused deadline-remedy proof and real-server smoke retain default TCP coverage:

```sh
make check RUN='^TestReferenceServerCancellation$'
make check RUN='^TestReferenceServerCancellation$' REFERENCE=1
make check RUN='^TestReferenceServerCancellation$' RACE=1
make check RUN='^TestReferenceServerCancellation$' REFERENCE=1 RACE=1
make check RUN='^TestServer(PID|StartupFailure|PIDSocketLifecycle)$' RACE=1
```

Each command has an external900-second deadline. Expected cancellation outcomes
remain57014 with the complete message/state, unchanged count1/value1 and SELECT42
continuation, without inherited-deadline write errors. Selected lifecycle proof
must still exercise the actual TCP child rather than just helper entry points.
Finalcheck's socket exclusion does not substitute for these selected proofs.

Separate executable smoke builds the real command, prompts through `passwd`,
starts a daemon with two database grants, and observes psql write/read42 plus an
independent pq read42 and42P01 isolation. It signals a dump, stops, restarts with
`-r`, and observes the same independent values through both clients. Daemons are
stopped and owned smoke artifacts removed. When capturing `start`, use ordinary
files rather than pipe-backed `CombinedOutput`: the daemon intentionally inherits
stdout/stderr, keeping such pipes open beyond its parent's exit.

#### Separate built-server Phase 2 feature smoke

This is additional executable evidence, not a substitute for selected socket
tests. The implementation handoff supplies the complete reviewed client source
as OMP `local://protocol-phase2-smoke.go`; materialize that source as `$S/client.go`
before running it, rather than reconstructing snippets. It imports actual lib/pq
and stdlib only. Its bounded `seed`, `check DSN S`, `cancel` and `logs` commands
close statements/connections/listener/rows and roll back failed transactions.
The source is temporary test material, not a production command or repository
script.

Choose an unused loopback `$PORT` and an owned private directory `$S` outside the
repository (`umask 077; S=$(mktemp -d)`). Build the server and use its real terminal
password prompt. Create `$S/pgpass` with mode0600 and a properly escaped record
`127.0.0.1:PORT:app:alice:PASSWORD`, using that same prompted credential; never
place the password in a command argument, DSN, transcript or source. Export
`PGPASSFILE="$S/pgpass"`. The source and private credential are prerequisites
to the following commands, not fabricated backend/client substitutes.

Run from the repository root, with an external900-second deadline for each
build/client/control invocation. `start` output must go to ordinary files, not a
pipe inherited by its child. Stop the owned server on any failed assertion.

```sh
set -eu
go build -o "$S/stuntgres" ./cmd/stuntgres
"$S/stuntgres" passwd -u "$S/users" --databases app,other alice
"$S/stuntgres" start -u "$S/users" -l "127.0.0.1:$PORT" \
  -d "$S/dumps" -L "$S/logs" -p "$S/server.pid" \
  >"$S/start.out" 2>"$S/start.err"
trap '"$S/stuntgres" stop -p "$S/server.pid"' EXIT
DSN="host=127.0.0.1 port=$PORT dbname=app user=alice sslmode=disable"
go run "$S/client.go" seed "$DSN"
printf 'COPY phase2_smoke FROM STDIN;\n3\tpsql\n\\.\n' |
  psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN"
# CSV uses protocol stdin EOF, not a literal \. data row.
printf '4,"comma,\nline"\n' |
  psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
    -c 'COPY phase2_smoke FROM STDIN (FORMAT csv)'
psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
  -c 'COPY (SELECT id,v FROM phase2_smoke ORDER BY id) TO STDOUT' >"$S/text.out"
psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
  -c 'COPY (SELECT id,v FROM phase2_smoke ORDER BY id) TO STDOUT (FORMAT csv)' >"$S/csv.out"
psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
  -c 'COPY (SELECT id,v FROM phase2_smoke ORDER BY id) TO STDOUT (FORMAT binary)' >"$S/binary.out"
psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
  -c 'CREATE TABLE phase2_binary(id int,v text)'
psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN" \
  -c 'COPY phase2_binary FROM STDIN (FORMAT binary)' <"$S/binary.out"
if printf 'COPY phase2_smoke FROM STDIN;\n5\tprefix\nbad\tfail\n\\.\n' |
   psql -X -w -At -v ON_ERROR_STOP=1 -d "$DSN"; then
  printf '%s\n' 'Expected malformed COPY to fail' >&2
  exit 1
else
  test "$?" -eq 3
fi
go run "$S/client.go" check "$DSN" "$S"
go run "$S/client.go" cancel "$DSN"
go run "$S/client.go" logs "$DSN" "$S/logs"
"$S/stuntgres" dump -p "$S/server.pid"
"$S/stuntgres" stop -p "$S/server.pid"
trap - EXIT
```

Expected observations:

- `seed`: `pq parameterized held COPY binary notification OK`, with direct
  parameterized INSERT, held SELECT twice and UPDATE, real explicit-tx pq.CopyIn,
  binary bytea00ff, idle pq.Listener Channel/Extra and rollback/sentinel ordering.
  pq.Notification does not expose notifier PID; raw Notifications tests prove PID.
- Text/CSV input each reports COPY1; binary input reports COPY4. The malformed
  input exits3 with integer-conversion error on line2 and must not publish id5.
- `check`: `pq rows and failed-COPY recovery OK`. Both tables contain exactly
  `(1,bound),(2,copy),(3,psql),(4,"comma,\nline")`. It checks text.out37 bytes,
  csv.out38 bytes and binary.out101 bytes against independent payloads, not parity:
  text `1\tbound\n2\tcopy\n3\tpsql\n4\tcomma,\\nline\n`;
  CSV `1,bound\n2,copy\n3,psql\n4,"comma,\nline"\n`; binary canonical19-byte
  PGCOPY header, four two-column rows, network int4 IDs and UTF8 text lengths
  5/4/4/11, then the ffff trailer.
- `cancel`: `pq active lock cancellation 57014 then 42 OK`, using a real held
  relation lock and100ms statement timeout followed by same-owner recovery.
  This is not attributed to CancelRequest; raw version/cancellation tests
  independently prove separate-connection opaque-key cancellation.
- `logs`: `prepared source parameters JSONL OK`, requiring SQLSTATE22012,
  exact source `SELECT 10 / $1::integer /* phase2-log-source */` and structured
  parameter `{index:1,oid:23,type:integer,format:text,null:false,encoding:text,value:"0"}`
  in error.log, and valid JSON/redaction in both log files.
- Dump and stop succeed and the actual child retires; inspect the owned dump/log
  files before removing the temporary source/materialization/credentials.

Root observed these feature outcomes on an actual built server with actual
lib/pq and psql16.15 clients in the Phase2 implementation smoke (tests-10);
the separate SQL oracle remains pinned PostgreSQL18.0 UTF8/C. That observation
does not imply universal client compatibility, SQL CAST/storage providers,
extra authentication/TLS, WAL/replication or physical PostgreSQL durability.

Whole-project Docker-free gates remain `make quickcheck`, `make racecheck`, and
post-integration `make finalcheck` (external1800-second deadline). `make deps`
includes `./cmd/...`; all non-standard production dependencies must be module
paths. Finalcheck does not replace selected dual-mode proof.

### Prepared rebind selected proof

The shared untagged `reference/sessions/prepared_rebind_*_test.go` scenarios use
only public SQL/driver APIs and the existing fixture. They independently assert
the earlier-shadow nontrigger for SELECT and all supported write targets/sources,
effective versus raw paths, namespace/USAGE/$user/TEMP transitions, live ACL/data,
recursive view/unused-CTE dependencies, fixed formals/results, full original-source
diagnostics, compatible empty-result metadata, complete scalar/array captures and
bounded admission/cancellation. The moved-shadow case in `reference/alter/` shares
the same retained-source expectation, not a native-divergent branch.

Private Publication subcases use fixed input clocks to distinguish refreshing
genuine scalar/temporal-array captures from retaining enum/composite logical
compounds, including their embedded temporal fields. Existing composite shape
and enum rename/native-control reference expectations remain authoritative;
these must not be repinned to accommodate changed capture lifetime semantics.

Admission success creates the shadow before acquisition; separate deterministic
target/nested-source cases create it during the wait and require the established
native ErrCatalogReplaced fence, unchanged captures and no effects. Existing
SchemaLockedBindingShadowCreation and QueryJoinsSecondLeafGrantRebind assertions
are preserved, not repinned. Namespace's schema SQL GRANT/REVOKE phase is pinned
PostgreSQL-only because that native syntax is absent; native public outcomes
explicitly transfer schema ownership to the reader before authorization, asserting
owner-visible path/shadow9 while bootstrap-owned tables retain shared explicit
SELECT ACL deny/regrant behavior. Ordinary private schemas do not supply default
reader USAGE; the native setup does not invent schema SQL GRANT support.
Validity's RealNamespaceACLInvalidation subcase uses the existing real
SetObjectACL seam to prove namespace callbacks, qualified42501 and handle repair.

Root-run selected commands, each with an external900-second deadline:

```sh
make check RUN='^TestReferencePreparedRebind(Shadow|Dependencies|Captures)$'
make check REFERENCE=1 RUN='^TestReferencePreparedRebind(Shadow|Dependencies|Captures)$'
make check RUN='^TestReferencePreparedRebind(DDL|Metadata|Path|Namespace|Admission)$'
make check REFERENCE=1 RUN='^TestReferencePreparedRebind(DDL|Metadata|Path|Namespace|Admission)$'
make check RUN='^TestReferenceAlterSchemaSearchPathAndViews$'
make check REFERENCE=1 RUN='^TestReferenceAlterSchemaSearchPathAndViews$'
make check RUN='^TestPreparedRebind(Validity|DependencyManifest|Publication|Admission)$'
make check RACE=1 RUN='^(TestPreparedRebindAdmission|TestReferencePreparedRebindAdmission|TestSchemaLockedBindingShadowCreation|TestQueryJoinsSecondLeafGrantRebind)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferencePreparedRebindAdmission$'
```

Test-first discovery runs the pinned oracle before native red; any temporary
new-scenario-only backend filter must be removed before native proof and never
survive review. Every changed/new definition requires final selected runs in both
modes. Internal identity/publication/admission units supplement public witnesses.
Root runs whole-project Docker-free quickcheck only after edits are stable.
The separate session-supplied public Go smoke uses one physical Conn, Go and SQL
held statements, direct shadow9/held7, late-only INSERT, dependency rebind9, shape
0A000/repair and changed path. Resolve its source to an absolute path S and run
`timeout 60s go run "$S"` from the worktree, recording actual output and exit;
remove only a root-created throwaway materialization afterward.

### DISCARD selected proof

The untagged shared `reference/sessions/discard_*_test.go` definitions assert
independent resource/category outcomes, full diagnostics and public physical
identity. Initial pinned PG-only discovery is removed before native red and
final proof. Production follows the pinned oracle/native-red checkpoint.
Select both bounded groups, each with an external900s deadline:

```sh
make check RUN='^TestReferenceDiscard(GrammarAndUtilityPhases|CategoryPrecision|PlansRetainedCaptures|PlansSignatureFence|SequenceMemoryAndCache|TemporaryClosureAndTransactions|TemporaryCollationProjection)$'
make check REFERENCE=1 RUN='^TestReferenceDiscard(GrammarAndUtilityPhases|CategoryPrecision|PlansRetainedCaptures|PlansSignatureFence|SequenceMemoryAndCache|TemporaryClosureAndTransactions|TemporaryCollationProjection)$'
make check RUN='^TestReferenceDiscard(AllSettingsAuthorizationAndLocks|TransactionGates|PoolExplicitCleanup|HealthyReuse|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceDiscard(AllSettingsAuthorizationAndLocks|TransactionGates|PoolExplicitCleanup|HealthyReuse|FeatureSmoke)$'
make check RUN='^TestDiscard(Grammar|OwnerPrecision|CaptureGeneration|TemporaryOverlay|ConfiguredReset|PublicationBoundary)$'
make check RACE=1 RUN='^TestReferenceDiscard(TemporaryClosureAndTransactions|PoolExplicitCleanup|HealthyReuse|AllSettingsAuthorizationAndLocks)$'
```

The TEMP collation reference covers durable column/index projection, owner-provider
absence, peer isolation and outer/savepoint rollback as well as committed cleanup.
PLANS signature coverage retains both an SQL definition and a Go handle through
result-shape rejection and restoration; private units additionally replace the
actual catalog lifetime and require that stale sequence authority never publishes
new captures or a generation stamp. Cursor rejection recovery checks the next
row (not a rewind), EOF with the original schema, and before-first-FETCH admission.
Use top-level selectors for these subcases, not slash-qualified selectors.

TransactionGates also checks DISCARD ALL first and after a write/setting prefix
inside a retained message: exact25001, no prefix publication, preserved SQL/TEMP/
sequence/settings/advisory resources and unchanged peers.

The configured-startup role/default supplement is a genuine PG options connection;
native configured defaults have separate private-owner units, not a new connector
API. Retained SQL errors use the original PREPARE source position (33 in temp_p),
not direct SELECT's15. Feature smoke selects `^TestReferenceDiscardFeatureSmoke$`
in both modes. The standalone public smoke is a root-materialized ephemeral
`.discard-feature-smoke/main.go` from the session artifact; run
`timeout 900s go run ./.discard-feature-smoke` and remove only that root-created
scaffold. It must exercise category cleanup, healthy retention, exact same-owner
cleaned reuse and unchanged peers/durable9, not merely Ping.

### Direct-message batch witnesses

Direct-message batch ownership, retained refresh contracts and selected proof now
live in [session_state](../session_state/README.md#direct-message-selected-proof).

### SQL cursor witnesses

SQL cursor HOLD, forward-only, motion and pg_cursors metadata contracts and
selected proof now live in [cursors](../cursors/README.md#testing).
Prepared transaction session boundaries remain owned by
[locking_transactions](../locking_transactions/README.md#testing).

### SQL prepared lifecycle

SQL prepared lifecycle definitions use `RUN='^TestReferenceSQLPrepared'` with
`make check` and
`make check REFERENCE=1`, each with a
900-second deadline. The feature smoke selector is
`RUN='^TestReferenceSQLPreparedFeatureSmoke$'` in both modes.
Independent expectations cover typed/inferred fresh values, declaration
boundaries, all currently executable preparable forms, full logical diagnostics,
EXECUTE expression evaluation and zero-formal extras, physical session ownership,
rollback persistence, utility Go-handle effect phases, and native controls.
Ordinary adapter argument failures are distinct from SQL diagnostics. Native
controls and eager retained-row guarantees have no invented wire counterpart.
Write-atomicity and canceled-write scenarios preserve PostgreSQL's INSERT RETURNING
witnesses; native uses the same RETURNING multirow INSERT for precancellation
and retains full failure/no-cursor/sentinel-data assertions. PostgreSQL's
pg_sleep cancellation witness remains a real server-only provider.
Retained eager native sql.Rows are consumed after DEALLOCATE and a real public
driver.Conn.Close invoked inside sql.Conn.Raw with a nil callback result. This
avoids conflating physical session end with database/sql's ErrBadConn wrapper-close
wait/cancellation while Rows still owns its borrowed-connection release.
`^TestReferenceSQLPreparedInquiryActuals$` additionally exercises already-supported
comment, deparse, index-property and visibility actual expressions, with independent
TEXT/BOOL/NULL values before comparison. The private
`TestPreparedSQLActualNoSnapshotDemand` checks that primitive/STRICT-null actuals
do not capture unneeded catalog environments; it does not replace public references.

### Settings and timeout witnesses

Settings, physical-session adapter and timeout contracts, including pinned FATAL
instrumentation and feature smoke, now live in
[session_state](../session_state/README.md#testing).

### Root driver session and catalog migration ledger

The whole-root cutover is complete: root retains13 focused nonSQL units and its
startup benchmark, with no public SQL test bodies or obsolete SQL helpers.
The final driver/session/catalog ledger accounts for372 original sources and131
helper dispositions, with651 shared untagged definitions,39 genuine PostgreSQL-only
and five PostgreSQL-required differential companions.36 genuine-only and all five
differential definitions remain tagged; three genuine-only callers are untagged.
Use [family commands](../../docs/DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover)
for exploration and the catalog's identical exact651-name RUN for native,
PostgreSQL and race acceptance. Select companions separately in real mode.

