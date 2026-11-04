# internal/engine/storage

The engine's physical data layer: committed relation storage and row
versions, deletion roots, logical B-tree/hash/GiST/SP-GiST/GIN/BRIN/bloom index roots, sequence
generator state, and the `Store` that owns them. The package sits at layer
L4 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `catalog`, `expr`, `ids`, `sqltype`, SQL-independent `util`
and the standard library, and never the engine.

## What it owns

- **`Store`** (`store.go`): the published maps `Relations`, `RelationMetadata`,
  `Sequences` and `IndexRoots`, and the row and version identity allocators.
  The engine holds one store by value (`Engine.Storage`) and never replaces it whole.
  The counters are private: read them with `RowFrontier`/`VersionFrontier`,
  reserve with `AllocateRowVersionsLocked` (one row and version per row, in
  order, with the exhaustion diagnostics), `AllocateRowLocked` and
  `AllocateVersionLocked` (one step; the caller has checked exhaustion with
  its own diagnostic), and install both with `SetFrontierLocked` after
  Restore or foreign replacement reminted storage. `IndexRootsCurrent`
  compares captured roots with the published ones.
  Map writes use `state_write.go`: `InstallRelationLocked`,
  `RemoveRelationLocked`, `ReplaceRelationsLocked`, and the corresponding
  sequence and index-root methods. Reads may use the exported maps directly.
- **Relation storage** (`relation.go`): `Relation` (the version vector, end
  root, immutable retained-predecessor root, generation, index-build origin
  and lineage tokens), `RowVersion`, `HeapPredecessor`, `RowEnd` and
  `DeletionRoot`. `SameVersionRoot` compares
  two version roots by identity. `IndexIdentity` names the
  storage an index root was built from. `RowByID` finds a row by its stable
  `RowID`, trying a captured position first.
  `PhysicalVersion(VersionID, hint)` resolves an exact genuine physical version,
  validating a known committed-vector ordinal before its constant-work lookup.
  Unknown or stale hints fall back across committed, retained, discarded and
  repaired additional roots, never to a same-RowID head. Every result applies
  the current immutable header root while sharing the original payload.
  `Generation` advances on ordinary DML publication as well as physical changes;
  it is a tuple-root revision, not an immutable heap-incarnation token. The heap
  pointer identifies the storage incarnation; rewrites install a new pointer.
  `heap_headers.go` owns immutable exact-VersionID repair/dead-slot roots and the
  shared logical TID bijection. `HeapXmin`/`HeapXmax` read captured physical
  insertion/deletion facts; freezing one version never substitutes its RowID's
  current head. `Discarded` retains genuine accepted private versions across
  owner disposal, sharing immutable payloads. Reserved allocator identities and
  rejected pre-insertion inputs do not create heap slots. Captures retain their
  original header roots; fresh sources enumerate repaired additional versions
  once by VersionID. Repair publication is deliberately outside transaction
  undo, while later genuine DML still uses its actual admission/end provenance.
  `HeapHeaderRoot.Versions` is the sole exact-header authority, an immutable
  `util.PersistentMap`: `Get`, `Set` and `All` preserve captured versions.
  Publications share unchanged trie nodes and payload backing; per-row UPDATE
  and DELETE copy only changed search paths, not the complete growing map.
  Commit end stamping shares the xmax publication pass. Nil roots and unchanged
  admission paths retain their identity without allocating.
  `KillHeapHeaders` copies only changed map paths and preserves every other
  header field, stamp and payload. `RowVersion.HeapKilled` is a captured
  projection, never another ledger: canonical visibility and visible-data
  export reject it before private/frozen/birth shortcuts. End/update/commit
  publication cannot clear death; genuine maintenance may retire slots without
  resurrecting their payloads. Shape transport preserves headers, while fresh
  incarnations and logical imports do not borrow old operation authority.
  Sparse `HeapHeader.TupleLockVersion`/`TupleLockStamp` name repaired tuple-lock
  lineages: zero pairs retain ordinary RowID arbitration; exact repair assigns
  its own version and fresh stamp, and accepted successors inherit that pair
  through `ReplaceHeapHeaders`. Re-repair splits only its exact target. Header
  transport and retention preserve pairs; genuine slot retirement and fresh
  imports carry no live tuple-lock anchor. RowVersion carries no anchor copy.
  `DeletionRoot.Lookup(RowID, VersionID)` selects only the exact physical end.
  `Rows` is its ordinary fast path; supplementary `Versions` owns other ends
  sharing the RowID, each once. `All` enumerates every fact, `Clone` detaches both
  maps, and `Set`/`Delete` mutate only unpublished roots. Maintenance and rewrite
  preserve or retire individual physical ends without erasing sibling markers.
- **Logical heap verification** (`verify_heapam.go`): `HeapVerification` borrows
  live column usages/stable attributes and immutable committed/own version/end
  roots. `VerifyHeapam` checks exact typed admission and bounded identity,
  birth, private-predecessor, width and end-sequence facts without visibility
  filtering, codec I/O, constraints or mutation. Findings retain exact RowID and
  VersionID; Attribute zero is tuple-wide. Stopping completes every finding for
  the first invalid version. Captured committed order precedes own order.
