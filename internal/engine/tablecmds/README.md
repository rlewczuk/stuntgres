# internal/engine/tablecmds

The table, sequence and VACUUM statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): ALTER TABLE
with its action passes (constraints, defaults, generated columns, column
types, identity, foreign keys, NOT NULL, INHERIT and NO INHERIT), ALTER
TABLE ... ALTER COLUMN DEFAULT, CREATE TABLE (LIKE, typed, PARTITION OF),
DROP TABLE, RENAME of relations, columns and constraints, and SET SCHEMA;
CREATE, ALTER and DROP SEQUENCE with OWNED BY, and COMMENT ON SEQUENCE;
VACUUM and CLUSTER; the table Restore repairs and the Restore identity check; and the
`TableHost`, `AlterHost` and `TypeHost` family hosts. The decisions are
[`table`](../table/README.md)'s, [`sequence`](../sequence/README.md)'s,
`index`'s and `partition`'s; this package orchestrates them over core's
runtime.

## Layering

It imports [`core`](../core/README.md) and the decision packages, never the
root or another family. The root composition (dispatch and Restore hooks),
[`indexcmds`](../indexcmds/README.md),
[`partitioncmds`](../partitioncmds/README.md),
[`typecmds`](../typecmds/README.md), [`viewcmds`](../viewcmds/README.md) and
[`schemacmds`](../schemacmds/README.md) import it; by rule 6 every family
except `rolecmds` may. Code that tables share with those families lives here:
renames, SET SCHEMA, typed sources, the IF EXISTS skip, the ALTER column
candidates and type rebinds, `PrepareAlterTypeLocked`, and the hosts,
`TypeHost` among them (`typecmds` and `schemacmds` build it, with no arrow
between them).

Stays in core: FK enforcement and actions, the DROP pipeline (`DropProvider`),
TEMP preservation, relation admission and publication.

Detached ALTER fills, rewrites, constraint scans and index-shape preparation
activate through the statement runtime only after capture locks are released.
`AlterRowEvaluation` is exclusively a detached row-work factory. ANALYZE likewise
activates its sampling evaluator, not CREATE STATISTICS cooking. Generated, index,
predicate and partition-bound binders remain dormant, including nested binding.
Explicit row-generation context and evaluator context must agree.
`PendingPartitionDefinition.Finish` is the shared ordinary/foreign partition
constructor continuation. It activates the operation's current context after
private capture releases `Engine.Mu`, and supplies that context to bound demand
and its evaluator. Reset/replacement still fences the captured stage and parent
identity before publication.

ALTER expression snapshot demand includes already typed candidate indexes before
DEFAULT preflight, even when the parent's first-index history bit is normalized
only at metadata transfer. Unresolved rewrite work retains its possible roots at
that first boundary; actual index selection/building stays in the physical pass.
A later phase cannot upgrade an already fixed pair with current roots.

## Entry points
- `ExecuteCluster` validates current kind/MAINTAIN before ordinary heap ACCESS
  EXCLUSIVE, then resolves an eligible index in the parent namespace. It simulates
  clustering by atomically recording the real preference; no selected/sibling
  index grants, heap ordering, replacement or forced all-index rebuild occurs.
  Unpopulated materialized views skip before changing preference or recipes,
  restoring the pretarget lock checkpoint and preserving all prior contributions.
  Named execution uses the normal transactional catalog journal, including
  read-only transactions, rollback and savepoints. Only genuinely changed stored
  recipes require baseline targeted overlay rebuilding under the restricted
  table-owner environment, safe initial path and detached command-local settings.
  Every exit restores the caller's identity and presentation.
  `cluster_recipes.go` compares index-owned physical construction provenance with
  current logical recipes, including changes already reminted by routine
  publication. It follows validated stored call identities to current compatible
  declarations, cooks changed execution trees and records selected rebuild
  intents with exact procedure read fences. Historical physical recipes supply
  structural evidence only, never runtime providers. Unchanged siblings remain
  untouched. Ordinary stored-program and replacement lifetime checks stay strict.
  Existing-index maintenance retains the current routine's actual volatility, recomputing exact
  call properties even when an attribute transition lowers it back to IMMUTABLE.
  IMMUTABLE admission belongs to new CREATE/ALTER expressions and predicates,
  including cloned declarations, not an eligibility rule reapplied during CLUSTER.
  Bare execution selects remembered identities and warns for denied MAINTAIN
  before acquiring target locks. Authorized foreign TEMP targets acquire heap
  ACCESS EXCLUSIVE before being excluded, without selected-index locks or work.
  Completed targets release one normal transaction each; earlier target commits
  survive a later failure. Neither path creates physical files or assumes a
  portable unordered result order.


