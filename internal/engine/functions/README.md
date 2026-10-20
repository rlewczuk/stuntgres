# Builtin function implementations

This package is the home of builtin functions: signatures and argument rules,
immutable call nodes, evaluation semantics, aggregate provider/state
algorithms and, family by family, the bind code and its registry entry. It is
a leaf library: it imports [`expr`](../expr/README.md), `ids`, `sqltype`,
`parser` and `sqlstate`, never the root engine or the engine's catalog,
storage, lock or visibility state. It reaches the engine only through
`Binder` at bind time and `expr.Context` at evaluation time, including the
narrow capability interfaces recovered from it (below). `expr` owns
`RoutineReference`, `RoutineRecipeReference`, `RandomOwner` and
`IntrinsicPropertyRule`; this package uses them under their `expr` names.

Function nodes are plain types over the `expr` contract: they embed
`expr.Info`, hold children as `expr.Expr` and evaluate with `Eval(expr.Context)`,
using only the `expr.Context` capabilities they need. `StringVariadic.Origin`
is an opaque `any` that the engine stores and reads; this package never
interprets it. The only remaining type parameter is the private
`randomInteger` sampler source, a numeric kernel detail rather than part of the
expression contract.

## Callable competition with SQL overloads

The engine's shared selector keeps existing builtin and installed providers in
the same namespace/signature competition as SQL declarations. Builtin metadata
supplies omitted trailing defaults and positional variadic expansion; expanded
**declared** formals remain the exact/category/preferred ranking inputs.
Polymorphic target substitution is never an exact concrete signature match.

Scalar, aggregate and window registry entries may expose a pure `ProbeCall` through
`CallShape`. Its `CallTypeContext` exposes only existing type, array-association
and common-type authority. It receives already-analyzed arguments plus separate
declared-input and resolved-target vectors. Family applicability reuses the
provider's pure target-selection helpers: no speculative Bind, parameter
inference, value conversion, dependency capture, provider construction or Eval.
Unknown operands remain deferred until one winner reaches its original binder.
The engine-owned enum, unnest, subscript and null-count families use their same
existing pure signature helpers rather than a second provider registry.

The optional `OriginalArgument` analyzer seam recovers syntax through the same
private slot carrying an already-bound operand, never by matching source keys.
`Argument.Raw` exposes that original recipe while binding keeps the private
carrier. Syntax-sensitive null-count admission and literal/parameter diagnostics
therefore retain their original rules without rebinding or evaluating actuals.

Selected-provider argument reuse and re-entry suppression have different
lifetimes. `selectedCallArguments` retains known-node coercion reuse throughout
provider binding. `selectedCallReentry` is consumed once by the immediate
ordinary-function or aggregate/cast gateway; it does not disable selection in
the provider's previously unanalyzed FILTER or aggregate ORDER BY children.
Nested winner frames restore both states together with argument recipes and
lexical diagnostic spelling, without rebinding actuals or changing ownership.

Compatible array transforms, searches and threshold width_bucket share common
leaf selection in `compatibleArrayTargets`; simple array families share
`arrayInputTarget`. Range and aggregate helpers retain their existing supported
type boundaries. The selected provider still owns coercions, default literals,
volatility, EXECUTE authority and all call-decoration/runtime diagnostics.
Resolution failure or ambiguity never triggers an unconditional builtin fallback.

Package-function proof for this interface and atomic unknown-category filtering:
`make check RUN='^TestSQLRoutine(ProviderCallShapes|CallableShapeMasking|UnknownCategorySelection|OriginalArgumentRecipes)$'`.
The public direct/prepared definitions and both backend modes are documented in
[`reference/query_core`](../../../reference/query_core/README.md).

Late-child composition, error recovery and actuals-once proof:
`make check RUN='^TestReferenceRoutineLateChild(Selection|Unwind)$'` and the same
selector with `REFERENCE=1`. These definitions also cover SQL routine bodies
and stored views, including a bounded native Dump/Reset/Restore phase.

## Closed source providers

Source-capable nodes implement `expr.Source` and `SourceOpening` beside their
family (`*_source.go`); there is no central function/provider registry or cursor
tag. Admission, set cardinality, scalar OUT labels and physical record layout
are separate questions. `String` admits only `string_to_table`, ACL excludes
containment, RangeInspect admits only the constructor, and Binary admits only
its selected JSON carrier. This is not general scalar-FROM fallback.

JSON/string/ACL providers use their existing native cursors and named next/close
callbacks; integer/subscripts/array advancement stays by the engine-owned
markers. JSONB record/path and regexp sets preserve complete-private conversion
before publishing any row (including demanded late errors under LIMIT).
Argument evaluation remains once per open in native order, using owner fields,
not a retained `Children` slice. Scalar NULL is one present row; EOF is typed
NULL with false presence. Every demanded invocation has independent state, and
close/EOF/error/cancellation release it without invalidating emitted values.

Occurrence hooks preserve each family's exact existing identity, not structural
child/group equality. In particular RegexpSet compares concrete family only
after engine set/position guards. Equivalent-child substitution preserves
selected providers and written-slot provenance; it never calls Reconstruct to
resolve a replacement provider. Catalog/session/installed-intagg authority and
query scheduling remain in the engine, not this package.

## Binder

