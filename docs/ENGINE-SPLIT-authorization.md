# Engine split: `internal/engine/authorization` (and `access`)

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-authorization.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2.5 (last paragraph), §3
  (L1 `authorization`) and the `access` row in §6.
- **Needs:** Part A needs nothing. Part B needs step 5 (`catalog`) through 5e.
- **Effort:** high for Part A; xhigh for B0; high for B1–B4.
- **Session size:** Part A takes one session. Part B takes one sub-step per
  session.

## Background

`authorization` is already a proper leaf: role descriptors, membership
graphs, predefined roles, ACL grant and revoke algorithms, default privileges
and passwords. It imports `parser`, `sqlstate` and the standard library, never
the engine. Catalog descriptors already hold its `ACLValue` and
`PrincipalRef`, so `catalog` can import it (§3).

The shallow part is the engine side: about 6k lines of enforcement code
(`authorization.go`, `acl*`, `role*`, `ownership.go`,
`default_privileges*`, `AuthorizationView`, policy selection). At
`a2252c0b1`, only about 270 of its lines were free of catalog types. The rest
reads descriptors, `CatalogRead`, `CatalogStage` and the `Engine`. B0
re-measured the area: of about 7.7k lines, only about 750 are enforcement.
The rest is orchestration, ACL SQL functions, system-relation projections,
Dump/Restore code and policy DDL (see "B0 outcome" below).

This enforcement code cannot move into `authorization`. `catalog` imports
`authorization`, so if enforcement in `authorization` imported `catalog`, the
two packages would form an import cycle. It moves to a new package, `access`,
above `catalog` (Part B). Part A only tidies the leaf.

## Part A — Consolidate the leaf

### A1 — Move the pure helpers and rewrite the README

- Move into `authorization` the engine helpers that are pure algorithms over
  authorization values. Candidates measured at `a2252c0b1`:
  - privilege name tables and comparison (`aclPrivilegeNames`,
    `aclPrivilegeEqual`);
  - default masks (`aclDefaultMasks`);
  - `containsEmptyACL` and `overlayDefaultACLs`;
  - `validateInitialRole` and `ownershipRoleOperand`.

  Re-measure, and move only what is an authorization algorithm.
- Do not move the ACL expression nodes (`aclexplode`, `aclitem` input and
  output, `makeaclitem`, ...). They are functions; they belong to step 3
  Tier C.
- Keep `authorization.Domain` separate from `ids.Domain`. Restore stages
  principals in their own domain (`principal_remap.go` with
  `Root.Reprovenance`), which a shared token would break.
- Rewrite `internal/engine/authorization/README.md` in plain language: purpose,
  API groups (identity and domain, roles and membership, ACL algorithms,
  defaults, passwords), invariants, what stays in the engine, and the plan for
  `access`. Keep its `## Testing` section accurate. It is dense today; aim for
  shorter, not longer.

## Part B — Engine-side enforcement into `access`

### B0 — Refine (no code changes)

Done; see "B0 outcome" in the Status section. B1–B4 below are the refined
plan.

### Target shape

- **`access.View`** is the immutable captured pair: the catalog owner
  (`*catalog.Owner`, which replaces the `engine` back-pointer), the
  `catalog.Read` and its paired read, the role publication record
  (`RoleRecord`, today `roleCatalog`), the `authorization.Read`, the database
  predecessor, the effective and session `RoleState`, the session
  authorization revision and `missingCurrent`. Its methods are `Effective`,
  `Check`, `CanUse`, `CheckCreate`, `Recapture` (today `recaptureCatalog`),
  `Stamp`, `Roles` and `Ref(address)` (today
  `engine.catalog.Domain.Ref(epoch, address)`).
- **`access.Environment`** is a narrow capability that the engine implements,
  as in §3 rule 5. It has two methods: `TemporaryNamespace()`, which returns
  the session's own TEMP namespace, and `ReferencePath()`, which returns the
  search path used for type diagnostic names. Checks that need them take an
  `Environment` parameter. Both are read lazily at call time, as today:
  `ReferencePath` reads live session settings under `sessionSettings.mu`.
  Never capture them as values, and never add methods that turn the
  interface into a service locator.
- **The engine's `AuthorizationView` stays** as statement-level authority. It
  embeds `access.View` and keeps `session`, `settings`, `definition` and the
  path inputs (`pathPrefix`, `namespaceUser`, `stagedTemporary`,
  `materializedPopulation`). Its `Check`, `CanUse`, `CheckCreate` and
  `Effective` become one-line entry points that pass the view itself as the
  `Environment`. They are permanent entry points, not aliases, so the call
  sites (103 `Check`, 33 `CheckCreate`, 24 `CanUse` and 23 `Effective` in
  production) do not change.
- **`access` also holds:**
  - the ownership checks (`ownershipSuccessor`, `ownershipEligibility`,
    `ownershipDatabaseCreate`), plus `schemaOwner` and
    `ownershipRoleOperand`, which share the `current_user`/`session_user`
    string protocol;
  - RLS policy selection (`rlsDisposition`, `rowSecurityApplies`,
    `rowSecurityDecision`, `rowSecurityGroups`, `rowSecurityPrincipal`,
    `rowSecurityExpressionError`, `rowSecurityProposal`);
  - role dependency scans (`roleDependency`, `descriptorRoleDependencies`,
    `aclRoleDependencies`, `recordRoleDependency`,
    `defaultRoleDependencies`);
  - the supporting pure helpers (`descriptorACL`, `classPrivileges`,
    `validateACL`, `databaseRights`, `predefinedPrivileges`, `rawObjectACL`,
    `objectACLClass`).
