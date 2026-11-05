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

The `system_rows_*_test.go` family fixes installed-handler identity, bigint
assignment coercion, exact visible cardinality, post-sample predicates, complete
diagnostics and metadata, prepared reuse, cancellation and symbolic native
controls. Sub-population samples assert membership and uniqueness, never the
selected identities or order; full-population samples assert the exact bag.
Inheritance and partitioning assert separate physical-source caps and ONLY
behavior, alongside admitted materialized views and exact source-kind errors.
The PostgreSQL18.6 original-held-handler cache defect is narrowly recorded in
`docs/POSTGRES_REGRESSION.md`; native definitions reacquire current authority.
The per-outer-row count witness uses existing scalar correlated-query syntax,
not adjacent explicit derived-table `LATERAL` support. Name reuse is shared:
a callable SQL `system_rows(integer)` is not a sampling handler, while the
qualified genuine provider remains usable. The internal-signature/wrong-result
`42809` SQL phase is PostgreSQL-only because arbitrary internal/C ABI routine
declarations are external mechanisms; `TestSystemRowsHandlerResultBinding`
independently reaches the native SELECT binder with the real typed catalog
descriptor and requires the same complete diagnostic. View-output metadata is
nullable under the existing view contract; sampled base/empty/retained-row
metadata keeps the base column's `NOT NULL` guarantee.
`TestReferenceSystemRowsViewWrites` fixes automatic INSERT/UPDATE/DELETE
rejection (including a zero-row sample), complete reason/hints, unchanged base
data and updatability metadata. Explicit INSTEAD OF rule/trigger routes remain
usable. Controls repeat the automatic-write boundary after symbolic replay,
including an independently remapped destination.
The SQL `pg_relation_is_updatable` observation is PostgreSQL-only: that builtin
is not a supported native caller. Common information-schema view/column metadata
remains exact in both modes, complemented by native capability-owner tests.
This is a caller-support boundary, not a physical/external mechanism exception
or a claim that the builtin is implemented.

The `system_time_*_test.go` family exercises floating-point millisecond budgets
through direct and held prepared public calls. Zero and positive infinity have
exact empty/exhaustive expectations; finite samples are occurrence-bounded
members of independently established visible bags, never timing-count or
unordered-sequence goldens. Metadata and complete logical diagnostics remain
exact. Backend subtests precede feature setup for oracle-first `/postgres`
selection. Snapshot, post-sample pipeline, inheritance, observed lock and nested
RLS cancellation, and native-only symbolic control phases share the same SQL
contract. The generous elapsed-query guard is liveness evidence, not a precise
whole-query millisecond deadline. Native production-stage units separately
exercise real nested elapsed work, rejected-tail expiry and ownership cleanup.
Physical heap-block distribution, cache/clustering and PostgreSQL shared-library
C ABI execution are external mechanisms, not portable sample-count promises.
The internal-signature wrong-result declaration is explicitly PostgreSQL-only.

## pgstattuple: index statistics ownership

`pgstatindex_*_test.go` owns the public acceptance for the installed
`pgstatindex(text)` and `pgstatindex(regclass)` overloads.
`pgstathashindex_*_test.go` owns `pgstathashindex(regclass)` and
`pgstatginindex_*_test.go` owns `pgstatginindex(regclass)`.
Heap tuple and page inquiries have their own shared scenarios below.
The B-tree fixture installs in `ps_part`, hash in `ph_part` and GIN in `gg_part`;
all scenarios use public SQL, `reference/fixture`, and public native controls,
never internal packages.

- `Metadata`, `Values`, `Diagnostics`, and `Permissions` fix the released
  signatures, ten OUT names/types, fresh-empty composite, strict NULL source
  cardinality, complete logical diagnostics and ordinary per-overload EXECUTE
  ACLs. Table ownership/SELECT is neither sufficient nor required; scan-role
  membership and explicit EXECUTE grants are independently exercised.
- `Binding` owns scalar/source/projection metadata, unknown and explicitly typed
  prepared arguments, named arguments, coercions, NULL, lateral calls, and
  sequence-backed once-per-occurrence evaluation.