`Binder` (`binder.go`) is what bind code may ask of the engine's analyzer; the
`analyze.Binder` implements it with thin adapters
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
  `routine_provider.go` owns the provider type and 23 family constants;
  engine declaration tables and callers use these `functions` names directly.
  The intentional `RoutineProvider = expr.RoutineProvider` alias lets catalog
  routine declarations hold the expr-owned representation without importing
  functions. There are no engine-local provider aliases.
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
  string variadics and SQL/JSON do the same. It reports whether the argument
  was such a parameter: a `JSON_OBJECT` key keeps it unfinalized, other
  untyped keys finalize to text (added for SQL/JSON constructors; callers
  that don't branch ignore it).
- `NoteCatalogText`: the statement renders a catalog-identifier value
  (`regclass` and friends) as text, so execution captures the catalog
  snapshot that output reads. It is statement-level analyzer state.
- `NoteCatalogSnapshot`: analysis-only catalog demand, distinct from
  `NoteCatalogText`'s execution flag. Catalog username/linkage binding notes both
  at its existing successful boundary; neither method captures authority eagerly.
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
- `JSONReturningType`: resolves a SQL/JSON `RETURNING` type name with the
  statement's type input (RETURNING-forbidden forms rejected). The JSON
  aggregates use it; the SQL/JSON constructors will.
- `SequenceArgument`: a sequence call's regclass provider input. It shares
  the engine's regclass input with explicit regclass casts and owns prepared
  sequence-literal capture: held constants keep their captured provider rather
  than following a later name replacement (`AGENTS.md`). In a column DEFAULT it
  admits only a non-NULL captured literal. A genuine analyzer capability: the
  capture cell and its reanalysis rules are statement-analysis state.
- `SequenceTemporary`: the TEMP provenance captured with that provider. In a
  column DEFAULT it also validates the provider for the consumer and ends
  call-site literal capture, since stored defaults never enter prepared
  reanalysis. A genuine analyzer capability (TEMP lifetime follows the physical
  session, which only the engine knows).

`AggregateBinder` extends `Binder` with what is valid only while an aggregate
call (`parser.AggregateExpr`) binds, where the engine owns grouping state.
Aggregate and window bind code takes it:

- `AnalyzeAggregateFilter`: the aggregate's bound FILTER (nil without one).
  `AnalyzeFilter` discards the node; aggregates keep it for the call.
- `FinishAggregate`: the engine's grouping finish of an `AggregateCall`:
  ORDER BY and DISTINCT binding (unless the call carries WITHIN GROUP keys),
  the window-nesting check, merging of equal calls, registration, and the
  occurrence node. Ownership checks and SQL/JSON RETURNING stay in dispatch.
- `DomainTypeName`: an aggregate operand's diagnostic spelling as min, max,
  range_agg and the ordered-set family report it: domains are qualified by
  the type search path and lookup errors propagate. It differs from
  `DiagnosticTypeName` (which never fails and qualifies every user type).
- `CollationName`: catalog collation spelling for the 42P21 messages of the
  WITHIN GROUP families.
- `CommonArgumentUsage`: the labeled common-type selection (a CASE-style
  42804 naming the label), which the nonthrowing `CommonUsage` cannot report.
  Hypothetical-set argument pairs use it.
- `CollationRelabel`: the engine's metadata-only collation/typmod node, which
  hypothetical-set pairs wrap their coerced inputs in.

The INSERT ... VALUES namespace that `sum` and `bit_*` bind in is not a
method: `AggregateDefinition.ValuesNamespace` asks the engine to apply it.

Argument analysis intents are values, not methods: `ThresholdArrayArgument`
is the comparison operand whose unknown-NULL annotation may name an enum
array, as `width_bucket`'s threshold array may. `ZoneArgument` is a
`timezone`/`date_add`/`date_subtract` operand, whose explicit casts admit the
zone family's temporal targets. `RecordBaseArgument` is a
populate-record base: named casts to user-defined composites keep their
occurrence identity. The coercion intent `RecordFormalArgument` finalizes a
fixed RECORD formal: a parameter keeps its inferred record type, other
operands get an anonymous record. `BareProjectionArgument` binds an
aggregate or window argument in projection context without the unknown-NULL
annotation (which changes deparse and grouping identity, for example of
`count(NULL)`). `AssignmentFormalArgument` binds an unanalyzed argument in
assignment context to its formal, as the statement-level `current_setting`
and `set_config` calls do (`BindSettingCall`). `JSONFormattedArgument`
applies a planned explicit FORMAT JSON input conversion.
`BareFormalArgument` applies a planned implicit conversion to an already
finalized argument at the raw argument's position, without a cast routine
identity: `pg_get_acl` and the access inquiries coerce their fixed formals
that way, and reporting a cast routine would add stored dependencies.
  Deparse known-child conversion uses the existing `PreserveProperties` request
  to keep analyzed dependence while omitting cast-routine provenance. Other
  callers keep the default coercion-property behavior.

`age(xid)` constructs its private transaction-identity node here from the
datetime resolver's already analyzed/coerced operand. It needs neither an
engine node-builder callback nor another Binder method or analysis pass.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Evaluation capabilities

`expr.Context` carries the runtime services every family may use. Tier C
families, whose evaluation reads engine state owned by a subsystem, get a
narrow capability interface here instead; a node recovers it from
`expr.Context` by type assertion, and `*exec.Context` implements it with a
compile-time assertion, forwarding the body to the engine's statement host. `expr.Context` does not grow per family.

- `ACLRuntime` (`acl_runtime.go`, the ACL SQL functions): `ACLAuthority`
  (also `ACLAuthoritySource`, which the aclitem algorithms take) is the
  executing statement's role-store ACL identity, nil without a live
  statement scope. `ObjectACL` is `pg_get_acl`'s raw ACL read,
  `RoleMembership` is `pg_has_role` (a nil actor is the expression's current
  user) and `RowSecurityActive` is `row_security_active`. Each inquiry
  borrows the statement's authorization, private catalog and role snapshot
  under the engine mutex for one call (`internal/engine/acl_runtime.go`);
  the node passes values (OIDs, names, privilege text) and caches nothing.
- `TransactionInquiryRuntime` (`transaction_inquiry_runtime.go`) supplies age,
  current/assigned ID, status, members and commit timestamp snapshots through
  `*exec.Context`. Identity calls initialize/check the evaluation
  context before evaluating children; child errors and strict NULL then precede
  live-scope demand. Scalar member misuse checks scope before its invocation
  error. Timestamp calls deliberately have no such eager context prelude:
  unary errors/NULL, xid0 and bootstrap/frozen handling precede runtime demand.
  Only ordinary lookups and last-completion reads require scope/tracking.
  The engine retains catalog/domain/epoch fences, visibility allocation/history,
  completion publication and lock-manager tickets. Timestamp/last snapshots are
  copied under `Engine.mu`; SQL values are constructed after unlocking. Members
  use the manager's single existing snapshot clone, then pack owned SQL records
  outside both locks; the capability returns owned values, not manager records
  or a second transfer-member buffer. Nodes retain no execution authority.
- `CatalogInquiryRuntime` (`catalog_inquiry_runtime.go`): finite username,
  object-address, visibility, index-property, comment and deparse operations.
  Nodes pass evaluated typed values, fixed-size value arrays, IDs, result
  descriptors and immutable provenance, never children or engine structures.
- `SessionInquiryRuntime` (`session_inquiry_runtime.go`): identity values,
  physical TEMP namespace getter/classifier and effective schema arrays.
  `*exec.Context` implements both with compile-time conformance assertions.
  Captured catalog/roles/comments/paths, authorization recapture, locks,
  current-schema caches, pending-TEMP staging/publication, FK/rule/view authority,
  address resolution, deparse engines and serial ownership remain engine-owned.
  Nodes retain no Session/Engine/StatementScope or executable authority cache.
  Missing runtime/context and foreign-owner failures remain ordinary Go errors.
- `AdvisoryRuntime` (`advisory_runtime.go`) accepts evaluated signed keys,
  key-space, action, mode, lifetime and result type. The engine builds canonical
  resources, selects the current session/transaction ticket and retains admission,
  wait/readmission and unmatched-unlock notices. `BlockingRuntime`
  (`blocking_runtime.go`) accepts only kind, evaluated PID and result type.
  Engine authorization is checked under `Engine.mu`; safe-snapshot capture stays
  there, but ordinary manager reads run after releasing it. Neither capability
  passes managers, scopes, tickets, callbacks or captured owners into this leaf.
- `SequenceRuntime` (`sequence_runtime.go`): `SequenceAuthority` runs the
  engine's evaluation prelude (cancellation and any active domain-check input)
  and requires the live statement scope before any argument is evaluated.
  `Sequence` takes one `SequenceRequest`: the call name, the bound provider
  reference or the OID read from the argument, TEMP provenance, the identity
  marker and setval's still-unconverted value/is_called inputs. The engine
  adapter (`internal/engine/sequence_runtime.go`) keeps the catalog epoch fence
  before and after the RowExclusive sequence lock, ACL and read-only checks,
  generator reservation and the physical-session `currval`/`lastval` memories,
  in the original order; setval's inputs are converted after the privilege
  checks, exactly as before. The node evaluates every argument once, left to
  right, returns a typed NULL without opening the sequence when any is NULL,
  and narrows identity results to the column type.

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
- `OwnsSpelling` (requires `SharedGates`): namespace inquiry alone retains
  child transformation, builtin visibility, then exact-spelling diagnostics.
  This does not exempt labels or explicit `ResolutionOnly`, nor make quoted
  noncanonical spellings succeed.

One more field covers families whose chain branch used a predicate rather than
a name list:

- `Claims`: the chain's admission predicate, replacing `Names` matching. It
  keeps each predicate's own comparison (exact, case-folded or lowercased,
  which differ for spellings such as `ſ`). `Names` still lists every claimed
  name; the registry tests check that each satisfies `Claims`.

Advisory names are case-insensitive claims with `SharedGates` and `OwnsLabels`:
visibility precedes DEFAULT rejection, and the family rejects labels after all
children have been analyzed. Blocking inquiries deliberately have no shared gates:
all children and the argument limit precede visibility and exact signature
checks, as in their former early engine branch. SQL-overload competition remains
engine-owned and precedes registry lookup.

Sequence calls are not registered. The lexer and grammar give
`nextval`/`currval`/`setval`/`lastval` their own `parser.CallExpr` syntax, and the
engine's expression dispatch calls `BindSequenceCall` directly for it, as it
does `BindJSONPredicate`. No shared gates, labels or SQL-overload competition
apply, as before the move.

Registered families:

| Family | File | Names | Fields |
|---|---|---|---|
| UUID extraction | `uuid_extract_bind.go` | `uuid_extract_*` | none |
| Math | `math_bind.go` | `mathNames` | `ExactSpelling`, `SharedGates` |
| UUID generation | `uuid_generation_bind.go` | `uuidv4`, `uuidv7` | `ExactSpelling`, `SharedGates`; `uuidv7` also `OwnsLabels` |
| `format` | `format_bind.go` | `format` | `SharedGates` |
| uuid-ossp | `uuid_ossp_bind.go` | `uuid_generate_v4`, `uuid_nil`, `uuid_ns_dns`, `uuid_ns_url`, `uuid_ns_oid`, `uuid_ns_x500` | none |
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
| ACL values | `acl_value_bind.go` | `acldefault`, `makeaclitem`, `aclexplode` | none |
| Object ACL | `object_acl_bind.go` | `pg_get_acl` | none |
| Access inquiries | `access_inquiry_bind.go` | `pg_has_role`, `row_security_active` | none |
| Transaction identity | `transaction_identity_bind.go` | `mxid_age`, `pg_current_xact_id`, `pg_current_xact_id_if_assigned`, `pg_xact_status`, `pg_get_multixact_members` | parser `Claims`, before shared gates |
| Commit timestamps | `commit_timestamp_bind.go` | `pg_xact_commit_timestamp`, `pg_xact_commit_timestamp_origin`, `pg_last_committed_xact` | parser `Claims`, before shared gates |

`datetimeNames` covers extract/date_part, date_trunc, date_bin,
timezone/date_add/date_subtract, justify_*, to_char/to_date/to_timestamp and
age/isfinite. The XID age branch uses the transaction-identity owner directly;
temporal overload selection and unknown-argument ambiguity are unchanged.
`stringNames` is the final switch's string list, including the
argument-analyzing `trim`, which PostgreSQL does not have as a function.

Calls of these families that keep an engine dispatch branch over an exported
entry point, because they need engine state the registry cannot express:

- `overlaps` (`BindDatetimeOverlaps`): the engine sets `WrapperContext` from
  inline safety, an engine node walk that F2 replaces.
- `similar_to_escape` (`BindSimilarEscape`): claimed by call syntax, not name.
- `current_date`, `current_time[stamp]`, `localtime[stamp]`
  (`BindCurrentDateTime`): SQL value functions, dispatched by syntax. The
  precision warning is returned as a value; the engine delivers it unless a
  held description is being re-analyzed.
- `int_array_aggregate` as an aggregate: the engine's aggregate and window
  binders call `AnalyzeIntaggArguments` and `ResolveIntagg`, then keep FILTER,
  grouping and the aggregate call (its procedure identity is engine state).
- Not name dispatch: AT TIME ZONE (`BindTimeZone`), the POSIX and SIMILAR TO
  operators (`BindPosix`, `BindSimilar`), `||`/`^@` (`StringNode`), the JSON
  operators and the SQL/JSON syntax (below).

The JSON and JSONB operators are dispatched by the engine's builtin operator
binder and call `json_operator_bind.go`: `BindJSONExtraction` (`->`, `->>`,
`#>`, `#>>`), `BindJSONBExistence` (`?`, `?|`, `?&`), `BindJSONBPathDelete`
(`#-`), and the branches that other operator selectors reach with analyzed
operands: `SelectJSONBConcat` (jsonb `||`, from the string `||` selector),
`SelectJSONBDelete` (jsonb `-`, from arithmetic) and `BindJSONBContainment`
(jsonb `@>`/`<@`, from array membership). `JSONExtractionName`,
`JSONBExistenceName` and `JSONBContainmentName` serve dispatch, stored scalar
SQL and index inquiry. They build `Binary` nodes with the operator's routine
identity (`BuiltinOperatorRoutineInfo`) and need no `Binder` method beyond the
existing set.

