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
Collection rejects an already-canceled context before catalog preflight or capture
allocation, retaining the ordinary context error and publishing no partial capture.
`Dump(ctx, capture, dst)` (`dump.go`) then sorts and completes the `Capture` in
place, builds the script and writes it; the root never reads the capture again.
Table declarations retain logged/unlogged identity, including typed and partition
children. Identity sequence completion states its own persistence explicitly in
both directions, preserving independent table/sequence choices on replay.
Partition constraint replay keeps one declaration/membership/name/data graph.
The existing bootstrap-owned scratch phases also create detached fresh exclusion
providers through ordinary temporary-parent DDL. Scratch columns share the normal
column-type/collation renderer; captured table defaults, identities, ownership and
layout stay with their ordinary declaration/completion authorities. Clone-name
reservation covers the whole actual creation sequence, including transient
children, before renames free stems. Stale-invalid false origins drop their
captured empty invalid-child witness before reconciling generated root names;
the private importer validates real completed lifecycle captures, not a blanket
generated-name DROP exemption. Scratch identities are never imported as captured
objects.
Scratch phase dispatch reads its explicit captured target from the existing
recipe record: the event anchor may be a real source backing index rather than
the table OID. Equivalent seeds retain one scratch-parent ownership lifetime
across intervening membership events; only the last completion detaches it.
Guard seeds protect predeclared adopted true origins while creating separate
false providers. Generated-name reservation remains with `ConstraintReplay`,
not a second serializer or a source-allocation ordering table.
Independent nonheap table/materialized declarations use a scoped
`default_table_access_method` setting around CREATE and its immediately paired
OWNER transfer, then reset to heap. Restore admits only this ordinary validated
creation setting, without extra privileges. All-heap canonical streams remain
unchanged; native Restore's isolated engine starts at heap regardless of the
destination session setting. Virtual partition defaults and compact children
receive qualified SET ACCESS METHOD only when their constructor would choose a
different method. Custom-method declarations precede consumers and replacement
remaps table methods through the existing access-method identity map. Live TEMP
consumers retain their exact custom-method provider with a certified installed
handler; a distinct same-name imported provider rejects replacement atomically.
Clustering preferences replay in a deterministic schema/table/index section after
events and index options, under each target's owner. The section includes implicit
constraint indexes and materialized-view indexes after their final names exist,
omits TEMP namespaces and emits ordinary qualified-target ALTER syntax. Restore
admits only a single unconditional qualified-target CLUSTER ON preference, uses
the common private ALTER stage and remaps the full index payload once; no
physical heap ordering or standalone CLUSTER execution is represented.

### Historical initial privileges

`pg_init_privs` is a separate historical inventory, never reconstructed from
current object ACLs or extension installation recipes. The coherent capture
validates current historical principal authority before writer I/O and omits
TEMP objects under the existing portability rules. An immutable engine-local
canonical seed map is comparison provenance, not additional live historical
records or role dependencies. If dump-visible history equals that baseline
exactly (full addresses, origin, ordered principal references/bits/options), Dump
omits metadata, preserving pristine and ordinary SQL-only canonical streams.
Pointer equality, row count alone and current object ACLs do not establish this
equality. Missing permanent baseline rows are differences, not exclusions.

Otherwise Dump emits one `stuntgres-initial-privileges-v1` base64 JSON comment
containing the complete inventory, including an empty inventory, a complete-record
marker, portable logical object identities, stable column numbers, exact `i`/`e`
origin and ordered named ACL entries with grant options. Row sorting is only for
deterministic output; ACL entries are not sorted.

Restore strictly rejects unsupported versions, unknown/missing/duplicate/null
fields, duplicate target records or grants, invalid origins, class identities,
subobjects and privilege masks. Its final hook resolves objects and principals
against the isolated replay engine after membership and current-ACL repairs.
The complete inventory replaces all replay-generated history through the normal
catalog candidate validation/publication path, including absent bootstrap rows
such as a dropped/recreated public schema. A plain SQL Restore without this
metadata retains ordinary bootstrap/extension recording. Late resolution errors
publish nothing to the destination.

