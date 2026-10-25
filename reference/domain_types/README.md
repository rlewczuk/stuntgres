# reference/domain_types

Dual-mode tests for domains (CREATE/ALTER DOMAIN defaults, nullability, CHECK
and identity lifecycle); the `information_schema` domains; the type identity
lifecycle of domains, composites and installed extension types; and
transactional domain/composite/type lifecycle.

Other type families:
[reference/geometry_network](../geometry_network/README.md#testing) (geometry
and network address types);
[reference/ranges_text_search](../ranges_text_search/README.md#testing) (ranges,
multiranges, text search and UUID);
[reference/extension_types](../extension_types/README.md#testing) (`cube`,
`earth`, ISN types, `query_int` and `lo`);
[reference/types](../types/README.md#testing) (composite types, enums, `citext`,
ACL values, TID, BOOLEAN and comparisons).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`. Each family keeps its helpers in `<family>_helpers_test.go`;
these are shared across families:
- `domain_helpers_test.go`: the shared domain fixture `domainReferenceScenario`
  (a pinned notice session per backend with schema `domain_s` and a one-row
  `public.one`; schema cleanup is registered before borrowing so handles close
  first). Most `domain_*` files use it; after changing it, run `^TestReferenceDomain`.

Client quirks:

- lib/pq returns textual values as `[]byte` with ScanType `any`; native returns
  strings. Assert raw cells before normalization.
- Unbounded VARCHAR-based columns report Length `(MaxInt64,true)` natively and
  `(-5,true)` through lib/pq.
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

### Type identity lifecycle

`type_lifecycle_*` uses shared backend scenarios for type/provider identity,
reciprocal companions, typed values/NULL/raw metadata, independent diagnostics,
dependency closure, live authorization and held-occurrence lifetime. Registered
I/O signature probes use public regprocedure identity and admitted pg_proc columns.
Installed EAN13 scenarios retain leading-zero, marked and NULL values across
identity ALTER, held casts/INSERTs/raw readers and same/fresh-driver controls;
base/array ownership stays independent of extension I/O routine authority.
Domains retain pg_type I/O identities and genuine base/CHECK providers, separately
from procedure inventory. Explicit companion DROP follows its actual extension
owner where applicable.

Native controls remain explicit phases in the shared scenarios. Current-name
mixed-owner graphs and nominal-only domain recipes must replay under trusted
private authority without changing owners or granting schema privileges. Domain
default scenarios distinguish captured typdefault text from current executable
recipes. Protection's shared range join independently specifies three native
integer/numeric relationships and six PostgreSQL relationships; it does not create
absent temporal companions.

Family selector: `RUN='^TestReferenceTypeLifecycle'`.

`transactional_types_*` exercises the same domain/composite/type lifecycle in
autocommit, retained messages, SQL BEGIN and Go BeginTx, through direct and genuine
held handles. Scenarios assert owner-private definitions and data, selected domain
scans, composite layouts and logical keys, savepoints, prepared freshness and native
replacement/Dump controls. Identity and composite-identity scheduling use canonical
observed queues, cancellation and joined workers; generic OWNER remains separate.
Actual simple/extended protocol coverage belongs in
[reference/sessions](../sessions/README.md#testing).

### Family selectors

- Domains (all): `RUN='^TestReferenceDomain'`
- Domain defaults: `RUN='^TestReferenceDomainDefault'`
- Domain CHECK: `RUN='^TestReferenceDomainCheck'`
- ALTER DOMAIN: `RUN='^TestReferenceDomainAlter'`
- Domain identity lifecycle: `RUN='^TestReferenceDomainIdentity'`
- Transactional domain/composite/type lifecycle: `RUN='^TestReferenceTransactionalTypes'`
- `information_schema` domains: `RUN='^TestReferenceInfo(CardinalNumber|CharacterData|SQLIdentifier|TimeStamp|YesOrNo)'`
