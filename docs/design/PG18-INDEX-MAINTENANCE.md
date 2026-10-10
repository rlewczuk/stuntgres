# PG18-INDEX-MAINTENANCE

## Supported contract and boundaries

Ordinary `DROP INDEX [IF EXISTS] name [, ...] [CASCADE|RESTRICT]`,
`ALTER INDEX [IF EXISTS] name RENAME TO destination`, `REINDEX INDEX name` and
`REINDEX TABLE name` operate on the existing substantive logical B-trees. Names
use the shared relation namespace and ordinary schema/search-path authority.
Utilities return zero affected rows and no result columns. Describe is
syntax-only, including absent targets and explicit transactions; full-input
parsing precedes all script effects.

There is no new optimizer, physical B-tree, tablespace, PostgreSQL storage
parameter, statistics subsystem, transactional utility implementation or SQL
catalog source. Native read-only execution rejects the canonical utility with
25006 before lookup, ACL checks or waits. Writable explicit native execution
rejects these utilities with 0A000 before lookup/waits. PostgreSQL permits the
corresponding transactional operations; PostgreSQL REINDEX also resolves and
locks a target before its read-only gate. Reference definitions retain these
explicit differences rather than normalizing the oracle.

Valid physical SET/RESET requests fail `index storage parameters are
unsupported`, with no Position or state change. SET validates integer/Boolean
values and duplicates in input order; RESET values fail42601 while unknown and
repeated RESET names remain valid requests followed by the native boundary.
`fillfactor=80.0`, Boolean reloption spellings and ignored PostgreSQL toast
namespace requests are not rejected as malformed options. Missing ALTER IF
EXISTS targets skip semantic option checks after syntax parsing.

REINDEX options use whitespace, not `=`. Their original scalar kinds are retained:
Boolean true/false and integer0/1 are distinct from quoted '0'/'yes'. Unknown
options, including parallel, fail positioned42601; malformed Boolean values
fail42601 without Position. Valid VERBOSE and false CONCURRENTLY syntax fails
`REINDEX options are unsupported`; actual concurrent requests fail
`concurrent index operations are unsupported`. TABLESPACE, global targets and
ALTER COLUMN STATISTICS have their specific positioned0A000 boundaries. None
is silently ignored or converted into a successful rebuild.

After the intentional native read-only/writable-explicit gates, admission calls
the pure validateReindexOptions once over the complete list before target/schema
lookup, kind/ACL checks or waits. The first malformed option wins in input order;
an earlier valid VERBOSE/CONCURRENTLY/TABLESPACE cannot mask a later bad control.
Syntax-only Prepare does not perform this execution validation. The separate
reindexControlError selects only valid capability exclusions at the established
target phase; valid controls still expose missing/kind/MAINTAIN target errors.
Shared direct/held Exec/Query cross-products independently pin full records,
Unicode positions, nil failed outputs and unchanged state. Genuine internal ACL
and held-parent-writer controls assert malformed diagnostics before denial/wait.

## Identity, privileges and acquisition

One `indexMaintenanceGrant` retains resolved ObjectRefs, parent/index resources,
grant-time descriptors, complete missing-object notices and TABLE index-set
observations. No wait retries a replacement name. Whole DROP groups are
preflighted for kind, owner/schema-owner authority and backing dependencies
before any wait. Repeated addresses are deduplicated, not repeatedly removed.

- DROP locks every distinct parent AX in ObjectAddress order, then every selected
  index AX in that order. It requires index ownership or schema ownership.
  Shared relation-drop authority preserves the actual object's owner diagnostic
  when schema-owner fallback does not apply; an index is not named a sequence.
- REINDEX INDEX takes parent Share, then selected index AX. REINDEX TABLE takes
  parent Share, then every implemented simple/expression/UNIQUE index AX. MAINTAIN
  is checked on the parent, with denied diagnostics naming the requested target.
- Index rename takes SUE on the index. The PostgreSQL-compatible table, sequence
  and view rename aliases delegate to shared relation rename under AX. Rename
  requires target ownership and schema CREATE, not schema ownership alone.

These are resources in the existing ticket/queue/timeouts/deadlock/epoch manager,
not a second lock authority. Grants reauthorize the current principal and retain
current same-incarnation descriptors. Execution and publication fence exact
observations. TABLE membership is checked at grant and before publication.
Cancellation, timeout, Reset/replacement and stale-incarnation failures remove
pending/held implicit contributions through the normal statement boundary.
Native scans use parent locks; no PostgreSQL planner index-lock behavior is
invented to make observational lock tests appear identical.

Public privilege controls distinguish real capability boundaries: PostgreSQL
grants schema USAGE before exercising parent MAINTAIN and index ownership;
native has no SQL schema GRANT and reports its earlier schema denial. Native
internal ACL controls grant real schema USAGE, assert complete target-specific
MAINTAIN/owner denials before a held parent writer, and exercise granted rebuilds.
No schema denial is presented as a parent MAINTAIN witness.

