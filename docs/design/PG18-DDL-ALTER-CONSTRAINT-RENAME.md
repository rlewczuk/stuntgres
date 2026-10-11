# Constraint rename

This operation supersedes the earlier UNIQUE and EXCLUDE design records' rename
exclusions, without expanding their other capability boundaries.

## Syntax and authority

The existing standalone `ALTER TABLE [IF EXISTS] [ONLY] target [*] RENAME
CONSTRAINT old TO new` AST is retained. Rename is not a comma-list ALTER action;
whole-input parsing rejects a suffix before executing any valid prefix.
Prepare is syntax-only. Execute acquires the existing owner-authorized ACCESS
EXCLUSIVE table grant and uses the grant-time private descriptor. Namespace
USAGE resolution remains normal; namespace CREATE is not required. Read-only
transactions retain25006. Only constraint rename is newly admitted in writable
explicit transactions; table/column rename and index utilities retain exclusions.

Represented NOT NULL, CHECK, UNIQUE, PRIMARY KEY and EXCLUDE definitions rename
through existing catalog providers. Sequence targets return PostgreSQL's42809
`cannot rename columns of relation "name"` with sequence Detail; view/index
targets resolve before constraint lookup. Missing constraints return42704.
Supporting-index namespace relation collisions return42P07 before same-parent
constraint collisions42710. Non-index constraints need only relation-local
constraint-name uniqueness, so unrelated relations do not prevent their rename.

## Immutable group and publication

The executor clones only affected immutable descriptor definitions. NOT NULL
keeps its attribute/OID and enforcement. CHECK keeps OID, expression,
dependencies, NO INHERIT and declaration ordinal, while its enforcement vector
is re-sorted by current lexical name. This can change the first failing CHECK.
UNIQUE/PRIMARY KEY/EXCLUDE reuse the existing reciprocal backing-index helper:
constraint/index addresses and OIDs, key vectors, NULL policy, PK flags,
expressions, predicate, method, deferrability and descriptor comments survive.
There is no row scan, index rebuild or storage/root copy for this metadata change.

Existing CatalogStage providers validate the complete group outside Engine.mu.
The validated descriptors transfer into a cloned catalogChanges overlay with
original descriptor/name fences. Every stage-touched name is checked against
the pre-validation committed catalog before transfer, so a provider callback
cannot restamp a competing destination as the original name observation.
StatementScope.Finish remains the only implicit publication or explicit overlay
merge authority. Cancellation, catalog epoch,
current actor/role revision, descriptor and destination-name fences run before
installation. Repeated rename, name reuse and rename-back retain the original
fences. Savepoints restore the exact earlier overlay rather than reverse edits.
A later failed statement, such as a missing constraint rename, aborts the
transaction;25P02 persists until rollback and no staged rename is published.

Deferred EXCLUDE selections and pending events remain keyed by constraint OID.
Validation uses the current private descriptor/name; rename neither resets the
selected mode nor loses an obligation. Prepared SELECT metadata is unchanged;
held INSERTs and ON CONSTRAINT references resolve current definitions at Execute.
Old ON CONSTRAINT names fail42704 instead of acquiring cached authorization.

## Persistence and boundaries

Canonical Dump emits current named CREATE definitions once, persistent rows and
representable table/column comments, not ALTER history or an extra implicit
CREATE INDEX. Existing isolated Restore and atomic replacement retain failed
import guarantees. Restore identities are valid new allocations, not pinned to
prior/back-end OID sequences. Old retained Rows remain immutable snapshots.
Internal backing comments survive descriptor rename, but public COMMENT ON
INDEX/CONSTRAINT and backing-comment serialization are not claimed.

Graph-shape CHECK/NOT NULL ALTER remains native0A000. Existing local
UNIQUE/PRIMARY KEY/EXCLUDE definitions on admitted graph participants retain their
allowance; recursive inherited closure/provenance mutation is not added. Domain
constraint, FK and constraint-trigger providers are absent. PostgreSQL physical
storage/WAL, wire protocol, extension/native ABI and server pg_dump mechanisms
are outside this process-local SQL operation, not successful no-op substitutes.

## Proof surfaces

Shared public `TestReferenceConstraintRename*` scenarios specify independent
rows, counts, types, identities and complete logical diagnostics in native and
pinned PostgreSQL modes. Supplemental `TestConstraintRename*` package/parser
units inspect real group functions, provider validation, overlay immutability,
root sharing, cancellation/publication and genuine authorization APIs.

```sh
make check RUN='^TestReference(ConstraintRename|UniqueExcludedControls|PrimaryKeyBoundaries|ExclusionQuotedNames)'
make check REFERENCE=1 RUN='^TestReference(ConstraintRename|UniqueExcludedControls|PrimaryKeyBoundaries|ExclusionQuotedNames)'
make check RUN='^TestConstraintRename'
```

The standalone public smoke source is a session artifact, not a permanent test
or a verification claim in this document. Root records exercised checks and
smoke evidence separately.
