# reference/alter

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### ALTER TABLE tablespace selected proof

Shared public scenarios exercise validated default placement, exact diagnostics,
direct/prepared metadata and live authority, mixed-action atomicity, held consumers,
native Dump/Restore and named wait/cancel. Named same-space assignment takes AX;
bulk same-space NOWAIT returns before relation selection/locking. Real PostgreSQL
physical phases use genuine user spaces for distinct-space owner filtering,
relocation and NOWAIT/cancellation; these are explicit native reachability limits,
not simulated native successes. Partitioned-index creation and mixed partition
ALTER remain independently asserted native prerequisite boundaries.
Mixed preparation regressions preserve written-order missing-column/destination/
kind diagnostics and cover target-type resolution, collation validation and
assignment coercion, including exact USING hints. USING binding is completed at
its own TYPE position: an earlier missing destination wins over an invalid USING
reference. Every direct/prepared failure checks unchanged data, shape and placement.
Successful TYPE captures, with and without USING, produce exact BIGINT values and
metadata in both assignment orders; each fresh one-row seed expects INSERT count1.
Internal inheritance capture coverage checks the complete original column identity
(including attribute number), unchanged root capture across descendant admission,
and independent parent/child row rewrites from 10/20 to 11/21.
Identity TYPE retains its real sequence-AS prephase after successful target and
USING admission, including when assignment-transformation completion fails.
Raw USING errors retain the caller's cached tail; assignment failure discards
only that caller's tail through actual staged AS, without publishing sequence
metadata or generator changes. Existing failure/prephase, source-consumer and
identity-smoke scenarios cover the distinction without relaxed expectations.
Internal admitted-prefix proofs use actual statement admission, sequence descriptors,
private AS generators, caller cache tails and Finish. They check that completion
failure includes the failing occurrence but excludes later TYPE actions, admission
errors expose no stageable prefix, real AS validation supersedes saved coercion
without clearing an invalid-AS cache, and shared-stage disposal stays with its
owner. These are internal lifecycle invariants, not new PostgreSQL expectations
for public multi-TYPE combinations.
Global movement checks remain distinct from
failing companion preparation and CHECK validation. Bulk literal-role utilities are held across
role creation/removal; quoted `"current_user"` stays literal. Real-space held
identity utilities distinguish prepare-time/current/session roles by exact
selected/excluded bags. Separate multi-candidate cancellation observes an actual
pending AX without assuming scan order: live catalog placement stays original,
retained Rows remain readable, and fresh post-cancel/rollback reads prove the
complete current data bag unchanged. The single-candidate 55006 witness remains.

Run each selector both without and with `REFERENCE=1`, with an external
900-second deadline:

```sh
make check RUN='^TestReferenceAlterTablespace(Syntax|Values|Diagnostics|Kinds)$'
make check RUN='^TestReferenceAlterTablespace(Transactions|Prepared|BulkBuiltin)$'
make check RUN='^TestReferenceAlterTablespace(Locks|PhysicalSelection|PhysicalLocks)$'
make check RUN='^TestReferenceAlterTablespace(Controls|FeatureSmoke)$'
make check RUN='^TestReferenceAlterTablespace(PreparationPrecedence|BulkLiveRoles)$'
make check RUN='^TestReferenceAlterTablespace(PhysicalHeldRoles|PhysicalBulkCancellation)$'
make check RUN='^TestAlterTablespace(Grammar|Preparation|Noop|TypeCapture|InheritanceTypeCapture)$'
make check RUN='^TestAlterTablespace(AdmittedTypePrefix|AdmissionFailureNoPrefix|PrefixErrorOwnership)$'
make check RUN='^TestReferenceAlterTypeIdentity(FailuresAndPrephase|FailureOnlyDiscardsCaller)(SourceConsumerRoutes)?$'
make check RUN='^TestReferenceSmokeAlterTypeIdentitySmoke$'
make check RACE=1 RUN='^TestReferenceAlterTablespace(Locks|Transactions)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceAlterTablespace(Locks|PhysicalLocks|PhysicalBulkCancellation)$'
```

