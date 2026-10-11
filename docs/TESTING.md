# Adding and maintaining tests

## Prepared rebind selected proof

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

## Multirange selected proof

Permanent untagged `reference/types/multirange_*_test.go` scenarios cover all
three concrete companions through shared direct/prepared SQL, raw transports,
metadata, full diagnostics, ACLs and nested domains/column NOT NULL.
Oracle discovery filters are removed before native-red; they are not a final
capability distinction. Native process-local control subphases additionally
execute the actual emitted SQL on pinned PostgreSQL18.0.
Pinned lib/pq exposes blank multirange type names (not uppercase range names);
the helper retains logical family identity for raw/metadata checks, and public
pg_type/pg_range witnesses independently verify released OIDs/companion links.
Scale-boundary unions follow released strict selection rather than a containment
shortcut; fully comparator-equal different-scale duplicates have no stable tie
ordering promise.
The permanent `TestReferenceMultirangeComparisonBoundary` uses all three
companions and plain/nested domains with unequal ascending/reverse, equal and
NULL operands through direct/prepared SELECT. PostgreSQL's six supported scalar
comparison result matrices are independently specified; native must reject with
full42883/logical operand names/Hint/Position, at direct execution or Prepare.
No discovery filter survives native-red or final selected evidence.

Run each bounded group in both modes, pinned first for new expectations, under
an external900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check REFERENCE=1 RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestMultirange'
make check RUN='^TestComparison'
make check RUN='^TestAnyValueProviderDatums$'
make check RACE=1 RUN='^TestReferenceMultirange(Binds|Controls|DomainControls|ComparisonBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceIntegerRange'
make check RUN='^TestReferenceIntegerRange'
make check REFERENCE=1 RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRange'
make check RUN='^Test(IntegerRange|NumericRange)'
```

The public standalone source `local://PG18-TYPE-MULTIRANGE-smoke.go` is an OMP
session artifact, not a committed executable. Root materializes
`.multirange-feature-smoke/main.go`, runs
`timeout 60s go run ./.multirange-feature-smoke`, records argv/cwd/exit and exact
observations, then removes the temporary materialization. It asserts three-family
canonical unions, gaps/NULL/empty/universal, binds/raw metadata/diagnostics/
atomicity, real same/fresh Driver controls and nested-domain constrained replay
with held-reader shape retention and direct/prepared scalar-comparison admission
rejection for builtin/plain/nested-domain operands. Success is quiet exit0, not a printed label or
test-shelling. Root runs whole-project Docker-free quickcheck after edits and
post-integration finalcheck; those do not replace selected dual-mode evidence.

## COPY text selected proof

Shared untagged `reference/dml/copy_text_*_test.go` definitions run on the native
public pinned-connection API and a genuine pgx stdlib5.9.2 stream transport using
the fixture's existing pinned PostgreSQL18.0 container/DSN. No SQL success
substitute, mocked stream, expectation derived from native output or permanent
PG-only discovery filter is retained. The ancillary pgx pool does not replace
lib/pq for existing reference definitions.

Selected commands (root-run, external900s deadlines):

```sh
make check RUN='^TestReferenceCopyText'
make check REFERENCE=1 RUN='^TestReferenceCopyText'
make check RUN='^TestCopy(Text|Parser)'
make check RUN='^(TestInsertSelect(RequiredClose|CancellationOnRequiredClose|EagerFailureAndCancellation)|TestInsertConflictProducerConsumedCountAndClose|TestQueryLimit(RequiredClose|EOFCloses))$'
make check RACE=1 RUN='^Test(CopyText(OwnerReservation|OwnerWaitCompletion|Replacement|OutputReplacement|Termination|Timeout|PanicCleanup|CopiedReservation|FinalCancellationVeto)|ReferenceCopyText(AdmissionAndLocks|Cancellation|InputStatementTimeout))$'
make check RUN='^TestReferencePredefined(UnavailableCapabilities|BoundaryFeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(UnavailableCapabilities|BoundaryFeatureSmoke)$'
make check RUN='^TestReferenceCopyText(OptionsAdmissionPrecedence|QuotedFormat|InputStatementTimeout)$'
make check REFERENCE=1 RUN='^TestReferenceCopyText(OptionsAdmissionPrecedence|QuotedFormat|InputStatementTimeout)$'
```

The feature definition `^TestReferenceCopyTextFeatureSmoke$` has selected native
and pinned counterparts. Standalone source `local://copy-text-smoke.go` is an
ephemeral public Go program: root materializes `.copy-text-smoke/main.go` without
staging it, runs `go run ./.copy-text-smoke` from this checkout with an
external900s deadline, and removes the temporary directory. It checks exact
bytes/counts/tags, typed rows, failed-import
atomicity and native Dump/Reset/Restore; successful stdout is exactly `COPY 3`,
`COPY 3`, `COPY text smoke passed`, each on its own line, after every assertion.

Private units separately prove reservation ownership, context-cancelled owner
entrants, replacement fencing, termination, panic cleanup and final cancellation.
Blocked stream assertions release and join their caller goroutines before
connection cleanup. Native replacement and caller-error identity assertions remain
explicitly native-owned, not presented as PostgreSQL control/error identities.

Codec raw transport asserts all seventeen real SELECT fields and metadata without
requiring unrelated TEXT casts. pgx DATE is a `time.Time`, native DATE is a string;
these shapes are asserted before civil-value normalization. BYTEA bytes and native/
pgx timestamps stay actual typed values. An unregistered pgx enum metadata tag is
the decimal server OID, checked against an independent pg_type identity query.
pgx stdlib's binary TIMESTAMPTZ scan preserves time.Unix's host-local location
when ScanLocation is nil. The witness asserts the exact instant and that actual
local location/offset before UTC normalization; it does not confuse a Go transport
location with server TimeZone, whose actual COPY output is independently checked.
Schema USAGE setup is PostgreSQL-only because native PUBLIC already has USAGE;
the table/column authorization body remains shared.

The Cancellation scenario's PostgreSQL transaction-timeout phase verifies100ms
configuration and observes peer server clock_timestamp reaching the captured PID's
xact_start+100ms deadline under a bounded context (or actual backend disappearance),
then releases and joins the blocked reader. Pending COPY interrupt processing can
require that drain before final teardown; no client sleep guesses expiry.
Native expiry uses the actual private Operation signal in TestCopyTextTimeout;
there is no invented public native timer channel or falsely asserted adapter parity.

InputStatementTimeout performs a real public COPY baseline/count/tag/typed-row
assertion on both backends, then the genuine PostgreSQL expiry phase independently
verifies100ms with SHOW, observes its PID's active COPY/ClientRead, and waits for
server clock_timestamp to reach that command's query_start+100ms deadline under a
bounded context before releasing/joining input. Its complete57014 diagnostic has
`COPY copy_input_timeout, line 2` Context and preserves the typed sentinel.
The private native TestCopyTextTimeout observes its actual Operation.Context
expiry at the same admitted-input stage and requires `COPY t, line 2`, separately
from relation-opening timeouts with empty Context. No public native timer channel,
client sleep or mock oracle is substituted for this adapter capability distinction.

OptionsAdmissionPrecedence independently checks missing relation, missing column
and denied INSERT/SELECT before bad table options, but bad query options before
query-source analysis. QuotedFormat checks preserved uppercase/mixed STRING case,
full22023 fields/positions, no native reader/writer invocation and actual lowercase
quoted/unquoted lexer-folded transfers; its error expectations come from the
pinned oracle rather than a native-generated golden.

For PostgreSQL admitted cancellation, the peer first observes the captured backend
executing COPY in ClientRead and obtains pg_cancel_backend's true acknowledgement,
then releases/joins the reader so COPY can drain its protocol. It does not wait
for idle while blocking that drain. The complete57014 user-request diagnostic
and unchanged sentinel prove server-side cancellation atomicity. Merely canceling
a client context while simultaneously releasing EOF races successful autocommit
and cannot portably establish this oracle outcome. Native caller-context
cancellation still independently requires ordinary identity, zero result and no rows.
COPY table-target lock timeout has no Position; query-source SELECT positioning
keeps its existing authority instead of sharing a fabricated COPY target position.

## Registered extension selected proof

The untagged shared `reference/catalogs/extension_*_test.go` scenarios independently
assert direct/prepared TEXT/NULL/rules, namespace and trusted ownership, exact
full diagnostics/notices, atomic failures, transaction/savepoint/publication
behavior and native control round trips. Controls additionally import named
extension/member owners into an independent Driver with different role attributes,
check strict metadata and target-principal fences, retained Rows/metadata,
committed-only Dump and deterministic redump. Native ownership comments are not
claimed as PostgreSQL SQL owner-restoration commands.

The disclosed PostgreSQL-only OracleSetup uses real loose FUNCTION/template/
dictionary and schema-GRANT setup unavailable natively; genuine package units
stage corresponding member descriptors and actual ACL consumers. Concurrent
oracle synchronization uses pg_stat_activity solely to observe the genuine
unique-index wait; native private transactions exercise the same complete-or-none
publication outcome at commit. No sleep or worker-order assumption identifies
the loser. Added helper Context expectations are source-grounded independently
and require confirmation in the selected pinned run.

Permanent TEMP definitions cover the independently observed own-template helper
failure, authorized foreign installation, exact namespace edge, transactional
DISCARD/savepoint recovery, bounded physical owner close and native portability/
retention. Peer assertions during tentative DISCARD deliberately read committed
catalog identities only: executable consumers can wait on the owner's real locks.
After rollback, both owner and authorized peer execute the restored provider/views.
Physical close means lease **and** pool close, not lease return; all close workers
join within5s. PostgreSQL's advisory exit barrier observes cleanup completion before
catalog assertions, without polling or assuming asynchronous socket close is exit.
Mixed collations, private maintenance, late-root/row/index publication, exhausted
metadata revisions, retained old Rows, source-token rejection, imported OID/name
collisions and named retained owner DROP dependencies have genuine engine units.
TemporaryReplacement also captures a genuine TEMP view procedure manifest and
TEMP DEFAULT/CHECK consumer before replacement, executes them afterward, and
rejects same-address procedure substitution, foreign physical tokens and ordinary
stale ObjectRefs. Schema retirement uses successful same-name CREATE SCHEMA rather
than adding an unrelated native pg_namespace projection.
SchemaDependencies independently specifies normal extension-to-schema RESTRICT
diagnostics and CASCADE notices/consumer closure. Confirm these independently
specified records against the pinned oracle before accepting native behavior.

The integrated `pg_extension_*` definitions use the same single live
descriptor-derived catalog source as extension installation, not a second empty
provider. Fresh native catalogs are genuinely empty; installed descriptors supply
actual identity/owner/schema/version rows. Engine witnesses preserve strict
column shape/type-generation ownership, cancellation and retained capture
semantics, and additionally compare an installed row with its published descriptor.
Missing-provider CREATE prepares successfully and fails at execution with
`0A000` on both backends. PostgreSQL's system-installation Hint is pinned only on
PostgreSQL; native exposes its truthful registered-only availability boundary.
Unchanged ALTER/DROP/LOAD command boundaries remain separately asserted.

Review2 tests-first checkpoint pins eighteen member-catalog attribute nullability
records as named bags, not physical attnum order. PostgreSQL's pq Nullable
capability remains unknown; native empty and populated direct/prepared queries
require known NOT NULL for all exposed procedure/template fields and dictionary
first five fields, with nullable dictinitoption. The actual standalone smoke
asserts those same native metadata contracts.

TemporaryCleanup's actual-session-end preparation_stale and
postdrain_late_consumer subcases delegate genuine outside-lock relation-provider
validation, publish an independent late procedure-dependent view and durable
row, and require internal fresh retirement. Ordinary cleanup validates before
index preparation; validated candidates are not mutated by later index metadata
evolution. Failed attempts unwind their scope/operation/stage before recapture,
and unchanged structural errors escape without retry. Mixed TEMP collation plus
saturated DEFAULT/CHECK metadata uses terminal-only fresh-descriptor semantics;
actual ordinary/postdrain malformed-survivor units require prompt visible error
and intact committed state. No user DDL overflow policy is relaxed.

Run each command with an external900s deadline from the part worktree:

```sh
make check RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RUN='^TestExtension(ProviderIdentity|Rules|ExpressionIdentity|ProviderConflicts|SchemaACL|ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
make check RUN='^TestTemporaryTable(CleanupClosure|CleanupPublication|TerminalRevisionPreparation|TerminalMaximumPublication|CascadeRevisionBounds)$'
make check RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check RUN='^TestPGExtension(ShapeAndOwnership|Capture|CaptureInstalled)$'
make check RUN='^TestReferenceExtensionMemberNullability$'
make check REFERENCE=1 RUN='^TestReferenceExtensionMemberNullability$'
make check RUN='^TestCollation(PrivateSessionIdentity|TemporaryCleanupAX|TemporaryCleanupCompletenessFence|TemporaryCleanupExhaustion)$'
make check RACE=1 RUN='^TestReferenceExtension(Transactions|Controls|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RACE=1 RUN='^TestExtension(ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
```

Select top-level definitions, not a backend slash filter. The same completed
reference definitions require both native and real pinned modes before review.
Root runs whole-project Docker-free quickcheck after synchronized edits, not
mid-flight. Standalone feature smoke is the complete session artifact
`local://ext-registry-smoke.go`, materialized outside the checkout and run with
`timeout 900s go run \"$SESSION_TMP/ext-registry-smoke.go\"` from the worktree;
quiet exit0 requires actual installation, TEXT/NULL, exact repeat/notice,
member relationships, rollback isolation, trusted privileges and control/import
ownership witnesses. It additionally performs actual own-TEMP42883 atomicity,
foreign installation and owner-vs-peer lookup, zero-output Dump failure, tentative
catalog-only DISCARD visibility, executable rollback recovery, committed full
consumer closure/surviving rows, fresh reinstall, bounded physical retirement
with exit barrier, and persistent schema RESTRICT/CASCADE closure. A test named
FeatureSmoke does not replace that program. Post-merge finalcheck uses an external
1800s deadline; targets themselves introduce no hidden deadlines.

## Expression collation selected proof

The shared untagged definitions in `reference/patterns_encoding/expr_collation_*`
cover independently expected rows/types/routes and complete diagnostics.
`TestReferenceCollationNativeCollision` additionally preserves the genuine TEMP
allocator collision, exact dependency totals, source immutability, repeated
Restore and real PostgreSQL emitted-SQL replay with stored comparisons.
Expected replay errors use recovered PostgreSQL savepoints, not aborted-transaction
assertion bypasses. Run every changed definition in both modes (external900s):

```sh
make check RUN='^TestReferenceExprCollation(Resolution|Demand|Consumers|Keys|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(Resolution|Demand|Consumers|Keys|FeatureSmoke)$'
make check RUN='^TestReferenceCollation(Identity|Provider|NativeCollision)$'
make check REFERENCE=1 RUN='^TestReferenceCollation(Identity|Provider|NativeCollision)$'
make check RUN='^TestExprCollation(ComparisonAuthority|RetagAuthority|KeyAuthority|AnnotationAST)$'
make check RACE=1 RUN='^TestExprCollation(ComparisonAuthority|RetagAuthority|KeyAuthority|AnnotationAST)$'
make check RUN='^TestReferenceExprCollation(RowOperandDemand|ScheduledPayloadDemand)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(RowOperandDemand|ScheduledPayloadDemand)$'
make check RUN='^TestReferenceGeneratedStoredTableOIDControls$'
make check REFERENCE=1 RUN='^TestReferenceGeneratedStoredTableOIDControls$'
make check RUN='^TestExprCollation(RowOperandDemand|ScheduledPayloadDemand|GeneratedTableOIDReachability)$'
make check RACE=1 RUN='^TestExprCollation(RowOperandDemand|ScheduledPayloadDemand|GeneratedTableOIDReachability)$'
make check RUN='^TestReferenceExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RUN='^TestExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RACE=1 RUN='^TestExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RUN='^TestReferenceExprCollationHash(Demand|Invocation|CTEInvocation|SourceOccurrence)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollationHash(Demand|Invocation|CTEInvocation|SourceOccurrence)$'
make check RUN='^TestExprCollationHashExecutionOwner$'
make check RACE=1 RUN='^TestExprCollationHashExecutionOwner$'
```

Private invariants assert actual retained capabilities, forged-ref admission,
cancellation, equality/hash consistency including BPCHAR domains, hidden BOOL
authority remapping/source immutability and full-authority optimizer eligibility.
RetagAuthority asserts direct stored projection demand skips an unused conflict
without changing source graph, selected provider or schema; full/star demand
retains the original graph and invalid slots reject. RowOperandDemand checks
actual pair providers and side-effect order; ScheduledPayloadDemand exercises
actual sort/evaluation cache producers rather than final cached readers.
GeneratedTableOIDReachability binds genuine first-only/later-only/no-owner row
generation graphs. Shared frontier definitions independently check decisive,
NULL and reached left/right demand, unused sorted/bounded payload, demanded
keys, retained volatile effects, full/star and materialized refresh.
The original generated tableoid controls fixture also binds first/later owner
row predicates to the actual retained TEMP OID. Its replay candidate compares
TRUE before owner remapping and must recompute FALSE afterward; scalar repair,
source-held rows, TEMP identity/state, DML and actual PostgreSQL replay remain.
KeyAuthority imports genuine declared domain values through existing Input,
retaining domain identity and the BPCHAR trailing-space normalization witnesses.
StoredPreflightDemand exercises the admitted ordinary legality route before
stored-source opening, including an unused CASE-to-TEXT payload and demanded
full diagnostics. NestedPayloadDemand checks actual nested projections for
separate same-OID aliases in both shared and distinct lexical frames, while
preserving full frozen ACL/dependency reads. DomainDerivation independently
distinguishes top annotations from nested concat/CASE explicit authority,
nondefault implicit reset and default-domain bubbling; private consumers demand
the actual bound provider and retain child dependencies/properties. RetagAuthority
also checks mapped provider authority, explicit diagnostic location,
consumer execution and source-immutable rendering, not incidental wrapper shape.
DomainDerivation additionally requires exact stored default/analyzed/planned
generation destination Usage while retaining semantic rendering and row demand.
Existing TestStringEncodingRetainedFormalOwnership keeps its genuine mapped
formal descriptor ownership and evaluation assertions. Select it together with
TestCollationStoredExpressionMetadataRetention and TestCollationRetainedProviderProvenance
for native storage/formal-role remapping proof.
HashDemand checks RHS-first empty/NULL/negated/effect/error frontiers.
HashInvocation and HashCTEInvocation check volatile RHS reuse across probes and
plain/OFFSET 0 correlated-parent rescans, versus actual correlated RHS freshness
and new held Execute/bind freshness. HashSourceOccurrence checks same-frozen-view
aliases independently without pinning build order. HashExecutionOwner requires
one counted build, non-consuming NULL evidence, failed/canceled nonpublication,
actual captured alias keys, source immutability and true-owner memory release;
borrowed stage closure must not release the ancestor's table.
Select affected existing consumer definitions separately in both modes; these
small checks do not replace whole-project Docker-free `make quickcheck`.

Public test smoke:
`make check RUN='^TestReferenceExprCollationFeatureSmoke$'` and the same command
with `REFERENCE=1`. Standalone smoke source is supplied in the OMP session file
`local://expr-collation-smoke.go`; materialize it temporarily as
`.expr-collation-feature-smoke/main.go` and run
`timeout 900s go run ./.expr-collation-feature-smoke` from the worktree.
It uses only public Driver/database/sql/Error/Dump/Restore, independently checks
C ordering/bytewise equality, full demand/conflict/noncollatable diagnostics,
types and stored comparison identity/dependencies after Restore. It also checks
nested resolved output and unused CASE-coercion success, then demands their
actual42P22 errors through direct/prepared execution before and after Restore.
Success is quiet exit0; failure reports the actual mismatch. Remove only that temporary
smoke directory afterward; session communication is not a parent deliverable.

## DISCARD selected proof

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

## Direct-message selected proof

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
and57014; native uses its held-conflict bounded caller deadline. Index obligations
are supported source indexes, not new CREATE INDEX ON materialized-view support.
Concurrent, genuine explicit-block and Restore refresh exclusions remain intact.
Supplemental TestDriverBatchMaterializedReplacementRoots and
TestDriverBatchMaterializedPublicationGuards inspect first committed versus
immediate private predecessor, generation/end/root/index/role/TxRef/epoch/cancel
fences and row births at true end. TestDriverBatchMaterializedPromotionSavepoint
pins the retained replacement across genuine promotion, failure and mark recovery.
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

## Mandatory policy

Agents MUST read and follow this guide before adding, modifying or migrating tests.
This is the authoritative test-placement and implementation workflow.

- All tests exercising public APIs by issuing SQL and checking responses MUST live
  in `reference/` as **dual-mode reference tests**. This includes direct/prepared
  execution, rows, metadata, diagnostics, cancellation and SQL witnesses around
  native controls. Implementing these tests in root, `smoke/`, `internal/` or any
  other directory is forbidden.
- When working in an area containing offending tests, immediately migrate them
  to dual-mode tests in `reference/`. Preserve the complete behavioral assertion
  set and public execution routes, merge with existing reference coverage where
  appropriate, and remove the superseded tests. Do not leave native-only copies.
  A migration subagent MAY handle this independent slice to keep the current
  implementation context focused; give it the original assertions and this guide.
  The approved smoke migration retains only `smoke/catalog_test.go`:
  `TestCatalogOwnersPublicWitness` tests the native Driver's opaque-DSN attachment
  and physical lifetime. SQL supplies incidental state observations, not a
  portable PostgreSQL scenario. Do not expand this named retention exception.
- Internal unit tests that exercise package functions rather than public SQL APIs
  may remain beside their package. They supplement, never replace, reference
  coverage for PostgreSQL functionality.
- Every new PostgreSQL functionality implemented in stuntgres MUST add or modify
  dual-mode reference coverage. Every discovered compatibility bug MUST have a
  failing reference regression before its fix.
- The actual SQL/API exercise and assertions MUST share one body across modes.
  Never maintain near-duplicate PostgreSQL and quick-mode implementations.
  Mode selection changes backend availability, not the scenario.
- Use only public stuntgres APIs in `reference/`; do not import internal packages
  or root-private test helpers. Name tests `TestReference<Feature><Behavior>` and
  use untagged `*_test.go` files so ordinary `make quickcheck` discovers them.
- Assert behavior, boundaries, errors, atomicity and lifecycle transitions, not
  source text, wiring counts, mock echoes or parity alone. Keep functional files
  at most 750 lines; shared fixture code is exempt. Keep output quiet by default.
  PostgreSQL internal DEBUG trace wording, allocator identities, record counts
  and loader order are implementation details, not SQL notice expectations.
  Keep independently specified feature notices and their complete diagnostics
  exact; distinguish DEBUG tracing from unexpected NOTICE/WARNING delivery.
- Organize whole test files in feature-group packages under `reference/`, with
  common importable helpers in `reference/fixture/`. Keep at most 30 packages
  total including the fixture, and at most 1,000,000 bytes per package counting
  all files. Do not split or duplicate test bodies to meet these limits.
- Every added, modified or migrated dual-mode reference test MUST be selectively
  run in both real pinned PostgreSQL mode and Docker-free quick mode, using the
  same completed test definitions and a `RUN` selector covering all affected tests.
  Repeat both selective runs after changes to those tests or their implementation.
  Finalcheck does not replace this pre-review requirement.
- Concurrency tests MUST identify a deadlock victim from its error outcome, not
  worker completion order: a ready channel select can choose either result.
  Victim cleanup can also wake a successful survivor before the victim reports
  its result. Join both workers under a bounded deadline and retain the exact
  victim, survivor and side-effect assertions.
  `TestUniqueClaimDeadlock` collects both outcomes under one five-second deadline,
  requires exactly one error, and assigns victim/survivor assertions by that error;
  its worker cleanup cancels and joins on assertion-failure paths as well.
- Catalog discovery fixtures MUST use scenario-specific object names when
  asserting a complete database-wide candidate bag. The shared PostgreSQL
  fixture resets `public`, not every nonpublic schema; unrelated same-name
  objects can legitimately remain outside the scenario's search path.
- The pinned fixture retains one idle administrative PostgreSQL session, with
  unrestricted open connections for concurrent observers. Serialized resets
  require all borrowers, Rows and workers to return before the boundary. Each
  before-case and after-case reset borrows that session under the existing
  15-second context, executes standalone `ROLLBACK`, then standalone `DISCARD ALL`,
  then unchanged `DROP SCHEMA public CASCADE; CREATE SCHEMA public` on that same
  connection. Returning it preserves authentication without retaining dirty
  authorization, role, settings, temporary, prepared or advisory state.
  Case pools and native engines remain isolated. Startup failure and final
  teardown close the administrative pool; no separate reset connection is owned.
  `TestReferenceFixtureResetIsolation` and
  `TestReferenceFixtureSessionAuthorizationReset` independently cover this boundary:

  ```sh
  make check RUN='^TestReferenceFixture'
  make check REFERENCE=1 RUN='^TestReferenceFixture'
  ```

  Schema-owner introspection through `pg_namespace` is PostgreSQL-only. Native
  reset isolation independently asserts bootstrap principals, fresh session
  settings, an empty public `Dump`, and same-name empty-table recreation. Its
  native-only public `Reset` phase removes actual inserted data/catalog state,
  preserves bootstrap principals, and again permits empty-table recreation;
  it does not add a native `pg_namespace` compatibility surface.

Session role switching uses the shared direct/prepared scenarios and complete
identity/diagnostic expectations in `reference/roles/role_switch_test.go`.
Capture the completed definitions against pinned PostgreSQL before implementing
a compatibility change, then run the same definitions natively to establish red.
Any temporary oracle-discovery backend filter must be removed before native red,
final dual-mode proof and review. The configured-reset supplement uses a genuine
PostgreSQL startup options connection and its own middleware recorder; native
configured defaults are internal-unit evidence, not a public connector capability.
Pre-canceled owner preservation is a native adapter guarantee, not a portable pq
guarantee: fast pq utilities may finish and retire the owner asynchronously.

Selected role proofs (external deadline900s each):

```sh
make check RUN='^TestReferenceRoleSwitch'
make check REFERENCE=1 RUN='^TestReferenceRoleSwitch'
make check RUN='^(TestReferenceRoleExcludedCommands|TestReferenceSessionIdentityFormsAndFreshness|TestReferenceSQLCursorScopePhases)$'
make check REFERENCE=1 RUN='^(TestReferenceRoleExcludedCommands|TestReferenceSessionIdentityFormsAndFreshness|TestReferenceSQLCursorScopePhases)$'
make check RUN='^(TestRoleSwitch|TestRoleSelectionBoundaries|TestRoleGrammar|TestRoleLocalSettingBoundary|TestSessionIdentity|TestSavepoints)'
make check RACE=1 RUN='^(TestRoleSwitch|TestSessionIdentity|TestSavepoints)'
make check RUN='^TestReferenceRoleSwitchFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRoleSwitchFeatureSmoke$'
```

