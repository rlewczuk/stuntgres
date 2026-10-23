# internal/engine/role

Role and ACL DDL decisions: GRANT/REVOKE targets, request and plan,
database GRANT, the SET ROLE and SESSION AUTHORIZATION targets, DROP ROLE,
the ALTER DEFAULT PRIVILEGES plan, the ALTER ... OWNER checks, the REASSIGN
OWNED and DROP OWNED plan and staging, and the role diagnostics. The handler
decisions reach the engine through `Host` and `StageHost`. The role graph and
ACL algorithms are in [`authorization`](../authorization/README.md); the
checks over a captured view are in [`access`](../access/README.md).

The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): it
imports `access`, `analyze` (types and pure functions), `authorization`,
`catalog`, `ddl`, `extension`, `ids`, `regobj`, `routine`, `sequence`,
`session`, `stored`, `parser`, `sqlstate` and the standard library, and
never the engine, `analyze`'s binder or another upper feature. See
[ENGINE-SPLIT-role.md](../../../docs/ENGINE-SPLIT-role.md).

## What it owns

- **Hosts** (`host.go`): `Host` serves the decisions made outside a catalog
  stage (`AccessView`, `SearchNamespaces`, relation, argument type and
  parameter lookups, the features referencing a role and the DROP ROLE
  dependency text); `StageHost` is `ddl.Host` plus `AddDomainBase`,
  `RemoveObject` and `OwnerStage` for REASSIGN and DROP OWNED staging. The
  engine's `ddlHost` implements both (`role_host.go`); for `Host` it has no
  stage.
- **GRANT/REVOKE on objects** (`grant_plan.go`, `grant_targets.go`,
  `grant_objects.go`): `ObjectGrantRequest(h, auth, s)` validates grantor,
  kind, targets, grantees, privileges and GRANT OPTION for PUBLIC in
  PostgreSQL's order. Its targets (`objectGrantTargets`: parameters,
  relations, tablespaces, schemas, types, domains, routines and ALL ... IN
  SCHEMA in OID order) resolve through `Host` after the kind check.
  `ObjectGrantPlan` builds the new table, column and non-relation ACLs into
  a `GrantPlan` (`Candidate`, `Changed`); sequence plan stamps come from a
  `revision` allocator; the partial-grant warning is
  `authorization.GrantWarning`, which database and FDW grants share.
- **Database GRANT** (`database_grant.go`): `PlanDatabaseGrant` returns the
  new database descriptor (nil: unchanged) and the warnings, also before a
  returned error.
- **Role DDL** (`roles.go`): `SetRoleTarget` and
  `SessionAuthorizationTarget` pick and check the new role;
  `PlanDropRoles` checks DROP ROLE (superuser gate, 42704, 55006,
  predefined roles, sorted 2BP01 dependency details) and returns the
  retired role record in a `DropRolesPlan`.
- **Default privileges** (`default_privileges.go`): `DefaultPrivilegesPlan`
  returns the default ACL records ALTER DEFAULT PRIVILEGES changes, without
  OIDs.
- **REASSIGN/DROP OWNED** (`owned.go`, `owned_cleanup.go`): `OwnedSources`
  and `PlanOwned` resolve and authorize the roles and pick the roots
  (`OwnedPlan`); `StageOwnedCleanup` stages DROP OWNED's ACL, policy,
  default-privilege and membership cleanup; `OwnedCleanupAddsDeletionRoot`
  decides the singular/plural diagnostic primary.
- **Ownership** (`ownership.go`): `PlanTypeOwnership`,
  `PlanDatabaseOwnership` and `PlanRelationOwnership` check ALTER ... OWNER
  and return an `OwnerChange` (nil `Object`: owner unchanged);
  `StageOwnership` stages a new owner for one descriptor and its reciprocal
  members.
- **Owned staging** (`owned_stage.go`): `StageOwned` stages REASSIGN
  OWNED's eligibility checks, owners and provider expressions, or DROP
  OWNED's removals, over `StageHost`; `OwnedDependentsError` rewords DROP
  OWNED's 2BP01.
- **Stage interfaces** (`stage.go`): `OwnerStage` (`ddl.Stage` plus the base
  catalog and cascade revisions) and `OwnedCleanupStage` (removals, default
  ACL records and the role candidate). The engine's `*CatalogStage`
  implements both.
- **Diagnostics** (`diagnostics.go`): `Diagnostic`, `LoginError`,
  `ConnectionLimitError`, `EmitPasswordNotice` and
  `DependencyDescription` (the DROP ROLE and DROP OWNED detail lines over
  the `access.View` and the reference path, rendered with
  `stored.DependencyObject`; the root's `DescribeRoleDependency` resolves
  the path).

## Invariants

- **Authority is a per-call value.** Every function takes the captured
  `access.View` (and an `access.Environment` where it needs the reference
  path), or a `ddl.Authority` it reads through the host; none captures or
  caches authority. `PlanOwned` names objects through a `describe`
  callback, which the root builds over `stored.DependencyObject`.
- **No locks, no publication.** The package adds no mutex and publishes
  nothing. Every `Host` and `StageHost` method runs with `Engine.mu` held;
  none takes, releases or waits on a lock. Plans return candidates and
  warnings; the engine emits the warnings (in statement order, also before a
  returned error), allocates OIDs and revisions and installs the result.
- **Role publication stays atomic.** `StageOwnedCleanup` joins its
  membership deletion to the stage through `SetRoleCandidate`, so it
  publishes with the catalog change.
- **Errors.** Messages, SQLSTATEs, details and hints are compatibility
  contracts.

## What stays in the engine

`Engine.mu` and its unlock/relock windows, lock waits, `AuthorizationView`
capture, `roleStore` publication and `installOperationRolesLocked`,
`SetObjectACL`/`SetRoleReferences`, the host adapter (`role_host.go`) and
the `*CatalogStage` adapters (`owned_stage.go`), the parameter targets
(`parameter_catalog.go`), and the orchestration: `initialRole`,
`executeRole` (dispatch, the SET LOCAL warning, session mutation,
`publishDroppedRolesLocked`),
`executeRoleAttributes`, `membershipLocked`, `objectGrantLocked`,
`executeDatabaseGrant` and `alterDefaultPrivilegesLocked` (warnings,
OIDs and installation), the three ALTER OWNER executors (resolution, the
materialized view authority, `ownershipStage`, lock waits,
`publishOwnership`) and `executeOwned` (the stage, lock waits,
`StageOwnedCleanup`, the scope dependents, publication and the cascade
notice).

## Testing

The package has no unit tests of its own: tests of its plans and staging need
an `Engine` fixture and live in `internal/engine` (`role_*_test.go`,
`ownership*_test.go`, `owned_roles_cleanup_test.go`, `object_acl_*_test.go`).
`TestRoleHostStageless` (`role_host_test.go`) pins the host seam: a
stage-less `ddlHost` as `Host`. Add `RACE=1` for the connection-limit and role
switch tests.

```sh
make check RUN='^Test(Role|ObjectGrant|ObjectRevoke|DefaultPrivileges|DropOwned|ReassignOwned|Membership|ObjectACL|DatabaseACL|Ownership|ParameterTablespaceGrant)'
```

Public behavior is in [reference/roles](../../../reference/roles/README.md#testing)
and [reference/object_grants](../../../reference/object_grants/README.md#testing)
(`^TestReference(Role|ObjectGrant|DefaultPrivilege|Reassign|DropOwned)`), plus
`^TestReferenceDumpRoundTrip$` for owner and ACL replay.
