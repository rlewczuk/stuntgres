# reference/query_core

Core query and routine behavior: SQL routines (CREATE FUNCTION/PROCEDURE with
quoted and SQL-standard bodies, overload resolution against builtins, ALTER/DROP),
WITH queries including recursive CTEs, CYCLE and SEARCH, subqueries, row
comparison, joins, FROM VALUES, aliases, set operations, CREATE TABLE AS,
expression indexes, parallel SELECT scans, and scalar expressions (BETWEEN,
CASE/COALESCE/NULLIF, logical operators). Related families: routine parameter
modes, defaults, named/variadic calls and OUT/INOUT
(`^TestReferenceRoutineParameters`) in [operators](../operators/README.md);
explicit casts in [type_resolution](../type_resolution/README.md); comparison and
NULL predicates in [types](../types/README.md); windows, ordering, LIMIT and
DISTINCT in [query_order](../query_order/README.md); grouping sets in
[aggregates](../aggregates/README.md).

Routine declarations share creation and identity-preserving replacement contracts,
including STRICT attributes, named/mode signatures and SQL bodies.

Column qualification selects visible relation identities, including eligible
outer frames. Schema-qualified names do not add FROM sources, and a matched
inner qualifier owns its missing-column diagnostic rather than retrying outer
relations. Source column descriptors and origins survive expression composition.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`.
- Helpers: `routine_helpers_test.go` (`routineScenario`, `routineValue`,
  `routineScalarMetadata`), `routine_drop_helpers_test.go`,
  `query_cycle_helpers_test.go`, `query_recursive_cte_helpers_test.go`,
  `query_search_helpers_test.go`, `query_subquery_helpers_test.go` and
  `query_parallel_helpers_test.go`.
- **Persistence.** Routine, view and set-operation persistence phases run native
  Dump/Reset/Restore; in real mode they replay the exact native Dump stream on
  PostgreSQL, never reconstructed SQL.
- **Metadata.** Native computed results, including scalar SQL calls, report
  unknown nullability, while stored columns and `COUNT` report it as known; use
  `routineValueWithNativeNullability` where a stored column is projected.
  Client quirks asserted here: lib/pq reports unbounded VARCHAR `Length` as
  `(-5,true)` (native `(MaxInt64,true)`; TEXT is `(MaxInt64,true)` on both),
  unconstrained NUMERIC and `_NUMERIC` `DecimalSize` as `(65535,65531,true)`
  (native unknown), RECORD/`_RECORD`, internal `"char"`, CSTRING and VOID with
  `ScanType` `interface {}` (native `string`), and their cells as `[]byte`
  (native string). Its OID inventory omits multiranges and the query-only
  compatible array/range identities: their database type names are empty, while
  raw transport and logical result usages are asserted separately.
- Create committed predecessors before BEGIN when testing restoration; private
  source/table chains construct their definitions inside the owner's block.
- Parallel scans: matching rows do not prove that workers ran. Concurrent
  evaluation, exact-once ranges and cancellation joins are proven by
  `internal/engine` `TestQueryParallel*` units; reference tests assert occurrence
  bags, settings and lifecycle. A parallel-settings change also needs the
  registry inventory selector in [session_state](../session_state/README.md#testing).
- `TestReferenceConditional(TypeMatrix|NaNAndTies)` and
  `TestReferenceQueryJoins(Scope)?Diagnostics(Oracle)?` carry the legacy
  `reference` build tag and run only with `REFERENCE=1`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Qualified column consumers: `RUN='^TestReferenceThreePart'`
- Source identity and system-column namespaces: `RUN='^TestReferenceQueryTableOID'`
  `TestReferenceQueryTableOIDFrozenPrivileges` distinguishes a view's effective
  source owner from its caller and SQL-standard invoker routines, including
  column-only grants and retained callers across source SELECT revoke/regrant.
- Scalar SQL routines: `RUN='^TestReferenceRoutine(Core|StoredArgumentIdentity|Review)'`
  `TestReferenceRoutineCoreStrictDeclarations` covers admitted STRICT declaration
  orderings and SQL-standard bodies, NULL body suppression versus a nonstrict
  control, non-NULL execution, any-NULL multiargument suppression, and exact
  `pg_proc.proisstrict` through direct and fresh-prepared queries. Run the changed
  diagnostics and positive family in both modes:
  `make check RUN='^TestReferenceRoutineCore(Diagnostics|StrictDeclarations)$'`
  and the same command with `REFERENCE=1`.
- Pseudo-type metadata, signature families and scalar record callers:
  `RUN='^TestReferencePseudoTypes'`. Result usages are concrete specializations;
  catalog signatures stay declared. Query-only typed pseudo NULLs grant no storage
  or handler execution authority. Native canonical database type names establish
  result identity; PostgreSQL SQL identity probes supplement lib/pq's finite OID
  names. Unnamed user enum results additionally use inferred CTAS column identity.
  Scalar-family literal casts retain UNKNOWN until final TEXT projection;
  enum casts and dummy container/handler inputs have separate exact diagnostics.
- <a id="ordered-sql-routine-bodies"></a>Ordered and SQL-standard routine bodies, procedures: `RUN='^TestReferenceRoutineSQLBodies'`
  `TestReferenceRoutineSQLBodiesMergeSourceCompaction` keeps its MERGE target
  unchanged while dropping an unused leading source column. Direct and genuinely
  held invocations must update target `(1,0)` to `(1,7)`, retaining the exact source
  id2/v3 dependency bag and used-column RESTRICT enforcement. Savepoint/outer
  rollback and committed compaction share those assertions. The incumbent
  `sqlBodiesReplayTwice` snapshots the original three-column baseline, performs
  two native replacements and replays each unchanged SQL stream on PostgreSQL;
  fresh handles compact the source anew in every phase.
  Run `make check RUN='^TestReferenceRoutineSQLBodiesMergeSourceCompaction$'`
  and the same selector with `REFERENCE=1` before review.
  `TestReferenceRoutineSQLBodiesCTESourceCompaction` covers the separate
  SELECT-CTE command owner, mixed modifying SELECT and actual nested UPDATE-FROM
  and INSERT-SELECT bindings. Its six cases include `main_update_values` and
  `child_update_values`, genuine UPDATE FROM sources, each reseeding before direct/preheld
  calls that return and store 7. Exact attribute-2 dependencies, used-column
  RESTRICT, savepoint/outer rollback, committed compaction and two replacement
  rounds retain those assertions. The same replay helper emits the unchanged
  native SQL stream for PostgreSQL; select the exact name in both modes.
- DROP FUNCTION/PROCEDURE: `RUN='^TestReferenceRoutineDrop'`
- Transactional routine/operator/aggregate matrix: `RUN='^TestReferenceTransactionalRoutine'`.
  The shared direct/held × autocommit/SQL/Go route helper retains actual handles;
  definition bodies, authorization, peer graphs, savepoints and native controls
  share independent expectations. Protocol phases live in operators.
- SQL overloads beside builtin, window and aggregate providers; rule qualifications: `RUN='^TestReferenceRoutine(Resolution|Window|Nesting|LateChild|Rule)'`
- <a id="select-cte-verification"></a>WITH queries: `RUN='^TestReferenceQueryCTE'`
- <a id="recursive-cte-verification"></a>Recursive CTEs: `RUN='^TestReferenceQueryRecursiveCTE'`
- CYCLE: `RUN='^TestReferenceQueryCycle'`
- SEARCH: `RUN='^TestReferenceQuerySearch'`
- Subqueries: `RUN='^TestReferenceQuerySubquery'`
- Row comparison: `RUN='^TestReferenceRowCompare'`
- Joins: `RUN='^TestReferenceQueryJoins'`
- FROM VALUES: `RUN='^TestReferenceQueryFromValues'`
- Aliases: `RUN='^TestReferenceQueryAliases'`
- Set operations: `RUN='^TestReferenceQuerySetOps'`
- CREATE TABLE AS: `RUN='^TestReference(QueryTableCreate|TransactionalSchemaQueryCreate)'`
- Expression indexes: `RUN='^TestReferenceExpressionIndex'`
- Parallel SELECT scans: `RUN='^TestReferenceQueryParallel'`
- Scalar expressions: `RUN='^TestReference(Between|Conditional|Logical|ExpressionCore|QueryCore|Smoke(Between|Compare|Conditional|ExpressionCore|Logical|QueryCore))'`

### Routine ALTER proof

`TestReferenceRoutineAlter*` shares independently specified SQL observations
between native and pinned PostgreSQL18.6: exact overload/domain/mode identity and
unchanged OIDs; hard view/standard/default/CHECK/rule references versus soft
quoted source; original Go/SQL held prepared reanalysis; current qualified
formals; collision/publication atomicity; schema/owner/SET ROLE/ACL authority;
released scalar/window and installed extension/aggregate execution; repeated
native persistence. Procedure declarations use real metadata, not CALL.

Run the full focused definitions in both modes (external 900-second deadlines):

```sh
make check RUN='^TestReferenceRoutineAlter(Identity|Signatures|Dependencies|Prepared|FormalQualifier|Atomicity|Authority|OwnerACL|Providers|ProviderPersistence|Persistence|SchemaBoundaries|OwnerOperands|ExplicitACLPersistenceBoundary)$'
make check RUN='^TestReferenceRoutineAlter(Identity|Signatures|Dependencies|Prepared|FormalQualifier|Atomicity|Authority|OwnerACL|Providers|ProviderPersistence|Persistence|SchemaBoundaries|OwnerOperands|ExplicitACLPersistenceBoundary)$' REFERENCE=1
make check RUN='^TestRoutineAlter(Parser|DeclarationIdentity|ProviderOverrideIndex|RevisionAtomicity|HardCaptureAdmission)$'
```

The temporary oracle-discovery filter is removed; tests use `routineScenario`.
Public values and metadata are expected independently, never inferred from
backend parity. Qualified-formal failures keep exact SQLSTATE/message and both
diagnostic records: native body-relative Position8/statement1 Context versus
observed PostgreSQL InternalPosition8/InternalQuery/inlining Context. Repair is
DROP/CREATE, not OR REPLACE; native claims no PostgreSQL optimizer phase.

Persistence checks two native replays, a fresh Driver with provisioned roles,
provider-only streams without a SQL-body directive, retained Rows and
read/cancellation/late-invalid atomicity; real mode replays the actual emitted
stream. Supported unrelated explicit routine ACLs now roundtrip exactly, including
renamed identity and named grantors, rather than retaining a Dump refusal.
Reset preserves roles. See [native replay](../../docs/FIXTURES.md#routine-identity-replay)
and the [owning contract and limits](../../internal/engine/routine/README.md#routine-alter-contract).

Additional shared routine-identity definitions:

- `TestReferenceRoutineAlterEqualityCaptures` and
  `TestReferenceRoutineAlterEqualityCapturePersistence` create independent
  DISTINCT, GROUP BY, UNION/INTERSECT/EXCEPT, window partition and peer consumers
  before equality-routine ALTER. Direct/prepared checks retain exact values and
  types, original operator lifetime versus a fresh same-name SQL function, and
  the selected operator's current EXECUTE denial/regrant. Real rule-action
  DISTINCT keys retain their original equality proof when NEW receives a fresh
  producer expression. Two native replays, a fresh engine and actual emitted
  PostgreSQL SQL cover persistent equality captures. These scenarios do not
  infer implicit grouping-helper ACL behavior from explicit operator calls.
- `TestReferenceRoutineAlterBuiltinCaptures` and
  `TestReferenceRoutineAlterBuiltinCapturePersistence` retain aggregate, window
  and independently admitted boolean-alias declarations created before ALTER.
  They distinguish hard captures from fresh source lookup, keep current EXECUTE
  revoke/regrant checks and cover native plus actual emitted-SQL replay.
  The persistence witness keeps six pre-ALTER TEMP views on the same physical
  connection across Restore without Reset, checking direct/prepared values and
  types separately from imported persistent declarations. It explicitly drops
  those TEMP views before the two Reset/replay rounds, so retained destination
  identity and imported-source identity exercise distinct replacement policies.
  Fresh controls use the raw native pool; independent recorded SQL checks retain
  both Context/non-Context and direct/prepared routes over the same connector.
- `TestReferenceRoutineAlterRuleBuiltinCaptures` retains pre-ALTER rule action
  windows, SUM, both COUNT orders and both independent boolean-alias orders.
  Direct/prepared INSERTs keep their original results after rename/move/owner,
  while current EXECUTE denial and regrant remain effective. Dependencies also
  composes an original stored callee with a fresh same-address producer call;
  those separate lifetimes yield 2001 rather than rebinding the stored call.
- `TestReferenceRoutineAlterProviders` also covers Cube's existing constructor,
  dimension, spatial, equality and btree-support members through rename/schema/
  owner transitions: original OIDs, stored direct/operator/CASE views, fresh
  direct/prepared calls, current owners and sibling overloads. Ordering, grouping
  and subsequent indexed writes exercise the existing support graph. Native
  restoration and the actual emitted PostgreSQL stream use the actual versioned,
  noncontiguous member slots and operator/family/class associations, not names.
  Existing procedural declarations also retain integer overload OIDs, views and
  untouched text siblings after rename/schema/owner, two native replays and
  actual PostgreSQL stream replay. Shared checks assert values, types and owner;
  the extra PostgreSQL language-column assertion stays external-only because
  native `pg_proc` does not expose `prolang`.
- `TestReferenceRoutineAlterSQLJSONArray` and
  `TestReferenceRoutineAlterSQLJSONObject` create views before selected
  implementation aggregate rename/move. Exact PostgreSQL18.6 canonical
  `pg_get_viewdef` keeps constructor syntax independently of implementation names.
  Expectations distinguish FORMAT JSON/quoted text, NULL/ABSENT, TEXT/JSON/BYTEA
  RETURNING and OBJECT VALUE/unique-key checks even for omitted nulls. Two native
  replays, fresh Driver and actual PostgreSQL stream replay retain values, types,
  canonical definitions and options; ordinary qualified implementation calls
  remain executable. Exact canonical assertions distinguish the original
  before/after-ALTER definition from actual emitted-stream PostgreSQL18.6
  canonical: replay retains its table hygiene alias, but omits redundant column
  aliases, quotes and visible schema qualification. Fresh Restore uses a raw
  native control pool; SQL observations use a separate sqlmw-recorded pool over
  the same connector/engine namespace. CALL remains unsupported, not ALTER.

```sh
make check RUN='^TestReferenceRoutineAlter(BuiltinCaptures|BuiltinCapturePersistence|RuleBuiltinCaptures|Dependencies|SQLJSONArray|SQLJSONObject)$'
make check RUN='^TestReferenceRoutineAlter(BuiltinCaptures|BuiltinCapturePersistence|RuleBuiltinCaptures|Dependencies|SQLJSONArray|SQLJSONObject)$' REFERENCE=1
```

No redundant permanent feature-smoke matrix is added. After whole-project
`make quickcheck`, root materializes the throwaway public `database/sql` program,
runs `go run /tmp/stuntgres-routine-rename-owner-smoke.go`, records actual output
and removes its scaffold. Expected observations include5/INT4 and sibling104,
held6/10 and metadata, stable OID/procedure kindp, hard1001 versus fresh2001,
collision42723/unauthorized-owner42501, genuine renamed builtin4/window maximum2,
SHA256 of `abc`, and two restored owner/provider/data rechecks. Readiness is
not verification; only exercised commands/output are evidence.
