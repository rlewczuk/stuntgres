# internal/engine/core/catalogrt

The catalog source runtime: the `pgcatalog.Runtime` that system relation
producers read, the shared capture cache, keep filters for a capturing
SELECT, the catalog source descriptors and the `pg_shdepend` classification.
It is a leaf below core (ENGINE-SPLIT §3, L14.5).

## Layering

- Imports only packages below core: `access`, `analyze`, `authorization`,
  `catalog`, `exec`, `expr`, `fdw`, `ids`, `index`, `partition`, `pgcatalog`,
  `plan`, `publication`, `regobj`, `routine`, `session`, `settings`, `stored`,
  `table`, `view`, `parser`, `sqltype`. It never imports core, a family or the
  root.
- Core keeps the capture policy: `openCatalogSourceLocked`
  (`catalog_source_capture.go`) looks up `pgcatalog.ProducerFor`, chooses the
  per-OID catalog and role roots, the authority, presentation and unlock
  timing, and builds the runtime. Core also keeps the `Engine.catalogCaptures`
  field and the scope's `KeepQuery`, installed around the early metadata prewalk
  and again by `captureSelectSourcesLocked` for materialization, plus
  the shared-dependency root capture and the `Host` implementation
  (`catalog_runtime_host.go`).

## Contract

- `Runtime` implements `pgcatalog.Runtime` for one capture. `View` is the
  captured authority (`access.View`); `Input`, `Database`, `Temporary`,
  `Presentation` and the sequence overlay (`SequenceBase`, `SequenceOwn`) are
  the values the selected producer reads. It exposes values per call, never
  Engine, StatementScope, AuthorizationView or bindings.
- `Host` is embedded: the engine-level capture methods (cursors, prepared
  statements and transactions, session settings, parameter validation,
  maintenance, statistics, vacuum, COPY and CREATE INDEX progress, the frozen
  XID) are promoted from it. It is also the authority's
  `access.Environment`, gives its restricted relation kinds and the backing
  engine's catalog owner. Core implements it on one struct that also holds
  the `Runtime`, so a capture allocates once.
  `SequenceIOStatistics` supplies only an owned encounter count through the
  physical session's transaction reader, never provider/writer authority.
  `IOStatisticsReset` supplies one SharedIO reset timestamp through that same
  reader with operation-effective settings. Core releases Engine.Mu before the
  call; a needed full snapshot briefly reacquires it to capture the complete
  accessible database domain through the shared cumulative first-access helper.
  Only that capture samples a construction clock. Zero time is absent reset
  information, not permission to synthesize a read timestamp.
  Every pg_stat_io row uses this one cut, including its WAL capabilities;
  SharedWAL remains a distinct family. Neither capability leaks a live reader,
  provider, writer or authorization handle into the producer.
  The statistics-view visitor is distinct from the protected DATA visitor:
  core passes captured presentation and journal authority to `core/stats`,
  without applying DATA SELECT or backing-function EXECUTE to public views.
- A `Runtime` without a `Host` serves only the presentation methods: core's
  deparse functions and `BuiltinViewDefinition`. Its optional borrowed wrap width
  selects structural target layout through `stored.ViewTargetsSQL`; nil keeps
  default/pretty bytes and allocates no target collection. No presentation call
  retains the width, source authority or rendered target slice.
