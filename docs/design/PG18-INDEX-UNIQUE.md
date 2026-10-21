# Standalone unique B-tree indexes

`CREATE UNIQUE INDEX` supports existing simple-column B-tree keys, including
composite and repeated keys, directions, NULL ordering, compatible operator
classes and collations, and INCLUDE payloads. `NULLS DISTINCT` is the default;
`NULLS NOT DISTINCT` changes duplicate admission, not sort ordering. An ordinary
index may retain either NULL policy for deparse without enforcing uniqueness.
Unique expressions and operator-class/type combinations whose equality and hash
semantics disagree are rejected with `0A000`; ordinary expression indexes and
ordinary provider behavior remain unchanged.

## One enforcement authority

The index definition records its unique and NULL-policy properties independently
of optional constraint attachment. A standalone index creates no `pg_constraint`
row. Attached UNIQUE constraints continue to own their backing index; standalone
indexes instead have the established automatic parent/key/payload dependencies.

DML binds all unique indexes of the relation through the statement's captured
catalog. Catalog identity participates in the statement-local binding cache. The
binding supplies key-column lock strength and reuses the existing equality/hash
providers, UniqueKey claims, row locks, waiting, retained row versions, and
logical index roots. There is no second lock manager or constraint-like shadow
object. INCLUDE columns neither participate in duplicate equality nor upgrade
payload-only UPDATEs to key-changing row locks.

Partial UNIQUE is a consumer of these same reservations, roots and comparisons.
Finalized candidate and old-row membership runs before key/null/hash/claim work;
own writes, staged rows and accepted prefixes compare only members. Old member
claims survive leave/delete until outcome; excluded rows reserve no partial key.
Strong key slots/history use only effective nil-predicate indexes. The shared
predicate binder validates then removes analyzed standalone BOOL TRUE, not
logical/comparison definitions that merely execute TRUE. Ordinary/attached
uniqueness and coexisting nonpartial strong-key behavior remain unchanged.

INSERT SELECT captures whether unique enforcement is needed before opening the
source producer. Only unique targets perform per-row duplicate admission.
Nonunique targets retain the existing complete-batch evaluation and epoch check
at WriteStage.Apply, including when Reset or Restore replaces the catalog while
a pinned source is being evaluated.

A new index is built and checked before catalog publication, including committed
rows and the caller's projected private rows. A failed build publishes no name,
root, dependency, or successful result. TYPE projects rows and validates affected
unique indexes before installing replacement storage; DROP, rename, REINDEX,
TRUNCATE, transaction boundaries, and replacement use existing catalog/root
publication paths.

The combined publication owner keeps PREPARE validation separate from completion
materialization: validation returns no installation root map or materialized
merged root, while completion constructs substantive installation roots.
Required rebuilt predecessor entries are checked before applying deletions or
replacement rows, so a delta cannot hide an invalid unique build. The merge
checks uniqueness at adjacent survivor/addition and addition/addition boundaries
using the same index identity, NULL policy, output settings, and ACL authority.
Materialized partial roots carry exact immutable RowID-to-VersionID membership,
including an empty map for an empty subset. Rebuild and merge use one constructor;
retained immutable predicate pointer identity also fences reuse/afterwait checks.
TYPE validation consumes final evolved/rebound candidate definitions separately
from current disclosure authority and merges projected own rows minus ends/deletes.
Predicate-only TYPE duplicate membership fails at Execute; private baseline roots
remain owned by the journal index slot (`index.Journal.UniqueRoots`) and
savepoint recovery.

## Diagnostics and authorization

DML duplicates identify the actual index with `23505`, its name, schema, and table.
Build duplicates use `could not create unique index` and the build-detail form.
Candidate values use statement presentation settings. Readable duplicate values
require table SELECT or SELECT on every distinct key attribute; index ownership,
INSERT, and partial key SELECT do not authorize disclosure. Hidden build details
say `Duplicate keys exist.`; hidden DML details are absent. Real catalog ACLs, not
unsupported successful native SQL GRANT/REVOKE, exercise native disclosure.
Waiting DML recaptures current authorization before duplicate disclosure.
ACL-only descriptor replacement preserves definition identity. Post-wait
validation checks that identity, row shape, and index key semantics instead of
requiring the old descriptor pointer, while still rejecting replaced epochs or
changed definitions.
Predicate and INCLUDE values are not additional unique-detail authorization keys.

## Replay and coverage

Dump emits standalone indexes as `CREATE UNIQUE INDEX` with keys, providers,
INCLUDE, NULL policy and retained predicate; analyzed standalone TRUE omits WHERE.
TYPE buffers share that renderer with old-Usage normalization and original trees,
preserving UNIQUE/NND and reconstructed diagnostic positions. Attached backing
indexes remain folded into constraint definitions. Restore rebuilds through the same uniqueness authority;
invalid streams preserve the current catalog, data, and retained handles.

Shared untagged reference scenarios use the `TestReferenceUniqueIndex` prefix.
They cover direct/prepared routes, build atomicity and locking, typed and declared
providers, NULL and composite policies, diagnostics, DML and INSERT SELECT,
transactions, conflict/row-lock behavior, retained history, TYPE, lifecycle, and
native Dump/Reset/Restore controls. Native control statements are explicitly
separate from PostgreSQL assertions; real native dump SQL is also replayed on the
PostgreSQL fixture. Actual native ACL regressions use `TestUniqueIndexBuildOwnerDisclosure`
and `TestUniqueIndexCurrentACLAfterWait`. Oracle/native execution and parser
regeneration are root-owned; this document is a contract, not a verification
report.

Direct package regressions named `TestUniqueIndexPublication` cover this combined
publication boundary for standalone and genuinely attached definitions in both
phase modes. They assert predecessor-before-delta errors, scalar/composite
DISTINCT and NOT DISTINCT streaming admission, exact returned completion
membership, deletion/replacement membership, nil validation-only installation
maps, unchanged retained catalog/store/root contents, real captured ACL
disclosure with independently expected German DATE output, and cancellation.
The existing shared `TestReferencePreparedTransaction` family supplements these
units in both native and pinned-oracle selections.
