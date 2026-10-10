# Queries and data manipulation

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAttrdef' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAttrdef' VERBOSE=0`.
Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAttrdefControlsSmoke$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAttrdefControlsSmoke$' VERBOSE=0`.
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
their own0A000 `views must not contain SELECT INTO` boundary (native eager Prepare,
PostgreSQL utility Execute). Set producers use the existing set owner, including
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

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceDeparseConstraints' VERBOSE=0`.
Dual-mode deparse assertion unions:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDeparseConstraints[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeparseConstraints[A-Za-z0-9_]*$' VERBOSE=0`.
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
`FETCH FIRST | NEXT [count] ROW | ROWS ONLY`. Omitted FETCH count is1.
OFFSET and LIMIT/FETCH may appear in either order, after ORDER BY. One admitted
locking clause may precede or follow the count decoration.
WITH TIES and comma-style `LIMIT n,m` remain excluded.

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

INSERT retains common source typing, all-source ACL and destination validation
even at0, including existing destination constant/default simplification. A
constant default `1/0` therefore fails during execution preflight even with
LIMIT0; zero does not suppress an admission failure. Only selected destination
rows undergo runtime assignment/default/constraint work, and one complete batch
reaches WriteStage. Stored bounds persist typed
definitions/dependencies, survive renames and native Dump/Restore, and may
canonicalize FETCH to LIMIT and ALL to NULL. Held public exports remain stable
through later execution, Close and Reset/Restore.

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryLimit(Smoke|LockSmoke)$' VERBOSE=0`.
Independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryLimit' VERBOSE=0`.

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

Public feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryAliases' VERBOSE=0`.
Dual-mode alias union:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryAliases[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryAliases[A-Za-z0-9_]*$' VERBOSE=0`.
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
DISTINCT ON, grouping DISTINCT modifiers, ordinary array comparison
operators/ordering, general FROM expansion and spill/planner machinery remain
excluded.

Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryDistinctSmoke$$' VERBOSE=0`.
Independent pinned18.0 contract:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryDistinct' VERBOSE=0`.

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
independent PostgreSQL GRANT scenarios. ORDER USING, general COLLATE/ICU,
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

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryOrder' VERBOSE=0`.
View persistence smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryOrderView' VERBOSE=0`.
Independent pinned18.0 reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryOrder' VERBOSE=0`.

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
LATERAL joins, CTEs and FROM functions remain excluded; derived SELECT queries and
parenthesized VALUES are supported non-lateral sources. DELETE USING and UPDATE FROM
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
converted/constraint-checked batch. Held prepared execution rebinds names, rights,
NATURAL keys and data; changed result shape retains the cached-plan diagnostic.
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


Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|Joins)' VERBOSE=0`.
Independent pinned PostgreSQL behavior and full diagnostic coverage:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|Joins)' VERBOSE=0`.

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
Materialized-view DDL remains outside the native dialect.

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceDeparseRelations' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeparseRelations' VERBOSE=0
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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceTimezoneReviewView[A-Za-z0-9_]*$' VERBOSE=0`.
Independent pinned PostgreSQL18.0:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTimezoneReviewView[A-Za-z0-9_]*$' VERBOSE=0`.

Explicit SELECT target `AS label` and eligible bare labels supply frozen output
metadata and preserve view labels during definition reconstruction. CREATE
column lists override the labeled output prefix. ORDER resolves output labels
and ordinals; WHERE/HAVING do not expose output labels, and GROUP BY retains its
input-name priority and existing ordinal rules.

Persistent joined views freeze their source occurrences, expanded star projections
and USING/NATURAL keys when created, unlike an ordinary prepared SELECT rebind.
The shared definition/Dump renderer emits canonical per-occurrence aliases and
explicit ON predicates from those identities; original alias column lists and
merged namespaces are preserved logically, not as stale source text. Source
renames and unrelated attribute compaction preserve both self-join sides and
fixed output names/types. ON literals retain their original analyzed type/value.

