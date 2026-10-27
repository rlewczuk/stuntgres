# internal/engine/control

The native Dump and Restore drivers and the catalog-replacement phases they
share with the engine. Dump turns a captured catalog into an ordered SQL script;
Restore replays such a script through an ordered hook table; features add
table entries, not switch cases. The event graph and order are in
[`dump`](../dump/README.md), per-object serializers in their feature packages,
the public contract in [docs/FIXTURES.md](../../../docs/FIXTURES.md).

## Imports and state

It imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `exec`,
`expr`, `extension`, `fdw`, `ids`, `index`, `largeobject`, `partition`,
`pgcatalog`, `plan`, `regobj`, `routine`, `sequence`, `session`, `storage`,
`stored`, `table`, `typedef`, `view`, `visibility`, `parser`, `sqltype` and
`sqlstate`. Only the root engine imports it. It holds no mutex, no engine
pointer and no state between calls.

## Dump

The root's capture window (`captureDumpLocked`, under `Engine.Mu`) calls
`CheckPortable` and `PreflightACLItems`, fills `Source` and calls
`Collect(ctx, source)`; nothing in `Source` is read after the unlock.
`Dump(ctx, capture, dst)` (`dump.go`) then sorts and completes the `Capture` in
place, builds the script and writes it; the root never reads the capture again.

Extension-owned composite types are reconstructed by their installation.
Their standalone backing relations are therefore omitted from CREATE TYPE
output, and backing-relation dependency edges resolve to the same extension
event without inventing direct relation membership.

Parameter ACLs use the shared typed ACLITEM[] preflight and deterministic
object-ACL section. Isolated Restore accepts only the generated single-assignment
`pg_parameter_acl.paracl` repair selected by literal TEXT `parname`, allocating
destination identities without publishing temporary grants. Sparse baseline
normalization, explicit-empty arrays, grantor order and role mapping are preserved;
setting values and source parameter OIDs are not serialized.

PL bootstrap languages/handlers are validated and not recreated. Accepted
handler parallel/cost metadata uses the existing provider ALTER replay.
Changed retained language owners and raw ACLs replay through bounded catalog
repairs. Restore compiles PL declarations on CREATE, preserves lazy expression
binding, and remaps declaration source addresses once without converting replay
prerequisites into hard edges.
Row-trigger definitions are post-data SQL groups, after their functions/tables
and saved rows, so Restore never transforms stored data twice. Partition clones
are derived through catalog publication rather than serialized independently;
definition OIDs, parent provenance and WHEN captures remap with the destination.

Event-trigger collection retains validated immutable descriptors and real owner/
function authority. `prepareEventTriggers` renders the complete final section
before any writer I/O; an existing owner demoted from superuser is explicitly
unrepresentable portable state and rejects Dump without an output prefix.
Representable owners, qualified functions, ordered filters and O/D state replay
unchanged. All ordinary DDL, data, defaults, attribute metadata and temporary
raw-codec CAST cleanup precede this section. Each definition is created and
disabled, then the saved enabled states are installed after every declaration.
No eligible replay DDL follows activation. Source callback frames and private
transaction definitions are not captured; replacement remaps global object and
function references once into the destination authority.

- **Collect tables** (`collect*.go`): `dumpCollectors` is an ordered list of
  `{name, matches, collect}`. Temporary-namespace objects are skipped, the first
  matching entry collects a descriptor and an unmatched one fails with
  `unsupported catalog descriptor kind`; the relation entry dispatches through
  `dumpRelationCollectors` the same way. **Entry order is error precedence.**
  Feature packages own each entry's decision (`routine.OperatorDumped`,
  `stored.ProcedureProvider`, `stored.TypeProvider`).
- **Dictionaries** (`dictionary_replay.go`): builtin simple and installed members
  never get duplicate CREATE statements. Their initialized identities use the
  captured names of their installation namespaces, after namespace replay, and
  share one dictionary collision plan, separate from routine keys; relabels
  precede option refreshes and owner transfers. Admitted option removals replay explicitly,
  preserving SQL NULL rather than equivalent default resource behavior; genuine
  unaccent dictionaries require explicit Rules and cannot replay its removal.
  Final slot metadata and stored references/defaults/views follow those changes.
  Standalone dictionaries own declaration events after template installation.
  Non-bootstrap owners use the shared `dump.Statement.PrivilegedCreation` protocol:
  CREATE runs under authenticated `DEFAULT`, and `FollowingSQL` transfers to the
  actual owner without assuming schema CREATE grants or a named administrator.
  Restore executes the emitted SQL, recompiles real resources and remaps
  dictionary/template links and stored regdictionary references.
