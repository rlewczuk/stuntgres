# PG18-DDL-EXCLUSION: owned scalar equality exclusions

## Contract and boundaries

CREATE TABLE table elements and existing ALTER TABLE ADD/DROP CONSTRAINT admit
real exclusion constraints over existing B-tree/HASH scalar equality families.
A pair conflicts only if both predicates are TRUE and every strict key comparison
is TRUE. FALSE/NULL predicates skip key extraction; any NULL key prevents a
conflict. INCLUDE is payload, not an operator-vector component.

The supported operator is original-type, commutative `=` in the selected owned
operator family. Index binary input compatibility is deliberately not operator
family membership: INTEGER/oid_ops and TEXT/bpchar_ops remain counterexamples.
Existing scalar providers govern domains, enums, character semantics, numeric
scale, NaN, signed zero and temporal equality. No adapter formatting determines
equality. HASH retains its existing single-key/no-INCLUDE/no-direction limits;
its constraint-local predicate does not widen standalone partial HASH creation.

DEFERRABLE, INITIALLY IMMEDIATE/DEFERRED and bounded SET CONSTRAINTS are definition/
transaction state separate from transaction READ DEFERRABLE. Deferrable UNIQUE,
general transactional CREATE and standalone rename boundaries remain unchanged.
Feature-local standalone RENAME CONSTRAINT supports actual EX attachments through
the existing rename publisher; active explicit transactions retain utility0A000.
No GiST/SP-GiST, range, extension, physical storage/WAL or PostgreSQL server pg_dump
implementation is claimed. Missing native methods are capability boundaries, not
claims that PostgreSQL rejects their process-local algorithms.

## Definition and identity ownership

Parser exclusion nodes retain original key/operator/property positions and the
existing index syntax. A tagged backing identity distinguishes UNIQUE from
EXCLUDE; there is no fake UNIQUE descriptor or compatibility alias. A parent
exclusion record owns a distinct constraint OID and index address, operator
vector, name and definition-time deferrability. The actual nonunique integrity
index owns the reciprocal tagged identity and the same immutable operator vector.
Catalog validation checks the whole attachment rather than trusting one backlink.

CREATE augments its already-created private parent directly; ALTER replaces the
actual base descriptor. CREATE does not use Replace to manufacture a nonexistent
base predecessor or bypass the catalog stage's replacement authority.

Definition binding reuses the existing installed, lock-required immutable index
binders and catalog staging convention. No adjacent CREATE INDEX analysis overhaul
or unsafe unlock is introduced. Runtime extraction, equality, dirty scanning,
event checks, root preparation and completion waits run outside engine/session/
transaction-owner/manager metadata locks. Existing statement/session admission is
retained across those unlocked computations and publication fences.

Existing pg_constraint exposes contype x and current symbolic pg_get_constraintdef.
Internal index metadata has IndIsExclusion true, IndIsUnique false and IndImmediate
based on definition-time deferrability. Native SQL pg_index and new pg_constraint
condeferrable/condeferred columns are not added. Exclusion integrity indices never
contribute strong KEY SHARE/NO KEY UPDATE row-key bits.

## Admission, dirty membership and selective events

The existing relation manager remains the sole grant/wait/deadlock authority.
Nondeferrable equality uses actual typed unique-key claims. Deferrable admission
performs a NOWAIT potential-conflict scan and admits the row without retaining an
extra grant owner. Completion checks wait on existing transaction-completion
resources, not a private condition variable or a second transaction graph.

Engine.mu owns immutable dirty projection membership keyed by the exact ticket,
constraint/index identities and actual reserved RowID/VersionID. Reservations are
reused by row staging; no fake probe-only row identity is manufactured. Projections
record qualifying keys and exact provisional predecessor endings. Later updates
remove superseded membership even when the new predicate is FALSE or a key is NULL.
Ungrantable eager peer candidates are not advertised as admitted conflicts, avoiding
a first-owner scan waiting on a later ungranted key claimant.

Nondeferrable deletion retains the existing exact typed-key grant, so eager
reinsertion waits there before scanning dirty membership. Deferrable deletion
does not take that grant; a conflicting immediate event waits on the deleting
owner's actual completion resource. Both outcomes recheck after commit/rollback.