The feature smoke additionally has a standalone public Go program, materialized
outside the checkout from the implementation session's `local://role-switch-smoke.go`.
Run `go run "$SESSION_TMP/role-switch-smoke.go"` from the checkout with an external
900-second deadline. It independently checks utility affected0, NAME metadata,
stable login identity, owner sentinel7, SESSION/LOCAL/savepoint/commit, NONE/reset,
outside-block warning25P01 and denied42501. Parameterless direct multi-command
execution follows [the direct-message contract](#direct-message-selected-proof);
prepared/parameterized multiple commands reject with42601, while unsupported
utilities retain their specific capability diagnostics. Malformed scripts still
parse completely before effects.

Enum support-function public witnesses use the shared direct/prepared definition:
`make check RUN='^TestReferenceEnumFunctions'` and
`make check REFERENCE=1 RUN='^TestReferenceEnumFunctions'`.
Standalone feature smoke uses `RUN='^TestReferenceEnumFunctionsFeatureSmoke$'`
in both modes. It checks typed NULL extrema, inclusive/NULL-bounded computed
arrays, metadata/raw transport, exact mismatch diagnostics and genuine native
stored-definition/eager-value controls. Private provider/retag units supplement
these witnesses with
`RUN='^(TestEnumSupportFunctions|TestEnumFunctionRetagAndStoredProviders|TestQueryTableCreateEnumArrayOutputBoundary|TestQueryTableCreateStructuralOutputWrappers)$'`.
The selected admission regressions use
`RUN='^TestReferenceEnumFunctions(StableAdmission|StorageBoundaries)$'` in
both modes. They independently assert STABLE generated-column/index-expression/
index-predicate rejection and the bounded native enum-array storage exclusion.
PostgreSQL storage succeeds, including query-derived and explicit enum arrays;
its successful stored values and NO DATA behavior are asserted separately from
native capability diagnostics. Computed ranges, ordinary views, projected
domain-over-enum arrays and non-enum array storage remain positive controls.
Derived native storage and explicit annotation/domain exclusions exercise direct
and prepared routes with full diagnostics and no partial publication. The
unpopulated PostgreSQL materialized view uses the shared full55000 diagnostic
and REFRESH hint, then refreshes successfully. Positive view metadata retains
declared native nullability rather than computed-expression nullability.

The affected visibility utility-boundary scenarios now assert supported MV
creation/count/stored rows and cleanup in both backends, instead of obsolete
native syntax rejection. Validate those changed contracts in both modes:
`make check RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
and
`make check REFERENCE=1 RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
(900 seconds each).

Materialized-view coverage uses the same public dual-mode scenarios:

```sh
make check RUN='^TestReferenceMaterializedView'
make check REFERENCE=1 RUN='^TestReferenceMaterializedView'
make check RUN='^TestReferenceMaterializedViewFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedViewFeatureSmoke$'
make check RUN='^TestMaterializedView'
```

Each selected check has a 900-second deadline. Independently specified bags retain
duplicate/NULL rows after base changes, distinguish empty/populated/no-data
states, and assert complete55000/42809/42501/25006 diagnostics, real cursor
metadata, dependency DROP, source/drop lock timeouts and atomic failure.
RestrictedSearchPath uses supported CURRENT_SCHEMA: population sees pg_catalog
while binding the original public source; SHOW and subsequent CURRENT_SCHEMA
still see public. Changed goldens require pinned oracle evidence, not native output.
Native controls assert exact stale rows after two cycles/fresh Driver/second pool,
including RestoreTemporary collisions. PostgreSQL replay of the actual emitted
script asserts definitions and unpopulated55000, never stale-row parity.
Native precanceled CREATE guarantees no publication; lib/pq may complete a fast
CREATE despite an already-canceled context. Shared lock-timeout cases retain
portable atomicity assertions. Private units cover real descriptor/staging,
frozen identities, strict codec/import and opt-in lexical comment boundaries.
Review-remedy regressions use
`RUN='^TestReferenceMaterializedView(KeywordTypes|SnapshotTypmods)$'` with both
`make check` and
`make check REFERENCE=1` (900 seconds each).
They preserve unquoted `materialized` type/schema legality and reject native
lossy snapshot coercions atomically beside genuine-Dump positives. NUMERIC
assertions use exact decimal driver transport, not numeric-to-text SQL casts.
KeywordTypes independently asserts native DROP TYPE's existing0A000 utility
exclusion; fixture reset and supported DROP SCHEMA CASCADE clean up native enum
types. Declaration, unquoted type references and cast legality remain shared.
Recursive/specialized snapshot scenarios use
`RUN='^TestReferenceMaterializedView(RecursiveSnapshots|TemporalSnapshots)$'`
with both `make check` and
`make check REFERENCE=1` (900 seconds each).
Independently expected raw domain/enum/constrained-array/composite values and
metadata precede native genuine-Dump restoration, reminted identities, retained
MV cursors and atomic malformed recursive payload rejection. Temporal coverage
includes both infinities and converted TIMETZ int32-displacement endpoints; the
endpoint's TEXT representation is intentional because pq cannot construct its
raw invalid-Location time.Time. Normal SQL expectations are shared; no PostgreSQL
Restore API or native stored-row parity from PostgreSQL script replay is invented.

Nonconcurrent refresh uses the shared `TestReferenceMaterializedRefresh` family:

```sh
make check RUN='^TestReferenceMaterializedRefresh'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefresh'
make check RUN='^TestMaterializedRefresh'
make check RACE=1 RUN='^TestMaterializedRefresh|^TestReferenceMaterializedRefresh(Locks|Cancellation|QueuedReplacement|Snapshots|Controls)$'
make check RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMaterializedRefreshQueuedReplacement/(data_data|no_data_data)/^postgres$'
```

Each check has an external900-second deadline. Independent bags retain
duplicate/NULL rows, default/DATA refresh returns affected0, and NO DATA is
unpopulated rather than populated-empty. Full producer/kind/ACL/read-only/lock
diagnostics, actual protocol Prepare/held statements, catalog identities and
typmod metadata are checked before backend comparison.
Public rowtype identity is observed through the existing REGTYPE registry;
`pg_class.reltype` and reciprocal `pg_type.typrelid` links are PG-only
complements. Native publication units independently pin the exact rowtype link.
Owner-context sequence witnesses use existing ALTER SEQUENCE OWNER transitions,
not excluded direct sequence grants. The caller's ownership cannot rescue a
denied MV owner; source/sequence owner context and caller settings/SESSION_USER
are checked independently.
Native explicit utility0A000/aborted25P02, eager retained Rows, original context
cancellation, genuine Dump/Reset/Restore and fresh-driver stale rows remain
explicit native phases. PostgreSQL independently asserts transactional
refresh/rollback/commit, active-portal55006 and actual backend-cancel57014.
Native active cancellation observes the actual public worker's target-request
wait through runtime goroutine profiles, following the existing active-provider
witness pattern. Package units additionally pin the lock-manager pending
request and queued refresh-refresh handoff. No SQL LOCK-on-MV capability,
elapsed-time admission assumption or merely-started signal is introduced.
QueuedReplacement starts an actual direct first worker behind a retained reader,
observes its target AX request, then starts an actual protocol-prepared second
worker and observes its later AX request. PG asserts the second worker's first
worker soft blocker; native profiles identify each distinct worker's real target
wait. DATA→DATA and NO DATA→DATA both join with affected0, the exact duplicate/NULL
four-row bag, populatedtrue and stable relation/REGTYPE/rewrite identities,
owner and definition. Private units additionally observe the first publication
and grant-time predecessor for both transitions, and reject owner, column,
attribute, rowtype, definition, identity and epoch changes while truly queued.
Workers always have bounded cancel/release/join cleanup. The PG-only command
above discovers this new contract without modifying the shared definitions.
Completed real mode consumes the actual native emitted dump SQL, asserts
unpopulated definitions, then explicitly refreshes replayed base rows.
The selected FeatureSmoke composes these contract scenarios. A separate
root-owned standalone public Go source in the OMP session is temporarily
materialized as `./.materialized-refresh-smoke/main.go`, run with
`go run ./.materialized-refresh-smoke` (external900-second deadline), then removed.
Neither selected tests nor oracle-only discovery replace this executable proof.


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
witnesses; native uses the same supported multirow INSERT without RETURNING and
retains full failure/no-cursor/sentinel-data assertions. INSERT RETURNING itself
is an existing native exclusion, not a new prepared-body capability.
Retained eager native sql.Rows are consumed after DEALLOCATE and a real public
driver.Conn.Close invoked inside sql.Conn.Raw with a nil callback result. This
avoids conflating physical session end with database/sql's ErrBadConn wrapper-close
wait/cancellation while Rows still owns its borrowed-connection release.
`^TestReferenceSQLPreparedInquiryActuals$` additionally exercises already-supported
comment, deparse, index-property and visibility actual expressions, with independent
TEXT/BOOL/NULL values before comparison. The private
`TestPreparedSQLActualNoSnapshotDemand` checks that primitive/STRICT-null actuals
do not capture unneeded catalog environments; it does not replace public references.

Timezone lookup coverage uses `RUN='^TestReferenceTimezoneViews'` in both
`make check` and
`make check REFERENCE=1` (900 seconds per invocation).
Its smoke selector is `RUN='^TestReferenceTimezoneViewsFeatureSmoke$'` in both
modes. Independent rows/types, full55000/42703/42804/42883 diagnostics, declared
metadata, nil/typed NULL predicate parameters, session/transaction capture,
PUBLIC SELECT and symbolic native controls are asserted before backend comparison.
WriteAnalysis and InsertAnalysis preserve existing supported semantic errors before
read-only rejection; VALUES column-reference0A000 is an explicit native capability
boundary, not a PostgreSQL parity claim. Smoke includes nil-bound empty metadata
and semantic-error savepoint recovery. Provider units exercise DST transitions,
negative DST, historical matching, inventory and cancellation without public SQL.
MONEY definitions use `RUN='^TestReferenceMoney'` in both
`make check` and
`make check REFERENCE=1` (900 seconds per invocation).
Run affected catalog/locale definitions with
`RUN='^TestReference(PGCast|PGDescription|FormatEncoding)'` in both modes.
Feature smoke is `RUN='^TestReferenceMoneyFeatureSmoke$'` in both modes: exact
integer versus float division, FLOAT8 monetary ratio metadata, prepared bytes,
numeric endpoint conversion, recoverable overflow and typed native Dump replay
have independently specified observations. Native controls stay explicitly
native-only inside the shared scenario; discovery filters are not retained.
The exact money_ops catalog inventory (OID10047/family2099/input790/method403/
defaulttrue) is explicitly PostgreSQL-only: native pg_opclass projection is
outside this part. `TestMoneyDefaultOrderingProvider` asserts actual native
class ownership/default identity, input admission and endpoint comparison;
the shared monetary arithmetic scenario independently exercises real index
creation, ordering and grouping in both modes. Native declared MONEY columns
have known nullability; literal/computed expression nullability remains unknown,
including a typed NULL literal. Tests assert these distinct metadata contracts.
`TestReferenceMoneyUniqueIndexes` covers constraint-backed and standalone unique
keys, direct/prepared differently spelled equal amounts, exact large-unit
neighbors, default NULL DISTINCT behavior, complete23505 diagnostics and atomic
failed writes. Locale/stability coverage queries the immutable arithmetic
expression index before and after INSERT/UPDATE/DELETE maintenance.

MONEY Restore cancellation distinguishes precanceled API rejection from a live
request canceled at reader EOF after consuming a nonempty actual Dump. The latter
proves complete reader progress beyond admission, before parsing or staged SQL
execution. Both preserve exact original rows/view and byte-identical catalog
Dump; subsequent default insertion establishes recovery and retained defaults.

Actual provider units use
`make check RUN='^TestMoney'`.
`TestMoneyMinimumDivisionOverflow` binds the real MONEY/INT2/INT4/INT8
Arithmetic providers with minimum raw units and -1, asserting complete native
22003 diagnostics, no result, unchanged operands and subsequent valid division.
This is the approved native safety exception: REL_18_0 cash_div_int64 lacks
an overflow guard. It is not a pinned oracle result, and no minimum/-1 SQL may
run in the shared singleton PostgreSQL fixture, discovery or smoke. Safe
neighboring negative division and checked overflow remain dual-mode witnesses.


Array aggregation uses `RUN='^TestReferenceArrayAgg'` with both
`make check` and
`make check REFERENCE=1` (900 seconds each).
Feature smoke uses `RUN='^TestReferenceArrayAggFeatureSmoke$'` in both modes.
These shared scenarios independently assert scalar NULL inclusion, array rank
and bounds, metadata/domain identity, complete diagnostics, direct/prepared
frames, source ACLs and INSERT atomicity; native controls remain explicit phases.
Public source-authority scenarios use actual table/column GRANT and the same
consumer identity in both modes; owner substitutions are not positive ACL proof.
The `TestReferenceRoleGrant` family covers grants, origin-specific delegation,
table/column revoke independence, warnings, atomicity, utility transaction
boundaries and native control lifetimes. Its independent expectations require
the pinned PostgreSQL oracle before native implementation. Select it with
`make check REFERENCE=1 RUN='^TestReferenceRoleGrant'`
and `make check RUN='^TestReferenceRoleGrant'`
(900 seconds per invocation). Migrated consumer families must also be selected
in both modes; this new family does not replace their complete assertion unions.
`TestReferenceRoleGrantColumnNoticeOrder` covers reversed/duplicate/mixed
requests, dropped attribute gaps and renamed-column diagnostic traversal.
`TestReferenceRoleGrantColumnFullMaskNotices` compares an enumerated merged
four-privilege column mask with ALL under partial and zero grant options.
Both assert complete ordered notices, quiet Prepare and real allowed/denied
consumers through direct and prepared routes. Focused gates (900 seconds each):
`make check RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`
and
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`.
`TestReferenceRoleGrantFeatureSmoke` exercises the complete public grant path.
The additional root-owned standalone public Go source is retained in the OMP
session, temporarily materialized as `./.role-grant-feature-smoke/main.go`, run
with `go run ./.role-grant-feature-smoke`, and removed after recording its result.
No permanent executable or native-only SQL witness belongs in the repository.
Each invocation has a 900-second deadline:
`make check RUN='^TestReferenceRoleGrantFeatureSmoke$'`
and
`make check REFERENCE=1 RUN='^TestReferenceRoleGrantFeatureSmoke$'`.
Source-provenance units complement, never replace, public delegation/revoke
witnesses. Keep upstream table-option revocation with surviving distinct-origin
column rights separate from a same-column chain's RESTRICT failure. Warning
assertions retain the complete release system/live-attribute traversal, without
claiming native system-column query support.

`TestReferenceRoleRevoke` adds option-only/PUBLIC/owner boundaries, selective
recursive CASCADE with alternate grant-option support, atomic RESTRICT,
origin-specific table/column selection, complete warnings and held-query/control
lifecycles. Each definition runs direct and prepared public routes. Selected
gates (external 900-second deadline each; run every line in native mode and again
with `REFERENCE=1` for the pinned PostgreSQL oracle):

```sh
make check RUN='^TestReferenceRoleRevoke(OptionOnly|PublicOwner|TableLike|Cascade|AlternateSupport|SelectiveBits|IndependentAccess)$'
make check RUN='^TestReferenceRoleRevoke(Columns|Provenance|Diagnostics|Notices|RestrictAtomicity)$'
make check RUN='^TestReferenceRoleRevoke(Lifecycle|Transactions|FeatureSmoke)$'
```

Oracle-first discovery may select `/(direct|prepared)/postgres` after the
top-level family expression with `REFERENCE=1`. Do not put `$` immediately
before `/` in Make's RUN value: Make consumes that pair and can silently select
no tests. Discovery is not complete dual-mode proof. Native pre-canceled utility
execution is an explicit adapter guarantee; lib/pq can complete a fast utility
before observing cancellation. The shared SQL lifecycle assertions remain the
same in both modes.

The additional root-owned revoke smoke source stays in the OMP session, is
temporarily materialized as `./.role-revoke-feature-smoke/main.go`, run with
`go run ./.role-revoke-feature-smoke` (900 seconds), and removed after recording
the observations. Its shared reference counterpart is selected with
`make check RUN='^TestReferenceRoleRevokeFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferenceRoleRevokeFeatureSmoke$'`.

Virtual generated-column coverage uses the shared untagged
`TestReferenceGeneratedVirtual` family: lazy demand, joined/outer/RETURNING
frames, definition/security diagnostics, column grants, constraints/index
boundaries, metadata, native controls and kind-preserving copies. Selected
proof requires both modes, plus the existing STORED family for preservation
(external 900-second deadline for every command):

```sh
make check RUN='^TestReferenceGeneratedVirtual'
make check REFERENCE=1 RUN='^TestReferenceGeneratedVirtual'
make check RUN='^TestReferenceGeneratedStored'
make check REFERENCE=1 RUN='^TestReferenceGeneratedStored'
```

Focused outer-join absence and destination-owned copied CHECK regressions:
`make check RUN='^TestReferenceGeneratedVirtual(OuterJoins|CopiedChecks|CopyGeometry)$'`,
also with `REFERENCE=1`. Private occurrence/frame, grafted-alias, transient merge
and destination-window units are included by `make check RUN='^TestGenerated'`.
Focused proof supplements, never replaces, the complete families above.

Focused scheduled/window/correlated-DML/MERGE/stored-view consumer proof:
`make check RUN='^TestReferenceGeneratedVirtual(BoundedProjection|WindowIdentity|CorrelatedUpdateFrom|CorrelatedDeleteUsing|JoinedMerge|StoredViewGraft)$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
goldens distinguish absent operands from real NULL-input rows, window input
permutations and partition/order keys, scalar-only DML demand, joined MERGE
ON/WHEN/actions, compound/derived grafts and atomic22012 failures. The window
file is `generated_virtual_window_identity_test.go`, not the GOOS-filtered
`_windows_test.go` suffix. Private `TestGeneratedVirtual` consumer units assert
missing-identity failures, unused-definition demand exclusion, shared window
row/provenance/version/lineage translation, scheduled-cache and retained-view
projection evaluation, and saved MERGE identity after borrowed cursor close.
Original `TestReferenceGeneratedVirtualDemand` also covers the retained view's
aggregate consumer; keep it selected alongside the newer regressions. All these
private units are also selected by the complete `^TestGenerated` command above.

Focused ordinary ALTER TYPE USING owner-row proof and feature smoke:
`make check RUN='^TestReferenceGeneratedVirtualAlterTypeUsing(Atomicity)?$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
direct/prepared expectations are (2,3,3) with c INT8, present all-NULL owner
(NULL,9,9), table/schema-qualified reads, unchanged generated definitions and
demanded Execute22012 preserving original c INT4, rows, defaults and catalog OIDs.
Query reads still require real occurrence identity; no synthetic query stage or
missing-identity fallback is used for the physical owner rewrite. The changed
initial mixed-kind Dump assertion additionally requires
`make check RUN='^TestReferenceGeneratedVirtualControls$'`, also with `REFERENCE=1`.

Focused shared INSERT-query/COPY logical NOT NULL proof and feature smoke:
`make check RUN='^TestReferenceGeneratedVirtual(InsertSelectConstraints|InsertSelectAtomicity|CopyConstraints|CopyAtomicity)$'`,
also with `REFERENCE=1`, each under an external900-second deadline. Independent
direct/prepared INSERT SELECT goldens require (2,3), real NULL-owner constant
and COALESCE recipes yielding (NULL,9), exact23502 with literal `virtual` detail,
and demanded division22012. Late ordered-source failures preserve both seed rows.
The COPY definitions live in `reference/dml` and reuse `copyTextScenario` and its
existing public native CopyFrom/pinned PostgreSQL stream consumer; the complete
virtual-family selector above includes them. Assert actual COPY1 counts/logical
rows, zero failure results and exact line1/line2 Context, unchanged seed rows
after late failures, successful transaction rollback and failed-savepoint25P02
recovery. Both consumers accept nullable1/a with a=0 without generation demand;
a-only reads succeed and a later b read fails22012 without changing the base row.

The root-owned standalone virtual smoke source stays in the OMP session and
is temporarily materialized as `./.generated-virtual-feature-smoke/main.go`.
Run `go run ./.generated-virtual-feature-smoke` with a 900-second external
deadline, record actual typed rows, generated-write/lazy-read diagnostics,
metadata, b-only grants and two native Restore/redump cycles, then remove the
temporary source directory. Its shared SQL counterpart is selected with
`make check RUN='^TestReferenceGeneratedVirtualFeatureSmoke$'` and the same
command with `REFERENCE=1`. No discovery-only backend filter remains in the
completed shared family; discovery results do not replace dual-mode proof.


Native Reset/Dump/Restore have no PostgreSQL API equivalent. Keep their witnesses
in the same dual-mode reference scenario, with explicit native-only control
phases and independently specified PostgreSQL SQL outcomes where applicable.
Do not invent a PostgreSQL control API or duplicate the main SQL test body.

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

CREATE inheritance coverage lives in the untagged `TestReferenceInheritance`
family. It independently asserts merges, diagnostics/notices, direct/prepared
metadata, nonpropagating keys, dependency/drop lifecycle, generated/serial/identity
composition, recursive/ALTER capability boundaries and actual native controls.
Controls use native Dump output, including persistent graph replay beside observed
live TEMP address collisions and exact live TEMP-to-persistent replacement refusal.
The controls have no invented PostgreSQL API counterpart; PostgreSQL independently
executes shared SQL and consumes native emitted streams where applicable.

The retained-exclusion collision regression observes an actual imported parent
descriptor OID equal to a live TEMP child's local EXCLUDE constraint OID before
native Restore. Its bounded native-only fixture uses at most 64 real sequence
allocations and existing regclass OID queries, not allocator constants or an
unsupported catalog projection. Shared direct/prepared PostgreSQL SQL independently
asserts graph/default/CHECK/EXCLUDE behavior. After Restore, native assertions
retain exact TEMP relation/index/constraint identities and rows/enforcement, and
ordinary CREATE with DEFAULT/NOT NULL/CHECK plus INSERT/query returns INT4 row42.
The descriptor unit additionally asserts all inheritance-origin remapping, source
immutability, retained storage and complete combined definition-identity validity.
Run the completed body in both modes with no discovery filter (900 seconds each):
```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$'
timeout 900s make check RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$'
timeout 900s make check RUN='^TestInheritanceControlsRetainedExclusionDefinitionCollision$'
```

Root-run selective gates (900 seconds each, after edits and generation finish):
```sh
timeout 900s make check RUN='^TestReference(Inheritance|NotNull|Check|TableLike)'
timeout 900s make check REFERENCE=1 RUN='^TestReference(Inheritance|NotNull|Check|TableLike)'
timeout 900s make check RUN='^(TestInheritance|TestNotNull|TestTableLike|TestAlterDefault)'
timeout 900s make check RACE=1 RUN='^TestInheritance(Admission|Graph|Controls)'
timeout 900s make check RUN='^TestReferenceInheritanceFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceFeatureSmoke$'
```
The standalone public API smoke is supplied as temporary session material, not
committed scaffolding. Neither selected gates nor smoke replace full quickcheck
and the post-integration1800-second finalcheck.

The dependency-shape review regressions independently assert PostgreSQL provider
CASCADE column/definition removal and native atomic inheritance ALTER rejection,
including consumer recovery and graph enforcement. Run the same completed bodies
in both modes (900 seconds per invocation):
```sh
make check RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$'
```

Retirement authority regressions cover provider schemas containing the sole child
or the parent, each with typed-column, DEFAULT and CHECK dependencies. Both routes
assert retained standalone physical rows/metadata/enforcement or complete outside
descendant retirement, without weakening the surviving-graph boundary above:
```sh
timeout 900s make check RUN='^TestReferenceInheritanceRetirementAuthority$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceRetirementAuthority$'
timeout 900s make check RUN='^TestInheritanceDependencyAuthority$'
```

Prospective retirement regressions use a parent that is also a regclass provider
for the retained other parent's DEFAULT or CHECK. CASCADE discovers their shared
child; the retained parent becomes standalone before its definition is pruned.
Both direct/prepared routes assert removed relations/edges, retained rows and
metadata, consumed provider definitions and independent local definitions; native
held eager child results retain actual original values and metadata. The real-stage
units use the same q root with actual admission, not a pre-removed child:
```sh
timeout 900s make check RUN='^TestReferenceInheritanceProspectiveRetirement$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceInheritanceProspectiveRetirement$'
timeout 900s make check RUN='^TestInheritanceProspectiveRetirement$'
```

`TestReferenceInheritanceLocalExclusionReplay` covers a child with only inherited
columns and a first local EXCLUDE declaration. It consumes the actual native dump
through Reset/Restore and pinned PostgreSQL replay, then asserts rows, default
enforcement and graph edges. Native replacement also retains a live TEMP-to-TEMP
inheritance graph, its exclusion constraint OID, physical data and enforcement.
Include `Inheritance|Exclusion` when verifying this integrated composition; do not
replace these consumer outcomes with dump-string or canonical-byte assertions.

## Automatically updatable view witnesses

The `TestReferenceViewUpdatable` families exercise logical view targets through
public direct and retained prepared consumers: base defaults and counts,
visibility/filter escape, existing DML caller variants, restricted shapes and
full diagnostics, current owner/invoker authority, atomicity, transactions,
contention, replacement and native-control cycles. Their independently specified
expectations must be observed on the pinned oracle before production changes.
Select the small families separately in both modes (900 seconds per command):

```sh
make check RUN='^TestReferenceViewUpdatable(Rows|Visibility|Callers|Obligations)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Restrictions|Diagnostics|Discovery)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Security|InsertSecurity|WriteSecurity)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Atomicity|Transactions|Contention)$' REFERENCE=1
make check RUN='^TestReferenceViewUpdatable(Lifecycle|Smoke|Replay)$' REFERENCE=1
make check RUN='^TestViewWrite(Mapping|Defaults|Demand|QueuedRecheck|Cancellation|Replacement)$' RACE=1
make check RUN='^TestReferenceViewWriteCapabilityBoundary$' REFERENCE=1
```

Repeat those exact selectors without `REFERENCE=1` for native proof. Initial
oracle discovery may temporarily filter non-PG backends under the policy below;
remove every discovery filter before native red and final dual-mode verification.
`Discovery` resolves no-op casts/COLLATE, stored row marks and unused constant
expression demand using full diagnostic mismatch evidence, not native goldens.
Native Reset/Dump/Restore phases do not invent server control APIs. PostgreSQL
tuple contexts are structurally validated, not assigned native physical values.
Supplemental native manager/stage units accompany the production round.
`TestViewWriteDefaults` also checks physical conflict-arbiter selection without
changing the logical target namespace; `TestViewWriteDemand` retains typed
parameter identity and imported values through the shared expression rewrite.
`TestViewWriteMapping` admits source-alias wrappers emitted by native Dump replay
without admitting aliases around derived sources; public Smoke and the actual-API
Go smoke retain their post-Restore future-write expectations unchanged.
The independent-review regressions pin narrow-view INSERT conversion22003,
independent stored-subquery SELECT42501 under owner/invoker grant changes, and
live subquery counts after unrelated source-layout changes and own update/delete
overlays. Run the complete definitions in both modes:

```sh
make check RUN='^TestReferenceViewUpdatableReview(Conversion|SubqueryACL|SubqueryVisibility)$'
make check RUN='^TestReferenceViewUpdatableReview(Conversion|SubqueryACL|SubqueryVisibility)$' REFERENCE=1
```

`TestViewWriteDemand` additionally captures a real physical prefix with a
committed deletion, selects its live rows, then bridges reordered frozen
attributes and closes the source before checking manager cleanup.
These mechanics supplement the public Callers/Security/Lifecycle/Smoke/Replay
regressions; they do not replace their required native and real-mode proof.
WITH, TABLESAMPLE, function FROM and target set-returning definitions have no
admitted executable native definition surface in this part; they are not falsely
reported as automatic-updatability diagnostics by parser-error witnesses.
The separate actual-API Go smoke source is an ephemeral OMP session artifact;
root materializes, runs and removes it rather than committing a smoke executable.

## View check-option witnesses

`TestReferenceViewCheck` covers independent direct and genuinely retained prepared
NEW-row behavior, the full 27-mode nested truth table, default/generated physical
mapping, logical RETURNING metadata, atomic producer failures, correlated/live/own
predicate sources, actor disclosure/ACL freshness, transaction/savepoint/cancel
boundaries, rule/trigger oracle semantics, and native persistent mode round trips.
The tests were authored before implementation; exact diagnostics and operation
precedence are observed against the pinned PostgreSQL18.0 oracle, not native
goldens. Functional definitions are shared; PostgreSQL provider and native
control-only phases explicitly identify mechanisms unavailable in the other mode.

Run these small selectors with an external900-second deadline in **both** modes:

```sh
make check RUN='^TestReferenceViewCheck(Grammar|Declaration|Options|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Basic|Nested|NewRows)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Atomicity|Sources|Security|ConflictUpdate)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Transactions|Cancellation|RuleTriggerBoundary)$' REFERENCE=1
make check RUN='^TestReferenceViewCheck(Lifecycle|Replay|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceViewCheckReview(VirtualNewRows|QuotedNames)$' REFERENCE=1
make check RUN='^TestReferenceViewReplace(CapabilityBoundaries|CapabilityBoundariesConsumerRoutes|Options)$' REFERENCE=1
```

Repeat each exact command without REFERENCE=1 for native proof. Changed existing
ViewUpdatable definitions/callers retain their separately selected dual-mode
regression families above. Discovery filters must be removed before native red
and final dual-mode proof. The additional existing identity-order conflict-update
producer received its own PG-first regression/native-red checkpoint:
`make check RUN='^TestReferenceViewCheckConflictUpdate$'`, also with REFERENCE=1.
It is common-gate coverage, not general view action mapping expansion.
The review-remedy regressions also received a PG-first/native-red checkpoint.
They independently assert virtual generated NEW predicates with retained handles
across live layout changes, independent generated subquery occurrences, and full
44000 diagnostics with literal embedded-quote/backslash view names.

Focused mechanism units (not public SQL substitutes):

```sh
make check RUN='^TestViewCheck(Options|Declaration|Stack|Mapping|Predicate|Admission|Cancellation|Finish|ControlRemap)$'
make check RUN='^TestViewCheck(Cancellation|Finish)$' RACE=1
```

The feature smoke selector is `^TestReferenceViewCheckFeatureSmoke$`, in both modes.
Native lifecycle proof performs two genuine Dump/Reset/Restore cycles and future
held writes, plus invalid suffix/reader/pre-cancel/EOF-cancel vetoes. Replay sends
actual native Dump bytes to PostgreSQL; SELECT-only ViewCopy is not persistence
proof. The separate actual-API smoke is an ephemeral OMP session source: root
materializes `.view-check-public-smoke/main.go`, runs
`timeout 900s go run ./.view-check-public-smoke/main.go`, records observations,
and removes that root-created scaffold rather than committing an executable.

## Required implementation order

1. **Write the shared reference scenario and run real PostgreSQL first.** Use
   the pinned PostgreSQL 18.0 fixture, not a local server or newer release. Start
   with independently reasoned expectations; use observed PostgreSQL results to
   resolve unknown values, metadata and diagnostics.
2. **Record reference data from that run.** Put concrete expected rows, column
   names, database type names, affected counts and relevant diagnostics in the
   test. Review them as a contract; never derive expected values from stuntgres
   output, copy native failures into goldens, or assert only backend equality.
3. **Run quick mode in the second iteration.** Confirm the recorded contract
   exposes missing/incorrect stuntgres behavior. Observe the failing regression,
   implement the smallest fix, and rerun until it passes.
4. **Finish by selectively running both modes.** Run the same completed definitions
   in quick and real PostgreSQL modes, confirming independent expectations and
   consistency for every added, changed or migrated scenario. Then run ordinary
   `make quickcheck`; final `make finalcheck` runs after integration under the
   catalog-part workflow. Selected tests do not replace full verification.

The shared predefined-domain witnesses select
`RUN='^TestReferenceInfoTimeStamp(Values|Metadata|Diagnostics|Identity|NativeControls|FeatureSmoke)$'`
with both `make check` and `make check REFERENCE=1` (900 seconds each).
Private descriptor/catalog/remap units select
`RUN='^TestInfoTimeStamp(BuiltinDescriptor|CatalogIdentity|CatalogRemap)$'`.
Consumer carrier coverage also selects `RUN='^TestAnyValueProviderDatums$'`:
the newly concrete builtin domain supplies finite p2/carry and nonfinite datums,
checks its domain/base tags and retains the exact chosen datum through aggregation.
Feature smoke selects `RUN='^TestReferenceInfoTimeStampFeatureSmoke$'` in both modes.
Pinned PostgreSQL18.0 supplies independent p2 values, TIMESTAMPTZ transport and
metadata, complete base diagnostics and catalog/default controls. Native APIs
add actual qualified Dump/Reset/Restore, retained cursors and failed replacement
atomicity; PostgreSQL has no corresponding process-local Restore API.
Cache ColumnTypes while a cursor is open to check retained descriptors after
EOF/Close. Pre-canceled pq reads can race database/sql's watcher: validate available
metadata/emitted ordered prefixes, permit ordinary cancellation/ErrBadConn, and
require full rows on clean EOF. Native pre-cancellation requires context.Canceled
without a cursor. Separate cancellation handles preserve an untouched write
atomicity observer. This adapter distinction never weakens domain SQL diagnostics.
Native inherited-domain defaults, sibling-domain/implicit-array executability
and general information-schema/pg_type views are explicit exclusions.

INET/CIDR scalar acceptance uses `make check RUN='^TestReferenceINET.*$'`
and `make check REFERENCE=1 RUN='^TestReferenceINET.*$'` (external900-second
deadlines), plus codec/coercion/ordering units with
`make check RUN='^TestNetwork.*$'`.
Feature smoke selects `^TestReferenceINETFeatureSmoke$` in both modes.
The definitions independently assert exact values/types/counts, full
diagnostics/stages, raw transport and empty/all-NULL metadata, held binds,
statement atomicity and real native Dump/Reset/Restore with PostgreSQL replay.
Native control API/cursor phases are explicitly native-only, not a fabricated
PostgreSQL equivalent. The oracle-first discovery filter is not permanent.
A separate throwaway public executable additionally exercises prepared binds,
prefix-aware ordering/casts, diagnostics, metadata and real control round trips;
its temporary main package is not committed or substituted by a test.

RANGE window coverage selects the complete `^TestReferenceQueryWindow` family
in both `make check RUN='^TestReferenceQueryWindow'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryWindow'`
(900 seconds each). Feature smoke uses `^TestReferenceQueryWindowRangeFeatureSmoke$` in both modes.
Named volatile-offset identity is covered by
`^TestReferenceQueryWindowRangeNamedOffsetIdentity` in both modes: same name
advances nextval once; distinct names advance it twice and give the final pair
{2,3}, without assuming evaluation order. The stored-view witness cycles offsets
1/2 to retain independent membership through repeated export/replay and native
Dump/Restore. Actual clone/remap units preserve query-local named identity while
checking shared sort groups. Successful support binding performs neither source
diagnostic formatting nor class-input diagnostic lookup.
Provider units use ` RUN='^TestWindowRange'`;
parser/stage units use `RUN='^TestQueryWindow'` in their respective packages.
Independent membership/type/English diagnostic constants are asserted before
comparison. Pinned discovery establishes that count inside EXISTS still demands
its frame, row_number/LIMIT0/unused frames eliminate runtime offsets, NULL fails
on empty input, and negative/NaN can avoid support demand on empty/all-NULL input.
Constant input division errors retain22012 before NULL frame validation.
RANGE offset diagnostics use the binder's coherent visible-type policy: visible
domains are unqualified and hidden/shadowed domains retain their required schema
spelling; descriptor SQL declarations are not diagnostic goldens. Relation ACL
failures occur at Execute in both native and PostgreSQL, including freshly
prepared SELECTs; do not broaden native Prepare authority to fit a wrong stage.
The public persistent-definition and four-row2/4/3/2 overlay witnesses migrated
from native window units; actual staged cancellation/descriptor mechanics remain
package units. Native Dump/Restore phases have no invented PostgreSQL API.

GROUPS coverage uses `make check RUN='^TestReferenceQueryWindowGroups'` and
`make check REFERENCE=1 RUN='^TestReferenceQueryWindowGroups'` (900 seconds each).
Focused smoke selects `^TestReferenceQueryWindowGroupsFeatureSmoke$` in both modes.
Shared scenarios independently specify complete peer rows/types, bigint coercion,
start/end diagnostics/stages/carets, empty/all-NULL demand, volatile named identity,
authorization, prepared recovery, atomicity and cancellation. GROUPS negatives
raise22013 even for empty/all-NULL demanded frames; do not reuse RANGE's relaxed
support-demand expectations. Native-control phases use actual pristine Dump/Restore
and retained cursors, not PostgreSQL script replay as a substitute. Native units
select `^TestQueryWindowGroups` (also `RACE=1`) for safe peer endpoints, actual
offset/validation/sort/peer/group/count/output/prepublication cancellation,
immutable graph remap/identity, exact descriptors and INSERT Finish veto.
Because the shared representation also carries RANGE, final selected proof includes
`^TestReferenceQueryWindow` in both modes and `^TestQueryWindow` native units.

ROWS coverage selects `^TestReferenceQueryWindowRows` in both native and real
pinned modes, with `^TestReferenceQueryWindowRowsFeatureSmoke$` for focused smoke.
Independent constants cover physical endpoints/empty frames, no-order bags,
shared tie order, bigint assignment typing, exact diagnostic fields/carets,
demand on empty/all-NULL/EXISTS, named volatile identity, metadata, ACLs and write
atomicity. Actual native controls reuse pristine Dump/Restore and retained-cursor
assertions. Internal `^TestQueryWindowRows` units (also `RACE=1`) cover MaxInt64
safe physical endpoint clamping, graph/domain remap, source authority, actual
offset/validation/sort/physical-frame/count/output/prepublication cancellation,
descriptor lifetime/release and Finish veto. Do not issue overflow-inducing
MaxInt64 FOLLOWING endpoints to PostgreSQL; moderate100 endpoints are portable.
Because sorting/offset storage is shared, final selected proof includes
`^TestReferenceQueryWindow` in both modes and `^TestQueryWindow` native units.

Parser scratch reuse is an internal allocation change, not a parsed-result cache.
Each SQL parse, restricted INSERT replay and standalone type-input parse borrows
an exclusive generated receiver. The whole receiver is cleared before return,
including on error or panic; lexers, options, contexts and ASTs are never pooled.
One private `sync.Pool` retains fixed-size cleared receivers; retention is
GC-evictable, not bounded by a hard object count. Checkout does not wait for
another invocation or cap active parses. No test may assume a particular
post-return receiver identity, guaranteed hit, or GC eviction. Grammar cells use
owned Column, AlterTableAction and WindowSpec pointers and compact ON COMMIT
action/position metadata; existing public AST storage boundaries stay unchanged.
Invocation-local grown stacks are not retained. `^TestParseScratch` in
`./internal/parser` covers deterministic full typed-zero reset after actual SQL/
type success, syntax error, in-flight cancellation and callback error/panic,
notice ownership/reentry, deep stacks, retained parameters/source positions,
declarations, lexical options and concurrent independent results/cancellation.
The production-used explicit receiver helper lets reset tests observe a known
receiver without manipulating the pool. Generated cleanup precedes full reset,
which precedes return; original panics are not recovered by production code.
Run `make check RUN='^TestParseScratch'` and its
`RACE=1` counterpart, then ordinary quickcheck after edits.

Performance evidence must compare the identical
`internal/parser/parser_scratch_bench_test.go` harness before and after changes.
`BenchmarkParseScratchMixed` measures one complete fifteen-input cycle;
`BenchmarkParseScratchNested` measures one restricted multirow INSERT.
Record serial/parallel samples, ns/op, B/op and allocs/op, absolute and percentage
deltas, toolchain/environment and actual commands/exits. Measure both default
startup P and a separate process started with `GOMAXPROCS=1`; `-cpu` alone does
not change a cache capacity initialized at startup. Attribute scratch bytes and
object counts at every actual receiver allocation site using matched profiles,
not only the unused generated constructor. GC and race-mode pool discards are
normal; neither warm identity assumptions nor disabled GC are acceptable.
The prior channel can already reuse ordinary serial parses at default P; reduced
P1 nested allocations do not promise default-P or whole-suite runtime gains.
Historical fresh-parser allocation profiles are motivation, not candidate
deltas. Complete real-reference race measurements are human-only; agents MUST NOT
invoke refcheck. Selected dual-mode proof and complete Docker-free verification
precede postintegration finalcheck. Disclose timeouts/failures honestly.


### Commands and oracle-first discovery

Run from repository root with Go >=1.26. Real mode needs Docker and image/module access.

```sh
# Oracle-first discovery, then native proof; repeat both after affected edits.
make check RUN='^TestReferenceFeatureBehavior$' REFERENCE=1
make check RUN='^TestReferenceFeatureBehavior$'
# Whole-project verification after edits, before review/integration.
make quickcheck
# Same Docker-free suite with race detection.
make racecheck
# After integration: startup benchmark, full native race suite, dependencies.
make finalcheck
```

`check` requires `RUN` selecting individual tests or small sets; add `RACE=1` for
selected race proof. All test targets regenerate parser and Unicode data and keep
output quiet. The Makefile imposes no deadline and disables Go's implicit test
timeout (`-timeout=0`). Agents MUST set external command deadlines of 900 seconds
for check, quickcheck, racecheck and bench, or 1800 seconds for finalcheck.
`refcheck` runs the whole real reference suite for humans only; agents MUST NEVER
invoke it. Neither whole-project target replaces selected dual-mode pre-review proof.

`make check RUN='...' REFERENCE=1` enables `-reference=true` and the `reference`
build tag for `./reference/...`; it runs both native and PostgreSQL backends,
not PostgreSQL alone. Real-mode targets use `-p 1`: each package owns an
independent fixture/container/process lifecycle, with startup and teardown
serialized across packages. Explicit true eagerly starts every selected
package's oracle even when no tests match. The stdlib-only
`reference/cmd/checkpackages` command recursively discovers tagged test files
using `go list -json -tags reference ./reference/...` and parses top-level test
names without running tests. For selectors without `/`, only packages with
matching top-level tests run. Slash-containing subtest selectors conservatively
retain all test packages so Go performs exact test/subtest matching; invalid
regexes fail rather than narrowing selection. A no-match selector runs the
fixture test package to preserve eager startup and visible startup failures.
Packages with no test files are excluded. The at-most-30-package cap, including
the selector command and fixture, bounds conservative startup overhead.
The ordinary runner visits native first. During the initial oracle-discovery iteration, use the
shared `p.Backends()` loop and temporarily select only `backend.PG` (for example,
`if !backend.PG { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make mode.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.Step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.

Direct trigonometric definitions use `RUN='^TestReferenceMathTrigDirect'` in both
`make check` and `make check REFERENCE=1`, with900-second external deadlines.
Complete direct/inverse regression proof uses `RUN='^TestReferenceMathTrig'`;
private parser/binder/provider units use `RUN='^TestMathTrig'`.
Feature smoke selects `RUN='^TestReferenceMathTrigDirectFeatureSmoke$'` in both
modes: all eight FLOAT8 direct/prepared functions, exact degree poles and zero
signs, NULL, complete22003/22P02 diagnostics, fresh held bindings and recovery.
Independent degree matrices, rotations, subnormal order, ordinary at-most2ULP
targets and finite/sign/bounded-magnitude near-zero witnesses precede comparison.
All discovery backend filters must be absent from completed definitions.
Native precancellation and Reset/Dump/Restore/retained readers are explicit native
phases; actual native-emitted SQL replays on PostgreSQL when the real mode runs.
SQL PREPARE/EXECUTE uses a held physical connection and checks source Position
instead of inventing wire-portal Context. After edits the root runs `make quickcheck`;
post-integration verification remains `make finalcheck`.
The separate root-owned public smoke source is the living plan Appendix A;
materialize it temporarily, run `timeout 60s go run ./.math-trig-direct-feature-smoke`,
record quiet exit0 or full failure evidence, and remove its directory.

The gcd/lcm family selects `RUN='^TestReferenceMathGcdLcm'` in both modes;
private parser/binder/provider coverage selects `RUN='^TestMathGcdLcm'`.
Use a900-second external deadline for each selected invocation:

```sh
timeout 900s make check RUN='^TestMathGcdLcm'
timeout 900s make check RUN='^TestReferenceMathGcdLcm'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceMathGcdLcm'
timeout 900s make check RUN='^TestReferenceMathGcdLcmFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceMathGcdLcmFeatureSmoke$'
```

Independent expectations cover the six INT4/INT8/NUMERIC signatures, sign/zero/
signed-minimum rules, exact fractions and display scales, nonfinite-before-zero
precedence, strict NULL and ambiguity, full diagnostics and character positions.
Held contextual binds and SQL PREPARE/EXECUTE assert phase, metadata and recovery.
Invalid held input uses the existing exact `unnamed portal parameter $1 = '...'`
Context on both adapters. Math projections have unknown nullability; the separate
COUNT projection retains native known nonnullable versus pq unknown metadata.
Computed NUMERIC has native unknown DecimalSize; pq exposes unconstrained typmod
as DecimalSize(65535,65531,true) and ScanType any, independently asserted before
normalized string comparison. Later-row write overflow asserts unchanged data.
Generated/CHECK/expression-index and aggregate/grouping consumers, actual native
Dump/Reset/Restore/fresh-Driver replay, retained Rows/descriptors and native ordinary
precancellation are part of the same shared definitions. Real mode also replays
the actual native-emitted SQL on PostgreSQL, not an invented PostgreSQL Restore API.
Discovery filters and observation-only errors must be absent before native proof.
The root-owned standalone public smoke is the catalog living plan Appendix A;
materialize it temporarily as `.gcd-lcm-smoke/main.go`, run
`timeout 900s go run ./.gcd-lcm-smoke`, record quiet exit0 or complete failure
evidence, then remove it. This complements, not replaces, dual-mode feature smoke,
whole-project quickcheck and post-integration finalcheck.

PG_USER coverage uses `RUN='^TestReferencePGUser'` in both
`make check` and
`make check REFERENCE=1`, with a 900-second deadline
per invocation. The smoke selector is `RUN='^TestReferencePGUserFeatureSmoke$'`.
The shared scenarios independently specify the nine-column LOGIN projection,
OID identity through a separately owned sequence, direct privilege flags,
PUBLIC readability, constant eight-star masking, typed NULL configuration,
finite/infinite expiry, full diagnostics and direct/prepared error stages,
retained snapshots, composition, cancellation and native-control boundaries.
Password setup is deliberately excluded from the recorder and diagnostic
failure text. Native Dump replay uses the actual emitted local SQL, not a
PostgreSQL control substitute.
Supplemental typed provider/saved-root/cancellation checks use
`make check RUN='^(TestPGUser|TestTableViewSummaryBuiltinIdentity)'`.
Related reference regression uses
`RUN='^(TestReferencePGUser|TestReferenceTimezoneViews|TestReferenceRegtype|TestReferencePGTableViews)'`
in both quick and REFERENCE=1 modes (900 seconds per invocation). This preserves
existing timezone routing, allocated regtype identity expectations and scoped
summary inventories while checking pg_user view classification.

For initial oracle discovery, temporarily filter native backends in the common
scenario helper as described above, then run
`make check REFERENCE=1 RUN='^TestReferencePGUser'`.
The exact postgres-only subtest selector is
`RUN='^TestReferencePGUser[^/]*$/^postgres$'`; full diagnostics can be selected
with `RUN='^TestReferencePGUserDiagnostics$/^postgres$/./^(direct|prepared)$'`.
`TestReferencePGUserOracleCatalog/postgres` additionally checks release attribute
types, typmods, collations and relation/rowtype identities, and independently
asserts the exact `pg_views` definition (differences print under).
These allocator assertions are
release-only research, not cross-backend role or relation allocator parity.
Remove the temporary backend filter before recording the missing-feature quick
regression and before all final dual-mode runs. Preserve the permanent
PostgreSQL-only `pg_attribute` research guard and native-only control phases.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

### Legacy pg_group verification

Use these small selectors in **both** Docker-free and pinned PostgreSQL modes,
with an external900-second deadline for each command:

```sh
timeout 900s make check RUN='^TestReferencePGGroup(Shape|OracleCatalog|Diagnostics)$'
timeout 900s make check RUN='^TestReferencePGGroup(Membership|GrantorMultiplicity)$'
timeout 900s make check RUN='^TestReferencePGGroup(Freshness|Snapshots|Visibility)$'
timeout 900s make check RUN='^TestReferencePGGroup(Controls|FeatureSmoke)$'
# Repeat each selected command above with REFERENCE=1.
timeout 900s make check RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check RUN='^Test(PGGroup(Descriptor|Projection|RoleRoot|Cancellation)|TableViewSummaryBuiltinIdentity)$'
timeout 900s make check RACE=1 RUN='^TestPGGroup(RoleRoot|Cancellation)$'
timeout 900s make check RUN='^TestReferencePGGroupFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGGroupFeatureSmoke$'
```

Shared public scenarios independently specify the exact NAME/OID/_OID fields,
current NOLOGIN predicate, real pg_roles identity relationships, direct-edge
element bags preserving grantor multiplicities, all-false membership options,
nonnull empty arrays, role defaults/overrides, unrelated-reader disclosure,
credential-column diagnostics, full diagnostic fields/positions and direct/
prepared error phases. Generated OIDs are backend-local, not allocator goldens.
The group helper validates raw consumer and middleware-owned buffers, preserves
element multiplicity and ignores only unpromised row/array order. Password setup
never enters the recorder or diagnostic failure output.

Oracle-first discovery may temporarily filter native backends **only** in the
new group scenario helper under the policy above. Remove the filter before
native-red and every completed dual-mode verification. Keep the permanent
PostgreSQL-only pg_attribute/pg_type structural witness and actual native-only
retained Rows/metadata, catalog-write/row-lock exclusions, cancellation and
Dump/Reset/Restore phases. Native controls preserve role identities/edges and
stored symbolic consumers; real mode replays the **actual native-emitted SQL**
on PostgreSQL using its own group/member identities, never a substitute Reset API.
Valid native writes fail0A000, not an invented PostgreSQL55000 join-view error.

The standalone public Driver/database/sql smoke program in the PG18-VIEW-GROUP
living plan is materialized temporarily at `/tmp/pg18-view-group-smoke.go`.
From the worktree, run `timeout 900s go run /tmp/pg18-view-group-smoke.go`.
Expected exit0 with exactly `pg_group public smoke: PASS`: three-column metadata,
empty→direct all-false member→empty, held unrelated-reader freshness, LOGIN hides/
NOLOGIN restores the same OID/list, and credential42703. Record observations and
remove the temporary source; it is not a committed test or production dependency.
Whole-project Docker-free verification remains `make quickcheck`; selected
dual-mode proof and the smoke are not replaced by quickcheck or finalcheck.

### Positioned DELETE verification

The `TestReferenceDeleteCurrentOf` family in `reference/dml/` exercises the
selected `DELETE WHERE CURRENT OF cursor_name` contract. Independent expectations
come from pinned oracle discovery and PostgreSQL REL_18_0
[`execCurrent.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execCurrent.c),
[`nodeTidscan.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/nodeTidscan.c),
and the PostgreSQL 18 DELETE, FETCH and ROLLBACK TO contracts.

The oracle-discovery backend filter has been removed. Every shared scenario now
runs natively as well as against PostgreSQL when reference mode is enabled.
Native-only Dump/Reset/Restore, detached-result and context-cancellation phases
remain explicitly asserted; they were not removed during the oracle cutover.
The old `TestReferenceDeleteCoreExcludedForms` definition is migrated into
`PositionedRow/migrated_survivor`; its obsolete native unsupported assertion is
removed, not retained as a second contract.

Selected dual-mode batches (each under an external 900-second deadline):

```sh
make check REFERENCE=1 RUN='^TestReferenceDeleteCurrentOf(PositionedRow|PreparationAndMetadata|DuplicateIdentityAndLastFetch|EligibilityAndDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceDeleteCurrentOf(RollbackAndRepeat|VersionsAndArbitration|Lifetime|UsingReturningAndConstraints)$'
make check REFERENCE=1 RUN='^TestReferenceDeleteCurrentOf(NativeControls|CancellationAndFailure|FeatureSmoke)$'
```

Run the same selectors without `REFERENCE=1` for native proof. After implementation,
test changes or remedies, repeat **both modes** with the completed definitions.
`make quickcheck` remains the whole-project Docker-free proof, not a substitute
for selected pinned-reference evidence. Supplementary package-seam proof:

```sh
make check RUN='^(TestDeleteCurrentOf|TestBetweenDeleteCursorBoundary|TestDeleteCoreParserBoundaries|TestSQLCursorCompletedPublicationBoundary|TestSQLCursorCancellationClearsAuthority)'
make check RACE=1 RUN='^TestDeleteCurrentOf(Identity|Arbitration|Publication|Lifetime|JoinIdentityRetention)$'
```

These private units inspect actual occurrence/lineage/effective versions,
zero-mode authority, original-row wait journals, post-Execute/pre-Finish staging
and cancellation, EOF/disposal and stale name incarnations.
`TestDeleteCurrentOfJoinIdentityRetention` also checks that unmarked ineligible
joins (including derived/view wrappers) allocate no occurrence/provenance/version
or recheck-pair caches, while actual marked descendants retain those caches and
eligible unlocked derived/view scans retain physical current identity. These units
supplement the public definitions, never replace them.

The family checks physical duplicate identity and last successful FETCH, quiet
Prepare/live quoted names, eligibility-before-position and complete diagnostics,
savepoint data rollback without motion rewind, row-version arbitration, cursor
and session lifetime, USING multiplicity/RETURNING metadata/constraint preservation,
committed-only persistence, replacement fences and no partial cancellation result.
Version traversal, empty-USING lookup and failed-executor diagnostics have explicit
independent expectations; fresh completed dual-mode runs remain required.
Plan-dependent unlocked ORDER BY has explicit PostgreSQL EXPLAIN/plan controls and
an independently stated native sort-versus-simple-scan contract; it does not
require reproducing PostgreSQL's optimizer. Raw `CURRENT OF $1` is independently
asserted as syntax42601 at character32 in direct/prepared SQL; executor refcursor
parameter hooks do not imply that raw SQL grammar admits this form. Genuine
foreign-key effects have complete shared native/PostgreSQL witnesses, not native no-ops.

Standalone feature proof selects `^TestReferenceDeleteCurrentOfFeatureSmoke$` in
both modes: prepare before DECLARE, FETCH id1, delete/rollback/repeat id1,
FETCH ids2,3/delete id3, commit survivor2 and post-transaction missing-cursor34000;
an independent duplicate phase removes exactly one occurrence. The root also runs
its OMP-session public-API smoke source; no repository smoke harness is added.
All new cursor fixtures use the prerequisite's admitted `FETCH FORWARD 1 FROM`
constant-count form, not adjacent FETCH NEXT syntax.

## Positioned UPDATE reference proof

The nine `TestReferenceUpdateCurrentOf` definitions in `reference/dml/` share
public-driver bodies across native and pinned PostgreSQL18.0 modes. The temporary
discovery filter is removed. Native Dump/Reset/Restore, detached rows,
pre-cancellation, pool persistence and independent Driver phases remain explicit.
Independent expectations cover last fetched physical identity, same-value count1,
simultaneous/latest-old-row SET and OLD/NEW metadata, duplicate/zero-width
projections, quiet preparation/live names, full diagnostics and character positions,
actual row-version arbitration, rollback without motion rewind, atomic failures,
and committed-only persistence/replacement fences.

Pinned discovery established empty-FROM cursor validation, losing-pair SET demand,
RR concurrent-delete wording, and analysis/Prepare0A000 rejection of a view UPDATE
target. A transparent cursor view to a base target remains a positive witness.
No PostgreSQL optimizer parity is required: physical sort versus simple/index scan
eligibility is independently asserted. Every added/changed definition requires
both selected modes after final edits; prior discovery is not final native proof.

Run each small batch under an external900s deadline, then repeat without
`REFERENCE=1`:

```sh
make check REFERENCE=1 RUN='^TestReferenceUpdateCurrentOf(PositionedRowAndCounts|IdentityAndPreparation|EligibilityAndDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceUpdateCurrentOf(VersionsAndArbitration|LifetimeAndRollback|ExistingFromReturningComposition)$'
make check REFERENCE=1 RUN='^TestReferenceUpdateCurrentOf(FailureAndCancellation|NativeControls|FeatureSmoke)$'
make check RUN='^(TestUpdateCurrentOfGrammar|TestUpdateCoreExcludedAndMalformed|TestUpdateCoreSyntax)$'
make check RUN='^TestUpdateCurrentOf(Identity|Publication|Arbitration|Lifetime)$'
make check RACE=1 RUN='^TestUpdateCurrentOf(Identity|Publication|Arbitration|Lifetime)$'
```

The private units inspect real borrowed RowID/source/index, staged replacement
versions before Finish, cancellation nonpublication, exact pending automatic row
grant and name-incarnation/disposal state. Existing DELETE occurrence/version,
eligibility, publication, arbitration, lifetime and join-cache assertions remain
regression authority for the shared mutation helper. Run all eleven positioned
DELETE definitions in their documented small batches in both modes after cutover.
Feature smoke selects `^TestReferenceUpdateCurrentOfFeatureSmoke$` in both modes;
the root's standalone public-API smoke source lives outside the checkout.

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

### MERGE insertion and implicit INSERT migration

Run each selective command under its own900-second deadline:

```sh
make check RUN='^TestReferenceMergeInsert'
make check REFERENCE=1 RUN='^TestReferenceMergeInsert'
make check RUN='^TestReferenceInsertImplicitShort'
make check REFERENCE=1 RUN='^TestReferenceInsertImplicitShort'
make check RUN='^TestReferenceInsertSelect'
make check REFERENCE=1 RUN='^TestReferenceInsertSelect'
make check RUN='^TestMerge'
```

The shared MERGE family independently checks frozen matching/bag multiplicity,
ordered conditions, mappings/defaults/identity/generated values, scalar
subqueries, complete diagnostics and analysis/execute stages, atomic constraints,
transactions, metadata, held rebind, locks/cancellation and native controls.
OracleQuestions permanently records O1–O4: demanded nonboolean ON, action
subqueries, system-column namespaces/providers and permission/read-only order.
Native count n and observed pinned pq export0 are separate expectations with
exact persisted rows, never inferred from driver count alone. Native provider42703
versus positive PostgreSQL system identities is explicit. O4 exercises genuine
public column GRANT in both backends, including empty-manifest DEFAULT VALUES,
source denial and permission/read-only ordering; native ACL units supplement it.
Schema-USAGE setup remains PostgreSQL-only; native uses its existing public
schema authority. O4 explicitly asserts native sequence-GRANT `0A000` while
PostgreSQL performs that setup. Empty sources prevent default-sequence demand;
the complete column/table authority scenario executes in both modes.
Fixtures use supported named UNIQUE constraints with NOT NULL (implicit for
identity); they do not depend on PRIMARY KEY support. Native controls receive the unwrapped `p.Native` pool;
wrapped same-DSN peers remain SQL visibility witnesses. Denied PostgreSQL actors
receive schema USAGE before testing table denial. Admission diagnostics distinguish
Prepare token positions on both backends from the absent Position on held Execute.
Review witnesses `OnCarriers` demand populated-target pairs for physical BOOL,
INT2/INT4/INT8/TEXT and their domain carriers, including zero, negative/wide
integers, empty/boolean-looking strings and NULL. `OnCarrierBoundary` separately
checks unavailable native FLOAT8 ON interpretation versus PostgreSQL effects.
`ConcurrentUnique` pins a repeatable-read frontier before the worker starts,
proves native blocking with bounded SQL timeout and PostgreSQL blocking through
its external wait catalog, then asserts commit23505 or rollback insertion without
ON restart. `Isolation` separately distinguishes public RC/RR source visibility.
`HeldACL` retains an actual handle through target all-arm INSERT and source
SELECT revoke/restore in both backends; native `TestMergeInsertHeldACL` also
supplements this with the adapter's `scope.Expected` execution seam.

`TestReferenceMergeUpdateDefaultInsertAuthority` covers empty INSERT destination
manifests through direct/prepared DEFAULT VALUES: denied selected, false and
empty-source arms, an unselected INSERT beside a matched UPDATE, and permitted
column/relation INSERT controls. Denials require complete42501 diagnostics and
unchanged data. `TestReferenceMergeUpdateDefaultInsertHeldACL` retains the same
actual handle through column/relation INSERT revoke and restore.

```sh
make check REFERENCE=1 RUN='^TestReferenceMergeUpdateDefaultInsert(Authority|HeldACL)$'
make check RUN='^TestReferenceMergeUpdateDefaultInsert(Authority|HeldACL)$'
make check REFERENCE=1 RUN='^TestReferenceMergeInsertOracleQuestions/^O4$'
make check RUN='^TestReferenceMergeInsertOracleQuestions/^O4$'
```

Pin the new authority regressions on PostgreSQL before observing native red or
correcting action-presence authorization. Each command has an external900-second
deadline; completed changed definitions require both modes before review.

Actual public feature smokes:

```sh
make check RUN='^TestReferenceMergeInsertFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMergeInsertFeatureSmoke$'
make check RUN='^TestReferenceInsertImplicitShortFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceInsertImplicitShortFeatureSmoke$'
```

MERGE smoke expects source1/2/3 versus existing1old to insert2two/first and
3three/fallback with omitted tail default, held repeat0, source append4/new insert,
empty command metadata, atomic23505, suppressed sequence draws, private rollback,
retained eager results across native Dump/Reset/Restore, same-DSN peer rows,
actual emitted SQL PostgreSQL replay and later held fresh execution.
The old native-only implicit INSERT smoke file is removed. Its full direct/
prepared/reordered/count/metadata/error/peer/retained-control/failed-staging/
handwritten-short-Restore union is covered by the complete
`TestReferenceInsertImplicitShort` family, not just its cohesive FeatureSmoke.

### MERGE matched UPDATE oracle-first regressions

The untagged `TestReferenceMergeUpdate` definitions share direct and actual
prepared SQL bodies and independent expected rows, metadata and full logical
diagnostics. These tests require selected native and pinned proofs after cutover.
Use these small selected groups with a separate external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Matching|Conditions|Bindings)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Expressions|Arrays|AssignmentBoundaries)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Cardinality|OraclePrecedence|ArrayCardinalityPrecedence)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Constraints|Atomicity|UniquePrefixes)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(ReadCommitted|RepeatableRead|RowLocks|FollowChainRowModes)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Privileges|HeldACL|PreflightOrder|SystemColumns)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(ArrayAuthority|ExcludedAssignments|MalformedActions|FieldAssignmentBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Diagnostics|CommandMetadata|Prepared)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Transactions|Cancellation|ServerCancellation)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdate(Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdateMixedDisclosure$'
make check REFERENCE=1 RUN='^TestReferenceMergeUpdateRepeatableReadFollowup$'
make check RUN='^TestReferenceExclusionWriteConsumers$'
make check REFERENCE=1 RUN='^TestReferenceExclusionWriteConsumers$'
make check RUN='^TestArrayExpressionColumnUseAndTargetManifest$'
make check RUN='^TestMergeUpdate(CaptureBaseOverlay|CaptureCancellation|QueryVisitor|MixedPendingCancellation)$'
make check RUN='^TestMergeUpdate(RowLocks|SyntacticRowModes|Publication|Cancellation|HeldACL|SourceCompletion)$'
make check RUN='^TestMergeUpdate(ReadCommittedPending|RepeatableReadPending|FollowChainPending)$'
make check RACE=1 RUN='^TestMergeUpdate(CaptureBaseOverlay|CaptureCancellation|MixedPendingCancellation)$'
make check RACE=1 RUN='^TestMergeUpdate(ReadCommittedPending|RepeatableReadPending|FollowChainPending|RowLocks|Cancellation)$'
```

Run each completed public definition with the identical selector without
`REFERENCE=1` for native-red, then both modes after the production cutover.
All temporary discovery gates/wrappers, including Prepared/HeldACL, are removed;
ordinary scenarios reuse `mergeInsertScenario` and the existing shared fixture.
No PostgreSQL-only ancestry selector is needed or appropriate for native-red.

ArrayAuthority retains independently expected subscript-source denial even with
an empty source and FALSE action, then exercises ordinary UPDATE and matched
MERGE with UPDATE-only array authority. Implicit old-container preservation is
not a SELECT reference: PostgreSQL18's
[transformAssignedExpr](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/parse_target.c)
constructs its assignment Var directly, without marking SELECT privilege.
Explicit old-value RHS reads and subscript reads still require SELECT.
Prepared/PreflightOrder assert the full existing unnamed-portal parameter Context
on both adapters; SystemColumns asserts the established42P10 WHEN rejection.
ExclusionWriteConsumers now requires genuine23P01 parity for MATCHED UPDATE,
including direct/prepared diagnostics and unchanged rows after failure; its
other write consumers remain covered. SyntacticRowModes checks the canonical
NKU-plus-UPDATE held union for actual key upgrades and the exact row resource's
journal membership/removal across savepoint rollback, not total journal size.

Public waiting bodies retain PostgreSQL's real in-flight RowWait observer.
Native instead executes a synchronous, bounded real SQL lock_timeout, asserts
full55P03/nil result/no published prefix, resets the timeout, releases the holder,
and retries the same actual prepared handle (or direct command). This is recovery
proof, not original-candidate restart proof. Fresh native RC retry sees source8
instead of the original retained7; noWiden commit leaves row1=20 and updates
newly matching row3=100/count1, rollback updates both rows/count2. PostgreSQL's
original blocked command retains source7 and excludes newly matching row3:
noWiden commit count0/rows(1,20),(3,10), rollback count1/(1,100),(3,10).
Native RR timeout uses SAVEPOINT/ROLLBACK TO/RELEASE without replacing the
transaction or snapshot; prior own row2=21 survives recovery.

`TestMergeUpdateReadCommittedPending` supplements public retries with actual
manager-pending executor/Finish restart commit/rollback, key change with/without
insert, delete with/without insert, retained source7, and noWiden vectors.
`TestMergeUpdateRepeatableReadPending` covers all update/delete commit/rollback
vectors with prior private UPDATE/DELETE/INSERT, exact count/diagnostic, logical
replacement identity/base and peer reuse. Committed RR update succeeds1:
the pinned old version10 and own descendant100 both remain visible, alongside
prior own writes; committed deletion raises40001 concurrent delete and25P02.
Worker rollback restores peer data, never collapses the RR bag to latest-only.
`TestMergeUpdateFollowChainPending` asserts the original pending NKU request,
then a real rowUpdate wait on surviving KEY SHARE for sameKey/unselected key
arms; the nonkey control completes while KEY SHARE survives. Public native
follow-chain timeout/retry deliberately does not claim this bitmap transition.

Pinned goldens retain original candidate WHEN/RHS frames, actual initial
key-change modes versus syntactic follow-chain union, whole UPDATE-arm supplied
column disclosure and exact source-grounded diagnostics/positions.
Privileges/HeldACL use genuine GRANT/REVOKE in both backends; there is no native
GRANT skip. Existing native system-column-provider and ordinary UPDATE DEFAULT
boundaries remain explicit divergent SQL expectations.

`MixedDisclosure` independently checks the cross-action supplied-column union
with restricted actors: matched NULL assignment retains secret99 from an
unexecuted INSERT destination, and INSERT DEFAULT99 retains disclosure from an
unexecuted UPDATE destination. `RepeatableReadFollowup` performs a second MERGE
over the old10/own100 bag with unconditional, original10-only and own100-only
conditions, checking cardinality/count, exact bag and savepoint recovery.
The focused pinned oracle and subsequent dual-mode proof establish these
diagnostic and prior-own-current-head outcomes; they are not pending proposals.

Controls exercise actual matched replacements and latest live-head Dump,
Reset/Restore, retained old rows, same-DSN peer values, failed/canceled replacement,
held reexecution and actual emitted SQL replay on PostgreSQL. PostgreSQL is never
given an invented native control API. Native exported MERGE affected counts are
exact; lib/pq exports zero for this tag while independently asserted stored rows
prove the complete logical effects. Public feature smoke uses
`^TestReferenceMergeUpdateFeatureSmoke$` in both modes. Process-local logical
transactions/locks do not imply disk/WAL/crash recovery, wire/network service,
physical tuple identifiers/planner/storage, pg_dump or native ABI replication.

### MERGE target-only UPDATE acceptance

The untagged `TestReferenceMergeSourceUpdate` definitions use the existing
direct/prepared fixture, independent typed rows and complete logical diagnostics.
Run these bounded groups in **both** native and pinned PostgreSQL modes: use
`make check RUN='<selector>'` and `make check REFERENCE=1 RUN='<selector>'`,
each with an external900-second deadline:

```text
^TestReferenceMergeSourceUpdate(Membership|Classification|Conditions)$
^TestReferenceMergeSourceUpdate(ThreeClasses|JoinLegality)$
^TestReferenceMergeSourceUpdate(Bindings|Diagnostics|Assignments)$
^TestReferenceMergeSourceUpdate(Analysis|Parameters)$
^TestReferenceMergeSourceUpdate(Cardinality|Constraints|Atomicity)$
^TestReferenceMergeSourceUpdate(Privileges|HeldACL)$
^TestReferenceMergeSourceUpdate(Prepared|CommandMetadata|Transactions)$
^TestReferenceMergeSourceUpdateReadCommitted$
^TestReferenceMergeSourceUpdateRepeatableRead$
^TestReferenceMergeSourceUpdate(Cancellation|ServerCancellation)$
^TestReferenceMergeSourceUpdate(Controls|FeatureSmoke)$
```

The obsolete BY SOURCE UPDATE0A000 insertion-boundary expectation is migrated
to a direct/prepared positive `TestReferenceMergeInsertBoundaries/bySourceUpdate`:
unmatched target1 becomes3/count1 natively (lib/pq0), with exact stored rows.
Run the full changed `^TestReferenceMergeInsertBoundaries$` in both modes.
Hidden-source diagnostics use the existing FROM manifest once, retaining
singular inaccessible-column Detail/no Hint for UPDATE and integrated DELETE.
The RR pending helper distinguishes whole-transaction discard from savepoint
ancestor retention; the target-only lane compares exact prior overlays before
recovery. Native smoke peer reads use the existing instrumented Memory pool and
its recorder, while actual Dump/Reset/Restore continue through Native controls.

Privileges checks UPDATE-only authority without INSERT, explicit target reads,
false-arm destination rights and empty-source INSERT authority. HeldACL revokes
and restores target UPDATE/source SELECT on the same actual handle. Controls
shares the established persistence fixture with matched UPDATE, but executes real
BY SOURCE replacements with identity/default/generated objects and a source view,
latest-head Dump/Reset/Restore, peer reads, canonical redump, retained old rows,
failed/canceled replacement, held epoch rebind and actual PostgreSQL script replay.
The changed incumbent Controls and ServerCancellation definitions also require
both modes, with `^TestReferenceMergeUpdate(Controls|ServerCancellation)$`.

Private actual executor proofs supplement public native fresh-command recovery:

```sh
make check RUN='^TestMergeSourceUpdate(Syntax|QueryVisitor)$'
make check RUN='^TestMergeSourceUpdate(SourceCompletion|Capture|Publication|CorrelatedArrayBound)$'
make check RUN='^TestMergeSourceUpdate(Pending|RowModes|Cancellation)$'
make check RACE=1 RUN='^TestMergeSourceUpdate(Pending|RowModes|Publication|Cancellation)$'
```

Pending establishes exact live manager requests before holder commit/rollback:
target-only class retention/no widening, retained-source matched two-action
conversion, late INSERT fault rollback and release18.0 typed-NULL deletion
fallback/default/constraint cases. Its RR lane retains prior private work and
snapshot roots, follows committed updates without generic40001, checks old+own
cardinality and condition restart, and recovers concurrent-delete40001/25P02
through a savepoint. RowModes asserts initial actual-key upgrades, follow-chain
syntactic-key union and exact resource journal removal. Cancellation includes
actual pending retirement and cancel-at-Finish; SourceCompletion covers empty
source Next/Close faults and cancel-at-Close before action locks. Capture preserves
distinct equal targets and the coherent source/target frontier. Publication keeps
all replacements private until the sole Finish and retains old row payloads.
Changed incumbent private fixtures retain their matched scenarios; include the
existing SourceCompletion/SyntacticRowModes/Cancellation/RepeatableReadPending/
FollowChainPending/MixedPendingCancellation selectors in regression proof.

Pinned18.0's exact correlated scalar array assignment bound
`a[(SELECT t.id)]=t.a[1],a[1]=t.a[t.id]` returned EOF and backend recovery
(historical reports10/20/22), not a PostgreSQL diagnostic or passing SQL contract.
Finalized public Assignments keeps direct target bounds and an independent
`(SELECT 2)` bound, alongside scalar RHS/generated/original-frame witnesses;
reports23 established those direct/prepared pinned expectations. The exact
correlated-bound `{20,10,30}` swap is tested separately by the actual native
executor: safety proof, **not** fabricated PostgreSQL parity or a conditional skip.

Standalone feature smoke source is an ephemeral OMP session artifact, executed
as `go run /tmp/stuntgres-merge-source-smoke/main.go` from the worktree. Expected
exit0/empty output: ordered target-only updates leave matched1 untouched, a held
parameter updates two targets, late22012 publishes nothing, command Query has
empty names/types/rows, and Dump/Reset/Restore plus held reexecution preserves
peer/canonical results. Root runs generation/quickcheck only after all edits;
selected dual/race/smoke observations precede review, finalcheck follows integration.


INSERT SELECT is included above because INSERT input binding is shared.

### MERGE matched and BY SOURCE DELETE

Shared `reference/dml/merge_delete_*_test.go` definitions specify independent
rows/types, native counts versus pq MERGE count0, complete diagnostics and actual
direct/prepared execution. The root pins new goldens before the corresponding
production change and records native feature-red. Run each selected group in
both modes, with a separate external900-second deadline:

```sh
make check RUN='^TestReferenceMergeDelete(Matching|Empty|Conditions)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(Matching|Empty|Conditions)$'
make check RUN='^TestReferenceMergeDelete(Cardinality|OraclePrecedence|Atomicity|UniquePrefixes|Constraints|ExclusionPrefixes)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(Cardinality|OraclePrecedence|Atomicity|UniquePrefixes|Constraints|ExclusionPrefixes)$'
make check RUN='^TestReferenceMergeDelete(Namespaces|Diagnostics|FullJoinCapabilities|FullJoinCarriers)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(Namespaces|Diagnostics|FullJoinCapabilities|FullJoinCarriers)$'
make check RUN='^TestReferenceMergeDelete(Privileges|HeldACL|ReadOnly)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(Privileges|HeldACL|ReadOnly)$'
make check RUN='^TestReferenceMergeDelete(Prepared|CommandMetadata|Transactions|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(Prepared|CommandMetadata|Transactions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceMergeDelete(ServerCancellation|ConcurrentClasses|RepeatableReadFollowup)$'
make check REFERENCE=1 RUN='^TestReferenceMergeDelete(ServerCancellation|ConcurrentClasses|RepeatableReadFollowup)$'
make check RUN='^TestMergeDelete(Syntax|QueryVisitor|SourceCompletion|Lineage|Publication|ConcurrentClasses|MixedPendingCancellation|RowLocks|RestartRowLocks|RepeatableReadLineage)$'
make check RACE=1 RUN='^TestMergeDelete(ConcurrentClasses|MixedPendingCancellation|Publication|RowLocks|RestartRowLocks|RepeatableReadLineage)$'
make check RUN='^TestMergeDeleteDeletionBaseAuthority$'
make check RUN='^TestDeleteCore(Visibility|CombinedCommit|CombinedPreflight|StageShapeAuthority)$'
make check RUN='^TestReferenceDeleteCoreVisibility$'
make check REFERENCE=1 RUN='^TestReferenceDeleteCoreVisibility$'
make check RUN='^TestReferenceMergeInsertBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceMergeInsertBoundaries$'
```

Namespaces retain inaccessible-source Detail/Hint and character positions under
UTF8 prefixes. Source ORDER BY is not itself an equality-join output-order
guarantee: ordered unique/exclusion retirement witnesses use a non-equality
conjunction to retain source-driven candidates. Real reverse immediate failures
must not be replaced by final-bag validation. The FULL-join witnesses admit
constant TRUE/FALSE/NULL and equality, but reject `<` even on empty table data.
FullJoinCarriers independently asserts constant/parameter integer/TEXT/NULL and
domain truth with both missing-side families on direct/prepared routes. Nonzero
integers and every non-NULL TEXT (including empty, `'0'`, `'false'`) match; zero/
NULL select BY SOURCE deletion plus BY TARGET insertion. Ordinary Boolean
JOIN/WHEN typing and analyzed ON dependencies remain unchanged.
Second demanded projection/constraint errors precede cardinality where pinned;
duplicate DELETE and mixed second modifications retain exact21000 and its Hint.

ServerCancellation independently captures PostgreSQL ctid before contention and
asserts the deleting-tuple Context; native has no fabricated physical Context.
Public native timeout then fresh execution proves recovery only. PostgreSQL's
real RowWait and native exact manager-pending executor units separately prove
initial MATCHED losing ON, retained source and initial BY SOURCE staying its
class. Actual-executor units also cover required source completion before row
grants, own lineage, strong DELETE versus KEY SHARE, cancellation before Finish
and exact resource retirement. Controls uses genuine Dump/Reset/Restore, canonical
survivor redump, peers, a restored fresh-driver clone, retained old rows and actual
emitted SQL replay; PostgreSQL has no invented native control API.
RestartRowLocks observes the exact original NKU request, then a real rowUpdate
wait against surviving KEY SHARE before an initial UPDATE restarts as MATCHED or
BY SOURCE DELETE. Initial DELETE already owns that strong grant before following
a predecessor; neither the follow-chain nor action append reacquires it.
The pinned RR followup extends the prerequisite old10/own100 bag to deletion:
unconditional DELETE raises21000, original10-only recheck affects0, and
own100-only deletion affects1 while retaining original10 plus ancestor-private
row2/21. Canonical private deletion carries the actual version/replacement base,
not a RowID-wide snapshot mask.

The untagged `TestReferenceDeleteCoreVisibility` is the sole migrated visibility
definition. It preserves the original held prepared repeat/count/duplicate-bag/
commit/rollback assertions and adds both actual direct/prepared prior-own INSERT
and same-base UPDATE deletion witnesses: immediate private survivors, repeat0,
unchanged peer, savepoint restoration and full rollback. Other legacy
reference-tagged DeleteCore definitions are not Docker-free proof merely because
a native RUN selector mentions their declarations.

Feature smoke selects `^TestReferenceMergeDeleteFeatureSmoke$` in both modes.
The standalone public program is supplied as `local://merge-delete-smoke.go`,
materialized outside the checkout by the root. From the checkout run
`timeout 900s go run "$SESSION_TMP/merge-delete-smoke.go"`: quiet exit0 requires
mixed count4/survivors(2,21),(4,40), held false/NULL/true deletion, atomic duplicate
delete21000, private/peer rollback, empty command metadata, retained old rows,
and genuine canonical control restoration. This does not claim WAL/crash
durability, physical identifiers/providers, wire protocol or pg_dump equivalence.

### Encoding, locale and float-presentation settings

The eleven shared definitions live in `reference/patterns_encoding/format_encoding_*_test.go`.
Their completed selector must run in both modes before review/integration,
under separate 900-second deadlines, without verbose output:

```sh
timeout 900s make check RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$'
```

For changed locale numeric-parsing witnesses, validate the existing nested
`to_char(to_number(...),'FM9999.00')` TEXT result and raw NUMERIC decimal codec
against the pinned oracle first; do not admit general Numeric-to-TEXT just to
make a selected-setting witness executable:

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Locales|Consumers)$'
timeout 900s make check RUN='^TestReferenceFormatEncoding(Locales|Consumers)$'
timeout 900s make check RUN='^(TestFormatEncoding.*|TestExplicitCoercionMatrix|TestStringPlanSpecializedOutputStability|TestSettingUtilitySnapshotOwnership|TestArrayVolatilityPreservesScalarClassification|TestStringGenerationOutputDependence)$'
timeout 900s make check RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$'
timeout 900s make check REFERENCE=1 RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$'
```

The executable feature/control smoke is in `format_encoding_controls_test.go`:

```sh
timeout 900s make check RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'
```

It asserts UTF8 Unicode transport, C numeric/time masks, current-role message
authority, transactional precision/restoration, held and retained results, and
native exact binary/Dump/Reset/Restore/emitted replay controls. PostgreSQL's
rounded scalar transport and native's exact scalar export are independently
asserted, not normalized into false equality. Native locale/client-encoding
capability errors have explicit backend expectations; ICU is policy only.
Ordinary quickcheck and the single post-integration 1800-second finalcheck remain
required. Commands above are acceptance instructions, not claims they passed.

### JSONPath typed-input reference contracts

The JSONPath family uses cohesive, independently expected case tables for
canonical grammar output, input-time regular-expression syntax, complete logical
diagnostics and their direct/prepared literal-versus-bind phases. Consumer
scenarios cover held inferred INSERT/UPDATE/CAST parameters, repeated slots,
owned bytes, SQL NULL versus path `null`, recovery and failed-write atomicity.
Character IO asserts TEXT/VARCHAR canonical output, explicit VARCHAR/BPCHAR
truncation, multibyte character sizing, BPCHAR padding and SQL NULL. Encoding
errors assert the full protocol diagnostic, including portal Context without a
parameter value; malformed paths assert the measured slot-specific Context.

Catalog OIDs are checked separately from driver metadata: lib/pq exposes blank
type names and interface{} ScanType for JSONPath results, whereas native results
must expose JSONPATH (or _JSONPATH) and string ScanType. The shared helper asserts
each backend's metadata and raw representation before normalization. Composition
also retains native-only Dump/Restore phases and emitted-SQL replay on PostgreSQL.
These are input/representation contracts, not path or regex evaluation claims.

JSONPath fixture IDs are unconstrained INT values made unique by the scenarios;
these tests do not exercise primary-key syntax or constraint enforcement.
Scalar/array catalog and attribute identities use separate equality-filtered
queries on PostgreSQL, retaining every independently expected OID, typmod,
collation and domain-base field without relying on unsupported IN expressions.
Native explicitly asserts that the two named relations `pg_catalog.pg_type` and
`pg_catalog.pg_attribute` remain absent (42P01, exact English message and original
relation Position), in both direct and prepared representation setup. Other
implemented catalog views do not imply these inquiry relations exist. Exact native
descriptor4072/4073 and reciprocal array identities remain covered by provider
units; all shared native JSONPATH/_JSONPATH metadata, IO, composition and controls
remain asserted without a skip. Equality, ordering and grouping
must reject rather than compare canonical output; grouping diagnostics retain
the declared scalar, array or domain name and original SQL source position.

Run all changed definitions in both modes, with a 900-second deadline per command:

```sh
make check RUN='^TestReferenceJSONPath'
make check REFERENCE=1 RUN='^TestReferenceJSONPath'
make check RUN='^TestReferenceJSONPathFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'
```

The feature smoke expects inferred repeated-slot insertion, strict indexed-path
and regex-flag canonicalization, exact regex diagnostics, unchanged data after
bad UPDATE/late bad INSERT, subsequent valid recovery, and distinct NULL/path-null
rows. No discovery filter, success logging or observation fallback is retained.
Change note: JSONPath oracle-discovery envelopes are replaced with quiet shared
reference assertions; quick-mode failure precedes production implementation.
The collation-object family covers independent C/builtin identity, provider
metadata and execution modes, validation, version notices, authenticated
ownership, rename/schema/dependency transitions, and native persistence with
PostgreSQL emitted-SQL replay. External ICU/libc data boundaries are explicit;
native never substitutes C for an unavailable provider. Run every changed
definition in both modes, each under a 900-second deadline:

```sh
make check RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
make check REFERENCE=1 RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
```

The cohesive feature selector is
`RUN='^TestReferenceCollation(Identity|Provider|Version|SeedAlterLifecycle|SeedTemporaryLifecycle|SeedNativeReplay|Lifecycle|Dependencies|NativeLifecycle|NativeCollision|CompositeNativeCollision|TemporaryLifecycle|TemporaryQualification|TemporaryRetainedConsumers|SimilarCopiedUnicode|ExternalBoundary)$'`.
Expected observations include independent object OIDs, real ASCII/Simple/Full
case behavior, NULL versus version `1`, stable permanent moves and mutable pinned
seed metadata, actual owner relationships without a pg_roles fixture dependency,
seed delta Dump/Reset/Restore and actual PostgreSQL emitted replay, column-cascade
surviving data, and repeated independent native Restore. TEMP witnesses cover
fresh creation, same-role/other-role foreign qualification42501, physical owner
end's atomic durable cascade, into/out-of-TEMP moves0A000 without mutation, portable
omission or fail-before-output, and actual import/target OID collisions across all
provider usages. Native control phases remain explicitly native-only.
Provider encoding is independently checked across actual PostgreSQL replay:
direct builtin `C` uses-1, while direct `C.UTF-8`/`PG_UNICODE_FAST` use UTF8 encoding6.
FROM copies retain source encoding; libc `C`/`POSIX` copies use-1 versus direct
libc options6, and copied builtin `C` from `ucs_basic` uses6 versus direct builtin
`C`-1. Only exceptional copies need immutable bootstrap FROM reconstruction.

The copied-provider SQL-regex consumer witness independently distinguishes
ASCII, C.UTF-8 and PG_UNICODE_FAST alphabetic, decimal and punctuation classes,
with source/pattern explicit COLLATE, direct/prepared context and non-context
routes, BOOL metadata and implicit copied identities in stored views. Run:

```sh
make check RUN='^TestReferenceCollationSimilarCopiedUnicode$'
make check REFERENCE=1 RUN='^TestReferenceCollationSimilarCopiedUnicode$'
```

Change note: SIMILAR class dispatch now follows the registered immutable provider
capability rather than bootstrap object OIDs; seed class semantics are unchanged.

The TEMP lifecycle witness also retains a real TEMP table, SERIAL-owned sequence
and index across native Reset/Restore, then asserts their physical-end cleanup
alongside the durable dependent-column projection. DEFAULT, CHECK and generated
provider manifests retain genuine collation dependencies through both private
validation and final publication fences. Native absent `pg_catalog."C.utf8"`
lookup is42704; explicitly requesting its unavailable libc provider remains0A000,
while the PostgreSQL witness uses its installed libc locale without substitution.

Change note: retained TEMP DEFAULT, CHECK and generated manifests preserve their
lawful captured collation references across repeated Reset/Restore. Older epochs
are admitted only for the exact live physical-owner consumer and its same-owner
TEMP provider, or an actual validated pinned seed, with exact class/SubID and
matching immutable capability. Subsequent DDL, default INSERT, prepared UPDATE,
failed CHECK atomicity, dependency identity and physical cleanup are exercised by
`TestReferenceCollationTemporaryRetainedConsumers`. After each catalog replacement,
ALTER of a separate seed default republishes that exact retained relation; new
inserts observe the changed default while the other explicit-COLLATE definitions
and their captured provider manifests survive unchanged.

`TestReferenceCollationCompositeNativeCollision` covers existing supported
composite member collations, composite domains/defaults/data and stored views:
Dump uses qualified live provider names, genuine target TEMP collisions remap
member capabilities over repeated native Restore, and emitted SQL is replayed
against PostgreSQL. Composite-array column admission remains unchanged and is
not claimed by this witness.

Stored scalar metadata no longer retains binding catalog roots. Name resolution
uses the actual ephemeral binding or private-candidate retag context; pure
reference/slot remapping preserves already validated provider metadata. Genuine
package regressions are `TestCollationRetainedProviderProvenance` (foreign,
stale/missing, class/SubID, physical owner and capability negatives plus full
DEFAULT/CHECK/generated serializer manifests, including captured epochs) and
`TestCollationStoredExpressionMetadataRetention` (repeated
real view/default replacement and legitimate held old definitions).

The generalized ARRAY constructor and subscript binders use that same actual
binding context; their existing retag visitor forwards the private candidate
and provider remapper. The obsolete REGCLASS-only array visitor is removed,
not retained as a second array authority. Shared ordinary/materialized-view
freezing captures collation identities once alongside the other definition facts.

Change note: array-aggregate providers remap their complete result Usage,
including the registered collation capability, alongside their argument type;
the copied provider never changes retained source authority.
`TestArrayAggregateCollationViewRemap` compares complete provider/schema Usage
for genuine scalar/array Full-to-ASCII capability mapping and source immutability.
`TestReferenceCollationNativeCollision` independently checks collated stored
`array_agg` rows/metadata, propagated subscript case behavior, repeated genuine
TEMP-collision Restore, source isolation and emitted-SQL PostgreSQL replay.

Change note: explicit COLLATE serialization uses the common captured-aware
provider record only; it must not append a second current-epoch reference for an
older lawful retained definition. Provider validation reads structural catalog
snapshots only. Owner/ACL validation remains at the synchronized shared stage and
final publication fences, rather than rereading mutable roles inside the provider.
`TestCollationProviderConcurrentRolePublication` overlaps genuine session role
publication with snapshot provider validation. Its race gate is:

```sh
make check RACE=1 RUN='^TestCollationProviderConcurrentRolePublication$'
```

The already integrated `TestReferenceLikeLegacyMigration` and its stage/namespace
scenarios remain the single owner of the former LIKE smoke assertion union; the
duplicate collation-named migration is removed. Required final selective gates
also explicitly include the three changed non-collation-named scenarios:

```sh
make check RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
make check REFERENCE=1 RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
```

Each invocation has a 900-second deadline. Complete-suite passes do not replace
these selective dual-mode gates.

Owned TEMP targets are excluded from the private stage before projecting surviving
dependents, so a dropped TEMP table never also publishes a replacement row shape.

Supplemental genuine package regressions include
`TestCollationBootstrapPrincipalNameReuse`, `TestCollationPrivateSessionIdentity`
and `TestCollationTemporaryCleanup(AX|CompletenessFence|Exhaustion)`. The baseline
has no public role-rename SQL: the name-reuse fixture clones and coherently
publishes the actual role catalog under existing live authority, retaining real
identities/attributes/memberships, then creates the replacement through real DDL.
It exercises real metadata capture, owner denial, Reset/Restore and private cleanup;
it does not bypass owners/pins or advertise ALTER ROLE support.

Table definition-copy coverage uses
`RUN='^TestReference(Table(Like|Core)|SchemaTableIntegration|IdentitySerialAndNativeBoundaries|NotNullExternalInheritance)'`
in both modes; feature smoke uses `RUN='^TestReferenceTableLikeFeatureSmoke$'`.
Oracle-first discovery fixes complete diagnostics/index-name expectations before
the native failing regression. Actual native Dump output is replayed on pinned
PostgreSQL with independent expected rows and future writes, not SQL-substring or
no-LIKE assertions. PostgreSQL-only structural inquiries for absent native catalog
sources/columns are paired with native descriptor proofs and shared pg_indexes,
pg_sequence/pg_depend inquiries and actual enforcement. Table/column GRANT/REVOKE
is now a shared public authority control; package-function ACL units supplement
those witnesses without duplicating public SQL tests outside reference.
Predicate-only `pg_depend` projection remains outside the bounded native catalog:
mixed key/INCLUDE/predicate inquiries independently expect native attributes2,3
versus PostgreSQL2,3,4, and predicate-only external counts0 versus1. Exact native
analyzed/execution manifests and shared real DROP/column lifecycle/controls prove
the underlying predicate dependencies; this inquiry boundary never skips a scenario.
The partial-copy subfamily is `RUN='^TestReferenceTableLikePartial'`; run
`make check RUN='^TestReferenceTableLikePartial'`
and `make check REFERENCE=1 RUN='^TestReferenceTableLikePartial'`.
Private cooked-tree, stable/dense attribute, fresh membership-root and actual
provider-admission witnesses use
`make check RUN='^TestTableLikePartial'`.
The expanded existing feature smoke includes actual partial-index Dump/Restore
and PostgreSQL replay with future membership, uniqueness and predicate-first
errors. The complete shared union also retains CoreNativeControlRoundTrip's
standalone typed/direct and quoted/prepared MAXINT8/O'Brien/TRUE rows and metadata
after actual Restore, plus CoreSemanticSkipNotices' quoted unknown `"integer"`
IF NOT EXISTS notice and unchanged-data witnesses. Each selected check invocation
has its own 900-second deadline.
TEMP composition uses `RUN='^TestReferenceTableLikeTemporary'` in both modes and
`RUN='^TestTableLikeTemporary'` for native engine units. Shared SQL covers
same-session cooked defaults, constant/permanent-sequence defaults from TEMP
sources, DEFAULT mask precedence, fresh destination-owned identity sequences,
and exact native0A000/PG-success permanent TEMP-provider differential. Native
control phases exercise actual Reset/Restore, retained rows/provider identity,
new copies from retained sources, failed/canceled replacement and actual permanent
stream replay on PostgreSQL. These phases have no PostgreSQL control API substitute.
The existing feature smoke includes both TEMP default and identity lifecycles.

Composite LIKE sources use `RUN='^TestReferenceTableLikeComposite'`, with ordinary
and pinned reference gates below. Plain/ALL/per-LIKE masks, nullable fields,
interleaved type usage, empty composites, source-type removal through the existing
dedicated-schema `DROP SCHEMA ... CASCADE` route, duplicate atomicity and actual
independent-table controls/replay share the existing fixtures. Native DROP TYPE
syntax is not expanded; all public copies must survive actual source retirement.
PostgreSQL GRANT/REVOKE setup verifies type USAGE rather than backing-relation
SELECT; `TestTableLikeCompositeSourceACL` exercises that distinction through the
native ACL seam, including namespace-first errors and reciprocal source captures.
Pinned PostgreSQL composite discovery expectations are encoded; the temporary
scenario-local backend filter is removed before the native regression.

```sh
make check RUN='^TestReferenceTableLikeComposite'
make check REFERENCE=1 RUN='^TestReferenceTableLikeComposite'
make check RUN='^TestTableLikeComposite'
```

The retained row-expression regression family is
`RUN='^TestReferenceTableLikeTemporaryRetained'`. Its SQL witnesses cover
GENERATED, CHECK, expression keys and partial predicates copied into permanent
and same-owner TEMP destinations, and a separate same-owner TEMP external-literal
fixture. Native phases perform actual persistent replacement before CREATE and
again after source removal: restored permanent and retained TEMP copies must
preserve full rows/metadata and future generation, CHECK and partial-index
enforcement even without Docker. Optional PostgreSQL replay consumes that actual
permanent Dump stream; it does not replace the second native Reset/Restore.
PostgreSQL exercises shared SQL without a fabricated control API.
`RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'` supplies descriptor
authority and real lock-manager contention witnesses: local row/tableoid refs
receive current destination authority, external stored references remain old,
and queued admission must preserve those manifests while canceling, rolling back,
publishing or rejecting a dead/recreated/same-address revised provider.
`TestTableLikeDefinitionDependencyOrder` additionally exercises the common DROP
dependency formatter through ordinary CREATE, not a LIKE special case: an earlier
view, generated column, two CHECKs whose definition-OID order opposes declaration
order, and a later index retain the exact report schedule. The shared retained
literal fixture asserts the full six-line PostgreSQL dependency diagnostic.
Pinned PostgreSQL discovery expectations are encoded and the scenario-local
backend filters removed. The native-red and final dual-mode gates use the
complete scenarios. Each invocation below has a900-second deadline:

```sh
make check RUN='^TestReferenceTableLikeTemporaryRetained'
make check REFERENCE=1 RUN='^TestReferenceTableLikeTemporaryRetained'
make check RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'
make check RACE=1 RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)'
make check RUN='^TestTableLikeDefinitionDependencyOrder'
```


The serial/identity inquiry family is selected with
`RUN='^TestReferenceSerialIdentity(Ownership|Lifecycle|Names|Nulls|Diagnostics|Consumers|Controls|FeatureSmoke|SystemColumns)$'`.
Its shared public scenarios independently assert AUTO/INTERNAL/manual ownership,
ownership versus defaults, qualified-name parsing and literal column names,
strict NULL and TEXT metadata, complete diagnostic fields and binding/execution
phases, symbolic consumers, retained results, and ownership-preserving native
Dump/Restore with actual emitted-SQL replay on PostgreSQL. Native control phases
have no PostgreSQL API equivalent; the SQL witnesses are shared. The cohesive
feature smoke is `RUN='^TestReferenceSerialIdentityFeatureSmoke$'`. Run both
selectors in quick and pinned modes using the commands above.
System-column coverage independently asserts all six existing table system names
return TEXT NULL, while absent system names on views retain complete42703
diagnostics and held recovery. The feature smoke includes both boundaries.

The datetime-truncation family independently asserts canonical fields/aliases,
calendar and BC boundaries, DATE/TIME selection, unrestricted metadata,
interval component preservation, strict NULL, complete diagnostics and held
recovery. Zone witnesses cover New York folds/spring, Sao Paulo midnight gaps,
Lord Howe half-hour transitions, explicit Sydney, POSIX/fixed/dynamic zones.
Configured-abbreviation direct/prepared/held witnesses cross Default/Australia
under UTC/New York: session EST retains05UTC, while Australia UTC EST is14UTC
and CST is14:30UTC on the preceding day. The captured execution environment,
not Prepare-time settings or a global Default table, selects the resolver.
Consumer atomicity, savepoint recovery, stored definitions, compatible Retag,
native destination replacement and actual emitted SQL replay share the bodies.
Fast precanceled INSERT is native-only: lib/pq may finish it before cancellation;
real session cancellation instead uses the fixture's observed admission barrier.
The excluded three-argument naive inputs retain separately asserted native0A000
and PostgreSQL implicit-coercion success, not parity-derived goldens.

Run changed definitions in both modes, each under900seconds:
```sh
make check RUN='^TestReferenceDatetimeTrunc'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTrunc'
make check RUN='^TestReferenceDatetimeTruncFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTruncFeatureSmoke$'
make check RUN='^TestDatetimeTrunc'
make check RUN='^TestDatetimeTrunc'
make check RUN='^TestDatetimeTrunc'
```
Smoke expects distinct NY fold hours05/06UTC, spring day05UTC, Sydney day13UTC,
held NULL/error recovery, unchanged late-write sentinel and symbolic stored
default/view/generated/index replay. Full family checks additionally cover
week2006-01-01→2005-12-26, quarter2001-02-16→2001-01-01, negative interval
seconds→-1second and TIME24:00→zero-day24hour INTERVAL. These selected gates
do not replace ordinary quickcheck or post-merge finalcheck.

The date-bin family uses independently specified shared direct/prepared bodies
for both timestamp variants: shifted-origin boundaries, fractional bins,
negative exact multiples, fixed-day DST behavior, mixed/date/unknown resolution,
held session coercion and NULL/import recovery. Complete logical diagnostic
records include exact stage/character Position and empty runtime optional fields.
Overflow witnesses cover the release subtraction/day-product/lower-bound vectors,
with package units distinguishing checked stride addition, component cancellation,
int64 correction underflow and retained END. Metadata asserts unrestricted
computed types and precision-2 inputs yielding an unrounded .119 result.
Stored/default/CHECK/view/generated/index consumers include actual emitted SQL
replay, fresh restored writes, compatible remapping and retained native rows/types.

Run selected proofs with an external900-second deadline per command:
```sh
make check RUN='^TestDatetimeBin'
make check RUN='^TestReferenceDatetimeBin'
make check REFERENCE=1 RUN='^TestReferenceDatetimeBin'
make check RUN='^TestReferenceDatetimeBinFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeBinFeatureSmoke$'
```
The smoke independently expects before-origin23:47:30, first-bin00:02:30,
next-bin00:17:30, negative-exact15:00, typed metadata, full22008 zero-stride
diagnostics, NULL/error/good recovery, failed-later-write preservation and actual
stored control replay. A root-owned ephemeral public Go smoke additionally runs
`timeout 900s go run ./.date-bin-feature-smoke`; its source uses only stdlib/public
stuntgres, and the directory is removed before review. Selected gates do not
replace whole-project quickcheck or post-merge finalcheck.

The transaction-time family uses the same completed public body for all five
providers: direct/held metadata and raw codecs, transaction/statement ownership,
peer/autocommit/CHAIN contexts, demanded wall variation, TimeZone and fixed
timeofday layout, bounded defaults/consumers, complete invalid-call diagnostics,
symbolic replay and native controls. Non-ISO DateStyle witnesses use TEXT-only
timeofday and explicit timestamp casts; raw pq temporal codecs are checked in ISO.
Shared prepared/parameterized tests allow now <= statement time; native one-command
equality and fixed microsecond/formatter outcomes have package-owned units.
The full legacy TestTimestampSQLTransactionChainSmoke union remains in
TestReferenceTimestampSQLTransactionChainClock; the superseded smoke file is removed.

Each selective invocation has its own 900-second deadline:
```sh
make check RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)'
make check REFERENCE=1 RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)'
make check RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$'
make check RUN='^TestTransactionTime'
```


The numeric-formatting family independently asserts the five numeric
`to_char` overloads, preferred FLOAT8 SMALLINT route, `to_number`, exact/binary
rounding, C currency space, global FM, signs, Roman/ordinal/V/scientific
semantics, permissive parsing, complete diagnostics and deferred mask demand.
Metadata, consumers, atomicity, stored definitions and actual native control
replay use the shared fixture. The temporal ownership boundary now expects
numeric success; epoch/TIME/ARRAY boundaries remain unchanged. Negative
numeric setup uses signed numeric string casts, not adjacent unary arithmetic.
The repeated-parameter NUMERIC→TEXT conversion gap keeps independently
specified native 0A000 and PostgreSQL success rather than widening cast scope.
Consumer TEXT composition uses `to_char(to_number(...),'FM99')`, not an
unowned general NUMERIC→TEXT cast. Stored-definition inquiries retain their
exact SQL text and reuse deparse's own backend metadata contract; numeric
computed metadata remains unchanged. Default catalog lookup joins matching
OID columns instead of requiring an adjacent OID=REGCLASS operator. Failed
native Restore reaches an invalid Roman DEFAULT through a scalar INSERT
that omits its column, then asserts unchanged complete catalog/data, retained
cursors and held statements; it does not expand the scalar Restore importer.
The UUID-default regression covers rebased closed-tree admission through both
numeric providers: non-NULL UUID generators make `num_nulls` return0, so
`to_char` and `to_number(format(...))` independently return TEXT/NUMERIC0.
Direct/prepared DEFAULTs, reused held inserts, new DEFAULT VALUES after actual
Dump/Restore and emitted SQL replay retain symbolic execution. The numeric
feature smoke reuses the same scenario body. Package admission units preserve
unrelated volatility and row/parameter/stage rejection.

Run all changed definitions in both modes under separate 900-second deadlines:
```sh
make check RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)'
make check REFERENCE=1 RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)'
make check RUN='^TestReferenceNumericFormattingFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericFormattingFeatureSmoke$'
make check RUN='^TestNumericFormatting'
make check RUN='^Test(NumericFormatting|DatetimeFormattingBindingAuthority)'
make check RUN='^TestNumericFormatting'
```
Smoke independently expects exact BIGINT9007199254740993, PR `<485>`,
Roman `CDLXXXV`, scientific ` 4.86e-04`, and V input
`1234.560000000000000000`, plus C/SMALLINT precision, NULL/error recovery,
late-write atomicity and stored default/view replay. Package units supplement
these public contracts with exhaustive canonical Roman1–3999, V factor-first
underflow, immutable plan/diagnostic ownership, Retag and cancellation.
The smoke also demands numeric/UUID defaults before and after native replacement;
these defaults must return0 rather than fail the closed UUID admission walker.

Binary conversion uses independent pinned18.0 goldens for all128 directed pairs,
all42 identities, format/padding boundaries, complete diagnostics, held statements,
volatility and late-write atomicity. Native lifecycle phases retain readers and
metadata across replacement and failed/canceled imports, and replay actual emitted
SQL on PostgreSQL. Select the whole affected family and smoke in both modes,
each invocation under900 seconds:

```sh
make check RUN='^TestReferenceBinaryConversion'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversion'
make check RUN='^TestReferenceBinaryConversionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversionFeatureSmoke$'
make check RUN='^TestBinary(Conversion|Encoding|Format)'
make check RUN='^TestBinaryConversion'
make check RUN='^TestBinaryConversion'
```

The shared hex/escape cores also serve BYTEA input; run existing
`^TestReferenceBytea` regressions selectively in both modes before review.

Stored-view metadata uses the established native declared nullable contract
`true,true`; pq reports unknown `false,false`. Computed conversion expressions
continue to require unknown nullability. Actual emitted-SQL replay uses the pq
contract explicitly, not metadata captured from the native backend.
Replay objects have bounded deferred cleanup before the next backend body,
including failure exits; the shared scenario resets schemas per test, not backend.

`TestReferenceBinaryConversionActiveCancellation` separately exercises admitted
aggregate encode reads and ordered staged convert writes on a32MiB LATIN1 source.
Native observes the worker's actual provider stack before canceling its context;
PostgreSQL observes the actual query active on the held backend PID and uses
server cancellation, preserving the same session. Workers have bounded joins on
every exit. Assertions require no successful cursor/result, unchanged sentinel,
ordinary native context cancellation or the complete PostgreSQL57014 diagnostic,
then independently expected read sum4 and write count2/UTF8 bytes through the
same pre-held read/write statements. No pg_sleep substitute or production delay
is used. Existing precanceled/expired contexts remain separate coverage.

```sh
make check RUN='^TestReferenceBinaryConversionActiveCancellation$'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversionActiveCancellation$'
```

The no-bind direct repeated-parameter witness asserts an explicit inherited
transport boundary (PG42P02 Position16 versus native42P08 Position19/Detail
`bytea versus name`); genuine Prepare remains shared42P08. See DIALECT's binary
conversion note. Codec diagnostics and all independent pair/identity goldens
remain unchanged.


The integer-base/TEXT-MD5 family asserts independent 32/64-bit two's-complement
vectors, RFC/UTF8 digest constants, unknown-TEXT versus known-BYTEA selection,
held parameter recovery and retained TEXT metadata, complete diagnostics,
consumer/write atomicity, symbolic stored widths and MD5 through native controls
with actual emitted SQL replay, and public cancellation. Run the complete
affected family in both modes before review, each under the 900-second deadline:

```sh
make check RUN='^TestReference(StringNumericBase|BinaryHash)'
make check REFERENCE=1 RUN='^TestReference(StringNumericBase|BinaryHash)'
make check RUN='^TestReferenceStringNumericBaseFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceStringNumericBaseFeatureSmoke$'
make check RUN='^Test(StringNumericBase|BinaryHash)'
make check RUN='^Test(StringNumericBase|BinaryHash)'
make check RUN='^TestStringNumericBase'
```

The smoke expects `fffffb2e` versus `fffffffffffffb2e` for INTEGER/BIGINT -1234,
`0` for zero, RFCabc/empty MD5, typed NULL, exact ambiguity/missing/input
diagnostics, no partial publication, and successful held-call recovery. Typed
nil `[]byte` inferred as TEXT intentionally retains native empty-input MD5 versus
pq NULL; do not erase this inherited adapter difference or use Go kinds for
signature selection. These selectors do not replace ordinary quickcheck or
post-merge finalcheck.

Size parsing/formatting coverage uses the untagged `TestReferenceSize` family:
independent exact binary/fractional/signed-limit vectors, every promotion
boundary, numeric scale/specials, full diagnostics and stages, actual held
statements and recovery, NULL-only/empty raw metadata, expression consumers and
write/transaction atomicity. Stored overloads use compact/pretty semantic deparse
replay rather than incidental formatting goldens. Native controls preserve
retained rows/metadata and failed replacement atomicity; pinned PostgreSQL
consumes the actual native-emitted Dump SQL. Every changed definition must run
in both modes before review, each within the 900-second deadline:

```sh
make check RUN='^TestReferenceSize'
make check REFERENCE=1 RUN='^TestReferenceSize'
make check RUN='^TestReferenceSizeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSizeFeatureSmoke$'
make check RUN='^Test(Size|Math|Numeric)'
make check RUN='^TestSize'
```

The smoke expects 1048576 INT8, `1024 bytes`, `-11 kB`, `10.5000 bytes`,
beyond-BIGINT `10240 PB`, typed TEXT NULL metadata, exact malformed-unit and
overflow records, held recovery, no failed-write prefix, and faithful stored
overloads/control replay. The shared MathPlan and numeric codec seams also
require existing math/numeric regression coverage; selected checks do not
replace ordinary quickcheck or post-merge finalcheck.

Size consumer witnesses retain the explicit native internal-CHAR coercion
boundary alongside independent PostgreSQL results; shared COUNT and SUM
arguments, INSERT affected counts/state reads and UPDATE RETURNING prove the
supported composition paths. Deparse catalog-query metadata independently checks
native known-nullable TEXT versus PostgreSQL unknown nullability, without changing
size-result metadata. Retained descriptors are captured before cursor exhaustion
and remain asserted after replacement/consumption.

DELETE USING coverage lives in the untagged `TestReferenceDeleteUsing` family:
physical occurrence/count/output deduplication, actual joined producers, alias/self
scopes, diagnostics, held parameters/source metadata, privileges, atomicity,
contention/isolation/cancellation and committed-state/native retained-output controls.
`MigratedCore`, `MigratedExpressions`, `MigratedReturning`,
`MigratedReturningControls`, `MigratedSequenceBoundary` and
`MigratedSequenceComposition` preserve the superseded public smoke assertion/route
union. Native no-USING qualified-cast and eager decisive-FALSE sequence demand
boundaries are explicit, not adjacent compatibility fixes. Held-dependency
invalidation asserts shared missing-name/no-draw behavior with independent state
expectations and restores the fixture search path on every exit.
Native ACL mutation/queue authority units supplement explicit PostgreSQL ACL phases.
Fast pre-cancelled DELETE calls are a native adapter boundary: lib/pq may complete
them before cancellation is reported. Shared PostgreSQL cancellation evidence comes
from `TestReferenceDeleteUsingWaitTimeout` at actual admitted target/source waits,
with complete diagnostics, no partial effects and explicit transaction recovery.
Sequence USING rejection requires the Execute stage, including a genuine held
Prepare followed by target/source/sequence no-effect assertions before execution.
Index-source analysis diagnostics remain separate; neither policy changes ordinary
SELECT/UPDATE source semantics. Internal admission/contender workers register
cancellation and a five-second, exactly-once outcome/exit join before queue
assertions; failure cleanup closes any retained result before session teardown.

```sh
make check RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms'
make check REFERENCE=1 RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms'
make check RUN='^TestReferenceDeleteUsingSmoke'
make check REFERENCE=1 RUN='^TestReferenceDeleteUsingSmoke'
```

Each selective invocation has a900-second deadline. Real oracle evidence precedes
native implementation; final verification uses the same completed bodies in both
modes. Internal `TestDeleteUsing` parser/engine tests exercise positioned traversal,
captured identity/authority, successful-only suppression and failure/cleanup seams.

The timezone/calendar function family independently covers all nine timezone
and four date_add/date_subtract signatures, operator counterparts, inference,
strict demand, metadata, DST/calendar stages, released displacement extrema,
stored definitions and native control atomicity. Complete original timezone
smoke assertions now live in `TestReferenceDatetimeZoneMigratedTimezoneSmoke`;
existing `TestReferenceTimezone*` fixtures remain intact. Selected live
cancellation uses a finite row-dependent date_add INSERT SELECT, gated by the
actual PostgreSQL query/PID and the native provider/worker synchronization
pattern. Each direct/prepared subcase cleans its fixtures even on failure.

```sh
make check RUN='^TestReference(DatetimeZone|Timezone)'
make check REFERENCE=1 RUN='^TestReference(DatetimeZone|Timezone)'
make check RUN='^TestReferenceDatetimeZoneFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeZoneFeatureSmoke$'
make check RUN='^TestDatetimeZone'
make check RUN='^TestDatetimeZone'
make check RUN='^TestDatetimeZone'
```

Both complete reference modes must run before review/integration. Each selected check
has a900-second deadline. Provider units include warmed EST/MSK/IANA allocation
witnesses; they supplement, not replace, public independent SQL expectations.
The discovery backend.PG filter and focused logging-only oracle probes are
temporary and must be removed before native checks; neither remains in the
completed family. The root runs verification and feature smoke after edits, then
the final post-merge finalcheck under one1800-second deadline.

The interval-justification family covers exact30-day/24-hour boundaries,
mixed signs and component extrema, complete diagnostics and phases, metadata,
held inferred parameters/recovery, four styles, existing consumers, statement
atomicity and native Dump/Restore with actual emitted SQL replay. Precanceled
ordinary context errors are a native-only API boundary, not a race-prone fast pq
request assertion. Run the same complete definitions in both modes:

```sh
make check RUN='^TestReferenceDatetimeJustify'
make check REFERENCE=1 RUN='^TestReferenceDatetimeJustify'
make check RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeJustifyFeatureSmoke$'
```

Each invocation has a900-second deadline. Supplemental units use
` RUN='^TestDatetimeJustify'`,
` RUN='^TestDatetimeJustify'` and
` RUN='^TestDatetimeJustify'`; neither these nor selected
smoke replace ordinary quickcheck or the post-merge1800-second finalcheck.

### Shared-comment catalog coverage

The untagged `TestReferencePGShDescription` family independently asserts the
three-column relation2396 contract, shared ROLE comment identities/contents,
local catalog separation, ordinary readability, full diagnostics, transactions,
held freshness, detached rows/metadata and stored-view/composition behavior.
Role IDs come from a separately owned helper relation's `pg_class.relowner`;
schema IDs come from that relation's `relnamespace`, not an adjacent catalog.
Native bootstrap has no seeded shared comments, while PostgreSQL can have
legitimate global rows: scenarios filter exact object/class pairs, never assert
whole-cluster row-count parity. Native-only Reset/Dump/Restore phases preserve
shared roles outside local replay and feed actual emitted local SQL to PostgreSQL.
Pure `TestPGShDescription` units cover descriptor/PUBLIC ACL, uint32 maximum and
uint64 non-aliasing, detached captures, inquiry agreement, saved-scope nested
opening and entry/in-loop cancellation without private public-SQL duplicates.

Run the same completed definitions in both modes, each within900 seconds:

```sh
make check RUN='^TestReferencePGShDescription'
make check REFERENCE=1 RUN='^TestReferencePGShDescription'
make check RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check RUN='^TestPGShDescription'
```

### Period overlap reference coverage

The shared `TestReferenceDatetimeOverlaps*` family independently covers all13
signatures, half-open/instant boundaries, reversed endpoints, the16-mask NULL
matrix, local clock durations, DATE preferred selection and DST, actual held
parameters, full diagnostics/metadata, query/stored consumers and native controls.
`ParameterDiagnostics` pins bound/imported constant wrapper errors;
`InliningDiagnostics` pins a constant duration body against table-column endpoints.
`InlineSafetyDiagnostics` pins ten/eleven surviving NULLIF comparisons,
weighted function-backed/IO coercion thresholds, duplicated scalar subqueries,
actual volatile duplicated versus single-use arguments and STABLE zoned literal
arithmetic. NULLIF contributes its equality operation; CASE/COALESCE structural
control remains zero overhead. Every reached body failure has direct/prepared
full diagnostics and independent recovery assertions.
No PostgreSQL-only discovery filters remain in the completed definitions.
Computed BOOL projections strictly assert unknown nullability in both backends.
Stored view/generated columns independently assert native `(nullable=true,known=true)`
versus lib/pq's unknown pair. `StoredDeparse` keeps its main SQL/deparse body
shared; an explicit PostgreSQL `pg_attribute` capability phase pins OID16 and
typmod-1, while a separate native empty/direct/prepared public driver phase pins
BOOL name/scan/length/decimal/nullability metadata. Native exports no public
OID/typmod hook and this family does not implement adjacent catalog relations.

```sh
make check RUN='^TestReferenceDatetimeOverlaps'
make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlaps'
make check RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$'
```

Each invocation has a900-second deadline. Supplementary parser, sqltype and
engine units use `RUN='^TestDatetimeOverlaps'` with the matching
`./internal/parser`, `./internal/sqltype` or `./internal/engine`.
These selective checks do not replace ordinary quickcheck or the single
1800-second post-merge finalcheck. The feature smoke asserts actual period
results `(true,false,true,true,NULL)`, held reuse, Denver day/hour differences,
diagnostic recovery and native stored-clock-duration replay.

### Sequence value view

`TestReferencePGSequencesShape`, `Definitions`, `Values`, `Privileges`, `Temp`,
`Demand`, `Transactions`, `Diagnostics`, `CTE` and `Controls` prescribe independent
pinned PostgreSQL18.0 outcomes, including exact cache high-water/NULL masks,
all eleven descriptors, full English diagnostic records, physical TEMP exit,
actual emitted Dump replay and cancellation cleanup. PostgreSQL-only pg_type
identity links supplement native actual descriptor/type-link units; native TEMP
namespace is obtained from the actual public pg_sequences row, not an unsupported
pg_my_temp_schema function. Shared direct/prepared behavior is not skipped.

```sh
make check RUN='^TestReferencePGSequences'
make check RUN='^TestReferencePGSequences' REFERENCE=1
make check RUN='^TestPGSequences(Descriptors|Provider|ACL|Demand|Concurrency|Cancellation|CTE)'
make check RUN='^(TestPGSequences(Concurrency|Cancellation|CTE)|TestReferencePGSequences(Demand|Transactions|Temp|CTE))' RACE=1
```

Use external900s deadlines. Changed reference definitions require both native
and real pinned modes before review. Whole-project Docker-free `make quickcheck`
and post-merge `make finalcheck` do not replace selected proof or the standalone
public smoke. Demand units inspect real manager holders/queues, including
definition/count/stored/join RX, rejected-name/FALSE no RX and terminal release.

### Sequence definition catalog discovery

The shared `TestReferencePGSequence` family covers the eight-column
`pg_catalog.pg_sequence` contract, exact definition options rather than generator
state, catalog identities, transactional visibility, repeatable-read peer
ALTER/CREATE/DROP, first-Prepare versus first-Execute timing, privileges and
other-session temporary definitions, full diagnostics, cancellation and actual
native Dump replay. `TestReferencePGSequenceFeatureSmoke` selects the integrated
definition lifecycle and control replay witness.

The privilege fixture keeps PostgreSQL's four false `has_schema_privilege` /
`has_sequence_privilege` assertions. Native mode does not implement those
predicates: a qualified `nextval` must report private-schema denial, while
numeric-regclass `nextval`, `currval` and `setval` must independently report
sequence denial with exact existing diagnostics before the same metadata read.
Denied `nextval` excludes USAGE/UPDATE and denied `currval` excludes USAGE/SELECT.
Failed native Restore appends a supported `CREATE SEQUENCE ... CACHE 0` to the
actual dump, expects option-binding SQLSTATE 22023, and preserves the complete
original definition bag; arbitrary ALTER is outside Restore's accepted input.

The oracle discovery observed retained RR definitions and row bags. Prepare
inside BEGIN retained start15/cache9 after peer ALTER; a statement prepared
before BEGIN captured start21/cache4 at its first execution. Discovery filters
are removed: both commands run the shared completed scenario. Private-schema
fixtures use default absent PUBLIC privileges with the independent false
predicates or denied-operation witnesses above, without native REVOKE support.

```sh
make check REFERENCE=1 RUN='^TestReferencePGSequence'
make check RUN='^TestReferencePGSequence'
```

After implementation, include all migrated definition probes in both modes:

```sh
make check RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check REFERENCE=1 RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check RUN='^TestReferencePGSequenceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGSequenceFeatureSmoke$'
make check RUN='^TestPGSequence'
```

Each selected check invocation has a 900-second deadline. The finalcheck runs
after merge under its single 1800-second deadline; neither gate replaces the
selective dual-mode runs.

### Sequence metadata view oracle

The `TestReferencePGSequences` family defines the eleven-column `pg_sequences`
oracle independently of backend comparison. `Shape`, `Demand`, `Transactions`,
`Diagnostics`, `Frames` and `CTE` cover exact types and fixed catalog identities,
admitted-row volatile helper locks, retained RR definitions with live
generator/ACL state, complete diagnostics, lexical-frame-safe correlated
projections and volatile CTE producer frontiers.
`Definitions`, `Values`, `Privileges`, `Temp` and
`Controls` cover options, cache high-water values, unreadable/uncalled NULL,
physical TEMP visibility and actual native control replay. These are test
expectations, not a claim that the native view implementation has passed.

RR drop/recreate diagnostics interpolate the original sequence OID captured
before peer DDL, never a hard-coded allocated OID or the replacement identity.
Even a definition-only admitted read expects `XX000`; the subsequent query
expects `25P02`. Namespace and sequence definitions, including the owner identity,
remain from the RR definition snapshot. The owner name is presented from the
current statement role root rather than a retained role-name snapshot. Current
role membership produces NULL/value/NULL, and current generator state is read
independently.

The Recreate witness issues DROP and CREATE as separate commands on the same
peer connection; it tests real identity replacement, not multi-statement SQL.
Full, definition-only and FALSE `FOR UPDATE` queries must succeed directly and
through retained prepared execution. Their SQL rowmark admission still matches
the bound manifest; the represented view has no physical tuple marks, while
admitted rows retain the normal per-sequence helper locks.

Ordinary derived sources capture the caller's lexical frame after nested binding
restores that scope. `TestPGSequencesAdmissionDerivedRowLockFrame` binds the actual
derived `FOR UPDATE` shape and proves that its local residual predicate crosses
the row-lock boundary into direct child projection columns in the child's exact
frame. The filter uses one captured evaluation frame for both admission and
residual evaluation. Foreign, zero or mixed frames remain whole-expression
barriers; CTE boundaries remain distinct and are not admitted by this mapping.
The existing Demand witness still requires a-only helper RX, with no b helper RX.

`TestPGSequencesConcurrency` observes the actual manager request on the exact
sequence identity: RESTART queues ShareRowExclusive behind the view helper's
transaction-owned RowExclusive. Closing Rows does not release that lock;
rollback admits RESTART, whose fresh last_value is NULL and nextval is 19.
Cancellation/join cleanup runs even when the queue assertion fails.

`TestReferencePGSequencesCTE` and `TestPGSequencesCTE` exercise direct,
ordinary-derived, stored-view and nested stored-view sources with default,
`MATERIALIZED` and `NOT MATERIALIZED` hints. A consumed volatile CTE completes
both sequence rows behind one producer even when an outer restriction returns
only one name; shared references retain that producer rather than independently
folding its restrictions. The tests observe helper RX holders and a real blocked
helper queue on the sequence absent from the final rows, cancellation and terminal
cleanup/recovery. Prepare opens no helper, and unused CTE bodies acquire no helper
locks. `TestPGSequencesCTEStoredGraph` and `TestPGSequencesCTEStoredGraphAuthority`
inspect real immutable stored definitions without opening providers; shared
acyclic sources are allowed, while invalid references and active-path cycles use
the existing catalog/view graph errors.

Malformed inferred REGTYPE input is an explicit PostgreSQL wire-only control:
OID-input `22P02` includes `unnamed portal parameter $1 = '...'`. The native
driver has no PostgreSQL parameter input codec and must not fabricate that
context. Shared direct and retained prepared routes still assert numeric
inferred REGTYPE inputs 20/23/21 and bigint/integer/smallint outputs. Explicit
symbolic input through `text::regtype` independently expects full `42704`
diagnostics and recovery using the same retained statement.

```sh
make check RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
```

Each command requires an external 900-second deadline. Before production
authorization, use the same reference selector with `/postgres$` appended to
exercise only the pinned oracle; unqualified definitions include native mode
and supply its missing-view behavioral red. No discovery backend filter or
deliberately failing observation helper remains.

The extraction family uses the same shared fixtures and public APIs in both
modes, with explicit independent values, diagnostics and metadata. Its smoke
covers all six source types and both APIs, strictness, ISO/BC, interval parts,
infinities, precision, prepared Execute-time zones and stored-definition replay:

```sh
make check RUN='^TestReferenceDatetimeExtractFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeExtractFeatureSmoke$'
make check RUN='^TestReferenceDatetimeExtract'
make check REFERENCE=1 RUN='^TestReferenceDatetimeExtract'
```

Each invocation has a900-second deadline. Both complete modes must pass before
review/integration; the pinned mode is not deferred until finalcheck.
Supplemental units live in `./internal/sqltype`, `./internal/parser` or
`./internal/engine` with `RUN='^TestDatetimeExtract'`. Regenerate grammar with
`make generate` after parser source changes; generated code is not hand-edited.
The root performs these checks and the single1800-second finalcheck.
Extraction boundary witnesses distinguish unsupported native constrained-domain
DDL/current_setting/random predicate admission from actual PostgreSQL behavior;
they do not skip either backend or introduce adjacent capabilities. Plain-domain
sources and table CHECK cover selected extraction consumers and later-row
atomicity. The wrapper Context matrix uses the supported CURRENT_DATE field;
real bound random-provider units supplement its public native syntax boundary.
Ordinary and partial B-tree extraction expression indexes are shared successes:
direct/prepared creation, filtered years, retained row count and stored WHERE.
The broad pg_index descriptor inquiry remains PostgreSQL-only. Catalog queries use
existing same-OID joins. Wrapper units supply the existing fixed transaction
clock fixture while retaining a real statement/session scope for random.
COUNT(EXTRACT(month FROM s)) covers actual aggregate-child ownership with an
independent INT8 count2. SUM(EXTRACT(month FROM s)) is a shared direct/prepared
success with exact NUMERIC3 (February2 plus January1), covered by both
`TestReferenceDatetimeExtractStoredConsumers` and
`TestReferenceDatetimeExtractFeatureSmoke`. Scoped helper subtests include
their SQL query for actionable quiet-run diagnostics.

The review lifecycle witnesses are
`TestReferenceDatetimeExtractNotNullAtomicity` (later NUMERIC/FLOAT8 extraction
NULLs into NOT NULL, complete23502 diagnostics, unchanged sentinel),
`TestReferenceDatetimeExtractTransactionAbortRecovery` (runtime22023,
aborted25P02, rollback of staged rows and successful extraction on the SAME
connection/held statement), and
`TestReferenceDatetimeExtractNativeRawRetainedZoneAfterSessionClose`.
The last is explicitly native-only public-driver ownership evidence: an
unconsumed NUMERIC/FLOAT8 hour/zone/epoch cursor captured under New York retains
its original values/metadata after source/peer UTC changes and actual physical
source close/discard; fresh peer executions independently use UTC. It does not
invent PostgreSQL raw-driver controls. All three are included by the full family
selector above; NOT NULL and transaction diagnostics were pinned independently.
The smoke itself now executes1BC century/millennium-1 and2BC decade-1 pairs.

### Regtype completed definitions

The `^TestReferenceRegtype` shared-body family encodes pinned PostgreSQL 18.0
diagnostics, metadata, raw transport, warning timing and held literal identity.
Every released/initialized name and array is exercised through regtype SQL
without requiring a native pg_type projection. The complete identity facts and
fixed-versus-allocated provenance are described in
[the inventory handoff](design/regtype-pg18-oracle.md).

The completed input and authorization scenarios preserve generic qualified
varchar modifier witnesses and use a superuser-owned denied table in an
accessible reader-owned schema. `TestReferenceRegtypeAliasModifierSyntax`
preserves rejected string/parenthesized alias modifiers across all six routes,
including held runtime input. `TestReferenceRegtypeHeldLiterals` and
`TestReferenceRegtypeWarnings` cover authority-sensitive identity capture and
literal-versus-parameter warning timing. No discovery filters, observation
logging or deliberate discovery failures remain.

`TestReferenceRegtypeOutputAuthority` and its held/atomic/native-control companions
exercise selected VARCHAR IO, TEXT/VARCHAR assignment, domain sources, stored
defaults and admitted ALTER-column conversions with actual Dump replay and
retained string metadata. The unrelated-role
family requires unchanged held OIDs and no repeated warnings after unrelated
CREATE/DROP ROLE, while its qualified-authority control requires literal/fresh
Prepare 42501 Position8, frame 42501 with portal Context, and owner-role recovery.
Run the completed review-remedy witnesses in both modes before review, each with
a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
make check REFERENCE=1 RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
```

Validate the same completed definitions in both modes before review; each
quickcheck invocation has a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype'
make check REFERENCE=1 RUN='^TestReferenceRegtype'
make check RUN='^TestReferenceRegtypeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRegtypeFeatureSmoke$'
```

The feature smoke independently asserts name/OID conversion, builtin and user
row identities, qualification/shadowing, held input recovery, metadata,
persistent scalar/domain/array values and native control atomicity. Native
controls must replay the actual emitted native stream on PostgreSQL; never
substitute a manually reconstructed stream or an invented PostgreSQL
Dump/Reset/Restore API. The unchanged cohesive fixture's `checkRaw` hook supplies
independent REGTYPE/array transport checks through the small regtype helper.
These selectors do not replace ordinary quickcheck or the post-merge finalcheck
under its single 1800-second deadline. Completed definitions are not passing
verification until the root records successful runs.

`TestReferencePartialIndexTypeGeneration` composes the same supported domain/
associated-array displacement with a simple-key partial UNIQUE index whose
predicate is the sole changed stored type consumer. Direct and held routes assert
stable array OID, renamed current metadata, exact rows and member23505,
outside-array admission, failed UPDATE atomicity and retained native cursor
metadata/content. It supplements, not replaces, the complete Regtype family:

```sh
make check RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$'
make check REFERENCE=1 RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$'
make check RUN='^TestPartialIndexPredicateTypeGeneration'
```

`TestReferencePartialIndexWindowDiagnostics` retains direct/held and UTF8-position
routes for window functions in partial predicates: full42P20 predicate-context
diagnostics, no-OVER42809 precedence, no effects and same-name supported recovery.
Keep the complete QueryWindow family selected after shared-comparator/binder
changes. Structural metadata has no native SQL pg_index projection; direct
`TestPartialIndexMetadataPredicate` consumers assert exact current/retained
truth/membership and original attribute dependencies despite simplification.

```sh
make check RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)'
make check REFERENCE=1 RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)'
make check RUN='^TestPartialIndexMetadataPredicate'
```

Regtype control witnesses retain a supported TEMP SEQUENCE across Reset/Restore
to fence physical-session ownership and allocator collisions. Persistent
domains supply the user-defined TypeClassID remapping witnesses. Temporary
domain creation remains an adjacent native 0A000 boundary, even in an already
initialized namespace; the path scenario asserts that boundary natively and
uses PostgreSQL-only temporary-domain setup for additional oracle context.

The removed `smoke/TestRoleSmoke` assertion union is preserved by
`TestReferenceRoleCreation` (aliases/login/counts/duplicate/drop),
`TestReferenceRoleAuthorization` (direct/held identities, NOLOGIN switches,
table ACL/no mutation and owner dependencies),
`TestReferenceRoleAuthenticatedSessions` (self/default/reset/peer isolation),
`TestReferenceRoleTrustedConnectorIsolationAndReplacement` and
`TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation`
(authentication, reconnect and Driver/opaque-key isolation), and
`TestReferenceRoleMigratedSmokeControls` (changed-session pool reuse, option
boundary, reader-authorized Dump/Reset/Restore, exact owner directive,
failed Restore/sentinel preservation, legacy stream and role cleanup).
`TestReferenceControlTransactionsHostAuthorization` additionally preserves
reader identity/settings and denied table access across host replacement.
Run the completed remedy/migration definitions in both modes:

```sh
make check RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)'
make check REFERENCE=1 RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)'
```

Role-attribute oracle findings are encoded in the untagged shared
`TestReferenceRoleAttributes...` scenarios; discovery filters/unresolved reports
are removed. Root-observed selected native and pinned gates passed before this
documentation cutover. A separate actual public Go smoke reached creator ADMIN,
ALTER USER omission, expired trusted admission, physical limit release, NOLOGIN
and held authority refresh. Those observations do not replace the remaining
full quickcheck, fresh affected selectors, review or post-merge finalcheck.

Run the exact changed reference definitions in both modes before review, each
with a900-second deadline:

```sh
make check RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$'
make check REFERENCE=1 RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$'
make check RUN='^Test(Role(Attributes(DefaultsAndRetention|Authority|MembershipNonInheritanceAndAdminTraversal|ConnectionLimitLifecycle|ConnectionLimitRaces|PasswordStorage|PasswordSASLprep|ValidityInput|PublicationAndSnapshots|ControlsRetention)|AuthorityAndSurface)|FormatEncodingCurrentRoleAndResetAll)$'
```

The format fixture uses the canonical attribute executor with a real captured
StatementScope. Trusted connector NOLOGIN specifically asserts FATAL28000 with
complete diagnostics; missing/miscased native names remain ordinary host errors.
All other opaque-key, Driver isolation, authenticator and replacement witnesses
remain selected by the broader role family above. Public pg_roles observations
now have independent dual-mode coverage; protected pg_authid probes remain
PostgreSQL-only. Membership authority witnesses run on native too. Native controls and explicit-transaction exclusions are
independent capability phases, never discovery filters.

Each invocation has a900-second deadline. Existing Array/Domain/Enum,
SearchPathStoredViewRuntime, StringCoreReviewRestore and
TableCoreAuthorizationAndOwnershipCleanup reference definitions also exercise
the corrected catalog allocator/ownership seam without changed expectations.

Temporary-table shared definitions cover physical owner isolation and shadowing,
pg_temp creation/resolution, direct/prepared metadata and complete diagnostics,
all commit policies, SQL/driver/CHAIN commit, rollback/savepoints, read-only DML,
dependency counters/index membership, physical close, completed utility invariants
and native persistent controls with exact retained stored-reference provenance.
Migrated sequence TEMP-table authority/control scenarios retain sequence memories,
held statements, peer ownership and persistent-control atomicity.
`TestReferenceTemporaryTableClosePermanentCheck` asserts actual permanent CHECK
enforcement/dependency identity, physical owner exit, removed constraint/provider,
surviving rows, held prepared peer INSERT and same-basename peer TEMP isolation.
The expression lifecycle family uses actual pg_class/pg_attrdef/pg_depend OIDs and
independent prospective rows for automatic, immediate, explicit committed/private
and physical cascades. It checks generated-column projection, surviving dense-slot
indexes, retained snapshots, pending writes/deletes, savepoint rollback, composed
ON COMMIT DELETE and native Dump/Restore. Package invariants cover late catalog and
row/index predecessor reprepare, unrelated current heads, genuine private absence,
terminal combined descriptor/store/index maxima, concurrent ready-invalid/not-ready
flags and unchanged ordinary counter-exhaustion rejection.
The index-composition definitions use a SERIAL TEMP owner, table/owned-sequence
generated dependencies and an expression index created before BEGIN. Immediate
and transactional/savepoint/full-rollback witnesses retain the actual index OID,
independent key definition, original/restored rows and coherent subsequent DML.
Their package supplement verifies original/intermediate/final derived layouts
and stable slots, immutable earlier candidates, genuine rebuilt key/row identity,
original publication predecessors, idempotent validation, ordinary second-pass
overflow and physical-terminal saturation.
The same fixtures retain an admitted B-tree partial expression index created
outside BEGIN, exact full `pg_temp` key/predicate rendering and positive→negative→positive
UPDATE membership. Actual `pg_tables.hasindexes=true` remains through compaction
and rollback; the narrower native `pg_class` projection is not expanded.
Genuine package deparse checks distinguish current physical owner from peer
same-name indexes and context-free canonical Dump.
The controlled sibling-provider cascade supplements this with actual table/index
generated consumers and an independent own-row `z+1` generation across Reset/Restore.
It asserts removed siblings, stable surviving definition/dependency identities,
dense rows, held INSERT/UPDATE and nonreused same-name provider recreation.
The package supplement also discovers the actual owned-index subtree after its
removal is already staged, asserting exact once-only sibling edges and unchanged
catalog/store/vector/end/index predecessors before the real SQL cascade.
The live-provider controls retain an actual same-owner TEMP table and index
through native Reset/Restore, then run unrelated CREATE, held INSERT, independent
ALTER, UPDATE and catalog-expression consumers. Shared direct/prepared SQL
witnesses assert exact provider/definition OIDs, dependency edges, independent
rows and peer same-name isolation; Dump excludes the retained TEMP objects.
The package supplement uses real sessions and catalog replacement, preserving
captured manifests, descriptors, stores and roots while rejecting foreign engine
or physical owner, future/zero epochs, invalid attributes, concrete type/row-origin
mismatches, user-domain/array-domain types and retired/same-name recreated providers.
`TestReferenceTemporaryTablePredicateOnlyControls` isolates a plain INT-key partial
index without another BOOL-bearing TEMP definition that could mask predicate-type
retention. Direct/prepared bodies assert exact identities/full definition and
positive/negative membership before and after native Reset/Restore plus held DML.
Its package supplement compares both predicate trees' concrete builtin generations
and exact unchanged descriptors, manifests, stores, vectors, ends and index roots.
The close witness also roundtrips native persistent output without the removed
CHECK/provider while retaining the peer TEMP serial counter and stored rows.

```sh
make check RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionUnsupportedSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)'
make check REFERENCE=1 RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionUnsupportedSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)'
make check RUN='^Test(TemporaryTable|TemporaryRelation|CatalogTemporary|SchemaSearchPathSessionDefaultsAndSpecials|SerializableEvidenceActualTemporaryNamespaceAndSequenceProviders|PGDependDefaultReplay)'
make check RUN='^TestReferenceTemporaryTableFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableFeatureSmoke$'
```

Both selected modes use the same completed definitions under900-second deadlines;
ordinary quickcheck and the post-merge1800-second finalcheck remain required.

Temporary-view definitions share physical-owner, notice and direct/prepared
fixtures. They independently assert explicit TEMP/TEMPORARY metadata and dynamic
duplicate/NULL/zero-width rows, relation-only inference throughout admitted query
frames, namespace/shadowing and same-name session isolation, exact complete
diagnostics/notices, real `_RETURN` identity/drop cleanup, dependency closure,
physical exit, and blocked-source cancellation followed by same-handle recovery.
`TestReferenceTemporaryTableCapabilityBoundaries` now asserts shared TEMP-source
view success and its exact inference notice, retaining the SET SCHEMA boundary.
The native control phases independently replay the actual persistent dump into
a fresh engine and prove TEMP-only/unchanged pinned-catalog retention with exact
identities, held reads and intermediate builtin usages. Invalidated persistent
providers, TEMP restore scripts and invalid/canceled imports must leave both
persistent and TEMP projections unchanged. Package-function stages supplement
associated rowtype/array private publication/removal, ON COMMIT DROP closure,
canceled commit, utility-only Describe and bounded retained-provider provenance.
They do not replace public SQL evidence or add pg_type/pg_class.reltype providers.
`TestReferenceTemporaryViewInferencePrecedence` independently combines TEMP
inference with duplicate outputs, forbidden/missing destinations, replacement
targets, options and indeterminate collation. Early SELECT/excess-alias errors
must remain notice-free. Fresh options-before-duplicates and existing-replacement
duplicates-before-options are distinct contracts. Isolation and FeatureSmoke
also perform an actual prepared peer lookup requiring complete42P01/Position15
at Prepare, separately from their retained direct Execute witness.
`TestTemporaryViewRetainedSourceFrontier` exercises genuine session publication,
native replacement and recursive source-frontier capture, while refusing a bare
old-epoch table reference. `TestReferenceTemporaryViewRetainedWrites` independently
asserts existing INSERT/UPDATE/DELETE counts, reordered nested-view rows and
peer isolation in both backends, with native Reset/Restore repetition. Stored
subquery sources retain their defining-layer ACL and frozen-layout obligations.

```sh
make check REFERENCE=1 RUN='^TestReferenceTemporaryView'
make check RUN='^TestReferenceTemporaryView'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableCapabilityBoundaries$'
make check RUN='^TestReferenceTemporaryTableCapabilityBoundaries$'
make check RUN='^Test(CreateTemporaryView|TemporaryView)'
make check REFERENCE=1 RUN='^TestReferenceTemporaryViewFeatureSmoke$'
make check RUN='^TestReferenceTemporaryViewFeatureSmoke$'
make check RACE=1 RUN='^TestReferenceTemporaryView(Cancellation|Lifetime)$'
make check REFERENCE=1 RUN='^TestReferenceViewUpdatable'
make check RUN='^TestReferenceViewUpdatable'
```

Use external900-second deadlines, and selected existing VIEW/replacement/join/
control regressions for every shared seam touched. Both completed modes are
required before review; neither quickcheck nor finalcheck replaces selected
pinned proof. A standalone public Go smoke may additionally use NewDriver,
physical sql.Conn owners and native Dump/Reset/Restore; test selectors alone
are not standalone runtime smoke evidence.

The datetime-constructor smoke directly asserts all five exact result types and
values, BC, TIME24:00/TIMESTAMP carry, sparse `=>`/`:=` interval defaults/NULL,
held prepared session-zone changes, full calendar/seconds/zone/signature errors,
later-row write atomicity and named stored-definition Dump/Restore with replay
of actual emitted native SQL against PostgreSQL:

```sh
make check RUN='^TestReferenceDatetimeMakeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceDatetimeMakeFeatureSmoke$'
```

Run the complete identical `RUN='^TestReferenceDatetimeMake'` family in both
modes before review, with900-second deadlines. Supplemental constructor units
use ` RUN='^TestDatetimeMake'`,
` RUN='^TestDatetimeMakeNamedArguments$'` and
` RUN='^TestDatetimeMake(Binding|Properties|ArgumentAssociation)$'`.
These selectors do not replace ordinary quickcheck or post-merge finalcheck.
Successful interval witnesses assert `INTERVAL` metadata and exact public output
codec strings directly; they do not require an `interval::text` conversion edge.
Constructor write coverage uses INSERT affected counts and existing UPDATE/DELETE
RETURNING projections, not unsupported INSERT RETURNING syntax.

The bitstring family covers independently pinned BIT/VARBIT values, raw scanner
and modifier diagnostics, direct/held parameters, assignment atomicity, complete
metadata and retained ownership, plus native Dump/Reset/Restore with PostgreSQL
replay of the actual emitted SQL. The expanded feature smoke combines the compact
scenario with stored-format replay/default demand, compound/simple-parenthesized
modifiers, differing-width repeated slots and held parameterized cursor snapshots.
Run each invocation under the quickcheck900-second deadline, quietly:

```sh
make check RUN='^TestReferenceBitstring'
make check REFERENCE=1 RUN='^TestReferenceBitstring'
make check RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
make check REFERENCE=1 RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
```

Both modes are required before review/integration. Finalcheck remains one
1800-second gate after merge. Discovery-only deliberate observation failures
and PG-only filters must be removed before the native failing proof and gates.

The string-encoding smoke executes all five functions with independently expected
rows/types, Unicode16 assigned/unassigned codepoints, exact conversion and escape
diagnostics, held valid/NULL/error/recovery frames and stored NAME/INT4 overloads.
Native phases exercise retained snapshots, Dump/Reset/Restore, failed Restore
atomicity and real PostgreSQL replay of actual emitted native SQL:

```sh
make check RUN='^TestReferenceStringEncodingFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceStringEncodingFeatureSmoke$'
make check RUN='^TestReferenceStringEncoding'
make check REFERENCE=1 RUN='^TestReferenceStringEncoding'
```

Each invocation has a900-second deadline. Smoke does not replace the complete
family, ordinary quickcheck or post-merge finalcheck. The offline Unicode generator
also emits assignment ranges from the existing vendored Unicode16 data:

```sh
go run ./internal/sqltype/unicode16/generate -source ./internal/sqltype/unicode16/data -output ./internal/sqltype/unicode16/tables_generated.go -case-output ./internal/sqltype/unicode16/case_tables_generated.go -assigned-output ./internal/sqltype/unicode16/assigned_tables_generated.go
```

Concurrent index scenarios use `^TestReferenceConcurrentIndex` for the complete
writer/snapshot/lifecycle/diagnostic/control family, and
`^TestReferenceConcurrentIndexFeatureSmoke$` for the feature smoke.
`^TestReferenceConcurrentIndex(TypeReconstruction|KeyLockEligibility)$` selects
the added independent TYPE/private-prefix and row-key-lock consumer contracts
for the root's real-first observation pass.
`^TestReferenceConcurrentIndexHash$` selects inherited hash-method lifecycle,
first/later writer admission, actual flags and equality results, unsupported
hash UNIQUE full diagnostics, and emitted Dump/Restore/replay coverage. It is
also included in the concurrent feature smoke. Preserve and run the integrated
`^TestReferenceHashIndex` family alongside it; the hash merge cancellation
calibration migrates to the actual descriptor/readiness authority without
changing its expected results.
`^TestReferenceConcurrentIndexHashInvalidQuery$` exercises a canceled ready-invalid
hash build and a subsequent committed grandfathered row. Its independent INT4
equality/heap results and REINDEX recovery close query-consumer eligibility.
The supplemental `TestConcurrentIndexHashInvalidQueryCapture` uses actual
incomplete buckets and matching storage identity to prove invalid capture is
rejected, heap filtering returns the row, and healthy reconstruction is selected.
Both regressions preserve pre-ready admission rather than repairing the fixture.
Run each selector with both `make check RUN='...'`
and `make check REFERENCE=1 RUN='...'`.
Affected ordinary-route regression selectors additionally include
`^TestReferenceBtreeIndex(OrdinaryShareBuild|ExcludedDefinitionsDiagnostics)$`
and `^TestReferenceRelationDeadlock`.
PostgreSQL queue/progress observations use actual lock/PID identities. Native
ordinary pending-Share/later-writer queue ordering is proved by internal lock
units: no public native queue catalog or worker-start timing is substituted.
The discovery-only pg-only filter has been removed: every final shared backend
loop discovers native and PostgreSQL scenarios according to the standard mode.
Native U1–U7 use `make check RUN='^TestConcurrentIndex'`;
parser ordinary/concurrent heads use the same RUN selector.
Each selected check invocation has one 900-second deadline. Root owns parser
generation, formatting and verification after all implementation edits land;
agents do not run mid-flight checks. Final post-merge finalcheck uses one
1800-second deadline. These commands are required inputs, not reported passes.

The current-tip ordinary UNIQUE/NND integration retains its independent shared
`^TestReferenceUniqueIndex` family in both modes. The added
`^TestReferenceConcurrentIndexNullsNotDistinct$` definition covers first-build
NULL duplicates, real old-snapshot admission, Ready-before-Valid NULL enforcement,
atomic INSERT/UPDATE, actual emitted Dump/native Restore/unchanged PostgreSQL
replay and default/explicit DISTINCT controls. Run this exact selector real-first,
then natively, each under `timeout 900s make quickcheck`; it is also in feature
smoke. Public ordinary provider exclusions remain admission policy; isolated
Restore reconstructs emitted computed definitions using existing authenticated
destination authority, not a UNIQUE parser option.

The index-clause feature smoke exercises mixed keys, an existing scalar expression
key with explicit owned collation/class and INCLUDE, prepared uppercase projection,
payload/derived-key maintenance, the total 32/33 boundary and native controls with
PostgreSQL replay of the actual emitted native SQL. Compact `pg_get_indexdef`
assertions cover default/provider spelling, the INCLUDE ordinal boundary and
post-TYPE state without changing canonical Dump/replay authority:

```sh
make check RUN='^TestReferenceIndexClausesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceIndexClausesFeatureSmoke$'
```

This smoke does not replace the complete identical dual-mode family selection
documented in [the index-clause design](design/PG18-INDEX-CLAUSES.md).

The index-property smoke checks the eleven independently expected BOOL values
for B-tree AM/index capabilities, mixed DESC/NULLS ordering and INCLUDE roles,
plus prepared true/false/NULL and explicit native Reset/Restore/foreign restore:

```sh
make check RUN='^TestReferenceIndexPropertiesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceIndexPropertiesFeatureSmoke$'
```

Run both with a900-second deadline. The full `^TestReferenceIndexProperties`
selector covers every new definition; see [the inquiry design](design/PG18-FUNC-INFO-INDEX-PROPERTIES.md)
for the shared-coercion consumer risk selectors. Smoke does not replace those
identical dual-mode selective runs, ordinary quickcheck or post-merge finalcheck.
The dependency catalog family independently specifies exact represented edge bags,
all seven result descriptors, owner-specific diagnostics, supported private
sequence changes, retained cursors and native controls with actual SQL replay.
Run the identical complete family in both modes, each with a900-second deadline:

```sh
make check RUN='^TestReferencePGDepend'
make check REFERENCE=1 RUN='^TestReferencePGDepend'
make check RUN='^TestReferencePGDependFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGDependFeatureSmoke$'
```
Changed existing default definitions must also run in both modes; the family
prefix does not select them. They preserve genuine direct and prepared CREATE,
default INSERT, data/provider sentinels and existing excluded sequence-expression
controls without separate duplicated PostgreSQL-only success bodies:

```sh
make check RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
make check REFERENCE=1 RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
```

Each selective invocation has a900-second deadline. Source-route migration
definitions are additional named selections, not replacements for this family
or these changed existing definitions.


Supplemental collector/walker units use
`make check RUN='^TestPGDepend'`.
These do not replace the dual-mode runs, ordinary quickcheck or post-merge
finalcheck. Public capability boundaries remain explicit: no REGCLASS column
storage, set-operation query rollout, transactional CREATE TABLE/ALTER DEFAULT
or native TEMP-table creation is implied by metadata reading.


The prepared-transaction family independently asserts state/diagnostics,
ownership and five-column XID metadata, physical-origin-close survival,
SESSION/LOCAL and TEMP restrictions, held resources, coupled existing-index/
ALTER/data/sequence publication, SSI finalization and native replacement guards:

```sh
make check RUN='^TestReferencePreparedTransaction'
make check REFERENCE=1 RUN='^TestReferencePreparedTransaction'
make check RUN='^TestReferencePreparedTransactionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePreparedTransactionFeatureSmoke$'
```

Bound each invocation to900 seconds. Smoke expects private7 invisible to both
origin/peer, one correctly typed stable prepared row, survival after actual
physical origin exit, peer commit publishing7, and rollback9 preserving7.
Register bounded GID-specific ROLLBACK PREPARED cleanup before each prepare-capable
operation, ahead of schema/pool/role cleanup; never blanket-clean unrelated GIDs.
sql.Conn.Close only returns a borrower. Dispose the isolated pool/raw driver
connection to witness physical origin end. All worker cancellation/join paths
remain bounded. The pinned fixture enables max_prepared_transactions100 at
startup; native has no matching capacity/configuration surface.

Prepared deferrable coverage distinguishes real admission from goroutine start:
PostgreSQL's genuine SafeSnapshot observer asserts admitted predecessor0 across
PREPARE/origin close and either final outcome. Native has no public readiness
catalog/notice; package-function tests admit a real candidate through the existing
tracker barrier and assert retained original roots/frontier and predecessor0.
The shared both-backend synchronous statement-timeout companion proves a prepared
writer remains unresolved, then verifies exact fresh commit1/rollback0 after
actual completion. No scheduling sleeps or0-or1 tolerance substitute for admission.

Private `TestPreparedPopulatedReplaceCatalogGuards`,
`TestPreparedPopulatedCompetingCompletionAndRetry`,
`TestPreparedCompetingGIDDisposesWholeLoser`,
`TestPreparedLateCancellationAndWaiterObservePublication` and
`TestPreparedValidationOmitsInstallationRoots` use real admitted AST statements
and populated surviving UPDATE plus insert/deletion/catalogue/index/generator/
SSI/ticket state. They cover same-owner and foreign-owner replacement refusal,
actual competing claims, outside-metadata expression barriers, cancellation and
independent final counter veto retaining the exact UPDATE/base/version, committed
heads, original ticket, other roots and all Engine counters; whole losing-GID/
settings disposal; successful retry exactly once; and publication visible to a
real resource waiter before late cancellation. The validation-only structural
regression checks genuine live UPDATE predecessors and rejects wrong base
versions and already-ended predecessors in both phases. Validation-only work
produces no replacement heads/slot arrays; materializing work produces a separate
head and the exact update-slot manifest without installing it during validation.
These package tests issue no public SQL and add no production hooks.
Every concurrent boundary scenario registers cleanup before launching workers:
arrival/outcome waits have deadlines, cleanup releases admission/expression
barriers once, cancels outstanding operation/request contexts and joins every
launched worker through bounded waits, including assertion-failure paths.
Admitted AST fixtures specify parser-established provider defaults explicitly
(for example, CREATE INDEX uses `Method: "btree"`); an empty method is not an
engine default. The existing changed-predecessor publication regression keeps
its original canonical `mergeIndexRoot` preparation boundary and all unchanged-
state, real-peer retry, final-veto and exact row/index/SSI outcome assertions.
`TestIndexClausesReplacement` and `TestIndexClausesValidationOnlyReplacement`
also preserve the canonical nonunique replacement contract for reduced valid
parent/index catalogues: ordering/provider/shape checks require no namespace
descriptor. Namespace binding remains required when constructing UNIQUE
diagnostics. Invalid key definitions still fail both validation-only and
materializing phases.

```sh
make check RUN='^TestPrepared'
```

Held-resource timeout Context includes genuine PostgreSQL physical tuple
coordinates only on PostgreSQL; native independently asserts its absent Context.


The set-operation feature smoke exercises all nine operator/modifier bags and
Exec counts, NULL multiplicities, precedence/parentheses, VALUES, local/global
bounds, pairwise unknown/REAL typing, SAME held parameters, derived/scalar/EXISTS
and correlated consumers, dedup-before-INSERT assignment, width/UTF8/ORDER/lock
diagnostics, stored views, foreign Restore and retained results. It executes the
actual native Dump stream on PostgreSQL; it never substitutes reconstructed SQL.
Native controls explicitly cover invalid trailing and EOF-canceled set-view
restoration, two foreign restores, destination-owned enum/domain/array grouping
after source Reset, and retained rows/schema across destination Reset/Restore.

```sh
make check RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
make check RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$'
```

Each invocation has a900-second deadline. The complete family selector is
`RUN='^TestReferenceQuerySetOps'` in both modes; smoke does not replace it or
ordinary quickcheck/post-merge finalcheck. Lifecycle checks row-dependent late
source/assignment failures, zero-demand runtime suppression with all-arm analysis
and argument admission, ordinary native pre-cancellation, genuine observed
PostgreSQL server admission/cancellation, and SAME owner/preheld set recovery.
PostgreSQL cancellation uses the existing admitted pg_sleep helper, not a new
native function or a timing-only assertion that a fast set query was interrupted.
Pinned PostgreSQL18.0 observations ground the additional consumer rows, exact
late source/assignment diagnostics and all-arm zero-demand analysis positions.
Successful discovery does not replace completed dual-mode acceptance runs.

Stored set-view lock-boundary regression smoke covers all four modes on catalog
and sequence sources, reader-owned security_invoker/nested catalog views, and
inline-derived rejection. PostgreSQL fixture-only GRANT controls additionally
exercise ordinary SELECT-only base readers, local stored marks and revoked SELECT
under LIMIT0; native private ACL units exercise the same authority boundary
alongside shared public table/column grants. Run each command within900 seconds:

```sh
make check RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$'
make check RUN='^TestQuerySetStored(InheritedMarks|LockAuthority|ReferenceKinds|RelationModes)$'
```

The private units retain local wait/strength/diagnostic recipes, ordinary-view
inheritance, all-arm admission, sequence reference-kind checks and actual acquired
relation modes. They do not duplicate public adapter scenarios. The stored-set
barrier changes inherited marks only, never ordinary SELECT/dependency traversal.
The hash-index feature smoke includes duplicate/NULL/range/order results, genuine
held probes with fresh parameters, post-Prepare mutation, method capabilities and
full diagnostics. Its controls contract exercises actual native Dump/Restore,
self/foreign replacement, retained TEMP/eager rows, failed late replay and real
PostgreSQL consumption of the unchanged native SQL. Public results alone cannot
prove indexing, so also run the package-function smoke that asserts the captured
real root, duplicate bucket candidates and consumption by the original filter:

```sh
make check RUN='^TestReferenceHashIndexFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceHashIndexFeatureSmoke$'
make check RUN='^TestHashIndexLookupFeatureSmoke$'
```

Each selected check has a900-second deadline. Full changed-definition selectors must
run identically in both reference modes before review/integration:

```sh
make check RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
make check REFERENCE=1 RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)'
```

The `TestReferencePGTableViews` family independently pins exact eight/four-field
summaries, raw metadata, current/quoted ownership and names, unfiltered denied
object disclosure, immutable captures/held refresh, sticky index history and
failed ordinary/concurrent publication, reconstructed user-definition replay,
represented builtin9/4 membership and full released definitions, diagnostics,
transaction overlays/RR differences and native logical control normalization.
The controls replay actual native SQL Dump on PostgreSQL. Column GRANT/REVOKE
fixture setup remains a separate phase from ACL-neutral native Dump replay:
explicit ACLs are not serializable. Internal units exercise source capture and
descriptor replacement without weakening that control boundary.
Run every new/migrated reference definition in both modes, each with a900-second
deadline, before review:

```sh
make check RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check REFERENCE=1 RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check RUN='^Test(TableSummary.*|TableViewSummaryBuiltinIdentity)$'
make check RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
```

Expected feature observations include DROP-last-index preserving true, a fresh
table starting false, denied-schema/table metadata disclosed on both backends,
four nonNULL released builtin SELECT definitions, executable dynamic user-view
copies after renames/replacement and Restore producing false/true/false for
never/currently/formerly indexed tables. No discovery gate remains in final tests.

The `TestReferencePGIndexes` acceptance family pins the five-column view shape,
compact definitions, held execution, lifecycle, supported transactional overlays,
repeatable-read peer CREATE boundary, independent disclosure policy and full
diagnostics. Controls replay actual native Dump SQL on PostgreSQL; the existing
ready-invalid concurrent hash witness also checks descriptor enumeration.
Run the complete affected selector in both modes (900 seconds per invocation):

```sh
make check REFERENCE=1 RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^TestReferencePGIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGIndexesFeatureSmoke$'
```

Initial pinned oracle discovery passed before production. All scenario-local
discovery filters are removed; both commands now execute the same shared bodies,
including native behavioral-red before implementation. A filtered native run
is not verification. PostgreSQL exposes denied underlying objects here;
native acceptance intentionally requires schema USAGE and parent table SELECT
or SELECT on any live column. No global fixture behavior is changed.
Private `TestPGIndexes` units exercise real SetObjectACL/AuthorizationView
captures: table versus live non-indexed column grants, PUBLIC, ordinary owner,
superuser, schema denial, dropped stable attributes, revocation, retained old
captures and original invoker dispatch. Structural metadata units remain
ACL-neutral. Run alongside the shared reference gate:

```sh
make check RUN='^(TestPGIndexes.*|TestIndexMaintenanceMetadata.*)$'
```

The shared fixture.HeldStatement retains its three-field layout and adds actual
plain DB/Tx Prepare/Exec/Query consumers alongside existing Context consumers.
Its matched query uses the genuinely held statement; unmatched queries use the
underlying plain DB/Tx, never a Context stand-in. This central fixture change also
requires supplemental identical all-reference risk selection (each900 seconds):

```sh
make check RUN='^TestReference'
make check REFERENCE=1 RUN='^TestReference'
```


PG-only relam/vector/type-provider catalog controls are genuine boundary coverage,
not native compatibility passes or skipped implementation. Native reduced-catalog
metadata is asserted independently through projectIndexMetadata package functions;
do not add unowned SQL catalogs to make a discovery query run. See
[hash design](design/PG18-INDEX-HASH.md). Generate retained parser productions
before selective checks; generation/formatting/verification belong to the root.

### BYTEA access completed definitions

`TestReferenceBinaryAccess*` covers zero-based LSB orientation, raw count and
reversal, setter truncation/immutability, bounds and input diagnostics, overload
resolution, authority, empty/NULL metadata, held frames, consumers and native
symbolic controls with actual emitted SQL replay on PostgreSQL. PostgreSQL
expectations are independent; BIT execution is asserted only there, with the
native capability error asserted separately. Portal Context and pq/native BYTEA
metadata/empty-buffer differences remain explicit, not normalized goldens.

Run each selective quickcheck under a 900-second deadline:

```sh
make check RUN='^TestReference(BinaryAccess|StringPad).*$'
make check REFERENCE=1 RUN='^TestReference(BinaryAccess|StringPad).*$'
make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryAccessFeatureSmoke$'
```

StringPad protects the reused reverse provider and shared parser/binder.
Feature smoke exercises the released six-function projection, held frames,
bounds/new-bit failure and recovery, retained source, empty/NULL metadata and
controls replay. These gates do not replace root quickcheck or post-merge
finalcheck (one 1800-second deadline).

## RANGE partition reference coverage

`reference/tables/range_partition_*_test.go` uses one shared scenario body in both modes.
Rebase consumer witnesses include genuine direct/prepared ON CONFLICT and MERGE
hierarchy boundaries (root/intermediate/physical member), full independently
expected errors, unchanged rows and undemanded defaults. A plain-table conflict
positive control retains skipped-row and affected-count authority. Genuine
transactional DECLARE/FETCH/CLOSE consumes the complete hierarchy provider.
Literal expectations cover scalar and multikey tuple boundaries, sentinels/NULL,
DEFAULT and nested routing, expression keys/coercion, exact errors and positions,
catalog identities/dependencies, ACLs, late multi-leaf atomicity, and controls.
Utility supplied-argument stages preserve zero slots and independently expected
adapter errors. Complete diagnostic records retain every optional field.
`TestReferenceRangePartitionACL` runs its entire named-target/leaf authorization
and missing-key disclosure matrix in both modes and both genuine direct/prepared
routes, including simple column SELECT, expression-key suppression/table SELECT
and qualified destination-schema precedence. SchemaGrantExpansion independently
covers current root/nested/leaf/table/view/materialized-view schema-wide grants,
explicit virtual grants removed by schema-wide REVOKE, denied subsequent writes,
future-table and sequence exclusion, complete errors and unchanged stored rows.
QuotedKeyPrivacy additionally covers mixed-case/space-containing, embedded-quote
and reserved-word simple keys at root and nested routing failures. Public
INSERT-only, column SELECT and revoked column SELECT transitions independently
assert hidden, correctly quoted and hidden-again complete diagnostics, payload
access denial and unchanged physical rows in direct/prepared execution.
`TestReferenceRangePartitionConstraintPrivacy` independently covers direct and
genuine prepared leaf/intermediate errors under INSERT-only, supplied-value,
column SELECT, all-column SELECT and table SELECT manifests. Both backends use the
existing public table/column GRANT routes; package ACL units supplement them. Errors
retain the actual named target, complete optional fields, correct labelled/full
row disclosure and unchanged data, through one shared ordinary NN/CHECK policy.
`TestReferenceRangePartitionPhysicalConstraintPrivacy` extends that independent
matrix to root, intermediate, leaf and ordinary NOT NULL/CHECK producers, with
single/batch VALUES and direct/prepared INSERT SELECT. It distinguishes named-target
SELECT authority from physical-leaf diagnostic identity and checks that omitted
secret defaults remain private, supplied values remain visible, and late failures
leave every affected leaf unchanged.
`TestReferenceCollationNativeCollision` additionally uses explicit RANGE-key user
collation under a real imported-provider/live-TEMP OID collision. Independent
parent/leaf bags, source immutability, retained TEMP authority, exact dependency
targets and direct/prepared routing are checked before and after replacement.
Real mode replays the actual emitted SQL on PostgreSQL and routes further values.
`TestReferenceRangePartitionCollationDependencies` covers key-only, effective and
combined collation authority with exact RESTRICT diagnostics, intact rows, normal
metadata edges and complete CASCADE hierarchy/owned closure. TemporaryDependencies
checks zero-output nonportable Dump rejection, physical provider cleanup, retained
rows, peer sentinels and fresh portable Restore.
`TestReferenceRangePartitionProviderRetirement` exercises full physical-close
retirement in both backends, with bounded PostgreSQL server-exit acknowledgement
before unchanged absence assertions. PostgreSQL also executes the full ON COMMIT
DROP composition; native explicitly asserts its existing transactional permanent
CREATE boundary and rollback atomicity. Private Retirement units exercise actual
nil-scope staging and COMMIT preparation/installation from validated owned catalog
state, two real leaf write roots and a committed provider deletion. They require
unchanged original roots and complete root/leaf-owned storage/index/generator cleanup.
`TestReferenceRangePartitionLeafExclusion` and the existing Controls body reuse
ordinary physical exclusion producers, enforcing exact direct/prepared conflicts,
late cross-leaf atomicity and genuine Dump/Restore/SQL replay. Virtual exclusions
retain the explicit execution boundary; no adjacent constraint rollout is inferred.
`TestReferenceRangePartitionFailureOrder` composes both arity preflights, lower
NULL, lower/later volatile demand, malformed upper expressions and finite
transformation failures after MIN/MAX. Complete diagnostics, nonrewinding sequence
state/next draw, child/type/index/constraint absence and DEFAULT data are asserted.
`TestReferenceRangePartitionOrderedDiagnostics` deliberately reverses sibling
creation order and covers later-component reversal, full finite equality-last,
equal-lower overlap-first and spanning gap/inside overlaps. Witness and Position
follow the actual typed ordered endpoint search's last comparison; unchanged
topology/data and subsequent gap/sibling routing remain asserted.
PRIMARY KEY syntax now uses the integrated ordinary producer: virtual declarations
fail the execution-time constraint-combination boundary in both routes, not an
obsolete positioned parser/Prepare rejection. Physical leaf constraints use the
existing local construction/enforcement authority.
Named inherited CHECK cases distinguish structural equality, commuted operands,
added arithmetic, NO INHERIT conflicts and duplicate actual child declarations.
Inherited enum/domain USAGE and revoked type-schema USAGE are separate witnesses;
fresh qualified child expressions retain normal namespace denial.
Discovery uses admitted equality/OR predicates and explicit OID coercion, not
adjacent IN/operator capabilities. Inherited domain authority uses a plain domain;
its fresh USAGE and stored namespace identity are independent of domain constraints.
Schema and owner discovery uses the existing `pg_tables` provider (including
virtual roots/intermediates) with `pg_class`, without requiring the public
`pg_roles` view or the still-unavailable native `pg_namespace` catalog. Manual ownership combines
`pg_get_serial_sequence(...,'payload')` with exact `pg_depend.refobjsubid = 2`,
plus a separate manually owned root key sequence with stable subID1 and matching
column inquiry. This avoids ambiguous multiple sequences on the same column
without changing production lookup semantics. Exact schema/table identities
remain asserted; discovery does not require an unowned `pg_attribute`.
Internal catalog character fields are projected directly with CHAR metadata,
not unsupported internal-char-to-TEXT casts. Window context uses admitted
ROW_NUMBER with PARTITION BY/ORDER BY and independent ordinals1,2; standalone SUM
remains in the aggregate consumer witnesses.
Failed embedded CREATE SCHEMA additionally proves namespace absence by successful
recreation of that exact name, followed by cleanup.
Adjacent CREATE SCHEMA partition definitions assert PostgreSQL success versus native
positioned0A000 before any schema/table publication or bound sequence demand, on
both direct and genuinely prepared utility execution routes.
Manual root/intermediate sequence ownership independently asserts live column
inquiry, AUTO dependencies, routed defaults, actual unmodified Dump replay,
deterministic redump and complete subtree/owned-sequence DROP closure.

Native Reset/Dump/Restore phases use the public control APIs, guarded only because
PostgreSQL has no corresponding API. The actual emitted stream is consumed
unmodified by the real PostgreSQL connection, with typed rows, owners, dependencies
and current defaults asserted afterward. Cross-owner replay provisions only
temporary inherited role membership and importing namespace rights in PostgreSQL;
ordinary ownership denials execute before that provisioning. Deferred hierarchy
defaults exercise importer-only replay and failed replay preserving the old target.
The deferred graph is a genuine cycle of REGCLASS defaults on the virtual parent
and an ordinary table, using the existing ordinary SET DEFAULT producer rather
than unsupported ALTER ADD CHECK syntax. Mixed-owner schema setup is performed
under the genuine bootstrap principal with explicit AUTHORIZATION owners.
`TestReferenceRangePartitionSelfRestoreControls` additionally restores directly
into the same open database without Reset: held statements, retained cursors,
expression/nested keys, local UNIQUE, views, inherited CHECK, sequence ownership
and ordinary coupled defaults must retain their semantic contracts. Internal
self-catalog replacement also retains exact live TEMP sequence/table/index
descriptors, stores, index roots, generator/cache and held demand. Existing
TEMP DEFAULT/CHECK/generated/partial-index definitions retain their own authority;
the permanent candidate is fully remapped before the existing exact TEMP overlay.
The TEMP regression keeps both a supported column partial UNIQUE index (with
duplicate enforcement) and a separate ordinary expression/predicate index (with
exact descriptor/root retention). Childless and populated hierarchy sources pin
the same real catalog/data-view authority as ordinary retained query sources.
Cancellation/reset observers wrap the actual leased operation context: Acquire's
WithCancel wrapper does not forward the original caller's Err callbacks.
All temporary roles/schema rights are cleaned up under bounded deadlines.
Every actual native SQL replay on PostgreSQL registers bounded same-session
RESET ROLE / RESET SESSION AUTHORIZATION / RESET search_path cleanup before
replay, ahead of schema recreation or role-grant teardown. The shared PostgreSQL
body therefore retains its authenticated fixture owner, not the stream's final
named owner.

The former `smoke/table_test.go` behavioral/API union is now
`TestReferenceRangePartitionMigratedTableCoreSmoke`, not a native-only duplicate.
Integrated LIKE INCLUDING ALL / EXCLUDING CONSTRAINTS now share their successful
copy, empty typed/NOT NULL metadata, enforcement, DROP and absence assertions in
both modes. Actual native Dump must exist before the PostgreSQL replay lifecycle;
an earlier native failure is never treated as an empty successful replay stream.
The cohesive `TestReferenceRangePartitionFeatureSmoke` includes ordinary table
definitions, real routing, nested/default bounds, metadata and persistent controls.
No discovery filters belong in final definitions.
Raw sentinel scenarios cover mixed-case keywords, quoted lowercase names and
redundant parentheses; quoted uppercase, qualified/cast/operator wrappers retain
the independent column-reference bound error. Finite TEXT casts stay finite.
TEMP hierarchy definitions are a separately positioned native0A000 boundary,
including implicit qualified pg_temp destinations; ordinary TEMP/ON COMMIT
capabilities retain their integrated baseline implementation.

Run the same completed reference definitions in both modes, with a separate
900-second deadline for every invocation:

```sh
make check RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)'
make check REFERENCE=1 RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)'
make check RUN='^TestReferenceRangePartitionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRangePartitionFeatureSmoke$'
make check RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$'
make check RUN='^TestRangePartition'
make check RACE=1 RUN='^TestRangePartition(Concurrency|Publication|Capture|Remap)'
make check RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$'
make check RUN='^Test(CheckInsertPrivilegeDisclosure|RangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|Retirement|RemapExplicitCollation))'
```

Package-function witnesses inspect owned descriptors, cooked type/row identities,
private importer provenance, real lock queues, multi-leaf transaction stages and
complete self-catalog expression remapping with retained old cursors. They supplement
the shared public reference routes. The root generates/formats the parser and runs
verification only after all edits pause; the post-integration finalcheck runs once
under a single 1800-second deadline, with actual failures remedied separately.

## How the dual-mode fixture works

`fixture.NewScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures in one package share one bounded-lifecycle
container: **do not use `t.Parallel()`**. Each package's `TestMain` delegates to
`fixture.Main`; different package processes own independent containers,
administrative sessions and fixture state. Real-mode Makefile targets serialize
those processes with `-p 1`, including startup and final teardown.

Change note: the pinned admin pool retains one idle physical session instead of
reconnecting at every fixture reset. Open connections remain unrestricted so
observers can run while a replay holds an admin handle. Every existing
15-second before/after reset borrows one connection, executes standalone
`ROLLBACK`, then standalone `DISCARD ALL`, then the existing public schema
DROP/CREATE on that same connection, and returns it on every exit.
`DISCARD ALL` must not share a multi-statement execution with ROLLBACK: it
cannot run inside a transaction block. It restores authenticated/session role
state, GUCs and temp/prepared/advisory state before schema ownership is assigned.
All borrowed admin handles, Rows and workers must be returned/closed/joined
before serial fixture cleanup. Case pools remain fresh; SCRAM authentication,
startup configuration, fixture contexts and verification deadlines are unchanged.

`TestReferenceFixtureSessionAuthorizationReset` retains its schema-owner and
empty-public assertions and now deliberately contaminates authorization, SET
ROLE, settings, TEMP, named PREPARE, advisory state and an aborted transaction
before both reset boundaries. The same definition runs without Docker, proving
fresh wrapped/native control settings, identities, prepared consumers and case
catalogs; admin-only sanitation checks run in pinned mode. It does not assert
incidental backend PIDs, reconnection counts or timing. Focused gates, each with
a 900-second deadline:
`make check RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`
and
`make check REFERENCE=1 RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`.

Every pair, including legacy `fixture.NewPair(t)`, owns a dedicated native
`Driver`. Its wrapped and native control pools share the exact fixture DSN;
closing and reopening a pool on that same Driver/DSN preserves its engine.
Legacy recorder provenance remains unchanged. Legacy notice helpers preserve
their caller-specific `reference/` + `t.Name()` DSNs on the pair's Driver:
equal caller names share state, different names remain isolated. Modern
scenario notice helpers retain the parent connector. Neither kind retains
finished fixture engines in the process-registered Driver. The explicit
registered-driver BYTEA ownership witness is intentionally unchanged.

Fixture ownership regressions live beside the common exported helpers in
`reference/fixture/fixture_native_isolation_test.go` and
`reference/fixture/fixture_notice_ownership_test.go`. These identical definitions
run in both modes (900 seconds per invocation):

```sh
make check RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
make check REFERENCE=1 RUN='^TestReferenceFixture(NativeIsolation|LegacyNoticeOwnership|ByteOwnership|ResetIsolation|SessionAuthorizationReset)$'
```

These selectors do not replace whole-project Docker-free `make quickcheck`
verification or post-merge `make finalcheck`. Use `RACE=1` on a selected
`make check` command for bounded race proof, and `REFERENCE=1 RACE=1` for the
same definitions against pinned PostgreSQL. Reduced engine retention is an
ownership correction, not evidence of a measured performance improvement.

Non-public schemas are not reset by the fixture. Register
`fixture.SchemaReferenceCleanup(t, "schema_name")` before constructing the scenario
when a case creates or moves objects into another schema; otherwise a later
case can fail with `42P06` even when it passes in isolation.
Dropping a moved view does not remove its destination schema. Prepared utility
and native replay cases must register that schema too, even when they drop every
view they created. Registering before the scenario lets fixture handles close
before schema teardown.

Queries of `pg_class` are database-wide across namespaces, not limited to the fixture's `public` reset.
Use case-specific relation names or constrain the intended namespaces when
asserting a complete candidate bag; common names such as `t` can match unrelated
objects outside the case even when its own schema cleanup is correct.
Preserve exact multiplicities and visibility assertions rather than ignoring
extra identities. The visibility source-route scenario keeps the complete
unfiltered two-peer bag and creates an unrelated `public.t` to exercise
isolation from the old generic basename.

`p.Step(t, step)` executes one description on native and, when enabled,
PostgreSQL. Each backend must match the supplied expectations independently;
complete observed records are then compared for parity. SELECT checks include
column names, database type names, complete rows and owned middleware-recorded
rows/closure. Raw codec representations are checked before normalization.

Use `p.Step` for straightforward streams. Use `p.Backends()` and one shared
callback for transactions, pinned sessions, held statements, richer metadata,
concurrency or initial oracle discovery. Keep SQL session state on a borrowed
`*sql.Conn` or `*sql.Tx`, not an arbitrary pooled connection. Close rows before
statements, then return connections before fixture/schema cleanup. Register
cleanup immediately and cancel/join goroutines on all exits; use deterministic
synchronization, not sleeps.

The whole-root cutover is complete: root retains13 focused nonSQL units and its
startup benchmark, with no public SQL test bodies or obsolete SQL helpers.
The final driver/session/catalog ledger accounts for372 original sources and131
helper dispositions, with651 shared untagged definitions,39 genuine PostgreSQL-only
and five PostgreSQL-required differential companions.36 genuine-only and all five
differential definitions remain tagged; three genuine-only callers are untagged.
Use [family commands](DEVELOPMENT.md#driver-session-and-catalog-whole-root-cutover)
for exploration and the catalog's identical exact651-name RUN for native,
PostgreSQL and race acceptance. Select companions separately in real mode.

Native eager retained cursors and detached Columns slices are separate contracts
from pq streaming cursors and aliased Columns slices. Shared metadata callbacks
run before iteration, including empty results; raw cells are asserted before
normalization. Native precanceled/expired routes retain ordinary context errors.
For pq user cancellation use observed server admission and joined completion,
then check the SAME physical owner and preheld consumers. Do not assert a
race-prone fast precanceled pq command must return nil outputs, or cancel its
Go context during a notice callback and invent stable transport health.

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

The new timeout family's runtime delay witnesses use half-scale budgets/stimuli
to keep complete pinned race coverage bounded without dropping cases: ordinary
250ms budgets cross at350ms; activity resets occur at125ms; shorter limits125ms
remain below250ms; CHAIN uses fresh500ms and300ms steps; the old500ms budget
after300ms has200ms residual, beating a newly configured375ms limit. Abort
recovery waits1100ms beyond suppressed1s idle and600ms beyond restored500ms.
Active-idle limits remain100ms while actual lock work lasts250ms (2.5 limits);
CPU parse witnesses retain1ms. Registry normalization/range inputs, observer
polls, asynchronous joins, context/watchdog bounds and all diagnostic/state/
atomicity assertions are unchanged. This timing-only scale is not a deadline
increase or readiness-only substitute. The separate public executable retains
its independently specified100ms/300ms budgets and650ms idle stimuli.

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

The superseded `TestSettingsSmoke` is removed; its typed registry, numeric,
metadata, notice/reentry, parser capture, access and connection-lifecycle
obligations remain in the existing shared `TestReferenceSettings*` consumers.
The superseded `TestNullCountSmoke` is removed. Its literal/table counts, raw
INT4 metadata and argument-count sums, nested calls, held parameter reuse and
empty-result metadata, Exec counts and complete diagnostic/nil-output contracts
remain in the shared `TestReferenceNullCount*` families. Former no-FROM rejection
cases now assert shared success; `TestReferenceNullCountInsertConsumerRoutes`
retains the original INSERT Query/Prepare routes and exact resulting state.
Malformed suffixes retain whole-input/no-prefix-effects rejection coverage,
while actual unsupported array/function-provider forms remain boundaries.


Original source methods are obligations, not interchangeable API labels.
For proven DB/Tx plain calls, set fixture.Step.NonContext=true on the existing
runner: actual Exec/Query/Prepare and returned Stmt.Exec/Query retain complete
count/error-stage/nil-output/cursor assertions. Metadata and specialized Scan
hooks inspect the actual source cursor before the truthful return preceding
Context-only recording. Keep a fresh recorded Context companion; never fabricate
its recorder key for a plain call. *sql.Conn has no plain methods: original
Conn.QueryContext and physical-owner adapters remain actual Conn routes.
Explicit QueryRow/Begin/native raw calls and SAME held objects stay explicit.
fixture.Prepared prepares anew; it does not prove quiet Prepare or held reuse.
Consumers such as fixture.DeleteReferenceConsume do not invoke step metadata/Scan
callbacks, so preserve those original checks at the callsite. Cleanup follows
rows, statements, rollback, borrowed connections, then fixture/schema/role reset;
worker joins and registered failure exits are part of the assertion union.



### Common helper reference

Import common helpers explicitly as `fixture` from
`"stuntgres/reference/fixture"`; do not use dot imports or package-local
compatibility aliases. Each feature-group package's `TestMain` must call
`os.Exit(fixture.Main(m))` to own its startup and teardown.

When moving or exporting helpers, update runtime-frame admission probes to the
actual new symbol name. `fixture.BinaryConversionActiveCancel` requires both the
provider and its worker frame before cancellation; preserve that gate, workload,
cleanup and assertions rather than weakening them to make a migration pass.

| Helper | Contract and use |
| --- | --- |
| `fixture.NewScenario(t) *fixture.Pair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `fixture.NewPair(t)`, which always starts Docker. |
| `fixture.NewFixture(t, withPostgres, nativeRaw) *fixture.Pair` | Low-level fixture seam for ownership/provenance tests. Every pair owns a dedicated native Driver. `withPostgres` controls oracle startup; `nativeRaw` selects native/PostgreSQL raw recorder provenance, not Driver ownership. False retains exact legacy raw semantics. Prefer `fixture.NewScenario` for feature tests. |
| `p.Step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.Backends()` | Returns backend descriptors with `Name`, `DB` (`*sql.DB`), `Recorder`, and `PG` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.Ctx`, `p.Native`, `p.NativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.Memory` and `p.Postgres` are the wrapped pools; `p.Postgres` is nil in quick mode. |
| `fixture.Exec(query, count, args...) fixture.Step` | Exec success with exact `RowsAffected` and unsupported `LastInsertId`; DDL generally expects zero. Arguments are bound normally. Error steps require nil published results. |
| `fixture.Select(query, columns, types, rows, args...) fixture.Step` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `fixture.Prepared(s) fixture.Step` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `fixture.Error(query, state, args...) fixture.Step` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `fixture.ErrorDiagnostic(query, want, args...) fixture.Step` | Exec failure with a complete `fixture.Diagnostic` expectation, including exact empty fields. |
| `fixture.ImplicitError(query, want, args...) fixture.Step` | As above, filling both severity fields with `ERROR`. |
| `fixture.Position(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `fixture.ControlsReferenceDiagnostic(code, message) fixture.Diagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `fixture.RunStep(t, ctx, executor, recorder, s) fixture.Observed` | Run standard independent assertions on a chosen backend/session. Context steps accept DB/Conn/Tx; proven `s.NonContext=true` requires actual DB/Tx plain methods and cannot use Conn or a fake forwarding adapter. Actual cursor callbacks remain checked before returning without Context-only recording. Does not compare backends for you. |
| `fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `fixture.RunStep`; leave `s.Prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `fixture.State(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `fixture.ExtractDiagnostic(t, err) fixture.Diagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `fixture.AssertDiagnostic(t, err, &want) fixture.Diagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `fixture.Normalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `fixture.NoticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.PG`; fixture registers cleanup. Native legacy provenance preserves the exact caller `reference/` + `t.Name()` DSN on the pair-owned Driver; scenario provenance shares the parent connector even for child names. The native pair must have an owning connector; there is no registered-driver fallback. |
| `fixture.SessionCancel(t, parent, conn, prepared, queryMode...)` | Observe real PostgreSQL pg_sleep admission, cancel through the server, join the operation and assert full57014. Optional true queryMode selects Query with nil error cursor; otherwise Exec. The caller asserts SAME-owner continuation; this is not a native precancel substitute. |
| `fixture.RoleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

Register role cleanup on the same `testing.T` that owns the scenario, before
constructing its pair. A child subtest using a parent's pair must not register
role cleanup on the child: child cleanup runs while parent-owned tables/ACLs
still exist. The parent owns role removal after its physical handles and schema
have been cleaned up; consumer subtests retain all assertions and reset identity.

### Step fields and expected data

- `fixture.Step.Query`, `Args`, `Prepared`, `SelectRows`, `Affected`, `State`,
  `Columns`, `Types` and `Rows` are populated by the constructors above.
- Set `s.Bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
- Set `s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ... }` for complete
  raw descriptor contracts on the actual result, before iteration even when
  empty. Retain independent backend ScanType/Nullable/Length/DecimalSize flags.
- Set `s.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) { ... }` to
  rescan the current row into specialized destinations. It must not issue SQL.
  Use a shared backend callback for metadata contracts beyond database type names.
- For Query errors, start with `fixture.ErrorDiagnostic`, then set
  `s.SelectRows = true`. Set `s.ErrorStage` to `"prepare"` or `"execute"` when
  stage matters; this field is checked only with a structured diagnostic.
- `s.PostgresDiagnostic` permits an independently specified PostgreSQL diagnostic
  for intentional differences. Both full records are asserted first; only
  Position/Context are normalized for subsequent parity. This is not permission
  to suppress unrelated differences.
- `fixture.Diagnostic` fields are `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unspecified
  fields must actually be empty; source File/Line/Routine are not reference data.
- Integer cells normally use `int64`, floating cells `float64`, Boolean cells
  `bool`, NULL `nil`; normalized byte payloads use strings. Do not confuse
  `ColumnType.ScanType` with the actual raw scanned value. Temporal/OID and other
  codec differences can require independently captured backend expectations;
  branch on `backend.PG` for those expectations, not to duplicate the scenario.

## Self-contained example

Place this untagged file in `reference/query_core/feature_answer_test.go`, using
that group's existing external test package and `TestMain`. New packages need a
`TestMain` calling `os.Exit(fixture.Main(m))`; do not duplicate an existing one.
The literal expectations illustrate the final recorded form; when adding real
functionality, validate its own expectations against the pinned PostgreSQL run
first.

```go
package reference_test

import (
    "testing"

    "stuntgres/reference/fixture"
)

func TestReferenceFeatureAnswer(t *testing.T) {
    p := fixture.NewScenario(t)
    for _, backend := range p.Backends() {
        t.Run(backend.Name, func(t *testing.T) {
            // During oracle discovery only, temporarily select backend.PG
            // before this callback. The completed test runs every backend.
            conn, err := backend.DB.Conn(p.Ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s fixture.Step) {
                fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
            }

            run(fixture.Exec("CREATE TABLE answer (v INTEGER)", 0))
            run(fixture.Exec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := fixture.Select(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = fixture.Prepared(s)
                }
                run(s)
            }
            run(fixture.Error("SELECT absent FROM answer", "42703"))
        })
    }
}
```

The shared callback creates independent state on each backend. A pinned
connection owns the session, and both direct and prepared queries check exact
values, NULL propagation and metadata through the same step description. The
SQLSTATE assertion is a separate failure witness. Standard steps also check
owned middleware rows and closure; the fixture closes its pools after the
borrowed connection is returned.

Run `make check REFERENCE=1 RUN='^TestReferenceFeatureAnswer$'` first, then
`make check RUN='^TestReferenceFeatureAnswer$'`. After any
test or implementation changes, rerun both selectively before review, then run
ordinary quickcheck and the post-integration finalcheck.

## Allocation checks

For package-function byte-allocation budgets, use calibrated
`testing.Benchmark` measurements and normalize `AllocedBytesPerOp` to one
provider evaluation when a benchmark iteration evaluates a batch.
Short process-wide `runtime.MemStats.TotalAlloc` samples can charge unrelated
runtime allocations to the provider, especially under the race detector.
Keep independently chosen payload-size and allocation-count bounds unchanged.

## Optional last-resort implementation materials

This guide is sufficient for ordinary dual-mode tests. Do **not** read other
feature tests to discover the basic convention. Only when a required advanced
fixture behavior is not covered above, optionally consult:

- `reference/fixture/fixture.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture/lifecycle.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.

## Subquery regression and instrumentation coverage

`reference/query_core/query_subquery_*_test.go` contains the shared direct/prepared,
held-handle, mutation, metadata, admission, locking and stored-view scenarios.
Both backends execute the same callback without a discovery-only backend filter:

```sh
make check RUN='^TestReferenceQuerySubquery'
make check REFERENCE=1 RUN='^TestReferenceQuerySubquery'
```

The integration handoff additionally supplies the exact selector for migrated
consumer/no-FROM families; those families are not replaced by the new prefix.
Each selective invocation uses a900-second deadline, in both modes before review.

Internal `TestQuerySubquery*` cases instrument actual bound plans, typed
comparison providers, lexical frame lookup, admission manifests and borrowed
stage cleanup. Sentinels prove ignored EXISTS targets versus required ORDER
keys/derived projections and normal target demand. They are not duplicate
public SQL feature fixtures and do not use fake database APIs. Parser
`TestQuerySubqueryAST` / `TestQuerySubquerySourceWalk` check structural syntax,
positions, source occurrence order and child-specific rowmark ownership.
Run those private units with
`make check RUN='^TestQuery(Subquery|DerivedSubquerySyntax)'`.


## Cast metadata catalog discovery and smoke

The shared `TestReferencePGCast` family asserts the complete independently
specified69-row supported boundary, exact six-column shape and raw codecs,
fixed type/function identities, generated row identity invariants, direct/
prepared/held consumers, full diagnostics, omissions versus automatic coercion,
PUBLIC SELECT and native retained-row/Reset/Restore controls. PostgreSQL reads
are restricted to the same candidate pairs; additional PostgreSQL catalog rows
are legitimate. Domain identity via pg_typeof is a PostgreSQL-only phase;
native domain creation/conversion plus its unchanged exact complete bag excludes
domain rows without a new type identity API.

The genuine feature smoke combines empty shape, representative eight-row
metadata, held valid/missing/NULL/error/recovery, signed OID conversion, BOOL
text, JSON I/O, VARCHAR sizing/atomicity, UTF8 diagnostic and actual native user
Dump replay against PostgreSQL. Discovery-only filters have been removed.
Run the same completed definitions in both modes before review/integration:

```sh
make check RUN='^TestReferencePGCast'
make check REFERENCE=1 RUN='^TestReferencePGCast'
make check RUN='^TestReferencePGCastFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCastFeatureSmoke$'
make check RUN='^TestCastCatalog'
make check RUN='^TestPGCast'
make check RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
make check REFERENCE=1 RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
```

Every quickcheck invocation has a900-second deadline. Private units exercise
owner planning/application in all claimed and excluded contexts and projection
ownership/cancellation/generation retention, without issuing SQL. Selective
runs and smoke do not replace ordinary quickcheck or the post-merge finalcheck
under its single1800-second deadline.

## Numeric SUM reference contract

The untagged `TestReferenceNumericSum*` family covers all six numeric overloads,
typed NULL and nullable/promoted metadata, exact integral and decimal boundaries,
same-precision ordered floating transitions, signed zero and specials, full
diagnostics, held parameter recovery, modifiers and consumers, eager write
atomicity, authorization, cancellation and stored view control round trips.
Native control phases additionally restore into an independent Driver and replay
the actual emitted SQL on PostgreSQL; they do not invent PostgreSQL controls.
Column GRANT is supported; explicit-ACL native Dump remains the control boundary.

Run the identical completed definitions in both modes, each under a900-second
deadline. The broad migration/risk selector covers every changed behavioral
definition plus retained RETURNING restrictions and shared AVG-state regressions;
the separate SSI selector covers its comment-only maintenance. Avoid interior
`$` anchors in `RUN`: make consumes them even inside shell single quotes.

```sh
make check RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)'
make check REFERENCE=1 RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)'
make check RUN='^TestReferenceNumericSumSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericSumSmoke$'
make check RUN='^TestReferenceSerializableWriteSkew$'
make check REFERENCE=1 RUN='^TestReferenceSerializableWriteSkew$'
```

Numeric SUM followed the oracle-first order above: root recorded the pinned
PostgreSQL contract before authorizing production after native behavioral red.
Discovery filters are removed from the completed definitions; never reintroduce
them or adapt independently observed expectations to native output. FLOAT4 pq
ScanType is float32 while native exports float64. A later-group overflow
opening witness uses ORDER BY aggregate result, then grouping key, to force a
blocking sort before any PostgreSQL cursor rows can be published.

The `TestReferenceNumericSumReview*` regressions preserve complete direct/prepared
Exec/Query diagnostics and Unicode positions. INSERT VALUES resolves children,
FILTER and signatures before rejecting a valid numeric SUM; a valid first VALUES
row remains unpublished when a later SUM expression fails. Domain signatures
distinguish visible unqualified names from hidden qualified names, while valid
numeric-domain input returns exact NUMERIC4.656 with promoted metadata. Missing
signatures with multiple in-call ORDER BY keys use the misplaced-ORDER-BY Hint;
ambiguity, child/FILTER precedence and successfully resolved ordered calls retain
their own contracts. Root validated these new cases on pinned PostgreSQL before
removing their discovery filter and observing native behavioral red:

```sh
make check RUN='^TestReferenceNumericSumReview'
make check REFERENCE=1 RUN='^TestReferenceNumericSumReview'
```

Both completed-mode runs require separate900-second deadlines after remedies;
neither the oracle/red checkpoint nor prior family passes establishes closure.

SUM child analysis uses the empty VALUES source namespace, not the destination
table's columns. The destination remains an inaccessible diagnostic candidate:
`sum(i)` reports42703 with the independently observed destination-column Detail,
not a successful source read. FILTER type errors beneath CAST and nested scalar
wrappers retain their own complete records instead of becoming target-assignment
errors. An unrelated BOOLEAN-to-INTEGER assignment control retains its ordinary
column-type message, rewrite/cast Hint and operand position. These additions
followed a separate pinned oracle/native-red round; unrelated scalar admission
and assignment diagnostics keep their existing boundaries.

Private units supplement the public contract with shared exact-state/AVG
regressions, zero-allocation integral transitions, same-precision/signed-zero
transitions, old/new-generation remapping, actual provider cancellation checks,
late/prepublication eager cancellation and exact column-ACL manifests:

```sh
make check RUN='^Test(NumericSum|NumericAvg)'
make check RUN='^Test(Sum|NumericSum|NumericAvg|Avg|UpdateReturningMalformedSumShape)'
make check RUN='^Test(Sum|NumericSum|UpdateReturningAggregate)'
make quickcheck
```

Focused smoke expects direct/prepared six outputs4, promoted nullable metadata,
typed empty/all-NULL results, exact INT8 max-pair NUMERIC, decimal4.656/0.0000,
ordered REAL0, negative zero, held parameter recovery, full diagnostics/no
partial effects, global INSERT affected1 versus empty keyed0, and SUM view
control replay with retained metadata. Root runs selective checks under separate
900-second deadlines after generation/formatting, and finalcheck only after merge
under one1800-second deadline.

## Numeric arithmetic oracle-first contract

The `TestReferenceNumericArithmetic` family exercises exact decimal `+`, unary
`+`, `-`, unary `-`, `*`, `/`, `%` and integer/fractional `^`, with independent
finite strings, published PostgreSQL 18 special matrices, pinned high-precision
power strings, full diagnostics, direct/held inputs, computed NUMERIC metadata,
statement/TX atomicity, precancellation and symbolic native control replay.
`TestReferenceArithmeticBoundaries` migrates the former numeric exclusions to
shared direct/prepared successes. These test definitions do not themselves
claim native support.

The permanent expectations include observed PostgreSQL18.0 precision/scale and
complete binding/input diagnostics. The general-power guard success asserts the
full 2609-byte result through SHA256, plus exact scale/weight/prefix/suffix.
Unknown results were resolved in oracle-first discovery; no backend filters,
observation failures or numeric tolerances remain. Deparsed DEFAULT/view
expressions are replayed to assert left64/right512 semantically rather than
pinning incidental SQL whitespace. Both backends expose the actual unnamed
portal parameter Context for invalid numeric binds.
Zero-argument native Query retains its existing42P08 parameter-typing record;
pq's simple-query protocol reports42P02 instead. Supplied-argument direct and
Prepare routes independently require42P08, not protocol emulation.

Root runs each invocation under a separate 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
make check RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check REFERENCE=1 RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check RUN='^Test(NumericArithmetic|NumericPower|NumericAvg|MathScalar)'
make check RUN='^TestExpressionNumericArithmetic'
```