Native installation replay detaches and reattaches members to reconstruct
identity, owner and current ACL state. Those reconstruction steps are not new
historical privilege events. Decoded extension-owner metadata identifies the
exact provider declaration slot or ordinary final logical member identity,
target extension and ADD/DROP action. Preparation grants statement-local replay
authority for that exact root/extension pair; the executor still performs normal
ownership, membership and reciprocal-closure checks and preserves history only
for that proven repair. Each exact root/extension/action tuple is consumed once
in Restore-local state; repeated appended membership events use ordinary history
semantics. Statement authority is cleared on execution success/error, the next
preparation and operation end. Unmatched/plain SQL ADD/DROP continues to capture
or delete history normally; an explicit complete historical record remains the
final authority after all reconstruction and provenance validation.

Direct replacement strips source TEMP history, applies the same principal
mapper as current ACLs and rekeys full historical addresses through the imported
object mapping without losing column numbers. Fresh maps reject destination
collisions before replacing historical roots; destination TEMP records retain
their exact local authority. Reset imports the newly seeded historical baseline.
The canonical comparison baseline follows the same principal/object remappers,
without requiring missing baseline targets to be live. Current-only mutations
and complete metadata replacement never rewrite that baseline.
Previously retained catalog roots and rendered rows remain immutable.


Extension-owned composite types are reconstructed by their installation.
Their standalone backing relations are therefore omitted from CREATE TYPE
output, and backing-relation dependency edges resolve to the same extension
event without inventing direct relation membership.

Statistics declarations render simple keys and parenthesized bound expressions
with current stable-attribute names. Expression-only objects omit the kind list;
mixed declarations serialize only requested d/f/m kinds, never explicit e.
Their graph follows input/provider definitions and completed sparse table layouts.
Recipe dependencies pass through the common predecessor resolver: authentic
builtins require no dump event, extension-owned members await their extension,
and user routines/types retain their actual declaration predecessors. Raw
dependency OIDs never stand in for resolved graph events.
Native semantic state replay preserves collected stale expression summaries,
separately from declaration replay and without re-ANALYZE.

Parameter ACLs use the shared typed ACLITEM[] preflight and deterministic
object-ACL section. Isolated Restore accepts only the generated single-assignment
`pg_parameter_acl.paracl` repair selected by literal TEXT `parname`, allocating
destination identities without publishing temporary grants. Sparse baseline
normalization, explicit-empty arrays, grantor order and role mapping are preserved;
setting values and source parameter OIDs are not serialized.
Parameter identity and immutable ACL authority are cluster-shared. Dump captures
committed authority; successful Reset/Restore replaces it coherently in every
attached database, while local storage replacement remains target-scoped.
Imported identities are reminted beyond destination cluster frontiers using a
private reservation, installed only on success. The pre-input Parameter
predecessor fences only that authority's incarnation, exact descriptors and
canonical name bindings, not unrelated catalog edits during input. Existing
principal remap, TEMP compatibility and provider validation keep their error
precedence before the narrow parameter fence. Replacement advances the parameter
incarnation even when both authorities are empty. Database identities,
owners/ACLs, shared roles and peer data are retained. Invalid input, cancellation
or stale authority installs no peer roots or replacement-reserved identities.
Shared parameter reservations also fence the captured peer-store set and each
peer allocation frontier after unlocked preparation; peer-local issuance cannot
silently consume an imported parameter's reserved destination identity.

Restricted builtin routine ACLs use the same object-ACL repair owner. Exact seed
authority needs no emitted SQL; raw absence is emitted as
`NULL::pg_catalog.aclitem[]`, distinct from an explicit empty array. Isolated
Restore recognizes that canonical typed literal and preserves ordinary default
ACL semantics without reapplying the destination's restricted seed. Untyped
catalog updates do not acquire this replay capability.

