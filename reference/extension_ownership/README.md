# reference/extension_ownership

Extension membership and ownership: `ALTER EXTENSION ADD/DROP` of every member
class, `SET SCHEMA` relocation, installation authority, companions, members that
survive detachment, foreign membership and retirement of their original
extension, and the native Dump/Reset/Restore controls of these states. The
extension registry, installation and removal themselves are in
[reference/extensions](../extensions/README.md).
The sepgsql family owns explicit simulated provider identities, labels, physical
subjects and their query/control effects. This registration is not an operating
system security mechanism; its ordinary PostgreSQL phases remain independent
from source-grounded active native policy expectations.

The bounded `isn_conversion_*_test.go` and `isn_validation_*_test.go` corpora own
installed ISN comparison, conversion, ordering and validity declarations,
independent packed-value expectations, full diagnostics, weak-mode physical
session/journal boundaries and retained provider identity through SQL lifecycle
and native controls. They use public fixture APIs; short presentation strings
are not comparison keys.
Typed-nil bytes are SQL NULL for direct typed ISN parameters and runtime
TEXT-to-ISN casts on both backends. Ordinary nil is also NULL; nonnil empty
bytes remain erroneous empty input. Direct, prepared and held routes retain
both NULL propagation and complete empty-input diagnostics.
Native replacement preserves the catalog-lifetime fence for held installed-type
parameters: they fail with the exact cached-parameter diagnostic, while a fresh
prepare binds restored identities. A held comparison with builtin TEXT inputs,
interior ISN casts and a BOOL result reanalyzes against the restored provider;
its fixed public signature does not authorize reuse of retired installed types.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the same
oracle options as `reference/extensions`
(`fixture.WithLegacyCryptoProvider()`, `fixture.WithAlterSystemDisabled()`).
Helpers shared with `reference/extensions` live in `reference/fixture`:
`ExtensionDump`, the physical TEMP owner (`ExtensionTemporaryOwner`), tablefunc
connectby/crosstab/normal helpers, template lexeme helpers and the released
pageinspect page images (`pageinspect_pages.go`, `pageinspect_header.go`). Ownership scenario and control
helpers shared with `reference/extension_ownership_ltree` live in
`reference/fixture/extension_ownership_helpers.go` (`OwnershipScenario`,
`OwnershipSurvivorControls`, ...); the rest are in
`extension_ownership_helpers_test.go` and
`extension_ownership_survivor_helpers_test.go`.
- Explicit simulated SELinux provider: `RUN='^TestReferenceSepgsqlSimulation'`.
  Each definition keeps meaningful ordinary/inactive PostgreSQL phases and a
  separately named native simulation phase. This is query compatibility, not
  kernel enforcement or an active PostgreSQL SELinux oracle.
  Both callbacks receive the actual fixture backend, so dynamic extension-type
  metadata uses existing backend-specific helpers: lib/pq reports an empty cube
  type name while native reports `CUBE`; neither result is cast away or inferred.
  `LoadDispatch` preserves SQL authority and distinguishes real module selection
  from unrelated activation. `RestoreconSharedDatabases` and
  `RestoreconAuthorization` check all shared database labels, current-root local
  traversal, denied-peer atomicity and superuser/profile precedence. Genuine
  post-peer Dump/Reset/Restore retains destination peer labels and peer-local
  data while restoring only the selected database's serialized state.
  `RestoreconEscapedBrackets` independently checks inactive-provider rejection,
  ordinary catalogs/data, and direct/prepared native restorecon with literal
  bracket escapes versus ordinary ranges and first-match labels. It verifies
  actual `pg_seclabel` associations and unchanged unrelated labels/data; select
  `make check RUN='^TestReferenceSepgsqlSimulationRestoreconEscapedBrackets$'`
  natively, with `REFERENCE=1`, and with `RACE=1`.
  The dedicated Restorecon family also owns genuine dropped-column catalog
  identity/typed-control preservation, five released traversal phases, unmatched
  warnings with full diagnostics, old/new relabel consent, rollback/savepoints,
  declaration rename/replacement/retirement and actual-feature smoke. Its
  family selector is `RUN='^TestReferenceSepgsqlSimulationRestorecon'`, natively,
  with `REFERENCE=1`, and with `RACE=1`. The source-grounded active phase never
  substitutes fake oracle functions or promises within-phase physical ordering.
  `GenericLabelResolution` retains actual object lookup, lock and ownership
  precedence before unsupported label-class rejection, including type/domain and
  existing shared tablespaces. `GenericLabelRoleOwnership` checks current actor
  CREATEROLE plus actual ADMIN (not self/mere membership), SUPERUSER-target
  precedence and held role-store changes. `GenericLabelDomainKind` checks actual
  domain kind before ownership and decoded written qualification, including
  array/domain distinctions and held DROP/recreation. `GenericLabelColumnIntegrity`
  checks sequence ownership before generic kind before provider, full primary/
  detail diagnostics, held owner changes and unchanged generators/labels/data.
  All three use direct/prepared/held native simulation and separate ordinary
  provider-unloaded plus role rename/domain drop/sequence comment contrasts in
  native and pinned PostgreSQL modes; no active SELinux parity, new label class
  or physical tablespace creation is implied.
  `SequenceCreate` covers actual CREATE SEQUENCE directly, prepared and through
  the same held handle: default P/name-selected Q labels without relabel,
  exactly one whole association/no sequence-column labels, options/generator
  metadata and owners/owned links. Independent ordinary inactive-native/pinned
  PostgreSQL phases retain option/namespace/ACL/notice diagnostics and supported
  SERIAL/CREATE identity/ADD identity behavior. Active-native phases retain
  schema-before-sequence denial, ordinary ACL precedence, actual denied audit
  bits/permissive success, initial IF NOT EXISTS noevent and TX/TEMP atomicity.
  Genuine concurrent name-binding skip and namespace/OWNED BY wait-currentness
  use the core `TestSepgsqlSimulationSequenceCreate(Currentness)?` fixtures;
  the public fixture has no namespace/name-binding admission observer.
  Existing `Controls`, `ControlConsent` and `FeatureSmoke` definitions include
  persistent explicit/serial/identity sequence labels (including relabeled and
  absent NULL associations), generators/options/ACLs/owners/owned dependencies,
  TEMP exclusion and real sequence transfer-consent/policy/root/role/cancel
  fences. Public sequence CREATE denial remains independent: CREATE-only policy
  denial rejects SQL but permits trusted replay with admitted transfer consent.
  Replay must not synthesize a default label for an absent association.
  Focused public proof: `make check RUN='^TestReferenceSepgsqlSimulationSequenceCreate$'`,
  also with `REFERENCE=1` and `RACE=1`. Select
  `RUN='^TestReferenceSepgsqlSimulation(Control(s|Consent)|FeatureSmoke)$'` in
  both native and real modes (plus `RACE=1` for controls).
  `FTS` exercises
  actual nested SQL/native callback subjects, original catalog permissions and
  direct/prepared/strict-NULL behavior with literal expectations; `Dictionaries`
  separates physical cold Init denial from warm reuse and genuine descriptor
  invalidation. `Codecs` checks genuine hstore scalar/domain/composite text I/O
  and concrete ltree-array nested I/O, independent execute versus trusted entry
  permissions, NULL leaves, renamed helper identity and denial before parsing.
  Literal-input policy errors retain the released parser callback's original
  literal position: scalar/domain position 8 and nested array position 9 in the
  selected witnesses. Runtime input/output callbacks do not acquire a fictional
  literal position.
  Select every changed definition in native and `REFERENCE=1` modes; neither mode
  substitutes for the other. Audit assertions use discriminating phase witnesses,
  not planner-dependent count equality.
  `ControlConsent` retains effective-unlabeled replacement checks and peer
  publication fences; portable labels retain genuine zero-argument aggregate
  identity as `count(*)`, not invalid `count()`. The registered foreign-factory
  fixture stages writes privately and publishes only on participant commit,
  retaining transaction rollback alongside denied-factory no-callback checks.
  `ForeignFactory` also registers a genuine scan-only method table with no
  optional importer: direct/prepared/held IMPORT yields exact `HV00P` after an
  allowed false-execute factory, but simulated `42501` at a denied entrypoint.
  SQL wrapper aliases/renames remain diagnostic text, handler rename retains
  identity, ordinary server/schema/handler and ACL errors keep precedence, and
  failed statements leave subjects, provider calls, rows and dumps unchanged.
  Its independent ordinary dual-mode boundary uses stock `file_fdw` in pinned
  PostgreSQL, without a foreign file, remote host or active SELinux oracle.
  Before role cleanup, closed scenario handles precede ordinary-admin retirement
  of its database-wide wrappers/servers and non-public destination schema.
  Teardown uses separate bounded cleanup contexts, never a refreshed scenario
  deadline; the schema's surviving USAGE grant must not strand the reader role.
  `Callbacks` independently specifies scalar hstore and concrete ltree-array
  SELECT DISTINCT grouping plus actual UTF8/LATIN1 conversion, distinguishing
  direct trusted entry from SQL execute and empty/same-encoding/NULL no-work.
  `PageSources` covers hstore NULL rows, dblink key capture, relation visibility,
  strict-NULL empty sources without body entry, and raw heap or new B-tree pages.
  Dblink's isolated trusted subject has namespace search but no procedure-execute
  or table/column SELECT permission: genuine initialization executes once as the
  caller, and PK metadata retains ordinary table SELECT ACL without fabricated
  executor DML MAC. Separate direct/prepared ACL denial and actual table-DML MAC
  denial remain required. Its exact native row/DONE entry witnesses are
  source-protocol assertions, not PostgreSQL optimizer/AVC-count parity.
  `Support` exercises genuine scalar hstore equality/order/hash and concrete
  ltree-array GIN/grouping plus ltree-subtype range kernels. It distinguishes
  ordinary execute initialization from direct callback entry, including identity
  replacement, denied nested leaves, no-work inputs and caller restoration.
  Its initial mixed B-tree/hash/GIN INSERT must publish all four genuine rows;
  array GIN uses the real default ltree B-tree comparator, not a fabricated
  array GIN role-1 support member.
  The original ordered-table query retains exact IDs 1,2,3,4 before and after
  comparator rename. Its genuine serial sort callback borrows the admitted
  evaluator, rather than the bare statement environment. Direct/prepared
  renamed-helper and range denials require full context-free diagnostics, no
  result cursor and restored caller; permitted relabel retries retain the exact
  rows/count. Empty and all-NULL ordering still invoke no installed comparator.
  The unbounded range witness first asserts its real generated two-argument
  constructor identity and original inherited P label, before any relabel.
  The complete real range/multirange constructor cohort separately requires all
  five released source/strictness/public-namespace/result declarations and all
  five inherited P labels. Exact `regprocedure` signatures select each identity
  and its raw label separately; a narrow `pg_range` lookup retains the genuine
  range/multirange family relationship without a broad catalog join.
  Labels cross the same candidate-to-journal transfer as the actual descriptors;
  no fixture relabel supplies a missing creation label.
  Both NULL bounds still invoke this nonstrict constructor: U denies its own
  direct/prepared entry, while restoring the asserted original P must return
  exact `isempty=false` with the subtype comparator still U and caller R.
  No generated-constructor label defect is hidden by setup relabeling, and no
  subtype callback is invented for infinite bounds.
  Its original-INTERNAL versus alias witness uses the genuinely installed
  `citext(boolean)` alias of `booltext`, compared with original
  `pg_catalog.text(boolean)`: both return TEXT `true` ordinarily; only the alias
  receives the denied trusted entry under the active policy. Direct/prepared
  strict-NULL calls skip both bodies. No arbitrary `LANGUAGE internal` loader is
  introduced for fixture setup.
  `AccessMethodFactories` keeps actual Bloom factory denials distinct from
  ordinary table reads. Its explicit-transaction direct/prepared filtered count
  returns 1 without a captured-index factory; the adjacent property inquiry
  genuinely invokes the denied handler, even for an unknown property. A WHERE
  predicate is not itself evidence of an index-method factory invocation.
  `Initialization` distinguishes retained preargument execute authority from
  actual strict/body entry and fresh executions from row/VPC reuse, including
  SQL/PL cleanup and default/domain/index/trigger/cursor attachment ownership.
  Conditional NULL and ordinary arithmetic retain independently expected
  outcomes without claiming optimizer or audit-multiplicity parity.
  Genuine round(numeric) FINAL and lpad(text,integer) SFUNC aggregate bodies also
  run directly and prepared: numeric4/empty0, TEXT two-spaces-x/empty-x/NULL-x.
  The native active phase separately proves the actual wrapper's trusted R-to-T
  entry, denied child execute under T before output, exact SQL-wrapper statement-1
  diagnostics, restored R and permitted exact-value retry. These wrappers keep
  their original aggregate support owner, not a fabricated scalar parent receipt.
  Native inactive execution derives that same aggregate-purpose body lifetime
  with zero simulation initializer/receipt state, rather than inventing an
  ordinary wrapper dependency. Its public DDL has no COMBINE/PARALLEL additions;
  this is not an extension of closed-body or worker eligibility.
  Ordinary both-mode SQL is the PostgreSQL18.6 compatibility witness, not active
  kernel enforcement or cross-backend worker-count evidence.
  Its ordinary SQL error witness sets a routine-local search_path: released
  `clauses.c:inline_function` rejects non-NULL proconfig, so division-by-zero
  remains an actual statement-1 executor/unwind error in both direct and prepared
  routes, not an optimizer-context parity claim. Scalar attachment denials
  require real execute masks. Direct trigger dispatch instead uses the existing
  R-to-T trusted transition with denied entrypoint permission; removing a
  transition is not an execute denial and would let its R body reach an ordinary
  domain error. Audit masks, no body initialization, unchanged rows and granted
  retry distinguish that genuine FHET boundary.
  `NativeBodies` checks selected Earth/pageinspect/freespace nested kernels,
  source-order errors and NULL demand with borrowed per-invocation ownership;
  protocol-entry witnesses remain native source contracts, not an active oracle.
  Actual successful entrypoint witnesses match the qualified quoted database,
  namespace and basename, without invented function-signature suffixes. The
  expanded row-NULL Earth case runs direct and prepared: conversion enters once,
  with neither the strict outer box nor strict cube enlargement body entered.
  `Demands` keeps ordinary relation SELECT separate from exact signed system
  attributes and whole-row user-column expansion, including empty inherited
  sources and stored views. Denying one system attribute must not deny an
  unselected different attribute or turn whole-row demand into a system sweep.
  `SurgeryIncoming` keeps actual freeze/kill changes, strict NULL, diagnostics
  and denial-before-mutation separate from ordinary table DML permissions.
  `IncomingVPC` exercises buffer snapshots and visibility checks at real
  borrowed first/row/DONE calls, including independent visibility-map and page
  flags. `Handlers` covers both `system_rows` and `system_time`, restoring the
  caller before runtime arguments and retaining the selected sampler on reset.
  `Retirement` uses the supported root-only hierarchy DROP CONSTRAINT: inherited
  leaf rejection precedes MAC, a late secret-leaf denial preserves both primary
  keys and their real backing-index identities, and a granted retry removes both.
  Multi-root DROP separately retains all tables, labels, attributes and rows on
  late denial. An ordinary-table composite DROP CONSTRAINT + ADD COLUMN + DROP
  COLUMN proves direct/prepared late column-denial atomicity without inventing
  mixed hierarchy ALTER support. Its granted result and the independent ordinary
  PostgreSQL phase require no primary key or backing index, live marker and no
  blocked column, preserved `k=1`, typed NULL marker, and exact surviving attribute
  numbers/types/nullability, with direct/prepared result metadata.

  Selected retirement proof (both modes required; these are commands, not passing
  evidence):

  ```sh
  make check RUN='^TestReferenceSepgsqlSimulationRetirement$'
  make check REFERENCE=1 RUN='^TestReferenceSepgsqlSimulationRetirement$'
  ```

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
checks through its own recorded executor. Ltree (in
[reference/extension_ownership_ltree](../extension_ownership_ltree/README.md))
and template retained-row witnesses
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
The complete detached English dictionary diagnostic includes its English,
Hindi and Russian configuration dependents on both backends.
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
`make check RUN='^TestReferenceExtensionOwnershipControls$$/^earth_body_coordinates$$'`;
repeat with `REFERENCE=1` for the pinned server. The exact original metadata
regression remains `make check RUN='^TestReferencePGProcInstalledControls$'`
and its `REFERENCE=1` counterpart.

