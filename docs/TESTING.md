# Adding and maintaining tests

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
- Every added, modified or migrated dual-mode reference test MUST be selectively
  run in both real pinned PostgreSQL mode and Docker-free quick mode, using the
  same completed test definitions and a `RUN` selector covering all affected tests.
  Repeat both selective runs after changes to those tests or their implementation.
  Fullcheck does not replace this pre-review requirement.
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
  make quickcheck PACKAGES=./reference RUN='^TestReferenceFixture' VERBOSE=0
  make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFixture' VERBOSE=0
  ```

  Schema-owner introspection through `pg_namespace` is PostgreSQL-only. Native
  reset isolation independently asserts bootstrap principals, fresh session
  settings, an empty public `Dump`, and same-name empty-table recreation. Its
  native-only public `Reset` phase removes actual inserted data/catalog state,
  preserves bootstrap principals, and again permits empty-table recreation;
  it does not add a native `pg_namespace` compatibility surface.

The affected visibility utility-boundary scenarios now assert supported MV
creation/count/stored rows and cleanup in both backends, instead of obsolete
native syntax rejection. Validate those changed contracts in both modes:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$' VERBOSE=0`
and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$' VERBOSE=0`
(900 seconds each).

Materialized-view coverage uses the same public dual-mode scenarios:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceMaterializedView' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMaterializedView' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceMaterializedViewFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMaterializedViewFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestMaterializedView' VERBOSE=0
```

Each quickcheck has a 900-second deadline. Independently specified bags retain
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
`make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference` (900 seconds each).
They preserve unquoted `materialized` type/schema legality and reject native
lossy snapshot coercions atomically beside genuine-Dump positives. NUMERIC
assertions use exact decimal driver transport, not numeric-to-text SQL casts.
KeywordTypes independently asserts native DROP TYPE's existing0A000 utility
exclusion; fixture reset and supported DROP SCHEMA CASCADE clean up native enum
types. Declaration, unquoted type references and cast legality remain shared.
Recursive/specialized snapshot scenarios use
`RUN='^TestReferenceMaterializedView(RecursiveSnapshots|TemporalSnapshots)$'`
with both `make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference` (900 seconds each).
Independently expected raw domain/enum/constrained-array/composite values and
metadata precede native genuine-Dump restoration, reminted identities, retained
MV cursors and atomic malformed recursive payload rejection. Temporal coverage
includes both infinities and converted TIMETZ int32-displacement endpoints; the
endpoint's TEXT representation is intentional because pq cannot construct its
raw invalid-Location time.Time. Normal SQL expectations are shared; no PostgreSQL
Restore API or native stored-row parity from PostgreSQL script replay is invented.

SQL prepared lifecycle definitions use `RUN='^TestReferenceSQLPrepared'` with
`make quickcheck PACKAGES=./reference VERBOSE=0` and
`make quickcheck REFERENCE=1 PACKAGES=./reference VERBOSE=0`, each with a
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
`make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference` (900 seconds per invocation).
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
`make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference` (900 seconds per invocation).
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
`make quickcheck PACKAGES=./internal/sqltype RUN='^TestMoney' VERBOSE=0`.
`TestMoneyMinimumDivisionOverflow` binds the real MONEY/INT2/INT4/INT8
Arithmetic providers with minimum raw units and -1, asserting complete native
22003 diagnostics, no result, unchanged operands and subsequent valid division.
This is the approved native safety exception: REL_18_0 cash_div_int64 lacks
an overflow guard. It is not a pinned oracle result, and no minimum/-1 SQL may
run in the shared singleton PostgreSQL fixture, discovery or smoke. Safe
neighboring negative division and checked overflow remain dual-mode witnesses.


Array aggregation uses `RUN='^TestReferenceArrayAgg'` with both
`make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference` (900 seconds each).
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
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleGrant' VERBOSE=0`
and `make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleGrant' VERBOSE=0`
(900 seconds per invocation). Migrated consumer families must also be selected
in both modes; this new family does not replace their complete assertion unions.
`TestReferenceRoleGrantColumnNoticeOrder` covers reversed/duplicate/mixed
requests, dropped attribute gaps and renamed-column diagnostic traversal.
`TestReferenceRoleGrantColumnFullMaskNotices` compares an enumerated merged
four-privilege column mask with ALL under partial and zero grant options.
Both assert complete ordered notices, quiet Prepare and real allowed/denied
consumers through direct and prepared routes. Focused gates (900 seconds each):
`make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`
and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleGrantColumn(NoticeOrder|FullMaskNotices)'`.
`TestReferenceRoleGrantFeatureSmoke` exercises the complete public grant path.
The additional root-owned standalone public Go source is retained in the OMP
session, temporarily materialized as `./.role-grant-feature-smoke/main.go`, run
with `go run ./.role-grant-feature-smoke`, and removed after recording its result.
No permanent executable or native-only SQL witness belongs in the repository.
Each invocation has a 900-second deadline:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleGrantFeatureSmoke$' VERBOSE=0`
and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleGrantFeatureSmoke$' VERBOSE=0`.
Source-provenance units complement, never replace, public delegation/revoke
witnesses. Keep upstream table-option revocation with surviving distinct-origin
column rights separate from a same-column chain's RESTRICT failure. Warning
assertions retain the complete release system/live-attribute traversal, without
claiming native system-column query support.


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
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceInheritanceRetainedExclusionDefinitionCollision$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./internal/engine RUN='^TestInheritanceControlsRetainedExclusionDefinitionCollision$' VERBOSE=0
```

Root-run selective gates (900 seconds each, after edits and generation finish):
```sh
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReference(Inheritance|NotNull|Check|TableLike)' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Inheritance|NotNull|Check|TableLike)' VERBOSE=0
timeout 900s make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^(TestInheritance|TestNotNull|TestTableLike|TestAlterDefault)' VERBOSE=0
timeout 900s make quickcheck RACE=1 PACKAGES=./internal/engine RUN='^TestInheritance(Admission|Graph|Controls)' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceInheritanceFeatureSmoke$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInheritanceFeatureSmoke$' VERBOSE=0
```
The standalone public API smoke is supplied as temporary session material, not
committed scaffolding. Neither selected gates nor smoke replace full quickcheck
and the post-integration3600-second fullcheck.

The dependency-shape review regressions independently assert PostgreSQL provider
CASCADE column/definition removal and native atomic inheritance ALTER rejection,
including consumer recovery and graph enforcement. Run the same completed bodies
in both modes (900 seconds per invocation):
```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInheritanceDependency(Shape|Definition)Boundary$' VERBOSE=0
```

Retirement authority regressions cover provider schemas containing the sole child
or the parent, each with typed-column, DEFAULT and CHECK dependencies. Both routes
assert retained standalone physical rows/metadata/enforcement or complete outside
descendant retirement, without weakening the surviving-graph boundary above:
```sh
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceInheritanceRetirementAuthority$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInheritanceRetirementAuthority$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./internal/engine RUN='^TestInheritanceDependencyAuthority$' VERBOSE=0
```

Prospective retirement regressions use a parent that is also a regclass provider
for the retained other parent's DEFAULT or CHECK. CASCADE discovers their shared
child; the retained parent becomes standalone before its definition is pruned.
Both direct/prepared routes assert removed relations/edges, retained rows and
metadata, consumed provider definitions and independent local definitions; native
held eager child results retain actual original values and metadata. The real-stage
units use the same q root with actual admission, not a pre-removed child:
```sh
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceInheritanceProspectiveRetirement$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInheritanceProspectiveRetirement$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./internal/engine RUN='^TestInheritanceProspectiveRetirement$' VERBOSE=0
```

`TestReferenceInheritanceLocalExclusionReplay` covers a child with only inherited
columns and a first local EXCLUDE declaration. It consumes the actual native dump
through Reset/Restore and pinned PostgreSQL replay, then asserts rows, default
enforcement and graph edges. Native replacement also retains a live TEMP-to-TEMP
inheritance graph, its exclusion constraint OID, physical data and enforcement.
Include `Inheritance|Exclusion` when verifying this integrated composition; do not
replace these consumer outcomes with dump-string or canonical-byte assertions.

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
   `make quickcheck`; final `make fullcheck` runs after integration under the
   catalog-part workflow. Selected tests do not replace full verification.

RANGE window coverage selects the complete `^TestReferenceQueryWindow` family
in both `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryWindow' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryWindow' VERBOSE=0`
(900 seconds each). Feature smoke uses `^TestReferenceQueryWindowRangeFeatureSmoke$` in both modes.
Named volatile-offset identity is covered by
`^TestReferenceQueryWindowRangeNamedOffsetIdentity` in both modes: same name
advances nextval once; distinct names advance it twice and give the final pair
{2,3}, without assuming evaluation order. The stored-view witness cycles offsets
1/2 to retain independent membership through repeated export/replay and native
Dump/Restore. Actual clone/remap units preserve query-local named identity while
checking shared sort groups. Successful support binding performs neither source
diagnostic formatting nor class-input diagnostic lookup.
Provider units use `PACKAGES=./internal/sqltype RUN='^TestWindowRange'`;
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
Run `make quickcheck PACKAGES=./internal/parser RUN='^TestParseScratch'` and its
`RACE=1` counterpart, then the complete parser package and ordinary quickcheck.

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
deltas. Rerun the complete real-reference race command under the unchanged
900-second quickcheck deadline before claiming a corpus improvement; disclose
timeouts/failures honestly. Selected dual-mode reference families and the actual
public executable smoke supplement, never replace, complete verification and
postintegration fullcheck.


### Commands and oracle-first discovery

Run from repository root with Go >=1.26 and GNU `timeout`. Real mode also needs
working Docker and image/module access.

```sh
# First: real PostgreSQL discovery/validation.
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$' VERBOSE=1
# Second: Docker-free validation of the recorded contract.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Before review: both modes, using exactly the same completed test.
make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureBehavior$'
# Ordinary full Docker-free verification.
make quickcheck
# After integration: all final gates under a one-hour deadline.
make fullcheck
```

Ordinary quickcheck and selective reference runs retain their 900-second limits.
Fullcheck gives its complete pinned PostgreSQL race stage a 3600-second shell and
Go package deadline inside the same 3600-second aggregate gate; native stages
remain capped at 900 seconds. This exception preserves the complete scenario and
race coverage, not a skip or permission to loosen SQL timeout expectations.
`TEST_TIMEOUT` is a numeric count of seconds, defaulting to `900`; fullcheck
explicitly selects `900` for native stages and `3600` for complete reference race.


`make quickcheck REFERENCE=1 PACKAGES=./reference` enables `-reference=true` and the `reference` build tag; it runs
both native and PostgreSQL backends, not PostgreSQL alone. The ordinary runner
visits native first. During the initial oracle-discovery iteration, use the
shared `p.backends()` loop and temporarily select only `backend.pg` (for example,
`if !backend.pg { continue }`) so an unimplemented native feature cannot prevent
PostgreSQL observations. Remove that discovery filter before quick validation
and final verification. Never commit a filter that bypasses either backend.

There is no automatic golden-file recorder or PostgreSQL-only make mode.
Use failure diagnostics or temporary verbose-only observation logging to inspect
unknown PostgreSQL results, then encode those results as explicit expectations
and remove discovery scaffolding. `p.step` validates expectations; it does not
learn or update them. Do not weaken assertions to accommodate native failures.

PG_USER coverage uses `RUN='^TestReferencePGUser'` in both
`make quickcheck PACKAGES=./reference` and
`make quickcheck REFERENCE=1 PACKAGES=./reference`, with a 900-second deadline
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
`make quickcheck PACKAGES=./internal/engine RUN='^(TestPGUser|TestTableViewSummaryBuiltinIdentity)' VERBOSE=0`.
Related reference regression uses
`RUN='^(TestReferencePGUser|TestReferenceTimezoneViews|TestReferenceRegtype|TestReferencePGTableViews)'`
in both quick and REFERENCE=1 modes (900 seconds per invocation). This preserves
existing timezone routing, allocated regtype identity expectations and scoped
summary inventories while checking pg_user view classification.

For initial oracle discovery, temporarily filter native backends in the common
scenario helper as described above, then run
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGUser' VERBOSE=0`.
The exact postgres-only subtest selector is
`RUN='^TestReferencePGUser[^/]*$/^postgres$'`; full diagnostics can be selected
with `RUN='^TestReferencePGUserDiagnostics$/^postgres$/./^(direct|prepared)$'`.
`TestReferencePGUserOracleCatalog/postgres` additionally checks release attribute
types, typmods, collations and relation/rowtype identities, and independently
asserts the exact `pg_views` definition (differences print under VERBOSE=0).
These allocator assertions are
release-only research, not cross-backend role or relation allocator parity.
Remove the temporary backend filter before recording the missing-feature quick
regression and before all final dual-mode runs. Preserve the permanent
PostgreSQL-only `pg_attribute` research guard and native-only control phases.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

