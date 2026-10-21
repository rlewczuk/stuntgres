# internal/engine/role

Role and ACL DDL decisions: the GRANT/REVOKE request and plan, the ALTER
DEFAULT PRIVILEGES plan, the REASSIGN OWNED and DROP OWNED plan, ownership
and DROP OWNED staging, and the role diagnostics. Step 28 of the engine
split created it (28b moved the clean set; 28d moves the remaining handler
decisions behind a host). The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `authorization`, `catalog`, `ddl`, `extension`, `ids`,
`routine`, `session`, `parser`, `sqlstate` and the standard library, and
never the engine, `analyze`'s binder or another upper feature. See
[ENGINE-SPLIT-role.md](../../../docs/ENGINE-SPLIT-role.md).

## What it owns

- **GRANT/REVOKE on objects** (`grant_plan.go`, `grant_objects.go`):
  `ObjectGrantRequest` validates grantor, kind, targets, grantees,
  privileges and GRANT OPTION for PUBLIC in PostgreSQL's order; it calls the
  `resolve` callback for the targets at the old point, so error precedence
  is unchanged. `ObjectGrantPlan` builds the new table, column and
  non-relation ACLs into a `GrantPlan` (`Candidate`, `Changed`); sequence
  plan stamps come from a `revision` allocator; the partial-grant warning
  is `authorization.GrantWarning`, which database and FDW grants share.
  `GrantRoutineArgumentDisplay` renders a routine target's argument types.
- **Default privileges** (`default_privileges.go`): `DefaultPrivilegesPlan`
  returns the default ACL records ALTER DEFAULT PRIVILEGES changes, without
  OIDs.
- **REASSIGN/DROP OWNED** (`owned.go`, `owned_cleanup.go`): `OwnedSources`
  and `PlanOwned` resolve and authorize the roles and pick the roots
  (`OwnedPlan`); `StageOwnedCleanup` stages DROP OWNED's ACL, policy,
  default-privilege and membership cleanup; `OwnedCleanupAddsDeletionRoot`
  decides the singular/plural diagnostic primary.
- **Ownership staging** (`ownership.go`): `StageOwnership` stages a new
  owner for one descriptor and its reciprocal members.
- **Stage interfaces** (`stage.go`): `OwnerStage` (`ddl.Stage` plus the base
  catalog and cascade revisions) and `OwnedCleanupStage` (removals, default
  ACL records and the role candidate). The engine's `*CatalogStage`
  implements both.
- **Diagnostics** (`diagnostics.go`): `Diagnostic`, `LoginError`,
  `ConnectionLimitError` and `EmitPasswordNotice`.

## Invariants

- **Authority is a per-call value.** Every function takes the captured
  `access.View` (and an `access.Environment` where it needs the reference
  path); none captures or caches authority. `PlanOwned` names objects
  through a `describe` callback, so the root's DROP diagnostics stay there.
- **No locks, no publication.** The package adds no mutex and publishes
  nothing. Plans return candidates and warnings; the engine emits the
  warnings (in statement order, also before a returned error), allocates
  OIDs and revisions and installs the result.
- **Role publication stays atomic.** `StageOwnedCleanup` joins its
  membership deletion to the stage through `SetRoleCandidate`, so it
  publishes with the catalog change.
- **Errors.** Messages, SQLSTATEs, details and hints match the engine's
  previous output.

## What stays in the engine

`Engine.mu` and its unlock/relock windows, lock waits, `AuthorizationView`
capture, `roleStore` publication and `installOperationRolesLocked`,
`SetObjectACL`/`SetRoleReferences`, the `*CatalogStage` adapters
(`owned_stage.go`), grant target resolution (`objectGrantTargets`,
`resolveGrantObject`, `resolveGrantRoutine`), the role DDL handlers
(`executeRole`, `executeRoleAttributes`, `dropRolesLocked`), the ALTER
OWNER, REASSIGN OWNED and DROP OWNED orchestration, `dependencyObject` and
dispatch. 28c plans the host that 28d moves the decisions behind.

## Testing

```sh
make check RUN='^Test(Role|ObjectGrant|ObjectRevoke|DefaultPrivileges|DropOwned|ReassignOwned|Membership|ObjectACL|DatabaseACL)'
make check RUN='^TestReference(Role|ObjectGrant|DefaultPrivilege|Reassign|DropOwned)|Ownership|Owned'
make check RUN='^TestReferenceDumpRoundTrip$'
```

The package has no unit tests of its own: the tests of its plans and
staging need an `Engine` fixture and stay in `internal/engine`
(`role_*_test.go`, `ownership*_test.go`, `owned_roles_cleanup_test.go`,
`object_acl_*_test.go`). Add `RACE=1` for the connection-limit and role
switch tests, and `REFERENCE=1` to the reference selections for the real
PostgreSQL oracle.
