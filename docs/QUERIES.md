# Queries and data manipulation

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## LIMIT, OFFSET and FETCH — change note (changelog)

Table-backed SELECT, stored scalar view definitions, the shared INSERT SELECT
producer, decorated VALUES and already-admitted selected-call singleton SELECT
accept `LIMIT expression`, `LIMIT ALL`, `OFFSET expression [ROW | ROWS]`, and
`FETCH FIRST | NEXT [count] ROW | ROWS ONLY`. Omitted FETCH count is1.
OFFSET and LIMIT/FETCH may appear in either order, after ORDER BY. One admitted
locking clause may precede or follow the count decoration. Ordinary `SELECT 1`,
standalone VALUES, WITH TIES and comma-style `LIMIT n,m` remain excluded.

```sql
SELECT id FROM t ORDER BY id OFFSET 1 FETCH NEXT 2 ROWS ONLY;
INSERT INTO dst SELECT id FROM t ORDER BY id LIMIT 2 OFFSET 1;
INSERT INTO dst VALUES(7),(8),(9) OFFSET 1 LIMIT 1;
```

These return/insert occurrences2,3 and insert8, respectively. Unordered winners
and complete sort peers have no portable order. Bounds bind in the real input
namespace but cannot depend on rows or aggregates. Unknown parameters infer
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

Feature smoke: `make smoke RUN='^TestQueryLimit(Smoke|LockSmoke)$' VERBOSE=0`.
Independent oracle: `make reference RUN='^TestReferenceQueryLimit' VERBOSE=0`.

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
sets/windows/aggregate-internal ordering and ordinary no-FROM SELECT
remain excluded. The existing AND/OR bare-label ambiguity remains a bounded
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

Feature smoke: `make smoke RUN='^TestQueryOrderSmoke$' VERBOSE=0`.
View persistence smoke: `make smoke RUN='^TestQueryOrderViewSmoke$' VERBOSE=0`.
Independent pinned18.0 reference: `make reference RUN='^TestReferenceQueryOrder' VERBOSE=0`.

## Inner and cross joins — change note

SELECT and its shared INSERT SELECT producer accept comma-separated FROM items,
CROSS JOIN, JOIN/INNER JOIN with ON or USING, and NATURAL [INNER] JOIN. Explicit
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
names means a Cartesian product. Every key participates in equality, so NULL keys
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
types. Direct merged keys preserve the selected physical origin (which may be
the right input); converted merged keys have synthetic origin/typmod metadata.

ON uses only its two operands: `a CROSS JOIN b JOIN c ON a.k=c.k` can see a,
but `a,b JOIN c ON a.k=c.k` cannot. WHERE, targets, grouping/HAVING and the
ORDER expressions resolve the complete joined namespace.
Outer/LATERAL joins, derived queries, CTEs and FROM functions remain excluded;
UPDATE FROM and DELETE USING are not
enabled by query joins.

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

Feature smoke: `make smoke RUN='^TestQueryJoinsSmoke' VERBOSE=0`.
Independent pinned PostgreSQL behavior and full diagnostic coverage:
`make reference RUN='^TestReferenceQueryJoins' VERBOSE=0`.

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
`make test RUN='^TestTimezoneReviewView' VERBOSE=0`.
Independent pinned PostgreSQL18.0:
`make reference RUN='^TestReferenceTimezoneReviewViews' VERBOSE=0`.

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

Reconstruction/native feature consumer:
`make test PACKAGES=. RUN='^TestViewJoinedDefinitionAndNativeReplay$' VERBOSE=0`.
Independent PostgreSQL reconstruction, with ADD COLUMN explicitly PG-only:
`make reference RUN='^TestReferenceViewJoinedDefinition$' VERBOSE=0`.

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

