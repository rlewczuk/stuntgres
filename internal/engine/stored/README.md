# internal/engine/stored

Stored SQL definitions and their presentation: the frozen payloads that catalog
descriptors carry (views, rules, policies, routine recipes), the renderers that
turn them back into SQL, and the mappers that retag and remap them. View, rule
and policy DDL decisions and rule rewriting live in [`view`](../view/README.md).

SQL/PL routines share callable captures. Language/body kind, declaration replay
sources and typed local Usages participate in validation, equivalence and remap;
no activation values or evaluated PL expressions enter stored payloads.

`Trigger`/`TriggerList` provide a checked relation-owned `TriggersPayload`, sorted
by table-local name. Definitions retain routine/attribute identities, immutable
WHEN recipes and partition-clone provenance, never activation values. Rename
retains every identity and recipe while reordering the table-local chain.
Automatic extension addresses are constructor-owned, sorted unique metadata,
separate from normal function/WHEN references and extension membership.
Mapping preserves source snapshots and resolves extension addresses in the
destination; SQL serialization omits derived clone CREATEs, renders current
names, and emits clone-local dependency ALTERs after all parent definitions.
Partition synchronization may replace a conflicting local child definition only
along the exact CREATE OR REPLACE root definition's stage-local clone lineage.
Ordinary CREATE, ATTACH and unrelated catalog synchronization retain conflicts;
replaced local definitions and their dependency edges retire atomically.
Clone matching follows exact Parent identity before checking destination-name
collisions. Existing clone OIDs and local extension edges survive parent
resynchronization; a new clone does not inherit the parent's extension edges.
WHEN literal validation closes OLD/NEW occurrences over the owning relation,
not the synthetic Boolean projection's output frame. Ordinary view validation
retains its query-frame rules. Pseudo-column remapping resolves current physical
slots from mapped stable attribute identities.
Installed WHEN validation checks current captured routine/language authority
and completeness of semantic DROP edges, not only the supplied reference list.
The operation's `catalog.ProcedureRemapper` is shared by the executable WHEN,
its binding and its frozen presentation, including retained TEMP payloads.
Partition cloning supplies one checked current-declaration mapper for the whole
stage; projecting parent rows onto a child does not rewrite routine signatures.

## Imports

Layer L11.5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture),
above `analyze` and `regobj`. It imports `analyze`, `authorization`, `catalog`,
`dump`, `exec`, `expr`, `extension`, `functions`, `ids`, `lock` (row-lock
modes), `plan`, `proc`, `regobj`, `routine`, `sequence`, `storage`, `typedef`,
`parser`, `sqlstate`, `sqltype` and the standard library, and never the root
engine, `session` or `visibility`. The engine and the upper feature packages
(`control`, `dml`, `fdw`, `index`, `partition`, `role`, `table`, `view`) import
it; `analyze`, `exec`, `session` and `pgcatalog` reach it only through engine
host methods.

## What it owns

- **Frozen payloads.** `View` and `ViewLeaf` (`view.go`, `view_payload.go`),
  the `catalog.ViewPayload` of a relation, with `CheckMode`; `Policy`
  (`policy.go`), the `catalog.PolicyPayload`; `Rule`, `RuleAction` and
  `RuleList` (`rule.go`, `rule_payload.go`), the `catalog.RulesPayload`.
  `ViewOf`/`ViewPayload`, `PolicyOf` and `RulesOf`/`RulesPayload` recover and
  store them; `FreezeQueryPresentation` (`view_freeze.go`) builds a `View`
  from a binding.
- **Payload API.** Payload fields are private. `NewView(ViewParts)` builds a
  view; `Parts` returns a copy of the fields and `With(func(*ViewParts))` an
  edited copy, never changing the receiver. `NewPolicy`, `RuleParts`/`NewRule`
  and `RuleActionParts`/`NewRuleAction` (`rule_parts.go`) follow the same
  pattern; a rule's `OID` and `Name` stay exported fields. Read accessors share
  maps and slices instead of copying them: callers never write through an
  accessor.
