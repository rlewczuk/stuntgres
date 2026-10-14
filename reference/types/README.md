# reference/types

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### ACL item oracle-first selected proof

The ACL definitions share one SQL body across native and pinned PostgreSQL,
with backend subtests directly beneath each top-level family. Independent
expectations cover all fifteen privilege letters and grant options, canonical
quoting, PUBLIC versus NULL, scalar and array storage/binds/raw descriptors,
equality-only scalar admission, array bounds/shape/NULL equality, complete
input diagnostics and default-grantor notice phases, and retained role identity
through DROP/recreate. Native-only control phases check live-role lossless
Dump/Restore, independently advanced target role identities, atomic staged
failures, retained rows, ordinary cancellation/I/O failures, and target-role
replacement fencing. Stale ACL Dump rejects before writer I/O; ordinary stale
output and equality remain distinct from a numeric-looking live role.
`Definitions` exercises persisted defaults, checks, views and expression indexes,
including prepared future writes into an independently advanced restore target.
`StaleDefinitions` supplies no-row default/view/check/index/derived-partition
preflight witnesses. `DumpSnapshot` blocks a real writer, changes roles after
capture, and requires the original coherent dump followed by zero-I/O rejection
of a later stale snapshot. Native-control branches are explicitly native-only.
`Casts` covers explicit sized-character conversions, NULL, source/destination
domains and assignment-length rejection without mutation. `Deparse` pins public
scalar/NULL view definitions through direct/prepared, pretty/nonpretty and
literal/bound lookup routes; its SQL expectations require pinned oracle proof.

Completed definitions require selected pinned-oracle and native proof after
affected edits; selected proof does not replace whole-project `quickcheck`.
Generic component coverage retains checked ACL scalar/array carriers through
`any_value` and explicitly rejects ACL ordering rather than assuming every
concrete scalar is sortable. Grant options follow the preceding letter:
`wr**` canonicalizes to `r*w`, not `rw*`. Omitted-grantor results use the
independently specified bootstrap spelling (PostgreSQL fixture `stuntgres`,
native fixture `postgres`), without changing either backend's role setup.
Literal default-grantor warnings carry the enclosing literal Position;
bound-input warnings carry parameter Context instead. With an external
900-second deadline per command:

```sh
# Initial oracle-only discovery: /postgres selects immediate backend children.
make check REFERENCE=1 RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)/postgres$'
# The completed definitions must pass both modes after implementation.
make check RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceACLItem(Values|Binds|Metadata|Comparison|Diagnostics|Notices|Identity|NativeControls|StaleControls|Definitions|StaleDefinitions|DumpSnapshot|Boundaries|Casts|Deparse|Smoke)$'
make check RACE=1 RUN='^TestACLItem(Identity|Codec|Equality|Constructor|RetainedIdentity|PrincipalRemap|RestoreFence|EqualityAdmission|StaleDump|RenderingAuthority)$'
make check RUN='^Test(AnyValueProviderDatums|OrderingUsage)$'
# Review regressions: oracle first, then observed native regression before remedy.
make check REFERENCE=1 RUN='^TestReferenceACLItem(Casts|Deparse)/postgres$'
make check RUN='^TestReferenceACLItem(Casts|Deparse)$'
# Feature smoke, not baseline-only success.
make check RUN='^TestReferenceACLItemSmoke$'
make check REFERENCE=1 RUN='^TestReferenceACLItemSmoke$'
```

Smoke requires scalar/array CREATE, literal and actual prepared bound INSERT,
canonical ordered values and metadata, scalar option-sensitive equality,
array equality/inequality, complete unknown-role diagnostics with unchanged
rows, and native Dump/Reset/Restore with held rows and deterministic redump.
Success is quiet exit 0 and the ordinary package summary after those assertions.


### Composite expression selected proof

Run each small group in native mode and with `REFERENCE=1`, using an external
900-second deadline per invocation:

