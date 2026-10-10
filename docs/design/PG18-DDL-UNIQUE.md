# PG18-DDL-UNIQUE

## Boundary

CREATE TABLE column/table UNIQUE supports immediate ordered simple keys and
NULLS DISTINCT (default) / NULLS NOT DISTINCT. The backing relation is a real
logical B-tree, not a catalog-only declaration or output-text uniqueness cache.
ALTER ADD UNIQUE, deferrability, RENAME CONSTRAINT and PK/FK remain explicit
capability boundaries. PG18-INDEX-UNIQUE admits standalone simple-key uniqueness
through the same enforcement, not a synthetic constraint attachment.
Concurrent computed/provider-aware standalone uniqueness is specified in
[PG18-INDEX-CONCURRENT](PG18-INDEX-CONCURRENT.md) through the same tuple authority.

## Descriptor and publication closure

A retained table UNIQUE definition contains its own generated definition OID,
name, ordered stable attributes, NULL policy and exact backing ObjectAddress.
The backing relation has a separate relation OID, the parent's owner/namespace,
key schema and reciprocal index definition. Catalog validation checks both
ends. The existing index root representation, sorted typed comparison and common
row publication maintain substantive immutable entries and exact row/version
identities. NULLS DISTINCT skips duplicate checking for any NULL component;
NULLS NOT DISTINCT includes those components.
Backing columns have their own dense stable attributes and matching allocation
high-water, independent of the parent's attribute holes. TYPE/remapping copies
retain this descriptor invariant alongside the real backing identity.

Index definitions now separate `unique` and `nullsNotDistinct` policy from the
optional actual `constraint` attachment. Nonunique indexes can retain their
index-wide NULL policy for truthful deparse without becoming enforcing.
Shared DML bindings enumerate actual immutable catalog index membership and
cache by catalog identity plus parent/ref, not parent pointer alone. Current
roots/ACL authority are recaptured after key waits. Only true attachments
propagate constraint names, prohibit direct DROP, collapse Dump graph nodes or
remint constraint OIDs. Standalone indexes otherwise share root validation,
TYPE/private baselines, diagnostics, key claims/history and native remapping.


CREATE binds all names/attributes and canonicalizes identical ordered key/policy
specifications before allocating retained objects. Explicit name transfer occurs
only while the retained declaration remains unnamed. The existing UTF8-safe
name allocator chooses namespace-safe generated backing names.
Generation sees existing namespace constraints/relations and earlier retained
backing names, never blanket reservation of future explicit UNIQUE names.
The pinned forward-collision, reverse-suffix and redundant-transfer records are
encoded in the shared TestReferenceUniqueFutureNames body.

CREATE publishes parent and indexes together. The index rebuild seam validates
adjacent equal keys before catalog publication; transaction publication repeats
that validation as a final fence after merged rows. These fences supplement,
not replace, per-row immediate checks.
Backing roots for a co-created parent use its exact candidate storage, never
fabricated empty storage. The committed capture remains separate: a new parent
fences continued descriptor/storage absence, while an existing parent retains
its generation, row-end and version-length fences.

TYPE rewrites allocate fresh affected constraint/index identities under unchanged
names, remove the exact old index and replace the parent atomically. The bounded
CatalogStage.Create name-reuse seam admits only a coherent prior removal of the
exact base incumbent, preserves its touched-name fence and rejects a second
staged claimant. Converted live committed and private rows are checked before
Execute success; cancellation or a conflict installs nothing. Renames/schema
moves preserve IDs. Existing index closure evolution removes indexes when the
constraint or an included attribute disappears.

## Immediate write checks and ownership

VALUES and query-source INSERT validate NOT NULL/CHECK, acquire/check each UNIQUE
key against the accepted private prefix, and only then demand the next row.
The completed batch passes through the canonical WriteStage storage method.
UPDATE rechecks its locked current row, validates the successor, claims vacated
keys, upgrades changed key datums to UPDATE row strength, and checks the proposed
prefix before RETURNING. DELETE claims its vacated keys after qualification.
There is no partial write or RETURNING publication on failure.

Claims use the existing transaction/session/epoch-owned relation lock manager.
An interned key is scoped by the exact backing ObjectRef. Registered grouping
hashes narrow candidates; typed key comparison establishes equality. The manager
snapshots immutable candidates plus bucket pointer/generation, compares outside
metadata mutexes, and revalidates membership before pinning the exact resource.
Pins cover the gap before ordinary grant/queue journaling. Empty entries/buckets
retire without permanent tombstones. Hash collisions do not become equality.
Normal outcome/savepoint/cancellation/timeout/deadlock/epoch cleanup owns claims;
there is no second lock owner or fabricated RowID for key resources.

After a wait, checks capture current committed roots and authority, exclude
exact superseded row identities, overlay transaction/statement writes/deletes,
and check accepted proposed rows. Rebuilt private backing indices use substantive
immutable baseline roots owned by the existing catalogChanges overlay, built once
outside metadata locks at accepted DDL projection. Cloning copies the root map
and shares immutable roots; savepoints restore the catalog pointer, and backing
removal removes its private root. Own writes/ends remain separate live overlays.
When validation has no private effects, its sorted baseline is reused rather
than built twice. Missing required roots are invariant errors, never per-row
rebuild fallbacks. Ordinary CREATE TABLE's existing explicit-transaction exclusion
is unchanged. StatementScope binds slots/comparisons/grouping providers once per
exact parent ref/descriptor, clears them at Finish, and never caches roots or ACL
approval. Comparison and diagnostic provider calls run outside engine/session/
manager metadata mutexes.