PL bootstrap languages/handlers retain genuine sealed installation authority.
Unchanged bootstrap definitions need no replay; changed handler metadata and
language identity/trust/routes use the existing installation replay. Global
bootstrap names are parked collision-free before final identities and aliases
are declared with destination-qualified handlers. Language owners and raw ACLs
use the existing bounded repairs. Genuine source-only PL declarations retain
their admission even when a validator was added before capture. Each such routine
is serialized between real same-language replacements omitting then restoring
the validator, through the shared `routine.LanguageDumpStatement` serializer.
Its privileged declaration runs under authenticated `DEFAULT`, restoring the
final routes before routine ownership transfer or any following consumer; the
routine owner need not own the language. Replacement preserves language identity,
owner/ACL, membership and installation provenance while rebuilding handler edges.
Restore remaps source addresses once without converting replay prerequisites
into hard edges.
The final routine replay pass recompiles checked PL programs, but never turns
source-only declarations into validated programs; malformed deferred bodies
remain deferred until reached invocation.
Checked PL declarations captured after validator removal carry a bounded native
admission record, paired strictly with their exact CREATE header and following
owner transfer. Restore recompiles their real source under that owner at the
ordinary fresh staged bind frontier, before later consumers. No record bypasses
validation or compiles genuinely source-only siblings. Before retiring missing
installed handlers, placement checks all released call/inline/validator routes.
If any destination seed route references a missing handler, it first reconciles
the surviving installed language's complete current routes at its actual/parked
name, after handler identity placement and before any missing-handler DROP.
This removes stale seed edges without cascading away genuine language origin.
Absent source languages are never recreated by this prelude. Final route/name
replay, actual handler retirement and strict installed owner/membership validation
remain unchanged; no retired handler is resurrected.
Central Restore admission accepts language lifecycle ASTs and passes them to the
ordinary authority/dependency executor; emitted identity replay is not a separate
catalog-write bypass.
Row-trigger definitions are post-data SQL groups, after their functions/tables
and saved rows, so Restore never transforms stored data twice. Partition clones
are derived through catalog publication rather than serialized independently;
definition OIDs, parent provenance and WHEN captures remap with the destination.

Unenforced foreign keys retain their constraint identity but have no internal
trigger identities. Replacement remaps the constraint OID and leaves those
absent trigger slots zero; it neither imports zero as an object identity nor
creates placeholder triggers.

Explicit bootstrap view return-rule identities are reconstructed from the
destination seed, not serialized as executable views. Replacement reserves their
captured `ReturnRuleOID` identities before remapping imported definitions, so a
colliding imported view rule moves without changing native metadata ownership.

Event-trigger collection retains validated immutable descriptors and real owner/
function authority. `prepareEventTriggers` renders the complete final section
before any writer I/O; an existing owner demoted from superuser is explicitly
unrepresentable portable state and rejects Dump without an output prefix.
Representable owners, qualified functions, ordered filters and O/D/R/A state
replay unchanged. All ordinary DDL, data, defaults, attribute metadata and
temporary raw-codec CAST cleanup precede this section. Each definition is created
and immediately disabled; final extension membership and strict owner metadata
follow, then terminal ENABLE, ENABLE REPLICA and ENABLE ALWAYS restore O/R/A
after every declaration; D remains disabled.
No eligible replay DDL follows activation. Source callback frames and private
transaction definitions are not captured; replacement remaps global object and
function references once into the destination authority.
Column policies replay as qualified `ALTER TABLE ONLY` completions. Ordinary
inheritance components retain type-default storage/default compression throughout
the constructor stream, even for source-shaped layouts that need no shape
schedule. Their live saved pairs complete under authenticated DEFAULT authority
after all constructor/retirement events and before late index repairs and event
trigger activation. Retired policies instead remain after their last constructor
consumer and before their own DROP. Other relation completions are unchanged.
For deferred components, late index repairs restore every saved attribute pair,
including defaults: final table ONLY actions can themselves change first simple
index attributes. Other indexes compare saved pairs with
`index.ConstructorColumnStorage`, including substituted key types, expression
defaults and INCLUDE copies, rather than with heap policies. Divergent duplicate
attributes and retained pre-rename labels use deterministic late `pg_attribute`
repairs under authenticated DEFAULT authority. Restore admits only
the literal storage/compression pair with optional trailing literal name, or a
single literal name, using a qualified regclass/positive attnum locator on a live
index attribute. Collision-excluded temporary labels precede final names so SQL
replay also handles name swaps. Ordinary catalog UPDATE stays read-only.
Late index repairs exclude temporary-namespace objects using the collection
eligibility rule. Qualified property completions retain ordinary relation
ownership checks while using the isolated importer's definition-lookup authority.
Retired policy-bearing attributes use paired inert `pg_catalog.text` placeholders
with ordinary storage/compression completions before retirement; other inert
placeholders use `pg_catalog.int4`. Both retain exact fresh-constructor identity,
physical-slot/inheritance checks and owner authorization, and cannot retire after
data or final ACL consumption.