```sh
make check RUN='^TestReferenceCompositeExpr(Constructors|ConstructorCoercion|ConstructorDemand|Fields|Metadata|NullSemantics)$'
make check RUN='^TestReferenceCompositeExpr(ScopeAmbiguity|FunctionalNotation|Expansion|ValuesAndInsertExpansion|NestedStars|Returning|Diagnostics)$'
make check RUN='^TestReferenceCompositeExpr(Assignments|MixedArrayFields|MergeUpdate|AssignmentDiagnostics|AtomicityAndACL|Boundaries)$'
make check RUN='^TestReferenceCompositeExpr(StoredConsumers|ControlsAndLifetime|ControlRejection|FeatureSmoke)$'
make check RUN='^TestReferenceCompositeExprWholeRowAbsence$'
make check RUN='^TestReferenceMergeUpdateFields$'
make check RUN='^TestReferenceCompositeExpr(StoredFieldPruning|CorrelatedReturning|FunctionAlias)$'
make check RUN='^TestReferenceCompositeExpr(ZeroWidthValues|NamedAssignmentIdentity)$'
make check RUN='^TestReferenceCompositeExpr(VirtualBoundUpdate|VirtualBoundMerge|ViewTargetPaths|JoinTargetPaths)$'
make check RUN='^TestReferenceCompositeExprNamedTargetDiagnostics$'
make check RUN='^TestReferenceCompositeExprFunctionSourceCutover$'
make check RUN='^TestReferenceCompositeExprFunctionSourceRestore$'
make check RUN='^TestCompositeExpr(RecordShape|FieldAndNull|AssignmentPaths|VisitorsAndRetag|Parser)$'
make check RACE=1 RUN='^TestReferenceCompositeExpr(AtomicityAndACL|ControlsAndLifetime|ControlRejection)$'
```

