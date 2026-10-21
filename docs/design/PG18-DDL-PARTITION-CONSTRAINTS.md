# PG18-DDL-PARTITION-CONSTRAINTS

## Selected contract and boundaries

RANGE UNIQUE/PRIMARY KEY and unique indexes cover every simple partition key at
that level, with matching effective collation/equality capability. INCLUDE is not
coverage; expression partition keys reject these constraints. Independent leaf
keys remain independent. Virtual indexes have no heap/index storage. Physical
leaves enforce real cloned keys and primary NOT NULL definitions. Nonunique virtual
index creation, virtual REINDEX, generated/identity/serial combinations,
hierarchy UPDATE/DELETE/row marks/LOCK and unrelated ALTER remain bounded out.

Constraint/index OIDs, stable attribute numbers and immediate-parent edges are
actual definitions, not a synthetic registry. Constraint ancestry uses the actual
parent constraint OID; index ancestry uses its immediate parent index address.
Mapped cooked keys/predicates retain type/function/operator/collation providers.
Compatibility compares key order, INCLUDE, method, cooked expression/predicate,
effective collation/operator family and NULL policy; sort/null ordering is not an
attachment incompatibility. Backed parents require actual compatible backed child
constraints. Bare independently owned child indexes are not silently converted
into constraint providers.

## Creation, attachment and validity

Recursive creation reuses compatible child indexes, including incomplete virtual
children without filling their missing descendants. ON ONLY leaves existing
children unattached. Future partitions clone supported keys even under incomplete
ONLY parents. Validity derives from all actual immediate child indexes and their
validity, then propagates upward. No import/attachment path force-valids a tree.

ALTER INDEX ATTACH validates membership before equivalence, permits repeat of the
same actual edge, rejects a second index for the same immediate partition and
preserves OIDs/names. Actual table-before-index locks and exact predecessor,
authority and epoch fences remain publication authorities. Metadata-only changes
reuse the latest unchanged-semantic physical key root only when the owning storage
incarnation is unchanged; virtual descriptors never require or create physical
storage. Shared overlay preparation rebuilds physical baselines for staged storage
replacements even when key definitions and row attributes are unchanged. Incoming
TRUNCATE therefore stages an empty private key root without changing definitions
or mutating the retained predecessor. Existing generation, lock, cancellation and
atomic publication fences remain the authorities; no TRUNCATE-specific index path
is introduced.

## Selected lifecycle and atomicity

Selected attach/index-drop/key-constraint-drop operations use existing private
catalog overlays and immutable physical roots. Savepoint copy/rollback/commit
preserve both metadata and actual key enforcement. Selected multi-action key DROP
requires an actual key constraint; a later missing name delegates to ordinary
42704 diagnostics, with no earlier action published. Existing non-key actions do
not acquire a new hierarchy exception.

Attached child indexes require their parent. A standalone parent with a local
constraint-backed child rejects RESTRICT with the actual local dependent; CASCADE
removes that local constraint/index closure. Dropping the local attached constraint
itself rejects both RESTRICT and CASCADE. Root key DROP removes inherited backing
and preserves independent keys and NOT NULL definitions. Failed recursive builds,
late dependency failures, canceled admitted waits and final publication vetoes
retain the old graph, row bags and physical roots.

## Catalog and logical replacement

pg_constraint appends conindid OID, conparentid OID, conislocal BOOL, coninhcount
INT2. pg_class appends relispartition BOOL; virtual index kind is internal CHAR I.
The readonly pg_index schema is exactly indexrelid OID, indrelid OID, indisunique
BOOL, indisprimary BOOL, indisvalid BOOL, indisready BOOL, indislive BOOL and
indnullsnotdistinct BOOL. pg_depend exposes genuine PRIMARY(P)/SECONDARY(S) index
and inherited key ancestry. CHECK/NOT NULL parent IDs remain zero; immediate
membership determines inheritance count. One existing NOT NULL definition flag
preserves actual explicit redeclaration provenance. Repeated inherited CHECK is
nonlocal; explicitly redeclared NOT NULL is local/count1. FK backing identifies the
actual referenced supporting index. Broad PostgreSQL catalog fields are not implied.

Dump uses collision-safe temporary key names through the existing naming authority,
reconciles generated names, then restores final names and manual edges in dependency
order. Native Restore remints references against retained live TEMP objects,
preserves renamed/manual/partial/nested graphs and deterministic redump. Import
uses the same representability rule as Dump: ready/live virtual indexes may be
invalid only when actual child topology explains the invalidity. Invalid physical,
nonready and nonlive indexes remain refused. Replacement failure/cancellation
preserves the old tree and retained readers. Native controls are not PG control APIs.

