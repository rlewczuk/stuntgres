# reference/catalog_privileges

Role, database and privilege catalogs: `pg_roles`, `pg_user`, `pg_group`,
`pg_db_role_setting`, `pg_database` and database creation, `pg_default_acl`,
`pg_parameter_acl`, `pg_get_acl`, tablespaces, VACUUM and size functions, and the
`information_schema` grant, role, user-mapping, key, constraint and SQL metadata
views (`role_column_grants`, `role_table_grants`, `role_udt_grants`,
`role_usage_grants`, `enabled_roles`, `data_type_privileges`, `user_mappings`,
`key_column_usage`, `constraint_column_usage`, `sql_features`, `sql_sizing`,
`sql_implementation_info`, `sql_parts`). The other catalog families are listed
in [reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package.

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `vacuumScenario`, `objectACLScenario`, `tablespaceContainer`).
- PostgreSQL cumulative statistics: flush the writer's pending statistics with
  `pg_stat_force_next_flush()` before taking a baseline, then read with bounded
  fresh-read polling and exact equality (`vacuumStatistic`). PostgreSQL VERBOSE
  INFO text is not an expectation.
- Tablespace directory scenarios use a real container-owned directory
  (`tablespaceContainer`), restore permissions before relation, tablespace and
  directory cleanup, and drop cluster objects explicitly.
- Frame names matched by tests (active-cancel admission): `(*relationRequest).wait`
  in `internal/engine/lock`. Update the tests when renaming it.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Databases and role settings: `RUN='^TestReferencePGDatabase'`, `RUN='^TestReferencePGDBRoleSetting'`
- Role views: `RUN='^TestReferencePGRoles'`, `RUN='^TestReferencePGUser'`, `RUN='^TestReferencePGGroup'`
- Database creation, selection and targeted controls: `RUN='^TestReferenceDatabaseCreate'`
- ACL catalogs and `pg_get_acl`: `RUN='^TestReferencePGDefaultACL'`, `RUN='^TestReferencePGParameterACL'`, `RUN='^TestReferenceObjectACL'`
- Information schema: `RUN='^TestReferenceInfo'` (shared with `reference/information_schema`), `RUN='^TestReferenceDataTypePrivileges'`
- Role table grants: `RUN='^TestReferenceInfoRoleTableGrants'`
- Tablespaces: `RUN='^TestReferenceTablespace'`
- Size functions: `RUN='^TestReferenceSize'`
- VACUUM: `RUN='^TestReferenceVacuum'`

### SQL feature metadata

`info_sql_features_helpers_test.go` reads the untouched official REL_18_6
six-column inventory in `testdata/sql_features_pg18.tsv`; expectations never
come from the native producer or live SQL results. Shared scenarios compare
the complete matrix as bags and exercise domain/table facts, cursor metadata,
direct/new/held prepared reads, NULL/empty strings, identity independence and
complete diagnostics.

Private-table denial is shared SQL coverage. Transactional PostgreSQL-only
target ACL revocation is rollback-contained, with separate savepoints for
expected errors and independently bounded cleanup. Native catalog-source
GRANT/REVOKE admission is not widened: engine units exercise actual ACL
publication, retained authorization, denial and restoration.

- SQL feature metadata: `RUN='^TestReferenceInfoSQLFeatures'`

### SQL sizing metadata

`info_sql_sizing_helpers_test.go` carries independently prescribed complete
native and PostgreSQL sizing inventories. The family checks all four nullable
domain columns, bag multiplicities, direct/fresh/held reads, empty and all-NULL
metadata, role independence and complete diagnostics. Independent table-column,
identifier-byte and SELECT-target witnesses ground reported bounds rather than
deriving test sizes from the metadata. PostgreSQL catalog ACL phases roll back
all changes; native units exercise real ACL publication. Native Reset/Restore
checks reinitialized sizing authority and detached retained descriptors.

- SQL sizing metadata: `RUN='^TestReferenceInfoSQLSizing'`

### SQL implementation metadata

`info_sql_implementation_info_helpers_test.go` owns independently specified
twelve-row expectations, including explicit backend product identities, domain
metadata and raw NULL/integer/string boundaries. Shared scenarios exercise
physical table/type ownership, direct/fresh/held reads, stable initial values,
ordinary identities and complete diagnostics. PostgreSQL target ACL revocation
is rollback-contained; engine units cover real native catalog ACL replacement,
schema admission and retained authorization without widening SQL ACL admission.

- SQL implementation metadata: `RUN='^TestReferenceInfoSQLImplementationInfo'`

### SQL part metadata

`info_sql_parts_helpers_test.go` owns an independent eleven-row literal matrix.
Shared scenarios assert the five nullable domains, physical table/type identity,
NULL verification versus empty comments, complete bags, direct/fresh/held reads,
cursor metadata and diagnostics. Default owner/superuser admission and ordinary
reader denial, including empty predicates, run on both backends. Explicit
PostgreSQL catalog GRANT/REVOKE phases are rollback-contained; engine units
exercise real native ACL publication, schema USAGE and immutable captured roots
without expanding SQL ACL admission.

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

`info_role_usage_grants_helpers_test.go` owns exact eight-column origin bags,
domain/adapter metadata and direct/fresh/held reads across domains, sequences,
collations, foreign wrappers and foreign servers. Scenarios distinguish raw
absent, materialized and empty ACLs, enabled endpoints, named-grantee
grantability, lifecycle frontiers, NULL extension and complete diagnostics.

- Usage grants: `RUN='^TestReferenceInfoRoleUsageGrants'`
