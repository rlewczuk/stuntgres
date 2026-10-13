# reference/dml

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### MERGE omission and ordering contracts

The untagged `merge_nothing_*_test.go` definitions share direct/prepared SQL
and independent pinned PostgreSQL18.0 outcomes. The fixed fixture selects no-op,
UPDATE and DELETE for matched candidates, no-op/DELETE for target-only candidates
and no-op/INSERT for source-only candidates. Native count is4; lib/pq count is0.
Survivors are `(1,10),(2,21),(4,40),(7,70)`; only UPDATE2, DELETE3, DELETE5 and
INSERT7 appear in the RETURNING bag. Every no-op leaves stored data unchanged.

The first TRUE eligible WHEN is terminal, including DO NOTHING; FALSE/NULL fall
through. Initial unmatched classification never widens just because no arm
selects. Unconditional clauses reject later same-class clauses with exact42601
before relation admission/binding. Namespace, Boolean, UTF8 diagnostic position,
any-column SELECT and held revoke/regrant contracts remain independently checked.
No-op presence needs SELECT on any live target column even with empty input or a
FALSE condition. Specified mutation arms keep startup mutation privileges.

Selected omission does not acquire a mutation row grant, assemble a payload,
consume a sequence/default/identity/generated value, mark an accepted RowID or
evaluate RETURNING, including ignored Exec output. Empty output retains names,
types and transport metadata; one held TRUE/FALSE/NULL handle exercises fresh
parameters. Fault-suppression source inputs use persisted zero denominators,
not constant VALUES expressions that PostgreSQL can fold during planning.

Public initial no-op finishes while the conflicting holder stays active.
PostgreSQL RowWait proves the same genuine mutation execution restarts into no-op
and, after lost ON, BY SOURCE omission still permits retained BY TARGET INSERT.
Native public timeout/healthy recovery is not restart parity; the private
RowRecheck/SourceAfter units observe actual pending row resources with frozen
source values. Cancellation units join the pending worker and check owner/claim
retirement and unchanged prefix. Controls use real native Dump/Reset/Restore,
clone/canonical redump, retained rows/metadata and held reexecution; real mode
also replays actual emitted SQL. These are not PostgreSQL physical control APIs.

Run each public group below in native mode and again adding `REFERENCE=1`.
Use an external900-second deadline for every invocation:

```sh
make check RUN='^TestReferenceMergeNothing(Mixed|Ordering|Suppression)$'
make check RUN='^TestReferenceMergeNothing(Unreachable|Namespaces)$'
make check RUN='^TestReferenceMergeNothing(Privileges|HeldACL)$'
make check RUN='^TestReferenceMergeNothing(Prepared|Metadata|Atomicity)$'
make check RUN='^TestReferenceMergeNothing(Cancellation|Concurrency)$'
make check RUN='^TestReferenceMergeNothingControls$'
make check RUN='^TestReferenceMergeNothingFeatureSmoke$'
make check RUN='^TestReferenceMergeInsertBoundaries$'
make check RUN='^TestMergeNothing(Syntax|QuerySources|Selection|Preflight|Publication)$'
make check RUN='^TestMergeNothing(RowAdmission|RowRecheck|SourceAfter|Cancellation)$'
make check RACE=1 RUN='^TestMergeNothing(RowAdmission|RowRecheck|SourceAfter|Cancellation)$'
make check RUN='^TestMergeInsertRejectsFullInput$'
```

Whole-project Docker-free proof uses `make quickcheck`. Definitions and this
description are not verification evidence. The standalone session smoke is
an OMP handoff, not a permanent repository program: from the worktree run
`timeout 900s go run "$SESSION_TMP/merge-nothing-smoke.go"`. It checks mixed
count/rows/output, held transitions, typed empty output, exact late-failure and
reachability diagnostics and actual controls; quiet success prints
`MERGE DO NOTHING smoke: ok`.

### MERGE RETURNING contracts

