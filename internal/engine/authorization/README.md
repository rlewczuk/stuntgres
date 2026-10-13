# Immutable authorization algorithms

This internal package owns role descriptors, immutable membership graphs,
predefined identities, role-attribute/password algorithms, ACL grant/revoke
algorithms and creator-scoped default-privilege leaves. It imports parser,
sqlstate, Unicode support and standard-library code, never its parent engine or
public driver adapters. SQL and native compatibility boundaries remain described
in [AUTHORIZATION.md](../../../docs/AUTHORIZATION.md).

## Ownership and capture

- `PrincipalRef` is an opaque comparable domain/identity pair. Numeric IDs and
  familiar names do not establish authority. Each engine embeds one inert,
  nonzero-sized `Domain`; the engine owns nonzero/no-reuse allocation and keeps
  the domain across Reset/Restore. Zero, foreign and stale references are checked
  before self/superuser shortcuts.
- `Root` contains private immutable descriptor/name maps and ordered membership
  edges, with no revision, lock, session, catalog or publication owner. The engine
  embeds its value in a private stamped publication record. Candidate transforms
  return root values; the engine retains the existing single publication-record
  allocation and decides installation, revision headroom and cancellation fences.
- `Read` retains root storage and the actual database owner supplied by engine
  capture. The engine retains the exact paired CatalogRead and publication record
  beside it; a stamp reads that captured record, not subsequent engine state.
  Catalog substitution recaptures the pair and database-owner input together.
  Equal epoch/revision counters are not a substitute for exact pairing.
- Capture copies neither publication records nor backing graphs. Descriptor
  transforms copy their maps and share unchanged edges; membership transforms
  share maps and copy the edge candidate as required. ACL-only publication shares
  unchanged payload backing. `RoleEntries`/`Edges` enumerate without materializing
  projections; the retained `Memberships` API returns an independent slice copy.
- `GroupRole(ref, descriptor)` checks the actual raw-enumerated NOLOGIN descriptor's
  provenance, nonzero/OID width and matching reference without a second map lookup.
  `GroupMember(ref)` checks the same scalar reference bounds and its live matching
  descriptor with one lookup; LOGIN and membership options do not filter members.
  Engine `capturePGGroup` uses the first rule for parents and the second in both
  existing count/fill edge passes. Raw enumeration/lookup stays unfiltered; the
  engine owns typed construction, count limits, multiplicities and cancellation.
- `PutDescriptor` is the genuine CREATE/ALTER/COMMENT immutable storage transform,
  not a SQL rename facility or fixture setter. `Reprovenance` is Restore's private
  staging transform and fills the engine's existing incarnation mapping.

The engine remains the sole lock/lifecycle/publication authority. Generic catalog
storage, object/namespace/column identity, default-policy record keys and
present/absent observations, physical admission, session journals/savepoints,
class-specific dependency cleanup and typed query projections stay engine-owned.
There is no engine facade, callback-per-helper provider, cached ACL decision or
child revision API.

## Algorithm boundaries

Membership has distinct MEMBER, INHERIT, SET and ADMIN traversal. Ordinary
privileges follow INHERIT; special attributes remain direct. The implicit
`pg_database_owner` hop uses the captured database owner and supplies no ADMIN
edge. Grantor selection preserves breadth-first ordering and independent origins.
GRANT sees its evolving candidate, whereas REVOKE plans against entry authority
and original tuples. A failed candidate never publishes a prefix. Reached notices
are returned in the existing notice buffer even when a later action fails;
password notices remain engine-emitted only after successful installation.

ACL leaves take concrete owners, ACL values, principals and applicable masks.
Absent and explicitly empty ACLs are distinct. Owner use, ownership and implicit
grant options are separate; PUBLIC has no principal or grant options. One grant
source is selected rather than unioning independent sources. Revoke/cascade
preserves independent support and removes only unsupported origin bits. The
engine retains stable-column/class checks, TEMP scope, view owner/invoker scope,
ordered diagnostics and catalog publication.

Defaults are creation-only, effective-creator/category scoped. Global and scoped
inputs are pointers to existing immutable ACL fields, not a child policy store;
scoped defaults are additive and cannot subtract global grants. Namespace
identity, sparse tombstones and record/present-absence fences remain catalog-owned.
Empty-ACL normalization occurs only at creation, not ordinary authorization.
Password/verifier encodings, SCRAM parameters and SASLprep fixed data retain their
existing behavior; native trusted host admission is not password authentication.

## Testing

Run from the repository root using the shared
[testing policy](../../../docs/TESTING.md) and
[development commands](../../../docs/DEVELOPMENT.md). Private units supplement,
never replace, the unchanged public dual-mode reference definitions.

Focused private witnesses include literal masks, selective revoke/alternate
support, immutable descriptor/name/edge candidates, reached notices, original-row
REVOKE, password vectors and creation-default merge. Extracted syntax diagnostics
retain parser character positions rather than UTF-8 byte offsets.

