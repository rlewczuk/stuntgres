# internal/engine/access

Authorization enforcement on catalog objects. It sits above `catalog` (layer
L4 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture))
and imports `catalog`, `authorization`, `ids`, `parser`, `sqlstate` and the
standard library. It never imports the engine. The role graph and ACL
algorithms it applies live in [`authorization`](../authorization/README.md).

## What it holds

- **`View`** (`view.go`): the immutable captured pair. It holds the catalog
  owner (`*catalog.Owner`, which identifies the engine), the `catalog.Read`
  and its paired read, the role publication record (`RoleRecord`), the
  `authorization.Read`, the committed database predecessor, the effective and
  session `RoleState`, the session authorization revision and
  `MissingCurrent`. Fields are exported so the engine can re-target copies of
  a view (a substituted catalog, an elevated current user); the captured
  roots themselves are never written.
  - Checks: `Effective`, `Check`, `CanUse`, `CheckCreate` and `CheckColumns`,
    each taking an `Environment`. `Object` resolves a reference.
  - Identity: `Ref` mints a reference in the view's epoch, `Stamp` names the
    captured publication, `Recapture` binds a substituted catalog to the same
    role root.
  - Helpers: `DescriptorACL`, `ClassPrivileges`, `ValidateACL`.
- **`Environment`**: the two session inputs of the checks.
  `TemporaryNamespace()` returns the session's own TEMP namespace, and
  `ReferencePath()` returns the search path that qualifies type names in
  diagnostics. Both are read at call time, never captured, and only when a
  check needs them (a TEMP namespace, a type denial). Keep it exactly two
  methods wide: it is not a service locator.
- **Roles** (`roles.go`): `RoleRecord` (a committed root and its revision,
  with `Advance`) and `RoleRead`, the role view of a record as seen by a
  catalog read's database owner.
- **Ownership** (`ownership.go`): `OwnershipSuccessor`,
  `OwnershipEligibility`, `OwnershipDatabaseCreate`, `SchemaOwner` and
  `OwnershipRoleOperand`, which share the `current_user`/`session_user`
  string protocol, `OwnershipRoot`, the bulk-root rule of REASSIGN and
  DROP OWNED, and `ForeignOwnershipEligibility`, the owner change rule of
  foreign-data wrappers and servers that ALTER SERVER (`fdw`) and REASSIGN
  OWNED (`role`) share.
- **Owned objects** (`owned.go`): `OwnedRoots`, `OwnershipRelationLocks`,
  `OwnedPolicies` and `OwnedMembershipCandidate`, the pure parts of
  REASSIGN and DROP OWNED. `OwnershipRoot` and `OwnedRoots` take the
  seeded-operator predicate as a value (`routine.OperatorSeeded` in the
  engine), because `routine` imports `access`.
- **Namespaces** (`namespace.go`): `CheckPersistentCreationNamespace`, the
  TEMP capability boundary of non-table creation and namespace moves,
  `CheckTemporaryStorageAccess`, which rejects another session's stored
  TEMP relation before storage admission, `OwnTemporaryNamespace`, which
  matches a namespace against the session's TEMP namespace address, and
  `AdjustRelationCreationPersistence`, the persistence of a relation created
  in a namespace, which reads the `Environment` only for a TEMP namespace.
- **Row security** (`rls.go`): `RowSecurityApplies`, `RowSecurityDecision`,
  `RowSecurityGroups` (returning an `RLSDisposition`), `RowSecurityProposal`,
  `RowSecurityExpressionError` and `RowSecurityPrincipal` over the view
  layers (`RowSecurityLayer`) the engine collects.
- **Row-security actions** (`rls_actions.go`): `RowSecurityActions`,
  `RowSecurityFlags` and `RowSecurityKind`, the ALTER TABLE ENABLE, DISABLE,
  FORCE and NO FORCE ROW LEVEL SECURITY rules. `RowSecurityKind` takes the
  diagnostic for the other relation kinds as a value
  (`analyze.TableKindError` in the engine), because `analyze` imports
  `access`.
- **Role dependencies** (`role_dependencies.go`): `RoleDependency`,
  `DescriptorRoleDependencies` and `DefaultRoleDependencies`. A nil collector
  stops at the first dependency.
