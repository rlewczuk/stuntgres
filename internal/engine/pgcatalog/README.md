# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the producer-backed `information_schema` tables and views (`sql_features`,
`sql_sizing`, `sql_implementation_info`, `sql_parts`, `table_privileges`,
`data_type_privileges`, `user_mappings`, `user_mapping_options`, `key_column_usage`,
`constraint_column_usage`, `column_column_usage`, `column_domain_usage`,
`routine_sequence_usage`, `routine_routine_usage`,
`check_constraint_routine_usage`, `check_constraints`, `referential_constraints`,
`character_sets`, `collations`, `schemata`, `column_options`, `transforms`, `enabled_roles`, `view_table_usage`, `triggers`, `domain_constraints`, `table_constraints`,
`sequences`, `role_column_grants`, `role_table_grants`, `role_routine_grants`, `routine_privileges`,
`role_udt_grants`, `role_usage_grants`, `usage_privileges`, `columns`, `domains`, `user_defined_types`, `views`, `tables`, `routines`). Each producer is a pure
function from a captured catalog, a paired role read and a narrow runtime
capability to rows. Orchestration (per-OID catalog choice,
unlock timing, wrapping rows as query sources) stays in the engine; see the
[engine README](../README.md).

## Imports

Layer L5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `catalog`, `authorization`, `functions`, `largeobject`, `lock` (lock
records), `expr`, `ids`, `settings`, `sqltype`, `sqlstate`, `parser` (catalog
descriptors store parser enums) and the standard library. It never imports the
root engine, `access`, `storage` or `visibility`.

## Producer contract

```go
type Input struct {
    Context context.Context
    Catalog catalog.Read
    Roles   authorization.Read
    Owner   *catalog.Owner
    Ref     ids.ObjectRef
    Source  *catalog.Relation
    Runtime Runtime
    TypeNamePath []ids.ObjectAddress
    Keep    Keep // {Column int; Test func(sqltype.Value) bool}
    RoleSettings RoleSettingsProjection
}
type Producer func(Input) ([][]sqltype.Value, error)
```

- `pg_index.indisclustered` projects the captured index definition's preference
  bit as a non-null BOOL with stable PostgreSQL attribute number 10. Projection
  ordinals remain independent of stable catalog attributes; the vector columns
  retain attributes 16–19 and their captured typed values.
- `pg_attribute` composes effective stable-attribute storage/compression with
  immutable type facts, including scalar dropped history. Index opclass storage
  types substitute representation facts without erasing simple-key policies.
  Default compression is the non-NULL empty character value.
- Text-search rows project actual descriptor ownership, namespace, template
  helpers and ordered options. Empty dictionary options are typed SQL NULL;
  numeric nodes remain unquoted and strings use canonical SQL escaping. Builtin
  dictionary/template identities are pinned; standalone dependencies follow their
  actual namespace/template rather than extension-name conventions.
  `pg_ts_config` projects real configuration owner/parser/namespace identities;
  `pg_ts_config_map` derives ordered one-based dictionary rows from those same
  immutable descriptors. It has no separately mutable map-row store. Both
  catalog sources retain their real row types and read-only SQL admission.
  Configuration dependencies follow current mappings even for a seeded
  configuration; only genuinely pinned referenced identities are omitted.
  Mapping token IDs belong to the configuration's actual parser; dictionary
  occurrences, including repeats, retain their one-based list positions and
  actual dictionary references. Sequence order is semantic, not a guarantee of
  SQL row order without `ORDER BY`.
  Capture contracts include both simple and English configurations, their full
  thirty-eight mapping rows, and simple/Snowball template and dictionary seeds.
  Allocated seed identities are checked through actual catalog relationships;
  retained captures keep their original source-owned types across replacement.
- `pg_event_trigger` projects actual database-local definitions from the immutable
  captured catalog and paired role authority, retaining trigger, owner and
  function identities. Unfiltered tags are SQL NULL; filtered tags retain their
  order, and disabled definitions remain visible through ordinary catalog reads.
  Reads neither execute referenced functions nor manufacture definitions.
  `evttags` is a nullable one-dimensional text array, consistent with its
  source-owned `pg_attribute` facts.
- `pg_trigger` (2620) projects one row per actual immutable relation-owned
  definition, including row/statement timing, event bits, real function and clone
  identities, ordered UPDATE attributes and zero-terminated argument bytes.
  The represented ordinary definitions are enabled `O`, noninternal and
  nonconstraint; absent transitions and WHEN remain typed NULLs. Persistent
  FK-owned RI triggers retain actual identities, INTERNAL constraint links,
  opposite relation/index, action-selected functions and AFTER ROW events.
  Check slots and NO ACTION slots project the FK's persistent timing defaults;
  other action slots remain nondeferrable. Empty args/vector and absent
  WHEN/transitions remain distinct from ordinary user definitions. Capture
  validates source/type generations and paired catalog/role authority even empty.
  Ordinary catalog PUBLIC SELECT applies independently of target privileges,
  WHEN disclosure, search_path and other-session TEMP ownership. Catalog ACL
  revocation remains authoritative. This is not information_schema filtering.
  Checked WHEN carriers retain their actual immutable payload elements and native
  logical structure without evaluation. Semantic `pg_get_expr` validates OLD/NEW
  namespaces before relation lookup and uses the supplied relation as context.
- `pg_statistic_ext` (3381) is a real PUBLIC-readable nine-column definitions
  catalog. Its authenticated source projects actual immutable CREATE STATISTICS
  definitions with current-generation schema, owner and input relation links.
  Ordinary tables and ANALYZE never manufacture definitions; the definition
  source is independent of collected immutable metadata.
  `stxkeys` is an INT2VECTOR of table attribute identities, `stxkind` an internal `"char"`
  array, and nullable `stxexprs` a PG_NODE_TREE expression-tree list, not SQL.
  Expression lists come from retained immutable bindings and deparse through
  stable input identities, so column rename changes presentation, not recipes.
  `pg_depend` projects AUTO simple/input-column edges and a whole-table AUTO
  edge when there are no simple keys; external expression providers have NORMAL
  dependencies, with ordinary pinned omission.
  `stxstattarget` is nullable, not a substituted -1 default. Collected metrics
  belong exclusively to `pg_statistic_ext_data`, not this source. Index presence
  is a catalog fact, not a claim of physical backing indexes. Even an empty
  capture requires the catalog's owner, reference domain and epoch, exact
  relation address (no subobject), and owner-bound role authority to agree.
  Every projected definition also requires its exact owner principal in that
  captured role root; a same-name recreated role cannot authorize an old row.
  Retained captures remain valid with their original catalog generation and
  captured roles. Common ACL, capture and guarded-mutation admission remain
  authoritative.
- Restricted `pg_statistic_ext_data` (3429, row10033/array10032) projects the same
  canonical ANALYZE metadata roots through the authenticated runtime bridge.
  Its six exact fields are stxoid, stxdinherit, stxdndistinct, stxddependencies,
  stxdmcv and stxdexpr; there is no oid column. Joint ndistinct, measured
  dependencies, joint MCV and automatic per-expression facts project from one captured fact.
  `stxdexpr` is a genuine typed pg_statistic[]10028 with FLOAT4 fractions,
  distinct encoding, MCV values/frequencies and ordered histogram values.
  Slot operators come from actual catalog families: MCV equality uses the
  default btree equality member or an equality-only type's default hash member;
  histogram ordering independently uses the default btree less-than member.
  ANYARRAY slots retain actual typed arrays; ordinary subscript/field access
  and explicit text output do not discard element authority. Unrequested or
  all-zero dependencies are genuine typed NULLs. Joint MCV is a registered
  pg_mcv_list value with real NULL flags, joint frequencies and full-population
  marginal baselines, or a genuine typed NULL when absent.
  Bootstrap/default catalog ACL has no PUBLIC SELECT grant;
  owning the table or statistics grants no reader privilege. Current catalog
  SELECT is checked even for WHERE FALSE and on every held-statement execution.
  An explicit catalog grant permits other owners' data too; revocation affects
  the same held statements immediately. Runtime capture pairs the authenticated
  catalog/role roots with their backing engine owner, without excluding valid
  transaction-private catalogs. No row exists before successful nonempty
  collection. Retained rowsets own immutable values across reanalysis, definition
  removal and native replacement; retaining a rowset does not authorize a later
  statement execution.
  The canonical restricted-result pg_statistic composite participates in type ACL
  projection through its validated type descriptor, without a fabricated
  relation reader. Ordinary unbacked composites remain authority errors.