The composed feature smoke demands exact0.3, large integer9223372036854775809,
product2.76000, division2.5000000000000000, remainder−1.00, typed left64/right512
with NUMERIC metadata, published fractional18.379173679952560, fresh held input
scales/NULLs, domain/zero/format-overflow diagnostics and unchanged destinations
after late failures. Its native phase additionally checks deterministic
Dump/Reset/Restore, preserved cursors and symbolic DEFAULT/view/CHECK grouping.

## Role membership oracle-first definitions

The shared `TestReferenceRoleMembership` family includes the migrated dependency
orders, option defaults and omission updates, inherited owner access, grantor
provenance, ADMIN delegation and support cycles, initial memberships, aliases,
dependent revocation, diagnostics and notices, prepared permission freshness,
transaction boundaries and native host-control phases. PostgreSQL catalog
inspection supplements shared SQL witnesses; it does not replace them.

Initial PostgreSQL18.0 oracle discovery passed before production changes.
The temporary discovery selector is removed; completed quick and real-mode
checks execute the same shared scenario bodies. Discovery observations do not
replace native behavioral-red or final dual-mode validation.

Run every invocation under its own 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceRoleMembership'
make check RUN='^TestReferenceRoleMembership'
make check RUN='^TestReferenceRoleMembershipFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRoleMembershipFeatureSmoke$'
make check RUN='^Test(Membership|Role|Authorization|SessionIdentity|ViewReplace)'
```

The feature smoke requires inherited owner row7 with INT4 metadata versus
42501 denial, ADMIN delegation versus denial, atomic RESTRICT and recursive
CASCADE, combined initial clauses, ALTER GROUP add/drop, disabled-option cycle
rejection and PUBLIC lookup rejection. Explicit native transaction/control
phases document actual API boundaries rather than skipping shared SQL behavior.

Inherited schema ownership is established by shared privileged
`CREATE SCHEMA ... AUTHORIZATION owner`, not a native database-GRANT API.
Functional failure paths remove the nonpublic fixture schema before roles.
`SchemaOwnerSequenceDrop` additionally distinguishes inherited schema DROP
authority over another role's sequence from sequence-owner-only ALTER rights.
Type ownership uses supported DROP DOMAIN; excluded DROP/ALTER TYPE is not
enabled merely to provide a membership witness.
Keyword-promotion regression checks retain the existing CHECK/NOT NULL
`NO INHERIT` consumers and genuinely unsupported role options, not selected attributes.
Class-mask units stage immutable type ACL descriptors through catalog validation,
as existing domain fixtures do, without extending the production ACL setter;
they include invalid-mask nonpublication and explicit-empty inherited-owner
grant-option assertions.
Internal planner units supplement public SQL with immutable edge copies,
exact option flags, selected-origin CASCADE, invalid identity/revision guards,
cancelled candidates, concurrent cycle publication, inherited class masks,
explicit-empty owner use versus implicit options and exact column ACL unions.
The shared `Batch` regressions additionally cover GRANT current-candidate
grantor freshness and rollback, all-recipient GRANT sanity before ADMIN/notices,
and forward/reverse RESTRICT warning order. REVOKE authority uses statement-entry
visibility: its successful batch removes p, warns for q's missing attributed row,
then a separate q revoke denies ADMIN authority. Repeated REVOKE targets that
change the same original tuple fail XX000 `tuple already updated by self`,
emit no missing-row warning, and preserve the original membership and owner row7;
a later single-target revoke succeeds. These corrected contracts were observed
on pinned PostgreSQL18.0 before the native remedy; final remedy verification
remains root-owned and is not implied by those discovery passes.
Positive post-failure delegation, normal notices and CASCADE witnesses ensure
errors neither suppress legitimate messages nor alter the original graph.
Internal ordered-candidate units check GRANT grantor selection, nonpublication
and immutable reads. Entry-authority units check the real explicit-grantor
REVOKE row, one publication and next-statement denial. Indexed-action units
cover recursively deleted later recipients, within-target duplicate matches,
cross-target tuple-action overlap, quiet ADMIN FALSE no-ops, repeated absent
warnings and independent origins. INHERIT/SET actions remain updates even when
already false; do not incorrectly treat them as ADMIN-style no-ops.

```sh
make check RUN='^TestReferenceRoleMembershipBatch'
make check REFERENCE=1 RUN='^TestReferenceRoleMembershipBatch'
```
The full-role consumer selector must also run in both reference modes:

```sh
make check RUN='^TestReference(Role|SessionIdentity|ViewReplace)'
make check REFERENCE=1 RUN='^TestReference(Role|SessionIdentity|ViewReplace)'
```

## Session advisory lock reference contract

The untagged `TestReferenceAdvisorySession` family uses the same completed public
definitions in both modes. It enumerates thirteen signatures through direct
Query/Exec and genuinely held prepared handles; validates raw VOID/BOOL/NULL and
metadata; and independently checks disjoint keys, mode/count matrices, complete
warnings, rollback/pooling/physical close, wait/cancel/timeout/deadlock, CHECK
runtime demand and native Reset/Dump/Restore staging. Stored CHECK/view SQL is not
live ownership serialization, and Restore's direct scalar allowlist stays closed.

`TestReferenceAdvisorySessionReviewCheckPreparation` distinguishes constant-NULL
suppression from runtime-column-NULL demand across writes, held prepared statements,
ALTER rewrites and native staged replay. It checks quiet complete warning capture,
peer-held blocking keys, unchanged counted ownership and execution-stage immutable
child errors before any volatile CHECK runs. `...ReviewQuotedArgumentLimit` asserts
all seven quoted nonexact spellings and qualification through direct/real Prepare,
including complete54023 diagnostics and earlier bad-child42703 source positions.
Private `TestAdvisorySessionCheckPreparationCache` and
`...CheckPrepareAllBeforeEvaluation` supplement the public outcomes with command/
descriptor cache isolation, copy-on-change syntax preservation, row-frame restoration
and complete immutable-child diagnostics. `TestCheckAbsentValidatorHasNoAllocation`
retains the no-CHECK allocation regression.

Root first resolves any new unknown result on pinned PostgreSQL18.0, then runs
both complete modes after changes. Discovery filters/logging are temporary only;
native controls are explicit native phases, not invented PostgreSQL equivalents.
Exact PostgreSQL pending evidence uses a narrow advisory `pg_locks` observer and
`pg_blocking_pids`. Native public outcomes are paired with private exact under-mutex
queue/fence tests; worker-start or elapsed sleep is never registration proof.
Every worker owns cancellation and bounded join before physical/schema cleanup.
Prior advisory holds survive deadlock/timeout/rollback; cleanup must explicitly
unlock or close them before requiring a blocked survivor to finish.

Each selected check invocation has a900second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionCheckConsumers'
make check RUN='^TestReferenceAdvisorySession'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySession'
make check RUN='^TestReferenceAdvisorySessionReview'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionReview'
make check RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)'
make check REFERENCE=1 RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)'
make check RUN='^TestAdvisorySession'
make check RACE=1 RUN='^Test(AdvisorySession|RelationLock|RelationDeadlock)'
make check RUN='^TestReferenceAdvisorySessionFeatureSmoke'
make check REFERENCE=1 RUN='^TestReferenceAdvisorySessionFeatureSmoke'
```