Subscription collection selects only the captured logical database. Complete
definitions and owner authority are preflighted before writer I/O. Replay sorts
by final subscription name and reserves all final and slot-origin names before
allocating deterministic unused staging names. Each non-NULL slot is recreated
implicitly by disconnected CREATE under its exact spelling, then immediately
renamed to its staging name; NULL slots are created directly under staging names
with `slot_name=NONE`. After all creations, a second pass installs final names,
declared SKIP and desired ENABLE state, then ordinary owner transfer under the
bootstrap owner group. Repeated origins and cycles between creation/final names
cannot collide. This preserves uppercase, UTF8 and literal `none` slots without
explicit slot-option conversion or a restore-only bypass. Replay never reconnects,
refreshes tables or alters a remote slot. Replacement remints imported object and
database identity while retaining other databases' committed subscriptions.

- **Collect tables** (`collect*.go`): `dumpCollectors` is an ordered list of
  `{name, matches, collect}`. Temporary-namespace objects are skipped, the first
  matching entry collects a descriptor and an unmatched one fails with
  `unsupported catalog descriptor kind`; the relation entry dispatches through
  `dumpRelationCollectors` the same way. **Entry order is error precedence.**
  Feature packages own each entry's decision (`routine.OperatorDumped`,
  `stored.ProcedureProvider`, `stored.TypeProvider`).
- **Installation actors** (`installation_schedule.go`): genuine CREATE EXTENSION
  retains the captured owner. Replay restores its administrator actor immediately
  afterward, before any genuine release UPDATE; destination role attributes need
  not match the source. Final strict owner/principal import restores current
  authority and still rejects a recreated captured principal.
- **Immutable body coordinates** (`installation_schedule.go`): sealed Earth
  declaration syntax supplies only the captured cube-schema spelling. The sealed
  domain's base-type identity selects the actual prerequisite cohort. Replay
  installs it directly at its first producer's coordinates, then repositions that
  same still-canonical installation between dependent producers if necessary.
  Detachment, type naming, provider identities and finalization consume the actual
  planner coordinate. Source namespace shells are retained; replay-only historical
  shells are retired after all producers/finalization, never with CASCADE.
  Existing pg_catalog is neither created nor dropped, nor entered by SET SCHEMA.
  Raw declaration trees, owner-v3 metadata and final import remain unchanged.
  Source-absent constrained cube headers retire after in-place name parking,
  before a later cube producer reuses their canonical keys. The single existing
  final-placement owner runs queued survivor records after all producers and
  active/new-cohort placement, before schema retirement and consumers. Temporary
  parent/array and routine/class/family labels use the ordinary shared owners;
  nominal-operand operators coexist by actual input identities, without an
  invented operator RENAME. Retained pg_catalog types normalize in place.
- **Installed operators** (`installation_replay.go`): immutable installation
  declarations replay moved recipe operators, including ltree path concatenation,
  with genuine ALTER OPERATOR SET SCHEMA after their operand type identities exist,
  before final owner metadata and stored consumers. Replay follows the origin
  cohort, including detached members and surviving objects whose original
  extension was deleted, rather than mutable extension membership. Unchanged
  operators emit no identity statement; none is recreated independently of its
  installation.
- **Templates** (`template_replay.go`): standalone declarations, including ordinary
  templates added to extensions, precede their dictionaries and depend on actual
  callback predecessors. Membership does not turn an ordinary declaration into
  an installed provider. Authenticated builtin callbacks require no declaration;
  installed helpers await their installation cohort. Seed template relabels use
  the common collision planner in their own key family, before dictionary
  relabels and consumers. Genuine installed templates retain immutable origin
  authority and replay with their canonical installation cohort, independently
  of current membership. Templates have no OWNER directive. Privileged creation
  restores optional INIT as zero and re-establishes genuine callback capability
  from destination descriptors; no initialized native pointers are serialized.
