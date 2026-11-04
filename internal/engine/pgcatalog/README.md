# internal/engine/pgcatalog

Row producers for the system relations: the `pg_catalog` tables and views and
the producer-backed `information_schema` tables and views (`sql_features`,
`sql_sizing`, `sql_implementation_info`, `sql_parts`, `table_privileges`,
`data_type_privileges`, `user_mappings`, `user_mapping_options`, `foreign_data_wrapper_options`, `foreign_data_wrappers`, `foreign_server_options`, `foreign_servers`, `foreign_table_options`, `foreign_tables`, `key_column_usage`,
`constraint_column_usage`, `constraint_table_usage`, `column_column_usage`, `column_domain_usage`, `domain_udt_usage`, `information_schema_catalog_name`,
`routine_sequence_usage`, `routine_routine_usage`,
`check_constraint_routine_usage`, `check_constraints`, `referential_constraints`,
`character_sets`, `collations`, `schemata`, `column_options`, `transforms`, `enabled_roles`, `applicable_roles`, `view_table_usage`, `view_column_usage`, `triggers`, `domain_constraints`, `table_constraints`,
`sequences`, `role_column_grants`, `role_table_grants`, `role_routine_grants`, `routine_privileges`,
`role_udt_grants`, `udt_privileges`, `role_usage_grants`, `usage_privileges`, `columns`, `attributes`, `domains`, `user_defined_types`, `views`, `tables`, `routines`, `parameters`, `element_types`). Each producer is a pure
function from a captured catalog, a paired role read and detached facts or a
narrow runtime capability to rows. Orchestration (per-OID catalog choice,
unlock timing, wrapping rows as query sources) stays in the engine; see the
[engine README](../README.md).

`VisitProcedureFormals` and `ProcedureNameProjection` are the single declaration
projection for pg_proc arrays, information_schema parameters and routine inquiry.
They retain all-formal order separately from input/default slots, including OUT,
INOUT, VARIADIC and TABLE modes. Invocation expansion and inferred call results
cannot replace these descriptor-owned facts.

`pg_replication_origin_status` projects one detached cluster progress map paired
with current catalog/role roots under `Engine.Mu`. Map presence (including zero),
not registration enumeration, owns row existence; orphan names are typed NULL.
Projection runs after unlocking and never borrows live maps or a statistics
Reader. The native local WAL position is invalid/unset zero. Common current
view/column SELECT and backing6014 EXECUTE are independently admitted.
The four nullable catalog attributes use collation0 except TEXT `external_id`,
which uses PostgreSQL's default collation100. Its explicit owner-only relation
seed and independently mutable column grants participate in native ACL replay.
`make check RUN='^TestPGReplicationOriginStatusProducer$'` exercises pure typed
projection, authority rejection and cancellation; shared SQL scenarios live in
`reference/catalogs`.

## Database cumulative monitoring

`pg_catalog.pg_stat_database` is a typed, read-only catalog source. Its native
relation identity is 12212; the existing seeded row/array types remain
12272/12271. These are native identities, not promises of PostgreSQL initdb OID
parity. There is one row per actual `pg_database` identity, including databases
with no published entry, and one shared-object row `(datid=0, datname=NULL,
numbackends=0)`. The zero follows REL_18_6 `system_views.sql`'s explicit CASE,
despite the monitoring manual describing NULL. No order is promised without
`ORDER BY`. Database aggregates have no backend-owner or `pg_read_all_stats`
masking; ordinary relation SELECT ACLs still apply.
The executable source is admitted through the common numerically sorted producer
registry, independently of descriptor and symbolic-definition inventory order.
`make check RUN='^TestProducerRegistryLookup$'` checks that lookup invariant,
including the database source; symbolic definition registration alone cannot
make a source executable.
The existing symbolic builtin-view inventory also supplies default/pretty
`pg_get_viewdef`, quote-all-identifiers and exact-signature search-path masking.
Its native return-rule identity is 12360, reserved independently of every
descriptor address and owned definition in the complete installed catalog.
In particular, relation 12359 remains `pg_statio_user_indexes`; database creation
and catalog replacement validate the same disjoint identity space.
Upstream-only getter names are bounded display references, not newly installed
SQL functions or another execution path.
The released deparser leaves outer `oid`/`datname` references unqualified for its
single FROM item, retaining the `d` alias and inner `pg_database` qualification;
the symbolic default/pretty forms follow that presentation, not source spelling.
`TestReferenceStatDatabaseDefinition` independently checks the released expression
inventory and identifier presentation without normalizing away qualifiers.

SELECT-star and prepared descriptions preserve this exact order:

| Columns | SQL types, in the same order |
| --- | --- |
| datid, datname, numbackends | oid, name, integer |
| xact_commit, xact_rollback, blks_read, blks_hit | bigint, bigint, bigint, bigint |
| tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted | bigint, bigint, bigint, bigint, bigint |
| conflicts, temp_files, temp_bytes, deadlocks, checksum_failures | bigint, bigint, bigint, bigint, bigint |
| checksum_last_failure | timestamp with time zone |
| blk_read_time, blk_write_time, session_time, active_time, idle_in_transaction_time | double precision, double precision, double precision, double precision, double precision |
| sessions, sessions_abandoned, sessions_fatal, sessions_killed | bigint, bigint, bigint, bigint |
| parallel_workers_to_launch, parallel_workers_launched | bigint, bigint |
| stats_reset | timestamp with time zone |

Missing cumulative numbers are zero. `checksum_failures` is NULL with checksums
off (the native default); its last-failure timestamp is NULL without an actual
failure, including when checksums are on. `stats_reset` is NULL without an
explicit database statistics-reset event, not the provider's catalog-replacement
timestamp. Floating times are milliseconds. Logical identities, accepted tuple
attempts/reads, top transaction outcomes, real deadlock decisions, client lifecycle
causes and executor worker starts are exact native events. Only block/access,
temporary-file, checksum and elapsed-time technical fields use the documented
in-memory model in `statistics/README.md`; no physical spill/checksum/recovery
engine is implied. Native current `numbackends` bypasses retained cumulative
snapshots. This deliberately differs from the released backend-status reader:
PostgreSQL 18.6's `pg_stat_get_db_numbackends` counts a transaction-local backend
snapshot, cached independently of `stats_fetch_consistency` until transaction
end or clear. The public retained-reader witness expects that released stale
status and native live status separately; an independent outside-transaction
observer proves real peer admission/publication without clearing the retained
cumulative facts. See released
[`backend_status.c`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/utils/activity/backend_status.c)
and [`pgstatfuncs.c`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/utils/adt/pgstatfuncs.c).

INSERT, UPDATE and DELETE reject even zero-match writes with SQLSTATE `55000`,
respectively `cannot insert into view "pg_stat_database"`,
`cannot update view "pg_stat_database"`, and
`cannot delete from view "pg_stat_database"`. Detail is exactly
`Views that do not select from a single table or view are not automatically updatable.`
Hints use `To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule.`
(UPDATE uses `updating the view`/`UPDATE`; DELETE uses
`deleting from the view`/`DELETE`). TRUNCATE rejects with `42809`,
`"pg_stat_database" is not a table`. No reset builtin or database getter routine
is introduced by this source.


`pg_class.relpartbound` captures immutable membership and parent-key roots.
`PartitionBoundParts` exposes those borrowed roots only for its genuine bound
carrier so the partition inquiry owner can render SQL without parsing structural
output or consulting a newer parent descriptor.

`element_types` projects actual array declarations into 28 nullable source-owned
domain cells. Nominal `TypeFacts.Element` and variable length determine admission;
declaration domains are not flattened. Relation/composite attributes retain actual
ordinals, domains describe only their immediate base, and routines use all-formal
positions plus the declared result. Captured object collation, element identity,
database, type-name path and effective object-specific rights share one coherent
catalog/role frontier. Metadata never scans stored arrays or invokes routines or
foreign handlers. Missing declarations remain absent; cancellation or invalid
authority publishes no prefix. Native source16256 links row13516/array13515 through
the common bootstrap. Symbolic view text supplies presentation, not execution.
Metadata admission is independent of codecs and lookup identity: canonical
array/domain links are validated before consulting the existing type-fact
inventory. A lookup-only identity cannot fabricate facts or hide malformed links.


## Imports

