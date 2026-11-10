# internal/engine/core/contrib

Installed contrib runtime bodies below core. Pure function recipes remain in
`functions`, providers in `extension`, and typed values in `sqltype`/storage
owners. This package never imports core or calls the root engine.

## Contract

Runtime entry points implement `exec.ContribRuntime` through the supplied host.
Declaration validators authenticate current owner, epoch, genuine provider,
formal/result generations and routine authority at the defined demand boundary.
Public names and extension membership alone never select executable capability.
Captured metadata does not retain an invocation or provider handle.

Logical models consume actual immutable captured roots and return detached owned
values. They do not claim physical PostgreSQL pages, WAL or OS activity. Nested
SQL uses `exec.NestedSQL` within the caller's genuine admitted operation.

## Host and authority

`Host[A]` supplies current operation, I/O environments, private catalog/storage,
routine/write authority, scoped relation admission, owner cohorts and nested SQL.
Core implements it on `statementHost`. `Authority` is a value containing the
captured `access.View` and its `Environment`; the generic seam avoids per-row
boxing and does not transfer engine ownership.

## Locks

Calls enter with nothing held. `ReacquireEngine`/`ReleaseEngineLocked` delimit
metadata windows; owner/engine capture takes the transaction owner first.
Inside a locked window use the already-locked projection, never an API that
recursively takes `Engine.Mu`.

Multi-owner capture acquires a stable sorted owner cohort before `Engine.Mu`.
Lock waits, nested SQL, maintenance-owner checks and model computation run
unlocked. Each scoped relation contribution is released on every exit, and
post-wait readmission rechecks original target/storage/provider authority.

## Testing

Pure value/model units live here; engine-host and admission/lifecycle tests
live in core and its `enginetests`. Public extension SQL belongs to the owning
reference package.