The untagged `merge_returning_*_test.go` definitions share direct/prepared SQL
and independent PostgreSQL18.0 expectations with native execution. Mixed actions
produce an unordered bag with one row per accepted INSERT/UPDATE/DELETE, none
for candidates with no selected arm. Plain star expands source before target;
qualified stars retain their own column order. Default target values are NEW
for INSERT/UPDATE and OLD for DELETE. Explicit OLD/NEW aliases hide the replaced
implicit names. Missing OLD, NEW or source whole rows are SQL NULL, while a
present all-NULL composite remains present.

`merge_action()` is zero-argument special syntax, lexical to MERGE RETURNING
and its expression subqueries. It is row-dependent TEXT, not a catalog overload
or statement-global action cache. Wrong arity gives42601 at the unexpected
argument. Quoted/qualified calls retain PostgreSQL42883 versus the native
generic-call capability boundary0A000; no new generic function provider is
implied. The absent SRF boundary likewise retains native `functions are
unsupported` versus PostgreSQL's RETURNING placement diagnostic.

Output is evaluated after each action's constraint/cardinality/unique admission,
before the next proposal, privately until the sole Finish. Late output errors
discard every mutation and expose no native cursor/count; PostgreSQL may stream
a prefix before rows.Err, which the fixture drains. Exec evaluates ignored
output too. Native affected counts are exact; lib/pq's MERGE result reports0.
Prepare is quiet; held execution imports fresh parameters, retains analyzed
output-only relation identities while valid, checks current grants and preserves
fixed result metadata. Adding a RETURNING-star column gives cached-result-type
0A000; invalidating SET's destination independently gives42703 first.
SQL PREPARE/EXECUTE inferred-parameter values are shared public proof.
`pg_prepared_statements.parameter_types` is a PostgreSQL-only oracle inspection;
the adjacent catalog view is not implemented natively by MERGE RETURNING.

All referenced output columns require SELECT, including absent sides, false
CASE, zero input and zero-member target whole rows. Constants/action-only output
invent no read demand. Source membership/provenance freezes before waits;
OLD uses the current predecessor after a row grant. Native public timeout/retry
proves recovery only; `TestMergeReturningRowRecheck` observes the actual private
pending resource, freezes source7 despite peer99 and requires OLD40/NEW47.

Matched-action order is unspecified: the failing matched volatility witness
permits next106 or107, not a PostgreSQL ordering promise. It then explicitly
restarts at107; the separate source-only ordered INSERT proposal witness requires
accepted output107 before the later NULL constraint failure and next108.
Native precancellation is precise; pq precancellation/session-health is not
asserted. Actual private pending cancellation checks retirement and atomicity.

Run each narrow group in native mode and again with `REFERENCE=1`, using an
external900-second deadline per invocation:

```sh
make check RUN='^TestReferenceMergeReturningCore$'
make check RUN='^TestReferenceMergeReturning(Stars|Aliases|WholeRows)$'
make check RUN='^TestReferenceMergeReturningExpressions$'
make check RUN='^TestReferenceMergeReturning(Metadata|Parameters)$'
make check RUN='^TestReferenceMergeReturning(Diagnostics|ActionPlacement)$'
make check RUN='^TestReferenceMergeReturning(Atomicity|Volatility)$'
make check RUN='^TestReferenceMergeReturning(Prepared|Privileges|Concurrency)$'
make check RUN='^TestReferenceMergeReturningControls$'
make check RUN='^TestReferenceMergeInsertBoundaries$'
make check RUN='^TestReferenceCompositeExprBoundaries$'
make check RUN='^TestMergeReturning(Frames|Publication|RowRecheck|Cancellation|OutputDemand)$'
make check RUN='^TestMergeReturning(Grammar|QuerySources)$'
make check RUN='^TestReferenceMergeReturningFeatureSmoke$'
make check RUN='^TestReferenceMergeReturningFeatureSmoke$' REFERENCE=1
```

Use `RACE=1` for the private mechanism group and, in both modes, the selected
public `^TestReferenceMergeReturning(Concurrency|Privileges|Controls)$` group.
Whole-project Docker-free verification is `make quickcheck`. Commands establish
verification; this capability description is not passing evidence.

