# XML2 scalar, table and transformation reference scenarios

Public dual-mode scenarios for XML2 scalar, relational and transformation
consumers. All use public SQL, native-control APIs and `reference/fixture`;
there is no package-private engine harness.

## Fixture contracts

- Direct, fresh-prepared and held routes share independently specified
  expectations. Metadata and complete diagnostic phases remain exact.
- XML node serialization is literal content, not normalized XML equivalence.
  Raw datum bytes, ordinary PostgreSQL text-wire bytes and SQL byte length may
  expose different boundaries; assert each explicitly rather than truncating
  native output for parity.
- XSLT uses actual source/stylesheet readers and compilation. Entity, stylesheet
  include/import, secondary-output and transform-time document access retain
  no-I/O safety witnesses.
- Native replay uses shared fixture controls, destination-owned recorded SQL
  pools and actual emitted SQL on PostgreSQL.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main`.
Family selector: `RUN='^TestReferenceXML2'`; add `REFERENCE=1` for the oracle.
