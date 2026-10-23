# internal/sqltype

SQL type descriptors and values, text and binary codecs, coercion, comparison,
operator resolution and the type-owned evaluation plans that builtins bind.
Catalog namespaces, ownership and atomic publication belong to the engine's
`catalog`/`typedef` owners; socket framing belongs to `cmd/stuntgres`; FTS
vector/query algebra lives in `internal/engine/fts` (a leaf below this package).

## Imports

Production code imports only the standard library, `sqlstate`, `engine/fts` and
its subpackages `unicode16` (Unicode 16.0.0 tables) and `pgencoding18`
(encoding conversion; provenance in its README). `tzdata/` is embedded
time-zone data (`go:embed`; provenance in its README). Most engine packages and
`cmd/stuntgres` import this package.

## Descriptors, values and codecs

- A `*Descriptor` owns text input/output (`Input`, `Output`, `SQLLiteral` and
  their `*WithEnvironment` forms) and binary receive/send
  (`BinaryInputWithEnvironment`, `AppendBinaryWithEnvironment`). Engine consumers supply the real captured temporal,
  locale, catalog, role and domain-check environments. Server Bind, row output
  and FunctionCall reuse these methods; there is no conversion registry. The
  embedded `database/sql` converter keeps its Go-value contract and never treats
  arbitrary byte slices as wire binary input.
- A `Usage` is descriptor + typmod + collation. `Value` is an immutable
  concrete datum; shared payloads (arrays, composites, geometry, JSONB) are
  never mutated after construction. Exact datum identity (`SameDatum`,
  `Value.SamePayload`) compares stored bits, independently of SQL equality.
- Domains delegate physical codecs to their base while keeping their logical
  identity and checking fresh input. `Descriptor.RebindDomain` rebuilds a
  same-ID immutable domain generation with current naming and base authority,
  preserving own CHECK/NOT NULL and recomputing inherited flags.
- Arrays and composites recursively keep their member usages and NULL
  identities. NULL belongs to the caller's framing: it is not the empty text or
  byte value or the zero `Value`.
- `Value.Retag` remints descriptor ownership without changing the datum. It
  requires the same type ID, domain/base identity and domain base constraints,
  and rejects foreign citext generations and enum members even for equal OIDs.
  Only source-authorized catalog import uses explicit remaps such as
  `RemapCitext`.
- Capabilities (comparison, ordering, grouping, hashing, index, binary
  providers, storage admission) come from descriptor providers, never from a
  catalog OID or from scalar operators alone. A missing provider fails with its
  own diagnostic; no path falls back to text while claiming binary format.
- `ParseInt8` reuses bigint input spelling and diagnostic precedence for numeric
  COPY options; do not add another integer parser.

### Text-array input

Text-array input validates each declared dimension and the rank limit early,
then demands the actual contents. A cumulative declared product does not preempt
child input: a child conversion or hard domain CHECK error precedes the
containing extent mismatch. Element storage grows only with parsed children,
never with the declared product. The shared `ownedArray` constructor keeps its
product, dimension, element-count and descriptor guards for every builder.

### Soft input errors

`InputSoftError` decides whether a conversion failure is recoverable:

- It first reads a direct `sqlstate.InputError` origin marker. Producers mark
  only their own save-context errors. Explicit hard markers cover regtype
  parser/typmod helpers, LINE construction arithmetic, JSONPath numeric and
  recursive helpers and domain CHECK execution, even when their causes carry
  input-like SQLSTATEs or a nested soft marker.
- Unmarked primitive input keeps the exact-code fallback list in
  `input_error.go`. The classifier never unwraps errors and never treats a whole
  SQLSTATE class as recoverable.
- Only COPY and JSONB call it, at their fresh destination conversion seams.
  Presentation helpers may copy public diagnostic fields but must keep the
  marker.
