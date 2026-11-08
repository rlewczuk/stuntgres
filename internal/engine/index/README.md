# internal/engine/index

Index records and algorithms: B-tree, hash, GiST and SP-GiST keys and roots, GIN
typed inverted postings, BRIN min/max summaries, bloom signatures, unique and exclusion enforcement,
publication and snapshots, write-time maintenance, operator-family/access-method
DDL and partition propagation.

## Imports

Layer L12 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports lower engine packages (`analyze` for types and pure functions only),
`parser`, `sqlstate`, `sqltype` and the standard library. It never imports the
engine, `fdw`, `table`, `partition`, `view` or `dml`; they import it. It never
builds a binder.

## What it owns

- **Row-identity eligibility** (`replica_identity.go`) validates actual parent
  membership, uniqueness, actual constraint timing, expressions/predicate and physical
  key NOT NULL flags in release order. INCLUDE attributes and index validity
  do not add restrictions. ALTER owns the atomic parent/index replacement;
  immutable definition copies retain selection across TYPE and rename.
  Cross-parent LIKE/partition construction starts unselected; adoption copies
  the existing child's own definition rather than the parent's selection.
- **Operator-family and access-method DDL** (`operator_family_*.go`,
  `access_method_ddl.go`): items resolve through the shared namespace, type,
  routine and operator authorities, validate the complete ordered list
  privately, then use the ordinary catalog stage and drop closure; class DDL
  shares this machinery. `catalog.OperatorFamily*` own metadata and
  `Read.FamilyOperator`/`Read.FamilySupport` are the only member lookup
  authority. Registered routine identity selects runtime operations;
  metadata-only callbacks are not callable grants. Validation dispatches on the
  physical implementation while keys keep the exact SQL method identity and
  diagnostics use the declared method name.
  These definitions share the canonical Journal/Finish path in autocommit,
  SQL/Go transactions and retained messages. Stagers do not wait under
  `Engine.Mu`; root admission owns exact provider/name locks and predecessor
  fences before publication. Private reads include earlier successful definitions.
  User-defined type diagnostics use the executing private catalog and current
  search-path visibility. Exact-default ambiguity names the domain's base type;
  missing-default diagnostics name the original requested type.
