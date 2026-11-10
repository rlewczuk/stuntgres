# internal/engine/logical/testdecoding

Stock test_decoding output formatting below core. The package consumes immutable
logical events and historic type/schema snapshots; it owns no catalog, lock,
slot, decoder or session authority.

`New` creates independent option and per-transaction output state; `Shutdown`
releases it. Values use their captured type descriptors, identifiers use shared
parser quoting, and output owns emitted bytes. Streaming callbacks format real
decoder events rather than inferring them from committed data or SQL errors.
Cancellation and formatting errors propagate without successful partial output.

## Testing

Pure option/formatting units live here. Reorder/history tests belong to
`logical`, and composed staging/lifecycle tests to `core/enginetests/logical`.