- **Routine recipes.** `RoutineProgram` owns checked SQL-standard actions.
  `RoutineDefaults` owns an immutable input-indexed declaration-default recipe
  and its source-domain/epoch manifest, never evaluated values. Default
  dependencies are hard even for quoted bodies.
  Routine public name/namespace/owner are mutable presentation. Same-address
  declaration checks retain kind, exact signature/result, SQL/default/body and
  provider association. Installed captures validate their real manifest slot
  through `extension.ProcedureSymbol`; labels never select executable capability.
  Scalar serialization and deparse validate the original carrier, then format
  checked current labels without refreshing its pointer or dependency revision.
  Citext comparison and pattern captures retain the original procedure declaration, revision,
  operation and input collation independently of their selected-operator wrapper.
  Import validates the mapped original against the destination's exact installed
  member, remaps registered collations, and rebuilds the scalar predicate.
  Replay renders checked current labels without refreshing captured authority.
  Pattern replay casts both original operands explicitly, retaining the selected
  citext/text signature even when ESCAPE normalization produced a text pattern.
  Installed citext `CastRoutine` calls use this same presentation seam after
  validating the original carrier and current provider manifest/signature.
  Schema rename therefore updates deparse and default replay labels without
  changing the captured declaration, coercion plan or original snapshot.
  Selected citext string scalars preserve their shared `String` capture through
  reference and original-declaration remapping. Validation checks the exact
  installed member, source/destination scalar generations and builtin third
  formals; rendering uses checked current labels with explicit formal casts.
  Capture names, matching OIDs and current same-name declarations never replace
  original authority.
  Mutation-rule VALUES expressions use the same checked current-label projection
  while preserving their original OLD/NEW bindings and callable input assembly.
  `RetagTypes` is a structural generation rewrite, not a presentation refresh.
  Earth coordinate captures remap the actual domain, cube base, procedure,
  formals and result generation together. Stored view/default calls keep their
  unevaluated kernels; provider validation rejects stale or same-OID foreign
  descriptors before rendering current qualified labels.
  Replacement and type-only refresh bootstrap closed installed-domain CHECK
  recipes before SQL declaration/body/default preparation or other consumers.
  `ReownInstalledDomainChecks` reuses the operation's type/collation authority;
  reference remapping occurs once, including callers that prebind routines.
  This ordering does not relax provider validation or bootstrap user defaults.
  FLOAT8-only coordinate calls still track their hidden earth generation in
  the existing descriptor-change scans.
  Earth-point captures instead depend only on builtin POINT/FLOAT8 generations
  and their installed procedure; the selected operator retains a separate address.
  Remap/retag rebuilds that kernel against destination descriptors, and rendering
  validates current manifest links before using qualified presentation names.
  `StoredProviderRoutine` is distinct from SQL-body/owned-aggregate markers and
  reuses the existing `routineRefs` occurrence map and dependency manifest.
  `StoredProviderSymbol` records immutable symbols to reject substituted captures;
  written names, syntax flags, named/default argument shape and SQL/JSON options
  stay intact. Ordinary relabelled calls use checked current names. SQL/JSON
  ARRAYAGG/OBJECTAGG retain constructor grammar after implementation rename/move,
  matching PostgreSQL18.6. Display omits canonical defaults and uses OBJECTAGG's
  colon separator; portable replay retains written FORMAT, null policy,
  uniqueness and RETURNING with captured types. Neither path drops the checked
  implementation ref or converts constructor grammar to a mutable routine call.
  Family validation preserves admitted named slots/redundant VARIADIC. Missing,
  extra, foreign, stale-epoch/SubID and wrong-provider captures remain errors.
  Quoted source stays soft/original; no SQL-source rewrite or alias registry.
  `ValidateRoutineCallIdentities` validates frozen SQL calls without replacing
  original handles: a captured descriptor and its dependency revision form one
  lifetime. Admission checks the original manifest, current immutable declaration
  and current EXECUTE; only hard recipes follow mutable revisions. Original-source
  revision fences remain. `TestRoutineAlterFrozenManifestRetention` covers
  retained handles/manifests and rejection of changed result/signature/SQL payloads.
  Frozen action planning/runtime sources borrow that original manifest.
  Rule conditions/actions select their exact live rule slot and original
  presentation before NEW substitution. Grafted scalar, window and declaration-
  specific aggregate occurrences carry that recipe identity; fresh producer
  nodes keep their own direct manifest, even at the same routine address.
  Quoted/rebound source does not inherit hard-recipe policy. Aggregate admission
  uses its captured descriptor and exact original ref/revision; ref-only carriers
  retain their revision fence. Transition support retains recipe provenance while
  checking EXECUTE as the current aggregate owner, not the invoker.
  `TestRoutineAlterFrozenExecutionAuthority` checks hard/default/soft values,
  current callee revoke/regrant and dormant explicit-argument defaults.
  Hidden arguments select their declaration-default manifest through the shared
  `SelectRoutineDefault` boundary in preflight, conditional simplification,
  scalar admission and runtime. Written actuals keep the caller's manifest;
  an entire source call is never promoted to hard-recipe policy. Local planning
  contexts propagate prepared domain checks without leaking default origin or
  changing invoker authority.
  `TestRoutineAlterAggregateHardPhaseAuthority` checks renamed aggregate/support
  execution under a support-denied invoker and owner EXECUTE revoke/regrant.
