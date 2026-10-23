# internal/sqltype

Descriptors own text input/output and binary receive/send. Engine consumers
supply the real captured temporal, locale, catalog, role and domain-check
environments. The server's Bind, row output and FunctionCall paths reuse these
methods; they do not add a conversion registry. The embedded `database/sql`
converter retains its existing Go-value contract rather than treating arbitrary
byte slices as wire binary input.

Domains delegate physical codecs while retaining logical identity and checking
fresh input. Admitted arrays and composites recursively retain their member
usages and NULL identities. NULL belongs to the caller's framing: it is not the
empty text/byte value or the zero `Value`.

`InputSoftError` first reads a direct `sqlstate.InputError` origin marker;
producers mark only their own save-context errors. Explicit hard markers cover
regtype parser/typmod helpers, LINE construction arithmetic, JSONPath numeric/
recursive helpers and domain CHECK execution, even when their causes have
primitive-looking SQLSTATEs or a nested soft marker. Unmarked
primitive input keeps the existing exact-code fallback. The classifier never
unwraps errors or treats an entire SQLSTATE class as recoverable. COPY and JSONB
invoke it only at their fresh conversion seams; presentation helpers preserve
the marker while copying public diagnostics.
Additional source-soft origins include array dimensions, text range reversal,
JSONPath lexer/grammar/Unicode/regex flags, BIT width, decoded JSONB Unicode NUL,
FTS, query_int and ACL names/role lookup. The shared range canonicalizer receives
text-save authority only from text input, not binary/operators; BIT's shared
assignment/binary width helper is unchanged. JSONPath's validation-only regex
adapter marks its compiler-owned direct2201B result, never cancellation or a
whole parser/regex return. No global state/message rules or valid-input rework.
Private source/noninput-caller and actual scoped-domain proof:
`make check RUN='^TestCopyFilterProviderProvenance$'` (sqltype and engine packages).
`ParseInt8` reuses bigint input spelling and diagnostic precedence for numeric
COPY options; it does not create another integer parser or catalog identity.

Text-array input validates each declared dimension and the rank limit early,
then demands actual contents. A cumulative declared product does not preempt
child input or later individual-bound diagnostics: a child conversion or hard
domain CHECK exception precedes the containing extent mismatch. Existing token,
open-brace and post-scalar depth/closing-extent checks keep their order.
Element storage grows only with actual parsed children, never the declared
product. The common `ownedArray` constructor retains its checked product,
dimension, actual element-count and descriptor guards for text input and all
other builders; binary metadata/receive guards are unchanged.
Real descriptor precedence, tiny-payload final construction and cancellation
proof: `make check RUN='^TestCopyFilterArrayInputDemand$'`. The public hard-domain
text/CSV regression is `TestReferenceCopyFilterArrayInputDemand`, run in both
native and real-reference modes.

`Descriptor.RebindDomain(base, name, declaration)` rebuilds a same-ID immutable
domain generation with current naming and immediate-base authority. It preserves
own CHECK/NOT NULL, builtin inventory provenance and associated identities while
recomputing inherited flags. Catalog namespace, ownership, recipes and atomic
publication remain with the existing catalog/type staging owners.

Generic operator resolution uses the released preferred-type facts before
unknown-input category fallback. VARBIT is preferred in bit-string category V;
BIT can already convert implicitly to VARBIT. Thus BIT plus an unknown operand
selects the genuine VARBIT concat signature instead of generic TEXT concat.
This is type-based overload selection, not a special case for empty bit literals,
NULL syntax, qualification or a particular caller. Exact signatures still win.


POINT retains inline X/Y binary64 bits, with type-set-owned FLOAT8 coordinate
and BOOL predicate result descriptors. Its optional-parenthesis pair decoder
shares `geometryScanner.number` with LSEG/CIRCLE, leaving BOX grammar unchanged.
`PointPlan` owns the eight real strict predicates; LSEG endpoint sameness uses
the shared `pointSame` kernel, including per-point exact NaN behavior.
Coordinate fetch and immutable replacement allocate no payload and preserve
the other coordinate's bits. Fixed-length subscripting retains ordinary NULL,
range, dimensional and slice precedence. Genuine scalar inequality does not
grant default equality/order/group/hash/index capabilities.

