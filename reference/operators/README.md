# reference/operators

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root. This package owns the user-defined operator scenarios and
`TestReferenceRegOperatorExternalBoundary`; the remaining
[operator OID alias scenarios](../type_resolution/README.md#operator-oid-aliases)
stay in `reference/type_resolution`.

### Routine parameter binding

This existing callable-resolution package also owns
`routine_parameters_{helpers,calls,defaults,diagnostics,persistence}_test.go`,
`routine_parameters_builtin_labels_test.go` and
`routine_parameters_named_path_test.go`. The definitions cover function
modes/defaults/named and mixed calls, input-only identity, scalar OUT/INOUT,
expanded/explicit VARIADIC and shared provider/path selection.
Query_core retains its original body scenarios; only its obsolete native
default-rejection case is removed.

The same unfiltered scenario runs on all available backends. Independent
expectations were established against pinned18.6 before the native cutover,
including full diagnostics/stages, scalar OUT/FROM and array metadata,
zero-actual variadic defaults and decoration of a nonvariadic array routine.
PostgreSQL-only discovery does not exercise native controls or emitted SQL;
those phases require the completed unfiltered dual-mode runs.

Independent outcomes include named-gap11/INT4; held defaults1 then2 with explicit
42 consuming none; input-only OUT4→5/INT4; `{1,2,3}`, `{}` and SQL NULL as distinct
array results; stored named102 versus fresh10102, nested stored102 versus fresh
1102; and stored variadic `{1,2,NULL}` versus newly selected `{99}`.
Defaults capture selected helper1001 while a fresh helper call selects2001.
Each persistence scenario includes two native Dump→Reset→Restore rounds.
`Persistence` retains the original `[0:1]={7,NULL}` constrained-domain array,
metadata, prepared/held calls, late-invalid/canceled replacement, no-prefix
publication and old Rows. Its explicit native-provenance helper uses the existing
[domain NULL replay authority](../types/README.md); ordinary PostgreSQL SQL
does not consume that native comment payload. Both independently populated
backends still check every original SQL expectation.
`PortablePersistence` uses a separate fresh catalog: constrained-domain
`[0:1]={7,8}` plus whole NULL/empty arrays, and plain integer
`[0:1]={7,NULL}` plus whole NULL/empty arrays. It checks domain-array
formal/default/result remapping and executes each unchanged emitted stream on
PostgreSQL. WrittenCapturePersistence, VariadicCapturePersistence and DefaultIdentity
also retain their unchanged emitted-SQL PostgreSQL proof.
`SequenceDefaultPersistence` independently requires an initially uncalled
sequence and then exact state106 before each replay-phase reset, matching
independently written table state. Direct/prepared default calls through plain,
stored-view and frozen-standard callers yield100 through105; held explicit
42/NULL/7 consume no default, so the next omission yields106. Both native rounds
and their unchanged PostgreSQL SQL streams preserve those declarations and
state. Full metadata retains native stored-column known nullability separately
from computed-expression and pq unknown nullability.
Multiple-output records, SQL CALL and PL copyback belong to downstream owners.

`BuiltinLabels` preserves incumbent named datetime, regexp, parse_ident, JSON
and polymorphic array routes in competition with SQL overloads; an explicit
bigint call also selects the SQL sentinel instead of blindly deferring to a
builtin. `NamedPath` requires candidate-specific labels and mapped supplied
types before path masking, while positional signatures retain existing masking.
Pure provider applicability never binds/evaluates a losing candidate.

Review regressions extend the same owning files without duplicating baseline
routes. `DefaultDeclarationVisibility` requires a newly declared integer
overload's default to capture the preexisting bigint overload (1000, not1),
while explicit integer calls retain1/42. Stored and held calls, hard dependency
identity, absent-first-declaration diagnostics/no publication, corrected reuse
and two native/unchanged-PG replay rounds are included.
`DomainRecipeRefresh` renames a domain used inside an integer-returning frozen
callee. Separate domain DEFAULT and CHECK owners still expose only integer/bool
expressions: fresh defaults remain1; CHECK accepts2/3/NULL and rejects0 without
publishing a row. Direct/prepared/held routes and two portable replay rounds
exercise both owners. Full diagnostic fields/stages were observed on pinned18.6
in root tests-47 before the genuine native failures in tests-48. This oracle/red
evidence does not claim passing verification of the subsequent remedies.

Root-owned selected commands (external900-second deadline each):

```sh
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|DefaultIdentity|Variadic|ScalarOutputsIdentity|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|DefaultIdentity|Variadic|ScalarOutputsIdentity|Diagnostics)$'
make check RUN='^TestReferenceRoutineParameters(WrittenCapturePersistence|VariadicCapturePersistence|Persistence|PortablePersistence|SequenceDefaultPersistence)$' REFERENCE=1
make check RUN='^TestReferenceRoutineParameters(WrittenCapturePersistence|VariadicCapturePersistence|Persistence|PortablePersistence|SequenceDefaultPersistence)$'
make check RUN='^TestReferenceRoutineParameters(BuiltinLabels|NamedPath)$'
make check RUN='^TestReferenceRoutineParameters(BuiltinLabels|NamedPath)$' REFERENCE=1
make check RUN='^TestReferenceRoutineParameters(DefaultDeclarationVisibility|DomainRecipeRefresh)$' REFERENCE=1
make check RUN='^TestReferenceRoutineParameters(DefaultDeclarationVisibility|DomainRecipeRefresh)$'
make check RUN='^(TestRoutineParameterLayout|TestSQLCallableParameterMapping|TestSQLRoutineWrittenCapture|TestSQLRoutineParameterRemap|TestSQLRoutineDefaultAddressRemap|TestSQLRoutineTypeGenerationRefresh|TestSQLRoutineDomainRecipeTypeGeneration|TestRoutineDefaultOriginRestoration|TestSQLRoutineNamedProviderShapes|TestSQLRoutineDumpEventsUseResolver)$'
make check RUN='^TestReferenceRoutineParameters(DefaultEvaluation|WrittenCapturePersistence|VariadicCapturePersistence|SequenceDefaultPersistence)$' RACE=1
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|ScalarOutputsIdentity|Variadic|WrittenCapturePersistence)$'
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|ScalarOutputsIdentity|Variadic|WrittenCapturePersistence)$' REFERENCE=1
```

The last group is the permanent feature-smoke selection; it reuses the owned
definitions instead of duplicating their SQL routes in a wrapper test.


### Shared alias boundary

`regoperator_external_test.go` retains quoted-schema output, ordered exact
signature identities, unary NONE, search-path masking, cross-schema name-only
ambiguity and the full stored-constant DROP dependency diagnostic.
`fixture.RegOperatorRows` and `fixture.RegOperatorDiagnosticRoutes` own the same
raw transport, complete metadata and direct/prepared/held error-route assertions
used by the remaining alias scenarios; the move does not duplicate their bodies.

```sh
make check RUN='^TestReferenceRegOperatorExternalBoundary$' REFERENCE=1
make check RUN='^TestReferenceRegOperatorExternalBoundary$'
```

### Namespace and arity membership

`TestReferenceUserOperatorNamespaceArity` installs unary and binary `#~#` in
two schemas and reverses their search-path order. Binary results are -1 then 5;
prefix execution remains -3, and exact alias identities distinguish both arities.
SET SCHEMA and DROP preserve the remaining definitions and complete no-match
diagnostics. Its body and global test name are unchanged by package migration.

```sh
make check RUN='^TestReferenceUserOperatorNamespaceArity$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorNamespaceArity$'
make check RUN='^TestReferenceUserOperatorPeerSetupCheckpoints$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorPeerSetupCheckpoints$'
```

The selector finder discovers these unchanged top-level names in this package;
do not add a package-selection flag. Every migrated definition requires both
modes after the move, followed by root-owned whole-project quickcheck and the
post-integration finalcheck. Use external 900-second deadlines for selected
checks and quickcheck, and 1800 seconds for finalcheck.

### Polymorphic unknown-input selection

`TestReferenceUserOperatorPolymorphicUnknownSelection` retains PostgreSQL §10.2
[Example 10.4](https://www.postgresql.org/docs/18/typeconv-oper.html): a known
integer array and unknown array literal select builtin array inclusion. The
result is one `v` column of type BOOL: true for RHS `{1,2,3}` and false for
`{2,3}`. The same pinned-session scenario runs before and after making a
nonmatching custom `<@(int4,int4)` visible ahead of pg_catalog. Direct execution
and literal Prepare independently require both outcomes; explicit
`OPERATOR(pg_catalog.<@)` and fully typed arrays require the same true/false
controls, not substitutes for the unknown-input expression.

In each phase, one genuinely held `SELECT ARRAY[1,2] <@ $1 AS v` statement leaves
the parameter uncast, requiring array inference during Prepare. Reuse with
`{1,2,3}`, `{2,3}`, NULL and `{1,2,3}` requires BOOL true, false, NULL and true.
Actual integer calls to the custom int4eq implementation require true for 2/2
and false for 2/3, through both unqualified and qualified custom calls.
Expectations are explicit, not learned from native output or parity alone.

The final unknown-assume-known phase uses the known base-type descriptor and
the shared polymorphic input-viability helper for both signature positions.
`TestUserOperatorResolutionPolymorphicUnknown` supplements the public regression:
real INT4[] and domain-over-array descriptors select ANYARRAY/ANYARRAY over
ANYELEMENT/ANYRANGE alternatives and an irrelevant integer signature, reversing
the complete candidate order and testing either known side with corresponding
range signatures. This restores existing selection, not new array/range providers.

Root-owned oracle-first discovery and subsequent selected proof (external900s):

```sh
make check RUN='^TestReferenceUserOperatorPolymorphicUnknownSelection$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorPolymorphicUnknownSelection$'
make check RUN='^TestUserOperatorResolution(PolymorphicUnknown|OrderedTypes|UnknownPositions)$'
make check RUN='^TestUserOperatorResolution(PolymorphicUnknown|OrderedTypes|UnknownPositions)$' RACE=1
```

For initial discovery, follow the shared guide's temporary PostgreSQL-only
backend selection, then remove it before observing the native regression.
Repeat both complete modes after the shared resolver correction; never retain a
backend bypass or treat earlier proof as approval of changed definitions.

### Whole-signature polymorphic consistency

`TestReferenceUserOperatorPolymorphicConsistency` exercises the existing array
concatenate, append and prepend providers before and after a genuine custom
`userop_concat.||(int4,int4)` becomes visible ahead of pg_catalog. Its implementation
is the existing `pg_catalog.int4pl`; unqualified and qualified integer calls must
return one `v`/INT4 value 5 for operands 2 and 3, directly and through Prepare.

In both phases, `ARRAY[1,2] || '{3,4}'`, `'{1,2}' || ARRAY[3,4]` and fully typed
`ARRAY[1,2] || ARRAY[3,4]` independently require `{1,2,3,4}`. Genuine array/element
append `ARRAY[1,2] || 3::int4` and prepend `1::int4 || ARRAY[2,3]` independently
require `{1,2,3}`. Each query runs directly and with literal Prepare, asserting
exactly one row and one `v` column with database type `_INT4`, raw array transport
and the existing native/PostgreSQL ScanType contracts.

Each phase also holds two genuinely prepared statements, `ARRAY[1,2] || $1`
and `$1 || ARRAY[3,4]`, with no cast on the inferred array parameter. The same
statement is reused for an array value, NULL, a different array and the original
value. NULL is an identity operand for array concatenation: the independent
results are `{1,2}` and `{3,4}`, respectively, not SQL NULL. Subsequent executions
must recover their exact concatenated values and `_INT4` metadata.

This regression requires signature-wide polymorphic viability, not merely an
independent array/scalar shape check at each position. Concatenation compares
the two array element types; append/prepend compare the array element with the
scalar operand. No new provider, generic-function route or adjacent family is
introduced, and the R16 unknown-input selection definition remains unchanged.
The shared resolver applies one whole-signature predicate both to initial
viability and final known-base substitution. Simple polymorphic families retain
exact logical leaf/container relationships; compatible leaves use the shared
descriptor common-type pair fold and then verify every known leaf's implicit
coercibility. Common expression binding shares category/preference/pair choice
only: it retains Usage, domains, typmods, collations and late conversion errors.

Root recorded PostgreSQL-first success and actual native-before ambiguity in
tests-46. Completed-mode proof uses the selector below (external 900-second
deadline per command); those before-fix observations do not verify the remedy.

```sh
make check RUN='^TestReferenceUserOperatorPolymorphicConsistency$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorPolymorphicConsistency$'
```

Supplementary pure units exercise all six append/prepend/concat orders, varying
the irrelevant scalar position, original winner indices, promotion, exact legacy
relationships, independent record shapes and lookup-only guards. Common-policy
units independently pin categories, preferences and the permissive TIME/TIMESTAMP
pair choice. The removed internal SQL loop is already covered by unchanged
`TestReferenceInsertSelectValuesDiagnostics`, including both full 42846 diagnostics,
direct/Prepare stages and unchanged-row checks; no SQL body is duplicated.

```sh
make check RUN='^(TestUserOperatorResolution(PolymorphicConsistency|PolymorphicUnknown|OrderedTypes|UnknownPositions)|TestOperatorGenericConsistency|TestCommonTypeCandidate|TestInsertSelectCommonTypes)$'
make check RUN='^(TestUserOperatorResolution(PolymorphicConsistency|PolymorphicUnknown|OrderedTypes|UnknownPositions)|TestOperatorGenericConsistency|TestCommonTypeCandidate|TestInsertSelectCommonTypes)$' RACE=1
make check RUN='^TestReferenceInsertSelectValuesDiagnostics$'
make check RUN='^TestReferenceInsertSelectValuesDiagnostics$' REFERENCE=1
```

### User-defined operators

`TestReferenceUserOperatorDefinitionExecution`, `ResolutionPrecedence`,
`DiagnosticsAtomicity`, `OptionsShells`, `OwnershipSchemaLifecycle` and
`DependenciesPrepared` independently assert real int4pl/int4mi/int4um/lower
execution, ordered resolution and masking, qualified generic precedence, exact
diagnostics/atomicity, shells/options, authorization and stored call dependencies.
The prefix `TestReferenceUserOperator` applies to each name.
`NativeRoundTrip` additionally replays **actual native dump bytes** on PostgreSQL,
perturbs destination allocations, checks mixed aliases and live definitions, and
retains old cursor values across replacement. Orphan shells are reconstructed by
transient real generator CREATE/DROP rather than omitted or given a fake body.
`FeatureSmoke` is a standalone end-to-end witness, not a wrapper around old tests;
it reuses one held statement for integer5, typed NULL, integer9 and integer5.
Its unauthorized ALTER uses a separate schema-USAGE permission fixture that is
dropped before native Dump; explicit custom namespace ACL serialization remains
outside the portable dump boundary.
`ShellCompletionReciprocalConflicts` covers stable shell identity, creator-owner
replacement, clearing unspecified completion links without repairing asymmetric
peers, and atomic rejection of a third commutator or negator.
`ReviewRegressions` rejects replacement of a populated commutator by self with
42P13 while preserving both links, execution and sentinel state; initial and
same-value self-links remain legal. Its mixed `(float4,float8)` and
`(float8,float4)` overloads independently require 42725 for two unknown operands
in direct and prepared analysis. Unknown-category/preferred requirements must
be computed for all positions before filtering, retaining the prior candidates
when their combined requirements reject every signature. Pure
`TestUserOperatorResolutionUnknownPositions` asserts that ambiguity in both
declaration orders without SQL or provider execution.
`ReplayGraph` independently checks the asymmetric D/C/A/B final graph in both
shell-allocation orders, reciprocal commutator/negator pairs and cross-kind
cycles, self-commutation, distinct peers and a still-unfilled shell carrying both
link kinds. Separate peer/shell owners, provider/operand/result metadata,
execution, a dependent view, fresh perturbed native replacement and stable
redump are checked before/after replay. PostgreSQL executes the actual native
bytes. The dump keeps asymmetric defined-target and unfilled-shell links in
CREATE; per-source ordinary ALTER events finish reciprocal defined-target links
after their endpoints. Semantic consumers await completion without making
reciprocal links structural declaration dependencies or adding an operator-wide
barrier. Bootstrap replay preserves captured owners through OWNER postludes.
`OrderedArgumentValidation` rejects each earlier SETOF LEFTARG/RIGHTARG even
when replaced later, with exact42P13, no target/shell publication and sentinel42
unchanged. Valid repeated argument options still select the final int4/int4
signature and execute real int4pl to5; valid repeated FUNCTION remains last-wins.
`OptionExtraction`, `OptionBooleanForms`, `OptionAcceptedForms`,
`OptionAlterExtraction` and `OptionLiteralAndPrecedence` cover the root-owned option-form matrix:
required parameters/kinds, source-ordered extraction before duplicate overwrite,
single-component string names/types, real string-bound int4pl/lower execution,
invalid symbolic string names, exact definition-Boolean words and integer forms,
ALTER's preliminary name extraction and command-specific bare/NONE semantics.
Literal/quoted NONE stays a real name; empty literals and reserved names retain
their lookup semantics. Qualified names keep both components through typmods,
including `FUNCTION=pg_catalog.numeric(8,2)`. Malformed ALTER extraction precedes
owner checks, while semantic reference lookup follows them.
`OptionSemanticCheckpoints` covers competing ALTER errors after ownership:
restriction then join estimator lookup; commutator then negator lookup (including
shell/peer ownership and self-negation); protected commutator then negator;
protected MERGES then HASHES; additional parameter combinations; only then tuple
and reciprocal updates in the private CatalogStage. Missing or self-invalid
negators therefore precede protected commutator replacements. Every public case
checks unchanged links/flags, actual provider results and sentinel42 after failure.
`CreationCheckpoints` covers CREATE's distinct post-implementation/result-USAGE
order: surviving restriction then join estimators, valid new name, parameter
combinations, duplicate or shell-owner checks, then CREATE-specific peer/shell
staging. Missing estimators and invalid parameters therefore beat duplicate and
shell-owner errors, without eagerly resolving superseded valid options.
`ParameterCheckpoints` covers both commands' shared two-pass validator: all
nonbinary restrictions (commutator, join, MERGES, HASHES), then all non-Boolean
restrictions (negator, restriction, join, MERGES, HASHES). Unary integer NEGATOR
plus HASHES reports the binary hash error, not the Boolean negator error.
These cases independently preserve failed-CREATE absence, shell/peer owners,
links/flags/estimators, actual providers and sentinel42; no ownership workaround
or weakened diagnostics substitute for the competing checkpoints.
CREATE resolves both surviving operand identities before missing/postfix
diagnostics and checks operand USAGE only afterward. Parser
`TestUserOperatorOptionGrammar` preserves positioned kind/presence, empty literal,
NONE, name components, type decorations and signed int32-token versus Float
boundaries. Pure engine `TestUserOperatorOptionExtraction` checks extraction,
literal provenance and ordered errors; `TestUserOperatorArgumentValidationOrder`
uses actual catalog/type ACLs to distinguish identity, postfix, USAGE and function
checkpoints. No passing verification is implied until root records fresh proof.
`TestUserOperatorAlterSemanticCheckpoints` additionally inspects actual staged
source/peer roots: both link kinds targeting one peer accumulate without mutating
published predecessors, and lookup/protected/combination failures stage no roots.
`TestUserOperatorCreationCheckpoints` and `TestUserOperatorParameterCheckpoints`
exercise those failures through real CREATE/ALTER staging, requiring both
unchanged published catalog roots and no staged descriptors before peer mutation.
`OperandDefaultBoundary` covers selected-function operands at generic precedence,
preserves native direct and grouped ordinary math DEFAULT rejection, and separately
executes a real custom-operator DEFAULT with literal operands. PostgreSQL admits
the ordinary math defaults; syntax admission does not invent a native provider.
`CatalogRows` checks all fifteen fields through the existing raw OID helper and
captures allocated namespace/owner/operator identities independently.
`RoutineAuthorization` uses actual authenticated raw login pools and checks their
rows directly, without requesting middleware recordings they cannot produce.
`Transactions`, `EdgeDefinitions` and `EdgeDiagnostics` retain utility and exact
definition/diagnostic boundaries. Pure engine
`TestUserOperatorCatalogEstimator...` units cover four/five-input selection,
ambiguity, return metadata and custom-estimator privilege without fake providers.
`LookupOnlySeedOverload` checks that custom integer `?` executes int4pl while
the same-spelling released JSONB operator retains true/false/NULL behavior and
its genuine routine identity4047, without fabricating a callable routine.
The catalog lookup-only seed unit separately rejects corrupted seed metadata
and custom definitions without an exact real implementation descriptor.
`TestUserOperatorCatalogDropClosureRootsAndLinks` exercises real closure
publication for deduplicated operator roots, shell removal and surviving links.
The remap unit also requires each stored selected call to retain both its
operator identity and actual provider routine manifest, before and after retagging.

`ComparisonConsumerBoundary`, `ComparisonConsumerResultContracts`,
`ComparisonConsumerStructuralControls`, `ComparisonConsumerBatching` and
`ComparisonConsumerStoredConstructors` cover actual selected operators in
direct and Prepare analysis, not builtin-by-spelling fallback. Native rejects an
executable custom winner in builtin-only contexts with exact `0A000: custom
operators are unsupported in this comparison context`; PostgreSQL executes it.
Scalar-array RHS-not-array precedes selection; selected shells retain42883 and
selected non-Boolean results retain42809 before the custom-runtime boundary.
Token positions are preserved; synthesized USING/NATURAL equality has no Position.
IN attempts common-type/non-Var batching before residual constructor lowering.
Eligible named-composite batches remain structural even with scalar field
operators shadowed; matching NULL members yield true for IN and false for NOT IN.
Residual raw ROW and transformed non-domain cast ROW pairs select fresh field
operators, including one-item lists; anonymous RECORD NULL fields remain unknown.
Frozen residual views preserve member coercions (numeric5.9 to integer6), field
NULL rules and actual emitted SQL through native Dump/Reset/Restore and PostgreSQL
replay. Ordinary named-composite equality, stored composite-column IN, array/record
leaves, grouping, extrema and retained provider reconstruction stay structural.
Nonmatching overloads and explicitly qualified builtins remain supported, while
ordinary custom scalar/BETWEEN operators execute real providers.
No passing result is claimed here; Main owns fresh native and pinned18.6 proof.

`ComparisonConsumerStructuralControls` additionally checks binary seed equality
before custom creation and with only a genuine unary `=` visible: ordinary and
array ANY both return one `v`/BOOL row containing true. The qualified unary call
executes int4um and returns one `v`/INT4 row containing -2. Qualified binary `=`
in that existing schema fails42883 both while empty and while unary-only, with
the complete no-match message, explicit-cast Hint and Position10. A matching
binary custom masked by an earlier pg_catalog signature still permits ordinary
and ANY equality. Seeded qualified `+` with ANY retains42809 for its non-Boolean
result, not a missing-operator or custom-runtime diagnostic. All these guards
reuse direct query and literal Prepare assertions, including prepare-stage errors.
Main observed these guards passing on pinned PostgreSQL18.6 and native before
the performance-only source remedy. Fresh selected proof after the remedy uses:

```sh
make check RUN='^TestReferenceUserOperatorComparisonConsumerStructuralControls$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorComparisonConsumerStructuralControls$'
```

Binary comparison candidate collection now records ephemeral availability from
the authorized path's current descriptors without materializing a seed-only set.
Any matching-arity custom descriptor, even one later masked, retains the full
ordered materializer; unary and noncomparison operators retain that path too.
Availability is not operand viability, a winner, or permission to execute, and
no cross-admission cache or new semantic authority is introduced.
Existing index units supplement the public guard with immutable predecessor,
staged/private publication, current metadata and reconstructed-root coverage:

```sh
make check RUN='^TestUserOperatorIndex(CopyOnWrite|CurrentMetadata|PublicationAndOverlay|BootstrapAndPrincipalRemap|TemporaryCollisionReplacement)$'
make check RUN='^TestUserOperatorIndex(CopyOnWrite|CurrentMetadata|PublicationAndOverlay|BootstrapAndPrincipalRemap|TemporaryCollisionReplacement)$' RACE=1
```


Selected consumer feature smoke/proof (external900s each):

```sh
make check RUN='^TestReferenceUserOperatorComparisonConsumer(Boundary|ResultContracts|StructuralControls|Batching|StoredConstructors)$'
make check RUN='^TestReferenceUserOperatorComparisonConsumer(Boundary|ResultContracts|StructuralControls|Batching|StoredConstructors)$' REFERENCE=1
```

With int4 `=` bound to int4ne ahead of pg_catalog, `SELECT 2 = 2 AS v`
returns false; `SELECT 2 = ANY(ARRAY[2]) AS v` fails native analysis at character10
with the exact custom boundary, while PostgreSQL returns false.
`SELECT 2 OPERATOR(pg_catalog.=) ANY(ARRAY[2]) AS v` returns true.
`ROW(2) IN (ROW(2))` reaches the field boundary under that shadow and returns
true with pg_catalog first; ordinary typed composite equality stays true.

Root-owned selected proof (external deadline900 seconds each; both modes required):

```sh
make check RUN='^TestReferenceUserOperatorOption(Extraction|BooleanForms|AcceptedForms|AlterExtraction)$'
make check RUN='^TestReferenceUserOperatorOption(Extraction|BooleanForms|AcceptedForms|AlterExtraction)$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorOptionLiteralAndPrecedence$'
make check RUN='^TestReferenceUserOperatorOptionLiteralAndPrecedence$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorOptionSemanticCheckpoints$'
make check RUN='^TestReferenceUserOperatorOptionSemanticCheckpoints$' REFERENCE=1
make check RUN='^TestReferenceUserOperator(CreationCheckpoints|ParameterCheckpoints)$'
make check RUN='^TestReferenceUserOperator(CreationCheckpoints|ParameterCheckpoints)$' REFERENCE=1
make check RUN='^TestUserOperator(OptionGrammar|OptionExtraction|ArgumentValidationOrder|AlterSemanticCheckpoints|CreationCheckpoints|ParameterCheckpoints)$'
make check RUN='^TestReferenceUserOperator(ReplayGraph|OrderedArgumentValidation)$'
make check RUN='^TestReferenceUserOperator(ReplayGraph|OrderedArgumentValidation)$' REFERENCE=1
make check RUN='^TestUserOperatorDump'
make check RUN='^TestPartitionConstraintsDump'
make check RUN='^TestReferencePartitionConstraints(Controls|ControlCollision|Attachment)$'
make check RUN='^TestReferencePartitionConstraints(Controls|ControlCollision|Attachment)$' REFERENCE=1
make check RUN='^TestReferenceUserOperator(DefinitionExecution|ResolutionPrecedence|DiagnosticsAtomicity)$'
make check RUN='^TestReferenceUserOperator(DefinitionExecution|ResolutionPrecedence|DiagnosticsAtomicity)$' REFERENCE=1
make check RUN='^TestReferenceUserOperator(OptionsShells|OwnershipSchemaLifecycle|DependenciesPrepared)$'
make check RUN='^TestReferenceUserOperator(OptionsShells|OwnershipSchemaLifecycle|DependenciesPrepared)$' REFERENCE=1
make check RUN='^TestReferenceUserOperator(CatalogRows|RoutineAuthorization|Transactions|EdgeDefinitions|EdgeDiagnostics)$'
make check RUN='^TestReferenceUserOperator(CatalogRows|RoutineAuthorization|Transactions|EdgeDefinitions|EdgeDiagnostics)$' REFERENCE=1
make check RUN='^TestReferenceUserOperator(ShellCompletionReciprocalConflicts|OperandDefaultBoundary)$'
make check RUN='^TestReferenceUserOperator(ShellCompletionReciprocalConflicts|OperandDefaultBoundary)$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorReviewRegressions$'
make check RUN='^TestReferenceUserOperatorReviewRegressions$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorNativeRoundTrip$'
make check RUN='^TestReferenceUserOperatorNativeRoundTrip$' REFERENCE=1
make check RUN='^TestReferenceUserOperatorLookupOnlySeedOverload$'
make check RUN='^TestReferenceUserOperatorLookupOnlySeedOverload$' REFERENCE=1
make check RUN='^TestUserOperator(OptionGrammar|QualifiedPrecedenceGrammar)$'
make check RUN='^TestUserOperator(Catalog|Resolution|RoutineBinding|Remap)'
make check RUN='^TestReferenceUserOperatorFeatureSmoke$'
make check RUN='^TestReferenceUserOperatorFeatureSmoke$' REFERENCE=1
```

These commands are required proof, not a claim that an implementer ran them.


### Cast definitions

`cast_definition*_test.go` is the cohesive cast-definition family in this
existing package: actual directional cast declarations, execution, authority,
dependencies, held/stored semantics and native persistence. The private
`castDefinitionSource`, `castDefinitionScore`, `castDefinitionEdge`,
`castDefinitionCleanup`, `castDefinitionFrozenReplayChecks` and
`castDefinitionReplaySQL` fixtures stay with their callers; no exported fixture
or additional package/TestMain is needed. The existing `fixture.Main` owns both
backends.

Root's initial PostgreSQL18.6 discovery passed in round4; the original eight
files retain their completed definitions. New review scenarios require their
own oracle discovery and removal of any temporary discovery filters.
Those historical observations do not establish current native success.
Completed implementation requires fresh selected proof in both modes.

Round 1 PostgreSQL18.6 observations supply exact assignment positions31/28,
missing-cast positions18/25, procedure42809, ambiguity hint, routine statement
context, dependency Details, trailing-token position57 and the `pg_catalog.int4`
missing-cast notice. Empty diagnostic fields remain assertions.
The fixture resets `public` with ordinary CREATE SCHEMA, losing initdb's PUBLIC
USAGE grant. Authority cases explicitly GRANT schema USAGE to their reader,
then independently vary type ownership/USAGE and function EXECUTE. They do not
require an adjacent schema-privilege inquiry builtin or change schema ownership.

The [REL_18_6 catalog](https://github.com/postgres/postgres/blob/REL_18_6/src/include/catalog/pg_cast.dat)
protects BOX→LSEG, PATH↔POLYGON and all INT2/INT4/INT8↔BYTEA pairs.
The tests instead declare absent LSEG→BOX (both endpoint orders), PATH→CUBE
(equal-layout ABI declaration only, never executed), and XID→BYTEA with a real
TEXT→BYTEA result adapter. Protected rows are never dropped.
Test-owned builtin-endpoint casts register pair cleanup before CREATE, first
asserting absence to avoid claiming preexisting objects. Cleanup drops pairs
separately with fresh bounded contexts even after failed assertions; dependent
casts precede adapter roots. Actual native-dump PostgreSQL replay also removes
schema-less pairs before the next backend phase.

Round 1 observed a held cast query retain41 after DROP/recreate returning77.
PreparedLifecycle independently expects PG41 versus native77 at that boundary,
then fresh SQL77; stored expressions retain41 on both. Held fallback has explicit
backend expectations rather than parity alone. ARRAY constructor element binding
independently rejects xid→real before a scalar declaration even when an
array-level INOUT row exists. Common categories still reject enum/integer
CASE and COALESCE before implicit edge selection.

`BuiltinInvariance` checks direct/prepared builtin values, FILTER and missing
signatures before/after an unrelated cast, then actual implicit conversion and
nonstrict NULL into `abs`. `RawCompositeArrayBounds` independently expects
rank2 bounds[0,-2] and distinct exact signaling/quiet NaN leaves before and after
native replay and actual emitted PostgreSQL SQL replay. These two new scenarios
require root oracle discovery/proof; source reasoning is not observed evidence.

`Persistence` also covers a view retaining enum INOUT after its declaration is
dropped, and a typed enum[] view retaining its original function/NULL99 and
lower bound0 after a conflicting function cast is recreated. The actual native
dump bytes must replay those old meanings on PostgreSQL while preserving pair
absence and the final new function row. These added assertions require fresh
root native and PostgreSQL proof.

`RawFloat4Persistence` asserts exact scalar, array and composite raw words via
the public REAL→XID binary edge, including qNaN2143289345 and sNaN2139095041.
Those same expected decimal words survive native Restore, held/prepared reads
and actual native-dump SQL replay on PostgreSQL in all four final cast modes.
The redundant SQL `float4send` check is omitted: that routine provider is not a
cast-definition prerequisite; binary send codec units remain separate coverage.

`ReplayConflicts` reads generated REAL leaves through an independently declared
REAL→XID binary edge: historical binary XID42 has raw word42, whereas the final
INOUT conversion produces REAL42 with raw word1109917696 (`0x42280000`).
Both arrays retain lower bound0, typed NULL leaves and stored-generation `s`
metadata, including fresh inserts after actual replay. Each backend asserts
these constants independently. Both schema-less cast pairs register ownership
cleanup before declaration.

`ReplayPhases` and `ReplayConflicts` replay unchanged native-produced SQL on a
borrowed PostgreSQL owner, reset `public` before and after that phase on the
same owner, and remove owned schema-less pairs. Step rows/statements close
before reset and the owner returns before the independent PostgreSQL setup.
Failure-only diagnostics identify the exact `ReplayConflicts` SQL statement.
These fixture remedies require fresh selected proof, not inferred success:

```sh
make check RUN='^TestReferenceCastDefinition(RawFloat4Persistence|ReplayPhases|ReplayConflicts)$' REFERENCE=1
make check RUN='^TestReferenceCastDefinition(RawFloat4Persistence|ReplayPhases|ReplayConflicts)$'
```


`CaseBinding` exercises saved simple-CASE input with a real implicit enum cast:
one source call, one conversion per reached equality, conversion before each
WHEN call, no evaluation of later matched-away arms, nonstrict NULL99, nested
saved-input frames, and an INOUT test conversion. `CaseErrorOrder` independently
expects a failing left conversion to precede a sequence-writing WHEN operand;
the sequence remains uncalled even though sequence effects would not roll back.
Both cover direct and prepared execution. These effect/error expectations are
oracle candidates until the root records pinned PostgreSQL observations.

```sh
make check RUN='^TestReferenceCastDefinitionCase(Binding|ErrorOrder)$' REFERENCE=1
make check RUN='^TestReferenceCastDefinitionCase(Binding|ErrorOrder)$'
```



Root discovery selector (external900-second deadline):

```sh
make check RUN='^TestReferenceCastDefinition(FunctionAndContexts|Signatures|IO|FeatureSmoke|BinarySafety|CapabilityBoundary|ContextPrecedence|PreparedLifecycle|StoredSemantics|Dependencies|Authority|Atomicity|Persistence|RawFloat4Persistence|RawCompositeArrayBounds|BuiltinInvariance|Grammar|Notices|SignatureAdapters|Cancellation|UtilityPolicies)$' REFERENCE=1
```

Run the identical selector without `REFERENCE=1`, then repeat both completed
definitions after remedies and before review. External deadline900 seconds.
Feature smoke:

```sh
make check RUN='^TestReferenceCastDefinitionFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceCastDefinitionFeatureSmoke$'
```

Expected smoke: red41/blue42/NULL99 through one held statement, typmod/explicit
flag values, actual enum IO, failing multirow rollback, exact qNaN/sNaN words,
literal backslash bytes, and context-aware routine binding. These are required
observations, not passing evidence. Whole-project quickcheck remains separate.

`TestReferenceCastDefinitionForeignDefaults` uses the portable metadata-only
`fdw_meta` wrapper (`NO HANDLER NO VALIDATOR`) and `fdw_server`; no remote scans
or provider identities are needed. Old/fresh simple-CASE defaults retain their
actual selected routine dependencies in inline and late foreign declarations,
including different epochs on two attributes of one declaration. Ordinary
defaults independently execute to 1/2, while the final implicit cast returns 77.
The native phase performs Dump/Reset/Restore and, when PostgreSQL is enabled,
executes the unchanged emitted SQL there and repeats those same catalog/value
assertions. Actual catalog queries require exactly the final cast and expected
routines, with no surviving replay helpers. Wrapper/schema and same-session
authorization cleanup runs before and after replay, leaving the later independent
PostgreSQL setup clean.

```sh
make check RUN='^TestReferenceCastDefinitionForeignDefaults$'
make check RUN='^TestReferenceCastDefinitionForeignDefaults$' REFERENCE=1
```

The metadata-only foreign case constructs `fixture.NewScenario` directly and
shares one SQL/assertion body across backends. Only ordinary wrapper/server
DDL is needed: it registers no native foreign provider and issues no remote
queries. PostgreSQL borrowed owners clean global wrapper/schema state with
fresh bounded contexts before closing, including native-emitted SQL replay
before the later independent PostgreSQL setup. Native teardown closes its
borrowed owner and uses public Reset with a fresh bounded context rather than
enabling unsupported schema-CASCADE dependency combinations.

Whole-family selected proof after migration (unchanged public test names;
external deadline900 seconds per command):

```sh
make check RUN='^TestReferenceCastDefinition'
make check RUN='^TestReferenceCastDefinition' REFERENCE=1
```

These commands include the foreign-default and review scenarios alongside the
original family. They are required proof, not recorded passing results.

The review regressions independently assert INOUT unconstrained input followed
by contextual scalar/array sizing (`re`/`é`, assignment `22001` and atomicity),
released enum/composite/range/multirange IO volatility (`42P17`) with immutable
XID→REAL controls, assignment-context Boolean constructs and routine defaults,
and exact signed LINE→CIRCLE payloads. Direct/fresh/held paths, NULL, retained
meaning after cast replacement, native Restore and actual native SQL replay on
PostgreSQL are exercised by the five named review definitions. Their
MERGE assertions preserve lib/pq's exported affected-count zero while checking
the actual mutations independently.

`TestReferenceCastDefinitionNestedConstructorReplay` independently checks outer
array bounds0:0/1 and nested bounds-2:-2/1, negative radii-3/-7, NULL composite
versus ROW(NULL circle), and captured snapshot id1 versus updated table id2.
Same-owner and fresh-owner native Restore read the captured rows before REFRESH;
unchanged emitted SQL on PostgreSQL preserves table values and the initially
unpopulated view definition, then REFRESH derives the real updated rows.

`TestReferenceCastDefinitionAggregateBinary` uses real byteacat support with
TEXT input and BYTEA state. Initialized transitions and strict first-value seeds
produce raw bytes616263/5c7834313432 rather than INOUT's4142. Explicit/assignment
binary edges and implicit function/INOUT edges cannot satisfy binary-only
support admission. After DROP/INOUT replacement, initialized adapters retain
their meaning; strict/no-init direct, prepared, held, stored and empty-input
execution instead fails42P13 with the actual aggregate identity. Each absent/inout
final has serialized complete owners: native setup, five direct/fresh/SAME-held
success cases, the SAME-held42P13 transition, and same-attachment
Dump/Reset/Restore/full final assertions share one continuous normal30s context.
Fresh-native replay owns a distinct Driver, raw Restore and wrapped sqlmw checks;
actual PostgreSQL replay executes the unchanged emitted SQL, separately from
the independent original PostgreSQL setup/held lifecycle. Each owner finishes
cleanup before the next shared-database reset.

Owner-explicit final checks retain initialized table, empty/NULL, stored-view
and parameter cases, strict table/empty/stored failures, current INOUT conversion,
fresh42883/42804 refusals and callable/aggregate publication counts0. Metadata
prefilters every cab_scope procedure before joins, preserving both bytes AND
seed, order, multiplicity, all seven fields and actual aggregate/support OID
edges. Each replay resolves its own seed identity for the full42P13 diagnostic.

```sh
make check RUN='^TestReferenceCastDefinition(AggregateBinary|NestedConstructorReplay)$'
make check RUN='^TestReferenceCastDefinition(AggregateBinary|NestedConstructorReplay)$' REFERENCE=1
```

`TestReferenceCastDefinitionAggregatePolymorphicBinary` composes historical
binary adapters with real polymorphic `array_append` specialization. Existing
OID→REAL word mappings produce array elements1/2/NULL, before and after DROP
or an assignment-only INOUT replacement; current ordinary conversion instead
produces1065353216. The initialized aggregate, its concrete REAL[] signature,
stored view and remapped native owners keep the admitted meaning. Each final
state has four serialized, complete fixture owners: the native lifecycle keeps
the same held statement before/after DROP or replacement and then performs
Dump/Reset/Restore on that same attachment; fresh-native replay restores those
exact bytes with its own context; actual PostgreSQL replay executes the unchanged
stream; independent PostgreSQL lifecycle repeats the original setup and held
transition. Every owner retains the normal30s budget, and each fixture finishes
cleanup before another PostgreSQL-resetting fixture starts.

Owner-explicit checks repeat values, saved view, empty/NULL input and metadata
directly and freshly prepared at every boundary. Metadata filters all matching
owning procedures before joins, preserving actual support identity378, concrete
REAL[] state/result, nonstrict false and initial text`{}` without deduplication
or truncation. Fresh declarations still fail at execute with full42883
diagnostics, followed by count0 publication/health checks. No CIRCLE[] codec or
new common-type category is enabled for this witness.

```sh
make check RUN='^TestReferenceCastDefinitionAggregatePolymorphicBinary$'
make check RUN='^TestReferenceCastDefinitionAggregatePolymorphicBinary$' REFERENCE=1
```

Selected neighboring/shared-consumer proof (real pinned PostgreSQL first, then
native and selected race; finalcheck does not supply race coverage):

```sh
make check RUN='^TestReference(CastDefinitionAggregate(Binary|PolymorphicBinary)|CreateAggregatePolymorphicSupport|CastDefinitionFeatureSmoke)$' REFERENCE=1
make check RUN='^TestReference(CastDefinitionAggregate(Binary|PolymorphicBinary)|CreateAggregatePolymorphicSupport|CastDefinitionFeatureSmoke)$'
make check RUN='^TestReference(CastDefinitionAggregate(Binary|PolymorphicBinary)|CreateAggregatePolymorphicSupport|CastDefinitionFeatureSmoke)$' RACE=1
```
