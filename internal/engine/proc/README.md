# proc — immutable routine definitions and private values

`proc` is the reusable, lower-level routine owner. It imports parser and scalar
value/type facilities, never engine or catalog. It does not interpret expressions
or own name lookup, permissions, query execution, transactions or prepared caches.

`Definition` retains body form, original quoted text/source position, ordered
statement syntax and ordered formal modes/names/usages/default-presence flags.
`SetFormals` derives input/output projections, inverse input slots and input-only
SQL names/usages. INOUT belongs to both; pure OUT consumes no invocation input.
PL lexical `$n` separately addresses all declared formals, including OUT.
Shared SQL names are projection-aware, allowing pure IN/OUT name reuse; PL's
single formal namespace instead rejects named/positional collisions.
Published syntax/metadata is immutable. Standard RETURN is a SELECT expression;
BEGIN ATOMIC is an ordered SQL program, not a transaction or procedural return.
Checked standard-body and declaration-default recipes live in `stored`, above
this package, never as cached values or catalog dependencies in proc.
`New` validates declaration usages and parses checked quoted SQL with closed
formals. `NewSource` shares usage admission but retains genuine source without
syntax; the runtime owns its first-use parsing. Neither constructor grants
provider, catalog or execution authority.
`NewRoutineDeclarationBody` acquires one closed release-analyzed typed-parts
recipe for every represented builtin/installed SQL-standard declaration. The
same owner retains its private structural AST and records RETURN versus atomic
wrapper and analyzed display parts. An explicit genuine released `Source` keeps
original syntax separate when analyzed display expands stars or qualifies
formals; otherwise source is assembled from the parts. Formal slots, the current
routine-name qualifier, routine/operator identities, types, relation attributes,
catalog-alias literals and installation-member identities remain explicit metadata,
not token replacement or a second per-name SQL output table.
`RoutineDeclarationRecipe` exposes only
immutable part values; `stored.RoutineDeclarationSQL` applies the current
catalog/search path, names and captured quoting/string policy without binding,
execution, target EXECUTE checks or executable provider authority.
Installation-member slots resolve through sealed producer `Installation` and
`InstalledOrigin` metadata, not the mutable current extension membership order.
Current reciprocal membership is checked separately; legal detachment does not
erase declaration identity. Earth's cube references follow the actual domain
base-type producer, never a schema/name lookup for a replacement installation.
`parser.AppendStructuralSyntax` records genuine concrete parser node/field names and
ordered children/literals; source positions are not structural identity.
Acquisition uses `parser.ParseRoutineDeclarationBody`, the ordinary lexer/grammar
with readonly syntax capture separate from selected-call execution admission.
Output remains native `pg_node_tree`, not PostgreSQL `nodeToString`.
For user declarations, `parser.CreateFunction.StandardReturn` propagates to
`Definition.StandardReturn`, retaining top-level RETURN versus BEGIN ATOMIC;
`parser.Select.RoutineReturn` separately retains RETURN statements inside an
atomic body. Native dump preserves these forms and Restore reconstructs them.

Declaration usages admit pseudo signature identities separately from executable
values. Catalog result-safety checks are unconditional; language admission follows
DDL replacement authority and compatibility. `ValidatePLSignature` owns PL
pseudotype and trigger/event formal restrictions for genuine validators and
reached compilation. Stored source-only PL definitions retain catalog safety
without claiming language validation; checked PL programs and SQL declarations
retain their language signature checks without executing a body.
`InvocationSignature` retains concrete input/result usages on checked calls.
`ValidFor` checks the declaration's family relationships and every derived output
member's label, ordinal and complete usage, including descriptor generations.
Array occurrences within each solved family retain the exact array descriptor,
not merely a shared element. Range and multirange occurrences retain reciprocal
companion descriptors and the exact subtype descriptor used by scalar members.
`ResolveResult` applies the caller's input-family solution to those output members;
outputs never supply independent type inference. Derived OUT members project that
solution without applying actual-input or scalar-result enum/nonarray restrictions.
`Definition.Specialize` projects concrete inputs, result and formals into an
invocation-private definition. Each derived OUT formal uses its corresponding
tuple member, including a procedure's sole output; scalar function policy is
unchanged. It shares immutable syntax, leaves published declarations untouched,
and returns the original definition when no usage changes.
PL polymorphic result `$0` is a private NULL-initialized slot only when there are
no OUT placeholders. Aliases share that result slot; invocation-specialized
formals and transitive `%TYPE` copies share immutable syntax, not mutable
runtime types. Dynamic RECORD formals expose their current tuple descriptor
to SQL parameter analysis and transport; named-composite NULL retains its
shape, while generic RECORD NULL remains indeterminate.

