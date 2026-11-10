# reference/extension_types

Dual-mode scenarios for installed ISN, ltree, intarray, seg and lo type families.
Separate owners are [hstore](../extension_hstore/README.md#testing) and
[cube/earth](../extension_cube/README.md#testing). Mutable membership belongs to
[extension_ownership](../extension_ownership/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`.
- Family-local helpers own installed scalar transport, metadata and complete
  diagnostics. Shared lifecycle/control assertions use the fixture's recording
  and public native-control seams.
- Bound catalog inputs to actual installed scalar/companion/codec identities
  before joining. Repeated lifecycle checks must not multiply unrelated catalogs.
- Each serial control phase owns its bounded scenario, retained cursor and
  genuine dump. Failed Restore preserves incumbent identities and values.
- Every destination uses its own recorded SQL pool/recorder; raw pools are for
  control operations. Register authorization/schema cleanup before replay.
- Stored consumers retain genuine installed identity through rename or detachment.
  Held concrete-type signatures obey the catalog-lifetime fence; builtin-signature
  consumers reanalyze against current installed types.

Client quirks:

- lib/pq returns textual values as `[]byte` with ScanType `any`; native returns
  strings. Assert raw cells before normalization.
- lib/pq reports an empty `DatabaseTypeName` for types missing from its OID table
  (MACADDR8, multiranges, extension types such as `cube`); native reports the
  concrete name. Assert the raw codec independently of the name.
- A typed-nil `[]byte` bound to TEXT is SQL NULL on both backends; nonnil empty
  bytes remain empty input. FLOAT8 retains its separate native empty-input
  versus lib/pq NULL transport policy.
- lib/pq fails on an oversized ErrorResponse; obtain such a server diagnostic on
  PostgreSQL with `pg_input_error_info`.
- A pre-canceled lib/pq read can race database/sql's cancellation watcher: check
  the available metadata and ordered prefix, accept `context.Canceled` or
  `driver.ErrBadConn`, and require full rows only on clean EOF. Native
  pre-cancellation returns `context.Canceled` without a cursor. Cache
  `ColumnTypes` while the cursor is open to check descriptors after EOF/Close.

Rules:

- Catalog identity probes first read the backend's actual identities from single
  catalogs, then bound every joined catalog input by them (derived operands before
  the join). Never use LIMIT, cached OIDs or expectation-derived catalog data.
  Compare allocated OIDs through relationships, never numerically across backends.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.

### Family selectors

Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- ISN types: `RUN='^TestReference(Ean13|Isbn|Ismn|Issn|Upc)'`
- Ltree types: `RUN='^TestReference(Ltree|Lquery|Ltxtquery)'`
- Intarray: `RUN='^TestReference(QueryInt|Intarray)'`
- Seg: `RUN='^TestReferenceSeg'`
- Lo: `RUN='^TestReferenceLo'`