- Further source-soft origins: array dimensions, text range reversal, JSONPath
  lexer/grammar/Unicode/regex flags, BIT width, decoded JSONB Unicode NUL, FTS,
  query_int and ACL names/role lookup. The shared range canonicalizer gets
  text-save authority only from text input, never from binary input or
  operators. The JSONPath validation-only regex adapter marks its own compiler
  result, never cancellation or a whole parser/regex return.
- SQL/JSON semantic policies (`IS JSON`, query ON ERROR, behavior conversion)
  inspect input annotations themselves and never use or widen this classifier;
  ordinary errors and cancellation keep their identity.

## Coercion and operator resolution

- `PlanCoercion(source, target, context)` admits only bounded directional
  edges, never transitive casts, and returns an immutable `CoercionPlan`.
  `Rebind` keeps admitted binary and SQL/JSON conversion operations across
  descriptor generations without consulting a later catalog edge.
  `CoercionPlan.FunctionExpression` exposes the selected cast/typmod function
  for binding-time DEFAULT shape validation (relabel/IO/domain wrappers inspect
  their input; array wrappers also the element expression). It never evaluates.
- `SelectCommonUsage` is the single domain/type/typmod fold for common
  expressions and anycompatible inference.
- Generic operator resolution applies preferred-type facts before the
  unknown-input category fallback; exact signatures still win. This is
  type-based overload selection, never a special case for a literal spelling or
  caller.
- `ResolveOperatorWithCoercion(candidates, left, right, unary, can)` shares the
  ranking body with `ResolveOperator`. The optional pure
  `can(source *Descriptor, target ID) bool` predicate supplies implicit
  feasibility from the caller's captured catalog; it is never stored or called
  during evaluation, and it is used in both directions for compatible
  polymorphic selection, final leaf checks and unknown-as-known viability.
  `ResolveOperator` (nil predicate) keeps fixed-provider behavior.
- `InputSessionDependent`/`OutputSessionDependent` classify the codec
  procedures actually invoked, for fixed coercions and catalog INOUT casts,
  following the release `pg_proc.dat` IO declarations. Domains follow the
  invoked base codec, not a synthetic `domain_in` call.

### Binary datum conversions

- `PlanBinaryCoercion(source, target)` captures exact descriptor pointers and
  one immutable representation mapping. `Apply(ctx, value)` checks cancellation
  and source ownership (including typed NULL) and returns the captured target.
  It never consults a catalog, dispatches a routine, uses text I/O or wire
  send/receive as a memory layout, or weakens `Retag` identity checks.
- `PlanDeclaredBinaryCoercion` carries that operation in an ordinary
  `CoercionPlan` after catalog authorization, including source-domain
  projection; its `Rebind` keeps the selected representation family and never
  falls back to IO.
- Supported families map one inline 32-bit word, one inline 64-bit word, shared
  immutable octets (TEXT/VARCHAR/BPCHAR/JSON/CITEXT/BYTEA, without parsing,
  trimming, folding or decoding), or shared structured payloads (BIT/VARBIT,
  INET/CIDR, BOX/LSEG, LINE/CIRCLE). Scalar words and byte families allocate no
  per-value buffer. The engine owns CREATE CAST permissions, physical/category
  validation, dependencies and publication.

### FLOAT4 raw words

`Float4FromBits` and `Value.Float4Bits` expose exact IEEE binary32 authority,
including signaling NaNs, NaN payloads, subnormals and signed zero. Widened
`floating` is presentation/arithmetic state; an inline raw32 marker owns
binary-created words. Copies, Retag/import, array/composite leaves, binary
transport, `SameDatum` and `SamePayload` keep the word. Ordinary constructors
and arithmetic/conversion results derive their own rounded word and never
inherit a stale one. Dump's stored-literal renderer reads `Float4Bits`.

## Binary providers

