# reference/types

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Composite-field domain-default fixture ownership

`TestReferenceDomainDefaultCompositeFieldDependencies` keeps each direct/prepared
DDL source lifecycle continuous: copied defaults/dependencies, exact RESTRICT
and no mutation/fresh default write, CASCADE and original-object survivors,
Dump/no mutation, SAME-attachment Reset/Restore and fresh replay writes.
Only after that source subtest closes does a fresh native target fixture consume
its actual emitted dump and repeat complete survivor/dependency/metadata and
fresh-write assertions. These native control phases have no fabricated
PostgreSQL API; the shared SQL lifecycle independently runs on PostgreSQL.
The selected-local runner closes notice/connection handles before teardown and
uses a separate15s cleanup-only context. It does not refresh scenario30s or alter
the shared domain fixture or its other consumers.

```sh
make check RUN='^TestReferenceDomainDefaultCompositeFieldDependencies$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultCompositeFieldDependencies$'
make check RUN='^TestReferenceDomainDefaultCompositeFieldDependencies$' RACE=1
# Unchanged companion explicitly owns failed/canceled restore acceptance.
make check RUN='^TestReferenceDomainDefaultNativeControls$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultNativeControls$'
make check RUN='^TestReferenceDomainDefaultNativeControls$' RACE=1
```

Use external900-second deadlines. Standalone public smoke must consume real dump
bytes on the same attachment and an independent Driver, observe fresh18 writes,
and verify failed23502/canceled Restore preserves sentinel42/no bad_restore plus
a subsequent valid default write. Selected companion proof retains full v22/p52.

### PATH scalar contract

Shared PATH scenarios independently specify open/closed and singleton IO,
ordered/repeated coordinates, released scanner edges, direct/new-prepared/held
string/byte/Valuer/NULL transport, raw metadata, complete diagnostics and stages,
statement atomicity and recovery, five point-count comparisons and absent
generic sorting/grouping/index capabilities. Controls add stored defaults,
count-predicate CHECKs/views and explicit native same/fresh-driver exact replay.
Pinned18.6 oracle-first discovery validated the independent expectations before
native implementation; native regression proof exposed unsupported PATH columns.

```sh
make check RUN='^TestReferencePath(Values|Scanner|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferencePath(Transport|Metadata|Comparison)$' REFERENCE=1
make check RUN='^TestReferencePath(Controls|FeatureSmoke)$' REFERENCE=1
```

Use external900-second deadlines. Repeat these exact selectors without
`REFERENCE=1` for native proof; completed definitions require both modes before
review. Private `TestPath(Datum|Comparison|Descriptor|ExactReplay)` covers
immutable payload ownership, bit identity and scalar-versus-generic authority.
`TestRegtypeInventoryCapability` retains lookup-only companion1019.
Parser `TestPathParserAdmission` checks keyword/quoted/qualified typed-literal
ASTs, source positions and retained domain-only authority outside pg_catalog:
`make check RUN='^TestPathParserAdmission$'`.
FeatureSmoke is a permanent shared integration witness, not the separate
standalone public-API program required after implementation. The latter must
observe held inserts, metadata, NULL, comparisons, atomic failure/recovery and
public Dump/Reset/Restore outside the tracked tree.

Signed-special regression `TestReferencePathSignedSpecialReplay` independently
asserts PostgreSQL's signed-NaN/Infinity input and canonical display. Native
host-control phases inspect actual public Dump bytes for signed NaN coordinates
in rows, defaults and stored view constants across same/fresh-driver replay.
`TestPathExactReplay` separately requires bit-exact negative-NaN reinput.
Initial regression discovery selects the PostgreSQL subtest before the expected
native failure; completed proof uses the whole shared definition in both modes:
`make check RUN='^TestReferencePathSignedSpecialReplay$'` and the same command
with `REFERENCE=1`.

Review-boundary regressions `TestReferencePathWrapperStages` and
`TestReferencePathDomainReplay` cover the two released shallow wrapper stages
and constrained-domain stored-cast authority. They assert literal/bound wrapper
IO and complete rejection diagnostics, signed-special/NULL domain values,
catalog-owned domain identities, required outer-domain checks, distinct TEXT
conversion, and stable native same/fresh-driver logical replay.
Select `make check RUN='^TestReferencePath(WrapperStages|DomainReplay)$'`
in both modes. Pinned-oracle discovery confirmed all independently specified
wrapper/domain expectations before native regression proof; discovery filters
and temporary helpers have been removed. Standalone smoke additionally exercises
the two-stage wrapper and constrained-domain cast/check/replay boundaries while
retaining the original seven-row main table.

### POINT oracle-first contract

`TestReferencePointValues`, `Comparisons`, `Components`, `Diagnostics`,
`Metadata`, `Authorization`, `Controls` and `FeatureSmoke` define the bounded
POINT contract independently of native output: two input forms, binary64 IO,
eight point predicates and their aliases/routines, FLOAT8 coordinate reads and
updates, exact diagnostics/authority, and native logical replay. They do not
grant ordinary equality, sorting/grouping/index classes or adjacent geometry.
BOX/LSEG component scenarios retain multidimensional NULL reads and additionally
assert parenthesized nested coordinate access.

Stage A used a temporary PostgreSQL-only discovery filter in `pointScenario`.
The filter is now removed: all definitions use the same assertions on every
available backend. Oracle discovery does not itself claim native compatibility;
observe the native regression before implementing production behavior.

```sh
make check RUN='^TestReferencePoint(Values|Comparisons|Components|Metadata)$' REFERENCE=1
make check RUN='^TestReferencePoint(Diagnostics|Authorization|Controls|FeatureSmoke)$' REFERENCE=1
# Stage B: observe the native regression before production edits.
make check RUN='^TestReferencePoint(Comparisons|Components)$'
# Completed implementation: repeat both POINT groups with and without REFERENCE=1.
make check RUN='^TestReference(Box|Lseg)Components$'
make check RUN='^TestReference(Box|Lseg)Components$' REFERENCE=1
make check RUN='^TestReferencePointFeatureSmoke$'
```

The smoke expects `(1,2)` from both forms and held string/byte inputs, typed NULL,
all eleven spellings' explicit Boolean outcomes, FLOAT8 coordinates, update to
`(-5,2)`, exact22P02 location and held recovery. Its native-only phase checks
Dump/Reset/Restore and an unchanged pre-update cursor. Controls additionally
exercise same/fresh-engine replay, full precision independent of display GUC,
defaults/CHECK/view expressions, committed-only capture and failed-restore
atomicity. PostgreSQL has no native host-control equivalent.

### LINE scalar contract

Independent pinned18.6 oracle-first scenarios cover coefficient preservation,
all endpoint forms and permissive scanner edges, range/semantic/full-field
diagnostics and stages, direct/new-prepared/held bytes/NULL/Valuer bindings,
metadata, proportional scalar equality versus absent generic comparison,
character sizing, atomicity and same/fresh-driver persistent-object replay.
Native host-control phases are explicit; actual emitted SQL is replayed on
PostgreSQL in real mode without inventing a PostgreSQL control API.
Review regressions additionally cover released bracket-branch/legacy closing
rules, callable line_eq direct/prepared/held/qualified NULL/error recovery,
nonempty bytes/NaN inputs, and complete arity/type rejection diagnostics.
The same callable/operator handles cross authorized/revoke/denied/grant reuse
with true/false/strict-NULL inputs, and recover after equality arithmetic
overflow/underflow. Stored callable views return true/false/NULL through direct
and prepared reads before and after same/fresh-driver and PostgreSQL replay.
Rollback probes execute restored defaults and both accepting/rejecting equality
CHECK branches without changing redump contents.
The oracle-observed denied strict-NULL matrix is route-specific: direct and
new-prepared calls return NULL on both backends; the repeatedly held PostgreSQL
statements demand EXECUTE after generic-plan transition and report42501, while
native's analysis-identity-only statements still fold NULL per execution.
These are independently asserted backend outcomes, not a permission exemption
for nonnull inputs or an implementation of PostgreSQL executable-plan caching.

```sh
make check RUN='^TestReferenceLine(Values|Scanner|Diagnostics)$'
make check RUN='^TestReferenceLine(Values|Scanner|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceLine(Transport|Metadata|CharacterCasts)$'
make check RUN='^TestReferenceLine(Transport|Metadata|CharacterCasts)$' REFERENCE=1
make check RUN='^TestReferenceLine(Comparison|Callable|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceLine(Comparison|Callable|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestLine(InputOutput|Scanner|Comparison|Datum|Descriptor|ExactReplay)$'
make check RUN='^TestRegtypeInventoryCapability$'
make check RUN='^TestReferenceLine(Transport|Controls)$' RACE=1
```

Use external900-second deadlines. FeatureSmoke independently checks a held
insert's semantic-failure/no-publication/recovery boundary with typed nullable
rows. Actual standalone consumer smoke additionally checks public metadata and
Dump/Reset/Restore. Whole-project Docker-free verification is `make quickcheck`;
the commands above specify proof obligations, not passing evidence.

### BOX scalar contract

Eight bounded shared scenarios cover independently expected canonical IO,
direct/prepared/held bindings, raw metadata/cells, complete diagnostics and
positions, area comparisons versus coordinate sameness, raw POINT component
reads/writes, statement atomicity and native exact replay. Pinned18.6 oracle
discovery settled these definitions before production implementation.

```sh
make check RUN='^TestReferenceBox(Values|Comparison|Components|Diagnostics|Metadata)$'
make check RUN='^TestReferenceBox(Values|Comparison|Components|Diagnostics|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceBox(Controls|FeatureSmoke)$'
make check RUN='^TestReferenceBox(Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceBoxCharacterCasts$'
make check RUN='^TestReferenceBoxCharacterCasts$' REFERENCE=1
make check RUN='^TestBox(InputOutput|Comparison|Components|ExactReplay)$'
make check RUN='^TestRegtypeInventoryCapability$'
```

Use external900-second deadlines. Native control phases retain inverted
components, precise floats, signed zero, subnormals, infinities/NaN and NULL
through same/fresh-driver replay; held readers and failed/canceled/I/O-error
imports preserve their prior state. PostgreSQL exercises the same ordinary SQL
facts without invented host controls.

CharacterCasts independently checks BOX/POINT explicit constrained VARCHAR/CHAR
truncation and padding, unconstrained TEXT, direct/prepared/held bindings and
target metadata (including held NULL executions). Genuinely held INSERT/UPDATE
assignments fail with complete22001 diagnostics and unchanged rows, then accept
NULL on the same statement with exact affected counts and resulting state.
Valid-first/later-overlong multirow INSERTs retain statement atomicity coverage.
It was added oracle-first for the constrained-character sizing regression;
explicit cast truncation does not weaken assignment-length checks.

Standalone feature smoke (run separately from the corpus):

```sh
make check RUN='^TestReferenceBoxFeatureSmoke$'
make check RUN='^TestReferenceBoxFeatureSmoke$' REFERENCE=1
```

Expected exit0 means literal/held reverse inputs return identical canonical
BOX/POINT values and metadata, NULL binds stay NULL, area equality differs from
coordinate sameness, and raw component inversion survives native replay.
Use `RACE=1` on selected Controls proof when needed; whole-project Docker-free
verification is `make quickcheck`. These commands document proof obligations,
not a claim that this implementation round has already passed them.

### Circle scalar contract

The eight shared `TestReferenceCircle` scenarios cover input/output (including
observed release scanner edges), binds/metadata, all bounded circle predicates
and radius, complete diagnostics and atomicity, unavailable default capabilities,
24-byte binary COPY, native controls and actual PostgreSQL dump replay.
No scenario orders by a circle or imports internal providers. POINT operations
and circle arrays remain outside this scalar feature; the independent BOX/POINT
contract above is unchanged.

`Operators` projects direct/prepared subquery `ANY` results: smaller-area `<`
is true, NaN `<>` NaN is false, NULL-left and unmatched-plus-NULL are unknown,
and a decisive true comparison wins over another NULL candidate. These use
scalar subqueries, not circle arrays. `Controls` stores both generated radius
and a generated BOOL comparing area to a radius-two circle. Different centers
with the same area yield true, different areas and NaN yield false, and NULL
remains unknown. The same direct/prepared rows survive low-precision native
Dump/Reset/Restore and actual PostgreSQL dump replay; subsequent radius-six
and radius-two inserts exercise both restored generated expressions. Detached
reader/metadata and failed-restore atomicity assertions remain in that cycle.

Run selected proof on the same completed definitions in both modes:

```sh
make check RUN='^TestReferenceCircle(InputOutput|BindingsMetadata|Operators|Diagnostics|Capabilities|Binary|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceCircle(InputOutput|BindingsMetadata|Operators|Diagnostics|Capabilities|Binary|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestCircle(InputOutput|NumericEdges|Operators|DatumIdentity|Binary|Provider.*)$'
make quickcheck
```

Actual new-behavior feature smoke:

```sh
make check RUN='^TestReferenceCircleFeatureSmoke$'
make check RUN='^TestReferenceCircleFeatureSmoke$' REFERENCE=1
```

Expected: canonical seven-row center/radius transport, held string/byte binds,
NULL, area equality versus geometric sameness, distance3, exact negative-radius
error and unchanged count; native Dump/Reset/Restore repeats those expectations.
Use external900-second deadlines. Commands are verification obligations, not
claims of observed success. Whole-project Docker-free verification is quickcheck.

### Polygon scalar contract

The six shared `TestReferencePolygon` scenarios specify ordered vertex-list
input/output, the four documented forms, one/two/repeated vertices, strict NULL
behavior, cyclic/reverse `~=` rather than area equality, and polygon/point
containment including boundaries and a bounding-box false positive control.
Bindings inspect raw native string versus PostgreSQL byte cells before logical
normalization, held statement recovery, owned input, and empty/all-NULL metadata.
Diagnostics include eager absent ordinary comparisons and sorting/grouping,
malformed values, numeric range errors, positions and statement atomicity.
Diagnostic expectations were exercised against the pinned PostgreSQL18.6 fixture
during oracle-first discovery; none are derived from native results.

`Controls` and `FeatureSmoke` exercise defaults, CHECK and a view. The stored view
contains all three selected signatures, including `point <@ polygon`, and typed
NULL constants in the left, right and both operand positions of each signature.
Independent BOOL values, NULLs and column metadata are checked through direct and
prepared queries before and after replay, including new rows from default and
explicit inserts. Their explicit native-only Dump/Reset/Restore phase retains an
old cursor, preserves full float precision under low display precision, and rejects
a failed staged restore atomically. Real mode also replays the actual emitted
native dump SQL on PostgreSQL and checks the same stored expressions and new-row
outcomes. PostgreSQL has no native-control API.
Polygon OID604 differs from path602; no path execution is asserted. In particular,
closed PostgreSQL paths can also contain interior points. These scenarios do not
request arrays, binary codecs, other documented spatial operations or physical
storage/index/network/native-ABI mechanisms.

