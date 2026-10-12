# reference/patterns_encoding

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Expression collation selected proof

The shared untagged definitions in `reference/patterns_encoding/expr_collation_*`
cover independently expected rows/types/routes and complete diagnostics.
`TestReferenceCollationNativeCollision` additionally preserves the genuine TEMP
allocator collision, exact dependency totals, source immutability, repeated
Restore and real PostgreSQL emitted-SQL replay with stored comparisons.
Expected replay errors use recovered PostgreSQL savepoints, not aborted-transaction
assertion bypasses. Run every changed definition in both modes (external900s):

```sh
make check RUN='^TestReferenceExprCollation(Resolution|Demand|Consumers|Keys|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(Resolution|Demand|Consumers|Keys|FeatureSmoke)$'
make check RUN='^TestReferenceCollation(Identity|Provider|NativeCollision)$'
make check REFERENCE=1 RUN='^TestReferenceCollation(Identity|Provider|NativeCollision)$'
make check RUN='^TestExprCollation(ComparisonAuthority|RetagAuthority|KeyAuthority|AnnotationAST)$'
make check RACE=1 RUN='^TestExprCollation(ComparisonAuthority|RetagAuthority|KeyAuthority|AnnotationAST)$'
make check RUN='^TestReferenceExprCollation(RowOperandDemand|ScheduledPayloadDemand)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(RowOperandDemand|ScheduledPayloadDemand)$'
make check RUN='^TestReferenceGeneratedStoredTableOIDControls$'
make check REFERENCE=1 RUN='^TestReferenceGeneratedStoredTableOIDControls$'
make check RUN='^TestExprCollation(RowOperandDemand|ScheduledPayloadDemand|GeneratedTableOIDReachability)$'
make check RACE=1 RUN='^TestExprCollation(RowOperandDemand|ScheduledPayloadDemand|GeneratedTableOIDReachability)$'
make check RUN='^TestReferenceExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RUN='^TestExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RACE=1 RUN='^TestExprCollation(StoredPreflightDemand|NestedPayloadDemand|DomainDerivation)$'
make check RUN='^TestReferenceExprCollationHash(Demand|Invocation|CTEInvocation|SourceOccurrence)$'
make check REFERENCE=1 RUN='^TestReferenceExprCollationHash(Demand|Invocation|CTEInvocation|SourceOccurrence)$'
make check RUN='^TestExprCollationHashExecutionOwner$'
make check RACE=1 RUN='^TestExprCollationHashExecutionOwner$'
```

Private invariants assert actual retained capabilities, forged-ref admission,
cancellation, equality/hash consistency including BPCHAR domains, hidden BOOL
authority remapping/source immutability and full-authority optimizer eligibility.
RetagAuthority asserts direct stored projection demand skips an unused conflict
without changing source graph, selected provider or schema; full/star demand
retains the original graph and invalid slots reject. RowOperandDemand checks
actual pair providers and side-effect order; ScheduledPayloadDemand exercises
actual sort/evaluation cache producers rather than final cached readers.
GeneratedTableOIDReachability binds genuine first-only/later-only/no-owner row
generation graphs. Shared frontier definitions independently check decisive,
NULL and reached left/right demand, unused sorted/bounded payload, demanded
keys, retained volatile effects, full/star and materialized refresh.
The original generated tableoid controls fixture also binds first/later owner
row predicates to the actual retained TEMP OID. Its replay candidate compares
TRUE before owner remapping and must recompute FALSE afterward; scalar repair,
source-held rows, TEMP identity/state, DML and actual PostgreSQL replay remain.
KeyAuthority imports genuine declared domain values through existing Input,
retaining domain identity and the BPCHAR trailing-space normalization witnesses.
StoredPreflightDemand exercises the admitted ordinary legality route before
stored-source opening, including an unused CASE-to-TEXT payload and demanded
full diagnostics. NestedPayloadDemand checks actual nested projections for
separate same-OID aliases in both shared and distinct lexical frames, while
preserving full frozen ACL/dependency reads. DomainDerivation independently
distinguishes top annotations from nested concat/CASE explicit authority,
nondefault implicit reset and default-domain bubbling; private consumers demand
the actual bound provider and retain child dependencies/properties. RetagAuthority
also checks mapped provider authority, explicit diagnostic location,
consumer execution and source-immutable rendering, not incidental wrapper shape.
DomainDerivation additionally requires exact stored default/analyzed/planned
generation destination Usage while retaining semantic rendering and row demand.
Existing TestStringEncodingRetainedFormalOwnership keeps its genuine mapped
formal descriptor ownership and evaluation assertions. Select it together with
TestCollationStoredExpressionMetadataRetention and TestCollationRetainedProviderProvenance
for native storage/formal-role remapping proof.
HashDemand checks RHS-first empty/NULL/negated/effect/error frontiers.
HashInvocation and HashCTEInvocation check volatile RHS reuse across probes and
plain/OFFSET 0 correlated-parent rescans, versus actual correlated RHS freshness
and new held Execute/bind freshness. HashSourceOccurrence checks same-frozen-view
aliases independently without pinning build order. HashExecutionOwner requires
one counted build, non-consuming NULL evidence, failed/canceled nonpublication,
actual captured alias keys, source immutability and true-owner memory release;
borrowed stage closure must not release the ancestor's table.
Select affected existing consumer definitions separately in both modes; these
small checks do not replace whole-project Docker-free `make quickcheck`.