- `Descriptor.CheckBinaryInput` and `CheckBinaryOutput` share the missing-provider
  boundary with receive/send dispatch without converting a fabricated value.
  Domains delegate to the base owner; generic array/composite send providers do
  not eagerly resolve members. `CheckBinaryInputDeclaration` resolves a declared
  receiver without preparing its invocation (a domain always has the generic
  domain receiver; the base receiver resolves at actual invocation).
- Initialization timing is part of the contract: binary COPY TO initializes
  send providers after CopyOutResponse and before any row demand (so empty
  results still fail); binary COPY FROM runs declaration setup for every live
  physical column before CopyInResponse; binary NULL Bind arguments initialize
  receive availability before domain constraints; ordinary row output
  initializes send on the first delivered tuple only; FunctionCall NULL results
  are a framing shortcut.
- Structured receive owns its decoded storage, validates member
  OIDs/generations/typmods and grows only after successfully received or NULL
  members, never from an announced cardinality. Wire OIDs never grant catalog
  identity. Numeric binary I/O is exact.

## Type families

One immutable plan per family is bound at analysis (`Bind<Family>`, usually
validating every formal's `Usage` and same-catalog authority before strict NULL
handling) and evaluated with `Evaluate(ctx, ...)`; per-call state is
invocation-local. Checked arithmetic order in geometry and crypto kernels is a
contract, not an optimization opportunity.

| Family | Files | Contract notes |
| --- | --- | --- |
| Integer, numeric, float, money | `integer*`, `numeric*`, `float*`, `money*`, `arithmetic*`, `math*` | Numeric I/O never round-trips through binary float; MONEY stores minor units. |
| Strings, citext, bytea, bits | `string*`, `character*`, `citext*`, `bytea*`, `bitstring*` | Citext is a generation-owned scalar on `Value`'s string; it never folds case. |
| Date/time, intervals, zones | `datetime*`, `timestamp*`, `interval*`, `timezone*`, `civil*` | Zone rules come from embedded `tzdata`; temporal values keep microseconds and sentinels. |
| Network | `network*`, `macaddr*` | `NetworkSubnetPlan` compares inline prefix bits without parsing or heap slices; CIDR/domain coercion belongs to analysis. |
| Geometry | `point*`, `lseg*`, `line*`, `circle*`, `path*`, `polygon*`, `geometry*` | Coordinates keep IEEE bits; payloads are shared and immutable. `GeometryConversionPlan`/`GeometryMeasurePlan` read payloads directly and check cancellation in vertex loops. No geometric type gains default ordering/hash/index authority from its scalar operators. |
| JSON, JSONB, JSONPath, SQL/JSON | `json*`, `jsonb*`, `jsonpath*`, `sqljson*` | One path walker serves JSONPath and SQL/JSON query plans; temporal path items never enter persistent JSONB. `@?`/`@@` use a two-operand `BinaryPlan` with hidden immutable vars. `JSONTableRows` reuses the same evaluator. |
| Arrays, composites, records, enums, domains | `array*`, `composite*`, `record*`, `enum*`, `domain*` | See descriptors and text-array input above. |
| Ranges, multiranges | `range*`, `multirange*` | Canonicalization is shared; only text input may soft-fail it. |
| Identifiers and system types | `reg*`, `oid*`, `xid*`, `tid*`, `aclitem*`, `snapshot*`, `tree*` | XID arrays use the genuine XID companion and the shared array codec. |
| Extension types | `cube*`, `earth*`, `query*`, `uuid*` | Earth checks read cube coordinates without allocation; keep its released NaN ordering and squared accumulation (no `Hypot`). |
| Regular expressions | `pgregex*`, `regexp*`, `similar*`, `posix*`, `like*` | See below. |
| pgcrypto | `pgcrypto*` | Raw AES/BF share one parser/dispatch in `pgcrypto_raw.go`; BF shares the Blowfish state and expansion with bcrypt. Schedules and feedback are per call; streaming kernels own one reusable 4096-byte scratch buffer; `Evaluate` takes an explicit per-call notice sink (nil allowed), never global state. |
| Unicode | `unicode16/` | Normalization/case tables generated from `unicode16/data` by `make generate`; runtime has no ICU, cgo, mutable maps or host-Go Unicode dependency. |