Controls exercise actual Dump/Reset/Restore objects/data/defaults/generated
values/identity continuation, clone/canonical redump, failed/cancelled restore,
retained completed output and held reexecution. Enabled real mode also replays
the actual emitted dump SQL on PostgreSQL, not an invented PG Restore API.
Restore continues to reject MERGE. The migrated insertion/composite boundaries
now assert real RETURNING rows and final stored bags; DO NOTHING boundaries share
positive omission assertions. Partial insertion and unrelated composite exclusions
remain. Source views remain
supported; writable target views, hierarchy writes, triggers/rules/FDWs,
writable CTEs, procedural functions and COPY(MERGE) remain outside this feature.

### INSERT RETURNING oracle-first contracts

The untagged `reference/dml/insert_returning_*_test.go` definitions specify
INSERT RETURNING values, completed producer rows, OLD/NEW aliases and bounded
named whole-row composites, metadata, prepared execution, diagnostics, ACLs,
atomicity, sequence timing, current predecessors and native persistence.
Runtime-sensitive diagnostic positions, whole-row labels/transport metadata and
sequence timing were specified and independently observed against pinned
PostgreSQL18.0 before native implementation; native failures are never goldens.
Selected commands, not this capability description, establish verification.
The `generate_series` diagnostic keeps separate source-backed boundaries:
native generic function admission (`lexer.call` in `internal/parser/parser.go`)
reports0A000 `functions are unsupported` with no Hint, while PostgreSQL reports
its0A000 `set-returning functions are not allowed in RETURNING`. Both point at
the call. This does not add a native SRF provider or bypass parser admission.
No discovery backend filter is installed. In enabled reference mode, independent
PostgreSQL subtests still run after a failing memory subtest. The Controls
definition necessarily exercises actual native APIs and actual emitted dump SQL;
its replay phase requires native support and is not a server Restore substitute.

Root runs each small selector in native and real reference modes. Initial
oracle/native-red evidence precedes the implementation; repeat both after changes:

```sh
make check RUN='^TestReferenceInsertReturning(Core|Counts|Producers|Expressions|Parameters)$' REFERENCE=1
make check RUN='^TestReferenceInsertReturning(Aliases|WholeRow|Subqueries)$' REFERENCE=1
make check RUN='^TestReferenceInsertReturning(Conflict|Volatility|Atomicity)$' REFERENCE=1
make check RUN='^TestReferenceInsertReturning(Metadata|Prepared|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceInsertReturning(Privileges|Concurrency|Controls|FeatureSmoke|External)$' REFERENCE=1
make check RUN='^TestReferenceInsertReturning(ZeroColumnPrivileges|PrivilegesAfterWait)$' REFERENCE=1
make check RUN='^TestReferenceInsert(ConflictExcludedActions|ConflictUpdateBoundaries|ImplicitShortBoundariesAndCancellation)$' REFERENCE=1
make check RUN='^TestReferenceUpdateReturning(MigratedSmoke|ExternalMechanisms|ExcludedMechanisms|ExcludedMechanismsConsumerRoutes)$' REFERENCE=1
make check RUN='^TestReference(AnyValueDiagnostics|ExtremaLateFailure|ExtremaDiagnostics|NumericAvgDiagnostics|StringAggDiagnostics|DatetimeFormattingConsumers)$' REFERENCE=1
make check RUN='^TestReference(PredefinedAllData|SQLPreparedCanceledWrite|ForeignKeyProducers|ViewUpdatableInsertSecurity|RegclassWriteDiagnosticPresentation)$' REFERENCE=1
```

Each command has an external900-second deadline. Selected race proof uses
`make check RACE=1 RUN='^TestReferenceInsertReturning(Concurrency|Controls|Privileges)$'`
and the identical command with `REFERENCE=1`. Native exact pending-resource
mechanism proof supplements the public current-predecessor witness: launching a
goroutine is not proof that it reached a lock queue. Whole-project Docker-free
verification remains `make quickcheck`; never invoke `make refcheck`.
Package mechanisms additionally prove side/frame short circuiting, actual named
rowtype/zero-member retention, final-stage admission before output, required
producer-close veto, and exact manager-pending logical-row recheck:
`make check RUN='^TestInsertReturning(Modes|WholeRow|FinalAdmission|ProducerClose|RowRecheck)$'`;
use `RACE=1` for the same mechanisms. These are private production seam proofs,
not replacement public SQL definitions.

