# reference/type_resolution

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Regcollation and regnamespace

The shared scenarios independently specify symbolic/numeric identity, SQL
identifier folding/quoting, unsigned boundaries, NULL and declared metadata.
Namespace input is a single schema identifier, independent of search path and
USAGE. Collation identity uses encoding-aware lookup and visibility, including
shadowing, implicit pg_catalog, explicit qualification and schema permission
failures; identity lookup does not grant ICU/libc comparison execution.
Diagnostic scenarios cover direct/prepared literals, direct/held parameters and
direct/held runtime text casts with full English diagnostics and character
positions. Consumer lifecycle scenarios cover held constants, eager retained
spelling, rename/move, stored dependencies versus opaque rows and supported
domain/array values.

The initial pinned run observed an empty pq `DatabaseTypeName` for REGCOLLATION,
while REGNAMESPACE retains its name. Tests preserve this transport distinction
separately from each alias's string/native and byte/pq raw codec contract.
DROP/recreate invalidates held literals for both aliases: their next execution
must identify the fresh named referents, not the old allocator OIDs.
The next pinned discovery run also observed held source reanalysis after catalog
rename: the old collation spelling fails with42704 rather than retaining success.
This is distinct from the stable symbolic presentation of already-returned Rows.
Retained-row mutation uses a second connection because pq cannot process another
query while the first connection has an open result stream.

Root's pinned oracle discovery passed the shared SQL definitions, and root then
observed the native failing regression before production changes. No discovery
backend filter or native-control deferral remains. Completed unfiltered selected
proof is required in both modes, including native controls and actual emitted
PostgreSQL replay; discovery alone does not prove those phases.
Completed native phases exercise real Dump/Reset/Restore with allocator
displacement, class-aware identity witnesses and stable redump; PostgreSQL
replays the actual emitted native stream in the same shared scenario.

The opaque-row DROP witness checks the original OID before and after deletion.
Before stable-redump controls, that row is replaced with the independently
unknown max sentinel4294967295; both spelling and bits are checked on source,
restored native values and actual PostgreSQL replay. A dropped allocator OID
can legitimately collide with a reminted live referent, changing symbolic
spelling while retaining bits; tests do not reserve IDs or suppress live output.

The bounded ArrayDependencies scenario independently requires opaque
REGCOLLATION[]/REGNAMESPACE[] DEFAULT and VIEW Const payloads not to block DROP.
It checks retained OIDs and fresh default execution after referent deletion.
Root's pinned run passed this witness and quick mode exposed the native default
provider rejection before the remedy. Dependency filtering follows exact Const
shape; existing REGTYPE recursive dependency behavior remains unchanged.

The bounded ObjectBoundaryStringCasts and ObjectBoundaryByteParameters scenarios
cover both aliases through the same direct/prepared SQL and direct/held parameter
paths. Independent expectations include TEXT/BPCHAR/VARCHAR/NAME IO, character
padding and explicit truncation, NULL propagation, Boolean/unrelated-alias
rejection, and valid -> nonnil-empty error -> nil/typed-nil NULL -> valid held
recovery. Root's pinned PostgreSQL-only discovery passed these definitions;
native-before rejected BPCHAR/NAME casts and treated typed-nil bytes as invalid
name syntax. Completed post-remedy proof is still required in both modes.

MixedRoutinePersistence exercises the shared routine-container consumer with
REGPROCEDURE, REGCOLLATION and REGNAMESPACE in composites and unconstrained
composite domains. Executable composite arrays share the logical codecs. Independent named-OID
equalities cover saved rows, a constant view and replayed defaults. Native replay
uses a fresh target's retained TEMP namespace/relation/rowtype allocations to
displace both imported object classes; permanent spacers alone are replaced.
Failure diagnostics report both original/restored OID pairs without weakening
the requirement that both differ.
PostgreSQL executes the actual emitted native stream. Full dual-mode selection
is required for replay; a postgres-only selector cannot supply that native stream.
The rebase union and this witness require fresh proof and independent review.
The sql-contract subtest independently exercises pinned SQL expectations without
native controls; logical-replay separately requires the actual emitted stream.
Routine spelling is projected as TEXT while OID equality still checks identity.


Use an external 900-second deadline per invocation, oracle first:

```sh
make check RUN='^TestReferenceReg(Collation|Namespace)(Input|Visibility|Diagnostics|Prepared|StoredControls|ArrayDependencies)$' REFERENCE=1
make check RUN='^TestReferenceReg(Collation|Namespace)(Input|Visibility|Diagnostics|Prepared|StoredControls|ArrayDependencies)$'
make check RUN='^TestReferenceRegObjectBoundary(StringCasts|ByteParameters)$' REFERENCE=1
make check RUN='^TestReferenceRegObjectBoundary(StringCasts|ByteParameters)$'
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence/postgres/sql-contract$' REFERENCE=1
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence$' REFERENCE=1
make check RUN='^TestReferenceRegCollationMixedRoutinePersistence$'
make check RUN='^TestReferenceRegCollationFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceRegCollationFeatureSmoke$'
make check RUN='^Test(RegObjectAlias(Datum|Codec|CapturedAuthority|Persistence|ComparisonBoundary)|AnyValueProviderDatums|MaterializedViewRegtypePresentation|PGDependRegtypeValueReferences|PGDependExpressionReferences)$'
```

