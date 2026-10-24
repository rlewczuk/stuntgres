# Engine split: `internal/engine/view`, fifth round

Session prompt for linear item 159 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the view README](../internal/engine/view/README.md). Sizes were
measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 159 — RLS policy import and materialized snapshot rows

Needs —. About 0.22k lines move, mostly to `view` and a little to `access`.

**Moves.**
- `sameRLSSelection` (`rls_fence.go`, already clean) goes to `access`, which
  owns the RLS selection type.
- `checkPolicyAuthorityLocked` (`rls_fence.go`) never uses its `Engine`
  receiver. It becomes `view.CheckPolicyAuthority(v, env, consumer,
  policy)`, built on the existing `view.CheckRowMarks` and
  `stored.ViewHasRowMarks`.
- The pure part of `importPolicyClauseLocked` (`rls_runtime.go`, about
  L98-147 and L152-187) becomes `view.ImportPolicyClause`. It returns the
  node, the binding, the source closure and the refs.
- From `materialized_snapshot.go`: `materializedSnapshotConstructorProviders`,
  the row-evaluation loop of `importMaterializedSnapshotRow` and its
  resolver-to-environment copy. `view` already owns the snapshot format and
  may import `regobj`.
- Inline `rowSecurityInputLocked` (`rls_base.go`), a one-caller forwarder.

**Stays root.**
- For the policy import: filling storage, the lock waits, the authority
  refresh, the snapshots and source preparation.
- For the materialized snapshot: the Restore operation, its binder and
  `Engine.mu`.

**Callers and locks.** The RLS pieces run under `Engine.mu` and take no
lock. The snapshot code runs with nothing held.

**Risks.**
- RLS selection is on a per-statement path: return structs and add no
  closures or allocations.
- The snapshot rows and the Dump output stay byte-identical.

**Tests.**
- Root units: `materialized_snapshot_lossless_test.go`,
  `geometry_conversion_mixed_replay_test.go` and the row security tests that
  name the moved functions.
- Quick and `REFERENCE=1` selections: `TestReferenceRowSecurity`,
  `TestReferenceDriverBatchPolicy` and one materialized view Dump/Restore
  selection (grep `TestReferenceMaterialized`).

**Docs.** Update the view README (RLS policy import, snapshot rows) and the
access README (`SameRLSSelection`).

## Status

- 159: done (`b6a2a87a0`, `b4873d3c2`, then the docs commit).
  - Moved: `access.SameRLSSelection`; `view.CheckPolicyAuthority` and
    `view.ImportPolicyClause` (`PolicyClauseImport`) in `policy_import.go`;
    `view.MaterializedSnapshotEnvironment`,
    `view.EvaluateMaterializedSnapshotRows` and the constructor-provider check
    in `materialized_snapshot_rows.go`. `rowSecurityInputLocked` is inlined.
  - Deviations: `CheckPolicyAuthority` takes `(a, env, consumer, policy)`
    with the policy's user already set as `a`'s current user; the root
    callers set it and keep returning the switched `AuthorizationView`. It
    inlines `AuthorizationView.CheckViewRowMarks` as `CheckRowMarks` behind
    `stored.ViewHasRowMarks`. `ImportPolicyClause` also sets each source's
    principal; the root fills source storage after the closure walk, which
    never reads it. `MaterializedSnapshotEnvironment` includes the binding
    shape check. `view` now imports `regobj`.
  - Escape analysis: the import's heap move changed from `binding` to the
    returned struct (one each); the authority view converts to
    `access.Environment` once per policy check instead of per column check.
  - Tests: none moved or rewritten; the root units still call
    `importMaterializedSnapshotRow`.
  - For item 167: `6d78b74d5` (trigger rename and dependency DDL) added
    `trigger_lifecycle.go` (0.15k) to the root.
