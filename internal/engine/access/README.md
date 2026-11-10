# internal/engine/access

Authorization enforcement over catalog objects, above `catalog` and
[`authorization`](../authorization/README.md). The latter owns pure role and ACL
algorithms; this package applies them to captured descriptors. It never imports
the engine and adds no mutex or engine callback.

## What it holds

- **`View`** is an immutable paired capture of catalog ownership, catalog and role
  reads, publication predecessors, and effective/session role state. `Check`,
  `CanUse`, `CheckCreate`, `CheckColumns` and `Effective` consume this capture;
  `Ref`, `Stamp` and `Recapture` preserve its provenance. Retargeting copies a
  view, never mutates its roots.
- **`Environment`** supplies only `TemporaryNamespace` and `ReferencePath` at check
  time. Keep it a narrow diagnostic/TEMP boundary, not a service locator.
- **`RoleRecord`** pairs principal and persisted-settings roots with publication
  lineage. Principal authority equality is distinct from complete-record and
  exact-stamp equality. Settings-only changes preserve graph lineage.
- **`RoleSettings`** owns immutable rows keyed by actual database addresses and
  principal references. Input is copied, unchanged rows are shared, and merges
  require exact row predecessors. Retirement removes only the retired scope.
- **`RoleStore`** holds principal authority, committed records and allocator
  frontiers. It is pointer-stable and never copied; the engine mutex protects
  every field and `Locked` mutation. Accepted reservations survive merge and
  rollback leaves nonreusable gaps; speculative reads/candidates reserve nothing.
- **Replacement helpers** map principals and ACL values into genuine destination
  authority. Private Restore captures omit persisted defaults: those remain
  destination cluster state.
- **Ownership, namespace and dependency helpers** provide shared decisions for
  role/ownership DDL, TEMP access, cleanup and catalog dependency capture.
- **Row-security selection** returns immutable policy groups. Metadata selection
  and action capability checks are separate from runtime row evaluation.
- **SQL privilege inquiries** accept raw SQL actor OIDs and preserve their
  inquiry-specific missing-object/actor semantics; these do not widen admission.

## Invariants

- Validate owner, domain, epoch, paired reads and principal provenance before
  admission shortcuts. A view accepts only references backed by its owner and
  exact catalog capture. Raw-OID SQL inquiry is a distinct boundary.
- There is no cached ACL decision. Absent and explicit-empty ACLs are distinct;
  ownership, ordinary use and grant authority are separate.
- Mandatory simulation checks are independent of SQL ACLs. Physical subject and
  lexical frame are not replaced by view-owner, superuser or RI SQL authority.
  Publication validates actual observed predecessors, not a retained allow token.
- Stable attributes, dense evaluation slots and whole-row demands remain distinct.
  Semantic consumers own privilege demands; catalog copying does not infer them.
- Callers supply required locking. Checks over live roots need the engine mutex;
  captured immutable roots are never written.
- Diagnostics and privilege/grantor decisions follow PostgreSQL compatibility.

## What stays in the engine

`AuthorizationView` adds statement/session/settings/path inputs and implements
binding privileges plus `Environment`. Core owns capture, role overlays and
publication fences. Statement families own lock windows, staging and publication;
[`role`](../role/README.md) owns role/ACL DDL decisions. Binding and stored-view
walkers belong to [`analyze`](../analyze/README.md) and [`view`](../view/README.md).

## Testing

Package units build a catalog owner and paired role roots without an engine.
Composed tests exercise `AuthorizationView` and publication in core. Public SQL
behavior belongs to the reference suite; see
[docs/TESTING.md](../../../docs/TESTING.md).
