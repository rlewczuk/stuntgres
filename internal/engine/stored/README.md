# internal/engine/stored

Stored SQL definitions and their presentation: the frozen payloads that catalog
descriptors carry (views, rules, policies, routine recipes), the renderers that
turn them back into SQL, and the mappers that retag and remap them. View, rule
and policy DDL decisions and rule rewriting live in [`view`](../view/README.md).

Dependency diagnostics use PostgreSQL object descriptions, not SQL declaration
spelling. In particular, extension descriptions retain the raw extension name
(such as `extension uuid-ossp`) in dependency messages and hints. This does not
change the explicit quoted extension names in membership or ownership errors,
or identifier quoting for other object classes.

The expression deparser is also the presentation owner for nonexecuting logical
plans. Callers supply bound column/stage/subquery presentation through its
existing callbacks; no callback evaluates an expression. Query remapping retains
immutable range/column aliases for source, derived, function and aliased VALUES
occurrences, sharing their alias slices while replacing executable identities,
schemas and expressions. One `ViewQueryRemapper` owns one logical operation,
including all query, CTE and mutation roots reached by that operation. Its accepted
roots retain canonical graph identity across those traversals; distinct self-join
occurrences remain distinct. Keep the source graph and mapping policy fixed.
Before changing the destination catalog or any mapping callback, use `Fresh()`
to copy the configuration without accepted query/CTE roots. The fresh operation
runs the ordinary source/current provider validation, including exact destination
generations and reference authority, rather than trusting an earlier accepted
root. An outermost failure discards partial operation memo state.

Frozen WAL summary view recipes retain written named operands and their formal
destinations when rendering both catalog presentation and replay SQL. A reordered
`pg_wal_summary_contents` call must not become positional during dump/restore.

Statistics recipes use these same expression remap/retag and stable-attribute
deparse owners. Replacement remaps relation/input/provider references and both
definition and execution trees before validating destination usages; it never
retains source OID authority or rebinds a rendered expression during ANALYZE.
Ordinary descriptor owner/target/name changes share immutable recipe identity.
Type-only normalization uses the existing routine-declaration change preflight:
unchanged callable declarations retain their original identity rather than an
operation-local clone. The complete expression mapper still checks hidden
type/collation fields, so genuine provider changes replace recipe backing.

XML recipes retain modes, whitespace/indent options and document negation through
rendering and frozen query presentation. Special XMLEXISTS lowers to the ordinary
qualified routine recipe, with normal function capture and replay; ignored passing
clauses require no separate frozen syntax or source-assignment authority.
Generated implicit cast calls retain a separate ordinary routine occurrence around
the complete written operand, including explicit casts. Normalization preserves
that occurrence order across capture and final query publication, so deparse and
replay use the actual selected cast procedure even after its name changes.
Serializer target Usage and every written XML source occurrence authenticate
against the actual accepted node in its owning query frame. Raw spelling cannot
supply descriptor or routine authority. Generic remap/retag rebinds hidden
conversion generations without reparsing XML. Lossless datum replay retains the
requested input mode. Literal validation pairs root syntax with its registered
query frame before traversal; detached replacement syntax has no authority.
Erased same-Usage source casts retain their exact frozen target annotations.
Standard CAST replay authenticates those boundaries before matching the surviving
operand position; raw cast spelling or a different canonical target cannot grant
source authority.

`TestXMLPredicateFrozenOwnership` also checks a real implicit catalog-cast operand
under ordinary XMLEXISTS: selected procedure association, invocation descriptor
generations and immutable backing survive rebuild/remap/identity retag. Same-valued
alternate routines cannot replace frozen executable occurrence references or the
selected cast's procedure/reference association. The public predicate lifecycle
carries that cast-bearing view through rename, deparse recreation and native
Dump/Reset/Restore with held, retained-row and fresh-reader assertions.

SQL/PL routines share callable captures. Language/body kind, declaration replay
sources and typed local Usages participate in validation, equivalence and remap;
declared pseudo signatures always pass catalog determinability and INTERNAL safety
checks, separately from concrete invocation validation. SQL declarations and
checked PL programs also pass language signature validation. No activation values
or evaluated PL expressions enter stored payloads.
Source-only PL definitions have neither syntax nor executable program; compiled
definitions have both. Provider validation admits source-only signatures without
language validation even if a validator was subsequently added. Current language
capability is rechecked independently of captured labels.
Remap/reownership preserve that distinction and never retain invocation frames.

`RoutineProgram.DefinitionSQL` presents SQL-standard routine bodies under captured
catalog/path/output authority, using metadata validation rather than executable
re-admission. Callee implementation replacement does not invalidate retained
declaration display; current logical identities still determine names. Replay
`StatementSQL` keeps its stricter executable capture checks. Top-level RETURN
and RETURN statements inside an atomic wrapper retain explicit parser facts.
Routine declaration queries retain relation qualification even for a sole source,
and atomic continuation clauses inherit the statement's one-space body indent.
Ordinary view display and executable replay keep their existing layout policies.


