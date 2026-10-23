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
checked immutable `PLProgram`. Fixed slots identify all formals then locals;
aliases share their original constrained slot. Initializer namespaces retain
declaration-order visibility. `PLFrame` owns values/scopes only; closing it
cannot mutate the program, caller arguments or already returned immutable values.
Replacement preserves complete local Usages, including typmods and once-mapped collation capabilities.
Private effective Usages inherit checked call collation for formals/ordinary
locals and their lexical `%TYPE` copies; column `%TYPE` stays fixed to its source.
Known rows retain typed whole NULL until materialized; RECORD begins indeterminate
and NULL assignment clears its shape. Composite-domain candidates validate before publication.
The higher root owns reached SQL binding, conversion, handler admission and
execution. PL VOID uses non-NULL `VoidValue`; procedure completion is separate.
PL nesting borrows the same balanced SQL-body/root guard, not a second counter.


## Testing

Package units cover frames and parameter layout; engine-level routine units live
in `internal/engine`. Public SQL routine scenarios live in
[reference/query_core](../../../reference/query_core/README.md#testing) (core,
SQL bodies, review) and [reference/operators](../../../reference/operators/README.md#testing)
(parameters). Add `REFERENCE=1` to reference selectors for the oracle.

```sh
make check RACE=1 RUN='^(TestSQLRoutine|TestRoutineParameterLayout)'
make check RUN='^TestReferenceRoutine(Core|SQLBodies|Review|Parameters)'
```