- `pg_subscription` (6100, row6101/array10112) is a real shared catalog with
  eighteen owned typed attributes and an explicit owner-only table ACL with
  all table privileges and no grant options. PUBLIC has column SELECT on the
  seventeen nonsensitive fields, not table SELECT or access to `subconninfo`.
  The common used-column manifest checks current Execute authority before filtering or
  LIMIT elimination, including predicates, sorting and whole-row references.
  Explicit column/table grants and inherited privileges remain authoritative;
  held statements immediately observe revocation. The authenticated producer
  validates exact catalog/role roots, engine ownership and type generations
  and projects the immutable shared subscription descriptor index, including
  private read-your-writes. Empty catalogs return a nonnil empty result.
  Reads neither populate definitions nor perform publisher connections, slot
  creation, worker activation or synchronization.
  Shared and index-presence facts describe catalog identity, not fabricated
  native physical indexes.
- `pg_subscription_rel` (6102, row10114/array10113) is a real database-local
  catalog with four attributes: OID subscription/relation identities, internal
  CHAR state and nullable PG_LSN synchronization position. Its explicit table
  ACL contains all owner table privileges plus PUBLIC SELECT, without grant
  options or column grants. Current Execute admission governs every held read.
  The producer authenticates exact catalog/role roots, backing engine and owned
  type generations, returning a nonnil empty rowset because no subscription
  mappings exist. Cancellation and invalid authority return no rows. Reads
  neither populate mappings nor simulate synchronization or contact publishers.
  Index presence is metadata, not a fabricated native physical index.
- A producer returns rows and nothing else. It never builds a query source,
  keeps a statement scope or takes a lock; the engine wraps the rows.
- A successful capture with no rows returns a non-nil empty slice (an actual
  empty anonymous source); nil is never a substitute. Cancellation or invalid
  authority returns nil rows and the error, never a prefix.
- Rows follow the column order of `Input.Source`, each cell typed by that
  column's descriptor. Producers check the expected shape and types and fail
  otherwise.
- Producers check `Context` between descriptors and return its error.
- `Keep`, when set, tests one key column's cell of a candidate row; the
  producer omits the rows it rejects, which the statement's own filter rejects
  anyway (`exec.CatalogSourceKeep`). `KeyColumns` lists them: `pg_settings.name`,
  `pg_attribute.attrelid` (one test per relation) and `pg_class.oid` or
  `relname`. Captures made with `Keep` are never shared.
- Rows are immutable once returned: the engine may share one capture across
  statements with the same inputs, so neither producers nor consumers write
  into returned rows.
- Both `pg_proc` producers share all30 release-ordered columns; projection
  positions equal released attribute numbers minus one. Security, settings,
  support links, leakproofness, transforms, source and binary provenance come
  from immutable canonical declaration owners, not public names or execution
  admission. Configuration is a typed nullable, ordered one-based TEXT array.
  Lookup-only supplements retain their genuine released defaults and never
  gain callable providers. SQL-backed routines require an actual language
  descriptor; native Go callbacks retain their native-only language0 sentinel.
  Builtin/provider set capability remains intrinsic; estimates consume canonical
  positive `Procedure.Rows` overrides, otherwise retaining genuine released or
  installed defaults. Scalar estimates remain zero.
- Persisted configuration projections borrow the statement's complete immutable
  role record through `RoleSettingsProjection`, paired with its role authority.
  `pg_db_role_setting` enumerates nonempty scope rows with actual database/role
  identities (zero means ALL); role-only rows supply `pg_roles.rolconfig`,
  `pg_user.useconfig` and authorized `pg_shadow.useconfig`. Arrays own their row
  backing. Providers and live overrides never enter these projections.
  `pg_settings` instead uses the physical owner's captured typed reset image and
  source, overridden only by live session source; `role` is not an ordinary GUC.
- **Provenance.** `Input.Owner` is the engine's stable `*catalog.Owner`; its
  `Domain`, `Principals` and `ACLAuthority` are compared as the same pointers,
  never as equal names or numbers. `Catalog.Owner()`, `Ref.Domain()`,
  `Roles.BelongsTo(Owner.Principals)` and `Source` identity must agree before
  rows are produced.
- `pg_auth_members` is a PUBLIC-readable real catalog descriptor projecting every
  captured direct membership edge: four nonnull OIDs and three stored Boolean
  options. It does not filter by LOGIN or reader, expand closure, or manufacture
  implicit memberships. Stored row IDs are allocated by role publication, never
  by reads. Schema/relation/column ACL admission remains in the common path;
  restricted `pg_authid` credentials remain independent.
- `pg_shmem_allocations` and `pg_shmem_allocations_numa` materialize a fixed,
  nonempty virtual 1 MiB profile, not process memory or host measurements. The
  named identity `stuntgres simulated arena` is a simulation label. Allocation
  `off`, `size` and `allocated_size`, and NUMA `numa_node` and `size`, are all
  approximate physical fields. A 4096-byte anonymous reservation precedes the
  named 65537-byte request, rounded to 65600 bytes at 64-byte alignment; the free
  frontier is 69696 with 978880 bytes remaining. Anonymous offset and free name
  are NULL. The named range covers 17 virtual 4096-byte pages (69632 bytes),
  assigned to simulated node 0; its NULL-node bucket is present with zero bytes.
  Neither the node nor page size describes host topology or residency. No arena
  is allocated, no provider is installed and workloads do not alter the profile.
  Schema/type/catalog identities and current-role ACL admission remain exact:
  bootstrap owner privileges and pg_read_all_stats SELECT are seeded through
  central authority, with no PUBLIC grant. Reads also require current EXECUTE
  on their actual pg_get_shmem_allocations/pg_get_shmem_allocations_numa backing
  declarations. These retain exact OUT metadata and bootstrap/pg_read_all_stats
  EXECUTE ACLs without a callable provider. View SELECT alone is insufficient;
  both admissions precede materialization. Captures own fresh typed row backing.
  Both read-only builtin definitions use the common identity-aware fragments
  for pg_views, pg_get_viewdef and information-schema consumers; default and
  pretty forms agree and retain actual search-path/identifier-quoting rules.
- `pg_authid` projects all roles and their direct typed attributes, genuine stored
  passwords and expirations from the immutable statement role frontier.
  Its real-table descriptor has no PUBLIC SELECT authority. `pg_shadow` uses the
  same captured role frontier but includes only LOGIN roles. Missing passwords,
  expirations and role-only configuration remain typed SQL NULL. The common
  relation ACL/column manifest
  and stored-view owner/invoker checks authorize access, including genuine
  delegated authority; the producer creates neither credentials nor permissions.
  Named GRANT and REVOKE validate the original captured source registration before
  ordinary immutable table/column ACL planning. Delegation, option-only removal,
  selective cascading and transaction rollback use that same ACL authority;
  neither operation changes an independent backing-source ACL.
  Public `pg_user` and `pg_roles` keep independent constant password masks.
  The `pg_shadow` password column preserves the source `pg_authid` C collation (OID 950),
  unlike the database-default collation of `pg_user`'s masked text literal.
- `column_domain_usage` projects seven nullable `sql_identifier` columns from
  live positive attributes of actual ordinary, view, foreign and partitioned
  relations, including provider-backed views and temporary relations. Each
  column contributes only its direct declared domain, never domain ancestors,
  array elements or expression references. Captured enabled domain ownership
  alone determines visibility; domain USAGE, relation ACLs and schema access
  are not substitutes. Names and source-owned NAME/domain generations come
  from the paired immutable catalog/role capture. Cancellation or malformed
  authority discards every row, while successful empty captures stay typed.
- `pg_ts_template` projects only captured, installed executable template
  descriptors. Its five nonnullable OID/NAME/OID/REGPROC/REGPROC columns retain
  the actual namespace and helper procedure identities; absent initialization
  is the non-NULL REGPROC identity zero. Installed unaccent descriptors own
  availability, independently of lookup-only routine identities and available
  modules. The empty source remains fully typed. Reads neither install or
  execute providers nor allocate identities; extension publication owns visibility.