The existing PGDependExpressionReferences units cover structural domain literals
whose declared Usage supplies the datatype dependency before a datum is
materialized. Alias value traversal must add no referent edge for that absent
payload while preserving the declared domain type edge.

Feature smoke reuses all eleven independent scenario bodies. Expected completed
observations include C950, namespace11, dash/unknown/NULL output, quoted-dot
namespace acceptance, shadow qualification, exact missing/ACL diagnostics,
held recovery including typed-nil bytes, string-category sizing and cast
boundaries, retained spelling, stored dependencies, mixed routine/object named
identity replay and logical control
roundtrip. Root records actual results; selected checks do not replace
whole-project quickcheck or post-integration finalcheck.

### Text-search OID aliases (RegTS)

The shared `TestReferenceRegTS...` scenarios specify scalar REGCONFIG (3734)
and REGDICTIONARY (3769), zero (`-`) versus SQL NULL, unsigned numeric input,
octal parsing, direct/held parameters, raw strings versus pq bytes, empty
metadata, integral carrier casts, real installed unaccent dictionary identity,
quoted namespaces, search-path visibility, authorization, retained cursors and
logical persistence. The companion array identities remain lookup-only natively.
Expected rows and full English diagnostics are independent contracts, not
values copied from native output.

Pinned oracle discovery passed all seven selected scenario groups in the root's
second discovery round. The temporary backend filter and replay early return
have been removed. Every available backend now runs the shared scenario, and
real-mode persistence requires nonempty actual native dump bytes for PostgreSQL
replay. Discovery success is not native, emitted-stream replay or control proof;
the filter-free native regression was observed before production implementation.
The first pinned oracle run reported normalized missing configuration name
`123` for input ` 123 ` (42704, Position 8), not a primary retaining whitespace.
The contract now uses the parsed name for both aliases. Retained alias rows use
a feature-local raw/metadata assertion instead of the generic fixture's closed
type-family inventory; the shared fixture has not been expanded.

Native symbolic configurations have no provider and must report 0A000
`text search configuration name lookup is not supported`; PostgreSQL missing
configurations report 42704. Native dictionaries use only installed real
descriptors, never a fabricated `simple` dictionary. PostgreSQL-only custom
dictionary DDL characterizes quoted names, first-path (not ambiguous) lookup
and held-plan path invalidation. It does not authorize native text-search DDL,
configuration objects, two-argument unaccent, full-text execution, filesystem
rules, native ABI loading, or configuration DDL even in these scenarios.
The competing-name witnesses first require distinct nonzero qualified dictionary
OIDs, then put both schemas on the path in left/right and right/left order.
Unqualified input must select the first dictionary without ambiguity. Numeric
output of the winner is unqualified; the shadowed dictionary's output retains
its schema. The held-literal and TEXT-parameter phase likewise keeps both
candidates visible while reversing their order, preserving the literal
invalidation assertions and independently checking each parameter's selected OID.
Native Reset/Dump/Restore and binary COPY public controls are explicit native
phases; common SQL independently specifies their logical rows. Persistence
asserts qualified dump references, same-engine replay, real displaced destination
OID allocation, default/generated/view identities, retained rows, failure/cancellation
atomicity and stable redump. The final PostgreSQL phase replays actual emitted SQL.
The fresh-target displacement phase reserves an actual owner-held TEMP sequence
whose publicly observed OID equals the captured source dictionary OID, then
requires the restored dictionary OID to differ. It also checks that the TEMP
sequence keeps its identity and next value, while dictionary data and definition
literals follow the new descriptor. No allocator sequence is pinned: ordinary
target tables are discarded by Restore and cannot establish this collision.
The mixed routine/dictionary composite scenario separately checks persistent data,
stored DEFAULT consumption by a new INSERT, and a view literal. Every routine
member must retain the pinned `abs(integer)` identity (1397); every live dictionary
member must equal the actual destination installation's descriptor OID. A dropped
source table prevents allocator parity from masking numeric container output.
Native replay uses the same retained TEMP-collision destination, and PostgreSQL
replays the actual native dump bytes, never a hand-authored substitute. Root's
pinned and native failing-before runs passed source setup but observed mixed data
retaining the source dictionary OID instead of the destination descriptor.
The routine-container scalar leaf now uses the same qualified dictionary formatter
as ordinary compound emission, without changing routine fragments or field quoting.
Fresh post-remedy native and pinned proof is required. No discovery filter or
replay skip is present in this scenario.
The partition persistence scenario creates a range parent and matching ordinary
child before installing unaccent, then attaches the child with the live dictionary
as its finite exclusive upper bound. The namespace exists before the relations,
so a missing dictionary predecessor is not confused with missing-schema ordering.
Only a zero-valued row remains at Dump: no live dictionary data/default reference
can mask the missing bound dependency. The actual stream must install the extension
before the finite partition declaration, restore natively, and replay on PostgreSQL.
Routing runs once after each destination replay, using the actual dictionary OID:
its immediate predecessor belongs to the finite child, while the named descriptor and its successor
belong to the default partition. Dropping that populated child avoids unsupported
partition DELETE. Without the default partition, the named upper
endpoint must report the full 23514 diagnostic and roll back a preceding valid
row in the same INSERT. Root's pinned and native failing-before runs passed the
supported zero-only source setup, then actual native Restore and PostgreSQL
emitted-byte replay both failed with 42704 because the qualified dictionary bound
was consumed before extension installation. The full catalog-alias reference
walker now visits real dictionary references after the error-checked operator walk,
adding the missing dump predecessor without changing exact-Const collation/namespace
dependency filtering. Root then observed successful native and PostgreSQL replay;
the remaining native endpoint Detail printed a numeric dictionary OID while the
unchanged PostgreSQL diagnostic expectation passed with its qualified name.
Diagnostic output demand now captures the existing coherent catalog/path environment
for RegTS columns and typed partition keys, including derived keys over primitive
columns. `TestReferenceRegTSPartitionDiagnostics` permanently exercises direct
dictionary-column and derived `k::regdictionary` range keys over an OID column.
Both direct and prepared multirow INSERTs must retain the qualified symbolic
dictionary in the complete English 23514 diagnostic, omit unrelated logical
fields, and publish neither row; a subsequent interior insert must still route.
The derived case also checks the expression's REGDICTIONARY result metadata.
No derived-key failure before the typed-output-demand fix is claimed. Root's pinned
validation established the independently specified label `((k::regdictionary))`;
native retained the symbolic dictionary but initially grouped the cast's operand
instead. Partition diagnostics now use the existing pretty renderer for explicit
RegTS cast keys and group the whole expression, without changing global cast
deparse or relaxing the full diagnostic contract. Fresh selected native/pinned
proof remains required. Numeric partition-bound structural output stays separate
from SQL and diagnostics.
Dictionary identity discovery uses the existing installation's
`pg_catalog.pg_extension.extnamespace` and numerically filters actual
`pg_catalog.pg_ts_dict` rows; named alias input must equal that descriptor OID.
Scalar/companion identities are independently fixed through public REGTYPE/OID
casts (3734/3769/3735/3770). Native pg_type and pg_namespace projections are
absent and are not added by this feature. PostgreSQL-only pg_type metadata
assertions retain genuine release catalog context; native units independently
check fixed descriptors, numeric category and four-byte inline codecs.
Stored CHECK, DEFAULT and generated-expression publication validates
real dictionary dependencies under the existing owner/epoch fences. Integral
carrier casts cover both implicit/assignment and explicit admission, and typed-nil
byte parameters remain SQL NULL for both text-search aliases.