- **Stays in the engine:**
  - view capture (`authorizationLocked`, `statementAuthorizationLocked` with
    its Restore elevation, `Operation.Authorization`);
  - role publication (`roleStore`, B1) and the `catalogAuthority` fences;
  - role DDL, GRANT and REVOKE resolution and publication, ALTER DEFAULT
    PRIVILEGES and the ownership statements;
  - `SetObjectACL` and `SetRoleReferences`;
  - search path resolution (`schema_path.go`) and `temporaryNamespace`;
  - RLS configuration (`configureRowSecurityLocked`,
    `rowSecurityInputLocked`) and the RLS DDL flags;
  - the definition reader;
  - the binding walkers (`checkBinding`, `checkQuerySources`, `checkColumns`,
    `checkViewWrite`, `checkViewRowMarks*`). They read `binding`,
    `queryLeaf`, `queryRowMark` and `viewDefinition`, and lock modes (`lock`
    is not an `access` import), so they wait for the `plan`/`exec` wave and
    call `access` for each object.
- **Imports:** `access` imports `catalog`, `authorization`, `ids`, `sqltype`,
  `parser`, `sqlstate` and the standard library, and `expr` only if policy
  selection needs it. It keeps its own `sqlError` and identifier quoting, as
  `authorization`, `functions` and `lock` do.

### B1 — Role state ownership in place

- Group `authorizationDomain`, `aclAuthority`, `roles` (the current
  `*roleCatalog`), `nextPrincipal` and `roleReferences` into one engine-local
  `roleStore`. `Engine` holds it by value and never replaces it whole,
  because `catalog.Owner.Principals`, `catalog.Owner.ACLAuthority` and
  `Root.Reprovenance` keep pointers into it. No mutex: `Engine.mu` guards it,
  and mutating methods carry the `Locked` suffix.
- Move onto the store:
  - `initializeRoles` and `nextRolePrincipalLocked`;
  - `advanceRoleRevision`;
  - the registry update in `SetRoleReferences`;
  - the role-reference scan shared by `dropRolesLocked` and
    `roleRetirementPreflightLocked`;
  - `roleRead`, which takes the catalog owner it checks against.
- Keep on `Engine`: `operationRolesLocked`, `sessionRolesLocked`,
  `installOperationRolesLocked` and `touchOperationIdentityLocked`, which read
  session and transaction state, and every publication and Restore path.
- `importTargets` is not authorization state. All of its roughly 25 uses are
  Restore staging, most of them nil checks meaning "this is a Restore
  stage". It stays on `Engine` for the `control` wave (see the notes below).
- Rewrite the selectors with a `go/types` tool and compare a before/after
  per-field census, as 6a did.

### B2 — Port the checks in place

1. Introduce an engine-local `accessView` with the pure fields of
   *Target shape*. `AuthorizationView` embeds it. Replace
   `engine.catalog.Domain.Ref` with `Ref`, and `engine.catalog.owner()` and
   `&engine.authorizationDomain` with the captured owner. Keep the
   statement-level identity checks (`auth.engine != e`,
   `auth.session != op.session`) on the wrapper.
2. Add the `accessEnvironment` capability. `Effective` and `Check` reach the
   TEMP namespace and the search path only through it.
3. Turn these into functions over `accessView`, or over `(catalog.Read,
   authorization.Read, RoleState)`:
   - the ownership checks and `schemaOwner`;
   - the role dependency scans;
   - RLS selection. `rowSecurityDecision` tests the `Using` and `Check`
     payloads for nil instead of calling `policyExpressionOf`.
     `rowSecurityPrincipal` takes the view-owner and invoker pairs that the
     engine collects from `binding.viewWrite`.
4. Give `checkColumns` a core over `(ObjectRef, *catalog.Relation)`. The
   binding walkers stay.
5. Pass the type diagnostic name helpers (`typeDiagnosticNameAt`,
   `dependencyIdentifier`) as values, or copy their quoting rule.

### B3 — Create `access` and move

- Move the *Target shape* set into `internal/engine/access`. Leave aliases in
  the engine: `AccessRequirement`, `AuthorizationStamp`, `roleCatalog`,
  `accessView`, `accessEnvironment`, `rlsDisposition` and its constants.
- Unit tests for moved code move with it.
- Check the imports with `go list` (§8.2).

### B4 — Remove the aliases

Remove them with a tool aware of `go/types`. This is a broad rewrite: do not
run it in parallel with 6c or with another broad rewrite (§8).

### B5 — Move the binding walkers (after step 12d)

Added when the later waves were planned. The binding walkers stayed in the
engine because they read `binding`, `queryLeaf`, `queryRowMark` and lock
modes: `AuthorizationView.checkBinding`, `checkQuerySources`,
`checkColumns`, `checkViewWrite`, `checkViewRowMarks` and
`checkViewRowMarksExpanded` (all in `authorization.go`). Once step 12d has
moved `binding` into `analyze` and step 10c the row marks into `plan`:

