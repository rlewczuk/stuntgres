# internal/engine/extension

The installed-extension layer that does not need the engine: provider records
and control data for bundled extensions (amcheck, autoinc, citext, cube,
earthdistance, fuzzystrmatch, hstore, insert_username, intagg, intarray, isn, lo, ltree,
moddatetime, pageinspect, pgstattuple, pgcrypto, pgrowlocks, refint, seg, tablefunc, tcn,
unaccent, uuid-ossp and seeded plpgsql),
member validation, membership, the owner metadata codec used by native Dump,
extension remap, provider staging, and CREATE/UPDATE/DROP plus the lower
ownership/membership/relocation and Restore owner-import decisions.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access` (privilege requirements and the captured view),
`authorization`, `catalog`, `ddl` (the shared authority and allocator), `expr`,
`functions` (kernels and member tables), `ids`, `parser`, `proc`, `sqlstate`, `sqltype`,
`typedef` (the relation type name check of installed-scalar staging)
and the standard library; never the engine, `analyze`, `routine` or `plan`.

## What it owns

- **Providers** (`*_provider.go`): one provider type per extension, each with
  `Control`, `Versions`, `ValidateExtension` (`catalog.ExtensionImplementation`)
  and, for providers with callable members, `ValidateProcedure`. The stock instances are
  `StockCitext` … `StockUUIDOssp`. The script contexts (`CitextTypeContext`,
  `CubeTypeContext`, `IntarrayInputContext`, `UnaccentTemplateContext`, …),
  signatures (`CubeSignature`, `CubeBinarySignature`, `QueryIntSignature`) and
  member tables (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
  PL/pgSQL staging uses catalog's `PLHandler.ReleasedProcedure`, the same released
  declaration producer as bootstrap; neither path infers C library provenance
  or parallel safety from current membership. The sealed recipe retains those
  declaration facts while current owner/ACL, cost and parallel overrides remain
  independent and survive native replay.
  Initial privileges are separate historical catalog records, not recipe ACLs.
  Providers call `Stage.RecordInitialPrivilege` only for genuine script ACL
  events: amcheck's eight implemented final REVOKE declarations capture the
  creator's global/schema routine defaults after selectively removing PUBLIC
  EXECUTE. Named grantees and grant options survive; owner-only EXECUTE is the
  untouched-default case. Default-NULL providers acquire no invented records.
  An UPDATE without a privilege event, including a same-version request, does
  not refresh history.
  External ADD captures raw nonempty object and live-column ACLs through the
  reciprocal membership closure; DROP detaches and removes all those snapshots.
  Ordinary GRANT/REVOKE and ALTER OWNER leave historical ACLs alone.
  Extension publication transfers historical deltas and predecessor observations
  alongside descriptors into the statement journal; validation alone does not
  publish the provider's private snapshots.
  After transaction-private ACL changes, journal authority fences retain the
  committed role predecessor; statement admission still uses private authority.
  Isolated native owners-v3 membership repair retains history only for its
  statement-local exact root/extension references and ADD/DROP action. It still
  executes ownership, closure and membership checks; ordinary SQL and unmatched
  replay flags use normal snapshot capture/removal.
  The insert_username provider owns one zero-input volatile, non-strict C
  trigger declaration. Its installed capability classifies dispatch only;
  current sealed provenance and principal validation still grant authority.
  Definer/configuration clauses are supported because its reached native body
  enters the shared routine environment. Native C loading, SPI ABI/shared-library
  files and PostgreSQL backend processes are external mechanisms, not alternate
  execution paths for this embedded stock body.
- **Accessors** (`extension_implementation.go`): `CitextProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Routine identity**: `ProcedureSymbol(view, procedure)` checks the current
  descriptor/address/index, typed capability and immutable installed
  release/declaration origin. Mutable names, schema, owner and SQL membership
  never select executable symbols. `Installation.Members` and aligned canonical
  `Declarations` form one sealed logical script recipe; `Extension.Members` and
  reciprocal descriptor links independently describe current SQL membership.
  Ordinary added objects gain no installed origin. Detached and foreign-attached
  providers remain executable after origin-extension removal, subject to genuine
  helper/type dependencies. Fuzzystrmatch UPDATE retains incumbent original
  declarations and adds the genuinely scripted release declaration. Slots are
  local to each release recipe: the 1.2 inventory prepends its new function,
  while surviving 1.1 objects retain their original sealed recipe and slot.
  Stored routine carriers compare that immutable release/declaration authority,
  not current extension links. Replay may bind a consumer while its provider is
  detached and complete the current membership afterward without staling the
  consumer. Independently remapped sealed recipes compare by mapped cohort,
  provider, implementation, release and slot; copied/unsealed recipes and changed
  releases or declaration slots are not equivalent.
  `ProcedureInstallationName` applies the same origin checks to the released SQL
  name used by replay; executable C symbols are not SQL call names.
  Script sealing and generated-membership admission revise already staged
  creations through the extension host's identity-preserving candidate update;
  they do not replace nonexistent base predecessors. The original CREATE
  name/absence observations remain publication fences. Base-object revisions
  still use ordinary replacement, and all candidates retain full provider,
  reciprocal membership and current installed-declaration validation.
  Initial CREATE copies the control comment into the immutable extension header;
  skipped CREATE and UPDATE never reapply that default. Header copies preserve
  current comment text across placement, membership and ownership changes.
  The pgrowlocks, pageinspect and amcheck manifest builders also own immutable fixed OUT
  type IDs and complete formal names, including their input names. Their
  comparable installed declaration carries the shared immutable payload through
  relabeling, owner import and address remapping; identity validation rejects a
  foreign payload even when its numeric formals happen to match.
  Intagg's enumerator likewise seals its installed set capability and default
  estimate separately from mutable canonical routine metadata. Public ROWS
  patches do not rewrite the installed declaration or its implementation identity.
  Routine display owners also retain explicit library provenance from shipped
  control/SQL declarations, independent of mutable member names and namespaces.
  SQL-standard citext and earthdistance declarations retain genuine immutable
  parsed body recipes without granting SQL execution or checked-program authority.
  Earthdistance resolves the installed cube schema substitution once at staging;
  catalog capture never parses or evaluates an installed routine.
  Replay reads that captured spelling through the private concrete syntax owner,
  binding it to the sealed domain's actual cube type/cohort. It runs the genuine
  Earth producer while that prerequisite is at the captured coordinates; current
  cube placement never rewrites an old declaration tree. Earth has only the
  represented 1.2 installation boundary, so no synthetic UPDATE is introduced.
  The spelling is not lookup/execution authority and adds no owner-codec field.
  Released Earth body dependencies use the shared read-aware definition visitor:
  sec_to_gc/gc_to_sec, ll_to_earth and latitude retain their real radius edge;
  ll_to_earth retains both genuine cube constructor overloads, and
  latitude/longitude retain cube_ll_coord. The sealed Earth domain's base type
  selects the genuine cube origin and declaration slots, never captured spelling
  or current extension membership. Repeated written calls yield one direct edge.
  Current callee identity and principal validation still gate callable authority.
  UUID-ossp staging likewise preserves the released parallel-safe declaration
  and the `namespace`, `name` input labels of v3/v5. These labels belong to the
  sealed declaration, survive remapping and current membership changes, and
  resolve named calls through the ordinary formal-argument binder.
  Stock tcn owns one volatile, non-strict trigger declaration and exposes native
  DML-trigger capability only for its immutable installed slot. Capability does
  not replace procedure-origin validation; rename, relocation and replay retain
  the same authority independently of public spelling and current membership.