The ACL witnesses keep actual column UPDATE/INSERT grants and denied explicit
field reads in both modes. Stored-field policy filtering is PostgreSQL-only;
native asserts the full existing unsupported policy-enforcement diagnostic,
then disables RLS to prove actual reader ACL projection. Schema USAGE grants/revokes are
PostgreSQL-only; the stored-reader native setup transfers public-schema
ownership for namespace visibility without transferring the table, then restores
ownership before dropping the role. Native schema-grant syntax stays unsupported.
Native control rejection asserts every diagnostic field using the actual appended
failing occurrence, never an earlier matching Dump substring. Stored computed
shapes retain anonymous occurrence ownership, contextual domain/typmod coercions
and exact leaf providers through genuine Dump/Restore; no generic RECORD storage
is admitted. INSERT VALUES expansion covers direct/prepared named targets and
identity-bearing destinations as well as INSERT SELECT.
Stored dot-builtin defaults retain their admitted notation through replay,
including composite/scalar operands and NULL `num_nonnulls`. Ordinary function
notation and generic DEFAULT capability boundaries remain unchanged.
Whole-row demand uses the existing copy-on-change expression visitor and the
side-aware RETURNING column wrapper; child binding errors propagate before
provider usage inspection. Retained consumer regression:
`make check RUN='^TestReferenceUpdateReturningExpressionsConsumerRoutes$'`
(also run with `REFERENCE=1`); its independent values/metadata remain unchanged.
StoredFieldPruning asserts direct/prepared generated/index admission, every
write's rows and untouched sequence, selected-volatility/immutable-preflight
errors, failed-name reuse, two native replacements and actual PostgreSQL SQL
replay with future writes. CorrelatedReturning asserts child/grandchild
transition frames, scalar stars/ROW, default versus member projections,
explicit aliases, nearer shadows, ambiguity, masked-name diagnostics and
mutation atomicity. INSERT field expansion retains the synchronized base's
already-supported INSERT RETURNING authority rather than an obsolete exclusion.
FunctionAlias distinguishes declared scalar function aliases from ordinality/
multiple-call tuples, actual NULL cells from missing tuples, top/scalar/nested
stars, explicit ROW, ordinary derived records and nearer column shadowing.
It asserts full scalar diagnostics and five actual frozen scalar/tuple views
through direct/prepared reads, two native replacements, emitted PostgreSQL SQL
replay and future null-extension rows. No new FROM provider is implemented.
ZeroWidthValues covers both width directions on direct Exec/Query and Prepare,
full positioned/unpositioned errors, unchanged destination/sentinel, same-connection
recovery and valid zero-column rows. NamedAssignmentIdentity distinguishes named
identity from contextual anonymous conversion, projected INSERT SELECT RECORD,
nested diagnostics, same-base/domain coercion and NULL versus anonymous
all-NULL reconstruction. No oracle-discovery filter remains in either definition.
VirtualBoundUpdate/Merge isolate virtual source reads solely in nested bounds,
with fresh held executions, missing-source NULL and recipe-error atomicity.
ViewTargetPaths independently asserts logical/physical slot and source-offset
remapping, ordered original-frame writes and recovered held handles.
JoinTargetPaths asserts planned FULL/nullable-rowmark errors even on empty
UPDATE/INSERT/conflict/MERGE input and valid fresh joined-bound writes.
NamedTargetDiagnostics independently checks full UPDATE/INSERT VALUES/INSERT
SELECT partial-target records for visible, hidden and shadowed quoted named
types and domains on direct Exec/Query and failed Prepare, unchanged nested
rows and a preheld valid handle. Invalid subscripting of selected composite/domain
members has no Position; plain scalar-column targets retain their independently
pinned head Position. Coercion failures retain the assignment head. Both backends
assert the same full diagnostic records without backend overrides or normalization.
FunctionSourceCutover applies the existing scalar-alias/true-tuple contract to
the admitted generic function stage: numeric, boolean, text and text-array
results, explicit ordinal/multiple-call tuples and nearer alias-column shadow.
No new function FROM provider is added.
Nested correlated sources retain packed occurrence layout and invocation flags,
including whole values consumed solely as function arguments and nullable
inherited prefixes; an empty physical source does not evaluate failing arguments.
FunctionSourceRestore independently checks declared scalar/tuple types and
NULL-versus-all-NULL values before genuine two-cycle native replacement, a fresh
driver with actual wrapped reads/unwrapped controls, retained original rows,
actual emitted PostgreSQL SQL replay and future matching/nonmatching writes.
The session-only public executable smoke separately exercises actual Dump,
Reset and Restore after constructors, partial writes, fields, prepared inputs,
stored-field admissions, correlated RETURNING transitions and declared function
alias/tuple consumers, with future generated writes, sequence-state checks and
frozen scalar-star/tuple replay. Root runs it as
`go run "$SESSION_TMP/composite-expr-smoke.go"`;
expected stdout is `composite expressions public smoke: PASS` and exit0.
Whole-project Docker-free verification remains `make quickcheck`, after edits;
selected checks and smoke readiness are not observed verification.

### Multirange selected proof

Permanent untagged `reference/types/multirange_*_test.go` scenarios cover all
three concrete companions through shared direct/prepared SQL, raw transports,
metadata, full diagnostics, ACLs and nested domains/column NOT NULL.
Oracle discovery filters are removed before native-red; they are not a final
capability distinction. Native process-local control subphases additionally
execute the actual emitted SQL on pinned PostgreSQL18.0.
Pinned lib/pq exposes blank multirange type names (not uppercase range names);
the helper retains logical family identity for raw/metadata checks, and public
pg_type/pg_range witnesses independently verify released OIDs/companion links.
Scale-boundary unions follow released strict selection rather than a containment
shortcut; fully comparator-equal different-scale duplicates have no stable tie
ordering promise.
The permanent `TestReferenceMultirangeComparisonBoundary` uses all three
companions and plain/nested domains with unequal ascending/reverse, equal and
NULL operands through direct/prepared SELECT. PostgreSQL's six supported scalar
comparison result matrices are independently specified; native must reject with
full42883/logical operand names/Hint/Position, at direct execution or Prepare.
No discovery filter survives native-red or final selected evidence.