Type inquiry recipes retain their selected declaration, ordered demanded
operands and logical type witness through generic rebuild, retag and remap.
Unknown literal and analyzer-confirmed pseudo-cast syntax remains unknown on
replay despite concrete TEXT execution transport. Genuine string-to-UNKNOWN IO
casts retain actual producers and logical target705 in the existing cast Usage
map, render a real UNKNOWN cast (not TEXT or no-op elision), and validate target
catalog ownership through a bounded metadata-only declaration helper. Retagging
revalidates source/target/operation/properties without copying unchanged backing.
Every original transparent cast retains its resolved target and normal frozen
child ordinal. Literal chains terminate at the exact authenticated raw literal;
IO chains terminate at one genuine executable conversion. Rendering may elide
authenticated no-ops, never real IO or explicit concrete casts. Publication checks
every target generation and chain order. Frozen cast usages must also match the
actual bound IO carrier or transparent step at the original source occurrence:
complete syntax, including positions, and its owning query frame are authority,
not neighboring entries in the cast map or canonical descriptor ownership alone.
The same literal-validation seam rejects cast-usage substitutions from UNKNOWN
to TEXT, concrete to UNKNOWN, and between outer steps in both views and policies.
An actual explicit concrete TEXT exit is a separate ordinary coercion around
the authenticated inner carrier: frozen and scalar rendering retain that boundary
and replay concrete identity25 without replacing genuine source-output IO.
Identity retag/remap preserves accepted backing and rejected publication cannot
mutate it. These captures grant no executable pseudo usage or unbacked
presentation authority.
Stable inquiry outputs are not serialized or retained as planning constants.
Snapshot literals retain their nominal `pg_snapshot` or `txid_snapshot` cast;
transparent relabels do not add another cast during replay. Live current-snapshot
and transaction-identity calls stay symbolic with their selected modern or legacy
routine identity through native catalog replacement.
Procedure result policy remains declaration-owned through validation and remap:
every OUT/INOUT tuple is RECORD even with one member, while no-output is VOID.
The shared original-occurrence memo maps output shape, formals and child
generations once; same-OID foreign/unshaped descriptors remain invalid.
SQL/PL declarations and the authentic installed insert_username trigger body may
carry definer security and routine-local configuration. The common procedure
validator rejects these states for native bodies that do not enter the shared
environment, before delegation or intrinsic early returns. Installed symbol
validation enforces the same boundary for capture/remap consumers. Scalar
insert_username recipes retain the selected provider and object reference,
never an enclosing trigger activation. Builtin/installed strictness and
volatility remain sealed; parallel/cost/rows patches do not relax them.
Allocated Snowball C helpers use their typed bootstrap declaration capability,
not the fixed builtin-OID registry or an externally callable INTERNAL ABI.
Pinning follows fixed bootstrap authority, independently of seed replay markers:
allocated Snowball helpers, template and English dictionary/configuration are
not pinned, so their genuine normal dependency edges remain visible under
rename and mixed extension membership. Fixed simple and default-parser
identities remain pinned.

Installed autoinc scalar carriers retain sealed Procedure/Ref authority through
rendering, reference remap and type retagging; ordinary scalar invocation cannot
borrow trigger activation. Trigger replay uses the helper's current identity
and escaped literal arguments, with no column/sequence name dependency. Triggers
remain post-data so manually retained and generated keys are not processed twice.

Installed crosstab consumers retain their selected procedure and genuine named
result generation, or their completed anonymous record definition. Reference,
remap, retag and rendering walkers preserve those captures and resolve current
presentation independently of mutable function/type labels. Dynamic inner SQL
remains text, not a frozen dependency on the relations it may later resolve.

Installed ltree array calls retain both exact formal generations, operation and
orientation, original procedure declaration and catalog-domain reference.
Captured-source validation precedes authorized remapping; the sealed released
procedure slot/symbol, current destination principal/provider and exact nominal
and companion generations are checked before retagging or rendering. Mutable
membership and a live originating extension are not execution authority. Current
reciprocal membership remains independently validated. Replay casts both operands
to genuine destination formals and preserves source occurrences/domain/epoch;
no matching scratch, caller authority or result-array resource is serialized.
Scalar hierarchy calls (`LtreeHierarchy`) and mixed scalar label-query calls
(`LtreeLtxtquery`) use the same original/current declaration authentication,
independently exact operand generations and destination-owned BOOL result.
The retained member is a sealed declaration slot, not a position in current
extension membership. Both qualified operators and genuine direct calls survive
detach, foreign attachment, original-header retirement and authorized transport;
mutable names and equal numeric OIDs never authorize remapping.
SQL dependency recording distinguishes explicit routine calls from selected
operators: operators reference their operator identity and actual operands,
including genuine nested function calls, while executable backing-procedure
authority remains separately retained.
Direct mixed scalar label-query calls record the actual selected procedure
address; their operator form records only the operator plus recursively visited
operands, without promoting its backing carrier into another SQL dependency.
Unchanged-generation retagging rejects captures newer than the current routine
revision, while historical presentation/ACL revisions remain valid. Authorized
cross-generation import preserves source revisions independently.
`LtreePath` retains an active count and inline ordered formal/operand generations,
including every repeated scalar-LCA occurrence. Original source declarations and
genuine destination sealed origins authenticate independently before remap,
retag or current-name rendering. The retained member is the immutable declaration
slot, not current SQL membership; path signatures and nominal array formals use
the origin's current registered type authority. Qualified replay casts each active
operand to its genuine destination formal; no caller scratch, array cursor or
authority is stored.
`LtreeLquery` authenticates scalar positional-match captures with the same
source/current/destination fences, preserving both distinct formal generations,
orientation, sealed member ordinal and source-ordered operand occurrences.
Neither carrier requires a live origin header or matching current membership;
current reciprocal links and principal/provider authority remain independently
validated.
Frozen lquery literals own their qualified typed annotation, like ltree and array
datums. Query replay elides only a source leaf cast with the exact captured Usage;
outer casts, domains and conversions remain intact. Formal operand annotations
and source capture provenance are retained independently of this presentation.
Captured non-domain ltxtquery literals own their typed leaf annotation during
replay. Reuse only an exact matching leaf usage; preserve outer casts, domain
checks, typmods and real conversions so repeated transport remains deterministic.
Ltree comparisons use the existing scalar/row, distinctness, membership,
NULLIF and simple-CASE carriers. They retain both operand generations, selected
immutable routine, operation and catalog-domain/epoch reference. Transport
authenticates the original capture and destination declaration independently,
shares immutable operand backing and rebinds typed comparison in the destination.
Implicit comparison replay uses the authenticated current operator namespace,
independently of its movable backing routine; current names never replace
captured identity.
Sort/window ordering first validates sealed original class/family/member/routine
declarations, support revisions and their exact kernel pairing. It maps all
four genuine references consistently and authenticates fresh destination support
with the shared index binder, never a builtin or current-default fallback.
Reference-only traversal shares both source comparator and original capture
until the typed remap can validate and rebuild destination ownership.
Dependency traversal retains those four captured support references as well as
the ordinary key-expression dependencies.