Reconstruction, native replay atomicity and dynamic feature consumers share
mode-aware definitions. The ADD COLUMN/NATURAL-key freeze complement now runs
in both modes; host invalid/canceled Restore controls remain native-only:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceView(JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceView(JoinedDefinition|JoinedReplayAtomicityAndDynamicConsumers)$' VERBOSE=0
```

## Grouping and count — change note

Table SELECT and shared INSERT SELECT now accept scalar, comma-separated
`GROUP BY` keys and optional `HAVING` before ORDER BY.
`count(*)` counts every source occurrence, including NULL and zero-column rows.
`count(expression)` evaluates its ordinary scalar argument once per input row
and counts only non-NULL results: zero, false and empty strings count normally.
Both have label `count`, SQL type `INT8`, and Go value `int64`.
Without keys, aggregate calls or HAVING create one global group: an empty
source has count zero. Empty keyed grouping instead has no rows, but retains the
complete result schema. HAVING alone also creates a global group.

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
Existing native multi-statement and INSERT RETURNING parser boundaries remain
`0A000`, before aggregate execution or consumer/signature analysis. Carrier
eligibility itself does not add explicit casts to TEXT: raw aggregate output,
typed-column assignment and admitted casts retain their existing authority.
The separately supported [INTERVAL→TEXT output edge](DIALECT.md#date-and-time-arithmetic--change-note)
also consumes INTERVAL aggregate results, with captured IntervalStyle.

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceAnyValueSmoke$' VERBOSE=0`.
Full shared contracts: `make quickcheck PACKAGES=./reference RUN='^TestReferenceAnyValue' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAnyValue' VERBOSE=0`.
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
INSERT SELECT RETURNING remains the independent native0A000 suffix exclusion;
it does not reach the extrema reducer. Plain consumer methods keep actual cursor
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
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceExtremaSmoke$' VERBOSE=0`.
Complete dual-mode contracts: `make quickcheck PACKAGES=./reference RUN='^TestReferenceExtrema' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceExtrema' VERBOSE=0`.

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
In INSERT VALUES, string_agg gives42803 at the call. INSERT RETURNING is still
an independent native0A000 suffix exclusion; it does not reach aggregate binding.

OVER and array_agg remain excluded. FILTER/DISTINCT/ALL and sibling ordering use
the shared modifier contract. No-FROM aggregates use the ordinary local input. Unsupported XML ordering controls do not add XML or
cast edges. Public smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceStringAggSmoke$' VERBOSE=0`.
Shared contracts:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceStringAgg' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceStringAgg' VERBOSE=0`.

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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceIntervalAvg' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIntervalAvg' VERBOSE=0`.

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
still vetoes the entire aggregate result. INSERT RETURNING remains an independent
native syntax exclusion.

Numeric AVG contract and feature smoke (both independently asserted modes):
`make quickcheck PACKAGES=./reference RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$' VERBOSE=0`.
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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericSum' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericSum' VERBOSE=0`;
use `RUN='^TestReferenceNumericSumSmoke$'` in both for the focused smoke.


This does not admit
aggregates other than count/bool_and/bool_or/every/avg(interval or numeric inputs)/sum(numeric inputs)/any_value/string_agg/min/max,
DISTINCT/ALL grouping modifiers, other window providers, grouping sets/ROLLUP/CUBE/empty sets,
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
make quickcheck PACKAGES=./reference RUN='^TestReferenceAggregateModifiersFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAggregateModifiersFeatureSmoke$' VERBOSE=0
```


Feature checks (repository root, quiet output):

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryAggregate' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestQueryAggregate' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestQueryAggregate' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceBooleanAggregate' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestBooleanAggregate' VERBOSE=0
make quickcheck PACKAGES=./internal/parser RUN='^TestBooleanAggregate' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBooleanAggregate' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(BooleanAggregate[A-Za-z0-9_]*|AnyValueSourceACL)$' VERBOSE=0
make quickcheck PACKAGES=./internal/sqltype RUN='^TestGrouping' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryAggregate' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryAggregateSmokeMigration$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceCountExpression' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCountExpression' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceCountExpressionSmokeMigration$' VERBOSE=0
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
RowsAffected counts. No RETURNING is implied: direct/prepared Query of INSERT
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
Standalone and FROM VALUES use the same query-source binder and runtime described
below. WITH/CTE, RETURNING and ON CONFLICT remain outside this INSERT producer surface.
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
make quickcheck PACKAGES=./internal/parser RUN='^TestInsertSelect' VERBOSE=1
make quickcheck PACKAGES=./internal/engine RUN='^TestInsertSelect' VERBOSE=1
make quickcheck PACKAGES=./reference RUN='^TestReferenceInsertSelect[A-Za-z0-9_]*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertSelect[A-Za-z0-9_]*$' VERBOSE=0
make quickcheck PACKAGES=./smoke RUN='^TestInsertSelectSmoke$' VERBOSE=0
```

## DELETE rows

`DELETE FROM target` removes every live occurrence while keeping the table and
its schema. Optional WHERE uses the same typed comparison and three-valued
Boolean evaluator as SELECT: only TRUE selects, never FALSE or NULL. AS and bare
target aliases are supported; an alias hides the original target name. Predicate
columns may be unqualified or qualified by the visible target/alias. USING adds
query sources as described below; CURRENT OF, foreign keys and triggers remain excluded.
DELETE RETURNING is described below; UPDATE has its own bounded contract.
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
FULL-path legality restrictions remain; no CTE/LATERAL/function source is added.
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

Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0`;
pinned mode: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteUsingSmoke' VERBOSE=0`.


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
triggers/FKs/view writes and rewrite-to-UPDATE (where PostgreSQL NEW can be
nonnull) remain real PostgreSQL-only controls, not native engine mechanisms.
PostgreSQL18.0's excluded parenthesized whole-row expansion
`RETURNING (old).*,(new).*` can expand both through the target's ordinary OLD
namespace: the independent one-column id1 witness returns `(1,1)`, not
`(1,NULL)`. This record-expansion path is distinct from supported scalar
`new.id` and direct `new.*` (typed NULL), and from a rule rewriting DELETE to
UPDATE (where NEW is the actual update replacement).
Physical ctids/tuple error Context and PostgreSQL wire/C source fields are not
fabricated by native diagnostics.

Feature commands: `make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingMigratedReturning' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteReturning[A-Za-z0-9_]*$' VERBOSE=0`,
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteReturning[A-Za-z0-9_]*$' VERBOSE=0`.


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
Feature smokes: `make quickcheck PACKAGES=./reference RUN='^TestReferenceSerializableMigratedIsolation$' VERBOSE=1`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSerializableMigratedIsolation$' VERBOSE=1`.

INSERT query sources use that same live view, including earlier own deletions:
self-insert copies only captured survivors, never deleted history or its new batch.

Dump captures only committed survivors (including duplicate/zero-width rows), not
private deletion or tombstone history; canonical CREATE/INSERT restores those
survivors and supported metadata. DELETE scripts remain outside Restore's whitelist.
Deleted NULL history does not prevent SET NOT NULL, while live NULL and subsequent
NULL insertion retain genuine 23502 enforcement. DELETE cannot violate the only
currently supported constraint, NOT NULL. The real PostgreSQL foreign-key rejection
witness is explicitly oracle-only, not native FK support or a synthetic constraint hook.

Feature commands: `make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingMigratedCore' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteCore[A-Za-z0-9_]*$' VERBOSE=0`,
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteCore[A-Za-z0-9_]*$' VERBOSE=0`.
Integrated-expression smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceDeleteUsingMigratedExpressions' VERBOSE=0`.

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

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceNullCount' VERBOSE=0`.
Independent pinned PostgreSQL witnesses:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNullCount' VERBOSE=0`.

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
Run `make quickcheck PACKAGES=./smoke RUN='^TestQueryCoreSmoke$' VERBOSE=1` for direct/prepared
projection/metadata, bags, NULL/empty/zero-width output, ordering, sticky raw
cursor cancellation, byte ownership and retained native-control results.
Independent PostgreSQL witnesses are
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryCore' VERBOSE=1`; feature-local unordered
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
Native exact ACL controls remain internal, not a new SQL GRANT API.

Triggers and whole-row records/parenthesized expansion,
view writes/rewrite rules, partition movement and physical/C/wire tuple metadata
remain external PostgreSQL mechanisms. The reference suite exercises real trigger
transformation/suppression and whole-row controls, not native successful no-ops.
INSERT RETURNING, DEFAULT/row assignments/CURRENT OF/CTE/inheritance
remain excluded; no new evaluator, durability or retry path is introduced.

Feature commands:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)$' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateReturning[A-Za-z0-9_]*$' VERBOSE=0`,
`make quickcheck PACKAGES=./internal/engine RUN='^TestUpdateReturning' VERBOSE=0`,
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateReturning[A-Za-z0-9_]*$' VERBOSE=0`.

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
suggestions; no correlation or LATERAL syntax is enabled.

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
source is checked even with no matches. Prepared execution rebinds relation
identities, privileges, parameters and result signatures before mutation. Destination
casts, source ON, WHERE, SET and RETURNING share existing eager immutable preflight.
PostgreSQL may prune immutable demand or stream early Query rows before a terminal
error; native eager preflight and no-partial-output are deliberate boundaries.
Both backends preserve statement mutation atomicity; sequence draws are nonrollback.
Retained native output survives rollback and Reset/Restore. Persistence includes
committed live heads/catalog/sequence state, never source captures or output history.