1. Re-measure the walkers' reach with the appendix tool.
2. Move them next to the binding they walk, in `analyze`, as functions that
   take the binding and an `access.View` (or the root's `AuthorizationView`
   entry points), calling `access` for each object. `access` must not import
   `analyze` or `plan`.
3. Keep privilege decisions, error order and messages identical; run the
   reference selections listed under "Targeted tests" with `REFERENCE=1`.

## Invariants

- Privilege decisions, SQLSTATEs, messages, notice order and grantor
  selection stay identical.
- Absent and empty ACLs stay distinct. Default privileges stay
  creation-only.
- Captured authorization stays paired with its exact `catalog.Read`. There is
  no cached ACL decision.
- Zero, foreign and stale principals are rejected before any superuser
  shortcut.
- A view still rejects foreign engines and stale catalogs. Once the `engine`
  pointer is gone, `access` compares catalog owners instead.
- The TEMP namespace and the search path are read through `Environment` at
  call time, never captured early. `Environment` stays two methods wide.
- Role publication and principal allocation keep their order, their revision
  headroom and their exhaustion messages.

## Targeted tests

- The `authorization` package tests and the engine selectors listed in its
  README, with `RACE=1` for the concurrent ones (all sub-steps).
- B1: also the Restore, Dump and role DDL unit tests that touch principal
  allocation (grep `nextPrincipal` and `roleReferences` in `*_test.go`).
- B2 and B3: reference selections for role membership, role switching,
  predefined roles, default privileges, object GRANT/REVOKE, RLS and
  ownership, run with `REFERENCE=1` and in quick mode. Start from the
  `authorization` README set, plus `TestReferenceAccessRoleRLS.*`,
  `TestReferenceForeignActionsAuthorization.*`,
  `TestReferenceSchemaAuthorization` and
  `TestReferenceTypedTableAuthorization`. Never edit `reference/`.

## Documentation

- Part A: the `authorization` README rewrite above.
- Part B: B1 documents `roleStore` in its doc comment, as `catalogStore` and
  `storageStore` do. B3 creates `internal/engine/access/README.md`
  (the *Target shape*, the `Environment` contract, what stays in the engine,
  and `## Testing`), and changes the "What stays in the engine" and
  "Enforcement" lines of the `authorization` README to point at it.
- Design notes and docs: `PG18-ARCH-AUTHORIZATION.md` and
  `docs/AUTHORIZATION.md` (it links the internal package), then
  `PG18-ARCH-SESSION.md` and `PG18-ARCH-ROUTINE-EXECUTION.md` where they name
  `AuthorizationView`, `roleCatalog` or moved files. Grep for more.
- `AGENTS.md`: after Part B, add `access` to the shared subpackage bullet and
  drop "authorization" from the `internal/engine` bullet. Part A should not
  need any `AGENTS.md` change.

## Done when

- **Part A:** the pure helpers live in `authorization` and the README
  describes the package as it is.
- **Part B:** `access` holds the *Target shape* set and imports nothing above
  `catalog`. The engine keeps only orchestration, the binding walkers and the
  entry points. `Engine` holds one `roleStore` instead of five fields. The
  checks in §8.3 pass.

## Status

- [x] A1 — Pure helpers moved, README rewritten (`e932fc381`, `96ee8d96b`,
  docs in the following commit)
- [x] B0 — `access` refined and approved (docs only, the commit that records
  this status)
- [x] B1 — `roleStore` role state ownership (`e4174a713`, `e91c6d175`,
  then the commit that records this status) *(racecheck skipped at user
  request; targeted RACE=1 runs passed)*
- [x] B2 — `accessView` and `accessEnvironment`; checks ported in place
  (`8e41e7091`, `a0fa8edec`, `7d5e526b8`, `809a3ed62`, then the commit that
  records this status) *(racecheck skipped at user request; targeted RACE=1
  runs passed)*
- [x] B3 — `access` created and code moved (`8e529feda`, `0d549283e`,
  `9cfb1306b`, `a168628d4`, then the commit that records this status)
  *(racecheck skipped at user request; targeted RACE=1 runs passed)*
- [x] B4 — Aliases removed (`68be6bf5a`, `4130fa669`, then the commit
  that records this status) *(racecheck skipped at user request; targeted
  RACE=1 runs passed)*
- [x] Names cleanup — diagnostic name helpers moved to `catalog`, the
  `access` copy deleted (`9a73852a6`, `416945ecb`, then the commit that
  records this status) *(racecheck skipped at user request)*
- [ ] B5 — Binding walkers moved next to `binding` (needs step 12d)

No aliases remain: B4 removed the nine B3 aliases and deleted
`internal/engine/access_aliases.go`. Part A, B1 and B2 added none.

A1 outcome. The re-measure (`go/types`, free declarations) found that most
listed candidates are not authorization algorithms, and found others that are:

- Moved: `validateInitialRole` (`ValidateInitialRole`); the `pg_has_role`
  check `rolePrivilege`, `parseRolePrivileges` and `accessRoleCheck`
  (`RolePrivileges`, `ParseRolePrivileges`, `HasRole`); `pgClassACLMask`
  (`ACLItemBits`); and the database baseline `databasePrivileges`,
  `databasePublicPrivileges` and `databaseACL` (`DatabasePrivileges`,
  `DatabasePublicPrivileges`, `MaterializeDatabaseACL`).