`BindJSONBPathOperator` (`@?`/`@@`, recognized by `JSONBPathOperatorName`) is
reached only after catalog overload selection supplies JSONB/jsonpath formals.
It reuses the selected argument slots and merges both children's properties with
immutable intrinsic behavior. Competing `@@` text-search signatures retain their
existing provider boundary. These Binary nodes capture the real two-argument
`jsonb_path_exists_opr`/`jsonb_path_match_opr` routines, not the four-argument
query functions. Generic reconstruction/retagging rebuilds the hidden consumer
usages; rendering uses qualified infix SQL and does not classify them as calls.

SQL/JSON constructors and `IS JSON` are syntax-dispatched
(`parser.SQLJSONExpr`, `parser.JSONPredicateExpr`). `BindSQLJSON` binds
`JSON_ARRAY`, `JSON_OBJECT`, `JSON`, `JSON_SCALAR` and `JSON_SERIALIZE` into
the `SQLJSON` node and returns the RETURNING usage; the engine applies that
assignment coercion, as it does for the JSON aggregates (`BindReturning`), and
keeps the subquery form `JSON_ARRAY(SELECT ...)`. `SQLJSONInput` applies FORMAT
JSON and the implicit JSON input through the `JSONFormattedArgument` coercion
intent; the JSON aggregates' FORMAT JSON input uses it too.
`SQLJSONReturningType` resolves RETURNING with the SETOF and pseudo-type
checks. `BindJSONPredicate` binds `IS JSON` into `JSONPredicate`.

`BindBinary` takes a precomputed visibility for the engine's analyzer tests;
it reaches the text routes `BindPosixSubstring`, `BindSimilarSubstring` and
`FinishString`. `FinishBinary` is shared with the string family's binary
candidates and the engine's `||` binder.

The LIKE operator binder is ported onto `Binder` but stays in the engine,
because its node is `plan.LikeExpr` and `plan` imports `functions`.

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
`JSONBPathQueryName` serves aggregate binding.

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
The engine also calls `ResolutionError` and `SignatureHint` directly, without
the former package-level math aliases. Its distinct
`analyze.Binder.functionResolutionErrorForTypes` method remains: it notes
only missing signatures (`!ambiguous`), preserving composite-projection
fallback. Actual `SignatureError` adapters and deliberately unnoted builder
paths retain their existing responsibilities.
`operator_error.go`'s `OperatorError` is the operator form of both
diagnostics; `ComparisonError` respells its 42883 with qualified diagnostic
type names, and `ArrayOperatorError` with element names and `[]` for array
operands (the array, range and JSONB operators, inside and outside the
engine). Pure supply helpers serve bind code in and out of the engine:
`TemporalTypesFor` and `SupplyBinaryTypes`.

### Aggregate and window registries

Aggregates are not name-chain calls: they arrive as `parser.AggregateExpr`,
whose name the parser has folded. `aggregate_registry.go` holds the static
`aggregateDefinitions` table, matched exactly by `LookupAggregate`. The
engine's aggregate dispatch (`expression.go`, `aggregate_dispatch.go`)
consults it after the shared placement gates (recursive term, EXECUTE
parameters, generated, index, CHECK and VALUES consumers), owns the result
(`ownAggregate`) and applies any SQL/JSON RETURNING coercion. An unregistered
name binds as `count`, for its missing-function diagnostics; only
`int_array_aggregate` keeps an engine branch (installed-procedure identity).

- `Bind` analyzes arguments, then FILTER, resolves the signature, selects the
  provider and calls `FinishAggregate` with an `AggregateCall`: provider,
  `AggregateOperation` identity, routine, coerced argument, separator, direct
  arguments, stage width, `IncludeNulls`, `RetainUntypedArgument` (count's
  ANY formal) and WITHIN GROUP `Order` keys.
- `BindReturning` replaces `Bind` for the JSON aggregates and also returns the
  RETURNING usage.
- `Window` resolves the signature of the aggregate called with OVER, with the
  same analysis and no provider or group state; `ResolveWindowAggregate` is
  the engine's entry point (unregistered names get the 42883). The WITHIN
  GROUP aggregates have none: they reject OVER while binding.
- `ValuesNamespace` applies the INSERT ... VALUES diagnostic namespace.

| Family | File | Names |
|---|---|---|
| count | `count_aggregate_bind.go` | `count` |
| sum, avg | `sum_aggregate_bind.go`, `avg_aggregate_bind.go` | `sum` (`ValuesNamespace`), `avg` |
| min/max, any_value | `extrema_aggregate_bind.go`, `any_value_aggregate_bind.go` | `min`, `max`, `any_value` |
| boolean, bitwise | `boolean_aggregate_bind.go`, `bitwise_aggregate_bind.go` | `bool_and`, `bool_or`, `every`, `bit_*` (`ValuesNamespace`) |
| statistical | `statistical_aggregate_bind.go` | `statisticalNames` |
| string_agg, array_agg, range | `string_aggregate_bind.go`, `array_aggregate_bind.go`, `range_aggregate_bind.go` | `string_agg`, `array_agg`, `range_agg`, `range_intersect_agg` |
| JSON | `json_aggregate_bind.go`, `json_object_aggregate_bind.go` | `jsonArrayAggregateNames`, `jsonObjectAggregateNames`, `json_objectagg` (`BindReturning`) |
| WITHIN GROUP | `ordered_set_bind.go` | `percentile_cont`, `percentile_disc`, `mode`, the rank family |

`window_function_bind.go` holds the `windowFunctions` table
(`LookupWindowFunction`): `row_number`, `ntile`, `first_value`/`last_value`/
`nth_value`, `lead`/`lag` and the rank family. `Signature` analyzes the
arguments and FILTER and returns a `WindowSignature` (argument, ordinal,
default, result usage); the engine uses it in window binding and, for calls
without OVER, before its "requires an OVER clause" error. The rank family is
in both tables: window function with OVER, hypothetical-set aggregate with
WITHIN GROUP.

Shared SQL-overload competition includes the existing `WindowRoutine`
declarations, not only scalar and aggregate declarations. The window table's
optional `ProbeCall` reuses `ResolveWindowValue` and `windowOffsetTargets`;
the latter is also the lag/lead `Signature` owner's value/default common-type
calculation. Concrete zero-argument, ranking and ntile shapes use the same
declared-input metadata matching as other concrete routines. No signature probe
binds children, infers parameters or builds an executable window provider.

A selected window identity returns to the existing qualified window binder,
including its missing-OVER, modifier, clause and same-query nesting checks.
The missing-OVER primary retains the original lexical callee independently of
qualified lookup authority. Its scoped spelling is consumed before children are
bound, so nested diagnostics cannot borrow the enclosing call's name.
Already-bound actuals retain their existing lexical window-occurrence evidence,
including through SQL scalar-call arguments; child queries remain separate.
An actual scalar SQL winner with OVER still reports wrong-kind42809. No
unconditional OVER bypass or new window executor is introduced.

Public coexistence and diagnostic proof:
`make check RUN='^TestReferenceRoutineWindow(Coexistence|Polymorphic|Diagnostics)$'`
and the same selector with `REFERENCE=1`. Package-function target proof:
`make check RUN='^TestSQLRoutine(ProviderCallShapes|WindowCallShapeTargets)$'`.


Providers live here too: the money/interval SUM providers, the JSON object
provider (`JSONObjectAggregate`, completed by `FinishAggregate` once ORDER BY
matching settles its input usages) and the WITHIN GROUP providers
(`OrderedSet`, `HypotheticalSet`) behind `OrderedAggregate`. The executor
sorts the staged input and lends it as an `OrderedInput` whose `Compare` is
the call's tuple ordering. `AggregateOrdering` (sort comparison for ORDER BY,
WITHIN GROUP and DISTINCT inputs) is shared with the engine's finish.

To add or port a builtin family:

1. In `analyze`, turn `func (b *Binder) bindX(...)` into
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
6. Capture-admitted nodes implement `expr.Capturable` beside their definition.
   Reuse Composite children only when they exactly match written operands and
   borrow safe slice storage, never fixed arrays from a value-receiver copy.
   Fixed-storage nodes implement value-receiver `expr.IndexedCapturable` locally:
   `CaptureOperand` maps existing WrittenSlots/WrittenCount or written Args
   directly, with checked bounds and no reordered copies, retained slices or
   receiver reboxing. `CaptureOperands.Indexed` carries only the storage mode;
   `Check(source, count)` and `At(source, written)` receive the original boxed
   Capturable (including promoted wrappers). Safe slices and at most two inline
   operands remain available. Array formal slots and EvaluationOrder do not
   reorder capture. SQL-standard calls retain parser Args order, not Pieces.
   Hooks never evaluate, render, rebuild, rebind or mutate. Engine wrappers win
   over promoted hooks; catalog/coercion decisions and annotation maps stay there.
   Helpers still used by function sources/aggregate binding remain exported.

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

