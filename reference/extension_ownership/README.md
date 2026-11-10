# reference/extension_ownership

Dual-mode scenarios for mutable extension membership, ownership, relocation and
survivors after detachment or origin retirement. Registry installation/removal
belongs to [extensions](../extensions/README.md#testing); ltree survivor corpora
belong to [extension_ownership_ltree](../extension_ownership_ltree/README.md#testing).

Explicit sepgsql simulation is query compatibility, not kernel enforcement or an
active PostgreSQL SELinux oracle. Ordinary/inactive PostgreSQL phases and
source-grounded active native simulation phases remain separately named.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.WithLegacyCryptoProvider()` and `fixture.WithAlterSystemDisabled()`.
Shared helpers live in [fixture](../fixture/README.md#helpers):
`OwnershipScenario`, `OwnershipSurvivorControls`, snapshots, physical TEMP owners,
released page images and tablefunc/template consumers. Remaining helpers are local.

- Bounded serial inventories share one genuine source graph across same-driver
  and independent-driver replay. Assert source behavior first, then repeat each
  destination's value, authority and dependency checks with its own recorder.
- Raw pools own Reset/Restore; recorded pools over the matching connector own
  SQL assertions and cleanup callbacks. A source recorder cannot prove a restored
  destination consumed values or closed its cursor.
- Retained-row witnesses open actual source resources before Reset and keep them
  through replacement; querying a restored catalog is not retention.
- Repair or roll back stock language/handler mutations before returning connections.
  Detach/restore mutated seeds before receiver teardown. Never execute unsafe
  mixed-provider or uninitialized callback combinations in definition-only probes.
- Global wrappers, servers and nonpublic schemas retire after scenario handles
  close and before roles. Cleanup uses separate bounded contexts, not refreshed
  scenario deadlines.
- Controls serialize logical declarations and data, not runtime cursors, physical
  pages, live sessions or library-loading authority.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Membership/ownership: `RUN='^TestReferenceExtensionOwnership'`
- Simulated provider: `RUN='^TestReferenceSepgsqlSimulation'`
- Installed ISN consumers: `RUN='^TestReferenceIsn'`