```sql
CREATE TABLE grouped_rows(k TEXT, v INT);
INSERT INTO grouped_rows VALUES ('a',1),('a',NULL),(NULL,2);
SELECT k,count(*) FROM grouped_rows GROUP BY k;
SELECT k FROM grouped_rows GROUP BY k HAVING count(*)>1;
SELECT (v+1)*2,count(*) FROM grouped_rows GROUP BY v+1;
SELECT count(v),count(NULLIF(v,1)),count(*) FROM grouped_rows;
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

This does not admit standalone SELECT without FROM,
other aggregates, FILTER, DISTINCT/ALL aggregate or grouping modifiers, within-call ORDER,
OVER, grouping sets/ROLLUP/CUBE/empty sets, new scalar calls or
optimizer/spill/parallel machinery.
Aggregate occurrences anywhere in a standalone no-FROM projection remain excluded
even with a selected sequence/REGCLASS sibling; those aggregate-free singleton
exceptions and INSERT-only producer rules remain separate and unchanged.

Feature checks (repository root, quiet output):

```sh
make test PACKAGES=. RUN='^TestQueryAggregate' VERBOSE=0
make test PACKAGES=./internal/engine RUN='^TestQueryAggregate' VERBOSE=0
make test PACKAGES=./internal/parser RUN='^TestQueryAggregate' VERBOSE=0
make test PACKAGES=./internal/sqltype RUN='^TestGrouping' VERBOSE=0
make reference RUN='^TestReferenceQueryAggregate' VERBOSE=0
make smoke RUN='^TestQueryAggregateSmoke$' VERBOSE=0
make test PACKAGES=. RUN='^TestCountExpression' VERBOSE=0
make reference RUN='^TestReferenceCountExpression' VERBOSE=0
make smoke RUN='^TestCountExpressionSmoke$' VERBOSE=0
```

## INSERT from query — change note

INSERT now accepts SELECT sources through the common query pipeline, including
parenthesized sources and INSERT-only no-FROM projections such as `SELECT '1'`,
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

Plain `VALUES ('1')` and `(VALUES ('1'))` retain destination-context typing.
Adding the supported one-key ORDER BY makes VALUES a query source: each column
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
These VALUES/no-FROM forms do not add standalone VALUES or ordinary no-FROM SELECT.
WITH/CTE, general casts/functions, LIMIT, RETURNING and ON CONFLICT remain outside
this INSERT producer surface. Supported SELECT row-lock clauses are described in
[SELECT row locking](#select-row-locking--change-note).

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
make test PACKAGES=./internal/parser RUN='^TestInsertSelect' VERBOSE=1
make test PACKAGES=./internal/engine RUN='^TestInsertSelect' VERBOSE=1
make test PACKAGES=. RUN='^TestInsertSelect' VERBOSE=1
make reference RUN='^TestReferenceInsertSelect' VERBOSE=1
make smoke RUN='^TestInsertSelectSmoke$' VERBOSE=1
```

## DELETE rows

`DELETE FROM target` removes every live occurrence while keeping the table and
its schema. Optional WHERE uses the same typed comparison and three-valued
Boolean evaluator as SELECT: only TRUE selects, never FALSE or NULL. AS and bare
target aliases are supported; an alias hides the original target name. Predicate
columns may be unqualified or qualified by the visible target/alias. This does not
enable DELETE USING, CURRENT OF, foreign keys, triggers or additional constraints.
DELETE RETURNING is described below; UPDATE has its own bounded contract.
DELETE also reuses SELECT's nullness, Boolean truth and DISTINCT predicates,
including alias-qualified operands. Existing baseline explicit scalar casts work
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
kind, and cannot duplicate the visible target or another explicit alias.
Explicit aliases and the visible target mask default OLD/NEW names:
`DELETE FROM t AS new RETURNING new.*` reads OLD;
`WITH (NEW AS n)` additionally makes n.* NULL;
`WITH (OLD AS new)` makes new.* OLD and hides old;
`WITH (NEW AS old)` makes old.* NULL and hides new.
These table-only namespaces exist only in RETURNING, not WHERE. Unqualified
columns named old/new remain ordinary target columns; unqualified * expands
the target once. An actual target alias still hides the original relation name.

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
Whole-row OLD/NEW records, scalar subqueries, absent window/SRF providers,
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

Feature commands: `make smoke RUN='^TestDeleteReturning(Smoke|ControlsSmoke)$' VERBOSE=0`,
`make test PACKAGES=./internal/engine RUN='^TestDeleteReturning' VERBOSE=0`,
and `make reference RUN='^TestReferenceDeleteReturning' VERBOSE=0`.


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
Feature smoke: `make smoke RUN='^TestTransactionIsolationSmoke$$' VERBOSE=1`.

INSERT query sources use that same live view, including earlier own deletions:
self-insert copies only captured survivors, never deleted history or its new batch.

Dump captures only committed survivors (including duplicate/zero-width rows), not
private deletion or tombstone history; canonical CREATE/INSERT restores those
survivors and supported metadata. DELETE scripts remain outside Restore's whitelist.
Deleted NULL history does not prevent SET NOT NULL, while live NULL and subsequent
NULL insertion retain genuine 23502 enforcement. DELETE cannot violate the only
currently supported constraint, NOT NULL. The real PostgreSQL foreign-key rejection
witness is explicitly oracle-only, not native FK support or a synthetic constraint hook.