`Definition.Invocation` and `PLProgram.Invocation` explicitly distinguish
ordinary routines, DML triggers and event triggers; none is inferred from a
single trigger boolean. PL DML-trigger definitions have zero formals.
Their fixed OLD/NEW and TG lexical slots are separate from SQL parameters;
invocations supply explicit timing/level, admitted table or logical-view row shapes,
typed absent records and owned zero-based text-array arguments. Row-trigger returns retain
strict table-row validation even when their AFTER result is ignored. Statement
returns still evaluate the expression and require RETURN, but skip table-row
shape conversion. BEFORE STATEMENT composite returns cannot suppress or replace
rows; scalar and expression-evaluation errors remain errors. Neither timing
discards execution errors.
RETURN expression failures retain the expression producer's SPI/planning callback
even for triggers; scalar return conversion failures retain only the RETURN frame.
Engine-created scalar TG slots resolve fixed `pg_catalog` types regardless of
search_path or shadow types; user-written declarations keep ordinary lookup.
The twelve canonical OLD/NEW/TG slots retain their fixed identities. One separate
BOOL FOUND follows that prefix, rather than a formal slot or a replacement TG
datum. It is invocation-private and exposed through the same function-scope
overlay as ordinary routines; the canonical trigger binding map contains only
its twelve names. Validation reconstructs both layouts exactly, and layout
equivalence retains the trigger contract as well as the FOUND owner.
Trigger assignment diagnostics retain binding-versus-execution provenance:
binding failures identify the complete assignment, while an executed arithmetic
failure retains its causal error and PL statement frame.

Event functions use a separate zero-formal contract with only `TG_EVENT` and
`TG_TAG` as injected `pg_catalog.text` datums, followed by ordinary private FOUND
and declared locals. `NewPLEventTriggerFrame` owns fresh values; `EventTrigger`
has void-like bare RETURN/fallthrough semantics, never scalar pseudotype or
DML composite-row return coercion. Both compilation and checked-layout validation
allow bare RETURN for the explicit event invocation kind, while retaining the
expression-return prohibition and ordinary nonvoid return requirements.
Compiler/provider/layout validation retains the invocation kind through copying,
definition replacement and remapping.

Checked parsing closes positional formals even after an unresolved call stops
semantic binding. Source-only quoted SQL/PL retains original text without claiming
syntax validation. Private Restore preserves that state and reconstructs quoted
declarations without catalog-dependent SQL analysis or result checking; reached
invocation owns those checks. Persistent declaration structure, checked standard
programs and hard defaults remain validated. Quoted strings/comments are not
formals, and unused declared inputs are valid.

The shared binder closes positional formals even with zero inputs. Named,
routine-qualified and `$n` references use the same typed slots; real columns
and genuine column ambiguities take precedence over formal fallback. Checked
CREATE inspects all commands without producers. Quoted bodies are prepared
sequentially at invocation, so an earlier command can run before a later reanalysis error.
Standard commands refresh checked recipes by identity, never render/reparse or
reselect source names/overloads at invocation.

