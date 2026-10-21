# Engine split, step 28: `internal/engine/role`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-role.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 28; §3 (upper
  features, rule 6); §4.1 patterns 3, 4 and 6; the
  [`access` README](../internal/engine/access/README.md) ("What stays in
  the engine").
- **Needs:** 28a (linear item 103): —. 28b (item 104): 28a, `regobj` 21b
  (item 65, takes the aclitem I/O) and access B6 (item 79, takes the pure
  ownership helpers). 28c (item 105): 28b and `ddl` 20b (item 61). 28d
  (item 106): 28c.
- **Priority:** lower than the other upper features. Less code moves here,
  and the publication of roles stays in the root anyway. Run it when no
  higher item is ready.
- **Effort:** high for every sub-step; 28c is docs only.
- **Session size:** one sub-step per session.
- **Linear items:** 28a = 103, 28b = 104, 28c = 105, 28d = 106.
- **Tools:** `reach` (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach
  -dir . -files '<regexp>'`) and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir .
  -files a.go,b.go [-decls X,Y]`), run from the worktree root; never pipe
  them into `head`. Moves: `movex`, `keylit`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`.

## Goal

Role and ACL DDL (CREATE/ALTER/DROP ROLE, membership, GRANT/REVOKE, ALTER
DEFAULT PRIVILEGES, ALTER ... OWNER, REASSIGN OWNED and DROP OWNED) are
`Engine` methods that mix three things: reading the statement's authority,
deciding what changes, and publishing roles and catalog state under
`Engine.mu`. `role` takes the deciding part. The root keeps capture and
publication, as the `access` README says.

`access` stays what it is: pure checks over an immutable `access.View`,
with no callbacks (L4). It cannot host these handlers: they need notices,
the catalog stage and role publication. `role` is an upper feature (L12):
it may import `access`, `authorization`, `catalog`, `ddl`, `regobj`,
`session` and `analyze` types, and never builds a binder.

## Inventory (measured at `c154e14fb`)

25 files, about 4.0k lines; 0.5k clean. Clean means the declaration reaches
none of `Engine`, `StatementScope` and `CatalogStage`; `AuthorizationView`
holds `*Engine`, so code that takes it is dirty even when it only reads its
`access.View`.

| Group | Files (lines) | Owner after the split |
|---|---|---|
| View capture and the public facade | `authorization.go` (341: `AuthorizationView`, `authorizationLocked`, `statementAuthorizationLocked`, `operationAuthorization`, `SetObjectACL`, `SetRoleReferences`), `transaction_roles.go` (75: `installOperationRolesLocked`, `operationRolesLocked`, `roleRetirementPreflightLocked`), `role_store.go` (100, clean, `roleStore`) | root |
| aclitem I/O | `aclitem_input.go` (62), `aclitem_output.go` (117), `acl_empty.go` (30), `aclitem_dump.go` (149; `preflightACLItemValue` clean) | `regobj` (done in 21b; `regrole_input.go` too); the dump preflight `preflightACLItemDump` with `control` |
| Restore remap | `aclitem_remap.go` (70), `principal_remap.go` (303) | `catalog` 5i (descriptor switch), `control` (30) |
| Role DDL | `role_attributes.go` (105), `roles.go` (354), `role_membership.go` (30), `role_password.go` (16), `role_connection_limit.go` (25) | `role` |
| GRANT/REVOKE | `role_grant.go` (464), `role_grant_objects.go` (135), `role_grant_routines.go` (126), `database_grant.go` (134) | `role` |
| ALTER DEFAULT PRIVILEGES | `default_privileges_execute.go` (182), `default_privileges_catalog.go` (96, `CatalogStage` methods: `creationACL`, `creationTypeACL`, ...) | `role` (decisions); the stage methods stay in the root (`ddl.Host.CreationACL` uses them) |
| Ownership statements | `ownership.go` (416), `owned_roles.go` (324), `owned_roles_cleanup.go` (276) | `role`; the pure helpers to `access` (B6) |
| Other | `database_acl.go` (25, `checkDatabaseConnectLocked`, session admission), `parameter_permission.go` (55, SET permission: `parameterSetAllowed`, `settingAccess`) | root |

**Handler sizes** (`Engine` methods unless noted):
- Role DDL: `executeRoleAttributes` (72), `validateRoleAttributesLocked`
  (19), `executeRole` (110), `dropRolesLocked` (88), `initialRole` (26),
  `membershipLocked` (20), `roleSessionCountLocked`.
- GRANT/REVOKE: `objectGrantLocked` (326), `objectGrantTargets` (86),
  `resolveGrantObject` (45), `resolveGrantRoutine` (93),
  `executeDatabaseGrant` (96); free functions `changeNonRelationGrant` (72)
  and `grantRoutineArgumentDisplay`.
- Default privileges: `alterDefaultPrivilegesLocked` (168),
  `stageDefaultChangesLocked` (16), `defaultACLChangeLocked` (12),
  `creationACLChangesLocked` (17).
- Ownership: `executeOwnership` (87), `executeRelationOwnership` (82),
  `executeDatabaseOwnership` (49), `ownershipStage` (5), `publishOwnership`
  (18), `executeOwned` (221); free functions `stageOwnership` (121),
  `stageOwnedCleanup` (135), `ownedSources` (32).

**Clean declarations** (about 0.5k lines):
- aclitem I/O (`capturedACLItemInput`, `capturedACLItemOutput`,
  `aclItemContextFormatter`, `captureACLItemOutputValue`,
  `containsEmptyACL`, `preflightACLItemValue`; about 280 lines): `regobj`
  (done in 21b: `ACLItemInput`, `ACLItemOutput`, `ACLItemContextFormatter`,
  `CaptureACLItemOutputValue`, `ContainsEmptyACL`, `PreflightACLItemValue`).
- `roleStore` and its methods (77): root (role publication).
- Pure ownership helpers (`ownershipRoot`, `ownedRoots`,
  `ownershipRelationLocks`, `ownedMembershipCandidate`, `ownedPolicies`;
  about 140): `access` (B6, done: `access.OwnershipRoot` and the others).
- Grant records: `columnGrantRequest`, `objectGrantChange`, `grantWarning`
  (33): `role`.
- Role diagnostics: `roleDiagnostic`, `roleConnectionLimitError`,
  `roleLoginError`, `emitPasswordNotice` (20): `role`.
- Generic relation diagnostics filed in `roles.go`:
  `relationDiagnosticNameAt`, `relationVisibleAt` (17). The view renderer
  uses them, so `stored` takes them in 22b (linear item 68).

**Dirty only through a borrowed root type** (cut targets for 28a):
`ownedSources`, `changeNonRelationGrant` and
`grantRoutineArgumentDisplay` take `AuthorizationView` but read only its
view; `stageOwnership` and `stageOwnedCleanup` take `*CatalogStage`
(`stageOwnedCleanup` also sets `CatalogStage.roleCandidate`).

**What `objectGrantLocked` does**, in order: request validation over the
view (grantor, unavailable kinds, grantees, privilege masks, GRANT OPTION
for PUBLIC; about 80 lines); change construction per target (new ACLs,
column masks, release traversal, 0LP01 warnings; about 210 lines, with
`revisionLocked` calls); installation (namespace generation, role record
advance, journal or published state, `installOperationRolesLocked`; about
35 lines).

## Part A

### 28a — In place (linear item 103)

1. Re-measure the inventory (`reach` with
   `-files '^(acl_empty|aclitem_.*|authorization|database_acl|database_grant|default_privileges_.*|ownership|owned_roles.*|principal_remap|role_.*|roles|transaction_roles|parameter_permission)\.go$'`).
2. **Renames** (one commit, file moves and splits only):
   - The generic relation diagnostics leave `roles.go` for
     `relation_diagnostics.go`: `relationDiagnosticName` (DROP SCHEMA uses
     it), and `relationDiagnosticNameAt` and `relationVisibleAt` unless
     22b has already moved them to `stored`. `dependencyDescription` stays
     (only DROP ROLE uses it).
   - `parameter_permission.go` becomes `setting_permission.go` (it decides
     SET permission).
   - `initializeDatabase` and `retainDatabaseLocked` leave
     `database_grant.go` for `database.go`.
3. **Cuts** (pattern 6; one commit each, behavior unchanged):
   - `ownedSources`, `changeNonRelationGrant`, `grantRoutineArgumentDisplay`
     take `access.View` and an `access.Environment` instead of
     `AuthorizationView`.
   - `stageOwnership` and `stageOwnedCleanup` take a stage interface
     (`ddl.Stage` if 20a has landed, otherwise a local interface with the
     methods they call) plus a setter for the role candidate, instead of
     `*CatalogStage`.
   - Split `objectGrantLocked` in place: `objectGrantRequest` (validation),
     `objectGrantPlan` (change construction; revisions come from an
     allocator function, warnings are returned, not emitted) and the root
     installation. Keep the warning order and every message.
   - The same split for `alterDefaultPrivilegesLocked` (decide the default
     ACL changes; the root allocates and installs) and `executeOwned`
     (plan the reassign and drop targets; the root takes the lock waits,
     the stage and publication).
4. Re-run `reach`. The clean set should now include the request and plan
   functions; list it in this file's Status.

### 28b — Create `role` and move the clean set (linear item 104)

1. Confirm that 21b (aclitem I/O in `regobj`) and B6 (ownership helpers in
   `access`) have landed; if not, stop and report.
2. Create `internal/engine/role` and move the clean set: grant records,
   role diagnostics and errors, the password notice, and the request and
   plan functions that 28a made clean. Export only what the root calls.
3. Give the package its own `errors.go` (`sqlError` copy), as the other
   subpackages do.
4. Check imports: nothing above L12; no `analyze` binder, no root.

## Part B

### 28c — Refine the host (docs only; linear item 105)

Measure the remaining role handlers and write the host and move set into
this file. Start from this sketch.

**Host sketch (`role.Host`).** It embeds `ddl.Host` for the catalog stage,
authority views, OIDs, revisions and notices, plus:
- **Roles:** `RoleRecord(Authority) access.RoleRecord` (the captured role
  state), `PrincipalCandidate(...)` (`roleStore.principalCandidateLocked`),
  `ReferencingFeatures(principal)` (`roleStore.referencingFeatures`).
- **Sessions:** `SessionCount(principal) int` (connection limits;
  `roleSessionCountLocked`).
- **Grant targets:** `ResolveGrantObject`, `ResolveGrantRoutine` (they use
  the search path and routine lookup, which stay in the root).
- **Dependents:** the DROP OWNED and REASSIGN OWNED stage helpers
  (`stageOwnedSequences`, `stageExpressionDependents`,
  `reownCatalogExpressions`, `removeObjectLocked`): through the `ddl`
  removal interface of 24c if it exists, otherwise as host methods.

**Decisions that move (28d):** role attribute validation, DROP ROLE
dependency ordering and its messages, membership rules, GRANT/REVOKE
request validation and change construction, default ACL change
construction, ALTER OWNER rules per object kind, REASSIGN/DROP OWNED
planning.

**Stays in the root:** `Engine.mu` and its unlock/relock windows, lock
waits (`acquireSequence`, `replacementMu`), `AuthorizationView` capture,
`roleStore` publication and `installOperationRolesLocked`,
`SetObjectACL`/`SetRoleReferences`, the catalog journal and published state
updates, and dispatch in `execute.go`.

Write the method list with signatures, which handler uses each, the move
set and the commit sequence, then wait for approval.

### 28d — Move the handler decisions (linear item 106)

Follow 28c: seam (host on the root `ddlHost` or a role adapter embedding
it; seam tests first), port each handler in place over `role.Host`, move
with `movex`, unexport, docs.

## Invariants

- Role publication stays atomic with the catalog change it belongs to
  (`roleCandidate` joins the same publication; `installOperationRolesLocked`
  keeps its transaction rules).
- GRANT/REVOKE warnings (0LP01) keep their order and text; ACL item output
  and `pg_*` ACL columns stay byte-identical.
- Authority stays a per-call value; checks keep passing the authority as
  the `access.Environment`.
- Lock order is unchanged: role DDL that waits releases `Engine.mu` first,
  as today.
- Dump and Restore of roles, ACLs and default privileges stay
  byte-identical.

## Targeted tests

- Tests follow the code (ENGINE-SPLIT §4): unit tests of moved pure code
  move into `role`; engine-fixture tests are rewritten to the new names.
  Never modify `reference/` or `smoke/`.
- Engine units: grep `*_test.go` for `objectGrant`, `defaultACL`,
  `stageOwnership`, `stageOwnedCleanup`, `executeOwned`, `roleDiagnostic`,
  `validateRoleAttributes`, `relationDiagnosticName`.
- Reference selections (`make check RUN='...'`, then with `REFERENCE=1`):
  `^TestReferenceRole` (164 tests, including the default-privilege ones),
  `^TestReferenceObjectGrant` (113), `Ownership|Owned|Reassign`,
  `^TestReferenceSchema(Owner|ACL|Authorization)` and the Dump/Restore
  round trips (`DumpRound`, `Restore`).
- `RACE=1` for the connection-limit and role-switch units (sessions).

## Documentation

- Create `internal/engine/role/README.md` (28b): what it decides, what the
  root publishes, the authority rule, `## Testing`.
- Update the `access` README ("What stays in the engine") in 28b/28d.
- `AGENTS.md`: add `role` to the brace list. The file must not grow.

## Done when

Part A: the grant, default-privilege and ownership plans are clean and live
in `role`. Part B: the role DDL, GRANT/REVOKE, default-privilege and
ownership decisions live there too; capture and publication stay in the
root. The checks in §8.3 pass.

## Status

- [ ] 28a — In place (renames, cuts, grant/default/owned plans split)
- [ ] 28b — `role` created and clean set moved
- [ ] 28c — Host refined (docs only)
- [ ] 28d — Handler decisions moved

Outstanding aliases: none yet.

### Notes for later steps

- `resolveGrantTablespace` (`tablespace_catalog.go`) and GRANT ON
  PARAMETER (`parameter_catalog.go`) are grant target resolution; take them
  in 28c if they fit the host, or leave them in the root.
- `fdw_grant.go` (GRANT on foreign data wrappers and servers) belongs to
  `fdw` (step 26d), not here.
- `principal_remap.go` is Restore: its descriptor switch goes to `catalog`
  (5i) and the rest to `control` (step 30).