`Advisory` and `BlockingInquiry` rebuild immutable recipes with mapped usages,
positions, routine references and children, retaining action/mode/lifetime/kind.
Blocking reconstruction refreshes only Property from its child plus VOLATILE,
matching its old conditional simplifier; its `Substitute` preserves the supplied
Info exactly for statement preflight and grouping/stage substitution. Scalar
type-generation retagging and view remapping use reconstruction, refreshing
blocking child-derived properties while retaining mapped descriptors.
Both are intrinsically VOLATILE, never planning-evaluated, and own capture,
group identity, function syntax and exact SQL/tree presentation. Advisory uses
`StrictAfterBare`/`PreflightNullBare`: demanded immutable child errors precede
bare-NULL folding. Runtime NULL still evaluates every child once in written
order using fixed two-value scratch. Blocking keeps its live carrier during
planning and reports known nullable array metadata. Replay validates stored
signatures; advisory remains relation-only. These hooks do not widen DEFAULT,
generated/index, source or parallel admission. The existing
`BareFormalArgument.PreserveProperties` request preserves advisory's old bare
cast properties without adding a cast routine identity; other callers are
unchanged.

`SequenceCall` rebuilds with the supplied Info and children while keeping its
provider `Ref`, TEMP `Temporary` and `Identity` marker: these are catalog facts,
and the engine's catalog consumers (stored DEFAULT SQL and replay, DEFAULT TEMP
validation, `closedSequenceDefault`, dependency references, view remapping of
`Ref`, COPY's nextval volatility and the identity DEFAULT constructor) keep
explicit `functions.SequenceCall` cases reading those exported fields. Generic
branches cover walk, join legality, retag, conditional simplification
(`FoldNever`), preflight (`PreflightChildren`), grouping identity (name, provider,
TEMP provenance and children; not the identity marker), grouping rewrite (it is
not a `GroupLeaf`, so `lastval()` is still staged), aggregate occurrence, index
function syntax, view literal capture (`CaptureFunction`) and the display/tree
rendering (`Sequence <name> oid <n>`, a bare call name quoted only under
`quote_all_identifiers`, call arguments with implicit casts shown). Stored
scalar SQL never replays that rendering; its explicit engine case validates and
records the provider. It has no intrinsic volatility, strict fold, result
nullability, stored-provider or closure hook, so those consumers are unchanged.

Nodes also implement the optional `expr` hooks that the engine's switches ask
(the `expr` README defines them):

- `ResultNullability(ResultSite)` on `AccessInquiry`, `ObjectACL` and
  `ACLValue`: known nullable at both SELECT and RETURNING descriptor sites,
  including every ACL kind. Value receivers preserve the capability on values
  and pointers. These immutable recipe answers never evaluate arguments, read
  authority/catalogs, allocate or mutate. The hook is independently optional,
  not a requirement of `Composite`; no hook or a declined claim preserves both
  descriptor fields. Engine-owned column/grouping/aggregate/window and
  OLD/NEW/MERGE precedence remains at the consumers. `Deparse` and
  `IndexProperty` claim only SELECT; `Identity` claims only for current_schema.
  CurrentSchemas and namespace inquiries retain their both-site answers.
  No generic strictness inference or wrapper propagation is introduced; a
  literal-NULL folded index inquiry is a literal, not a retained inquiry claim.

- `IntrinsicVolatility` on every node: the value its planned volatility,
  generated admission and `rewrittenIntrinsicProperties` use.
  Seven catalog/session owners deliberately return retained analyzed volatility
  (Identity, CurrentSchemas, NamespaceInquiry, VisibilityInquiry, IndexProperty,
  CommentInquiry and Deparse), preserving eliminated-arm dependence even in
  intrinsic-only walks. CatalogUserName, CatalogForeignKeys and ObjectAddress
  instead supply STABLE plus physical-child inspection.
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
- `SameGroup` on every node that grouping can match: its own identity (kind,
  plan, written-slot provenance where it matters), with children compared by
  the engine. Presentation never counts: TRIM spelling, `pg_catalog`
  qualification, redundant VARIADIC. `CurrentDateTime` precision counts only
  for CHECK definition equality.
- `GroupProperties` on IndexProperty and Deparse merges STABLE with rewritten
  child properties without forcing stage dependence. This grouping-only hook
  does not run during substitution, preflight or retagging.
- `SnapshotDemand` reports only the owner's analysis/catalog-text/visibility
  demand using the caller's literal-NULL predicate; child demands stay separate.
  Deparse suppresses its own analysis demand for decisive bare NULL, but its
  stored Replay.CatalogSnapshot remains unconditional.
  Retained VisibilityInquiry uses Replay.AlwaysVisibilitySnapshot even for a
  coercion-wrapped NULL; binary conversions retain NULL-sensitive replay demand.
- `CallCost` preserves username/visibility cost 10 and comment cost 100 for
  inline policy; it does not execute or recapture authority.
- `GroupLeaf` on the three leaves, which the grouping rewrite leaves
  unstaged.
- `PreflightFold` on nodes whose preflight is not "children, then conditional
  simplification" (see the `expr` README for the kinds), and
  `SpecializeTemplate` on `DatetimeFormatting` and `NumericFormatting`.
- `Substitute` on every node with children: the old in-place child swap that
  preflight and the grouping rewrite rely on, without rebinding the provider.
  `SQLJSON` and `JSONCreate` rebuild instead, as preflight always did for
  them.
- `EvaluationOrder` on `ArrayTransform`, whose `Args` keep written slots.
- `LooksLikeFunction` for index key display: true for calls written as calls,
  false for operators (`||` String, ArraySearch membership, RangeOperator,
  JSONB existence and containment `Binary`), AT TIME ZONE and the set,
  pattern and volatile array calls that never reach an index.
- `ClosureTransparent` on the calls that both closed-DEFAULT checks look
  through.
- `ClosedDefault` (`expr.ConditionalClosure`) on `ACLValue`: scalar kinds
  inspect every written argument with the caller's predicate; `aclexplode`
  refuses unconditionally. Only transaction-time closure adopts this hook,
  after dependency and explicit-leaf checks and before transparency/fallback.
  The unconditional `ClosureTransparent` marker remains separate.
- `Render` on every node the deparser and node-tree output display: one
  `expr.Rendering` with the node-tree header and inputs, and the SQL shape
  (leaf text, call, infix operator or keyword-syntax template) with the
  quoting, implicit-cast display, labels, VARIADIC and display casts that
  PostgreSQL's ruleutils uses for that call. `render.go` holds the shared
  builders: `callRendering`, `writtenArgs`/`writtenCall` for written-order
  calls with labels, `infixRendering`, and `syntaxPieces`/`trimPieces` for
  OVERLAY, SUBSTRING, POSITION and TRIM. `Render` runs the validation either
  renderer ran before. `JSONPredicate` and all five argument/member SQLJSON
  constructors render through the template form. SQLJSON requests the original
  formatted-input source through `RenderArg.JSONFormattedSource`, keeps implicit
  casts hidden and operands in function-argument position, and marks its
  representation boundary with `JSONConstructor`. Only ARRAY, OBJECT and
  SERIALIZE expose `JSONReturning` and a named optional type slot. The engine
  supplies that usage only from the constructor-owned RETURNING conversion;
  JSON/SCALAR and genuine enclosing casts never acquire a RETURNING clause.
  `SQLJSONPieces` is the shared syntax-only recipe for bound presentation and
  raw stored views, including empty OBJECT/ARRAY whitespace, member VALUE syntax
  and FORMAT/ENCODING suffixes. Tree headers and children are unchanged.
  The extension nodes (`Soundex`, `Metaphone`, `Levenshtein`, `Pgcrypto`) have
  no `Render`: only their engine wrappers display, with the installed
  procedure's schema.

A new node implements `Children`, `Rebuild`, `Substitute`,
`IntrinsicVolatility`, `Render` and `SameGroup`, and each other hook whose
default (fold as a constant, never strict, not a stored provider, rebuild on
change, preflight by simplification, parenthesized index key, not looked
through by DEFAULT closure) does not describe it. `Arguments()` is gone where
it duplicated `Children()`; nodes slice their argument arrays directly.

## What stays in the engine

The engine owns the analyzer behind `Binder`, the bind code of families not
yet registered, function column definition lists and JSONB record star
expansion, name/catalog/ACL and dependency resolution, parameter/result contracts, descriptor generations,
retag/remap and stored SQL presentation. The capabilities supply existing
authority without copying rows or the full evaluation context. Installed
extension routines keep their wrapper nodes in `plan` (for example
`plan.InstalledUUIDOsspExpr`, `plan.InstalledCryptoExpr` and
`plan.InstalledTextExpr` for unaccent and `daitch_mokotoff`) and stored
identity; the engine validates that identity, and manifest validation lives
in `extension`. Registered bind code
reaches them only through `InstalledRoutine`. UUID, pgcrypto and genuine SQL routine DEFAULT admission
(`ClosedUUIDDefault`, `ClosedPgcryptoDefault`, `ClosedSQLRoutineDefault`) is in
`analyze`, shared by the DEFAULT binder and the engine's stored-expression
validator. Registered
function nodes replay through `expr.Rendering` (`Replay` in the `expr` README);
engine SQL routine wrappers retain their checked descriptor/reference instead.
The SQL/JSON constructor's query form and RETURNING conversion stay in
`sqljson_expression.go` and `sqljson_query.go` in the engine. Generic display
and replay interpret its `Rendering`; `sqljson_deparse.go` retains only
protocol-boundary and formatted-source helpers plus predicate assembly.
The engine owns conversion provenance, catalog-qualified declarations,
typmods/domains, literal transport, raw occurrence/capture maps and query
rendering. Capture and presentation request formatted-input unwrapping without
owning the coercion or stripping genuine user casts.

Ordinary calls whose spelling resembles an aggregate or type are normalized
locally by the engine binder after shared namespace/signature selection, before
aggregate-clause rejection or name-only builtin dispatch. Actual aggregate
winners return to the existing aggregate owner; SQL winners use private proc
frames and the same root statement effects. There is no second function binder
or query executor here. Frozen presentation reuses the engine's routine
occurrence map for both SQL and installed providers; a normalized-only syntax
marker demands a checked capture but never supplies catalog authority.

