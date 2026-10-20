# Concurrent index publication and observation

## Supported surface and change note

`CREATE [UNIQUE] INDEX CONCURRENTLY [IF NOT EXISTS]` uses the existing B-tree
key surface: simple or immutable computed keys, owned COLLATE and operator
classes, direction/NULL order, repeated keys and INCLUDE payload. It runs only
outside an explicit transaction block. Read-only rejection precedes this
restriction, relation lookup and lock acquisition. Ordinary public standalone
UNIQUE/NND remains supported for its existing bounded simple-key/provider surface;
concurrent B-tree creation additionally admits computed/provider-aware enforcing
tuples. Partial concurrent indexes, concurrent DROP/REINDEX, physical access
methods and optimizer/index-only-scan promises remain excluded.

The concurrent lifecycle, formerly excluded, now publishes real logical roots
and descriptor states. Successful ordinary and backing-index constructors remain
healthy. There is no new public SQL catalog or queue-observation API.

Concurrent nonunique hash creation inherits the integrated hash method's typed
equality/provider surface and logical bucket-root authority. Both methods share
the same writer/snapshot lifecycle, readiness admission and per-index rebuild
intent. Hash phases dispatch through the existing `buildIndexRoot` and hash
merge implementation; there is no B-tree ordering substitute or second hash
implementation. UNIQUE hash remains `0A000` before descriptor publication.
Syntactic concurrent `WHERE`, including `WHERE TRUE`, retains native Execute-time
unpositioned `0A000` (`partial concurrent indexes are unsupported`) before
predicate binding or descriptor publication. Ordinary partial B-tree creation
and ordinary concurrent creation remain available; HASH WHERE retains its
method-specific exclusion and diagnostic precedence.
`TestReferenceConcurrentIndexHash` covers both direct and held creation, actual
first and later writer barriers, permitted writes before either release,
healthy flags, duplicate-preserving equality reads, and actual emitted Dump SQL
through native Restore and unchanged PostgreSQL replay.

Hash query capture requires Ready, Valid and Live before constructing a retained
query candidate, in addition to every existing method/provider, storage identity,
generation, prefix, deletion-root and visibility proof. A ready-invalid root may
legitimately omit a subsequently committed pre-ready row while advancing its
storage identity; matching those identities alone does not establish complete
coverage. Such queries fall back to the existing heap source, without healing
the root or changing grandfathered publication. Successful REINDEX restores
query eligibility only after its complete healthy root is published.
`TestReferenceConcurrentIndexHashInvalidQuery` prescribes the canceled-build
public equality/heap outcomes; `TestConcurrentIndexHashInvalidQueryCapture`
supplements them with actual incomplete bucket and captured-source/filter proof,
then actual healthy capture/lookup after reconstruction.

## Descriptor observation

`stuntgres.IndexState` is a detached publication-state value. `InspectIndex(ctx, db,
schema, name)` uses the existing native-control adapter, releases its borrowed
connection, and resolves exact decoded names under the engine mutex. It does not
borrow search_path. The immutable definition owns Ready/Valid/Live; the shared
lightweight projection feeds both internal metadata and the public control
without deparse work or metadata-vector allocation. Valid implies Ready and
Live; Ready implies Live. Existing nil-context, foreign/wrapped-driver and exact
schema/name/kind control boundaries are unchanged.

## Lifecycle and publication authority

1. Acquire persistent parent SHARE UPDATE EXCLUSIVE. Publish the named descriptor
   with `(Ready, Valid, Live) = (false, false, true)` and no logical root.
2. Capture the exact currently granted parent holders conflicting with SHARE.
   Wait for their top-level outcomes, without acquiring or queuing parent SHARE.
   Later writers remain admissible and cannot extend that captured cohort.
3. Capture a fresh immutable row root, construct the actual sorted typed-key,
   row/version root and check first-build uniqueness. Canonical catalog
   publication installs that candidate and Ready atomically.
4. Capture and finish a second writer cohort. Validate a fresh root with ordinary
   write-style uniqueness diagnostics, while keeping the descriptor invalid.
5. Drop the builder's own advertised snapshot and visibility XID. Wait for actual
   same-engine endangered snapshot owners with `xmin <= validation xmin`.
