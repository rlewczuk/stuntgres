# reference/sessions

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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

### Phase 1 server selected proof

Untagged `reference/sessions/server_*_test.go` definitions use one shared body
against an actual `cmd/stuntgres` subprocess and pinned PostgreSQL18.0. The opt-in
`reference/fixture.NewServerFixture` preserves the existing container/reset
ownership and does not add a server backend to unrelated embedded scenarios.
Direct calls retain sqlmw/pq and notice recording; wire controls use independent
raw connections, and client witnesses invoke actual `psql -X -w -At` with a private
PGPASSFILE. No oracle-discovery filter remains in the fixture.

Coverage includes command counts/tags and typed row metadata, whole-input batch
admission, successful-prefix failures/rollback, I/T/E state and settings/notice
ordering; startup/authentication/database grants/isolation; fragmented/malformed
frames and explicit Phase1 extended/COPY/TLS boundaries; command/row-output
cancellation and disconnect cleanup; actual native dump replay through psql,
restart/restore, structured logs and genuine daemon/PID/startup failure controls.
PostgreSQL's genuine extended/TLS/GSS and frontend-processing behavior is specified
independently where the Phase1 boundary differs. Native allowlists/PID/file controls
are not fabricated PostgreSQL APIs. Empty native database dumps can be empty SQL.
Protocol negotiation transports the complete encoded version: Phase1 advertises
196608 (3.0), not0. Pinned18.0 and its actual psql18 client ground the downgrade
boundary; higher-only clients reject3.0 explicitly rather than receiving malformed
negotiation data. Skipped query-derived creations retain their utility completion
tags rather than pretending that a SELECT populated zero rows.
Canonical native session-end events also deliver fatal transaction/idle expiry
without an extra query or ReadyForQuery. Restored dump-only databases preserve
known owners after grant revocation while retaining42501 network denial.

The cancellation scenario retains one lock-owner connection across subtests.
Each owner-using subtest renews the same bounded20-second socket deadline before
its first owner operation; preceding subtests do not consume that scenario's
allowance. Dial, observer, cancellation and worker-join bounds are unchanged.
Actual wire57014, transaction state, atomicity and continuation remain required.

