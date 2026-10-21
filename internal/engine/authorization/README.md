# internal/engine/authorization

Roles, role membership and ACL algorithms. The package holds immutable values
and pure functions over them: no locks, sessions, catalog storage or
publication. It imports `parser`, `sqlstate`, `sqltype/unicode16` (SASLprep)
and the standard library, never the engine. It sits at layer L1 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture), below
`catalog`, because catalog descriptors hold its `ACLValue` and `PrincipalRef`.
SQL behavior and native limits are described in
[AUTHORIZATION.md](../../../docs/AUTHORIZATION.md).

## What it holds

- **Identity and domain:** `Domain` and `PrincipalRef`. A reference is an
  opaque, comparable domain/ID pair minted by `(*Domain).Principal`. This
  domain is separate from `ids.Domain`: Restore stages principals in a domain
  of its own (`Root.Reprovenance`).
- **Roles and membership:** `Root` (the stored role graph), `Read` (a capture
  of one root plus the database owner), `RoleDescriptor`, `Membership` and
  `RoleState` (session, current and reset roles). `Seed` builds the predefined
  roles. Queries: `Lookup`, `Find`, `Descriptor`, `ValidatePrincipal`,
  `RoleClosure`, `InheritsRole`, `AdminOfRole`, `CanSetRole`,
  `ValidateInitialRole`, `HasRole` with `ParseRolePrivileges` (`pg_has_role`),
  `GroupRole`/`GroupMember` (`pg_group`) and `PredefinedPrivileges`.
  Candidates: `PutDescriptor`, `Retire`, `Reprovenance`,
  `ValidateRoleAttributes`, `AttributeCandidate`, `MembershipCandidate`,
  `OwnedMembershipCandidate` and `ValidateDeletionCandidate`.
- **ACL algorithms:** `Privileges`, `ACLValue`, `ACLEntry` and `ACLGrantee`.
  `ACLRights` evaluates an ACL. `ObjectGrantSource`, `ChangeObjectACL`,
  `ScopedACLGrantOptions` and `OwnedACL` plan GRANT, REVOKE and DROP OWNED;
  `GrantWarning` is the 01006/01007 warning of a partial GRANT or REVOKE.
  `SchemaACLOwner` transfers ownership. `MaterializeRelationACL` and
  `MaterializeDatabaseACL` build absent-ACL baselines. `ObjectPrivilege` and
  `ForeignPrivilege` parse privilege lists. `ACLItemBits` converts to
  PostgreSQL's `aclitem` bit layout. `ValidateACL` checks stored values.
- **Default privileges:** `DefaultACLClass`, `DefaultClass`, `DefaultMask`,
  `DefaultBaseline`, `MatchesDefaultBaseline`, `DefaultPrivilege`,
  `CreationDefaultACL`, `ValidateDefaultACL`, `ValidateDefaultACLHeader` and
  `EqualACL`.
- **Passwords:** `PrepareRolePassword` (SCRAM-SHA-256 and MD5 verifiers,
  SASLprep) and `PasswordNotice`.

## Invariants

- **Provenance first.** Zero, foreign and stale references are rejected before
  any self or superuser shortcut. Numeric IDs and names never establish
  authority. `HasRole` is the one exception: it takes raw SQL OIDs and keeps
  PostgreSQL's shortcuts for absent roles.
- **Immutable roots.** `Root` has no revision, lock or owner. Transforms return
  new roots and never publish a prefix of a failed candidate. Capturing a
  `Read` copies nothing; transforms copy maps and share unchanged edges;
  `Memberships` returns an independent copy.
- **Membership.** MEMBER, INHERIT, SET and ADMIN traverse separately. Ordinary
  privileges follow INHERIT; role attributes are direct only. The implicit
  `pg_database_owner` hop uses the captured database owner and grants no
  ADMIN. Grantor selection is breadth-first. GRANT sees its evolving candidate;
  REVOKE plans against the entry state and original rows.
- **ACLs.** Absent and empty ACLs stay distinct. Ownership, owner use and
  implicit grant options are separate; PUBLIC has no principal or grant
  options. A grant uses one source, never a union. Revoke cascades remove only
  bits that lost all support.
- **Defaults are creation-only.** They are scoped by creator and object class.
  Schema-scoped defaults add to global ones and cannot remove them. Empty-ACL
  normalization happens only at creation.
- **Notices.** Functions return reached notices even when a later step fails.
  Password notices are emitted by the engine, only after installation.

