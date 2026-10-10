# Table definitions and object comments

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## Materialized views — change note

`CREATE MATERIALIZED VIEW saved AS SELECT i,label FROM source` creates an owned,
permanent stored result and an identity-bound definition. Later source writes
leave saved rows unchanged. WITH DATA/default preserves NULLs, duplicate
multiplicity and zero-column occurrences. An empty populated result remains
readable; WITH NO DATA is a distinct state whose demanded reads fail55000.
The definition's source/attribute/type dependencies survive stale data and source
renames. Ordinary views over a materialization read its stored rows, not its base.
Public INSERT/UPDATE/DELETE never populate or modify it.

Native Dump writes owner-bearing canonical CREATE MATERIALIZED VIEW ... WITH NO
DATA in dependency order, then captures exact stored rows at the dump frontier.
Definition SQL uses the existing identity-bound presentation renderer with
fully qualified names, preserving the restored public definition presentation
without adding synthetic source aliases. Ordinary view serialization is unchanged.
A versioned SQL line comment, `-- stuntgres-matview-v1 <base64>`, carries strict
JSON fields `schema`, `name`, `populated`, `rows`. The row text uses existing
canonical scalar codecs; it is converted privately by Restore, never executed
as public MV INSERT or used to reevaluate the defining query. Empty true/false
and zero-width row occurrences remain distinct. Ordinary SQL ignores the record.

Restore recognizes only genuine line comments, not marker text in string values,
identifiers or nested block comments. It rejects malformed/duplicate records,
wrong targets, invalid shape/type/typmod or effectful row expressions, validating
in an isolated candidate before one catalog replacement. Read, parse, context
and late SQL failures leave the destination catalog/data/retained rows unchanged.
Native second-cycle/fresh-driver Restore preserves stale data, current names,
owners, typed references and population; caller-owned TEMP objects retain their
existing replacement contract.
Snapshot input and every implicit, explicit, nested and assignment coercion must
preserve the encoded datum. Modifier rounding, truncation or padding is rejected,
including domain bases and array elements and literals folded during binding.
Canonical domain/enum/array, temporal and symbolic-reference codecs remain valid.
This import-only rule does not change ordinary SQL's cast or assignment semantics.