LSEG reuses the immutable four-coordinate geometry payload and allocation-free
inline POINT endpoints. Its path decoder preserves endpoint order and shares
the strtod-prefix scanner with CIRCLE without changing BOX's cube-scanner
grammar. `ComparisonPredicate` dispatches the six LSEG operators separately:
endpoint equality never calculates length; inequalities share checked coordinate
distance/scaled hypot with CIRCLE. Exact datum/payload identity retains all
coordinate bits, independently of geometric epsilon equality. Fixed-pair
fetch/copy-on-write assignment is shared with BOX. Scalar comparisons grant no
default ordering/grouping/hash/index authority; `_lseg` remains lookup-only.
Binary provider declaration/demand guards fail0A000 without adding a codec.

PATH owns one immutable nonempty point slice and a closure flag through a
pointer payload, so copying Value neither duplicates coordinates nor exposes
mutable storage. SameDatum compares closure, order and IEEE coordinate bits;
the five scalar predicates compare only point counts. PATH never grants
generic comparison/grouping/hash/index authority or companion-array admission.
Exact SQLLiteral output is independent of extra_float_digits display precision.
Its private formatter distinguishes canonical display (`NaN` for either sign)
from exact persistence (`-NaN` for accepted negative-NaN coordinates).
Stored PATH constants reuse SQLLiteral, including domain delegation, rather
than passing through lossy display output. Both presentation deparsing and
captured catalog/view recipes retain an explicit PATH cast, including typed
NULL, so exact literal text cannot silently become a TEXT result.
Replayed catalog/view recipes elide only an identical captured literal's
duplicate PATH/domain leaf annotation; outer casts, conversions and differing
domain or typmod usages retain their original checks.
Input keeps path_in's optional single-parenthesis wrapper separate from
path_decode's open/closed wrapper and consumes their closing obligations in
order. This admits `([1,2])` without introducing recursive list parsing.

Circle owns a fixed inline center/radius datum and shared text/binary codecs.
`CirclePlan` evaluates only actual circle scalar signatures and strict radius;
it never grants `ComparisonCapabilities`, grouping or default index authority.
The shared `ComparisonPredicate` retains a dedicated `CirclePlan` for the six
area operators, including non-negatable NaN inequality. Predicate identity
includes that payload; hash-equality and grouping demands explicitly reject it.
Valid concrete types without a default ordering provider report PostgreSQL
42883 with its ordering hint; scalar operators alone do not establish one.
Representation identity compares all three IEEE bit patterns independently of
fuzzy SQL geometry. Area and edge arithmetic reuse checked float operations;
center distance follows the released scaled hypot order.

`GeometryConversionPlan` owns exactly twenty-one BOX/CIRCLE/LINE/LSEG/PATH/POINT/POLYGON
constructors, with fixed typed operands and the same catalog's float8 authority.
The explicit polygon count additionally requires the result POLYGON's canonical
INT4 association; foreign same-OID operands fail before strict NULL handling.
It reads immutable geometry directly, preserves raw inverted BOX corners and checks cancellation
in polygon loops. `circle(point,float8)` creates legitimate negative-radius
datums without relaxing text22P02 or binary22P03 input checks. Existing circle
consumers accept those datums. Negative CIRCLE `SQLLiteral` uses a closed,
qualified constructor with exact coordinate/radius spelling. Composite replay
uses typed members; negative-containing existing array carriers use recursive
typed SQL constructors and bounded INSERT targets to preserve rank and bounds,
not invalid circle text or a native-only NULL placeholder.
Known TEXT-to-CIRCLE/POLYGON typename input uses the existing viaIO representation
only in Explicit context, including normal domain recursion and generation Rebind.
It calls the ordinary input codec, retaining negative circle text rejection;
no implicit, assignment, reverse or cross-geometry route is admitted.
LSEG retains ordered/coincident endpoints and shares immutable raw BOX diagonals.
PATH owns one closed vertex slice copied from POLYGON, preserving all vertices.
POINT copies coordinates/circle centers inline; BOX/LSEG means use checked sums
then division. Both polygon center and circle use the full shared circle kernel,
including checked radius arithmetic and cancellation, not a mean-only shortcut.
POLYGON copies BOX corners low-low/low-high/high-high/high-low or every closed
PATH vertex without normalization/deduplication/closing append. Open PATH fails22023.
Circle sampling defaults12 or accepts explicit INT4 n>=2, starting leftmost for
positive radii and retaining checked unsnapped trig arithmetic. Validation order:
inclusive abs(radius)<=1e-6 (0A000), n<2 (22023), released signed-int storage
guard16*n+40<=INT_MAX (54000), then coordinate arithmetic (22003 on overflow).
NaN/Infinity and negative constructor radii outside fuzzy zero are not blanket
rejected. Unary SQL-wrapper kernel diagnostics retain their function Context.

