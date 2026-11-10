# Simulated HBA diagnostic facts

`hbarules` owns independent nullable Rule facts and a stable Driver-wide Source.
It imports only the standard library and carries no SQL descriptors, parser,
authentication, filesystem or network state.

`Source.Set` copies the batch, pointees and nonnil string slices before publishing
one immutable Snapshot. NULL, explicit empty and zero remain distinct; order and
duplicates are retained. Callers may mutate inputs only after Set returns.

Capture retains existing immutable backing under a short RLock. The zero/nil
source is empty; borrowed Rules are read-only and retained snapshots survive
replacement/clear. Publication is Set's linearization point; copying precedes
its lock and writers never enter Driver/Engine locks.

All engines/templates/logical targets of one Driver share the destination holder.
Reset/Restore retain it; Dump excludes facts. Separate Drivers are independent.
Core checks current source/routine authority before capture and materializes typed
owned values after unlock, without retaining producer permission.

## Testing

External component units live in `enginetests`; Driver adapter and core capture
tests cover composed holder ownership. Public SQL lives in `reference/catalogs`.
