# Engine split, step 9: `internal/engine/pgcatalog`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-pgcatalog.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 9; §4.1 pattern 3
  (capability); §3 (L5).
- **Needs:** steps 5 and B4 (done). 9b also needs step 8b (`settings`),
  because `pg_parameter_acl` reads the setting registry. Read the Tier C ACL
  record in [`ENGINE-SPLIT-functions.md`](ENGINE-SPLIT-functions.md) for the
  capability pattern.
- **Effort:** xhigh for 9a (dispatch precedence and catalog provenance); high
  for 9b; medium for 9c.
- **Session size:** one sub-step per session.

## Goal

System relation producers become a package of pure functions from a captured
catalog (plus a narrow runtime capability) to rows. The engine keeps one
registry lookup that wraps those rows into a query source.

## Historical inventory (before 9a, measured at `75fe0d710`)

This records the pre-cutover seams, not the current file/dispatch layout.
The implemented 9a layout is described below and in the engine README.

**Producers.** 35 `pg_*.go` files (4.2k lines) plus `catalog_sources.go`
(586, the spec table and the second dispatcher), `view_source.go` (237, the
first dispatcher), `table_view_summary.go` (287, `pg_views`, `pg_tables`,
`pg_matviews`), `extension_sources.go` (115, `pg_extension`, `pg_proc`,
`pg_ts_template`, `pg_ts_dict`), `available_extension_sources.go` (89),
`timezone_sources.go` (70), `prepared_source.go` (50, `pg_prepared_xacts`),
`catalog_attribute_facts.go` (110), `table_privileges.go` (165) and
`data_type_privileges.go` (166), the two `information_schema` views.

**Not producers** (they only share the `pg_` prefix):

| File | What it is | Used by |
|---|---|---|
| `pg_class_history.go` | catalog publication: `markStageSubclassHistoryLocked`, `normalizeReplacementSubclassHistory`, `visitSubclassParents` | `catalog.go` |
| `table_view_summary.go` (part) | catalog publication: `markStageIndexHistoryLocked`, `normalizeReplacementIndexHistory` | `catalog.go` |
| `pg_sequences_admission.go` | planner predicate admission for `pg_sequences` | `query.go` |
| `pg_depend_expression.go` | `pgDependRefs` walker over expressions and plan nodes | `pg_depend.go`, `rule_dependencies.go`, `policy_expression.go`, `policy_validation.go` |
| `pg_depend.go` (part) | `pgDependPinned`, used 9 times outside (`rule_remap.go`, `sequence_control.go`, `temporary_provenance.go`) | several |

**Dispatch.** Two chains, both under `Engine.mu`:

