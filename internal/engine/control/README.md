# internal/engine/control

The native Dump and Restore drivers. Dump turns a captured catalog into an
ordered SQL script; Restore replays such a script through an ordered hook
table. Before step 30 both lived in the root's `control.go`, which named
the internals of almost every feature. Now a feature adds a table entry
instead of editing a central switch.

The package imports `access`, `analyze`, `authorization`, `catalog`, `dump`,
`exec`, `expr`, `extension`, `fdw`, `ids`, `index`, `largeobject`,
`partition`, `pgcatalog`, `plan`, `regobj`, `routine`, `sequence`,
`session`, `storage`, `stored`, `table`, `typedef`, `view`, `visibility`,
`parser`, `sqltype` and `sqlstate`. Only the root `engine` package imports
it. It holds no mutex, no engine pointer and no state between calls. Step
30 of the split created it; see
[ENGINE-SPLIT-control.md](../../../docs/ENGINE-SPLIT-control.md).

## What it owns

### Dump

- **Collect** (`collect.go`, `collect_relation.go`, `collect_foreign.go`): `Collect(ctx,
  source)` builds the builtin-schema preamble, then hands every descriptor
  of `source.State` to the Collect tables and returns the filled
  `Capture`. `Source` is what the tables read: the capture's catalog and
  role reads, the constraint replay, the ACL output, the generation recipe
  plan, and the locked engine state (catalog state, relation roots,
  sequence generators, commit frontier, bootstrap principal, foreign
  registry). The root fills it under `Engine.mu` and calls `Collect`
  inside that window, with the ACL output context on `ctx`; nothing in
  `Source` is read after the unlock. `dumpCollectors` is an ordered list
  of `{name, matches(Descriptor), collect(ctx, *dumpCollection, address,
  Descriptor)}` entries: the first entry whose `matches` accepts a
  descriptor collects it, a descriptor no entry accepts is `unsupported
  catalog descriptor kind`, and objects in temporary namespaces are
  skipped before matching. The relation entry runs the checks shared by
  every relation, then `dumpRelationCollectors` (foreign, composite, index,
  view, sequence, table) the same way. Order is error precedence: keep it when
  adding an entry. A feature package owns an entry's decision once its
  dump code moves (`routine.OperatorDumped`, `stored.ProcedureProvider`,
  `stored.TypeProvider`). `BootstrapNamespaceDumpable` is the builtin
  namespace ACL check.
  Foreign declarations use the feature-owned FDW renderer and the same graph:
  wrapper before server, server before mappings/tables, types before tables
  and tables before views. Registered procedures are validated destination
  prerequisites, not executable dump events. Foreign relations never enter
  storage/row/default-repair collection. Bootstrap CREATE plus ordinary owner
  transfer preserves independent supported owners without replay grants.
- **Capture checks** (`capture_checks.go`, `aclitem_preflight.go`):
  `CheckPortable` rejects unsupported explicit SQL/MED ACLs, default
  privileges and invalid indexes; `PreflightACLItems`
  checks every ACL leaf a dump would write. The root calls both in its
  lock window, before `Collect`, in today's order.
- **Capture record** (`dump.go`): `Capture` is everything Dump reads from
  the engine (the catalog and role reads, the constraint replay, the ACL
  output, the collected schemas, types, routines, tables, views, indexes
  and the event `Graph`); `Schema`, `OwnedStatement` and `SchemaComment`
  are its records.
- **Driver** (`dump.go`): `Dump(ctx, capture, dst)` sets the ACL output
  context, builds the script and writes the sections, then returns
  `ctx.Err()`.
- **Script** (`script.go`): `buildDumpScript` renders the policy, rule and
  row-security groups and the type statements, completes the event graph,
  decides default deferral and orders the events with `dump.Order`.