- Dispatch (`core.Statements.ExecuteLocked`): `AlterTableLocked`,
  `AlterDefaultLocked`, `CreateTableLocked`, `DropTableLocked`,
  `RenameConstraintLocked`, `RenameRelationLocked` and `AlterSchemaLocked`
  are lock-domain continuations: entered with `Engine.Mu` held, they release
  it on every return and run admission waits, provider work and row
  evaluation unlocked.
  `AlterTableLocked` takes a nil physical-work collector for an ordinary
  statement; inheritance supplies its command-local collector so every target's
  metadata is installed privately before any rewrite callback or heap work.
  Physical work retains its original source layout and storage fences, and
  merges only into the latest private journal.
  A codec-only `ReownRelation` copy of that unchanged source records actual
  VM-preserving provenance and its copied root under `Engine.Mu`. Physical
  projection/rewrite candidates instead start a fresh model; equal generations
  alone never authorize preserving-copy publication.
  Historical CHECK/NOT NULL scans select effective rows from the final heap's
  captured headers after shape and codec transport, including genuine repaired
  additional versions. Validity-only scans use their coherently captured roots;
  neither scan materializes header projections into immutable physical payloads.
  `TestPgvisibilityCheckAlterPhysicalProducer` consumes real collected TYPE work
  and deferred metadata before the actual physical continuation; it distinguishes
  unchanged-heap codec transport from destructive rewrite.
  Its optional admitted-ADD collector reports original action indexes only for
  successful, non-skipped ADDs in the evolving phased relation, including cached
  inheritance actions. Inheritance forwards it only to the named root, never
  descendants. Column-option routing consumes it only after a successful prefix
  in the same statement journal; unrelated callers pass nil and allocate no
  admission tracking.
  Parent DEFAULT runtime preflight precedes a later inherited-action selection
  diagnostic. The prewalk retains that semantic diagnostic on its prepared
  descendant and replays it only at the actual sole-stage visit; grants,
  authorization, definition-reader validation and partition error timing keep
  their existing boundaries. No later descendant is cooked after that failure.
  Sequence prephase admission checks exact grant-time originals without
  transferring effects. Ordinary metadata transfer installs sequence descriptors,
  generators and retirements once before identity or volatile physical recipes;
  AS validation and cache-tail demand retain their earlier ordering.
  Table persistence preparation retains original FK identity through definition
  work, including root re-entry during inheritance. Final surviving owned
  sequences receive OID-ordered AccessExclusive grants outside Engine.Mu and
  replacement descriptors/generators in the same metadata stage. A sequence
  addressed by ALTER TABLE selects itself through that same stage; unsupported
  companion actions reject the complete list before any persistence effect.
  Pure persistence work never replaces heap storage. Cache adoption remains lazy.
  Post-grant descriptor replacement rejects the stale candidate without widening
  its base; generator-only advancement is captured from the latest live state.
  VALIDATE resolves after deferred ADD FOREIGN KEY, carries only selected
  identities into unlocked effective-row scans, and publishes through the same
  fenced Finish. Eligible valid roots return without a stage or heap work.
  Common ALTER admission checks executing constraint-invocation ancestry before
  effective pending index/FK/user events, after relation identity/owner authorization and before
  preparation, special paths and no-op elision. Separate SET/DROP DEFAULT checks
  the root and every actual
  recursive target before cooking or staging any default. Neither path flushes,
  filters repaired rows nor consumes events. A parent-side FK event protects the
  parent, not the child; separately dispatched rename/schema actions do not use
  this ALTER guard. FK timing changes share the atomic candidate/publication
  pipeline; persistent defaults, OID-keyed SET overrides and trigger modes remain
  independent.
  This constraint guard does not add cursor restrictions to metadata-only ALTER;
  native eager cursors retain their captured immutable input under flag changes.
  Constraint-trigger rename changes only independent conname. The ordinary ALTER
  pending-event guard precedes DROP CONSTRAINT, which cannot directly drop its
  trigger-owned INTERNAL constraint. DROP TABLE checks the actual staged
  retirement closure, including descendants and dependency cascades, after waits
  and again at transfer/publication. Dropping a trigger does not discard an old
  event or bypass its physical relation's pending fence.
  NN inheritability uses that same stage, with exact source-parent events.
  NO INHERIT retains/localizes direct-child facts without child SET authority;
  INHERIT checks actually visited child owners and uses ordinary inherited SET.
  Compatible existing facts stop recursion without validation; missing facts
  acquire normal selected row scans. No descendant publishes independently.
  Column statistics configuration uses the same metadata stage and revision
  publication, never a row rewrite. Deferred statistics actions prevent pre-misc
  no-op elision, and changed candidates receive final revision publication after
  misc. Pure statistics lists omit heap capture. ANALYZE captures each effective
  target once,
  omits disabled scalar columns while retaining old facts, and collects extended
  inputs independently. Flavor-specific distinctness overrides attach only after
  distribution collection; descriptor changes alone never recompute statistics.
  Trigger-mode lists use one statement-private metadata stage across the addressed
  relation, partition clones and referenced-side FK owners. The target holds
  SHARE ROW EXCLUSIVE (or a stronger companion grant); additional owner grants
  are OID-ordered outside `Engine.Mu`, then exact base descriptors are rechecked.
  Root MISC consumes the original ONLY intent; descendant structural candidates
  retain already staged modes. Finish publishes all owners together or none.
  Replica identity is root-only MISC metadata: the parent mode and real child
  index selection bits share that same stage and publication. Eligibility uses
  current key attributes (not INCLUDE nullability); mode/index switches revise
  dependencies without changing stored rows. Later failure or cancellation
  publishes neither the mode nor any index bits.
  An index created by an earlier ADD/TYPE pass retains its private creation and
  name observations when MISC updates its selection bit; only existing base
  indexes use predecessor replacement.
  Historical FK work is one ordered payload: ADD-time history-validating keys
  precede action-ordered MISC selections. ADD NOT VALID is not retroactively
  enrolled in the ADD prefix when MISC later promotes it. Resolution retains the
  incumbent changed-value traversal as an earlier prefix, then deduplicates stable
  child/OID identities without resorting scheduled work into descriptor order.
  ADD, VALIDATE and enable all retain the same admitted historical obligation:
  a later genuine disable of that queued identity still checks history before
  publishing final false flags and absent triggers. Resolution requires that
  disable evidence rather than exempting one producer's work. TYPE rebuilds use
  the authentic replacement FK identity, unlike enforcement-only toggles. All
  scans and later actions finish before flags, dependencies and trigger identities
  are published together. ADD cooking consumes immutable captured name schedules,
  retaining PostgreSQL's duplicate-name boundary when later explicit CHECK
  installation collides with an already-assigned unnamed FK.
  Recursive CHECK/NOT NULL validation admits the closure through the incumbent
  non-pure provider path, then uses one metadata/physical/final-validation stage;
  already-valid intermediates relay intent without scanning or replacement.
  Only propagated validity-only actions omit descendant-owner checks.
  Pure validation performs no heap-shape or index-root publication and preserves
  earlier own transaction shape authorities. Its source fences protect heap
  pointer identity while enforced DML may advance tuple generations and roots.
  Descendant NOT NULL names resolve by logical column at actual misc, after that
  descendant's ADD phases, rather than against its old descriptor.
  Successful explicit payload deletions pass their admitted pre-step authority
  and old descriptor to core's event capture, including inherited defaults.
  Query inquiry initialization is not part of ALTER ownership or capture.
  Column policy actions share the post-FK misc stage, descendant owner checks
  and final publication fences. Each action changes the first matching simple
  attribute of every affected index in that same stage, even when the table
  policy was already equal. Metadata-only changes retain heap and index roots.
  Restore-only index attribute name/policy repairs use authenticated private
  catalog publication, preserving immutable column/name lookups and executable
  index definitions, never general writable system catalogs.
  Semantic rewrite inspection aggregates persistence/default/type/method causes
  once per stored relation. Actual prepared persistence/method transitions count
  even without native row copies; same-state requests and partitioned roots do
  not. Checked type/default classifiers remain independent of validation scans,
  index rebuilding and maintenance. Callbacks borrow core's live target/mask
  at the existing unlocked metadata/physical boundary with unchanged proofs.
  These are logical row/catalog/transaction guarantees. External server trigger,
  transport and physical-page/WAL/init-fork/crash-recovery mechanisms remain
  outside this executor, as do arbitrary external table-AM native handlers and
  pg_dump process execution; persistence uses logical native Dump/Restore.