Run each bounded group in both modes, pinned first for new expectations, under
an external900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check RUN='^TestReferenceMultirange(Values|Binds|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check RUN='^TestReferenceMultirange(Diagnostics|Modifiers|InvalidSQL|Authorization)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check RUN='^TestReferenceMultirange(Domains|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check RUN='^TestReferenceMultirange(Controls|DomainControls)$'
make check REFERENCE=1 RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestReferenceMultirangeComparisonBoundary$'
make check RUN='^TestMultirange'
make check RUN='^TestComparison'
make check RUN='^TestAnyValueProviderDatums$'
make check RACE=1 RUN='^TestReferenceMultirange(Binds|Controls|DomainControls|ComparisonBoundary)$'
make check REFERENCE=1 RUN='^TestReferenceIntegerRange'
make check RUN='^TestReferenceIntegerRange'
make check REFERENCE=1 RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRange'
make check RUN='^Test(IntegerRange|NumericRange)'
```

The public standalone source `local://PG18-TYPE-MULTIRANGE-smoke.go` is an OMP
session artifact, not a committed executable. Root materializes
`.multirange-feature-smoke/main.go`, runs
`timeout 60s go run ./.multirange-feature-smoke`, records argv/cwd/exit and exact
observations, then removes the temporary materialization. It asserts three-family
canonical unions, gaps/NULL/empty/universal, binds/raw metadata/diagnostics/
atomicity, real same/fresh Driver controls and nested-domain constrained replay
with held-reader shape retention and direct/prepared scalar-comparison admission
rejection for builtin/plain/nested-domain operands. Success is quiet exit0, not a printed label or
test-shelling. Root runs whole-project Docker-free quickcheck after edits and
post-integration finalcheck; those do not replace selected dual-mode evidence.

### Enum support functions

Enum support-function public witnesses use the shared direct/prepared definition:
`make check RUN='^TestReferenceEnumFunctions'` and
`make check REFERENCE=1 RUN='^TestReferenceEnumFunctions'`.
Standalone feature smoke uses `RUN='^TestReferenceEnumFunctionsFeatureSmoke$'`
in both modes. It checks typed NULL extrema, inclusive/NULL-bounded computed
arrays, metadata/raw transport, exact mismatch diagnostics and genuine native
stored-definition/eager-value controls. Private provider/retag units supplement
these witnesses with
`RUN='^(TestEnumSupportFunctions|TestEnumFunctionRetagAndStoredProviders|TestQueryTableCreateEnumArrayOutputBoundary|TestQueryTableCreateStructuralOutputWrappers)$'`.
The selected admission regressions use
`RUN='^TestReferenceEnumFunctions(StableAdmission|StorageBoundaries)$'` in
both modes. They independently assert STABLE generated-column/index-expression/
index-predicate rejection and the bounded native enum-array storage exclusion.
PostgreSQL storage succeeds, including query-derived and explicit enum arrays;
its successful stored values and NO DATA behavior are asserted separately from
native capability diagnostics. Computed ranges, ordinary views, projected
domain-over-enum arrays and non-enum array storage remain positive controls.
Derived native storage and explicit annotation/domain exclusions exercise direct
and prepared routes with full diagnostics and no partial publication. The
unpopulated PostgreSQL materialized view uses the shared full55000 diagnostic
and REFRESH hint, then refreshes successfully. Positive view metadata retains
declared native nullability rather than computed-expression nullability.

### Predefined timestamp domain

