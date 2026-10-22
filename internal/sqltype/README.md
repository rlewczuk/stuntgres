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

`Descriptor.RebindDomain(base, name, declaration)` rebuilds a same-ID immutable
domain generation with current naming and immediate-base authority. It preserves
own CHECK/NOT NULL, builtin inventory provenance and associated identities while
recomputing inherited flags. Catalog namespace, ownership, recipes and atomic
publication remain with the existing catalog/type staging owners.

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


## Testing
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