`ZeroColumnPrivileges` independently specifies table SELECT demand for zero-member
OLD/NEW/target whole rows, including zero input, while constants remain read-free.
`PrivilegesAfterWait` revokes a column used only by RETURNING: PostgreSQL retains
executor-start rights after an observed server row wait; native denies with42501.
There is no native public queue observer. Its causal companion
`make check RUN='^TestInsertReturningAuthorizationAfterWait$'` proves actual
unique-key and logical-row pending resources before revocation, direct and held
SQL preparation, no failed output/count/write and lock cleanup. Run this companion
with `RACE=1`, and run the public privilege-wait selector with `RACE=1` in both modes.

### TRUNCATE selected proof

The untagged public `reference/dml/truncate_*_test.go` definitions share real
direct/prepared SQL bodies and fixed independent counts, rows, metadata,
full diagnostics and NOTICE sequences in native and pinned PostgreSQL18.0 modes.
`TestReferencePGTableViewsIndexHistory` is migrated to successful shared
TRUNCATE while retaining its existing non-TRUNCATE summary lifecycle.
`TestReferenceInheritanceBoundaryNoEffects` retains shared inherited SELECT,
positive nextval and WHERE-false no-evaluation witnesses alongside successful
shared TRUNCATE and empty parent/child outcomes.
No discovery filter belongs in the final candidate.

The oracle-first workflow requires an explicit tests-only stop, pinned oracle
discovery, filter removal and meaningful compiling native red before authorized
production. Every changed/new/migrated definition needs fresh selected native
and real PostgreSQL proof before review/integration. Docker/image failures fail
the real run; they are not skips or native fallback. Give each command an
external900-second deadline; targets do not own deadlines:

```sh
make check REFERENCE=1 RUN='^TestReferenceTruncate(SyntaxAndPrepare|Diagnostics|Atomicity|ForeignKeys)'
make check RUN='^TestReferenceTruncate(SyntaxAndPrepare|Diagnostics|Atomicity|ForeignKeys)'
make check REFERENCE=1 RUN='^TestReferenceTruncate(Transactions|Authorization|Hierarchy|Identity|ReadOnly)'
make check RUN='^TestReferenceTruncate(Transactions|Authorization|Hierarchy|Identity|ReadOnly)'
make check REFERENCE=1 RUN='^TestReferenceTruncate(Locks|Admission|Snapshots|Lifetime)'
make check RUN='^TestReferenceTruncate(Locks|Admission|Snapshots|Lifetime)'
make check REFERENCE=1 RUN='^TestReferenceTruncate(Boundaries|ProviderControls|FeatureSmoke)'
make check RUN='^TestReferenceTruncate(Boundaries|ProviderControls|FeatureSmoke)'
make check REFERENCE=1 RUN='^TestReferencePGTableViewsIndexHistory$'
make check RUN='^TestReferencePGTableViewsIndexHistory$'
make check REFERENCE=1 RUN='^TestReferenceInheritanceBoundaryNoEffects$'
make check RUN='^TestReferenceInheritanceBoundaryNoEffects$'
make check RUN='^TestTruncate(Grammar|Generation|Indexes|PrivateRoots|Savepoints|Closure|Authority|Graph|Locks|Admission|FinalVeto|Snapshots|Provenance|Activity|Buffers)'
make check RACE=1 RUN='^TestTruncate(Locks|Admission|Authority|FinalVeto|Graph)'
make check REFERENCE=1 RUN='^TestReferenceTruncateHierarchyTemporarySessions$'
make check RUN='^TestReferenceTruncateHierarchyTemporarySessions$'
make check RUN='^TestTruncateGraphTemporarySessions$'
make check RACE=1 RUN='^TestTruncateGraphTemporarySessions$'
```