- **Keys, roots and programs** (`index.go`, `hash*.go`, `gist.go`,
  `predicate.go`, `expression.go`, `program.go`, `clauses.go`, `options.go`,
  `property.go`): `BindIndexOrdering`, `BuildIndexRoot`, `MergeIndexRoot`,
  `BindHashIndex`, `BuildHashIndexRoot`, `MergeHashIndexRoot`, `BindIndexKeys`
  (GiST and SP-GiST), `PrepareIndexRecipe`, `ResolveIndexMethod`, the clause and option
  validators, and `CaptureHashIndexesLocked` (published hash roots that exactly
  cover a plain table query source; caller holds `Engine.Mu`).
  BRIN uses `BindBrinIndex`, `BuildBrinIndexRoot`, `MergeBrinIndexRoot` and
  `BrinConsistent`; `BuildRelationIndexRoot` carries descriptor reloption intent
  into method dispatch. Summary support is not B-tree comparator support.
  B-tree array keys (including domains over arrays) resolve the released default
  `array_ops` and compare through the element type's default btree comparison;
  hash array keys keep the pre-class admission 0A000 because native array
  hashing is not implemented.
  Bloom uses the descriptor-aware builder, typed registered support1 hashes and
  immutable per-version signatures over the shared produced-key memberships.
  `length` and `col1`…`col32` retain lexical reloptions separately from effective
  16-bit-word build intent. Effective option changes atomically rebuild roots.
  Delta publication shares unchanged signature backing; layout, vacuum and
  replacement preserve exact row/version/position ownership or rebuild against
  destination bindings. `CaptureBloomIndexesLocked` proves the same committed
  catalog/storage/generation/prefix/end frontier as the retained query source.
  Signatures select lossy candidates, never exact equality: the executor must
  evaluate the original predicate on each captured visible stored row.
  No PostgreSQL C AM ABI, physical pages, WAL or durable signature format is used.
  Actual admitted SQL CREATE INDEX/REINDEX heap builds use the same cumulative
  RelationAccess capability as query heaps. The selected BuildCapture.Progress
  binding plus a live statement host grants scan authority at collector start;
  generic reconstruction, validation, reused roots and Restore have no such
  authority. Empty real builds count scan1/read0. Each fetched build version is
  counted before build visibility, partial predicates and NULL-key exclusion,
  never inferred from final entries. Cancellation retains only fetched work.
  Composed build proofs respect method admission: HASH uses an ordinary build
  with a skipped NULL key, while B-tree/bloom/BRIN also witness actual partial
  predicate exclusion. This does not expand HASH's supported SQL clauses.
  Generic/Gin/Bloom share the entry collector; hash and BRIN retain their
  specialized row frontiers. These are not index search/simple-fetch events.
  Ordinary ANALYZE uses a distinct analyze source and ordinary VACUUM scans
  maintenance blocks, so neither becomes a generic sequential utility event.
  Handler factories are distinct from catalog validation: CREATE/constraint
  definition owners consume `Host.InitializeIndexMethod`; live publication,
  overlay builds and ALTER rebuilds consume `exec.InitializeIndexMethod`.
  Each selects the genuine immutable AM/handler generation through the canonical
  catalog provider, enters the actual zero-argument static factory with no SQL
  execute check, and returns the native authority used by dispatch. The trusted
  subject ends before key expressions, scans or mutation work. Mu-held DDL,
  query/EXPLAIN capture and VACUUM collection use the same finite pure factory
  without releasing/reentering Mu or invoking an external callback.
  CREATE OPERATOR CLASS and ALTER OPERATOR FAMILY ADD/DROP consume the same
  genuine factory before their released role/member/type validation, using the
  returned native authority for strategy/support/storage/signature limits.
  Actual AM/index/column property inquiries also consume a factory, including
  valid unknown properties; invalid object/column addresses return NULL first.
  Raw `pg_am`, opclass binding, dependency checks, deparsing and descriptor
  validation remain metadata-only. Heap/table factories have only the genuine
  original handler, authenticated by the existing visibility heap authority;
  all seven original fmgrtab handlers remain exempt after rename/schema moves,
  whereas the genuine Bloom C handler is eligible.
  `ambuild`, `aminsert`, `ambulkdelete` and other returned method pointers are
  not separate pg_proc routines. Pure `SnapshotCapture`/`RemintBase`, retained
  layout reconstruction and explicitly no-principal publication are internal
  native construction, not fabricated client invocations. A client cannot
  borrow the no-principal publication path or a missing live factory owner.
  Canonical extension installation constructs its fixed descriptor cohort and
  validates providers as metadata; it does not replay upstream extension SQL
  scripts or emit conceptual script-handler invocations. Explicit supported
  CREATE OPERATOR CLASS/ALTER FAMILY commands instead consume real native
  method configuration as described above. Neither installation-script call
  multiplicity nor active installation-policy parity beyond the implemented
  hooks is claimed.
  This simulation does not promise PostgreSQL relcache cold/warm invocation
  counts, native per-method C callback counts, physical AM pages or enforcement.
  Selected support callbacks retain the genuine class, family, member and
  routine generations, including family-owned members. `Ordering.BorrowSupport`
  attaches only the actual operation's `FunctionInput`, before comparisons in
  builds, publication, uniqueness checks, amcheck and concurrent materialized
  refresh. The btree array class owns slot1 `btarraycmp`382 and delegates to the
  element comparison; family627's `hash_array`626/`hash_array_extended`782 are
  captured metadata only and never reach a native hasher.
  Captures contain no subject, session or permission decision.
  Hash-index equality retains its real strategy-1 operator function separately
  from unseeded/extended hash support; it is a direct callback, not a synthetic
  SQL execute check or a comparator support slot. Supported native hash-join
  acceleration remains restricted to its existing original builtin predicates;
  this does not add extension hash-join or arbitrary alias-opclass capabilities.
  Original fmgrtab support is exempt from trusted entry; genuinely callable
  aliases are not original identities. Renaming a source permits a fresh
  capture, never adoption of stale class/family/member/routine stamps.
  The existing no-SQL-principal publication branch authenticates an explicit
  `InternalIndexPublicationEvaluation`; live clients cannot acquire this origin.
  It suppresses support invocation only for that exact internal catalog root,
  without disabling unrelated TypeIO or encoding boundaries.
  Plain attribute keys have no executable key recipe and acquire no routine
  initialization owner or owner-vector allocation. Genuine derived keys still
  mint their exact live owners before the original prewalk; a missing host is
  rejected even on the typed internal publication lane. Predicate recipes keep
  their separate initialization owner and unchanged execution timing.
  GiST and SP-GiST roots share unsorted exact live membership extraction and
  immutable delta maintenance; scalar predicates and distances remain query authority.
  They contain original typed values, not bounding boxes or signatures.
  GIN (`gin.go`, `gin_scalar.go`, `gin_jsonb.go`, `delta.go`) binds actual
  array/vector or authenticated installed scalar family support and the jsonb
  lexeme providers (default `jsonb_ops`, named `jsonb_path_ops`), then builds
  per-column typed dictionaries and exact row/version/slot postings from
  already-produced keys. `gin_jsonb.go` exposes the jsonb strategy candidate
  probe over those postings with mandatory SQL recheck.
  `GinRoot.Columns` is main inversion; `Pending` is genuine deferred DML
  inversion, not a mirror or counter. Bulk builds/REINDEX populate main.
  Accepted INSERT/UPDATE outputs capture effective fastupdate placement once;
  publication never reevaluates today's options. Batch delta merges extract
  only added keys, end exact predecessors in both portions and share unchanged
  posting backing. NULL items,
  empty items and NULL elements are separate categories; array duplicate tokens
  are deduplicated per version, never across duplicate SQL rows. Token comparison
  reuses concrete scalar element equality/ordering and collation, not rendered
  values. Array GIN has no class comparison member: as in released `initGinState`,
  its dictionary selects the key element's default B-tree comparator, retaining
  that genuine comparator's class/family/member/routine identity. Scalar and
  vector GIN instead invoke their actual class role-1 support. This selection
  grants no new SQL ordering/index capability. Vector projection reads immutable
  canonical lexemes.
  Whole-value extraction uses role 2 and whole-array/scalar query extraction uses
  role 3. The immutable `GinExtraction` borrows existing datum storage and ends
  the extractor's trusted body before any emitter performs dictionary lookup or
  comparison. Ordinary array tokens retain actual nested element comparators;
  NULL markers never invoke a per-NULL helper. Scalar amcheck validates scalar
  payloads rather than treating them as array/lexeme tokens.
  `GinLexemeCandidates` accepts an already extracted lexeme, and its current
  only caller is the focused `index/enginetests/gin_test.go` dictionary probe.
  There is no existing SQL TSQuery GIN-scan extractor owner to enter here:
  exact/prefix dictionary comparisons use real role 1, not an invented role-3
  TSQuery callback or a new SQL scan capability.
  Network/UUID/OID scalar classes use the same dictionary and probe paths, with
  whole concrete source datums, selected unsigned OID semantics and separately
  authenticated CIDR support/search associations.
  Integer, float and boolean installed classes retain exact concrete datums:
  int8 keys never pass through float64, float4 keys retain binary32 rounding,
  signed zeros share an equality key, and NaNs compare equal above infinity.
  Duplicate SQL rows retain separate postings, including zero/NaN peers.
  Date/time/timetz/timestamp/timestamptz/interval classes retain their concrete
  temporal generations and exact finite/infinite representations. Timetz compares
  the unwrapped UTC clock then the stored offset, so equal clocks alone are not
  equality. Interval uses the shared wide span comparison: month/day/hour peers
  share one dictionary key while source components remain distinct. NULL and
  reserved interval infinity tuples remain separate from finite extrema.
  Text/varchar/bpchar/name and unsigned internal-char/bytea scalar classes retain
  whole source datums, including non-NULL empty/zero keys. Selected bpchar_ops
  groups trailing-ASCII-space peers without altering source values, padding,
  clipping, result metadata or ordinary SQL text predicates. Shared text/varchar
  helper identities retain distinct class-associated members and captures.
  Only strategies1–5 are scalar GIN probes; SQL not-equal remains ordinary
  predicate evaluation and does not authorize a GiST/distance member.
  Root-and-ordinal exact/prefix/array/scalar probes search both portions,
  intersecting inside a coherent portion before union. Scalar strategies1–5
  select each portion's typed dictionary postings and union exact physical
  triples. Equality and single-list ranges share borrowed backing when only one
  portion matches; range probes binary-bound ordinary keys and use storage's
  typed cursor-heap union of only the selected lists, never relation heap or
  NonNull membership. Within each portion, P postings from k nonempty lists cost
  O(P log k), with one output allocation and O(k) cursor scratch rather than
  growing-prefix copies. Two nonempty portions need one further linear identity
  union. Empty intervals return nil; one selected list remains borrowed.
  Cancellation at any phase, including the final cross-portion union boundary,
  returns nil rather than partial postings. Scalar predicate
  recheck and visibility remain source-owned; no optimizer path or second
  scalar/array/FTS matcher is introduced. Current roots do not replace ordinary
  own-write or historical visibility.
  Class/family presentation ALTER (`gin_presentation.go`) strictly authenticates
  predecessor captures before selecting current target support. Search transport
  follows the bound member IDs and complete search stamps, never inferred class
  input types or duplicated strategy counts; full member/owner/operator authority
  remains identical even when CIDR or varchar support and search operands differ.
  The catalog stage copies affected live index keys; ordinary/private publication copies
  only root/binding wrappers and shares actual entries, dictionaries and postings.
  A shared text helper presentation change refreshes both text and varchar
  dependent wrappers while retaining the same physical dictionary/posting backing.
  Mapped construction history retains prior recipe debt. Private own-write keys
  travel with the certified descriptor mapping without executing SQL again;
  retained routine-source fences remain required. Detached stale support still
  fails ordinary binding. Extension relocation/current membership uses the same
  stage certificate for complete installed cohorts, allowing only authenticated
  SQL presentation/membership changes with unchanged original executable seal.
  Installed helper OWNER changes require exactly `SchemaACLOwner`'s projection
  of the predecessor ACL: grantor/grantee replacement, coalescing and retained
  grant options, preserving NULL versus explicit-empty ACLs. Unchanged owners
  require unchanged ACLs. This certificate never skips current provider,
  principal/ACL validation or ordinary callable EXECUTE checks, and cannot
  authorize an unrelated grant, wrong grantor or dropped grant option.
  A changed fifth strategy, provider, helper body or codec cannot use transport.
  Nonmaterializing admission uses the same fallible merge traversal with only
  delta-sized scratch, never constructing merged survivor dictionaries/postings
  or an installable root merely to discard it.
  Baseline and vacuum structurally filter/remap postings without SQL callbacks;
  slot-only layouts share unchanged bindings/backing. Snapshot/remint reconstructs
  destination generations, split placement and exact physical ownership from
  transported produced keys, retaining construction history. Empty roots retain
  actual column bindings.
  Replacement first retains destination-bound support/search metadata in the
  imported GIN descriptors after identity/expression remapping, then repairs and
  remints roots against those same captures. Source support is not transplanted;
  certified live TEMP consumers are grafted afterward without rebinding.
  Exact retained TEMP indexes have a separate replacement-owner certificate
  (`gin_replacement.go`). Strict old bindings authenticate the live source;
  fresh destination classes require the same native method, sealed callable
  declarations, support/search members and concrete type/collation semantics.
  COW key and both binding wrappers share entries, membership, tokens and
  postings, retaining construction lineage. Reset may restore persistent class
  presentation without stranding TEMP captures at the old revision. Detached
  binding admission remains strict; no SQL/key extraction or old persistent
  class retention is used to repair replacement.
  A seeded lookup-only search operator retains its exact validated metadata
  without inventing a callable provider for Function/Result zero. Nonzero search
  functions still require the full callable certificate; arbitrary shell
  operators cannot use the retained replacement path.
  Concurrent materialized refresh (`gin_materialized.go`) is differential even
  though the heap owner remints destination versions. Matched rows carry exact
  retained Values provenance, including separate occurrences sharing a buffer:
  their placement survives; genuinely new rows use current fastupdate. The
  transport partitions already-built typed postings without repeating SQL/key/
  token extraction and freezes matching build-output flags before attachment.
  Ordinary nonconcurrent refresh is a genuine bulk-main rebuild.
  `CleanGin` bulk-merges already-extracted pending dictionaries into main without
  replaying SQL expressions/predicates. It preserves exact Entries, membership
  and construction debt. The returned technical estimate is ceil(U/128), where
  U is deduplicated pending token-posting incidences across all columns, including
  NULL/empty categories; it is not physical PostgreSQL pages or a row count.
  `GinMaintenance` prepares COW owner/savepoint/history/prepared output placement
  and candidate/receipt transports. Core owns cohort locks, readmission and the
  single callback-free installation; private rows never enter shared membership.
  Output admission uses exact enrolled descriptor/receipt pointers or strict
  actual `IndexAdmission` certification, never semantic `OutputMatches` alone.
  `fastupdate` defaults on. Off routes later writes to main but never flushes old
  pending work. SET/RESET/LOCAL retains typed/lexical configuration without
  rebuilding; inquiry and separate ALTER replay preserve explicit defaults,
  spelling and order. PostgreSQL insertion uses the session threshold unless an
  index reloption overrides it (default 4096KB). Native `gin_pending_list_limit`
  retains intent only: no automatic threshold/autovacuum or physical page
  algorithm is introduced. Native metadata numbers the same captured-header
  visible heads and exact-version-deduplicated repaired Additional rows as
  relational export. Source positions still validate genuine memberships and
  postings; exact row/version identity selects surviving pending placement.
  Repaired exports absent from source membership acquire ordinary rebuilt main
  placement, never fabricated pending work. Capture leaves source roots/backing
  unchanged; destination split reconstruction consumes destination-produced keys.
  JSONB GIN classes 10090 `jsonb_ops` (flag+payload key/value lexemes) and
  10091 `jsonb_path_ops` (4-byte path hashes) are lexeme providers: role 2
  extraction walks the immutable jsonb tree, and the dedicated strategy entry
  point serves `@>`, `?`, `?|`, `?&` plus conservative `@?`/`@@`. The scalar
  jsonb/jsonpath matcher remains the only SQL authority; `GinJSONBCandidates`
  exposes posting probes with mandatory recheck and no planner path.
  `pgstattuple.go` estimates tuple/page metrics from current logical roots and
  already-produced private outputs. Published logical removal stops contributing
  at once; pgstattuple dead index count is zero because roots have no killed-item
  marker. No archive,
  historical-key reconstruction or predicate/expression evaluation is required.
  Private outputs and current keys retain their normal compatible-generation
  transport; immutable saved roots remain usable. GiST root-only scans count no
  tuples, and hash reserves a metadata page and at least two estimated buckets.
  `ClassOptions` captures each key's explicit siglen independently; relation
  fillfactor/buffering and key siglen retain logical configuration without
  physical pages. Catalog inquiry and replay preserve explicit defaults and
  absent defaults distinctly. Column capabilities use the selected catalog's
  actual family ordering/fetch/compress support, not fixed class names.
  SP-GiST ordering members may match the class input on either operand; GiST
  retains its left-input and distance-support requirements. Both require a
  live ordering-purpose member and a sortable result family.
  SP-GiST keeps its distinct method/class identity, accepts one key plus INCLUDE
  and relation fillfactor only. Its seven classes preserve exact typed payloads,
  including polygon values despite BOX storage metadata. Distance eligibility
  uses actual ordering family members and their sortable result family; returnability
  uses SP-GiST compression metadata, never GiST callback slots.
  LIKE and partition copies retain immutable relation and per-key configuration;
  their roots are rebuilt against destination row and descriptor identities.
  Ordinary GiST and SP-GiST partitioned indexes use the same parent/child publication and
  attachment path as partitioned constraint indexes.