- **Lineage tokens** (`lineage.go`): `TruncateLineage` and
  `MaterializedLineage`. A token is a nonzero-size allocation compared by
  pointer; it retains no storage or rows. `SameTruncateLineage` is nil-safe.
- **Index roots** (`index.go`, `hash.go`): `IndexRoot` and `IndexEntry`
  (B-tree keys or GiST membership), `HashRoot`, `HashEntry` and the bound
  key `HashBinding`, and `IndexOutput`, an index membership/key a private
  row already produced. `CaptureRowEntries` builds the per-row hash entry
  index for exclusion indexes with runtime domain key conversion.
  `index_identity.go` owns exact-version partial membership and stored-key lookup.
  Existing per-row maps retain the ordinary fast path; supplemental VersionID maps
  own genuine collisions once. All lookups require the requested exact version.
  Fresh construction/maintenance uses `SetMember`/`SetRowEntry`; captured roots and
  hash bucket pointers remain immutable. Incremental GIN retirement sets likewise
  carry every actual VersionID with its RowID association, not one end per RowID.
  GIN member outputs additionally own immutable `GinPending` placement. Cleanup
  may clear it through COW without changing keys, SQL values or row visibility;
  a later version receives independent produced placement.
  `IndexRoot.Construction` is an opaque immutable index-owner provenance payload.
  Storage transports preserve it; they cannot mint a new full-build recipe.
  Index roots contain only current logical membership; tuple/page statistics
  estimate physical metrics without retaining deleted index items.
  `brin.go` owns the immutable `BrinRoot`, per-key bindings and range summaries.
  It retains effective width and storage/generation/prefix/end captures, not
  a row-key list. Logical pages are authoritative version-vector slots; datum
  comparison, summary publication and candidate consistency belong to `index`.
  `BloomRoot` owns per-occurrence immutable uint16 signatures aligned with the
  shared exact key/version memberships, typed hash bindings, effective options
  and storage/generation/prefix/end authority. Local signature scratch never
  escapes into a root. Delta publication shares only unchanged immutable
  signature backing; vacuum moves signatures with their exact versions.
  `index_gin.go` owns `GinRoot.Columns` (main) and `Pending` per-column dictionaries,
  concrete token bindings, nonnull membership and exact immutable row/version/slot
  postings. Full builds are main-only; DML partitions already-produced deltas by
  accepted placement. Batches sort/deduplicate typed tokens and merge each target
  dictionary once; ended exact versions are removed from both portions.
  `CleanGin` merges existing typed pending payload without extraction, shares
  untouched posting backing and preserves the predecessor. `GinPageEstimate`
  uses 128 token-posting incidences per technical unit, never heap membership.
  Binary lookup consumes postings rather than full Entries.
  `UnionGinPostingLists` combines selected sorted posting lists with a typed
  binary cursor heap, sharing the single-list fast path and otherwise allocating
  one result plus O(k) cursor scratch. It uses the same complete
  Row/Version/Position order as existing posting operations, deduplicates only
  exact triples and never changes source backing. Capacity sums are checked;
  bounds/sizing/heap/merge completion remain cancellable, with nil results on
  error rather than partial output. The selector owns token/category bounds.
  `ValidateGinMerge` shares extraction and comparison/cancellation traversal with
  `MergeGin`, but retains only delta-sized scratch. Admission never appends an
  incumbent survivor dictionary or allocates merged posting output.
  `TransportGin` is pure cancellable structural filtering/position
  remapping; vacuum validates each original heap identity and preserves unchanged
  roots when no compaction is needed. SQL key/predicate execution stays outside
  storage and all fallible work completes before publication.
  These are process-local logical artifacts, not physical pages/WAL or a durable
  PostgreSQL index format. Native controls rebuild destination-owned payloads and
  retain GIN split through symbolic dump-row metadata, never serialized roots,
  source identities, live pointers or session resources.
- **Sequence generators** (`sequence.go`): `SequenceGenerator`.
- **Catalog publication values** (`row_shape.go`, `publication.go`): a
  stage's planned heap replacement `RowShape`, whose `Matches` fences store,
  generation and row count and `MatchesWithEnds` also the deletion ends;
  actual codec-preserving copies additionally compare their observed immutable
  VM root. `PreservesVisibility` comes from the genuine copy-producing seam,
  with same-generation consistency, never from generation equality alone.
  Metadata-only same-pointer candidates and destructive fresh heaps do not take
  this additional fence; their existing tuple guards remain unchanged.
  `Publication` (initial tables, row shapes, refreshed store) and its
  `CandidateRelations`, the detached relation-map candidate, and
  `PreserveUnchangedRelations`, which refreshes its borrowed heap pointers under
  Engine.mu at final installation without changing owned replacements/removals;
  `Relation.StampPublishedBirth` and `CountPublishedInserts` for privately
  built stores; and `Store.ReplacementCapacityLocked`, the identity and
  commit-sequence capacity check of a catalog replacement.