Internal units exercise real storage/descriptor/index roots, private generations
and savepoints, immutable snapshot provenance, SQL cursor producer teardown,
real manager pending resources/tickets/prefix journals and final vetoes.
Graph changes use existing validated CatalogStage publication/FK binding;
queued ACL revocation uses the real immutable ACL authority, not a mock or
new test-only production hook. Serialized queued TRUNCATE follows only proven
same-lineage generation changes; name, non-TRUNCATE and control replacements
remain fenced. PG contention records actual PID/OID/pg_locks/blocking identity.
Public native scheduling alone is not claimed as exact queue evidence.
`TestTruncateSnapshotsBirthFilter` exercises an actual undeleted new incarnation
containing peer9: the old frontier filters it while a mixed frozen q prefix
keeps its allocation-free path and a later own insert remains visible.
`TestTruncateSnapshotsInherited` and the public inherited-incarnation scenario
exercise this same filter through the existing inherited SELECT physical-leaf
consumer: unchanged parent rows survive, the peer's new child row is invisible
at the retained frontier, and the reader's own child insert is visible.
Hierarchy assertions retain the shared child's two parent edges and query the
other parent's complete inherited bag through the existing SELECT provider;
no native-only DROP workaround or new SELECT implementation is required.
`TestReferenceTruncateHierarchyTemporarySessions` uses two physical connections
and reciprocal truncation to prove foreign-session TEMP descendants keep their
rows while the invoking session's TEMP descendants participate. The internal
`TestTruncateGraphTemporarySessions` asserts excluded storage/selected identity,
AX lock entries and ticket journals, includes own TEMP/persistent positives, and
publishes a real host rename of the excluded child to exercise the matching
final graph fence without a fake provider or extra child privilege checks.

PG-only labeled witnesses exercise genuine trigger ordering/no DELETE events,
provider failure rollback, pending trigger activity, FDW and partition foreign
keys. They do not claim absent native providers. RLS is an existing native
provider and its whole-table ACL/default-deny witness runs in both modes.
SQL CREATE-in-explicit-transaction late-provider setup remains a labeled native
boundary; real internal stage authority covers grant-time graph changes.
Valid native RESTART fails positioned0A000, not a fake successful reset.

Feature smoke has both public reference selectors above and a standalone public
Go source handed to the root for temporary materialization. Under its own
external900-second deadline, it must exit0 quietly after independently asserting
direct/prepared FK RESTRICT/CASCADE, savepoints, identity/session memory, retained
typed Rows, old-snapshot TRUNCATE versus DELETE, reciprocal TEMP-descendant
session isolation, ACL and readonly diagnostics.
Record actual argv/cwd/exit/output and observations; package readiness is not
smoke evidence. Reserve quickcheck for whole-project Docker-free verification.

### COPY binary selected proof

Shared untagged `reference/dml/copy_binary_*_test.go` definitions use the existing
genuine pgx stream fixture, handwritten binary builders and complete independent
diagnostics. Provider goldens include raw SELECT/ColumnTypes and independently
encoded canonical exports; dynamic user OIDs come independently from native
`regtype::bigint` or the actual PostgreSQL catalog.
Header/EOF/options and malformed nested precedence were discovered against pinned
PostgreSQL18 before production. Native unsupported domain-constraint DDL and SQL
server observation functions are explicitly asserted, never fake parity or skips.
Private lifecycle units use actual Operation statement/transaction expiry and
caller cancellation; pinned server phases observe ClientRead/deadlines and release
complete valid payload/trailer framing before joining. Controls genuinely import
binary native state, Dump/Reset/Restore, re-export and replay its actual dump on PG.
`TestReferenceCopyBinaryReviewRegressions` distinguishes actual nullable plain
domain-array receive/NULL/base-error/OID/metadata proof from constrained-domain
setup boundaries. It includes pinned canonical NUMERIC infinity/NaN range and
multirange bytes, raw/direct-prepared consumers, real persistence controls,
complete-first UTF8 versus later missing array length, declared member truncation,
and canonical character/temporal/array OID mismatch records. Actual catalog CHAR
output retains its raw table-DDL exclusion. Public XID scalar declarations now
exercise genuine binary population, canonical four-byte export and text output;
the independently pinned short/trailing diagnostics remain 08P01/22P03 with full
COPY context. pgx XID int64 and native canonical decimal byte results are asserted
separately before normalization; existing query-derived carrier witnesses remain.

