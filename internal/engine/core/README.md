# internal/engine/core

Core is the engine runtime: `Engine`, `Engine.Mu`, catalog/storage ownership,
statement admission and scopes, transaction lifecycle, sessions and TEMP,
prepared SQL, cursors, query/DML orchestration, procedural runtime and protocol.
It imports lower packages, never the root or a statement family. Family executors
call its exported API; the root composes them.

## Hooks

`NewEngine` seeds stores, settings, visibility, locks, roles, providers and builtin
catalog state. Feature-owned providers register before builtin providers.
Composition installs two fixed hooks:

- `Engine.Statements` dispatches family statements. `Execute` is entered with
  nothing held. `ExecuteLocked` is a lock-domain continuation entered with
  `Engine.Mu` held and releases it on every handled return.
- `Engine.Factory` constructs composed private template seeds and Restore stages.

Cross-family orchestration belongs here; feature decisions remain in their
owners. Core runtime leaves (`catalogrt`, `contrib`, `explain`, `inquiry`,
`nativetrig`, `stats`, `tsearch`) own focused implementations behind core hosts.

## Cluster and logical database ownership

A Driver DSN owns one pointer-stable `DatabaseCluster`: shared metadata mutex,
role authority and database registry. Local engines share those cluster inputs
but own independent catalog domains, storage, visibility, replacement gates and
session resources. Equal local numbers never authorize cross-database use.
Shared publication prepares every affected immutable root before installation.

Physical startup reserves an exact database/principal admission before callbacks;
private maintenance/Restore sessions have distinct ownership and are not public
connections. Database retirement drains exact-incarnation resources before its
terminal publication. Template copying and native replacement remint local
ownership rather than importing sessions, handles or runtime monitoring history.

## Hosts

`statementHost` implements execution, DML and focused runtime capabilities over
`StatementScope`. `BindingHost` implements `analyze.Host`; `DDLHost` supplies the
common locked DDL seam embedded by family hosts; `sessionHost` implements
`session.Host`. `catalogRuntime` adapts the `catalogrt.Runtime` capture contract.
Implementations carry compile-time assertions and use subpackage types directly.

Family-owned statement/stage slots are opaque to core. Each owner recovers its
slot with one checked helper; nil means absent, never a typed nil.

## Lock order

`replacementMu` and `TransactionOwner.Mu` precede `Engine.Mu`, which precedes
`Session.Mu`, `SettingsStore.Mu` and `Engine.sessionMu`. Lock-manager mutexes are
never acquired under `Engine.Mu`. Callbacks, external I/O, waits and detached
expression work run outside metadata locks; see
[`lock`](../lock/README.md#mutex-ownership-and-lock-order).

Multi-owner capture discovers pointers under `sessionMu`, releases it, acquires
distinct owner mutexes in stable session order, then takes `Engine.Mu` and
revalidates the cohort. It never acquires peer admission or waits for completion
while holding those locks. Cluster `AdmissionMu` protects reservation/count state
only and is released before startup, cleanup or other lock domains.

Workload policies and cooperative-pause requests attach inertly to contexts.
Known-unlocked runtime owners activate the current invocation's local child after
capture, never the retained operation or definition. Callback readmission still
checks live operation, immutable transaction reference, inputs and catalog epoch.
Retained cursors/portals use each current request's context and release scoped
contexts between pulls; workers own private state and are joined before cleanup.

## Row mutation

Binding and capture produce immutable statement inputs. Detached executors use
real admitted physical identities; post-wait acceptance rechecks exact version,
storage, header and catalog authority. Stable attributes, RowID, VersionID and
lock resources are separate identities. Position hints never grant authority.

`WriteStage` and catalog journals carry accepted rows, descriptor changes and
index roots together. Finish is the statement publication/abort owner; explicit
transactions, savepoints, procedural checkpoints and PREPARE retain/transfer the
same immutable roots. Failure installs no partial state. Sequence effects and
explicit nontransactional maintenance retain their separate owner semantics.

Constraint/effect queues belong to the actual command/transaction. Nested command
completion settles its causal work without replaying completed actions; outer
completion drains remaining work. Trigger body entry shares balanced routine,
effective-user/settings and invocation-depth ownership. Exceptions restore the
actual stage/effect checkpoint, not a parallel undo journal.

## Prepared statements

`Description` owns fixed parameter/result signatures and immutable analysis.
Reanalysis and signature validation precede parameter import; accepted analysis
publication is independent of importer success. Ordinary dependency validity and
executable fold lifetime are separate checks.

Definitions, accepted typed folds and portals have distinct owners. Reanalysis
builds private candidates and publishes only after current identity, path,
dependency, type and provenance checks succeed. Current data and ACLs remain
fresh; no retained artifact caches a permission or live host. Failed publication
preserves accepted generations and backing, and old detached rows remain valid.
Portals retain only their accepted execution artifacts and release producer/frame
resources at their own terminal boundary.

## Immutable settings registry

`Engine.Settings` is built before provider registration and never mutated.
[`session`](../session/README.md) owns typed session values and journal actions;
[`settings`](../settings/README.md) owns pure definitions/parsing. Effective
routine environments are balanced on success, failure and cancellation.

## System catalog producer contract

[`pgcatalog`](../pgcatalog/README.md) owns producers, relation specifications and
OID lookup. Core selects the source's coherent catalog/role/presentation capture,
checks current source/backing routine authority and hands detached values to the
producer. Captured rows and prepared definitions never retain a live session,
provider or permission token. Catalog-runtime caches use exact immutable roots;
private catalogs capture independently.

## Catalog writes and built-in seed

Private stages prepare complete descriptor/storage/index candidates; shared and
local roots install in one callback-free region. Allocation frontiers commit only
with successful installation. Replacement performs unlocked fallible repair,
then checks the original destination predecessors before publishing; it never
silently adopts a newer destination. Destination TEMP and physical resources
remain with their genuine retention owners.

## Function runtimes

`functions` owns immutable recipes and argument demand; core runtime bodies own
live authority, lexical environments, resources and publication. Nested SQL
borrows the admitted statement and restores query/parameter/snapshot scratch on
all exits. Runtime leaves expose focused capabilities rather than engine state.

## Server protocol and timeouts

Wire messages reuse session, transaction, prepared-analysis and typed-frame owners.
Each message acquires its current command context; protocol cycles retain work,
not admission locks. Portals/cursors own producer lifetime across demand, and
COPY releases admission around I/O while preserving final cancellation fences.
Physical session cleanup joins outstanding work outside engine/session locks.

## Testing

Core tests cover private runtime contracts; public SQL belongs in `reference/`
under [docs/TESTING.md](../../../docs/TESTING.md). Lower-package engine fixtures
live in `core/enginetests/<area>`; family tests live beside family executors.
Use [`enginetest`](../enginetest/README.md) for common admitted fixtures.

Tests are external except pure units and area-owned `export_<area>_test.go`
accessors. Keep test-only accessors with their declaring owner; family tests
assert through exported APIs rather than core-private accessors. Composed tests
retain genuine accepted artifacts to check identity/backing and rejected
publication independently of SQL result equality.

Fixtures acquire actual statement `Gate` admission before relation waits or
runtime capabilities. Never recursively enter `Engine.Execute` while holding
admission. Lock tests observe the exact queued request, use deterministic deadline
controls and join workers; workers return outcomes for assertions on the test
goroutine. Runtime fixtures use real descriptor/provider authority, not fabricated
names, numeric identities or empty authorization.
