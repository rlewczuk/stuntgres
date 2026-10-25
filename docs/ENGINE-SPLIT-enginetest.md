# Engine split: `enginetest` and the root tests, seventh round

Session prompts for linear items 181, 183–187 and 193–194 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md): §2.2 (T1–T5), §3 and §5. The placement
policy is in [TESTING.md](TESTING.md).

The root holds 1,346 test files (10.65 MB). They are white-box `package engine`
tests, and most build an engine and run SQL through the helpers in
`engine_test.go`. Sizes below come from `closure` at `26f8fea15`. Re-run it
first, together with `waves.py`
(`.cswd/tmp/PG18-ENGINE-SPLIT-CLOSING-REVIEW-4/`).

## How a test wave works

Items 183–187 each take one group of root test files. Within the group they
handle only the files `closure` lists as free, or as reaching only exported
names after item 182 (`tests.tsv` and `testrefs.tsv`, transitive columns). For
each file:
1. **Inventory (T1)**, per test function.
2. **Dedupe (T2).** Delete tests that `reference/` already covers.
   `refcover.py` lists candidates; confirm each by reading both tests. Name each
   deletion in the commit body.
3. **Move (T3).**
   - A test that builds an engine becomes an external test
     (`package <owner>_test`) in the owner's directory, or in the owner's
     `enginetests/` subdirectory when the owner is a lower package (the T3
     fallback). It imports `stuntgres/internal/engine` (names qualified
     `engine.X`) and `enginetest`.
   - A pure unit becomes an internal test of the package whose code it tests.
   - A directory for a package that does not exist yet (`core/`, `*cmds/`)
     starts with a `doc.go` that holds only the package clause and its
     one-sentence doc.
4. **Helpers (T4).** A helper used by two or more destinations goes to
   `enginetest`. A helper used by one destination moves with its users. The
   root copy stays while root tests still use it. Internal tests cannot import
   `enginetest`; they take a copy.

Keep test names and bodies; rename only when two moved names collide. Stop at
about 200 files or 1.3 MB; leftovers become a new item at the end of the plan.

**Verification for every wave:**
- `make build` and `go test -run '^$' ./internal/engine/...`;
- the moved selectors in their new packages, and the root selectors of the
  files that stay;
- `make quickcheck`;
- `REFERENCE=1` and bench skipped (no production change);
- report the root test bytes before and after.

## Item 181 — test fixture and pilot

Needs 180. Mode P. About 0.26 MB leaves the root.

1. **`internal/engine/enginetest`.** Write exported helpers over the root's
   exported API only:
   - `Operation`, `Execute`, `ExecuteFrame`, `Describe`, `Statement`, `Run`,
     `Tuples`, `IndependentFinalRows`, `Export`, `Usage` and `State`;
   - they follow `testOperation`, `testExecute`, `testExecuteFrame`,
     `testDescribe`, `statement`, `run`, `tuples`, `independentFinalRows`,
     `testExport`, `testUsage` and `state` in `engine_test.go`;
   - `ExecuteFrame` builds its `0A000` error with `sqlstate` instead of the
     root's `sqlError`.

   Add a README: the contract, that only `_test.go` files import it, and the
   two stages.
2. **TESTING.md.** Amend the placement policy in place, without growing
   `AGENTS.md`. Internal tests that need a composed engine are external tests
   beside their owner, using `enginetest`; new ones are never added to the root
   `internal/engine`.
3. **Pilot.** Move or dedupe the 63 root test files that `closure` reports as
   transitively free.
4. **Build cost.** Put one engine-fixture test in `functions` as an external
   test, and measure:
   - `go test -count=1` wall time and test-binary size for `functions`;
   - the same with the test in a test-only subdirectory of `functions`, and in
     `core/`;
   - quickcheck wall time.

   Fix the T3 fallback in ENGINE-SPLIT.md §2.2 and in TESTING.md.

**Verify:** as for a wave, plus `make deps` (`enginetest` imports only the
standard library and this module).

**Docs:**
- the `enginetest` README;
- TESTING.md;
- the root README's `## Testing`, if it names the helpers.

