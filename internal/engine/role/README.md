# internal/engine/role

Role and ACL DDL decisions: GRANT/REVOKE targets, request and plan,
database GRANT, the SET ROLE and SESSION AUTHORIZATION targets, DROP ROLE,
the ALTER DEFAULT PRIVILEGES plan, the ALTER ... OWNER checks, the REASSIGN
OWNED and DROP OWNED plan and staging, the parameter grant catalog, the
COMMENT decisions, the role retirement preflight and the role diagnostics. The
handler decisions reach the engine through `Host` and `StageHost`. The role graph and
ACL algorithms are in [`authorization`](../authorization/README.md); the
checks over a captured view are in [`access`](../access/README.md).

The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): it
imports `access`, `analyze` (types and pure functions), `authorization`,
`catalog`, `ddl`, `extension`, `ids`, `pgcatalog`, `regobj`, `routine`, `sequence`,
`session`, `settings`, `stored`, `parser`, `sqlstate`, `sqltype` and the standard
library, and never the engine, `analyze`'s binder or another upper feature.

## What it owns

- **Hosts** (`host.go`): `Host` serves the decisions made outside a catalog
  stage (`AccessView`, `SearchNamespaces`, relation, argument type and
  parameter lookups, other databases' role dependencies and the DROP ROLE
  dependency text); `StageHost` is `ddl.Host` plus `AddDomainBase`,
  `RemoveObject` and `OwnerStage` for REASSIGN and DROP OWNED staging.
  [`rolecmds`](../rolecmds/README.md)' `RoleHost` implements both; for
  `Host` it has no stage. Core calls `OtherDatabaseDependencies` on the core
  `DDLHost`.
- **GRANT/REVOKE on objects** (`grant_plan.go`, `grant_targets.go`,
  `grant_objects.go`): `ObjectGrantRequest(h, auth, s)` validates grantor,
  kind, targets, grantees, privileges and GRANT OPTION for PUBLIC in
  PostgreSQL's order. Its targets (`objectGrantTargets`: parameters,
  relations, tablespaces, languages, schemas, types, domains, routines and ALL ... IN
  SCHEMA in OID order) resolve through `Host` after the kind check.
  `ObjectGrantPlan` builds the new table, column and non-relation ACLs into
  a `GrantPlan` (`Candidate`, `Changed`); sequence plan stamps come from a
  `revision` allocator; the partial-grant warning is
  `authorization.GrantWarning`, which database and FDW grants share.
  The root admits named relations with AccessShare before ACL planning and may
  refresh the request-owned `Targets` inventory after a wait. Once staged,
  descriptor identity and epoch remain strict. Selected ACL/default commands
  return scope-owned candidates: the root merges them into the transaction
  journal at Finish and publishes them with other private work, never here.
  Narrow LANGUAGE USAGE resolves real catalog language identities and uses this
  same grant-option/revoke/cascade/role-dependency authority. It does not add
  language lifecycle or grant a native interpreter/ABI execution capability.
  Named registered catalog tables and provider views use ordinary table/column
  GRANT/REVOKE masks, grantor selection and atomic ACL publication. Admission checks the
  captured descriptor against its source registration and stable attribute/type
  identities; neither names/OIDs alone nor synthetic catalog descriptors suffice.
  ACL replacement does not change registration identity. Catalog write guards,
  backing-function EXECUTE and underlying-source ACLs remain independent.
  Registration is checked against the original captured target before reusing
  statement-private ACL replacements. Registered views omit negative system
  attributes from implicit column-revoke warning traversal, like ordinary views.
- **Database GRANT** (`database_grant.go`): `PlanDatabaseGrant` returns the
  new database descriptor (nil: unchanged) and the warnings, also before a
  returned error.
- **Role DDL** (`roles.go`): `SetRoleTarget` and
  `SessionAuthorizationTarget` pick and check the new role;
  `PlanDropRoles` checks DROP ROLE (superuser gate, 42704, 55006,
  predefined roles, sorted 2BP01 dependency details) and returns the
  retired role record in a `DropRolesPlan`.
- **Rename and persisted defaults**: pure decisions resolve role and database
  targets from the captured authority, check effective administration or actual
  database ownership and return immutable candidates. Rename preserves principal
  identity and settings; role-specific defaults use principal/database identities
  and never mutate the issuing session's settings.
  `PlanDatabaseSettingsTarget` resolves the named database before ownership,
  admits inherited ownership or superuser without CREATEDB/CONNECT checks, and
  renders the ALTER DATABASE owner diagnostic with its raw decoded name.
  ALTER ROLE's existing role-first order and identifier diagnostics stay separate.
- **Default privileges** (`default_privileges.go`): `DefaultPrivilegesPlan`
  returns the default ACL records ALTER DEFAULT PRIVILEGES changes, without
  OIDs.
  The root owns OID assignment, default-key writer admission and the paired
  catalog/role journal. Creator defaults are read from the same private view as
  object creation; already-private outputs are not committed input fences.
