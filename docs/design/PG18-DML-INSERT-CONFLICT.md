# PG18-DML-INSERT-CONFLICT

## Contract and scope

INSERT's existing scalar VALUES, DEFAULT VALUES and query-source producers admit
`ON CONFLICT DO NOTHING`. Targets are omitted, inferred column/expression sets
with optional COLLATE/class/WHERE, or an installed named constraint. DO UPDATE,
INSERT RETURNING, writable target views, deferred/exclusion providers and
conflict-bearing Restore INSERT input remain outside this part. No configuration,
model, optimizer, generic-plan cache or prerequisite DDL changes are required.

## Owners and execution schedule

The parser retains a narrow InsertConflict node and positions inference opening
punctuation and excluded suffix tokens. Grammar generation remains the repository
owner's normal make responsibility; no handwritten generated parser is installed.
An actual INSERT INTO head enables contextual ON_CONFLICT_LA lookahead using the
existing single raw-token cache; the accepted-statement boundary clears it.
Only real unquoted CONFLICT qualifies, including intervening scanner-owned
comments. JOIN's on_keyword accepts either ON token so a genuine conflict-named
predicate remains legal. Standalone SELECT/predicate suffix guards and global ON
precedence are unchanged. Shared PredicateSuffix witnesses independently retain
native/PG quoted-action and standalone/script boundary records; root observed
every PostgreSQL witness and the native bare-Boolean failure before authorizing
this narrow remedy.
Conflict-target direct known aggregate calls retain AggregateExpr classification
using the existing parser aggregate authority; the existing index-context binder
then rejects them without requesting an ordinary SUM execution provider. Arbitrary,
non-pg_catalog-qualified, named, variadic and failure-only calls keep generic
resolution. CREATE index-call classification remains unchanged.
Native public calls still reject multiple successfully parsed statements with
0A000 before executing any prefix. PostgreSQL's direct script success is asserted
separately; parser-only units prove accepted-marker reset without adding script
execution capability.
Analysis uses the existing index-expression binder with a target-table namespace,
shared parameter constraints and stable dependencies. It captures actual read
manifests and does not manufacture index providers. Execute selects arbiters even
for empty producers after current authorization and current parameter evaluation.

`insert_conflict.go` compares unordered original expression/attribute sets,
ignoring repeated occurrences and INCLUDE payload. Explicit class identity is
family plus declared input, distinct from CREATE's broader binary-compatible
provider admission. Unspecified decorations do not constrain inference. Named
constraints use their actual backing-index address, not a same-named standalone
index. Targetless arbitration includes live ready-invalid unique providers;
explicit inference requires valid providers. Native installed definitions have
no deferred/exclusion flags and this consumer does not invent them.

`insert_conflict_predicate.go` is a separate release-conservative strong TRUE
implication consumer. Immutable constants and execution parameters specialize into
owned literals without mutating retained definitions. Recursive normalization
preserves full SQL TRUE/FALSE/NULL values, including truth tests beneath NOT,
null tests and equality. Truth-test nodes remain opaque proof atoms except for
structural identity; mathematical TRUE-set inclusion alone is not eligibility.
PostgreSQL 18 rejects `active IS TRUE => active`, `active IS FALSE => NOT active`
and `NOT (active IS NOT TRUE/FALSE) => active IS NOT NULL` in these fixtures.
Released AND/OR recursion, Boolean equality and actual strictness remain bounded
rules. Strictness does not pass through nonstrict truth/null tests. Negated
comparison operators exist only in proof-local views, never executable nodes with
stale evaluators. Operator families, collations, namespaces and session-dependence
constrain order proofs; typed comparison owns constant comparisons. SELECT's
exact-conjunction mask is unchanged; no truth-subset solver or theorem engine is added.

Row assembly preserves assignment/default/identity/generated/NOT NULL/CHECK
validation before arbitration. `probeUniqueRow` returns a value outcome with the
actual provider and authorization view. Only selected stable-isolation hits need
committed row/version identity.
The ordinary `checkUniqueRow` wrapper retains its23505 behavior for INSERT without a
conflict action, UPDATE and MERGE. Selected providers are probed first; a selected
hit omits the candidate even if another provider would also fail. Only after no
selected hit does an unselected collision become ordinary23505. All paths use
existing typed key reservation, roots, private replacement/deletion visibility
and accepted batch prefixes.