## Atomic names and backing constraints

Standalone index rename, unique or ordinary, changes only relation name/revision.
Parent/key/root identities and siblings remain unchanged, while23505 uses the
current index name. Only a genuine UNIQUE attachment also publishes its matching
parent constraint name, retaining both real OIDs. Relation-name collisions42P07
precede parent constraint collisions42710. Direct attached DROP fails2BP01 even
with CASCADE and points to DROP CONSTRAINT; standalone DROP frees enforcement.
Shared root-build validation uses captured current SELECT disclosure and output
environment for CREATE/TYPE/REINDEX; ownership alone does not reveal unreadable
key values. Immutable publication and final catalog/role/session fences remain
the sole installing authority.

Initial backing construction, temporary TYPE validation and owned TYPE descriptor
reconstruction retain the same central DefaultIndexClass as ordinary keys.
Owned reconstruction detaches old keys and reclassifies against the new column
Usage, preserving immutable prior class vectors/roots and existing replacement
constraint/index identity rules. Ordinary explicit-class reconstruction is not
overridden by this backing-only rule. Implicit effective collation inherits actual
column Usage; metadata never fills in an absent retained class.
Shared public identity controls capture pg_class and pg_constraint OIDs
independently; PostgreSQL alone exposes the additional conindid relationship.
Native units assert the actual constraint Index link, including domain/nested
domain and collated text backing maintenance and existing TYPE reconstruction.

`sameIndexKeySemantics` is the sole build-decision equivalence rule. Parent,
ordered stable attributes/direction/null placement, retained derived-provider
identity, actual class/effective collation, ordered INCLUDE attributes/repetitions,
column usages, uniqueness presence, unique attributes and null policy are
significant; a name-only unique wrapper change is not. It preserves the real
root on backing rename without suppressing TYPE/shape/derived rebind work.

## Real rebuild publication

`CatalogStage.indexRebuilds` records selected index addresses and their immutable
observed descriptors. Parent observations and TABLE index-set counts are separate
from the existing parent row-shape rebuild intents. A descriptor-unchanged stage
is real work: no artificial descriptor revision or sibling rebuild flag is used.
All existing publication/replacement callers pass no selected maintenance intent.

The existing catalog publication authority captures committed live parent heads
and generation/end/length tuples, builds selected roots outside Engine.mu using
the shared key evaluator/order provider, validates UNIQUE roots, and performs
final catalog, row-store, operation/authorization and context fences before one
install. Selected INDEX replaces only its root; TABLE replaces all implemented
roots atomically. RowIDs/VersionIDs, rows, stable key attributes and descriptors
are unchanged. Retained predecessor roots are immutable; unselected siblings
retain their newest committed roots. No-index TABLE returns zero plus a NOTICE
without a catalog/root publication.

The Share grant includes a prior writer's committed rows and excludes rolled-back
private writes. Extraction, sorting, unique validation or final veto failures
leave the old catalog, table storage and all roots installed. Query scans alone
cannot establish that a rebuild happened, so internal units inspect fresh root
pointers, complete typed tuples and live version identities directly.

## One coherent metadata projection

`projectIndexMetadata(context, CatalogRead, ObjectRef)` projects both internal
`indexMetadata` and `indexesMetadata`; `indexCatalogMetadata` is the enumeration
owner for later pg_index/pg_indexes inquiry owners. It registers no SQL source,
vector codec, node-tree list API or driver API. Every field and deparse operation
uses one immutable capture, with no current-engine lookup or ACL filtering.
Scalars/vectors and the expression list are detached; immutable analyzed nodes
are shared. Old captures remain valid across rename, drop and replacement.

The pg_index-shaped record includes actual index/parent OID relationships,
natts=keys+INCLUDE and nkeyatts=keys, stable parent attributes/repeats in indkey,
and key-only indclass/indcollation/indoption (DESC1/NULLS_FIRST2). Included payloads
need no ordering provider, including JSON. Actual key collations and central
actual B-tree class identities are projected, not underlying default substitutes. Ordinary
primary/exclusion/clustered/replident/checkxmin flags are false; immediate,
valid, ready and live are true for published implemented indexes. Unique and
NULLS NOT DISTINCT flags come from the retained constraint definition. Predicate
is nil because partial indexes are not implemented.

`indexCatalogKey` follows PostgreSQL ComputeIndexAttrs: peel only top-level
collationExpression wrappers from the original analyzed definition; a column
uses its stable parent attribute and has no indexprs entry, while another node
is the original analyzed expression entry with indkey0. Casts and simplification
are never peeled/evaluated. Native execution/original trees remain unchanged;
key ResultUsage retains the actual full-key collation. This same classification
is used by non-dump indexDefinitionSQL. Thus collated attributes survive earlier
unused-column removal and later name changes without being mislabeled as
expression columns.