6. Publish Valid through canonical catalog publication, retaining the newest root
   already maintained by successful ready-index writes.

Each detached candidate is fenced against exact parent/index descriptors,
engine epoch, namespace/owner/session authority and source storage pointer,
generation, length and deletion root. Conflicts recapture a fresh candidate.
The coordinator never assigns catalog/root maps directly. Failure leaves the
last committed descriptor/root phase intact; successful final publication cannot
be retracted by a later cancellation. Reset/Restore fence every old owner and
request, preventing resurrection into a replacement catalog.

## Completion and visibility ownership

Completion waits are hard edges in the existing relation/row/unique lock graph.
They are keyed by exact registered top-level tickets, not fake relation addresses
or a second channel authority. Savepoint or relation-contribution release cannot
complete an owner; install/rollback/abort/close does. Pending completion requests
are not soft FIFO edges and cannot be reordered to erase a mixed deadlock.
Existing cancellation, lock/statement deadlines, cleanup and epoch fencing own
these requests too. Native diagnostics explicitly name transaction completion.

Visibility XIDs are globally ordered within an engine and assigned at first
write/catalog visibility, not BEGIN. They are independent of CommitSeq and
per-session transaction numbers. Snapshot ownership is advertised in the same
critical section as actual row-root capture. RR/Serializable ownership persists
until transaction end or successful PREPARE ownership transfer; RC statement
completion drops its snapshot even if eager result rows remain open. PREPARE
clears only the exact detached owner's snapshot/xmin/safe-builder advertisement,
under the successful installation lock, while retaining its writer XID, SSI,
ticket and completion/lock ownership until actual prepared completion.
Bare BEGIN, idle RC and unrelated non-snapshot locks do
not pin validity. Unrelated ordinary readers and late-acquired endangered
snapshots do pin it. Only simple concurrent builder snapshots are exempt;
expression builders and ordinary readers are not. The builder drops its own
ownership before collecting the old-snapshot cohort.

`WaitingForOlderSnapshots` separately observes the live coordinator's admitted
older cohort. Dropping validation XID/snapshot, freezing eligible owners and
publishing that Boolean share one engine critical section. The marker is keyed
by exact index address and builder transaction/epoch, not persisted in the index
definition. It clears on every phase exit, owner end and epoch replacement;
stale cleanup cannot clear a replacement builder. It does not promise a
registered manager request. Shared negative newer-snapshot witnesses synchronize
on this native admission or PostgreSQL's actual old-snapshot progress, never on
Ready alone.

`WaitingForWriters` observes first/second frozen granted cohorts, distinguished
by Ready=false/true. Writer capture precedes publishing that marker, so a later
writer admitted after observation cannot join the already frozen first cohort.
It shares the sole index-address record with older-snapshot observation, keyed
by exact builder owner/epoch and cohort kind. The Booleans are mutually exclusive
and clear between phases and on cancellation/completion/replacement. Neither
claims lock-manager request registration. Shared writer witnesses require this
admission proof natively and actual progress/locker identities on PostgreSQL.

## Standalone uniqueness and readiness admission

The canonical definition owns `unique bool`, `nullsNotDistinct bool` and an
optional real `constraint` attachment. Standalone indexes have no fabricated
constraint rule/OID or parent uniqueConstraints entry. Their actual descriptor
name/address supplies diagnostic identity. INCLUDE never participates in tuple
equality or diagnostic labels. DISTINCT skips tuples with any NULL component;
NND compares NULL components as equal through the same tuple and claim providers.

Ordinary public UNIQUE admission preserves the integrated simple-key and provider
boundaries. Concurrent creation admits full computed/provider-aware tuples.
Structural validation, evolution and publication validate actual providers, not
the ordinary SQL capability policy; no stored trust/history flag distinguishes
these legitimately produced descriptors.

Root extraction and DML share typed computed-key extraction and the actual class
comparator. Nondefault equality classes use a congruent claim bucket followed by
actual tuple comparison (including TEXT bpchar_ops trailing-space equality), not
an incompatible type-only hash. Existing own-prefix, private writes/deletes,
claims, savepoint rollback and disclosure rules remain in force.

