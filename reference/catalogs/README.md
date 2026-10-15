# reference/catalogs

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### pg_language selected proof

The `TestReferencePGLanguage` definitions specify the complete native
internal-only bag `(12,internal,10,false,false,0,0,2246,NULL)` independently.
The pinned PostgreSQL18.0 oracle separately asserts the released bootstrap
`internal`, `c` and `sql` tuples; native c/sql/plpgsql absence is a capability
boundary, not an assertion about PostgreSQL's installed languages. Handler and
validator IDs remain OID metadata; no language DDL or execution is exercised.

Shape/Metadata/CatalogLinks cover all nine columns, backend raw codecs,
ScanType/nullability/length/decimal metadata before iteration on populated and
empty cursors, owner/attribute/class/table-summary relationships and
direct/new-prepared/actual-held name/OID predicates. Both backends assert
`pg_class.reltype` resolves to the pg_language REGTYPE identity. PostgreSQL also
joins `pg_type` to assert reciprocal row/array relationships; native has no
`pg_type` SQL projection, so its actual row/array descriptor ownership and links
are checked privately before and after Reset. Diagnostics and Permissions
assert complete English error records and stages, native immutable mutation/
row-lock boundaries, preserved rows/sentinel and ordinary-role PUBLIC reads.
HeldExecution adds native ordinary-context cancellation and recovery.
Lifecycle retains a populated native cursor across staged-failure/canceled/
successful Reset/Restore, checks fresh held queries, and replays an actual
native user dump on PostgreSQL with exact sentinel and language properties.
The private capture-ownership test supplements these public witnesses with
typed NULL ownership, foreign-generation rejection and independent row storage.

Run each bounded group in both modes, with an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGLanguage(Inventory|Shape|Metadata|CatalogLinks)$'
make check RUN='^TestReferencePGLanguage(Inventory|Shape|Metadata|CatalogLinks)$'
make check REFERENCE=1 RUN='^TestReferencePGLanguage(Diagnostics|Permissions|HeldExecution|Lifecycle)$'
make check RUN='^TestReferencePGLanguage(Diagnostics|Permissions|HeldExecution|Lifecycle)$'
make check RUN='^Test(PGLanguageCaptureOwnership|PgClassBuiltinIndexMetadata|TableViewSummaryBuiltinIdentity)$'
make check REFERENCE=1 RUN='^TestReferencePGTableViewsBuiltins$'
make check RUN='^TestReferencePGTableViewsBuiltins$'
make check RACE=1 RUN='^TestReferencePGLanguage(HeldExecution|Lifecycle)$'
```

Permanent feature smoke reuses these scenario helpers, not a duplicate test body:

```sh
make check REFERENCE=1 RUN='^TestReferencePGLanguageFeatureSmoke$'
make check RUN='^TestReferencePGLanguageFeatureSmoke$'
```

Expected success is quiet exit0/package `ok` with exact internal row and metadata,
held empty-to-populated rebinding, PUBLIC reads, catalog linkage, exact
missing-column diagnostic, native availability/immutability and a real
Dump/Reset/Restore sentinel round trip with a retained populated language row.
PostgreSQL independently checks its supported bootstrap subset and user-dump
replay; it does not inherit native-only control or immutability assertions.
Selected dual-mode proof and smoke supplement whole-project quickcheck.

### pg_opclass selected proof

The `TestReferencePGOpclass` scenarios specify the nine released columns,
OID/NAME/BOOL raw codecs and metadata, fixed class/method/family/input identities,
default and key-storage distinctions, PUBLIC reads, held execution, diagnostics,
index/data agreement and native Dump/Reset/Restore. Expected tuples are independent
of native lookup. Native coverage requires the complete supported 58-row bag
(29 btree and 29 hash classes); PostgreSQL permits additional implemented classes.
Array/record/range and other diagnostic-only native classes are not advertised.
These tests do not add pg_am, pg_opfamily or pg_index.indclass SQL support.

The oracle-first checkpoint confirmed the released identities using the existing
pinned PostgreSQL 18.0 UTF8/C fixture before production changes.
`JSONBIdentity/admission` creates a real JSONB hash index, checks equality,
prepared fresh parameters and INSERT/UPDATE/DELETE before reading pg_opclass.
The private supplemental `TestPGOpclassJSONBHashIdentity` checks the existing
root, actual candidate probe and exact stored class identity without new APIs.
The native identity must be hash10089, not inherited btree10088.

Run each bounded group in both modes with an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGOpclass(Inventory|Shape|Metadata|JSONBIdentity)$'
make check RUN='^TestReferencePGOpclass(Inventory|Shape|Metadata|JSONBIdentity)$'
make check REFERENCE=1 RUN='^TestReferencePGOpclass(IndexSemantics|Diagnostics)$'
make check RUN='^TestReferencePGOpclass(IndexSemantics|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation|FeatureSmoke)$'
make check RUN='^TestPGOpclassJSONBHashIdentity$'
make check RUN='^Test(IndexClassMetadata(Inventory|Providers)|PGOpclass(CaptureOwnership|IndexIdentity)|HashIndexClassProviders)$'
make check RUN='^TestReferenceIndexMaintenanceDecoratedMetadataOracle$'
make check REFERENCE=1 RUN='^TestReferenceIndexMaintenanceDecoratedMetadataOracle$'
make check RACE=1 RUN='^TestReferencePGOpclass(HeldExecution|Lifecycle|Cancellation)$'
```

Standalone permanent feature smoke:

```sh
make check REFERENCE=1 RUN='^TestReferencePGOpclassFeatureSmoke$'
make check RUN='^TestReferencePGOpclassFeatureSmoke$'
```

Expected success is quiet exit0 after exact INT4 btree/hash tuples and nine-column
metadata; default/nondefault VARCHAR, pattern and NAME keytype distinctions;
direct/held index creation, method/deparse agreement and maintained data;
complete missing-class diagnostics with no residue; native array exclusion versus
real PostgreSQL admission; and a persistent catalog view/indexed-data native
Dump/Reset/Restore roundtrip retaining old rows and refreshing held reads.
PostgreSQL lifecycle SQL asserts the independently selected supported subset of
its broader catalog. Native controls have no invented PostgreSQL API equivalent.
Smoke supplements, not replaces, selected dual-mode proof and whole quickcheck.

### Object ACL inquiry contract

The `TestReferenceObjectACL...` scenarios independently specify the single
PostgreSQL 18 signature `pg_get_acl(oid, oid, integer) -> aclitem[]`, including
formal names `classid`, `objid`, `objsubid`. They retrieve stored ACLs, not
effective permissions or synthesized defaults. Column addresses use `pg_class`
1259 and stable attribute numbers; column grants do not change the table ACL or
other columns. NULL ACL and explicit empty `{}` are distinct.
Owner/grantor labels use independently configured fixture identities: native
`postgres`, pinned PostgreSQL `stuntgres` (`fixture/lifecycle.go` username).
For example, the single PUBLIC column grant is `{=r/postgres}` natively and
`{=r/stuntgres}` on the oracle; no actual result is normalized to hide this.

Selected tests:

- `TestReferenceObjectACLSignatures`: direct/newly prepared calls, parameter
  inference, qualified/quoted/named notation, scalar composition, grouping,
  joins and stored views.
- `TestReferenceObjectACLNulls`: each strict NULL slot, inferred nil parameters,
  typed subscripting/empty expansion and demanded argument errors.
- `TestReferenceObjectACLValues`: exact column SELECT expansion, grant options,
  revokes, table/column separation, unrelated ACL preservation and explicit empty.
- `TestReferenceObjectACLAddresses`: missing/ACL-less classes, dropped/system
  attributes, signed AttrNumber wrapping, nonrelation subids and sequence ACLs.
  PostgreSQL exercises real sequence GRANT expansion; native asserts its existing
  exact 0A000 rejection and unchanged NULL, supplemented by private explicit ACL
  lookup through the existing native ACL publication seam.
- `TestReferenceObjectACLTypes`: fresh domain/enum/composite/row/array raw NULLs,
  custom type creation defaults and subsequent baseline restoration.
- `TestReferenceObjectACLDatabase`: independently asserted existing native
  `{postgres=C*/postgres}` versus pinned PostgreSQL NULL; catalog consistency
  is supplementary, not the source of expected values.
- `TestReferenceObjectACLDiagnostics`: complete error fields, direct/prepare/
  execute stages, nil outputs, named labels, scalar decorations, input ranges,
  unknown classes and a multibyte diagnostic position.
- `TestReferenceObjectACLMetadata`: raw array/scalar values, empty/NULL/explicit
  empty results and descriptors before iteration, including backend-specific
  ScanType/nullability and absence of length/precision.
  Native direct inquiry arrays are known nullable; expanded unnest ACLITEM
  scalars preserve the existing unknown computed-expression nullability.
- `TestReferenceObjectACLHeldExecution`: SAME prepared calls across grants,
  revokes, role/column rename and dropped-column gaps, raw inquiry without table
  SELECT, and retained rows/metadata across mutation.
  The observer receives real schema USAGE on PostgreSQL and temporary public
  schema ownership natively; the bootstrap-owned table is not transferred and
  the observer still receives exact 42501 for its ordinary table SELECT.
  Role RENAME is a PostgreSQL-positive/native-exact-0A000 producer boundary:
  native retains the same reader identity/spelling and both modes preserve
  preexisting Rows, fresh held grant/revoke and column identity transitions.
- `TestReferenceObjectACLLifecycle`: actual returned ACL array data and stored
  regclass inquiry recipes, supported native Reset/Dump/Restore, retained rows
  and destination role-identity remapping.
- `TestReferenceObjectACLControls`: unchanged explicit authorization ACL Dump
  rejection before any writer call and exact atomic Restore-GRANT rejection.
- `TestReferenceObjectACLCancellation`: native direct/held precancel, pinned
  observed server cancellation using the existing fixture, and SAME-owner/
  preheld inquiry continuation.
- `TestReferenceObjectACLFeatureSmoke`: standalone NULL -> column grant ->
  one-row expansion -> revoke witness, untouched table/column, typed ACL-less/
  NULL calls, returned data/recipe native roundtrip and atomic control rejection.

The database difference is intentional; see [native database catalog
policy](../../docs/DIALECT.md). Native lifecycle does not persist arbitrary
authorization grants. The lifecycle test stores function-produced ACLITEM[]
**data**, removes the source column grant, then serializes data and recipes.
It does not whitelist GRANT replay or claim persisted authorization.

Oracle-first selected groups (external deadline 900 seconds per invocation):

```sh
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Signatures|Nulls|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Values|Addresses|Types|Database)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Metadata|HeldExecution)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACL(Lifecycle|Controls|Cancellation)$'
make check REFERENCE=1 RUN='^TestReferenceObjectACLFeatureSmoke$'
```

Repeat each selector without `REFERENCE=1` after oracle discovery, with the same
completed definitions and no temporary PG-only selection. Before integration,
both selected modes must pass. Selected race proof:

```sh
make check RACE=1 RUN='^TestReferenceObjectACL(HeldExecution|Cancellation|Controls)$'
```

Expectations originate in the PostgreSQL 18 release contract and the pinned
18.0 oracle-discovery runs. They include exact diagnostics/stages, named/scalar
decorations, signed-int16 subid wrapping, raw type ACLs, last-column-revoke NULL,
VARIADIC admission and pq metadata. They are not learned from native output.
Both unfiltered modes must be rerun after implementation changes.

#### Standalone public API smoke

Besides the permanent reference smoke, save the following program as `main.go`
in a fresh temporary directory outside the checkout. From the repository root
run `go run /absolute/temporary/directory/main.go` with an external 900-second
deadline, then remove that temporary directory. Expected output is exactly
`object ACL public smoke passed`; any contract mismatch panics/exits nonzero.
The program uses only the public driver, SQL and native-control APIs.

```go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"stuntgres"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connector, err := stuntgres.NewDriver().OpenConnector("object-acl-public-smoke")
	must(err)
	db := sql.OpenDB(connector)
	defer db.Close()
	exec := func(q string) { _, err := db.ExecContext(ctx, q); must(err) }
	check := func(q, want string, valid bool) {
		var got sql.NullString
		must(db.QueryRowContext(ctx, q).Scan(&got))
		if got.Valid != valid || valid && got.String != want {
			panic(fmt.Sprintf("%s: got %#v want %q/%v", q, got, want, valid))
		}
	}
	exec("CREATE TABLE acl_smoke(a int,b int)")
	exec("INSERT INTO acl_smoke VALUES (7,9)")
	exec("CREATE TABLE acl_saved(acl aclitem[])")
	exec("CREATE VIEW acl_recipe AS SELECT pg_get_acl(1259,'acl_smoke'::regclass,1) AS acl")
	var oid int64
	must(db.QueryRowContext(ctx, "SELECT 'acl_smoke'::regclass::oid").Scan(&oid))
	stmt, err := db.PrepareContext(ctx, "SELECT pg_get_acl($1,$2,$3)")
	must(err)
	defer stmt.Close()
	held := func(want string, valid bool) {
		var got sql.NullString
		must(stmt.QueryRowContext(ctx, int64(1259), oid, int64(1)).Scan(&got))
		if got.Valid != valid || valid && got.String != want {
			panic(fmt.Sprintf("held ACL %#v want %q/%v", got, want, valid))
		}
	}
	held("", false)
	exec("GRANT SELECT(a) ON acl_smoke TO PUBLIC")
	held("{=r/postgres}", true)
	check("SELECT unnest(pg_get_acl(1259,'acl_smoke'::regclass,1))", "=r/postgres", true)
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,0)", "", false)
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,2)", "", false)
	exec("INSERT INTO acl_saved SELECT pg_get_acl(1259,'acl_smoke'::regclass,1)")
	exec("REVOKE SELECT(a) ON acl_smoke FROM PUBLIC")
	held("", false)
	must(stmt.Close())
	var dump bytes.Buffer
	must(stuntgres.Dump(ctx, db, &dump))
	must(stuntgres.Reset(ctx, db))
	must(stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())))
	check("SELECT acl FROM acl_saved", "{=r/postgres}", true)
	check("SELECT acl FROM acl_recipe", "", false)
	exec("GRANT SELECT(a) ON acl_smoke TO PUBLIC")
	var rejected bytes.Buffer
	err = stuntgres.Dump(ctx, db, &rejected)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit relation or column ACL" || rejected.Len() != 0 {
		panic(fmt.Sprintf("Dump boundary: %v/%q", err, rejected.String()))
	}
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE acl_replacement(a int); GRANT SELECT ON acl_replacement TO PUBLIC"))
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "0A000" {
		panic(fmt.Sprintf("Restore boundary: %v", err))
	}
	check("SELECT pg_get_acl(1259,'acl_smoke'::regclass,1)", "{=r/postgres}", true)
	check("SELECT a::text FROM acl_smoke", "7", true)
	check("SELECT count(*)::text FROM pg_class WHERE relname='acl_replacement' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')", "0", true)
	fmt.Println("object ACL public smoke passed")
}
```