A potential conflict enlists a concrete row/version event. An uncontested first
deferred insertion does not invent an obligation because a later peer appears.
In ordered A1/B1 admission, B owns the obligation: B COMMIT waits for A, and A
rollback permits B. Crossed A1/B2/A2/B1 admissions independently enlist both owners
and can form a real closed completion-resource deadlock. Dirty scans do not use a
repeatable-read query frontier to evade current integrity checks; ordinary reads
retain their existing MVCC view.

Statement-final IMMEDIATE, SET IMMEDIATE, implicit finish, SQL/Go COMMIT and PREPARE
force only due live events. Rechecks recapture immutable committed roots and dirty
membership after completion waits, with catalog/store/version/generation fences.
Deleted/superseded versions and retired constraint identities do not remain live
obligations. There is no blanket touched-constraint boundary scan that synthesizes
new deferred events from later peers.

SET resolution follows the current namespace path and first namespace containing
matches, including all matching table constraints in that namespace. ALL resets
named overrides and ignores nondeferrable definitions. Named DEFERRED rejects
nondeferrable matches; named IMMEDIATE is valid. Missing names and outside-block
warning/error ordering follow the released independent reference contracts.

## Savepoints, prepared owners and publication

Savepoints retain immutable projection/event/mode snapshots and existing lock
journals. Rollback restores those snapshots with the private catalog and actual
writes/deletions. Constraint modes are keyed by real identities, so dropped and
recreated same-named definitions do not inherit stale named overrides; ALL's
transaction-wide default remains independently meaningful.

Explicit ROLLBACK TO installs projection and constraint modes inside its existing
atomic effect step, as well as restoring the lock journal outside metadata locks;
error recovery restores the same saved state through its separate recovery path.

PREPARE checks existing events before registry installation and transfers the same
ticket/dirty projection. The former session does not become the prepared owner's
completion authority. COMMIT/ROLLBACK PREPARED retires that projection through the
existing ticket-completion path, before waiter grants/wakes. Completion does not
manufacture a new obligation for an earlier uncontested prepared row.

Existing publication packages validate actual B-tree entries or HASH buckets using
the captured exclusion operators. PREPARE's nonmaterializing path still validates
integrity; it does not install a new root map. Duplicate, derived-provider failure,
cancellation and final publication veto install no partial catalog/store/root
state. Provider waits and callbacks are not moved beneath metadata locks.

Final publication integrity vetoes retain the complete logical diagnostic,
including actual schema/table/constraint and both typed keys. B-tree and HASH,
materializing publication and nonmaterializing PREPARE use the same captured
namespace authority; omission of Schema is not an acceptable final-veto shortcut.

Store capture uses the candidate catalog's actual index parents while borrowing
their installed predecessor storage. This includes a first index added to an
existing previously unindexed table, so final store-generation fences have a real
baseline rather than permanently retrying against a missing capture.

ALTER ADD validates final committed plus own-write rows before staging the pair;
failed builds retain the original rows/catalog. DROP, schema move, table/column/
backing-index rename, predicate/include/key removal and TYPE use existing catalog
lifecycle owners. Affected TYPE remints constraint/index identities and rebinds
providers before validating converted data; unaffected attachments remain intact.

TYPE expression rebinding may already have registered the old backing generation
as a private staged candidate. Removal preserves the original descriptor/ref and
name publication observations, but retires that private descriptor and its own
private name slot before creating the new same-named identity. Stage reads,
provider validation and publication deltas therefore enumerate the same actual
candidate rather than resurrecting or validating the removed generation beside
its replacement. Create uses the canonical live-name check, not a second
presence-only private-map check; live identities and original conflict fences
remain authoritative.

## Consumers and native controls