The feature smoke requires double prepared acquisition42, rollback survival,
peer exclusion until the second matching decrement, exact unmatched warning,
shared pair coexistence and bigint independence, real timeout/wake outcomes and
empty unlock-all. Native phases additionally require retained counts and data
through actual controls. Private ownership/graph/binding/parser tests supplement,
not replace, the identical public contract. Finalcheck runs only after merge
under one1800second deadline.


## BYTEA access oracle-first contract

`TestReferenceBinaryAccess` defines the BYTEA `bit_count`, `get_bit`,
`get_byte`, `set_bit`, `set_byte` and existing `reverse` contract. Independent
release/manual and raw-octet goldens precede comparison: LSB-first bit offsets,
INT8 bit indexes/counts versus INT4 byte indexes/getters, low-eight-bit byte
replacement, strict NULL, source ownership, complete errors, overload boundaries,
authorization, metadata, statement/savepoint atomicity and stored consumers.
The existing StringPad reverse definitions remain unchanged.

During initial discovery only, the shared scenario helper temporarily excludes
native backends. This is not native support or passing verification: remove that
filter after pinned PostgreSQL observations and before the native failing proof,
and never integrate the filter. Body-error stages, portal input Context,
ambiguous overloads, diagnostic precedence and complete metadata flags require
root observations before production implementation. Native controls are defined
now but are not exercised by the PostgreSQL-only discovery phase.

