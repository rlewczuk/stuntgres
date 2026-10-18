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

Added for the math family (F4/F5); each is reused by glue still in the engine:

- `DefaultConsumer`: whether a column DEFAULT is being bound. It is analyzer
  context set by the engine's DEFAULT binder and carried through recursive
  binding; most families reject themselves there with 0A000 (random, binary,
  string, SIMILAR, regexp and JSON aggregates read the same flag). A later
  DEFAULT-admission hook (F2) may replace it.
- `ArrayType`: the array type of an element type, read from the binding
  catalog or the statement's definition stage (catalog state; ~8 glue files).
- `CommonUsage`: PostgreSQL's nonthrowing common-type probe
  (`select_common_type` with no error label) over typed operand usages. The
  engine owns the algorithm shared with CASE, VALUES and IN; if its usage-only
  core moves to `sqltype`, this becomes a plain function.
- `ComparisonInfo`: comparison input collation derivation, recording the
  operands' diagnostic type inputs as dependencies (array search/comparison
  and LIKE use the same service).

Added for UUID generation, `format` and uuid-ossp (F4/F5):

- `CollatedInfo`: result collation derived from the bound inputs, with the
  engine's conflict diagnostics (17 glue files use the same service).
- `RetainUntypedParameter`: an argument bound to an ANY formal that is a
  still-untyped `$n` stays pending, so statement completion reports 42P08
  unless another occurrence resolves it. Count, NULL-count, JSON creation,
  string variadics and SQL/JSON do the same.
- `NoteCatalogText`: the statement renders a catalog-identifier value
  (`regclass` and friends) as text, so execution captures the catalog
  snapshot that output reads. It is statement-level analyzer state.
- `InstalledRoutine`: resolves a name on the function search path (lookup
  errors propagate) and returns the routine that an `Extension` installed
  there with a signature (comma-separated input type OIDs), or nil. The
  result is an opaque handle (`installed_routine.go`): `Validate` checks the
  installation manifest, and `Attach` completes the call's routine identity,
  records the dependency and returns the engine's identity-carrying wrapper.
  Extension installation and procedure identity stay in the engine. The
  pgcrypto, fuzzystrmatch and unaccent ports added their `Extension` values
  and share one engine handle with uuid-ossp; a procedure installed by another
  provider is not the extension's routine. pgcrypto's lookup walks the path
  namespace by namespace and returns `CatalogBuiltin` when `pg_catalog` comes
  before the installed `gen_random_uuid`, so the call binds as the core
  builtin. The intagg port added `IntaggExtension`: `Attach` takes an
  `IntaggEnumeration` (the bound `int_array_enum` call), applies the
  engine's set-returning admission (EXECUTE parameters, policy frames,
  `AdmitSetReturning`) and returns the engine's set-returning unnest node.
  The engine's `int_array_aggregate` aggregate and window paths read the
  identity from the same handle. intarray needed nothing: it installs only
  the `query_int` type and its I/O procedures, which bind no calls.

Added for the array and range families (F4/F5), which also use
`CollatedInfo` and `NoteCatalogText` (array_to_string over catalog-identifier
elements):

- `BuiltinOperatorRoutineInfo`: the selected builtin operator's routine
  identity, as `BuiltinRoutineInfo` is for functions. `FinishArrayTransform`
  needs it when the engine's `||` binder reaches it in operator mode; operator
  ports will reuse it.