- Not moved: see the notes below.
- Tests moved: `TestAccessInquiryPrivileges` became `TestRolePrivilegesParse`;
  `TestObjectGrantParameterACLTransport` and the unknown-bit check of
  `TestPgClassACLTranslation` became `TestACLItemBits`. Adapted:
  `TestAccessInquiryRoleOIDs` and the `TestDatabase*` ACL tests.

Notes for later steps:

- **Step 3 Tier C:** `aclPrivilegeNames`, `aclPrivilegeEqual`,
  `aclPrivilegeLabels` and `aclDefaultMasks` serve only the ACL SQL function
  nodes and use raw `aclitem` bits. Move them with those nodes. `functions`
  cannot import `authorization`, so `aclDefaultMasks` keeps its own copy of the
  default masks in `aclitem` layout. (Done in the step 3 Tier C ACL part:
  they are in `functions/acl_items.go`.)
- **Step 5:** `overlayDefaultACLs`, `cloneDefaultACLs` and the default-ACL
  record keys are catalog storage keyed by `ObjectAddress`.
- **Dump/deparse owner:** `containsEmptyACL` walks `sqltype` values; it is not
  an authorization algorithm.
- **B0:** `ownershipRoleOperand` adapts parser role specs for the engine's
  `schemaOwner`; move it with that function into `access`.
  `capturedRegroleInput` belongs to `regrole` input, not `access`. The
  `pg_has_role` node now only gathers arguments and authority.

B0 outcome. Measured on `master` at `6421cbaca` with a `go/types`
declaration census and file sums:

- **Size.** About 50 production files with authorization-like names hold
  7,763 lines. The *Target shape* set is about 740 declaration lines:
  - checks, about 480;
  - ownership, about 60;
  - RLS selection, about 100;
  - role dependency scans, about 100.

  The rest splits as follows:

  | Code | Lines | Destination |
  |---|---|---|
  | View capture, role DDL, GRANT/REVOKE, ALTER DEFAULT PRIVILEGES, ownership statements, publication and fences | about 2.5k | Stays in the engine (orchestration) |
  | Binding walkers | about 320 | Stays in the engine until `plan`/`exec` |
  | ACL SQL function nodes | about 1,460 | `functions` Tier C |
  | `information_schema` and `pg_*` projections | about 440 | `pgcatalog` |
  | Dump, Restore and remap | about 1,150 | `control` |
  | Policy DDL, binding and validation | about 650 | Policy feature package |
  | Code that is not authorization despite its name | about 210 | Unaffected |
- **Default privileges.** Resolution already moved to `catalog` in step 5
  (`catalog.CreationDefaultACL`, `ValidateDefaultACLs`), so `access` has no
  default-privilege resolution of its own.
- **References into the set from the rest of the engine.** `AuthorizationView`
  appears 407 times in 181 production files and 66 times in tests, and 328
  functions take or return it. Only `Engine.Roles` and `ResolveLogin` are used
  outside `internal/engine` (the driver and `cmd/stuntgres`).
- **References out of the set.** The census found only the blockers that B2
  removes:
  - `AuthorizationView` fields;
  - `sqlError`;
  - `Engine.referencePath`, `ownTemporaryNamespace` and `catalogStore.owner`;
  - `typeDiagnosticNameAt` and `dependencyIdentifier`;
  - `policyExpressionOf`;
  - the view-write types used by `rowSecurityPrincipal`.
- **`AuthorizationView.engine`.** It has 101 uses:
  - 22 build `ObjectRef`s through `catalog.Domain.Ref`;
  - 10 call `referencePath`;
  - 7 are identity checks;
  - the rest pass the engine on (17 to `capturedACLItemOutput`) or reach
    `catalog.owner()`, `authorizationDomain` and `roleRead`.

  All of them except `referencePath` and the pass-throughs reduce to the
  `catalog.Owner` record of 5b.
- **`AuthorizationView.session`.** It has about 100 uses (token, transactions,
  database name, TEMP namespace, sequence path), all at engine call sites.
  Inside the checks, only the TEMP namespace (`Effective`) and the search path
  (`Check`, for type diagnostic names) depend on the session. That is why
  `Environment` has exactly two methods.

Notes for later steps (B0):

- **Step 3 Tier C:** besides the A1 list, `acl_values.go`, `object_acl.go`
  (except `rawObjectACL` and `objectACLClass`, which go to `access`),
  `access_inquiry.go` (`pg_has_role`, `has_*_privilege`,
  `row_security_active`), `acl_explode.go`, and the aclitem and regrole input
  and output adapters. `row_security_active` will call
  `access.RowSecurityApplies`. (Done in the step 3 Tier C ACL part, except
  the aclitem and regrole input and output adapters, which are statement
  presentation state and stay in the engine; see the functions prompt file.)
- **`pgcatalog`:** `table_privileges.go`, `data_type_privileges.go`,
  `pg_roles.go` and `pg_parameter_acl.go`.
- **`control`:** `principal_remap.go`, `extension_owners.go`,
  `inheritance_owner_restore.go`, `aclitem_dump.go`, `aclitem_remap.go`,
  `policy_dump.go`, `policy_remap.go` and `rls_base_dump.go`. `importTargets`
  (Restore staging) moves with Restore. Its many nil checks should go behind
  one "restore stage" accessor at that point.