- **Private physical identity:** `UniqueRowDeleted` accepts an effective captured
  `RowVersion`, including its repair stamp, and delegates to `PrivateDeletes.Ends`.
  `BuildVisibleStore` captures genuine repaired additional versions
  once, preserves untouched siblings and suppresses a replacement Base only at
  its captured repair-admission stamp. Ordinary private heads retain their
  existing owner visibility. Association validation matches exact versions and
  actual predecessor chains, never a unique-RowID assumption; captures share
  immutable payload backing.
- **Compact B-tree statistics** (`pgstatindex.go`): `CapturePgstattupleStats` borrows
  one effective immutable root, parent versions/ends, commit frontier and actual
  private produced outputs. It validates row/version associations, counts resident
  live/dead memberships once and never reevaluates predicates or expressions.
  Removed committed memberships are absent, not invented dead heap history.
  The admitted attribute-only row-backed lane reads actual key slots directly.
  `CompactBtreeLayout` is a deterministic aggregate-packing approximation: each
  item weighs 20 bytes plus shared storage datum slots (NULL 0, positive fixed
  widths rounded to 8, variable slots 32), including INCLUDE attributes. Pages
  are 8192 bytes with 40 bytes overhead; leaves are ceil(weight/8152), internal
  fanout is 256. Metapage block 0 precedes leaves then bottom-up internal pages;
  the last block is root (one leaf uses block 1). Size is all pages times 8192.
  Empty captures use version 4, level/root 0, size 8192 and NaN density and
  fragmentation. Nonempty density is round2(100*weight/(leaves*8152));
  fragmentation, empty/deleted pages are 0. No retained page/split history is
  modeled, including DELETE-to-empty. Counts and inputs are exact; these page,
  byte, level, version and density outputs are explicitly the compact model,
  not physical inspection. Arithmetic is checked and loops honor cancellation.