## Item 183 — builtin function, operator and type tests

Needs 182. Mode P. About 174 files (1.25 MB).

- **Group:** test-only prefixes of builtin functions, operators and types:
  - containers and JSON: `range`, `json`, `jsonb`, `jsonpath`, `sqljson`,
    `array`, `subscripts`, `list`;
  - text and bits: `string`, `character`, `format`, `binary`, `bitstring`,
    `bitwise`, `boolean`, `logical`;
  - date and time: `datetime`, `date`, `time*`, `timezone`, `interval`;
  - numbers and other values: `network`, `uuid`, `numeric`, `math`,
    `arithmetic`, `money`, `oid`, `tid`, `mac`, `geometry`;
  - expressions and comparison: `cast`, `compare*`, `expr*`, `function`,
    `between`, `any`, `strict`;
  - aggregates and set-returning functions: `aggregate`, `avg`, `sum`,
    `extrema`, `statistical`, `hypothetical`, `grouping`, `srf`, `series`.
- **Owners:** `functions` or `sqltype`, by the package of the code under test,
  or the fallback that item 181 fixed.
- **Frame-name tests here:** `aggregate_modifiers`, `bitwise_aggregate_eager`,
  `hypothetical_set_aggregate`, `extrema`, `range_aggregate`. Their frames are
  subpackage names, so they move unchanged.

## Item 184 — query, DML, COPY, cursor and protocol tests

Needs 182. Mode P. About 148 files (1.05 MB).

- **Group:** test files whose prefix area is `query`, `dml`, `statement` or
  `exprutil`. That covers `query_*`, `insert_*`, `update_*`, `delete_*`,
  `merge_*`, `copy_*`, CTE, cursor, portal, protocol and prepared-statement
  tests.
- **Owner:** `core/` (create `doc.go`). Binder-only units move to `analyze`, and
  executor-only units to `exec`.
- **Keep unchanged:** the allocation-budget files and the benchmarks
  (`insert_conflict*`, `update_allocation`), and the frame-name tests
  `query_aggregate`, `query_window*` and `query_pruning_safety`.

## Item 185 — transaction, session, catalog and procedural tests

Needs 182. Mode P. About 117 files (0.8 MB).

- **Group:**
  - areas `txsession`, `catalogorch`, `plpgsql`, `routine` (the runtime), `engine`,
    `funcglue`, `regobj` and `pgcatalog`;
  - test-only prefixes of runtime behaviour: `concurrent`, `transactional`,
    `settings`, `notices`, `driver`, `xid`, `snapshot`, `version`, `error`.
- **Owners:**
  - `pgcatalog` for producer tests (`pg_*`, `info_*`, catalog sources), or the
    item-181 fallback;
  - `core/` for the rest.
- **Keep unchanged:** `transaction_remedy` and `plpgsql_walk_benchmark`, and
  the frame-name tests `serializable_publication` and `datetime_sleep`.

## Item 186 — table, index, partition and sequence DDL tests

Needs 182. Mode P. About 147 files (0.94 MB).

- **Group:**
  - areas `table`, `index`, `inheritance`, `partition`, `sequence` and `vacuum`;
  - test-only prefixes `unique`, `check`, `primary`, `partial`, `composite`,
    `serial`.
- **Owner by statement under test:**
  - `tablecmds/`: CREATE, ALTER and DROP TABLE, constraints, sequences,
    identity and VACUUM;
  - `indexcmds/`: index DDL;
  - `partitioncmds/`: partition and inheritance DDL;
  - `core/`: runtime behaviour, such as FK enforcement, unique probing and
    partition routing.
- **Frame-name tests:** `range_partition_publication`, `index_hash_cancellation`
  and `vacuum_concurrency` match root frames such as
  `(*pendingPartitionDefinition).finish`. Moving the test does not change a
  frame name. The strings change only in the commit that renames or moves the
  function (T5).

## Item 187 — type, view, role and schema-object DDL tests

Needs 182. Mode P. About 114 files (0.78 MB).

