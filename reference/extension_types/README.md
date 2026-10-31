# reference/extension_types

Dual-mode tests for extension types: `seg`, `query_int`, `lquery`, `ltree`, `ltxtquery`,
`ean13`, `isbn`, `isbn13`, `ismn`, `ismn13`, `issn`, `issn13`, `upc` and `lo`.
`hstore` is in [reference/extension_hstore](../extension_hstore/README.md#testing);
`cube` and `earth` are in [reference/extension_cube](../extension_cube/README.md#testing).

Other type families: [reference/domain_types](../domain_types/README.md#testing)
(domains, `information_schema` domains, type identity lifecycle and
transactional types);
[reference/geometry_network](../geometry_network/README.md#testing) (geometry
and network address types);
[reference/ranges_text_search](../ranges_text_search/README.md#testing) (ranges,
multiranges, text search and UUID);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

`TestReferenceExtensionOwnershipPrerequisites` retains actual cube/earthdistance
prerequisites, distinct nominal types and extension/schema dependency restrictions.
Its cohort scenario lawfully detaches genuine old objects, retires their original
extensions and installs a new same-named cube: earth must still use the original
cube type, not the new installation's same-spelled type. Shared SQL precedes
native same/independent Dump/Reset/Restore and no-phantom assertions. Each
destination uses its own recorded SQL pool and recorder, separate from its raw
control pool, so consumed-value and cursor-close evidence comes from that engine.
Run the
definition with both `make check RUN='^TestReferenceExtensionOwnershipPrerequisites$'`
and the same command with `REFERENCE=1`.

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
  Held scalar-literal ownership witnesses reuse `issnOutput` for the documented
  release-specific short display while retaining complete native marked issue
  values across membership mutation, type generations and native controls.
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
  Opt-in lo_manage coverage owns direct/prepared/held BEFORE and AFTER row cleanup,
  exact firing diagnostics, transaction/statement rollback, role authority,
  target-column identities, shared-reference hazards and native trigger/control replay.
  Its reusable selector is `RUN='^TestReferenceLoManage'`.
- `ltree_helpers_test.go` owns exact scalar-text/raw transport and metadata
  assertions. The family covers empty versus NULL paths, UTF8/C label admission,
  count/length diagnostic precedence, held binds, statement atomicity and native
  replacement with retained readers. All three scalar roles are consumers of the same
  extension during lifecycle tests. The reusable selector is `RUN='^TestReferenceLtree'`.
  Comparison fixtures independently specify label traversal order, strict NULLs,
  equality consumers and typed B-tree/hash key mutation. Exact catalog and
  diagnostic records accompany retained stored comparisons through replacement,
  failed Restore and actual emitted-SQL replay. Populated stored sorting uses a
  limiting inner order to distinguish comparator execution from empty/NULL-only
  binding and from an outer result order. Comparison helpers share
  `RUN='^TestReferenceLtreeComparison'`.
- `ltree_arrays_helpers_test.go` owns mixed BOOL/installed-text array result
  contracts, including both nullability values for computed and declared stored
  columns. Retained descriptors are checked after consumption/close and native
  replacement, including empty readers. Array overloads, matching, diagnostics,
  prepared authority and actual emitted-SQL replay share `RUN='^TestReferenceLtreeArray'`.
- Scalar hierarchy fixtures independently specify inclusive path relationships,
  Boolean metadata, overload/privilege diagnostics and restored stored consumers.
  Both direct `ltree_isparent`/`ltree_risparent` calls and `@>`/`<@` operators
  retain their existing direct, prepared and held witnesses. The ownership
  scenarios in `reference/extension_ownership_ltree` additionally retain genuine detached and
  foreign members after origin retirement through destination-recorded controls.
  The reusable selector is `RUN='^TestReferenceLtreeHierarchy'`.
 - Path utilities share `RUN='^TestReferenceLtreePath'`: independently specified
   slices, whole-label index, conversion, proper-ancestor scalar/array LCA and the
   three typed concatenations use direct, fresh and held routes. Metadata and full
   diagnostics distinguish empty paths from SQL NULL and operation failures from
   input conversion. Stored nonconstant consumers and repeated LCA arguments survive
   relocated/renamed providers, native replacement and actual emitted-SQL replay.
- Scalar positional-match fixtures reuse array BOOL metadata helpers and independently
  specify ordered matching, query alternatives, diagnostics and stored-consumer
  replacement. Controls install the provider directly in its schema and rename
  that namespace after storing view, CHECK and DEFAULT expressions; direct and
  prepared reads assert the stored results before renaming it back for native
  replacement and emitted-SQL replay.
  Mutation witnesses retain an UPDATE with TEXT parameters and cast only after a
  row-dependent CASE. Swapped malformed branches exercise either row traversal
  order; successful candidates change their original values. Each runtime-cast
  failure asserts the complete diagnostic with empty Context and unchanged rows,
  then the same held UPDATE recovers with exactly two independently expected
  changes. Typed-parameter rejection remains a separate input-boundary assertion.
  Their reusable selector is `RUN='^TestReferenceLtreeLquery'`.
- Scalar label-query fixtures retain mixed formal roles through Boolean matching,
  diagnostics, prepared reuse, presentation changes and native replacement.
  Presentation witnesses check public view definitions and independently expected
  first-dump cast chains before and after two native replacement cycles, then
  replay the actual emitted SQL on PostgreSQL. Row-dependent TEXT conversion
  failures and domain NOT NULL diagnostics distinguish retained conversions and
  domain checks from result-only equality; successful reuse restores the seed.
  The reusable selector is `RUN='^TestReferenceLtreeLtxtquery'`.

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

### Family selectors

- `query_int`: `RUN='^TestReferenceQueryInt'`
- `seg`: `RUN='^TestReferenceSeg'`
- `lquery` declarations, canonical text codecs, assignment, metadata and controls:
  `RUN='^TestReference(Lquery|Ltxtquery)'`. Fixtures preserve explicit non-star quantifiers,
  normalize modifier flags and quantifier bounds, and assert complete diagnostics
  independently of native formatting. Controls retain rows and metadata across
  replacement and replay actual emitted SQL on PostgreSQL.
