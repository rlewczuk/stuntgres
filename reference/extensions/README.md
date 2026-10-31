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

## pgstattuple: pgstatindex ownership

`pgstatindex_*_test.go` owns the public acceptance for the bounded installed
`pgstattuple` 1.5 provider's `pgstatindex(text)` and `pgstatindex(regclass)`.
`pgstathashindex_*_test.go` owns its sole hash declaration,
`pgstathashindex(regclass)`. Other module functions are not claimed.
The shared fixture installs in `ps_part`; all scenarios use public SQL,
`reference/fixture`, and public native controls, never internal packages.

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

## Testing

Installed statistics scenarios use `RUN='^TestReferencePgstat(index|hashindex)'`.

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
- Stock username trigger helper: `RUN='^TestReferenceInsertUsername'`
- fuzzystrmatch: `RUN='^TestReferenceDaitchMokotoff'`, `RUN='^TestReferenceLevenshtein'`, `RUN='^TestReferenceMetaphone'`, `RUN='^TestReferenceSoundex'`
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