- **Group:** areas `typedef`, `view`, `trigger`, `roles`, `ddlcommon`, `routine`
  (DDL), `extension` and `fdw`, plus the extension prefixes (`cube`, `earth`,
  `intagg`, `isn`, `citext`, `pgcrypto`, `intarray`, `lo`).
- **Owner by statement under test:**
  - `typecmds/`, `viewcmds/` (views, rules, policies, trigger DDL), `rolecmds/`
    or `schemacmds/`;
  - `core/` for runtime behaviour: RLS, rule execution, trigger firing,
    matview refresh;
  - `extension` or `functions` for extension function behaviour.
- **What waits:** the extension-provider tests that call
  `stageExtensionLocked` or `extensionHost` directly (about 1.3 MB)
  reach family code. They move with item 203.

## Item 193 — test accessors I: runtime areas

Needs 192. Mode P. About 0.1 MB of deletions; the rest moves in item 195.

The runtime-area root tests that are still in the root reach test-only core
internals: 528 names in all, for example `Engine.publishCatalog`,
`StatementScope.acquireRelation`, `replacementAdmission.TryLock`,
`cursorRegistry.entries` and `StatementDescription.sequences`. Do this for each
remaining file in the areas of items 184–185:
1. **Dedupe (T2).**
2. **Route accesses.** Send every access to an unexported core member through
   an accessor function in a root `inspect_<area>_test.go` file. Give each
   accessor its final exported name (`ScopeOp(s)`, `EngineCatalog(e)`), so the
   core move only moves the file. Names that item 182 exported need no
   accessor.
3. **Check.** No test outside the `inspect_*_test.go` files may name an
   unexported core member. Check with `closure`: the remaining runtime-area
   tests reach unexported core names only through accessor files.

**Verify:** the touched selectors, and `make quickcheck`.

## Item 194 — test accessors II: DDL areas and family tests

Needs 193. Mode P. About 0.1 MB of deletions.

1. **DDL-area tests.** For the DDL-area files left from items 186–187, do the
   same as item 193.
2. **Family tests that also need core internals.** The 123 files (1.54 MB) that
   reach family code and test-only core internals (about 175 names) cannot stay
   in the root after the core move: a root test cannot see core's
   `export_test.go`. For each file, in this order of preference:
   1. rewrite the assertion over the exported API, `enginetest` or SQL
      (`pg_locks`, catalog queries);
   2. delete it under T2 or T5;
   3. add a listed exported inspection accessor to a root `inspect.go`, which
      becomes `core/inspect.go`. Keep the list in the prompt Status and in the
      future core README; item 204 removes accessors once unused.

**Verify:** the touched selectors, and `make quickcheck`. Report how many
inspection accessors were added.

## Status