- **ANALYZE facts** (`relation_metadata.go`, `statistics.go`,
  `statistics_collect.go`): `Store.RelationMetadata` is the one canonical owner
  for immutable ANALYZE records across all relation kinds. Partitioned parents
  retain metadata without any synthetic physical `Relation`. Each record owns
  the sole committed `Statistics` root, keyed by stable attribute and
  local/inherited flavor, plus successful scan cardinality/count/time facts.
  `CollectStatistics` consumes caller-owned visible/layout-mapped borrowed rows
  outside engine locks. Actual grouping/hash/ordering bind once per column.
  NULLs, supported non-NULL distinct and MCV counts describe actual collector
  input: full visible native passes, direct foreign samples, or the same bounded
  projected sample shared by mixed inherited scalar and extended collectors.
  ForeignAnalyze separately owns direct sample size and estimated live/dead/page
  population, never fake heap maintenance. InheritedTuples is the sole float64
  acquired population: exact for full-native capture, estimated and possibly
  incomplete for mixed visits that legitimately perform no acquisition. Partition
  parents project it without setting local Scanned/Tuples. Unknown distinct is
  not measured zero. Provider-free structural authority includes foreign nodes
  regardless of current capability; graph/layout changes invalidate old facts.
  `DistinctOverride` is a separate signed estimate: it never changes measured
  distinct/NULL/MCV counts or histogram construction. Zero retains ordinary
  measured encoding; negative estimates require current source-population
  authority from the planner caller.
  SamplePopulation enables singleton-sensitive sampled-distinct estimation from
  the collector's existing groups without changing literal counts. ExtendedInput
  carries it into projected expression summaries, not joint counts or degrees.
  NULL/MCV frequencies always use accepted sample Rows; empty samples replace
  no compatible distributions.
  Each StatisticsColumn carries the admitted target (1..10000): at most target repeated MCV groups and
  target+1 weighted residual quantile endpoints are retained. Target1 still
  collects statistics. Deterministic membership/ties and residual summaries are
  approximations,
  not PostgreSQL physical samples. Frequencies use all rows as denominator.
  Missing equality keeps NULL facts; missing ordering keeps grouping facts.
  Empty collection returns no replacements, preserving prior distributions.
  ANALYZE callers admit selected enabled scalar columns before extended inputs.
  Missing required simple keys retain compatible old extended backing; an empty
  scalar admission retains all extended backing, including expression summaries.
  Only admitted extended objects contribute to mixed sampling requirements.
  `MergeStatistics` replaces selected columns without mutating older roots or
  unselected payloads; `CompatibleStatistics` filters dropped/type/collation
  incompatible attributes. Callers own transactional column deltas, authority
  revalidation, lifecycle retention and record replacement under `Engine.mu`.
  Completed local heap ANALYZE updates physical maintenance scan facts and, for
  no-list collection, resets only the since-analyze modification counter. It
  retains cumulative DML/VACUUM and since-vacuum counters. That maintenance and
  count/time survive a later inherited failure for an existing object, while
  both typed flavors stay transactional and unfinished inherited estimates
  never publish. Physical Relation.Maintenance does not duplicate typed
  statistics, ANALYZE count/time or inherited population.
  Imported column facts share that same canonical root. `ImportedStatistics`
  has explicit scalar presence and five immutable typed kind slots, so zero
  distinct estimates and arbitrary FLOAT4 probability mass never require fake
  sample counts. Measured integer backing remains unchanged; wholly measured
  planner inputs retain their exact-count path. Selected restore/clear deltas
  use the ordinary journal, including statement rollback and savepoints, without
  changing ANALYZE scan timestamps or maintenance counters.
  Attribute snapshots retain measured backing, scalar bits and typed kind
  construction facts through `StatisticsSnapshotCodec`. Destination staging
  resolves column usages and remints typed values before atomic installation;
  no rescan refreshes stale imported distributions. Malformed native records
  fail without installing a prefix. Units: `make check RUN='^TestAttributeStatistics'`.
  Native admission checks kind eligibility independently of value presence:
  both element kinds require an array/tsvector element with equality support,
  including numbers-only element-count histograms. Value slots retain the full
  expected usage (type generation, typmod and collation), not just a type OID.
  SQL restore and native admission share the same element eligibility helper.
  Histogram/correlation admission checks less-than operator metadata in the
  captured catalog, not the narrower native ORDER BY/index execution gate.
  Exact default-btree classes take precedence; generic array/range/multirange/
  record operators resolve their declared polymorphic signatures. Arrays and
  records also require comparable element/member usages, so a generic operator
  declaration alone does not admit JSON arrays or records with JSON fields.
  Imported estimates on nonempty inputs retain PostgreSQL's one-row
  minimum, while wholly measured exact-count zero estimates remain unchanged.
  With no selected statistics row, builtin typed column/constant equality uses
  PostgreSQL's DEFAULT_EQ_SEL (1/200) in the canonical detached filter estimator.
  Measured/imported facts still override that fallback; clear never leaves a
  hidden distribution or a fixture-specific planner exception behind.
  Retained TEMP column facts remint catalog-result ANYARRAY carriers even when
  the stored element usage is unchanged. Changed element generations and
  catalog-bearing values use the existing retained typed codec; unchanged
  immutable backing is reused. TEMP state does not escape into persistent dump
  sections, and already detached cursors retain their original value generation.
  Extended facts share this same root, keyed by generation-qualified statistics
  definition and inherited flavor. `ExtendedCollector` reuses the ordinary scalar
  collector for typed projected expression values and shares those values with
  ndistinct, dependency and MCV collectors. Typed NOT DISTINCT keys are bound once
  per dimension and prepared/hashed once per row for all requested kinds; every
  hash collision is equality-checked. Ndistinct covers subsets of widths2..8.
  Dependencies cover every nonempty proper determinant subset and each other
  dependent dimension (at most1016 directions). A group contributes all its rows
  only if every dependent value agrees; conflicting groups contribute zero.
  Positive row-weighted degrees are retained; all-zero analysis yields typed
  SQL NULL in an existing analyzed row. This deterministic logical-population
  estimate does not emulate PostgreSQL sampling or physical TOAST-width filtering.
  Joint MCV counts every joint and marginal group before applying the captured
  target. Singleton groups are eligible; ties retain first captured occurrence.
  Frequencies use all accepted collector rows, and baselines multiply those
  marginal fractions, never renormalized retained counts. Mixed population
  estimates never inflate integer joint counts. `ExtendedMCV` owns
  compact non-NULL typed dictionaries, explicit NULL flags and integer joint/
  marginal counts; `BuildExtendedMCV` derives the single immutable native value.
  MCV-only definitions do no ndistinct subset work.
  NULLs form real groups. Simple keys retain positive attribute identities;
  ordered expression dimensions use -1,-2,... . Serialized members preserve
  positive simple keys followed by expression order, matching PostgreSQL.
  Single-expression, automatic-summary-only (`e`-only) facts have genuine
  scalar summaries but no joint Value. All-expression multivariate definitions
  can also collect ndistinct, dependencies and MCV. `MergeExtendedStatistics`
  preserves unselected backing; compatibility
  checks retained recipe identity, stable input/result usages, actual external
  provider revisions and current hierarchy authority, not display names or DML.
  AverageWidth retains the actual result type's available fixed datum width,
  including all-NULL populations. Non-NULL text/bytea use datum byte lengths,
  never formatted wire size; all-NULL/unsupported variable-width models remain0.
  The strict `stuntgres-ndistinct-v1` channel transports checked extended facts,
  including optional typed expression summaries and semantic recipes.
  Expression summaries preserve the signed sampled distinct estimate as exact
  IEEE754 bits separately from literal sample rows and distinct counts. Zero
  retains measured encoding; nonzero estimates require known non-NULL groups
  and finite values at least -1. Restoring that typed estimate transports no
  scan, inherited-population or maintenance count/time authority.
  A dependencies array is mandatory exactly for f records. Optional joint MCV
  records denote built state, not merely a requested kind; non-MCV records keep
  their prior bytes. MCV snapshots retain population, full marginal and joint
  counts, true NULL flags and compact non-NULL constructor dictionaries.
  Import validates recursive required fields, canonical indexes, logical
  uniqueness and count bounds before destination typed reconstruction. Current
  targets do not invalidate older larger summaries. Native bytes are rebuilt
  with destination type/member/element identities, never recomputed from rows.
  The composing engine supplies checked typed replay, including a private
  retained-tree capability; storage never binds SQL or grants input authority.
  Genuine destination TEMP reownership preserves construction backing for
  unchanged usages, otherwise remints values through typed replay before atomic
  publication. Generic Retag cannot adopt enum/domain generations.