- `CaptureComparisonInput`: records the statement's diagnostic type-name input
  when comparing the type needs it (array_sort's element comparison; the
  engine's comparison binders do the same).

Added for the JSON/JSONB function calls (F4/F5); JSON creation also uses
`RetainUntypedParameter` and `NoteCatalogText`:

- `Literal`: the engine's literal node for a constant. The engine owns the
  node, because literal identity drives `FunctionLiteralNull`, folding and
  stored SQL; omitted default arguments use it (63 engine sites build
  literals).
- `Context`: the binding statement's cancellation context, for input
  conversions done while binding (the `'{}'::jsonb` default of JSONPath
  calls).
- `AdmitSetReturning`: set-returning call placement (clause, DEFAULT, CHECK,
  generated, index and aggregate-argument rules) plus target-list set
  bookkeeping; string, regexp, series and JSON SRFs share it.
- `CaptureTypeInput`: the diagnostic-name, ACL and catalog-identifier input
  that a record result type needs (also used by function column definitions).

Added for the datetime, LIKE/SIMILAR/regex and string families (F4/F5),
which also use `CollatedInfo`, `Literal` (`make_*` zero defaults, the default
SIMILAR escape), `Context` (bind-time template compilation for `to_char`,
`to_date`, `to_timestamp` and `to_number`), `BuiltinOperatorRoutineInfo`
(POSIX, SIMILAR TO, LIKE), `AdmitSetReturning` (`string_to_table`),
`RetainUntypedParameter` and `NoteCatalogText` (the variadic string family):

- `ConstantValue`: reads the value of the analyzer's literal node, the
  counterpart of `Literal`; constant formatting templates compile once at
  bind time.
- `BuiltinOperatorVisible`: whether operator lookup reaches `pg_catalog`
  under the current `search_path` (POSIX and LIKE operators).
- `CallSignatureError`: the analyzer's whole-call 42883 (argument
  re-analysis for diagnostic names, composite-candidate 0A000, the note),
  used by `like_escape` and `similar_to_escape` for malformed calls.
  Reimplementing it here would duplicate the engine's diagnostic routing.

Added for binary data functions (F4/F5), which also use
`BuiltinOperatorRoutineInfo` (the `||` identity for bytea and bit-string
concatenation), `CollatedInfo` and `DefaultConsumer`; the binary port also
removed the string family's `FinishBinary` leak:

- `LiteralNull`: whether a bound argument is the engine's NULL literal,
  looking through collation and coercion wrappers. A strict `convert*` call
  with a literal NULL never runs, so it demands no visibility snapshot; the
  engine's random, advisory and conditional-planning code reads the same
  predicate.
- `NoteVisibilitySnapshot`: the statement executes an encoding conversion
  lookup, so it captures the catalog visibility snapshot that lookup reads.
  It is statement-level analyzer state, like `NoteCatalogText`.

Added to register `regexp_*` and the variadic string family, replacing the
values their engine branches passed in:

- `Frame`: the query frame identity that the variadic family's quote-capture
  folding keys on.
- `CapturePolymorphicBody`: captures the builtin routines a SQL-bodied
  builtin (`quote_literal`/`quote_nullable` on anyelement) selects from its
  actual argument.
- `TargetSet`: target-list set bookkeeping alone, for regexp sets.
- `RegexpSetPlacement` (**leak**): the regexp family's own set-returning
  placement policy (CASE/COALESCE hint, clause tracking), which
  `AdmitSetReturning` does not cover; unify the two when query analysis gets
  an owner.

Argument analysis intents are values, not methods: `ThresholdArrayArgument`
is the comparison operand whose unknown-NULL annotation may name an enum
array, as `width_bucket`'s threshold array may. `ZoneArgument` is a
`timezone`/`date_add`/`date_subtract` operand, whose explicit casts admit the
zone family's temporal targets. `RecordBaseArgument` is a
populate-record base: named casts to user-defined composites keep their
occurrence identity. The coercion intent `RecordFormalArgument` finalizes a
fixed RECORD formal: a parameter keeps its inferred record type, other
operands get an anonymous record.

Engine state that bind code needs at a single point is passed as a value
instead of a method: `age(xid)` receives the engine's transaction-identity
node builder.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Registry

`registry.go` holds a static `definitions` table; there is no `init()`
registration. `Lookup(name)` claims a call by its unqualified name, compared
case-insensitively; the definition's `Bind` owns exact-spelling resolution,
visibility, argument analysis and every diagnostic. The engine's
`bindFunctionProvider` consults `Lookup` before its remaining name chain, so a
registered name must not overlap any family the chain still dispatches.

Two fields reproduce where a family sat in that chain:

- `ExactSpelling`: claim only the exact, parser-folded names (lowercase), as
  the chain's final `switch name` did. Other spellings, such as a quoted
  `"ABS"`, keep the chain's missing-function path.