LATERAL/CTE/function FROM, row assignments and DEFAULT,
CURRENT OF, whole-row records and INSERT RETURNING remain excluded.
No new SQL GRANT API, retry, sequence provider or optimizer is introduced.
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateFromSmoke$' VERBOSE=0`.
Quick: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateFrom' VERBOSE=0`.
Independent pinned PostgreSQL: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateFrom' VERBOSE=0`.

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
row assignment, CURRENT OF, DEFAULT or new casts/functions.
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

Mixed-DML witnesses: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$' VERBOSE=0`.
They cover direct/prepared duplicate-preserving composition and commit/rollback,
cross-mode latest-row counts and frozen candidates, mixed cancellation/resource
reacquisition, immutable candidate slots, all-relation publication, live-only
control/import behavior and identity exhaustion. Native internal waits observe
the actual manager; PostgreSQL waits observe transaction-ID locks and the exact
blocker. Public native effect/deadline tests do not claim queue-observation proof.

Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCoreMigratedSmoke' VERBOSE=0`.
Docker-free and independently pinned PostgreSQL witnesses:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$' VERBOSE=0`.
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

Public feature witness: `make quickcheck PACKAGES=./smoke RUN='^TestLogicalSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
Docker-free scalar scenarios:
`make quickcheck RUN='^TestReferenceLogical.*$' PACKAGES=./reference VERBOSE=1`.
Parser/engine units remain separate:
`make quickcheck RUN='^TestLogical.*$' PACKAGES='./internal/parser ./internal/engine' VERBOSE=1`.



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

The native fixed identity surface is bounded: all25 seeded type descriptors,
seven fixed collations and128 fixed conversions. Collation eligibility uses UTF8
encoding6/exact-before-fallback inside each namespace; conversion shadowing uses
name, not encoding pair. Inquiry-only identities do not expand comparison or
encoding conversion execution, DDL or system catalog query sources.
See [dialect details and boundaries](DIALECT.md#search-path-visibility-inquiries--change-note).
Public smoke: `make quickcheck PACKAGES=./smoke RUN='^TestVisibilityInquirySmoke$' VERBOSE=0`.
Real PostgreSQL18.0 evidence: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceVisibilityInquiry.*$' VERBOSE=0`.

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
Composition smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceArithmeticMigratedDeleteSmoke$' VERBOSE=0`.

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
metadata. Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceArithmeticMigratedInsertSourceSmoke$' VERBOSE=0`.

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
Composition smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceArithmeticMigratedCastSmoke$' VERBOSE=0`.

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
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceArithmeticMigratedSmoke$' VERBOSE=0`;
real pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(FloatingArithmetic|Arithmetic)' VERBOSE=0`.

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
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.


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