### Logical allocation estimates

`relation_size.go` owns checked INT8 arithmetic over captured immutable roots;
`core` captures descriptors, vector headers, private overlays and index roots
under the cluster mutex, then releases it before counting. No datum payload is
copied, rendered or measured through SQL output; predicates and index expressions
are not reevaluated, and SELECT/RLS visibility does not filter allocations.
Current-command staged rows are included alongside prior transaction writes.
Complete projected private roots override the prior private map; replacement
staging supersedes prior private rows by stable RowID without copying payloads.
Nested routine activation may expose a staged version through both borrowed
vectors; matching RowID/VersionID pairs are charged once in heaps and indexes.
Deletion markers alone do not reclaim allocated physical versions.

These counters are deterministic technical estimates, not PostgreSQL physical
heap, index, TOAST or map-file bytes. Let `A8(x)=8*ceil(x/8)`, with `A8(0)=0`.
A NULL cell costs zero; a non-NULL fixed-width datum costs `A8(typlen)`, using
the type descriptor's PostgreSQL length (including domain base types); any other
non-NULL datum costs one 32-byte slot, independent of payload length.
A row costs `24+A8(ceil(cells/8))+sum(cell costs)`. An allocated heap's main
component is `64+sum(row costs)` over every retained physical version, including
ended versions. Its `fsm=8*ceil(versions/256)` and `vm=ceil(versions/8)`.
Empty allocated heaps cost 64; absent roots and storage-less relations cost zero.
TRUNCATE charges the current root, not retained predecessors; reclamation removes
reclaimed versions from later estimates. Supported native heaps/indexes have no
initialization allocation, so their `init` component is zero.