Feature commands: `make smoke RUN='^TestDeleteCoreSmoke$' VERBOSE=1`,
`make test PACKAGES=./internal/engine RUN='^TestDeleteCore' VERBOSE=1`,
and `make reference RUN='^TestReferenceDeleteCore' VERBOSE=1`.
Integrated-expression smoke:
`make smoke RUN='^TestDeleteCoreIntegratedExpressionsSmoke$' VERBOSE=1`.

### DELETE change log

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

Calls work in table SELECT projections and nested selected arguments. SELECT
without FROM is supported only when every target is rooted in a selected call,
with literal/parameter/nested-call arguments and no columns or extra clauses.
Former signed numeric literals remain valid arguments. Computed arithmetic does
not: table-backed calls fail native0A000 at the operator, while no-FROM calls
fail the existing `SELECT without FROM is unsupported` admission check at
SELECT (character1), before function binding.
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

Feature smoke: `make smoke RUN='^TestNullCountSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceNullCount' VERBOSE=1`.

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
Run `make smoke RUN='^TestQueryCoreSmoke$' VERBOSE=1` for direct/prepared
projection/metadata, bags, NULL/empty/zero-width output, ordering, sticky raw
cursor cancellation, byte ownership and retained native-control results.
Independent PostgreSQL witnesses are
`make reference RUN='^TestReferenceQueryCore' VERBOSE=1`; feature-local unordered
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
predecessor. Labels name outputs, not sibling inputs. This does not enable UPDATE
target aliases or qualifications in SET/WHERE.

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

Triggers, scalar subqueries, whole-row records/parenthesized expansion, FROM joins,
view writes/rewrite rules, partition movement and physical/C/wire tuple metadata
remain external PostgreSQL mechanisms. The reference suite exercises real trigger
transformation/suppression and whole-row controls, not native successful no-ops.
INSERT RETURNING, target aliases, DEFAULT/row assignments/CURRENT OF/CTE/inheritance
remain excluded; no new evaluator, durability or retry path is introduced.

Feature commands:
`make smoke RUN='^TestUpdateReturning(Smoke|ControlsSmoke)$' VERBOSE=0`,
`make test PACKAGES=. RUN='^TestUpdateReturning' VERBOSE=0`,
`make test PACKAGES=./internal/engine RUN='^TestUpdateReturning' VERBOSE=0`,
`make reference RUN='^TestReferenceUpdateReturning' VERBOSE=0`.

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
UPDATE FROM, target aliases, row/subquery assignment, CURRENT OF, DEFAULT
or new casts/functions. RETURNING has the shared output surface described above.

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

Mixed-DML witnesses: `make test PACKAGES='./internal/engine .' RUN='^TestUpdateCoreDelete' VERBOSE=1`
and `make reference RUN='^TestReferenceUpdateCoreDelete' VERBOSE=1`.
They cover direct/prepared duplicate-preserving composition and commit/rollback,
cross-mode latest-row counts and frozen candidates, mixed cancellation/resource
reacquisition, immutable candidate slots, all-relation publication, live-only
control/import behavior and identity exhaustion. Native internal waits observe
the actual manager; PostgreSQL waits observe transaction-ID locks and the exact
blocker. Public native effect/deadline tests do not claim queue-observation proof.

Feature smoke: `make smoke RUN='^TestUpdateCoreSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceUpdateCore' VERBOSE=1`.
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