- **Policy feature package:** `policy.go`, `policy_payload.go`,
  `policy_ddl.go`, `policy_dependencies.go`, `policy_expression.go` and
  `policy_validation.go`. `validatePolicies` and `policyDepends` are pure
  catalog invariants and could become `catalog` functions first.
- **Catalog:** `CatalogStage.creationACL`, `creationTypeACL`,
  `defaultPreflight` and `removeSchemaDefaults` touch only the embedded
  `catalog.Candidate`, so they can become `Candidate` methods.
- **Misnamed files:** `identity_owner.go`, `foreign_scan_owner.go`,
  `query_authority.go` and `inheritance_dml_authority.go` hold transaction,
  foreign-scan and statement plumbing, not authorization. Leave them out of
  `access`.
- **B2:** `descriptorReferences` has no production callers, only tests.
  Delete it, or move it with its test.

B1 results:

- **Store.** `roleStore` (`internal/engine/role_store.go`) holds `domain`
  (was `Engine.authorizationDomain`), `aclAuthority`, `published` (was
  `Engine.roles`, the committed `*roleCatalog`), `nextPrincipal` and
  `references` (was `roleReferences`). `Engine` holds it by value as
  `roles`; `newEngine` points `catalog.Owner.Principals` and
  `ACLAuthority` into it. `importTargets` stays on `Engine`.
- **Rewrite.** A go/types selector rewriter (the 6a tool, re-parametrized)
  changed all 1,028 field uses (755 `roles`, 176 `authorizationDomain`,
  65 `nextPrincipal`, 26 `aclAuthority`, 6 `roleReferences`) in 269
  production and in-package test files; the per-field census matched
  before and after, with no composite-literal keys. A second pass rewrote
  all 144 `Engine.roleRead` calls to `X.roles.read(X.catalog.owner(), ...)`
  (census 144 before and after).
- **Methods moved.** `initializeRoles` → `initialize`;
  `nextRolePrincipalLocked` → `principalCandidateLocked`; `roleRead` →
  `read`, which takes the catalog owner it checks against; the
  `SetRoleReferences` registry update → `setReferencesLocked` (descriptor
  validation of the payload stays in `SetRoleReferences`); the feature scan
  shared by `dropRolesLocked` and `roleRetirementPreflightLocked` →
  `referencingFeatures` (DROP ROLE sorts its dependencies afterward, so
  detail order is unchanged).
- **Deviation.** `advanceRoleRevision` became the `roleCatalog.advance`
  method, not a store method: `role_grant.go` and
  `default_privileges_execute.go` advance transaction-overlay records
  (`v.roleRecord`), not the store's. It moves with `roleCatalog` in B3.