Row-key lock strengthening follows PostgreSQL's separate LIVE, simple-only
unique-index bitmap, including non-ready and invalid indexes. Canonical
`indexCatalogKey` classification admits parenthesized/top-COLLATE columns without
rewriting their original trees. Any real expression excludes the whole mixed
index from that bitmap; INCLUDE is excluded. This is distinct from the
Ready-and-Live gate for uniqueness claims and maintenance.

Readiness is a write-admission boundary. Private rows retain their admission
catalog until publication; committed rows clear that pin. Pre-ready writes may
commit after Ready without retroactive expression evaluation or enforcement.
Validation catches their resulting duplicates. Ready-invalid indexes continue
to maintain and enforce admitted writes; unrelated distinct/payload-only writes
do not rerun global standalone uniqueness validation or heal old duplicates.
Valid standalone roots retain integrated streaming publication duplicate
validation, alongside backing constraints and sanctioned reconstruction.
Ready-invalid standalone roots do not receive that global veto.

Generic ADD/DROP/unrelated TYPE, rename, remap and catalog publication preserve
not-ready absent roots and ready-invalid roots/flags. Parent row-shape intent is
not per-index rebuild authority. Explicit REINDEX INDEX/TABLE and actual affected
key/INCLUDE TYPE reconstruction can repair flags only with a successful atomic
full rebuild. Failed rebuilds preserve the predecessor state.
Affected TYPE checks the complete private prefix during Execute, caches the real
typed private baseline for later same-transaction producers, and reconstructs
accepted own rows even when the predecessor has no root. This explicit rebuild
does not remove the ordinary concurrent admission exemption. First build,
REINDEX and TYPE duplicate details use actual SELECT disclosure rights, not
ownership as an implicit read grant.
Actual expression keys require whole-table SELECT for tuple disclosure; SELECT
on every underlying column is insufficient. Simple keys instead require SELECT
on each key column, excluding INCLUDE payloads. Parentheses and a top-level
COLLATE do not turn a simple column into an actual expression for this purpose.
`TestReferenceConcurrentIndexDisclosure` supplies genuine PostgreSQL ACL
provisioning and full build, REINDEX, TYPE and runtime diagnostic expectations;
native coverage uses actual catalog ACLs rather than unsupported GRANT SQL.

## Controls and replay

Dump rejects persistent invalid/in-flight indexes deterministically before the
first writer call. Valid standalone unique definitions serialize ordinary
PostgreSQL-compatible UNIQUE index SQL, including NULLS NOT DISTINCT. Isolated
Restore uses the normal parser and the existing authenticated index destination
authority (`restoreReplay.tableDestination`, bootstrap and `importTargets`) to
reconstruct emitted computed definitions. Only this producer bypasses the
bounded ordinary SQL provider-admission policy; actual tuple/provider/root
validation and later claims remain shared. There is no UNIQUE parser option or new trust gate.
Concurrent phase SQL is rejected in a Restore stream. Malformed/duplicate streams
leave the destination unchanged.
Replacement rejects invalid imported definitions instead of healing them, while
retaining the destination's existing TEMP descriptors/roots and ownership.

## Evidence and verification inputs

The shared `reference/indexes/concurrent_index_*_test.go` bodies define R1–R11 and a real
feature smoke for both backends. They retain direct/held/plain/Context consumers,
full independent diagnostics, exact row/type/count observations and bounded
joined workers. PostgreSQL observes real pg_index/progress/VXID/pg_locks. Native
uses public flags; U2 additionally proves exact ordinary pending SHARE then
later RX ordering and a compatible reader through the actual manager journal.
Worker-start timing is not queue proof. Functional files are below 750 lines.

`^TestConcurrentIndex` internal parser/engine units cover U1–U7; public selectors
are `^TestReferenceConcurrentIndex` and
`^TestReferenceConcurrentIndexFeatureSmoke$`, each required in ordinary and
`REFERENCE=1` quickcheck modes. These are verification inputs, not a claim of
exercised checks. Root owns generation, formatting, verification and integration.

PostgreSQL sources: [CREATE INDEX](https://www.postgresql.org/docs/18/sql-createindex.html#SQL-CREATEINDEX-CONCURRENTLY),
[index flags](https://www.postgresql.org/docs/18/catalog-pg-index.html),
[progress](https://www.postgresql.org/docs/18/progress-reporting.html#CREATE-INDEX-PROGRESS-REPORTING).