Public test smoke:
`make check RUN='^TestReferenceExprCollationFeatureSmoke$'` and the same command
with `REFERENCE=1`. Standalone smoke source is supplied in the OMP session file
`local://expr-collation-smoke.go`; materialize it temporarily as
`.expr-collation-feature-smoke/main.go` and run
`timeout 900s go run ./.expr-collation-feature-smoke` from the worktree.
It uses only public Driver/database/sql/Error/Dump/Restore, independently checks
C ordering/bytewise equality, full demand/conflict/noncollatable diagnostics,
types and stored comparison identity/dependencies after Restore. It also checks
nested resolved output and unused CASE-coercion success, then demands their
actual42P22 errors through direct/prepared execution before and after Restore.
Success is quiet exit0; failure reports the actual mismatch. Remove only that temporary
smoke directory afterward; session communication is not a parent deliverable.

### Encoding, locale and float-presentation settings

The eleven shared definitions live in `reference/patterns_encoding/format_encoding_*_test.go`.
Their completed selector must run in both modes before review/integration,
under separate 900-second deadlines, without verbose output:

```sh
timeout 900s make check RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Settings|UTF8|Locales|ICU|FloatPrecision|Consumers|Privileges|Scope|Atomicity|Controls|FeatureSmoke)$'
```

For changed locale numeric-parsing witnesses, validate the existing nested
`to_char(to_number(...),'FM9999.00')` TEXT result and raw NUMERIC decimal codec
against the pinned oracle first; do not admit general Numeric-to-TEXT just to
make a selected-setting witness executable:

```sh
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Locales|Consumers)$'
timeout 900s make check RUN='^TestReferenceFormatEncoding(Locales|Consumers)$'
timeout 900s make check RUN='^(TestFormatEncoding.*|TestExplicitCoercionMatrix|TestStringPlanSpecializedOutputStability|TestSettingUtilitySnapshotOwnership|TestArrayVolatilityPreservesScalarClassification|TestStringGenerationOutputDependence)$'
timeout 900s make check RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$'
timeout 900s make check REFERENCE=1 RUN='^TestReference(SettingsBuiltinRegistry|SettingsShowAllPlaceholdersAndLifecycle|SettingsShowAllCurrentOverrides|SettingsAuthenticatedAccessClasses|Float.*|NumericFormatting.*|DatetimeFormatting.*|StringEncoding.*|AnyValueTypes|ExtremaTransitions|ExtremaTypes|TimezoneBuiltinRegistryDescriptions)$'
```

The executable feature/control smoke is in `format_encoding_controls_test.go`:

```sh
timeout 900s make check RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceFormatEncoding(Controls|FeatureSmoke)$'
```

It asserts UTF8 Unicode transport, C numeric/time masks, current-role message
authority, transactional precision/restoration, held and retained results, and
native exact binary/Dump/Reset/Restore/emitted replay controls. PostgreSQL's
rounded scalar transport and native's exact scalar export are independently
asserted, not normalized into false equality. Native locale/client-encoding
capability errors have explicit backend expectations; ICU is policy only.
Ordinary quickcheck and the single post-integration 1800-second finalcheck remain
required. Commands above are acceptance instructions, not claims they passed.

### Collation objects and retained consumers