- **Dictionaries** (`dictionary_replay.go`): initialized simple/English dictionaries
  use their captured installation namespaces after namespace replay and share the
  builtin dictionary collision planner. Installed dictionaries are reconstructed
  with their immutable installation cohort; neither gets standalone duplicate
  CREATE statements. Relabeling precedes option refresh and owner transfer.
  Admitted option removals replay explicitly, preserving SQL NULL rather than
  equivalent default resource behavior; genuine unaccent dictionaries require
  explicit Rules and cannot replay its removal. English seed differences remove
  default options before replaying the exact current ordered list, so removed
  StopWords cannot reappear on Restore. Standalone dictionaries retain
  dependency-ordered declaration events after template installation.
  Ordinary Snowball dictionaries keep CREATE events even as extension members.
  Allocated Snowball helpers/template and English dictionary/configuration instead
  replay their canonical bootstrap identities with ordinary ALTER planners.
  Current owners/ACLs and each independent e-link follow identity reconstruction;
  intrinsic seeds never acquire installed-origin declaration slots.
  Non-bootstrap owners use the shared `dump.Statement.PrivilegedCreation`
  protocol: CREATE runs under authenticated `DEFAULT`, and `FollowingSQL`
  transfers to the actual owner without assuming schema CREATE grants or a named
  administrator. Restore executes the emitted SQL, recompiles real resources,
  and remaps dictionary/template links and stored regdictionary references,
  defaults and views.
  Thesaurus normalizers add replay-only predecessors, not SQL dependencies.
  The committed host-authorized snapshot resolves current logical names and emits
  qualified Dictionary values without changing stored option spelling. Unqualified
  options carry only their accepted lookup namespace as replay provenance; import
  remaps it, and execution always performs independent current-caller lookup.
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
  Deterministic strict `stuntgres-ndistinct-v1` comments transport checked
  extended facts, including stale joint counts, dependencies, joint MCV and
  automatic typed expression summaries, with separate inherited flavors.
  Optional MCV construction records retain population and marginal counts,
  true NULL flags, compact non-NULL typed dictionaries and canonical indexes;
  records without built MCV preserve their previous byte format. Restore checks
  shape, uniqueness and counts, then resolves current recipes/usages and remints
  dictionary values before rebuilding native bytes with destination type, enum
  member and nested array-element identities. It never re-ANALYZEs live rows.
  The typed replay traversal has one statistics-only structural-tree capability;
  ordinary row/domain replay and SQL input cannot use it. Retained tree text is
  backend-local structural output, not PostgreSQL node syntax.
  Genuine destination TEMP statistics retain their separate existing authority:
  preflight certifies dependencies and advances retained references into the new
  epoch. Unchanged usages preserve immutable construction backing; changed usages
  use checked typed reconstruction, never generic Retag adoption. All failures
  precede atomic destination publication, preserving held old captures.
  PostgreSQL18 script replay ignores these comments and leaves collected state
  absent.
- **Parsers** (`ts_parser.go`) serialize fully qualified actual callback names,
  omit zero HEADLINE and emit no owner/ACL clauses. Unmodified default is seeded;
  modified default emits rename/schema ALTERs from its bootstrap identity.
  Parser events precede dependent configurations and follow represented routine
  declarations. Replacement remaps real parser/callback/configuration identities
  through the existing private Restore candidate.
  Current membership never suppresses ordinary parser CREATE or substitutes an
  extension event for its callback/parser/configuration dependency chain. Final
  symbolic ADD follows ordinary creators. Strict v3 kind `parser` carries only
  qualified identity/current membership, with no signature, declaration, provider
  version, owner or ACL; import requires the final reciprocal parent graph.
  Seed3722 always retains its ALTER replay and never becomes a duplicate CREATE.
