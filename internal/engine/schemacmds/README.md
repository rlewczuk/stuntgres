# internal/engine/schemacmds

The schema-object statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): CREATE,
ALTER and DROP SCHEMA, TYPE, DOMAIN, COLLATION, CONVERSION, TEXT SEARCH CONFIGURATION, TEXT SEARCH TEMPLATE and TEXT SEARCH DICTIONARY; CREATE
LANGUAGE and ALTER/DROP LANGUAGE; ALTER DOMAIN RENAME and SET SCHEMA; CREATE FUNCTION, PROCEDURE and
AGGREGATE, ALTER and DROP ROUTINE; cast, operator, operator family, operator
class and access method DDL; COMMENT; CREATE TABLESPACE and ALTER TABLE /
MATERIALIZED VIEW ALL IN TABLESPACE; LOAD and CHECKPOINT; CREATE, ALTER and DROP EXTENSION; the
foreign-data DDL (wrappers, servers, user mappings, foreign tables, IMPORT
FOREIGN SCHEMA) and its GRANT and REVOKE; the extension owner import of a
native Restore; and the family hosts `RoutineHost`, `SchemaHost` and
`extensionHost`. The decisions are [`ddl`](../ddl/README.md)'s,
[`typedef`](../typedef/README.md)'s, [`routine`](../routine/README.md)'s,
[`index`](../index/README.md)'s (access methods, operator families),
[`extension`](../extension/README.md)'s and [`fdw`](../fdw/README.md)'s; this
package orchestrates them over core's runtime.

CREATE/DROP STATISTICS use `ddl.StageCreateStatistics`/`StageDropStatistics`
on `SchemaHost`, with the ordinary name/definition admission and catalog
publication. Input relation lifetime uses the shared relation lock owner;
Target, rename and schema alterations use the same definition mutation locks,
original-source/name/namespace fences and current-authority recapture. Staging
retains immutable selection/expression backing and collected facts. Destination
CREATE precedes schema-move no-op and source-owner checks. Admitted same-schema
no-ops still retain and lock their original definition. Provisional notices are
discarded before recapture, so warnings are delivered once.
Ordinary ownership belongs to `rolecmds`, including isolated Restore execution.

ALTER FOREIGN TABLE reuses `table.ColumnStatisticsCandidate` for column targets
and n_distinct SET/RESET options, with the same clamp warning and ordinary
foreign-definition stage/revision/publication. These metadata-only actions never
acquire local heap storage or invoke provider validation.

Subscription utilities use `subscription.Stage` on the ordinary schema command
host. Definition/name locks, post-wait authority recapture, role fences and
journal publication are the same path as other catalog objects. The host's
cluster-wide OID reservation is separate from connection/option validation;
Describe never stages metadata or attempts an external mechanism.

SECURITY LABEL uses `ExecuteSecurityLabel` after ordinary transaction, read-only
and DDL-start admission. Without a registered provider it retains PostgreSQL's
22023-before-target-lookup boundary, including NULL removal. Explicit simulated
registration activates `selinux` only. The executor resolves actual descriptors
and ordinary SQL ownership, acquires their existing class-aware catalog locks
(including the shared subscription lock domain), then rechecks current authority,
descriptor identity and ownership before provider admission. Publications and
event triggers use their real local names; subscriptions use the current
database-qualified key; large objects use OID input and actual metadata existence.
Only large-object ownership honors the existing `lo_compat_privileges` setting.
These four classes still reject provider support with 0A000 before context
validation or staging, including NULL/empty labels; no labels/defaults are added.
Role authorization uses the current actor's SUPERUSER or CREATEROLE plus real
ADMIN traversal, with SUPERUSER targets requiring SUPERUSER first. Self identity
is not ADMIN. This rejection-only branch has no synthetic catalog role address,
lock or label store. DOMAIN checks the selected SQLType's actual domain kind
before ownership, retaining decoded written type qualification in diagnostics;
arrays of domains are not domains. COLUMN checks fresh postwait ownership before
generic relation-kind integrity and then the provider: sequence/index columns
reject with 42809 and the released kind detail, without touching a generator.
Supported classes validate context membership and independent MAC relabel
permissions through the same immutable Stage/Validate/Publish path as other
schema commands. NULL removes their provider association; no COMMENT store,
SQL-user subject substitution, arbitrary native loader or kernel enforcement is
involved. Denial leaves all staged label changes unpublished.