The collation-object family covers independent C/builtin identity, provider
metadata and execution modes, validation, version notices, authenticated
ownership, rename/schema/dependency transitions, and native persistence with
PostgreSQL emitted-SQL replay. External ICU/libc data boundaries are explicit;
native never substitutes C for an unavailable provider. Run every changed
definition in both modes, each under a 900-second deadline:

```sh
make check RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
make check REFERENCE=1 RUN='^(TestReferenceCollation.*|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceTemporaryTable.*|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
```

The cohesive feature selector is
`RUN='^TestReferenceCollation(Identity|Provider|Version|SeedAlterLifecycle|SeedTemporaryLifecycle|SeedNativeReplay|Lifecycle|Dependencies|NativeLifecycle|NativeCollision|CompositeNativeCollision|TemporaryLifecycle|TemporaryQualification|TemporaryRetainedConsumers|SimilarCopiedUnicode|ExternalBoundary)$'`.
Expected observations include independent object OIDs, real ASCII/Simple/Full
case behavior, NULL versus version `1`, stable permanent moves and mutable pinned
seed metadata, actual owner relationships without a pg_roles fixture dependency,
seed delta Dump/Reset/Restore and actual PostgreSQL emitted replay, column-cascade
surviving data, and repeated independent native Restore. TEMP witnesses cover
fresh creation, same-role/other-role foreign qualification42501, physical owner
end's atomic durable cascade, into/out-of-TEMP moves0A000 without mutation, portable
omission or fail-before-output, and actual import/target OID collisions across all
provider usages. Native control phases remain explicitly native-only.
Provider encoding is independently checked across actual PostgreSQL replay:
direct builtin `C` uses-1, while direct `C.UTF-8`/`PG_UNICODE_FAST` use UTF8 encoding6.
FROM copies retain source encoding; libc `C`/`POSIX` copies use-1 versus direct
libc options6, and copied builtin `C` from `ucs_basic` uses6 versus direct builtin
`C`-1. Only exceptional copies need immutable bootstrap FROM reconstruction.

The copied-provider SQL-regex consumer witness independently distinguishes
ASCII, C.UTF-8 and PG_UNICODE_FAST alphabetic, decimal and punctuation classes,
with source/pattern explicit COLLATE, direct/prepared context and non-context
routes, BOOL metadata and implicit copied identities in stored views. Run:

```sh
make check RUN='^TestReferenceCollationSimilarCopiedUnicode$'
make check REFERENCE=1 RUN='^TestReferenceCollationSimilarCopiedUnicode$'
```

Change note: SIMILAR class dispatch now follows the registered immutable provider
capability rather than bootstrap object OIDs; seed class semantics are unchanged.

The TEMP lifecycle witness also retains a real TEMP table, SERIAL-owned sequence
and index across native Reset/Restore, then asserts their physical-end cleanup
alongside the durable dependent-column projection. DEFAULT, CHECK and generated
provider manifests retain genuine collation dependencies through both private
validation and final publication fences. Native absent `pg_catalog."C.utf8"`
lookup is42704; explicitly requesting its unavailable libc provider remains0A000,
while the PostgreSQL witness uses its installed libc locale without substitution.

Change note: retained TEMP DEFAULT, CHECK and generated manifests preserve their
lawful captured collation references across repeated Reset/Restore. Older epochs
are admitted only for the exact live physical-owner consumer and its same-owner
TEMP provider, or an actual validated pinned seed, with exact class/SubID and
matching immutable capability. Subsequent DDL, default INSERT, prepared UPDATE,
failed CHECK atomicity, dependency identity and physical cleanup are exercised by
`TestReferenceCollationTemporaryRetainedConsumers`. After each catalog replacement,
ALTER of a separate seed default republishes that exact retained relation; new
inserts observe the changed default while the other explicit-COLLATE definitions
and their captured provider manifests survive unchanged.

`TestReferenceCollationCompositeNativeCollision` covers existing supported
composite member collations, composite domains/defaults/data and stored views:
Dump uses qualified live provider names, genuine target TEMP collisions remap
member capabilities over repeated native Restore, and emitted SQL is replayed
against PostgreSQL. Composite-array column admission remains unchanged and is
not claimed by this witness.

