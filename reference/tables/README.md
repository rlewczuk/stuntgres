# reference/tables

Table definitions and relationships: CREATE TABLE core, relation storage
options, CHECK and NOT NULL constraints, foreign keys and referential actions,
and foreign tables (SQL/MED metadata, scans, writes, IMPORT FOREIGN SCHEMA, user
mappings). Related families: inheritance, typed tables (`OF type`) and LIKE in
[reference/inheritance](../inheritance/README.md); partitioning in
[reference/partitions](../partitions/README.md); temporary tables, defaults,
identity and generated columns in `reference/sequences`; ALTER TABLE actions in
`reference/alter`; indexes and non-partitioned exclusion constraints in
`reference/indexes`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
The rules below also apply to `reference/inheritance` and `reference/partitions`.

- Scenario helpers: `relationOptionsScenario` (`relation_options_helpers_test.go`).
- Public mapping-view scenarios reuse `fdwScenario` with handlerless metadata and
  `pg_user_mappings_helpers_test.go` for direct/fresh/held queries, independently
  specified masks and relational OID checks. They retain every mapping identity
  while checking whole-array NULL disclosure under current authority, populated
  and empty metadata, lifecycle diagnostics and explicit native-control phases;
  no foreign connection is implied.
- Foreign tables use no canned rows. Natively, `newFDWSQLReader`
  (`fdw_reader_test.go`) serves an independent in-process stuntgres database
  (DSN `fdw-independent/<test>`) through wrappers registered with
  `fixture.NewScenarioWithDriverConfig`; on PostgreSQL the same SQL runs against a
  `postgres_fdw` loopback server (`127.0.0.1:5432`, database
  `stuntgres_reference`), whose user mappings need `password_required 'false'`.
  Use `fdwScenario`/`fdwWriteScenario`/`fdwConfiguredScenario`
  (`fdw_fixture_test.go`) and `fdwHeld` (`fdw_lifecycle_helpers_test.go`).
  `fdwCleanup` runs on the case's own connection under a fresh 15-second
  context (ROLLBACK, RESET ROLE, then drops), so it still works after the case
  context is canceled.
  Typed query scenarios (`fdw_pushdown*_test.go`) deparse the provider's bound
  occurrence/slot tree into genuine SQL against these independent sources.
  Their quoted source identifiers differ from foreign names. Direct/prepared
  public results are independent expectations; selected-stage observations only
  supplement them. Absent/declined paths and unsafe semantic frontiers retain
  real base scans. Source cursor resources and fault/cancellation hooks reuse
  `fdwSQLScan`, rather than a canned composite-row implementation.
  `TestReferenceStatDatabaseForeignWorkers` reuses this real independent SQL
  provider and its production partition callbacks. It separately asserts admitted
  requested maximum versus two actual partition starts, plus Begin/Partition
  failure before launch and Iterate failure after launches. Retiring the actual
  writer publishes events to a none-mode observer. PostgreSQL expectations come
  from that backend's own executed EXPLAIN ANALYZE worker facts, not native
  goroutine/configuration parity. Run
  `make check RUN='^TestReferenceStatDatabaseForeignWorkers$'` in native and
  `REFERENCE=1` modes.
  JSON plans use the same selected composite/fallback witnesses, preserving
  provider property order/duplicates and actual selected-node measurements.
  Canceled direct/prepared INSERT SELECT consumes an admitted composite source,
  leaves its local target sentinel unchanged and publishes no affected result.
  Provider-level changed-parameter ReScan reopens a genuine source SQL cursor;
  owning `exec/enginetests` separately exercises engine Stage.Rewind and its
  current-context parameter export, selection/cache reset and error ownership.
  Async scenarios (`fdw_async_*_test.go`) use a test-only HTTP provider that
  advances independent real source SQL cursors. PostgreSQL uses two loopback
  servers and an advisory-lock gate in a volatile fetched target; admission
  observes blocked slow FETCH and completed ready FETCH, never cursor creation.
  Native HTTP response delivery is gated after selecting a real source row.
  Every gated worker is released/canceled/joined on exit, and cancellation must
  join before the external gate is released.
  Repeated-invocation scenarios release the external gate and inspect live
  resource high-water bounds, distinguishing optional async requests from
  synchronous HTTP reads and live cursors from cumulative ReScan generations.