Layer L6.5 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `catalog`, `authorization`, `functions`, `largeobject`, `lock` (lock
records), `storage` (relation statistics), `proc` (routine definitions),
`plan` (expression trees for `pg_statistic_ext`), `expr`, `ids`, `settings`,
`sqltype`, `sqlstate`, `parser` (catalog descriptors store parser enums) and
the standard library. It never imports the root engine, `access`, `visibility`
or `session`.

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
    SharedDependencies []SharedDependency // core-classified immutable facts
    Activities map[ids.SessionToken]statistics.Activity
    ActivityObserver authorization.PrincipalRef
    RecoveryPrefetch statistics.SharedFact // detached current provider cut
    Archiver statistics.SharedFact // detached admitted cumulative Reader cut
    SLRU [statistics.SLRUCount]statistics.SLRUFact // detached cumulative family
    BGWriter statistics.SharedFact // detached demand-admitted cumulative Reader cut
    Checkpointer statistics.SharedFact // detached demand-admitted cumulative Reader cut
}
type Producer func(Input) ([][]sqltype.Value, error)
```

Current fixed-family projections consume detached value facts, never live provider
or session pointers. Core captures recovery-prefetch under Engine.Mu before the
leaf provider lock, then releases Engine.Mu before pure projection. Registration
and exact source/type/attribute authority are checked before owned rows publish.
Missing reset metadata remains typed NULL; integer gauges use checked INT4
conversion. Projection never initializes metadata or constructs Reader snapshots.
The archiver singleton has seven nullable INT8/TEXT/TIMESTAMPTZ attributes;
TEXT retains default collation100. Slots0/1 project successful/failed archive
counts, independently absent event strings/timestamps become typed NULL, and
unused slots are ignored. Core checks current view/column SELECT and backing
routine3195 EXECUTE before taking the admitted cumulative Reader cut; ordinary
SELECT does not imply session-style redaction. The released function-source
definition supplies normal read-only diagnostics. Its zero-provider routine
descriptor owns exact OUT metadata and default PUBLIC EXECUTE/GRANT/REVOKE,
without providing a callable getter.

`pg_stat_slru` (relation12236, rowtype12238, array12237) projects exactly eight
fixed SLRU names and nine nullable attributes: TEXT name (default collation100),
seven BIGINT counters and TIMESTAMPTZ reset metadata. Core admits current view/
column SELECT and independent `pg_stat_get_slru` EXECUTE before accessing the
existing session statistics Reader; denied reads cannot create a snapshot.
Unlike prefetch, SLRU honors none/cache/snapshot. The pure producer receives only
the detached coherent family, preserves missing reset times as NULL and never
manufactures physical activity. Its released getter-backed definition is display/
dependency metadata; an engine-owned zero-provider routine2306 descriptor owns
the live default PUBLIC EXECUTE ACL and ordinary GRANT/REVOKE without exposing
a callable getter implementation. Ordinary readers see
all fields without activity-style masking; common registered-view mutation
rejection owns INSERT/UPDATE/DELETE/TRUNCATE/COPY diagnostics.

The unbounded TEXT name exposes known driver length `math.MaxInt64`; the BIGINT
and TIMESTAMPTZ columns expose unavailable length metadata. Its producer entry
precedes recovery-prefetch in the strictly OID-ordered binary-search registry.

The bgwriter singleton projects slots0/1/2 into three INT8 technical counters and
genuine reset metadata into TIMESTAMPTZ, with nullable view attributes. With no
physical background writer or buffer allocator, all three estimates are zero;
SQL activity and logical buffercache slots are not physical events. Unrelated
fact slots and event names/times are ignored. Current relation/column SELECT
precedes independent EXECUTE checks on only the demanded scalar getters
2772/2773/2859/3075, including predicate and sort dependencies. Recursive source
preparation admits all selected getter occurrences before any cumulative capture,
and direct source opening rechecks the same current authority. A presence-only
source preserves singleton cardinality without a getter or Reader access. A proven
direct FALSE input also avoids Reader access through an ordinary aggregate, but
does not waive the referenced getters' initialization-time EXECUTE admission.
Demanded cumulative bgwriter rows establish the same complete accessible-domain
snapshot as database statistics before reading their fixed-family fact; the
presence-only, FALSE-input and denied-admission paths remain noncapturing.
Authorized rows have no role-dependent masking. The scalar no-FROM definition
is display metadata, not a callable getter implementation; ordinary virtual-view
mutation diagnostics remain authoritative.

The checkpointer singleton (relation12297, composite12299, array12298) uses the
same scalar admission and suppression policy. Its eleven nullable attributes are
`num_timed`, `num_requested`, `num_done`, `restartpoints_timed`,
`restartpoints_req`, `restartpoints_done`, `write_time`, `sync_time`,
`buffers_written`, `slru_written`, `stats_reset`: eight INT8 counters, two FLOAT8
millisecond totals and a genuine TIMESTAMPTZ reset instant. Counters0–5 and6–7
project independently around the two fractional-millisecond times. Without
physical checkpoint/restartpoint, buffer, SLRU or IO events all ten technical
estimates are zero, never inferred from logical SQL work. Uninitialized ResetAt
is typed NULL. Public reads have no role masking; demanded getter ACL denial
rejects rather than redacts. Metadata-only getter identities remain noncallable.
Rows are detached; singleton cardinality adds no ordering contract for joins.


`pg_stat_replication_slots` (12266) validates current registered catalog/source
and paired role authority for ten nullable attributes: TEXT identity, eight INT8
counters and TIMESTAMPTZ reset metadata. Only actual logical-slot inventory can
establish rows; provider keys, resets and rejected creation cannot. Native slot
creation publishes no inventory, so capture returns no rows without fetching
statistics, allocating absent rows or establishing a cumulative reader snapshot.
The existing ReplicationSlot provider remains the sole statistics owner.
Ordinary current SELECT admission applies without field redaction; the common
virtual-view paths reject mutations. Symbolic display describes the released
inventory/lateral-statistics join without granting callable backing interfaces.

`pg_stat_subscription` (relation12248, rowtype12250, array12249) authenticates
the captured catalog/source, type generations, paired role root and qualified
source reference before enumerating real shared subscription identities. Its
eleven nullable attributes preserve exact OID/NAME identity across databases.
Without a real apply worker the released LEFT JOIN yields nine typed NULLs;
Enabled metadata, counters and ordinary session PIDs never establish a worker.
There is no owner-based field mask or protected conninfo projection. Current
view/column SELECT and backing routine6118 EXECUTE are independently admitted
before pure capture outside Engine.Mu. Constant-false queries still require SELECT
but do not invoke the getter or demand its EXECUTE. Reads do not access or
initialize a cumulative Reader, publish events, reset counters or allocate keys.
The existing Subscription provider category and command lifetime hooks remain
the sole genuine error/conflict counter owner. Separate symbolic default/pretty
definitions preserve released join formatting. Native `_RETURN` rule12251 uses
the existing parsed default rewrite declaration, not a
callable SRF or executable rule. Common non-updatable-view admission owns mutation
diagnostics; retained rows own their original immutable typed backing.

- `pg_stat_activity` (12226) projects one detached unmasked cluster activity cut
  with exactly 22 nullable fields and independently live actual wait ownership.
  Core samples canonical XID/xmin and releases metadata locks before manager
  capture; the producer has no live session pointer or provider callback.
  Current observer INHERIT/stats privilege is reapplied at every execution;
  XID/xmin remain public and protected query uses the literal redaction.
  Login OID is immutable, while the current role-name LEFT JOIN reflects rename
  and produces typed NAME NULL after role drop. Missing same-domain targets
  cannot poison the source or erase valid super/stats privilege; strict observer
  and foreign/zero principal validation remains intact.

- `pg_stat_replication` (12231, row type 12233, array 12232) is a read-only,
  twenty-column nullable view on the common registered source/capture path.
  The shared provider has no actual WAL senders, so validated capture returns
  nonnil empty rows, not a fictitious worker or zero-progress LSN. Ordinary
  clients, inactive slots, receivers, origins and standby configuration are not
  senders. Physical replication and replication startup remain unavailable;
  rejected wire startup cannot register a sender. The symbolic display retains
  PostgreSQL 18.6's activity/sender join, complete function output alias lists,
  literal `NULL::integer` cast and role LEFT JOIN without installing callable
  backing functions. No row order is promised without ORDER BY.

  The PostgreSQL 18.6 view composes **two distinct privilege gates**:
  `pid`, `usesysid`, `usename`, `application_name` and `backend_xmin` are general
  identity/horizon fields (genuine absence can still be NULL).
  `client_addr`, `client_hostname`, `client_port` and `backend_start` are visible
  to superusers, holders of effective `pg_read_all_stats` privileges, or callers
  possessing privileges of the sender's authenticated role; otherwise NULL.
  All eleven fields from `state` through `reply_time` require superuser or
  effective `pg_read_all_stats` privileges; otherwise **all eleven are NULL,
  even for a sender-role reader**. Do not reuse activity's own-role allowance
  for sender state, LSNs, lags, priority, sync state or reply time.
  No positive native sender exists to exercise these masks: empty role tests
  establish visibility and absence, not positive filtering evidence.

  Genuine unavailable peer address/hostname, feedback xmin, individual LSNs,
  lag and reply time remain NULL, never guessed IPs, timestamps, `0/0` progress
  or zero lag. Unix-socket peers have NULL address and port -1; hostname is not
  inferred. Idle measured lag may persist briefly before becoming NULL.
  Cardinality is one row per actual WAL sender, not per slot or receiver.

- `pg_stat_ssl` (relation12253, rowtype12255, array12254) projects that same
  canonical unmasked physical activity cut into eight nullable columns:
  INT4 pid, BOOL ssl, TEXT version/cipher, INT4 bits, TEXT client_dn,
  NUMERIC client_serial and TEXT issuer_dn. Text uses default collation100.
  Native sessions have no TLS: authorized rows contain actual PID, false and
  six typed NULLs. Current inherited login/stats authority omits denied peers
  before row construction; target SET ROLE never relabels its login principal.
  The pure producer has no transport/provider callbacks and needs no wait work.
  The released display uses unqualified output names, all 31 SRF OUT aliases and
  client_port filtering as display/dependency metadata, not an executable
  endpoint predicate or a new public getter. Existing
  registered-view machinery owns read-only diagnostics and catalog identities.

- `pg_matviews` (12006) projects captured materialized descriptors and paired
  roles, using the runtime's current definition presentation. Population is
  independent of stored row count; index presence follows sticky publication
  history, not a live-index count. Default placement is NAME-typed NULL.
  Represented-object ACLs, schema USAGE and search_path do not filter rows.
  Core owns the transaction/database catalog frontier and ordinary admission to
  the catalog view; the producer neither scans materialized contents nor borrows
  current global state.

- `pg_backend_memory_contexts` consumes detached current-session layout facts,
  captured only after independent view SELECT and backing EXECUTE admission.
  Three ownership groups retain own bytes, not descendant totals. Typed NULLs
  represent unsupported allocator block/free-list counters; `GoObjectLayout`
  identifies selected real Go layouts, excluding referenced storage and allocator
  overhead. View rows and the callable SRF share `BackendMemoryRows`, using
  current-generation descriptors and invocation-owned path arrays. Cancellation
  publishes no partial row vector; producers never inspect a session registry.
  Whole-view-row values use the completed builtin nominal row 12144 and array
  12143, whose members reference the same current descriptors as source columns;
  the callable routine continues to return its declared anonymous RECORD shape.

- `pg_config` (12130) captures the immutable embedded distribution identity as
  the `VERSION` row, reusing `functions.EmbeddedVersionBanner`. Its two nullable
  TEXT columns retain captured descriptors, origins and ordinary TEXT collation.
  Unsupported installation/build keys are absent, never synthesized paths or
  placeholders. Capture validates registered source/owner/ref/type/attribute
  authority and cancellation before publishing invocation-owned rows.
  Normal current view SELECT precedes the backing routine 3400 EXECUTE check in
  core. Both bootstrap ACLs are explicit owner-only; immutable initial privileges
  do not follow later grants/revokes. The backing OUT declaration and exact view
  display metadata confer no callable provider capability. Reset/Restore rebuild
  the identity through the common catalog bootstrap and symbolic ACL replay.

- `pg_file_settings` (12110) exposes seven nullable attributes: `sourcefile`,
  `sourceline`, `seqno`, `name`, `setting`, `applied`, `error`. The runtime has no
  parsed GUC-file source, so its owned, typed capture is honestly empty. CLI
  option files, simulated `config_file`, registered/default/provider values and
  SET/RESET are not file assignments. Registered descriptors, type generations
  and cancellation are validated even with no rows. Normal view SELECT precedes
  current routine 3329 EXECUTE; both ACLs are owner-only by default, without
  `pg_read_all_stats`/`pg_monitor` access. Explicit grants may authorize readers.
  The exact `pg_show_all_file_settings` declaration (`prosrc=show_all_file_settings`)
  supplies metadata and ACL authority only, not a callable provider. Initial ACLs
  remain immutable; native controls reuse bootstrap and symbolic ACL replay.

- `pg_ident_file_mappings` (12118) captures current actual files only through the
  immutable Driver `ConfigureIdentFile` capability. The `identfile` package owns
  lexing, shared-depth includes/@ expansion, provenance, PostgreSQL ARE validation
  and diagnostics; the catalog producer owns seven nullable typed SQL fields.
  No native attachment means no rows; explicit top-file ENOENT raises 58P01.
  Current view/column SELECT and independent routine 6250 EXECUTE admission occur
  before filesystem access. Parsing runs outside `Engine.Mu`, without producer
  filter pushdown, and materializes owned rows before query projection/sorting.
  File edits do not mutate retained results. Both ACLs start owner-only, without
  implicit statistics/monitor access; initial privilege records remain historical.
  The volatile, parallel-safe backing OUT declaration matches the pinned 18.6
  oracle; its declaration/deparse metadata grants no callable provider authority.
  Reset/Restore retain destination startup authority and replay symbolic ACLs;
  Dump neither reads nor serializes filesystem authority. No authentication,
  identity matching, role lookup or backreference substitution is performed.

- `pg_rewrite` (2618) projects all eight nonnullable attributes from actual
  immutable view/materialized-view and ordinary-rule owners. `VisitRewriteCatalog`
  bridges checked definitions without importing `stored`; the producer supplies
  the captured owning relation identity. Tree columns use PG_NODE_TREE (194),
  typmod -1 and C collation (950). Qualification absence is the non-NULL `<>`
  node. Actions retain ordered concrete commands and analyzed expressions.
  Explicit builtin return-rule identities instead retain private parsed sealed
  SELECT declarations and their inventory reference facts, tagged
  `BuiltinRewriteDeclaration`; they are not checked executable views and grant
  neither execution, provider, write nor dependency authority. Failed capture
  publishes no prefix. Tree output is native structure, not PostgreSQL
  `nodeToString` bytes or SQL text.
  Checked query trees also retain actual selected aggregate/window and
  equality/ordering declarations with occurrence associations. The existing
  dependency collector, not serialized bytes, supplies their real edges.
  Qualification inquiry renders retained subqueries: OID-zero versus existing
  relation context controls child prefixing independently of pretty mode, and
  child-local columns do not trigger outer OLD/NEW diagnostics.

- `pg_foreign_data_wrapper` (2328) projects only actual SQL wrapper definitions,
  including valid definitions without handlers. Its public inventory, raw ACL and
  complete ordered options are independent of wrapper USAGE; normal catalog
  relation/column SELECT admission still applies. Missing support procedures are
  zero OIDs; absent ACL/options are typed NULL, not default ACLs or empty arrays.
  Non-NULL arrays own rank-one, lower-one backing; an explicit empty ACL retains
  that shape with length zero rather than becoming NULL or a rank-zero array.
  Initialized `pg_attribute` facts preserve declared nullability: the five fixed
  fields are nonnullable and both rank-one array fields are nullable.
  Capture validates all seven initialized facts against declared nullability,
  dimensions and effective collation, as well as source/type/role authority and
  descriptor references, even for empty inventories;
  cancellation publishes no prefix. Metadata never invokes registered providers
  or performs foreign I/O, and registration alone creates no catalog row.
- `pg_foreign_server` (1417) projects actual immutable server definitions with
  their server, owner and wrapper identities, nullable type/version, raw ACL and
  complete ordered options. Ordinary catalog relation/column SELECT admission
  governs reads independently of server or wrapper USAGE; the information-schema
  server views retain their separate ownership/USAGE eligibility filter.
  The first four attributes are nonnullable; type/version and both rank-one
  arrays are nullable. Default ACL and absent options are typed NULL; explicit
  empty ACL retains rank-one, lower-one empty backing. Source/type/catalog/role
  authority and live wrapper linkage are checked even for an empty inventory.
  Cancellation or invariant failure publishes no prefix. Retained rows own their
  arrays and captured type generation across later definition replacement.
  Metadata neither invokes registered providers nor attempts foreign connections;
  valid no-handler definitions remain catalog-visible.
- `pg_shseclabel` (3592, rowtype4066) validates owned source authority and exact
  nonnullable OID/OID/TEXT/TEXT metadata (C collation on text), then emits the
  genuine empty provider inventory without storage or per-call row allocation.
  Ordinary PUBLIC SELECT/current ACL admission applies; shared classification
  supplies global tablespace1664 and logical index presence. The key is
  `(objoid,classoid,provider)`; no local `objsubid` exists. PostgreSQL native
  provider ABI/loading and SELinux/MAC integration are not available; bounded
  shared-target SECURITY LABEL commands fail before object lookup, never populate
  rows. `pg_seclabel` and `pg_seclabels` are not implemented here.
  Its explicit bootstrap ACL retains all eight owner table privileges plus
  PUBLIC SELECT, seeded through the common materialized-relation ACL path.
  Native unbounded TEXT length metadata is known `math.MaxInt64`; OID length and
  precision/scale are unavailable.
- `pg_shdepend` (1214) encodes core-classified immutable shared-dependency facts
  into seven nonnullable OID/OID/OID/INT4/OID/OID/internal-CHAR columns. It validates
  source/type/fact authority and cancellation without importing access or live
  cluster state. Core owns canonical owner/current/column/default/initial ACL,
  policy and membership-grantor classification, referenced pins, cluster scope
  and shared-copy selection. There is no persisted dependency row store,
  fabricated membership endpoint or default-placement tablespace dependency.
- `pg_init_privs` (3394) projects immutable historical records by full object
  address with no allocated row OID. Its five nonnullable columns retain exact
  source-owned OID/INT4/internal CHAR/ACLITEM[] carriers and ordered ACL entries.
  Catalog and paired principal authority are validated before capture; canceled
  captures expose no partial rows. The inventory is public and unfiltered by the
  represented object's privileges or schema USAGE, while normal catalog
  relation/column SELECT admission and held-statement rechecks still apply.
  Reads never create history or derive it from current ACLs or provider recipes.
- `pg_seclabel` (3596) exposes the five nonnullable PostgreSQL columns
  `objoid`/`classoid` (OID), `objsubid` (INT4), `provider`/`label` (TEXT),
  with no row OID. It reuses the initialized row/array types and validates
  captured source, owned shape and paired role authority even when empty.
  Textual bootstrap `pg_attribute` facts use C collation (950), independently
  of the result carrier's ordinary TEXT usage (default collation 100).
  PUBLIC SELECT and ordinary column grants/revocation apply before EOF.
  There are no installed security-label providers: valid SECURITY LABEL
  commands reject at execution with 22023 before target lookup or ownership.
  This includes NULL removal and conventional dummy/selinux spellings.
  Existing DDL command-start event triggers run before provider resolution for
  eligible target kinds and may veto first. Database, tablespace, role and
  event-trigger targets bypass those events; subscription remains eligible.
  Read-only/failed-transaction admission precedes callbacks. Provider failure
  suppresses command-end events and rolls back callback writes through the
  existing event journal, without adding label storage.
  No label map, provider registry, fake assignments or serializer exists.
  Reset and Restore use normal catalog bootstrap; Dump emits no label commands.
  With a real provider, labels would key object OID, catalog class, stable
  subobject attribute number and provider; rename would preserve identity,
  replacement/removal would affect only that key and drop would remove its
  labels. Those provider-backed effects and veto/target-ownership branches are
  unreachable here, not delivered successful attachment behavior.
  Shared-object labels (`pg_shseclabel`) and the `pg_seclabels` presentation
  view remain separate and are not exposed by this source.
- `pg_partitioned_table` (3350) projects eight attributes from real immutable
  partition descriptors through `Runtime.PartitionMetadata`, without a row store
  or DDL population hooks. Stable key attnums, selected operator classes, effective
  collations, strategy and immediate default-child identity belong to that
  captured frontier. The first seven attributes are nonnullable; the three
  numeric vectors own rank-one, lower-zero backing. Only `partexprs` is nullable
  in both the source schema and its registered `pg_attribute` facts: it retains
  original ordered definition nodes in the established logical expression-list
  carrier, not physical PostgreSQL node serialization.
  Source identity, captured type generations and descriptor references are
  validated even for empty captures; cancellation/invariant failures publish no
  prefix. Structural rows do not depend on target-table privileges, while catalog
  SELECT and guarded mutation admission retain centralized execution-time ACLs.
- `pg_index` projects all 21 attributes from one detached `index.Metadata`
  capture, transported through `Runtime.IndexMetadata`. Actual index identities,
  definition flags and publication state remain index-owner facts. Structural
  rows are independent of target-table privileges; catalog ACLs still apply.
  Typed vectors own numeric backing, with INCLUDE attributes only in `indkey`.
  Only `indexprs` and `indpred` are nullable: their immutable definition nodes
  retain ordered expression-list or scalar carriers and native logical structure,
  not physical PostgreSQL node serialization. Attribute ordinals, provenance and
  effective nullability follow this complete schema. `indisexclusion` projects
  the actual index definition's exclusion capability as a non-null BOOL at
  PostgreSQL attribute 8; `indisclustered` retains attribute 10.
  The runtime uses `ProjectIndexFacts`, not the combined SQL-deparse projection.
  Facts remain readable before CLUSTER and after failed/canceled maintenance
  even when its private current-body recipe refresh was correctly discarded.
  Original expression nodes stay frozen; no callable or body is executed.
  Exclusion `pg_constraint.connoinherit` projects explicit declaration provenance,
  not an inference from the current parent edge.
- `pg_attribute` composes effective stable-attribute storage/compression with
  immutable type facts, including scalar dropped history. Index opclass storage
  types substitute representation facts without erasing simple-key policies.
  Default compression is the non-NULL empty character value.
- Text-search rows project actual descriptor ownership, namespace, template
  helpers and ordered options. Empty dictionary options are typed SQL NULL;
  numeric nodes remain unquoted and strings use canonical SQL escaping. Builtin
  simple dictionary/template identities are pinned; allocated Snowball and
  standalone dependencies follow their actual namespace/template, not labels.
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
  Capture contracts include simple and all twenty-nine Snowball configurations,
  their full 570 mapping rows, and simple/Snowball template and dictionary seeds.
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
  Ordinary definitions have no constraint metadata; transition names retain NAME
  values and absent transitions/WHEN remain typed NULLs. User constraint definitions
  link their real distinct class2606 identity and immutable timing defaults.
  Optional FROM sets tgconstrrelid, never constraint confrelid; their contype`t`
  rows retain separate connames, local/noinherit flags and no constraint parent/
  index/key. Constraint definitions cannot name transition tables. Persistent
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
- `pg_replication_origin` (6000) is a real shared public catalog with non-null
  OID `roident` and TEXT `roname`. It projects actual explicit standalone and
  subscription-created registrations from the captured immutable shared index,
  including transaction-private effects. Canonical names use actual subscription
  OIDs, but subscription correlation alone never synthesizes a missing row. IDs
  are allocated class-local state. Capture validates exact source/type/role/engine
  authority and complete registration/name-index coherence before exposing rows.
  Replay progress remains separate immediate cluster state, not catalog columns.
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
- `pg_publication` (6104, row10107/array10106) is a real database-local catalog
  with ten nonnullable owned attributes: OID identity/owner, C-collated NAME,
  six BOOL options and internal CHAR generated-column policy. Its explicit ACL
  grants all owner table privileges and PUBLIC SELECT, without grant options or
  column grants. Current Execute admission governs direct and held reads.
  Capture authenticates exact catalog/role roots, backing engine and owned type
  generations, then projects actual immutable database-local definitions.
  Cancellation and invalid authority return no rows. Remote publication names
  on subscriptions never become local publication rows.
  Reads neither create definitions nor imply replication transport or contact
  publishers. Index presence is metadata, not a fabricated physical index.
- `pg_publication_namespace` (6237) is a real database-local catalog with three
  nonnullable owned OID fields: `oid`, `pnpubid`, `pnnspid`. Capture authenticates
  paired catalog/role roots and backing-engine ownership, including private
  transaction roots. Current SELECT admission governs direct and held reads,
  even when empty. Its explicit owner/PUBLIC ACL has no column grants. Population
  contains actual schema-publication mappings; ordinary schema/table DDL and
  subscription names never infer edges.
- `pg_publication_rel` (6106) is a real database-local catalog with exactly five
  fields in PG18.6 order: nonnullable OID `oid`, `prpubid`, `prrelid`, nullable
  PG_NODE_TREE `prqual`, and nullable INT2VECTOR `prattrs`. The owning
  `pg_attribute` facts preserve that same nullability: only the first three
  fields have `attnotnull` set. It exposes only actual
  explicit table-publication edges, never inferred schema/all-table or implicit
  partition edges. Explicit lists retain sorted stable attribute numbers; absent
  lists and filters are typed NULL rather than empty payloads. Owner privileges
  and PUBLIC SELECT are explicit, with no column grants or per-edge visibility
  filtering. Reading membership does not implement or start replication.
  Capture authenticates paired catalog/role roots, engine/source identity and
  owned type generations. Invalid sources/mappings and cancellation expose no
  partial rows through either the producer or the shared anonymous-source wrapper.
  Genuine immutable `stored.PolicyCarrier` values retain the actual bound filter
  expression and frozen presentation. Native structural output is BoundTree
  encoding, not PostgreSQL nodeToString byte compatibility; raw tree bytes are
  opaque across backends. Retained vectors/carriers survive accepted mapping
  replacement/removal while fresh captures observe current membership.
  Dedicated private definitions in `core/pg_publication_rel_test.go` cover
  registration/ACL/type relationships, empty and populated capture ownership,
  malformed current descriptors and mappings, retained predecessor/successor
  identity, cancellation at the validated-candidate final publication fence,
  and canceled populated captures. Select them with:

  ```sh
  make check RUN='^TestPGPublicationRel(RegistrationFacts|CaptureOwnership|MalformedMapping|CaptureLifetime|RejectedPublication|CaptureCancellation)$'
  make check RACE=1 RUN='^TestPGPublicationRel(CaptureOwnership|CaptureLifetime|RejectedPublication|CaptureCancellation)$'
  ```

  Dedicated external `TestReferencePGPublicationRel` shape, payload, lifecycle,
  privilege and feature-smoke definitions own public SQL/diagnostic proof; the
  broader publication command families remain supporting coverage.
- Publication membership inquiries reuse the same protected capture boundary.
  `pg_publication_tables` resolves current effective membership, including
  implicit partition descendants and current/future schema/all-table coverage.
  It enumerates and deduplicates presented relations separately per publication;
  overlap never duplicates a row or suppresses another publication's row.
  Its native relation identity is source-specific, not a PostgreSQL allocator
  constant. All four readers retain the same protected capture boundary.
  Direct metadata for each selected presented relation arrives as borrowed
  `PublicationTableFact` values through `Runtime.VisitPublicationTables`; core
  shares its projection with the publication-table inquiry, separately from
  DML-effective ancestry coverage. Physical attribute order produces owned typed
  `name[]` values; empty projections produce typed NULL `attnames`, matching the
  canonical aggregate. Absent filters are NULL; stored filters are deparsed, not
  evaluated against local data. Ineligible/internal/temp relations are excluded,
  but represented-table SELECT, schema USAGE and search-path visibility do not
  redact membership. The view's own current SELECT ACL still controls admission.
  These are local metadata rows, not WAL/network replication effects.
  Producers use the existing
  `PolicyTree`/`PolicyDisplay` bridges and never import the higher publication,
  stored-expression, routine or table command packages.
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
  `pg_attribute.attrelid` (one test per relation), `pg_class.oid` or
  `relname`, `pg_proc.oid`, `information_schema.routines.specific_schema`,
  `specific_name` or `routine_schema`, and `information_schema.parameters`'
  `specific_schema` or `specific_name` (domain columns, compared through
  their base-type relabel). Captures made with `Keep` are never shared; a
  statement with `Keep` may still read a shared full capture.
- Rows are immutable once returned: the engine may share one capture across
  statements with the same inputs, so neither producers nor consumers write
  into returned rows.
- Both `pg_proc` producers share all30 release-ordered columns; projection
  positions equal released attribute numbers minus one. Security, settings,
  support links, leakproofness, transforms, source and binary provenance come
  from immutable canonical declaration owners, not public names or execution
  admission. Configuration is a typed nullable, ordered one-based TEXT array.
  Metadata-only routines retain descriptor-owned released declarations without
  callable providers. SQL-backed routines require an actual language descriptor
  and project its address independently of its current label; native released
  internal/C/SQL declarations use fixed language identities, not name lookup.
  Host scalars likewise project their authentic associated language, while
  handler/converter utilities keep native Go's zero-language sentinel. The shared
  catalog projection also drives function-definition inquiry across language
  renames. Common typed layouts supply host formal names, default counts and
  checked parameter-default disclosure without a fabricated SQL body.
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
- `pg_stat_gssapi` (12257) consumes only core-classified detached real-client PID
  facts through `Input.GSSAPISessions`, never a session visitor or provider lock.
  The common source wrapper preserves five nullable typed columns and existing
  row/array identities. Every actual native client has false authentication,
  encryption and delegation flags and a NULL text principal; no transport fact
  is synthesized. Core applies current authorization to raw transaction membership
  before production. Unauthorized peers are absent. PUBLIC SELECT and ordinary
  read-only view diagnostics use the common relation path; this producer adds no
  backing-function EXECUTE admission or shared catalog-row cache.
- `pg_aios` captures the actual in-use AIO inventory as fifteen nullable typed
  attributes. Logical storage owns no physical AIO handles, so capture is empty,
  including after SQL work and native controls. Source identity, descriptor and
  owned type generations are validated even for an empty result. Ordinary view
  SELECT and current `pg_get_aios` EXECUTE are independent admissions; defaults
  grant only the bootstrap owner and inherited `pg_read_all_stats` authority.
  Its backing declaration supplies metadata and ACL authority, not a callable
  provider. Capture never manufactures workers, handle generations or counters.
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
- `column_udt_usage` projects seven nullable `sql_identifier` columns from live
  positive attributes of ordinary, view, foreign and partitioned relations,
  including catalog/provider and temporary relations. A declaration resolves
  exactly one domain hop through the shared column type facts; builtin,
  composite and array identities remain actual types. Only enabled ownership of
  that resolved type admits a row, independently of represented relation,
  schema and type ACLs. Source SELECT remains separately checked and initially
  PUBLIC. Source16260 reuses canonical row13314/array13313. Identifier values,
  names, database and role visibility belong to the same captured statement
  roots, even after a live source ACL revision. Actual declaration changes
  update population; routines and default/generated expressions add no invented
  dependency rows. Cancellation or malformed authority publishes no prefix.
  Same-lifetime held executions recapture current definitions; returned rows
  and metadata survive catalog replacement, which retains the ordinary held
  signature lifetime fence. Dump/Restore replays declarations and owners, never
  provider rows or the builtin definition. Structured released display uses
  normal path, quoting, routine-shadow and owner-disclosure rules.
- `information_schema_catalog_name` projects one nullable `sql_identifier`
  column, `catalog_name`, and exactly one non-NULL captured database scalar,
  including an internal empty string. Source16251 reuses canonical row13251
  and array13250 and the capture-owned NAME/domain generations. PUBLIC SELECT
  admits ordinary readers without a row-ownership filter; normal source ACL
  denial remains an error even under a false predicate. Its structured builtin
  definition uses ordinary display-path, quoting and disclosure rules.
- `domain_udt_usage` projects six nullable `sql_identifier` columns in released
  UDT-catalog/schema/name, domain-catalog/schema/name order. Both catalogs use
  the captured current database. Real domain descriptors, including unused and
  builtin domains, contribute their immediate `DomainBase` relationship; nested
  domains are not flattened, and enum/composite/array bases keep their actual
  type names. Arrays of domains are not domains and contribute no extra row.
  Only enabled ownership of the immediate base admits rows: inheritance and
  SET ROLE participate, but domain ownership, USAGE ACLs, schema access and
  column/routine references are not inclusion predicates. Ordinary source SELECT
  remains separate and initially PUBLIC. Source16249 reuses canonical row13339
  and array13338; domain cells, namespaces and role authority belong to one
  immutable statement capture. Malformed authority or cancellation returns no
  partial rows; an empty successful capture retains its owned schema. Held
  execution recaptures changes, while detached results survive lifecycle and
  native Reset/Restore. Controls replay real types and owners, not this builtin
  view's definition. The tokenized release definition shares normal path,
  quoting, shadowing and nonowner-disclosure behavior with other builtin views.
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
  nonzero callback dependencies use ordinary pin suppression. Stock handler
  pg_proc rows come from real metadata-only Procedure descriptors, independently
  of parser definitions and without standalone execution providers.
  `pg_proc.proretset` comes from actual routine declaration metadata.
  Its full row includes FLOAT4 `prorows` and `procost`, declaration `prosrc`,
  OIDVECTOR `proargtypes` built from each routine's input signature, and OID `provariadic`.
  Routine cost preserves explicit immutable builtin declaration costs, falling
  back to one for C/internal and one hundred for SQL/PL declarations.
  Nullable `probin` projects immutable `RoutineDisplay.Binary` when `HasBinary`
  is true, and SQL NULL otherwise; it never uses current extension membership
  or a runtime library lookup. The selected
  tablefunc and pageinspect producers retain their actual released
  `$libdir/tablefunc` and `$libdir/pageinspect` paths; unrepresented library
  declarations and internal routines project SQL NULL.
  SQL/PL formal names/types/modes come from `proc.Formals`; fixed native OUT
  types and complete names come from the actual builtin or installed declaration.
  Builtin input names have one immutable declaration owner. `parameters` and
  `pg_proc` share the ordered formal walk and name projection; argument count
  and identity remain input-only.
  `proallargtypes` and `proargmodes` retain all declaration slots in order through
  one typed-array materializer.
  Released declaration presence is independent: `RoutineDisplay.OmitAllArgTypes`
  preserves SQL NULL `proallargtypes` while retaining non-NULL `proargmodes`.
  It defaults false and is set for relation- and attribute-statistics restore's
  released declarations. The shared projector still visits/validates every formal,
  but does not allocate or materialize an omitted type array. Existing ANY
  variadics retain their explicitly declared all-argument arrays.
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
  descriptors retain their ordinary NULL default ACL representation.
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
  primitive carriers. `table_privileges`, `column_privileges`, `role_column_grants`, `role_table_grants`,
  `role_routine_grants`, `routine_privileges`, `role_udt_grants`, `udt_privileges`, `role_usage_grants` and `usage_privileges` use `sql_identifier`,
  `character_data` and `yes_or_no` from the captured type catalog.
  Numeric metadata likewise retains genuine `cardinal_number` domain values.
- `triggers` projects real relation-owned definitions through a captured stored
  presentation visitor. Name-order ranks partition by event, timing and
  orientation; TRUNCATE contributes no information-schema event row.
  Enabled ownership or non-SELECT table/column write/reference/trigger privileges
  expose rows; only enabled owners see WHEN expressions. Target-schema USAGE
  is not required; other-session temporary relations are excluded. All seventeen
  cells retain source-owned domains, including transition alias identifiers and
  typed NULLs for absent aliases and creation time.
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
  columns and one `character_data` column. Its inventory follows the genuine
  `pg_transform` type/language/direction definitions. Without an explicitly
  supplied host runtime and its materialized pairs it remains empty; ordinary
  types, languages, codecs and SQL routines never synthesize rows.
  The bootstrap owner and ordinary schema-USAGE/view-SELECT admission govern
  reads. There is no default PUBLIC SELECT grant and no row privilege predicate.
  Its released definition retains the direction joins, including explicit OID
  coercions of the regproc direction references, and UNION; automatic INSERT,
  UPDATE and DELETE are rejected by the shared view path.
- `column_privileges` and `role_column_grants` share one captured catalog/role
  frontier and project actual table/column ACL origins on live positive
  attributes. Their raw-origin UNION includes the grant-option bit before
  ownership determines grantability, so identical final rows retain multiplicity.
  Both expose enabled grantors or named grantees; `column_privileges` also
  exposes grantees whose resolved name is exactly `PUBLIC`, including the
  distinct pseudo-PUBLIC and named-role identities. The role view retains
  pseudo-PUBLIC origins only with an enabled grantor. Named grantees retain
  normal grantability; pseudo-PUBLIC never receives grant options.
  Neither uses schema-USAGE or data-access predicates. Membership closures
  and grantee/owner authority are cached within that immutable capture;
  cancellation discards the entire result.
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
- `udt_privileges` and `role_udt_grants` share a seven-column, nullable domain-typed
  projector of raw type USAGE origins as `TYPE USAGE` for actual reciprocal
  composite row types, including lookup-only catalog rows. Arrays, domains and
  other noncomposites do not contribute; reserved identities alone are not objects.
  Synthetic default ACLs retain raw absence; absent ACLs supply owner/PUBLIC
  origins and explicit-empty ACLs supply none. Enabled grantor or named grantee
  determines visibility without schema, relation-access or TEMP filters.
  `udt_privileges` additionally exposes every PUBLIC origin; the role view
  requires its grantor to be enabled. Named-grantee grant options or immediately
  usable ownership determine grantability, never observer ownership; PUBLIC is
  not grantable. Captured catalog/role authority owns domains and lazy membership
  caches, and cancellation discards the entire result.
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

`attributes` traverses only actual standalone composite relations and their live
positive attributes, retaining sparse physical ordinals. Reciprocal type/member
authority, source domains, database and immediately usable ownership or composite
type USAGE share the admitted catalog/role frontier. Schema, member-type and
relation SELECT privileges do not gate disclosure. All 31 nullable cells retain
their source-owned domains. Declared member identity/classification is distinct
from immediate-domain-base dimensional helpers; attribute defaults never borrow
domain defaults. Actual attribute collation suppresses only pg_catalog.default.
Invalid metadata and cancellation publish no prefix; returned rows remain owned
across publication and catalog replacement. Symbolic normal/pretty definitions
are presentation only and confer no write or execution capability.

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
Concurrent source additions must retain strict numeric ordering when combined:
backend memory (12142) precedes all-table statistics (12146); cumulative SYS
tables (12156) lies between xact ALL (12151) and xact SYS (12161), before
all-index statistics (12187). Cumulative function statistics (12279) precedes
xact function statistics (12284), followed by archiver (12289). Identity cutovers
must reposition the existing producer: registration presence alone is insufficient
for binary-search reachability, including unrelated sources between the old and
new IDs.

`catalog_source_specs.go` holds `Specs`, the ordered descriptor metadata for
every system relation (OID, namespace, name, columns with type, typmod,
dimensions, nullability and collation, flags). Spec order is the descriptor
registration order and differs from the registry order.

The `pg_statio_all_indexes` (12358) and `pg_statio_user_indexes` (12359) entries
share exactly one seven-column producer and follow numeric OID lookup order.
USER excludes pg_catalog/information_schema/case-sensitive pg_toast-prefix parent
namespaces before datum construction, Keep and Reader demand. Both use existing
row/array type identities, real SQLIndex parents, zero-default selected facts,
PUBLIC SELECT and live view ACL checks without target ACL filtering. USER's
structured wrapper preserves definition/dependency introspection and redirects
I/U/D errors to ALL, not utility errors. Core captures current authorization
and effective settings alongside catalog/role roots so the shared session
statistics Reader, including snapshot/cache policy, remains authoritative.

`pg_statio_all_tables` (12174), `pg_statio_sys_tables` (12179) and
`pg_statio_user_tables` (12183) are registered between the table-statistics and
index-statistics producers in numeric lookup order. Their descriptors reuse
row/array pairs12176/12175,12181/12180 and12185/12184.
The shared producer walks actual Relations, captures SQLIndex parent relationships once,
admits only ClassKind r/t/m, and rejects identity/schema/name keys before any
Reader demand. Eleven nullable attributes retain exact released types,
collations, typmods and origins. Heap projection delegates to TableBlockStatistics;
index sums use the existing IndexBlockStatistics through that same Reader.
Empty index sums and absent direct TOAST counters are typed NULL. A missing
TOAST identity reaches strict functions as NULL, unlike missing statistics for
a valid relation, which default to numeric zero. There is no TOAST descriptor
synthesis or underlying ACL filter.
Structured released view metadata supplies definitions and read-only behavior.
SYS applies exact `pg_catalog`/`information_schema` equality or case-sensitive
`pg_toast` prefix membership before identity allocation and heap/index demand.
USER applies the exact case-sensitive complement of SYS before identity
allocation, Keep and Reader demand. Named ALL/USER rows have identical eleven
cells under one snapshot; SYS excludes those user identities. Both wrappers'
I/U/D diagnostics name underlying ALL; TRUNCATE/COPY retain the selected name.
Own-view SELECT is checked live, including held prepared execution;
underlying ACLs do not redact.

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
   and implement it on `catalogrt.Runtime` (or its core `Host`). If the engine must
   choose a different catalog, role read or unlock policy for the OID, add a
   case to `openCatalogSourceLocked` in the engine.

## Runtime capability

`Runtime` is the narrow interface the engine implements per call
(`catalogrt.Runtime`). Methods take and return values, never engine structs.
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
`MaintenanceFact.ForeignAnalyzed` distinguishes genuine foreign population/page
estimates from absent information and exact heap tuples. `pg_class.reltuples`
uses the separate floating TotalRows, never sample size; `relpages` converts the
provider's page estimate to the catalog's bounded integer representation.
`MaintenanceFact.Imported` carries all four exact imported relation fields from
the same capture frontier, including transaction-private masked deltas.
`pg_class` derives ordinary defaults/maintenance first, then overrides only
present fields, preserving INT4/FLOAT4/INT4/INT4 result metadata. Explicit clear
produces `(0,-1,0,0)`; an absent mask remains ordinary derived state. Fractional
tuples and negative integer fields are estimates, never physical row counts.
Publication/immutable snapshot units use `make check RUN='^TestRelationStatistics'`;
public coverage is `make check RUN='^TestReferenceRelationStatistics'` in both
native and pinned-reference modes.

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
| `Maintenance`, `TableStatistics`, `SequenceIOStatistics`, `DatabaseFrozenXID`, `VisitVacuumProgress`, `VisitClusterProgress`, `VisitCopyProgress`, `VisitCreateIndexProgress` | immutable logical maintenance, transaction-owned cumulative facts, and authenticated active-work redaction |
| `XactRelationStatistics` | copied physical-owner pending access facts plus live current tuple frames, independent of cumulative Reader cuts |
| `ExpressionTree` | structural `pg_node_tree` output of bound expressions |
| `PolicyTree`, `PolicyDisplay` | validated relation-owned policy carriers and current-identity display without evaluation |
| `VisitPublicationTables` | authoritative captured presented-relation metadata shared with the inquiry; core owns expansion/projection, producers own typed row serialization |
| `VisitCheckAttributes`, `ExpressionReferences`, `ViewReferences`, `IndexReferences`, `PartitionReferences`, `IndexSimpleAttribute`, `IndexKey` | walks over engine plan and expression nodes |
| `ForeignKeyComparisons` | the binder's foreign-key comparison operators (`pg_constraint`) |
| `ViewDefinition`, `IndexDefinition`, `VisitRuleDefinitions` | current-identity stored definition rendering; rule visitor omits exact `_RETURN` before rendering |
| `ColumnExpression`, `ColumnUpdatable` | captured stored-expression display and metadata-only view/rule/foreign capability |
| `ViewCapabilities` | structural event and actual trigger facts, bridging stored views and FDW handler interfaces without query execution |
| `Pinned`, `OperatorSeeded`, `OperatorDependencies`, `InstalledProcedure` | pin decisions, builtin operator inventory and dependencies owned by `routine`, and genuine installed routine ABI dependencies independent of current membership |
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

`pg_stat_progress_cluster` retains its exact twelve typed columns and view/
`_RETURN` metadata, with native identities independent of initdb allocation.
Simulated CLUSTER starts no physical worker, so the validated, cancellation-aware
producer is honestly empty, including during heap admission and targeted recipe
maintenance. No runtime registry, fabricated phases or counters are exposed.


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

- `pg_description` projects immutable local descriptor comments. Extension text
  contributes exactly its header OID, class 3079 and subobject zero; absence
  produces no row and never contributes to `pg_shdescription`.
  Text-search dictionary text contributes its genuine dictionary OID, class 3600
  and subobject zero; removing text removes the row and class-qualified inquiry
  returns NULL. No template or helper comments are inferred from membership.
- `pg_am`: live global method descriptors with owned typed cells per capture;
  family→method and method→handler dependencies use shared selectors, and
  references to pinned stock handlers are suppressed, not fabricated.
- `pg_depend`: repeated routine result/formal/support declarations share one
  normal declaration edge per referenced address. Checked standard-body and
  default-expression dependencies retain their separate capture-pass rows,
  including column/subobject identities. Materialization suppresses pinned
  prerequisites without globally deduplicating these independent passes.
  Released provider body helpers use that same lower catalog dependency visitor;
  language-handler and common membership edges retain their separate meanings.
  Installed scalar type facts validate through the catalog owner's registered
  type and procedure class providers, including each genuine codec declaration.
  `VisitTypeProcedureDependencies` shares the distinct nonzero input, output,
  receive, send, typmod, analyze and subscript NORMAL references from those
  authentic `TypeFacts` with DROP discovery and definition consumers. Visitation
  uses fixed local storage, stops on request and propagates cancellation or
  provider failure. Dependent domains and arrays contribute only their own
  resulting facts; array-to-element INTERNAL ownership remains separate.
  The supplied catalog must own the exact visited SQLType generation. Retirement
  callers supply their coherent retained metadata projection, not a predecessor
  catalog paired with a staged type; provider and codec validation still read
  that same supplied graph, without admitting foreign or stale generations.
  Missing registered owners are errors, not skipped validation. SRF and UUID
  declaration metadata use that same procedure owner; the extension metadata
  interface is not a callable-validation capability. This projection layer
  does not import extension implementation packages.
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
- `pg_stats` (12053): public, detached 17-column statistics projection over the
  same session-resolved canonical root used by the planner. Local/inherited
  rows remain separate; missing kinds are typed NULL. ANYARRAY metadata wraps
  genuine typed arrays rather than changing the schema per row. Each live
  attribute requires SELECT, independently of MAINTAIN, and active row security
  excludes the row. Reads include private restore/clear deltas but never perform
  ANALYZE or authorize writes. Tests order rows explicitly.
  Descriptor validation grants canonical catalog-result ANYARRAY authority only
  to its five released name/attribute pairs (8, 10, 12, 15 and 17), under the
  fixed pg_catalog view identity and current type generation. Other columns and
  stored relations cannot inherit that allowance; pg_attribute and extended
  statistics retain their existing independent bounded carrier rules.
  Its source capture uses the same locked current catalog/role authorization
  path as pg_statistic_ext_data; the visitor receives that captured principal
  and transaction journal, not the definition-only default source adapter.
  Canonical/default and pretty `pg_get_viewdef` forms use the shared explicit
  name/identifier fragment inventory, including current routine identities and
  quote-all formatting. Independent pinned-18.6 SQL fixtures exercise both forms
  under both quote settings in `TestReferenceAttributeStatisticsViewDefinition`.
- `pg_stat_all_indexes` (registry relation12187, rowtype12189): exactly nine nullable
  OID/OID/NAME/NAME/NAME/INT8/TIMESTAMPTZ/INT8/INT8 columns. Enumerate actual
  SQLIndex descriptors joined to r/t/m parents, regardless of parent schema/table
  privileges or readiness/validity. PUBLIC SELECT belongs to the view itself;
  no per-field role redaction applies. Missing activity is0/NULL, never an absent
  identity. Safe pushed identity/name `Keep` rejection precedes the shared Reader
  fetch, so cache mode does not freeze unrelated filtered-out indexes.
  Dynamic statistics never enter the catalog-state CaptureCache. Exact displayed
  current names/identities follow rename/move/drop without changing retained
  result values. Bootstrap HasIndexes flags do not create native index inventory.
  `pg_stat_sys_indexes` (relation12192, rowtype12194, array12193) is registered
  on that same producer and Reader, with all nine typed columns. SYS admits
  exactly parent namespaces `pg_catalog`, `information_schema`, or the entire
  case-sensitive `^pg_toast` prefix (including `pg_toasty`), before allocating
  a row or demanding statistics. ALL remains unfiltered; the complementary USER
  predicate can be queried over ALL but no named USER view is registered.
  The shared r/t/m parent-kind check precedes demand; native has no physical
  TOAST relation descriptors. Neither view invents ordering or bootstrap indexes.
  SYS's own current SELECT ACL applies even to prepared/WHERE FALSE reads,
  without parent redaction or requiring the caller's ALL SELECT privilege.
  Missing counters/time remain0/NULL/0/0. Canonical/pretty dependency-aware
  display and read-only events share builtinViews. SYS INSERT/UPDATE/DELETE
  diagnostics name the underlying `pg_stat_all_indexes` join view; TRUNCATE
  and COPY diagnostics retain `pg_stat_sys_indexes`.
- `pg_stat_all_tables` (registry relation12146, rowtype12148, array12147) and
  `pg_stat_sys_tables` (relation12156, rowtype12158, array12157) share
  exactly 30 nullable typed columns in PostgreSQL 18 order and the same provider.
  The current catalog supplies r/t/m/p membership and names, including inaccessible schemas/tables;
  there is no parent ACL filter or per-field redaction. The view's own SELECT
  ACL remains ordinary, including prepared and WHERE FALSE reads. Identity,
  schema and relation-name Keep rejection precedes every table/index Reader
  access. An attached-index grouping from SQLIndex.Parent supplies SUM scans,
  MAX last scan and table bitmap fetches plus SUM simple index fetches.
  No attached indexes means all three index outputs are NULL; untouched indexes
  instead give numeric zero counters and NULL time. Missing activity gives
  numeric zero and NULL event times, not an all-NULL row. Retained results remain
  immutable across clear, rename, drop and replacement.
  Sequential counters follow real demand before filtering; tuple attempts can
  include rolled-back operations. Completed manual maintenance is immediate,
  not manufactured by reads. HOT/newpage are declared zero logical optimization
  estimates, live/dead are logical version estimates, and FLOAT8 elapsed totals
  are milliseconds. No automatic worker is synthesized. The released grouped
  definition remains authoritative for display and GROUP BY read-only diagnostics.
  SYS includes only exact namespace names pg_catalog/information_schema and the
  case-sensitive prefix pg_toast; USER is the complementary ALL subset. This
  classification and identity/schema/name Keep rejection precede Reader demands
  for both the table and its attached indexes. SYS's own current SELECT privilege
  is independent of ALL and parent table/schema privileges; no fields are masked.
  SYS INSERT/UPDATE/DELETE diagnose the underlying grouped `pg_stat_all_tables`;
  TRUNCATE and COPY retain the named SYS target. Neither projection promises
  traversal order without ORDER BY or fabricates native TOAST descriptors.
  Cumulative and transaction-local SYS display share the same column/source
  fragment formatter. Canonical and pretty forms retain dependency-aware
  identifier quoting and search-path qualification; pretty removes the
  redundant outer predicate parentheses without changing membership.
- `pg_stat_xact_all_tables` (registry relation12151, rowtype12153, array12152),
  `pg_stat_xact_sys_tables` (relation12161, rowtype12163, array12162) and
  `pg_stat_xact_user_tables` (relation12170, rowtype12172, array12171) share
  the owner-local pending provider without acquiring a cumulative Reader:
  twelve nullable columns: OID relid, NAME schemaname/relname, then INT8
  seq_scan, seq_tup_read, idx_scan, idx_tup_fetch, n_tup_ins, n_tup_upd,
  n_tup_del, n_tup_hot_upd and n_tup_newpage_upd. Current r/t/m/p membership
  and names have no parent ACL filter or field redaction; the view's own
  SELECT ACL is enforced normally. Identity/schema/name Keep rejection
  precedes table and attached-index inquiries. Index SUMs are NULL without
  attached indexes and zero for attached untouched indexes; fetches combine
  index simple fetches and table bitmap fetches. HOT/newpage are deterministic
  zero physical optimization estimates. Rows are detached values with no
  portable order absent ORDER BY. SYS admits only namespace names pg_catalog,
  information_schema and the case-sensitive prefix pg_toast; USER admits their
  exact complement, including TEMP namespaces. Namespace exclusion precedes
  identity allocation and runtime inquiries, not just output filtering. Current
  rename/schema moves and rollback change names/membership without changing OIDs.
  Each view's own current relation/column SELECT ACL applies, including held
  prepared and WHERE FALSE reads, without requiring SELECT on ALL or access to
  underlying tables/schemas. All three consume the authenticated physical writer
  plus live owner/stage counts exactly once, never peer or published history.
  Executed attempts, including NULL payloads and aborted child work, are not
  committed row counts. Successful publication removes pending facts; a throttled
  unreported batch can cross COMMIT/ROLLBACK. Reader modes/clear neither freeze
  nor flush them; collection settings gate new events, not collected visibility.
  Repeated reads never synthesize maintenance timestamps/counts; those exist only
  in cumulative views and change on actual command completion.
  SYS/USER have dependency-aware default/pretty definitions over ALL, including
  quote_all_identifiers and search-path shadow handling. Their INSERT/UPDATE/DELETE
  diagnostics name grouped `pg_stat_xact_all_tables` (55000, GROUP BY detail and
  operation-specific trigger/rule hint), even for empty mutations. TRUNCATE and
  COPY FROM retain the named view's own identity and 42809 diagnostics.
- `pg_stat_xact_user_functions` (relation12284, rowtype12286, array12285):
  six nullable attributes in released order: OID funcid, NAME schemaname/funcname,
  INT8 calls, FLOAT8 total_time/self_time. Current non-INTERNAL procedures with a
  present pending function entry qualify, including admitted calls0/times0; absent
  activity is absent rather than fabricated zero or masked NULL. Identity/name
  Keep rejection precedes inquiry. The authenticated physical owner supplies copied
  pending facts, never published/peer facts or a cumulative Reader. Tracking changes
  affect new collection, not membership of already collected events. Completed
  events survive rollback; legitimate publication removes pending entries, while
  throttled batches can cross transaction ends. Names follow current catalog DDL.
  Nonowner/schema/underlying-routine ACLs do not redact distribution, but current
  view/column SELECT and invoker EXECUTE on all three retained VOLATILE backing
  expressions remain mandatory, including identity/count/LIMIT0 reads. Literal
  FALSE eliminates backing expression initialization, not own-view SELECT.
  Only timings approximate physical behavior: one logical millisecond per completed
  tracked invocation, total inclusive of tracked descendants and self own-only,
  with same-key recursion overlap removed. Genuine provider elapsed time is retained.
  Normal rows have no NULL cells; outer joins can naturally produce typed NULLs.
  Detached results have no portable order without ORDER BY. The structured default/
  pretty LEFT JOIN definition is display-only and yields ordinary join-view
  read-only diagnostics, never public getter execution.
- `pg_stat_user_functions` (native relation12279, rowtype12281, array12280)
  shares the six-column typed FunctionFact projection, but uses shared membership
  and published counters from one Reader cut. These initialized-release native
  identities are not cross-server OID allocation promises. Current non-INTERNAL
  identities/names qualify only when present in that cut. First admission prepares
  shared calls0 without publishing pending counts/timing; disabled/excluded calls
  create no entry. Only timing uses
  deterministic logical invocation milliseconds, inclusive total and own self;
  genuine provider elapsed measurements remain independent. Ordinary missing
  statistics produce no row, while outer joins retain genuine typed NULLs.
  Distribution is unredacted by observed routine/schema/owner ACLs. Current
  view/column SELECT and retained STABLE backing-expression EXECUTE are separate
  authority: calls is a live membership dependency, timing getters are demanded
  by live outputs/predicates/sorts, and false input retains only surviving
  targets. LIMIT0 retains initialization. Structured released default/pretty
  display and read-only join-view diagnostics use the existing registry.
  Rows are detached; no sequence is promised without ORDER BY.
- `pg_rules`: projects represented stored relation rules as NAME/NAME/NAME/TEXT,
  excluding only the exact `_RETURN` name. Definitions are public metadata:
  relation ACLs, schema USAGE, search path and another session's TEMP ownership
  do not filter rows. Captured presentation controls current-name SQL and own
  TEMP spelling; reads never authorize or execute rule actions.
- `pg_transform` (3576): checks cancellation and the five carrier types, then
  projects genuine provider-validated type/language pairs. Its first three fields
  are OID values; FROM/TO procedure references are REGPROC values, including a
  non-NULL REGPROC zero for an absent direction. Empty inventory still produces
  a non-nil empty source. Reads never install or execute a callback.
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
- Table-statistics views read the shared cumulative provider through the current
  transaction reader, separately from physical `Maintenance`. ALL's
  `relid`/`schemaname`/`relname` safe-key rejection precedes any fact fetch,
  preserving per-object cache semantics. These dynamic rows never enter the
  shared catalog capture slots.
  Completed ANALYZE/VACUUM publish directly into that same shared provider,
  independently of the ordinary tuple writer's outside-transaction gate.
  `none` sees the report immediately; cached/snapshot facts still require clear
  or transaction end. Report policy does not gate physical optimizer work.
  Progress views report real active phases with authenticated-owner visibility:
  unavailable CREATE INDEX totals are zero for authorized observers, while
  restricted work details are typed NULL.
  FULL uses the actual cluster-progress source, not the ordinary vacuum source.
  Tuple prefixes and completed rebuilt roots are genuine logical work; absent
  physical block work reports zero. `pg_class.relfilenode` projects the catalog's
  current logical storage incarnation without adding physical file inquiries.
- `pg_statio_all_sequences`, `pg_statio_sys_sequences` and
  `pg_statio_user_sequences` are PUBLIC-readable, read-only five-column sources:
  OID/NAME/NAME/INT8/INT8, all nullable metadata, five non-NULL cells per row.
  The shared producer enumerates actual sequence descriptors, including private
  and other-session TEMP identities, without schema-USAGE, sequence-ACL or
  activity filtering. Missing activity is zero, not privilege-redacted NULL.
  The sys slice includes exactly `pg_catalog`, `information_schema` and the
  case-sensitive `pg_toast` prefix; `pg_toasty` qualifies, while `pg_temp_N` and
  `PG_TOAST` do not. The user slice is its exact complement. Namespace
  classification and identity/name rejection precede the transaction reader, so
  excluded identities cannot initialize its cache or snapshot. With published
  tracked encounters N, all three sources share the simulated one-page
  projection (0,0) for N=0 and (1,N-1) otherwise: cumulative logical residency,
  not physical storage or sequence CACHE size. Current descriptors own membership
  and names, not retained statistics keys. Released definitions own presentation
  and non-updatability; sys/user DML reports the underlying non-updatable all
  view, while TRUNCATE and streamed COPY retain the original target name.
  No executable block-getter capability is registered. Rows have no implicit SQL
  ordering.
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
- `information_schema.foreign_data_wrapper_options`: actual wrapper options in
  four nullable source-owned domains (three `sql_identifier`, one
  `character_data`). Immediately inherited ownership or effective wrapper USAGE
  exposes every configured value, including passwords and empty strings.
  Unauthorized readers receive no rows, never masked or NULL option values.
  Only real wrapper descriptors contribute; server, table, column and mapping
  options and handler registration do not. Paired catalog/role captures and owned
  domain carriers survive later publication; cancellation discards every row.
  Metadata reads neither execute handlers nor contact foreign servers.
- `information_schema.foreign_data_wrappers`: one row per actual wrapper, including
  optionless wrappers, in three nullable `sql_identifier` domains and two nullable
  `character_data` domains. Captured database, wrapper and owner names accompany
  a typed NULL library and language `c`. Immediately inherited ownership or
  effective wrapper USAGE admits the complete row; unrelated object rights do
  not. Owned rows retain their paired catalog/role authority across publication.
  Options, dependents and handler registration never create or multiply rows;
  metadata reads do not execute handlers or contact foreign servers.
- `information_schema.foreign_servers`: one row per actual server joined to its
  wrapper and server owner, in five nullable source-owned `sql_identifier` and
  two nullable `character_data` domains. Captured database and names accompany
  the stored type/version; absent values remain typed NULL, distinct from empty
  strings. Immediately inherited server ownership or effective server USAGE
  admits the complete row, independently of wrapper or dependent-object rights.
  Paired immutable catalog/role captures retain owned rows across publication;
  cancellation discards partial results. Options, mappings, tables and handler
  registration neither create nor multiply rows. Metadata never invokes handlers
  or performs remote I/O.
- `information_schema.foreign_table_options`: one row per actual table option in
  four nullable `sql_identifier` domains and one nullable `character_data` domain.
  The shared `infoRelationVisible` predicate admits enabled ownership, any
  qualifying table right or a qualifying right on any live positive attribute;
  schema/server/wrapper USAGE and MAINTAIN alone do not qualify. Eligible readers
  receive all keys and values, including passwords and empty strings; denied
  readers receive no rows, never masked values. Only `Foreign.Options` contributes:
  column/wrapper/server/mapping options are not inherited, and zero-column tables
  still contribute their table options. Captured catalog, roles, database and
  source-owned domains retain immutable rows across publication; cancellation
  publishes no prefix. Metadata reads neither invoke handlers nor perform I/O.
  Its annotated default and pretty definitions retain distinct released SRF-field
  cast parentheses; neither format is synthesized from the other at runtime.
- `information_schema.foreign_tables`: one row per actual foreign relation,
  including optionless and zero-column tables, in five nullable source-owned
  `sql_identifier` domains. The shared `infoRelationVisible` predicate admits
  enabled ownership or a qualifying table/live-column privilege, not MAINTAIN
  or unrelated schema/server/wrapper USAGE. The table's stored server reference
  supplies the actual server name after server/wrapper association validation;
  server eligibility is not a second disclosure gate. Both catalog cells share
  the captured database value. Denied tables disappear entirely; options and
  mappings neither contribute fields nor multiply rows. Coherent catalog/role
  captures and domain carriers retain immutable rows across publication, while
  cancellation publishes no prefix. Its explicit default and pretty builtin
  definitions refer to `_pg_foreign_tables` only for display, not as a fabricated
  executable source. Metadata reads never invoke handlers or perform remote I/O.
- `information_schema.foreign_server_options`: actual server options in four
  nullable source-owned domains (three `sql_identifier`, one `character_data`).
  Immediately inherited server ownership or effective server USAGE exposes all
  configured values, including passwords and empty strings; denied readers get
  zero rows, not masked values. Wrapper, table, column and mapping options never
  contribute. Eager owned rows retain the paired immutable catalog/role capture
  across later publication; cancellation discards every row. Metadata reads
  neither create servers, execute handlers nor contact external servers.
- `key_column_usage`: foreign-key positions follow the supporting index's
  ordered keys; visibility is owner inheritance or a column privilege.
- `constraint_column_usage`: CHECK columns come from `ExpressionReferences`,
  never a second walker; owner-role inheritance on the table is the only
  visibility predicate.
- `constraint_table_usage`: six nullable `sql_identifier` columns identifying the
  projected table and actual constraint. Each PK/UNIQUE descriptor contributes
  one owning-table row; each FK contributes one referenced-table row with its
  child's constraint namespace/name. Identical tuples from separate constraints
  survive: no DISTINCT, column expansion or fabricated unique-index constraint.
  Only projected ordinary/partitioned tables qualify, and inherited ownership of
  that table alone controls visibility, including peer TEMP tables. Child
  ownership, table/column grants, schema USAGE, RLS and search_path do not
  substitute for enabled target ownership. CHECK/NOT NULL never contribute.
  Capture uses paired immutable catalog/role roots and the current identifier
  domain; cancellation or invalid authority discards the entire prefix. The
  native source OID16252 links the existing row13329/array13328 identities.
- `column_column_usage`: generated-column dependencies from the shared
  `ExpressionReferences` walker over analyzed (not folded) expressions,
  deduplicated per generation; enabled relation-owner membership is the only
  visibility predicate, evaluated once per relation.
- `view_column_usage`: seven nullable capture-owned `sql_identifier` fields,
  using the stored view's validated direct SELECT-rule references. Whole-relation
  dependencies contribute no columns; stable attribute numbers resolve current
  names after rename or dense-slot compaction, including supported `tableoid`.
  The shared collector deduplicates complete addresses and omits pinned objects;
  ordinary views referencing eligible r/v/f/p targets contribute distinct
  view/target/attribute rows, excluding self edges. Enabled ownership of the
  referenced relation alone controls disclosure, not defining-view ownership,
  SELECT grants, schema USAGE or search path. Capture never executes definitions
  or traverses routine bodies or unrelated rewrite rules. Database, namespaces,
  domain generations and origins belong to one immutable statement frontier;
  errors and cancellation discard every row, including an already-built prefix.
  Native source16254 links the existing row13491/array13490 identities.
- `view_routine_usage`: six nullable capture-owned `sql_identifier` fields,
  identifying the defining ordinary view and its directly referenced routine.
  Stored SELECT-rule queries supply edges through `ViewReferences` and the
  shared `QueryDependRefs` collector, which deduplicates addresses and omits
  pinned objects. Routine identity constants and aggregate identities qualify
  without a routine-kind filter; called bodies, operator/aggregate support
  implementations and non-defining rewrite rules do not invent direct edges.
  Enabled ownership of the referenced routine alone controls disclosure, not
  defining-view ownership, SELECT, EXECUTE, schema USAGE or search path.
  Source descriptor, catalog, role authority, database name and canonical
  identifier-domain generation share the captured inquiry frontier. Invalid
  authority, missing providers and cancellation discard every row, including
  an already-built prefix; valid empty captures remain non-nil. Native
  source16255 links the existing row13496/array13495 identities. The builtin
  definition inventory owns displayed SQL, not executable dependency authority.
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
- `applicable_roles` (16257): one inherited closure selects visible grantees;
  allocation-free edge enumeration emits actual member/target/ADMIN triples,
  not closure edges. Grantor duplicates collapse only when ADMIN agrees. The
  paired database owner contributes the implicit `pg_database_owner` NO edge
  through the same filter/emitter. Actual current superusers see all live edges;
  inherited SUPERUSER attributes and edge SET flags do not bypass visibility.
  Source-owned identifier/yes-or-no domains share one flat result backing.
  Invalid authority or cancellation publishes no prefix; no-edge ordinary
  readers receive a typed nonnil empty result through normal source admission.
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
including `information_schema.columns`, `information_schema.domains`, `information_schema.domain_udt_usage`, `information_schema.column_udt_usage`, `information_schema.information_schema_catalog_name`,
`information_schema.user_defined_types`, `information_schema.attributes`, `information_schema.tables`,
`information_schema.usage_privileges`, `pg_catalog.pg_policies` and
`pg_catalog.pg_rules`, with classification and per-output capability facts.
Every represented catalog view, including a newly registered virtual source,
must have a real canonical definition in this inventory: `views` and `pg_views`
capture all candidate definitions before outer query filtering. Publication
tables retain the released attribute aggregation and publication-table inquiry
references here; execution remains owned by the registered native producer.
Its canonical literal fragments contain explicit object-name and identifier
occurrences. `pg_rules` uses the same definition authority as both consumers;
its `pg_get_ruledef(oid)` reference captures stable routine identity, while
column and relation aliases remain identifier-only annotations. Its structural
update and insert flags are NO; this does not change rule visitation or DDL.
`SummaryViewDefinition` supplies canonical default metadata;
`BuiltinViewDefinition` selects explicit default or pretty fragments and formats
their occurrences using captured naming and identifier formatters. UDT, COPY,
CLUSTER/FULL and CREATE INDEX progress inquiries own both released forms; absent pretty
fragments never become a synthesized definition. The same runtime authority
renders `pg_views` and `views` in default
format and serves the boolean `pg_get_viewdef` presentation selector.
`cluster_progress.go` owns the single CLUSTER/FULL source, canonical relation
OID12208/rule OID12209 and REL_18_6 default/pretty projection, including command/
phase CASEs, OID cast and parameter aliases. Its real FULL visitor uses the
current private catalog runtime and authenticated viewer; this metadata grants
no simulated CLUSTER execution provider. COPY, CLUSTER/FULL, CREATE INDEX,
database statistics and subscription statistics declare native `_RETURN`
identities, stamped into immutable relation descriptors during bootstrap.
The rewrite declaration regression checks this complete view/rule inventory
explicitly, rejecting missing, unexpected or mismatched identities while
retaining every declaration and ownership assertion.
Rewrite projection, object-name/address lookup and dependency ownership use `ReturnRuleOID`; the
rule has an internal edge to its view. Released backing catalog/routine references
are bootstrap-pinned, not fabricated dependency objects or executable payloads.
Return-rule ownership is collected before ordinary catalog-row dependency
suppression; the existing pinned-object policy still filters backing references.
`pg_get_ruledef` wraps those same owned SELECT bodies without changing leading
space, wrapping or body parentheses; this display authority grants no stored-view
execution. The CREATE INDEX pretty fragments omit the default form's redundant
cast and concatenation parentheses, as independently observed on PostgreSQL 18.6.
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

Detached archiver projection, typed absence and source authority use
`make check RUN='^TestPGStatArchiverProjection$'` in `enginetests/`.

Subscription identity/absence, authenticated source rejection and retained
type-generation ownership use `make check RUN='^TestPGStatSubscriptionSource$'`
in `enginetests/`; composed provider lifetime witnesses live with core.

Replication capture/ownership units use
`make check RUN='^TestPGStatReplication(Capture|Ownership)$'`: exact twenty-field
owned metadata, nonnil empty rows, malformed/foreign/stale authority rejection,
cancellation-first publication and retained Reset generations. Public SQL proof
belongs to `reference/session_state` under `TestReferencePGStatReplication`;
the existing `TestReferenceServerProtocol` owns rejected replication startup.

SSL descriptor, typed absence, current principal authority and detached projection
use `make check RUN='^TestPGStatSSLDescriptorAndProjection$'` in `enginetests/`.

Build-configuration capture/ownership units in `enginetests/` use
`make check RUN='^TestPGConfig'`. They retain real catalog/type generations,
reject forged capture authority and verify the noncallable backing declaration
and immutable owner-only initial privileges.

Identity-file producer authority/ownership units use
`make check RUN='^(TestPGIdentFileMappingsCapture|TestPGIdentFileMappingsOwnership)$'`.
Parser lexical/include/depth/cancellation/large-line units use
`make check RUN='^TestIdentFileParser'`. SQL acceptance and feature smoke live in
`reference/catalogs` under `TestReferencePGIdentFileMappings`, in both selected
native and pinned-reference modes.

Rewrite schema, sealed builtin declaration and atomic-capture units use
`make check RUN='^TestPGRewrite'`.
Shared-dependency producer authority, unsigned encoding and canonical visitor
units in `enginetests/` use
`RUN='^Test(PGShdepend|RoleDependencyVisitor|DefaultACLDependencyAddress)'`.

Producer registry ordering and exhaustive binary-search reachability use
`RUN='^Test(ProducerRegistryLookup|CatalogProducerLookup)$'`. Adding a source must preserve existing OID
identities and lookup reachability, independently of descriptor installation order.

Package units here run without an engine. Producer behavior tests need an
engine-seeded catalog, so they run on a composed engine in `enginetests/`
and reach producers through the real registry. Producer tests that drive core's
capture runtime live in `core/enginetests/pgcatalog` (system catalogs) and
`core/enginetests/infoschema` (information schema), or in `core/` while they
need core accessors; see the
[core Testing section](../core/README.md#system-catalog-producer-contract). Public
catalog SQL belongs in `reference/catalogs` and its sibling catalog packages
(large objects in `reference/object_grants`, settings in
`reference/session_state`); follow
[docs/TESTING.md](../../../docs/TESTING.md) and add `REFERENCE=1` for the oracle.

Prepared statement metadata ownership and public scenarios share the family
selectors `RUN='^TestPGPreparedStatements'` and
`RUN='^TestReferencePGPreparedStatements'`.

Partition descriptor/source authority, cancellation and retained typed captures
use `RUN='^TestPGPartitionedTable'` in `core/`; public catalog SQL shares
`RUN='^TestReferencePGPartitionedTable'` in `reference/partitions`.

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
- Producer units (engine): `RUN='^Test(PGLargeobject|PGTransform|PGSettings|Info(UserMappings|KeyColumnUsage|ConstraintColumnUsage|ConstraintTableUsage|RoutineSequenceUsage|RoutineRoutineUsage|CharacterSets|Collations))'`
- Column-domain usage producer units (`core/enginetests/infoschema`): `RUN='^TestInfoColumnDomainUsage'`
- View-column dependency producer units (`core/enginetests/infoschema`): `RUN='^TestInfoViewColumnUsage'`
- View-routine dependency producer/capture/cancellation and stored-call identity
  units (`enginetests/`): `RUN='^TestInfoViewRoutineUsage'`
- Element declaration producer, nominal boundaries, captured generations and
  cancellation units (`enginetests/`): `RUN='^TestInfoElementTypes'`
- Stored-view consumer and retained TEMP reference authority:
  `RUN='^(TestCatalogSourceRuntimeViewReferencesConsumer|TestCatalogSourceRuntimeViewReferencesRetainedTemporary)$'`
- View-routine public scenarios: `RUN='^TestReferenceInfoViewRoutineUsage'`
- Foreign-wrapper option producer units (`core/enginetests/infoschema`): `RUN='^TestInfoForeignDataWrapperOptions'`
- Foreign-wrapper producer units (`core/enginetests/infoschema`): `RUN='^TestInfoForeignDataWrappers'`
- Foreign-server producer units (`core/enginetests/infoschema`): `RUN='^TestInfoForeignServers'`
- Raw foreign-server catalog units (`core/enginetests/pgcatalog`): `RUN='^TestPGForeignServer(Producer|Authority|Cancellation|Ownership)$'`
- Foreign-server option producer units (`core/enginetests/infoschema`): `RUN='^TestInfoForeignServerOptions'`
- Installed text-search template producer units: `RUN='^TestPGTSTemplate'`
- Type procedure dependency ownership and retirement (composed engine):
  `RUN='^TestTypeProcedureDependencies'`.
- Wait-event definition units: `RUN='^TestPGWaitEvents'`
- Wait-event definition public scenarios: `RUN='^TestReferencePGWaitEvents'`
- Information schema: `RUN='^TestReferenceInfo(UserMappings|KeyColumnUsage|ConstraintColumnUsage|ConstraintTableUsage|RoutineSequenceUsage|RoutineRoutineUsage|CharacterSets|Collations)'`
- Schema inquiry units: `RUN='^TestInfoSchemata'`
- Schema inquiry SQL: `RUN='^TestReferenceInfoSchemata'`
- Trigger and constraint-ownership producer units: `RUN='^TestPGTrigger'`
- Large objects: `RUN='^TestReferenceLargeObject'`
- Settings view: `RUN='^TestReferenceStatisticsSettings'`
- Cumulative-statistics SQL: `RUN='^TestReferenceStatsSnapshot'`
- Logical-slot source authority and orphan-provider ownership:
  `RUN='^TestPGStatReplicationSlots'`; public scenarios:
  `RUN='^TestReferencePGStatReplicationSlots'` in both fixture modes.
- Session monitoring SQL: `RUN='^TestReferencePGStatGSSAPI'`
- Session capture/producer ownership (core): `RUN='^TestPGStatGSSAPI'`, also `RACE=1`
- Transforms: `RUN='^TestReferencePGTransform'`
- Sequence-definition producer units: `RUN='^TestInfoSequences'`
- Sequence-definition public scenarios: `RUN='^TestReferenceInfoSequences'`
- Sequence-I/O descriptor classification and real-reader exclusion units
  (`core/enginetests/pgcatalog`): `RUN='^TestPGStatioSysSequences'`.
  Shared provider/runtime units use `RUN='^TestStatsSequence'`; public scenarios
  use `RUN='^TestReferencePGStatio(All|Sys)Sequences'`.
- Table-I/O producer units: `RUN='^TestStatio(All|Sys|User)Tables'`; composed
  classification/Reader units: `RUN='^TestPGStatio(Sys|User)Tables'`; public scenarios:
  `RUN='^TestReferencePGStatio(All|Sys|User)Tables'`, also with `REFERENCE=1`.
- Column ACL producer, ownership and frontier units (`enginetests/`):
  `RUN='^TestInfo(ColumnPrivileges|RoleColumnGrants)'`
- Column metadata producer units: `RUN='^TestInfoColumns'`
- Column metadata public scenarios: `RUN='^TestReferenceInfoColumns'`
- Composite attribute producer/capture/authority units: `RUN='^TestInfoAttributes'`
- Composite attribute public scenarios: `RUN='^TestReferenceInfoAttributes'`
- Domain-definition producer units: `RUN='^TestInfoDomains'`
- Domain-definition public scenarios: `RUN='^TestReferenceInfoDomains'`
- Domain/base usage producer/capture/cancellation units: `RUN='^TestInfoDomainUdtUsage'`
- Domain/base usage public scenarios: `RUN='^TestReferenceInfoDomainUdtUsage'`
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
- UDT grant producer/authority units: `RUN='^TestInfo(RoleUdtGrants|UdtPrivileges)'`
- UDT grant public scenarios: `RUN='^TestReferenceInfo(RoleUdtGrants|UdtPrivileges)'`
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