- **Statistics** (`statistics.go`) serialize canonical named declarations after
  all ordinary graph phases, in qualified-name order. This includes completed
  input attribute history and unrelated row events, so reminted OIDs cannot move
  definitions across data on redump. Stable keys resolve current column spellings;
  generated original names replay explicitly. Named owner
  directives pair privileged creation with one immediate owner transfer in the
  isolated Restore, without enabling ordinary statistics owner alteration.
  Bounded target alterations replay after that immediate owner pair, under the
  actual owner. Replacement remaps object addresses, principals and relation refs.
  The pair record belongs to `ddl`; `schemacmds` performs trusted capture and
  owner-only COW, while the root driver alone carries and expires that pair.
  Bootstrap-principal definitions use their named owner directive without a
  redundant transfer; DEFAULT may name a different authenticated role on replay.
  Deterministic strict `stuntgres-ndistinct-v1` comments transport only checked
  collected ndistinct facts, including stale values and separate inherited
  flavors. Restore resolves names/keys/usages after definition finalization and
  reconstructs immutable typed values before atomic destination publication.
  No ANALYZE replay or single-column distribution import occurs. Genuine
  destination TEMP facts retain their separate existing retention authority.
  PostgreSQL18 script replay ignores these comments and leaves data absent.
- **Configurations** (`ts_configuration.go`) serialize actual PARSER and ordered
  ADD MAPPING statements, never COPY provenance. Namespace, mapped dictionary
  and stored alias/default/view dependencies use the ordinary event graph.
  Owner transfers use privileged creation; seed edits replay current mappings
  after canonical seed initialization. Temporary retention and replacement
  remap configuration descriptors and REGCONFIG datums through existing owners.
- **Provider differences** (`provider_replay.go`): immutable released builtin
  identity and checked versioned extension slots, never mutable labels, select
  real ALTER FUNCTION or ALTER TEXT SEARCH DICTIONARY replay. Original SQL
  declaration names are distinct from executable C symbols. Required
  schemas/extensions and every installed scalar identity relocation precede
  differences; dependent definitions follow.
  Free destinations move first; only occupied cycles or blocked intermediate keys
  need collision-checked temporary names.
  Canonical parallel/cost/rows patches follow final identities and precede
  owner transfers; builtin costs compare against immutable released defaults.
  Generated range/multirange constructor patches use the same replay planner,
  but emit immediately after their actual parent type declaration, before type
  ACL repairs and dependent events. Their identities are validated type-owned
  capabilities, not builtin OIDs; seeded PL handlers use their real declarations.
  Existing generated constructor/handler authority portability gates remain.
  Owner changes follow relabeling, then stable-slot owner metadata. Authentic
  installed routine ACLs use that metadata's named grantor/grantee codec during
  native Restore, preserving absent/explicit-empty ACLs and grant options.
  Other supported routine, language, wrapper, server, namespace, type,
  tablespace, relation and column raw ACLs use destination-resolved administrative
  catalog repairs
  (`object_acl.go`). Namespace/tablespace owner and ACL repairs run after schema
  creation, before dependent definitions. Type ACLs run at their definition or
  owning-family identity-ready event, before consumers requiring USAGE. Final
  repairs reconcile exact provenance after later identity and owner operations.
  Named regrole repairs preserve retained namespace/tablespace owners. Type
  locators use exact regtype identities, including generated companions. The
  role-aware ACLITEM codec preserves grantors/options and NULL versus
  explicit-empty/default-equivalent ACLs without collapsing raw provenance.
  Shared-memory view and backing-routine ACLs omit only the exact destination
  seed baseline: bootstrap owner, namespace/identity and ordered explicit
  bootstrap/pg_read_all_stats entries, without grant options or column ACLs.
  Changed raw authority uses the existing repairs; reordering, duplicates and
  explicit absence/emptiness are not effective-rights normalization. Defaults
  therefore add no dump noise, while custom SELECT/EXECUTE grants survive replay.
  Explicit-empty repairs use the existing lossless
  `pg_catalog.acldefault('c',0)` constructor, with qualified argument types, to
  preserve PostgreSQL's one-dimensional empty ACL representation. Isolated
  admission recognizes only that fixed constant shape, never evaluates arbitrary
  functions, and does not accept zero-dimensional `'{}'` repairs.
  ACL capture shares declaration collection's temporary-namespace exclusion:
  omitted temporary objects never leave dangling raw-ACL repairs in the stream.
  Installed extension slots retain their existing metadata path, without
  duplicate raw replay. `object_acl_repair.go` admits only fixed generated shapes;
  ordinary public UPDATE gains no catalog write capability.
  Restore/Reset starts from canonical destination providers and destination
  foreign registrations, not source callbacks. Missing/incompatible capabilities
  fail before publication. Replacement preserves provider-name tombstones so
  registry fallback cannot resurrect vacated names.
  Immutable destination registration authority survives SQL DROP: Reset can
  reseed it without application callbacks or adopting a source token. Registry
  lookup is replacement-only, never runtime public-name fallback.
  Body-edge address and principal remapping preserve original declaration
  revisions and shared/cyclic associations, not newer current descriptors at
  historical addresses; owners/ACLs are remapped before recipe reconstruction.
  ALTER argument rendering uses routine identity type spellings, including
  released pseudotypes such as `internal`, not storage-column declaration
  usage. Each argument OID must resolve to a real catalog type; non-keyword
  names stay qualified, with no numeric missing-type fallback. Naming a
  pseudotype enables neither SQL storage nor native ABI execution.