Installed hstore array, JSON and JSONB calls retain exact formal/result generations,
conversion mode and selected procedure/member references. Comparison and hash
nodes retain every strict operand, operation, selected declaration and catalog
domain/epoch reference. Their remap, retag and qualified rendering paths validate
authentic source and current declarations independently of presentation names.
Within one type generation, a retained hstore declaration may precede the current
presentation revision but cannot come from a future revision. Cross-generation
transport preserves the original declaration revision rather than refreshing it.
Set-returning hstore captures also preserve projection kind and exact TEXT/fixed
OUT record usages. Remapping authenticates original and destination members
independently; retagging rebuilds typed plans without retaining invocation state.
Current rendering resolves checked current schema/name rather than captured spelling.
Implicit equality in predicates, membership, CASE and NULLIF retains the same
installed authority; replay binding windows require its genuine installation.
Constructor, lookup and predicate nodes retain ordered formal/result generations,
including installed constructor results and consumer inputs. Generic
selected-operator recipes retain prefix, comparison and binary lookup identities
with their actual backing routine.
Record constructor/replacement calls retain declared RECORD/ANYELEMENT signatures
separately from effective tuple usages. Current and original provider validation,
authorized remap and qualified rendering preserve that distinction, including
the `#=` backer, domain identity and anonymous occurrence shapes.

`Trigger`/`TriggerList` provide a checked relation-owned `TriggersPayload`, sorted
by table-local name. Definitions retain routine/attribute identities, immutable
WHEN recipes, explicit timing/orientation and row-only partition-clone provenance,
never activation values. Checked kinds are BEFORE/AFTER ROW I/U/D,
BEFORE/AFTER STATEMENT I/U/D/TRUNCATE, and view-only INSTEAD OF ROW I/U/D.
Views otherwise admit BEFORE/AFTER STATEMENT I/U/D. INSTEAD OF definitions cannot retain
WHEN, UPDATE OF or partition provenance; statement definitions cannot be clones.
Routine capability is shared with DDL and runtime: genuine zero-input PL DML
programs or the three checked builtin DML declarations. Provider/provenance checks
remain mandatory. Builtin string arguments stay literal owned strings, without
hidden column/configuration dependencies or rename rewriting. Dump preserves
their order and writes triggers after data so replay does not recompute vectors.
Rename retains every identity and recipe while reordering the table-local chain.
`WithMode` replaces only the definition envelope, retaining the WHEN binding,
presentation, arguments and dependency backing. User and RI triggers share checked
catalog O/D/R/A modes. Nondefault modes and clone-local states form a final
deterministic post-definition dump phase using ONLY; ordinary CREATE supplies
origin mode without a redundant ALTER, including for view-owned triggers.
Complete four-slot FK mode footer records identify the logical owning
constraint, never allocated RI names/OIDs, and sort independently of allocation.
A shared SQL seal permits a per-FK snapshot data waiver only for the exact saved
stream; metadata validation remains independent of that waiver.
CHECK/FK deparse renders NOT ENFORCED in preference to NOT VALID. Native replay
uses ordinary bound post-data declarations for unenforced facts and their
violating rows. An absent FK implementation produces no trigger-mode footer
records; enforcement is not serialized as a fabricated disabled trigger mode.
UNIQUE/PK and FK SQL renders persistent timing defaults, including USING INDEX
replay, never transaction-local SET overrides or pending integrity events.
Automatic extension addresses are constructor-owned, sorted unique metadata,
separate from normal function/WHEN references and extension membership.
Mapping preserves source snapshots and resolves extension addresses in the
destination; SQL serialization omits regenerable row-clone CREATEs, renders current
names, and emits clone-local dependency ALTERs after all parent definitions.
Retained row clones beneath statement parents emit their real child CREATE with
a strict positioned native-link record; PostgreSQL replays the SQL behavior,
while only native Restore reinstates historical parent provenance.
Serialization renders the actual timing, orientation and events in the post-data
section; queues, row images and firing progress never enter persistent payloads.
Partition synchronization may replace a conflicting local child definition only
along the exact CREATE OR REPLACE root definition's stage-local clone lineage.
Ordinary CREATE, ATTACH and unrelated catalog synchronization retain conflicts;
replaced local definitions and their dependency edges retire atomically.
Clone matching follows exact Parent identity before checking destination-name
collisions. Existing clone OIDs, local modes and local extension edges survive
unrelated parent resynchronization; new clones inherit parent mode, not extension
edges. Actual CREATE OR REPLACE retains identity but resets the replaced
definition and its row-clone lineage to ordinary O.
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
`VisitTriggerDefinitions` validates actual payload and routine/attribute provenance
against one immutable root and renders metadata without invoking code. Its captured
path resolves zero-argument routine names; WHEN uses current attribute labels and
lowercase pseudo-row aliases. Action arguments double single quotes and, only
when captured standard-conforming strings are off, backslashes; no E prefix is added.
This presentation is distinct from portable, qualified declaration serialization;
neither path changes executable recipes or their shared immutable backing.
`VisitTriggerCatalog` instead projects actual definition identities, event bits,
ordered attributes and literal arguments without presentation privilege filters.
Its `TriggerCarrier` retains the actual element of the immutable `TriggerList`,
not a detached same-name/OID copy, together with WHEN, presentation and binding.
`TriggerTree` checks captured owner, type generation and payload membership.
Structural output uses the shared native logical writer without evaluation.
The `pg_get_expr` consumer validates scalar Scope/Frame and whole-row RuleSide
before opening the supplied relation: zero context rejects all Vars, nonzero
context rejects NEW, and OLD uses that relation's columns and whole-row name.
Cancellation never publishes a partial capture or poisons later lazy output.


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
  Ordinary rules carry checked catalog-owned O/D/R/A enable modes. Parts/With,
  remap, identity replacement and rename preserve mode independently of action
  recipes. Zero/unknown modes fail validation. Rule dump groups emit a
  nondefault mode ALTER immediately after CREATE in the same post-data owner
  group, using current quoted names; data replay precedes rule installation.