- **Privileges** (`privileges.go`): `DatabaseRights`, `PredefinedPrivileges`,
  `RawObjectACL` and `ObjectACLClass`.

`access` keeps its own `sqlError`. Type and identifier names in its messages
come from the `catalog` diagnostic name helpers it shares with the engine.

## Invariants

- Privilege decisions, SQLSTATEs, messages and grantor selection match
  PostgreSQL 18; the reference suite is the oracle.
- A view is paired with its exact `catalog.Read`. There is no cached ACL
  decision. A view rejects references and catalogs its owner does not back,
  stale epochs and an unpaired role read, before any superuser shortcut.
  Zero, foreign and stale principals are rejected first.
- Absent and empty ACLs stay distinct.
- No mutex and no engine callback: callers hold whatever lock their reads
  need (`DatabaseRights` on live roots needs the engine mutex).

## What stays in the engine

- `AuthorizationView`: statement-level authority that embeds `View` and adds
  the session, settings, definition reader and path inputs. It holds no
  engine (B7): one constructor gives it a pointer to the engine's
  `authorityInputs` (the engine's domain for path references and the TEMP
  token check, the settings registry for the boot `search_path`, and
  the published storage that `StorageLocked` reads under the engine mutex),
  and statement identity checks compare the view's catalog owner. Its `Check`,
  `CanUse`, `CheckCreate`, `Effective` and `CheckColumns` are entry points
  that pass the view itself as the `Environment`. It implements
  `analyze.Privileges` for the binding walkers.
- View capture (`authorizationLocked`, `statementAuthorizationLocked`,
  `operationAuthorization`), role publication (`roleStore`) and the catalog
  authority fences.
- The lock windows, staging and publication of role DDL, GRANT/REVOKE,
  ALTER DEFAULT PRIVILEGES and the ownership statements; `SetObjectACL` and
  `SetRoleReferences`. Their decisions are in [`role`](../role/README.md),
  which calls this package over the captured `View`.
- Search path resolution (functions over `AuthorizationView`) and
  `temporaryNamespace`, RLS configuration and the binding and rule-program
  walkers of `row_security_setting.go`, which call `access` for each object;
  the stored-view source walker is `view.CheckRowSecuritySource`.
  The binding walkers (`CheckBinding`, `CheckQuerySources`) are in
  `analyze`; the walkers over view definitions and view writes
  (`CheckWrite`, `AuthorizeSource`, `CheckRowMarks`, `QueryTableRefs`) are
  in [`view`](../view/README.md), over the captured `View` and the
  engine's `AuthorizationView` as the `Environment`.

## Testing

Run from the repository root under the shared
[testing policy](../../../docs/TESTING.md). The package tests build a catalog
owner, a seeded role root and a current database without an engine, and pin
the view's owner, pairing and epoch rejection, `RoleRead`'s owner and domain
checks, the lazy TEMP environment, `RowSecurityPrincipal`, DROP OWNED policy
cleanup, the `RowSecurityKind` fallback and the TEMP namespace helpers:

```sh
timeout 900s make check RUN='^(TestViewRejectsForeignOrStaleObjects|TestRoleReadRequiresOwnerDomainAndRoot|TestViewTemporaryNamespaceOnlyFromEnvironment|TestRowSecurityPrincipalFollowsInnermostDefiner|TestDropOwnedPolicyTargets|TestRowSecurityKindDelegatesOtherKinds|TestOwnTemporaryNamespaceNeedsTheSessionTemporarySchema|TestAdjustRelationCreationPersistenceReadsTemporaryLazily)$'
```

Engine unit tests exercise the checks through `AuthorizationView`:

```sh
timeout 900s make check RUN='^(TestAccessView.*|TestRoleStore.*|TestDatabaseTemporaryCapturedRights|TestDataTypePrivilegesCapture|TestObjectACLAddressLookup|TestRLS.*|TestOwnership.*|TestRoleOwnership.*|TestDropOwned.*|TestReassignOwned.*)$'
```

Public behavior is covered by the dual-mode reference tests; run each with
`REFERENCE=1` and in quick mode:

```sh
timeout 900s make check REFERENCE=1 RUN='^(TestReferenceAccessRole.*|TestReferenceForeignActionsAuthorization.*|TestReferenceSchemaAuthorization|TestReferenceTypedTableAuthorization)$'
```
