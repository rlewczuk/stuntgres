# reference/inheritance

Tables defined from other relations or types: inheritance (creation, queries,
DML, ALTER recursion, INHERIT/NO INHERIT maintenance), typed tables (`OF type`)
and CREATE TABLE LIKE. This package also owns relation inheritance links in
`pg_inherits`, extended-statistics definitions and restricted typed data in
`pg_statistic_ext`/`pg_statistic_ext_data`, and relation-owned statistics
declaration, collection and native-control lifecycles, including hierarchical
collection. Related families: table core, constraints, foreign keys and foreign
tables in [reference/tables](../tables/README.md); partitioning in
[reference/partitions](../partitions/README.md); other relation and type catalogs
in [reference/catalogs](../catalogs/README.md); composite types in
`reference/types`; inherited row-lock rechecks in `reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared table rules in
[reference/tables](../tables/README.md#testing). `TestMain` uses default options.

- Scenario helpers: `inheritanceScenario` (`inheritance_helpers_test.go`) and
  `typedTableScenario` (`typed_helpers_test.go`).
- Roles named by a replayed native Dump are passed as `inheritanceScenario`
  replay roles so they exist on PostgreSQL before the replay.
- Catalog scenarios retain the shared catalog identity, cleanup and native-control
  rules in [reference/catalogs](../catalogs/README.md#testing).
- `pg_inherits_helpers_test.go` owns complete edge bags, independent identity
  lookup and held metadata reads. Statistics scenario and assertion helpers stay
  with their coherent families (`statistics_object_helpers_test.go`,
  `statistics_expression_helpers_test.go`, the ndistinct/dependencies/MCV family
  files and `pg_statistic_ext*_test.go`); shared catalog helpers remain in
  `reference/fixture`. Direct, fresh-prepared, held, native-control and actual
  PostgreSQL dump-replay routes use the same original assertions.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- CREATE TABLE LIKE: `RUN='^TestReferenceTableLike'`
- Typed tables: `RUN='^TestReferenceTypedTable'`
- Inheritance (creation and lifecycle; selects every `Inheritance*` family): `RUN='^TestReferenceInheritance'`
- Inherited queries: `RUN='^TestReferenceInheritanceQuery'`
- Inherited DML: `RUN='^TestReferenceInheritanceDML'`
- Inherited ALTER: `RUN='^TestReferenceInheritanceAlter'`
- INHERIT/NO INHERIT maintenance: `RUN='^TestReferenceInheritanceMaint'`
- Relation inheritance catalog links: `RUN='^TestReferencePGInherits'`
- Inherited ALTER CHECKs share native/PostgreSQL success for `NOT ENFORCED`,
  asserting false enforcement/validity on both parent and child while retaining
  enforced sibling checks and final rows: `RUN='^TestReferenceInheritanceAlterChecks$'`.

Engine and parser units: `make check RUN='^Test(Inheritance|Typed|TableLike)'`.

Extended-statistics definition reads cover exact schema and row/array links,
PUBLIC access and current ACL revocation, ordinary-role mutation diagnostics,
and native controls without privileged PostgreSQL catalog writes. Select the
family in either native or real-reference mode with
`make check RUN='^TestReferencePGStatisticExt'` (add `REFERENCE=1` for PostgreSQL).
Statistics lifecycle scenarios include direct/prepared relation-kind admission,
virtual-column rejection, creation/removal, namespace/temp lookup, NORMAL/AUTO
cleanup, malformed source positions, bulk ownership and transaction lifecycle,
held conditional notices, typed NULLs and native definition replay with exact
paired-owner guards. Native-only phases cover pre-canceled utilities and gated
external I/O with live statistics definitions. Their family
selector is `make check RUN='^TestReferenceStatisticsObject'` in either mode.
Collected statistics remain independently owned.
Statistics adapter metadata is asserted independently: native database type names
are exact, while lib/pq returns an empty name for the three unknown statistics
OIDs. Catalog type identities remain exact; numeric cross-adapter OID witnesses
use explicit BIGINT casts rather than comparing driver-specific raw carriers.
Multivariate ndistinct scenarios cover real NULL-inclusive collection,
canonical subsets, target/column selection, separate inheritance flavors,
diagnostics, transactional publication, cancellation and lossless native state.
Retained-TEMP control phases keep the physical session alive through Restore
and Reset, read TEMP and imported persistent payloads through DATA, and retain
old rows/metadata independently of replacement type generations.
Native-control phases replay actual emitted definition/target SQL on PostgreSQL,
which ignores native state comments. Select the family in either mode with
`make check RUN='^TestReferenceStatisticsNDistinct'` (add `REFERENCE=1`).

Measured functional-dependency scenarios share the same direct/prepared helpers:
NULL-inclusive row-weighted strengths, all determinant widths, sparse stable
attributes and signed expression dimensions. Refresh, transaction visibility,
authorization warning/error distinction, genuine typed results and stale native
fractional/analyzed-NULL replay are covered by
`make check RUN='^TestReferenceStatisticsDependencies'` (add `REFERENCE=1`).
Native values describe complete logical populations, not physical TOAST-width
filtering or server sample emulation; substantive query rows remain exact.

Multivariate MCV scenarios share the direct/prepared fixture and independently
decode public `pg_mcv_list_send` BYTEA images using released physical layouts,
never production codecs. They assert full-population joint/marginal frequencies,
SQL NULL flags, unrenormalized target caps, typed dictionaries and logical OIDs.
Native physical images are not COPY-wire values or cross-backend byte goldens.
Array setup uses each backend's existing usable default-class authority rather
than shadowing a complete provider with incomplete exact metadata. Explicit
rejected-provider phases distinguish accepted class metadata from executable
statistics admission, while retaining native positive physical-codec assertions.
Remint witnesses combine reachable domain-over-array and scalar enum-domain
dimensions without introducing new column constructors.
Reachable native XID/CID/vector phases restore deliberately stale joint facts into
independent destinations and then distinguish them from real reanalysis. Public
vector assertions include exact extents, offsets, element OIDs and element words,
alongside NULL markers and full-population marginal baselines.
Authority, transaction visibility, real blocked cancellation, stale semantic
Dump/Restore, destination reminting and genuine retained TEMP facts use public
APIs. Select the family with `make check RUN='^TestReferenceStatisticsMCV'`
(add `REFERENCE=1` for the pinned oracle).

Expression-statistics scenarios use the same public direct/prepared fixture and
the genuine `pg_statistic_ext_data.stxdexpr` row array: subscript/field access,
typed INT4/TEXT MCV slots, FLOAT4 frequencies, NULL fractions/distinct encoding,
all-NULL versus nonstrict output, and ordered histogram bounds. Definitions
cover expression-only `{e}`, explicit `{d,e}`, default `{d,f,m,e}`, source-order
names, width limits, nonimmutable functions and conditional-create identity.
Dependency scenarios assert the full NORMAL/AUTO edge bag, current-name deparse,
stable identity, selected/unselected column retirement and routine RESTRICT/CASCADE.
Full diagnostics cover prohibited nodes, name resolution and nonowner admission.
The mixed/dual-expression witnesses require real NULL-inclusive joint ndistinct;
the dependencies and MCV families also cover these dimensions.
Selected-column ANALYZE checks simple statistics keys, while expression-only
and mixed definitions evaluate their expressions from full captured rows even
when expression input columns are not selected for scalar collection.
Native-control phases retain deliberately stale expression summaries through
Dump/Reset/Restore, sparse input layouts, mixed owners and destination reminting;
malformed/canceled imports retain destination data and old facts. PostgreSQL
replays actual emitted declaration SQL, not native semantic state comments.
Equality-only XID expression collection checks the authentic MCV equality
operator, typed values and continued readability of unrelated collected objects;
single-expression projection does not impose a delayed btree requirement.
All-NULL INT4 expression width is four, while unknown all-NULL TEXT width is
zero; native replay retains both stale widths after source rows change.
Hierarchy coverage attaches an existing child with different physical attribute
positions, checks separate parent-local/parent-inherited/child-local expression
distributions and real joint ndistinct, and requires inherited-only data for a
partitioned parent. Native replay preserves stale inherited expression and joint
facts despite the restored tables containing a different current distribution.
Select every expression definition in both modes:
`make check RUN='^TestReferenceStatisticsExpression'` (add `REFERENCE=1`).

DATA-specific catalog scenarios independently assert all six fields (including
empty-result metadata), relation/row/array identity, genuine analyzed-object
population and exact missing-column diagnostics. The direct/fresh-prepared and
retained-prepared ACL matrix proves that owning statistics is insufficient,
explicit catalog SELECT permits other-owner data, and revoke/regrant affects
the same held statements, including WHERE FALSE. Its feature smoke retains an
unbuilt definition while collecting an actual count2 payload. Select these
witnesses with `make check RUN='^TestReferencePGStatisticExtData'` (add
`REFERENCE=1`); the existing NDISTINCT family still owns collector lifecycle,
transactions, hierarchy, cancellation and native controls.

Column statistics restoration owns the released heterogeneous variadic restore,
typed named clear, ordered partial-restore warnings, selected transactional
deltas, target authority and locks, `pg_stats` SELECT/RLS visibility, actual
planner estimates and typed native semantic replay. Expectations derive from
released REL_18_6 `attribute_stats.c`/`stat_utils.c`, not native output. Native
control phases preserve stale values after DML and use independent Driver
destinations; PostgreSQL phases exercise the same SQL assertions.
Select the family with `make check RUN='^TestReferenceAttributeStatistics'`
(add `REFERENCE=1` for the pinned oracle). The standalone new-behavior smoke is
`make check RUN='^TestReferenceAttributeStatisticsFeatureSmoke$'`, in both modes.
`TestReferenceAttributeStatisticsViewDefinition` independently checks exact
pinned-18.6 default/pretty `pg_stats` definitions with quote-all identifiers
both off and on, through direct and prepared execution. Select it in both modes
with `make check RUN='^TestReferenceAttributeStatisticsViewDefinition$'`.
The authority matrix includes database-owner admission on another owner's table,
held MAINTAIN revocation/regrant, and parent-authorized index targets. Transaction
witnesses observe private/peer EXPLAIN estimates and a genuinely blocked,
selected-column commit merge. Catalog metadata is checked before iteration for
direct/prepared ANYARRAY reads and empty sources. Native lifetime phases retain
TEMP typed distributions and old ANYARRAY cursors across Reset/Restore, prove
capture isolation while writer/reader callbacks are gated, and reject malformed
kind/type/slot-count records and input I/O failure without replacing destination
sentinels. These are assertions in the same family, not separate test modes.
Authority fixtures explicitly grant schema USAGE before testing table MAINTAIN
and revoke those grants during per-case cleanup, including failed cases. The
external-cancellation witness owns a disposable waiter connection; subsequent
visibility and merge assertions use the original uncanceled session.
Failed attribute-statistics steps report their newly delivered notice records
alongside the unchanged row/type assertions, so an unexpected false restore
exposes its precise warning instead of inviting a guessed implementation change.
The binding family fixes runtime-versus-resolution phases for named explicit
VARIADIC arrays and bare ANY parameters, and checks wrong-typed `attnum` warnings
with both missing-selector rejection and successful partial restoration.
Authority cases include missing schemas, dropped and invalid numeric attributes,
materialized/foreign/partitioned restore-readback-clear, and materialized-index
parent diagnostics. Planner coverage exercises non-MCV equality after explicit
zero distinctness. Native corruptions replace the original record (never append
a duplicate), including scalar/unequatable-element histograms and wrong retained
typmod/collation; every rejection preserves destination statistics and sentinel.

Relation statistics restoration has eight independent shared scenarios:
`TestReferenceRelationStatisticsRestoreClear`, `Validation`,
`BindingDiagnostics`, `TypedLifecycle`, `TargetsPrivileges`, `Transactions`,
`ResetOrdering` and `NativeControls` (each suffix shares the same full prefix). They pin exact
four-field estimates, BOOL/VOID metadata, warning order and partial false,
required selectors/typed ANY failures, live MAINTAIN/database-owner/index-parent
authority, locks/cancellation, transaction/savepoint/2PC publication and physical
row independence. Native phases prove independent-driver symbolic replay,
stable redump, malformed/canceled replacement atomicity, TEMP retention and old
captured catalog rows. Expectations derive from REL_18_6 relation_stats.c and
stat_utils.c; no expected value is learned from native output.
ResetOrdering covers imported→new-file TRUNCATE and TRUNCATE→restore/clear,
same-subtransaction repeat/created-here preservation, own/peer readback, nested
savepoint allocation, RELEASE/ROLLBACK and both prepared completions. Ordinary
ALTER TYPE rewrites exercise both operation orders and rewrite→restore→in-place
TRUNCATE. A real PL/pgSQL exception block exercises an internal entry captured
after enclosing statement writes. Physical rows remain independently asserted.
Validation rejects Unicode confusables in schema/version keys with full
warning/error diagnostics under C/UTF8.
The maintenance companion rolls back selected ANALYZE facts to a saved reset
journal, then restores pages only: the measured tuple estimate must remain2,
not resurrect the old imported-1. A genuine later tuple restore still wins.
Existing `TestReferenceAnalyzeTruncateOrdering` and
`TestReferenceAnalyzeTruncateRewriteOrdering` retain their independent native
maintenance versus PostgreSQL physical-rewrite expectations unchanged; run both
in native and `REFERENCE=1` modes when changing these shared journal seams.

Select with `make check RUN='^TestReferenceRelationStatistics'` and again with
`REFERENCE=1`. Focused feature smoke is
`make check RUN='^(TestReferenceRelationStatisticsRestoreClear|TestReferenceRelationStatisticsValidation|TestReferenceRelationStatisticsResetOrdering|TestReferenceRelationStatisticsNativeControls)$'`.