Stored scalar metadata no longer retains binding catalog roots. Name resolution
uses the actual ephemeral binding or private-candidate retag context; pure
reference/slot remapping preserves already validated provider metadata. Genuine
package regressions are `TestCollationRetainedProviderProvenance` (foreign,
stale/missing, class/SubID, physical owner and capability negatives plus full
DEFAULT/CHECK/generated serializer manifests, including captured epochs) and
`TestCollationStoredExpressionMetadataRetention` (repeated
real view/default replacement and legitimate held old definitions).

The generalized ARRAY constructor and subscript binders use that same actual
binding context; their existing retag visitor forwards the private candidate
and provider remapper. The obsolete REGCLASS-only array visitor is removed,
not retained as a second array authority. Shared ordinary/materialized-view
freezing captures collation identities once alongside the other definition facts.

Change note: array-aggregate providers remap their complete result Usage,
including the registered collation capability, alongside their argument type;
the copied provider never changes retained source authority.
`TestArrayAggregateCollationViewRemap` compares complete provider/schema Usage
for genuine scalar/array Full-to-ASCII capability mapping and source immutability.
`TestReferenceCollationNativeCollision` independently checks collated stored
`array_agg` rows/metadata, propagated subscript case behavior, repeated genuine
TEMP-collision Restore, source isolation and emitted-SQL PostgreSQL replay.

Change note: explicit COLLATE serialization uses the common captured-aware
provider record only; it must not append a second current-epoch reference for an
older lawful retained definition. Provider validation reads structural catalog
snapshots only. Owner/ACL validation remains at the synchronized shared stage and
final publication fences, rather than rereading mutable roles inside the provider.
`TestCollationProviderConcurrentRolePublication` overlaps genuine session role
publication with snapshot provider validation. Its race gate is:

```sh
make check RACE=1 RUN='^TestCollationProviderConcurrentRolePublication$'
```

The already integrated `TestReferenceLikeLegacyMigration` and its stage/namespace
scenarios remain the single owner of the former LIKE smoke assertion union; the
duplicate collation-named migration is removed. Required final selective gates
also explicitly include the three changed non-collation-named scenarios:

```sh
make check RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
make check REFERENCE=1 RUN='^(TestReferenceCollation.*|TestReferenceLikeLegacyMigration|TestReferenceLikeLegacyStageConsumers|TestReferenceLikeLegacyMissingNamespace|TestReferenceAlterTypeNondefaultLocaleBoundary|TestReferenceVisibilityInquiryBoundaries|TestReferenceVisibilityInquiryUtilityBoundaries)$'
```

Each invocation has a 900-second deadline. Complete-suite passes do not replace
these selective dual-mode gates.

Owned TEMP targets are excluded from the private stage before projecting surviving
dependents, so a dropped TEMP table never also publishes a replacement row shape.

Supplemental genuine package regressions include
`TestCollationBootstrapPrincipalNameReuse`, `TestCollationPrivateSessionIdentity`
and `TestCollationTemporaryCleanup(AX|CompletenessFence|Exhaustion)`. The baseline
has no public role-rename SQL: the name-reuse fixture clones and coherently
publishes the actual role catalog under existing live authority, retaining real
identities/attributes/memberships, then creates the replacement through real DDL.
It exercises real metadata capture, owner denial, Reset/Restore and private cleanup;
it does not bypass owners/pins or advertise ALTER ROLE support.

### SQL SIMILAR oracle and feature checks

The untagged `TestReferenceSimilar` families share independent direct/prepared
expectations for SQL matching/extraction, classes/escapes/capture, diagnostic
fields/stage/positions, metadata/deparse, demand/held parameters, consumers,
stored atomicity and public replay. Discovery-only backend filters and failure
logging were removed after the root's pinned PostgreSQL18.0 observations.
Review regressions are `TestReferenceSimilarReviewDefault`,
`TestReferenceSimilarReviewMalformedDefault`,
`TestReferenceSimilarReviewDefaultControls`,
`TestReferenceSimilarReviewDefaultBoundaries`,
`TestReferenceSimilarReviewGroupIdentity` and
`TestReferenceSimilarReviewTaggedIterator`. They encode the pinned default
diagnostic, all sixteen cross-spelling grouping pairs and eight exact
nullable-iterator/backreference capture outcomes. Native-only DEFAULT boundaries
and host controls are explicit; the controls replay actual emitted SQL on the
pinned backend. `TestSimilarSubstringPresentationIdentity`,
`TestPGRegexExecutionReplacementOwnership` and
`TestPGRegexExecutionReplacementBudget` are genuine structural/injected-limit
units; the latter cover configuration-preserving invalidation, preallocation
replacement ownership, translation/compile/runtime failure, NULL, cancellation
and recovery. They do not claim a PostgreSQL-identical resource cutoff.
`TestReferenceSimilarActualCancellation` executes the actual regex UPDATE;
server admission is observed before cancellation, and native cancellation
observes entry into translation/compiler/matcher work. It must not substitute
`pg_sleep`, a precanceled context or readiness for exercised cancellation.