- **Historical routine remapping.** Raw mapping preserves original declaration
  carriers and exact dependency revisions; an address change is not SQL reanalysis.
  Each builtin aggregate slot, window and scalar/body capture uses the scoped
  original-procedure callback, never current-descriptor substitution.
  Each selected declaration maps its own ref and descriptor independently.
  `NewRoutineRemap` shares original SQL-definition/body/default mappings with the
  existing two-phase rewrite. Keys are original immutable payload identities,
  not names, addresses or revisions: relabel-only COW versions share mapped
  payloads, distinct original bodies remain distinct. The existing graph memo
  preserves sharing/cycles and source immutability; pending payloads complete and
  validate before publication. Normal type evolution rebinds affected current
  payloads while leaving unaffected originals intact.
  Generated range constructors participate in the same declaration-stabilization
  phase: the operation memo owns their destination type/provider, exact signature
  and result. No separate post-principal COW pass creates unregistered target
  clones, and no current/newer declaration replaces a captured original.
  Selected cast/operator gateway negative parameter slots are transport only;
  the owning written expression supplies frozen children, including typed NULL.
  The same operation memo owns original `proc.PLProgram` identities and maps
  complete formal/result/non-RECORD local Usages, including typmod and collation
  capabilities. PL lexical names belong to immutable `Definition.Name`, not the
  descriptor's mutable public label; relabelling does not recompile the body.
  Imported containers use captured source-principal/name/incarnation policy only.
  Preflight-retained TEMP containers use a separate checked destination lane:
  genuine historical owners/grantees require nonzero principal-domain provenance,
  not continued role existence; foreign/mixed domains fail. TEMP presentations
  accompany rule/policy remapping without import address mapping.
  Full-reference mapping stays separate from datum/address mapping. Frozen
  view/default/CHECK manifests keep original routine stamps rather than recapturing
  current revisions. Structural copies mint no destination authority; admission
  still rejects foreign originals. Non-SQL mapped-original numeric signatures
  must match the destination immutable declaration; SQL uses typed symbolic
  comparison. Current EXECUTE remains independent.
  Standalone remap boundaries that accept a procedure-mapper callback take it
  explicitly; no zero-or-one variadic compatibility form remains. Where such
  a boundary permits `nil`, it selects only verified same-domain/structural
  policy, never guessed import principal authority. Body/default
  `catalog.RoutineProgram.Rebind` and `catalog.RoutineDefaults.Rebind` instead
  require the same complete nonnil `*catalog.RoutineRemap`, including its Type,
  Collation and Procedure mappings. Cross-domain callers supply the actual host
  source policy and shared payload context; graph preparation likewise takes
  one explicit callback.
  Installed intagg enumeration validates the complete mapped ref, original
  Procedure/Info pair and argument/result generations before resolving its
  current immutable association. The unchanged exact-current provider guard
  validates that current declaration; the stored carrier and hard stamp remain
  original. Deparsing uses the validated current name/namespace without replacing
  the original proof. `TestIntaggIdentityRemap` covers direct, frozen and retained
  TEMP remapping, rejects incoherent captures and preserves child type diagnostics.