Only the actual nonvoid terminal SELECT or DML RETURNING supplies results.
`ResultPlan` validates scalar/whole-value or positional member Assignment
conversions before producers, including empty inputs. `OutputTuple` derives an
immutable ordered record from OUT/INOUT/TABLE formals, synthesizing unnamed output
labels by output ordinal. `RecordSignatureShape` admits canonical polymorphic
declaration members without granting value or storage authority; specialization
builds an ordinary concrete `RecordShape`. `MatchesOutputTuple` checks exact
formal provenance. Function policy collapses one output; procedure policy keeps
a declaration-owned RECORD for any output, and VOID only without outputs.
`PlanTuple` preserves even one composite output as a tuple member. Derived records
have no catalog storage identity; their children retain exact type generations
and usages. VOID discards body output and yields typed SQL NULL without a CALL
row descriptor.
Result mismatch rendering accepts an admitted type-name capability without
retaining it in the plan; named types follow the captured search path, while
anonymous records keep their SQL diagnostic identity.
`PlanNamedResult` derives a scalar RECORD's actual tuple from the named terminal
schema and uses the same `ResultPlan.Apply` conversion as concrete output tuples.
One composite expression travels whole; scalar columns, including a single scalar,
pack into one owned record with ordered names and usages, without inventing a
catalog identity.
Scalar calls take the first row, while set calls retain every row; VOID never
reuses an earlier result.

Each invocation owns an input-sized `Frame`. Supplied expressions retain written
order; a validated plan assembly maps them and hidden selected defaults into
private slots exactly once. Expanded variadic elements transfer one owned array;
explicit arrays are not unpacked/copied. Declaration defaults use Assignment
conversion and fresh evaluation; actuals use Implicit conversion. Nested/repeated
calls cannot overwrite caller parameters. Scalar results use Assignment conversion.
Scalar/tree `sqltype.Value` payloads are immutable: a returned value survives frame
closure without an unnecessary payload copy. Driver mutable-byte export stays
with the type/driver owner. Closing a frame clears slots and result and is
idempotent; no closed frame admits a value.

Engine retains one session-private quoted-body analysis specialization, keyed by
the declaration, live name, input collation and complete concrete invocation
signature, including descriptor generations and record shapes. It checks live
EXECUTE/invoker authority and data. Eligible non-set terminal SELECT
uses first-row demand; nonterminal/VOID SELECT, set results and every DML command
run fully. Required sort/aggregate/auxiliary work remains query/DML-owned.
No scalar row yields whole typed NULL; a present all-NULL tuple remains present.
Standalone CALL rejects an absent SQL output record with XX000, rather than
fabricating a row of NULL members. PL procedures and anonymous multi-OUT
functions export final output slots into their declaration-owned tuple before
closing the lexical frame, on bare RETURN or fallthrough. A function's sole OUT
remains scalar; a procedure preserves even one OUT as a tuple. The no-output
procedure's private handler result remains empty.
Compiled host procedures reuse this exact sole-output tuple and `PlanTuple`
conversion. Their real Program returns the scalar member; core wraps that
converted value into the declaration-owned record. NULL remains a member in a
present tuple, never an alternate values side channel or a fabricated CALL row.
Set declarations retain every row and yield zero values when empty. Completed
set values belong to the existing owned source protocol, not a suspended frame
or statement scope. Persistence retains TABLE/SETOF and ordered formal modes,
quoted source or checked standard bodies, never invocation resources.

Setting SELECTs parse directly as ordinary function expressions, before routine
declaration analysis or immutable standard-body capture. Quoted first-use
parsing uses that same grammar, including aliases and composed children; no
standalone setting statement or normalization shim exists. The setting runtime
and live operation authority are never retained in the definition.

Invocation borrows the same admitted engine statement scope. A balanced lexical
activation guard saves/restores query, parameter, view and protocol scratch;
the root stage, effects, completion queues, transaction and live reservation
ownership stay with their owners. A callee never calls root Finish.
Reached transaction instructions delegate to the host's segment rotation;
lexical frames and immutable checked programs survive the new incarnation.