- `SharedGates`: the engine applies the chain's shared gates before `Bind`:
  argument labels get the missing-signature error, then `ResolutionOnly`
  calls get the format diagnostics. On a case-insensitive definition, other
  spellings of a claimed name (`"FORMAT"`) also get the format diagnostics.
  `Bind` then checks builtin visibility itself, preserving the old gate,
  visibility, bind order.
- `OwnsLabels` (requires `SharedGates`): skip the label gate; `Bind` owns
  every label diagnostic, as `uuidv7` does for its `shift` label.

One more field covers families whose chain branch used a predicate rather than
a name list:

- `Claims`: the chain's admission predicate, replacing `Names` matching. It
  keeps each predicate's own comparison (exact, case-folded or lowercased,
  which differ for spellings such as `ſ`). `Names` still lists every claimed
  name; the registry tests check that each satisfies `Claims`.

Registered families:

| Family | File | Names | Fields |
|---|---|---|---|
| UUID extraction | `uuid_extract_bind.go` | `uuid_extract_*` | none |
| Math | `math_bind.go` | `mathNames` | `ExactSpelling`, `SharedGates` |
| UUID generation | `uuid_generation_bind.go` | `uuidv4`, `uuidv7` | `ExactSpelling`, `SharedGates`; `uuidv7` also `OwnsLabels` |
| `format` | `format_bind.go` | `format` | `SharedGates` |
| uuid-ossp | `uuid_ossp_bind.go` | `uuid_generate_v4` | none |
| Arrays | `array_{inspect,random,transform,search}_bind.go` | `array*Names` | none |
| Range inspection | `range_inspect_bind.go` | `rangeInspectNames` | none |
| JSON/JSONB calls | `json_call_bind.go` | `jsonCallNames` | `Claims` |
| `json_object`, `jsonb_object` | `json_call_bind.go` | `textObjectNames` | `ExactSpelling`, `SharedGates` |
| Datetime | `datetime_bind.go`, `datetime_*_bind.go` | `datetimeNames` | `ExactSpelling`, `SharedGates` |
| `make_*` | `datetime_make_bind.go` | `datetimeMakeNames` | `ExactSpelling`, `SharedGates`, `OwnsLabels` |
| `like_escape` | `like_escape_bind.go` | `like_escape` | `ExactSpelling`, `SharedGates` |
| Scalar strings | `string_bind.go` | `stringNames`, `string_to_table` | `ExactSpelling`, `SharedGates` |
| `parse_ident`, `lower`/`upper` | `string_bind.go` | `parse_ident`, `lower`, `upper` | none |
| Variadic strings | `string_variadic_bind.go` | `stringVariadicNames` | `Claims` (`StringVariadicFunctionName`) |
| `regexp_*` | `regexp_bind.go` | `regexpNames` | `ExactSpelling` (checks its own labels) |
| `to_number` | `numeric_formatting_bind.go` | `to_number` | `ExactSpelling`, `SharedGates` |
| Binary data | `binary_bind.go` | `binaryNames` | `ExactSpelling`, `SharedGates` |
| pgcrypto | `pgcrypto_bind.go` | `pgcryptoNames` | none |
| fuzzystrmatch | `fuzzystrmatch_bind.go` | `fuzzystrmatchNames` | none |
| unaccent, `daitch_mokotoff` | `unaccent_bind.go` | `unaccent`, `daitch_mokotoff` | none |
| Random | `random_bind.go` | `random`, `random_normal`, `setseed` | `ExactSpelling`, `SharedGates` |
| Sleep | `datetime_sleep_bind.go` | `pg_sleep`, `pg_sleep_for`, `pg_sleep_until` | `ExactSpelling`, `SharedGates` |
| Clocks | `transaction_time_bind.go` | `now`, `transaction_timestamp`, `statement_timestamp`, `clock_timestamp`, `timeofday` | none |
| intagg | `intagg_bind.go` | `int_array_aggregate`, `int_array_enum` | none |

