# Ordinary B-tree indexes

## Bounded SQL and authority

Support ordinary nonunique `CREATE INDEX [name] ON [schema.]table [USING btree]
(column [ASC|DESC] [NULLS FIRST|LAST], ...)`, including `IF NOT EXISTS name`,
1–32 simple or scalar expression keys and repeated keys. Expression syntax and
immutability are described below. Defaults are ASC NULLS LAST and DESC NULLS
FIRST. INDEX retains its unreserved keyword category. An index is a real
`pg_class` relation of kind `i`, in the parent schema with the parent owner and
real key count; it shares the table/sequence/view relation-name namespace.

Execution resolves the current target through existing search_path/USAGE
rules, checks table ownership before waiting, and obtains the existing ordinary
Share relation grant. Readers, ROW SHARE and other Share grants remain
compatible; writers and stronger conflicting utilities wait. Grant-time authority
and exact incarnation/epoch are revalidated. Namespace CREATE belongs to the
initiating authority, not a substitute index catalog. Prepare describes an empty
utility result without binding the table or keys; every Execute binds afresh.
This syntax-only preparation also applies inside active writable/read-only
transactions; CREATE INDEX's native exclusion and read-only checks belong to
Execute. Other utilities retain their existing central preparation policy.

Explicit writable transactions retain the existing native utility exclusion:
CREATE INDEX Execute returns 0A000 before target lookup, ACL checks or Share
requests, without effects. PostgreSQL permits ordinary transactional CREATE
INDEX, including rollback. Read-only execution instead returns 25006 before
that exclusion and relation resolution. SQL PREPARE AS CREATE INDEX is not the
extended database/sql preparation surface.

## Representation

A relation descriptor owns an immutable definition: parent ObjectAddress and
ordered stable attribute IDs with direction and resolved NULL placement. This is
separate from the descriptor's existing column-name-to-slot `index` map.

The engine owns immutable logical roots keyed by index ObjectAddress. A root is
a sorted vector of key tuples and RowID/VersionID pairs, one entry per currently
committed live parent row. Duplicates remain separate identities; NULLs remain
members. Comparisons reuse bound sqltype ordering providers and explicit NULL
placement. Row/version identity breaks internal ties deterministically, but does
not establish portable SQL ordering among equal keys. Cancellable error-returning
merge sort checks even NULL and tie paths; no swallowed comparator errors.
Physical tree pages, disk/WAL/vacuum algorithms and cost optimization are not
represented or claimed.

## Publication and maintenance

CatalogStage privately evolves dependent definitions for parent schema/owner/key
usage changes and removes indices whose parent or indexed attribute disappears.
Catalog publication captures immutable row heads/end roots and scalar generation
observations, builds new or row-shape-rebuilt roots before final locking, and
atomically installs names/descriptors/storage/roots after catalog, storage,
authorization, epoch and cancellation fences.

The existing row publication owner covers VALUES, INSERT SELECT, defaults,
identity/CHECK inserts, UPDATE, DELETE and explicit commits. It captures the
current catalog/index set and root predecessors with parent heads; candidates
are built outside the final mutex. It extracts/sorts only newly live key tuples,
removes exact ended row/version identities and batch-merges survivors/additions
once into a fresh vector. Unchanged tuples are shared, not copied per VALUES row.
Rows, end markers, private TYPE/drop metadata and index roots cross the same
no-allocation/no-callback/no-failure installation boundary. Catalog changes
(including concurrent ordinary index creation) force preparation to retry, so
DML cannot miss a newly published definition. Rollback/savepoint discard private
changes; no private write publishes an index entry before commit.

Without published indexes, row publication skips index-store capture, catalog
root preparation and membership merging. It still fences the captured catalog
set, so concurrent first-index creation forces a retry. Index construction polls
cancellation during actual work; a generic root-map preparation step adds no
extra context callback. The owning publisher's final cancellation observation
immediately precedes irreversible installation. Replacement prepares reminted
roots before that final observation. No later context check may retract a
successful installation or its committed result.