- `Contents` and `Transactions` exercise real partial/expression/INCLUDE indexes,
  populated page relationships, private DML, savepoint/transaction rollback and
  fresh-empty TRUNCATE. No test invents tuple-count OUT columns or infers index
  membership from heap count. Exact logical live/dead capture is additionally
  the production owner's private-unit responsibility.
- `Identity` distinguishes names from OIDs, quoted/numeric/dash names, held text
  versus bound regclass, rename/name reuse, routine rename/move, relocation,
  user shadows and extension recreation.
- `Controls` owns stored scalar/expanded callers, symbolic regclass dependencies,
  scan ACL replay, held statements, retained rows, atomic rejected/canceled
  Restore, independent-driver destination roots and preserved session TEMP data.
- `Cancellation` checks native already-canceled ordinary context errors, then
  observes real index AccessShare admission blocked by a peer's transactional
  `DROP INDEX` AccessExclusive lock before cancellation. PostgreSQL asserts the
  complete server-cancellation `57014` diagnostic, not fast pre-canceled pq
  behavior. Rollback, successful reads and another exclusive `DROP INDEX` with
  rollback prove both preserved index identity and admission resource release.
  `FeatureSmoke` combines empty/populated calls, both overloads, permission
  distinction, wrong-target diagnostics and native stored-view replay.

Fresh-empty output is independently published by PostgreSQL 18.6:
`(4,0,8192,0,0,0,0,0,NaN,NaN)`. Populated physical values are **not parity
fixtures**. Native uses a deterministic compact model, not physical inspection:
8192-byte pages, 8152-byte leaf capacity, aggregate resident-item weights
(20-byte entry overhead plus stored key/include slots), fanout 256, no retained
empty/deleted pages, two-decimal density and zero compact-order fragmentation.
Exact target identity, ACLs and real logical membership remain exact. Shared
populated assertions independently require valid types, page-size accounting,
a usable root, nonnegative counters and finite bounded percentages. PostgreSQL
may retain leaf/dead-page history after DELETE or rollback; these tests do not
equate that history with the stateless native model.

Selected proof (run each selector both without and with `REFERENCE=1`):

```sh
make check RUN='^TestReferencePgstatindex(Metadata|Values|Binding|Diagnostics|Permissions)$'
make check RUN='^TestReferencePgstatindex(Identity|Controls|Contents|Transactions|Cancellation)$'
make check RUN='^TestReferencePgstatindexFeatureSmoke$'
```

`TestReferencePgstatindexPermissionArgumentOrder` additionally fixes executor
authorization precedence over a nonconstant failing child expression, through
direct/prepared/held calls for both overloads and grant/revoke transitions.
Run `make check RUN='^TestReferencePgstatindexPermissionArgumentOrder$'` both
natively and with `REFERENCE=1`; denied callers expect `42501`, while explicitly
authorized callers reach the child and expect `22012` (`division by zero`).

Hash fixtures install in `ph_part` and use supported full, sibling and expression
indexes. Fresh root witnesses are 4/4/5 live entries, rebuilt witnesses 1/1/4;
stored expression views distinguish exact target identity with 5 entries.
The eight released fields include `dead_items`, not `dead_tuples`. Ordinary
coercion, owned scalar/FROM shapes, STRICT NULL, runtime ACLs, supplied-index
AccessShare cancellation, identity and native controls share the same scenario
body in both modes. Native hash statistics sample effective-root bucket lengths,
report dead zero and apply the compact model documented by `index`; private DML
phases assert callability, shape and lifecycle rather than forced private counts.
PostgreSQL physical values are independently invariant-checked, not model parity.