- **Renderers.** `Deparser`/`DeparseParent` (`deparse_*.go`): expressions in
  the style of PostgreSQL's `ruleutils.c`, plus constraint SQL
  (`constraint_sql.go`) and `DeparseAttribute`. `ScalarRenderer`
  (`scalar_*.go`): stored scalar SQL for defaults, checks and dumps, recording
  the references it prints (`DefaultSQL`, `RenderColumnDefault`).
  `QueryRenderer` and `Presentation` (`view_deparse*.go`,
  `view_render_sources.go`, `sqljson_deparse.go`): whole queries,
  `View.SelectSQL`, `ViewDefinitionSQL` (`pg_get_viewdef`) and `PolicySQL`.
  Public display of a sole noncorrelated aliased table uses current relation/
  attribute names, unqualified columns and only nonredundant column aliases.
  Correlation-participating frames retain lexical qualification; local keys use
  the actual emitted alias, never a relation name hidden by it. Portable replay
  keeps its qualified positional hygiene bridge. Consequently the exact
  PostgreSQL canonical definition of an emitted replay can retain a table alias
  absent from the original definition; canonical display is not dump SQL.
- **Rule and policy dumps.** `RuleDumpGroups`/`RuleActionSQL`,
  `PolicyDumpGroups` and `RowSecurityDumpGroups`.
- **Rule display.** `RuleDefinitionSQL` reconstructs current event, attribute
  and action identities using request-local `Presentation` and the querying
  TEMP owner. It shares frozen action/expression traversal with `RuleActionSQL`,
  but prints catalog display indentation, default target-list wrapping,
  conditional quoting, lower-case OLD/NEW aliases and path-sensitive action
  names. Ordinary aggregate/window routine names use display identifier quoting,
  including `quote_all_identifiers`; SQL/JSON aggregate grammar remains syntax,
  and unqualified replay call spelling does not depend on display settings.
  SELECT, FROM and USING use shared frozen namespace/slot presentation,
  including current source names and retained aliases. Mutation declarations
  and target-column references share one request-local retained target alias;
  source slots, event OLD/NEW, action RETURNING sides and conflict pseudo rows
  retain their own namespaces. Inference expressions use unqualified target
  attributes and live path-sensitive collation/operator-class identities,
  suppressing the actual default class rather than a name-based approximation.
  RETURNING labels come from its analyzed output schema; retained typed
  occurrences preserve explicit side aliases when execution shares a default-
  side target slot. Sublink punctuation and quantifiers share one display policy
  across bound mutations, SELECT actions and INSERT SELECT sources. Every query
  boundary establishes output-name visibility: immediate sublinks hide fallback
  labels; derived SELECT and CTE bodies expose their names independently of the
  caller; set operands follow their own left/right visibility. Nested boundaries
  restore the complete caller presentation on success and error. CTE bodies and
  recursive anchors recover original labels through the shared
  `analyze.ExpressionName` authority and frozen expansion captures, independently
  of execution's declared CTE schema. Rule VALUES bodies keep their intrinsic
  column names rather than introducing replay wrappers for CTE declarations.
  `view_rule_names.go` finalizes each frame's range-table names before
  rendering descendants or CTE bodies: preload allocated ancestors, then process
  logical RTEs in source order, with joins after their inputs. Explicit aliases
  replace their occurrence's preferred name before allocation and participate
  in collision suffixing just like defaults; later explicit names have no
  priority over earlier sources. Current relation defaults use stable identities.
  One source-alias mechanism supplies declarations, slots and whole-row names;
  wrappers neither allocate extra RTEs nor reserve phantom defaults. Unrelated
  columns and sibling-query names are not reservations. Request-local indentation
  propagates through derived queries, sublinks, INSERT sources and CTE/set bodies
  without SQL-text patching or reduced-parenthesis pretty mode.
  Finalized parent name facts also cross modifying-CTE renderer boundaries,
  independently of each frozen view's frame numbering. Targets, event OLD/NEW
  and EXCLUDED use their allocated namespace facts without changing executable
  slots. INSERT SELECT distinguishes the INSERT's target/source-wrapper parent
  namespace from the source query containing its pushed-down OLD/NEW entries;
  CTE bodies inherit the appropriate structural parent, not incidental rendering
  state. SELECT and INSERT SELECT actions use the same configured renderer
  factory as bound mutations, including retained modifier-body dispatch.
  ONLY, CURRENT OF, DEFAULT VALUES and multirow VALUES retain their typed forms.
  Event names stay qualified; the querying session's
  TEMP event schema is `pg_temp`. Display never changes the payload or executes
  an action. Replay retains its explicit ALSO, positional source bridges,
  fully quoted identities, trailing newlines and separate TEMP export rules.