Root runs each selected command from the worktree with an external 900-second
deadline. Oracle discovery and failing-before native regression are recorded;
the completed implementation now requires fresh native and pinned proof:

```sh
make check RUN='^TestReferenceRegTS(ValuesMetadata|Diagnostics|Namespace|NamespaceAuthorization|PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence|PartitionDiagnostics|FeatureSmoke)$'
make check RUN='^TestReferenceRegTS(ValuesMetadata|Diagnostics|Namespace|NamespaceAuthorization|PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence|PartitionDiagnostics|FeatureSmoke)$' REFERENCE=1
make check RUN='^Test(RegTS(ScalarCodecs|CastsDomains|ProviderResolution|PersistenceRemap|PreparedCapture|CapturedNamespaceACL|StoredDependencyProviders)|AnyValueProviderDatums)$'
make check RUN='^Test(RegTS(PreparedCapture|CapturedNamespaceACL)|ReferenceRegTS(PreparedLifetime|RetainedRows|Persistence|MixedRoutinePersistence|PartitionPersistence))$' RACE=1
```

Root recorded F2 failures with these selected commands; rerun them after the remedy:

```sh
make check RUN='^TestReferenceRegTSMixedRoutinePersistence$' REFERENCE=1
make check RUN='^TestReferenceRegTSMixedRoutinePersistence$'
```

Root recorded F3 failures with these selected commands; rerun them after the remedy:

```sh
make check RUN='^TestReferenceRegTSPartitionPersistence$' REFERENCE=1
make check RUN='^TestReferenceRegTSPartitionPersistence$'
```

The supplemental diagnostic scenario requires independent pinned expectation
proof first, followed by native proof (no pre-fix derived failure is claimed):

```sh
make check RUN='^TestReferenceRegTSPartitionDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceRegTSPartitionDiagnostics$'
```

The internal units exercise actual type codecs and captured catalog/ACL/remap
seams, not public SQL or source-wiring counts. Algorithm-only dictionary
snapshots are cloned from installed typed descriptors and never publish extra
providers. Shared public scenarios remain the acceptance proof for direct/held
driver behavior, metadata and actual native emitted-byte PostgreSQL replay.

Repeat both modes after scenario/implementation changes. Feature smoke uses
`make check RUN='^TestReferenceRegTSFeatureSmoke$'` and the same command with
`REFERENCE=1`; expected exit zero and assertions for all named RegTS scenarios,
not a readiness message. Whole-project `make quickcheck` remains a separate
post-edit gate; no checks or formatters were run while writing discovery tests.

### Regrole