Feature smoke: `make check RUN='^TestReferenceAlterTablespaceFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceAlterTablespaceFeatureSmoke$'`.
Expect default identity/data/metadata preservation, owner/missing/global errors,
successful mixed changes with failed companions atomic, bulk preselection
short-circuit and named lock timeout, plus native persistent control round-trip.

For oracle-first discovery of only the new precedence regression (without a
temporary backend filter), use
`make check REFERENCE=1 RUN='^TestReferenceAlterTablespacePreparationPrecedence/postgres'`.
Then use the unsuffixed top-level selector in ordinary mode for the native red
baseline. Final proof must include both complete modes for all affected definitions.

### Ordinary-inheritance migration checkpoint

`TestReferenceAlterDefaultInheritanceRecursion` is now untagged and shares the
complete five-parent/five-child DEFAULT matrix and subquery rejection through
direct/prepared fixtures. `TestReferenceConstraintRenameInheritanceBoundary`
requires released CHECK/NN protections instead of an ordinary-inheritance native
capability error; its local indexed rename identities remain unchanged.
Root runs each with an external 900-second deadline and repeats without
`REFERENCE=1`; both complete modes are required:

```sh
make check REFERENCE=1 RUN='^TestReferenceAlterDefaultInheritanceRecursion$'
make check REFERENCE=1 RUN='^TestReferenceConstraintRenameInheritanceBoundary$'
```

### ALTER ADD constraint selected proof

Shared untagged definitions use identical native and pinned PostgreSQL bodies,
fixture steps/full diagnostics, genuine held consumers and explicit catalog/
backing/deparse/data assertions. Eligible NOT VALID is a historical-validation
choice, never a DML bypass. Source-derived supplemental successor/attribute
expectations require both selected modes; no parity-only or discovery coverage.

```sh
make check RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|NotValid|Diagnostics|Transactions|Ordering|Provenance|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|NotValid|Diagnostics|Transactions|Ordering|Provenance|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check RUN='^TestReferenceAlterAddConstraint(Locks|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(Locks|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceAlterAddConstraint(Replacement|PrimaryPrerequisite)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(Replacement|PrimaryPrerequisite)$'
make check RUN='^TestReferenceAlterAddConstraintMixedDropModes$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraintMixedDropModes$'
make check RUN='^TestAlterAddConstraintDropModeIsolation$'
make check RACE=1 RUN='^TestAlterAddConstraintDropModeIsolation$'
make check RUN='^TestAlterAddConstraint(BackingRetirement|PrimaryPrerequisite)$'
make check RACE=1 RUN='^TestAlterAddConstraint(BackingRetirement|PrimaryPrerequisite)$'
make check RUN='^TestAlterAddConstraint(ParserValidity|StableKeys|ValidationSelection|ForeignKeyAdmission|ForeignKeySuccessors|ProjectionProvenance|Atomicity|Locks|Controls)$'
make check RACE=1 RUN='^TestAlterAddConstraint(ForeignKeyAdmission|ForeignKeySuccessors|ProjectionProvenance|Atomicity|Locks|Controls)$'
make check RACE=1 RUN='^TestReferenceAlterAddConstraint(Locks|Transactions|Provenance|SuccessorWitness|Controls)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceAlterAddConstraint(Locks|Transactions|Provenance|SuccessorWitness|Controls)$'
```

Affected shared witnesses: `^TestReference(PrimaryKeyBoundaries|UniqueExcludedControls|NotNullPublicExcludedSyntaxAndQuotedDiagnostics|NotNullExternalDeferredValidation|SmokeNotNullSmoke|CompositeTableShape|AlterSchemaPostgresClosure|ForeignKeyNativeControls)$`,
plus existing UsingIndex, ForeignKey TYPE/producer and AlterTypeCheck families.
Select these in both modes. The Provenance regression pins private orphan-before-
ADD plus unrelated/affected TYPE and retained valid-definition obligations; real
Finish/native failure must be observed before exempting historical projections.
SuccessorWitness distinguishes committed immediate predecessors from prior own
updates and proves savepoint recovery. Native controls compare actual emitted
noncyclic SQL replay on PostgreSQL, not an invented server Restore API.
FreshLikeValidity proves invalid source CHECK/NN definitions produce new valid
empty-table constraints without changing source data, flags or logical identities.

