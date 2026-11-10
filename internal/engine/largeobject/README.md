# internal/engine/largeobject

Immutable sparse large-object bytes and bounded generated-replay recognition.
The package owns no engine, session, mutex, allocator or publication authority.

## Values

`Value` owns signed logical EOF and private fixed-size pages. Missing pages and
short tails below EOF read as zero. Size/seek do not materialize gaps.

Writes copy the page index and touched pages only; untouched immutable backing is
shared. Truncation preserves the canonical endpoint page, including aligned/zero
EOF, without allocating sparse holes. Bounded reads construct owned output.
`Chunks` lends read-only stored slices; `Validate` checks canonical extent/EOF
and declared bounds. Failed operations leave predecessors unchanged.

## Ownership outside the package

Catalog descriptors own values. Core journals descriptor replacements, captures
the appropriate read/mutation frontier, enforces rights, owns descriptor cursors
and publishes metadata/content together. Savepoints restore old roots; there is
no second byte store. Dump and protected catalog projection visit actual stored
chunks rather than synthesizing logical-read zeros.

## Replay shapes

Replay helpers accept only bounded generated literal forms. Principal mapping,
codecs, exact statement-local replay authority and atomic installation belong to
engine/control hosts. Arbitrary nesting, parameters or expanded envelopes do not
gain authority from resembling generated SQL.

## Testing

Pure sparse-value/COW/validation and replay-shape units live here. Engine tests
cover cursor, journal and principal ownership; public byte/descriptor SQL lives
in `reference/large_objects` and `reference/object_grants`.
