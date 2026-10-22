# proc — immutable SQL definitions and private values

`proc` is the reusable, lower-level routine owner. It imports parser and scalar
value/type facilities, never engine or catalog. It does not interpret expressions
or own name lookup, permissions, query execution, transactions or prepared caches.

`Definition` retains original body text/source position, exactly one parsed SELECT
and concrete declared input/result usages. Published definitions and their syntax
are immutable. CREATE validation belongs to engine's ordinary query binder and
never opens producers or evaluates providers. Text-body object names are runtime
analysis facts, not permanent CREATE-time dependencies.

Complete body parsing and the shared binder treat declared formals as a closed
namespace, including zero inputs: out-of-range `$n` fails even after an unresolved
call that would stop semantic binding. Quoted strings/comments are not formals.
Unused declared slots remain valid. Private Restore may defer only a structured
terminal missing callable, preserving original syntax. Other errors abort;
semantic checks blocked by that missing call run at invocation once resolvable.
Runtime binding checks
the current terminal SELECT width and Assignment result plan before producers
or modifying CTEs open, including empty input and dependency reanalysis. Internal
body output changes may remain valid through assignment conversion without
changing the caller's declared scalar result contract.

Each invocation owns a `Frame`. Actual expressions are evaluated once by engine,
coerced using implicit input plans, then placed in private slots. Nested/repeated
calls cannot overwrite caller parameters. Scalar results use assignment coercion.
Scalar/tree `sqltype.Value` payloads are immutable: a returned value survives frame
closure without an unnecessary payload copy. Driver mutable-byte export remains
with its existing type/driver owner. Closing a frame clears slots and result and
is idempotent; no closed frame admits a new value.

Engine holds checked catalog identities, shares existing prepared analysis, checks
live EXECUTE/invoker authority, opens current query sources and selects scalar
first-row demand before producer opening. Required sort/aggregate/auxiliary work
remains with the query/DML owners. No row yields NULL of the declared result type.
Persistence stores symbolic declarations and original SQL, never frames, actual
values, executable plans, data views or authorization decisions.

Invocation borrows the same admitted engine statement scope. A balanced lexical
activation guard saves/restores query, parameter, view and protocol scratch;
the root stage, effects, completion queues, transaction and live reservation
ownership stay with their existing owners. A callee never calls root Finish or
creates a separate publication boundary.

The root statement scope also owns a balanced maximum of 128 active SQL bodies,
shared by direct and mutual calls. Excess calls return SQLSTATE 54001, `stack
depth limit exceeded`; successful, failed and canceled calls all unwind. This
native safety limit is not PostgreSQL's byte-valued `max_stack_depth` setting.
Result usage/coercion plans are invocation scratch, never retained executable
plans in the session's analysis cells.

Catalog procedure descriptors hold `*Definition`, not a second executable
registry. Namespace/signature selection, the catalog's derived COW name index,
prepared-analysis retention and stored-call identity capture remain above proc.
Frozen engine syntax marks selected SQL calls only after binding; that marker
cannot authorize a call or replace its checked occurrence reference.

## Testing

Run from the repository root, with an external 900-second command deadline:

```sh
make check RACE=1 RUN='^TestSQLRoutine(FrameIsolation|FrameResultOwnership|CallableSelection|StagedLookup|FirstRowDemand|ValidationNoExecution|BodyAnalysisRetention|RestoreStaging|FrozenPresentation|StoredDefaultValidation)$'
make check RUN='^TestReferenceRoutineCore(CreationAndCalls|Resolution|BodyAndResults|Diagnostics|Lifetime|Persistence|FeatureSmoke|CallableKinds)$'
make check REFERENCE=1 RUN='^TestReferenceRoutineCore(CreationAndCalls|Resolution|BodyAndResults|Diagnostics|Lifetime|Persistence|FeatureSmoke|CallableKinds)$'
make check RUN='^TestReferenceRoutineReview(ClosedFormals|ReturnReanalysis|Recursion|MutualRecursion)$'
make check REFERENCE=1 RUN='^TestReferenceRoutineReview(ClosedFormals|ReturnReanalysis|Recursion|MutualRecursion)$'
make check RACE=1 RUN='^TestSQLRoutine(ClosedFormalBoundary|ReturnContract|DepthUnwind|ResultScratchUnwind)$'
```

The permanent selected feature smoke is
`make check RUN='^TestReferenceRoutineCoreFeatureSmoke$'`, in native mode and with
`REFERENCE=1`. Expected: add1(4)=5/INT4, held inputs 4/9 yield 5/10,
empty-body typed NULL, first eligible projected row 10, FLOAT8 adaptation,
restored add1=5, cycle-default sum=14 and missing-function SQLSTATE 42883.
A separately exercised public-API `go run` smoke observes the same compact native
outcomes; its temporary source is not a repository deliverable.
Persistence additionally exercises actual emitted-stream replay on pinned
PostgreSQL18.6. See the [reference testing contract](../../../reference/query_core/README.md#scalar-sql-routine-contract).
These commands are proof selections, not claims that this package ran them.
