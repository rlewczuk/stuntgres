# reference/operators

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root. This package owns the user-defined operator scenarios and
`TestReferenceRegOperatorExternalBoundary`; the remaining
[operator OID alias scenarios](../type_resolution/README.md#operator-oid-aliases)
stay in `reference/type_resolution`.

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


