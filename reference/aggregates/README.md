# reference/aggregates

Aggregate families: `CREATE AGGREGATE`; aggregate modifiers (DISTINCT, local
ORDER BY, FILTER); `any_value`; `min`/`max`; GROUPING SETS; `num_nulls`/`num_nonnulls`;
variance, standard deviation and regression aggregates; hypothetical-set and
ordered-set aggregates; SUM over MONEY and INTERVAL; JSON/JSONB object and array
aggregates and SQL/JSON `JSON_OBJECTAGG`/`JSON_ARRAYAGG`; bitwise and range
aggregates. Related families elsewhere: numeric SUM/AVG in `reference/numeric`,
INTERVAL AVG in `reference/temporal_types`, `array_agg` in
[cursors](../cursors/README.md#testing) and `intagg` in
[binary_json_arrays](../binary_json_arrays/README.md#testing), `string_agg` in `reference/strings`, Boolean
aggregates in `reference/types`, multiranges in `reference/ranges_text_search`,
count expressions in
`reference/query_order`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- XML aggregates: `RUN='^TestReferenceXMLAgg'`. The family-local helper uses
  pinned sessions and direct/prepared fixture steps. Ordered results have exact
  spelling; unordered distinct fragments are checked as multisets with duplicate
  counts, never as a portable serialized sequence. Lifecycle cases retain actual
  rows and held statements across input changes and native logical replay.
  Pre-canceled direct/held admission checks are native-only and require ordinary
  context errors with no published rows; healthy held reuse remains shared.
  Deterministic cancellation during actual XML body copying is covered by the
  private XML accumulator tests.

- **Helpers.** Per-family `*_helpers_test.go` files (`aggregate_modifiers`,
  `bitwise_aggregate`, `create_aggregate_remap`, `hypothetical`,
  `json_array_aggregate`, `json_object_aggregate`, `json_object_unique_aggregate`,
  `money_interval_sum`, `ordered_set`, `ordered_set_cancel`, `range_aggregate`,
  `sqljson_object_aggregate`).
- **Worker profiles.** The shared combine scenario requests cheap costs only
  for native local workers. PostgreSQL keeps its ordinary planner defaults;
  a scenario requiring real PostgreSQL workers supplies its own cost profile
  and checks its physical plan. Do not force PostgreSQL workers globally:
  restricted support may then run inside a parallel operation rather than the
  ordinary serial operation whose effects the safety case asserts.
- **Catalog probes.** CREATE AGGREGATE lifecycle checks select the owning
  procedure in a filtered derived source before catalog joins, so they never
  join complete procedure inventories within the 30-second scenario.
- **Logical authority.** Routine and aggregate native-control phases retain
  explicit raw EXECUTE ACLs as well as definitions, support edges, owners and
  data. Grantor/options and absent-versus-empty state are not reconstructed from
  effective rights; actual emitted SQL remains the PostgreSQL replay input.
  Both the aggregate/support-function and final-function explicit-ACL streams
  are replayed on PostgreSQL, with the same authority and complete graph/data
  expectations as native replay, not only the earlier default-ACL streams.
- **Floating results.** Results that depend on accumulation order use named SQL
  interval witnesses such as `abs(actual-expected) <= 1e-12*max(1,abs(expected))`;
  every other cell, count, metadata and diagnostic stays exact. Do not add a
  global approximate comparator.
- **Carrier boundaries.** Ordered-set witnesses assert executable internal
  `"char"[]`, REGCONFIG[] and REGDICTIONARY[] results independently on both
  backends, while retaining separate ordering-admission and unavailable-array
  companion diagnostics for excluded carriers.
- **Late-group atomicity.** To prove a later group's failure publishes nothing on
  PostgreSQL, force a blocking sort (ORDER BY the aggregate result, then the key)
  so no cursor rows are sent before the error.
- **Ordered-set active cancellation** (`TestReferenceOrderedSetAggregateActiveCancellation`,
  `ordered_set_cancel_helpers_test.go`):
  - Each direct/prepared route creates its own fixture. The 30-second scenario and
    15-second admission deadlines are never refreshed within a route.
  - Native labels the owned operation with its full serial subtest name and
    requires that label and the real `exec.sortOrderedAggregate.func1`
    comparator in the same goroutine-profile record. It uses pprof's debug=1
    profile, not runtime.Stack/debug=2 all-goroutine stack dumps; profiler stack
    truncation cannot substitute an unrelated worker or a launcher-only frame.
  - Native cancellation returns nil Rows/Result/Stmt and `context.Canceled`.
    Cleanup shares one 5-second join with normal cancellation handling.
  - Recovery reuses the same held statement with a small input instead of
    repeating the large sort.
- **Other cancellation.** Hypothetical-set cancellation admits on
  `stuntgres.WorkAggregateInput` through `fixture.BinaryConversionActiveCancel`; sort
  phases are covered by private units. SQL/JSON object aggregation places a
  `pg_sleep` inside the aggregated value and uses `fixture.SleepActiveCancel`;
  transition and sort cancellation belong to the internal
  `TestSQLJSONObjectAggregateReachedCancellation` unit, not public stack sampling.
- **Frame names matched by tests.** Ordered-set's labeled native sort observer
  matches `exec.sortOrderedAggregate.func1`; update it when renaming the comparator.
- **Client metadata.** The pinned lib/pq OID map lacks some types (for example
  REGCOLLATION and XID8): assert their pq database type name separately from the
  native name.
- **Selected-call decoration syntax.** `RUN='^TestReferenceSelectedFunctionFilterSyntax$'`
  checks duplicate FILTER and FILTER after OVER before binding, including complete
  diagnostics, direct/prepared publication failures, same-owner recovery and
  unchanged sentinel data after a malformed script.

Add `REFERENCE=1` for the oracle.

- Parallel SQL invocation ownership:
  `RUN='^TestReferenceAggregateParallelOwnership$'`. A physical16384-row,
  1024-byte-padding source admits actual PostgreSQL Partial/Finalize plans for
  bigint, BOOL and numeric SQL-final variants, with SQL transition/combine and
  nested regclass/regtype/regrole spelling/cast helpers. FILTER retains four
  genuine transition inputs; bounded unfiltered NULL input is checked separately.
  Direct, fresh-prepared and same-held queries assert exact NULL/5/30,
  NULL/false/true and NULL/numeric5/numeric30 values and `v` metadata through
  ceilings0/2/7, empty/all-NULL states, grouping, live support/aggregate EXECUTE
  revocation and same-handle recovery at serial/parallel ceilings. Revocation
  targets active SFUNC, including empty-input admission; revoked unused combine
  remains a serial positive. A held generic plan may remain serial, so a separate
  EXPLAIN never implies that execution reached combine. Each variant restores
  shared support ACLs/session authorization even after a failed assertion.
  Native logical EXPLAIN does not claim physical PostgreSQL topology; composed
  `TestAggregateParallelInvocationOwnership` observes two actual production
  workers overlapping real SQL support with environment/coercion/presentation,
  private cache owners, diagnostic queues, exact scratch/definition restoration,
  cancellation/join and ordinary/parallel reuse. Run native and `REFERENCE=1`
  modes, plus the selected owner/public `RACE=1` checks. The incumbent Combine
  authority/cancellation/value/state/codec assertions and all22 reached effects
  remain separate required proofs, not replaced by this scenario.
- Parallel ACL-input diagnostic ownership:
  `RUN='^TestReferenceAggregateParallelACLNotices$'`, in native and `REFERENCE=1`
  modes, plus selected `RACE=1`. The real aclitem-state aggregate validates
  missing-grantor INITCOND once at declaration, rejects malformed seeds without
  publishing pg_proc rows, and admits actual PostgreSQL Partial/Finalize over
  the physical padded source. Explicit-grantor ACL parameters demand immutable
  root input authority without another warning. Direct/fresh/same-held serial
  and parallel reuse returns exact `=r/<bootstrap-role>` TEXT even on empty
  input; every seed warning has exact WARNING/0L000/message/provenance.
  Bags retain multiplicity from both planning and execution. PG `prepagg.c`
  inputs INITCOND once per planning pass, separately from each executor state:
  new direct/fresh serial queries therefore require two plain warnings
  (planning1 + runtime1), including empty input. A matching positive-input
  Partial/Finalize query requires three plain warnings (planning1 + leader2)
  plus zero to two exact `parallel worker` warnings; empty custom plans permit
  the finite serial/partial union. Non-ANALYZE EXPLAIN also requires three plain
  warnings, because it plans and initializes two leader states without workers.
  Native has no planning input: serial requires one, partial three, never two.
  The first five PG held executions use one custom planning pass under the
  default auto policy and retain the corresponding exact topology counts.
  Later PG held executions explicitly permit planning P=0 for cached generic
  reuse, P=1 for a custom/new generic plan, or P=2 for rejected generic followed
  by custom. Allowed pairs are serial `(P+1,0)` or partial `(P+2,W)` with W=0..2;
  retained topology is not inferred from settings or the separate EXPLAIN.
  Native held ceiling0 still requires one via the current execution cutoff.
  No cross-worker order or particular launched PG worker count is pinned.
  Separate ordinary literal and parameter routes pin exact counts, prepare/bind
  phases, position8/portal context, warning-before-error, same-handle recovery
  and client filtering/drain.
  Retain `RUN='^TestReferenceACLItemNotices$'` in both modes. The composed owner
  test separately proves six paired real initializer decodes under actual
  worker overlap, exact30 total diagnostics and no live/duplicate delivery.

- Parallel support effects: `RUN='^TestReferenceAggregateCombineParallelEffects$'`.
  Falsely SAFE transition, nested-body and modifying-CTE writes, plus leader
  combine/final writes and sequence effects, must fail with complete `25000`
  diagnostics. PostgreSQL physical cost settings and an EXPLAIN admission check
  establish a real Partial Aggregate / Finalize Aggregate plan; native
  worker/leader identity belongs to the composed
  `TestAggregateCombineParallelEffects` proof, not topology parity.
  Only transition diagnostics admit the two released PostgreSQL context forms
  (support frames alone, or those exact frames followed by `parallel worker`).
  Direct, fresh-prepared and held failures retain log/destination sentinels and
  sequence state; the same held statement recovers at ceiling zero with exact
  ordinary serial effects. Run both native and `REFERENCE=1` selected modes.
  The same selection retains all twenty-two cases: seven write/sequence cases,
  twelve session-setting mutations in transition, reached nested helpers and
  leader combine/final support, one legal read-positive case, and two nested
  zero-argument deallocation cases.
  SQL `SET`, `SET LOCAL` and `SET ... DEFAULT` fail with `25000` /
  `cannot execute SET during a parallel operation`; `RESET` and `RESET ALL`
  instead use `cannot execute RESET during a parallel operation`. The earlier
  utility-command gate precedes parameter lookup, value parsing, warnings and
  installation. The existing reached command guard also covers
  role/session-authorization and transaction-setting forms that otherwise bypass
  ordinary setting installation, using their existing SET command tag.
  SQL-callable `set_config` instead resolves its name first, then fails with
  `25000` / `parameter "application_name" cannot be set during a parallel operation`
  before access checks, value parsing or installation; NULL-name `22004` and
  unknown-name `42704` retain precedence. Canonical parameter display names are
  preserved. These command-tag and named-parameter paths are not interchangeable.
  `SHOW` and the existing standalone `current_setting` route remain legal reads;
  this fixture does not add generic-expression `current_setting` support.
  Settings failures must retain `application_name='before'`, native nil rows/count
  zero, complete routine statement contexts, the same owner and restored scoped
  context. Serial controls must perform genuine changes. LOCAL/config-local
  bodies end with a scalar SELECT from `pg_catalog.pg_settings` filtered by
  `name = 'application_name' AND setting = 'escaped'`; the read-positive body
  uses `setting = 'before'`. They retain independently expected aggregate `10`,
  so wrong visibility yields no scalar row/NULL state and fails the assertion.
  LOCAL values restore to `before` externally. External unaliased
  `SELECT current_setting('application_name')` and SHOW probes remain intact.
  Quoted `LANGUAGE SQL AS $$ ... $$` bodies retain the reached statement frames;
  unsupported unquoted utility-body admission is not a substitute diagnostic.
  Native utility dispatch must consume zero statement parameters without erasing
  the enclosing routine's formals; the existing setting-call binder seeds those
  formals from `StatementScope.initialParameters` before binding specialized
  setting SELECTs. Count validation, actual effects and runtime authorization
  remain real, not bypassed to admit the fixture.
  The deallocation witnesses reach `ac_effect_clear()` with no formal arguments:
  transition `nested_deallocate_all` executes `DEALLOCATE ALL`, while leader
  `final_deallocate` executes `DEALLOCATE ac_saved`. Their independently expected
  errors are `25000` / `cannot execute DEALLOCATE ALL during a parallel operation`
  and `cannot execute DEALLOCATE during a parallel operation`, respectively.
  Both require the actual inner `SQL function "ac_effect_clear" statement 1`
  frame followed by outer `ac_effect_step` or `ac_effect_final` statement 2.
  Two-command enclosing SQL bodies prevent scalar inlining from erasing the
  outer frame. Only the transition allows the finite PostgreSQL worker suffix;
  the leader context is fixed.
  A real SQL `PREPARE ac_saved AS SELECT 42 AS v` is distinct from the held
  database/sql aggregate statement. `EXECUTE ac_saved` must still return `42`
  (`INT4`) after every direct/fresh/held rejection, on the same session. The
  composed test also checks the retained registry definition before `Finish`,
  nil output/count zero, released pins and restored routine/operation context.
  Ceiling-zero execution must genuinely deallocate the name while returning
  aggregate `10`; a subsequent EXECUTE independently requires `26000` /
  `prepared statement "ac_saved" does not exist`, and private registry absence.
  Refusal must precede utility lookup/mutation, not rely on utility argument-count
  errors, rollback or replacing the owner. Existing command tags distinguish
  DEALLOCATE ALL, DEALLOCATE, EXECUTE and SET CONSTRAINTS. Normal successful SQL
  EXECUTE retains its body command tag.
  The reached admission seam also follows the released classification for other
  supported SQL backend-local utilities (CLOSE, SET CONSTRAINTS, DECLARE,
  DISCARD, EXECUTE, FETCH, PREPARE and UNLISTEN), LOAD, maintenance commands,
  prepared transaction controls and stronger-than-ROW EXCLUSIVE locks.
  COPY FROM refusal uses the released utility command tag `COPY`, not its
  read-only-transaction classification tag `COPY FROM`; COPY TO remains allowed
  by this parallel guard. This is not a second read-only-transaction classifier:
  backend-local commands can remain legal in a read-only transaction while
  forbidden in a parallel operation. DO/CALL retain reached-command checks, not
  a blanket enclosing-command refusal; internal PL cursor operations are not
  SQL DECLARE/FETCH/CLOSE utilities. No unsupported utility family is introduced.
  The twenty-two effects cases do not execute every ancillary utility or setting
  branch; those admission boundaries also require source/caller review.
  Operation membership covers both workers and leader support; it never makes
  the leader a worker or changes the shared session operation context.
  The released `GUC_ALLOW_IN_PARALLEL` builtin exception is only `is_superuser`,
  absent from the supported registry; scoped `GUC_ACTION_SAVE` requires unsupported
  routine `proconfig`. There is no supported exception intersection and no new
  setting, allow-map or function-configuration feature here. See PostgreSQL18.6
  [Parallel Safety](https://www.postgresql.org/docs/18/parallel-safety.html),
  [`utility.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/tcop/utility.c)
  (`ClassifyUtilityCommandAsReadOnly`, `standard_ProcessUtility`, `CreateCommandTag`),
  [`ExecSetVariableStmt`/`set_config_by_name`](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/utils/misc/guc_funcs.c)
  and [`set_config_with_handle`](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/utils/misc/guc.c).
  The later generic guard in `ExecSetVariableStmt` is not an alternate public
  SQL SET/RESET primary: the earlier utility-tag gate determines that diagnostic.
  Run the oracle selection first, then native and selected race proof, retaining
  all direct/fresh/held routes, snapshots and deadlines. This effects selection
  is the new effect-admission behavior smoke, separate from value/persistence
  `FeatureSmoke`; these are required assertions, not a verification status.

- <a id="create-aggregate-contract"></a>CREATE AGGREGATE: `RUN='^TestReference(CreateAggregate|AggregateFinal|AggregateMoving)'`
- Aggregate ALTER/DROP lifecycle: `RUN='^TestReferenceAggregateLifecycle'`.
  Lifecycle helpers reuse CREATE's direct/prepared value and metadata assertions;
  identity checks compare OIDs only within a backend, and native-control phases
  replay actual emitted SQL with destination roles supplied by the caller.
  View retirement helpers cover permanent, explicit TEMP and inferred TEMP
  persistence with real procedure-object lock admission in both directions;
  authority helpers reuse executed statements across owner and support grants.
- Routine direct aggregate dependencies: `RUN='^TestReferenceRoutineAggregateDirect'`.
  The eleven focused bags/defaults/text-body/value/authority/identity/drop/persistence/literals/types/moving
  scenarios assert complete outgoing procedure-edge bags (including separate
  body/default multiplicity), held execution authority and unchanged native dump
  replay, rather than subtracting implementation supports from catalog output.
  Persistence checks default PUBLIC EXECUTE through a real nonowner's effective
  routine privileges, with no grant options, across all twelve declarations
  after each replacement and in a fresh Driver. It does not require optional
  `pg_proc.proacl` column projection.
  Literals covers regproc/regprocedure references sharing an aggregate-support
  address, exact direct bags and original identity through held rename.
  Its authority subscenario executes as a support-denied reader; its independent
  default-ACL persistence subscenario preserves literal identity/bags/values and
  actual nonowner effective EXECUTE after replacement. It does not require
  serialization of schema/table ACLs or treat alias values as invocations.
  Types additionally asserts the complete outgoing normal procedure-to-type bag,
  with class IDs, subobject IDs and physical multiplicity intact. A domain used
  only as transition state has no direct aggregate type edge; support signatures
  still own their domain edges. Genuine aggregate input/result domain edges
  survive state-type overlap and deduplicate within the declaration, while pinned
  integer/text types contribute no rows. Direct, fresh-prepared and held values
  cover populated, NULL-only and empty input before and after native
  Dump/Reset/Restore, proving replay retains the state/support prerequisites.
  Moving retains upstream bounded-frame moving execution and unbounded-start
  ordinary selection. Checked callers expose only their written aggregate edge;
  explicit forward/inverse/final calls retain separate direct edges. Complete
  support-role bags, exact direct/fresh/held values and two native restore rounds
  cover both selected support lanes without provisional ordinary captures.
- The transactional routine/operator/aggregate matrix lives in
  [query_core](../query_core/README.md#testing), reusing its held-handle routes.
  Aggregate results keep their explicit native nullable/known metadata contract
  rather than inheriting scalar SQL-function metadata.
- User ordered-set/hypothetical declarations:
  `RUN='^TestReferenceAggregateOrdered'`. These untagged public scenarios cover
  fixed direct/row support vectors, zero-direct and no-final definitions,
  support-owned sorting (including a contrary WITHIN direction), typed-NULL
  FINALFUNC_EXTRA inputs, grouped/direct demand and strict suppression,
  direct/fresh-prepared/same-held execution, metadata, stored consumers,
  live support/aggregate/type authorization, dependencies, statement atomicity
  and reached SQL-final cancellation. The legal paired VARIADIC ANY witnesses
  invoke the actual transition_multi and all four rank-family final supports,
  with independent scalar/multikey/NULL/empty outcomes. Canonical collapsed
  signatures use regprocedure lookup plus pronargs/provariadic and canonical
  catalog-vector argument metadata.
  `Modes` asserts both final/moving-final modification defaults and independent
  overrides, even without either final implementation. `StrictDeclaration`
  distinguishes PostgreSQL's canonical-first-declared-input no-seed admission
  from runtime row-only transition vectors, for ordinary and moving state.
  `MovingSupport` selects real state/direct/EXTRA moving-final signatures and
  preserves ordered OVER rejection. Their metadata, selected support identities
  and seeded values also run through the actual-stream persistence checks.
  `Persistence` replays the actual native Dump stream unchanged on PostgreSQL,
  including domain/collation/support identities, default/explicit final modes,
  strict SQL support metadata and NULL/no-reseed behavior, raw ACLs and
  ownership, including INTERNAL-state aggregates created by a superuser and
  transferred to a nonsuperuser. Native phases retain old Rows and held statements,
  restore twice, reject invalid/canceled replacement, check stable redump and
  restore into an independent driver. `FeatureSmoke` exercises the new fixed
  and legal variadic behavior, not only prerequisite aggregate checks.
  Native C extension loading, fmgr pointers and PostgreSQL memory-context ABI,
  physical storage/spill and parallel workers are external mechanisms, not
  substitutes for these executable SQL-visible support routines. Ordinary SQL
  cannot manufacture their opaque INTERNAL state: the scalar transition call
  witness fails lookup before support execution. Private aggregate-context
  checks are distinct from this public boundary; final support NULL behavior
  is not replaced by an invented blanket out-of-context rejection.
- Aggregate combine declaration contracts:
  `RUN='^TestReferenceAggregateCombine(InternalDefinition|Diagnostics|SortSupport)$'`.
  The internal-input fixture checks actual builtin support signatures and owned
  declaration metadata without manufacturing an SQL `internal` datum. Its
  native-control phase checks two real Restore operations. SORTOP includes a
  non-Boolean `+` declaration; it does not prescribe transition input ordering.
  Additional public families are `Values|Metadata|Serialization`,
  `Authority|Dependencies|Lifecycle`, `Errors|Cancellation|ParallelSafety`, and
  `FeatureSmoke`, under the same `TestReferenceAggregateCombine` prefix.
  All definitions require selected native and pinned-oracle runs; native
  lifecycle phases use actual emitted Dump SQL, not reconstructed recipes.
  The lifecycle test replays that native SQL on PostgreSQL after native
  execution, so its oracle phase deliberately requires a native dump.
  Private `TestAggregateCombine` units inspect actual partitions, closed
  combine frames, worker ownership/join/error identity, and retained recipes.
  `TestAggregateCombineSignatures` also exercises real declared-arity lookup
  using type-only placeholders: opaque `internal` cannot form even a SQL NULL,
  so signature selection must not construct synthetic argument datums.
  Actual `pg_proc.proargtypes` is a genuine OIDVECTOR datum, not a TEXT substitute;
  `proacl` uses the established ACLITEM[]/NULL producer. InternalDefinition checks
  raw `DatabaseTypeName=OIDVECTOR` and literal byte/text payloads before Normalize,
  zero-argument/empty/NULL distinctions, direct/fresh/held vector input, released
  pg_type facts, and stored vector values through its two actual native Restores.
  Shared ordering checks length-first/unsigned carrier order with SQL NULLS LAST;
  the qualified sort expression cannot resolve to the TEXT display alias.
  Canonical catalog-vector unit tests cover owned/borrowed data, zero-based shape,
  generation remap, exact text/SQLLiteral, OID prefix-scanner diagnostics,
  occurrence ordering, foreign-generation/NULL rejection and cancellation.
  Creator admission checks all declared supports, while execution checks only
  the active split's supports: serial SFUNC and selected final support,
  worker transition/serialization and leader combine/deserialization/finalization.
  Builtin string owner authority comes from the captured aggregate declaration, not its generic
  state provider; all five genuine support identities survive binding/remap.
  `Authority/builtin_string_support` changes the real builtin owner, revokes unused
  combine/codecs without breaking serial execution, and denies each active
  transition/final support under that owner. Its explicit transaction, per-error
  savepoints, and bounded deferred ROLLBACK restore original owners and exact
  complete nullable `pg_proc.proacl` for the aggregate and all five supports.
  Owner plus raw ACL equality preserves the entire effective grantor/grantee/
  grant-option set, including implicit defaults and unrelated grants, without
  requiring an additional information_schema view; public-schema reset alone
  is not sufficient.
  Private `Ownership/builtin_string_support` reaches actual worker transport and
  leader combine/final stages with each owner's support right denied/granted;
  `Remap/builtin_string_support` checks independent captured-support remapping.
  Owner setup uses the existing `ALTER FUNCTION ... OWNER TO` spelling, which
  includes aggregate-kind routines; it does not add an ALTER AGGREGATE utility.
  Private support hosts enter the ordinary statement gate (and physical-source
  admission where applicable), so sequence recovery uses the same attached owner
  rather than bypassing its real transaction/epoch fence.
  Qualified SORTOP uses `OPERATOR(schema.symbol)`, including Dump.
  Domain INITCOND validation uses the incumbent captured unlock/revalidate
  domain-input protocol and captured type diagnostic naming; it must retain CHECK
  diagnostics and search-path-aware qualification, never bypass checks.
  Active cancellation observes the native coordinator and then a real partial
  worker inside `InvokeAggregateRoutine` / `datetimeSleepWaitRemaining`,
  with `runAggregatePartition` on that same live worker stack. PostgreSQL uses
  the shared fixture's actual PgSleep admission instead.
  Worker CTE inheritance is captured before launch, including absence; workers
  never read the live SQL host's temporarily installed/restored CTE scratch.
  `TestAggregateCombineSQLGate/worker_initialization_snapshot` holds one real
  scalar partial worker in admitted SQL pg_sleep while its sibling initializes
  and repeatedly builds Evaluation before/after actual scratch restoration.
  It requires zero live-host CTE reads in those phases, cancellation/join with
  no rows/pins/frame leakage, and unchanged ordinary lexical inheritance.
  `TestStringAggregateSerialization` independently decodes literal codec bytes.
  Run the quiet smoke as `make check RUN='^TestReferenceAggregateCombineFeatureSmoke$'`
  and again with `REFERENCE=1`; it checks exact values, metadata, declaration
  errors, held settings/parameters and two native Restore operations.
- DISTINCT, local ORDER BY, FILTER: `RUN='^TestReferenceAggregateModifiers'`
- `any_value`: `RUN='^TestReferenceAnyValue'`
- `min`, `max`: `RUN='^TestReferenceExtrema'`
- GROUPING SETS, ROLLUP, CUBE: `RUN='^TestReferenceGroupingSets'`
- `num_nulls`, `num_nonnulls`: `RUN='^TestReferenceNullCount'`
  Capability boundaries assert retained ordinary-call star/FILTER syntax through
  exact shared semantic diagnostics, including direct and prepared failure stages.
- Variance, stddev, `regr_s*`: `RUN='^TestReferenceStatisticalAggregate'`
- `corr`, `covar_*`, remaining `regr_*`: `RUN='^TestReferenceCorrelationRegression'`
- Hypothetical-set (`rank`, `dense_rank`, `percent_rank`, `cume_dist`): `RUN='^TestReferenceHypotheticalAggregate'`
- Ordered-set (`percentile_cont`, `percentile_disc`, `mode`): `RUN='^TestReferenceOrderedSetAggregate'`
- SUM over MONEY and INTERVAL: `RUN='^TestReferenceMoneyIntervalSum'`
- `json[b]_object_agg[_strict]`: `RUN='^TestReferenceJSONObjectAggregate'`
- `json[b]_object_agg_unique[_strict]`: `RUN='^TestReferenceJSONObjectUniqueAggregate'`
- `json[b]_agg[_strict]`, `json_arrayagg`: `RUN='^TestReferenceJSONArrayAggregate'`
- `JSON_OBJECTAGG` and SQL/JSON aggregate output encoding: `RUN='^TestReferenceSQLJSONObjectAggregate'`
- `bit_and`, `bit_or`, `bit_xor`: `RUN='^TestReferenceBitwiseAggregate'`
- `range_agg`, `range_intersect_agg`: `RUN='^TestReferenceRangeAggregate'`
