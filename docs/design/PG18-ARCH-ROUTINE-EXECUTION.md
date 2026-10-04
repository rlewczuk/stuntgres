# PG18-ARCH-ROUTINE-EXECUTION — Routine execution frames and invocation boundaries

## 1. Decision, scope and evidence status

**Decision:** use one engine-private invocation frame and one internal statement route for SQL functions, procedures, PL/pgSQL, anonymous blocks, triggers, event triggers and dynamic SQL. Language consumers supply immutable programs and construct-specific adapters, not alternate binders, expression interpreters, authorization stacks, transaction owners or stores. Public direct and prepared SQL continue to converge on the connection execution route.

This is a future architecture decision, not implementation of CREATE FUNCTION/PROCEDURE, CALL, DO, PL/pgSQL, triggers or event triggers. All declarations below are **proposed contracts**, including prerequisite-design interfaces; their appearance here does not mean they exist in the current engine. This session changes only this document. It does not modify prerequisites, catalog edges, configuration, parser, engine, drivers or tests.

The official `/docs/18/` manual identifies PostgreSQL **18.6**. Preserve the existing **18.0**, UTF8/C reference runtime, `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Minor-specific alignment belongs to `PG18-REF-ORACLE-18-6`. Local PostgreSQL 20devel and ramsql are research aids, not release oracles or production dependencies.

**No SQL witness in this document has been executed.** Every outcome is an independently specified future expectation. Baseline verification cannot prove routine behavior. Future feature acceptance must assert direct/prepared public database/sql rows, exact logical names/types, command-defined counts, full PostgreSQL English diagnostics and supplied optional fields, failures and atomicity, cancellation and relevant retained/native-control behavior; comparing two implementations to each other is insufficient.

The implemented baseline remains narrow: CREATE TABLE, INSERT VALUES and single-table SELECT, primitive types and retained snapshots, plus native Reset/Dump/Restore. Ordinary built-in scalar functions/operators are independent of this design. This decision does not prescribe physical planning, publication/lock algorithms, storage durability, wire protocol, external language ABI, disk spilling or a generic successful no-op for missing SQL effects.

## 2. Shared owners and concrete interfaces

### 2.1 Authority and lifetime

| Owner | Sole authority | Routine integration |
|---|---|---|
| CATALOG | ObjectRef/revision, descriptors, dependencies, BoundPlan validity and publication | Own routine definitions, invalidate programs/bindings after relevant changes; never retain a permission token or data snapshot in a plan |
| TYPE / EXPR | sqltype.Usage/Value, coercions, callable/operator resolution, indexed EvalFrame evaluation | Resolve signatures and defaults, prepare coercions, expose a routine-call adapter; no PL-specific type system |
| QUERY | ColumnDescriptor, FrameLayout, common RowCursor/RowResult, finite eager public output | Consume routine scalar/row/set adapters and internal statements; Rows.Schema() is authoritative |
| SESSION | Physical session, pointer-owned *Operation, prepared records, portals and terminal cancellation | Admit once for the public request; no recursive connection acquisition or child Operation.Done |
| TX | Persistent TransactionOwner, current incarnation, conditional views, write stages, marks, participant/end lifecycle | Add child scopes, unnamed exception marks and eligible segment rotation (§6); no frame-owned TxState or publication |
| AUTH | Immutable AuthorizationView, coherent ACL/owner/role checks and scoped identity | Ordinary call authorization or narrowly trusted manager entry (§7); no copied mutable RoleState |
| Routine execution | Private invocation slots, lexical control, call-local status/handled diagnostic and result adaptation | Own balanced enter/pop and temporary resources only |
| DML / DDL managers | Trigger/event scheduling, installed descriptors, firing operation and collected evidence | Issue contextual invocation requests; interpret return actions and retain queue/evidence lifetimes |

### 2.2 Proposed engine-private records

The following field contracts are concrete but do not add a public Go API. Existing owner-defined identity/type/source shapes must be reused at implementation, not shadowed with structurally similar alternatives.

| Record | Immutable/shared fields | Mutable ownership |
|---|---|---|
| `RoutineDefinition` | Coherent ObjectRef/revision; kind/language; typed input identity/signature, modes/defaults/variadic mapping; lexical slot layouts; declared result usage/schema/**setness**; body representation and source occurrences; strictness, volatility, security and SET policies | Catalog owns descriptor publication/replacement, not execution locals |
| `RoutineProgram` | Ordered SQL occurrences or procedural instruction tree; lexical labels/coordinates; each SQL occurrence's original text/span, value-parameter coordinate recipe, captured ambiguity policy and catalog BoundPlan handle | Retention belongs to SESSION PreparedSQL/catalog machinery. Replace immutable binding versions through that owner; no locals, FOUND, output scratch, snapshot or separate validity cache in program |
| `InvocationRequest` | Resolved definition; caller execution state; typed actual values/copyback destination recipes; closed kind `SQLFunction`, `ProcedureCall`, `AnonymousBlock`, `Trigger`, `EventTrigger`; callsite source; typed optional special context | Ordinary-call and manager-call constructors are distinct internal seams. Manager provenance cannot be supplied by SQL or a public adapter |
| `RoutineFrame` | Private parent link; retained definition/program; immutable slot layouts; special context descriptor/evidence | Private parameter/local/OUT slots, lexical block state, FOUND, ROW_COUNT, control status, active handled diagnostic, scoped AUTH/settings handles, eligibility evidence, result accumulator and temporary cursor/pins |

A frame borrows the executing state and root Operation. It resolves **current** transaction identity through TX's live segment context; it must not copy an authoritative TxRef into every frame and keep using it after COMMIT. Parent links and local slots are execution-private, never part of shared bound plans. Recursion/repeated calls/session executions get distinct mutable storage. Coordinates reuse QUERY/EXPR FrameLayout conventions; slot numbers are not catalog attribute numbers.

Proposed seams:

```text
invokeRoutine(state, request) -> declared result or originating error
executeInternalSQL(state, frame, occurrence, typedParameters, bindingPolicy, productionPolicy)
    -> common Result{Count, Rows} or originating error
