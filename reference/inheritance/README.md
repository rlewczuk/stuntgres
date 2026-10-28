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
