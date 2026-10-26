# reference/extensions

Extensions and loadable code: the extension registry, installation, TEMP
installs, versions and removal, `pg_extension`, the available-extension views,
LOAD, pgcrypto, uuid-ossp and fuzzystrmatch, plus encoding conversions
(`pg_conversion`, which share the extension TEMP helpers). The other catalog
families are listed in [reference/catalogs](../catalogs/README.md).

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
- Expected data comes from published vectors, never from native output:
  RFC/NIST/Openwall/PostgreSQL regression vectors for pgcrypto, uuid-ossp and
  fuzzystrmatch.
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
- Mutable routine/provider names and schemas retain genuine OID/kind and
  extension associations; this does not promote lookup-only catalog identities
  to execution. Cross-family ALTER metadata and replay proof lives in
  [query_core](../query_core/README.md#routine-alter-proof), selected in both
  native and `REFERENCE=1` modes.

- pgcrypto digest, HMAC, installation and controls:
  `RUN='^TestReferencePgcrypto(Digest|HMAC|Installed|Overloads|Diagnostics|Consumers|Control|Replay|Forced|FeatureSmoke)'`