### Forward-only SQL cursor verification

Run the entire cursor family after changing an oracle expectation: a driver panic
can truncate later tests, so a focused passing run does not establish family
coverage. Each invocation has a separate 900-second deadline:

```sh
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSQLCursor' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSQLCursor' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSQLCursorFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSQLCursorFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSQLCursorRegtypeWarnings$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSQLCursorRegtypeWarnings$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtypeWarnings$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtypeWarnings$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestSQLCursor(CompletedPublicationBoundary|CancellationClearsAuthority)$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestSQLCursor' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestSQLCursor' VERBOSE=0
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
success but before Finish for FETCH, DECLARE, CLOSE and CLOSE ALL. Expect success
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
make quickcheck PACKAGES=./reference RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionUnsupportedSessionBoundaries$)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(SQLCursor|AdvisorySession|PreparedTransactionUnsupportedSessionBoundaries$)' VERBOSE=0
```

Each command has its own900-second deadline. The prepared-transaction boundary
scenario retains PostgreSQL's actual WITH HOLD/PREPARE restriction and independently
asserts native0A000 `WITH HOLD cursors are unsupported` at position32, not the
obsolete DECLARE syntax error. Hash and join logical-stage fixtures keep real
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceMergeInsert' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMergeInsert' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertImplicitShort' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertImplicitShort' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertSelect' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertSelect' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestMerge' VERBOSE=0
```

The shared MERGE family independently checks frozen matching/bag multiplicity,
ordered conditions, mappings/defaults/identity/generated values, scalar
subqueries, complete diagnostics and analysis/execute stages, atomic constraints,
transactions, metadata, held rebind, locks/cancellation and native controls.
OracleQuestions permanently records O1–O4: demanded nonboolean ON, action
subqueries, system-column namespaces/providers and permission/read-only order.
Native count n and observed pinned pq export0 are separate expectations with
exact persisted rows, never inferred from driver count alone. Native provider42703
versus positive PostgreSQL system identities is explicit; native column ACL units
supplement real PostgreSQL GRANT, without implementing an adjacent public API.
Fixtures use supported named UNIQUE constraints with NOT NULL (implicit for
identity); they do not depend on PRIMARY KEY support. Native controls receive the unwrapped `p.native` pool;
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
`HeldACL` retains one actual PostgreSQL handle through target all-arm INSERT and
source SELECT revoke/restore; native `TestMergeInsertHeldACL` retains the same
AST/description through the adapter's `scope.Expected` execution seam.

Actual public feature smokes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceMergeInsertFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMergeInsertFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertImplicitShortFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertImplicitShortFeatureSmoke$' VERBOSE=0
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
INSERT SELECT is included above because INSERT input binding is shared.

### Encoding, locale and float-presentation settings

The eleven shared definitions live in `reference/format_encoding_*_test.go`.
Their completed selector must run in both modes before review/integration,
under separate 900-second deadlines, without verbose output:

```sh
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$' VERBOSE=0
```

For changed locale numeric-parsing witnesses, validate the existing nested
`to_char(to_number(...),'FM9999.00')` TEXT result and raw NUMERIC decimal codec
against the pinned oracle first; do not admit general Numeric-to-TEXT just to
make a selected-setting witness executable:

```sh
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Locales|Consumers)$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Locales|Consumers)$' VERBOSE=0
timeout 900s make quickcheck PACKAGES='./internal/sqltype ./internal/engine ./internal/parser' RUN='^(TestFormatEncoding.*|TestExplicitCoercionMatrix|TestStringPlanSpecializedOutputStability|TestSettingUtilitySnapshotOwnership|TestArrayVolatilityPreservesScalarClassification|TestStringGenerationOutputDependence)$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$' VERBOSE=0
```

The executable feature/control smoke is in `format_encoding_controls_test.go`:

```sh
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$' VERBOSE=0
```

It asserts UTF8 Unicode transport, C numeric/time masks, current-role message
authority, transactional precision/restoration, held and retained results, and
native exact binary/Dump/Reset/Restore/emitted replay controls. PostgreSQL's
rounded scalar transport and native's exact scalar export are independently
asserted, not normalized into false equality. Native locale/client-encoding
capability errors have explicit backend expectations; ICU is policy only.
Ordinary quickcheck and the single post-integration 1800-second fullcheck remain
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceJSONPath' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceJSONPath' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceJSONPathFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceJSONPathFeatureSmoke$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceCollationSimilarCopiedUnicode$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCollationSimilarCopiedUnicode$' VERBOSE=0
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
make quickcheck RACE=1 PACKAGES=./internal/engine RUN='^TestCollationProviderConcurrentRolePublication$' VERBOSE=0
```