Only `make finalcheck` enables the negative `noservertests` build tag. It excludes
the twelve `server_*_test.go` scenario/helper files here and the Linux TCP PID
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
make check RUN='^TestReferenceServer(Queries|Transactions)$'
make check RUN='^TestReferenceServer(Protocol|Authentication|Clients|Negotiation)$'
make check RUN='^TestReferenceServer(Cancellation|Persistence|RestoredGrants|Logs|Daemon)$'
make check RUN='^TestReferenceServer(OutputCancellation|PipelineLifetime|FatalSession)$'
make check RUN='^TestServer(CLI|Users|Framing|PID|PIDSocketLifecycle|StartupFailure|PersistenceFiles|Logging|StartupParameters)$'
make check RUN='^TestServerUsers'
```

Selected race proof uses `RACE=1` for the cancellation/persistence/restored-grants/
daemon and output/pipeline/fatal-session selectors in both modes. Race-enabled
fixtures also build the actual subprocess server with `-race`; instrumenting only the parent test process
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

### Direct-message selected proof

The shared, unfiltered driver-batch definitions cover effects/counts (B1), result
sets (B2), parsing/prepared boundaries (B3), error timing/rollback (B4), transaction
boundaries (B5), clocks/settings/timeouts (B6), cancellation/lifecycle (B7), the
complete supported utility inventory (B8), diagnostics/authorization (B9),
retained block controls (B10), and the public feature smoke.
Run each selector below in native mode and again with `REFERENCE=1` against the
real pinned PostgreSQL18.0 UTF8/C oracle before review:

```sh
make check RUN='^TestReferenceDriverBatch(EffectsCounts|ResultSets|ParsePrepared|ErrorTimingRollback|FeatureSmoke)$'
make check RUN='^TestReferenceDriverBatch(TransactionBoundaries|ClockSettingsTimeouts|CancellationLifecycle)$'
make check RUN='^TestReferenceDriverBatch(UtilityAtomicity|DiagnosticsAuthorization|RetainedBlockControls)$'
make check RUN='^TestReference(DeleteCoreDiagnosticBoundaries|InsertConflictPredicateSuffix|RowsMetadataAndCommandSemantics)$'
make check RUN='^TestReferenceDriverBatch(PreparedBoundaries|InitialPrepare|Refresh(Composition|Atomicity|Promotion|Authorization|Cancellation|Controls))$'
```

These are shared acceptance definitions, not comparison-only parity assertions.
Utility observations use supported metadata and actual command/authorization
consumers rather than inventing pg_roles/pg_namespace/pg_attribute/pg_constraint/
pg_type or has_* inquiry support. Enum/composite/plain-domain messages create and
consume real typed destinations; missing-type CREATE TABLE probes establish
rollback and private-peer invisibility, and committed peers use the published type.
Class probes use name/kind plus the independently captured raw public namespace.
Domain CASCADE asserts actual exposed SELECT * columns/values, restored rollback
shape and missing-column42703. Physical relnatts retains dropped attribute slots
and is not an exposed-column counter.
Identity names remain NAME; raw generated OIDs are formatted in Go, not via new
casts. The renamed-column dependent view must display its current physical name
and still return its frozen output label/value. Selected summary regressions:
`make check RUN='^TestReferencePGTableViews'`, also with `REFERENCE=1`.
Native REINDEX peers read the old committed value before COMMIT and the final
value afterward; PostgreSQL independently observes its actual utility wait.
Column rename uses PostgreSQL's actual dependent-view wait before commit/join.
Native peers retain the old nonblocking catalog summary, but the dependent-view
consumer also waits: a bounded held-conflict deadline proves exclusion before
COMMIT, and the final consumer returns the new value afterward.
Other peer reads queued behind utility locks are released by commit before
joining; cancellation uses a held conflicting lock and bounded native deadline,
with PostgreSQL's public actual-wait observation.
Notice callbacks run only after a message and cannot synchronize its active wait.

The detached-private-role prepared COMMIT/ROLLBACK subcases are part of
RetainedBlockControls. They capture the real private role OID independently before
PREPARE, assert exact current_user/session_user and ordinary authorization
diagnostics afterward, then assert separate prepared finish and SET ROLE NONE
recovery. No discovery filter remains. A narrow selector must escape the dollar
before slash through make; unescaped `$/` can silently select no test:

```sh
make check RUN='^TestReferenceDriverBatchRetainedBlockControls$$/detached_private_role_prepared$$'
make check REFERENCE=1 RUN='^TestReferenceDriverBatchRetainedBlockControls$$/detached_private_role_prepared$$'
```

PreparedBoundaries pins first/post-COMMIT/post-ROLLBACK completion rejection
before lookup for existing and nonexistent GIDs, immutable prepared entries,
independent prefix disposition and unchanged singleton completion. InitialPrepare
pins warning25P01 plus an actual prepared empty implicit segment, independent
successful/failing suffixes and the singleton idle warning/no-entry contrast.

The six Refresh definitions retain independent duplicate/NULL row bags, DATA/
NO DATA population, source writes, private CREATE, repeated replacement, pending
role/MAINTAIN and MV-owner population, promotion before BEGIN, late rollback,
held-conflict cancellation and detached output across actual native control epochs.
Controls uses the unwrapped native pool sharing the notice connector's engine,
never the wrapped recording pool. PostgreSQL independently observes real waits
and57014; native uses its held-conflict bounded caller deadline. Source and MV
logical indexes publish atomically with their respective rows. Concurrent
retained refresh is also supported; genuine explicit-block and Restore refresh
exclusions remain intact.
Supplemental TestDriverBatchMaterializedReplacementRoots and
TestDriverBatchMaterializedPublicationGuards inspect first committed versus
immediate private predecessor, generation/end/root/index/role/TxRef/epoch/cancel
fences and row births at true end. TestDriverBatchMaterializedPromotionSavepoint
pins the retained replacement across genuine promotion, failure and mark recovery.
The shared `TestReferenceMaterializedRefreshConcurrentRetained` and
`TestReferenceMaterializedRefreshConcurrentRetainedCancellation` definitions
independently cover multiple targets plus RR old-root retention, private CREATE
and NULL-image matching, late suffix rollback, BEGIN promotion/rollback,
MV-owner runtime23505 disclosure and real source-wait cancellation. Native
Dump observations use the unwrapped pool belonging to the same fixture engine.

`TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized` adds a
retained outer refresh waiting on an ordinary source while a peer concurrently
publishes its inner MV. Two MV aliases and a changed ordinary source distinguish
one active producer snapshot from per-source recapture: the first/private/final
outer output stays old, and a fresh refresh reads both new sources. A late-error
variant rolls back the retained marker write while preserving the peer's commit.
`TestMaterializedRefreshConcurrentRetainedSourceCapture` exercises actual source
and retained-stage capture, generated source-only fences, one physical frontier,
and unchanged strict target/index/ordinary/provider predecessor observations.
The actual-function `WorkBound` regression bounds real cancellation checkpoints
and comparator work for large valid composite NULL-key groups and duplicate
preflight. `DuplicateDiagnosticOrder` keeps the earliest producer diagnostic
even when key sorting visits a later producer's duplicate group first.

### Testing retained concurrent refresh

```sh
make check RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestReferenceDriverBatchRefresh'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestReferenceDriverBatchRefresh'
make check RUN='^TestMaterializedRefreshConcurrent|^TestDriverBatchMaterialized'
make check RACE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetained|^TestMaterializedRefreshConcurrent'
make check RUN='^TestMaterializedRefreshConcurrent(WorkBound|DuplicateDiagnosticOrder|RetainedSourceCapture)'
make check RUN='^TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized'
```

Each selected invocation has an external900-second deadline. No public SQL
definition is duplicated in native-only tests.

The focused
TestDriverBatchPolicyIdentityPublication inspects actual private CREATE→ALTER/
rename OID retention, committed-root isolation, final installation and late
rollback/dependency closure; it does not add native pg_policy inquiry.

RoleRetirementPeers independently checks PostgreSQL's role-lock wait and captured-
OID42704 against native optimistic publication rejection after a peer schema or
policy dependency. Dispositions covers own dependency retirement, unrelated peer
changes, rollback, promotion, savepoints and prepared completion. Native schema
GRANT remains excluded; the PostgreSQL fixture alone grants/revokes public USAGE.
Pure retirement guards preserve exact catalog/role/frontier/prepared authority.

HoldTrueEnd observes a middle sequence inquiry before holdable-cursor materialization
at the actual message end, stable later FETCH values, and no draw/cursor after a
failed message. ForeignKeyComposition creates and consumes private parent/child
descriptors in one direct message; a late FK failure rolls back both descriptors.
These preserve current upstream capabilities, not adjacent feature implementation.

Run these additional bounded groups in native mode and again with `REFERENCE=1`:

```sh
make check RUN='^TestReferenceDriverBatchRoleRetirement(Peers|Dispositions)$'
make check RUN='^TestReferenceDriverBatch(HoldTrueEnd|ForeignKeyComposition)$'
make check RACE=1 RUN='^TestDriverBatchRoleRetirement(PublicationGuard|CandidateRestoration|Membership)$'
```

Wrap regex alternatives once; an internal `$|` is a Make automatic variable and
can silently erase an anchor/alternation, selecting no tests. No-match package
output is not behavioral evidence.


Package admission, message ownership, private catalog/roles/index sources and
cursor cleanup supplement those public proofs. Mechanical batch fixtures use
AcquireCommand, matching the public statement budget and SQL-activity/idle
completion boundaries, not plain native-control Acquire:

```sh
make check RUN='^TestDriverBatch'
make check RACE=1 RUN='^TestDriverBatch'
```

Each check invocation has an external 900-second deadline; targets own no deadline.
`make quickcheck` remains whole-project Docker-free verification, not a substitute
for selected real PostgreSQL evidence. Finalcheck's external deadline is 1800
seconds and likewise does not replace either selected mode.

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

### Pinned timeout FATAL instrumentation

Pinned timeout FATAL instrumentation keeps the real server PID, SQLSTATE and
exact primary message/analysis position separate from pq's public bad-connection
sentinel. Its Docker log client belongs to the existing pinned fixture lifecycle,
not a separate container or startup owner. Before arming an owner, capture its
official PostgreSQL `%c` incarnation from `pg_stat_activity` backend-start time
and PID. Baseline and later reads use `Since` backend-start minus one second;
exact PID/incarnation matching and baseline rejection preserve stale-evidence
protection without imposing any post-arming tail limit.
Read raw non-following responses synchronously with a 32 MiB evidence cap checked
before frame payload allocation. Cancellation closes the owned response and joins
its close callback; malformed/truncated frames, size/read/close errors fail rather
than accepting partial oracle evidence. This avoids rereading the full accumulated
suite history through per-frame pipes. All test deadlines remain unchanged.

### Holdable cursor verification

The untagged `TestReferenceSQLCursorHold` definitions share the existing session
fixtures and independent row/type/count/full-diagnostic assertions. Select bounded
groups (external900-second deadline per invocation):

```sh
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(CommitRemaining|AutocommitAndPrepared|RollbackAndSavepoints|Materialization|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(FailureAndPromotion|ScopeAndPresentation|Restrictions|NestedRowLocks|PrepareBoundaries)$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(Cancellation|SessionAndControls)$'
make check REFERENCE=1 RUN='^TestReferencePreparedTransactionUnsupportedSessionBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorHold(Recursive)?CatalogChanges$'
```

Repeat the same selectors without `REFERENCE=1` for selected native proof. Both
modes are required for changed reference definitions. No discovery filter remains.
Nested/stored-view rowmarks are admitted; top-level rowmarks retain the exact
read-only restriction. Successful portal exports survive later portal
materialization failure and final SSI publication failure while private writes
roll back. Partial-promotion assertions use independent sequence drain witnesses
and exact surviving rows, never PostgreSQL hash traversal order. Fixed-name
rotations must witness at least one actually drained candidate before failure.
The division target uses the prerequisite DeclarationTiming late outer projection
over an `ORDER BY i OFFSET 0` source, so a bounded first FETCH succeeds and the
remaining fault is genuinely demanded by COMMIT, not by a blocking target sort.
Scope assertions bracket first-demand timestamps, retain prior effective FETCH
values, preserve FETCH0 as nondemanding, and check current stored-FETCH rendering.
Catalog-change definitions independently capture each backend's own relation and
table-rowtype OIDs, then require renamed symbolic and dropped numeric output.
Scalar and recursive array/domain/composite values remain typed through commit;
unread earlier FETCH batches retain their original/renamed spelling despite peer
DDL. Removing the original query source cannot replay or rebind the stored output.
Mixed scope batches use the existing REGTYPE raw transport assertion (native
string versus PostgreSQL nonnil `[]byte`); other columns keep generic validation.
PREPARE assertions distinguish live/removed/failed/old HOLD entries; live HOLD
wins TEMP and empty-GID restrictions. The shared commit-warning case separately
asserts quiet Prepare/DECLARE, current COMMIT warning policy and no later
stored-FETCH evaluation warnings.

Private mechanism and selected race proof (external900 seconds each):

```sh
make check RUN='^TestSQLCursorHold(Grammar|Owner|Demand|Authority|Publication|Cancellation)$'
make check RACE=1 RUN='^TestSQLCursorHold(Owner|Authority|Publication|Cancellation)$'
```

The private probes use real installed producers, projection stages, SQL and Go
commit paths and canonical SSI publication. They assert original transaction
tickets/current unfinished commit scope with owner/engine/session mutexes unlocked;
zero retained producer/authority/source pins after export; unreplayed remainder
and zero-width cardinality; cancellation after actual projection admission and an
actual sequence lock wait, bounded joins, no private write publication and
retired timeout/ticket authority. Implicit HOLD installation is not completion,
while cancellation after successful commit cannot retract exported values/writes.

### Forward-only SQL cursor verification

Run the entire cursor family after changing an oracle expectation: a driver panic
can truncate later tests, so a focused passing run does not establish family
coverage. Each invocation has a separate 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceSQLCursor'
make check RUN='^TestReferenceSQLCursor'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorFeatureSmoke$'
make check RUN='^TestReferenceSQLCursorFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSQLCursorRegtypeWarnings$'
make check RUN='^TestReferenceSQLCursorRegtypeWarnings$'
make check REFERENCE=1 RUN='^TestReferenceRegtypeWarnings$'
make check RUN='^TestReferenceRegtypeWarnings$'
make check RUN='^TestSQLCursor(CompletedPublicationBoundary|CancellationClearsAuthority)$'
make check RUN='^TestSQLCursor'
make check RUN='^TestSQLCursor'
```

The permanent definitions have no discovery backend filter. Native-only controls
and explicit backend capability phases remain independently asserted. Cursor
assertions distinguish blocking volatile view sorts from demand-lazy outer
projections, logical lock-error context
from PostgreSQL's unstable physical tuple coordinates, and first-effective-FETCH
stable expression values from current lock/cancellation authority.
The role/GRANT transitions and application-name `current_setting` probe are
PostgreSQL-only capability evidence. The native scope witness retains its original
owner identity, uses an explicit literal application marker, and exercises
already-supported timestamp and regclass providers rather than enabling adjacent
role/settings features. Its regclass output changes from `cursor_rows` to
`public.cursor_rows` after changing the search path; its first-demand statement
timestamp remains equal on the second FETCH.
The empty-table fixture is created before BEGIN on both backends; cursor coverage
does not enable unrelated permanent CREATE TABLE inside explicit transactions.
Internal units cover detached ownership/pin release, exact-limit/no-lookahead
demand, zero-width/zero-position states, savepoint ordinals/stale transaction
references, nested view/later UNION authority, admitted cancellation/no partial
batch and quiet description/counter exhaustion.
Nested view/later UNION units require exact returned `(i,nextval)` values and
sequence state at DECLARE, each FETCH, EOF and CLOSE. Their consumer probes observe
the current FETCH context and unfinished operation, not producer readiness or
stored occurrence counts. The later UNION arm is first demanded by the second
FETCH; its common-type consumer expression is the evaluated target.

`TestSQLCursorCompletedPublicationBoundary` cancels synchronously after Execute
success but before Finish for MOVE, FETCH, DECLARE, CLOSE and CLOSE ALL. MOVE must
publish its affected count with a genuinely nil RowResult interface, not a typed
nil detached receiver: Query MOVE must expose empty metadata without invoking
detached-row methods. `TestReferenceSQLCursorMoveCountsAndPreparation` covers
that public direct/prepared Query boundary in both modes. Expect success
from repeated Finish, an active transaction, preserved install/removal and detached
row ownership. After rollback to a savepoint, the older cursor next returns row2
after its delivered row1, never replaying or skipping it; CLOSE is not resurrected.
`TestSQLCursorCancellationClearsAuthority` separately retains mid-demand failure,
no partial result, cleared admission and recovered55000 `portal "c" cannot be run`.
The cursor-only completion classification must leave noncursor private writes,
catalog and projected-root publication intact.

`TestReferenceSQLCursorRegtypeWarnings` uses four table TEXT `timestamp(7)` values,
not a planning-time literal cast, with alternating held/direct FETCH executions.
Prepare and DECLARE are quiet. Each demanded row returns REGTYPE
`timestamp without time zone`; current warning/error/notice/warning policies yield
one/no/one/one complete WARNING/22023 precision-reduction records, with empty
Position/Context and all other unspecified logical fields empty. Exhaustion retains
full schema and emits no diagnostic; there is no extra projected warning.
Run this definition and ordinary `TestReferenceRegtypeWarnings` in both modes
before review, preserving the existing direct-operation warning contract.

Stored view columns keep their declared native nullable metadata through cursor
view/derived/join consumers; base-table NOT NULL metadata is not substituted.
Analysis diagnostics separately assert the native unknown-function capability
error0A000 `functions are unsupported` and PostgreSQL42883 with its function Hint.
Failed native Restore retains its full42P02 diagnostic at parameter position53.

`TestReferenceSQLCursorAdvisoryScope` changes peer ownership between successive
FETCH batches, independently expecting true then false from the retained advisory
expression. DECLARE is quiet, EOF preserves BOOL metadata, and cursor/transaction
teardown leaves the peer's session hold intact. After changes to shared expression
authority, select the entire existing advisory family and prepared-transaction
boundary scenario alongside every cursor definition, in both modes before review:

```sh
make check RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionUnsupportedSessionBoundaries$)'
make check REFERENCE=1 RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionUnsupportedSessionBoundaries$)'
```

Each command has its own900-second deadline. The prepared-transaction boundary
scenario asserts the actual WITH HOLD/PREPARE restriction on both backends:
0A000 `cannot PREPARE a transaction that has created a cursor WITH HOLD`.
Its TEMP/listen/export capability boundaries remain unchanged. Hash and join logical-stage fixtures keep real
admissions alive until stage disposal, then finish the scope before
operation/session cleanup. The selected-private-roots join helper completes eager
finalization before releasing its statement and operation; detached output remains
usable after transaction rollback. These fixtures never rely on finished scope
authority.

`TestReferenceSQLCursorPreparedMetadata` checks absent and stale prepared FETCH
descriptions separately from a fresh direct FETCH's runtime schema. Its safe
INT4-to-INT4 alias rebind consumes actual held-statement rows: PostgreSQL advertises
the old alias but returns the new value, whereas native describes the live portal.
Native absent/rebound rows likewise use the live schema, not lib/pq wire quirks. The pinned
lib/pq transport retains its old binary decoder after a same-name INT4-to-TEXT
cursor rebound: iterating TEXT `new` through that decoder panics. The stale
description witness therefore inspects Query metadata without calling Next, closes
the returned Rows, and independently checks fresh direct runtime rows. It does not
recover the panic, promise decoded rebound-row parity, or change the production
driver to imitate this lib/pq limitation.

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

### Settings and session timeouts

Settings timeout witnesses live in `TestReferenceSettingsPublicTimeoutStages`
and `TestReferenceSettingsPublicTimeoutRollbackCleanup`. Once a physical owner
is armed with the genuine 1ms statement timer, even `SHOW` or `RESET` can
legitimately time out under scheduling pressure. The implicit-timeout scenario
therefore disposes of its armed owner without further SQL; transaction rollback
restores timeout 0 before checking SAME-owner and held-statement recovery.
Both prepared-execute routes prepare only the current candidate at timeout0,
then arm1ms before executing that genuine Conn-held handle. The rollback route
begins its transaction after preparation and closes the handle after rollback,
including fatal-path cleanup. After an untimed atomic23502 fallback, complete
candidate assertions and statement closure, then physically discard the still-armed
implicit owner without further owner SQL. The next reached candidate acquires a
fresh timeout0 owner. Rollback restores entry timeout0 on the same owner before
statement closure and recovery assertions.
All four bounded fallback sizes and the shared30s fixture deadline remain;
unused million-row fallback handles are not eagerly parsed/bound.
`TestReferenceSettingsSQL` checks tiny-timeout encoding on the arming command
and then disposes of that isolated owner. Do not increase the timer, retry an
expired-owner reset, or suppress its diagnostic to make fixture cleanup pass.
Generate and prepare only the reached bounded workload, before arming its owner.
Each candidate owns its held statement and physical connection through assertions
and closure; an untimed fallback disposes the armed implicit owner before the next
candidate opens a fresh timeout0 owner. Retain all fallback sizes and stage/backend
assertions without eager million-row preparation or a larger fixture deadline.

The `TestReferenceTimeout` family adds transaction, physical idle, cleanup and
pool contracts without duplicating the statement/lock corpus. Its ten names
are selected together below; `settingsTimeoutWorkload` is shared with the
existing CPU scenarios, and lock workers use `fixture.LockOperation` plus the
PostgreSQL external queue barrier. No new delay SQL is needed.

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$'
timeout 900s make check RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceTimeoutFeatureSmoke$'
timeout 900s make check RUN='^TestReferenceTimeoutFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceSettingsPublicTimeout'
timeout 900s make check RUN='^TestReferenceSettingsPublicTimeout'
```

The pinned full ten-definition discovery run passed before production changes.
The scenario-local PostgreSQL-only filter and its comments were then removed;
the root observed native regression from the three missing settings. Completed
checks now run the same shared bodies in both modes, with no discovery filter
remaining. That oracle result does not claim native or final verification.
The pinned run established pq's direct/Prepare/idle FATAL API
sentinel as ErrBadConn and Conn-held Execute as ErrConnDone, not portable
SQL diagnostic fields. The pinned fixture's `log_line_prefix` includes real
backend PID, session incarnation and SQLSTATE (`timeout-oracle|%p|%c|%e|`).
PostgreSQL [documents `%c`](https://www.postgresql.org/docs/18/runtime-config-logging.html#RUNTIME-CONFIG-LOGGING-WHAT)
as hexadecimal backend-start seconds plus hexadecimal PID; PG-only
`pg_stat_activity` instrumentation captures that identity before arming.
Docker `Since` reads start one second before that backend's start, preserving
boundary records without repeatedly fetching/reparsing the suite's full log
history. No `Tail` limit truncates expected errors. Exact PID/incarnation/code
matching and the same-suffix pre-arm baseline reject stale evidence; the exact
FATAL severity/English primary/Position remain independently asserted.
Idle-expiration boundaries and the feature smoke use bounded full-idle-window
retries rather than assuming a single fixed delay proves expiration. Owner probes
restart idle timers, so retry windows back off up to one second under a five-second
deadline; continuous short-interval owner polling would prevent expiration.
Only a live cursor or the exact aborted-transaction diagnostic permits another
window. The final FATAL diagnostic, unusable physical handle and rollback checks
remain mandatory.

A suite-owned Docker client reads each raw response synchronously, closes it
on every exit, and validates complete eight-byte frame headers and bodies under
a 32 MiB cumulative wire cap. Truncation, excess, daemon error streams and
read/close errors fail visibly; no unjoined pipe demultiplexer owns the response.
`TestTimeoutOracleLogFrames` exercises framing, cap and close/error ownership
without SQL. This fixture-cost remedy addresses cumulative reference race-suite
work, not an established single-test hang. The remedy changes no timed witnesses,
fixture budgets or check deadlines. Recorder comparisons likewise scan
backward to the latest successful matching query under the existing lock, with
the same predicate and owned-buffer assertions. No sentinel is converted into
a manufactured diagnostic object.

```sh
timeout 900s make check RUN='^TestTimeoutOracleLogFrames$'
timeout 900s make check REFERENCE=1 RUN='^TestTimeoutOracleLogFrames$'
```

Native still asserts its full structured portable diagnostics. Capture PID/OID
and log baseline before arming or Go BeginTx, then execute Go work through that
actual Tx: querying its borrowed Conn deadlocks behind database/sql ownership.
The pinned run established real server FATAL analysis locations: blocked SELECT
direct/Prepare has `at character 15`, blocked INSERT and PG CPU semantic Prepare
have `at character 13`. Assert the full independently specified suffix, not a
stripped message. Idle, direct CPU parse and cached Execute have absent Position.
Root native verification established that the1ms CPU Prepare budget instead
cancels incomplete parsing before scope.Describe; its exact25P04 FATAL has absent
Position. This is a genuine phase distinction, not a fabricated native semantic
location or a parsing-cancellation bypass. Deterministic locked semantic Prepare
still asserts Position15 in both modes. All routes retain full severity/code/
primary/optional fields, no published result/handle/rows, termination and cleanup.
Stored physical-session fatal is not blanket-decorated.
For Go-held execution, prepare `LOCK TABLE timeout_blocked IN ACCESS SHARE MODE`
on the actual Tx: this is metadata-only and retains no relation lock. Then the
peer acquires AX and the actual held Exec queues AS. Preparing SELECT inside that
Tx would retain AS and block the peer before Execute. Conn-held SELECT and
direct/semantic-Prepare SELECT routes remain separately covered.

The retained-Rows idle witness keeps native immutable Rows through the first
exact FATAL, reads/checks/closes them, and only then issues the dead-handle probe.
That later ErrBadConn can make database/sql close sql.Conn; doing so while its
previous Rows still owns the release callback blocks close until caller deadline.
This ordering preserves the retained-snapshot assertion, not a cursor lease leak
workaround. PostgreSQL's streaming Rows are still drained before transport reuse.
Detached prepared-entry lifetime compares exact identifiers/owner/database,
time.Time.Equal instant and zone name/offset, never time.Location pointer identity.

The first pooled INSERT99 after idle expiry succeeds through safe pre-work
replacement on pq; assert affected1 and exactly7,99, then remove99 and verify
new physical identity/default0. This is distinct from accepted blocked INSERT8,
which must remain unpublished and never be replayed. Savepoint restoration of
the configured positive duration after SET0 leaves SHOW positive but does not
rearm: independently observe a live owner beyond it, then require off/on expiry.

The timeout family's ordinary delay witnesses retain250ms budgets crossing
at350ms; shorter limits125ms remain below250ms. The four CHAIN timer-incarnation
cases use6s session and1s LOCAL budgets, with monotonic timestamps bracketing
BEGIN and AND CHAIN. Their successful SHOW occurs after both obsolete timer
windows but strictly before the earliest possible chained deadline; scheduling
overruns fail with measured brackets rather than accepting premature expiry or
retrying the transaction. Final25P04 and all peer-row assertions remain exact.
The positive-positive and same-value transaction assignment cases separately
use an original6s budget and assign12s or6s at the absolute BEGIN-return+3s
target. Monotonic BEGIN and SET/SHOW brackets require assignment to finish
before the earliest original deadline. They retain the same physical owner
and require exact25P04/dead-owner proof after the latest original deadline
but before the earliest possible assignment-refreshed deadline; scheduler
overruns fail explicitly, without retrying or accepting a later expiry.
Other assignment branches and the fixture's30s context remain unchanged.

```sh
make check RUN='^TestReferenceTimeoutTransactionAssignments$' REFERENCE=1
make check RUN='^TestReferenceTimeoutTransactionAssignments$'
make check RUN='^TestReferenceTimeoutTransactionAssignments$' RACE=1
```

Run each selected command with an external900-second deadline.

The old500ms budget after300ms has200ms residual in the separate precedence
witness, beating a newly configured375ms limit. Abort recovery waits1100ms beyond suppressed1s idle
and600ms beyond restored500ms. Active-idle limits remain100ms while actual
lock work lasts250ms (2.5 limits); CPU parse witnesses retain1ms. Registry
normalization/range inputs, observer polls, asynchronous joins and all
diagnostic/state/atomicity assertions are unchanged. The separate public
executable retains its independently specified100ms/300ms budgets and650ms
idle stimuli.

`TestReferenceTimeoutChainPrepared/detached-survives-origin-idle` allows eight
500ms idle intervals with a lock-free `SELECT 1` probe after each interval.
A successful probe retries; any error must still be the exact idle-session
FATAL57P05, and failure to terminate within the bounded attempts fails.
The probe must not read the table whose ACCESS EXCLUSIVE lock remains owned by
the detached transaction. Prepared-entry identity, retained-lock conflict and
eventual committed-row assertions remain exact.

The query/prepare/ping-reset branches of `TestReferenceTimeoutIdleBoundaries`
adapt only their timing stimulus to scheduler pressure. Idle budgets start
at250ms, double incrementally through500ms and1s, and stop at2s (four attempts).
Each attempt waits half its selected budget before each of three activities.
Before issuing an activity or the final identity check, elapsed time since
the prior operation START must be below three quarters of the budget. This
conservative interval includes response/assertion work and leaves dispatch
headroom. Only a measured invalid interval permits disposal of that physical
owner and a larger-budget attempt on a fresh owner; no SQL, Prepare, Ping or
identity error is retried. A valid attempt requires all three successful
activities on the SAME owner, cumulative elapsed time exceeding its SELECTED
idle budget, then unchanged identity and eventual57P05 after budget+100ms
without intervening owner activity. Exhausted timing/context bounds fail,
never skip. The original30s scenario context remains; each candidate reserves
three half-budget waits, final expiry and3s for bounded probes before starting.

Selected proof (external900-second deadline per command):

```sh
make check RUN='^TestReferenceTimeoutIdleBoundaries$'
make check RUN='^TestReferenceTimeoutIdleBoundaries$' REFERENCE=1
make check RUN='^TestReferenceTimeoutIdleBoundaries$' RACE=1
make check RUN='^TestReferenceTimeoutIdleBoundaries$' REFERENCE=1 RACE=1
```

The transaction-assignment command witness issues SHOW after two50ms idle
intervals, then probes expiry after175ms more. Its nominal275ms probe is after
the original250ms deadline but before an incorrectly rearmed350ms deadline;
the second successful SHOW has150ms nominal headroom. This test-only window
correction preserves the nonrearming assertion without changing provider budgets.

The complete pinned run also confirmed actual Go-Tx-held ErrBadConn transport,
distinct from Conn-held ErrConnDone. Native caller context cancellation retains
its ordinary error; pq health stays separate. Native mechanics selectors are
`timeout 900s make check RUN='^TestSessionTimeout'`
and `timeout 900s make check RUN='^TestSessionFatalNoReplayMechanics$'`.
These cover callback generation/token/ref/deadline authority, provisional adoption,
assignment/restoration, configured idle suppression, active/ready boundaries,
first end reason, copied fatal diagnostics, canonical RESET ALL batch/journal/cache
publication/restoration and no-replay adapter classification.
The root runs these and both completed reference modes after edits; readiness
or a discovery pass is not production verification.

`timeout 900s make check RUN='^TestSessionTimeoutPending'`
exercises actual Session liveness, not merely relation-wait diagnostic ordering.
Canonical scopes and real queued requests cover due/delivered lock ERROR,
absolute statement expiry before context delivery, both SQL deadlines, ERROR
claim before possible grant, full/child recovery and stopped-budget restoration.
The inverse grant/non-SQL terminal/completed/old-incarnation cases preserve
genuine25P04 FATAL. Concurrent grant-versus-expiry units require one atomic
outcome: LIVE/stopped/55P03 or terminal25P04, never stopped-budget success or
recoverable ERROR on a physically ended owner. Workers/callbacks join with
bounds; no SQL text, sleeps or production scheduler hook is used.

Changed complete builtin-inventory definitions also require both modes:
`timeout 900s make check RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$'`
and `timeout 900s make check REFERENCE=1 RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$'`.
The supported native registry is29 independently named definitions, not the
previous26; the PostgreSQL projection independently checks the new three0-valued
definitions and exact release short_desc, not whole-server catalog equality.

The root additionally executes the recorded standalone public-API Go feature
program with `timeout 60s go run ./.timeout-feature-smoke`, from an ephemeral
root-owned source directory which is removed after evidence capture, not committed.
Independent assertions include ordinary55P03/57014 same-owner recovery,
transaction25P04 and idle25P03/57P05 first FATAL. Compare the entire expected
native Error (both severities, code, English message, contextual Position and
every remaining optional field), nil failed Rows/results and exact affected
counts. The old handle must reject INSERT99 without effects; peer ordered INT4
rows retain7 and exclude private8/99, and an independent blocked-table query
must be empty. Peer reads also prove actual AX release. Replacements must have
different physical identities and all five timeout defaults exactly0. Pooled
idle replacement happens before new work; accepted timed work is never replayed.
The executable must exit0 only after every assertion and print
`timeout feature smoke passed`; a selected test pass does not replace this run.

The standalone smoke expects lock ERROR55P03 Position15 and statement ERROR57014
with absent Position, then transaction FATAL25P04 Position13, idle-in-transaction
FATAL25P03 and idle-session FATAL57P05 with absent Position. Setup, lock, setting,
reset and end commands affect0; initial/private/healthy inserts affect1.
Committed7 survives every death; healthy final INSERT9 affects1 and the peer
must observe exactly ordered INT4 rows7,9/cardinality2, with the blocked table
still empty and no99/private8/blocked8 replay.
Commands are verification instructions, not claims of exercised outcomes.

### Settings smoke migration

The superseded `TestSettingsSmoke` is removed; its typed registry, numeric,
metadata, notice/reentry, parser capture, access and connection-lifecycle
obligations remain in the existing shared `TestReferenceSettings*` consumers.

### Cursor motion selected proof

SCROLL and shared FETCH/MOVE positioning have independent, pinned PostgreSQL
18.0 expectations. The oracle-first discovery round establishes complete ordered
sequence traces for late/derived/view projection, ORDER caches, and volatile
WHERE/VALUES rewind; native results must never supply expected values.
All selected groups below require both `make check RUN='SELECTOR'` and
`make check REFERENCE=1 RUN='SELECTOR'`, with an external 900-second deadline.
The same completed definitions run unchanged in both modes; no discovery filter
remains. Native precancel continuation is an explicit native-only public-control
supplement because pq may asynchronously retire its physical socket.

| Group | Exact selector |
|---|---|
| Directions/boundaries/metadata | `^TestReferenceSQLCursor(ScrollDirections|ScrollBoundaries|ScrollMetadataAndOccurrences)$` |
| Counts/projection/smoke | `^TestReferenceSQLCursor(MoveCountsAndPreparation|MotionProjectionEffects|MoveFeatureSmoke)$` |
| Grammar/no-scroll/analysis | `^TestReferenceSQLCursor(NoScrollMotionExceptions|MotionSyntaxAndDiagnostics|ZeroAndDirections|AnalysisAndDiagnostics)$` |
| Lifetime/current-of | `^TestReferenceSQLCursor(ScrollLifecycleAndAuthority|ScrollCurrentOf)$` |
| Prepared preservation | `^TestReferenceSQLCursor(PreparedUtilities|PreparedMetadata|EmptyAndMetadata|RegtypeWarnings|FeatureSmoke)$` |
| Demand/output preservation | `^TestReferenceSQLCursor(OrderedBatches|DeclarationTiming|Snapshots|QueryConsumers|OutputCapture|Parameters)$` |
| Authority/locking preservation | `^TestReferenceSQLCursor(NestedDemand|ScopePhases|CorrelatedFailure|AdvisoryScope|LockingLatestRow|SecondFetchLockTimeout)$` |
| Lifecycle preservation | `^TestReferenceSQLCursor(Savepoints|TransactionEnds|CloseAndSession|Cancellation|NativeControls)$` |
| Positioned mutation preservation | `^TestReference(DeleteCurrentOf(PositionedRow|DuplicateIdentityAndLastFetch|EligibilityAndDiagnostics|RollbackAndRepeat|FeatureSmoke)|UpdateCurrentOf(PositionedRowAndCounts|IdentityAndPreparation|EligibilityAndDiagnostics|LifetimeAndRollback|FeatureSmoke))$` |

Selected system/frame identity smoke:
`make check RUN='^TestReferenceSQLCursorScrollSystemIdentity$'` and
`make check REFERENCE=1 RUN='^TestReferenceSQLCursorScrollSystemIdentity$'`.
The supported contract is generated-query frame identity, not a new physical
`ctid` provider. Integrated virtual `n GENERATED ALWAYS AS(i+1)` and compound
`n+1` projection have independently predetermined values `3,4,5` for `i=1,2,3`;
native cursor output never supplies those expectations. The first case composes
the existing LEFT JOIN with generated right rows present only for `i=1,2`:
compound outputs are `3,4,NULL`. NEXT, ABSOLUTE2, then PRIOR selects present
`i=1` while a stale next-demand frame would identify absent `i=3`. This is an
independent non-EOF identity assertion, not a claimed observed native mismatch.
The all-present `3,4,5` case follows. Direct/prepared traces cover reverse,
absolute/relative, exact-last and after-EOF motion with meaningful metadata.
Completed definitions run unfiltered in both modes. A native external-deadline
expiry is failure evidence, not proof of a particular value mismatch or panic.

Preserve upstream public identity coverage in both modes with
`RUN='^TestReferenceGeneratedVirtual(BoundedProjection|Frames|WindowIdentity|TableOID|Composition)$'`
and private native coverage with
`make check RUN='^TestGeneratedVirtual(ScheduledProjectionIdentity|RetainedViewProjectionIdentity|WindowInputIdentityAdapter)$'`.

Selected-ordinal projection binds its generated frame to the same physical
index as the selected row and cached order values, independently of the
sequential next-demand frontier. Preserve both virtual-read and motion-retention
flags. Private proof:
`make check RUN='^TestGeneratedVirtualSelectedOrdinalIdentity$'` and
`make check RUN='^TestSQLCursorScrollVirtualFrameIdentity$'`.
The first uses real occurrence provenance and a permutation to check present
and absent selections with a divergent frontier, including a frontier at EOF;
it also asserts no recipe demand for a missing occurrence, no input mutation
and no sequential-position change. The second exercises the actual retained SQL
cursor through reverse/absolute/relative/EOF and CLOSE without public adapter
socket teardown. Neither suppresses a panic.

Integrated HOLD shared-seam preservation requires these additional small groups
in both native and real pinned modes, with the same 900-second external deadlines:

* H1: `^TestReferenceSQLCursorHold(CommitRemaining|AutocommitAndPrepared|RollbackAndSavepoints|Materialization|FeatureSmoke)$`
* H2: `^TestReferenceSQLCursorHold(FailureAndPromotion|ScopeAndPresentation|CatalogChanges|RecursiveCatalogChanges|Restrictions|NestedRowLocks|PrepareBoundaries)$`
* H3: `^TestReferenceSQLCursorHold(Cancellation|SessionAndControls)$`
* H4: `^TestReference(SQLCursorZeroAndDirections|SQLCursorAnalysisAndDiagnostics|SQLCursorTransactionEnds|PreparedTransactionUnsupportedSessionBoundaries)$`
* Additional positioned preservation: `^TestReferenceDeleteCurrentOfUsingReturningAndConstraints$`

Combined SCROLL/HOLD capture and saved-position witnesses are
`^TestReferenceSQLCursorScrollHold(CommitPosition|CapturedValues)$`.
Their independent release-source expectations require an oracle-first checkpoint
before production composition, followed by the same complete definitions in both
modes without discovery gates. No prior cursor motion golden is replaced by these
interaction witnesses.
The combined witnesses assert MOVE's empty schema and affected counts separately,
complete EOF descriptors and closed MOVE34000, captured RELATIVE1 values, and
sequence stability at every stored operation including before-first/after-last.
Computed NEXTVAL metadata uses the documented unknown-nullability contract, not
the catalog-column helper assumption or a native-derived value/type golden.
Supplementary witnesses require pinned oracle proof before native/final proof.

Stored-view partial-capture witnesses use
`^TestReferenceSQLCursor(StoredViewFrontier|ScrollHoldStoredViewFrontier)$`
in both modes. Their stored definition is `SELECT i FROM cursor_rows ORDER BY i`,
with no outer sort: partial MOVE then rewind must drain exactly `[1,2,3]`.
The SCROLL HOLD variant commits after partial MOVE, preserves the saved position,
and checks all three stored occurrences, reverse/absolute/relative seeks and
MOVE counts. Its late NEXTVAL target draws 10 before commit and 11..13 during
full capture; stored motion preserves those values and sequence state.
The private `TestSQLCursorStoredViewFrontier` additionally inspects the retained
child's next-uncaptured position, captured prefix, full-drain cardinality and
producer release. A position-only rewind preserves that frontier; only actual
child invalidation clears the view's values, selection, completion and lineage.
Selected private command: `make check RUN='^TestSQLCursorStoredViewFrontier$'`.

Private parser/engine supplement: `make check RUN='^TestSQLCursor'`.
Shared public smoke: `make check RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`
and the identical command with `REFERENCE=1`. It observes SCROLL EOF/reverse,
absolute/relative traversal, quiet MOVE counts, held prepared movement, detached
rows and closed-name 34000. Whole-project Docker-free verification remains
`make quickcheck`, after edits finish; final integration verification remains the
root's `make finalcheck`, not a replacement for selected dual-mode evidence.

### Session cursor metadata selected proof

The untagged `TestReferencePGCursors` definitions use the shared public fixture
and independently specified metadata, rows, counts and full diagnostics. Select
small groups from the repository root, each with an external900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGCursors(Metadata|SourcesAndTime)$'
make check REFERENCE=1 RUN='^TestReferencePGCursors(OwnerAndMotion|Lifecycle)$'
make check REFERENCE=1 RUN='^TestReferencePGCursors(SessionAndControls|Diagnostics|FeatureSmoke)$'
```

Repeat these same selectors without `REFERENCE=1` for native proof. Every changed
definition requires both modes. Filter assertions to scenario-owned names:
PostgreSQL's observer/protocol portals are not native SQL DECLARE cursors.
Omitted SCROLL's plan-dependent PostgreSQL behavior and native forward-only
limitation are asserted separately; explicit SCROLL/NO SCROLL shares the contract.

```sh
make check RUN='^TestSQLCursor(DeclarationSource|MetadataInstall)$'
make check RUN='^TestPGCursors(Source|Catalog)$'
make check RACE=1 RUN='^TestReferencePGCursors(OwnerAndMotion|SessionAndControls|FeatureSmoke)$'
make check RUN='^TestReferencePGCursorsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCursorsFeatureSmoke$'
```

The smoke observes exact full declaration text/options/nonzero bracketed timestamp
only in the owning physical session. FETCH returns row1, MOVE skips row2, and
NEXT returns row3 while metadata remains unchanged. CLOSE removes the row; HOLD
creating COMMIT preserves its tuple and saved position; owner DISCARD ALL clears
metadata without clearing the independently held peer cursor.
`TestReferencePGCursorsOwnerAndMotion/role_and_definer` keeps independent same-name
HOLD cursors on two physical pins outside explicit transactions. Their complete
direct and stored-view metadata tuples/instants remain unchanged as the sessions
swap effective actor/definer roles and RESET ROLE. The ordinary view is explicitly
owned by the definer, with SELECT granted to the actor; the peer never sees the
owner-only cursor. Role/view setup precedes cursor installation, and cleanup resets
roles/closes cursors before fixture schema teardown and role removal.
The view is fully qualified. PostgreSQL alone receives explicit public-schema
USAGE grants to restore visibility removed by fixture schema recreation; native
public already grants USAGE, so this does not claim native schema-GRANT support.
Actual role assertions use the NAME-typed current_user result without a cast.
Native-only phases check detached metadata, ordinary precancellation and Dump
exclusion. After Reset/Restore, fresh metadata retains the tuple/instant while
actual FETCH remains fenced with ErrCatalogReplaced. Pool return is not physical
cleanup.
Shared catalog SQL checks pg_class view kind and six attributes using the builtin
pg_catalog namespace OID11. Native pg_namespace, pg_type and pg_class.reltype
remain unavailable; no adjacent catalog feature is required. Native row/array
ownership and reciprocal element associations are checked through internal
catalog APIs; an additional PostgreSQL-only catalog phase independently checks
the real row/array relationships.
Private units supplement public routes with controlled declaration/transaction/
first-demand clocks, producer teardown, owned descriptor validation, builtin
timestamp descriptor reownership without datum/registry mutation, and canceled
materialization with no partial publication. Parser generation and whole-project
`make quickcheck` remain root-owned after edits; selected proof is still required.