The root statement scope also owns a balanced maximum of 128 active SQL bodies,
shared by direct and mutual calls, independent of `max_stack_depth`. Excess calls
return SQLSTATE 54001, `stack depth limit exceeded`; successful, failed and
canceled calls all unwind. Result usage/coercion plans are invocation scratch,
never retained executable plans in the session's analysis cells.

Catalog procedure descriptors hold `*Definition`, not a second executable
registry. Namespace/signature selection, the catalog's derived COW name index,
prepared-analysis retention and stored-call identity capture remain above proc.
Frozen engine syntax marks selected SQL calls only after binding; that marker
cannot authorize a call or replace its checked occurrence reference.

PL definitions retain a distinct body kind, complete positioned syntax and a
checked immutable `PLProgram`. Fixed slots identify all formals, implicit FOUND,
locals, cursor arguments, loop RECORDs and handler-local constant TEXT intrinsics;
aliases share their original constrained slot. Initializer and bound-query
namespaces retain declaration-order visibility. Ordered handler conditions and
bodies are checked against positioned syntax, including unreachable branches.
The released error-condition inventory preserves aliases. Every initializer,
body and handler expression query must match the compiler's complete synthetic
single-target SELECT, including its parser expression and position; extra query
clauses are not a checked expression program.
RAISE arguments and ordered option occurrences each own an exact single-target
query; ASSERT owns its condition and optional message queries. Compilation checks
decoded format arity and direct conditions without binding operands. Construction
selects the first released condition alias, while handlers retain every alias.
Validation checks all variant exclusions, source spans and operand order and
reconstructs the diagnostic ledger for both message statements. Runtime assertion
settings and rendered diagnostics never enter a published program.
GET diagnostics retains only checked lexical destinations and item identities.
Validation binds the parser-admitted GET interval/text and semicolon to the owning
definition source before reconstructing ordered associations and the destination-field
datum ledger, including unreachable handlers. Runtime values never enter that payload.
Bound cursor actuals are the explicit row-projection exception: one ordinary
multi-target SELECT matches the complete declaration-ordered expression list and
optional formal aliases, without destination coercions. Each destination retains
its original parsed span; no per-argument executable query is kept.
Successful argument-query evaluation publishes invocation-private FOUND before
formal conversion; query failure does not publish it. Declaration CONSTANT
metadata remains intact for the runtime's late generated-name assignment check.
Cursor operand admission does not ban constants: a non-NULL name needs no write,
and declaration initialization retains its existing conversion contract.
Checked targets retain one root slot and ordered parser field/subscript steps;
validation checks their exact lexical and syntax association. Binding and bound
values belong only to the reached invocation, never to the checked program.
Cursor operands require the actual REFCURSOR descriptor identity, not a domain's
base identity, in both compilation and checked-program validation. Domains remain
valid ordinary values and FETCH destinations through the shared conversion owner.
Cursor recipes contain syntax and slot references only: every typed Usage stays
in Variables for provider validation and replacement. `PLFrame` owns values/scopes,
processed-row count and the active handled diagnostic, restoring the previous
diagnostic on nested handler exit. Closing it clears private status and references
without mutating the program, caller arguments, returned values or owned portals.
Replacement preserves complete local Usages, including typmods and once-mapped collation capabilities.
Private effective Usages inherit checked call collation for formals/ordinary
locals and their lexical `%TYPE` copies; column `%TYPE` stays fixed to its source.
Known rows retain typed whole NULL until materialized; RECORD begins indeterminate
and NULL assignment clears its shape. Composite-domain candidates validate before publication.
Only whole composite-to-row movement is ordinal; destination members and partial
leaves use assignment conversion, with textual IO only when no cast path exists.
The semantic assignment datum is the leading record field when present, otherwise
the slot. Query projections reconstruct that datum per output row; empty results
retain its typed NULL identity, while multiple rows never publish. Field ancestors
are reconstructed once after cardinality admission, preserving their siblings.
Partial query assignments enter that reached projection. FETCH instead supplies
an already detached typed value through the same semantic-target binding,
selected conversion and ancestor publisher. Bounds precede leaf conversion;
scalar lists stage every candidate, including repeated roots, before publication.
The higher root owns reached SQL binding, conversion, handler admission and
execution. PL VOID uses non-NULL `VoidValue`; procedure completion is separate.
PL nesting borrows the same balanced SQL-body/root guard, not a second counter.
Conditional statement lists reuse their enclosing lexical scopes; BEGIN owns
declarations. Loop scopes retain label identity and any loop-owned private slots.
Immutable acyclic control tokens identify each executable block/loop and the
hidden function namespace; transfers retain the exact enclosing target token.
Validation reconstructs owner order, kind, ancestry and condition queries, including
positioned projection aliases. WHILE owns one immutable condition query and an
ordinary loop body, with no additional variable/search slot. Its condition's
compound names precede the body's names in the diagnostic ledger. No invocation
flow or evaluated condition value persists.
Static query-loop instructions resolve existing whole RECORD/composite slots in
the enclosing namespace, without cursor recipes, implicit records or extra Usages.
Their ordinary statement syntax, source spans, target and loop body are checked
together. Query and body compound names participate in the diagnostic ledger in
lexical order without a cursor-private ROW datum.
Integer FOR compiles its target-derived private name and operand queries in the
outer namespace before publishing an unconstrained INT4 slot on the labelled body.
The single typed authority is Variables; qualified and parameter-shaped targets
name new datums, not field/formal writes. The walker evaluates and converts bounds
and optional positive step once per entry, uses independent overflow-safe machine
progression, and clears the counter on every exit. Context fences include empty
and CONTINUE-only bodies. Successful completion publishes intrinsic FOUND;
errors preserve its previous value. Validation reconstructs slot, query, scope,
source and control ownership; replacement reuses generic Variables remapping.
Comma-list targets use existing outer lexical datums and assignability first,
with declaration-sensitive unknown/constant/whole-row diagnostics in written
order, before integer-FOR's single-target rejection. A single target never
inherits those destination constraints. Only exactly one target can appear in
an accepted checked program; parser comma metadata remains syntax-only.
FOREACH instructions resolve ordered existing targets in the enclosing scope
and retain one source query, slice syntax and a loop owner, with no extra typed
captures. Compiler and validator share FOREACH-only admission: no subscript
targets or whole RECORD/named-row members in scalar lists. An actual lexical
datum can shadow the unreserved SLICE clause; labels and record-field names
alone do not. Separator/keyword positions retain admission diagnostics.
The same signed-32-bit SLICE invariant guards parser admission and recompared
raw/checked instructions, including coordinated metadata mutations.
The walk evaluates the array once, traverses immutable flat elements
or trailing slices and preserves the target on empty input. Exact descriptor
arrayness precedes per-item assignment. Lists unpack composite fields ordinally
and publish each destination sequentially; repeated roots see earlier writes.
Whole targets use normal slot conversion; partial values reuse the host's
semantic-target binder and ancestor publisher. FOREACH never uses FETCH staging.
Static capture instructions retain only ordinary query syntax, resolved ordered
targets and STRICT. Scalar/field lists add one diagnostic-only ROW after target
lexing, including singleton lists; whole RECORD/named rows add none. Validation
reconstructs lexical query/target accounting, source boundaries and target
legality, including exact source-derived INTO redaction: every byte in the
procedural interval becomes a space, including CR/LF; outside spelling and
original UTF8 coordinates remain authoritative. A suffix INTO has no post-target
SQL span to visit. Nonempty spans still use the strict bounded-token visitor.
No capture evaluator or runtime
shape is persisted.
Static command/PERFORM/CALL instructions share ordinary SQL syntax, but retain
distinct checked result policies. Validation checks exact raw/checked association
and the parser-owned static-category/source admission, as well as variant
exclusions, original spans and the diagnostic-datum ledger, including unreachable
SQL and CALL actuals. Agreement between raw and checked pointers alone cannot
admit a replacement query. No selected procedure, output association,
frame or executable graph enters these immutable recipes. `PLHost.Command`
owns reached binding, execution, discard/destination checks and CALL copyback.
Dynamic instructions retain command/USING syntax queries and checked INTO
slots only. Validation includes parser-owned admission and reconstructs the
diagnostic operand/target ledger in source order, including USING before INTO.
Computed SQL, parameter values and tuple descriptors are invocation-owned.
Checked ordered branches cache syntax queries, never bound
evaluators. Simple CASE search slots are separate invocation-private Value/Usage
pairs, not declarations, formals or persisted type dependencies. Their canonical
private names cannot occur in SQL source. Provider validation checks exact query,
target, branch and private-slot ownership before publication.
Postorder diagnostic datum names are independent of preorder invocation indices.
Compilation/validation scratch accounts for declarations, completed CASE datums
and deduplicated record-field names by lexical parent. Expression occurrences
come from bounded original tokens, including qualified callees, rather than an
SQL AST-node catalogue. Field existence is not resolved, SQL names are not bound,
and no additional executable slots or runtime name cache are published.
Assignment targets use the same scanner over original statement start through
RHS start, including the assignment operator but excluding the RHS. Recursive
field/subscript paths and index expressions need no target-node exceptions.
Diagnostic parent lookup searches one lexical level at a time: an eligible
unqualified record precedes label qualification at that level. Scalar matches
with a remaining field component are skipped. A two-part qualified match stops
the search without adding a field datum; ordinary runtime lookup is unchanged.
Each level uses the shared canonical lookup, including the function's implicit
FOUND overlay, before applying component-sensitive record eligibility. The
diagnostic CASE ledger accounts for that single real FOUND slot, not a duplicate.
The same compilation/validation scratch ledger includes one parser-private ROW
for a nonempty cursor formal list before bound-query lexing, and one ROW after
each FETCH scalar target list (including singleton/field targets). Whole RECORD,
named composite and composite-domain destinations add no FETCH ROW. These are
diagnostic counts only, never Variables, Usages or executable search indices.
Cursor query/command/USING/count/target spans use the shared original-token name
visitor; actuals are visited in written order in their caller scope. Bound queries
use the argument/declaration-visible scope before the cursor name is published.
FOR actuals precede the new loop RECORD, so outer and loop fields keep distinct
parent identities; repeated fields/aliases share the existing deduplication set.
Provider validation reconstructs this ledger and rejects coherently forged display
names, queries and rendered-coordinate metadata without trusting a stored counter.
`ValidateDefinition` checks cursor recipe/argument/target associations, implicit
slot roles and lexical scopes as well as the exact all-formal namespace.
Canonical ordinary-routine root bindings retain only numeric/named formal slots;
DML-trigger roots retain their OLD/NEW/TG namespace; event-trigger roots retain
their separate two-TEXT namespace. The separate
function-scope FOUND overlay shadows a formal named `found` for bare and
function-qualified lookup, after inner locals; `$n` and OUT/INOUT return slots
still address the original formal. Alias and `%TYPE` lookup use the same overlay.
`SameLayout` compares the complete semantic layout independently of mapped Usages.
Bound actuals map first into one uncoerced projection and evaluate once in
declaration order before formal conversion; dynamic USING retains written order.
Local declaration COLLATE retains positioned syntax and resolves after ordinary
or copied `%TYPE` Usage through a mandatory collation resolver. It preserves
type/typmod, fixes the local collation instead of inheriting invocation input,
and remains part of checked Variables Usage for provider validation and remapping.
Cursor argument syntax does not gain a COLLATE clause.


