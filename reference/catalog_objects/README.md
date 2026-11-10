# reference/catalog_objects

Dual-mode scenarios for schema-object catalogs and object metadata.
Shared catalog rules and neighboring ownership boundaries are in
[catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules.
`TestMain` uses `fixture.Main(m, fixture.WithLegacyCryptoProvider())`.

- Expected inventories come from released data, not native output. Shared operator
  facts live in `docs/design/pg-operator-pg18-supported.tsv` and
  `docs/design/regoperator-pg18-inventory.tsv`; update all consumers when admission changes.
- Catalog presence is metadata authority, not permission to execute a scalar
  routine, access method or physical callback.
- Generated OIDs are checked within each backend through independent identities
  and graph relationships, never cross-backend numeric goldens.
- Raw catalog visibility and filtered information-schema visibility are distinct.
  Foreign metadata reads must not invoke remote provider callbacks.
- Native controls retain opened rows/metadata and replay actual emitted SQL.
  Use source-only allocation history or retained TEMP objects to force remapping;
  permanent destination spacers are discarded by isolated Restore.
- Notice-enabled scenarios assert complete notice records. Current reader
  authorization applies independently of underlying object permissions.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Namespaces: `RUN='^TestReference(PGNamespace|Schema)'`
- Object comments: `RUN='^TestReference(PGDescription|PGShDescription|Comment|TransactionalComment)'`
- Operator and aggregate catalogs: `RUN='^TestReference(PGOperator|PGAggregate)'`
- Access-method catalogs: `RUN='^TestReference(PGAM|PGOpclass|PGAmop|PGAmproc|PGOpfamily)'`
- Foreign metadata: `RUN='^TestReferencePGForeign(Server|Table)'`
- Visibility: `RUN='^TestReference(Visibility|SearchVisibility)'`
