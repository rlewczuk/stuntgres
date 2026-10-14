# Builtin function implementations

This package owns builtin-specific signatures and argument rules, immutable scalar
call carriers, evaluation semantics and aggregate provider/state algorithms. It
reuses `internal/sqltype` selected plans, coercion, values and input/output codecs;
it does not import the engine or public adapters.

The engine owns generic expression metadata and binding, name/catalog/ACL and
dependency resolution, parameter/result contracts, descriptor generations,
retag/remap and stored SQL presentation. `Info[P]` carries the engine's opaque
properties; narrow typed argument/context capabilities supply existing authority
without copying rows or the full evaluation context.

Physical-session RNG, UUID clock/entropy and SQL time ownership stay in the
engine. Function evaluation borrows the same owners only when demanded. Literal
NULL folding and reached runtime argument effects remain distinct. Aggregate
providers own execution-local state; the engine owns FILTER/DISTINCT/ORDER,
grouping, windows and state lifetime. Query/set expansion and physical regex
executor ownership remain engine responsibilities. There is no alternate binder,
plugin registry, compatibility forwarding carrier or duplicated type algorithm.

`DaitchMokotoff` owns the complete PostgreSQL REL_18_0 phoneme chart, narrow
UTF8/Latin1 scanner and correlated digit-prefix state machine. Codes retain
completion/padding order and all alternatives; no-encodable input returns nil
for the engine's typed SQL NULL. State and result buffers are evaluation-local,
with incremental cancellation during scans/lookahead/expansion. Installed
fuzzystrmatch identity, strictness, immutable binding, owned text[] construction,
generated consumers and control remapping remain engine responsibilities.

## Testing

Run from repository root; use `make check RUN='...'` for individual or small
cohesive selections. Focused private state/demand examples:

```sh
make check RUN='^(TestSumProviders|TestSumIntegralTransitions|TestSumFloatingTransitions|TestSumProviderCancellation)$'
make check RUN='^(TestRandomIntegerSampler|TestRandomRuntimeNullEvaluatesSibling|TestUUIDGenerationPacking|TestUUIDGenerationAscendingClock)$'
make check RUN='^(TestDatetimeSleepWait|TestDatetimeSleepCleanup|TestQueryAggregateRealAdvanceFailure)$' RACE=1
```

```sh
make check RUN='^TestDaitchMokotoff(PublishedCodes|Boundaries|Cancellation|ProviderIdentity|ExpressionIdentity|Remap)$'
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
```

True leaf units live here; engine-integrated binding, parameter, descriptor,
publication, native-control, grouping/window and resource-cleanup assertions stay
with their engine owner. Borrowed array/format payload invariants accompany the
NULL-count and zero-delay sleep allocation checks; no test-only production state
API is needed to seed malformed aggregate state.

Public behavior remains in the unchanged dual-mode `reference/` definitions.
Their owning READMEs specify exact scalar/aggregate, held prepared, diagnostics,
authorization, cancellation and stored/native-control selections. For example:

```sh
make check REFERENCE=1 RUN='^TestReferenceUUIDGeneration(BitsMetadataAndOrder|Signatures)$'
make check RUN='^TestReferenceUUIDGeneration(BitsMetadataAndOrder|Signatures)$'
```

Selected proof uses an external900-second command deadline; use `quickcheck` for
whole-project Docker-free verification, `racecheck` for the full quick-mode race
suite, and `bench` for five fresh-engine Ping/Close startup measurements. The
post-integration `finalcheck` deadline is1800 seconds. This extraction adds no SQL
surface or oracle-version changes.