`datetimeNames` covers extract/date_part, date_trunc, date_bin,
timezone/date_add/date_subtract, justify_* and to_char/to_date/to_timestamp.
`stringNames` is the final switch's string list, including the
argument-analyzing `trim`, which PostgreSQL does not have as a function.

Calls of these families that keep an engine dispatch branch over an exported
entry point, because they need engine state the registry cannot express:

- `age`/`isfinite` (`BindDatetimeAgeFinite`): `age(xid)` is a Tier C
  transaction-identity node.
- `overlaps` (`BindDatetimeOverlaps`): the engine sets `WrapperContext` from
  inline safety, an engine node walk that F2 replaces.
- `similar_to_escape` (`BindSimilarEscape`): claimed by call syntax, not name.
- `current_date`, `current_time[stamp]`, `localtime[stamp]`
  (`BindCurrentDateTime`): SQL value functions, dispatched by syntax. The
  precision warning is returned as a value; the engine delivers it unless a
  held description is being re-analyzed.
- `int_array_aggregate` as an aggregate: the engine's aggregate and window
  binders call `AnalyzeIntaggArguments` and `ResolveIntagg`, then keep FILTER,
  grouping and the aggregate call.
- Not name dispatch: AT TIME ZONE (`BindTimeZone`), the POSIX and SIMILAR TO
  operators (`BindPosix`, `BindSimilar`) and `||`/`^@` (`StringNode`).

`BindBinary` takes a precomputed visibility for the engine's analyzer tests;
it reaches the text routes `BindPosixSubstring`, `BindSimilarSubstring` and
`FinishString`. `FinishBinary` is shared with the string family's binary
candidates and the engine's `||` binder.

The LIKE operator binder is ported onto `Binder` but stays in the engine,
because its node (`likeExpression`) is engine-private.

`BindUUIDGeneration` is exported because pgcrypto's `gen_random_uuid`, which
still binds in the engine, uses it when `pg_catalog` wins the path.

For the JSON/JSONB processing calls, `bindJSONCall` replays the chain's
branch order: JSON creation (visibility after argument analysis), then
visibility, JSONPath array and query calls, `jsonb_set*`/`jsonb_insert`,
`json_strip_nulls`, the exact `*_extract_path*` names, JSONB records, then
the remaining processing calls (typeof, length, keys, each, elements, JSON
records). `json_object`/`jsonb_object` sat in the final switch, so they use
`ExactSpelling` and `SharedGates`. The slot calls (`jsonb_set*`, JSONPath
calls, `json_strip_nulls`) share `bindSlots` in `json_slot_bind.go`.
`json_sql.go` exports their stored-SQL renderers, `MapJSONExtractChildren`
and `JSONBPathQueryName` for the engine's deparse, preflight and
aggregate-binding switches until they adopt the protocol.

Shared scalar call helpers live in `call_syntax.go`: `ValidateCallSyntax`
(FILTER analysis, the 100-argument limit, labels), `CallLabelError` (the
label part alone, for families that analyze no FILTER), `CallDecorationError`
(DISTINCT/ORDER BY/FILTER/OVER on a scalar), `NamedArgumentType` and
`ArgumentTypeNames` (projection analysis of every argument spelled for a
42883, as the engine's `functionArgumentTypeNames`; the clock functions use
it).
`resolution_error.go` holds `ResolutionError` (the pure 42883/42725 builder;
it notes nothing, so missing signatures still go through `SignatureError`),
`DirectResolutionError` (overload failure over `AnalyzeDirectArguments`
results; random and deparse inquiry use it too), `ArrayTypeName` (array
signature spelling by leaf name), `NotedResolutionError` (42883 through
`SignatureError`, 42725 unnoted) and `DiagnosticNameOrUnknown` (the
unqualified argument spelling, `unknown` for deferred literals).
`operator_error.go`'s `OperatorError` is the operator form of both
diagnostics. Pure supply helpers serve bind code in and out of the engine:
`TemporalTypesFor` and `SupplyBinaryTypes`.

To add or port a builtin family:

1. In the engine, turn `func (b *expressionBinder) bindX(...)` into
   `func bindX(b functions.Binder, ...)` (F4). The compiler then proves the
   interface is enough; record any new `Binder` method above.
