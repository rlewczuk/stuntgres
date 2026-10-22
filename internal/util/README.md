# util

General-purpose data structures shared by the engine. The package depends only
on the standard library and on nothing else in this module, so any package may
use it. Put a structure here only when it is independent of SQL and engine
concepts.

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
- **Transients.** `m.Transient()` returns a single-goroutine editor for many
  changes. Nodes it creates are edited in place by later changes; nodes shared
  with other versions are copied on first change. `Persistent()` returns the
  result and ends the transient: any later use panics, as does changing it
  while iterating it. The source map never changes.

The trie is canonical: no node other than the root holds exactly one entry
and no children. Keys whose whole 64-bit hashes collide meet in a collision
node after thirteen levels.

## Testing

Unit tests are white-box: they check structural invariants (bitmaps against
slice lengths, hash prefixes along each path, canonical shape, sizes) after
every operation, compare against a builtin map, and force collisions and deep
shared prefixes with test-chosen hashes.

```sh
make check RUN='^(TestPersistentMap|FuzzPersistentMap)'
make check RUN='^TestPersistentMapConcurrentReaders$' RACE=1
```

The fuzz seed corpus runs with the ordinary tests. To fuzz, run
`go test -run '^$' -fuzz '^FuzzPersistentMap$' -fuzztime 60s ./internal/util`.
Benchmarks against builtin maps at catalog scale are opt-in:
`go test -run '^$' -bench PersistentMap -benchmem ./internal/util`.
