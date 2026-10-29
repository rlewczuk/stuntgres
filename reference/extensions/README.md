# reference/extensions

Extensions and loadable code: the extension registry, installation, TEMP
installs, versions and removal, `pg_extension`, the available-extension views,
LOAD, loaded-module and optional-runtime inquiries, pgcrypto, uuid-ossp and fuzzystrmatch, plus encoding conversions
(`pg_conversion`, which share the extension TEMP helpers). The other catalog
families are listed in [reference/catalogs](../catalogs/README.md).

`pg_conversion` exposes the [eight-column catalog contract](https://www.postgresql.org/docs/18/catalog-pg-conversion.html)
from implemented conversion descriptors, including off-search-path nondefault
objects. Conversion identity, namespace and owner remain distinct from the
encoding pair and procedure identity; rejected definitions publish no catalog row.

Runtime information scenarios keep physical facilities distinct from extension
descriptors and settings. Typed empty sources, genuine backend-local libraries,
collector absence, exact format validation, live routine ACLs and portable ACL
reconstruction use public fixture/control APIs only.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package; pgcrypto's BF/DES/CAST scenarios need the legacy OpenSSL provider
([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `pgcryptoCase`, `uuidOsspCase`).
- Conversion helpers (`conversion_helpers_test.go`) own scenario setup, registered
  handler declarations, metadata and real byte assertions. `conversionHideSeed`
  registers repair before moving a default; owner-transaction assertions use the
  owner executor, never `b.DB`. Direct/held autocommit, SQL BEGIN and Go BeginTx
  routes couple dispatch to private metadata, transition/savepoint restoration,
  current authority and peer isolation. DEFAULT checks distinguish serial
  visible conflicts from legal moved/concurrent distinct-name definitions.
  Conflict workers use shared lock observers and are always joined; native
  controls use unwrapped pools for committed-only Dump and fresh-driver Restore.
  Catalog assertions use independently captured namespace/owner identities and
  raw OID handling; held full-row reads preserve metadata checks even for empty
  results. Rejected-creation assertions check publication and builtin identity
  after each execution route. Oracle provenance follows the
  [fixture policy](../fixture/README.md#postgresql-oracle-policy-and-provenance).
- Expected data comes from published vectors, never from native output:
  RFC/NIST/Openwall/PostgreSQL regression vectors for pgcrypto, uuid-ossp and
  fuzzystrmatch.
- Template dependency diagnostics isolate each installed callback in a
  definition-only probe so RESTRICT has one genuine external cause, independent
  of extension-member allocation order. The executable full callback pair
  separately checks both catalog edges, dictionary restriction and the complete
  extension-to-template-to-dictionary CASCADE closure.
- BRIN pageinspect images are independently assembled from released native
  layouts in `pageinspect_brin_fixture_test.go`; descriptor tables stay empty.
  The shared scenarios exercise the identical bytes in both modes, including
  compressed inline datums, typed errors and executable stored/control identities.
  A shared metadata checker asserts adapter-specific scan types and both native
  nullability flags independently of cursor contents, including replayed views.
  Literal catalog identities, retained prepared consumers and unchanged-byte
  output-setting transitions supplement the stored decoder results.
- GIN pageinspect images in `pageinspect_gin_fixture_test.go` independently
  encode released metadata, opaque flags and compressed posting segments.
  Expectations never use production decoders or acquired pages. Shared helpers
  cover direct/prepared/held calls, exact diagnostics, adapter metadata and
  current privileges; stored consumers and native controls preserve symbolic
  installed identities, raw bytes and immutable retained results. Unsafe C
  layouts belong only in native decoder units, not the PostgreSQL oracle.
- LOAD (`load_fixture_test.go`) checks missing-file candidates inside the oracle
  container only and requires `dynamic_library_path=$libdir` and an installed
  `plpgsql.so`; unexpected files are failed preconditions, never skips.
- FIPS mode needs one process per mode:
  `env GODEBUG=fips140=off make check RUN='^TestReferencePgcryptoRandomFIPSMode$'`,
  then the same with `fips140=on`.
- Active provider cancellation uses typed `stuntgres.WorkEncodingConvert`,
  `WorkPasswordSHA` and `WorkPasswordBcrypt` admission quanta through the shared
  [fixture](../fixture/README.md#helpers), not function-name matching.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Encoding conversions: `RUN='^TestReferenceConversion'`
- LOAD: `RUN='^TestReferenceLoad'`
- Runtime inquiries: `RUN='^TestReferenceRuntimeInfo'`
- Extensions (registry, TEMP, removal, versions): `RUN='^TestReferencePGExtension'`, `RUN='^TestReferenceExtensionTemporary'`, `RUN='^TestReferenceExtensionDrop'`, `RUN='^TestReferenceExtensionVersion'`, `RUN='^TestReferenceExtension'`
- Available-extension views: `RUN='^TestReferenceAvailableExtensions'`
- Installed registry fixtures require genuine stock `plpgsql` 1.0 on both
  backends: bootstrap owner, `pg_catalog` export target, nonrelocatable, and
  NULL configuration arrays. Generated extension OIDs are captured, not fixed;
  held statements retain the real row across name shadowing, and native controls
  preserve retained row data/metadata while fresh generations resolve current
  identities. Available-but-uninstalled modules remain absent from `pg_extension`;
  the complete available-extension registry also includes installed `plpgsql`.
- Installed dictionary metadata: `RUN='^TestReferenceTSDict'`
- Template definitions, callback execution, authority, dependencies, lifecycle and native controls: `RUN='^TestReferenceTSTemplate'`
- pgcrypto: `RUN='^TestReferencePgcryptoAES'`, `RUN='^TestReferencePgcryptoBF'`, `RUN='^TestReferencePgcryptoPassword'`, `RUN='^TestReferencePgcryptoRandom'`
- uuid-ossp: `RUN='^TestReferenceUUIDOssp'`, `RUN='^TestReferenceObjectGrantUUIDOsspAuthority$'`
- fuzzystrmatch: `RUN='^TestReferenceDaitchMokotoff'`, `RUN='^TestReferenceLevenshtein'`, `RUN='^TestReferenceMetaphone'`, `RUN='^TestReferenceSoundex'`
- tablefunc Gaussian sources: `RUN='^TestReferenceTablefuncNormal'`
- BRIN pageinspect: `RUN='^TestReferencePageinspectBrin'`
- GIN pageinspect: `RUN='^TestReferencePageinspectGIN'`
- tablefunc hierarchy sources: `RUN='^TestReferenceConnectby'`. Shared helpers
  own caller-record metadata, full SQL diagnostics, retained statements and
  explicit native-control phases. Renamed members use current names or stored
  view identities; held original-name SQL follows ordinary reparse semantics.
  `RawSQLAndQuoting` covers read-only utility rejection with unchanged settings,
  catalog and data, unsupported SPI transaction/frontend-COPY distinctions and
  multiple SELECT last-result semantics.
  Nested derived, CTE, scalar and stored-view locking witnesses cover all four
  strengths directly and prepared against conflicting held tuple locks with
  NOWAIT; exact nonvolatile diagnostics distinguish rejection from row-lock
  execution. An unused locking CTE remains a successful read-only control.
  Actual PG18.6 diagnostics for weaker enclosing/stronger stored marks include
  plain `SELECT` when a planned reference/COPY marker precedes the locking
  markers. Nested-view forms, earlier weak/later unrelated strong ordering, and
  plain versus ordered view boundaries have separate direct/prepared witnesses.
  Reversed locking-clause versus FROM order is exercised directly and through
  stored/nested views. A parent-explicit/earlier-child locking control prevents
  comparing restarted first-mark orders across query levels.
  `AuthorityAndPrepared` traverses a
  recursively protected policy relation under target-only DML with private
  inserts/deletes; its native phase observes a scheduler wait on the dynamically
  named source before canceling SELECT and INSERT, checking no exposed rows,
  atomic target preservation, and subsequent demand. Phase-specific sorting and
  accumulated-output DML publication proofs live in the composed core owner tests.
  `Smoke` includes cyclic atomic INSERT and successful bounded INSERT.
- Mutable routine/provider names and schemas retain genuine OID/kind and
  extension associations; this does not promote lookup-only catalog identities
  to execution. Cross-family ALTER metadata and replay proof lives in
  [query_core](../query_core/README.md#routine-alter-proof), selected in both
  native and `REFERENCE=1` modes.

- pgcrypto digest, HMAC, installation and controls:
  `RUN='^TestReferencePgcrypto(Digest|HMAC|Installed|Overloads|Diagnostics|Consumers|Control|Replay|Forced|FeatureSmoke)'`

### Extension ownership

`extension_ownership_*_test.go` exercises mutable membership separately from
installed provider identity: relocatable unaccent, stock nonrelocatable plpgsql,
trusted installation authority, every represented member class, recursive
companions, transaction/savepoint visibility, dependency restrictions and
physical TEMP-owner retirement. Expectations come from the PostgreSQL 18 manual
and release implementation; reference execution validates them independently.
Each class has a native-only logical replay and independent-driver phase after
its shared SQL assertions. All ownership controls—including arrays and array
names, absent origins, range placement/collisions, rowtype names, stock deltas,
tablefunc/connectby survivors, templates and smoke—pair each raw Reset/Restore
pool with that destination's recorded SQL pool and recorder over the same
connector. Value, metadata and cleanup callbacks carry that recorder with their
executor, so consumed-value and recorded cursor-close assertions cannot borrow
source evidence. Exact rows, member counts, inventories, owners and detach
witnesses remain independently asserted.
Bounded survivor inventories share one genuine source graph per corpus/state
across same-driver and independent-driver replay, rather than reinstalling and
mutating identical graphs for each phase. Source assertions run before replay;
each destination independently repeats its value, authority and dependency
checks through its own recorded executor. Ltree and template retained-row witnesses
open two actual source resources before either reset and preserve both through
both restores; neither resource is reconstructed by querying a restored catalog.
Stock language/handler mutations are rolled back or repaired in each destination
before its connection is released. Foreign-table dependency diagnostics use the
statement's effective lookup path, qualifying names outside that path just like
ordinary relations.
Trusted installation alone does not authorize movement of bootstrap-owned
functions: the authority fixture requires atomic rejection, retains ordinary
same-target ownership/CREATE checks, and performs successful full unaccent
relocation under actual superuser authority. Inherited extension ownership
likewise does not bypass the lower member mover's authority checks.

Configuration membership includes parser and ordered dictionary-map preservation,
independent current ownership, exact duplicate/wrong-member and schema diagnostics,
direct/prepared vector and typed-NULL behavior, and relocated same/independent
native controls. Tablefunc survivor cases use genuine Gaussian providers with
zero deviation for deterministic observations, preserving strict NULL, SRF
metadata and stored views across detached/foreign membership and absent origin.
They do not replace or change random generation.

The Snowball seed-chain membership case uses actual allocated helpers, template,
English dictionary and configuration with different current extension receivers
and detached states. It keeps these seeds in `pg_catalog` and exercises ordinary
same-namespace rename, so cleanup requires no system-schema movement capability.
It preserves owners/ACLs, options, mappings, held/direct/prepared consumers and
same/independent control identities, old rows, byte-redump and failed-restore
atomicity. Allocated initdb seeds are not pinned: attached DROP is restricted by
the current receiver, detached DROP by genuine normal dependencies; an
unreferenced detached configuration can drop inside a rolled-back transaction.
The ordinary dictionary class witness distinguishes fixed simple-template pin
suppression from the genuine allocated Snowball-template normal edge.

The bounded synonym membership case uses fixed pinned callbacks/template with
different current receivers and a detached callback, plus one ordinary dictionary.
It preserves rename identity, callback declaration/principal metadata, ordered
options and actual pin-suppressed normal edges across source and same/independent
native replay. Ordinary member dictionaries retain CREATE declarations; fixed
seeds retain pin restrictions independently of current membership, and cleanup
detaches/restores the actual seeds before receiver teardown.

The existing MemberClasses `parser` row uses ordinary CREATE with the genuine
`prsd_start`/`prsd_nexttoken`/`prsd_end`/`prsd_lextype` callbacks, optional
Headline zero, and one nonmember configuration mapping asciiword to simple.
Its 6→7→6 membership transitions preserve actual identities and normal
dependencies; direct/prepared token, vector, empty and typed-NULL results,
five-callback metadata, member/dependent DROP restrictions, collision and
mixed-schema atomicity are independently asserted. The existing class controls
repeat these witnesses after relocated same-driver and independent-driver replay,
including retained rows and failed-restore atomicity, without replacing ordinary
parser CREATE with an extension event.
The registered `parser_authority` leaf isolates bare ownerless ADD/DROP authority
and the lower relocation gate by transferring real function/dictionary owners
and detaching only the ownerless unaccent template. The real dictionary dependency
remains. Before and after that lawful superuser detach, bounded nonsuperuser
template DROP/ADD probes also require bare `must be superuser` and preserve the
actual parent, e edges and target state; lower template movement keeps its
distinct diagnostic. `parser_seed_membership` lawfully adds/detaches default parser
3722 and requires the exact system-pin DROP diagnostic, including the quoted
`"default"` description observed on pinned PostgreSQL 18.6, before member
restrictions. It detaches on failure before extension teardown. Authority
expectations follow REL_18_6, not native output.
Select all three leaves with
`RUN='^TestReferenceExtensionOwnershipMemberClasses$$/^parser(_authority|_seed_membership)?$$'`
in native and `REFERENCE=1` modes.

Template survivors use genuine standalone dsimple and unaccent callback pairs,
as well as the installed unaccent template. Ordinary creators remain ordinary
when added to an extension. Lawful builtin callback membership is exercised in a
rolled-back transaction, without removing pinned builtins during cleanup.
Detached and foreign template/callback cohorts retain real dictionary normal
dependencies after origin retirement. Direct, prepared and retained lexizers
assert values, typed NULL and metadata; same-driver and independent-driver
controls share that family's source graph and use destination recorders,
retained rows, byte-stable redumps and failed-restore atomicity.
Definition-only optional-INIT probes never
execute unsafe mixed-provider or uninitialized callback combinations.

`earth_scalar_survivors` preserves the detached radius and both float8 conversion
procedures after genuine earthdistance/cube retirement, including each real
conversion-to-radius normal dependency and the absence of nominal types.
Controls repeat those assertions and the transactional radius-CASCADE/rollback
witness through separately named same-driver and independent-driver destinations.
Reset/Restore uses each destination's raw control pool; SQL assertions use its
recording adapter and matching recorder, so the earlier CASCADE's zero count
cannot stand in for a restored procedure count of three.

`earth_body_coordinates` creates genuine Earth installations across lawful
cube movement. Shared-cube leaves cover a removed quoted historical schema,
an ordinary relation occupying the historical canonical type key, and captured
pg_catalog placement. The occupied-provider leaf retains an old cube cohort at
canonical historical keys after a distinct new cube/Earth cohort captured those
keys and moved away. The first Earth origin is retired after detaching its real
domain and six SQL routines. Both backends check distinct/current base-type and
callee dependencies, preserved source OIDs, direct/prepared values and stored
consumers. Native same/independent controls also preserve all six raw captured
trees, a pre-reset reader and byte-identical redump.
The `occupied_provider_pg_catalog` leaf performs actual same-namespace source
renames, retains a real old equality operator, and distinguishes builtin-input
constructor collisions from coexisting nominal-input overload/operator keys.
It checks exact array labels/identity/owner coherence and real e/n edges, and
forbids emitted CREATE/DROP pg_catalog or SET SCHEMA into it.
Its PostgreSQL teardown retains the actual installed function OIDs until all
witnesses finish, then drops surviving detached helpers through their current
SQL identities. Upstream builtin-only GiST helpers are not necessarily removed
by dropping cube's nominal type; they must not leak into a later leaf.
No PostgreSQL historical syntax-text equivalence is inferred from its bound tree.
The focused selector is
`make check RUN='^TestReferenceExtensionOwnership(Membership|Controls)$$/^earth_body_coordinates$$'`;
repeat with `REFERENCE=1` for the pinned server. The exact original metadata
regression remains `make check RUN='^TestReferencePGProcInstalledControls$'`
and its `REFERENCE=1` counterpart.

The same Membership/Controls families also register `crosstab_survivors`,
`hstore_survivors` and `pageinspect_families_survivors`. These use genuine released
record/named crosstab providers, the retained hstore nominal/helper graph and
BTREE/FSM/GIN fixture images. Shared SQL mutates membership and current identities,
retires the original extension lawfully, and checks direct/prepared/stored/NULL
behavior with exact logical declaration metadata. Native phases repeat the
survivor behavior through same-driver and independent-driver restoration; they
do not serialize runtime cursors, physical page handles or source sessions.
The full hstore corpus is partitioned into bounded serial value inventories under
unchanged fixture deadlines. Each inventory owns one genuine complete nominal/helper
mutation and constructor/record dependency witnesses shared by both restore
destinations; together they retain every original value and lifecycle assertion.

Ltree array survivors use the complete released 1.3 corpus: sixteen routines
and sixteen operators, including all four first-match extractors (`?@>`, `?<@`,
`?~`, `?@`). Serial two-case value inventories each retain one complete nominal,
I/O, array and operator source graph for both destinations under the unchanged
scenario deadline.
Per-operation declaration, owner/ACL and normal-dependency assertions follow
the same disjoint inventory as values; the complete corpus is checked for
each state and destination without repeating every operation in every leaf.
Expected first matches follow input order rather than label order, including nondefault
array lower bounds; misses and strict typed NULL remain NULL. Direct/prepared
calls, held/stored consumers, current owner/ACL, declarations and e/normal edges
survive detachment, foreign membership and lawful origin retirement. Native
same-driver and independent-driver controls use destination-owned recorders and
retain pre-reset rows and their nominal metadata.
Stored consumers record explicit function and operator occurrences independently:
operator implementation dependencies remain on the operator, while genuine
function calls nested in operands retain their own normal edges.
Select the family with `RUN='^TestReferenceExtensionOwnershipLtreeArraySurvivors$'`
in native and `REFERENCE=1` modes.

The shared occurrence contract also covers explicit hstore array, JSON,
constructor, record and row calls, and captured citext string/regexp calls.
Their view-to-procedure edges survive detached and foreign membership and
destination-recorded native controls without depending on executable lifetime
manifests.

Ltree comparison survivors retain all nine comparison/hash routines, six
operators and both B-tree/hash class/family support graphs from the complete
94-declaration recipe (97 initial current members including generated arrays).
Three-routine metadata inventories share one source graph across both destinations
using the same ownership scenario/control helpers. Callable coverage is the six
documented operators and genuine
ordering/equality/index routes, not extra direct SQL APIs for C backers.
Detached and foreign final states preserve direct/prepared/held/stored and
strict-NULL behavior after origin retirement. Operators move independently of
their implementing routines, while all nine backing declarations, current
owners, ACLs, e/normal/internal and loose hash-support dependencies remain
observable. Generated arrays retain their internal scalar edge rather than
an independent namespace dependency. Separate B-tree and hash
tables preserve ordered results and collision equality rechecks; native
same/independent controls pair each destination with its recorder and retain
old rows/metadata. Select this family with
`RUN='^TestReferenceExtensionOwnershipLtreeComparisonSurvivors$'` in both modes.

Hierarchy survivors exercise both directly callable `ltree_isparent` and
`ltree_risparent` as well as their reciprocal `@>`/`<@` operators. Bounded
serial relation inventories share their source graph across both destinations
and retain self and empty ancestry, case-sensitive whole labels and non-prefix
misses through direct/prepared, held and stored
calls, including each strict typed-NULL operand. Genuine detachment, ordinary
rename, foreign `uuid-ossp` membership and origin retirement preserve full
logical declarations, current owner/ACL and e/normal edges. Same-driver and
independent-driver native controls use destination-owned recorders, compare
destination declarations and byte-stable redumps, reject partial failed
restores, and retain old row values and metadata.
Select with `RUN='^TestReferenceExtensionOwnershipLtreeHierarchySurvivors$'`
in native and `REFERENCE=1` modes.

Path/query survivors cover all twenty-three operations: nineteen path signatures,
both scalar lquery directions and both scalar ltxtquery directions, with three
concatenation operators and reciprocal `~`/`@` query operators. Bounded ltxtquery
values distinguish unordered labels, Boolean precedence/negation, empty paths,
case/prefix/word modifiers and false matches; each endpoint has strict NULL coverage.
Reciprocal callees and operators are retained only within their own query group.
Before membership mutation the scenarios capture genuine nominal, array, operator
and discovered helper identities and declarations, and prepare direct and stored
consumers. Exact normal edges
from each scalar type to its actual `pg_type.typinput`/`typoutput` OIDs are
independently expected before helper discovery, after detach and foreign attach,
and after origin retirement. Pinned builtin array codecs do not imply such rows.
Each detached or foreign operation partitions its complete value corpus, including
generated strict-NULL operands, into disjoint inventories of at most six values.
Only larger corpora add inventory subtests. Each inventory owns the full genuine
source graph and feeds both real native control destinations under the original
scenario deadline, bounding stored-view work in each catalog dependency projection.
Every inventory retains an independent non-NULL nested-call witness and both
original source row resources. The controls repeat the exact codec edges and compare
destination-relative graphs, including companion namespace, owner, element and
array links, current ACLs and membership, strict NULLs, old rows, byte-stable
redumps and failed-restore atomicity. Consumer normal dependencies distinguish
explicit function calls from operator syntax, including a genuine SQL-function
operand, and rejected RESTRICT drops exercise normal dependencies independently
of the current extension link.
Select with `RUN='^TestReferenceExtensionOwnershipLtreePathQuerySurvivors$'`
in native and `REFERENCE=1` modes.

Pageinspect's `Membership/pageinspect_survivors` and
`Controls/pageinspect_survivors` each contain `detached` and `foreign` cases.
Actual pageinspect installation supplies the BRIN declarations (including OUT
metadata), and an owner-renamed classifier retains its OID, declaration and
current owner/ACL after detachment, genuine uuid-ossp attachment and retirement
of the original extension. The stored consumer exists before that retirement.
The shared released-layout images independently classify as `meta`, `revmap`
and `regular`; direct/prepared typed NULL remains NULL with TEXT metadata.
The existing BRIN metadata checker covers scan types and both nullability
flags for direct/prepared calls and the stored view. Native controls repeat
these assertions after same-driver and independent-driver Dump/Reset/Restore,
including current e/normal edges and symbolic declaration/ACL equality, without
resurrecting pageinspect or serializing physical page handles.
BRIN and BTREE/FSM/GIN survivor controls keep raw pools for Reset/Restore only.
Every restored SQL witness uses that destination's wrapped recording pool and
matching recorder, including prepared calls and permission failures. A prior
strict-NULL or zero-page result cannot substitute for the restored image's
independently expected values.

The fixture independently expects 2BP01 on standalone member DROP (including
CASCADE), with the extension-required primary message and the hint to drop the
current uuid-ossp extension instead. Once detached, the stored view instead
causes 2BP01 with its normal function dependency in Detail and the standard
DROP CASCADE hint. Both diagnostics are execution errors with ERROR severity;
position, internal position/query, context, schema/table/column/data type and
constraint fields are empty, as are Detail for the membership diagnostic and
all other unspecified fields. Cleanup drops the real consumer, detaches the
current membership if present, and only then drops the function and receiver.
These cases supplement, rather than replace, the complete baseline BRIN suite.

Connectby's `Membership/connectby_survivors` and `Controls/connectby_survivors`
each run `detached` and `foreign` cases through one shared SQL body. All four
genuine released overloads retain their own identities, declarations and
current owner/ACL after detach, rename, uuid-ossp attachment and lawful retirement
of tablefunc. Stored views bind before these changes. Direct/prepared calls and
views reuse the existing deterministic tree and caller-record layouts: unordered
overloads compare bags, while ordered overloads explicitly order by serial.
Strict typed NULL produces an empty record result even with an absent dynamic
source. Current exact e and namespace-normal dependencies remain authoritative;
same-driver and independent-driver native replay repeat results and symbolic
metadata/ACL comparisons without resurrecting tablefunc. These additions leave
normal_rand and the complete integrated connectby suite unchanged.

The connectby native corruption controls use only owner-v3 records and select
the genuine released overload by symbolic schema/name/signature and origin
version, not record order or current member count. The complete pageinspect and
connectby survivor snapshots retain `pg_proc.probin`, grounded in the
[18.6 tablefunc control](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/tablefunc/tablefunc.control)
and [18.6 pageinspect control](https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/pageinspect/pageinspect.control).
These are immutable declaration paths, independent of detached or foreign
membership; they do not authorize library loading.

GIN and crosstab native corruption controls select the unique owner-v3 cohort
by provider and final presence, independently of the stock plpgsql seed and
absent cohorts. They select the renamed GIN leaf, crosstab overload and named
crosstab result type by symbolic identity and released version, never member
order or slot. Unknown codec versions, missing owners and corrupt recipe
identities must still fail without publishing over the live control state.

Companions and Controls preserve exact generated-array names for installed and
ordinary member domains and relation rowtypes. Real parent renames and domain
displacement produce triple, numeric, mixed and UTF-8 truncation scars, with
blockers retained or their history removed; direct array ALTER remains protected.
Range and multirange placement is independent. Generated constructors keep their
actual routine identities, including a renamed range whose real subtype-difference
function occupies the final constructor signature. Same/independent native replay
must retain callable constructors, defaults, NULLs, membership and ordinary edges
without leftover replay schemas, domains or arrays.

Selected commands (run each reference definition in both modes):

```sh
make check RUN='^TestReferenceExtensionOwnership(Relocation|Bootstrap|Membership|MemberClasses|Companions|Authority|Transactions|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceExtensionOwnership(Relocation|Bootstrap|Membership|MemberClasses|Companions|Authority|Transactions|Diagnostics)$'
make check RUN='^TestReferenceExtensionOwnership(Prerequisites|Controls|Resources|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExtensionOwnership(Prerequisites|Controls|Resources|FeatureSmoke)$'
make check RUN='^TestExtensionOwnership(Identity|Membership|Relocation|Publication|Controls|Resources)$'
make check RACE=1 RUN='^(TestExtensionOwnership(Publication|Resources)|TestReferenceExtensionOwnership(Transactions|Resources))$'
make check RUN='^TestReferenceExtensionOwnership(Membership|Controls)$$/^pageinspect_survivors$$'
make check REFERENCE=1 RUN='^TestReferenceExtensionOwnership(Membership|Controls)$$/^pageinspect_survivors$$'
make check RUN='^TestReferenceExtensionOwnership(Membership|Controls)$$/^connectby_survivors$$'
make check REFERENCE=1 RUN='^TestReferenceExtensionOwnership(Membership|Controls)$$/^connectby_survivors$$'
make check RUN='^TestReferencePageinspectBrin(Installation|Pages|Revmap|Items|Datums|Metadata|Diagnostics|Permissions|OutputEnvironment|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePageinspectBrin(Installation|Pages|Revmap|Items|Datums|Metadata|Diagnostics|Permissions|OutputEnvironment|Controls)$'
```

The prerequisite fixture lives in `reference/extension_types`; it reuses the
installed cube/earthdistance contract rather than constructing substitute
provider types. `TestReferenceExtensionOwnershipFeatureSmoke` is the bounded
public feature smoke, including native same-driver and independent-driver
Dump/Reset/Restore witnesses. Internal owner assertions in `core` inspect
immutable descriptors, declaration identity, reciprocal edges, publication
fences, remap ownership and physical drain; they are not SQL-only duplicates.