PL runtime diagnostics live here as value code (`plpgsql_runtime_errors.go`).
`PLFormatError` is the single formatter; `PLRuntimeError`, `PLRuntimeQueryError`
and `PLAssignmentError` add the PL action line and take the routine identity as
a callback, called only when a SQL diagnostic is formatted (the root renders it
with `regobj`). Phase markers (`PLExpressionBindingError`, `...PlanningError`,
`...ExecutionError` with `Simple`, `PLCardinalityError`) record the producer
phase; `PLExceptionCleanupError` is fatal to handler matching. Assignment phases
from `plan` are matched by marker methods (`PLAssignmentDomain`,
`PLAssignmentPlanning`, `PLAssignmentProjection`), so proc never imports `plan`.
`PLDatumAccessError` marks lexical-datum access during binding without selecting
a universal callback policy. Ordinary expressions retain their expression frame;
the assignment owner omits its assignment-query callback for a current binding's
datum-access failure. Outer execution/planning routes retain their own provenance.
PL and SQL context decoration uses `sqlstate.WithDiagnostic`: the decorated
primary diagnostic is visible through `errors.As`, while the original cause,
fatal cleanup classification and secondary failure remain traversable across calls.
Simple CASE, cursor and assignment query/position mapping share one formatter
and classification-preserving seam. Actual current-owner planning/simple/SPI
markers survive scope restoration; an inner callee's simple route cannot suppress
an outer ordinary query callback. Simple direct execution omits that callback.
Diagnostic requests retain exact parsed spans or a synthesized query with explicit
character-coordinate mapping. The original positive Position chooses translation
before any rewrite; otherwise eligible expression, SQL or assignment callbacks
append their frame even when an inner diagnostic already has Context/InternalQuery.
The PL action line comes independently from the statement keyword.
`PLFlow` carries RETURN/EXIT/CONTINUE as a non-error value addressed to a
`PLControl` token. `NewPLTriggerFrame` builds a trigger invocation frame from a
caller-resolved `PLTriggerRelation`; `(*PLFrame).CursorName` reads a cursor slot.