```

`invokeRoutine` is the sole entry/pop owner. `executeInternalSQL` invokes the shared parser/binder/catalog validity/executor under a TX child scope; it is not recursive public `conn.execute`, does not import driver arguments again and does not autocommit. An EXPR call adapter consumes declared scalar/row results. `PG18-QUERY-SRF-FROM` owns relational occurrence scheduling, not the frame. A dynamic command uses the same seam with a freshly parsed occurrence and no lexical capture; it does **not** establish another routine identity. DML/event managers invoke the same frame with typed context and manager-issued provenance.

`productionPolicy` is a closed **QUERY-owned** semantic production/effect contract installed before opening internal SQL (§5.2), not post-hoc truncation of an already drained public result. The routine/static-INTO adapter requests bounded consumption where permitted; **dynamic EXECUTE requests complete production and only afterward applies INTO/STRICT adaptation**. The shared executor validates mandatory producer effects and returns common typed output/count evidence for the selected policy. It cannot introduce a language executor or weaken eager public output.

Programs dispatch through the supported language registry. Unsupported language/construct behavior must be diagnosed by its bounded owner, not silently ignored. Registry dispatch is not permission for external ABI/plugin machinery.

## 3. Entry, slots, exit and result ownership

### 3.1 Entry categories and transitions

| Entry | Frame and authority | Exit consumer |
|---|---|---|
| SQL function / nested scalar function | Fresh invocation; ordinary callable checkpoint; function entry makes transaction end atomic | EXPR declared scalar/composite/set adapter |
| Top-level or nested CALL | Fresh procedure invocation; ordinary checkpoint; contextual non-atomic eligibility, not privilege based only on kind | Public OUT/INOUT row or procedural destination copyback |
| DO | Fresh anonymous lexical frame; language/definition checks, no synthetic routine EXECUTE target | No row relation; contextual transaction eligibility |
| PL lexical block | New lexical slot scope inside invocation, not a new invocation or SQL transaction | Parent lexical control/slots; protected block optionally owns TX mark |
| Trigger/event trigger | Fresh frame for each fire; manager-specific context/provenance and identity | Manager action/result interpretation, never ordinary scalar export |
| Dynamic EXECUTE | Child SQL command of existing frame/AUTH/Operation | INTO/status/discard adapter; no second identity transition |

State transitions are `Entering -> Running -> Returning -> Closed`, or any preclosed state `-> Unwinding -> Closed`. `Closed` never executes again. Failed partial entry releases only acquired resources. A STRICT skipped call is a completed ordinary authorized call **without body execution**: nonset scalar/composite declarations adapt a typed NULL value; SETOF/TABLE declarations adapt an empty typed set with full declared schema and **zero occurrences**, not one NULL occurrence. The ordinary EXECUTE checkpoint remains required. QUERY's relational consumer, not the skip adapter, owns any surrounding outer-join null extension.

Entry validates coherent kind/reference/signature and ordinary authorization or trusted manager provenance; marshals actuals/defaults/coercions according to caller SQL semantics; applies STRICT; allocates private slots/context; enters scoped identity/settings; installs frame/eligibility; runs the program. Return validates declared result, closes required child production, detaches output, applies successful OUT copyback and pops/restores. Do not invent left-to-right SQL expression evaluation guarantees beyond the caller's contract. Default/coercion evaluation uses the shared TYPE/EXPR machinery, not string evaluation in the frame.

Cancellation is checked at entry, instruction/loop boundaries, internal SQL analysis/production and before successful return/publication, including nonproductive loops. Body/result/cleanup failures visit every acquired resource exactly once. No child calls Operation.Done. Root finishes the current segment once and then releases its Operation; required completion failures prevent success. Original diagnostic stays primary if cleanup also fails (§6/§7).

### 3.2 Parameters and lexical state

IN initializes a private input slot. OUT initializes a private output slot and does not belong to a function's input calling identity. INOUT participates in input identity and initializes a writable output slot. VARIADIC maps actual values to its typed array through signature binding; STRICT tests the entire array, not each element. Input defaults (including INOUT) must satisfy trailing-input rules at definition. Overload/named-argument/polymorphic resolution belongs to TYPE/EXPR/signature consumers, not runtime name lookup.

Top-level CALL supplies the required plain-SQL OUT actual placeholders; these expressions are not evaluated (NULL is conventional). Procedural CALL requires assignable destinations for OUT/INOUT. Stage destination adaptation before assignment and copy all outputs back only after successful callee return, never a partially copied prefix on failure. A failed child must not overwrite caller locals/FOUND.

PL blocks have prebound lexical labels and shadowing. Qualification can reach an enclosing labeled slot. The hidden routine-name block contains parameters and FOUND; no per-row maps or global variable dictionary. Declarations initialize at block entry; constants, NOT NULL, `%TYPE`, `%ROWTYPE`, record shape and assignment/coercion restrictions are construct/type-owner obligations. FOUND starts false and is invocation-local; ROW_COUNT is call-local and updated by relevant commands. EXCEPTION handlers save/restore an independent active handled diagnostic; these are not transaction state.

### 3.3 Declared results and lifetime

A non-void SQL function takes its result from the **actual final statement**, which must be SELECT or INSERT/UPDATE/DELETE/MERGE with RETURNING and satisfy its declared type/schema. Never search backward for an earlier result-producing command. Definition-time semantic validation executes no statements; deferred text-body runtime validation occurs sequentially when each command is prepared, with terminal shape checked before executing **that terminal command**, not necessarily before earlier commands (§4). SQL void functions and procedures follow their own no-scalar-result contract and may end in a nonresult command; they never reuse an earlier SELECT as a scalar result.

| Consumer | Required adaptation |
|---|---|
| SQL scalar | First demanded row of the actual final valid command; no row gives typed NULL. Eligible ordinary SELECT uses FirstRow before production; DML RETURNING completes mandatory effects before scalar adaptation. Without ORDER BY, no general first-row identity promise. Validate declared width/type and owner-planned conversions |
| SQL composite / OUT | Actual final command satisfies exact declared schema/names/types; nonset demand as above, OUT names determine composite metadata |
| SQL SETOF / TABLE | All required rows of actual final valid command, including empty relation with full metadata; STRICT skip supplies zero occurrences with that schema |
| SQL void | Complete required body effects; discard unneeded row output with proper cleanup, no scalar selection from an earlier statement |
| PL scalar RETURN | Exit invocation with declared value; required scalar falling off end is failure |
| PL OUT / void | RETURN uses current OUT slots; allowed implicit return at end |
| PL RETURN NEXT / RETURN QUERY | Append, do not exit. Materialize complete permitted set before public exposure; later error yields no public prefix |
| Procedure | RETURN has no expression. CALL exposes OUT/INOUT row if applicable; no SELECT-procedure invocation |
| DO | No row relation (`Rows == nil`), not an empty SELECT relation |
| Trigger/event | Manager-specific interpretation (§8), not scalar/composite SQL export |

All result adapters use QUERY's `ColumnDescriptor`, typed Values and common `RowResult`. `Rows.Schema()` alone is authoritative; zero rows preserves schema. Counts remain command-defined; CALL/DO counts are not the sum of internal DML counts (initial adapter uses zero affected count for CALL/DO). Top-level CALL with OUT values has one result occurrence. SQL function results consumed in SELECT do not create their own public command count.

Final output independently retains needed immutable values/schema/pins **before** frame/scope end. It cannot retain a live frame, mutable slot, statement lease, eligibility token or current transaction. Exported mutable bytes detach from internal values and other exports. Public rows survives subsequent calls/TX end/native replacement as QUERY permits. Exec closes discarded rows, including CALL OUT/RETURNING; Query transfers sole ownership; EOF/error/Close cleanup is idempotent, with immutable metadata retained. PL sets follow QUERY finite eager/no-fragment policy, not a new streaming implementation. Use slot slices/private indexed scratch, retaining/copying only values whose lifetimes require it.

## 4. Embedded analysis and name/parameter binding

One binder receives explicit language policy and lexical slot recipes, while QUERY retains relational scope/correlation rules. PL expressions delegate to the same SQL expression executor (conceptually SELECT expression), with one column/at most one row and NULL for no row. Assignments use type-owned assignment casts and PostgreSQL's documented textual output/input fallback when no assignment cast exists, not a second coercion implementation.

| Body/occurrence | Parse/analyze boundary | Retention |
|---|---|---|
| SQL-standard RETURN / BEGIN ATOMIC | Definition-time parse/analyze and dependency tracking; unresolved names/types reject installation; polymorphic definition limitations remain DDL-owned | Definition dependencies and immutable program |
| SQL string body | Definition validator/check_function_bodies behavior distinct from runtime **sequential command** semantic preparation; terminal return-shape check before terminal command execution, earlier commands may already have run | Shared prepared/catalog retention; do not claim every text body is unvalidated at CREATE or every runtime command eagerly validated before body effects |
| PL body | Validator compiles procedural structure/applicable syntax; syntactically valid embedded name/type semantics are analyzed on first **reached** execution | Reached statements retain shared binding; unvisited semantic errors remain deferred |
| Dynamic EXECUTE | New command text parse/analyze each execution | Temporary fresh plan under shared validity machinery, not PL static cache |

For SQL text bodies, default `check_function_bodies=on` semantically validates at CREATE without executing statements and rejects an invalid terminal shape (`42P13` in W5e). Disabling that validator permits text-body installation; runtime prepares/analyzes each command as execution reaches it, runs that command under the shared child-scope/production policy, then proceeds to the next. Actual-final-command return validation rejects before executing **the invalid terminal command**, but preceding commands can already have run. No eager whole-body runtime return validator or blanket no-body-effects invariant is allowed. Neither boundary returns an earlier SELECT as the function result or executes the invalid terminal INSERT. Prior transactional effects roll back through the containing statement/subtransaction; prior nontransactional sequence advances can remain observable (W5g). SQL-standard bodies remain definition-parsed/analyzed independently of suppressed text-body checking. PL's first-reached embedded semantics remain distinct.

A syntax error is not a witness for deferred semantic validation. Even on an empty relation, a reached statement binds all of its expressions before scanning. W1 contrasts unreachable valid SQL with SQL-standard definition-time rejection.

| Binding policy | Resolution rule |
|---|---|
| SQL routine body | Column names take precedence over unqualified arguments; function-qualified argument does not win over an actual matching qualified column |
| PL static SQL | Default variable/column ambiguity fails `42702`. Qualification or captured `error`/`use_variable`/`use_column` policy resolves it; per-function directive overrides global compilation policy |
| PL lexical slots | Only grammar value positions substitute typed parameters by prebound coordinates. Relation, target-column and function identifiers remain SQL identifiers |
| Dynamic SQL | No lexical-variable substitution. USING supplies typed values; identifiers must be constructed as quoted text, e.g. format `%I`/quote_ident. Utility parameter acceptance belongs to the command binder |

Changing the global variable-conflict setting affects later statement compilations, not already compiled occurrences. Retain the effective policy in the immutable occurrence. SQL `EXECUTE` of a prepared statement and PL dynamic `EXECUTE` are distinct command consumers, not ambiguous routes to the same string dispatcher.

**Static** non-STRICT ordinary SELECT INTO supplies FirstRow before opening; static STRICT supplies CardinalityProbe (enough evidence for exactly one, `P0002`/`P0003`). **Static modifying RETURNING INTO, STRICT or not, supplies a two-returned-occurrence primary-producer probe**: second occurrence yields P0003 before an unused third primary action/RETURNING; no successful assignment/output, transactional effects roll back. Required secondary modifying work/cleanup remains owned by QUERY/DML; it does not force unlimited primary draining. **Dynamic EXECUTE uses complete/unlimited production with or without INTO**, then assigns first/nulls without STRICT or requires exactly one with STRICT; dynamic modifying RETURNING multiple rows is accepted **without STRICT**. EXECUTE updates ROW_COUNT from complete production but does **not** change FOUND. PERFORM discards a properly closed result and updates FOUND. RETURN QUERY, loops and FETCH update status only as their construct's rules require; no universal status update in `executeInternalSQL`.

## 5. Program validity and QUERY-owned internal production demand

### 5.1 Retention and invalidation

CATALOG BoundPlan owns reference/dependency/type/environment validity and reanalysis; SESSION PreparedSQL owns session retention. Routine occurrences refer to those records, not a second generation counter or permission cache. Specialization must distinguish routine revision, actual polymorphic usages, session-dependent resolution and trigger relation descriptor where applicable. Shared immutable programs hold no execution scratch; replacement swaps immutable bindings rather than mutating a plan during execution.

Retain original SQL/body text, occurrence spans and parameter coordinate/type recipes for reanalysis and diagnostics. Frozen public prepared parameter/output description remains authoritative; revalidation cannot silently change public prepared shape or types. Record shape/type changes preserve first-analysis expectations and release-defined errors; dynamic SQL is the fresh-analysis alternative, not a promise that all cached statements transparently retype.

Relevant DDL/type/catalog revisions, resolution/search_path changes and statistics changes cause the shared PREPARE-consistent reanalysis/replan. Merely creating a same-name object earlier in an unchanged search_path does **not**, by itself, promise automatic rebinding of a previously resolved reference. Do not lookup every name afresh each invocation, nor execute stale pointer storage after ALTER/DROP/epoch replacement. Ordinary authorization is reevaluated at execution; namespace USAGE retains AUTH's previously-looked-up statement caveat. Preparation stores no permission token and no persistent data snapshot, although snapshot-requiring semantic analysis can establish TX's higher-isolation pin. Generic/custom physical planning heuristics are not routine architecture obligations.

### 5.2 Preproduction demand and mandatory effects

QUERY distinguishes eager **public final output** from demand-sensitive internal children. Before open, the proposed `productionPolicy` carries closed consumer classification, primary production (`FirstRow`, `CardinalityProbe`, `TwoReturnedProbe`, `CompletePrimaryProduction`, `AllRows` or query-loop demand), required auxiliary completion and cardinality/assignment adaptation. Classification distinguishes SQL-function final DML, static modifying RETURNING INTO, static modifying commands without INTO and dynamic EXECUTE; it is not inferred solely from “is DML”. The shared binder/executor enforces that classification: a scalar SQL-function adapter cannot limit final DML actions, and cleanup cannot erase a static modifying INTO probe by draining its unused primary occurrences. All policies use one executor, not manufactured LIMIT ASTs or a language-specific route.

| Internal consumer | Production contract before open |
|---|---|
| Nonset SQL function final eligible ordinary SELECT; **static** non-STRICT PL SELECT INTO | FirstRow: request one occurrence or EOF, finalize retained first/no-row output, close unused child production. Unneeded later volatile targets/rows are not evaluated |
| **Static** STRICT SELECT INTO; one-row expression/cardinality adapter | CardinalityProbe: request enough evidence (a second occurrence) to prove too-many; do not drain arbitrary later rows. On cardinality failure discard output/assignment and preserve the consumer's originating diagnostic |
| SQL SETOF/TABLE; PL RETURN QUERY; commands requiring all results | AllRows: complete required output; query-loop adapters instead use QUERY's owned incremental demand/stop/close contract |
| **Static modifying RETURNING INTO, STRICT or not** | TwoReturnedProbe: request at most two returned occurrences from the primary producer. Second yields P0003; do not execute unused third primary action/RETURNING. Discard assignment/output and roll back transactional effects, never successfully publish partial DML. Independently required secondary modifying work and cleanup retain their owner-defined obligations |
| SQL-function final DML (including scalar), static modifying commands **without INTO** | CompletePrimaryProduction: finish required primary actions/RETURNING and auxiliary work before successful completion. Scalar SQL function retains first output, never limits writes. Counts producer-owned. Static no-INTO result-destination checks remain applicable |
| **Dynamic PL EXECUTE**, with/without INTO/STRICT | AllRows/unlimited production, then INTO first/null adaptation or STRICT exactly-one check (P0002/P0003). No static one/two-row stop. Non-STRICT multirow DML RETURNING succeeds, assigns first, ROW_COUNT reflects full production, FOUND unchanged |

Complete production does not require retaining every unused row: the common result collector may retain only first-row/schema/count evidence for dynamic INTO/discard while still evaluating **every** required occurrence and effect. Do not stop dynamic production once a second occurrence proves STRICT failure; its cardinality check follows completion. Dynamic no-INTO also fully executes then discards/closes results. Non-STRICT empty dynamic INTO assigns nulls; STRICT empty fails P0002. Late production failure remains primary over any unrealized postproduction cardinality failure. Do not turn full production into avoidable copying or a public streaming API.

Demand does not imply every physical stage scans only one/two input rows: sorts, aggregates, subplans and required secondary modifying work can need more. Eligible scalar SQL final ordinary SELECT can stop after one demanded occurrence. **Do not generalize full primary DML completion to static modifying RETURNING INTO**: its two-returned probe stops primary production before an unused third occurrence; finish/cleanup still performs required auxiliary work through QUERY/DML and closes pins/cursors. On second occurrence P0003 prevents assignment and successful publication, then TX rolls back protected/statement writes. A zero/one-returned successful case still honors its producer's required finish semantics. Full primary production remains for SQL-function final DML, dynamic EXECUTE and static modifying commands without INTO. Intermediate SQL-body commands retain required effects; DML count remains producer-owned. No new publication or secondary-stage algorithm is defined here.

After bounded demand stop, complete production, EOF, failure or cancellation, QUERY closes resources once and preserves required auxiliary completion **without unconditionally draining a stopped primary producer**. Child scopes finish under TX without autocommit; root Finish/Operation.Done and retained result ownership remain. Public finite output is eager for the permitted policy, not indiscriminate internal production. W5d separates scalar SELECT/static SELECT probes from full SQL-function DML; W5f preserves full dynamic semantics; W5h distinguishes static modifying INTO's two-returned probe and error precedence. U5 asserts pre-open classification, assignment discard, auxiliary cleanup and no partial successful DML publication.

## 6. TX-owned child commands, exceptions and transaction rotation

### 6.1 Additive proposed TX protocol

This section explicitly extends the future TX consumption contract **without changing** `PG18-ARCH-TX-VISIBILITY.md` or claiming implemented APIs. TX continues to own existing visibility/publication/participant algorithms. The extension is needed because root `StatementScope.Finish` alone cannot represent caught block failure or a live CALL spanning multiple transactions.

Proposed private signatures use existing scopes/Operation and opaque handles:

```text
BeginChild(parentScope, viewPolicy) -> ChildScope | error
ChildScope.Finish(cause) -> stored completion error
MarkException(parentScope) -> ExceptionMark | error
ReleaseException(mark) -> stored completion error
RollbackException(mark, originatingCause) -> stored completion error
RotateRoutineSegment(rootScope, Commit|Rollback, chain, eligibility)
    -> SegmentTransition{Ended, Current} | error