Root runs both modes before review, each under the Makefile's 900-second bound:

```sh
make check RUN='^TestReferenceSimilar'
make check REFERENCE=1 RUN='^TestReferenceSimilar'
make check RUN='^TestReferenceSimilarFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSimilarFeatureSmoke$'
make check RUN='^TestReferenceSimilarReview'
make check REFERENCE=1 RUN='^TestReferenceSimilarReview'
make check RUN='^(TestSimilarSubstringPresentationIdentity|TestPGRegexExecutionReplacement(Ownership|Budget))$'
make check RUN='^TestReference(BinarySlice|LikeLegacy)'
make check REFERENCE=1 RUN='^TestReference(BinarySlice|LikeLegacy)'
```

The BinarySlice capability selector preserves positional TEXT
exclusions but admits two-TEXT POSIX and all three SQL-regex substring spellings. LIKE
legacy migration retains its independent matching/metadata/replay assertion
union. Genuine parser/provider/bound-node units supplement public reference
coverage; they must not recreate SQL-through-internal-engine tests. Native
resource-accounting units use injected limits rather than claiming a portable
PostgreSQL complexity cutoff. Finalcheck belongs to the root after merge
under one 1800-second deadline.

### POSIX regex oracle and feature checks

`TestReferencePosix` is backend-first and shares independent direct/prepared
expectations for search/case/negation/NULL, ARE/ERE/BRE, captures and hierarchical
preferences, brackets/escapes/constraints/lookaround/options/newlines, effective
collation/physical character types, full English diagnostic fields/stages/positions,
held inferred parameters, stored consumers and atomic native controls/PG replay.
Initial pinned18.0 discovery passed before native red authorized production;
no discovery backend filter is retained. Provider all-capture/span/budget and
parser/bound-node units supplement, not replace, these public expectations.

Root verifies after all provider/consumer edits land, with external900-second
deadlines (targets themselves own no deadline):

```sh
make check RUN='^TestReferencePosix'
make check REFERENCE=1 RUN='^TestReferencePosix'
make check RUN='^(TestPGRegex|TestPosix|TestSimilar|TestReferenceSimilar|TestReferenceCollation|TestReferenceJSONPath|TestReferenceBinary|TestReferenceLike)'
make check REFERENCE=1 RUN='^(TestReferenceSimilar|TestReferenceCollation|TestReferenceJSONPath|TestReferenceBinary|TestReferenceLike)'
make check RACE=1 RUN='^(TestPGRegex|TestPosix|TestReferencePosix(ActualCancellation|StatementTimeout|HeldParameters|Controls)|TestReferenceSimilarActualCancellation)'
make check RUN='^TestReferencePosixFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePosixFeatureSmoke$'
```

Review witnesses isolate constructor-specific titlecase closure and nested
assertion continuation without adding another public regex function:

```sh
make check RUN='^TestReferencePosix(TitlecaseConstructors|NestedAssertions)$'
make check REFERENCE=1 RUN='^TestReferencePosix(TitlecaseConstructors|NestedAssertions)$'
```

`TitlecaseConstructors` covers literal/plain bracket/equivalence versus explicit
singleton range/collating element for Ǆ/ǅ/ǆ under C, POSIX, both admitted Unicode
providers and their copies, including sensitive/insensitive negation and
two-TEXT substring. Independent REL_18_0 `regcomp.c`/`regc_locale.c` expectations
retain ǅ only in insensitive range/collating construction under Unicode; literal,
plain bracket and equivalence contain only its simple lower/upper counterparts.
`NestedAssertions` checks successful continuation and failing children, both
polarities, noncapturing assertion groups, variable-width lookbehind containing
lookahead beyond its endpoint, and UTF8 endpoints. The release `rege_dfa.c`
`lacon` evaluates child lookahead against the original source end, not the
parent lookbehind endpoint. These source-grounded expectations require both
selected native and pinned runs; native output is never the expected-value oracle.

