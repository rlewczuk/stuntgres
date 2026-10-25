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
  `Engine.stageExtensionLocked` or `extensionHost` directly (about 1.3 MB)
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
- 184: not started.
- 185: not started.
- 186: not started.
- 187: not started.
- 193: not started.
- 194: not started.
