# internal/engine/access

Authorization enforcement on catalog objects. It sits above `catalog` (layer
L4 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture))
and imports `catalog`, `authorization`, `ids`, `parser`, `sqlstate`, `sqltype`
and the standard library. It never imports the engine. The role graph and ACL
algorithms it applies live in [`authorization`](../authorization/README.md).

Language descriptors use the ordinary owner/ACL/class-mask and paired role
authority. Trusted default PUBLIC USAGE is distinct from explicit empty ACL;
DO/CREATE admission checks USAGE while an existing call checks routine EXECUTE.
Language ACL entries participate in the same principal dependencies and cleanup.
No parallel language-permission table or cached grant decision exists.


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
    Virtual catalog views use the same effective ACL and column authority as
    stored views and report the view noun in authorization diagnostics. Source
    production never substitutes original authentication for current authority.
    Routine EXECUTE denials distinguish procedures using canonical `RoutineKind`,
    not the shared `ProcedureObject` class or the presence of a SQL body.
    Generic routine ownership denials retain the function noun used by
    CREATE OR REPLACE; statement-specific callers supply their own nouns.
  - Identity: `Ref` mints a reference in the view's epoch, `Stamp` names the
    captured publication, `Recapture` binds a substituted catalog to the same
    role root.
  - Inquiries: `RelationPrivilege` receives a runtime-resolved existing
    relation/valid attribute and ordinary/option alternatives. It follows the
    inheritable role closure, preserves relation-kind absent ACL defaults and
    implicit owner options, and scans only live positive columns for any-column
    checks. Requested rights are not clipped to grant-admission class masks.
  - Helpers: `DescriptorACL`, `ClassPrivileges`, `ValidateACL`. Global
    access methods have no owner or ACL: `ValidateACL` classifies them with
    the other ownerless descriptors, while their catalog provider validates
    structural and handler authority. Owning descriptors reject every
    zero or foreign principal.
- **`Environment`**: the two session inputs of the checks.
  `TemporaryNamespace()` returns the session's own TEMP namespace, and
  `ReferencePath()` returns the search path that qualifies type names in
  diagnostics. Both are read at call time, never captured, and only when a
  check needs them (a TEMP namespace, a type denial). Keep it exactly two
  methods wide: it is not a service locator.
- **Roles** (`roles.go`): `RoleRecord` (a committed root and its revision,
  with `Advance`) and `RoleRead`, the role view of a record as seen by a
  catalog read's database owner.
  `Advance` changes only the revision and preserves a normalized immutable
  authority origin; an actual root mutation constructs a fresh record.
  `SameAuthority` is the fast path for checked ACL-only publication and the
  selected pending-statement authority fence. It is not a blanket completed
  publication permission: compatible actual roots use the existing explicit
  merge. It never replaces exact `Stamp` validation or caches permission.
- **Role store** (`role_store.go`): `RoleStore`, the engine's role state:
  the principal `Domain`, the `ACLAuthority`, the committed `Published`
  record and the principal and revision allocators. It has no mutex; the
  engine mutex guards every field and mutating methods carry the `Locked`
  suffix. Cluster runtimes share one pointer-stable store, because
  `catalog.Owner.Principals`, `catalog.Owner.ACLAuthority` and
  `Root.Reprovenance` keep pointers into it, so it is never copied (a
  `noCopy` guard lets `go vet` report copies).
  - Publication: `Published` changes only through `PublishLocked`, the
    frontiers only through `InstallPrincipalFrontierLocked`,
    `InstallRevisionFrontierLocked` and `AdvanceRevisionFrontierLocked`.
    `PrincipalCandidateLocked` and `RevisionCandidateLocked` return private
    candidates; only publication advances allocation.
  - Reads: `Read` and `PublishedRead`, the store-checked `RoleRead`.
  - Replacement (`role_remap.go`, `aclitem_import.go`): `PrincipalMapper`
    and `PrincipalRemap` map a source's principals by name with
    target-incarnation fences, reading the destination `*catalog.Store`
    at each mapping; `ACLItemImporter` reowns typed aclitem values from the
    source store's ACL authority; `ValidateReplacementACLsLocked` checks a
    replacement's ACLs against the committed roles. `CaptureLocked` and
    `InstallCapture` copy a committed record and principal frontier into a
    private Restore stage's domain. `PrincipalRemap` takes the
    seeded-operator predicate as a value.
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
  `OwnershipRelationLocks` merges actual policy-deletion containing relations from
  its optional source map into the sorted/deduplicated root lock list. It expands
  companions only for ownership roots, not these surviving policy tables; PUBLIC
  or any nonselected target prevents deletion. Other callers pass nil.
- **Namespaces** (`namespace.go`): `CheckPersistentCreationNamespace`, the
  TEMP capability boundary of non-table creation and namespace moves,
  `CheckTemporaryStorageAccess`, which rejects another session's stored TEMP
  relation or sequence before storage admission, `OwnTemporaryNamespace`, which
  matches a namespace against the session's TEMP namespace address, and
  `AdjustRelationCreationPersistence`, the persistence of a relation created
  in a namespace, which reads the `Environment` only for a TEMP namespace.
- **Row security** (`rls.go`, `rls_selection.go`): metadata-only
  `RowSecurityApplies`/setting guard, role-aware `SelectRowSecurity` returning
  immutable payload groups (`RLSSelection`, `RLSGroup`, `RLSClause`),
  `SameRLSSelection`, which the engine's fence uses to detect a changed
  selection after role changes, and `RowSecurityPrincipal` over view layers. `AdjacentRowSecurityGroups`,
  `AdjacentRowSecurityProposal` and `AdjacentRLSDisposition` are exclusively
  MERGE/ON CONFLICT action capability gates, never ordinary row evaluators.
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
  `RawObjectACL` and `ObjectACLClass`. `CheckDatabaseConnect`
  (`database_connect.go`) is the CONNECT check of physical session start.
