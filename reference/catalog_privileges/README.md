# reference/catalog_privileges

Role, database and privilege catalogs: `pg_authid`, `pg_auth_members`, `pg_roles`, `pg_user`, `pg_shadow`, `pg_group`,
`pg_db_role_setting`, `pg_database` and database creation, `pg_default_acl`, `pg_init_privs`,
`pg_parameter_acl`, `pg_get_acl`, tablespaces, VACUUM and size functions, and the
`information_schema` grant, role, user-mapping, key, constraint and SQL metadata
views (`table_privileges`, `role_column_grants`, `role_table_grants`, `role_udt_grants`,
`role_usage_grants`, `usage_privileges`, `enabled_roles`, `data_type_privileges`, `user_mappings`, `user_mapping_options`,
`key_column_usage`, `constraint_column_usage`, `sql_features`, `sql_sizing`,
`sql_implementation_info`, `sql_parts`). The other catalog families are listed
in [reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package.

The `TestReferencePGInitPrivs` family independently prescribes bootstrap and
extension initial ACL privilege/grantor/option bags, rather than deriving
expectations from current ACLs. It covers typed shape and diagnostics,
membership/column cleanup, unfiltered inventory with catalog authorization,
transaction/savepoint publication, historical role dependencies and native
Reset/Dump/Restore with destination remapping. Native control phases use only
unwrapped public pools; PostgreSQL phases exercise the shared SQL behavior.
`TestReferencePGInitPrivsExtensionDefaults` separately composes global routine
default grants/options with schema-specific defaults during amcheck installation:
its selective PUBLIC revoke preserves named grantees in both current and initial
ACLs. Later current/default revocation leaves historical dependencies and native
round-trip values intact. Owner-only amcheck expectations apply to untouched
defaults, not to installations with configured named grantees.
Run each selected group in native mode and with `REFERENCE=1`, for example
`make check RUN='^TestReferencePGInitPrivs(Shape|Bootstrap|Diagnostics)$'`.
The permanent feature smoke is `make check RUN='^TestReferencePGInitPrivsSmoke$'`.

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `vacuumScenario`, `objectACLScenario`, `tablespaceContainer`).
- `pgParameterACLSharedScenario` composes `DatabaseScenarioPool` with the existing
  parameter metadata/raw-carrier assertions. Shared identity and native replacement
  scenarios use `RUN='^TestReferencePGParameterACLShared'`; controls retain actual
  Rows across peer replacement and use gated readers with joined workers.
- PostgreSQL cumulative statistics: flush the writer's pending statistics with
  `pg_stat_force_next_flush()` before taking a baseline, then read with bounded
  fresh-read polling and exact equality (`vacuumStatistic`). PostgreSQL VERBOSE
  INFO text is not an expectation.
- Tablespace directory scenarios use a real container-owned directory
  (`tablespaceContainer`), restore permissions before relation, tablespace and
  directory cleanup, and drop cluster objects explicitly.
- Rule-action size scenarios defer `DROP OWNED` for both actors before role
  teardown, releasing database/tablespace ACLs as well as owned action relations.
- Frame names matched by tests (active-cancel admission): `(*relationRequest).wait`
  in `internal/engine/lock`. Update the tests when renaming it.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Databases and role settings: `RUN='^TestReferencePGDatabase'`, `RUN='^TestReferencePGDBRoleSetting'`
- Role views: `RUN='^TestReferencePGRoles'`, `RUN='^TestReferencePGUser'`, `RUN='^TestReferencePGShadow'`, `RUN='^TestReferencePGGroup'`
- Protected all-role metadata: `RUN='^TestReferencePGAuthID'`
- Database creation, selection and targeted controls: `RUN='^TestReferenceDatabaseCreate'`
- ACL catalogs and `pg_get_acl`: `RUN='^TestReferencePGDefaultACL'`, `RUN='^TestReferencePGParameterACL'`, `RUN='^TestReferenceObjectACL'`
- Information schema: `RUN='^TestReferenceInfo'` (shared with `reference/information_schema`), `RUN='^TestReferenceDataTypePrivileges'`
- Role table grants: `RUN='^TestReferenceInfoRoleTableGrants'`
- Table privileges: `RUN='^TestReferenceInfoTablePrivileges'`
- Tablespaces: `RUN='^TestReferenceTablespace'`
- Size formatting: `RUN='^TestReferenceSize'`; logical relation/database/tablespace allocation: `RUN='^TestReferenceRelationSize'`
  Size diagnostic scenarios include retained FILTER predicate analysis and
  resolved-scalar rejection, with direct/prepared failure publication and
  same-connection recovery.
