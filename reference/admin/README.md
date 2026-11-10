# reference/admin

Dual-mode reference scenarios for administrative object comments. Catalog and
comment-inquiry coverage is shared with
[catalog_objects](../catalog_objects/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main(m)`.
Direct and prepared routes share one body over `p.Backends()`; prepare utilities
on the prepared route and keep catalog observations independent.

Family selector: `RUN='^TestReferenceAdminComment'`. Add `REFERENCE=1` for the oracle.