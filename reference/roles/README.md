# reference/roles

Table-level authorization: ownership, REASSIGN OWNED, DROP OWNED and DROP ROLE;
table and column GRANT/REVOKE; ALTER DEFAULT PRIVILEGES; predefined roles;
`pg_has_role`/`row_security_active`; the
`row_security` setting; and row security (policy DDL, table flags, policy
combination, existing-row USING filtering and finalized INSERT/UPDATE proposed-row
checks with atomic rejection).
Related: non-table grants and large objects in
`reference/object_grants`; `pg_roles`, `pg_user`, `pg_group`, `pg_default_acl`
and ACL inquiry functions in `reference/catalog_privileges`; session identity forms in
`reference/sessions`.
Role identity, attributes, membership, trusted connectors, transactional
lifecycle, rename and persisted-default scenarios live in
`reference/object_address`. Shared role helpers belong to `reference/fixture`.

Existing-row USING policies filter SELECT sources and UPDATE/DELETE targets before
non-leakproof user expressions. FALSE or NULL silently suppresses rows; missing
applicable policies default to denial. Ordinary ACL checks remain separate, and
policy evaluation errors preserve their diagnostics rather than becoming hidden
rows or proposed-row policy violations.

Generic server directory listing scenarios own the nine selected signatures,
typed strict empties, central EXECUTE and live inherited-role authority,
lexical dirname restrictions, held-statement reauthorization and procedure ACL
control replay. Physical native rejection and stock filesystem/slot outcomes
have separate independent expectations; actual stock entries are unordered bags.