- **Sections and Emit table** (`sections.go`, `emit.go`,
  `emit_declaration.go`): `dumpSections` is the whole script in order; its
  `events` section writes each ordered event through `dumpPhaseEmitters`,
  indexed by `dump.Phase`. Each entry has an `owner` the event loop sets
  before the entry runs (`nil`: the emitter sets it, as `Declaration` and
  `DomainChecks` do; domain checks render before the owner switch so an
  error writes nothing) and an `emit`; an empty entry (`TableCompletion`)
  writes nothing. `dumpWriter` writes text and tracks the `SET SESSION
  AUTHORIZATION` owner. A feature exports its dump function and the table
  names it in the same position.
- **Adapters** (`resolver.go`, `presenter.go`): `Resolver` is the
  `dump.Resolver` (`Predecessor` maps a dependency to the event that must
  precede its consumer) and `ColumnPredecessors` adds a relation's row and
  column type predecessors; `Presenter` is the `dump.Presenter`. Both read
  only their arguments.
  Global access methods use ordinary declaration events: one quoted CREATE with
  qualified destination handler, no owner directive, before dependent families/
  classes/indexes. Builtin methods/registered handlers are validated and omitted.
  Restore admits the utility through normal staging; existing typed identity/
  TEMP remapping also moves AM descriptors and their consumers, then rebuilds
  destination support/root captures rather than transplanting source operations.
- **Large objects** (`largeobject.go`): immutable captured descriptors use a
  separate fixed-OID-sorted list, outside numeric relation graph keys. Owner and
  raw ACL formatting is preflighted through existing codecs before writer I/O.
  Sparse chunks are emitted without copying or materializing holes, under exact
  owner directives; typed raw ACL repairs follow content. The root admits only
  these bounded literal replay shapes and installs in the private stage.


### Restore

- **Driver** (`restore.go`): `Restore[R](ctx, host, hooks, run, src)`
  reads `src` through `contextReader` (it stops once `ctx` is done), parses
  it with the `Decode` entries reading line comments, then calls
  `Host.Begin` and `Host.Start` (its `end` is deferred). Per statement it
  calls `Host.Statement`, walks `Before`, `Admit` and `Prepare`, calls
  `Host.Execute` and walks `After`. Then it walks `Finish` and calls
  `Host.Publish`. The first error stops the replay and skips `Publish`;
  `end` still runs.
- **Hook table types** (`hooks.go`): `Hook[R]` has `Name` and the phase
  functions `Decode`, `Before`, `Admit`, `Prepare`, `After` and `Finish`,
  each over the caller's run state `R`; any may be nil. `Admission` is an
  admit entry's answer: `NotHandled`, `Admitted` or `Consumed` (replayed,
  nothing executes). A statement no entry handles is rejected with
  SQLSTATE `0A000` and the allow-list text. `Outcome{Created}` is what an
  executed statement tells the `After` entries.
- **Admission checks** (`admit.go`, `admit_checks.go`): the allow-list
  entries that never read the caller's run. `AdmitKind[R, T]` admits every
  statement of kind `T`; `AdmitSchemaObject[R]` and `AdmitOperator[R]`
  admit the schema-object and operator DDL kinds; `AdmitStatement[R,
  T](check)` admits a `T` that `check` accepts, else answers `NotHandled`
  with the check's error. The checks read only the statement:
  `CheckCreateTable` (TEMP staging rejection, then the stored-expression
  Restore scalars; the caller captures and strips foreign keys first),
  `CheckAlterDomain`, `CheckAlterView`, `CheckCreateView`,
  `CheckCreateQueryTable`, `CheckAlterIndex`, `CheckAlterTableOf`,
  `CheckAttachPartition`, `CheckCreateSequence`, `CheckAlterTableDefault`,
  `CheckSelect`, `CheckComment` and `CheckInsert`. The root's hook table
  names them in its own order, so rejection order stays the table's.

### Catalog replacement