- **Script** (`script.go`): `buildDumpScript` renders policies, rules,
  row-security, types and event-trigger declarations, completes the event graph,
  decides default deferral and orders events with `dump.Order`.
- **Sections and emit table** (`sections.go`, `emit*.go`): `dumpSections` is
  the script in order; its `events` section writes each event through
  `dumpPhaseEmitters`, indexed by `dump.Phase`. The loop sets an entry's `owner`
  before `emit` runs (`nil`: the emitter sets it); an emitter that can fail
  renders before switching owner so an error writes nothing. `dumpWriter`
  tracks the `SET SESSION AUTHORIZATION` owner. Privileged declaration creation
  uses `dump.Statement.PrivilegedCreation` and `DEFAULT` (the authenticated
  administrator), emits `FollowingSQL` with the actual owner transfer, then
  restores tracked owner authority before any dependent event.
  Ordinary non-bootstrap-owned tables likewise capture the named owner, create
  under authenticated `DEFAULT`, transfer that exact fresh table immediately,
  and restore owner authority before declaration completion or data. The private
  importer accepts only that captured creator/target/owner pair, including
  inheritance members; it grants no schema privilege or ordinary DDL exemption.
  Partitioned roots and partition members instead keep their named-owner
  constructor replay; ordinary-table pairing does not extend to either.
  **Adding a dumped object:** export the feature's dump function and name it at
  the matching table position.
- **Adapters** (`resolver.go`, `presenter.go`): `Resolver` (`dump.Resolver`)
  and `Presenter` (`dump.Presenter`) read only their arguments.
  Unmodified builtin scalar declarations use the existing machine-identity
  spelling: SQL keyword aliases remain keywords and other names are qualified.
  Routine overloads and table columns therefore retain builtin identities when
  replay runs with shadowing domains before `pg_catalog` in `search_path`.
  Extension-member predecessors collapse to their actual owning extension;
  internally owned family rows first collapse to their actual class or family
  owner. Lifetime comes from the extension's target namespace: a missing namespace is invalid, while
  a valid TEMP installation gets the same atomic nonportable rejection as
  extension collection, independent of descriptor traversal order.
- **Large objects** (`largeobject.go`): a fixed-OID-sorted list outside the
  relation graph; owner and ACL formatting is preflighted before writer I/O.

### Ordering and replay rules

- Anything Dump cannot serialize exactly (default privileges, invalid indexes
  or unsupported loose providers) is rejected before any output, never silently
  reset. Supported object ACL rendering and provider validation are fully
  preflighted before writer I/O; column repairs locate live columns by exact name.
  Database registration and destination attachment authority are excluded, not
  replaced by ACL repairs.
  Attribute ACL updates are classified separately from array-rank repairs;
  malformed ACL syntax remains a rejected ACL repair, never a rank repair.
