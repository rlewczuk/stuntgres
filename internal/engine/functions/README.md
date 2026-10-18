# Builtin function implementations

This package is the home of builtin functions: signatures and argument rules,
immutable call nodes, evaluation semantics, aggregate provider/state
algorithms and, family by family, the bind code and its registry entry. It is
a leaf library: it imports [`expr`](../expr/README.md), `ids`, `sqltype`,
`parser` and `sqlstate`, never the root engine or the engine's catalog,
storage, lock or visibility state. It reaches the engine only through
`Binder` at bind time and `expr.Context` at evaluation time. `expr` owns
`RoutineReference`, `RoutineRecipeReference`, `RandomOwner` and
`IntrinsicPropertyRule`; this package keeps aliases for them until step 2d.

Function nodes are plain types over the `expr` contract: they embed
`expr.Info`, hold children as `expr.Expr` and evaluate with `Eval(expr.Context)`,
using only the `expr.Context` capabilities they need. `StringVariadic.Origin`
is an opaque `any` that the engine stores and reads; this package never
interprets it. The only remaining type parameter is the private
`randomInteger` sampler source, a numeric kernel detail rather than part of the
expression contract.

## Binder

`Binder` (`binder.go`) is what bind code may ask of the engine's analyzer; the
engine's `expressionBinder` implements it with thin adapters
(`internal/engine/function_binder.go`). Each method is a reviewed decision:

- `AnalyzeArgument` / `CoerceArgument`: occurrence-owned argument analysis and
  formal coercion, selected by intent. Comparison operands and conditional
  coercion are intents, not separate methods.
- `Type`: the binding catalog's builtin descriptor (identity comparisons and
  `Usage()` need the descriptor, not a usage).
- `Info`: node metadata; records type and collation dependencies.
- `BuiltinVisible`: whether function lookup reaches `pg_catalog` under the
  current `search_path`.
- `BuiltinRoutineInfo` / `BuiltinRoutineReference`: the selected builtin
  routine identity, keyed by `RoutineProvider`, name and declared formals.
  `RoutineProvider` (`routine_provider.go`) is the family vocabulary of the
  engine's routine declarations.
- `DiagnosticTypeName`: PostgreSQL's diagnostic spelling, including qualified
  user-defined types.
- `SignatureError`: the 42883 missing-function error. The engine also notes it
  to decide whether `f(x)` may fall back to composite field selection, so bind
  code must report missing signatures through it.
- `AnalyzeFilter` (added beyond the initial set for the UUID pilot; 27 glue
  files analyze FILTER this way): analyzes and discards a scalar call's FILTER
  so its errors precede the decoration rejection.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Registry

`registry.go` holds a static `definitions` table; there is no `init()`
registration. `Lookup(name)` claims a call by its unqualified name, compared
case-insensitively; the definition's `Bind` owns exact-spelling resolution,
visibility, argument analysis and every diagnostic. The engine's
`bindFunctionProvider` consults `Lookup` before its remaining name chain, so a
registered name must not overlap any family the chain still dispatches.

Shared scalar call helpers live in `call_syntax.go`: `ValidateCallSyntax`
(FILTER analysis, the 100-argument limit, labels), `CallDecorationError`
(DISTINCT/ORDER BY/FILTER/OVER on a scalar) and `NamedArgumentType`.

To add or port a builtin family:

1. In the engine, turn `func (b *expressionBinder) bindX(...)` into
   `func bindX(b functions.Binder, ...)` (F4). The compiler then proves the
   interface is enough; record any new `Binder` method above.
2. Move it here, add its `Definition`, and delete its engine branch and glue
   file (F5). Its unit tests move with it unless they exercise engine
   switches.
3. The chain applies shared gates after some branches: argument labels are
   rejected for most names, and `ResolutionOnly` calls get format diagnostics.
   A family whose engine branch sat after those gates must reproduce them in
   `Bind`, because `Lookup` runs first.

## What stays in the engine

The engine owns the analyzer behind `Binder`, the bind code of families not
yet registered, name/catalog/ACL and dependency resolution, parameter/result contracts, descriptor generations,
retag/remap and stored SQL presentation. The capabilities supply existing
authority without copying rows or the full evaluation context.