1. `openRelationSourceLocked` (`view_source.go:12`) switches on the relation
   kind. For `catalog.CatalogRelation` it switches on the OID:
   - runtime-backed: `pgLocksOID` (releases and retakes `Engine.mu`), the five
     foreign catalogs and `pgUserMappingsOID` (need `AuthorizationView`),
     `pgCursorsOID` (session cursors), 12081/12085 (available extensions),
     12048 (`pg_sequences`, lazy completion through the scope), 12004/12005
     (timezones, statement input), `pgSettingsOID` 16202 (`pg_settings`,
     reads the session's `sessionSettings`; added by `PG18-ADMIN-SETTINGS-
     ROW-SECURITY` right after this measurement, in `pg_settings.go`), 12014/12007/12008 (`pg_user`, `pg_roles`,
     `pg_group`), `tablePrivilegesOID` and `dataTypePrivilegesOID` (comment
     catalog, `deparseAuth`, database name);
   - then 2396 (`pg_shdescription`, comment roles), 2224 (`pg_sequence`, with
     the transaction journal overlay), 12002 (`pg_indexes`, recaptured
     authorization) and 12006 (`pg_matviews`, view presentation);
   - otherwise it picks the catalog (the comment snapshot for 2609, 3079 and
     parameter ACLs, the private statement catalog for the rest) and the roles,
     and calls the second chain.
2. `captureCatalogSourceLocked` (`catalog_sources.go:406`, its only caller is
   the first chain) is an `if` chain on the OID (`pg_type`, `pg_attribute`,
   parameter ACL, `pg_default_acl`, 2964 inline, `pg_namespace`,
   `pg_database`, `pg_tablespace`, extension catalogs, collation, conversion,
   cast, language, aggregate, opclass, am, range, operator, enum, constraint,
   inherits, `pg_sequence` without overlay, description, 12001, depend,
   12000/12003), then a loop over all relations with a final `switch` for
   2610 `pg_index`, 2604 `pg_attrdef`, 1259 `pg_class` and 2618 `pg_rewrite`.

**Producer signatures in use** (five shapes):

- catalog only: `capturePGX(ctx, view catalog.Read, ref ids.ObjectRef,
  source *catalog.Relation) (*querySource, error)`;
- `*Engine` methods with roles: `pg_type`, `pg_attribute`, `pg_namespace`,
  `pg_database`, `pg_tablespace`, `pg_parameter_acl`, `pg_default_acl`,
  `pgClassRow`, `captureTableViewSummary`;
- roles only: `pg_group`, `pg_roles`, `pg_user`, `pg_shdescription`;
- with `AuthorizationView`: `pg_indexes`, the foreign catalogs, the two
  privilege views;
- runtime state: listed under "Capability" below.

**Why they are dirty** (appendix tool): 30 producers reach a god struct only
through `*querySource`, which holds `scope *StatementScope`. With
`querySource` treated as clean, the remaining blockers are:

- the `Engine` methods, which use only `e.catalog.owner()`,
  `e.catalog.Domain`, `e.roles.domain`, `e.roles.aclAuthority`, `e.pgClassACL`
  and `e.settings` (the immutable settings registry since 8a). `catalog.Owner` already holds `Domain`,
  `Principals` and `ACLAuthority`;
- `captureDataTypePrivileges` (`auth.session` for the TEMP namespace check);
- `captureTablePrivileges` and `capturePGIndexes` (`auth.checkColumns` with a
  `binding`);
- `capturePGCursors` (`*cursorRegistry`);
- `capturePGDepend` and `capturePGConstraint`
  (`pgDependExpressionRefs`/`pgDependQueryRefs`, which walk engine plan
  nodes, and `rulesOf`);
- `pg_sequences` (`*StatementScope`, `EvalContext`).

## Sub-steps

### 9a — One producer contract and one registry, in place

**In-place code cutover implemented and verified in `internal/engine`.**
No `pgcatalog` package is created by 9a. The six outcomes are:

1. **Separate non-producers.** `catalog_relation_history.go` now owns subclass
   publication history and the index-history functions formerly mixed into
   `table_view_summary.go`. `query_pg_sequences_admission.go` owns sequence
   predicate admission. `expression_references.go` owns the shared expression/
   plan walker (`pgDependRefs`), including rule/policy consumers.
2. **One seven-field input.** `catalogSourceInput` contains exactly `Context
   context.Context`, `Catalog catalog.Read`, `Roles authorization.Read`,
   `Owner *catalog.Owner`, `Ref ids.ObjectRef`, `Source *catalog.Relation` and
   `Runtime catalogSourceRuntime`.
3. **Rows-only producers.** Eligible producers are free functions with
   `func(catalogSourceInput) ([][]sqltype.Value, error)`. This includes extracted
   `pg_db_role_setting`, `pg_index`, `pg_attrdef`, `pg_class`, `pg_rewrite` and
   prepared-transaction production. The engine's `captureCatalogSourceRows`
   wraps success as `querySource{ref, anonymous}` and preserves nil/error
   behavior. Producers do not construct query sources or retain statement
   scopes. Provenance uses the same Owner/Domain/Principals/ACLAuthority
   pointers as before, not numeric identity substitutes.
   `pgClassACL(owner, roles, array, acl)` and hidden
   `capturedRegroleOutput`/`capturedACLItemOutput` formatters now use
   `*catalog.Owner` plus immutable roles, not Engine. Absent/explicit-empty ACL,
   retained-root and type-generation checks keep their original meanings.
4. **Demand-only per-call capability.** `engineCatalogRuntime` privately retains
   only the selected handles/cheap facts at construction. Value-taking methods
   expose no Engine, StatementScope, AuthorizationView or binding. Cursor
   visitors do not fetch; settings read one coherent physical-session tuple
   after unlock; prepared visitors read the actual registry, assigned XID and
   published owner, with entry cancellation before those lookups. Error-bearing
   privilege/provenance operations, physical TEMP ownership, parameter
   definition validation, shared expression/query walks, rule facts and
   view/index presentation remain adapter-owned. Sequence definition visitors
   preserve base/overlay order without generator reads. Foreign disclosure
   retains deliberate boolean denials and native missing-server errors.
5. **One immutable registry.** `catalogSourceProducers` is a process-initialized,
   OID-sorted fixed array with binary-search lookup, shared by both engine
   entry paths. All five foreign metadata relations are registry producers:
   `pg_foreign_data_wrapper`, `pg_foreign_server`, `pg_user_mapping`,
   `pg_foreign_table` and `pg_user_mappings`, with fresh paired authorization.
   **Only `pg_locks` and lazy `pg_sequences` are explicit handlers.**
   `catalog_source_capture.go` selects per-OID input roots, runtime handles,
   presentation and unlock policy; it is not another producer dispatcher.
   Comment snapshots, index authorization recapture, physical ownership and
   the statement-only `pg_sequence` journal overlay remain caller policy.
   Lock capture preserves unlock/capture/relock epoch fencing and never takes
   the lock-manager mutex under Engine.mu; sequence completion stays lazy.
6. **Pure ordered specs, engine registration.** `catalog_source_specs.go` holds
   process-initialized pure metadata, including foreign, privilege and settings
   definitions. The engine still resolves owned types, builds fresh descriptors
   and registers them through `initializeCatalogSources`,
   `catalogSourceDescriptor` and `registerCatalogSource`. Spec order is not
   sorted lookup order. OIDs, column/carrier/typmod/dimension/nullability/
   collation/origin metadata, flags and actual row/array links are preserved;
   startup allocation/performance impact still requires measurement.

The four actual new units are `TestCatalogSourceProducerCoverage`,
`TestCatalogSourceInputProvenance`, `TestCatalogSourceRuntimeCapture` and
`TestCatalogSourceSpecMetadata`. See
[engine Testing](../internal/engine/README.md#system-catalog-producer-contract)
for selected native, unchanged dual-mode reference and race commands.

### 9b — Create `pgcatalog` and move

1. Export in place what the engine still calls: the producer table, the spec
   data, `Pinned` (`pgDependPinned`), `ClassKind` (`pgClassKind`, used by
   `object_identity.go`), `AttributeMetadata`, `HasSystemAttributes`,
   `DatabaseCollationActualVersion`, `VirtualViewWriteError` and
   `TimezoneViewWriteError` (used by `engine.go`), the OID constants used in
   `catalog.go`, `catalog_mutation.go` and `catalog_attribute_facts.go`, and
   `LockRows`.
2. Move the producers, spec tables, input record, capability interface and
   helpers into `internal/engine/pgcatalog`. The engine keeps the capability
   adapter, the dispatcher, descriptor registration and the explicit cases.
3. Leave aliases in the engine for the types; functions are called as
   `pgcatalog.X` directly.
4. Check imports: only `catalog`, `authorization`, `lock` (lock row data),
   `settings`, `expr`, `ids`, `sqltype`, `sqlstate` and the standard
   library. Not `access`, `storage` or `visibility`.

### 9c — Remove the aliases

Use the alias tool of 5f/6c. Expect few aliases: most moved code is
functions.

## Stays in the engine

`openRelationSourceLocked` and the `querySource` wrapping;
`authorizeViewSourceLocked` and `authorizeViewSourceSnapshot`;
`capturePGLocksLocked` and `capturePGPredicateLocksLocked` (they read the lock
manager and the visibility registry around `Engine.mu`); `pg_sequences`
completion (`pgSequencesSource`, `openPGSequencesStage`); the capability
adapter; descriptor registration during catalog seeding (`newEngine` calls
`initializeCatalogSources()` at `engine.go:270`); the `information_schema`
namespace and domain seeding (`engine.go:219`, `domain_check.go`);
`foreign_scan.go` (user foreign tables, not system relations).

## Invariants

- Every system relation returns the same rows, in the same order, with the
  same NULLs, collations and errors. Provenance errors ("foreign catalog
  ACL authority", stale epochs, `ids.ErrCatalogReplaced`) stay identical and
  happen at the same point.
- Dispatch precedence and the per-OID choice of catalog and roles are
  unchanged.
- `pg_locks` keeps its lock order: the lock manager's mutex is never taken
  under `Engine.mu` (see the `lock` README).
- Catalog seeding keeps the same OIDs, column types, nullability and
  registration order. Startup allocation/performance preservation must be
  established by before/after measurements, not inferred from this structure.

## Targeted tests

- Engine unit tests: grep `*_test.go` for `capturePG`, `catalogSource`,
  `pgClassRow`, `pgDependPinned` and the producer file names. Tests of pure
  producer behavior move with the code in 9b and use a fake capability.
- Reference selections (quick mode and `REFERENCE=1`): grep `reference/` for
  `pg_catalog.`, `pg_class`, `pg_attribute`, `pg_type`, `pg_depend`,
  `pg_constraint`, `pg_locks`, `pg_cursors`, `pg_prepared_xacts`,
  `pg_sequences`, `pg_indexes`, `pg_views`, `information_schema`, `regclass`.
  Never edit `reference/`.
- `make bench` after 9a step 6 and after 9b (catalog seeding is on the
  startup path).
- `RACE=1` for `pg_locks` and `pg_cursors` tests.

## Documentation

- For 9a, update the existing engine README, including Testing and current
  moved-name references. Do not create a package README before 9b.
- Create `internal/engine/pgcatalog/README.md` (9b): the producer contract,
  the capability and why each method exists, the registry and how to add a
  system relation, provenance rules, what stays in the engine, `## Testing`.
- `docs/design/PG18-EXPR-COMPOSITE.md` names `querySource`; check it. Grep
  `docs/` for the moved file names.
- In 9b only, add `pgcatalog` to the `AGENTS.md` subpackage bullet.

## Done when

`pgcatalog` imports only the packages listed in 9b. The engine dispatches
catalog relations through one registry lookup, and no producer builds a
`querySource` or reads `Engine` fields. The checks in §8.3 pass.

## Status

- [x] 9a — One producer contract and one registry, in place
- [ ] 9b — `pgcatalog` created and producers moved
- [ ] 9c — Aliases removed

Outstanding aliases: none yet.

Observed 9a evidence: the four new contract units passed and the root's external
public `database/sql` smoke passed dispatch, direct/new/SAME-held DDL metadata,
definition/link presentation, bounded runtime rows, retained unadvanced
Rows/types across Dump/Reset, and Restore live compact slots/data.
Selected unchanged native/PostgreSQL18.6 definitions, selected race checks,
build, dependencies and full quickcheck passed. Full racecheck and the
race-containing post-integration finalcheck were waived for this cutover.
Steady startup allocations fell from 5152 to 5100. Latest current-base samples
measured 880449–1214836ns/op: three below the 1ms target and two above it.
Earlier samples and overload failures remain evidence; no controlled timing
speedup or consistently sub-millisecond startup is claimed.
`reference/` and `smoke/` are unchanged; 9b/9c remain pending.

Notes for later steps:

- `pg_stat_activity` and `pg_prepared_statements` are not implemented;
  adding them is feature work, not part of the split. Once `pgcatalog`
  exists, new producers go there. (`pg_settings` landed at `2205fe385`.)
- Step 11 (`exec`): `querySource.anonymous` remains the row carrier for
  catalog relations; the executor sees no change.