GIN has exactly three OUT fields: `version INT4`, `pending_pages INT4`,
`pending_tuples INT8`. Native uses the deterministic physical approximation
`(2,0,0)`, independent of genuine logical main/pending inversion and `CleanGin`,
not metapage inspection or logical live/dead accounting. Pending token-posting
incidences and cleanup estimates are not PostgreSQL pending heap-tuple/page
counts. Fresh builds, prepopulated builds and fastupdate-off insertion have
independently specified zero pending counters in PostgreSQL 18.6 too. A separate
default-fastupdate trajectory expects positive PostgreSQL pending pages and one
pending heap tuple after inserting one nonempty tuple; native still expects
`(2,0,0)`.
Sorted array/vector query results, real DML and transaction rollback independently
prove logical contents and distinct index identities. Metadata, strict NULL,
binding, permissions, errors, held statements, cancellation and actual native
dump replay reuse the shared installed statistics interfaces. Public diagnostics
retain partitioned-table rejection and ordinary leaf GIN success. Owning units
cover partitioned-index relkind and differing custom-AM OID without unsupported
partitioned GIN or custom-opclass creation prerequisites.

Selected GIN proof (each public selector both natively and with `REFERENCE=1`):

```sh
make check RUN='^TestReferencePgstatginindex(Metadata|Binding|Values)$'
make check RUN='^TestReferencePgstatginindex(Diagnostics|Permissions)$'
make check RUN='^TestReferencePgstatginindex(Contents|Transactions|Identity)$'
make check RUN='^TestReferencePgstatginindex(Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestPgstatginindex(Provider|Capture|CaptureCost|CaptureTargetChecks|Expression)$'
```

The standalone GIN `FeatureSmoke` exercises real array/vector indexes and query
changes, typed outputs/NULL/wrong-kind errors, held parameters and stored-view
native controls with actual SQL replay into PostgreSQL; it is not inventory-only.

## Heap tuple statistics scenarios

`pgstattuple_heap_*_test.go` owns the shared public scenarios for
`pgstattuple(text/regclass)`, `pgstattuple_approx(regclass)` and
`pg_relpages(text/regclass)`. The fixture installs in `hs_part`, disables
autovacuum on the counted heap, and uses an established REPEATABLE READ
horizon where genuine predecessors must remain available. Exact logical
counts are independently specified for committed, own, peer and detached
prepared transactions; ordinary SELECT snapshot visibility is checked
separately. Own inserted-and-ended versions distinguish approximate Vacuum
classification from peer insert-in-progress classification. Savepoint and full
rollback assert restored live rows without assuming portable aborted-tuple debris.
The rollback fixture explicitly ends its horizon after the protected savepoint
phase, before later constraint DDL; maintenance ends it before heap replacement.
A deferred rollback alone would retain an AccessShare lock that blocks the
fixture's own AccessExclusive operation.

The declaration and empty-result scenarios check the complete released
catalog rows, OUT metadata, named/positional/direct/prepared/held paths,
one-time child evaluation and non-SETOF STRICT NULL expansion. Permissions
exercise actual per-overload EXECUTE, scan/monitor inheritance and schema
USAGE, independently of table SELECT. Constant scalar page NULLs may fold before
EXECUTE; nullable dynamic page arguments and record NULLs retain runtime checks.
Kind/error scenarios include supported
heap, leaf, materialized-view, sequence and index targets, unsupported kinds
and access methods, textual lookup versus OID inputs, and peer TEMP storage.
GIN, TOAST, custom table-AM and invalid-index construction are explicitly
oracle-only setup phases; they do not claim native public construction coverage.
Native descriptor/host dispatch supplements GIN, invalid supported-index and
custom table-AM release witnesses without adding public construction machinery.

Complete raw empty/NULL results establish every output field's name and type,
including direct and prepared calls. Populated byte/page/scanning values use
SQL equations and bounded percentages, not backend byte equality. The SQL
projections check page alignment, length/free-space accounting, finite/range
bounds and grouped CAST/round expressions for exact two-decimal percentages.
Logical live/dead counts have independent expected values.
Maintenance scenarios distinguish protected from eligible versions, then check
TRUNCATE and descriptor changes. Native composed units additionally pin real
immutable heap/index roots, atomic owner pre/post merge capture and PREPARE,
savepoint restoration, publication vetoes, old captures and retirement.
Stored name/OID consumers, moved members, extension relocation and native
logical Dump/Reset/Restore retain real SQL witnesses, held statements and old
Rows; rejected/canceled Restore and independent destinations check atomicity.
The clean native retained-statistics control captures only after explicit
maintenance retires its aborted peer insertion; rollback itself is not a
physical pruning operation. The retained Rows still outlive Reset unchanged.