## Layering

It imports [`core`](../core/README.md),
[`tablecmds`](../tablecmds/README.md) (`TableHost`, `TypeHost` and
`AlterHost`; `SchemaTableLocked` for CREATE SCHEMA's table elements;
`RenameRelationLocked` and `AlterSchemaLocked` for foreign tables and
composite types; the ALTER column candidates for ALTER FOREIGN TABLE; and
`ExecuteSequenceComment`) and the decision packages, never the root or
another family (rule 6: `tablecmds` → every other family). In production only
the root composition imports it.

Stays in core: authorization capture, stage begin and publication, the lock
waits on the statement scope, the dependency closure
(`StageExpressionDependencies`, `StageDropProvider`), relation ownership and
the ownership stage DROP EXTENSION publishes (`ExecuteRelationOwnership`,
`OwnershipStage`, `PublishOwnership`), the role installation COMMENT shares
(`InstallOperationRolesLocked`), SQL routine invocation, the extension
registry and `statementHost.OpenExtensionPaths`, foreign scan capture and
participant enlistment, and the builtin catalog seed.

## Entry points

Dispatch (`core.Statements.Execute`); each is entered with nothing held:
- `ExecuteSchema` runs every schema-element statement listed above except
  the routine, COMMENT, tablespace, LOAD, CHECKPOINT, extension and foreign-data ones. It
  stages under `Engine.Mu` (`stageSchemaLocked`: the capture, the early actor
  read that gates every staged statement, one call per statement; DROP
  SCHEMA first takes its namespace removal locks and CREATE COLLATION its
  catalog lock unlocked), admits the definition mutation and retirement
  with the mutex released (`admitDefinitionMutation`,
  `admitDefinitionRetirement`: hold the captured identities, then recapture
  at most one final stage from current authority), and publishes one catalog
  stage with the cascade notice. CREATE SCHEMA rejects partition elements
  with 0A000 first. `ExecuteSQLRoutineDDL` (CREATE FUNCTION, PROCEDURE,
  AGGREGATE) runs through it.
  Routine/language retirement retains captured SQL-root drop provenance across
  grant-time candidate recapture after epoch, identity and authorization fences;
  dependency-closure roots are not promoted to explicit SQL targets. Global
  language identities use ordinary definition-object locks, not relation aliases.
  Transform CREATE/REPLACE/DROP use the same schema candidate, definition
  mutation/retirement capture, lock admission, current-authority recapture and
  single publication. Pair and refreshed consumer descriptors retire together;
  failed/canceled rebuilding leaves the old root intact. Transform names use
  the real type/language unique claim: an admitted concurrent collision reports
  the catalog's 23505 constraint diagnostic, distinct from serial 42710.
  `RoutineHost.ValidateAggregateInitial` captures demanded type diagnostics and
  ACL authority before splitting ordinary input from domain input. A real
  aclitem INITCOND can therefore validate through `InputWithEnvironment`, emit
  its exact default-grantor warning, and reject malformed input before catalog
  publication. Domains retain `DecodeDomainLiteral`'s captured unlock/readmit
  CHECK boundary, never direct CHECK execution under the DDL mutex.
- `StageDictionaryDetached` captures dictionary inputs and authority under
  `Engine.Mu`, compiles real shipped resources with it released, then calls
  `CompleteDictionaryLocked` under the mutex. Completion fences captured
  namespace, template and dictionary identities, recaptures current authority
  and stages the compiled operation without publishing it. The first TEMP
  namespace pair stays candidate-owned through compilation and unrelated
  publication recapture; completion reuses that exact namespace authority,
  without installing a premature session pointer or allocating a second pair.
  `ExecuteSchema`
  uses this path for CREATE/ALTER dictionaries, including ALTER recapture
  after definition-lock admission; DROP uses the ordinary dependency closure.
  Configuration DDL uses the ordinary locked stage, definition admission,
  dependency closure and journal publication; it never compiles dictionary
  resources or introduces a detached configuration store.
  Template utilities use this same locked stage, definition admission and
  retirement path. Creation and identity alteration apply ownerless-template
  authority; DROP also admits the containing schema's owner. Callback lookup
  records actual procedure inputs in the publication fences.