```

`ExceptionMark` and child handles are exact-owner/session/target/**segment** qualified, nonforgeable to public callers; nil/foreign/stale handles fail ordinary invariant errors without mutation. Marks are unnamed and inaccessible to SQL SAVEPOINT lookup. Routine consumers do not add PL SAVEPOINT support.

`BeginChild` attaches to the current segment under the same admitted Operation/TransactionOwner. `viewPolicy` requests either inherited caller query view (STABLE/IMMUTABLE) or fresh query view (VOLATILE), always subject to TX isolation/own-command visibility. Acquire only when semantic analysis or execution needs a snapshot. A VOLATILE command is not a separate implicit transaction. STABLE/IMMUTABLE non-SELECT restrictions belong to attributes; calling a volatile function through SELECT remains permitted as PostgreSQL documents.

Child success closes temporary cursors/views, freezes/merges its writes privately into the current segment and advances the appropriate command boundary. Child failure discards its command stage and propagates toward the nearest protected mark or root; it does **not** prematurely finish/autocommit the public request or irreversibly abort ancestors which a protected rollback can recover. A handler reads restored healthy state after TX rollback/release. Each child completion is effect-idempotent and returns its stored outcome. It cannot publish, call root Finish or Operation.Done. Required child cleanup failure prevents successful completion.

### 6.2 Protected block state and error unwind

`Ready -> Marked -> ProtectedBody -> Released -> Complete` on success. On failure: `ProtectedBody -> RollingBack -> MarkReleased -> MatchingHandler -> Handler -> Complete`, or `MatchingHandler/Handler -> Propagating` on no match/new error. A mark must be fully acquired **before** protected-body effects. Declaration initialization follows the language's block-entry ordering; the protected database mark covers the executable protected body, not a new local-variable rollback mechanism.

TX marks row/catalog overlays and registered settings/temp/portal/provider participants. Partial mark acquisition unwinds acquired marks. Release merges successful work into parent. Rollback restores all protected database/participant effects and releases the failed mark before selecting the first matching handler. Local/OUT/status slots are not TX participants and retain failure-time values. Preceding enclosing-block writes remain. Nested marks affect only their protected boundary; released child writes subsequently belong to their parent.

Preserve the originating diagnostic before cleanup; install it as handler-active diagnostic and restore the prior handled diagnostic afterward. Bare RAISE rethrows the same record. OTHERS excludes query_canceled/assert_failure. A handler's new error bypasses its own handler list and propagates outward. Rollback/restore failure must not leave a partially healthy Active state or replace the initiating diagnostic; TX continues cleanup and records secondary evidence. Uncaught error invokes root Finish(error), applying ordinary implicit discard/explicit-abort/savepoint rules once. Returned rows and pending OUT copyback are discarded.

### 6.3 Eligibility and segment state

Eligibility is closed engine evidence propagated from the actual stack and revalidated by TX, not a user bool or solely a routine-kind flag.

| Context at transaction command | Allowed? |
|---|---|
| Standalone top-level CALL/DO, no explicit outer transaction | Yes |
| Uninterrupted nested CALL/DO from eligible context | Yes, unless another blocker applies |
| Explicit outer BEGIN / database/sql transaction | No, atomic context |
| Intervening SELECT/function invocation, SQL/PL function, trigger/event frame | No, atomic context propagates to descendants |
| SECURITY DEFINER procedure, procedure with SET clause | No |
| Active exception protected-body mark | No; otherwise non-atomic context reports subtransaction-specific error |
| Handler after failed mark rollback/release | Not barred solely by lexical EXCEPTION membership; all other blockers still apply |
| Non-read-only cursor loop | No; cursor owner supplies restriction |

Atomic-context checks precede active-subtransaction checks. Known primaries from release source are `2D000 invalid transaction termination`, or, in otherwise eligible protected context, `2D000 cannot commit while a subtransaction is active` / `cannot roll back while a subtransaction is active`. Do not catch OTHERS in the failure witness and accidentally convert it to success.

Eligible rotation prepares/completes prior segment through existing TX end authority (participant/observer validation/preflight, commit publication or rollback), retires old marks/views/TX scratch and starts a fresh healthy segment automatically. Ordinary COMMIT/ROLLBACK uses session defaults for the new transaction; AND CHAIN preserves characteristics. Root scope remains a live segment envelope; frames consult its renewed TX context rather than stale scope handles. Retain Operation, args, locals, lexical control and AUTH; renew all transaction-qualified resources. Root Finish later completes **only the remaining segment**, then Operation.Done once. Earlier committed segments survive later error/cancellation; rollback discards only the current segment. End failure unwinds; no continuation with a usable old incarnation, partial publication or automatic retry.

Cursor ownership remains SESSION/TX. The first eligible end in a read-only query loop makes its cursor holdable/fully evaluated; loop exit closes it. TX end releases query locks per lock owner; routine frames do not retain them. A write-driven cursor loop cannot end a transaction. No callbacks/external I/O run under engine/publication locks.

Settings/rotation must distinguish scopes: routine SET-clause restoration is a scoped settings handle; SET LOCAL of the same variable is confined to it. Other LOCAL changes follow TX marks/end. Ordinary SET overrides the routine restoration value and persists absent transaction rollback. Never restore every session setting on every frame pop or silently roll ordinary SET back on successful return.

## 7. AUTH entry, explicit manager refinement and errors

### 7.1 Ordinary scoped authority

Ordinary calls use AUTH's `ForRoutine(ref,definer)`: coherent callable EXECUTE check before body effects; INVOKER inherits caller's effective authority; DEFINER enters the **current owner** view, retaining Authenticated/SessionUser/ResetRole. Nested invokers inherit that scoped view. Never mutate persistent session RoleState to implement entry. Save/restore the exact caller AuthorizationView on normal/error/cancellation/partial-entry paths. Definer context prohibits role/session-authorization switching through AUTH. Revalidate refs/owner/ACL at the proper execution checkpoint; prepared calls cannot cache grants.

Routine SET settings/search_path use the settings owner, with the distinctions in §6. Safe definer examples specify trusted schemas followed by explicit pg_temp; this design does not rewrite arbitrary search_path or promise an unsafe function is secure. Default PUBLIC EXECUTE persists until revoked.

### 7.2 Narrow precedence refinement for installed trigger/event firing

AUTH §7's unconditional ordinary caller EXECUTE wording does **not** apply to implicit firing of an already installed trigger/event trigger. This document settles a bounded consumer refinement, not a prerequisite edit: **trusted implicit manager dispatch omits only the per-fire ordinary user-call EXECUTE recheck**, while sharing the same AUTH scoped-identity owner and every body access check.

Use separate private ordinary/installed-manager constructors. The DML/DDL manager issues unforgeable internal provenance tying the installed descriptor, coherent reference, permitted dispatch event/context, captured queuing identity where applicable and current operation to the entry. AUTH validates this evidence and applies invoker/definer identity using the same scoped mechanism. No exported `skipAuthorize` flag, user-constructible token, generic bypass or second role representation is allowed. Direct SQL AST/function calls cannot manufacture manager entry.

CREATE TRIGGER retains its table TRIGGER and function EXECUTE creation checkpoints. Event-trigger definition retains its superuser and function/protocol validation owner. Revoking function EXECUTE after installation does not disable installed implicit firing; reinstalling after revoke must fail the definition checkpoint. Body relation/sequence/function access still authorizes normally. Queued invoker triggers use the role that queued the event, unless SECURITY DEFINER uses current owner. Direct ordinary trigger/event function calls retain normal authorization and protocol restrictions, not ordinary success or a bypass. W10 separates these cases.

Evidence: official [CREATE TRIGGER Notes](https://www.postgresql.org/docs/18/sql-createtrigger.html) describes creation privileges; release `ExecCallTriggerFunc` / `EventTriggerInvoke` dispatches without ordinary per-fire EXECUTE checks (§12). This refinement is deliberately not generalized to other callable use.

### 7.3 Diagnostic and location invariants

The originating parser/resolver/type/AUTH/feature/TX diagnostic remains primary, including SQLSTATE, PostgreSQL English primary, detail/hint and available Position/InternalPosition/InternalQuery/Context/object fields. Record source occurrences from whole input/body/embedded command/dynamic command; external positions are one-based characters, not raw byte offsets. Nested context adds caller occurrences without losing leaf evidence. RAISE supplied fields and active handled diagnostic survive rethrow. Do not invent Schema/Table/Column from a reference, guessed context line numbers, or PostgreSQL C File/Line/Routine.

Reuse `internal/sqlstate`, root Error alias and existing diagnostic/notice transport. NOTICE/WARNING must be delivered, not silent success or an exception. Adapter/context/I/O/invariant failures are ordinary Go errors with no fabricated SQLSTATE. An already failing path preserves originating error if cleanup also fails; required cleanup/Finish failure on an otherwise successful path prevents successful return/output/publication. SQL cancellation and ordinary context cancellation retain their existing owner-defined distinction.

## 8. Trigger and DDL event contextual frames

### 8.1 DML triggers

Trigger functions have no ordinary actual arguments; installed literal arguments are TG_ARGV (zero-based) and TG_NARGS, with out-of-range element NULL. Typed context includes OLD/NEW according to event/level, TG_NAME/TG_WHEN/TG_LEVEL/TG_OP/TG_RELID/TG_TABLE_NAME/TG_TABLE_SCHEMA and PG18-compatible deprecated TG_RELNAME. OLD/NEW availability and relation row descriptor are supplied by DML, not invented by a scalar function adapter.

| Fire kind | Return interpretation |
|---|---|
| BEFORE row INSERT/UPDATE | NULL skips this row and subsequent row triggers; returned NEW replaces action image |
| BEFORE row DELETE | Nonnull return permits action; returned row contents do not replace DELETE target; NULL skips |
| INSTEAD OF row | Nonnull marks handled/counts; NULL skips; DML owns real underlying effects |
| AFTER row / statement | Return ignored; required child result cleanup still occurs |

Statement triggers fire even for zero affected rows. DML owns alphabetical/timing order, action selection, transition relations, final-image RETURNING/counts, constraint queues and firing-operation atomicity. A trigger error rolls back all effects of the firing statement, including prior trigger audit writes/earlier rows, subject to enclosing TX exception/savepoint behavior. Do not detach trigger writes into an implicit transaction.

Deferred queues own immutable row images/ref/source/queuing-role evidence under TX mark/end lifecycle. They retain no old mutable RoutineFrame or finished StatementScope. Each fire creates a fresh frame with current applicable descriptor/owner validity and queue identity. Transition relation providers retain manager-owned set images for their proper lifetime, not a last-row mutable slot.

### 8.2 DDL event triggers

The special result kind is event_trigger, no ordinary arguments or scalar row. Frame exposes TG_EVENT/TG_TAG; manager-owned collection handles supply command/drop/rewrite/address evidence to contextual helper consumers. These collections are not a second catalog.

`ddl_command_start` fires before command object-existence checks; error prevents command/end. `ddl_command_end` fires after staged catalog changes before commit; error rolls back both DDL and handler writes. `sql_drop` runs after removal before end and exposes removed-object evidence without live lookup; `table_rewrite` precedes the relevant rewrite. Aborted transactions do not fire events. Event manager owns tag filters, alphabetical order, excluded shared-object/event-trigger DDL and helper context checks. PG18 login events are a SESSION/adjacent extension boundary, not added DDL implementation here.

Helper misuse must produce release-defined contextual errors, never fake empty success. Exact helper-specific diagnostics belong to each future bounded consumer's pinned fixture. Event definition/scheduling/catalog effects must be substantive before its implementation task claims acceptance.

## 9. Exact clean-cutover inventory

Paths are relative to the assigned worktree; ordinary anchors describe current code, not frozen evidence or implemented replacements. Future bounded owners inspect every actual caller and migrate affected tests/docs at their cutover. This session changes none of these sources.

| Current seam | Required clean replacement and preserved behavior |
|---|---|
| `internal/parser/ast.go:3–4,30–61` closed Statement and CreateTable/Insert/Select markers | Extend the closed positioned AST with routine DDL/CALL/DO and distinct definition-text/SQL-standard/procedural body representations. Preserve source occurrences; no open string-command registry |
| `internal/parser/grammar.y:22–29,46–53`; `lexer.go` unsupported keywords/token dispatch | Add owned tokens/productions, preserve complete input/body semicolons. Never split routine text at semicolons or dispatch SQL by prefix. Generate `grammar_gen.go` from grammar, never hand-edit it |
| `internal/parser/parser.go:14–41,59–85` Parse/error positions | Retain UTF8/NUL/whole-input parsing and source-to-character diagnostics; internal body positions map to correct occurrence |
| `internal/engine/engine.go:24–42,130–138,197–340` ParameterInfo/Result/binding/Describe/describe | One typed description/binder and catalog BoundPlan/program references; remove migrated flat parser.Type/driver.Value inference/projection branches, no PL-only binder |
| `engine.go:436–459,482–578` prepareArgs/public execution lock/coercions | Import public driver args once; internal calls use typed slots. Capture coherent refs/views under synchronization then execute recursion outside the non-reentrant Engine mutex. Never call current Execute recursively while holding it |
| `engine.go:579–634` INSERT staging/SELECT Snapshot switch | Preserve complete multirow atomic staging until DML/TX migration; use same execution pipeline/child scopes. Remove migrated legacy result/execution paths, not retain dual routine/query routes |
| `internal/engine/snapshot.go` public primitive snapshot path | QUERY cutover retains useful immutable indexed capture internally, removes migrated public Snapshot/driver comparator path; routines use common typed results |
| `conn.go:35–46,51–110,119–123` parseOne/Prepare/execute/Exec/Query | One public statement (body internals are not public statements), one convergence. SESSION admits before parse/analysis. Prepared handle retains SQL/source/AST/frozen description/valid binding, no execution snapshot. Exec closes discarded rows; Query sole ownership transfer |
| `stmt.go:11–55` AST/count/NumInput/direct prepared adapters | Frozen parameter count/description and replaceable catalog-valid binding with independent execution state; same conn execution/result cleanup, no prepared-only route |
| `rows.go:12–60` parser.Column/Snapshot metadata/Next | QUERY RowResult.Schema/type-owned metadata, typed scratch and ExportValue, detached bytes, idempotent EOF/error/Close. Remove parser.Column/Snapshot public shim and primitive metadata switches |
| `result.go` count adapter | Preserve command-defined count; do not sum child routine writes or derive counts from OUT/RETURNING rows |
| `internal/engine/control.go:148–186` Restore parser/whitelist/stage.Execute | Migrate execution signature/admission/result cleanup under responsible SESSION/TX/control owner, preserving CREATE/INSERT-only whitelist, parameter rejection and atomic staged replacement. No new routine script runner |
| `internal/engine/engine_test.go` run/tuples; `internal/engine/catalog_test.go` direct Execute callers | Migrate signature/result consumers to typed schema/Next/export/close, no test-only legacy shim; preserve identity/replacement/concurrency assertions |
| `reference_test.go:23–135`, driver/control fixtures | Reuse full-field diagnostic constructors/direct-prepared metadata fixtures; preserve cancellation/close and old rows across native Reset/Restore. Existing tests are baseline evidence only |

Required future caller-search inventory includes `parser.Statement`, statement type switches, `Describe`, `.describe`, `Execute`, `prepareArgs`, `binding`, `Result.Columns`, `Snapshot`, `.Values(`, `newRows`, `NumInput`, `parser.Column`, `parameterColumn`, `Predicate`, generated-parser callers, `Finish`, `Operation.Done` and Exec-discard paths. Every migrated caller must use its owner's sole contract, including native staging/tests; delete obsolete aliases/reexports/comments and alternate paths. Preserve unrelated baseline/native storage/publication behavior. Later implementation scope comes from selected consumer acceptance, not license to rewrite all prerequisites at once.

## 10. Bounded stable-symbol consumers and extension boundaries

These are observed stable backlog symbols, not new catalog records/edges. Each row describes an independently bounded future input/output, owner/exclusion and exit evidence. Every implementation still reads its literal selected acceptance; this table is not evidence that capability is implemented. Shared dependencies are CATALOG/TYPE/EXPR/QUERY/SESSION/AUTH/TX and settings/diagnostics as required; DML/DDL/cursor/advisory capability dependencies are explicit in the relevant witnesses.

| Stable symbol | Consumer contract and boundary | Future evidence |
|---|---|---|
| PG18-ROUTINE-CORE | Single-SELECT typed positional LANGUAGE SQL scalar definition/call/conversion/metadata through shared frame; no full PL/procedure/security/set claim | W5/U1/U4 |
| PG18-ROUTINE-SQL-BODIES | Ordered multicommand/definition-parsed dependency body, **sequential deferred runtime command validation**, actual terminal result and preproduction policy; no binder/publication ownership | W1/W5d/W5e/W5g |
| PG18-ROUTINE-PARAMETERS | Signature modes/default/variadic mapping, typed slot initialization/coercion and staged copyback; TYPE/EXPR retains overload choice | W5/U3 |
| PG18-ROUTINE-RESULTS | Scalar/composite/set adapters, declaration-sensitive STRICT empty set, preproduction demand/common lifetime; PG18-QUERY-SRF-FROM retains relational scheduling | W5c–e/U4/U5 |
| PG18-ROUTINE-PROCEDURES | Definition/CALL, plain OUT row and procedural writable destinations; no SELECT procedure call or inherent right to commit | W3/W5 |
| PG18-ROUTINE-ATTRIBUTES | Declaration-sensitive STRICT, volatility view request, scoped AUTH/settings policy; no ACL/snapshot/physical planner engine | W5c/W9/U8 |
| PG18-PLPGSQL-CORE | DO, lexical declarations, params/$n/ALIAS, initialized scalar/constant/not-null/%TYPE/%ROWTYPE/record slots, basic assignment/RETURN; no dynamic/exception/TX claim | W4/W5/U1 |
| PG18-PLPGSQL-ASSIGNMENT | Scalar/row/array destination/coercion recipes; not another type system | W5/U3 |
| PG18-PLPGSQL-SQL-EXECUTION | First-reached static SQL/expressions/PERFORM, exact consumer-specific primary/auxiliary production policy and construct-local status through shared binder | W1/W5d/W5h/W9/U5 |
| PG18-PLPGSQL-SELECT-INTO | Static SELECT FirstRow/CardinalityProbe; **static modifying RETURNING TwoReturnedProbe even non-STRICT**, P0003/discarded assignment/TX rollback before unused third primary action; dynamic adaptation distinct | W5b/W5d/W5h P0002/P0003/U5 |
| PG18-PLPGSQL-DYNAMIC-SQL | Fresh EXECUTE/USING/INTO, quoted identifiers/no capture, unlimited primary production then STRICT-only cardinality, full ROW_COUNT/unchanged FOUND; distinct SQL EXECUTE and static modifying probe | W5b/W5f/W5h/U5 |
| PG18-PLPGSQL-EXCEPTIONS | TX marks/error match/rollback-release-before-handler/local retention, including failed two-returned modifying probe; not public savepoint aliases | W2/W4/W5h/U6 |
| PG18-PLPGSQL-RAISE-ASSERT | Structured RAISE/rethrow/notice/warning/ASSERT; diagnostic transport, no suppression | W4/U8 |
| PG18-PLPGSQL-DIAGNOSTICS | GET DIAGNOSTICS/call status and GET STACKED/nested active error record; no new error envelope | W4/W5 |
| PG18-PLPGSQL-TX-CONTROL | CALL/DO eligibility/rotation/CHAIN/context blockers and cursor-owner coordination; no end/publication/lock algorithms | W3/W8/U7 |
| PG18-PLPGSQL-CONTROL-CONDITIONAL | Branch instructions/reached expression SQL on lexical coordinates; no branch transaction | W1 |
| PG18-PLPGSQL-CONTROL-LOOP | Labeled loop/EXIT/CONTINUE/cancellation transfer; shared slots, no invocation per iteration | W5 control/U1/U2 |
| PG18-PLPGSQL-CONTROL-INTEGER-FOR | Captured integer bounds/step and loop-local iteration slot | W5 control/U1/U3 |
| PG18-PLPGSQL-CONTROL-WHILE | Reached condition using shared expression analysis/control transfer | W5 control/U1/U2 |
| PG18-PLPGSQL-QUERY-LOOPS | Common cursor-to-local-row slots/control transfer/close; no private store | W5 control/U3/U4 |
| PG18-PLPGSQL-ARRAY-FOREACH | Typed element/slice-to-slot/control transfer; TYPE owns array representation | W5 control/U3 |
| PG18-PLPGSQL-CURSORS | SESSION portal handles/OPEN-time parameters/close/TX end and loop holdability; no escaped statement lease | W3 cursor/U4/U7 |
| PG18-ROUTINE-TRIGGER-CORE | BEFORE row/statement contextual entry/result interpretation and real atomic DML | W6/W10 |
| PG18-ROUTINE-TRIGGER-AFTER | After-final-image dispatch/ignored result, DML scheduling retained | W6 extensions |
| PG18-ROUTINE-TRIGGER-INSTEAD-OF | View handled/skip/count adapter with substantive underlying writes | W6 extensions |
| PG18-ROUTINE-TRIGGER-DEFERRED | Immutable image/ref/role queue, fresh firing/TX marks; no retained mutable frame | W10/U4/U6 |
| PG18-ROUTINE-TRIGGER-TRANSITION-TABLES | Manager-owned set-image provider/lifetime, no last-row slot borrowing | W6 extensions/U4/U6 |
| PG18-ROUTINE-TRIGGER-DDL | Install/alter/drop descriptor and definition ACLs, not runtime dispatcher | W10 |
| PG18-ROUTINE-EVENT-TRIGGER-CORE | DDL start/end dispatch/filter/context/trusted entry/real rollback | W7/W10 |
| PG18-ROUTINE-EVENT-TRIGGER-DDL | Definition/alter/drop/enable, privileged validation/excluded DDL owner | W7/W10 |
| PG18-ROUTINE-EVENT-TRIGGER-DDL-COMMANDS | Valid-context collected command projection, not catalog mutation | W7 extensions |
| PG18-ROUTINE-EVENT-TRIGGER-DROPPED-OBJECTS | Removed-object immutable evidence, not live lookup | W7 extensions |
| PG18-ROUTINE-EVENT-TRIGGER-REWRITE | Before-rewrite table/reason contextual projection | W7 extensions |
| PG18-ROUTINE-EVENT-TRIGGER-OBJECT-ADDRESS | Address projection/resolution via manager/catalog owner | W7 extensions |
| PG18-ROUTINE-EVENT-TRIGGER-BUILTINS | Separate valid-context helper projections, no writable helper catalog | W7 extensions |
| PG18-ROUTINE-LANGUAGES | Supported registry/validator dispatch, nonexecuting definition validation versus sequential deferred SQL runtime checks; no external runtime ABI | W1/W5e/W5g |
| PG18-ADMIN-SETTINGS-CHECK-FUNCTION-BODIES | Definition validator timing/no execution versus deferred runtime reached-command/terminal validation; not eager all PL semantics | W1/W5e/W5g |
| PG18-ROUTINE-DROP | Catalog lifecycle/dependency/ref invalidation, not routine cache | W9 |
| PG18-ROUTINE-RENAME-OWNER | Catalog owner/ref/auth revision changes; next call uses current owner | W9 |

No ordinary `PG18-FUNC-*` builtin/operator, external language runtime, transform or aggregate implementation acquires an accidental architecture prerequisite from this table. Smallest routine core must deliver its public behavior, not a scaffold depending on every later language feature.

## 11. Independent future witnesses

### Common execution/evidence rules

All SQL below is **future/unexecuted**. Run each top-level command as a separate public request; body commands stay within their routine request. Use fresh schemas/fixtures for independent rounds, privileged setup only where required, UTF8/C, and direct/prepared variants of eligible invocations. Use held physical sql.Conn A/B for session schedules; READ COMMITTED unless stated otherwise. Rows without ORDER BY are bags. Expected INT4 exports use the public integer adapter, TEXT exports strings and SQL NULL exports nil. Logical metadata must match the named outputs/declared types; do not infer NOT NULL from a particular value.

Error fixtures assert severity ERROR, supplied primary/detail/hint and originating code, then capture exact optional field presence/absence and Context/InternalQuery/InternalPosition/character positions against the pinned oracle with body formatting fixed. Unobserved context strings/line numbers/helper diagnostics are not invented here. This is a required future-consumer fixture step, not permission for SQLSTATE-only acceptance. Native C source fields remain absent. Unexpected successful SQL, leaked private effects or changed retained rows fail independently of parity.

### W1 — Deferred embedded semantic validation versus definition-time SQL body

```sql
CREATE FUNCTION lazy_name(take_bad boolean) RETURNS int LANGUAGE plpgsql AS $$
BEGIN
 IF take_bad THEN PERFORM absent_col FROM absent_relation; END IF;
 RETURN 7;
END $$;
SELECT lazy_name(false);
SELECT lazy_name(true);
CREATE FUNCTION early_name() RETURNS int LANGUAGE SQL
BEGIN ATOMIC SELECT absent_col FROM absent_relation; END;
CREATE TABLE lazy_types(i int);
CREATE FUNCTION lazy_type(take_bad boolean) RETURNS int LANGUAGE plpgsql AS $$
BEGIN
 IF take_bad THEN PERFORM i + true FROM lazy_types; END IF;
 RETURN 7;
END $$;
SELECT lazy_type(false);
SELECT lazy_type(true);
CREATE FUNCTION lazy_column(take_bad boolean) RETURNS int LANGUAGE plpgsql AS $$
BEGIN
 IF take_bad THEN PERFORM absent_col FROM lazy_types; END IF;
 RETURN 7;
END $$;
SELECT lazy_column(false);
SELECT lazy_column(true);
```

Prerequisite: absent_relation does not exist; default check_function_bodies is on. All PL definitions succeed; false returns INT4 7. First reached name branch fails `42P01`, `relation "absent_relation" does not exist`. Reached column branch fails `42703`, `column "absent_col" does not exist`; type branch fails `42883`, `operator does not exist: integer + boolean`, even though lazy_types is empty. SQL-standard early definition fails `42P01` before installation; `SELECT to_regprocedure('early_name()');` returns NULL. PL lazy_name remains installed after its failed invocation. Syntax is valid in every branch. Dependency/validation/default-setting consumers are required; no local-source runtime claim.

### W2 — Protected write rollback, surviving locals and uncaught failure

```sql
CREATE TABLE ex_log(i int);
CREATE FUNCTION caught() RETURNS int LANGUAGE plpgsql AS $$
DECLARE x int := 0;
BEGIN
 INSERT INTO ex_log VALUES(1);
 BEGIN
  INSERT INTO ex_log VALUES(2); x := 9;
  RAISE EXCEPTION USING ERRCODE='22012', MESSAGE='inner failure';
 EXCEPTION WHEN division_by_zero THEN
  IF SQLSTATE <> '22012' OR SQLERRM <> 'inner failure' THEN
   RAISE EXCEPTION 'lost diagnostics';
  END IF;
 END;
 RETURN x;
END $$;
SELECT caught();
SELECT i FROM ex_log ORDER BY i;
CREATE FUNCTION uncaught() RETURNS void LANGUAGE plpgsql AS $$
BEGIN INSERT INTO ex_log VALUES(3); RAISE EXCEPTION 'uncaught'; END $$;
SELECT uncaught();
SELECT i FROM ex_log ORDER BY i;
```

Expected caught INT4 9, ex_log [1], never 2; uncaught `P0001`/`uncaught`, still [1]. Fresh fixture schedule: A `BEGIN; SELECT caught();` sees own [1]; B SELECT sees empty; A COMMIT; B next SELECT sees [1]. Explicit uncaught variant: A BEGIN, INSERT 4, SELECT uncaught (P0001), ordinary SELECT fails `25P02`/`current transaction is aborted, commands ignored until end of transaction block`; ROLLBACK removes private 4 and 3, not committed 1. Separate A BEGIN/INSERT 4/SAVEPOINT s/uncaught/ROLLBACK TO s/SELECT sees [1,4]; COMMIT makes 4 visible. TX/public savepoint capability is separate from unnamed PL marks.

### W3 — Eligible transaction end and contextual blockers

```sql
CREATE TABLE tx_log(i int);
CREATE PROCEDURE rotate() LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO tx_log VALUES(1); COMMIT;
 INSERT INTO tx_log VALUES(2); ROLLBACK;
 INSERT INTO tx_log VALUES(3);
END $$;
CALL rotate();
SELECT i FROM tx_log ORDER BY i;
DO $$BEGIN INSERT INTO tx_log VALUES(4); COMMIT; END$$;
SELECT i FROM tx_log ORDER BY i;
CREATE PROCEDURE relay() LANGUAGE plpgsql AS $$BEGIN CALL rotate(); END$$;
CALL relay();
CREATE FUNCTION bridge() RETURNS void LANGUAGE plpgsql AS $$BEGIN CALL rotate(); END$$;
CREATE PROCEDURE blocked_bridge() LANGUAGE plpgsql AS $$BEGIN PERFORM bridge(); END$$;
CALL blocked_bridge();
CREATE PROCEDURE protected_commit() LANGUAGE plpgsql AS $$
BEGIN
 BEGIN COMMIT; EXCEPTION WHEN division_by_zero THEN NULL; END;
END$$;
CALL protected_commit();
CREATE PROCEDURE protected_rollback() LANGUAGE plpgsql AS $$
BEGIN
 BEGIN ROLLBACK; EXCEPTION WHEN division_by_zero THEN NULL; END;
END$$;
CALL protected_rollback();
CREATE PROCEDURE handler_commit() LANGUAGE plpgsql AS $$
BEGIN
 BEGIN RAISE division_by_zero;
 EXCEPTION WHEN division_by_zero THEN INSERT INTO tx_log VALUES(5); COMMIT;
 END;
END$$;
CALL handler_commit();
```

Standalone rotate gives [1,3], DO adds 4, uninterrupted relay adds another 1 and 3. Bridge rejects first COMMIT `2D000`/`invalid transaction termination`, adds no row. Otherwise-top-level protected calls reject `2D000`/`cannot commit while a subtransaction is active` and rollback analog. Handler_commit adds committed 5 because its mark has been released, assuming no other blocker. Expected final bag ordered [1,1,3,3,4,5]. An explicit `BEGIN; CALL rotate();` fails first COMMIT atomic primary, then ROLLBACK leaves **unchanged prior bag**, not an empty table. Repeat independently with SECURITY DEFINER rotate or `SET search_path = public, pg_temp` procedure attributes: end denied by atomic restriction/no added rows.

CHAIN witness, independent session defaults READ COMMITTED: a top-level procedure `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ; INSERT ...; COMMIT AND CHAIN;` reads `current_setting('transaction_isolation')` as `repeatable read` into a log; ordinary COMMIT instead reads new default `read committed`. Public outer transaction is never used to make a top-level procedure eligible. Cursor extension: create rows 1,2; procedure FOR r IN SELECT i ORDER BY i loops INSERT i and COMMIT; expect [1,2] with no invalid cursor after rotation. Replace cursor source with UPDATE ... RETURNING: first transaction command rejected by its cursor-specific PG diagnostic, whose exact fields are the cursor/TX consumer's pinned fixture obligation.

### W4 — Nested unwind, caller state and originating diagnostics

```sql
CREATE FUNCTION err_leaf() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION USING ERRCODE='22012', MESSAGE='origin',
  DETAIL='leaf detail', HINT='leaf hint';
END $$;
CREATE FUNCTION caller_state() RETURNS int LANGUAGE plpgsql AS $$
DECLARE x int := 11; s text; m text; d text; h text; c text;
BEGIN
 PERFORM 1;
 BEGIN PERFORM err_leaf();
 EXCEPTION WHEN division_by_zero THEN
  GET STACKED DIAGNOSTICS s=RETURNED_SQLSTATE, m=MESSAGE_TEXT,
   d=PG_EXCEPTION_DETAIL, h=PG_EXCEPTION_HINT, c=PG_EXCEPTION_CONTEXT;
  IF s <> '22012' OR m <> 'origin' OR d <> 'leaf detail' OR h <> 'leaf hint'
   OR NOT FOUND OR x <> 11 THEN RAISE EXCEPTION 'lost caller state'; END IF;
 END;
 RETURN x;
END $$;
SELECT caller_state();
SELECT err_leaf();
CREATE FUNCTION err_middle() RETURNS void LANGUAGE plpgsql AS $$BEGIN PERFORM err_leaf(); END$$;
SELECT err_middle();
CREATE FUNCTION rethrow() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 BEGIN PERFORM err_leaf(); EXCEPTION WHEN division_by_zero THEN RAISE; END;
END$$;
SELECT rethrow();
DO $$BEGIN BEGIN ASSERT false; EXCEPTION WHEN OTHERS THEN NULL; END; END$$;
```

Caller returns 11 and preserves FOUND from its preceding PERFORM. Uncaught/rethrow failures preserve `22012`/origin/detail/hint, including leaf context then actual caller occurrences; direct err_leaf has no invented middle context. ASSERT bypasses OTHERS with `P0004`/`assertion failed`. Nested handlers must restore previous active stacked diagnostic; U6 injects distinct outer/inner error records to assert exact restoration and bare rethrow identity. Cleanup error injection must not replace SQL error; otherwise-success cleanup failure must prevent output.

### W5 — Parameters, results, dynamic SQL, ambiguity and control adapters

```sql
CREATE FUNCTION plus_default(a int,b int DEFAULT 4) RETURNS int LANGUAGE SQL AS $$SELECT a+b$$;
SELECT plus_default(3);
CREATE FUNCTION out_pair(a int,OUT x int,OUT y text) LANGUAGE SQL AS $$SELECT a,a::text$$;
SELECT * FROM out_pair(5);
CREATE PROCEDURE triple(INOUT x int) LANGUAGE plpgsql AS $$BEGIN x:=x*3; END$$;
CALL triple(5);
DO $$DECLARE n int:=5; BEGIN CALL triple(n);
 IF n<>15 THEN RAISE EXCEPTION 'bad copyback'; END IF; END$$;
CREATE FUNCTION empty_scalar() RETURNS int LANGUAGE SQL AS $$SELECT 1 WHERE false$$;
CREATE FUNCTION empty_set() RETURNS SETOF int LANGUAGE SQL AS $$SELECT 1 WHERE false$$;
SELECT empty_scalar();
SELECT * FROM empty_set();
CREATE TABLE body_log(i int);
CREATE FUNCTION sql_sequence() RETURNS int LANGUAGE SQL AS $$INSERT INTO body_log VALUES(8); SELECT 9$$;
SELECT sql_sequence();
SELECT i FROM body_log ORDER BY i;
CREATE FUNCTION late_set_error() RETURNS SETOF int LANGUAGE plpgsql AS $$
BEGIN RETURN NEXT 1; RETURN NEXT 2; RAISE EXCEPTION 'late set'; END$$;
SELECT * FROM late_set_error();
```

Expected: plus_default INT4 7; out_pair columns x INT4/y TEXT=(5,'5'); CALL x INT4=15, DO no relation; empty_scalar one NULL INT4, empty_set zero rows with INT4 schema; sql_sequence returns 9/log [8]; late_set_error `P0001`/`late set`, **no public rows**. Function OUT needs no dummy input. Add a plain-OUT procedure with `(OUT x int)` and `CALL p(NULL)` to assert one output row; procedural CALL uses writable x. For failed copyback use procedure `x:=99; RAISE EXCEPTION 'failed call';`, handler around CALL with n=5; assert n remains 5.

#### W5c — STRICT scalar/composite NULL versus empty typed SETOF/TABLE

```sql
CREATE FUNCTION strict_scalar(x int) RETURNS int LANGUAGE SQL STRICT AS $$SELECT x$$;
CREATE FUNCTION strict_set(x int) RETURNS SETOF int LANGUAGE SQL STRICT AS $$SELECT x$$;
SELECT strict_scalar(NULL::int);
SELECT * FROM strict_set(NULL::int);
SELECT * FROM strict_set(7);
CREATE FUNCTION strict_table(x int) RETURNS TABLE(v int,label text)
 LANGUAGE SQL STRICT AS $$SELECT x,x::text$$;
SELECT * FROM strict_table(NULL::int);
SELECT * FROM strict_table(7);
CREATE TYPE strict_pair AS (v int,label text);
CREATE FUNCTION strict_composite(x int) RETURNS strict_pair
 LANGUAGE SQL STRICT AS $$SELECT x,x::text$$;
SELECT strict_composite(NULL::int) IS NULL AS skipped;
```

Independent expectations: strict_scalar target has one INT4 row NULL; strict_set(NULL) full INT4 schema **zero rows**, strict_set(7) one row7. strict_table(NULL) has columns v INT4/label TEXT and **zero rows**, strict_table(7) row (7,'7'). Nonset strict_composite skip is a typed composite NULL, so skipped BOOL=true. Composite TYPE and FROM-SRF capabilities are explicit future dependencies. No skipped body runs; ordinary callable authorization still precedes completion, including retained prepared calls after revoke. U4 checks schema/zero occurrences rather than a manufactured NULL set row; U8 retains ordinary authorization on a skip.

#### W5d — First-row SELECT demand versus complete SQL-function final DML

```sql
CREATE SEQUENCE scalar_seq START 1;
CREATE FUNCTION scalar_tick() RETURNS bigint LANGUAGE SQL VOLATILE AS $$
 SELECT nextval('scalar_seq') FROM generate_series(1,2);
$$;
SELECT scalar_tick();
SELECT currval('scalar_seq');
CREATE TABLE complete_t(i int);
CREATE FUNCTION scalar_insert() RETURNS int LANGUAGE SQL VOLATILE AS $$
 INSERT INTO complete_t VALUES(1),(2) RETURNING i;
$$;
SELECT scalar_insert();
SELECT i FROM complete_t ORDER BY i;
CREATE TABLE complete_fail_t(i int CONSTRAINT positive_first CHECK(i<2));
CREATE FUNCTION scalar_insert_fail() RETURNS int LANGUAGE SQL VOLATILE AS $$
 INSERT INTO complete_fail_t VALUES(1),(2) RETURNING i;
$$;
SELECT scalar_insert_fail();
SELECT i FROM complete_fail_t ORDER BY i;
```

Expected scalar_tick INT8=1 and currval INT8=1: the eligible final SELECT does not demand the second nextval. Sequence/generate_series capabilities are explicit future dependencies; sequence effects are nontransactional, not claimed rollbackable. scalar_insert INT4=1 for this specified direct VALUES producer, complete_t [1,2]; internal producer count2 if observed. This producer-specific expectation is not a portable unordered first-row promise for arbitrary SQL. scalar_insert_fail rejects the second row with `23514`, primary `new row for relation "complete_fail_t" violates check constraint "positive_first"`; no public scalar and complete_fail_t empty despite a first candidate. Capture constraint/detail/position fields independently in its future DML fixture.

INTO demand contrast, fresh sequence so unwanted third-row work is observable:

```sql
CREATE SEQUENCE probe_seq START 1;
DO $$DECLARE n bigint;
BEGIN
 BEGIN
  SELECT nextval('probe_seq') INTO STRICT n FROM generate_series(1,3);
 EXCEPTION WHEN too_many_rows THEN
  IF currval('probe_seq')<>2 THEN RAISE EXCEPTION 'drained unused third row'; END IF;
 END;
END$$;
SELECT currval('probe_seq');
DO $$DECLARE n int;
BEGIN INSERT INTO complete_t VALUES(3),(4) RETURNING i INTO n; END$$;
SELECT i FROM complete_t ORDER BY i;
```

The **static** STRICT ordinary SELECT proves too-many with a second occurrence; handler succeeds, currval=2 INT8, never3. **Static** INTO multirow DML RETURNING also uses a two-returned primary probe, failing `P0003`/`query returned more than one row`; complete_t stays [1,2] through failed-command rollback. This two-candidate example cannot distinguish bounded versus unlimited modifying production; W5h adds a third candidate to make the difference observable. Full scalar SQL-function final DML and dynamic production remain distinct. U5 verifies pre-open classification, required auxiliary completion, stopped primary production and no externally successful partial writes.

#### W5e — Actual final statement and validator-controlled failure timing

Default validation on, fresh fixture:

```sql
CREATE TABLE terminal_t(i int);
CREATE FUNCTION wrong_last() RETURNS int LANGUAGE SQL AS $$
 SELECT 7;
 INSERT INTO terminal_t VALUES(1);
$$;
SELECT to_regprocedure('wrong_last()');
SELECT i FROM terminal_t ORDER BY i;
```

CREATE fails **42P13**, primary **`return type mismatch in function declared to return integer`**, detail **`Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING.`** to_regprocedure is NULL and terminal_t empty; validation did not execute either statement. Never accept the earlier7 as the function result.

Separate fresh variant (same SQL issued as separate public commands):

```sql
CREATE TABLE terminal_t(i int);
SET check_function_bodies=off;
CREATE FUNCTION wrong_last() RETURNS int LANGUAGE SQL AS $$
 SELECT 7;
 INSERT INTO terminal_t VALUES(1);
$$;
SELECT wrong_last();
SELECT i FROM terminal_t ORDER BY i;
RESET check_function_bodies;
```

Text-body CREATE succeeds with validation suppressed; runtime can execute the earlier SELECT7, then rejects actual-final-command shape at terminal preparation/return validation with corresponding 42P13/primary/detail. It never **returns**7 as the function result or executes the terminal INSERT; table empty. This side-effect-free earlier SELECT does not prove whole-body runtime validation before execution; W5g supplies the distinguishing sequence witness. Cleanup resets the setting even on fixture failure. SQL-standard bodies remain definition-analyzed. Exact optional fields/context differences between CREATE and invocation are required future pinned fixtures, not guessed.

Void/procedure contrast, independent tables:

```sql
CREATE TABLE terminal_void_t(i int);
CREATE FUNCTION valid_void() RETURNS void LANGUAGE SQL AS $$
 SELECT 7; INSERT INTO terminal_void_t VALUES(1);
$$;
CREATE PROCEDURE valid_terminal_proc() LANGUAGE SQL AS $$
 SELECT 7; INSERT INTO terminal_void_t VALUES(2);
$$;
SELECT valid_void();
CALL valid_terminal_proc();
SELECT i FROM terminal_void_t ORDER BY i;
```

Both definitions/invocations succeed, terminal_void_t [1,2]; the function supplies its declared VOID result, not INT4=7, and CALL without OUT has no row relation. U5 distinguishes nonexecuting definition validation from sequential deferred runtime preparation, rejects an invalid terminal command before **that command**, prevents earlier-result fallback and applies ordinary transactional rollback to earlier writes; void/procedure validation uses its own declared contract.

#### W5f — Dynamic full production versus static bounded probes

```sql
CREATE SEQUENCE dynamic_probe_seq START 1;
DO $$DECLARE n bigint;
BEGIN
 BEGIN
  EXECUTE $q$SELECT nextval('dynamic_probe_seq') FROM generate_series(1,3)$q$ INTO STRICT n;
 EXCEPTION WHEN too_many_rows THEN NULL;
 END;
END$$;
SELECT currval('dynamic_probe_seq');
CREATE TABLE dynamic_return_t(i int);
DO $$DECLARE n int; c bigint;
BEGIN
 PERFORM 1;
 EXECUTE 'INSERT INTO dynamic_return_t VALUES(1),(2) RETURNING i' INTO n;
 GET DIAGNOSTICS c=ROW_COUNT;
 IF c<>2 OR NOT FOUND OR n NOT IN (1,2) THEN RAISE EXCEPTION 'lost dynamic result/status'; END IF;
END$$;
SELECT i FROM dynamic_return_t ORDER BY i;
```

Dynamic STRICT SELECT produces all three occurrences before P0003, caught by the block; currval INT8=**3**, unlike static W5d's INT8=**2**. Sequence/generate_series capabilities are explicit future dependencies and sequence effects are nontransactional. Dynamic non-STRICT DML RETURNING succeeds, stores ordered [1,2], assigns first returned candidate, reports ROW_COUNT=2 and preserves FOUND=true from PERFORM1. There is **no P0003** just because this non-STRICT dynamic modifying command returns two rows; do not invent a portable unordered first-row identity beyond candidate membership.

Additional complete dynamic cases, each request separate:

```sql
CREATE SEQUENCE dynamic_plain_seq START 1;
DO $$DECLARE n bigint;
BEGIN EXECUTE $q$SELECT nextval('dynamic_plain_seq') FROM generate_series(1,3)$q$ INTO n;
 IF n<>1 THEN RAISE EXCEPTION 'bad first dynamic occurrence'; END IF;
END$$;
SELECT currval('dynamic_plain_seq');
CREATE SEQUENCE dynamic_discard_seq START 1;
DO $$BEGIN EXECUTE $q$SELECT nextval('dynamic_discard_seq') FROM generate_series(1,3)$q$; END$$;
SELECT currval('dynamic_discard_seq');
DO $$DECLARE n int;
BEGIN EXECUTE 'INSERT INTO dynamic_return_t VALUES(3),(4) RETURNING i' INTO STRICT n; END$$;
SELECT i FROM dynamic_return_t ORDER BY i;
DO $$DECLARE n int:=9;
BEGIN EXECUTE 'SELECT 1 WHERE false' INTO n;
 IF n IS NOT NULL THEN RAISE EXCEPTION 'bad empty dynamic assignment'; END IF;
END$$;
DO $$DECLARE n int; BEGIN EXECUTE 'SELECT 1 WHERE false' INTO STRICT n; END$$;
```

Non-STRICT dynamic SELECT fully produces three, assigns its first sequence occurrence1, currval=3; no-INTO dynamic SELECT also currval=3, not early discard. Dynamic STRICT DML produces its two returned occurrences then fails P0003/`query returned more than one row`; no successful destination/output, transactional inserts3/4 rolled back, prior [1,2] unchanged. Non-STRICT empty assigns NULL; STRICT empty P0002/`query returned no rows`. Future exact diagnostic fields and source positions are captured independently. U5 spies on all three dynamic occurrences **before** cardinality checking, static second-row stopping, strict-only dynamic >1 failure, complete count/status and cleanup.

#### W5g — Deferred sequential terminal validation and earlier side effects

```sql
CREATE SEQUENCE terminal_validation_seq START 1;
CREATE TABLE terminal_validation_t(i int);
SET check_function_bodies=off;
CREATE FUNCTION late_terminal_validation() RETURNS int LANGUAGE SQL VOLATILE AS $$
 SELECT nextval('terminal_validation_seq');
 INSERT INTO terminal_validation_t VALUES(1);
$$;
SELECT late_terminal_validation();
SELECT currval('terminal_validation_seq');
SELECT i FROM terminal_validation_t ORDER BY i;
RESET check_function_bodies;
```

CREATE succeeds. Standalone invocation fails **42P13**, primary `return type mismatch in function declared to return integer`, detail `Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING.` The earlier SELECT executed: currval INT8=**1** persists nontransactionally through failure. The invalid terminal INSERT never executes and the table is empty; the earlier sequence result is never the function result. Sequence/settings capabilities are explicit dependencies, not routine-owned implementations. Use standalone failure requests for subsequent inspection; an explicit outer transaction instead follows 25P02 until rollback/recovery, so inspect currval after recovery.

Fresh default-validation contrast with independent sequence/session:

```sql
CREATE SEQUENCE early_terminal_seq START 1;
CREATE TABLE early_terminal_t(i int);
CREATE FUNCTION early_terminal_validation() RETURNS int LANGUAGE SQL VOLATILE AS $$
 SELECT nextval('early_terminal_seq');
 INSERT INTO early_terminal_t VALUES(1);
$$;
SELECT to_regprocedure('early_terminal_validation()');
SELECT currval('early_terminal_seq');
SELECT i FROM early_terminal_t ORDER BY i;
```

With check_function_bodies on, CREATE rejects with the same 42P13/primary/detail; no body command runs, to_regprocedure NULL/table empty. currval is unset in this session and fails `55000`, primary `currval of sequence "early_terminal_seq" is not yet defined in this session`; never invent an allocator-zero result.

Transactional earlier-effects contrast under disabled text checking:

```sql
CREATE TABLE earlier_tx_t(i int);
CREATE TABLE invalid_terminal_t(i int);
SET check_function_bodies=off;
CREATE FUNCTION earlier_transactional_write() RETURNS int LANGUAGE SQL VOLATILE AS $$
 INSERT INTO earlier_tx_t VALUES(1);
 SELECT 7;
 INSERT INTO invalid_terminal_t VALUES(2);
$$;
SELECT earlier_transactional_write();
SELECT i FROM earlier_tx_t ORDER BY i;
SELECT i FROM invalid_terminal_t ORDER BY i;
RESET check_function_bodies;
```

Invocation 42P13 at terminal check; earlier insert executes privately and is rolled back by statement failure, both tables empty, terminal insert never executed. Table emptiness alone cannot establish that no earlier command ran. U5 instruments reached-command order (earlier execution, terminal validation failure, no terminal execution/no output) and TX rollback, not sequence undo. Fixture cleanup resets settings on every path.

#### W5h — Static modifying RETURNING two-occurrence probe and failure precedence

```sql
CREATE SEQUENCE static_return_seq START 1;
CREATE TABLE static_return_t(i int);
DO $$DECLARE n bigint:=99;
BEGIN
 BEGIN
  INSERT INTO static_return_t VALUES(1),(2),(3)
   RETURNING nextval($s$static_return_seq$s$) INTO n;
 EXCEPTION WHEN too_many_rows THEN
  IF n<>99 THEN RAISE EXCEPTION 'partial static assignment'; END IF;
 END;
END$$;
SELECT currval($s$static_return_seq$s$);
SELECT i FROM static_return_t ORDER BY i;
```

Static non-STRICT modifying INTO probes two returned occurrences, then P0003/`query returned more than one row`, caught here. Expected currval INT8=**2**, table empty from protected transactional rollback, destination remains99. No unused third primary action/RETURNING is forced by completion. Fresh STRICT static variant adds STRICT to the same INTO and has the same two-occurrence result. Sequences are explicit future dependencies; their advances are nontransactional and not undone by rollback.

Corresponding fresh dynamic round:

```sql
CREATE SEQUENCE dynamic_return_seq START 1;
CREATE TABLE dynamic_return_three_t(i int);
DO $$DECLARE n bigint:=99;
BEGIN
 BEGIN
  EXECUTE $q$INSERT INTO dynamic_return_three_t VALUES(1),(2),(3)
   RETURNING nextval('dynamic_return_seq')$q$ INTO STRICT n;
 EXCEPTION WHEN too_many_rows THEN
  IF n<>99 THEN RAISE EXCEPTION 'partial dynamic assignment'; END IF;
 END;
END$$;
SELECT currval('dynamic_return_seq');
SELECT i FROM dynamic_return_three_t ORDER BY i;
```

Dynamic full production reaches **3** sequence occurrences before P0003; currval INT8=3, table empty, n remains99. Fresh non-STRICT dynamic variant succeeds/stores [1,2,3], assigns first returned candidate and ROW_COUNT=3. This retains W5f, not a static probe in disguise.

Failure-precedence contrast, separate public requests:

```sql
CREATE TABLE static_return_check_t(i int CHECK(i<3));
DO $$DECLARE n int;
BEGIN
 INSERT INTO static_return_check_t VALUES(1),(2),(3) RETURNING i INTO n;
END$$;
SELECT i FROM static_return_check_t ORDER BY i;
DO $$DECLARE n int;
BEGIN
 EXECUTE 'INSERT INTO static_return_check_t VALUES(1),(2),(3) RETURNING i' INTO STRICT n;
END$$;
SELECT i FROM static_return_check_t ORDER BY i;
CREATE FUNCTION complete_check_insert() RETURNS int LANGUAGE SQL VOLATILE AS $$
 INSERT INTO static_return_check_t VALUES(1),(2),(3) RETURNING i;
$$;
SELECT complete_check_insert();
SELECT i FROM static_return_check_t ORDER BY i;
```

Static DO fails **P0003, not23514**, because the unused third primary action/check is not reached before second-returned cardinality failure; table empty. Dynamic STRICT DO and complete SQL-function final DML reach third check and fail **23514**, before any dynamic postproduction cardinality adaptation/successful scalar return; table empty. Exact check-constraint English diagnostics/optional fields are required future pinned fixtures, not fabricated from generic object refs. In an explicit transaction recover from 25P02 before inspection; standalone failing requests leave subsequent inspection usable.

Positive full-production boundaries, fresh independent sequences:

```sql
CREATE SEQUENCE static_plain_return_seq START 1;
CREATE TABLE static_plain_return_t(i bigint);
DO $$BEGIN
 INSERT INTO static_plain_return_t VALUES
  (nextval('static_plain_return_seq')),(nextval('static_plain_return_seq')),(nextval('static_plain_return_seq'));
END$$;
SELECT currval('static_plain_return_seq');
SELECT i FROM static_plain_return_t ORDER BY i;
CREATE SEQUENCE sql_full_return_seq START 1;
CREATE TABLE sql_full_return_t(i int);
CREATE FUNCTION sql_full_return() RETURNS bigint LANGUAGE SQL VOLATILE AS $$
 INSERT INTO sql_full_return_t VALUES(1),(2),(3) RETURNING nextval('sql_full_return_seq');
$$;
SELECT sql_full_return();
SELECT currval('sql_full_return_seq');
SELECT i FROM sql_full_return_t ORDER BY i;
```

Static no-INTO plain INSERT executes all three: currval3 INT8/stored [1,2,3]. It deliberately has **no RETURNING**, since static PL result data without a destination is not a successful discard route. SQL-function final DML returns its first sequence value1 INT8 but completes all three, currval3/stored [1,2,3]. U5 separately instruments required secondary modifying work/cleanup versus stopped primary producer, P0003 before third primary error, destination discard and TX rollback; it cannot authorize successful partial DML publication.

#### W5b — Dynamic SQL, cardinality, ambiguity and control continuation

```sql
CREATE TABLE "odd table"(v text);
DO $$DECLARE val text:='x''; DROP TABLE nope; --';
BEGIN EXECUTE format('INSERT INTO %I(v) VALUES($1)','odd table') USING val; END$$;
SELECT v FROM "odd table";
DO $$DECLARE v int; BEGIN EXECUTE 'SELECT $1' INTO v USING 9;
 IF v<>9 THEN RAISE EXCEPTION 'bad binding'; END IF; END$$;
DO $$BEGIN EXECUTE 'SELECT * FROM $1' USING 'odd table'; END$$;
DO $$DECLARE private_name int:=9; BEGIN EXECUTE 'SELECT private_name'; END$$;
DO $$DECLARE v int; n bigint;
BEGIN
 PERFORM 1;
 EXECUTE 'SELECT 1 WHERE false' INTO v;
 GET DIAGNOSTICS n=ROW_COUNT;
 IF NOT FOUND OR n<>0 OR v IS NOT NULL THEN RAISE EXCEPTION 'bad dynamic status'; END IF;
END$$;
CREATE TABLE cardinal(i int);
DO $$DECLARE v int; BEGIN SELECT i INTO v FROM cardinal;
 IF v IS NOT NULL THEN RAISE EXCEPTION 'bad empty INTO'; END IF; END$$;
DO $$DECLARE v int; BEGIN SELECT i INTO STRICT v FROM cardinal; END$$;
INSERT INTO cardinal VALUES(1),(2);
DO $$DECLARE v int; BEGIN SELECT i INTO STRICT v FROM cardinal; END$$;
DO $$DECLARE v int; BEGIN UPDATE cardinal SET i=i+1 RETURNING i INTO v; END$$;
SELECT i FROM cardinal ORDER BY i;
```

Odd-table row is literal `x'; DROP TABLE nope; --`, no attempted DROP. USING value yields 9; identifier substitution fails `42601`, `syntax error at or near "$1"`; no lexical capture fails `42703`, `column "private_name" does not exist`. Dynamic zero-row status preserves FOUND=true, ROW_COUNT=0, v=NULL. The **static** STRICT SELECT INTO commands fail empty `P0002`/`query returned no rows`, two rows `P0003`/`query returned more than one row`. The **static** multirow UPDATE RETURNING INTO fails P0003 even without STRICT; failed UPDATE leaves cardinal [1,2]. Dynamic multirow DML RETURNING without STRICT instead succeeds as W5f specifies. Hints/detail/positions are captured per command, not assumed identical.

```sql
CREATE TABLE ambiguity(v int);
INSERT INTO ambiguity VALUES(3);
CREATE FUNCTION sql_column(v int) RETURNS int LANGUAGE SQL AS $$SELECT v FROM ambiguity$$;
SELECT sql_column(9);
CREATE FUNCTION pl_conflict(v int) RETURNS int LANGUAGE plpgsql AS $$
DECLARE result int;
BEGIN SELECT v INTO result FROM ambiguity; RETURN result; END$$;
SELECT pl_conflict(9);
CREATE FUNCTION pl_qualified(v int) RETURNS int LANGUAGE plpgsql AS $$
DECLARE result int;
BEGIN SELECT pl_qualified.v INTO result FROM ambiguity; RETURN result; END$$;
SELECT pl_qualified(9);
```

SQL returns 3; reached default PL conflict `42702`/`column reference "v" is ambiguous`; qualified PL returns 9. Separate fresh compilations with per-function `#variable_conflict use_variable`/`use_column` return 9/3; changing global setting does not rewrite a previously compiled statement.

Control extension SQL, independently named functions returning SETOF int: LOOP `i:=i+1; EXIT WHEN i>2; RETURN NEXT i;` from i=0; integer FOR i IN 1..2; WHILE i<2 increment/RETURN NEXT; query FOR r IN SELECT i FROM cardinal ORDER BY i RETURN NEXT r.i; FOREACH i IN ARRAY ARRAY[1,2] RETURN NEXT i. Query `SELECT * FROM f() ORDER BY 1` expects [1,2] for each. Labeled nested EXIT/CONTINUE and canceled nonproductive loop tests exercise transfer/cleanup, not an invocation/transaction per iteration. Arrays/general control/query capabilities are separate consumers, not baseline evidence.

Concrete control definitions (each query has column `v`, INT4, ordered [1,2]):

```sql
CREATE FUNCTION control_loop() RETURNS SETOF int LANGUAGE plpgsql AS $$
DECLARE i int:=0;
BEGIN LOOP i:=i+1; EXIT WHEN i>2; RETURN NEXT i; END LOOP; RETURN; END$$;
CREATE FUNCTION control_for() RETURNS SETOF int LANGUAGE plpgsql AS $$
BEGIN FOR i IN 1..2 LOOP RETURN NEXT i; END LOOP; RETURN; END$$;
CREATE FUNCTION control_while() RETURNS SETOF int LANGUAGE plpgsql AS $$
DECLARE i int:=0;
BEGIN WHILE i<2 LOOP i:=i+1; RETURN NEXT i; END LOOP; RETURN; END$$;
CREATE FUNCTION control_query() RETURNS SETOF int LANGUAGE plpgsql AS $$
DECLARE r record;
BEGIN FOR r IN SELECT i FROM cardinal ORDER BY i LOOP RETURN NEXT r.i; END LOOP; RETURN; END$$;
CREATE FUNCTION control_array() RETURNS SETOF int LANGUAGE plpgsql AS $$
DECLARE i int;
BEGIN FOREACH i IN ARRAY ARRAY[1,2] LOOP RETURN NEXT i; END LOOP; RETURN; END$$;
SELECT v FROM control_loop() AS t(v) ORDER BY v;
SELECT v FROM control_for() AS t(v) ORDER BY v;
SELECT v FROM control_while() AS t(v) ORDER BY v;
SELECT v FROM control_query() AS t(v) ORDER BY v;
SELECT v FROM control_array() AS t(v) ORDER BY v;
```

### W6 — DML trigger context, action returns and operation atomicity

```sql
CREATE TABLE trg_t(i int);
CREATE TABLE trg_audit(op text,level_name text,tab text,arg text,value int);
CREATE FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO trg_audit VALUES(TG_OP,TG_LEVEL,TG_TABLE_NAME,TG_ARGV[0],NEW.i);
 IF TG_NARGS<>1 OR TG_ARGV[1] IS NOT NULL THEN RAISE EXCEPTION 'bad args'; END IF;
 IF NEW.i<0 THEN RAISE EXCEPTION 'negative'; END IF;
 NEW.i:=NEW.i+10; RETURN NEW;
END $$;
CREATE TRIGGER check_t BEFORE INSERT ON trg_t FOR EACH ROW EXECUTE FUNCTION trg_fn('tag');
INSERT INTO trg_t VALUES(1) RETURNING i;
INSERT INTO trg_t VALUES(2),(-1);
SELECT i FROM trg_t ORDER BY i;
SELECT op,level_name,tab,arg,value FROM trg_audit ORDER BY value;
```

First count1/INT4 11, audit ('INSERT','ROW','trg_t','tag',1). Second `P0001`/`negative`, discards both candidate rows and their audit writes; prior [11]/one audit remains. Fresh A BEGIN/first insert sees own row/audit, B neither, A COMMIT then B both. Rollback variant exposes neither. DML/core SQL capabilities are prerequisites for these real effects.

Extensions use separate fixtures: BEFORE row function `RETURN NULL` gives INSERT count0/no RETURNING rows/full schema/no row; AFTER function audits NEW.i=11 then returns NULL, ignored; statement AFTER function asserts TG_LEVEL='STATEMENT', NEW/OLD unavailable, INSERT SELECT WHERE false fires once/count0. INSTEAD OF INSERT on view writes a base table and RETURN NEW gives count1; null-return variant gives count0. AFTER statement `REFERENCING NEW TABLE AS new_rows` function selects ordered [1,2] from transition relation after multirow insert, demonstrating set images rather than one NEW slot. NEW/OLD event availability/DELETE return contents/UPDATE replacement use manager-owned typed tests, not invented scalar NULL semantics.

### W7 — Event context and DDL rollback

```sql
CREATE TABLE event_audit(ev text,tag text);
CREATE FUNCTION ddl_guard() RETURNS event_trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO event_audit VALUES(TG_EVENT,TG_TAG);
 RAISE EXCEPTION USING ERRCODE='P0001', MESSAGE='blocked ddl';
END $$;
CREATE EVENT TRIGGER ddl_guard ON ddl_command_end
 WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION ddl_guard();
CREATE TABLE blocked_t(i int);
SELECT to_regclass('blocked_t');
SELECT ev,tag FROM event_audit;
CREATE OR REPLACE FUNCTION ddl_guard() RETURNS event_trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO event_audit VALUES(TG_EVENT,TG_TAG); END$$;
CREATE TABLE allowed_t(i int);
SELECT ev,tag FROM event_audit;
```

Privileged setup required; audit created before filtered installation. Blocked CREATE `P0001`/`blocked ddl`; to_regclass NULL, audit empty (DDL and handler effects both rolled back). Allowed CREATE succeeds, one ('ddl_command_end','CREATE TABLE') audit. Fresh A BEGIN/allowed CREATE sees object/audit; B sees neither until A COMMIT, then both. ROLLBACK variant exposes neither. A start-error variant installed ON ddl_command_start prevents creation and end-event audit. Event-trigger DDL itself is excluded, avoiding recursive setup.

Helper extensions: in end callback `SELECT command_tag FROM pg_event_trigger_ddl_commands()` for a simple CREATE TABLE returns command tag CREATE TABLE; in sql_drop callback `pg_event_trigger_dropped_objects()` captures identity/name of dropped fixture after live catalog removal; table_rewrite callback reads `pg_event_trigger_table_rewrite_oid()` matching the affected table and nonzero applicable reason bitmask for ALTER causing rewrite. Identity/allocated OIDs are validated by relationships, not allocator-sequence parity. Calls to contextual helpers outside permitted event must fail with exact helper-specific release diagnostics captured by those bounded consumers. Non-superuser CREATE EVENT TRIGGER denies its privileged definition checkpoint. These helper/scheduling effects depend on DDL and their named consumers, never generic empty success.

### W8 — Committed segment interleaving and late error

```sql
CREATE TABLE segment_log(i int);
CREATE PROCEDURE segment_pause() LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO segment_log VALUES(10); COMMIT;
 PERFORM pg_advisory_lock(42);
 INSERT INTO segment_log VALUES(11);
 PERFORM pg_advisory_unlock(42);
END$$;
```

B first `SELECT pg_advisory_lock(42);`. A begins standalone `CALL segment_pause()` asynchronously. B repeatedly issues bounded READ COMMITTED `SELECT i FROM segment_log ORDER BY i` until committed10 is observed; assert11 absent while A is blocked. B `SELECT pg_advisory_unlock(42);`, await A completion; final [10,11]. Separate fresh fixture with body inserting11 then `RAISE EXCEPTION 'late segment'` after releasing lock: A P0001/late segment, B final [10] only. Harness finally releases all acquired session advisory locks, including on cancellation. Advisory synchronization is a distinct capability dependency; there is no invented routine pause SQL or sleep-only ordering. Engine analogue uses a controlled callback barrier, explicitly not PG SQL evidence.

### W9 — Invalidation, retained prepared authority/settings, separate snapshot rounds

```sql
CREATE TABLE cache_t(v int);
INSERT INTO cache_t VALUES(1);
CREATE FUNCTION cache_read() RETURNS int LANGUAGE plpgsql AS $$
DECLARE n int; BEGIN SELECT v INTO n FROM cache_t; RETURN n; END$$;
SELECT cache_read();
```

A invokes/saves exported1 and metadata. B `ALTER TABLE cache_t RENAME COLUMN v TO w;` commits. A next reached invocation fails `42703`/`column "v" does not exist`, never stale binding; saved1/type remain stable. Search_path extension: two schemas each with cache_t holding 1/2, function selects unqualified v; A changes path between calls and gets shared PREPARE-consistent rebinding. Creating an earlier-path same-name table without changing path alone does not promise rebind. DROP/recreate/ref epoch and rename-owner tests consume catalog validity/current owner, not a routine cache.

Ordinary prepared authority, privileged setup:

```sql
CREATE ROLE runner;
CREATE FUNCTION acl_f() RETURNS int LANGUAGE SQL AS $$SELECT 7$$;
REVOKE EXECUTE ON FUNCTION acl_f() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION acl_f() TO runner;
```

A attached as runner prepares `SELECT acl_f()` and executes7. B owner revokes runner EXECUTE/commits. A executes the same prepared handle and gets `42501`/`permission denied for function acl_f`; retained previous7 unchanged. Database/schema access granted independently, no permission token in plan.

Definer/settings extension: owner defines SQL invoker helper returning current_user::text and definer wrapper selecting helper/current_user/session_user with SET search_path=trusted,pg_temp. Runner invocation sees owner in current_user/helper, unchanged runner session_user; next caller query current_user is runner on success/error/cancellation. Function SET search_path restores caller value; ordinary SET search_path within routine overrides/persists unless rollback; SET LOCAL of same variable stays confined to routine SET scope. Assert exact resulting settings, not blanket restoration. Owner change affects next scoped call.

**Snapshot witness uses two fresh rounds, not child SELECTs inside a volatile procedural wrapper.** Setup in each fresh round:

```sql
CREATE TABLE snap_t(v int);
INSERT INTO snap_t VALUES(1);
CREATE FUNCTION wait_for_peer() RETURNS int LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 PERFORM pg_advisory_lock(43);
 PERFORM pg_advisory_unlock(43);
 RETURN 0;
END$$;
CREATE FUNCTION read_after_wait() RETURNS int LANGUAGE SQL STABLE AS $$
 SELECT wait_for_peer();
 SELECT v FROM snap_t;
$$;
```

B acquires session advisory lock43. A starts standalone `SELECT read_after_wait()` under READ COMMITTED. Harness establishes A has entered helper and is waiting (advisory lock queue observation supplied by that capability or independently controlled reference coordination), **then** B `UPDATE snap_t SET v=2;` commits and unlocks43. Await A: STABLE returns INT4 **1**, its calling-query snapshot. In a separate reset/new-schema round use identical body declared **VOLATILE**, snap_t initialized1 and the same schedule; A returns INT4 **2**, fresh later-query view. Stable body contains only SELECT; calling volatile helper is permitted. Helper unlocks its session lock after acquisition; harness finally cleanup also covers errors/cancellation. Do not use target-list evaluation order or sleeps as proof. A repeat under REPEATABLE READ remains governed by TX's established pin, not unconditional latest peer commit. Attributes/TX/advisory lock observation are explicit dependencies; neither round was executed here.

For an executable future schedule, A first records `SELECT pg_backend_pid();` outside the timed invocation. B acquires `SELECT pg_advisory_lock(43);`; start A's SELECT asynchronously, then B polls a bounded `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND objid=43 AND NOT granted;` with A's recorded pid as a typed value until one waiter exists. Only then issue B's committed UPDATE and unlock. Each poll is a separate request; failed waiter observation is a failed witness, not evidence of snapshot inheritance. Backend/lock metadata and advisory capability own this observation, not the routine architecture.

In the **fresh VOLATILE round**, recreate snap_t with value1 and wait_for_peer as above, but define the reader with this complete SQL instead:

```sql
CREATE FUNCTION read_after_wait() RETURNS int LANGUAGE SQL VOLATILE AS $$
 SELECT wait_for_peer();
 SELECT v FROM snap_t;
$$;
```

Use the same recorded-pid/waiter/UPDATE/unlock schedule and independent expected INT4 2. The two reader declarations are never executed as one same-name setup script in one fixture, and no STABLE result is inferred from a preceding VOLATILE wrapper's separately snapshotted child queries.

### W10 — Trusted installed-manager checkpoint and queued identity

Fresh privileged fixture; auth roles have independently granted database/schema access:

```sql
CREATE ROLE installer;
CREATE ROLE fire_runner;
CREATE TABLE manager_t(i int);
CREATE TABLE manager_audit(who text);
CREATE FUNCTION manager_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO manager_audit VALUES(current_user::text); RETURN NEW; END$$;
REVOKE EXECUTE ON FUNCTION manager_fn() FROM PUBLIC;
GRANT TRIGGER ON manager_t TO installer;
GRANT EXECUTE ON FUNCTION manager_fn() TO installer;
GRANT INSERT ON manager_t,manager_audit TO fire_runner;
SET ROLE installer;
CREATE TRIGGER installed BEFORE INSERT ON manager_t FOR EACH ROW EXECUTE FUNCTION manager_fn();
RESET ROLE;
REVOKE EXECUTE ON FUNCTION manager_fn() FROM installer;
SET ROLE fire_runner;
INSERT INTO manager_t VALUES(1);
RESET ROLE;
SELECT who FROM manager_audit;
SET ROLE installer;
CREATE TRIGGER denied_install BEFORE INSERT ON manager_t FOR EACH ROW EXECUTE FUNCTION manager_fn();
RESET ROLE;
```

Installed fire succeeds/count1/audit fire_runner despite no runner EXECUTE; reinstall fails `42501`/`permission denied for function manager_fn`. Body audit privilege still matters: revoke runner INSERT on manager_audit, next fire fails ordinary relation `42501`, entire firing insert rolls back. SECURITY DEFINER version audits owner and uses owner's body rights; restore caller role afterward. Direct authorized `SELECT manager_fn()` retains trigger protocol failure, not ordinary callable success; exact diagnostic/precedence is a future pinned protocol fixture, not guessed from ACL denial. Ordinary acl_f denied call remains W9's independent ACL case.

Deferred queue identity, fresh fixture with roles r1/r2 and required membership/INSERT rights, plus audit rights for r1: install `CREATE CONSTRAINT TRIGGER queued AFTER INSERT ON queue_t DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION queue_fn();`, whose invoker body inserts current_user::text into queue_audit then RETURN NULL. A BEGIN/SET ROLE r1/INSERT queue_t/SET ROLE r2/SET CONSTRAINTS ALL IMMEDIATE/RESET ROLE/COMMIT. Audit **r1**, not firing-time r2; definer variant owner. B sees neither private row/audit before commit, both afterward. SAVEPOINT-before-insert/ROLLBACK TO removes queued event and produces no callback audit. Queue survives its original finished frame without borrowing slots.

Concrete queue setup as a privileged owner (default PUBLIC EXECUTE is immaterial to installed firing; no function EXECUTE grant is needed for r1/r2):

```sql
CREATE ROLE r1;
CREATE ROLE r2;
CREATE TABLE queue_t(i int);
CREATE TABLE queue_audit(who text);
GRANT INSERT ON queue_t TO r1,r2;
GRANT INSERT ON queue_audit TO r1;
CREATE FUNCTION queue_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO queue_audit VALUES(current_user::text); RETURN NULL; END$$;
CREATE CONSTRAINT TRIGGER queued AFTER INSERT ON queue_t
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION queue_fn();
```

A is the privileged session permitted to SET ROLE to both roles. Execute each command separately:

```sql
BEGIN;
SET ROLE r1;
INSERT INTO queue_t VALUES(1);
SET ROLE r2;
SET CONSTRAINTS ALL IMMEDIATE;
RESET ROLE;
SELECT who FROM queue_audit;
COMMIT;
```

A sees TEXT r1; B `SELECT i FROM queue_t ORDER BY i` and `SELECT who FROM queue_audit` are empty before commit and [1]/['r1'] afterward. Fresh fixture rollback round:

```sql
BEGIN;
SAVEPOINT before_queue;
SET ROLE r1;
INSERT INTO queue_t VALUES(2);
ROLLBACK TO before_queue;
RESET ROLE;
SET CONSTRAINTS ALL IMMEDIATE;
COMMIT;
SELECT i FROM queue_t ORDER BY i;
SELECT who FROM queue_audit;
```

No row2/new audit. On a reused first-round fixture results stay [1]/['r1']; on a fresh empty fixture both stay empty. The savepoint includes transactional role/settings restoration; RESET ROLE makes the final observation authority explicit.

Event revoke-after-install extension: superuser installs filtered end event function auditing current_user, revokes PUBLIC/function EXECUTE, non-superuser DDL runner with CREATE/audit INSERT creates table; callback still fires as runner. Body privilege revoke fails and rolls back DDL. Definition remains superuser-owned checkpoint; direct event function invocation remains protocol-restricted. This independently distinguishes installed implicit manager entry from ordinary callable authorization without broadening the bypass.

Concrete privileged event setup in a fresh schema with no other CREATE TABLE filters:

```sql
CREATE ROLE ddl_runner;
GRANT CREATE,USAGE ON SCHEMA public TO ddl_runner;
CREATE TABLE manager_event_audit(who text,tag text);
GRANT INSERT ON manager_event_audit TO ddl_runner;
CREATE FUNCTION manager_event_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO manager_event_audit VALUES(current_user::text,TG_TAG); END$$;
CREATE EVENT TRIGGER manager_event ON ddl_command_end
 WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION manager_event_fn();
REVOKE EXECUTE ON FUNCTION manager_event_fn() FROM PUBLIC;
```

A attaches as ddl_runner (not merely a superuser whose current_user is disguised), issues `CREATE TABLE managed_ddl(i int);`, succeeds. Owner observes `SELECT who,tag FROM manager_event_audit;` as one ('ddl_runner','CREATE TABLE'); managed_ddl exists. Owner revokes INSERT on manager_event_audit; A `CREATE TABLE denied_ddl(i int);` fails `42501`/`permission denied for table manager_event_audit`; owner `SELECT to_regclass('denied_ddl');` is NULL, earlier audit unchanged. Ordinary function access denial is not used as a proxy for this installed dispatch case.

### U1–U8 — Independent engine contracts, not PostgreSQL SQL evidence

| Unit family | Independently required assertions |
|---|---|
| U1 | Recursion/repeated/session executions have distinct locals/OUT/status/output scratch; immutable shared programs/layouts; lexical coordinates/shadowing exact |
| U2 | Inject bind/body/result/cleanup/Finish/cancellation/partial-enter failures: unwind acquired resources once, exact caller AUTH restoration, root completion once, child never Done; failed admission no mutation |
| U3 | Defaults/modes/variadic maps and usages; OUT excluded/INOUT included in identity; failed CALL no partial destination copyback |
| U4 | Independent schema/Values/detached bytes; no escaped active frame/lease/queue slots; discarded rows close once; empty relation metadata versus no-relation DO; STRICT nonset typed NULL versus SETOF/TABLE full schema and **zero occurrences**, no body execution or manufactured NULL occurrence |
| U5 | Lazy binding/captured policy/source/type specialization/catalog validity/public args once; pre-open scalar FirstRow/static SELECT probe/**static modifying INTO TwoReturnedProbe even non-STRICT, no unused third primary action/RETURNING**; full SQL-function final DML/static modifying no-INTO/dynamic; required auxiliary completion/cleanup distinct from primary draining; no partial successful DML publication/failed assignment discarded/TX rollback/P0003 before unused third check; dynamic full production then STRICT-only adaptation/complete ROW_COUNT/unchanged FOUND; definition validator no execution versus sequential deferred runtime/terminal check before terminal execution/no earlier-result fallback/transactional rollback but sequence advances persist |
| U6 | Mark-before-effects/rollback-release-before-handler; row/catalog/settings/providers included, locals excluded; nested handled diagnostic restored; restore failure cannot leave partially Active |
| U7 | Rotation uses existing TX end, renews all TX-qualified handles, keeps locals/Operation, defaults versus CHAIN; prior commit survives later error; illegal context rejected before end effects |
| U8 | User AST cannot forge manager provenance; omit only installed per-fire EXECUTE, retain body checks/current-owner scope; original diagnostic primary and secondary cleanup distinct; ordinary Go errors untagged; notices delivered |

Units exercise ownership/invariant/error-injection details that SQL cannot directly expose. They do not substitute for the independently expected PostgreSQL positive/failure/interleaving scenarios above.

## 12. Dependencies, official references and handoff

Prerequisite design contracts are reused, not implemented by their completed status:

- [QUERY execution](PG18-ARCH-QUERY-EXECUTION.md) §§2–3,6–9: typed schemas/frames/common rows, one binder, finite eager output, completion and cutover.
- [AUTHORIZATION](PG18-ARCH-AUTHORIZATION.md) §§4–7: coherent revisions, preparation/execution phase precedence, immutable identity and ordinary call checks; this document's §7.2 explicitly refines installed-manager entry only.
- [TX visibility](PG18-ARCH-TX-VISIBILITY.md) §§2–4,7: conditional AcquireDataView, root statement/Operation completion, state/marks/participants/end authority; §6 adds future child/unnamed-mark/non-atomic-rotation consumption protocol without replacing algorithms.
- CATALOG/TYPE/EXPR/SESSION, settings/diagnostics and future DML/DDL/cursor/advisory consumers retain their own actual capability/lifecycle authority. Snapshot/role/queue/catalog interleavings require those real capabilities; unsupported external mechanisms are not successful no-ops.

Official PG18 normative references (sections support decisions and independent expectations, not observed runs):

| Topic | Reference |
|---|---|
| SQL argument precedence/last result/OUT/set | [SQL functions §36.5](https://www.postgresql.org/docs/18/xfunc-sql.html) |
| STRICT/modes/defaults/SQL body definition timing/security/settings | [CREATE FUNCTION Parameters/sql_body/Notes](https://www.postgresql.org/docs/18/sql-createfunction.html) |
| Procedure attributes/CALL outputs/transaction restrictions | [CREATE PROCEDURE](https://www.postgresql.org/docs/18/sql-createprocedure.html), [CALL Notes](https://www.postgresql.org/docs/18/sql-call.html) |
| Query snapshot policy | [Volatility §36.7](https://www.postgresql.org/docs/18/xfunc-volatility.html), [isolation](https://www.postgresql.org/docs/18/transaction-iso.html) |
| Lexical blocks/expressions | [PL structure §41.2](https://www.postgresql.org/docs/18/plpgsql-structure.html), [expressions §41.4](https://www.postgresql.org/docs/18/plpgsql-expressions.html) |
| Binding/deferred analysis/plan reuse | [PL implementation §41.11](https://www.postgresql.org/docs/18/plpgsql-implementation.html), [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html) |
| Dynamic SQL/INTO/status/assignments | [PL statements §41.5](https://www.postgresql.org/docs/18/plpgsql-statements.html) |
| Return/control/exception rollback and local survival | [PL control §41.6](https://www.postgresql.org/docs/18/plpgsql-control-structures.html) |
| Portals/loop/end coordination | [PL cursors §41.7](https://www.postgresql.org/docs/18/plpgsql-cursors.html), [transactions §41.8](https://www.postgresql.org/docs/18/plpgsql-transactions.html) |
| RAISE/ASSERT/diagnostics | [PL errors §41.9](https://www.postgresql.org/docs/18/plpgsql-errors-and-messages.html), [Appendix A SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html) |
| Trigger context/actions/definition privileges | [PL trigger/event variables §41.10](https://www.postgresql.org/docs/18/plpgsql-trigger.html), [trigger overview §37.1](https://www.postgresql.org/docs/18/trigger-definition.html), [CREATE TRIGGER Notes](https://www.postgresql.org/docs/18/sql-createtrigger.html) |
| Event timing/failures/exclusions | [Event definition §38.1](https://www.postgresql.org/docs/18/event-trigger-definition.html) |
| Authorization checkpoint meaning | [Privileges](https://www.postgresql.org/docs/18/ddl-priv.html) |

Release source corroboration, not SQL execution evidence: [`REL_18_0 spi.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/spi.c) `_SPI_commit`/`_SPI_rollback` for atomic-before-subtransaction diagnostics; [`pl_handler.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/pl/plpgsql/src/pl_handler.c) validator for procedural compilation; [`trigger.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/commands/trigger.c) `ExecCallTriggerFunc` and [`event_trigger.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/commands/event_trigger.c) `EventTriggerInvoke` for installed dispatch. Local research code does not override PG18 release semantics.

Additional release result evidence, not runtime observations: [`REL_18_0 execSRF.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execSRF.c) strict table-function/set-expression branches supply empty sets for NULL inputs; [`REL_18_0 functions.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/functions.c) `init_execution_state`/`postquel_getnext`/`fmgr_sql` distinguish one-row eligible SELECT from complete **SQL-function final DML**, and compilation/`check_sql_stmt_retval` enforce actual final statement/42P13. Official SQL functions §36.5's DML completion rule is scoped to SQL-function bodies, not static PL modifying INTO. QUERY's eager-public/demand-sensitive-child policy remains authoritative with no prerequisite edit.

Dynamic/sequential corroboration, not executed witness evidence: [`REL_18_0 pl_exec.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/pl/plpgsql/src/pl_exec.c) `exec_stmt_execsql` sets static one/two-row demand and modifying-statement cardinality checks, whereas `exec_stmt_dynexecute` uses zeroed/unlimited options and `n > 1 && stmt->strict` after execution; [PL §41.5.4](https://www.postgresql.org/docs/18/plpgsql-statements.html#PLPGSQL-STATEMENTS-EXECUTING-DYN) specifies dynamic INTO, and [SPI_execute_extended](https://www.postgresql.org/docs/18/spi-spi-execute-extended.html) defines zero tcount as unlimited. `functions.c` `fmgr_sql`/`init_execution_state`/`prepare_next_query` prepare and execute SQL text commands sequentially, checking final result shape at terminal preparation; this is distinct from nonexecuting definition validation.

Static modifying probe corroboration, not witness execution: [PG18 SPI_execute](https://www.postgresql.org/docs/18/spi-spi-execute.html) says a positive returned-row count stops primary INSERT RETURNING after that many rows, unlike INSERT without RETURNING; release `pl_exec.c` `exec_stmt_execsql` sets tcount2 for modifying INTO even non-STRICT, and `spi.c` passes that count to ExecutorRun. [`REL_18_0 execMain.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execMain.c) `ExecPostprocessPlan` drains auxiliary modifying nodes; [`REL_18_0 nodeModifyTable.c`](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/nodeModifyTable.c) `ExecInitModifyTable` registers only `!canSetTag` secondary nodes there. Required auxiliary work is not a blanket finish-time drain of the stopped primary modifying RETURNING producer.

Acceptance mapping: §2/§3 settle shared frame/interfaces/entry-exit/slots/results; §4/§5 settle binding/analysis/cache; §6 settles exception/eligibility/state/TX extension; §7 settles AUTH/error/source propagation and explicit trusted-manager refinement; §8 settles trigger/event context/atomicity; §9 names every affected current route/caller and clean removal; §10 maps bounded consumers; §11 W1 fulfills (a), W2 (b), W3/W8 (c), W4 (d), W6/W7 (e), W9/W10 add visibility/prepared-authority/trusted-manager interleavings, U1–U8 distinguish engine-independent invariants.

**Design-session feature smoke argv: `[]`; witness executions: none.** No new test/script is supplied or required to claim routine behavior here. Root owns the fresh complete baseline utility verification and independent design review after edits; implementation agent runs no build/test/formatter/check. Future implementation owners convert the concrete SQL/session schedules into executable independent fixtures, capture unobserved exact diagnostic fields and record actual commands/expected-versus-observed outcomes. No unresolved design blocker remains after the root-settled TX extension/AUTH refinement and corrected separate W9 STABLE/VOLATILE rounds; future runtime field capture is an explicitly named acceptance obligation, not fabricated present evidence.