- **Routine recipes.** `RoutineProgram` owns checked SQL-standard actions.
  Their `StructuralOutput` retains ordered concrete parser nodes and fields,
  including command/expression kinds and literal values; it does not prepare,
  evaluate, deparse or consult a session/catalog. `proc.NewRoutineDeclarationBody`
  instead privately parses immutable released builtin/installed syntax once and
  explicitly grants no checked program or dependency authority. Both use native
  structural encoding, not PostgreSQL `nodeToString` or SQL text masquerading as
  `pg_node_tree`. Quoted SQL/PL bodies have no standard-body tree. Lazy tree output
  runs outside locks and caches only successful output; cancellation does not
  poison retained values.
  Checked action syntax retains normalized positional `parser.ParameterExpr`
  inputs, not original formal-name column references; structural output records
  those actual immutable nodes rather than reconstructing source spelling.
  `ValidateMetadata` checks retained structure, input/definition association and
  manifest provenance without demanding current callee executable equivalence.
  Catalog capture therefore retains genuine old program/default owners after
  legitimate callee attribute changes or same-signature replacement. Executable
  `Validate`, `ValidateAction` and `DefaultSQL` retain their admission checks.
  The explicit `Deparser.RoutineDeclaration` mode is limited to declaration
  default presentation: SQL routine references retain local provider/ref/principal
  checks and resolve current labels through logical address/signature/kind/result,
  not current executable attributes or language-handler admission.
  Routine action leaves retain their creation-time `FrozenAttributes`, just as
  rule leaves do. Invocation refreshes live read/admission slots, while the
  existing source-layout bridge presents rows in the retained recipe layout.
  Every supported action source opening, including MERGE with an unchanged
  target, consumes this provenance after raw-source selection. Its executor owns
  the returned bridge/bundle through normal finalization and close; no activation
  state enters the stored program. Unused or CASCADE-retired source columns cannot
  shift a surviving body's reads. No rendering, reparsing or name rebind is involved.
  Actual modifying-CTE child bindings retain the same source provenance, using
  the existing immutable recipe/definition mapper rather than substituting an
  outer source list at invocation. Independent SELECT-CTE owner states and
  foreign target/input openings consume that provenance through their real
  source lifetimes, not a routine-specific scan or alternate evaluator.
  Per-presentation occurrence maps retain genuine raw source owners, including
  mutation frame-1 VALUES/function/JOIN-ON syntax. `LiteralRoots` shares the
  checked raw/source enumeration with strict recursive validation; missing,
  extra and malformed paths/counts remain errors. Every full original capture
  key has an owner, and shared CTE occurrences may belong to several parts.
  Original Capture, namespaces, dependencies and read/target authority remain
  complete. A Query-less DerivedNode is valid only for an actual nonrecursive
  MutationBinding with supported statement and coherent RETURNING, definition
  and scan widths/Usages. Ordinary derived queries retain their QueryRoot checks.
  Shared `VisitRuleModifiers` enumeration requires a complete statement-to-
  presentation association; immutable `ActionSyntax` rejects swapped or orphan
  modifier presentations and survives With/remap. No synthetic executable Query
  or snapshot-format change supplies this association.
  The main and exactly associated modifier presentations jointly cover the full
  action source manifest, including sources reachable only from a child. The
  incumbent leaf traversal validates each node against its own ordinal leaf and
  the original outer binding authority; child prefix manifests keep global
  ordinals. Coverage finalizes once after all roots, while frame/rowmark and
  duplicate-node checks remain local to each presentation. Ordinary
  `ValidateViewLeaves` still requires complete per-view source coverage.
  `RoutineDefaults` owns an immutable input-indexed declaration-default recipe
  and its source-domain/epoch manifest, never evaluated values. Default
  dependencies are hard even for quoted bodies. Named composite fields retain
  their exact stable attribute address and owning relation's captured revision;
  only descriptor lookup normalizes to the whole owner. Capture honors staged
  definitions, and validation, rendering and remapping preserve the original
  provenance and revision while using checked current labels.
  Tableoid occurrences use a tagged `ViewColumn` capture with full source ref,
  lexical frame and occurrence, never an ordinary slot. Validation closes the
  capture over the executable source; rendering uses that source's checked
  current qualifier. Frozen leaves retain a separate table SELECT requirement.
  Executable and presentation refs remap once together, preserving attribute -6
  in semantic dependencies without weakening foreign-domain or epoch fences.
  Frozen typed identifier operands keep operand-only dependency purpose through
  view, rule and routine-default manifests. Actual relation/rewrite sources and
  callable uses remain executable; executable use of the same identity wins.
  Both purposes remain in ordinary analysis validity. Only the executable
  projection governs retained-fold retirement; neither purpose caches authority.
  Routine public name/namespace/owner are mutable presentation. Same-address
  declaration checks retain kind, exact signature/result, semantic attributes,
  immutable configuration identity, SQL/default/body and provider association.
  Logical replay compares configuration values while original retained carriers
  preserve backing identity. Installed captures validate their real manifest slot
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
  `Dependencies()` is the complete tagged capture manifest, not a semantic edge
  projection. `DefinitionDependency` captures written references;
  `AggregateSupportDependency` captures selected implementation/admission
  providers, including synthetic builtin recaptures. Ordinary and window provider
  carriers retain forward, inverse and final descriptors and scalar calls in
  that support lane; each requires its own original capture even when an
  implementation is not invoked. Deduplication retains
  `(Ref, Purpose, original Revision)`, so mixed same-address historical captures
  never borrow each other's authority. Reached-action validation checks original
  descriptors and exact purpose/ref/revision correspondence, not the multiplicity
  of identical recording occurrences. Full publication/remapping additionally
  checks complete action occurrence counts and the program-wide set. Missing support-only captures
  cannot be repaired by a plausible direct graph or a fresh name lookup.
  Remapping preserves every purpose, original stamp and source immutability.
  Stored views project written definition and operand captures in `DependsOn`;
  support-only execution captures do not bypass the aggregate's transitive edge.
  An explicitly written support call keeps its independent direct edge.
  `OperandDependency` separately records identities used only as constant-input
  operands. A written definition use subsumes an operand capture only at the
  same Ref and Revision; it never subsumes an aggregate-support capture.
  Routine construction and full validation/remapping use the same canonical
  program-wide union, with exact count and membership checks. Definition
  dominance is independent of recording order, and column captures suppress
  whole-relation captures only within their own purpose. Normalization never
  changes the exact original per-action captures or their occurrence counts.
  Prepared executable-fold validation ignores operand-only revisions but
  requires exact aggregate-support stamps before considering declaration or
  codec equivalence.
  Regproc/regprocedure definition references are validated from checked retained
  literal occurrences and the presentation's original Definition or Operand
  Ref/Purpose/Revision, then matched exactly to the action's complete triple.
  A shared aggregate-support address neither
  authorizes a forged direct lane nor converts a literal into an EXECUTE call.
  Pre-remap checks retain original literal authority without a live source read;
  full target occurrence/type validation follows remapping.
  Type refresh uses the full manifest: SQL=nil aggregate descriptors cannot
  bridge a support-only caller's refresh dependency. Only the shared catalog
  definition visitor projects ordinary direct edges for publication, pg_depend,
  DROP and dump ordering; body and defaults remain separate recording passes.
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
  Diagnostics instructions share syntax/slots/item identities through this
  remapper; validator admission precedes sharing. No frame, status, handled error
  or live provenance is retained by a stored definition.
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
  Public expression deparse presents adjacent AND/OR nodes with the same
  connective as a flat PostgreSQL boolean chain, retaining operand grouping and
  mixed-connective precedence without changing the binary executable tree.
  Scalar-array presentation exposes selected-operator input casts; homogeneous
  implicit IN-item conversions render around their common-type array, without
  changing the executable items or the scalar replay path.
  Builtin identifier-type annotations preserve captured type-name visibility and
  identifier quoting; SQL grammar type names such as json remain grammar text.
  Nondefault literal collations are typed constant annotations with captured
  visibility/quoting; explicit COLLATE expressions retain their own grouping.
  `VariadicRoutineName` reuses callable shape and overload
  ranking against actual arguments for nonexecuting identity presentation;
  released metadata never becomes an executable routine descriptor.
  `QueryRenderer` and `Presentation` (`view_deparse*.go`,
  `view_render_sources.go`, `sqljson_deparse.go`): whole queries,
  `View.SelectSQL`, `ViewDefinitionSQL` (`pg_get_viewdef`) and `PolicySQL`.
  Public display of a sole noncorrelated aliased table uses current relation/
  attribute names, unqualified columns and only nonredundant column aliases.
  Correlation-participating frames retain lexical qualification; local keys use
  the actual emitted alias, never a relation name hidden by it. Ordinary view
  dumps retain executable positional bridge aliases and unique column labels;
  policy replay's logical naming is not an ordinary-view replay authority.
  Rule introspection reconstructs the current stored SELECT using the same
  authority as view display. Native Restore retains canonical executable
  definitions and metadata, not original source alias spellings; subsequent
  introspection may legitimately show the replay's synthetic aliases.
  Actual `_RETURN` SELECT display carries `Presentation.TemporaryOwner` from
  the querying request, not the stored owner. Source visibility is checked
  before qualification; hidden own-TEMP sources use `pg_temp`, while foreign
  or zero-owner requests retain physical namespaces, including quote-all.
  Naming neither allocates a TEMP namespace nor changes executable replay.
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
  Pretty action expressions receive the same reduced-parenthesis policy as
  their containing rule; default display and native replay are unchanged.
  Trigger function names resolve current routine identities against the captured
  path independently of target naming. RI trigger functions use the release
  identity inventory, not a fabricated executable Procedure payload.
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
  `view_rule_presentation.go` owns structural continuation indentation and the
  derived/CTE source-alias helpers shared by rule display and policy
  logical-source rendering.
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
  `RemapScalar` (`expression_remap.go`), `RemapViewQuery` and `ViewQueryRemapper`
  (`remap_view_query.go`, `remap_view_providers.go`), `RemapLikeExpression`;
  `RemapView` (`frozen_presentation_remap.go`).
  `PruneFoldCaptures` (`fold_captures.go`) validates exact default/view/routine
  definition origins without retaining executable trees or using unrelated
  relation revisions as stored-definition tokens. Frozen `DefinitionRevision`
  identifies a defining expansion separately from mutable presentation metadata.
