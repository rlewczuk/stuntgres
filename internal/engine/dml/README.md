# internal/engine/dml

Mutation executors and command-local write algorithms over captured typed inputs.
Core owns admission, source capture, write stages and final row/catalog/index
publication. Decisions and records from lower packages are reused rather than
introducing another transaction path.

## Hosts

`Host` embeds `exec.Host`, `index.WriteHost` and `fdw.WriteRuntime` and adds
command, stage, row-lock, disclosure and trigger capabilities. `FinishHost` adds
constraint settlement and statement-effect exposure. Core implements both on the
current statement; no host call borrows a finished scope.

## Command state

`Command` owns target/source frames, insert/conflict state, indexes, partition and
inherited targets, view-write lowering and RLS for one actual command. Prepared
bindings keep typed recipes and original occurrences, never candidates, trigger
frames or mutable arbiter state. Runtime contexts come from the current unlocked
invocation and are forwarded through scans, predicates, projection and COPY pulls.

## Entry points

Write entry points consume captured inputs, use shared insertion/update/deletion
loops, and return results to the existing statement completion owner. COPY
cursors own decoding/value/error context while `copyfmt` owns framing. Foreign
DML reuses enlisted participants and immutable request snapshots.

## Execution order

Executors enter with nothing held. They freeze admitted source/candidate ownership
before mutation, preserve native predicate/RHS/trigger/constraint demand order,
and wait through Host capabilities. Post-wait acceptance checks the exact
physical version/storage and current authority, not RowID or equal values alone.

Accepted predecessor/successor effects retain command identity. Nested completion
settles its causal actions/checks to a fixed point without replaying completed
actions; root completion drains remaining work. Failed stages and enclosing
savepoint/exception rollback restore private roots through core's checkpoint.

## Invariants

- No package-owned engine state or mutex. All waits and callbacks run unlocked.
- Pass per-row values across host methods by value; pointers to stack probes or
  one-row arrays must not escape through an interface on every row.
- Error order, SQLSTATE, detail disclosure and COPY line/context behavior are
  compatibility contracts. Failure-only diagnostic rendering may allocate;
  ordinary row processing reuses buffers and state.
- RLS SQL authority, disclosure eligibility and physical target identity remain
  separate; a recipe or cached selection never grants current access.
- Unique/exclusion/FK checks use actual private writes and exact physical
  versions. Source capture and later checked current versions remain distinct;
  fresh same-RowID siblings are not inferred successors.
- Graph/preflight receipts authenticate their original catalog lineage and
  touched descriptors. Final publication checks remain independent of shortcuts.

## What stays in the engine

Statement dispatch, captured data views, owner locks, stage windows, writable-CTE
orchestration, row/index installation and transaction/trigger effect lifetime.
No executor installs committed roots independently of Finish.

## Testing

Pure mutation decisions/cursor units live here; composed host and lifecycle tests
live in core and its `enginetests/writes`. Public SQL belongs to reference
packages. Assert accepted versions, roots and failed publication separately from
SQL outcomes; see [docs/TESTING.md](../../../docs/TESTING.md).