- **Retag and remap.** `MapExpression`, `RetagTypes`, `RetagTypesWithQuery`
  (`expression_retag.go`, the canonical mapper); `RemapExpression`,
  `RemapScalar`, `RemapLikeExpression`, `RemapViewQuery` (`expression_remap.go`);
  `RemapView` (`frozen_presentation_remap.go`).
- **The remap protocol.** `ReownExpressions` (`expression_reown.go`) remaps and
  retags every stored expression of a catalog state after a renumbering
  (Restore, extension owner import, DROP OWNED, TEMP preservation) or after type
  changes only. It walks domain checks and defaults, column defaults and
  generations (`RefreshGenerationChecks`), index keys and predicates, partition
  bounds (`RemapPartitionDescriptor`), rules (`RemapRules`, `RemapRuleBinding`,
  `RemapRuleConstraintIdentities`), policies (`RemapPolicies`) and views. The
  rule tree mapper (`NewRuleTreeMapper`, `MapRuleBindingRecipes`,
  `RefreshRuleBinding`) is shared with rule rewriting. `ReownScalarTypes`
  points relation column types and stored datums at current type generations;
  `ReownVersions` applies it after an enum or domain change.
  `NewRoutineRemap` initializes one scoped `analyze.RecordTypeMapper` before
  any current/historical payload, query or scalar transform. Its existing
  `catalog.RoutineRemap` carries the complete Type/Collation transformation,
  as well as the original procedure and SQL-definition/body/default memos.
  Bodies and defaults consume that same context, including binding, frozen and
  modifier presentations, PL locals and presentation collation inventories;
  no payload creates its own occurrence mapper or destination type lookup.
  `ReownExpressions` consumes a supplied prebound replacement context, or
  creates and binds one for a standalone full-remap entry. Type-only entries
  retain their selective affected-payload rewrite. Independent exported view,
  rule and policy remap entries initialize their own occurrence boundary, while
  the internal operation seams reuse the complete mapper. Retained TEMP rules
  keep their separate target-container policy; import mappings never enter it.
  `RangeIdentityConstraints` captures actual finite alias relationships from
  rows and known stored/default/generation recipes through range-bearing
  carriers. Captured source interval records retain flags, empty/NULL and
  independent unbounded sides. Each companion's numeric endpoint preorder
  preserves separation and hidden overlap comparisons, not written argument
  adjacency; expanded and VARIADIC forms share those semantics. Independent
  rows/carrier elements acquire no cross-value edges. Capture is cancellation-
  aware and provider-free under the catalog lock. Shared object addresses share
  assignments; dangling numbers stay fixed. The control planner activates only
  affected endpoint components and includes ordinary collision capacity in the
  same demand-driven plan. `ValidateRemappedRanges` still certifies the detached
  destination with real providers and operation context after unlocking, before
  publication.
- **Readers of stored definitions.** `pg_depend` references
  (`ExpressionDependRefs`, `QueryDependRefs`, `IndexDependRefs`,
  `PartitionDependRefs`, `DependPinned`); temporary provenance and reference
  epoch checks (`temporary_provenance.go`); `ProcedureProvider` and
  `TypeProvider` validators with the domain recipe validators and
  `ValidateTypeProviders`; `PruneQuoteCaptures`; installed-node validators
  (`ValidateInstalled*`); the view read graph and volatility
  (`ValidateViewReadGraph`, `ViewVolatile`, `CaptureRetainedInput`); row-mark
  readers (`view_row_marks.go`); `HasPgrowlocksDependency`; view validation and
  view-write shape; `ValidateRules`; and `DependencyObject` (object
  descriptions for DROP and role diagnostics).
  `ViewWriteShape` and `ViewUpdatabilityShape` share one automatic-admission
  implementation; only the write route collects execution predicates.
  Metadata shape inspection neither allocates that vector nor evaluates it.

- **Catalog relation pinning** (`catalog_pinning.go`): bootstrap catalog
  tables stay pinned, while catalog views such as `pg_views` record ordinary
  SELECT-rule dependency edges. Relation refs in the immutable source and
  dependency manifests, including attributes, are validated before the
  address-only dependency pass. Runtime view-reference capture checks
  cancellation first, then requires the exact live consumer and a concrete
  non-nil stored view definition; an invalid consumer returns
  `ids.ErrCatalogReplaced` with no references. Retained TEMP view lifetime is
  independent of pinning: `ViewReferenceValid` requires the exact captured
  catalog-relation descriptor and valid consumer, namespace, domain, epoch and
  attribute authority.

