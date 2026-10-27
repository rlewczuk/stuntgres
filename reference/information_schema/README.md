# reference/information_schema

`information_schema` object views: `views` (including builtin view definitions,
shared with `pg_views`), `view_table_usage`, `triggers`, `routines`, `parameters`,
`routine_sequence_usage`, `routine_routine_usage`, `check_constraint_routine_usage`, `check_constraints`, `routine_privileges`, `role_routine_grants`, `tables`,
`columns`, `column_column_usage`, `column_domain_usage`, `column_options`,
`character_sets`, `collations`, `schemata`, `domains`, `user_defined_types`,
`domain_constraints`, `referential_constraints`, `table_constraints`, `transforms` and `sequences`. Other grant, key, constraint and SQL metadata
views are in [reference/catalog_privileges](../catalog_privileges/README.md); the
other catalog families are listed in [reference/catalogs](../catalogs/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) and the shared catalog rules in
[reference/catalogs](../catalogs/README.md#testing). `TestMain` is
`fixture.Main(m, fixture.WithLegacyCryptoProvider())`, as in every catalog
package. The column inventory (`info_columns_provider_helpers_test.go`) reads the
other families' column vectors from `reference/fixture/catalog_columns.go`.
Protected role catalogs retain exact real-table or view classification;
`pg_authid` carries the complete twelve-column source facts, and current
authority filters role-catalog metadata on direct and retained consumers.
The inventory also includes PUBLIC-readable `pg_auth_members` as a real catalog
table with its exact seven ordered membership columns.
The PUBLIC-readable `pg_policy` entry retains all eight ordered physical columns
and real-table classification; its definitions are not filtered by RLS applicability.
The shared `pg_subscription` catalog contributes all eighteen ordered columns
for the administrator inventory, including `subconninfo`; PUBLIC column grants
do not hide its administrator-visible metadata or turn the relation into a view.

Every query of these views builds the whole view before filtering, so an
inventory of builtin or installed objects (view definitions per display mode,
parameter names, extension members) is one batched query per consumer in one
scenario, never one scenario or query per object.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Information schema: `RUN='^TestReferenceInfo'` (shared with `reference/catalog_privileges`)
- Builtin `pg_views`/`pg_tables` definitions: `RUN='^TestReferencePGTableViewsBuiltins$'`
- Collation inquiry: `RUN='^TestReferenceInfoCollations'`
- CHECK routine usage: `RUN='^TestReferenceInfoCheckConstraintRoutineUsage'`
- CHECK definitions: `RUN='^TestReferenceInfoCheckConstraints'`
- Routine dependency usage: `RUN='^TestReferenceInfoRoutineRoutineUsage'`
- Schema inquiry: `RUN='^TestReferenceInfoSchemata'`
- Table constraints: `RUN='^TestReferenceInfoTableConstraints'`
- Foreign-column options: `RUN='^TestReferenceInfoColumnOptions'`
- Transform metadata: `RUN='^TestReferenceInfoTransforms'`
- Standalone composite inquiry: `RUN='^TestReferenceInfoUserDefinedTypes'`
- Column domain usage: `RUN='^TestReferenceInfoColumnDomainUsage'`
- Trigger metadata: `RUN='^TestReferenceInfoTriggers'`
- Routine parameter inquiry: `RUN='^TestReferenceInfoParameters'`
- Referential constraints: `RUN='^TestReferenceInfoReferentialConstraints'`

Parameter scenarios prescribe the complete nullable 32-domain projection,
declared formal order/names/modes, type identity, owner-visible expression
presentation and EXECUTE-only redaction. SQL PREPARE does not participate.
Direct, fresh-prepared and held consumers cover role/catalog publication,
typed empty results, complete diagnostics and native control round trips.

CHECK routine-usage scenarios assert complete six-column identity bags and
source-owned identifier domains over actual table/domain CHECK semantic edges.
They distinguish routine-owner visibility from table/schema/EXECUTE grants,
direct edges from called bodies, and individual constraint identity from final
DISTINCT. Held reads cover catalog and role publication; native retained eager
rows survive Reset. Companion catalogs carry definition and validation facts.

Routine dependency scenarios assert exact six-column direct function/procedure
pairs from stored bodies and defaults, with both endpoints' specific identities.
Quoted and PL body text, pinned callees and aggregate execution-support captures
do not invent rows. Referenced-owner enabled roles alone govern disclosure.
Direct, fresh-prepared and held reads cover lifecycle and role publication;
native controls retain old rows and use a separate recorded destination pool
for independent-driver Restore assertions.

Trigger scenarios prescribe seventeen-column domain/driver shape, independent
event/timing/orientation ranks, TRUNCATE filtering, current-identity presentation
and owner-only WHEN disclosure independently.
Physical held consumers observe DDL, role, ACL and presentation-setting changes.
The presentation family includes a genuine Boolean-domain WHEN coercion and
retains one full projection across path/name and standard-string transitions.
Metadata reads
never invoke trigger bodies; genuine firing and native control roundtrips are
separate assertions over the same real definitions.

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

Foreign-column option scenarios assert exact six-domain rows and NAME/VARCHAR
transport through direct, fresh-prepared and held reads. Real stable attribute
identities keep options attached across column gaps and renames. Owner and
four-column-privilege visibility is distinct from mapping-value disclosure:
authorized readers receive actual values, denied readers receive no rows.
Native control phases retain old captures and roundtrip real option/ACL metadata.
Independent-driver control reads use their own middleware-wrapped pool and
recorder, so raw transport and recorded rows share the destination database.
Builtin self-description is asserted before foreign execution metadata is seeded.

Column domain usage checks the complete seven-column NAME projection and
source-owned identifier domains, direct declared-domain edges for actual
relations, enabled domain-owner visibility independent of ACL USAGE, held
definition/ownership refresh, complete diagnostics and native control retention.
Expression casts, domain ancestry and array elements do not fabricate usages.

### Referential constraints

The shared referential-constraint helpers prescribe complete nine-cell bags,
logical domains, raw/adapter metadata and direct/new/held consumers. Scenarios
exercise actual FK actions and supporting-index identity, duplicate-name bags,
standalone-index NULLs, exact referencing-table/column authority, publication,
structured diagnostics and explicit native controls. Builtin view consumers
independently specify all six search-path/quoting presentations.

### Table constraints

The shared table-constraint scenarios prescribe complete eleven-column bags,
domain/base metadata, nullable UNIQUE null-distinctness and relation-distinguished
duplicate names. Direct, fresh-prepared and held consumers cover actual constraint
families, validation, partition/inheritance and session-local temporary objects.
Visibility separates ownership and qualifying table/column privileges from
SELECT, MAINTAIN and schema USAGE, including grant revocation and RLS boundaries.
Constraint/table rename, schema/owner changes and transactional rename rollback
refresh held readers. Native control phases preserve eager rows and logical
metadata across Reset and same-engine or independent-driver Dump/Restore.

### Check constraints

CHECK-definition scenarios prescribe the exact nullable four-column projection
and logical identifier/character-data domains. They preserve duplicate tuple
multiplicity across actual table/domain CHECK and NOT NULL definitions, use only
enabled owner authority, and distinguish a NULL-valued CHECK expression from a
SQL-NULL result. Direct, fresh and held consumers cover definition, privilege,
presentation and table/domain transaction rollback; companion catalogs witness
identities, validation and routine dependencies. Native control phases assert
detached rows/types, fresh-target restore and failed replacement atomicity.

Transform metadata scenarios assert the complete empty eight-domain view and
its NAME/VARCHAR result description, including singleton NULL extension.
The supported transform inventory is empty; ordinary type/routine/extension
lifecycle does not invent transform objects. Owner reads and ordinary
grant/revoke/schema-USAGE admission use direct, fresh-prepared and held queries.
Role switches restore the session owner with `SET ROLE NONE` before ACL cleanup.
Cached column descriptors survive EOF, Close and authority changes; native
eager rows additionally remain consumable after those changes.
Complete diagnostics distinguish `42501` (`permission denied for view transforms`)
from schema denial, semantic errors and UNION write rejection.
Native controls retain old metadata and restore into independent driver authority.
All builtin column and definition inventories include the view.

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
The provider inventory includes all nine documented `pg_shadow` fields and its
view classification without relaxing the complete native relation bag. Direct,
fresh-prepared and held metadata reads hide these fields from an ordinary role,
expose them under genuine read-all membership, and hide them again on revocation.

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