`TestRoleAttributesCyclicAdminTraversal` checks positive and negative ADMIN,
INHERIT/SET distinctions, invalid endpoints and stored direct attributes on the
same disabled-option cycle. Genuine attribute validation/candidates prove denied
inherited CREATE/ALTER, later direct CREATEROLE authority, unchanged cyclic edges
and immutable old Reads. `TestPGGroupMalformedRoot` directly exercises the
production `GroupRole`/`GroupMember` rules on foreign, stale, wide, zero, mismatched
and dangling private storage. Raw enumeration and old-root behavior remain
separate assertions, not a reimplemented projection.

Engine `TestPGGroupProjection`, `TestPGGroupRoleRoot` and `TestPGGroupCancellation`
retain complete typed rows, origin multiplicity, high-ID nonalias, snapshots,
constructor failures and per-pass cancellation. Other engine units retain full
publication/headroom, actual database pairing, bootstrap-name reuse, lifecycle
and journal assertions. The child predicates do not replace those consumers.

```sh
timeout 900s make check RUN='^(TestObjectGrantMasksAndDefaults|TestObjectGrantSingleSourceAndImmutableOrigins|TestObjectRevokeAlternateSupport|TestObjectRevokeSelectiveBits|TestDefaultPrivilegesMasks|TestDefaultPrivilegesBaselines|TestDefaultPrivilegesMerge)$'
timeout 900s make check RUN='^(TestAuthorizationDescriptorCandidateImmutability|TestAuthorizationMalformedEnumeration|TestPGGroupMalformedRoot|TestOwnershipDeletionCandidateValidation|TestMembershipCandidateImmutabilityAndReachedNotices|TestRoleAttributesCyclicAdminTraversal)$'
timeout 900s make check RUN='^(TestAuthorizationStampRetainsRolePublication|TestRoleOwnershipDatabaseImmutableRolePair|TestRoleOwnershipDatabaseRejectsMixedAuthorization|TestCollationBootstrapPrincipalNameReuse|TestRoleOwnershipInitialSourceCanonicalIdentity)$'
timeout 900s make check RACE=1 RUN='^(TestMembershipConcurrentCyclePublication|TestAuthorizationInitializationFencesDeletedPrincipal|TestRoleAttributesConnectionLimitRaces|TestRoleAttributesPasswordCancellationLifecycle)$'
timeout 900s make check RUN='^TestAuthorizationDiagnosticCharacterPosition$'
timeout 900s make check RUN='^(TestRoleAttributesCyclicAdminTraversal|TestRoleAttributesMembershipNonInheritanceAndAdminTraversal)$'
timeout 900s make check RUN='^(TestPGGroupMalformedRoot|TestPGGroupProjection|TestPGGroupRoleRoot|TestPGGroupCancellation)$'
```

Existing public witnesses must run with the same definitions in pinned PostgreSQL
18.0 UTF8/C and native modes. For example:

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleMembership(AuthorityGrantor|NoninheritAdminCorner|NearestGrantor|FeatureSmoke)$'
timeout 900s make check RUN='^TestReferenceRoleMembership(AuthorityGrantor|NoninheritAdminCorner|NearestGrantor|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleSwitch(IdentityAuthority|Savepoints|CachedRestoration|FeatureSmoke)$'
timeout 900s make check RUN='^TestReferenceRoleSwitch(IdentityAuthority|Savepoints|CachedRestoration|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePredefined(DataFeatureSmoke|MaintainFeatureSmoke|BoundaryFeatureSmoke)$'
timeout 900s make check RUN='^TestReferencePredefined(DataFeatureSmoke|MaintainFeatureSmoke|BoundaryFeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceRoleDefaultPrivileges(Authority|Precedence|OwnerOptions|FeatureSmoke)$'
timeout 900s make check RUN='^TestReferenceRoleDefaultPrivileges(Authority|Precedence|OwnerOptions|FeatureSmoke)$'
```

These focused examples do not replace the extraction's complete selected replay,
`make build`, whole-project `timeout 900s make quickcheck` and
`timeout 900s make racecheck`. Post-integration verification uses
`timeout 1800s make finalcheck`; finalcheck does not replace real PostgreSQL proof.

A separate temporary public Go/database/sql smoke provisions roles/table access,
holds the same prepared reader across membership revoke/regrant, checks
savepoint/rollback identity restoration and continues on the same physical owner.
Its independently expected observations are:

```text
held reader: 7 -> 42501 -> 7; savepoint/rollback identity: extract_reader; same-owner health: 11
```

The unchanged RoleGrantFeatureSmoke separately witnesses table revoke/regrant on
a held reader. Remove the temporary program after observing its results; this
package does not add a permanent public smoke test or alternate fixture.