## Recipe rules

- **Selected recipes, not endpoints.** Stored trees keep the selected
  conversion: simple CASE arms keep their test conversion as a typed
  lexical-input template, and array-cast element templates keep their element
  routine. Remap and generation refresh include those templates and their
  routine dependencies and never rebuild a fixed coercion from endpoint types.
  Rendering emits ordinary simple CASE (the source is evaluated once) or array
  CAST syntax, never the placeholders. Selected datum casts render a literal
  with its source type before the target cast, so a quoted value never becomes
  target-codec input on replay.
- **View display constants.** Captured typed literals own their exact leaf
  annotation during deparsing; matching source casts do not add a second one.
  Outer casts and conversions to different usages remain. Captured sequence
  constants use the same path-aware regclass presentation as other literals,
  rather than the fully qualified replay spelling.
- **View source display.** Public deparsing uses current relation names, explicit
  aliases and natural output labels for every FROM frame, including joins.
  Frozen USING keys retain column aliases on their actual live operand slots;
  explicit alias lists follow those slots through physical column compaction.
  Duplicate derived labels receive unique display names before slot references
  are emitted. Unaliased CTEs retain their real names.
  Sole-source aliases remain qualified when another captured query frame refers
  to their slots. Input ORDER references to synthetic USING keys receive a
  collision-safe USING namespace when a displayed output label would hide them;
  nested frames do not inherit outer view column overrides. Public ORDER output
  expansion retains ordinals for DISTINCT selected occurrences, so scalar
  subqueries are not rebound as different expressions. Dump output references
  retain their frozen ordinals independently of public presentation.
  Replay retains its frozen namespace independently. SQL routine display uses the captured search
  path and exact signature to omit a namespace only when the same stored
  procedure is selected; identifier quoting follows the output environment.
- **Geometric literal recipes.** Ordinary view dumps obtain owning-type
  annotations from `regobj.ConstantLiteral`. Source-AST serialization elides
  only a matching direct captured literal cast with exactly the same Usage,
  outside presentation deparsing. Outer casts, domains, typmods and conversions
  to other usages remain. This keeps Dump/Restore/redump byte-stable instead of
  accumulating geometric casts; analyzed display and private scalar replay retain
  their separate formal-cast channels.
- **SQL/JSON.** Query nodes carry their RETURNING usage in
  `Rendering.OwnedReturning`; replay and display use it, not raw type spelling.
  Raw view capture keeps all query children and options. Reconstruction remints
  executable query/default plans after descriptor generations change.
- **Routine evolution.** Replacement first stabilizes routine, formal and result
  type handles, then remaps default and standard captures; address-only and
  full-reference maps stay distinct and typed children are retagged once.
  Normal type evolution refreshes ordered formal/input/output/result handles
  before dependent trees, copies only affected declarations and payloads, and
  shares unchanged identity. Its fast paths compare declarations with
  `exec.SameRoutineDeclaration`, never descriptor pointers; replacement/retag
  uses the broad carrier-change detector. Domain DEFAULT and CHECK consumers use
  the same declaration-aware predicate.
- **Address vs reference mapping.** Regclass datum OIDs use the address mapper;
  only actual captures enter checked source-domain/epoch reference remapping.
  There is no zero-domain lifetime bypass.
- **Calls.** SQL routine call presentation keeps written labels and explicit
  VARIADIC. `Capture` exposes only written operands; `Children` also includes
  omitted defaults for dependency, refresh and remap traversal.
- **Aggregates.** Owned aggregate recipes keep the original selected argument
  syntax and catalog reference/epoch; capture, count and render share
  `AggregateRecipeProcedure`. Transition and final expression remaps validate
  exact source and destination private formal vectors before and after mapping,
  alongside selected declarations and independent state/result identities.
  Ordinary stored parameters retain their zero-bound rejection.
- **Policies.** Policy runtime consumes `Policy.Expression`/`Presentation`
  through `RewriteViewExpression`/`MapExpression` and never mutates the payload
  or reparses its SQL. A presentation keeps the binder's full execution
  manifest, including pinned callable providers; `pg_depend` edges are a
  filtered subset (validation requires inclusion, not equal size).
