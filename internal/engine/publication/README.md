# internal/engine/publication

Database-local logical publication validation, private staging and effective
membership below core. The package reuses catalog/DDL, authorization, normal
expression and relation/hierarchy owners; it does not implement transport.

`Stage` writes only the caller's private catalog under caller-owned Engine.Mu.
Core owns name/definition/dependency admission, waits and journal publication.
A canceled waiter cannot publish its provisional candidate.

`VisitTables` enumerates deduplicated presented relations; `VisitRelation` resolves
current effective definitions for one physical relation through real ancestry.
Presentation membership and DML-effective filters/column lists are distinct.
Borrowed descriptors and stable source attributes remain immutable; callers map
them to actual leaf layouts rather than inferring physical identity.

Filters reuse `stored.Policy` and the ordinary binder/renderer. Semantic catalog
edges and full executable lifetime dependencies are distinct. Local SQL is never
filtered by publication metadata. Replica-identity checks consume current
membership and actual physical key definitions; Prepare retains no permission.
Core's Execute admission does not grant unsupported executor capabilities.

## Testing

Pure domain/decision units live in `enginetests`; core tests cover retained
predicates and capture/publication ownership. Public SQL lives in
`reference/catalogs` under [docs/TESTING.md](../../../docs/TESTING.md).