- Genuine external PostgreSQL ownership uses
  `external_sql_provider_*_test.go`, not the in-process reader or HTTP source.
  Contract/registration phases are Docker-free; physical phases run only with
  `REFERENCE=1` and use the pinned fixture for both native GO-handler and stock
  postgres_fdw consumers. Every native scan opens through its injected execution
  capability and genuine transaction participant. Caller/mapping credentials,
  actual PID admission/exit, owned text descriptors/results/errors, cancellation,
  remote notifications and native destination prerequisites have independent
  assertions. Close joins workers before fixture/schema/role cleanup.
  The one genuine adapter now lives in `reference/fixture`, shared with dblink;
  table scenarios retain their prior ownership/results/fault assertions through
  its public snapshots and controlled-fault API. Credential mechanism reports
  actual successful observed authentication, not the former constant `unknown`.
  The observer retains no credential payload or trace. All callers distinguish
  `ConninfoPresent=true` (even empty) from structured options; no explicit empty
  request falls back to the fixture DSN. Host establishment markers preserve
  ordinary errors.Is/As and do not acquire SQLState at the raw provider boundary.
  The pgconn adapter copies each live result descriptor before row iteration or
  reader close, and copies each text/NULL cell before advancing; `ReadAll` in
  the pinned pgconn version omits descriptors for empty tuples. Physical/smoke
  witnesses retain both full and empty four-column results plus an empty,
  differently named varchar(7) result across later activity. Assertions inside
  active GO callbacks return errors to unwind the SQL request; never call
  `testing.T.Fatal`/`FailNow` there or capture a parent T for callback failure.
  Raw bytea cells remain PostgreSQL output text; the genuine fixture handler
  decodes them once through pgx's ByteaCodec when exporting ForeignRow driver
  values, keeping NULL distinct from non-NULL empty bytes.
  Selected-database retirement uses both the real postgres_fdw cache and native
  capability-opened handles. Refused drops preserve resources; successful FORCE
  joins late Open and actual Close before completion, without blocking peer
  metadata or independent-Driver owners. These callback timing gates are
  native-only host phases, not PostgreSQL parity claims; neither old handles nor
  old capabilities gain authority over a same-name replacement database.
  The shared Contracts phase also preserves unqualified BYTEA/custom/array column
  OPTIONS, NOT NULL ordering and quoted/unreserved names through CREATE, ADD and
  native dump replay; ordinary-table column OPTIONS specifies the released
  semantic 42809 kind rejection, not guessed parser syntax failure. Malformed
  type tails retain their separate full syntax diagnostics.
  CREATE/ALTER kind failures must occur at execution, including prepared calls,
  and leave no created relation, added column or changed sentinel. Multi-action
  failure rolls back the earlier ADD. Ordinary ALTER of a genuine foreign table
  applies new-column options, but skipped ADD IF NOT EXISTS leaves existing
  options unchanged despite the written extra option; ordinary targets skip
  their unused column option clause as well.
  Focused `TestReferenceExternalSQLProviderColumnOptionsDropAddOrdinary`,
  `TestReferenceExternalSQLProviderColumnOptionsDropAddForeign` and
  `TestReferenceExternalSQLProviderColumnOptionsSkippedAdd` distinguish actual
  phased DROP/re-add admission from unchanged-column skips, direct/prepared and
  through both foreign ALTER spellings. They check full execution diagnostics,
  original ordinary rows/schema, replacement options and real public SQL
  validator rejection with atomic original-schema preservation.
- Native-control phases replay their actual Dump output, unmodified, on
  PostgreSQL. Roles named by the dump must already exist there: Dump serializes
  their object/schema ACLs, not role definitions or memberships. Register
  same-session `RESET ROLE`, `RESET SESSION AUTHORIZATION` and `RESET search_path`
  cleanup before replaying.
  Wrapper/server USAGE and foreign relation/column ACLs roundtrip as exact named
  raw ACL repairs with independent inquiry outcomes. Capture invokes no provider
  callbacks and replacement preserves the real catalog graph. Routine handler
  inquiries read the actual EXECUTE ACL even when the handler is metadata-only.
- BPCHAR cells scan as strings on both clients, but `ScanType` is `string`
  natively and `interface{}` on pq.
