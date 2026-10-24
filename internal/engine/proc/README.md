# proc — immutable routine definitions and private values

`proc` is the reusable, lower-level routine owner. It imports parser and scalar
value/type facilities, never engine or catalog. It does not interpret expressions
or own name lookup, permissions, query execution, transactions or prepared caches.

`Definition` retains body form, original quoted text/source position, ordered
statement syntax and ordered formal modes/names/usages/default-presence flags.
`SetFormals` derives input/output projections, inverse input slots and input-only
SQL names/usages. INOUT belongs to both; pure OUT consumes no actual input.
PL lexical `$n` separately addresses all declared formals, including OUT.
Shared SQL names are projection-aware, allowing pure IN/OUT name reuse; PL's
single formal namespace instead rejects named/positional collisions.
Published syntax/metadata is immutable. Standard RETURN is a SELECT expression;
BEGIN ATOMIC is an ordered SQL program, not a transaction or procedural return.
Checked standard-body and declaration-default recipes live in `stored`, above
this package, never as cached values or catalog dependencies in proc.

PL trigger definitions carry an explicit trigger contract and zero formals.
Their fixed OLD/NEW and TG lexical slots are separate from SQL parameters;
invocations supply fresh physical row shapes, typed absent records and owned
zero-based text-array arguments. The engine validates final trigger row returns
strictly, separately from ordinary permissive PL row assignment.
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

Complete parsing closes positional formals even after an unresolved call stops
semantic binding. Private Restore may defer only a structured terminal missing
call in a quoted body; blocked semantic checks run once invocation can resolve it.
Quoted strings/comments are not formals, and unused declared inputs are valid.

The shared binder closes positional formals even with zero inputs. Named,
routine-qualified and `$n` references use the same typed slots; real columns
and genuine column ambiguities take precedence over formal fallback. CREATE
checks all commands without producers. Quoted bodies are prepared sequentially
at invocation, so an earlier command can run before a later reanalysis error.
Standard commands refresh checked recipes by identity, never render/reparse or
reselect source names/overloads at invocation.

Only the actual nonvoid terminal SELECT or DML RETURNING supplies the scalar
result. Width and Assignment conversion are checked before that command's
producers, including empty inputs. VOID discards every output and yields typed
SQL NULL; it does not reuse an earlier SELECT result.

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

Engine shares per-command prepared-analysis retention for quoted bodies and
checks live EXECUTE/invoker authority and data. Eligible terminal SELECT uses
first-row demand; nonterminal/VOID SELECT and every DML command run fully.
Required sort/aggregate/auxiliary work remains query/DML-owned. No row yields
typed NULL. Persistence writes quoted declarations or actual standard bodies with
checked current identities/implicit usages, never invocation values, opened
executors, data snapshots or permission decisions.

Invocation borrows the same admitted engine statement scope. A balanced lexical
activation guard saves/restores query, parameter, view and protocol scratch;
the root stage, effects, completion queues, transaction and live reservation
ownership stay with their owners. A callee never calls root Finish or creates a
separate publication boundary.

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
in Variables for provider validation and replacement. `PLFrame` owns values/scopes
and the active handled diagnostic,
restoring the previous diagnostic on nested handler exit. Closing it cannot mutate
the program, caller arguments, returned immutable values or transaction-owned portals.
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
Conditional statement lists reuse their enclosing lexical scopes; only BEGIN
introduces declarations. Loop scopes retain label identity without new local slots.
Immutable acyclic control tokens identify each executable block/loop and the
hidden function namespace; transfers retain the exact enclosing target token.
Validation reconstructs owner order, kind, ancestry and condition queries, including
positioned projection aliases. No invocation flow or evaluated WHEN value persists.
Static query-loop instructions resolve existing whole RECORD/composite slots in
the enclosing namespace, without cursor recipes, implicit records or extra Usages.
Their ordinary statement syntax, source spans, target and loop body are checked
together. Query and body compound names participate in the diagnostic ledger in
lexical order without a cursor-private ROW datum.
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
legality; a suffix INTO has no post-target SQL span to visit. Nonempty spans
still use the strict bounded-token visitor. No capture evaluator or runtime
shape is persisted.
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
trigger roots retain their fixed OLD/NEW/TG namespace instead. The separate
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

`PLRun{Host, Definition, Frame}` walks one invocation of a checked program
(`plpgsql_walk*.go`). Entry points: `Invoke` (fresh frame, result selection and
the missing-RETURN diagnostic), `Trigger` (caller's trigger frame, then
`TriggerReturn` against the current row type), `Block` and `Statements`.
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

`Capture` owns reached static SQL and publishes FOUND before cardinality and
target conversion. Lists publish sequentially from one captured source row;
whole rows use `AssignRow`, with producer-shaped NULL fields for no rows.

RETURN/EXIT/CONTINUE use compact non-error flow, so successful protected
transfers release checkpoints rather than invoking handlers. Blocks and loops
consume only their own transfers. Loop iteration checks `Host.Context()` even for
an empty body; `executePLLoop` keeps its name because a reference cancellation
fixture waits for that frame. The hidden function namespace is not a block-exit
consumer; an escaping EXIT reaches the routine's missing-RETURN diagnostic.
Query loops use one width-sized demand buffer. Anonymous records are detached
per row; named rows use ordinal assignment conversion. An empty first demand
assigns descriptor-shaped NULLs, targets survive loop transfer, body and error
diagnostics keep their own action, and source failures use the FOR action alone.

## Testing

Package units cover frames and parameter layout; engine-level routine units live
in `internal/engine`. Public SQL routine scenarios live in
[reference/query_core](../../../reference/query_core/README.md#testing) (core,
SQL bodies, review) and [reference/operators](../../../reference/operators/README.md#testing)
(parameters). Add `REFERENCE=1` to reference selectors for the oracle.
PL cursor units use `RUN='^TestPLpgSQLCursor'`; public scenarios live in
[reference/cursors](../../../reference/cursors/README.md#testing).
Static query-loop validation units use `RUN='^TestPLpgSQLQueryLoopValidation$'`.
Static capture compiler/validation units use `RUN='^TestPLpgSQLSelectIntoValidation$'`.
Runtime diagnostic units use `RUN='^TestPL(FormatError|AssignmentErrorPhase|CaseExpressionError|pgSQLCursorCurrentDiagnostic)'`.
Message/assertion compilation and validation use `RUN='^TestPLpgSQL(Raise|Assert)'`.
Statement-walk units over a fake host use `RUN='^TestPLRun'`.
Array-loop compiler/validator/frame/cancellation units use
`RUN='^TestPLpgSQLForeach(Compiler|Validation|TargetAdmission|Frame|Cancellation|DatumDiagnostic)$'`.

```sh
make check RACE=1 RUN='^(TestSQLRoutine|TestRoutineParameterLayout)'
make check RUN='^TestReferenceRoutine(Core|SQLBodies|Review|Parameters)'
make check RACE=1 RUN='^TestPLException(Matcher|ProgramValidation)$'
make check RACE=1 RUN='^TestPLpgSQLConditional(Compiler|Validation|Frame|Remap)$'
make check RACE=1 RUN='^TestPLpgSQLLoop(Compiler|Validation|Frame|Remap|Cancellation)$'
```