## Statement walk

`PLRun{Host, Definition, Frame, Values}` walks one invocation of a checked program
(`plpgsql_walk*.go`). Entry points: `Invoke` (fresh frame, result selection and
the missing-RETURN diagnostic), `Trigger` (caller's DML frame, then
`TriggerReturn` against the current row type), `EventTrigger` (caller's event
frame and void-like exit), `Block` and `Statements`.
`AssignVariable`, `PrepareVariable` and `AssignRow` are exported for the host's
cursor and target code. The walk holds no lock; element traversal allocates no
enumerator or source copy. Published record rows and proper trailing slices own
their detached buffers.

`PLHost` is the walk's only capability; every method takes values and runs with
nothing held. `Evaluate`, `Convert`, `AssignTarget` and `AssignValue` execute SQL
or selected conversion and finalize notices emitted during that call with the
`PLOrigin` statement action. `TargetUsage` resolves reached partial destinations
without evaluating bounds. A zero origin adds no notice context.
`Identity` and catalog-visible `TypeName` are called only on error paths. `BeginException`
and `EndException` bracket a block with handlers around an opaque `PLCheckpoint`;
initializers run before it, and a `PLExceptionCleanupError` or a non-SQL error
(cancellation) skips handler matching. `OpenQuery` returns `PLRows` with errors
already formatted; the walk calls `PLRows.Close` exactly once with its primary
error and publishes FOUND only if that returns nil. `Cursor` and `Message` run
cursor statements and RAISE/ASSERT.
`Transaction` executes only a checked COMMIT/ROLLBACK payload. The host owns
context restrictions, pinned-loop holding and publication/restart; the walk
retains no transaction permission or transaction-owned producer in its program.