Automatic clone naming and dump reconciliation share one derived declaration
order. Inline inherited keys precede local inline keys; inherited order follows
the immediate parent's declarations recursively, using actual constraint-parent
identities. Local inline keys retain their stored declaration order. Separately
emitted standalone/prebuilt indexes follow the existing sparse source priority,
with current OID fallback. INCLUDE-backed USING INDEX declarations stay in that
separate category. This order does not rewrite stored constraint slices, remint
unrelated objects or introduce persistent ranks. In particular, a mid-level local
NND declaration cannot take the inherited DISTINCT key's unsuffixed descendant
name merely because native binding allocated the local backing first.

Reconciliation reserves the entire generated sibling-name sequence before
emitting any rename or releasing any generated name. Thus two same-stem keys
retain their distinct unsuffixed/suffixed creation names even after the first
rename frees the unsuffixed stem. Bounded dump-private records use the existing
generated-name authority and reserved temporary targets; definitions, NULL policy,
immediate ancestry and current-name diagnostics are not inferred from key columns.

The captured declaration graph breaks ready-node ties by source OID allocation
order, preserving CHECK/provider dependency diagnostics, same-name Restore
identities and order-sensitive generated array names. Global alphabetical ties
are unsafe: whole-project verification65 exposed regressions in those contracts.
Native replacement retains a sparse immutable catalog map from actually remapped
declaration addresses to original source priorities. Noncolliding declarations
keep their actual OIDs and ordinary catalogs allocate no provenance map. The
descriptor remap captures priority after type remapping has completed its mapping;
an existing source priority wins over its reminted address on another replacement.
Catalog clones copy the map only when present; stage reads retain the immutable
base map unless removals require a filtered copy. Publication/private removals
discard retired entries; clone filtering omits absent descriptors. Allocators remain
monotonic, so newly created objects cannot reuse retired source ranks. Fresh
Restore reconstructs ranks from the isolated replay candidate, without hidden SQL
metadata. Dependency edges remain the sole readiness authority, including
typed-table and index/provider prerequisites. No post-sort, alternate serializer,
global identity remap or lexical-name convention is introduced.

Descriptor addresses and owned definition OIDs share one identity space.
Before allocating collision replacement addresses, Restore seeds its existing
reservation set with every imported descriptor and owned definition, including
view rules, policies, NOT NULL, CHECK, UNIQUE, exclusion, FK, defaults and generated
definitions. The same reservation traversal serves imported definition remapping
and retained TEMP ownership. A reminted backing must not steal a noncolliding
child constraint's OID and force a secondary identity change. No extra reservation
map, per-candidate catalog scan or persistent metadata is required.

## Stored backing regclass references and replay-stage names

Logical replay validates virtual constraint-backed dependencies and renders their
captured identities with names usable at each emission stage. Existing naming,
reservation, declaration order and diagnostic contracts remain unchanged.

