# internal/engine/extension

The installed-extension layer that does not need the engine: provider records
and control data for bundled extensions (amcheck, autoinc, bloom, btree_gin, citext, cube,
earthdistance, dblink, dict_int, dict_xsyn, fuzzystrmatch, hstore, insert_username, intagg, intarray, isn, lo, ltree,
moddatetime, pageinspect, pg_buffercache, pg_trgm, pg_stat_statements, pgstattuple, pgcrypto, pgrowlocks, postgres_fdw, refint, seg, tablefunc, tcn,
sslinfo, tsm_system_rows, tsm_system_time, unaccent, uuid-ossp, xml2 and seeded plpgsql),
member validation, membership, the owner metadata codec used by native Dump,
extension remap, provider staging, and CREATE/UPDATE/DROP plus the lower
ownership/membership/relocation and Restore owner-import decisions.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access` (privilege requirements and the captured view),
`authorization`, `catalog`, `ddl` (the shared authority and allocator), `expr`,
`functions` (kernels and member tables), `ids`, `parser`, `proc`, `settings`
(pure numeric option decoding), `sqlstate`, `sqltype`,
`typedef` (the relation type name check of installed-scalar staging)
and the standard library; never the engine, `analyze`, `routine` or `plan`.

## What it owns

- **Providers** (`*_provider.go`): one provider type per extension, each with
  `Control`, `Versions`, `ValidateExtension` (`catalog.ExtensionImplementation`)
  and, for providers with callable members, `ValidateProcedure`. The stock instances are
  `StockCitext` … `StockUUIDOssp`, and `StockSSLInfo`. The script contexts (`CitextTypeContext`,
  `CubeTypeContext`, `IntarrayInputContext`, `UnaccentTemplateContext`, …),
  signatures (`CubeSignature`, `CubeBinarySignature`, `QueryIntSignature`) and
  member tables (`FuzzystrmatchMembers`, `IntaggMembers`) are what staging needs.
  PL/pgSQL staging uses catalog's `PLHandler.ReleasedProcedure`, the same released
  declaration producer as bootstrap; neither path infers C library provenance
  or parallel safety from current membership. The sealed recipe retains those
  declaration facts while current owner/ACL, cost and parallel overrides remain
  independent and survive native replay.
  Dblink's bounded recipe installs `dblink_build_sql_delete` in slot0,
  `dblink_build_sql_insert` in slot1 and `dblink_build_sql_update` in slot2,
  with stock 1.2 control and released C declarations. Slots3/4 own the nominal
  `dblink_pkey_results` composite and implicit array; slot5 owns the unnamed,
  STRICT SETOF `dblink_get_pkey(text)` declaration. Type validation includes the
  backing relation, declared nullable INT4/TEXT fields and reciprocal array.
  Sealed roles authenticate independently of current names, owner and membership,
  including the same-signature INSERT/UPDATE pair; symbolic replay retains the
  primary/array roles and function return link. Slots6–12 append connect/connect_u
  overloads, disconnect overloads and get_connections without renumbering those
  six origins. Both connect_u declarations share C symbol `dblink_connect` but
  retain distinct immutable SQL/binding names and initial SECURITY DEFINER,
  non-PUBLIC ACLs. Current owner/security/configuration remain routine-environment
  facts, not spelling-based authority. The selected native manifest is thirteen;
  shared PG tests filter these declarations and prior relationships rather than
  constrain the real extension's full query/cursor membership.
  Bloom installs its released six-member handler/method/family/class graph.
  Its C declaration describes a registered Go implementation, not a loaded
  shared library. The global AM is ownerless; its handler and int4/text classes
  are schema-owned. Sealed origin authenticates the native capability and exact
  builtin equality/support1 links independently of current e-membership.
  Symbolic control replay creates genuine declarations through CREATE EXTENSION;
  aliases remain ordinary separately deparsed access methods.
  Existing generic DDL can give a genuine handler alias ordinary int4/text
  classes: binding requires the actual matching family, builtin equality1 and
  exact native support1. Neither alias nor class acquires stock origin by name.
  Btree_gin stages bit, varbit, enum, money, inet, cidr, macaddr, macaddr8, uuid,
  oid, int2, int4, int8, float4, float8, bool, date, time, timetz, timestamp,
  timestamptz, interval, text, varchar, bpchar, internal char, name and bytea
  families with five search and five support slots through the same installation
  owner. Network, numeric/boolean, temporal and text recipes retain AUTO family
  ownership for search/optional supports and INTERNAL class ownership for
  extraction slots2/3; incumbent compound dependency ownership is unchanged.
  One explicit helper-triplet group per class drives both staging and original
  graph authentication. Text and varchar share the same text-formal triplet;
  varchar support associations remain1043 while search/comparator operands are25.
  CIDR support association remains650 while search/comparator operands are869.
  These distinctions never grant execution by mutable names or copied routines.
  Internal-ABI declarations are index capabilities, not C pointers or scalar emulation.
  The genuine `gin_enum_cmp` declaration is separately SQL-callable through the
  concrete enum comparator. Class/helper authority follows original sealed
  recipe roles across current membership and presentation; allocated IDs alone
  cannot substitute an independently installed recipe.
  Initial privileges are separate historical catalog records, not recipe ACLs.
  Providers call `Stage.RecordInitialPrivilege` only for genuine script ACL
  events: amcheck's nine implemented final REVOKE declarations capture the
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
  Tsm_system_rows and tsm_system_time each install their single released strict
  volatile `(internal) RETURNS tsm_handler` C declaration. `SamplingArgumentType`
  authenticates the complete current sealed declaration before selecting the
  closed bigint rows or float8 elapsed-time recipe, independently of rename or
  relocation. Names, return shape and copied membership grant no authority.
  Runtime factories first authenticate any retained declaration through the
  existing stored-routine carrier check, then validate the actual current
  descriptor. Lawful rename/owner/ACL COW therefore preserves a selected handler
  without accepting a stale ABI, substituted origin or mixed implementation.
  Low-level sources must carry their genuine captured catalog just like ordinary
  query sources; absence is not permission to infer a handler from its name.
  Native replay reinstalls ordinary sealed declarations and symbolic callers,
  never clocks, sample selections, session resources or C pointers. Physical
  heap blocks, buffer/cache clustering and shared-library ABI execution remain
  external mechanisms, not portable timing-count or selected-order contracts.
- **Accessors** (`extension_implementation.go`): `CitextProviderOf` …
  `UnaccentDictionaryProviderOf` return a descriptor's implementation when it
  has that concrete type, and nil otherwise.
- **Stock postgres_fdw** owns a sealed two-routine/wrapper installation and real
  base-scan/transaction callbacks. Execution consumes only the core-authenticated
  private catalog lease interface; physical caches, credentials, publication and
  cleanup remain in the existing remote.Owner. Projections quote mapped names by
  presence and cursors fetch actual source rows. Immutable admitted local
  stable-attribute/base-type facts select projected BYTEA input attributes once;
  remote OIDs never select a local decoder. Only local BYTEA/base-domain slots
  pass through the captured builtin input descriptor before ordinary foreign
  import; other local types receive remote text unchanged. Decoder failures
  retain the original cause and stable attribute for exec's existing local
  column diagnostic owner, never provider-generated aliases or remote context.
  The shared importer alone applies local domain/typmod checks, preserving NULL
  and empty octets.
  Generic path costs use the shared numeric parser and saturate only unbounded estimates to
  the finite callback domain. No transport, catalog coordinator or optional FDW
  callback is synthesized by storing an option.

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
  Recipe equivalence is not catalog-domain authority: current resident procedure,
  principal and domain/epoch-qualified reference admission still reject imported
  descriptors or references even when independent installations allocated equal IDs.
  `ProcedureInstallationName` applies the same origin checks to the released SQL
  name used by replay; executable C symbols are not SQL call names.
  `ProcedureBindingName` uses that same validation once to select the internal
  binding recipe: explicit `functions.Definition.Installed` gateways use the
  released SQL declaration, while engine-owned specialized/polymorphic families
  keep `ProcedureSymbol`. Probe, named-call and selected-gateway matching share
  this selection. Registry classification never substitutes for current origin,
  provider-family or execution privilege checks and exposes no C-symbol aliases.
  Stock file_fdw 1.0 seals two strict volatile C helper declarations and its
  global wrapper. Their exact immutable method tokens, canonical slots and
  reciprocal helper links are authenticated independently of mutable labels,
  owners and membership. `ForeignObject.Origin` participates in the same seal,
  class validation, owner codec and coherent canonical/live remapping as other
  installed members; Driver registrations cannot select this recipe. Native
  replay reinstalls it before foreign consumers and replays global wrapper
  identity through the existing provider collision planner.
  Stock pg_stat_statements 1.12 seals three C-function recipe roots: the two
  readers retain slots0/1 and the scalar reset owns slot2, four immutable literal
  defaults and genuine argument names. Reset's script PUBLIC EXECUTE revoke
  preserves named creation grants and records an independent initial ACL.
  Its `ConsumerViews` manifest still creates exactly two ordinary SQL views
  after sealing, through `StageConsumers` in the same unpublished candidate.
  Ordinary views acquire real membership and rowtype/array closure, never
  fabricated installed relation origins. Each `ConsumerView` owns its ordinary
  CREATE AST and ordered script ACL events; nil policy preserves creation defaults.
  Statistics views explicitly grant PUBLIC SELECT. Restricted consumers revoke
  PUBLIC and grant named roles without erasing unrelated creator defaults.
  Actual script ACL events record historical initial privileges. The same
  declaration list identifies fresh replay products; current source view state
  remains the ordinary view codec's concern.
  Pgcrypto preserves slots0–21 and appends four strict, parallel-safe BYTEA PGP
  declarations at22–25: volatile encryption and immutable decryption, each with
  two and three arguments. Existing text PGP slots18–21 and armor slots14–17
  retain their identities. Armor uses `pg_armor` / `pg_dearmor`; text PGP uses
  `pgp_sym_encrypt_text` / `pgp_sym_decrypt_text`, and BYTEA PGP uses its SQL names.
  Binding and replay use independently authenticated SQL declaration names.
  `pgp_armor_headers` owns the sole pgcrypto OUT capability: one unnamed TEXT
  input, `key`/`value` TEXT outputs, RECORD result, STRICT/IMMUTABLE/PARALLEL SAFE,
  set cardinality and rows1000. Only that sealed slot may carry its exact immutable
  `RoutineOutputs` object; matching names, record shape or transplanted output
  metadata never confer implementation authority. Native owner metadata/replay
  remaps the existing sealed installation and symbolic formals, not source OIDs.
  Script sealing and generated-membership admission revise already staged
  creations through the extension host's identity-preserving candidate update;
  they do not replace nonexistent base predecessors. The original CREATE
  name/absence observations remain publication fences. Base-object revisions
  still use ordinary replacement, and all candidates retain full provider,
  reciprocal membership and current installed-declaration validation. The one
  exception is a membership-only header revision (ALTER EXTENSION ADD/DROP,
  `catalog.MembershipRevision`): `CatalogProvider.ValidateRevision` checks its
  provenance and reciprocal membership but not the surviving declarations, which
  never read sibling membership; the re-linked members are staged and validated
  by their own providers.
  Initial CREATE copies the control comment into the immutable extension header;
  skipped CREATE and UPDATE never reapply that default. Header copies preserve
  current comment text across placement, membership and ownership changes.
  Stock pgcrypto preserves its twenty-six installed declarations and provider
  identities. Its first actual C-member validation initializes only the
  installer's physical setting attachment; Prepare, catalog presence, early
  errors and skipped CREATE do not. Initialization survives later SQL rollback
  or member conflicts. The provider owns exact declaration script Context for
  initialization warnings and the corresponding duplicate-member diagnostics.
  This bounded initializer is not native library loading: generic LOAD keeps
  its external native-ABI/file boundary and is not a successful special case.
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
  latitude/longitude retain cube_ll_coord. Query bodies retain precisely
  earth_distance→sec_to_gc/cube_distance and earth_box→gc_to_sec/cube_enlarge;
  their radius dependency is transitive, not another flattened direct edge.
  The sealed Earth domain's base type
  selects the genuine cube origin and declaration slots, never captured spelling
  or current extension membership. Repeated written calls yield one direct edge.
  Current callee identity and principal validation still gate callable authority.
  UUID-ossp staging likewise preserves the released parallel-safe declaration
  and the `namespace`, `name` input labels of v3/v5. These labels belong to the
  sealed declaration, survive remapping and current membership changes, and
  resolve named calls through the ordinary formal-argument binder.
  XML2 1.2 seals twelve signatures: the four scalar declarations, the five-TEXT
  `xpath_table` source in its original slot 4, five nodeset/list overloads in slots
  5–9, then three-TEXT and two-TEXT `xslt_process` in slots 10 and 11.
  Its superuser-only untrusted installation remains nonrelocatable. Strict
  parallel-safe declarations retain INTERNAL `xml_is_well_formed` / STABLE,
  C XPath scalar `$libdir/pgxml` / IMMUTABLE and table C / STABLE / SETOF RECORD
  (Rows1000) provenance. XSLT's three-argument declaration is VOLATILE; its
  two-argument declaration is IMMUTABLE. Both retain C `xslt_process` /
  `$libdir/pgxml` display metadata, with no argument defaults or labels.
  Three real SQL-standard RETURN recipes refer to their installation's
  authenticated full-arity member slots. Normal dependency edges
  survive membership detach, current-name changes and remapping. Wrapper proconfig
  is ordinary current routine metadata and blocks inlining without changing body
  identity. Actual native body initialization selects these slots through the
  shared closed declaration-member resolver. It requires the exact canonical body
  part and authenticates both the current wrapper and selected kernel; the
  provider's metadata helper alone is not an executable source authority.
  Signature collisions retain released `xml2--1.1.sql` statement
  provenance through atomic staging; final 1.2 bodies do not invent an update
  graph or imply a shared-library loader.
  SSLInfo 1.2 seals ten strict, volatile, parallel-restricted C declarations.
  The original seven zero-input scalar slots remain stable; two TEXT-field
  scalars and the fixed-OUT certificate source append to them. The untrusted,
  superuser-only relocatable installation owns signatures and OUT metadata.
  Embedded non-TLS evaluation returns false BOOL inquiries, typed scalar NULLs
  and an empty `(name TEXT, value TEXT, critical BOOL)` source, after ordinary
  argument execution and invocation admission. No transport or certificate state
  is retained by the provider.
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
  Installed unaccent, dict_int and dict_xsyn templates authenticate their sealed released
  template role and callback declarations from the same genuine cohort;
  detached, foreign and absent-origin membership states do not change execution
  capability. Compilation and publication-current checks share this validation.
- **Seg members** (`seg_provider.go`, `seg_stage.go`, `seg_interval.go`): the
  trusted relocatable 1.4 provider owns nineteen scalar recipe slots: type/I/O,
  eight strict immutable C predicate procedures and their operators. Its
  reciprocal array is internally dependent and a current direct member, without
  an invented recipe slot. Recipe indexes retain logical declaration/origin
  order. Allocation places the generated array after type/I/O and before
  predicates/operators; its current membership is appended separately after
  sealing the nineteen declarations. Immutable origins, signatures and links
  authenticate calls and symbolic remapping; mutable labels/membership grant
  neither codec nor predicate authority. Equality retains MERGES metadata without
  granting ordering, and unrepresented negator/commutator links remain zero.
- **Intarray members** (`intarray_provider.go`, `intarray_stage.go`, `intarray_set.go`, `intarray_slice.go`, `intarray_sort.go`):
  the trusted relocatable 1.5 provider preserves query_int and bqarr_in/out at
  slots 0–2, intbig_gkey and _intbig_in/out at slots 3–5. Ten strict immutable
  executable integer-array backers occupy 6–15, six genuine stable INTERNAL
  estimator declarations occupy 16–21, and ten selected operators occupy 22–31.
  Five strict immutable slice declarations occupy 32–36: icount, idx, both
  subarray overloads and intset. Prefix and binary # occupy 37–38 with their
  genuine icount/idx backers and no estimator or commutator links.
  Five strict immutable sorting/adjacent-uniq overloads append at 39–43.
  Operator classification remains bounded to 22–31 and 37–38; appended sort
  roles are procedures, preserving every existing original identity.
  Estimators are catalog dependencies, not scalar execution kernels. Reciprocal
  commutators and restriction/join links use the complete privately allocated
  graph. Both generated type companions join current membership without extra
  original slots (44 sealed roles, 46 initial members). The support-key companion
  uses the shared concrete array codec; query_int's stays lookup-only.
  One private stage makes late type, helper and operator collisions atomic.
  Sealed installation roles, not mutable names or membership, drive validation,
  execution selection, ownership replay and destination remapping. Support-I/O
  slots 4/5 retain typed strict NULL and released nonnull rejecting diagnostics;
  bqarr declarations gain no direct-call gateway. Set, slice and sort calls bind through
  ordinary catalog overload selection and shared coercion. Unrelated query/index
  families remain outside this bounded recipe.
  Dump emits symbolic installation before consumers, preserving logical data,
  owners and dependencies rather than C library handles or physical GiST pages.
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
  Snowball helpers, or a currently authenticated unaccent, dict_int or dict_xsyn helper pair.
  Intrinsic callback/template/dictionary capability does not require empty or
  matching current extension membership or a live installation header. Each
  current member edge still requires its own reciprocal parent validation.
  Standalone templates use the same actual helper authority. Missing INIT never
  fabricates initialized state, and unavailable native callback combinations fail
  before publication.
  Detached compilation captures the catalog and rechecks both real helpers at
  completion. No callback or resource I/O runs under the catalog mutex.
  Callback links remain executable dependencies even when SQL `pg_depend`
  omits references to pinned builtin procedures. Installed unaccent, dict_int and dict_xsyn
  helpers retain visible NORMAL dependency edges.
- **Dictionary definitions** (`dictionary_*.go`): ordinary schema-CREATE and
  owner authority, independent of extension installation privileges. Preparation
  captures template/options and immutable startup resources; actual ispell
  parsing/condition compilation runs outside the catalog mutex. Completion
  rechecks current namespace/template/revision and role authority.
  SET SCHEMA checks destination CREATE before source ownership, both during
  preparation and at current-authority completion.
  Ordered immutable options and genuine simple/synonym/unaccent/dict_int/dict_xsyn/Snowball/thesaurus/ispell runtime state publish in
  the existing catalog journal. Unaccent initializes each Rules resource at its
  ordered option occurrence, so an earlier resource failure precedes later
  duplicate or unknown options without publishing partial runtime state.
  ALTER removes matching options before appending supplied replacements; the last
  replacement wins without borrowing or modifying the caller's option backing.
  Standalone dictionaries depend on their real namespace
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
  Dict_int owns four released declarations: two strict, volatile, parallel-unsafe
  helpers, the template and the default dictionary. Its registered Go kernel
  owns only maxlen/rejectlong/absval settings; ordered option text remains
  independent from effective settings, and no server files or C ABI are loaded.
  Dict_xsyn is superuser-required and untrusted. Its four released declarations
  seal the strict volatile parallel-unsafe helper pair, xsyn_template and an
  initially resource-free dictionary. Ordered Boolean/rules duplicates are
  last-wins; resource loading follows the entire option scan. Absent rules
  differs from a supplied empty basename. Admission checks every immutable
  Boolean, basename and presence bit against the genuine helper cohort.
  Bundled `.rules` bytes replace only server-file acquisition; ordinary
  dictionaries acquire no provider membership. Dictionary comments are immutable
  descriptor metadata, independent of runtime, and replay under actual qualified
  identities; installed/detached replay explicitly preserves default removal.
  Shared configuration lexization and logical typed index roots consume the same
  rich lexical result; physical PostgreSQL index machinery is not reconstructed.
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
  provenance, not a permanent default-dictionary binding. The explicit overload
  retains the genuine `3769,25` procedure and invokes the supplied dictionary's
  actual runtime, irrespective of template family. Both wrappers remain members
  of the existing six-object installation.
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
- **pg_trgm** (`pgtrgm_provider.go`, `pgtrgm_stage.go`): trusted relocatable
  release 1.6 has exactly sixteen scalar procedures (six functions and ten
  operator backers) and ten reciprocal operators. The single member table
  supplies C symbols, signatures, formal types/arity, result types, volatility
  and parallel safety; sealed slots, not current names or membership, authorize
  execution and native replay. Predicates and the zero-argument getter are STABLE;
  scores, distances and trigram extraction are IMMUTABLE. All declarations are
  STRICT. The REAL setter is VOLATILE/PARALLEL UNSAFE; the others are PARALLEL
  SAFE. Index support and version-update paths are not installed. Member staging
  reaches `Stage.ActivatePgTrgm` after the first setter's duplicate-name check,
  immediately before creating that genuine declaration. A setter collision
  leaves an unloaded session unloaded; getter and later collisions retain the
  physical loading reached at the setter's C validator despite catalog rollback.
  Skipped CREATE and early admission failures never reach that boundary.
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
  The 1.8 `hstore_subscript_handler` is the last, newest role; through the
  optional `catalog.TypeSubscriptProvider` it is hstore's `typsubscript`.
  The sealed manifest has sixty-seven declarations, independent of current
  membership; its sixty-six owner-bearing roles retain their authority.
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
- **intagg** (`intagg_identity.go`, `intagg_aggregate.go`): four real canonical
  procedures in released script order: non-strict volatile safe
  `int_agg_state(internal,int4)->internal`, non-strict volatile safe
  `int_agg_final_array(internal)->int4[]`, the immutable
  `int_array_aggregate(int4)->int4[]`, and strict immutable safe
  `int_array_enum(int4[])->SETOF int4`. The helpers are genuine known INTERNAL
  aliases to array aggregate implementations, not hidden OIDs or arbitrary
  callable INTERNAL support. UNKNOWN cannot implicitly coerce to INTERNAL.
  `ValidateIntaggIdentity`, `ProcedureSymbol`, `IntaggDefinition` and canonical
  support membership preserve owner, namespace, signatures, provenance and
  remapping. Actual transition/final SQL execute authority is the aggregate
  owner; simulated MAC uses the real lexical subject independently. Enumeration
  borrows the actual evaluator on each value-per-call resume, leaving its token
  before every yield, done or error; no cursor retains an evaluator or subject.
- **pg_freespacemap** (`pgfreespacemap_provider.go`) installs exactly the 1.3
  unnamed STRICT C `(regclass,bigint)->int2` scalar and non-STRICT SQL
  `(rel regclass)->SETOF (blkno bigint,avail int2)` wrapper. Both are volatile,
  parallel safe and invoker-rights. PUBLIC EXECUTE is revoked; genuine
  pg_stat_scan_tables grants and initial ACLs use normal authority. The released
  BEGIN ATOMIC declaration retains actual scalar-member and pinned builtin
  identities. Its normal wrapper-to-scalar dependency participates in catalog
  visitors, rename, relocation, retirement and native remapping. Optimized native
  execution enumerates the live-row freespace layout, not the physical-size counter.
  Inner authority resolves the authenticated scalar slot and installation cohort,
  never a same-named routine or a captured descriptor's old ACL.
  Installation collisions use the failed member's recipe-specific statement and
  location in the released 1.1 base script, including its legacy SQL wrapper;
  this diagnostic source is distinct from the final 1.3 BEGIN ATOMIC body.
- **pg_buffercache** (`pgbuffercache_provider.go`, `pgbuffercache_numa.go`) seals
  the 1.6 zero-argument VOLATILE, non-STRICT, parallel-safe invoker pages and NUMA
  SETOF-record declarations without OUT parameters. Ordinary views supply nine
  and three typed definitions. Each function EXECUTE and view SELECT has separate
  pg_monitor grants and PUBLIC revocations, preserving named creation defaults.
  Mutable names, owners, schemas and membership never select callable provenance.
  Stored caller definitions retain their sealed operation through reconstruction
  and remapping; execution admits current authority before row/count/type errors.
  The core-owned logical cache and supported virtual single-node NUMA topology
  inspect no PostgreSQL shared memory, host NUMA or C ABI.
  Recipe slots one through three seal the integer, regclass and zero-input
  eviction declarations with exact scalar-record OUT fields; pages retains slot
  zero. Evictions use ordinary creation/default routine ACLs, not observer script
  revocations or monitor grants. Their runtime effective-superuser admission is
  separate from SQL ACLs. Summary and usage retain slots four and five, with
  five/four declared OUT fields and scalar/SETOF cardinality respectively.
  Each uses pages' creation ACL, PUBLIC revoke, monitor grant and initial-ACL
  history independently; neither has eviction's superuser body gate. NUMA appends
  slot six. The two ordinary view/type/array closures remain outside the seven
  provider declaration slots; canonical replay preserves all thirteen members.
  Membership DROP removes initial privileges; ADD records the then-current ACL,
  independently of subsequent owner/grant changes and native replay.
  Installation conflicts preserve the released statement and script location in
  diagnostic Context: the integer declaration first occurs in 1.4--1.5, before
  its 1.6 OUT-record replacement; relation/all conflicts come from 1.5--1.6,
  and aggregate conflicts retain their original 1.3--1.4 statements/locations.
  This diagnostic provenance does not install the historical boolean-returning
  declaration.
  NUMA's CREATE OR REPLACE uses the ordinary pure catalog compatibility leaves:
  kind precedes result/set and declaration-owned OUT tuple checks, all before
  membership protection. Exact Detail/Hint survive the added 1.5--1.6 script
  Context; the incumbent's identity, body and ACL are never replaced.
- **pg_prewarm** (`pgprewarm_provider.go`) installs one genuine 1.2 C function,
  `(regclass, mode text DEFAULT 'buffer', fork text DEFAULT 'main',
  first_block bigint DEFAULT NULL, last_block bigint DEFAULT NULL)->bigint`.
  The first input is unnamed. It is volatile, non-STRICT, parallel safe and
  invoker-rights; ordinary creation/default ACLs apply. Installation does not
  invent an initial-privilege row. Its sealed provider slot, defaults, names and
  origin survive ordinary detach/reattach, rename, relocation and remapping.
  No autoprewarm helper is installed. All modes use the same explicit native
  coordinate-cache preload, not PostgreSQL shared memory or OS prefetch.
- **pg_visibility** (`pgvisibility_provider.go`) seals eight declarations: the
  five map/page/summary read recipes, two scalar TID set checks, and a scalar VOID
  map truncator with no OUT declaration. Incumbent slots remain stable. Immutable
  recipes retain released C symbols, unnamed inputs, exact result/OUT usage and
  source cardinality. The relocatable 1.2 provider is superuser-required and
  untrusted; members are STRICT VOLATILE PARALLEL UNSAFE invokers. Installation
  preserves other default grants/options, revokes PUBLIC EXECUTE, grants the
  scan principal to reads/checks but not truncate, and records real initial ACLs.
  Explicit EXECUTE can authorize truncate; current membership/spelling never
  selects execution or grants authority.
- **pg_surgery** (`pgsurgery_provider.go`) seals a finite freeze-first,
  kill-appended cohort: `heap_force_freeze` and `heap_force_kill`, each with
  `(reloid regclass, tids tid[]) RETURNS void`. The relocatable 1.0 provider is
  superuser-required and untrusted; both C members are STRICT VOLATILE PARALLEL
  UNSAFE and invoker-security. Each installation independently revokes PUBLIC
  EXECUTE, retains other creation-default grants and records its initial ACL.
  Sealed declaration/source and operation, not spelling or current membership,
  select execution through rename, detachment, foreign attachment and remapping.
  Runtime routine/schema privileges and target ownership remain live checks.
  Both unsafe operations publish exact version headers outside ordinary DML undo;
  kill leaves actual index membership and constraint/trigger state untouched.
  Physical pages, WAL and unreadable payload recovery are not implemented here.
- **pgstattuple** (`pgstattuple_provider.go`): the superuser, untrusted,
  relocatable 1.5 provider installs nine declarations: `pgstatindex(text/regclass)`,
  `pgstathashindex(regclass)`, `pgstattuple(text/regclass)`,
  `pgstattuple_approx(regclass)`, `pg_relpages(text/regclass)` and the appended
  `pgstatginindex(regclass)` with INT4/INT4/INT8 version/pending outputs.
  Per-member immutable declarations own their input
  name/type, scalar bigint or fixed OUT record result, and STRICT VOLATILE
  PARALLEL SAFE C identity. The sealed declaration slot
  retains distinct released C symbols through rename, move, membership and
  control remapping. Installation preserves creation defaults, revokes PUBLIC
  EXECUTE and grants EXECUTE to the actual predefined pg_stat_scan_tables
  principal through ordinary ACL authority; final script ACLs are recorded as
  initial privileges. Runtime authorization is ordinary EXECUTE, not a
  superuser-only or parent-table privilege policy.
- **pageinspect** (`pageinspect_provider.go`): the superuser, untrusted,
  relocatable 1.13 manifest owns four BRIN byte decoders, `bt_page_items(bytea)`,
  `fsm_page_contents(bytea)`, three GIN BYTEA decoders, `page_header(bytea)`
  and `page_checksum(bytea,bigint)`, followed by four hash BYTEA decoders,
  `heap_page_items(bytea)`, `heap_tuple_infomask_flags(integer,integer)` and the
  two overloads each of `tuple_data_split` and `heap_page_item_attrs`.
  Hash type returns TEXT, stats and metadata scalar RECORD, and items SETOF
  RECORD; all four are STRICT, VOLATILE and parallel safe. Physical
  `hash_bitmap_info(regclass,bigint)` acquisition is not registered by this
  byte-decoder provider. Heap inspection owns fourteen typed OUT fields and
  returns a set; flags own two TEXT[] fields and return one record.
  Header inspection owns nine typed OUT
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
  The v3 owner codec preserves all twenty-one declaration slots, symbolic
  signatures and immutable OUT payload alongside current owners and membership;
  import still validates each actual destination member identity. Split overloads
  own immutable five/six-input names and non-STRICT C declarations; attrs
  overloads own genuine parsed SQL-standard declaration recipes with symbolic
  heap/split/member dependencies, rather than fictitious C identities.
  The provider's `ProcedureDependencyProvider` visits exactly those genuine
  SQL-body helper edges for catalog projection, retirement and replay ordering.
  Analyzed display recipes expand the function-RTE columns under stable released
  aliases, qualify outer formals through the current wrapper name, and resolve
  helper calls through sealed member identities. The existing declaration
  renderer admits only these exact wrapper/helper edges, retaining canonical
  body, current descriptor and full helper-signature validation. Definition
  disclosure remains metadata-only through helper rename/move, detachment and
  native replay; it does not grant or require helper EXECUTE authority.
- **amcheck** (`amcheck_provider.go`): the untrusted/superuser/relocatable 1.5
  provider installs nine callable members. Slot zero is `verify_heapam`, with
  six canonical inputs and five trailing defaults, a non-STRICT volatile
  record-set result and parallel-unsafe declaration. Slots one through seven
  are the three `bt_index_check` and four `bt_index_parent_check` scalar
  overloads returning VOID, all STRICT, VOLATILE and parallel restricted.
  Appended slot eight is only `gin_index_check(index regclass) RETURNS void`,
  STRICT, VOLATILE and parallel unsafe; no incumbent slot is renumbered.
  Each member's creation ACL observes the actual owner's global and target-schema
  routine defaults through the normal catalog candidate. The script's selective
  PUBLIC EXECUTE revoke preserves named grants/options and records the complete
  resulting ACL even when PUBLIC was already absent. Raw NULL defaults alone
  materialize the hardwired baseline; explicit ACLs are not replaced by it.
  Ordered manifest
  symbols and the distinct heap/shared index identity validators preserve exact
  installed declaration authority through rename, move, owner transfer and
  symbolic replay. Current arbitrary installed ACLs, including grantor and
  grant-option provenance and PUBLIC entries, use the shared member codec;
  mutable names never authorize execution. No scan state is serialized.
  After selected-lifetime and current EXECUTE admission, the shared index host
  invocation borrows the admitted catalog's canonical procedure and epoch,
  never a retained preparation epoch. This invocation-local bridge neither
  mutates the stored selection nor authorizes an otherwise stale reference.
- **tablefunc** (`tablefunc_provider.go`, `tablefunc_stage.go`): trusted/relocatable
  version 1.0 retains strict volatile `normal_rand(integer,float8,float8)` at
  slot zero and four strict stable SETOF-record connectby overloads at slots
  one through four. Single-source `crosstab(text)`, its ignored-integer overload,
  `crosstab2`/`crosstab3`/`crosstab4`, and the appended slot-sixteen
  `crosstab(text,text)` are STABLE, STRICT and set-returning. The two-text form
  authenticates `crosstab_hash`; the other crosstab declarations use `crosstab`.
  Generic calls use caller-owned record definitions, not provider OUT parameters;
  each fixed wrapper returns its genuine standalone text composite.
  One sealed seventeen-declaration recipe contains eleven procedures, three named
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
  distinguished by their own scalar signature. The immutable recipe appends 168
  INTERNAL comparison routines and their 168 operators over exactly 28 ordered
  role pairs, seven checked conversion routines, eight INTERNAL comparators, one
  B-tree family, eight default same-type classes and twenty real casts. The
  unchanged 404-declaration prefix is followed by eight `is_valid`, eight
  `make_valid` and two `isn_weak` declarations (422 total). Validity/repair are
  immutable, strict C routines; the zero-input weak getter is stable/parallel
  safe and the Boolean setter volatile/parallel unsafe. Both are strict.
  Class-owned strategy/support rows are not extension declarations. Codecs reject
  these noncodec roles. Signature authority uses
  sealed scalar slots and one installation cohort, independently of current
  membership or original-header lifetime. Owner metadata serializes every formal
  and result symbolically; import remaps both sides of checked conversions.
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
  `geo_distance(point,point)` and its self-commuting `<@>` operator. Slots0–9 stay
  fixed; earth_distance and earth_box append at10/11 in the twelve-member recipe.
  Explicit operation/slot mapping skips the point pair. Operator admission
  validates its exact slot, formals, result and backing procedure against the
  current manifest. `Stage.EarthChecks` attaches the
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
  host a provider installs through: immutable base `Read`, coherent candidate
  `CurrentRead`, staged-only `Get`, name lookup, checked `Create`/`Replace`,
  allocation, array and creation ACL helpers. Genuine prerequisite lookups use
  `CurrentRead`, never staged-only `Get`; reading it does not change the base.
  `ObserveNamespace` and `ObservePrerequisite` retain
  the schemas and incumbent installations consulted by the statement.
  `ScriptFunctions(owner, ext, helpers)` resolves the target and actual direct
  prerequisite schemas under the script principal, retaining implicit
  pg_catalog priority and excluding the caller's TEMP routines.
  Closed Earth CHECK kernels capture genuine cube helper declarations through
  the sealed base-type cohort. Names and current e-membership do not select
  their roles; ordinary stored reference/manifest validation retains authority.
  `Installer.Stage(Stage, ext, owner)` constructs the base version; `StagePath`
  executes the provider's explicit update path.
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
  version). `DropTargets` selects DROP EXTENSION's targets (notices through a callback).
  CREATE owns one candidate for the entire depth-first prerequisite closure.
  Dependencies use their own default versions, inherit only the original
  explicit schema and CASCADE, and reuse installed prerequisites unchanged.
  Each provider resolves its own control/default namespace before its script
  privilege boundary; untrusted-script failure discards newly staged trusted
  dependencies too. Schema overrides do not bypass explicit-schema existence
  checks. Recursion tracks active names and rejects cycles without publication.
  `ImportOwners` applies Restore's owner/installed-procedure ACL
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
  `RemovalRoots` preserves the deduplicated requested-root prefix for both
  DROP EXTENSION and DROP OWNED. Each appended authentic member block follows
  released dependency visitation (OID descending, catalog and unsigned subobject
  ascending), not recipe slot order: shared consumers retain their first actual
  dependency parent. Expansion never sorts caller roots or published manifests.
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
  (`ValidateExtension`) runs later, outside the lock. Provider member order and
  diagnostic precedence remain release-defined; dynamic allocator values are
  identities, not expected numeric sequences.
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

Sampling provider units use `RUN='^TestSystem(Rows|Time)'` over genuine installations,
cross-catalog authority, declaration forgeries and authentic rename/relocation.

`TestRegisterAddsOnlyValidImplementations` pins `Register` (nil registry
allocated; duplicate, nil, versionless or badly named registrations rejected
with the registry unchanged). Pure provider identity and owner-codec units use
unpublished catalog states; composed installation, conflict and owner-import
witnesses live in `internal/engine/schemacmds`.
`TestExtensionRemovalRootsTraversalOrder` in core's extension enginetests pins
mixed requested roots, duplicate suppression, immutable authentic manifests and
the selected operator parent of a shared type/procedure/operator view consumer.
Public SQL lives in
[reference/extensions](../../../reference/extensions/README.md#testing) (extension
DDL and catalogs),
[reference/extension_types](../../../reference/extension_types/README.md#testing)
(lo, ltree and ISN values),
[reference/extension_hstore](../../../reference/extension_hstore/README.md#testing) (hstore),
[reference/extension_cube](../../../reference/extension_cube/README.md#testing) (cube, earth),
[reference/types](../../../reference/types/README.md#testing) (citext) and
[reference/locking_transactions](../../../reference/locking_transactions/README.md#testing)
(pgrowlocks). Add `REFERENCE=1` for the oracle.
Freespace layout, released identity and composed capture ownership use
`RUN='^TestPgFreespace'`.
Visibility provider and expression ownership use `RUN='^TestPgvisibility'`.
Surgery declaration, remapping and prepared-owner authority use
`RUN='^TestPgsurgery(Freeze|Kill)Provider'`.
Pgcrypto formal/kernel, authenticated OUT/source, retained definition and remap
ownership units use `RUN='^TestPgcryptoArmor'`; incumbent provider units use
`RUN='^TestPgcrypto'`.

Integer dictionary kernel, provider, publication and index-owner units share
`RUN='^TestDictInt'`; the public family is owned by `reference/extensions`.
Extended synonym kernel/options and installed identity/publication/remap owners
use `RUN='^Test(XsynDictionary|DictXsyn)'`; public scenarios remain in
`reference/extensions`.

Autoinc installation and forgery units in `enginetests` use
`RUN='^TestAutoincProviderIdentity$'`; its public scenarios live in `reference/operators`.

Identity-remap units retain genuine physical TEMP owners and derive collision
targets from actual imported identities. The earth control fixture allocates
namespace pairs until an observed identity collides, failing if allocation
passes the target. Replacement must remint the imported domain while preserving
the retained TEMP descriptor; incidental prerequisite-member counts are not
collision evidence.
Recursive candidate, script-path and publication ownership units use the
`RUN='^TestExtensionDependency'` family. Public closure, metadata, diagnostics
and logical-control witnesses live in `reference/extension_cube`.

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