An omitted candidate restores only unique-key grants acquired since its mark.
The same lock manager owns that operation; relation, row-witness and transaction
completion resources are untouched. Previously held key grants/modes survive.
Checkpoint/restore accepts only ordinary unique resources on nonpersistent
transaction tickets. It reads/intersects the current relationHold.mode without
replacing retained holder contributions. Session advisory tickets and their
counted shared/exclusive grants remain outside omission and statement Finish;
the supplemental claim seam explicitly checks repeated counted grants and cleanup.
Accepted candidates retain grants until the existing transaction/savepoint owner
releases them. No second key dictionary, speculative row registry or retry worker
is introduced. Provider publication after a wait recaptures selected arbiters and
current ACL checks. Publication-time recapture compacts accepted rows in place and
updates the command count, restoring the statement's unique-claim baseline first.

Committed conflicting row birth is compared with the existing transaction
frontier. Read committed may skip a row absent from the source snapshot;
repeatable read and serializable give40001 for a post-frontier committed conflict.
Own private and accepted-prefix outcomes are exempt. Cancellation and subsequent
failure use normal Finish/savepoint cleanup and veto staged writes. Source
consumption is counted separately from accepted rows; required Close errors veto
the batch. Already demanded sequence values remain nontransactional as before.

Logical index entries carry immutable physical storage positions. Initial,
concurrent, private and trusted reconstruction enumerate the actual storage
vector; publication uses the existing append/replacement slot manifest and
sorting carries the entire entry. Selected repeatable-read/serializable probes
capture the index root and storage under the same engine mutex and retrieve a
version in O(1), checking bounds, RowID and VersionID. A mismatch is
ErrCatalogReplaced, never a scan fallback. Ordinary enforcement and read-committed/
read-uncommitted omissions do not recover identity. No per-proposal map or value
copy is introduced. Reusable no-overlay TYPE validation roots build against the
original physical vector plus deletion ends; compacted overlay validation roots
are not reusable. This preserves positions after tombstones.

## Controls and privileges

INSERT target writes retain existing column INSERT checks. Explicit inferred
keys/predicate and named unique keys add SELECT requirements on their real stable
attribute manifests; INCLUDE columns and hidden targetless provider evaluation
do not add artificial SELECT requirements. Postwait checks use current catalog
and roles. Held statements reanalyze existing dependencies/current definitions.
Dump/Reset/Restore preserve supported installed definitions and accepted data;
Restore rejects conflict-bearing INSERTs before isolated replay admission.

## Evidence and verification responsibility

The shared `TestReferenceInsertConflict` family was authored before production.
Root observations established the real PostgreSQL18 cancellation Context, syntax
and semantic subquery separation, collation offsets, opclass-option inference and
custom/generic parameter-plan distinction. The final discovery run had no failing
PostgreSQL subtests and native runs exposed the expected unsupported grammar.
These observations authorized implementation; they do not establish a passing
native implementation. PostgreSQL can switch to generic parameter plans whereas
native inference specializes each Execute; tests retain that explicit distinction.

Public definitions independently specify rows, types, metadata, diagnostics and
stages, real provider phase barriers, contention/cancellation and native controls.
The read-committed invisible-conflict witness uses the existing JOIN plus volatile
ORDER-output frontier and positive sequence-capture helper before peer commit.
Ordinary projection is not assumed to precede source rowmarks. PostgreSQL additionally
observes its actual waiter externally; no native public wait catalog is fabricated.
Package units independently enumerate every comparison-table cell and use actual
expression/descriptor/root/cursor/role/ACL/ticket objects, issuing no public SQL.
The unique-provider fixture assigns its table/index owners from the engine's
actual bootstrap role catalog, so admission exercises genuine authorization
rather than zero identities or bypasses. Negative volatility proof uses a real
surviving stable FORMAT operation: manually labeling an immutable comparison
Stable does not change plannedVolatility's physical-tree invariant.
Nested truth reference cases were refined against pinned PostgreSQL before the
native behavioral red and production remedy. Package supplements independently
assert value fidelity, every immutable entry position, bad-hint fences and
ordinary/nonstable consumer gating. The committed-omission benchmark exercises
actual admission for increasing batches in both read-committed and repeatable-read.
Cancellation covers direct and retained held handles, prefix atomicity, savepoint
recovery and a healthy same-route insertion. Native postwait ACL tests use a real
restricted actor, exact held/pending unique resource and accepted-prefix claim
barrier, revoke predicate SELECT or table INSERT, then require full42501, unchanged
sentinel identity/data, no remaining tickets/grants/requests/key buckets and real
regranted publication. They do not fabricate public GRANT or PostgreSQL parity.
No build, test or formatter was run by the implementer. Root must generate/format,
run ordinary quickcheck plus both complete reference modes, feature smokes and
focused races before review/integration. Commands and deadlines are recorded in
[TESTING](../TESTING.md#insert-conflict-omission-acceptance). Final fullcheck is
post-integration, under a single1800-second deadline; failures are separate remedies.