Run each command with a separate 900-second deadline, in this order:

```sh
make check REFERENCE=1 RUN='^TestReferenceBinaryAccess.*$'
# After removing the temporary filter: establish native failing proof.
make check RUN='^TestReferenceBinaryAccess.*$'
# After implementation: both completed shared definitions and reverse regression union.
make check RUN='^TestReference(BinaryAccess|StringPad).*$'
make check REFERENCE=1 RUN='^TestReference(BinaryAccess|StringPad).*$'
make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryAccessFeatureSmoke$'
```

The composed smoke asserts the six-function released projection, orientation and
wrapping, raw held frames and unchanged storage, exact bounds/newbit errors with
recovery, empty/NULL metadata before iteration and symbolic native controls with
actual emitted SQL replay on PostgreSQL. Control replacement failures must leave
the destination dump byte-for-byte unchanged; parameters in every new argument
slot remain rejected. Quiet test success alone is not a discovery transcript.

## Grouping sets regression-first contract

The shared untagged `TestReferenceGroupingSets` families independently assert
ROLLUP/CUBE/nested expansion, ALL/DISTINCT multiplicities, placeholder NULLs and
GROUPING bits, query ownership, EXISTS, limits, full diagnostics, consumers,
metadata, statement atomicity, cancellation and stored-definition/native controls.
`TestReferenceCountExpressionCapabilityBoundaries` now treats grouping constructs
as shared success contracts; `TestReferenceQueryAggregateExclusionPrefixEffects`
uses malformed grouping suffixes while retaining the unrelated window boundary.
The pinned tests-only contract and native-red checkpoints precede production.
The original discovery filter was removed; final scenarios must visit both backends.
Final family selectors (900 seconds each):