- **The remap protocol.** `ReownExpressions` (`expression_reown.go`) remaps and
  retags every stored expression of a catalog state after a renumbering
  (Restore, extension owner import, DROP OWNED, TEMP preservation) or after type
  changes only. It walks domain checks and defaults, column defaults and
  generations (`RefreshGenerationChecks`), index keys and predicates, partition
  bounds (`RemapPartitionDescriptor`), rules (`RemapRules`, `RemapRuleBinding`,
  `RemapRuleConstraintIdentities`), policies (`RemapPolicies`) and views. The
  rule tree mapper (`NewRuleTreeMapper`, `MapRuleBindingRecipes`,
  `RefreshRuleBinding`) is shared with rule rewriting. `ReownScalarTypes`
  points relation column types and stored datums at current type generations.
  `ReownVersions` uses only its copy-on-change row transport against an immutable
  catalog snapshot; it does not normalize shared descriptors. Deferred typed
  projections transport produced nested values before CHECK/index consumers,
  while old source tuples and SQL assignment coercions keep their own authority.
  `NewRoutineRemap` initializes one scoped `analyze.RecordTypeMapper` before
  any current/historical payload, query or scalar transform. Its existing
  `catalog.RoutineRemap` carries the complete Type/Collation transformation,
  as well as the original procedure and SQL-definition/body/default memos.
  Bodies and defaults consume that same context, including binding, frozen and
  modifier presentations, PL locals and presentation collation inventories;
  no payload creates its own occurrence mapper or destination type lookup.
  Formal-derived routine records use this same original-occurrence memo as
  their call expressions and frozen presentations. Mapped output formals and
  result members must agree exactly; no secondary reconstruction creates a
  competing shape or maps an already mapped child. Unresolved polymorphic
  declaration tuples rebuild through `RecordSignatureShape`, retaining canonical
  member admission and no executable authority; concrete tuples continue through
  `RecordShape`. Provider validation checks ordered names/attributes/Usages,
  TABLE/set policy and child catalog ownership.
  Named results retain their exact catalog descriptor fence; RECORD OID alone
  grants no provenance exemption.
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
  `ValidateTypeProviders`; `PruneFoldCaptures`; installed-node validators
  (`ValidateInstalled*`); the view read graph and volatility
  (`ValidateViewReadGraph`, `ViewVolatile`, `CaptureRetainedInput`); row-mark
  readers (`view_row_marks.go`); `HasDynamicSourceDependency` (installed
  crosstab, pgrowlocks, pageinspect, connectby and amcheck capture); view validation and
  view-write shape; `ValidateRules`; and `DependencyObject` (object
  descriptions for DROP and role diagnostics, including global procedural
  languages and text-search templates, dictionaries and configurations with
  class-specific search-path visibility).
  `ViewWriteShape` and `ViewUpdatabilityShape` share one automatic-admission
  implementation; only the write route collects execution predicates.
  Metadata shape inspection neither allocates that vector nor evaluates it.
  Literal references use the existing typed-alias walkers, including
  regproc/regprocedure identities as well as type, operator and text-search
  aliases. NULLs, text spelling and unrepresented routine numbers create no
  routine edge; pinned references remain excluded.

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
  Replay serialization likewise elides the exact captured leaf annotation for
  typed hstore and `pg_lsn` constants, including NULL, without removing written
  outer casts, domain checks or conversions to another usage. Selected argument
  transport reuses an explicit source cast only when its captured full Usage
  exactly matches the formal, keeping repeated portable replay idempotent.
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
  Installed cube source leaves use the same direct-leaf, exact-Usage rule;
  their type-owned annotation remains single across replay without erasing outer casts.
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
- **Aggregates.** Owned grouped and window recipes keep original selected
  argument syntax and actual aggregate catalog reference/epoch; capture, count
  and render share `AggregateRecipeProcedure`, not provider-symbol lookup.
  Window membership stays with the captured complete specification. Forward,
  inverse and final remaps validate exact closed source/destination namespaces,
  row/direct partitions, expanded variadic usages, retained conversions and
  selected declarations. Actual final vectors retain direct inputs before typed
  NULL extras. Opaque ordered-support recipes rebuild with destination tuple
  comparisons and contain no execution state. Kind, direct count, modes and
  independent state/result identities survive replacement without name lookup.
  Mapping never mutates the source provider. Ordinary stored parameters retain
  their zero-bound rejection.
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
The dump owner derives that context only from validated immutable typed captures
and the genuine operator in their sealed installation inventory, not SQL text,
current membership, or a familiar namespace name. Lookup follows the operator's
current namespace: renaming or moving its backing equality routine independently
does not move the hidden `=` lookup. Exact operands, result, backing routine,
installed origin and current operator owner authority remain checked; dependency
ordering still visits the authenticated current backing routine. Simple CASE
keeps its original syntax, once-only test and ordered, lazy per-arm conversions,
including NULL and parameter semantics. Each actual owning DDL statement carries
its optional comparison-namespace binding separately from its SQL. The emitted private
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