The already integrated `TestReferenceLikeLegacyMigration` and its stage/namespace
scenarios remain the single owner of the former LIKE smoke assertion union; the
duplicate collation-named migration is removed. Required final selective gates
also explicitly include the three changed non-collation-named scenarios:

```sh
make quickcheck PACKAGES=./reference RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$' VERBOSE=0
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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceTableLikePartial' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTableLikePartial' VERBOSE=0`.
Private cooked-tree, stable/dense attribute, fresh membership-root and actual
provider-admission witnesses use
`make quickcheck PACKAGES=./internal/engine RUN='^TestTableLikePartial' VERBOSE=0`.
The expanded existing feature smoke includes actual partial-index Dump/Restore
and PostgreSQL replay with future membership, uniqueness and predicate-first
errors. The complete shared union also retains CoreNativeControlRoundTrip's
standalone typed/direct and quoted/prepared MAXINT8/O'Brien/TRUE rows and metadata
after actual Restore, plus CoreSemanticSkipNotices' quoted unknown `"integer"`
IF NOT EXISTS notice and unchanged-data witnesses. Each quickcheck invocation
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceTableLikeComposite' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTableLikeComposite' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestTableLikeComposite' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceTableLikeTemporaryRetained' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTableLikeTemporaryRetained' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)' VERBOSE=0
make quickcheck RACE=1 PACKAGES=./internal/engine RUN='^TestTableLike(TemporaryRetained|CookedRowRemap)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestTableLikeDefinitionDependencyOrder' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTrunc' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTruncFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTruncFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestDatetimeTrunc' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestDatetimeTrunc' VERBOSE=0
```
Smoke expects distinct NY fold hours05/06UTC, spring day05UTC, Sydney day13UTC,
held NULL/error recovery, unchanged late-write sentinel and symbolic stored
default/view/generated/index replay. Full family checks additionally cover
week2006-01-01→2005-12-26, quarter2001-02-16→2001-01-01, negative interval
seconds→-1second and TIME24:00→zero-day24hour INTERVAL. These selected gates
do not replace ordinary quickcheck or post-merge fullcheck.

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
make quickcheck PACKAGES=./reference RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(DatetimeTxTimes|TimestampSQLTransactionChainClock)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeTxTimesFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES='./internal/engine ./internal/parser' RUN='^TestTransactionTime' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericFormattingFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericFormattingFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestNumericFormatting' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(NumericFormatting|DatetimeFormattingBindingAuthority)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestNumericFormatting' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversion' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversion' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversionFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestBinary(Conversion|Encoding|Format)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestBinaryConversion' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestBinaryConversion' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryConversionActiveCancellation$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryConversionActiveCancellation$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringNumericBaseFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringNumericBaseFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(StringNumericBase|BinaryHash)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestStringNumericBase' VERBOSE=0
```

The smoke expects `fffffb2e` versus `fffffffffffffb2e` for INTEGER/BIGINT -1234,
`0` for zero, RFCabc/empty MD5, typed NULL, exact ambiguity/missing/input
diagnostics, no partial publication, and successful held-call recovery. Typed
nil `[]byte` inferred as TEXT intentionally retains native empty-input MD5 versus
pq NULL; do not erase this inherited adapter difference or use Go kinds for
signature selection. These selectors do not replace ordinary quickcheck or
post-merge fullcheck.

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
make quickcheck PACKAGES=./reference RUN='^TestReferenceSize' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSize' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSizeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSizeFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(Size|Math|Numeric)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestSize' VERBOSE=0
```

The smoke expects 1048576 INT8, `1024 bytes`, `-11 kB`, `10.5000 bytes`,
beyond-BIGINT `10240 PB`, typed TEXT NULL metadata, exact malformed-unit and
overflow records, held recovery, no failed-write prefix, and faithful stored
overloads/control replay. The shared MathPlan and numeric codec seams also
require existing math/numeric regression coverage; selected checks do not
replace ordinary quickcheck or post-merge fullcheck.

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
make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteUsing|^TestReferenceDeleteCoreExcludedForms' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(DatetimeZone|Timezone)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(DatetimeZone|Timezone)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeZoneFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeZoneFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestDatetimeZone' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestDatetimeZone' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestDatetimeZone' VERBOSE=0
```

Both complete reference modes must run before review/integration. Each quickcheck
has a900-second deadline. Provider units include warmed EST/MSK/IANA allocation
witnesses; they supplement, not replace, public independent SQL expectations.
The discovery backend.pg filter and focused logging-only oracle probes are
temporary and must be removed before native checks; neither remains in the
completed family. The root runs verification and feature smoke after edits, then
the final post-merge fullcheck under one1800-second deadline.