Run `go test -count=1 -v -run '^TestExpressionCoreSmoke$' ./smoke` for the public
direct/prepared scalar, BOOL/NULL, diagnostic, retained-cursor/control and atomicity
witness. Migrated scalar/query assertion unions use
`make quickcheck PACKAGES=./reference RUN='^TestReference(ExpressionCore|QueryCore)[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(ExpressionCore|QueryCore)[A-Za-z0-9_]*$' VERBOSE=0`;
local engine units remain `go test -count=1 -run '^TestExpression.*$' ./internal/engine`.
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
make quickcheck PACKAGES=./smoke RUN='^TestCompareNullSmoke$' VERBOSE=1
make quickcheck PACKAGES=./reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1
make quickcheck PACKAGES=./internal/engine RUN='^TestExpressionCompareNull.*$' VERBOSE=1
make quickcheck PACKAGES=./internal/parser VERBOSE=1
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1
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

Executable feature evidence: `make quickcheck PACKAGES=./smoke RUN='^TestBetweenSmoke$' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceBetween.*$' VERBOSE=0`,
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBetween' VERBOSE=0` against pinned18.0.

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

Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitSmoke$' VERBOSE=1`.
Review-regression smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitReviewSmoke$' VERBOSE=1`.
Parameter/ACL smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitPreflightSmoke$' VERBOSE=1`.
Pinned PostgreSQL coverage: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCastExplicit.*$'`.

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

`make quickcheck PACKAGES=./reference RUN='^(TestReferenceSmallintArithmeticSmoke|TestReferenceSmallintMigratedSmoke)$' VERBOSE=0`
exercises shared direct and genuinely held prepared smallint arithmetic,
independent width/bound/error contracts, metadata, NULL and migrated baseline
routes. The broader selector `^(TestReferenceSmallint.*|TestReferenceArithmeticBoundaries)$`
covers atomicity/cancellation, persistent defaults/views and native controls,
retained raw cursors, and authorization. Run both selectors with `REFERENCE=1`
as well for the pinned PostgreSQL18.0 oracle; execution establishes results.