Checked default roots use `Procedure.RoutineDefinition().Inputs` for SQL, PL and
compiled-host declarations alike. `VisitDefinitionExpressions` exposes only
present defaults, once in input-slot order, so comparison validation and range
inspection share the actual payload owner. A host declaration still has no SQL
program: SQL-standard action/query traversal remains guarded by its SQL body.
The shared routine remapper reowns host layouts, typed default operands and
dependency references in the destination catalog without mutating source
artifacts or reparsing a substitute default.

Domain CHECK manifests retain their actual selected cube operator reference as
well as the procedure/type references. Their provider validator admits only the
exact checked stock-cube operator: current reference lifetime, selected typed
capture, both operand slots, result/procedure, namespace and validated extension
member must agree. This does not grant arbitrary operator providers.
Independent source-literal retagging requires an explicit scalar translation on
an unpublished candidate. Publication clears that temporary authority; mapping
type descriptors alone does not authorize cross-ID scalar values.

Trigger/rule introspection is display, not native replay. `TriggerDefinitionSQL`
shares nonexecuting WHEN, current routine naming and argument quoting with the
information-schema visitor, while retaining that visitor's separate disclosure
policy. Display emits canonical INSERT/DELETE/UPDATE/TRUNCATE order, stable
UPDATE OF attributes and current identities, but no mode/ownership/clone replay
annotations or final semicolon. FK display consumes shared validated logical RI
facts; FROM visibility is independent of the event target in both formats.
`RuleDefinitionSQL` carries pretty through frozen conditions/actions; pg_rules
explicitly requests default format. `SelectRuleDefinitionSQL` preserves the owned
SELECT body's leading space and adds the rule's one terminal semicolon.
Default event targets are qualified; pretty targets and trigger function names
resolve their own captured-path visibility. Rendering does not evaluate WHEN or
actions or replace frozen query/binding backing. Native Dump keeps its own
post-data replay, modes, dependencies and clone policy; saved rows are not fired
through the saved definitions on Restore, and views remain CREATE VIEW rather
than duplicate `_RETURN` declarations.

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

