# internal/engine/role

Role, ACL and ownership DDL decisions below core. Pure role/ACL algorithms live
in `authorization`, and captured catalog enforcement in `access`. Statement
orchestration/publication belongs to `rolecmds` and core.

## What it owns

Focused pre-stage and stage Hosts supply captured access, namespace/name/type
resolution and private catalog changes. Plans resolve actual principals/objects,
compute grant/default/ownership/retirement deltas and stage complete candidates.
Bulk cleanup composes membership, ownership, policy and ACL decisions through
the same dependency and principal authority as ordinary DDL.

## Invariants

- Authority is a per-call captured `access.View` plus its Environment, never a
  cached grant or engine callback. Principal references, not names, establish
  identity; settings/default scopes use genuine database incarnations.
- Admission and target preflight precede waiting/effects; original descriptors,
  role roots and diagnostic order are retained through final validation.
- The package has no mutex, stage lifetime or publication authority. Host methods
  run under their documented caller-owned lock; waiting controllers are unlocked.
- Cleanup joins its role/catalog changes into one candidate. Failure/cancellation
  publishes no prefix and savepoints retain immutable predecessors.
- Transaction support is class-owned: one ACL or ownership path does not widen
  unrelated role lifecycle or utility policies.
- Messages, SQLSTATEs, details, hints and grantor choice are compatibility contracts.

## What stays in the engine

Core captures authority, owns role locks and pointer-stable `RoleStore`, installs
private role overlays and merges journals at Finish/commit. `rolecmds` supplies
hosts and utility lock windows; catalog/storage roots publish together.

## Testing

Pure decisions and fake-host units live here. Core/family tests exercise actual
journals, principal and publication fences. Public SQL belongs in `reference/roles`
and `reference/object_grants` under [docs/TESTING.md](../../../docs/TESTING.md).
