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
reads descriptors, `CatalogRead`, `CatalogStage` and the `Engine`.

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

- Measure the engine-side authorization code on the current tree: files,
  declarations, external references, and which `Engine`, `Session` and
  `StatementScope` dependencies remain after step 5.
- Propose the contents of `access`. Expect `AuthorizationView` capture,
  privilege checks on catalog objects, default-privilege resolution, RLS
  policy selection and ownership checks. Role publication and
  `GRANT`/`REVOKE` statement execution are orchestration and likely stay in
  the engine.
- Write the refined plan into this file as sub-steps B1–B4, update the
  `access` row in ENGINE-SPLIT.md §6, and get approval.

### B1 — State ownership in place

Group the authorization state on `Engine`: `authorizationDomain`,
`aclAuthority`, `roles`, `nextPrincipal`, `importTargets` and
`roleReferences`. Restore staging fields may belong to Restore instead; decide
in B0. Move methods that only touch this state onto the group.

### B2 — Port the checks in place

Turn privilege-check `Engine` methods into functions over `catalog.Read`,
`authorization.Read` and the captured view. Entry points that need sessions or
statements stay in the engine and call them.

### B3 — Create `access` and move

`access` imports only `catalog`, `authorization`, `ids`, `expr` (policy
expressions), `sqltype`, `parser` and `sqlstate`. Leave aliases.

### B4 — Remove the aliases

Remove them with a tool aware of `go/types` (§8 on broad rewrites).

## Invariants

- Privilege decisions, SQLSTATEs, messages, notice order and grantor
  selection stay identical.
- Absent and empty ACLs stay distinct. Default privileges stay
  creation-only.
- Captured authorization stays paired with its exact `CatalogRead`. There is
  no cached ACL decision.
- Zero, foreign and stale principals are rejected before any superuser
  shortcut.

## Targeted tests

- The `authorization` package tests and the engine selectors listed in its
  README, with `RACE=1` for the concurrent ones.
- Part B: reference selections for role membership, role switching,
  predefined roles, default privileges, object GRANT/REVOKE, RLS policies and
  ownership changes, run with `REFERENCE=1` and in quick mode. The
  `authorization` README lists a starting set. Never edit `reference/`.

## Documentation

- Part A: the `authorization` README rewrite above.
- Part B: create `internal/engine/access/README.md`, and point the
  `authorization` README at it.
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
- **Part B:** `access` holds enforcement and imports nothing above `catalog`.
  The engine keeps only orchestration and entry points. The checks in §8.3
  pass.

## Status

- [x] A1 — Pure helpers moved, README rewritten (`e932fc381`, `96ee8d96b`,
  docs in the following commit)
- [ ] B0 — `access` refined and approved
- [ ] B1 — Authorization state grouped
- [ ] B2 — Checks ported to functions over catalog and authorization reads
- [ ] B3 — `access` created and code moved
- [ ] B4 — Aliases removed

Outstanding aliases: none (Part A added none).

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
  default masks in `aclitem` layout.
- **Step 5:** `overlayDefaultACLs`, `cloneDefaultACLs` and the default-ACL
  record keys are catalog storage keyed by `ObjectAddress`.
- **Dump/deparse owner:** `containsEmptyACL` walks `sqltype` values; it is not
  an authorization algorithm.
- **B0:** `ownershipRoleOperand` adapts parser role specs for the engine's
  `schemaOwner`; move it with that function into `access`.
  `capturedRegroleInput` belongs to `regrole` input, not `access`. The
  `pg_has_role` node now only gathers arguments and authority.
