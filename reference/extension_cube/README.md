# reference/extension_cube

Dual-mode tests for the `cube` and `earth` (earthdistance) extension types:
cube scalar codecs, constructors, coordinates, transformations and spatial
predicates/distances, and the earth domain, coordinates and point distances,
with their native Dump/Reset/Restore controls. Other extension types are in
[reference/extension_types](../extension_types/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. The lib/pq client quirks and catalog-probe rules of
[reference/extension_types](../extension_types/README.md#testing) apply here too.

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