```sh
make check REFERENCE=1 RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$'
make check RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$'
make check REFERENCE=1 RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
make check RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
```

Root removed the initial PostgreSQL-only discovery loop before native red.
Pinned discovery supplies positions, prepared phases and raw descriptor flags;
new or changed witnesses remain unobserved until their next pinned run. Full
exposed diagnostics are asserted rather than stripped. Discovery established
position8 for an ungrouped `a` projection with legal `GROUP BY ()`, and absent
positions for explicit-empty rowmarks and over-limit CUBE Cartesian products.

An empty-call syntax-position override applies only to malformed42601 calls,
not a legal empty grouping clause. Atomicity Query witnesses divide by zero on
every group, while INSERT retains late-failing expressions and unchanged-state
assertions. An original late SELECT returning from `QueryContext` successfully
does not establish completed SQL success or NULL results: pq may return a
cursor before a subsequent server error. Native eager late SELECTs separately
assert the stronger nil-cursor contract; it is not fabricated as pq parity.

Grouped-query PostgreSQL cancellation prepares before an ACCESS EXCLUSIVE peer
lock, observes the actual relation waiter/blocker and exact active query, server
cancels, joins with a deadline, asserts nil cursor/full57014, then reruns the
same held owner. This proves admitted-query cancellation, not interruption of
group execution loops. Native precancellation likewise only proves publication
and recovery. Package units cover structural AST/source traversal/whole input,
canonical expansion/raw limits, masks/outer frames/no argument evaluation,
occurrence state/modifier evaluation, actual fanout/family/finalization/merge
cancellation, column ACL manifests, physical output origins and Restore whitelist.
No production hook or pg_sleep substitute satisfies active-loop obligations.