- **TEMP replacement.** Full runtime manifests differ from catalog edges: exact
  retained builtin type generations, equal seeded collation capabilities and
  unchanged seeded procedure declarations may bridge reconstructed wrappers.
  User providers, operators and physical sources stay exact, and full
  remapping and final validation still run. Never remove a lifetime to make
  replacement pass.
- **Access methods.** `ProcedureProvider` validates exact registered
  access-method identity, signature, result and kind; dependency pinning follows
  real bootstrap handlers, not an OID cutoff.
- The engine, not these payloads, owns admission records, post-wait
  selection/ACL fences and lazy producer pin lifetime.

Installed cube comparisons retain the exact selected routine `ObjectRef` beside
the immutable procedure capture. This includes scalar/row comparison pairs,
distinctness, scalar-list predicates, NULLIF and each simple-CASE equality arm,
not only explicit cube function calls. Address remapping moves those references
before retagging; resolution uses only the mapped address, never source/name
fallback. One cube-specific validator checks both source/mapped input slots,
operation and procedure result, implementation, reference domain/epoch, actual
registered stock manifest and exact member. Equality returns BOOL even when its
NULLIF carrier returns cube. Rebinding reconstructs destination comparisons.
Row and subquery lexical walks visit comparison operands rather than their
carrier wrappers, so dependency collection explicitly records each pair's
selected routine reference in addition to the normal child traversal.
Other routine declaration/signature rules remain unchanged.
Rule WHERE conditions obey the same mapped-first ordering as rule action
bindings: remap references, then retag against the destination. Otherwise a
lawfully captured source lifetime reaches the new-epoch validator unchanged.

Scalar cube IN/NOT IN batches preserve an IN-list frozen recipe and renderer.
Their typed scalar children do not become a public ARRAY expression during
Dump/Restore. The lhs remains one occurrence and the existing evaluator retains
item order/NULL behavior. Actual array children and other operators keep their
ANY/ALL representation; no public cube-array provider/admission is added.

Implicit installed-cube equality syntax (scalar IN/NOT IN, NULLIF and simple
CASE) additionally requires a declaration binding context when rendered as SQL.
The dump owner derives that context only from validated immutable typed captures,
not SQL text or a familiar namespace name. Each actual owning DDL statement carries
its optional cube-namespace binding separately from its SQL. The emitted private
comment/SET protocol admits exactly one eligible statement per window and restores
the baseline after it; owner switches remain outside. Native Restore reconstructs
current stock authority, validates the actual newly bound target captures and
publishes only a completely validated stage. A dedicated external PostgreSQL
replay session finishes with RESET search_path (configured default, not an
arbitrary previously selected path). No-window dumps retain their previous bytes.

Comparison binding requirement traversal uses `SQLRoutineExpr.Capture`'s
validated written-argument projection, not its complete executable `Children`.
An omitted checked default belongs to the routine's declaration and does not
impose cube-namespace USAGE on a calling view owner. Explicit written comparison
arguments still require a caller window; declaration defaults are collected
separately. Runtime evaluation, hard dependencies, refresh and remapping retain
every expanded argument.

Domain CHECK manifests retain their actual selected cube operator reference as
well as the procedure/type references. Their provider validator admits only the
exact checked stock-cube operator: current reference lifetime, selected typed
capture, both operand slots, result/procedure, namespace and validated extension
member must agree. This does not grant arbitrary operator providers.
Independent source-literal retagging requires an explicit scalar translation on
an unpublished candidate. Publication clears that temporary authority; mapping
type descriptors alone does not authorize cross-ID scalar values.

## Invariants

- Rendering is byte-stable: `pg_get_viewdef`, `pg_get_expr`,
  `pg_get_constraintdef`, `pg_get_indexdef`, rule definitions, policy
  expressions, `information_schema` view text, stored defaults and checks and
  every Dump statement keep their exact bytes unless a change intends otherwise.
- Frozen payloads are immutable once published. Retag, remap and `With` build
  new values (copy on write) and never mutate a published `View`, rule list or
  `Policy`; source expressions stay immutable.