- `pg_ts_dict` projects only captured, installed dictionary descriptors, with
  their actual namespace, owner and template identities. The stock unaccent
  provider supplies the exact `rules = 'unaccent'` initialization text; options
  remain nullable catalog text. Extension installation/removal and transaction
  publication own row visibility; catalog reads allocate no identities and
  neither install nor execute a provider. `pg_available_extensions` advertises
  module availability independently of installed dictionary rows. Dictionary
  execution remains owned by the extension/FTS provider, not this projection.
- `pg_ts_parser` projects current seeded and user immutable parser descriptors
  once, through provider-checked OID/NAME/REGPROC authority. Optional HEADLINE
  is a nonNULL REGPROC zero; no owner or ACL is synthesized. Namespace and
  nonzero callback dependencies use ordinary pin suppression. The bounded stock
  handler pg_proc supplement remains independent of parser definitions and
  confers no standalone execution provider.
  `pg_proc.proretset` comes from actual routine declaration metadata.
  Its full row includes FLOAT4 `prorows` and `procost`, declaration `prosrc`,
  OIDVECTOR `proargtypes` built from each routine's input signature, and OID `provariadic`.
  Routine cost preserves explicit immutable builtin declaration costs, falling
  back to one for C/internal and one hundred for SQL/PL declarations.
  SQL/PL formal names/types/modes come from `proc.Formals`; fixed native OUT
  types and complete names come from the actual builtin or installed declaration.
  Builtin input names have one immutable declaration owner. `parameters` and
  `pg_proc` share the ordered formal walk and name projection; argument count
  and identity remain input-only.
  `proallargtypes` and `proargmodes` retain all declaration slots in order through
  one typed-array materializer.
  `pronargdefaults` reads SQL/PL `Formals` through `DefaultCount`, or the captured
  builtin/installed declaration's default count; metadata-only internal routines
  have zero defaults. It never evaluates defaults or retains a separate registry.
  `proargdefaults` retains a genuine immutable expression list, sharing checked
  default acquisition with information_schema and semantic `pg_get_expr`
  rendering. SQL NULL defaults are present tree entries, unlike absent defaults.
  `prosqlbody` retains a checked SQL-standard program or immutable provider-owned
  parsed declaration recipe; quoted SQL/PL bodies remain NULL. Both fields use
  the native StructuralTree carrier, not PostgreSQL's nodeToString encoding.
  Capture neither executes nor recompiles any default, body or support callback.
  Returned tree values retain their original immutable owners after replacement.
  All-IN declarations return typed NULL arrays before allocating element slices.
  Non-SQL VARIADIC uses the existing `ProcedureVariadic` owner and terminal input
  slot, retaining its declared array/ANY type rather than the element OID.
  Generated constructor signatures retain their actual owning type generation.
  Setness and result shape never imply OUT parameters. SQL set declarations
  report `proretset` and their declaration row estimate independently of shape.
  Zero arguments produce an empty non-NULL argument vector. `provariadic`
  projects the selected builtin declaration, SQL variadic array element, range
  constructor, or owned aggregate's VARIADIC ANY identity; nonvariadic
  declarations report zero. Selected internal parser/vector-I/O metadata,
  `pg_ddl_command_recv(internal)` and the twelve RI trigger functions retain
  actual identities without installing callable providers. RI functions preserve
  their explicit volatile declaration; represented descriptors take precedence.
  Installed codec source symbols stay independent of mutable public names.
  Owned aggregate `proargnames` follows the canonical flattened declaration,
  including retained direct and ordered argument names; unnamed declarations
  retain NULL rather than an invented list.
  The twenty-two-column projection preserves these declaration fields, nullable
  ACLITEM[] `proacl`, and non-NULL INT2 `pronargdefaults`. ACLs use captured
  `Runtime.ObjectACL` and `ClassACL` authority. Implicit routine ACLs remain SQL
  NULL; explicit empty and granted ACLs remain real arrays. Internal metadata-only
  supplements retain NULL ACLs.
  Canonical array-compatible OIDVECTOR owns `proargtypes`; no separate carrier
  or scalar-only vector path is introduced. Released attribute identities stay
  `procost`6, `pronargdefaults`18, `proargtypes`20, `proallargtypes`21, `proargmodes`22 and `proacl`30
  independently of projection order.
  The fixed dropped-object helper declaration supplies its twelve OUT names and
  released cost and row estimate without evaluating its lexical event-trigger source.
- `pg_index` vector cells use the runtime's captured index metadata projection,
  then shared typed constructors: all key/INCLUDE attributes in `indkey`,
  key-only `indcollation`, `indclass` and `indoption`. No textual vector store or
  duplicate index-class/option derivation belongs to the catalog producer.
- Absent and explicit-empty ACLs stay distinct: `ClassACL` returns NULL for an
  absent ACL and `{}` for an empty one.
- Information-schema producers emit genuine source-owned domains, not bare
  primitive carriers. `table_privileges`, `role_column_grants`, `role_table_grants`,
  `role_routine_grants`, `routine_privileges`, `role_udt_grants`, `role_usage_grants` and `usage_privileges` use `sql_identifier`,
  `character_data` and `yes_or_no` from the captured type catalog.
  Numeric metadata likewise retains genuine `cardinal_number` domain values.
- `triggers` projects real relation-owned definitions through a captured stored
  presentation visitor. Name-order ranks partition by event, timing and
  orientation; TRUNCATE contributes no information-schema event row.
  Enabled ownership or non-SELECT table/column write/reference/trigger privileges
  expose rows; only enabled owners see WHEN expressions. Target-schema USAGE
  is not required; other-session temporary relations are excluded. All seventeen
  cells retain source-owned domains, including NULL transition/creation fields.
  Reading metadata neither admits trigger declarations nor executes routines.
- `table_constraints` projects actual CHECK/NOT NULL, UNIQUE/PRIMARY KEY and
  foreign-key records on ordinary and partitioned tables. Same-name constraints
  retain their owning relation identity. Physical nullability, domain checks,
  indexes and exclusions do not manufacture rows. Its eleven nullable columns
  retain captured sql_identifier, character_data and yes_or_no domains, including
  a domain-typed NULL nulls_distinct outside UNIQUE.
  CHECK/FK enforcement derives from the independent stored enforcement bit in
  both pg_constraint and table_constraints; validation never supplies it.
  Deferrability and initial timing derive only from persistent definitions,
  as do `pg_constraint` flags and backing `pg_index.indimmediate`; transaction
  SET CONSTRAINTS modes never alter these catalog facts.
  The condensed `pg_index` source appends its nonnullable BOOL `indimmediate`
  without shifting established result slots; its stable catalog attribute is 9.
  It shares the existing vector projection's captured index metadata.
  Immediately usable ownership, non-SELECT/non-MAINTAIN table
  privileges or INSERT/UPDATE/REFERENCES on any live column disclose all table
  constraints; schema USAGE is not required. Temporary rows belong only to the
  captured session. One paired immutable catalog/role frontier owns every cell
  and visibility decision, with cancellation discarding the whole result.
- `check_constraints` projects four nullable source-owned domains from actual
  table/domain CHECK and NOT NULL definitions. Enabled owner-role authority,
  not data access or type USAGE, determines visibility. CHECK rendering uses
  the captured default non-pretty stored-expression deparser; NOT NULL uses
  the raw live stable attribute name or VALUE. Identical projected tuples
  retain UNION ALL multiplicity. The paired catalog/role/presentation frontier
  supplies both typed relation and validated owning-domain rendering.
- `transforms` has eight nullable source-owned domains: seven `sql_identifier`
  columns and one `character_data` column. Its inventory, like `pg_transform`,
  is truthfully empty because no implemented objects own transform definitions;
  ordinary types, languages, codecs and SQL routines never synthesize rows.
  The bootstrap owner and ordinary schema-USAGE/view-SELECT admission govern
  reads. There is no default PUBLIC SELECT grant and no row privilege predicate.
  Its released definition retains the direction joins, including explicit OID
  coercions of the regproc direction references, and UNION; automatic INSERT,
  UPDATE and DELETE are rejected by the shared view path.
