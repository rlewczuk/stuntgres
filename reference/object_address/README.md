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

Role identity lifecycle also lives here: CREATE/ALTER/DROP ROLE and USER,
attributes/passwords/admission, trusted connectors, membership ADMIN/INHERIT/SET,
SET ROLE and SESSION AUTHORIZATION, retained identities, transactional role
publication, native controls, and shared role rename and persisted-default
mutation/lifetime scenarios. Ownership, grants and RLS remain in
`reference/roles`.

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
  Special-role cases distinguish explicit self selection from NONE, captured
  startup RESET, definer reads/capture and restricted writes, and local/savepoint
  restoration without deriving setting text from effective identity.
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
  Single-key index consumers assert the complete `pg_depend` class-edge bag and
  exact `pg_class.relam`, retaining the captured index OID through default
  multiplicity, explicit selection, INSERT and recovery. Native control phases
  use actual emitted bytes and preserve incumbent/private work on failed replay.
  `TestReferenceTransactionalDefinitionExactDiagnostics` uses incomplete concrete
  enum DEFAULT classes in distinct concurrent transactions to reach exact
  ambiguity before executable support validation. Direct and retained handles
  report the visible or qualified base enum for enum/domain inputs as the current
  search path changes, with full diagnostics and no index/dependency effects.

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
- Transactional method, family and class definitions:
  `RUN='^TestReferenceTransactional(AccessMethodDefinitions|OperatorFamilyDefinitions|OperatorClassDefinitions|Definition)'`.
- Role attributes and admission: `RUN='^TestReferenceRoleAttributes'`
- Role transaction publication and lifetime: `RUN='^TestReferenceRoleTransactional'`
- Shared role identity/default mutations: `RUN='^TestReferenceRole(Rename|Settings)'`
- Trusted connectors: `RUN='^TestReferenceRole(Trusted)?Connector'`
- Membership and role switching: `RUN='^TestReferenceRole(Membership|Switch)'`
- Membership inquiry: `RUN='^TestReferenceAccessRole(Membership|Privileges|NamesAndOIDs)$'`
- Role DDL and boundaries:
  `RUN='^TestReferenceRole(Creation|Authorization|AuthenticatedSessions|ExcludedCommands|DropConditionalBoundary|TransactionMutationBoundary)$'`
- Retained identities and native controls:
  `RUN='^TestReferenceRole(Retained|MigratedSmokeControls|ControlsOwners|RestoreOwnerReset|PreparedSetup)'`