`PolicyCarrier` retains the actual immutable `Policy`, analyzed expression and
complete frozen `View` presentation for catalog trees. Structural output visits
bound expression/query nodes, including subqueries, rather than replaying SQL.
`PolicyDisplay` uses request-private query-renderer state and the caller's captured
namespace visibility/current display names, not the creation actor's privileges;
`PolicySQL` remains the qualified native dump recipe. Retained carriers never
look up replacement policies by name or OID. Successful lazy datum output is
shared atomically; a canceled attempt publishes nothing.
The policy's frozen root is a syntax-less expression frame with its real owner
source, not a SELECT. Display seeds the outer range-name reservation from that
owner before visiting captured subquery SELECT frames; it does not manufacture
FROM syntax, replace retained frames, or rebind correlations. Nested sources
inherit that reservation through the ordinary rule-display range-name owner.
Policy SELECT bounds retain PostgreSQL's one-space LIMIT indentation independently
of ORDER BY. Row-comparison sublink operands display as test tuples, not ROW
constructors, and retain their enclosing parentheses in both pretty modes.
Identity SQL value-function nodes display as uppercase keywords only in policy
display; their lowercase qualified-dump replay rendering is unchanged.
`TestPGPolicyDisplayRendering` exercises actual frozen scalar/window/row payloads;
`TestPGPolicyDisplayIdentityKeywords` checks keyword display in both pretty modes,
unchanged replay SQL and frozen payload ownership.