- **Autoinc trigger provider** (`autoinc_provider.go`): the sealed installed slot
  exposes catalog's shared `SupportsDMLTrigger` native capability, alongside
  tcn and refint; ordinary provider validation authenticates the current declaration.
  Autoinc 1.0 owns one nonstrict, volatile, parallel-unsafe C
  declaration returning TRIGGER; names, current membership and library display
  alone never grant its capability. Ordinary EXECUTE checks remain at trigger
  creation, not each firing.
- **Moddatetime trigger provider** (`moddatetime_provider.go`):
  Moddatetime seals one volatile, non-strict, parallel-unsafe zero-input C trigger
  declaration. Its optional native DML capability identifies the released body;
  both firing paths still validate the current descriptor and provider origin.
  Relabeling, relocation and membership changes do not select a different body.
  Non-SQL routine configuration and SECURITY DEFINER remain unadmitted.
- **Catalog validation** (`extension_catalog.go`): `CatalogProvider` validates
  the extension, text search template and dictionary classes and installed
  member procedures; `ValidateProviderHeader` is the identity check it shares
  with `stored.ProcedureProvider`; a descriptor's owning extension is
  `catalog.ExtensionMembership`. `extension_type_validation.go` validates an
  installed codec's array through the type provider registered in the catalog
  owner. Current installation validation checks both directions of membership,
  including descriptors omitted from a supplied member bag, without requiring
  current membership to equal the released recipe. Missing class providers in a
  populated engine registry reject; registry-free identity units retain their
  explicit typed validation path.
  Extension-class admission requires the exact header resident in the supplied
  view and a principal from that view's owner. Private install, transaction and
  remap views may contain unpublished headers; this is not a committed-store
  pointer fence. `ValidateExtension` checks the candidate's sealed installation
  and reciprocal current graph without granting that candidate live-header
  authority.
  Installed tablefunc procedures enter the same class validator and retain the
  full released recipe, composite/array reciprocity, explicit parallel-unsafe
  declarations and current owner checks; a recognized implementation alone never
  bypasses origin validation. SQL replay reconstructs the three declared
  composite identities and their generated arrays together. Their six immutable
  type roles remain independent of mutable current extension membership; array
  role suffixes are not independently declared SQL identifiers.
  Installed operator admission retains its released restriction/join estimator
  identities and current owner authority. Object-local membership validation
  checks a nonzero parent link; parent installation validation owns the reverse
  member bag, so a one-sided detach rejects there. Coherent detach or foreign
  attachment preserves the sealed operator declaration and backer authority.
  Text-search templates validate their captured callbacks through the registered
  procedure provider, including exact intrinsic builtin declaration identity.
  Ordinary templates remain ordinary creators even when extension members.
  Installed unaccent templates authenticate the sealed released template role
  and callback declarations from the same genuine cohort; detached, foreign and
  absent-origin membership states do not change their execution capability.
  Compilation and publication-current checks share this authority validation.
- **Seg members** (`seg_provider.go`, `seg_stage.go`): the trusted relocatable
  1.4 provider owns the twelve-byte scalar and strict immutable C input/output
  roles. Its reciprocal array is internally dependent and a current direct
  extension member, without an invented scalar recipe slot. Mutable labels and
  membership never grant codec execution.