An allocated index's main component is `64+sum(entry costs)`. B-tree/GiST entries
cost `16+sum(stored key cell costs)+sum(included cell costs)`; included values
come from stable attributes and matching parent row/version identity. Private
entries consume their already-held row directly, without searching the published
prefix. Captured index-entry lookups validate both RowID and VersionID, use the
position hint when valid, and poll cancellation during any fallback scan.
Hash counts each bucket entry once at `16+cell(stored key)`, never its duplicate
lookup maps.
BRIN counts only summarized ranges: `16+sum(8+cell(min)+cell(max))`, with extrema
charged only for keys having non-NULL values; unsummarized ranges cost zero.
Partial/expression indexes charge actual stored memberships/keys, including
already produced private row outputs exactly once. Pending BRIN suffixes do not
own summaries until publication. Index `fsm` and `vm` are zero. An allocated
sequence generator costs main 64, with init 64 only when its actual stored
sequence options specify unlogged persistence. Native variable datums are inline:
external TOAST heap and TOAST-index components are both zero, not synthetic roots.

`pg_relation_size` selects one fork (one argument means main); `pg_table_size`
sums the object's forks and external TOAST components, not attached ordinary
indexes. `pg_indexes_size` sums every directly attached index's forks once;
`pg_total_relation_size=pg_table_size+pg_indexes_size`. An index passed to
`pg_indexes_size` has no attached ordinary indexes. Parents do not recursively
charge partition or inheritance descendants. Database size sums each owned stored
relation once, counting indexes separately and excluding shared pg_global
allocations. Tablespace size sums assigned allocations across registered database
runtimes once: default storage belongs to pg_default, shared storage to pg_global.
Virtual catalog rows do not create physical allocations.

For `(INT4,TEXT,BOOL)` rows `(1,'x',TRUE),(2,NULL,FALSE)`, row costs are 80 and
48, main 192, fsm 8, vm 1, table 201. An ordinary index on the integer costs
112, giving total 313. Native Dump/Restore reconstructs supported typed contents
and index metadata through ordinary SQL replay. Clean-state allocations survive
that round trip; dead-version bloat, stale BRIN extrema, sequence advancement and
nonserialized auxiliary allocation history are not a replay identity contract.



## Invariants

- **One lock authority.** The package has no mutex. `Engine.Mu` guards every
  `Store` field and every lineage token. Mutating store methods carry the
  `Locked` suffix; the caller holds `Engine.Mu`.
  This includes bootstrap and isolated replacement engines; standalone storage
  unit fixtures instead own their store exclusively.
- **Minimal map writes.** Install retains the supplied pointer, remove is an
  ordinary delete, and replace installs the supplied map container (including
  nil) in one assignment. No method allocates, copies, validates, initializes
  a nil map or acquires a lock. Installing into a nil map still panics.
  The engine builds detached candidates directly and publishes through these
  methods without changing preflight, allocator or catalog publication order.
- **Immutable published roots.** Published `Relation` vectors, end roots,
  heap predecessors and index roots are immutable and shared copy-on-write.
  Publication swaps the store's maps with the catalog state under `Engine.Mu`. Snapshots
  retain roots by pointer and never copy row values.
  Private `RowVersion.PrivateHistory` links actual predecessor headers in O(1),
  sharing immutable values. Existing Writes transfers retain these roots through
  statement finish, savepoints, routines/rules and PREPARE; rollback drops only
  the discarded ownership. Commit prepares fresh predecessor headers before the
  all-relations veto and stamps/publishes them with heads, ends and indexes.
  Published heads retain no private history or index-admission pins. Projection
  transforms history into fresh payload vectors; a genuine rewrite/import starts
  fresh physical history.
  `MapHeapPredecessors` retains exact no-op roots and shares unchanged suffixes
  during compatible generation transport. Existing datum/key visitors transform
  changed heap headers without recipe evaluation, preserving row/version
  identity, insertion provenance and end facts.
- **Publication values take no lock.** `RowShape`, `Publication` and the
  stamping methods are value code the engine runs under `Engine.Mu` inside
  `publishCatalog`; the visibility calls, retry loop and installation stay
  in the engine.
- **Allocation order.** Row and version identities are globally nonreused
  and allocated in order; failed work may leave gaps.
- **Sequence generators.** Generator state is nontransactional and distinct
  from the transactional sequence options in the catalog and from the
  per-session currval/lastval memories of [`sequence`](../sequence/README.md).
  Never add live catalog undo or per-CACHE arrays for it.