The untagged shared scenarios independently assert role-name folding/quoting,
numeric OID boundaries (unknown IDs admitted), direct/fresh/held prepared input,
captured spelling, metadata/raw payloads, exact English diagnostic fields and
stored scalar Const restriction. Numeric conversion trees, runtime casts, typed
NULL and opaque arrays are distinguished from prohibited scalar children.
Native phases exercise real Dump/Reset/Restore, cross-Driver target-role remapping,
retained metadata/cursors, failure/cancellation atomicity and actual emitted-dump
replay on PostgreSQL18.0. Role provisioning is fixture setup, not an extension of
authentication/ACL/role DDL scope.
CHECK-grid array equality uses shared ordinary array providers and preserves the
exact scalar-Const ban; native-only missing-array-operator assertions are removed.
Existing domain-constraint admission boundaries remain independently asserted.
DEFAULT/VIEW shapes exercise native numeric/runtime/opaque-array distinctions.
Unknown-role Restore failures assert full independent diagnostics against the
corresponding pinned SQL replay: ERROR/ERROR, exact42704 primary, character
Position in the complete script, absent Context and all other optional fields.
The actual-dump missing-role case identifies the first opaque array DEFAULT in
`rr_recursive`; earlier runtime-text defaults do not resolve names at CREATE.

Run each small set both without and with `REFERENCE=1`, serially, with an external
900-second deadline:

```sh
make check RUN='^TestReferenceRegrole(Values|Parameters|Diagnostics|Metadata)$'
make check RUN='^TestReferenceRegrole(Values|Parameters|Diagnostics|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceRegrole(StoredConstants|StoredShapes|PreparedIdentity)$'
make check RUN='^TestReferenceRegrole(StoredConstants|StoredShapes|PreparedIdentity)$' REFERENCE=1
make check RUN='^TestReferenceRegrole(AtomicityCancellation|NativeControls)$'
make check RUN='^TestReferenceRegrole(AtomicityCancellation|NativeControls)$' REFERENCE=1
make check RUN='^TestRegrole(Datum|Codec|StoredLiteral|CapturedAuthority)$'
make check RUN='^TestReferenceRegrole(PreparedIdentity|AtomicityCancellation|NativeControls)$' RACE=1
```

Feature smoke also requires a real throwaway public API Go program, not tests
alone. Run `go run /tmp/regrole-feature-smoke/main.go` from the worktree after
materializing the session-provided source outside the repository. It independently
checks direct/held prepared quoted and unknown numeric values, REGROLE metadata,
full0A000 stored diagnostic, legal runtime-default data and NULL view, actual
Dump/Reset/Restore, retained eager rows and identical redump. Expected exit0 and
empty stderr; its observation lines report direct/prepared values, stored error,
nonempty dump, retained/restored values and identical redump only after validation.
Remove the temporary program after recording evidence; never commit its source.
Selected proofs do not replace whole-project quickcheck or post-merge finalcheck.

### Operator OID aliases

The `TestReferenceRegOperator...` definitions cover name-only and exact ordered
signature resolution, prefix NONE, complete released builtin identities, direct
and held parameters, metadata, complete diagnostics, and real private namespace
and operand-type authorization. Independent expectations/provenance live in
`docs/design/regoperator-pg18-inventory.tsv` and `regoperator-pg18-oracle.md`.
Custom operator creation/catalog projection is a genuine PostgreSQL-only
mechanism witness, not native custom DDL or execution support.

Pinned oracle discovery is complete in the root's tests-1 through tests-4 reports;
tests-5 records the failing native-before-production regression. No discovery
filter/logging remains. Both aliases' typed-nil byte binds are SQL NULL. The
native unsupported CREATE OPERATOR boundary independently asserts the observed
42601 primary and Position8, plus unchanged sentinel data.

Review-remedy discovery is recorded in tests-13/14: pinned complete competing
diagnostics and NBSP records preceded the observed native/parser/prepared failures.
`ParserEdges` preserves all six literal/parameter/runtime routes, while
`PreparedAnalysis` covers public mixed-alias values, held recovery and snapshots.
Internal `TestRegOperatorPrepared...` units additionally use real typed catalog
descriptors to prove ordered operand manifests, direct shared-cell validation,
atomic group publication and unchanged captures under unrelated role changes.
Those internal descriptors do not extend public custom operator DDL support.

Root verifies the identical completed definitions with an external 900-second
deadline for each command:

```sh
make check RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory|ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceRegOperator(ValuesMetadata|Signatures|Diagnostics|ParserEdges|NamespaceAuthorization|Inventory|ExternalBoundary|PreparedLifetime|PreparedAnalysis|Persistence|FeatureSmoke)$'
make check RUN='^TestRegOperator'
make check RUN='^TestRegOperator(Signature|Prepared)'
make check RACE=1 RUN='^Test(RegOperatorPrepared|ReferenceRegOperator(PreparedLifetime|PreparedAnalysis|Persistence))'
make check RUN='^TestReferenceRegOperatorFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRegOperatorFeatureSmoke$'
```

Persistence replays the actual emitted native dump on PostgreSQL, not constructed
SQL. Smoke covers unique/ambiguous names, exact ordered signatures, unary NONE,
held parameter failure/recovery, metadata, private-schema denial, numeric dump
roundtrips, retained strings and failed-tuple atomicity. These definitions and
implementation are not passing evidence until root exercises the gates.

### Regproc / regprocedure definitions and evidence

