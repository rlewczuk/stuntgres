# Queries and data manipulation

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## Expression collation and comparison demand

Scalar comparison keeps input collation separately from its noncollatable BOOL
result. Empty/filtered scalar inputs and strict NULL pairs do not demand
comparison; reached implicit conflict fails42P22. Explicit operand annotation
resolves it. CASE's saved test is implicit, so annotate WHEN for precedence.
Local row comparisons evaluate each reached pair left then right, stopping
before either operand of a later pair after a decisive or strict NULL result.
Subquery comparisons retain their separately captured left tuple semantics.
MIN/MAX/GREATEST/LEAST require a second eligible nonNULL candidate to compare.

Key binding differs from scalar demand: ORDER/GROUP/DISTINCT rejects implicit
conflict42P21 during analysis, even for empty input. Join equality keys spanning
both inputs initialize their actual comparison provider at execution, including
empty inputs, while branch-local comparisons keep ordinary scalar demand.
Uncorrelated hashable IN/`= ANY` builds provider-bound membership buckets before
projecting the LHS. Empty RHS returns FALSE (TRUE for NOT IN) without LHS errors,
effects or probe scratch. Successful empty/nonempty tables are retained by the
existing execution owner for their actual RHS plan and captured source occurrence,
not by frozen expressions, stored views, prepared statements or a global cache.
Unchanged children, including constant volatile materialized CTE inputs, retain
their tables across correlated parent rescans. Distinct aliases of the same stored
view remain independent. New Execute always builds fresh, including unchanged
binds; actual outer-dependent RHS/CTE bodies retain their correlated scan route.
Reached RHS hashing reports string-hashing42P22 before any LHS effects. Probes
reuse owner-local scratch and preserve partial-NULL indexes for later probes.
Borrowed stage/CTE-reader closure cannot erase the ancestor table; true execution
completion, errors and cancellation release it. Failed builds are never published.
Correlated forms and ALL retain scan comparison and existing NULL truth tables.
Index eligibility and predicate implication use full resolved input authority;
an incompatible/indeterminate predicate cannot optimize away its demand.
Stored views/defaults/checks/generated expressions retain hidden comparison
authority through dependency accounting, rendering and native remapping.
An ordinary stored-view read maps actual referenced output slots before coercion
preflight: unused nonvolatile payloads are not evaluated just because a sibling
is selected. Execution-local projection and sort/bounded payload cache slots
retain their full typed layout. Demand follows selected and internally read
cache slots to their scheduled producers, including shared/transitive reads;
only unused nonvolatile payloads are omitted. Clause/key demand, volatile
payloads, frozen definitions/dependencies, authorization and snapshot ownership
remain intact. Star/full consumers and materialized refresh retain full demand.
Legality preflight and actual stored-source opening use the same demand-shaped
execution root; neither preflights the full frozen payload first. Nested runtime
reads come from remaining executable producers, not the frozen read manifest.
Lexical frames and physical occurrence spans distinguish repeated aliases of
one relation; captured attribute identities bridge those reads to current slots.
Frozen full manifests retain their existing authorization/dependency roles and
analysis-only CTE edges. Clause predicates, keys and volatile payloads
still demand their genuine inputs across each nested view boundary.
Admitted pass-through stored roots without a payload projection retain their
original source/snapshot stage; referenced slots still validate against the root
schema. They are not wrapped in a synthetic projection.
GROUP BY reuses an equivalent SELECT target's collation provenance, including
its diagnostic location, rather than rebinding the later spelling for key demand.
Owner-dependent stored generation detects tableoid in any operand of any row
pair. Native owner collision/remapping therefore recomputes affected row BOOL
cells even when other pairs contain only ordinary columns.

## Inherited relation scans — change note

`FROM parent` and `FROM parent*` include the named plain table and descendants.
`FROM ONLY parent` or `FROM ONLY(parent)` scan just its physical rows. Qualified,
quoted and aliased names retain the same modes. CTE lookup keeps its normal
precedence, including `ONLY cte`, `ONLY(cte)` and `cte*`; these are not catalog-only
forms. Ordinary filters, joins, subqueries, CTEs, aggregates, views, ordering,
limits and supported query-consuming DML/creation/COPY routes share this source path.

The named parent's columns and full metadata define the result. Descendant
payloads are mapped to that layout; child-only columns are not exposed. A diamond
descendant contributes once per occurrence, but equal-valued physical rows and
self-join occurrences retain bag multiplicity. Use ORDER BY for an order guarantee.
Parent SELECT/column ACL and RLS own logical admission; physical descendant ACL/RLS
is checked only when the descendant itself is a named source. VIRTUAL generated
reads use the parent's recipe; STORED values belong to the physical row.

Stored-view admission reuses the named bases' already validated grants. Ordinary
and frozen ONLY sources do not reacquire those bases; effective rowmarks retain
their stronger relation grants, and missing descendants join the same accounting.

Supported rowmarks lock the actual physical descendant identity and remap
postwait updates before predicate/order/virtual recheck. NOWAIT/SKIP LOCKED and
outer-join absence retain their ordinary semantics. All roots share the sole
statement snapshot. ONLY persists in frozen views and genuine native Dump/Restore;
prepared execution sees later admitted descendants while held eager Rows stay fixed.
Recursive modifying targets, ordinary LOCK and propagating ALTER remain boundaries.
Public tableoid exposure and arbitrary native RLS-policy evaluation are not added.

## String set expansion — change note

`string_to_table(source,delimiter[,null_string])` is the selected TEXT set
provider. It uses the same typed string conversions and field rules as
`string_to_array`, but genuine NULL fields remain present rows and whole
NULL/empty input produces zero rows with retained TEXT metadata.

```sql
SELECT v,n FROM string_to_table('x,,x',',','') WITH ORDINALITY AS t(v,n);
-- ('x',1), (NULL,2), ('x',3); n is INT8.
SELECT string_to_table('a,b',',') AS a,string_to_table('x',',') AS b;
-- ('a','x'), ('b',NULL), not Cartesian.
SELECT string_to_table(string_to_table('a:b,c:d',','),':') AS v;
-- a,b,c,d across successive expansion levels.
```

Functions in FROM may reference preceding FROM columns, implicitly or with
LATERAL. Each correlated invocation evaluates its arguments once for the actual
left row; INNER/LEFT JOIN permit correlation, RIGHT/FULL JOIN reject it.
Independent RIGHT/FULL function operands still work. LEFT JOIN preserves an
empty invocation with typed NULLs. ROWS FROM zips its calls with NULL padding,
and WITH ORDINALITY counts the resulting invocation rows from one.
`AS t(v)` names a column; alias-only scalar `AS t` exposes `t.t`. Aliases hide
original qualifiers. These computed columns have no physical table origin.

Target expansion happens before LIMIT/OFFSET and after unreferenced source
sorting, grouping and windows. GROUP/ORDER/DISTINCT/window-key references demand
their sets earlier, including hidden keys; resolved aliases/ordinals reuse the
expanded occurrence. Plain DISTINCT sees full expanded rows; unrelated DISTINCT
ON may choose a source winner before expansion. Shallower sets repeat at upper
nesting levels, and a NULL lower input may disappear through an empty upper set.
Bare ProjectSet scalar siblings execute on every attempt, including exhaustion;
a maximum-depth scalar wrapper instead puts them in an upper scalar-only stage.
Thus bare nextval plus two fields draws three times without LIMIT, while
nextval plus upper(string_to_table(...)) draws twice. Literal strict NULL
wrappers may fold to a single scalar NULL; runtime NULL wrappers retain emitted
field cardinality.
Within each expansion level, executable target order interleaves scalar attempts
and set calls. Set arguments evaluate once, when their call is first reached for
that input row, not eagerly before earlier scalar targets. Later attempts fetch
the same cursor; exhausted peers pad typed NULL without reopening arguments.
The terminal attempt still runs its scalar events. An earlier failure prevents
later events, and initialized cursors close without replacing that primary error.
For example, scalar-first nextval beside a one-field set whose input also draws
nextval emits (1,'2'); reversing the targets emits ('1',2). Both leave currval3.
Wrapping that set in upper instead leaves the sibling in the upper projection:
the source draws first, the emitted sibling draws second, and currval remains2.
Typed local operands and original marker occurrences are retained as immutable
local-group templates. Every level shares fixed typed marker/scalar slots.
Execution preflight simplifies original roots once in the imported request/view
environment, then reconstructs the local level chain from surviving depths.
Erasing a nested inner call can bring its outer call into the same zip as a
previously shallower peer, without shifting existing ORDER/window references.
The maximum surviving depth determines scalar ownership: bare sets own ordinary
siblings on every attempt including exhaustion; a wrapper at that maximum keeps
them in the owner scalar projection. A shallower or erased wrapper cannot choose
that upper stage. With no surviving calls, one ordinary scalar attempt runs per
input row. Held plans and their analyzed metadata are not mutated.
Scalar ownership is classified after lower-level set occurrences become typed
references. A shallow target such as nextval(...)::text || string_to_table(...)
therefore becomes an ordinary scalar at a deeper bare expansion level, even
though its original root contained a set. Its wrapper runs at its target event,
including terminal attempts; its owner reads the reserved typed scalar result.
A deepest-level wrapper still keeps these roots in the upper, real-row-only
projection. Current-level bare calls remain calls, and scalar fragments inside
their arguments remain once-per-invocation arguments, not independent targets.
Typed non-call owner candidates and their original request identities remain
immutable across this classification and execution-private reconstruction.
Fixed slot layout is storage identity, not execution order: reconstruction retains
the immutable target-event order while filtering events to surviving local calls
and the current scalar owner. Stored capture validates both closed event indices
and their agreement with that template.
Ordinary scalar storage uses an implicit identity layout without a mapping
allocation; explicit sparse candidate maps preserve the same reserved addresses.
An ORDER-demanded target set brings its same-level target peers into the same
zip rather than leaving a late peer to form a Cartesian expansion. Window slot
ownership remains shared through this local rewrite.
View deparse and Dump register finalized visible predecessor slots before
rendering correlated function-source arguments. Nested joins use the existing
left-width offsets in the full owner FROM frame; future/right slots are not
published early, and completed join/alias namespaces remain authoritative.
Stored-query validation distinguishes executable typed expressions from retained
contextual analysis. Set operands may retain unresolved string/NULL literal
occurrences in their original analysis after common-type resolution; those
occurrences are not executable outputs. Runtime expressions still require valid
provider usages, and retained analysis keeps its shape, owner and dependency
checks rather than receiving a generic empty-usage exemption.
Every executable logical-node schema independently validates usage, current
descriptor identity and stored collation authority, including intermediate
project/filter/order/group/window/derived frames. Retained contextual analysis
does not exempt those published or executable schema fields.
Original local-group template roots may retain the same direct contextual
string/NULL occurrences after set-operand coercion. They receive no untyped
reserved scalar slots; actual calls, scalar attempts, marker origins and every
executable schema remain typed and validated. Dependency/capture walks retain
the original roots even when execution simplification erases their demand.
Retagging and capture persistence preserve these descriptor-free literal
occurrences as immutable analysis syntax, without inventing scalar values or
object references. Stored executable occurrences remain invalid. Rename/deparse
proof recreates each view and compares complete results and metadata, rather
than requiring incidental alias or source-column spelling in generated SQL.


Function FROM rows are computed, not physical row-lock identities. Unqualified
locking ignores a function source while locking contributing table/view leaves;
explicit `OF` a function alias/name rejects with `0A000`. A local target SRF
rejects locking even if a strict NULL wrapper later folds it away. Inherited
view locking rejects target SRFs when relation/subquery rowmarks reach that
stored query; directly function-only/source-free views acquire no such marks.
On a peer-updated physical row, replacement rechecks refresh that physical
input and rescan function sources. Correlated arguments use the refreshed
predecessor frame; independent calls reuse their recheck-local materialization
and rewind. Each original candidate contributes at most the first qualifying
replacement row, not a new top-level expansion; ordinal resets on rescan and
later fields can qualify after an earlier field fails WHERE/ON. Existing LEFT
JOIN padding applies when the refreshed function source has zero fields.

Ordinary single INSERT VALUES and INSERT SELECT consume the same target stages
before assignment and atomic write publication. Multirow/decorated VALUES and
scalar-only illegal contexts reject SRFs before runtime demand or folding.
Stored read views retain providers, frames, dependencies and replay metadata;
target-SRF views are not automatically writable. Public results are finalized
before publication and retain their schema across native controls.
General table functions and lateral derived SELECT/VALUES remain excluded.

## Timezone lookup sources — change note

