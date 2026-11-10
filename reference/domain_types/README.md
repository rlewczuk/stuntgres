# reference/domain_types

Dual-mode scenarios for domains, type identity/lifecycle and type inquiry.
Other scalar families belong to [types](../types/README.md#testing);
installed types to [extension_types](../extension_types/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`;
these are shared across families:
- `domain_helpers_test.go` owns `domainReferenceScenario`: one pinned notice
  session per backend with schema cleanup registered before borrowing, so handles
  close first. Its consumers share the `^TestReferenceDomain` selector.

Client quirks:

- lib/pq returns textual values as `[]byte` with ScanType `any`; native returns
  strings. Assert raw cells before normalization.
- Unbounded VARCHAR-based columns report Length `(MaxInt64,true)` natively and
  `(-5,true)` through lib/pq.
- A typed-nil `[]byte` bound to TEXT is SQL NULL on both backends; nonnil empty
  bytes remain empty input. FLOAT8 retains its separate native empty-input
  versus lib/pq NULL transport policy.
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
- Compare temporal defaults and relative inputs with `CURRENT_TIMESTAMP` or
  `CURRENT_DATE` captured in the same transaction, never with a wall-clock
  tolerance.
- While a DROP waits for its AccessExclusiveLock, observe it through catalog
  queries; a new reader of the table would queue behind the DROP.
- A new or changed builtin type, operator, opclass or extension also changes the
  inventories in reference/catalogs, reference/catalog_objects and
  reference/extensions: rerun `^TestReferencePGType`,
  `^TestReferencePGOperator`, `^TestReferencePGOpclass` and, for extension types,
  `^TestReferenceAvailableExtensionsFiltering$`.

### Family selectors

Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- Domains: `RUN='^TestReferenceDomain'`
- Type inquiry: `RUN='^TestReferenceTypeDescribe'`
- Type identity: `RUN='^TestReferenceTypeLifecycle'`
- Transactional types: `RUN='^TestReferenceTransactionalTypes'`
- Information-schema domains: `RUN='^TestReferenceInfo(CardinalNumber|CharacterData|SQLIdentifier|TimeStamp|YesOrNo)'`