- **Compact hash statistics** (`pgstatindex.go`): the shared capture samples the
  immutable effective hash root by summing bucket lengths, including duplicates.
  It never visits entries, heap rows or private projections. Live items are this
  simulated root sample and dead items are zero, not transaction-visible or
  LP_DEAD accounting. Private DML remains callable without forcing publication.
  `CompactHashLayout` uses version 4, four base bucket pages, one bitmap page,
  zero unused pages, 8152 usable bytes per page and 20 bytes per sampled item.
  With N items, P=max(4,ceil(N/407)); overflow=P-4 and free percent is
  100*(8152*P-20*N)/(8152*P). Checked arithmetic and bounded bucket cancellation
  require no per-entry allocation or physical page/split history.
- **Deterministic GIN statistics** (`pgstatindex.go`): shared capture requires a
  coherent genuine GIN root, matching current retained key bindings and parent
  identity, and exact catalog GIN AM. `pgstatginindex(regclass)` returns `version=2`,
  `pending_pages=0`, `pending_tuples=0`, a deterministic physical approximation
  independent of genuine logical main/pending inversion and `CleanGin`.
  `GinPendingIncidences` counts token-posting incidences, not pending heap tuples;
  cleanup's technical page estimate is not PostgreSQL physical pending pages.
  Capture remains O(keys), borrowing root metadata only: no parent projection,
  heap/entry/token/posting scan, recipe evaluation or row-proportional allocation.
  Capture tests cover immutable contents/backing and zero-allocation empty/populated
  roots with both fastupdate-off main postings and default-fastupdate pending postings.
  Private DML remains callable without publishing roots. These fields are not
  logical live/dead counts; PostgreSQL fastupdate inserts may produce nonzero
  pending heap-tuple/page counters.
- **Logical verification** (`amcheck*.go`): `CheckBtree` consumes an immutable
  `AmcheckCapture`, validates actual stored order/identity/positions and parent
  maps, searches the real baseline root independently, and checks demanded heap
  representation and visible uniqueness using its bound ordering and recipes.
  Under HeapAllIndexed, genuine retained entries referencing exact dead slots
  report XX002 `index entry references killed heap tuple for index "name"`.
  Ordinary MVCC-ended entries are not this finding. Surgery never changes the
  captured root, entries, keys or membership to manufacture or hide corruption;
  genuine later vacuum/rebuild follows existing maintenance rules.
  Capture pairs the baseline with coherent heap/end/frontier authority and
  real private row outputs or the admitted implicit row-backed lane. No root is
  rebuilt, repaired or published by verification. Uniqueness scratch is never
  verification-root authority. Engine orchestration owns scoped heap-then-index
  AccessShare (basic) or Share (parent) contributions and their reverse cleanup.
  Physical B-tree levels/downlinks/sibling pages, physical TIDs, WAL/LSNs and
  standby/recovery are non-engine mechanisms: logical verification neither
  fabricates them nor reports their physical invariants as checked. Private
  write overlays have no stored ordered tree to descend.
  Missing-heap diagnostics use the shared logical-version TID encoding; no
  physical index coordinates or LSN Detail are synthesized.
  `CheckGin` binds the actual GIN key/class/collation generations and validates
  both main and pending dictionaries: categories, canonical payloads, strict
  token/posting order, exact entry-to-store ownership and NonNull membership.
  Each entry belongs to exactly one portion across all indexed columns; neither
  duplicate placement nor a cross-column split is accepted. Independently
  extracted retained keys must find their actual portion's postings; ordinal
  stamps deduplicate repeated tokens without merging or building another tree.
  Real private produced output, successor and deletion authority is checked
  separately, never demanded in committed dictionaries. Every GIN insertion
  requires its actual output/placement; no missing-output row extraction or
  callable replay is allowed. Scans, extraction and searches honor cancellation.
  Authenticated installed scalar bindings admit ordinary tokens only at the
  exact concrete key generation and payload-free GinNullItem; array-only
  NULL-element/empty categories and lexeme carriers cannot become scalar tokens.
  Malformed retained GIN data reports XX002 `GIN posting invariant violated for
  index "name"` without invented physical coordinates. Physical GIN pages,
  links and pending-list pages are not represented by this logical checker.