- **Configurations** (`ts_configuration.go`) serialize actual PARSER and ordered
  ADD MAPPING statements, never COPY provenance. Actual parser, namespace, mapped
  dictionary and stored alias/default/view dependencies use the ordinary graph.
  Owner transfers use privileged creation; allocated English and fixed simple
  seed identities share the ordinary collision planner before mapping replay.
  Seed edits replay current mappings after canonical initialization. Temporary
  retention and replacement remap configurations and REGCONFIG datums through
  existing owners.
  Current extension membership never suppresses that ordinary creator or its
  parser/mapping dependency event. The sole v3 codec carries the configuration's
  final namespace/name and membership, not a second owner or nonexistent ACL;
  final ALTER EXTENSION membership follows its actual creation and owner transfer.
- **Provider differences** (`provider_replay.go`): immutable released builtin
  identity, never mutable labels, selects real ALTER FUNCTION replay. Original
  SQL declaration names are distinct from executable C symbols.
  Canonical parallel/cost/rows patches follow final identities and precede
  owner transfers; builtin costs compare against immutable released defaults.
  Generated range/multirange constructor patches use the same replay planner,
  but emit immediately after their actual parent type declaration and identity
  epilogue, before type ACL repairs and dependent events. Their identities are
  validated type-owned capabilities, not builtin OIDs. The type epilogue owns
  current constructor identity and owner replay; raw ACL repairs preserve
  current authority. Seeded PL handlers use their real declarations.
  Ordinary type declaration events also follow their genuine I/O/support
  procedure predecessors through the shared metadata dependency visitor.
  Codec identities come from the captured catalog, not render-only generations;
  installed producers remain canonical cohort events, not duplicate function DDL.