- 181: done (`89d097235`, `32fd10461`, `e54dc0604`, `8fa49b723`, `8067af598`).
  `enginetest` stage 1 has `Operation`, `Execute`, `ExecuteFrame`, `Describe`,
  `Statement`, `Run`, `Tuples`, `IndependentFinalRows`, `Export`, `Usage` and
  `State`, plus `RoleOperation` and `RoleExecute` (deviation: pulled forward
  from stage 2, because four pilot destinations use them). Build cost, one
  engine test for `functions`, after a change to its export data with the normal
  build cached (wall/CPU s): in `functions/` 12.3/71 (24 packages, the root
  among them, rebuilt as `functions.test` variants), in `functions/enginetests/`
  4.9/23, in `core/` 4.9/23, none 4.7/17; binaries 56/52/52/25 MB; quickcheck
  wall 324 s before and 279 s after (load varied; the root package 135 s both
  times, each new test package under 0.5 s). Fallback: lower packages with
  internal tests take engine tests in `<owner>/enginetests/` (`doc.go` plus
  `package enginetests_test`); `core/` and `*cmds/` in their own directory. Pure
  units become internal tests of the package of the code under test; internal
  tests cannot import `enginetest`, so files that share a helper move together
  (the similar/posix files to `analyze`) or take a copy. Kept in the root for
  their users in later waves: `catalog_source_producers`, `eval_context`,
  `format`, `identity_helpers`, `operator_family_fixture` and
  `pg_description_seed_data` (helpers only) and `object_acl_fixture` (tests the
  root fixture `withObjectACL`); `math_consumers_test.go`, `range_type_test.go`
  and `range_type_remedy_test.go` remain as helper-only remainders. Root helpers
  copied into moved files (the root copies stay): `state`, `cancelAtErrCheck`,
  `mathObservedArgument`, `primaryKeyUnitName`, `nullCountUnitDiagnostic`,
  `nullCountRaw`, `indexClausesTypes`, `jsonObjectUnitTypes`, `queryProbe`,
  `queryProbeExpression`, `evalContext`, `cteUnitNext`, `cteUnitType`,
  `cteUnitValue`, `recursiveUnitFixture`, `generatedUnitDescriptor`,
  `generatedOccurrenceProbe`, `accessMethodHeapOperationsUnit`, `btreeKeys`,
  `operatorFamilyTestRead`, `operatorFamilyTestExclusion`,
  `operatorFamilyUnitSeed`, `aclUnitType`, `cancelAfterPreflightContext`,
  `subscriptsUnit` and `domainUnitType`. `info_sql_sizing_boundaries_test.go`
  became free after the 26f8fea15 measurement, and `upc_coercion_test.go` was
  added to the root after the start tip (feature inflow, for the waves). Tools:
  `mvtest` and `inv` (§2.3). For item 195: once core has internal test files,
  `core_test` files that import `enginetest` rebuild the root and the families
  against core's test variant (the cost measured above); weigh
  `core/enginetests/` for engine tests that need no accessor.
- 183: done (`fa8fdc8e7`, `6ad4ba40b`, `8b5c5a3c1`). Group measured with
  `group.py`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-183-BUILTIN-FUNCTION-OPERATOR-TYPE-TESTS/`): 202
  root files with a listed test-only prefix, 174 transitively free (1.23 MB).
  Moved 157 with that directory's `mvtest` (item 181's, plus mapping of
  test-file methods on production types, `e.SetObjectACL(...)` →
  `enginetest.SetObjectACL(e, ...)`, renaming of locals named `engine`, reuse of
  helpers the destination already holds, and a fixpoint for root files reduced
  to helpers): builtin functions and aggregates to `functions/enginetests`,
  types, operators, casts, comparisons, collation, subscripts, SQL/JSON and
  jsonpath to `internal/sqltype/enginetests`, and `range_type_*` (range type
  DDL, as item 181 placed range type tests) to `typedef/enginetests`. T2: none
  deleted; the `refcover` candidates assert bound nodes, grouping, catalog or
  view identity, or bind shapes `reference/` lacks. Left in the root: the
  partition DDL files `range_partition_*` (10) and `list_partition_*` (3) for
  item 186, `datetime_sleep` for item 185, the helper-only `format`,
  `math_consumers`, `range_type` and `range_type_remedy`, and 21 moved files
  reduced to the helpers staying root tests use. Root helpers copied into the
  moved files (the root copies stay), as in item 181, because item 184 ran in
  parallel on the same query helpers: `queryCoreFixture`, `queryProbe` and
  `queryProbeExpression`, `evalContext`, `binderOperation`,
  `bindTestTargetNamespace`, `replacementProcedurePolicyFixture` and
  `regexpSetUnitLower`/`functionSetUnitArgument` are now copied in two or more
  destinations and are candidates for `enginetest` (T4). Not in any wave's
  prefix list, free, still in the root: `predefined_capabilities`, `name`,
  `like_acl`, `like_namespace`, `nominal_type_restore_namespace`,
  `floating_arithmetic_cancellation` (and the helper-only `eval_context`).
  Non-free files of this group stay for items 193–194: 14 reach core internals,
  11 family or shared code, 3 root (`bitwise`, `timestamp`, `timezone_sources`).
- 184: done (`d42ce4707`, `d780b2628`, `7294fc404`, `2896c3a92`). Group
  re-measured at `5c9bdbeb3`: 231 root files in the `query`, `dml`, `statement`
  and `exprutil` areas, 154 transitively free (1.09 MB). Moved: 137 files to
  `core/` (`core_test`, including the pure `statement_host` units, which test
  root code), and 15 test functions that build no engine, split out of
  `query_aliases_hint`, `query_cte`, `query_outer_joins`, `query_row_marks`,
  `query_set` and `query_subquery` into four `analyze` and five `exec` internal
  test files. Kept in the root unchanged: `query_aggregate`, `query_window*` (12
  files) and `query_pruning_safety` (frames), `query_joins` (allocation budget)
  and the two `insert_conflict*` benchmark files; the other `insert_conflict`
  tests moved. T2: none deleted; `TestQueryOrderValuesProducer`'s only cover,
  `TestReferenceQueryOrderValuesAndAtomicity`, is a `//go:build reference` test
  that quick mode never runs. T4: moved ACL tests use item 183's
  `enginetest.SetObjectACL` (item 183's `mvtest` maps `e.SetObjectACL(…)` to
  it); after item 185 merged, the core move was replayed on it: 50 root helpers
  were copied into `core/`, 95 copies item 185 had put there were reused, and
  its copies of `hashCaptureQuery`, `hashImplicitQuery`, `hashFilterStage` and
  `deleteOutcome` were deleted in favor of the originals that moved with their
  files; 4 helpers were copied into `analyze`/`exec` (`statement`,
  `cteUnitScan`, `cteUnitNext`, `queryProbe`), and 16 root files were reduced to
  the helpers staying tests use. For items 186–187: `mvtest` reuses helpers
  already declared in `core/` by name, but not the other way round: a helper
  declared by a moving file clashes with an earlier copy, which then has to go.
  Root helpers left without root users, for the waves that move their files:
  `alterDropTestStage`, `copyFinalCancellationVeto`, `copyTimeoutTest`,
  `copyWaitContext`, `deleteCancelAfterCheck`, `enumCapturedArgument`,
  `aggregateCancelContext` (in the kept `query_aggregate`),
  `assertDistinctACLFailure`, `queryJoinsAdmittedFixture` (in the kept
  `query_joins`), `queryCountingContext`, `updateDeleteAsync` and
  `updateDeleteExecute`. The 77 non-free group files (71 reach test-only core
  names) wait for item 193.