- **Ltree members** (`ltree_provider.go`, `ltree_stage.go`, `ltree_arrays.go`):
  the immutable 1.3 recipe preserves lquery, ltree and ltxtquery type/input/output
  triples at slots 0–8. Sixteen strict immutable C array routines at 9–24 and
  their operators at 25–40 are staged and validated atomically. Staging resolves
  signatures against the privately constructed scalar/companion descriptors;
  the stage's read view still describes the pre-install catalog. The twelve
  predicates have reciprocal commutators and `matchingsel`/`matchingjoinsel`
  metadata, as set by the upstream 1.1-to-1.2 upgrade and retained in 1.3. The four
  extractors return the first ancestor, descendant, lquery or ltxtquery match as
  installation-owned ltree or NULL. Reciprocal companions are internally
  dependent current direct members, separate from immutable recipe slots.
  Ltree/lquery companions are concrete; ltxtquery's stays lookup-only.
  Routine-name discovery is only a selector hint: `ProcedureSymbol` and the
  sealed origin's declaration slot select an operation. Scalar/array signatures
  resolve the same sealed cohort and authenticate actual current type generations,
  registered providers and principals. Current reciprocal e-membership is checked
  separately: detach, foreign uuid-ossp attachment and retirement of the original
  extension do not disable surviving routines/operators or grant a codec.
  Nine comparison/support routines at 41–49 and six standard operators at 50–55
  follow the array tail, with default B-tree and hash classes/families at 56–59.
  Two scalar hierarchy routines at 60–61 and reciprocal `@>`/`<@` operators at
  62–63 append after those classes, returning strict BOOL with inclusive equality
  and genuine `matchingsel`/`matchingjoinsel` identities. Hierarchy selection uses
  the sealed declaration slot and released symbol, validated against the current
  registered provider, not the receiving extension's header or member order.
  Nineteen path procedures at 64–82 and three typed concatenation operators at
  83–85 preserve the explicitly bounded hierarchy tail. One declaration table
  supplies exact overloads, C symbols, symbolic ownership signatures and operator
  backers. Path and scalar-query selection authenticate the sealed origin's
  declaration and released symbol, never current extension headers or member
  positions. Scalar LCA has separate arities two through eight; array LCA uses the
  genuine current companion of the origin's nominal ltree role. Builtin
  integer/text formals retain their stock identities; text concatenation adds no
  implicit cast.
  Scalar lquery backers occupy slots 86–87 and reciprocal `~` operators occupy
  88–89, preserving the ninety-declaration prefix. These strict immutable C BOOL
  roles retain `contsel`/`contjoinsel`, with no negator, hash or merge support.
  Mixed scalar `ltxtq_exec` and `ltxtq_rexec` append at 90–91, with reciprocal
  `@` operators at 92–93; their symbolic signatures are `ltree,ltxtquery` and
  `ltxtquery,ltree`. They use the genuine `matchingsel`/`matchingjoinsel` identities.
  Current mixed-role admission resolves both actual types from the sealed cohort
  and validates their registered provider and principal, rather than consulting
  mutable extension membership or a surviving installation header.
  Private staging resolves its own unpublished descriptors without requiring a
  sealed installation. Complete recipe validation follows sealing, keeping all
  94 declarations atomic and preserving earlier roles and both installed scalar
  descriptor authorities; three generated companions yield 97 initial direct
  members. Current reciprocal membership validation is separate and imposes no
  original placement or member-count requirement. Symbolic owner import does not
  impose a current member count. Required strategy/support members have exact
  INTERNAL class ownership; optional hash support2 has AUTO family/routine
  dependencies.
  Operator bindings retain their released backer, operands, commutators and
  selectivity identities independently of current names and membership. The sole
  owner-v3 codec/shared remap preserve the full recipe and three generated
  companions, retaining current names, owners and ACLs independently of origin.
  Scalar storage metadata uses extended storage. This bounded recipe is not an
  inventory of every object in the upstream extension.
- **Template definitions** (`template_*.go`): ownerless schema-bound identities
  hold an optional INIT and mandatory LEXIZE procedure link. Utility lookup
  validates every supplied exact input signature and INTERNAL result before
  publishing. Rename and schema changes preserve identity and callback links;
  genuine unaccent slot-four placement may change independently of siblings
  and the installation namespace, without weakening reciprocal manifest checks.
  Explicit member DROP remains forbidden under both RESTRICT and CASCADE;
  only retirement through the owning extension consumes that membership.
  Fixed pinned templates retain their independent DROP veto; its object
  description uses the current dependency search path, not input qualification.
  Definition validity is separate from native dictionary execution. Compilation
  admits only the genuine stock-simple/synonym/thesaurus/ispell pairs, authenticated bootstrap
  Snowball helpers, or a currently authenticated unaccent installation's helper pair.
  Intrinsic callback/template/dictionary capability does not require empty or
  matching current extension membership or a live installation header. Each
  current member edge still requires its own reciprocal parent validation.
  Standalone templates use the same actual helper authority. Missing INIT never
  fabricates initialized state, and unavailable native callback combinations fail
  before publication.
  Detached compilation captures the catalog and rechecks both real helpers at
  completion. No callback or resource I/O runs under the catalog mutex.
  Callback links remain executable dependencies even when SQL `pg_depend`
  omits references to pinned builtin procedures. Installed unaccent helpers
  retain visible NORMAL dependency edges.
- **Dictionary definitions** (`dictionary_*.go`): ordinary schema-CREATE and
  owner authority, independent of extension installation privileges. Preparation
  captures template/options and immutable startup resources; actual ispell
  parsing/condition compilation runs outside the catalog mutex. Completion
  rechecks current namespace/template/revision and role authority.
  SET SCHEMA checks destination CREATE before source ownership, both during
  preparation and at current-authority completion.
  Ordered immutable options and genuine simple/synonym/unaccent/Snowball/thesaurus/ispell runtime state publish in
  the existing catalog journal. ALTER removes matching options before appending
  supplied replacements. Standalone dictionaries depend on their real namespace
  and template, without fabricated extension membership. `DictionaryDumpStatement`
  serializes qualified template/options and owner metadata, never runtime pointers.
  Snowball CREATE validates each Language/StopWords option as encountered, then
  requires Language; ALTER uses the same ordered remove-then-append merge.
  Admission checks the registered immutable language/configuration and genuine
  bootstrap template/helper capability, never mutable names or output equality.
  Synonym CREATE scans recognized options in order (last wins), then loads only
  the final Synonyms resource. Ordered CREATE duplicates remain serializable;
  ALTER removes all matching old options before appending replacements. Provider
  admission checks the immutable resource/case configuration and authenticated
  helper pair, not a mutable template name or an output-only comparison.
  Snowball helper, template and dictionary current membership is independent:
  distinct receivers and detachment preserve intrinsic capability, while class
  admission still requires each nonzero current e-link's reciprocal parent.
  Installed dictionaries retain genuine provider origin and template/helper links
  across independent membership, name and schema changes. V3 import checks final
  symbolic labels against the actual reconstructed immutable declaration.
  Thesaurus preparation resolves decoded logical Dictionary options using current
  caller namespace authority, captures the selected scalar normalizer, compiles
  outside the mutex and re-resolves/rechecks on completion. The normalizer is a
  compiler input, never a permanent OID binding or an invented SQL dependency.
  Logical normalizer lookup uses the existing semantic utility source policy,
  including during Restore: privileged destination creation never grants USAGE
  on an existing source namespace.
  Capture preserves an accepted DictFile acquisition point separately from later
  option/source errors. Detached resource reading includes complete syntax
  validation before those deferred diagnostics. The immutable parsed rules are
  then normalized once using the accepted scalar input; no second parse or file
  I/O occurs under Mu.
  Host-authorized Dump qualifies its current same-name logical source; unqualified
  options use the accepted initialization namespace solely as replay provenance.
  That namespace remaps independently on import and never redirects SQL lookup.
  Builtin ispell validates its distinct fixed helper identities without implying
  an extension or a builtin ispell dictionary. Its pinned template relationship
  remains in dicttemplate without fabricated physical pg_depend rows.
  The one-argument unaccent function independently resolves literal `unaccent` in
  its current function schema at execution; the original member slot is provider
  provenance, not a permanent default-dictionary binding.