The original eight `TestReferenceRegProc` definitions preserve the independently specified
PostgreSQL18.0 contract. Root Verification1 exposed diagnostic/quoting corrections;
Verification2 confirmed the corrected oracle SQL contract, with native-generated
dump phases still deferred at that test-only stage. Root removed the PostgreSQL-only
filter and both nil-stream guards before Verification3's genuine failing native
regression. Verification8 subsequently passed the original eight definitions and
changed standalone REGTYPE definition in native/pinned modes, quickcheck and public
smoke after captured type-authority reuse, including actual emitted-stream replay.
Review1 identified additional numeric/dash signature-type and mixed routine/REGROLE
serialization defects outside those exercised paths. Verification12 completed
native/pinned proof for both new regression definitions, including restored-default
INSERT and actual emitted-stream replay. Prior passes alone did not establish
their remedies. No discovery filters or control deferrals remain in the twelve
RegProc definitions; completed selected native/pinned proof now includes R5.

Witnesses cover direct/fresh-prepared/same-held values, empty-result metadata,
string/textual-byte/typed-nil/NULL frames, exact fixed identities and signatures,
six complete diagnostic routes, casts, all-path overload visibility, real unaccent
authority, expression versus data-only dependencies, atomic late-row failure,
tables/defaults/views/domains/arrays, retained outputs and native replacement.
Schema authority setup deliberately uses real PostgreSQL USAGE grants versus
supported native schema ownership transfer; it does not admit native GRANT/REVOKE
SQL or transfer bootstrap-owned tables.
`DROP EXTENSION` is likewise an existing native utility boundary, not delivered
by routine aliases. PreparedAuthority and Persistence assert its exact native
`0A000` / `statement is unsupported` / Position `1` diagnostic and unchanged
alias values, then continue supported path/role authority and persistence phases.
Real PostgreSQL retains the complete drop/reinstall, dangling data identity,
held-literal retention and dependency Detail/Hint assertions. Private real catalog
removal supplements native identity-lifetime coverage without claiming SQL support.

Signature witnesses include canonical aliases, schema-qualified types, modifiers,
arrays, zero arity, aggregate sum and
the quoted mixed-case RI builtin. Complete release extraction validates all 3397
source-fixed identities and 116 argument-type symbols; see
[the evidence note](../../docs/design/regproc-pg18-oracle.md).

`SignatureTypeSyntax` distinguishes decoded argument syntax from whole-alias input:
numeric/dash arguments must use the existing type parser and diagnostics, while
whole REGTYPE/REGPROC/REGPROCEDURE numeric and dash shortcuts remain valid.
`MixedRolePersistence` covers a supported composite carrying REGPROCEDURE and a
named REGROLE, preserving symbolic role serialization in rows/defaults/views,
displaced native Restore and exact emitted-stream PostgreSQL replay. Its role OID
must resolve to the receiving catalog's named role, not retain the source OID.
Scalar REGROLE stored constants remain unsupported; executable composite arrays
use the same recursive logical replay path.

Review2's distinct mixed routine/operator capture coverage gap is addressed by
`reference/type_resolution/regproc_mixed_operator_test.go` /
`TestReferenceRegProcMixedOperatorPersistence`: the admitted composite contains
REGPROCEDURE, REGOPERATOR and REGTYPE, with independently expected builtin OIDs
1397/551/23 and symbolic output. Direct and held string/byte/NULL frames,
row/default/view persistence, actual emitted native Dump replay on PostgreSQL,
restored-default INSERT and retained native output/metadata across Reset/Restore
preserve existing operator behavior without adding operator functionality.
The private `TestRoutineMixedOperatorCompoundCapture` supplements public values
with real descriptor publication, exact ordered fresh and retained operand
dependencies at the same occurrence, cell classifications, actual operand revision
invalidation/recapture and qualified operand-authority invalidation/recovery.
Completed root native/pinned checks confirm the public scenario, and the real
private mixed-capture lifecycle selector passes. Verification12 remains historical;
the later proof exercises the exact mixed owner and restored-view remedy.

```sh
make check RUN='^TestRoutineMixedOperatorCompoundCapture$'
make check RUN='^TestReferenceRegProcMixedOperatorPersistence$'
make check RUN='^TestReferenceRegProcMixedOperatorPersistence$' REFERENCE=1
```

The public selector is also the bounded R4 feature smoke: expect exact symbolic
values and OIDs, restored-default evaluation, actual PostgreSQL replay, and
retained native output/metadata to pass, not merely successful setup.

Review3's materialized retained-input finding has a public regression in
`regproc_materialized_test.go` / `TestReferenceRegProcMaterializedPersistence`.
It independently specifies fixed builtin OIDs1397/551/23 and receiving-catalog
named-role identity in a nonpublic opaque composite. Direct and nested definitions
must retain native saved stale population1 after Dump/Reset/Restore, refresh to2,
then refresh to2/3. Actual emitted SQL creates PostgreSQL views WITH NO DATA:
complete55000 diagnostics precede explicit refresh to2, then2/3. Native unread
rows and metadata remain retained across replacement and refresh.
Verification16 records oracle-only SQL discovery success and corrected native
Restore failure `type "mixed" does not exist`, not passing materialized proof.
The source remedy reuses shared retained input traversal under the population
owner before frozen preflight, qualifies dumped composite casts through the
existing renderer, and validates emitted closed snapshot concatenation through
the existing import owner. Completed native and pinned replay proof now confirms
this consumer; Verification15 remains the historical earlier-consumer evidence.

