# reference/roles

Roles and table-level authorization: CREATE/ALTER/DROP ROLE and USER
attributes, passwords, validity and connection limits; trusted connectors and
authenticated sessions; role membership (GRANT/REVOKE role, ADMIN/INHERIT/SET,
ALTER GROUP); SET ROLE and SET SESSION AUTHORIZATION; ownership, REASSIGN OWNED,
DROP OWNED and DROP ROLE; table and column GRANT/REVOKE; ALTER DEFAULT
PRIVILEGES; predefined roles; `pg_has_role`/`row_security_active`; the
`row_security` setting; and row security (policy DDL, table flags, policy
combination, existing-row USING filtering and finalized INSERT/UPDATE proposed-row
checks with atomic rejection).
Related: non-table grants and large objects in
`reference/object_grants`; `pg_roles`, `pg_user`, `pg_group`, `pg_default_acl`
and ACL inquiry functions in `reference/catalog_privileges`; session identity forms in
`reference/sessions`.

Existing-row USING policies filter SELECT sources and UPDATE/DELETE targets before
non-leakproof user expressions. FALSE or NULL silently suppresses rows; missing
applicable policies default to denial. Ordinary ACL checks remain separate, and
policy evaluation errors preserve their diagnostics rather than becoming hidden
rows or proposed-row policy violations.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
Role-grant actors, permission diagnostics and notice helpers are the fixture's
`RoleGrant*` helpers ([fixture README](../fixture/README.md#helpers)).

Type identity lifecycle reuses the existing OWNER/ACL authority, including real
array and standalone-composite backing owners. Extension membership is not an
identity-ALTER prohibition. Successor owners exercise the same ordinary lifecycle
authority.

- `membershipScenario` (`role_membership_helpers_test.go`) is the shared roles
  fixture: `run`/`exec`/`actor` over one notice-enabled pinned connection, with
  `s.bootstrap` = `postgres` natively and `stuntgres` on the oracle.
  `membershipCases(t, roles, body)` builds one per `prepared=false|true` ×
  backend subtest, after registering cleanup for `roles`, which it creates
  with LOGIN.
  Family helpers build on it: `ownershipCases` (`role_ownership_helpers_test.go`),
  `predefinedCases` (`predefined_helpers_test.go`), `rlsBaseCases`
  (`rls_base_helpers_test.go`), `combinationCases` (`rls_combination_helpers_test.go`),
  `accessRoleCases` (`access_role_helpers_test.go`) and `policySetup`
  (`rls_policy_definition_test.go`). Default privileges use `dpScenario`
  (`default_privileges_helpers_test.go`); role attributes use `roleAttributesRun`
  and the facts in `role_attributes_fixture_test.go`.
- `ownershipCases` runs `DROP OWNED BY ... CASCADE` for every surviving
  `ownershipRoles` entry and drops its nonpublic schemas under a bounded
  context. Establish schema ownership with `CREATE SCHEMA ... AUTHORIZATION`;
  drop nonpublic fixture schemas before roles on failure paths.
- `ownershipTransactionCases` covers autocommit, SQL BEGIN and Go BeginTx with
  direct and held ownership/bulk statements, earlier-private objects, peer
  isolation and savepoint/end transitions. `ownershipRouteModes` runs
  `sql-held-inside` and `go-stmt` prepared only: unprepared, they repeat
  `sql-held-before` and `go-prepare`. For database OWNER, select the ordinary
  CREATEDB session actor before BEGIN; use SET ROLE, not SET SESSION AUTHORIZATION,
  inside the transaction.
  Cleanup authority witnesses use actual ordinary-role SELECT/UPDATE operations
  and savepoint recovery for expected denials. Independent surviving SELECT grants
  separate RLS policy effects from ACL loss; PostgreSQL policy catalog assertions
  supplement common behavior and exact canonical-descriptor units.
- Dependency Detail lines have no PostgreSQL ordering contract: compare them as
  bags with exact multiplicities (`ownershipFailureDetailBag`,
  `ownershipNoticesDetailBag`) and keep every other diagnostic field exact.
- Default-privilege identities come from querying `pg_default_acl` on each
  backend (`role_ownership_defaults_helpers_test.go`): positive uint32 OIDs,
  exact counts and stability, never fixed values.
- Known oracle defect: in `TestReferenceRoleOwnershipDefaultCreatorRecipientUnion`
  PostgreSQL fails owner-first baseline-only cleanup with XX000 `could not find
  tuple for default ACL <oid>`, while native cleans up atomically. Its
  witness-specific creator-only `DROP OWNED` cleanup runs before the shared
  helper on every path, so the defect cannot mask assertion failures.
- A PostgreSQL held statement denied at planning can stay denied after
  BYPASSRLS or inherited-owner grants; native held execution always checks
  current authority. Specify each backend's held outcome independently.
- The subscription witness uses `connect=false`, `slot_name=NONE` and a harmless
  conninfo password; its cleanup, registered before the scenario, drops the
  subscription and revokes database CREATE before role teardown.
- lib/pq reports COPY denial on the outside-transaction prepared route as an
  ordinary driver error, not a SQLSTATE.
- `ALTER POLICY IF EXISTS ... RENAME` is accepted but does not skip: a missing
  policy still raises 42704. Do not reuse DROP IF EXISTS notice expectations.
- Frame names matched by tests: lock-wait observers in
  `rls_policy_lifecycle_test.go` and `rls_combination_latest_test.go` match the
  native `(*relationRequest).wait` frame plus their own workers
  `policyLockExecute` and `combinationMutation`. Update them when renaming.
- Transactional privilege cases use SQL BEGIN and Go BeginTx, direct and held
  utilities/readers, private owner versus peer observations, commit/rollback,
  savepoint suffix restoration, creator-only future defaults and atomic object
  creation. Scope does not expand role lifecycle/membership, generic ownership,
  ALTER SYSTEM or SQL PREPARE. Protocol cases live in `reference/sessions`;
  non-table families and lock/concurrent-writer cases live in
  `reference/object_grants`.

Native control witnesses preserve materialized table/column ACLs, raw grantor
and grant-option state, data and actor authority across Dump/Restore. Default
privilege descriptors still reject Dump before output; arbitrary GRANT/REVOKE
and ALTER DEFAULT PRIVILEGES remain outside Restore admission and fail atomically.
Transactional controls retain committed-only capture and stale-publication
fences, then prove a committed grant survives replacement with its roles intact.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Role attributes: `RUN='^TestReferenceRoleAttributes'`
- Trusted connectors: `RUN='^TestReferenceRole(Trusted)?Connector'`
- Membership: `RUN='^TestReferenceRoleMembership'`
- SET ROLE / SESSION AUTHORIZATION: `RUN='^TestReferenceRoleSwitch'`
- Ownership, REASSIGN/DROP OWNED, DROP ROLE: `RUN='^TestReferenceRoleOwnership'`
- Table and column GRANT: `RUN='^TestReferenceRoleGrant'`
- Table and column REVOKE: `RUN='^TestReferenceRoleRevoke'`
- Default privileges: `RUN='^TestReferenceRoleDefaultPrivileges'`
- Transactional ACL/default creation, delegation, controls and held execution:
  `RUN='^TestReferenceTransactionalPrivileges(Creation|Defaults|Savepoints|Delegation|Prepared|Controls)$'`
- Private publication/lineage and cancellation invariants:
  `RUN='^TestTransactionalPrivileges'`
- Predefined roles: `RUN='^TestReferencePredefined'`
- `pg_has_role`, `row_security_active`: `RUN='^TestReferenceAccessRole'`
- `row_security` setting: `RUN='^TestReferenceRowSecurity'`
- Policy DDL: `RUN='^TestReferenceRLSPolicy'`
- Transactional policy definitions and existing RLS consumers: `RUN='^TestReferenceTransactionalPolicies(Routes|Retention|Savepoints|Diagnostics|Prepared|Dependencies|Locks|Controls)$'`
- Table RLS flags, default deny, BYPASSRLS: `RUN='^TestReferenceRLSBase'`
- Policy applicability and combination: `RUN='^TestReferenceRLSCombination'`
- Existing-row USING filtering: `RUN='^TestReferenceRLSFilter'`
- Proposed-row checks, transformations and statement atomicity: `RUN='^TestReferenceRLSCheck'`

- Role DDL, authorization and sessions:
  `RUN='^TestReferenceRole(Creation|Authorization|AuthenticatedSessions|ExcludedCommands|DropConditionalBoundary|TransactionMutationBoundary)$'`
- Retained identities and native controls:
  `RUN='^TestReferenceRole(Retained|MigratedSmokeControls|ControlsOwners|RestoreOwnerReset)'`
- Grant and ownership consumer regressions:
  `RUN='^TestReferenceRole(PreparedSetup|InputPrecedence|NamespaceLookup|LaterDependent|DependencyAddress|PublicGrantOptions)'`
- Engine units use the family names without `Reference`, for example
  `make check RUN='^TestRLSPolicy'` or `make check RUN='^TestDefaultPrivileges'`.
