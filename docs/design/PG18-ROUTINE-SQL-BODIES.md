# SQL-language ordered routine bodies

## Contract and authority

SQL functions and procedure declarations share ordinary named/anonymous IN
inputs, a closed positional frame, and quoted or SQL-standard body forms.
Functions return a supported scalar or VOID; procedures have no RETURNS clause
and are not scalar-expression-callable. CALL and ALTER routine lifecycle DDL
remain separate capabilities. No language/provider ABI, attributes, defaults,
OUT/INOUT, variadics or set/composite outputs are introduced.

PostgreSQL authorities: [CREATE FUNCTION](https://www.postgresql.org/docs/18/sql-createfunction.html),
[SQL functions](https://www.postgresql.org/docs/18/xfunc-sql.html),
[CREATE PROCEDURE](https://www.postgresql.org/docs/18/sql-createprocedure.html),
and [dependency tracking](https://www.postgresql.org/docs/18/ddl-depend.html).
The current reference fixture pins PostgreSQL18.6 UTF8/C; local source trees are
research aids, not release oracles. The initial bounded definitions were run
against that oracle, corrected from observations, and then run natively before
production edits. Full completed-candidate proof remains a root responsibility.

## Syntax and declaration validation

`parser.CreateFunction` is the common function/procedure declaration, carrying
quoted source or a non-nil ordered standard statement list. The complete parser
handles scripts and ATOMIC lists; inner statements are not appended to the
outer script. Standard RETURN lowers to SELECT, including nonterminal RETURN:
it does not install a procedural interpreter or early-exit operation.

`proc.Definition` owns lower syntax, ordered formal names/types and result
policy. It does not import catalog/analyze/engine. DDL resolves concrete inputs,
permits VOID only as a result, rejects duplicate formal names, and preserves
namespace/owner/ACL/OID/signature authorities. Standard syntax defaults to SQL.
With body checks enabled, CREATE checks all commands without data producers or
expression effects. Public `check_function_bodies = off` and Restore's private
compatible directive retain quoted SQL source without parsing, catalog-dependent
analysis or result checking; reached execution performs those checks with closed
formals. Declaration types, language, owner and persistent structure are checked
independently. Standard SQL always analyzes and freezes its program. PL validation
and checked replay remain owned by their language provider.

The shared binder carries a routine lexical policy independent of row demand.
Column and outer-column lookup, including ambiguity, occurs before formal
fallback. Bare and routine-qualified formals lower to the same typed `$n` slots;
parameter count is closed even with zero inputs. Input actuals are evaluated and
implicitly converted once in the existing private `proc.Frame`.

## Frozen standard programs

`catalog.RoutineProgram` is a narrow immutable payload contract.
`stored.RoutineProgram` owns the ordered checked action recipes and dependencies;
it uses existing stored RuleAction/View presentation, source/attribute captures,
selected routine/operator identities and implicit argument usages. Shared
mutation recipe mapping and presentation include MERGE source/ON/WHEN/action
inputs/RETURNING as well as SELECT/INSERT/UPDATE/DELETE. This does not admit
MERGE as CREATE RULE grammar.

Publication checks the complete statement/formal association and program
dependency set. Replacement checks the original recipe structure, remaps every
action, and validates the complete replacement before it can publish. Immutable
per-action dependency manifests let invocation validate only the reached command,
including captures in its unexecuted MERGE arms. The program lookup does not
rescan other statement associations at each command boundary. Dump validates the
whole program once, then uses checked per-action rendering; neither path
revalidates every unrelated command for every statement. These structural checks
never retain authorization results or replace fresh invoker admission.

Mutation presentations retain the target-plus-source lexical row needed by
correlated subqueries. A private mutation-frame marker distinguishes this row
from the source-only FROM/USING syntax, including VALUES sources. One shared
scalar-root enumeration supplies freezing and checks missing/extra mutation
captures. Source occurrences are not duplicated by the target-row envelope.
Literal validation and VALUES source rendering share the same checked source-root
selection before matching retained syntax to its graph; neither treats the
synthetic target envelope as a raw FROM join or accepts a missing VALUES capture.

Genuine frame-1 FROM/USING VALUES, function and JOIN-ON occurrences are raw
owners even though the synthetic target carrier is not. Each main/modifier
presentation projects all ten occurrence maps through the validator's checked
raw-root enumeration; every original key must remain owned, and shared CTE
owners need not form a partition. Recursive missing/extra/path/count checks and
the full original binding captures, namespaces and dependency authority remain
unchanged. Actual child bindings retain frozen sources through the incumbent
memoized mapper. Query-less modifying-CTE scans require genuine MutationBinding,
supported statement and coherent RETURNING/definition/scan schemas and Usages.
Shared modifier enumeration pairs every actual statement with its immutable
ActionSyntax presentation and rejects missing, extra or swapped associations.
Ordinary derived queries still require their real QueryRoot; no fake producer
or runtime rebind represents a mutation.

Source-manifest completeness belongs to the entire associated action: a genuine
Query-less mutation scan does not expose its child's FROM graph as a SELECT.
One validation-local coverage set combines the main and exact modifier roots
through the incumbent leaf traversal, then checks every original outer source
ordinal. Each local leaf/prefix must retain original reference, stable attributes,
reads and provenance. Frames, rowmarks and duplicate-node checks stay local to
each presentation; ordinary view validation keeps complete per-view coverage.
No source is discarded, renumbered or marked analysis-only to fill this boundary.
Before freezing, common mutation binding activates CTE source demand from the
actual query/FROM/USING/MERGE roots using the existing executable-query walk.
An executable mutation source is not necessarily a registered SELECT query.
Genuinely unused SELECT definitions remain analysis-only; actual main and child
sources receive real capture rather than an identity-only analysis header.

Invocation uses checked action bindings, current target/source refresh and the
incumbent rule-command admission/ACL/data-view owners. It never renders, parses
or reselects names/overloads from standard SQL at runtime. Opened sources, data,
frames and authorization decisions are never stored in the payload. Ordinary
relation/view renames and same-path shadows do not retarget the recipe; later
more-specific overloads do not replace the creation-selected callee.
The admitted bound command bundle includes actual modifying-CTE children, not
only the main action or rule-rewritten children. Incumbent target/source grants
and readmission precede installing exact statement-keyed mutation contexts.
Each keeps its own physical target; main SELECT keeps none. Bound view/hierarchy
admission and fresh identity/ACL/RLS/context checks precede effects under the
same root ticket, data-view owner and scratch lifetime. No lexical name lookup
or fabricated admission index replaces the immutable child target.
Bound target grants do not imply local storage. The admission owner includes
only canonical Stored relation kinds in returned heap roots, while retaining
foreign target RX/ACL/identity authority and the real local source data roots.
Foreign execution continues through its provider, never a manufactured heap.

Standard source/callee/type dependencies enter DROP closure, pg_depend and dump
events. Quoted incidental references remain soft. Captures require the actual
catalog domain/epoch and surviving attributes, not a raw-name fallback.
The published normal dependency set de-duplicates identities and omits a
whole-relation edge when column edges already express that relation dependency.
The richer per-action admission manifests remain separate from this catalog
projection and still check targets, sources and their surviving attributes.
System-column occurrences retain the named relation and supported negative
attribute (-6 for tableoid), while execution can report a containing descendant.
Their existing column-occurrence capture is tagged with lexical frame and source
occurrence rather than a fabricated slot. Qualified rendering and one-step
reference remapping preserve that distinction through replacement.


## Execution and lifetime

A single balanced invocation-depth guard surrounds an ordered body. Every
command borrows the containing StatementScope, operation and cumulative write
stage. The existing lexical scratch guard gives each command fresh admission,
query/source/presentation/parameter state and refreshes own-write/deletion
visibility from the cumulative prefix. The live replacement reservation,
constraint/rule completion, root effects and transaction stay with their owners.
No command calls Finish or creates an inner publication/commit boundary.

Quoted syntax is parsed when a session first invokes that immutable definition;
the analysis owner retains that syntax so pointer-keyed occurrence manifests
remain stable. Commands are prepared only as reached, retaining descriptions per
routine/command through the existing prepared identity validity mechanism.
Standard commands are refreshed from immutable recipes. Reached transaction
control in quoted functions receives the SQL-function restriction; utility
commands in standard bodies fail at definition. Other unsupported utility
paths retain the existing honest native boundary.

Only the actual terminal nonvoid SELECT or DML RETURNING supplies a scalar.
Its fresh executing binding must have one Assignment-compatible column before
producers run, even with empty data. Eligible final SELECT selects first-row
demand before opening; every preceding/VOID SELECT evaluates required output
fully. All DML executes completely, including every RETURNING evaluation and
auxiliary obligation, before adapting the first row. No result rows produce
declared typed NULL; VOID yields typed SQL NULL and discards all outputs.
A parsed body with no commands is not an empty final query. Nonvoid invocation
rejects it through the final-result diagnostic before allocating or publishing
successful retained analysis; empty VOID bodies remain executable.

Errors/cancellation close owned rows/providers and restore scratch/depth. The
outer statement rolls back transactional prefix writes; sequence increments
retain their nontransactional semantics. Diagnostic context identifies the
actual one-based body command while preserving the leaf primary record.

## Replay

Dump preserves declaration kind, named inputs, VOID and body form. Standard
commands are deparsed only for persistence, using checked current object names
and selected implicit usages. They are never serialized as unchecked quoted
source. Standard declaration events follow their body predecessors, while
quoted bodies preserve signature-only scheduling and existing forward cycles.

Restore stages all declarations/data privately and remaps declaration types
before cross-routine captures. Checked standard programs and hard declaration
defaults retain complete validation and destination identity remapping. In the
private SQL-body window, quoted source stays byte-exact and soft even if lawful
DDL has made its names or result unusable; final replay validation does not bind
that source. Genuine invocation analyzes reached commands and checks their
results normally, including default calls reached by replay data. Any such
execution error aborts the private stage without publishing a replacement.
Stable private routine descriptor handles prevent capture remapping order from
changing callee identity. No frames/plans/ACL decisions are serialized. Reference
coverage requires two native replacement rounds, unchanged native-emitted SQL
replayed on pinned PostgreSQL, and failed/canceled replacement preservation.

Immutable routine presentations alone supply a closed formal namespace to
expression/query remapping. A typed `$n` is retained only inside that namespace
and within its formal bound; generic stored-expression and rule remapping
continue rejecting runtime parameters, including parameters nested in children.
Each original nested query owns exactly one reference and type replacement
through the query mapper's original-project memo. The subsequent scalar type
pass treats that already-replaced query as opaque, in both SELECT and mutation
recipes and in cycle expressions. This matters for non-idempotent replacement
maps: original A→B must not be applied again as B→C; the same rule applies to
descriptor mappings and repeated original-query occurrences.

Replacement also distinguishes retained `ObjectRef` captures from address-only
metadata projections. Actual source/callee/column/literal references pass the
source-domain/epoch check before mapping. Schema `OriginTable` and reg* datum
OIDs use a separate private address mapper: those numbers neither possess nor
create lifetime authority. The same nested mapper threads both roles once.
Generic remap entry points retain their existing address behavior; routine
replacement supplies the stricter capture boundary without rejecting legitimate
schema origins or granting authority to foreign retained references.

## Proof ownership

The owning [reference contract](../../reference/query_core/README.md#ordered-sql-routine-bodies)
lists the actual selected tests, independent values/metadata/diagnostics and
feature-smoke expectations. The root runs generation/formatting, selected native
and real modes, targeted race checks, whole-project quickcheck and standalone
public API smoke. These descriptions are implementation contracts, not passing
verification claims.

Review regressions add `FrozenAuthority` (held standard caller across table and
selected-callee revoke/restore), `NestedPersistence` (named-formal SELECT and
mutation subqueries through both replacement rounds), and `MergeCaptures`.
The latter retains captured ON/WHEN/action/RETURNING choices through rename,
shadows and new overloads, with the dormant callee solely in an unreachable
UPDATE action. A separate exact-key standard MERGE covers existing BY SOURCE
DELETE/DO NOTHING without adding full-join predicate capabilities. Independently
expected pg_depend edges and RESTRICT checks run after each replacement; the
destructive dormant-callee CASCADE runs after the final native round and on
independent PostgreSQL replay states, preserving the original captured program
through both native rounds. Nonpublic tests cover one-step remapping,
malformed/missing/extra/foreign/stale captures and reached-action isolation.