- **Maintenance provenance.** `RowVersion.InsertFullXID` is actual canonical
  committed publisher identity, independent of Born CommitSeq. Ordinary `Frozen`
  is a visibility flag: `HeapXmin` still exposes the original raw insertion XID.
  Exact `ForcedFrozen` repair exposes raw xmin 2/xmax 0 while retaining publisher
  provenance. `ImportFrozen` denotes an atomically committed detached import
  without fictional source/target XID and exposes raw xmin 2; ordinary self
  replacement preserves provenance. `Relation.Maintenance` holds coherent logical facts.
  Same-object storage rewrites copy cumulative maintenance from the validated
  current predecessor inside publication, not stale prepare-time facts.
  `Maintenance.RecordBirth` clamps an existing full-width bound against each
  actual unfrozen publisher without rescanning old versions. Unknown bounds
  remain unknown until `visibility.Registry.InitializeMaintenanceLocked` seeds
  them against the canonical safe cutoff. `Maintenance.RecordVacuum` records an
  installed VACUUM pass.
  Remint initializes current Live from committed visible capture cardinality,
  Dead 0, and independently resets scan/vacuum/cumulative history; transport is
  not counted as native committed INSERT activity.
- **VACUUM roots.** `InspectVacuum` counts and chooses exact committed matching
  end/version retirement; `MaterializeVacuum` copies headers only when changed,
  preserving survivor payloads and old roots. The engine owns the conservative
  snapshot gate and final fence. `RemapVacuumIndex` preserves keys and existing
  index state, remaps positions/membership/RowEntries and rebuilds hash pointers
  against new buckets. Hash/bloom prefix coverage is preserved, never upgraded to
  a complete index; freeze-only work and predecessor retirement rebind hash/BRIN/
  bloom generation without needless B-tree remapping or signature recomputation.
  BRIN compaction dispatches through the index owner's
  conservative destination-range reconstruction, never generic entry remapping.
  No-change passes retain roots; no row/version IDs are minted.
  `VacuumProgress` separates an optional per-fetched-source callback from the
  existing batched Completed callback. Actual FULL supplies source read authority
  only to the primary inspection loop, after cancellation admission and before
  dead/retention filtering; copy and history reconciliation never refetch it.
  Ordinary vacuum and reconstruction leave Fetched nil. Storage owns no statistics
  provider and does not infer access events from survivor or completion totals.
  `MaintainHeapHistory` applies that gate to actual committed predecessors and
  discarded versions using the captured exact-version `HeapHeaderRoot`. A repaired
  live version survives maintenance, including a version absent from the head
  vector. Eligible retirement records only genuine allocated redirect/dead/unused
  slots and releases their payloads; arbitrary missing coordinates create nothing.
  Redirect targets come only from accepted UPDATE successor links to genuine
  surviving normal slots, never RowID equality or following an existing redirect.
  Index cleanup determines whether an indexed retired slot remains dead.
  `EffectiveHeapRows` shares untouched vectors; `MapHeapHeaderRows` transports
  repaired payloads without changing immutable header authority or old captures.
  `RewriteHeapVacuum` promotes visible repaired additional versions and preserves
  the existing rewrite RowID/VersionID/insertion identity contract. Independent
  control imports remint identities and clear physical history/header roots.
  Statistics collection only borrows these roots. Index roots have no physical-item archive.
  `pgstattuple.go` owns Dirty/Vacuum classification and checked deterministic
  page/byte/percentage arithmetic, not a second statistics store.
  `pgfreespace.go` owns a separate compact live-population model. Each visible
  logical row contributes checked `RowAllocation+4` bytes once, using the caller's
  own writes/deletes and retained MVCC frontier, not dirty physical predecessors.
  Payload P occupies ceil(P/8168) pages (zero when empty); full pages report zero,
  the last floor((8168-used)/32)*32 bytes, and absent valid blocks zero. Deletions
  reclaim/repack immediately; rollback restores the population. The immutable
  layout holds only page count and last slack, never expanding scalar probes.
  A genuine sequence contributes a fixed 48-byte state tuple and 4-byte pointer.
  Index domains use ceil(Allocation.Forks.Main/8192) and only entirely unused pages;
  current compact roots retain none, so probes are zero (hash has no FSM).
  This does not change relation_size's retained-version allocation model.
  Physical FSM tree pages, stale update cadence, disk history and index recycling
  are non-engine mechanisms, not fabricated storage or authorities.
  `pgvisibility.go` consumes detached facts in genuine VersionID order, deduplicates
  only physical identity, and packs checked `RowAllocation+4` weights into
  8168-byte page payloads. Compressed immutable runs supply probes, relation rows
  and exact flag counts without expanding pages for scalar/summary calls.
  `PrepareVisibilityFacts` owns canonical ordering in `PreparedVisibilityFacts`.
  Its Capture/Check/Basis consumers reuse that vector; maintenance sampling may
  update nonidentity facts, but cannot append/reorder them or change VersionIDs.
  Only appended genuine dead pointers require a second preparation sort.
  Standalone capture/check/basis functions still accept arbitrary fact order and
  canonicalize safely. The wrapper owns no cache, payload copy or new authority.
  Universal proof requires genuine committed frozen provenance, no unresolved end
  and every known protected CommitSeq; unknown snapshots clear positive flags.
  Effective tuple-maintenance evidence independently clears all-frozen; the
  caller derives it from active ownership and genuine full-XID provenance
  against an installed maintenance cutoff, never the SQL raw xmin value.
  VM layout is derived from frozen headers, not from positive flags: coverage
  rounds the highest intersected frozen block to a 32672-block VM page.
  Unknown catalog provenance uses actual producer allocation with conservative false bits.
  `VisibilityRoot` is immutable relation-incarnation VM authority: nil enables
  rebuilding, materialized claims retain exact physical/fact layout basis, and an
  explicit invalidated root suppresses all bits/extent across DML/header changes.
  Only successful VACUUM publication re-enables rebuilding, even on a no-op pass.
  Separate compressed page-proof runs preserve PD independently of VM claims.
  Checks share checked layout traversal but test positive contradictions, not the
  inverse READ proof: old committed unfrozen tuples can be all-visible. Only
  selected claims produce exact VersionID findings; unused/redirect slots are
  ignored, dead slots reported, continuation allocations emitted once. Running
  full-XID horizons and actual raw xmax maintenance stay distinct from advertised
  CommitSeq READ protection. No map operation changes ordinary MVCC admission.
  Visibility-only preparation adds actual retained header-only dead slots as
  four-byte line pointers, deduplicated against payload versions. They establish
  no tuple/frozen/PD proof and acquire no fabricated tuple-lock resource.
  Persistent private-fact keys retain only scalar Domain/epoch/diagnostic identity,
  never TxRef/SessionToken owner attachments; unknown identities cannot reuse a
  materialized basis. Current capture truth still uses exact genuine owner refs.
  FULL instead uses `RewriteVacuum` and `NewRewriteRelation`: always fresh
  incarnation/header/end containers, dense surviving placement, stable row,
  version and insertion identities, and shared immutable datum backing.
  New stores carry bounded read-routing provenance: `RetainedOrigin` is the
  predecessor's grounded family (initially its actual logical locator), and
  `TruncateBoundary` is the latest real committed TRUNCATE frontier in that family.
  FULL and genuine concurrent MV refresh preserve the pair; successful pure
  TRUNCATE sets only its new store's boundary at actual commit. FULL also carries
  the predecessor's existing command-specific lineage tokens without stamping it.
  For a newly SQL-admitted stable query, equal grounded family/boundary, actual
  row shape and unchanged View/RowType/TableMethod identity permit retained
  roots despite valid name/owner changes or equal-valued layout backing.
  Current SQL schema/ACL and catalog-validity checks stay outside read routing.
  Already-captured views and query sources retain their strict defining gate.
  A newer boundary requires
  the distinct current-root/birth-filter policy. Nonconcurrent refresh, destructive
  shape rewrites and imports clear continuity. No heap-incarnation predecessor
  chains/maps/counters are introduced; these fields never authorize publication
  or vacuum retirement.
  Separately, FULL captures the existing `Relation.Retained` root of genuine
  superseded UPDATE headers and invokes `InspectRetainedVacuum` with that same
  conservative snapshot gate. Protected roots transfer unchanged; safe retirement
  drops only the captured obsolete history. Checked dead/removed totals include
  it after current-head sizing and dense position mapping, never as copy slots.
  Final preflight fences the original root and retirement decision. Saved
  committed/private nodes, links, end/full-XID facts and payloads stay immutable.
  `Maintenance.RecordRewrite` retains cumulative live/dead estimates, DML,
  inserts-since-vacuum and ordinary vacuum count/time while recording class
  tuple/frozen facts, separately from cumulative statistics.
  Index recipes are rebuilt by the index owner, never `RemapVacuumIndex`.