- **Cube members** (`cube_provider.go`, `cube_coordinates.go`, `cube_spatial.go`,
  `cube_spatial_call.go`): one atomic installation validates its 48 typed recipe declarations plus genuine generated companion membership.
  The comparison graph keeps its 18-member prefix; constructor/inquiry/coordinate
  and four transformation procedures and two coordinate operators precede the
  spatial procedure/operator tails. Starts and `CubeMemberCount` derive from the
  routine tables; each table
  retains its feature authority. Operators carry real extension membership and
  explicit backing-procedure indices independent of appended routine tails.
  Staging, validation and ownership import share these links. Symbolic owner signatures preserve every builtin
  formal, including `cube,1007` subset and `cube,23` coordinate signatures.
  Import resolves the expected
  destination member, not an arbitrary same-name object.
  Comparison support keeps its exact INTERNAL class address; a family owner is
  not an interchangeable execution capability. `RemapDescriptor` delegates class,
  family and member relocation to the typed catalog remappers, preserving the
  actual owner kind and immutable source descriptors.
- **hstore** (`hstore_provider.go`, `hstore_stage.go`): trusted, relocatable 1.8
  installation of the scalar, strict immutable parallel-safe C-language codecs,
  `hstore_to_array(hstore)` and `hstore_to_matrix(hstore)` with prefix
  `%%`/`%#` operators, and `hstore_to_json(hstore)` /
  `hstore_to_json_loose(hstore)` returning JSON, plus `hstore_to_jsonb(hstore)` /
  `hstore_to_jsonb_loose(hstore)` returning JSONB. The strict JSONB function backs
  the explicit-only extension-owned cast. The reciprocal associated array is
  internally dependent and lookup-only. Appended comparison/hash roles install
  nine strict immutable routines, six real operators (`=`, `<>`, `#<#`, `#<=#`,
  `#>#`, `#>=#`) and default B-tree/hash families and classes. B-tree support
  retains its comparator; hash support retains distinct unseeded and seeded
  routines in slots one and two. Every family member has its exact INTERNAL
  class owner. Immutable declaration slots govern provenance and remapping;
  current membership independently includes the associated array and the
  ownerless, namespaceless cast. The v3 record preserves both without inventing
  cast ownership. Symbolic signatures preserve each builtin or hstore formal,
  never a source OID. Array, JSON and JSONB roles retain distinct result identities.
  Text-pair and array constructors, scalar/array lookups and presence/defined
  predicates append seven procedures and two binary `->` operators without
  renumbering incumbent roles. Key-array existence, pair containment and key/value
  projections add six procedures and five predicates (`?`, `?&`, `?|`, `@>`, `<@`).
  Their recipes own released `matchingsel`/`matchingjoinsel` identities and reciprocal containment
  commutators. Update roles append five strict immutable procedures and four
  operators for concat, scalar/array/pair deletion and slice. Recipe result zero
  resolves to the installation-local hstore in both staging and validation.
  Authenticated recipe operators may change schema independently; manifest,
  signature, result, backer and current header checks retain identity authority.
  The sealed manifest has sixty-six declarations, independent of current
  membership; its sixty-five owner-bearing roles retain their authority.
  Nonstrict `hstore(record)` and `populate_record(anyelement,hstore)` retain
  their released pseudo signatures; binary `#=` has ANYELEMENT left/result and
  the exact replacement backer. Symbolic ownership never substitutes a selected
  named tuple for these declarations. The two-argument scalar/array constructors
  are also nonstrict; exact recipes authenticate signatures, results, symbols
  and strictness. Remapping covers builtin operands, unary `0` positions,
  polymorphic declarations and installed constructor results.
  Appended `skeys`, `svals` and `each` declarations own immutable set capability,
  default row estimate and parallel safety. `each` additionally owns the named
  `hs` input and fixed TEXT `key`/`value` outputs. The same finite declaration
  recipes drive staging and authentication; mutable public estimates grant no authority.
  The shared installed-identity gate admits hstore's fixed OUT capability, then
  checks the exact slot-owned declaration: missing, reconstructed same-shape or
  transplanted OUT capabilities remain invalid.
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`):
  `ValidateIntaggIdentity`, `ProcedureSymbol`, `IntaggDefinition` and the
  owned-aggregate checks and remap (`plan.SameIntaggIdentity` compares captured
  identities).
- **pgstattuple** (`pgstattuple_provider.go`): the superuser, untrusted,
  relocatable 1.5 provider installs only `pgstatindex(text)` and
  `pgstatindex(regclass)`, with ten immutable fixed OUT fields, STRICT VOLATILE
  RECORD results and PARALLEL SAFE C declarations. The sealed declaration slot
  retains distinct released C symbols through rename, move, membership and
  control remapping. Installation preserves creation defaults, revokes PUBLIC
  EXECUTE and grants EXECUTE to the actual predefined pg_stat_scan_tables
  principal through ordinary ACL authority; final script ACLs are recorded as
  initial privileges. Runtime authorization is ordinary EXECUTE, not a
  superuser-only or parent-table privilege policy.
- **pageinspect** (`pageinspect_provider.go`): the superuser, untrusted,
  relocatable 1.13 manifest owns four BRIN byte decoders, `bt_page_items(bytea)`,
  `fsm_page_contents(bytea)`, three GIN BYTEA decoders, `page_header(bytea)`
  and `page_checksum(bytea,bigint)`, followed by four hash BYTEA decoders.
  Hash type returns TEXT, stats and metadata scalar RECORD, and items SETOF
  RECORD; all four are STRICT, VOLATILE and parallel safe. Physical
  `hash_bitmap_info(regclass,bigint)` acquisition is not registered by this
  byte-decoder provider. Header inspection owns nine typed OUT
  fields; checksum uses the released `page_checksum_1_9` C identity and INT2
  result. Both are scalar, STRICT, VOLATILE and parallel safe. GIN metadata and
  opaque inspection return scalar records; compressed leaf inspection returns
  a record set. FSM has no named OUT parameter: its source label follows the
  SQL function name or range alias.
  Scalar, record and set results retain exact released declaration shapes and input
  labels. Complete private recipe validation runs at CREATE admission; current
  installation validation accepts reciprocal membership changes without a fixed
  member count. `ValidatePageinspectCall` validates the immutable released SQL
  symbol, formal slots and descriptor-owned results independently of mutable
  public names or the live original extension. Explicit C source identities
  preserve `bt_page_items_bytea` through member renames. Detachment, foreign
  membership and original-extension retirement do not change released capability.
  The v3 owner codec preserves all fifteen declaration slots, symbolic signatures
  and immutable OUT payload alongside current owners and membership; import
  still validates each actual destination member identity.
- **amcheck** (`amcheck_provider.go`): the untrusted/superuser/relocatable 1.5
  provider installs eight callable members. Slot zero is `verify_heapam`, with
  six canonical inputs and five trailing defaults, a non-STRICT volatile
  record-set result and parallel-unsafe declaration. Slots one through seven
  are the three `bt_index_check` and four `bt_index_parent_check` scalar
  overloads returning VOID, all STRICT, VOLATILE and parallel restricted.
  Each member's creation ACL observes the actual owner's global and target-schema
  routine defaults through the normal catalog candidate. The script's selective
  PUBLIC EXECUTE revoke preserves named grants/options and records the complete
  resulting ACL even when PUBLIC was already absent. Raw NULL defaults alone
  materialize the hardwired baseline; explicit ACLs are not replaced by it.
  Ordered manifest
  symbols and the distinct heap/B-tree identity validators preserve exact
  installed declaration authority through rename, move, owner transfer and
  symbolic replay. Current arbitrary installed ACLs, including grantor and
  grant-option provenance and PUBLIC entries, use the shared member codec;
  mutable names never authorize execution. No scan state is serialized.
  After selected-lifetime and current EXECUTE admission, the B-tree host
  invocation borrows the admitted catalog's canonical procedure and epoch,
  never a retained preparation epoch. This invocation-local bridge neither
  mutates the stored selection nor authorizes an otherwise stale reference.
- **tablefunc** (`tablefunc_provider.go`, `tablefunc_stage.go`): trusted/relocatable
  version 1.0 retains strict volatile `normal_rand(integer,float8,float8)` at
  slot zero and four strict stable SETOF-record connectby overloads at slots
  one through four. Appended single-source `crosstab(text)`, its ignored-integer
  overload and `crosstab2`/`crosstab3`/`crosstab4` are STABLE, STRICT and
  set-returning. Generic calls use caller-owned record definitions, not provider
  OUT parameters; each fixed wrapper returns its genuine standalone text composite.
  One sealed sixteen-declaration recipe contains ten procedures, three named
  types and their three implicit arrays, independent of mutable reciprocal
  membership. Backing composite relations are not extension members. Staging,
  immutable symbol checks and symbolic v3 owner replay share that recipe;
  mutable names never grant execution.
  `Stage.Composite` uses the ordinary relation allocator, row-type staging and
  type ACLs without SQL reentry. Validation checks current executable symbols,
  reciprocal primary/backing/array identities, stable attributes and coherent
  owners. Portable identities use current symbolic type names and signatures,
  never allocator numbers; import reowns each real backing together with its
  primary and array. Procedure remapping carries actual named result identities.
  Detachment, foreign membership and deletion of the original extension do not
  change genuine routine capability. Current owner/ACL, domain/epoch and complete
  released declaration checks still apply.
- **pgrowlocks** (`pgrowlocks_provider.go`, `pgrowlocks_stage.go`): the
  nontrusted/superuser/relocatable 1.2 singleton installs exactly
  `pgrowlocks(text) RETURNS SETOF record` (STRICT, VOLATILE, parallel safe).
  `ValidatePgrowlocksIdentity` checks actual owner-domain, epoch, descriptor and
  singleton manifest, not spelling. Owner metadata, transaction publication,
  dependency removal and remap use the shared extension lifecycle; no lock state
  is serialized.
- **refint**: the untrusted/superuser-required, relocatable 1.0 provider installs
  the two zero-input C-language functions `check_primary_key()` and
  `check_foreign_key()`, both returning trigger. Immutable declaration slots and
  current provider validation, not public names or mutable membership, grant
  native trigger capability. Generic installation, owner import, transaction
  publication and remapping preserve those identities. The shared trigger
  owner executes the stock AFTER helper protocols and ordinary invoker SQL;
  no native-FK authority or arbitrary C-library loader is implied.
  A stock member name collision reports that member's actual CREATE FUNCTION
  statement and location in the released installation script.
  Extension creation publishes the control-file comment into the installed
  descriptor once. The ordinary comment projection supplies both
  `pg_description` and `obj_description`; it does not look up a default by
  extension name at query time. Restore recreates this default through the
  same installation path.
- **isn** (`isn_provider.go`, `isn_stage.go`): the trusted, relocatable 1.3
  provider installs `ean13`, `isbn`, `isbn13`, `ismn`, `ismn13`, `issn`, `issn13` and `upc`, each with a
  reciprocal internally dependent associated array and immutable strict C-language codecs.
  Twenty-four append-only recipe declarations bind `ean13_in(cstring)`/`ean13_out(ean13)`,
  `isbn_in(cstring)`/`isn_out(isbn)`,
  `isbn13_in(cstring)`/`ean13_out(isbn13)`,
  `ismn_in(cstring)`/`isn_out(ismn)`,
  `ismn13_in(cstring)`/`ean13_out(ismn13)`,
  `issn_in(cstring)`/`isn_out(issn)` and
  `issn13_in(cstring)`/`ean13_out(issn13)` and
  `upc_in(cstring)`/`isn_out(upc)` to their own scalars. The ISBN13,
  ISMN13 and ISSN13 input SQL identities have immutable source symbols `isbn_in`,
  `ismn_in` and `issn_in`; output overloads are
  distinguished by their own scalar signature. Owner metadata records
  each output argument symbolically and remaps it to its destination descriptor.
  Admission follows script order: input signature, output signature, then full
  type collision. An occupied type supplies only the existing output argument
  identity for collision detection; installation never adopts its descriptor.
- **citext and earthdistance.** Citext stages the scalar, two I/O routines,
  six strict immutable comparison routines and their six operators, plus the
  real strict `citext(bpchar)`, `citext(bool)` and `citext(inet)` conversions.
  Its eight strict immutable INTERNAL pattern procedures and sixteen operators
  accept `(citext,citext)` and `(citext,text)`. Their append-only slots preserve
  the storage/comparison/conversion prefix; paired negators retain exact routine
  and operand identities through symbolic owner metadata and import.
  Ten SQL-language regexp overloads occupy a bounded procedure range after the explicit
  pattern-operator bound: `regexp_match`, `regexp_matches`, `regexp_replace`,
  `regexp_split_to_array` and `regexp_split_to_table`, each with and without text
  flags. Their first two formals are citext; replacement and flags are text.
  Declaration metadata retains release argument names, strictness, immutability,
  parallel safety, set status and row estimates without granting execution.
  Shared regexp scalar/set hooks preserve original spelling and text/text-array
  results, default to insensitive matching unless flags contain `c`, and retain
  exact provider identity, current caller ACL admission and control remapping.
  Four subsequent SQL scalar slots bind `strpos(citext,citext)`,
  `replace(citext,citext,citext)`, `split_part(citext,citext,integer)` and
  `translate(citext,citext,text)`, with unnamed arguments and INT4/TEXT results.
  Their symbolic owner signatures retain the distinct third formals; category
  bounds keep regexp membership independent of the complete 56-member manifest.
  Incumbent comparison/operator declarations remain fixed; the associated array
  has genuine internal/current-extension dependencies. Operator links and
  procedure authority come from immutable recipe declarations and signatures.
  Comparison routine display declarations retain C
  language and original source symbols; I/O routines retain INTERNAL language
  and text codec symbols. I/O helper names use `ScriptFunctions`; owner metadata
  serializes citext inputs symbolically and resolves destination descriptors
  and installed conversion results on import, never copying a source OID.
  Conversion declarations own the released internal display symbols `rtrim1`,
  `booltext` and `network_show`, with invoker security and unnamed arguments;
  display metadata does not replace the installed provider's execution authority.
  Earthdistance requires installed cube and atomically stages the `earth` domain,
  its actual companion and six coordinate/conversion routines, followed by
  `geo_distance(point,point)` and its self-commuting `<@>` operator. Incumbent slots
  stay fixed. Operator admission validates its exact slot, formals, result and
  backing procedure against the current manifest. `Stage.EarthChecks` attaches the
  three released domain recipes. V3 owner metadata records final portable SQL
  identities plus immutable release/declaration keys; companion resolution uses
  the domain's actual Array identity, not `_earth`. Routine labels, namespaces,
  owners and membership can vary independently; domain/companion owner coherence
  remains the ordinary type owner's responsibility.
- **lo** (`lo_provider.go`): the trusted, relocatable 1.2 provider stages three
  ordered declarations: the constraint-free concrete domain over builtin OID,
  its lookup-only companion, and volatile non-strict parallel-unsafe
  `lo_manage() RETURNS trigger`. Each carries an allocated catalog revision.
  Ordinary domain coercion and OID transport retain unsigned payloads and typed
  NULLs without looking up large objects; logical lo results have OID metadata.
  No trigger is automatically attached. Opt-in BEFORE or AFTER row cleanup
  unlinks non-NULL OLD references on DELETE, or on UPDATE when the column is
  explicitly targeted and its reference changes (including to NULL).
  Earlier row rewrites do not widen the target set. Equal values and NULL OLD references
  preserve bytes. Cleanup shares row/LO rollback; TRUNCATE and DROP bypass it.
  There is no reference counting: shared references can leave dangling rows.
  Domain, companion and procedure retain sealed origin across independent
  rename/schema/membership mutation. V3 import resolves final portable identities
  and procedure ownership without source OIDs.
- **Registry and update paths** (`extension_register.go`, `extension_paths.go`):
  `Register(registry, name, implementation)` checks one startup registration
  (name, control version, `ValidateActions`, duplicates, in that order) and
  returns the extended registry (allocating a nil one); on error it returns nil
  and leaves the registry unchanged. `UpdatePathRows(ctx, registry, name,
  shape)` builds the `pg_extension_update_paths` rows (0A000 first, then one
  row per ordered version pair, checking `ctx` before each).
- **Staging** (`stage.go`, `*_stage.go`, `extension_actions.go`): `Stage` is the
  host a provider installs through (the stage base, name lookup, checked
  `Create`/`Replace`, OID and revision allocation, the associated array helpers,
  creation type/routine ACLs and `ScriptFunctions`, the shared script helper lookup
  used by unaccent and citext). `Installer` is an implementation with
  `Stage(Stage, ext, owner)`; every stock provider is one, and fuzzystrmatch is
  also an `Updater` (`Updates`, one `UpdateAction` per executable version edge).
  `Actions`, `ValidateActions` (registration check) and `StagePath` (runs the
  update actions along a version path).
  Bundled installers stage finite declarative provider graphs; they do not load
  arbitrary filesystem control/SQL scripts, native libraries or external
  processes. Event-command inspection records their real outer CREATE/UPDATE,
  not an invented script history; an already-installed UPDATE version returns
  no command address and therefore no inspection row. Ordinary member ALTER is
  not installation execution and does not derive `in_extension` from membership.
- **Handlers** (`host.go`, `extension_create.go`, `extension_update.go`,
  `extension_drop.go`, `extension_owner_import.go`): `Host` is the statement
  host of CREATE and ALTER EXTENSION UPDATE (context, notices, the creation
  path, the trusted-replay flag, the bootstrap superuser, OID and revision
  allocation through the embedded `ddl.Allocator`, and `BeginStage`, which
  begins and authorizes the stage and returns it as a `Stage`; `Stage` embeds
  `ddl.Allocator` and `ddl.TypeStaging`). The statement authority is a
  [`ddl.Authority`](../ddl/README.md); the handlers check it through the
  captured `access.View` passed beside it (`view.Check(auth, ...)`,
  `view.Effective(auth, ...)`), which also supplies the catalog, roles, role
  state, registry (`Owner.Extensions`) and object domain. `StageCreate` and
  `StageUpdate` make every decision of the two statements and stage the result,
  returning nil for a skipped statement (IF NOT EXISTS, already installed
  version). `DropTargets` selects DROP EXTENSION's targets (notices through a
  callback). `ImportOwners` applies Restore's owner/installed-procedure ACL
  records to a private catalog clone and reports whether authority changed.
  Ownership import uses the shared immutable ACL-owner transform for types,
  companions and procedures. Synthetic type baselines encode as raw NULL ACLs,
  not historical-owner grants; import reconstructs that baseline for the current
  named owner while keeping explicit-empty and explicit-default ACLs distinct.
  Malformed synthetic baselines still fail the ordinary provider validator.
  Shared untrusted-extension creation rejects nonsuperusers with 42501.
  The sole owner metadata codec is **v3**, marker
  `stuntgres-extension-owners-v3`; no older decoder/shim remains. Each installation
  record distinguishes final extension presence from surviving provider origin.
  Provider members carry immutable release/declaration keys plus final typed SQL
  identities, named owner/ACL and independent current extension membership.
  Portable type spellings come from the lower catalog naming owner shared with
  regtype output and machine identities, including aliases, visibility, quoting
  and generated arrays. The codec does not import regobj, execution, analysis or
  pgcatalog, and does not maintain a parallel formatter/resolver.
  Ordinary members carry only typed final identity and membership; their existing
  serializers remain the sole owner/ACL authority. Globals have no fake schema.
  Text-search configurations use kind `configuration` with final namespace/name
  and empty signature. They remain ordinary owner-bearing objects without an
  object ACL or fabricated installed origin: their parser, ordered mappings and
  owner are reconstructed once by the existing serializer before membership.
  Text-search parsers use ordinary kind `parser` with qualified namespace/name,
  empty signature and no declaration/version/owner/ACL. Their ordinary CREATE
  and genuine callback graph precede final ADD; seeded default3722 keeps ALTER
  replay. Shared remap carries parser membership and all nonzero callbacks in
  the same map as the configuration's Parser edge, preserving optional zero
  HEADLINE. Final import validates each complete reciprocal parent graph;
  early installed-owner replay admission does not require future ordinary SQL.
  Duplicate/unknown/null/missing fields, invalid member identity, missing roles
  and incompatible declarations reject before publication. Import preflights all
  records before mutating even its private candidate. Named grantor/grantee
  privilege masks preserve PUBLIC, grant options and absent/explicit-empty ACLs;
  no source principal or physical handle crosses the codec.
  Generated companions use their actual captured SQL labels, including numeric
  and UTF-8-truncated collision history. Control replay reconstructs them through
  existing type/relation owners and real temporary-domain displacement, sharing
  the ordinary serializer's naming model; owner import never renames an array to
  make a record fit. Ordinary range constructors retain their independent public
  identity, owner and ACL authority. Their real private CREATE TYPE baseline is
  finalized through typed constructor declarations and ordinary ALTER FUNCTION;
  moving a type or refreshing its generation does not move/rename its routines.
- **Owners, remap, errors**: the owner metadata codec (`OwnerMetadata`,
  `DecodeOwners`, `OwnerRecord`, `MemberOwner`, `OwnersMarker`;
  `extension_owners.go`), `RemapDescriptor` (`extension_remap.go`),
  `MemberConflict`, `NameConflict` and `VersionError` (`extension_errors.go`),
  `AuthorityObservation` (its `Current` is the publication fence check) and
  `RemovalRoots`.
  `RemapDescriptor` is the sole operator-descriptor remap owner, including
  membership and all type/procedure links. Each source maps once; a destination
  that is also a source does not trigger another mapping step.

Type execution identity is its immutable installed declaration, not its mutable
name, namespace or membership. `InstalledTypes`/`MemberTypeRole` expose surviving
typed recipe roles where needed; ordinary type owners enforce reciprocal arrays,
concrete generation and genuine dependencies. V3 owner import uses final symbolic
SQL identities and checks real reconstructed origin, never a same-named loose type.


## Invariants

- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read`, `Stage` and the `*catalog.State` values the engine
  passes (`ImportOwners` writes only its private clone).
