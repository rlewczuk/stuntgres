# internal/engine/core/catalogrt

Catalog-source runtime below core: `pgcatalog.Runtime`, coherent capture caches,
query keep filters, builtin source registration and shared-dependency projection.
It never imports core; core supplies its `Host` adapter.

## Contract

- `Runtime` serves one immutable paired `access.View` plus captured input,
  database, TEMP, presentation and sequence-overlay values. Producers receive
  values, not Engine, StatementScope, bindings or live provider/writer handles.
- The embedded `Host` supplies engine-owned capture capabilities and acts as the
  authority's `access.Environment`. A hostless Runtime supports presentation only.
- `ValidateCapture` requires exact source/reference, owner pointer and current
  principal even for empty results. Equal names, OIDs or epochs are insufficient.
- Presentation uses captured settings/path and checked declarations without
  expression evaluation, provider binding or retained runtime authority.
- `CaptureRows` returns owned typed sources. Empty rowsets remain real owned
  sources rather than missing source handles.
- `CaptureCache` keys published rows by exact catalog/role roots and source/ref,
  under `Engine.Mu`. Private roots capture fresh. Errors are never cached, and
  hits still honor cancellation. Query-restricted partial captures are not
  installed as complete cache entries.
- `KeepQuery` restricts only the exact query-owned source occurrence. Demand
  proofs do not evaluate arbitrary expressions or become retained permissions;
  current parameters and normal source authorization remain independently checked.
- `InitializeSources` installs `pgcatalog.Specs` in registration order with
  engine-owned descriptors, ACLs and type generations. Shared templates remain
  immutable. Dependency visitors consume retained per-database roots.

## Locks

The package owns no lock. `Locked` capture functions enter and return with
`Engine.Mu` held; core owns unlocked session/settings capture windows.

## Testing

Pure capture/dependency units live here. Engine fixtures stay with core or its
`enginetests` and use area-owned test accessors. Public catalog SQL lives in
reference packages.