The shared predefined-domain witnesses select
`RUN='^TestReferenceInfoTimeStamp(Values|Metadata|Diagnostics|Identity|NativeControls|FeatureSmoke)$'`
with both `make check` and `make check REFERENCE=1` (900 seconds each).
Private descriptor/catalog/remap units select
`RUN='^TestInfoTimeStamp(BuiltinDescriptor|CatalogIdentity|CatalogRemap)$'`.
Consumer carrier coverage also selects `RUN='^TestAnyValueProviderDatums$'`:
the newly concrete builtin domain supplies finite p2/carry and nonfinite datums,
checks its domain/base tags and retains the exact chosen datum through aggregation.
Feature smoke selects `RUN='^TestReferenceInfoTimeStampFeatureSmoke$'` in both modes.
Pinned PostgreSQL18.0 supplies independent p2 values, TIMESTAMPTZ transport and
metadata, complete base diagnostics and catalog/default controls. Native APIs
add actual qualified Dump/Reset/Restore, retained cursors and failed replacement
atomicity; PostgreSQL has no corresponding process-local Restore API.
Cache ColumnTypes while a cursor is open to check retained descriptors after
EOF/Close. Pre-canceled pq reads can race database/sql's watcher: validate available
metadata/emitted ordered prefixes, permit ordinary cancellation/ErrBadConn, and
require full rows on clean EOF. Native pre-cancellation requires context.Canceled
without a cursor. Separate cancellation handles preserve an untouched write
atomicity observer. This adapter distinction never weakens domain SQL diagnostics.
Native inherited-domain defaults, sibling-domain/implicit-array executability
and general information-schema/pg_type views are explicit exclusions.

### INET and CIDR

INET/CIDR scalar acceptance uses `make check RUN='^TestReferenceINET.*$'`
and `make check REFERENCE=1 RUN='^TestReferenceINET.*$'` (external900-second
deadlines), plus codec/coercion/ordering units with
`make check RUN='^TestNetwork.*$'`.
Feature smoke selects `^TestReferenceINETFeatureSmoke$` in both modes.
The definitions independently assert exact values/types/counts, full
diagnostics/stages, raw transport and empty/all-NULL metadata, held binds,
statement atomicity and real native Dump/Reset/Restore with PostgreSQL replay.
Native control API/cursor phases are explicitly native-only, not a fabricated
PostgreSQL equivalent. The oracle-first discovery filter is not permanent.
A separate throwaway public executable additionally exercises prepared binds,
prefix-aware ordering/casts, diagnostics, metadata and real control round trips;
its temporary main package is not committed or substituted by a test.

### Bitstrings

The bitstring family covers independently pinned BIT/VARBIT values, raw scanner
and modifier diagnostics, direct/held parameters, assignment atomicity, complete
metadata and retained ownership, plus native Dump/Reset/Restore with PostgreSQL
replay of the actual emitted SQL. The expanded feature smoke combines the compact
scenario with stored-format replay/default demand, compound/simple-parenthesized
modifiers, differing-width repeated slots and held parameterized cursor snapshots.
Run each invocation under the quickcheck900-second deadline, quietly:

```sh
make check RUN='^TestReferenceBitstring'
make check REFERENCE=1 RUN='^TestReferenceBitstring'
make check RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
make check REFERENCE=1 RUN='^TestReferenceBitstring(FeatureSmoke|StoredFormatReplay|ModifierExpressions|RepeatedVaryingWidths|HeldSelectArguments)$'
```

Both modes are required before review/integration. Finalcheck remains one
1800-second gate after merge. Discovery-only deliberate observation failures
and PG-only filters must be removed before the native failing proof and gates.

### NUMRANGE oracle-first contract

`TestReferenceNumericRange` definitions independently specify continuous numeric
bounds, exact scale-preserving output, empty/unbounded/NULL distinctions, explicit
numeric infinities and NaN, direct/prepared binds, metadata, full diagnostics,
statement atomicity and already-admitted plain/nested domain composition.
The control scenarios use public native Dump/Reset/Restore and replay actual
emitted SQL on pinned PostgreSQL; PostgreSQL has no native-control API analogue.
The feature smoke is `TestReferenceNumericRangeFeatureSmoke`.

The oracle-first Round A definitions were introduced before production changes.
The temporary discovery filter was removed before native red. Completed definitions
now share `reference/types/range_helpers_test.go` with the integer-range scenarios:
actual NUMRANGE/INT4RANGE/INT8RANGE raw values and metadata are asserted before
normalization. Pinned PostgreSQL18.0 grounds expected diagnostics and phases;
never learn golden values from native output.

Run from the assigned worktree, with an external900-second deadline per command:

```sh
make check RUN='^TestReferenceNumericRange' REFERENCE=1
make check RUN='^TestReferenceNumericRange'
make check RUN='^TestReferenceNumericRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceNumericRangeFeatureSmoke$'
```

Because common range helpers are consolidated, both selected modes must additionally
cover `^TestReferenceIntegerRange`. Reserve `make quickcheck` for completed
whole-project Docker-free verification. Standalone smoke source
is supplied in the OMP session as `local://numrange-smoke.go`: materialize it
temporarily as `.numrange-feature-smoke/main.go`, run
`timeout 60s go run ./.numrange-feature-smoke`, record observations and remove
the materialized source. Success is quiet exit0; failure reports an actual mismatch.

### DATERANGE contract and selected proof

Untagged `TestReferenceDateRange` definitions independently assert civil-day
canonicalization, finite DATE extrema/BC/leap boundaries, explicit infinity
versus omission/empty/NULL, input and held binds, raw string/byte transport and
metadata lifetime, complete diagnostics/phases and statement atomicity.
DateStyle-sensitive input/output and retained contextual literal capture reuse
the DATE environment. Relative endpoint expectations derive day D from scalar
DATE in the same transaction, without a second wall-clock observation.
Ordinary/nested domains, column NOT NULL and named-composite fields retain the
same datum. Native control phases perform actual same/fresh Driver cycles,
deterministic redumps, failed staged replacement and populated-reader shape
replacement, then replay the actual emitted SQL on pinned PostgreSQL.
PostgreSQL has no native Reset/Dump/Restore API; those phases are explicit.
No discovery filters remain. No timestamp range, datemultirange, SQL comparison,
range array or new aggregate admission is implied.
Computed composite-field projections independently assert unknown native
nullability (false/false), not their member declarations; native ANY_VALUE
remains known nullable. String-literal modifiers retain the existing native
grammar rejection at the string token (Prepare for prepared execution), while
PostgreSQL rejects unsupported daterange modifiers at execution with the exact
type-name diagnostic. Supported numeric/raw modifier forms retain descriptor
rejection before conversion. The declaration resolver retains qualified type-name
spelling for structured no-modifier diagnostics only when the declaration has an
explicit schema. Unqualified aliases and lowered declarations retain descriptor
canonical diagnostic names, including SERIAL's integer/bigint primitives. The
descriptor remains the support authority. This adds no modifier grammar.

Each command has an external900-second deadline; run each group in both modes:

```sh
make check RUN='^TestReferenceDateRange(Values|CanonicalBoundaries|Infinity)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Values|CanonicalBoundaries|Infinity)$'
make check RUN='^TestReferenceDateRange(Inputs|Binds|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Inputs|Binds|Metadata)$'
make check RUN='^TestReferenceDateRange(Diagnostics|Modifiers|InputBoundaries)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Diagnostics|Modifiers|InputBoundaries)$'
make check RUN='^TestReferenceDateRange(Environments|RelativeCapture)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Environments|RelativeCapture)$'
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition)$'
make check RUN='^TestReferenceDateRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceDateRangeFeatureSmoke$'
make check RUN='^TestDateRange'
make check RUN='^TestAnyValueProviderDatums$'
```

Shared range adapter/codec signatures also require the selected integer,
numeric-range and multirange value/bind/metadata/domain-control definitions in
both modes. Binary transport proof belongs to `reference/dml` and reuses its
existing stream fixture (`^TestReferenceDateRangeBinary$`, both modes).
Reserve quickcheck for the whole-project Docker-free gate, not selected proof.

The additive standalone public-API smoke is `local://daterange-smoke.go`, outside
the parent deliverables. Root materializes it in an owned temporary .go file
outside the module, then runs from the worktree:
`go run /tmp/PG18-TYPE-RANGE-DATE-daterange-smoke.go` (or its allocated absolute
path), with an external900-second deadline. Expected exit0 and stdout:
`daterange smoke: canonical days, infinity/unbounded, NULL, metadata, atomic diagnostics, nested domains, Dump/Reset/Restore and held snapshots passed`.
Record actual argv/cwd/exit/output and remove only the owned temporary file.