Selected proof and executable runtime smoke (external900-second deadlines):

```sh
make check RUN='^TestReferencePolygon(Values|Operators|Diagnostics|BindingsMetadata|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePolygon(Values|Operators|Diagnostics|BindingsMetadata|Controls|FeatureSmoke)$'
make check RUN='^TestPolygon(InputOutput|DatumIdentity|Operators|ProviderLifecycle)$'
make check RUN='^TestReferencePolygonFeatureSmoke$'
make check RUN='^TestReferencePolygonFeatureSmoke$' REFERENCE=1
```

Expected smoke: six ordered rows (four canonical triangles, a rotated vertex
sequence, NULL), BOOL row `true,false,true,true,false,NULL`, malformed held input
with unchanged count6, then persistent-object replay and successful default insert.
Commands state obligations, not observed passing evidence. Whole-project
Docker-free verification uses `make quickcheck`.

### MACADDR scalar contract

The eight shared scenarios cover documented forms and scanf edges, full
diagnostics/phases, comparison/NULL behavior, direct/prepared/held transport,
metadata, scalar/array identities and native persistent-object replay:

```sh
make check RUN='^TestReferenceMACADDR(Values|Scanner|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceMACADDR(Comparison|Transport|Identity|Boundaries|Controls)$' REFERENCE=1
make check RUN='^TestReferenceMACADDR(Values|Scanner|Diagnostics)$'
make check RUN='^TestReferenceMACADDR(Comparison|Transport|Identity|Boundaries|Controls)$'
make check RUN='^TestMACADDR(InputOutput|Scanner|Comparison|Datum|Descriptor)$'
```

Use external900-second deadlines. `make quickcheck` is the whole-project
Docker-free check. Optional selected race proof:
`make check RUN='^TestReferenceMACADDR(Transport|Controls)$' RACE=1`.

Pinned18.6 observations settle permissive scanf signs/prefixes/field widths,
uint64 saturation before uint32 truncation, English diagnostics and pq metadata.
pq nonnull values are textual bytes with ScanType any; native values are strings.
The explicit native phase replays the actual native dump on PostgreSQL, not a
handwritten equivalent, and checks defaults/CHECK/view behavior, deterministic
redump, failed-restore preservation and retained cursor metadata.

The feature smoke is a temporary external public-API Go program, not a duplicate
permanent test. Run it from the repository with
`timeout 900s go run /absolute/session-owned/macaddr-smoke/main.go`.
It asserts seven canonical inputs, held string/bytes/NULL frames, result metadata,
unsigned high-bit comparison, exact22P02/22003 diagnostics, atomicity and actual
Dump/Reset/Restore of typed persistent objects. Record the actual source,
argv/output and expected-versus-observed assertions, then remove the program.
The catalog living plan owns its complete execution obligations.

### Citext storage and value round trips

The untagged `TestReferenceCitextInstallation`, `Values`, `Assignment`,
`Metadata`, `Diagnostics`, `Controls`, `Temporary` and `FeatureSmoke`
definitions use one shared public SQL/API body. Independently specified values
preserve spelling, whitespace, decomposition, escapes, empty strings and NULL.
Assignment covers TEXT/VARCHAR/BPCHAR/BOOL/INET, integer-to-citext string-category
I/O and explicit citext-to-integer; parameter input is tested separately.
Metadata checks run before iteration, with separate native and lib/pq contracts.
The cohesive catalog helper first reads the current backend's actual scalar,
array, input/output routine and extension OIDs from single catalogs, then bounds
each joined catalog input by those identities. The identities are fresh
locators on every invocation, including after restore/recreation, never fixed
allocator expectations or cross-backend equality. Independent expected type
facts, reciprocal array links, dependency multiplicities, routine signatures,
ownership and namespace relationships still run directly and prepared.
**[INFERENCE]** The former unbounded multi-catalog joins could materialize a
`pg_type × pg_proc × pg_proc`-sized intermediate before filtering, making
provider inventory growth especially costly on the prepared route under race
instrumentation. The observed 30-second Controls failures motivate bounding the
fixture inputs, not a demonstrated production optimizer defect or a deadline
increase. Fresh selected dual-mode and race evidence is required after this
fixture change:

```sh
make check RUN='^TestReferenceCitext(Metadata|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceCitext(Metadata|Controls)$'
make check RACE=1 RUN='^TestReferenceCitext(Metadata|Values|Controls|Temporary)$'
```

Diagnostics compare every exposed logical field, including expected empty fields.
Controls include retained readers, independent Driver restore, failed-replay
atomicity and actual emitted Dump SQL replay on the pinned PostgreSQL fixture.
The `round_trip`, `control_boundaries` and `independent_restore` subtests own
separate bounded fixtures: Reset/Restore and its retained reader stay together;
cancelled/context-I/O controls retain their post-failure direct/prepared checks;
independent restore retains its allocator spacer, failed replay, old reader and
deterministic redump. These independent lifecycles do not share one 30-second
scenario budget. The separate malformed-replay oracle still checks no publication.
TEMP installation is bounded to existing registry placement and owner lifetime.
Feature smoke exercises literal/direct/held input, atomic encoding rejection,
recovery and native controls or PostgreSQL SQL recreation.

```sh
make check RUN='^TestReferenceCitext(Installation|Values|Assignment|Metadata|Diagnostics|Controls|Temporary|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceCitext(Installation|Values|Assignment|Metadata|Diagnostics|Controls|Temporary|FeatureSmoke)$'
make check RACE=1 RUN='^TestReferenceCitext(Values|Controls|Temporary)$'
```

All eight definitions run both available backends without discovery filters.
Pinned PostgreSQL18.6 oracle observations ground the values, full diagnostics,
TEMP outcomes and generated-OID metadata; native controls and actual emitted
Dump replay remain separate verification obligations, not inferred parity.
The installed provider also changes the exact native availability inventory:

```sh
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestCitext(InputOutput|Identity|Coercion|Metadata|Remap|ProviderIdentity|ProviderConflicts|ProviderImport)$'
```

Standalone public smoke source is supplied as the session artifact
`local://citext-smoke.go`, not a checked-in helper. After materializing it at
`/tmp/stuntgres-PG18-TYPE-CITEXT-smoke.go`, run from the repository root:

```sh
go run /tmp/stuntgres-PG18-TYPE-CITEXT-smoke.go
```

Expected: quiet exit0 after literal/direct/held byte and NULL round trips,
metadata-before-iteration, atomic late invalid UTF8 rejection and held-statement
recovery, then actual Dump/Reset/Restore with retained old rows/metadata intact.
The runner owns an unwrapped Driver/connector pool; it uses no internal packages.
This native smoke does not substitute for real PostgreSQL emitted-SQL replay.


### Domain identity lifecycle contract

`TestReferenceDomainIdentityLifecycle`, `Permissions`, `Diagnostics`,
`DropAtomicity`, `PreparedMetadata`, `NativeControls`, `Atomicity`, and
`Boundaries` are the cohesive identity-lifecycle inventory. They cover same-OID
rename/move and recursive carriers, existing OWNER/ACL behavior, schema-owner
DROP, qualified missing-schema IF EXISTS, whole-list atomicity and scoped
dependency closure, actual held utilities/consumers and base metadata, native
current-name replay and failure preservation, real DROP lock cancellation, and
explicit-transaction/read-only boundaries. Existing dependency and recreation
tests retain their separate baseline contracts; no duplicate feature-smoke
reference definition is added.

Pinned18.6 observations are encoded as complete utility diagnostics, ordered
multi-object notices, exact companion spellings, and held explicit-cast outcomes.
Both constrained and unconstrained old-spelling casts survive identity edits,
then fail42704 at character12 after search_path forces reanalysis. Failed DROP
publication emits no CASCADE notice. Those eight completed scenarios run both
available backends without discovery filters, loggers or intentional failures.
The cancellation observer reads catalog identities while DROP waits; a new table
reader would queue behind DROP's AccessExclusiveLock. Table values and the SAME
preheld consumer are checked after cancellation joins, without longer deadlines.
Owner ACL witnesses scan the public `typacl` array and compare its exact ACLITEM
bag, including all grantors/grantees/masks and multiplicities; owner/PUBLIC array
storage order is not an authorization contract. Temporary destinations resolve the actual
session-owned namespace before normal CREATE/owner/temp checks, never create one.
After schema-owner CASCADE, the survivor's comment is read from its exact
`pg_description` object/class/subobject address. Surviving rows are selected
under the genuinely delegated actor role, proving retained SELECT access
rather than relying on an inquiry function or a privileged-owner read.
The recursive closure retires indexes automatically through their exact parent
stable attributes, including direct/derived keys, INCLUDE and predicates, before
removing the domain types. Sibling-column and unrelated-parent indexes survive.
Domain-root traversal also retains view output-column datatype edges: visiting
one can reach a downstream view's `_RETURN` owner before visiting the source
table column. Descending dependency visitation and reverse postorder preserve
this ownership order; output-column subobjects disappear from diagnostics only
when their whole view is retired. The recursive witness keeps all14 dependents
and their exact pinned notice order, without sorting labels for parity.
Lifetime captures of a selected column's datatype are not SQL type-to-view
dependencies. The existing stored-query dependency classifier excludes only
those passive whole-view edges, preserving explicit SQL typename references
and both real output-column and source-column traversal paths.

```sh
make check RUN='^TestReferenceDomainIdentity(Lifecycle|Permissions|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceDomainIdentity(DropAtomicity|PreparedMetadata|Atomicity|Boundaries)$' REFERENCE=1
make check RUN='^TestReferenceDomainIdentityNativeControls$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraintBoundaries$' REFERENCE=1
```
Review H1 adds `TestReferenceDomainIdentityPreparedArrays`, a distinct held-input
carrier regression: bracketed domain-array and direct companion annotations,
checked/unchecked elements, array bounds and NULLs, current coercion/CHECK
semantics, environment retirement and dropped/recreated companion lifetime.
Pinned18.6 retains both old annotation forms through rename/move; environment
reanalysis then fails42704 at character13, including after restoring the path.
Complete parameter coercion/CHECK diagnostics retain their portal context.
Dropped/recreated companions use their captured physical OID in PostgreSQL's
XX000 diagnostic; native keeps its existing missing-name and changed-parameter
boundaries. This definition runs both backends without discovery scaffolding.
The same occurrence map retains the resolved companion ID and reconstructs its
current element or direct-array annotation under live reciprocal identity checks.
Both array and element are analysis dependencies. The canonical stamp rule covers
both pair members and certified companion displacement, while semantic element
mutations invalidate normally; unrelated arrays and direct array ACL exclusions
are unchanged. Private `ArrayCapture` and `ArrayPlanStamp` supplements inspect
these retained-input and immutable-generation boundaries.

```sh
make check RUN='^TestReferenceDomainIdentityPreparedArrays$' REFERENCE=1
```


Package-function supplements and migrated signature/capture callers:

```sh
make check RUN='^TestDomainIdentity(Descriptor|Grammar|Stage|DropStage|Authorization|PlanStamp|GrantStamp|PreparedCapture|CaptureRetirement|IndexClosure|ArrayCapture|ArrayPlanStamp)$'
make check RUN='^Test(DomainCheckInputVersusIdentity|DomainNullabilityAuthority|Info(CardinalNumber|SQLIdentifier|TimeStamp|YesOrNo)BuiltinDescriptor)$'
make check RUN='^Test(ExpectedOfCopiesHeldInputs|CompositeExprVisitorsAndRetag|CompositeAlterFunctionalRetainedFields|JSONBRecord(SourceProjection|ArgumentIdentity)|ViewLiteralProtocolAnnotations)$'
make check RUN='^TestReferenceDomain(Dependencies|PreparedLifecycle|Metadata|NativeControls)$'
make check RUN='^TestReferenceDomainIdentity(Atomicity|PreparedMetadata)$' RACE=1
```

The preexisting public regression selection above also requires `REFERENCE=1`.
These are selected proof commands, not claims of exercised verification.

Each selected check has an external900-second deadline and must also run without
`REFERENCE=1`, using identical definitions. The root alone runs checks, generation
and formatting. Native pre-cancel and Dump/Reset/Restore phases are explicit
public-control witnesses; PostgreSQL supplies the shared ordinary SQL oracle.
A standalone public native executable was also exercised during implementation;
its temporary source location, argv and observations belong to the part's
verification evidence, not checkout instructions. The repository-resolvable
selectors above are the enduring regression entry points; the shared reference
scenarios supply the PostgreSQL oracle.


### Text-search scalar IO

`TestReferenceFTSTypesValues`, `Binds`, `Query`, `Boundaries`, `Diagnostics`,
`Notices`, `Metadata`, `Controls`, `Scope` and `FeatureSmoke` use the common
fixture and public APIs. The contract covers canonical vector lexemes,
positions/weights, Boolean/phrase query structure and flags, literals/binds,
NULL versus empty values, full diagnostic fields/phases, NOTICE phases and
suppression, exact catalog/driver metadata, atomic failures and real native
logical round trips. No discovery filter, logging or learned golden remains.

Root runs each selection in quick and pinned18.6 modes with external900-second
deadlines; commands shown without REFERENCE must also run with `REFERENCE=1`:

```sh
make check RUN='^TestReferenceFTSTypes(Values|Binds|Query)$'
make check RUN='^TestReferenceFTSTypes(Boundaries|Diagnostics|Notices)$'
make check RUN='^TestReferenceFTSTypes(Metadata|Controls|Scope)$'
make check RUN='^TestReferenceFTSTypesFeatureSmoke$'
make check RUN='^TestFTS(VectorCanonical|VectorLimits|QueryGrammar|QueryStructure|QueryLimits|CodecCancellation)$'
make check RUN='^TestFTSTyped(Value|Metadata|Scope)$'
make check RUN='^TestPGTypeBuiltinFacts$'
make check RUN='^TestReferencePGType(Builtins|Admission)$'
```

Feature smoke expects affected1 inserts for weighted/positioned vector and
compound-query rows, held bytes/string/NULL/empty inputs, exact canonical
ordered rows and metadata, the actual empty-query NOTICE, full malformed and
NOT NULL errors without partial insertion, and deterministic native
Dump/Reset/Restore with retained cursor output. Real mode executes the actual
native dump on PostgreSQL and independently checks its resulting rows/types.

Backend-specific evidence is explicit: native string versus pq byte raw
values/ScanTypes; PostgreSQL machine-stack54001 versus native iterative large
queries; first-held PostgreSQL runtime-TEXT constant folding versus native
per-execution evaluation; and oversized pq ErrorResponse failure versus actual
server54000 obtained using PG-only `pg_input_error_info`. Native excludes that
function, companion arrays, matching/operators and FTS index execution.
The query arena's fixed32 pending-operator limit remains shared grammar, not
one of those physical distinctions. Selected proof does not replace root's
whole-project Docker-free quickcheck or post-integration finalcheck.