- 185: done (`daec25f89`, `3134ef51b`, `1f7b7027d`). 111 of the 123 transitively
  free files of the group moved with `mvtest` (external tests, names and bodies
  unchanged): 44 producer tests (`pg_*`, `info_*`,
  `catalog_source_runtime_view`) to `pgcatalog/enginetests`, 67 to `core`. T2:
  none deleted (every refcover candidate asserts internal state). Left:
  `engine_test.go` and the helper-only `catalog_state_fixture`,
  `catalog_source_producers` and `pg_description_seed_data`; the routine and
  operator DDL files (`operator_class_dependency`, `operator_class_support`,
  `operator_dump`, `operator_estimator`, `operator_option`, `operator_user`,
  `routine_alter_owner_acl`, `routine_drop_replay`) are `routine` (DDL), for
  item 187. `enginetest.SetObjectACL` came with item 183. Root-side cut: the
  test-file method `AuthorizationView.definitionProceduresNamed` became a
  receiver-first helper (external tests cannot declare methods on root types);
  the copied `pgOperatorReleaseRows` opens `docs/design` from its new depth.
  Notes: the `concurrent_index_*` files in `core` test CREATE INDEX CONCURRENTLY
  and may follow `indexcmds` (item 198); mvtest left 24 root helpers with no
  root user (in `advisory_xact_lock`, `pg_depend`, `pg_locks`,
  `session_timeout`, ...) for item 193's dedupe; 19 free root test files (0.09
  MB) match no wave's prefix list (`access` 8, `like` 2, `dependents`, `eval`,
  `floating`, `name`, `nominal`, `operation`, `predefined`, `public`, `three`).