The ACL SQL functions keep these engine parts: the `ACLRuntime`
implementation; `aclexplode`'s set-returning admission and function scan,
which open the node's `ACLExplodeCursor` (`string_set_query.go`,
`string_table_bind.go`, `string_set_lower.go`); the `@>` selection in array
membership, which calls `BindACLContains`; raw/bound view literal pairing
(`view_literals.go`); the closed-DEFAULT check (`aclexplode` is never
closed); and the stored-default guard that rejects `pg_get_acl` and the
access inquiries outside a relation-owned definition (`default.go`). The
aclitem and regrole input and output adapters (`aclitem_input.go`,
`aclitem_output.go`, `regrole_input.go`, `regrole_output.go`) are statement
input and output state over the role store, not function nodes, and stay
with the engine's presentation code; `containsEmptyACL` (`acl_empty.go`)
serves Dump and deparse.

Physical-session RNG, UUID clock/entropy and SQL time ownership stay in the
engine: `random.go`, the session generators behind the `exec.Context` clock
and sleep methods, and the closed DEFAULT admission of clock and sleep
calls (`closedTransactionTimeDefault`). Function evaluation borrows the same owners only when demanded. Literal
NULL folding and reached runtime argument effects remain distinct. Aggregate
providers own execution-local state; the engine owns FILTER/DISTINCT/ORDER,
grouping, windows and state lifetime: `finishAggregate`, `ownAggregate`, the
grouping switches of `aggregate_bind.go`, window specs, frames, planning and
execution (`window_bind.go`, `window_query.go`), ordered aggregate sorting and
replay (`aggregate_query.go`), `pg_aggregate` and intagg's procedure
identity. Statement-level `current_setting`/`set_config` bind through
`BindSettingCall`; their execution and session-settings locking stay in
`setting_calls.go`. Query/set expansion and physical regex
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
binding.

### Catalog inquiry and session identity

All ten immutable owners live here: CatalogUserName, CatalogForeignKeys,
ObjectAddress, NamespaceInquiry, CurrentSchemas, VisibilityInquiry,
IndexProperty, CommentInquiry, Deparse and Identity. Function-specific binding,
validation, properties, child evaluation, strictness, grouping, substitution,
retagging and display/tree/replay recipes move with them. Exported recipe fields
support genuine retained binder/stored-plan integrations; published recipes must
not be mutated. Derived index normalization and FK payload remain private.

Ordinary calls use the registry after the engine's SQL-callable selection.
Username/linkage and object-address binders retain their own gates; identities
keep their parser predicate, namespace inquiries additionally own spelling, and
current_schemas, visibility, index, comment and deparse retain exact shared gates.
`parser.IdentityExpr` uses `BindIdentity`, not ordinary lookup. Keyword identity
captures `CaptureIdentity`; ordinary-call identity captures `CaptureFunction`.
Folded view literals retain their original typed written-operand pairing.
The adjacent pg_extension_update_paths binder/provider stays in the engine.

The source companions for username, linkage and object addresses live here.
The single attributed 219-link inventory supplies descriptor-owned six-field
payloads: equivalent substitution borrows them, genuine descriptor retag rebuilds
them. Cursor position is invocation-private; Close releases the borrowed state.
Present typed NULL is one row, not EOF. Engine admission, layout, ordinality,
scheduling and installed/catalog authority are unchanged. Scalar describe uses
StrictAfter; record address siblings decline that fold, preserving reached-child
evaluation and import errors. No new per-row adapter or inventory copy is added.

Visibility includes the four path-based inquiries and all three collation
management kinds: actual-version calls remain VOLATILE TEXT, import VOLATILE
INT4. Strict NULL, effective-superuser, namespace and unavailable-provider ordering
remain unchanged; native import never fabricates success. Index AM/name algorithms
are local, catalog index interpretation is not. Comments retain post-NULL lazy
routine admission through immutable provenance. Deparse owns all twelve existing
signatures, including pg_get_serial_sequence, but actual rendering/tree lookup,
catalog locks, relation/column parsing and serial AUTO/INTERNAL ownership stay
in the engine. Sequence calls moved separately (Group A Item 10, see
`SequenceRuntime` above); their execution authority stays in the engine.

Ordering is family-specific, not a universal guard-first rule. Address evaluates
all children once in written order, maps formal slots, then applies strict NULL;
runtime validates scope/comment snapshot before decoding IDs/arrays. Visibility
likewise validates scope before OID decoding; index runtime resolves the relation
before decoding its column ordinal. Username decodes OID before captured-scope/
role-domain lookup. Namespace/current_schemas decode their primitive input before
path/TEMP authority. Comments finish all-child NULL handling before lazy admission,
snapshot and OID decoding; deparse type/NULL checks precede catalog/signature
decoding. These boundaries preserve error priority and reached child effects,
without introducing context copies, per-Eval plans or authority caches.


## Family notes

The ACL SQL functions (`acl_value.go`, `object_acl.go`, `access_inquiry.go`,
`acl_items.go`) bind and evaluate here. `acl_items.go` holds the aclitem
algorithms over raw bits: privilege names (`makeaclitem`), the `acldefault`
masks (its own copy, since this package cannot import `authorization`),
array validation, containment and the `aclexplode` cursor, all checking
cancellation every 256 items. Every node renders schema-qualified or
labelled as before (the `@>` operator through a template, so an explicit
`OPERATOR(pg_catalog.@>)` survives display and replay), never folds during
planning, only preflights its children, keeps its kind, notation, slots and
labels in its grouping identity, and answers index key syntax; only
`ACLValue` is a stored generation provider. `inquiry_call.go` holds the
shared envelope of `pg_get_acl` and the access inquiries.

`ACLValue.ClosedDefault` reads its inline `Args` by the existing kind arity,
without returning a copied receiver's `Children` slice, rebuilding, evaluating,
validating privileges or retaining the callback. Inspection leaves Info, children
and notation unchanged. It does not implement `ClosureTransparent`: UUID closure
keeps the old root-volatility fallback, including stable set-kind carriers, and
does not gain scalar ACL transparency. These checks are admission policies, not
general node-validity checks.

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
embedded here and compiled once into `StockUnaccent`, which
`extension.UnaccentProvider` uses.

`bindInstalledText` keeps the order of the old engine binder:
- unaccent rejects decorations as a missing signature; `daitch_mokotoff`
  reports them with 42809 after argument finalization.
- No implicit coercion to TEXT has a cast routine, so the operand's TEXT
  coercion carries no routine identity.

Installed pgcrypto calls reuse this package's SQL-usage overload selection,
formal coercion hooks and reconstructible `Pgcrypto` carrier. `PgcryptoMembers`
is the 1.4 manifest as plain data. The engine stages and `extension` validates
installations from it, and `bindPgcrypto` reads the selected member's result
and volatility from it, which installed procedures never change. The engine separately
owns installed procedure address, namespace/ACL, intrinsic volatility, dependency,
replay identity and narrow strong-entropy/actual-FIPS context capabilities.
`internal/sqltype/pgcrypto.go` owns admitted signature/result plans, raw digest/HMAC
and random-byte count bounds. `pgcrypto_password.go` dispatches crypt and validates
execution-local private-kind/uint32 salt parameters: des25, xdes odd1..16777215
(default725), md5 1000 or bf4..31 (default6); zero selects the default.
The carrier requests exactly2/3/6/16 entropy bytes for des/xdes/md5/bf using its
sixteen-byte buffer. No entropy is requested for NULL or invalid inputs.
The independent salted DES kernel keeps schedules/expansion call-local, truncates
DES at eight bytes, folds all xdes bytes and polls cancellation. MD5-crypt streams
passwords through the existing context-aware hash helper, with eight-byte salt
truncation. Exact2a/2x bcrypt semantics remain unchanged;2b/2y use DES fallback.
SHA-crypt remains unsupported. Six digest algorithms return raw BYTEA.
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
### Catalog inquiry ownership and selected proof

Pure inventory/cursor, AM-name, node validation, nullability, grouping-property
and volatility units live here. Real analyzer, captured-authority, retag/remap,
stored CHECK/default and serial/catalog integrations remain engine-owned.
Public SQL/control witnesses remain in the existing dual-mode reference families.
No reference definitions or expectations changed in this extraction.

```sh
make check RUN='^TestCatalogRelations(Inventory|Cursor|MissingMetadata)$'
make check RUN='^TestIndexProperty(CapabilityMatrix|ExpressionGuardsAndNull)$'
make check RUN='^TestDeparseConstraintsReviewIdentityMetadata$'
make check RUN='^TestNamespaceIdentityStoredValidation$'
make check RUN='^TestResultNullability(Identity|CatalogInquiry)$'
make check RUN='^TestCatalogInquiry(GroupProperties|IntrinsicVolatility)$'
make check RUN='^TestReferenceCatalogRelations(Inventory|Shape|Roles)$'
make check RUN='^TestReferenceCatalogRelations(Inventory|Shape|Roles)$' REFERENCE=1
make check RUN='^TestReferenceIndexProperties(AccessMethods|IndexDefinitions|Columns|NullAndInvalid|SignaturesAndInputs)$'
make check RUN='^TestReferenceIndexProperties(AccessMethods|IndexDefinitions|Columns|NullAndInvalid|SignaturesAndInputs)$' REFERENCE=1
```

