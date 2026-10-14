# reference/type_resolution

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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

### Regtype control lifetime

Regtype control witnesses retain a supported TEMP SEQUENCE across Reset/Restore
to fence physical-session ownership and allocator collisions. Persistent
domains supply the user-defined TypeClassID remapping witnesses. Temporary
domain creation remains an adjacent native 0A000 boundary, even in an already
initialized namespace; the path scenario asserts that boundary natively and
uses PostgreSQL-only temporary-domain setup for additional oracle context.