2. Move it here, add its `Definition`, and delete its engine branch and glue
   file (F5). Its unit tests move with it unless they exercise engine
   switches.
3. The chain applies shared gates after some branches: argument labels are
   rejected for most names, and `ResolutionOnly` calls get format diagnostics.
   A family whose engine branch sat after those gates sets `SharedGates`
   (and `ExactSpelling` if it sat in the final switch, `OwnsLabels` if the
   label gate exempted it), because `Lookup` runs first. Check that no
   earlier chain predicate claims its names.
4. Some engine 42883 paths used the unnoted builder (regexp, string
   variadics). Keep them on `ResolutionError(..., false)`; noting them would
   change the composite fallback decision.
5. An installed extension family resolves its procedure through
   `InstalledRoutine` (add its `Extension` and the engine's handle). The
   engine's wrapper node and its validation stay in the engine; switches keep
   explicit wrapper cases ahead of their protocol branch, because the wrapper's
   catalog identity is engine state.
6. Helpers that remaining engine switches still call (preflight, aggregate
   binding, deparse) stay exported until those switches adopt the protocol.

## Node protocol

Every function node implements `expr.Composite` beside its definition (helpers
in `composite.go`). `Children` lists the lexical children in evaluation order
(`Timezone` lists the zone before the value; AT LOCAL has no zone). `Rebuild`
checks the child count and revalidates through `Reconstruct`, keeping the inputs
the node derived at binding: input collation, retained descriptors, text and
clock usages, folded OVERLAPS ends and the math wrapper context. Fixed-arity
nodes keep argument slots beyond `Children`; nodes without a provider
(`Format`, `NullCount`, `Random`, the three leaves) only take the new metadata
and children. Engine wrappers that embed a node override `Rebuild` to keep
their routine identity.

Nodes also implement the optional `expr` hooks that the engine's switches ask
(the `expr` README defines them):

- `IntrinsicVolatility` on every node: the value its planned volatility,
  generated admission and `rewrittenIntrinsicProperties` use.
- `PlanningFold`: `FoldNever` for stage-owned and never-evaluated calls (JSON
  set and record functions, `RegexpSet`, `jsonb_path_query`,
  `string_to_table`, JSON creation, `format`, random and UUID generation,
  sleep, the leaves, and STABLE `ArrayTransform`/`StringVariadic` calls);
  `FoldOverlapsRoute` and `FoldQuoteCapture` name the engine's OVERLAPS and
  quote-capture folds.
- `StrictNull` for calls that conditional planning replaces with a typed NULL
  when a child is a literal NULL.
- `ValidateStoredProvider` on the nodes a stored generation plan may contain.
- `Retag` on nodes with catalog-derived inputs, using the engine's `expr.Retag`
  environment and change bit.
- `Annotation`/`WithAnnotation` on `StringVariadic`, whose quote-capture origin
  the engine owns.

A new node implements `Children`, `Rebuild` and `IntrinsicVolatility`, and each
other hook whose default (fold as a constant, never strict, not a stored
provider, rebuild on change) does not describe it.

## What stays in the engine

The engine owns the analyzer behind `Binder`, the bind code of families not
yet registered (for JSON: the operators, SQL/JSON constructors, `IS JSON`
and JSON aggregates), function column definition lists and JSONB record star
expansion, name/catalog/ACL and dependency resolution, parameter/result contracts, descriptor generations,
retag/remap and stored SQL presentation. The capabilities supply existing
authority without copying rows or the full evaluation context. Installed
extension routines keep their engine wrapper nodes (for example
`installedUUIDOsspFunctionExpression`, `installedCryptoFunctionExpression` and
`installedFunctionExpression` for unaccent and `daitch_mokotoff`), manifest
validation and stored identity; registered bind code reaches them only through
`InstalledRoutine`. UUID and pgcrypto DEFAULT admission (`closedUUIDDefault`,
`closedPgcryptoDefault`) stay with the engine's DEFAULT binder. Binary call deparse (`renderBinaryCall` in `binary_render.go`) stays
with the engine's renderers until F2.

Physical-session RNG, UUID clock/entropy and SQL time ownership stay in the
engine: `random.go`, the `EvalContext` clock and sleep methods in
`transaction_time.go`, and the closed DEFAULT admission of clock and sleep
calls (`closedTransactionTimeDefault`). `TransactionTimeSQL` and
`CurrentDateTimeSQL` are exported for the engine's deparse and DEFAULT
switches until F2. Function evaluation borrows the same owners only when demanded. Literal
NULL folding and reached runtime argument effects remain distinct. Aggregate
providers own execution-local state; the engine owns FILTER/DISTINCT/ORDER,
grouping, windows and state lifetime. Query/set expansion and physical regex
executor ownership remain engine responsibilities. There is no alternate binder,
dynamic plugin registration, compatibility forwarding carrier or duplicated
type algorithm.

Bind code of registered families that engine glue still reaches is
exported: `FinishArrayTransform` (the `||` operator), `ArraySearchNode` (the
`@>`/`<@`/`&&` membership operators) and `FinishRangeInspect` (`lower` and
`upper` of a range, dispatched by the string glue). `lower` and `upper` are
not registered: their operands are analyzed with string intents, and they
move with the string family. `unnest` and `generate_subscripts` stay with the
engine's set-returning functions, and the array/range operators with operator
binding. `array_node_sql.go` holds the array nodes' child-mapping and deparse
helpers for the engine's preflight and deparse switches until they adopt the
protocol.

## Family notes

`DaitchMokotoff` owns the complete PostgreSQL REL_18_0 phoneme chart, narrow
UTF8/Latin1 scanner and correlated digit-prefix state machine. Codes retain
completion/padding order and all alternatives; no-encodable input returns nil
for the engine's typed SQL NULL. State and result buffers are evaluation-local,
with incremental cancellation during scans/lookahead/expansion.
`InstalledText` evaluates `daitch_mokotoff` into an owned text[] and
`unaccent` through the installed dictionary's rules. It is not a protocol
node: the engine wrapper that embeds it keeps its own switch cases. Installed
fuzzystrmatch identity, strictness, generated consumers and control remapping
remain engine responsibilities. `unaccent.rules` (PostgreSQL REL_18_0) is
embedded here and compiled once into `StockUnaccent`, which the engine's
installed unaccent provider uses.

`bindInstalledText` keeps the order of the old engine binder:
- unaccent rejects decorations as a missing signature; `daitch_mokotoff`
  reports them with 42809 after argument finalization.
- No implicit coercion to TEXT has a cast routine, so the operand's TEXT
  coercion carries no routine identity.

Installed pgcrypto calls reuse this package's SQL-usage overload selection,
formal coercion hooks and reconstructible `Pgcrypto` carrier. `PgcryptoMembers`
is the 1.4 manifest as plain data. The engine stages and validates
installations from it, and `bindPgcrypto` reads the selected member's result
and volatility from it, which installed procedures never change. The engine separately
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

`bindFuzzystrmatch` binds Levenshtein, Metaphone and Soundex calls through
one kernel table. The table is selected by the folded name, as the engine's
chain did, and each kernel sees the lowercased spelling. The call records its
dependency without deduplication, exactly as the three old engine binders did.

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

Node protocol on function nodes:

```sh
make check RUN='^Test(Composite|IntrinsicVolatilityExcludesChildren|PlanningHooks|RetagReportsDerivedInputChanges|StringVariadicAnnotationRoundTrip)'
```

JSON/JSONB function calls (registry; the engine's `TestJSON*` bind through
the analyzer and cover engine switches; reference tests in both modes):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestJSON|TestJsonb)'
make check RUN='^(TestReference(JSON|SQLJSON)|TestReferenceObjectGrant.*JSON)'
make check REFERENCE=1 RUN='^(TestReference(JSON|SQLJSON)|TestReferenceObjectGrant.*JSON)'
```

Math family (registry, kernels; the engine's `TestMath*` cover analyzer
switches; reference tests in both modes):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestMath|TestRandomBinder)'
make check RUN='^TestReferenceMath'
make check REFERENCE=1 RUN='^TestReferenceMath'
```