```sh
make check RUN='^TestReferenceRegProcMaterializedPersistence$' REFERENCE=1
make check RUN='^TestReferenceRegProcMaterializedPersistence$'
```

This dedicated R5 smoke expects exit0/PASS for current symbolic values, exact
builtin identities, relational role identity, direct/prepared fresh metadata,
stale-versus-refreshed populations, retained native results, and actual stream
replay. Root removed discovery filters and missing-stream deferrals before the
genuine native failure; the fixture preserves Reset's existing role catalog.
The modified private owner proof is selected separately:

```sh
make check RUN='^Test(MaterializedRefreshOwnerContext|MaterializedRefreshAuthorization|MaterializedViewSnapshotLosslessCoercions|RoutineMixedOperatorCompoundCapture)$'
```

It expects genuine retained coercion1397, owner-authorized restricted input,
unchanged caller session identity, and input/principal restoration after success
or producer failure. Full quickcheck and fresh review remain separate root gates.
The snapshot unit admits the generated closed concatenation and rejects nested
function, identity, column and binary-arithmetic expressions.

`Persistence` and `FeatureSmoke` use actual native Dump bytes, native Reset/Restore,
displaced independent engine identities, retained rows and failed/canceled Restore
atomicity. Real mode replays that exact emitted stream through ordinary PostgreSQL
SQL; no manually reconstructed stream or simulated PostgreSQL control API is used.

Selected root proof commands, each with an external 900-second deadline:

```sh
make check RUN='^TestReferenceRegProc(ValuesMetadata|Signatures|SignatureTypeSyntax|Diagnostics|Casts)$'
make check RUN='^TestReferenceRegProc(ValuesMetadata|Signatures|SignatureTypeSyntax|Diagnostics|Casts)$' REFERENCE=1
make check RUN='^TestReferenceRegProc(SearchPath|PreparedAuthority|Persistence|MixedRolePersistence|FeatureSmoke)$'
make check RUN='^TestReferenceRegProc(SearchPath|PreparedAuthority|Persistence|MixedRolePersistence|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceRegProcFeatureSmoke$'
make check RUN='^TestReferenceRegProcFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceRegProc(SignatureTypeSyntax|MixedRolePersistence)$'
make check RUN='^TestReferenceRegProc(SignatureTypeSyntax|MixedRolePersistence)$' REFERENCE=1
make check RUN='^TestReferenceRegProc(PreparedAuthority|Persistence)$' RACE=1
make check RUN='^TestReferenceRegtypeAliasModifierSyntax$' REFERENCE=1
make check RUN='^TestReferenceRegtypeAliasModifierSyntax$'
```

Smoke expects exact builtin OIDs1242/1397/1644, REGPROC/REGPROCEDURE metadata,
full ambiguity/missing records, same-held invalid-to-valid recovery, no partial
INSERT rows, native persistent roundtrip and actual emitted-stream oracle replay.
Expected success is test PASS/exit0, not an invented console banner. Private parser,
codec, release-inventory, candidate-mask, remap and retained-presentation units
supplement these public scenarios; they do not replace selected dual-mode proof,
whole-project quickcheck or post-integration finalcheck. Neither readiness nor
source extraction is passing native smoke evidence.

Changed preexisting definition: `TestReferenceRegtypeAliasModifierSyntax` now
adds the shared generic decoded-type keyword boundary (NONE on all six routes)
and preserves admitted quoted/qualified domain and builtin/modifier paths.
Its REGTYPE-specific route expectations use the existing independent error owner;
Verification8 confirmed these standalone fields in both native and pinned modes.
The two review regressions also pass completed native/pinned proof, including
six-route numeric/dash rejection and mixed composite row/default/view restoration
with receiving-catalog role identity and actual emitted-stream PostgreSQL replay.

Complete added/modified dual-definition inventory (`A` = first RegProc selector
above, `B` = second, `C` = standalone Regtype selector, `D` = dedicated R4
MixedOperatorPersistence selector, `E` = dedicated R5 MaterializedPersistence
selector; repeat each in both modes):