- `role_column_grants` captures one catalog/role frontier and projects actual
  table/column ACL origins on live positive attributes. Its raw-origin UNION
  includes the grant-option bit before ownership determines grantability, so
  identical final rows can retain multiplicity. Visibility uses enabled grantor
  or enabled named grantee, including PUBLIC origins with an enabled grantor;
  it does not reuse table/column data-access predicates. Membership closures
  and grantee/owner authority are cached within that capture.
- `table_privileges` and `role_table_grants` share a projector over real relation
  ACL origins, including zero-column relations, without schema-USAGE or
  data-readability filters. Both expose enabled grantors or named grantees;
  `table_privileges` additionally exposes every PUBLIC origin. The sister view
  requires an enabled grantor for PUBLIC. An actual current superuser sees all.
  INHERIT closures and grantee/owner authority use one captured frontier.
  Absent ACLs supply seven owner origins; explicit-empty ACLs supply none.
  MAINTAIN, column ACLs and partition-parent expansion do not contribute rows.
  Grant options or immediately usable owner authority determine grantability;
  only SELECT has hierarchy.
- `routine_privileges` and `role_routine_grants` share one EXECUTE ACL projector
  over actual Procedure descriptors of every represented kind, without inspecting
  or executing bodies or requiring schema USAGE. Both expose enabled grantor or
  named-grantee origins; `routine_privileges` additionally exposes every PUBLIC
  origin. The role view retains PUBLIC origins when their grantor is enabled.
  Absent ACLs supply owner and PUBLIC origins; explicit-empty ACLs supply none.
  Grantability belongs to the named grantee through its grant option or immediately
  usable owner authority, never the observer's authority; PUBLIC is not grantable.
  One captured catalog/role frontier owns domains and lazy membership/ownership
  caches. Specific names retain the actual routine OID within NAME's UTF8 boundary.
- `role_udt_grants` projects raw type USAGE origins as `TYPE USAGE` for actual
  reciprocal composite row types, including lookup-only catalog rows. Arrays
  and domains do not contribute. Synthetic default ACLs retain raw absence;
  absent ACLs supply owner/PUBLIC origins and explicit-empty ACLs supply none.
  Enabled grantor or named grantee determines visibility, without schema or
  relation access filters. Grantability belongs to the named grantee, using
  captured grant options and immediately usable ownership authority.
- `usage_privileges` and `role_usage_grants` share an eight-column, nullable
  domain-typed projector over real domains, sequences, collations, foreign
  wrappers and foreign servers. Both preserve USAGE origins and multiplicities,
  applying enabled grantor-or-named-grantee visibility without schema,
  data-readability, TEMP or identity filters. `usage_privileges` also exposes
  every PUBLIC origin; the role view requires an enabled grantor for PUBLIC.
  Named-grantee grant options or immediately usable ownership determine
  grantability, never observer ownership. Raw-NULL domains supply owner/PUBLIC
  origins; wrappers and servers supply the owner origin. Raw-NULL sequences
  supply none, following the released table-default fallback; explicit-empty
  ACLs never create origins. Compatible collations supply owner-to-PUBLIC only.
  Foreign objects have empty schema names. Captured catalog, roles and domain
  authorities remain stable, with cancellation discarding the entire result.
- `pg_type` composes user-domain facts through exact captured immediate-base
  providers, including nominal-only domains and reciprocal arrays. Metadata
  admission does not grant executable codec or storage authority; foreign
  same-OID base generations are not interchangeable.
  Installed scalar I/O identities come from each exact scalar's own position in
  its provider-validated manifest, checked against formal argument/result types.
  Procedure language OIDs derive from the descriptor's existing declaration
  metadata; display language grants no execution capability.
- `routines` reads only actual captured Procedure descriptors. Immutable provider
  display facts are separate from execution-language authority. Enabled ownership
  or effective EXECUTE exposes the row; only enabled ownership exposes source,
  while C link symbols remain visible to executors. Declared result identity is
  not domain-unwrapped and does not admit a codec or execute a provider. All 82
  cells, including NULLs, retain their captured information-schema domains.
- `parameters` expands actual declaration formals of every represented routine
  language and kind, never invocation operands or SQL PREPARE placeholders.
  All 32 cells retain their captured nullable domains, including NULLs.
  Enabled ownership or effective EXECUTE admits rows without schema/type USAGE;
  only enabled ownership discloses default expressions. OUT/TABLE positions
  count in ordinals but never shift input-indexed defaults. Declared type
  classification shares routines' generation checks without codec admission.
  Checked defaults and finite builtin/installed literal recipes are rendered
  through captured `stored.Deparser` presentation, never evaluated or replay
  serialized. Cancellation or malformed authority publishes no partial rows.

`user_defined_types` traverses actual standalone composite relations and validates
their reciprocal type descriptors before applying immediately usable ownership
or effective type USAGE. Schema, member-type and relation-read privileges do not
gate disclosure. All 29 cells retain captured information-schema domains,
including the fixed-width `yes_or_no` carrier and the 24 typed NULL tail fields.
Each capture owns detached rows; malformed metadata or cancellation publishes
none. Its read-only builtin definition uses the shared explicit spelling parts
for both view-definition consumers, independently of query execution.

### Wait-event definitions

`pg_wait_events` exposes three nullable TEXT columns, `type`, `name` and
`description`, with PUBLIC SELECT. Its complete inventory describes implemented
blocking mechanisms; it is independent of active sessions, queues and wait counts.
The definitions remain present when no operation is blocked.

| type | name | description |
| --- | --- | --- |
| IPC | SafeSnapshot | Waiting to obtain a valid snapshot for a READ ONLY DEFERRABLE transaction |
| Lock | advisory | Waiting to acquire an advisory user lock |
| Lock | object | Waiting to acquire a lock on a non-relation database object |
| Lock | relation | Waiting to acquire a lock on a relation |
| Lock | transactionid | Waiting for a transaction to finish |
| Lock | virtualxid | Waiting to acquire a virtual transaction ID lock |
| Timeout | PgSleep | Waiting due to a call to pg_sleep or a sibling function |

Descriptions contain no final periods. A static capture uses only the owned
catalog descriptors, allocates detached typed rows and never samples runtime
wait state. The builtin view's `pg_get_wait_events()` display reference grants
no executable routine capability.

There are no definitions for distinct physical tuple arbitration, speculative
insertion tokens or other unimplemented lock resources. PostgreSQL storage,
buffer, I/O and LWLock mechanisms, replication/recovery/parallel/background
processes, extension/injection points, socket/client and backend-termination
waits are unavailable mechanisms and are not advertised as runtime events.

### Schema inquiry

`schemata` enumerates actual captured namespaces once, using the paired role
descriptor for the owner name and the runtime's actual current database name.
Immediately usable owner authority or any effective schema CREATE/USAGE exposes
the row. Direct, inherited and PUBLIC rights use existing namespace authorization;
table/type/routine rights and search-path inclusion do not reveal namespaces.
There is no name-based or other-session TEMP exclusion.

All seven columns are nullable source-owned domains: six `sql_identifier`
columns followed by `character_data`. The default-character-set fields and SQL
path are typed SQL NULL, not empty strings or synthesized settings. Reads never
install namespaces. Domain/base generations, namespace bindings and owner
authority are validated before projection; cancellation or malformed authority
discards the entire capture. Retained rows and descriptions remain immutable.
The released builtin display definition uses the common identifier, type,
relation and routine spelling tokens, independently of executable query storage.


## Referential constraints

`referential_constraints` projects actual foreign-key definitions from one paired
catalog/role frontier. Its six identifier and three character-data cells retain
captured domain authority, including the fully NULL referenced-constraint triple
when a standalone unique index supports the FK. Referenced constraint identity
requires both the target relation and its actual supporting index; duplicate
names and identical final tuples never collapse. The referencing table's enabled
owner, qualifying table rights or any live positive column's qualifying rights
determine visibility, independently of SELECT, schema access, target access and
TEMP namespace ownership. Capture cancellation discards the entire result.

## SQL feature metadata

