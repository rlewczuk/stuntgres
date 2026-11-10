# reference/extension_ownership_ltree

Dual-mode ltree extension-survivor scenarios. Other ownership families belong to
[extension_ownership](../extension_ownership/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.WithLegacyCryptoProvider()` and `fixture.WithAlterSystemDisabled()`.
`OwnershipScenario`, survivor controls and snapshots have one shared owner in
`reference/fixture/extension_ownership_helpers.go`.

Partition corpora into bounded serial inventories. Each owns a genuine source
graph and feeds both same-driver and independent-driver destinations under its
original deadline. Controls use each destination's recorded SQL pool and recorder,
retaining source rows and nominal metadata across replacement.

Family selector: `RUN='^TestReferenceExtensionOwnershipLtree'`;
add `REFERENCE=1` for the oracle.