## Testing

- Elapsed-time table sampling: `RUN='^TestReferenceSystemTime'`.

Password-strength scenarios use `RUN='^TestReferencePasswordCheck'` and the
ordinary public fixture with genuine oracle `WithPasswordCheck()` startup.
Independent direct/prepared outcomes cover byte boundaries, complete hook
diagnostics, typed settings, parameter ACLs, NULL/empty/prehash distinctions,
publication atomicity and interrupted username verification. Static registration
scenarios cover default-off/zero Driver, freeze, independent clusters and logical
databases. Native Dump/Reset/Restore and independent-Driver replay are explicit
phases in the same scenario, not substitute PostgreSQL commands. Exact oracle
credential bytes use privileged `pg_authid`; native public role identity,
membership, notices and physical admission witnesses complement mandatory
RoleRead owner tests for exact storage, clearing, omission, rejected publication
and control retention, without adding a password-inspection API. PostgreSQL
preload/C ABI/process lifetime and optional CrackLib custom builds remain external.

SYSTEM_ROWS sampling scenarios use `RUN='^TestReferenceSystemRows'` in both
modes. Backend subtests `/postgres` and `/memory` allow oracle-first selection
without filtering the shared scenario. Sampled subsets assert independently
specified count, membership and uniqueness, never cross-backend sampled IDs or
heap traversal order. Stored consumers, complete diagnostics, source-frontier
predicate witnesses and cancellation share the public fixture; native controls
remain explicit native-only phases. PostgreSQL heap blocks and C ABI loading are
external mechanisms, not replacements for exact SQL sampling behavior.

Installed statistics scenarios use `RUN='^TestReferencePgstat(index|hashindex)'`.
Visibility inquiry scenarios use `RUN='^TestReferencePgvisibility'`. Their
`pgvisibility_fixture_test.go` helper owns isolated installation, exact typed
metadata and full diagnostics. Physical flags use independently specified
ownership/maintenance fixtures; quiescent summary/scalar/SRF consistency is an
additional invariant, never a substitute for those expectations. Native control
witnesses remain phases of the same public dual-mode definitions.
`pgvisibility_check_*` adds scalar-TID/VOID contracts, distinct check/truncate ACLs,
persistent map invalidation with independent PD/MVCC, identity/stored-control
transport, and admitted lock cancellation. Its oracle-first shared callback
filters neither backend. The physical corruption case owns all PG borrowers
across fixture-owned stopped archive copy/reconnect/recovery on the same pinned
container; native contradictory-map proof belongs to actual installed-host units,
not a public fake-corruption SQL path. Stored mutators project BOOL around VOID.

Buffer-cache scenarios use `RUN='^TestReferencePgBuffercache'` with one shared
public fixture. They assert exact caller-defined record/view metadata, catalog
identity, separate real ACLs and complete diagnostics before comparison.
Physical PostgreSQL cache fields use independent domain/identity invariants;
native-only phases specify actual-read usage and capture-owned session pins.
Unused-slot NULLs require a real witness. Native controls retain SQL state while
discarding target live occupancy; source-owner units supplement public lifetime
scenarios without adding an inspection API.

The same family includes declared-OUT eviction metadata, genuine removal and
fresh read usage, pinned protection, storage targeting and shared-cluster counts.
Ordinary routine/default ACLs remain distinct from runtime superuser checks.
Generic ordinary/shadow source-call contrasts preserve decoration diagnostics
without granting provider behavior by name. Stored field views and native replay
retain callable identity and ACLs, never live occupancy or pin ownership.