| Path in this package | Exact definition | Membership | Reason |
| --- | --- | --- | --- |
| `regproc_values_test.go` | `TestReferenceRegProcValuesMetadata` | A | New alias values, raw transport, metadata and storage |
| `regproc_values_test.go` | `TestReferenceRegProcSignatures` | A | New exact signatures, released OIDs and quoting |
| `regproc_signature_type_syntax_test.go` | `TestReferenceRegProcSignatureTypeSyntax` | A and remedy smoke | Numeric/dash argument syntax across six routes, valid held recovery and preserved whole-input shortcuts |
| `regproc_diagnostics_test.go` | `TestReferenceRegProcDiagnostics` | A | New complete six-route diagnostics |
| `regproc_values_test.go` | `TestReferenceRegProcCasts` | A | New casts, comparisons and assignment boundaries |
| `regproc_authority_test.go` | `TestReferenceRegProcSearchPath` | B | New path/namespace authority and qualified output |
| `regproc_authority_test.go` | `TestReferenceRegProcPreparedAuthority` | B | Held identity/input lifecycle, supported path/role recovery and explicit native DROP boundary; full PG drop/reinstall |
| `regproc_persistence_test.go` | `TestReferenceRegProcPersistence` | B | Native controls, owned REGPROC/REGPROCEDURE retained raw cells, remap/dependencies, DROP boundary and actual stream replay |
| `regproc_persistence_test.go` | `TestReferenceRegProcFeatureSmoke` | B and exact smoke | New feature acceptance smoke |
| `regproc_mixed_controls_test.go` | `TestReferenceRegProcMixedRolePersistence` | B and remedy smoke | Supported routine/role composite row/default/view serialization, displaced target-role identity and actual emitted-stream replay |
| `regproc_mixed_operator_test.go` | `TestReferenceRegProcMixedOperatorPersistence` | D and R4 smoke | Actual mixed routine/operator/type capture, row/default/view persistence, restored-default INSERT, retained output and emitted-stream replay |
| `regproc_materialized_test.go` | `TestReferenceRegProcMaterializedPersistence` | E and R5 smoke | Direct/nested materialized frozen definitions, routine/operator/type/role authority, actual controls and refresh, stale/retained rows and actual emitted-stream replay |
| `regtype_alias_syntax_test.go` | `TestReferenceRegtypeAliasModifierSyntax` | C | Modified shared decoded-type keyword admission proof |

The eight RegProc definitions were authored and oracle-corrected in the prior
test-only stage; Stage C preserves their full expectations. No public definition
was migrated, and no other preexisting public definition was modified.

### Transaction and command identifiers

The shared XID definitions independently assert canonical unsigned input/output,
distinct scalar/companion identities and metadata, exact diagnostics, asymmetric
XID/INT4 equality, CID equality-only and unsigned XID8 comparisons/order. They
exercise documented low32 XID8→XID and string I/O casts, direct/held parameters,
NULL/typed-nil bytes, raw byte ownership, retained metadata and persistent
defaults/CHECKs/views. Native controls replay the actual emitted dump on the
pinned PostgreSQL oracle; native array/system-column capability boundaries do
not claim PostgreSQL lacks those mechanisms.
Identifier array annotations reject at type admission, before typed NULL or datum
construction. Prepared native CREATE TABLE validates this capability at execution;
SELECT cast annotations reject at Prepare. Persistent identifier view literals
retain explicit scalar casts without accumulating redundant wrappers across
Dump/Restore cycles.
ConditionalEquality, ExtremaOrdering and PublicDeparse regressions cover existing
consumer cutover: NULLIF/simple CASE directional equality and unknown operands;
XID/CID extrema rejection at execute with absent Position/Hint even empty/all-NULL;
unsigned XID8 extrema; and exact public default/CHECK deparse annotations before
and after native Restore. Direct and actual held routes share independent
expectations confirmed first on pinned PostgreSQL, not native-derived goldens.

Oracle-first discovery established pinned PostgreSQL18.0 outcomes before native
implementation. No discovery filters, deliberate observation failures or control
deferral guards remain. The existing-provider XID8 opclass identities have narrow
real-catalog assertions; metadata keeps native XID8 despite pq's empty name.
Shared `regtype` lookup proves the fixed scalar/companion identities without
claiming native `pg_type` relation support. Physical widths4/4/8 remain checked
at the owned descriptor/binary seams; genuine `pg_type` projection is an explicit
PostgreSQL-only catalog boundary. The xid8→xid cast uses fixed function5071 and
explicit/function metadata; generated cast row identities stay backend-owned.
Root must verify the completed identical definitions in both modes, each with a
900-second external deadline:

```sh
make check RUN='^TestReferenceXID(ValuesMetadata|InputDiagnostics|Operators|Casts|BindsAndNulls|DumpReplay|FeatureSmoke)$'
make check RUN='^TestReferenceXID(ValuesMetadata|InputDiagnostics|Operators|Casts|BindsAndNulls|DumpReplay|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceXID(ConditionalEquality|ExtremaOrdering|PublicDeparse)$'
make check RUN='^TestReferenceXID(ConditionalEquality|ExtremaOrdering|PublicDeparse)$' REFERENCE=1
make check RUN='^TestReferencePGCast(Metadata|Exclusions|Controls|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGCast(Metadata|Exclusions|Controls|Diagnostics|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceXIDFeatureSmoke$'
make check RUN='^TestReferenceXIDFeatureSmoke$' REFERENCE=1
```

Smoke expects max-width/NULL direct and held values, distinct metadata/raw bytes,
admitted comparisons/cast results, exact unsupported operator/cast diagnostics,
native persistent roundtrip and actual emitted-dump replay. Selected checks do
not replace whole-project quickcheck or post-merge finalcheck. Test definitions
are not passing evidence until root records successful execution.


### Regtype completed definitions

The `^TestReferenceRegtype` shared-body family encodes pinned PostgreSQL 18.0
diagnostics, metadata, raw transport, warning timing and held literal identity.
Every released/initialized name and array is exercised through regtype SQL
without requiring a native pg_type projection. The complete identity facts and
fixed-versus-allocated provenance are described in
[the inventory handoff](../../docs/design/regtype-pg18-oracle.md).