Public feature witness: `make smoke RUN='^TestLogicalSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
Native/parser/engine units:
`make test RUN='^TestLogical.*$' PACKAGES='. ./internal/parser ./internal/engine' VERBOSE=1`.



Relation names accept one or two identifier components. Qualified `schema.name` resolves only in that schema; unqualified references follow the session's effective search_path, with implicit `pg_catalog` first unless explicitly positioned. Unqualified creation chooses the first usable explicit destination and requires CREATE there, without falling through on denial. Namespace and relation descriptors carry engine-local epoch-scoped identities, while rows remain stored by relation identity. Internal object references also retain a stable engine-owner token: coincident numeric identities in separate instances are never interchangeable. Reset/Restore preserve that owner while atomically replacing the catalog lifetime, and retained result snapshots remain usable. Built-in namespace identities are internal infrastructure, not SQL catalog tables.

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
Composition smoke: `make smoke RUN='^TestArithmeticDeleteCompositionSmoke$' VERBOSE=1`.

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
metadata. Smoke: `make smoke RUN='^TestArithmeticInsertSourceContextualSmoke$' VERBOSE=1`.

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
Composition smoke: `make smoke RUN='^TestArithmeticCastCompositionSmoke$' VERBOSE=1`.

SMALLINT/NUMERIC/floating and other noninteger arithmetic remain explicitly
unsupported, without changing their input/comparison/assignment capabilities.
Previously supported signed NUMERIC literal input, such as `+1.5`, remains a
literal identity operation, not a NUMERIC arithmetic provider.
Selected mathematical `mod` functions separately support SMALLINT, INTEGER,
BIGINT and exact NUMERIC remainders through the existing expression consumers;
this does not admit SMALLINT/NUMERIC arithmetic operators. Selected round/scale/
trunc/sign calls compose with existing projections, grouping by projection
ordinal, UPDATE and INSERT SELECT. They may wrap existing CASE scalar arms and
count-star aggregates, but do not add selected function CASE result leaves or
general GROUP BY call syntax.
See [the bounded signature note](DIALECT.md#scale-and-rounding-functions--change-note).
Unknown-only arithmetic fails `0A000`, `integer arithmetic requires known integer
or bigint operands`; unlike PostgreSQL it does not select FLOAT8 for unary unknown
`+` or emulate ambiguous full-provider overloads. Settings signatures, casts,
functions, aliases and general SELECT-without-FROM remain bounded as before.
Feature smoke: `make smoke RUN='^TestArithmeticSmoke$' VERBOSE=1`;
real pinned reference: `make reference RUN='^TestReferenceArithmetic' VERBOSE=1`.

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
arguments and singleton SELECT admission. No NUMERIC/float explicit casts,
BIT/VARBIT signatures or new no-FROM scalar consumer are implied.
Feature smoke: `make smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.


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
witness. Independent native/unit checks are
`go test -count=1 -v -run '^TestExpressionCore.*$' .` and
`go test -count=1 -v -run '^TestExpression.*$' ./internal/engine`;
the real pinned reference is
`go test -tags reference -count=1 -v -run '^TestReferenceExpressionCore.*$' ./reference`.
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
the bounded baseline and character-family sets, aliases,
row/composite tests and no-FROM scalar SELECT remain excluded except for the
selected null-count call roots described above. Those calls remain standalone
projection roots, not predicate operands; predicates remain excluded from their
arguments. ANY and null-test unknown occurrences share binding-order validation:
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
make smoke RUN='^TestCompareNullSmoke$' VERBOSE=1
make test PACKAGES=. RUN='^TestCompareNull.*$' VERBOSE=1
make test PACKAGES=./internal/engine RUN='^TestExpressionCompareNull.*$' VERBOSE=1
make test PACKAGES=./internal/parser VERBOSE=1
make reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1
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
value/endpoints. This does not broaden ordinary comparisons without a BETWEEN
predicate: `(v IS NULL)=TRUE` and `(id=1)=TRUE` remain outside the native dialect.
Native Dump/Restore preserves grouped comparison/logical/nested BETWEEN
defaults, including fresh DEFAULT inserts after replay. Predicate-valued
comparison operands serialize through the existing scalar identity
`CASE WHEN TRUE THEN ... END`; this preserves typed NULL and deferred errors
without evaluating defaults during serialization or admitting unrelated grouped
ordinary comparisons.

Executable feature evidence: `make smoke RUN='^TestBetweenSmoke$' VERBOSE=0`,
`make test PACKAGES=. RUN='^TestBetween' VERBOSE=0`,
and `make reference RUN='^TestReferenceBetween' VERBOSE=0` against pinned18.0.

## Explicit baseline casts

Baseline casts also serve as the existing comparison and null/Boolean predicate
operands, Boolean leaves, and selected null-count arguments. They do not admit logical/comparison input to
INSERT/settings or selected calls as Boolean/comparison leaves. The selected
null-count-only no-FROM form may use literal/parameter cast arguments; a standalone
cast still requires FROM.
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

Public feature smoke: `make smoke RUN='^TestCastExplicitSmoke$' VERBOSE=1`.
Review-regression smoke: `make smoke RUN='^TestCastExplicitReviewSmoke$' VERBOSE=1`.
Parameter/ACL smoke: `make smoke RUN='^TestCastExplicitPreflightSmoke$' VERBOSE=1`.
Pinned PostgreSQL coverage: `make reference RUN='^TestReferenceCastExplicit.*$'`.

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