The interval-justification family covers exact30-day/24-hour boundaries,
mixed signs and component extrema, complete diagnostics and phases, metadata,
held inferred parameters/recovery, four styles, existing consumers, statement
atomicity and native Dump/Restore with actual emitted SQL replay. Precanceled
ordinary context errors are a native-only API boundary, not a race-prone fast pq
request assertion. Run the same complete definitions in both modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeJustify' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeJustify' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeJustifyFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeJustifyFeatureSmoke$' VERBOSE=0
```

Each invocation has a900-second deadline. Supplemental units use
`PACKAGES=./internal/sqltype RUN='^TestDatetimeJustify'`,
`PACKAGES=./internal/parser RUN='^TestDatetimeJustify'` and
`PACKAGES=./internal/engine RUN='^TestDatetimeJustify'`; neither these nor selected
smoke replace ordinary quickcheck or the post-merge1800-second fullcheck.

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
make quickcheck PACKAGES=./reference RUN='^TestReferencePGShDescription' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGShDescription' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGShDescriptionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGShDescriptionFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGShDescription' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeOverlaps' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeOverlaps' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeOverlapsFeatureSmoke$' VERBOSE=0
```

Each invocation has a900-second deadline. Supplementary parser, sqltype and
engine units use `RUN='^TestDatetimeOverlaps'` with the matching
`PACKAGES=./internal/parser`, `./internal/sqltype` or `./internal/engine`.
These selective checks do not replace ordinary quickcheck or the single
1800-second post-merge fullcheck. The feature smoke asserts actual period
results `(true,false,true,true,NULL)`, held reuse, Denver day/hour differences,
diagnostic recovery and native stored-clock-duration replay.

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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGSequence' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGSequence' VERBOSE=0
```

After implementation, include all migrated definition probes in both modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGSequenceFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGSequenceFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGSequence' VERBOSE=0
```

Each quickcheck invocation has a 900-second deadline. The final fullcheck runs
after merge under its single 1800-second deadline; neither gate replaces the
selective dual-mode runs.

The extraction family uses the same shared fixtures and public APIs in both
modes, with explicit independent values, diagnostics and metadata. Its smoke
covers all six source types and both APIs, strictness, ISO/BC, interval parts,
infinities, precision, prepared Execute-time zones and stored-definition replay:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeExtractFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeExtractFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeExtract' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeExtract' VERBOSE=0
```

Each invocation has a900-second deadline. Both complete modes must pass before
review/integration; the pinned mode is not deferred until final fullcheck.
Supplemental units use `PACKAGES=./internal/sqltype`, `./internal/parser` or
`./internal/engine` with `RUN='^TestDatetimeExtract'`. Regenerate grammar with
`make generate` after parser source changes; generated code is not hand-edited.
The root performs these checks and the single1800-second final fullcheck.
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)' VERBOSE=0
```

Validate the same completed definitions in both modes before review; each
quickcheck invocation has a 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
```

The feature smoke independently asserts name/OID conversion, builtin and user
row identities, qualification/shadowing, held input recovery, metadata,
persistent scalar/domain/array values and native control atomicity. Native
controls must replay the actual emitted native stream on PostgreSQL; never
substitute a manually reconstructed stream or an invented PostgreSQL
Dump/Reset/Restore API. The unchanged cohesive fixture's `checkRaw` hook supplies
independent REGTYPE/array transport checks through the small regtype helper.
These selectors do not replace ordinary quickcheck or the post-merge fullcheck
under its single 1800-second deadline. Completed definitions are not passing
verification until the root records successful runs.

`TestReferencePartialIndexTypeGeneration` composes the same supported domain/
associated-array displacement with a simple-key partial UNIQUE index whose
predicate is the sole changed stored type consumer. Direct and held routes assert
stable array OID, renamed current metadata, exact rows and member23505,
outside-array admission, failed UPDATE atomicity and retained native cursor
metadata/content. It supplements, not replaces, the complete Regtype family:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(PartialIndexTypeGeneration|RegtypeArrayDisplacement.*)$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPartialIndexPredicateTypeGeneration' VERBOSE=0
```

`TestReferencePartialIndexWindowDiagnostics` retains direct/held and UTF8-position
routes for window functions in partial predicates: full42P20 predicate-context
diagnostics, no-OVER42809 precedence, no effects and same-name supported recovery.
Keep the complete QueryWindow family selected after shared-comparator/binder
changes. Structural metadata has no native SQL pg_index projection; direct
`TestPartialIndexMetadataPredicate` consumers assert exact current/retained
truth/membership and original attribute dependencies despite simplification.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(PartialIndexWindowDiagnostics|QueryWindow)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPartialIndexMetadataPredicate' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Regtype|Role|ControlTransactionsHostAuthorization)' VERBOSE=0
```

Role-attribute oracle findings are encoded in the untagged shared
`TestReferenceRoleAttributes...` scenarios; discovery filters/unresolved reports
are removed. Root-observed selected native and pinned gates passed before this
documentation cutover. A separate actual public Go smoke reached creator ADMIN,
ALTER USER omission, expired trusted admission, physical limit release, NOLOGIN
and held authority refresh. Those observations do not replace the remaining
full quickcheck, fresh affected selectors, review or post-merge fullcheck.

Run the exact changed reference definitions in both modes before review, each
with a900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRole(Attributes(DefaultsAndAliases|Authority|DiagnosticsAndAtomicity|ConnectionAdmission|PasswordAndValidity|RetentionAndControls|FeatureSmoke)|MigratedSmokeControls|TransactionMutationBoundary|TrustedConnectorAdmissionAndOpaqueIsolation)$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(Role(Attributes(DefaultsAndRetention|Authority|MembershipNonInheritanceAndAdminTraversal|ConnectionLimitLifecycle|ConnectionLimitRaces|PasswordStorage|PasswordSASLprep|ValidityInput|PublicationAndSnapshots|ControlsRetention)|AuthorityAndSurface)|FormatEncodingCurrentRoleAndResetAll)$' VERBOSE=0
```

The format fixture uses the canonical attribute executor with a real captured
StatementScope. Trusted connector NOLOGIN specifically asserts FATAL28000 with
complete diagnostics; missing/miscased native names remain ordinary host errors.
All other opaque-key, Driver isolation, authenticator and replacement witnesses
remain selected by the broader role family above. Privileged pg_roles/pg_authid
observations remain PostgreSQL-only; membership GRANT/REVOKE authority witnesses
run on native too. Native controls and explicit-transaction exclusions are
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
make quickcheck PACKAGES=./reference RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionUnsupportedSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(TemporaryTable|SequenceTemporaryTable|Unique(CreateTransactionBoundary|TemporaryLifecycle)|PreparedTransactionUnsupportedSessionBoundaries|VisibilityInquiry(Boundaries|UtilityBoundaries)|ArrayNativeControls|QueryTableCreate|PGDependTransactions)' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^Test(TemporaryTable|TemporaryRelation|CatalogTemporary|SchemaSearchPathSessionDefaultsAndSpecials|SerializableEvidenceActualTemporaryNamespaceAndSequenceProviders|PGDependDefaultReplay)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceTemporaryTableFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTemporaryTableFeatureSmoke$' VERBOSE=0
```

Both selected modes use the same completed definitions under900-second deadlines;
ordinary quickcheck and the post-merge1800-second fullcheck remain required.

The datetime-constructor smoke directly asserts all five exact result types and
values, BC, TIME24:00/TIMESTAMP carry, sparse `=>`/`:=` interval defaults/NULL,
held prepared session-zone changes, full calendar/seconds/zone/signature errors,
later-row write atomicity and named stored-definition Dump/Restore with replay
of actual emitted native SQL against PostgreSQL:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDatetimeMakeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDatetimeMakeFeatureSmoke$' VERBOSE=0
```