- **Definition OID remap** (`definition_oids.go`, `view_rule_oids.go`):
  `RemapViewRuleOIDs(view, allocate)` gives each imported view rule an OID
  that no descriptor, other rule or earlier claim uses, then runs
  `RemapDefinitionOIDs(view, allocate)`. That reserves every OID of the
  candidate (descriptors, aggregate helpers, domain checks and relation
  definitions), keeps the identities retained TEMP objects claim, and gives
  each colliding imported domain check, rule, policy, constraint, default
  or generation a fresh OID. It rebuilds the unique and exclusion backing
  of the index it touches, repoints partition parents of remapped unique
  and exclusion constraints, and rewrites rule references to remapped
  constraints. `view` reads the private replacement candidate, which both
  functions change in place. `allocate` is the engine's OID allocator: the
  caller (`preserveTemporaryLocked`) holds `Engine.mu` for the whole call.
  Error texts are the engine's (`stuntgres: invalid imported ...`).
- **TEMP preservation phases** (`persistent_replacement.go`,
  `temporary_set.go`, `temporary_types.go`, `temporary_identity.go`,
  `replacement_remap.go`, `replacement_remap_relation.go`): the decisions a
  persistent replacement makes to keep live TEMP objects.
  `PersistentReplacementCatalog(view)` returns the private candidate
  without temporary namespaces, their members and names (a temporary
  extension installation is not portable). Then, over `current` (the
  published catalog) and `next` (the candidate): `TemporarySet` collects
  the temporary namespaces and their members and rejects a candidate
  that rebinds a live temporary extension's name. Before dependency checking,
  `RetainTemporaryBuiltinTypes` restores in the private candidate the exact
  builtin codec generations retained objects use and reports whether it changed
  anything. `CheckTemporaryDependencies` then rejects changed policy sources,
  user providers or user domains under retained array columns. Full runtime
  manifests also name builtin usage types/collations and release callables:
  compatibility requires exact retained type generations/headers, equal genuine
  seeded collation capabilities, or unchanged genuine seeded procedure
  declarations, with both declaration providers validated. User/custom operators
  and physical sources remain exact. Full manifests, remapping and final
  validation are not bypassed. These phases precede allocator/publication work.
  `TemporaryCollisionMapping(next, temporary, allocate)` gives
  each imported identity (and aggregate helper) that collides with a
  retained OID a fresh one and returns the mapping;
  `RemapReplacementIdentities` moves the mapped descriptors, their
  references, dump priorities, default schemas, the name indexes and the
  storage and sequence roots. The root calls them in this order from
  `preserveTemporaryLocked`, under `Engine.mu`, with the retain, check,
  import, repair and install steps between them; error texts are the
  engine's.
- **TEMP replacement checks** (`temporary_sources.go`): over the published
  catalog, which the caller holds under `Engine.mu`.
  `CheckTemporaryRelationSourcesLocked(current)` rejects a retained TEMP
  table with a persistent inheritance parent
  (`TemporaryInheritanceReplacementError`); the root runs it, then
  typedef's typed-table check (`CheckTemporaryTypedReplacementLocked`), at
  replacement preallocation, at final replacement admission and first in
  `preserveTemporaryLocked`.
  `CheckTemporaryRuleSourcesLocked(current)` rejects a retained TEMP rule
  that depends on an object outside its session's temporary namespace
  (pinned builtins aside). `RetainTemporaryViewProvidersLocked(current,
  next, temporary)` keeps in the candidate the exact bootstrap catalog
  sources of retained TEMP views, after checking their shape and
  namespace/ACL authority, and rejects any other persistent source.

## The host contract

`Host` is the engine's side of one Restore; the root `restoreHost`
implements it:

- `Begin(source, statements)` records the script on the run and pairs the
  domain replay records with their INSERTs.
- `Start(ctx)` starts the private restore stage's session as the bootstrap
  superuser and acquires its operation. `end` clears the operation's replay
  flags, releases the operation and ends the session, in that order. If
  `Start` fails it cleans up itself and returns no `end`.
- `Statement(index)` resets the per-statement state.
- `Execute(op, stmt)` runs one admitted statement on the stage, clears the
  per-statement replay flags, closes its rows and detaches its notices.
- `Publish(ctx, op)` installs the stage catalog into the target engine.

## What stays in the engine