`Capture` owns reached static SQL and publishes FOUND before cardinality and
target conversion. Lists publish sequentially from one captured source row;
whole rows use `AssignRow`, with producer-shaped NULL fields for no rows.
`Dynamic` owns reached command evaluation, typed USING binding and complete
result consumption before ordinary INTO cardinality/assignment, preserving
FOUND except for explicit assignment to it. Dynamic RETURN QUERY appends to the
existing invocation-private `Values` sink and then continues the walk; bare
RETURN and fallthrough finish set invocations without a scalar result.

RETURN/EXIT/CONTINUE use compact non-error flow, so successful protected
transfers release checkpoints rather than invoking handlers. Blocks and loops
consume only their own transfers. LOOP and WHILE share one scope-balanced walker,
checking `Host.Context()` at each head even for an empty body. WHILE evaluates
its retained query against the current frame and uses host Boolean assignment
conversion; NULL/false ends the loop, own CONTINUE returns to the head, and
EXIT/RETURN/outward flow does not evaluate another condition. Failures retain
their causal expression/conversion phase and WHILE source origin.
`executePLLoop` keeps its name because a reference cancellation fixture waits
for that frame. The hidden function namespace is not a block-exit
consumer; an escaping EXIT reaches the routine's missing-RETURN diagnostic.
Query loops use one width-sized demand buffer. Anonymous records are detached
per row; named rows use ordinal assignment conversion. An empty first demand
assigns descriptor-shaped NULLs, targets survive loop transfer, body and error
diagnostics keep their own action, and source failures use the FOR action alone.

