# reference/information_schema

`information_schema` object views: `views` (including builtin view definitions,
shared with `pg_views`), `view_table_usage`, `routines`,
`routine_sequence_usage`, `check_constraint_routine_usage`, `routine_privileges`, `role_routine_grants`, `tables`,
`columns`, `column_column_usage`, `character_sets`, `collations`, `schemata`, `domains`,
`domain_constraints` and `sequences`. Grant, key, constraint and SQL metadata
views are in [reference/catalog_privileges](../catalog_privileges/README.md); the
other catalog families are listed in [reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package. The column inventory (`info_columns_provider_helpers_test.go`) reads the
other families' column vectors from `reference/fixture/catalog_columns.go`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Information schema: `RUN='^TestReferenceInfo'` (shared with `reference/catalog_privileges`)
- Builtin `pg_views`/`pg_tables` definitions: `RUN='^TestReferencePGTableViewsBuiltins$'`
- Collation inquiry: `RUN='^TestReferenceInfoCollations'`
- CHECK routine usage: `RUN='^TestReferenceInfoCheckConstraintRoutineUsage'`
- Schema inquiry: `RUN='^TestReferenceInfoSchemata'`

CHECK routine-usage scenarios assert complete six-column identity bags and
source-owned identifier domains over actual table/domain CHECK semantic edges.
They distinguish routine-owner visibility from table/schema/EXECUTE grants,
direct edges from called bodies, and individual constraint identity from final
DISTINCT. Held reads cover catalog and role publication; native retained eager
rows survive Reset. Companion catalogs carry definition and validation facts.

Collation inquiry asserts the exact four-column projection, domain/base
relationships and encoding applicability through existing catalogs. Shared
direct, fresh-prepared and held reads cover empty/NULL boundaries, collation
lifecycle and complete diagnostics. Actual owner and reader sessions see
private-schema collation metadata without schema USAGE, while ordinary
qualified relation lookup still denies access. Native retained eager rows
preserve their original values and metadata across catalog mutations.

Schema inquiry uses independently prescribed seven-column identities and nullable
domain metadata, including raw NAME transport and the four SQL NULL tail fields.
Direct, fresh-prepared and held consumers cover namespace creation, rename,
ownership transfer, removal and current role/ACL publication. Visibility tests
separate immediately usable ownership and schema CREATE/USAGE from unrelated
object grants and search-path names. Empty and nil predicates preserve the
description; complete binder diagnostics are checked before filtering.
Native control phases retain eager rows and metadata across replacement and
exercise atomic Dump/Reset/Restore, independent-driver ownership and cancellation.
Builtin column inventories and all view-definition consumers include this view.
See the [producer contract](../../internal/engine/pgcatalog/README.md#schema-inquiry).


### Domain constraints

The seven `TestReferenceInfoDomainConstraints` definitions prescribe the exact
released eight columns, nullable logical sql_identifier/yes_or_no identity,
NAME/VARCHAR(3) driver metadata/raw transport and row/array relationships.
Actual own CHECK/NOT NULL rows include genuine bootstrap recipes. Duplicate
constraint names retain distinct domains; table checks, inherited definitions
and unconstrained domains do not create rows. Empty/false/NULL/nil-parameter
predicates keep the complete description through direct/prepared execution,
EOF and Close.

Visibility covers enabled owner roles, direct/inherited/PUBLIC type USAGE,
grant options, NOINHERIT/SET-only membership, actual SET ROLE, superuser and
owner/ACL refresh on the same held statement. Schema USAGE is neither sufficient
nor necessary for this view. Lifecycle assertions check full bags after
ADD/NOT VALID/VALIDATE/DROP, SET/DROP NOT NULL, rename/move/drop and failures.
All projected flags remain NO; validation is separately witnessed through
pg_constraint. Complete English diagnostics and failure stages include missing
columns/constraints, duplicates, ownership and stored-data validation.

The controls definition runs shared SQL acceptance in both modes, then native
Dump/Reset/Restore, fresh-driver restore, retained eager data/types and failed
or canceled restore/read atomicity. PostgreSQL transactional ALTER followed by
rollback is distinct from native's existing explicit-transaction capability
diagnostic; this view does not widen DDL admission.
See the [projection and visibility contract](../../internal/engine/pgcatalog/README.md#domain-constraints).

```sh
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceInfoDomainConstraints(Shape|Population|Visibility|Lifecycle|Diagnostics|NativeControls|FeatureSmoke)$'
make check RUN='^TestInfoDomainConstraints(Producer|Capture|Cancellation)$'
make check RUN='^TestCatalogSourceProducerCoverage$'
make check RUN='^TestReferenceInfoDomainConstraintsFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceInfoDomainConstraintsFeatureSmoke$'
```

Expected smoke behavior: exactly three initial full identity rows (two same-name
CHECKs and one own NOT NULL), then a fourth NOT VALID CHECK row; validation
leaves the bag unchanged and selected DROP removes only its actual row. The
same held consumer omits a domain after PUBLIC USAGE revocation and includes it
after direct type USAGE without schema USAGE. Typed empty NULL/nil predicates
and full42703/Position8 missing-column diagnostics finish the scenario.
These are proof commands and expected observations, not a passing-check claim.

### Information-schema sequence definitions

`info_sequences_helpers_test.go` owns the shared scenario, exact option vectors,
raw-codec and metadata assertions, safe role/schema cleanup and direct/new/held
consumers. The family covers definition, authority, object-boundary, lifecycle,
transaction, typed-NULL and diagnostic contracts using independent expectations.
Retained eager-row ownership is asserted only on native cursors.
See [producer ownership](../../internal/engine/pgcatalog/README.md#information-schema-sequence-definitions).

- Sequence definitions: `RUN='^TestReferenceInfoSequences'`
- Domain definitions: `RUN='^TestReferenceInfoDomains'`

### Column metadata

`info_columns_helpers_test.go` owns the ordered domain/type vectors, complete
independent row assembly, raw/descriptor checks and direct/new/held consumers.
Family-local provider vectors enumerate actual represented fields and stable
attributes; the common provider/self cases assert complete 44-cell rows.
Foreign capability uses genuine installed handlers and explicit native public
interface phases without external scans. Native controls replay actual Dump
bytes, preserve old eager rows and roundtrip explicit raw ACLs, generated and
identity data, privilege inquiries and the reader's filtered column metadata.

- Column metadata: `RUN='^TestReferenceInfoColumns'`

### Table metadata

`info_tables_helpers_test.go` owns exact twelve-column rows, logical domains,
raw/adapter metadata and direct/fresh/held consumers. Scenarios assert represented
object kinds, typed identities, physical TEMP scope, effective privilege
visibility and metadata refresh without executing view bodies or foreign scans.
Native retained rows and Dump/Reset/Restore are explicit public-control phases.
Builtin definition consumers retain independent PostgreSQL display expectations.

- Table metadata: `RUN='^TestReferenceInfoTables'`

### Routine grant metadata

`info_role_routine_grants_helpers_test.go` owns the shared routine ACL scenario,
exact origin bags, independently resolved routine identities, domain/adapter
metadata and direct/fresh/held reads. `info_routine_privileges_helpers_test.go`
selects the PUBLIC-visible source with independent role/schema setup. The family
distinguishes PUBLIC origins from enabled grantor/grantee visibility, checks named
grantee grantability, live ACL/membership publication, represented routine kinds,
lifecycle frontiers and complete structured diagnostics.

- Routine ACL metadata: `RUN='^TestReferenceInfo(RoleRoutineGrants|RoutinePrivileges)'`

### View definition metadata

`info_views_helpers_test.go` owns paired scenarios, exact ten-column domain and
adapter metadata, role/schema cleanup and direct/new/held consumers. The family
checks definition disclosure, physical TEMP isolation, options, lifecycle
atomicity and structural capabilities without body execution. Builtin definition
expectations are literal independent PostgreSQL observations for default,
information-schema and exact-shadow paths, with quote_all_identifiers both off
and on. `pg_views` uses the same consumer expectations.
Foreign-handler capability scenarios live with the real foreign-source fixture
in `reference/tables`; they share the same family selector.
See [producer ownership](../../internal/engine/pgcatalog/README.md#information-schema-views).

- View definitions: `RUN='^TestReferenceInfoViews'`
- Routine metadata: `RUN='^TestReferenceInfoRoutines'`. Full-row witnesses are
  supplemented by test-owned released builtin identity/source/language/security
  facts, queried in serial batches under the ordinary scenario deadline. Extension
  witnesses include citext's INTERNAL I/O and C comparison declarations with
  independently specified source symbols.
  `RUN='^TestReferenceInfoRoutinesCastInventory$'` selects the added 99 real
  cast/multirange declarations plus three installed citext conversions. It
  checks exact released display facts and SQL-NULL argument names using fixed
  builtin OIDs or independently resolved installed signatures, without adding
  execution providers or a Cartesian runtime matrix.
