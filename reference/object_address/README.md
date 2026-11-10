# reference/object_address

Dual-mode object-address resolution and role-identity lifecycle scenarios.
Table authorization belongs to [roles](../roles/README.md#testing), alias input
to [type_resolution](../type_resolution/README.md#testing). Shared role/session
helpers belong to `reference/fixture`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.

- Helpers: `objectAddressScenario`, `objectAddressRun`, `objectAddressResolve`
  and `objectAddressWitness` (`object_address_helpers_test.go`); the inventory
  runner `objectAddressInventory` and TSV loader (`object_address_inventory_test.go`).
- Role lifecycle uses `fixture.RoleMembershipScenario` and `RoleMembershipCases`
  for notice-enabled direct/prepared actors, `fixture.AccessRole*` for membership
  inquiries, and the shared `fixture.RoleScenario*`, `RoleAttributes*` and
  `RoleTransaction*` helpers. Attribute facts and transaction route helpers remain
  local; no second fixture owns their sessions or cleanup.
- Shared role/default scenarios use the single moved `fixture.RoleAttributes*`,
  `fixture.RoleTransaction*` and independent `fixture.RolePassword*` helpers.
  They retain direct/fresh/held routes and real cluster/database selection;
  no scenario bodies or authority helpers are copied between packages.
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
- Transactional definition scenarios share `definitionScenario` and
  `definitionRoutes`: fresh serial autocommit/SQL/Go transactions, direct or
  genuinely held handles, exact identities/member dependencies and peer views.
  Register global method cleanup before scenario setup through the fixture.
  Wire scenarios use the real server fixture and pgconn's public frontend for
  effect-free Parse/Describe/Bind and held Execute/Sync recovery.
  Retirement schedules use `fixture.ObjectLockPending` for real dependency
  endpoint AS/AX protection in both publication orders, not transaction-ID name
  waits. Unique-index cancellation contexts vary only physical tuple coordinates.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Object addresses: `RUN='^TestReferenceObjectAddress'`
- Transactional definitions: `RUN='^TestReferenceTransactional(AccessMethodDefinitions|OperatorFamilyDefinitions|OperatorClassDefinitions|Definition)'`
- Role lifecycle: `RUN='^TestReferenceRole'`
- Database defaults: `RUN='^TestReferenceDatabaseSettings'`
- Membership inquiries: `RUN='^TestReferenceAccessRole'`

Address reconstruction resolves a current same-kind counterpart, never a
historical numeric OID. Native controls are in-process logical operations, not
physical durability or universal reconstruction.