- **Kept on `Engine`.** `Roles`, `ResolveLogin`, `operationRolesLocked`,
  `sessionRolesLocked`, `installOperationRolesLocked`,
  `touchOperationIdentityLocked`, `restoreStage`, `SetObjectACL`, the
  publication paths in `catalog.go` and `transaction_publication.go`, and
  the principal advance after CREATE ROLE publication in
  `role_attributes.go` (inline, like 6a's counters).
  `CatalogStage.roleRead` stays as a thin wrapper choosing the candidate.
- **Tests.** Adapted mechanically by the rewrite (seven call sites of
  `nextRolePrincipalLocked` and `advanceRoleRevision` by hand); none moved
  or deleted. New seam tests in `role_store_test.go`:
  `TestRoleStoreReadRequiresOwnCatalogAndRoot` and
  `TestRoleStoreSetReferencesFailuresLeaveStateUnchanged` (both fail
  under mutation of the owner check and the cancellation check).
- **Verification.** `make build` and targeted `make check` selections
  (role DDL, membership, predefined roles, ownership, Restore, Dump, driver
  batch retirement, `pg_roles`/`pg_user`/`pg_group`/`pg_shdescription`,
  access inquiry, object grants, default privileges and the new seam tests)
  passed after each commit, and a `RACE=1` run of the authorization README
  concurrency set, the driver batch role tests and the seam tests passed.
  `quickcheck` passed. `REFERENCE=1` passed for the README role membership,
  role switch, predefined role, default privilege and
  `TestReferenceAccessRole.*` selections. The full `racecheck` was skipped
  at the user's request. `make bench`: 7,768 allocations before and after;
  2.41–4.01 ms/op before and 1.51–1.98 ms/op after on a machine shared with
  parallel sessions (noise, not a change; the <1 ms target is already
  missed on `master`). `make deps` lists only module paths; imports are
  unchanged.

Notes for later steps (B1):

- **B2/B3:** `roleStore.read` needs only the owner, the domain pointer and
  the record, so B3 can turn it into an `access` function over
  `(*catalog.Owner, *authorization.Domain, catalog.Read, *RoleRecord)`;
  the store keeps a one-line method.

B2 results:

- **Seam.** `access_view.go` holds `accessView`: the catalog owner
  (`*catalog.Owner`, new), `catalog`, `pairedCatalog`, `roleRecord`,
  `roles`, `databasePredecessor`, `state`, `sessionRoleState`, `revision`
  and `missingCurrent`. Its methods are `Effective`, `Check`, `CanUse`,
  `CheckCreate` and `CheckColumns` (the core of `checkColumns`), each
  taking an `accessEnvironment`, plus `Ref`, `Stamp`, `Roles` and
  `Recapture` (renamed from `recaptureCatalog`, 128 calls). `object`
  compares catalog owners instead of reaching the engine. `descriptorACL`,
  `classPrivileges` and `validateACL` moved into the same file.
  `accessEnvironment` has `TemporaryNamespace` and `ReferencePath`;
  `AuthorizationView` implements both and embeds `accessView`, keeping
  `engine`, `definition`, `settings`, the path inputs, `session` and
  `materializedPopulation`. Its `Check`, `CanUse`, `CheckCreate`,
  `Effective` and `checkColumns` are one-line entry points. `roleRead` is
  the body of `roleStore.read` over an explicit principal domain; the store
  method delegates with `&s.domain`.
- **Selectors.** A go/types rewrite changed the view-reached engine
  selectors: `X.engine.catalog.Domain.Ref(X.catalog.Epoch(), A)` to
  `X.Ref(A)` (17), `X.engine.catalog.Domain` to `X.owner.Domain` (4),
  `X.engine.catalog.owner()` to `X.owner` (3) and `X.engine.roles.domain`
  to `X.owner.Principals` (2). The other 69 `AuthorizationView.engine` uses
  are identity checks and pass-throughs (`capturedACLItemOutput`,
  `resolve*`, `referencePath`) and stay on the wrapper.
- **Ported.** `access_ownership.go`: `ownershipSuccessor` and `schemaOwner`
  over `accessView`, `ownershipDatabaseCreate` and `ownershipEligibility`
  over `(accessView, accessEnvironment, ...)`, and `ownershipRoleOperand`.
  `access_rls.go`: `rlsDisposition`, `rowSecurityApplies`,
  `rowSecurityDecision` and `rowSecurityGroups` over `authorization.Read`
  (`rowSecurityDecision` tests the payloads for nil), `rowSecurityProposal`,
  `rowSecurityExpressionError`, and `rowSecurityPrincipal` over the view
  layers' `{owner, invoker}` pairs, which the engine collects in
  `rowSecurityLayers`. The role dependency scans were already functions
  over `(catalog.Read, authorization.Read)` and did not change.
- **Deviations.** Item 5 is deferred at the user's request:
  `typeDiagnosticNameAt` and `dependencyIdentifier` stay engine helpers, and
  B3 copies them into `access`. The engine's `ownTemporaryNamespace` keeps
  its `AuthorizationView` parameter, and the checks use their own
  `ownsTemporaryNamespace(env, ns)`; taking the interface in the engine
  helper would box the view on every search-path entry.
- **Cost of the environment.** Passing `AuthorizationView` as the
  environment boxes the view once per entry-point call (`v escapes to
  heap`). The checks reuse that one value internally, so `CheckColumns`
  pays once per relation. A throwaway driver benchmark as a column-granted
  role measured 247 to 256 allocations (+3.6%) per SELECT and 114 to 117
  per INSERT, with latency within noise. If that matters, a later step can
  pass the environment lazily, so that only TEMP namespace and type-denial
  paths box the view.
- **Tests.** New seam tests in `access_view_test.go`:
  `TestAccessViewRejectsForeignOwner`,
  `TestAccessViewTemporaryNamespaceFromEnvironment` and
  `TestAccessViewTypeDenialQualifiesByReferencePath`. Adapted: the
  `AuthorizationView` literals in 10 test files (nested `accessView`, with
  `owner` wherever `engine` is set), `TestDatabaseTemporaryCapturedRights`
  (calls the core `ownTemporaryPrivileges`), `TestDataTypePrivilegesCapture`
  (a foreign owner replaces the foreign-engine mutation) and the RLS tests
  (pass the role read). `descriptorReferences` had no production callers
  and moved to `authorization_fixtures_test.go`. None deleted.
- **Verification.** `make build` and targeted `make check` selections
  (authorization, roles, ownership, RLS, policies, schemas, foreign
  objects, collations, conversions, operators, sequences, temporary
  namespaces, default privileges, privilege projections, rules, views and
  partitions) passed after each commit. `RACE=1` passed for the README
  concurrency set, the seam tests and the role store tests. `quickcheck`
  passed. The README reference set plus `TestReferenceAccessRole.*`,
  `TestReferenceForeignActionsAuthorization.*`,
  `TestReferenceSchemaAuthorization` and
  `TestReferenceTypedTableAuthorization` passed in quick mode. With
  `REFERENCE=1`, everything passed except
  `TestReferenceSchemaAuthorization` (memory database name) and
  `TestReferenceTypedTableAuthorization` (the pinned PostgreSQL rejects the
  fixture with "permission denied for type pair"). Both fail identically on
  `master` at `7df7894b8`, so they are PostgreSQL 18.6 oracle drift, not
  B2. The full `racecheck` was skipped at the user's request. `make bench`:
  1.75–1.93 ms/op and 7,768 allocations on `master`, 1.70–1.97 ms/op and
  7,769 allocations after (the extra one is the boxed environment of one
  check during Ping; the <1 ms target is already missed on `master`).
  `make deps` lists only module paths.

Notes for later steps (B2):

- **B3:** copy `typeDiagnosticNameAt` and `dependencyIdentifier` (with
  `quoteIdentifier`) into `access`, next to its own `sqlError`. The
  checks also call `predefinedPrivileges` (`predefined_roles.go`), which
  moves with them.
- **B3:** the engine reads the `accessView` fields about 2,000 times
  (`catalog` about 1,360, `state` about 330, `roles` about 240,
  `roleRecord` about 55). Once `access.View` lives in another package,
  those reads need exported fields or accessors, and `roleCatalog`'s
  `root` and `revision` likewise. That is a broad rewrite: refine it at the
  start of B3, and do not run it in parallel with another broad rewrite.
- **B3:** the move set is the files `access_view.go`, `access_ownership.go`
  and `access_rls.go`, plus `roleRead`, `roleCatalog` and the pure helpers
  listed in *Target shape* that are still in other files (`databaseRights`,
  `predefinedPrivileges`, `rawObjectACL`, `objectACLClass`, the role
  dependency scans).

B3 results:

- **Field access.** The engine reads and writes the view's fields directly:
  besides about 2,300 reads, production code re-targets copies about 95
  times (`auth.catalog = view` 54, `auth.state.CurrentUser = x` 39). So the
  fields are exported rather than wrapped in accessors, as catalog
  descriptors were in 5d (§7.3): `Owner`, `Catalog`, `PairedCatalog`,
  `RoleRecord`, `Roles` (replacing the `Roles()` method, which had three
  test callers), `DatabasePredecessor`, `State`, `SessionRoleState`,
  `Revision` and `MissingCurrent`. A view is a value, so writes touch only a
  copy; the captured roots stay immutable.
- **Rewrites.** A go/types tool renamed by object identity and compared a
  reference census of every object before and after (289,250 objects,
  identical). The first pass exported the view fields, `roleCatalog`'s
  `Root`, `Revision` and `Advance`, and the `roleDependency` and
  `rowSecurityLayer` fields (2,748 identifiers in 482 files). The second
  exported the moved function and type names in place (209 identifiers in
  70 files).
- **Move.** `internal/engine/access` holds `view.go` (`View`, `Environment`,
  `Requirement`, `Stamp`, the checks, `Object`, `DescriptorACL`,
  `ClassPrivileges`, `ValidateACL`), `ownership.go`, `rls.go`
  (`RLSDisposition`, `RowSecurityLayer`, ...), `roles.go` (`RoleRecord`,
  `RoleRead`), `role_dependencies.go` (`RoleDependency`, the scans),
  `privileges.go` (`DatabaseRights`, `PredefinedPrivileges`, `RawObjectACL`,
  `ObjectACLClass`), `names.go` (copies of `dependencyIdentifier`,
  `typeDiagnosticNameAt` and `quoteIdentifier`) and `errors.go`
  (`sqlError`). It imports `catalog`, `authorization`, `ids`, `parser`,
  `sqlstate` and the standard library. The engine files `access_view.go`,
  `access_ownership.go`, `access_rls.go` and `predefined_roles.go` are gone;
  153 references in 65 engine files are qualified as `access.X`.
  `roleStore.read` delegates to `access.RoleRead`.
- **Deviations.** `View.object` became the exported `Object`: seven
  production and six test call sites in the engine (`routine_admission.go`,
  `sequence.go`, `data_type_privileges.go`, ...) resolve objects through it,
  which the B2 census did not show. `aclRoleDependencies`,
  `recordRoleDependency` and `ownsTemporaryNamespace` stay unexported in
  `access`.
- **Tests.** No engine test could move: all of them build an engine. They
  were adapted by the rewrites only (the three `Roles()` callers, the
  `ownTemporaryPrivileges` and `object` callers). New standalone tests in
  `access/view_test.go` build a catalog owner, a seeded role root and a
  current database without an engine: `TestViewRejectsForeignOrStaleObjects`,
  `TestRoleReadRequiresOwnerDomainAndRoot`,
  `TestViewTemporaryNamespaceOnlyFromEnvironment` and
  `TestRowSecurityPrincipalFollowsInnermostDefiner`. Mutating the epoch,
  pairing, reference-domain, role owner and role domain checks, the lazy
  TEMP lookup, the TEMP CREATE right and either RowSecurityPrincipal branch
  makes them fail. Removing only the catalog-owner comparison in `Object`
  does not, because the paired role-read check rejects the same views.
  None deleted.
- **Verification.** `make build` and a targeted `make check` selection
  (authorization, roles, ownership, access, RLS, policies, default
  privileges, object ACLs and grants, databases, predefined roles,
  `pg_group`, `pg_class` ACLs, data type privileges, membership,
  collations, foreign objects, schemas, extensions, renames, rule sources,
  `pg_indexes`, range partition Restore, driver batches, table privileges,
  Dump and Restore) passed after each commit. `RACE=1` passed for the
  README concurrency set, the access-view and role-store seam tests and the
  new `access` tests. `quickcheck` passed. The README reference set plus
  `TestReferenceAccessRole.*`, `TestReferenceForeignActionsAuthorization.*`,
  `TestReferenceSchemaAuthorization` and
  `TestReferenceTypedTableAuthorization` passed in quick mode and with
  `REFERENCE=1` after the rebase onto `ce0c3d233`, which fixed the two
  PostgreSQL 18.6 oracle drifts recorded in B2 (before it, those two failed
  on the real-server side only). `quickcheck` passed again after the
  rebase. The full `racecheck`
  was skipped at the user's request. `make bench`: 1.53–1.68 ms/op before
  and 1.52–1.85 ms/op after, 7,769 allocations in both (the first run 7,777
  in both); the <1 ms target is already missed on `master`. `make deps`
  lists only module paths. `AGENTS.md` stays at 100 lines (`access` joins
  the subpackage list).

Notes for later steps (B3):

- **B4:** the aliases above have about 170 production and 130 test uses
  (`AccessRequirement` 97 and 43); `AuthorizationStamp` has none left. `AuthorizationView` embeds `accessView`,
  so replacing that alias renames the embedded field from `accessView` to
  `View` (`v.accessView.Check` becomes `v.View.Check`, and test literals
  change key). `fixedAccessEnvironment` (engine tests) can stay.
- **Later:** `access/names.go` duplicates the engine's diagnostic name
  helpers. A shared home below both (for example `catalog`, which already
  owns the type descriptors) would remove the copy; the engine has about 80
  uses. Done in the names cleanup (see its results below).

B4 results:

- **Rewrite.** A go/types tool (the 1d/4c qualifier, re-parametrized)
  type-checked the engine with its in-package tests and rewrote by object
  identity: 241 alias uses (`AccessRequirement` 141, `roleCatalog` 40,
  `accessView` 20, `rlsUnrestricted` 16, `rlsDefaultDeny` 13,
  `rlsNeedsExpressions` 6, `rlsDisposition` 5) to `access.X`, and the 53
  uses of `AuthorizationView`'s embedded `accessView` field (selectors and
  composite-literal keys) to `View`, in 108 files. `accessEnvironment` and
  `AuthorizationStamp` had no uses left. It checked with `LookupParent`
  that `access` names the package at every rewritten use (no shadowing
  found), added the import to 76 files and gofmt'ed only files that were
  already clean. The per-object census matched before and after. Engine
  comments naming the aliases and the alias paragraph of the `access`
  README were updated by hand.