- **Attribute history** (`attribute_history.go`, `attribute_simulation.go`):
  deterministic inert placeholders preserve retired slots through ordinary DDL.
  Compatible declarations retain the existing CREATE/attach serializers; mixed
  inherited layouts use a simulated CREATE/ADD/DROP action schedule constrained
  by exact slots, locality and current origins. Per-action graph identities
  retain real provider and consumer edges. Erased origins use ordinary helper
  tables, with closed detach/empty teardown pairs and no final helper metadata.
  Typed and partition attachment follows the completed ordinary pre-attachment
  layout, including local dropped slots. Existing attachment, row, constraint and
  provider dependencies remain authoritative; partition membership is not an
  ordinary history edge or a transient helper.
  Restore pre-pairs literal constructors, checks their destination-owned fresh
  references and exact live closure, and delegates effects to ordinary DDL.
  Closed staged CHECK declarations use that constructor authority, including
  ordinary inherited/local definition merges, rather than unrelated generated
  expression repair authority. Standalone fresh CHECK replay still requires an
  absent name throughout its actual propagation closure; unpaired merges do not
  gain admission. Paired inert retirements enable only the existing destination
  namespace lookup seam, retaining the named owner's ordinary ALTER authority
  even when the schema owner differs. Namespace ACL serialization is unchanged.
  Data/final ACL consumption closes column-shape admission. Rendering finishes
  before writer I/O; no captured descriptor is injected or renumbered.
- **Default deferral** breaks table → routine → table cycles as CREATE TABLE,
  CREATE FUNCTION, then ALTER TABLE SET DEFAULT. Replaying a default definition
  does not evaluate it; staged DML uses ordinary default evaluation.
- **Cast replay windows** (`cast.go`, `cast_replay.go`, `cast_consumer.go`):
  ordinary-SQL cast windows are derived only after default deferral and CHECK
  phases are settled, one per emitted consumer statement; the final conflicting
  cast row is installed once after all windows for its pair. Aggregate binary
  recipes, foreign defaults and generated-column placeholders
  (`table.PrepareGenerationPlaceholders`) use the same windows.
  Genuine extension-owned casts have no standalone declaration or window:
  matching frozen semantic recipes depend on their validated extension
  predecessor. Replacement remaps reciprocal membership as well as endpoints.
- **Raw FLOAT4 words** (noncanonical NaN only, `regobj.NeedsFloat4RawReplay`)
  replay through a binary `xid → real` cast; Restore has no raw-datum decoder.
- **Exact Seg cells** share the bounded private row-replay record. A tagged
  six-field payload preserves endpoint words, precision and markers, paired
  immediately with a one-row INSERT containing the canonical quoted literal.
  Genuine destination-provider validation and exact canonical text precede
  substitution; reinputtable finite/open values additionally bind independently.
  Only derived infinity/inverted payloads bypass stock text input, which rejects
  their own canonical output. Malformed metadata never reaches replacement
  publication. PostgreSQL ignores the comment; the ordinary SQL replay corpus
  therefore uses reinputtable values, while native controls retain both classes.
- **Installed type identity replay** (`type_identity_replay.go`): canonical
  extension installations follow prerequisite order before ordinary consumer
  declarations. Every installed scalar role's renamed/moved identity is reconstructed with
  ordinary ALTERs under bootstrap authority. Ltree scalar roles include lquery,
  ltree and ltxtquery; codec ownership uses canonical roles even when ltxtq_out
  refers to a renamed or moved scalar. Deterministic free temporary names break
  cross-member cycles; moves preserve companion spelling and renames use
  the shared generated-array predictor. This predictor seeds mixed domain/composite
  rendering, including domains over installed arrays. All four ISN scalars' captured
  owners are then restored with ordinary TYPE OWNER SQL, which transfers each
  companion without changing independent extension or I/O owners. Separate ordinary
  FUNCTION OWNER replay preserves independent codec owners in native Restore and
  PostgreSQL replay. When provider
  alterations take ownership of early extension declarations, they also emit this
  shared scalar schedule before routine ALTERs and owner metadata; otherwise the
  ordinary last-extension declaration emits it. The schedule is transient captured
  rendering state, not a persisted identity registry.

## Restore

- **Driver** (`restore.go`): `Restore[R](ctx, host, hooks, run, src)` reads `src`
  through `contextReader`, parses it with the `Decode` entries, then calls
  `Host.Begin` and `Host.Start` (its `end` is deferred). Per statement:
  `Host.Statement`, the `Before`, `Admit` and `Prepare` entries,
  `Host.Execute`, the `After` entries. Then `Finish` and `Host.Publish`. The
  first error stops replay and skips `Publish`; `end` still runs.
- **Hook table** (`hooks.go`): `Hook[R]` has a `Name` and optional phase
  functions over the caller's run state `R`. The first `Admit` answer other
  than `NotHandled` (`Admitted`, or `Consumed`: replayed, nothing executes)
  wins; an unhandled statement fails with `0A000` and the allow-list text.