`info_sql_features.go` exposes the complete PostgreSQL 18 release conformance
matrix from `info_sql_features_data.go`, independently of native SQL execution
capabilities. Released support flags and comments are literal metadata, not
inferred from parser admission, catalog population or parent/child features.
The relation is a physical catalog-backed table with seven nullable
`character_data`/`yes_or_no` domain columns, typed NULL verification values,
genuine empty strings and PUBLIC SELECT. Ordinary schema/relation ACL admission
still applies; there is no per-feature identity or object visibility filter.
Each capture owns its output buffers and captured domain descriptors; process
data contains only strings. Cancellation or invalid capture authority returns
no partial matrix.

`info_sql_sizing.go` owns a small standardized sizing inventory and captures four
nullable `cardinal_number`/`character_data` columns. Positive values describe
enforced native bounds; zero is unlimited or undetermined, while typed NULL
marks unsupported sizing items. Character-set comments accompany only actual
identifier byte bounds. The physical catalog table uses ordinary schema/relation
admission and PUBLIC SELECT, without per-item identity or object filtering.
Captures validate both domains against the current base descriptor generations
and allocate detached rows; cancellation publishes no prefix.

`info_sql_parts.go` owns eleven fixed full-part conformance records, independently
of individual feature support and user objects. All support values are `NO`:
no entire SQL-standard part is fully implemented, including SQL/Foundation and
SQL/Schemata. Reading this table does not itself establish full-part support.
The physical table has five nullable `character_data`/`yes_or_no` domain columns,
typed NULL verification and non-NULL empty comments matching the release data.
Unlike the other conformance tables, its default ACL has no PUBLIC SELECT.
Ordinary schema USAGE and relation SELECT admission expose the complete matrix
to authorized identities, not an identity-filtered subset. Each capture validates
paired catalog/role authority and current domain/base identities, owns detached
row buffers and returns no prefix on cancellation or invalid authority.

## SQL implementation metadata

`info_sql_implementation_info.go` owns twelve stable initial product
characteristics, not feature-conformance population. The physical table has
five nullable `character_data`/`cardinal_number` domain columns. Integer zero,
empty character strings and typed NULLs remain distinct. DBMS NAME is
`stuntgres`; DBMS VERSION uses `functions.EmbeddedVersionBanner`, the same pure
identity as `version()`. Initial collation `C` and default isolation information
do not change with user collations or session settings.

Schema USAGE and relation SELECT admission apply normally; PUBLIC SELECT
exposes the complete bag without per-item identity filtering. Each capture
validates the catalog/role frontier and exact captured domains/base identities,
owns its row/cell buffers and publishes no prefix on cancellation or error.


## Registry

`catalog_source_producers.go` holds one process-initialized array of
`Registration{OID, Produce}`, sorted by relation OID and never changed.
`ProducerFor(oid)` finds a producer by binary search; `Registrations()` returns
a copy for coverage checks. `pg_locks` (`LocksOID`) and `pg_sequences` are not
registered: the engine handles those two itself.

`catalog_source_specs.go` holds `Specs`, the ordered descriptor metadata for
every system relation (OID, namespace, name, columns with type, typmod,
dimensions, nullability and collation, flags). Spec order is the descriptor
registration order and differs from the registry order.

`pg_policy` (3256) is a physical eight-column source over immutable
`Relation.Security.Policies`. Capture validates the exact source, captured type
generations and paired principal authority even for an empty catalog. It
preserves command/permissiveness, ordered duplicate role references and PUBLIC's
zero sentinel; only the two expression columns are nullable. Definitions are
PUBLIC metadata, not an actor's applicable-policy list. Target/provider execution
ACLs and RLS evaluation remain with their existing owners. `Runtime.PolicyTree`
retains the actual frozen policy/presentation without evaluating it; cancellation
discards the entire capture.

`pg_policies` (12018) projects that same collection as a read-only virtual view.
Its eight nullable descriptor columns retain the existing rowtype12020/array12019
association; role names and expression text are owned by the captured authority.


### Adding a system relation

1. Add a `Spec` to `Specs` at the position that keeps registration order.
2. Write a `Producer` in its own file, named after the relation.
3. Add `{OID, producer}` to the registry, keeping it sorted.
4. If the producer needs engine state, add a value-taking method to `Runtime`
   and implement it on the engine's `EngineCatalogRuntime`. If the engine must
   choose a different catalog, role read or unlock policy for the OID, add a
   case to `openCatalogSourceLocked` in the engine.

## Runtime capability

`Runtime` is the narrow interface the engine implements per call
(`EngineCatalogRuntime`). Methods take and return values, never engine structs.
Building the adapter reads nothing; only the selected producer calls the
methods it needs. Rule facts and view options are read from catalog payloads
directly (`Relation.HasRules`, `Relation.VisitRules`, `ViewPayload.Options`).
Policy definitions likewise remain relation-owned; `PolicyTree` bridges their
opaque frozen payload to the stored carrier, and `PolicyDisplay` validates that
same carrier before default-format rendering with captured caller presentation.
Neither bridge exposes its concrete owner here. Trigger definitions use
`VisitTriggerCatalog` to project checked value
facts and owned tree carriers from the actual relation payload; the distinct
`VisitTriggerDefinitions` retains information_schema presentation and disclosure.
Maintenance-aware projections need an adapter backed by a real engine;
an empty adapter is not an initialized maintenance capture.

`pg_prepared_statements` is captured fresh from the admitted physical session,
never cached by catalog revision. The engine releases its mutex before visiting
the registry. Borrowed signatures expire with each synchronous visit; the
producer creates private immutable regtype arrays and current timestamp/type
descriptors, retaining no definition, session or execution graph. No-result
commands have NULL result_types; zero-column results have an empty array.
Unsupported generic/custom chooser statistics remain typed bigint NULLs.

Policy producer authority and immutable structural ownership use
`make check RUN='^TestPGPolicy'`; add `RACE=1` for retained-output concurrency.
Policy-view projection, typed role arrays, authority, cancellation and retained
backing use `make check RUN='^TestPGPolicies'`.

| Methods | Why they are engine-owned |
|---|---|
| `CursorCount`, `VisitCursors` | session cursor registry (`pg_cursors`) |
| `PreparedStatementCount`, `VisitPreparedStatements` | current token's named SQL/protocol definitions, distinct from prepared transactions |
| `SessionSettings` | one coherent typed settings snapshot under the physical-session mutex (`pg_settings`) |
| `PreparedCount`, `VisitPreparedTransactions` | prepared-transaction registry, assigned XIDs, published role names |
| `TimezoneInput`, `DatabaseName`, `TemporaryOwner`, `CurrentPrincipal` | statement input environment, session database, physical TEMP owner, current user |
| `ValidateCapture`, `Effective`, `CanUse`, `SelectColumns`, `ForeignServerVisible` | error-bearing privilege checks through the statement's authorization view |
| `ValidateParameter` | settings registry validation of parameter ACL rows |
| `ValidateACL`, `ObjectACL` | ACL validation and raw object ACLs (`access` is not imported here) |
| `ValidateType` | type descriptor validation, including installed extension types |
| `TypeOutput` | regclass, regrole and aclitem formatters for type defaults |
| `Maintenance`, `DatabaseFrozenXID`, `VisitVacuumProgress`, `VisitCopyProgress`, `VisitCreateIndexProgress` | immutable logical maintenance facts and authenticated active-work redaction |
| `ExpressionTree` | structural `pg_node_tree` output of bound expressions |
| `PolicyTree`, `PolicyDisplay` | validated relation-owned policy carriers and current-identity display without evaluation |
| `VisitCheckAttributes`, `ExpressionReferences`, `ViewReferences`, `IndexReferences`, `PartitionReferences`, `IndexSimpleAttribute`, `IndexKey` | walks over engine plan and expression nodes |
| `ForeignKeyComparisons` | the binder's foreign-key comparison operators (`pg_constraint`) |
| `ViewDefinition`, `IndexDefinition`, `VisitRuleDefinitions` | current-identity stored definition rendering; rule visitor omits exact `_RETURN` before rendering |
| `ColumnExpression`, `ColumnUpdatable` | captured stored-expression display and metadata-only view/rule/foreign capability |
| `ViewCapabilities` | structural event and actual trigger facts, bridging stored views and FDW handler interfaces without query execution |
| `Pinned`, `OperatorSeeded`, `OperatorDependencies`, `TypeProviderProcedure` | pin decisions, the builtin operator inventory and operator dependencies owned by `routine`, extension type providers |
| `VisitSequenceDefinitions` | sequence definitions with the statement's journal overlay (`pg_sequence`) |