Binary POSIX admission is arity-specific: unary `~`, including qualified forms,
remains the existing bitwise operator. Retain `TestBitwise` structural and
`TestReferenceBitwise`/`TestReferenceSearchPath.*Bitwise` public regression
coverage in both applicable modes. The migrated
`TestReferenceSimilarReviewDefaultBoundaries` independently expects the two-TEXT
substring DEFAULT to return `"a"`, while positional TEXT/BYTEA and unrelated
function DEFAULT exclusions remain native-only. Aggregate `min(substring(...))`
and pg_get_* inquiries retain native known-nullable metadata and PostgreSQL
adapter unknown nullability; regex expression metadata remains unknown.
Failed late Restore uses an admitted stored regex DEFAULT followed by INSERT,
not a scalar SELECT rejected by Restore's sequence-state whitelist.
Unary `~` also retains Restore's binder-owned malformed-type diagnostics.
POSIX substring occurrences carry independent stored-view provenance, so
ordinary pg_get_viewdef output quotes `"substring"` without relabeling it
SQL SIMILAR; pg_get_indexdef classifies the POSIX node as a function key without
an extra expression wrapper. Existing pinned definition goldens remain unchanged.

```sh
make check RUN='^(TestBitwise|TestDeparseConstraintsPersistedFamilies|TestReferenceBitwise|TestReferenceSmokeBitwiseSmoke|TestReferenceSearchPath.*Bitwise|TestReferenceSimilarReviewDefaultBoundaries)'
make check REFERENCE=1 RUN='^(TestReferenceBitwise|TestReferenceSmokeBitwiseSmoke|TestReferenceSearchPath.*Bitwise|TestReferenceSimilarReviewDefaultBoundaries)'
```

Changed BinarySlice and SIMILAR cancellation definitions require both native and
real pinned modes. ActualCancellation observes common `PGRegexProgram` matcher
entry before canceling a real UPDATE; StatementTimeout asserts57014, unchanged
sentinel and healthy same-connection reuse. Precancel/readiness is not entry proof.
Reserve `make quickcheck` for the whole-project Docker-free gate. Root runs
post-merge `make finalcheck` under an external1800-second deadline.

Standalone smoke source is session-owned `local://posix-smoke.go`, materialized
outside the checkout. From the worktree run `go run "$SESSION_TMP/posix-smoke.go"`.
It uses public driver/control/error APIs only, asserts all four operators,
first capture/greediness/absent/empty/BRE/lookbehind/newline behavior, held
replacement diagnostics, stored definitions, atomic UPDATE and stable
Dump/Reset/Restore with retained Rows, then prints `POSIX smoke: ok`.
No permanent smoke scaffolding or internal observation import is introduced.

### Regular-expression function oracle and feature checks

The unfiltered `TestReferenceRegexp` definitions retain independent expected
values, diagnostics/stages/character positions, typed metadata and direct/
semantic-prepared execution for the exact eight selected functions. The initial
pinned18.0 observations authorize these expectations; production success still
requires fresh selected native and real runs after the edits. External `e` and
`qe` are BRE on the pinned release, whereas embedded `(?e)` is ERE. The public
assertion union must not be reduced or replaced by native-generated expectations.

Array ordering/index opclasses and constrained-domain DDL are existing adjacent
capability limits, not regexp prerequisites supplied by this change. Shared
scenarios assert PostgreSQL success and exact native boundaries: array ORDER BY
42883 plus its ordering hint, array btree opclass42704 plus its opclass hint,
and domain CHECK0A000. Array wrapper results are also compared as complete bags;
all scalar overloads retain real DEFAULT/generated/table CHECK outcomes, with
array calls in supported scalar-key index predicates and plain-domain coercion.
No supported array grouping/equality or selected regex result assertion is
removed. Direct COUNT/ROW_NUMBER metadata requires native known NOT NULL and
pq unknown, without changing the unknown metadata of computed regex calls.