### pg_db_role_setting selected proof

`TestReferencePGDBRoleSetting` independently specifies relation2964 and its
exact three columns: NOT NULL OID `setdatabase` and `setrole`, nullable TEXT[]
`setconfig`, attribute origins1–3, typmods-1 and array collation C950.
The catalog is an indexed, cluster-shared table in `pg_global`, not a view,
with linked row/array type identities and no fourth row `oid` column.
Direct Context/plain calls, newly prepared calls and repeated held executions
check exact empty metadata and OID0/4294967295/NULL binds; retained ColumnTypes
remain inspectable after EOF/Close. ScanTypes/nullability intentionally distinguish
native adapters from pq, rather than pretending pq exposes native-only metadata.
Shared public identity witnesses compare `pg_class.reltype` with the resolved
row REGTYPE, require positive distinct row/array identities, and equate bracket
array lookup with the initialized `_pg_db_role_setting` array name. They prescribe
relationships, not PostgreSQL's allocated OID sequence.
Exact `pg_attribute` rows now run on both backends through the existing native
attribute catalog. The actual `pg_type` reciprocal row/array catalog remains an
explicit PostgreSQL-only phase because pg_type is not a native provider.
Native component coverage independently asserts the exact descriptor attributes,
row/array owner and owning-relation links, associated array and element reciprocity.
Public native Rows metadata remains checked exactly in the shared shape scenario.
This distinction does not narrow the new readable `pg_db_role_setting` catalog.


There is no native persistent scoped-settings owner. Empty results are the
current real projection, not synthesized session defaults. Registered and
connection defaults, SET/custom placeholders, SET LOCAL, commit/rollback/savepoint
changes and ordinary-role switching must leave it empty on both physical owners.
ALTER ROLE/DATABASE SET/RESET population, nonempty scope keys/ordered stored arrays
and new-login application belong to future settings/command owners and are not
claimed by these tests. Future rows must represent actual stored defaults, never
effective SHOW values or a filter to only the current role/database.

OID parameter-input failures have the same full unnamed-portal Context on
both adapters: nonempty input is redacted as `'...'`, empty input as `''`.
Row-type relationship queries explicitly convert REGTYPE to OID before
comparison; they do not require an additional mixed-type operator.

Full diagnostics cover missing columns/relation, literal and parameter OID errors,
analysis before empty execution and successful held retries. PostgreSQL-only
catalog ACL revocation asserts42501 even for an empty source; native component
coverage publishes actual relation ACLs through CatalogStage. Both witnesses
grant SELECT on only stable attribute1 (`setdatabase`) with table SELECT absent:
that typed empty projection succeeds, another column and star fail42501, and
the same held column analysis observes grant/revoke/regrant. PostgreSQL catalog
column and table ACLs are restored on success and failure cleanup.
Public native catalog ACL SQL is not added.
Pre-canceled QueryContext, PrepareContext and held QueryContext run in the shared
body on both backends, require ordinary context.Canceled with no SQLSTATE or
published Rows/Stmt, and then reuse the same held statement successfully.
Native-only mutation/locking guards and acquired-Rows cancellation remain
explicitly distinct from PostgreSQL catalog DML/server cancellation.
Reset/Dump/Restore witnesses check failed/canceled replacement atomicity,
sentinel18 preservation, held recapture, retained empty metadata, deterministic
round trips and destination attachment isolation. No backing index/TOAST
relations or scoped-default serialization are fabricated.

```sh
make check RUN='^TestReference(PGDBRoleSetting(Shape|Metadata|SessionVisibility|Diagnostics|Controls|Smoke)|PGTableViewsBuiltins)$' REFERENCE=1
make check RUN='^TestReference(PGDBRoleSetting(Shape|Metadata|SessionVisibility|Diagnostics|Controls|Smoke)|PGTableViewsBuiltins)$'
make check RUN='^TestPGDBRoleSetting(ShapeAndOwnership|Capture|SessionDefaultsExcluded|Authorization)$'
# Permanent feature smoke, reusing the full scenario bodies:
make check RUN='^TestReferencePGDBRoleSettingSmoke$'
make check RUN='^TestReferencePGDBRoleSettingSmoke$' REFERENCE=1
```

Smoke expects exact typed empty reads/count0, shared table discovery, repeated
held execution, session/ordinary-reader exclusion, and native replacement
preserving catalog metadata and sentinel atomicity. Selected native and pinned
real proof are both required; smoke does not replace whole-project quickcheck.

### pg_collation selected proof

`TestReferencePGCollation` specifies the complete PostgreSQL18 twelve-column
metadata contract, including populated and empty direct/prepared/held results,
Context/plain routes, raw carriers, retained ColumnType descriptors and native
known versus pq unavailable nullability. The PG-only `pg_attribute` witness and
private `TestPGCollationShapeAndOwnership` independently require relation3456,
attributes1–12, the release type vector and C950 on NAME/five TEXT attributes.
The implicit catalog-C versus user-POSIX comparison rejects42P22 with its complete
English diagnostic; explicit POSIX annotation succeeds. This observes catalog
metadata through existing comparison semantics, not a new comparison provider.

Seven selected fixed OIDs are asserted without claiming whole-host inventory
parity. Provider/encoding and libc categories versus provider locale are exact.
The pinned PostgreSQL18.0 fixture initializes `unicode.collversion` to `153.120`;
native explicitly reports unavailable ICU version as NULL instead of inventing
provider facts. Configured empty recorded versions remain nonnull empty strings.
Fresh held reads cover CREATE, rename, namespace/owner changes and DROP;
already returned native rows retain old values. PUBLIC catalog reads include
another owner's off-path collation while name-discovery visibility remains false.
Generated identities use positive distinct identities and actual namespace/owner
relationships, not allocator goldens.

Diagnostics cover missing columns/relation, malformed/out-of-range OIDs,
analysis before an empty source, held malformed parameter failure and successful
reuse, and native row-lock exclusions on populated/empty sources. Both adapters
report the same unnamed-portal OID parameter Context. Existing
`TestReferenceCollationAuthorization` supplies the readonly mutation witness.
Private capture units additionally assert actual descriptor membership, typed
NULL/empty distinctions, immutable old captures, unchanged catalog/OID/revision
frontiers and cancellation without partial publication.

```sh
make check REFERENCE=1 RUN='^TestReferencePGCollation(Shape|Seeds|AttributeCollation|FreshMetadata|PublicRead|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGCollation(Shape|Seeds|AttributeCollation|FreshMetadata|PublicRead|Diagnostics|FeatureSmoke)$'
make check RUN='^TestPGCollation(ShapeAndOwnership|Capture)$'
make check RACE=1 RUN='^TestPGCollation(ShapeAndOwnership|Capture)$'
# Permanent feature smoke:
make check RUN='^TestReferencePGCollationFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCollationFeatureSmoke$'
```

Smoke expects exact full `pg_c_utf8` metadata, fresh held lifecycle results,
immutable native captured rows, implicit-comparison42P22, an explicit POSIX true
control, and exact versus unavailable Unicode recorded versions. Locale
population, ICU execution and arbitrary libc semantics remain outside this part.

### pg_attribute selected proof

The shared `TestReferencePGAttribute` definitions independently assert all 25
released fields, native/pq raw metadata, canonical nullable ANYARRAY transport,
relation-local identity, exact builtin/type/system facts, syntactic rank, flags,
column ACLs, dropped history and current DDL publication. Direct/plain, fresh
prepared and SAME held readers use the existing fixture and middleware.
Transactions/temporary cases retain explicit owner/observer boundaries; Controls
exercises real native Dump/Reset/Restore and actual emitted SQL replay on PostgreSQL.
TypedControls additionally preserves altered typed rank3 beside later-created
rank0, using ordinary CREATE followed by the existing ALTER TABLE OF attachment.
Native materialized ADD fills differ explicitly from PostgreSQL fast-default
physical history, and logical dumps do not replay dropped-column tombstones.

CopiedRoutes pins partition rank0, initial typed rank0, and explicit typed
ALTER/ADD CASCADE ranks2/3. The merged explicit inherited declaration is also
oracle-pinned rank0. Unconstrained domains share the ordinary metadata matrix;
domain NOT NULL remains a real-only observation plus the existing exact native
constraint-admission exclusion, not a new type feature.
RepresentedBoundary independently checks the existing primitive native
table_privileges schema against PostgreSQL's actual domain-backed view, without
fabricating domain execution or relabeling native descriptor types.
Committed TEMP descriptors are catalog-visible across sessions in both backends;
peer execution by the TEMP name remains forbidden. Controls separately asserts
old literal-held catalog fencing, numeric-parameter held refresh and immutable
eager captures. SQL REVOKE empties column ACLs to NULL without discarding the
private explicit-empty `{}` representation.
Array btree keys and partitioned index creation retain their exact native
`42704`/`0A000` admission boundaries and no-residue checks; full PostgreSQL rows
remain asserted, alongside shared scalar-key/array-INCLUDE metadata. Existing
NAME casts and mixed OID/regclass comparisons remain explicit exclusions, not
scope expansion; role/group/user and JSONPath migrations also check admitted
raw-NAME/numeric-OID inquiries in both modes.
ConstraintAttributes independently checks all 28 represented constraint fields,
including seven nullable array fields plus nullable `conbin`.
ControlsPartitionRank distinguishes minimal attached rank3 from copied rank0.
ControlsRankHistory and RankRepairSQL preserve inherited/merged ranks, local ADD
merges, nested descendants, source2/root3 divergence and independently aged zero
siblings through two Restore/redump cycles and actual emitted PostgreSQL SQL.
RankRepairGraphs covers multiple columns/parents and a nonbootstrap owner with
unchanged implicit ACLs. Repair SQL runs after `SET SESSION AUTHORIZATION DEFAULT`;
real PostgreSQL replay requires superuser catalog-update authority.
Native public UPDATE remains exactly forbidden. Strict malformed repair AST,
late failure after a valid repair, missing/dead targets, cancellation and retained
eager captures supplement full metadata/default/data/relationship checks.