Run the complete identical `RUN='^TestReferenceDatetimeMake'` family in both
modes before review, with900-second deadlines. Supplemental constructor units
use `PACKAGES=./internal/sqltype RUN='^TestDatetimeMake'`,
`PACKAGES=./internal/parser RUN='^TestDatetimeMakeNamedArguments$'` and
`PACKAGES=./internal/engine RUN='^TestDatetimeMake(Binding|Properties|ArgumentAssociation)$'`.
These selectors do not replace ordinary quickcheck or post-merge fullcheck.
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceBitstring' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBitstring' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$' VERBOSE=0
```

Both modes are required before review/integration. Final fullcheck remains one
1800-second gate after merge. Discovery-only deliberate observation failures
and PG-only filters must be removed before the native failing proof and gates.

The string-encoding smoke executes all five functions with independently expected
rows/types, Unicode16 assigned/unassigned codepoints, exact conversion and escape
diagnostics, held valid/NULL/error/recovery frames and stored NAME/INT4 overloads.
Native phases exercise retained snapshots, Dump/Reset/Restore, failed Restore
atomicity and real PostgreSQL replay of actual emitted native SQL:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringEncodingFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringEncodingFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceStringEncoding' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringEncoding' VERBOSE=0
```

Each invocation has a900-second deadline. Smoke does not replace the complete
family, ordinary quickcheck or post-merge fullcheck. The offline Unicode generator
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
Run each selector with both `make quickcheck PACKAGES=./reference RUN='...' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='...' VERBOSE=0`.
Affected ordinary-route regression selectors additionally include
`^TestReferenceBtreeIndex(OrdinaryShareBuild|ExcludedDefinitionsDiagnostics)$`
and `^TestReferenceRelationDeadlock`.
PostgreSQL queue/progress observations use actual lock/PID identities. Native
ordinary pending-Share/later-writer queue ordering is proved by internal lock
units: no public native queue catalog or worker-start timing is substituted.
The discovery-only pg-only filter has been removed: every final shared backend
loop discovers native and PostgreSQL scenarios according to the standard mode.
Native U1–U7 use `make quickcheck PACKAGES=./internal/engine RUN='^TestConcurrentIndex' VERBOSE=0`;
parser ordinary/concurrent heads use the same invocation with `PACKAGES=./internal/parser`.
Each quickcheck invocation has one 900-second deadline. Root owns parser
generation, formatting and verification after all implementation edits land;
agents do not run mid-flight checks. Final post-merge fullcheck uses one
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
```

This smoke does not replace the complete identical dual-mode family selection
documented in [the index-clause design](design/PG18-INDEX-CLAUSES.md).

The index-property smoke checks the eleven independently expected BOOL values
for B-tree AM/index capabilities, mixed DESC/NULLS ordering and INCLUDE roles,
plus prepared true/false/NULL and explicit native Reset/Restore/foreign restore:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexPropertiesFeatureSmoke$' VERBOSE=0
```

Run both with a900-second deadline. The full `^TestReferenceIndexProperties`
selector covers every new definition; see [the inquiry design](design/PG18-FUNC-INFO-INDEX-PROPERTIES.md)
for the shared-coercion consumer risk selectors. Smoke does not replace those
identical dual-mode selective runs, ordinary quickcheck or post-merge fullcheck.
The dependency catalog family independently specifies exact represented edge bags,
all seven result descriptors, owner-specific diagnostics, supported private
sequence changes, retained cursors and native controls with actual SQL replay.
Run the identical complete family in both modes, each with a900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePGDepend' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGDepend' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGDependFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGDependFeatureSmoke$' VERBOSE=0
```
Changed existing default definitions must also run in both modes; the family
prefix does not select them. They preserve genuine direct and prepared CREATE,
default INSERT, data/provider sentinels and existing excluded sequence-expression
controls without separate duplicated PostgreSQL-only success bodies:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$' VERBOSE=0
```

Each selective invocation has a900-second deadline. Source-route migration
definitions are additional named selections, not replacements for this family
or these changed existing definitions.


Supplemental collector/walker units use
`make quickcheck PACKAGES=./internal/engine RUN='^TestPGDepend' VERBOSE=0`.
These do not replace the dual-mode runs, ordinary quickcheck or post-merge
fullcheck. Public capability boundaries remain explicit: no REGCLASS column
storage, set-operation query rollout, transactional CREATE TABLE/ALTER DEFAULT
or native TEMP-table creation is implied by metadata reading.


The prepared-transaction family independently asserts state/diagnostics,
ownership and five-column XID metadata, physical-origin-close survival,
SESSION/LOCAL and TEMP restrictions, held resources, coupled existing-index/
ALTER/data/sequence publication, SSI finalization and native replacement guards:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferencePreparedTransaction' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePreparedTransaction' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePreparedTransactionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePreparedTransactionFeatureSmoke$' VERBOSE=0
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
make quickcheck PACKAGES=./internal/engine RUN='^TestPrepared' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOps(Consumers|ViewsControls|SourceDependencies|Lifecycle|DemandAdmission|FeatureSmoke)$' VERBOSE=0
```

Each invocation has a900-second deadline. The complete family selector is
`RUN='^TestReferenceQuerySetOps'` in both modes; smoke does not replace it or
ordinary quickcheck/post-merge fullcheck. Lifecycle checks row-dependent late
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOpsStoredLockBoundaries$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestQuerySetStored(InheritedMarks|LockAuthority|ReferenceKinds|RelationModes)$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceHashIndexFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceHashIndexFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestHashIndexLookupFeatureSmoke$' VERBOSE=0
```

Each quickcheck has a900-second deadline. Full changed-definition selectors must
run identically in both reference modes before review/integration:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(HashIndex|Unique|IndexClauses|BtreeIndex|ExpressionIndex|IndexMaintenance|IndexProperties|DeparseRelationsIndexForms|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(TableSummary.*|TableViewSummaryBuiltinIdentity)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$' VERBOSE=0
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGIndexesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGIndexesFeatureSmoke$' VERBOSE=0
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
make quickcheck PACKAGES=./internal/engine RUN='^(TestPGIndexes.*|TestIndexMaintenanceMetadata.*)$' VERBOSE=0
```

The shared referenceHeldStatement retains its three-field layout and adds actual
plain DB/Tx Prepare/Exec/Query consumers alongside existing Context consumers.
Its matched query uses the genuinely held statement; unmatched queries use the
underlying plain DB/Tx, never a Context stand-in. This central fixture change also
requires supplemental identical all-reference risk selection (each900 seconds):

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(BinaryAccess|StringPad).*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(BinaryAccess|StringPad).*$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryAccessFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryAccessFeatureSmoke$' VERBOSE=0
```

StringPad protects the reused reverse provider and shared parser/binder.
Feature smoke exercises the released six-function projection, held frames,
bounds/new-bit failure and recovery, retained source, empty/NULL metadata and
controls replay. These gates do not replace root quickcheck or post-merge
fullcheck (one 1800-second deadline).

## RANGE partition reference coverage

`reference/range_partition_*_test.go` uses one shared scenario body in both modes.
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
virtual roots/intermediates) with `pg_class`, not unavailable native
`pg_roles`/`pg_namespace` catalogs. Manual ownership combines
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
make quickcheck PACKAGES=./reference RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(RangePartition|CollationNativeCollision|TableCore|AlterDropColumnExternalHierarchy)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRangePartitionFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRangePartitionFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRangePartitionMigratedTableCoreSmoke$' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestRangePartition' VERBOSE=0
make quickcheck RACE=1 PACKAGES=./internal/engine RUN='^TestRangePartition(Concurrency|Publication|Capture|Remap)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|FailureOrder|OrderedDiagnostics)$' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(CheckInsertPrivilegeDisclosure|RangePartition(ConstraintPrivacy|PhysicalConstraintPrivacy|Retirement|RemapExplicitCollation))' VERBOSE=0
```

Package-function witnesses inspect owned descriptors, cooked type/row identities,
private importer provenance, real lock queues, multi-leaf transaction stages and
complete self-catalog expression remapping with retained old cursors. They supplement
the shared public reference routes. The root generates/formats the parser and runs
verification only after all edits pause; the post-integration fullcheck runs once
under a single 3600-second deadline, with actual failures remedied separately.

## How the dual-mode fixture works

`newReferenceScenario(t)` creates a fresh native engine, a middleware-wrapped
native pool and an unwrapped pool for native controls. In quick mode PostgreSQL
is absent. In real mode it also opens the pinned PostgreSQL pool. The fixture
owns a 30-second context and pool cleanup; real fixtures reset `public` before
and after each case. All real fixtures share one bounded-lifecycle container:
**do not use `t.Parallel()`**.

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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`
and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFixture(SessionAuthorizationReset|NativeIsolation)'`.