Outer-join null extensions and nullable base-table fields retain native known
nullable metadata versus pq unknown; window-key ORDER BY alone does not order
the result, so that witness compares a complete bag. Stored-expression inquiry
uses independently specified `pg_attrdef.adnum` ordinals and the admitted explicit
`regclass::oid` cast instead of the absent
`pg_attribute` prerequisite. View inquiry selects its complete independently
expected SQL definition, retaining function/ordinality/renamed-source assertions
without extending inquiry-LIKE grammar. Supported INSERT/UPDATE RETURNING and
COUNT window routes prove SRF prohibitions using the same shared expectations;
SUM-window and first_value retain exact native capability errors versus
PostgreSQL's selected SRF errors. Missing no-argument parameter admission remains
native42P08 versus pq42P02, while provided-argument conflict42P08 is shared.
FILTER owns its SRF error before the enclosing aggregate argument owner, without
the aggregate/window LATERAL Hint.

`TestReferenceRegexpQueryBoundaryScalars`, `FilterValues` and `Restrictions`
exercise query-local placement contexts: selected target SRFs inside child SELECTs
remain legal beneath CASE, COALESCE, aggregate FILTER and multirow INSERT VALUES.
Independent witnesses cover correlated/nested children, zero-row scalar NULL and
fallback, scalar cardinality 21000, EXISTS counts, exact inserted counts and late
failure atomicity. A later direct parent SRF remains prohibited with its original
context, Hint, stage and UTF8 Position; a preceding legal child cannot erase that
restriction. The same definitions run unchanged in both modes.

`TestReferenceRegexpJoinDependencyRightFull`, `Prefixes`, `Diagnostics` and
`Views` pin the boundary-relative distinction between local-left and inherited
prefix captures. Their complete direct/prepared witnesses retain internally
dependent RHS joins, empty-left RIGHT/FULL preservation, genuine inherited
offsets, exact mixed-prefix forbidden-reference diagnostics, independently
specified compact view SQL, replay, renames and real stored dependencies.
These definitions must remain unchanged across native and pinned selections:

```sh
make check REFERENCE=1 RUN='^TestReferenceRegexpJoinDependency'
make check RUN='^TestReferenceRegexpJoinDependency'
make check RUN='^TestRegexpCorrelationFrameRebase$'
make check RACE=1 RUN='^TestRegexpCorrelationFrameRebase$'
```

The private component proof executes actual invocation stages with divergent
lexical and private virtual-owner frames before/after prefix normalization;
it verifies retained argument ownership and prevents rebasing already-local
right invocations twice. It is not a public-SQL replacement or an allocation/
identity-wiring assertion. Root owns fresh verification; these commands are
required proof selections, not claims that this remedy has passed.

Root generates parser code and formats once after all edits land, then runs
selected proof under external900-second deadlines:

```sh
make check RUN='^TestReferenceRegexp(Signatures|NamedArguments|Inference|Metadata)$'
make check RUN='^TestReferenceRegexp(CountInstrLike|CaptureArrays|NullDemand)$'
make check RUN='^TestReferenceRegexp(Flags|Diagnostics|Collation)$'
make check RUN='^TestReferenceRegexp(Replacement|Splits|GlobalBoundaries)$'
make check RUN='^TestReferenceRegexp(SetProjection|SetFrom|SetCorrelation)$'
make check RUN='^TestReferenceRegexp(SetComposition|SetPlacement|SetViews)$'
make check RUN='^TestReferenceRegexp(Consumers|StoredDefinitions|Controls|Atomicity)$'
make check RUN='^TestReferenceRegexp(ActualCancellation|StatementTimeout|HeldParameters)$'
make check RUN='^TestReferenceRegexpQueryBoundary(Scalars|FilterValues|Restrictions)$'
make check RUN='^TestRegexpFunctions(Flags|OriginalStart|Iteration|Captures)$'
make check RUN='^TestRegexpFunctions(Replacement|Split|Ownership|Budget|LoopCancellation)$'
make check RUN='^TestRegexpSet(Projection|Nested|Correlation|Ownership|Walks)$'
make check RUN='^TestRegexp(ScalarBinding|ScalarStrictChildren|ScalarPreflight|ScalarGroupingAlias|ScalarOuterFromFrame|ScalarFilterPlacement|ScalarViewPrefix|FunctionSyntax|FromSyntax)$'
make check RACE=1 RUN='^(TestRegexpFunctions(Ownership|Budget|LoopCancellation)|TestRegexpSet(Ownership|Correlation)|TestReferenceRegexp(ActualCancellation|HeldParameters|Controls))$'
make check RUN='^TestReferenceRegexpFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRegexpFeatureSmoke$'
```
Each public definition group above also requires its identical selected command
with `REFERENCE=1`. Shared-provider regression seeds, also in both modes, are
`^TestReferencePosix(SearchCase|OptionsNewlines|TypeCollationDemand|HeldParameters|TitlecaseConstructors|NestedAssertions)$`,
`^TestReferenceSimilar(FeatureSmoke|ReviewTaggedIterator|ReviewGroupIdentity)$`,
`^TestReferenceBinarySliceCapabilityBoundaries$` and
`^TestReferenceJSONPath(Regex|ReviewRegex)$`. Native provider demand regression:
`make check RUN='^(TestPGRegexBoolResultDemand|TestPGRegexCaptureVectors|TestSimilarSubstringCaptureFreeBudgetAndScratchReuse)$'`.