The same Controls families also register `crosstab_survivors`,
`hstore_survivors` and `pageinspect_families_survivors`. These use genuine released
record/named crosstab providers, the retained hstore nominal/helper graph and
BTREE/FSM/GIN/header/checksum fixture images. The checksum survivor retains its
BYTEA/INT8 signature and header retains all nine OUT fields through the same
membership/control harness. Shared SQL mutates membership and current identities,
retires the original extension lawfully, and checks direct/prepared/stored/NULL
behavior with exact logical declaration metadata. Native phases repeat the
survivor behavior through same-driver and independent-driver restoration; they
do not serialize runtime cursors, physical page handles or source sessions.
The full hstore corpus is partitioned into bounded serial value inventories under
unchanged fixture deadlines. Each inventory owns one genuine complete nominal/helper
mutation and constructor/record dependency witnesses shared by both restore
destinations; together they retain every original value and lifecycle assertion.

Stored consumers record explicit function and operator occurrences
independently, as in the ltree families, for explicit hstore array, JSON,
constructor, record and row calls, and captured citext string/regexp calls.
Their view-to-procedure edges survive detached and foreign membership and
destination-recorded native controls without depending on executable lifetime
manifests.

Pageinspect's `Controls/pageinspect_survivors` contains `detached` and
`foreign` cases.
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
BRIN and BTREE/FSM/GIN/header/checksum survivor controls keep raw pools for Reset/Restore only.
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

Connectby's `Controls/connectby_survivors` runs `detached` and `foreign` cases
through one shared SQL body. All four
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

Controls preserves exact generated-array names for installed and
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
make check RUN='^TestReferenceIsn(Conversion.*|Conversions|Comparisons|Ordering|Validation.*)$'
make check REFERENCE=1 RUN='^TestReferenceIsn(Conversion.*|Conversions|Comparisons|Ordering|Validation.*)$'
```

The prerequisite fixture lives in `reference/extension_types`; it reuses the
installed cube/earthdistance contract rather than constructing substitute
provider types. `TestReferenceExtensionOwnershipFeatureSmoke` is the bounded
public feature smoke, including native same-driver and independent-driver
Dump/Reset/Restore witnesses. Internal owner assertions in `core` inspect
immutable descriptors, declaration identity, reciprocal edges, publication
fences, remap ownership and physical drain; they are not SQL-only duplicates.