Non-public schemas are not reset by the fixture. Register
`schemaReferenceCleanup(t, "schema_name")` before constructing the scenario
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

`p.step(t, step)` executes one description on native and, when enabled,
PostgreSQL. Each backend must match the supplied expectations independently;
complete observed records are then compared for parity. SELECT checks include
column names, database type names, complete rows and owned middleware-recorded
rows/closure. Raw codec representations are checked before normalization.

Use `p.step` for straightforward streams. Use `p.backends()` and one shared
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
existing CPU scenarios, and lock workers use `referenceLockOperation` plus the
PostgreSQL external queue barrier. No new delay SQL is needed.

```sh
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceTimeout(Settings|TransactionBoundaries|TransactionAssignments|TransactionAbortRestore|IdleBoundaries|Precedence|ChainPrepared|FatalCleanup|PoolNoReplay|FeatureSmoke)$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTimeoutFeatureSmoke$' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceTimeoutFeatureSmoke$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSettingsPublicTimeout' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceSettingsPublicTimeout' VERBOSE=0
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
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestTimeoutOracleLogFrames$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestTimeoutOracleLogFrames$' VERBOSE=0
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
`timeout 900s make quickcheck PACKAGES=./internal/engine RUN='^TestSessionTimeout'`
and `timeout 900s make quickcheck PACKAGES=. RUN='^TestSessionFatalNoReplayMechanics$'`.
These cover callback generation/token/ref/deadline authority, provisional adoption,
assignment/restoration, configured idle suppression, active/ready boundaries,
first end reason, copied fatal diagnostics, canonical RESET ALL batch/journal/cache
publication/restoration and no-replay adapter classification.
The root runs these and both completed reference modes after edits; readiness
or a discovery pass is not production verification.

`timeout 900s make quickcheck PACKAGES=./internal/engine RUN='^TestSessionTimeoutPending' VERBOSE=0`
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
`timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$' VERBOSE=0`
and `timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$' VERBOSE=0`.
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
For proven DB/Tx plain calls, set referenceStep.nonContext=true on the existing
runner: actual Exec/Query/Prepare and returned Stmt.Exec/Query retain complete
count/error-stage/nil-output/cursor assertions. Metadata and specialized Scan
hooks inspect the actual source cursor before the truthful return preceding
Context-only recording. Keep a fresh recorded Context companion; never fabricate
its recorder key for a plain call. *sql.Conn has no plain methods: original
Conn.QueryContext and physical-owner adapters remain actual Conn routes.
Explicit QueryRow/Begin/native raw calls and SAME held objects stay explicit.
referencePrepared prepares anew; it does not prove quiet Prepare or held reuse.
Consumers such as deleteReferenceConsume do not invoke step metadata/Scan
callbacks, so preserve those original checks at the callsite. Cleanup follows
rows, statements, rollback, borrowed connections, then fixture/schema/role reset;
worker joins and registered failure exits are part of the assertion union.



### Common helper reference

These helpers are package-local and directly available to `reference_test` files.
No additional helper imports or fixture setup are needed.

| Helper | Contract and use |
| --- | --- |
| `newReferenceScenario(t) *referencePair` | Required entry point for new dual-mode tests; honors `-reference` and uses a fresh native engine. Do not use legacy `newReferencePair(t)`, which always starts Docker. |
| `p.step(t, s)` | Execute and independently assert the same step on available backends, then compare observations. |
| `p.backends()` | Returns backend descriptors with `name`, `db` (`*sql.DB`), `recorder`, and `pg` (true only for PostgreSQL). Loop once over these and share the callback. |
| `p.ctx`, `p.native`, `p.nativeConnector` | Bounded scenario context; unwrapped native control pool; fresh native connector for public connector controls. `p.memory` and `p.postgres` are the wrapped pools; `p.postgres` is nil in quick mode. |
| `referenceExec(query, count, args...) referenceStep` | Exec success with exact `RowsAffected` and unsupported `LastInsertId`; DDL generally expects zero. Arguments are bound normally. Error steps require nil published results. |
| `referenceSelect(query, columns, types, rows, args...) referenceStep` | Query success with exact column names, `DatabaseTypeName` strings and normalized `[][]any` values. Supply metadata even for zero rows. |
| `referencePrepared(s) referenceStep` | Return the same step with Prepare followed by statement execution. This prepares anew per step; it does not prove held-statement reuse. |
| `referenceError(query, state, args...) referenceStep` | Exec failure with exact SQLSTATE, asserted through `SQLState() string`. Use structured diagnostics when testing message/field fidelity or failure stage. |
| `referenceErrorDiagnostic(query, want, args...) referenceStep` | Exec failure with a complete `referenceDiagnostic` expectation, including exact empty fields. |
| `referenceImplicitError(query, want, args...) referenceStep` | As above, filling both severity fields with `ERROR`. |
| `referencePosition(n) string` | Decimal diagnostic character position; zero becomes the absent-field empty string. PostgreSQL positions are character-based, not byte offsets. |
| `controlsReferenceDiagnostic(code, message) referenceDiagnostic` | Build a diagnostic with both severities `ERROR`, code and message; set any additional expected fields explicitly. |
| `referenceRunStep(t, ctx, executor, recorder, s) referenceObserved` | Run standard independent assertions on a chosen backend/session. Context steps accept DB/Conn/Tx; proven `s.nonContext=true` requires actual DB/Tx plain methods and cannot use Conn or a fake forwarding adapter. Actual cursor callbacks remain checked before returning without Context-only recording. Does not compare backends for you. |
| `referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}` | Executor wrapper for an already prepared `*sql.Stmt`, usable with `referenceRunStep`; leave `s.prepared` false, use the matching query, and close the held statement yourself. Other queries route through the embedded executor. |
| `referenceState(t, err, want) string` | Check SQLSTATE through the interface; empty `want` requires success. Useful for custom public API calls. |
| `referenceExtractDiagnostic(t, err) referenceDiagnostic` | Extract structured public native/pq diagnostics; validates diagnostic shape and excludes native claims of PostgreSQL C source locations. Useful while discovering oracle data. |
| `referenceAssertDiagnostic(t, err, &want) referenceDiagnostic` | Assert the complete structured diagnostic for custom calls. Does not accept a nil expectation. |
| `referenceNormalize(value) any` | Converts `[]byte` to string and `time.Time` to UTC without monotonic/location identity; leaves other values unchanged. Standard steps apply this automatically. |
| `noticeReferencePool(t, p, postgres, &records)` | Return a notice-enabled pool, pinned connection and recorder; append complete notice diagnostics to `records`. Select the backend using `backend.pg`; fixture registers cleanup. |
| `referenceSessionCancel(t, parent, conn, prepared, queryMode...)` | Observe real PostgreSQL pg_sleep admission, cancel through the server, join the operation and assert full57014. Optional true queryMode selects Query with nil error cursor; otherwise Exec. The caller asserts SAME-owner continuation; this is not a native precancel substitute. |
| `roleReferenceCleanup(t, names)` | Register cleanup for real PostgreSQL roles created by the test **before constructing the scenario**, so pool closure and schema reset happen before role removal. Public-schema resets do not remove cluster roles. |