`VisitCopyProgress` supplies cluster-wide, command-lifetime facts captured under
the shared engine mutex. The COPY producer constructs owned typed rows, returning
a nonnil empty result when idle; retained rows never borrow live counters.
The shared progress viewer applies the observer's current inherited role closure
to the worker's authenticated principal. PID/database identity remains visible
when relation, command, endpoint and all five counters are masked to SQL NULL.
Authorized PIPE totals are zero. A genuinely missing database lookup yields NULL
datname; normal active workers retain their own database identity across observers.

`VisitCreateIndexProgress` supplies actual admitted CREATE INDEX and supported
REINDEX command facts, including concurrent waits and recursive partition work.
The sixteen-column `pg_stat_progress_create_index` producer retains only
pid/datid/datname for restricted observers; all thirteen remaining cells are
typed NULL. Authorized unknown/inapplicable counters are zero, not NULL.
Ordinary CREATE reports index OID zero; concurrent CREATE reports its published
index, and REINDEX its current existing target. Idle, finished, failed and
canceled commands have no progress row. Concurrent cancellation can retain the
ordinary PostgreSQL invalid-index artifact without retaining progress.

Build callbacks describe actual scan/sort work; frozen transaction cohorts
describe actual writer/snapshot waits. Native B-tree vector wrapping is not a
physical tree-loading phase. Only block counters approximate physical work:
one logical block is up to 128 captured storage row versions, completed only
after successful processing. Counters reset at real phase/target boundaries;
they are not globally monotonic. See the
[index-progress design](../../../docs/design/PG18-VIEW-PG-STAT-PROGRESS-CREATE-INDEX.md)
for formulas, supported command paths and independent public witnesses.

## Exported helpers

The engine calls `ClassKind`, `ClassACL`, `AttributeMetadata`,
`HasSystemAttributes`, `AttributeNumber`, `InitializeAttributes`, `RegisteredSource`,
`CollationActualVersion`, `DatabaseCollationActualVersion`,
`DescriptorComment`, `ForeignDependencies`, `VirtualViewWriteError`,
`LockRows`, `CapturePGSequences`, `NewProgressViewer` (which progress
rows the current user reads in full: superuser, the worker's authenticated
role or `pg_read_all_stats`) and the OID constants (`LocksOID`,
`CursorsOID`, `SettingsOID`, `TypeOID`, ...). Engine unit tests also call row
helpers such as `CollectDepend`, `ClassRow`, `AttributeRow`, `TypeFacts` and
`ConstraintOperator`. `CollationActualVersion` reads actual provider
authority, never the recorded `Version`.
`RegisteredSource` checks captured descriptor membership against `Specs`, including
namespace/name, catalog/view kind, stable attributes and catalog-owned column
types. Relation GRANT uses it before ordinary ACL planning; it does not authorize
catalog writes or bypass a provider's independent backing-source checks.
`CollectDepend` emits each direct extension-membership edge through
`catalog.ExtensionMembership`; per-class branches add only their other edges.

## Relations with special rules

- `pg_am`: live global method descriptors with owned typed cells per capture;
  family→method and method→handler dependencies use shared selectors, and
  references to pinned stock handlers are suppressed, not fabricated.
- `pg_depend`: repeated routine result/formal/support declarations share one
  normal declaration edge per referenced address. Checked standard-body and
  default-expression dependencies retain their separate capture-pass rows,
  including column/subobject identities. Materialization suppresses pinned
  prerequisites without globally deduplicating these independent passes.
- `pg_largeobject` (2613): actual immutable stored chunks, each borrowed chunk
  copied once into an owned BYTEA; holes are never materialized and per-object
  ACLs are not consulted (ordinary relation SELECT protects it).
- `pg_largeobject_metadata` (2995): object OID, creator's owner OID and raw
  `ClassACL`, without per-object filtering or redaction.
- `pg_policies`: projects all represented policy definitions under ordinary
  view SELECT authorization (PUBLIC by default), not target-object filtering.
  Captured caller visibility controls expression qualification. Current named
  targets are sorted and deduplicated into genuine NAME[]; PUBLIC is `{public}`.
  USING and CHECK are independently nullable and use the stored default renderer;
  reading definitions never evaluates conditions or enables row filtering.
- `pg_rules`: projects represented stored relation rules as NAME/NAME/NAME/TEXT,
  excluding only the exact `_RETURN` name. Definitions are public metadata:
  relation ACLs, schema USAGE, search path and another session's TEMP ownership
  do not filter rows. Captured presentation controls current-name SQL and own
  TEMP spelling; reads never authorize or execute rule actions.
- `pg_transform` (3576): checks cancellation, then the five carrier types, and
  returns a non-nil empty source.
- `pg_settings`: projects registry metadata and one coherent typed snapshot;
  with `Keep` (a SELECT filtering its own scan by setting name) it builds only
  the named rows;
  the engine filters restricted rows using captured effective-user INHERIT
  authority before acquiring the settings mutex. Boot/reset metadata preserves
  SQL NULL separately from empty current strings and simulated startup defaults.
  Explicit simulated startup assignments report `override`, not a loaded file;
  source locations remain NULL. SET authority does not imply restricted-row read
  authority. Captured provider defaults retain `client` provenance even when equal
  to boot, and RESET restores that captured typed value without invoking the
  provider again. The producer owns its enum-array buffers and never publishes
  partial canceled rows; empty captures retain the same 17-column carrier.
- Table-statistics and progress views: committed logical DML and manual VACUUM
  facts and real active phases with authenticated-owner visibility, captured
  under the engine capture lock. NULL/zero semantics are view-specific:
  unavailable CREATE INDEX totals are zero for authorized observers; restricted
  work details are typed NULL.
- `information_schema.user_mappings`: real mapping identities; server owner or
  effective USAGE exposes all mappings. It never copies options or reuses
  `ForeignServerVisible`, which governs `pg_user_mappings` option redaction.
- `information_schema.user_mapping_options`: actual mapping options in five
  nullable source-owned domains. Server ownership or effective USAGE exposes
  identities and option names; exact mapped-user identity, PUBLIC with inherited
  server ownership, or superuser authority discloses values. Other visible values
  remain domain-typed NULL, including harmless and empty options. Optionless
  mappings contribute no rows. Paired immutable catalog/role captures retain
  their names, values and redaction independently of later DDL or ACL publication;
  reads neither create objects nor contact foreign servers.
- `key_column_usage`: foreign-key positions follow the supporting index's
  ordered keys; visibility is owner inheritance or a column privilege.
- `constraint_column_usage`: CHECK columns come from `ExpressionReferences`,
  never a second walker; owner-role inheritance on the table is the only
  visibility predicate.
- `column_column_usage`: generated-column dependencies from the shared
  `ExpressionReferences` walker over analyzed (not folded) expressions,
  deduplicated per generation; enabled relation-owner membership is the only
  visibility predicate, evaluated once per relation.
- `routine_sequence_usage`: direct stored SQL-body relation references to real
  sequences, validated before duplicate suppression; sequence-owner role
  inheritance is the only visibility predicate.
- `check_constraint_routine_usage`: actual own table/domain CHECK expressions
  supply direct semantic routine edges through `ExpressionReferences`, also used
  by `pg_depend`. Routine-owner role inheritance is the sole visibility predicate;
  table ownership, EXECUTE and schema USAGE do not substitute for it. Constraint
  identities remain separate until final six-column DISTINCT, including same-name
  checks on different objects. All six nullable cells use the captured
  `sql_identifier` domain; empty captures are typed and non-nil, and cancellation
  discards the entire prefix.
- `routine_routine_usage`: distinct direct hard-definition edges from stored SQL
  bodies and parameter defaults, using the shared procedure dependency walker.
  Both endpoints must be functions or procedures; pinned callees are excluded.
  Only referenced-owner enabled-role membership filters disclosure, never caller
  ownership, EXECUTE or schema USAGE. All six cells use captured `sql_identifier`
  domains with exact domain usages, current namespaces and OID-suffixed specific
  names at both ends. Aggregate execution-support captures do not become direct
  edges; an explicit scalar call to the same support routine remains an edge.
