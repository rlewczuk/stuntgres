# reference/extension_cube

Dual-mode scenarios for the `cube` and `earth` extension types.
Other extension types belong to [extension_types](../extension_types/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. The lib/pq client quirks and catalog-probe rules of
[reference/extension_types](../extension_types/README.md#testing) apply here too.

Shared fixtures assert raw cells, metadata and complete diagnostics before
normalization.

### Family selectors

- Cube: `RUN='^TestReferenceCube'`
- Earth: `RUN='^TestReferenceEarth'`
- extension prerequisite closure: `RUN='^TestReferenceExtensionDependencies'`

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


PostgreSQL replay uses one pinned connection, resets session/role authorization,
and cleans up created objects and roles before returning it.


Catalog probes relate exact qualified procedure/operator signatures and both
argument identities, without fixed dynamic OIDs. Keep probes bounded to a single
catalog or derived identities rather than expanding catalog products.

Control scenarios use the shared fixture replay and retained-row ownership rules.
Forced remapping requires measured retained TEMP collisions, not permanent
allocator spacers. Refresh materialized views after PostgreSQL replay because
native snapshot comments do not populate them.