- Provenance is same-pointer authority (the engine's `catalog.Owner`), never
  equal names, OIDs or epochs. `ValidateCapture` checks the exact source and
  ref and the current principal before producing rows, even for an empty
  catalog; publication, statistics and subscription readers also require the
  backing engine's owner.
- Routine-default presentation validates the paired capture and exact
  Procedure, then renders checked expressions or declaration-owned finite
  literals through `stored.Deparser` with captured path, output and string
  settings. Finite source literals include the logical-decoding TEXT[]
  default, decoded by its captured array codec. Source literals retain their
  type and spelling across validated assignment coercion; no expression
  evaluation, provider binding or replay-renderer change occurs.
- `CaptureRows` constructs produced catalog `exec.Source` values. An empty
  producer result is still an owned anonymous rowset; an admitted undemanded
  input instead uses the executor's existing typed-empty materialized source.
- `CaptureCache` keeps the latest `pg_type`, `pg_proc` and `pg_depend` rows per
  engine, owned by `Engine.Mu`, keyed by the published `*catalog.State`, the
  published `*access.RoleRecord`, the source relation and its ref.
  `CaptureLocked` serves statements reading the published catalog and roles
  read-only; private transaction catalogs or role records capture fresh.
  Errors are never stored, and a hit still returns the caller's context
  error. The slot also keeps an `exec.CatalogKeyIndex` of its rows; hits and
  misses attach it as `Source.CatalogKeys`, so oid equality filters visit only
  matching rows. A statement whose filter yields a `pgcatalog.Keep`
  (`pg_proc.oid`) uses a hit as is; on a miss it builds only the kept rows and
  stores nothing.
- `KeepQuery.Keep` returns the producer row test for a leaf of the SELECT
  being captured (system relations with `pgcatalog.KeyColumns` only); nested
  view and policy captures number their occurrences separately.
  `KeepQuery.ScalarStatsReadSuppressed` uses the same captured-leaf ownership guard
  before the literal-only `exec.CatalogSourceFalse` proof. A proven direct false
  input can pass through ordinary aggregates with no ordered-set direct arguments:
  synthesized aggregate output does not demand input-source values. Identity reads,
  subqueries and unsupported boundaries reject the proof. Bgwriter/checkpointer
  use it only to suppress cumulative evaluation, never current getter ACLs.
  The xact function view also uses it to eliminate the uninitialized volatile
  subtree's backing getter demand, while retaining own-view SELECT admission.
  Both early metadata admission and capture receive the same occurrence-owned
  preflighted root; query-source admission still precedes policy/source capture.
- `KeepQuery.RejectsInput` reuses the executor's literal/imported-parameter WHERE
  proof only for this SELECT's exact single Source under Project→Filter. FALSE
  and Boolean NULL can elide eager catalog production after common SELECT
  admission. TRUE demands capture. Current parameters are checked on each
  execution; no rejected-demand decision is retained. Nested/CTE/rule frames,
  samples, row marks, locks and other topology conservatively keep capture.
  Arbitrary expressions are never evaluated as proof. Cancellation and invalid
  parameter frames remain errors, not successful empty results. Bgwriter and
  checkpointer scalar getter ACLs remain admitted before this proof; their broader
  aggregate read-only suppression remains independent of direct-input source elision.
  Xact-function metadata admission additionally consumes this direct-input proof
  before admitting retained volatile getters; TRUE still requires all three.
  Subscription-stats admission and capture share one additional shallow proof:
  exactly one physical AND pairs direct source-owned `subname` column1 equality
  with a literal/current imported BOOL. The equality must be builtin immutable
  NAME equality to a same-usage literal/imported NAME, with exact origin,
  attribute, frame and type generation. No wrappers, custom routines, recursive
  logic or arbitrary counterpart work qualify. FALSE/NULL elides getter6231 and
  Reader configuration/capture only after SELECT; TRUE rechecks current EXECUTE.
  These current-parameter witnesses use custom planning, not an automatic held
  generic/custom choice. The generic and literal-only scalar proofs stay unchanged.
- `InitializeSources` registers descriptors from `pgcatalog.Specs`, preserving
  registration order, OIDs, columns and row/array links. It copies relation
  templates built once per process and sets this engine's owner, ACL and
  column types; engines share the templates' index maps and attribute slices.
- `ClassifySharedDependencies` lists the `pg_shdepend` rows of the retained
  per-database roots (`SharedDependencyDatabase`); exactly one is current.
- `PublicationTableMetadata` and `VisitSequenceOverlay` are shared with core's
  publication inquiry and `pg_sequences` capture.

## Locks

- The package holds no lock. `…Locked` functions (`CaptureCache.CaptureLocked`)
  are entered with `Engine.Mu` held and return with it held. Settings rows
  capture one coherent physical-session snapshot after core unlocks
  `Engine.Mu`.

## Testing

- `go test ./internal/engine/core/catalogrt` runs the shared-dependency units
  (internal) and the engine tests that need the `export_test.go` accessor
  (`TestViewCapabilitiesForeignEvents`, `TestBuiltinViewRoutineIdentity`). Per
  T7, engine tests of this package live here, not in an `enginetests`
  subpackage.
- Shared-dependency immutable-root, local-OID collision and referenced-pin
  units use `RUN='^TestPGShdepend'`; public SQL acceptance stays in
  `reference/catalog_privileges`.
- Bound catalog-input proof and topology guards use
  `make check RUN='^TestCatalogSourceRejectsInputProof$'`; actual current ACL and
  provider/Reader ownership remain composed core and public reference witnesses.
- Producer tests build runtimes with `core.NewCatalogRuntime` and read their
  fields back with `core.CatalogRuntimeFieldsOf`; they live with the
  producers (`core/enginetests/{pgcatalog,infoschema}`,
  `pgcatalog/enginetests`).
- Reference selector: `make check REFERENCE=1 RUN='^TestReference(Pg|PG|Info|Catalog)'`.