- **Imported relation estimates.** `RelationMetadata.Imported` owns immutable
  INT4/FLOAT4/INT4/INT4 values with independent presence bits. Partial imports
  merge without changing heaps, scan cardinality, live/dead counts or allocator
  frontiers. ANALYZE replaces tuples (and foreign pages), while VACUUM replaces
  its affected overrides. Semantic new-file TRUNCATE/heap-rewrite staging records
  ordered field resets/defaults bounded to current imported presence; operations
  without imports retain native maintenance authority. Saved private writes
  cannot hide newer successful ANALYZE after rollback: the journal validates
  replaced fields against the existing AnalyzeCount before resolution or merge.
  Later restore/clear remains authoritative. An in-place TRUNCATE on a heap created/new-file
  in the same subtransaction empties rows without resetting estimates. Its
  transient journal provenance is separate from immutable heap transports and
  never enters snapshots. Versioned snapshot
  comments preserve signed integers and exact float32 bits, resolve symbolic
  schema/relation names after remint, and reject malformed/duplicate/unsupported
  records before atomic publication. Record preparation checks cancellation
  during actual record work; the engine owns the final cancellation observation
  before indivisible installation, including catalogs with no imported records.
  Explicit defaults remain distinct from missing fields. Codec/masked-state
  tests use `make check RUN='^TestRelationStatistics'`.