### Cube scalar oracle-first contract

`TestReferenceCubeInstallation`, `Grammar`, `Assignment`, `Diagnostics`,
`Metadata`, `Controls`, and `FeatureSmoke` are permanent shared-body definitions
for installed cube1.5 scalar text storage. Expectations were independently
specified from released PostgreSQL18 grammar/codecs and corrected against root's
pinned18.6 oracle observations before native production work. Coverage includes declared
identity, orientation-preserving text, coincident-corner collapse, 0/100/101
dimensions, IEEE special values and precision, direct/new/held prepared binds,
owned bytes/NULL, directional character I/O, full diagnostic records and write
atomicity, raw extension metadata, retained readers and native control replay.
`TestReferenceCubeDiagnosticPrecedence` adds independently specified diagnostic
boundaries confirmed by root's pinned18.6 oracle run before the native remedy:
incomplete punctuation beats overflow; bracketed boxes require `]` before
dimension checks; completed productions check mismatched dimensions, then the
100-dimension limit, then convert coordinates before rejecting trailing garbage.
Mismatch details discard inter-token ASCII whitespace while preserving numeric
token spellings, not formatted float values. Literal and bound direct/prepared
routes assert complete diagnostics and unchanged ordered sentinel rows.
The PostgreSQL control branch uses real SQL extension recreation. Real-mode
native control execution also replays the actual emitted dump SQL in PostgreSQL.

```sh
make check REFERENCE=1 RUN='^TestReferenceCube(Installation|Grammar|Assignment|Diagnostics|DiagnosticPrecedence|Metadata|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestReferenceCube(Installation|Grammar|Assignment|Diagnostics|DiagnosticPrecedence|Metadata|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
```

Root runs these commands with an external900-second deadline. Repeat both
selected modes after expectation/implementation edits. Whole-project Docker-free
verification uses `make quickcheck`, not a broad selected check. Supplementary
units cover immutable coordinate/descriptor authority, coercion directions,
float display versus exact literals, genuine staged manifests and import remapping:

```sh
make check RUN='^(TestCube(Input|Output|Identity|Coercion|Cancellation|DiagnosticStages|MismatchDetail)|TestCubeProvider(Identity|Conflicts|Controls)|TestAvailableExtensions(Projection|StatementFrontier))$'
make check RACE=1 RUN='^(TestCube(Cancellation|DiagnosticStages|MismatchDetail)|TestCubeProviderControls|TestReferenceCube(Assignment|Diagnostics|DiagnosticPrecedence|Metadata|Controls))$'
```

The focused behavioral smoke selector is
`make check RUN='^TestReferenceCubeFeatureSmoke$'` (also with `REFERENCE=1`).
It expects literal box storage, a genuinely held reversed-endpoint bind, NULL,
exact rows and metadata, and native Dump/Reset/Restore or PostgreSQL extension
recreation. The separate standalone public API executable is supplied as the OMP
session artifact `local://cube-smoke.go`. Materialize that artifact into the
session-files directory as `$SESSION_TMP/cube-smoke.go`, then run from the
repository root (standard library plus the public stuntgres module only):

```sh
timeout 900s go run "$SESSION_TMP/cube-smoke.go"
```

It is quiet on success and exits nonzero on a mismatch. It asserts owned byte/
held bindings, typed NULL, raw cells and empty metadata, genuine codec/regtype
identity, exact settled diagnostics and failure atomicity (including incomplete
overflow, missing box bracket, overlimit-with-overflow and spelling-preserving
mismatch boundary representatives), low-display-precision
dump exactness, retained readers, same/fresh-driver replacement and deterministic
redump after failed Restore. Native's selected capability is text codecs
only: no cube arrays, advanced operators/functions, or binary handlers.
PostgreSQL's real cube_recv/cube_send and pq unknown-OID metadata are explicit
backend differences, never normalized into nonexistent native capabilities.

### Information-schema sql_identifier selected proof

The untagged shared `TestReferenceInfoSQLIdentifier` scenarios cover qualified
user-declared domain columns over NAME: exact spelling/UTF8 byte clipping,
NULL/empty/omitted values, direct/prepared/held binds, raw NAME metadata and
cached metadata after EOF/Close, complete encoding diagnostics with execute-stage
atomicity, catalog/base/companion relationships, and native control round trips.
Native controls include independent-driver restoration, retained cursor values,
and failed/canceled staged replacement. The companion remains nonexecutable;
existing information-schema view carriers are unchanged.

Run each command with an external 900-second deadline:

```sh
make check RUN='^TestReferenceInfoSQLIdentifier(Values|Metadata|Diagnostics|Identity|NativeControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceInfoSQLIdentifier(Values|Metadata|Diagnostics|Identity|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferencePGTypeAdmission$' REFERENCE=1
make check RUN='^TestReferencePGTypeAdmission$'
make check RUN='^Test(InfoSQLIdentifierBuiltinDescriptor|InfoTimeStampBuiltinDescriptor)$'
```

The public feature smoke is
`make check RUN='^TestReferenceInfoSQLIdentifierFeatureSmoke$'` and the same
command with `REFERENCE=1`. It expects three inserted rows containing `MiXeD`,
a clipped 61-ASCII-plus-é value, and NULL; exact NAME metadata; qualified
domain/base catalog identity; a late malformed UTF8 parameter raising 22021
without partial insertion; and native Dump/Reset/Restore with fresh reads.
The PostgreSQL phase shares the SQL assertions but has no native-control API.

#### Bounded information-schema identity query efficiency

The sql_identifier, cardinal_number and yes_or_no identity witnesses prefilter
their domain-name and witness-column/relation inputs before joining catalogs.
These predicates duplicate the original outer WHERE conditions; all joins,
outer conditions, projected metadata, independently expected rows and
direct/prepared executions remain intact. This avoids joining unrelated domain
and attribute candidates on every repeated native-control identity check.
The same-owner retained cursors, fresh destination, repeated round trips,
failure atomicity and cancellation assertions retain their original fixtures,
30-second contexts and lifetimes. No production behavior or deadline changes.

This is a bounded response to the post-merge race-suite fixture deadline
failures, not measured proof of their elimination. The separate exhaustive
object-address inventory batching remedy is documented in its owning README.

The complete affected caller closure requires each selected command below in
native mode and again with `REFERENCE=1` (pinned PostgreSQL18.6 UTF8/C):

```sh
make check RUN='^TestReferenceInfoSQLIdentifier(Identity|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferenceInfoCardinalNumber(Identity|NativeControls|NativeFreshRestore|NativeRestoreAtomicity|FeatureSmoke)$'
make check RUN='^TestReferenceInfoYesOrNo(Identity|NativeControls|FeatureSmoke)$'
```

Use an external900-second deadline for each selected command. Repeat the three
commands with `RACE=1` for the reported race-suite paths. Fresh whole-project
`make quickcheck`, the information_schema.user_mappings feature smoke in both
modes, and independent review are required before remedy integration; selected
proof does not replace post-integration `make finalcheck`.

### INET mask, network and merge contract

The untagged `TestReferenceINETMaskValues`, `Boundaries`, `Resolution`,
`MetadataAndBinds`, `Diagnostics`, `RoutineIdentity`, `Authorization`, `Stored`
and `FeatureSmoke` definitions cover exactly `hostmask(inet)`,
`netmask(inet)`, `network(inet)`, `inet_merge(inet,inet)` and both
`set_masklen(inet/cidr,integer)` overloads. Values are independently specified
from the PostgreSQL18 manual/released sources and explicit bit-boundary
reasoning, not native-derived goldens or backend equality alone.

Root observed all nine corrected PostgreSQL-only discovery definitions pass
against pinned18.6, then removed the temporary filter and observed genuine
unsupported native function/ACL failures before releasing production work.
Those initial gates are not final dual-mode implementation proof. The completed
definitions run every available backend, with native-only control phases for
Reset/Dump/Restore. Routine identity uses the existing public regprocedure
signature/return/strictness metadata on both backends; additional pg_proc columns
not exposed by native are checked explicitly on PostgreSQL only, without
expanding native catalog infrastructure.
Unknown setter inference is exercised through the SAME genuinely unconstrained
held statement in both modes, including textual INTEGER binds, NULL and -1
recovery with exact INET metadata and retained host bits. The supplementary
`pg_prepared_statements` parameter vector is PostgreSQL-only: native does not
expose that preexisting catalog relation.

Pinned18.6 corrected three source-derived hostmask spellings:
IPv6 /96 gives `::255.255.255.255`, /97 gives `::127.255.255.255`, and
/111 gives `::0.1.255.255`. It confirmed -1 as full width, INET versus CIDR
setter return types and host-bit handling, preferred unknown INET/INTEGER
inference, complete unpositioned22023 provider diagnostics at Execute, strict
NULL/input-child distinctions, and constant/parameter preflight under
FALSE/empty/LIMIT0 versus excluded row-dependent laziness.
The nonthrowing hostmask/netmask/network inquiries retain the ordinary
simplifier. Merge/setter calls use the existing shared strict-NULL-or-evaluate
preflight owner: children convert first, then literal/imported-parameter NULL
proofs discard non-invocations before routine admission, then the immutable
evaluator applies its ordinary row/stage/domain dependence checks. This
preserves both provider failures without demanded rows and strict-NULL ACL
frontiers; no executable-result cache or global eager-function rule is added.

For each of the six signatures, pinned18.6 also observed a non-NULL all-constant
call on the SAME already-warmed PostgreSQL statement succeeds after EXECUTE
revocation; direct/new prepared calls still fail42501. Only that exact PG held
route has a backend-specific expectation. Native analysis identities remain
non-executable metadata, not a new result or permission cache. Exact setter
sibling-overload isolation and ordinary table/schema privileges remain separate.

Root-only selected commands, with an external900-second deadline each:

```sh
make check RUN='^TestReferenceINETMask(Values|Boundaries|Resolution|MetadataAndBinds|Diagnostics|RoutineIdentity)$' REFERENCE=1
make check RUN='^TestReferenceINETMask(Authorization|Stored|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceINETMask(Values|Boundaries|Resolution|MetadataAndBinds|Diagnostics|RoutineIdentity)$'
make check RUN='^TestReferenceINETMask(Authorization|Stored|FeatureSmoke)$'
make check RUN='^TestNetworkMask(Values|Boundaries|Validation|Reconstruction|StrictDemand|Cancellation|OriginalAuthority|StoredLiteralIdentity|ParserAdmission|Allocations)$'
```

Repeat both selected reference modes after definition/implementation edits.
Use `make quickcheck` for whole-project Docker-free verification. Existing
neighboring `TestReferenceINETFormat` dual-mode selectors below also cover the
shared carrier/result-selection changes.

Standalone public consumer source is handed to root as
`local://net-mask-smoke.go`. Materialize it as
`net-mask-smoke-temporary/main.go` within this module, run
`go run ./net-mask-smoke-temporary/main.go` with external900-second deadline,
and remove the temporary directory after recording evidence. No permanent
smoke command is installed. The consumer asserts six manual results,
nonbyte IPv6, -1 suffix distinctions, direct/held and empty/all-NULL metadata,
complete held-error recovery, generated/default/view recipes, same/fresh
Driver restoration and retained cursor ownership. Expected exit0, empty stderr,
and these summaries printed only after all assertions and checked closes:

```text
masks: 0.0.0.3 | 255.255.255.0 | 192.168.1.0/24
rewrite/merge: 192.168.1.5/16 | 192.168.0.0/16 | 192.168.0.0/22
ipv6: ::7fff | ffff:ffff:ffff:ffff:ffff:ffff:ffff:8000 | 2001:db8::8000/113
full-width: -1 accepted for INET/CIDR; CIDR suffix retained
metadata/null: INET INET CIDR INET CIDR CIDR; empty/all-NULL preserved
held recovery: 22023 invalid mask; 22023 mixed families; valid again
stored replay: all six signatures; same/fresh Driver 3 rows
```

These operations add no INET XOR/integer shifts, network arrays,
grouping/hash providers, DNS or OS networking. Subnet predicates are covered below.

### INET formatting oracle-first contract

The untagged `TestReferenceINETFormatValues`, `Abbreviation`, `Broadcast`,
`Resolution`, `MetadataAndBinds`, `Diagnostics`, `RoutineIdentity`,
`Authorization`, `Stored`, and `FeatureSmoke` definitions specify the eight
selected signatures: abbrev(INET/CIDR), host, text, family, masklen,
inet_same_family and broadcast. Initial expectations come from the PostgreSQL18
manual/released sources, with corrections grounded only in pinned PostgreSQL
observations, never native output. Root completed all ten PostgreSQL-only
discovery definitions against pinned18.6, then removed the sole temporary
native filter and observed the unsupported native regression before production
authorization. The shared definitions now exercise both available backends.
The native-only control phase inside Stored is an API distinction, not a
discovery filter. New production edits still require fresh selected proof.

```sh
make check RUN='^TestReferenceINETFormat(Values|Abbreviation|Broadcast|Resolution|MetadataAndBinds|Diagnostics|RoutineIdentity)$' REFERENCE=1
make check RUN='^TestReferenceINETFormat(Authorization|Stored|FeatureSmoke)$' REFERENCE=1
# Native proof uses the same definitions without any discovery filter:
make check RUN='^TestReferenceINETFormat(Values|Abbreviation|Broadcast|Resolution|MetadataAndBinds|Diagnostics|RoutineIdentity)$'
make check RUN='^TestReferenceINETFormat(Authorization|Stored|FeatureSmoke)$'
# Kernel, carrier, parser and original-authority supplements (not public SQL):
make check RUN='^TestNetworkFormat(Values|Abbreviation|Broadcast|Validation|OriginalAuthority|Reconstruction|StrictDemand|Cancellation|ParserAdmission|StrictParameterNull|StoredLiteralIdentity)$'
```

Each command has an external900-second deadline and is root-only during this
assignment. Repeat both selected modes after expectation/implementation changes.
Ordinary whole-project Docker-free verification uses `make quickcheck`.

Discovery obligations include distinct CIDR and promoted-INET output, IPv6
truncated-word boundaries and mapped dotted tails, full/zero/nonbyte broadcast,
NULL/empty raw metadata, typed held bind recovery, full diagnostics and routine
ACL demand frontiers across direct/newly prepared/same warmed statements.
Unconstrained TEXT has known Length MaxInt64 on both backends, following the
existing character descriptor contract; INT4, BOOL and INET have no length.
Native stored-view columns have catalog-known nullable descriptors, while direct
computed and pq descriptors retain unknown nullability. The consumer smoke
asserts that existing distinction explicitly rather than altering driver metadata.
The odd-zero-run input `2001:db8:0:1:0:2::/112` asserts
`2001:db8:0:1::0/112`, initially a released-source hypothesis and now exercised
without reported failure in root's complete Abbreviation discovery run.
Expectations are never native-derived goldens; source evidence alone is not an
oracle observation.

