# reference/type_resolution

Dual-mode scenarios for type/operator resolution, explicit casts, identifier and
OID-alias values, input diagnostics and text-search dictionary providers.
User-defined operators and cast declarations belong to
[operators](../operators/README.md#testing); catalog rows to
[catalog_objects](../catalog_objects/README.md#testing) and
[catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` passes an explicit
text-search file manifest to `fixture.Main(m)`. `name_external_test.go` and `diagnostics_postgres_test.go`
carry the `reference` build tag (PostgreSQL-only complements).

### Independent inventories

[comparison-ledger.md](comparison-ledger.md) records the evidence method and
inventory; `comparison_facts_test.go` encodes released facts. Cast and alias
inventories likewise use independent expected data, never native output.

Comparison Matrix/Between/PredicateErrors are sharded by semantic suffix.
Each shard runs separately; a shared-helper change affects every calling shard.
Scenario windows and bounded projections keep work within original deadlines.

Runtime cast coverage uses provider, direct-edge and generic-rule families,
not Cartesian products of every route. Keep typed storage, executable codecs
and metadata-only companions distinct; catalog presence grants no carrier API.

### Identity and resource ownership

- Generated OIDs are backend-relative. A dropped numeric OID can be reminted;
  retained bits may then have a different symbolic spelling.
- lib/pq raw OID/alias cells and type-name coverage differ from native. Assert
  transport before normalization; a missing pq name is not missing type identity.
- Do not issue SQL on an unread PostgreSQL streaming connection. Mutate behind
  retained rows through a separate physical owner.
- Forced Restore displacement retains an actual owner-held TEMP object;
  ordinary permanent destination spacers are discarded by replacement.
- Catalog probes bind independent identities before reciprocal joins and batch
  OID checks once per checkpoint, refreshing after replacement.
- Native replay consumes actual emitted SQL, so PostgreSQL-only discovery cannot
  supply the stream. Concrete-type lifetime fences remain separate from
  same-lifetime rename and current runtime resolution.
- Shared regoperator adapters have one owner in `fixture/regoperator.go`.
  Identity evidence and released TSV inventories live in `docs/design/`.

### Dictionary fixtures

The text-search manifest supplies identical real released resource bytes to
native registration and oracle installation. Ispell provenance/license is in
[testdata/ispell](testdata/ispell/README.md); malformed resources are explicit
manifest bytes, not production sample matchers or host-file fallbacks.

Source namespace/owner/option repair is registered before seed mutation and
restores captured identities before teardown. Output-gated cancellation proves
admitted SQL after lexization, not interruption inside a particular stemmer.
Native controls preserve symbolic resources and replay actual emitted bytes.

### Selectors

Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- Comparison shard template: `RUN='^TestReferenceComparison{Matrix,Between,PredicateErrors}<Suffix>$'`
  Expand to one exact name, such as `^TestReferenceComparisonBetweenStrings$`.
- Other comparisons: `RUN='^TestReferenceComparison'`
- Explicit casts: `RUN='^TestReferenceCastExplicit'`
- Cast families: `RUN='^TestReferenceCastImplemented'`
- Registry lookups: `RUN='^TestReferenceRegLookup'`
- OID aliases: `RUN='^TestReferenceReg'`
- Input validity: `RUN='^TestReferenceInputValidity'`
- Dictionaries: `RUN='^TestReferenceDictionary'`
- Ispell: `RUN='^TestReferenceIspell'`
- Snowball family template: `RUN='^TestReference<Language>Snowball'`
  English uses `RUN='^TestReferenceSnowball'`.
- Synonym/unaccent: `RUN='^TestReference(Synonym|UnaccentDictionary)'`