- **SQL inquiries** (`object_inquiry.go`): `ObjectPrivilege` accepts a raw actor
  OID and independent ordinary/grant-option ANY masks. Missing actors still
  receive PUBLIC rights; live superusers bypass missing object OIDs. Other
  actors receive an absent result for a missing object. Type checks delegate
  array then multirange authority; absent database/type/routine/language ACLs
  retain inquiry defaults, while explicit-empty ACLs do not. Schema checks
  include predefined data roles and physical-session TEMP authority, with
  CREATE contingent on the queried actor's database TEMP right. These rules
  do not broaden admission or change database attachment authority.
  The core special-inquiry boundary establishes large-object existence before
  this evaluator's superuser shortcut, using the active LO frontier unless the
  ordinary mask includes UPDATE. Missing parameter ACL identities instead
  produce false for non-superusers, without requiring a setting provider.

`access` keeps its own `SQLError`. Type and identifier names in its messages
come from the `catalog` diagnostic name helpers it shares with the engine.

## Invariants

- Privilege decisions, SQLSTATEs, messages and grantor selection match
  PostgreSQL 18; the reference suite is the oracle.
- A view is paired with its exact `catalog.Read`. There is no cached ACL
  decision. A view rejects references and catalogs its owner does not back,
  stale epochs and an unpaired role read, before any superuser shortcut.
  Zero, foreign and stale principals are rejected first by admission checks;
  the raw-OID SQL inquiry boundary deliberately accepts nonexistent actors.
- Absent and empty ACLs stay distinct.
- No mutex and no engine callback: callers hold whatever lock their reads
  need (`DatabaseRights` on live roots needs the engine mutex).

## What stays in the engine

- `AuthorizationView`: statement-level authority that embeds `View` and adds
  the session, settings, definition reader and path inputs. It holds no
  engine: one constructor gives it a pointer to the engine's
  `AuthorityInputs` (the engine's domain for path references and the TEMP
  token check, the settings registry for the boot `search_path`, and
  the published storage that `StorageLocked` reads under the engine mutex),
  and statement identity checks compare the view's catalog owner. Its `Check`,
  `CanUse`, `CheckCreate`, `Effective` and `CheckColumns` are entry points
  that pass the view itself as the `Environment`. It implements
  `analyze.Privileges` for the binding walkers.
- View capture (`authorizationLocked`, `statementAuthorizationLocked`,
  `OperationAuthorization`), `Engine.RoleStore` (the cluster's `*RoleStore`),
  the session role overlays (`sessionRolesLocked`,
  `installOperationRolesLocked`, `preparedActorLocked`) and the catalog
  authority fences.
- The lock windows, staging and publication of role DDL, GRANT/REVOKE,
  ALTER DEFAULT PRIVILEGES and the ownership statements. Their decisions are
  in [`role`](../role/README.md), which calls this package over the captured
  `View`.
- Search path resolution (functions over `AuthorizationView`) and
  `temporaryNamespace` and RLS configuration. The binding and rule-program
  `row_security` walkers, which call `access` for each object, are
  `view.CheckRowSecurityBinding` and `view.CheckRowSecurityRuleProgram`; the
  stored-view source walker is `view.CheckRowSecuritySource`.
  The binding walkers (`CheckBinding`, `CheckQuerySources`) are in
  `analyze`; the walkers over view definitions and view writes
  (`CheckWrite`, `AuthorizeSource`, `CheckRowMarks`, `QueryTableRefs`) are
  in [`view`](../view/README.md), over the captured `View` and the
  engine's `AuthorizationView` as the `Environment`.

## Testing

The package tests build a catalog owner, a seeded role root and a current
database without an engine, and pin the view's owner, pairing and epoch
rejection, `RoleRead`'s owner and domain checks, the lazy TEMP environment,
`RowSecurityPrincipal`, the role store's read fences, allocators and Restore
capture, the row-security setting guard, DROP OWNED policy
cleanup, the `RowSecurityKind` fallback, the TEMP namespace helpers and the
ownerless ACL validation of access methods and casts. Routine denial checks pin
canonical function/window/procedure kinds independently of a SQL body, while
preserving generic replacement-ownership wording. Engine unit tests exercise
the checks through `AuthorizationView`.
System-read versus ordinary-column authority uses `RUN='^TestQueryTableOIDAdmission'`.


```sh
make check RUN='^Test(AccessMethodOwnerlessACLValidation|AdjustRelationCreationPersistence|RoleStore|CastDefinitionOwnerlessACL|DropOwnedPolicyTargets|OperatorFamilyMemberOwnershipValidation|OwnTemporaryNamespace|RoleReadRequiresOwnerDomainAndRoot|RowSecurity(KindDelegates|PrincipalFollows|SettingGuard)|View(RejectsForeign|RoutineDenialKind|TemporaryNamespaceOnly))'
make check RUN='^Test(AccessView|RoleStore|DatabaseTemporaryCapturedRights|DataTypePrivilegesCapture|ObjectACLAddressLookup|RLS|Ownership|RoleOwnership|DropOwned|ReassignOwned)'
make check RUN='^TestReference(AccessRole|ForeignActionsAuthorization|SchemaAuthorization$$|TypedTableAuthorization$$)'
```

Access-method publication and control witnesses are in the
[access-method suite](../../../reference/indexes/README.md#access-method-definitions).