Internal selectors for root verification:
`make check RUN='^Test(GroupingSets|QueryAggregate|AggregateModifiers)'`.
Native control branches remain in shared public scenarios. The typed lifecycle
uses admitted unconstrained domains; illegal RETURNING grouping uses the admitted
UPDATE route, not an expansion of the INSERT RETURNING boundary.

### Blocking-review regression contracts

`TestReferenceGroupingSetsReview.*` supplies illegal producer-child syntax and
whole-input mutation-prefix rejection, ordinary nested unreserved function-name
ownership, typed supplied keys, compound parameter keys, equal-value distinct
parameter identities, held non-NULL/NULL reuse, grouped INSERT counts and
NOT NULL atomicity with same-owner held recovery. Exact bags, types and metadata
are independent expectations. Root's pinned Review checkpoint passed these new
diagnostics, character positions, supplied-parameter rows and metadata.

The temporary new-family-only PG discovery helper was removed and every caller
migrated to `groupingSetsScenario` before native red. No grouping discovery filter
remains. Native red found the producer-child/function admission bug; parameter,
identity and INSERT scenarios passed. The grammar remedy uses expression-only
ROLLUP/CUBE units, preserving implicit sublists and recursive GROUPING SETS.
Root must freshly validate the completed definitions in both modes, under a
separate900-second deadline per invocation:

```sh
make check REFERENCE=1 RUN='^TestReferenceGroupingSetsReview.*$'
# Both backends execute shared definitions; no temporary discovery helper.
make check RUN='^TestReferenceGroupingSetsReview.*$'
```

Final complete-family selectors above include every changed/new definition.
The review smoke adds a valid composite producer, forbidden empty/SETS children
with prefix atomicity, supplied held7/NULL/7 masks and metadata, and a prepared
parameterized grouped INSERT. Ordinary nested cube/rollup calls retain the
existing native generic-call0A000 boundary versus PostgreSQL42883 with Hint;
this does not admit new scalar functions or syntactically reject their names.
Parser units supplement public contracts with positioned expression-unit ASTs,
legal nested SETS/producers and bare names, full malformed-child diagnostics,
whole-input zero-statement publication and inner ordinary-call capability ownership.
These new units and the final grammar remedy require root verification; the
historical oracle pass/native red are not final passing evidence.

## SQL SIMILAR oracle and feature checks

The untagged `TestReferenceSimilar` families share independent direct/prepared
expectations for SQL matching/extraction, classes/escapes/capture, diagnostic
fields/stage/positions, metadata/deparse, demand/held parameters, consumers,
stored atomicity and public replay. Discovery-only backend filters and failure
logging were removed after the root's pinned PostgreSQL18.0 observations.
Review regressions are `TestReferenceSimilarReviewDefault`,
`TestReferenceSimilarReviewMalformedDefault`,
`TestReferenceSimilarReviewDefaultControls`,
`TestReferenceSimilarReviewDefaultBoundaries`,
`TestReferenceSimilarReviewGroupIdentity` and
`TestReferenceSimilarReviewTaggedIterator`. They encode the pinned default
diagnostic, all sixteen cross-spelling grouping pairs and eight exact
nullable-iterator/backreference capture outcomes. Native-only DEFAULT boundaries
and host controls are explicit; the controls replay actual emitted SQL on the
pinned backend. `TestSimilarSubstringPresentationIdentity`,
`TestPGRegexExecutionReplacementOwnership` and
`TestPGRegexExecutionReplacementBudget` are genuine structural/injected-limit
units; the latter cover configuration-preserving invalidation, preallocation
replacement ownership, translation/compile/runtime failure, NULL, cancellation
and recovery. They do not claim a PostgreSQL-identical resource cutoff.
`TestReferenceSimilarActualCancellation` executes the actual regex UPDATE;
server admission is observed before cancellation, and native cancellation
observes entry into translation/compiler/matcher work. It must not substitute
`pg_sleep`, a precanceled context or readiness for exercised cancellation.

Root runs both modes before review, each under the Makefile's 900-second bound:

```sh
make check RUN='^TestReferenceSimilar'
make check REFERENCE=1 RUN='^TestReferenceSimilar'
make check RUN='^TestReferenceSimilarFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSimilarFeatureSmoke$'
make check RUN='^TestReferenceSimilarReview'
make check REFERENCE=1 RUN='^TestReferenceSimilarReview'
make check RUN='^(TestSimilarSubstringPresentationIdentity|TestPGRegexExecutionReplacement(Ownership|Budget))$'
make check RUN='^TestReference(BinarySlice|LikeLegacy)'
make check REFERENCE=1 RUN='^TestReference(BinarySlice|LikeLegacy)'
```

The BinarySlice capability selector preserves positional TEXT
exclusions but admits two-TEXT POSIX and all three SQL-regex substring spellings. LIKE
legacy migration retains its independent matching/metadata/replay assertion
union. Genuine parser/provider/bound-node units supplement public reference
coverage; they must not recreate SQL-through-internal-engine tests. Native
resource-accounting units use injected limits rather than claiming a portable
PostgreSQL complexity cutoff. Finalcheck belongs to the root after merge
under one 1800-second deadline.

## POSIX regex oracle and feature checks

`TestReferencePosix` is backend-first and shares independent direct/prepared
expectations for search/case/negation/NULL, ARE/ERE/BRE, captures and hierarchical
preferences, brackets/escapes/constraints/lookaround/options/newlines, effective
collation/physical character types, full English diagnostic fields/stages/positions,
held inferred parameters, stored consumers and atomic native controls/PG replay.
Initial pinned18.0 discovery passed before native red authorized production;
no discovery backend filter is retained. Provider all-capture/span/budget and
parser/bound-node units supplement, not replace, these public expectations.

Root verifies after all provider/consumer edits land, with external900-second
deadlines (targets themselves own no deadline):

```sh
make check RUN='^TestReferencePosix'
make check REFERENCE=1 RUN='^TestReferencePosix'
make check RUN='^(TestPGRegex|TestPosix|TestSimilar|TestReferenceSimilar|TestReferenceCollation|TestReferenceJSONPath|TestReferenceBinary|TestReferenceLike)'
make check REFERENCE=1 RUN='^(TestReferenceSimilar|TestReferenceCollation|TestReferenceJSONPath|TestReferenceBinary|TestReferenceLike)'
make check RACE=1 RUN='^(TestPGRegex|TestPosix|TestReferencePosix(ActualCancellation|StatementTimeout|HeldParameters|Controls)|TestReferenceSimilarActualCancellation)'
make check RUN='^TestReferencePosixFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePosixFeatureSmoke$'
```

Review witnesses isolate constructor-specific titlecase closure and nested
assertion continuation without adding another public regex function:

```sh
make check RUN='^TestReferencePosix(TitlecaseConstructors|NestedAssertions)$'
make check REFERENCE=1 RUN='^TestReferencePosix(TitlecaseConstructors|NestedAssertions)$'
```

`TitlecaseConstructors` covers literal/plain bracket/equivalence versus explicit
singleton range/collating element for Ǆ/ǅ/ǆ under C, POSIX, both admitted Unicode
providers and their copies, including sensitive/insensitive negation and
two-TEXT substring. Independent REL_18_0 `regcomp.c`/`regc_locale.c` expectations
retain ǅ only in insensitive range/collating construction under Unicode; literal,
plain bracket and equivalence contain only its simple lower/upper counterparts.
`NestedAssertions` checks successful continuation and failing children, both
polarities, noncapturing assertion groups, variable-width lookbehind containing
lookahead beyond its endpoint, and UTF8 endpoints. The release `rege_dfa.c`
`lacon` evaluates child lookahead against the original source end, not the
parent lookbehind endpoint. These source-grounded expectations require both
selected native and pinned runs; native output is never the expected-value oracle.

Binary POSIX admission is arity-specific: unary `~`, including qualified forms,
remains the existing bitwise operator. Retain `TestBitwise` structural and
`TestReferenceBitwise`/`TestReferenceSearchPath.*Bitwise` public regression
coverage in both applicable modes. The migrated
`TestReferenceSimilarReviewDefaultBoundaries` independently expects the two-TEXT
substring DEFAULT to return `"a"`, while positional TEXT/BYTEA and unrelated
function DEFAULT exclusions remain native-only. Aggregate `min(substring(...))`
and pg_get_* inquiries retain native known-nullable metadata and PostgreSQL
adapter unknown nullability; regex expression metadata remains unknown.
Failed late Restore uses an admitted stored regex DEFAULT followed by INSERT,
not a scalar SELECT rejected by Restore's sequence-state whitelist.
Unary `~` also retains Restore's binder-owned malformed-type diagnostics.
POSIX substring occurrences carry independent stored-view provenance, so
ordinary pg_get_viewdef output quotes `"substring"` without relabeling it
SQL SIMILAR; pg_get_indexdef classifies the POSIX node as a function key without
an extra expression wrapper. Existing pinned definition goldens remain unchanged.

```sh
make check RUN='^(TestBitwise|TestDeparseConstraintsPersistedFamilies|TestReferenceBitwise|TestReferenceSmokeBitwiseSmoke|TestReferenceSearchPath.*Bitwise|TestReferenceSimilarReviewDefaultBoundaries)'
make check REFERENCE=1 RUN='^(TestReferenceBitwise|TestReferenceSmokeBitwiseSmoke|TestReferenceSearchPath.*Bitwise|TestReferenceSimilarReviewDefaultBoundaries)'
```

Changed BinarySlice and SIMILAR cancellation definitions require both native and
real pinned modes. ActualCancellation observes common `PGRegexProgram` matcher
entry before canceling a real UPDATE; StatementTimeout asserts57014, unchanged
sentinel and healthy same-connection reuse. Precancel/readiness is not entry proof.
Reserve `make quickcheck` for the whole-project Docker-free gate. Root runs
post-merge `make finalcheck` under an external1800-second deadline.

Standalone smoke source is session-owned `local://posix-smoke.go`, materialized
outside the checkout. From the worktree run `go run "$SESSION_TMP/posix-smoke.go"`.
It uses public driver/control/error APIs only, asserts all four operators,
first capture/greediness/absent/empty/BRE/lookbehind/newline behavior, held
replacement diagnostics, stored definitions, atomic UPDATE and stable
Dump/Reset/Restore with retained Rows, then prints `POSIX smoke: ok`.
No permanent smoke scaffolding or internal observation import is introduced.


## Role attribute coverage and feature smoke

The `TestReferenceRoleAttributes` R1–R7 family covers CREATE/ALTER ROLE and
USER defaults/aliases, direct attribute authority, replication-target and
special-attribute matrices, diagnostics/atomicity, physical admission,
password verifier mathematics/SASLprep and expiry metadata, omission/control
retention and a composed feature smoke. Shared public membership actions show
special attributes are not inherited, transitive ADMIN works independently of
INHERIT/SET, and changing member INHERIT only changes future default edges.
Public pg_roles scenarios supplement shared native/reference coverage; privileged
PostgreSQL pg_authid/pg_auth_members probes remain oracle-only without adding
those native providers. Internal units independently check complete descriptors,
immutable snapshots, verifier keys and lifecycle races.
`TestReferenceRoleAttributesContextualMembershipNames` additionally covers all
five contextual payload words in initial and ALTER GROUP comma-list positions.
`TestReferenceRoleAttributesSelfPasswordCancellation` exercises ordinary effective
self-PASSWORD with a valid high-iteration verifier: PostgreSQL statement timeout
reports57014, native returns its context deadline, both recover the same owner
and release the limit-one physical slot. Native units interrupt after a known
derivation step and assert unchanged roots/allocator plus RequestEnd completion.

The current `RUN='^TestReferenceRole'` selector covers every affected role
definition, including the two review regressions and trusted-connector boundary;
run it in both native and pinned modes before review. Each selected check has a
900-second deadline. Finalcheck remains the post-merge gate under one
1800-second deadline.

```sh
make check RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'
make check REFERENCE=1 RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$'
```

Expected smoke: a CREATEROLE actor creates and alters its target through automatic
creator ADMIN; ALTER USER retains omitted attributes/password/expiry. Limit1
admits one physical handle, denies the second FATAL53300 and physical close permits
a replacement. Unrelated ALTER fails42501 and held ALTER immediately loses authority
after CREATEROLE revocation. NOLOGIN denies new physical admission FATAL28000 without
terminating the incumbent. Native trusted expired-password admission is independent
of PostgreSQL password authentication; it is not a password handshake parity claim.
Limit0/-1, idle/reuse/original-authenticator accounting and superuser exemption
are covered by admission/lifecycle scenarios, not inferred solely from smoke.
Role rename/settings, password handshake and catalogs remain adjacent omissions;
native explicit transactions and existing DROP/COMMENT gates remain bounded.

## INSERT conflict omission acceptance

All public SQL scenarios live in the untagged shared
`TestReferenceInsertConflict` family. The completed oracle-first definitions have
no backend discovery filters or observation logs. Native-specific provider,
Restore and cancellation boundaries are independent expectations, not fake
PostgreSQL capabilities. Package supplements use actual expressions, descriptors,
roots, cursors, roles/ACLs and lock tickets; they issue no public SQL.

Run each selected check separately under a 900-second deadline:

```sh
make check RUN='^TestReferenceInsertConflict'
make check REFERENCE=1 RUN='^TestReferenceInsertConflict'
make check RUN='^TestInsertConflict'
make check RUN='^TestInsertConflict(Predicate|IndexPositionHints|AuthorizationAfterWait)'
make check RUN='^TestInsertConflictAuthorizationAfterWait$' RACE=1
timeout 900s go test ./internal/engine -count=1 -timeout=0 -run '^$' -bench BenchmarkInsertConflictCommittedOmissions -benchmem
make check RUN='^TestReferenceInsertConflictFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceInsertConflictFeatureSmoke$'
make check RUN='^TestReferenceInsertConflict(Contention|ConcurrentSameKey|SnapshotVisibility|ReadCommittedInvisibleConflict|Cancellation|OmittedReservations|ConcurrentProviderPhases|CanceledProvider)$' RACE=1
make check REFERENCE=1 RUN='^TestReferenceInsertConflict(Contention|ConcurrentSameKey|SnapshotVisibility|ReadCommittedInvisibleConflict|Cancellation|OmittedReservations|ConcurrentProviderPhases|CanceledProvider)$' RACE=1
```

The feature smoke asserts exact accepted rows/counts and empty command metadata,
selected-first/unselected uniqueness, partial strong implication and nested truth
rejection with unchanged TRUE/FALSE/NULL rows, native control round-trips and40001
for a post-frontier repeatable-read conflict. Focused race cases independently
assert contention outcomes, sum-of-counts1, provider phases, resource cleanup,
invisible read-committed skips and atomic direct/held cancellation.
`NestedTruthTests` separates full three-valued value fidelity from released
conservative inference, asserting full42P10 at Execute for mathematically valid
but unsupported implications. Native `AuthorizationAfterWait` independently
observes an actual pending key/accepted-prefix claim before SELECT/INSERT
revocation and proves failure cleanup, unchanged sentinel identity/data and
regranted publication. `IndexPositionHints` covers all entries, publication slots,
partial membership, shape/trusted/private reconstruction, reusable TYPE tombstones,
bad-hint fences and ordinary/nonstable bypass. The benchmark reports both batch
cost and ns/proposal across128,4096,65536 committed duplicates; compare scaling,
not a brittle timing threshold. Run this specialized benchmark directly with the
external deadline and Go timeout disabled, rather than overriding test-target flags.
Passing native alone does not validate the changed reference definitions.
Ordinary quickcheck precedes review; finalcheck runs after integration under
one 1800-second deadline.

## INSERT conflict update acceptance

The shared public `TestReferenceInsertConflictUpdate` definitions and migrated
`TestReferenceInsertConflictExcludedActions` case run in native and pinned real
PostgreSQL modes, with no discovery filters. They cover producer/alias scopes,
complete proposal inputs and constraints, typed assignments/parameters, actual
providers, duplicate identity, command metadata/counts, authorization, snapshots,
atomicity/cancellation, retained resources and native control/held lifecycle.
Native deferrable UNIQUE/PRIMARY KEY DDL, hierarchy/view targets and exclusion
UPDATE arbiters retain independently specified capability boundaries.

Run selected proof under separate 900-second external deadlines:

```sh
make check RUN='^TestReferenceInsertConflictUpdate'
make check REFERENCE=1 RUN='^TestReferenceInsertConflictUpdate'
make check RUN='^TestReferenceInsertConflictExcludedActions$'
make check REFERENCE=1 RUN='^TestReferenceInsertConflictExcludedActions$'
make check RUN='^TestInsertConflictUpdate(Parser|Binding|Identity|TargetFence|MixedStage|DuplicateIdentity|DistinctStage|UniqueDelta)$'
make check RUN='^TestInsertConflictUpdate(RowRecheck|LockOrdering|AuthorizationAfterWait|CursorFailure)$'
make check RACE=1 RUN='^TestInsertConflictUpdate(TargetFence|RowRecheck|LockOrdering|AuthorizationAfterWait|MixedStage|CursorFailure|DistinctStage|UniqueDelta)$'
make check RACE=1 RUN='^TestReferenceInsertConflictUpdate(Contention|Visibility|RetainedLocks|LockOrdering|Cancellation)$'
```

The invisible-source shared witness uses installed advisory CHECK admission,
not goroutine timing: reset the holder's marker after seed insertion, verify a
successful initial probe/unlock, then observe worker ownership before COMMIT.
Every successful observer probe unlocks immediately; cancellation/join and
unlock-all cleanup release all session counts before retained-connection teardown.
Do not assume CHECK OR short-circuit or call this a native pending-key observer.
`RowRecheck/invisible_source` separately inspects the real native UniqueKey request,
selected provider/key/blocker and immutable source frontier, requiring exact
affected1/final11. Other native row-wait mechanisms inspect actual pending RowID,
lock modes, current authority, stage rollback and joined cleanup; PostgreSQL
public interleavings use its actual blocker observer. A native no-op wait helper
or successful asynchronous launch supplies no pending-acquisition evidence.

`RowRecheck/latest_payload` supplements the public snapshot cases with an actual
selected-RowID pending barrier. Holder COMMIT must supply target20 to SET, giving
21; holder ROLLBACK supplies10, giving11. Both target-dependent predicate
threshold transitions check the mixed fresh prefix, exact counts and committed
rows under READ COMMITTED and READ UNCOMMITTED, with joined cleanup.

`DistinctStage` independently checks original committed bases through prior-own
replacements, immutable committed/prior/current-command prefixes, append slots,
fresh identity, duplicate21000 without additional effects, rollback and exactly
one transaction publication. `UniqueDelta` checks candidate-only restoration,
prior key/stronger mode preservation, non-key grants, retry/accept lifetimes and
no resurrection after release. Private overlay hash buckets use the installed
provider's grouping and final ordering equality, not hash-only equality.

Replacement-heavy scaling runs the real executor and Finish; setup, earlier
commands, exact row/count checks and rollback are outside benchmark timing:

```sh
timeout 900s go test ./internal/engine -count=1 -timeout=0 -run '^$' -bench '^BenchmarkInsertConflictUpdateDistinctStage$' -benchmem
```

Compare128,4096,65536 proposal batches for replacement, mixed fresh/replacement
and prior-own modes, using ns/proposal and bytes/allocations per batch (divide by
proposals/op for per-proposal cost). No brittle timing threshold or unobserved
performance claim substitutes for root-recorded scaling evidence. Ordinary
UPDATE/MERGE/DO NOTHING retain their existing staging and overlay semantics.

An internal fixture that directly captures a query source with a real statement
scope must attach that scope through the installed `borrowAuthority` owner before
producer/assembly demand. Threaded queryState resolves live authority through its
source, not an EvalContext fallback. `TestQueryDistinctInsertSourceDescriptorOwnership`
checks the integrated caller cutover without weakening its original DISTINCT
schema, identity-pruning or source-call assertions; it adds no DISTINCT capability.

The root-owned temporary standalone program has also exercised the installed
public API with exact output
`mixed=2 filtered=0 held=1 duplicate=21000 atomic=true rows=[(1,20,10,held),(2,30,40,fresh)]`,
exit0 and empty stderr. It is removed after execution, not a permanent command.
Selected proof and this smoke do not replace root-owned Docker-free quickcheck,
regression/race proof, independent review or post-integration finalcheck.


## DISTINCT ON reference contracts and feature smoke

The untagged `reference/query_order/query_distinct_on_*_test.go` family shares fixture/public
API bodies in both modes. Latest-row/NULL grouping, visible/hidden metadata,
direct/fresh/held routes, resolved-prefix diagnostics, typed hidden keys,
group/window/FETCH/subquery consumers, INSERT source typing, physical authority,
real cancellation and actual ON view/native-control replay have independent
expectations. No oracle-discovery backend filter remains.

The pinned18.0 oracle distinguishes analysis target identity from physical output
sharing: ON(y) random x/y yields four shared draws and continuation5; ON(1,2)
requires eight interleaved draws and continuation9. Cheap immutable losing
payloads still fail22012. The unrelated volatile sequence payload executes only
for winner2, returns INT8 payload1 and leaves nextval2 after independent resequencing.
Do not replace these expectations with ordinary DISTINCT's eager paired golden.

Run the complete family and executable feature smoke in both modes, each command
under its own900-second deadline:

```sh
timeout 900s make check RUN='^TestReferenceQueryDistinctOn'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOn'
timeout 900s make check RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
```

Smoke requires seven events→ids3/5/7, hidden payload metadata, NULL grouping,
empty/nil/repeated held frames, ordinal/duplicate ORDER first-policy resolution,
bounds→id5, complete42P10 original ON caret, INSERT count3/exact winners and actual
ON view export/native successful/invalid/canceled Restore with sentinel17 and
retained nonempty results. Broader typed/window/volatile/ACL/cancellation witnesses
live in the complete family. Internal key/cache/collision/pin/manifest probes
supplement these public scenarios; they are not substitute PostgreSQL evidence.

## Transaction and ALTER smoke migrations

The former public SQL smoke scenarios now live in untagged reference tests.
Run both selectors before review, each under its own 900-second deadline:

```sh
make check RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$'
make check REFERENCE=1 RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$'
```

These definitions preserve direct and prepared SQL, held statements, independent
rows/types/counts and complete logical diagnostics. Native ScanType and known
nullability assertions supplement PostgreSQL's public wire metadata without
requiring lib/pq to report native-only descriptors. SQL transaction visibility,
abort recovery, CHAIN, notices, ALTER atomicity, dropped-column dependencies,
quoted names, owner authority, comments and zero-column multiplicity run against
both backends. Dump/Reset/Restore, retained eager native rows, replacement
incarnation errors and Restore reader cancellation remain explicit native control
phases; PostgreSQL has no equivalent engine control API. No migration enables
additional engine functionality or turns a native failure into an oracle golden.

Pre-canceled native BeginTx deterministically rejects admission with an ordinary
context error and no effects. In the pq route, pre-canceled admission and rollback
race the adapter's context watcher; their incidental transport outcomes are not
PostgreSQL SQL expectations. These scenarios clean up any returned transaction
and explicitly retire the canceled physical connection, then independently assert
unchanged rows through the unaffected observer and a fresh usable borrower.
The live canceled transaction still publishes no private rows, and a subsequent
transaction commits its independently expected row. The context-cancellation
definition additionally exercises admitted server cancellation in direct/prepared
routes. Run the changed cancellation definitions under race in both modes before
review, each invocation bounded to 900 seconds:

```sh
make check RACE=1 RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$'
make check RACE=1 REFERENCE=1 RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$'
```

## Predefined role oracle-first contracts

The untagged `reference/roles/predefined_*_test.go` definitions exercise all
sixteen documented identities, implicit database-owner membership, restrictions
and pinned diagnostics, real read/write/schema/sequence consumers, REINDEX and
transactional lock exclusion, monitoring settings, lifecycle controls and explicit
absent-provider boundaries. Every supported shared operation runs direct and
prepared routes with independently specified rows, metadata, counts and complete
diagnostics. The safe `pg_roles` projection, including all sixteen predefined
identities, is supported and tested on both backends. Privileged PostgreSQL
`pg_authid`, hidden-setting/statistics and policy-less RLS supplements remain
explicitly separate from native unsupported providers.

Oracle-first discovery uses these small exact groups with `REFERENCE=1`, followed
by native red before production changes. The temporary PostgreSQL-only discovery
guard has been removed; integrated definitions always discover both backends.
All four groups, including every new definition and all three test smokes, must
subsequently pass both modes:

```sh
make check REFERENCE=1 RUN='^TestReferencePredefined(RoleIdentities|DatabaseOwner|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(AllData|Sequences|DataFeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(Maintain|MaintainFeatureSmoke|MonitoringSettings)$'
make check REFERENCE=1 RUN='^TestReferencePredefined(UnavailableCapabilities|RLSBoundary|BoundaryFeatureSmoke|Lifecycle)$'
```

The review-remedy acceptance definitions add actual automatically updatable view
INSERT/UPDATE/DELETE, owner-versus-invoker source authority and held owner revoke;
safe catalog OIDs/direct flags/password mask/typed NULLs observed by an ordinary
actor; and parameter/NULL table and sequence state with held revoke/regrant.
Existing definitions remain intact. Discover these independent expectations on
the pinned oracle first, then use the matching native selector:

```sh
make check REFERENCE=1 RUN='^TestReferencePredefined(ViewWrites|SafeCatalog|ParametersAndNulls)$'
make check RUN='^TestReferencePredefined(ViewWrites|SafeCatalog|ParametersAndNulls)$'
```

Each definition has `prepared=false` and `prepared=true` subtests; held statements
add execution-time authority rechecks in both routes. Supported updatable views
are not an unavailable-provider boundary: materialized and DISTINCT view write
errors are separately asserted, with unchanged physical data.

Use the same four selectors without `REFERENCE=1` for native proof, with an
external 900-second deadline per command. These are definitions, not verification
claims. Native host-control phases have no invented PostgreSQL Reset/Restore API.
Fresh native engines remap builtin ownership; actual dump replay is additionally
consumed by PostgreSQL whenever that backend is enabled.
The safe PostgreSQL subscription witness uses `connect=false`, `slot_name=NONE`
and a harmless conninfo password; even disconnected creation validates password
presence for a nonsuperuser. Cleanup is registered before scenario creation and
unconditionally drops the scenario subscription and revokes its introduced
database CREATE privilege before role teardown, including assertion-failure paths.

PostgreSQL COPY denial is exercised directly. Its outside-transaction prepared
route also asserts lib/pq's exact ordinary driver error, not a fabricated SQLSTATE.
Lifecycle pre-canceled admission is deterministic native-only evidence; the
PostgreSQL branch independently grants inside a transaction, rolls back, and
asserts no authorization publication. This rollback witness is not claimed as
PostgreSQL cancellation proof.

Selected absent file/signal/scan calls independently expect native0A000
`functions are unsupported`, position8, with no invented function Hint.
PostgreSQL missing-function42883 diagnostics are unchanged. This preserves the
existing parser exclusion instead of implementing absent functions to match a
proposed golden. Native DROP likewise retains the prerequisite actual-SUPERUSER
gate: creator ADMIN/CREATEROLE still deny42501 before current/session checks.
PostgreSQL creator current/session55006 remains a backend-specific witness;
privileged bootstrap current-user55006 and builtin pins/missing-name preflight
remain directly and prepared SQL-exercised on both backends.

All-data INSERT RETURNING independently preserves PostgreSQL's table42501 and
the native parser boundary0A000 `INSERT suffix is unsupported`, position39.
Supported read-dependent UPDATE/DELETE denial and constant writer effects
remain separate assertions; this is not a RETURNING implementation claim.
The independent-driver lifecycle witness restores through a host-control pool,
then observes rows, types and affected counts through the existing recorded
notice-pool wrapper for that same connector. PostgreSQL dump replay resets
imported session authorization before cleanup, without changing replay results.


Separate root-owned non-test executable smoke evidence must use public driver,
authentication and database/sql APIs, without test fixtures or internal hooks.
Create its source ephemerally in the assigned worktree, execute it under an
external 900-second deadline, and retain the source and actual argv/exit/stdout/
stderr in the part verification evidence. Remove the ephemeral source afterward;
durable repository reproduction commands are the reference selectors above.
It independently asserts public grants/revokes and held prepared authorization,
data rows/counts and sequence10/11, REINDEX INDEX/TABLE, ACCESS EXCLUSIVE NOWAIT
exclusion/release and complete truthful unavailable diagnostics. Test-smoke exits
do not replace this executable evidence. No executable native RLS, OS-program,
filesystem, physical-checkpoint or replication-provider parity is claimed.