- **REASSIGN/DROP OWNED** (`owned.go`, `owned_cleanup.go`): `OwnedSources`
  and `PlanOwned` resolve and authorize the roles and pick the roots
  (`OwnedPlan`); `StageOwnedCleanup` stages DROP OWNED's ACL, policy,
  default-privilege and membership cleanup; `OwnedCleanupAddsDeletionRoot`
  decides the singular/plural diagnostic primary.
  Historical initial ACLs participate independently: DROP ROLE and its final
  retirement preflight include original grantors/owners and grantees with shared
  dependency kind `i`, rendered as “initial privileges for”. REASSIGN OWNED
  rewrites snapshot-only references with the normal ACL owner replacement and
  duplicate-entry merge rules; bare ALTER OWNER deliberately does not.
  DROP OWNED revokes selected historical grantees as the current object owner,
  with RESTRICT, after object removals against the coherent candidate. Empty
  snapshots disappear without resurrecting object/column history. Entries from
  an old owner or another grantor can remain dependencies until REASSIGN OWNED.
- **Ownership** (`ownership.go`): `PlanTypeOwnership`,
  `PlanDatabaseOwnership`, `PlanLargeObjectOwnership` and `PlanRelationOwnership`
  check ALTER ... OWNER and return an `OwnerChange` (nil `Object`: unchanged).
  Large objects resolve recipient before execution-time NumericOnly OID input;
  owner privileges and the recipient SET path are independent, with no namespace
  CREATE requirement. `StageOwnership` rewrites owner/ACL descriptors and reciprocal
  members without replacing immutable type, storage or sparse large-object content.
  Language transfer uses the same immutable descriptor/ACL rewrite, preserving
  handler identities, installation origin and current extension membership.
  Bulk language eligibility has no namespace CREATE requirement; DROP OWNED
  retains the shared intrinsic-pin, extension-member and dependency protections.
- **Owned staging** (`owned_stage.go`): `StageOwned` stages REASSIGN
  OWNED's eligibility checks, owners and provider expressions, or DROP
  OWNED's removals, over `StageHost`; `OwnedDependentsError` rewords DROP
  OWNED's 2BP01.
  Shared subscriptions contribute one concrete owner dependency regardless of
  the observer's database. REASSIGN OWNED rewrites them across databases using
  ordinary ownership eligibility and shared object locks; DROP OWNED retains
  them, as it retains other shared objects. Remote publication and connection
  names are not local role dependencies.
- **Parameter grant catalog** (`parameters.go`): `ParameterProvider`
  validates parameter ACL identities against the borrowed setting registry;
  `CanonicalGrantParameter` accepts registered and custom dotted names
  (42704 otherwise); `GrantParameterDescriptorLocked` returns the existing
  identity or a baseline one owned by the bootstrap superuser, with an OID
  and revision from the engine's allocators. `RoleHost`'s `CanonicalParameter`
  and `GrantParameter` call them.
  `CanonicalInquiryParameter` is deliberately separate: it folds arbitrary
  names and released legacy aliases without validating or creating providers.