- **Publication, snapshots and ALTER TYPE rebuilds** (`publication.go`,
  `row_publication.go`, `layout_*.go`, `snapshot*.go`, `remint.go`,
  `effective_root.go`, `alter_type.go`): `PrepareIndexPublication` over
  `RowPublication` (materialized by `PrepareRowPublication` outside `Engine.Mu`
  over private roots), `ValidatePrivateExpressions`, `EvolveIndex*`,
  `CaptureLayouts`, `CaptureSnapshot`, `RemintStorage`, `EffectiveRoot` (a
  transaction's private root, else the committed one), `AlterIndexBuild` and
  `RebindExclusionOperators`. The engine fills capture inputs under `Engine.Mu`
  after its epoch checks and holds the lock across the call.
  Current captured headers filter killed versions in builds, probes, statistics
  and logical transport. Final row publication retains exact slot death while
  allowing already-admitted writers to finish; only death is ignored during
  predecessor liveness admission, never real ends, shape or identity checks.
  `ValidateAlterIndexBuilds` invokes its owner's execution callback only after
  the incumbent late selector chooses actual builds and admits their snapshot.
  Table ALTER supplies the existing restricted table-owner environment for
  key/predicate execution; empty selections do not enter it. Disclosure still
  uses the separately captured initiating view, and physical root preparation
  reuses produced entries without replaying callbacks.
  Key reconstruction resolves the destination type's default within the declared
  method and rejects an absent default before replacing the captured class;
  it never publishes a zero class for a later provider to interpret.
  Constructors (including LIKE) and TYPE reconstruction use the allocation-free
  `ConstructorColumnStorage` leaf, also used by Dump to predict recreation.
  Every simple key/INCLUDE occurrence copies both destination column policies;
  expressions start with result-type defaults. Only an actual different opclass
  key type resets both policies to that type's default storage and empty compression;
  an explicit same-type key preserves the copied pair.
  Later ALTER policy changes intentionally update only the first simple match,
  without rebuilding executable roots.
  CLUSTER compares the immutable root-owned last-complete-construction recipe
  with current logical semantics, not merely the logical descriptor's captured
  routine. Routine publication preserves physical history while reminting logical
  captures. Incremental merge/delete and vacuum retain that same history.
  Readiness/validity-only COW also retains the original construction record.
  A fresh comparison build may therefore have different history but identical
  logical root data. Structural tests validate each root's construction against
  current recipe semantics independently, then compare every logical root field;
  missing, forged or indebted provenance never passes as equivalent history.
  Layout and snapshot transport add exact source/destination logical context
  without clearing prior recipe debt; stale physical providers are never executed
  through provenance. Only a genuine full build starts clean history.
  A reminted expression/predicate recipe gets that mapping even when its referenced
  dense slots did not move. The btree path shallow-copies only the root's provenance
  wrapper and shares physical entries/membership; recursive source checking keeps
  clean histories clean and never launders existing debt.
  CLUSTER selectively cooks and rebuilds changed recipes through ordinary overlay
  builds, uniqueness validation and retained candidates; unchanged siblings and
  heap storage remain untouched. Baseline private-write/domain outputs remain
  authoritative; no CLUSTER-specific heap/output transport exists.
  Progress bindings select only actual captures. Optional IndexStart/IndexComplete
  notifications bracket real index work; scan completion alone is not success.
  Overlay completion follows successful uniqueness validation, baseline retention
  and row-output attachment, while preserving partition-tree completion.
  Callbacks must never enter engine/session/publication locks.
- **Catalog publication captures** (`publication_stores.go`,
  `overlay_capture.go`): `CandidatesMatch` (the concurrent root-candidate
  fence of catalog publication and of overlay capture; the caller picks each
  parent's store), `CaptureStagedIndexStores` (the captured stores, including
  staged indexes' parents), `PublicationBuildStores` (build stores, rebuild
  set and source roots over a `storage.Publication`) and
  `SelectOverlayCapturesLocked` (the indexes a transaction overlay rebuilds,
  with their read fences). They take no lock; the engine calls them under
  `Engine.Mu` and keeps the fence order and the `Conflict` it returns.
  `PreserveRoutineRoot` additionally consumes the stored owner's opaque routine
  publication proof and actual engine-owned source descriptor, parent, root and
  row-storage window. Its private candidate receipt retains earliest committed
  pins through authenticated private chains. `RoutineSourcesMatch` checks those
  exact source/root/storage pins at capture and final installation, independently
  of unchanged strict `MatchesIndex`/`SameIndexKeySemantics` checks. Replacement
  does not rebuild populated entries; a genuine later builder explicitly
  supersedes the receipt through ordinary candidate recording.
  At `PrepareIndexPublication`, authenticated source lineage also permits a
  lazy copy-on-write retag of already-produced pending-write index outputs.
  Membership (including nonmembership), key backing and row/version identity
  remain unchanged; absent outputs are never fabricated. Genuine rebuild
  intents instead evaluate current recipes and cannot reuse transported output
  as fresh truth. Ordinary `OutputMatches` remains strict.
- **Write-time records and enforcement** (`write_candidate.go`, `insertion.go`,
  `unique*.go`, `exclusion_*.go`, `constraint.go`): `WriteCandidate`,
  `WriteBatch` (optional caller-owned `Accepted` physical-version output slots
  follow Values through Prefix/Slice; Set clears stale admission, and nil keeps
  the ordinary write path allocation-free; an explicitly known scalar
  `PredecessorHint` belongs only to a single-row replacement window, survives
  unchanged windows and is cleared by expansion or different slices),
  `UniqueWriteBinding`, `UniqueKeyHash` (the opaque key the engine pins in the
  lock manager), `ValidateUniques`, `ValidateExclusions`, the exclusion
  projection/event records, and `UniqueKeyDetailAllowed` (key disclosure over
  `access.View`; a nil environment means trusted publication).
  A candidate's optional `Logical` reference is immutable capture-owned
  insertion evidence, not index authority or an accepted table row. WriteState
  preserves it through Append/Set/Prefix/Slice and RETURNING transfer; only the
  real accepted stage joins it to the successful-effect projection.
- **DDL records and presentation** (`control.go`, `metadata.go`,
  `maintenance_*.go`, `set_constraints.go`, `using_index*.go`): `DefinitionSQL`
  (`pg_get_indexdef` and Dump), `Metadata`/`IndexesMetadata`, the
  `MaintenanceGrant` lock plan, SET CONSTRAINTS name selection and the
  `USING INDEX` records.
  `ProjectIndexFacts` serves `pg_index` without demanding `DefinitionSQL`.
  Shape/provider ownership, original dependency lifetimes and frozen node
  structure remain validated, but a compatible routine body replacement does
  not invalidate catalog flags or substitute newer body nodes. The combined
  `ProjectIndexMetadata` projection retains strict SQL deparse validation for
  callers that actually request it; executable validation is unchanged.
  Both metadata projections require a nonzero index owner from the captured
  engine's principal domain and matching parent ownership. Reduced fixtures
  injected directly into catalog state must supply that genuine ownership too;
  matching zero owners are not valid metadata identities.
  Existing-descriptor validation does not reapply CREATE's volatility policy:
  maintenance may reconstruct a compatible current STABLE or VOLATILE routine.
  `ValidateIndexDeclaration` applies IMMUTABLE admission only to newly cloned
  keys/predicates, including LIKE-backed UNIQUE/PK and synthesized partition
  children. Partition mapping, existing-child reuse and explicit ATTACH are
  compatibility operations, not new declarations. Strict stored-program,
  identity, type and provider validation remains required in both cases.
  `Metadata.IndIsClustered` projects the single persisted
  `catalog.IndexDefinition.Clustered` scalar. Preference changes do not replace
  keys, executable providers or storage roots; table ALTER owns their atomic
  staged selection and uses `Property` for method eligibility.
  UNIQUE/PK timing remains on the parent constraint definition, not standalone
  index state. Metadata, LIKE, partition cloning and USING INDEX replay resolve
  the exact backing identity and retain those defaults; root validation still
  rejects duplicate committed keys regardless of initial timing.
- **Partitioned indexes** (`partition_*.go`, `row_history.go`): coverage,
  ATTACH checks, `PartitionConstraintDropActions`, captured depth-first
  `PreparePartitionConstraintDrop` programs, `MapPartitionIndex` and Dump replay.

## The DDL host

`Host` serves CREATE INDEX, ALTER/DROP INDEX, REINDEX and constraint-index
staging for one statement: `ddl.Host` plus object removal, diagnostics, the
stage and its index slot, `ACLOutput`, `DefinitionReader`, `ValidityTarget` and
the `TransformIndexExpression`/`TransformIndexPredicate` and matching `Complete`
methods (the engine builds binders; this package never does). `tablecmds.TableHost`
(`index_host.go`)
implements it. **Every method runs with `Engine.Mu` held, never takes or
releases it, and never waits.**

TEMP creation transforms its predicate, then expression keys, before consuming a
tablespace destination. It selects/checks that destination before access-method
lookup and physical-definition validation. `ExpressionTransform` holds analyzed
definition, input, dependencies and column uses only for this operation; completion
performs late virtual-use, coercion, volatility and rendering checks without
rebinding. Only completed recipes enter descriptors. Permanent admission order
and noncreation reconstruction semantics remain unchanged.

Narrower contracts:

- `KeyHost`: key binding and operator class lookup, no stage (the analysis host
  and partition key binding pass a stage-less core `DDLHost`).
- `DropStage` (`*CatalogStage`): DROP INDEX staging, which runs without
  `Engine.Mu`; the removal takes it. Its required relation-projection operation
  captures actual entering constraint identities before checked replacement,
  retaining records privately until successful catalog transfer. Each index
  retirement explicitly supplies its ownership direction: only index-driven
  removal marks a retired backing constraint NORMAL. Constraint-driven
  recursion does not promote descendants; SQL target selection alone marks
  ORIGINAL, independently of the expanded index admission/removal set.
- `ValidityTarget`: the partitioned-index validity pass. `UsingIndexStage`:
  attaching a prepared USING INDEX, no lock (`PrepareUsingIndex` takes only a
  `ddl.Allocator` and runs under `Engine.Mu`).

Entry points:

- CREATE INDEX: `PrepareCreate` (authority, privilege, method, clauses,
  predicate, name, unique providers, coverage; `CreatePlan.Skip` for IF NOT
  EXISTS), then `StageCreate`.
  Standalone UNIQUE indexes admit immutable derived keys beside simple columns:
  `ValidateUniqueProviders` resolves every key's usage through `KeyUsage`
  (expression result usage), so opclass, bpchar cross-input, Inet/Cidr and
  grouping admission apply identically, while constraint-backed UNIQUE/PK
  backing indexes remain simple-attribute only.
  Ordinary creation in explicit transactions uses that same path, including
  UNIQUE and shipped methods/options. The engine captures the owner-private
  parent and current execution authority under its canonical Share grant.
  Names/descriptors/dependencies and candidate roots remain private until the
  existing combined DDL/DML publisher commits; no live publication/undo path
  exists. Baseline candidates exclude own writes, whose frozen `IndexOutputs`
  contribute once during enforcement/publication. Read-only and concurrent
  transaction restrictions remain engine execution-admission responsibilities.
  For unnamed ordinary creation the engine also passes an owned snapshot of
  other owners' granted relation-name claims, captured outside engine/session
  mutexes. `PrepareCreate` includes these names in its existing generated-name
  predicate without exposing other private definitions. Named explicit
  post-capture catalog uniqueness errors, including IF NOT EXISTS, propagate
  unchanged; preexisting-name behavior and autocommit recovery stay distinct.
- DROP INDEX, ALTER INDEX SET/RESET, ATTACH PARTITION and REINDEX:
  `StageMaintenance`, `ValidateMaintenance`; rename delegates to the shared
  relation rename owner. Ordinary DROP/rename/options use the existing private
  Journal and combined DDL/DML publication path in explicit and retained
  transactions. REINDEX retains its separate transaction boundary.
  A genuine REINDEX reserves a fresh logical locator, advances descriptor revision
  exactly once and owns a new ready/valid/live SQLIndex definition. Its immutable
  key/include/exclusion backing and constraint/predicate providers are retained.
  A later rename retains that newly published recipe, not its pre-REINDEX pointer.
  FULL uses ordinary genuine root builders against its new heap,
  including expression/domain/predicate evaluation and unique/exclusion checks;
  it never substitutes ordinary VACUUM's produced-key remapping.
  Genuine FULL builds mint current `Construction` provenance; ordinary vacuum
  remapping still preserves its existing physical history without recipe
  evaluation or laundering declaration debt.
  `PrepareIndexRecipe` refreshes SQL callable declarations structurally through
  that exact recipe's dependency manifest before executable preflight. A current
  CREATE OR REPLACE body is executed under current authority without mutating
  stored expressions, rebinding SQL names, or relaxing retained-statement carrier
  checks; incompatible signatures, foreign references and unowned calls fail.
  Manifests without SQL callable dependencies keep their exact cooked executable
  and provider seam; they do not enter the structural refresh pass. In particular,
  prepared body-free index recipes still execute at the real completion boundary.
  Internal recipe callers may provide only a live statement host, without an
  explicit catalog. Procedure lookup then resolves the canonical routine-admission
  catalog once on the local recipe context; refresh, coercion and executable
  admission share it. Missing/ended authority fails, explicit captured catalogs
  remain real fences, and the outer evaluation's authority is never overwritten.
  SQL call reconstruction updates `Info.Property` and uses the existing
  `Rebuild`/provider-validation contract; it never mutates stored argument arrays.
  `TestIndexRecipeLiveHostCatalog` lives with composed stored-definition tests in
  `core/enginetests/stored`, using the current statement Host adapter.
  `TransformIndexOptions` owns immutable logical option validation/ordering;
  metadata-only edits retain identity/providers/key roots. Bloom's effective
  signature-option changes instead use the existing fenced rebuild publication.
  Binding: `LookupClass`, `BindKey`,
  `BindAttributes`, `RejectVirtualUses`.
- Constraint indexes, called by `table`: `StageUniqueDefinitions`,
  `StageCreateUniques`, `StageExclusion`, `StageCreateExclusions`,
  `RebuildAlter{Uniques,Exclusions}`, `CaptureUsingIndexes`,
  `PrepareUsingIndex`, `StageUsingIndex`. Callers own the table edges: LIKE
  sources before unique staging, NOT NULL rebuild between the unique and
  exclusion rebuilds, primary-key NOT NULL before `PrepareUsingIndex`.
- Partitioned indexes: `StagePartitionIndex*`, `PartitionIndexValidity`,
  `PreparePartitionConstraintDrop`/`ConstraintDropPlan.Apply`. General ALTER
  retains this typed late program in its preparation/stage instead of
  rediscovering children after primary removal. Current owner/domain/epoch and
  original backing identities are validated; a program is consumed only once.
- `Property`: `pg_index_has_property`, `pg_index_column_has_property`.

## The write host

`WriteHost` (implemented by `*statementHost`, `statement_host_index.go`) wraps
write-time unique and exclusion enforcement in `Engine.Mu` windows and lock
waits. **Each method either takes `Engine.Mu` itself for one window and never
waits (the caller must not hold it), or waits with the caller holding nothing;
none takes a callback or evaluation context.**

- Windows: `UniqueParent`, `BindingAuthority` (stale means rebind),
  `CaptureUniqueProbe`, `ExistingIndexRoot`, `CaptureExclusionScan`,
  `ExclusionScanCurrent`, `PublishExclusion` (epoch check, row identity, edit
  and install in one window; takes the candidate by value and returns the
  reserved row for `WriteCandidate.RecordExclusion`, so no candidate escapes),
  `DiscardExclusion`, `OwnExclusionKeys`, `AdmitExclusion`, `EndExclusionRow`.
- Waits: `ClaimUniqueKey` (pin, claim, `replacementMu` release and
  re-acquire, program readmission) and `WaitTransaction`.

Probe and scan captures return the write authority read in the same window as
an `access.View` by value; the engine rebuilds an access environment from it
only for key disclosure on error paths and the ON CONFLICT binding check.

Pure decisions over captured values: `BuildUniqueWriteBinding`,
`UniqueRootStart`, `ExistingRootOutput`, `VisitExclusion` (committed-root and
pending passes; asks its context source for a context per comparison),
`ExclusionDeferred` and the projection edits (`ExclusionEdit.Apply`,
`ExclusionProjection` methods, `ExclusionShapeEntries`). Each edit works on a
`ProjectionLocked` copy and replaces slices and maps, so an installed
projection never changes.
Projection ends and incremental retirement sets are keyed by actual VersionID
with their RowID association. Two repaired siblings can therefore acquire ordinary
new index entries and end independently without shadowing one another. Projection
edits remove only exact selected/base versions; another sibling remains a genuine
constraint candidate. Repair itself does not recreate missing index entries.

Deferrable UNIQUE/PK reuse this projection/admission machinery with their real
equality ordering and NULL semantics, not synthetic EXCLUDE definitions or eager
exclusive key claims. Equality-root visits use the bound lower key boundary.
Only potential conflicts enlist exact row/version events; uncontested first
admission gets no retrospective obligation from a later peer. Due rechecks use
current committed and private/dirty visibility and the existing completion
tickets, then revalidate captures after every wait.


`BuildUniqueWriteBinding` also captures `ConstraintEffectsRelevant` for incoming
and outgoing FKs, exclusion and deferrable-unique obligations in its descriptor walk.
FK validity does not disable new-write enforcement; unenforced FKs supply no
effect obligation. Incoming references may use standalone unique indexes.
This classification belongs to the binding's
exact `Ref`/`Parent`/`Catalog` authority. Existing per-leaf insertion groups retain
it across routed rows; a changed reference, parent or catalog root requires rebind.

## State slots and registries

- **Registries.** `WaitRegistry` (CONCURRENTLY build waits) and
  `ExclusionRegistry` (shared dirty UNIQUE/PK/EX index projections plus a
  generation) have no mutex: `Engine.Mu` guards them and every method has the
  `Locked` suffix. The engine holds one of each (`Engine.IndexWaits`,
  `Engine.Exclusions`). Every projection change advances the generation, so a
  scan captured with `ScanLocked` fails `CurrentLocked` and is retaken; the maps
  stay private so nothing changes a projection without advancing it.
- **Journal slot.** `Journal` implements `session.IndexSlot` (private logical
  roots, candidates, REINDEX intents); `Clone` copies its maps while sharing
  immutable roots and candidate pins. Savepoint marks retain older journals.
  Rollback-to restores the catalog/row prefix and lock checkpoint; full rollback
  discards private index names, descriptors, dependencies and roots.
  DROP removes only selected private descriptors/dependencies/root candidates;
  rename/options preserve key semantics. Dump sees committed state and replays
  persistent options with qualified ALTER after final names and attachments,
  including indexes declared by constraints, without broadening CREATE syntax.
- **Savepoint constraint mark.** `ExclusionMark` implements the incumbent
  `session.ExclusionMarkSlot`, retaining `ConstraintModes` and the shared dirty
  projection. Its singular vector discriminates concrete index, FK and user
  trigger payloads; defaults, ALL and per-OID modes are independent of persistent
  descriptors. User payloads contain immutable accepted images/catalog provenance
  and an optional principal, never callable authority or runtime frames.
  `ExclusionMarkSlotOf` keeps exactly zero state nil. Append detaches inherited
  backing; `Scope.ClaimConstraintEvent` detaches and removes membership before a
  callback, preserving the original vector for consumed-event rollback.
  SET name selection shares one first-matching-schema loop for table and trigger
  constraints, using actual independent conname/OID, not tgname or clone ancestry.
- **Statement and stage slots.** `Scope` and `Stage` are the slot records; the
  accessors on `StatementScope` and `CatalogStage` stay in the engine.

## Invariants

- **Immutable roots.** Roots are ordered entries, unsorted GiST/SP-GiST memberships,
  equality buckets, typed inverted postings or logical range summaries, maintained at the same
  Finish/commit frontier as rows. Derived keys evaluate
  at Execute, after predicate qualification and before rows are installed;
  failed batches, waits and builds preserve predecessor rows and roots.
  Maintenance rebuilds roots atomically without changing rows or identities.
  Partial membership and stored-key ordinals use canonical exact-version lookup.
  The ordinary RowID fast path retains one genuine entry; collision maps own only
  additional versions. Construction, probing, verification and maintenance consume
  the same identity helpers, including hash-entry backing pointers.
- **Class identity.** Operator classes resolve through current catalog
  descriptors, exact method OID and real family members; the physical method
  enum is not class identity. Declared identity compares with
  `sqltype.IndexClass.SameIdentity`, not structural equality. Grouping
  compatibility is captured once in the ordering's key mask, never per row.
- **Family members.** `InternalOwner` is the insertion-time class or family
  address, not a current-class search. Required GiST support1,2,5,6,7 belongs to
  the creating class or, for ALTER FAMILY, to the family; SP-GiST support1–5
  follows the same hard-ownership rule. Their operators and optional support stay
  loose. Btree/hash retain required same-type versus
  optional/cross-type rules, including physical403/405 ALTER lookup rather than
  SQL-alias adoption. The common closure rejects standalone hard-member deletion
  and promotes dependencies through the actual owner. Dump folds class-owned
  members into CREATE CLASS and emits family-owned members as ALTER FAMILY.
- **Exclusion events.** Deferral events enlist only actual potential conflicts.
  Savepoints retain immutable event/mode/projection snapshots; transient modes
  and events are never serialized.
- **Partitioned exclusion.** Each declaration covers every partition key of its
  target and descendants with actual equality; virtual (partitioned) indexes own
  metadata only and leaf providers enforce conflicts. CREATE and EXCLUDE
  ADD/DROP reuse staged recursive index production.
  Compatible unowned exclusion providers are adopted without changing identities,
  owned operators, timing or physical roots. Table ATTACH requires valid matching
  constraint-backed providers; recursive ADD can retain an invalid virtual subtree
  without healing its gaps. Explicit matching index ATTACH is idempotent and
  preserves diagnostic precedence for genuine mismatch and earlier ancestry errors.
  Exclusion `NoInherit` is declaration provenance, independent of current locality:
  local declarations start true, fresh inherited providers false, and attachment,
  detachment and immutable copies preserve it.
- **BRIN ranges.** A storage version-vector slot is one logical page; range
  ordinal divides the actual publication slot by the root's effective width.
  Summarized keys retain conservative independent extrema and NULL flags.
  Missing summaries always admit candidates and every candidate needs SQL
  recheck. Current `autosummarize` closes previous occupied unsummarized ranges
  on boundary crossing. SET/RESET width changes intent, not existing partitions;
  REINDEX/layout rebuild takes current intent. Destination owner repair rebuilds
  before locked remint; exact slot-preserving transport does not replay SQL.
  VACUUM rebuilds conservative destination summaries from its slot manifest and
  produced source summaries, propagating unsummarized contributors.
  Baseline transport consumes the caller's shared row-position map rather than
  allocating another map per BRIN index.
  See [BRIN design](../../../docs/design/PG18-INDEX-BRIN.md).

Snapshot `Transport`/`TransportRows`, `RemintStorage` and
`TransportEmptyIndexStorage` consume the actual `catalog.Read`, not an ownerless
view reconstructed from its state. Imported and generation-rebound keys require
the same registered installed-provider authority as ordinary binding; transport
does not infer a stock provider from a name or retained discriminator. The
caller supplies its existing owner with the candidate state (`WithState`).
Captured keys, source rows and index expressions remain immutable; this changes
authority retention, not predicate/expression evaluation during transport.
`RemintStorage` owns the private replacement catalog. Symbolic import strips
source support seals, so remint strictly binds destination GIN classes and COWs
only changed relation/key descriptors before transporting their roots. Both
payload portions and construction history then capture the same actual
destination descriptor; cleanup does not refresh or shape-admit detached source
captures. Retained TEMP indexes keep their separate source-authorized rebind.
Ordinary `Snapshot.Transport`/`TransportRows` do not mutate their catalog.
Whole-storage deletion and detached template TEMP metadata share the same
method-specific empty-root constructor. It rebinds providers to a real private
empty store without evaluating SQL keys; remint retains normal descriptor,
provider and unique-root validation rather than importing source TEMP entries.

Independent destination remint assigns fresh row/version identities to every
captured visible tuple, including repaired siblings that shared one source RowID.
It clears source ends, discarded/predecessor roots and repair/slot authority;
ordinary insertion visibility owns the destination. Preserving FULL/type rewrites
retain their existing identity contract and do not use this import cutover.

## What stays in the engine

The statement executors (CREATE INDEX with its CONCURRENTLY phases, ALTER
INDEX, DROP INDEX, REINDEX) are [`indexcmds`](../indexcmds/README.md)'s. Core
keeps the runtime: the `Engine.Mu` and capture windows, lock admission
(`acquireIndexMaintenance` over the grant plan), unique key pins, the registry
instances, savepoint capture/restore, Finish order, publication and Restore
storage (`prepareIndexCatalog`). Family code builds a `tablecmds.TableHost`
for production callers; tests call `index` functions directly with one. The
write-time loops
(unique probe/claim, exclusion probe/admission, ON CONFLICT arbiters) are
`dml`'s and reach this host through `dml.Host`.

## Testing

Private deletion/capture ownership and sibling repair-admission stamps use
`RUN='^TestIndexPrivateDeletionVersionCapture$'`.

Caller-owned accepted-version and exact predecessor-position windows use
`RUN='^TestWriteBatch(AcceptedVersions|PredecessorHint)$'`.
Real capture-owned insertion references and failed uniqueness publication use
`make check RUN='^TestLogicalStream'` in `core/enginetests/logical`.

Compact statistics layout arithmetic and cancellation units use
`RUN='^TestPgstat(index|hashindex)Approximation$'` from `enginetests/`.

Package units cover column capabilities, `EffectiveRoot`, savepoint exclusion
marks, access-method properties, operator-family validation and BRIN summaries.
Index tests build an engine and live in `internal/engine/indexcmds` (index DDL),
`internal/engine/core` (unique and exclusion probing), `enginetests/` or, until
they move, `internal/engine`. BRIN structural and composed lifecycle units live
in `enginetests`. Their real callable/partial fixtures observe domain CHECK
execution through sequence state: VACUUM and same-ID generation transport must
not replay recipes, while admitted replacement repair produces destination
truth once before locked remint. Replacement also forces retained TEMP identity
collisions and checks later summary maintenance. Publication inspects both
rollback isolation and the root installed by a successful private-write commit.
SP-GiST structural coverage includes destination-domain ownership and exact
RowID/VersionID/position/value membership after real Dump/Restore with a retained
TEMP relation collision. Source, removed target and subsequently replaced roots
remain immutable; later writes in either engine cannot alter the other engine.
Bloom composed owner units live beside core and use actual published roots,
command-admitted scan preferences and deterministic context boundaries. The
collision proof finds a real one-bit collision in the production 17-value root,
then consumes the nonmatching candidate through the original predicate. Native
controls assert destination bindings/backing and unchanged source ownership.
Run concurrency,
exclusion and unique-claim selections with `RACE=1`. Private units assert
support IDs/routines and root membership, never ORDER BY results as evidence of
index use. Public SQL: [reference/indexes](../../../reference/indexes/README.md#testing)
(add `REFERENCE=1` for the oracle).
Retained-root snapshots preserve nil versus nonnil-empty entry/key slices.
The compound presentation proof checks snapshot equality before performing any
operation, then retains the same strict deep-equality and shared-backing checks
after relocation, membership changes and indexed writes.
The network presentation fixture retains its six plain-column lanes and adds a
genuine callable MACADDR8 comparison key requiring insertion output. It checks
actual produced-key backing and descriptor migration without forcing allocation
on the plain-column row-backed path.
`TestBtreeGINCompoundHelperOwnerACL` covers actual explicit-ACL ownership with
dependent roots, empty/populated/private/rollback cases, coalesced owner grants
and unpublished invalid/canceled candidates. `TestBtreeGINCompoundRangeResources`
builds genuine 256/2048-key money roots, checks all broad ranges and exact
physical identities, equality backing, source immutability and nil-on-cancel.
Only probe work is measured: at most eight allocations and `192*N+8192` bytes
per call, with large-case growth at most twelve times small plus 8192 bytes.
Text-family owner fixtures independently specify whole ordinary empty/zero keys,
bpchar trailing-space equivalence without source mutation, unsigned character
and bytea boundaries, exact row/version/slot candidates, shared text/varchar
helper authority, immutable publication and destination-owned transport.
They inspect real payloads; public scan agreement is not index-use evidence.

**Frame names matched by tests.** `TestHashIndexMergeCancellation` matches
`.MergeHashIndexRoot` and `TestSerializablePublicationRealChangedPredecessorRetry`
matches `.MergeIndexRoot`. Rename those functions only together with the
fixtures.

```sh
make check RUN='^Test(Index|EffectiveRoot)'   # keys, clauses, properties, maintenance, hosts
make check RUN='^TestAmcheck'   # immutable verification, identity, capture and invocation environments
make check RUN='^Test(CandidatesMatch|DriverBatch|PartitionAttachPublicationFences)'   # catalog publication captures
make check RACE=1 RUN='^Test(Unique|Exclusion|UsingIndex|ConcurrentIndex|HashIndex)'   # enforcement
make check RUN='^TestBrinIndex'   # range summaries, consistency, publication and transport
make check RUN='^TestSPGiST'   # genuine method, membership, publication and destination transport
make check RUN='^(TestGINClassInventory|TestGINTypedTokens|TestGINInvertedRoot|TestGINJSONBTokens|TestGINJSONBQueries|TestGINJSONBCompare|TestGINJSONBDictionary|TestGINJSONBMaintenance)$'
make check RACE=1 RUN='^(TestGINPublication|TestGINTransport|TestGINVacuum|TestGINCancellation)$'
make check RUN='^TestBtreeGINCompound' # installed scalar bindings, postings and immutable lifecycle
make check RUN='^TestBtreeGINNetwork' # network class authority, exact postings and transport
make check RUN='^TestBtreeGINTemporal' # typed temporal equivalence, exact postings and sealed lifecycle
make check RUN='^TestBtreeGINText' # text-family typed equivalence, posting triples and ownership
make check RUN='^TestPgsurgeryFreezeIndexVersions'   # exact sibling deltas, projections, membership and probes
make check RUN='^TestPgsurgeryKill(RowPublication|UniqueAdmission)$' # exact death, real ends and structural predecessor admission
make check RUN='^TestPgsurgeryKillGinCapture$' # genuine split ordinals and immutable heap/header/index backing
make check RUN='^TestBloom'   # installed identity, real signatures, capture/recheck and controls
```

Operator families, classes and access methods:

```sh
make check RUN='^Test(OperatorFamily|OperatorClass|AccessMethod|BuiltinRoutineResultInventory)'
make check RACE=1 RUN='^TestOperatorFamilyPublication$'
make check RUN='^TestReference(OperatorFamily|OperatorClass|AccessMethod)'
```
