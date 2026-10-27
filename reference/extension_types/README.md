# reference/extension_types

Dual-mode tests for extension types: `seg`, `hstore`, `cube`, `earth`, `query_int`, `lquery`, `ltree`, `ltxtquery`,
`ean13`, `isbn`, `isbn13`, `ismn`, `ismn13`, `issn`, `issn13`, `upc` and `lo`.

Other type families: [reference/domain_types](../domain_types/README.md#testing)
(domains, `information_schema` domains, type identity lifecycle and
transactional types);
[reference/geometry_network](../geometry_network/README.md#testing) (geometry
and network address types);
[reference/ranges_text_search](../ranges_text_search/README.md#testing) (ranges,
multiranges, text search and UUID);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`;
these are shared across families:
- `ean13_helpers_test.go`: shared installed-ISN scenario, raw string/byte
  transport, exact diagnostics and metadata checks for installed ISN scalar roles.
  ISBN helpers share relational manifest identity and owner-record mutation
  checks across all eight roles. ISSN and ISSN13 expectations preserve invalid markers and
  nonzero issue digits, including full-number serialization in native controls.
  ISSN13 always displays the long form, independently of ISSN short output.
  UPC expectations retain compact twelve-digit values and markers natively;
  independently specified PostgreSQL short display cropping never masks native
  marker loss. Typed EAN13 conversions witness the retained packed flag.
  The reusable family selector is
  `RUN='^TestReference(Ean13|Isbn|Ismn|Issn|Upc)'`; controls retain readers across replacement,
  validate whole-script diagnostic positions and rollback with identical failing
  SQL on PostgreSQL, and replay actual emitted SQL on PostgreSQL.
  Relational identity witnesses filter catalog inputs to the installed
  scalar/companion/codec and relevant dependency edges before joining. Keep
  reciprocal links, overload signatures, ownership, membership and exact result
  metadata assertions; unrelated builtin catalog populations must not multiply
  the work of each repeated lifecycle check.
  ISBN13 and ISMN controls use `isn_controls_phases_test.go`: statement
  atomicity/recreation, same-engine replacement, displaced fresh-engine
  replacement and each failed replacement run as serial independent scenarios.
  Each failure seeds its own actual dump, frozen identity graph and retained
  cursor; complete diagnostics, values, metadata, identity and deterministic
  redump assertions remain per case. No fixture deadline spans the failure matrix.
  The ISBN13, ISMN, ISMN13, ISSN, ISSN13 and UPC failure leaves run the full
  verify (values, metadata and `isbnIdentity`) once, after the failed Restore.
  The roundtrip and `fresh_driver` phases verify the source and the successful
  replacement; a leaf does not repeat those passes.
- `lo_helpers_test.go`: shared installed-domain scenarios reuse the fixture's OID
  raw-value adapter. Logical lo identity is independent of physical OID result
  metadata, including NULL/empty results and declared nullability. Numeric
  values, text canonicalization, direct/held binds, complete diagnostics and
  statement atomicity have independent expectations. Native control phases
  preserve values and retained metadata across same/fresh-engine replacement,
  replay actual emitted SQL on PostgreSQL and exercise extension recreation.
  Same-engine replacement, fresh-engine replacement, malformed owner-metadata
  rejection and recreation run as serial subtests, each with its own bounded
  scenario and independently seeded rows. Failed public Restore preserves the
  incumbent identities, rows, metadata, retained reader and deterministic dump.
  The reusable family selector is `RUN='^TestReferenceLo'`.
- `hstore_helpers_test.go` owns scalar text/raw metadata and complete diagnostic
  assertions. Canonical pairs distinguish empty, NULL-valued and quoted-NULL
  values from SQL NULL. Conflicting duplicate keys have one allowed original
  survivor, not a backend-choice parity golden. Native controls preserve the
  chosen stored value and retained metadata; emitted SQL is replayed directly
  on the oracle. Held literal assignments retain their canonical values across
  rename/schema moves; retired installations and unrelated TEXT destinations
  bind the original SQL afresh, never stale hstore values. Conversion fixtures
  use ordinary `_TEXT` transport for owned flat/rectangular arrays and invoke
  both named functions and prefix operators after restoration.
  `hstore_json_helpers_test.go` checks JSON result metadata, detached scans and
  SQL NULL separately from JSON member null. Strict/loose expectations preserve
  numeric spelling and distinguish whole JSON numbers from string boundaries;
  stored views/defaults execute after restore and current-name dump replay.
  EXECUTE checks use current callers, distinguishing folded literal NULL from
  demanded row-dependent NULL calls.
  `hstore_jsonb_helpers_test.go` applies the JSONB metadata/transport contract;
  independent strict/cast strings and loose exact-numeric normalization drive
  projection, equality predicates, privileges and extension-owned cast lifecycle.
  The reusable family selector is `RUN='^TestReferenceHstore'`.
- `ltree_helpers_test.go` owns exact scalar-text/raw transport and metadata
  assertions. The family covers empty versus NULL paths, UTF8/C label admission,
  count/length diagnostic precedence, held binds, statement atomicity and native
  replacement with retained readers. All three scalar roles are consumers of the same
  extension during lifecycle tests. The reusable selector is `RUN='^TestReferenceLtree'`.

Client quirks:

- lib/pq returns textual values as `[]byte` with ScanType `any`; native returns
  strings. Assert raw cells before normalization.
- lib/pq reports an empty `DatabaseTypeName` for types missing from its OID table
  (MACADDR8, multiranges, extension types such as `cube`); native reports the
  concrete name. Assert the raw codec independently of the name.
- A typed-nil `[]byte` bound to FLOAT8 or to an inferred TEXT parameter is empty
  input natively but NULL through lib/pq; do not use it as a shared NULL witness.
- lib/pq fails on an oversized ErrorResponse; obtain such a server diagnostic on
  PostgreSQL with `pg_input_error_info`.
- A pre-canceled lib/pq read can race database/sql's cancellation watcher: check
  the available metadata and ordered prefix, accept `context.Canceled` or
  `driver.ErrBadConn`, and require full rows only on clean EOF. Native
  pre-cancellation returns `context.Canceled` without a cursor. Cache
  `ColumnTypes` while the cursor is open to check descriptors after EOF/Close.

Rules:

- Catalog identity probes first read the backend's actual identities from single
  catalogs, then bound every joined catalog input by them (derived operands before
  the join). Never use LIMIT, cached OIDs or expectation-derived catalog data.
  Compare allocated OIDs through relationships, never numerically across backends.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.
- Cube spatial helpers assert raw BOOL/FLOAT8 values, metadata and complete
  diagnostics before parity. Native controls retain actual TEMP identities to
  force remapping and replay the emitted SQL on PostgreSQL. Excluded Restore
  INSERT producers fail with ordinary control errors, not SQL codec diagnostics.
- `earthIdentityChecks` is shared by Earth Metadata, Controls and
  PrerequisiteRemoval; rerun all three after changing it. Its selected inventory
  includes `cube_distance`, the six earth coordinate/conversion routines and
  `geo_distance(point,point)` with its operator backing/commutator/membership links.
- Earth coordinate principal roundtrips reject nonfinite outputs before applying
  their finite tolerance or circular longitude comparison.
  `TestReferenceEarthCoordinateRenamedDiagnostics` checks complete current-name
  body errors and execute stages through direct/fresh/held calls, pre-rename
  stored views/defaults, native Restore and emitted-SQL PostgreSQL replay.
- Earth-point shared scenarios check raw FLOAT8 statute miles and fixed independent
  rounded witnesses, complete diagnostics and stored consumers through native
  controls. Raw precision additionally compares native/oracle direct/fresh/held
  results within two ULP, with exact NULL/nonfinite categories and zero signs.
  Reuse the family selector `RUN='^TestReferenceEarthPoint'` in both modes.

### Family selectors

- `cube` (scalar/codecs, coordinate extraction and spatial predicates/distances): `RUN='^TestReferenceCube'`
- <a id="earth-domain-contract"></a>`earth` domain, coordinates and conversions: `RUN='^TestReferenceEarth'`
- `query_int`: `RUN='^TestReferenceQueryInt'`
- `seg`: `RUN='^TestReferenceSeg'`
- `lquery` declarations, canonical text codecs, assignment, metadata and controls:
  `RUN='^TestReference(Lquery|Ltxtquery)'`. Fixtures preserve explicit non-star quantifiers,
  normalize modifier flags and quantifier bounds, and assert complete diagnostics
  independently of native formatting. Controls retain rows and metadata across
  replacement and replay actual emitted SQL on PostgreSQL.

### Cube fixtures

Cube scenarios share `cubeScenario`, `fixture.CubeSelect` and the metadata/control helpers.
Use `fixture.CubeSelect` only for actual cube columns; Boolean and integer comparison
results use ordinary fixture projections. Assert raw cells and metadata before
normalization, including empty and NULL-only results.

`cube_constructor_helpers_test.go` owns the shared constructor scenario and
mixed cube/integer/Boolean result adapter. `cubeConstructorScenario` borrows and
closes one connection per backend. `cubeConstructorSelect` checks cube raw cells
and metadata without replacing the ordinary scalar-column assertions.
Expected array and point text is specified independently of native formatting.
Transformation scenarios reuse this adapter for scalar cube results, including
empty and NULL-only projections. Their stored calls retain the same installed
carrier through same-target and allocator-colliding fresh-driver replacement.

`cube_coordinate_helpers_test.go` checks FLOAT8 metadata and exact extraction
bits before parity, including signed zero and NaN classification. Coordinate
scenarios retain shared direct/prepared/held execution and real native control
reminting; stored consumers execute after replay rather than merely preserving
previously extracted floats.
Independent coordinate member owners use the public named-owner Dump/Restore
metadata seam natively and ordinary member-owner SQL on PostgreSQL. Match all
four procedure and two operator signatures exactly, preserve other member owners,
and provision the named role before independent-destination replay. PostgreSQL
ignores native ownership comments, so assert transferred member owners after
replaying the actual emitted SQL on a pinned, authorization-cleaned connection.

Stored-expression fixtures retain actual procedure/type identity rather than
spelling or allocated-OID goldens. Import authority belongs only to private
migration candidates; remapped expressions must validate against the published
catalog without retained import permission.

Constructor controls preserve logical ownership across replacement and actual
dump replay. PostgreSQL replay uses one pinned connection, resets session/role
authorization, and cleans up created objects and roles before returning it.
The Earth retained-TEMP fixture retains real TEMP objects until an observed
identity occupies the source earth type's OID; replacement must remint the
imported type without changing that live object's or namespace's identity.
Fixtures do not depend on prerequisite member counts.

The comparison graph has 18 direct extension members: the scalar, nine
strict immutable procedures (text input/output, six comparisons and `cube_cmp`),
six comparison operators, and the default B-tree `cube_ops` class and family.
The complete installed manifest also includes eight constructor/dimension/point
routines, four coordinate routines, two coordinate operators, four scalar
transformation routines and six spatial routine/operator pairs, for 48 direct members. The
associated `_cube` catalog identity remains lookup-only and internally dependent,
not array storage. Equality is merge-capable, not HASHES; there is no cube
hash-index class. Native receive/send metadata stays zero. Constructor and spatial
fixtures cover their own overloads; GiST, cube arrays and binary COPY remain
outside this fixture contract.

Catalog probes relate exact qualified procedure/operator signatures and both
argument identities, without fixed dynamic OIDs. Keep probes bounded to a single
catalog or derived identities rather than expanding catalog products.

Control scenarios retain readers and compare same/fresh-driver results, settings,
ownership and future writes. Forced remapping requires measured retained TEMP
collisions, not permanent allocator spacers. Replay the actual emitted SQL on a
dedicated PostgreSQL connection; refresh materialized views explicitly because
native snapshot comments do not populate them.

Declaration-window witnesses distinguish written expressions from inherited or
omitted defaults. Preserve both valid inherited replay and malformed-window
rejection, and prove inaccessible namespace authority with real denied lookups
rather than grants or owner substitutions. See the
[stored-expression contract](../../internal/engine/stored/README.md#recipe-rules).