Register role cleanup on the same `testing.T` that owns the scenario, before
constructing its pair. A child subtest using a parent's pair must not register
role cleanup on the child: child cleanup runs while parent-owned tables/ACLs
still exist. The parent owns role removal after its physical handles and schema
have been cleaned up; consumer subtests retain all assertions and reset identity.

### Step fields and expected data

- `referenceStep.query`, `args`, `prepared`, `selectRows`, `affected`, `state`,
  `columns`, `types` and `rows` are populated by the constructors above.
- Set `s.bag = true` for unordered results: order is ignored but duplicate
  occurrences must match. Otherwise add SQL `ORDER BY` when ordering is part of
  the expected result; never depend on traversal order.
- Set `s.checkMetadata = func(t *testing.T, rows *sql.Rows) { ... }` for complete
  raw descriptor contracts on the actual result, before iteration even when
  empty. Retain independent backend ScanType/Nullable/Length/DecimalSize flags.
- Set `s.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) { ... }` to
  rescan the current row into specialized destinations. It must not issue SQL.
  Use a shared backend callback for metadata contracts beyond database type names.
- For Query errors, start with `referenceErrorDiagnostic`, then set
  `s.selectRows = true`. Set `s.errorStage` to `"prepare"` or `"execute"` when
  stage matters; this field is checked only with a structured diagnostic.
- `s.postgresDiagnostic` permits an independently specified PostgreSQL diagnostic
  for intentional differences. Both full records are asserted first; only
  Position/Context are normalized for subsequent parity. This is not permission
  to suppress unrelated differences.
- `referenceDiagnostic` fields are `Severity`, `SeverityUnlocalized`, `Code`,
  `Message`, `Detail`, `Hint`, `Position`, `InternalPosition`, `InternalQuery`,
  `Context`, `Schema`, `Table`, `Column`, `DataType`, `Constraint`. Unspecified
  fields must actually be empty; source File/Line/Routine are not reference data.
- Integer cells normally use `int64`, floating cells `float64`, Boolean cells
  `bool`, NULL `nil`; normalized byte payloads use strings. Do not confuse
  `ColumnType.ScanType` with the actual raw scanned value. Temporal/OID and other
  codec differences can require independently captured backend expectations;
  branch on `backend.pg` for those expectations, not to duplicate the scenario.

## Self-contained example

Place this untagged file in `reference/feature_answer_test.go`. The literal
expectations illustrate the final recorded form; when adding real functionality,
validate its own expectations against the pinned PostgreSQL run first.

```go
package reference_test

import "testing"

func TestReferenceFeatureAnswer(t *testing.T) {
    p := newReferenceScenario(t)
    for _, backend := range p.backends() {
        t.Run(backend.name, func(t *testing.T) {
            // During oracle discovery only, temporarily select backend.pg
            // before this callback. The completed test runs every backend.
            conn, err := backend.db.Conn(p.ctx)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() {
                if err := conn.Close(); err != nil {
                    t.Error(err)
                }
            })
            run := func(s referenceStep) {
                referenceRunStep(t, p.ctx, conn, backend.recorder, s)
            }

            run(referenceExec("CREATE TABLE answer (v INTEGER)", 0))
            run(referenceExec("INSERT INTO answer VALUES (41), (NULL)", 2))
            for _, prepared := range []bool{false, true} {
                s := referenceSelect(
                    "SELECT v + $1::integer AS result FROM answer ORDER BY v NULLS LAST",
                    []string{"result"}, []string{"INT4"},
                    [][]any{{int64(42)}, {nil}}, int64(1),
                )
                if prepared {
                    s = referencePrepared(s)
                }
                run(s)
            }
            run(referenceError("SELECT absent FROM answer", "42703"))
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

Run `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$' VERBOSE=1` first, then
`make quickcheck PACKAGES=./reference RUN='^TestReferenceFeatureAnswer$'`. After any
test or implementation changes, rerun both selectively before review, then run
ordinary quickcheck and the post-integration fullcheck.

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

- `reference/fixture_test.go`: step runner, diagnostic, notice and control helpers.
- `reference/fixture_lifecycle_test.go`: mode selection, backend descriptors and
  container lifecycle.
- `Makefile`: non-standard command details.

These are last-resort implementation references, not prerequisites for each test.

## Subquery regression and instrumentation coverage

`reference/query_subquery_*_test.go` contains the shared direct/prepared,
held-handle, mutation, metadata, admission, locking and stored-view scenarios.
Both backends execute the same callback without a discovery-only backend filter:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySubquery'
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
`make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestQuery(Subquery|DerivedSubquerySyntax)'`.


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
make quickcheck PACKAGES=./reference RUN='^TestReferencePGCast' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGCast' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferencePGCastFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferencePGCastFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestCastCatalog' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestPGCast' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)' VERBOSE=0
```

Every quickcheck invocation has a900-second deadline. Private units exercise
owner planning/application in all claimed and excluded contexts and projection
ownership/cancellation/generation retention, without issuing SQL. Selective
runs and smoke do not replace ordinary quickcheck or the post-merge fullcheck
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
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericSum|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects|UpdateReturning|GeneratedStoredDefinitionDiagnostics|ExpressionIndexDiagnostics|NumericAvg|IntervalAvgDiagnostics|DatetimeExtractStoredConsumers|DatetimeExtractFeatureSmoke)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericSumSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericSumSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSerializableWriteSkew$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSerializableWriteSkew$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericSumReview' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericSumReview' VERBOSE=0
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
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(NumericSum|NumericAvg)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^Test(Sum|NumericSum|NumericAvg|Avg|UpdateReturningMalformedSumShape)' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^Test(Sum|NumericSum|UpdateReturningAggregate)' VERBOSE=0
make quickcheck VERBOSE=0
```