`go test -count=1 -run '^TestTypeCoreSmoke$' ./smoke` exercises public
direct/prepared inference, codecs, exact diagnostics and phases, metadata, byte
ownership, multirow atomicity, cancellation and four-kind native round trips with
retained raw cursors. Go test reports the result.

Focused independent witnesses are `go test -run '^TestTypeCore.*$' -count=1 -v .`,
`go test ./internal/sqltype ./internal/engine`, and the pinned real-reference
`go test -tags reference -run '^TestReferenceTypeCore.*$' -count=1 -v ./reference`.
Dump uses descriptor-owned canonical SQL literals, and staged Restore uses the
same binder/codecs before replacing the catalog once; existing snapshots retain
their old values, types and column metadata.

## Omitted INSERT columns and defaults

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted columns use their declared default or typed NULL. Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Targetless DEFAULT VALUES requests one all-default row. Conversion, arity, default evaluation and NOT NULL failures publish no rows from the statement. The public feature smoke is `make quickcheck PACKAGES=./smoke RUN='^TestDefaultSmoke$' VERBOSE=1`.

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
`make quickcheck PACKAGES=./smoke RUN='^TestConditionalSmoke$' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReferenceConditionalAggregateComposition$' VERBOSE=0`,
`make quickcheck RUN='^TestReferenceConditional.*$' PACKAGES=./reference VERBOSE=0`,
`make quickcheck RUN='^TestConditional' PACKAGES='./internal/parser ./internal/sqltype ./internal/engine' VERBOSE=0`,
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceConditional' VERBOSE=0` against the real
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
`make quickcheck PACKAGES=./reference RUN='^TestReferenceTimezoneReviewViewAt[A-Za-z0-9_]*$' VERBOSE=0` and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTimezoneReviewViewAt[A-Za-z0-9_]*$' VERBOSE=0`.
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
Without locking, DISTINCT ON remains a parser capability boundary after complete
syntax validation, including shared INSERT SELECT and stored definitions.
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
CTEs, general derived SELECTs, cursors and adjacent excluded providers remain
excluded. There is one occurrence manifest and arbiter for direct/stored marks.
Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryLockFeatureSmoke$' VERBOSE=0`;
pinned: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryLockFeatureSmoke$' VERBOSE=0`.

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

LATERAL, CTEs and FROM functions remain excluded. SELECT and VALUES compose with
UNION/INTERSECT/EXCEPT; derived SELECT and admitted outer joins use the shared FROM
dispatcher. Destination assignment follows set-source common typing and dedup,
while ordinary INSERT VALUES/SELECT retain their existing assignment contexts.

Independent coverage: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryFromValues' VERBOSE=0`.


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
TABLE, CTE, LATERAL and DISTINCT ON remain outside this change.

Independent public API coverage and smoke (both modes required):

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOps' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOps' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySetOpsFeatureSmoke$' VERBOSE=0
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
FROM item cannot see same-level siblings without LATERAL (which remains excluded),
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
indexes. Stored-expression exclusions precede child analysis. CTE, LATERAL
and function FROM are not added by this change.

Stored views retain child query frames, literals, column identities, source
occurrences and dependencies even for ignored EXISTS targets. Rename, deparse,
catalog/type remap and native Dump/Restore preserve those definitions rather
than cached result rows. Held executions rebind current authority and metadata;
completed eager cursors retain their original values.

Private rendered FROM bridges give duplicate positional columns distinct emitted
labels without changing the stored view's public output names or frozen slots.
Read-only rejection of a marked view uses the rewritten `SELECT` command,
including constant views; physical-table rowmarks retain `SELECT FOR ...`.

Shared reference coverage: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQuerySubquery'`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuerySubquery'`.
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
count, row_number, avg, bool_and/bool_or/every, any_value, min/max, string_agg and
sum. Shared ordinary signature/coercion helpers
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

Prepared execution rebinds fresh parameters, catalog and authority. Window keys,
partitions, peer scans and provider work finish before cursor publication. Late
failure/cancellation publishes no partial cursor or INSERT batch; native cancellation
preserves ordinary context errors. Finalized cursors and metadata survive later
append/Reset/Restore. Stored view capture/export/remap and pristine Dump/Restore
persist input/key identities and definitions, never mutable window state.

Explicit frames/units/exclusions, other window providers, count(expr) windows and
window aggregate FILTER remain excluded. Native errors distinguish valid excluded
capabilities (0A000) from malformed syntax (42601). Ordinary aggregate modifiers
remain unchanged; this does not add public GRANT, types, joins or persistence formats.

Feature smoke and independent pinned PostgreSQL counterpart:

```sh
make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryWindowFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryWindowFeatureSmoke$' VERBOSE=0
```

