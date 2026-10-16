# internal/engine

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy and
public/private test placement. Run these commands from the repository root.

`TestObjectACLTypeSetterProvenance` exercises actual `Engine.SetObjectACL`
publication for domains, enums and standalone composites. Genuine replacement
clears constructor-synthetic provenance for explicit default-equivalent, empty
and owner-only ACLs. The test checks raw visibility, provider validity, retained
catalog snapshots and rejection of explicit ACL serialization before either
writer interface is called.

```sh
make check RUN='^TestObjectACLType(SetterProvenance|Provenance)$'
make check RACE=1 RUN='^TestObjectACLTypeSetterProvenance$'
```

Public grant, raw-ACL, held-execution and native-control witnesses live in
[`reference/object_grants`](../../reference/object_grants/README.md), not in this
package's private invariant tests.

`TestObjectAddressRoutineOriginalAuthority`, `SourceNullOriginalGraph`,
`ScalarNullPreflightInfo` and `SourceFalseDemand` (with the ObjectAddress prefix)
retain original inquiry descriptors, complete manifests and immutable source
schemas while execution-only NULL/empty-source pruning changes actual demand.
Independent imported NULL proofs do not erase per-argument domain barriers or
actual typed-domain input validation.

`TestNumericSeriesRoutineOriginalAuthority`, the two `TestJSONRecordRoutine`
definitions and `TestIntaggRoutineOriginalAuthority` exercise selected full
formal identities, genuine source/project admission and missing/empty/foreign/
stale manifest rejection. Shape completion, retagging, relocation and stored
recipe grafting must retain original authority, not recover it from an OID.
`TestIntaggRoutineIndependentOwners` covers actual COW ownership staging, named
owner metadata import and reassignment with independent member owners.

The six `TestJSONBRecordRoutine` definitions apply original-authority, stored-view
recipe, ended-scope, failed-constructor and real ACL/owner-COW invariants to all
five existing JSONB providers. Compatible owner declarations do not waive revision
checks: retained old snapshots remain valid, an old graph rejects a changed current
revision, and fresh analysis captures the new owner/revision before admission.

These are private provider/lifetime invariants, not duplicate public SQL tests.
Fixtures create stored recipes before retaining statement admission; entering
Engine.Execute on an operation that already owns admission would recursively
acquire its mutex. Parameterized expressions retain structural rewrite coverage
without pretending they are persistable native recipes.

```sh
make check RUN='^TestObjectAddress(RoutineOriginalAuthority|SourceNullOriginalGraph|ScalarNullPreflightInfo|SourceFalseDemand)$'
make check RACE=1 RUN='^TestObjectAddress(RoutineOriginalAuthority|SourceNullOriginalGraph|ScalarNullPreflightInfo|SourceFalseDemand)$'
make check RUN='^(TestJSONRecordRoutine(OriginalAuthority|FailedConstructor)|TestIntaggRoutine(OriginalAuthority|IndependentOwners)|TestNumericSeriesRoutineOriginalAuthority)$'
make check RACE=1 RUN='^(TestJSONRecordRoutine(OriginalAuthority|FailedConstructor)|TestIntaggRoutine(OriginalAuthority|IndependentOwners)|TestNumericSeriesRoutineOriginalAuthority)$'
make check RUN='^TestJSONBRecordRoutine(OriginalAuthority|StoredRecipe|EndedScope)$'
make check RACE=1 RUN='^TestJSONBRecordRoutine(OriginalAuthority|StoredRecipe|EndedScope)$'
make check RUN='^TestJSONBRecordRoutine(FailedConstructor|OwnershipCOW|ACLCOW)$'
make check RACE=1 RUN='^TestJSONBRecordRoutine(FailedConstructor|OwnershipCOW|ACLCOW)$'
```

Virtual-generation private invariants retain the original owning recipe,
current-invoker authority, captured-source mapping and allocation-free lookup.
Cursor invariants retain DECLARE's genuine authorization snapshot while rejecting
missing/foreign/stale original manifests at FETCH/promotion and releasing all
producer authority at teardown. Stored-recipe replacement checks actual private
catalog remapping; public SQL answers and Dump/Restore live in `reference/`.

```sh
make check RUN='^(TestAlterGeneratedExpressionCapturedVirtual|TestVirtualGenerationOriginalAdmission|TestSQLCursorRoutineAuthorityLifetime)$'
make check RUN='^(TestAlterGeneratedExpressionCapturedVirtual|TestVirtualGenerationOriginalAdmission|TestSQLCursorRoutineAuthorityLifetime)$' RACE=1
make check RUN='^TestRoutineBody(OriginalLifetimeAndForeignRetag|StoredRecipeReplacement|EveryProviderOriginalLifetime)$'
make check RUN='^TestUpdateCoreRecursivePreflight$'
```

`TestNetworkOperatorRoutineOriginalAuthority` independently checks the seven
canonical declarations, complete original manifests and the one genuine SQL
edge, including missing/foreign/stale authority rejection. The SQL-body lifetime
witness now binds 40 distinct roots in 42 instances, retaining independent
BOOL/INET cast-reference removal cases.

```sh
make check RUN='^(TestNetworkOperatorRoutineOriginalAuthority|TestRoutineBodyEveryProviderOriginalLifetime)$'
make check RUN='^(TestNetworkOperatorRoutineOriginalAuthority|TestRoutineBodyEveryProviderOriginalLifetime)$' RACE=1
```

`TestTIDRoutineAuthority` binds all six existing row-backed comparison providers,
retains their canonical original descriptor/reference/revision and rejects
missing/foreign/stale or forged-retag authority. No private SQL-answer oracle or
new physical TID mechanism is involved.

```sh
make check RUN='^TestTIDRoutineAuthority$'
make check RUN='^TestTIDRoutineAuthority$' RACE=1
```

`TestACLValueRoutineOriginalAuthority` checks the four existing bound ACL
providers' full IN signatures, original descriptors/manifests and authentic
missing/foreign/stale/retag rejection. Expansion retains physical RECORD2249,
four OUT fields and SETOF classification; genuine source/project admission
uses `(false,false)`, not the scalar-planning exemption. Bare expression binding
does not itself mark the enclosing query's target-set observation.

```sh
make check RUN='^TestACLValueRoutineOriginalAuthority$'
make check RUN='^TestACLValueRoutineOriginalAuthority$' RACE=1
```

`TestPartitionRoutineOriginalAuthority` exercises genuine bound partition-key
outer/body manifests, missing/empty/foreign/stale/retag rejection and sibling
isolation. Complete-success statement preparation preserves enclosing row/scope/
frame, actual actor/command/RI phases and graph-only post-wait readmission.
The simple-key path initializes no derived program. The retained-check fixture
uses its actual copied generation owner/attribute; missing-owner rejection stays.

```sh
make check RUN='^TestPartitionRoutineOriginalAuthority$'
make check RUN='^TestPartitionRoutineOriginalAuthority$' RACE=1
```