Focused smoke expects direct/prepared six outputs4, promoted nullable metadata,
typed empty/all-NULL results, exact INT8 max-pair NUMERIC, decimal4.656/0.0000,
ordered REAL0, negative zero, held parameter recovery, full diagnostics/no
partial effects, global INSERT affected1 versus empty keyed0, and SUM view
control replay with retained metadata. Root runs selective checks under separate
900-second deadlines after generation/formatting, and fullcheck only after merge
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericArithmeticFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericArithmeticFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(NumericArithmetic|NumericPower|NumericAvg|MathScalar)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestExpressionNumericArithmetic' VERBOSE=0
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleMembership' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleMembership' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleMembershipFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleMembershipFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^Test(Membership|Role|Authorization|SessionIdentity|ViewReplace)' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleMembershipBatch' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleMembershipBatch' VERBOSE=0
```
The full-role consumer selector must also run in both reference modes:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReference(Role|SessionIdentity|ViewReplace)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Role|SessionIdentity|ViewReplace)' VERBOSE=0
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

Each quickcheck invocation has a900second deadline:

```sh
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAdvisorySessionCheckConsumers' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceAdvisorySession' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAdvisorySession' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceAdvisorySessionReview' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAdvisorySessionReview' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCheck(VolatileEffects|ScalarConstants|CompleteRowAndAtomicity|CorePreparedFrames|Controls|ExplicitTransactionAndSavepoint|PreparedExecutionOutputEnvironment|AlterCandidates|AlterCandidateTransactions|ReviewReplayHistory|ReviewReplayOwnedSequence|Review2OrderLogicalRoundtrip)' VERBOSE=0
make quickcheck PACKAGES='./internal/engine ./internal/parser' RUN='^TestAdvisorySession' VERBOSE=0
make quickcheck RACE=1 PACKAGES='./internal/engine ./internal/parser' RUN='^Test(AdvisorySession|RelationLock|RelationDeadlock)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceAdvisorySessionFeatureSmoke' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAdvisorySessionFeatureSmoke' VERBOSE=0
```

The feature smoke requires double prepared acquisition42, rollback survival,
peer exclusion until the second matching decrement, exact unmatched warning,
shared pair coexistence and bigint independence, real timeout/wake outcomes and
empty unlock-all. Native phases additionally require retained counts and data
through actual controls. Private ownership/graph/binding/parser tests supplement,
not replace, the identical public contract. Final fullcheck runs only after merge
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryAccess.*$' VERBOSE=0
# After removing the temporary filter: establish native failing proof.
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryAccess.*$' VERBOSE=0
# After implementation: both completed shared definitions and reverse regression union.
make quickcheck PACKAGES=./reference RUN='^TestReference(BinaryAccess|StringPad).*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(BinaryAccess|StringPad).*$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryAccessFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryAccessFeatureSmoke$' VERBOSE=0
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(GroupingSets.*|CountExpressionCapabilityBoundaries|QueryAggregateExclusionPrefixEffects)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$' VERBOSE=0
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
`make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^Test(GroupingSets|QueryAggregate|AggregateModifiers)' VERBOSE=0`.
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
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceGroupingSetsReview.*$' VERBOSE=0
# Both backends execute shared definitions; no temporary discovery helper.
make quickcheck PACKAGES=./reference RUN='^TestReferenceGroupingSetsReview.*$' VERBOSE=0
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
`TestSimilarExecutionReplacementOwnership` and
`TestSimilarExecutionReplacementBudget` are genuine structural/injected-limit
units; the latter cover configuration-preserving invalidation, preallocation
replacement ownership, translation/compile/runtime failure, NULL, cancellation
and recovery. They do not claim a PostgreSQL-identical resource cutoff.
`TestReferenceSimilarActualCancellation` executes the actual regex UPDATE;
server admission is observed before cancellation, and native cancellation
observes entry into translation/compiler/matcher work. It must not substitute
`pg_sleep`, a precanceled context or readiness for exercised cancellation.

Root runs both modes before review, each under the Makefile's 900-second bound:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceSimilar' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSimilar' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSimilarFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSimilarFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceSimilarReview' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSimilarReview' VERBOSE=0
make quickcheck PACKAGES='./internal/engine ./internal/sqltype' RUN='^TestSimilar(SubstringPresentationIdentity|ExecutionReplacementOwnership|ExecutionReplacementBudget)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(BinarySlice|LikeLegacy)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(BinarySlice|LikeLegacy)' VERBOSE=0
```

The BinarySlice capability selector preserves positional TEXT/two-argument
POSIX exclusions but requires all three SQL-regex substring spellings. LIKE
legacy migration retains its independent matching/metadata/replay assertion
union. Genuine parser/provider/bound-node units supplement public reference
coverage; they must not recreate SQL-through-internal-engine tests. Native
resource-accounting units use injected limits rather than claiming a portable
PostgreSQL complexity cutoff. Final fullcheck belongs to the root after merge
under one 1800-second deadline.

## Role attribute coverage and feature smoke

The `TestReferenceRoleAttributes` R1–R7 family covers CREATE/ALTER ROLE and
USER defaults/aliases, direct attribute authority, replication-target and
special-attribute matrices, diagnostics/atomicity, physical admission,
password verifier mathematics/SASLprep and expiry metadata, omission/control
retention and a composed feature smoke. Shared public membership actions show
special attributes are not inherited, transitive ADMIN works independently of
INHERIT/SET, and changing member INHERIT only changes future default edges.
Privileged PostgreSQL pg_authid/pg_roles/pg_auth_members queries supplement these
witnesses without adding native catalog projections. Internal units independently
check complete descriptors, immutable snapshots, verifier keys and lifecycle races.
`TestReferenceRoleAttributesContextualMembershipNames` additionally covers all
five contextual payload words in initial and ALTER GROUP comma-list positions.
`TestReferenceRoleAttributesSelfPasswordCancellation` exercises ordinary effective
self-PASSWORD with a valid high-iteration verifier: PostgreSQL statement timeout
reports57014, native returns its context deadline, both recover the same owner
and release the limit-one physical slot. Native units interrupt after a known
derivation step and assert unchanged roots/allocator plus RequestEnd completion.

The current `RUN='^TestReferenceRole'` selector covers every affected role
definition, including the two review regressions and trusted-connector boundary;
run it in both native and pinned modes before review. Each quickcheck has a
900-second deadline. Final fullcheck remains the post-merge gate under one
1800-second deadline.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRoleAttributes(FeatureSmoke|ContextualMembershipNames|SelfPasswordCancellation)$' VERBOSE=0
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

Run each quickcheck separately under a 900-second deadline:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertConflict' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertConflict' VERBOSE=0
make quickcheck PACKAGES='./internal/parser ./internal/engine ./internal/sqltype' RUN='^TestInsertConflict' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestInsertConflict(Predicate|IndexPositionHints|AuthorizationAfterWait)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestInsertConflictAuthorizationAfterWait$' RACE=1 VERBOSE=0
make quickcheck PACKAGES=./internal/engine TEST_FLAGS='-count=1 -timeout=120s -run NoTestMatches -bench BenchmarkInsertConflictCommittedOmissions -benchmem' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertConflictFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertConflictFeatureSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertConflict(Contention|ConcurrentSameKey|SnapshotVisibility|ReadCommittedInvisibleConflict|Cancellation|OmittedReservations|ConcurrentProviderPhases|CanceledProvider)$' RACE=1 VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertConflict(Contention|ConcurrentSameKey|SnapshotVisibility|ReadCommittedInvisibleConflict|Cancellation|OmittedReservations|ConcurrentProviderPhases|CanceledProvider)$' RACE=1 VERBOSE=0
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
not a brittle timing threshold. Its make TEST_FLAGS override retains the harness
and gives the benchmark a120-second internal timeout.
Passing native alone does not validate the changed reference definitions.
Ordinary quickcheck precedes review; final fullcheck runs after integration under
one 1800-second deadline.

## DISTINCT ON reference contracts and feature smoke

The untagged `reference/query_distinct_on_*_test.go` family shares fixture/public
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
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryDistinctOn' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryDistinctOn' VERBOSE=0
timeout 900s make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$' VERBOSE=0
timeout 900s make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$' VERBOSE=0
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
make quickcheck PACKAGES=./reference RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(TransactionControlsSmoke|TransactionSmoke|TransactionBeginContextCancellation|ControlTransactionsSmoke|AlterNullability(Smoke|ControlsSmoke)|AlterDropColumn(Smoke|ZeroColumnControlsSmoke))$' VERBOSE=0
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
make quickcheck RACE=1 PACKAGES=./reference RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$' VERBOSE=0
make quickcheck RACE=1 REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTransaction(Smoke|BeginContextCancellation)$' VERBOSE=0
```