- **Not changed.** `docs/design/PG18-ARCH-AUTHORIZATION.md` keeps its
  design-time sketch names (`AccessRequirement`, `AuthorizationStamp`, an
  interface `AuthorizationView`): they are rationale, and the note already
  points at the `access` README. `fixedAccessEnvironment` (engine tests)
  stays, as planned.
- **Tests.** None moved, deleted or substantially rewritten; test files
  changed only by the mechanical rewrite.
- **Verification.** `make build` after each commit. Targeted `make check`
  (the `authorization` README unit and engine-consumer sets, the `access`
  package tests, role store, access view, RLS, policies, ownership,
  `pg_group`/`pg_roles`/`pg_user`/`pg_shdescription`, predefined roles,
  data type privileges, ACL items, comments, generated columns, rules and
  index predicates) passed, and `RACE=1` passed for the README concurrency
  set with the role store and `access` seam tests. `quickcheck` passed.
  The full `racecheck` was skipped at the user's request. `make bench` was
  not run: removing type aliases leaves the compiled code unchanged.
  `make deps` lists only module paths; `access` imports are unchanged.
  `AGENTS.md` stays at 100 lines (`authorization` dropped from the
  `internal/engine` bullet).

Notes for later steps (B4):

- Part B is done. What remains of authorization in the engine waits for
  later waves, as recorded above: the ACL SQL function nodes for step 3
  Tier C, the privilege projections for `pgcatalog`, Dump/Restore and
  remap for `control`, policy DDL for the policy feature package and the
  binding walkers for `plan`/`exec`.
