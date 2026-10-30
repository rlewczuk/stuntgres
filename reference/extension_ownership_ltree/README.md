# reference/extension_ownership_ltree

Ltree members that survive `ALTER EXTENSION DROP` detachment, foreign
membership and retirement of their original extension: the array, comparison,
hierarchy and path/query routine and operator families, with their native
Dump/Reset/Restore controls. The other ownership families are in
[reference/extension_ownership](../extension_ownership/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the same
oracle options as `reference/extension_ownership`
(`fixture.WithLegacyCryptoProvider()`, `fixture.WithAlterSystemDisabled()`).
The ownership scenario, survivor control and snapshot helpers are shared with
that package through `reference/fixture/extension_ownership_helpers.go`
(`OwnershipScenario`, `OwnershipSurvivorControls`, `OwnershipSnapshot`, ...).

Ltree array survivors use the complete released 1.3 corpus: sixteen routines
and sixteen operators, including all four first-match extractors (`?@>`, `?<@`,
`?~`, `?@`). Serial two-case value inventories each retain one complete nominal,
I/O, array and operator source graph for both destinations under the unchanged
scenario deadline.
Per-operation declaration, owner/ACL and normal-dependency assertions follow
the same disjoint inventory as values; the complete corpus is checked for
each state and destination without repeating every operation in every leaf.
Expected first matches follow input order rather than label order, including nondefault
array lower bounds; misses and strict typed NULL remain NULL. Direct/prepared
calls, held/stored consumers, current owner/ACL, declarations and e/normal edges
survive detachment, foreign membership and lawful origin retirement. Native
same-driver and independent-driver controls use destination-owned recorders and
retain pre-reset rows and their nominal metadata.
Stored consumers record explicit function and operator occurrences independently:
operator implementation dependencies remain on the operator, while genuine
function calls nested in operands retain their own normal edges.
Select the family with `RUN='^TestReferenceExtensionOwnershipLtreeArraySurvivors$'`
in native and `REFERENCE=1` modes.

Ltree comparison survivors retain all nine comparison/hash routines, six
operators and both B-tree/hash class/family support graphs from the complete
94-declaration recipe (97 initial current members including generated arrays).
Three-routine metadata inventories share one source graph across both destinations
using the same ownership scenario/control helpers. Callable coverage is the six
documented operators and genuine
ordering/equality/index routes, not extra direct SQL APIs for C backers.
Detached and foreign final states preserve direct/prepared/held/stored and
strict-NULL behavior after origin retirement. Operators move independently of
their implementing routines, while all nine backing declarations, current
owners, ACLs, e/normal/internal and loose hash-support dependencies remain
observable. Generated arrays retain their internal scalar edge rather than
an independent namespace dependency. Separate B-tree and hash
tables preserve ordered results and collision equality rechecks; native
same/independent controls pair each destination with its recorder and retain
old rows/metadata. Select this family with
`RUN='^TestReferenceExtensionOwnershipLtreeComparisonSurvivors$'` in both modes.

Hierarchy survivors exercise both directly callable `ltree_isparent` and
`ltree_risparent` as well as their reciprocal `@>`/`<@` operators. Bounded
serial relation inventories share their source graph across both destinations
and retain self and empty ancestry, case-sensitive whole labels and non-prefix
misses through direct/prepared, held and stored
calls, including each strict typed-NULL operand. Genuine detachment, ordinary
rename, foreign `uuid-ossp` membership and origin retirement preserve full
logical declarations, current owner/ACL and e/normal edges. Same-driver and
independent-driver native controls use destination-owned recorders, compare
destination declarations and byte-stable redumps, reject partial failed
restores, and retain old row values and metadata.
Select with `RUN='^TestReferenceExtensionOwnershipLtreeHierarchySurvivors$'`
in native and `REFERENCE=1` modes.

Path/query survivors cover all twenty-three operations: nineteen path signatures,
both scalar lquery directions and both scalar ltxtquery directions, with three
concatenation operators and reciprocal `~`/`@` query operators. Bounded ltxtquery
values distinguish unordered labels, Boolean precedence/negation, empty paths,
case/prefix/word modifiers and false matches; each endpoint has strict NULL coverage.
Reciprocal callees and operators are retained only within their own query group.
Before membership mutation the scenarios capture genuine nominal, array, operator
and discovered helper identities and declarations, and prepare direct and stored
consumers. Exact normal edges
from each scalar type to its actual `pg_type.typinput`/`typoutput` OIDs are
independently expected before helper discovery, after detach and foreign attach,
and after origin retirement. Pinned builtin array codecs do not imply such rows.
Each detached or foreign operation partitions its complete value corpus, including
generated strict-NULL operands, into disjoint inventories of at most six values.
Only larger corpora add inventory subtests. Each inventory owns the full genuine
source graph and feeds both real native control destinations under the original
scenario deadline, bounding stored-view work in each catalog dependency projection.
Every inventory retains an independent non-NULL nested-call witness and both
original source row resources. The controls repeat the exact codec edges and compare
destination-relative graphs, including companion namespace, owner, element and
array links, current ACLs and membership, strict NULLs, old rows, byte-stable
redumps and failed-restore atomicity. Consumer normal dependencies distinguish
explicit function calls from operator syntax, including a genuine SQL-function
operand, and rejected RESTRICT drops exercise normal dependencies independently
of the current extension link.
Select with `RUN='^TestReferenceExtensionOwnershipLtreePathQuerySurvivors$'`
in native and `REFERENCE=1` modes.