### Regular expressions

`RegexpPlan` binds the registered regexp providers into immutable plans carrying
concrete usages, collation and selected formal roles. `Evaluate`/`EvaluateSet`
share the statement-local `PGRegexExecution`; there is no global regex registry
or prepared-program cache. `PGRegexProgram.MatchFrom` searches the original
source from a UTF8 byte boundary with explicit boolean, whole or capture demand;
only capture demand publishes numbered spans, and whole demand completes
preference selection instead of taking the boolean shortcut. Iteration,
replacement copying and result growth participate in the checked
budget/cancellation authority; published buffers and arrays are
invocation-owned. Compilation precedes start-range exhaustion.

## Testing

Units here call codec, value and plan functions directly; public SQL and wire
behavior belong in `reference/` (mostly `types`, `operators`,
`binary_json_arrays`, `patterns_encoding`, `catalogs` and `sessions`). Follow
[docs/TESTING.md](../../docs/TESTING.md).

- **Frames matched by tests.** `TestSimilarActualMatcherCancellation` and
  `TestRegexpSubstrCancellation` cancel only when specific frames are on the
  stack: `matchPGRegexStates`, `pgRegexWork.step`, `regexpFlags`,
  `RegexpPlan.evaluate` and `matchPGRegexTagged`. Renaming them breaks the tests.
  Other cancellation units match frame suffixes too (for example
  `(*Descriptor).scanJSON`, `SQLJSONPlan.Apply`, `jsonbOutput`); grep tests for
  `frame.Function` before renaming a function.
- **Allocation budgets.** `TestBinaryHashAllocations` requires equal small and
  1 MiB allocation counts (at most 10; 0 for CRC) and, measured with
  `testing.Benchmark` in 1000-evaluation batches, at most 32 KiB per evaluation
  and at most 1024 extra bytes for the large input. The pgcrypto BF/bcrypt
  allocation tests budget output plus 32 KiB scratch and 16 allocations per
  call. Build inputs and plans outside measurement.

Selectors (add `RACE=1` for cancellation and concurrency units):

- Geometry: `RUN='^Test(Point|Lseg|Circle|GeometryConversion|GeometryMeasure|EarthCubeChecks)'`
- Network subnet: `RUN='^TestNetworkSubnet'`
- Cast definitions, binary datums, raw32, IO volatility: `RUN='^Test(CastDefinition|AggregateBinaryConversionRebind|Float4NeedsRawReplay)'`
- Operator and comparison resolution: `RUN='^Test(OperatorBitUnknownPreference|ComparisonResolutionSignatures|ComparisonImplicitEligibility|ConditionalEqualitySignatures)$'`
- Regex: `RUN='^Test(RegexpFunctions|RegexpSubstr|SimilarActualMatcherCancellation)'`
- SQL/JSON and JSONPath plans: `RUN='^Test(SQLJSON|JSONTablePlan|JSONBPathOps)'`
- pgcrypto: `RUN='^TestPgcrypto'`
- Binary hashing: `RUN='^TestBinaryHash'`
- Protocol codecs: `RUN='^TestProtocolValueCodecs$'`
- Citext, XID arrays, identifiers: `RUN='^Test(Citext|PgrowlocksXIDArray|IdentifierScalarProviders|XIDScalarAuthorityAndGrouping)'`
- COPY input demand and soft errors: `RUN='^TestCopyFilter(ProviderProvenance|ArrayInputDemand)$'`
- Ordering: `RUN='^Test(SearchOccurrenceOrdering|OrderingBtree)'`
- Unicode tables: `RUN='^TestUnicode16'`

Several selectors also match engine tests of the same mechanism; that is
intended.