VALUES/default/query INSERT, UPDATE, UPDATE FROM, MERGE NOT MATCHED INSERT and
DELETE maintenance share the existing write owners. MERGE MATCHED retains its
existing native full0A000 capability boundary at MATCHED (actual Prepare versus
direct Execute), while the independent PostgreSQL control still expects23P01
for its conflicting update. No adjacent MATCHED implementation is introduced.
Omitted or named ON CONFLICT DO NOTHING can select
supported nondeferrable exclusions; unique inference cannot. Unselected exclusion
violations still fail23P01. Selected deferrable arbiters fail55000 with actual
schema/table/constraint only when a proposal is demanded; a zero-row producer
succeeds. Defaults, generated columns, NOT NULL, CHECK and demanded sequence effects
remain upstream of arbitration, and skipped rows retain no new key claim.

Exclusion key-detail expressions use the existing PostgreSQL pretty deparse
context, including ungrouped simple cast operands such as `lower(label::text)`.
This diagnostic presentation does not change canonical symbolic Dump rendering.

Detailed runtime/build errors render both actual typed keys with current names and
output environment. Table SELECT or every simple key column SELECT permits detail;
expression keys require table SELECT. Ownership does not bypass revoked SELECT.
Unreadable details remain the released `Key conflicts with existing key.` or
`Key conflicts exist.`, not a suppressed error or UNIQUE-style omitted Detail.

Dump renders the original inline constraint once and skips its separately owned
index. Isolated Restore reuses the real binder/root validation and rejects late
conflicts without changing the destination. Reset and foreign/same-owner replacement
reuse existing identity/TEMP remappers. Typed operators are rebound only when their
captured usage descriptor changes, so imported domain values compare with destination
providers rather than old source descriptor pointers. Source definitions remain
immutable, retained live TEMP objects keep exact identity, and modes/events are not
serialized. These are native process-local controls, not server backup tooling.

## Independent acceptance and root verification

Shared `TestReferenceExclusion*` scenarios prescribe rows, multiplicity, metadata,
counts, diagnostics, notices and actual Prepare/Execute phases independently for
native and pinned PostgreSQL18.0. Oracle discovery and observed native quick-red
precede production. Later changed reference definitions require both modes again;
readiness is not verification. No builds/tests/formatters are run by implementers.

Internal `TestExclusion*` units inspect actual roots/backlinks/typed providers,
projection/version identities, savepoint/prepared lifetime, existing key/completion
queues, crossed-owner deadlock cleanup and immutable publication failure state.
Public channels alone are not queue proof; workers are cancelled and joined.

Root commands (each selected check has a 900-second deadline):

```sh
make check RUN='^TestReferenceExclusion'
make check REFERENCE=1 RUN='^TestReferenceExclusion'
make check RUN='^TestReferenceInsertConflictProviderBoundaries$'
make check REFERENCE=1 RUN='^TestReferenceInsertConflictProviderBoundaries$'
make check RUN='^TestExclusion'
make check RUN='^TestReferenceExclusionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceExclusionFeatureSmoke$'
```

Feature smoke must observe real expression/predicate B-tree and constraint-local
HASH enforcement, duplicate23P01/no effects, exact INT4/TEXT/BOOL rows and counts,
ALTER ADD/DROP, selective deferred SET failure/savepoint repair/COMMIT, omitted/
named conflict omission and native Dump/Reset/Restore future conflicts. The root
also runs the complete affected migration selectors and ordinary quickcheck before
review, then finalcheck after integration under the user-updated 1800-second deadline.
No oracle/native verification result is asserted by this design document.

## Primary release sources

- [PostgreSQL18 constraints](https://www.postgresql.org/docs/18/ddl-constraints.html)
- [CREATE TABLE](https://www.postgresql.org/docs/18/sql-createtable.html)
- [SET CONSTRAINTS](https://www.postgresql.org/docs/18/sql-set-constraints.html)
- [REL_18_0 operator resolution](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/parse_oper.c)
- [REL_18_0 index definition validation](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/indexcmds.c)
- [REL_18_0 exclusion admission/recheck](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/executor/execIndexing.c)
- [REL_18_0 deferred trigger/SET ownership](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/trigger.c)

The rolling /docs/18 pages are conceptual sources, not substitutes for the pinned
18.0 runtime or released diagnostic source. Development-tree positions are not
silently substituted for the release's diagnostics.
