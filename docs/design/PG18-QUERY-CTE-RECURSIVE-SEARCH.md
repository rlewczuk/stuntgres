# Recursive SEARCH ordering columns

## Scope and semantic authority

SEARCH adds ordering values to the installed recursive CTE producer. It does not
schedule traversal or promise an order without ORDER BY. The authority is
[PostgreSQL 18 WITH search order](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-SEARCH-ORDER),
[composite comparison](https://www.postgresql.org/docs/18/functions-comparisons.html#COMPOSITE-TYPE-COMPARISON),
and [array comparison](https://www.postgresql.org/docs/18/functions-array.html).
Pinned18.6 reference observations own metadata, diagnostics and prepared-result
behavior; local development sources are not the release oracle.

## Analysis and rewrite diagnostic boundaries

Ordinary recursive legality (self placement/count, subqueries, UNION form and
decorations) stays in the initial identity-aware parser pass. Installed
CYCLE-only expandability checks remain there too. SEARCH-bearing definitions,
including SEARCH+CYCLE, defer SELECT-arm expandability until both arms and their
width, overall type and collation have been analyzed. Arm form then precedes
SEARCH key/sequence names, installed CYCLE names and shared cross-name checks.
Only after those names succeed is the top-level recursive-reference rewrite
restriction checked, using the same identity-aware source walker. Parent carries
are projected only after that restriction succeeds. No deferred errors are stored
in recipes, persisted queries or runtime state.

For nonrecursive SEARCH, ordinary body and excessive-alias-width errors precede
`WITH query is not recursive`, with or without the RECURSIVE modifier. Existing
ordinary and CYCLE-only callers retain their original legality boundary.

## Immutable typed recipe and slot ownership

Each recursive definition retains one original `userWidth`. SEARCH occupies that
slot; optional CYCLE mark/path follow it. Without SEARCH, CYCLE follows the
originals directly. A cycle recipe's `markSlot` is an output offset, never another
original-width authority. Outer output is originals, sequence, mark, path.
Definition-body stars and relation alias lists retain original width; explicit
generated references remain typed and available before binding the recursive arm.

The immutable SEARCH recipe retains symbolic syntax, ordered original key slots,
occurrence descriptors and DISTINCT-only equality providers. BFS builds RECORD
(OID2249, typmod−1, outer collation0): first INT8 member `*DEPTH*`, then current
keys with their actual Usage/name/typmod/collation. Anchor depth is zero. DFS
builds _RECORD (OID2287, concrete RECORD element), one-dimensional lower1 ancestry
including the current key record. NULL keys remain fields inside non-NULL records.
Normal typed composite/array output owns quoting and public adapter conversion.
There is no anonymous catalog registration, text-only payload or stringify/reparse.

The installed top-level worktable discovery finds the actual self occurrence
through joins and accumulates left widths. Recursive projections carry all parent
generated slots, even when outer output omits them. Parent SEARCH and CYCLE values
are captured before generated outputs overwrite scratch. Original user expressions
are evaluated once; generated values are filled before tuple identity/publication.
DFS copies required private Value slots once and shares immutable ancestor records.
No preceding-row proxy, generation-wide depth counter, Go recursive stack or row cap
is introduced.

## Comparison and complete UNION identity

BindOrdering admits concrete occurrence RECORD and occurrence record[] through
existing Comparison providers. `OrderingBound` records initialization separately
from a physical index opclass. Scalar opclass identities remain authentic and
allocation-free; occurrence records claim no executable index class. General
arrays, named-composite ordering and index admission remain unchanged.

Existing composite comparison retains per-field collation/provider authority,
demands reached providers before NULL shortcuts, compares NULL fields high and
short-circuits earlier unequal fields. Existing array comparison supplies ancestry
lexicographic/prefix/dimension ordering. JSON can be constructed/emitted under ALL;
only comparison reaching its field errors. No eager SEARCH grouping requirement,
engine-local comparator or hidden tie breaker is installed.

UNION ALL retains no SEARCH equality/hash. DISTINCT retains original queryGrouping
providers plus optional CYCLE mark, with explicit index-to-output-slot mapping.
SEARCH record/path and CYCLE path identity belong to immutable recipes. Hash and
collision equality include every generated component, depth, path record and array
dimension. Equal base tuples with different sequence values are distinct, even
when outer projection omits sequence. Existing CYCLE closing-row/stop rules remain.
Construction, ancestry copying, hashing and collision/provider loops poll context;
current/next frontier scheduling remains the existing iterative producer.

## Reanalysis, remapping, persistence and publication

Public result signatures distinguish PostgreSQL RECORD/_RECORD identity from
internal shape. Rebinding INT4 keys to INT8 preserves raw generated result identity
and seq::text, while a public original INT4→INT8 result still fails0A000. Parameters
retain exact occurrence shape. `sqltype.SameRecordUsage`, expression/catalog validation,
record type mapping and prepared catalog-owner/epoch fences are not relaxed.
Captured Rows retain original descriptor/data and every execution starts fresh.

The existing occurrence mapper memoizes distinct shapes independently of OID,
remaps catalog leaves/member collations, and preserves immutable sources. CTE
identity is memoized before recursive self edges. SEARCH recipes rebuild equality
against destination descriptors. Stored layout validation checks exact original,
recursive carry and output widths/usages, recipe slots and parent projections.
Graph/rewrite/preflight copies retain immutable SEARCH state; it contains no user
expressions or invocation frontier needing another walker/lifetime owner.

Stored SQL renders symbolic SEARCH before CYCLE and excludes generated targets or
hidden carries. Raw anonymous RECORD/record[] persistent outputs reject42P16;
original scalars and seq::text remain legal view/CTAS/MV/INSERT outputs. Native
Dump/Restore persists physical edge tables, ordinary values and symbolic views,
never anonymous type DDL or frontiers. Existing stage/control owners provide
atomic replacement. The CTE producer owns sticky primary errors and close-once;
required successful-path Close failure vetoes publication. StatementScope,
WriteStage and finalRows remain the sole publication owners, with no extra undo
registry or prepared frontier cache.

## Proof placement and bounded exclusions

Public SQL/API witnesses live only in the shared dual-mode reference/query_core
SEARCH bodies. Private parser/type/engine units exercise package components,
shape/identity/cancellation/remap/signature invariants, not a second public-SQL
suite. Exact selectors and smoke commands belong to the owning package READMEs.
A standalone public consumer smoke is session-only, not a tracked throwaway.

No traversal scheduler, general record grouping, JSON equality, general array or
named-composite sorting/indexes, new CYCLE semantics, ROW/ARRAY constructor work,
new prepared validity owner, optimizer or rollback subsystem is included.