`TestRegexpFunctionsFlags`, `OriginalStart`, `Iteration`, `Captures`,
`Replacement`, `Split`, `Ownership`, `Budget` and `LoopCancellation` are genuine
sqltype units. They inspect actual demand/spans, owned typed capture payloads,
configuration-preserving resource invalidation and active consumer cancellation,
not SQL through an internal engine. `TestRegexpScalarBinding`,
`TestRegexpScalarStrictChildren` and `TestRegexpScalarPreflight` exercise real
bound plans and all-child strictness/invariant versus row-dependent demand.
`TestRegexpScalarGroupingAlias` inspects actual alias keys and ambiguity, and
`TestRegexpScalarOuterFromFrame` inspects the nested bound query graph to require
a distinct inner function frame retaining its captured lexical outer column frame1.
`TestRegexpScalarFilterPlacement` invokes the real FILTER binder with an active
aggregate argument owner, asserting exact message/no Hint and restored scopes.
`TestRegexpScalarViewPrefix` inspects actual analyzed stored-view rendering,
including a nested RHS prefix inherited from its enclosing dependent join,
without executing SQL through an internal-engine mock or deriving a golden.
Its public inquiry branch separately rejects Dump-style quoted bridge aliases
while retaining the same actual captured lexical references. The public SetViews
definition remains a complete independently pinned SQL golden, not a substring
or native-generated parity expectation.
`TestRegexpFunctionSyntax` and `TestRegexpFromSyntax` inspect actual parser ASTs,
including selected source positions, labels, ordinality and ROWS FROM structure.
The set units `TestRegexpSetProjection`, `Nested`, `Correlation`, `Ownership` and
`Walks` now exercise the shared project-set/FROM/join owners, rewrites, fixed
templates, frame prefixes, padding, ordinality and owner reuse directly.
`TestRegexpCorrelationFrameRebase` preserves caller-owned arguments, distinct
lexical frames and private virtual-owner windows through normalization.
`TestFunctionSourceMixedRows` exercises mixed typed padding, scalar strict NULL
and ordinality; `TestQuerySetMixedAttemptOwnership` checks bare terminal draws
against emitted-only upper wrappers. `TestFunctionRecheckOwnedRegexpCursor`
checks immutable constant-vector reuse, refreshed dependent arguments and held
values after cache closure. Existing string component units retain their
assertions while using the same closed call storage and invocation context.

The independent shared definitions `TestReferenceRegexpMixedProjection` and
`TestReferenceRegexpMixedFrom` cover mixed targets, nesting, wrappers, hidden
keys, held plans, terminal sequence counts, ROWS FROM and exact P/L boundaries.
Root runs both `make check RUN='^TestReferenceRegexpMixed(Projection|From)$'`
and the same definitions with `REFERENCE=1`, then the full affected selected and
incumbent string groups, race proof and standalone smoke. Their SQL/row/error/
sequence-count goldens are not generated from native output.

The standalone session-only regexp smoke exercises the public driver, held
plans/cursors, actual correlated sets, typed metadata, stored consumers,
Dump/Reset/Restore and atomic error recovery. It must print `regexp smoke: ok`
only after all assertions pass; it is not a repository deliverable. Native and
pinned feature-smoke definitions must both pass before review. No readiness
claim, generated artifact or unexercised cancellation replaces observed proof.
Whole-project Docker-free verification is reserved for root `make quickcheck`;
post-merge finalcheck uses a separate external1800-second deadline.