- `Engine.mu` and the Dump capture window: `captureDumpLocked`
  (`dump_capture.go`) reads the engine state, calls `CheckPortable`, builds
  the generation recipe plan, calls `PreflightACLItems`, projects the
  constraint catalog, runs the temporary collation check (it needs a
  catalog stage) and the configured foreign namespace check, then fills a
  `Source` and calls `Collect`. The root hands the capture over: `Dump`
  sorts and completes it in place, and the root never reads it after the
  call.
- The OID allocator (`catalogStore.allocateOIDLocked`) and the TEMP
  preservation orchestrator (`preserveTemporaryLocked`): it holds
  `Engine.mu`, calls the phases above and keeps the steps that need the
  engine or its stage: `retainDatabaseLocked`, the composition of the
  relation source check with typedef's typed-table check
  (`checkTemporaryRelationSourcesReplacementLocked` runs
  `CheckTemporaryRelationSourcesLocked`, then
  `typedef.CheckTemporaryTypedReplacementLocked` over the published state),
  the calls to typedef's scalar and domain imports
  (`ImportScalarTypesLocked` with the engine's enum member allocator,
  `ReownDomainTypes`, `RefreshScalarImportDestinations`),
  `repairRemappedGenerationsLocked`, the installation
  of the live TEMP descriptors and storage (with the stage lock), the TEMP
  policy and rule ref refresh, and the final validation.
- The restore stage (`restoreStage` in `principal_remap.go`) and the atomic
  publication (`replaceCatalog`): its lock order, the fence after unlock,
  the sequence-memory and serializable fences and the `foreignNamespaces`
  replacement in the same publication.
- `restoreRun`, `restoreHost` and the hook table `restoreHooks`
  (`[]control.Hook[*restoreRun]`, composed at package initialization), the
  admit adapters for entries that read the run (`restoreAdmitChecked`,
  `restoreAdmitReplayed`), those entries (ALTER OWNER, CREATE TABLE's
  foreign key capture, ALTER TABLE, DETACH PARTITION, DROP TABLE, ALTER
  SEQUENCE, SET SESSION AUTHORIZATION, the UPDATE and DELETE repairs and
  SQL/MED), every other hook body, and the operation replay slot
  (`restore_replay.go`). Entries share state only through
  `restoreRun`, so their order is part of the contract; every entry runs
  on the stage, never on the target engine.
- `Engine.Reset`, and the thin `Engine.Dump` (nil-writer and context
  checks, the `Engine.mu` window) and `Engine.Restore` (nil-reader and
  context checks, the stage) wrappers in `control.go`.

## Testing

Owned SQL routines and ordinary aggregates share routine dependency scheduling:
actual support/type predecessors precede aggregate declarations and consumers.
Existing statement owners preserve default-ACL ownership; roles are supplied
by the receiving environment. Explicit SQL-routine and aggregate ACLs retain
truthful pre-output Dump rejection, not silent permission reset.
The aggregate persistence scenario replays the actual unchanged emitted bytes
on PostgreSQL and twice through native Restore. Its TEMP scenario records real
namespace/sequence allocator collisions and retained identity after each Restore.
Retained aggregate binary recipes request the same real per-consumer CAST
windows used by frozen expressions. Their endpoint/support prerequisites precede
CREATE AGGREGATE; the window closes afterward and the authentic final absent or
replacement cast state is preserved. Stored calls use the retained aggregate
declaration rather than inventing a permanent compatibility cast or deferring
aggregate validation globally. Strict/no-init execution may then honestly fail
its live startup compatibility check while the declaration remains dumpable.

```sh
make check RUN='^TestReferenceCreateAggregate(Persistence|TemporaryRemap)$'
make check RUN='^TestReferenceCreateAggregate(Persistence|TemporaryRemap)$' REFERENCE=1
```