The completed input and authorization scenarios preserve generic qualified
varchar modifier witnesses and use a superuser-owned denied table in an
accessible reader-owned schema. `TestReferenceRegtypeAliasModifierSyntax`
preserves rejected string/parenthesized alias modifiers across all six routes,
including held runtime input. `TestReferenceRegtypeHeldLiterals` and
`TestReferenceRegtypeWarnings` cover authority-sensitive identity capture and
literal-versus-parameter warning timing. No discovery filters, observation
logging or deliberate discovery failures remain.

`TestReferenceRegtypeOutputAuthority` and its held/atomic/native-control companions
exercise selected VARCHAR IO, TEXT/VARCHAR assignment, domain sources, stored
defaults and admitted ALTER-column conversions with actual Dump replay and
retained string metadata. The unrelated-role
family requires unchanged held OIDs and no repeated warnings after unrelated
CREATE/DROP ROLE, while its qualified-authority control requires literal/fresh
Prepare 42501 Position8, frame 42501 with portal Context, and owner-role recovery.
Run the completed review-remedy witnesses in both modes before review, each with
a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
make check REFERENCE=1 RUN='^TestReferenceRegtype(OutputAuthority|UnrelatedRoles|ArrayDisplacement)'
```

Validate the same completed definitions in both modes before review; each
quickcheck invocation has a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype'
make check REFERENCE=1 RUN='^TestReferenceRegtype'
make check RUN='^TestReferenceRegtypeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRegtypeFeatureSmoke$'
```

The feature smoke independently asserts name/OID conversion, builtin and user
row identities, qualification/shadowing, held input recovery, metadata,
persistent scalar/domain/array values and native control atomicity. Native
controls must replay the actual emitted native stream on PostgreSQL; never
substitute a manually reconstructed stream or an invented PostgreSQL
Dump/Reset/Restore API. The unchanged cohesive fixture's `checkRaw` hook supplies
independent REGTYPE/array transport checks through the small regtype helper.
These selectors do not replace ordinary quickcheck or the post-merge finalcheck
under its single 1800-second deadline. Completed definitions are not passing
verification until the root records successful runs.

Post-merge replay reliability retains all 40 mixed creator/order cases (five
creator kinds, four unlike counterparts, both orders) in the same catalog,
every checkpoint, late invalid Restore rollback, second Restore and fresh
default writes for both named elements of every pair. Expectation OID probes
are batched once per catalog checkpoint; positivity and pairwise distinct
identities, display/raw carriers/metadata and actual direct/prepared checks
remain unchanged. Identity results are refreshed after catalog replacement,
not cached across generations. Shared checker migration also includes
ArrayDisplacementControls.

MixedReplay alone gives each cohesive phase a fresh 30-second context rooted
in `t.Context()`: backend borrowing/schema/search-path setup, creator-pair
setup, identity batch, each complete case check, each data witness, Dump,
replacement/retained-cursor observation, and actual emitted PostgreSQL replay.
The test locally repeats the shared wrapper's exact setup so native-first work
cannot exhaust the original context before PostgreSQL borrowing even begins;
schema cleanup registration, backend order and notice provenance are unchanged.
Foreign replay keeps the same target fixture,
spacer-before-Restore, pools, recorder and borrowed physical connection through
setup, full validation, invalid Restore, validation, redump, second Restore,
validation and fresh default writes. Only a shallow local Pair view's `Ctx`
changes; no fixture-global deadline or API is changed. Each phase cancels on
exit, and retained rows are read/closed after replacement but before their
phase cancels or the unrelated full matrix resumes. Failure output includes
the phase label, elapsed time and context error.

The intentionally invalid late Restore must report 22P02 with the exact
`invalid input syntax for type integer: "bad"` cause while its context remains
live; a deadline is not evidence of failure atomicity. The full checks still
follow that failure and both successful restores, and PostgreSQL still executes
the actual emitted native dump, never a reduced hand-written script.

For the phase-lifetime remedy and feature smoke, run the complete MixedReplay
definition, oracle first, with an external 900-second deadline per invocation:

```sh
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$'
make check RUN='^TestReferenceRegtypeArrayDisplacementMixedReplay$' RACE=1
```

Expected observations are the complete 40-case source and restored identity,
logical name, direct/prepared metadata/data and native retained-row contracts,
unchanged state after the independently diagnosed invalid Restore, successful
redump/second Restore and two rows after every fresh default INSERT. Real mode
also validates actual emitted SQL on pinned PostgreSQL. These are required
outcomes, not a claim that the root has already verified this remedy.

The broader shared-checker regression selectors remain:

```sh
make check RUN='^TestReferenceRegtypeArrayDisplacement(Controls|MixedReplay)$'
make check RUN='^TestReferenceRegtypeArrayDisplacement(Controls|MixedReplay)$' REFERENCE=1
make check RUN='^TestReferenceRegtypeArrayDisplacement(Controls|MixedReplay)$' RACE=1
```


### Regtype control lifetime

Regtype control witnesses retain a supported TEMP SEQUENCE across Reset/Restore
to fence physical-session ownership and allocator collisions. Persistent
domains supply the user-defined TypeClassID remapping witnesses. Temporary
domain creation remains an adjacent native 0A000 boundary, even in an already
initialized namespace; the path scenario asserts that boundary natively and
uses PostgreSQL-only temporary-domain setup for additional oracle context.