UUID generation, `format` and uuid-ossp (registry and label helper here; the
engine's `TestUUIDGeneration*`, `TestUUIDOssp*` and `TestFormat*` cover
analyzer switches, stored identity and grouping; reference tests in both
modes):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestCallLabelErrorOrder|TestUUIDGeneration|TestUUIDOssp|TestFormat[A-Z])'
make check RUN='^TestReference(Format|UUID|PgcryptoRandomUUID|ObjectGrantUUIDOssp|NumericFormattingUUIDDefaults|SearchPath)'
make check REFERENCE=1 RUN='^TestReference(Format|UUID|PgcryptoRandomUUID|ObjectGrantUUIDOssp|NumericFormattingUUIDDefaults|SearchPath)'
```

Array and range families (registry; the engine's `TestArray*`,
`TestRangeInspect*` and `TestRangeMore*` bind through the analyzer and cover
its switches; reference tests in both modes):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestArray|TestRangeInspect|TestRangeMore)'
make check RUN='^TestReference(Array|RangeInspect|RangeMore|Multirange)'
make check REFERENCE=1 RUN='^TestReference(Array|RangeInspect|RangeMore|Multirange)'
```

Datetime, LIKE/SIMILAR/regex and string families. Their binders are
unit-tested through the engine analyzer (`TestDatetime*`, `TestPosix*`,
`TestSimilar*`, `TestRegexp*`, `TestLike*`, `TestString*`), because those
tests exercise grouping identity, retag, remap and preflight switches.
Reference selections (add `REFERENCE=1` for the PostgreSQL run):