- 186: done (`cf462a24b`, `c261ae301`, `664831a86`). Group re-measured at
  `13ad970b9` (`group.py`,
  `.cswd/tmp/PG18-ENGINE-SPLIT-186-TABLE-INDEX-PARTITION-SEQUENCE-DDL-TESTS/`):
  148 transitively free files of the listed areas and prefixes (0.95 MB), plus
  the 12 free `range_partition_*`/`list_partition_*` files item 183 left. 157
  moved with `mvtest` as external tests (names and bodies unchanged), by
  statement under test (`dest.py`): 79 to `tablecmds` (ALTER TABLE forms,
  constraints, NOT NULL, primary keys, CREATE TABLE LIKE and AS, sequences,
  identity, serial, VACUUM), 21 to `indexcmds` (index DDL, exclusion and unique
  index builds), 27 to `partitioncmds` (partition and inheritance DDL), 30 to
  `core` (relation locks, waits and deadlocks, FK enforcement, unique and
  exclusion probing, check evaluation on writes, `composite_expr`, virtual
  generated reads, `pg_partition_*` and sequence functions); `tablecmds`,
  `indexcmds` and `partitioncmds` start with a `doc.go`. T2: none deleted (every
  refcover candidate asserts internal state, waits, cancels or publishes ACLs
  directly). Kept in the root: the helper-only `identity_helpers`,
  `vacuum_lifecycle` and `vacuum_truncate` (users are non-free root tests); the
  frame-name `range_partition_publication` moved (its frames are root names,
  unchanged); `index_hash_cancellation` and `vacuum_concurrency` are not free.
  T4: added `enginetest.Comment` (follows the test-file method `Engine.Comment`;
  item 187's `comment_test.go` needs it too). Helper clashes in `core` (item
  184's note): the earlier copies of `deadlock*`, `waitsCommandAsync`,
  `waitsDiagnostic`, `mustLockStatement`, `relationFailReader` and
  `uniqueAsyncUnit`/`uniqueClaimsEmpty`/ `uniqueKeyBucketsEmpty` were deleted in
  favor of the originals that moved with their files; the moved
  `relation_lock_test.go` uses enginetest's `Lock*` instead of declaring its
  own; the copied unique publication fixture dropped its unused `prepare` method
  (it reaches the unexported `Engine.prepareIndexRoots`). Copied into two or
  more destinations, candidates for `enginetest`: `withPublicSchemaUnitOwner`,
  `nullCountUnitDiagnostic`, `testSessionID`, `mustLockStatement`,
  `waitsCommandAsync`, `cancelAtErrCheck`, `sequencePreparedDescription`,
  `btreeWantKeys`, `indexClausesTypes`, `inheritanceUnitParents`,
  `retiredUnitTable`, `operatorFamilyTestRead` and `operatorFamilyUnitSeed`.
  Root helpers left without root users (for item 193's dedupe):
  `arrayUnitInteger`, `indexClausesTypes`, `indexHostFixture`,
  `newPartialUniquePublication`, `operatorFamilyUnitSeed`, `partialUniqueRow`,
  `partitionConstraintUnit`, `retiredRaw`, `retiredRepairSQL`, `retiredRow` and
  `serialLeaf`. The non-free group files (about 1 MB, 65 reach core internals)
  wait for items 193–194.
- 187: - 187: done (`9c2d30c27`, `e01a0f1a9`). Group measured with `group.py`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-187-TYPE-VIEW-ROLE-SCHEMA-OBJECT-DDL-TESTS/`) at
  `13ad970b9`: 321 root files, 129 transitively free (0.90 MB). Moved 116 (290
  tests) with that directory's `mvtest`, replayed on item 186, by statement
  under test: `schemacmds` 30 (schemas, COMMENT, collations, conversions,
  operator, operator class and routine DDL, CREATE TABLESPACE, LOAD,
  foreign-table DDL), `rolecmds` 18, `viewcmds` 16 (views, policies, rule DDL),
  `typecmds` 1 (ALTER TYPE on typed tables) and `core` 41 (row locks, RLS, rule
  expansion, runtime domain checks, TEMP namespace storage, object address, ACL,
  comment, tablespace and extension inquiry functions, aclitem output, foreign
  parallel scans, type catalog descriptions). Deviation: 10 files went to
  `tablecmds`, beside item 186's (ALTER TABLE RENAME, whose executor
  `renameRelationLocked` is an item 196 entry, column-default Restore and CREATE
  TABLE OF). No extension-prefix file is free (all reach `fam:extension`); they
  wait for item 203. T2: none deleted (every refcover candidate asserts
  lock-manager, catalog, storage or role-store state, or runs barriers). Moved
  callers of the root test method `Engine.Comment` use item 186's
  `enginetest.Comment`; the method went with `comment_test.go`. This `mvtest`
  also drops earlier destination copies of helpers that a moving file declares
  (ten row-lock, namespace and tablespace helpers in `core`,
  `renameAuthorityFixture` and `renameWantError` in `tablecmds`). Kept in the
  root: the helper-only `authorization_fixtures`, `default_privileges_helpers`,
  `domain_alter_*` (subtest bodies of `domain_test`), `extension_retirement`,
  `operator_family_fixture`, `rls_base_lock` and `trigger_fixture`, and
  `object_acl_fixture`; deleted the dead helper-only `fdw_parallel_pressure`,
  `routine_builtin_capture` and `routine_program_wait`. Root helpers left
  without root users: `alterSchemaPublishDescriptor`,
  `databaseAdmissionPolicyLocked`, `indexMaintenanceSnapshot`,
  `publicationConflictProvider`, `ruleUnitInt` and
  `typedReview{ACLFixture,TypeACL}`. The 192 non-free group files (66 reach core
  internals, 82 family code, 43 shared or several families, 1 unassigned) wait
  for items 193–194 and 203.
- 193: done (`4d534b352`, `83aca4617`, `71c7556ae`, `3b9051c1b`). Group measured
  with `group.py`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-193-TEST-ACCESSORS-I-RUNTIME-AREAS/`, the item
  184/185 areas and prefixes) at `7177872dd`: 309 root test files, 199 reach
  only core internals (195 build an engine, 4 are pure units), 50 are free and
  60 reach family or shared code (item 194). `closure` now counts 335 reached
  unexported core names (528 in the item text, an earlier tip). T2: the `dead`
  tool deleted 16 root test helpers without users (the leftovers items 184–187
  listed, 7 files emptied); a per-function `refcover` pass over the 227 test
  functions that reach no internal found only tests that assert Go transaction
  refs, provider call counts, plan nodes or retained streams, so no test was
  deleted (0.01 MB, not the estimated 0.1 MB). Routing: the type-checked `acc`
  tool (`c2.sh g1|g2|g3` replays the three commits, then `post.py`) rewrote
  every site in the declarations reachable from the 195 engine-building files:
  318 accessors plus `PLHostBeginQueryRows` by hand, in 19
  `inspect_<area>_test.go` files filed by the member's declaring-file area (core
  members in DDL-area files land in `inspect_table`, `inspect_view`, ...).
  Forms: `<Prefix><Field>` getter, `Set…` setter, `…Ref` for address, compound
  writes and addressable chains, a value getter for fields of unaddressable
  struct values, same-signature wrappers for methods and functions, constants,
  12 type aliases (`SQLCursor`, `PLHost`, `StatementHost`, `SessionHost`, ...;
  deviation: test-only aliases are the only way an external test names an
  unexported type) and 10 `New<Prefix>(<Prefix>Fields{…})` constructors for
  literals with unexported fields. Check: `census -loose` finds no unexported
  core member named outside the inspect files; `closure` puts 178 of the 195
  files in the free bucket directly, and the other 17 only call exported methods
  of unexported types (`replacementAdmission.Lock`,
  `statementHost.AcquireQueryRow`, `cursorRegistry.EndSession`, ...), which an
  external test calls on the values accessors return. Kept with direct access,
  as core internal tests for item 195: `replacement_admission`, `catalog_fence`,
  `expected_inputs`, `fts_query_runtime`. For item 194: run `acc` with that
  item's seeds; it skips `inspect_*` files but does not yet reuse accessors they
  already declare (it reports a scope CLASH), so teach it to reuse an existing
  accessor with the same name and signature first. For item 195: the inspect
  files become core's `export_test.go` (one file or `export_<area>_test.go`),
  and the aliases and constructors go with them.
- 194: not started.
