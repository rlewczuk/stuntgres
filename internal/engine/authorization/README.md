# internal/engine/authorization

Immutable roles, membership and ACL values with pure algorithms: no locks,
sessions, catalog storage or publication. Catalog descriptors depend on these
values; catalog-object enforcement lives above them in
[`access`](../access/README.md), and DDL decisions in [`role`](../role/README.md).

## What it holds

- `Domain` and opaque comparable `PrincipalRef` establish principal provenance,
  independently of the catalog's `ids.Domain`.
- `Root` owns the role graph; `Read` captures it with database-owner context.
  `RoleState` keeps authenticated, session and current identity separate from
  accepted display/reset text. Names never replace principal references.
- Role/attribute/membership candidates are immutable transforms. `MergeRoots`
  combines disjoint changes only after exact touched predecessor checks and
  complete graph validation. Surviving membership row identities are preserved;
  actual removal/regrant receives a fresh identity at accepted publication.
- ACL algorithms select grant sources, plan grant/revoke cascades and ownership
  cleanup, and validate stored values. Default privileges resolve creation-time
  baselines separately from existing object ACLs.
- Password helpers prepare/check verifiers without session or engine locks;
  activation, notices and installation remain the executor's responsibility.

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

Core's `access.RoleStore` holds cluster principal authority, allocator frontiers
and committed publication. Core owns exact catalog/role pairing, journals,
savepoints, cancellation and final atomic publication. Catalog descriptors own
ACL/default-privilege records; statement families orchestrate role and ACL DDL.
Raw ACL SQL value functions live in [`functions`](../functions/README.md).

## Testing

Package units exercise pure graph/ACL transforms and immutability. Engine tests
cover pairing, publication and lifecycle; public SQL is in `reference/roles` and
`reference/object_grants` under [docs/TESTING.md](../../../docs/TESTING.md).