The shape follows the [PostgreSQL 18 catalog contract](https://www.postgresql.org/docs/18/catalog-pg-attribute.html).
Run each public definition in both modes after affected changes:

```sh
make check RUN='^TestReferencePGAttribute(Shape|Identity|Builtins|Types|Declarations|DeclarationsCopiedRoutes)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Shape|Identity|Builtins|Types|Declarations|DeclarationsCopiedRoutes)$'
make check RUN='^TestReferencePGAttribute(Flags|ACL|System|Kinds|Dropped)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Flags|ACL|System|Kinds|Dropped)$'
make check RUN='^TestReferencePGAttribute(Lifecycle|Transactions|Temporary|Diagnostics|Controls|Smoke)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(Lifecycle|Transactions|Temporary|Diagnostics|Controls|Smoke)$'
make check RUN='^TestReferencePGAttribute(RepresentedBoundary|TypedControls)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(RepresentedBoundary|TypedControls)$'
make check RUN='^TestReferencePG(ExtensionShape|EnumShape|RolesAttributeCatalog|GroupAttributeCatalog|UserAttributeCatalog)$'
make check REFERENCE=1 RUN='^TestReferencePG(ExtensionShape|EnumShape|RolesAttributeCatalog|GroupAttributeCatalog|UserAttributeCatalog)$'
make check RUN='^TestReference(AvailableExtensionsMetadata|ExtensionMemberNullability|PGRolesOracleCatalog|PGGroupOracleCatalog|PGUserOracleCatalog)$'
make check REFERENCE=1 RUN='^TestReference(AvailableExtensionsMetadata|ExtensionMemberNullability|PGRolesOracleCatalog|PGGroupOracleCatalog|PGUserOracleCatalog)$'
make check RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check REFERENCE=1 RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check RUN='^TestReferencePGAttribute(ConstraintAttributes|ControlsRankHistory|ControlsPartitionRank|RankRepairSQL|RankRepairGraphs)$'
make check REFERENCE=1 RUN='^TestReferencePGAttribute(ConstraintAttributes|ControlsRankHistory|ControlsPartitionRank|RankRepairSQL|RankRepairGraphs)$'
make check RUN='^TestReferenceJSONPath(Representation|Composition)$'
make check REFERENCE=1 RUN='^TestReferenceJSONPath(Representation|Composition)$'
make check RUN='^TestPGAttribute(Shape|Capture|Publication|DeclaredRank|DroppedHistory|Carrier)$'
make check RACE=1 RUN='^TestPGAttribute(Capture|Publication|DroppedHistory)$'
make check RUN='^TestAttribute(TypeFacts|CatalogNullCarrier)$'
make check RUN='^(TestPgClassBuiltinIndexMetadata|TestTableViewSummaryBuiltinIdentity)$'
make check RUN='^TestObject(Grant|Revoke)'
make check RUN='^TestAttributeRankRestore(Syntax|Publication)$'
make check RACE=1 RUN='^TestAttributeRankRestorePublication$'
# Permanent feature smoke:
make check RUN='^TestReferencePGAttributeSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGAttributeSmoke$'
```

Attribute assertions formerly guarded as PostgreSQL-only are now shared in the
owning extension/enum/available-extension tests and the named role/group/user
AttributeCatalog definitions. Adjacent pg_type-only observations retain their
real-only guards. Private units supplement SQL with actual capture, descriptor,
mutation, publication, type-fact and adapter-export functions, including foreign
authority rejection, checked INT2 narrowing and immutable scalar tombstones.

### pg_namespace selected proof

The ten shared `TestReferencePGNamespace` families cover exact shape/raw
metadata, pristine seeds, user identity, visibility, ACLs, TEMP pair lifecycle,
diagnostics, native controls and feature smoke. Native OID/NAME/ACL carriers
are int64/string/string; pq uses bytes for these catalog types. Expectations
are independently specified, including exact stored ACL array order.
Visibility additionally proves the shared public-schema ownership fixture restores
actual prior owner and caller identity after ordinary return or panic; the package
component counterpart is `TestPGNamespaceFixtureOwnership`. Neither fixture changes
pristine seeds or globally grants schema CREATE.
The `Controls/nominal_replay` phase independently creates public domains/enums/composites
under scoped actual ownership, restores pristine namespace authority before any Dump,
and preserves complete native streams, owner-only domain/composite DROP authority
and stored values across genuine Reset/Restore. PostgreSQL also drops the owned
enum directly; native pins the existing enum DROP TYPE0A000/Position1 boundary
through direct/prepared calls, proves no effect and uses supported exact-owner
DROP OWNED cleanup. The shared ordinary value/schema-denial contract runs on both
backends; native control APIs have no fictitious PostgreSQL copy.
`TestNominalTypeRestorePublicNamespaceAuthority` and `DestinationFence` supplement
that surface with real package-level stream reconstruction and forged/ordinary
authenticator rejection. `TestPGNamespaceTemporaryViewCreationAuthority` pins first
TEMP view staging/execution under an ordinary creator; physical cleanup retains the
pair, not its children or namespace-scoped default directives.
`TestReferencePGNamespaceTemporarySuperuserAuthority` distinguishes captured true
bootstrap superusers from ordinary SET ROLE actors, live qualified REGCLASS lookup
from own-session creation, and retired-child42P01 from ordinary schema42501.
Its direct/prepared shared body retains exact pair metadata and zero-child facts,
foreign/retired CREATE42P16 and new-session nonadoption; it does not scan another
backend's physical TEMP table. `TestPGNamespaceTemporarySuperuserAuthority` additionally
proves immutable captured actors across live role switches and unchanged own-TEMP
authority. Existing bootstrap retirement readers use common42P01, not a native
database-owner explanation or diagnostic override.
Native physical foreign-TEMP storage remains unavailable after valid qualified
namespace lookup. `TestPGNamespaceForeignTemporaryStorageCapture`, `Execution`
and `Targets` exercise actual roots, direct/ONLY and recursive stored-view denial,
metadata-only/preseeded-reference admission, persistent/own inheritance and no-effect
DML/COPY/TRUNCATE. Full0A000 is an execution boundary, not a Prepare/schema error.
`TestReferenceTemporaryTableDiagnostics` preserves the independently observed PG
small-peer empty cursor while asserting that honest native storage exclusion.
The shared `TestReferencePGNamespaceViewStoragePrecedence` and
`TestReferencePGNamespaceCopyStoragePrecedence` (in `reference/dml`) preserve
source/table/schema42501 and COPY read-only25006 ahead of physical admission.
Actual revocation, nested owner/invoker views, paired snapshots, storage roots,
TEMP footprint and denied stream I/O are supplemented by
`TestPGNamespaceViewStoragePrecedence` and `TestPGNamespaceCopyStoragePrecedence`.

Shared public relation/namespace identities and existing REGTYPE lookup prove
the registered source relationship. PostgreSQL additionally executes its true
pg_type reciprocal query; native asserts the exact missing-pg_type boundary,
with registered rowtype/array ownership covered by TestPGNamespaceShape.
ACL ownership transfer/merge remains real shared SQL. Role rename remains a
PostgreSQL SQL phase: native direct and actual Prepare attempts assert the
existing 0A000/Position1 exclusion, check no effect, and retain eager ACL rows across
supported schema ownership transfer. No adjacent catalog or role capability is
implemented to make an incidental observation succeed.

```sh
make check RUN='^TestReferencePGNamespace(Shape|Builtins|Identity|Visibility|ACL|Temporary|TemporarySuperuserAuthority|Lifecycle|Diagnostics|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGNamespace(Shape|Builtins|Identity|Visibility|ACL|Temporary|TemporarySuperuserAuthority|Lifecycle|Diagnostics|Controls|FeatureSmoke)$'
make check RUN='^TestReferencePGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check REFERENCE=1 RUN='^TestReferencePGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check RACE=1 RUN='^TestPGNamespace(ViewStoragePrecedence|CopyStoragePrecedence)$'
make check RACE=1 RUN='^TestPGNamespace(Shape|Capture|ACL|Authority|Cancellation|TemporarySuperuserAuthority)$'
make check RUN='^TestPGNamespaceFixtureOwnership$'
make check RUN='^TestReferencePGNamespaceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGNamespaceFeatureSmoke$'
```

Use external900-second deadlines. These are selected proofs, not substitutes
for whole-project quickcheck or finalcheck. No discovery backend filter remains.
Pristine builtin expectations use the existing exported oracle DSN with its
database path set to `/postgres`, never the reset fixture's reconstructed
public schema. Builtins reads are read-only. The first-use TEMP transaction
witness alone uses `/postgres` for TEMP-only rollback/commit isolation on the
same physical creator before its first commit; it does not mutate permanent
schemas or shared infrastructure. Other TEMP scenarios use the normal fixture
and permit PostgreSQL backend-slot reuse of preexisting namespace tuples.

Controls are explicit native-only phases; there is no fake PostgreSQL API.
The session-only standalone public smoke is not committed. It asserts real
four-field prepared reads, seeds, user identity/rename, schema denial, TEMP
owner separation/pair and Dump/Reset/Restore; maintained feature smoke exercises
these surfaces on both backends with the fixture's recorders.


### pg_database selected proof

`TestReferencePGDatabase` independently specifies the PostgreSQL 18 eighteen-column
schema, raw driver metadata, current identity and cluster inventory, accessible
UTF8/C facts, owner and stored CREATE ACL publication, ordinary-role visibility,
search-path shadowing, complete diagnostics and native-only attachment/control
lifetime boundaries. Unavailable native template/admission/limit/horizon/placement
properties are typed NULL, not copied PostgreSQL physical values. Physical oracle
XIDs and generated database OIDs are checked by carrier and identity relationships,
not allocator goldens.
Unconstrained TEXT columns expose `ColumnType.Length()=(math.MaxInt64,true)` on
both native and pq adapters, including empty and NULL-only results. The other
pg_database types report unavailable length; all report unavailable decimal size.

Native opaque attachments and Reset/Dump/Restore are explicit native-only phases;
the shared SQL definitions run on every available backend.

The provider reads one immutable database descriptor with paired catalog/role
authority, projects actual stored ACLs through the existing ACLITEM codec, and
never scans other engines or manufactures physical horizons/placement.
`TestPGDatabaseShapeAndOwnership`, `Capture` and `Publication` supplement public
SQL with exact descriptor ownership/origins, concrete typed NULLs, cancellation,
foreign/stale authority rejection, transaction-private candidate isolation and
retained capture lifetime. New relation metadata extends only the represented
indexed/shared inventories and the bounded pg_tables inventory.

```sh
make check REFERENCE=1 RUN='^TestReferencePGDatabase(Shape|Identity|Metadata|Visibility|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePGDatabase(ACL|Controls|Smoke)$'
make check RUN='^TestReferencePGDatabase(Shape|Identity|Metadata|Visibility|Diagnostics)$'
make check RUN='^TestReferencePGDatabase(ACL|Controls|Smoke)$'
# Permanent feature smoke:
make check RUN='^TestReferencePGDatabaseSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGDatabaseSmoke$'
make check RUN='^TestPGDatabase(ShapeAndOwnership|Capture|Publication)$'
make check RACE=1 RUN='^TestPGDatabase(Capture|Publication)$'
make check RUN='^(TestPgClass(Projection|BuiltinIndexMetadata)|TestTableViewSummaryBuiltinIdentity)$'
make check RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
make check REFERENCE=1 RUN='^(TestReferencePgClassBuiltinIndexHistory|TestReferencePGTableViewsBuiltins)$'
```

ACL cases restore the captured real database owner and exact nullable ACL through
the administrative fixture after borrowers close, before dropping roles. The
parameterized current-database-only catalog UPDATE is teardown, not a public
catalog mutation compatibility claim. Every cleanup failure is reported.

### pg_class selected proof

The shared `TestReferencePgClass` definitions cover the PostgreSQL 18 34-column
schema before iteration, exact existing relation identities/kinds, row types,
attribute high-water, CHECK/RLS/population facts, sticky index/subclass history,
ordered stored ACLs and grant options, installed lexical view options and typed
RANGE/default bounds. Direct, actual plain, fresh prepared and SAME held readers
retain independently specified metadata, raw codecs, rows and diagnostics.
Case-specific names scope database-wide catalog bags; table permission does not
filter metadata discovery.

`BuiltinIndexHistory` checks all twenty-two represented indexed table catalogs and
every represented view negative through direct, fresh and SAME prepared readers,
without inventing native backing-index rows. `FirstChildRollback` starts with a
childless RANGE parent: successful first ATTACH sets history only in the owner's
private catalog, then savepoint/full rollback restores false for both owner and
peer, including SAME prepared readers. `InvalidIndexHistory` retains an actual
live/unready/invalid index after a duplicate-data concurrent unique build fails,
asserts its index kind and parent history, then drops it while history stays true.

Native physical fields are explicit
[logical boundaries](../../docs/DIALECT.md#pg_class-logical-relation-metadata),
not parity goldens derived from native output. PostgreSQL observes real
sequence/partitioned-index initialization and ANALYZE; native tuples remain
unknown (-1) except fixed singleton sequences (1). Native structural bound output
differs intentionally from recorded released nodeToString bytes.

PostgreSQL performs actual member RENAME and catalog-source REVOKE/GRANT.
Native independently asserts their existing complete 0A000 capability errors;
retained-bound lifetime uses admitted member DROP and actual Dump/Restore.
`TestPgClassCatalogACLRecheck` publishes real native catalog ACLs with SetObjectACL,
checks denied/restored current permission through the same operation, and verifies
that retained authorization roots are unchanged. No schema/catalog privilege SQL
or hierarchy capability is added merely to produce metadata witnesses.

Run each public group in both modes after changing its definitions or owning
implementation:

```sh
make check RUN='^TestReferencePgClass(Shape|Relations|Builtins)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Shape|Relations|Builtins)$'
make check RUN='^TestReferencePgClass(Flags|PhysicalBoundaries|Partitions)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Flags|PhysicalBoundaries|Partitions)$'
make check RUN='^TestReferencePgClass(ACL|Options)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(ACL|Options)$'
make check RUN='^TestReferencePgClass(Lifecycle|Transactions|Temporary|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Lifecycle|Transactions|Temporary|Diagnostics)$'
make check RUN='^TestReferencePgClass(Controls|Smoke)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(Controls|Smoke)$'
make check RUN='^TestReferencePgClassBuiltinIndexHistory$'
make check REFERENCE=1 RUN='^TestReferencePgClassBuiltinIndexHistory$'
make check RUN='^TestReferencePgClass(FirstChildRollback|InvalidIndexHistory)$'
make check REFERENCE=1 RUN='^TestReferencePgClass(FirstChildRollback|InvalidIndexHistory)$'
make check RUN='^TestPgClassBuiltinIndexMetadata$'
make check RUN='^TestPgClass(Projection|Kinds|ACLTranslation|CatalogACLRecheck)$'
make check RACE=1 RUN='^TestPgClass(SubclassPublication|SubclassReplacement|OptionHistory|PartitionTree|CatalogACLRecheck)$'
make check RUN='^TestPG(CastShapeAndOwnership|DescriptionShape|ExtensionShapeAndOwnership|SequenceShape|ShDescriptionDescriptor)$'
make check RACE=1 RUN='^(TestInheritanceQueryStoredViewAdmission|TestTypedPublicationLateOwnRowConsumer)$'
```

Affected private catalog provider consumers use named pg_class fields, retaining
their exact identity/kind/attribute-count assertions rather than the obsolete
ten-column offsets. Inheritance/stored-view and typed late-consumer fixtures
assert the immutable false-to-true subclass publication, then capture their real
post-successful-child baseline. Their later lock-wait/admission and rejected
mutation descriptor/storage invariants remain unchanged.

`Controls` replays actual emitted native Dump SQL on PostgreSQL, asserting no
invented absent options. Native failed/canceled replay and dependency/prepared
transaction fences retain the current bag, held readers and captured bytes;
successful replacement preserves valid live TEMP identities/options/history
and remaps persistent row types relationally. Active graph history reconstructs
on replay; deleted-only persistent history is not promised. Arbitrary explicit
relation ACL Dump rejection stays intact.

`Smoke` is the permanent public end-to-end witness. The part also supplies an
external public-only standalone module/program: root runs
`timeout 900s go run -mod=mod .` in its temporary module, expecting
`pg_class smoke OK` after complete shape/logical/tree/options assertions, one
retained prepared reader, explicit ACL/Dump rejection, baseline Restore,
invalid Restore atomicity and Reset. No standalone smoke scaffold is committed.
The historical standalone reader checked the then-eighteen indexed builtin table
facts and represented view negatives initially and after successful/failed Restore
and Reset. The maintained `BuiltinIndexHistory` definition includes pg_database,
pg_attribute, pg_namespace and pg_db_role_setting, giving twenty-two represented indexed catalogs.
Selected proof and standalone smoke do not replace whole-project quickcheck.

### CREATE TABLESPACE selected proof

The shared `tablespace_*_test.go` scenarios independently specify builtin
`pg_tablespace` rows and ownership, named projection types, direct/prepared
syntax and diagnostics, owner/path/name precedence, fresh effective authority,
read-only/explicit/implicit transaction admission and failed-state recovery.
Real PostgreSQL creates a nonreserved tablespace in an actual container-owned
directory before asserting duplicate 42710, and explicitly drops its cluster
objects. Embedded creation must instead refuse physical locations with 0A000,
including repeated attempts, without publishing a user row or changing sentinels.
Reserved builtin names are not mislabeled as duplicates.

The native projection deliberately omits unsupported `spcacl`; supported
columns are `oid,spcname,spcowner,spcoptions`. Native Reset/Dump/Restore and
retained-row witnesses are explicit control phases in the shared scenario,
not fictitious PostgreSQL controls. Valid WITH syntax reaches the physical
boundary after earlier validation; option-value compatibility is not claimed.
Canonical path byte boundaries are 970 accepted lexically and 971 rejected;
short-component and multibyte witnesses distinguish filesystem component limits
and rune counts from the SQL validation contract. The public OID codec outcomes
are independently specified (pq normalized strings, native int64), not compared
as if the drivers had identical raw carriers. These instructions are not evidence.

```sh
make check REFERENCE=1 RUN='^TestReferenceTablespace'
make check RUN='^TestReferenceTablespace'
make check RUN='^Test(Tablespace|CreateTablespace|PGTablespace)'
make check RUN='^TestReferenceTablespaceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTablespaceFeatureSmoke$'
```

The durable smoke checks builtin rows/types, owner and superuser errors,
relative and reserved-name errors, transaction admission, physical refusal
and unchanged sentinel/no user rows. The real-directory duplicate is covered
by the selected family, not by reserved-name smoke assertions.

### pg_enum selected proof

The shared `TestReferencePGEnum` family specifies the four-column
`pg_catalog.pg_enum` contract independently of native output: raw metadata,
actual owning regtype identities, definition-only labels, generated member OID
relationships, initial/negative/fractional positions, stable ALTER identities,
dense-gap renumbering, ordinary query consumers, transaction publication and
retained Rows, logical controls, exact diagnostics and PUBLIC metadata access.
`FeatureSmoke` creates and evolves an actual enum and compares stored enum order
with catalog order; it is not a baseline catalog-registration test.

```sh
make check RUN='^TestReferencePGEnum(Shape|DefinitionsAndQueries|AlterPositions|Renumber)$' REFERENCE=1
make check RUN='^TestReferencePGEnum(VisibilityAndLifetime|Controls|DiagnosticsAndPrivileges|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferencePGEnum(Shape|DefinitionsAndQueries|AlterPositions|Renumber)$'
make check RUN='^TestReferencePGEnum(VisibilityAndLifetime|Controls|DiagnosticsAndPrivileges|FeatureSmoke)$'
make check RUN='^TestReferencePGEnumFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferencePGEnumFeatureSmoke$'
```

Logical Reset/Restore and native readonly mutation/locking boundaries are explicit
native-only phases within the shared scenarios. `Controls` produces a real native
logical dump using delivered enum DDL in an independent fixture-owned Driver for
PostgreSQL replay, avoiding state left by the preceding native backend.
PG-only pg_attribute shape inspection supplements the oracle without requiring
an adjacent catalog implementation. Raw FLOAT4 cells remain checked as float64;
logical position comparisons explicitly round through binary32 because pq parses
PostgreSQL's shortest decimal FLOAT4 text into float64.
Direct catalog origins retain the NOT NULL metadata contract; stored view
consumers use the existing native nullable projection metadata while asserting
the same exact column types, raw codecs and rows. Native schema/type privilege
SQL is outside scope: PostgreSQL alone performs the schema/type REVOKE setup and
supplemental privilege predicates. Both backends prove denied private-schema
type resolution followed by successful PUBLIC catalog metadata discovery.
Catalog-source SQL GRANT/REVOKE is also outside the delivered native privilege
syntax. PostgreSQL retains the complete SQL revocation diagnostic witness;
`TestPGEnumShapeAndCapture` exercises actual native PUBLIC access, explicit ACL
revocation/restoration through SetObjectACL, and retained authorization lifetime.

Private metadata/allocator/capture/import proof and affected enum callers:

```sh
make check RUN='^TestPGEnum(DefinitionMetadata|AllocatorBounds|SortInsertion|ShapeAndCapture|ImportMetadata)$'
make check RUN='^TestEnum'
make check RUN='^TestQueryFilterProvenEmptyDemand$'
make check RUN='^TestReferenceEnumAlter(Order|Controls|SavepointProvenance|VisibilityLocks|PreparedIdentity)$' REFERENCE=1
make check RUN='^TestReferenceEnumAlter(Order|Controls|SavepointProvenance|VisibilityLocks|PreparedIdentity)$'
make check RUN='^TestReferenceEnum(Controls|RestoreAtomicity|OwnerAndTemporaryRemapping|Diagnostics|PreparedLifecycle)$' REFERENCE=1
make check RUN='^TestReferenceEnum(Controls|RestoreAtomicity|OwnerAndTemporaryRemapping|Diagnostics|PreparedLifecycle)$'
```

During part verification, the root additionally runs the supplied standalone
public-API smoke from an external session temporary file:
`timeout 900s go run "$SESSION_TMP/pg-enum-smoke.go"`. It creates an independent
Driver/database, checks raw metadata and actual owner/member identities, holds
old Rows and a prepared catalog query, evolves z/q into first/z/a/end with
positions 0/1/1.5/2, and checks stored enum order. Dump/Reset/Restore must recreate
positions 1/2/3/4 with fresh member identities while old eager Rows remain
unchanged. A failed catalog mutation must return the full native 0A000 record
and leave rows unchanged. Success is quiet exit 0; any failed assertion exits
nonzero. This standalone observation supplements, not replaces, selected
dual-mode proof and whole-project quickcheck.

### Encoding conversion oracle-first definitions

`conversion_*_test.go` owns the shared conversion lifecycle, complete DDL
diagnostics/Prepare phases/notices, ownership, default lookup, duplicate-default
schema moves, actual encoding outputs, retained results, released catalog
associations, native controls and feature smoke. The expected handler and seed
inventory is literal REL_18_0 data, independent of the native implementation.
REGPROC output and its OID/TEXT casts are checked separately from handler names.

Initial pinned discovery is followed by native failure observation and selected
dual-mode proof using the same definitions; no backend-discovery filter remains.
Diagnostics encode the pinned oracle contract rather than native failure output.
PostgreSQL schema ACL phases use actual GRANT/REVOKE. Native public setup uses
explicit schema-owner transfers for reachability/CREATE, preserving conversion
ownership independently; actual native ACL denial remains private-seam coverage.
Namespace metadata uses independently captured namespace OIDs from uniquely
named ordinary tables resolved through existing qualified REGCLASS/pg_class.
Each witness is dropped before conversion owner/dependency assertions; no
pg_namespace or REGNAMESPACE capability is assumed. Seed repair reads the pinned
conversion's name and namespace ID, requiring the known hidden namespace or
pg_catalog identity. After native Restore remints a user namespace, cleanup
independently re-resolves only the known hidden schema under a fresh bounded
context and requires an exact ID match before repair.
Native private consumer units acquire real statement snapshots for conversion
evaluation and construct unsigned REGPROC carriers. General routine-alias input/
storage is covered by the [type-resolution family](../type_resolution/README.md#testing),
not by expanding conversion providers. Control units assert actual lossless replay and preservation of
existing namespace collisions, not incidental temporary-helper spellings;
the shared reference definitions separately retain exact public CREATE output.

Small selected commands (repeat each with and without `REFERENCE=1`):

```sh
make check RUN='^TestReferenceConversion(Lifecycle|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceConversion(Ownership|DefaultsAndLookup|DefaultSchemaMove)$' REFERENCE=1
make check RUN='^TestReferenceConversion(Metadata|ExecutionAndRetained|Controls)$' REFERENCE=1
make check RUN='^TestReferenceConversionFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceConversionCancellation$' REFERENCE=1
make check RUN='^TestReferenceConversionCancellation$' RACE=1
make check RUN='^TestReferenceConversion(Transactions|Temporary)$' REFERENCE=1
make check RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$' REFERENCE=1
```

The affected visibility-boundary definitions now consume accepted conversion
CREATE through direct utility Query and genuine prepared execution. They assert
no Prepare publication, actual user conversion metadata/visibility, DROP removal
and unchanged sentinel rows; obsolete native conversion syntax exclusions are
not retained as compatibility shims.

Feature smoke expects raw LATIN1 `e9`, full `22P05` for Euro and SAME held
statement recovery, stable conversion identity across rename/schema move, exact
native quoted dump definition/replay and `42883` after dropping the reachable
default. Native controls are explicitly native-only phases in the same shared
body; Controls replays the actual native dump on the pinned PostgreSQL backend
and an independent native Driver. Seed repair is registered before mutation and
uses an independent bounded cleanup context before nonpublic schema teardown.

`TestReferenceConversionCancellation` selects a real custom LATIN1-to-UTF8
default after hiding the seed, observes actual provider/server admission for a
32MiB stored payload, cancels active reads and staged writes, joins every worker,
and recovers on the SAME held owner/statements with exact UTF8 bytes and no
published write prefix. The existing real admission helper remains unchanged.

`TestReferenceConversionTransactions` compares PostgreSQL transactional DDL with
the retained native Execute-phase `0A000` boundary, including prepared statements
and shared read-only `25006` diagnostics. `TestReferenceConversionTemporary`
uses an actual physical-session retirement barrier and verifies TEMP conversion
omission from portable Dump, retention across live native Reset/Restore, and
removal when the owning physical session exits. Controls also exercise named
owner replay, independently pre-created target principals, and missing-owner
failure without destination replacement.


### Registered extension selected proof

The untagged shared `reference/catalogs/extension_*_test.go` scenarios independently
assert direct/prepared TEXT/NULL/rules, namespace and trusted ownership, exact
full diagnostics/notices, atomic failures, transaction/savepoint/publication
behavior and native control round trips. Controls additionally import named
extension/member owners into an independent Driver with different role attributes,
check strict metadata and target-principal fences, retained Rows/metadata,
committed-only Dump and deterministic redump. Native ownership comments are not
claimed as PostgreSQL SQL owner-restoration commands.

The disclosed PostgreSQL-only OracleSetup uses real loose FUNCTION/template/
dictionary and schema-GRANT setup unavailable natively; genuine package units
stage corresponding member descriptors and actual ACL consumers. Concurrent
oracle synchronization uses pg_stat_activity solely to observe the genuine
unique-index wait; native private transactions exercise the same complete-or-none
publication outcome at commit. No sleep or worker-order assumption identifies
the loser. Added helper Context expectations are source-grounded independently
and require confirmation in the selected pinned run.

Permanent TEMP definitions cover the independently observed own-template helper
failure, authorized foreign installation, exact namespace edge, transactional
DISCARD/savepoint recovery, bounded physical owner close and native portability/
retention. Peer assertions during tentative DISCARD deliberately read committed
catalog identities only: executable consumers can wait on the owner's real locks.
After rollback, both owner and authorized peer execute the restored provider/views.
Physical close means lease **and** pool close, not lease return; all close workers
join within5s. PostgreSQL's advisory exit barrier observes cleanup completion before
catalog assertions, without polling or assuming asynchronous socket close is exit.
Mixed collations, private maintenance, late-root/row/index publication, exhausted
metadata revisions, retained old Rows, source-token rejection, imported OID/name
collisions and named retained owner DROP dependencies have genuine engine units.
TemporaryReplacement also captures a genuine TEMP view procedure manifest and
TEMP DEFAULT/CHECK consumer before replacement, executes them afterward, and
rejects same-address procedure substitution, foreign physical tokens and ordinary
stale ObjectRefs. Schema retirement uses successful same-name CREATE SCHEMA rather
than adding an unrelated native pg_namespace projection.
SchemaDependencies independently specifies normal extension-to-schema RESTRICT
diagnostics and CASCADE notices/consumer closure. Confirm these independently
specified records against the pinned oracle before accepting native behavior.

The integrated `pg_extension_*` definitions use the same single live
descriptor-derived catalog source as extension installation, not a second empty
provider. Fresh native catalogs are genuinely empty; installed descriptors supply
actual identity/owner/schema/version rows. Engine witnesses preserve strict
column shape/type-generation ownership, cancellation and retained capture
semantics, and additionally compare an installed row with its published descriptor.
Missing-provider CREATE prepares successfully and fails at execution with
`0A000` on both backends. PostgreSQL's system-installation Hint is pinned only on
PostgreSQL; native exposes its truthful registered-only availability boundary.
Unchanged ALTER/DROP/LOAD command boundaries remain separately asserted.

Review2 tests-first checkpoint pins eighteen member-catalog attribute nullability
records as named bags, not physical attnum order. PostgreSQL's pq Nullable
capability remains unknown; native empty and populated direct/prepared queries
require known NOT NULL for all exposed procedure/template fields and dictionary
first five fields, with nullable dictinitoption. The actual standalone smoke
asserts those same native metadata contracts.

TemporaryCleanup's actual-session-end preparation_stale and
postdrain_late_consumer subcases delegate genuine outside-lock relation-provider
validation, publish an independent late procedure-dependent view and durable
row, and require internal fresh retirement. Ordinary cleanup validates before
index preparation; validated candidates are not mutated by later index metadata
evolution. Failed attempts unwind their scope/operation/stage before recapture,
and unchanged structural errors escape without retry. Mixed TEMP collation plus
saturated DEFAULT/CHECK metadata uses terminal-only fresh-descriptor semantics;
actual ordinary/postdrain malformed-survivor units require prompt visible error
and intact committed state. No user DDL overflow policy is relaxed.

Run each command with an external900s deadline from the part worktree:

```sh
make check RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(InstallAndProvider|IdentityAndRepeat|VersionSchemaAndPrivilege|Transactions|Controls|OracleSetup|FeatureSmoke)$'
make check RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check REFERENCE=1 RUN='^TestReferenceExtension(NamespaceDependency|TemporaryOwnInstall|TemporaryLookup|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RUN='^TestExtension(ProviderIdentity|Rules|ExpressionIdentity|ProviderConflicts|SchemaACL|ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
make check RUN='^TestTemporaryTable(CleanupClosure|CleanupPublication|TerminalRevisionPreparation|TerminalMaximumPublication|CascadeRevisionBounds)$'
make check RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGExtension(CommandBoundaries|Diagnostics|Shape|Installed|ResolutionAndPrepared|Controls|FeatureSmoke)$'
make check RUN='^TestPGExtension(ShapeAndOwnership|Capture|CaptureInstalled)$'
make check RUN='^TestReferenceExtensionMemberNullability$'
make check REFERENCE=1 RUN='^TestReferenceExtensionMemberNullability$'
make check RUN='^TestCollation(PrivateSessionIdentity|TemporaryCleanupAX|TemporaryCleanupCompletenessFence|TemporaryCleanupExhaustion)$'
make check RACE=1 RUN='^TestReferenceExtension(Transactions|Controls|TemporaryDiscard|TemporaryRollback|TemporaryClose|TemporaryControls|SchemaDependencies)$'
make check RACE=1 RUN='^TestExtension(ProviderPublication|DatabaseCreateAuthority|OwnerMetadata|TemporaryClosure|TemporaryCleanup|TemporaryLookup|TemporaryReplacement|SchemaClosure)$'
```

Select top-level definitions, not a backend slash filter. The same completed
reference definitions require both native and real pinned modes before review.
Root runs whole-project Docker-free quickcheck after synchronized edits, not
mid-flight. Standalone feature smoke is the complete session artifact
`local://ext-registry-smoke.go`, materialized outside the checkout and run with
`timeout 900s go run \"$SESSION_TMP/ext-registry-smoke.go\"` from the worktree;
quiet exit0 requires actual installation, TEXT/NULL, exact repeat/notice,
member relationships, rollback isolation, trusted privileges and control/import
ownership witnesses. It additionally performs actual own-TEMP42883 atomicity,
foreign installation and owner-vs-peer lookup, zero-output Dump failure, tentative
catalog-only DISCARD visibility, executable rollback recovery, committed full
consumer closure/surviving rows, fresh reinstall, bounded physical retirement
with exit barrier, and persistent schema RESTRICT/CASCADE closure. A test named
FeatureSmoke does not replace that program. Post-merge finalcheck uses an external
1800s deadline; targets themselves introduce no hidden deadlines.

### Available extension views selected proof

The untagged `reference/catalogs/available_extensions_*_test.go` definitions
independently assert both released shapes, raw transports/nullability, registry
universe, installed/default distinction, exact pg_views reconstruction, predicates,
held prepared freshness, joins/stored views, full42703 diagnostics, low-privilege/
off-path/foreign-TEMP metadata and transaction publication. Pinned discovery ran
first with a temporary native filter; that filter was removed before genuine
native missing-relation red and production authorization. No discovery filter or
observation-file probe survives. Oracle-only attribute/type grounding does not
add native catalog producers. Unknown installed-version mutation is real
PostgreSQL-only UPDATE in a transaction always rolled back; native covers the
same projection with a private authentic descriptor copy, not new installation.

Controls exercise actual public native Dump/Reset/Restore, committed-only Dump,
independent Driver restoration and retained rows/types; PostgreSQL's phase uses
real installation/rollback, not native APIs on pq. Component units assert all
thirteen origins, generation-owned types, reciprocal row/array ownership,
collations, immutable metadata conversion and real cancellation.
JoinConsistency independently asserts five fields from both availability views
and pg_extension in both availability opening orders: initially
`[unaccent,NULL,1.1,false,NULL]`, installed `[unaccent,1.1,1.1,true,1.1]`.
Direct/new prepared/already-held statements on separate physical connections
cover self-private/peer-committed state, savepoint/full rollback and commit.
StatementFrontier is a native component witness: capture the real statement
catalog, open one actual source, genuinely publish in its unlocked opening gap,
then open the other sources. All three first-source choices must retain the old
state; a fresh scope sees the installation. This deterministic schedule is not
claimed for the public PostgreSQL matrix, and does not widen all catalog sources.
Root runs these bounded commands in both modes where shown, each under an
external900-second deadline:

```sh
make check RUN='^TestReferenceAvailableExtensions(Shape|Metadata|Filtering|Installed|Diagnostics|JoinConsistency)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensions(Shape|Metadata|Filtering|Installed|Diagnostics|JoinConsistency)$'
make check RUN='^TestReferenceAvailableExtensions(Visibility|Transactions|UnknownInstalledVersion|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensions(Visibility|Transactions|UnknownInstalledVersion|Controls|FeatureSmoke)$'
make check RUN='^TestAvailableExtensions(ShapeAndOwnership|Projection|MetadataValues|Capture|StatementFrontier)$'
make check RACE=1 RUN='^(TestAvailableExtensionsStatementFrontier|TestReferenceAvailableExtensions(Transactions|Controls|JoinConsistency))$'
```

### Pgcrypto digest/HMAC selected proof

The native registry contains unaccent1.1, selected-type intarray1.5,
selected-function fuzzystrmatch1.2 and bounded pgcrypto1.4. Pgcrypto installs only four genuine immutable/strict/
parallel-safe TEXT/BYTEA digest/HMAC overloads for six algorithms. Exact signatures
are asserted using the supported public OID→REGPROCEDURE text identity, not an
OIDVECTOR/proargtypes capability. Namespace, positive distinct IDs, owner and
membership edges remain independently asserted; PostgreSQL's full stock member
inventory is not equated to native's four.

Fixed RFC/independently calculated digest/HMAC vectors cover empty, binary,
UTF8, exact-block and oversized keys through direct/new/held prepared frames.
Full logical diagnostics, NULL and demanded failing/effectful siblings are
asserted before backend comparison. Native-only controls exercise executable
default/CHECK/generated/index/view consumers, named owners with changed
destination attributes, held rebinding, retained old Rows/metadata, and failed
replay atomicity through public APIs.

Stored generation dependencies admit the actual installed procedure identity,
alongside the existing type/collation/operator/namespace providers; generated
columns and expression indexes remain full consumer witnesses through replay.
InstalledIdentity retains real PostgreSQL REVOKE/GRANT USAGE phases. Native SQL
has no schema GRANT/REVOKE: the ordinary private schema initially denies the
reader, then explicit ALTER SCHEMA OWNER enables both unqualified path lookup
and qualified calls without changing the installed procedures' owners.

Restore's strict owner manifest admits pgcrypto1.4 only with the complete four
procedure name/signature pairs; dictionary/type members, duplicates, missing or
extra members and foreign signatures are rejected. The existing unaccent1.1
and intarray1.5 manifests retain their separate validation. Actual dumped
metadata is corrupted in the native control witness to check rejection without
changing live data, identities or executable consumers, including missing and
inconsistent member owners. Positive replay preserves named destination
principal identities even when role attributes differ; no provider fallback or
owner substitution is used.

Control replacement remaps source references before retagging against destination
types/collations, exactly once each. The supplementary `TestPgcryptoControlRemap`
component regression retains its manually mapped descriptor coverage; it is not
evidence of actual Restore collision handling.

`TestReferencePgcryptoConsumersAndControls` includes a native-only real-pipeline
relocation phase. An ordinary independent-Driver replay establishes candidate
procedure identities. A second identically initialized destination allocates
live TEMP sequences until all four procedure OIDs have actual cross-class
collisions. Public Restore must relocate every imported procedure while retaining
the TEMP OIDs, rows and generator capture. Fixed bytes are asserted for all four
overloads after ordinary and collision replay. Defaults, CHECKs, generated
columns, expression indexes, stored views and a nested scalar-subquery view
remain executable. Procedure dependencies compare complete seven-field addresses
resolved independently from the named default/generated attrdefs, CHECKs, index
and view/nested rewrite rules, not class totals. Attrdef owning-column edges
also identify default column3 (`a`) and generated column4 (`i`).
Both persistent view relation OIDs are resolved separately from their `_RETURN`
rule OIDs, with exact internal rewrite-to-view ownership edges.
Source identities, rows and unread result/metadata captures remain usable.
Each independent destination has its own RawNative middleware recorder. Its SQL
pool wraps the exact configured connector shared with the raw native-control
pool, preserving Driver/DSN identity without recording control operations as SQL
witnesses. The collision destination keeps its wrapped physical connection
pinned from TEMP creation through Restore and refusal checks; source and baseline
assertions continue to use their respective recorders.
The collision destination captures its actual TEMP namespace/relation/rowtype,
and every retained sequence's relation/namespace association. Native verifies
both captured pg_class relation and reltype OIDs with REGCLASS and REGTYPE
resolution qualified by the captured actual TEMP namespace, before and after replacement.
Native does not expose pg_type, so this is not independent proof of its inverse
typnamespace/typrelid fields. The PostgreSQL-only SQL phase asserts those public
pg_type links for its TEMP table; it does not simulate native Restore. The unread
INT4/BYTEA cursor and TEMP prepared SELECT are opened before Restore. The same
unread cursor's current Columns/ColumnTypes are checked before replacement,
after successful publication and after every refusal against independent names,
both scan types and Nullable(value,known) tuples and the saved snapshot.
Saved column metadata is rechecked after consumption without querying metadata
on the exhausted cursor. The deliberately blocking final TEMP view adds its
namespace/relation/rewrite identities, internal ownership edge and persistent
projected-column dependency edges to the final refusal snapshot.
Four held crypto statements are prepared after relocation. Every wrong-version,
unknown-provider, malformed-manifest, late-SQL, cancellation, private-TEMP-placement
and retained-dependent-TEMP-view refusal uses the same postcondition: unchanged
persistent dump, procedure/consumer/owner identities, exact dependency tuples,
TEMP identities/data/metadata, fixed held/direct results, and fresh rolled-back
default/generated/CHECK/index/view/nested evaluation plus failed-write atomicity.
Provider validation rejects simultaneous crypto and fuzzy markers before provider
dispatch; the private mutation unit checks both descriptor and parent validation.
These are source assertions requiring fresh selected native and pinned-reference
proof; test definitions alone are not passing evidence.

```sh
make check RUN='^TestReferencePgcrypto(DigestVectors|HMACVectors|OverloadsAndParameters|DiagnosticsAndNull|InstalledIdentity|ConsumersAndControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePgcrypto(DigestVectors|HMACVectors|OverloadsAndParameters|DiagnosticsAndNull|InstalledIdentity|ConsumersAndControls|FeatureSmoke)$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestReferenceBinaryHash(Vectors|MD5Overloads|Consumers|Controls|Diagnostics|Quoted|Metadata|Inference|PreparedOwnership|Smoke)$'
make check REFERENCE=1 RUN='^TestReferenceBinaryHash(Vectors|MD5Overloads|Consumers|Controls|Diagnostics|Quoted|Metadata|Inference|PreparedOwnership|Smoke)$'
make check RUN='^TestPgcrypto(ProviderIdentity|ExpressionIdentity|OwnerMetadata|ControlRemap|OverloadResolution|RawVectors|StrictDemand|Cancellation)$'
make check RUN='^(TestExtensionOwner(Metadata|ManifestAdmission)|TestIntarrayProviderIdentity)$'
```

Selected checks use external900-second deadlines; the root alone runs these
after edits. Whole-project Docker-free verification remains `make quickcheck`.
The standalone public smoke source is supplied in the OMP session; it is not a
test fixture import or a duplicate committed runner.


Whole-project Docker-free proof uses quickcheck only after edits stop. The actual
standalone public source `local://available-extensions-smoke.go` is an OMP session
artifact, not a committed executable. Root materializes it outside the repository
at absolute S, runs `timeout 60s go run "$S"` from the assigned worktree, records
resolved argv/cwd/exit/stdout/stderr and removes the materialization afterward.
Quiet exit0 requires exact A0/V0 and A1/V1 metadata/rows, absent typed results,
held statements through real rollback/installation, callable Hotel, exact full
direct/Prepare42703 without handles and genuine retained native Rows through
Dump/Reset/Restore. The actual five-field three-source join must yield J0/J1
through private installation, peer isolation, rollback, commit, Reset and Restore,
including both availability orders and peer handles prepared before commit.
Fresh handles are prepared after generation-replacing controls. FeatureSmoke
supplements rather than replaces that source; the component alone forces the
mid-opening publication boundary.

### Visibility utility boundaries

The affected visibility utility-boundary scenarios now assert supported MV
creation/count/stored rows and cleanup in both backends, instead of obsolete
native syntax rejection. Validate those changed contracts in both modes:
`make check RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
and
`make check REFERENCE=1 RUN='^TestReferenceVisibilityInquiry(Boundaries|UtilityBoundaries)$'`
(900 seconds each).

### Timezone lookup views

Timezone lookup coverage uses `RUN='^TestReferenceTimezoneViews'` in both
`make check` and
`make check REFERENCE=1` (900 seconds per invocation).
Its smoke selector is `RUN='^TestReferenceTimezoneViewsFeatureSmoke$'` in both
modes. Independent rows/types, full55000/42703/42804/42883 diagnostics, declared
metadata, nil/typed NULL predicate parameters, session/transaction capture,
PUBLIC SELECT and symbolic native controls are asserted before backend comparison.
WriteAnalysis and InsertAnalysis preserve existing supported semantic errors before
read-only rejection; VALUES column-reference0A000 is an explicit native capability
boundary, not a PostgreSQL parity claim. Smoke includes nil-bound empty metadata
and semantic-error savepoint recovery. Provider units exercise DST transitions,
negative DST, historical matching, inventory and cancellation without public SQL.

### PG_USER coverage

PG_USER coverage uses `RUN='^TestReferencePGUser'` in both
`make check` and
`make check REFERENCE=1`, with a 900-second deadline
per invocation. The smoke selector is `RUN='^TestReferencePGUserFeatureSmoke$'`.
The shared scenarios independently specify the nine-column LOGIN projection,
OID identity through a separately owned sequence, direct privilege flags,
PUBLIC readability, constant eight-star masking, typed NULL configuration,
finite/infinite expiry, full diagnostics and direct/prepared error stages,
retained snapshots, composition, cancellation and native-control boundaries.
Password setup is deliberately excluded from the recorder and diagnostic
failure text. Native Dump replay uses the actual emitted local SQL, not a
PostgreSQL control substitute.
Supplemental typed provider/saved-root/cancellation checks use
`make check RUN='^(TestPGUser|TestTableViewSummaryBuiltinIdentity)'`.
Related reference regression uses
`RUN='^(TestReferencePGUser|TestReferenceTimezoneViews|TestReferenceRegtype|TestReferencePGTableViews)'`
in both quick and REFERENCE=1 modes (900 seconds per invocation). This preserves
existing timezone routing, allocated regtype identity expectations and scoped
summary inventories while checking pg_user view classification.

For initial oracle discovery, temporarily filter native backends in the common
scenario helper as described above, then run
`make check REFERENCE=1 RUN='^TestReferencePGUser'`.
The exact postgres-only subtest selector is
`RUN='^TestReferencePGUser[^/]*$/^postgres$'`; full diagnostics can be selected
with `RUN='^TestReferencePGUserDiagnostics$/^postgres$/./^(direct|prepared)$'`.
`TestReferencePGUserOracleCatalog/postgres` additionally checks release attribute
types, typmods, collations and relation/rowtype identities, and independently
asserts the exact `pg_views` definition (differences print under).
These allocator assertions are
release-only research, not cross-backend role or relation allocator parity.
Remove the temporary backend filter before recording the missing-feature quick
regression and before all final dual-mode runs. Preserve the permanent
PostgreSQL-only `pg_attribute` research guard and native-only control phases.
Docker/image failures in an explicitly enabled real run MUST fail visibly, not
be skipped or mocked. Fix slow/hanging tests rather than increasing deadlines.

### Legacy pg_group verification

Use these small selectors in **both** Docker-free and pinned PostgreSQL modes,
with an external900-second deadline for each command:

```sh
timeout 900s make check RUN='^TestReferencePGGroup(Shape|OracleCatalog|Diagnostics)$'
timeout 900s make check RUN='^TestReferencePGGroup(Membership|GrantorMultiplicity)$'
timeout 900s make check RUN='^TestReferencePGGroup(Freshness|Snapshots|Visibility)$'
timeout 900s make check RUN='^TestReferencePGGroup(Controls|FeatureSmoke)$'
# Repeat each selected command above with REFERENCE=1.
timeout 900s make check RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGTableViews(Builtins|Controls)$'
timeout 900s make check RUN='^Test(PGGroup(Descriptor|Projection|RoleRoot|Cancellation)|TableViewSummaryBuiltinIdentity)$'
timeout 900s make check RACE=1 RUN='^TestPGGroup(RoleRoot|Cancellation)$'
timeout 900s make check RUN='^TestReferencePGGroupFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferencePGGroupFeatureSmoke$'
```

Shared public scenarios independently specify the exact NAME/OID/_OID fields,
current NOLOGIN predicate, real pg_roles identity relationships, direct-edge
element bags preserving grantor multiplicities, all-false membership options,
nonnull empty arrays, role defaults/overrides, unrelated-reader disclosure,
credential-column diagnostics, full diagnostic fields/positions and direct/
prepared error phases. Generated OIDs are backend-local, not allocator goldens.
The group helper validates raw consumer and middleware-owned buffers, preserves
element multiplicity and ignores only unpromised row/array order. Password setup
never enters the recorder or diagnostic failure output.

Oracle-first discovery may temporarily filter native backends **only** in the
new group scenario helper under the policy above. Remove the filter before
native-red and every completed dual-mode verification. Keep the permanent
PostgreSQL-only pg_attribute/pg_type structural witness and actual native-only
retained Rows/metadata, catalog-write/row-lock exclusions, cancellation and
Dump/Reset/Restore phases. Native controls preserve role identities/edges and
stored symbolic consumers; real mode replays the **actual native-emitted SQL**
on PostgreSQL using its own group/member identities, never a substitute Reset API.
Valid native writes fail0A000, not an invented PostgreSQL55000 join-view error.

The standalone public Driver/database/sql smoke program in the PG18-VIEW-GROUP
living plan is materialized temporarily at `/tmp/pg18-view-group-smoke.go`.
From the worktree, run `timeout 900s go run /tmp/pg18-view-group-smoke.go`.
Expected exit0 with exactly `pg_group public smoke: PASS`: three-column metadata,
empty→direct all-false member→empty, held unrelated-reader freshness, LOGIN hides/
NOLOGIN restores the same OID/list, and credential42703. Record observations and
remove the temporary source; it is not a committed test or production dependency.
Whole-project Docker-free verification remains `make quickcheck`; selected
dual-mode proof and the smoke are not replaced by quickcheck or finalcheck.

### Size parsing and formatting

Size parsing/formatting coverage uses the untagged `TestReferenceSize` family:
independent exact binary/fractional/signed-limit vectors, every promotion
boundary, numeric scale/specials, full diagnostics and stages, actual held
statements and recovery, NULL-only/empty raw metadata, expression consumers and
write/transaction atomicity. Stored overloads use compact/pretty semantic deparse
replay rather than incidental formatting goldens. Native controls preserve
retained rows/metadata and failed replacement atomicity; pinned PostgreSQL
consumes the actual native-emitted Dump SQL. Every changed definition must run
in both modes before review, each within the 900-second deadline:

```sh
make check RUN='^TestReferenceSize'
make check REFERENCE=1 RUN='^TestReferenceSize'
make check RUN='^TestReferenceSizeFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSizeFeatureSmoke$'
make check RUN='^Test(Size|Math|Numeric)'
make check RUN='^TestSize'
```

The smoke expects 1048576 INT8, `1024 bytes`, `-11 kB`, `10.5000 bytes`,
beyond-BIGINT `10240 PB`, typed TEXT NULL metadata, exact malformed-unit and
overflow records, held recovery, no failed-write prefix, and faithful stored
overloads/control replay. The shared MathPlan and numeric codec seams also
require existing math/numeric regression coverage; selected checks do not
replace ordinary quickcheck or post-merge finalcheck.

Size consumer witnesses retain the explicit native internal-CHAR coercion
boundary alongside independent PostgreSQL results; shared COUNT and SUM
arguments, INSERT affected counts/state reads and UPDATE RETURNING prove the
supported composition paths. Deparse catalog-query metadata independently checks
native known-nullable TEXT versus PostgreSQL unknown nullability, without changing
size-result metadata. Retained descriptors are captured before cursor exhaustion
and remain asserted after replacement/consumption.

### Shared-comment catalog coverage

The untagged `TestReferencePGShDescription` family independently asserts the
three-column relation2396 contract, shared ROLE comment identities/contents,
local catalog separation, ordinary readability, full diagnostics, transactions,
held freshness, detached rows/metadata and stored-view/composition behavior.
Role IDs come from a separately owned helper relation's `pg_class.relowner`;
schema IDs come from that relation's `relnamespace`, not an adjacent catalog.
Native bootstrap has no seeded shared comments, while PostgreSQL can have
legitimate global rows: scenarios filter exact object/class pairs, never assert
whole-cluster row-count parity. Native-only Reset/Dump/Restore phases preserve
shared roles outside local replay and feed actual emitted local SQL to PostgreSQL.
Pure `TestPGShDescription` units cover descriptor/PUBLIC ACL, uint32 maximum and
uint64 non-aliasing, detached captures, inquiry agreement, saved-scope nested
opening and entry/in-loop cancellation without private public-SQL duplicates.

Run the same completed definitions in both modes, each within900 seconds:

```sh
make check RUN='^TestReferencePGShDescription'
make check REFERENCE=1 RUN='^TestReferencePGShDescription'
make check RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGShDescriptionFeatureSmoke$'
make check RUN='^TestPGShDescription'
```

### Sequence value view

`TestReferencePGSequencesShape`, `Definitions`, `Values`, `Privileges`, `Temp`,
`Demand`, `Transactions`, `Diagnostics`, `CTE` and `Controls` prescribe independent
pinned PostgreSQL18.0 outcomes, including exact cache high-water/NULL masks,
all eleven descriptors, full English diagnostic records, physical TEMP exit,
actual emitted Dump replay and cancellation cleanup. PostgreSQL-only pg_type
identity links supplement native actual descriptor/type-link units; the TEMP
namespace is independently obtained from the public pg_sequences row. The
session namespace inquiry scenarios additionally cover pg_my_temp_schema and
physical TEMP/TOAST ownership. Shared direct/prepared behavior is not skipped.

```sh
make check RUN='^TestReferencePGSequences'
make check RUN='^TestReferencePGSequences' REFERENCE=1
make check RUN='^TestPGSequences(Descriptors|Provider|ACL|Demand|Concurrency|Cancellation|CTE)'
make check RUN='^(TestPGSequences(Concurrency|Cancellation|CTE)|TestReferencePGSequences(Demand|Transactions|Temp|CTE))' RACE=1
```

Use external900s deadlines. Changed reference definitions require both native
and real pinned modes before review. Whole-project Docker-free `make quickcheck`
and post-merge `make finalcheck` do not replace selected proof or the standalone
public smoke. Demand units inspect real manager holders/queues, including
definition/count/stored/join RX, rejected-name/FALSE no RX and terminal release.

### Sequence definition catalog discovery

The shared `TestReferencePGSequence` family covers the eight-column
`pg_catalog.pg_sequence` contract, exact definition options rather than generator
state, catalog identities, transactional visibility, repeatable-read peer
ALTER/CREATE/DROP, first-Prepare versus first-Execute timing, privileges and
other-session temporary definitions, full diagnostics, cancellation and actual
native Dump replay. `TestReferencePGSequenceFeatureSmoke` selects the integrated
definition lifecycle and control replay witness.

The privilege fixture keeps PostgreSQL's four false `has_schema_privilege` /
`has_sequence_privilege` assertions. Native mode does not implement those
predicates: a qualified `nextval` must report private-schema denial, while
numeric-regclass `nextval`, `currval` and `setval` must independently report
sequence denial with exact existing diagnostics before the same metadata read.
Denied `nextval` excludes USAGE/UPDATE and denied `currval` excludes USAGE/SELECT.
Failed native Restore appends a supported `CREATE SEQUENCE ... CACHE 0` to the
actual dump, expects option-binding SQLSTATE 22023, and preserves the complete
original definition bag; arbitrary ALTER is outside Restore's accepted input.

The oracle discovery observed retained RR definitions and row bags. Prepare
inside BEGIN retained start15/cache9 after peer ALTER; a statement prepared
before BEGIN captured start21/cache4 at its first execution. Discovery filters
are removed: both commands run the shared completed scenario. Private-schema
fixtures use default absent PUBLIC privileges with the independent false
predicates or denied-operation witnesses above, without native REVOKE support.

```sh
make check REFERENCE=1 RUN='^TestReferencePGSequence'
make check RUN='^TestReferencePGSequence'
```

After implementation, include all migrated definition probes in both modes:

```sh
make check RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check REFERENCE=1 RUN='^TestReference(PGSequence|SequenceCacheBoundsRow|SequenceHugeCacheArithmetic|SequenceCreateRestart|AlterTypeIdentityTargets|AlterTypeIdentityFailuresAndPrephase)'
make check RUN='^TestReferencePGSequenceFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGSequenceFeatureSmoke$'
make check RUN='^TestPGSequence'
```

Each selected check invocation has a 900-second deadline. The finalcheck runs
after merge under its single 1800-second deadline; neither gate replaces the
selective dual-mode runs.

### Sequence metadata view oracle

The `TestReferencePGSequences` family defines the eleven-column `pg_sequences`
oracle independently of backend comparison. `Shape`, `Demand`, `Transactions`,
`Diagnostics`, `Frames` and `CTE` cover exact types and fixed catalog identities,
admitted-row volatile helper locks, retained RR definitions with live
generator/ACL state, complete diagnostics, lexical-frame-safe correlated
projections and volatile CTE producer frontiers.
`Definitions`, `Values`, `Privileges`, `Temp` and
`Controls` cover options, cache high-water values, unreadable/uncalled NULL,
physical TEMP visibility and actual native control replay. These are test
expectations, not a claim that the native view implementation has passed.

RR drop/recreate diagnostics interpolate the original sequence OID captured
before peer DDL, never a hard-coded allocated OID or the replacement identity.
Even a definition-only admitted read expects `XX000`; the subsequent query
expects `25P02`. Namespace and sequence definitions, including the owner identity,
remain from the RR definition snapshot. The owner name is presented from the
current statement role root rather than a retained role-name snapshot. Current
role membership produces NULL/value/NULL, and current generator state is read
independently.

The Recreate witness issues DROP and CREATE as separate commands on the same
peer connection; it tests real identity replacement, not multi-statement SQL.
Full, definition-only and FALSE `FOR UPDATE` queries must succeed directly and
through retained prepared execution. Their SQL rowmark admission still matches
the bound manifest; the represented view has no physical tuple marks, while
admitted rows retain the normal per-sequence helper locks.

Ordinary derived sources capture the caller's lexical frame after nested binding
restores that scope. `TestPGSequencesAdmissionDerivedRowLockFrame` binds the actual
derived `FOR UPDATE` shape and proves that its local residual predicate crosses
the row-lock boundary into direct child projection columns in the child's exact
frame. The filter uses one captured evaluation frame for both admission and
residual evaluation. Foreign, zero or mixed frames remain whole-expression
barriers; CTE boundaries remain distinct and are not admitted by this mapping.
The existing Demand witness still requires a-only helper RX, with no b helper RX.

`TestPGSequencesConcurrency` observes the actual manager request on the exact
sequence identity: RESTART queues ShareRowExclusive behind the view helper's
transaction-owned RowExclusive. Closing Rows does not release that lock;
rollback admits RESTART, whose fresh last_value is NULL and nextval is 19.
Cancellation/join cleanup runs even when the queue assertion fails.

`TestReferencePGSequencesCTE` and `TestPGSequencesCTE` exercise direct,
ordinary-derived, stored-view and nested stored-view sources with default,
`MATERIALIZED` and `NOT MATERIALIZED` hints. A consumed volatile CTE completes
both sequence rows behind one producer even when an outer restriction returns
only one name; shared references retain that producer rather than independently
folding its restrictions. The tests observe helper RX holders and a real blocked
helper queue on the sequence absent from the final rows, cancellation and terminal
cleanup/recovery. Prepare opens no helper, and unused CTE bodies acquire no helper
locks. `TestPGSequencesCTEStoredGraph` and `TestPGSequencesCTEStoredGraphAuthority`
inspect real immutable stored definitions without opening providers; shared
acyclic sources are allowed, while invalid references and active-path cycles use
the existing catalog/view graph errors.

Malformed inferred REGTYPE input is an explicit PostgreSQL wire-only control:
OID-input `22P02` includes `unnamed portal parameter $1 = '...'`. The native
driver has no PostgreSQL parameter input codec and must not fabricate that
context. Shared direct and retained prepared routes still assert numeric
inferred REGTYPE inputs 20/23/21 and bigint/integer/smallint outputs. Explicit
symbolic input through `text::regtype` independently expects full `42704`
diagnostics and recovery using the same retained statement.

```sh
make check RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Shape|Demand|Transactions|Diagnostics|Frames|CTE)$'
make check RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGSequences(Definitions|Values|Privileges|Temp|Controls)$'
```

Each command requires an external 900-second deadline. Before production
authorization, use the same reference selector with `/postgres$` appended to
exercise only the pinned oracle; unqualified definitions include native mode
and supply its missing-view behavioral red. No discovery backend filter or
deliberately failing observation helper remains.

### Dependency catalog

The dependency catalog family independently specifies exact represented edge bags,
all seven result descriptors, owner-specific diagnostics, supported private
sequence changes, retained cursors and native controls with actual SQL replay.
Run the identical complete family in both modes, each with a900-second deadline:

```sh
make check RUN='^TestReferencePGDepend'
make check REFERENCE=1 RUN='^TestReferencePGDepend'
make check RUN='^TestReferencePGDependFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGDependFeatureSmoke$'
```
Changed existing default definitions must also run in both modes; the family
prefix does not select them. They preserve genuine direct and prepared CREATE,
default INSERT, data/provider sentinels and existing excluded sequence-expression
controls without separate duplicated PostgreSQL-only success bodies:

```sh
make check RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
make check REFERENCE=1 RUN='^TestReference(SequenceLatestIntegrationDefaultBoundary|StringCoreRegclassDefaultSourceConsumerRoutes|StringCoreRegclassDefaultBoundary)$'
```

Each selective invocation has a900-second deadline. Source-route migration
definitions are additional named selections, not replacements for this family
or these changed existing definitions.


Supplemental collector/walker units use
`make check RUN='^TestPGDepend'`.
These do not replace the dual-mode runs, ordinary quickcheck or post-merge
finalcheck. Public capability boundaries remain explicit: no REGCLASS column
storage, set-operation query rollout, transactional CREATE TABLE/ALTER DEFAULT
or native TEMP-table creation is implied by metadata reading.

### Table and index summary views

The `TestReferencePGTableViews` family independently pins exact eight/four-field
summaries, raw metadata, current/quoted ownership and names, unfiltered denied
object disclosure, immutable captures/held refresh, sticky index history and
failed ordinary/concurrent publication, reconstructed user-definition replay,
represented builtin9/4 membership and full released definitions, diagnostics,
transaction overlays/RR differences and native logical control normalization.
The controls replay actual native SQL Dump on PostgreSQL. Column GRANT/REVOKE
fixture setup remains a separate phase from ACL-neutral native Dump replay:
explicit ACLs are not serializable. Internal units exercise source capture and
descriptor replacement without weakening that control boundary.
Run every new/migrated reference definition in both modes, each with a900-second
deadline, before review:

```sh
make check RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check REFERENCE=1 RUN='^(TestReferencePGTableViews.*|TestReferenceViewSmokeCatalogReplayControls)$'
make check RUN='^Test(TableSummary.*|TableViewSummaryBuiltinIdentity)$'
make check RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
make check REFERENCE=1 RUN='^TestReferencePGTableViews(Lifecycle|IndexHistory|FailedIndexHistory|Visibility|Builtins|Definitions|Controls)$'
```

Expected feature observations include DROP-last-index preserving true, a fresh
table starting false, denied-schema/table metadata disclosed on both backends,
four nonNULL released builtin SELECT definitions, executable dynamic user-view
copies after renames/replacement and Restore producing false/true/false for
never/currently/formerly indexed tables. No discovery gate remains in final tests.

The `TestReferencePGIndexes` acceptance family pins the five-column view shape,
compact definitions, held execution, lifecycle, supported transactional overlays,
repeatable-read peer CREATE boundary, independent disclosure policy and full
diagnostics. Controls replay actual native Dump SQL on PostgreSQL; the existing
ready-invalid concurrent hash witness also checks descriptor enumeration.
Run the complete affected selector in both modes (900 seconds per invocation):

```sh
make check REFERENCE=1 RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^(TestReferencePGIndexes.*|TestReferenceIndexMaintenanceMetadataOracle|TestReferenceDropViewIndexWrongKind|TestReferenceConcurrentIndexHashInvalidQuery|TestReferenceConcurrentIndexFeatureSmoke)$'
make check RUN='^TestReferencePGIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGIndexesFeatureSmoke$'
```

Initial pinned oracle discovery passed before production. All scenario-local
discovery filters are removed; both commands now execute the same shared bodies,
including native behavioral-red before implementation. A filtered native run
is not verification. PostgreSQL exposes denied underlying objects here;
native acceptance intentionally requires schema USAGE and parent table SELECT
or SELECT on any live column. No global fixture behavior is changed.
Private `TestPGIndexes` units exercise real SetObjectACL/AuthorizationView
captures: table versus live non-indexed column grants, PUBLIC, ordinary owner,
superuser, schema denial, dropped stable attributes, revocation, retained old
captures and original invoker dispatch. Structural metadata units remain
ACL-neutral. Run alongside the shared reference gate:

```sh
make check RUN='^(TestPGIndexes.*|TestIndexMaintenanceMetadata.*)$'
```

### Index catalog provider boundaries

PG-only relam/vector/type-provider catalog controls are genuine boundary coverage,
not native compatibility passes or skipped implementation. Native reduced-catalog
metadata is asserted independently through projectIndexMetadata package functions;
do not add unowned SQL catalogs to make a discovery query run. See
[hash design](../../docs/design/PG18-INDEX-HASH.md). Generate retained parser productions
before selective checks; generation/formatting/verification belong to the root.

### Visibility source-route isolation

extra identities. The visibility source-route scenario keeps the complete
unfiltered two-peer bag and creates an unrelated `public.t` to exercise
isolation from the old generic basename.

### Cast metadata catalog discovery and smoke

The shared `TestReferencePGCast` family asserts the complete independently
specified70-row supported boundary, including explicit xid8→xid (function5071),
exact six-column shape and raw codecs,
fixed type/function identities, generated row identity invariants, direct/
prepared/held consumers, full diagnostics, omissions versus automatic coercion,
PUBLIC SELECT and native retained-row/Reset/Restore controls. PostgreSQL reads
are restricted to the same candidate pairs; additional PostgreSQL catalog rows
are legitimate. Domain identity via pg_typeof is a PostgreSQL-only phase;
native domain creation/conversion plus its unchanged exact complete bag excludes
domain rows without a new type identity API.

The genuine feature smoke combines empty shape, representative eight-row
metadata, held valid/missing/NULL/error/recovery, signed OID conversion, BOOL
text, JSON I/O, VARCHAR sizing/atomicity, UTF8 diagnostic and actual native user
Dump replay against PostgreSQL. Discovery-only filters have been removed.
Run the same completed definitions in both modes before review/integration:

```sh
make check RUN='^TestReferencePGCast'
make check REFERENCE=1 RUN='^TestReferencePGCast'
make check RUN='^TestReferencePGCastFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGCastFeatureSmoke$'
make check RUN='^TestCastCatalog'
make check RUN='^TestPGCast'
make check RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
make check REFERENCE=1 RUN='^TestReference(OID|CastExplicit|Character|JSON|JSONB|PGSequence|PGDepend|PGDescription|QueryOrderCatalogChar)'
```

Every quickcheck invocation has a900-second deadline. Private units exercise
owner planning/application in all claimed and excluded contexts and projection
ownership/cancellation/generation retention, without issuing SQL. Selective
runs and smoke do not replace ordinary quickcheck or the post-merge finalcheck
under its single1800-second deadline.


### Constraint metadata contract

The `TestReferencePGConstraint` scenarios specify the released 28-column
`pg_constraint` shape independently, including actual n/c/p/u/f/x definitions,
stable sparse keys, CHECK encounter order, FK operator and action vectors,
partition provenance, identity-preserving lifecycle changes, transactional
visibility, logical native controls, and complete SQL diagnostics.
Generated object identities are checked relationally, not against PostgreSQL's
allocator. CHECK `conbin` is a typed captured expression: its internal tree bytes
are backend-specific; presence and deparsed semantics remain asserted.
The adjacent `pg_attribute` scan is an explicit PostgreSQL-only validation phase;
native exact column ordinals, origins, types and nullability are independently
covered by `TestPGConstraintShapeAndOwnership`, while all 28 driver fields remain
shared. Operator signatures and exact builtin identities use the existing shared
`regoperator` scalar API, not an unavailable native `pg_operator` scan. Namespace
movement uses a separately named destination relation's `pg_class.relnamespace`,
with nonzero/distinct-from-public identity and SAME-held constraint reexecution;
it does not depend on native `pg_namespace` or regnamespace implementation.

Run these unfiltered selected definitions in both modes with an external
900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Shape|Kinds|ForeignKeys)$'
make check RUN='^TestReferencePGConstraint(Shape|Kinds|ForeignKeys)$'
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Provenance|Lifecycle|Transactions)$'
make check RUN='^TestReferencePGConstraint(Provenance|Lifecycle|Transactions)$'
make check REFERENCE=1 RUN='^TestReferencePGConstraint(Controls|Diagnostics|FeatureSmoke)$'
make check RUN='^TestReferencePGConstraint(Controls|Diagnostics|FeatureSmoke)$'
```

`TestReferencePGConstraintExclusionSignatures` independently specifies canonical
exclusion equality identities: CIDR/INET and CIDR domains use INET operator1201;
OID and every admitted OID alias (including alias domains) use OID operator607;
VARCHAR uses TEXT operator98 and enum uses anyenum operator3516. Direct,
fresh-prepared and SAME-held reads assert exact key/operator vectors and real backing identities. A retained reader
prepared before each exclusion is created must still read unrelated CHECK
metadata afterward; a valid exclusion cannot poison catalog-wide discovery.
The complete scenario is unfiltered; run both modes:

```sh
make check REFERENCE=1 RUN='^TestReferencePGConstraintExclusionSignatures$'
make check RUN='^TestReferencePGConstraintExclusionSignatures$'
```


Feature smoke: `make check RUN='^TestReferencePGConstraintFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferencePGConstraintFeatureSmoke$'`.
It creates all six admitted kinds, asserts the exact empty star description,
CHECK keys and semantics, actual FK supporting-index/operator relationships,
held-reader rename freshness, and rollback of actual ADD/DROP operations.
Component proof and selected race coverage exercise catalog-owned descriptors,
actual projection/capture/publication, and comparison-family metadata directly:

```sh
make check RUN='^TestPGConstraint(ShapeAndOwnership|Projection|Capture|Publication|OperatorMetadata|ExclusionMetadata)$'
make check RACE=1 RUN='^TestPGConstraint(Capture|Publication)$'
```

Promoted owner assertions and retained lifecycle/provenance coverage require
both selected modes as well:

```sh
make check RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|Diagnostics|NotValid|Controls|Provenance|Transactions|PrimaryPrerequisite|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check REFERENCE=1 RUN='^TestReferenceAlterAddConstraint(Kinds|ExistingData|Diagnostics|NotValid|Controls|Provenance|Transactions|PrimaryPrerequisite|SuccessorWitness|ValidityAttributes|FreshLikeValidity)$'
make check RUN='^TestReferenceInheritance(Constraints|LocalConstraints)$'
make check REFERENCE=1 RUN='^TestReferenceInheritance(Constraints|LocalConstraints)$'
make check RUN='^TestReferenceConstraintRename(Transactions|DeferredExclusion)$'
make check REFERENCE=1 RUN='^TestReferenceConstraintRename(Transactions|DeferredExclusion)$'
make check RUN='^TestReferencePartitionConstraints(NonuniqueCatalog|IncompleteReuse)$'
make check REFERENCE=1 RUN='^TestReferencePartitionConstraints(NonuniqueCatalog|IncompleteReuse)$'
```

Selected proof does not replace whole-project Docker-free `make quickcheck`
or the root-owned post-integration finalcheck.

### Daitch-Mokotoff selected proof

`TestReferenceDaitchMokotoffCodes` specifies every published PostgreSQL18 manual
and REL_18_0 regression array, preserving complete alternative element order.
Direct literal/parameter, newly prepared and repeatedly held calls assert `_TEXT`
metadata and raw carriers. Boundaries independently require SQL NULL for NULL,
empty and unencodable input, six zero digits for A, skipped punctuation inside
phonemes, narrow Latin1/special-letter handling and no 255-character limit.
Diagnostics prescribe complete English fields for absent installation, signatures,
quoted names, missing schema, off-path lookup and recognized scalar decorations.
The Public and Relocated diagnostic cases retain `public.daitch_mokotoff` and
`dm_addon.daitch_mokotoff` in DISTINCT/ORDER BY/FILTER/OVER primary messages.
Direct Query and Prepare failures require whole42809 records with Position8,
ERROR severities and empty Hint/Detail and other unsourced fields.

Installation checks fuzzystrmatch1.2 availability, real extension/procedure
identity relationships, immutable/strict/parallel-safe text→text[] metadata,
transaction/savepoint rollback, duplicate notices and invalid-version atomicity.
Trusted ordinary installation independently checks SAME-connection current_user
and session_user before installation and after reset. The extension remains
installer-owned (`dm_installer`); the procedure belongs to fixed bootstrap
superuserOID10. Its exact declared name is native `postgres` versus pinned
fixture initdb `stuntgres`, not the fixture's separate native-replay postgres role.
Native fuzzystrmatch membership has ten selected overloads: DM, four
Levenshtein signatures, three Metaphone signatures and two Soundex signatures.
The upstream-only undocumented `text_soundex` alias is not falsely asserted
absent on PostgreSQL. Selected Soundex calls now execute: qualified
soundex('John') is J500 and difference('Ann','Anne') is4.
Native pg_proc does not expose proargtypes: its exact argument signature25 is
checked against the real installed descriptor in the provider identity unit,
while an explicit PostgreSQL-only metadata phase asserts proargtypes25. The
remaining exposed identity/properties and callable overload outcomes are shared.
Stored expressions consume the function in views, defaults, CHECK constraints
and STORED generated columns; a late failed multirow insertion preserves all
prior rows. Controls share those SQL outcomes and separately exercise native
Dump/Reset/Restore, retained array cursors, independent Driver attachment,
symbolic named-owner redump and failed/canceled replacement preserving executable
consumers. Shared named-owner and schema-DROP/CASCADE SQL outcomes are distinct
from native missing-owner replay and staged-prefix I/O/cancellation. The same
held DM statement returns its complete array before and after replacement,
using existing same-shaped builtin-array reanalysis; no stale-alias capture or
universal stale-statement fence is claimed.

```sh
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceDaitchMokotoff(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestReferenceAvailableExtensionsFiltering$'
make check RUN='^TestDaitchMokotoff(PublishedCodes|Boundaries|Cancellation|ProviderIdentity|ExpressionIdentity|Remap)$'
```

The feature smoke expects complete John/Bierschbach/brzęczy arrays, NULL/empty,
qualified off-path resolution, stored generated/view values and reusable held
execution. Precanceled no-output/ordinary context-error assertions are native-only;
pq fast precancellation has no stable nil-output guarantee. Selected proof does
not replace whole-project quickcheck, standalone feature smoke or finalcheck.

### Levenshtein selected proof

The nine `TestReferenceLevenshtein` definitions independently specify distances,
weighted substitutions, bounded exact/greater-than behavior, signed release
int32 cases, Unicode character limits and empty bypass, strict NULL/demand,
complete diagnostics and installed identity. Direct literals/binds, fresh
Prepare and SAME held frames assert INT4 metadata/raw values and recovery after
execution errors. Invalid INT4 bind failures retain the pinned PostgreSQL portal
Context on both adapters through the existing parameter-input annotation policy.

Diagnostics includes positional-prefix/named-suffix syntax in all four selected
signatures and public/relocated qualification. The unnamed installed overloads
still fail normal missing-signature resolution; carrying labels through the
ordinary call grammar does not enable named matching. Parser units retain the
prefix's empty label slots, suffix labels, value order and source coordinates.
Bounds/ReleaseBoundedInt32 independently specifies six nonempty/max0 release
overflow cases, covering wrapped cost sums, theoretical min/max products,
substitution reduction, pruning-disable choices and active asymmetric residual
slides/narrow-band extension. These pathological exact outputs are release
witnesses, not ordinary mathematical bound or sentinel guarantees.

Installation requires four genuine selected identities/extension edges on both
backends, with total native membership10 including retained DM, Metaphone and Soundex. Stored SQL uses a
relocated provider in defaults/generated/CHECK/view, nested/CASE, grouping and
join expressions. Failed atomic inserts leave original rows unchanged. Controls
share the SQL setup, then use real native APIs for Dump ordering, exact
ten-member symbolic manifests, retained INT4 rows, held reanalysis, independent
Driver/named-owner replay and failed/I/O/canceled Restore atomicity. PostgreSQL
has no invented control equivalent.

Run each small set in both modes after changes:
```sh
make check RUN='^TestReferenceLevenshtein(Distances|Costs|Bounds)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(Distances|Costs|Bounds)$'
make check RUN='^TestReferenceLevenshtein(UnicodeAndLength|NullAndDemand|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(UnicodeAndLength|NullAndDemand|Diagnostics)$'
make check RUN='^TestReferenceLevenshtein(Installation|StoredExpressions|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceLevenshtein(Installation|StoredExpressions|Controls)$'
make check RUN='^TestReferenceDaitchMokotoff(Installation|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceDaitchMokotoff(Installation|Controls|FeatureSmoke)$'
make check RUN='^TestLevenshtein(Distance|Weights|Bounds|ReleaseInt32|Unicode|Cancellation|ExpressionIdentity|Remap)$'
make check RACE=1 RUN='^TestReferenceLevenshtein(NullAndDemand|Controls)$'
```

Use external900-second deadlines. The standalone public-consumer smoke is
materialized outside the checkout from the session's
`local://fuzzy-levenshtein-smoke.go` and run from the repository root:
```sh
timeout 900s go run "$SESSION_TMP/fuzzy-levenshtein-smoke.go"
```
It must exit silently0 after independently checking distance3, weighted3,
bounded exact3/greater-than2, Unicode255/nonempty256 error/empty256 bypass,
typed NULL, held weighted/bounded frames, retained DM codes and membership10,
stored recipes, Dump/Reset/Restore, independent Driver replay and failed-Restore
preservation. Selected proof and smoke do not replace whole-project Docker-free
quickcheck or post-integration finalcheck.

### Metaphone selected proof

The seven `TestReferenceMetaphone` definitions independently specify release
codes, primary/alternate differences,255-byte source/output boundaries, empty
cap bypass, strict NULL, punctuation and multibyte limits. Codes and boundaries
exercise literals, binds, fresh Prepare and SAME held frames; descriptors and raw
TEXT values remain checked for NULL and empty results. Diagnostics checks all
structured fields, function visibility, named/VARIADIC/decoration rejection,
literal and portal INT4 input, and held execute-error recovery.
Late full-input Germanic classification is independently checked with `SIO`,
300 punctuation bytes and a trailing `W`; `bagallegos` checks continued alternate
generation after the primary code fills, yielding `PKLK` / `PKKS`.

Installation checks three real procedure identities, TEXT metadata,
immutable/strict/parallel-safe flags, extension/namespace links, superuser and
trusted bootstrap ownership. Native total membership is8; the oracle's extra
functions are not asserted absent. Private-schema visibility recovery uses real
PostgreSQL USAGE and the supported native schema-ownership transfer, not a new
native schema GRANT capability.

StoredExpressions checks relocated defaults, generated codes, CHECK, views,
WHERE, CASE/nesting, grouping, joins and atomic late write failure. A shared
literal-call view independently returns AK/SM0/SMT before and after Restore,
including independent Driver replay; this proves persisted call arguments and
qualified invocation without depending on identifier quoting in serialized SQL.
Controls shares the SQL phase, then separately checks native Dump ordering,
exact10-member owner manifest, Reset/Restore, old TEXT rows/metadata,
SAME held reanalysis, independent Driver replay and failed/canceled restore
atomicity. PostgreSQL has no fictitious equivalent of these native APIs.

```sh
make check REFERENCE=1 RUN='^TestReferenceMetaphone(Codes|Boundaries|Diagnostics)$'
make check RUN='^TestReferenceMetaphone(Codes|Boundaries|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceMetaphone(Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceMetaphone(Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RACE=1 RUN='^TestReferenceMetaphone(Boundaries|Controls)$'
```

Use external900-second deadlines. FeatureSmoke expects KM/KMP/KMP, AX cap2 AK,
Smith SM0/XMT, Schmidt XMT/SMT, typed NULL/empty/empty-cap bypass, generated/view
rows, WHERE id2, SAME held reuse and cap0 error followed by cap1 success. Native
phases actually restore and reinvoke consumers/functions/held statements,
validate membership10 and reject a failed replacement without losing invocation.
The standalone public-consumer program is retained in session
`local://fuzzy-metaphone-smoke.go`, materialized outside the checkout and run as:

```sh
timeout 900s go run "$SESSION_TMP/fuzzy-metaphone-smoke.go"
```

Expected success is quiet exit0 only after independent rows/type/diagnostic/
held/generated/WHERE/manifest/restore assertions. Selected proof does not replace
whole-project Docker-free quickcheck or post-integration finalcheck.


### Soundex selected proof

The seven `TestReferenceSoundex` definitions independently specify only
`soundex(text) RETURNS text` and `difference(text,text) RETURNS int4`.
Manual18.6 F.16.1 and unchanged REL_18_0/REL_18_6 source ground the expectations;
the runtime remains pinned PostgreSQL18.0 UTF8/C. Initial oracle discovery
confirmed Codes/Boundaries/Diagnostics/Installation before native implementation,
including full metadata and diagnostics. No discovery-only backend filter remains.

Ann/Anne are unequal spellings sharing A500/difference4; Anne/Andrew score2
and Anne/Margaret0. Original-byte adjacency is deliberate: Ashcraft A226,
BHB/B2B B100, B1B B000, D3D D000. Empty and no-ASCII-letter codes are empty TEXT,
not NULL or0000; two such buffers agree4. Inputs have no255-byte limit.
PostgreSQL's documented multibyte limitation is retained, not approximated by
Unicode normalization/transliteration: Émile M400 versus Emile E540.

Direct literals/binds, fresh Prepare and SAME held name/NULL/empty/name frames
assert independent values and complete TEXT/INT4 metadata even with zero rows.
Both runtime-row NULL and the exact literal NULL/division witness demand22012.
Failure expectations include exact English primary, all exposed logical fields
including empties, nil error cursors and preparation/execution stages.
Installation checks genuine namespace/owner/procedure/dependency identities,
while stored views/defaults/generated/CHECKs exercise both result types.
Controls share real SQL setup with PostgreSQL and separately exercise native
Dump ordering, exact10-member named-owner manifest, retained old rows/detached
metadata, held reanalysis, independent Driver replay and failed/canceled Restore
preservation. Native control APIs are not claimed as PostgreSQL facilities.

Run every changed definition in both modes, with external900-second deadlines:

```sh
make check RUN='^TestReferenceSoundex(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSoundex(Codes|Boundaries|Diagnostics|Installation|StoredExpressions|Controls|FeatureSmoke)$'
make check RUN='^(TestReferenceDaitchMokotoff(Installation|Controls)|TestReferenceLevenshtein(Installation|Controls)|TestReferenceMetaphone(Installation|Controls|FeatureSmoke))$'
make check REFERENCE=1 RUN='^(TestReferenceDaitchMokotoff(Installation|Controls)|TestReferenceLevenshtein(Installation|Controls)|TestReferenceMetaphone(Installation|Controls|FeatureSmoke))$'
make check RUN='^(TestSoundex(Kernel|Expression|ExpressionIdentity|ExpressionRemap|ExpressionProperties)|TestDaitchMokotoffProviderIdentity|TestMetaphoneExpressionIdentity|TestLevenshteinExpressionIdentity)$'
```

The standalone public-consumer source is session evidence at
`local://fuzzy-soundex-smoke.go`, materialized outside the checkout:

```sh
timeout 900s go run "$SESSION_TMP/fuzzy-soundex-smoke.go"
```

Expected success is quiet exit0 only after actual independent phonetic/lexical,
byte/NULL/metadata/held/stored/identity/manifest/replacement assertions.
Selected proof and smoke do not replace whole-project Docker-free quickcheck
or post-integration finalcheck.