The [timezone lookup views](DIALECT.md#timezone-lookup-views--change-note)
use normal name resolution, filters, projections, ordering, joins and stored
user-view consumers. Empty results retain their exact declared schema.
Without ORDER BY treat results as bags, including independent name aliases;
never infer portable archive or abbreviation iteration order.
Each execution materializes immutable typed facts from captured transaction/session
authority outside the catalog mutex. Repeated leaves use one transaction instant.
PUBLIC SELECT does not confer writes: semantically valid INSERT, UPDATE and DELETE
fail with55000, no affected count or successful result. Existing supported
semantic analysis still precedes read-only rejection: target, predicate, assignment,
source-query and UPDATE/DELETE RETURNING errors are not suppressed. No write
expression is evaluated. Nil text/typed INTERVAL/BOOL comparison operands obey
SQL three-valued logic and produce empty results with unchanged metadata.
Existing scalar VALUES column-reference exclusions remain; INSERT RETURNING is supported.
Row locking remains unsupported for virtual catalog sources. No timezone SRFs
or stored rewrite/deparse surface is added.

## MERGE insertion — change note

```sql
MERGE INTO destination AS t
USING source AS s ON t.id=s.id
WHEN MATCHED AND s.value<0 THEN DELETE
WHEN MATCHED AND s.id>0 THEN UPDATE SET value=t.value+s.value
WHEN NOT MATCHED BY SOURCE AND t.value<100 THEN UPDATE SET value=t.value+1
WHEN NOT MATCHED BY SOURCE AND t.id>0 THEN DELETE
WHEN NOT MATCHED BY TARGET AND s.id>0
  THEN INSERT(id,value) VALUES(s.id,s.value)
WHEN NOT MATCHED THEN INSERT DEFAULT VALUES;
```

Each source occurrence is classified against every captured target occurrence
before the first row wait. Every true ON pair produces a MATCHED candidate;
zero pairs for a source produce one NOT MATCHED [BY TARGET] candidate. If BY SOURCE
is present, each captured target occurrence with no ON match produces one
NOT MATCHED BY SOURCE candidate, not one per nonmatching source. Equal-valued but
distinct target rows are not deduplicated. Within each class, only the first TRUE
arm executes; FALSE/NULL fall through and a matched candidate with no selected
arm never inserts or becomes BY SOURCE. An unconditional arm prevents subsequent
arms of its own class, not the other classes. An empty target demands no ON;
an empty source demands no MATCHED/BY TARGET action or default draw, but BY SOURCE
still visits all targets. Current-command writes never widen frozen membership
or later candidates' original WHEN/SET frames.

ON and MATCHED conditions/SET expressions see both target and source namespaces.
NOT MATCHED conditions/insert values see only source; target references there
are hidden-entry42P01 errors. BY SOURCE conditions see only target, with analogous
inaccessible-source diagnostics. SET RHS and array element/slice bounds use one
unchanged old frame; partial writes compose in their private replacement.
BY SOURCE conditions, RHS, correlated scalar subqueries and array bounds see
only target variables. References to the absent source are inaccessible-entry
errors without an invented LATERAL hint; an independent FROM relation/local
alias is not banned. UPDATE-only actions do not require INSERT authority.
Specifying both BY SOURCE and BY TARGET requires FULL-join legality at Execute:
an effective hash/merge-joinable cross-side key or supported constant condition,
not merely an equality anywhere in ON. FALSE arms/empty inputs do not waive it.
Generated values recompute from the completed payload. Existing UPDATE DEFAULT,
generated/identity restrictions and field/row assignment boundaries remain.
All arms are analyzed and authorized even when false or empty. Separate INSERT
and UPDATE destination unions, presence of any DELETE action, actual target reads
and recursive source SELECT govern privileges. Constant DELETE predicates do not
invent a target-column SELECT demand. Sequence draws remain demand-owned; DELETE
evaluates no replacement defaults/generated values.

Cardinality measures actual modifications: false conditions consume no RowID,
same-value updates do, and a second attempted UPDATE or DELETE raises21000
`MERGE command cannot affect row a second time`, with the source-multiplicity
Hint. Projection/row-constraint errors can precede that check; unique conflicts
use23505, including duplicate unmatched inserts. Scalar-subquery21000 retains
its distinct primary and no MERGE Hint. Mixed actions check unique constraints
in actual candidate action order against the accepted logical prefix and remaining
original rows. DELETE retires its unique/exclusion keys before subsequent writes;
a later DELETE cannot forgive an earlier violation. Key swaps are not deferred.
Any late error rolls back the whole command. Source ORDER BY alone is not a promise
of candidate order when a PostgreSQL equality join chooses a different output order.

Pinned PostgreSQL18.0 has a nonboolean ON quirk: integer zero and NULL do not
match, while nonzero integer and a non-NULL unknown string do. MERGE implements
these observed carriers locally; WHERE/JOIN/WHEN boolean rules do not change.
Physical source xmin/ctid/cmin/xmax/cmax in WHEN are42P10; tableoid is exempt from
that restriction. General positive system-column providers are unavailable
through the native query binder (ordinary42703), not fabricated identities or
NULL placeholders; pinned PostgreSQL independently supplies actual identities.

Source and target share one captured frontier including prior successful own
writes. Required source Close succeeds before action row locking.
DELETE takes FOR UPDATE strength; UPDATE initially uses FOR NO KEY UPDATE and
upgrades for an actual changed eligible key. A peer-update follow-chain additionally
uses the syntactic UPDATE-key union and DELETE strength. Read Committed rechecks ON
against the latest target and retained source for a formerly matched pair.
An initial MATCHED candidate losing ON may execute BY SOURCE for its target and
then BY TARGET for its retained source. An initial BY SOURCE candidate remains
that class even if a peer makes ON true. For an initial BY SOURCE UPDATE,
pinned18.0 concurrent deletion can select BY TARGET with typed NULL source,
independently of captured real-source candidates. The integrated BY SOURCE
DELETE lane retains its concurrent-delete skip contract. Current-command
deletion participates in cardinality, never insertion. No restart widens
membership. Unique-key claimant waits never restart ON.

MERGE's Repeatable Read update path follows the latest committed predecessor
instead of ordinary UPDATE's concurrent-update40001. If its own replacement
base differs from the pinned snapshot VersionID, public SELECT sees both the
old snapshot version and its own descendant, with the same logical RowID; the
intermediate peer version remains invisible. It neither advances the frontier
nor creates a new INSERT. A concurrent deletion still raises40001
`could not serialize access due to concurrent delete`.
Deleting an own descendant retires that version and its replacement base, not
an unrelated old snapshot occurrence sharing the same logical RowID. For the
pinned old10/own100 bag, own100-only DELETE leaves original10 visible; a later
command still rechecks against its actual current predecessor.

After all demanded actions/constraints/cancellation checks succeed, ordered
private runs reuse canonical replace/insert/Delete staging and the sole Finish owner.
Errors, required-Close failure and cancellation publish none; explicit failed
transactions require rollback/savepoint recovery. Read-only MERGE fails25006
even for matched/FALSE/empty commands after analysis/input preflight. Held
statements reanalyze current names, assignments and ACLs. Command Query exposes
no columns or rows. Native RowsAffected sums updates, inserts and deletes; pinned lib/pq
exports0, so witnesses independently inspect exact effects. LastInsertId is unsupported.

Dump persists ordinary object definitions, rows and sequence state, not MERGE
statements or cursor caches. Native Reset/Restore replacement and replay of the
actual emitted SQL on PostgreSQL are covered separately from SQL execution.
This remains process-local logical behavior, not disk/WAL crash durability,
wire-protocol compatibility, external pg_dump equivalence or native ABI support.
Both missing-side families require a FULL-joinable condition. Constant TRUE/FALSE/NULL
and equality witnesses are admitted, as are MERGE-local row-independent integer/
TEXT carriers (including typed parameters/domains): nonzero integers and non-NULL
TEXT match, while zero/NULL do not. The analyzed ON expression remains intact and
ordinary JOIN/WHEN Boolean rules stay unchanged. `<` raises the ordinary FULL-join
0A000 even when a table happens to be empty. BY SOURCE INSERT, DO NOTHING, RETURNING,
action INSERT SELECT/multirow tuples and writable target views remain outside this feature.

The exact correlated scalar array assignment-bound case caused EOF/server
recovery on pinned18.0; it is not an SQLSTATE golden or a claimed parity result.
Public array witnesses retain old-frame composition using target column bounds
and an independent scalar bound. A private actual native executor test preserves
the correlated `(SELECT t.id)` bound and expected `{20,10,30}` swap.

## Column-default catalog — change note

`pg_catalog.pg_attrdef` is readable through direct and prepared SELECT, including
empty results. Its complete projection is `oid OID`, `adrelid OID`, `adnum INT2`,
`adbin PG_NODE_TREE`; native nullability is known NOT NULL for every field.
The PostgreSQL pq adapter does not expose known nullability. Native OID/INT2 raw
cells are int64 and tree cells are strings; pq OID/tree cells are byte payloads
and INT2 scans as int64 despite its int16 ScanType.

Rows identify the stored definition, actual owning relation, and stable owning
attribute number, not a compacted column position. Supported ordinary defaults
and stored generated expressions appear; bare NULL and NULL::integer ordinary
defaults and identity columns do not. Generated NULL expressions remain stored.
Domain-coerced NULL defaults are a documented native producer limitation (see
[tables](TABLES.md)), not repaired by this projection.

Introspection does not evaluate a default, including a dormant narrowing error.
Rename preserves definition identities; SET/re-add DEFAULT assigns fresh ones,
and DROP DEFAULT/column/table removes the corresponding rows. Held prepared
catalog queries see current definitions; already-open native eager cursors retain
their captured rows and schema across native Reset/Restore. Logical Restore may
remint relation/definition identities and attribute numbers: use current owner
relationships, not equality to a prior allocator sequence.

Native PG_NODE_TREE is an immutable nonphysical structural carrier, not SQL TEXT
or PostgreSQL nodeToString interchange. Use `pg_get_expr(adbin,adrelid)` for SQL.
Catalog resolution errors retain SQLSTATE, English diagnostics and character
positions through direct Query and prepared Prepare failures.

Dedicated acceptance:
`make check RUN='^TestReferenceAttrdef'`
and `make check REFERENCE=1 RUN='^TestReferenceAttrdef'`.
Feature smoke:
`make check RUN='^TestReferenceAttrdefControlsSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceAttrdefControlsSmoke$'`.
The smoke exercises actual default/generated data, typed catalog identities,
failed/canceled Restore preservation, Reset emptiness, retained raw catalog
cursors, Restore and future defaults; PostgreSQL executes the actual native
Dump SQL and independently checks its catalog/data effects. Native controls are
host mechanisms, not PostgreSQL APIs or physical pg_dump/durability emulation.

## Query-created table consumers

`CREATE TABLE ... AS query` and top-level `SELECT ... INTO name` use the same
analysis, source manifest, parameter inference, snapshots, source ACLs, row marks
and eager finalization as current SELECT. Supported joins, derived/scalar and
predicate subqueries, grouping, DISTINCT, ordering/bounds, views, catalog sources
and query VALUES retain their existing capabilities and exclusions. The sink
stores finalized typed values, never adapter-rendered text or a second evaluation.

Prepare analyzes sources and parameters but creates no table and executes no
producer. Direct and SAME held Execute reanalyze current source definitions;
the command has no public result schema, so a held SELECT-star creation can
derive the current destination shape. Parameters still use the existing Bind
count/type/codec contract. Destination existence, names, storage validity and
permissions are checked when the command executes.

Native query-creation utility admission precedes snapshot acquisition: explicit
Prepare rejects with `0A000`, and Execute rejects read-only work with `25006`
before the explicit-block exclusion. Neither enters a serializable read-only
deferrable safe-snapshot wait. Conn-prepared handles converted by `Tx.StmtContext`
retain their sticky Prepare exclusion. With an unresolved serializable writer,
PostgreSQL instead requires a safe snapshot during analysis; a statement timer
reports `57014` on direct Execute, explicit Prepare and that held route.
Ordinary SELECT and permitted standalone query creation retain the snapshot owner.

`WITH NO DATA` still analyzes names, operators, types and literals and imports
arguments. It skips planning-owned conversions/folding, executor source SELECT
checks, data capture, runtime row marks and producer evaluation—not an evaluated
query with discarded rows or LIMIT0. Analysis may acquire source relation locks;
source namespace/type authority still applies. A valid existing IF NOT EXISTS
destination skips before schema CREATE permission, preserves its definition/data,
returns count0 and emits one Execute notice. Source analysis remains active.

Populated execution finalizes the producer once before atomic destination
publication. Required source Close errors veto success; a primary query failure
is not replaced by a cleanup failure. No partial caller rows or affected count
escape a failed command. Exec returns the stored occurrence count; Query returns
zero columns and EOF, including zero-column tables with a nonzero stored count.

Only an enclosing top-level SELECT promotes INTO, including its leftmost set
arm. Right-arm INTO reports42601 with the first-SELECT diagnostic; ordinary nested
and CTAS-body INTO report42601 `SELECT ... INTO is not allowed here`. Views retain
their own0A000 `views must not contain SELECT INTO` boundary at utility Execute
in both backends. Set producers use the existing set owner, including
pairwise common types, leftmost output labels and DISTINCT/ALL tuple multiplicities;
CTAS validates its final output collation even with WITH NO DATA.

Source `pg_prepared_xacts.transaction` retains its concrete XID Usage in stored
query output; ordinary canonical string/held inputs and native controls use the
existing unsigned codec. This does not enable raw XID type syntax or casts.
`pg_attrdef.adbin` remains readable as PG_NODE_TREE for its existing deparser, but
the native sink rejects that structural carrier at descriptor Execute, for both
DATA and NO DATA, rather than storing an opaque key as replayable SQL data.


## Constraint and expression deparsing

`pg_get_constraintdef(oid)` and `pg_get_constraintdef(oid, boolean)` return the
creating-command suffix for supported CHECK and NOT NULL constraints.
`pg_get_expr(pg_node_tree, oid)` and `pg_get_expr(pg_node_tree, oid, boolean)`
return the stored expression as TEXT. All four functions are Stable and strict:
NULL in any argument produces typed TEXT NULL; omitted pretty means false.
Pretty changes presentation, not the authoritative expression or its evaluation.
Stored expressions are never evaluated by introspection, including dormant errors
and volatile sequence/random providers. Canonical native replay remains separate.
Boolean constants render as SQL `true`/`false`, not their text-codec `t`/`f`.
Synthetic numeric comparison annotations use the descriptor's base Usage; an
opposite NUMERIC(4,2) or NUMERIC(2,-1) column does not size the comparison constant.
Real explicit typmod coercions remain sized and may fail at execution with22003.
Assignment-only casts stay invisible. Omitted random_normal arguments remain
omitted in the bound call and use their defaults only during evaluation.

Comparison operands retain required grouping in both modes, including lowered
BETWEEN predicates used under another comparison. Grouped predicates are valid
comparison operands; ungrouped comparison chains and Boolean arithmetic are not.
Ordinary function arguments show their bound implicit casts, unlike operator
Vars and assignment sizing. Regclass spelling uses the statement's captured
catalog and effective relation path: a shadowed provider is schema-qualified,
while a visible provider is unqualified regardless of its namespace.
The canonical presentation contract renders negative DISTINCT as logical
`NOT ... IS DISTINCT FROM ...` and renders a CASE's authoritative ELSE result,
including an inferred typed NULL when the source omitted ELSE. Pretty arithmetic
uses parent/operand-side grouping rather than flattening the stored tree: left
equal-priority children can omit groups, right children retain them; function and
CASE argument boundaries separate expressions. Comparison operands still retain
their required groups. These are presentation rules, not new operators or Eval.

Standard CAST and postfix `::` consume same-type annotations/typmods for admitted
stored scalar types and existing directional integer/numeric/float conversion
edges. They do not add generic known-TEXT input conversions, transitive cast
search or function-form casts. `NULL::unknown` (or standard CAST of literal NULL)
is a bounded syntax carrier, not a concrete UNKNOWN value/input/column type.
Polymorphic null-count arguments retain this provenance through native replay.

Typed TIME/TIMESTAMP literals retain the pre-precision constant plus the ordinary
typmod coercion: output may contain `.1234::time(3)` while execution yields `.123`.
Unknown-string defaults similarly retain preassignment values and omit destination
sizing in presentation. INTERVAL input is the exception: its field/precision
typmod applies at input and remains on the stored literal. Signed literal constants
retain checked constant identity rather than becoming evaluated arithmetic.
Literal output uses captured output settings and plain SQL quotes, never an E
prefix; backslashes double only with standard_conforming_strings off.


```sql
SELECT pg_get_constraintdef(oid, true)
FROM pg_catalog.pg_constraint WHERE conname = 'positive';
SELECT pg_get_expr(adbin, adrelid)
FROM pg_catalog.pg_attrdef;
```

The supplied relation resolves stored stable attribute numbers against its current
columns, even when names/types differ from the original owner. OID zero permits
Var-free trees; a tree containing Vars reports22023 `expression contains variables`.
A missing nonzero relation returns NULL even for a constant tree. Missing
attributes reportXX000 `invalid attnum N for relation "name"`. Missing constraint
IDs return NULL. Rename preserves identities and updates decompiled spelling.
Saved tree values retain immutable structure after their owner is removed.

Non-NULL external `pg_node_tree` input reports0A000
`cannot accept a value of type pg_node_tree`; NULL needs no input decoding.
Literal input failures retain their source character Position. Parameter codec
failures retain the shared adapter's unnamed-portal parameter Context: nonempty
tree input is redacted as `'...'`, while empty input is shown as `''`.
Incorrect names, arities and known argument types use ordinary42883 resolution
with the PostgreSQL function Hint and source character Position. Bounded
`pg_get_*` parser admission does not add providers. Ordinary relationless SELECT
now uses the same typed query pipeline as table-backed SELECT. Prepared calls use
current statement catalog authority and import typed arguments on execution.

Executable reconstruction fixtures consume actual compact/pretty outputs before
checking conditional acceptance/NULL/errors, controlled COMMENT state, and each
backend's own seeded random continuation. A failed row publication preserves
old row values but does not rewind random-provider effects. Held statements
recapture comment, search-path and TimeZone state. current_schemas(b) IS NOT NULL
checks strict BOOL NULL, not path membership; path reversal is observed separately
through public NAME[] output. NAME[] comparison/order and plain TIME AT are not
added by reconstruction; admitted AT inputs are TIMESTAMP/TIMESTAMPTZ/TIMETZ.

Feature smoke: `make check RUN='^TestReferenceDeparseConstraints'`.
Dual-mode deparse assertion unions:
`make check RUN='^TestReferenceDeparseConstraints[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceDeparseConstraints[A-Za-z0-9_]*$'`.
Returned SQL is consumed through actual direct/prepared callers, not just compared
as text. Native public restore and PostgreSQL consumer phases remain independent.
Source-consumer phases preserve original QueryRow typed/deparse reads, actual
held statements through all failing bindings and SAME-statement recovery,
post-rejected-write sentinels and numeric-sizing descriptors on original/copies.
Original physical Conn.QueryContext adapters remain real pinned-Conn consumers,
not replacements with pooled DB.Query. Metadata callbacks must inspect the
actual cursor; generic row helpers alone do not prove typed Scan or sizing.



## LIMIT, OFFSET and FETCH — change note (changelog)

Table-backed and no-FROM SELECT, stored view definitions, the shared INSERT SELECT
producer and standalone/FROM/decorated VALUES accept `LIMIT expression`,
`LIMIT ALL`, `OFFSET expression [ROW | ROWS]`, and
`FETCH FIRST | NEXT [count] ROW | ROWS {ONLY | WITH TIES}`. Omitted FETCH count is1.
OFFSET and LIMIT/FETCH may appear in either order, after ORDER BY. The complete
locking clause list may precede or follow the count decoration.
Comma-style `LIMIT n,m` remains excluded.

Change note: WITH TIES returns every additional peer of the last quota row,
using equality of the **complete resolved ORDER BY** with its owned type and
collation semantics. Two NULL keys are peers; NULL and nonNULL are not. Hidden,
computed, alias and ordinal keys retain the existing ORDER cache (volatile keys
are not reevaluated). Payload/projection equality does not define peers.
ORDER BY must belong to this query, not an ordered subquery/view. Missing ORDER
gives42601 at WITH; SKIP LOCKED is incompatible, while FOR UPDATE/NOWAIT is legal.
Keys1,2,2,3 with count2 return1,2,2. OFFSET2/count1 returns one2, not the skipped peer.
Raw NULL (including grouped NULL) count gives2201W under ties; parenthesized
expressions/casts and parameter counts evaluating NULL remain unlimited.
Raw-NULL SELECT analysis occurs at Prepare; CREATE VIEW is utility-only at Prepare
and checks its body at Execute in both backends. Both reject the same raw-NULL
count with the complete2201W diagnostic.

Eligible presence-only EXISTS queries with a proven positive literal count retain
the analyzed bounds/ORDER dependency graph but bypass its bounds and peer/cache
production. Ignored computed or volatile ORDER keys are not evaluated merely to
answer presence. Zero and parameterized counts keep ordinary bounds decisions.

```sql
SELECT id FROM t ORDER BY id OFFSET 1 FETCH NEXT 2 ROWS ONLY;
INSERT INTO dst SELECT id FROM t ORDER BY id LIMIT 2 OFFSET 1;
INSERT INTO dst VALUES(7),(8),(9) OFFSET 1 LIMIT 1;
```

These return/insert occurrences2,3 and insert8, respectively. Unordered winners
and complete sort peers have no portable order. Bounds bind in the real input
namespace but cannot depend on current-query rows or aggregates. Child queries
retain their own scope and may reference a legal enclosing frame. Unknown parameters infer
INT8; already typed integer/OID/numeric/float values use the existing BIGINT
assignment conversion, including NUMERIC half-away and floating half-even
rounding. Known TEXT (even NULL) and BOOL do not become unknown strings.
This does not admit additional explicit casts or default functions. Typed
NUMERIC/FLOAT4/FLOAT8 providers can come from parameters whose usage was inferred
by an earlier clause. A fractional value supplied to a parameter inferred as
BIGINT instead uses bigint input and is rejected, rather than receiving a
floating assignment cast.
Raw unknown strings use bigint input. Numeric special values retain the
provider's0A000 conversion diagnostics; floating specials/overflow give22003.
Variable bounds give42P10, aggregate bounds42803, incompatible types42804.
Negative OFFSET gives2201X; negative LIMIT/FETCH gives2201W, without Position.

Each execution evaluates OFFSET, then LIMIT, once, even on empty input and
regardless of textual order. NULL OFFSET is0; NULL LIMIT/ALL is unlimited.
Negative OFFSET wins over a negative LIMIT or LIMIT0. Count0 opens no producing
child, but does not prune schema, ACL, static analysis/capture, imported
conversion preflight or its own bounds. Empty/past-end results remain nonnil
cursors with complete normal metadata, including zero-column occurrences.
Join coercion preflight visits both operands independently of an empty sibling;
it validates row-independent input without demanding either row producer.

The private shared occurrence producer stops nonblocking VALUES, predicates,
joins and nested scalar/security-barrier view projection at demanded survivors.
It does not push an outer predicate into a view or replay effects across its
boundary. Sort and grouping drain qualified input when demanded. Cheap immutable
computed targets below Sort can therefore fail on a discarded tail:
`SELECT 10/v FROM t ORDER BY id LIMIT 1` still fails if any input v is0.
An unsorted LIMIT1 avoids that tail; LIMIT0 avoids all producing target/key/
predicate work. Unrelated volatile targets run after Sort but below the count
frontier, so OFFSET consumes their effects; an ORDER-referenced target shares
its existing cache instead of drawing again. Public execution remains eager and
atomic: `Rows.Next` never resumes SQL work or exposes a successful prefix.

ONLY stops without pulling a tail row. Finite positive WITH TIES instead pulls
the first nonpeer before stopping, so that excluded lookahead can execute source
projection effects or acquire a rowmark. Errors/cancellation on it still veto
the complete result; no successful prefix is published. A fresh projected
nextval over keys1,2,2,3/count2 returns draws1,2,3 and leaves the next draw5.
Count0 pulls no child; all-peer EOF needs no additional occurrence.

The selected time functions follow this same demand boundary: now() and
transaction_timestamp() share a transaction instant, statement_timestamp() is
one command instant, and clock_timestamp()/timeofday() draw per demanded
occurrence. Reusing an ORDER alias reuses its produced value; spelling another
wall-clock call is a separate occurrence. LIMIT0 still analyzes signatures and
metadata without drawing a runtime clock. Prepare and stored view creation do
not freeze values, and introspection never samples these providers.


Pending stored rowmarks pass through scalar qualification, including a mark
owner's own ORDER, but settle before a consuming outer Sort or aggregate.
A local count is an irreversible frontier: marks settle before skipped and
returned survivors consume OFFSET/count. Outer rejection cannot replace an
inner LIMIT winner or erase its holds. Direct/inherited top-level rowmarks
follow their own Sort and stop after enough survivors; stored marks below an
outer Sort can hold all consumed input, even under an outer LIMIT1. Deleted or
requalified-away candidates refill without spending count. See
[row-lock frontiers](TRANSACTIONS.md#row-locks--change-note).
Unmarked view siblings retain execution-local occurrence and lineage identity
for replacement recheck without inheriting an otherwise absent lock mode.
Grouped sibling outputs are frozen values, not aliases of physical input rows.
Locked replacements keep the original sorted rank, but WITH TIES compares current
replacement keys. Hidden computed peer keys refresh through the existing ORDER
cache owner once per shared cache identity, under the same dependency/volatility
rules as visible keys. Peer comparison itself never evaluates expressions.
Ordinary ONLY does not acquire new hidden-key replacement demand.

INSERT retains common source typing, all-source ACL and destination validation
even at0, including existing destination constant/default simplification. A
constant default `1/0` therefore fails during execution preflight even with
LIMIT0; zero does not suppress an admission failure. Only selected destination
rows undergo runtime assignment/default/constraint work, and one complete batch
reaches WriteStage. Stored bounds persist typed
definitions/dependencies, survive renames and native Dump/Restore, and may
canonicalize FETCH ONLY to LIMIT and ALL to NULL. WITH TIES retains
`FETCH FIRST (<count>) ROWS WITH TIES` in both structural SQL and view definition
presentation, so replay and Dump/Restore preserve peer semantics. Held public
exports remain stable through later execution, Close and Reset/Restore.
Exported definitions are checked by executable replay and independently expected
peer rows, not identical whitespace, aliases or ORDER ordinal presentation.
Cancellation witnesses wait for database/sql's asynchronous PostgreSQL cursor
closure before testing sticky Next; native eager cursors reject immediately.
Canceled-before-execution queries are checked on both backends and cause no effects.

Feature smoke: `make check RUN='^TestReferenceQuery(Ties(FeatureSmoke|PersistenceSmoke)|LimitMigrated(Smoke|LockSmoke))$'`.
Independent oracle: run the same command with `REFERENCE=1`.

## Query labels and relation aliases — diagnostic fidelity change note

FROM column aliases and output labels have different roles:

```sql
SELECT x.iid AS shown, x.a
FROM t AS x(iid)
ORDER BY shown;
```

The positional FROM list renames only the first input column; the remaining
columns keep their names. `shown` is an output name, not a new input column.
Output labels are unavailable to sibling targets and WHERE. A bare ORDER BY
label prefers the output: `SELECT a AS b FROM t ORDER BY b` sorts by a, whereas
`ORDER BY t.b` or `ORDER BY b+0` uses the physical input b. A WHERE b predicate
also uses input b. Table aliases hide the original qualified name, including
schema-qualified references.

Missing-column diagnostics now consider the exposed FROM column names:
`SELECT x.id FROM t AS x(iid)` fails with42703 and can suggest `"x.iid"`.
PostgreSQL18's case-sensitive character edit distance and qualifier penalty
govern suggestions; one or two best occurrences may produce a Hint, while
three tied occurrences or distant names suppress it. Suggestions never make
hidden names visible. SQLSTATE, primary English, optional fields and one-based
character Position are checked independently against pinned PostgreSQL18.0
UTF8/C, including Unicode names/comments and empty input analysis.

Exact inaccessible base columns take precedence over spelling suggestions.
For `t(id)` and `u(k)`, `SELECT id FROM (t CROSS JOIN u) j(iid,k)`
reports42703 with Detail `There is a column named "id" in table "t", but it
cannot be referenced from this part of the query.` and no Hint. Two or more
hidden exact matches use the plural inaccessible-columns Detail; an unqualified
request also gets `Try using a table-qualified name.` This exact-match branch
is distinct from fuzzy tie suppression and still does not expose hidden names.

Within one hidden base entry, two exposed columns with the requested exact
spelling instead raise42702 `column reference "id" is ambiguous` at the
requested column's character Position. This ambiguity check is independent of
qualifier penalty: it also applies when the requested visible join qualifier
differs from the hidden base alias, even by more than the fuzzy distance bound.
All base entries are checked before inaccessible-column Detail is selected;
two earlier exact entries cannot conceal a later duplicate-column ambiguity.

Aliases retain physical types, typmods, collation, origin and known nullability;
output labels do not change column privileges. Held statements use fresh inputs
and parameters, including NULL and empty results. Aliased view definitions use
the existing native Dump/Restore path; finalized results survive replacement.
This is a diagnostic correction and focused alias coverage, not new DISTINCT
or grouping support.

Public feature smoke: `make check RUN='^TestReferenceQueryAliases'`.
Dual-mode alias union:
`make check RUN='^TestReferenceQueryAliases[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryAliases[A-Za-z0-9_]*$'`.
Both backends assert complete diagnostics; the separate PostgreSQL column-ACL
companion is `TestReferenceQueryAliasesPostgresColumnACL`. A postgres subtest
filter on a shared scenario is not a substitute for native coverage.

## Distinct projected rows — change note (changelog)

Ordinary `SELECT DISTINCT expression [, ...]` deduplicates the
**complete projected tuple**, after WHERE and grouping/HAVING and before
ORDER BY, OFFSET and LIMIT. Both physical FROM sources and the no-FROM local
input use this path; `SELECT DISTINCT 1` returns one row. Each visible expression is evaluated once per demanded source
occurrence, including volatile expressions; ordering reuses the projected datum.
All keys must finish before a positive LIMIT can publish rows. LIMIT0 retains
binding/type/authority checks without demanding runtime keys.

Two NULLs are equal in each projected position; one NULL and one non-NULL are
different. Equality follows the represented type's key contract: numeric scale,
floating NaN/signed zero, BPCHAR padding, temporal/interval and projected REGCLASS
identity retain their existing semantics. REGCLASS literal providers can be
projected over a table source; DISTINCT does not add REGCLASS column storage or
TEXT-column-to-REGCLASS casts. Recursive array keys include rank, extents, lower
bounds and NULL-aware element equality, including domain carriers.
Enum and enum-domain keys use checked declaration ordinals, including recursively
inside admitted projected domain-element arrays. This does not add enum-array
column storage or ordinary array operators.
Immutable `pg_node_tree` carriers use their binary-compatible structural text
equality representation, not payload-pointer identity. Result labels, logical
usages, typmods, collation and physical origin/nullability survive row elimination,
including empty results. No implicit output order or representative spelling is
promised; use an explicit ORDER BY where ordering matters.

ORDER keys must resolve to a projected expression: a standalone output alias,
ordinal or exact projected expression is accepted. An unprojected input column or
an expression merely derived from a projected value fails42P10. Existing output
alias precedence and ambiguous/missing-name/ordinal diagnostics remain unchanged.
An alias or ordinal retains its exact visible occurrence: with two independent
`random()` projections, `ORDER BY` the second alias or ordinal2 reads the second
retained draw, including through persistent views. Equivalent expression syntax
does not merge independently visible volatile occurrences.
VOID projections fail analysis42883, even on empty input. All four FOR locking
modes fail0A000, including inherited locking through a DISTINCT view.

Persistent views, held/prepared execution and native Dump/Restore share this
pipeline. INSERT SELECT deduplicates **source-typed** keys before destination
assignment: different numeric source values that round to the same INTEGER still
insert separate occurrences. OVERRIDING USER VALUE cannot prune a DISTINCT key.
Failure/cancellation publishes no partial native cursor or INSERT batch.
Ordinary array comparison operators/ordering, general FROM expansion and
spill/planner machinery remain excluded.

Feature smoke:
`make check RUN='^TestReferenceQueryDistinctSmoke$$'`.
Independent pinned18.0 contract:
`make check REFERENCE=1 RUN='^TestReferenceQueryDistinct'`.

## Distinct on groups — change note (changelog)

`SELECT DISTINCT ON (key, ...)` retains the first occurrence of each complete
resolved typed key tuple after the explicit ORDER and before OFFSET/LIMIT/FETCH.
Unlike ordinary DISTINCT, hidden keys are legal and unselected payload columns do
not need equality. NULL components compare equal; composite tuples with another
different component remain distinct. Numeric scale, NaN/signed zero, BPCHAR padding,
enum/domain and structural array/JSONB/tree keys use existing type-owned grouping.
Equality does not add ordinary array/tree ordering capabilities.

Use a total order to choose portable winners, for example:

```sql
SELECT DISTINCT ON (k) id, payload
FROM events
ORDER BY k ASC NULLS LAST, stamp DESC, id DESC;
```

This returns the newest timestamp per key, breaking timestamp ties by descending id;
NULL keys form one group. Without ORDER or with unresolved peers, the winner is a
valid source member but is not pinned to a particular row or incidental plan order.

ON resolves bare output labels before input names, qualified/compound expressions
against input columns, and integer ordinals against expanded visible outputs.
Hidden keys never increase visible width. Exact output occurrences retain analysis
identity even for structurally identical volatile expressions. Equivalent duplicate
labels choose the first; unequal labels fail42702 with DISTINCT ON wording.
This first-match rule includes independently analyzed equivalent scalar-subquery
outputs under the same label: `(SELECT k FROM only_key) AS x` and
`(SELECT only_key.k FROM only_key) AS x` resolve through ON(x) and ORDER BY x.
Both outputs retain their own analyzed occurrences; unequal subqueries report
42702 with the referencing clause's wording and original label position.
Ordinary SELECT with ORDER BY x uses the same first-label rule but retains all
outer rows; equivalent scalar outputs do not introduce ON-style deduplication.
SQL99 target reuse compares resolved semantics, not expression spelling. Scalar
subqueries selecting `k` versus `only_key.k` from the same source can match; a
correlated outer `e.k` does not match the inner key merely because names coincide.
Comparison pairs generated query-local frames and source occurrences, preserving
physical source identity, typed operators, source/column alias namespaces and
unmapped outer correlations. Changing an inner source alias from `q` to `r` does
not make the subqueries the same SQL99 target. Both occurrences remain fully
analyzed and authorized even when they share a computed datum.

The effective ORDER removes repeated resolved targets, retaining the first direction
and NULL policy. For ON(a,b), ORDER b,a,c or ORDER a alone is legal; ORDER a,c,b or
ORDER a,c is not. Matching keys may occur in any permutation before a non-ON target.
Missing keys after a gap fail42P10, `SELECT DISTINCT ON expressions must match initial
ORDER BY expressions`, at the original troublesome ON expression, including Unicode
character positions. Algebraic variants such as a and a+0 are not equivalent.

Grouping/HAVING and windows run before ON. FETCH WITH TIES uses the actual user ORDER
peer definition, not omitted ON keys. Required keys share computed values with
referenced outputs. Cheap immutable payloads can execute below sort/ON, so a losing
row's division error remains observable. Unrelated volatile payloads are winner-only;
an unneeded volatile expression structurally matching a required target may reuse
that datum. The pinned oracle's ON(y) random x/y witness yields x=y/four draws, while
ON(1,2) demands two distinct occurrences/eight draws. This physical reuse does not
erase analysis identities or allow ON(x) ORDER y.

Binding, equality capability and physical SELECT authority remain mandatory for empty,
WHERE FALSE, all-NULL and LIMIT0 inputs. JSON payloads are legal with integer keys;
JSON/VOID keys fail42883 independently of row demand. Explicit ORDER capability
diagnostics retain their ORDER position and Hint. All four FOR modes reject with
mode-specific0A000 without a synthetic Position.

Direct, fresh prepared and repeated held execution preserve visible metadata and
fresh per-execution seen state. INSERT selects source-typed groups before assignment
or identity generation. Actual ON views retain frozen output ordinals, hidden physical
and USING keys and ON-only subquery dependencies across native Dump/Restore. Failed
key/payload evaluation, cancellation and staged Restore publish no partial native
cursor, INSERT batch or replacement catalog; sequence effects are not rolled back.
Stored source namespaces are selected by their analyzed query frame and node
identity, not by the order in which ON and ORDER SQL happens to be emitted.
Equivalent sibling subqueries receive stable generated source/column aliases;
separate lexical depths preserve outer correlations during executable replay.
Ordinary and materialized definitions capture ON keys and independently analyzed
ON/ORDER scalar-subquery frames, including WITH NO DATA materialized definitions.
Populated materialized winners remain snapshots after source writes and native
Dump/Restore; unpopulated reads retain 55000 and the REFRESH hint. Materialized
bound-parameter rejection and failed-population atomicity remain unchanged.

Both complete reference definitions and feature smoke require separate900-second runs:

```sh
make check RUN='^TestReferenceQueryDistinctOn'
make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOn'
make check RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
make check REFERENCE=1 RUN='^TestReferenceQueryDistinctOnFeatureSmoke$$'
```


## Expression ordering — change note (changelog)

SELECT and shared INSERT SELECT/ORDER-decorated VALUES now support comma-separated
ORDER BY expressions, output labels and one-based visible-output ordinals. Each
key independently accepts ASC/DESC and NULLS FIRST/LAST. ASC defaults to NULLS
LAST; DESC defaults to NULLS FIRST. Later keys break earlier ties; complete peers
and queries without ORDER BY have no portable relative-order promise.

Scalar outputs accept AS labels or eligible bare labels. Labels change only the
public name, retaining type/typmod/collation and physical origin/nullability.
Star expansion and repeated targets count toward ordinals; hidden keys do not.
A bare ORDER label prefers output over input. Equivalent duplicate labels are
legal; distinct matches give42702 `ORDER BY "label" is ambiguous`. Qualified
names and compound expressions use the input namespace, not output labels.
GROUP BY retains input-name priority; labels are not installed in WHERE/HAVING.
Invalid positions give42P10; raw noninteger constants give42601. Unary plus is
an expression, not an ordinal; signed lexical int32 boundaries remain unchanged.

ORDER keys bind before grouping determination, including hidden count(*).
Ungrouped leaves retain42803, hidden physical columns retain SELECT ACL checks,
and VALUES orders its common-typed source before destination assignment.
Computed keys are evaluated once per admitted occurrence before sorting.
Structurally equivalent keys share that datum even when both keys are hidden;
direction and NULL placement remain independent. Referenced equivalent visible
outputs reuse the same datum. Unaffected visible outputs remain occurrence-sensitive
and are evaluated after sorting: unrelated duplicate nextval outputs are not
globally merged. Selected nextval ORDER labels/ordinals return the evaluated values,
not fresh sequence calls. Prepared plans contain no mutable datum cache:
every execution owns its keys/permutation/parameters.

Locking SELECT preserves those execution-private caches through row arbitration
and nested scalar-view lineage. Accepted-result compaction does not change the
cache occurrence, and unchanged rows do not redraw volatile ordered outputs.
Qualifying READ COMMITTED replacements refresh output-referenced keys once,
without re-sorting the admitted rank; hidden ordering keys are not re-evaluated
merely to reorder replacement rows. See the query-level volatile placement
qualification in [transactions](TRANSACTIONS.md).
Stored-local marks now settle before a consuming outer Sort, with or without a
count decoration; latest survivors supply that outer rank/key evaluation. This
supersedes the former local-stored delayed-Sort divergence. Inherited-only marks
over ordinary nested ordered views retain their separately qualified frozen
outer-rank behavior. Native's eager marked aggregate input frontier can
retain grants under constant-false output gates that PostgreSQL skips; runtime
aggregate-dependent empty-output retention has separate executed-input witnesses.

Ordering capability and the bounded UTF8/C collation are checked during analysis
even for empty/single/all-NULL inputs. Existing type providers supply numeric,
NaN, temporal, bytea, UUID and character semantics; REGCLASS compares canonical
unsigned OIDs, not lexical text or silent equality. The valid result-only VOID
type is not sortable: hidden `setseed` keys and referenced VOID outputs fail
analysis with42883 / `could not identify an ordering operator for type void`,
Hint `Use an explicit ordering operator or modify the query.`, positioned at the
original ORDER expression/alias/ordinal. No key or visible output is evaluated.
Nil/UNKNOWN and invalid internal usages remain invariant failures, not SQL errors.
The catalog-only internal `"char"` provider also supports ordering through its
existing unsigned-byte comparator; this does not admit internal-CHAR type DDL.

Eager finalization preserves immutable result buffers/metadata, cancellation,
captured-source ownership and atomic INSERT publication. Dump/Restore persists
existing data/sequence state and analyzed view definitions, never sort datum caches.
Native public SQL/API ACL mutation remains absent: exact column grants use engine units and
independent PostgreSQL GRANT scenarios. ORDER USING, arbitrary locale/ICU providers,
unadmitted aggregate-internal ordering remain excluded; selected windows are described below.
The existing AND/OR bare-label ambiguity remains a bounded
logical-grammar difference; AS and quoted forms can label those outputs.

View definition export and native round trips retain qualified input ORDER
identity even when a view output-name override shadows that input name. Synthetic
USING columns retain their merged type/typmod through a collision-safe render-private
USING alias, not a physical operand substitution or cast. Existing explicit USING
and whole-join aliases remain authoritative. Output references are frozen separately
by position. Numeric unary-plus
ORDER expressions retain their analyzed operand occurrence and numeric lexical
identity through definition replay; unary plus is not recategorized as an ordinal.

The nested USING reference witness executes the same native-exported definition
verbatim on both engines, with independently specified rows and sequence effects.
Pinned PostgreSQL18.0's own `pg_views` deparser was observed to emit an alias-sensitive
bare ORDER key for that nested output-override fixture: the original view and its
own definition replay have different sequence-to-row associations. Native definition
compatibility does not assume PostgreSQL's own deparse fidelity in that case.

Feature smoke: `make check RUN='^TestReferenceQueryOrder'`.
View persistence smoke: `make check RUN='^TestReferenceQueryOrderView'`.
Independent pinned18.0 reference: `make check REFERENCE=1 RUN='^TestReferenceQueryOrder'`.

## Inner and cross joins — change note

SELECT and its shared INSERT SELECT producer accept comma-separated FROM items,
CROSS JOIN, JOIN/INNER JOIN and LEFT/RIGHT/FULL [OUTER] JOIN with ON or USING,
and NATURAL [INNER | LEFT [OUTER] | RIGHT [OUTER] | FULL [OUTER]] JOIN. Explicit
joins bind tighter than commas and associate left; parentheses preserve operand
boundaries. Every matching left/right occurrence emits a row: duplicate values
are not deduplicated. ON keeps only TRUE; FALSE and SQL NULL reject the pair.
Empty inputs still have a fully analyzed output schema. No unordered sequence
guarantee follows from the nested-loop implementation.

FROM aliases may use AS or a bare name and rename the first N exposed columns
with a positional column list, leaving the suffix unchanged. A table alias hides
its original name. An alias on a parenthesized join hides its operand qualifiers.
Targets can mix `*`, `a.*`, qualified columns and existing scalar expressions.
Unaliased schema-qualified relations also permit `schema.table.column` and
`schema.table.*`; a FROM alias prevents bypass through the original schema/name.
Ambiguous names fail rather than selecting a source arbitrarily.

Keyword-category alias names include bare `values`, `values(k)`, explicit
`AS values` and quoted `"values"`. Consequently a numeric alias-column list in
`INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)` is now diagnosed at `1`
(`42601`, Position 50), independently observed in direct/Prepare PostgreSQL
18.0 execution; the former pre-alias VALUES-token rejection is obsolete.

USING emits merged keys in list order, followed by remaining left then right
columns. NATURAL derives common keys in left exposed-column order; no common
names means ON TRUE (a Cartesian product for inner joins). Every key participates in equality, so NULL keys
do not match. Qualified operand stars/columns retain the original keys and types.
`USING(q,k) AS u` exposes only the merged keys through `u.*`/`u.k`, without hiding
the operands; this special alias has no column-alias list.

```sql
CREATE TABLE a(l TEXT,k INT,q INT);
CREATE TABLE b(r TEXT,q INT,k INT);
INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8);
INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL);
SELECT * FROM a CROSS JOIN b;
SELECT * FROM a JOIN b USING(q,k);
SELECT u.*,a.k,b.k FROM a JOIN b USING(q,k) AS u;
SELECT * FROM (a JOIN b USING(k)) AS j(kk,ll);
```

The product has sixteen occurrences. USING(q,k) has schema `q,k,l,r` and the
bag `(9,1,'a','x')` four times. NATURAL has schema `k,q,l,r` and four matching
occurrences. A merged INT4/INT8 key uses INT8 while qualified originals keep their
types. INNER selects a common-type operand where possible; LEFT selects the
coerced left key and RIGHT the coerced right key. FULL merges with COALESCE.
An unconverted/unrelabelled selected key retains its physical origin; conversions
and FULL merged keys have no physical origin. Nullable subtree descriptors are
copied outward without mutating source declaration proofs or child frames.

ON uses only its two operands: `a CROSS JOIN b JOIN c ON a.k=c.k` can see a,
but `a,b JOIN c ON a.k=c.k` cannot. WHERE, targets, grouping/HAVING and the
ORDER expressions resolve the complete joined namespace.
General lateral derived queries and other FROM functions remain excluded.
`string_to_table` admits only function-specific LATERAL; SELECT CTEs, derived SELECT
queries and parenthesized VALUES remain non-lateral. DELETE USING and UPDATE FROM
consume the same bounded join tree and are described below.

An ON clause cannot follow CROSS/NATURAL JOIN or a completed join condition:
these malformed joined queries report positioned `42601` at ON. Other excluded
SELECT suffixes retain their capability diagnostics.

Each source requires current SELECT authority, including constant targets, empty
sources and ON FALSE. Equality reads both USING/NATURAL inputs even when merged
output chooses one physical origin. Sources capture one selected statement view
at one frontier, with private writes/deletes and existing RC/RR semantics.
Retained input roots are restricted to the mentioned relations, including private
write prefixes and deletion overlays.
An earlier source lock does not freeze compatible COMMENT/ACL metadata while a
later source waits. Final analysis refreshes same-identity/same-row-shape source
authority; a revoked grant returns42501, while identity/shape replacement retains
the existing catalog-replacement failure. Bound layouts and captured views are
never retroactively refreshed.
Cancellation removes the pending source request without releasing another
transaction's locks. A full explicit abort ends the reader's ticket and grants;
a recoverable savepoint abort retains the outer ticket and admitted grants,
including earlier join leaves. ROLLBACK TO recovers that same authority, and the
outer transaction's end releases it. Finish clears statement admission metadata;
retained eager results independently own the captured data they still require.
INSERT acquires/checks its destination before sources and publishes only a complete
converted/constraint-checked batch. Held prepared execution retains valid source
identities, reanalyzes invalidated names/NATURAL keys and checks fresh rights/data;
changed result shape retains the cached-plan diagnostic.
Renaming an existing distinct column to a common name can change both NATURAL
matching and star width without adding new DDL capability. Aggregate consumers
replace the joined input layout with finalized group rows: an empty join still
produces global `count(*) = 0`, while keyed grouping remains empty.
Results are finalized before publication: public Next does not evaluate joins,
and retained direct/computed rows and metadata survive append and Reset/Restore.
The ordinary single-source indexed/no-copy path remains separate from joined
admitted-frame materialization; rejected pairs do not build a Cartesian buffer.

### Outer joins — change note

LEFT preserves every unmatched left occurrence, RIGHT every unmatched right
occurrence, and FULL both. Each missing entire operand frame receives typed
NULLs, including descendant synthetic slots; missing child expressions are not
evaluated. Equal NULL keys never match. ON match state is recorded before WHERE:
discarding all matched output rows with WHERE does not invent an unmatched row.
For a=(1,a) twice,(2,b),(NULL,n) and b=(1,x) twice,(3,y),(NULL,m),
equality produces four matching pairs plus LEFT's two, RIGHT's two, or FULL's
four labeled unmatched rows:6/6/8. `ON ... AND b.r='x'` keeps LEFT6, whereas
the same restriction in WHERE leaves4.

FULL execution requires a Boolean constant after immutable/imported-parameter
simplification or a genuine conjunctive, supported opposite-input equality key.
Computed/coerced keys are supported; bare cross-input inequality, same-input
equality, volatile keys and an equality hidden in an unsimplified OR are not.
These planning-only restrictions report0A000 on Execute, after successful
Prepare, even for empty inputs/LIMIT0. Binding errors retain their earlier stages.
Constant CASE selection can produce a Boolean constant even when analysis retains
dead-arm column dependencies. FULL legality recognizes that constant without
discarding those dependencies or changing predicate evaluation.
Negated supported comparisons use their effective operator for FULL planning:
`NOT (CAST(a.k AS BIGINT)<>b.k)` is an equality key, including reversed operands
and the `!=` spelling. Common keys across OR branches require the same bound
operand order; reversing a key in only one branch does not make it common.
`NOT (a.k=b.k)` and bare `a.k<>b.k` remain nonkeys without another usable conjunct.
At the join match boundary, equality OR NULL has the same TRUE matches as equality,
so `NOT ((a.k<>b.k) AND NULL::boolean)` is also eligible. This planning summary
does not rewrite the execution predicate, its UNKNOWN results, dependencies,
types or volatility checks.
Removing leading constant NULL arguments from `COALESCE(NULL::boolean,a.k=b.k)`
does not remove the COALESCE wrapper: it is not a FULL equality key and reports
the same Execute-stage0A000, including LIMIT0. Ordinary scalar COALESCE evaluation
is unchanged. Same-type USING keys with differing typmods publish the common
unconstrained typmod in both their synthetic recipe and output slot, including
stored/replayed views.

The producer retains SQL operand order and scans left-major for all kinds.
RIGHT/FULL track demanded right occurrences, not distinct values or pair matrices,
and emit their unmatched tail lazily. LIMIT0 opens no producing child and evaluates
no predicate/merge/output, but preserves binding, ACL, legality and coercion checks.
An early LIMIT1 match does not demand another pair or the unmatched-right tail.
Sorting/grouping remain blockers; public Next still reads finalized results only.

An enclosing FOR clause reaching the nullable side of an outer join reports
mode-specific0A000 on Execute (not Prepare). An unmarked outer query can consume
locally marked view inputs on preserved or nullable sides; each local mark locks
demanded real source rows even if no outer output match uses them. An absent
occurrence has no RowID and cannot lock or recheck a physical row. Native demand
is not PostgreSQL's physical hash-build order: empty-left LEFT never demands a
marked right input, whereas empty-right RIGHT scans a marked left input.
PostgreSQL's observed plan locks the former input and not the latter. These
backend-specific extra scanned locks are independently asserted; selected and
preserved real marked rows lock, LIMIT0 locks none, and rollback releases grants.


Feature smoke: `make check RUN='^TestReferenceQuery(OuterJoins|Joins)'`.
Independent pinned PostgreSQL behavior and full diagnostic coverage:
`make check REFERENCE=1 RUN='^TestReferenceQuery(OuterJoins|Joins)'`.

## Index and view deparsing — change note

All seven PostgreSQL 18 signatures return TEXT through the normal typed binder:
`pg_get_indexdef(oid)` / `(oid, integer, boolean)` and
`pg_get_viewdef(text)` / `(oid)` / `(text, boolean)` / `(oid, boolean)` /
`(oid, integer)`. Omitted pretty equals false; integer width always enables pretty.
Unknown literals and parameters are resolved before input decoding and strict
NULL handling. TEXT is preferred for the single unknown view argument, whereas
conflicting second-argument categories can produce42725 ambiguity. NULL cannot
hide missing children, wrong arities/types or malformed nonnull argument input.
Prepared executions recapture catalog, roles, path and output environment.
Grouped deparse calls retain their real rewritten argument dependencies, including
constant and strict-NULL projections; grouping does not change the TEXT contract.

Index column zero reconstructs CREATE INDEX with no terminal semicolon.
Individual keys use current stable attribute names, without direction/NULL
decorations; invalid nonzero columns return empty text. Missing/nonindex OIDs
return NULL. Full output retains supported UNIQUE and NULLS NOT DISTINCT flags,
omits default ASC/NULL placement, qualifies compact parents and uses visible
parents in pretty mode. No expression/partial/INCLUDE index DDL is added.

View output ends in one semicolon. Ordinary SELECT definitions remain SELECT;
plain top-level VALUES retains its VALUES form in every presentation mode.
Renamed VALUES outputs use an explicit SELECT projection with current labels
and the shared target layout. Missing/nonview OIDs yield NULL.
TEXT uses normal qualified relation-name parsing and captured search_path:
numeric-looking text is a name, absent names report42P01, absent namespaces
3F000 and malformed names42602. No base SELECT privilege check is introduced.
SELECT output still contains clause indentation in compact mode. Pretty reduces
unnecessary expression parentheses recursively while preserving precedence,
including binary arithmetic operands beneath unary signs such as `-(id + 1)`;
wrap zero places subsequent SELECT targets on new lines, negative wrap disables
width wrapping, and positive width measures complete target bytes.
Tokens are never split and the first target may exceed the requested width.
Plain top-level VALUES has no SELECT target list to wrap.
Current output labels and bound source identities survive rename/replacement.

The shared structural renderer preserves every admitted view construct, including
duplicates/NULLs, frozen stars, VALUES, grouping, CASE, joins/USING and hidden
ORDER identities. Complex frozen namespaces can require canonical synthetic
aliases instead of PostgreSQL's private cosmetic choices. The documented nested
USING/output-override fidelity exception above applies to these exports too.
No definition is evaluated; dormant errors and sequence draws remain dormant.
Permanent materialized views support bounded CREATE/DROP and nonconcurrent
REFRESH through the common frozen-query pipeline. Deparsing does not execute
the producer; REFRESH does. Selected metadata ALTER preserves frozen query
identities while current exposed-column names and logical MV index definitions
use existing renderers. CONCURRENTLY remains excluded.

```sh
make check RUN='^TestReferenceDeparseRelations'
make check REFERENCE=1 RUN='^TestReferenceDeparseRelations'
```

## Persistent view sources — change note

Views and bounded catalog providers enter SELECT and INSERT SELECT through the
same relation source/binder/evaluator path as tables and sequences. Nested
definitions retain immutable identity-bound trees and demand-driven inner
occurrences; outer predicates are never pushed across the boundary. Bags, NULLs, grouping,
zero-width occurrences and empty-result metadata retain their normal semantics.
Direct projected backing is retained rather than materialized and copied again;
execution-private borrowed row scratch handles projected/frozen slot mappings.
Final public results expose only stable copied values or immutable owned backing,
never a borrowed scratch slice. See [view authorization and catalogs](SQL.md#views).

View execution-local preflight, including simple CASE branch selection, uses the
admitted statement's captured TimeZone and DateStyle input/output environments.
Nested views and retained prepared statements use the current execution capture,
not the settings at CREATE VIEW or PREPARE. Already returned results retain their
original capture after later session setting changes. These environments do not
mutate stored definitions, replace source identity pins or change owner/invoker
authorization; timezone catalog-view population remains a separate capability.

Temporal view regression smoke:
`make check RUN='^TestReferenceTimezoneReviewView[A-Za-z0-9_]*$'`.
Independent pinned PostgreSQL18.0:
`make check REFERENCE=1 RUN='^TestReferenceTimezoneReviewView[A-Za-z0-9_]*$'`.

Explicit SELECT target `AS label` and eligible bare labels supply frozen output
metadata and preserve view labels during definition reconstruction. CREATE
column lists override the labeled output prefix. ORDER resolves output labels
and ordinals; WHERE/HAVING do not expose output labels, and GROUP BY retains its
input-name priority and existing ordinal rules.

Persistent joined views freeze their source occurrences, expanded star projections
and USING/NATURAL keys when created; ordinary prepared SELECT reanalyzes them on invalidation.
The shared definition/Dump renderer emits canonical per-occurrence aliases and
explicit ON predicates from those identities; original alias column lists and
merged namespaces are preserved logically, not as stale source text. Source
renames and unrelated attribute compaction preserve both self-join sides and
fixed output names/types. ON literals retain their original analyzed type/value.

Reconstruction, native replay atomicity and dynamic feature consumers share
mode-aware definitions. The ADD COLUMN/NATURAL-key freeze complement now runs
in both modes; host invalid/canceled Restore controls remain native-only:

```sh
make check RUN='^TestReferenceView(JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers)$'
make check REFERENCE=1 RUN='^TestReferenceView(JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers)$'
```

## Grouping and count — change note

Table SELECT and shared INSERT SELECT now accept scalar, comma-separated
`GROUP BY` keys and optional `HAVING` before ORDER BY.
`count(*)` counts every source occurrence, including NULL and zero-column rows.
`count(expression)` evaluates its ordinary scalar argument once per input row
and counts only non-NULL results: zero, false and empty strings count normally.
Both have label `count`, SQL type `INT8`, and Go value `int64`.
Without a grouping clause, aggregate calls or HAVING create one global group:
an empty source has count zero. Explicit empty sets also emit one row per set,
even without an aggregate. Empty keyed-only grouping has no rows but retains
the complete result schema. HAVING alone also creates a global group.

Boolean aggregates `bool_and(b)`, `bool_or(b)` and `every(b)` accept BOOL
and return nullable BOOL (Go bool for non-NULL values; scan NULL with sql.NullBool
or nullable any). AND/every require every non-NULL input true; OR requires any
non-NULL input true. All ignore NULL and return NULL on empty/all-NULL input,
including NOT NULL source columns with no eligible rows. Empty keyed input
still returns no rows. Raw output labels remain bool_and/bool_or/every.
Unknown literals such as `'true'`, NULL and unresolved `$1` infer BOOL from the
signature; typed TEXT or INTEGER do not implicitly coerce and give42883,
`function bool_and(integer) does not exist` (or the actual raw name/type),
with `No function matches the given name and argument types. You might need to
add explicit type casts.` and a one-based character call position. Portable
auxiliary diagnostic fields are empty. Zero/star Boolean calls have no signature
and give42883, unlike count(). Every preserves its own diagnostic spelling.
For mixed TRUE/FALSE/NULL input the three results are false/true/false.
Hidden Boolean HAVING and ORDER calls reuse ordinary grouping and rights.
Repeated immutable AND/every arguments may share a state, but OR/count never
share it; volatile occurrences remain independent. A decisive false AND or true
OR never skips later argument evaluation: errors still veto the whole query or
INSERT batch. No aggregate work occurs during public Next.

```sql
CREATE TABLE grouped_rows(k TEXT, v INT);
INSERT INTO grouped_rows VALUES ('a',1),('a',NULL),(NULL,2);
SELECT k,count(*) FROM grouped_rows GROUP BY k;
SELECT k FROM grouped_rows GROUP BY k HAVING count(*)>1;
SELECT (v+1)*2,count(*) FROM grouped_rows GROUP BY v+1;
SELECT count(v),count(NULLIF(v,1)),count(*) FROM grouped_rows;
SELECT bool_and(v>0),bool_or(v IS NULL),every(v>0) FROM grouped_rows;
```

The first query returns the unordered bag `('a',2),(NULL,1)`. NULL grouping keys
compare equal without changing strict SQL equality or three-valued predicates.
Numeric display scale, floating NaNs/signed zero and BPCHAR trailing padding
follow typed comparison semantics. Multiple keys are complete tuples, not
concatenated text. Repeated keys do not duplicate groups; equal projected values
from different groups remain separate occurrences. No group ordering is promised.

Grouping expressions match whole bound subtrees before their children, ignoring
parentheses and positions, not algebraically rewriting operators. Constant
identity retains its typed datum representation, including NUMERIC display scale,
BPCHAR bytes and floating sign/NaN bits; it is not runtime grouping equality.
For example, `v=1.0` is not the identical expression `v=1.00` even though the two
constants compare equal as group values. Source leaves
in SELECT/HAVING/ORDER must be grouped or used inside an aggregate; otherwise
analysis gives `42803`, `column "table.column" must appear in the GROUP BY clause
or be used in an aggregate function`, even for empty sources or HAVING FALSE.
WHERE remains a source-row predicate. Aggregate calls in WHERE/GROUP BY give
`42803`, `aggregate functions are not allowed in WHERE` / `... GROUP BY`.
HAVING uses contextual BOOL typing and keeps only TRUE, never FALSE or NULL.
Its count calls may be hidden from the projection schema.

Expression arguments use the original source row and require their column
rights even on empty/FALSE input. Arguments are analyzed once before the outer
call's diagnostics. More than 100 arguments gives54023, `cannot pass more than
100 arguments to a function`; a 100-argument call still gives the ordinary42883
unresolved-signature diagnostic. Namespace/visibility resolution follows child
analysis and the argument-limit guard: an absent explicit schema gives3F000,
`schema "count_missing_schema" does not exist`. Thus
`count_missing_schema.count(missing)` reports the missing column, while an
over-limit call with valid children reports54023 before the absent schema.
Bad arity or an unresolved function in an existing schema gives42883 with actual
argument types and the PostgreSQL explicit-casts hint.
`count()` gives42809, `count(*) must be used to call a parameterless aggregate
function`; `pg_catalog.count()` retains `pg_catalog.count(*)` in that message.
Only a resolved aggregate call checks nesting: `count(count(*))` gives42803 at
the inner call, while `count(count(*),1)` gives42883 at the outer call and
`count(count(*),missing)` reports the missing column instead.
The ANY signature does not infer a bare `$1`: use `count(CAST($1 AS INT))` (NULL gives0).
An earlier unresolved ANY occurrence followed by a typed occurrence gives42P08;
otherwise an unresolved parameter gives42P18.

Bare integer keys, including parenthesized integers, are one-based output
ordinals: `GROUP BY 1` groups the first expanded target. Zero, negative and
out-of-list positions give `42P10`; fractional, oversized, string, BOOL and NULL
raw constants give `42601`, `non-integer constant in GROUP BY`. As with VALUES
ORDER keys, unsigned tokens must fit int32 (minus does not admit2147483648).
Unary-plus numeric expressions such as `+1` and `+1.5` are constant keys, not
ordinals. GROUP BY names prefer input columns; ORDER BY names prefer output labels.

Every group is finalized eagerly outside the engine lock before HAVING, sorting,
projection, statement completion or public row publication. Context/key/provider
errors veto the entire result or INSERT batch. Returned rows retain finalized
values/schema through source changes and native Reset/Restore. Prepared execution
freshly binds rights/catalog/source data. Count-star requires table SELECT or
SELECT on any live column; real grouping/HAVING/hidden ORDER reads require their
column rights even on empty input. No all-column read is invented by count-star.
INSERT assignment and RowsAffected use selected output occurrences, not the count
datum. Restore's generated-sequence SELECT whitelist is unchanged.

### Arbitrary non-NULL value — change note

`any_value(anyelement)` returns an arbitrary non-NULL input datum, preserving its
concrete SQL type and representation. It does not promise the first, last, minimum
or maximum value, and separate calls/backends need not choose the same candidate.
Repeated equal non-NULL inputs return that value. Empty/all-NULL global input
returns one typed NULL; empty keyed input returns no rows with the full schema.
The result label is `any_value`, with known nullable metadata even for a NOT NULL
source. Result typmod is unrestricted (`-1`); input collation is retained and no
physical source-column origin is reported. The existing type's public codec
applies unchanged, including BYTEA, temporal, NUMERIC and catalog-carrier values.

```sql
SELECT any_value(v),count(v) FROM grouped_rows;
SELECT k,any_value(v) FROM grouped_rows GROUP BY k;
SELECT k FROM grouped_rows GROUP BY k HAVING any_value(v)>0;
SELECT any_value($1::integer) FROM grouped_rows;
```

The polymorphic signature requires a concrete argument type. Bare NULL, unknown
strings and unresolved parameters give `42804`, `could not determine polymorphic
type because input has type unknown`, without a call-position field. An outer
cast does not type the argument. An earlier binding occurrence can constrain a
parameter; later SELECT/WHERE/HAVING/ORDER occurrences do not retroactively type
an already-bound call. Use an explicit cast inside `any_value`.
Zero/star or multiple arguments give the normal `42883` signature diagnostic;
child analysis, the 100-argument guard, namespace visibility and nesting retain
the existing aggregate precedence and full portable diagnostic fields.

Direct/prepared SELECT, scalar wrappers, hidden HAVING/ORDER, INSERT SELECT,
stored views and native Dump/Reset/Restore use the ordinary eager aggregate
pipeline. Every argument still evaluates once per source occurrence after a
candidate is retained. Later errors or cancellation prevent any result or partial
INSERT batch. Finalized values retain their immutable payload through source,
statement and fixture lifecycle changes; no aggregate work occurs during Next.
Source ACLs apply even to empty or filtered-out input. This adds no OVER provider,
ordinary arrays, casts or codecs; aggregate modifiers use the shared contract below.
INSERT RETURNING performs aggregate child/signature analysis before rejecting
an aggregate at42803; prepared multi-command input retains42601. Carrier
eligibility itself does not add explicit casts to TEXT: raw aggregate output,
typed-column assignment and admitted casts retain their existing authority.
The separately supported [INTERVAL→TEXT output edge](DIALECT.md#date-and-time-arithmetic--change-note)
also consumes INTERVAL aggregate results, with captured IntervalStyle.

Feature smoke: `make check RUN='^TestReferenceAnyValueSmoke$'`.
Full shared contracts: `make check RUN='^TestReferenceAnyValue'`
and `make check REFERENCE=1 RUN='^TestReferenceAnyValue'`.
Each backend asserts membership independently when multiple candidates exist.

### Minimum and maximum — change note

`min(value)` and `max(value)` ignore NULL and retain the smallest/largest datum
using the selected type's shared comparison hook. Empty global input returns one
typed NULL pair; empty keyed input has no rows but retains the complete schema.
Result labels are `min`/`max` unless aliased. Native metadata is known nullable,
without source-column origin or source typmod (`-1`), even for NOT NULL input.
The chosen payload is not rounded, repadded, serialized or converted into a
sorting key. Each group keeps one datum per call, not a collected/sorted list.

```sql
SELECT min(v),max(v),count(v) FROM grouped_rows;
SELECT k,min(v),max(v) FROM grouped_rows GROUP BY k HAVING max(v)>3 ORDER BY min(v);
INSERT INTO extrema_output(lo,hi) SELECT min(v),max(v) FROM grouped_rows;
SELECT min($1),max($2::integer) FROM grouped_rows; -- TEXT and INT4
```

The supported scalar overloads and aliases are listed in [TYPES.md](TYPES.md).
Domains resolve to base overloads; VARCHAR/NAME/internal CHAR produce TEXT,
REGCLASS produces OID. Bare NULL/string/untyped parameters select preferred TEXT.
BOOL/UUID have no overload (42883), despite their ordinary comparison support.
NAME[] and private PG_NODE_TREE have explicit native0A000 capability diagnostics;
this does not add general arrays, tree ordering or public casts to TEXT.

Calls work through aliases, joins, views, scalar wrappers, hidden HAVING/ORDER,
and mixed aggregates. Direct/prepared/held statements execute afresh after writes
and rights changes. All input arguments evaluate even after a winner is retained;
late argument/comparator errors and cancellation publish no cursor or INSERT
batch. INSERT SELECT affects the number of output groups (empty global1/keyed0).
Late TEXT-to-INTEGER failures are exercised by SELECT Query and INSERT SELECT
Exec, direct and prepared, with full22P02 diagnostics and no inserted rows.
INSERT SELECT RETURNING reaches the same extrema reducer and late cast error.
Plain consumer methods keep actual cursor
assertions but cannot carry the Context-only middleware recorder: separate
QueryContext executions of the same held statement prove recorded row ownership
and closure, rather than attributing companion records to plain Query.
Public Next performs no aggregate evaluation. Native Reset/Dump/Restore remaps
view descriptors/comparators while already owned rows retain their original data.
Conflicting implicit string collations fail42P22 only when two eligible candidates
need comparison; zero/one/all-NULL inputs succeed. A final COLLATE resolves it.
Runtime diagnostics retain their original fields and have no invented Position.

OVER remains excluded; FILTER/DISTINCT/ALL and min/max in-call ORDER use the shared modifier contract.
No-FROM extrema use the ordinary local input. Ordinary namespace, arity, 100-argument, nested-call and clause guards
retain full SQLSTATE/English diagnostic/character-location contracts.
Feature smoke: `make check RUN='^TestReferenceExtremaSmoke$'`.
Complete dual-mode contracts: `make check RUN='^TestReferenceExtrema'`
and `make check REFERENCE=1 RUN='^TestReferenceExtrema'`.

### Range set aggregates — change note

Four signatures cover every implemented int4/int8/numeric family:

| Function | Range input result | Multirange input result |
|---|---|---|
| `range_agg(value)` | corresponding multirange | same multirange |
| `range_intersect_agg(value)` | same range | same multirange |

Plain and nested domains resolve to these physical base results. Both functions
ignore SQL NULL. Empty/all-NULL global input returns one typed NULL pair; empty
keyed input returns no rows with complete schema. Nonnull `empty`/`{}` differs
from NULL: union of only empty sets is `{}`, and intersection with an empty set
remains `empty` (range) or `{}` (multirange), without suppressing later evaluation.

```sql
CREATE TABLE ranges(k INTEGER,v INT4RANGE);
INSERT INTO ranges VALUES(1,'[1,4)'),(1,'[7,9)'),(2,NULL);
SELECT range_agg(v),range_intersect_agg(v) FROM ranges;
-- {[1,4),[7,9)} | empty
SELECT k,range_agg(v) FROM ranges GROUP BY k;
SELECT range_intersect_agg(v ORDER BY k) FILTER(WHERE k=1) FROM ranges;
```

Union merges overlapping/adjacent members but preserves gaps; it is not a hull.
Multirange intersection preserves disconnected components. NUMRANGE is continuous:
`(1,2)` and `(2,3)` retain a union gap; `[1,2]` and `[2,3]` intersect at `[2,2]`.
Endpoints use exact numeric magnitude, not float64. Ordered intersection retains
the accumulated representation on equal bounds; union uses released strict-sort
bound selection. Fully comparator-equal differently scaled union members have no
stable display-scale winner promise.

Native results have known nullable metadata, typmod−1, no physical origin,
uppercase concrete family names and string raw/ScanType. Pinned lib/pq exposes
range names, blank multirange names, raw []byte and any ScanType; length/decimal
metadata and pq nullability are unavailable. Zero-row metadata retains exact types.
Unknown unary NULL/string/parameter operands are ambiguous42725. Native successful
binds use destination-context input (for example `INSERT INTO ranges(v) VALUES($1)`)
and aggregate the typed column; this feature does not add explicit range casts.
Wrong scalar signatures give full42883/English/Hint/character Position.
Source authority is checked even for empty/WHERE FALSE/FILTER FALSE input.

Direct/held statements execute afresh; integer grouping/HAVING, derived/stored
views and INSERT SELECT use the existing eager pipeline. INSERT counts output
groups (global1, empty keyed0); later errors publish no partial rows/writes.
Native control replacement remaps both input and result type authority while held
owned rows retain their values/schema. Public Next performs no aggregate work.
FILTER/ALL and integer-key local ORDER are admitted. DISTINCT, range-valued ORDER
and OVER retain independent native capability diagnostics versus PostgreSQL
success; this adds no general range/multirange comparison/order/group provider,
temporal family, array, constructor, operator or cast.

Feature smoke: `make check RUN='^TestReferenceRangeAggregateFeatureSmoke$'`.
Complete public contracts: `make check RUN='^TestReferenceRangeAggregate'`
and the identical selector with `REFERENCE=1`; see TESTING.md for bounded groups
and the separate root-owned standalone public smoke.

### Text and binary concatenation — change note

`string_agg(value,delimiter)` has exactly two overloads: TEXT→TEXT and
BYTEA→BYTEA with same-type delimiters. Results are nullable base types without
source typmod or physical origin. BYTEA values and separators retain raw octets.
Metadata follows existing adapters: native TEXT Length is `(MaxInt64,true)`,
native BYTEA Length is `(0,false)`, and lib/pq exposes `(MaxInt64,true)` for
both overloads. DecimalSize is unavailable; native aggregate Nullable is
`(true,true)` while lib/pq reports unknown nullability.
Unknown literals/parameters default to TEXT; a known BYTEA operand on either
side selects BYTEA. Existing character/domain-base implicit coercions apply,
including CHAR trailing-space conversion; integers/Booleans and mixed typed
TEXT/BYTEA are not implicitly converted to form a signature.

NULL values contribute nothing. NULL delimiters contribute no separator, not
row exclusion. The first surviving value ignores its delimiter; each later
surviving value uses its own row's delimiter. Empty values are real values:
empty followed by `x` with comma yields `,x`. Empty/all-NULL global input returns
one typed NULL; empty keyed input returns no rows with full metadata.

```sql
CREATE TABLE fragments(id INTEGER,v TEXT,d TEXT);
INSERT INTO fragments VALUES(3,'c','/'),(1,'a','ignored'),(2,'b',NULL);
SELECT string_agg(v,d ORDER BY id) FROM fragments; -- ab/c
SELECT string_agg(v,d ORDER BY id DESC) FROM fragments; -- cbignoreda
```

In-call ORDER BY binds input expressions, not projection labels or output
ordinals. Use source keys with a unique tiebreaker for exact ordering. Multiple
keys, ASC/DESC and NULLS FIRST/LAST follow normal typed ordering; integer `1`
inside the call is a constant. Calls with opposite orders sort independently.
Without call-local ordering the concatenation order is unspecified; outer
ORDER BY orders output groups, not their input values.

Value, delimiter and each key occurrence evaluate once per eligible row, even
when the value is NULL. Cached ordered datums are replayed only after successful
evaluation and sorting. Native all-group finalization precedes cursor publication;
late expression/comparison errors and cancellation veto all output. PostgreSQL
may instead stream an earlier completed group and expose the later error through
Rows.Err. INSERT SELECT remains statement-atomic on both backends.

Direct/newly prepared and SAME held statements, scalar wrappers, hidden HAVING/
outer ORDER, INSERT SELECT and persistent aggregate views share the ordinary
pipeline. Native Dump/Reset/Restore replays definitions, arguments/literals and
hidden ordering keys, never transition state. Source rename and fresh held
executions use current catalog/data; existing eager native cursors retain values.
Column permissions apply to both arguments and hidden keys, including empty input.
Bad signatures/arity, nesting, forbidden clauses, missing keys and malformed
ORDER preserve complete SQLSTATE/English diagnostics and character positions.
In INSERT VALUES and RETURNING, string_agg gives42803 at the call, after
child/signature analysis.

OVER remains excluded. FILTER/DISTINCT/ALL and sibling ordering use
the shared modifier contract. No-FROM aggregates use the ordinary local input. Unsupported XML ordering controls do not add XML or
cast edges. Public smoke:
`make check RUN='^TestReferenceStringAggSmoke$'`.
Shared contracts:
`make check RUN='^TestReferenceStringAgg'`
and `make check REFERENCE=1 RUN='^TestReferenceStringAgg'`.

### Array aggregation — change note

Both overloads use the shared grouping, modifier, eager publication and source
authorization paths, including direct/prepared SQL, INSERT SELECT and stored
views/native Dump/Restore. Scalar aggregation includes SQL NULL:

```sql
CREATE TABLE array_values(id INT,v INT);
INSERT INTO array_values VALUES(1,3),(2,NULL),(3,1);
SELECT array_agg(v ORDER BY id) FROM array_values; -- {3,NULL,1}
SELECT array_agg(DISTINCT v ORDER BY v) FROM array_values; -- {1,3,NULL}
```

Without call-local ordering, element order is unspecified. Outer ORDER BY sorts
groups, not elements. No admitted rows return a typed SQL NULL, not an empty
array; empty keyed groups produce no rows with complete metadata. Scalar domains
retain their associated domain-array identity. Domains over arrays select the
array overload and return the base array; array-of-domain elements stay domains.

Array input concatenates rectangular arrays into rank+1 without changing the
array type/OID. New outer bounds start at1; every inner extent and lower bound
must match. Element NULLs survive. Whole NULL input arrays fail22004, first empty
arrays fail2202E, later dimensionality/bounds mismatch fails2202E, and rank6
input fails54000. FILTER runs first and can explicitly exclude these inputs;
it never suppresses semantic analysis or authorization.

Native arrays export as Go strings with string ScanType; lib/pq exports raw
[]byte with any ScanType. SQL NULL is nil. Array Length is unavailable; native
aggregate Nullable is known true, while lib/pq leaves nullability unknown.
Array result typmod is -1 and physical origin is zero. Prepared executions have
fresh private group state; native late failure/cancellation publishes neither
a cursor nor a partial INSERT SELECT batch. INSERT VALUES rejects array_agg42803.
Unknown NULL/string/untyped parameters give42725 ambiguity with the cast hint.
Existing concrete types without an associated array (for example PG_NODE_TREE
from pg_attrdef.adbin) fail unpositioned42704 `could not find array type for data
type pg_node_tree`; no synthetic array type or native0A000 substitute is used.

Scalar DISTINCT and array aggregation ordered by an integer source key succeed.
Native array DISTINCT and array-valued local ordering retain positioned0A000;
PostgreSQL supports those comparisons. Valid array_agg OVER retains the existing
unsupported-window-provider0A000 after ordinary signature resolution.
Smoke: `make check RUN='^TestReferenceArrayAggFeatureSmoke$'`;
oracle: `make check REFERENCE=1 RUN='^TestReferenceArrayAgg'`.

### Interval average — change note

`avg(interval)` shares the grouped/global eager pipeline, including HAVING,
hidden ORDER BY, repeated calls, prepared execution, views and INSERT SELECT.
Each execution/group owns fresh checked component sums. NULL does not contribute
to the denominator; empty/all-NULL global input returns one typed NULL, while
empty keyed input returns no rows with INTERVAL metadata. A zero mean is not NULL.

```sql
CREATE TABLE durations (category TEXT, elapsed INTERVAL);
INSERT INTO durations VALUES ('work','1 mon'),('work','0'),('work',NULL);
SELECT category,avg(elapsed) FROM durations GROUP BY category;
-- Unordered bag: ('work','15 days').
```

The result is nullable INTERVAL with no physical column origin or inherited
typmod. Fractional months become 30-day fractions, fractional days become
24-hour fractions; PostgreSQL's float8/nearest-even stages are preserved rather
than exact-rational division or interval typmod rounding. Component sums and
division boundaries fail with22008, `interval out of range`, including finite
overflow after infinity. Both infinity signs fail; one sign yields that infinity.
Late group errors publish neither rows nor partial INSERT batches.

Explicitly type ambiguous arguments (`avg($1::interval)`, `avg(NULL::interval)`):
bare unknown NULL/string/parameter calls give42725 and the explicit-casts hint.
Invalid signatures and arity give42883, including `avg()`/`avg(*)`; more than100
arguments gives54023. Namespace lookup/ACL and nested/clause/grouping diagnostics
use the existing authority for all supported AVG overloads.
Native Dump/Restore preserves stored interval inputs/views, never aggregate state.

Public feature smoke, using independently pinned PostgreSQL18.0 expectations:
`make check RUN='^TestReferenceIntervalAvg'`
and `make check REFERENCE=1 RUN='^TestReferenceIntervalAvg'`.

`avg(SMALLINT)`, `avg(INTEGER)`, `avg(BIGINT)` and `avg(NUMERIC)` return
unrestricted NUMERIC; `avg(REAL)` and `avg(DOUBLE PRECISION)` return FLOAT8.
Each is nullable, with no physical origin or inherited typmod, including for
NOT NULL or sized NUMERIC inputs. NULL is omitted from the denominator;
empty/all-NULL global input yields one typed NULL row, empty keyed input none.

```sql
CREATE TABLE readings (n NUMERIC, r REAL);
INSERT INTO readings VALUES (1,1),(NULL,NULL),(3,3);
SELECT avg(n),avg(r) FROM readings;
-- ('2.0000000000000000', 2), with NUMERIC/FLOAT8 result types.
```

Exact numeric accumulation preserves the maximum finite input display scale.
Division chooses scale from the normalized base-10000 sum/count weights and
leading digits, with at least16 significant digits, never more than1000
fractional digits, and ties rounded away from zero. Thus AVG of singleton1 has
20 fractional digits, not a fixed16. The finite SUM is packed before division:
two `9e131071` numeric inputs fail22003, `value overflows numeric format`, even
though their mathematical mean fits. Transient oversized sums may cancel before
finalization. NaN or both infinity signs yield NaN; a single infinity sign
dominates even an unrepresentable finite sum.

REAL is widened from binary32 (AVG of REAL0.1 is FLOAT8
`0.10000000149011612`). FLOAT8 transitions retain PostgreSQL's N/Sx/Sxx
Youngs–Cramer state: `{1e200,0}` fails22003 through Sxx although the sum is finite.
Final division permits IEEE underflow and signed zero; nonfinite values follow
IEEE arithmetic. Unordered input has no promised floating evaluation order.
All groups finalize eagerly before any result or INSERT SELECT batch is
published. Prepared reexecution creates fresh group states; native Dump/Restore
stores inputs/views, never mutable accumulators, and restores both AVG input and
result type ownership for all six numeric overloads. Infinity/NaN do not skip
later argument evaluation: a later row's supported TEXT-to-INTEGER cast error
still vetoes the entire aggregate result, including INSERT SELECT RETURNING.

Numeric AVG contract and feature smoke (both independently asserted modes):
`make check RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`
and `make check REFERENCE=1 RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`.
Use `RUN='^TestReferenceNumericAvgSmoke$'` in both commands for the focused smoke.

### Numeric sum — change note

`sum` ignores NULL and returns typed NULL for empty/all-NULL global input;
empty keyed input returns zero rows with the same schema. SMALLINT/INTEGER
produce INT8; BIGINT/NUMERIC produce unrestricted NUMERIC; REAL produces FLOAT4
and DOUBLE PRECISION produces FLOAT8. Result metadata is nullable, typmod-1,
without source origin; the default label is `sum`.

```sql
SELECT sum(n),sum(r) FROM readings;
-- ('4', 4), with NUMERIC/FLOAT4 result types.
SELECT sum(v) FROM (VALUES (9223372036854775807::bigint),
                          (9223372036854775807::bigint)) AS t(v);
-- exact NUMERIC '18446744073709551614'
```

SUM shares exact accumulation/packing with AVG but never divides or counts a
denominator. It preserves maximum finite display scale (`1.20+3.456`→`4.656`;
`1.20-1.20+0.0000`→`0.0000`), including positive numeric zero. Transient
oversized subtotals can cancel; only the final finite packed value fails22003,
`value overflows numeric format`. NaN and infinity precedence matches numeric
AVG and does not suppress later eligible argument evaluation.

Unlike AVG, REAL SUM rounds each transition to binary32. In-call id ordering
for16777216,1,-16777216 yields0, not1. DOUBLE PRECISION sums at binary64.
First non-NULL input seeds the state directly, retaining singleton/repeated-0.
Two finite operands producing infinity fail immediately22003, `value out of
range: overflow`; explicit infinity/NaN uses IEEE arithmetic. There is no
addition-underflow check. No unordered output or transition order is promised.
Native FLOAT4 exports its stored binary32 widened to float64; pq parses the
released text at binary64 (and reports float32 ScanType). Ordinary approximate
reference witnesses use bounded absolute+relative1e-6 FLOAT4/1e-12 FLOAT8
tolerances, never normalizing NULL/type/special/signed-zero/overflow or ordered
rounding discriminators.

Existing FILTER-before-argument, DISTINCT/ALL/order, HAVING/hidden ORDER,
correlated consumers, prepared fresh states, ACL checks, eager publication and
INSERT SELECT atomicity apply. Stored views remap both input/result ownership.
Unknown unary arguments require explicit casts (42725); invalid signatures,
including zero/star/multiple arguments, fail42883 with signature hints/positions.
Missing signatures with multiple in-call ORDER BY keys instead carry the
PostgreSQL hint that ORDER BY must appear after all regular aggregate arguments.
This does not change unknown-input ambiguity or child/FILTER error precedence.
Domain inputs use their existing base-type overload; signature diagnostics name
visible domains without a schema and hidden domains with their schema.
INSERT VALUES analyzes SUM children, FILTER and signature before rejecting a
valid numeric aggregate with42803, `aggregate functions are not allowed in VALUES`.
Invalid calls retain their earlier diagnostic, and no VALUES prefix is installed.
Scalar wrappers do not convert a contained SUM analysis error into an assignment
error. Destination columns remain inaccessible as SUM inputs; a matching column
may appear only in PostgreSQL's missing-column Detail, never as a source read.
Runtime INTERVAL SUM and windows remain excluded. Contract and feature smoke:
`make check RUN='^TestReferenceNumericSum'`
and `make check REFERENCE=1 RUN='^TestReferenceNumericSum'`;
use `RUN='^TestReferenceNumericSumSmoke$'` in both for the focused smoke.


This does not admit
aggregates other than count/bool_and/bool_or/every/avg(interval or numeric inputs)/sum(numeric inputs)/any_value/string_agg/array_agg/min/max/range_agg/range_intersect_agg,
other window providers or frame exclusions,
new scalar calls or optimizer/spill/parallel machinery.
Ordinary no-FROM SELECT uses the shared local-unit query input, including
aggregate projections and selected sequence/REGCLASS siblings. Function-provider
restrictions and INSERT-only producer rules remain separate and unchanged.

### Aggregate modifiers — change note

All existing providers share FILTER, DISTINCT, default-equivalent ALL and local
ORDER BY. For `(1),(1),(NULL),(2)`, `count(DISTINCT v)` is2 and
`count(v) FILTER(WHERE v=1)` is2. FILTER requires BOOL and admits only TRUE,
running before every argument/key. Groups still exist when all their inputs are
rejected; global COUNT returns0, other providers typed NULL. Every admitted
argument and unique key evaluates before NULL/duplicate transition skipping,
including string_agg delimiters on NULL values and keys on decisive Boolean or
any_value states. DISTINCT uses type-owned equality/hash for the **complete**
argument tuple; NULL delimiter components are valid and equal to each other.

Local keys bind only in the input namespace: no SELECT aliases or ordinals.
ASC defaults NULLS LAST, DESC NULLS FIRST; explicit null placement is independent.
Different calls order independently, and final ORDER BY sorts result groups only.
Peers without a unique tiebreaker have no portable order. DISTINCT ORDER keys must
match complete arguments, not arbitrary expressions containing the same column.
Only implicit argument coercions are ignored; explicit casts retain identity.
Matching argument/order expressions (even volatile) reuse one evaluated input
within a call. Other distinct expressions still evaluate separately, and any
volatile argument/FILTER/order prevents aggregate-state sharing between calls.

Analysis transforms arguments, FILTER, arity/namespace/signature, then ORDER/
DISTINCT eligibility and ownership. Bad FILTER gives42804; invalid DISTINCT
ORDER reference42P10; direct aggregate in FILTER42803. Star permits FILTER only:
DISTINCT/ALL star or ordered star gives42601 before any batch prefix effects.
DISTINCT COUNT unknown parameters and a matched UNKNOWN local ORDER target infer
TEXT; FILTER unknown parameters infer BOOL. Plain `count($1)` without a resolving
modifier retains its unresolved-parameter diagnostic. Modifier-only
columns/relations/sequences retain ACL and dependency checks even on empty input
or FILTER FALSE, including held prepared reauthorization.

Aggregate DISTINCT requires equality and sort eligibility even though native
deduplication uses hashing. Native arrays/domains-over-arrays and PG_NODE_TREE
give positioned0A000 aggregate-specific capability boundaries for DISTINCT or
local ORDER; PostgreSQL supports these represented carriers. Explicit tree TEXT
casts use existing TEXT support. SELECT DISTINCT structural equality is unchanged;
no ordinary array/tree comparator or additional window provider is added.
Prepared/view plans remain immutable; per-execution group/call dedup and ordering
buffers never survive execution. Stored views/export/native Dump/Restore preserve
FILTER/DISTINCT/order and rebind descriptor-owned key families/comparators.
All errors/cancellation veto native cursor/INSERT publication; public Next does
no expression evaluation.

```sh
make check RUN='^TestReferenceAggregateModifiersFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceAggregateModifiersFeatureSmoke$'
```


Feature checks (repository root, quiet output):

```sh
make check RUN='^TestReferenceQueryAggregate'
make check RUN='^TestQueryAggregate'
make check RUN='^TestQueryAggregate'
make check RUN='^TestReferenceBooleanAggregate'
make check RUN='^TestBooleanAggregate'
make check RUN='^TestBooleanAggregate'
make check REFERENCE=1 RUN='^TestReferenceBooleanAggregate'
make check RUN='^TestReference(BooleanAggregate[A-Za-z0-9_]*|AnyValueSourceACL)$'
make check RUN='^TestGrouping'
make check REFERENCE=1 RUN='^TestReferenceQueryAggregate'
make check RUN='^TestReferenceQueryAggregateSmokeMigration$'
make check RUN='^TestReferenceCountExpression'
make check REFERENCE=1 RUN='^TestReferenceCountExpression'
make check RUN='^TestReferenceCountExpressionSmokeMigration$'
```

Query-read test migration carries the complete 67-test root assertion union into
80 untagged external `reference_test` definitions for projection, NULL counts,
aggregates, joins, limits and ordering. Default/explicit-false mode runs the real
native adapter without Docker; explicit `-reference=true` adds the unchanged
pinned PostgreSQL 18.0 oracle to those same definitions. Backend-specific
diagnostics and metadata are independently specified, not hidden by parity.
Native controls preserve held rows and pristine restored sequence state;
PostgreSQL lock witnesses use actual bounded waiters. Joined BYTEA scan ownership
is checked on both backends, while detached `Rows.Columns` name slices are a
native-only contract because lib/pq may return borrowed names. Unrelated legacy
families remain tagged. See [dual-mode verification](DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
for runtime mode and legacy-selection boundaries. This migration changes no
production SQL behavior or public API.

## INSERT RETURNING — change note

`INSERT ... [ON CONFLICT ...] RETURNING [WITH (OLD AS o, NEW AS n)] projection`
uses the ordinary query interface and expression binder. Star expands the logical
target's live columns in declaration order. Target-name/visible-target-alias and
unqualified values mean NEW. Output sees completed defaults, identity and demanded
generation values for VALUES, DEFAULT VALUES and all existing query/view/partition
producers. Source aliases and `excluded` are not output namespaces. Explicit pseudo
aliases hide their default names; target aliases hide the original target name.
Scalar subqueries use lexical correlation and the captured statement frontier.

OLD fields are typed NULL for insertion; successful conflict UPDATE returns its
actual predecessor and replacement. DO NOTHING and FALSE/NULL update predicates
emit no row and do not evaluate output. RETURNING expressions are evaluated once
after final private-stage admission and before the next proposal is demanded.
Output sequence effects survive a later transactional error under existing
sequence policy. Exec evaluates ignored output too; RowsAffected counts successful
inserts plus updates and LastInsertId remains unsupported. Without RETURNING there
is still no result relation. Empty output retains the complete bound metadata.

Bare visible target/OLD/NEW names in INSERT RETURNING can denote the actual named
logical target rowtype, after ordinary same-named column lookup. Absent OLD is
SQL NULL, not an all-NULL composite. Present all-NULL NEW is `(,)` for two fields;
zero-member NEW is `()`. Whole-row reads demand SELECT on every live member, even
when OLD is absent. With zero live members, a whole-row reference still requires
table SELECT; a constant-only projection does not. Zero-row input does not erase
this requirement. Direct `old.*`/`new.*` are field expansion, not one composite.
Generic ROW constructors, `(old).field`, `(old).*` and global query whole-row
resolution are not added. Native compound output is canonical string with blank
DatabaseTypeName/string ScanType; pq exposes raw []byte/interface{} ScanType.

Initial and fresh native admission check output SELECT rights for scalar, star,
whole-row and dead CASE children, including empty/skipped input. Native refreshes
current rights after waits; PostgreSQL retains its existing executor-start ACL
decision. Bind errors and late projection/constraint/cancellation failures
publish no partial native rows/count/writes. PostgreSQL can stream an output
prefix before a later Rows.Err; consumers must drain/close before another command.

Go held statements and SQL PREPARE/EXECUTE retain fixed formals/output signatures,
without Prepare evaluating defaults/output/sequences. Dump serializes committed
definitions/data, never output/history. Restore rejects RETURNING with native
0A000 `restore INSERT RETURNING is unsupported`, atomically preserving the target.
Fresh restored statements resolve reminted rowtype identities; held eager values
and metadata keep their captured generations. Server trigger transformation and
suppression are genuine PostgreSQL-only controls, not native successful no-ops.

Selected proof: [INSERT RETURNING contracts](TESTING.md#insert-returning-oracle-first-contracts).
Feature smoke: `make check RUN='^TestReferenceInsertReturningFeatureSmoke$'`,
and the identical command with `REFERENCE=1`, each under an external900-second deadline.

## INSERT from query — change note

INSERT now accepts SELECT sources through the common query pipeline, including
parenthesized sources and ordinary no-FROM projections such as `SELECT '1'`,
`SELECT NULL` and `SELECT $1`. The target is not part of the source namespace.
Star expansion and output width/type checks happen during analysis, even if the
source is empty. An explicit target list requires the same number of expressions;
without a list, the source maps the first N declared live columns. Omitted
columns use declared defaults or typed NULL; existing NOT NULL constraints still
apply. Explicit source NULL bypasses a default. ORDER-decorated VALUES shares
this omission behavior but rejects a source DEFAULT marker with 42601;
ordinary and parenthesized undecorated VALUES retain per-cell DEFAULT requests.
Sequences and serial remain unsupported native subsystems, not successful no-ops.

```sql
CREATE TABLE source_rows(i INT, label TEXT);
INSERT INTO source_rows VALUES (1,'one'),(1,'one'),(2,NULL);
CREATE TABLE destination(label TEXT, i BIGINT, flag BOOL);
INSERT INTO destination(i,label)
  SELECT i,label FROM source_rows ORDER BY i;
```

This inserts three occurrences (including both duplicates), converts INT4 to
INT8 and fills `flag` with NULL. Zero, one and many selected rows have exact
RowsAffected counts. Without RETURNING, direct/prepared Query of INSERT
has no columns or data rows; Exec exposes only the successful command count.
Prepare is effect-free, and reused statements freshly bind and read the current
source rather than caching source data.
Already-supported casts, null/truth/distinct predicates, DATE and BYTEA remain
available in query sources. SELECT result names retain the existing cast naming
rules. Source and assignment conversions use the same recursive, execution-private
preflight as ordinary SELECT, outside the engine lock and before source capture.

Within INSERT, plain `VALUES ('1')` and `(VALUES ('1'))` retain destination-context
typing. ORDER BY or LIMIT/OFFSET/FETCH makes VALUES a query source: each column
gets a common type first, with all-unknown columns becoming TEXT. Consequently
`INSERT INTO integer_target VALUES ('1') ORDER BY 1` fails 42804 with the rewrite/
cast Hint and no Position; it does not silently reparse known TEXT as integer.
ORDER BY uses an output ordinal, an implicit `columnN` name or a unary-plus
numeric constant, not a destination column name. Unsigned integer tokens within
int32 are ordinals, including after minus; `2147483648` and `-2147483648` instead
give 42601 / `non-integer constant in ORDER BY`. Unary-plus keys such as `+2`
and `+1.5` are validated numeric expressions with all rows tied, never ordinals.
Duplicate occurrences and NULL sort placement for column/ordinal keys are retained.
ORDER analysis follows target validation and VALUES width/common typing. Raw
parsing always consumes the complete script first, so later syntax errors take
precedence over ORDER analysis errors and prevent effects from a valid prefix.
No-FROM SELECT sources use the same local input as standalone SELECT.
Standalone and FROM VALUES use the same query-source binder and runtime described below.
SELECT WITH queries use this same INSERT source producer. Top-level WITH INSERT,
modifying CTE bodies and RETURNING remain outside this surface.
Conflict omission is described in [INSERT conflict omission](#insert-conflict-omission--change-note).
Supported SELECT row-lock clauses are described in [SELECT row locking](#select-row-locking--change-note).

The target RX lock is acquired before source relation locks: AS for ordinary
sources, ROW SHARE for marked sources, including stored view clauses. Target
INSERT and source SELECT authority (projection, WHERE and hidden ORDER columns)
are independently checked.
Self-insert reads a frozen prefix including earlier own transaction writes, never
this command's newly assembled batch. Source scanning, sorting and conversion run
outside the engine mutex; a failed cast, constraint, source evaluation, cancellation
or publication fence cannot publish a partial batch. Existing explicit-transaction
failure/rollback and other-session visibility rules are unchanged.

Dump records persisted destination values as canonical literal VALUES, not query
provenance. Restore roundtrips that output but deliberately rejects SELECT and
decorated VALUES INSERT source scripts with native 0A000 before trusted execution.
Held native result cursors keep their original schema/data across failed Restore,
successful replacement and Reset. These host controls are not PostgreSQL physical
pg_dump, disk durability or wire-server APIs.

Feature checks (run from the repository root):

```sh
make check RUN='^TestInsertSelect'
make check RUN='^TestInsertSelect'
make check RUN='^TestReferenceInsertSelect[A-Za-z0-9_]*$'
make check REFERENCE=1 RUN='^TestReferenceInsertSelect[A-Za-z0-9_]*$'
make check RUN='^TestInsertSelectSmoke$'
```

## Positioned DELETE — change note

`DELETE FROM target [AS alias] [USING sources] WHERE CURRENT OF cursor_name
[RETURNING outputs]` deletes only the live physical occurrence most recently visited
by FETCH or MOVE on that named cursor. It is not a Boolean expression and cannot be combined with
AND/OR. Names use ordinary identifier folding/quoted decoding. Raw `$1` cursor
syntax remains syntax42601; RETURNING parameters retain their normal type binding.
Prepare validates target/source/output analysis without demanding a cursor;
each Execute resolves the current name, eligibility and position anew.

One bounded FETCH or MOVE positions on its **last** visited row, including zero-width
and duplicate-valued occurrences. Exactly consuming the last row is positioned
until a real EOF probe; forward FETCH ALL and MOVE ALL end after-last. DELETE neither advances nor
clears the cursor: the first deletion counts one and repetition counts zero.
Equal-valued replacement tuples are not substitutes. Existing row arbitration
selects the actual current predecessor for RETURNING and preserves RC/RR failures.

Unlocked physical scans beneath transparent projection/filter/limit/derived/view
wrappers are eligible. An actual sort/aggregate/distinct/set/join frontier is not
a simply updatable scan. PostgreSQL eligibility depends on its chosen plan:
reference tests explicitly force Sort versus useful Index paths, while native
tests use its actual pipeline. One target FOR UPDATE/NO KEY UPDATE/SHARE/KEY SHARE
reference admits supported sorting/joins; zero or multiple target references have
their distinct24000 diagnostics. Eligibility precedes position; missing, closed
or ended names give34000, and before-first/after-last gives24000.

USING still drains and captures its normal source frames. Empty input deletes
zero after cursor validation; repeated source matches delete/count/return the
target once. RETURNING retains actual OLD values, typed NULL NEW values, source
scope and owned schema. Mutation or output failure/cancellation uses ordinary
DELETE staging and publishes no partial data/result. It does not fail a healthy
cursor; recovery restores data but never rewinds older FETCH movement.
Full transaction/PREPARE/session end closes nonholdable names.
Exported WITH HOLD names survive later transaction/PREPARE ends, but their
producer and physical occurrence have been released. CURRENT OF therefore gives
24000 `cursor "name" is held from a previous transaction`; stored projected values
are never rebound as an updatable occurrence.

Native Dump excludes private deletion and all execution state; committed dumps
restore only survivors and persistent metadata. Failed Restore preserves current
identity, successful replacement fences it, and detached FETCH/RETURNING rows
remain immutable. Eligible live SCROLL cursors support backward positioning;
refcursor parser hooks are not added. UPDATE CURRENT OF is described below.
Smoke: `make check RUN='^TestReferenceDeleteCurrentOfFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceDeleteCurrentOfFeatureSmoke$'`.

## Positioned UPDATE — change note

`UPDATE target SET a=b,b=a WHERE CURRENT OF cursor_name [RETURNING outputs]`
uses the same live session/transaction eligibility and physical occurrence as
positioned DELETE. The qualifier is not a Boolean predicate or raw refcursor
parameter. Prepare analyzes target/SET/output types without capturing a cursor.
Each execution resolves the current name, including CLOSE/redeclare incarnations.

Only the last successfully visited live physical RowID is updated, including MOVE,
even with identical or zero-column projections or after its values stop matching the cursor's query.
UPDATE does not move the cursor. Simultaneous SET expressions and OLD/NEW output
use the current predecessor, not stale FETCH values; unassigned columns persist.
Same-value and repeated successful updates count one. A deleted current identity
counts zero and equal-value reinsertion is never a replacement identity.

Existing FROM pairs and RETURNING schemas remain unchanged. Empty FROM produces
zero updates but still validates cursor eligibility/position. Losing duplicate
pairs retain SET demand and failure atomicity. A transparent cursor view may
identify its base table; targeting a view with CURRENT OF instead rejects at
analysis/Prepare with full0A000 `WHERE CURRENT OF on a view is not implemented`.
Runtime cursor errors retain empty Position and optional diagnostic fields.
RR peer updates/deletes report40001 concurrent-update/concurrent-delete wording.

Ordinary automatic UPDATE grants, key-change escalation, private replacement
staging and cancellation-sensitive Finish remain in force. Savepoint rollback
restores data without rewinding an older cursor; failures do not invalidate a
healthy cursor. Transaction/physical-session end closes nonholdable names.
Dump/Restore persists committed changed rows/schema, not cursor or private state;
catalog replacement fences original authority, never retargets equal tuples.

Smoke: `make check RUN='^TestReferenceUpdateCurrentOfFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceUpdateCurrentOfFeatureSmoke$'`.


## DELETE rows

`DELETE FROM target` removes every live occurrence while keeping the table and
its schema. Optional WHERE uses the same typed comparison and three-valued
Boolean evaluator as SELECT: only TRUE selects, never FALSE or NULL. AS and bare
target aliases are supported; an alias hides the original target name. Predicate
columns may be unqualified or qualified by the visible target/alias. USING adds
query sources as described below; triggers remain excluded.
The dedicated `WHERE CURRENT OF cursor_name` alternative is described above.
DELETE RETURNING is described below; UPDATE has its own bounded contract.
Immediate NO ACTION FK checks inspect the final command image before row/RETURNING publication.
DELETE also reuses SELECT's nullness, Boolean truth and DISTINCT predicates,
including alias-qualified operands. Without USING, existing baseline explicit scalar casts work
on unqualified operands; qualified columns inside casts remain excluded, and
the complete statement is rejected before effects. Frame-only cast conversions
run before executor ACL checks or candidate capture, even on empty tables and
inside decisive Boolean branches. Row-dependent conversions remain per-row, but
the shared logical evaluator does not guarantee short-circuiting: on stored
`s='oops'`, both `FALSE AND int4(s)=42` and `TRUE OR int4(s)=42` fail with
22P02 and leave every original row intact. Empty tables demand no row conversion
and succeed with count zero. Pinned PostgreSQL optimizes these specific decisive
branches: FALSE AND deletes zero, TRUE OR deletes all rows, including when the
unused cast takes an invalid TEXT parameter. Native frame-only casts still fail
before scanning even on empty input. This bounded optimization difference is not
a general PostgreSQL evaluation-order or short-circuit promise.

Direct and held prepared execution infer parameter types from SQL, not Go values,
and return the exact number actually deleted through RowsAffected; zero is success.
There is no result relation without RETURNING and no LastInsertId. Preparation
captures descriptions and relation locks, and fixes an explicit REPEATABLE READ
snapshot without taking row locks or executing writes.
Execution checks table DELETE and SELECT on actually read predicate columns;
omitted or constant predicates do not invent a SELECT requirement. SQL GRANT
remains excluded; the existing internal catalog ACL authority supplies rights.

### DELETE USING — change note

`DELETE FROM t AS d USING s AS x WHERE d.id=x.k RETURNING d.id,x.label`
joins target occurrences against the actual supported source tree. Relations,
self-relations, comma/CROSS/inner/outer joins, ON/USING/NATURAL, parentheses,
table/whole-join/column/merged-key aliases, derived SELECT, VALUES, supported
views/catalog relations reuse query binding and execution. Immediate sequence
USING sources fail with 42809 (`cannot lock rows in sequence`); this reference-kind
check does not acquire source tuple locks or require source UPDATE privileges. Existing
FULL-path legality restrictions remain; top-level WITH DELETE, general lateral
derived queries and other function sources remain excluded. `string_to_table`
USING sources cannot reference the mutation target.
Internal source ON and derived queries cannot reference the mutation target;
WHERE and RETURNING resolve the combined namespace. Source aliases hide original
names, whole-join aliases hide operands, and merged-key aliases expose only keys.
USING predicates permit supported source-qualified casts/arithmetic/schema references;
the no-USING baseline qualification boundary is unchanged.

Each successful physical target identity is deleted once, with one affected count,
output occurrence and RETURNING sequence demand, however many source matches exist.
Equal-valued physical target rows each count independently. No source winner or
unordered RETURNING traversal is guaranteed. With no WHERE, a nonempty source product
deletes every target once; an empty source deletes none. Zero-column sources still
have cardinality. Self-source membership is captured before deletions.

One complete command snapshot covers target and recursive sources. Qualifying
target/source pair identities are frozen before the first target row wait. After
arbitration, only the same target identity is followed and qualification rechecked
against its latest predecessor plus the original captured source occurrence.
Rejected rechecks do not suppress later originally qualifying pairs; newly matching
targets or source insertions are never discovered during the wait. REPEATABLE READ
retains its source snapshot and reports newer target update/delete conflicts.
Target RX admission precedes source AS admission; ordinary source tuples are read-only.

Table DELETE on target and SELECT for every referenced source are required,
including empty/FALSE/constant source queries. Target SELECT applies only to
actually read columns. ON/merged-key/output reads retain physical column authority.
Prepare has no mutation/draw effects; each held Execute rebinds and checks current
rights and metadata. Late source/predicate/output failure and prepublication
cancellation publish neither partial effects nor successful output. Existing
explicit-abort/savepoint rules apply; sequence draws remain nontransactional.
Native Dump/Restore serializes committed survivors and unchanged source objects,
not private deletions or RETURNING history.

Smoke: `make check RUN='^TestReferenceDeleteUsingSmoke'`;
pinned mode: `make check REFERENCE=1 RUN='^TestReferenceDeleteUsingSmoke'`.


### DELETE RETURNING — change note

`DELETE FROM t WHERE id=$1 RETURNING *,old.*,new.*,id+10 AS bumped`
returns one output occurrence per actual deletion, preserving duplicates rather
than applying DISTINCT. No implicit ordering is promised. The output list uses
the already-admitted general scalar projection surface, labels and mixed stars,
including supported casts/conditional/functions and sequences. Unqualified and
visible target/alias-qualified columns read the actual deletion predecessor;
OLD reads that same row. Ordinary NEW reads typed NULL, not a blanket NULL
expression result: `new.id IS NULL` is true and `COALESCE(new.id,old.id)` is OLD.
Plain outputs retain source type/typmod/collation/origin; NEW is nullable even
when its target column is NOT NULL. Empty results retain the complete schema.
A zero-column `RETURNING *` fails42601; `RETURNING *,1` remains nonempty.

`RETURNING WITH (OLD AS o, NEW AS n) o.*,n.*` replaces default names, with
either option order and quoted aliases. Options require AS, cannot repeat a
kind, and cannot duplicate a visible target/source name or another explicit alias.
Explicit aliases and visible target/source qualifiers mask default OLD/NEW names:
`DELETE FROM t AS new RETURNING new.*` reads OLD;
`WITH (NEW AS n)` additionally makes n.* NULL;
`WITH (OLD AS new)` makes new.* OLD and hides old;
`WITH (NEW AS old)` makes old.* NULL and hides new.
These table-only namespaces exist only in RETURNING, not WHERE. Unqualified
columns named old/new remain ordinary columns; unqualified * expands the visible
target plus USING sources once. Target/OLD/NEW qualified stars contain target columns
only, while source-qualified stars expose that source. An actual target alias
still hides the original relation name.

RETURNING joins WHERE's actual-column ACL manifest: table DELETE plus SELECT on
every referenced column, including NEW NULL/star and skipped conditional children.
Constant outputs alone require no SELECT. Prepare describes output/parameters
without scanning, deleting, drawing sequences or evaluating row-owned output.
Held execution freshly binds and authorizes; changed result signatures fail0A000
before deletion. Immutable/frame conversions preflight even without WHERE.

Projection is eager after each successful row stage, using the actual current
predecessor after contention and predicate recheck, never stale captured values.
Concurrent-deleted/recheck-false rows have no output or sequence draw; later
insertions do not enter the fixed candidate set. All output/type/REGCLASS checks
and final cancellation checks precede atomic Finish/public exposure. Query
returns materialized owned rows; Next never reevaluates expressions. Exec
evaluates outputs even when ignored, closes their rows and retains RowsAffected.
Output failure/cancellation before publication discards all transactional deletes
and follows existing explicit abort/savepoint rules. Sequence effects are not
rolled back. Cancellation after a successful Query cannot retract publication.

Native retained result values/schema survive rollback, Reset/Restore and later
generations. Dump serializes committed survivors, descriptors/defaults/NOT NULL,
comments, views and current sequence state, excluding private deletions and
output cursors/history. Restore continues to reject DELETE RETURNING scripts.
Whole-row OLD/NEW records and SRF providers,
triggers/FKs and rewrite-to-UPDATE (where PostgreSQL NEW can be
nonnull) remain real PostgreSQL-only controls, not native engine mechanisms.
PostgreSQL18.0's excluded parenthesized whole-row expansion
`RETURNING (old).*,(new).*` can expand both through the target's ordinary OLD
namespace: the independent one-column id1 witness returns `(1,1)`, not
`(1,NULL)`. This record-expansion path is distinct from supported scalar
`new.id` and direct `new.*` (typed NULL), and from a rule rewriting DELETE to
UPDATE (where NEW is the actual update replacement).
Physical ctids/tuple error Context and PostgreSQL wire/C source fields are not
fabricated by native diagnostics.

Feature commands: `make check RUN='^TestReferenceDeleteUsingMigratedReturning'`,
`make check RUN='^TestReferenceDeleteReturning[A-Za-z0-9_]*$'`,
and `make check REFERENCE=1 RUN='^TestReferenceDeleteReturning[A-Za-z0-9_]*$'`.


Immutable row identities/end roots preserve READ COMMITTED views, duplicates,
zero-column occurrence counts and already-returned cursors. Successful earlier
own INSERT/DELETE commands are visible in the next command; peers see only the
combined committed batch. Rollback or an admitted error/cancellation discards
private inserts/deletes and releases transaction holds. All predicates and
preflight complete before atomic publication, and publication precedes lock release.
Go context errors remain ordinary errors. Ordinary SELECT and writes on different
rows are not blocked by a DELETE row hold; same-row DELETE waits and revalidates
the original candidate, never retries SQL or widens its candidate snapshot.
Compatible TABLE/COLUMN COMMENT and ACL metadata revisions do not invalidate
staged or waiting DELETE: authority follows the exact storage-shape generation,
object/epoch and row/version/ordinal, not descriptor revision. ACL changes do not
retroactively reauthorize staged work at COMMIT; each fresh prepared Execute
still checks current privileges. Replacement, removal/recreation and incompatible
row-shape changes remain fenced.

REPEATABLE READ atomically retains the immutable version headers/end roots of
all relations, including empty and never-read tables, on the first data analysis
or SELECT (including identity/settings functions), not at BEGIN. No row values
are copied. Later statements see that committed frontier plus successful own
changes. Current names, descriptors and ACLs still bind at grant time: a new or
recreated identity has an empty committed root at the retained frontier.
Compatible COMMENT/ACL revisions remain valid; incompatible saved storage/row
shape is the ordinary native `ErrCatalogReplaced` boundary, not transactional-DDL
compatibility. Native Reset/Restore remains an epoch fence; returned rows retain
their own immutable pins across end, CHAIN and teardown.

`default_transaction_isolation` and `default_transaction_read_only` are typed
session defaults. SET/LOCAL, RESET, session characteristics and set_config share
the settings journal: commit preserves SESSION, rollback restores, LOCAL expires.
SHOW/SHOW ALL/current_setting of `transaction_isolation`/`transaction_read_only`
resolve the active owner. Current values cannot be RESET, SET TO DEFAULT or reset
by set_config(NULL): `0A000`, `parameter "<name>" cannot be reset`. RESET ALL
excludes current modes but transactionally resets defaults.

A changed requested isolation after the first data snapshot fails `25001`,
`SET TRANSACTION ISOLATION LEVEL must be called before any query`; identical
assignment is allowed (RU and RC are different requested modes). RW to RO is
allowed after a query/write; late RO to RW fails `25001`, `transaction read-write
mode must be set before any query`. Outside a block SET TRANSACTION warns `25P01`,
`SET TRANSACTION can only be used in transaction blocks`, at execution only.
Read-only execution rejects INSERT/UPDATE/DELETE and supported CREATE/ALTER/DROP
table/schema/role and COMMENT tags with `25006`, `cannot execute <tag> in a
read-only transaction`. Utility rejection precedes object resolution,
authorization and lock acquisition, as well as the native explicit-utility
boundary. DML retains semantic analysis before its read-only execution check.
Valid data-write Prepare analyzes/describes without execution rejection;
utility Prepare inside an explicit transaction remains native `0A000`.
Held statements recheck at Execute. Ordinary (nonlocking) SELECT, settings,
controls and supported ACCESS EXCLUSIVE LOCK remain allowed. Locking SELECT
uses analysis-before-read-only execution diagnostics like DML. Failures
abort/discard prior private work.
Feature smokes: `make check RUN='^TestReferenceSerializableMigratedIsolation$'`
and `make check REFERENCE=1 RUN='^TestReferenceSerializableMigratedIsolation$'`.

INSERT query sources use that same live view, including earlier own deletions:
self-insert copies only captured survivors, never deleted history or its new batch.

Dump captures only committed survivors (including duplicate/zero-width rows), not
private deletion or tombstone history; canonical CREATE/INSERT restores those
survivors and supported metadata. DELETE scripts remain outside Restore's whitelist.
Deleted NULL history does not prevent SET NOT NULL, while live NULL and subsequent
NULL insertion retain genuine 23502 enforcement. DELETE's final command image
also enforces real immediate NO ACTION foreign keys: a surviving referencing row
prevents removal of its parent key. The complete FK rejection witness is shared
by native and pinned PostgreSQL, not an oracle-only or synthetic constraint hook.

Feature commands: `make check RUN='^TestReferenceDeleteUsingMigratedCore'`,
`make check RUN='^TestReferenceDeleteCore[A-Za-z0-9_]*$'`,
and `make check REFERENCE=1 RUN='^TestReferenceDeleteCore[A-Za-z0-9_]*$'`.
Integrated-expression smoke:
`make check RUN='^TestReferenceDeleteUsingMigratedExpressions'`.

### DELETE change log

- Added DELETE USING through shared source grammar/binding/join stages, physical
  successful-target deduplication and source-aware metadata/RETURNING scope.
- Captured qualifying pair identities and immutable source occurrences before
  row waits; rechecked latest targets without rescanning or weakening mutation authority.
- Migrated baseline DELETE/RETURNING/sequence public smoke routes into permanent
  shared reference scenarios, preserving documented no-USING boundaries.
- Added located DELETE RETURNING output lists/OLD-NEW aliases, source-typed NEW
  NULL, shared scalar binding/preflight and output-column ACL requirements.
- Added eager actual-predecessor finalization, prepared output signatures and
  atomic output-error/cancellation handling without a second source snapshot.
- Added public/reference direct/prepared, output metadata, real contention,
  sequence demand, diagnostic and native-control regressions/feature smokes.
- Qualified supported scalar cast children use the RETURNING-only namespace,
  including nested conditional children and custom output aliases; legacy
  DELETE WHERE and unrelated cast-admission guards remain unchanged. REGCLASS
  keeps its existing independent qualification and source-type restrictions:
  NULL/literal/parameter inputs are admitted, not TEXT-column casts even when
  that column is NULL.
- Comment inquiry outputs reuse the statement capture-frontier catalog/role
  snapshot rather than live lookups. COMMENT setup retains its existing
  committed utility boundary; DELETE rollback restores rows independently.
- Complete target binding precedes unresolved output-parameter finalization,
  preserving ordinary occurrence-sensitive parameter diagnostics and inference.
  Plain `current_schemas` output retains its established known-nullable metadata.
- Cross-kind alias witnesses include both swapped option orders, hidden defaults,
  non-NULL payloads and actual held-handle empty/fresh reuse.


- Added positioned DELETE grammar, target-only alias qualification, shared typed
  predicate binding and execution-time DELETE/read-column ACL requirements.
- Added immutable committed/private deletions, combined insertion/deletion
  publication, exact counts and transaction-owned row arbitration.
- Migrated retained SELECT, Dump, catalog replacement/shape and NOT NULL consumers
  to live membership; retained snapshots and failed publications remain unchanged.
- DELETE column predicates evaluate in the shared binder's row frame, including
  qualified aliases and held prepared parameters. Qualified missing-column
  diagnostics render decoded components (`column D.Zzzzz does not exist`).
- Foreign replacement retains one final cancellation boundary after live-row
  filtering and authority preparation; cancellation cannot retract installation
  after that successful observation.
- Corrected DELETE lifetime/publication fences to use existing storage-shape
  identity rather than descriptor identity, preserving compatible COMMENT/ACL
  publication without relaxing replacement or predecessor authority.
- Integrated the existing null/truth/DISTINCT predicate nodes and frame-only
  scalar-cast preflight into DELETE, preserving current qualification boundaries,
  fresh authorization and storage-shape fencing after preflight.
- Corrected decisive-branch reference expectations to assert PostgreSQL's exact
  optimized counts/survivors separately from native conversion diagnostics and
  atomic preservation, without changing the shared evaluator or cast preflight.


## Null argument counts

`num_nulls` and `num_nonnulls` accept 1–100 heterogeneous scalar arguments
without common-type coercion. They are immutable and nonstrict: explicit NULL
positions count as null, while zero, false, empty strings and nested count results
are nonnull. Results are nonnull INT4 with native int64 values and the selected
function name as the column label; physical origin is absent and scalar
nullability metadata stays conservative.

Calls work in table SELECT projections and nested selected arguments.
Without FROM, ordinary local-unit analysis applies; there is no selected-call
syntax whitelist. Former signed numeric literals remain valid arguments.
Computed arithmetic in these calls fails native0A000 at the operator in
both table-backed and no-FROM queries.
Both forms use the same binder, typed execution and finalized query pipeline:

```sql
SELECT num_nulls(1,NULL,'x'),num_nonnulls(1,NULL,'x');
SELECT num_nulls(v,s),num_nonnulls(v,s) FROM t WHERE id=$1;
```

Direct and prepared invocations share analysis and metadata. The second example
can hold a prepared statement and reuse its INT4 predicate parameter. Variadic
ANY arguments themselves do not infer parameter types: `SELECT num_nulls($1)`
fails analysis with42P18 even if the Go value is nil, integer or string. An
initially unknown ANY occurrence is not retroactively typed by a later target
TEXT fallback or WHERE constraint; that mismatch has42P08 at the occurrence.
Existing bare-target42P08 behavior remains unchanged.

Zero arguments fail42883 with PostgreSQL's function-resolution Hint; over100
fail54023 after child analysis: a missing column or failing nested call takes
precedence, while unresolved ANY parameters still reach the arity error before
whole-statement type finalization. Explicit VARIADIC scalar operands fail42804 at the operand, and
multiple ordinary/VARIADIC operands fail42883 signature lookup. Arrays, including
PostgreSQL's NULL-array/empty-array cases, are unsupported; ordinary NULL is a
single null argument, not a NULL array. No calls are added to INSERT, settings
arguments or WHERE. General functions, function qualification, casts outside the
bounded baseline and character-family sets, arithmetic, DISTINCT, ORDER BY within
calls, FILTER, OVER, star and named arguments remain outside the native surface.

Feature smoke: `make check RUN='^TestReferenceNullCount'`.
Independent pinned PostgreSQL witnesses:
`make check REFERENCE=1 RUN='^TestReferenceNullCount'`.

## Logical query results

SELECT now uses one bound source/filter/sort/projection pipeline and the same
typed result contract as identity SELECT, SHOW and setting calls. Finite results
are finalized eagerly before statement completion: a later projection or sort
failure exposes no cursor or successful statement fragment. Public iteration
never evaluates targets or publishes writes. Direct-column output retains
immutable captured source/index views rather than copying whole tables;
computed output retains only finalized projected values.

Rows own their schema independently of the statement. Empty results preserve
column names/types; zero-column SELECT preserves one occurrence per input row.
Physical projections preserve origin and known nullability, while scalar
nullability remains unknown. WHERE retains only TRUE (not FALSE or NULL).
Duplicates remain duplicates. Without ORDER BY, output is a bag with no promised
sequence; sort peers have no portable tie order. ASC places NULL last and DESC
places NULL first; filter and sort-only columns do not leak into output metadata.

EOF, cancellation and iteration errors automatically release result resources
without erasing metadata or saved exported values. At the raw driver cursor,
the originating error remains sticky until idempotent Close; closed Next returns
EOF. Ordinary context errors remain ordinary Go errors. Mutable exported bytes
(including timestamp infinity) detach from both internal values and other
exports. Captured rows/types remain stable across append, UPDATE, Reset, Restore
and transaction completion; result retention does not grant publication authority.

Missing ORDER BY identifiers and analyzed relation names now retain their token's
one-based **character** Position during Query and Prepare, including multibyte
SQL and empty input. COMMENT/ALTER/LOCK utility records keep their own semantics.
This migration adds no SQL syntax, binder, catalog, storage or routine-frame path.
Run `make check RUN='^TestQueryCoreSmoke$'` for direct/prepared
projection/metadata, bags, NULL/empty/zero-width output, ordering, sticky raw
cursor cancellation, byte ownership and retained native-control results.
Independent PostgreSQL witnesses are
`make check REFERENCE=1 RUN='^TestReferenceQueryCore'`; feature-local unordered
fixtures independently check cardinality and every occurrence in both consumed
and recorder-captured rows before canonicalizing only for backend comparison.
Duplicate, NULL and zero-width row counts remain significant; ordered rows and
column metadata still require exact sequence equality.

## UPDATE RETURNING — change note

`UPDATE t SET a=b,b=a WHERE id=2 RETURNING id,a,b,old.a,new.a,
new.a-old.a AS delta` returns each successfully updated occurrence, including
duplicate and unchanged matches. Unqualified columns, `*`, `t.*` and `public.t.*`
read the complete constrained NEW row. `old.*` and `old.column` read the actual
immediate predecessor used by SET, not a stale pre-wait snapshot. Rechecked-false,
concurrently deleted and UNKNOWN/nonmatching candidates return none; no global
rescan discovers newly matching rows.

`RETURNING WITH (OLD AS o, NEW AS n) o.a,n.a` supplies output-only aliases.
Options may be reversed/quoted. Each alias hides its corresponding default name;
cross-kind explicit aliases mask conflicting implicit names. Visible target names
take precedence: a table named old makes old.a mean NEW, so use OLD AS o for its
predecessor. Labels name outputs, not sibling inputs. Target aliases hide the
original target; SET RHS/WHERE admit visible qualified target and source columns.

Outputs use incumbent scalar providers, casts, conditional expressions, parameters
and sequences. Names follow SELECT conventions; explicit labels override them.
OLD and NEW direct columns retain declared types/typmods/nullability and attribute
origins, unlike DELETE's absent NEW. No-match Query retains full metadata.
Prepare describes only, mutates/draws nothing, and retained execution imports fresh
arguments, rebinds current dependencies/ACLs and checks its cached result signature.
WHERE binds first, RETURNING including unknown-output TEXT fallback next, SET last.
Thus `SET id=$1 RETURNING $1` reports 42804 at the assignment, while typed WHERE
can establish INT4 and make it valid. `$1,$1::int4` outputs report 42P08 at the
first output occurrence; reverse order succeeds. Positions count UTF8 characters.

Aggregate outputs are rejected, not evaluated. Ordinary SUM binding preserves
child/FILTER errors first, then checks namespace, arity and overload before
reporting42803, `aggregate functions are not allowed in RETURNING`, for valid
numeric inputs. Unary typed INTERVAL SUM retains that same clause diagnostic,
without an interval runtime provider. A missing column remains42703; TEXT,
unavailable qualified calls, zero/star/multiple arguments fail42883 with the
function-signature Hint (or the misplaced-ORDER-BY Hint for multiple ordering
keys); unknown unary arguments fail42725 ambiguity.
Numeric SELECT SUM is executable; valid SUM in UPDATE assignments/INSERT VALUES
gives42803 after child/FILTER and signature analysis.
Window calls are diagnosed as illegal in RETURNING; SRFs remain excluded and
existing COUNT/modifiers retain their ordinary clause restrictions.

Each successful constrained row is projected exactly once before atomic Finish.
Late output conversion/division/overflow and prepublication cancellation expose
no partial output/count or replacement and use existing transaction abort/savepoint
rules. Constants need no SELECT; both real versions/stars/conditional children
record actual read-column privileges independently of UPDATE assignment rights.
Next/Close never evaluate expressions; Exec evaluates outputs even when discarded.
Volatile draws occur per output occurrence, not for no-match/skipped rows/Prepare/
Next; actual sequence draws survive failure and rollback.

Eager values/schema/settings are owned after successful Finish and remain stable
after rollback, later DML and native Reset/Restore. Dump serializes committed live
rows/catalog/sequence state, never private replacement/history/cursors; Restore's
mutation whitelist is unchanged and rejects UPDATE RETURNING scripts atomically.
View targets use current public relation/column GRANT/REVOKE authority.

Triggers and whole-row records/parenthesized expansion,
rewrite rules, partition movement and physical/C/wire tuple metadata
remain external PostgreSQL mechanisms. The reference suite exercises real trigger
transformation/suppression and whole-row controls, not native successful no-ops.
DEFAULT/row assignments/top-level WITH UPDATE and recursive inheritance
remain excluded; leaf inherited-table DML is supported. No new evaluator,
durability or retry path is introduced.

Simple view targets preserve these UPDATE/DELETE RETURNING routes: output
names/types/stars and OLD/NEW aliases remain logical view metadata, while values
come from the actual predecessor and constrained complete physical base row.
UPDATE FROM and DELETE USING source columns retain their own namespaces.
Invisible base rows are excluded before user work and rechecked after row waits;
an UPDATE leaving the view still returns NEW without filtering it away.
Computed columns may be demanded by RHS/predicates/RETURNING, but cannot be
destinations. All late errors discard staged writes and unexposed output.
Independent stored subqueries require their defining layer's current-owner or
original-invoker SELECT rights, separately from the main target's conditional
read demand. Each demanded physical source selects live versions and own changes
before applying its frozen attribute layout, so unrelated source column additions
cannot expose deleted or superseded versions. Held Prepare retains source
admission without executing these ACLs; Execute rebinds and authorizes afresh.

Feature commands:
`make check RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)$'`,
`make check RUN='^TestReferenceUpdateReturning[A-Za-z0-9_]*$'`,
`make check RUN='^TestUpdateReturning'`,
`make check REFERENCE=1 RUN='^TestReferenceUpdateReturning[A-Za-z0-9_]*$'`.

The complete RETURNING selector includes the bounded ConsumerRoutes owners:
all seven alias-option and nine parameter-projection actual DB.Query vectors,
full aggregate/count/diagnostic/atomicity unions, Begin/QueryRow sequence demand
and SAME held parameters remain beside recorded Context/prepared companions.
Default/source setup and EACH failure sentinel retain their actual public
methods, full descriptors/raw cells and checked statement/cursor teardown.


## UPDATE FROM — change note

`UPDATE t AS dst SET a=src.v FROM s AS src WHERE dst.id=src.id
RETURNING old.a,new.a,src.v` combines a target namespace with the existing
table/view inner/cross/outer FROM namespace. Aliases hide originals; source aliases,
positional column aliases, ON, USING, NATURAL and parenthesized join aliases retain
query semantics. SET destinations remain bare target columns, never aliases or
source columns. Ordinary `*` expands target NEW then exposed source columns;
qualified source stars preserve query metadata and merged-column origins.
OLD/NEW pseudo aliases are output-only and cover only the target. Real visible
target/source qualifiers named old/new mask implicit defaults, not explicit aliases.

Parenthesized non-lateral VALUES sources share this UPDATE namespace, including
fresh held parameters, source/OLD/NEW outputs and mixed physical-source joins.
The UPDATE target is prohibited inside a VALUES source, not merely awaiting a
LATERAL qualifier. `UPDATE t x SET v=q.n FROM (VALUES (x.id,7)) q(k,n)` reports
42P01 with the inaccessible-entry Detail and character Position, without a
LATERAL Hint (direct and semantic Prepare, including empty targets). Ordinary
preceding FROM-source table/column references retain their existing LATERAL
suggestions; VALUES gains no correlation or LATERAL syntax.

Stored source views use the shared current-identity read-graph validation during
binding, including recursive rewrite detection. At Execute, UPDATE validates its
preflighted FROM root and stored-view descendants through the existing query join
legality checks, before staging any target replacements or RETURNING output.
Semantic Prepare does not reject a FULL non-equijoin condition; Execute reports
0A000, `FULL JOIN is only supported with merge-joinable or hash-joinable join
conditions`, with no partial result or target effects. Outer-source NULL extension
preserves outward nullable descriptors without weakening physical target
constraints or mutating source catalog declarations.

One coherent selected view captures the target and all expanded sources, including
private writes. Candidate descriptors retain stable target RowID and source-row
identity, without copying every product row. WHERE rejects FALSE/NULL. Each eligible
pair evaluates simultaneous SET against the captured predecessor/source; later
successful pairs for the same RowID do not validate, replace, count or return again.
There is no promised source winner, pair order or output order. Equal-valued targets
remain distinct physical occurrences. A self source never reads this statement's
staged replacements. RETURNING combines constrained NEW, actual predecessor OLD
and the same winning source, not an arbitrary later source match.

READ COMMITTED freezes source/candidate membership before target row waits. A
changed target is refreshed and rechecked against that same frozen source; rejected
or deleted targets are skipped without searching newly matching rows. REPEATABLE
READ uses existing concurrent-update/delete serialization failures. Relation
admission captures data only after every required relation lock; retained views
cannot widen. Native package-function queue companions prove the exact wait/frozen
source boundary; public callbacks assert no capture ordering without a real barrier.
PostgreSQL queue controls use its actual blocking relation/row state.

Target UPDATE and actual target/source SELECT dependencies are separate; every
source is checked even with no matches. Prepared execution retains valid relation
identities, reanalyzes invalidated sources and checks fresh privileges/parameters
against fixed result signatures before mutation. Destination
casts, source ON, WHERE, SET and RETURNING share existing eager immutable preflight.
PostgreSQL may prune immutable demand or stream early Query rows before a terminal
error; native eager preflight and no-partial-output are deliberate boundaries.
Both backends preserve statement mutation atomicity; sequence draws are nonrollback.
Retained native output survives rollback and Reset/Restore. Persistence includes
committed live heads/catalog/sequence state, never source captures or output history.

General lateral derived queries/other function FROM, top-level WITH UPDATE,
row assignments and DEFAULT, and whole-row UPDATE/DELETE records remain excluded;
INSERT RETURNING is supported.
No new SQL GRANT API, retry, sequence provider or optimizer is introduced.
Feature smoke: `make check RUN='^TestReferenceUpdateFromSmoke$'`.
Quick: `make check RUN='^TestReferenceUpdateFrom'`.
Independent pinned PostgreSQL: `make check REFERENCE=1 RUN='^TestReferenceUpdateFrom'`.

## Scalar UPDATE

`UPDATE table SET column = expression [, ...] [WHERE predicate]` supports the
existing scalar literals, parameters, old unqualified columns, comparisons,
Boolean operators/grouping, null/truth/distinct predicates, baseline explicit
INTEGER/BIGINT/TEXT/BOOLEAN casts and standalone selected null-count calls.
Existing INTEGER/BIGINT arithmetic also participates in SET and WHERE through
the same binder and provider, preserving its operand/type boundaries, NULL
propagation and division/overflow errors; UPDATE adds no arithmetic provider.
DATE and BYTEA participate through existing destination/contextual input rules,
including typed NULL; this does not add explicit DATE/BYTEA casts or arithmetic,
row assignment, DEFAULT or new casts/functions. CURRENT OF has the separate
positioned UPDATE contract above.
Target aliases/FROM and RETURNING have the shared surfaces described above.

All RHS expressions read the same old row: `SET a=b,b=a` swaps values.
Assignment conversions use the destination type and typmod; the completed row
must satisfy existing NOT NULL constraints. Every qualifying logical row counts
once, including unchanged values and duplicate-valued occurrences; no match
returns zero. Direct and prepared commands expose this count through
RowsAffected, with no LastInsertId; omitted RETURNING has no returned row schema. Analysis rejects bad
targets/types/duplicate assignments even on an empty table; late conversion,
constraint or cancellation failures publish none of the statement's rows.
Assignment type mismatch (`42804`) retains the rewrite/cast Hint and points to
the RHS expression start, not an arithmetic operator. Grouping is transparent,
postfix casts retain their operand's start, and Position counts UTF8 characters
rather than bytes. This applies to direct execution and semantic Prepare;
operator-resolution errors retain their own operator-token positions.
REAL/DOUBLE PRECISION column assignments to SMALLINT/INTEGER/BIGINT round ties
to even before checking the destination's signed bounds. NaN, infinities and
out-of-range rounded values fail with 22003 and no partial effects. This is a
direct assignment-only provider edge, not an implicit comparison cast or a
text detour; existing NUMERIC-to-integer ties still round away from zero.

The shared recursive conversion preflight visits every RHS and WHERE tree.
Immutable conversions depending only on the current imported parameter frame
fail at Execute even on empty or rejected input, before executor table/column
ACLs, data capture or row holds. Semantic Prepare still succeeds for those
execution-owned conversions. Folding copies only changed execution branches;
analyzed expressions, conservative dependence metadata and later parameter
frames remain independent. Analysis/namespace checks precede preflight; exact
locked relation identity and current authority are revalidated after it.
Row-dependent RHS conversions run only for qualifying rows. Existing native
Boolean evaluation does not promise short-circuiting: PostgreSQL can simplify
`WHERE FALSE AND CAST(n AS INTEGER)=0` to zero rows while native can evaluate
the row-dependent cast and report 22003. This is distinct from the shared
rejected-row RHS witness `SET a=CAST(n AS INTEGER) WHERE FALSE`, which counts
zero without evaluating the cast on either backend.

DATE exports remain native canonical strings versus pq civil `time.Time`
values. BYTEA parameters are raw bytes, including strings such as `\q`;
malformed textual BYTEA diagnostics apply to SQL unknown literals, not those
raw binds. Whole-row NOT NULL Detail uses the command's captured output
environment, including current hex/escape `bytea_output` for updated payloads.
Parameter and result bytes remain detached, and Dump/Reset/Restore preserve
current updated DATE/BYTEA heads without encoding presentation settings.

Updates preserve logical row identity and install fresh immutable versions.
Self reads see the latest successful private head once; peers see committed
heads until commit, and rollback discards private replacements. Retained results
keep their captured values. READ COMMITTED freezes qualifying candidates, takes
transaction-owned NO KEY UPDATE locks, and after a competing updater commits
rechecks WHERE and RHS against that same row's latest version. It never discovers
initially rejected or concurrently inserted rows. Different-row updates and
ordinary SELECT can proceed while a row is held. Row waits honor the existing
lock/statement deadlines and release replacement admission while queued;
cancellation aborts explicit work and releases its holds.
Explicit row holds survive statement, result and pooled-connection borrower
close; commit, rollback, admitted failure or physical session end releases them.
Cancellation during payload construction or multi-relation commit publishes no
replacement roots. Failed/canceled Restore preserves eligible live holders and
waiters; successful Restore fences old row requests before same-name reuse.

Candidate storage grows only with actual matches. A no-match UPDATE does not
copy the committed root; a sparse replacement copies that immutable root once,
using a linear single-row slot lookup rather than a table-sized auxiliary index.

Dump emits only current committed heads as canonical CREATE/INSERT, preserving
duplicate occurrences, not UPDATE history or private state; Reset/Restore and
ALTER consumers use the same current-head storage. UPDATE requires UPDATE on
assigned columns/table plus SELECT on actual RHS/WHERE reads, not on protected
columns merely retained. SQL GRANT remains excluded.

UPDATE and DELETE share one physical head vector, immutable deletion roots and
generation guards. DELETE does not compact that vector: candidate ordinals are
hints, while row arbitration and latest-predecessor liveness use stable RowIDs.
UPDATE's NO KEY UPDATE and DELETE's row UPDATE are distinct conflicting modes
on the same resource. A peer committed deletion makes a waiting UPDATE affect
zero rows; DELETE rechecks a peer-updated predicate and ends the actual latest
version, never resurrecting a head or admitting newly matching rows.
Earlier own UPDATE and INSERT heads appear exactly once in SELECT/INSERT SELECT,
and later own DELETE hides them immediately. Mixed head/end commits publish at
one frontier; cancellation discards both kinds of private work. Compatible
COMMENT/ACL revisions preserve a captured valid layout/storage token, with fresh
execution still checking current authority. Reset/Restore and generic self/foreign
catalog replacement detach/remint only live heads; retained old results remain
immutable. These are existing surfaces composed, not additional DELETE syntax.

Mixed-DML witnesses: `make check RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$'`.
They cover direct/prepared duplicate-preserving composition and commit/rollback,
cross-mode latest-row counts and frozen candidates, mixed cancellation/resource
reacquisition, immutable candidate slots, all-relation publication, live-only
control/import behavior and identity exhaustion. Native internal waits observe
the actual manager; PostgreSQL waits observe transaction-ID locks and the exact
blocker. Public native effect/deadline tests do not claim queue-observation proof.

Feature smoke: `make check RUN='^TestReferenceUpdateCoreMigratedSmoke'`.
Docker-free and independently pinned PostgreSQL witnesses:
`make check RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$'`.
The diagnostic oracle distinguishes direct/Prepare/bind/runtime phases and
asserts full portable fields, UTF8 character positions and atomic failures.
The destination matrix covers TIME/TIMESTAMP precision, UUID input and NULL,
TEXT assignment conversion and NUMERIC ordinary/negative/wide-scale sizing,
including late multirow overflow. Bad parameter input intentionally has no
native portal Context; pq identifies its unnamed execution portal. SQL row
deadline diagnostics likewise keep native Context empty while PostgreSQL
reports its physical tuple context, asserted structurally rather than by an
invented tuple identity.
Mixed contextual UPDATE parameter types report 42P08 with the conflicting
occurrence's character Position and old-versus-requested type Detail. This
UPDATE policy does not change INSERT/settings mixed-destination 0A000 boundaries.
Native TIME results remain canonical clock strings; PostgreSQL/pq TIME results
remain civil time.Time values, with independent expected representations.


## Three-valued logical operators

Table SELECT targets and WHERE now support Boolean `AND`, `OR` and `NOT`
through the shared typed expression binder. Results are BOOL, named `?column?`
without a physical origin; computed nullability remains unknown, including
empty results. SQL NULL projects as native `nil`, and only TRUE passes WHERE.

| a | b | a AND b | a OR b |
|---|---|---|---|
| TRUE | TRUE | TRUE | TRUE |
| TRUE | FALSE | FALSE | TRUE |
| TRUE | NULL | NULL | TRUE |
| FALSE | TRUE | FALSE | TRUE |
| FALSE | FALSE | FALSE | FALSE |
| FALSE | NULL | FALSE | NULL |
| NULL | TRUE | NULL | TRUE |
| NULL | FALSE | FALSE | NULL |
| NULL | NULL | NULL | NULL |

NOT maps TRUE to FALSE, FALSE to TRUE, and NULL to BOOL NULL. Scalar comparisons
bind above NOT, then AND, then OR; parentheses override precedence. NOT
associates right, AND/OR left. Thus `TRUE OR FALSE AND FALSE` is TRUE, while
`(TRUE OR FALSE) AND FALSE` is FALSE. There is **no guarantee that the left
operand evaluates before the right**, or that a decisive value suppresses
evaluation of another operand. All operands are analyzed even in
`FALSE AND bad_operand` and `TRUE OR bad_operand`.

Unknown strings, NULL and unconstrained parameters receive contextual BOOL:
`'yes' AND 'no'` is FALSE and `NULL AND NULL` is BOOL NULL. Concrete non-BOOL
columns and already-known parameter types are not implicitly converted; wrong
types report construct-specific `42804`, malformed Boolean input `22P02`, and
invalid syntax `42601`, with source character positions for SQL input.
Logical binding carries each analyzed child's earliest source location into its
parent in constant work per node, preserving positioned diagnostics for long
operator chains and nested NOT without repeatedly walking their raw subtrees.
`SELECT $1 AND TRUE,$1 FROM t` fixes both occurrences as BOOL;
`SELECT $1,$1 AND TRUE FROM t` retains the first unresolved occurrence and
fails `42P08` (`boolean versus text`) on its output fallback.

FROM remains required outside identity/settings special forms and the selected
null-count-only form. Existing binary
scalar comparisons form Boolean leaves in targets and WHERE, such as
`NOT id=1 AND (a OR b)` or `id>=2 AND a=b`. Comparisons keep scalar operands
only: no chaining or logical/comparison expressions as comparison operands.
This adds no logical INSERT VALUES or setting arguments,
casts outside the bounded baseline and character-family sets or general functions.
PostgreSQL also accepts `SELECT TRUE AND FROM t` and `SELECT TRUE OR FROM t`
as TRUE targets with bare aliases `and` and `or`. In native bare-label syntax
these keywords continue logical expressions, so these two incomplete expressions
report `42601` at FROM. Reference tests assert both native failures and PostgreSQL
positive results independently, not false syntax-error parity.

Public feature witness: `make check RUN='^TestLogicalSmoke$'`.
Independent pinned PostgreSQL witnesses:
`make check REFERENCE=1 RUN='^TestReferenceLogical.*$'`.
Docker-free scalar scenarios:
`make check RUN='^TestReferenceLogical.*$'`.
Parser/engine units remain separate:
`make check RUN='^TestLogical.*$'`.



Relation names accept one or two identifier components. Qualified `schema.name` resolves only in that schema; unqualified references follow the session's effective search_path, with implicit `pg_catalog` first unless explicitly positioned. Unqualified creation chooses the first usable explicit destination and requires CREATE there, without falling through on denial. Namespace and relation descriptors carry engine-local epoch-scoped identities, while rows remain stored by relation identity. Internal object references also retain a stable engine-owner token: coincident numeric identities in separate instances are never interchangeable. Reset/Restore preserve that owner while atomically replacing the catalog lifetime, and retained result snapshots remain usable. Built-in namespace identities are internal infrastructure, not SQL catalog tables.

## Search-path visibility inquiries

```sql
CREATE SCHEMA a;
CREATE SCHEMA b;
CREATE TABLE a.t(id integer);
CREATE TABLE b.t(id integer);
SET search_path = a,b;
SELECT pg_table_is_visible('a.t'::regclass), pg_table_is_visible('b.t'::regclass);
-- true, false; SET search_path=b,a reverses these decisions.
SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='t';
-- Scan all rows: exactly two distinct positive OIDs, one true and one false.
-- Retain the complete identity bag; the true candidate identifies a.t.
SET search_path = b,a;
SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='t';
-- Require the same two-OID bag; the unique true candidate now identifies b.t.
SELECT pg_type_is_visible(23), pg_collation_is_visible(811),
       pg_conversion_is_visible(4402);
-- true, true, true for fixed catalog identities.
SELECT pg_table_is_visible(0), pg_type_is_visible(NULL);
-- NULL, NULL; missing identity differs from existing off-path false.
```

All four names accept one implicitly coercible OID argument and return BOOL
(native Go bool, nil for NULL), with no physical column origin. TEXT/BOOL typed
arguments and wrong arity/namespace produce the existing PostgreSQL-style
signature diagnostics; bad unknown OID strings retain positioned input errors.
Prepared `$1` is inferred as OID and imported on each Execute. Project inquiries
in a stored view to compose its BOOL column in predicates, CASE/COALESCE and
aggregates. Selected projections, UPDATE/DELETE RETURNING and CHECKs reuse the
typed expression consumers; INSERT uses Exec then SELECT, and DEFAULT stays
excluded. A selected inquiry beside an ordinary expression such as `$1::oid`
uses a supported FROM source rather than widening relationless SELECT syntax.
Child analysis precedes the 100-argument guard and namespace/signature lookup.
For example, `missing_schema.pg_type_is_visible(missing_column)` from a supported
table reports the missing column (42703), not the absent schema. A 101-integer
call reports 54023 even with that absent schema, but a malformed last child still
reports 42703. These diagnostics and character positions agree for direct SQL
and Prepare; STRICT NULL never bypasses child analysis.

Visibility compares object identity against same-name path lookup, not permission
to read the subject. Schema USAGE filters the effective path. Prepared executions
refresh path/role/catalog state; eager returned values and metadata do not change
after settings changes, rename/drop or native Reset/Restore. Inquiries do not open
subject relations, allocate absent TEMP or mutate data.
Ordinary B-tree index identities use the same namespace as tables, views and
sequences. Thus an earlier same-named relation of any supported kind shadows
an index, and an earlier index shadows those relation kinds. Native Dump/Restore
preserves index definitions; visibility probes rediscover their generated OIDs.
Capture is demanded by bound inquiries, including nested views, RETURNING and
CHECKs; unrelated queries do not retain a visibility snapshot. One explicit
search path produces distinct immutable relation/type and no-TEMP paths.

Stored row-dependent inquiry CHECKs support compact/pretty
`pg_get_constraintdef` and `pg_get_expr` rendering and CREATE TABLE replay.
`SELECT conbin FROM pg_catalog.pg_constraint` exposes native structural data,
not PostgreSQL nodeToString bytes. ALTER COLUMN TYPE rebinds these CHECKs and
captures visibility before validating transformed rows; a rejected transform
leaves the old rows, column type and definition intact.

Numeric relation OIDs are backend-local: discover them from the bounded pg_class
projection above and assert visibility/reversal relationships, not allocator
equality with PostgreSQL or before Restore. Rediscover after Restore and reapply
the session search_path. This does not require a pg_namespace query source,
OID/regclass comparison or explicit regclass-to-oid cast.

The native seeded identity surface includes all25 type descriptors, seven fixed
collations and128 fixed conversions. Registered user collation definitions also
participate in visibility and the read-only pg_collation query source. Collation
eligibility uses UTF8 encoding6/exact-before-fallback inside each namespace;
conversion shadowing uses name, not encoding pair. Inquiry-only seed identities
do not expand comparison or encoding conversion execution.

User metadata/version inquiry uses the same captured catalog authority:
```sql
SELECT collname,collprovider,collencoding,collversion,
       pg_collation_actual_version(oid) AS actual
FROM pg_catalog.pg_collation
WHERE collname = 'user_collation';
```
NULL recorded/actual libc C/POSIX versions remain NULL; registered builtin actual
version is1. pg_collation is read-only. Discover backend-local OIDs rather than
assuming allocator equality or preserving generated numbers across Restore.
See [dialect details and boundaries](DIALECT.md#search-path-visibility-inquiries--change-note).
Public smoke: `make check RUN='^TestReferenceCollationLegacyVisibility$'`.
Real PostgreSQL18.0 evidence: `make check REFERENCE=1 RUN='^TestReferenceVisibilityInquiry.*$'`.

## Integer arithmetic

Table SELECT projections, comparison operands/WHERE, unqualified DELETE
predicates and column-free INSERT VALUES accept
INT4/INT8 `+`, `-`, `*`, `/`, `%`, unary `+`/`-` and arithmetic parentheses.

DELETE uses its existing affected-row, authorization and staged-publication
contracts: arithmetic reads require SELECT on the actual predicate columns,
and late overflow/division failure publishes no deletion. DELETE aliases do
not enable qualified arithmetic; qualified column atoms retain their existing
comparison support. Arithmetic can coexist with existing DATE typed literals
in projection without adding DATE arithmetic or new casts.
Composition smoke: `make check RUN='^TestReferenceArithmeticMigratedDeleteSmoke$'`.

Multiplication/division/remainder precede addition/subtraction; binary operators
associate left, unary signs right. Division truncates toward zero and remainder
has the dividend's sign (`7/2=3`, `-7%2=-1`). Unary signs preserve width;
INT4 pairs produce INT4, either INT8 operand produces INT8. Width is fixed before
assignment: a BIGINT destination cannot prevent INT4 expression overflow.
Checked overflow reports `22003` (`integer out of range`/`bigint out of range`);
division/remainder by zero reports `22012` (`division by zero`), without invented
runtime positions. Minimum divided by -1 overflows; minimum remainder -1 is zero.
Strict NULL returns a result-typed NULL. Numeric literal minus is folded,
including grouped literals, so signed minimum constants retain integer width.
Numeric unary plus is identity: numeric input overflow points at the operand
token, not the plus sign, in direct execution and Prepare (including ORDER BY).
Use whitespace or parentheses before a negative remainder divisor (`i% -1`,
`i%(-1)`). Adjacent `%-` is one symbolic operator, not remainder followed by
negation; unsupported combined arithmetic spellings fail with a typed `42883`
at the operator rather than executing or publishing data. Contextual WHERE
and incompatible-assignment errors use the expression start and resolved type;
missing-operator errors retain the operator token location.
Existing INSERT query-source assignment, common-VALUES typing and excess-source
diagnostics also retain the recursive expression start, including widening
postfix casts and Unicode input. Decorated VALUES now supports ORDER BY and
LIMIT/OFFSET/FETCH through the shared query producer; count decoration still
requires common source typing before destination assignment. Thus boolean and
integer VALUES remain incompatible even when LIMIT would retain only the boolean
row. The source smoke also executes `INSERT INTO good SELECT 1,7/2,-7%2`,
asserting affected count 1 and persisted `(1,3,-1)` with INT4/INT4/INT8 destination
metadata. Smoke: `make check RUN='^TestReferenceArithmeticMigratedInsertSourceSmoke$'`.

Parameters obtain width from a known integer peer, in SQL occurrence order,
not from the Go argument kind or assignment destination. Computed metadata is
`?column?`, INT4/INT8, native int64, with no physical column origin. Existing eager
SELECT and atomic INSERT/Restore publication remain unchanged; Dump stores
canonical values rather than expression source. Parameter-free arithmetic
Restore is supported and nested parameters are rejected.

Existing baseline INT4/INT8/TEXT/BOOL casts compose with these scalar expressions,
using `CAST`, postfix `::`, or the supported function-style cast names. Postfix
casts bind before unary signs: `-i::bigint` widens before negation, whereas
`(-i)::bigint` cannot rescue INT4 negation overflow. Similarly, casting an operand
can widen arithmetic; casting its result cannot undo overflow. Cast preflight
visits arithmetic children even on empty or filtered input, preserving conversion
errors without evaluating the arithmetic root early. Cast wrappers do not admit
computed arithmetic arguments into the bounded null-count functions. Cast
families and BYTEA capabilities remain unchanged.
Composition smoke: `make check RUN='^TestReferenceArithmeticMigratedCastSmoke$'`.

Exact NUMERIC `+`, unary `+`, `-`, unary `-`, `*`, `/`, `%` and `^` use
the shared typed operator path; integer peers widen to NUMERIC without losing
fractional or large-integer precision. Both integer and fractional power retain
PostgreSQL-selected decimal precision; `^` remains left-associative.
Computed results expose NUMERIC/string/unknown DecimalSize independently from
constrained operands. Signed literal input retains its existing normalization.
See [numeric arithmetic](DIALECT.md#numeric-arithmetic--change-note).
Floating operators still use fixed REAL/DOUBLE widths and PostgreSQL mixed
resolution; floating `%` remains missing. See
[floating arithmetic](DIALECT.md#floating-arithmetic--change-note).
Selected mathematical `mod` functions and NUMERIC `%` share exact remainder
semantics. Selected round/scale/
trunc/sign calls compose with existing projections, grouping by projection
ordinal, UPDATE and INSERT SELECT. They may wrap existing CASE scalar arms and
count-star aggregates, but do not add selected function CASE result leaves or
general GROUP BY call syntax.
See [the bounded signature note](DIALECT.md#scale-and-rounding-functions--change-note).
Unary unknown `+` and unknown power select FLOAT8; unary unknown minus and
unknown-only binary +/- are ambiguous with PostgreSQL's `42725` diagnostic.
Power associates left and binds above */%, below unary signs; floating `%`
has no signature. Settings, functions and general SELECT-without-FROM
remain bounded as before.
Feature smoke: `make check RUN='^TestReferenceArithmeticMigratedSmoke$'`;
real pinned reference: `make check REFERENCE=1 RUN='^TestReference(FloatingArithmetic|Arithmetic)'`.

Integral bitwise `& | # ~ << >>` additionally support SMALLINT, INTEGER and
BIGINT in the same general scalar consumers, plus defaults and view replay.
They are separate from ordinary arithmetic: mixed AND/OR/XOR widen, unary
NOT and shifts preserve left width, and shift counts resolve to INTEGER.
Generic operator precedence is below addition and above comparison.
After complete binding, a known SQL constant NULL eliminates the selected strict
bitwise operation in execution-private planning, retaining its resolved width,
computed metadata and conservative dependencies. Row-dependent conversions in
the eliminated peer are not evaluated; unknown quoted input still fails eagerly.
Runtime row/parameter NULLs do not globally short-circuit operand evaluation.
See [integral widths, NULL and pinned shift counts](TYPES.md#integral-bitwise-operators).
Computed bitwise expressions remain excluded from bounded null-count
arguments, independently of ordinary no-FROM query admission. No NUMERIC/float
explicit casts or BIT/VARBIT signatures are added.
Feature smoke: `make check RUN='^TestBitwiseSmoke$'`.


Missing column and invalid SQL `$0` references fail during analysis with `42703`
and `42P02`, respectively, at the occurrence's one-based character position.
The first parameter gap fails `42P18` without a position in native direct/Prepare
and PostgreSQL Prepare. PostgreSQL's zero-argument direct simple query instead
rejects `SELECT $2 FROM t` with `42P02` at `$2`; this protocol boundary is asserted
independently, not normalized into parity. Missing actual Go arguments remain
ordinary adapter/count errors, not PostgreSQL protocol errors.
Prepared executions retain fixed inferred usage and use fresh private
values. Cursors retain captured multiplicity, imported parameters and metadata
across appends, Reset and Restore; no row order is promised without ORDER BY.

Run `make check RUN='^TestExpressionCoreSmoke$'` for the public
direct/prepared scalar, BOOL/NULL, diagnostic, retained-cursor/control and atomicity
witness. Migrated scalar/query assertion unions use
`make check RUN='^TestReference(ExpressionCore|QueryCore)[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReference(ExpressionCore|QueryCore)[A-Za-z0-9_]*$'`;
local engine units remain `make check RUN='^TestExpression.*$'`.
The QueryCore owners include retained captured projection/restore, empty-table
analysis and held1/2/NULL frames formerly duplicated in ExpressionCore.
Go test reports assertion failures or PASS; these commands require execution to
establish verification.

## Null and Boolean comparison predicates

Table SELECT projection and WHERE support scalar `IS [NOT] NULL`, its
`ISNULL`/`NOTNULL` aliases, `IS [NOT] DISTINCT FROM`, and all six Boolean tests:
`IS [NOT] TRUE`, `IS [NOT] FALSE`, `IS [NOT] UNKNOWN`. Each returns a nonnull
BOOL (`?column?`, OID16, typmod−1, native bool ScanType, scalar origin zero).
Expression nullability metadata remains unknown even though these values are
never NULL. Null tests accept every supported scalar type; Boolean tests require
BOOL, contextualizing unknown strings/NULL/parameters but rejecting concrete
nonboolean inputs with `42804` at the operand.

DISTINCT treats two NULLs as not distinct, one NULL as distinct, and nonnull
values through the existing equality provider (including exact numeric,
cross-width integer, NaN, UUID and temporal semantics). NOT DISTINCT is the
complement. Both operands can be admitted scalar atoms, comparisons or grouped
predicates/logical expressions. Unknown operands contextualize to the known
opposite type; two unknown operands use TEXT. Incompatible concrete inputs fail
`42883` at IS
with the ordinary equality signature, even on empty tables or typed NULLs.
An undecorated NULL literal on either side instead becomes a nullness test:
`$1 IS DISTINCT FROM NULL` therefore does **not** infer a parameter type.
Bound nil is not a raw NULL literal: `v IS NOT DISTINCT FROM $1` infers the
column's type and selects its NULL rows when bound nil.

Null tests do not infer parameter types. An entirely unresolved slot fails
`42P18` without Position; an earlier unknown nested occurrence left behind by
a later constraint fails `42P08` at that occurrence. For example,
`SELECT $1 IS NULL,$1 IS TRUE FROM t` fails at character8, while reversing
the targets succeeds with a BOOL slot. Root target TEXT fallback and sparse
parameter/protocol distinctions described above remain unchanged.
Selected occurrence consistency is checked before unresolved shared slots or
gaps. Thus `SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t` reports positioned
`42P08` for the earlier `$1`, not `42P18` for `$2`; likewise an inconsistent
`$2` occurrence takes precedence over the missing `$1` slot. With no occurrence
mismatch, `$1 IS TRUE,$2 IS NULL` still reports unpositioned `42P18` for `$2`.

Parentheses group admitted atoms, comparisons, predicates and logical expressions.
Unary postfix tests nest left: `TRUE IS NULL IS TRUE` and `v IS NULL ISNULL`
both return false.
IS/ISNULL/NOTNULL share nonassociative precedence where a binary DISTINCT
right operand competes with another ungrouped predicate:
`1 IS DISTINCT FROM 1 IS TRUE` fails `42601`; group either intended nesting.
`(v IS NULL) IS TRUE` and `TRUE IS DISTINCT FROM (v IS NULL)` work.
Postfix casts complete their target type before a predicate continuation:
`v::int4 IS NULL`, `v::bool IS TRUE` and `v::int8 IS DISTINCT FROM 0`
apply the predicate to the cast result, not to a multiword type name.
Scalar comparisons bind more tightly than these predicates, which bind above
NOT, AND and OR: `v=1 IS TRUE` and `(v=NULL) IS UNKNOWN` work in projection
and WHERE. Ordinary `v=NULL` remains UNKNOWN and selects no rows; no
transform_null_equals behavior is added. Comparison and predicate operands
admit bounded INT4/INT8 arithmetic and existing baseline and character-family
casts; comparison chaining remains excluded. General functions, casts outside
the bounded baseline and character-family sets, aliases and public composite
values remain excluded. Ordinary no-FROM SELECT and subquery row comparisons
use the shared query pipeline; this does not expand null-count argument
providers. Predicates remain excluded from those function arguments.
ANY and null-test unknown occurrences share binding-order validation:
`num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE` fails42P08 for$2 at18,
while reversing the first two targets fails for$1 at8. Still-unresolved
occurrences do not mask a later mismatch, and mismatches precede slot gaps.
Ordinary INSERT VALUES and settings retain scalar-only arguments. INSERT SELECT
executes the same admitted predicate and selected null-count projection roots;
`INSERT INTO counts SELECT num_nulls(v) FROM t` inserts one count per source row.
Appending `VALUES (1)` instead reports `42601` at `1`, character55: VALUES is
a legal bare FROM alias, but numeric alias-column names are not legal.

Executable acceptance (run these commands to establish verification):

```sh
make check RUN='^TestCompareNullSmoke$'
make check RUN='^TestReferenceCompareNull.*$'
make check RUN='^TestExpressionCompareNull.*$'
make quickcheck # full Docker-free verification after edits
make check REFERENCE=1 RUN='^TestReferenceCompareNull.*$'
```

These exercise independent direct/prepared truth/null/distinct matrices,
held typed parameters, complete eager diagnostics/phases, precedence and
admission, provider families, dependency/ACL manifests, cancellation and
captured computed BOOL results across native controls. Reference checks use
the unchanged pinned real PostgreSQL18.0 fixture, asserting expectations
before backend parity. Source/manual semantics:
[PostgreSQL18 comparison predicates](https://www.postgresql.org/docs/18/functions-comparison.html)
and [operator precedence](https://www.postgresql.org/docs/18/sql-syntax-lexical.html).

## BETWEEN predicates

The four forms `a BETWEEN l AND u`, `a NOT BETWEEN l AND u`,
`a BETWEEN SYMMETRIC l AND u` and `a NOT BETWEEN SYMMETRIC l AND u`
use the existing scalar comparison types, BOOL metadata and WHERE TRUE-only
filtering. Ordinary BETWEEN includes both endpoints and is false for reversed
non-NULL bounds. SYMMETRIC checks both orders. Equal bounds include only equal
values. NOT forms complement those results, retaining UNKNOWN.

Evaluation uses independent binary comparisons and SQL three-valued logic:
BETWEEN is `a >= l AND a <= u`, NOT BETWEEN is `a < l OR a > u`;
symmetric forms combine the two orders with OR and AND respectively.
Thus `5 BETWEEN NULL AND 3` is false and `5 NOT BETWEEN NULL AND 3` true,
not blanket NULL propagation. A NULL tested value gives UNKNOWN.
Unknown literal occurrences resolve independently against each peer;
`'2' BETWEEN integer_column AND text_column` need not coerce the columns to
a common type. Repeated parameters retain the existing shared-slot inference
and prepared execution contracts. Repeated expressions are not promised a
single evaluation or short-circuit ordering.
Prepared temporal literal captures retain contextual type and precision as
well as source position. For example, `'2000-01-02' BETWEEN date_column AND
timestamp_column` independently materializes DATE and TIMESTAMP occurrences;
held prepared execution preserves each frozen value without introducing a
DATE/TIMESTAMP comparison operator or a common type across the three values.

Arithmetic and casts bind above BETWEEN, then ordinary comparisons, IS tests,
outer NOT, AND and OR. The restricted lower endpoint admits ordinary comparisons
but excludes unparenthesized logical syntax. Group upper comparison endpoints:
`TRUE BETWEEN (1<2) AND (2<3)` and `TRUE BETWEEN 1<2 AND (2<3)` are true.
In contrast, `TRUE BETWEEN 1<2 AND 2<3` parses as
`(TRUE BETWEEN (1<2) AND 2) < 3` and eagerly fails42883:
`operator does not exist: boolean <= integer` at BETWEEN. An ordinary comparison
outside BETWEEN is valid, for example `2 BETWEEN 1 AND 3 = TRUE`.
Group nested predicates or Boolean endpoints:

```sql
SELECT v BETWEEN (1+0) AND (2+1) FROM t;
SELECT TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE FROM t;
SELECT id FROM t WHERE v BETWEEN 1 AND 3 AND v=2;
```

Ungrouped BETWEEN chaining is excluded. BETWEEN remains an unquoted column
name; SYMMETRIC remains reserved. ASYMMETRIC is not admitted. Generated
comparison failures point at BETWEEN, or NOT for NOT forms, with the existing
SQLSTATE/English message/Hint; literal failures point at the literal.
Existing DELETE/UPDATE/CASE/conditional, INSERT SELECT, grouping/HAVING and
parenthesized DEFAULT consumers share this lowering without new native paths.
Ordinary scalar-only INSERT VALUES/settings and other documented boundaries
remain unchanged. Grouped predicate values are admitted as BETWEEN's tested
value/endpoints and as ordinary comparison operands, including `(v IS NULL)=TRUE`
and `(id=1)=TRUE`. Ungrouped chains remain nonassociative.
Native Dump/Restore preserves grouped comparison/logical/nested BETWEEN
defaults, including fresh DEFAULT inserts after replay. Predicate-valued
comparison operands serialize directly with structural parentheses, without
CASE identity bridges or evaluation of stored definitions.

Executable feature evidence: `make check RUN='^TestBetweenSmoke$'`,
`make check RUN='^TestReferenceBetween.*$'`,
and `make check REFERENCE=1 RUN='^TestReferenceBetween'` against pinned18.0.

## Explicit baseline casts

Baseline casts also serve as the existing comparison and null/Boolean predicate
operands, Boolean leaves, and selected null-count arguments. They do not admit logical/comparison input to
INSERT/settings or selected calls as Boolean/comparison leaves. Ordinary
no-FROM SELECT admits existing casts without a selected-call witness; function
argument restrictions remain separate.
Grouping applies to existing scalar operands, including columns: `(id)=1` and
`(i)<(j)` are admitted, with or without a cast. It does not turn a comparison or
logical expression into a scalar comparison operand: `(id=1)=TRUE` and comparison
chaining remain excluded.

`CAST(expression AS type)`, `expression::type` and `int4(expression)`,
`int8(expression)`, `text(expression)`, `bool(expression)` execute through the
shared typed expression path in table SELECT, WHERE and INSERT VALUES.
Nested/grouped casts and postfix chains are supported. Standard/postfix targets
accept INTEGER/INT/INT4, BIGINT/INT8, BOOLEAN/BOOL and TEXT aliases; function-style
names are only int4/int8/text/bool, including their quoted lowercase spellings.
Unquoted `integer(...)`, `bigint(...)`, `boolean(...)` and `int(...)` remain syntax
errors; this feature does not introduce general function resolution.

| Source | INT4 | INT8 | TEXT | BOOL |
| --- | --- | --- | --- | --- |
| INT4 | identity | widen | decimal output | zero false, nonzero true |
| INT8 | checked narrow | identity | decimal output | unsupported |
| TEXT | integer input | bigint input | identity | boolean input |
| BOOL | false 0, true 1 | unsupported | `false` / `true` | identity |

NULL retains the target type; unsupported pairs fail even for typed NULL or empty
relations. No transitive conversion is inferred. Explicit casts involving INT2,
NUMERIC, floats or UUID remain native `0A000` boundaries, including same-type
casts. Selected datetime casts are now admitted through the same binder;
see [temporal types](TYPES.md#time-zone-aware-values). Existing non-temporal
assignment/implicit policies are unchanged: BOOL cannot assign to INT4 without
a cast, and concrete TEXT cannot assign to INT4. Unknown quoted input may still
acquire a destination type directly.

The first standard/postfix cast of an unresolved parameter fixes its source type:
`SELECT $1::int4,$1::text FROM t` imports INT4 and returns INT4/TEXT. Later casts
convert that source rather than re-inferring it. Earlier unresolved targets retain
the occurrence-sensitive rule: `$1,$1::int4` fails `42P08` (integer versus text).
`int4($1)`, `int8($1)` and `bool($1)` are ambiguous (`42725`); `text($1)` infers TEXT,
and `int4($1::text)` imports TEXT then converts. Prepared executions retain fixed
metadata and private parameter frames.

Standard/postfix output names preserve column/function names, otherwise use the
normalized target name; function casts use their callee name. Same-type column
casts reduce to the column and retain physical origin; conversions have no column
origin. Unknown literal input errors carry the quote's character Position.
Typed conversion/overflow errors have no Position and finalize before Query exposes
a cursor. Unsupported standard/postfix pairs use `42846`; function pairs use `42883`
with PostgreSQL's function hint. Multirow INSERT and staged Restore remain atomic.
Dump emits canonical typed literals, and Restore recursively admits literal casts
while rejecting nested parameters at their token.
Immutable row/stage-independent conversions, including imported parameter casts,
are preflighted in a fresh execution-private frame even when the relation is empty
or WHERE selects no rows; Prepare still succeeds for typed conversion failures.
SELECT preflight runs outside engine locks before executor table privileges are
checked; namespace lookup/USAGE remains earlier. Valid casts still require SELECT.
Row-dependent conversions are evaluated only for admitted input rows. This bounded
materialization does not implement PostgreSQL's custom/generic plan-cache switching.
Preflight also traverses null tests, Boolean tests and both DISTINCT operands,
including raw-NULL rewrites, without folding the predicate or pruning its dependencies.
Errors from a cast inside equality keep their original diagnostic and Position;
operator-resolution errors report the actual bound operand types.

Public feature smoke: `make check RUN='^TestCastExplicitSmoke$'`.
Review-regression smoke: `make check RUN='^TestCastExplicitReviewSmoke$'`.
Parameter/ACL smoke: `make check RUN='^TestCastExplicitPreflightSmoke$'`.
Pinned PostgreSQL coverage: `make check REFERENCE=1 RUN='^TestReferenceCastExplicit.*$'`.

Normalized integer parameters targeting INT2/INT4/INT8 and Boolean parameters targeting
BOOL use checked scalar imports without successful-value formatting or reparsing.
They retain the inferred descriptor and cancellation checks; INT2/INT4 overflow keeps
the textual-input diagnostic rather than the assignment-narrowing diagnostic.

Integer textual input (quoted constants and string/byte parameters) accepts
optional signs, leading/trailing C whitespace, decimal leading zeros, radix
prefixes and single digit separators, including `0x_FF`, `0o17`, `0b10_10` and
`1_000`. Malformed input is `22P02`; width overflow is `22003`. Parsing order
matters: `2147483648x` is malformed integer input, but a sufficiently long
overflowing digit prefix can fail range before trailing junk. Non-ASCII
whitespace is not C whitespace. Boolean input supports case-insensitive unique
prefixes of true/yes/on and false/no/off, plus 1/0. Boolean codec text output is
t/f; Boolean assignment to TEXT is true/false, independently of driver output.
Integer WHERE literals compare across widths without assignment narrowing:
`int4_column = 2147483648` is valid and matches nothing, while a parameter in
that predicate retains INT4 input and rejects the same overflowing bound text.

SMALLINT/INT2 stores the signed range -32768 through 32767 and returns native
`int64` with INT2 metadata; INTEGER and BIGINT retain their signed-32/signed-64
ranges and existing inline storage. Integer input diagnostics use English names:
`invalid input syntax for type smallint: "oops"` (`22P02`) and
`value "32768" is out of range for type smallint` (`22003`), with `integer` or
`bigint` for those widths. Original input spelling is retained. SMALLINT parsing
order likewise distinguishes `32768x` (`22P02`) from `327680x` (`22003`).
Typed INT4/INT8 assignments to SMALLINT prepare successfully and fail execution
with `smallint out of range` on overflow; INT2 widens implicitly to INT4/INT8
and the supported floats. Bare SQL integer literal inference remains INT4/INT8.
All conversions precede statement publication; a bad later tuple publishes no
rows. Dump emits canonical SMALLINT/INTEGER/BIGINT declarations and decimal
values, and staged Restore preserves widths and retained cursor snapshots.

`make check RUN='^(TestReferenceSmallintArithmeticSmoke|TestReferenceSmallintMigratedSmoke)$'`
exercises shared direct and genuinely held prepared smallint arithmetic,
independent width/bound/error contracts, metadata, NULL and migrated baseline
routes. The broader selector `^(TestReferenceSmallint.*|TestReferenceArithmeticBoundaries)$`
covers atomicity/cancellation, persistent defaults/views and native controls,
retained raw cursors, and authorization. Run both selectors with `REFERENCE=1`
as well for the pinned PostgreSQL18.0 oracle; execution establishes results.

`make check RUN='^TestTypeCoreSmoke$'` exercises public
direct/prepared inference, codecs, exact diagnostics and phases, metadata, byte
ownership, multirow atomicity, cancellation and four-kind native round trips with
retained raw cursors. Go test reports the result.

Focused independent witnesses are `make check RUN='^TestTypeCore.*$'`,
`make quickcheck`, and the pinned real-reference
`make check RUN='^TestReferenceTypeCore.*$' REFERENCE=1`.
Dump uses descriptor-owned canonical SQL literals, and staged Restore uses the
same binder/codecs before replacing the catalog once; existing snapshots retain
their old values, types and column metadata.

## Omitted INSERT columns and defaults

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted columns use their declared default or typed NULL. Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Targetless DEFAULT VALUES requests one all-default row. Conversion, arity, default evaluation and NOT NULL failures publish no rows from the statement. The public feature smoke is `make check RUN='^TestDefaultSmoke$'`.

Preparation checks assignment compatibility; typed integer literals assigned to INT4 are range-checked during execution, before NOT NULL checks. Thus preparing a short INSERT containing `2147483648` succeeds, but executing it fails with `22003` (`integer out of range`) without publishing rows. Unknown quoted numeric strings still run the destination input function during preparation and retain their input-specific diagnostic and cursor position.

That feature smoke exercises both direct execution and an actual prepared short INSERT, checks declaration-order metadata and statement atomicity, and retains a cursor across Reset/Restore. It also checks deterministic full-row Dump/Restore, successful hand-authored short-row Restore, and catalog preservation when a staged short INSERT omits a NOT NULL tail. Go test reports whether those public API assertions pass.

## Conditional expressions

Simple and searched `CASE`, `COALESCE`, `NULLIF`, `GREATEST` and `LEAST` are
closed SQL scalar constructs, not ordinary routine lookup. They compose with
the existing projections, predicates, arithmetic/casts, selected call arguments,
INSERT VALUES/SELECT sources, UPDATE RHS/WHERE, DELETE WHERE and stored DEFAULTs.
Existing consumer restrictions still apply recursively: INSERT scalar inputs
cannot hide columns, DEFAULTs cannot hide columns/parameters/sequence calls,
and qualified references remain DELETE-only (and excluded inside casts).
There is no new ordinary no-FROM SELECT, alias, ORDER expression, array or
VARIADIC admission. CASE requires at least one WHEN; lists are nonempty and
NULLIF has exactly two arguments.

Conditionals also compose with the admitted `count(*)` aggregate in SELECT and
HAVING, and may serve as structural GROUP BY keys. For example,
`SELECT COALESCE(count(*),0) FROM t` returns one INT8 aggregate result.
Aggregate occurrences in every CASE test/WHEN/result/ELSE and list argument are
analyzed before conditional pruning. Thus a dead-arm ungrouped column still
fails grouping validation, and aggregates hidden in WHERE or GROUP BY remain
`42803` errors. Conditional grouping keys use the same structural identity as
other scalar keys; rewriting them does not mutate prepared expression trees.
Existing count(expression), aggregate modifiers and no-FROM aggregates use the
same conditional analysis and immutable rewrite contracts.

```sql
SELECT CASE WHEN v=0 THEN 0 ELSE 10/v END,
       CASE v WHEN 0 THEN 'zero' WHEN 2 THEN 'two' ELSE 'other' END,
       COALESCE(s,'fallback'), NULLIF(v,0),
       GREATEST(v,NULL,2), LEAST(v,NULL,2)
FROM t;
```

Searched CASE takes the first TRUE condition; FALSE and NULL fall through.
Simple CASE evaluates its test once and uses each WHEN's resolved equality
operator. A NULL test does not match a NULL WHEN. Only the selected result is
evaluated; missing ELSE produces a typed NULL. COALESCE stops at the first
nonnull value and yields typed NULL if all arguments are NULL. Extrema evaluate
every argument, ignore NULLs, return NULL only if all arguments are NULL and
retain the first argument on ties. Ordering/equality use type-owned providers,
including NaN equality and BPCHAR's trailing-space semantics. NULLIF evaluates
both arguments, including its second when the first is NULL; strict equality
returns a typed NULL when equal and the first resolved operand otherwise.

### Result types and metadata

CASE outputs and COALESCE/extrema arguments resolve one common type across all
inputs. Unknown quoted literals, NULL and unresolved parameter occurrences stay
provisional until this resolution; all-unknown inputs resolve TEXT. Known types
must have compatible categories and direct implicit coercions to the chosen
type, not assignment conversions or a transitive cast chain. CASE considers
ELSE before THEN outputs when choosing the type, while analysis still visits
test, each WHEN/THEN, then ELSE in source order. The surrounding INSERT/UPDATE
destination cannot redefine the construct's common type: COALESCE(NULL,NULL)
is TEXT even when assigned to an INTEGER column.

The bounded mechanics execute for all supported scalar identities. Numeric
promotion uses existing direct conversions; DATE→TIMESTAMP is implicit with
infinity preservation and checked finite range (`22008`, `date out of range
for timestamp`). DATE→TIMESTAMPTZ and TIMESTAMP→TIMESTAMPTZ use the session
zone through direct datetime providers. Sharing a category alone never creates a
conversion: DATE/TIME remain incompatible. Name→TEXT and TEXT/BPCHAR/VARCHAR→Name are implicit;
Name→BPCHAR/VARCHAR requires assignment conversion. Name input/conversion retains
the UTF8-safe 63-byte bound. Incompatible categories fail `42804`; a same-category input without
the required direct conversion fails `42846`, at that input.

NULLIF resolves a builtin equality signature instead of a common result type.
The result is its coerced first operand: NULLIF(1,2147483648) remains INT4
(cross-width integer equality), while NULLIF(1,2.2) is NUMERIC.
FLOAT4/FLOAT8 equality preserves each operand, so NULLIF's result keeps its
first float type. An integer paired with REAL uses FLOAT8 on the integer side
but retains FLOAT4 on the REAL side: NULLIF(integer,real) is FLOAT8, while
NULLIF(real,integer) is FLOAT4. VARCHAR equality resolves TEXT; equal BPCHAR inputs retain
BPCHAR. DATE/TIMESTAMP equality preserves each operand and compares even a
finite DATE outside TIMESTAMP's range without overflowing a conversion.
Unsupported or ambiguous signatures have the operator SQLSTATE, English
diagnostic, Hint and original source position, rather than a routine fallback.

CASE takes a strongly named ELSE expression's label when available, otherwise
`case`; other labels are `coalesce`, `nullif`, `greatest`, `least`. Conditional
results have no physical column origin and conservative nullability metadata.
CASE/COALESCE/extrema retain a typmod only when every final input agrees;
omitted ELSE or unknown NULL normally loses it. NULLIF retains the first
operand's typmod unless signature coercion changes its type. Direct, Prepare
and repeated execution retain the same logical schema, independent of values
or nil parameters; argument Go types never choose the SQL signature.

For the conditional VARCHAR vectors, constrained Length is `(3,true)` on both
adapters. Unbounded VARCHAR instead exposes `(MaxInt64,true)` natively and
`(-5,true)` through actual lib/pq; TEXT remains `(MaxInt64,true)`. Tests assert
these descriptors independently, before normalization, including NULL outputs.


### Analysis, planning and runtime demand

All children are analyzed, input-decoded, dependency-tracked and authorized,
including runtime-dead arms. `CASE WHEN TRUE THEN 1 ELSE 'bad' END` still fails
integer input decoding during analysis. Prepare analyzes but does not run
sequences, import Go arguments or fold constant division beneath row-dependent
CASE. Execution planning folds truly immutable row/parameter/stage-independent
subtrees outside publication locks. Decisive FALSE/NULL CASE arms are discarded
without folding their results; TRUE drops later arms/ELSE. A constant nonnull
COALESCE argument drops following arguments.

Supplied INSERT VALUES, query sources and consumed defaults share the existing
unlocked planning phase. Context, existing consumer-specific relation identity
contracts and current authorization are revalidated before data capture or
staged publication. INSERT retains strict grant-time descriptor identity;
conditional planning does not broaden that admission contract.

Consequently `CASE WHEN TRUE THEN 1 ELSE 1/0 END` and `COALESCE(1,1/0)` succeed,
but `CASE WHEN v=0 THEN 0 ELSE 1/0 END` or `COALESCE(v,1/0)` can fail `22012`
during execution planning even for empty input or rows that would skip the
division. Native row-dependent division and parameter-dependent casts remain
lazy inside CASE/COALESCE: `CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END`
need not decode bad TEXT when only TRUE rows select the other result. The
parameter itself must still import according to its analyzed SQL type. This is
not demand parity: pinned PostgreSQL 18.0 can plan the immutable conversion of a
bound TEXT parameter before CASE/COALESCE runtime demand and report22P02 even
for the skipped branch. Tests retain the original id1v0 setup and consume the
successful `"7"`, id2 result after failure on the same held statement. Native
`NULLIF(NULL,10/v)` with v=0 reports22012; the exact PostgreSQL query instead
returns one SQL NULL of type INT4. These outcomes are independently asserted,
without forcing a generic plan or expanding native syntax.
Ordinary queries do not gain consumed-default constant AND/OR pruning; this
feature is not a general Boolean short-circuit or optimizer promise.
Consumed stored defaults alone retain their existing decisive FALSE-AND and
TRUE-OR policy through CASE/COALESCE nesting, without demanding the skipped
right child or replacing the stored definition.

Stored defaults preserve the original conditional structure and saved-test
form through Dump/Restore without Eval. CREATE and Dump do not demand dormant
runtime errors; only a consumed default is execution-planned. Explicit values
(including NULL) bypass their defaults. Retagged catalog descriptors rebuild
typed providers without changing old result snapshots. Restore traverses every
conditional child, rejects even a dead-arm parameter and publishes no partial
replacement. Multirow INSERT/UPDATE/DELETE failures likewise publish no partial
row mutation; sequence side effects retain their existing separate contract.

Permanent public witnesses:
`make check RUN='^TestConditionalSmoke$'`,
`make check RUN='^TestReferenceConditionalAggregateComposition$'`,
`make check RUN='^TestReferenceConditional.*$'`,
`make check RUN='^TestConditional'`,
and `make check REFERENCE=1 RUN='^TestReferenceConditional'` against the real
pinned PostgreSQL 18.0 fixture. These commands must be executed to establish
verification; expectations precede backend comparison.


## Captured temporal query environment

AT TIME ZONE and AT LOCAL are scalar temporal expressions, not a general function
registry or no-FROM SELECT expansion. TIMESTAMPTZ equality, order, DISTINCT,
grouping and extrema use instant identity; TIMETZ keys include its offset
tie-break, not modulo-24-hour equality. Civil DATE/TIMESTAMP compared with
TIMESTAMPTZ is interpreted in the captured session zone. Conditional/coercion
and comparison paths use this same environment, including prepared execution.
Typed temporal literals freeze at analysis; execution settings control parameters,
column conversions, AT LOCAL and output. Cursors retain captured presentation
after SET/Reset/Restore. See [types](TYPES.md#time-zone-aware-values) and
[settings](SETTINGS.md#temporal-settings) for bounded support and adapter differences.

Typed INTERVAL zone operands reuse the same temporal conversion body with a
fixed offset, without an interval-to-text conversion. Infinite intervals and
intervals containing months or days fail at execution with22023; definition
binding/deparsing does not evaluate these operands.

Ordinary stored views admit both forms, including column-valued zone operands
and nested conversions. The definition and analyzed temporal literals remain
immutable; `AT LOCAL` uses each execution's captured TimeZone, not the zone used
when the view was created. Sources and column operands retain their bound
identities across renames and same-name replacements. Native logical Dump/Restore
renders these expressions with explicit grouping and replays their definitions,
not previously computed results; restoration retains the target session settings.

Stored-view timezone regressions:
`make check RUN='^TestReferenceTimezoneReviewViewAt[A-Za-z0-9_]*$'` and
`make check REFERENCE=1 RUN='^TestReferenceTimezoneReviewViewAt[A-Za-z0-9_]*$'`.
The latter independently asserts results against the pinned PostgreSQL 18.0
fixture; native replay and catalog identity replacement are native controls.

## SELECT row locking — change note

Existing table/view SELECT and INSERT SELECT admit repeated adjacent locking
clauses with per-clause OF and NOWAIT/SKIP LOCKED. The entire list follows ORDER
BY and appears before or after the complete bounds group. OF resolves physical
relation/view occurrences separately from expression qualifiers: a relation's
own alias hides its original name; a whole-join alias does not hide child lock
targets. Naming JOIN/USING aliases rejects0A000; qualified targets reject42601,
unknown names42P01, with originating character positions. Duplicate OF targets
are idempotent. Default marks cover all applicable source occurrences, including
hidden contributors and zero-width outputs. FROM VALUES aliases are legal but
have no RowID, UPDATE authority or physical grant.

Stored OF targets retain their individual source occurrence through base renames
and native Dump/Restore. Deparsing registers only each occurrence's finalized
visible name: in `t CROSS JOIN t b`, alias `b` cannot hide the separate unaliased
target `t`. Qualified same-basename sources with distinct aliases remain separate,
and emitted definitions preserve selected contributors, strength and wait policy.

The query pipeline is FROM/join → WHERE → ORDER → lock/recheck → projection.
Everything is finalized before the adapter exposes Rows: Next/Close never acquires
grants or evaluates SQL. After a READ COMMITTED wait, only frozen contributing
RowIDs are followed to their current heads. Updated rows rebuild the same bound
join frame, including USING/NATURAL merged values and frozen view attribute
layouts, then recheck inner ON/WHERE/projection and outer ON/WHERE. Deleted or
nonqualifying combinations disappear without discovering new matches or
resorting current values. Ordinary unmarked queries carry no locking provenance.

View definitions may store a supported clause. Scalar locking waits until the
outer scalar qualification has selected contributors; it does not gratuitously
lock every row of a materialized view. Definition marks cover only their subtree;
unrelated sources stay unmarked. Overlapping strength and policy merge
independently: KEY SHARE < SHARE < NO KEY UPDATE < UPDATE and blocking < SKIP <
NOWAIT. A stronger SKIP clause plus a weaker NOWAIT clause therefore means
stronger NOWAIT, in either textual order. An ordinary upper
aggregate over a marked view is legal: it locks/rechecks the qualifying input
before grouping and aggregate Advance. HAVING FALSE or an outer filter excluding
aggregate output does not undo those input locks. Aggregate outputs and unmarked
aggregate siblings are frozen values, not physical row identities.
Grouping keys retain the enclosing statement's execution authority even when
locking or joins replace the physical input stage. Sequence-valued keys evaluate
once per consumed input occurrence, including duplicates, for both SELECT and
INSERT SELECT; their sequence effects survive transaction rollback.

All four clauses need SELECT privileges for actual reads and UPDATE on any live
column of every marked base source, using each view's current owner or original
security_invoker principal. Empty/filtered results still require authority and
ROW SHARE relation locks. Prepare is row-lock-free and does not check executor
UPDATE privileges; Execute refreshes candidates and authority. A clause reaching
GROUP BY, HAVING, aggregates or DISTINCT produces mode-specific0A000 using the
first actual clause mode. With locking, DISTINCT ON retains source/column/ORDER analysis;
its expressions must match the initial ORDER keys. Set-operation result or arm
marks reject0A000 before result arity/common typing; inline derived sets reject
enclosing row marks too. Stored set views have opaque copy-valued output, so an
outer FOR UPDATE may read them without manufacturing physical arm row identities.
UNION/INTERSECT/EXCEPT execute with the common-typed tuple semantics below;
ordinary DISTINCT retains source-typed projected-tuple materialization.
Nonlocking DISTINCT ON executes first-per-key selection, including shared
INSERT SELECT and persisted definitions, as described above.
Expanded GROUP BY/HAVING/aggregate view marks are checked at Execute after a
successful Prepare, matching PostgreSQL18 planning. Their grouping diagnostic
takes precedence over descending into a nullable outer-join input. Direct query
grouping/rowmark bind restrictions remain unchanged.
Sequences produce `42809`, `cannot lock rows in sequence "s"` at execution,
including unselected reference-mark siblings of marked queries. This kind
validation adds no UPDATE ACL, grant or latest refresh on an unselected sibling.
Ordinary sequence SELECT is unchanged. READ ONLY and REPEATABLE READ diagnostics
and the four-by-four matrix are documented in
[row locks](TRANSACTIONS.md#row-locks--change-note).
Missing analyzed FROM sources retain their identifier Position in `42P01`,
including OF-selected ROW SHARE admission as well as ordinary AS admission.
Relation-lock waits during direct analysis or Prepare retain the source-name
character Position for `55P03` and `40P01`, including ROW SHARE sources. Cached
execution, utility/row waits and `57014` cancellation remain unpositioned.

Native virtual catalogs (`pg_class`, `pg_rewrite`, `pg_views`) have no durable
physical tuple identity. Marked direct, nested, stored and empty reads fail0A000
`row locking is not supported for virtual catalog relations`, without partial
row grants or Position/Detail/Hint. Ordinary catalog reads remain supported;
PostgreSQL physical catalog lockers are separately labeled reference controls.

NOWAIT and SKIP use the existing manager's atomic immediate-grant branch, never
an external eligibility probe. NOWAIT reports55P03 with the physical base name;
SKIP drops the whole candidate as soon as a selected contributor is unavailable.
Earlier acquired contributors remain held. Neither option bypasses relation
waits or changes the original sort rank; no order is promised without ORDER BY.
Unselected siblings retain captured values during selected-row replacement.
Unavailable/deleted/requalified candidates spend no OFFSET/LIMIT quota.
Inherited marks enter a bounded view's own count frontier before survivor
consumption; plain scalar views still defer to enclosing qualification.
Native eager failure exposes no prefix/cursor. pq can expose an independently
expected acquired prefix before terminal Rows.Err: consume that error and close
rows/statements before rollback or other SQL on the same connection.
Outer row marks do not propagate through virtual CTE references. General derived SELECTs, cursors and adjacent excluded providers remain
excluded. There is one occurrence manifest and arbiter for direct/stored marks.
Smoke: `make check RUN='^TestReferenceQueryLockFeatureSmoke$'`;
pinned: `make check REFERENCE=1 RUN='^TestReferenceQueryLockFeatureSmoke$'`.

## Standalone and FROM VALUES — change note (changelog)

`VALUES (1,'a'),(1,'a'),(2,NULL)` is an executable query, including parenthesized
query forms. `SELECT v.* FROM (VALUES (1,'a'),(2,NULL)) AS v(n,s)` admits VALUES
as a non-lateral relation. The relation alias is optional; default output names
are `column1`, `column2`, etc. Positional column aliases may rename a prefix and
may repeat names; ordinary qualified/unqualified ambiguity rules still apply.
An unaliased source has no user-visible `unnamed_subquery` qualifier.
Parentheses may group the VALUES query before its alias or group a whole join;
they cannot wrap an already aliased VALUES operand alone.

Each column resolves its common type independently, left to right, before
execution or destination assignment. Unknown-only columns become TEXT. Existing
directional implicit casts, character typmods and default/C/POSIX collation
rules remain authoritative; VALUES does not infer types from an INSERT target.
Rows must have equal widths. DEFAULT and aggregate calls are not VALUES cells.
Cells cannot reference surrounding FROM operands or their own output columns.
VALUES entries and aliases still participate in ordinary missing-column spelling
suggestions despite having no physical catalog identity. Rejected references to
preceding physical or VALUES operands retain inaccessible-entry Detail and the
observed table/column LATERAL Hint; that diagnostic does not enable correlation
or LATERAL syntax.
Diagnostic Positions count characters, including multibyte comments, and full
raw-script parsing precedes effects from any valid prefix.

Duplicates and typed NULLs retain bag multiplicity. Local ORDER BY and
LIMIT/OFFSET/FETCH operate on VALUES before outer predicates, joins, grouping,
ordering or counts. Cells use existing scalar binding, coercion and execution
preflight; prepared statements rebind through the same parameter path. Synthetic
columns have computed metadata and no catalog identity, physical attribute
origin, ACL use or row-lock target. Mixed table/VALUES joins still capture and
authorize every real leaf through the existing statement-view path.

Standalone VALUES cannot have FOR UPDATE. SELECT over FROM VALUES accepts the
existing locking clause but locks no synthetic rows; mixed queries retain locks
on their OF-selected real leaves. Copy-valued OF targets acquire no tuple grants.
Physical row rechecks retain
the frozen synthetic occurrence rather than evaluating its cells again.
This is a consumer-visible native execution boundary, not PostgreSQL EPQ
expression-replay parity. Both refresh replaced physical rows and requalify
predicates. Native retains the locally selected synthetic occurrence and its
already-consumed effects; PostgreSQL may reevaluate volatile VALUES cells during
EPQ. On a later rejected execution PostgreSQL can also avoid that operand, while
native's existing join/view predicate boundary still demands it. Consequently
sequence-valued synthetic outputs and counters can differ independently of
physical replacement, duplicate multiplicity and predicate-rejection outcomes.

Stored views admit standalone and FROM VALUES, including local decorations and
mixed joins. Structural source-local literal paths remain separate from physical
source ordinals. Deparsed definitions execute with preserved labels, types and
bags; native Dump/Reset/Restore retains typed cells and computed outputs.
Restore still rejects query INSERT sources before publication, and failed
Restore leaves the prior engine intact. Eager results own metadata and exported
cells across subsequent Reset/Restore; public Next does not execute SQL.

Lateral derived queries and other FROM functions remain excluded. SELECT CTEs, SELECT and VALUES compose with
UNION/INTERSECT/EXCEPT; derived SELECT and admitted outer joins use the shared FROM
dispatcher. Destination assignment follows set-source common typing and dedup,
while ordinary INSERT VALUES/SELECT retain their existing assignment contexts.

Independent coverage: `make check REFERENCE=1 RUN='^TestReferenceQueryFromValues'`.


## Set operations — change note (changelog)

`UNION`, `INTERSECT` and `EXCEPT` combine SELECT or VALUES queries of matching
width with compatible corresponding types. Default and explicit `DISTINCT`
remove duplicate whole tuples; `ALL` retains multiplicity. With left/right tuple
counts m/n, UNION ALL emits m+n, INTERSECT ALL emits min(m,n), and EXCEPT ALL emits
max(m−n,0). DISTINCT emits one tuple for union presence, shared presence or
left-only presence, respectively. NULLs compare not-distinct for these operations;
every column participates, including the empty tuple in zero-target queries.
Unordered results are bags, never an append-order promise.

INTERSECT binds tighter than UNION/EXCEPT; UNION and EXCEPT associate left.
Parentheses override this and retain arm-local ORDER BY/LIMIT/OFFSET.
Unparenthesized trailing ORDER/bounds apply to the complete result. Set ORDER BY
accepts result names or ordinals, not arbitrary expressions/functions; use an
outer SELECT for those. Labels come from the leftmost arm, never a right-only
alias or input table namespace.

Each binary operation resolves corresponding types **pairwise**, before tuple
comparison. All-unknown SELECT pairs become TEXT, so
`SELECT NULL UNION SELECT NULL UNION SELECT 1` fails TEXT/integer matching;
`SELECT NULL UNION (SELECT NULL UNION SELECT 1)` yields INT4. VALUES resolves its
own columns first: `VALUES(NULL) UNION SELECT 1` is also a TEXT/integer mismatch.
INT4 mixed with REAL resolves REAL, not arithmetic's DOUBLE PRECISION promotion.
Same-domain operands can retain domain identity; mixed base/unknown inputs flatten
domains. Common typmods survive only when compatible. Synthetic output metadata
does not inherit a physical arm's NOT NULL promise.
Dropping a VARCHAR typmod means unbounded character length: native metadata
reports `(MaxInt64,true)` and actual lib/pq reports `(-5,true)`, including empty
results. Neither adapter's descriptor is normalized to the other's.

UNION ALL does not require tuple equality: JSON payloads are admitted there.
Every other set operation, including INTERSECT ALL/EXCEPT ALL and empty inputs,
requires the existing grouping/equality capability. No new type/collation provider
is added. UNION ALL may retain indeterminate implicit collation; a demanded ORDER
comparison then fails at execution, even empty, after successful Prepare.

Derived/scalar/EXISTS and correlated queries use the same set result. Scalar
cardinality errors occur at execution; INTERSECT/EXCEPT targets cannot be removed
just because EXISTS needs only presence. INSERT common typing and dedup precede
destination assignment: distinct NUMERIC1.1 and1.2 can insert two integer1s.
Assignment changes only the destination projection schema, not the common source
schema used by tuple validation, equality or local ORDER/bounds. BIGINT source
values outside the INTEGER destination range report22003 `integer out of range`
at assignment rather than an internal source-value validation error.
Late source/assignment failure publishes no native rows, successful count or
partial write batch. LIMIT0 avoids runtime targets but still analyzes/admit sources
and parameters from both arms. Public eager rows and metadata remain retained
through writes and native Reset/Restore.
Every pure INSERT VALUES source retains its existing restricted scalar/DEFAULT
cell grammar, including ORDER/bounds and nested parentheses. Query-only
expressions in genuine set-query VALUES operands do not widen that syntax.
Decorated DEFAULT cells remain syntactically admitted but report42601
`DEFAULT is not allowed in this context` during source binding, as before.
Locking-bearing VALUES wrappers remain intact through INSERT and derived-source
conversion: all four row-lock modes report0A000 `FOR <mode> cannot be applied to
VALUES`. Destination admission and VALUES cell analysis retain their diagnostic
precedence; a malformed trailing statement rejects the entire input before any
prefix write is published.

Stored views retain the binary tree, labels, typed literals, current renamed
source identities and every arm dependency. Dump emits replayable SQL; foreign
Restore reowns enum/domain/array grouping and leaves the prior catalog intact on
invalid or canceled replacement. Set result/arm and inline-derived row marks
remain prohibited; stored set views are opaque copy-valued lock-lineage barriers.
Inherited outer marks stop at the stored set boundary during source acquisition,
kind checks, authorization and source capture, not only during row production.
Thus readable catalog/sequence arms and SELECT-only base tables do not acquire
inherited row-lock or UPDATE requirements. Locally stored descendant marks still
apply, and every arm retains its used-column SELECT and dependency admission,
including empty or LIMIT0 reads. Ordinary non-set views remain transparent to
inherited marks; inline-derived set output still rejects enclosing marks.
TABLE and LATERAL remain outside this change; SELECT CTEs are documented below.

Independent public API coverage and smoke (both modes required):

```sh
make check RUN='^TestReferenceQuerySetOps'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOps'
make check RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQuerySetOpsFeatureSmoke$'
```

## Subqueries and derived SELECT — change note (changelog)

The ordinary query pipeline supports scalar `(SELECT ...)`, `EXISTS`, `IN` /
`NOT IN`, comparison `ANY` / `SOME` / `ALL`, and explicit `ROW(...)` or implicit
`(a,b)` comparisons with subquery output. FROM accepts derived SELECT with an
optional relation/column alias. No-FROM SELECT is a one-occurrence local input,
including nested queries; WHERE, grouping, HAVING, ordering, bounds and admitted
row marks use the same dispatcher. `SELECT count(*)` therefore returns1.

Scalar subqueries require one output column: no row gives typed NULL, one row
gives its value, and a second projected tuple raises21000. Producing that second
tuple can fail first, including projection validation or division by zero.
Single-row comparisons require matching width and the same cardinality rule.
Neither cardinality probe requests a third subplan output tuple; upstream
materialization can nevertheless evaluate further source rows before delivering
its first tuple. Ordinary unbounded sorting caches nonvolatile computed targets
once below the sort; target-only volatile expressions remain at final projection.
Existing bounded projection placement is unchanged. Quantified comparisons
evaluate the left operands once per demand, use typed comparison providers and
preserve SQL three-valued logic: ANY over empty input is false, ALL is true,
and NOT IN does not turn an unknown comparison into true. Ordered row comparison
stops at the first unequal field; equality can be false despite another NULL
field. Row syntax does not expose a general composite value API.

Every child binds completely, including unreachable/empty queries and unused
EXISTS targets. Eligible simple EXISTS ignores root targets, plain GROUP BY,
ordinary DISTINCT and ORDER work, including a positive or NULL constant LIMIT.
Actual local aggregate calls, HAVING, a represented OFFSET (even zero), rowmarks
or an unproven/nonpositive LIMIT retain ordinary tuple production and projection:
target errors can therefore remain observable, including while skipping OFFSET
rows. LIMIT 0 remains false without demanding a tuple. Binding and required
planning errors may still precede any surviving tuple; eligibility is not a
universal error or side-effect suppression guarantee. Required predicates and
derived projections are never stripped. Scalar and quantified plans retain
normal projection demand.
Each evaluation opens a fresh child stage and closes it on success, error or
cancellation; borrowed statement captures are owned by the enclosing execution.

Names resolve in the nearest visible lexical frame. Local ambiguity or a local
qualifier with a missing column cannot fall through to a grandparent. A derived
FROM item cannot see same-level siblings; lateral derived SELECT remains excluded,
but can see a legal enclosing query frame. Aggregate argument ownership follows
the nearest visible referenced frame, including references through sublinks;
child-local aggregates are not incorrectly treated as same-level nested calls.
Grouped outer references retain ordinary grouping diagnostics.

The shared source manifest admits every physical occurrence before execution,
including ignored targets; used-column ACLs, selected child row marks and a
coherent statement snapshot are preserved. UPDATE/DELETE predicates, UPDATE
assignments/RETURNING (including OLD/NEW aliases), INSERT sources and plain
VALUES expressions use the same child analyzer and demand path. Subqueries
remain prohibited in DEFAULT, CHECK, generated expressions and expression
indexes. LATERAL and function FROM are not added by this change.

Stored views retain child query frames, literals, column identities, source
occurrences and dependencies even for ignored EXISTS targets. Rename, deparse,
catalog/type remap and native Dump/Restore preserve those definitions rather
than cached result rows. Held executions rebind current authority and metadata;
completed eager cursors retain their original values.

Private rendered FROM bridges give duplicate positional columns distinct emitted
labels without changing the stored view's public output names or frozen slots.
Read-only rejection of a marked view uses the rewritten `SELECT` command,
including constant views; physical-table rowmarks retain `SELECT FOR ...`.

Shared reference coverage: `make check RUN='^TestReferenceQuerySubquery'`
and `make check REFERENCE=1 RUN='^TestReferenceQuerySubquery'`.
These are verification commands, not claims that a check has been run.


Nested JOIN execution planning uses the existing legality checker before cursor
publication. An ordinary scalar target is planned even when its parent's WHERE
is FALSE; an eligible EXISTS discarded target is not. A child's own physically
folded FALSE/NULL restriction can eliminate the unsupported FULL JOIN path
diagnostic, but does not erase structural nullable-side rowmark checks or change
the existing native conservative view-row-grant lifecycle. Derived FROM children
inherit parent row marks; expression subqueries inherit no parent mark and retain
their own selected marks.

The same Execute-time checker visits admitted UPDATE assignments/predicates,
DELETE predicates, supplied INSERT VALUES and UPDATE/DELETE RETURNING before
effects or cursor publication. A parent FALSE/empty input does not hide an
illegal scalar target or RETURNING child; Prepare still succeeds. Eligible EXISTS
discarded targets and a child's own FALSE retain their separate planning rules.
A physical non-NULL FALSE left operand of AND or TRUE left operand of OR omits
a query-containing right operand from planning and runtime demand. The original
graph remains authoritative for admission, dependencies, capture and ACLs;
ordinary frame-only coercion preflight still visits both peers. This bounded
query-demand rule does not enable general native Boolean/cast branch optimization,
parameter-dependent short-circuit promises or right-constant simplification.
An imported BOOL parameter used as a child's entire WHERE is checked in the
current execution frame for FALSE/NULL input elimination. Held executions retain
no such fact: changing false/true/false arguments replans the child's FULL legality
each time, without rewriting the analyzed predicate or skipping source grants.

Existing format, index-property and datetime-formatting arguments participate in
the same lexical aggregate ownership walk. Outer references below these providers
remain outer-owned through scalar subqueries, stored-view rendering and native
Dump/Restore; adding a no-FROM input does not add function providers.

## Selected windows — change note (changelog)

The selected providers are `row_number()` and unmodified `count(*)`, both INT8
(`int64` when scanned into any). Native metadata proves nonnull output, typmod-1,
and no relation/attribute origin. Aliases change only the output name. Source-column
metadata is unchanged, and hidden outer-order windows do not add visible columns.

```sql
SELECT id,
       row_number() OVER ordered AS rn,
       count(*) OVER (PARTITION BY p ORDER BY k) AS n
FROM t
WINDOW base AS (PARTITION BY p),
       ordered AS (base ORDER BY k,id)
ORDER BY id;
```

Numbering starts at1 separately for every partition. The default count frame is
RANGE UNBOUNDED PRECEDING through the current row's last peer: keys `(10,10,20)`
produce counts `(2,2,3)`, not `(1,2,3)`. Without window ORDER, every row in the
partition receives its full partition count. All order keys define peers; adding
an id tiebreaker changes frames. Equivalent specifications share their tie order;
assignment of row numbers within peers remains unspecified. Window sorting does
not promise final presentation: use an explicit outer ORDER.

Window keys are SQL99 input expressions, not projection labels or ordinal
references. `OVER (ORDER BY 1)` therefore counts the full partition at every row.
Typed partition equality groups NULLs together and uses existing numeric,
character/domain/enum/structural key contracts. ASC defaults NULLS LAST; DESC
defaults NULLS FIRST. NULL peer keys compare equal.

### ROWS offsets — change note

Explicit ROWS measures physical positions in each partition, not peer groups or
key distances. Window ORDER BY is optional; without a unique order, assignment
of counts to tied rows is unspecified. Equivalent window sorts retain the same
tie order, and only outer ORDER guarantees presentation.
`ROWS BETWEEN 1 PRECEDING AND CURRENT ROW` counts1,2,2 on three ordered rows;
the shorthand `ROWS 1 PRECEDING` is identical. CURRENT ROW, zero PRECEDING and
zero FOLLOWING select only the current physical row, including NULL/tied keys.
UNBOUNDED endpoints cover partition limits; finite endpoints are inclusive and
clip safely without overflow. `BETWEEN 1 PRECEDING AND 2 PRECEDING` and
`BETWEEN 2 FOLLOWING AND 1 FOLLOWING` are legal empty frames/count0.
Impossible bound-kind ordering retains42P20 with PostgreSQL English and carets.

Offsets use bigint assignment coercion: numeric1.5 rounds to2, unknown `'1'`
parses as bigint, but unknown `'1.5'` raises22P02 and typed text/bool raises42804.
Current-query variables raise42P10 after type coercion; aggregates/GROUPING and
nested windows remain forbidden. Independent scalar subqueries and outer-query
variables in inner frames use the established lexical scopes.
Start then end are evaluated once per demanded specification/scan, not per
row/partition. NULL raises22004 and negative raises22013 with starting/ending
English diagnostics, even for empty/all-NULL demanded input. LIMIT0,
row_number-only and unused frames remove runtime offset demand, not binding or
authority checks; count inside EXISTS remains demanded. Prepared arguments are
fresh on each execution, including recovery after failed bounds.

The established stage runs after WHERE/group/HAVING and before DISTINCT/outer
ORDER/LIMIT. ROWS coexists with RANGE/GROUPS while sharing equivalent sort work.
Counts are nonnull INT8 even for empty frames; empty input retains the full
cursor schema with zero rows. Source-column origins survive the overlay; computed
columns have typmod-1/no origin. Hidden keys, offset sources and unused definitions
require their real source privileges, including WHERE FALSE and LIMIT0.
Named frames retain volatile identity, and stored inline/named/domain/subquery
offsets survive rename, export/replay and actual pristine native Dump/Restore.
Failed/canceled writes or Restore publish no partial state; retained cursors keep
their values/metadata across Reset/Restore. No mutable offsets enter prepared plans.

Independent contract: `make check RUN='^TestReferenceQueryWindowRows'` and
`make check REFERENCE=1 RUN='^TestReferenceQueryWindowRows'`.
Focused smoke uses `RUN='^TestReferenceQueryWindowRowsFeatureSmoke$'` in both.

### GROUPS offsets — change note

Explicit GROUPS measures PRECEDING/FOLLOWING offsets in peer groups, not rows
or key distances. Every GROUPS clause requires effective window ORDER BY, even
CURRENT ROW, unbounded or unused named definitions. All ordering keys, directions
and NULL placement define peers; multiple keys and already-admitted text/bool/enum
ordering need no arithmetic RANGE support. Outer ORDER does not split window peers.
For keys1,1,2,3,3, `GROUPS 1 PRECEDING` counts2,2,3,3,3; CURRENT ROW counts2,2,1,2,2.
`BETWEEN 1 PRECEDING AND 1 PRECEDING` counts0,0,2,1,1, and following-only1
counts1,1,2,0,0. Partition boundaries clip the complete-group frame; reversed
same-category offsets can give an empty frame/count0. MaxInt64 offsets clip
without wrapping. The default remains RANGE, not GROUPS.

Offsets use bigint assignment coercion: unknown literals/parameters infer bigint,
numeric1.5 rounds to2, and typed text/bool is not a bigint offset. Independent
scalar subqueries and outer-query variables in inner-query frames are admitted;
current-query variables, aggregates, GROUPING and nested windows are forbidden.
Start analysis precedes end analysis, and type coercion precedes the variable
restriction. Missing ORDER reports42P20 `GROUPS mode requires an ORDER BY clause`.
Demanded count frames evaluate offsets once per specification/scan across partitions;
start NULL/negative checks precede end evaluation. NULL raises22004
`frame starting offset must not be null` (or `ending`); negative raises22013
`frame starting offset must not be negative` (or `ending`), even for empty/all-NULL
input. LIMIT0, row_number-only and unused frames eliminate runtime offset demand,
not analysis/authorization; an active count frame in EXISTS still demands offsets.

Bare OVER w reuses its named frame once. Distinct named definitions retain separate
volatile offset identity while sharing sorting. Inline/named/inherited/domain and
offset-subquery definitions survive hidden-key rename, output-alias collisions,
export/replay and actual pristine native Dump/Restore. Failed/canceled Restore
preserves the old catalog/cursors. Prepared executions import fresh offset arguments;
late errors/cancellation publish neither partial cursors nor INSERT prefixes.
`OVER(w)` cannot copy an explicitly framed parent. The omit-parentheses Hint
belongs only to that OVER clause, not to inheritance in a named WINDOW definition.
GROUPS does not admit exclusions or additional window providers.
Independent contract and smoke: `make check RUN='^TestReferenceQueryWindowGroups'`
and `make check REFERENCE=1 RUN='^TestReferenceQueryWindowGroups'`;
use `RUN='^TestReferenceQueryWindowGroupsFeatureSmoke$'` in both for focused smoke.

### RANGE offsets — change note

Explicit RANGE admits UNBOUNDED PRECEDING/FOLLOWING, CURRENT ROW and expression
offset PRECEDING/FOLLOWING, including BETWEEN bounds. An omitted end is CURRENT
ROW. Finite bounds are inclusive; CURRENT ROW covers the whole peer group.
Numerically reversed bounds can produce a nonnull zero count. Offset frames
require exactly one effective ORDER expression: repeated equivalent keys do not
add a key, and the first direction/NULL placement wins. For integer gaps1,3,5,
RANGE1PRECEDING counts1,1,1, unlike ROWS1PRECEDING's1,2,2.

Released B-tree in-range support is registered for existing SMALLINT/INTEGER/
BIGINT keys (integer offsets), REAL/DOUBLE PRECISION (FLOAT8 offsets), NUMERIC
(NUMERIC offsets), and DATE/TIMESTAMP/TIMESTAMPTZ/TIME/TIMETZ/INTERVAL
(INTERVAL offsets). Unknown literals and parameters receive the selected support
context; typed offsets use ordinary implicit coercion, never assignment casts.
Domains retain logical metadata and dependencies. An integer-domain offset can
be ambiguous among integer registrations; explicitly cast it to the intended
integer width. Sortable text, money, OID, enum, UUID and JSONB keys do not thereby
gain in-range support; existing unsortable-key boundaries still precede lookup.

Integer mathematical bounds do not overflow; numeric distances remain exact
and do not inherit key typmods. Timestamp months clamp to month end. Zoned
calendar arithmetic captures the Execute timezone (a day need not be24hours).
Clock bounds do not wrap at midnight; TIME/TIMETZ ignore offset months/days.
Temporal component overflow retains ordinary errors, not arbitrary saturation.
NULL keys form their full peer group for finite bounds under either direction
and either NULL placement. Mixed unbounded endpoints respect their sort placement.

Offsets cannot contain current-query variables, correlated variables, aggregates,
grouping operations or window calls; independent scalar subqueries are allowed.
Each demanded count frame evaluates start then end once per execution, across
partitions. NULL gives22004, including empty input. Negative/NaN offsets give
22013 only when an in-range predicate is demanded: empty/all-NULL inputs can
suppress that predicate. LIMIT0, row_number-only frames and unused definitions
do not demand offsets, but all definitions still undergo analysis/authorization.
An active count frame inside EXISTS remains demanded. Ordinary source tuple,
sorting and constant-expression failures can precede offset validation.

Bare OVER w uses an explicitly framed named window. OVER(w) cannot copy that
window; a frameless inherited order may receive a new frame. Inline/named/unused
offset literals and source dependencies survive symbolic view replay, rename and
native Dump/Restore. No per-execution offsets, permutations or output overlays
are stored in prepared plans or catalogs.

Different named definitions retain independent frame identity even when their
offset trees are structurally equal. Calls using the same named definition share
one offset evaluation; distinct names do not discard volatile side effects or
merge count outputs. Equivalent partition/order keys still share their sorting
and peer order. Named ordinals are query-local analyzed identity, copied by graph
rewrite/remap and rebound from symbolic definitions during Restore, not global
runtime IDs or persisted mutable state.


The stage follows WHERE, ordinary grouping/aggregation and HAVING, then precedes
DISTINCT, outer ORDER and LIMIT/OFFSET. A window count alone does not create a
grouped query. Grouped keys must be grouped columns or ordinary aggregate results.
Each set arm runs its own windows before the existing set operation. Scalar
composition, hidden outer-order calls and INSERT SELECT use the ordinary evaluator
and assignment/coercion stages. Bounds never truncate window frames.

`SELECT row_number() OVER (),count(*) OVER ()` returns one `(1,1)` through the
ordinary no-FROM singleton. WHERE FALSE and empty table input return zero rows
with complete metadata, not the ordinary aggregate count's singleton zero.

Targets can reference any named definition in their query. Definition-to-definition
inheritance permits only earlier names. An inherited specification cannot add
PARTITION, even if the parent has none; it may add ORDER only to an orderless
parent. `OVER name` and `OVER (name)` retain their written shapes. All definitions,
including unused ones, undergo column/type/permission analysis. Nested and sibling
queries have independent names and execution buffers.
Raw named references are checked at the call, but key expressions are transformed
after targets, WHERE, GROUP, HAVING and outer ORDER analysis; a bad key therefore
does not overtake an earlier expression diagnostic. Only resolved immutable specs
reach the executable stage.
Inline calls and named declarations retain independent key trees across later
calls and statements in the same parsed script.
The PostgreSQL keyword policy keeps OVER unreserved and WINDOW reserved; both
require AS (or quoting) when used as projection labels.
New contextual window/frame tokens retain their unreserved enum/domain type-name
and schema-prefix roles; reserved WINDOW does not become a generic type name.

Calls are allowed only in SELECT and outer ORDER. Illegal WHERE/group/HAVING/
FILTER/bounds/CHECK/DEFAULT/index/RETURNING/generation calls report42P20 at the
actual call; nested calls report42P20, and a window inside an ordinary aggregate
reports42803. Missing names report42704 at the bare name or specification opening
parenthesis. Missing OVER on row_number reports42809. Active windows with row
locking report0A000 without a caret; an unused valid WINDOW does not activate this
restriction. Physical source authority is required even for empty/no-key queries;
keys, hidden keys and unused definitions require their actual referenced columns.
Argument and FILTER children, then ordinary callable signatures, are analyzed
before window shape or clause restrictions for every already admitted aggregate:
count, row_number, avg, bool_and/bool_or/every, any_value, min/max, string_agg,
array_agg and sum. Shared ordinary signature/coercion helpers
retain argument limits, unknown ambiguity and polymorphic-type diagnostics without
registering ordinary aggregation or constructing another window provider.
Thus invalid callable signatures are not hidden by DISTINCT, WHERE or provider
exclusions, and a missing FILTER column still precedes signature resolution.
Primary function diagnostics retain the complete qualified callee and original
character-position owner.
The nearest same-query argument owner decides nesting only after its own callable
resolves. Thus `count(count(row_number() OVER ())) OVER ()` reports42803 at
`row_number`, while `count(count(row_number() OVER ()) OVER ())` reports42P20 there.
An invalid two-argument COUNT retains42883 even when an argument contains a window.
Scalar subqueries reset this ownership: their windows are not argument occurrences
in the parent query. FILTER and window-definition clause prohibitions retain their
own authority. Failed analysis neither registers these occurrences nor leaves
pending ownership for the next call.
These FILTER checks also precede the missing-OVER diagnostic for row_number.
Call-local aggregate ORDER BY is rejected as a window shape without resolving
its keys; it does not change ordinary aggregate ORDER BY analysis. Actual
OVER/WINDOW ordering keys are resolved in the window-definition phase.
Repeated OVER attachments are rejected for scalar and aggregate calls, using
the actual second keyword's spelling and character position, independently of
the specification's name/parenthesis caret. Malformed whole-input scripts do not
publish a parsed statement prefix.

Prepared execution uses fresh parameters/catalog authority and retains valid source identities. Window keys,
partitions, peer scans and provider work finish before cursor publication. Late
failure/cancellation publishes no partial cursor or INSERT batch; native cancellation
preserves ordinary context errors. Finalized cursors and metadata survive later
append/Reset/Restore. Stored view capture/export/remap and pristine Dump/Restore
persist input/key identities and definitions, never mutable window state.

Frame exclusions, other window providers, count(expr)
windows and window aggregate FILTER remain excluded. Native errors distinguish valid excluded
capabilities (0A000) from malformed syntax (42601). Ordinary aggregate modifiers
remain unchanged; this does not add public GRANT, types, joins or persistence formats.

Feature smoke and independent pinned PostgreSQL counterpart:

```sh
make check RUN='^TestReferenceQueryWindowFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceQueryWindowFeatureSmoke$'
```

## Grouping sets and GROUPING — change note

`GROUPING SETS` concatenates explicitly requested sets, `ROLLUP` generates
prefixes including the empty set, and `CUBE` generates subsets of its units.
Parenthesized sublists are indivisible units; nested sets flatten, and multiple
top-level grouping items form a Cartesian product. Repeated expressions inside
a set are redundant. An absent GROUP BY clause differs from explicit `()`: the
latter emits a group even on empty input and without ordinary aggregate calls.
Empty nonempty-key sets emit no rows but retain the complete output schema.

ROLLUP/CUBE argument lists contain ordinary expressions, not the general
grouping-item list. `ROLLUP(a,(b,v))` preserves the sublist as one unit; `ROLLUP(())`
and `CUBE(GROUPING SETS((a),()))` are42601 syntax errors. GROUPING SETS still
accepts recursive grouping constructs, including nested SETS and valid producers.
In `ROLLUP(cube(a))`, the inner unreserved name denotes an ordinary scalar call,
not a producer. PostgreSQL reports42883 for that missing function; native retains
the existing generic-call0A000 capability boundary, rather than rejecting the
name as illegal grouping syntax or admitting a new function.

Each set reads the same admitted FROM/WHERE input. Canonical keys and admitted
aggregate arguments/FILTER/order inputs evaluate once per source row, then feed
separate execution-local state for each set occurrence. A key absent from a set
becomes a typed NULL, including an omitted whole computed or ordinal-selected
constant expression. Placeholder NULLs are not merged with included source NULLs.
Default grouping and `GROUP BY ALL` retain duplicate sets; `GROUP BY DISTINCT`
removes duplicate sets before execution, not identical projected result rows.
Use `SELECT DISTINCT` separately when duplicate output rows should disappear.

```sql
SELECT a,b,GROUPING(a,b),count(*),sum(v)
FROM gs GROUP BY ROLLUP(a,b);
-- Equivalent levels: GROUPING SETS ((a,b),(a),()).
SELECT a,GROUPING(a),count(*)
FROM gs GROUP BY DISTINCT GROUPING SETS ((a),(a),());
```

`GROUPING(...)` returns nonnullable INT4 (Go int64 transport). Rightmost argument
is bit0: included0, omitted1. Thus a real NULL key can have bit0, while its
subtotal placeholder has bit1. Arguments are analyzed dependencies, never
evaluated by the operation; they must exactly match grouping expressions of the
associated query level. Ordinary GROUP BY produces mask0. There is no
functional-dependency or algebraic-equivalence exemption. PostgreSQL18 outer
Var references in scalar subqueries use their owning outer group; correlated
computed arguments retain42803 even when matching the outer computed key.

Typed supplied parameters can be whole keys or part of compound keys.
For three input rows, duplicate `($1::INT)` sets plus() give `(7,0,3)` twice and
`(NULL,1,3)` when supplied7. Supplying NULL instead gives `(NULL,0,3)` twice
and `(NULL,1,3)`: included NULL is still distinct from the omitted placeholder.
Reexecuting one held statement uses fresh supplied values and aggregate states;
output types and nullable-key/nonnullable-indicator metadata remain stable.
Parameter number is part of exact expression identity: `$2::INT` cannot be a
GROUPING argument for grouped `$1::INT` merely because both supplied values are7.
Parameterized grouped INSERT has the same occurrence counts and atomicity,
including omitted-key NOT NULL veto and same-owner recovery after failure.

HAVING and supported windows see group rows, including repeated occurrences;
hidden ORDER and window partition/order expressions can use GROUPING.
EXISTS preserves empty/multiple-set and operation-owning grouping stages:
EXISTS over empty input with GROUP BY () is true, while keyed-only grouping is
false; HAVING FALSE removes the empty-set row. Analysis errors still surface on
empty inputs and in discarded EXISTS targets.

GROUPING accepts at most31 arguments (32 gives54023). CUBE permits12 units
(13 gives54011 after child resolution). Raw expansion is capped at4096 before
DISTINCT reduction (54001); repeating keys does not bypass either guard.
Invalid grouping references and illegal clauses/nesting give full42803
diagnostics. Empty call/list syntax is42601, not an unsupported capability.
Grouping clauses, including explicit(), retain FOR-locking restrictions.

INSERT SELECT, CTAS/no-data, derived/scalar/set queries and stored views share
the grouped graph. Omitted base NOT NULL keys become nullable output metadata;
always-present keys retain their proof. GROUPING has no physical origin and no
private marker column leaks. Native eager late failures/cancellation publish no
cursor or partial INSERT; pq may expose a cursor before a late streaming error.
Views preserve actual grouping syntax, ordinals and dependencies through
rename/deparse replay and native Dump/Reset/Restore with destination-owned types.
Domain constraints, additional aggregates/window providers,
ROW/composite execution and optimizer/spill/parallel machinery remain unchanged.

Focused independent feature smoke (900 seconds each):

```sh
make check RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceGroupingSets(FeatureSmoke|ReviewFeatureSmoke)$'
```

## INSERT conflict omission — change note

`INSERT ... ON CONFLICT DO NOTHING` accepts the existing VALUES, DEFAULT VALUES
and SELECT producers, including their supported decorations. A target may be
omitted, specify `(column-or-expression [COLLATE collation] [opclass], ...)`
with an optional inference `WHERE`, or name `ON CONSTRAINT constraint`.
Targets infer every installed matching unique provider, independent of key
order and repeated occurrences. INCLUDE payload, key direction and NULL
ordering do not participate in equality; inference itself rejects direction
and NULL ordering decorations. Explicit classes match family and declared input,
not the broader binary-compatible CREATE admission set.

Partial inference proves immutable SQL TRUE implication using released conservative
Boolean composition, actual strictness and owned B-tree comparison rules.
Normalization preserves full TRUE/FALSE/NULL values beneath NOT, truth/null tests
and equality. Structural identical nested predicates can infer; mathematical
equivalence alone is insufficient. For example, `active IS TRUE` does not infer
bare `active`, and `NOT (active IS TRUE)` does not imply `active IS NOT NULL`.
This does not change SELECT's exact-conjunction matching or infer unrelated algebra.
The target predicate chooses providers; it does not filter proposed rows.
Native held statements specialize parameters afresh at each Execute.
PostgreSQL can instead switch to a generic parameter plan after custom plans;
that established backend difference is explicit in the reference family.

Defaults, assignments, identity, generated columns, NOT NULL and CHECK run
before conflict arbitration. Selected unique or supported nondeferrable exclusion
collisions omit only that proposal; unselected violations remain23505/23P01.
Accepted rows retain producer order and claims; skipped proposals retain no newly acquired key claim.
A later error, cancellation or required cursor Close error vetoes all staged
row effects, but already demanded sequence draws are not rolled back. Affected
rows count accepted proposals, not consumed source occurrences.

Read committed may omit a committed conflict invisible to the source snapshot.
Repeatable read and serializable reject a post-frontier committed conflicting
tuple with40001; own private/prefix proposals are exempt.
Selected stable-isolation visibility recovery uses an immutable physical position
checked against the simultaneously captured storage's RowID and VersionID; it
does not add a linear per-duplicate lookup to ordinary/read-committed enforcement.
Current privileges, definitions and installed providers are checked again after arbitration waits.
Ready-invalid providers participate in targetless arbitration; explicit
inference requires a valid provider. Native Dump/Reset/Restore retain supported
definitions and accepted data, but Restore input rejects conflict-bearing INSERTs.

INSERT RETURNING uses the shared output contract. Native
deferrable UNIQUE DDL retains its0A000 boundary. Omitted/named targets can select
supported nondeferrable equality exclusions, while unique inference cannot.
Selected deferrable exclusions report55000 with schema/table/constraint only
when a proposal is demanded; zero-row INSERT succeeds. See
[exclusion constraints](TABLES.md#exclusion-constraints--change-note).
The complete public contract and smoke use the same
`TestReferenceInsertConflict` definitions in native and pinned PostgreSQL modes.


## SELECT CTEs — change note (changelog)

Nonrecursive SELECT WITH queries define virtual relations for one statement:

```sql
WITH a(k, v) AS (SELECT id, value FROM t),
     b AS MATERIALIZED (SELECT k, v FROM a WHERE k = 2)
SELECT * FROM b;
```

Each definition sees earlier siblings and enclosing WITH definitions, not its
own pending or later local names. The primary query sees completed local
definitions. An outer definition or real table can satisfy a pending spelling;
schema qualification bypasses virtual names. Nested WITH shadows locally and
restores its enclosing environment afterward. Definition names are not ordinary
FROM entries until referenced. Existing table/column alias hiding, stars,
duplicate output slots, unknown-to-TEXT resolution and type metadata apply.
Prefix aliases may rename fewer than all columns; excess definition aliases
fail42P10. Duplicate local definitions fail42712 before body resolution. Missing,
ambiguous and alias-hidden references retain ordinary English diagnostics and
original one-based character positions; unresolved future WITH names add
PostgreSQL's forward-reference Detail and Hint.

### Folding and evaluation boundaries

An eligible side-effect-free single-reference SELECT folds by default.
Multiple references share unless NOT MATERIALIZED requests independent folding.
MATERIALIZED forces shared evaluation. VOLATILE expressions anywhere in the
analyzed definition, including otherwise-unused targets, nested projected queries
and unused nested WITH definitions, prevent folding even under NOT MATERIALIZED.
STABLE expressions do not. Static eligibility does not demand an unused producer:
an enclosing division can fail22012 while its unused nested sequence makes no draw.

Folding places safe occurrence-local restrictions before output projection;
it is not just reopening an already projected result. For t(id)=(0),(1),
`WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t)
SELECT v FROM c WHERE id<>0` returns10, while MATERIALIZED raises division by zero.
Eligible repeated aliases keep their own restrictions. Folding does not bypass
LIMIT/OFFSET survivors, aggregate-dependent predicates, required DISTINCT/set
evaluation, window input coordinates or outer-join NULL extension. Raw grouping,
DISTINCT and set keys can move safely; windows may filter completed window rows
without deleting input members.

### Shared demand, authority and lifetime

A shared producer opens only on first row demand and stores each typed row once.
Readers have independent replay positions and extend one append-only frontier.
Closing an early scalar reader does not discard rows or close a producer needed
by a later reader. Unused definitions and LIMIT0 do not evaluate their bodies;
LIMIT1 can stop after one draw, while blocking sort/count consumers may drain.
Zero-column rows and NULL/multiplicity retain their exact cardinality.

A proven FALSE or BOOL NULL consumer, including an imported BOOL parameter,
returns typed empty rows without demanding an opaque CTE body for any hint.
This optimization preserves native's existing eager physical settlement frontier:
an independently reachable stored marked aggregate or other consuming owner still
runs its original input/lock path, even when its output is rejected. PostgreSQL
constant gating may omit those physical grants; this is an intentional existing
native contract, not new CTE lock propagation. Physical demand inside an opaque
CTE does not by itself require opening that definition.

The producer belongs to the defining query invocation. Enclosing definitions
remain shared across child scalar readers; correlated local WITH invocations
receive independent producers. Every Execute is fresh. Failure/cancellation
sticks at the producer, closes its child once and prevents successful-prefix
publication. Public Rows still contain eagerly finalized values and immutable
schema, not a live producer or borrowed statement scope.

All definitions undergo static name/type/source/dependency analysis, even when
unused. Execution authority follows statically referenced subtrees: unused
definitions omit execution permissions, whereas a referenced LIMIT0 or WHERE
FALSE still checks source access. CTE scans have no ObjectRef, OID, storage or
independent snapshot/ACL authority. Outer row-lock clauses do not lock through
virtual CTE references; explicit locks inside the definition retain their own
query semantics.

SELECT subqueries, derived queries, set operands, views/materialized views,
CTAS, SELECT INTO, cursors and INSERT SELECT use this same graph. Stored WITH
definitions preserve real-object dependencies and executable replay through
rename and native Dump/Restore, never persistent CTE objects or cached results.
Recursive/SEARCH/CYCLE, modifying bodies, top-level WITH DML, standalone
VALUES/TABLE bodies and lateral derived queries remain excluded.

Coverage: `make check RUN='^TestReferenceQueryCTE'` and the same selector with
`REFERENCE=1`. Feature smoke: `RUN='^TestReferenceQueryCTEFeatureSmoke$'`.
See [the implementation design](design/PG18-QUERY-CTE.md).
## INSERT conflict update — change note

```sql
INSERT INTO destination AS dst VALUES(1,20),(2,30)
ON CONFLICT(id) DO UPDATE SET value=dst.value+excluded.value
WHERE dst.value<excluded.value;
```

The existing VALUES, DEFAULT VALUES and supported query-source producers compose
with this action, including their existing defaults, identity OVERRIDING,
generated values and source decorations. An explicit inference specification or
`ON CONSTRAINT` is required. Unique/PRIMARY KEY arbiter resolution uses the same
installed providers, partial-predicate proof and typed equality as DO NOTHING;
inference errors are not suppressed by empty sources or FALSE action predicates.
Named equality exclusions cannot be UPDATE arbiters, including empty sources and
deferrable constraints. Other unique/exclusion violations retain23505/23P01.

`excluded` contains the completed proposed row after destination assignments,
defaults, identity and generated values. Its implicit namespace is available only
in action RHS/WHERE, not in the INSERT source or arbiter inference. The target
alias is introduced with `AS` and hides its original qualified name in the action.
A target actually named `excluded` requires an alias to distinguish it from the
proposal. Unqualified names shared by both namespaces are ambiguous. SET
destinations are unqualified columns or existing array element/slice targets;
field/row assignments and other existing UPDATE exclusions are not widened.

Every RHS and destination bound uses one unchanged target/proposal frame.
Partial assignments compose into a private replacement without making earlier
SET results visible to later RHS expressions. DEFAULT, assignment coercions and
generated-column recomputation reuse UPDATE semantics. Supported scalar subqueries
retain the original statement source snapshot, including earlier transaction
writes but excluding this command's earlier inserted/replaced prefix.

Proposal NOT NULL/CHECK and demanded defaults precede arbitration; WHERE FALSE
does not suppress their failures or undo sequence draws. Once a live selected
conflict is acquired and rechecked, only TRUE executes runtime SET and counts an
update. FALSE/NULL keep the row lock and count0. A TRUE self-assignment counts1.
Planning immutable SQL constants still precedes filtering, so `SET value=1/0
WHERE FALSE` fails22012; row-dependent runtime expressions remain demand-owned.

Cardinality tracks actual affected row identity, not source ordinals or equal key
text. For two conflicts on one existing row, FALSE/FALSE counts0, FALSE/TRUE
counts1, while TRUE/FALSE and TRUE/TRUE fail21000. That check precedes the second
action's WHERE. A row inserted earlier in this command is already affected,
including a later hit with WHERE FALSE. Earlier transaction commands do not
consume this command's cardinality allowance. Key movement can leave the old key
free for a genuinely different newly inserted row.

Read committed, including READ UNCOMMITTED, can update a committed row invisible
to its captured source snapshot. Conflict-key waits and logical-row waits do not
widen or rerun that source. After a selected-row wait, the latest payload governs
WHERE/SET; moved/deleted rows restart admission of the completed proposal, not its
defaults or source. Repeatable read/serializable reject selected post-frontier
updates/deletions with40001 before evaluating a false/null predicate. Deletion
before any selected hit can instead leave a free key for a new row identity.
See [transaction and lock lifetime](TRANSACTIONS.md#insert-conflict-update).

INSERT rights on supplied destinations, UPDATE rights on action destinations,
SELECT on actually read target/proposal/arbiter columns and source SELECT are
distinct requirements, including empty/filtered commands. Native checks current
complete rights again after conflict waits. A revoke can therefore yield42501
and discard the whole mixed prefix, while pinned PostgreSQL's already admitted
command uses its startup ACL decision; subsequent commands reject on both.
See [authorization timing](AUTHORIZATION.md#table-and-column-grants--change-note).

One final publication installs ordered mixed inserts/replacements and their real
index/constraint effects. Late action, constraint, producer, required-Close or
cancellation failure installs none; explicit failed transactions require normal
rollback/savepoint recovery. Command Query returns no columns/rows, RowsAffected
sums inserts and TRUE updates, and LastInsertId remains unsupported. Held
statements rebind current definitions and rights. Dump persists ordinary objects,
rows and generator state, not action plans; supported native Reset/Restore retain
the existing lifecycle and conflict-bearing Restore rejection.

Native writable target views and virtual/physical partition hierarchy targets
remain unsupported. Hierarchy conflict UPDATE rejects0A000 after analysis and
rights, before provider/default/routing admission. Native deferrable UNIQUE/PRIMARY
KEY DDL remains bounded rather than simulated; INSERT RETURNING is supported. The shared
`TestReferenceInsertConflictUpdate` family has independently specified native and
pinned PostgreSQL expectations; [testing](TESTING.md#insert-conflict-update-acceptance)
separates public capture markers from actual native manager-pending proof.
