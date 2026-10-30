# internal/engine/functions

Builtin functions: signatures and argument rules, immutable call nodes,
evaluation, aggregate and window providers, and each family's bind code with its
registry entry. The analyzer that drives binding is in
[`analyze`](../analyze/README.md), stored presentation in
[`stored`](../stored/README.md), execution in [`exec`](../exec/README.md), and
the authority behind evaluation capabilities in the root
[engine core](../core/README.md#function-runtimes).

Cooperative execution pauses belong to the detached runtime, not function
binding. Array transformation uses a stable work point; lower typed providers
own their corresponding points. Attached requests are dormant until runtime
admission, and nested binders remove that permission before constant capture.


`EmbeddedVersionBanner` is the pure native product/version identity shared by
`version()` and implementation-information metadata; it retains no runtime or
catalog authority.

`InputValidity` binds the strict two-TEXT `pg_input_is_valid` and
`pg_input_error_info` signatures and ordinary routine provenance. Only error-info
has named inputs (`value`, `type_name`); the boolean declaration is unnamed.
Stable nodes never planning-fold and retain no target or
diagnostic cache. `InputValidityRuntime.CheckTypeInput(value,typeName)` returns
either nil for success, a producer-owned recoverable SQL diagnostic, or a hard
error. The live execution host owns declaration resolution and input environment;
only direct destination conversion is inside the soft boundary.
Dynamic destinations reuse `CaptureTypeDiagnosticInput`; domain CHECK evaluation
borrows that conversion's coherent input environment, including visibility-aware
type names, and restores the caller's environment on every exit. The four-TEXT
record is occurrence-owned: valid input constructs a non-NULL all-NULL-fields
record, whereas strict NULL returns a typed NULL record. `SourceAdmission` and
`OpenSource` use the common scalar-once typed cursor: FROM expands the record
members and emits one row even for a whole-NULL record, without declaring an
SRF. Generic record transport, stored protocols and native replay require no
adapter special case.

`VisibilityInquiry` retains one typed OID operand and the selected descriptor
class. Text-search configuration, dictionary, parser, template and statistics
inquiries share its strict BOOL, STABLE, non-folding protocol. Evaluation borrows
the statement-captured catalog and USAGE-filtered non-TEMP effective path;
same-class namespace/name shadowing never adds object-privilege checks. Prepared
recipes capture a fresh frontier on execution, while retained rows keep their
original one. Views and CHECK expressions reuse structural binding and replay;
the inquiry adds no persistent state.

`EncodingNameCall` binds the strict NAME/INT4 encoding conversions through the
ordinary routine owner and reuses `sqltype.EncodingID`/`EncodingName` after formal
coercion. `Keywords` binds a fixed five-OUT record and streams the parser's sole
vocabulary through an invocation-private inline cursor. Both retain only typed
operands/descriptors, remain STABLE and planning-unfolded, and need no runtime
capability, catalog snapshot or dump serializer. Keyword category uses internal
CHAR; computed source columns retain ordinary unknown nullability.

`Setting` resolves `pg_settings_get_flags` through live statement settings
authority with a typed TEXT[] result, never a retained settings snapshot or
restricted value read. `OptionsToTable` binds one named TEXT[] input and an
occurrence-owned two-TEXT OUT record. Its invocation-private inline source cursor
borrows the immutable array, validates all NULL elements before emitting rows,
then splits only the first equals sign without materializing an output vector.
Both calls remain STABLE, strict and unfolded; routine EXECUTE admission stays
with the existing selected-call owner. Neither adds persistent state.
Options OUT members follow generic computed-source metadata: nullability is
unknown, not a table NOT NULL claim, even when a particular invocation contains
only non-NULL fields. Scalar flag results separately expose known nullable TEXT[].
Stored views containing either expression use the ordinary typed renderer,
dependency capture and native-control replay path, without a builtin serializer.
Their published view attributes expose relation-column nullability independently
of direct computed-source metadata.

`XMLParse` and `XMLSerialize` retain immutable checked operands, explicit modes
and written options. The analyzer owns ordinary source assignment conversions
to TEXT/XML and implicit serializer target conversion, including catalog casts,
domains and typmods. Providers consume checked TEXT/XML and never
planning-fold: execution owns errors and call-local diagnostics.
Explicit modes are independent of xmloption; semantic volatility follows child
and conversion properties. Capture kinds distinguish both written families.
Rebuild/retag follows ordinary conversion wrappers and preserves accepted values.

`ArrayTransform` evaluates its child operands before parent transformation.
`array_to_string` consumes one `WorkArrayTransform` runtime admission grant per
input element, after strict NULL handling and separator/replacement conversion.
An element grant is not a byte bound on its type's output. Refusal publishes no
partial string and retains neither policy nor grant in the immutable expression.

`RecoveryInfo` retains a zero-input immutable typed recipe: BOOL false or typed
PG_LSN/TIMESTAMPTZ NULL in an engine that never performs physical recovery.
These inquiries remain VOLATILE and unfolded. `WALResourceManagers` retains
only its fixed three-member OUT record; metadata binding never borrows a
physical registry. Demanding source opening rejects that unavailable mechanism
with an explicit diagnostic, without synthesizing manager rows or progress.

`WALSummary` retains immutable typed RECORD recipes, written operands and formal
slots for two set sources and one scalar state inquiry. All written operands
evaluate before strict NULL yields an exhausted typed contents source; timeline
validation follows, without an LSN interval rule. Ordinary live routine EXECUTE
admission precedes runtime evaluation. Planning keeps these volatile calls intact;
preflight folds immutable operand work before admission, including errors from
this execution's parameters. Binding and LIMIT 0 expose metadata without physical
demand; demand rejects unavailable embedded physical files or summarizer state
rather than inventing stock PostgreSQL file rows or never-ran progress. Recipes
store no physical authority or state and use ordinary capture and control replay,
preserving named operands and their formal destinations in stored view SQL.

`ControlData` is an immutable, zero-child, volatile and unfolded scalar RECORD
recipe for the four control-file inquiries. Exact PostgreSQL 18 OUT metadata,
routine provenance, reconstruction and capture use the existing typed owners.
Ordinary live EXECUTE admission precedes the explicit unavailable-control-file
error; the recipe retains neither invocation authority nor physical values.

`RuntimeCapability`, `LoadedModules` and `CurrentLogfile` retain immutable typed
recipes, not physical facility state or invocation authority. Without native
JIT, NUMA, a library loader or a collector, they report BOOL false, a typed empty
three-TEXT source, and TEXT NULL respectively. Extension descriptors and settings
do not fabricate facilities. These recipes never fold; logfile overloads are
non-strict, evaluate their optional child once and validate exact format names
before returning collector absence. Ordinary live routine EXECUTE admission owns
authorization, including NULL operands and inherited `pg_monitor` grants.

`SessionActivity` is a zero-child typed recipe with ordinary routine provenance.
Query text is volatile; physical identity and cluster event clocks are stable.
All four remain unfolded in planning and preflight. `SessionActivityRuntime`
borrows the executing command through the live host, including FETCH; no source,
session, clock result or authority is retained in the immutable leaf.

`TriggerDepth` is a stable, unfolded zero-child INT4 recipe with ordinary
routine identity. Its runtime capability reads the executing invocation owner;
reconstruction, stored capture and scalar sources retain no measured depth.

`TriggerBuiltin` is a volatile, nonfoldable zero-child TRIGGER recipe for the
three builtin DML declarations and genuine installed refint, autoinc and
moddatetime declarations.
The installed route first validates the immutable provider symbol and captures
the selected procedure dependency; spelling alone never grants capability.
It retains routine provenance, never activation state. Ordinary SQL evaluation
always reports the function's outside-trigger protocol error with complete ERROR
severity diagnostics; firing belongs to the shared core trigger owner. Rebuild,
rendering, scalar-source and stored capture use ordinary recipes.
Autoinc binding uses installed-provider resolution, not builtin visibility; its
plan carrier retains current validated declaration authority through reconstruction
and replay.
`Tcn` uses the same zero-child protocol with an authenticated installed routine
reference supplied by `InstalledRoutine.Attach`. Generic routine admission,
retagging and current-name rendering retain authority without a separate wrapper.
Its ordinary SQL evaluation always raises the outside-trigger error, including
inside a PL trigger; only core's admitted native body stages notifications.

The same `Deparse` typed path owns the four one-OID routine inquiries:
`pg_get_functiondef`, `pg_get_function_arguments`,
`pg_get_function_identity_arguments` and `pg_get_function_result`. All are strict,
STABLE, unfolded nullable TEXT expressions with ordinary callable resolution,
snapshot demand and stored capture. Identity arguments omit defaults, not OUT
formals; procedure result is NULL. No text or pretty overload is admitted.


`Deparse` also owns `pg_get_triggerdef(oid [, boolean])` and
`pg_get_ruledef(oid [, boolean])`, all returning nullable TEXT. The shared typed
signature resolver analyzes every child before strict NULL evaluation, preserving
ordinary overload/coercion diagnostics. Missing or wrong-class identities return
typed TEXT NULL. These STABLE calls never fold or cache definition text: each
execution borrows the statement catalog, path, quoting and string policies.
Omitted and false pretty selectors agree; true enables visibility-qualified
event targets and reduced expression parentheses without changing stored state.
The existing children/rebuild, routine capture and snapshot-demand protocols
apply to all four signatures.

`SessionEndpoint` retains a zero-child stable typed recipe and ordinary routine
provenance, never socket state or authority. In-process sessions have no transport
endpoints: address leaves return typed INET NULL and port leaves typed INT4 NULL.
Planning and preflight preserve the call for live EXECUTE admission. Scalar source
opening produces one typed NULL row. Stored calls use ordinary symbolic capture,
reconstruction and native dump/replay; there is no endpoint serialization.

`TypeInquiry` binds `format_type`, `pg_basetype` and `pg_typeof` through ordinary
selected routine admission. Stable calls never retain evaluated results;
preflight preserves demanded child work. Logical type identity is separate from
the concrete TEXT transport of raw unknown literals, analyzer-confirmed ordered
pseudo/no-op chains and genuine string-to-UNKNOWN IO. The latter validates one
existing coercion's source/target/operation and outer chain, preserving producer
effects and BPCHAR padding. Wrong-arity names use authenticated logical identity
before concrete usage on both ordinary and selected-provider routes; malformed
authority is an invariant error, not permission to label arbitrary TEXT unknown.
Nodes retain only immutable witnesses and operands, never catalog authority.
Authorized retagging checks every raw-chain target against the descriptor mapper
by identity, including structurally equal inner replacements. True identity
mappings retain accepted marker and chain backing; reconstruction alone cannot
authorize a foreign descriptor generation.
`CatalogInquiryRuntime.FormatType` and `BaseType` borrow captured catalog/path
authority; `pg_typeof` uses its validated logical witness without a catalog read.

`GenericFileList` binds eleven `pg_ls_*` signatures as volatile, strict typed
sources, retaining only a closed kind, immutable operands and result shape.
Ordinary EXECUTE admission precedes runtime NULL handling; all written operands
evaluate before a NULL produces a typed empty cursor. `pg_ls_tmpdir` borrows a
typed OID capability and the statement's owner-validated tablespace snapshot.
Non-NULL calls borrow current statement authority, never host filesystem access.

`LogicalDecoding` owns the four non-strict, volatile logical-slot get/peek typed
sources. Written operands and formal mappings survive capture/reconstruction;
the released empty TEXT[] default belongs to the routine declaration. Every
demanded invocation evaluates all written operands before borrowing live current
role authority through the used `LogicalDecoding` runtime bridge. REPLICATION is
an actor attribute, not an inherited capability. Pure NULL/rank/element/count
checks precede truthful missing-slot errors; no slot, decoder, cursor or authority
is retained. Text and binary output descriptors differ only in their data member.

`GenericFileRead` binds text/binary reads and scalar file metadata with fixed
typed operands and written-to-formal slots. Scalar reads permit strict NULL
planning erasure after required child work; RECORD stat calls remain admitted
before runtime strictness. Scalar sources produce one NULL row, unlike strict
NULL listing sources. Live runtime capabilities apply pure length/path checks
before rejecting the unavailable physical mechanism; no content is synthesized.

`StatisticsIO` binds the released internal statistics input/output/send
declarations through the ordinary signature and coercion owners. Calls are
strict; non-NULL input delegates to the rejecting concrete codec. Output uses
the invocation's presentation environment and send shares frozen serialized
bytes as BYTEA. No collector or public non-NULL construction API is introduced.
Input/output retain immutable declaration volatility; send is STABLE and never
planning-folded. Receive has released STABLE catalog metadata but no callable
INTERNAL-argument provider.

`Deparse` retains a checked signature, immutable operands and selected routine
provenance for the catalog definition inquiries, including the two one-OID
partition inquiries. Calls return known nullable TEXT in SELECT and remain
STABLE and unfolded. Strict NULL eliminates catalog demand without suppressing
operand analysis; non-NULL execution uses ordinary live routine admission.
Core dispatches partition presentation to its owner over the same captured
inquiry catalog. Capture, reconstruction, retagging and stored replay share the
ordinary typed node; no definition text or catalog authority is retained.

`SizeInquiry` binds the nine strict, VOLATILE, scalar BIGINT declarations:
`pg_database_size(name|oid)`, `pg_tablespace_size(name|oid)`,
`pg_relation_size(regclass)` and `(regclass,text)`, and
`pg_table_size(regclass)`, `pg_indexes_size(regclass)`,
`pg_total_relation_size(regclass)`. Unknown database/tablespace arguments select
NAME; relation arguments select REGCLASS through the ordinary typed coercer.
The immutable node retains operands and selected routine provenance, never size
or ACL results. It never planning-folds, evaluates written children before strict
NULL handling, and borrows the existing catalog inquiry runtime for each call.
Results, including NULL and empty-result metadata, remain typed INT8.
The one-argument SQL wrapper's fixed body edge to the two-argument declaration
uses the existing routine-body manifest, lifetime and EXECUTE admission owners.
Its released `SELECT pg_relation_size($1, 'main')` declaration recipe is retained
separately for `prosqlbody`, not represented as a fabricated checked SQL program.

REGCLASS input owns namespace/search-path/quoted-name resolution and early
literal identity within the analyzed expression. DDL invalidation can reanalyze
the original prepared SQL, so an old symbolic literal may then fail after rename;
an existing numeric relation identity does not require the old name. Missing
symbolic relations error on input; missing numeric relation
OIDs return NULL. Existing relations accept exactly main/fsm/vm/init; an invalid
fork raises 22023 `invalid fork name` with the released valid-forks hint, but a
missing numeric relation returns NULL before fork validation. Missing fork
allocations return zero. The body adds no SELECT/ownership/RLS check; ordinary
namespace and routine EXECUTE admission remains active.
Native Reset/Restore retires a held persistent REGCLASS literal with the ordinary
catalog-replaced error; reprepare it. A held REGCLASS parameter resolves its
target in the replacement catalog, and already returned rows retain their values
and metadata across controls.

Database/tablespace objects are resolved before privilege checks, including for
superusers. Missing database NAME raises 3D000; missing database OID and missing
tablespace NAME/OID raise 42704, with the released object-specific diagnostics.
Database access requires CONNECT on the specified target or inherited
pg_read_all_stats privileges. Tablespace access requires CREATE or inherited
pg_read_all_stats, except for the current database's default pg_default
tablespace; changing session default_tablespace does not change the exemption.
Denials use 42501 `permission denied for database NAME` or
`permission denied for tablespace NAME`, without physical-login Detail.
The [storage allocation formula](../storage/README.md#logical-allocation-estimates)
defines every approximate byte component and database/tablespace aggregation;
all lookup, NULL, typing, diagnostic and authority behavior is exact.


Installed `TablefuncCrosstab` nodes retain immutable arguments and the admitted
record or named-composite usage. They are strict, stable, never planning-folded
sources; no SQL, catalog authority, cursor or runtime rows are retained on them.
The statement capability executes the supplied SQL only when the source opens.
An unresolved generic result uses the shared deferred-record carrier for typed
NULL frames; only a declared FROM record supplies its executable tuple shape.
Non-NULL calls without that shape fail when the source opens, not during binding.

`random` and `random_normal` retain their selected declarations and children
through binding. Conditional planning and statement preflight process required
constant children before strict literal-NULL elimination. Required immutable
parameter children use the imported typed frame and current actor; an incomplete
frame defers elimination. Discarded volatile and row-dependent siblings are not
evaluated. Non-NULL calls remain volatile and unfolded. Runtime row NULL still
evaluates the written siblings before testing strictness or borrowing the
session generator.

Installed `normal_rand` binds INT4/FLOAT8/FLOAT8 through the shared coercer.
Its volatile expression evaluates all three operands once before strict NULL
handling. An invocation-private cursor owns only remaining count and transform
parameters; each Gaussian draw briefly borrows the existing session generator.
Raw IEEE multiplication/addition preserves signed and nonfinite deviations;
there is no stream or setseed-replay contract. The installed plan wrapper retains
procedure authority through structural rewrites and stored/native replay.

`Connectby` binds four selected installed signatures through shared TEXT/INT4
coercion. Its immutable expression retains ordered/branch flags and a caller-owned
record usage completed by the analyzer. Arguments evaluate once before strict
NULL handling; non-NULL calls delegate materialized traversal to the statement
host. Rebuilds retain the selected variant and exact anonymous record shape.

`AccessInquiry` uses closed kinds for role membership, row security, routine,
language, wrapper, server, database, schema, type, tablespace, table, sequence,
column, any-column, large-object and parameter privileges. Role NAME/OID,
object TEXT/OID and column TEXT/INT2 axes bind independently to exact selected
declarations; large-object targets have only OID and parameters only TEXT.
Binding validates every typed formal axis before coercing unknown literals, so
an impossible signature cannot trigger an earlier literal's input codec. The four-slot
carrier evaluates written arguments once before strict NULL handling and retains
no authority or decision; its result is nullable BOOL.
`ACLRuntime.ObjectPrivilege` and `RelationPrivilege` borrow the executing
statement's current private catalog and expression principal, including on held
prepared reuse. Existing regprocedure, regtype and regclass resolvers own text
identities with the executing invoker's namespace authority. Independent ordinary
and grant-option masks implement comma-list ANY after complete token validation.
Relation inquiries preserve stable attribute numbers and distinguish absent
objects from false privileges. Inquiry-specific defaults, PUBLIC and raw unknown
role OIDs do not change creation or execution admission.
All thirty relation/column overloads have successful CatalogRoutineProvider
associations in `routine/inventory.go`, with released internal source labels in
`routine/display_inventory.go`. Lookup-only result identities alone never grant
binding or execution authority; the selected descriptor and manifest are required.


## Imports and layering

- A leaf library: it imports only [`expr`](../expr/README.md), `fts`, `ids`, `sqltype`,
  `parser` and `sqlstate`, never the root engine or the catalog, storage, lock
  or visibility packages. It is catalog-free: catalog facts arrive through
  `Binder` or an evaluation runtime, never from a static OID table.
- It reaches the engine only through `Binder` at bind time and through
  `expr.Context` plus the capability interfaces asserted from it at evaluation
  time. `expr.Context` does not grow per family.
  `VisibilityInquiry` carries the typed OID-to-BOOL table, type, collation,
  conversion, function, operator, operator-class and operator-family inquiries.
  Its ordinary expression protocol preserves strict typed NULLs, STABLE
  properties, cost 10, nonfolding, routine provenance and captured-path demand
  through reconstruction and stored replay; evaluation borrows the statement
  inquiry runtime rather than retaining catalog or session authority.
  JSONPath functions, operators and SQL/JSON consumers pass the current captured
  input through `JSONPathEnvironment`. The five path-query carriers retain only
  immutable ordinary-versus-`_tz` provider identity through reconstruction,
  grouping and replay; `_tz` is intrinsically STABLE and obtains the current
  execution's zone rather than caching preparation settings. Precision warnings
  borrow the current statement's `FunctionJSONPathNotice` capability; neither
  plans nor parsed paths retain a session, notice sink or execution scratch.
  Ordinary path operators use the explicit environment-bearing
  `BinaryPlan.EvaluateJSONPath` entry point and retain disabled timezone policy.
- `expr` owns `RoutineReference`, `RoutineRecipeReference`, `RandomOwner`,
  `IntrinsicPropertyRule` and the `RoutineProvider` representation;
  `routine_provider.go` aliases the type (so catalog declarations need not
  import this package) and owns the family constants. There are no other
  aliases.
- Nodes embed `expr.Info`, hold children as `expr.Expr` and evaluate with
  `Eval(expr.Context)`.
- Snapshot calls use the ordinary typed binder and stored-expression protocols.
  Current-snapshot evaluation borrows an immutable command capture through
  `TransactionInquiryRuntime`; it never allocates an XID or refreshes visibility.
  The member SRF declares target-set placement and stage dependence, preserves
  occurrence identity across rewrites, and keeps only its immutable value and
  invocation-private index. NULL and empty member lists exhaust that invocation
  without producing a row.
  Modern and legacy calls retain explicit flavor in the same providers:
  `pg_snapshot`/XID8 versus `txid_snapshot`/INT8. Bigint arguments use ordinary
  implicit widening; raw full-XID bits convert at the typed value boundary.
  Current/assigned/status reuse `TransactionInquiryRuntime` and never retain
  allocator or visibility authority. Reconstruction and semantic identity keep
  the selected nominal signature and SQL name.

`ts_parse` and `ts_token_type` share one fixed-OUT source provider. Their text
and OID formals select ordinary routine authority; text-name resolution borrows
the statement's captured text-search namespace path. Inputs evaluate once in
written order before strict NULL handling. Resolution returns a catalog-free
`fts.ParserRuntime`; document parsing and descriptor enumeration consume its
method-specific native authority, never a default-only post-lookup fallback.
Document tokenization completes privately before a source cursor publishes.
Cursor advancement constructs typed records against its current result
descriptor, so rewritten plans retain no stale composites or invocation state.
Each descriptor cursor owns a pointer to its checked token-type array; advancing
copies only the selected entry, never the complete inventory.

`FTSInspection` owns the four strict fixed-OUT `ts_debug`/`ts_stat` sources,
including written-order named operands and checked record descriptors. Debug is
stable in both forms; statistics is volatile. Neither retains computed rows or
folds at planning time. The statement capability returns invocation-owned records
after complete debug acquisition or nested SQL consumption, including typed empty
sources for strict NULL. Dictionary arrays retain REGDICTIONARY identities.

`FTSConversion` binds the five TEXT constructors and JSON/JSONB vector
conversions, including the filtered JSON names, with up to three checked children.
All forms are strict; explicit REGCONFIG forms are immutable and omitted forms
are stable. Input kind and filter layout survive rebuild, capture, grouping and
qualified replay. The node owns operands and routine authority, never a synthetic
default-config constant. Execution borrows one coherent parser/dictionary
capture; ordinary folding owns immutable explicit results without a conversion
cache or notice replay.

`FTS` owns the four selected `@@` signatures and their actual `ts_match_vq`,
`ts_match_qv`, `ts_match_tt` and `ts_match_tq` routine identities. Typed pairs
are immutable; text pairs are stable and borrow `FTSConversionRuntime` only
after written-order evaluation and strict NULL handling. Documents use vector
conversion, text queries use plain conversion, and omitted configuration stays
live at execution. Rebuild and substitution retain checked descriptor authority,
written operands and operator/function qualification without synthetic
constructor children or retained statement/configuration state.

`VerifyHeapam` holds six canonical typed inputs, including omitted defaults.
Named-input binding and applicability share slot assignment and ordinary
coercion; supplied inputs evaluate once in canonical formal order. The
non-STRICT volatile source delegates to a distinct statement capability after
current installed identity and EXECUTE admission. Every invocation owns its
materialized four-field record set, including the empty set.

## Registry

`registry.go` holds the static `definitions` table; there is no `init()`
registration. `Lookup(name)` claims a call by unqualified name,
case-insensitively unless `ExactSpelling`. The analyzer's `BindFunctionProvider`
(`analyze/function.go`) runs SQL-overload competition, then `Lookup`, then its
remaining name chain, so a registered name must not overlap a family the chain
still dispatches. `Bind` owns exact-spelling resolution, visibility, argument
analysis and every diagnostic.

- `ExactSpelling`: claim only the exact parser-folded names; other spellings
  keep the chain's missing-function path.
- `SharedGates`: the analyzer applies the shared label and spelling gates before
  `Bind`; `OwnsLabels` and `OwnsSpelling` (both require it) leave the label or
  exact-spelling diagnostic to `Bind`.
- `Claims`: a custom admission predicate replacing `Names` matching; registry
  tests check every name in `Names` satisfies it.
- `ProbeCall`, `ResultCall`, `NamedCall`: pure overload-competition hooks.

Bind code lives in `<family>_bind.go` or beside the family's node. Calls reached
by syntax or by the operator binder (SQL value functions, OVERLAPS, SIMILAR,
`||`, JSON operators, SQL/JSON, sequence calls through `BindSequenceCall`)
enter through exported `Bind*`, `Finish*` and `Select*` functions and bypass the
registry. Shared diagnostics: `call_syntax.go` (`ValidateCallSyntax` and
`CallDecorationError`), `resolution_error.go` and `operator_error.go`. Ordinary
selected-call providers analyze written children before the shared FILTER/count/
label envelope, then reject retained scalar decorations after signature
selection and before executable publication. This also applies to math, random,
comment/visibility/deparse inquiries and qualified OVERLAPS; catalog-selected
callable validation does not replace the ordinary provider's responsibility.
`ComparisonError` uses `Binder.DiagnosticTypeName` for both missing and ambiguous
binary operators: visible types are unqualified, while hidden or shadowed types
retain their schema qualification. The shared operator diagnostic owns the hint
and source position independently of type presentation.

Setting calls share fixed-formal typed binding across ordinary queries and routine
bodies. `Setting` retains only its builtin recipe and typed children;
`SettingRuntime` resolves the executing operation's effective store, read grants
and setting installation. Reads remain unfolded STABLE expressions; `set_config`
remains VOLATILE. No session state or actor is captured. Direct setting results
declare known-nullable TEXT through `ResultNullability` at SELECT and RETURNING
sites: strict NULL inputs and missing-ok reads can return NULL, as can a NULL
`set_config` name. This declaration does not infer metadata for enclosing computed
expressions.

`CastRoutine` is the selected executable cast-procedure provider. Its checked
routine declaration supplies signature, namespace, strictness and volatility;
`sqltype.CoercionPlan` supplies conversion and session codec semantics. Typmod
and explicitness arguments remain evaluated operands. Rebuild, capture, grouping
and rendering preserve the selected call rather than reinterpreting it as a
typename shortcut. No procedure becomes callable from type-catalog presence alone.
`HstoreArray` and the shared JSON/JSONB `HstoreJSON` node each retain one selected
installed conversion declaration and one owned typed operand. Reconstruction,
retagging, grouping and qualified replay preserve formal/result generations,
mode and exact member identity.
`LtreeArray` retains two operands in source order, exact formals/results and the
selected installed array-operation role. Shared routine/operator binding,
strict-null preflight and current routine admission apply normally. Immutable
matching indexes belong to typed values; all execution scratch is call-local.
`LtreeHierarchy` applies the same capture and admission contract to scalar
inclusive ancestor/descendant BOOL calls, without retaining execution authority.
`LtreeLquery` applies that contract to both scalar positional-match orientations,
retaining exact ltree/lquery formals and the original selected declaration.
Reconstruction, capture, grouping and qualified replay preserve the selected
member and original declaration rather than dispatching by operator spelling.
`LtreeLtxtquery` retains independently selected mixed ltree/ltxtquery formals
in source order, with the same strict preflight and declaration admission.
Capture, retagging and qualified replay preserve each formal's exact generation
and transport cast; matching scratch remains invocation-local in sqltype.
Shared strict NULL preflight and routine admission use current caller authority;
these nodes do not cache ACLs. Typed evaluation belongs to sqltype, not function spelling.
 `LtreePath` applies that contract to the selected path utilities. Inline eight-slot
 operand/formal storage and an active count retain every written occurrence,
 including repeated LCA arguments, without borrowing producer scratch. Rebuild,
 capture, grouping, qualified transport casts and typed retagging preserve the
 genuine installed overload. Invocation values and scan scratch stay call-local.
 Scalar FROM calls use explicit source admission and the shared scalar-once cursor:
 every open evaluates the selected call once and emits one typed row, including
 SQL NULL. No cursor or evaluation context is retained on the immutable node.
`HstoreRows` retains the selected set-returning declaration and a checked typed
pair projection plan. Shared source admission/lifting owns placement and row
combination; each open evaluates its operand once and owns a separate cursor.
Reconstruction preserves fixed OUT shape, member identity and occurrence.
No cursor, caller authority or intermediate array survives in the expression.
`HstoreConstruct` owns the ordered selected operands, exact formal/result
generations and installed constructor/lookup/key-predicate/containment/projection
or concat/deletion/slice role. Hstore-result calls require the exact selected
base descriptor, not merely its numeric OID. Shared call and
operator selection use the same provider; the operator wrapper owns no second
routine admission. Strict consumers retain literal-NULL preflight while demanded
children evaluate in order; nonstrict constructors preserve present NULL values.
Capture, rebuild and qualified replay retain declaration authority, never actors.
`HstoreRecord` retains the installed record/anyelement declaration separately
from effective operand/result usages. Constructor and replacement are nonstrict;
ordered evaluation borrows current output/input and domain environments.
The ordinary selected-operator wrapper routes `#=` to the same replacement
node without another routine admission. Rebuild, capture, retag and replay
preserve exact tuple generations and original occurrence identity.
Replacement uses the shared scalar source cursor for FROM expansion: every
invocation yields exactly one tuple, including a whole-NULL tuple, never an SRF.
`String` optionally retains a `CitextStringCapture` for a selected installed
scalar. Reconstruction, grouping, rendering and type retagging preserve its
opaque declaration and member reference; explicit formal replay casts retain
overload selection. The shared strict typed-NULL preflight precedes routine
admission, while nonnull calls use current caller permissions. Matching and
translation remain type-owned plans rather than name-based expression dispatch.


## Geometric relation operator provider

`GeometryRelation` consumes one or two already selected/coerced children, preserving
routine authority, immutable volatility, namespace/group identity and cancellation.
Runtime row NULL does not hide a later child error; the existing literal-NULL
shortcut remains. Rebuild rebinds actual operand/result authority. Analyzed
deparsing retains formal display casts; private scalar replay retains separate
formal transports, including NULL. Ordinary view dumps use captured source AST
literals instead, whose owning-type annotations belong to `regobj.ConstantLiteral`.
Both paths preserve overload identity. Capture is operator-only, not function-call
admission. Polygon-point and circle-circle nodes retain their existing ownership;
the callable registry is not expanded by relation routine metadata.
BOX/POLYGON positions use this node as well, including formal typed NULL
operands in persisted expressions. Their predicates do not add expression
variants or change the existing POINT/CIRCLE providers.
LINE/LSEG prefix alignment exposes one active child in evaluation, rebuilding,
group identity and operator capture. Its template rendering retains qualified
operator identity and typed formal transports during replay; binary relations
retain their two-child infix form.
Public execution, ACLs and stored consumers use
[`reference/geometry_network`](../../../reference/geometry_network/README.md#testing).


## Callable competition with SQL overloads

- The analyzer's shared selector ranks builtin and installed providers in the
  same namespace/signature competition as SQL declarations. Builtin metadata
  supplies omitted trailing defaults and variadic expansion; expanded declared
  formals stay the ranking inputs. Polymorphic substitution is never an exact
  concrete match.
- `ProbeCall` (a `CallShape`) and `NamedCall` receive analyzed arguments,
  separate declared-input and resolved-target vectors and a `CallTypeContext`
  with an effect-free `CanCoerce`, and reuse the provider's own pure
  target-selection helpers. No hook binds, infers parameters, converts values,
  captures dependencies, constructs a provider or evaluates, so a losing call
  leaves nothing behind. Unknown operands stay deferred until the winner binds.
- `OriginalArgument` recovers syntax through the private slot carrying an
  already bound operand (`Argument.Raw`), never by matching source keys.
  `selectedCallReentry` is consumed once by the immediate gateway; nested
  winner frames restore it and `SelectedCallArguments`.
- Resolution failure or ambiguity never falls back to a builtin, and a
  common-resolver miss is never retried through a provider.

## Closed source providers

- Source-capable nodes implement `expr.Source` and `SourceOpening` beside their
  family (`*_source.go`); there is no central source registry. Admission is per
  family, never a scalar-FROM fallback.
- Datetime sleep providers admit a scalar-once VOID source using the same
  evaluation, wait, cancellation and authority path as scalar calls.
- Arguments evaluate once per open in native order. Each invocation's state is
  released by close, EOF, error and cancellation without invalidating emitted
  values; set families convert a complete invocation privately before
  publishing a row. `JSONTable` is a composite source carrier whose
  `OpenSource` owns all row state.
- Occurrence hooks keep each family's exact identity, not structural equality.
  Equivalent-child substitution preserves selected providers and never calls
  `Reconstruct`. Query scheduling stays in the engine.

`EventTriggerDroppedObjects` is a stable, nonfoldable twelve-OUT record source.
Binding retains only its selected routine and shape; both source placements open
against the live statement's `EventTriggerDroppedRuntime`. Each opening owns its
typed records and leaves lexical command scratch unconsumed. Missing runtime or
inactive `sql_drop` reports the event-trigger protocol error, not an empty set.
Composite wrapping polls the evaluation context before, during and after
conversion; interruption returns the ordinary context error without a partial
source or consuming lexical records.

`EventTriggerRewrite` is a stable scalar OID/INT4 inquiry with immutable selected
routine provenance, never-fold planning and execution-time lexical lookup.
Scalar FROM uses the same live opening; only the OID declaration supplies the
single OUT label `oid`. Target-list labels remain the function spelling.
Absent active rewrite context reports 39P03, including on prepared Execute.
No target, reason, command pointer or routine frame is retained by the node.

`EventTriggerDDLCommands` uses the same owned source protocol with the selected
4568 declaration and nine exact OUT members. Binding/Prepare retain no event
state. Execute opens the current complete-query inventory, not a phase-gated
drop view; missing state is 39P03 even for an empty result. The ninth
`PG_DDL_COMMAND` member is a non-NULL immutable query datum. Projection and
metadata do not display it; explicit text/binary display rejects with 0A000.


## Binder

`Binder` (`binder.go`) is what bind code may ask of the analyzer;
`analyze.Binder` implements it (`analyze/function_binder.go`). Each method is a
reviewed decision:

- `AnalyzeArgument` / `CoerceArgument` / `CanCoerce`: occurrence-owned
  analysis, formal coercion selected by intent (intents are values in
  `arguments.go`, not methods) and effect-free feasibility for call shapes.
  Setting calls check implicit formal applicability before assignment coercion:
  an incompatible known argument reports the missing function signature rather
  than leaking a coercion implementation error. SQL setting SELECTs use this
  same ordinary expression binder, including aliases and composed children.
- `Type` / `ArrayType` / `Info`: binding-catalog descriptors (identity
  comparisons and `Usage()` need them) and node metadata that records type and
  collation dependencies.
- `BuiltinVisible` / `BuiltinOperatorVisible` / `BuiltinRoutineInfo` /
  `BuiltinRoutineReference` / `BuiltinOperatorRoutineInfo`: whether lookup
  reaches `pg_catalog` under `search_path`, and the selected builtin routine
  identity (catalog state).
- `DiagnosticTypeName` / `SignatureError` / `CallSignatureError`: diagnostic
  spelling and the analyzer's missing-signature routing for composite-field
  recovery. `ResolutionError` constructs the same diagnostic without a note;
  neither path carries replay policy.
- `AnalyzeFilter` / `DefaultConsumer`: a scalar call's FILTER errors precede
  the decoration rejection; whether a column DEFAULT is being bound.
- `CommonUsage` / `ComparisonInfo` / `CollatedInfo` / `RetainUntypedParameter`:
  common type, comparison collation and pending untyped `$n` for ANY formals,
  with the analyzer's diagnostics and dependency recording.
- `NoteCatalogText` / `NoteCatalogSnapshot` / `NoteVisibilitySnapshot`:
  statement-level snapshot demand; none captures eagerly.
- `InstalledRoutine`: an opaque handle to an `Extension`'s installed routine;
  `Attach` returns the engine-authenticated call (installation is engine state).
  `Amcheck` binds the seven installed B-tree scalar signatures through this
  same handle. Its lexical children remain in written order with checked
  written-to-formal slots; the plan wrapper, never the bare kernel, owns
  execution identity and the statement-host invocation.
  Both heap and B-tree binders reuse the existing `AmcheckExtension` family:
  `VerifyHeapam` retains its non-STRICT record-set shape and defaults, while
  `Amcheck` remains STRICT scalar VOID. Provider membership alone does not
  classify every amcheck call as set-returning.
  `Pageinspect` binds the installed BRIN, B-tree, FSM and GIN byte decoders
  through the same handle, preserving written argument order and formal slots.
  Its installed wrapper supports scalar, anonymous-record and set-source results;
  STRICT argument evaluation precedes current-superuser and index-descriptor admission. No
  elevated role or relation/page state is retained in the expression.
  `ValidateArguments` checks the immutable argument types and formal-slot map
  without copying or rebuilding. Actual `Rebuild`/`Substitute` operations share
  that validation and own a fresh child slice.
- `CaptureComparisonInput` / `CaptureTypeInput`: the diagnostic-name, ACL and
  catalog inputs a comparison or type production needs.
- `Literal` / `ConstantValue` / `LiteralNull` / `Frame` / `Context`: the
  engine's literal node (its identity drives folding and stored SQL), the query
  frame retained-fold occurrence keys on, and bind-time cancellation.
- `AdmitSetReturning` / `TargetSet` / `RegexpSetPlacement`: set-returning
  placement and bookkeeping; the regexp form is a leak to unify.
- `CapturePolymorphicBody` / `JSONReturningType` / `ValidateSQLJSONDefault` /
  `SequenceArgument` / `SequenceTemporary`: statement-analysis state for
  SQL-bodied builtins, SQL/JSON RETURNING and DEFAULT shape, and sequence
  regclass input with prepared-literal capture and TEMP provenance.
  Scalar query functions and JSON_TABLE validate analyzed DEFAULT shapes through
  the same hook before RETURNING fallback coercion; relabels and I/O wrappers
  cannot confer function-expression status on a bare parameter.

`AggregateBinder` adds what is valid only while a `parser.AggregateExpr` binds,
where the engine owns grouping state: `AnalyzeAggregateFilter`,
`FinishAggregate` (ORDER BY/DISTINCT, window nesting, registration, occurrence
node), `DomainTypeName`, `CollationName`, `CommonArgumentUsage` and
`CollationRelabel`.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Evaluation capabilities

A family whose evaluation reads subsystem state gets a narrow capability
interface here (`ACLRuntime`, `TransactionInquiryRuntime`,
`CatalogInquiryRuntime`, `SessionInquiryRuntime`, `AdvisoryRuntime`,
`BlockingRuntime`, `SequenceRuntime`, `LargeObjectRuntime`,
`PgrowlocksRuntime`, `TablefuncCrosstabRuntime`, `EventTriggerDroppedRuntime`,
`FTSQueryRuntime`). The node asserts it from `expr.Context`; `*exec.Context`
implements it with a compile-time assertion and forwards to the root statement
host, which owns all locking and returns owned values.

- Nodes pass values (OIDs, names, typed values, descriptors, immutable
  provenance) and retain no Session, Engine, StatementScope, manager, ticket,
  callback or authority cache. A missing runtime or foreign owner is ordinarily
  a Go error; event-trigger inspection instead reports its SQL protocol error.
- Evaluation order is part of each family's contract (error priority and
  reached child effects): which children evaluate, when strict NULL applies and
  when the runtime is first demanded.
- Notification inquiries use `SessionInquiryRuntime` for committed physical
  subscriptions and the cluster queue fraction. The zero-child recipes retain
  neither snapshots nor authority. Listening opens an invocation-owned typed
  vector (including empty TEXT sources); queue usage is volatile and scalar.
  Both are nonfoldable and reuse ordinary callable identities and source gates.
- Publication inquiries use the current protected catalog inquiry snapshot:
  `pg_relation_is_publishable` reads genuine relation eligibility;
  `pg_get_publication_tables` opens an invocation-owned typed membership source.
  Recipes retain typed operands and normal routine identities, never catalog
  authority or expanded membership caches.

## Aggregate and window registries

- `aggregateDefinitions` (`aggregate_registry.go`) is matched exactly by
  `LookupAggregate`; `windowFunctions` (`window_function_bind.go`) by
  `LookupWindowFunction`. Owned aggregates keep their catalog
  declaration; the static table is not authority for a same-spelled owned
  routine.
- `Bind` analyzes arguments, then FILTER, resolves the signature, selects the
  provider and calls `FinishAggregate` with an `AggregateCall`. Providers are
  immutable and keep group state execution-local. Builtin ordered-set and
  hypothetical providers use `OrderedAggregate` with executor-sorted input.
  `ExecutionAggregateProvider` borrows the current `expr.Context` and evaluated
  direct vector; no provider caches execution values. User ordered supports
  stream child-order rows. `AggregateContextSupport` binds genuine selected
  support identities through `AggregateContextRoutineProvider`, owns private
  per-group opaque tuple state and requests sorting only from its actual final.
  Native support state is not an SQL datum or user-name alias to an aggregate.
  Scalar lookup cannot convert ordinary SQL arguments to INTERNAL, including
  unknown NULL. Its signature errors are distinct from the native entrypoint's
  private aggregate-context validation.
  Owned moving inverse/restart capability stays with the executor provider.
- Providers a view may store implement `RemappableProvider`
  (`aggregate_remap.go`); without it view creation fails with "unpersistable
  view aggregate".

## Adding or porting a family

1. In `analyze`, turn `func (b *Binder) bindX(...)` into
   `func bindX(b functions.Binder, ...)`; the compiler proves the interface is
   enough. Record any new `Binder` method above.
2. Move it here, add its `Definition`, and delete its engine branch and glue.
   Unit tests move with it unless they exercise engine switches.
3. A family whose engine branch sat after the chain's shared gates sets
   `SharedGates` (plus `ExactSpelling` if it sat in the final switch,
   `OwnsLabels` if the label gate exempted it), because `Lookup` runs first.
   Check that no earlier chain predicate claims its names.
4. Paths that avoid composite-recovery notes (regexp, string variadics) use
   `ResolutionError` directly, not `SignatureError` or `NotedResolutionError`.
5. An installed extension family resolves through `InstalledRoutine`. Use generic
   routine-reference protocols when sufficient; catalog-dependent execution uses
   an engine wrapper with explicit cases ahead of protocol branches.
6. Capture-admitted nodes implement `expr.Capturable` (fixed-storage nodes the
   value-receiver `expr.IndexedCapturable`, mapping written slots directly with
   checked bounds and no retained slices). Hooks never evaluate, render,
   rebuild, rebind or mutate.
7. Implement the node protocol below and add registry, node and leaf units.

## Node protocol

Every function node implements `expr.Composite` beside its definition (helpers
in `composite.go`): `Children` (lexical children in evaluation order),
`Rebuild`, `Substitute`, `IntrinsicVolatility`, `Render` and `SameGroup`, plus
each optional `expr` hook whose default does not describe it (the `expr` README
defines them).

- `Rebuild` checks the child count and revalidates through `Reconstruct`,
  keeping inputs derived at binding (input collation, retained descriptors,
  text and clock usages). Engine wrappers that embed a node override it to keep
  their routine identity.
- `Substitute` swaps children without rebinding the provider; `SQLJSON` and
  `JSONCreate` rebuild instead.
- `SameGroup` compares the node's own identity (kind, plan, written-slot
  provenance), never presentation.
- `ResultNullability` answers from the immutable recipe only, never evaluating
  arguments or reading authority.
- `Render` returns one `expr.Rendering` built with `render.go`'s builders.
  Extension carrier nodes have no `Render`; their engine wrappers display with
  the installed schema.

## What stays in the engine

The analyzer behind `Binder` and unregistered bind code (`analyze`); name,
catalog, ACL and dependency resolution; descriptor generations and retag/remap;
stored SQL presentation (`stored`); installed-routine wrappers (`plan`) and
manifests (`extension`); query/set expansion, grouping, windows and aggregate
state lifetime (`analyze`, `exec`); session RNG, UUID clocks and SQL time
(`session`); and the runtime bodies behind the capabilities (root
`*_runtime.go`). Catalog and session inquiry owners here hold binding,
properties, grouping and render/replay recipes, which are never mutated once
published; rendering, catalog locks and serial ownership stay in the engine.

### Transaction inquiry ownership and demand

`transaction_identity` and `commit_timestamp` nodes, binders, source operations
and protocol hooks live here; `Substitute` keeps the selected provider without
re-resolution, and no planning hook is inferred from execution strictness.
Display and stored replay share rendering descriptors. Allocation, history,
publication and lock-manager tickets stay in the engine.

## Family notes

- **Dictionary lexization**: `DictionaryLexize` binds REGDICTIONARY/TEXT to
  checked TEXT[] results. Internal runtimes return `fts.LexicalResult`, preserving
  unrecognized versus stopword, ordered spelling, prefix and variant metadata;
  only the SQL boundary projects an array. `SimpleDictionary` owns immutable
  Accept/stopword state and whole-token database-C normalization.
  `english.stop` is the verbatim released PostgreSQL English resource; its
  compiled sorted list is shared, not reread per evaluation. Simple/unaccent,
  Snowball and synonym resource names resolve shipped bytes; missing names report
  the actual embed-relative lookup path. Kernels retain neither catalog identities
  nor expression collation.
  `SnowballDictionary` uses an immutable language-provider table and the complete
  English and Arabic kernels shipped by PostgreSQL REL_18_6 (Snowball revision
  d19326ac6c1b9a417fc872f7c2f845265a5e9ece), with invocation-local scratch.
  Stopwords precede stemming; C-locale ASCII lowering and the original-byte
  1000-byte guard belong to the wrapper. A nonempty token whose kernel result
  is empty retains its lowercased input. Arabic has no stock stopword resource;
  explicitly configured English stopwords share the sorted 127-word resource
  with simple and English Snowball. The full BSD notice is in
  `snowball_LICENSE.txt`; binary
  distributions must reproduce it in their documentation or accompanying materials.
  `SynonymDictionary` compiles the real `synonym_sample.syn` resource into an
  immutable sorted table. Its first two C-whitespace-delimited words per line
  are source and replacement; case-insensitive mode lowers both with C semantics.
  A terminal replacement star becomes rich prefix metadata, not spelling in the
  SQL array or vector. Misses remain unrecognized so the mapping chain continues.
  Compilation, sorting and lookup check cancellation; malformed UTF8 reports the
  actual resource and line before any runtime is published. Results own their
  lexeme slices and share only immutable strings.
  `IspellCompiler` consumes explicit immutable `DictionaryFiles` during detached
  initialization: legacy/MySpell
  affixes, default/long/numeric flags, aliases and conditions become dictionary
  and ordered affix tries. Conditions use simple masks or validated PostgreSQL
  ARE programs; demanded matchers and compound DFS frames belong to each Lexize.
  Duplicate words coalesce raw flag-vector identities before final membership
  decoding: equal original nonalias vectors share an identity, alias slots remain
  distinct even with equal text, and nonempty merges create fresh identities
  (with a comma only in numeric mode). Compilation preserves original-vector
  compound demand, recomputes duplicate compound-only eligibility, and checks
  compound-marked affixes against the ordered original/intermediate vector table.
  That table and its lazy decode cache never enter the immutable runtime.
  Numeric flag, alias and alias-count prefixes follow checked signed64 conversion
  then signed32 narrowing before their distinct caller-owned bounds and demand;
  raw affix rule identifiers remain strings rather than normalized flag numbers.
  Malformed long/numeric dictionary flags remain dormant without compound
  initialization or affix-membership demand; membership found before a malformed
  tail still succeeds. The MySpell first pass reads raw case-sensitive `FLAG`
  prefixes and operands without field-comment handling; empty/default operands
  preserve the current mode, and the final mode governs affix-rule import.
  The second pass recognizes lowercased `AF`/`PFX`/`SFX` command prefixes.
  Alias dispatch precedes short-line rejection: missing first counts reject,
  while subsequent empty alias entries remain valid. Affix rules retain the
  direction and cross-product state of their preceding recognized header.
  Continuation references use the fixed declared alias bound, independently of
  the populated prefix: absent in-range slots reject, initialized empty slots
  remain valid, and the declared one-past slot retains empty semantics.
  Compound commands require raw non-whitespace operands before generic short-line
  handling, while unmarked legacy lines keep their released format-detection and
  ignore behavior. Legacy compound operands retain the complete bounded first
  C-whitespace-delimited field after the single-byte-leading eligibility check.
  `IspellDictionary` retains no input, session, catalog authority or matcher scratch.
  Morphology precedes per-lexeme stop filtering; ordered variants become rich
  lexical results, distinguishing unknown NULL from recognized empty arrays.
  Strict NULL-or-evaluate preflight uses the shared immutable typed-fold owner:
  warmed all-constant calls may reuse their accepted result across dictionary
  option revisions, while runtime-token calls invoke current dictionary state.
  This includes thesaurus calls: accepted all-literal lexemes/vectors retain
  their typed values across normalizer and dictionary option revisions. Fresh
  calls and parameterized invocations independently acquire current state and
  can fail initialization while an older accepted constant remains valid.
  The node retains no dictionary-result cache or statement authority.
  The same embedded resource owner ships the verbatim REL_18_6
  `thesaurus_sample.ths` and project-owned `stuntgres_thes_*.ths` inputs.
  Reference fixture copies remain byte-identical; their provisioning and
  provenance are owned by `reference/fixture/README.md`.
  `ReadThesaurusRules` validates complete physical resource syntax into immutable
  `ThesaurusRules`, independently of option/source validation and normalization.
  `CompileThesaurusDictionary` consumes that parsed input once to construct the
  normalized trie from the catalog-free rich scalar protocol; rejected normalization
  cannot mutate parsed backing. `expr.PhraseMatcher` cursors belong to individual
  invocations; replacements preserve relative positions and prefix/variant metadata
  without SQL descriptors or rendered-array transport. Execution initialization
  belongs to the actual demanded dictionary chain, not configuration capture.
  Sample stopwords use a dedicated key, RHS literal markers bypass normalization,
  and later normalized duplicates replace terminals. Resource-reader errors retain
  physical lines; compilation and matching observe cancellation before publication.
- **Text-search query utilities**: `FTSQuery` binds owner-checked INT4, TEXT
  and TSQUERY results. Inspection and direct rewriting are immutable;
  SQL-based rewriting is intrinsically volatile and only demands
  `FTSQueryRuntime` after strict-NULL and empty-query bypass. Stored nodes keep
  typed operands and routine identity, never SQL rows or a statement host.
- **Text-search vector utilities**: `FTSVector` owns checked immutable scalar
  operations and strict typed operands. The string family's `FinishString`
  routes an already analyzed typed vector `length` operand to this provider;
  unknown and other typed length operands retain their existing owners.
- **Cube spatial** (`cube_spatial.go`, `cube_spatial_bind.go`): `CubeSpatial`
  owns two coerced operands and a checked `sqltype.CubeSpatialPlan`.
  `InstalledCubeRoutine.CubeType` exposes the selected installation's descriptor
  to the shared binder; discovery preserves namespace/signature precedence.
  The `plan` wrapper, not the kernel, owns installed callable admission.

- **ACL**: aclitem algorithms keep their own copy of the `acldefault` masks
  (this package cannot import `authorization`).
- **Regexp**: `RegexpCall` binds a `sqltype.RegexpPlan` and shares the
  statement-local `PGRegexExecution` (no global regex cache). The query binder
  lifts set calls into stages, so `RegexpSet.Eval` is an invariant failure.
  An optional `CitextRegexpCapture` preserves opaque selected-routine identity,
  reference/member provenance and descriptive naming without importing catalog
  ownership. Scalar/set scheduling and kernel hooks stay shared; `analyze`,
  `stored` and `exec` validate actual declarations, generations and current ACLs.
  Captured SQL bodies retain their declaration-default collation independently
  of outer metadata and inline error provenance. After shared constant/parameter
  evaluation declines, eligible nonconstant no-flags scalar bodies retain inline
  diagnostics in projection, WHERE and stored consumers; flags and set wrappers
  retain invocation Context. Diagnostic naming borrows the current validated
  declaration through `CitextRegexpRuntime`, never the kernel symbol or stale
  captured public spelling. Scalar NULL preflight uses the shared typed-frame
  proof and regex reset hook; set initialization still demands routine admission.
  Unchanged replacements borrow the original bytes but construct the bound TEXT
  result descriptor; the input's installed CITEXT identity never becomes a
  TEXT-typed literal's datum identity.
- **pgcrypto**: `sqltype` owns the plans and kernels. Plans retain no password,
  salt, entropy, key or IV.
- **TEXT pattern comparisons** (`text_pattern.go`): `TextPatternComparison`
  owns strict lt/le/ge/gt scalar providers and their symbolic operator entry.
  Rebuild, stored validation and replay retain genuine TEXT-to-BOOL routine
  identity. Its comparison binds C byte ordering independently of expression
  collation, preserving prefix length, case, UTF8 bytes and trailing spaces.
- **MAC operations**: `trunc` keeps one registry owner in `math_bind.go`;
  exact one-argument typed MACADDR/MACADDR8 calls delegate to `MACUnary` using
  the already analyzed child. Numeric/unknown selection stays with the existing
  math owner, and numeric defaults remain excluded. Resolved numeric trunc
  also rejects decorations admitted by the shared ordinary scalar grammar.
  Only `macaddr8_set7bit` adds a registry name. Its function-owned six-to-eight
  expansion retains source829 while selecting sole formal/result774 and
  routine4125, not a general cast. Helpers use genuine NetworkRoutineProvider
  declarations; symbolic MAC operators use OperatorRoutineProvider.
  Rebuild/retag binds fresh source/result descriptors. Stored validation
  checks the selected operation, provider and routine identity; rendering emits
  the original typed source in `pg_catalog.macaddr8_set7bit`, without a
  cross-width CAST. Catalog lifetime/provenance remains with existing admission
  and stored-renderer owners. See [kernels](../../sqltype/README.md#mac-address-operations)
  and [public proof](../../../reference/geometry_network/README.md#mac-address-operations).
- **cube**: `cubeFunctionNames` is the single name inventory for registry dispatch
  and analyzer family recognition; registry folding does not bypass exact
  installed resolution. `bindCube` consumes selected/coerced operands from the
  shared analyzer callable selector; exact/preferred/unknown ranking and type-name
  coercion remain shared. Explicit operation/formal selection includes int4
  coordinate indices, INT4[] subset selectors, FLOAT8 enlargement radii and
  FLOAT8 extraction results.
  `CubeRoutine` exposes the resolved installation's cube descriptor without a
  type-name lookup in `Binder`. `Cube` invokes sqltype's immutable constructor,
  inquiry, coordinate and transformation kernels; strict evaluation finishes
  all children first.
  `plan.InstalledCubeExpr` preserves procedure authority through
  rebuild, retag, substitution, grouping and stored rendering.
- **earthdistance**: `EarthRoutine` exposes the genuine installed domain to
  `bindEarthCoordinate`; shared callable selection owns coercion and shadows.
  `EarthCoordinate` evaluates a captured typed plan, then attaches SQL-body
  diagnostic context only to failures reached inside that plan. On that error
  path, `EarthCoordinateRuntime` resolves the checked current routine name;
  immutable operation selection never follows mutable presentation. Argument
  and coercion errors keep their own origin. Successful calls perform no
  diagnostic-name lookup. `InstalledEarthExpr` retains callable authority
  through reconstruction and catalog replacement.
  `EarthPoint` separately owns two POINT operands and a checked mile-distance
  plan, without domain/cube generations or SQL-body error wrapping.
  `InstalledEarthPointExpr` retains its selected procedure through the same
  reconstruction and lifecycle seams; no callable exists without installation.
- **unaccent**: `unaccent.rules` (PostgreSQL REL_18_0) is embedded and compiled
  once into `StockUnaccent`, used by dictionary providers. Installed
  `unaccent(text)` retains its routine identity, not a captured matcher: the live
  engine resolves literal `unaccent` in the function's current schema and invokes
  that dictionary's actual runtime. It returns the first lexeme or the original
  token for an empty/unrecognized result; strict SQL NULL never demands lookup.
- **dmetaphone licence**: Single Metaphone retains its complete release
  PGDG/Schwern/Philips provenance. The Double Metaphone files retain the complete
  Maurice Aubrey/Text::DoubleMetaphone and North Carolina State Highway Patrol
  notices. The Perl original Artistic alternative and full terms are in
  `dmetaphone_LICENSE.txt`; modified source is Freely Available under clause
  3(a). Binary redistributors should accompany it with machine-readable
  modified source under clause 4(b), or satisfy another applicable license
  condition. This does not relicense the distinct NC permission or imply
  copyright-holder endorsement.
- **Geometry measurements**: `GeometryMeasure` retains fixed operands and selected
  routine/formal/result authority for eighteen signatures, with radius remaining
  in `Circle`. Binding probes every formal without evaluation and coerces only
  the winner. Shared-name `length` stays string-owned and sends analyzed PATH/LSEG
  operands through `FinishGeometryMeasure`, preserving text/binary/unknown
  selection. Matching scalar VARIADIC syntax does not declare a variadic overload;
  scalar decorations are rejected after viable selection. Rebuild, substitution,
  grouping, capture and stored validation retain the selected provider.
  Provider rendering uses the established literal transport; captured query AST
  literals instead pass through `regobj.ConstantLiteral`, whose explicit LSEG and
  POLYGON annotations preserve geometric length and point-count overloads on
  logical replay.

## Testing

Encoding-name and keyword binding, protocol and cursor units use
`RUN='^TestEncodingKeywords'`; public scenarios live in `reference/catalogs`.
Visibility inquiry binding/protocol and composed capture ownership use
`RUN='^Test(VisibilityInquiry|BooleanCall)'`; public scenarios live in
`reference/catalog_objects`.

Session activity leaf and composed authority units use `RUN='^TestSessionActivity'`.
Endpoint leaf ownership units use `RUN='^TestSessionEndpoint'`.
XML expression protocol, immutable conversion authority and call-local diagnostics
use `RUN='^TestXMLExpression'`.

Type inquiry protocol units use `RUN='^TestTypeDescribe(Protocol|ComposedProtocol)$'`;
frozen chain ownership uses `RUN='^TestTypeDescribeComposedStoredOwnership$'`.
Public SQL and metadata scenarios live in `reference/domain_types`.

Leaf units of nodes, kernels, providers and the registry live here. Tests of
builtin functions and aggregates that bind through the real analyzer or run on
a composed engine (binding, retag and remap, routine authority, grouping sets,
series, cancellation and publication) live in `enginetests/`; units of other
engine mechanisms stay with their owner (`analyze`, `exec`). Public SQL
behavior is tested only in `reference/`
([docs/TESTING.md](../../../docs/TESTING.md)).
Datetime sleep source admission and scalar-once lifetime use
`RUN='^TestDatetimeSleepScalarSource$'`, also with `RACE=1`.
Statistics I/O volatility and folding units use `RUN='^TestStatisticsObjectIO'`.
Settings/options declaration and expression protocols use
`RUN='^TestSettingsOptionsBinding$'`; private cursor ownership, typed EOF,
whole-input validation and cancellation use `RUN='^TestOptionsToTableCursor$'`.
Generic file-list binding, lexical paths, source ownership and ACL persistence
use `RUN='^TestGenericFileList'`.
Read/stat/tmpdir binding, scalar strict protocols and runtime ownership use
`RUN='^TestGenericFileRead'`; public SQL scenarios remain in `reference/roles`.
Recovery inquiry carriers, fixed OUT validation and physical source admission use
`RUN='^TestRecoveryInfo'`; public witnesses use `^TestReferenceRecoveryInfo`.
WAL summary declarations, typed shapes, evaluation and owned reconstruction use
`RUN='^TestWALSummary'`; public scenarios live in sessions and roles.
Control-file declaration, shape, cancellation and immutable protocol units use
`RUN='^TestControlData'`; public scenarios live in sessions and roles.
Runtime inquiry binding, immutable protocols and typed source ownership use
`RUN='^TestRuntimeInfo'`; public scenarios live in `reference/extensions`.
Logical-decoding declaration, reconstruction, option and runtime ownership units
live in `core` and use `RUN='^TestLogicalDecoding'`; public shared scenarios live
in `reference/roles`.
Legacy snapshot and transaction-identity protocols, binding and source ownership
use `RUN='^TestLegacy(Snapshot|TransactionIdentity)'`.

Native ordered support state, tuple ownership and cancellation units use
`RUN='^TestAggregateOrderedSupportState'`; public aggregate behavior remains in
`reference/aggregates`.

Live trigger-depth leaf, provenance and executable catalog units use
`RUN='^TestTriggerDepth'`; public SQL scenarios stay in `reference/`.
Builtin DML recipe provenance units use `RUN='^TestTriggerBuiltins'`.
Continuous-source normalization and cancellation belong to core's composed
trigger owner; SQL values, diagnostics and controls stay in `reference/`.
Autoinc installed scalar provenance and reconstruction units use
`RUN='^TestAutoincDirectRecipe$'`.

- Unit fakes embed `expr.Info` (arguments) or the nil `unitCapabilities`
  (contexts), so they implement only what the code under test calls; any other
  call panics.
- Allocation checks construct caller-owned scratch before measuring.
- Frame names matched by tests: `datetimeSleepWaitRemaining`
  (`reference/datetime` sleep cancellation). Renaming or moving it makes that
  observer wait on a runaway query; update the probe with it. The aggregate
  cancellation tests in `enginetests/` match `IntegralBitwiseAggregateProvider`,
  `BitStringAggregateProvider.fold` and `(*HypotheticalSet).PrepareOrdered` and
  `FinalizeOrdered` here, plus `exec` and `sqltype` frames; grep them before
  renaming.

```sh
make check RUN='^Test(LookupClaimsRegisteredNames|DefinitionsAreWellFormed|AggregateDefinitionsAreWellFormed|Lookup(Aggregate|WindowFunction)ClaimsRegisteredNames)$'  # registries
make check RUN='^Test(AggregateProviderRemap|(Sum|Avg|Count|Extrema|AnyValue|MoneySum|IntervalSum|IntervalAvg|StringAggregate|BooleanAggregate|RangeAggregate)Provider|(Bitwise|Statistical|Array)Aggregate)'  # aggregate providers
make check RUN='^Test(Composite(Arity|Counted|Leaf|Named|Providerless|Retained|Slice|String|Timezone)|IntrinsicVolatilityExcludesChildren|PlanningHooks|RetagReportsDerivedInputChanges|GroupIdentity|GroupLeaf|SubstituteKeepsTheProvider|FunctionSyntax|ClosureTransparency|Render|CaptureOperands|ResultNullability)'  # node protocol and hooks
make check RUN='^Test(CatalogInquiry|TransactionInquiry|AdvisoryNode|BlockingInquiry|SequenceCall|DeparsePartition)'  # runtime-capability nodes
make check RUN='^Test(ACLValues|ACLNodes|ACLValueEvaluates|ObjectACLEvaluates|AccessInquiryEvaluates)'  # ACL functions
make check RUN='^Test(FTSOps|Point|Circle|Polygon|GeometryAlignment|GeometryRelation|GeometryConversion|GeometryMeasureBoxLine|MACADDR8Comparison|NetworkSubnet)Provider'  # operator providers
make check RUN='^TestGeometryPositionProviderLifecycle$'  # position provider authority
make check RUN='^TestFTSQueryFunctions'  # query utility providers, kernels and runtime lifetime
make check RUN='^TestFTSJSON'  # JSON conversion operand lifecycle
make check RUN='^TestSimpleDictionary'  # dictionary kernels and shipped resources
make check RUN='^TestSnowball'  # released English/Arabic rules, provider/resources and lifetime ownership
make check RUN='^TestSynonym'  # real synonym compiler, provider and retained publication lifetimes
make check RUN='^TestIspell'  # explicit dictionary readers, conditions, morphology, compounds and cancellation
make check RUN='^TestTablefunc'  # installed Gaussian expression and cursor contracts
make check RUN='^TestGeometryMeasure(GroupIdentity|ProviderLifecycle)$'  # measurement providers
make check RUN='^TestMACOperationsProviderLifecycle$'  # fixed-width MAC function protocol
```

For any other family, select its prefix alone (for example `^TestPgcrypto`,
`^TestUUIDExtract`, `^TestLevenshtein`). Selectors run module-wide, so a family
prefix also selects that family's engine-integrated units.
