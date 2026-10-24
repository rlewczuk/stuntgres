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


## Testing

Package units cover frames and parameter layout; engine-level routine units live
in `internal/engine`. Public SQL routine scenarios live in
[reference/query_core](../../../reference/query_core/README.md#testing) (core,
SQL bodies, review) and [reference/operators](../../../reference/operators/README.md#testing)
(parameters). Add `REFERENCE=1` to reference selectors for the oracle.
PL cursor units use `RUN='^TestPLpgSQLCursor'`; public scenarios live in
[reference/cursors](../../../reference/cursors/README.md#testing).

```sh
make check RACE=1 RUN='^(TestSQLRoutine|TestRoutineParameterLayout)'
make check RUN='^TestReferenceRoutine(Core|SQLBodies|Review|Parameters)'
make check RACE=1 RUN='^TestPLException(Matcher|ProgramValidation)$'
make check RACE=1 RUN='^TestPLpgSQLConditional(Compiler|Validation|Frame|Remap)$'
make check RACE=1 RUN='^TestPLpgSQLLoop(Compiler|Validation|Frame|Remap|Cancellation)$'
```
