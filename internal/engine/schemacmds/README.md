# internal/engine/schemacmds

The schema-object statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): CREATE,
ALTER and DROP SCHEMA, TYPE, DOMAIN, COLLATION, CONVERSION, TEXT SEARCH CONFIGURATION, TEXT SEARCH TEMPLATE and TEXT SEARCH DICTIONARY; CREATE
LANGUAGE and ALTER/DROP LANGUAGE; ALTER DOMAIN RENAME and SET SCHEMA; CREATE FUNCTION, PROCEDURE and
AGGREGATE, ALTER and DROP ROUTINE; cast, operator, operator family, operator
class and access method DDL; COMMENT; CREATE TABLESPACE and ALTER TABLE ALL IN
TABLESPACE; LOAD and CHECKPOINT; CREATE, ALTER and DROP EXTENSION; the
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

SECURITY LABEL uses `ExecuteSecurityLabel`, a failure-only boundary before
object lookup or permissions. No native module can register a label provider,
so omitted providers and explicitly named providers return PostgreSQL 22023,
including NULL removal. It stages no catalog data, publishes no utility
completion and does not reuse COMMENT storage. Ordinary transaction,
read-only and DDL-start admission still precede this executor.

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
- `ExecuteComment` stages the comment (the locked-relation check, the
  private role record installation or object stage) and publishes it.
- `ExecuteSecurityLabel` owns zero-argument validation, the mutex and fresh
  operation authority capture around `ddl.SecurityLabel`. All supported target
  kinds fail provider selection before target lookup or ownership, without
  publication; Describe is effect-free. The shared-label catalog remains empty.
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
  ALTER through a shell or exposes incomplete descriptors to committed readers;
  `executeForeignImport` keeps the importer callback, the per-table relock,
  re-authorization and epoch check, consumes each completed definition's fresh
  stage into the ordinary statement-private overlay, and retains initial
  discovery dependencies until final validation. Only enclosing Finish commits
  metadata and validator effects; `executeForeignTableAlter` routes OWNER,
  RENAME and SET SCHEMA to the shared relation executors and runs the phased
  action loop with its lock windows. `ExecuteForeignGrant` clones the
  journal's catalog changes, emits the grant warnings and installs each
  change.

Dispatch (`core.Statements.ExecuteLocked`): `AlterAllTablespaceLocked` is a
lock-domain continuation: entered with `Engine.Mu` held, it releases it on
return.

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
`CatalogStage.ExtensionSlot`; `stageOf` gives the executors their stage back
and `extensionNamespaceOf` the exact namespace fenced for publication.

## Extensions

Records, decisions and providers are [`extension`](../extension/README.md)'s.
The executors keep the argument count, `Engine.Mu`, the authorization
capture and publication: CREATE, UPDATE, membership mutation and schema
relocation share `publishExtensionStage`, whose provider validation runs with
the mutex released, fenced by `extensionAuthorityFence`. Destination resolution
includes CREATE admission before extension ownership and same-target handling.
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

Trigger lifecycle identity, clone, source-fence, dependency and remap units
construct metadata with the same trigger fixture. Lo provider units stage
concrete domain/companion recipes through the shared extension host,
validate immutable manifests and symbolic ownership, and exercise genuine
collision/allocation and destination-role replacement fences without public
SQL. Routine-metadata units use the real capture fixture and staged
extension constructors for declaration completeness, independent source
bags, redaction, empty/canceled results and generation fences.
Dependency-bag units resolve the English, Arabic, Armenian, Basque, Danish,
Dutch, Estonian, German, Greek, Hindi and Hungarian Snowball seed pairs by
declaration, retaining each pair's normal dependencies and the shared
template-to-helper edges once, without pinning allocated identities. Hindi also
retains its English ASCII-mapping dependency; Hungarian maps words to its own
dictionary without an English fallback.
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
make check RUN='^Test(Extension|AvailableExtension|Citext|Cube|Earth|Isn|Intagg|Intarray|Lo|Pgcrypto|Pgrowlocks|UUIDOssp|Fuzzystrmatch|Levenshtein|Soundex|Metaphone|Daitch)'  # extensions and providers
make check RUN='^Test(TriggerDDL|InfoRoutines|FDW)'      # trigger lifecycle, routine metadata, foreign-data DDL
make check RUN='^Test(InfoRole|PG(Aggregate|Constraint|Depend|Enum)|Conditional|Array|TableLike|JSONB|CopyFilter|Reg|NetworkDomain|DumpPresenter)'  # catalog producers and runtime units whose fixtures stage schema elements
make check RACE=1 RUN='^Test(ExtensionProviderPublication|ExtensionDropPublication|ExtensionVersionPublication|CommentLocks|TransactionalSchemaDropManifest|FDWLateDependencyPublication)'  # publication races
```