```sh
make check RUN='^TestReference(Datetime(AgeFinite|Bin|Extract|Justify|Make|Overlaps|Trunc|Zone|Formatting)|Timezone|NumericFormatting)'
make check RUN='^TestReference(Like|Similar|Posix|Regexp)'
make check RUN='^TestReferenceString(Core|Case|Encoding|Ident|Length|Numeric|Pad|Replace|Slice|Split|Variadic)'
```

Binary data functions (registry here; the engine's `TestBinary*`,
`TestBitString*` and `TestStringSlice*` cover analyzer switches, consumers
and grouping; add `REFERENCE=1` for the PostgreSQL run):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestBinary|TestBit|TestStringSlice)'
make check RUN='^TestReference(Binary|BitAccess|BitString|BitOperators|StringSlice|StringCore|StringPad|Similar|Posix|LikeBinary|Bytea|Pgcrypto|SearchPath|Conversion)'
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

pgcrypto, fuzzystrmatch and unaccent registration (registry here; the
engine's `TestPgcrypto*`, `TestLevenshtein*`, `TestMetaphone*`,
`TestSoundex*`, `TestDaitchMokotoff*` and `TestExtension*` bind through
`bindFunctionProvider` and cover the wrappers' switches; add `REFERENCE=1`
for the PostgreSQL run):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestExtensionRules)$'
make check RUN='^TestReference(Pgcrypto|Levenshtein|Metaphone|Soundex|DaitchMokotoff)'
make check RUN='^TestReference(Extension|AvailableExtensions|MaterializedAlterExtension|ObjectGrant(Levenshtein|Metaphone|Soundex|PGCrypto|UUIDOssp))'
```

Random, sleep, clock and intagg registration (registry here; the engine's
unit tests bind through `bindFunctionProvider` or `BindCurrentDateTime` and
cover engine switches; add `REFERENCE=1` for the PostgreSQL run):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|Test(Random|Datetime(Sleep|Current)|TransactionTime|CurrentDateTime|Intagg))'
make check RUN='^TestReference(Datetime(TxTimes|Current|Sleep)|Random|DeparseConstraintsReviewRandom|Intagg|QueryInt|ObjectGrantIntagg|PGAggregateIntagg|ComparisonInstalledQueryInt)'
```

Array inspect, random, transform and search bind over comparison-analyzed
operands. Unknown arguments never erase a domain shared by the known leaves;
every known edge is probed before any unknown literal or parameter is
imported. Each node captures its released polymorphic formal family, not the
concrete coerced types.

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
