# reference/information_schema

Dual-mode scenarios for information-schema object metadata. Grant, key and SQL
feature metadata belongs to [catalog_privileges](../catalog_privileges/README.md#testing).
Shared catalog identity, cleanup and replay rules live in
[catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`.

- Family-local helpers own exact independent row/domain vectors, raw adapter
  assertions and direct/fresh-prepared/held consumers. Shared catalog columns
  come from `reference/fixture/catalog_columns.go`.
- Provider inventories assert the complete represented native bag. PostgreSQL's
  larger inventory is not whole-cluster parity; metadata-only or empty providers
  still contribute their actual columns and relation kind.
- Builtin definition expectations are independent released observations across
  search-path shadows and identifier-quoting settings. Metadata reads must not
  execute routine/view bodies or foreign scan callbacks.
- Visibility is source-specific: do not substitute schema lookup, object EXECUTE
  or an adjacent information-schema view for the owning source's predicate.
- These views build their whole input before filtering. Batch builtin/installed
  inventories once per consumer/display mode, not one query or scenario per object.
- Retained native eager rows/metadata survive replacement, but held domain-result
  signatures obey the catalog-lifetime fence. Fresh consumers bind the restored lifetime.
- Native controls rebuild real declarations without serializing provider rows.
  Independent destinations use their own recorded SQL pool and recorder; raw
  pools own control operations. PostgreSQL replays actual emitted SQL.
- ACL mutation phases restore session authority and original ACLs under bounded
  cleanup, including assertion failure.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Object metadata families: `RUN='^TestReferenceInfo'`
- Complete column inventory: `RUN='^TestReferenceInfoColumnsPopulation$'`
- Builtin view definitions: `RUN='^TestReferenceInfoViewsBuiltins$'`
- Shared pg_views/pg_tables definitions: `RUN='^TestReferencePGTableViewsBuiltins$'`