- Dispatch (`core.Statements.Execute`): `ExecuteSequenceDDL`,
  `ExecuteSequenceComment` and `ExecuteVacuum` are entered with nothing held;
  they take `Engine.Mu` in short windows and release it around lock waits.
- Restore hooks: `CaptureRestoredForeignKeys`,
  `ResolveRestoredForeignKeyTargets`, `BindRestoredForeignKeys`,
  `RestoreAttributeRank`,
  the physical NOT NULL constructors and foreign-key providers,
  `ValidateRestoredDefault` and `PublishRestoreRepairLocked`;
  `ValidateRestoreIdentityConfiguration` admits only the parameter-free
  configuration of an existing internal sequence.
  FK mode replay resolves all already constructed definitions, including ordinary
  post-data NOT VALID ALTERs. Complete sealed snapshot groups waive row scans only
  for their exact selected FKs. Without the matching SQL seal, every valid
  installed FK is scanned, including definitions outside deferred CREATE work;
  BoundSlot/OID and provider validation remain mandatory in either case.
  NN inheritability replay instead executes ordinary qualified ALTER, or the
  isolated canonical invalid-NN ADD constructor, under normal ownership and
  atomic publication. It grants no writable pg_constraint capability.
  Replay resolves logical owners through catalog names and indexes each owner's
  constraint positions once. It detaches one envelope/FK slice only on the first
  changed slot, validates complete groups before replacing each changed owner
  once, and retains unchanged owners for default-O or repeated-mode snapshots.
  Fully waived pending groups require no temporary definition-slice copy.