- **Lock contract.** The engine calls `StageCreate`, `StageUpdate`,
  `DropTargets`, `ImportOwners`, `AuthorityObservation.Current`,
  `Installer.Stage`, the update actions and `StagePath` with `Engine.Mu` held,
  and its `Host` and `Stage` implementation (`schemacmds`' `extensionHost`)
  runs under that lock too; nothing here may call anything that takes it. Provider validation
  (`ValidateExtension`) runs later, outside the lock. Allocation and check order
  are part of the output: installed OIDs, revisions and the first error must
  not change.
- **Immutable stock providers.** The stock instances are never mutated;
  `Control` and `Versions` return detached copies.
- **One provider identity.** The engine registers the stock providers in
  `catalog.Owner.Extensions`, and staging stores the same pointer in each
  member's `Implementation`. Every `*ProviderOf(d) != Stock*` check and the
  validators rely on that identity.
- **One lifecycle.** Family/signature/array validation, symbolic owner metadata
  and procedure remapping are registry-owned; providers never add a second
  extension lifecycle or invented callable members.
- **Stable output.** Member OIDs, namespaces, owners, messages, SQLSTATEs, the
  owner metadata format and remap are byte-stable; dumps depend on them.

## What stays in the engine

[`schemacmds`](../schemacmds/README.md#extensions) holds the `extension.Host`
and `extension.Stage` adapter `extensionHost` (`extension_host.go`, with
`stageOf`, which gives the executors their `CatalogStage` back) and the
orchestration of the handlers (`extension_install.go`, `extension_update.go`,
`extension_drop.go`, `extension_owner_import.go`): argument count, `Engine.Mu`,
authorization capture, publication (`publishExtensionStage`), DROP's removal
pipeline and its multi-target 2BP01 text and cascade notice, and the owner
import's authority check, clone, expression reown and publish; and the fence
wrapper `extensionAuthorityFence`. Core keeps the closed-registry check of
`registerExtension` and the startup list in `engine.go`; the snapshot check of
`statementHost.OpenExtensionPaths`; the installed expression wrappers and the
`pg_extension_update_paths` node, which belong with `plan`; the intagg binder
and view recipe methods.

## Testing

`TestRegisterAddsOnlyValidImplementations` pins `Register` (nil registry
allocated; duplicate, nil, versionless or badly named registrations rejected
with the registry unchanged). Pure provider identity and owner-codec units use
unpublished catalog states; composed installation, conflict and owner-import
witnesses live in `internal/engine/schemacmds`. Public SQL lives in
[reference/extensions](../../../reference/extensions/README.md#testing) (extension
DDL and catalogs),
[reference/extension_types](../../../reference/extension_types/README.md#testing)
(lo, ltree and ISN values),
[reference/extension_hstore](../../../reference/extension_hstore/README.md#testing) (hstore),
[reference/extension_cube](../../../reference/extension_cube/README.md#testing) (cube, earth),
[reference/types](../../../reference/types/README.md#testing) (citext) and
[reference/locking_transactions](../../../reference/locking_transactions/README.md#testing)
(pgrowlocks). Add `REFERENCE=1` for the oracle.
Autoinc installation and forgery units in `enginetests` use
`RUN='^TestAutoincProviderIdentity$'`; its public scenarios live in `reference/operators`.

Identity-remap units retain genuine physical TEMP owners and derive collision
targets from actual imported identities. The earth control fixture allocates
namespace pairs until an observed identity collides, failing if allocation
passes the target. Replacement must remint the imported domain while preserving
the retained TEMP descriptor; incidental prerequisite-member counts are not
collision evidence.
Hstore comparison graph, authenticated index support, staged collision and
source-preserving transport units use `RUN='^TestHstoreComparison(Provider|IndexSupport|IndexTransport|Remap)$'`.

```sh
make check RUN='^Test(Extension|TablefuncCrosstab(ProviderIdentity|ControlIdentity)|Isn|Issn|Citext(Provider|Comparison|Pattern)|CubeProvider|CubeCoordinateProvider|CubeSpatialProvider|EarthProvider|EarthCoordinate|PgrowlocksProvider|PgcryptoOwner|InstalledCodecArray)'
make check RUN='^TestAmcheck'   # installed B-tree manifest and expression identity
make check RUN='^TestReference(Extension|AvailableExtensions)'
```


Routine origin/label and strict codec proof (no SQL execution):
`make check RUN='^Test(InstalledProcedure(RelabelIdentity|Forgery|VersionTransition|RemapIdentity)|DetachedUnaccentImplementationIsNotOrigin|ExtensionOwner(DeclarationCodec|ImportCurrentSlotIdentity|ImportTransfer|V3.*|ImportLateFailureAtomic))$'`.
The independently declared selected tablefunc inventory (normal_rand slot zero,
four connectby declarations), mutable-label identity, forged-recipe rejection and
strict v3 DecodeOwners plus ImportOwners authority are selected with
`make check RUN='^TestTablefuncConnectby'`.
All four overloads also have typed detached/foreign/absent-origin carrier and
shared canonical-remap coverage under
`make check RUN='^TestExtensionOwnershipIdentity$$/^connectby_'`.
Engine caller witnesses cover v3 metadata, detached/foreign origins, invalid
import labels, atomic late failures and genuine provider relabeling.
Public provider/replay behavior uses shared `TestReferenceRoutineAlter*`
definitions in native and `REFERENCE=1` modes; see
[routine proof](../../../reference/query_core/README.md#routine-alter-proof).