Function-level initialization, allocation/exhaustion/publication fences, implicit
MEMBER/INHERIT/SET (without ADMIN), capability/column/protected-class masks, real
maintenance roots, immutable snapshots, principal remapping and control retention
are independently asserted without adding a second public SQL test surface:

```sh
make check RUN='^TestPredefined(RoleInitialization|RoleAllocation|ImplicitDatabaseOwner|MembershipRestrictions|CapabilityMasks|CapabilityGrantSource|MaintainRebuild|RoleSnapshots|RoleControls|NoBypassRLS|UnavailableSyntax)$'
```

The grant-source proof checks actual no-op table/column GRANT WITH GRANT OPTION
warnings, unchanged catalog/role authority, and independently absent recipient
rights, rather than treating global use rights as grant options. Existing
semantic split/reordered public ACL and renamed-public lifecycle/control
regressions remain substantive behavioral roundtrips:

```sh
make check RUN='^Test(AuthorizationDumpRejectsChangedNamespaceState|SchemaPublicLifecycleControls)$'
make check RUN='^TestReferenceRoleAttributesRetentionAndControls$'
make check REFERENCE=1 RUN='^TestReferenceRoleAttributesRetentionAndControls$'
```


Canonical public ownership also changes the existing bootstrap schema replay
definition. Its original owner is now pg_database_owner; ordinary recreated
schemas still retain their real creator/selected owner:

```sh
make check RUN='^TestReferenceSchemaPublicLifecycleControls$'
make check REFERENCE=1 RUN='^TestReferenceSchemaPublicLifecycleControls$'
```

Run every command with an external 900-second deadline. These selected proofs
supplement, rather than replace, the root-owned whole-project Docker-free
quickcheck and final verification.

## Row-security policy definitions

Public direct/prepared witnesses are the untagged `reference/roles/rls_policy*`
families. They independently specify grammar, retained ALTER fields, ownership,
role multiplicity, exact diagnostics/notices, provider/column dependencies,
native controls, lock/cancellation lifecycle and unchanged native rows.
Conditional rename witnesses retain release18's accepted-but-nonskipping
IF EXISTS behavior; do not reuse DROP IF EXISTS notice expectations.
Internal `TestRLSPolicy` units exercise real capture/bind/render/validation,
catalog remapping and canceled publication, supplementing public witnesses.

Root-selected verification, with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceRLSPolicy'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicy'
make check RUN='^TestRLSPolicy'
make check RACE=1 RUN='^TestReferenceRLSPolicy(Ownership|Controls|Lifecycle|ConditionalRename)$'
make check RACE=1 RUN='^TestRLSPolicy(Publication|Remap)'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyDefaultDenyPostgres$'
```

The original last selector is intentionally PostgreSQL-only: its applicable
USING(TRUE) pre-drop reader witness requires expression enforcement. Shared
`TestReferenceRLSBaseLastPolicyDrop` separately certifies native last-policy DROP,
retained enabled/forced flags and actual default deny with honest bypass and
role-inapplicability before DROP. Metadata-only policy conditions do not certify
FILTER/CHECK. Controls retain explicit native API phases; PostgreSQL independently
checks SQL and genuine emitted-stream replay, not an invented Restore API.
The commands specify required proof, not a claim that all selected checks passed.

## Row-security BASE oracle-first contracts

All BASE public scenarios are untagged, shared direct/prepared bodies under
`reference/roles/rls_base_*_test.go`, with independent schemas, rows, counts and
complete diagnostic expectations. Root runs these bounded groups once native
and once with `REFERENCE=1`, external900s each:

```sh
make check RUN='^TestReferenceRLSBase(FlagTransitions|Diagnostics|Transactions|LocksAndCancellation)$'
make check RUN='^TestReferenceRLSBase(DefaultDenyAndInputs|BypassAndACL|Applicability|Freshness)$'
make check RUN='^TestReferenceRLSBase(Writes|Principals)$'
make check RUN='^TestReferenceRLSBase(PolicyPreservation|PredefinedReadAllData|LastPolicyDrop|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceRLSBase(AuthorityWaits|ProposalPrecedence|ExpressionUses|ControlsReplay|Syntax)$'
make check RUN='^TestReferenceRLSBase(FreshAuthority|PredefinedNoInherit|InheritanceFlags|ProposedReadConjunction|DefaultAndCoercion|NestedWritablePrincipals)$'
make check RUN='^TestReferenceRLSBase(MixedAlterBoundary|EmptyExpressionBoundary|SQLPreparedExpressionBoundary|ReadBeforeProposalBoundary)$'
make check RUN='^TestReference(PredefinedRLSBoundary|PredefinedBoundaryFeatureSmoke|RLSPolicyDefaultDenyPostgres)$'
make check RUN='^TestRLSBase(Grammar|FlagsAndPublication|Decision|Controls|CancellationAndLocks|OwnerPublicationFreshness|DeniedTargetHistory)$'
make check RUN='^TestRLSBase(FlagsAndPublication|CancellationAndLocks|OwnerPublicationFreshness|DeniedTargetHistory)$' RACE=1
make check RUN='^TestReferenceRLSBase(AuthorityWaits|LocksAndCancellation|Freshness|Controls)$' RACE=1
```

No discovery skips remain in BASE definitions. Applicable-expression scenarios
assert native Execute0A000 `row-security policy expression enforcement is unsupported`
(ERROR severities, all optional fields empty), and independently specify pinned
PG outcomes. TRUE/FALSE/NULL are not native evaluator shortcuts. Restrictive-only,
omitted qualifiers and role/command-inapplicability really default-deny.
The review-boundary definitions independently retain successful mixed flag/column
metadata ALTER, applicable-expression rejection on physically empty input,
successful SQL PREPARE followed by the EXECUTE boundary, and source/target-read
enforcement preceding a default-denied INSERT proposal. Native full diagnostics
remain distinct from PostgreSQL's real query/proposal outcomes; failed writes
retain exact destination and source rows.
Virtual generated-column creation retains native Execute0A000; the PG virtual
proposal witness does not pretend native virtual columns exist. TABLE OWNER TO
retains native Prepare0A000, while a real private catalog-stage owner publication
unit checks fresh native owner authority.

Pinned PG18 already-denied held statements remain denied through the tested
BYPASSRLS/inherited-owner grants; actual unprepared/new handles use current grants.
Native held execution deliberately remains always fresh. PG active cursors block
same-session flag ALTER55006; native eager cursor captures remain immutable.
Do not fabricate matching lifetime/cache behavior. Actual unchanged native Dump
bytes must replay on pinned PG after production; independently authored flag SQL,
Dump string inspection or a skipped producer cannot replace that evidence.
These selectors and expectations describe required proof, not passing evidence.


Review-remedy witnesses independently cover semantic aggregate ownership and
signature/nesting precedence, REGTYPE-only provider RESTRICT/CASCADE and ALTER
retention, and cascade owner-lock wait/cancel/retry with mixed dependencies.
Provider-only TEMP controls exercise persistent-provider replacement refusal
and compatible TEMP collation/sequence/row-type retention in both clauses.
TEMP domain CREATE remains an explicit native capability boundary, not a
retention success. `TestReferenceRLSPolicyPostgresReplay` executes the actual
native Dump bytes unchanged on pinned PostgreSQL, with existing named roles,
correlated conditions and domain/collation/sequence/REGTYPE providers; it checks
metadata, identities, dependencies and rows independently.

```sh
make check RACE=1 RUN='^TestReferenceRLSPolicy(RegtypeDependencies|RegtypeTemporary|CascadeOwnerLock|AggregateOwner.*|ProviderControls|PostgresReplay)$'
make check RUN='^TestPGDependRegtypeValueReferences'
make check RACE=1 RUN='^TestRLSPolicy'
```

`TestReferenceRLSPolicyRestoreSelectors` executes the same named-session-owner
SET SESSION AUTHORIZATION / CREATE TABLE / CREATE POLICY TO CURRENT_USER,
CURRENT_ROLE, SESSION_USER input on PostgreSQL and through native Restore.
The independent expectation is three occurrences of that named owner, not
the private import-authority principal. PostgreSQL's actual policy OID vector
and native Dump's ordered TO identifier vector supplement full role-dependency
diagnostics: the owner plus three policy targets block DROP ROLE; ALTER TO
PUBLIC releases the targets but not table ownership; dropping the table releases
the owner. Native complete emitted bytes are reimported twice and, in real mode,
replayed unchanged on PostgreSQL with independently expected ownership/targets/rows.
SQL probes and dependency release run through both direct and prepared routes.

Root-owned oracle-first discovery and native-red commands (external900s each):

```sh
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyRestoreSelectors/^prepared=/^postgres$'
make check RUN='^TestReferenceRLSPolicyRestoreSelectors$'
```

After the source remedy, run the complete definition in both modes:

```sh
make check RUN='^TestReferenceRLSPolicyRestoreSelectors$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyRestoreSelectors$'
make check RUN='^TestRLSPolicyRestoreAuthorization$'
make check RACE=1 RUN='^TestRLSPolicyRestoreAuthorization$'
```

These specify intended proof, not observed results. Existing Controls retains
failed-Restore atomicity and trusted differently owned provider round trips;
PostgresReplay retains its independent named-target actual-byte replay.

`TestRLSPolicyRestoreAuthorization` supplements the public witness with real
locked authorization captures: ordinary semantic state equals session state,
trusted policy authority changes only its authorization actor, subsequent
session revisions produce fresh target captures, retained captures stay
immutable, and a live-engine importer flag cannot forge private authority.
The root-owned temporary public policy smoke also imports selector-bearing
SQL under a named stage owner while preserving a different named caller,
asserts the actual Dump's three named targets and reimports its complete bytes.
Its expected quiet stdout is
`policy DDL smoke: create/alter/rename/roundtrip/drop/restore-selectors OK`,
with empty stderr. The session smoke source is not a permanent deliverable.

## NUMRANGE oracle-first contract

`TestReferenceNumericRange` definitions independently specify continuous numeric
bounds, exact scale-preserving output, empty/unbounded/NULL distinctions, explicit
numeric infinities and NaN, direct/prepared binds, metadata, full diagnostics,
statement atomicity and already-admitted plain/nested domain composition.
The control scenarios use public native Dump/Reset/Restore and replay actual
emitted SQL on pinned PostgreSQL; PostgreSQL has no native-control API analogue.
The feature smoke is `TestReferenceNumericRangeFeatureSmoke`.

The oracle-first Round A definitions were introduced before production changes.
The temporary discovery filter was removed before native red. Completed definitions
now share `reference/types/range_helpers_test.go` with the integer-range scenarios:
actual NUMRANGE/INT4RANGE/INT8RANGE raw values and metadata are asserted before
normalization. Pinned PostgreSQL18.0 grounds expected diagnostics and phases;
never learn golden values from native output.

Run from the assigned worktree, with an external900-second deadline per command:

```sh
make check RUN='^TestReferenceNumericRange' REFERENCE=1
make check RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceNumericRangeFeatureSmoke$'
```

Because common range helpers are consolidated, both selected modes must additionally
cover `^TestReferenceIntegerRange`. Reserve `make quickcheck` for completed
whole-project Docker-free verification. Standalone smoke source
is supplied in the OMP session as `local://numrange-smoke.go`: materialize it
temporarily as `.numrange-feature-smoke/main.go`, run
`timeout 60s go run ./.numrange-feature-smoke`, record observations and remove
the materialized source. Success is quiet exit0; failure reports an actual mismatch.


## Policy frozen-expression renderer regression

Policy conditions use the stored-view expression renderer's analyzed literals,
casts, collations, provider identities and lexical column slots. Their outer
expression frame has no SELECT syntax. SELECT-only correlated-column display
rules must not run during policy validation or export; view display keeps its
existing simple-source spelling.

`TestRLSPolicyFrozenExpression` exercises actual capture, validation, rendering
and recapture, including typed temporal values, typed NULL, domain casts,
collation/sequence/regtype providers, identity expressions, nested correlation,
ANY/ALL, row comparison and nested aggregate/window queries. Rendered SQL must
bind again and redump unchanged. A row-dependent division-by-zero condition must
be stored without evaluation. `TestRLSPolicyRemap` checks the same frozen
provider/cast/literal/correlation context after actual catalog replacement and
retains the original immutable definition.

`TestReferenceRLSPolicyExpressionsRegtype` independently expects direct/prepared
policy DDL to accept domain and row-type REGTYPE constants, typed REGTYPE NULL
and the supported symbolic `CURRENT_USER IS NOT NULL` condition. A shared view
retains the renamed row-type identity rather than a new table with the old name;
its Boolean identity comparisons must return `(true,false)` and policy DDL must
leave both target rows unchanged. Native controls repeat exact Dump/Restore/Dump
equality three times, including policy symbolic identity, current qualified type
names and the view's live identity comparisons. Frozen non-NULL REGTYPE leaves
already carry their type annotation, so replay adds no redundant cast. Typed
NULL retains its explicit source cast. This adds no NAME-to-TEXT capability.

Root-selected proofs, each with an external 900-second deadline:

```sh
make check RUN='^TestRLSPolicy(FrozenExpression|Remap)$'
make check RUN='^TestReferenceRLSPolicy(FeatureSmoke|Expressions)$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicy(FeatureSmoke|Expressions)$'
make check REFERENCE=1 RUN='^TestReferenceRLSPolicyExpressionsRegtype$'
make check RUN='^TestReferenceRLSPolicyExpressionsRegtype$'
```

The existing direct/prepared feature smoke is the public panic regression;
internal presentation tests supplement, not replace, its dual-mode proof.
These commands describe required verification, not observed passing outcomes.

## SELECT CTE verification

The untagged `reference/query_core/query_cte_*_test.go` definitions independently
specify dependent/reused bags, scopes, genuine folding/fences, executable
volatility, shared lazy demand, invocation ownership, complete diagnostics,
stored consumers, authority/snapshots, native lifecycle controls and feature smoke.
Every changed definition requires both selected modes; do not retain an
oracle-discovery backend filter. Catalog pg_class and transient-name assertions
are shared; pg_type is a PostgreSQL-only supplement to the existing native
absence boundary, not an added native provider.

Run bounded batches from the selected worktree, with an external 900-second
deadline for each invocation:

```sh
make check RUN='^TestReferenceQueryCTE(Dependencies|Reuse|Scope|Parenthesized|Diagnostics|Catalog)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Dependencies|Reuse|Scope|Parenthesized|Diagnostics|Catalog)$'
make check RUN='^TestReferenceQueryCTE(Folding|Volatility|Demand|Invocation)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Folding|Volatility|Demand|Invocation)$'
make check RUN='^TestReferenceQueryCTE(Stored|Authority|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(Stored|Authority|Lifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceQueryCTE(RowLocks|DeadDemand|PhysicalFrontier)$'
make check REFERENCE=1 RUN='^TestReferenceQueryCTE(RowLocks|DeadDemand|PhysicalFrontier)$'
make check RUN='^Test(RowLockViewAggregateComposition|ReferenceRowLockViewAggregateComposition)$'
make check REFERENCE=1 RUN='^TestReferenceRowLockViewAggregateComposition$'
make check RUN='^Test(QueryCTE|CTEParser|InsertSelectBoundaries)'
make check RACE=1 RUN='^TestQueryCTE(SharedFrontierAndReaderLifetime|StickyFailureAndCloseOnce|InvocationIsolationAndInheritance|ProjectDispatcherOwnsInvocation)$'
```

The existing stored aggregate composition expectations are unchanged: native's
marked hidden/grouped FALSE frontier retains physical grants/conflicts, whereas
the pinned PostgreSQL constant-gating control has none. The CTE physical-frontier
definition separately checks opaque lock bodies and independently reachable
marked aggregate siblings. Function units prove branch-local owner/mark inspection,
correct nested input mapping and identity-only/opaque boundaries without SQL.

Parser generation is root-owned after grammar edits. Agents do not run
generation, builds, checks or formatters. Root whole-project Docker-free
verification uses quickcheck, not a selected-test substitute.

Static folding eligibility includes unused nested WITH volatility, independently
of executable ACL/source/demand pruning. The enclosing default/NOT MATERIALIZED
division witness expects22012, followed by an unused-sequence draw1. Stored view
sources must establish invocation ownership through universal project dispatch,
without changing inherited row-mark settlement; the direct-dispatch unit checks
lazy sharing and owner-driven close-once lifetime.

Actual feature smoke also requires the complete standalone public Go source
retained in the implementation session as `local://cte-smoke.go`. The root
materializes it outside the checkout as `$SESSION_TMP/cte-smoke.go` and records
the resolved argv, worktree cwd, exit, stdout and stderr:

```sh
timeout 900s go run "$SESSION_TMP/cte-smoke.go"
```

This program uses only public stuntgres and standard-library APIs and fails on
any independent metadata/bag/diagnostic/count mismatch. It exercises dependent
direct/prepared results, real fold-versus-materialization division witnesses,
shared VOLATILE draws and fresh held Execute, lazy unused/LIMIT0/LIMIT1 demand,
enclosing-reader sharing, correlated local isolation, static unused-nested-WITH
folding fences with no draw, stored view invocation ownership, transient scope,
full duplicate/malformed diagnostics and persistent Dump invariance. The review
supplement asserts full explicit CTE-alias OF diagnostics with positive untargeted
locking, materialized FALSE/NULL no-draw and folded false divisor gates.
It also checks direct/prepared opaque marked CTE bodies leave peer DELETE free
for all hints while an independently reachable marked aggregate sibling retains
native's mandatory conflict frontier, then releases grants on transaction end.
The program is a temporary session artifact, not a committed scaffold. Permanent
`TestReferenceQueryCTEFeatureSmoke` additionally runs in both selected modes.

## Foreign-key proof selection — change note

Shared independently specified references use the same bodies for native and
pinned PostgreSQL, including all migrated FK boundary callers. Run each selected
group with `make check RUN='...'` and again with REFERENCE=1:

* `^TestReferenceForeignKey(Definition|DefinitionNames|DefinitionIneligibleKeys|DefinitionTypes|DefinitionProviders|NetworkSupportingKeys|Diagnostics|Values|Metadata|Boundaries|BoundariesModes|BoundariesPartition|BoundariesTemporal|BoundariesTemporary)$`
* `^TestReferenceForeignKey(Atomicity|Transactions|Producers|Authorization|Concurrent|ConcurrentCancellation)$`
* `^TestReferenceForeignKey(Lifecycle|RetainedStatements|NativeControls|NativeControlsTemporary|FeatureSmoke|DeleteReturningCaller|DropColumnCaller|SchemaCaller)$`
* `^(TestReferenceDeleteCoreForeignKeyBoundary|TestReferenceAlterTypeForeignKeyBoundary|TestReferenceAlterRenameExternal|TestReferenceInheritanceKeyBoundaries|TestReferenceTableCoreExternalDependencyBoundary)$`

The affected tagged remainder definitions are
TestReferenceDeleteReturningBoundaries,
TestReferenceAlterDropColumnExternalDependencies and
TestReferenceAlterSchemaPostgresClosure. Their unrelated PostgreSQL-only
assertions remain selected with REFERENCE=1; their extracted FK assertions are
now in the untagged caller definitions above. No discovery filter remains.
Native controls do not run on PostgreSQL; the permanent shared bodies retain
complete SQL assertions around their native-only Reset/Dump/Restore phases.

Internal `^TestForeignKey` exercises the real canonical K/U row requests,
non-key compatibility, queued cancellation/replacement, private row overlays,
definition/index authority and immutable TYPE identities. Race proof uses
`make check RACE=1 RUN='^TestForeignKey'`. Keep the shared concurrent references
selected too: internal manager evidence complements, not substitutes for, the
real PostgreSQL observed behavior.
The migrated parser witness is selected by
`make check RUN='^TestObjectGrantReferencesAndForeignKeyGrammar$'`.
The comment-only role-grant consumer remains
`make check RUN='^TestReferenceRoleGrantKinds$'` and its REFERENCE=1 counterpart;
its delegated privilege assertions are unchanged.

Feature smoke: `make check RUN='^TestReferenceForeignKeyFeatureSmoke$'`, plus
the identical selector with REFERENCE=1. Every external check invocation uses
a 900-second deadline. Quickcheck remains whole-project Docker-free proof.

FK regression remediation keeps incumbent keyword and consumer semantics:
INITIALLY remains usable by EXCLUDE/PRIMARY KEY/UNIQUE controls, and FOREIGN
retains its keyword-label behavior. Supporting-index and referenced-column
RESTRICT retain complete dependency diagnostics; CASCADE prunes definitions,
not child data. The internal `TestForeignKeyQueuedNonKeyCompatibility` observes
the actual pending DELETE and compatible non-key grant on the same parent RowID.
`TestForeignKeyPrivateFinalImage` covers parent keys born in a prior own INSERT,
not merely replacements with a committed predecessor.

`TestReferenceForeignKeyNetworkSupportingKeys` independently checks primary
and standalone inet_ops providers, equivalent INET/CIDR spellings, duplicate
parent rejection, missing child rejection and unchanged typed rows. Its
supporting-key consumer reuses the existing comparator bucket, not a new
network grouping/hash provider. Select the following existing consumer
definitions in both native and real modes as well:
`make check RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)$'`
and the identical command with REFERENCE=1.

Parser/internal regression selection:
`make check RUN='^(TestMappedKeywordLabels|TestExclusionSyntax|TestPrimaryKeyCapabilityRecords|TestUniqueDeferrabilityCapabilityBoundary|TestObjectGrantReferencesAndForeignKeyGrammar)$'`;
canonical lock compatibility selection: `make check RUN='^TestRowLock'`.
Existing reference controls remain selected in both modes with
`make check RUN='^(TestReferenceExclusion|TestReferencePrimaryKeyBoundaries|TestReferenceUniqueExcludedControls|TestReferenceInsertConflictProviderBoundaries|TestReferenceInsertConflictExcludedActions)'`.
All changed FK definitions above require fresh native and pinned evidence.
The expression-index, stored internal `"char"` and INSERT suffix witnesses
assert existing native capability diagnostics versus real PostgreSQL behavior;
they do not weaken the admitted FK consumer checks. Schema GRANT is only
PostgreSQL fixture setup; the table/column REFERENCES and redaction assertions
remain shared. No discovery filter or executable discovery scaffold is added.

The positioned DELETE FK caller is shared in
`TestReferenceDeleteCurrentOfUsingReturningAndConstraints`: its FK failure
preserves the parent row and positioned cursor, and removing the child permits
deleting that same physical parent occurrence. The unrelated USING/RETURNING,
constraint and cursor eligibility phases remain intact. Select the complete
definition with
`make check RUN='^TestReferenceDeleteCurrentOfUsingReturningAndConstraints$'`
and the identical command with REFERENCE=1.

DROP COLUMN regression selection preserves incumbent AUTO ownership, immutable
staged owners and exact default/CHECK/policy notice schedules. The shared
`TestReferenceForeignKeyDropColumnCaller` additionally observes mixed FK/view
ordered RESTRICT and CASCADE, child-data survival and local self-FK AUTO
retirement. `TestReferenceForeignKeyProducers` shares real23503 for the incoming
admitted MERGE MATCHED UPDATE; it keeps independent INSERT suffix boundaries.
Required fresh native and pinned selections (not observed passes):

```sh
make check RUN='^TestReferenceForeignKey(DropColumnCaller|DefinitionTypes|Producers)$'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(DropColumnCaller|DefinitionTypes|Producers)$'
make check RUN='^(TestCheckReview2OrderOrdinalInvariants|TestRegclassDropColumnStagedOwnerDefinitions|TestTemporaryTableRetainedLiveRelationProviders)$'
make check RUN='^TestReference(RegclassDropColumnDependencies|RegclassFeatureSmoke|RLSPolicyDependencies|AlterNullabilityReviewRemainingDependency|AlterTypeCheckDependencies|HashIndexLifecycle|PartialIndexLifecycle|PartialIndexUniqueCurrentNames|PartialIndexUniqueDependencyClosure|ExpressionIndexLifecycle|LikeStoredDependencies|LikeStoredPatternEscapeDependencyRestrictionsSourceConsumerRoutes|CheckAlterOwnedSequenceAutoConstraint|CheckControls|CheckLifecycle|TableLikePartialProviders)'
make check REFERENCE=1 RUN='^TestReference(RegclassDropColumnDependencies|RegclassFeatureSmoke|RLSPolicyDependencies|AlterNullabilityReviewRemainingDependency|AlterTypeCheckDependencies|HashIndexLifecycle|PartialIndexLifecycle|PartialIndexUniqueCurrentNames|PartialIndexUniqueDependencyClosure|ExpressionIndexLifecycle|LikeStoredDependencies|LikeStoredPatternEscapeDependencyRestrictionsSourceConsumerRoutes|CheckAlterOwnedSequenceAutoConstraint|CheckControls|CheckLifecycle|TableLikePartialProviders)'
make check RUN='^TestReferenceTemporaryTableExpressionCommitDrop$'
make check REFERENCE=1 RUN='^TestReferenceTemporaryTableExpressionCommitDrop$'
```

The session-only public smoke uses finite context, a real prepared composite
child INSERT, exact nullable INT4/TEXT rows, full23503 fields, atomic parent
UPDATE/DELETE, savepoint recovery, actual Reset before persistent Restore,
SIMPLE NULL, cyclic replay/redump, invalid final import preserving sentinel91
and held Rows, and retained TEMP identity through Reset and Restore. Successful
stdout is empty; mismatches exit nonzero. It is not a staged deliverable.

Independent FK review remedies require these exact completed selectors in native
and real pinned PostgreSQL modes (external900-second deadlines):

```sh
make check RUN='^TestReferenceForeignKeySupportingFamily'
make check REFERENCE=1 RUN='^TestReferenceForeignKeySupportingFamily'
make check RUN='^TestReferenceForeignKey(ClauseStructure|NotNullNameCollision|TypeAtomicity)'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(ClauseStructure|NotNullNameCollision|TypeAtomicity)'
make check RUN='^TestReferenceForeignKeyRestore(SkippedCreate|CapturedAuthority)'
make check REFERENCE=1 RUN='^TestReferenceForeignKeyRestore(SkippedCreate|CapturedAuthority)'
make check RUN='^TestForeignKey'
make check RACE=1 RUN='^TestForeignKey'
```

The eight new shared definitions are SupportingFamilyAdmission,
SupportingFamilyCoercion, SupportingFamilyRestore, ClauseStructure,
NotNullNameCollision, TypeAtomicity, RestoreSkippedCreate and RestoreCapturedAuthority
(each prefixed TestReferenceForeignKey). They independently assert full diagnostics,
metadata, stable/reminted identities, complete typed bags, recovery and failed
publication. All eight temporary PostgreSQL-only discovery returns are removed
from the four foreign_key_review_* reference files; native-control-only phases
remain backend-specific inside otherwise complete shared definitions.

Package seams TestForeignKeyIndexProbeVisitBounds, SelectedFamilyAuthority,
TypeFailedSnapshotAuthority, TrustedReplayNamespacePrincipal and CreateOutcomeIdentity
(each prefixed TestForeignKey) complement the shared observations. Count proof wraps
the actual selected-family ordered comparator and actual entry visitor over a
4096-entry native logical index; it asserts bounded lower-bound work, first-match
stop, exact source version locators and unchanged borrowed entries, not wiring counts.
Snapshot proof retains original metadata/storage/index authority through failed and
successful TYPE; namespace proof separates transient replay permissions from semantics.
The name_ops class uses its released text_ops family identity without adding a new
type/index capability; existing unique-index typed/provider/catalog assertions must
also run in both modes:

```sh
make check RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)'
make check REFERENCE=1 RUN='^TestReferenceUnique(TypedEquality|IndexTypedEquality|IndexProviderBoundaries|IndexDeclaredTypes|IndexBuild|IndexAtomicity|IndexContention|IndexSupportedControlsPreserveState)'
```

These are required proof commands, not observed passes. Existing FK families,
migrated callers, parser controls and standalone session-only public smoke above
remain selected. Whole-project Docker-free verification remains quickcheck.

Post-rebase FK fixture remedies preserve raw NUMERIC transport and held pid INT4
metadata/nullability/content after failed Restore. Incoming ON CONFLICT target
DO UPDATE is admitted: Producers asserts its no-RETURNING23503 and unchanged
complete bags in both modes; only RETURNING supplies the positioned native suffix
exclusion for the combined query. Required fresh selections (900 seconds each):

```sh
make check RUN='^TestReferenceForeignKey(SupportingFamilyAdmission|RestoreSkippedCreate|Producers)$'
make check REFERENCE=1 RUN='^TestReferenceForeignKey(SupportingFamilyAdmission|RestoreSkippedCreate|Producers)$'
make check RUN='^TestEnum(GenerationPublicationAtomicity|OperationSafetyCapture)$'
```

Incoming enum staged-table fixture callers pass the empty FK reference-grant
inventory explicitly; their original provenance/publication/safety assertions
are unchanged.

## Existing-index constraint attachment witnesses

The thirteen shared `TestReferenceUsingIndex` definitions use the existing fixture
and public interfaces in both modes, with direct/prepared routes and independent
values, counts, types, nullable metadata, complete logical diagnostics, notices
and relative real catalog identities. Original scalar `using_index` regression
coverage lives in `TestReferenceUsingIndexEligibility/original_using_index`;
other PRIMARY KEY boundary cases are unchanged. There are no discovery filters
or oracle loggers in the completed definitions.

`TestReferenceUsingIndexNames/foreign_key` composes an already-admitted populated
FK with both UNIQUE and PRIMARY KEY attachments, in direct/prepared routes.
It expects the full dynamic catalog23505 at Execute and one rename notice,
preserves the original FK/index identities, dependencies, data and nullable
metadata, then retries under a noncolliding name on the same connection.
The retry retains the FK and physical INCLUDE index, adds NN only to successful
PK keys, and accepts another independently expected valid child row.

Select bounded groups with the current make check contract, not dollar end anchors:

```sh
make check RUN='TestReferenceUsingIndex(Eligibility|Diagnostics|Names)'
make check RUN='TestReferenceUsingIndex(PrimaryNullability|AlterPasses|ImmediateAttributes)'
make check RUN='TestReferenceUsingIndex(Include|Ownership)'
make check RUN='TestReferenceUsingIndex(Transactions|Locks)'
make check RUN='TestReferenceUsingIndex(RetainedStatements|NativeControls|FeatureSmoke)'
```

Run each identical selector with `REFERENCE=1` for real pinned PostgreSQL18.0.
NativeControls explicitly exercises native Reset/Dump/Restore and replays the
actual portable emitted SQL in PostgreSQL; it is not a simulated server control.
The `NativeControls/provider_graph` direct/prepared subcases attach a populated
enum-key INCLUDE index with sequence and enum-provider defaults, a REGCLASS
default referencing that index, and a persistent view. They assert real default,
type, constraint/view definitions, dependency edges, values and held consumers
before/after native Restore and on actual emitted-SQL PostgreSQL replay; the next
default sequence value is independently9. View inquiry is consumed as real replay
SQL and checked against independent columns/values, not cosmetic whitespace or
dump-generated source aliases. Successful same-name attached Restore without
Reset retains the parent number and reconstructs a reciprocal constraint/index
graph in a fresh catalog generation. Both public controls diverge live data after
Dump, then prove Restore removes that divergence, rebinds held consumers and
preserves canonical redump; Reset and bad-replay controls remain separate.
`TestUsingIndexRestoreGeneration` additionally asserts exact epoch advancement
and distinct owner/epoch/address ObjectRefs, new descriptor/storage/root objects,
key-only NN/INCLUDE, immutable predecessor snapshots and retained-session writes
into the reconstructed root. Numeric definition OIDs may coincide: Restore uses
an isolated New-engine allocator, and import remaps candidate collisions rather
than reserving removed destination definitions.
Public native pre-cancellation is only a request-admission boundary.
`TestUsingIndexBlockedCancellation` starts live direct/prepared attachment work
behind a real conflicting grant, observes its native wait queue via the existing
lock seam, then cancels and joins it. It asserts no waiter/grant/NOTICE leak,
original catalog/storage/root and healthy same-session retry. These definitions
are required proof inputs, not claims of observed passing outcomes.
The NULL precedence subcases `AlterPasses/null_precedence/two_primary` and
`.../check_collision` assert runtime metadata failures before final NN scan,
both notices/one notice respectively, original complete state and healthy retry.
WHERE a>0 proves actual partial rejection; folded WHERE TRUE is a positive.
The genuine concurrent expression+partial PostgreSQL fixture has a native
expression-only setup counterpart because combined native setup is unavailable;
the separate actual partial negative and valid represented private composite
preserve both rejection obligations. Native index timing is inherently immediate;
no manufactured deferred or unique HASH provider fixture is permitted.
Index-comment SQL is PostgreSQL-only; private descriptor preservation is separate.
Namespace witnesses reuse `fixture.UniqueReferencePublicNamespace` once per case
and parameterized pg_class relnamespace restrictions, with constraint-to-parent
pg_class joins. They do not require native pg_namespace or regnamespace support.
Typed CreateIndex fixtures specify the real method explicitly: SQL's parser
supplies the B-tree default, not the package-entry executor or a new fallback.
Common catalog witnesses use the admitted pg_constraint oid/conname/contype/
conrelid/conbin surface, canonical definitions, pg_class backing identities,
actual internal pg_depend ownership and public ColumnTypes/data snapshots. Failure
snapshots also retain relation and constraint dependency edges. Native has no
pg_attribute relation: shared table metadata and NN/DML witnesses plus private
physical index descriptors retain that coverage. Exact pg_attribute, conindid,
conkey and timing inquiry are PostgreSQL-only extras; native key-only, timing and
reciprocal ownership facts also have typed descriptor proofs. SQL
selectors use supported OR comparisons rather than introducing IN inquiry work.
The semantic-Var fixture binds a real expression and publishes a validated catalog
stage: ordinary CREATE INDEX normalizes a bare Var to an attribute. TYPE rebuilds
must update the physical name-to-slot lookup when renamed key/INCLUDE labels
change; catalog identity validation remains enabled.
`TestUsingIndexCombinedTypeAttachment` covers key and INCLUDE TYPE with attachment
in both textual orders, UNIQUE/PRIMARY KEY, successful remint and renamed-target
collision. A TYPE-created index stays a private creation when attachment updates
its descriptor/name slot; existing base identities still use Replace. Original
removal, destination-name observations and publication fences remain intact.
The fixture asserts reciprocal identity/type/data, key-only NN, immutable original
descriptors/root and collision recovery through the original usable index.
Real OID-to-regclass catalog comparisons use the existing bigint casts. Absent
named ON CONSTRAINT targets fail INSERT Prepare42704 in both modes; ordinary
preattachment INSERT/SELECT and valid postattachment conflict handles are retained.
Restore requires a newly reconstructed reciprocal group/catalog generation,
not numeric OID inequality across isolated allocators or remint of a retained
same-name parent. Source authority is `restoreStage`, `remapDefinitionOIDsLocked`
and `installForeignCatalogLocked`; the last advances the catalog epoch and
remints actual storage/roots.
The PGDepend construction fixture includes a real reciprocal backing index and
INCLUDE auto dependency; semantic-Var ordering accepts only catalog-simple keys,
not arbitrary expressions, when rebuilding a unique backing group.

Affected and supplementary proof inputs:

```sh
make check RUN='TestReferencePrimaryKey(Boundaries|MalformedBoundarySuffix)'
make check RUN='TestReferenceUniqueIndex(Lifecycle|Type|NativeControls)'
make check RUN='TestReferencePrimaryKey(Lifecycle|DropRetainsNotNull|SchemaMove|IdentityGuardPrecedence)'
make check RUN='TestUsingIndex'
make check RUN='TestPrimaryKey(Attachments|CapabilityRecords|WholeInputValidation|ReservedSuffixConsumers)'
make check RUN='TestExclusion(Syntax|PropertySyntaxDiagnostics)'
make check RUN='Test(InsertConflictIndexPositionHints|PartialIndexUniqueType)'
make check RUN='TestReferenceExclusion(PropertyDiagnostics|Definitions)'
make check RUN='TestUsingIndex(BlockedCancellation|CombinedTypeAttachment|ImmutableGroup|PrivateOverlay|PublicationFences)' RACE=1
make check RUN='TestReferenceUsingIndex(Transactions|Locks)' RACE=1
```

The reference regression/lock selectors also require identical real mode,
adding `REFERENCE=1`. Package-entry tests use independently constructed ASTs and
real catalog/providers/storage/roots, not additional private copies of public SQL.
They prove immutable metadata-only vectors/root identity, existing NN/comment
retention, actual include dependencies and semantic-Var LIKE/TYPE remapping,
savepoint/private-root sharing, and validation/Finish cancellation, Reset/Restore,
name/descriptor/authorization fences with no leaked candidate. Successful Finish
cannot be retroactively vetoed by cancellation.

Root owns generation/formatting and all verification after edits. Give each
check/quickcheck/race/bench invocation one external 900-second deadline; whole
Docker-free verification is `make quickcheck`, not selected quickcheck runs.
Post-merge `make finalcheck` has an external 1800-second deadline.
These commands are proof inputs, not recorded passes.

The standalone public smoke uses NewDriver/Connector notices and database/sql:
create populated composite UNIQUE INCLUDE payload, attach as renamed PK with
stable index identity and one exact notice, verify key NN/payload metadata and
held duplicate/NULL errors, healthy writes and ordered rows, a quiet later-failing
ALTER with original catalog preserved, unnamed UNIQUE ownership, actual native
Dump/Reset/Restore with held-handle reuse, and DROP ownership/canonical redump.
Exit zero with no output is the expected observation; reference FeatureSmoke
and full native-control/fence families remain required independent proof.

## Cursor motion selected proof

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
