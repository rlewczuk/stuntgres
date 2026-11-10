# internal/engine/typecmds

Type/domain evolution and native typed replay orchestration. `typedef` and
`table` own decisions; core owns generation, domain-check and row runtime.

## Layering

Imports core, tablecmds' shared type/table hosts and decision packages, never the
root. Viewcmds reuses the declared domain replay seam. Schema-level creation and
identity utilities dispatched through schemacmds stay with that family.

## Entry points

`ExecuteAlterDomain`, `ExecuteAlterComposite` and `ExecuteEnumDDL` enter with
nothing held. Resolve under Engine.Mu, wait unlocked, recheck exact original
type/table identities and authority, then stage/validate one candidate.
`AlterTableOfLocked` is an admitted lock-domain continuation that releases
Engine.Mu on return.

Actual existing-value validation, typed row conversion and index preparation run
in detached current-invocation contexts, not under metadata binding locks.
Table maintenance ownership is separate from source-type ownership. Complete
candidate rows/index roots and generation/provenance observations transfer into
the ordinary journal together; destructive rewrites and preserving codec transport
retain distinct physical incarnation/maintenance fences.

Dump checks and native replay helpers share real typed binder/codec owners.
Replay requires authenticated isolated-import authority and exact current provider
identity, not literal spelling or a generic privilege bypass. Physical source
handles, grants and session state are never imported.

## Testing

External `typecmds_test` tests use composed engines, controls and `enginetest`.
Schema-staged type fixtures live with schemacmds; core tests cover real generation
and publication ownership. Public SQL belongs to `reference/types` and
`reference/domain_types` under [docs/TESTING.md](../../../docs/TESTING.md).