pg_indexes-shaped names are current schema/table/index names, tablespace is nil,
and indexdef uses the existing indexDefinitionSQL owner, without a semicolon.
Non-dump expressions use existing pgRenderer, shared catalog-key classification
and integration's pgIndexFunctionExpression grammar (including SQL-special
function forms). Full, column-only and pretty callers share this owner; column
ordinals include payload attributes. The existing reconstructIndexKey/whole-TYPE
definition owner retains explicit nondefault/binary-compatible providers and
suppresses default decorations. Full-key collation compares actual collation to
the classified attribute/expression collation, not type default, and emits it once.
Dump retains scalarRenderer replay and its qualification/quoting/original
definitions/explicit decorations/INCLUDE exactly. No second deparser is added.

## Central provider identity and stored-type distinction

The existing immutable `sqltype.IndexClass` owns released OIDs inside its existing
LookupIndexClass selection. `OID()` exposes actual implemented class identity,
including varchar_ops10044 and pattern4217/4218/4219. Metadata consumes retained
key.class identity directly. BindOrdering preserves its existing admission and
diagnostics, but DefaultBtreeOpclass obtains the default identity from
DefaultIndexClass rather than a parallel constant authority. Scalar BindComparison
is unchanged and has no ordering identity.

Domains recursively use their real base provider. Registered enum_ops10069 uses
BaseType().IsEnum() and the existing declaration-order comparator, preserving
previously implemented enum/domain/nested-domain indexes after integration's
ID-only class consumer had excluded them. No new input descriptor or comparator
is introduced. The 21 default classes cover the original scalar providers;
explicit compatible/pattern classes retain their actual identity and semantics.
Unsupported arrays/void/JSON and invalid usages advertise no ordering identity.
Diagnostic-only unimplemented classes expose no implemented identity.

An ordering provider is not a stored declaration capability. Internal Char and
Regclass ordering remain admitted, while native bare stored declarations are
excluded and Domain rejects Char bases. The public provider oracle retains all
PG class/domain-wrapper assertions in an explicit PostgreSQL-only control after
shared ordinary maintenance setup. Native units assert every admitted central
provider and all internally valid domain/nested-domain/enum identities without
expanding the stored type surface.

## Public definitions and executable checks

Untagged shared public definitions under reference/index_maintenance*_test.go
cover lifecycle, UNIQUE enforcement, full diagnostics/notices/options/precedence,
held preparation/admission/routes, privileges, real lock waits/cancellation,
retained rows, native controls/foreign restore, metadata/provider controls and
actual populated-table feature smoke, including explicit compatible/pattern
classes, repeated INCLUDE/JSON payload and enum/nested-domain maintenance. Internal
TestIndexMaintenance units cover real root/membership/publication/grant, complete
key/payload vectors and decoration equivalence; TestOrderingBtreeIdentity covers
independently pinned default/actual class identities and original enum consumers.

Root runs generation/formatting and these selective gates; each quickcheck has
one 900-second deadline. Commands describe required verification, not a claim that
this document's commands were already exercised.

```sh
make quickcheck PACKAGES=./internal/parser RUN='^TestIndexMaintenance' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^Test(OrderingBtreeIdentity|IndexClauses)' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestIndexMaintenance' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(IndexMaintenance|IndexClauses|BtreeIndex|ExpressionIndex|Unique|DeparseRelations)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(IndexMaintenance|IndexClauses|BtreeIndex|ExpressionIndex|Unique|DeparseRelations)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders|UniqueDomains)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexMaintenance(Smoke|ControlsSmoke|DecoratedLifecycle|EnumProviders|UniqueDomains)' VERBOSE=0
```

Smoke checks actual new rename/INDEX/TABLE rebuild/drop behavior, current names,
exact ordered rows/duplicate bags, backing enforcement/dependencies, explicit
provider semantics and repeated/non-orderable payloads, unavailable physical
options without changes, and Dump/Restore/DML/control invariants.
Ordinary quickcheck and the root's post-integration fullcheck remain required;
fullcheck has one 1800-second deadline. Readiness is not verification evidence.

## PostgreSQL18 sources

- [ALTER INDEX](https://www.postgresql.org/docs/18/sql-alterindex.html),
  [DROP INDEX](https://www.postgresql.org/docs/18/sql-dropindex.html),
  [REINDEX](https://www.postgresql.org/docs/18/sql-reindex.html).
- [pg_index](https://www.postgresql.org/docs/18/catalog-pg-index.html) and
  [pg_indexes](https://www.postgresql.org/docs/18/view-pg-indexes.html).
- [REL_18_0 indexcmds.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/commands/indexcmds.c),
  ComputeIndexAttrs and ReindexIndex/ReindexTable.
- [REL_18_0 reloptions.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/access/common/reloptions.c),
  transformRelOptions and parseRelOptions; [guc.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/misc/guc.c), parse_int.
- [REL_18_0 pg_opclass.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_opclass.dat)
  and the independent pinned PostgreSQL18.0 reference oracle identities.