`dumpPredecessor` in `internal/engine/dump_resolver.go` (formerly `view_dump.go`) must accept legitimate
UNIQUE/PRIMARY KEY backings owned by exactly an ordinary or partitioned table,
matching `unique.go:28`. Materialized-view standalone indexes are not constraint
owners. Keep actual descriptor resolution, reciprocal `unique.Index` and
`backingConstraintPresent`, namespace/owner equality, persistent lifetime and the
existing INCLUDE-backed USING INDEX graph-node distinction. Do not exempt virtual
indexes from validation or manufacture scalar DROP dependencies for opaque arrays.
[PostgreSQL object identifier types](https://www.postgresql.org/docs/18/datatype-oid.html)
ground stored regclass identity through rename; the pinned18.0 fixture supplies
runtime expectations, not local development sources.

Creation readiness alone is insufficient. `control.go:96–98` captures the original
immutable catalog and creates the existing private naming catalog. The private
copy substitutes only reserved temporary constraint/backing names, preserving
addresses, epochs, owners, namespaces, source priority and the captured data
frontier. All mixed graph declarations emit before the global final rename pass
(`control.go:770–1025,1026–1036`). Each table CREATE is immediately followed by
inherited generated-to-temporary reconciliation; descendants then see the existing
temporary names. Final renames precede manual attachments and all deferred defaults,
rows, materialized snapshots, policies and security flags.

Use the catalog whose names exist **when the resulting SQL is replayed**, not when
its text is captured:

| Emission stage | Existing consumers | Required naming catalog |
|---|---|---|
| Mixed graph, before global final renames | Ordinary/materialized view definitions; inline ordinary/root/partition-child defaults and CHECKs | Private captured `catalog` |
| After global final renames | Deferred defaults; ordinary stored row literals; materialized snapshot row literals; stored policy expressions | Captured `originalCatalog` |
| After rows, table-only flags | RLS ENABLE/FORCE groups | Existing catalog unchanged; no backing-name expressions |

`control.go` passes the captured catalogs through existing `selectSQL`,
`emitDeferredDefaults`, `emitRows`, `emitMaterializedSnapshot` and `policyDumpGroups`
interfaces. `rls_base_dump.go` emits only table names and flags and is intentionally
unchanged. No renderer, provider or data capture is reconstructed from the live
catalog after unlock.

Inline consumers **can be emitted before a referenced backing's final rename**.
That includes inherited defaults and CHECKs. `control.go:534` excludes inheritance
participants from default deferral even when their expression is otherwise eligible.
`partition_dump.go:115–199` compares and renders child/parent defaults and CHECKs
with the private catalog: equal inherited definitions are omitted and inherited
from the already-bound parent; differing local defaults/CHECKs appear in PARTITION
OF before final renames. Ordinary INHERITS similarly inherits already-bound parent
definitions while emitting only local declarations. All need temporary replay names,
not final-name readiness at that stage. Opaque array payloads beneath a BOOL default
remain mandatory inline prerequisites when there is no genuine scalar provider
capture; do not widen default eligibility to hide this timing issue.

Thus widening the owner-kind guard alone leaves two additional name-stage defects:
views refer to final names before they exist, and late literals/policies refer to
temporary names after they cease to exist. Stage-aware catalog arguments fix these
without moving any consumer or rename, changing the source-priority graph, extending
ALTER admission, or adding a second identity/readiness registry. Preserve existing
self/cycle behavior: an index not yet created during its own owner CREATE cannot be
made ready by suppressing a predecessor. Witnesses use actual prior-created provider
trees and separate supported consumer definitions, not invented self construction.

The PARTITION OF grammar admits the existing `primary_constraint` production as
`TableElement.Unique`. Existing child unique binding, NOT NULL and index staging
handle local PRIMARY KEY declarations and real descendant enforcement; no second
AST or binder is introduced.

Portable stored-view rendering uses the captured datum's owning typed cast as its
canonical representation. An identical source cast over a captured regclass array
or domain literal is not wrapped again when its complete type/typmod/collation usage
matches the analyzed target. Child rendering still advances its ordinal normally.
NULL arrays retain their owning type; different-target, uncaptured and numeric
coercion casts and the PostgreSQL deparse/pretty branch remain unchanged.

New shared public definitions belong in
`reference/tables/partition_constraints_regclass_references_test.go`:
`TestReferencePartitionConstraintsRegclassReferences` and
`TestReferencePartitionConstraintsRegclassControls`. They independently cover
UNIQUE and PRIMARY KEY root/intermediate virtual backings, scalar/opaque array
views/defaults/CHECKs bound before backing rename, an inline opaque BOOL default,
inherited inline defaults/CHECKs, and actual late stored scalar/opaque data.
Policy/materialized coexistence preserves established boundaries: native policy
expressions still reject execution with0A000, while their stored definitions and
backing identities persist. PostgreSQL proves actual policy filtering. Native
Restore retains materialized snapshot rows/population; PostgreSQL ignores native
snapshot comments and explicit REFRESH validates the emitted view definition.

Expected assertions are current final-name values and equality to the real backing
identity, correct inline/default/CHECK behavior, raw metadata, exact immediate
constraint/index ancestry and kind/validity, two byte-identical native redumps, and
unmodified emitted PostgreSQL replay. Retained TEMP collisions preserve actual
reservation/selective reminting; all noncolliding identities and source/retained
snapshots remain unchanged. Supplemental helper units may test malformed reciprocal
owners and early/late renderer names using real captured descriptors; they do not
replace public SQL witnesses. Existing ordinary/prebuilt, source-priority, F1
collision/nested and regclass control goldens must remain full-strength.

Root proof commands (900-second external deadline each):

```sh
make check REFERENCE=1 RUN='^TestReferencePartitionConstraintsRegclassReferences/stored_virtual_backings/(direct|prepared)/postgres'
make check RUN='^TestReferencePartitionConstraintsRegclassReferences'
# Only after recorded oracle and meaningful native red: source authorization.
make check RUN='^TestReferencePartitionConstraintsRegclass(References|Controls)'
make check REFERENCE=1 RUN='^TestReferencePartitionConstraintsRegclass(References|Controls)'
make check RACE=1 RUN='^TestReferencePartitionConstraintsRegclass(References|Controls)'
```

Root must extend selectors for any actual additional definitions and preserve the
full affected regression inventory in living plan §25. The actual public executable
`local://partition-constraints-smoke.go` must add this new behavior without dropping
its baseline or prior F1 goldens. From the assigned worktree, after root materializes
the session source and provisions pinnedPG18.0 UTF8/C with existing owners:

```sh
timeout 900s go run /tmp/pg18-partition-constraints-smoke.go -mode native -dump /tmp/partition-constraints-smoke.sql
timeout 900s go run /tmp/pg18-partition-constraints-smoke.go -mode replay -dsn "$PG18_DSN" -dump /tmp/partition-constraints-smoke.sql
```

Native success requires real Dump/Restore and canonical redumps plus current-name
identity/default/CHECK/view/late-data assertions, not baseline acceptance alone.
Replay executes the unmodified actual stream and independently checks those
references and new omitted-default inserts, preserving existing metadata, duplicate
and future-clone observations. Success banners remain `partition constraints native
smoke passed` and `partition constraints PostgreSQL replay smoke passed` only after
all old and new assertions. Record actual results separately from these instructions.

## Concrete acceptance witnesses

- Coverage, typed descendant admission, INCLUDE/collation and local independence:
  TestReferencePartitionConstraintsCoverage/CoveragePrecedence; compound ordinary
  binding precedence: TestReferencePartitionConstraintsCompoundAdmission.
- Immediate backing/constraint/P-S identities, future clones and renamed physical
  diagnostics: TestReferencePartitionConstraintsClones; exact nonunique metadata
  and incomplete recursive reuse: NonuniqueCatalog/IncompleteReuse.
- Root/intermediate/direct leaf keys, NULL policies, complete late-failure bags:
  Values/NestedValues/TypedEquality; held metadata: PreparedMetadata.
- Actual nested completion, repeat, future ONLY clones, mismatch admission:
  Attachment/AttachmentDiagnostics/AttachmentEquivalence; private immutable cooked
  expression/method candidates and compound membership precedence:
  TestPartitionConstraintsStructuralEquivalence. These candidates do not enable
  unsupported UNIQUE expression-index SQL.
- Missing/schema/kind/current-role held authority: Admission/HeldCurrentRole;
  disclosure/RLS: Authorization/Privacy.
- Dependencies, local CASCADE closure, multi-action atomicity and overlays:
  Drops/DropPrecedence/Transactions/Atomicity; private DropClosure/TransactionOverlay.
- Genuine admitted cancellation and healthy retries: Cancellation/IndexCancellation
  and TestPartitionConstraintsAdmittedCancellation; measured late veto:
  FinalPublicationCancellation/DropFinalPublicationCancellation; actual identity,
  role and epoch replacement fences: PublicationFences. RecursiveBuildFailure
  checks no leaked cold roots; PreservesNewestPhysicalRoot checks metadata-only
  publication retains actual newer storage/key roots.
  Unchanged TestTruncateIndexes and TestReferenceTruncateAtomicity/AtomicityIndexes
  supplement these with storage-only replacement: empty private/live roots,
  retained old entries, successful first post-TRUNCATE insert, subsequent
  unique/exclusion conflict, rollback/savepoint behavior and stable definition IDs.
- Renamed/manual/partial/nested graphs, cross-level swapped names, retained TEMP
  collisions, reminted edges/local provenance, deterministic cycles and retained
  cursors: Controls/ControlCollision and DefinitionRemapping/ReplayNames.
  OwnedProviderReplay exercises actor-owned enum-backed nested keys and an owned
  regtype default through actual Dump/Restore and a future clone, checking current
  provider identity as well as equality. Actors are provisioned by ordinary live
  SQL; Restore does not import role definitions, and failed role import preserves
  live authority. IncompleteReplacementAdmission retains incomplete virtual imports
  while refusing physical/ready/live invalid states.
  DumpLogicalOrder independently specifies source order before/after root-only
  remint; DumpPriorityLifecycle checks nil ordinary provenance, immutable cloning,
  stage/private removal and dependency precedence; DumpCollisionProvenance
  exercises actual TEMP-collision remapping twice with unchanged unrelated IDs
  and deliberately reverse-alphabetical automatic/manual names.
  MultiKeyCollision adds two inherited same-stem keys with different NULL policies,
  an actual selective earlier-backing TEMP collision, exact identities/current
  diagnostics, two byte-canonical redumps and future clones. MultiKeyNestedOrder
  independently pins inherited DISTINCT before local NND on a nested parent.
  DumpMultiKeyNames supplements these with captured descriptor remapping and whole
  sibling reservation; DumpIndexDeclarationOrder checks inherited/local/prebuilt
  categories, sparse standalone priority and unchanged captured constraint slices.
  DumpCollisionDefinitionReservation places the real collision allocator at an
  imported leaf constraint's OID, then checks selective backing remap, unchanged
  parent/child constraint identities, synchronized backing/ancestry and immutable
  source/retained TEMP authority.
- Ordinary typed bounds/grammar/demand precedence and unchanged Finish/row-storage
  publication remain existing RANGE authorities: TestReferenceRangePartitionFailureOrder,
  TestRangePartitionPublicationCancelAtMultiLeafApply,
  TestRangePartitionPublicationResetDuringBoundDemand and
  TestRangePartitionPublicationMultiLeafWriteStageSavepoints. They supplement,
  rather than replace, selected key/graph publication witnesses.

Names above identify executable assertions, not a claim that all groups have passed.
The root owns selected native/real/race proof and independent review. Root reported
actual standalone native smoke and actual unmodified emitted PostgreSQL18.0 UTF8/C
replay passing (tests27), then authorized this documentation cutover. Source is
local://partition-constraints-smoke.go; launch and selected proof instructions are
in [TESTING](../../reference/tables/README.md#partitioned-constraints--selected-proof). The smoke checks
row/metadata/backing/ancestry/kind/validity/dependency/duplicate and healthy-write
observations on both sides, not merely acceptance of emitted SQL.

## Partitioned exclusion cutover

EXCLUDE extends the existing coverage/remap/recursive index stage, not a parallel
hierarchy walker. Every applicable partition key must be a simple exclusion key
with matching collation and the partition equality's actual canonical signature.
Each declaration owns target-plus-descendant scope. An independent child-local
declaration is not widened by unrelated ancestor partition keys.

Each exclusion owns immediate Parent and IsLocal provenance, a distinct backing
and its own immutable rebound operator slice. Virtual roots own no membership;
physical leaves retain runtime conflict diagnostics and actual logical GiST roots.
The shared structural comparator validates generated provider structure, while
PostgreSQL18.0 automatic adoption refuses all independent exclusion candidates.
Explicit exclusion index ATTACH preserves earlier55000 checks before42P17.
Selected ONLY ADD leaves an incomplete virtual index rather than cloning children.

DETACH severs crossing edges only; scoped DROP follows inherited edges and retains
independent local constraints. Dump projects names through the existing constraint
scheduler, declares local providers once, and restores inherited names without
explicit exclusion index ATTACH. Destination operator rebinding and constraint
Parent remapping use the installed replacement pipeline.

Temporary constraint names are allocated in schema/table-name order, then by the
original backing index's schema/name within each table, across both unique and
exclusion constraints. This allocation is separate from declaration/clone order:
PARTITION OF can reconstruct local constraints before inherited constraints even
when the original table added locals later. Neither that slice permutation nor
replacement OID allocation may change the canonical redump bytes. The private
naming projection preserves captured constraint slices, provenance, definitions
and addresses; the existing generated-name reconciliation and final rename
barriers still own PostgreSQL replay.

Invalid local virtual exclusions are separate existing declaration events,
rendered as captured `ALTER TABLE ONLY ... ADD CONSTRAINT`. Gap memberships precede
the declaration; covered memberships follow it and create only the saved
inherited providers. A captured DEFAULT gap may precede bounded children while
empty, so only contradictory DEFAULT-last dependencies are omitted. Existing
data-readiness edges remain authoritative.

Without a captured gap, existing collision-reserved scratch preparation and
completion events retain complete-invalid or childless-invalid state. Scratch
is removed before captured DEFAULT membership and rows. Actual child
detach/reattach is not a substitute: PostgreSQL18.0 exclusion nonadoption would
duplicate independent providers. Membership and populated ATTACH own clone
creation, name reservation and rename readiness. Terminal names follow logical
final-name order rather than original provider OIDs; existing ATTACH preload
dependencies remain intact, without a global row/name barrier.

