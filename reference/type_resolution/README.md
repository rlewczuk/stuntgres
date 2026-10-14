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
composite domains. Composite arrays remain excluded. Independent named-OID
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
CHECK-grid native array-to-array equality failures and domain-constraint admission
failures are independently asserted broader capability boundaries; PostgreSQL
still verifies the admitted array/domain shapes and exact scalar-Const ban.
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
Native arrays of composites and scalar REGROLE stored constants remain unsupported.

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

Post-merge replay reliability retains all40 mixed creator/order cases in the same
catalog, every checkpoint, late invalid Restore rollback, second Restore and
default recovery. Expectation OID probes are batched once per catalog checkpoint;
positivity and pairwise distinct identities, display/metadata and actual
direct/prepared checks remain unchanged. Identity results are refreshed after
catalog replacement, not cached across generations. Shared checker migration
also includes ArrayDisplacementControls. Fixture deadlines are not extended.

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