Root's initial pinned18.6 discovery observed
`abbrev(CIDR '::ffff:0.0.0.0/111') = '::ffff:0.0/111'`, correcting the
source-derived `::ffff:0/111` hypothesis. The failed discovery run also exposed
test callback-index and denial-argument plumbing bugs; correcting those does not
establish outcomes for routes that the failure prevented from completing.

Root's focused pinned18.6 ACL discovery identified one distinct frontier for
each of the eight signatures: a non-NULL all-constant selected call on the
same already-warmed PostgreSQL statement succeeds after EXECUTE revocation,
while direct and newly prepared calls are denied. The semantic
`pgWarmConstant` case flag preserves that observed PostgreSQL success only on
the same-held route. All other tested ACL frontiers retain their independent
expectations. Native prepared analysis identities are not executable-result
caches: this PostgreSQL folded-plan distinction does not authorize a new
native cache or broader prepared machinery. Both modes are rerun after
production changes; initial oracle discovery does not replace that proof.

The complete temporary consumer source is handed to root as
`local://net-format-smoke.go`. Materialize it as
`net-format-smoke-temporary/main.go` inside this module, then execute
`go run ./net-format-smoke-temporary/main.go` with external900-second deadline.
Remove the temporary directory after recording evidence. The executable uses
only public APIs and independently checks all eight signatures, generated/view/
default recipes, raw metadata including empty/all-NULL streams, exact held22P02
recovery and atomicity, retained rows and Dump/Reset/Restore into a fresh Driver.
Success is exit0, empty stderr and these seven lines, printed only after all
assertions and checked resource closes:

```text
format: 192.168.1.5/24 | 192.168.1.5 | 192.168.1.5/24
cidr: 10.1/16 | 2001:4f8:3:ba/64 | ::ffff:1.2.3/120
broadcast: 192.168.1.255/24 | 2001:db8::7fff/113
family/mask/null: IPv4=4/24; IPv6=6/113; strict NULL
metadata: TEXT INT4 BOOL INET; empty/all-NULL preserved
held recovery: 22P02; valid again; 2 rows unchanged
stored replay: selected calls preserved; fresh-driver 2 rows
```

The smoke's odd-zero-run assertion retains the pinned source-derived outcome.
Adjacent helpers, operators, arrays and protocol expansion are not added by
these definitions.


### Selected-call comparison and null-test consumers

`TestReferenceCompareNullFunctionBoundaries` independently asserts selected-call
comparison/null-test results and typed nonboolean logical failures through
direct and prepared execution in both modes. Predicate-valued call arguments
remain an explicit native capability boundary, with independent PostgreSQL
success expectations; admitting calls as predicate leaves does not widen their
argument language.

```sh
make check RUN='^TestReferenceCompareNullFunctionBoundaries$'
make check RUN='^TestReferenceCompareNullFunctionBoundaries$' REFERENCE=1
```

Use an external900-second deadline per command. These selectors also provide
the focused consumer smoke: exact BOOL result bags, empty INT4 filter metadata,
and construct-specific42804 diagnostics, not an obsolete syntax exclusion.

### INET bitwise and arithmetic selected proof

The untagged `TestReferenceINETOperator` definitions independently specify
INET `~`, `&`, `|`, both orders of address-plus-INT8, address-minus-offset,
and INT8 address differences. Tables include both address widths, masks and
host bits, CIDR/domain coercion, strict NULL, empty-result metadata,
direct/prepared/held string/byte/nil binds, complete English diagnostics,
error stages, failed-write atomicity and stored default/generated/view recipes.
Native controls reconstruct recipes in the same and fresh Driver; emitted
native SQL is also executed by the real PostgreSQL fixture.

```sh
make check RUN='^TestReferenceINETOperatorReleaseEdges$' REFERENCE=1
make check RUN='^TestReferenceINETOperator(Diagnostics|ErrorFrontier)$' REFERENCE=1
make check RUN='^TestReferenceINETOperator(Values|Resolution|MetadataAndBinds|StoredRecipes|FeatureSmoke)$' REFERENCE=1
```

The pinned PostgreSQL18.0 run confirmed source-derived INT64_MIN
offset subtraction and modular extreme IPv6 differences. Bound parameters
raise the same provider errors at Execute even behind FALSE/on empty sources;
valid or NULL bound inputs retain zero rows there. Commuted-addition range
errors include Context `SQL function "int8pl_inet" statement 1`.
Run all three selectors both with and without `REFERENCE=1`. Every command
needs an external900-second deadline. The bounded shared feature smoke is
`make check RUN='^TestReferenceINETOperatorFeatureSmoke$'`, also `REFERENCE=1`;
it asserts documented values/types, IPv6 carry, prefix retention, NULL,
full diagnostics, held recovery and actual stored-expression execution.
The task session additionally supplies a public-only executable smoke; it is
not a production helper or a permanent command.

Private operator-provider and binding/reconstruction proof:

```sh
make check RUN='^TestNetworkOperator(Bitwise|Arithmetic|Resolution|OwnershipAndCancellation|Allocations|BindingAndReownership|StoredValidation)$'
```

These private units supplement the public dual-mode scenarios with inline
carrier invariants, every-byte carries/borrows, exact descriptor ownership,
ordinary cancellation, zero-allocation successful evaluation, provider
corruption rejection, reownership/equality, and scoped imported-parameter
preflight barriers. Existing bitwise consumers remain covered with
`make check RUN='^(TestBitStringOperators|TestBitwise)'`; their reference
definitions retain the shared testing guide's separate dual-mode obligations.

### Tuple identifier selected proof

The untagged TID scenarios independently specify PostgreSQL18 scalar input
(including permissive/empty/unsigned-long forms), canonical string transport,
raw metadata, direct/prepared/held parameters, strict NULL predicates, unsigned
component ordering, equality consumers and declared-key btree/hash behavior.
Full malformed-input/semantic/authorization diagnostics retain their observed
source locations and phases; later bad tuples publish no rows.
The focused Coercion matrix covers typed TEXT/VARCHAR/BPCHAR input, canonical
character output, explicit truncation versus assignment overflow/padding, typed
NULL, direct/prepared routes and rejection of implicit character comparisons.

```sh
make check RUN='^TestReferenceTID(Values|Comparison|Coercion|Diagnostics|Controls|Authorization|FeatureSmoke)$'
make check RUN='^TestReferenceTID(Values|Comparison|Coercion|Diagnostics|Controls|Authorization|FeatureSmoke)$' REFERENCE=1
make check RUN='^(TestTID.*|TestIndexClausesSystemIdentity|TestIndexClassMetadata.*)$'
```

Affected class inventory consumers require both modes with
`RUN='^TestReferencePGOpclass(Inventory|IndexSemantics|Diagnostics|HeldExecution|Lifecycle|FeatureSmoke)$'`.
Standalone feature smoke is `make check RUN='^TestReferenceTIDFeatureSmoke$'`
and the same command with `REFERENCE=1`: exact ordered IDs2,1,3, canonical TID
strings, held textual binds, NULL, permissive input and failed-INSERT atomicity.
Every command needs an external900-second deadline. Selected race proof can use
`make check RUN='^TestReferenceTID(Values|Controls)$' RACE=1`.

Native-only control phases exercise actual Dump/Reset/Restore, deterministic
redump, PostgreSQL replay of the native dump and retained raw cursor metadata.
Native ctid stays42703; PostgreSQL's actual ctid values are not pinned to allocator
coordinates. Scalar support does not imply physical rows, executable arrays,
binary transport or development functions. Discovery filters are not retained.

### Multirange constructor and expansion selected proof

The untagged `TestReferenceRangeMore` definitions specify unary generic
`multirange(anyrange)` and `unnest(anymultirange)` for the three executable
integer/numeric companion families. Independent expectations cover canonical
values, ascending raw member order, numeric gaps/scale, NULL/empty/universal,
direct/prepared/held typed binds, raw metadata on empty results, domains,
target/FROM/lateral/ordinality/zip/nesting, full English diagnostics, ACL
rechecks, stored recipes, statement atomicity and native control ownership.

```sh
make check RUN='^TestReferenceRangeMore(Values|Metadata|Binds|Composition|Variadic)$'
make check RUN='^TestReferenceRangeMore(Diagnostics|DiagnosticContracts|Authorization)$'
make check RUN='^TestReferenceRangeMore(Stored|Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestRangeMore'
```

Run each reference selector again with `REFERENCE=1`; all checks need external
900-second deadlines. Selected held-bind/control/cancellation race proof uses
`RACE=1`. Reserve quickcheck for whole-project Docker-free verification.
The oracle is the shared pinned PostgreSQL18.0 fixture, never a local server.
No discovery filters or observers belong in completed definitions.

The public executable feature smoke is maintained as the task's session source,
not a production helper or a second SQL harness. Run `go run` on its absolute
session path from the repository root. Quiet success requires the canonical
three-family constructor results, ascending prepared expansion, strict
NULL/empty distinctions, fresh held cursor state, exact diagnostics and
actual native controls; a mismatch exits nonzero.

### UUID extraction selected proof

The untagged `TestReferenceUUIDExtract` families specify PostgreSQL 18.0
`uuid_extract_timestamp(uuid)` / `uuid_extract_version(uuid)` outcomes.
Only RFC variant prefixes 8/9/a/b are accepted; all version nibbles 0–15 are
returned as INT2. Timestamp extraction supports v1 (100-ns ticks truncated
to microseconds) and v7 (Unix milliseconds), never v6; other versions and
unsupported variants return typed NULL. Timestamp metadata is TIMESTAMPTZ.
Shared scenarios cover direct/prepared/held binds, malformed-input recovery,
full independent English diagnostics, relation authorization, raw metadata,
generated/default/check/view/index recipes and native replacement controls.

```sh
make check RUN='^TestReferenceUUIDExtract(Values|Versions|Timestamps|Metadata|Binds)$' REFERENCE=1
make check RUN='^TestReferenceUUIDExtract(Diagnostics|Authorization|Cancellation)$' REFERENCE=1
make check RUN='^TestReferenceUUIDExtract(Stored|Controls|FeatureSmoke)$' REFERENCE=1
```

Run identical selectors without `REFERENCE=1` for native proof, with an external
900-second deadline for every invocation. Feature smoke is selected with
`make check RUN='^TestReferenceUUIDExtractFeatureSmoke$'` (also `REFERENCE=1`).
It expects four independently specified source rows, correct held-bind recovery,
timestamp boundary vectors, version/variant outcomes, denied reader access and
stored recipe round trips. Native-only phases exercise Dump/Reset/Restore and
actual emitted-SQL replay on PostgreSQL; they are not PostgreSQL control APIs.

### Range and multirange operator selected proof