Replacement covers every UNIQUE/PK/EX source-target pair, explicit/generated
names, both action orders and direct/prepared routes. It independently checks
new logical/backing identities, canonical-name reuse, held current enforcement,
late build rollback/repair and genuine unrelated live-name collisions.
MixedDropModes covers both mixed RESTRICT/CASCADE directions, direct/prepared
execution and textual ADD first/last against populated parent/child tables.
Successful authorized CASCADE remints the replacement key and removes its
dependent FK; failed later RESTRICT retains the original full catalog/backing
identities, rows, native Dump and held metadata/enforcement. The complete2BP01
record must name the current constraint, not a generic prior-retirement batch.
DropModeIsolation privately checks the same real backing traversal and current
diagnostic subject while proving that original live catalog/storage/roots remain
unchanged. Existing shared dependency callers pass no explicit subject and retain
their ordinary whole-closure diagnostics and authority.
PrimaryPrerequisite pins original invalid-NN admission before same-statement
SET/DROP effects, including dirty history, full55000 Execute diagnostics,
unchanged whole state/held metadata and separate SET validation then PK success.
The private BackingRetirement unit observes exact removals, effective lookup,
original touched-name fences and roots, and no resurrection by later evolution.
The private PrimaryPrerequisite unit observes original sparse attributes and
defers genuinely new keys without changing catalog/storage. Both new reference
bodies require selected native and real pinned modes before review/integration;
prior passing feature groups do not discharge these compositions.

Post-rebase caller proof also selects the migrated FK grammar and affected NN
TYPE identity test, plus the independent ADD COLUMN declaration-order witness:
```sh
make check RUN='^(TestForeignKeyGrammarBoundaries|TestAlterTypeMetadataAndAtomicity)$'
make check RUN='^TestReferenceAlterAddColumnChecks$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddColumnChecks$'
```
Historical CHECK scans use declaration order while ordinary DML keeps the
existing alphabetical traversal. Invalid ADD CHECK Restore uses the same
recursive stored-expression whitelist as CREATE CHECK, including parameter
rejection; it does not widen ordinary INSERT replay. Ownership witnesses create
tables under their actual role instead of relying on excluded ALTER OWNER.
Mixed candidate rollback uses supported ADD COLUMN DEFAULT, not unrelated
SET DEFAULT action-list syntax. Standalone catalog queries explicitly cast
regclass to oid rather than expanding operator admission.
Born-current FK witnesses create their empty table before the explicit
transaction, then retain private INSERT/invalid ADD/update/savepoint/rollback
obligations. This does not expand transactional CREATE.

Separate stdlib/public-API smoke sources live in OMP session files
`local://alter-add-constraint-smoke.go` and
`local://alter-add-constraint-controls-smoke.go`. Root materializes them outside
the checkout and runs `go run` from the worktree with an external900s deadline;
quiet exit0 means every value/count/metadata/full diagnostic/control assertion
passed. They do not wrap `make check` or replace selected dual-mode evidence.
The controls smoke keeps strict noncyclic canonical SQL equality. Exceptional
cyclic forward replay compares the actual persistent constraint graph, valid
and invalid definitions, complete data and retained TEMP rows independently of
OID allocation/declaration order, then exercises both FK directions with exact
failure records and successful writes. Distinct public-driver imports use the
fixture's raw-native recorder so returned rows/metadata are really observed.
Root owns generation/formatting and one post-edit verification pass. External
check/quickcheck/racecheck/bench deadlines are900s; finalcheck is1800s.
Use `make quickcheck` only for completed whole-project Docker-free verification.
