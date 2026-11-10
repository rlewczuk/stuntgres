# internal/engine/rolecmds

Role, privilege, ownership and database statement orchestration. `role`, `access`
and `authorization` own decisions/algorithms; core owns runtime publication.

## Layering

Imports core and decision packages, never the root or another family.
Composition is its production caller. Core retains authority capture, role locks,
RoleStore, transaction overlays and cross-family relation ownership publication.

## Entry points

Dispatch entry points enter with nothing held. They resolve/validate under
Engine.Mu, release it for password/callback work and waits, then recheck captured
principals, targets and authority before staging. Catalog and role changes join
the same journal and Finish/commit owner; failed work publishes no prefix.
`RoleHost{core.DDLHost}` implements role Host/StageHost; only family code builds
it and pre-stage decisions receive no fake stage.

## Database creation

Core's database registry owns exact source/destination incarnations and physical
admission. Creation copies committed persistent source contents into independent
catalog/storage authority, not private transactions, live TEMP ownership or
physical session resources. Permission, template and owner checks remain separate
from copying. Complete fallible preparation precedes atomic target registration.

## Database retirement

The executor delegates to core's exact-incarnation drain/retirement owner.
Read-only/transaction/role admission precedes destructive work. Callbacks and
terminal joins run outside metadata and replacement locks; failed prepublication
retirement cannot silently adopt a same-name successor.

## Testing

External `rolecmds_test` tests use composed engines and `enginetest` helpers.
Owner-private seams use test-only accessors only where exported APIs cannot
assert them. Public SQL belongs to `reference/roles` and `reference/object_grants`.