`make smoke RUN='^TestSmallintSmoke$' VERBOSE=1` exercises the public direct and
genuinely prepared smallint path, bounds/English diagnostics, INT2 metadata,
NULL, atomicity/cancellation, mixed-width controls and authorization. Independent
focused witnesses are `make test RUN='^TestSmallint' PACKAGES='./internal/sqltype ./internal/parser ./internal/engine .' VERBOSE=1`
and `make reference RUN='^TestReferenceSmallint' VERBOSE=1` against the pinned
PostgreSQL 18.0 oracle. These commands require execution to establish results.

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

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted columns use their declared default or typed NULL. Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Targetless DEFAULT VALUES requests one all-default row. Conversion, arity, default evaluation and NOT NULL failures publish no rows from the statement. The public feature smoke is `make smoke RUN='^TestDefaultSmoke$' VERBOSE=1`.

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
This does not admit `count(expression)`, aggregate modifiers, or relationless
aggregate SELECT.

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
division. Row-dependent division and parameter-dependent casts remain lazy
inside CASE/COALESCE: `CASE WHEN b THEN 1 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END` need not decode
bad TEXT through the cast when only TRUE rows select the other result.
The parameter itself must still import according to its analyzed SQL type.
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
`make smoke RUN='^TestConditionalSmoke$' VERBOSE=0`,
`make smoke RUN='^TestConditionalAggregateSmoke$' VERBOSE=0`,
`make test RUN='^TestConditional' PACKAGES='. ./internal/parser ./internal/sqltype ./internal/engine' VERBOSE=0`,
and `make reference RUN='^TestReferenceConditional' VERBOSE=0` against the real
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

Ordinary stored views admit both forms, including column-valued zone operands
and nested conversions. The definition and analyzed temporal literals remain
immutable; `AT LOCAL` uses each execution's captured TimeZone, not the zone used
when the view was created. Sources and column operands retain their bound
identities across renames and same-name replacements. Native logical Dump/Restore
renders these expressions with explicit grouping and replays their definitions,
not previously computed results; restoration retains the target session settings.

Stored-view timezone regressions:
`make test RUN='^TestTimezoneReviewViewAt' VERBOSE=0` and
`make reference RUN='^TestReferenceTimezoneReviewViewAt$' VERBOSE=0`.
The latter independently asserts results against the pinned PostgreSQL 18.0
fixture; native replay and catalog identity replacement are native controls.

## SELECT row locking — change note

Existing table/view SELECT and INSERT SELECT sources admit one trailing
`FOR KEY SHARE`, `FOR SHARE`, `FOR NO KEY UPDATE` or `FOR UPDATE`, after ORDER BY
when present. Qualified/quoted sources, parameters, aliases and existing
inner/cross join forms retain their metadata and bag multiplicity. Unqualified
locking covers contributing physical base relations beneath scalar views,
including hidden contributors and zero-column outputs; no synthetic view RowIDs
are used.

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
unrelated sources stay unmarked. Overlapping marks on one occurrence use the
strongest SQL mode, KEY SHARE < SHARE < NO KEY UPDATE < UPDATE. An ordinary upper
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
GROUP BY, HAVING or aggregate output produces `0A000`, `<clause> is not allowed
with GROUP BY clause` / `HAVING clause` / `aggregate functions`, without Position.
Native Describe rejects an expanded grouped-view mark during Prepare; PostgreSQL
18 Describe does not plan that expansion and reports the same error at Bind/
execution. Tests retain independently specified phase evidence for this boundary.
Sequences produce `42809`, `cannot lock rows in sequence "s"` at execution;
ordinary sequence SELECT is unchanged. READ ONLY and REPEATABLE READ diagnostics
and the four-by-four matrix are documented in
[row locks](TRANSACTIONS.md#row-locks--change-note).
Relation-lock waits during direct analysis or Prepare retain the source-name
character Position for `55P03` and `40P01`, including ROW SHARE sources. Cached
execution, utility/row waits and `57014` cancellation remain unpositioned.

Native virtual catalogs (`pg_class`, `pg_rewrite`, `pg_views`) have no durable
physical tuple identity. Marked direct, nested, stored and empty reads fail0A000
`row locking is not supported for virtual catalog relations`, without partial
row grants or Position/Detail/Hint. Ordinary catalog reads remain supported;
PostgreSQL physical catalog lockers are separately labeled reference controls.

SELECT OF/NOWAIT/SKIP LOCKED/repeated clauses remain whole-input rejected with no
prefix effects. No outer/derived joins, keys/indexes, LIMIT/OFFSET,
set operations or additional locking subsystem are introduced.
Smoke: `make smoke RUN='^TestRowLock(Smoke|JoinSmoke|VersionSmoke|ViewSmoke)$' VERBOSE=0`;
reference: `make reference RUN='^TestReferenceRowLock' VERBOSE=0`.
