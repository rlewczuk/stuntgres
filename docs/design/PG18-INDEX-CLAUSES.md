# PG18-INDEX-CLAUSES: B-tree key clauses and INCLUDE

## Contract and authority

Ordinary CREATE INDEX accepts simple multicolumn B-tree keys and the already-owned
same-table scalar expression keys, ASC/DESC, NULLS FIRST/LAST, owned built-in
COLLATE/operator classes and simple INCLUDE columns.
At least one key is required; the 32-attribute limit counts keys plus INCLUDE,
including repeated attributes. Names and generated labels use the existing
shared relation namespace and key-then-payload naming authority.

The compatibility oracle is the pinned PostgreSQL18.0 UTF8/C fixture documented
in [DEVELOPMENT](../DEVELOPMENT.md), not local development sources or the current
18.6 manual. Public SQL/API witnesses are untagged dual-mode reference scenarios
with independently specified data, metadata and complete diagnostics. The root
recorded initial pinned-oracle expectations and native red before production
implementation; this design does not claim completed production verification.

References: [CREATE INDEX](https://www.postgresql.org/docs/18/sql-createindex.html),
[multicolumn](https://www.postgresql.org/docs/18/indexes-multicolumn.html),
[ordering](https://www.postgresql.org/docs/18/indexes-ordering.html),
[INCLUDE](https://www.postgresql.org/docs/18/indexes-index-only-scans.html),
[collations](https://www.postgresql.org/docs/18/indexes-collations.html),
[classes](https://www.postgresql.org/docs/18/indexes-opclass.html).

## Raw clauses and immutable attributes

The existing parser owns complete-input syntax and character positions. One raw
index-element grammar serves keys and INCLUDE; decorations in INCLUDE remain
syntactically legal so their semantic errors occur at Execute. Parenthesized and
function INCLUDE elements retain expression shape, including schema-qualified
and quoted qualified calls, even `(n)` or unresolved `(missing+1)`; they are
rejected without ordinary expression binding. An explicit
ASC is distinguishable from absent direction. INCLUDE remains an unreserved
identifier, while only one INCLUDE clause can follow the complete key list.
Class options parse signed numeric values and reserved ColLabel names, never
silently ignore parameters or confuse them with a key function call.
Ordinary expression keys preserve the incoming shared binder/evaluator authority;
only included expressions are rejected before binding or bare-column normalization.

Definitions contain stable parent key IDs/options and a separate ordered INCLUDE
ID slice. Descriptor columns/highwater contain all attributes with distinct labels
across both roles. Key-INCLUDE and INCLUDE-INCLUDE duplicates are legal. Payload
Usage stays its valid parent storage Usage; definition membership marks its role,
not an invalid collatable Usage with collation ID zero. PostgreSQL-only inquiry
may observe index INCLUDE attcollation zero; no native pg_attribute/pg_index family
is introduced. Ordinary table result metadata remains table/query Usage.

Logical entries contain **only key values and RowID/VersionID**, with no copied
INCLUDE payload. This avoids unnecessary storage/copying for a feature that does
not promise physical covering scans. Included values are projected from ordinary
selected table views and are valid residual predicates, not search/comparison
keys. Key equality, unique claims, NO KEY UPDATE classification and sort/merge
never inspect payload. Duplicate keys retain distinct live identities. NULL
placement is resolved before direction, so DESC does not invert NULL policy twice.

## Actual owned providers

`internal/sqltype/index_ordering.go` owns finite class identity/family, defaults,
directional compatibility and bound index comparison. Engine namespace lookup
owns qualified/unqualified search_path and USAGE, excluding TEMP for provider
searches. A familiar basename in the wrong schema never grants a provider.

Accepted classes are bool_ops, bytea_ops, char_ops, name_ops, int2_ops, int4_ops,
int8_ops, text_ops, varchar_ops, bpchar_ops, oid_ops, float4_ops, float8_ops,
date_ops, time_ops, timestamp_ops, timestamptz_ops, timetz_ops, interval_ops,
numeric_ops, uuid_ops, text_pattern_ops, varchar_pattern_ops and bpchar_pattern_ops.
VARCHAR defaults to text_ops; REGCLASS defaults to oid_ops; domains flatten to
base defaults while retaining their original logical descriptor in stored values.
Direct REGCLASS table columns retain their existing storage exclusion; public
operator-class witnesses use the already-owned domain-over-REGCLASS storage,
actual non-NULL assignments and text projection, without expanding type admission.

Binary compatibility is directional and finite: text/varchar interoperate and
can use bpchar classes; BPCHAR cannot use text/varchar classes. INT4 can use
OID classes but OID cannot use int4_ops. REGCLASS uses OID semantics. Numeric
widening and NAME-to-TEXT are not binary compatibility. Class admission selects
actual semantics: oid_ops reads INT4 bits as uint32 (1 before -1), and bpchar
classes trim trailing ASCII spaces from text/varchar ('a' and 'a ' are peers).
Adaptation uses immutable inline values without text I/O, storage mutation or
allocation per comparison. All remaining owned comparisons delegate the existing
provider. These rules do not alter ordinary signed SQL ORDER BY or TEXT ordering.

Admitted collations are default100, C950, POSIX951, pg_c_utf8 811 and
pg_unicode_fast6411. They share existing deterministic UTF8 byte/codepoint
comparators but retain distinct identities. Pattern classes use the owned byte
or padding comparator only under these admitted collations; coincident output
is not a promise of pattern-family inequality/search acceleration. Source evidence:
[released casts](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_cast.dat),
[classes](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_opclass.dat),
[pattern comparator](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/varlena.c),
[padding comparator](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/varchar.c),
[builtin locale](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/pg_locale_builtin.c).

NAME[]/domain NAME[] payload needs no comparator; using it as a key with its
PostgreSQL-valid default/array_ops is a truthful native0A000 `index operator class
is unsupported`. Valid unowned collation/method controls likewise succeed on
PostgreSQL beside native capability diagnostics. Unknown names get42704 and
incompatible existing classes/types get42804; no new owning type/locale domain
or generic cast graph is added.

## Diagnostic phases and atomic execution

Prepare retains only syntax/empty utility description. Each Execute freshly
binds current relation shape, role, search_path, keys, includes and providers.
Existing read-only25006, native explicit-transaction0A000, owner-before-Share and
replacement fences remain unchanged. Total33 precedes method/attribute/schema
CREATE validation after existing target/owner acquisition. Keys bind left to
right, then includes. Column lookup precedes simple INCLUDE-decoration rejection;
raw included expressions fail0A000 without expression lookup. Decorations fail
42P17. Known hash capability combinations get actual PostgreSQL diagnostics,
rather than the generic valid-non-btree boundary.

System key diagnostics use released type/class identity only, without adding a
tid/xid/cid descriptor, codec or comparator. Name lookup and type/class checks
precede final system-column prohibition, so a later missing ordinary attribute
can win. xid/cid have no default B-tree class and instead report42704 with its
complete hint. Included system attributes need no default class, but their
decorations are rejected before system prohibition. Focused new signed/reserved
parameter and system-precedence assertions are source-derived until their
root-owned pinned oracle run; they are not claimed observed in the initial oracle.

Binding precedes name collision/IF NOT EXISTS **except class-option provider
validation**, which is later. Fresh int4_ops(foo=1) fails22023 `operator class
int4_ops has no options`; an existing name wins42P07; IF NOT EXISTS succeeds with
exact NOTICE42P07 and leaves its existing definition untouched. This order follows
[REL18 catalog/index.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/index.c)
883-899/1242-1245, not a generic early-return shortcut for invalid columns/classes.

Semantic PostgreSQL18.0 records have absent Position and unrelated fields; raw
syntax has exact character positions. Shared collation namespace lookup is reused
with caller-owned index attribution, preserving column-declaration precedence.
No native PostgreSQL C source provenance is fabricated. Failed or canceled bind,
sort, publication, TYPE or Restore leaves published descriptor/name/root/data
unchanged. Sorting remains error-capable; NULL/tie paths poll cancellation.

## TYPE, lifecycle and controls

TYPE of any dependent key **or INCLUDE** reconstructs the whole index definition
using every key's pre-change parent Usage. A class equal to its old type default
becomes implicit and is rebound to the new type default, even if CREATE explicitly
named it. A collation equal to the old parent collation becomes inherited.
Genuinely nondefault classes and actual overrides remain and must validate.
Thus explicit int4_ops INT-to-TEXT rebuilds with text_ops; nondefault oid_ops
INT-to-TEXT fails42804 atomically. Payload TYPE can normalize an unchanged key's
explicit-default status before a later key TYPE. Rename, unrelated DROP, row
maintenance and schema/owner moves do not trigger normalization.

Dependent same-type key/payload TYPE also reconstructs, with or without identity
USING.
Reconstruction is triggered by the operation's stable attribute dependency,
not Usage inequality or the decision to rewrite stored rows. The existing ALTER
owner passes its recorded transformations and parent identity into index
evolution; generic catalog validation passes no TYPE trigger.

Simple-key and provider metadata normalize directly in native descriptors.
Affected derived keys retain the existing single TYPE queue: capture the original
PostgreSQL-style definition buffer with its decorations and INCLUDE, reparse it,
and rebind affected original expressions against the final parent. That original
buffer owns generated diagnostic positions; normalization must preserve the
freshly rebound trees rather than replace this authority with metadata-only work.
PostgreSQL grounds it through `RememberIndexForRebuilding` and unconditional
`ATPostAlterTypeCleanup` reparse/drop/recreate in
[tablecmds.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/commands/tablecmds.c),
plus `get_opclass_name`/collation omission in
[ruleutils.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/ruleutils.c).
Same-type reference witnesses independently check logical index OID replacement,
resolved providers and rows on PostgreSQL, and canonical normalized native Dump.
Physical index-storage reuse is separate and is not asserted.
Unrelated DROP remaps stable slots; dropping a key or included ID removes its
index. Untouched parents skip evolution before allocation/binding. Existing
catalog/row publication owners retain immutable old roots and sole authority.

Dump serializes actual current names/options after parents: explicit providers
use quoteIdentifier for every component, e.g. COLLATE "pg_catalog"."C" and
"pg_catalog"."text_pattern_ops"; resolved directions/NULL policies precede
INCLUDE current names. Reconstructed implicit defaults do not emit obsolete
explicit classes. Self/fresh foreign Restore and replacement build destination
key-only roots with reminted identities, never source descriptor slots/row IDs.
PostgreSQL replay consumes the **actual native Dump stream** and asserts independent
data/metadata and subsequent DML. Late invalid INCLUDE replay preserves the native
destination, and retained TEMP/eager rows keep existing replacement authority.

The already-integrated `pg_get_indexdef` shares the index presentation entrypoint,
not Dump's canonical spelling. Full inquiry uses the existing PostgreSQL renderer,
omits actual default classes and inherited-equivalent collations regardless of
original explicit spelling, and prints nondefault provider identities such as
`varchar_ops` even when their comparison family equals `text_ops`. Pretty mode
shortens a visible parent name; inquiry output has no replay semicolon/newline.
Attribute-only ordinals cover all keys plus INCLUDE: current simple/payload names
or original derived expressions, without ordering/provider decorations. Repeated
payloads retain their positions; out-of-range is empty TEXT, distinct from NULL.
Rendering never normalizes stored flags or evaluates expressions. Held consumers
reacquire actual logical identity after TYPE/Restore and resolve current names;
canonical Dump remains separate evidence of operation-triggered normalization.
Full and attribute-only inquiry and original TYPE-buffer grouping share PostgreSQL's
`func_expr_windowless` classification. Owned ordinary calls and SQL-special
COALESCE/NULLIF/GREATEST/LEAST forms print bare; operator expressions require an
outer index-expression group. Compact operator rendering keeps its inner group
too; pretty rendering omits it. Canonical Dump retains its separate parentheses.
TYPE positions come from the correctly grouped original buffer, not offset fixes.
The index grammar reuses the ordinary conditional-call production for these
unparenthesized SQL-special forms, retaining ConditionalExpr rather than a named
FunctionExpr in original TYPE reparse. Quoted/qualified ordinary calls keep their
function shape; raw INCLUDE expressions still reject before any binding.

## Boundaries and executable evidence

No standalone CREATE UNIQUE INDEX or ALTER attachment is admitted. Existing table
UNIQUE plus ordinary INCLUDE has public key-only23505 witnesses; a real internal
unique-backed INCLUDE definition uses actual validators/root equality. The
PostgreSQL-only unique-INCLUDE control does not create a native attachment path.
Physical planner/index-only scans, new inquiry catalogs, partial/concurrent
indexes, arbitrary locales/classes, storage parameters/tablespaces and DROP/ALTER
INDEX remain outside scope. Existing expression keys compose their result Usage
with the selected class/collation through the same IndexComparison; no default
comparator fallback bypasses explicit providers. Unordered queries are bags,
never promised index traversal; query sorting retains its selected-view authority.

Root commands, each quickcheck invocation bounded to900 seconds:

```sh
make generate
make quickcheck VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReference(IndexClauses|BtreeIndex|ExpressionIndex|DeparseRelations|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(IndexClauses|BtreeIndex|ExpressionIndex|DeparseRelations|AlterTypeCompatiblePlainIndexBoundary)' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceIndexClausesFeatureSmoke$' VERBOSE=0
```
The identical broad selectors include every clause, B-tree, incoming expression
and DeparseRelations preservation definition plus the compatible TYPE boundary,
not only a named subset. New diagnostics keep original Position46/56/74/105
witnesses and add preceding decorated SQL-special keys at Position86/84/86/83.


Expected smoke: mixed orders z,x,y,w,m,n and n,m,w,y,x,z; prepared INCLUDE
projection; expression key `upper(p)` with C/text_pattern_ops and INCLUDE(a);
independent uppercase projection X,Y,Z; x-to-xx update leaves the covering index's
simple keys unchanged while maintaining the expression key; key-only UNIQUE23505;
relnatts32 and exact54011/no leaked total33; canonical self/foreign Restore and
actual emitted SQL replay retain rows/metadata and allow subsequent DML.
Internal units supplement with provider/default/compatibility/domain matrices,
actual unsigned/padding roots, key-count versus attribute-count, genuine unique
payload equality, whole-index TYPE normalization, immutable merge, cancellation
and private replacement reminting. Root alone executes generation/gates/smoke and
review. Final fullcheck runs after integration under one1800-second deadline;
these prescribed commands are not readiness-based claims of completed checks.
