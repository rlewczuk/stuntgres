# internal/engine/authorization

Roles, role membership and ACL algorithms. The package holds immutable values
and pure functions over them: no locks, sessions, catalog storage or
publication. It imports `parser`, `sqlstate`, `sqltype/unicode16` (SASLprep)
and the standard library, never the engine. It sits at layer L1 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture), below
`catalog`, because catalog descriptors hold its `ACLValue` and `PrincipalRef`.
Enforcement over catalog objects is in [`access`](../access/README.md); role
and ACL DDL decisions are in [`role`](../role/README.md).

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
  `Read.GroupRole` validates an already-enumerated role's NOLOGIN identity
  without another descriptor lookup; `Read.GroupMember` checks a live,
  same-domain, nonzero, OID-representable endpoint with a matching
  descriptor. The engine's `pg_group` producer applies these same rules in
  its parent loop and both direct-edge passes; typed NAME/OID/member arrays,
  origin multiplicities, count limits, per-entry cancellation and returned-row
  snapshots stay in the engine.
  Candidates: `PutDescriptor`, `Retire`, `Reprovenance`,
  `ValidateRoleAttributes`, `AttributeCandidate`, `MembershipCandidate`,
  `OwnedMembershipCandidate` and `ValidateDeletionCandidate`.
  `MergeRoots` combines an existing base, private candidate and current root:
  exact touched descriptor/name/edge preconditions protect peer changes, while
  disjoint changes survive and the merged graph is validated before publication.
  `RoleDescriptor.AuthIDVersion` identifies attribute tuple replacements,
  including no-op updates; membership and comments preserve that identity.
  `Membership.OID` is immutable numeric row metadata, not a principal reference.
  The logical edge key remains (role, member, grantor). Pure candidates may have
  zero IDs; `AssignMembershipOIDs` preflights and copies only new edges before
  accepted publication. Option changes, renames, merge and reprovenance preserve
  surviving row IDs; removal followed by a new grant obtains a fresh identity.
- **ACL algorithms:** `Privileges`, `ACLValue`, `ACLEntry` and `ACLGrantee`.
  `ACLRights` evaluates an ACL. `ObjectGrantSource`, `ChangeObjectACL`,
  `ScopedACLGrantOptions` and `OwnedACL` plan GRANT, REVOKE and DROP OWNED;
  `GrantWarning` is the 01006/01007 warning of a partial GRANT or REVOKE.
  `SchemaACLOwner` transfers ownership. `MaterializeRelationACL` and
  `MaterializeDatabaseACL` build absent-ACL baselines. `ObjectPrivilege` and
  `ForeignPrivilege` parse grant lists; `ParseRelationPrivileges` validates full
  inquiry lists into ordinary/grant-option alternatives without changing the
  stored ACL grammar. `ACLItemBits` converts to PostgreSQL's `aclitem` layout.
  `ValidateACL` checks stored values.
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

- **Publication:** `Engine.RoleStore`, the cluster's
  [`access.RoleStore`](../access/README.md), holds the domain, the
  committed record with the current `Root` and its revision headroom, and
  principal allocation. Cancellation fences, journals and savepoints stay
  with the transaction code.
  Object/default ACL changes use that same scope-owned journal and combined
  publication. The `access.RoleRecord` immutable authority origin distinguishes
  revision-only ACL changes from actual role-root mutations. ACL-only journals
  keep a checked logical delta until final allocator-consistent publication.
  Completed ACL-only work merges its zero actual role delta when a compatible
  principal publication changed the root, and validates retained catalog/default
  references against current authority. Actual-role and mixed journals retain
  explicit three-way merging and principal/member/precondition checks.
  Same-authority reads retain the exact private record; compatible actual-root
  refreshes preserve the original ACL delta without reserving an allocator
  revision. Pending selected statements separately retain their original
  private/published authority pair through Finish, not through outer Commit.
  This does not weaken exact access stamps or make lineage a permission token.
- **Pairing:** each `Read` is captured together with its exact `catalog.Read`;
  a stamp reads that captured pair, not later engine state. There is no cached
  ACL decision.
- **Catalog data:** ACL fields on descriptors, default-privilege records and
  their keys, and typed projections such as `pg_roles`, `pg_group` and
  `relacl`.
- **Statements:** GRANT, REVOKE, ALTER DEFAULT PRIVILEGES, role DDL, SET ROLE
  and session startup.
  Transactional object/default ACLs do not widen existing role lifecycle,
  membership or ownership policies, enable ALTER SYSTEM or admit SQL PREPARE
  of those utilities; class-owned boundaries remain outside this package.

The ACL SQL functions (`aclexplode`, `acldefault`, `makeaclitem`, `aclitem`
input and output) work on raw `aclitem` bits and live in
[`functions`](../functions/README.md).

**Enforcement** reads catalog descriptors, so it cannot live here without an
import cycle. It lives in [`access`](../access/README.md), above `catalog`:
the captured view and its privilege checks, the ownership checks, RLS policy
selection and the role dependency scans. Default-privilege resolution lives
in `catalog` (`CreationDefaultACL`).

## Testing

Unit tests here pin the algorithms: masks, grant sources, selective revoke,
candidate immutability, reached notices, cyclic ADMIN traversal, malformed
`pg_group` storage, password vectors, default merges, predefined roles and
parser character positions. Engine unit tests cover publication, pairing,
lifecycle, cancellation and typed projections (`pg_group`, `relacl`, database
ACL baselines).

```sh
make check RUN='^Test(ACLItemBits|Authorization(DescriptorCandidate|Diagnostic|Malformed)|Default(LargeObject|Schema)CreationACLOrder|DefaultPrivileges(Baselines|Masks|Merge)|MembershipCandidate|ObjectGrant|ObjectRevoke|OwnershipDeletionCandidate|PGGroupMalformedRoot|PredefinedRoleInitialization|RoleAttributes|RoleOwnership(ACLScopes|GrantOptions)|RolePrivilegesParse|SessionIdentityMembershipClosure)'
make check RUN='^Test(AuthorizationStampRetainsRolePublication|RoleOwnershipDatabase|CollationBootstrapPrincipalNameReuse|RoleOwnershipInitialSourceCanonicalIdentity|RoleStore|PGGroup|AccessInquiryRoleOIDs|DatabaseACLBaselineTransitions|PgClassACLTranslation)'
make check RACE=1 RUN='^Test(MembershipConcurrentCyclePublication|AuthorizationInitializationFencesDeletedPrincipal|RoleAttributesConnectionLimitRaces|RoleAttributesPasswordCancellationLifecycle)$$'
```

Public behavior is in [reference/roles](../../../reference/roles/README.md#testing)
and [reference/object_grants](../../../reference/object_grants/README.md#testing).