Current-only index roots are deliberately not query membership authorities.
Existing scan/filter/sort and selected MVCC views retain correctness with or
without indexes, including RR snapshots and own writes. No planner choice or
unordered-result guarantee is added.

## Lifecycle and native controls

Renames retain stable parent/key IDs and the index's own name. DROP COLUMN removes
only indices involving the dropped stable attribute and remaps surviving keys;
TYPE rebinds comparison usages and rebuilds from the final candidate row shape.
Table/schema removal leaves no names or root orphans. SET SCHEMA moves the table,
indices and owned sequences as one collision-checked group. Ordinary nonunique
indices do not strengthen UPDATE's NO KEY UPDATE row-lock classification.

Dump captures actual definitions, renders current quoted schema/table/key names
and every direction/NULL option, and emits parent definitions/data before indices
under named owner directives. It emits no logical entry contents/history. Restore
replays only the supported CREATE INDEX surface in its isolated import engine,
then atomically replaces persistent state. Existing trusted import destination
authority handles independent schema/table owners; ordinary SQL ACLs are not
weakened or grants synthesized. Replacement privately remints rows/version IDs,
rebuilds destination roots, and advances allocator/epoch frontiers only after
candidate comparisons/cancellation succeed. Reset removes persistent indices;
retained eager Rows retain their established immutable ownership.
Retained TEMP identities may collide with imported relation addresses. The
existing collision mapper detaches affected index definitions and remaps their
parents alongside table/storage addresses, leaving stable key attributes and
the source definitions/roots immutable. Destination roots rebuild from the
remapped parent storage; subsequent DML and parent-drop closure use that same
destination authority.

Dependency evolution skips indexes whose parent is neither replaced nor removed
before binding ordering, avoiding unrelated per-index allocation under the
engine mutex. A permanent zero-allocation evolution witness covers this path.

## Exclusions and evidence

Standalone UNIQUE/PK/FK, partial/concurrent indexes, non-btree methods, unowned
collations/opclasses, storage parameters/tablespaces, physical index-only scans,
DROP/ALTER INDEX and broad pg_index/pg_indexes catalogs remain excluded.
The existing pg_get_indexdef inquiry is supported. Table UNIQUE backing indexes
are independently supported; standalone
unique attachment is not admitted. [PG18-INDEX-CLAUSES](PG18-INDEX-CLAUSES.md)
supersedes this part's historical COLLATE/class/INCLUDE exclusions: owned providers
and simple INCLUDE payload are now admitted, with the 32 limit counting both roles.
Expression support supersedes the original expression exclusion: the shared
expression-index reference family retains spaced/Unicode syntax and quiet
prepared admission, full semantic diagnostics and atomic no-leakage witnesses.
Valid remaining excluded syntax fails explicitly instead of dropping meaning;
unknown methods report 42704 at execution. Exclusion contracts retain full
diagnostics, character positions where applicable, prepared admission and no
descriptor/data/Dump leakage. Valid unowned PostgreSQL definitions have successful
PostgreSQL controls beside truthful native capability failures, not fictitious
shared rejection. Semantic clause errors follow PostgreSQL18.0 absence fields;
the later class-option phase respects duplicate-name/IF NOT EXISTS precedence.

The release oracle is PostgreSQL18.0 UTF8/C from the pinned external fixture;
local20devel and the currently18.6 manual are research aids, not runtime evidence.
Release18.0 missing-simple-key diagnostics have no Position. Public native,
external reference, parser and engine tests independently prescribe results;
engine units inspect actual roots to rule out metadata-only success. Feature
smokes now share the dual-mode `TestReferenceBtreeIndexRegression`,
`TestReferenceBtreeIndexControls` and `TestReferenceBtreeIndexLifecycle` bodies.
Verification and parser generation are root-owned;
this design record itself does not claim exercised checks.