## Testing

Package units cover frames, parameter layout and genuine result-schema/tuple
adaptation (`RUN='^TestRoutine(Parameter|Result|Procedure)'`). Engine-level
invocation units live in `internal/engine/core`. Public SQL routines live in
[reference/query_core](../../../reference/query_core/README.md#testing) (bodies),
[reference/operators](../../../reference/operators/README.md#testing) (parameters)
and [reference/query_outer](../../../reference/query_outer/README.md#testing)
(result sources). Add `REFERENCE=1` for the oracle.
PL cursor units use `RUN='^TestPLpgSQLCursor'`; public scenarios live in
[reference/cursors](../../../reference/cursors/README.md#testing).
Static query-loop validation units use `RUN='^TestPLpgSQLQueryLoopValidation$'`.
Static capture compiler/validation units use `RUN='^TestPLpgSQLSelectIntoValidation$'`.
Dynamic compiler/validation units use `RUN='^TestPLpgSQLDynamicSQLValidation$'`.
Runtime diagnostic units use `RUN='^TestPL(FormatError|AssignmentErrorPhase|CaseExpressionError|pgSQLCursorCurrentDiagnostic)'`.
Message/assertion compilation and validation use `RUN='^TestPLpgSQL(Raise|Assert)'`.
Diagnostics syntax/program/frame ownership uses `RUN='^TestPLpgSQLDiagnostics'`.
Statement-walk units over a fake host use `RUN='^TestPLRun'`.
Array-loop compiler/validator/frame/cancellation units use
`RUN='^TestPLpgSQLForeach(Compiler|Validation|TargetAdmission|Frame|Cancellation|DatumDiagnostic)$'`.

```sh
make check RACE=1 RUN='^(TestSQLRoutine|TestRoutineParameterLayout)'
make check RUN='^TestReferenceRoutine(Core|SQLBodies|Review|Parameters)'
make check RACE=1 RUN='^TestPLException(Matcher|ProgramValidation)$'
make check RACE=1 RUN='^TestPLpgSQLConditional(Compiler|Validation|Frame|Remap)$'
make check RACE=1 RUN='^TestPLpgSQLLoop(Compiler|Validation|Frame|Remap|Cancellation)$'
make check RACE=1 RUN='^TestPLpgSQLWhile(Parser|Compiler|Validation|Frame|Remap)$'
```
