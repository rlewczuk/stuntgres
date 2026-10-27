# reference/extensions

Extensions and loadable code: the extension registry, installation, TEMP
installs, versions and removal, `pg_extension`, the available-extension views,
LOAD, pgcrypto, uuid-ossp and fuzzystrmatch, plus encoding conversions
(`pg_conversion`, which share the extension TEMP helpers). The other catalog
families are listed in [reference/catalogs](../catalogs/README.md).

`pg_conversion` exposes the [eight-column catalog contract](https://www.postgresql.org/docs/18/catalog-pg-conversion.html)
from implemented conversion descriptors, including off-search-path nondefault
objects. Conversion identity, namespace and owner remain distinct from the
encoding pair and procedure identity; rejected definitions publish no catalog row.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package; pgcrypto's BF/DES/CAST scenarios need the legacy OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `pgcryptoCase`, `uuidOsspCase`).
- Conversion helpers (`conversion_helpers_test.go`) own scenario setup, registered
  handler declarations, metadata and real byte assertions. `conversionHideSeed`
  registers repair before moving a default; owner-transaction assertions use the
  owner executor, never `b.DB`. Direct/held autocommit, SQL BEGIN and Go BeginTx
  routes couple dispatch to private metadata, transition/savepoint restoration,
  current authority and peer isolation. DEFAULT checks distinguish serial
  visible conflicts from legal moved/concurrent distinct-name definitions.
  Conflict workers use shared lock observers and are always joined; native
  controls use unwrapped pools for committed-only Dump and fresh-driver Restore.
  Catalog assertions use independently captured namespace/owner identities and
  raw OID handling; held full-row reads preserve metadata checks even for empty
  results. Rejected-creation assertions check publication and builtin identity
  after each execution route. Oracle provenance follows the
  [fixture policy](../fixture/README.md#postgresql-oracle-policy-and-provenance).
- Expected data comes from published vectors, never from native output:
  RFC/NIST/Openwall/PostgreSQL regression vectors for pgcrypto, uuid-ossp and
  fuzzystrmatch.
- BRIN pageinspect images are independently assembled from released native
  layouts in `pageinspect_brin_fixture_test.go`; descriptor tables stay empty.
  The shared scenarios exercise the identical bytes in both modes, including
  compressed inline datums, typed errors and executable stored/control identities.
  A shared metadata checker asserts adapter-specific scan types and both native
  nullability flags independently of cursor contents, including replayed views.
  Literal catalog identities, retained prepared consumers and unchanged-byte
  output-setting transitions supplement the stored decoder results.
- LOAD (`load_fixture_test.go`) checks missing-file candidates inside the oracle
  container only and requires `dynamic_library_path=$libdir` and an installed
  `plpgsql.so`; unexpected files are failed preconditions, never skips.
- FIPS mode needs one process per mode:
  `env GODEBUG=fips140=off make check RUN='^TestReferencePgcryptoRandomFIPSMode$'`,
  then the same with `fips140=on`.
- Frame names matched by tests (active-cancel admission): `sqltype.binaryEncodingPass`,
  `pgcryptoSHA` and `pgcryptoBcrypt` in `internal/sqltype`. Update the tests when
  renaming them.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Encoding conversions: `RUN='^TestReferenceConversion'`
- LOAD: `RUN='^TestReferenceLoad'`
- Extensions (registry, TEMP, removal, versions): `RUN='^TestReferencePGExtension'`, `RUN='^TestReferenceExtensionTemporary'`, `RUN='^TestReferenceExtensionDrop'`, `RUN='^TestReferenceExtensionVersion'`, `RUN='^TestReferenceExtension'`
- Available-extension views: `RUN='^TestReferenceAvailableExtensions'`
- Installed dictionary metadata: `RUN='^TestReferenceTSDict'`
- Installed template metadata, diagnostics, lifecycle and native controls: `RUN='^TestReferenceTSTemplate'`
- pgcrypto: `RUN='^TestReferencePgcryptoAES'`, `RUN='^TestReferencePgcryptoBF'`, `RUN='^TestReferencePgcryptoPassword'`, `RUN='^TestReferencePgcryptoRandom'`
- uuid-ossp: `RUN='^TestReferenceUUIDOssp'`, `RUN='^TestReferenceObjectGrantUUIDOsspAuthority$'`
- fuzzystrmatch: `RUN='^TestReferenceDaitchMokotoff'`, `RUN='^TestReferenceLevenshtein'`, `RUN='^TestReferenceMetaphone'`, `RUN='^TestReferenceSoundex'`
- tablefunc Gaussian sources: `RUN='^TestReferenceTablefuncNormal'`
- BRIN pageinspect: `RUN='^TestReferencePageinspectBrin'`
- tablefunc hierarchy sources: `RUN='^TestReferenceConnectby'`. Shared helpers
  own caller-record metadata, full SQL diagnostics, retained statements and
  explicit native-control phases. Renamed members use current names or stored
  view identities; held original-name SQL follows ordinary reparse semantics.
  `RawSQLAndQuoting` covers read-only utility rejection with unchanged settings,
  catalog and data, unsupported SPI transaction/frontend-COPY distinctions and
  multiple SELECT last-result semantics.
  Nested derived, CTE, scalar and stored-view locking witnesses cover all four
  strengths directly and prepared against conflicting held tuple locks with
  NOWAIT; exact nonvolatile diagnostics distinguish rejection from row-lock
  execution. An unused locking CTE remains a successful read-only control.
  Actual PG18.6 diagnostics for weaker enclosing/stronger stored marks include
  plain `SELECT` when a planned reference/COPY marker precedes the locking
  markers. Nested-view forms, earlier weak/later unrelated strong ordering, and
  plain versus ordered view boundaries have separate direct/prepared witnesses.
  Reversed locking-clause versus FROM order is exercised directly and through
  stored/nested views. A parent-explicit/earlier-child locking control prevents
  comparing restarted first-mark orders across query levels.
  `AuthorityAndPrepared` traverses a
  recursively protected policy relation under target-only DML with private
  inserts/deletes; its native phase observes a scheduler wait on the dynamically
  named source before canceling SELECT and INSERT, checking no exposed rows,
  atomic target preservation, and subsequent demand. Phase-specific sorting and
  accumulated-output DML publication proofs live in the composed core owner tests.
  `Smoke` includes cyclic atomic INSERT and successful bounded INSERT.
- Mutable routine/provider names and schemas retain genuine OID/kind and
  extension associations; this does not promote lookup-only catalog identities
  to execution. Cross-family ALTER metadata and replay proof lives in
  [query_core](../query_core/README.md#routine-alter-proof), selected in both
  native and `REFERENCE=1` modes.

- pgcrypto digest, HMAC, installation and controls:
  `RUN='^TestReferencePgcrypto(Digest|HMAC|Installed|Overloads|Diagnostics|Consumers|Control|Replay|Forced|FeatureSmoke)'`