- **Admission checks** (`admit*.go`): run-independent allow-list entries
  (`AdmitKind`, `AdmitSchemaObject`, `AdmitOperator`, `AdmitStatement(check)`
  over `Check*` functions). The root's table names them in its own order, so
  rejection order is the table's.
  TYPE admission covers the implemented RENAME/SET SCHEMA identity AST and
  qualified named-role OWNER for each genuine installed ISN scalar paired to its
  canonical captured owner record, not arbitrary TYPE evolution or DROP TYPE.
  Ownership uses ordinary authority checks and staged base/companion transfer.
  Canonical extension member-role owner metadata resolves through genuine
  destination manifests, not final names, and validates independent member owners.
- **Comparison context** (`comparison_restore.go`, `comparison_binding.go`):
  `ComparisonRestore` is the run's slot for installed comparison windows that
  Dump writes (`emitDefinition`). `Decode` collects the marker comments,
  `Pair` ties each to the `SET`/`RESET search_path` right after it (only blank
  text between). `Before(index, stmt, current)` walks begin → bind → one DDL
  → close, repeated, then finish: bind needs stock cube or hstore installed in the
  named persistent namespace; any unmarked `search_path` change, a non-DDL or
  second DDL in a window, or a statement after finish is invalid. A marked
  statement returns `execute`, and the root runs it with no other entry.
  Dump derives the namespace from authenticated typed captures and orders each
  bound definition after the namespace's installed comparison providers, including
  both providers when cube and hstore share that namespace.
  `After` hands the bound window's check to the root, which captures its
  authorization under `Engine.Mu`, checks USAGE on the namespace and calls
  `ValidateComparisonBinding`; `Bound` then closes the window. Only
  definitions this statement published, with expressions at or after its
  position, count as a receipt. `Finish` rejects unconsumed markers and an
  unfinished context. The state machine holds nothing; its hook entries keep
  their table positions, so error precedence is unchanged.

### Host contract

`Host` is the engine's side of one Restore (root `restoreHost`):

- `Begin(source, statements)` records the script on the run. `Start(ctx)`
  starts the private stage's session as the bootstrap superuser and acquires
  its operation; `end` clears replay flags, releases the operation and ends the
  session, in that order. A failed `Start` cleans up and returns no `end`.
  The root holds its non-retractable-effect policy for that entire operation,
  including nested routine/default/CHECK/index evaluation, not just one admitted
  statement. Actual foreign acquisition, modification and schema import enforce
  it before provider invocation. Advisory ownership and setting changes stay on
  the private stage's manager/session, disposed at session end, never published.
  Definition-only FDW validation still invokes authentic trusted destination
  validators; this is not a sandbox for arbitrary registered Go callbacks.
- `Statement(index)` resets per-statement state; `Execute(op, stmt)` runs one
  admitted statement on the stage, clears per-statement replay flags, closes
  its rows and detaches its notices.
- `Publish(ctx, op)` installs the stage catalog into the target engine.

Entries share state only through the run, so their order is part of the
contract; every entry runs on the stage, never on the target engine.

## Catalog replacement

The root's `preserveTemporaryLocked` holds `Engine.Mu` for identity planning and
publication. Provider repair uses the existing isolated candidate/session with
the target mutex released and publication fences rechecked. On the private
candidate, these owning operations retain the engine's error contracts:

1. `PersistentReplacementCatalog`, `TemporarySet`,
   `RetainTemporaryBuiltinTypes`, `CheckTemporaryDependencies`,
   `TemporaryCollisionMapping`, `RemapReplacementIdentities`
   (`persistent_replacement.go`, `temporary_*.go`, `replacement_remap*.go`).
   A retained catalog-only NULL composite closes over its actual member types
   and companion array. Otherwise type reownership rebuilds the fixed description
   against the candidate's current member generations, preserving old captures.
   Collision remapping stays selective outside actual affected alias endpoint
   components. All captured range/member order edges are structural facts;
   unrelated collisions, unused families and separate singleton values do not
   activate them. A finite retain-first/backtracking assignment includes all
   ordinary collision demand in one owned pool from the central allocator's
   operation-local replacement cursor. Normal issuance uses the same helper.
   Complete search precedes every further acquisition and stops immediately on
   success, leaving unnecessary later intervals and future capacity untouched.
   Only saturated/impossible intervals permit sparse floor jumps through the
   existing allocator. Fixed numeric endpoints remain fixed; TEMP identities
   are excluded destinations, not source anchors. Exhausted legal complete
   choices alone justify no-fit. Provider certification remains unlocked.
   The candidate cursor spans all preservation OID consumers, including view
   rules, and publishes only with the final catalog installation. A no-fit,
   later validation error or cancellation discards private reservations, not
   live issuance. Ordinary allocations during unlocked repair invalidate the
   candidate through both repair and publication frontier fences.
   Generated constructors with independently changed routine owners or explicit
   ACLs reject Dump before output; four-type owner replay does not serialize or
   reset independently altered routine authority.
