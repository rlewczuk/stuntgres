# reference/object_address

The four object-address functions `pg_identify_object`,
`pg_identify_object_as_address`, `pg_get_object_address` and
`pg_describe_object`: signatures, inverse round trips, NULL handling, typed
records/arrays and metadata, exhaustive builtin inventories (types, routines,
operators, casts, operator classes and families), owned objects (relation kinds,
columns and system attributes, constraints, defaults, rules, policies, roles,
namespaces, collations, conversions, extension members), diagnostics, stored
DEFAULT/CHECK consumers, held statements, transactions, native
Reset/Dump/Restore and cancellation. Related: reg* alias input in
`reference/type_resolution`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `objectAddressScenario`, `objectAddressRun`, `objectAddressResolve`
  and `objectAddressWitness` (`object_address_helpers_test.go`); the inventory
  runner `objectAddressInventory` and TSV loader (`object_address_inventory_test.go`).
- Inventory expectations are released data, never native output: types from
  `docs/design/regtype-pg18-inventory.tsv`, operators from
  `docs/design/regoperator-pg18-inventory.tsv`, routines from the REL_18_0
  `pg_proc.dat` tuples in `object_address_routine_facts_*_test.go`, casts and
  operator classes in `object_address_catalog_facts_test.go`.
- Check dynamic OIDs against their catalog or alias owner (for example a
  `::regclass` lookup), never against another server's allocator.
- Long inventories run as serial half-open subtests, each with its own
  `objectAddressScenario` (`inventory_<start>_<end>`, 512 routine facts per
  group). Never renew or extend a live scenario's context to fit more rows.
- TEXT columns report `Length() = (math.MaxInt64, true)` on both adapters.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Released inventories and owned objects: `RUN='^TestReferenceObjectAddressAuthority'`
- Diagnostics and parameter matrices: `RUN='^TestReferenceObjectAddressDiagnostic'`
- Strict evaluation and constant NULL: `RUN='^TestReferenceObjectAddressStrict'`
- Stored DEFAULT/CHECK consumers: `RUN='^TestReferenceObjectAddressStored'`

- Core behavior:
  `RUN='^TestReferenceObjectAddress(Signatures|RoundTrips|Nulls|Metadata|Arrays|MissingNames|QuotingVisibility|Stale|TypeArgumentOrder)$'`
- Held execution and lifecycle:
  `RUN='^TestReferenceObjectAddress(HeldExecution|Transactions|Lifecycle|Cancellation|FeatureSmoke)$'`
- Engine units for the address providers: `make check RUN='^TestObjectAddress'`.