- VACUUM: `RUN='^TestReferenceVacuum'`
- VACUUM progress: `RUN='^TestReferenceVacuumProgress'`; Active, Privileges,
  ActiveWorkers and ActiveTargets require real second-session SQL rows from
  executing workers. PROCESS_TOAST false keeps base-target identity exact.
  PostgreSQL-only cost pacing is physical workload tuning, not native behavior.
  ReadOnly checks complete direct/prepared55000 diagnostics and unchanged state.
  Run the selected family both natively and with `REFERENCE=1`; owner barriers
  supplement, never replace, these public active-work witnesses.
- Bootstrap trigger/rule deparse inventory:
  `RUN='^TestReferenceDeparseRewriteBootstrapReturnRules$'` checks both advertised
  progress-view `_RETURN` identities through all overloads, exact independent
  default/pretty bodies, captured naming and identifier quoting. The CREATE
  INDEX pretty body uses released reduced cast/concatenation parentheses; it is
  not default text with different whitespace. Run both native and `REFERENCE=1`.
  User trigger/rule and real view/materialized-rule cases live in
  [reference/views](../views/README.md#testing).

`TestReferencePGShadowPreparedAuthorization` retains a credential statement
prepared by an ordinary reader through denial, administrator-granted
`pg_read_all_data` membership, and revocation. The same reader and statement
must return the exact stored verifier with its TEXT metadata and one-row
cardinality only while authorized; both denials check complete diagnostics
and reject cursor exposure. Credential assertions remain redacted.

`pg_authid` scenarios separate exact all-role attributes from central relation
and column authority. Credential comparisons use unrecorded raw callbacks and
redacted failures across direct, fresh and retained prepared reads. PostgreSQL
catalog ACL phases always roll back; native components publish real ACL values.

`pg_auth_members` scenarios assert the exact public direct-edge bag, physical
column metadata, distinct grantors and stable row identity across option changes,
rename, rollback, concurrent private publication and native controls. Generated
row IDs are checked within each backend's lifetime, not compared numerically
across servers. Unrelated ordinary readers see the same stored edges; credential
catalog denial remains independently covered.
SQL role rename remains a PostgreSQL-only phase because the native role command
is unsupported. Native immutable-authority rename units check the same principal,
membership identities/options and projected public role name; all supported
public lifecycle steps run on both backends using their current role names.

### SQL feature metadata

`info_sql_features_helpers_test.go` reads the untouched official REL_18_6
six-column inventory in `testdata/sql_features_pg18.tsv`; expectations never
come from the native producer or live SQL results. Shared scenarios compare
the complete matrix as bags and exercise domain/table facts, cursor metadata,
direct/new/held prepared reads, NULL/empty strings, identity independence and
complete diagnostics.

Private-table denial and transactional target ACL revocation are shared SQL
coverage. ACL phases roll back, with separate savepoints for expected errors and
independently bounded cleanup. Direct, fresh and held consumers recheck the
registered source's actual table/column ACLs; engine units additionally cover
immutable captured roots and publication.

- SQL feature metadata: `RUN='^TestReferenceInfoSQLFeatures'`

### SQL sizing metadata

`info_sql_sizing_helpers_test.go` carries independently prescribed complete
native and PostgreSQL sizing inventories. The family checks all four nullable
domain columns, bag multiplicities, direct/fresh/held reads, empty and all-NULL
metadata, role independence and complete diagnostics. Independent table-column,
identifier-byte and SELECT-target witnesses ground reported bounds rather than
deriving test sizes from the metadata. Shared catalog and schema ACL phases
roll back all changes; native units additionally exercise immutable publication.
Native Reset/Restore checks reinitialized sizing authority and retained descriptors.

- SQL sizing metadata: `RUN='^TestReferenceInfoSQLSizing'`

### SQL implementation metadata

`info_sql_implementation_info_helpers_test.go` owns independently specified
twelve-row expectations, including explicit backend product identities, domain
metadata and raw NULL/integer/string boundaries. Shared scenarios exercise
physical table/type ownership, direct/fresh/held reads, stable initial values,
ordinary identities and complete diagnostics. Shared target ACL revocation is
rollback-contained; engine units additionally cover catalog ACL replacement,
schema admission and retained authorization.

- SQL implementation metadata: `RUN='^TestReferenceInfoSQLImplementationInfo'`

### SQL part metadata

`info_sql_parts_helpers_test.go` owns an independent eleven-row literal matrix.
Shared scenarios assert the five nullable domains, physical table/type identity,
NULL verification versus empty comments, complete bags, direct/fresh/held reads,
cursor metadata and diagnostics. Default owner/superuser admission and ordinary
reader denial, including empty predicates, run on both backends. Explicit
catalog GRANT/REVOKE phases share transaction/savepoint rollback and retained
consumer denial/restoration; engine units additionally cover immutable roots.

- SQL part metadata: `RUN='^TestReferenceInfoSQLParts'`

### Column grant metadata

`info_role_column_grants_helpers_test.go` owns the shared scenario, exact
eight-column bags, domain/adapter metadata, identity setup and held consumers.
The family checks ACL origins and raw-UNION multiplicity, enabled grantors and
grantees, object classes, mutation atomicity and fresh execution visibility.
Eager retained rows and native controls are explicit native-only phases.

- Column grants: `RUN='^TestReferenceInfoRoleColumnGrants'`

### UDT grants

`info_role_udt_grants_helpers_test.go` owns exact type ACL origin bags,
domain/adapter metadata and direct/fresh/held reads. The family distinguishes
actual reciprocal composite rows from arrays and domains, preserves raw absent
versus empty ACLs, and covers enabled identities, captured grantability, live
ACL/membership publication, lifecycle frontiers and complete diagnostics.

- UDT grants: `RUN='^TestReferenceInfoRoleUdtGrants'`

### Usage grants

`info_role_usage_grants_helpers_test.go` and
`info_usage_privileges_helpers_test.go` own exact eight-column origin bags,
domain/adapter metadata and direct/fresh/held reads across domains, sequences,
collations, foreign wrappers and foreign servers. Scenarios distinguish raw
absent, materialized and empty ACLs, enabled endpoints, named-grantee
grantability, lifecycle frontiers, NULL extension and complete diagnostics.
The usage-privilege scenarios also assert unconditional PUBLIC visibility and
its distinction from the role-filtered view, including native control replay.
Independent canonical definition literals cover search-path visibility,
relation shadows and identifier quoting across direct and held metadata reads.

- Usage privilege families: `RUN='^TestReferenceInfo(RoleUsageGrants|UsagePrivileges)'`

### Table privilege metadata

`info_table_privileges_helpers_test.go` owns exact eight-column ACL-origin bags,
captured domain/adapter metadata and direct/fresh/held reads. The family
distinguishes unconditional PUBLIC visibility from the sister role view,
preserves grantor origins and grantee grantability, and checks live membership,
object lifecycle, cancellation and native controls. PostgreSQL target ACL
revocation is rollback-contained; native units exercise real ACL publication
and retained authorization without expanding catalog-source SQL admission.

### User mapping options

`info_user_mapping_options_helpers_test.go` owns exact five-column option bags,
domain/adapter metadata and direct/fresh/held readers. Scenarios distinguish
server visibility from mapped-user/PUBLIC-owner value disclosure, including
inherited versus exact identities, redacted NULL versus empty strings, real
option lifecycle, live authority refresh and complete diagnostics. Native-only
phases exercise retained results and logical controls with destination identities.

- User mapping options: `RUN='^TestReferenceInfoUserMappingOptions'`