- Compare catalog OIDs relationally (joins, captured values); allocator numbers
  can repeat across Reset/Restore.
- On PostgreSQL the rebuilt `public` grants no USAGE to PUBLIC: grant it to
  restricted actors before asserting table- or column-level denial.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- CREATE TABLE core: `RUN='^TestReferenceTableCore'`
- Storage options: `RUN='^TestReferenceRelationOptions'`
- CHECK constraints: `RUN='^TestReference(Smoke)?Check'`
  `TestReferenceCheckTableoidBoundary` retains PostgreSQL's positive CHECK
  witness and the native direct/prepared rejection with table/constraint
  publication guards; ordinary query `tableoid` remains shared-positive.
- NOT NULL constraints: `RUN='^TestReference(Smoke)?(Drop)?NotNull'`
- Foreign keys (also selects alter/dml callers): `RUN='^TestReferenceForeignKey'`
- Referential actions: `RUN='^TestReferenceForeignActions'`
- Constraint timing (including nested utilities and procedural transaction lifetime), FK completion and native controls:
  `RUN='^TestReferenceDeferred(Constraint|ForeignKey)'`
- BEFORE statement foreign-operation boundaries (shared selector with the view
  scenarios in `reference/operators`):
  `RUN='^TestReferenceTriggerBeforeStatementViewForeign$'`.
- Foreign tables: `RUN='^TestReferenceFDW'`
  Analyzed foreign writes and cancellation reuse the same provider and lifecycle
  helpers, selected with `RUN='^TestReferenceExplainAnalyzeForeign'`; they assert
  emitted output separately from affected rows, real source state and rollback.
  `TestReferenceExplainAnalyzeForeignSelections` distinguishes actual selected
  composite callbacks from base callbacks (`Query == nil`) and records base
  Begin calls in both reader and transaction-borrowing writer implementations.
  Direct and held invocations retain exact selected plans/current parameter
  frames; declined composites must execute genuine base selections.
  `TestReferenceExplainAnalyzeForeignParameters` starts fresh direct/held
  scenarios for both RETURNING modes and checks repeated UPDATE/DELETE effects.
  Held statements close even on assertion failure; provider observations are
  copied before assertions so cleanup never runs under their recording mutex.
  `TestReferenceFDWRoutineSourceCompaction` exercises six real foreign-target
  INSERT/UPDATE/DELETE source forms with direct/held routine calls, exact rows,
  dependency bags, RESTRICT, remote/local rollback and native controls.
  `TestReferenceFDWRoutineInputCompaction` drops only local FOREIGN TABLE metadata
  over unchanged real remote rows, preserving literal 7 through the frozen input
  bridge, genuine scan lifecycle and later progress. Both use real native SQL
  handlers and PostgreSQL `postgres_fdw`; select each exact name in native and
  `REFERENCE=1` modes. Registered-handler controls remain native-only, never
  translated into synthetic PostgreSQL replay.
  `TestReferenceFDWConversion` retains ordinary conversion cases and composes a
  frozen routine's original aliases with local-only foreign-column compaction.
  Direct/preheld calls retain 7; invalid remote text has two-line Context
  `column "picked" of foreign table "f"` followed by
  `SQL function "fdw_alias_conversion_run" statement 1`. Restore/progress checks
  retain the unchanged three-column remote row and balanced actual scan ownership.
  `TestReferenceFDWRowLockFailureAtomicity` checks complete `22P02` diagnostics
  for invalid integer refetch import (empty Context, unlike ordinary scan import),
  retaining real source/local rollback and suffix-lock reacquisition witnesses.
  `TestReferenceFDWRowLockRecheck/changed-projection` covers genuine postcapture
  source mutation through inner/outer marked derived queries and stored views,
  direct and twice on one prepared statement. Native late refetch returns the
  changed qualifying row with exactly one volatile invocation; a nontransactional
  sequence also detects duplicate evaluations surviving rollback. PostgreSQL's
  independently established early-lock RR snapshot instead raises `40001`
  before projection, with exact remote Context and zero sequence invocations.