Physical-session RNG, UUID clock/entropy and SQL time ownership stay in the
engine. Function evaluation borrows the same owners only when demanded. Literal
NULL folding and reached runtime argument effects remain distinct. Aggregate
providers own execution-local state; the engine owns FILTER/DISTINCT/ORDER,
grouping, windows and state lifetime. Query/set expansion and physical regex
executor ownership remain engine responsibilities. There is no alternate binder,
dynamic plugin registration, compatibility forwarding carrier or duplicated
type algorithm.

## Family notes

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
replay identity and narrow strong-entropy/actual-FIPS context capabilities.
`internal/sqltype/pgcrypto.go` owns admitted signature/result plans, raw digest/HMAC,
validated bf salt costs and random-byte count bounds. The adjacent public-domain bcrypt kernel uses fixed
Blowfish state, byte-oriented72-byte key expansion, PostgreSQL2a/2x semantics,
canonical custom base64 and per-round cancellation. Six hashes return raw BYTEA.
The same carrier supports the four BYTEA-only raw AES overloads, including four
children for `_iv`; unknown raw slots infer BYTEA rather than HMAC's TEXT
preference. `internal/sqltype/pgcrypto_aes.go` owns stdlib AES CBC/ECB, PKCS/none,
fixed effective key/IV buffers, full padding validation and bounded cancellation.
Neither the comparable signature plan nor the carrier retains frame key/IV/mode.
crypt/gen_salt return TEXT. Volatile gen_salt and gen_random_bytes request
entropy once only after demanded children, strict NULL handling and count/name
validation. Random bytes validate1..1024 before using at most1024 bytes of scratch.
The installed UUID wrapper and core V4 calls share one private generation helper,
using the core entropy capability, V4 packer and UUID value constructor, while
retaining the engine's distinct installed procedure identity.
fips_mode obtains actual execution mode through the engine's crypto/fips140.Enabled()
capability, not an environment parser, constant or OpenSSL dependency.
All reached children run once in source order. Plans retain no password, salt,
entropy or frame-specific algorithm across executions. Builtin md5 still returns
hexadecimal TEXT.
Closed volatile pgcrypto DEFAULT recipes use the same provider-identity and closed-tree
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
cohesive selections. Registry shape and claims:

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed)$'
```

Focused private state/demand examples:

```sh
make check RUN='^(TestSumProviders|TestSumIntegralTransitions|TestSumFloatingTransitions|TestSumProviderCancellation)$'
make check RUN='^(TestRandomIntegerSampler|TestRandomRuntimeNullEvaluatesSibling|TestUUIDGenerationPacking|TestUUIDGenerationAscendingClock)$'
make check RUN='^(TestDatetimeSleepWait|TestDatetimeSleepCleanup|TestQueryAggregateRealAdvanceFailure)$' RACE=1
make check RUN='^TestPgcrypto(OverloadResolution|RawVectors|StrictDemand|Cancellation)$'
make check RUN='^TestPgcrypto(Password(Entropy|Volatility)|Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation))$'
make check RUN='^TestPgcryptoAES(Vectors|KeyIV|Padding|Parser|Cancellation|Carrier)$'
make check RUN='^TestReferencePgcryptoAESFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePgcryptoAESFeatureSmoke$'
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
per-evaluation allocation. It is the first registered family:
`uuid_extract_bind.go` binds both names through `Binder`, which supplies
catalog-owned UUID and TIMESTAMPTZ/INT2 usages. Engine retag/remap reconstructs
the same carrier with fresh usages rather than retaining source descriptors.

```sh
make check RUN='^TestUUIDExtract(Vectors|VariantMatrix|VersionMatrix|StrictDemand|Cancellation|Properties|Identity|Remap|ScalarLifecycle|ParserAdmission)$'
```

Public cases and standalone feature smoke share the
[UUID extraction reference scenarios](../../../reference/types/README.md#uuid-extraction-selected-proof).
Run selected checks with an external 900-second deadline; the root owns
whole-project verification.

Unit fakes implement the plain contract: fake arguments embed `expr.Info`, and
fake contexts embed the nil `unitCapabilities` (`expr.Context`) so they
implement only what the code under test calls; any other call panics.

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