Row lock strength uses SameDatum across the union of UNIQUE key attributes,
not B-tree equality: numeric scale and signed-zero datum changes use UPDATE,
while payload/exact-datum no-ops retain NO KEY UPDATE compatibility.
Each row version carries the latest committed key-transition sequence and a
private pending-transition bit. UPDATE carries existing history into successors
and accumulates SameDatum transitions; the no-failure commit fence stamps pending
history once and clears the bit. Payload updates never erase history, including
after a changed-and-restored key. REPEATABLE READ KEY SHARE accepts a retained
candidate only when that history is not newer than its snapshot frontier.
Savepoints restore the existing immutable write prefixes. Stable attribute/TYPE
projections copy metadata, while replacement imports with reminted RowIDs clear
foreign history. No new history store, lock owner or provider evaluation is added.

## Diagnostics, projection and controls

Ordinary duplicate writes produce23505, complete conditional Schema/Table/
Constraint and no synthetic Column/Position. Full key Detail is formatted only
for table SELECT or SELECT on every key attribute; partial readability suppresses
runtime Detail. Build/rewrite errors use the distinct unique-index primary:
readable keys produce `Key (...)=(...) is duplicated.`, otherwise the complete
Detail is `Duplicate keys exist.`. Both paths share the same permission predicate,
using current immutable authorization captured after waits and TYPE rewriting,
not ownership or cached bindings as a substitute for SELECT rights.
Typed output settings and SQL identifier labels are preserved; formatting and
permission evaluation remain outside engine/session/manager metadata mutexes.

The bounded existing pg_constraint projection adds contype u, parent conrelid and
NULL conbin. pg_class exposes the real backing relation; constraint decompilation
uses current attribute names. Catalog import remaps both ObjectAddresses and
embedded definition OIDs while retaining reciprocal links and TEMP sequence state.
Dump emits each named table UNIQUE once and skips separate serialization of its
owned backing relation. Isolated Restore reconstructs roots and rejects
nonunique replay without replacing the target.
TEMP tables remain outside the bounded native CREATE surface; the shared
temporary-lifecycle scenario retains PostgreSQL's TEMP-table control and checks
the native exclusion leaves its public parent/backing intact.

## Verification manifest

The untagged public SQL reference suite uses the same independently expected
bodies under native and pinned PostgreSQL18.0. Feature selectors begin
TestReferenceUnique; the feature smoke is TestReferenceUniqueFeatureSmoke.
Internal supplemental units assert substantive roots, immutable failed batches,
TYPE identity rebuilding, typed key interning/collisions/retirement, native ACL
conditional disclosure and CatalogStage exact-incumbent name reuse.
`TestUniqueCoCreatedParentStorageFence` covers new-parent absence, existing
storage fences and failed/canceled atomic creation. The former UNIQUE TYPE
capability-boundary witness is now the untagged dual-mode
`TestReferenceUniqueAlterTypeDependentBoundary`, with direct/prepared failures,
retained constraint/backing identities, canonical deparse and continuing checks.
Direct binding/interning units first gate their statement to initialize the real
transaction reference and lock ticket. Asynchronous key-wait units use command
admission and command-error normalization so configured statement deadlines
exercise the public command timer, not only a caller's cleanup deadline.
The shared schema-denial witness uses an independently owned nonpublic schema;
native public's default USAGE/CREATE rights are intentionally unchanged.
The RR key-history family independently covers payload/no-op/key/representation
changes, restored keys, later payload updates and snapshot-age/rollback controls
on both direct and prepared routes. Internal history units cover immutable
prefixes, savepoints, failed batches, publication, stable projection and reminting.
The owner TYPE-build disclosure family pairs PostgreSQL SQL REVOKE/GRANT controls
with native catalog ACL units; native SQL GRANT remains excluded. Shared terminal
tests cover configured statement timeout and a two-key deadlock with exact
diagnostics, survivor progress before victim rollback and continuing enforcement.
The remaining root B-tree SQL assertions are consolidated in untagged canonical
`TestReferenceBtreeIndexDiagnostics`, `TestReferenceBtreeIndexTransactionDistinction`
and `TestReferenceBtreeIndexExcludedDefinitionsDiagnostics`. MaintenanceNonContext
and TransactionNonContext retain actual original plain consumers beside recorded
Context owners. The complete incoming regression/maintenance/lifecycle/control
unions retain direct/prepared diagnostics, name rebinding, native dump atomicity
and independent PostgreSQL accepted-definition/transaction witnesses.
Superseded root/tagged and generic UniqueBtree-named duplicate hosts are removed,
not kept as forwarding aliases. Constraint-backed UNIQUE tests share consumer
bodies with standalone-index wrappers; obsolete standalone exclusions now assert
supported creation and preserve their original direct/plain/prepared/state union.
Tests are verification inputs, not a claim that verification has been run.