PostgreSQL ignores these comments. Replaying the actual dump script there
restores schema and frozen definitions with all MVs unpopulated, not native stale
rows. There is no public DML bypass, REFRESH or reconstruction from current base
data. See [syntax and exclusions](SQL.md#materialized-views--change-note).

## CREATE-time inheritance — change note

`CREATE TABLE child (local definitions) INHERITS (parent, ...)` supports ordered
one/multiple plain-table parents, including schema-qualified and quoted names,
empty local lists and session-owned TEMP parents/children. IF NOT EXISTS skips
parent resolution when the destination already exists. Malformed lists, repeated
clauses and extra tails reject the complete input without prefix effects.

Inherited live columns precede local-only columns. Same-name columns merge only
with identical type identity, typmod and collation. Explicit local redeclarations
retain their inherited position. Missing defaults are neutral; specified parent
defaults must have equal cooked trees unless a local DEFAULT (including NULL)
resolves the conflict. CREATE and Dump never evaluate definitions.
LIKE defaults are copied after this conflict check: INCLUDING DEFAULTS can replace
a nonconflicting inherited default, but cannot resolve conflicting parent defaults.
An explicit same-name column beside LIKE still rejects42701 as a duplicate.

Inheritable CHECKs remap stable parent attributes to the child row. Equal
same-name definitions merge; unequal expressions reject. NO INHERIT CHECKs stay
parent-local. NOT NULL is OR by column, not by parent constraint name, and local
NULL cannot relax it. Constraints receive fresh child identities. Existing STORED
generated expressions inherit with row remapping; identity ownership does not,
but its NOT NULL does. SERIAL inherits the original sequence default without
child ownership. UNIQUE, ordinary indexes, PK and FK do not inherit. Existing
native PRIMARY KEY support supplies inherited NOT NULL, not a child key;
foreign-key and foreign-table provider admission retain their independent boundaries.
Generated/base parent kinds conflict. A local ordinary redeclaration retains an
inherited generated expression; an explicit local generation expression may
override it or resolve conflicting parent expressions. A local DEFAULT on an
inherited generated column, or local generation on an inherited base column,
rejects42611 with the PostgreSQL English primary/hint.
INCLUDING GENERATED likewise cannot resolve conflicting inherited generation
expressions. Copying a LIKE generation expression over an inherited generated
column rejects2BP01 with PostgreSQL's default-dependency diagnostic; no child is
published. Plain generated INHERITS and explicit child generation overrides remain
supported.

Direct child-to-parent normal dependencies are visible in pg_depend. Diamond and
direct-plus-indirect ancestry are valid; repeated resolved immediate parents are
not. Leaf DROP retains parents; RESTRICT protects unrequested descendants and
CASCADE removes the descendant closure with ordinary dependent objects. Whole
table RENAME/SET SCHEMA preserve graph identities. TEMP cleanup/ON COMMIT DROP
uses the same closure; ON COMMIT DELETE ROWS clears only the named physical table.

Recursive parent scans, nested/stored source scans, UPDATE/DELETE targets and
ordinary LOCK on a parent with children reject0A000
`recursive table inheritance is unsupported`. Physical INSERT and leaf DML work;
LOCK ONLY is physical. Graph column/default/type/nullability/constraint ALTER
rejects0A000 `ALTER TABLE inheritance operations are unsupported`, except inherited
column rename/drop/type and inherited NOT NULL removal use PostgreSQL42P16.
Dependency-driven column, DEFAULT or CHECK removal on a surviving graph
participant has the same0A000 ALTER boundary. Survival uses the effective staged
graph after complete whole-relation retirement is staged, including schema-contained
parents, their outside descendants, and shared children discovered through another
retiring parent. DROP SCHEMA CASCADE cannot remove
domain-dependent columns or definitions while an inheritance relationship
survives: rejection preserves the provider schema/domain, tables, rows and graph.
If removal of the sole child leaves its parent standalone, ordinary provider
projection/removal remains supported and preserves independent physical rows and
definitions. A retiring parent can also be the regclass provider of a retained
other parent's DEFAULT or CHECK: CASCADE retires their shared child before pruning
that newly standalone owner's dependent definition. Independent definitions and
physical rows remain. Whole-table graph closure removal also remains supported.
ALTER INHERIT/NO INHERIT, recursive execution, propagation and partitions are not added.

Dump topologically emits ordered INHERITS and only local declarations, preserving
explicit DEFAULT NULL, equal local CHECK/NN merges and actual data. A plain local
redeclaration of an inherited generated column remains plain in the stream;
only an actual local generation override is emitted as GENERATED. Restore imports
the real graph atomically, including relation and constraint-origin identity
remapping beside unrelated live TEMP objects. TEMP-to-TEMP graphs retain their
exact relation, constraint and storage identities.
Imported descriptors yield to retained TEMP definition OIDs as well as descriptor
OIDs, including child-local EXCLUDE constraints and their exact backing indexes.
The retained graph remains usable alongside the imported graph and subsequent
ordinary CREATE/INSERT operations.
Reset/Restore with any live TEMP-to-persistent parent edge reject the ordinary control error
`stuntgres: catalog replacement with live temporary table inheritance of persistent parents is unsupported`
before replacement; drop/close the TEMP owner first. These controls are not pg_dump
or filesystem/wire durability APIs.

## Query-derived tables — change note

```sql
CREATE TABLE copy (key) AS SELECT i, label FROM source;
CREATE TABLE empty_copy AS SELECT i, label FROM source WITH NO DATA;
SELECT i, label INTO copied_again FROM source;
```

These create ordinary independent tables, not views or stored query definitions.
Default population and `WITH DATA` store the complete query result, preserving
duplicate multiplicities and zero-column occurrences. A supplied column-name list
renames an output prefix; its remaining labels come from the analyzed query.
Too many names fail42601, duplicate normalized/clipped labels fail42701, and
reserved system labels fail42701. Pseudo-types and indeterminate collations cannot
be stored. Diagnostics belong to destination execution, not returned row metadata.

Column Usage retains resolved type identity, typmod and collation, including
existing domains, enums, arrays and concrete catalog output types. New attributes
and origin metadata belong to the destination; stored columns are nullable.
Source table NOT NULL/default/generated/identity/CHECK/UNIQUE/PRIMARY KEY constraints,
indexes, comments and ACLs are not copied. A domain retains its own existing type semantics;
this feature does not add native domain constraints or defaults.

The effective creator owns the destination. Initial population requires schema
CREATE and the query's ordinary executor source privileges, not destination
INSERT. Successful creation publishes the definition and all initial rows at one
catalog/storage frontier; query, cleanup, cancellation or publication failure
leaves no partial destination. Sequence effects retain ordinary nonrollback
semantics. Dropping a source does not remove or change the copied table.

SELECT and ordinary writes/drop operate on the resulting table. Native Dump emits
ordinary definitions and literal rows (DEFAULT VALUES for zero-column rows),
and Restore remains restricted to its existing replay whitelist, not CTAS/INTO.
Isolated Restore column reconstruction can recover builtin internal CHAR,
REGCLASS/its existing arrays and scalar XID already stored by a query. XID retains
its canonical unsigned scalar codec and nullable shape, without admitting raw XID
declarations, casts, domains or arrays. Other ordinary raw declaration/cast/domain
admission is not widened. Named REGCLASS references use
the existing codec/replacement mapping, not foreign numeric relation identities.
Generated nonnull REGCLASS row literals use a name-only `CAST('name' AS REGCLASS)`
and the existing binder resolves the restored identity. Restore admits only that
literal child, not parameter, column, function, query or effectful reference
expressions; stored NULL follows the ordinary NULL literal path.

PG_NODE_TREE remains a nonphysical structural catalog carrier, not ordinary
stored SQL data. Native query-table descriptor admission rejects it with `0A000`
`query table output type pg_node_tree is unsupported`, including DATA, NO DATA and
existing domain/array physical wrappers. An existing-target IF NOT EXISTS skip
still precedes this descriptor check. Its SELECT/catalog/deparser capabilities
remain unchanged; PostgreSQL can materialize its physical node serialization.

## Temporary tables — change note

`CREATE [TEMP | TEMPORARY] TABLE [IF NOT EXISTS] name (...)` uses the ordinary
ordered table elements, declared types, NULL/NOT NULL, DEFAULT, CHECK, stored
generation, serial/identity, UNIQUE, PRIMARY KEY and plain LIKE construction path. TEMP
creation ignores the default persistent creation namespace. Ordinary
`CREATE TABLE pg_temp.name`, an actual own TEMP namespace, or a leading
`search_path=pg_temp` also creates an owner-TEMP relation. Qualified persistent
access bypasses owner shadowing; explicit `public,pg_temp` path order is honored.
Lookup alone does not allocate a namespace and peers never share TEMP data.

Trailing `ON COMMIT PRESERVE ROWS` is the default; `DELETE ROWS` prepares empty
storage/deletion/index roots at successful outer commit while preserving
serial/identity counters; `DROP` removes table, owned sequences and backing/plain/
expression indexes through the ordinary dependency closure. Autocommit counts:
CREATE DROP succeeds then the table is absent; INSERT DELETE reports its actual
affected count then the table is empty. Explicit SQL/driver commit and CHAIN
apply actions once, not at intermediate statements. Rollback/savepoint rollback
restores descriptor/action/data roots without rolling back nextval draws.

Owned named IDENTITY sequences using `pg_temp` and same-stage SERIAL-dependent
DEFAULTs resolve against the table builder's exact owner namespace before CREATE
publication; this grants no generic persistent sequence alias authority.

TEMP persists through pooled borrowing, but physical-session end removes all
owned dependencies and aborts private work. Explicit DROP, ON COMMIT DROP and
physical retirement use the same complete expression dependency closure:
dependent DEFAULT/CHECK definitions, generated columns and external expression
indexes are removed while their surviving consumers remain. Removing a generated
column projects committed rows and pending writes/deletes by stable attribute
identity, including nested private DDL and savepoint restoration; an ON COMMIT
DELETE consumer stays empty after this projection. Retained immutable snapshots
keep their old shape. Unrelated permanent objects and peer TEMP remain unchanged;
permanent DEFAULTs retain their existing TEMP-provider exclusion.
Surviving row shapes and affected index roots publish with the catalog at one
frontier; physical retirement retries obsolete predecessors rather than replacing
newer rows or index roots.
If table and owned-sequence discovery reshape the same survivor more than once,
each expression index evolves from its current immutable staged candidate to the
latest stable-attribute layout. Already coherent candidates remain unchanged;
earlier derived trees and original publication predecessors remain retained.
Dump excludes TEMP definitions/data;
native Reset/Restore preserves exact live owner descriptors, stores, generators,
index roots/actions and retained builtin types. Stored DEFAULT/CHECK/generated
and derived-index references retain their immutable manifests under bounded
owner-TEMP provenance, including live same-owner table and index providers.
References retain their original engine, physical namespace token, nonreused
address and epoch; rendering still requires exact concrete types and row origins.
Builtin retention walks both definition and execution trees of derived index keys
and partial predicates, including a predicate that alone carries BOOL. Replacement
does not remap those retained trees, their concrete type generations or manifests.
This does not grant sequence runtime authority to tables/indexes, forgive
foreign-owner or retired/recreated providers, or admit stale persistent consumers.
Stale active transactions remain fenced by replacement; incompatible live
user-domain-array dependencies retain their explicit ordinary-error boundary.
Full index deparse uses `pg_temp` for the current physical owner's TEMP namespace,
identified by captured namespace authority rather than name matching. Other
owners retain their physical namespace spelling; context-free Dump stays canonical.
CASCADE also traverses actual owned-index provider roots, including already-staged
index removals, before final validation of their consumers. Independent own-row
generated columns retain their definition identities and remap to the surviving
dense layout; missing or retired providers never gain reference authority.

Explicit ON COMMIT on a permanent destination fails42P16. TEMP in a persistent
schema and creation in a foreign TEMP namespace fail42P16 after CREATE authority.
IF NOT EXISTS does not bypass destination/permission/persistence checks or replace
an incumbent's shape/data/action. Malformed/trailing clauses fail42601.
DELETE/PRESERVE policies require the `ROWS` suffix; window frame-unit keywords
`RANGE` and `GROUPS` are not alternative commit-policy units.
Explicit identity LOGGED/UNLOGGED options on TEMP fail42P16
`cannot set logged status of a temporary sequence` at the option.
TEMP source SET SCHEMA is excluded with0A000
`cannot move objects into or out of temporary schemas`.
TEMP views (including a TEMP-source view), GLOBAL/LOCAL spellings, nested TEMP
CREATE SCHEMA elements and permanent transactional CREATE/DROP remain excluded.
CREATE/DROP TABLE driver Prepare is metadata-only, including inside a transaction:
it creates/resolves no table or TEMP namespace and performs no read-only or
destination rejection. Execute applies those checks and still excludes permanent
transactional CREATE/DROP without effects.

Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceTemporaryTableFeatureSmoke$' VERBOSE=0`;
pinned: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTemporaryTableFeatureSmoke$' VERBOSE=0`.

## Decompiled definitions — change note

CHECK/default/NOT NULL definitions have generated catalog identities distinct from
their owning table and stable attributes. CREATE allocates only at execution;
renames and surviving DROP-column remaps preserve IDs. SET DEFAULT replaces its
definition with a fresh identity, DROP removes it, and re-add creates another.
Existing SET NOT NULL remains a no-op retaining identity.
An actual column-type change that rebuilds a DEFAULT replaces its definition OID
through the same atomic publication allocator while retaining captured metadata.
Failed conversion preserves the old identity, default, type and rows; this does not
change the existing same-Usage no-op boundary.

`pg_get_constraintdef` returns `CHECK (<expression>)` plus ` NO INHERIT` when
present, or `NOT NULL <current-column>`. Returned CHECK suffixes can be consumed
by CREATE for the same supported scalar syntax and enforcement semantics; original
source formatting is not preserved. Rename updates quoted names without rebinding
the stored types/providers. `pg_get_expr` renders catalog tree expressions.
Both compact and pretty suffixes preserve nested comparison grouping and provider
identity under search-path shadowing. Copying a returned default into the same
destination Usage preserves typed assignment values, not merely its output text.
NUMERIC(4,2) DEFAULT '1.2' deparses as 1.2 but inserts 1.20; CHARACTER(3) DEFAULT
'xy' deparses unsized and inserts 'xy '. Temporal precision is likewise applied
by the stored coercion/assignment tree; INTERVAL retains input field/precision sizing.
Synthetic comparison casts never inherit an unrelated column's typmod:
NUMERIC(4,2) n<1000 emits unsized ::numeric, so copying cannot introduce a sizing
overflow. A real CAST(1000 AS NUMERIC(4,2)) remains sized and erroring.
Actual-copy fixtures require exact retained row values after rejected batch
INSERT and UPDATE, not merely unchanged row counts. CHECK is not retroactively
revalidated when comments, search paths or session TimeZone change; subsequent
DML observes current provider/session state through held prepared handles.

CREATE DEFAULT NULL and transparent built-in casts such as NULL::integer create
no pg_attrdef definition; ALTER SET DEFAULT NULL removes one. This narrow producer
normalization never evaluates CASE, COALESCE, functions or arbitrary expressions
to infer NULL. Nontrivial NULL-producing definitions remain stored; generated
NULL expressions also remain stored. Native normalization currently strips domain
coercions too, so domain-coerced NULL defaults can be pruned where PostgreSQL
retains them. That DDL/storage limitation is not fixed by pg_attrdef projection.
Explicit INSERT NULL and NOT NULL/CHECK enforcement use the ordinary typed executor.


## Object comments

COMMENT stores one immutable UTF8 text per TABLE, COLUMN, SCHEMA, SEQUENCE or ROLE.
Another command replaces it; NULL and the empty string remove it, while
whitespace remains text. It affects zero rows and returns no columns or
parameters. Names and literals use the ordinary positioned parser, including
quoted dots, E/dollar/Unicode strings and parse-time string settings. Values
cannot be expressions or parameters. COMMENT remains an unreserved identifier.

TABLE/COLUMN lookup uses current search_path and schema USAGE. Missing columns
are resolved before owner checks. Owners and effective superusers may comment
local objects; ordinary grants do not substitute for ownership. Nonowner TABLE
uses `must be owner of table t`, COLUMN `must be owner of relation t`, and
SCHEMA `must be owner of schema s` (42501). ROLE uses real shared role authority:
ordinary users cannot comment even themselves (CREATEROLE required); a superuser
target requires SUPERUSER. This native role surface has no CREATEROLE/ADMIN SQL.
Prepare caches syntax, never target identity or ownership; Execute resolves anew.
Qualified TABLE/COLUMN targets in a missing schema fail `3F000`,
`schema "s" does not exist`; a missing relation in an existing schema fails
`42P01`. Schema-USAGE denial precedes relation/column lookup and has no Position
field for COMMENT, unlike positioned DML analysis diagnostics.
Serialized COMMENT waiters refresh immutable metadata at the original captured
relation identity after lock grant; a prior comment does not count as replacement.
Reset/Restore and drop/recreate still fence the old epoch/address, never retargeting
a waiting command to an object with the same name.

CatalogRead.Comment exposes local metadata at stable class/object/SubID addresses;
RoleRead exposes shared text through RoleDescriptor.Comment. SQL inquiries use
the same immutable metadata; see [object comment inquiries](SQL.md#object-comment-inquiries--change-note).
`pg_catalog.pg_description` now projects local descriptions at those same
`(objoid,classoid,objsubid)` identities, with no empty/NULL description rows.
Relation/column keys use class1259; schemas2615, types1247, collations3456 and
conversions2607. Shared ROLE text remains excluded; no `pg_shdescription`
projection is added. Reading a numeric identity requires no target privilege.
Legacy whole-object collision results remain consistent within each statement,
without promising a portable catalog ordering. Persisted inquiry views retain
typed syntax even when strict constant-NULL inputs eliminate execution demand.
Dropping/recreating an object clears text, while schema rename/owner transfer
preserves it. Plain LIKE does not copy comments; INCLUDING COMMENTS copies exact
column text, not the table comment. COMMENT ON TYPE, DATABASE and
other unsupported object kinds remain excluded. Table/schema COMMENT in an
explicit driver transaction returns native 0A000; supported sequence COMMENT
uses transaction-private metadata. PostgreSQL permits transactional COMMENT.

Dump captures local comments with the same immutable snapshot as rows: schemas
in name order, then table/column text after CREATE in declaration order. Escape
literals preserve apostrophes, backslashes, newlines and Unicode independently
of destination string settings. Fresh native `pg_catalog` and `public` carry
`system catalog schema` and `standard public schema`, respectively. Unchanged
bootstrap descriptions add no dump bytes; overrides emit text and explicit
removals emit `COMMENT ON SCHEMA ... IS NULL`. Reset restores this fresh baseline,
not overrides or removals. Restore replaces local state atomically and replays
those differences, including removals. Both retain role comments, which are
deliberately excluded from database Dump and Restore.
The readonly projection is derived, not dumped as INSERTs. Its163 fresh native
rows cover2 namespace,26 scalar type,7 collation and128 conversion release seeds;
unsupported objects and represented arrays do not acquire invented text.
Live column comments retain their original attribute numbers through drop,
rename, supported type changes and schema moves. Existing SCHEMA/SEQUENCE owner
changes retain their description addresses/text; no TABLE OWNER TO syntax is
added by this catalog. Canonical Dump names surviving columns;
Restore reconstructs declarations and may remint a historical survivor3 as2
after middle-column removal. Text and named object relationships round-trip,
not old allocator OIDs or historical attribute gaps.


## Column defaults

CREATE TABLE stores structurally immutable, bound column `DEFAULT` definitions;
this does not require the expression's volatility to be Immutable. Supported
closed, parameter-free, row-independent Immutable and Stable trees are admitted,
including temporal casts, `AT TIME ZONE`/`AT LOCAL`, comparisons, CASE and NULLIF.
Session-dependent temporal operations remain Stable, not folded constants.
Existing literal/NULL, typed temporal/interval constants, same-type scalar Usage
CAST/`::` annotations, existing directional numeric and baseline/character/OID/
temporal casts, bounded function-style casts, INT4/INT8 unary/binary arithmetic,
scalar comparisons and IS [NOT] DISTINCT FROM are admitted
at the outer level. Volatile producers remain unsupported except the existing
fixed early-bound `nextval('sequence')` allowance, closed selected UUID-generator
trees and closed `clock_timestamp()`/`timeofday()` trees through already admitted
scalar wrappers. The selected time exception does not admit random, sequence,
UUID or other unrelated volatile descendants through a mixed tree.
Row/parameter/execution-stage dependence remains prohibited.
CREATE DEFAULT Boolean NOT/AND/OR and null/truth tests require parentheses, e.g.
`DEFAULT (NOT NULL)` or `DEFAULT (NULL IS NULL)`. Column references and positional
parameters are prohibited even inside casts or decisive logical branches.
General SQL functions, subqueries, arithmetic beyond admitted scalar operators,
new cast families remain outside this DEFAULT-expression surface.
ALTER TYPE independently assignment-coerces stored defaults; USING transforms
existing rows only, never the default expression or future DEFAULT inserts.
Restrictions apply as ordinary recursive binding reaches each occurrence; earlier
child Boolean, input and operator diagnostics retain their PostgreSQL precedence.
The existing null, truth and distinct predicates retain their ordinary typed
semantics and fully parenthesized unevaluated Dump representation in defaults.
Named DEFAULT syntax is accepted but does not create a named constraint object.
Plain LIKE copies shape and NOT NULL, **not defaults**. INCLUDING DEFAULTS copies
the cooked typed definition without evaluating it or recapturing temporal literals.
Copied nextval defaults retain the original sequence; this is independent of
INCLUDING IDENTITY, which creates a new owned sequence.

Runtime INSERT default demand stops at the first completed row failing NOT NULL
or CHECK, before evaluating later rows. Rows publish only as one atomic batch;
sequence allocations already demanded by the rejected row are not rolled back.

`ALTER TABLE target ALTER [COLUMN] name SET DEFAULT expression` and `DROP DEFAULT`
change only the selected immutable default descriptor. Existing rows and column
nullability remain unchanged; future omitted/DEFAULT requests, including held
prepared INSERTs, use the current definition. Dropping an absent default succeeds
without a new descriptor revision. No explicit default means implicit typed NULL,
which still fails a retained NOT NULL constraint when consumed.
ALTER uses the general supported expression grammar: `TRUE OR FALSE`, `NOT FALSE`
and `NULL IS NULL` do not require extra parentheses. Assignment binding reuses
CREATE's type providers without evaluating a value: unknown invalid literal input
fails immediately; typed conversion, integer narrowing and division by zero can
fail only on subsequent demand. Failed SET preserves the old definition.
Semantic ALTER default diagnostics have no source Position; syntax errors retain
their character positions and CREATE diagnostics retain their existing positions.
Owner-only execution takes the existing AX grant and publishes one fenced
metadata-only catalog stage. Qualified/quoted targets, optional COLUMN, ONLY,
ONLY(...) and trailing `*` work for standalone tables. Graph participants reject
the inheritance ALTER capability boundary above; PostgreSQL-only reference tests
specify recursive SET/DROP and ONLY behavior without claiming native parity.
Action lists and ALTER TABLE IF EXISTS for these actions remain unsupported.
Native explicit transactions exclude standalone SET/DROP DEFAULT, not selected
ADD COLUMN/TYPE/nullability/DROP COLUMN lists; read-only ALTER fails25006 before lookup.
Dump writes current defaults and explicit old rows. Defaults referencing inline
identity providers order their owning tables first; legal default cycles instead
emit ordinary sequence-reference SET DEFAULT statements after all definitions and
before data. Restore admits these bounded deferred statements through the same
default binder, not arbitrary ALTER scripts. UPDATE DEFAULT remains unsupported.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterDefaultSmoke$' VERBOSE=0`.

### Adding a column

`ALTER TABLE target ADD [COLUMN] [IF NOT EXISTS] name type` uses the existing
column type/typmod/collation and NULL/NOT NULL/DEFAULT/CHECK declaration surface.
Qualified/quoted names, ONLY/ONLY(...) and trailing `*` apply to standalone tables;
inheritance/partition descendant propagation is outside this feature.
ADD serial/identity/generated/unique/FK declarations remain excluded.

Schema metadata and stored shapes change atomically under the existing owner-only
AX grant. Old rows receive typed NULL without a default. Supported Immutable/Stable
defaults are evaluated once at ADD, even when no old rows exist; admitted volatile
defaults, including clock_timestamp()/timeofday(), draw once per effective live
row (never ended/deleted/replaced predecessors). Future INSERT omission/DEFAULT
evaluates the retained original
expression. A later standalone SET DEFAULT changes only future requests.
Structural DEFAULT NULL creates no pg_attrdef definition, subject to the
domain-coercion NULL pruning limitation described above.

New NOT NULL and CHECK constraints validate completed rows; CHECK NULL passes.
ALTER errors use contains-null / violated-by-some-row diagnostics, without INSERT's
failing-row detail. Failed validation leaves the old schema/data intact. Sequence
draws already demanded are nontransactional and are not promised to rewind.
IF NOT EXISTS preserves the existing column and emits42701 NOTICE before semantic
type/default/CHECK binding; system names conflict even with that modifier.
Syntax still parses completely. DROP/TYPE precede ADD regardless of textual order;
added CHECKs bind after all new columns exist. Mixed SET/DROP DEFAULT lists remain
unsupported, and their standalone explicit-transaction exclusion is unchanged.

Fresh direct/prepared results append labels/types/native nullability; held explicit
survivor SELECTs and targeted INSERTs rebind, but changed-width held SELECT* fails
0A000 cached-plan result-type validation. Stored view stars retain their old width.
Private ADD shapes/defaults/CHECKs participate in joint Commit and savepoint rollback.
Stable attributes never reuse dropped identities in the same relation; pg_class
relnatts retains the attribute high-water, while pg_attrdef.adnum uses actual IDs.
Logical Dump writes final CREATE and explicit data, not ADD/DROP history; Restore
may remint live attribute numbers and subsequent ADD advances its restored ceiling.
Native eager cursors retain old values/metadata across ADD/Reset/Restore.
Native Control/context/I/O APIs are host mechanisms, not PostgreSQL control APIs;
no physical tuple optimization, server/wire protocol, disk durability or pg_dump
process is implemented. Existing ACL Dump limits remain unchanged.
Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$' VERBOSE=0`.


Each INSERT row evaluates declared defaults for omitted columns or per-cell
`DEFAULT`; absent declarations yield typed NULL. Explicit supplied values,
including NULL, bypass defaults. Targetless `DEFAULT VALUES` inserts one all-default row;
an explicit target list before DEFAULT VALUES is a syntax error (42601).
Stable defaults use the admitted INSERT's captured TimeZone and DateStyle.
Changing settings between executions of the same prepared INSERT can therefore
change actual default values without changing the stored definition. CREATE and
Dump serialize/validate the expression tree without evaluating it; typed literal
leaves still freeze at CREATE analysis, while supported session-dependent parent
operations are evaluated only when consumed by INSERT.
Ordinary INSERT supplied values retain the existing scalar/cast surface;
SELECT and ORDER-decorated VALUES sources retain their existing query surface.
Defaults do not change declared result types, ScanTypes or nullable metadata.
CREATE resolves each column's type before validating its NULL/NOT NULL and
DEFAULT declarations in source order, before proceeding to later elements.
The second DEFAULT fails with 42601 at that token. Earlier type/nullability
errors still win; all declaration structure and NOT NULL merging complete before
any default expression is bound, so a later duplicate can precede an earlier
default-input error. Restore uses the same binder and preserves the existing
catalog and data on failure.
Utility Prepare remains semantic-analysis-free; CREATE binds input/type errors
on execution, while destination narrowing and typmod sizing remain deferred to
INSERT. For example `INT DEFAULT 2147483648` creates successfully but fails
on use with 22003. Every INSERT preflights actually consumed declared defaults'
immutable conversions and closed integer arithmetic at execution, after supplied-value
or query-source preflight and before executor authorization and capture. For query
INSERT, even an empty source can report demanded division-by-zero or overflow errors.
This bounded default-only planning also covers ordinary omissions, per-cell requests,
undecorated parenthesized VALUES and DEFAULT VALUES; it visits the left
Boolean operand first.
A resulting concrete TRUE on OR, or FALSE on AND, makes the whole execution
expression a private BOOL literal without demanding the discarded right branch.
Nested closed constant arithmetic/comparisons/predicates and NOT use their existing
typed operations after required children are handled. NULL/nondecisive left operands
still demand the right; a bad left operand is not rescued by a decisive right.
CREATE still analyzes both operands and rejects prohibited references or eager
input errors. Stored definitions remain unfolded, including discarded casts.
Explicitly supplied values, including NULL, bypass defaults. A default is
planned only when some ordinary row requests or omits that destination, or
a query omits it; a column supplied in every row is never default-planned.
Mixed rows retain one private schedule but evaluate remaining runtime expressions
only in cells consuming the default, never for a zero-row source.
This is not a general runtime short-circuit or broader optimizer guarantee;
ordinary query/DELETE/UPDATE/supplied-value preflight and logical evaluation are unchanged.
Relative temporal input
such as `'now'` captures CREATE's transaction input environment, not a volatile
per-row SQL function.

Every statement stages complete rows before publication. Later default evaluation,
conversion, NOT NULL or cancellation failure leaves existing rows unchanged.
Descriptor owner/comment/NOT NULL changes preserve definitions; dropping a column
removes only its stable attribute's default. Dump preserves canonical expression
structure and captured temporal leaves without evaluating defaults; stored data,
including explicit NULL, remains explicit. Restore uses the same public syntax
and binder, atomically replacing definitions/data or preserving the target on failure.
Existing integer arithmetic defaults retain operator structure, grouping and operand
widths through native controls; CREATE and Dump never evaluate them. Destination
BIGINT does not widen an INT4 operation to avoid overflow. Explicit character CAST
typmods retain Unicode truncation and BPCHAR-to-TEXT trimming; destination assignment
still reports overlength rather than silently truncating. UPDATE DEFAULT remains
outside this INSERT-only default consumer.
The rounded finite TIMESTAMP endpoint retains its type-owned `TIMESTAMP(0)`
encoding exactly once, including temporal leaves inside comparison defaults.
Explicit casts retain their actual coercion mode and canonical CAST structure.
Known TEXT children stay typed, so `CAST('oops'::text AS INT)` remains an
execution-demanded conversion through empty/populated controls rather than
becoming a CREATE-time unknown-input error. Recursive DATE leaves retain DATE
introducers, including BC comparisons whose civil ordering differs from TEXT.
BYTEA defaults reuse the existing canonical hex codec and return raw octets;
same-type BYTEA annotations add no known-TEXT-to-BYTEA conversion provider.

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestDefaultSmoke$' VERBOSE=0`.
Dual-mode defaults: `make quickcheck PACKAGES=./reference RUN='^TestReference(Default.*|InsertDefaultMixedParameters|AlterDefault.*)$' VERBOSE=0`.
Pinned PostgreSQL witnesses: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(Default.*|InsertDefaultMixedParameters|AlterDefault.*)$' VERBOSE=0`.
General sequence/volatile composition remains unsupported in declared DEFAULT;
fixed nextval leaves support SERIAL and native replay. Identity instead uses an
internally owned sequence. PostgreSQL disk/WAL
durability, wire networking, native ABI extensions and external pg_dump processes
or custom archives are non-engine mechanisms, not successful no-op promises.

## Identity columns — change note

Integral columns support `GENERATED ALWAYS AS IDENTITY` and `GENERATED BY DEFAULT
AS IDENTITY`, with independent internally owned sequences and implicit NOT NULL.
There is no implied uniqueness, nor an ordinary stored nextval DEFAULT.
Omission/per-cell DEFAULT generates; BY DEFAULT permits explicit values, while
ALWAYS requires `OVERRIDING SYSTEM VALUE` to keep explicit values. USER overrides
replace identity inputs only, leaving ordinary columns/defaults unchanged.
Sequence allocation and session memories survive row failure/rollback; transactional
RESTART replaces a private generator and rolls back with identity metadata.

Existing sequence calls in CHECK can reference the identity sequence declared
by the same CREATE TABLE, with automatic or explicit SEQUENCE NAME, including
nested CREATE SCHEMA elements. Binding uses one private staged catalog for
namespace lookup, regclass identity and provider stamps; incumbent admission and
runtime authorization remain live. Failed construction installs neither table,
sequence nor generator. CREATE and Dump do not evaluate the CHECK. For a fresh
CACHE 1 sequence, an omitted INSERT
produces id 1 and its own `CHECK(nextval('i_id_seq') > 0)` consumes 2.
Own-table CHECK references do not obstruct reciprocal table/sequence cleanup;
external view/CHECK dependencies retain the ordinary restrictive drop behavior.

ALTER ADD requires NOT NULL and no regular default; it never backfills old rows.
SET generation/options and RESTART affect future inserts. DROP IDENTITY removes
only its internal sequence and preserves rows/NOT NULL. Direct sequence DROP and
OWNED BY reassociation cannot bypass the link. Rename/schema/owner transitions
preserve stable attribute ownership. Plain LIKE copies NOT NULL but not identity.
Stored expression-generated columns have a separate current-row expression,
not an identity sequence or ordinary DEFAULT; see [stored generated columns](#stored-generated-columns).

Identity ALTER diagnostic ordering follows PostgreSQL's utility phases. Repeated
SET GENERATED clauses are syntactically valid but fail at execution with42601
`conflicting or redundant options`, without a parser Position. Numeric sequence
validation precedes that conflict. ADD privately validates/stages its sequence
before checking existing identity, NOT NULL and regular-default guards: invalid
CACHE can therefore produce22023 before55000. Nonintegral ADD produces22023
`identity column type must be smallint, integer, or bigint`. A failed guard or a
genuinely later action publishes neither the proposed sequence nor partial
mode/options/restart changes; the old reciprocal link and row backing remain.

The immutable identity definition is keyed by stable column attribute identity and
holds the exact sequence address; the sequence holds the reciprocal internal-owned
column address. Catalog validation requires both directions, integral type, NOT NULL,
no ordinary default and matching owner/schema. Native replacement remaps both links,
including collisions with retained TEMP sequences, rather than associating by name.

The assigned identity SQL regression scenarios live in untagged external
`reference/` definitions selected by `^TestReferenceIdentity[A-Za-z0-9_]*$`.
They merge the 28 former root interaction tests with existing reference
assertions, including actual held-statement reuse, empty/raw metadata, failure
demand and session-cache checkpoints. The same definitions run against native
stuntgres by default and add the pinned PostgreSQL oracle with explicit
`-reference=true`; see the [development guide](DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
for complete family commands.

Native Dump/Reset/Restore, retained snapshots and repeated owner/TEMP remaps are
explicit native phases; PostgreSQL independently replays the unchanged SQL
stream and checks its consumers, owners, comments and persistence. Raw metadata
and complete backend-specific diagnostics are asserted before normalization.
Unordered source consumers retain duplicate-preserving payload bags without a
portable generated-ID/payload association. These test-routing changes do not
expand SQL support or imply physical durability parity. Unrelated tagged
catalog, authority and lock witnesses remain separate.

Identity ALTER waits for the exact linked sequence operation lock, then uses its
grant-time committed options/generator while preserving staged changes and the
original table/storage fences. A concurrently committed ALTER SEQUENCE must not be
overwritten by stale options. Cancellation publishes no identity change and cleans
the waiting operation; subsequent healthy INSERT remains usable. Regression witnesses:
`TestIdentityAlterSequenceGrantFreshness`, `TestIdentityAlterCanceledPublication`,
`TestReferenceIdentitySequenceGrantFreshness` (untagged dual-mode) and
`TestIdentityControlsTemporaryCollision`. The migrated public ALTER lifecycle
is covered by `TestReferenceAlterIdentityTransitions` and
`TestReferenceAlterIdentityDropLifecycle`; the complete selected family is
`^TestReferenceAlterIdentity` in `reference/`. See [test policy](TESTING.md)
for oracle-first discovery and shared quick/reference execution.

`TestReferenceAlterIdentityTransactionalAtomicity` covers genuinely later SET
failure and private commit/rollback/savepoint behavior.
`TestReferenceAlterIdentityWaitCancellation` observes real table/sequence waits
before cancellation and checks healthy held-command reuse.
`TestReferenceAlterIdentityControls` captures current state after ALTER, including
explicitly named links and a removed second identity, rather than serializing
ALTER history. Native controls retain old snapshots and preserve the altered
baseline on malformed, reader-failed or canceled Restore. PostgreSQL server
PID/lock-catalog cancellation probes are external fixture mechanisms; native
context cancellation remains an ordinary Go error, not a fabricated SQLSTATE.

Integral identity TYPE preserves the exact reciprocal sequence link and mode,
owner, ACL and comments while using the sequence owner's AS rules for type,
matching old type-extreme bounds and retained custom options. It also runs AS for
same-type TYPE. All raw USING expressions bind against the original row before
these before-statements; nonintegral identity targets fail22023 before row demand,
even if the same action list drops identity. A separately completed DROP IDENTITY
removes that restriction. A plain AUTO OWNED BY sequence is not an identity and
does not change type/options/cache when its column changes.

AS uses a private scalar generator alongside the candidate descriptor. Existing
USING/CHECK nextval calls see that generator, and successful publication retains
their advance. Failed TYPE and rollback restore catalog/data/scalar roots, not
physical-session currval/lastval or the caller's discarded AS cache tail.
Rollback to a pre-TYPE mark restores the old scalar; a post-TYPE mark retains
demand in that same private generation. Peer unseen cache tails are not flushed
by private or failed AS. Grant-time option/state refresh and cancellation use the
existing identity sequence lock owner.
Pre-wait dependency observations account for only the exact internal identity
descriptor captured after that grant. New view/CHECK dependencies, plain AUTO
ownership changes and later descriptor replacements still conflict; the original
descriptor fence and final reciprocal-link validation remain mandatory.
Witnesses: `TestAlterTypeIdentitySequenceGrantFreshness`,
`TestAlterTypeIdentityCanceledSequencePublication`,
`TestAlterTypeIdentityStagedSequenceDemand`,
`TestAlterTypeIdentityPrivateSavepoints` and `TestAlterTypeIdentityControls`.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterTypeIdentity(Smoke|ControlsSmoke)$' VERBOSE=0`.


## Ordinary table definitions and removal

`CREATE TABLE z ()` is a real zero-column relation: direct/prepared `SELECT *`
returns no columns and no rows for the newly created table. `LIKE z` is legal.
There is no dummy column. Public `INSERT ... DEFAULT VALUES` inserts one row,
including into a zero-column table; `INSERT ... VALUES ()` is invalid syntax. Ordinary `oid` is a legal column;
`ctid`, `xmin`, `cmin`, `xmax`, `cmax` and `tableoid` conflict with system names.

LIKE copies live column names, supported full types/typmods/collations and NOT NULL
at the element's position. Named NOT NULL declarations retain source names, with
fresh destination definition identities. Multiple/interleaved clauses and existing
view and composite-type sources are allowed; duplicate expanded names fail `42701`
atomically. Composite members retain their declared nullable shape; options
without applicable descriptors have no effect.
The destination has fresh relation/attribute identities, current-user ownership,
no source rows or ACL and no automatic parent linkage. A copied expression can
still retain real external dependencies, including an explicit source REGCLASS.
Table/view LIKE sources require relation-level SELECT; column-only SELECT is
insufficient. Composite sources instead require USAGE on the reciprocal type,
not SELECT on its nonstorage backing relation. Namespace USAGE precedes source-kind
validation, which precedes the applicable source privilege. Sequences and indexes
fail `42809`, even when their SELECT privilege is denied. Composite source relation
and type incarnations are captured through the existing CREATE publication fences.

Qualified LIKE sources use that schema's USAGE checks; unqualified sources follow
the effective search path independently of the destination. Nested CREATE TABLE
elements in CREATE SCHEMA support the same ordered elements and zero-column shape.
They bind against preceding staged tables as the enclosing schema owner, with that
schema prepended to a local path. The caller's identity and stored path do not
change; inaccessible sources or missing applicable source privileges fail the
entire schema construction.

For example:

```sql
CREATE TABLE source (i INTEGER NOT NULL, s TEXT, f BOOLEAN);
CREATE TABLE clone (lead BIGINT, LIKE source, tail TEXT);
DROP TABLE source RESTRICT; -- clone remains independent and writable
CREATE TABLE empty_shape ();
```

Each LIKE clause starts with all optional properties excluded. Ordered repeated
`INCLUDING`/`EXCLUDING` COMMENTS, COMPRESSION, CONSTRAINTS, DEFAULTS, GENERATED,
IDENTITY, INDEXES, STATISTICS, STORAGE and ALL are accepted; the last option wins.
ALL changes every option bit, and subsequent exclusions affect only their property.
NOT NULL is unconditional, including EXCLUDING CONSTRAINTS or EXCLUDING ALL.

DEFAULTS copies ordinary cooked defaults; GENERATED separately copies admitted
STORED expressions and remaps row references/tableoid to the destination. Explicit
REGCLASS/REGTYPE constants and external calls are not redirected. CONSTRAINTS
copies named CHECKs and NO INHERIT, not UNIQUE. Copied CHECK-name collisions use
`42710` and never merge equal expressions.

TEMP sources are not blanket exclusions. Constant defaults and defaults using
permanent sequences copy into permanent or same-session TEMP destinations.
An ordinary default capturing a TEMP sequence requires the destination's same
physical session token. Copying it into a permanent destination fails native
`0A000`, `temporary sequence DEFAULT is unsupported`, at Execute without Position;
PostgreSQL permits that dependency. Excluding DEFAULTS avoids this boundary.
Same-session TEMP copies retain the original sequence and its contextual
reference manifest across native Reset/Restore, without evaluating or rebinding
the default. This does not make generic stale or foreign references valid.

IDENTITY copies mode and numeric configuration into a fresh independently owned
sequence, initially uncalled at its configured START, not at source current state
or RESTART value. Fresh sequences follow destination namespace and persistence:
permanent for permanent destinations, temporary for TEMP destinations, never a
source token or persistence override.
INDEXES copies live btree/hash/unique/primary-key/expression/INCLUDE definitions and admitted
equality EXCLUDE attachments, including their definition-time deferrability,
partial B-tree/UNIQUE and constraint-local HASH predicates, classes, collations
and ordering into freshly named ready/valid/live indexes with new roots and
backing identities only where the source has a backing constraint.
Both analyzed and execution predicate trees use destination attributes; external
REGCLASS/REGTYPE constants retain their original dependencies. Only TRUE rows
enter a partial root, before derived-key evaluation. Live invalid source
definitions are rebuilt, nonlive ones omitted. COMMENTS copies exact column text,
never table text
or source authority. Index-comment SQL/control and constraint-comment providers
remain absent; a generic descriptor comment field does not promise those APIs.

Compression/storage/extended-statistics descriptors, virtual generation,
constraint-comment providers are not implemented. Standalone partial HASH
and partial concurrent index creation retain their existing native exclusions;
ordinary partial B-tree/UNIQUE and equality EXCLUDE definitions are real providers.
An option with no applicable admitted descriptor copies nothing, even under ALL;
accepting its grammar does not add an absent provider.

Source and selected providers are held by exact incarnation through the single
atomic publication. Inline CREATE SCHEMA uses preceding private staged sources.
Dump serializes the resulting independent definitions/data, and Restore replays
them atomically; no retained LIKE history or source-name rebinding is needed.
Native controls retain existing physical-session TEMP definitions/data separately
from the permanent emitted stream. New copies from retained TEMP sources give
mapped local column/tableoid references the fresh destination's current authority,
including both cooked generation, expression-key and predicate trees and CHECKs.
External literals and DEFAULT providers retain their original contextual authority;
this does not admit stale external TEMP references into permanent consumers.
Actual independent permanent stream replay requires neither the original TEMP
source nor its private namespace.
Shared coverage: `RUN='^TestReferenceTable(Like|Core)'`. Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceTableLikeFeatureSmoke$' VERBOSE=0`
and the same command with `REFERENCE=1` for the pinned PostgreSQL oracle.
Unknown syntactically valid type names prepare successfully and fail execution
with `42704`, `type "name" does not exist`, at the type's character position.
Known single-name types outside INT2/INT4/INT8/NAME/TEXT/BPCHAR/VARCHAR/BOOL/FLOAT4/FLOAT8/DATE/TIME/TIMESTAMP/TIMETZ/TIMESTAMPTZ/NUMERIC/UUID/BYTEA fail
execution with `0A000`. Excluded multiword families (BIT VARYING and qualified
INTERVAL fields), unsupported qualified type names, arrays and modifiers other than FLOAT(p),
numeric precision/scale, character length, temporal precision and admitted BYTEA/NAME modifiers
remain parser-level `0A000` boundaries. DATE, BYTEA, TEXT and NAME modifier syntax is admitted only
to reject it semantically with `42601`, `type modifier is not allowed for type
"date"`, `"bytea"`, `"text"` or `"name"`, at the type token, before validating modifier operands.
CREATE Prepare is metadata-only and succeeds; direct or prepared execution rejects
the modifier without publishing a table. TIME/TIMESTAMP precision has its own bounded integer grammar.
Even an existing IF NOT EXISTS target cannot bypass excluded syntax forms;
failure-only guards construct no executable family AST. DOUBLE PRECISION and
valid FLOAT(p) are supported.

Type keyword aliases are not quoted catalog names: `"smallint"`, `"integer"`,
`"bigint"` and `"boolean"` fail with `42704`, while `"int2"`, `"int4"`, `"int8"`,
`"text"` and `"bool"` select the supported concrete types. Quoted case is significant (`"INT2"` or `"INT4"` is
missing), and diagnostic names retain decoded quotes and backslashes without
SQL or Go re-escaping. NAME and quoted `"name"` are supported distinct from TEXT;
quoted `"NAME"` is missing. Six unqualified lowercase serial spellings, including
quoted lowercase names, are CREATE-only shorthand: smallserial/serial2 → SMALLINT,
serial/serial4 → INTEGER, bigserial/serial8 → BIGINT. Quoted `"SERIAL"` and
schema-qualified serial names are missing types, not shorthand.
SERIAL modifiers are rejected with the lowered integral type's `42601`
diagnostic at the original type position; no integral typmod support is added.

SERIAL adds NOT NULL, a width-matched sequence and an ordinary fixed nextval
default, not uniqueness or identity override behavior. Supplied values (including
duplicates, zero and negative values) bypass allocation; supplied NULL fails
NOT NULL. Generated names use collision-safe UTF8 truncation in the table schema.
Sequence privileges apply independently of table INSERT; failed rows/rollback
can consume values. Width overflow uses checked assignment, not identity wrapping.
AUTO ownership removes the sequence on owning column/table drop. The independent
NORMAL default dependency blocks direct sequence RESTRICT; CASCADE removes only
the default. DROP DEFAULT does not drop the sequence, and OWNED BY NONE does not
remove the default. Rename/schema move retains the exact default target.
Dump writes integral columns and separate sequences, preserving names/options/
state/ownership; Restore uses the same shared fixed-nextval binder.
An ordinary fixed-nextval default may target an identity-owned sequence without
becoming an identity column or bypassing sequence ACLs. Native publication protects
incumbent default providers with AccessShare holds and checks the merged catalog
root; newly staged generated sequences stay private until atomic publication.
This is a native catalog-integrity fence, not a claim of identical PostgreSQL
transaction-lock behavior. Dump orders inline identity providers before consumers,
and defers sequence-reference defaults when otherwise legal defaults form a cycle.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSerialSmoke$' VERBOSE=0`.

DROP preflights the entire explicit target group before one atomic catalog
publication. A later missing or unauthorized target leaves every table intact.
Duplicate resolved targets such as `a, public.a, a` remove one identity once.
IF EXISTS emits per-missing-target NOTICE/00000 (`table "name" does not exist,
skipping`, or `schema "name" does not exist, skipping`). DROP requires table
owner, schema owner or superuser authority, not ordinary table privileges, and
preserves current schema lookup/USAGE authorization.

The supported dependency set includes stored views, owned sequences and B-tree/hash indexes.
Both modes remove the selected tables and their contained columns, storage and
relation/column ACL records; dependent views retain the RESTRICT/CASCADE policy
described below. Namespace,
builtin types, owner roles and unrelated tables survive. Real DROP ROLE ownership
and ACL blockers disappear with the removed definitions. LIKE clones, prepared
statements and held cursors are not persistent dependents; held cursors retain old
rows/metadata, while prepared executions rebind (absent table `42P01`,
incompatible recreation `42703`). B-tree/hash indexes and column-owned
sequences disappear automatically with their parent; views retain their existing
RESTRICT/CASCADE dependency policy. Foreign keys remain excluded.
Explicit-transaction CREATE/DROP TABLE remains unsupported.

Dump emits expanded canonical declarations, including `()`, independently of a
dropped LIKE source. Restore uses the same binder in isolated staging; failed
LIKE/type/duplicate/option declarations preserve the target. DROP remains
excluded from Restore scripts. Existing explicit-ACL Dump rejection is unchanged.
The independent reference recorder preserves genuine zero-column cursor
metadata as an empty result, distinct from no query result; consumed rows and
cursor closure are checked for both native and PostgreSQL executions.

## Primary-key constraints — change note

```sql
CREATE TABLE scalar(id INT PRIMARY KEY);
CREATE TABLE composite(a INT, b TEXT, CONSTRAINT composite_pk PRIMARY KEY(a, b));
```

CREATE TABLE supports named or unnamed column and table PRIMARY KEY declarations,
including table keys before their columns. Keys are ordered lists of 1–32
distinct ordinary attributes. Missing/repeated columns fail42703/42701 at the
PRIMARY or CONSTRAINT declaration; a second explicit primary key fails42P16
before binding its keys. Empty/expression key lists are syntax errors.

Column-local PRIMARY KEY implies NOT NULL at its declaration position:
`a INT NULL PRIMARY KEY` and `a INT PRIMARY KEY NULL` conflict with42601.
A table-form key can upgrade a separately declared nullable column. Its missing
NOT NULL identities are appended in key order after original explicit/inherited
NOT NULL declarations, rather than physical column order. An explicit NOT NULL
name remains independent of the primary-key name.

Explicit CREATE index declarations retain the primary key first. Identical
ordered ordinary UNIQUE keys collapse into it, transferring the first explicit
UNIQUE name if the primary key is unnamed. Reversed keys and UNIQUE NULLS NOT
DISTINCT remain separate. Two primary keys never deduplicate. This retention
rule applies only to explicit declarations: LIKE copies and explicit UNIQUE
constraints remain independently owned, even on the same keys.

Generated names are `table_pkey`, then numbered suffixes, using existing UTF8-safe
63-byte clipping. Generation sees existing namespace objects and earlier retained
backing relations, not future explicit names. Existing backing relation collisions
fail42P07 before local CHECK/NOT NULL name collisions; otherwise local constraint
name collisions fail42710. CREATE publishes the table, separate NOT NULL and
primary constraint identities, and actual enforcing B-tree as one atomic group.

Typed immediate uniqueness and transaction key claims reuse the ordinary UNIQUE
providers. NULL fails23502 before uniqueness or ON CONFLICT omission. Duplicates
fail23505 naming the real primary constraint, with typed key Detail only when
the current caller can SELECT the table or every key column. All existing INSERT
producers, UPDATE and MERGE retain their whole-statement atomicity.
`ON CONFLICT ON CONSTRAINT primary_name DO NOTHING` selects this real provider.

`pg_constraint` reports kind `p`, owning table and NULL conbin, with separate
kind `n` identities for nonnullable keys. Both pg_get_constraintdef signatures
render PRIMARY KEY using current labels; pg_indexes renders CREATE UNIQUE INDEX.
Native result metadata knows key columns are nonnullable, including empty and
prepared cursors; lib/pq does not expose nullable metadata. The backing attachment
supplies primary/unique/immediate index metadata internally without adding native
catalog columns.

Renames, schema-group moves and REINDEX preserve the primary discriminator and
reciprocal attachment. Direct backing-index DROP, even CASCADE, fails2BP01;
backing-index rename updates the constraint name while retaining both identities.
Dropping a key column removes the primary key/backing, not surviving key NOT NULL
identities. Unrelated column removal preserves the group and stable attributes.
Actual key TYPE changes remint affected primary/backing/NOT NULL identities;
the rebuilt primary identity precedes rebuilt NOT NULL identities. Unaffected
identities and the table identity remain. Duplicate or NULL conversions leave
all original descriptors, rows and roots unchanged.

A live primary key prevents ALTER COLUMN DROP NOT NULL and DROP CONSTRAINT
of its key's NOT NULL identity with42P16. Identity-column ALTER COLUMN DROP NOT
NULL retains its earlier42601 identity guard; named NOT NULL DROP still reaches
the primary-key42P16 guard first. Dropping the primary key leaves its NOT NULL
identities unchanged. Ordered `DROP CONSTRAINT pk, ALTER a DROP NOT NULL` can
succeed for ordinary keys; reversing those actions fails atomically. Existing
transaction, savepoint and read-only admission boundaries are unchanged.

Plain LIKE copies NOT NULL but not the primary key. INCLUDING INDEXES or ALL
copies the primary kind with destination names, fresh identities, remapped keys
and actual key characteristics, never source rows or roots. Multiple explicit/
copied primary keys fail42P16 without a source position. Index descriptor comments
copy under INCLUDING COMMENTS; SQL COMMENT ON INDEX remains PostgreSQL-only,
with a native package-function copy counterpart rather than invented SQL support.
Dump emits named NOT NULL and the primary declaration once in the owning CREATE;
Restore rebuilds and enforces populated persistent groups in isolated staging.
Failed duplicate/NULL replay leaves the destination and held rows unchanged;
existing TEMP exclusion and retained TEMP groups remain intact.

ALTER ADD PRIMARY KEY, ADD USING INDEX, ADD COLUMN PRIMARY KEY, RENAME CONSTRAINT,
deferrability (including NOT DEFERRABLE/INITIALLY), WITHOUT OVERLAPS and primary
index parameter tails are explicitly unsupported. Malformed full input still
fails42601 before deferred capability diagnostics or prefix effects.
Foreign keys and physical PostgreSQL index/WAL/server mechanisms remain external.

Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferencePrimaryKeyFeatureSmoke$' VERBOSE=0`;
use the identical RUN with `REFERENCE=1` for pinned PostgreSQL18.0.

## Exclusion constraints — change note

Table elements and existing ALTER TABLE ADD CONSTRAINT accept
`[CONSTRAINT name] EXCLUDE [USING btree|hash] (key WITH operator, ...)`,
with the existing immutable index-key, class/collation and property grammar.
Only commutative original-type equality operators belonging to the selected owned
scalar family are admitted. Binary-compatible class input alone is not membership.
B-tree supports composite keys, ordering and INCLUDE payload; HASH retains its
existing single-key/no-INCLUDE limits. Optional `WHERE (predicate)` is TRUE-only
and runs before keys, including for the constraint-local HASH exception. Ordinary
HASH CREATE INDEX WHERE remains native0A000. Any NULL key makes a pair nonconflicting.
INCLUDE never participates in the exclusion operator vector.

Each definition owns a distinct constraint OID and real nonunique integrity index.
Existing pg_constraint exposes contype x; pg_get_constraintdef renders the current
symbolic keys/operators/properties. Internal index metadata sets IndIsExclusion,
not IndIsUnique, and IndImmediate reflects definition-time deferrability. This
does not add SQL pg_index or condeferrable/condeferred columns to native projections.
Exclusion keys never strengthen KEY SHARE/NO KEY UPDATE row-lock eligibility.

DEFERRABLE and INITIALLY IMMEDIATE/DEFERRED are supported independently of UNIQUE
deferral and transaction READ DEFERRABLE. Immediate constraints check statement-final
due events; deferred events run at SET CONSTRAINTS IMMEDIATE, implicit completion,
SQL/Go COMMIT or PREPARE. Events are enlisted only by actual potential conflicts:
an uncontested first insertion has no synthetic recheck obligation for a later peer.
Savepoints restore immutable event/mode/projection snapshots; repair or removal of
the actual row/version retires its obligation. Prepared owners retain the same
dirty ticket until COMMIT/ROLLBACK PREPARED. Shared completion resources own waits,
deadlocks and cancellation, while ordinary reads retain their MVCC view.

Omitted/named ON CONFLICT DO NOTHING selects supported nondeferrable exclusions;
unique column/expression inference does not infer them. Selected deferrable
arbiters fail55000 only when a row is demanded; zero-row INSERT succeeds.
Unselected conflicts still fail23P01 with no partial effects.
Build errors use `could not create exclusion constraint`; runtime errors use
`conflicting key value violates exclusion constraint`. Details disclose both keys
only with table SELECT or SELECT on every simple key column; expression keys require
table SELECT. Ownership does not bypass revoked SELECT. Unreadable runtime/build
details are `Key conflicts with existing key.` / `Key conflicts exist.`.

ALTER ADD validates populated/private final rows atomically. DROP CONSTRAINT
retires its owned index; direct DROP INDEX retains the existing dependency error.
Supported standalone constraint/backing-index renames preserve identity and update
both names. Table/column rename and schema move retain the closure; affected TYPE
remints and validates it, while key/include/predicate column removal retires it.
Existing transactional CREATE/rename boundaries are not widened.
Native Dump emits the constraint once, Restore validates isolated staging, and
Reset/foreign/same-owner replacement reowns typed providers and remints actual roots
without changing retained TEMP objects. Transient modes/events are not serialized.
GiST/SP-GiST/range/extension providers, physical indexes/WAL and PostgreSQL server
pg_dump remain outside this native capability.

See [design](design/PG18-DDL-EXCLUSION.md). Shared feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceExclusionFeatureSmoke$' VERBOSE=0`;
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceExclusionFeatureSmoke$' VERBOSE=0`.

## UNIQUE constraints — change note

CREATE TABLE supports `[CONSTRAINT name] UNIQUE [NULLS [NOT] DISTINCT]`
on a column, and `[CONSTRAINT name] UNIQUE [NULLS [NOT] DISTINCT] (a, b, ...)`
as a table element. Table elements can precede their columns. Keys are ordered
lists of 1–32 distinct ordinary attributes. Missing/repeated attributes fail
42703/42701 at the declaration token; an empty key list is a syntax error.
Default and explicit NULLS DISTINCT allow any key containing NULL to repeat;
NULLS NOT DISTINCT compares NULL components as equal.

Equal ordered keys and equal NULL policy among explicit CREATE declarations
collapse into one retained definition. A later explicit name transfers onto an
unnamed retained definition; otherwise the first explicit name wins. Independent
LIKE copies do not participate in this merge. Reversed key order and a different
NULL policy are separate constraints. Generated `table_columns_key` names use UTF8-safe 63-byte
identifier clipping and numbered suffixes. Backing relations share the ordinary
relation namespace; collisions with table or index names fail42P07, while
collisions with existing CHECK/NOT NULL names on the table fail42710.
Generated names see existing namespace objects and previously retained backing
indices, not future explicit UNIQUE names. Consequently,
`CREATE TABLE t(a INT UNIQUE,b INT CONSTRAINT t_a_key UNIQUE)` fails42P07
atomically, while reversing those declarations selects `t_a_key1` for `a`.
Redundancy/name transfer runs before this ordered generation.

Each definition has a distinct generated constraint OID and owns a real logical
B-tree index relation with ordered stable attributes, table owner and namespace.
`pg_constraint` projects contype `u`, the parent OID and NULL conbin;
`pg_class` projects the backing relation as relkind `i`. Both signatures of
`pg_get_constraintdef` return the canonical suffix with current key names.
Typed comparison, not output text, defines uniqueness: numeric scale, BPCHAR
padding, NaN, signed zero and temporal equivalents follow the registered type
providers. INSERT VALUES/defaults/query sources and UPDATE check each candidate
after NOT NULL/CHECK and before accepting it. An error exposes no partial rows
or RETURNING results. Immediate multirow swaps can therefore fail even when the
desired final set would be unique.

Renaming tables/columns and moving schemas preserve identities and backing
names. SET SCHEMA preflights the complete closure, including backing names.
Key TYPE rebuilds affected constraint/index identities, validates converted
private and committed rows, and atomically rejects new duplicates. Unaffected
constraints retain their identities. DROP CONSTRAINT or an included column
removes its backing relation; dropping an unrelated column retains it.
Transactions, savepoints and catalog replacement preserve the same descriptor/root
closure, including retained TEMP table/owned-sequence/index lifetimes.
Dump writes canonical named persistent UNIQUE declarations once;
Restore reconstructs and validates their backing B-trees in isolated staging.
Duplicate-write diagnostics disclose key values only with table SELECT or SELECT
on every key column. The same rule applies to TYPE-build failures even for the
table owner: unreadable build keys yield `Duplicate keys exist.`, whereas unreadable
runtime duplicates omit Detail. Ownership does not replace revoked SELECT rights.

ALTER ADD UNIQUE, deferrability (including explicit NOT DEFERRABLE), RENAME
CONSTRAINT and FK remain unsupported. Standalone CREATE UNIQUE INDEX reuses
the same enforcing B-tree authority without creating a constraint attachment.
It supports simple/composite keys and trailing NULLS DISTINCT/NOT DISTINCT:
all key components must compare equal, and DISTINCT bypasses conflicts whenever
any component is NULL. Directions/NULL ordering do not change equality; INCLUDE
is payload only. Failed builds, multirow writes and TYPE conversion are atomic.
Same-table immutable Boolean WHERE predicates qualify standalone UNIQUE members:
FALSE/NULL rows remain stored but do not claim or compare unique keys. This also
filters private/staged rows and accepted prefixes; member leave/delete retains
its old-key reservation until outcome. Predicate-only TYPE validates final
rebound membership with projected own writes/deletes before private installation.
Only effective nonpartial unique keys supply strong UPDATE locks/history.
Analyzed standalone Boolean TRUE becomes no predicate/no WHERE; execution-folded
logical/comparison predicates retain partial status and original dependencies.
Ordinary unique expressions and BPCHAR classes on TEXT/VARCHAR remain explicit0A000,
while concurrent UNIQUE admits them through the same tuple authority.
Ordinary nonunique support is unchanged. Standalone rename preserves identity,
DROP frees enforcement, and Dump/Restore emits/rebuilds the actual index once
after parent data. Duplicate-build key disclosure uses the same current SELECT
authority as TYPE/runtime failures, including owners with revoked SELECT.
UNIQUE does not enable index-planner or physical PostgreSQL storage claims.
Partial predicates remain B-tree-only. Ordinary nonunique HASH support is
unchanged and does not admit WHERE, including syntactic WHERE TRUE.
Permanent CREATE TABLE remains excluded in explicit transactions; owner-TEMP
CREATE/DROP and rollback use the ordinary private catalog/storage publisher.
The corresponding PostgreSQL transaction/rollback control is independent.
Concurrent standalone UNIQUE indexes are independently admitted without a parent
constraint or fabricated constraint identity; see [concurrent creation](design/PG18-INDEX-CONCURRENT.md).
See [the design](design/PG18-DDL-UNIQUE.md) and
[transaction locking](TRANSACTIONS.md#row-locks--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUniqueFeatureSmoke$'`.
Full shared suite: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUnique'`;
the same selectors with `make quickcheck REFERENCE=1 PACKAGES=./reference` run the pinned PostgreSQL oracle.

## NOT NULL constraints

Both column `a INT [CONSTRAINT nn] NOT NULL` and table-form
`[CONSTRAINT nn] NOT NULL a` are supported (the table form has no parentheses).
Table constraints can precede their columns. `NULL`, including named NULL, is
nullable syntax, not a stored constraint. Repeated compatible declarations
merge into one identity; an explicit name replaces a pending generated name.
Conflicting NULL/NOT NULL declarations within one column definition fail
`42601` at the conflicting token. Column-local declarations are validated before
table constraints: differently named column NOT NULL duplicates reproduce the
pinned PostgreSQL 18.0 `XX000`, even when a table constraint precedes the column.
Table-level NOT NULL can enforce a column declared NULL in either element order;
only NOT NULL identities participate in that merge. Different names in that
merge use `42601`. Duplicate explicit names on different columns fail `42710`.
Named/unnamed CREATE table constraints targeting a system column, such as ctid,
fail `0A000`, `cannot add not-null constraint on system column "ctid"`, rather
than reporting a missing ordinary column.

An unnamed constraint gets a PostgreSQL-style UTF8-safe 63-byte
`table_column_not_null` name, with numeric suffixes avoiding every constraint
name in its namespace, including preceding nested CREATE SCHEMA tables and
already-selected names in the current command. Allocation follows the original
column/table/LIKE element order after merging later declarations for the same
column; future explicit names are not reserved. For example,
`CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)` fails
`23505` for `pg_constraint_conrelid_contypid_conname_index`, with Schema
`pg_catalog`, Table `pg_constraint`, and Constraint equal to that index name.
Detail is `Key (conrelid, contypid, conname)=(OID, 0, t_a_not_null) already exists.`,
where OID is the actual locally allocated candidate relation ID, not a fixed
cross-server value. No table is published. Reversing these column declarations
succeeds and generates `t_a_not_null1` for `a`.
Explicit names are relation-local and can be reused on another table. Names
stay attached to stable attribute identities through schema renames and dense
DROP COLUMN compaction. DROP COLUMN removes only its own NOT NULL identity.
LIKE copies the names onto fresh destination attributes; source ALTER/DROP
never weakens the clone.

Single-action SET/DROP NOT NULL and ADD/DROP CONSTRAINT use owner-authorized
ACCESS EXCLUSIVE execution; Prepare has no locks, validation scans, notices or
name allocation. SET/ADD validates visible committed rows first. Old NULL gives
`23502`, `column "a" of relation "t" contains null values`, with schema/table/
column fields and no failing-row Detail or Constraint field. INSERT NULL gives
`23502`, `null value in column "a" of relation "t" violates not-null constraint`,
with a failing-row Detail and the same object fields even for named constraints.
Multirow failure publishes no rows. Failed ALTER leaves descriptor, names,
nullability and data intact; successful ALTER changes metadata only, retaining
row storage and immutable snapshots.

Compatible repeated SET/ADD preserves the original name and is a count-zero
no-op. DROP NOT NULL is also a no-op when already nullable. DROP CONSTRAINT
removes that identity and makes its column nullable. Missing names give `42704`;
IF EXISTS emits `00000` NOTICE `constraint "name" of relation "t" does not exist,
skipping`, after authorization, without allocating a revision. Every selected
utility returns zero affected rows. Retained prepared SELECT and parameterized
INSERT handles use current nullability across SET/ADD/DROP without a
cached-result-type failure: bound nil fails while enforced and succeeds after
DROP. Native metadata reports known nullability and INT4's int64 ScanType;
the pinned pq adapter reports int32 ScanType but no Nullable capability.
Table/schema owner distinctions, namespace USAGE and final
context/session/epoch/revision/row-length fences still apply.

Dump emits canonical quoted `CONSTRAINT name NOT NULL` declarations, including
generated names, not ALTER history. Isolated Restore uses the common CREATE/
INSERT binder, preserving names/data/nullability/ownership/comments; failed
declarations or NULL insertion preserve the target. Explicit-ACL Dump rejection
and ordinary context/I/O errors are unchanged.

CREATE inheritance merges inheritable NOT NULL by column (see above). NOT NULL's
NOT VALID, VALIDATE and NO INHERIT remain explicit boundaries, not successful no-ops.
Scalar UPDATE enforces NOT NULL and CHECK after destination coercion.
Valid INHERITS lists create the graph above; malformed/repeated clauses and extra
tails remain42601 complete-input rejections, with no earlier script prefix effects.
Foreign keys, foreign-table providers, unowned access methods and unrelated ALTER
actions remain excluded engine capabilities; supported PRIMARY KEY declarations
do not propagate their uniqueness to inheritance children.
Concurrent creation inherits the supported B-tree and nonunique hash methods.
Selected ALTER COLUMN TYPE/SET/DROP NOT NULL, ADD NOT NULL, DROP CONSTRAINT and
DROP COLUMN compose atomic comma-separated lists in implicit and explicit
transactions. PostgreSQL pass order is DROP, TYPE, original SET, transformed
ADD. All TYPE expressions run in action order against each statement-original
tuple, including dropped source columns. Dependent CHECK definitions are
reparsed against the completed TYPE descriptor before row conversion. Each
completed row passes final NOT NULL checks in physical-column order, then
affected CHECKs in TYPE discovery/declaration order, before the next row is
converted. Multicolumn CHECKs enter that queue once, not once per changed
column. Defaults are independently coerced; dependent views block even same-type
changes. Fresh grant-time live scans include own
writes/deletions even in RR, and ignore invalid dead predecessors. Ordinary RR
queries retain their snapshot; the TYPE owner's private queries instead see its
captured live rewrite. Savepoints restore descriptors, storage and own DML
together. Pure nullability changes never rewrite stored values. Native held
eager results remain immutable; peer future statements retain the native
replacement fence rather than simulating PostgreSQL's physical rewrite MVCC.
Shared tests witness inherited OR nullability and inherited-drop rejection;
PostgreSQL-only phases retain NOT NULL NO INHERIT and deferred validation.
CHECK unknown acceptance and leaf UPDATE enforcement are shared witnesses.

Public feature smokes:

```sh
make quickcheck PACKAGES=./smoke RUN='^TestNotNullSmoke$' VERBOSE=0
make quickcheck PACKAGES=./smoke RUN='^TestNotNullControlsSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(NotNull.*|AlterNullability.*)$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(NotNull.*|AlterNullability.*)$' VERBOSE=0
```

## Identity-preserving table and column renames

`ALTER TABLE [IF EXISTS] t RENAME [COLUMN] old TO new` and
`ALTER TABLE [IF EXISTS] t RENAME TO new` relabel existing objects without moving
or rewriting rows. Both table and column targets accept `t*`, `ONLY t` and
`ONLY(t)` for standalone tables; these markers do not add hierarchy semantics.
Destinations are one identifier, not schema-qualified. Collisions, including
renaming to the current name, fail atomically (`42701` column, `42P07` relation).
Missing columns fail
`42703`, `column "old" does not exist`; fixed system columns cannot be renamed.
New names resolve immediately and old names fail with undefined-name diagnostics.

Both forms require the table owner or effective superuser, not merely the schema
owner. Table rename additionally requires CREATE on its containing schema;
column rename does not. Execution acquires ACCESS EXCLUSIVE with the existing
lock timeout, cancellation, grant-time identity and publication fences. Prepare
does not resolve targets, authorize, lock, emit notices or mutate data. IF EXISTS
applies only to the relation: missing targets (including absent qualified schemas)
return count zero and one `00000` NOTICE naming the unqualified relation.
It does not suppress missing-column or collision errors.

Relation OID/namespace and sparse attribute/origin identities remain stable;
only the immutable definition revision and selected label/name change. Row
storage, NOT NULL constraint names and attribute links, ownership, ACLs and
table/column comments remain attached. Retained Rows preserve original labels
and values. Retained prepared SQL is reanalyzed, not textually rewritten:
renamed explicit columns fail `42703`, old table names fail `42P01` with the
original relation-name character Position (15 in `SELECT * FROM t`), and a
renamed output label in SELECT star fails `0A000`,
`cached plan must not change result type`. Unchanged projections and omitted-target
INSERT survive column relabeling; fresh prepares expose current result labels.

Dump serializes current qualified CREATE/INSERT/COMMENT names and unchanged
named NOT NULL constraints, never ALTER history. Reset/Restore round trips values,
metadata and comments through existing native controls; explicit ACL serialization
remains rejected rather than silently losing grants. These are process-local
controls, not pg_dump, physical storage durability or network protocol support.
Stored CHECK constraints and supported views have real descriptors/dependencies;
views continue to reference stable relation/attribute identities through supported
renames. B-tree/hash keys also retain stable attribute identities, and Dump
renders current parent/key names without renaming the index itself. User table/view
row codecs retain stable identities through supported renames and moves.
Foreign keys and broader transactional DDL remain excluded; graph column RENAME is protected.
Native explicit-transaction table/column RENAME and RENAME CONSTRAINT remain unsupported.

```sh
make quickcheck PACKAGES=./smoke RUN='^TestAlterRenameSmoke$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceAlterRename.*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterRename.*$' VERBOSE=0
```

## Column removal and selected action lists

`ALTER TABLE t DROP [COLUMN] [IF EXISTS] name [RESTRICT | CASCADE]`
removes one live column and its stored cells atomically. Ordinary `t`, `t*`,
`ONLY t` and `ONLY(t)` operate on supported standalone tables. DROP COLUMN may
participate in selected TYPE/nullability/DROP COLUMN/constraint lists in explicit
transactions. Pass order is DROP, TYPE, SET NOT NULL, ADD NOT NULL. Mixing these
with standalone DEFAULT, SET SCHEMA or rename is unsupported. Graph shape changes
and partition propagation remain unsupported.
The table's effective owner or superuser is required; unlike DROP TABLE, schema
ownership alone does not authorize ALTER. Execution takes ACCESS EXCLUSIVE and
waits for existing readers/writers; utility Prepare is metadata-only. Cancellation
removes a waiting request without changing the table. A committed preceding INSERT
is retained, and later INSERTs use the new dense visible shape.
Queued SELECT, INSERT and subsequent DROP bind the current definition for the
exact originally acquired relation reference after their lock grants. A definition
revision alone is not replacement; actual removal/drop-recreate or Reset/Restore
still fences the old identity, without substituting another table by name.

Surviving columns keep their catalog attribute/origin identities; visible result
slots compact without renumbering those identities. Retained Rows keep old values
and metadata.
Surviving column grants and role-dependency diagnostics resolve those stable
attribute identities back to live column names, not dense slot numbers.
New direct/prepared queries omit the dropped column. Previously
prepared explicit survivor queries rebind successfully; dropped-column queries
fail `42703`, and a changed-width prepared star fails the existing `0A000`,
`cached plan must not change result type` signature boundary. DROP COLUMN,
including mixed nullability lists, participates in the existing private catalog
overlay and combined row/catalog Commit. Savepoint and full rollback restore
the prior width, values and stable identities; no public prefix is installed.
Missing expression-column diagnostics interpolate decoded quoted identifiers
literally, including embedded quotes/backslashes, with character-based positions
when direct or retained/new prepared queries rebind after DROP.

The supported dependent set includes named NOT NULL constraints, defaults,
column ACL/comments, owner-local CHECKs and supported view dependencies.
DROP removes local CHECKs referencing the selected stable attribute under both
RESTRICT and CASCADE, while survivor expressions retain attributes and remap
dense evaluation slots. Independent columns/tables and plain LIKE clones survive.
Supported inbound view dependencies retain the existing RESTRICT/CASCADE policy.
Canonical Dump renders current names and survivor metadata; Reset/Restore
preserves definitions/data even though replay allocates fresh attribute IDs.
B-tree/hash indexes mentioning a dropped attribute, including expression dependencies, are removed automatically,
even with RESTRICT; unaffected indexes survive dense-slot compaction. FK and
partition dependencies remain unsupported; graph column removal is protected.

Missing columns fail `42703`, `column "missing" of relation "t" does not exist`;
IF EXISTS emits a `00000` NOTICE ending `, skipping` with no descriptor or row
change, after ownership checks. Fixed system names ctid/xmin/cmin/xmax/cmax/tableoid
fail `0A000`, `cannot drop system column "ctid"`, even with IF EXISTS. Modern `oid`
is an ordinary user column. All failures leave the original shape and data intact.

Dump serializes the final live CREATE/INSERT shape, never ALTER history. Dropping
every column retains row multiplicity: CREATE TABLE `()` is followed by one
canonical `INSERT INTO ... DEFAULT VALUES;` per empty row. Restore and public
INSERT share this syntax and execution path; nonzero-column relations use their
declared defaults or typed NULL. `VALUES ()` remains a syntax error.
ALTER TABLE is not admitted to Restore scripts.
Explicit ACL Dump rejection remains unchanged.

Owner-bearing feature controls exercise an ordinary owner of both a nonpublic
schema and its table, direct/prepared DROP, canonical owner directives, restored
owner authority and replay of the same dump on PostgreSQL. A separate distinct
schema/table-owner Restore fixture checks preserved ownership and schema-owner
denial after compaction. An ordinary distinct table owner still needs namespace
USAGE for qualified public SQL: native GRANT is not added, and Dump does not
silently discard explicit ACLs. PostgreSQL-only fixture grants provide its real
destination authority for replaying the distinct-owner dump.

Executable public feature witnesses (Go test reports actual pass/failure):

```sh
make quickcheck PACKAGES=./smoke RUN='^TestAlterDropColumn(Smoke|ZeroColumnControlsSmoke)$' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceAlterDropColumn.*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterDropColumn.*$' VERBOSE=0
```

Duplicate-relation `42P07` errors preserve decoded names inside literal outer
quotes, including embedded quotes and backslashes, both for an existing target
and for a competing publication. Failed CREATE preserves the winning definition
and rows; utility preparation succeeds before the execution-time failure.

Executable feature proof (Go test reports actual assertions):

```sh
make quickcheck PACKAGES='./internal/parser ./internal/engine' RUN='^TestTableCore' VERBOSE=0
make quickcheck PACKAGES=./internal/engine RUN='^TestCatalogDrop' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceTableCore.*$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTableCore.*$' VERBOSE=0
make quickcheck PACKAGES=./smoke RUN='^TestTableCoreSmoke$' VERBOSE=0
```

The former root SQL-interaction definitions now live in the external dual-mode
reference package. See [the complete migrated selection](DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
for all table/default/nullability definitions, including held cursors, native
controls and explicit backend boundaries. Broad family reference selections above
also retain genuine tagged oracle-only complements; use the exact development
selector for a tagged Docker-free `-reference=false` run.

## CHECK constraints — change note

```sql
CREATE TABLE checked (
  a INT DEFAULT 1 CONSTRAINT only_one CHECK (a=1) NO INHERIT,
  b INT,
  CONSTRAINT ordered CHECK (a<b)
);
```

Column/table CHECKs bind the complete new row, including later columns.
`a`, `checked.a` and `public.checked.a` resolve the same stable attribute.
Every supported scalar expression uses the existing binder and providers.
TRUE or NULL passes; FALSE rejects with `23514`, the table/constraint identity
and PostgreSQL English failing-row Detail. CHECK does not declare NOT NULL:
`CHECK (a IS NOT NULL)` still leaves Nullable metadata true.

For INSERT/UPDATE, NOT NULL runs first, then CHECKs in alphabetical constraint-name
order. All INSERT routes and UPDATE enforce after defaults/coercions and completed
simultaneous assignments, before publishing any row write. Failed multirow
statements retain original rows and consume the existing transaction abort/
savepoint policy. Supported volatile scalar effects are evaluated once when
reached and are not rewound by row-write rollback.
Comment inquiries (`col_description`, either `obj_description` signature and
`shobj_description`) share the immutable statement-owned catalog/role comment
snapshot on every INSERT route and UPDATE. A later statement sees committed comment
changes; capture is not repeated per row or when a query source opens. These
functions remain excluded from DEFAULT. Table/column COMMENT in explicit transactions
retains its existing unsupported boundary; PostgreSQL private-comment behavior is
independently covered as a release-only control.

Names use distinct referenced columns rather than syntactic placement:
one attribute yields `table_column_check`, otherwise `table_check`, with
namespace-aware suffixing and UTF8-safe63-byte clipping. Names remain unchanged
on rename. Named table clauses, actual scalar trees and NO INHERIT survive
native Dump/Restore; NO INHERIT CHECKs are excluded from child inheritance.
DROP CONSTRAINT removes a CHECK. DROP COLUMN removes local dependent CHECKs
and remaps survivors. Plain LIKE copies shape/NOT NULL, not CHECK.
CHECK removal composes with the selected atomic ALTER action lists. Inside an
explicit transaction, successful removal and column compaction affect only the
private descriptor/row roots until Commit; Rollback and savepoints restore
CHECK enforcement together with the original layout and DML. Peer operations
retain the existing ACCESS EXCLUSIVE wait rules.

ALTER COLUMN TYPE reparses each surviving affected original CHECK definition
against the final destination usages and dense layout. Incompatible operators
fail even on empty/all-NULL tables; an incompatible stored default fails first.
Identity TYPE and same-type USING still revalidate their affected constraints.
TYPE's queue follows TYPE action order and retained CHECK declaration order,
deduplicating multicolumn dependencies, without changing ordinary DML's
alphabetical order. Each final transformed row passes NOT NULL then this CHECK
queue before conversion begins on the next row. FALSE returns `23514`,
`check constraint "NAME" of relation "TABLE" is violated by some row`, with
Schema/Table/Constraint but no failing-row Detail; evaluator errors retain
their own diagnostic. Reparse errors have no synthetic SELECT source Position.
Original CHECK expressions, data and metadata survive failed TYPE; successful
TYPE publishes rebound expressions/manifests with converted values atomically.
Transactions/savepoints and native Dump/Restore retain that final checked
definition, NO INHERIT and future DML enforcement, never TYPE history.

Persistent CHECK creation holds each referenced relation with ACCESS SHARE
through its single catalog publication; sequence DROP takes ACCESS EXCLUSIVE
and scans the grant-time dependent set. Current-root publication also fences
new external CHECK edges against provider removal, including lower-level catalog
stages. This is a native no-orphan safety boundary, not identical PostgreSQL
concurrency scheduling: pinned PostgreSQL 18.0 permits CREATE referencing a
sequence while another session's DROP remains private. Native CREATE instead
waits; rollback permits the original provider identity, while commit rejects
its disappearance without publishing a table fragment. Cancellation releases
the contender's holds through the existing transaction ticket.
Restrictive sequence diagnostics merge every supported CHECK and view edge by
relation creation address, with CHECK declaration ordinals within each owner.
They retain complete ordered Detail, including transitive view dependencies;
alphabetical row enforcement remains independent. Declaration ordinals are
nonnegative and unique per table; dropping constraints or columns may leave gaps.

Canonical Dump replays tables and views in common creation-address order among
objects whose sources are ready, preserving ordinary SQL-created multi-table and
mixed CHECK/view diagnostics across Restore. Table data and table/column comments
remain with their CREATE; owner directives switch as needed. The incumbent view
topology takes precedence when native replacement remaps a source address above
its dependent: replay remains executable, but that exceptional remapped catalog
does not promise the original allocator-relative diagnostic ordering. No separate
persistent ordering authority is maintained.

Sequence CREATE/options precede table definitions in Dump so CHECK bindings can
resolve real providers; ownership and captured setval follow table data.
Restore genuinely reevaluates volatile CHECKs while inserting staged rows.
History-dependent conditions may therefore reject Restore, preserving the
destination; no CHECK bypass or fake sequence identity is used.

Parameters/aggregate/outside-row references fail before table publication.
ctid/xmin/cmin/xmax/cmax are forbidden CHECK system references; PostgreSQL permits
tableoid but native system-column projection remains outside this surface.
Subqueries/windows/SRFs, other CHECK modifiers, ALTER ADD CHECK
and VALIDATE are not supported. The shared row validator is available to later
existing-data validation work; this change does not implement that traversal.

Public feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCheckSmoke$' VERBOSE=0`.
The 31 root CHECK interaction tests now live as 38 merged/split dual-mode
definitions in `reference/`. See the [complete CHECK selector](DEVELOPMENT.md#dual-mode-check-constraint-interactions)
for Docker-free native and pinned PostgreSQL runs, including held frames,
dependency ordering, cancellation and actual native replay controls.


## Ordinary logical index maintenance

CREATE INDEX builds immutable ordered B-tree entries or equality hash buckets
from committed live rows under Share admission. INSERT VALUES/SELECT/default/
identity/CHECK writes, UPDATE and DELETE maintain roots at the same Finish/commit
boundary as rows. Duplicate keys retain distinct RowID/VersionID membership.
B-tree NULL entries are retained; hash NULL keys are omitted. Hash is one key
without INCLUDE/ordering, and stores captured physical positions for safe candidate
translation, never a full-table row-identity recovery map. Its narrow current-view
strict equality route keeps the original SQL predicate and ordinary fallbacks.
INCLUDE attributes are stable parent IDs in metadata, separate from logical keys.
The total key+INCLUDE limit is 32, repetitions count, and included types need storage
but no ordering provider. Payload-only updates never introduce payload comparisons
or change uniqueness equality. Explicit ordering queries retain table/query Usage,
not an index's collation/class comparator or an unordered traversal guarantee.
Failed multirow statements, rollback and savepoint recovery publish no partial
index effects. Current-only roots are not substitutes for historical/own-write
query visibility.
Expression keys retain both original analyzed definitions for dependencies and
shared simplified execution trees for computation. INSERT evaluates new derived
keys before installing either committed rows or explicit-transaction private
rows: a late failing batch fails at Execute, not COMMIT. Savepoint rollback can
recover the transaction without retaining any rows from the failed statement.
Failed evaluation leaves predecessor roots and parent rows intact. Query matching
expressions still use ordinary snapshots, not current-only index membership.
Real constraint-backed UNIQUE indexes retain simple-slot enforcing keys.
Ordinary standalone UNIQUE/NND creation keeps its bounded simple-key/provider
admission policy. Standalone indexes created concurrently, or reconstructed by
authenticated Restore, may enforce computed/provider-aware tuples through the
same shared extraction, comparison and claim paths; INCLUDE is never an enforcing key.

Renames preserve stable key/included attributes and expression dependencies.
Dependent TYPE reconstructs the whole definition using old parent defaults:
old-default classes are rebound for the new type; nondefault classes and actual
collation overrides must remain compatible. Original expression definitions
reparse against the final shape and rebuild entries. Included TYPE requires only
storage validity. Unrelated DROP remaps slots; dropping a key dependency or payload
removes its index. SET SCHEMA moves indexes with table/owned sequences as one
collision-checked publication. DROP TABLE/schema CASCADE leave no index/root/name
orphans. Dump captures actual options/current names; Restore/replacement rebuild
key-only roots with destination identities.
See [B-tree design](design/PG18-INDEX-BTREE.md) and [clause design](design/PG18-INDEX-CLAUSES.md).
See [hash design](design/PG18-INDEX-HASH.md) for method-specific providers, captured
prefix/generation proofs, immutable touched-bucket publication and no range/order/
NULL-search claim. Native Dump/Restore and real PG replay preserve USING hash
without B-tree direction decorations; valid physical options remain exclusions.

Ordinary index RENAME preserves its substantive root and stable parent/key
identities; a UNIQUE backing rename atomically changes its parent constraint
record and current23505 diagnostic name without rebuilding the root. DROP INDEX
groups preflight kinds/ownership/dependencies and deduplicate addresses before
one publication; a backing index is internally owned and cannot be dropped
directly, including with CASCADE.

REINDEX INDEX rebuilds exactly the selected real root; REINDEX TABLE rebuilds all
implemented roots atomically from committed live versions, including expression
and UNIQUE keys. It does not change rows, descriptor/key/OID/version identities or
unselected sibling roots. No-index TABLE is a NOTICE/no-op, not a synthetic
definition publication. Failed waits/builds/final publication preserve all prior
state. Valid physical options remain unsupported rather than simulated.
Internal detached metadata projects complete flags/vectors, retained analyzed
expression lists, actual key collations and central actual method-specific class identities
from one immutable CatalogRead. Total attributes include repeated INCLUDE payload
attributes; class/collation/direction vectors contain keys only. Maintenance
preserves every implemented explicit provider and included payload, including
non-orderable JSON payload. It adds no SQL catalog family.
See [maintenance design](design/PG18-INDEX-MAINTENANCE.md) and
[SQL syntax/boundaries](SQL.md#index-maintenance).



## Stored generated columns

```sql
CREATE TABLE g(a INTEGER, b INTEGER GENERATED ALWAYS AS (a + 1) STORED);
INSERT INTO g(a) VALUES(2); -- b = 3
INSERT INTO g VALUES(4, DEFAULT); -- b = 5
UPDATE g SET a=8 WHERE a=2; -- b = 9
UPDATE g SET b=DEFAULT; -- recomputes from the completed new row
```

Only explicit STORED is supported. PostgreSQL18's omitted kind means VIRTUAL,
not STORED; omitted/VIRTUAL declarations remain native unsupported boundaries.
Expressions use the existing scalar binder, operators, functions and destination
assignment coercion. They may reference plain current-row columns, including
later-declared columns and qualified/quoted names. A plain column named like its
table takes precedence over a bare whole-row reference. References to another
generated column (including self or unreachable CASE arms) remain forbidden.
Aggregate/grouping/window, subquery/set-returning, parameter and nonimmutable
expressions are rejected. The volatility check considers surviving providers
after constant planning.
Mixed TEXT concatenation retains its concrete output provider's volatility:
integer output is eligible, while timestamp/timestamptz and other session- or
catalog-dependent output is not. Physically pruned volatile CASE arms do not
make the surviving expression volatile.
Constant arithmetic errors fail CREATE, while row-dependent arithmetic errors
occur when a write consumes the expression. Existing unconstrained domains
remain eligible; domain CHECK/NOT NULL declarations are not added here.

The generated value is evaluated after all base/default/identity inputs and
simultaneous UPDATE RHS assignments, before NOT NULL, CHECK, uniqueness and
RETURNING. NULL input follows ordinary expression semantics. Any supplied
non-DEFAULT generated value, including NULL, fails428C9 even with no source rows
or no UPDATE matches. Identity OVERRIDING modes cannot bypass this guard.
Errors/cancellation publish no partial statement rows. Reading a stored generated
column does not evaluate its expression or add a SELECT privilege requirement
on its input columns.

Metadata remains physical declared-column metadata. Existing pg_attrdef and
pg_get_expr expose stable definition/attribute ownership, including a generated
NULL expression. Rename and unrelated DROP preserve stable identities and update
deparse names/row slots. SET/DROP DEFAULT on a generated destination are rejected;
changing a referenced base column's type is rejected. DROP base RESTRICT reports
dependent generated columns; CASCADE removes those columns and their definitions.
Generated destination TYPE USING is rejected; destination TYPE without USING
remains a native unsupported boundary (PostgreSQL can widen it).

tableoid alone is the admitted system-column reference in generation expressions.
It retains owning relation identity rather than a cached numeric constant.
Native Dump writes canonical generated declarations and DEFAULT for generated
row cells, including all-generated tables. Restore recomputes normal generated
values and remaps tableoid-dependent values to the final destination owner;
held eager cursors retain original cells and metadata. INCLUDING GENERATED
copies the cooked STORED provider with destination row/tableoid remapping.
Virtual columns, ADD generated and SET/DROP EXPRESSION remain outside scope.

Feature smoke (also run with `REFERENCE=1` for the pinned PostgreSQL oracle):
`make quickcheck PACKAGES=./reference RUN='^TestReferenceGeneratedStored(Smoke|ControlsSmoke)$' VERBOSE=0`.