Free-space inquiries use `RUN='^TestReferencePgFreespace'`. The shared fixture
keeps compact native live-row estimates distinct from PostgreSQL FSM counters,
while fixing exact declaration, identity, ACL, NULL and diagnostic contracts.
Declaration scenarios cover scalar and wrapper installation collisions separately,
including full script diagnostics and atomic preservation of the existing SQL
routine's identity, source and output without publishing staged members.
Permission scenarios select custom and generic SQL-body parameter-demand routes
explicitly: fresh custom NULL is empty, generic parameter NULL still requires the
genuine inner scalar, and revoked outer EXECUTE denies either route. Automatic
PostgreSQL plan-cache history is not inferred from a fresh outer statement.
Control and smoke phases borrow retained Rows on a separate connection, keep
their old data and metadata through Reset, then drain/close the rows and return
that borrower before fresh queries, Restore or destination DML. Private writers
are rolled back and returned before retained-output capture; held statements
remain available to prove current authority and restored identities.

Genuine partitioned-index and TOAST construction are oracle-only setup phases.
Native partitioned-index rejection additionally uses real published descriptors
and the current installed host, preserving kind-before-block diagnostics without
adding public construction machinery.

Dblink delete-construction scenarios use `RUN='^TestReferenceDblinkBuildSQLDelete'`
in both modes. Shared direct/prepared/held bodies assert independent generated
TEXT and execute the returned SQL against local fixtures. Metadata, complete
diagnostics, logical numbering, settings, privileges and genuine identities
share native replacement phases with retained results and atomic rejected replay.

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider(), fixture.WithAlterSystemDisabled(), fixture.WithPasswordCheck())`.
The genuine passwordcheck preload is oracle-only unless a scenario explicitly
registers the native capability. Pgcrypto's BF/DES/CAST scenarios need the legacy
OpenSSL provider ([fixture README](../fixture/README.md#test-only-legacy-crypto-oracle)).

- Helpers: family scenario and assertion helpers live in the family's
  `*_fixture_test.go` or `*_helpers_test.go` files (for example
  `pgcryptoCase`, `uuidOsspCase`).
  `pgcryptoPolicyCase` uses a new notice-enabled physical connection for every
  unloaded case, and revokes parameter grants before disposable-role cleanup.
  Policy assertions retain full script diagnostics and distinguish accepted
  immutable values from actual new password calls; native controls remain
  explicit phases of those same public scenarios.
- Integer dictionary scenarios use `RUN='^TestReferenceDictInt'` in both modes.
  `dict_int_fixture_test.go` owns isolated public SQL scenarios and reuses the
  shared lexeme metadata/raw-carrier assertions. Independently specified byte,
  sign, option and diagnostic expectations feed direct and retained prepared
  consumers, genuine configuration/GiST queries, mutable installed identities
  and native control phases. PostgreSQL replays actual native-emitted SQL;
  physical index implementation and native C loading are not test substitutes.
- Extended synonym dictionary scenarios use `RUN='^TestReferenceDictXsyn'` in
  both modes. `dict_xsyn_fixture_test.go` owns scenario cleanup and reuses the
  shared rich lexeme metadata/raw-carrier assertions. Independent group/options,
  diagnostic and identity expectations feed direct/held consumers and genuine
  configuration queries. Native controls preserve ordinary and installed state,
  including comment removal, and PostgreSQL replays actual native-emitted SQL.
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
  layouts in `pageinspect_brin_fixture_test.go` and, where `reference/extension_ownership`
  shares them, `reference/fixture/pageinspect_pages.go`; descriptor tables stay empty.
  The shared scenarios exercise the identical bytes in both modes, including
  compressed inline datums, typed errors and executable stored/control identities.
  A shared metadata checker asserts adapter-specific scan types and both native
  nullability flags independently of cursor contents, including replayed views.
  Literal catalog identities, retained prepared consumers and unchanged-byte
  output-setting transitions supplement the stored decoder results.
- GIN pageinspect images in `reference/fixture/pageinspect_pages.go` independently
  encode released metadata, opaque flags and compressed posting segments.
  Expectations never use production decoders or acquired pages. Shared helpers
  cover direct/prepared/held calls, exact diagnostics, adapter metadata and
  current privileges; stored consumers and native controls preserve symbolic
  installed identities, raw bytes and immutable retained results. Unsafe C
  layouts belong only in native decoder units, not the PostgreSQL oracle.
- Header/checksum pageinspect fixtures in `reference/fixture/pageinspect_header.go`
  independently assemble released headers and published checksum vectors.
  Shared cases assert exact fields, signed INT2 results, PG_LSN/XID raw
  transport, strict NULL/source cardinality, full diagnostics and current
  permissions. Constant-NULL scalar checksum elision is distinguished from
  RECORD and stored-NULL invocation ACLs. Stored consumers and native controls
  preserve symbolic identities and caller bytes without acquiring physical pages.
- Hash pageinspect images in `reference/fixture/pageinspect_hash.go` independently
  encode released bucket/overflow, line-pointer, tuple and full metadata arrays.
  Four bytea decoders share direct/prepared/held, diagnostic, privilege,
  descriptor-lifetime and symbolic-control scenarios. Safe unusual offsets and
  low header versions remain positive fixtures; undefined C reads are covered
  only by native safety units. Physical bitmap acquisition is a separate
  non-engine mechanism, not a bytea overload or synthesized logical index page.
- Heap pageinspect fixtures in `pageinspect_heap_fixture_test.go` independently
  assemble released tuple headers, line pointers and caller-owned payload bytes.
  Shared scenarios retain exact row/array expectations, raw transports, complete
  diagnostics and descriptors independently of cursor contents. Stored consumers
  preserve current installed identities through rename, membership changes and
  native controls; no physical relation pages are acquired.
  `pageinspect_heap_attrs_*_test.go` owns schema-based deformation of those
  independently supplied bytes, including dropped physical slots, NULL arrays
  and elements, and physical-versus-payload inline detoasting. Descriptor
  relations are empty; no fixture acquires pages or invents TOAST content.
  Declarations, named binding, full diagnostics, retained metadata and real
  stored consumers share direct/prepared/held routes and native controls.
  Helper-ACL scenarios revoke real member permissions independently of wrapper
  grants and retain startup denial across NULL, empty and unavailable-tail inputs.
  `FunctionDefinitions` compares full independently specified PG18.6
  `pg_get_functiondef` TEXT for both SQL wrappers, including current formal
  qualifiers, stable function-RTE aliases and current helper names. Direct,
  fresh-prepared and held routes cover helper/wrapper rename and move, valid
  detachment and native control replay. `FeatureSmoke` also discloses both full
  definitions before and after its native reset/restore phase.
  The family selector is `RUN='^TestReferencePageinspectHeapAttrs'`, in both modes.
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

- Heap tuple statistics: `RUN='^TestReferencePgstattupleHeap'`
  Declaration fixtures distinguish declared signatures from implicit
  text-to-regclass coercion. Permission fixtures retain dynamic nullable table
  inputs so EXECUTE denial is reached, and separately cover PostgreSQL's static
  STRICT-NULL scalar-call elision (RECORD calls are not elided). Supported-index
  counts explicitly distinguish PostgreSQL's physical post-DELETE item (one for
  B-tree/hash) from native current-root simulation (zero). Both count one before
  DELETE and zero when empty; root-only GiST counts zero. No broad backend skip
  or parity-only assertion replaces these independent expectations. Physical heap
  equations retain exact two-decimal assertions using standard CAST syntax.
  Native public GIN index construction is unavailable; only that genuine
  construction witness joins the explicit oracle-only TOAST/invalid-index phase.
  Its exact/approx rejection diagnostics and page-count assertions remain
  present. Production `TestPgstattupleHeapCapture` must separately prove native
  GIN descriptor dispatch, exact primary/detail diagnostics and page capture;
  adding GIN construction is outside this part.
  Visibility schedules distinguish own and peer approximate classifications:
  Vacuum treats own inserted-and-ended versions as DELETE_IN_PROGRESS (live),
  but a peer treats ongoing xmin as INSERT_IN_PROGRESS (dead), before xmax.
  Exact Dirty classifications remain unchanged.
- Forced tuple freezing: `RUN='^TestReferencePgsurgeryFreeze'`. Shared scenarios
  capture actual TIDs and transaction headers within each backend, then assert
  exact-version repair, rollback/live-owner outcomes, strict arrays, diagnostics,
  slot notices, live authority and installed identities. Explicit maintenance
  creates slot-disposition witnesses; no autovacuum timing or physical allocator
  parity is assumed. Native-control phases preserve visible SQL data and routine
  authority without requiring physical TIDs, XIDs or discarded history to survive.
  Relation-kind diagnostics use genuine SQL objects in the same shared body,
  including a UNIQUE index on a partitioned parent. Descriptor-level method
  dispatch is additionally covered by the installed runtime's native owner units.
- Forced tuple killing: `RUN='^TestReferencePgsurgeryKill'`. Shared direct/held
  prepared scenarios assert exact unsafe removal, unchanged DELETE/FK consumers,
  current identity/ACLs, strict NULL planning versus live NULL admission,
  diagnostics/notices, irreversibility, retained snapshots, private/prepared
  publication and scoped lock/cancel boundaries. The native amcheck phase asserts
  genuine retained killed membership; the PostgreSQL phase independently proves
  physical HOT-parent failure. Native controls test authenticated orphan replay
  and atomic rejection of edited streams, not serialized TIDs or handles.
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
  Complete native inventory expectations include pg_freespacemap 1.3 and
  pg_buffercache 1.6 as genuinely untrusted, superuser-required, relocatable
  available-but-uninstalled modules with no fixed schema or prerequisites.
  Their exact released comments and control flags are asserted independently in
  both source-capture/statement-frontier inventories and the public dual-mode
  filtering scenario, without asserting whole-oracle inventory parity.
  The btree_gin row uses released default/version1.3, trusted/superuser-required
  and relocatable controls, no fixed schema or prerequisites, and the exact
  released comment. Filtering asserts these rows directly, freshly prepared and
  held in both modes; complete native inventory and private statement-frontier
  expectations include the same available-but-uninstalled module.
  Stock file_fdw uses default/version1.0 and the released comment
  `foreign-data wrapper for flat file access`: superuser-required, untrusted,
  relocatable, no fixed schema or prerequisites. It is available but uninstalled
  in these registry fixtures, not an unavailable-module control. Its independent
  rows join both complete native inventories without filtering unknown rows or
  weakening bag completeness. Run `make check RUN='^TestReferenceAvailableExtensionsFiltering$'`
  in native and `REFERENCE=1` modes for direct, prepared and held observations.
- Installed dictionary metadata: `RUN='^TestReferenceTSDict'` preserves full
  metadata and identity links across unaccent installation/removal, including
  the complete native bootstrap inventory of 18 dictionaries (simple and 17
  Snowball providers, including Swedish), then 19 with unaccent installed.
- Template definitions, callback execution, authority, dependencies, lifecycle and native controls: `RUN='^TestReferenceTSTemplate'`
- pgcrypto: `RUN='^TestReferencePgcryptoAES'`, `RUN='^TestReferencePgcryptoBF'`, `RUN='^TestReferencePgcryptoPassword'`, `RUN='^TestReferencePgcryptoRandom'`, `RUN='^TestReferencePgcryptoArmor'`, `RUN='^TestReferencePgcryptoPolicy'`, `RUN='^TestReferencePgcryptoPGP'`
  PGP scenarios anchor an independent RFC/stdlib decoder with upstream ciphertext,
  then authenticate actual SQL output without comparing random packet bytes.
  Authenticated framing cases distinguish outer rejection from inner context
  lengths and verify atomic multi-literal concatenation. Chunk cases supply
  complete bodies at and above the release's per-chunk 16 MiB bound, including
  continuations and valid larger partial aggregates. The independent authenticity
  decoder deliberately retains RFC-valid overlimit chunks, so a release-framing
  rejection cannot be confused with damaged crypto. Literal-format cases check
  every non-`t`/`u` classification and structural/MDC diagnostic precedence.
  Large direct/prepared witnesses each own a fresh scenario and compare complete
  TEXT quietly; failures retain complete diagnostics and forbid any returned row.
- uuid-ossp: `RUN='^TestReferenceUUIDOssp'`, `RUN='^TestReferenceObjectGrantUUIDOsspAuthority$'`
- sslinfo connection inquiries: `RUN='^TestReferenceSSLInfo'`
- Stock username trigger helper: `RUN='^TestReferenceInsertUsername'`
- fuzzystrmatch: `RUN='^TestReferenceDaitchMokotoff'`, `RUN='^TestReferenceLevenshtein'`, `RUN='^TestReferenceMetaphone'`, `RUN='^TestReferenceSoundex'`
- pg_trgm: `RUN='^TestReferencePgTrgm'`. Settings scenarios create fresh physical
  notice-enabled sessions; resetting schemas or using DISCARD does not unload a
  library. Stored identity and native replay assertions retain independent
  destination recorders and distinguish provider declarations from SQL shadows.
  Fractional threshold SET inputs use quoted configuration strings, retaining
  released REAL validation, precision and journal behavior. Bare integer
  endpoints zero and one remain exercised.
- tablefunc Gaussian sources: `RUN='^TestReferenceTablefuncNormal'`
- BRIN pageinspect: `RUN='^TestReferencePageinspectBrin'`
- GIN pageinspect: `RUN='^TestReferencePageinspectGIN'`
- Header/checksum pageinspect: `RUN='^TestReferencePageinspect(Header|Checksum)'`
- Hash pageinspect: `RUN='^TestReferencePageinspectHash'`
- Heap pageinspect: `RUN='^TestReferencePageinspectHeap'`
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
- Simulated provider ownership scenarios are in
  [reference/extension_ownership](../extension_ownership/README.md#testing),
  selected with `RUN='^TestReferenceSepgsqlSimulation'`.

- pgcrypto digest, HMAC, installation and controls:
  `RUN='^TestReferencePgcrypto(Digest|HMAC|Installed|Overloads|Diagnostics|Consumers|Control|Replay|Forced|FeatureSmoke)'`

Extension membership and ownership tests (`TestReferenceExtensionOwnership*`) are in
[reference/extension_ownership](../extension_ownership/README.md#testing).

## Stock refint oracle contracts

The `TestReferenceRefint*` scenarios specify the released `check_primary_key`
and `check_foreign_key` trigger helpers, not declarative foreign keys. They cover
literal argument protocol, partial-NULL shortcuts, exact generated SQL error
locations, restrict/cascade/setnull effects, current prepared UPDATE values,
notices, nested statement rollback, invoker privileges/RLS, inheritance, and
binding identity. Installation includes untrusted denial and atomic late-member
collision. Metadata checks distinguish native known nullability from pq's
unknown nullability; native controls additionally exercise independent Drivers
with the same opaque DSN.

The shared scenarios run natively and, with `REFERENCE=1`, against pinned
PostgreSQL 18.6. Diagnostics are complete records, including empty fields;
duplicate-install notices use `42710`. Invoker-denial and held-revoke diagnostics
include the nested SQL Context. Late-install collisions and nested-action
cancellation retain their independently specified Context expectations.
There is no oracle-only gate or diagnostic-discovery path.

Cancellation uses a pool-level pre-canceled adapter query without assuming nil
Rows, then observes the actual nested child-relation lock wait before canceling
and joining the operation. PostgreSQL cancellation uses the server to preserve
the physical notice owner for rollback/state checks and cleanup. Native uses
ordinary context cancellation at the same admitted action boundary.

Selected proof (external command deadline 900 seconds; run both modes):

```sh
make check RUN='^TestReferenceRefint(Installation|Primary|Arguments|QuotingDiagnostics|PreparationOrder|Atomicity|NestedAtomicity|Visibility|Privileges|Cancellation|Persistence|Identity)$'
make check REFERENCE=1 RUN='^TestReferenceRefint(Installation|Primary|Arguments|QuotingDiagnostics|PreparationOrder|Atomicity|NestedAtomicity|Visibility|Privileges|Cancellation|Persistence|Identity)$'
make check RUN='^TestReferenceRefintFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceRefintFeatureSmoke$'
make check RACE=1 RUN='^TestReferenceRefint(Cancellation|Persistence)$'
```

Actual PostgreSQL SPI/C ABI, shared-library loading and process-local allocator
mechanisms are external. SQL effects, permissions, diagnostics, notices,
statement atomicity and native persistent-object bindings are not excluded.