- **COMMENT** (`comment.go`): `PlanRoleComment` checks COMMENT ON ROLE
  (42704, the superuser rule) and returns the next role record;
  `PlanObjectComment` resolves a schema, extension, table, foreign table or column
  (relations through the engine's locked ref), checks kind, schema USAGE where
  applicable and live ownership, and returns an immutable replacement revision.
  Extension headers have no schema-USAGE requirement.
  These existing kinds work in autocommit and explicit transactions. Object
  metadata is database-local; role text is cluster-shared, with private changes
  visible only to the transaction owner until commit. Role renaming remains a
  separate command family.
- **Role retirement** (`retirement.go`): `RetirementPreflightLocked`, the
  final-publication check that no default ACL, descriptor or membership edge
  still references a retired role.
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
- **Retirement admission precedes waiting.** The actor gate precedes target
  lookup; each target's current/session identity checks precede its lock wait.
  Engine readmission repeats these shared checks against the captured principal
  before planning whole-group dependencies and atomic retirement.
- **Scope stays bounded.** Object/default ACL transaction support does not widen
  existing role lifecycle/membership or ownership policies, enable ALTER SYSTEM
  or language execution/ABI capabilities, or admit SQL PREPARE of these utilities.
- **No locks, no publication.** The package adds no mutex and publishes
  nothing. Every `Host` and `StageHost` method runs with `Engine.Mu` held;
  none takes, releases or waits on a lock. Plans return candidates and
  warnings; the engine emits the warnings (in statement order, also before a
  returned error), allocates OIDs and revisions and installs the result.
- **Role publication stays atomic.** `StageOwnedCleanup` joins its membership
  deletion to the stage through `SetRoleCandidate`. The engine validates a nonnil
  candidate against the session's current private role predecessor and attaches it
  to `Journal.Roles`, retaining the actual write's committed delta base in
  `RolesBase` until a successful immutable journal refresh advances both roots.
  A nil candidate preserves earlier pending roles and does not create a write
  fence. Catalog, ACL/default/policy and membership changes publish together.
- **Errors.** Messages, SQLSTATEs, details and hints are compatibility
  contracts.

## What stays in the engine

Core keeps `Engine.Mu` and its unlock/relock windows, lock waits,
`AuthorizationView` capture, `access.RoleStore` publication and
`InstallOperationRolesLocked`, the `*CatalogStage` adapters
(`owned_stage.go`), the `ParameterProvider` registration, the
`roleRetirementPreflightLocked` adapter, `initialRole` and relation
ownership (`ExecuteRelationOwnership`, `OwnershipStage`, `PublishOwnership`).
The statement executors are [`rolecmds`](../rolecmds/README.md)': role
attributes and membership, SET ROLE and SESSION AUTHORIZATION, DROP ROLE
(`PublishDroppedRolesLocked`), object and database GRANT and ALTER DEFAULT
PRIVILEGES (warnings, OIDs and installation), the type, domain, database and
large-object OWNER executors and `ExecuteOwned` (the stage, lock waits,
`StageOwnedCleanup`, the scope dependents, publication and the cascade
notice), with the host adapter `RoleHost`. `stageCommentLocked` (the
locked-relation check, capture, the private role record installation or
object stage) is [`schemacmds`](../schemacmds/README.md)'.

Generic table/type/domain/database/large-object OWNER, feature-specific view/materialized OWNER
and REASSIGN/DROP OWNED use `session.TransactionalSchemaUtility` for the shared
Describe/Execute transaction boundary. Autocommit, explicit SQL/Go transactions
and retained protocol messages use the same stages and see earlier successful own
changes. Savepoints and rollback restore private definitions, ownership and
cleanup together. Bulk relation/type/schema changes remain database-local; shared database-owner
projection and cross-database retirement checks stay with the root.

`installOperationRolesLocked` publishes ordinary autocommit changes but installs
an immutable journal role root for active explicit or retained-message/protocol
transactions. The first actual role mutation captures its committed predecessor as
`RolesBase`; later mutations chain against the current private predecessor.
Read/publication refresh merges the exact descriptor/name/membership delta with
disjoint committed changes, cloning the journal and advancing base and candidate
without mutating retained roots or savepoint marks. Changed touched predecessors
still veto publication; a different root identity or revision alone does not.
Finish and savepoints own failure disposal and restoration. Combined commit
preserves unrelated role work and validates retirement dependencies across databases.
Completed catalog metadata, including ACL/default-privilege changes, does not
retain its execution authorization or pin an unrelated committed role root.
Pending query-derived rows and materialized replacements keep their separate
producer role-read fence until publication; actual role deltas retain exact
touched descriptor, name and membership preconditions.
Role object, tuple and name admission uses the cluster's canonical lock manager
outside `Engine.Mu`. Session authorization changes only the physical owner's
identity; commit retains it, while full or savepoint rollback restores it.
Dump omits shared roles, and Reset/Restore preserve their committed state.

## Testing

`TestCanonicalGrantParameterTargets` and `TestAccessSpecialParameterNames`
cover validating grant names and permissive inquiry spelling independently.
Tests of the other plans and staging need an `Engine` fixture.
Roles, membership, GRANT and REVOKE, default privileges, ownership and DROP
OWNED units live in `internal/engine/rolecmds`, COMMENT units in
`internal/engine/schemacmds`, `object_acl_test.go` and
`parameter_tablespace_grant_test.go` in `internal/engine/core`, and
`object_acl_fixture_test.go` in `internal/engine`.
`TestRoleHostStageless` (`role_host_test.go`) pins the host seam: a
stage-less `RoleHost` as `Host`. Add `RACE=1` for the connection-limit and role
switch tests.

```sh
make check RUN='^Test(Role|ObjectGrant|ObjectRevoke|DefaultPrivileges|DropOwned|ReassignOwned|Membership|ObjectACL|DatabaseACL|Ownership|ParameterTablespaceGrant|Comment|CanonicalGrantParameter)'
```

Public behavior is in [reference/roles](../../../reference/roles/README.md#testing)
and [reference/object_grants](../../../reference/object_grants/README.md#testing)
(`^TestReference(Role|ObjectGrant|DefaultPrivilege|Reassign|DropOwned)`), plus
`^TestReferenceDumpRoundTrip$` for owner and ACL replay and
`^TestReference(Comment|PGShDescription|PGParameterACL)` for COMMENT ON ROLE
and parameter ACLs.
