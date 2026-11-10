# internal/engine/viewcmds

View, rule, policy, trigger and materialized-refresh statement orchestration.
`view` owns decisions; core owns firing, rule/DML runtime and atomic publication.

## Layering

Imports core, tablecmds' shared hosts and the declared typecmds replay seam,
never the root. Composition is its production caller; other families do not
import it. Core retains source opening, row generation, RLS, trigger execution,
clone synchronization and relation ownership publication.

## Entry points

Utility dispatch enters with nothing held. Resolve/decide under Engine.Mu,
release it for waits/provider/executable work, then recheck original authority
and targets before staging. Rule actions retain every consumed provider fence
through interleaved binding and unlocked admission.

`RefreshMaterializedLocked` enters with Engine.Mu held and the view admitted,
and releases it on return. Rows execute as the view owner, not the caller.
Source capture, destination storage and index roots retain exact generation and
incarnation fences. Concurrent reconciliation reuses genuine eligible keys and
compatible lineage; ordinary refresh owns a destructive successor.

Native snapshot/clone import runs on the authenticated isolated stage. Actual
destination identities are captured from production, not inferred from allocator
order or equal values. Final repair validates current owners, definitions and
membership before one checked publication; ordinary SQL gets no replay authority.

`ViewHost{tablecmds.TableHost}` implements view Host/StageHost. Only this family
builds it; decisions before a stage exists use a host without a stage.

## Triggers

Immutable definitions, dependencies and row effects share the normal journal and
commit/savepoint boundary. Definition locks and clone synchronization use actual
root/descendant identities; collisions fail before publication. Dependency
CASCADE grants no upward partition deletion authority. Core owns invocation
frames and balanced routine environments, not this DDL executor.

## Testing

External `viewcmds_test` tests use composed engines, controls and `enginetest`.
Schema-staged trigger fixtures stay with schemacmds; core tests cover firing and
retained producer ownership. Public SQL belongs to reference view/role/operator
packages under [docs/TESTING.md](../../../docs/TESTING.md).
