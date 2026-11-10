# reference/roles

Dual-mode scenarios for table authorization, ownership, default privileges,
predefined roles, RLS and logical-slot lifecycle. Non-table grants belong to
[object_grants](../object_grants/README.md#testing); role identity, attributes,
membership and defaults to [object_address](../object_address/README.md#testing).
Shared role helpers belong to `reference/fixture`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses
`fixture.WithLogicalWAL()` for both backends.

- `RoleMembershipScenario` and `RoleMembershipCases` own notice-enabled pinned
  actors; family wrappers retain their own setup. Inquiry helpers use
  `fixture.AccessRole*`; default privileges use local `dpScenario`.
- Register bounded schema/object cleanup before role teardown. Ownership cleanup
  runs DROP OWNED for surviving actors and drops nonpublic schemas after handles
  close. Restore shared catalog/builtin ACLs on every exit, removing grant
  dependencies before repairing raw ACLs.
- Query generated identities independently; compare dependency Detail lines as
  bags with exact multiplicity while keeping every other diagnostic field exact.
- PostgreSQL held plans may retain planning-time permission results; native held
  execution checks current authority. Specify outcomes independently.
- Logical native controls preserve data and raw grantor/options, not live
  replication workers or physical WAL. Restore admission is distinct from
  ordinary SQL authorization; rejected controls remain atomic.
- Stock logical decoding uses real producers and bounded slot/session cleanup.
  Dynamic XIDs/LSNs/timestamps are relational or clock-bracketed expectations,
  never fixed physical values. Streaming synchronization uses observed work,
  not elapsed sleeps.
- File and physical-facility scenarios keep native capability rejection separate
  from actual PostgreSQL outcomes. Restricted actors do not inspect protected
  settings merely to construct helper expectations.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Ownership: `RUN='^TestReferenceRoleOwnership'`
- Table grants: `RUN='^TestReferenceRole(Grant|Revoke|DefaultPrivileges)'`
- Predefined roles: `RUN='^TestReferencePredefined'`
- RLS: `RUN='^TestReference(RLS|RLSPolicy|RowSecurity|PGPolicy|PGPolicies)'`
- Transactional authority: `RUN='^TestReferenceTransactional(Privileges|Policies)'`
- Logical decoding: `RUN='^TestReference(LogicalDecoding|TestDecoding|ReplicationSlot)'`
- Physical file boundaries: `RUN='^TestReferenceGenericFile'`