## What stays in the engine

- **Publication:** `roleStore` (`role_store.go`) holds the domain, the
  committed record with the current `Root` and its revision headroom,
  principal allocation and other features' role references. Cancellation
  fences, journals and savepoints stay with the transaction code.
- **Pairing:** each `Read` is captured together with its exact `catalog.Read`;
  a stamp reads that captured pair, not later engine state. There is no cached
  ACL decision.
- **Catalog data:** ACL fields on descriptors, default-privilege records and
  their keys, and typed projections such as `pg_roles`, `pg_group` and
  `relacl`.
- **Statements:** GRANT, REVOKE, ALTER DEFAULT PRIVILEGES, role DDL, SET ROLE
  and session startup.
- **ACL SQL functions** (`aclexplode`, `acldefault`, `makeaclitem`, `aclitem`
  input and output). They work on raw `aclitem` bits and move with the other
  builtin functions in step 3.

**Enforcement** reads catalog descriptors, so it cannot live here without an
import cycle. It lives in [`access`](../access/README.md), above `catalog`:
the captured view and its privilege checks, the ownership checks, RLS policy
selection and the role dependency scans. Default-privilege resolution lives
in `catalog` (`CreationDefaultACL`).

## Testing

Run from the repository root under the shared
[testing policy](../../../docs/TESTING.md). Unit tests here pin the algorithms:
masks, grant sources, selective revoke, candidate immutability, reached
notices, cyclic ADMIN traversal, malformed `pg_group` storage, password
vectors, default merges and parser character positions. Engine unit tests
cover publication, pairing, lifecycle, cancellation and typed projections.

```sh
timeout 900s make check RUN='^(TestObjectGrantMasksAndDefaults|TestObjectGrantSingleSourceAndImmutableOrigins|TestObjectRevokeAlternateSupport|TestObjectRevokeSelectiveBits|TestDefaultPrivilegesMasks|TestDefaultPrivilegesBaselines|TestDefaultPrivilegesMerge|TestACLItemBits|TestRolePrivilegesParse)$'
timeout 900s make check RUN='^(TestAuthorizationDescriptorCandidateImmutability|TestAuthorizationMalformedEnumeration|TestPGGroupMalformedRoot|TestOwnershipDeletionCandidateValidation|TestMembershipCandidateImmutabilityAndReachedNotices|TestRoleAttributesCyclicAdminTraversal|TestAuthorizationDiagnosticCharacterPosition)$'
```

Engine consumers:

```sh
timeout 900s make check RUN='^(TestAuthorizationStampRetainsRolePublication|TestRoleOwnershipDatabaseImmutableRolePair|TestRoleOwnershipDatabaseRejectsMixedAuthorization|TestCollationBootstrapPrincipalNameReuse|TestRoleOwnershipInitialSourceCanonicalIdentity|TestRoleAttributesMembershipNonInheritanceAndAdminTraversal|TestRoleStoreReadRequiresOwnCatalogAndRoot|TestRoleStoreSetReferencesFailuresLeaveStateUnchanged|TestAccessView.*)$'
timeout 900s make check RUN='^(TestPGGroupProjection|TestPGGroupRoleRoot|TestPGGroupCancellation|TestAccessInquiryRoleOIDs|TestDatabaseACLBaselineTransitions|TestPgClassACLTranslation)$'
timeout 900s make check RACE=1 RUN='^(TestMembershipConcurrentCyclePublication|TestAuthorizationInitializationFencesDeletedPrincipal|TestRoleAttributesConnectionLimitRaces|TestRoleAttributesPasswordCancellationLifecycle)$'
```

Public behavior is covered by the dual-mode reference tests. Run each selection
with `REFERENCE=1` (pinned PostgreSQL) and without (quick mode), for example:

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleMembership(AuthorityGrantor|NoninheritAdminCorner|NearestGrantor|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleSwitch(IdentityAuthority|Savepoints|CachedRestoration|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePredefined(DataFeatureSmoke|MaintainFeatureSmoke|BoundaryFeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleDefaultPrivileges(Authority|Precedence|OwnerOptions|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^(TestReferenceAccessRole.*|TestReferenceObjectGrantAccessInquiry.*|TestReferencePGDatabaseACL|TestReferencePgClassACL|TestReferencePGParameterACL.*|TestReferenceObjectGrantDatabaseAdmission)$'
```
