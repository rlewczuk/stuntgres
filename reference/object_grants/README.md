# reference/object_grants

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root. Role, membership and row-security proofs remain in
[reference/roles](../roles/README.md#testing).

### Non-table object revocation

These focused scenarios exercise the existing shared ACL/revoke path; this
coverage adds no production revoke implementation or external provider.

- `TestReferenceObjectRevokeFamilies`: independent table SELECT with schema
  USAGE/CREATE, surviving inherited/PUBLIC origins, same-held schema-reader
  denial after the final origin disappears, and enum/domain new-dependency
  denial while old rows and enum casts remain valid.
- `TestReferenceObjectRevokeDependencies`: schema CREATE and TYPE/DOMAIN USAGE
  delegation; default/explicit RESTRICT `2BP01 dependent privileges exist`,
  exact Hint `Use CASCADE to revoke them too.`, unchanged ACLs and consumers;
  option-only CASCADE versus ordinary removal; alternate ordinary/option origins;
  non-superuser owner self-revocation; failed late-target atomicity.
- `TestReferenceObjectRevokeDiagnostics`: exact invalid-mask, unknown-privilege,
  column-list, authority and grantor errors, plus complete ordered WARNING01006
  diagnostics. Partial named sequence SELECT,UPDATE revoke emits ten warnings:
  relation first, then tableoid, cmax, xmax, cmin, xmin, ctid, last_value, log_cnt,
  is_called. ALL suppresses only the relation warning, retaining nine column
  warnings. Repeated already-effective owner revoke remains quiet.
- `TestReferenceObjectRevokeFeatureSmoke`: direct/per-step-prepared utilities
  with SAME-held sequence and demanded parameterized `abs($1::integer)` consumers.
  Sequence100/101 survives RESTRICT; CASCADE denies the delegate without
  advancing, grantor reaches102, PUBLIC restores103, final state is103/true.
  Held abs returns7, survives RESTRICT, denies after CASCADE, retains grantor7,
  restores independent7, and denies after final ordinary revoke.

Focused extensions retain the existing assertions in DatabaseAdmission,
SequenceTargets, RoutineSchemaExpansion, NativeBoundaries and
ParametersTablespaces. Failed routine expansion is checked through raw proacl on
PostgreSQL and actual still-allowed function use on both backends; native pg_proc
does not expose proacl. Database CREATE removal denies new schema creation while
existing data7 remains; sequence revocation leaves outside/later explicitly
granted sequences usable; FUNCTIONS/PROCEDURES/ROUTINES stay partitioned.
Real PostgreSQL language declarations, large-object bytes6162→6364 and private
tablespace data42 exercise post-use revocation. Native legal language/large-object
revoke retains exact0A000 boundaries, physical placement remains unavailable, and
ALTER SYSTEM remains syntax-rejected. Existing parameter SET and fresh CONNECT
proofs are reused, as is
[`TestReferenceFDWObjectGrants`](../tables/fdw_object_grants_test.go) for actual
wrapper/server creation consumers, provenance, held access and warnings.

Run each selector in native and pinned PostgreSQL18.6 modes; these commands are
proof instructions, not a claim that a future run has passed:

```sh
timeout 900s make check RUN='^TestReferenceObjectRevoke(Families|Dependencies|Diagnostics)$'
timeout 900s make check RUN='^TestReferenceObjectRevoke(Families|Dependencies|Diagnostics)$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceObjectGrant(SequenceTargets|RoutineSchemaExpansion)$'
timeout 900s make check RUN='^TestReferenceObjectGrant(SequenceTargets|RoutineSchemaExpansion)$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceObjectGrant(DatabaseAdmission|NativeBoundaries|ParametersTablespaces)$'
timeout 900s make check RUN='^TestReferenceObjectGrant(DatabaseAdmission|NativeBoundaries|ParametersTablespaces)$' REFERENCE=1
# Observed feature-smoke contract, also run as a real public API program:
timeout 900s make check RUN='^TestReferenceObjectRevokeFeatureSmoke$'
timeout 900s make check RUN='^TestReferenceObjectRevokeFeatureSmoke$' REFERENCE=1
```

The standalone public API smoke was run separately from the tests with
`go run /tmp/revoke-objects-smoke.go` from the worktree. It uses a fresh Driver,
real sequence, SAME-held parameterized abs, exact diagnostics, affected counts,
metadata and final sequence-state assertions. Its observed stdout was:

```text
sequence 100,101; RESTRICT 2BP01; CASCADE delegate 42501; grantor 102; PUBLIC 103; final 103/true
held parameterized abs 7; RESTRICT 2BP01 unchanged; CASCADE 42501; grantor 7; independent 7; final 42501
```

The temporary smoke source is session evidence, not a repository deliverable or
runtime dependency. No PG-only discovery filter remains in the tests. The shared
testing guide's selected dual-mode and whole-project verification rules still apply.

### Sequence object grants

`TestReferenceObjectGrantSequences` exercises direct/prepared sequence grants,
SELECT/UPDATE/USAGE and ALL, a held currval consumer through revoke/regrant,
grant-option CASCADE, and serial-versus-identity insertion. START10 produces
10/11 with USAGE, SELECT retains currval11 but denies nextval, UPDATE advances
to12 and sets20 but denies currval, and ALL advances to21. Denied calls do not
advance the generator. Delegated USAGE reaches22; CASCADE denies the delegate
while the original holder advances to23. This scenario is a bounded actual
sequence-feature smoke as well as selected compatibility proof.

`TestReferenceObjectGrantSequenceColumns` exercises implicit/TABLE column SELECT
through direct, prepared and the same retained reader: last_value101, sibling
denial, any-column constant/count fallback and separate denied generator functions
with state101,false unchanged. Whole SELECT/ALL revoke clears column grants and
options; RESTRICT/CASCADE, independent origins and late-target failure preserve
atomicity. Exact per-item SELECT-only mask warnings, whole-before-column notice
ordering and raw r-only ACLs are independently asserted against PostgreSQL18.0.
Explicit ON SEQUENCE column lists retain their distinct0LP01 error.

`TestReferenceObjectGrantColumnEligibility` distinguishes sequence USAGE and
table DELETE from column-applicable preliminary authority. It pins whole-warning
before column-error ordering, the UPDATE-only warning boundary and failed
multi-target GRANT/REVOKE atomicity through raw ACLs and the same held recipient.
The failed grant leaves its reader denied; the failed revoke retains value101.

```sh
timeout 900s make check RUN='^TestReferenceObjectGrantSequences$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceObjectGrantSequences$'
timeout 900s make check RUN='^TestReferenceObjectGrantSequenceColumns$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceObjectGrantSequenceColumns$'
timeout 900s make check RUN='^TestReferenceObjectGrantColumnEligibility$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceObjectGrantColumnEligibility$'
timeout 900s make check RUN='^(TestObjectGrantSequenceMasks|TestObjectGrantSequencePublication|TestObjectGrantMasksAndDefaults)$'
```

### Schema, type and sequence target grants

`TestReferenceObjectGrantSchemasTypes` separates schema USAGE/CREATE from table
SELECT, checks ordinary-schema NULL/default ACLs, and proves enum/domain/composite
and builtin integer USAGE denial/regrant at persistent dependency creation.
Existing rows and casts remain usable after revocation. True-array target rejection,
DOMAIN kind validation and late-target atomicity use complete oracle diagnostics.

`TestReferenceObjectGrantSequenceTargets` covers implicit/TABLE sequence targets,
the warning that strips table-only bits, sequence-only ALL expansion, isolation
from other schemas/later creations/tables, late target errors and separate schema
USAGE versus object grant authority. The legacy sequence boundary and MERGE
default consumer retain their original routes under supported sequence grants.

`TestReferenceObjectGrantTypeControls` checks actual builtin/user-type ACL Dump
rejection before any writer callback, including explicit default-equivalent
regrants. Native Reset supplies a genuinely implicit baseline for Dump/Restore
principal remapping and atomic Restore rejection after a valid CREATE prefix.
Its shared SQL contrasts denied/restored builtin dependency creation without
inventing a PostgreSQL host-control equivalent.

`TestReferenceRoleOwnershipControls` uses real shared schema grants and an enum
consumer in a bounded setup schema. Native explicit namespace ACLs reject Dump
before writer callbacks. Dropping that setup restores the original logical Dump
snapshot before the full ACL-free ownership/Restore witness proceeds.

```sh
timeout 900s make check RUN='^TestReference(ObjectGrant(SchemasTypes|SequenceTargets|TypeControls)|RoleGrantSequenceBoundary|MergeInsertOracleQuestions)$' REFERENCE=1
timeout 900s make check RUN='^TestReference(ObjectGrant(SchemasTypes|SequenceTargets|TypeControls)|RoleGrantSequenceBoundary|MergeInsertOracleQuestions)$'
timeout 900s make check RUN='^(TestObjectGrantSchemaTypeMasks|TestObjectGrantSchemaTypePublication|TestObjectGrantBuiltinTypeAuthority)$'
timeout 900s make check RUN='^TestReferenceRoleOwnershipControls$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceRoleOwnershipControls$'
timeout 900s make check RUN='^(TestArrayRecursiveExplicitIdentityRemap|TestArrayRetainedTemporaryBuiltinClosureComponent|TestInfoTimeStampCatalogIdentity|TestInfoTimeStampCatalogRemap)$'
```

### Genuine routine grants and admission

Shared direct/prepared definitions retain independent backend expectations:
`ObjectGrantRoutinesScalar`, `ObjectGrantRoutinesSetAggregateWindow`,
`ObjectGrantRoutineOperators`, `ObjectGrantRoutineSchemaExpansion`,
`ObjectGrantRoutineSchemaKinds`, `ObjectGrantRoutineMixedComparisons`,
`ObjectGrantRoutineFormalSelection` and `ObjectGrantAggregateDistinctAuthority`.
They cover exact scalar/polymorphic/variadic/defaulted/lowered formal selection,
real aggregate/window/SRF/oprcode identities, equality versus sort/support
authority, current class expansion, genuine later extension installation,
other-namespace isolation, delegation/cascade and late atomic errors. Recognized
lookup-only targets do not become native descriptors or execution providers.

`ObjectGrantRoutineStoredAdmission` uses genuine json_array_length in DEFAULT,
generated, CHECK, index and view consumers. Declaration/evaluation timing,
first actual key/predicate demand, owner-relation versus caller-function authority,
held freshness and pruned/empty inputs are separately asserted.
`ObjectGrantRoutineIndexDemand` isolates source-empty insertion, false-predicate
key pruning, literal versus nonconstant strict NULL, same-held revoke/regrant,
index/RETURNING planning-versus-row error precedence and unchanged data on denial.
The pinned oracle distinguishes constant RETURNING planning22012 from index42501
before row-dependent RETURNING; both errors leave data unchanged.
`ObjectGrantRoutineConditionalEquality` retains actual int4eq for NULLIF, simple
CASE and distinct predicates: nonconstant zero-row sources deny, literal strict
NULL and eliminated branches do not invent demand.

`ObjectGrantRoutineQuoteDelegation` and `ObjectGrantRoutineJSONMutation` preserve
full/default/named signatures, held calls and strict/nonstrict NULL behaviour.
The precise PostgreSQL old inlined quote_literal held-plan case and native fresh
ACL denial are independently specified, not compared for forced equality.
Fresh queries/prepared executions and actual TEXT body denial are shared.

Review-remedy witnesses additionally exercise the finite 40-root SQL-body closure:
`ObjectGrantRoutineBodyBitLength`, `Padding`, `Numeric`, `Temporal`, `CastDate`,
`CastSleep`, `ConstantFolding`, `Inquiry`, `PolymorphicCasts`, `Concatenation`,
`BitAndSimilar`, `OverlapsDemand`, and `OverlapsPrecedence` (all body suffixes).
They use genuine selected primitives/operators and four existing embedded casts,
actual fresh authenticated and held consumers, exact Context and pruning.
The network authority witness adds the genuine `int8pl_inet`→`inetpl` SQL edge;
the six other INET operator routines remain INTERNAL and body-free.

`TestReferenceObjectGrantMathLogPowerAuthority` covers all fifteen genuine math
declarations, including the two numeric SQL log wrappers and the canonical
`width_bucket(anycompatible,anycompatiblearray)` identity. Its full held routes
distinguish PostgreSQL's retained parameter-free inlined body from fresh/custom
plans and native original-wrapper admission; neither backend's outcome is
forced onto the other. `TestReferenceObjectGrantStringSliceAuthority` covers
eighteen numeric/end-slice declarations, their distinct short overloads, syntax,
canonical BIT formals, NULL demand and current stored/held authority.
`TestReferenceObjectGrantPGCryptoPasswordAuthority` covers the three allocated
password declarations, actual bcrypt values/salt structure and authentication,
schema visibility and current held/stored authority. Its full PostgreSQL catalog
projection has an explicit native unsupported-column diagnostic, followed by
the supported public projection and actual cursor type checks. Native logical
BIT casts and scalar math table sources are not enabled by these ACL witnesses.

```sh
make check RUN='^TestReferenceObjectGrant(MathLogPower|StringSlice|PGCryptoPassword)Authority$'
make check REFERENCE=1 RUN='^TestReferenceObjectGrant(MathLogPower|StringSlice|PGCryptoPassword)Authority$'
```

`TestReferenceObjectGrantAddressAuthority` and `BitAccessAuthority` cover the
four address inquiries and two BIT access declarations with independent fixed
results, every input NULL position, source/target demand, same-held authority,
stored consumers and rejected-write atomicity. Addressed object identities are
data, not additional EXECUTE calls. Known scalar NULL and proven-empty FROM
pruning retain original schema/lifetime/dependency authority; RECORD initialization
and runtime NULL are not the same exemption.

`NumericSeriesAuthority` distinguishes the genuine two- and three-NUMERIC SRFs,
exact decimal scale, omitted private step1, NULL/empty initialization, sibling
overloads and current-invoker stored calls. `IntaggAuthority` checks the two
allocated aggregate/enum identities independently from each other and core
array_agg/unnest, including held INSERT SELECT atomicity. Its sink ordering uses
existing TEXT formatting rather than enabling native ARRAY ordering.

`JSONRecordAuthority` covers four JSON-only declarations, physical versus
anonymous result shapes, named arguments, parameter/import precedence, held
views and writes. Populate's GRANT identity includes the released defaulted
BOOLEAN third formal; native SQL calls remain two-argument. Full PostgreSQL
declaration projections remain real assertions alongside explicit native
missing-column boundaries and supported canonical-identity projections.

`JSONBRecordAuthority` covers all five existing JSONB providers with distinct
two-IN populate/valid identities, BOOL true/false/NULL validity and scalar/SETOF
initialization. Literal, bound, lateral, physical-rowtype and different-owner
stored views retain current invoker EXECUTE; denied writes publish no prefix.
Full released metadata and native catalog boundaries remain independent assertions.
This adds no DEFAULT/CHECK/scalar-rendering support or conversion algorithm.

`PGCryptoCipherAuthority` uses published NIST fixed encryption/decryption bytes,
not native-generated ciphertext goldens. `PGCryptoRandomAuthority` checks exact
byte lengths, complete UUID version/variant structure and truthful FIPS state;
it never compares draws or asserts fixed random values. Both retain same-held
and stored current-authority cycles. Installed UUID and core3432 are separately
revoked. UUID formatting uses existing concat rather than adding an unsupported
UUID-to-TEXT cast; binary expected values are decoded data, not SQL hex literals.

```sh
make check RUN='^TestReferenceObjectGrant(Address|BitAccess|NumericSeries)Authority$'
make check REFERENCE=1 RUN='^TestReferenceObjectGrant(Address|BitAccess|NumericSeries)Authority$'
make check RUN='^TestReferenceObjectGrant(Intagg|JSONRecord|JSONBRecord)Authority$'
make check REFERENCE=1 RUN='^TestReferenceObjectGrant(Intagg|JSONRecord|JSONBRecord)Authority$'
make check RUN='^TestReferenceObjectGrantPGCrypto(Cipher|Random)Authority$'
make check REFERENCE=1 RUN='^TestReferenceObjectGrantPGCrypto(Cipher|Random)Authority$'
```


`ObjectGrantDomainCheckAuthorityOperator`/`Function` retain real VALUE-dependent
CHECKs, input context, domain-owner versus invoker authority and atomic writes.
`ObjectGrantRoutineIndexConstant` separates fresh and warm physical key/predicate
behavior, strict-NULL/dead-arm/zero-source demand, constant-before-predicate and
RETURNING precedence. `ObjectGrantBitOperatorAuthority` covers all six integrated
BIT operators, exact formal/coercion choices and unrelated integral controls.
`ObjectGrantBitConcatAuthority` independently exercises the existing BIT/VARBIT
concatenation provider, its selected VARBIT formals and held revoke/regrant
transitions; concatenation does not inherit BYTEA routine authority.
`ObjectGrantJSONEachAuthority`/`AuthorityDemand` cover the two genuine JSON-input
record SRFs, duplicate keys, JSON versus TEXT value metadata, named input,
FROM/target/lateral initialization, held revocation and NULL/empty demand.
Their OUT fields are not extra routine formals. The genuine JSONB-input pair is
covered separately by `ObjectGrantJSONBEachAuthority` below.
`ObjectGrantJSONElementsAuthority`/`AuthorityDemand` cover the two genuine
JSON-input scalar SRFs: JSON/TEXT values, duplicate preservation, ordinality,
named/lateral/target consumers and current same-held authority. Surviving NULL,
empty and data-zero SRFs initialize EXECUTE checks; statically false FROM is
pruned, unlike target WHERE FALSE. The scalar OUT `value` is not an input formal.

The current-provider closure also covers `ObjectGrantAccessInquiryRoleAuthority`,
`AccessInquiryRLSAuthority` and `AccessInquiryDemand`; regression aggregates,
`RecentNtileAuthority` and `RecentMultirangeAuthority`; hypothetical-set and
SQL/JSON object-aggregate providers; ARRAY/BIT comparisons; and SQL EXECUTE actual
arguments and their genuine SQL bodies. Each uses the selected released identity,
fresh authority and the same held statement through revoke/regrant.

`ObjectGrantJSONBElementsAuthority`/`AuthorityDemand` retain scalar OUT shape and
the same SRF initialization/pruning distinction as JSON. The two extraction
definitions cover all existing JSON/JSONB field/index/path operators and named
path functions, including shared oprcode identities, full TEXT[] variadic formals,
NULL elements versus NULL arrays, imported parameters and current held authority.
`ObjectGrantLagProviderAuthority`/`LeadProviderAuthority` distinguish all three
released overloads, lazy offset/default arguments, dead CASE and surviving
empty-source initialization.

`ObjectGrantArrayRandomProviderAuthority` uses deterministic singleton/empty/
sample-zero results, not random-order assertions. The two unary unnest definitions
separate ANYARRAY from ANYMULTIRANGE authority, NULL elements from zero-row
NULL/empty streams, and target/FROM/lateral pruning. Multirange fixtures use
existing typed columns/CASE inference rather than unsupported explicit casts;
range values retain INT4RANGE metadata and the existing RECORD raw transport.

`ObjectGrantMetaphoneProviderAuthority` and `ObjectGrantLevenshteinProviderAuthority`
cover all three Metaphone members and four Levenshtein overloads. Row-backed,
stored-view and same-held calls use current member EXECUTE through revoke/regrant;
strict NULL/dead branches retain their observed pruning. Sibling members remain
independent. The witnesses reuse actual installed providers, not new algorithms
or fixed-OID declarations.

`ObjectGrantJSONBInsertProviderAuthority` distinguishes the false default,
explicit/named fourth argument and all four NULL boundaries under the same
released signature. `ObjectGrantWindowRankProviderAuthority` covers all four
zero-argument window rankings, distinct from their hypothetical-set overloads,
with exact two-row values/types, nullable CASE, empty-source initialization and
dead-CASE pruning through held revoke/regrant.

`ObjectGrantACLGetterProviderAuthority` separates a callable's current EXECUTE
from the raw ACL data it exposes, including named-slot order, strict NULL,
stored-view and same-held consumers. `ObjectGrantTypeRawACL` independently
distinguishes constructor raw NULL from an explicit default-equivalent ACL for
domains, enums and composites, retaining exact owner/PUBLIC entries across
revoke/regrant.

`ObjectGrantArrayTransformAuthority` covers twelve genuine generic signatures
and the three corresponding concatenation operators without inventing array
leaf EXECUTE checks. Immutable all-constant calls use fresh executions because
PostgreSQL can retain a previously folded result; row-backed and stored consumers
exercise the same held statement across grant changes.
`ObjectGrantSoundexProviderAuthority` covers both installed Soundex members.
`ObjectGrantJSONBPathDeleteProviderAuthority` and
`ObjectGrantRangeOperatorAuthority` retain each successfully selected operator's
actual generic procedure, independently of argument values and result emptiness.
`ObjectGrantRangeComparisonAuthority` separately covers twelve genuine
range/multirange comparison procedures, including `!=` normalization, without
turning physical subtype comparison kernels into separate SQL invocations.
`ObjectGrantBlockingInquiryAuthority` separates both real blocker-inquiry
callables' current EXECUTE from their empty-result and strict-NULL behavior,
including stored and same-held consumers.
`ObjectGrantJSONBContainmentAuthority` distinguishes the two real containment
procedures from range/array operators sharing their spellings, preserving
strict-NULL pruning and current invoker authority through stored/SAME-held calls.
`ObjectGrantJSONBPrettyAuthority` and `ObjectGrantRegexpSubstrAuthority` cover the
six finite signatures already consumed by their native providers. Same-held
row/stored calls retain current EXECUTE checks; immutable constant calls use
fresh execution rather than assuming a PostgreSQL folded-result lifetime.
`ObjectGrantHyperbolicAuthority` covers all six genuine unary FLOAT8 identities
with exact zero/one values. `ObjectGrantJSONBEachAuthority` separately asserts
both JSONB record SRFs, including output types and EXECUTE initialization for
empty/NULL input; neither path invents a new runtime or subtype permission.
`ObjectGrantJSONBConcatDeleteAuthority` covers exactly the three admitted
concatenation/key/index deletion procedures, not the unavailable TEXT[] deletion
overload. `ObjectGrantParseIdentAuthority` keeps omitted and reordered arguments
on the same canonical full signature. `ObjectGrantCommitTimeAuthority` distinguishes
scalar NULL pruning from unreduced RECORD initialization and checks current
EXECUTE before the real disabled-tracking diagnostic, including `LIMIT 0`.

```sh
make check RUN='^TestReferenceObjectGrantAccessInquiry(RoleAuthority|RLSAuthority|Demand)$'
make check RUN='^TestReferenceObjectGrantAccessInquiry(RoleAuthority|RLSAuthority|Demand)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantRecent(Regression|Ntile|Multirange)Authority$'
make check RUN='^TestReferenceObjectGrantRecent(Regression|Ntile|Multirange)Authority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(Hypothetical|SQLJSONObjectAgg)ProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(Hypothetical|SQLJSONObjectAgg)ProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(Array|Bit)ComparisonProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(Array|Bit)ComparisonProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantSQLPreparedActual(Authority|Body)$'
make check RUN='^TestReferenceObjectGrantSQLPreparedActual(Authority|Body)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantJSONBElementsAuthority(Demand)?$'
make check RUN='^TestReferenceObjectGrantJSONBElementsAuthority(Demand)?$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(JSONBExtractProviderAuthority|JSONExtractSharedProviderAuthority)$'
make check RUN='^TestReferenceObjectGrant(JSONBExtractProviderAuthority|JSONExtractSharedProviderAuthority)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(Lag|Lead)ProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(Lag|Lead)ProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(ArrayRandom|ArrayUnnest|MultirangeUnnest)ProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(ArrayRandom|ArrayUnnest|MultirangeUnnest)ProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(Metaphone|Levenshtein)ProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(Metaphone|Levenshtein)ProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(JSONBInsert|WindowRank)ProviderAuthority$'
make check RUN='^TestReferenceObjectGrant(JSONBInsert|WindowRank)ProviderAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(ACLGetterProviderAuthority|TypeRawACL)$'
make check RUN='^TestReferenceObjectGrant(ACLGetterProviderAuthority|TypeRawACL)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(ArrayTransformAuthority|SoundexProviderAuthority|JSONBPathDeleteProviderAuthority|Range(Operator|Comparison)Authority)$'
make check RUN='^TestReferenceObjectGrant(ArrayTransformAuthority|SoundexProviderAuthority|JSONBPathDeleteProviderAuthority|Range(Operator|Comparison)Authority)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantBlockingInquiryAuthority$'
make check RUN='^TestReferenceObjectGrantBlockingInquiryAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantJSONBContainmentAuthority$'
make check RUN='^TestReferenceObjectGrantJSONBContainmentAuthority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(JSONBPretty|RegexpSubstr)Authority$'
make check RUN='^TestReferenceObjectGrant(JSONBPretty|RegexpSubstr)Authority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(Hyperbolic|JSONBEach)Authority$'
make check RUN='^TestReferenceObjectGrant(Hyperbolic|JSONBEach)Authority$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(JSONBConcatDelete|ParseIdent|CommitTime)Authority$'
make check RUN='^TestReferenceObjectGrant(JSONBConcatDelete|ParseIdent|CommitTime)Authority$' REFERENCE=1
```

Run complete new definitions in both modes (these commands are required proof,
not a claim that the current source candidate passed):
```sh
make check RUN='^TestReferenceObjectGrantRoutineBody(BitLength|Padding|Numeric)$'
make check RUN='^TestReferenceObjectGrantRoutineBody(BitLength|Padding|Numeric)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantRoutineBody(Temporal|CastDate|CastSleep)$'
make check RUN='^TestReferenceObjectGrantRoutineBody(Temporal|CastDate|CastSleep)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantRoutineBody(ConstantFolding|BitAndSimilar|OverlapsPrecedence)$'
make check RUN='^TestReferenceObjectGrantRoutineBody(ConstantFolding|BitAndSimilar|OverlapsPrecedence)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantRoutineBody(Inquiry|PolymorphicCasts|Concatenation)$'
make check RUN='^TestReferenceObjectGrantRoutineBody(Inquiry|PolymorphicCasts|Concatenation)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(RoutineBodyOverlapsDemand|DomainCheckAuthority(Operator|Function)|RoutineIndexConstant)$'
make check RUN='^TestReferenceObjectGrant(RoutineBodyOverlapsDemand|DomainCheckAuthority(Operator|Function)|RoutineIndexConstant)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(BitOperatorAuthority|BitConcatAuthority|JSONEachAuthority(Demand)?)$'
make check RUN='^TestReferenceObjectGrant(BitOperatorAuthority|BitConcatAuthority|JSONEachAuthority(Demand)?)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantJSONElementsAuthority(Demand)?$'
make check RUN='^TestReferenceObjectGrantJSONElementsAuthority(Demand)?$' REFERENCE=1
make check RUN='^TestRoutineProgramOriginalCallsAfter(UniqueWait|SecondUniqueWait)$'
make check RUN='^(TestDomainProgramInvokerAndRIOwnerPhases|TestRoutineInquiryBodyInvokerAndRIOwnerPhases|TestDomainProgramPublicationCurrentAuthority)$'
make check RUN='^TestRoutineBody(OriginalLifetimeAndForeignRetag|StoredRecipeReplacement|EveryProviderOriginalLifetime)$'
make check RUN='^(TestRoutineProgramOriginalCallsAfterUniqueWait|TestRoutineProgramOriginalCallsAfterSecondUniqueWait|TestDomainProgramInvokerAndRIOwnerPhases|TestRoutineInquiryBodyInvokerAndRIOwnerPhases|TestDomainProgramPublicationCurrentAuthority)$' RACE=1
```
The deterministic native units use real UNIQUE waiters, revocation/cancel/reset,
and volatile domain CHECK sequence effects to assert graph readmission without
truth replay, regrant retry, no data prefix and original lifetime/actor isolation.
The two-index waiter forces an actual benign catalog-drift rebind between two
distinct blocking UNIQUE claims, then revokes at the second wait; both an ordinary
constant key and a planning-only key behind FALSE must deny atomically.
The actual Execute/Finish boundary checks fresh implicit publication and explicit
private-stage admission after revocation, independently of index presence.
The finite-provider unit binds all 40 real concrete roots in 42 instances,
including independently selected BOOL/INET quoting casts, and removes each
selected callee's original manifest. Recipe replacement/Restore also executes
DEFAULT and view bodies without mutating the source catalog.
Existing bit-value/typing/held/stored replay definitions remain independent
compatibility coverage, not replaced by the new ACL witnesses.


```sh
make check RUN='^TestReferenceObjectGrant(RoutinesScalar|RoutinesSetAggregateWindow|RoutineOperators|RoutineSchemaExpansion|RoutineSchemaKinds|RoutineMixedComparisons)$'
make check RUN='^TestReferenceObjectGrant(RoutinesScalar|RoutinesSetAggregateWindow|RoutineOperators|RoutineSchemaExpansion|RoutineSchemaKinds|RoutineMixedComparisons)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(RoutineFormalSelection|AggregateDistinctAuthority|RoutineStoredAdmission|RoutineIndexDemand|RoutineConditionalEquality|RoutineQuoteDelegation|RoutineJSONMutation)$'
make check RUN='^TestReferenceObjectGrant(RoutineFormalSelection|AggregateDistinctAuthority|RoutineStoredAdmission|RoutineIndexDemand|RoutineConditionalEquality|RoutineQuoteDelegation|RoutineJSONMutation)$' REFERENCE=1
```

### Database, parameter, tablespace, lifecycle and native controls

`ObjectGrantDatabaseAdmission` checks NULL owner CTc/PUBLIC Tc versus explicit
state, CREATE and actual authenticated physical CONNECT, direct/inherited TEMP,
first allocation versus existing physical namespace errors, grant options,
deferred cleanup and preserved admitted sessions. Fresh login pools close even
on denial; namespace observations use actual public catalog relationships.
`ObjectGrantParametersTablespaces` checks actual protected SET/RESET/set_config,
USERSET/readonly boundaries, canonical sparse parameter sA and delegation,
real builtin spcacl/owner metadata, role dependencies and cleanup. PostgreSQL-only
inquiries/filesystem facilities do not become native providers.

`ObjectGrantSyntaxDiagnostics`, `ObjectGrantNativeBoundaries`,
`ObjectGrantAuthorityLifecycle` and `ObjectGrantControls` retain complete
diagnostic fields/stages, valid unsupported external classes, named grantor
precedence, actual member owners and bootstrap identities, alternate grant-option
support, selective RESTRICT/CASCADE, DROP OWNED/REASSIGN and native atomic controls.
Explicit ACL Dump fails before any writer callback; Restore cannot publish a
valid object/data/settings prefix before a rejected authority suffix.
Native plain structural PG_NODE_TREE grouping and PostgreSQL textual-tree DISTINCT
have independent authority expectations. SQL ARRAY[adbin] is unsupported on both
backends (native0A000, PostgreSQL42704); private recursive grouping remains an
internal capability, not a SQL array constructor/provider.

```sh
make check RUN='^TestReferenceObjectGrant(DatabaseAdmission|ParametersTablespaces|SyntaxDiagnostics|NativeBoundaries|AuthorityLifecycle|Controls)$'
make check RUN='^TestReferenceObjectGrant(DatabaseAdmission|ParametersTablespaces|SyntaxDiagnostics|NativeBoundaries|AuthorityLifecycle|Controls)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrant(DatabaseAdmission|AuthorityLifecycle|Controls)$' RACE=1
make check RUN='^TestReference(RoleGrantTransactions|RoleRevokeTransactions|RoleOwnershipControls|PgcryptoConsumersAndControls|RuleConditionsQueryShapes|RuleReview2CTEPreparation|ViewCheckReviewVirtualNewRows)$'
make check RUN='^TestReference(RoleGrantTransactions|RoleRevokeTransactions|RoleOwnershipControls|PgcryptoConsumersAndControls|RuleConditionsQueryShapes|RuleReview2CTEPreparation|ViewCheckReviewVirtualNewRows)$' REFERENCE=1
```

### Object-grant feature smoke

`TestReferenceObjectGrantFeatureSmoke` exercises actual sequence USAGE/ALL/options,
same-held abs revoke/regrant, aggregate/window/integer-series and checked equality
versus plain sorting, SQL-wrapper TEXT delegation, genuine digest/fuzzy providers,
stored view/index first demand and atomic denial, parameter SET versus ALTER SYSTEM
metadata, builtin spcacl, physical TEMP phases and fresh authenticated CONNECT.
Native-only endings assert explicit-ACL zero-output Dump and unavailable mechanisms.
It has concrete values/types/errors, not an exit-zero-only readiness assertion.

```sh
make check RUN='^TestReferenceObjectGrantFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantFeatureSmoke$'
```

Use external900-second deadlines for selected checks. New definitions first use
the permitted bounded PG discovery callback, then remove that filter before
native-red and final dual-mode evidence. These argv describe required proof,
not current passing claims. Root verification supplies evidence; quickcheck is
the whole-project Docker-free check, not a substitute for real reference modes.


### Generated recipes, settings and initialized cursors

The following shared definitions cover virtual JSON generation under the current
invoker, strict fresh-NULL settings versus nonstrict writes, initialized ordinary
and HOLD cursor lifetimes, and public stored-recipe Dump/Restore. They retain
direct/prepared/SAME-held routes, independently expected values/types and full
permission diagnostics. Cursor promotion includes a real overflow/rollback case;
the DEFAULT/view replacement witness keeps INT4=24 and multiplicity1→2.

Run each selector in both modes:

```sh
make check RUN='^TestReferenceObjectGrant(SettingCallAuthority|VirtualGenerationAuthority)$'
make check RUN='^TestReferenceObjectGrant(SettingCallAuthority|VirtualGenerationAuthority)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantSQLCursor(Authority|HoldAuthority)$'
make check RUN='^TestReferenceObjectGrantSQLCursor(Authority|HoldAuthority)$' REFERENCE=1
make check RUN='^TestReferenceObjectGrantStoredRecipeReplacement$'
make check RUN='^TestReferenceObjectGrantStoredRecipeReplacement$' REFERENCE=1
```

Existing setting-call cast/sibling-signature boundaries are explicitly asserted,
not silently enabled by these authorization tests.

### USING and INET operator authority

`TestReferenceObjectGrantAlterUsingAuthority` retains one original utility handle
through nonowner/outer/body denial and regrant. Non-NULL OID0 invokes the genuine
description body and produces TEXT NULL; failed multi-column rewrites preserve
all original types/rows before the authorized INT8 rewrite.

`TestReferenceObjectGrantNetworkOperatorAuthority` covers all seven released
operator identities, canonical INET/INT8 inputs, CIDR coercion, same-held/stored
consumers, siblings, fresh constants and strict typed NULL. The real commuted
SQL wrapper distinguishes PostgreSQL row inlining from constant execution and
native retained original identity; unsupported standalone function dispatch is
not promoted.

```sh
make check RUN='^TestReferenceObjectGrant(AlterUsingAuthority|NetworkOperatorAuthority)$'
make check RUN='^TestReferenceObjectGrant(AlterUsingAuthority|NetworkOperatorAuthority)$' REFERENCE=1
```

### TID comparison authority

`TestReferenceObjectGrantTIDAuthority` independently specifies all six canonical
comparison identities, BOOL truth and unsigned tuple order, held/stored/parameter
consumers and strict NULL demand. Equality-derived GROUP BY/DISTINCT/simple
CASE/NULLIF use `tideq`; NOT IN uses `tidne`. Ordinary sorting remains independent
of those scalar EXECUTE grants. Type-USAGE phases retain existing datum reads
while denying new declared dependencies.

```sh
make check RUN='^TestReferenceObjectGrantTIDAuthority$'
make check RUN='^TestReferenceObjectGrantTIDAuthority$' REFERENCE=1
```

### ACL value provider authority

`TestReferenceObjectGrantACLValueAuthority` independently specifies the four
released ACL value/containment identities, dynamic role provenance, exact
item/array/BOOL values and four-field expansion. Same held and stored consumers
use the current invoker across revoke/grant/revoke. Scalar literal/imported NULL,
surviving RECORD/SETOF initialization and genuinely pruned FROM sources have
separate demand expectations; typed input/body errors preserve their stage.
Explicit constructor children retain independent EXECUTE authority, while
reading preexisting ACL data does not replay those constructors.

```sh
make check RUN='^TestReferenceObjectGrantACLValueAuthority$'
make check RUN='^TestReferenceObjectGrantACLValueAuthority$' REFERENCE=1
```

### Demanded partition-key authority

`TestReferenceObjectGrantPartitionKeyAuthority` covers JSON/TEXT recipes in
HASH/RANGE, actual parent/direct-leaf and same-held writes, current writer versus
separately authorized owner, real NULL versus zero-input demand, independent SQL
child rights and complete failed-write atomicity. PostgreSQL also runs the full
LIST body; native explicitly asserts its existing42601 syntax limit, never Skip.
`TestReferenceObjectGrantPartitionKeyDDLAuthority` exercises populated DEFAULT
validation and same held CREATE retries. Its warmed PostgreSQL inlined TEXT
wrapper versus fresh native original-wrapper authority is independently specified;
both still require the surviving child. Neither case licenses a permission cache.

```sh
make check RUN='^TestReferenceObjectGrantPartitionKey(Authority|DDLAuthority)$'
make check RUN='^TestReferenceObjectGrantPartitionKey(Authority|DDLAuthority)$' REFERENCE=1
```