Sources: [CREATE INDEX](https://www.postgresql.org/docs/18/sql-createindex.html),
[B-tree](https://www.postgresql.org/docs/18/indexes-types.html#INDEXES-TYPES-BTREE),
[ordering](https://www.postgresql.org/docs/18/indexes-ordering.html),
[locking](https://www.postgresql.org/docs/18/explicit-locking.html),
[REL18.0 indexcmds.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/indexcmds.c).

## Independent parent publication

Root preparation shares unaffected immutable roots. The final commit fences
the storage generations of roots it actually changes, then retains any newer
unchanged sibling roots from the live map. Independent DML on two indexed
parents therefore cannot overwrite the other parent's newly committed entries
or acquire a needless cross-parent storage conflict. The catalog-set fence still
applies to ordinary DML, including when it has no private catalog changes, so a
concurrent index creation cannot be missed.

Only unnamed definitions construct a name addition. Its unique internal
attribute labels are appended whole, with underscores, until the byte length
first reaches 64; generatedRelationName then performs the release's balanced
UTF8 clipping. No preliminary 63-byte clipping or spare-byte redistribution is
performed. Repeated-key labels remain separate from stable parent attributes.

Trusted native Restore gives CREATE INDEX the existing statement-local
bootstrap lookup authority, only after the importer/authenticated-superuser
marker is validated. It stamps the index with its actual parent's owner and
namespace, never with the temporary bootstrap lookup actor. Ordinary commands
retain schema USAGE/CREATE and parent-owner checks; replay does not manufacture
grants or change target session identity.

## Expression keys — change note

Ordinary nonunique indexes accept parenthesized shared scalar expressions and
bare supported function calls alongside simple columns. Binding exposes only
the parent relation, with existing authorization, scalar providers and result
usage/orderability checks. Aggregates and subqueries fail semantically at Execute;
syntax-only utility Prepare remains quiet. UNIQUE backing keys remain valid
simple slots; expression sentinels never enter UNIQUE grouping or claim paths.

Each derived key retains its original analyzed definition and dependencies plus
the shared simplified execution tree. Immutability is checked after simplification,
including concrete coercion/output-provider dependence and surviving children;
dead CASE/COALESCE branches do not contribute volatility. Explicit timestamp and
timestamptz AT TIME ZONE text/interval overloads, and interval-zone timetz, are
immutable. AT LOCAL and text-zone timetz remain stable. Planned inspection uses
the retained node's concrete provider classification and surviving children, not
a separate index function allowlist. Original analyzed metadata stays conservative
(Stable) for existing consumers, including DEFAULT; simplification does not rewrite
that metadata.

Build and batch maintenance evaluate those execution trees before publishing new
logical tuples. Explicit-transaction statement Finish also evaluates completed
candidate live derived rows before private installation, outside the engine mutex;
it neither allocates full tuples nor sorts/rebuilds roots. The existing operation,
catalog/authority, epoch and cancellation preflights still gate installation.
Late evaluation errors fail the statement, abort private work and permit existing
savepoint recovery rather than waiting for COMMIT.

DROP remaps surviving dense slots in both trees while retaining stable attribute
dependencies. TYPE captures the full pre-change PostgreSQL-style CREATE INDEX
buffer through pgRenderer, including all earlier keys, quoted/Unicode identifiers
and nondefault direction/NULL options. Rebinding that exact saved buffer against
the final parent preserves generated diagnostic Position. Dump renders current
original expressions; Restore/replacement remaps dependencies and rebuilds fresh
destination tuples without mutating source trees, rows or roots.

The shared ExplicitInsertAtomicity, TimeZoneVolatility and TypeReparsePositions
reference definitions prescribe transaction phases, concrete timezone results
and mixed/quoted/Unicode TYPE diagnostics independently. Engine SQLLifecycleRoots
and SQLErrorAndForeignReplacementRoots witnesses inspect all tuple components,
real live row/version membership, DROP compaction, TYPE usages, retained roots and
a proven retained TEMP address collision. Root-owned dual-mode checks and feature
smoke remain the verification authority; this document does not claim checks.
