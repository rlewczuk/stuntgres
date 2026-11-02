# internal/engine/core/explain

EXPLAIN rendering: the presentation trees of one command, their logical
estimates, statistics, pruning and foreign-provider details, and TEXT/XML/JSON/YAML
`QUERY PLAN` output. It is a leaf below core (ENGINE-SPLIT §3, L14.5).

## Layering

- Imports only packages below core: `access`, `analyze`, `authorization`,
  `catalog`, `exec`, `expr`, `fdw`, `functions`, `ids`, `lock`, `partition`,
  `plan`, `regobj`, `session`, `settings`, `storage`, `stored`, `table`,
  `view`, `parser`, `sqlstate`, `sqltype`. It never imports core, a family or
  the root.
- Core calls it from its statement seam: `explain.go` (Describe, ordinary
  Execute), `explain_analyze.go` (EXPLAIN ANALYZE capture), the bound
  executor (`execute.go`, `execute_prepared_bound.go`), relation admission
  (`relation_scope.go`, option validation) and query admission
  (`PlannerSourceRows`). `scope.explain` (the activation's renderers) stays in
  core.

## Contract

- `ValidateOptions(statement, overexplainLoaded)` validates ordered execution
  options against current physical-session recognition. `DescribeFormat` reads
  FORMAT alone for the result descriptor; `Error` builds located diagnostics.
- `NewRenderer(host, auth, options)` starts one command's presentation.
  `Command(statement, Bound)` renders a bound command (and its rule rewrites,
  CTE producers and subplans) into `Tree`s; `Render` returns TEXT lines and
  `Output` publishes rows through the selected TEXT/XML/JSON descriptor codec.
  `ForeignFence` re-checks the captured catalog state and roles.
- EXPLAIN ANALYZE: core sets `Observation`, reads `CommandTree` (its `Actual`
  measurements are filled by the executor), and calls `MarkRuleRewrite` for
  rule actions. `PresentedNodes` supports core's actual-owner presentation tests
  and `ExplainObservationForTest` (the existing recorded T6 test seam).
- `SetForeignParameters` supplies the typed parameter frame and its provider
  export for foreign planning.

## Host and authority

- `Host` is the statement being presented; core implements it on
  `statementHost` (the shared leaf method set). It gives the execution
  context, the operation, I/O environments, admitted planner methods, the
  pending catalog journal, the catalog domain, partition/inheritance reads
  with the pending detach snapshot, published and private storage, the
  restore-effect check, current access for fences, and view-source
  authorization.
- `Authority` is the statement authorization: `analyze.Privileges` (binding
  checks), `access.Environment` (policy checks) and `view.RuleAuthority`
  (rewritten rule commands). Core's `AuthorizationView` implements it. The
  renderer keeps the `access.View` cached next to it; rule authorities are
  recovered with one checked assertion.

## Locks

- The renderer is entered with `Engine.Mu` held and holds no lock of its own.
- Provider callbacks (foreign scan, query and modification planning) and the
  detached comparisons (pruning proofs, selectivity) run between
  `Host.ReleaseEngineLocked` and `Host.ReacquireEngine`; foreign callbacks
  also drop the replacement read lock first (`ReleaseReplacementRead`).
  Presentation re-fences catalog, context and authority after each window.

## Behaviour

Ordinary EXPLAIN (including effective ANALYZE FALSE) shares the description-only
binder, source admission and rule/view validation with Prepare. Its command
boundary returns before execution fold planning, source execution, DML startup,
modifying CTE activation and tuple locking. Foreign occurrences instead obtain
actual selected provider plans and
optional explain properties, detached from engine/replacement locks.
Composite eligibility/translation/compatible minimum selection is shared with
execution and attempted before replaced children. Selected composites expose an
owned query/path payload rather than invented base estimates. Metadata-only
query-lock demand and stored-view effective marks feed shared row-mark planning
before base size/path/plan callbacks, including COPY/REFERENCE siblings. These
paths never capture runtime admission, enlist, begin, refetch, recheck or acquire
remote tuple locks; the ordinary final grants/rebind/authority fence still runs.
Value-only Outer/Invocation/IdentityReads metadata follows the actual fresh or
copied State boundary: scalar subplans set Outer; ordinary/folded derived inputs
preserve Outer/Invocation and rederive identity; correlated right inputs establish
Invocation; stored producers reset all three; materialized CTE producers use
their captured definition-owner context, not a later scalar reader's context.
RuleInvocation assigns Outer from Invocation rather than monotonically adding a
nested-depth exclusion. Bound query leaves alone normalize nil Uses to empty,
matching execution's presence-only demand without changing nil/full requests
for modification targets or ANALYZE.
Bound graphs and expression presentation remain
activation-local; prepared descriptions retain only ordinary immutable
dependency/parameter captures and the wrapper's single `QUERY PLAN` descriptor.
Their fold owner is inert: ordinary EXPLAIN never grants child fold-publication
rights, including during Describe or protocol Parse/Bind, and retains no accepted
generation. The ordinary renderer describes actual logical stages and rewritten
commands, with detached maintenance statistics or labelled deterministic
heuristics. It never executes child expressions or opens child scans; providers
may obtain genuine remote estimates during planning. Before publishing owned output
it fences catalog, context and current authority again, including after unlocked
typed comparisons.

TEXT line rows and one non-NULL XML/JSON/YAML document row publish through one
output owner. Database types are TEXT/XML/JSON/TEXT respectively. XML and JSON
use their catalog input codecs, never retagged TEXT data or adapter conversion.
FORMAT description does not validate execution-only options early; ordered
option errors still follow child analysis. `Options.Structured()` selects one
detached typed map/list projection shared by all structured formats and late
actual foreign-composite substitution. Properties are captured at construction,
never parsed from labels or TEXT. Lists preserve expression boundaries, nested
VALUES/grouping sets, MERGE arms and ordered duplicate provider Name/Value records.
Provider labels are data, never structural keys or XML names.
XML uses the PostgreSQL EXPLAIN namespace, Query/Plan/Plans containers, hyphenated
fixed property labels and Item list elements (Plan inside Plans). Its scalar
content is lexical, with no invented SQL xsi typing; empty maps/lists and nil
scalars use empty elements. YAML uses YAML-compatible JSON flow collections
directly from the same typed projection: all keys/strings are double quoted,
numbers/Booleans remain typed, empty maps/lists are `{}`/`[]`, and nil is `null`.
No JSON parse/round trip or second plan representation is involved.
Auxiliary roots remain ordered document entries. Logical estimates use Plan Rows,
Logical Cost and Estimate Model; genuine provider estimates remain under Provider
Estimates. Unknown quantities are omitted. VERBOSE gates ordinary output and
qualification; COSTS gates estimates, not actual measurements. Default/DEBUG FALSE
TEXT allocates neither structured property maps nor debug identity maps.

DEBUG recognition belongs to the physical owner's authorized `pg_overexplain`
activation, including DEBUG FALSE. Describe never snapshots it: an already-held
definition sees a later LOAD at Execute. Ordered Boolean parsing is shared with
ordinary options; an invalid earlier duplicate is not rescued by a later value.
Debug projection itself grants no execution or expression-evaluation rights.
`Embedded Debug` contains only real bound-node information: positive statement-local
Node ID (first final-presentation encounter, shared for the same actual node),
owned Frame/Occurrence fields, checked Relation OID, ordered Project/Sort Target
List SQL and Sort Targets records (Target Identity, Output Ordinal, Slot, Cache,
Output and Expression). Valid zero fields and zero-width target lists survive.
Capture uses the active lexical frames and existing stored deparser; numbering
does not mutate the graph. Synthetic wrappers have no fabricated node identity.
Synthetic DML candidate-frame adapters (target/VALUES/Singleton/join/filter)
likewise receive no node IDs. `Embedded Command Debug` instead captures the
actual checked command Relation OID and its ordered bound Returning Target List
SQL, explicitly `[]` when there are no RETURNING expressions.
The command group has no Node ID; genuine bound source/subquery children retain
their ordinary Embedded Debug identities. The existing target-versus-source
deparser frames serve both ordinary RETURNING details and this debug projection.
IDs, frames, target SQL and all embedded debug fields are nonportable internal
presentation, not PostgreSQL planner structures or stable identities across rebind.
Only detached scalar/container output survives renderer/operation teardown.
Named conflict constraints retain the raw syntax name already resolved by the
binder to a relation-local constraint identity, rather than a descriptor lookup.
Requested domain defaults retain their bound default origin and effective
assignment expression in structured output without evaluating or deparsing the recipe again.

Actual fields identify emitted output and real invocation loops separately from
logical estimates. Measured node elapsed milliseconds include child work and
exclude consumer idle time. Parallel aggregate elapsed is summed active worker
plus leader work, not wall critical-path time; one logical loop emits only
finalized groups. Original HAVING/sort/projection identities retain the actual
copied leader-wrapper work. Optional planning/execution summaries exclude plan
transfer and outer transaction publication. Results complete with the non-counted
EXPLAIN command tag; child affected rows are not the wrapper affected count.
Structured Actual Rows and Actual Total Time are averages over Actual Loops; zero-loop
nodes have numeric zero rows/loops and no elapsed field. Actual Total Time is the
active inclusive/summed-worker elapsed measurement described above, not physical
startup or wall critical-path time. TIMING gates node elapsed, SUMMARY gates the
once-per-command Planning Time/Execution Time fields; neither removes row/loop
measurements. Ordinary SUMMARY has planning only. Measurement Model explains
the existing measurement semantics without inventing instrumentation.
Single-source filter access details share execution's detached CPU decision.
`CPU Cost` is separate from inclusive logical tree costs: it compares actual
hash-candidate versus full-traversal work without changing unrelated estimates.
Capture uses maintenance metadata (or default1000), never source counting,
bucket probing or expression evaluation. Explicit/private/RLS/unsupported
admission retains the safe full traversal; named hash indexes are ordered by
object identity. Local-worker costs use the same command-admitted value record.
Cursor declaration additionally captures its startup/total retrieval fraction;
ordinary queries retain fraction1. Existing explicit/private/RLS hash guards
remain unchanged, including for cursors. Implicit WITH HOLD declaration can use
the eligible hash alternative before promotion; promotion retains complete
results and releases the producer normally. Worktable estimates are renderer-local,
scoped by the owning CTEDefinition and its seed rows times the admitted factor.
EXPLAIN's `Pruned Sources` detail uses partition's actual conservative branch
proof over captured metadata, with comparisons unlocked and no source opening.
Method details use admitted `PlannerMethods` and pure shared runtime shape/frame
predicates: hash-assisted versus nested-loop joins and hash versus equality
grouping/deduplication. They do not assert runtime index-root eligibility,
partition interval counts or launched gather/foreign work. Statement scopes
own copied method values; routine scratch restores the enclosing value
independently of presentation refresh, while a separately admitted nested SQL
command captures its current effective settings. Cursor/portal executor owners
retain their original value across FETCH or suspension.
Window clauses are resolved through their owning stage's canonical specification
indices, and SRF outputs through that stage's fixed call/scalar slots. Verbose
outputs omit private grouping identities and reserved, not-yet-produced SRF
slots; neither is an executable SQL output expression.
UPDATE, DELETE and MERGE RETURNING expressions and their correlated subplans
retain the combined target/source candidate frame. INSERT RETURNING instead
uses the produced target row with its accepted target alias, independently of
input-query labels. OLD/NEW presentation does not replace source ownership.

## Testing

- Pure presentation units are internal tests here
  (`RUN='^TestExplain(JSON|Structured)Presentation$'`).
- Engine-level EXPLAIN tests stay in core until their census owners move
  them (`RUN='^Test(Explain|Planner|FDWExplain)'` over `./internal/engine/core`);
  public scenarios are `RUN='^TestReference(Explain|PlannerCosts)'`.
- `explain_cancel_test.go` (core) matches the frame `(*Renderer).node`;
  renaming that method or type changes it.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/explain/enginetests`.
