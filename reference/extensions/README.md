# reference/extensions

Dual-mode scenarios for the extension registry, installed providers, loadable-code
boundaries and encoding conversions. Mutable extension membership belongs to
[extension_ownership](../extension_ownership/README.md#testing); shared catalog
rules live in [catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.Main(m, fixture.WithLegacyCryptoProvider(), fixture.WithAlterSystemDisabled(), fixture.WithPasswordCheck())`.
Physical oracle options do not implicitly register native capabilities.
Family setup and assertions live in `*_fixture_test.go` or `*_helpers_test.go`.

- Expected data comes from published RFC/NIST/Openwall/PostgreSQL vectors, not
  native output. Random outputs use independently specified properties, not
  cross-backend byte equality.
- Sampling asserts occurrence-bounded membership and uniqueness. Full-population
  bags can be exact; heap traversal, block distribution and elapsed sample counts
  are not portable. Query liveness guards are not precise execution budgets.
- Physical PostgreSQL statistics and native compact estimates have independent
  expectations. Validate types, accounting equations and bounds without treating
  simulated pages as physical inspection.
- Pageinspect fixtures independently assemble released byte layouts and checksum
  vectors. Shared consumers use the same bytes; expectations never come from
  production decoders or acquired pages. Unsafe C layouts belong only in native
  safety units, not the oracle.
- Keep real visibility horizons bounded: release reader transactions before
  conflicting DDL or maintenance. Drain/close retained streaming rows and return
  their borrower before issuing more SQL on that connection.
- Physical TEMP retirement uses the shared fixture owner and observed backend exit.
  A schema reset or DISCARD does not unload a library; unloaded-provider cases
  need fresh physical sessions.
- Restore stock catalog ACLs and dependency-aware grants before role cleanup.
  Register repair before moving hidden conversion defaults; owner-private
  assertions use the owner executor.
- Native controls replay actual emitted SQL on PostgreSQL and preserve symbolic
  identities, not runtime handles, physical pages, WAL or shared-library ABI state.
- LOAD checks files only inside the oracle container and treats unexpected files
  as failed preconditions.
- FIPS mode requires a separate process per mode (`GODEBUG=fips140=off` or `on`).
  Active provider cancellation uses typed work-class admission through the fixture.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Registry: `RUN='^TestReference(PGExtension|AvailableExtensions|Extension)'`
- Conversions: `RUN='^TestReferenceConversion'`
- Module loading: `RUN='^TestReferenceLoad'`
- Pgcrypto: `RUN='^TestReferencePgcrypto'`
- Pageinspect: `RUN='^TestReferencePageinspect'`
- Installed statistics: `RUN='^TestReferencePgstat'`
- Sampling: `RUN='^TestReferenceSystem(Rows|Time)'`
- Tablefunc: `RUN='^TestReference(TablefuncNormal|Connectby)'`
- Refint: `RUN='^TestReferenceRefint'`