- External SQL provider family: `RUN='^(TestReferenceExternalSQLProvider|TestExternalSQLProvider)'`.
  Physical retirement selects
  `RUN='^TestReferenceExternalSQLProviderDatabaseDrop$'` in both modes, adding
  `REFERENCE=1 RACE=1` for the actual host-callback ownership race.
  Shared dblink/FDW active-slot refusal selects
  `RUN='^TestReferenceDatabaseDropResourceSlots$'` in both modes, in the existing
  logical-WAL owner package rather than changing table-provider startup.
- Stock postgres_fdw provider family: `RUN='^TestReferencePostgresFDW'`.
- External column-option DROP/re-add regressions:
  `RUN='^TestReferenceExternalSQLProviderColumnOptions(DropAddOrdinary|DropAddForeign|SkippedAdd)$'`.
- Async foreign scans: `RUN='^TestReferenceFDWAsync'`
- Foreign explain and sampled analysis: `RUN='^TestReferenceFDW(Explain|Analyze)'`.
  Live ANALYZE ownership, imported statistics, retained inherited visits and
  provider cancellation/replacement use `RUN='^TestReferenceFDWAnalyzeProgress'`.
  The real SQL reader uses reservoir sampling with a separately counted source
  population. Its explicit negotiation `TotalPages` is provider-owned logical
  metadata (normally one segment, configurable for allocation scenarios), not a
  mapped source query or a physical-page claim. Mapping-dependent SQL and import
  happen only during acquisition. Zero negotiation weight is meaningful for
  inherited visits; unchanged direct foreign ANALYZE still acquires at zero.
  Direct supported targets build noninherited joint and expression definitions
  from the same once-imported real sample as their scalar statistics. The reusable
  direct family selector is `RUN='^TestReferenceFDWAnalyzeDirect'`.
  Its committed1000-row `a=b=i%3` source yields joint3, perfect dependencies and
  typed MCV masses .334/.333/.333. Distinguishing source mutations exercise joint6,
  scalar staleness/refresh, selected or disabled missing-key WARNING42P17,
  object-target0 and all-disabled retention, and empty-input retention.
  Expression inputs include unselected constrained-domain columns; genuine SQL
  sequence effects distinguish input import from expression evaluation, including
  NULLs. Domain import advances1000 in both backends, native expression work1000.
  The deliberately effectful IMMUTABLE wrapper is instrumentation, not a portable
  SQL side-effect promise: [released PostgreSQL's builder](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/statistics/extended_stats.c)
  evaluates in both `make_build_data` and `compute_expr_stats`, advancing2000.
  A searched CASE with distinct result branches demands the sequence before
  projecting its argument, including NULL; assertions name both phase and sequence.
  SQL-body sequence references are schema-qualified for the restricted search_path.
  Domain and expression errors retain prior products, while
  rollback/savepoint/commit separate transactional typed publication from completed
  existing-object scan population. Registered provider cancellation/failure gates
  are explicit native-only controls; the oracle uses stock loopback postgres_fdw.
  Ordinary and partitioned parents acquire actual supported foreign descendants;
  supported foreign roots retain their direct local pass and also acquire their
  inherited population. Child descriptors project into parent order, including
  an existing extra/reordered foreign descriptor attached by ALTER INHERIT.
  ONLY suppresses separate descendant refresh, not inherited acquisition.
  `make check RUN='^TestReferenceFDWAnalyze(Inherited|Partition|ForeignRoot)$'`
  covers genuine local10/foreign30, ordinary local10 versus inherited40,
  partition reltuples40 without local monitoring tuples, foreign-root local30,
  untouched child estimates before recursive refresh, exact ordered results,
  selected-column staleness, NULL/empty retention and immutable held output.
  The partition empty-input phase really truncates its local leaf on both
  backends, then analyzes empty input, checks zero actual rows and retained
  compatible parent distributions, and repopulates before singleton import.
  Successful ordinary/foreign targets report one local count/time event, not two;
  partition roots report one inherited event. Foreign count/time getters are
  exercised on PostgreSQL only; internal native canonical-ownership tests cover
  that field because the released table-stat view excludes foreign relations.
  `make check RUN='^TestReferenceFDWAnalyzeMixedSampling$'` distinguishes native
  K300/weights8:24/allocation75:225 from a biased full-native-plus-foreign cap.
  Literal accepted-record counts are exact; inherited population, scalar and
  extended estimates describe that genuine bounded sample and can be fractional
  or incomplete after zero-work visits. Underfill, rounded-zero/no-mapping,
  fractional4000.5 and direct-zero acquisition are explicit native controls,
  not assertions of PostgreSQL's physical/random sample allocation.
  `make check RUN='^TestReferenceFDWAnalyzeInheritedExtended$'` uses shared
  genuine scalar/joint/expression input, target2 K600, actual joint2 changed to4,
  disabled required-key WARNING42P17 with compatible complete payload retained2.
  Missing-key42P17 precedes the object's own target0 skip: both target2 and
  target0 retain the complete old payload without raising the mixed budget.
  The native fixture reservoir uses a per-acquisition seeded generator for
  reproducible genuine-record joint-group coverage; ineligible samples need not
  contain every group to prove skip-and-retain behavior.
  Reenabled-key collection refreshes4. Append-only mutation grows native1000/foreign3000
  to1024/3072 while retaining weights8:24 and the 1:3 population ratio; it needs
  no native partition DELETE/UPDATE support. With every scalar disabled, actual
  remote expression3 becomes4 while the complete old3/2 array stays retained;
  a genuinely enabled selected label refreshes4/2 at frequencies.75/.25.
  All-native eligibility has its own
  `TestReferenceStatisticsAnalyzeEligibility` local/inherited/partition coverage.
  `make check RUN='^TestReferenceFDWAnalyzeInheritedExpressionSnapshot$'` separates
  actual records from estimated expression distinctness. Ten native1s plus990
  distinct remote values produce a genuine controlled native40-record sample
  (d31, thirty singletons, acquired population1000): the estimator rounds
  `40*31/(10+30*40/1000)` to111, encoding-.111 rather than raw sample-.775.
  PostgreSQL independently collects this complete1000-row fixture and expects
  D991/N1000=-.991; its physical sample is not the native underfill control.
  The explicitly native-only public Dump/Restore phase uses a separate registered
  destination provider, retaining-.111, actual MCV counts/frequencies and all
  thirty genuine histogram values after local/external source mutations.
  Import and statistics reads must not negotiate, query or reacquire foreign
  samples; actual later queries use destination-owned callbacks/current data.
  Typed semantic snapshots do not transport canonical inherited population,
  scan state, analyze count or last-analyze time. Run native and `REFERENCE=1`.
  `make check RUN='^TestReferenceFDWAnalyzeInherited(Lifecycle|Cancellation|Transactions)$'`
  checks ordinary local30/count+1 surviving later inherited42704 or actual child
  lock cancellation with old inherited40/typed backing, partition failure with
  no completion, and foreign local50 surviving later inherited failure. Repaired
  success publishes inherited60 once. Transaction rollback/savepoint/commit keep
  typed publication separate from completed existing-object maintenance; genuine
  new private targets leave no ghost. PostgreSQL's actual postgres_fdw two-phase
  refusal is checked separately from native prepared completion, not hidden.
  Transaction mutations use UPDATE ONLY on the intended local10 records.
  Partition mapping setup changes the real foreign leaf owner while detached,
  then reattaches the same bounds before baseline collection. Partition child-lock
  cancellation uses a real leaf TRUNCATE inside the holder transaction and rolls
  it back, preserving both actual blocking and records without new LOCK TABLE
  partition-hierarchy support. These setup choices are shared by both backends.
  `make check RUN='^TestReferenceFDWAnalyzeInheritedDomain$'` uses a real positive
  integer domain on an ordinary parent and its supported foreign child, over
  independently queried remote base-integer records. Direct/prepared collection
  derives genuine typed domain scalar/joint/expression facts and ordered query
  rows; planner witnesses use supported plain-text label predicates, not a new
  domain-literal estimator. Actual invalid remote values produce exact23514 with
  common message and Schema/DataType/Constraint fields. A real restricted caller
  search_path plus qualified ANALYZE requires the common domain name
  `public.fdw_analyze_positive`, without backend-specific message goldens.
  Only PostgreSQL adds postgres_fdw's
  `column "id" of foreign table "fdw_analyze_foreign"` conversion Context; native
  typed Go-provider utility import asserts exact empty Context, not omitted fields.
  After that inherited import failure, completed local30/count+1/time survives
  with old inherited40 and complete old local/inherited joint/expression payloads.
  Restoring remote values and retrying collects inherited60, joint3 and actual
  expression values5/4/2 without independently publishing the child. Run both modes.
  `make check RUN='^TestReferenceFDWAnalyzeInherited(Eligibility|Authority)$'`
  covers child-owner exact/PUBLIC mapping rather than the MAINTAIN actor,
  foreign-node diamond traversal, real admission diagnostics, and explicit native
  capability/provider/import/authority/cancel/Reset/Restore gates with released
  source resources. No internal packages or invented statistics reader are used.
  Every changed public definition requires both native and `REFERENCE=1` modes.
  PostgreSQL runs genuine loopback postgres_fdw sampling; native EXPLAIN and
  PostgreSQL parent `pg_stats` observations have independently stated expectations.
  Existing direct regression selectors remain
  `RUN='^TestReferenceFDW(AnalyzeDistributions|ExplainAnalyzePermissions|ExplainAnalyzeLifecycle)$'`;
  preserve their exact distributions, selected facts and lifecycle assertions.
  Explain plans share execution's actual non-first minimum path. Modification
  explain gates also cover provider errors, cancellation and authority replacement
  without writing startup. Foreign statistics options exercise default/-1, exact
  clamp warnings and atomic invalid-option rejection; unsupported negotiation
  preserves precedence through direct/fresh/held routes.
  `TestReferenceFDWExplainCompositePlans` enables the real join/upper provider,
  ties execution to explained stages and distinguishes local residual/declined/
  incompatible fallback. `TestReferenceFDWExplainRowMarkPlans` checks projected
  full-tuple/identity planning and COPY/REFERENCE sibling occurrences against
  real source revisions and independent tuple-lock witnesses. Detached selection
  and explanation gates live in `TestReferenceFDWExplainPlanningLifecycle`.
  `TestReferenceFDWExplainIntegratedFeatureSmoke` combines both genuine fixture
  families. Select each changed definition in native and `REFERENCE=1` modes.
  CompositePlans and integrated smoke also distinguish genuine count(*) upper
  selection from scalar/derived contextual exclusion and declined presence-only
  base scans, with exact empty attribute demand/Unused slots and actual count5.
  Stored and materialized-CTE producer contexts are independent of scalar readers;
  folded CTEs preserve ordinary derived exclusions. Lifecycle gates normalized
  base and count-upper properties after production, retaining final-authority and
  nonmutation witnesses.
- Executing foreign plans: `RUN='^TestReferenceExplainAnalyzeForeign'`.
  ForeignSelections holds the same prepared SELECT across arguments1,2,99,1,
  with independent exact base/chosen-composite/declined-fallback results, source
  invariance, actual executed current-argument requests and released resources.
  ForeignParameters checks held parameterized UPDATE/DELETE effects and emitted
  RETURNING counts; ForeignMutations retains explicit rollback/autocommit proof.
  ForeignCancellation reuses the genuine post-fetch/advisory cancellation owners.
  Select every changed family in both native and `REFERENCE=1` modes.
- Stock file configuration: `RUN='^TestReferenceFileFDW'`. Shared direct/prepared
  scenarios assert configuration, authority, complete diagnostics and installed
  identity; native-only phases exercise logical controls and the explicit
  external filesystem/program boundary without opening a source. Controls retain
  sparse attnums/options and trailing highwater through repeated/fresh-destination
  replay, including source-shaped/staged mixed inheritance and selected inherited
  force options on compact/separately attached foreign partitions, including
  genuine direct/prepared option ALTER and wrong-kind rejection. Malformed or
  late completion cannot publish a staged prefix. Atomicity retains parsed
  duplicate-column CREATE's exact42701 and absence of relation/rowtype metadata.
  Parent data is read with ONLY; no foreign rows are requested.
- `pg_user_mapping` and `pg_user_mappings`: `RUN='^TestReferencePGUserMapping'`

Also in this package: `make check RUN='^TestReferenceSeriesCheckRestrictions$'`
and the migrated smoke tests `make check RUN='^TestReferenceSmoke(Check|NotNull|Drop)'`.
Engine and parser units: `make check RUN='^Test(RelationOptions|ForeignKey|ForeignActions|FDW)'`.