- `ExecuteComment` stages the comment (the locked-relation check, the private
  role record installation or object stage) and publishes it. Dictionary targets
  use the ordinary captured dictionary namespace search, then the shared role
  owner/schema-USAGE checks and immutable descriptor replacement.
  Dictionary COMMENT lookup uses a copied semantic name without expression
  locations, retaining the original parsed name for utility ownership diagnostics.
- `ExecuteSecurityLabel` owns zero-argument validation, the mutex and fresh
  operation authority capture around `ddl.SecurityLabel`. Without registration,
  every target fails provider selection before lookup/ownership and publication;
  Describe remains effect-free. With simulation registered, generic support,
  real lookup/ownership, lock/currentness and provider-class rejection retain that
  ordering. DOMAIN validates actual selected kind before ownership; a genuine
  domain (including one over an array) and TYPE retain existing TypeExclusive.
  Actual builtin/renamed TABLESPACE descriptors use ordinary shared AX grants and current
  OwnerOnly admission. Physical CREATE TABLESPACE remains unsupported. Local
  large-object authority is canonical2995; public object locks use released2613
  and the current database, never a relation alias or shared database-zero tag.
  All these provider-unsupported targets still reject before label validation or
  staging, including NULL/empty/invalid labels; LO compatibility affects only its
  ordinary owner check. `TestReferenceSepgsqlSimulationGenericLabelResolution`
  covers ordinary released diagnostics and direct/prepared active simulation
  separately. Its ordinary publication/subscription rename witnesses grant the
  intended owner database CREATE, then revoke it at cleanup; they do not broaden
  SECURITY LABEL admission or the unrelated reader's authority.
  `TestSepgsqlSimulationGenericLabel(RoleOwnership|DomainKind|ColumnIntegrity)`
  checks complete primary/detail records, real authority mutations and unchanged
  roots, allocators, subjects and sequence generators.
  Physical role actors explicitly have LOGIN; target-only and public SET ROLE
  identities do not need that privilege. Exact native resolver evidence permits
  only the real public/pg_catalog `db_schema:search` observations, including
  predecessor descriptor/label and physical-phase identity; it permits no
  relabel observation, receipt completion or audit queue growth. Column kind
  fixtures construct their partitioned index through existing text-key SPGiST,
  not unsupported default-method partition-index DDL or private descriptors.
  Its kind detail follows the actual indexed parent's partition metadata in the
  fresh catalog, not the index descriptor's unset partition field.
  The corresponding public
  `TestReferenceSepgsqlSimulationGenericLabel(RoleOwnership|DomainKind|ColumnIntegrity)`
  definitions retain direct/prepared/held execution and separate ordinary
  provider-unloaded/rename/drop/comment contrasts. Neither phase adds successful
  label classes. `TestSepgsqlSimulationGenericLabel(NoPublication|FreshOwnership|LockLifecycle)`
  covers genuine grants/waits/public projection (including domain TypeExclusive
  and whole-sequence ShareUpdateExclusive), shared cross-root identity,
  release/cancel/join, fresh ownership/descriptor/epoch fences and immutable
  no-stage rejection, retaining publication/event-trigger/subscription/LO routes.
- `ExecuteCreateTablespace`, `ExecuteLoad` and `ExecuteCheckpoint` keep the
  argument count, mutex and fresh authority capture around `ddl.CreateTablespace`,
  `ddl.Load` and `ddl.Checkpoint`; none publishes or marks a utility complete.