See the complete [owning proof matrix](../../../reference/aggregates/README.md#create-aggregate-contract).

Operator-class events use the ordinary dump dependency scheduler: real family
and implementation objects precede class, class precedes indexes and partition
consumers. INTERNAL members are declared exactly once in CREATE CLASS; loose
members remain separate family events. Current names/owners/schema and bootstrap
class deltas replay without inventing source identities. Foreign Restore
reconstructs remapped class captures and binds destination support.

```sh
make check RUN='^TestReferenceOperatorClass(Roundtrip|SeedRoundtrip)$'
make check REFERENCE=1 RUN='^TestReferenceOperatorClass(Roundtrip|SeedRoundtrip)$'
```

Roundtrip replays actual native-emitted bytes on PostgreSQL. The user enum column
is genuinely remapped while its polymorphic class/member input stays anyenum3500.
The scenario checks post-Restore maintained inserts and late failed replacement
atomicity. See [design](../../../docs/design/PG18-INDEX-OPCLASS.md).


Package units: the driver order with a fake `Host`, the walkers and the
INSERT admission check:

```sh
make check RUN='^TestRestore(Driver|Hooks).*|^TestRestoreAdmitsInsertTargetIndirection$'
```

Engine witnesses: the ALTER DOMAIN admission check, the presenter's domain
expressions, the builtin namespace ACL check, the resolver users and the
definition OID remap and the TEMP preservation callers:

```sh
make check RUN='^Test(RuleNamedConstraintRemap|DeparseConstraintsAllocatorOccupancy|PartitionConstraintsDefinitionRemapping|ViewRuleOIDUniquenessAfterRestore|DumpPresenterDomainExpressions|PGNamespaceShape|PredefinedRoleControls|IndexPropertyDump.*|IdentityDumpProviderMapping|IdentityControlsOwnerCollisionPredecessors|PartitionAttachDumpPhaseDependencies|PartitionDetach(Replay|UniqueReplay).*|DomainAlterConstraintReplayEligibility|InheritanceControls.*|TypedTemporary.*|PGAggregateIntagg.*|UserOperatorIndex.*|ExtensionTemporary.*|DefaultPrivilegesControls|PartitionConstraints.*|.*RetainedTemporary.*|.*TemporaryCollision)$'
```

Public behavior is covered by the Dump, Restore and Control reference tests
(quick mode, then `REFERENCE=1`). Dump output must stay byte-identical:

```sh
make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)|^TestReferenceControl'
make check RACE=1 RUN='^TestReferenceControl|^TestControlTransactions|^TestRestore(Driver|Hooks)'
```

Changes to the Collect or Emit tables, the sections or the script also need a dump,
restore and re-dump comparison against a `master` baseline (see 18b and
18c in [ENGINE-SPLIT-dump.md](../../../docs/ENGINE-SPLIT-dump.md)).

Cast-feature rendering adds real qualified declarations and raw32-preserving
SQL only where required. Ordinary FLOAT4 values, including subnormals, signed
zero, infinities and canonical quiet NaN, keep their existing canonical literals
and dump shape. Only noncanonical NaN words need raw bootstrap; the recursive
`regobj.NeedsFloat4RawReplay` predicate also gates literal rendering and bounded
composite-array INSERT targets. Detection uses visible physical rows (excluding
generated cells) and retained literal expressions, not merely column types.
Genuine frozen cast expressions retain their selected recipes even for finite
values. `cast.go` shares catalog dependency ownership for graph ordering. Dump
uses the final exact binary xid→real edge or ordinary temporary CREATE/DROP
around raw recipes/data and preserves a conflicting final edge.
Qualified raw-bit SQL uses the actual catalog identity `pg_catalog.float4`,
including scalar leaves and direct arrays: `real` is an unqualified SQL keyword
alias, not a type named `pg_catalog.real`.
Raw word-array input names the existing concrete catalog identity
`pg_catalog._xid` and uses its shared executable array codec. This does not admit
transaction-ID array columns or expand the bracket type annotation surface.
Table composite-array replay uses existing INSERT target slices to retain bounds;
Restore has no hidden raw-datum decoder or cast-registration bypass.
Select `TestReferenceCastDefinition(Persistence|ReplayPhases|ReplayConflicts|RawFloat4Persistence|RawCompositeArrayBounds|Atomicity)`
in both native and `REFERENCE=1` modes after changes.
Canonical float dump regressions additionally select
`^TestReference(FloatControlSnapshots|FloatMigratedSmoke|FloatExactOutputText|FloatingArithmeticControlsSmoke)$`
in both modes, preserving their existing expected SQL and dump→redump equality.
`^TestFloat4NeedsRawReplay$` independently exercises the scalar boundary and
canonical codec roundtrip, including domain wrappers and signed/signaling NaNs.
`cast_replay.go` derives ordinary-SQL windows only after default deferral and
invalid/inherited CHECK phases are settled. `cast_consumer.go` identifies the
actual emitted statement: deferred defaults and CHECKs each own their windows,
not a union over the earlier CREATE event. Independently altered members with
conflicting historical recipes move to the existing SET DEFAULT/ADD CHECK SQL
phases without changing validity. Cast declarations are excluded from the
generic early declaration chain so a final pair can follow a post-data window.
The default-deferral eligibility also includes a retained, validated standard
SQL routine whose exact reference is in the default's captured dependencies.
This breaks the genuine table → default routine → same table declaration cycle
by emitting CREATE TABLE, CREATE FUNCTION, then ALTER TABLE SET DEFAULT.
The routine retains its real volatility; neither deferral nor Restore evaluates
the default. Existing rows replay with explicit values. The default's routine
and type prerequisites move to its actual DEFAULT event, while NOT VALID table
and domain checks retain their post-data events and final pair windows.
Original methods, selected routines, signature adapters and contexts are kept.
Retained CASE templates use ordinary simple CASE syntax (one test evaluation)
under the selected implicit edge. Omitted routine defaults belong to their
declaration, not the call's written arguments; standard-body SELECT queries are
direct consumers too. Source-column virtual/transitive grafts are not walked.
Actual final conflicting rows are installed once after all windows for their
pair; no helper survives. Immutable element/CASE nodes remain the sole execution
authority, not a second capture registry.
Foreign defaults use the same requirement collector and cast windows.
Inline foreign members bind in their real CREATE FOREIGN TABLE; independent
late attributes bind in the existing post-rules foreign-default section, not
a fabricated graph DEFAULT event. Conflicting foreign default epochs defer
only their affected members, without changing ordinary global deferral. Final
cast rows needed after those windows retain graph dependency order but emit
once at the end of that section. No provider callback/default evaluation is
introduced.

Selected integration proof (run each also with `REFERENCE=1`):

```sh
make check RUN='^TestReferenceCastDefinitionForeignDefaults$'
make check RUN='^TestReferenceFDWImport(DefaultPersistence|Persistence)$'
make check RUN='^TestReferenceCastDefinitionPersistence$'
```

The new witness independently expects old/fresh ordinary default values 1/2,
foreign per-attribute dependencies on cast_old/cast_new (including conflicting
epochs in one declaration), and one final implicit function cast to cast_new.
It uses the existing portable FDW metadata fixture with NO HANDLER and
NO VALIDATOR. Native Dump/Reset/Restore and actual emitted SQL replay on
PostgreSQL repeat the same independently specified catalog/value assertions;
the scenario performs no remote scan.

Generated-column epoch conflicts use explicit seeds for the existing
`table.PrepareGenerationPlaceholders` component planner, not a second traversal
or catalog authority. SET EXPRESSION windows are per attribute; the collector
rebuilds only affected CREATE dependencies using `tableDumpPredecessors`.
Recursive historical CHECKs use the existing real ADD executor after every
affected declaration and before affected rows; local leaf CHECKs use ONLY.
The generated replay witness uses scalar xid input and existing array
constructors: the old binary xid→float4 recipe preserves word `0000002a`,
while the new INOUT recipe produces numeric 42, word `42280000`. Both retain
lower bound zero, their NULL element and stored-generation flags. The final
catalog contains one explicit INOUT row with function OID zero; no
`pg_catalog.float4(integer)` provider or immutable SQL routine syntax is needed.