2. `RemapViewRuleOIDs(view, allocate)` and `RemapDefinitionOIDs`
   (`view_rule_oids.go`, `definition_oids.go`): give colliding imported rules,
   checks, policies, constraints, defaults and generations fresh OIDs from the
   engine allocator, keeping identities claimed by retained TEMP objects, and
   repoint everything that referenced them.
3. `Check*SourcesLocked` and `RetainTemporaryViewProvidersLocked`
   (`temporary_sources.go`) validate retained TEMP objects against the
   published catalog.

## What stays in the engine

`Engine.Mu`, the capture window (`dump_capture.go`), the OID allocator and
`preserveTemporaryLocked`; the restore stage (`restoreStage`) and atomic
publication (`replaceCatalog`) with their lock order and fences; `restoreRun`,
`restoreHost`, `restoreHooks` and every hook that reads the run (the
comparison hooks only forward, run marked statements and capture
authorization); the composition functions `Reset`, `Dump` and `Restore`
(`control.go`).

## Testing

Package units drive the Restore driver and hooks with a fake `Host`; engine
units cover the collect, emit, remap and TEMP preservation callers. Public
Dump/Restore behavior is in `reference/` ([docs/TESTING.md](../../../docs/TESTING.md)).

Provider replay proof covers key cycles, move-before-rename, owner-last ordering,
invalid identities, internal pseudotype signatures, missing argument types and
the explicit-ACL boundary:
`make check RUN='^TestProviderReplay'` and
`make check RUN='^TestRestoreForeignProvider'`.
Run `make check RUN='^TestReferenceRoutineAlter(ProviderPersistence|Persistence|ExplicitACLPersistenceBoundary)$'`
in native and `REFERENCE=1` modes: actual emitted SQL is replayed on PostgreSQL;
native proof also covers repeated Restore and fresh-target identity remapping.
Dictionary member/seed collision and provenance proof:
`make check RUN='^TestDictionary(ProviderReplayCycle|ExtensionProviderValidation)$'`.
Run `make check RUN='^TestReferenceDictionaryExtension(Identity|Controls)$'`
in native and `REFERENCE=1` modes for member changes, actual emitted SQL,
native replacement/remapping and deterministic redump.
Body/ownership remap proof:
`make check RUN='^Test(RoutineBodyPrincipalRemap|ReplacementRoutineBodyRemap|JSONBRecordRoutineOwnershipCOW)$'`.

Dump output must stay byte-identical unless a change intends otherwise. For
collect, emit, section or script changes, write a throwaway program under
`.cswd/tmp/<task>/` that runs a fixed corpus covering every object kind through
the root driver, calls `stuntgres.Dump`, restores into a fresh database and
dumps again; run it on a detached `master` worktree and on the branch and
compare byte for byte. Never commit the program.

- Driver and hooks: `RUN='^TestRestore(Driver|Hooks|AdmitsInsertTargetIndirection)'`
- Comparison context: `RUN='^Test(Comparison(Restore|Context)|ValidateComparisonBinding)'`,
  and `RUN='^TestReferenceCubeComparison(BindingPath|InheritedBindings|WrittenDefaults|Controls)$'`
  in native and `REFERENCE=1` modes
- FDW dump units: `RUN='^TestFDW(Dump|ReplacementRemap)'`
- Engine remap and TEMP preservation: `RUN='^Test(RuleNamedConstraintRemap|ViewRuleOIDUniquenessAfterRestore|PartitionConstraintsDefinitionRemapping|.*RetainedTemporary.*|.*TemporaryCollision)$'`
- Round trips: `RUN='^TestReference(Control|DumpRoundTrip)'`
- Cast replay windows: `RUN='^TestReferenceCastDefinition(ReplayPhases|ReplayConflicts|RawFloat4Persistence|ForeignDefaults)$'`
- Ownerless cast membership, replay semantics and replacement remapping:
  `RUN='^TestCastExtension'` (also with `RACE=1`).