Generic file-read scenarios cover all twelve read/binary/stat/tmpdir overloads,
typed scalar and record metadata, scalar strict NULL erasure versus retained
record/SRF admission, held parameter execution and live ACL controls. Native
physical rejection and stock file contents have independently stated outcomes.
The family uses `make check RUN='^TestReferenceGenericFileRead'`; the same shared
builtin ACL cleanup and emitted-repair rules apply.
Fixture row expectations use normalized strings for BYTEA payloads; the shared
fixture still checks raw byte slices, and surface scenarios separately assert
BYTEA scan metadata. Runtime file validation and physical-boundary errors retain
the complete ERROR diagnostic, including both severity fields. Malformed-call
scenarios distinguish impossible typed formals from UNKNOWN conversion, named
notation grammar from signature lookup, and nonaggregate decorations from
trailing-input syntax errors; all retain direct/prepared stages and positions.
Predefined-capability boundaries check read-function EXECUTE denial both before
and after server-file role grants; those roles grant path authority, not function
execution. Remaining unavailable functions retain their binding-time boundaries.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.WithLogicalWAL()` for the package-owned oracle. The logical-decoding
family uses `make check RUN='^TestReferenceLogicalDecoding'` for signatures,
typed metadata, options, diagnostics, live authority and unchanged-state witnesses.
Role-grant actors, permission diagnostics and notice helpers are the fixture's
`RoleGrant*` helpers ([fixture README](../fixture/README.md#helpers)).
The file-list family uses `make check RUN='^TestReferenceGenericFileLists'`.
File-list scenarios restore the original PostgreSQL builtin ACLs on every exit,
after removing actor-owned grant dependencies and before cluster role teardown.
Restricted actors never read protected log settings for helper expectations;
the administrative backend pool supplies fixture configuration. Raw native
procedure ACL repairs use emitted Dump statements through Restore, not live
catalog UPDATE.

Type identity lifecycle reuses the existing OWNER/ACL authority, including real
array and standalone-composite backing owners. Extension membership is not an
identity-ALTER prohibition. Successor owners exercise the same ordinary lifecycle
authority.

- `fixture.RoleMembershipScenario` provides `Run`/`Exec`/`Actor` over one
  notice-enabled pinned connection. `fixture.RoleMembershipCases` covers both
  prepared modes and backends after registering role cleanup and creating LOGIN
  roles. Family wrappers (`ownershipCases`, `predefinedCases`, `rlsBaseCases`,
  `combinationCases`, `policySetup`) retain their existing setup; access inquiry
  helpers use `fixture.AccessRole*`. Default privileges use local `dpScenario`.
- Predefined-role ANALYZE consumers exercise the actual maintenance command:
  missing MAINTAIN warns and skips successfully, `pg_maintain` admits collection
  without granting SELECT or ownership, schema USAGE remains mandatory, and a
  held ANALYZE rechecks authorization after membership revocation. Both direct
  and prepared scenarios retain their independent PostgreSQL expectations.
  Denied collection preserves the preceding relation estimate, including
  PostgreSQL's population estimate already published by CREATE INDEX; native
  index creation leaves that maintenance estimate unknown.
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
Named registered catalog tables and provider views share the table/column ACL
scenarios: direct and prepared revoke, retained-reader rechecks, option-only
removal, independent grant origins, per-ACL cascades, atomic refusal and full
warning vectors. Catalog cleanup restores shared ACLs before dropping actors.
Backing-source credentials and function EXECUTE remain independently protected.
Transactional controls retain committed-only capture and stale-publication
fences, then prove a committed grant survives replacement with its roles intact.

Policy metadata witnesses read physical `pg_policy` and both `pg_get_expr`
signatures through shared direct/prepared helpers. They preserve raw role order,
duplicates and absent CHECK separately from effective RLS checks. Known-OID
metadata remains readable independently of target/schema execution privileges.
The authorization witness explicitly revokes PUBLIC schema USAGE before testing
unrelated actors: native bootstrap grants it, while the oracle fixture's rebuilt
public does not. Exact provider/sequence qualification follows current captured
namespace visibility; this is independent of PUBLIC metadata availability.
Transaction peers use scalar/raw-tree observations while ALTER owns its relation
lock, and exact display before/after lock release. Native control phases retain
actual Dump fidelity/replay assertions alongside common logical metadata.


Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Ownership, REASSIGN/DROP OWNED, DROP ROLE: `RUN='^TestReferenceRoleOwnership'`
- Table and column GRANT: `RUN='^TestReferenceRoleGrant'`
- Table and column REVOKE: `RUN='^TestReferenceRoleRevoke'`
- Default privileges: `RUN='^TestReferenceRoleDefaultPrivileges'`
- Transactional ACL/default creation, delegation, controls and held execution:
  `RUN='^TestReferenceTransactionalPrivileges(Creation|Defaults|Savepoints|Delegation|Prepared|Controls)$'`
- Private publication/lineage and cancellation invariants:
  `RUN='^TestTransactionalPrivileges'`
- Predefined roles: `RUN='^TestReferencePredefined'`
- Migrated ANALYZE predefined-role consumers:
  `RUN='^TestReferencePredefined(UnavailableCapabilities|Maintain|MaintainFeatureSmoke)$'`
  requires both native mode and `REFERENCE=1`.
- `pg_has_role`, `row_security_active`: `RUN='^TestReferenceAccessRole'`
- `row_security` setting: `RUN='^TestReferenceRowSecurity'`
- Policy DDL: `RUN='^TestReferenceRLSPolicy'`
- Transactional policy definitions and existing RLS consumers: `RUN='^TestReferenceTransactionalPolicies(Routes|Retention|Savepoints|Diagnostics|Prepared|Dependencies|Locks|Controls)$'`
- Policy catalog metadata, definitions, frozen expression display and authorization:
  `RUN='^TestReferencePGPolicy'`
- Table RLS flags, default deny, BYPASSRLS: `RUN='^TestReferenceRLSBase'`
- Policy applicability and combination: `RUN='^TestReferenceRLSCombination'`
- Existing-row USING filtering: `RUN='^TestReferenceRLSFilter'`
- Proposed-row checks, transformations and statement atomicity: `RUN='^TestReferenceRLSCheck'`

- Grant and ownership consumer regressions:
  `RUN='^TestReferenceRole(InputPrecedence|NamespaceLookup|LaterDependent|DependencyAddress|PublicGrantOptions)'`
- Engine units use the family names without `Reference`, for example
  `make check RUN='^TestRLSPolicy'` or `make check RUN='^TestDefaultPrivileges'`.
