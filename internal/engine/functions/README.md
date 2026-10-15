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

Installed pgcrypto calls reuse this package's SQL-usage overload selection,
formal coercion hooks and reconstructible `Pgcrypto` carrier. The engine separately
owns installed procedure address, namespace/ACL, intrinsic volatility, dependency,
replay identity and the narrow strong-entropy context capability.
`internal/sqltype/pgcrypto.go` owns admitted signature/result plans, raw digest/HMAC
and validated bf salt costs. The adjacent public-domain bcrypt kernel uses fixed
Blowfish state, byte-oriented72-byte key expansion, PostgreSQL2a/2x semantics,
canonical custom base64 and per-round cancellation. Six hashes return raw BYTEA;
crypt/gen_salt return TEXT, with volatile gen_salt entropy requested once only
after demanded children, strict NULL handling and name/count validation.
All reached children run once in source order. Plans retain no password, salt,
entropy or frame-specific algorithm across executions. Builtin md5 still returns
hexadecimal TEXT.
Closed gen_salt DEFAULT recipes use the same provider-identity and closed-tree
admission at binding and serialization; unrelated volatile producers and
row/parameter/stage-dependent recipes remain excluded. This does not weaken
the separate generated-column/expression-index immutability rules.

Installed fuzzystrmatch Levenshtein calls reuse shared SQL-usage analysis and
formal coercion: TEXT,TEXT followed by INT4 costs/bound, with INT4 results.
`Levenshtein` owns the immutable selected call, strict demand, reconstruction
and validation; the engine wrapper owns installed identity/ACL/dependencies.
The REL_18_0 kernel uses fixed local rune/row buffers, exact codepoint equality,
empty-before-limit behavior, signed int32 intermediates and release band slides.
No result/cost/bound cache or devel overflow guard is introduced.
Ordinary named-argument parsing retains the complete argument/label list,
including empty label slots for a positional prefix before a named suffix.
The installed Levenshtein binder reuses shared malformed-label validation before
missing-signature resolution; these overloads declare no SQL argument names.

Installed Metaphone calls use the same shared analysis/formal-coercion contract:
TEXT plus INT4 for `metaphone`, unary TEXT for `dmetaphone`/`dmetaphone_alt`,
all returning TEXT. `Metaphone` owns strict source-order demand, validation and
reconstruction; the installed engine wrapper owns identity/ACL/dependencies.
Exactly these three actual call heads reuse the parser's existing full scalar
route, preserving qualified spelling, source positions, named labels and
positional-prefix VARIADIC for normal signature rejection. Bare identifiers
remain identifiers; no general VARIADIC grammar or callable overload is added.
The single kernel retains REL_18_0's nontraditional rules and255-byte boundaries.
Double Metaphone borrows the input through an ASCII-uppercase byte reader with
five virtual trailing spaces, caches full-input SlavoGermanic classification,
and uses independent fixed4-byte outputs. No input-sized uppercase copy, rune
conversion, padding allocation, result cache or alternate fallback is introduced.
Long scans poll the borrowed evaluation context.

Single Metaphone retains its complete release PGDG/Schwern/Philips provenance.
The Double Metaphone files retain the complete Maurice Aubrey/Text::DoubleMetaphone
and North Carolina State Highway Patrol notices. The Perl original Artistic
alternative and full terms are in `dmetaphone_LICENSE.txt`; modified source is
Freely Available under clause3(a). Binary redistributors should accompany it
with machine-readable modified source under clause4(b), or satisfy another
applicable license condition. This does not relicense the distinct NC permission
or imply copyright-holder endorsement.

Installed `Soundex` owns TEXT argument coercion, selected TEXT/INT4 results,
strict source-order sibling demand, validation and reconstruction. Its engine
wrapper owns actual provider identity/ACL/dependencies. The byte kernel borrows
input, polls context on long scans, retains immediately preceding original-byte
codes (including B1B digit collision), and uses fixed four-byte buffers.
No-ASCII-letter buffers remain zero; difference compares all four positions
without allocating code strings, so two empty/no-letter inputs agree4.
There is no input-length limit, Unicode normalization or transliteration.

## Testing

Run from repository root; use `make check RUN='...'` for individual or small
cohesive selections. Focused private state/demand examples:

```sh
make check RUN='^(TestSumProviders|TestSumIntegralTransitions|TestSumFloatingTransitions|TestSumProviderCancellation)$'
make check RUN='^(TestRandomIntegerSampler|TestRandomRuntimeNullEvaluatesSibling|TestUUIDGenerationPacking|TestUUIDGenerationAscendingClock)$'
make check RUN='^(TestDatetimeSleepWait|TestDatetimeSleepCleanup|TestQueryAggregateRealAdvanceFailure)$' RACE=1
make check RUN='^TestPgcrypto(OverloadResolution|RawVectors|StrictDemand|Cancellation)$'
make check RUN='^TestPgcrypto(Password(Entropy|Volatility)|Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation))$'
```

```sh
make check RUN='^TestDaitchMokotoff(PublishedCodes|Boundaries|Cancellation|ProviderIdentity|ExpressionIdentity|Remap)$'
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestLevenshtein(Distance|Weights|Bounds|ReleaseInt32|Unicode|Cancellation|NamedArguments)$'
make check RUN='^TestLevenshtein(ExpressionIdentity|Remap)$'
make check RUN='^TestMetaphone(Signatures|Codes|Boundaries|StrictDemand|Cancellation|ExpressionIdentity|Remap|ParserAdmission)$'
make check RUN='^TestDoubleMetaphone(Codes|Boundaries|Cancellation|Allocations)$'
make check RUN='^TestSoundex(Kernel|Expression|ExpressionIdentity|ExpressionRemap|ExpressionProperties)$'
```

Range inspection/hull carriers retain same-generation `sqltype` plans and at
most two operands. Literal NULL folding follows existing strict demand rules;
a reached runtime NULL never skips a demanded sibling error/effect.
Selected units: `make check RUN='^TestRangeInspect'`.
Public nine-family, metadata/diagnostic/ACL, held-bind and native stored-recipe
witnesses live under
[range inspection selected proof](../../../reference/types/README.md#range-inspection-selected-proof).

UUID extraction uses one strict unary `UUIDExtract` carrier with exact immutable
intrinsic properties. It classifies the two variant bits before reading the
version nibble; only v1/v7 produce timestamps. Unsigned integer arithmetic
preserves PostgreSQL 18.0 truncation without clock, entropy, formatting or
per-evaluation allocation. The binder supplies catalog-owned UUID and
TIMESTAMPTZ/INT2 usages; retag/remap reconstructs the same carrier with fresh
usages rather than retaining source descriptors.

```sh
make check RUN='^TestUUIDExtract(Vectors|VariantMatrix|VersionMatrix|StrictDemand|Cancellation|Properties|Identity|Remap|ScalarLifecycle|ParserAdmission)$'
```

Public cases and standalone feature smoke share the
[UUID extraction reference scenarios](../../../reference/types/README.md#uuid-extraction-selected-proof).
Run selected checks with an external 900-second deadline; the root owns
whole-project verification.

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