- `access/names.go` still duplicates the engine's diagnostic name helpers
  (see the B3 notes). Done in the names cleanup below.

Names cleanup results:

- **Move.** `internal/engine/catalog/diagnostic_names.go` holds
  `QuoteIdentifier`, `DependencyIdentifier` and `TypeDiagnosticNameAt`,
  moved verbatim from the engine (`control.go`, `roles.go`, `domain.go`).
  `access/names.go` is deleted and `access/view.go` calls the `catalog`
  helpers. `catalog` gains only the `strings` import.
- **Rewrite.** The engine's three definitions are gone. Their 393 uses in
  76 files (`quoteIdentifier` 305 including 7 in four test files,
  `dependencyIdentifier` 73, `typeDiagnosticNameAt` 15, comments included)
  now name `catalog.X`; four files gained the `catalog` import.
  Seventeen locals and parameters named `catalog` shadowed the package
  where a helper was called; `gopls rename` renamed each to `catalogView`.
  Other parameters named `catalog` stay.
- **Tests.** New `catalog/diagnostic_names_test.go`:
  `TestDependencyIdentifierQuotesLikeQuoteIdentifier` and
  `TestTypeDiagnosticNameAtQualifiesHiddenTypes` (nil, empty and shadowing
  paths, a quoted schema, arrays). No engine test moved or was deleted;
  four engine tests changed only by the rewrite.
- **Verification.** `make build` after each commit. Targeted `make check`
  (the new tests, `catalog` and `access` package tests, `TestView.*`,
  Dump/Restore tests, and the typed-table, table-LIKE composite ACL,
  range-partition type ACL, domain, composite and enum diagnostics
  reference tests) passed in quick mode and with `REFERENCE=1`.
  `quickcheck` passed. The full `racecheck` was skipped at the user's
  request; no locks or goroutines changed. `make bench` was not run: no
  startup path changed. `make deps` lists only module paths. `AGENTS.md`
  stays at 100 lines.