- `enabled_roles` (16208): the paired catalog/role capture and
  `CurrentPrincipal`; an ordinary actor uses one cancellation-aware inherited
  `RoleClosure` (self, INHERIT paths, implicit database-owner hop), a current
  superuser enumerates live role entries. No copied membership graph or cached
  name set; partial rows are discarded on cancellation.
- `character_sets`: one current-database UTF8 row; encoding and locale facts
  are shared with `pg_database`.
- `collations`: four nullable source-owned domains (`sql_identifier` for the
  database/schema/name and `character_data` for `NO PAD`). Rows enumerate actual
  captured collations whose encoding is independent or matches `pg_database`,
  joined to their actual namespaces. PUBLIC inquiry does not require schema
  USAGE or search-path visibility and includes inquiry-only collations without
  executing providers or inventing provider details. Empty captures remain typed.
- `column_options`: six nullable source-owned domains (five `sql_identifier`
  fields and one `character_data` value). Only real foreign-table column options
  contribute rows, keyed by live stable attribute numbers, never dense slots.
  Immediately inherited ownership or effective SELECT, INSERT, UPDATE or
  REFERENCES on that column exposes its actual values, including sensitive names;
  other identities receive no rows. Schema/server USAGE and user mappings are
  not visibility prerequisites. Captured catalog, roles and domain authority stay
  paired and immutable; canceled captures discard every row. Empty strings remain
  non-NULL, and absent options remain an owned empty source.

## Domain constraints

`information_schema.domain_constraints` has exactly eight ordered nullable
domain columns: `constraint_catalog`, `constraint_schema`, `constraint_name`,
`domain_catalog`, `domain_schema`, `domain_name` are
`information_schema.sql_identifier` over NAME; `is_deferrable` and
`initially_deferred` are `information_schema.yes_or_no` over VARCHAR(3).
Logical attribute typmods are -1, with C collation; real rows have eight
non-NULL cells. Both catalogs name the current database, and both flags are NO
for current CHECK/NOT NULL definitions. Empty, false and NULL predicates retain
the same description without placeholder rows.

Rows project actual domain-owned CHECK and own NOT NULL definitions. Inherited
definitions remain on their original domain; unconstrained domains, table
CHECKs and name-like expression text manufacture no rows. Distinct domains can
share a constraint name. NOT VALID CHECKs appear normally; validation changes
`pg_constraint.convalidated`, not this view. The released view has no expression,
validation, enforcement or constraint-kind columns.

The PUBLIC-readable relation filters each domain by enabled owner membership
or effective type USAGE, including direct, inherited and PUBLIC grants and
actual superuser authority. Schema USAGE alone is insufficient, while type
USAGE without schema USAGE is sufficient for this metadata view. Search paths
and TEMP ownership add no filters. NOINHERIT/SET-only membership does not enable
ownership until SET ROLE. Hidden domains are omitted successfully, not exposed
as NULL identities or permission errors.

Fresh direct/prepared and held executions use current catalog/role captures
after supported ADD/DROP/VALIDATE, NOT NULL, rename, schema move, owner/ACL and
domain-drop changes. Failed DDL preserves prior rows. Native replay rebuilds
the metadata from real definitions; returned eager native rows retain their
original data/types. Existing ALTER DOMAIN explicit-transaction and validation
boundaries remain unchanged; no adjacent DDL or information-schema view is added.