`GeometryMeasurePlan` owns six unary area/center/diameter operations, leaving
radius in `CirclePlan`. It validates the same catalog's input/result authority
before strict NULL, reuses BOX/CIRCLE area kernels and the BOX/LSEG midpoint,
and never reparses text or copies path vertices. Open-path area bypasses
coordinate arithmetic; closed paths use sequential checked products/add/subtract
then absolute half, checking cancellation per vertex. Inline circle-center
copies retain IEEE bits. Checked operation order is a contract, not an
optimization opportunity.


`Citext` is a catalog-generation-owned scalar using Value's immutable string,
with shared encoding validation and collatable Usage. It does not fold case.
Its preferred BPCHAR/BOOL/INET casts run before string-category scalar I/O;
implicit outgoing TEXT/VARCHAR does not grant implicit incoming conversion.
Retag rejects foreign family/generation coincidences, including NULL; only
source-pointer-authorized catalog import uses RemapCitext. Metadata is category
S, nonpreferred, collatable and extended-storage, without binary routines or
advanced comparison/array authority.

A catalog OID alone does not grant an executable provider, cast, table/storage
admission or persistence capability. Lookup-only companion arrays remain
unavailable. Raw internal CHAR value codecs do not admit CHAR columns. Native
TID has text but no binary provider; its binary boundary is type-specific
`0A000`. ACLITEM and installed query_int/cube/citext have no binary functions and report
`42883` rather than fabricating bytes. Nonnull pg_node_tree input and anonymous
RECORD text input retain their existing explicit restrictions. See
[`docs/TYPES.md`](../../docs/TYPES.md#binary-stream-providers) for the full type
and storage distinctions.

Pgrowlocks uses the genuine concrete XID companion1011 with the shared array
codec and owned-value constructors; it is not an OID-labeled TEXT array.
CID/XID8 companions remain lookup-only. Only plain XID is admitted by this
required output seam: arrays over domains whose base is XID remain excluded.
It does not change the engine's transaction-array table-declaration boundary
or add scalar operators/identity allocation. `TestPgrowlocksXIDArray` checks
reciprocal descriptor ownership, real unsigned elements/NULL, text round-trip,
owned empty/nonempty output and rejection of foreign generations.

`Descriptor.CheckBinaryInput` and `CheckBinaryOutput` share the actual
missing-provider boundary with binary receive/send dispatch, without invoking
conversion on a fabricated value. Domains delegate to the base owner; generic
array and composite send providers do not eagerly resolve their members. Existing
canonical nullable catalog carriers retain only their metadata/NULL capability,
not new nonnull value admission. Protocol binary COPY TO initializes these declared
providers after CopyOutResponse and before its signature or row demand, so a
missing send function still fails for an empty or all-NULL result.

`CheckBinaryInputDeclaration` resolves a declared receiver without preparing its
invocation. A domain always has the generic domain receiver; only actual input
invocation (`CheckBinaryInput` for NULL, the real converter otherwise) resolves
the base receiver before domain constraints. Binary COPY FROM uses declaration
setup for all live physical columns before CopyInResponse, even omitted columns.
Its domain NULL invocation remains row-owned after that response. These seams
share one absence authority; no fake datum, second codec inventory or eager
container-member initialization is involved.

Binary NULL arguments initialize receive availability before domain constraints;
nonnull arguments retain their actual converter. Ordinary row output initializes
send availability only on its first delivered tuple, including all-NULL tuples,
not for an empty result. FunctionCall NULL results remain a distinct framing
shortcut rather than ordinary row-destination initialization.

JSONB `@?`/`@@` use the comparable `BinaryPlan` with two public operands and
hidden immutable vars/silent usages derived from the document/result owners.
Binding validates those four consumer formals once. Execution supplies one
shared immutable empty-object payload and invocation-local values to the existing
exists/match consumers, not a second evaluator or an allocated result sequence.
Descriptor validation, context-before-NULL, strict full demand and retained
Boolean-prefix behavior remain owned by those consumers. Input/ordinary errors
are never suppressed by SQLSTATE; unsupported arithmetic/scalar methods remain
`0A000`.
Raw pgcrypto AES and BF use one parser/dispatch in `pgcrypto_raw.go`, not separate
type grammars. AES owns its stdlib block loop; BF CBC/ECB/CFB64 uses the same
fixed Blowfish P/S state, constants, Feistel function and expansion as bcrypt.
Raw key packing is separate (unsigned bytes including NUL, first56, empty-zero);
bcrypt's terminating-NUL and2x sign-extension protocol is unchanged. Per-call
schedules/feedback, immutable input strings and one builder-owned output avoid
payload-sized scratch or frame caches. Complete PKCS-run validation is shared;
CFB64 streams arbitrary tails and treats valid padding options as no-ops.


Standard SQL/JSON query plans share this same walker. Its private path item
borrows either an immutable JSON node or an invocation-owned temporal Value;
temporal payloads never enter persistent JSONB. Ordered PASSING keeps first-name
lookup while evaluating every input. Query/value selection fully demands strict
tails and discards structural-error prefixes. Result conversion uses destination
input/domain/array/composite providers; SQL VALUE temporal text and UTC JSON
temporal encoding are separate. Selected behavior conversion is terminal.

These semantic policies inspect underlying diagnostics through input annotations:
`IS JSON` still maps only `22P02`/`22P05` to false, while query conversion's
existing ON ERROR policy still admits only classes `22`/`23`. Behavior conversion
retains its `42804` diagnostic and original message detail. Neither policy uses
or broadens COPY's source-soft classifier; ordinary errors and cancellation keep
their identity. Real target-input proof:
`make check RUN='^(TestSQLJSONScannerKindsAndUniqueness|TestSQLJSONMarkedInputConversion)$'`.

`JSONTableRows` reuses those typed bindings and the same evaluator for complete
row-path selection. Selected rows retain both the borrowed immutable JSON tree
and canonical JSONB text required by the ordinary descriptor output codec.
Suppressible failures discard the entire private prefix; missing variables,
unsupported operations, ordinary errors and cancellation stay hard errors.
`SQLJSONQueryPlan` carries optional table-column context for exact
cardinality diagnostics and typed EXISTS conversion (INT4 0/1, other types JSON
boolean spelling). It does not alter standalone query-function behavior.
Private boundary proof: `make check RUN='^TestJSONTablePlan$'`.
`CoercionPlan.FunctionExpression` exposes selected cast/typmod functions for
binding-time SQL/JSON DEFAULT validation. Relabel/IO wrappers inspect their
input; array wrappers also inspect the element expression, so a real element
cast function can establish a valid DEFAULT shape. Domain wrappers retain the
underlying selected operation. This query never evaluates a value or changes
conversion policy.

Constant-path properties walk the parsed tree with separate incoming-item and
filter-current temporal categories. Filters preserve their candidate category;
lax array access preserves scalar wrapping, strict array access clears it.
Recursive access preserves it only when the minimum depth is zero. Nested
filters and subscript bounds cannot overwrite an enclosing current category.
Mixed zoned/nonzoned comparisons are mutable; bare bindings and same-zone
comparisons are not blanket mutable.

### Catalog-aware operator viability

`ResolveOperatorWithCoercion(candidates, left, right, unary, can)` shares the
existing ranking body with `ResolveOperator`. The optional pure
`can(source *Descriptor, target ID) bool` predicate supplies concrete implicit
feasibility from the caller's captured catalog; it is neither stored nor invoked
for value evaluation. Exact/domain matches, polymorphic shape and identity,
categories, preferred types and unknown-position ordering remain unchanged.
Compatible-polymorphic common selection uses the same predicate in both
directions, as do final leaf checks and unknown-assume-known viability.
`ResolveOperator` keeps the original fixed-provider behavior with a nil predicate.

### Binary datum conversions

`PlanBinaryCoercion(source, target)` captures exact descriptor pointers and one
immutable native representation mapping. `Apply(ctx, value)` checks cancellation
and source ownership, including typed NULL, and returns the captured target.
It does not consult a catalog, dispatch a routine, use text input/output, use
wire send/receive as memory layout, or weaken `Retag`'s logical-identity checks.
Core owns CREATE CAST permissions, PostgreSQL physical/category validation,
context selection, dependencies and atomic publication.

`PlanDeclaredBinaryCoercion` carries that same physical operation in an ordinary
`CoercionPlan` after catalog authorization, including source-domain projection.
Its `Rebind` preserves the selected representation family under descriptor-owner
remapping; it does not fall back to ordinary IO or select a current cast.
`TestAggregateBinaryConversionRebind` checks real bytes, typed NULL, cancellation
and rejection of foreign source owners and changed physical operations.

```sh
make check RUN='^TestAggregateBinaryConversionRebind$'
```

Supported families are:

- One inline 32-bit word: INT4, FLOAT4, DATE, OID, XID, CID and executable REG*
  aliases. DATE translates native int64 infinity sentinels to PostgreSQL's
  int32 sentinels; finite days retain the 2000-01-01 epoch. Alias words retain
  numeric identity without name lookup. Binary-created REGCLASS renders its
  canonical decimal spelling lazily, avoiding a string allocation per cast.
- One inline 64-bit word: INT8, FLOAT8, MONEY, TIME, TIMESTAMP, TIMESTAMPTZ and
  XID8. MONEY retains minor units and temporal datums retain microseconds and
  sentinels without timezone or epoch conversion.
- TEXT/VARCHAR/BPCHAR/JSON/CITEXT/BYTEA share immutable octets. Binary JSON is
  raw text, not JSON parsing; BPCHAR is neither trimmed nor padded, CITEXT is
  not folded, and BYTEA escape/hex syntax is not decoded.
- BIT/VARBIT share canonical bits; INET/CIDR retain family, prefix and address
  without masking; BOX/LSEG share ordered immutable point pairs without BOX
  input normalization; LINE/CIRCLE map their three binary64 fields directly,
  including signed negative radii. This is not circle input/receive: ordinary
  negative-radius input remains invalid, and stored negative datums use the
  existing constructor replay.

Scalar words and byte-family mappings allocate no per-value byte buffer or
payload copy. CIRCLE to LINE creates the ordinary immutable geometry payload;
other point-pair/LINE mappings reuse existing immutable payloads where possible.
Unsupported structured ABI cross-families (including JSONB/BYTEA, NUMERIC/text,
POINT/INTERVAL/ACLITEM, PATH/POLYGON/CUBE and structured text-search/node values)
return native `0A000: binary cast representation is not supported` at planning.
Admitted mappings whose target cannot represent the payload (invalid temporal
range, BYTEA to invalid UTF8/NUL text, CIDR host bits or geometric invariants)
return native `0A000: binary cast value is not representable in embedded mode`.
These are explicit native boundaries, not PostgreSQL receiver/input errors.
BOOL/internal CHAR general SQL capability remains unchanged.

`Float4FromBits` and `Value.Float4Bits` expose exact IEEE binary32 authority,
including signaling NaNs, NaN payloads, subnormals and signed zero. Widened
`floating` remains presentation/arithmetic state; an inline raw32 marker owns
binary-created words. Copies, Retag/import, array/composite leaves, binary
transport, SameDatum and SamePayload retain those words. Ordinary constructors
and genuine arithmetic/conversion results derive their own rounded words and
never inherit stale raw authority. SQL equality/grouping keep their existing
NaN and zero semantics. Ordinary SQLLiteral output remains unchanged; core's
recursive stored-literal/Dump renderer uses Float4Bits and real staged SQL
binary-cast bootstrap declarations rather than lossy NaN text.

`InputSessionDependent` and `OutputSessionDependent` classify the actual codec
procedures for both fixed coercions and catalog-declared INOUT casts.
PostgreSQL REL_18_6 declares enum, record, range, multirange and array input/output
STABLE even with immutable member codecs. Domain input/output delegate to the
invoked base codec here, not a synthetic call to `domain_in`; existing domain
checks remain independently enforced. Pure XID output plus REAL input does not
acquire STABLE volatility merely because the conversion uses text; scalar
temporal, floating output and other existing classifications remain unchanged.
Release authority:
[`pg_proc.dat` IO procedure declarations](https://github.com/postgres/postgres/blob/REL_18_6/src/include/catalog/pg_proc.dat).


## Testing

Selected conversion arithmetic, ownership, NULL, LINE error-policy and
constructor literal units:

```sh
make check RUN='^(TestGeometryConversion(Kernel|LineErrors)|TestCircleConstructorReplay)$'
```

Focused measurement invariants guard unsafe midpoint/cross-product rewrites,
foreign catalog NULL retagging, traversal-only cancellation and center bit loss:

```sh
make check RUN='^TestGeometryMeasure(ArithmeticOrder|Authority|Cancellation|CenterBits)$'
```

Public values/diagnostics/metadata and logical replay use the six shared
definitions in [`reference/types`](../../reference/types/README.md#geometric-measurement-oracle-contract).

Binary datum and raw32 supplemental units exercise actual word/byte/coordinate
results, all scalar family pairs, temporal epoch/infinity translation, descriptor
provenance, NULL, precise capability/payload boundaries, cancellation, raw32
transport/reownership/nested leaves, fresh arithmetic and allocation budgets:

```sh
make check RUN='^TestCastDefinition(BinaryDatum|BinaryPayload|BinaryAllocations|Float4RawBits)$'
make check RUN='^TestCastDefinition(BinaryDatum|BinaryPayload|Float4RawBits)$' RACE=1
make check RUN='^TestCastDefinition(NegativeCircleBinary|IODescriptorVolatility)$'
make check RUN='^TestCastDefinition(NegativeCircleBinary|IODescriptorVolatility)$' RACE=1
make check RUN='^TestReferenceCastDefinition(IOTypmod|IOVolatility|NegativeCircleBinary|CapabilityBoundary)$'
make check RUN='^TestReferenceCastDefinition(IOTypmod|IOVolatility|NegativeCircleBinary|CapabilityBoundary)$' REFERENCE=1
make check RUN='^TestReferenceCastDefinition(BinarySafety|CapabilityBoundary|RawFloat4Persistence|FeatureSmoke)$'
make check RUN='^TestReferenceCastDefinition(BinarySafety|CapabilityBoundary|RawFloat4Persistence|FeatureSmoke)$' REFERENCE=1
```

Public cast SQL and native Dump/Restore witnesses live in `reference/operators`
and must pass both native and pinned PostgreSQL modes before integration. These
units do not replace those selected proofs or root's whole-project quickcheck.

`make check RUN='^TestCastDefinitionOperatorCoercion$'` supplements public
operator/implicit-cast coverage with direct-edge selection and ambiguity,
exact/domain/unary precedence, one/both unknowns, polymorphic identity and
category rejection, compatible common direction and unchanged fixed admission.

Shared operator preferred-type and conditional/comparison regression proofs:

```sh
make check RUN='^TestOperatorBitUnknownPreference$'
make check RUN='^Test(ComparisonResolutionSignatures|ComparisonImplicitEligibility|ConditionalEqualitySignatures|ConditionalTemporalAndNameCoercion)$'
make check RUN='^TestReferenceObjectGrantBitConcatAuthority$'
make check RUN='^TestReferenceObjectGrantBitConcatAuthority$' REFERENCE=1
```

POINT scanner, bit-preserving immutable components, predicate kernels,
descriptor ownership, cancellation and absent default capabilities:

```sh
make check RUN='^TestPoint(InputOutput|CoordinateBits|Predicates)$'
```

Public SQL expectations and both-mode proof belong to
[`reference/types`](../../reference/types/README.md#point-oracle-first-contract).

Earth's three bounded payload checks inspect immutable cube coordinates without
allocating or reparsing text. Released ordered-comparison NaN behavior and
squared accumulation are intentional; do not substitute Hypot or finite checks.
`make check RUN='^TestEarthCubeChecks$'` covers shapes, specials and adjacent
strict surface boundaries. Public domain/NULL/transport proof belongs in
[`reference/types`](../../reference/types/README.md#earth-domain-contract).

LSEG codec, ordered bits/replay, scalar predicates, strict NULLs, endpoint
immutability/inline fetch and default-capability/binary-boundary units:

```sh
make check RUN='^TestLseg(InputOutput|Comparison|Components|ExactReplay|Capabilities)$'
```

Public SQL and both-mode smoke are in
[`reference/types`](../../reference/types/README.md#lseg-oracle-first-contract).


`TestSimilarActualMatcherCancellation` translates `(a|aa)*`, compiles outside the
cancellation phase, and uses the real matcher on finite20,000-character input.
Its delegated context calls an actual CancelFunc only when the current stack
contains BOTH matchPGRegexStates and pgRegexWork.step. It requires closed embedded
Done, ordinary context.Canceled, no successful match publication, cleared work
contexts and same-program true/false recovery. This is genuine primitive evidence,
distinct from [public relation-admitted SQL cancellation](../../reference/patterns_encoding/README.md#sql-similar-oracle-and-feature-checks),
which does not claim provider entry.

```sh
make check RUN='^TestSimilarActualMatcherCancellation$'
make check RUN='^TestSimilarActualMatcherCancellation$' RACE=1
```

`TestRegexpSubstrCancellation` exercises actual `RegexpPlan.Evaluate` with four
finite named cases: flags, UTF8 start traversal, high-N occurrence traversal and
the tagged matcher selected by whole-match demand. Its delegated real context
calls CancelFunc only at the exact synchronous checkpoint: regexpFlags; own
evaluate+pgRegexWork.step excluding compiler/provider frames; or
evaluate+matchPGRegexTagged+pgRegexWork.step, respectively. Done starts open and
must close; Err returns the embedded context's actual error, not synthetic
cancellation. Each case requires ordinary context.Canceled, no scalar publication
and cleared execution program, then reuses the SAME plan/state for exact TEXT
recovery: flags `'a'`, start/occurrence NULL, tagged source `'a'` -> `'a'` and
source `'b'` -> nonnull empty TEXT. Recovered programs retain no work context.
These are native consumer/provider units, not PostgreSQL interrupted-frame
claims or a substitute for the
[dual-mode public cancellation lifecycle](../../reference/patterns_encoding/README.md#regexp_substr-selection-and-lifecycle).

```sh
make check RUN='^TestRegexpSubstrCancellation$'
make check RACE=1 RUN='^TestRegexpSubstrCancellation$'
```

Circle codec, numeric/diagnostic precedence, area/sameness/NULL distinction,
bit identity, binary framing and selected-plan ownership units:

```sh
make check RUN='^TestCircle(InputOutput|NumericEdges|Operators|DefaultCapabilities|DatumIdentity|Binary)$'
```

Public bindings, metadata, capabilities and native replay proof live in
[`reference/types`](../../reference/types/README.md#circle-scalar-contract).

Citext function-level identity, codecs, supported registered collations, NULL,
cast-context priority and remap boundaries:

```sh
make check RUN='^TestCitext(InputOutput|Identity|Coercion|Metadata|Remap)$'
```

Public SQL/provider/control proof is in
[`reference/types`](../../reference/types/README.md#citext-storage-and-value-round-trips).


Follow the [shared testing guide](../../docs/TESTING.md). These tests call codec
and value functions directly; public SQL and socket answers belong in
`reference/`.

Pgrowlocks' required XID array value seam:

```sh
make check RUN='^(TestPgrowlocksXIDArray|TestArrayBuiltinAuthority|TestRegtypeInventoryCapability|TestIdentifierScalarProviders|TestXIDScalarAuthorityAndGrouping|TestCompositeArrayStorageAdmission)$'
```

`TestSQLJSONQueryPlan` covers private descriptor authority, strict prefix
discard, ordinary errors, context-before-NULL, duplicate-name lookup,
path-sensitive properties and DST-sensitive civil-date time/timetz comparison.
Run `make check RUN='^TestSQLJSONQueryPlan$'`; shared public SQL proof remains in
`reference/binary_json_arrays` and requires native and real pinned modes.
`TestSQLJSONPathMutable` supplements public admission proof with a parsed-path
matrix: nested/current isolation, filter output, lax/strict index/range/wildcard,
recursive zero/positive/LAST bounds, key/method category clearing, nested work
in subscript bounds and boolean operands, first-duplicate lookup, all five bare
temporal types and typed/constant NULL controls.
Run `make check RUN='^TestSQLJSONPathMutable$'`.



`TestJSONBPathOpsSignatures`, `Consumers` and `Resolution` exercise the typed
two-operand plan, foreign-value/context admission, concurrent invocation-local
state, and unknown-operand overload selection independently of SQL execution.
The functions/engine/parser companions cover reminting, real routine dependency
admission and maximal-munch qualified operator positions.

```sh
make check RUN='^TestJSONBPathOps(Signatures|Consumers|Resolution|Binding|Reconstruct|Parser)$'
make check RUN='^TestJSONBPathOpsConsumers$' RACE=1
```

Password package units exercise the independent salted DES/xdes and streamed
MD5-crypt kernels, execution-local generator parameters and salt encoding.
Published PostgreSQL regressions/pinned18.6 edge constants ground exact hashes;
DES primitive known-answer, parallel distinct-key schedules, bytewise folding,
24-bit counts and deterministic context polling supplement public coverage.
`TestPgcryptoMD5Salt` retains the raw byte-truncated UTF8 salt result without
widening SQL TEXT admission. Streaming callers own one reusable4096-byte scratch
buffer; MD5-crypt reuses it across all1000 rounds.

SHA256/SHA512-crypt reuse two standard-library digest states, fixed digest/salt
buffers and the same bounded streaming scratch. P is streamed from repeated DP
blocks, never materialized at password length or allocated per round. DP retains
the algorithm's quadratic input work but polls cancellation while streaming;
each round also polls. Parser-only tests select maximum999999999 without hashing.
`pgcrypto_sha_salt.go` retains raw and decoded salts and pinned64-bit signed64
saturation/int32 narrowing. `Evaluate` receives an explicit per-call notice sink;
nil is valid for standalone units, never a retained/global session policy.
The public fixtures independently assert full22003 clamp notices and body errors.

```sh
make check RUN='^TestPgcrypto(DES(Vectors|Protocol|Cancellation)|MD5(Vectors|Salt|Cancellation)|Salt(Parameters|Encoding)|PasswordDispatch)$'
make check RACE=1 RUN='^TestPgcrypto(DES(Protocol|Cancellation)|MD5Cancellation)$'
make check RUN='^TestPgcrypto(SHA(Vectors|Parsing|Cancellation|Isolation)|Salt(Parameters|Encoding)|PasswordDispatch|BcryptCancellation|MD5Salt|RawVectors|Cancellation|Password(Entropy|Notices)|NoticeCurrentHost)$'
make check RACE=1 RUN='^TestPgcrypto(SHA(Cancellation|Isolation)|PasswordNotices|NoticeCurrentHost)$'
```

The public direct/prepared/control scenarios and actual feature smoke belong to
[`reference/catalogs`](../../reference/catalogs/README.md); they require both
native and real pinned PostgreSQL modes before integration.

Raw BF units supplement the public SQL proofs with independent primitive encrypt
AND decrypt vectors, unsigned raw packing/56-byte clipping, full padding-run
validation (including preceding-data acceptance), deterministic cancellation at
entry/mid-data/final publication and concurrent distinct schedules/modes.
Allocation inputs (short/1MiB data and a1.5MiB key) are prebuilt. Calibrated
`testing.Benchmark` iterations evaluate batches of16 and byte totals normalize
to one evaluation without integer truncation; warmed `testing.AllocsPerRun(20)`
supplies exact kernel counts. The independent budgets remain output plus32KiB
fixed scratch and16 allocations/call. Data-growth residual bytes and oversized-key
extra bytes must each fit that same32KiB allowance; key allocation counts must
match exactly. This catches payload/key-sized copies without requiring unrelated
process byte noise in separate benchmark runs to be identical. These are
behavioral/resource tests, not source/wiring tests.

```sh
make check RUN='^TestPgcrypto(BF(Vectors|KeySchedule|Padding|Cancellation|IsolationAndAllocations)|AES(Vectors|KeyIV|Padding|Parser|Cancellation|Carrier)|Bcrypt(Vectors|PasswordProtocol|SaltParsing|SaltEncoding|Allocations|Cancellation))$'
make check RACE=1 RUN='^TestPgcryptoBF(Cancellation|IsolationAndAllocations)$'
```


`TestProtocolValueCodecs` exercises real unavailable receive/send diagnostics,
domain receiver declaration versus invocation/base-error precedence, the builtin
time_stamp domain's epoch representation and recursive domain-array
receive/send with independently specified network bytes and separate NULL
members. It does not substitute an OID whitelist or inventory-membership test
for consumer behavior. The engine's same-named mechanism test separately checks
borrowed output scratch, captured role formatting and retained anonymous RECORD
occurrence authority.

```sh
make check RUN='^TestProtocolValueCodecs$'
```

The public provider matrix, text/binary Bind and result metadata use the actual
server subprocess and the pinned PostgreSQL fixture:

```sh
make check RUN='^TestReferenceServerExtended(Codecs|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceServerExtended(Codecs|Metadata)$'
```

Those shared definitions assert independently expected values, bytes, type
metadata and explicit unavailable-provider boundaries. Their owning
[`reference/sessions` README](../../reference/sessions/README.md) contains the
complete protocol proof and actual pq/psql smoke instructions. Selected checks
do not replace the root's full-project verification.

`make check RUN='^TestSearchOccurrenceOrdering$'` checks occurrence RECORD and
record[] sorting through existing typed providers: numeric ordering, inner NULL
high, ancestry prefix, reached JSON demand before NULL, context cancellation and
OrderingBound versus authentic physical scalar opclass identity. General arrays,
named composites and occurrence-record physical indexes remain unadmitted.
`make check RUN='^TestOrderingBtreeIdentity$'` retains scalar identity/allocation
coverage. Public SEARCH witnesses remain in `reference/query_core`.

### Network subnet predicates

`NetworkSubnetPlan` keeps catalog-owned INET inputs separate from BOOL result
authority and compares inline prefix bits without parsing, formatting or heap
argument slices. CIDR/domain coercion belongs to analysis. Cross-family values
return false; host bits are never changed.

```sh
make check RUN='^TestNetworkSubnet(Values|Boundaries|PlanValidation)$'
```

These units include every IPv4/IPv6 prefix width, host-bit changes, significant-bit
neighbors, descriptor rejection, typed NULL and cancellation. Public contracts and
both-mode selectors belong to [`reference/types`](../../reference/types/README.md).