## What stays in the engine

Write staging (`WriteStage`), the overlays that consult the transaction
catalog journal (`privateStorageLocked`,
`sequenceGeneratorLocked`, `domainStageIndexRootLocked`), DML candidate
building, index builds and maintenance orchestration, Restore, Dump and
replacement, and sequence DDL and transactional sequence metadata. Session
sequence memories belong to [`sequence`](../sequence/README.md). Index key
comparison (`index.OutputMatches`) belongs to [`index`](../index/README.md). The
transaction-private deletion overlay and the single concrete
`HeapOperations.LiveVersion` rule belong to
[`visibility`](../visibility/README.md). Registered heap operations consume these
version vectors and end roots; they do not wrap or replace `storage.Relation`.

`IndexItemAllocation` shares relation-allocation datum-slot sizing with compact
B-tree statistics, including actual INCLUDE row values. Its 20-byte item base
includes the model line pointer and header; the unrelated 64-byte allocation
fork base is not a PostgreSQL page size.

## Testing

Exact physical-header ownership, validated position hints, discarded-root sharing
and repaired-source selection use
`RUN='^TestPgsurgeryFreeze(Storage|PhysicalVersionHints|DiscardedRoots)$'`.
Immutable intermediate headers, successor chains and publication allocation
growth use `RUN='^TestPgsurgeryFreezeHeader'`; the no-op units require zero
allocations and bytes for unchanged ordinary and rejected stale paths.
Immutable death projection, no-op ownership and genuine slot retirement use
`RUN='^TestPgsurgeryKill(Storage|Maintenance)$'`.
The shared TID boundary/packing unit is `RUN='^TestLogicalTID$'`.

Exact physical end collisions, immutable clone/removal and maintenance transport
use `RUN='^TestDeletionRoot'`.
Canonical index collision metadata and compaction ownership use
`RUN='^TestPgsurgeryFreezeIndexVersions'`.

Signed-dimension collection invariants that require real `plan` expression
recipes live in external `enginetests/`, avoiding a storage→plan→storage test
variant import cycle. Pure collector and snapshot-codec units remain local.
Heap tuple classification and allocation units use `RUN='^TestPgstattupleHeap'`;
composed retention, publication and race witnesses live with core.
Freespace layout, released identity and composed capture ownership use
`RUN='^TestPgFreespace'`.
Visibility layout/classification and composed owner proofs use `RUN='^TestPgvisibility'`.
Exact-version maintenance, slot lifecycle, immutable payload transports and
generation fences use `RUN='^TestPgsurgeryFreezeMaintenance'`.

MCV collection, full marginals, typed collision chains and immutable construction
facts use `make check RUN='^TestStatisticsMCVCollection$'`.
Sample-versus-population scalar counts, singleton estimation and empty/canceled
collection mechanics use `RUN='^TestForeignAnalyze'`.

Package units pin the allocators, `IndexRootsCurrent`, index identity and
truncate lineage. Engine witnesses cover storage-map publication
(materialized views, generated replacement, unique and concurrent index
publication, TRUNCATE generations, TEMP cleanup, commit-time publication) and
VACUUM roots; public sequence, control and hash-partition replacement
witnesses are in `reference/`.

```sh
make check RUN='^Test(Store|Relation(IndexIdentity|SameTruncateLineage)|RowShape|StampPublishedBirth|ReplacementCapacityLocked)'
make check RACE=1 RUN='^Test(ConcurrentIndexU1RootPhasesAndVersions|ConcurrentIndexU6ReadyCancellationReleasesSUE|UniqueIndexPublication(Streaming|Cancellation)|GeneratedReplacement(StaleAuthority|LateCancellation)|TemporaryTableCleanupPublication)$$'
make check RUN='^Test(MaterializedViewSnapshotImport|MaterializedViewPublication|MaterializedRefreshPublicationFences|ControlTransactionsReplacementCancellationBoundary|CommitTimePublication|TruncateGeneration|TruncateIndexes)$$'
make check RUN='^TestVacuum'
make check RUN='^TestVerifyHeapamLogicalRows$'
make check RUN='^TestReference(ControlSnapshotSurvivesCatalogReplacement|ControlFailedRestorePreservesCatalog|HashPartition(Controls|FinalRemap))$$'
make check RUN='^TestStatisticsNDistinct'
make check RUN='^TestStatisticsExpression(CollectionAllNullWidth|PublicationInheritedAuthority|Snapshot(AllNullWidth|InheritedState))$'
make check RUN='^TestPlannerOtherStatisticsTarget$'
make check RUN='^TestRelationSize'
```
