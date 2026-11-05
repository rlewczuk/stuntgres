# internal/engine/logical/testdecoding

Stock test_decoding output formatting below the runtime. This package imports
logical/sqltype/parser, never core, and owns no lock, catalog, slot or decoder.
New creates independent option and per-top-transaction output state. Shutdown
releases that state. Inputs are immutable capture-time values and schema/type
snapshots; all supported datum output goes through its exact type descriptor.
Identifiers use the parser's conditional quoting inventory. Old NULL attributes
are omitted, while new NULLs are explicit. Output owns the emitted bytes.

Streaming callbacks format real decoder-owned event blocks and child aborts;
they never infer events from committed contents or SQL errors. Empty-block and
transaction flags are separate. Two-phase and ordinary callbacks use the same
context and formatter. `skip-empty-xacts` can suppress an empty BEGIN/PREPARE,
but does not suppress COMMIT PREPARED or ROLLBACK PREPARED callbacks. Output
emptiness never decides journal lifecycle retention. Options are processed in
written order, boolean prefixes are not whitespace-trimmed, and binary output
remains sticky once requested.

## Testing

`make check RUN='^TestTestDecoding'` covers exact formatting, options and callback
output. `make check RUN='^TestReferenceTestDecoding'` selects the public family;
use small individual selectors and `REFERENCE=1` for pinned PostgreSQL proof.