`info_domain_constraints.go` reads captured type records directly, not a
reconstructed pg_constraint query or inherited recipe traversal. Source-owned
sql_identifier/yes_or_no cells retain canonical base-generation authority.
The engine pairs catalog/role/session authority, validates exact source
identity and wraps rows through the ordinary catalog source path. Successful
empty capture is nonnil; cancellation or malformed authority/shape returns no
partial rows. Native relation identity16211 is independent of PostgreSQL's
allocator-dependent view OID.
See [selected proof and smoke](../../../reference/information_schema/README.md#domain-constraints).

## Information-schema sequence definitions

`info_sequences.go` projects actual immutable SequenceOptions using one paired
catalog/role capture with the current principal, database and physical TEMP owner.
The normal catalog source path owns registration, PUBLIC SELECT, source-owned
domain carriers, origin attributes and existing row/array associations.
Visibility is enabled ownership OR any relation-level SELECT, UPDATE or USAGE;
schema USAGE and search_path are not filters. Identity sequences and foreign
physical TEMP objects are excluded; ordinary AUTO ownership is eligible.
The producer does not read generators or acquire sequence-value locks. Fresh
held executions capture current options and permissions; returned eager native
Rows remain unchanged. Errors and cancellation publish no partial prefix.

## Column metadata

`info_columns.go` owns the 44-column nullable domain projection and validates
its exact source identity, canonical domain usages and physical base generation
before enumeration. It reads the paired immutable catalog/role roots and source
attribute facts, not query results or stored rows. Real r/v/f/p descriptors
contribute their live positive stable attributes; missing provider attributes,
dropped columns and other relation kinds do not manufacture rows. Ordinal,
DTD identifier and column ACL address all use the same stable attribute.

Visibility is enabled ownership or effective SELECT, INSERT, UPDATE or REFERENCES
on that column, after excluding another physical session's TEMP namespace.
Schema USAGE, search path, row security and sequence ACLs do not filter metadata.
Type dimensions use the declared type's immediate domain base exactly once;
domain-owned NOT NULL is distinct from recursive conversion constraints.
Actual nondefault collations retain their catalog identities. Display uses the
captured stored deparser; identity options come from immutable definitions,
never generators. Metadata reads never evaluate default/generated expressions.

The runtime's updatability capability is structural and ACL-independent:
view/rule recursion maps frozen attributes to the current captured layout,
and foreign capability inspects registered interfaces without callbacks or
write binding. Successful empty captures remain typed, nonnil sources;
cancellation, stale authority and display errors discard every partial row.

## Domain definitions

`info_domains.go` projects genuine concrete domain descriptors through the
ordinary PUBLIC-readable catalog source and its 27 nullable logical-domain
columns. One paired catalog/role/session capture supplies enabled owner or
effective domain USAGE visibility, immediate base identity and typmod,
creation-time copied default text, and the domain's collation. Schema USAGE,
search paths and base-type visibility add no row filters. The immutable
effective type-name path from that same inquiry controls qualification of
nonspecial base names and array element names; parser-special spellings stay
stable. Predefined domains use the same authority as user domains; constraints
neither multiply rows nor add columns. Fresh held executions capture current
definitions, privileges and naming paths; returned rows retain their original
values and descriptor generations.
Malformed source/base/collation authority and cancellation return no prefix.

## Table metadata

`info_tables.go` uses the ordinary PUBLIC-readable source/type path and twelve
nullable source-owned domain columns. Capture validates the source layout,
domain/base generations and column origins before projecting actual represented
relations. Classification comes from `ClassKind`; a physical session's TEMP
namespace takes precedence, and peer TEMP namespaces are excluded.

Tables and views share enabled-ownership and effective relation/live-column
privilege visibility. Schema USAGE and search paths add no metadata filter;
MAINTAIN alone does not disclose a relation. Typed-table identity comes from
the captured `OfType` descriptor and namespace, not an implicit row type.
Insertability reuses structural view/rule capabilities and registered foreign
modification interfaces without executing relation bodies or foreign callbacks.
The PostgreSQL-unimplemented fields remain genuine typed NULLs.

Each held execution captures current definitions and authorization. Returned rows
retain their original values and domain authority; an empty capture is nonnil,
and invalid authority or cancellation publishes no partial result.

## Information-schema views

`info_views.go` projects ten nullable source-owned domain columns through the
ordinary source/type path: three `sql_identifier` identities, definition and
check option as `character_data`, then five `yes_or_no` flags. Empty results
retain the same description. Every represented `ClassKind` v relation is a
candidate except another physical session's TEMP relation.

Visibility is enabled owner membership or any supported relation DML/reference/
trigger privilege, or any supported privilege on a live positive attribute.
MAINTAIN alone does not disclose a view; schema USAGE and search_path do not
filter metadata. ACL-visible nonowners receive a typed NULL definition; enabled
owners receive the current definition. Query bodies are never evaluated.
Fresh held executions capture current definitions, options, role authority and
display settings together; failed capture or cancellation publishes no prefix.

Structural flags do not depend on the viewer's DML rights. The engine bridges
`view.Capabilities`, including nested output subsets, unconditional INSTEAD rules
and the actual registered foreign handler's modification interfaces. Rules are
not trigger facts. Local INSTEAD OF ROW definitions supply the three trigger
flags independently of structural events; an outer view does not inherit them.
Represented catalog relations classified as ordinary tables supply ordinary
base-table structural events; this does not change catalog write admission.

`builtin_views*.go` owns one closed inventory of builtin view definitions,
including `information_schema.columns`, `information_schema.domains`,
`information_schema.user_defined_types`, `information_schema.tables`,
`information_schema.usage_privileges`, `pg_catalog.pg_policies` and
`pg_catalog.pg_rules`, with classification and per-output capability facts.
Its canonical literal fragments contain explicit object-name and identifier
occurrences. `pg_rules` uses the same definition authority as both consumers;
its `pg_get_ruledef(oid)` reference captures stable routine identity, while
column and relation aliases remain identifier-only annotations. Its structural
update and insert flags are NO; this does not change rule visitation or DDL.
`SummaryViewDefinition` supplies canonical default metadata;
`BuiltinViewDefinition` selects explicit default or pretty fragments and formats
their occurrences using captured naming and identifier formatters. UDT and COPY
progress inquiries own both released forms; absent pretty fragments never become a synthesized
definition. The same runtime authority renders `pg_views` and `views` in default
format and serves the boolean `pg_get_viewdef` presentation selector.
COPY progress's builtin entry also declares a native `_RETURN` identity, stamped
into its immutable relation descriptor during bootstrap. Rewrite projection,
object-name/address lookup and dependency ownership use `ReturnRuleOID`; the
rule has an internal edge to its view. Released backing catalog/routine references
are bootstrap-pinned, not fabricated dependency objects or executable payloads.
Return-rule ownership is collected before ordinary catalog-row dependency
suppression; the existing pinned-object policy still filters backing references.
Routine occurrences capture their original OID from the existing released
identity registry. Represented targets use the current name, namespace and
signature from that same captured catalog before exact-signature qualification;
same-name replacements cannot retarget them. Missing current namespaces fail
the capture rather than inventing labels.
Released references absent from the native catalog remain display-only facts,
never descriptors, stored query payloads or executable providers. Captured
search_path, exact-signature routine shadows and quote_all_identifiers affect
presentation without rewriting column qualifiers as relation names.
The usage-privilege definition retains all five released UNION ALL branches
and their real class/domain/routine references, including the sequence
table-default ACL fallback. Its display inventory is independent of execution
through the shared usage-origin projector.

## Testing

Package units here run without an engine. Producer behavior tests need an
engine-seeded catalog, so they run on a composed engine in `enginetests/`
(or in `core/` when they reach core's capture runtime) and reach producers
through the real registry; see the
[core Testing section](../core/README.md#system-catalog-producer-contract). Public
catalog SQL belongs in `reference/catalogs` and its sibling catalog packages
(large objects in `reference/object_grants`, settings in
`reference/session_state`); follow
[docs/TESTING.md](../../../docs/TESTING.md) and add `REFERENCE=1` for the oracle.

Prepared statement metadata ownership and public scenarios share the family
selectors `RUN='^TestPGPreparedStatements'` and
`RUN='^TestReferencePGPreparedStatements'`.

Composed index-identity witnesses that require actual hash probing prescribe
their CPU profile through session settings and command admission. Opening a
private filter stage also admits the source's detached scan decision; retaining
a hash root alone does not select that access method.

- Package units: `RUN='^Test(ProducerRegistryLookup|PgClassPartitionTree|CollationActualVersionProviders)$'`
- Ordered routine formal arrays, represented builtin/installed/generated
  declarations and released attribute identities:
  `RUN='^(TestRoutineResultFormal(Builtin|Owned)?Catalog|TestRoutineProcedureDefaultCount|TestRoutineAttributeCatalogProjection)$'`, also with `RACE=1`.
  Composed declaration captures live in `enginetests/`; SQL projection units
  remain here. Expectations include scalar OUT, canonical VARIADIC, one-based
  dimensions, typed all-IN NULL controls and declaration-owned default counts.
- Registry coverage and captured registration identity (engine):
  `RUN='^TestCatalog(SourceProducerCoverage|GrantRegistration)$'`
- Producer units (engine): `RUN='^Test(PGLargeobject|PGTransform|PGSettings|Info(UserMappings|KeyColumnUsage|ConstraintColumnUsage|RoutineSequenceUsage|RoutineRoutineUsage|CharacterSets|Collations))'`
- Column-domain usage producer units (core): `RUN='^TestInfoColumnDomainUsage'`
- Installed text-search template producer units: `RUN='^TestPGTSTemplate'`
- Wait-event definition units: `RUN='^TestPGWaitEvents'`
- Wait-event definition public scenarios: `RUN='^TestReferencePGWaitEvents'`
- Information schema: `RUN='^TestReferenceInfo(UserMappings|KeyColumnUsage|ConstraintColumnUsage|RoutineSequenceUsage|RoutineRoutineUsage|CharacterSets|Collations)'`
- Schema inquiry units: `RUN='^TestInfoSchemata'`
- Schema inquiry SQL: `RUN='^TestReferenceInfoSchemata'`
- Trigger and constraint-ownership producer units: `RUN='^TestPGTrigger'`
- Large objects: `RUN='^TestReferenceLargeObject'`
- Settings view: `RUN='^TestReferenceStatisticsSettings'`
- Transforms: `RUN='^TestReferencePGTransform'`
- Sequence-definition producer units: `RUN='^TestInfoSequences'`
- Sequence-definition public scenarios: `RUN='^TestReferenceInfoSequences'`
- Column metadata producer units: `RUN='^TestInfoColumns'`
- Column metadata public scenarios: `RUN='^TestReferenceInfoColumns'`
- Domain-definition producer units: `RUN='^TestInfoDomains'`
- Domain-definition public scenarios: `RUN='^TestReferenceInfoDomains'`
- Table metadata producer units: `RUN='^TestInfoTables'`
- Table metadata public scenarios: `RUN='^TestReferenceInfoTables'`
- Routine metadata producer/identity units: `RUN='^TestInfoRoutines'`
- Routine metadata public scenarios: `RUN='^TestReferenceInfoRoutines'`
- Routine ACL producer/authority units: `RUN='^TestInfo(RoleRoutineGrants|RoutinePrivileges)'`
- Routine ACL public scenarios: `RUN='^TestReferenceInfo(RoleRoutineGrants|RoutinePrivileges)'`
- CHECK routine-usage producer units: `RUN='^TestInfoCheckConstraintRoutineUsage'`
- CHECK routine-usage public scenarios: `RUN='^TestReferenceInfoCheckConstraintRoutineUsage'`
- CHECK-definition producer/authority units: `RUN='^TestInfoCheckConstraints'`
- CHECK-definition public scenarios: `RUN='^TestReferenceInfoCheckConstraints'`
- UDT grant producer/authority units: `RUN='^TestInfoRoleUdtGrants'`
- UDT grant public scenarios: `RUN='^TestReferenceInfoRoleUdtGrants'`
- SQL-feature producer/authority units: `RUN='^TestInfoSQLFeatures'`
- SQL-feature public scenarios: `RUN='^TestReferenceInfoSQLFeatures'`
- Usage grant producer/authority units: `RUN='^TestInfoRoleUsageGrants'`
- Usage grant public scenarios: `RUN='^TestReferenceInfoRoleUsageGrants'`
- SQL-sizing producer/authority units: `RUN='^TestInfoSQLSizing'`
- SQL-sizing public scenarios: `RUN='^TestReferenceInfoSQLSizing'`
- SQL-part producer/authority units: `RUN='^TestInfoSQLParts'`
- SQL-part public scenarios: `RUN='^TestReferenceInfoSQLParts'`

### Domain constraints

```sh
make check RUN='^TestInfoDomainConstraints(Producer|Capture|Cancellation)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$' REFERENCE=1
```