Policy replay shares only the structural range-table naming owner with logical
display. Its request-private source mode retains genuine aliases (including
generated-looking user names), explicit column aliases and lexical correlation
reservations, while unaliased providers and columns follow captured identities
to current names. Provider SQL remains schema-qualified; expression, literal,
type, operator and routine rendering remains in replay mode. Consequently actual
Dump/Restore does not turn positional bridge aliases into logical policy aliases.
No SQL-text normalization, alias-name heuristic or parallel provenance registry
is involved. Ordinary view/rule/routine replay modes remain unchanged.
`TestPGPolicyDisplayRestoreSources` covers actual native replacement, stable
redump/replacement, renamed providers/attributes and retained payload ownership.

## Testing

XML frozen target/operand occurrence authority, selected conversion association
and hidden conversion generations use
`RUN='^(TestXMLFrozenCaptureOwnership|TestXMLSerializationAssociation)$'`.

Type inquiry immutable recipe ownership and frozen view/policy cast-occurrence
authority use `RUN='^TestTypeDescribe(StoredOwnership|ComposedStoredOwnership|FrozenCastOccurrence)$'`.
Scalar hierarchy and positional-match source/destination authority and immutable
graph ownership use `RUN='^TestLtree(Hierarchy|Lquery)Stored'`.
Bounded path expression/capture and source/destination ownership use
`RUN='^TestLtreePath'`, including repeated operands and rejected remap publication.
Mixed scalar label-query ownership uses `RUN='^TestLtreeLtxtqueryStored'`.

Package units cover the payload API, rule presentation, routine recipes and aggregate recipes.
`TestViewPartsRoundTrip` and `TestRulePartsRoundTrip` require every `View`,
`Rule` and `RuleAction` field to have a parts twin that round-trips alone and
`With` to leave its receiver unchanged; extend them when adding a field.
Renderer-only units use frozen catalog fixtures here; engine capture, retag,
remap, `pg_depend`, provenance and validator tests live in `internal/engine`
and `internal/engine/core`.
Public behavior belongs in `reference/`; follow [docs/TESTING.md](../../../docs/TESTING.md) and
add `REFERENCE=1` for the oracle.
Frozen source-identity captures and remapping use `RUN='^TestStoredTableOID'`.
Formal-derived routine result validation and shared occurrence remapping use
`RUN='^TestSQLRoutine(Result|Procedure)'`, also with `RACE=1`; malformed shapes and foreign
child generations must fail without changing the original declaration.
Installed declaration and generated constructor ownership use
`RUN='^TestRoutineFormal(Installed|Constructor)OwnerRemap$'` with `RACE=1`.
These units map real installed manifests and actual generated type families,
preserving immutable declaration payloads while changing owner identities.


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

- Payload API, definition dependency projection and stored row marks: `RUN='^Test(ViewPartsRoundTrip|ViewDependencyPurpose|RulePartsRoundTrip|QuerySetStoredInheritedMarks)$'`
- Rule display and replay separation: `RUN='^TestRuleDefinition(Presentation|ReplaySeparation|ReturningLabels)$'`
- Routine recipes, remap and type-generation refresh: `RUN='^Test(Routine|SQLRoutine)'`
- PL array-loop usage remap and immutable controls: `RUN='^TestPLpgSQLForeachRemap$'`
- Diagnostics remap/admission ownership: `RUN='^TestPLpgSQLDiagnostics'`
- Aggregate recipes: `RUN='^Test(CreateAggregate(FrozenRecipeAuthority|TransitionFrameRemap|PolymorphicTransitionRemap)|Aggregate(Final.*FrameRemap|MovingFrameRemap))$'`
- Complete aggregate capture purpose, validation and remap invariants:
  `RUN='^TestRoutineAggregate(DependencyPurpose|ManifestValidation|PurposeRemap)$'`
  (also run with `RACE=1`). Includes original mixed revisions, synthetic builtin
  scope/restoration, malformed manifests, action isolation and support-only type
  refresh indexing; public SQL bags/authority/drop/replay are in
  `reference/aggregates` under `^TestReferenceRoutineAggregateDirect`.
- Engine renderer and `pg_depend` units: `RUN='^Test(Deparse|PGDepend|ViewCheck)'`
- Geometric captured-source and literal serialization: `RUN='^TestGeometryRelationLiteral'`
- Views, rules and deparsing: `RUN='^TestReference(Rule|Deparse|Materialized)'`
- Row security: `RUN='^TestReference(RLS|RowSecurity)'`
- Catalog projections: `RUN='^TestReference(PGDepend|PGTableViews)'`
- Stored generated columns: `RUN='^TestReferenceGeneratedStored'`
- Installed comparison capture and lifecycle: `RUN='^TestCube'`
- Installed earth captures and generation ordering: `RUN='^TestEarth(Coordinate|Point)'`
- Installed hstore conversion/comparison/record reconstruction, provenance and authorized generation remap:
  `RUN='^TestHstore'` (also with `RACE=1`). These units preserve
  original declaration/operand identity and reject foreign or corrupted
  source/current authority without changing the published graphs.