- Retag keeps the canonical mapper's property rules
  (`RewrittenIntrinsicProperties`: eliminated branches keep analyzed
  dependence; replaced child demand is dropped).
- `ProcedureProvider.Validate` and `TypeProvider.Validate` are the single
  validators, with fixed error text and order.
- No mutex and no engine state: every function reads only the `catalog.Read`,
  session token and arguments its caller passes.

## What stays in the engine

- View, rule and policy DDL entry points (decisions in `view`) and the
  Restore calls of `ReownExpressions` (`temporary_preservation.go`,
  `generated_replacement.go`). The imported rule OID remap is
  `control.RemapViewRuleOIDs`.
- Host forwarders into this package: the `analyze.Host` methods
  `MapBoundExpression`, `MapBoundQueryExpression`, `StoredViewVolatile`,
  `ValidateViewReadGraph`, `CaptureRetainedViewInput` (`analysis_host.go`) and
  the `pgcatalog.Runtime` methods `ViewDefinition`, `VisitRuleDefinitions`,
  `ExpressionReferences`, `ViewReferences`, `IndexReferences`, `PartitionReferences`
  (`catalog_source_runtime.go`). `IndexDefinition` renders through
  `index.DefinitionSQL`; relation deparsing is `table.Deparse`.

## Testing

Package units cover the payload API, rule presentation, routine recipes and aggregate recipes.
`TestViewPartsRoundTrip` and `TestRulePartsRoundTrip` require every `View`,
`Rule` and `RuleAction` field to have a parts twin that round-trips alone and
`With` to leave its receiver unchanged; extend them when adding a field.
Renderer-only units use frozen catalog fixtures here; engine capture, retag,
remap, `pg_depend`, provenance and validator tests live in `internal/engine`.
Public behavior belongs in `reference/`; follow [docs/TESTING.md](../../../docs/TESTING.md) and
add `REFERENCE=1` for the oracle.

Provider occurrence/shape proof:
`make check RUN='^(TestProviderRoutineOccurrenceIdentity|TestRegexpScalarViewPrefix|TestViewLiteralProtocolQueryBoundaries|TestStoredProviderCallShape)$'`.
Existing EXTRACT, OVERLAPS, SUBSTRING, SIMILAR, sleep and SQL/JSON aggregate
reference definitions retain independent PostgreSQL expectations.

Scoped historical graph/payload and imported-versus-TEMP principal proof:

```sh
make check RUN='^TestRoutineHistorical(BuiltinReplacementRemap|TemporaryPrincipalRemap|PayloadRemap)$'
make check RUN='^TestRoutineHistorical(BuiltinReplacementRemap|TemporaryPrincipalRemap|PayloadRemap)$' RACE=1
```

`TestSQLRoutineDomainRecipeTypeGeneration` distinguishes genuinely changed
current body/default semantics from valid equivalent COW: old hard captures
reject, while healthy fresh current manifests remain usable.

- Payload API and stored row marks: `RUN='^Test(ViewPartsRoundTrip|RulePartsRoundTrip|QuerySetStoredInheritedMarks)$'`
- Rule display and replay separation: `RUN='^TestRuleDefinition(Presentation|ReplaySeparation|ReturningLabels)$'`
- Routine recipes, remap and type-generation refresh: `RUN='^Test(Routine|SQLRoutine)'`
- PL array-loop usage remap and immutable controls: `RUN='^TestPLpgSQLForeachRemap$'`
- Aggregate recipes: `RUN='^Test(CreateAggregate(FrozenRecipeAuthority|TransitionFrameRemap|PolymorphicTransitionRemap)|AggregateFinal.*FrameRemap)$'`
- Engine renderer and `pg_depend` units: `RUN='^Test(Deparse|PGDepend|ViewCheck)'`
- Geometric captured-source and literal serialization: `RUN='^TestGeometryRelationLiteral'`
- Views, rules and deparsing: `RUN='^TestReference(Rule|Deparse|Materialized)'`
- Row security: `RUN='^TestReference(RLS|RowSecurity)'`
- Catalog projections: `RUN='^TestReference(PGDepend|PGTableViews)'`
- Stored generated columns: `RUN='^TestReferenceGeneratedStored'`
- Installed comparison capture and lifecycle: `RUN='^TestCube'`
- Installed earth captures and generation ordering: `RUN='^TestEarth(Coordinate|Point)'`