- **Installed state** (`installation_replay.go`, `provider_replay.go`): immutable
  script-sealed declaration recipes, not mutable membership positions, identify
  providers. Nonseed cohorts are recreated by real CREATE EXTENSION, detached using
  real ALTER EXTENSION, and relabeled through existing SQL class owners before
  any ordinary consumer. Restore admits these emitted membership, placement,
  update and retirement statements to the ordinary executor, not an import-only
  catalog mutation. The script-sealed prerequisite cohort/type graph keeps
  canonical prerequisite identities available while dependent scripts install;
  dependents finalize before prerequisite rename/removal. A required provider
  with no surviving source cohort is installed genuinely and retired afterward.
  CREATE versus CREATE+UPDATE preserves surviving release/declaration origins.
  Source-absent declarations are retired, and absent origin extensions disappear
  without deleting detached survivors. Same-name cohorts replay serially.
  Isolated scratch schemas and collision-planned type/routine/template/dictionary/
  class/family identity changes avoid occupied intermediate names. Builtin provider differences
  likewise use immutable released identities, not mutable labels. Free
  destinations move first; occupied cycles or blocked intermediate keys use
  collision-checked temporary names. A reserved real uuid-ossp installation can
  retire unwanted global languages through ordinary extension dependencies;
  templates instead use their actual standalone ALTER/DROP owners.
  Cleanup namespaces use the shared collision-reserving scratch-name allocator,
  never a source namespace suffix that could create a reserved `pg_` name.
  Installed templates detach at canonical coordinates, replay final rename/move
  in their own collision family alongside the initialized template, and acquire
  only their captured current membership after all ordinary creators finish.
  Missing templates of a retained cohort use ordinary DROP TEXT SEARCH TEMPLATE;
  absent-origin residual declarations retire with their genuine extension.
  Surviving lookup-only aggregate helpers have fixed creation coordinates and no
  independent SQL mover. Their cohort installs in the actual captured helper
  namespace, reusing (never creating or retiring) that source schema. Parent
  aggregate names/schemas still replay independently through ordinary ALTER;
  no helper follows a parent-only move. Cohorts with no surviving helper carrier
  retain normal temporary installation and retirement.
  A genuine source installation with surviving objects in `pg_catalog` likewise
  installs directly in that existing namespace; replay never creates/retires
  the system schema or widens public namespace-mover admission. Initialized
  dictionary identity replay joins the installed dictionary collision group
  after actual installation, preserving the real `simple`/`unaccent` cycle.
  Surviving origin procedures carry their current Parallel/Cost/Rows through
  installation replay even though they bypass ordinary provider collection.
  Generated constructor estimate replay is admitted by the existing pre-render
  creator graph event, then emitted after actual creation and final identity/
  current-owner normalization, before ACL repairs or dependent consumers.
  Final symbolic membership waits for ordinary objects, then replays recursive
  roots before reconciling each companion's independent detached/foreign
  membership. Seeded plpgsql reconciles handlers/language and complete source
  absence without duplicate installation or seed resurrection. Only its exact
  unchanged membership, declaration identities, current planner metadata and
  owner/ACL baseline are omitted; the final membership planner starts from
  those preserved seed links. Handler Parallel/Cost/Rows overrides are compared
  with the immutable released defaults and take genuine installation replay,
  just like detached, foreign, renamed, missing or authority-modified state.
  The owner baseline is the captured live bootstrap principal and absent raw
  ACL, never a historical recipe owner's role-domain pointer. Principal remapping
  therefore preserves unchanged-seed omission without rewriting immutable recipes.
  Installed type/domain OWNER replay admits the exact current qualified identity,
  immutable declaration/version and target owner from v3 metadata, including a
  detached survivor with no live origin extension; it has no provider-name
  exception or membership-slot authority. A live origin's header must also match
  its current presence, release, installation schema and owner; an absent origin
  carries no invented schema/owner header. Ordinary owner execution still runs.
  At the first installed type/domain OWNER use, the shared importer validates
  installed symbolic identities, headers, declaration coverage and owner/ACL
  data without mutation; not-yet-created ordinary entries and final membership
  are deferred. Complete ImportOwners remains mandatory after all statements.
  Malformed v3 authority returns an ordinary codec error before generated SQL
  can mask it with an owner-admission failure; unsupported raw SQL still has its
  normal SQLSTATE boundary.
  The sole owner codec is v3. Named owners/grants and immutable release/declaration
  keys survive independent-driver remap; v2 and malformed authority reject
  atomically. No sessions, resources, loaders or source callback handles serialize.
  Generated-array labels are reconstructed, not inferred from idealized final
  element names. `array_name_trace.go` solves finite exact/prefix constraints
  with UTF-8 truncation guards against the type owner's shared candidate rule.
  `array_identity_replay.go` realizes the trace through real parent operations
  and temporary domain displacement, tracking and retiring each helper's own
  array in the same `DomainReplayNames` model. Numeric, repeated-prefix and
  removed-history collisions do not require protected-array ALTER or import
  mutation. Installation rendering and every ordinary represented row/type
  creator share this model before dependent declarations consume its names.
  Released composite backing relations use their row type's immutable installed
  origin to select that installation creator and its already-modeled root/array.
  Detaching or attaching the type to another extension never adds an ordinary
  composite CREATE event or changes its dependency barrier. Conversely, an
  ordinary composite added to an extension retains its real CREATE event and
  consumer ordering; mutable membership is not a declaration producer.
  Ordinary normalization belongs to the existing creator event: after real
  CREATE and final parent identity, before consumers. Relation-only epilogues
  never replace table/view CREATE entries. Protected rowtypes use actual
  TABLE/VIEW/MATERIALIZED VIEW/FOREIGN TABLE owners. The existing writer switches
  to bootstrap only for normalization and restores the previous emitting role.
  Nonbootstrap domain/range owners use the existing exact fresh-owner pair:
  bootstrap CREATE is immediately followed by OWNER at the actual creation
  coordinates, without an intervening directive or normalization statement.
  Private range coordinates are not mistaken for their final names. The
  bootstrap emitting actor then performs normalization through ordinary owners;
  transferring the object first grants no extra schema CREATE/USAGE privilege.
  Range families are created at explicit, distinct private coordinates while
  subtype, diff, class and collation references retain their captured identities.
  The private schema is emitted in the actual range declaration's prelude,
  before its captured-owner directive, not in the global installation prefix.
  Earlier source-bound relation allocations therefore retain their numeric
  endpoint constraints, while the CREATE TYPE/immediate OWNER pair stays intact.
  This avoids constructor collisions with prerequisite routines or later type
  creators. Range and multirange parents/arrays then move separately; generated
  constructor public labels, schemas and owners are restored through real ALTER
  FUNCTION, not coupled to parent generation refresh. Mutable constructor owner
  and routine ACL authority does not change the separate multirange parent
  policy: that parent's ACL remains the synthetic type-default baseline from
  genuine range creation. Replay units assert quoted SQL identities and the
  dependency-safe order of cohort retirement, method cycles and independent
  companion membership. The private schema retires after every type and
  constructor has left. No second replay executor or
  counterfeit executable generation is involved.
  Raw-float4 CAST ordering never makes prefix installation barriers depend on
  an ordinary CAST: that would invert their actual emission phases and create
  an artificial cycle through the CAST's own installation predecessor.
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
  Generic-file-list routine ACLs likewise omit only exact bootstrap-owned
  callable identities and ordered explicit owner/pg_monitor seed entries
  (owner-only for both pg_ls_dir overloads). NULL, empty, default-equivalent,
  reordered, delegated and grant-option edits remain raw repairs.
  Explicit-empty repairs use the existing lossless
  `pg_catalog.acldefault('c',0)` constructor, with qualified argument types, to
  preserve PostgreSQL's one-dimensional empty ACL representation. Isolated
  admission recognizes only that fixed constant shape, never evaluates arbitrary
  functions, and does not accept zero-dimensional `'{}'` repairs.
  ACL capture shares declaration collection's temporary-namespace exclusion:
  omitted temporary objects never leave dangling raw-ACL repairs in the stream.
  Installed routine/type ACLs are available after reconstruction and before
  consumers; final repairs and strict v3 import retain exact raw authority.
  `object_acl_repair.go` admits only fixed generated shapes; ordinary public
  UPDATE gains no catalog write capability.
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
  When FK snapshot records exist, the writer incrementally hashes successfully
  emitted ordinary bytes using bounded scratch. After every section and final
  comparison-context RESET it emits the allocation-independent native FK footer,
  excluded from that seal. Restore checks the whole SQL stream excluding only
  those complete record lines; appended SQL executes normally but cannot retain
  a stale snapshot waiver. Short writes, errors and cancellation retain their
  ordinary writer boundaries; the seal is not an authentication mechanism.
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
  Immutable installed origins resolve to the early reconstruction barrier even
  when detached or foreign-attached; ordinary added members retain their own
  declaration events and dependencies. Internally owned family rows collapse to
  their actual class/family owner. TEMP installation or membership is rejected
  before writer I/O; otherwise omitted TEMP descriptors never become replay state.
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
  refers to a renamed or moved scalar.
  Absent declarations left attached to a retiring origin are removed by its real
  DROP EXTENSION, without replaying their identities or requiring their generated
  argument types in the source. Explicit standalone deletion still renders its
  actual identity; surviving declarations retain full current provider validation.
  Deterministic free temporary names break cross-member cycles; moves preserve
  companion spelling and renames use the shared generated-array predictor.
  This predictor seeds mixed domain/composite
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
  → close, repeated, then finish: bind needs a genuine released cube, hstore or ltree
  comparison operator in the named persistent namespace, authenticated by its
  current class/origin/principal and validated backing declaration. The backing
  procedure may live elsewhere; a comparison procedure alone in the namespace
  cannot authorize a window. Neither object's current extension membership
  selects its immutable implementation authority.
  Any unmarked `search_path` change, a non-DDL or second DDL in a window, or a
  statement after finish is invalid. A marked statement returns `execute`, and
  the root runs it with no other entry. Dump derives the namespace from current
  authenticated typed captures and orders each bound definition after its actual
  selected procedures and namespace. Detached, foreign-member and absent-origin
  comparisons do not acquire a phantom dependency on the old extension.
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
   Each FK's four RI trigger identities participate independently, even when
   its constraint OID does not collide. FK SQL reconstructs those identities;
   RI triggers are not separately serialized or executed as user triggers.
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

Text-search parser graph, declaration and canceled/failed capture ownership
uses the shared `RUN='^TestTSParser'` family.

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