Use external 900-second deadlines. Full public selections belong to
reference/catalogs, sessions, object_address, indexes, patterns_encoding, views,
sequences and dml READMEs. The current pinned oracle is PostgreSQL18.6 UTF8/C.
The rebased implementation with the stored-manifest remedy passed build,
all 70 selected public native/real commands, focused lifecycle races, full
quickcheck and full GOMAXPROCS=8 racecheck, dependencies/import-layer checks and
actual direct/SAME-held/native replay smoke (940 dump bytes). The earlier
default-CPU race failure and startup target misses remain recorded, not waived;
see [engine proof and limits](../README.md#catalog-inquiry-and-session-identity).
`TestVisibilityInquiryStoredNullManifest` failed before the narrow replay remedy
and passed afterward with identical argv. Its real binder/CHECK renderer checks
persisted manifests, bare-NULL folding, unrelated binary-conversion NULL policy
and both sibling orders. Review, integration and post-integration finalcheck
remain separate lifecycle gates recorded in the part reports.


### Transaction inquiry ownership and demand

Private `transaction_identity` and `commit_timestamp` nodes, binders, source
operations and protocol hooks live here. `Rebuild` validates; `Substitute`
preserves the selected provider and immutable metadata without re-resolution.
Members retain nonreflexive source occurrence. No strict-planning fold,
result-nullability, group-leaf or transparent-closure hook is inferred from
execution strictness; conditional DEFAULT closure preserves the existing
separate UUID policy. Display and stored replay share rendering descriptors,
with provider validation deferred to replay.

Genuine demand and immutable-node units:

```sh
make check RUN='^TestTransactionInquiry(IdentityDemand|TimestampDemand|ImmutableProtocols)$'
make check RUN='^TestLookupClaimsRegisteredNames$'
```

Identity demand units pin context-before-child and strict-NULL-before-scope
ordering, member NULL emptiness and scalar misuse diagnostics. Timestamp units
exercise strict/special paths without a runtime capability and ordinary/last
runtime demand. Immutable units retain validation versus substitution, provider
identity, original children, group identity, written capture and lazy replay
validation. Mixed `TestTransactionIdentityBinding` and `TestCommitTimeExpression`
remain engine integrations through real binding/remap/retag and generic hooks;
no existing whole test definition was deleted. Canonical allocator/publication,
MultiXact and source-lifetime tests remain at their engine/manager owners.

Public goldens remain in the unchanged transaction identity, commit-time,
datetime and EXECUTE-authority reference scenarios. Run their bounded selections
in pinned PostgreSQL18.6 mode first and native mode second; see the owning
reference READMEs. On this Item8 worktree the root observed `make build` and the
standalone public-driver smoke pass after code edits: held identity/member and
diagnostic behavior, actual commit timestamps and byte-identical symbolic native
roundtrip (583 observed bytes, not an expected-size constant). Required selected
dual-mode, full-suite, race and review gates are not claimed by that observation.


### Sequence call cutover

`SequenceCall` units here use the real node with a recording runtime: authority
before arguments, every argument once in written order, strict NULL without
opening the sequence, argument-supplied provider OIDs, identity narrowing, the
missing-runtime errors and the immutable protocols (provider-preserving
`Rebuild`, grouping identity, rendering, capture). Generator, lock, epoch, ACL,
read-only and prepared-literal behavior stays covered at its engine owners and
in `reference/sequences`, whose goldens are unchanged.

```sh
make check RUN='^Test(SequenceCall(RuntimeDemand|IdentityResult|MissingRuntime|Protocols)|CaptureOperandsWrittenSlots|CaptureOperandsNoAllocations)$'
make check RUN='^Test(SerialStoredDefaultInvariants|SerialDefaultIdentityProviderTemporaryCollision|QueryParallelEligibility|TableLikeTemporary(DefaultAuthority|RetainedCopy)|Inheritance(MergeStableRemap|RetainedRowAuthority)|TableLikeCookedRowRemap|IdentityOwnCheckBindingAuthority)'
make check RUN='^TestReferenceSequence'
make check RUN='^TestReferenceSequence' REFERENCE=1
make check RUN='^TestReference(Serial|Identity|Default)'
make check RUN='^TestReference(Serial|Identity|Default)' REFERENCE=1
```

Observed for this cutover, each with an external 900-second deadline: the leaf
and engine units above passed; eight public selections passed in quick and pinned
PostgreSQL 18.6 modes (`^TestReferenceSequence`, `^TestReferenceSerial`,
`^TestReferenceIdentity`, `^TestReferenceDefault`, the sequence/serial/identity/default
smoke, the sequence grant/catalog/session/CHECK/DELETE selection,
`^TestReference(View|Deparse|Check|Generated)` and `^TestReference.*(Dump|Restore)`).
The only exception is the pinned run of `^TestReferenceSerial`, where the PG-only
`TestReferenceSerialDefaultProvider(PublicationHold|DropFirst)` failed identically on
unmodified master `4d3568a9d`: oracle drift, not this change. After the rebase onto
`55f377e30`, full quickcheck, build, deps and the functions import check passed, and a
throwaway public `database/sql` probe stayed byte-identical to master (results,
errors, deparse with and without `quote_all_identifiers`, node trees, dependencies and
native Dump/Restore). Racecheck, finalcheck and bench were skipped at the user's
request; no startup code changed.

### Shared integration and protocol selections

### Advisory and blocking inquiry cutover

Private node units live here; real analyzer, lock, replacement and SSI units
remain in their owning engine/manager packages. Existing public SQL golden
definitions in `reference/locking_transactions` and `reference/sessions` are
unchanged. Selected commands (each external deadline 900 seconds):

`TestAdvisoryBlockingDomainInput` uses admitted immutable domain declarations
and substitutes a real Binder-built recipe only in the private context's detached
programme. Normal `CheckDomain` supplies/restores the active VALUE/input frame;
nested checked conversions and moved-node NULL/pruned demand are exercised
without publishing or admitting volatile domain CHECK definitions.

```sh
make check RUN='^Test(AdvisoryNode(RuntimeDemand|Protocols)|BlockingInquiryNodeProtocols|AdvisoryBlockingDomainInput|AdvisorySession(BindingTraversal|CheckPreparationCache|CheckPrepareAllBeforeEvaluation)|AdvisoryXactBindingTraversal|BlockingPids(BindingAndIdentity|TraversalReplay)|CaptureOperandsNoAllocations)$'
make check RUN='^TestReferenceAdvisorySession(Signatures|DiagnosticsConsumers|ReviewQuotedArgumentLimit)$'
make check RUN='^TestReferenceAdvisorySession(Signatures|DiagnosticsConsumers|ReviewQuotedArgumentLimit)$' REFERENCE=1
make check RUN='^TestReferenceAdvisoryXact(Signatures|DiagnosticsStrictness)$'
make check RUN='^TestReferenceAdvisoryXact(Signatures|DiagnosticsStrictness)$' REFERENCE=1
make check RUN='^TestReferenceBlockingPids(Signatures|Diagnostics)$'
make check RUN='^TestReferenceBlockingPids(Signatures|Diagnostics)$' REFERENCE=1
```

Run the additional bounded lifecycle/contention/prepared/safe-snapshot cohorts
specified by the living part plan in both modes, selected race proof, then full
Docker-free quickcheck/racecheck, build/deps and five-run startup bench.
Observed for this cutover: all 13 bounded cohorts passed in quick and pinned
PostgreSQL 18.6 modes, selected race and private node checks passed, and full
quickcheck, build, dependency and import-layer checks passed. Historical full
racecheck attempts exceeded the 900-second deadline or failed unchanged scenario
contexts under observed host contention; they are not passing evidence.
The user subsequently requested quickcheck-only verification, waiving racecheck
and the race-based finalcheck. Fresh quickcheck and public smoke remain required
after synchronization, followed by independent review and safe integration.

Actual-node Eval allocations are unchanged at 0/3/1/1 for NULL, balanced session
acquire/unlock, ordinary empty and safe empty inquiries. Startup already missed
the <1ms target at baseline (1.047–1.284ms/op); post-cutover samples were
3.639–16.814ms/op under substantial observed host CPU pressure. Neither meets
the target; wall-time change cannot be attributed to the extraction from these
measurements. Numbered part verification reports retain commands and output.

The temporary public consumer is session artifact
`local://advisory-blocking-feature-smoke.go`, materialized outside the repository
and run as `go run /tmp/pg18-advisory-blocking-smoke/main.go`. It checks exact
metadata/results, pinned owners, genuinely held wait/cancel/reuse, counted
session rollback survival, transaction savepoint release and live stored-view
Dump/Restore without serializing locks. All five phases passed. The materialized
source and unchanged temporary allocation probe were removed after observation.


Function-source private binding/scheduling/authority units remain engine-owned:
their fixtures require the real analyzer and installed/catalog identities.
Existing sqltype native cursor/kernel units stay beside those kernels. Engine
units now consume `SourceCursor.Next/Close`, presence, typed EOF, independent
state and retained payload instead of obsolete union tags. Public reference
outcomes remain unchanged; the numeric native cancellation fixture observes
`SourceCursor.Next` instead of the removed cursor method, preserving its
same-worker admission, no-publication, join and recovery assertions.

```sh
make check RUN='^TestSourceProtocol(Substitution|Occurrence|CursorOwnership|Presence|Cleanup|Cancellation|BorrowedLifetime)$'
make check RACE=1 RUN='^TestSourceProtocol(CursorOwnership|Presence|Cleanup|Cancellation|BorrowedLifetime)$'
make check RUN='^Test(JSONEachFromPaddingAndTargetSlot|JSONElementsNullPresenceAndPadding)$'
make check RUN='^TestACLValues(Invocation|ScanCancellation)$'
make check RUN='^TestReferenceJSONRecord(Values|Metadata|Grammar|Held|Composition|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceJSONRecord(Values|Metadata|Grammar|Held|Composition|Diagnostics)$'
```

The complete family-specific public selectors remain in their owning reference
READMEs (numeric, binary_json_arrays, strings, patterns_encoding, types,
catalogs, locking_transactions, object_address and object_grants). Each selected
batch requires native and pinned PostgreSQL modes; whole-project quickcheck is
Docker-free, not a substitute for that proof.

**F3 provider and diagnostic owner cutover.**

Private call-shape, authority and cast-diagnostic fixtures remain engine-owned;
only their removed alias names/imports changed. No public reference definitions
or expectations changed. Selected private proof:

```sh
make check RUN='^(TestSQLRoutineProviderCallShapes|TestSQLRoutineCallableShapeMasking|TestSQLRoutineUnknownCategorySelection|TestSQLRoutineOriginalArgumentRecipes|TestSQLRoutineWindowCallShapeTargets)$'
make check RUN='^(TestACLValueRoutineOriginalAuthority|TestJSONRecordRoutineOriginalAuthority|TestJSONBRecordRoutineOriginalAuthority|TestNetworkOperatorRoutineOriginalAuthority|TestNumericSeriesRoutineOriginalAuthority|TestTIDRoutineAuthority)$'
make check RUN='^(TestJSONBPathOpsBinding|TestJSONBPathQueryArrayDeclaration|TestJSONBPathQueryBinding|TestVersionInformationBinding|TestVersionInformationRemap|TestCastExpressionEqualityBoundDiagnosticOwnership)$'
```

Core public provider competition and exact diagnostic proof uses the same
definitions in both modes:

```sh
make check RUN='^(TestReferenceRoutineResolutionProviderCoexistence|TestReferenceRoutineResolutionUnknownPositions|TestReferenceRoutineCoreResolution|TestReferenceRoutineCoreDiagnostics)$'
make check RUN='^(TestReferenceRoutineResolutionProviderCoexistence|TestReferenceRoutineResolutionUnknownPositions|TestReferenceRoutineCoreResolution|TestReferenceRoutineCoreDiagnostics)$' REFERENCE=1
```

The distinct `NetworkRoutineProvider` family needs its own selected proof
(P10), not the network-operator authority selection in P2: those operator
declarations belong to `OperatorRoutineProvider`.

```sh
make check RUN='^(TestReferenceINETFormatMetadataAndBinds|TestReferenceINETFormatDiagnostics)$' REFERENCE=1
make check RUN='^(TestReferenceINETFormatMetadataAndBinds|TestReferenceINETFormatDiagnostics)$'
```

These existing definitions independently assert IPv4/IPv6 format results and
`TEXT`/`INT4`/`BOOL`/`INET` metadata, direct/prepared/reused-handle routes,
NULL/empty results, parameter-error recovery, and exact missing-signature
`42883` primary/Hint/character Position in direct and Prepare paths.
Review1 found this selected native/real proof missing from P0–P9. Root
tests-14 subsequently passed both P10 commands, closing that family-coverage gap.
Do not change the reference bodies or substitute whole-project native passes.

Latest-base root tests-12 establishes 1291 exact-owner migrations
(4 type, 1283 constant, 2 pure-builder and 2 Hint uses), 26 removed declarations,
28 Go files (17 production, 11 private units) and conservation of 1,065,774
non-target bindings with zero loaded type errors. The real diagnostic method
and its eight callers retain missing-only notes. Transaction/commit-timestamp
and advisory/blocking binders already live here and use existing provider
constants and diagnostics. The intentional functions/expr type alias remains.
No runtime work was added. Preceding-base tests-10 proved1293 migrations,
30Go files and1,066,520 conserved bindings; those are historical counts.

Actual public database/sql smoke passed again in tests-12: builtin/SQL overloads,
reused prepared metadata/results, exact direct/Prepare diagnostics, recovery,
stored view/default and native Dump/Reset/Restore. Build passed on the preceding
base in tests-10. On that preceding base, tests-11 passed all three private
batches and all ten selected public provider/authority/metadata/stored groups
in native and real pinned PostgreSQL18.6 modes. Full quickcheck passed in147.74s,
full racecheck in595.62s; dependencies and the downward import graph passed.
Latest complete-candidate gates refresh before review; those earlier passes
are not latest-base runtime proof. Public reference bodies remain unchanged.

Latest-base tests-13 subsequently passed build, U1–U3 and P0–P9 in both modes,
public smoke, dependencies/import graph, full quickcheck (90.38s) and full
racecheck (410.44s). Tests-14 adds the distinct Network-family native/real
proof required by review1. Fresh complete-candidate quickcheck/smoke and
independent review remain the integration gates.
Tests-13 startup samples were1,026,520/870,124/867,502/845,360/903,488ns/op,
with5159/5152/5152/5152/5152allocs/op: first above1ms, remaining four below.
This is neither an all-five target pass nor an isolated causal comparison.

Five preceding-base startup samples were1,227,197,1,100,162,1,016,517,868,481 and892,106
ns/op; B/op were1,660,193,1,655,595,1,655,897,1,658,712 and1,656,965;
allocs/op were5161,5152,5152,5152 and5152. The first three exceed the<1ms
target; the final two meet it. The historical old-base baseline was
833,213–867,442ns/op, with5158/5152/5152/5152/5152allocs/op.
Because upstream extraction advanced the base and the host was shared, this is
not a clean causal performance comparison; no speedup or harmless regression
is claimed. The semantic audit proves qualification-only code changes, not
performance attribution. No runtime optimization, deadline relaxation or
test suppression was introduced.

Original-base evidence remains historical:1306 migrations,39Go files,
1,075,900 conserved other bindings and ten method callers. Earlier failed
race/quickcheck reports and900-second expiries remain failed observations,
not waived or recast as passes. Earlier post-cutover startup samples were
37,786,093/7,203,948/5,973,393/5,993,069/6,142,934ns/op;
B/op1,680,608/1,667,612/1,646,138/1,648,633/1,650,265 and
allocs/op5380/5153/5151/5151/5151 under observed heavy contention.
Passing preceding-base gates superseded the old blocked readiness state,
not its evidence. Item6 alone is verified; latest complete-candidate gate refresh,
independent review and safe integration remain root-owned lifecycle gates.
The user explicitly waived only post-integration finalcheck, not run or passed.



Run from repository root; use `make check RUN='...'` for individual or small
cohesive selections. Registry shape and claims:

Result-nullability recipe/site units and public consumer coverage:

```sh
make check RUN='^TestResultNullability(Engine|Functions|Identity|CatalogInquiry)$'
make check RUN='^TestReferenceResultNullability(Select|Returning)$'
make check RUN='^TestReferenceResultNullability(Select|Returning)$' REFERENCE=1
make check RUN='^(TestReferenceAccessRoleNullAndMetadata|TestReferenceObjectACL(Metadata|HeldExecution)|TestReferenceBlockingPidsSignatures)$'
make check RUN='^(TestReferenceAccessRoleNullAndMetadata|TestReferenceObjectACL(Metadata|HeldExecution)|TestReferenceBlockingPidsSignatures)$' REFERENCE=1
```

Units independently pin all ACL kinds (including ACLExplode) and both access
recipes without evaluation/context/catalogs. The public matrix checks populated
and empty direct/same-held consumers, preserving native claims versus pq unknown.
See [`reference/dml`](../../../reference/dml/README.md#result-nullability)
for the public contract and standalone metadata smoke.

Written operand hooks and engine raw/bound capture (no public SQL API fixture):

```sh
make check RUN='^TestCaptureOperands(WrittenSlots|RawKinds|SpecialAnnotations|NoAllocations)$'
make check RUN='^TestViewLiteralProtocol(WrittenOperands|OccurrenceOwnership|Annotations|RejectsMismatch|QueryBoundaries|InstalledWrapper)$'
make check RUN='^Test(JSONBSetContextualArrayCapture|JSONBSetRetagRemapAndPresentation|JSONBSetViewReplayDeterminism|JSONBSetViewReplayCastBoundaries|JSONStripNullsBindingStoredPresentation|DatetimeMakeArgumentAssociation)$'
```

The hooks preserve written identity, omitted defaults, variadic boundaries,
annotations and immutability. NoAllocations measures Capture, Check and every At
through a dynamic interface with preboxed fixtures and an escaping descriptor
sink; every migrated fixed-storage family and safe slice/inline/zero controls
must allocate zero times. This is a test requirement, not a measured claim here.
Engine tests pin structural paths, true subquery/function-source boundaries,
remap/retag and installed-wrapper identity. These
supplement the unchanged dual-mode stored-consumer definitions; run the selected
reference families in both native and pinned PostgreSQL modes before review.

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed)$'
```

Aggregate and window registries, settings calls (the engine's aggregate,
window and setting tests bind through the analyzer and cover grouping and
execution; reference tests in both modes):

```sh
make check RUN='^Test(AggregateDefinitionsAreWellFormed|LookupAggregateClaimsRegisteredNames|LookupWindowFunctionClaimsRegisteredNames|MoneySumProvider|IntervalSumProvider)'
make check RUN='^Test(Sum|Avg|Count|Extrema|AnyValue|Boolean|Bitwise|Statistical|StringAggregate|ArrayAggregate|RangeAggregate|JSONObject|SQLJSONObject|OrderedSet|Hypothetical|AggregateModifiers|QueryAggregate|QueryWindow|Setting)'
make check RUN='^TestReference(CountExpression|NumericSum|NumericAvg|Extrema|AnyValue|BooleanAggregate|BitwiseAggregate|StatisticalAggregate|StringAgg|ArrayAgg|RangeAggregate|JSONArrayAggregate|JSONObjectAggregate|SQLJSON|OrderedSet|HypotheticalAggregate|AggregateModifiers|QueryAggregate|QueryWindow|GroupingSets|Settings)'
make check REFERENCE=1 RUN='^TestReference(CountExpression|NumericSum|NumericAvg|Extrema|AnyValue|BooleanAggregate|BitwiseAggregate|StatisticalAggregate|StringAgg|ArrayAgg|RangeAggregate|JSONArrayAggregate|JSONObjectAggregate|SQLJSON|OrderedSet|HypotheticalAggregate|AggregateModifiers|QueryAggregate|QueryWindow|GroupingSets|Settings)'
```

ACL SQL functions (aclitem algorithms, node protocol and evaluation through
a fake `ACLRuntime` here; the engine's `TestACLValue*`, `TestAccessInquiry*`
and `TestObjectACL*` bind through the analyzer; reference tests in both
modes):

```sh
make check RUN='^Test(ACLValues|ACLValue|ACLNodes|ObjectACL|AccessInquiry|PGParameterACL)'
make check RUN='^TestReference(ACLValues|ACLItem|ObjectACL|AccessRole|ObjectGrant(AccessInquiry|ACLGetter|ACLValue|TypeRawACL)|ObjectAddressRoutineFacts)'
make check REFERENCE=1 RUN='^TestReference(ACLValues|ACLItem|ObjectACL|AccessRole|ObjectGrant(AccessInquiry|ACLGetter|ACLValue|TypeRawACL)|ObjectAddressRoutineFacts)'
```

Conditional DEFAULT closure has six focused private engine regressions using
actual bound providers and the real predicates, not interface membership or
callback counts. They cover all scalar argument slots, unselected CASE arms,
unconditional set refusal, dependency guards, stable fallback, selected leaves,
UUID shift/calendar asymmetry, non-Eval/immutability and SQL-routine fallback.
The contextual internal-CHAR slot uses a genuine bound parameter child with an
explicitly inconsistent copied root to isolate recursion from the global guard.

```sh
make check RUN='^TestConditionalDefaultClosure(ACLScalars|ACLExclusion|DependenciesAndFallback|PolicyPrecedence|Inspection|SQLRoutineConsumer)$'
make check RUN='^Test(TransactionTimeExpressionContracts|UUIDGenerationBinderNullAndDemand|UUIDGenerationCapturedNullTyping|DatetimeSleepStored|ClosureTransparencyExcludesGenerators|ACLNodesProtocol|SQLRoutineDefaultClosureBoundary)$'
make check RUN='^TestReferenceACLValues(Controls|Smoke|Lifecycle)$' REFERENCE=1
make check RUN='^TestReferenceACLValues(Controls|Smoke|Lifecycle)$'
make check RUN='^TestReferenceUUIDGeneration(StoredConsumersAndControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceUUIDGeneration(StoredConsumersAndControls|FeatureSmoke)$'
make check RUN='^TestReferenceDatetime(TxTimes(StoredConsumers|NativeReplay|Immutability)|SleepStored)$' REFERENCE=1
make check RUN='^TestReferenceDatetime(TxTimes(StoredConsumers|NativeReplay|Immutability)|SleepStored)$'
```

The unchanged public scenarios independently assert ACL rows/types/empty shapes,
fresh demanded UUIDs and stable shift guards, consuming-statement/wall clocks,
supplied-value bypass, symbolic definitions and actual native Dump/Reset/Restore
with retained rows and future demand. Real mode uses the pinned PostgreSQL 18.6
UTF8/C fixture; native controls do not imply a PostgreSQL control API.

The supplemental public smoke for this adoption is root's corrected temporary
program, run from repository root:

```sh
timeout 900s go run /tmp/stuntgres-conditional-closure-smoke-01a1552f.go
```

It checks direct and same-held prepared inserts/reads, three volatile scalar ACL
defaults, v4/v7 freshness, supplied-value bypass versus demanded invalid-privilege
error, symbolic Dump and actual Reset/Restore retaining rows before fresh inserts.
Expected exit 0 and stdout:
`conditional closure smoke: direct/held/default bypass/render/native replay passed`.
Root observed that smoke before adoption (`tests-1.md`) and all eight selected
baseline checks above (`tests-2.md`) in
`.cswd/parts/PG18-ENGINE-SPLIT-FUNCTIONS-CONDITIONAL-CLOSURE/`.
Post-adoption build, both focused internal selections, selected public proofs in
native and pinned PostgreSQL modes, the actual external smoke and full quickcheck
passed (`tests-3.md`); production dependencies were module/stdlib-only
(`tests-4.md`). The selected definitions and smoke argv above retain that proof,
not a request to rerun them under the user's later verification restriction.
Full racecheck did not pass: attempts timed out, with an interrupted attempt also
retained (`tests-3.md` through `tests-5.md`). The user then instructed, “Please
skip all checks except quickcheck”; remaining racecheck and finalcheck are
explicitly waived, not passing. Only `make quickcheck` is authorized for further
verification in this part. Root's fresh quickcheck passed (`tests-7.md`), and the
independent review completed with PASS and no blocking findings (`review-1.md`).
That review's low stale-handoff prose finding is corrected here; subsequent
rebase/remedy verification is root-owned. Existing public definitions are unchanged.

Node protocol on function nodes:

```sh
make check RUN='^Test(Composite|IntrinsicVolatilityExcludesChildren|PlanningHooks|RetagReportsDerivedInputChanges|StringVariadicAnnotationRoundTrip|Render)'
make check RUN='^Test(GroupIdentity|GroupLeaf|Preflight|SubstituteKeepsTheProvider|ArrayTransformEvaluatesFormalSlots|FunctionSyntax|ClosureTransparency)'
make check RUN='Group|Having|Aggregate|Distinct|Index|Default|Closed'
make check RUN='^TestReference(DeparseConstraints|DeparseRelations|View|Generated|Default|Check)'
make check RUN='^TestReference.*(Dump|Restore|StoredConsumers)'
```

JSON/JSONB function calls (registry; the engine's `TestJSON*` bind through
the analyzer and cover engine switches; reference tests in both modes):

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed|TestJSON|TestJsonb)'
make check RUN='^(TestReference(JSON|SQLJSON)|TestReferenceObjectGrant.*JSON)'
make check REFERENCE=1 RUN='^(TestReference(JSON|SQLJSON)|TestReferenceObjectGrant.*JSON)'
```

JSON operators, SQL/JSON constructors and `IS JSON` (node protocol here; the
reference selection also covers the array and range callers of the shared
operator diagnostics and the stored/deparse paths):

```sh
make check RUN='^Test(SQLJSON|JSONPredicate)Protocol$'
make check RUN='^TestReference(JSON|SQLJSON|ObjectGrant.*JSON|Array|Range(Operator|More)|Enum|Comparison|StringConcat|View|Generated|Default|Check|Deparse)'
make check REFERENCE=1 RUN='^TestReference(JSON|SQLJSON|ObjectGrant.*JSON|Array|Range(Operator|More)|Enum|Comparison|StringConcat|View|Generated|Default|Check|Deparse)'
```

SQLJSON rendering cutover (real bound constructors/coercions, exact native SQL,
ordinary-call identity, immutable children and non-evaluation of a volatile
operand) has a focused private matrix. Only the obsolete FormNone conjunct in
`TestSQLJSONProtocol` was removed; its tree/children/rebuild/volatility checks
remain. No tests moved or were deleted; public reference assertions are unchanged.

```sh
make check RUN='^Test(SQLJSONProtocol|JSONPredicateProtocol|SQLJSONRendererBoundaries|SQLJSONReturningOwnershipRebind)$'
make check RUN='^TestReferenceSQLJSON(Stored|Controls|EmptyObjectControls|AssignmentControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Stored|Controls|EmptyObjectControls|AssignmentControls|FeatureSmoke)$'
make check RUN='^TestReferenceSQLJSON(Format|Returning|OutputEncoding|Diagnostics|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Format|Returning|OutputEncoding|Diagnostics|Metadata)$'
```

The cutover's private matrix and actual public database/sql smoke passed; all
emitted definitions and native Dump bytes matched the pre-cutover fixture,
with independent direct/prepared values, metadata and restored defaults.
Selected pinned18.6 dual-mode checks and full quickcheck also passed. Three full
racecheck attempts hit their 900-second deadlines; a fourth was cancelled when
the user waived all remaining non-quickcheck gates, including post-integration
finalcheck. No full-racecheck or finalcheck pass is claimed.
An earlier documentation-only quickcheck rerun failed in the unchanged
`TestReferenceRegexpActualCancellation` provider-observation witness. The user
explicitly waived that failure for integration; no clean final quickcheck is
claimed and no regexp test or behavior was changed.

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

The six-member `UUIDOsspMembers` manifest supplies installed declaration and
owner-recipe validation. `UUIDGeneration` also hosts the five immutable UUID
constants: literal bytes return before session readiness, entropy or clock
demand. Stored installed recipes retain their procedure/epoch/ACL carrier,
symbolic SQL name and dependencies. `FoldNever` governs the generic UUID
composite route; specialized execution planning may fold an immutable installed
constant only after admitting its original routine. V4 remains volatile.
Selected namespace proof:

```sh
make check RUN='^TestUUIDOsspNamespace(Constants|Construction|ExpressionIdentity|Manifest|Collision)$'
make check RUN='^TestReferenceUUIDOsspNamespace(Values|Bindings|Identity|Diagnostics|Authority|Consumers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceUUIDOsspNamespace(Values|Bindings|Identity|Diagnostics|Authority|Consumers|Controls|FeatureSmoke)$'
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
make check RUN='^TestLevenshtein(Distance|Weights|Bounds|ReleaseInt64|Unicode|Cancellation|NamedArguments)$'
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
per-evaluation allocation. It is registered as one family:
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
