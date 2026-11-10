# reference/extension_hstore

Dual-mode scenarios for the `hstore` extension type and its consumers.
Other extension types belong to [extension_types](../extension_types/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main(m)`.
The shared client and catalog-probe rules in
[extension_types](../extension_types/README.md#testing) apply here.

- `hstore_helpers_test.go` owns scalar transport, metadata and full diagnostics.
  Family-local conversion, comparison and row helpers reuse those contracts.
- Compare unordered extraction results as bags with multiplicities.
- Native replay uses the shared fixture controls and actual emitted SQL on PostgreSQL.
  Retained rows/metadata and held statements follow their public driver lifetimes.

Family selector: `RUN='^TestReferenceHstore'`; add `REFERENCE=1` for the oracle.