Each command has an external900-second deadline. Run every shared group in both
native and real pinned modes; race proof remains a separate native invocation:

```sh
make check RUN='^TestReferenceCopyBinary(RoundTrip|Framing|EOFBehavior|HeaderDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinary(RoundTrip|Framing|EOFBehavior|HeaderDiagnostics)$'
make check RUN='^TestReferenceCopyBinary(CodecsScalar|CodecsTemporal|CodecsStructured|CodecsNested|OutputOnly)$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinary(CodecsScalar|CodecsTemporal|CodecsStructured|CodecsNested|OutputOnly)$'
make check RUN='^TestReferenceCopyBinary(Diagnostics|Options|Admission|DatabaseSQLBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinary(Diagnostics|Options|Admission|DatabaseSQLBoundary)$'
make check RUN='^TestReferenceCopyBinary(QueryAndColumns|Consumers|Atomicity|Transactions)$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinary(QueryAndColumns|Consumers|Atomicity|Transactions)$'
make check RUN='^TestReferenceCopyBinary(Lifecycle|Controls|FeatureSmoke|ForbiddenInput|AdditionalProviders)$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinary(Lifecycle|Controls|FeatureSmoke|ForbiddenInput|AdditionalProviders)$'
make check RUN='^TestReferenceCopyBinaryReviewRegressions$'
make check REFERENCE=1 RUN='^TestReferenceCopyBinaryReviewRegressions$'
make check RUN='^TestBinary(Scalar|Temporal|Structured|Nested)$'
make check RUN='^TestCopyBinary(Options|Framing|Output|Lifecycle|FinalCancellationVeto)$'
make check RUN='^TestCopyParser'
make check RACE=1 RUN='^(TestCopyBinary(Lifecycle|FinalCancellationVeto)|TestReferenceCopyBinaryLifecycle)$'
```

Keep selected text/CSV option/framing/consumer regressions below. `make quickcheck`
is the whole-project Docker-free gate, not a replacement for selected pinned proof.
The separate ephemeral public feature program (`local://copy-binary-smoke.go`,
root-materialized `.copy-binary-smoke/main.go`) runs as
`go run ./.copy-binary-smoke`. Remove that materialization after evidence. Success
is exit0/empty stderr and exactly three LF-terminated stdout lines: `COPY 2`,
`COPY 2`, `COPY binary smoke passed`. Its expected bytes are not native exports.
The program additionally binary-populates canonical infinity NUMRANGE/
NUMMULTIRANGE and a nullable plain domain-element array, checks direct/prepared
raw metadata and text/binary exports after native and fresh restore, and checks
real catalog CHAR plus nonempty query-derived XID output. Its XID SQL seed is
solely an admitted output witness, not a substitute for binary population.

### COPY CSV selected proof

Untagged `reference/dml/copy_csv_*_test.go` shares the existing public COPY
transport, full diagnostic fixture and arbitrary-chunk/barrier helpers. Independent
handwritten rows and output bytes cover quoted NULL/empty, permissive quoting,
custom escapes, header/force matrices, pathological accepted one-byte settings,
UTF8 byte-budget Context, release-specific line counting and malformed atomicity.
Direct/prepared consumers, defaults/identity/sequence demand, transactions,
constraints and admitted multiline cancellation use real existing authorities.
Native adapter errors and Dump/Reset/Restore are explicit native phases; pinned
Controls genuinely replays emitted native SQL on PostgreSQL. No discovery filter
or staging gate belongs in final evidence.

Root runs each bounded group in both modes, with external900s deadlines:

```sh
make check RUN='^TestReferenceCopyCSV(RoundTrip|Escapes|Framing)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(RoundTrip|Escapes|Framing)$'
make check RUN='^TestReferenceCopyCSV(Header|ForceOptions|OptionsDiagnostics|PathologicalOptions)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(Header|ForceOptions|OptionsDiagnostics|PathologicalOptions)$'
make check RUN='^TestReferenceCopyCSV(Diagnostics|UnicodeContext|InputConstraints|LineCounting)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(Diagnostics|UnicodeContext|InputConstraints|LineCounting)$'
make check RUN='^TestReferenceCopyCSV(DefaultsDemand|AdmissionAndFormat)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(DefaultsDemand|AdmissionAndFormat)$'
make check RUN='^TestReferenceCopyCSV(Transactions|Lifecycle)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(Transactions|Lifecycle)$'
make check RUN='^TestReferenceCopyCSV(Controls|DatabaseSQLBoundary|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(Controls|DatabaseSQLBoundary|FeatureSmoke)$'
make check RUN='^TestReferenceCopyCSV(SeparatorTransitions|EscapedLineCounting)$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSV(SeparatorTransitions|EscapedLineCounting)$'
make check RUN='^TestReferenceCopyCSVEqualNewlineQuote$'
make check REFERENCE=1 RUN='^TestReferenceCopyCSVEqualNewlineQuote$'
make check RUN='^TestCopyCSV(Options|Framing|Fields|Output|Context|Lifecycle)$'
make check RUN='^TestCopyParser'
make check RUN='^TestCopyText(OwnerReservation|Timeout|FinalCancellationVeto)$'
make check RACE=1 RUN='^(TestCopyCSVLifecycle|TestReferenceCopyCSVLifecycle|TestCopyText(OwnerReservation|OwnerWaitCompletion|Replacement|OutputReplacement|Termination|Timeout|PanicCleanup|CopiedReservation|FinalCancellationVeto))$'
```

Review regressions explicitly cover established CR followed by CRLF, conversion
precedence before leftover LF, escaped LF/CR pairs and escapes before quotes,
initial separator history, subsequent-record Context and decoded value/output.
EqualNewlineQuote exercises actual CR/LF QUOTE bytes with default/explicit equal
ESCAPE, first-record raw Context and staged-row mixed-separator failures. Private
Framing units cover equal-quote toggles and distinct odd/even escape parity/reset;
the standalone public smoke includes both actual newline-QUOTE failures.
Their full diagnostics and unchanged committed sentinel are independently
specified and require both-mode proof; private Framing units supplement them.

Shared text codec/diagnostic regressions retain selected native and pinned
`^TestReferenceCopyText(RoundTrip|Framing|Diagnostics|OptionsDiagnostics|OptionsAdmissionPrecedence|QuotedFormat|Header|OptionsAndDefaults)$`
proof. Whole-project Docker-free verification remains `make quickcheck`, not
a broad selected-check expression.

Standalone public source `local://copy-csv-smoke.go` is session evidence:
root materializes `.copy-csv-smoke/main.go`, runs `go run ./.copy-csv-smoke`
from this checkout under an external900s deadline, records exact observations
and removes the owned materialization. It verifies NULL/empty/multiline rows,
metadata and held-prepared SELECT, selected forces/custom escaping, late-error
atomicity, Unicode diagnostics, actual native controls and retained snapshots.
Success prints only `COPY CSV smoke passed` followed by LF.

Lifecycle input blocks inside an open quoted multiline field. PostgreSQL
cancel observes ClientRead and a true pg_cancel_backend acknowledgement;
statement timeout observes its real query_start deadline. Release supplies a
closing quote before EOF so malformed-input errors cannot outrank interrupts.
Full57014 Context includes the completed raw record. Native expiry separately
observes the actual private Operation signal; caller cancellation/I/O identities
remain ordinary errors. Every blocked callback is released and its worker joined
before connection teardown.

### COPY text selected proof

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

### Positioned UPDATE reference proof

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

### DELETE USING

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

### INSERT conflict omission acceptance

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

### INSERT conflict update acceptance

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
