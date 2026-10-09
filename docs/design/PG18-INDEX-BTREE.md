# Ordinary simple-key B-tree indexes

## Bounded SQL and authority

Support ordinary nonunique `CREATE INDEX [name] ON [schema.]table [USING btree]
(column [ASC|DESC] [NULLS FIRST|LAST], ...)`, including `IF NOT EXISTS name`,
1–32 simple keys and repeated columns. Defaults are ASC NULLS LAST and DESC
NULLS FIRST. INDEX retains its unreserved keyword category. An index is a real
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

UNIQUE/PK/FK, expression/partial/concurrent indexes, non-btree methods, custom
collations/opclasses, INCLUDE, storage parameters/tablespaces, index-only scans,
DROP/ALTER INDEX and broad pg_index/pg_indexes/pg_get_indexdef catalogs remain
excluded. Valid excluded syntax fails explicitly rather than silently dropping
meaning. Unknown methods instead report PostgreSQL 42704 at execution.
Excluded-definition contracts prescribe full ERROR/0A000 diagnostics, including
feature-specific English messages, character-based Position where applicable
and absent unrelated logical fields. UNIQUE reports "unique indexes are
unsupported"; non-btree methods remain an Execute-only, position-free exclusion.
The public/parser/reference matrices include spaced and Unicode-prefixed
expression positions, actual prepared admission, no descriptor/data/dump
leakage and successful subsequent native DDL. Independent PostgreSQL streams
execute valid excluded definitions, using TEXT COLLATE "C" and INT int4_ops
rather than relying on errors from invalid provider combinations.

The release oracle is PostgreSQL18.0 UTF8/C from the pinned external fixture;
local20devel and the currently18.6 manual are research aids, not runtime evidence.
Release18.0 missing-simple-key diagnostics have no Position. Public native,
external reference, parser and engine tests independently prescribe results;
engine units inspect actual roots to rule out metadata-only success. Feature
smokes now share the dual-mode `TestReferenceUniqueBtreeRegression`,
`TestReferenceUniqueBtreeControls` and `TestReferenceUniqueBtreeLifecycle` bodies.
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