Untagged shared scenarios cover all six concrete range families and the three
integer/numeric multiranges. They independently assert containment/overlap,
position/adjacency, comparisons, set outputs, matching cross operands,
empty/NULL behavior, exact metadata and complete diagnostics before parity.
The signature ledger is [PostgreSQL18 Tables9.58–9.59](https://www.postgresql.org/docs/18/functions-range.html)
plus scalar comparisons; cross range/multirange predicates are supported, but
set/comparison operands
must share one physical family. Temporal multiranges remain lookup-only: the
boundary scenario asserts real upstream success versus native's absent codecs,
not full temporal-multirange execution or a PostgreSQL missing-operator claim.

Actual definition inventory and bounded selectors (run each in native mode and
again with `REFERENCE=1`, using an external900-second deadline):

```sh
make check RUN='^TestReference(RangeOperatorPredicates|MultirangeOperatorPredicates)$'
make check RUN='^TestReference(RangeOperatorComparisons|TimestampRangeComparisons|TimestamptzRangeComparisons|MultirangeComparisons)$'
make check RUN='^TestReference(RangeOperatorSets|MultirangeOperatorSets)$'
make check RUN='^TestReferenceRangeOperator(Diagnostics|Resolution)$'
make check RUN='^TestReferenceRangeOperator(Composition|Binds)$'
make check RUN='^TestReferenceRangeOperator(Controls|Boundaries)$'
make check RUN='^TestReferenceRangeOperatorFeatureSmoke$'
make check RUN='^TestReferenceRangeOperator(DeparseGrouping|IndexDeparse)$'
make check RUN='^TestRangeOperator'
make check RUN='^TestMultirangeOperator'
make check RUN='^TestReferenceRangeOperator(Binds|Controls)$' RACE=1
```

The first eight selectors cover all17 new/changed/migrated public definitions:
the original seven groups plus one group for the two catalog-deparse definitions.
No oracle-discovery filters remain. The three positive comparison definitions
replace the obsolete timestamp/timestamptz/multirange comparison boundaries.
Internal units supplement public proof with endpoint ties, semantic equality,
exact authority, allocation assertions, immutable inputs/results, bounded scan
cancellation, carrier evaluation/retagging and parser precedence/positions.
Final changed definitions require both native and real pinned PostgreSQL18.0
proof; initial oracle discovery and readiness do not replace that evidence.
Reserve `make quickcheck` for whole-project Docker-free verification.

`TestReferenceRangeOperatorDeparseGrouping` asserts exact compact/pretty
`pg_get_expr` output for `(a + b) * c`, `a - (b - c)`, nested predicates and
comparisons, matching multirange expressions and qualified `OPERATOR(pg_catalog...)`
input. It executes the actual emitted SQL against independently specified
range/multirange/BOOL results, so lost grouping cannot pass as presentation-only
differences. Visible built-in qualified operators normalize to their ordinary
spelling under the pinned PostgreSQL contract.

`TestReferenceRangeOperatorIndexDeparse` uses the existing BOOL expression-index
provider for `a @> b`. Exact full/key definitions distinguish an infix expression
from a function expression and retain the required wrappers in both compact and
pretty output. Actual emitted keys are evaluated and full index DDL is replayed.
This does not admit range-valued ordering or index keys.

`TestReferenceMultirangeOperatorPredicates` retains false/empty/NULL vectors and
adds independent positive `r << m`, `m >> n`, `m >> r` and `m &> n` outcomes for
each of int4multirange, int8multirange and nummultirange; these signatures cannot
pass with an always-false provider.

Feature smoke argv:

```sh
make check RUN='^TestReferenceRangeOperatorFeatureSmoke$'
make check RUN='^TestReferenceRangeOperatorFeatureSmoke$' REFERENCE=1
```

Expected outcomes include six-range/three-multirange predicates and set results,
actual held string/bytes/nil reuse, disconnected range union/difference22000
versus successful multirange analogues, complete42883 diagnostics, and real
CHECK/generated/view consumers. Native phases perform same/fresh Driver
Dump/Reset/Restore, deterministic redumps, failed staged replacement and actual
emitted-SQL replay on PostgreSQL when enabled. Populated nested-domain readers
retain rows/metadata; a held no-parameter BOOL predicate reanalyzes across
same-shape domain remint with true/false/NULL rows. TEXT-shaped replacement
produces exact42883 at held/direct execution and fresh Prepare, while a fresh
TEXT projection proves replacement. Native control APIs have no PostgreSQL API
equivalent; no domain parameter/result replacement fence is widened.

### Range inspection selected proof

The shared untagged `TestReferenceRangeInspect` definitions cover the six
executable ranges and three integer/numeric multiranges: canonical bounds,
empty/omitted/explicit infinity/NULL distinctions, outer normalized members,
gap-filling hulls, numeric endpoint scale, exact scalar/range metadata,
direct/prepared/contextual/held binds, independent complete diagnostics,
table/column/schema authorization, ordinary cancellation and staged controls.
Inputs use existing declared-column and INSERT-destination authority, not
unsupported explicit range casts. Unknown string `lower`/`upper` remain TEXT;
all selected range overloads are strict and immutable.
`TestReferenceRangeInspectDiagnosticOrdering` pins direct/prepared lower/upper
named-argument and scalar-decoration errors for both text and typed range
overloads. Ordinary argument and FILTER children are transformed before
count/label checks, namespace lookup and signature/decorator rejection.
Malformed FILTER and child diagnostics are not discarded or rebound; ordinary
TEXT literal/NULL/parameter preference remains unchanged.

```sh
make check RUN='^TestReferenceRangeInspect(Ranges|Multiranges|Merge|Metadata|Resolution|Diagnostics|DiagnosticOrdering|Binds|Authorization|Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceRangeInspect(Ranges|Multiranges|Merge|Metadata|Resolution|Diagnostics|DiagnosticOrdering|Binds|Authorization|Controls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestRangeInspect'
make check RUN='^TestReferenceRangeInspectFeatureSmoke$'
make check RUN='^TestReferenceRangeInspectFeatureSmoke$' REFERENCE=1
```

Use an external900-second deadline for selected checks; reserve `make quickcheck`
for whole-project Docker-free verification. Real mode uses the existing pinned
PostgreSQL18.0 fixture. Native-only control phases run real Dump/Reset/Restore
on same/fresh Drivers, hold old rows/metadata across successful/failed replacement,
and replay the actual emitted SQL on PostgreSQL when present.
Executable stored recipes cover CHECK/generated/views and nested inspectors.
Range DEFAULT expressions remain unavailable under existing cast/constructor
and default-column/parameter/subquery boundaries; no prerequisite is widened.
Leaf units cover exact generation authority, strict literal/runtime demand,
fresh retag/reconstruction, sealed members and allocation-free evaluation.
Computed call targets require unknown nullability; selected stored view,
generated and INSERT-SELECT destination columns independently require native
known nullable metadata. Pinned pq keeps unknown nullability in both cases.
These explicit source contracts apply during live reads and actual dump replay.
Feature smoke completes cancellation and authorization on their actual owned
relations before native control replacement. The ACL fixture is dropped before
Dump; shape-changing Restore and retained-reader assertions use the control
helper's separate target Driver, not an assumed relation in the source Driver.

### query_int selected proof

The seven untagged `TestReferenceQueryInt` definitions share public SQL/API
fixtures across native and pinned PostgreSQL. They independently assert
installation and generated identity relationships, canonical integer Boolean
query text, direct/prepared/held values, owned byte bindings, nil and typed-nil
NULLs, empty-byte failures, explicit character I/O, directional query_int-to-
TEXT/VARCHAR assignment (VALUES/SELECT/UPDATE) and rejected typed character
assignment into query_int, full diagnostics/stages/locations, raw metadata,
and statement atomicity.
Controls include pending readers/metadata, committed-only dumps, atomic
same/fresh-driver restore, SQL extension recreation and actual emitted-SQL
oracle replay. Native controls are explicit native-only subphases.

Run each selection from the repository root with an external900-second deadline:

```sh
make check RUN='^TestReferenceQueryInt(Installation|Grammar|Assignment|Diagnostics|Metadata|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceQueryInt(Installation|Grammar|Assignment|Diagnostics|Metadata|Controls|FeatureSmoke)$'
make check RUN='^(TestQueryInt(Input|Output|Identity|Coercion|Cancellation)|TestIntarrayProvider(Identity|Conflicts|Controls))$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RACE=1 RUN='^(TestQueryIntCancellation|TestIntarrayProviderControls|TestReferenceQueryInt(Assignment|Controls|Metadata))$'
```

The existing availability witness now asserts both actual native registered
providers (intarray and unaccent), not PostgreSQL's larger package inventory.
The genuine LANGUAGE internal/cstring helper conflict remains a PG native-ABI
witness; native provider units retain strict reciprocal membership and prove
published type conflicts, invalid-manifest rejection, nonaliasing remapping
and actual TEMP validation/retirement. No arbitrary SQL loader is introduced.
The declaration string-modifier syntax boundary is documented in
[DIALECT](../../docs/DIALECT.md#query_int-extension-type), distinct from query
text diagnostics. Flat OR15/16/17/32 succeeds;16 NOTs succeeds and17 fails.
Generated OIDs are checked relationally, not pinned to another allocator.

The standalone public executable is transient session material
`local://query-int-smoke.go`, not a parent deliverable or committed helper.
The root materializes it outside the checkout and runs:

```sh
timeout 900s go run "$SESSION_TMP/query-int-smoke.go"
```

Expected quiet exit0 with no stdout/stderr. It exercises literal and held
bindings (including buffer ownership and both NULL forms), raw direct/prepared
metadata, complete atomic parameter diagnostics, same-type copies, directional
TEXT/VARCHAR assignment through direct/prepared VALUES/SELECT/UPDATE (including
typed NULL), extension dump ordering, retained readers, Reset/Restore,
independent destination restore, failed replacement and coexistence with
unaccent. A passing test or smoke is
evidence only when the root records its actual execution.

### ACL construction and expansion

Independent pinned PostgreSQL18.0 expectations cover all thirteen defaults,
fifteen privilege names/options, zero and missing numeric identities, strict
NULLs, exact signatures/English diagnostics, fixed four-field record metadata,
duplicate-preserving expansion, target/FROM/LATERAL/ROWS FROM/ordinality and
single-entry array/item containment. Native controls exercise real emitted SQL,
rank-one empty ACLs inside recursively nested domains/composites, stored
recipes, retained rows, atomic Restore and pre-writer rejection of zero/stale
grantors and arrays of composite carriers containing empty ACLs. Expansion
also checks both argument orders of rank-zero/rank-one empty concatenation.
Component units cancel validation, containment and zero-rights expansion after
scanning begins, then exercise recovery without timing-dependent SQL admission.

Run each selected group in native and real pinned modes after changes:

```sh
make check RUN='^TestACLValues(Defaults|Privileges|ZeroIdentity|EmptyArray|Containment|Invocation|StoredRecipe|ScanCancellation)$'
make check RUN='^TestACLItemConstructor$'
make check RUN='^TestReferenceACLValues(Defaults|Construction|Expansion)$'
make check REFERENCE=1 RUN='^TestReferenceACLValues(Defaults|Construction|Expansion)$'
make check RUN='^TestReferenceACLValues(Operators|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceACLValues(Operators|Diagnostics)$'
make check RUN='^TestReferenceACLValues(Controls|Lifecycle|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceACLValues(Controls|Lifecycle|Smoke)$'
make check RACE=1 RUN='^TestReferenceACLValues(Construction|Controls|Lifecycle)$'
make check RUN='^TestReference(ArrayRelationalUnnest(Storage|Zip)|JSONRecordComposition)$'
make check REFERENCE=1 RUN='^TestReference(ArrayRelationalUnnest(Storage|Zip)|JSONRecordComposition)$'
make check RUN='^TestReferenceACLValuesSmoke$'
make check REFERENCE=1 RUN='^TestReferenceACLValuesSmoke$'
```

The smoke checks eight owner relation privileges with false grant flags,
construction/output/equality/containment, strict empty record metadata,
documented failures and actual native rank-preserving replay. No successful
test exit is claimed here; root verification records actual observations.

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

### Domain ALTER CHECK selected proof

The `TestReferenceDomainAlterConstraint` definitions cover Lifecycle,
Diagnostics, TargetOnly, Dependencies, Containers, RepeatedValidate, Metadata,
Prepared, Atomicity, NativeControls, Boundaries and Messages. They share one SQL/assertion
body per scenario across the ordinary fixture backends. Native control phases
use public Dump/Reset/Restore, including a fresh Driver, retained eager results,
failed/canceled replacement and actual emitted-SQL replay on pinned PostgreSQL.

All twelve shared scenarios now use both backends without discovery filters.
Root's initial Messages discovery against pinned PostgreSQL18.0 completed before
removing its sole callback gate. The development source tree is not a release
oracle. Catalog predicates use explicit OID casts rather than requiring
additional OID/regtype operators. Rollback absence uses direct typed-cast and
relation lookup diagnostics rather than requiring to_regtype or to_regclass.
ALTER accepts and ignores NO INHERIT (`connoinherit=false`); the separate
CREATE DOMAIN rejection remains covered by the existing CHECK scenarios.

Run every changed definition in both modes after changes. Use an external
900-second deadline for each command:

```sh
make check RUN='^TestReferenceDomainAlterConstraint(Lifecycle|Diagnostics|TargetOnly)$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraint(Lifecycle|Diagnostics|TargetOnly)$'
make check RUN='^TestReferenceDomainAlterConstraint(Dependencies|Containers|RepeatedValidate)$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraint(Dependencies|Containers|RepeatedValidate)$'
make check RUN='^TestReferenceDomainAlterConstraint(Metadata|Prepared|Boundaries)$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraint(Metadata|Prepared|Boundaries)$'
make check RUN='^TestReferenceDomainAlterConstraint(Atomicity|NativeControls)$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraint(Atomicity|NativeControls)$'
make check RUN='^TestReferenceDomainAlterConstraintMessages$' REFERENCE=1
make check RUN='^TestReferenceDomainAlterConstraintMessages$'
make check RUN='^TestReferenceDomainAlterConstraint(Atomicity|Prepared|Messages)$' RACE=1
make check RUN='^TestDomainAlterConstraint(Grammar|SelectedEvaluation|Discovery|Generations|Publication|ReplayEligibility|MessagePublication)$'
make check RUN='^TestDomainAlterConstraint(Discovery|Generations|Publication|MessagePublication)$' RACE=1
make check RUN='^TestReferenceCompositeAlterDomainChecks$' REFERENCE=1
make check RUN='^TestReferenceCompositeAlterDomainChecks$'
```

Lifecycle smoke must observe old -1 surviving NOT VALID, a false flag and new
-2 rejection, failed validation preserving false, repair followed by the same
CHECK OID becoming true, and selective DROP retaining the independent upper
bound. RepeatedValidate specifically requires18.0's already-valid CHECK to
rescan and reject an empty array holder without clearing its true flag.
NativeControls smoke must preserve a stored violator, false flags (even for a
satisfying value), current enforcement, owners and deterministic redump.
These selected smoke scenarios supplement the root's separate throwaway actual
API exercise after implementation; they do not replace it.
Messages additionally exercises private CREATE/derived domains, preceding own
inserts/updates/deletes, current NOT VALID enforcement, repeated alterations,
later error/cancel rollback, subsequent own container changes, BEGIN promotion
and savepoint recovery, real multiple Query result sets and index outputs.
For partition carriers, native UPDATE excludes even directly named attached
members. The shared repair detaches, updates and reattaches the same physical
child with its original bound; stable identity and root-visible data prove that
the child is attached before each historical certification attempt.
NativeControls proves restored domain ownership by ordinary-role ALTER
admission on the fresh restored clone and PostgreSQL replay, with real schema
USAGE granted only after the clone's deterministic redump. Same-driver redump
also compares emitted owner/state declarations exactly. No source ownership
change, permission bypass or namespace-ACL serialization extension is used.
The cross-owner source deliberately has no schema USAGE grant for its domain
owner. Actual emitted post-data NOT VALID additions use `postgres` replay
authority, while domain ownership survives same/fresh Restore and PostgreSQL SQL
replay; this does not relax ordinary ALTER admission.
Cross-owned CREATE DOMAIN also runs as bootstrap, immediately followed by the
qualified OWNER command restoring its captured source owner. Restore eligibility
requires the exact fresh domain and immediate captured-owner/creator sequence,
not arbitrary ALTER OWNER. ReplayEligibility includes adversarial ordering,
identity, kind and target tests. PostgreSQL replay registers schema/role teardown
before execution so assertion failures cannot contaminate the next scenario.

Container discovery follows PostgreSQL's stored-container rejection, including
empty/NULL storage; it does not recursively certify nested old values. Native
domain utilities remain autocommit-only: Prepare is quiet inside an explicit
block, execution rejects without publication, whereas PostgreSQL's transactional
ALTER is legal and rolls back. PostgreSQL cancellation uses57014; native context
cancellation remains an ordinary Go error. External archive/WAL/physical-server
mechanisms are not native control APIs.

### Domain CHECK selected proof

Run each small group first against pinned PostgreSQL with `REFERENCE=1`, then
in native mode, with an external 900-second deadline per invocation:

```sh
make check RUN='^TestReferenceDomainCheck(Truth|NamesOrder)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(Truth|NamesOrder)$'
make check RUN='^TestReferenceDomainCheckDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDiagnostics$'
make check RUN='^TestReferenceDomainCheck(ConversionsNull|Ancestors)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(ConversionsNull|Ancestors)$'
make check RUN='^TestReferenceDomainCheck(InputRoutes|Metadata|NativeControls)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(InputRoutes|Metadata|NativeControls)$'
make check RUN='^TestReferenceDomainCheckInputOrdering$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckInputOrdering$'
make check RUN='^TestReferenceDomainCheckSQLExecute$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckSQLExecute$'
make check RUN='^TestReferenceDomainCheckDemand$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDemand$'
make check RUN='^TestReferenceDomainCheckDemandWrappers$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckDemandWrappers$'
make check RUN='^TestReferenceDomainCheck(PlanningDemand|RecipeInitialization)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(PlanningDemand|RecipeInitialization)$'
make check RUN='^TestReferenceDomainCheck(ConflictProofDemand|AddColumnDemand|StoredDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheck(ConflictProofDemand|AddColumnDemand|StoredDemand)$'
make check RUN='^TestReferenceDomainCheckIndex(RuntimeDemand|LifecycleDemand|FailureDemand|ConflictDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndex(RuntimeDemand|LifecycleDemand|FailureDemand|ConflictDemand)$'
make check RUN='^TestReferenceDomainCheckIndex(ProducerDemand|ProducerFailureDemand|ReturningProducerDemand|MutationProducerDemand|MergeProducerDemand|CopyProducerDemand|CopyInsertionOrderDemand|InsertionOrderDemand)$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndex(ProducerDemand|ProducerFailureDemand|ReturningProducerDemand|MutationProducerDemand|MergeProducerDemand|CopyProducerDemand|CopyInsertionOrderDemand|InsertionOrderDemand)$'
make check RUN='^TestReferenceDomainCheckIndexCopyFlushDemand$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndexCopyFlushDemand$'
make check RUN='^TestReferenceDomainDiagnostics$' REFERENCE=1
make check RUN='^TestReferenceDomainDiagnostics$'
```

The scenarios assert named/generated identities, three-valued CHECK truth,
ancestor-first and per-domain alphabetical order, declaration diagnostics,
conversion versus already-domain NULL bypass, atomic assignments, COPY and
nested input, direct/prepared base metadata, and CHECK-owned dependency
retirement. Native-only control phases additionally exercise Dump/Reset/Restore,
fresh-driver remapping, retained eager rows, and failed/canceled replay atomicity.
These definitions are contracts, not passing-verification claims.
Demand separates domain expression initialization from truth consumption:
invalid scalar casts behind false filters/empty sources remain unconsumed,
empty omitted-default INSERT affects no rows, and each consumed row executes
its volatile CHECK. Real SQL sequence rows/state and late-error atomicity are
the witnesses, not a mocked evaluator. Constant CHECK recipe errors still
initialize at Execute for an empty consumer; inferred domain input remains
immediate while explicit-base parameters and already-domain NULL preserve their
separate conversion/bypass contracts. The private supplement is selected by
`make check RUN='^TestDomainCheckPreflightDemandInternal$'`.
DemandWrappers exercises public direct/prepared abs, COALESCE, CASE and ANY
consumers behind false filters and empty sources, plus demanded failure/success
and already-domain NULL bypass. The shared conditional constant eligibility
used by these providers must preserve live checked conversions and enclosing
runtime barriers; a literal physical child is not a completed CHECK event.
PlanningDemand covers empty keyed FULL JOIN/MERGE scalar, array and conditional
casts without truth consumption, real FULL JOIN sequence demand, and MERGE's
separate ordinary BY SOURCE action-stage ON evaluation. After the earlier
observable nextval1, one FULL JOIN match ends at nextval3, while a matched
BY SOURCE MERGE ends at nextval4 and deletes its target. MERGE command counts
retain the existing lib/pq adapter contract (PG0/native actual1).
RecipeInitialization requires retained CASE/COALESCE/ancestor/array and stored
default recipes to report22012 at Execute even for false/empty consumers; removed
dead arms return1 without initializing their eliminated conversion.
ConflictProofDemand proves empty partial-index inference consumes no truth,
while an actual inserted index row fails23514 atomically.
AddColumnDemand proves fresh omitted NULL and invalid defaults are checked only
on actual old rows, failed schema/data publication permits same-column retry,
three checked rows draw three sequence values, an empty ADD draws none, and
retained recipe1/0 still initializes with22012 on an empty table.
StoredDemand checks generated and RANGE-partition initialization and actual input:
retained division-by-zero recipes reject CREATE with22012, publish no partial
table, and permit a same-name valid retry. IndexRuntimeDemand requires empty CREATE
to consume no truth, one INSERT effect and no COMMIT replay. IndexLifecycleDemand
covers B-tree/hash predicate/key and existing unique-predicate INSERT/UPDATE/DELETE,
duplicate23505 and rollback with retained sequence effects. IndexFailureDemand
requires failed UPDATE to draw only its genuine new-value effect before23514,
preserve old index/row data after rollback, and permit a later valid update.
IndexConflictDemand preserves distinct accepted ON CONFLICT precheck/insertion
events and duplicate existing-key rechecks: after initial sequence1, ordinary
INSERT ends at2, accepted ON CONFLICT at4, and duplicate DO NOTHING at5 for
predicate-only or6 for key/zero-width witnesses. COMMIT and zero-width DELETE
do not replay these effects. All SQL expectations are strict direct/prepared
dual-mode definitions; they are not proof that the current implementation passes.
HASH partial-predicate and UNIQUE expression-key/zero-width behaviors remain
PostgreSQL witnesses: the same definitions independently assert native0A000
with exact existing diagnostics and no partial row/effect publication. Native
artifact behavior uses admitted HASH derived nonpartial keys and UNIQUE simple
keys with a predicate; these definitions do not expand index providers.
ProducerDemand and ProducerFailureDemand distinguish actual per-row VALUES/query
insertion from later batch publication, with and without RETURNING. Mutation and
MERGE companions retain genuine new-version events and first-failure atomicity.
CopyProducerDemand independently retains PG18's eligible nextval-default COPY
flush phase: buffered defaults precede insertion checks; this is not the ordinary
INSERT producer schedule. InsertionOrderDemand uses reverse lexical index names
to prove creation/ObjectID order, exact first domain/constraint identity and
pre-error sequence effects. These strict expectations require pinned oracle and
native proof; equal final event counts alone do not establish correct ordering.
ReturningProducerDemand also makes RETURNING consume nextval, distinguishing
source/index/RETURNING events before the next source: stored IDs1,4 and returned
(1,3),(4,6), ending at6. CopyInsertionOrderDemand rejects tuple2's first index
after buffered defaults1,2 and tuple1's ordered checks3,4; exact curr5 and line2
prove row-outer/index-inner flush order and actual tuple diagnostic provenance.
CopyFlushDemand distinguishes the finite COPY tuple and server raw text-byte
boundaries: binary tuple1001 stores ID2001 after the preceding1000 checks, while
the third32768-byte text row stores ID5 after the preceding two checks. These
source-grounded strict traces also require their selected pinned/native proofs.
Native `TestDomainCheckIndexSnapshot` supplements exercise produced B-tree/hash
entry transport, selected live identities/positions, unchanged CHECK sequence
state, source immutability and missing/malformed/canceled snapshot rejection.

`TestReferenceDomainCheckIndexAliasSnapshotCollision` shares ordinary SQL for
extension-owned unaccent dictionaries and real `unaccent(text)` routine aliases
(REGPROC and REGPROCEDURE). Domain-wrapped keys and NULLs exercise a UNIQUE
B-tree and a separate HASH-only table, including direct/prepared equality,
UPDATE/DELETE/reinsertion and complete duplicate diagnostics. Domain-wrapped
oid_ops Detail contains the independently captured numeric represented identity,
not its symbolic spelling or a pinned allocator value. Its explicit
native control phase retains a TEMP sequence at the observed represented source
OID, restores the actual dump into another Driver and requires a reminted
dictionary/procedure identity while the retained sequence keeps its original
identity and state. The same SQL checks must still hold after replacement;
CHECK sequence frontier and byte-identical source dump guard against replay or
source mutation. Recursive private units distinguish equal numeric identities
in procedure/dictionary/unrelated catalog classes, nested domains, arrays,
composites, bounds and NULLs without adding compound index providers.
The key-output supplement requires physical OID diagnostics without changing
logical alias/domain keys, including the provider's unsigned INT4 bit semantics.

```sh
make check RUN='^TestReferenceDomainCheckIndexAliasSnapshotCollision$' REFERENCE=1
make check RUN='^TestReferenceDomainCheckIndexAliasSnapshotCollision$'
make check RUN='^TestDomainCheckIndexSnapshotAlias(Recursive|KeyOutput)$'
```



Private package supplements use
`make check RUN='^TestDomainCheck'` and
`make check RUN='^(TestDomain(ExcludedClausesNeverPublishInternal|BaseUsageAndOwnerAuthorizationInternal)|TestArrayCatalogAssociationPublicationAndOrigin|TestEnumCatalogStagingAndAuthority)$'`.
They exercise actual catalog staging, frame invariants, recipe/ancestry remapping,
fresh-input versus exact-identity coercion, nested codec completion, typmod
precedence and the allocation-free unconstrained path. They do not replace
selected public SQL proof in both modes. The Diagnostics group also checks
direct/prepared modifier failure stages, not just SQLSTATE.
InputOrdering checks lexical codec/CHECK precedence across multiple parameters
and array/composite members, including NULL. Member literal input fails at the
actual Prepare boundary, whereas inferred parameters and an outer domain
conversion fail at Execute. The literal decoder releases only its owning
binder's mutex around the captured codec/evaluator and revalidates the admitted
provider graph before resuming; it does not replay CHECKs or resolve new names.
SQL EXECUTE actual expressions enter and leave their frame importer unlocked.
The importer owns its narrow binding/capture lock, evaluates in the current
statement scope outside that lock and validates captured providers before
returning; failed CHECKs cannot strand the mutex or poison a later frame.
Former CHECK capability exclusions now exercise shared datetime extraction,
regexp conversion, binary COPY, foreign actions and composite member consumers:

```sh
make check RUN='^TestReference(DatetimeExtractDomainConstraintBoundary|RegexpStoredDefinitions|ForeignActionsConstraintsDomain|CompositeConstrainedDomainBoundary)$' REFERENCE=1
make check RUN='^TestReference(DatetimeExtractDomainConstraintBoundary|RegexpStoredDefinitions|ForeignActionsConstraintsDomain|CompositeConstrainedDomainBoundary)$'
make check RUN='^TestReferenceCopyBinaryCodecsNested$' REFERENCE=1
make check RUN='^TestReferenceCopyBinaryCodecsNested$'
make check RUN='^TestPreparedSQLActualNoSnapshotDemand$'
```


Feature smoke uses `make check RUN='^TestReferenceDomainCheck(Truth|ConversionsNull|Ancestors)$'`
and `make check RUN='^TestReferenceDomainCheckNativeControls$'`. Expected
observations include positive 1 accepted, 0 rejected with exact 23514 constraint
identity, UNKNOWN admitted, fresh NULL rejected by `VALUE IS NOT NULL` while
already-domain subquery NULL bypasses conversion, unchanged rows after failed
assignment, and identical post-roundtrip constraint behavior. A CHECK-only
provider cascade preserves the domain/data and removes only the dependent check.
Whole-project Docker-free verification remains `make quickcheck`.

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
`TestReferenceMultirangeDomains` also creates NOT NULL domains over each concrete
multirange in both backends; column NOT NULL diagnostics and failed multirow
statement atomicity retain their independent expectations.
Oracle discovery filters are removed before native-red; they are not a final
capability distinction. Native process-local control subphases additionally
execute the actual emitted SQL on pinned PostgreSQL18.0.
Pinned lib/pq exposes blank multirange type names (not uppercase range names);
the helper retains logical family identity for raw/metadata checks, and public
pg_type/pg_range witnesses independently verify released OIDs/companion links.
Scale-boundary unions follow released strict selection rather than a containment
shortcut; fully comparator-equal different-scale duplicates have no stable tie
ordering promise.
`TestReferenceMultirangeComparisons` uses all three companions and plain/nested
domains with unequal ascending/reverse, equal, empty and NULL operands through
direct/prepared SELECT. Independent positive matrices cover all six scalar
comparisons, contextual unknowns, string/bytes/nil binds in both directions,
zero-row/all-NULL metadata and WHERE equality. The additional operator comparison
matrix covers lexicographic member order and shorter equal prefixes, defeating
hull-only or constant-zero comparison. SQL ordering/key providers remain separate.

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
make check REFERENCE=1 RUN='^TestReferenceMultirangeComparisons$'
make check RUN='^TestReferenceMultirangeComparisons$'
make check RUN='^TestMultirange'
make check RUN='^TestComparison'
make check RUN='^TestAnyValueProviderDatums$'
make check RACE=1 RUN='^TestReferenceMultirange(Binds|Controls|DomainControls|Comparisons)$'
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

### Domain defaults and nullability

The shared CREATE DOMAIN default/nullability contract is organized as D1–D9:

- D1 `TestReferenceDomainDefaultPrecedence`: column/domain/base precedence,
  explicit NULL masks, typmods, direct/prepared and held insert reuse.
- D2 `TestReferenceDomainNullability`: fresh conversions, named and inherited
  NOT NULL, error identity/order, atomic writes and already-domain NULL bypass.
- D3 `TestReferenceDomainDefaultDiagnostics`: variable-free default restrictions,
  conflicting declarations, complete diagnostics/stages and failed-DDL
  atomicity, including constrained immediate-base cooking.
- D4 `TestReferenceDomainDefaultCallers`: INSERT SELECT, UPDATE DEFAULT, MERGE,
  view writes, COPY omission/default markers and ADD COLUMN row demand.
- D5 `TestReferenceDomainDefaultBuiltinInheritance`: predefined
  `information_schema.time_stamp` inheritance compared with transaction time.
- D6 `TestReferenceDomainDefaultMetadata` and
  `TestReferenceDomainDefaultDependencies`: base driver descriptors, own versus
  inherited flags, default/tree metadata and provider drop closure.
  `TestReferenceDomainDefaultNullMetadata` distinguishes the elided final bare
  builtin Const NULL from retained inherited-domain and numeric-typmod NULL
  recipes, with exact text/tree presence and omitted values on both routes.
- D7 `TestReferenceDomainDefaultNativeControls`: shared SQL witnesses and explicit
  native-only same/fresh Driver Dump/Reset/Restore with newly defaulted inserts,
  enforcement, provider/owner reownership and atomic failed replacement.
- D8 `TestReferenceDomainDefaultNullReplay`: independently specified public
  already-domain NULL values in scalar/domain/array/composite carriers, exact
  multidimensional/nested bounds, a materialized snapshot and fresh-input errors.
  Native-only same/fresh replay, new omitted writes and malformed-record atomic
  preservation exercise the host interface without granting ordinary SQL a bypass.
- D9 `TestReferenceDomainDefaultCompositeFieldDependencies`: own/inherited selected
  composite fields, actual stable-attribute/type edges, complete RESTRICT and
  ordered CASCADE diagnostics, atomic failed DDL, retained data and successful
  sibling defaults after dropping an earlier attribute. Explicit native-only
  same/fresh replay preserves those results and new omissions; source deparse
  retains `ROW(17, 18)` while restored deparse normalizes to `ROW(18)`.
  `TestReferenceDomainDefaultStoredDeparseVisibility` separately fixes the released
  creation-time text contract: visible public names versus qualified hidden
  names, unchanged stored text after path changes/provider renames, and exact
  inherited copies even when the child's creation path differs.
  The type-renaming control succeeds on PostgreSQL; native execution asserts its
  existing exact42601 TO-token boundary on direct/prepared routes instead of
  introducing adjacent utility syntax. Path and inherited-text phases are shared.

Selected root proof/smoke commands (external 900-second deadlines):

```sh
make check RUN='^TestReferenceDomain(DefaultPrecedence|Nullability)$' REFERENCE=1
make check RUN='^TestReferenceDomainDefault(Diagnostics|Callers)$' REFERENCE=1
make check RUN='^TestReferenceDomainDefault(BuiltinInheritance|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceDomainDefault(Dependencies|NativeControls)$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultNullReplay$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultCompositeFieldDependencies$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultStoredDeparseVisibility$' REFERENCE=1
make check RUN='^TestReferenceDomainDefaultNullMetadata$' REFERENCE=1
make check RUN='^TestReferenceDomainDiagnostics$' REFERENCE=1
```
The package invariant selector is
`make check RUN='^TestDomainDefaultCompositeFieldDependenciesInternal$'`.
It covers authentic attribute references, immutable predecessor snapshots,
repeated shape migration, selected-child demand, full-row live projection and
rejection of forged/foreign retained associations.
`make check RUN='^TestDomainDefault(ViewSQLDependencies|DependencyPreparedProjection)Internal$'`
additionally distinguishes passive column datatype captures from explicit SQL
type edges, preserving full lifetime manifests. Actual prepared CASCADE effects
must omit the retired rowtype dependency without reviving its opaque numeric
alias, retain the genuine surviving alias and reject a removed bound column;
the captured predecessor still retains its original dependency.
`make check RUN='^TestDomainDefault(BareNull|LogicalSelection|Recipes)Internal$'`
covers exact NULL normalization, retained conversion/CASE nodes, inherited mask
selection, stored-text coherence and unchanged fresh NOT NULL enforcement.
`make check RUN='^TestGeneratedCompositeAttributeProviders$'` covers existing stored
generation consumers of named composite fields. Capture and validation share the
same live stable-attribute/backing-rowtype checks; exact manifests, epochs and
same-row data authority remain required. Missing/foreign/forged providers and
unrelated live metadata cannot authorize another table's data expression.


Repeat each selector without `REFERENCE=1` for native proof. Both modes use the
same definitions with explicit expected rows, metadata, diagnostics and stages.
Existing domain diagnostics migrate DEFAULT/NOT NULL to shared positive
assertions; ALTER default and native utility transaction boundaries remain
explicit. Native controls are not PostgreSQL pg_dump, physical or wire
interoperability.

User-domain `typdefaultbin` uses the existing native `boundTree` representation:
typed Const/Coerce nodes, not PostgreSQL nodeToString. D6 asserts native concrete
root kinds separately from released PostgreSQL CONST/FUNCEXPR/COERCETODOMAIN
predicates; neither accepts the other's layout. Deparse, presence and own flags
remain exact. D7 additionally distinguishes an unchanged inherited recipe's
target-domain error from an explicit default's immediate-base conversion error
before and after native restore.
Catalog name assertions use the actual `NAME` descriptor rather than expanding
the separate explicit NAME-to-TEXT cast boundary. Inherited default dependencies
retain their direct provider edge even when traversal reaches the child through
its parent first. Native owner privilege checks apply their ordinary schema
USAGE grant after the dump/replay phase; explicit namespace ACL serialization is
not part of this contract.

Native captured NULL provenance validates the live domain identity even when
CHECK declarations are still deferred until after data replay. Closed replay
expressions retain their original binding's routine lifetime manifest, including
materialized snapshots; a matching routine address alone never grants authority.

Domain/default VALUES rows reuse the existing destination-attribute projection:
single-row input checks an earlier supplied domain value before demanding a later
omitted provider default. Multirow input retains the existing source-first
schedule. The same rule applies after logical-to-physical view mapping.
NativeControls asserts the unchanged sequence state in the shared SQL phase,
before Dump. Named DEFAULT/NULL/NOT NULL declaration diagnostics separately retain
the CONSTRAINT token's UTF8 source position without replacing clause positions.

Selected existing persistence regressions (both modes, same expectations):

```sh
make check RUN='^(TestReferencePGAggregate(IdentityPersistence|FeatureSmoke)|TestReferenceRegProcMaterializedPersistence|TestReferenceMaterializedViewTemporalSnapshots|TestReferenceDomainAlterConstraintNativeControls)$' REFERENCE=1
make check RUN='^(TestReferencePGAggregate(IdentityPersistence|FeatureSmoke)|TestReferenceRegProcMaterializedPersistence|TestReferenceMaterializedViewTemporalSnapshots|TestReferenceDomainAlterConstraintNativeControls)$'
```


Internal descriptor/binder/remap supplements:

```sh
make check RUN='^(TestDomainNullabilityAuthority|TestDomainDefault(RecipesInternal|LogicalSelectionInternal|RemapInternal|DependencyReasonInternal|ValuesSchedulingInternal|ReplayAdmissionInternal|ReplayPairingInternal|ReplayDeferredChecksInternal|ReplayRoutineManifestInternal)|TestDomainDeclarationClausesAtomicInternal|TestDomainNamedDeclarationPositions|TestDomainCheck(InputVersusIdentity|NestedInputAndTypmod)|TestInfoTimeStampCatalogRemap|TestPGTypeInstalledDomainAuthority)$'
```

The shared projection's existing identity demand guards remain selected:

```sh
make check RUN='^TestIdentity(EarlyRowFailureConstrainsCompleteProjection|DiscardedRuntimeConversionDemandBinding)$'
```

The internal line-comment callback now carries `(string, parser.Position)` for
immutable source pairing; no public parser mode or compatibility hook was added.
Selected callback consumer regressions (and their `RACE=1` counterparts):

```sh
make check RUN='^Test(MaterializedViewRestoreComments|LineCommentPositions)$'
make check RUN='^TestParseScratch(OptionsReentryAndCancellation|TypeErrorsAndCallbackPanic|FullReset)$'
make check RUN='^TestParseScratch(RetainedDeclarationCarriers|RetainedCollationSource|RetainedParametersAndPositions|ConcurrentCancellation)$'
make check RUN='^Test(PgcryptoOwnerMetadata|DaitchMokotoffProviderIdentity|IntarrayProviderIdentity)$'
```

Selected race proof uses the same command with `RACE=1`. These private units do
not replace the selected native and pinned reference definitions above.

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
Inherited-domain defaults execute through the same cooked recipe selection,
including transaction `CURRENT_TIMESTAMP(2)` and explicit column NULL masks.
`TestReferenceInfoTimeStampIdentity` checks omitted values on direct and prepared
inserts against SQL `CURRENT_TIMESTAMP(2)` captured in the same transaction, with
exact raw timestamp and base metadata assertions rather than wall-clock tolerance.
Only the general information-schema column/domain projections remain PostgreSQL-only.
Sibling-domain/implicit-array executability and general information-schema views
retain their independent exclusions. The bounded pg_type source preserves the
seeded released tree and genuine type relationships; lookup-only codec visibility
does not enable input or storage.

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
No discovery filters remain. Datemultirange, range arrays and new temporal range
aggregates remain excluded; six-family scalar operators are covered above.
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


### TSRANGE oracle-first contract

The timestamp-range definitions specify continuous timestamp-without-time-zone
bounds: finite output is quoted, open adjacent-microsecond bounds stay nonempty,
and explicit infinity/inclusivity remains distinct from omitted bounds, empty
and NULL. Independently specified expectations cover scalar endpoint rounding and
extrema, ignored-but-validated written zones, DateStyle, frozen prepared literals
versus fresh relative/ambiguous binds, raw builtin/plain/nested-domain metadata,
all logical error fields and stages, failed-handle behavior and late-tuple
atomicity. Named-composite projections and the deterministic one-nonNULL
ANY_VALUE carrier retain existing composition semantics.

Run these small selections with an external900-second deadline each:

```sh
make check RUN='^TestReferenceTimestampRange(Values|Metadata|Diagnostics|Modifiers|Environments|RelativeCapture)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Binds|DomainControls|Composition)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Values|Metadata|Diagnostics|Modifiers|Environments|RelativeCapture)$'
make check RUN='^TestReferenceTimestampRange(Binds|DomainControls|Composition)$'
make check RUN='^TestReferenceTimestampRange(Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceTimestampRange(Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceDateRange(Controls|DomainControls|Composition|FeatureSmoke)$'
make check RUN='^Test(TimestampRange|DateRangeCanonicalDatum|AnyValueProviderDatums)'
```

`Composition` also selects the binary COPY definition in `reference/dml`,
which checks handwritten network bytes, flags and timestamp microseconds using
the existing stream fixture. These are permanent shared scenarios, not native
goldens. Pinned18.0 discovery confirmed the value, diagnostic, stage, environment,
bind and composition expectations. Builtin/plain/nested-domain pq metadata uses
TSRANGE, interface{} ScanType and unavailable length/precision/nullability.
The empty held-result expectation uses an empty row slice, not nil; this corrects
the discovery harness without changing SQL expectations. All scenarios visit both
backends without discovery logging or filters. Full-record/stage mismatches
remain nonfatal within each diagnostic route, preserving complete diagnostics.
`Controls`, `DomainControls` and `FeatureSmoke` share `range_control_helpers_test.go`
with date ranges: actual same/fresh Dump/Reset/Restore, canonical ISO under a
nonISO session, actual emitted PostgreSQL SQL replay, byte-identical redump,
failed/canceled replacement and retained populated builtin/nested-domain readers
and metadata through a TEXT-shaped replacement. Native-only controls are explicit
subphases; ordinary SQL and independent assertions remain shared across modes.

The standalone public executable is session material
`local://tsrange-feature-smoke.go`, not a parent deliverable or committed wrapper.
Root materializes it as `./.tsrange-feature-smoke/main.go` and runs
`timeout 60s go run ./.tsrange-feature-smoke` from the worktree. Expected exit0,
stdout exactly `tsrange public smoke: PASS`, and no stderr. It checks new continuous
timestamp behavior, binds/metadata/capture/full atomic failures and nested-domain
controls with held readers, not merely startup. Remove the owned temporary source
after recording actual observations. No passing native proof is claimed here.

`TestReferenceTimestampRangeComparisons` independently asserts positive
Boolean/NULL results for all six comparisons, unequal/equal finite ranges and
empty ranges, builtin/plain/nested-domain columns, contextual strings and
NULL/binds in either operand position, zero-row/all-NULL metadata and a WHERE
equality predicate. Direct execution and Prepare share the same contract.
`TestReferenceRangeOperatorComparisons` additionally checks inclusion ties,
NUMERIC scale equality and all six concrete range families.

```sh
make check RUN='^TestReferenceTimestampRangeComparisons$' REFERENCE=1
make check RUN='^TestReferenceTimestampRangeComparisons$'
make check RUN='^TestRangeOperatorComparisonAdmission$'
```

The permanent range-operator feature smoke above exercises actual temporal
operators and comparison results. Native no longer treats timestamp scalar
comparison as a42883 capability boundary. Ordering/grouping/index providers,
temporal multirange codecs and general range casts remain excluded.


### TSTZRANGE oracle-first contract

`TestReferenceTimestamptzRange` scenarios independently assert continuous UTC
endpoint ordering, equal-instant singleton/empty normalization, inclusivity,
adjacent microseconds, infinity versus omission and NULL. They exercise direct
and held text/byte binds, raw TSTZRANGE metadata for builtin/plain/nested domains,
complete grammar/subtype diagnostics and eager failure/late-tuple atomicity.
TimeZone/DateStyle capture includes frozen literals versus fresh binds, DST gap
and fold choices, offset seconds, all four output styles and transaction-relative
input. Named composites and handwritten binary COPY reuse existing providers.

Native control phases reuse the shared cycle: source New York/German presentation,
target Tokyo/SQL presentation, actual ISO UTC Dump replay, deterministic redump,
same/fresh Driver Restore, cancellation/I/O and failed staging, retained populated
readers/metadata and successful TEXT-shaped replacement. Actual emitted SQL is
replayed on pinned PostgreSQL in real mode. No discovery filters remain.
`TestReferenceTimestamptzRangeComparisons` independently asserts positive BOOL/NULL
results on both backends, including plain/nested domains, contextual operands,
NULL and empty relations. Equal UTC instants compare equally despite offset spelling.

```sh
make check RUN='^TestReferenceTimestamptzRange' REFERENCE=1
make check RUN='^TestReferenceTimestamptzRange'
make check RUN='^TestTimestamptzRange'
make check RUN='^TestReferenceTimestamptzRangeFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceTimestamptzRangeFeatureSmoke$'
```

Use external900-second deadlines. Feature smoke asserts UTC ordering, continuous
binds/NULL, frozen literals/fresh NY binds, exact malformed/reversed/NOTNULL atomic
errors, nested-domain metadata and actual nonUTC control lifecycle. Pinned18.0
initial discovery passed before implementation; native/final dual-mode proof is
root-owned and is not implied by these commands or readiness.


### Composite attribute evolution selected proof

Run each selected group without and with `REFERENCE=1` (external 900-second
deadline); real mode also executes unchanged actual native Dump bytes on PostgreSQL:

```sh
make check RUN='^TestReferenceCompositeAlter(Values|Arrays|Descriptor)$'
make check RUN='^TestReferenceCompositeAlter(StoredDependencies|Diagnostics|Authorization)$'
make check RUN='^TestReferenceCompositeAlter(Prepared|Atomicity|Controls|DumpReplay)$'
make check RUN='^TestReferenceCompositeAlter(FunctionalPrepared|MaterializedDependencies|DomainChecks|InheritedChecks)'
make check RUN='^TestCompositeAlter(FunctionalRetainedFields|MaterializedStoredGuard|DomainCheckPruning|InheritedCheckStageAuthority)'
make check RUN='^TestReferenceTypedTableCompositeAlter(Conversions|Modes)$'
make check RUN='^TestReferenceCompositeExprBoundaries$'
make check RUN='^TestReferenceTypedTableNestedStoredBoundary$'
make check RUN='^TestComposite(ArrayEvolution|ArrayCodec|ArrayStorageAdmission|DomainArrayGenerationOwnership|AlterDescriptorCandidate|AlterCancellationAndReplacement)$'
make check RACE=1 RUN='^TestCompositeAlterCancellationAndReplacement$'
```

Independent observations: appended scalar/array members are NULL, rename retains
identity, drop/re-add never revives a value, dimensions/lower bounds and NULL
elements survive nested/domain generation mapping. Domain-array units stage and
publish actual declarations/holder rows, then verify stable identities and
reciprocal codecs through evolution and catalog replacement. Malformed bound array
input asserts the exact portal parameter Context in both modes. Storage-free
descriptor TYPE does not require Assignment, but actual typed roots/descendants still do.
RESTRICT and stored TYPE fences preserve the entire old state; actual dependent
field CHECKs are diagnosed or removed by CASCADE. Held field/expanded queries keep
original slots/names and return NULL for a dropped slot; named outputs use the
current descriptor, held literals retain original input shape and current binds
decode freshly. Eager rows remain immutable. Native controls retain final fields,
arrays, constraints, typed attachment and future writes; malformed/canceled imports
cannot publish a prefix. Composite comparison/hash/index providers remain absent.
The index-consumer native phase asserts the existing qualified type-name provider
boundary, while PostgreSQL creates a real dependent expression index.

Review-regression feature smoke uses the FunctionalPrepared, MaterializedDependencies,
DomainChecks and InheritedChecks selector above in both native and `REFERENCE=1`
modes. Expected observations: original held dot/functional TEXT slots become NULL
after DROP and stay NULL after re-add, including zero-member composites; actual
materialized scalar/array/domain/nested storage rejects explicit identity/widening
TYPE even empty/all-NULL and with CASCADE. Domain field CHECK RESTRICT diagnostics
and CASCADE notices name the actual constraint; unrelated CHECK enforcement and
outermost nested-domain diagnostics survive. PostgreSQL asserts its exact domain
constraint catalog bag; native independently asserts the documented absent domain
projection and real before/after enforcement. Internal units prove actual OID
pruning and immutable descriptors, not an empty-catalog proxy. Typed child/diamond
CHECK removal survives later propagation, fresh/Reset Restore and actual unchanged
native Dump replay on pinned PostgreSQL, with future accepted/rejected writes.
Ordinary inherited ALTER and absent comparison/index providers remain unchanged.


### Information-schema yes_or_no selected proof

The untagged `TestReferenceInfoYesOrNo` definitions independently cover
consumer-created public columns declared as `information_schema.yes_or_no`,
YES/NO/NULL, direct/prepared/held bindings, assignment sizing versus explicit
truncation, raw VARCHAR/string/length3 metadata on populated/empty/all-NULL
results, logical domain/base/C/companion/CHECK catalog relationships, complete
English diagnostics, failed multirow atomicity and ordinary precancellation.
The PostgreSQL-observed inferred-parameter portal Context is asserted identically
on both backends. General information-schema columns/domains queries remain
explicit PostgreSQL controls; the existing native table_privileges fields keep
their primitive representation. No neighboring provider is migrated.

Run from the repository root with an external900-second deadline per command:

```sh
make check RUN='^TestReferenceInfoYesOrNo'
make check RUN='^TestReferenceInfoYesOrNo' REFERENCE=1
make check RUN='^TestReferencePGTypeAdmission$' REFERENCE=1
make check RUN='^TestReferencePGTypeAdmission$'
make check RUN='^(TestInfoYesOrNo|TestInfoTimeStampBuiltinDescriptor)'
make check RUN='^TestReferenceInfoYesOrNoFeatureSmoke$'
make check RUN='^TestReferenceInfoYesOrNoFeatureSmoke$' REFERENCE=1
```

The feature smoke declares the qualified domain, binds YES/NO/nil, checks exact
rows and base metadata plus genuine catalog identity, rejects CHECK-invalid and
overlength multirow writes without a prefix, and exercises native Dump/Reset/
Restore with enforcement still active. The larger NativeControls definition
adds same/fresh/repeated replacement, retained rows/metadata and atomic failed/
canceled Restore. PostgreSQL runs the shared SQL acceptance before the explicit
native-only API boundary; no PostgreSQL control API or allocator parity is implied.
Private units supplement this with immutable inline values, base sizing before
CHECK, exact native13256/13255 identity, real recipe truth/dependencies and catalog
generation reownership. Reserve quickcheck for whole-project Docker-free checks.
The catalog admission definition also keeps native13256/13255 in its complete
inventory and independently checks the shared domain/base/C/companion facts.
Private catalog fixtures allocate relation identities rather than assuming the
first user-range OID is free; complete pg_constraint bags retain and validate
the genuine bootstrap CHECK through staging, publication and Reset.

### Information-schema cardinal_number selected proof

The untagged `TestReferenceInfoCardinalNumber` families cover public declared
columns, 0/7/2147483647/NULL, literal and bound direct/prepared conversions,
INT4 result metadata on populated/empty/all-NULL rows, logical domain/base and
linked CHECK catalog identity, complete English diagnostics, multirow atomicity,
held-statement recovery and ordinary precancellation. Identity includes explicit
PostgreSQL-only information-schema columns/domains controls; shared catalog and
public-column acceptance still runs on both backends.

The pinned PostgreSQL18.6 observations distinguish negative CHECK failures
(23514), malformed integer text (22P02), textual integer overflow (22003 with
the value quoted), and numeric overflow (22003, `integer out of range`).
CHECK and numeric-overflow failures occur at execution without Position.
Malformed/overflowing text literals fail at prepare for prepared statements
and at execution otherwise. The UTF8-prefixed INSERT positions are 73 for
assignment, 78 for CAST, and 74 for `::`; the unprefixed assignment position
is 67. Bound failures have no Position. The PostgreSQL-observed Context is
asserted identically on both backends: `unnamed portal parameter $1 = '...'`
for inferred parameters and base-typed malformed/overflowing text, and `$2`
for the held INSERT.

Run all eight families from the repository root, with an external900-second
deadline per command (root owns execution), oracle mode first:

```sh
make check RUN='^TestReferenceInfoCardinalNumber(Values|Metadata|Identity|Diagnostics|NativeControls|NativeFreshRestore|NativeRestoreAtomicity|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceInfoCardinalNumber(Values|Metadata|Identity|Diagnostics|NativeControls|NativeFreshRestore|NativeRestoreAtomicity|FeatureSmoke)$'
make check RUN='^TestReferenceInfoCardinalNumberFeatureSmoke$'
make check RUN='^TestReferenceInfoCardinalNumberFeatureSmoke$' REFERENCE=1
```

The smoke expects exact 0/7/NULL rows, INT4 metadata and linked domain identity,
negative/malformed-text rejection without a valid prefix, then native
Dump/Reset/Restore retaining rows and enforcement. `NativeControls` keeps the
retained cursor and saved metadata across Reset/Restore on the same owner.
`NativeFreshRestore` independently checks fresh-driver restoration with the
remap-spacer role, followed by a second Dump/Restore on that same destination.
`NativeRestoreAtomicity` gives each negative, malformed, syntax and precanceled
replacement its own fresh `NewScenario` SQL baseline and fresh destination;
these are independent failed-replacement witnesses, not phases of the retained
cursor or repeated roundtrip. All fixtures keep their existing bounded contexts;
no existing fixture clock is reset and no operation is retried for success.
Each failed Restore preserves sentinel42, absence of the staged table, exact
0/7/NULL rows, domain relationships and all direct/prepared enforcement checks
on the same restored destination. Negative and malformed replay diagnostics
retain their full fields and literal positions; precancellation retains ordinary
`context.Canceled` identity. Source and destination use the existing fixture's
owned pools, recorders and cleanup; no parallel fixture framework is introduced.
These APIs are explicit native-only phases after shared SQL acceptance, not
simulated PostgreSQL controls. Allocated domain/constraint OIDs are checked by
relationships, not cross-server numeric equality.

For the lifecycle remedy alone, select every changed/new definition in both
modes (external900-second deadline each), then selected race proof:

```sh
make check RUN='^TestReferenceInfoCardinalNumberNative(Controls|FreshRestore|RestoreAtomicity)$' REFERENCE=1
make check RUN='^TestReferenceInfoCardinalNumberNative(Controls|FreshRestore|RestoreAtomicity)$'
make check RUN='^TestReferenceInfoCardinalNumberNative(Controls|FreshRestore|RestoreAtomicity)$' RACE=1
```

`NativeRestoreAtomicity` contains `negative`, `malformed`, `syntax` and
`precancel` subcases, each running the same SQL baseline on every available
backend before its explicit native-only phase. The unchanged FeatureSmoke
also consumes `infoCardinalNumberDump` and `infoCardinalNumberRestored`; neither
helper's assertions or implementation change in this remedy.

Physical database/sql metadata is INT4, raw Go int64 or nil, with unknown
Length/DecimalSize. Native ScanType is int64 and declared Nullable is known;
pq ScanType is int32 and Nullable is unknown. These driver facts do not replace
the logical domain Usage or information-schema numeric precision32/radix2/scale0.
Both logical and base usages have typmod-1/collation0: the checked-domain
promotion must not inherit yes_or_no's C collation. The companion remains
lookup-only and character_data remains nonconcrete; existing provider carriers
and general native information-schema columns/domains providers are unchanged.

Supplemental package units cover inline payload and typed-NULL conversion,
actual bound TRUE/FALSE/UNKNOWN CHECK evaluation, catalog replacement,
fresh INT4 generation reownership and logical descriptions:

```sh
make check RUN='^TestInfo(CardinalNumber(BuiltinDescriptor|BuiltinCatalog|BuiltinBaseRebind|LogicalDescriptions)|SQLIdentifierBuiltinDescriptor|TimeStampBuiltinDescriptor|YesOrNoBuiltinDescriptor)$'
```


### INET subnet predicate oracle-first contract

The untagged `TestReferenceINETSubnetValues`, `Boundaries`, `Resolution`,
`SharedOperators`, `CatalogPaths`, `Diagnostics`, `Atomicity`, `BindsMetadata`,
`Controls` and `FeatureSmoke` definitions independently specify `<<`, `<<=`,
`>>`, `>>=` and `&&`. Strict containment excludes equal prefix widths;
inclusive containment and overlap compare network bits, not host bits.
Non-NULL mixed families always return false, including mapped IPv6 versus
IPv4. CIDR and scalar domain chains reuse implicit INET conversion.

The root recorded pinned PostgreSQL oracle-first discovery and native
failing-before evidence before production editing. Discovery filters are removed.
The shared scenarios retain independent truth tables, exact English diagnostics,
operator positions/stages, computed BOOL metadata and demand-sensitive routine
ACL expectations. Known-TEXT rejection uses the supported `'::1'::text` spelling;
range/multirange collateral uses typed columns and input rather than enabling
adjacent constructors or explicit casts.

```sh
make check RUN='^TestReferenceINETSubnet(Values|Boundaries|Resolution|SharedOperators|CatalogPaths|Diagnostics|Atomicity|BindsMetadata|Controls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceINETSubnet(Values|Boundaries|Resolution|SharedOperators|CatalogPaths|Diagnostics|Atomicity|BindsMetadata|Controls|FeatureSmoke)$'
```

Use external900-second deadlines. Repeat both selected modes after implementation.
After implementation repeat both selected modes, whole-project `make quickcheck`
(external900 seconds by assignment override), and the public consumer executable
from session artifact `local://net-ip-ops-smoke.go`, materialized temporarily as
`net-ip-ops-smoke-temporary/main.go`: `go run ./net-ip-ops-smoke-temporary/main.go`.
Controls verify actual native Dump/Reset/Restore, a fresh Driver, emitted SQL on
PostgreSQL, regenerated expressions and retained rows after failed staged restore.
These definitions do not enable INET integer shifts/XOR, network arrays or
grouping/hash providers, DNS or OS networking.

Component supplements (no public SQL/API scenarios):

```sh
make check RUN='^Test(NetworkSubnet(Values|Boundaries|PlanValidation)|NetworkSubnetProviderLifecycle|INETSubnetBinding|INETSubnetRoutineAuthority|INETSubnetParser)$'
make check RUN='^TestReferenceObjectGrantINETSubnetAuthority$'
make check RUN='^TestReferenceObjectGrantINETSubnetAuthority$' REFERENCE=1
```

The executable smoke must print only after checked assertions: nested TTFFT,
equal FTFTT, disjoint FFFFF; IPv4/IPv6 partial-byte boundaries and unchanged host
bits; mapped-family FFFFF and NULL vectors; BOOL/bool empty/all-NULL metadata;
22P02 held-input diagnostics with valid recovery and three unchanged rows;
default/generated/view recomputation after same/fresh-Driver native replay.

### LSEG oracle-first contract

`TestReferenceLsegValues`, `Comparison`, `Components`, `Diagnostics`, `Metadata`,
`Controls` and `FeatureSmoke` share the public fixture and geometry raw-adapter
checks. They specify ordered endpoints, documented and historical input forms,
six scalar comparisons (endpoint equality versus epsilon-length predicates),
strict NULLs, zero-based POINT subscripts, exact metadata, full English error
records, statement atomicity and native text/literal replay.

The independent release-source expectations were checked against the pinned
PostgreSQL 18.6 oracle before production editing. Historical delimiter forms,
NaN/infinite-length comparisons, diagnostics and default-capability rejection
remain explicit contracts. Default-opclass utility errors occur at execution,
including after successful Prepare; expression-resolution errors fail Prepare.
Both backends execute these original definitions without discovery filters.
PostgreSQL does not exercise native host Dump/Reset/Restore phases.

```sh
make check RUN='^TestReferenceLseg(Values|Comparison|Components|Diagnostics|Metadata|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceLseg(Values|Comparison|Components|Diagnostics|Metadata|Controls|FeatureSmoke)$' REFERENCE=1
```

Use external 900-second deadlines. The smoke creates and inserts all four
documented forms plus reversed endpoints, binds NULL through a held statement,
checks six comparisons, updates endpoint0, checks literal and held-bind errors
with recovery, and checks native replay with retained old rows. The shared
geometry helper also serves BOX/POINT: run affected BOX scenarios in both modes
after helper changes. These tests do not request spatial operators, companion
arrays, default sort/hash authority or a binary codec.

Supplemental private provider units cover exact coordinate-bit identity versus
epsilon SQL equality, copy-on-write endpoints, allocation-free POINT fetch,
scaled distance/range boundaries, strict NULL and unavailable default/binary
capabilities:

```sh
make check RUN='^TestLseg(InputOutput|Comparison|Components|ExactReplay|Capabilities)$'
```

Typed literal parser regression covers unqualified, quoted and pg_catalog LSEG
heads with exact literal positions, while other schemas retain domain-only
syntax authority. Existing parser classification admits LSEG alongside
BOX/POINT; type resolution and input diagnostics remain analyzer-owned.

```sh
make check RUN='^TestLsegParserAdmission$'
```

Review regression `TestReferenceLsegDelimiterBoundary` distinguishes permitted
ordinary whitespace/immediate trailing commas from rejected whitespace after
the final optional comma. It specifies complete22P02 messages, literal quote
positions, bind execution context, direct/prepared/held routes and recovery.
The independent release-source boundary expectations passed pinned PostgreSQL
oracle discovery, then exposed the native malformed-input regression. Discovery
filters are removed. Both modes must pass after decoder changes; earlier
oracle/baseline evidence is not post-fix proof.

```sh
make check RUN='^TestReferenceLsegDelimiterBoundary$'
make check RUN='^TestReferenceLsegDelimiterBoundary$' REFERENCE=1
```

