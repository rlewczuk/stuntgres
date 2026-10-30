# util

General-purpose data structures shared by the engine, and the allocation
probe of allocation-budget tests. The package depends only on the standard
library and on nothing else in this module, so any package may use it. Put a
structure here only when it is independent of SQL and engine concepts.

`ExecutionPause` is a SQL-independent, context-scoped one-shot work suspension,
keyed by an opaque work-point string. Attachment carries a request only.
`ActivateExecutionPause` grants detached execution permission;
`DeactivateExecutionPause` strips that permission at metadata/binding reentry
without losing the request. Unconfigured activation returns the original
context, including nil for an absent optional evaluation context; deactivation
also preserves nil. Neither supplies a Background context or cancellation
authority. `ExecutionCheckpoint` still requires a non-nil effective operation
context; it allocates nothing and starts no goroutine.
A private mutex linearizes first entry with early Resume; it is released before
waiting on Resume or the effective context's Done. The pause never owns or
replaces cancellation, deadlines, causes or context values. Callers must hold
no runtime locks when activating work that may reach a checkpoint.
`HasExecutionPause` observes only attachment, without activating or invoking
anything. Runtime owners use it to release their own reservations even when
no application workload policy is attached; checkpoints never perform owner
callbacks or obtain catalog/publication authority.


## PersistentMap

`PersistentMap[K comparable, V any]` is a persistent (immutable, versioned)
hash map: a CHAMP-style hash array mapped trie with 32-way bitmap nodes.

- **Versions.** A `PersistentMap` value is one version. `Set` and `Delete`
  return a new version; the receiver never changes. Versions share every
  unchanged node, so keeping or copying a version is O(1), and an update copies
  only the path to the changed entry: O(log₃₂ n) nodes, three for a few
  thousand entries.
- **Concurrency.** A version is never written after it is created, so any
  number of goroutines may read it without locks. Publishing a version to other
  goroutines needs the usual happens-before edge (a mutex, channel or atomic
  pointer), as for any value.
- **Reads.** `Get` and `Len` are O(log₃₂ n) and O(1). `All` walks the version
  it was called on: reassigning the variable that holds the map during the
  loop, for example `m = m.Set(k, v)`, does not affect the iteration, so every
  entry present at the start is yielded exactly once with its value at the
  start. Iteration order is unspecified. It is stable for one version within a
  process, and the hash seed is chosen per process, so no order can be relied
  on across runs.
  `Every(predicate)` walks the same retained version directly, without an
  iterator adapter. It calls the predicate synchronously, stops on the first
  false result, and returns true for an empty map without calling it.
- **Keys.** Keys are compared with `==` and hashed with
  `hash/maphash.Comparable`, the runtime's map hasher. Hashing allocates nothing
  for keys made of numbers, strings, and arrays or structs of them. A key that
  is not equal to itself, such as a NaN, can be stored but never found, as in a
  builtin map.
- **Bulk construction.** `PersistentMapFrom(seq)` and `PersistentMapOf(map)`
  build a map in one pass: they hash every entry once, partition the entries
  level by level as the trie splits them, and lay nodes, entries and child
  slices out in a few large allocations. Later entries for the same key win.
  The result has the same canonical shape as one built by `Set`.
- **Batches.** `SetAll(seq)` and `DeleteAll(keys)` apply many changes in one
  pass; each node shared with the receiver is copied at most once.
- **Value maps.** `PersistentMapValues(m, f)` builds a map with `m`'s keys and
  values `f(k, v)` by copying `m`'s trie, hashes and keys into a few large
  allocations, without hashing or partitioning; the result shares nothing with
  `m`. It is the cheap way to build many maps over one key set.
- **Transients.** `m.Transient()` returns a single-goroutine editor for many
  changes. Nodes it creates are edited in place by later changes; nodes shared
  with other versions are copied on first change. `Persistent()` returns the
  result and ends the transient: any later use panics, as does changing it
  while iterating it. The source map never changes.

The trie is canonical: no node other than the root holds exactly one entry
and no children. Keys whose whole 64-bit hashes collide meet in a collision
node after thirteen levels.

## AllocationsPerCall

`AllocationsPerCall(calls, f)` reports the heap allocations and bytes of one
call of `f` for allocation-budget tests (see
[docs/TESTING.md](../../docs/TESTING.md#allocation-checks)): one warm-up call,
then three samples of `calls` calls at `GOMAXPROCS(1)`, each divided with
integer division like `testing.BenchmarkResult`; the smallest sample wins.

## Testing

Unit tests are white-box: they check structural invariants (bitmaps against
slice lengths, hash prefixes along each path, canonical shape, sizes) after
every operation, compare against a builtin map, and force collisions and deep
shared prefixes with test-chosen hashes.

```sh
make check RUN='^(TestPersistentMap|FuzzPersistentMap)'
make check RUN='^TestPersistentMapConcurrentReaders$' RACE=1
```

Pause ownership and allocation tests: `make check RUN='^TestExecutionPause'`;
add `RACE=1` for concurrent entry, release and child cancellation.


The fuzz seed corpus runs with the ordinary tests. To fuzz, run
`go test -run '^$' -fuzz '^FuzzPersistentMap$' -fuzztime 60s ./internal/util`.
Benchmarks against builtin maps at catalog scale are opt-in:
`go test -run '^$' -bench PersistentMap -benchmem ./internal/util`.