- Shared with other families: `SchemaTableLocked` (CREATE TABLE inside CREATE
  SCHEMA), `SkipMissingRelationLocked`, `AcquireTypedSources`,
  `AcquirePartitionValidationViewsLocked`, `PendingPartitionDefinition` and
  `PartitionBoundBinder` (`partitioncmds`' ATTACH PARTITION), and the ALTER
  column candidates, `AlterRowEvaluation`, the check and index captures and
  type rebinds (`typecmds`' ALTER TYPE on composites); `RenameRelationLocked`
  and `AlterSchemaLocked` also serve `viewcmds`' ALTER VIEW.

Declarative column rename captures snapshot-visible membership under the addressed
AX grant using `partition.EffectiveChildren` and the scope's retained pending-detach
snapshot. ONLY uses the same effective direct children. Omitted pending branches
contribute no locks, ownership checks or candidates; canonical membership and
inherited eligibility remain intact. The complete breadth-first, per-parent
OID-ordered AX closure is acquired outside `Engine.Mu`. Authentic catalog/descriptor
fences are refreshed after waits. Descendant ownership and eligibility run only
after all grants, in queue order with the addressed root last. One metadata stage
publishes all local-slot name candidates together, including transaction
overlays/savepoints; heaps, index roots, bound recipes and retained readers are
unchanged.
After stage authorization, rename releases the replacement read gate before
provider work; final publication still fences the captured catalog epoch.

`Engine` (`inheritance_alter_admission.go`) is a defined type over
`core.Engine` that carries only `admitInheritanceAlter`, whose
`(*Engine).admitInheritanceAlter(` frame a read-only reference cancellation
fixture matches. Keep that name.

Partition constraint DROP retains the admitted catalog, roles and reference
path before releasing `Engine.Mu`. Shared index retirement captures each
actual constraint projection; successful overlay transfer alone admits the
records. Validation, authorization, cancellation and database fences remain
the ordinary catalog publication boundaries.

Exclusion adoption preserves actual constraint/backing identity, operators and
physical entries while revising owning relation metadata. Prepared ownership
proof retains a real accepted protocol portal's graph, fold backing/generation
and analysis dependencies across adoption and successor Bind. The definition's
current reuse generation may retire after that legitimate revision; it is not
the lifetime of the old accepted portal's immutable artifacts.

## VACUUM

`vacuum.go` runs in one `StatementScope` and transaction/ticket; per-target
holds return to lock manager checkpoints outside engine and session locks.
`vacuum_publication.go` rechecks root, storage, shape, authority and gate
tokens before atomic installation; peer DML causes recapture, never SQL
replay. The VACUUM barrier and progress tests park the statement at the
`vacuumCompletion` frame and at `storage`'s scan frames: keep those names.
The captured VM-root pointer is also a publication fence: an older detached pass
cannot undo later map truncation. After all fallible/final gates, every successful
ordinary pass re-enables VM rebuilding, including already-frozen passes with
unchanged tuple/header roots and generation. Skipped, unauthorized, failed and
canceled work never clears explicit invalidation.
Progress snapshots flow from actual completed storage prefixes and index loops
through `table.VacuumProgress`, outside engine/session/install locks. The existing
engine-owned row is updated coherently, never reinserted after replacement.
Fixed first-capture block high-water estimates and completed cycles survive
recapture; dead-item and index gauges belong to the current collection/phase.
`TestVacuumProgress` covers boundary prefixes, retirement, selective freeze
roots, recapture, independent workers, target turnover and terminal cleanup.
Context callbacks collect errors/results; assertions run after admission unwinds.
See [the driver contract](../../../docs/DRIVER.md#vacuum-progress).

Ordinary VACUUM activates only a local effective operation child after capture
and initial progress publication unlock, once across recapture passes.
`vacuum.inspect` cooperatively holds genuine `storage.InspectVacuum` work after
each completed 128-version prefix or final nonempty short prefix and after
the progress callback returns. Callback absence does not disable checkpoints;
empty inspection never admits. No engine/session/storage/publication mutex or
catalog reservation spans a hold. Source context, final cancellation fences,
retirement, index remapping and maintenance publication remain authoritative.
Absent or disabled controls allocate no pause state and start no goroutine.

`TestVacuumInspectExecutionPause` covers storage boundaries and attachment;
`TestVacuumExecutionPause` covers exact no-publication cancellation, owner
causes/cleanup, successful survivor/index remapping and independent holds.
The existing generic core/util pause tests retain deadline, release and nested
transport authority. See
[cooperative pauses](../../../docs/DRIVER.md#cooperative-execution-pauses).

Effective FULL acquires AccessExclusive after transient root selection, with the
same current maintenance ACL, kind and TEMP gates. `vacuum_full.go` forces a new
heap and owner-restricted genuine index builds, then atomically publishes heap,
index roots, logical `pg_class.relfilenode` locators and rewrite facts. Work and
callbacks are detached; the final current-authority/cancellation gate precedes
the first install. Immutable predecessor roots remain owned by retained readers.
Captured heads, committed predecessors, discarded versions and exact header roots
share one final generation/root fence. Detached maintenance uses effective exact
headers: repaired live historical versions survive and FULL promotes them into
the replacement's ordinary row vector. Eligible genuine slots retire under the
existing conservative gate; INDEX_CLEANUP OFF preserves indexed dead slots rather
than inventing or following redirects. FULL/type rewrites preserve their incumbent
row/version/insertion identities; independent control imports remint them.
Guarded FULL's physical cardinality includes retained discarded insertions as
well as committed predecessors; quiescent retirement removes each exact slot.
Neither pass mutates earlier captured discarded headers, links or tuple backing.
Native Storage, Retention
and Publication witnesses cover exact counts/current-copy sizing, saved private
savepoint/prepared roots, immutable node/link/header/end/full-XID/payload ownership
and cancellation/recapture/Reset/Restore with nonempty committed history.
Each completed target stays committed if a later target fails. FULL ANALYZE
releases that rewrite's contribution before the same captured occurrence's
analysis, so an invalid analysis column does not retract its preceding rewrite.
Explicit column lists refresh selected facts, row estimates and analyze count/time
but preserve `n_mod_since_analyze`; only a successful no-list ANALYZE resets it.
Both forms preserve FULL's ordinary-vacuum history and inserts-since-vacuum.
FULL progress belongs only to the actual `pg_stat_progress_cluster` worker;
ordinary VACUUM counters and progress remain separate. Disk shrinking and file
paths are external, not effects of the native logical rewrite.

The actual FULL old-heap rewrite also owns a sequential relation-access
capability: scan start includes an empty source, and the primary storage
inspection charges each fetched version before rewrite filtering. The
`storage.VacuumProgress` Fetched callback is independent of batched completed
progress and the later copy/history passes. Errors/cancellation preserve the
reached prefix and a genuine restarted rewrite starts a new invocation.
Index-root reconstruction during FULL has no admitted CREATE INDEX scan
authority. This adds no ordinary vacuum count/timestamp/duration. ANALYZE's
distinct source and lazy ordinary VACUUM remain outside sequential collection.
Like ordinary VACUUM, FULL activates the existing serial statement runtime
after capture locks are released and passes that effective context to the
detached rewrite. Binding's masked pause request is not execution permission.
The original operation still owns cancellation/deadlines and publication fences;
no capture or state mutex spans admission or an execution pause.

FULL preserves the predecessor's grounded `RetainedOrigin`/`TruncateBoundary`
pair and genuine materialized-refresh/TRUNCATE tokens, without modifying that
published predecessor. Genuine concurrent materialized refresh propagates the
same family in both direct and journal publication. A pure committed TRUNCATE
preserves that family but advances its boundary to the actual publication commit
sequence, including prepared completion, never to a staged command count.
New stable queries resolve the current name and current SELECT authority, then
retain exact old roots for grounded family/equal boundary, actual row shape and
unchanged View definition/RowType/TableMethod identity. Valid name/owner changes
and equal-valued layout backing may pass, not structural identity/value changes.
Current SQL name/schema/ACL admission remains separate. Captured-view, admitted
query-source and publication defining authority remain strict; new-read
continuity retains no old permission. Crossing a newer TRUNCATE instead selects
current roots with the original-frontier birth filter, not pre-TRUNCATE rows.
Nonconcurrent refresh, real row-shape rewrites and restore/import establish new
families. No family relation grants publication permission or retires old roots.
`TestVacuumFullMixedRetention` covers repeated mixed chains, both concurrent
publication paths, exact retained roots/index maps/frontiers, own writes,
actual commit and prepared-completion boundaries, and destructive controls.
`TestVacuumFullMetadataRetention` separately covers both rename/FULL and owner/
FULL orders, exact old roots/payloads/pair/locators, strict captured-source
rejection, current ACL denial and actual shape/domain/epoch boundaries.



## ANALYZE

`ExecuteAnalyze` shares positioned maintenance syntax and lock acquisition, not
VACUUM's transaction-block prohibition or row retirement. Relation names resolve
before work; columns validate per target. Each target acquires a real
ShareUpdateExclusive lock, captures statement-visible immutable row roots and
stable attribute/layout authority, then collects outside `Engine.Mu`. Final
publication rechecks epoch, catalog, store, shape, rights and cancellation.
Notices and comparisons execute without engine locks. Virtual generated columns
are skipped without evaluation. ONLY suppresses separate descendant refresh,
not the parent's inherited population.
Each target owns an independent capture at the same statement frontier, rather
than releasing or replacing the statement's query-cache view. Collection uses
the executor's visible-head selection once per physical segment, including own
replacement substitution; a raw union filtered only by LiveVersion would count
replaced committed predecessors twice.

Explicit foreign targets negotiate optional analysis from owned metadata before
resolving a mapping. Unsupported capability warns without sampling. Acquisition
uses the table owner's exact-then-PUBLIC mapping, independently of the caller's
MAINTAIN authority. Both callbacks and codec/grouping/ordering work run outside
engine/replacement locks. The target is at least100, otherwise300 times the
greatest enabled scalar statistics target. Actual bounded sample rows feed the
shared collector; separate provider population/pages and literal sample size
publish on the same canonical RelationMetadata, never a synthetic heap.
Final catalog, owner/handler/mapping/type/permission/context fences precede the
atomic selected-column/count/time/population publication. Empty samples retain
compatible old distributions while updating genuine relation estimates.
An explicit zero n_distinct option retains the collector's normal sample
estimator, just like RESET; only a nonzero user option replaces that estimate.
Literal sample counts and MCV frequencies remain unchanged by every override.


Standalone column facts install per completed target; explicit transactions
record immutable selected-attribute Journal deltas. Maintenance row estimates
and actual analyze count/time update independently and survive rollback for
existing committed relations. No row rewrite or DML counters accompany a
statistics-only publication. Valid BUFFER_USAGE_LIMIT controls are physically
inapplicable; ordinary execution invents no physical progress.
A last-child removal followed by local-only collection clears obsolete inherited
population authority and records inherited-column deletions through the ordinary
canonical/Journal paths, independent of the selected column list. When scanning
a private TRUNCATE successor, ANALYZE also records the scan on that actual heap;
this preserves maintenance ordering through commit and savepoint rollback while
column deltas remain transactional.
Requested ndistinct, dependencies, MCV and automatic expression definitions are captured with the
same relation/selection authority. Scalar and extended collectors consume one
traversal per population. Every simple key must occur in the current column
selection; ineligible definitions warn without combining old column facts.
Expressions evaluate against full rows regardless of selected expression inputs,
whose stable usages and recipe/provider authority remain captured independently.
Expression-only objects need no selected source column. Object target
zero skips rebuilding without clearing its payload. Empty populations retain
prior payloads. Ordinary parents collect separate local/inherited facts;
partitioned parents collect only inherited facts and never gain synthetic storage.
Each retained expression executes once per row through the ordinary executor;
the resulting typed value feeds the ordinary scalar and requested multivariate
collectors. Stable attribute layouts map inherited/sparse rows before execution,
without reparsing SQL or copying complete rows separately for each expression.
Tuple values use typed NULL-inclusive grouping and signed canonical dimensions:
positive simple attribute numbers and expression identities -1, -2, ... .
Selected extended deltas follow the same transactional Journal path.
The full-data tuple scan is deterministic. Ndistinct counts and measured
functional-dependency strengths are technical estimates relative to PostgreSQL
sampling. Dependencies measure the fraction of rows whose determinant group
has one dependent value, including NULL, never constraint-based confidence.
Native collection uses all logical projected rows without fictional physical
varlena width filtering. Definition identity, built-row existence, inheritance,
visibility, privileges and types are exact. Automatic expression facts and all
multivariate kinds share one immutable Extended root. Single-expression (`e`-only)
objects have no joint payload; definitions with multiple expression dimensions
can collect all three multivariate kinds. MCV captures a positive object target
or the command's default target. It counts all joint and marginal groups first,
then retains at most target entries by decreasing count and first occurrence.
SQL NULLs have explicit flags; singleton groups remain eligible. Joint frequency
uses all rows; baseline is the product of full-population marginal frequencies.
No retained estimate changes substantive query results.



## Hosts

Each embeds core's `DDLHost` with its `{E, Op, CatalogStage}` layout, carries
a compile-time assertion, and runs every method under `Engine.Mu` without
waiting.

| Type | Implements |
| --- | --- |
| `TableHost` (`table_host.go`, `index_host.go`, `partition_host.go`) | `table.Host`, `index.Host`, `partition.Host`, `fdw.Host` |
| `AlterHost{TableHost; Scope}` (`alter_host.go`) | `table.AlterHost`, one per call: the ALTER loop begins its stage lazily |
| `TypeHost{TableHost}` (`typedef_host.go`, `domain_default_snapshot.go`) | `typedef.StageHost`, built by `typecmds` and `schemacmds`; `BindRestoredDomainDefault` binds a trusted Restore domain default snapshot |

`viewcmds`' `ViewHost` embeds `TableHost` too, and `schemacmds` builds
`TableHost` for the foreign-data DDL and `AlterHost` for ALTER FOREIGN TABLE.

## Testing

Tests are external (`package tablecmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they inspect; add a name there only when the test cannot assert
through SQL or the exported API.
Units exercising admitted session settings use `Session.AcquireCommand`;
`enginetest.SessionSQL` deliberately uses plain `Acquire` for engine setup.
Heap population assertions read physical `Maintenance`, while metadata-only
relations use the canonical relation-metadata cardinality.

```sh
make check RUN='^TestAlter'                                # ALTER TABLE passes, defaults, types, schema
make check RUN='^TestAlterStatistics'                      # metadata ownership and publication
make check RUN='^Test(InheritanceAlter|InheritanceMaint|InheritanceMetadata|TableInheritance)'  # inheritance ALTER admission
make check RUN='^TestPartitionRenameColumn'               # hierarchy names, identity, locks and publication
make check RUN='^Test(TableLike|TableCore|PartitionConstraints|PartitionExclusion|UsingIndex)'  # CREATE TABLE forms and constraints
make check RUN='^Test(TableHost|AlterHost|IndexHost)'      # the hosts
make check RUN='^TestAlterValidateConstraint'               # selection, publication, admission and parser invariants
make check RUN='^TestConstraintEnforcement'               # enforcement candidate, lifecycle, history and publication ownership
make check RUN='^Test(Sequence|Identity|Serial)'           # sequence, identity and serial DDL
make check RUN='^TestAlterPersistence'                     # metadata, cache, post-grant fences and retained authority
make check RUN='^TestAlterAccessMethod'                    # method intent, providers, publication and retained authority
make check RUN='^TestVacuum'                               # VACUUM selection, phases, publication
make check RACE=1 RUN='^TestVacuum(PublicationFences|AuthorizationLocks)$$'  # VACUUM lock and publication races
```