- `ExecuteExtension`, `ExecuteExtensionUpdate`, `ExecuteExtensionAlter` and
  `ExecuteDropExtension` (see [Extensions](#extensions)).
- `ExecuteForeignDDL` stages through `fdw.StageDDL` on `TableHost`, releases
  the replacement read before wrapper validations. Actual SQL validators
  enter core's common private-journal admission, also used by table ALTER and
  IMPORT; root Finish owns metadata and routine DML together. Empty jobs and
  native validators do not force that SQL completion path. Direct and imported
  CREATE FOREIGN TABLE share `finishForeignTableCreation`: a sealed core
  construction token supplies relation-shape, table and configured-column
  visibility before the corresponding validators complete. It never routes
  ALTER through a shell or exposes incomplete descriptors to committed readers.
  Successful completion returns that unique fresh relation's genuine `Created`
  reference for ordinary paired Restore constructor admission. Errors/cancellation
  and skipped IF NOT EXISTS creation return no Created identity; imports discard
  the result without changing their enclosing atomic publication.
  `executeForeignImport` keeps the importer callback, the per-table relock,
  re-authorization and epoch check, consumes each completed definition's fresh
  stage into the ordinary statement-private overlay, and retains initial
  discovery dependencies until final validation. Only enclosing Finish commits
  metadata and validator effects; `executeForeignTableAlter` routes OWNER,
  RENAME and SET SCHEMA to the shared relation executors. Ordinary descriptor
  actions use tablecmds' hierarchy/identity stage with no foreign heap phase;
  option-bearing actions retain ordered unlocked validator jobs.
  `AlterTableColumnOptionsLocked` serves ordinary ALTER TABLE column OPTIONS:
  tablecmds first performs genuine target/owner/existence/ADD admission, then the
  same statement journal runs the existing foreign option tail and validators.
  A nonforeign target fails 42809 without publishing the ordinary prefix.
  IF EXISTS missing targets skip the tail. Both ALTER spellings select ADD-option
  tails from tablecmds' actual admitted ADD action indexes, including cached
  inheritance execution, not from the original relation's column names. DROP
  precedes ADD, so dropping and re-adding an existing column applies its new
  options and validates them; a truly skipped IF NOT EXISTS ADD leaves its
  options unapplied on either ordinary or foreign targets. Kind and validator
  failures roll back the prefix in the same statement journal. Prepared ASTs
  are never mutated.
  Foreign PARTITION OF uses the shared pending partition definition and completes
  bounds/parent dependencies before entering the same CREATE validator phases.
  DROP uses the shared expression closure followed by owned-sequence retirement,
  including foreign owners reached through server, wrapper or hierarchy CASCADE;
  identity sequences and their dependents retire in the same private publication.
  `ExecuteForeignGrant` clones the
  journal's catalog changes, emits the grant warnings and installs each
  change.

Dispatch (`core.Statements.ExecuteLocked`): `AlterAllTablespaceLocked` is the
shared TABLE/MATERIALIZED VIEW bulk continuation over `ddl.AlterAllTablespace`.
Entered with `Engine.Mu` held, it releases it on return. The AST kind remains
available to completion/event classification; this continuation adds no
placement inventory or independent publication.

Restore hooks: `ImportExtensionOwners` (below) and `BindSQLRoutineLocked`.
The private SQL-body directive defers catalog-dependent quoted SQL analysis to
invocation and preserves legitimate source-only declarations, including malformed
quoted SQL/PL. Standard analysis/freezing and hard defaults retain their checks.
Public CREATE uses its scoped check_function_bodies policy for quoted validation
and standard terminal results, never to bypass standard analysis or admission.
`RoutineHost.BindSQLRoutine` also consumes an exact private Restore checked-PL
lease when a previously checked declaration's final validator is absent.
The root pairs the full CREATE header with the immediate actual owner transfer;
the bind host validates the exact body coordinates and current language, then
compiles under the resolved target principal before publishing the fresh stage.
This rebuilds real programs and declaration dependencies without changing normal
CREATE validation, globally compiling deferred siblings or borrowing a validator
name. Restore clears the lease on every execution outcome.

## Hosts

Each family host carries a compile-time assertion; `RoutineHost` and
`SchemaHost` embed core's `DDLHost` with its `{E, Op, CatalogStage}` layout.

| Type | Implements |
| --- | --- |
| `RoutineHost` (`routine_host.go`, `operator_family_host.go`) | `routine.DDLHost`, `index.FamilyDDLHost` |
| `SchemaHost` (`schema_host.go`) | `ddl.SchemaHost`, `extension.DictionaryHost` |
| `extensionHost` (`extension_host.go`) | `extension.Host`, `extension.Stage` |

`extensionHost` captures one authorization view and wraps the stage rather
than being a defined type over `CatalogStage`, so `Create` and `Replace` keep
the stage's lifecycle checks. `BeginStage` stores it in
`CatalogStage.ExtensionSlot`; `stageOf` returns the root stage and
`extensionNamespacesOf` returns all captured schema/predecessor observations.
`Read` remains the immutable stage base; `CurrentRead` includes earlier staged
prerequisites. Script helper lookup replaces the caller path with target then
actual required namespaces, preserving implicit pg_catalog priority and excluding
own TEMP functions. Schema USAGE is checked as the actual script principal.

## Extensions

Records, decisions and providers are [`extension`](../extension/README.md)'s.
The executors keep the argument count, `Engine.Mu`, the authorization
capture and publication: CREATE, UPDATE, membership mutation and schema
relocation share `publishExtensionStage`, whose provider validation runs with
the mutex released, fenced by `extensionAuthorityFence`. Destination resolution
includes CREATE admission before extension ownership and same-target handling.
The extension adapter retains all candidate/name/member/namespace/prerequisite
observations and finish authority, then unlocks and uses the ordinary
`Engine.StageCatalogOverlay`. That owner transfers descriptors/defaults,
prepares actual private index roots and produced outputs outside the mutex,
and performs final epoch/role/routine-source/preflight/admission fences before
installing the scope journal and event commands. Cohort relocation therefore
refreshes private GIN binding wrappers as well as committed roots; it does not
publish a descriptor-only private graph or bypass same-schema no-op fences.
The narrow pg_trgm script capability delegates to core's physical settings owner
after early CREATE admission and extension-header staging, before the released
script's initial C-validation boundary. This loads even when a later member
collides, but never for IF NOT EXISTS or earlier privilege/schema/version errors.
Settings/session locks are released before owned warnings are queued; no external
notice callback runs under publication locks.
CREATE acquires one stage for the whole recursive closure. Any staging or
materialization error disposes that owned stage after releasing `Engine.Mu`;
successful staging transfers disposal to publication. Cancellation before
recursive work and publication rejects the whole candidate.
Every script target, reused prerequisite schema and originally explicit schema
retains its committed predecessor before validation releases the mutex.
Namespace authority fences carry an explicit address and nullable predecessor:
a transaction-private schema has committed absence, never an unset address.
Reused prerequisite descriptors and their members retain actual private/base
observations and committed originals; concurrent replacement cannot silently
change the provider used by a newly installed dependent.

`extensionHost.ScriptView` binds a provider's ordinary consumer through the same
unpublished `CatalogStage`, after genuine function sealing. Its typed
`scriptViewHost` reuses `TableHost`/`DDLHost` and the narrow `view.CreateHost`;
it does not import sibling `viewcmds`, execute SQL recursively, or publish an
intermediate catalog. Creation uses the selected member owner, staged current
authority and target-only script path with ordinary implicit pg_catalog lookup.
Both view collisions and late validation remain atomic installation failures.

Successful membership ADD/DROP and real schema movement annotate one outer
`ALTER EXTENSION` address on the gated command stage; recursive members and
internal namespace movers do not emit separate command receipts. Publication
transfers those annotations only after journal preflight. Same-target movement
keeps an empty command bag and unchanged descriptors, but still passes through
the statement journal, authority and extension-predecessor fences before Finish.
Typed member resolution reuses actual routine/type/object identity owners; lower
routine/type/relation mechanics preserve their distinct authority rules. Read
fences, including ACLs, come from the staged snapshot, never a newer descriptor
observed after outside-lock validation. Stage transfer checks that exact private
predecessor through `session.JournalCapture`, while retaining committed
descriptor and name predecessors in the journal. In particular, successive
membership changes must not replace a transaction-created object's absence
fence with its private name binding. Transfer preflights the completed journal.
Private preparation checks the same committed originals and names again before
constructing its provider-validation view, including a winner committed after
transfer. Both preparation and final publication first check the existing
extension authority fence, preserving CREATE's `pg_extension_name_index` 23505
diagnostic for a competing installation. A competing UPDATE still reports
ordinary `catalog.Conflict`, never a mixed reciprocal-membership graph.
Retirement captures the
complete target set, including automatic aliases, and rejects incoming
dependencies with `catalog.Conflict` before mutation; DROP keeps its
multi-target 2BP01 text and cascade notice.

Only a pgcrypto CREATE that still needs physical initialization takes Owner.Mu
before Engine.Mu during staging. Its genuine first C validation promotes owned
active setting/savepoint images; both mutexes are released before provider
validation/publication. Unrelated providers and already initialized sessions
retain the ordinary locking path.

Identity ALTER remints citext/EAN13 values through a transient exact
source-descriptor-to-current-descriptor map, never an OID-only permission.
Generation reownership restores the prior import map before returning and leaves
retained catalog/storage roots unchanged. Base rename, move and ownership do not
change installed I/O routine namespaces or owners. Immutable release/declaration
origin, rather than current membership position, owns portable provider identity.
`ImportExtensionOwners`
applies only to Restore's isolated catalog: it checks the import authority,
clones the state, reowns the stored expressions and publishes.

## Testing

Tests are external (`package schemacmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. The package's `testSessionID` starts above
the session IDs `enginetest` mints, so a test may use both. The extension
provider units stage through the shared extension host
(`schemacmds.ExtensionHost`, `StageExtensionLocked`); the type, domain and
enum units stage through `StageSchemaLocked`.
Schema DROP units retain `CaptureSchemaDrop`'s real ordered identity/absence
manifest and use `constructSchemaDropUnit` to run the same selected-root,
expression-dependency and owned-tail preparation/application as the executor
before validating publication. `StageSchemaLocked` alone only selects roots:
publishing that unfinished stage does not exercise retirement fences.
`TestExtensionSchemaClosure` introduces a genuine dependent from a separate
physical session after completed capture, requires `catalog.Conflict`, and
retains the exact committed root, provider and surviving relation on rejection.
`StageExtensionLocked` transfers every nonnil candidate to its caller, including
an uncommitted partial candidate returned with an error. Unlock `Engine.Mu`
before disposing it; the executor owns that unlocked failure path. Internal
callers register cleanup before any error/Fatal branch and must not lose a
returned failed stage. Failure units check disposal rejects further writes and
that no staged extension/member prefix reaches the committed catalog; a nil
stage is expected only when admission fails before a stage was created.

Unaccent expression, remap and rollback-survivor units evaluate with real
`Engine.StartStatement` admission and `HostOf(scope)`. They finish each scope
before publishing catalog mutations or starting the next statement, and defer
an error unwind before releasing operations or sessions. Fresh contexts after
dictionary CREATE/DROP, provider rename or replacement capture current catalog
authority; cancellation cases finish while the canceled operation context is
still installed. These fixtures do not retain protocol messages, so `Finish`
alone releases statement admission; `FinishMessage` is not applicable.
Dependency witnesses retain nonpinned installed procedure/dictionary edges and
omit pinned builtins. Remap fixtures occupy the exact source dictionary identity
with a real dictionary in a live session's TEMP namespace, assert that replacement
retains that descriptor and callback, then distinguish imported execution and
rendering from the retained collision.

XML2 provider, expression and stored-identity units use `RUN='^TestXML2'`.
They inspect real staged collision rollback, twelve sealed declarations, typed
children, group/capture reconstruction and forced destination identity remapping.
The two XSLT signatures retain independent origin, volatility and folding policy
through reconstruction and stored controls; TEXT adapter ownership is tested
without borrowing runtime trees or reducing the full serialized datum.
Closed statement-state DEFAULT children keep their original replay flags and
immutable operand backing; volatile, parameter-dependent and owner-row cases
remain distinct from that bounded replay permission.
CHECK ownership coverage extracts an actual row-bound installed expression and
its routine/type/attribute manifest, preserves CHECK versus DEFAULT closure,
rejects invalid recipes/captures and renders genuine renamed/detached and remapped
provider authority. Public CHECK writes and diagnostics live in the shared
reference scenarios, not these composed-owner units.

SSLInfo provider, expression-protocol and stored-identity units use
`RUN='^TestSSLInfo'` and the same staged installation helpers; they inspect sealed
authority, argument ownership/dependencies, scalar versus fixed-OUT source
cardinality and destination generations without internal public-SQL scenarios.
Pgcrypto provider/remap/volatility components explicitly supply a test-only
installed-catalog initializer and on policy for isolated kernel evaluation.
They do not acquire SQL session authority or grant missing live hosts a fallback.

Trigger lifecycle identity, clone, source-fence, dependency and remap units
construct metadata with the same trigger fixture. Lo provider units stage
concrete domain/companion recipes through the shared extension host,
validate immutable manifests and symbolic ownership, and exercise genuine
collision/allocation and destination-role replacement fences without public
SQL. Routine-metadata units use the real capture fixture and staged
extension constructors for declaration completeness, independent source
bags, redaction, empty/canceled results and generation fences. The complete
Earth routine source bag includes all eight parsed SQL RETURN bodies with empty
source text and the C `geo_distance` symbol, preserving their language split.
The builtin completeness inventory owns every callable and metadata-only row.
Metadata-only expectations specify scalar versus SETOF, exact result types and
default ACLs per declaration. The archiver and four zero-input bgwriter scalar
backing routines retain implicit PUBLIC EXECUTE without weakening the restricted
SETOF declarations or granting a callable provider.
Dependency-bag units resolve every installed Snowball seed pair by
declaration, retaining each pair's normal dependencies and the shared
template-to-helper edges once, without pinning allocated identities. Hindi and
Russian also retain their English ASCII-mapping dependencies; the other languages
map words to their own dictionaries without an English fallback.
Scalar hierarchy and positional-match manifest authority and atomic member
conflicts use `RUN='^TestLtree(Hierarchy|Lquery)Provider'`.
Path manifest, original SQL-name/C-symbol identity, atomic staged conflicts and
path/query survivor authority use `RUN='^TestLtreePath(Provider|QuerySurvivor)'`.
Survivor fixtures retain genuine sealed declarations while varying reciprocal
membership and live origin headers; copied installation headers are negative
inputs, never replacement installation authority.
Mixed scalar label-query manifest/owner authority and late conflicts use
`RUN='^TestLtreeLtxtqueryProvider'`.
Stock tcn declaration, immutable-origin and native capability authority units use
`RUN='^TestTcn'`; SQL firing and native-control scenarios live in `reference/sessions`.

Public SQL lives in
[reference/extensions](../../../reference/extensions/README.md#testing),
[reference/extension_types](../../../reference/extension_types/README.md#testing),
[reference/extension_hstore](../../../reference/extension_hstore/README.md#testing),
[reference/extension_cube](../../../reference/extension_cube/README.md#testing),
[reference/catalog_objects](../../../reference/catalog_objects/README.md#testing)
(schemas, COMMENT), [reference/domain_types](../../../reference/domain_types/README.md#testing),
[reference/operators](../../../reference/operators/README.md#testing) and
[reference/indexes](../../../reference/indexes/README.md#testing) (operator
classes and families, access methods).

```sh
make check RUN='^Test(Schema|Comment|Collation|Conversion|Tablespace|Load|Checkpoint|Transactional)'  # schema elements and physical utility boundaries
make check RUN='^Test(Domain|Enum|Composite|TypeLifecycle|Geometry)'  # types and domains staged through the schema statements
make check RUN='^Test(User|Operator|Routine|SQLRoutine|Cast|CreateAggregate|Aggregate|OrderedSet|Access)'  # routine, operator and access method DDL
make check RUN='^Test(Extension|AvailableExtension|UnaccentDictionary|Citext|Cube|Earth|Isn|Intagg|Intarray|Lo|Pgcrypto|Pgrowlocks|UUIDOssp|Fuzzystrmatch|Levenshtein|Soundex|Metaphone|Daitch)'  # extensions and providers
make check RUN='^Test(TriggerDDL|InfoRoutines|FDW)'      # trigger lifecycle, routine metadata, foreign-data DDL
make check RUN='^Test(InfoRole|PG(Aggregate|Constraint|Depend|Enum)|Conditional|Array|TableLike|JSONB|CopyFilter|Reg|NetworkDomain|DumpPresenter)'  # catalog producers and runtime units whose fixtures stage schema elements
make check RACE=1 RUN='^Test(ExtensionProviderPublication|ExtensionDropPublication|ExtensionVersionPublication|CommentLocks|TransactionalSchemaDropManifest|FDWLateDependencyPublication)'  # publication races
```
