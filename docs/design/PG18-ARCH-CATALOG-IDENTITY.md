# PG18-ARCH-CATALOG-IDENTITY — Object identity, namespaces, and dependency contract

## 1. Decision status, scope, and evidence

This is the completed **design decision artifact**, not an implementation of catalog SQL. All signatures below are future internal contracts. No SQL, unit tests, reference tests, smoke commands, or production gates were executed for this design. The only deliverable is this file; the frozen plan remains unchanged.

Decision: extend the existing parser/engine/driver seams into **one engine-owned descriptor authority**, with identity-backed relation storage, immutable catalog reads, private staged changes, dependency-aware deletion, and object-sensitive prepared-plan validity. Names are indexes, never identity. There is no alternate catalog, binder, DSN interpretation, transaction store, or metadata synthesis path.

Transaction visibility, MVCC, lock scheduling, session settings, ACL algorithms, cluster storage, and downstream SQL syntax are deliberately owned by their respective capability records. Their absence does not prevent settling catalog interfaces here; they are integration boundaries, not claims of completed prerequisites. The selected record has no prerequisites.

### Evidence and version policy

Repository anchors are relative to the assigned worktree, inspected during this design:

- `internal/engine/engine.go:13-109,245-343`: `Engine.tables` is the single name-keyed authority; `table` holds columns/name/index/rows. `Describe/describe` performs binding. `Execute` repeats binding under `e.mu`, validates before publication, and captures SELECT rows plus copied result columns. Preserve this binder, coercion, cancellation, and synchronization seam.
- `internal/parser/ast.go:18-49`, `internal/parser/grammar.y:47-85`, `internal/parser/lexer.go:49-66,154-164,231-251`: relation names are strings, positions are offsets, and identifier folding/quoted decoding/UTF8-safe 63-byte truncation already belong at input parsing.
- `conn.go:51-110`, `stmt.go:11-48`: prepare currently discards binding and retains AST/count only; both prepared execution paths enter `conn.execute` and bind again. Selective caching requires intentional migration, not relabeling existing behavior.
- `internal/engine/control.go:14-44,128-171`: Reset replaces the map, Dump captures under lock before I/O, Restore reads/parses/executes in an isolated engine before replacement. `internal/engine/snapshot.go:9-29` retains committed rows/projection independently of current authority.
- `driver.go:17-58`, root `control.go:11-65`: exact opaque DSN within one Driver defines engine ownership; independent Drivers isolate engines. Native controls release their borrowed connection before I/O. Closing connections does not erase catalogs.
- `internal/sqlstate/error.go:4-14`, `reference_test.go:17-64,84-125,145-215`: current transport has Code/Message/Schema/Table/Column; failure fixtures check only SQLSTATE, while success fixtures independently check rows/types/counts. Per-step prepare/close cannot witness a held plan across DDL.
- `reference_helpers_test.go:20,57-88`: the reference runtime remains PostgreSQL **18.0**, UTF8/C, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, expected server version `180000`.
- `control_test.go:97-183`: existing source specifies failed-restore sentinel preservation and ordered cursor survival; these are reusable test seams, not passing-test evidence from this session.

Release semantics come from PostgreSQL 18 manuals, identified as **18.6** by supplied research: [schemas §5.10](https://www.postgresql.org/docs/18/ddl-schemas.html), [search_path/check_function_bodies §19.11.1](https://www.postgresql.org/docs/18/runtime-config-client.html#GUC-SEARCH-PATH), [dependencies §5.15](https://www.postgresql.org/docs/18/ddl-depend.html), [pg_depend §52.18](https://www.postgresql.org/docs/18/catalog-pg-depend.html), [OID identity §8.19](https://www.postgresql.org/docs/18/datatype-oid.html), [identifier syntax §4.1.1](https://www.postgresql.org/docs/18/sql-syntax-lexical.html#SQL-SYNTAX-IDENTIFIERS), [catalog ownership §52.1](https://www.postgresql.org/docs/18/catalogs-overview.html), [PREPARE Notes](https://www.postgresql.org/docs/18/sql-prepare.html), [ALTER §5.7](https://www.postgresql.org/docs/18/ddl-alter.html), [diagnostic fields §54.8](https://www.postgresql.org/docs/18/protocol-error-fields.html), and [SQLSTATE Appendix A](https://www.postgresql.org/docs/18/errcodes-appendix.html). Only 18.6-specific differences consume `PG18-REF-ORACLE-18-6`; do not change the pinned runtime speculatively.

Supplied research cites local PostgreSQL `configure.ac:20` as **20devel**, `objectaddress.h:23-32`, `namespace.c:655-708,886-904`, `dependency.c:1080-1177`, and `plancache.c:10-40,871-890`. These corroborate architecture only, not release diagnostics or observed runtime behavior. Ramsql's name-map schema is not a dependency/publication oracle and is not reused. Backlog ownership below follows the bounded records cited in research and the frozen plan; adjacent records are not implemented here.

## 2. Identity and ownership invariants

Future engine-local vocabulary:

```go
type OID uint32
type CatalogEpoch uint64
type DefinitionRevision uint64
type ObjectAddress struct { ClassID OID; ObjectID OID; SubID int32 }
type ObjectRef struct { Epoch CatalogEpoch; Address ObjectAddress }
```

1. An address identifies an object within a database engine and epoch. `ClassID` is the OID of the owning catalog, not an enum of object kinds. `SubID=0` names the whole object; column subobjects use stable attribute numbers. Mutable slice offsets are execution projections, not persistent column identity.
2. Engine identity is an implicit domain of every internal read/ref/plan/stage. A ref from another engine cannot be consumed even if its numbers coincide. Constructors are engine-owned; reads/stages/plans retain that ownership. Neither metadata OIDs nor an `ObjectRef` alone authorize cross-engine attachment.
3. Engine owns a nonzero OID allocator. Do not reuse an allocated OID in one epoch, including after drop. Skip registered fixed builtin OIDs; detect exhaustion before publication without wraparound/collision. Aborted allocations may leave gaps. Epoch/revision exhaustion also fails before mutation rather than making an old token valid.
4. Rename or schema move preserves address, epoch, column SubIDs, and storage identity; it publishes a new immutable descriptor revision. Drop/recreate gets a different ref. Dropped attribute numbers are not reused or compacted; visible attribute order and storage layout are feature-owned mappings.
5. Reset/Restore preserves Driver/engine identity but replaces catalog lifetime by incrementing epoch. Numeric OIDs in a replacement may coincide; old-epoch refs cannot validate. Exposed catalog OIDs omit the internal engine/epoch domain. Test generated identities for valid values and relationships, never another server's allocation sequence. Feature-registered fixed builtin OIDs remain exact.
6. Published descriptors, bound definitions, metadata snapshots, and cursor metadata are immutable for their retained-view lifetime. Rename never mutates a descriptor already used by a reader. Bound stored expressions/views carry refs across rename/move; textual late-bound inputs resolve at analysis/execution as specified by their feature.
7. The engine owns the address map, uniqueness/name indexes, forward/reverse dependency indexes, and storage keyed by relation identity. Features own descriptor content and validation rules, not competing writable stores. Every committed index/edge/storage entry refers to an object in that same authority, except intentional pinned/shared boundaries.

## 3. Normalized names and resolution scopes

Future parser input:

```go
type Identifier struct { Text string; Pos Position }
type ObjectName struct { Parts []Identifier }
```

`Parts` holds one through three individually normalized identifiers. Fold unquoted ASCII, decode doubled quotes, preserve quoted case, and truncate UTF8 safely at 63 bytes exactly once at lexer/input parsing. Resolver does not normalize again. Keep positions for each component and original SQL for diagnostics/reanalysis. `"A.B"."t"` has two components, not three. Textual reg* input reuses identifier parsing; do not split raw text on dots or concatenate components into one lookup string.

CAT-CORE migrates `CreateTable.Name`, `Insert.Name`, and `Select.Name` together; column strings remain normalized strings until column syntax owners extend them. Baseline grammar whole-input rejection remains intact. Qualification is bounded to one/two-part public lookup initially; later owners enable three-part/special session forms through the same contract.

```go
type ResolutionContext struct {
    DatabaseName string
    CurrentUser string
    ExplicitPath []PathEntry
    TempSchema ObjectRef
    EnvironmentRevision uint64
    Access NamespaceAccess
}
type PathEntry struct { Name string; User bool; Temporary bool }
type NamespaceAccess interface {
    CanUse(ObjectRef) bool
    CheckCreate(ObjectRef) error
}
```

SESSION supplies a coherent context/path/environment revision; AUTHORIZATION supplies checks and diagnostics. `User` denotes the special `$user` entry, `Temporary` the special pg_temp entry, not literal quoted names. Native attachment supplies DatabaseName without parsing an opaque DSN. A missing temp ref is absence, never an allocator side effect of reference lookup. Special temp creation is delegated to its session/temp owner before ordinary catalog staging.

Future resolver operations:

```go
ResolveReference(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) (ObjectRef, error)
ResolveCreation(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) (ObjectRef, error)
LookupCandidates(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) ([]ObjectRef, error)
```

`LookupKind` distinguishes Relation, Type, Routine, Operator. `ResolveCreation` returns the destination **schema ref**, never an existing relation. Routine/operator analysis uses ordered candidates and the existing feature signature/type binder; it is not a second overload/coercion algorithm. Exact routine DDL supplies input-type signature keys. Reference resolution does not authorize DROP/ALTER; feature handlers check ownership and class-specific permissions after resolution.

| Input/operation | Required behavior |
|---|---|
| Qualified reference | Explicit schema only, no search-path fallback; check existence and USAGE. Missing relation remains command-specific 42P01, not a universal schema error. |
| Unqualified reference | Expand special entries; silently omit missing/non-USAGE schemas; select first matching relation/type. Deduplicate schema identities preserving earliest position. |
| Implicit lookup schemas | pg_catalog precedes explicit entries unless explicitly positioned. Session temp precedes pg_catalog for relation/type only. Never use temp for unqualified routine/operator lookup. |
| Explicit pg_temp | Maps only to the current session's namespace. Do not cross-session attach or create on read. Explicit positioning is respected. |
| `$user` | Expand using CurrentUser and include only an existing usable schema. Literal quoted spelling stays literal. |
| Unqualified CREATE | First existing USAGE-usable **explicitly selected** schema after expansion; not implicit lookup schemas and not whichever schema already has this object name. Check CREATE there; denial must not fall through. No usable creation schema: 3F000. |
| Qualified CREATE | Check explicit schema existence and CREATE permission, not generic lookup USAGE. Missing schema: 3F000. |
| Three-part name | Database must equal attached DatabaseName; another database yields 0A000 without cross-attachment. |

Relations, sequences, indexes, views, materialized views, and foreign tables share schema/name uniqueness. Routine uniqueness also includes ordered input type identities; operator keys include operands. `NameKey` is an immutable comparable class-owned key encoding, with central uniqueness validation, not an ambiguous formatted string or universal `(schema,name)` rule. Quoting/63-byte normalization collisions go through ordinary uniqueness checks. Catalog objects without a schema use their class-defined global key rather than a fabricated public namespace.

## 4. Authoritative descriptors and metadata reads

```go
type Descriptor interface {
    Address() ObjectAddress
    Revision() DefinitionRevision
    Namespace() ObjectAddress
    Name() string
    Kind() ObjectKind
}
type CatalogRead interface {
    Epoch() CatalogEpoch
    Descriptor(ObjectAddress) (Descriptor, bool)
    Lookup(NameKey) []ObjectAddress
    Dependents(ObjectAddress) []DependencyEdge
    References(ObjectAddress) []DependencyEdge
}
type CatalogProvider interface {
    ClassID() OID
    Validate(CatalogRead, Descriptor) error
    CatalogRows(CatalogRead) (CatalogRows, error)
}
RegisterCatalogProvider(provider CatalogProvider) error
```

These are internal engine contracts. `ObjectKind` is feature discrimination within the owning class. A class descriptor owns subobject details, allowing stable attribute-number lookup without a second column authority. `CatalogRead` is a coherent immutable retained view; returned descriptors/collections are borrowed read-only values valid for that lifetime. The acquiring engine/session owns retention; no pointer to mutable current maps may escape. Stage reads overlay the same view coherently.

Provider registration occurs at engine initialization, rejects duplicate class IDs, and has no publication side effects. Providers validate purely against the supplied staged view. `CatalogRows` is a typed immutable snapshot using existing engine values and exact feature-supported columns. Provider callbacks never execute during final publication. Cache projections only with view/ref revisions; invalidation cannot make an old retained projection change under its reader.

Feature creation stages the descriptor, its bound definitions, edges, and storage. SQL pg_class/pg_attribute/pg_namespace/pg_depend readers project those same objects from one CatalogRead, with AUTHORIZATION filtering as required by their actual surface. They are not separately writable tables and do not synthesize pretend unsupported objects. Initialization registers public/pg_catalog and supported builtin identities through the same validation path. Unimplemented feature providers/kinds remain absent, not successful no-ops. Fixed OIDs are supplied only by the owning feature's exact contract.

Database-local descriptors remain engine-owned. Cluster-shared identities and pg_shdepend require a separately assigned shared owner hook. Do not duplicate shared ownership in each engine or implement a cluster store here. Any future shared-reference provider must retain the proper ownership domain and coherent read context; this interface does not silently make database-local OIDs globally unique.

## 5. Staged DDL and dependency traversal

```go
type DependencyKind byte // n, a, i, P, S, e, x
type DependencyEdge struct {
    Dependent ObjectAddress
    Referenced ObjectAddress
    Kind DependencyKind
}
// DropMode: Restrict or Cascade
// Methods on the owner-private CatalogStage:
Create(d Descriptor, edges []DependencyEdge) error
Alter(ref ObjectRef, replacement Descriptor, edges []DependencyEdge) error
PlanDrop(targets []ObjectRef, mode DropMode) (DropPlan, error)
ApplyDrop(plan DropPlan) error
```

Edges point **dependent → referenced**, with indexes in both directions. Exact duplicate edges collapse; distinct kinds between a pair remain. Persistent edges describe definitions, not transient prepared caches. Column dependencies address SubID, and whole-object deletion covers its subobjects. References to pinned objects may intentionally omit graph rows; pin protection is descriptor policy, not inferred from graph absence.

`Alter` supplies the complete replacement outgoing-edge set for the changed object/subobject scope. It cannot change address or epoch. Staging centrally rejects dangling endpoints, invalid subobjects/kinds, ownership-domain mismatches, uniqueness collisions, and unsupported changes before publication. Associated index/sequence/constraint changes come from feature-owned refs, never guessed from matching names. Rename does not rewrite prepared SQL. Stored bound definitions retain refs; display/deparse reads current names from the supplied view while preserving a view's declared output labels.

### State transitions

| Operation | Private work | Commit effects | Failure effects |
|---|---|---|---|
| CREATE | Normalize/resolve creation schema; allocate; construct descriptor/storage/edges; validate staged view | One new identity plus name/index/metadata/storage/edge entries, definition ChangeSet | No committed object or invalidation; allocator gap permitted |
| ALTER/rename/move | Resolve/fetch ref and revision; stage immutable replacement, all associated moves and edge changes; check destination collision | Same identity, new revision; old/new name keys replaced together, edge/index/metadata coherence | Original names/location/revisions/rows/edges intact |
| DROP | Resolve all targets; preflight closure/permissions/reasons; prepare deletions and remove storage/index/incident edges | All closure objects/subobjects removed in one batch; changed/deleted refs invalidate plans | RESTRICT/ownership/cancellation failure leaves entire graph and data intact |

Successful staging is not publication. Final validation/cancellation applies to the entire statement batch, including its rows. Unsupported alteration fails rather than publishing only the supported subset. `DropPlan` is tied to its stage/read preconditions, retains selected addresses and reasons, and cannot be replayed against changed authority without revalidation.

### Deletion closure rules

Use a worklist with visited address/reason sets to a fixed point; whole-object deletion subsumes its subobjects. Compute closure before deciding RESTRICT blockers, so explicit multi-target removal and automatically included objects do not falsely block each other. Retain reasons for diagnostics; order deletion safely within preflight, and handle cycles/shared descendants without repeated deletion or partial publication.

| Kind | Referenced object deleted | Direct dependent deletion / boundaries |
|---|---|---|
| NORMAL `n` | Outside closure: RESTRICT blocker; CASCADE adds dependent | Allowed independently, subject to other edges/permissions |
| AUTO `a` | Add dependent even under RESTRICT | Allowed; do not promote to containing owner |
| INTERNAL `i` | Owner deletion includes implementation; dependency-driven implementation deletion promotes its owner | Reject direct implementation-only deletion; owner-directed diagnostic |
| PartitionPrimary `P`, PartitionSecondary `S` | Include dependent when a parent is deleted | Require at least one partition parent in closure; never promote an unrelated parent because another dependency was removed. P selects preferred diagnostic parent, not stronger traversal permission. |
| EXTENSION `e` | Extension-owner deletion includes member; dependency-driven member deletion follows owner semantics | Reject direct member-only deletion; preserve extension membership/serializer distinctions rather than treating it as an ordinary AUTO edge |
| AutoExtension `x` | Include dependent on extension deletion even under RESTRICT | Independent dependent deletion allowed |

Combine all edge kinds: an automatic path may permit inclusion but cannot erase INTERNAL/extension/partition restrictions. Pin protection applies even under CASCADE. After closure settles, a NORMAL blocker outside it yields 2BP01. All ownership restrictions and authorization checks precede applying any removal.

Concrete containment distinction: an FK has AUTO containment in its independently owned orders table and NORMAL reference to products. Dropping products CASCADE removes FK, **not orders**. A view's ON SELECT implementation rule has INTERNAL ownership; deleting a referenced base through that rule promotes removal to the view, recursively including dependent views. Do not implement generic backward containment traversal.

### Routine discovery limit

String/deferred routine bodies retain externally bound signature/property edges only. Optional creation-time body validation does not establish durable body dependencies. Never scan body strings or dynamic SQL to invent references. SQL-standard BEGIN ATOMIC bodies retain addresses actually bound at definition. Routine owners provide execution/revalidation and supported descriptor kinds. This distinguishes a surviving string routine that later fails or resolves a same-name recreation from an eager routine removed by CASCADE.

## 6. Prepared source validity and rebinding

```go
type PlanDependency struct {
    Ref ObjectRef
    Definition DefinitionRevision
    Statistics uint64
}
type BoundPlan struct {
    Source string
    AST parser.Statement
    Parameters ParameterInfo
    ResultColumns []parser.Column
    Dependencies []PlanDependency
    EnvironmentRevision uint64
}
// Future Engine methods; both reuse describe's binder:
Prepare(ctx context.Context, view CatalogRead, rc ResolutionContext,
    source string, ast parser.Statement) (*BoundPlan, error)
ExecutePrepared(ctx context.Context, view CatalogRead, rc ResolutionContext,
    plan *BoundPlan, args []driver.NamedValue) (Result, error)
```

A prepared source is session-owned and retains original SQL, original parameter count/types, initial fixed result contract, and a replaceable complete bound representation. `ParameterInfo` reuses engine inference; type identities/typmod/collation metadata extend the same descriptor when owning features support them. database/sql and later SQL PREPARE use the same binder/validity contract, not independent dependency systems.

Validity checks engine domain, epoch, existence and definition/statistics revision of referenced refs, and relevant environment revision. Changes to path, current user/access environment or relevant namespace accessibility may trigger reanalysis. Namespace replacement/deletion must invalidate its users; merely adding an earlier-path relation is not a negative-name dependency or blanket environment change. Statistics have separate revisions; INSERT does not change a definition merely because rows changed. Unrelated catalog changes do not invalidate all sources.

State machine: **Unbound → Valid → NeedsReanalysis → Valid**, or **NeedsReanalysis → RebindError**; Close prevents later use. Replacement marks old epochs invalid. On next use, analyze original unchanged SQL against the current caller's coherent read/context, validate original parameters and fixed result contract, then install the complete new binding. Failure cannot execute stale pointers, mutate the public contract, or partially install dependencies. A later valid context/catalog can attempt reanalysis again; this is not an automatic statement retry policy.

Public database/sql and SQL PREPARE are fixed-result consumers in this contract: preserve initial arity, labels, supported type identities/typmods/collations. An incompatible shape returns SQLSTATE 0A000, primary `cached plan must not change result type`. Parameters retain count/types; do not silently mutate `NumInput` or re-infer externally fixed parameter types. Direct statements analyze fresh. No adaptable internal consumer is introduced. Already returned cursor rows/types/labels stay immutable, including across catalog replacement.

Prepared references are ephemeral validity dependencies, not pg_depend edges: prepared statements never block DROP RESTRICT. Stored views/expressions and prepared SQL are intentionally different: a view follows a renamed identity, whereas reanalysis of prepared original text can fail or choose a newly shadowing object.

## 7. Transaction publication and replacement hooks

```go
type PublicationToken struct { Epoch CatalogEpoch }
type ChangeSet struct {
    Definitions []ObjectAddress
    Statistics []ObjectAddress
    Namespaces []ObjectAddress
    Replacement bool
}
// CatalogStage and PreparedCatalogPublication are opaque, engine-owned.
BeginCatalogStage(base CatalogRead) *CatalogStage
ValidateCatalogStage(ctx context.Context, stage *CatalogStage) (*PreparedCatalogPublication, error)
PublishCatalog(prepared *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error)
DiscardCatalogStage(stage *CatalogStage)
ReplaceCatalog(ctx context.Context, replacement CatalogRead) (ChangeSet, error)
```

The stage is an owner-private overlay of the same descriptor authority, not a second writable catalog. It observes per-address revisions, touched uniqueness keys and dependency neighborhoods needed by deletion closure. This includes detecting a new dependent inserted after a drop preflight: reverse-edge preconditions cannot be limited to the target descriptor revision alone. Stage catalog/storage reads are coherent for their owner. The TX owner chooses base visibility, snapshot retention, transaction/savepoint attachment, rollback discard, and publish timing; LOCKING chooses scheduling. This design specifies none of their MVCC/isolation/retry algorithms.

Lifecycle: **Open → Validated → Published** or **Open/Validated → Discarded**. Validation freezes/preallocates the complete batch and performs provider validation outside final publication. Mutating a stage after validation requires new validation, never reuse of an obsolete prepared publication. Publish is single-use. Discard is idempotent; using discarded/published stages is an internal lifecycle error, not fabricated SQLSTATE.

Under the existing engine publication boundary, recheck token epoch, touched descriptors/keys/graph preconditions and row-storage preconditions supplied by the transaction owner. Then apply rows, descriptors, names, edges and lookup indexes as one indivisible batch. No external I/O, provider callback, or fallible partial mutation occurs after final preflight. Commit-visible invalidation follows successful installation and is co-published with the authority; lazy validity checks cannot miss a dropped ref even if event delivery is deferred. `Definitions` includes altered/deleted addresses, `Statistics` only statistics changes, `Namespaces` relevant namespace changes; lists deduplicate. Private changes/discard emit no committed ChangeSet.

A touched-precondition mismatch yields internal `CatalogConflict` before mutation. Unrelated key/address changes do not conflict solely due to a global generation increment. TX maps conflicts to its operation-specific diagnostics; the catalog does not invent SQLSTATE or retry. Stage/plan ownership also fences cross-engine usage.

Reset/Restore calls the same replacement boundary after complete successful staging. Replacement validates closure/index/metadata/storage integrity, installs one new epoch and emits `Replacement=true`; it never merges Restore with the target. The replacement read is from an isolated instance of the same engine machinery, with its identities installed in the target's new lifetime, not exposed as foreign-engine refs. Reset reinitializes supported builtin schema/provider state while clearing user objects/data. Epoch advancement occurs only after successful validation and cancellation preflight.

An old-epoch stage returns ordinary sentinel `ErrCatalogReplaced`, primary Go message `stuntgres: catalog replaced before publication`; it cannot resurrect previous data. `PG18-CONTROL-TRANSACTIONS` consumes that fencing boundary. Failed/read-failing/canceled Restore changes neither committed state nor epoch nor an otherwise eligible stage. Native adapter/context/I/O errors remain ordinary Go errors. SESSION cleans abandoned private transactions, not committed engine catalogs.

Retained reads/cursors remain usable against their prior immutable lifetime. New readers get only replacement state. Dump captures one view then serializes outside target locks, quoting qualified components separately. Future serializers consume exact feature descriptors/dependency ordering; unsupported kinds cannot silently disappear from a purported complete dump. Root native-driver validation/release-before-I/O and Driver exact opaque-DSN lifetime stay unchanged.

## 8. Clean cutover and session-sized backlog ownership

No caller or test is changed by this design. Each future implementation session below owns a bounded public behavior and removes its obsolete path rather than keeping aliases/shims. Capability prerequisites govern execution order; this table is not a claim that adjacent records are ready.

| Backlog owner | Concrete cutover / bounded outcome | Independent exit witness |
|---|---|---|
| PG18-CAT-CORE | `ast.go` CreateTable/Insert/Select.Name, grammar CREATE/INSERT/FROM name actions, existing generated grammar; `engine.go` New/table/Describe/describe/Execute authority; `internal/engine/control.go` Reset/Dump/Restore migrated together. Register public/pg_catalog, stable baseline relation/column identities, read/stage/provider seams. Remove Engine.tables/table.name as authority. No CREATE SCHEMA, ACL commands, rename or MVCC. | W1, W3 normalization, W11 replacement; qualified public CREATE/INSERT and unqualified SELECT share one ref and preserve baseline coercion/atomicity. |
| PG18-DDL-SCHEMA | Schema descriptors, schema DDL and stage uniqueness/dependencies, without alternate schema map | W2 qualification/isolation and W3 missing schema; exact pg_namespace projection when reader exists |
| PG18-DDL-SEARCH-PATH | Session path expansion, kind-specific reference/creation context; compose SESSION/AUTHORIZATION | W2 path permutations/temp/$user; W3 no CREATE-permission fallthrough |
| PG18-DDL-TABLE-CORE | Public drop target resolution, closure preflight and complete staged removals for supported kinds | W5 RESTRICT and W6 multi-target/containment as dependent features become available |
| PG18-DDL-ALTER-RENAME | Relation/column stable identity, new revision/key replacement; bound-reference deparse | W4 identity/old-name failure/collision atomicity |
| PG18-DDL-ALTER-SCHEMA | Stage relation and feature-associated identity moves and destination collision checks | W4 qualified names and surviving views; no name-guessed associated objects |
| PG18-DDL-VIEW | View descriptor/bound query/INTERNAL implementation edges and serializer | W4 rename survival, W6 recursive view removal |
| PG18-DDL-FOREIGN-KEY | Real FK descriptor, NORMAL referenced-key edge and AUTO containment; data enforcement owner | W5 exact RESTRICT diagnostics, W6 preserve independently owned orders |
| PG18-ROUTINE-DROP | Exact signature keys, routine-owned diagnostic and drop graph integration | W7 argument-type restriction and independent overload identity |
| PG18-ROUTINE-SQL-BODIES | Eager BEGIN ATOMIC vs string/deferred binding, external signature edges, execution/revalidation | W7 body-only table drop/recreation and no speculative dynamic-string edges |
| PG18-DRIVER-PREPARED | Session-owned source/lifecycle, original parameters/result contract through shared binder | W8 held statement, Close/NumInput/compatible selected-column behavior |
| PG18-DRIVER-PREPARED-REBIND | `conn.go` PrepareContext/execute; `stmt.go` ast/count/ExecContext/QueryContext to retained source/plan validity; engine shared binder returns dependencies. Remove unconditional prepared re-describe path. | W8 triggers/fixed result and W9 nontrigger; direct statements remain fresh |
| PG18-CAT-PG-CLASS, PG18-CAT-PG-ATTRIBUTE, PG18-CAT-PG-NAMESPACE, PG18-CAT-PG-DEPEND | Each bounded record adds exact same-view projections over owning feature descriptors/edges, never another catalog | W1/W4/W6 addresses and metadata coherence, supported objects only, exact privilege filtering |
| PG18-DRIVER-ERROR-FIELDS | Extend internal/sqlstate and root transport plus reference failure records; remove `engine.go` sqlError unconditional Schema=public. Features own wording/conditional fields. | W3/W5/W8 complete diagnostic records; no fabricated object/source fields |
| PG18-TX-CORE | Consume coherent views/stages/publication only for assigned initial INSERT/SELECT behavior; later DDL owners compose these hooks | W10 publication, private discard and conflict preconditions; do not widen TX-CORE to all transactional DDL |
| PG18-CONTROL-TRANSACTIONS | Bind stage epoch fencing to native replacement/abandoned transaction handling | W11 no stale resurrection and old cursor lifetime |
| PG18-CONTROL-SCRIPTS | Feature serializers/dependency ordering through ordinary parser/executor and replacement; preserve root control validation | W11 round trip and failed Restore preservation, no lock over I/O |

SESSION/TX-VISIBILITY/AUTHORIZATION/LOCKING contracts supply context/visibility/access/scheduling; partition/extension kinds remain dormant until their actual owners create those objects. Registering enum kinds does not implement their SQL.

Observed production migration hits: `engine.go:75,90,288`; `internal/engine/control.go:23,39-40,161,170`; `conn.go:61,82`. Engine tests also call Describe/Execute; migrate their helpers and direct calls at the owning implementation cutover, not by weakening assertions. Both stmt execution methods currently route through `conn.execute` and must move together. Search anew at implementation for `e\.tables|stage\.tables|\.Describe\(|\.Execute\(` in internal/conn.go/stmt.go, since future edits may add callers. Keep `driver.go` engine ownership, root `control.go` native validation, and snapshot/rows cursor retention intentionally unchanged except necessary type integration. Do not retain legacy name-map re-exports or a second binder for controls/tests.

## 9. Independent acceptance witnesses and diagnostics

The following are **predetermined future unit/public/reference scenarios**, not observations of executing SQL. Assert each backend against these expectations before parity. Use public direct and prepared database/sql where applicable, a held physical sql.Conn/sql.Stmt for multi-step interleavings, and the pinned serialized PostgreSQL 18.0 fixture. Do not replace a held-plan witness with prepare/close on every step. Without ORDER BY compare bags/multiplicity; explicit order is required for ordered cursor witnesses. Generated identifiers are checked relationally.

Diagnostic records independently represent severity, five-character SQLSTATE, primary English message, DETAIL/HINT, conditional Schema/Table/Column/Datatype/Constraint, and original-source Position. Positions are one-based characters, not bytes; internal position requires internal query. Do not copy PostgreSQL C file/line/routine into engine errors or infer optional fields from words in a message. Exact optional presence/absence and positions not established by supplied evidence must be captured from the pinned oracle by the owning feature before its acceptance fixture is finalized. That evidence work is required for downstream implementation, not an unfinished decision here.

### W1 — Identity and normalization (positive/boundary)

`CREATE TABLE public.t(id integer); INSERT INTO public.t VALUES(1);` has INSERT affected count 1. Both `SELECT id FROM public.t` and `SELECT id FROM t` return `[(1)]`, name `id`, type `INT4`. Unit resolution returns one ref, descriptor/index/column-address coherence, and identity-keyed storage. Separate Drivers/different exact DSNs cannot see t; reconnecting to the same engine can. Unquoted FOO collides with quoted `"foo"` (42P07), quoted `"Foo"` differs. `"A.B"."t"` selects one dotted schema identifier. Two overlong identifiers sharing a UTF8-safe 63-byte prefix collide; suffix text cannot bypass uniqueness. Drop/recreate differs from old ref; rename preserves it. Allocator exhaustion/reserved IDs and deleted attribute slots have unit boundary witnesses with no partial publication or wraparound.

### W2 — Schema shadowing and creation (positive/interleaving)

With a.t(v integer)=11 and b.t(v integer)=22, path a,b yields `[(11)]`; path b,a yields `[(22)]`; a.t always yields `[(11)]`, metadata `v/INT4`. Under b,a, unqualified CREATE new places new in b even if a already has that name. Path missing,a ignores missing. Two held sessions with opposite paths get their independently expected rows, without one session mutating another's context.

Unit path witnesses cover implicit pg_catalog ahead of explicit schemas, explicit pg_catalog position, implicit temp ahead of permanent relation/type, no temp routine/operator candidates, identity deduplication and `$user` existing/accessibility expansion. Explicit quoted dotted names are not special entries. Exact qualified/current-database lookup never cross-attaches. Assert schema identity/index ownership, not cross-server numeric IDs.

### W3 — Resolution and privilege failures (failure/atomicity)

Empty usable creation path: 3F000, expected primary `no schema has been selected to create in`. Missing explicit creation schema: 3F000, expected `schema "missing" does not exist`. These primary wordings are supplied research expectations, corroborated by development-source evidence, **not observed release diagnostics**; owning features confirm against pinned 18.0. Missing relation: 42P01; missing column: 42703; duplicate relation: 42P07; denied chosen-schema CREATE or explicit access: 42501; different database qualification: 0A000. Feature fixtures must pin command-specific primary wording/positions and conditional fields rather than use a universal lookup message.

An inaccessible earlier path entry is ignored for unqualified reference. A USAGE-usable selected creation schema without CREATE must fail, not fall through to the next schema. After each failure, unit authority/name/storage/edge state and publication events are unchanged; public queries show original data only. Cancellation returns the ordinary context error at its adapter boundary, not a made-up SQLSTATE.

### W4 — Rename/move with dependencies (positive/failure/interleaving)

Bind view v to a.t(id) containing 1 and retain relation/column refs. Rename t→renamed, id→new_id, then move to b. New names return 1, relation and column refs are unchanged, view returns its original declared output/data, metadata uses current base names, graph endpoints remain addresses. Old relation text fails 42P01; old column text on the renamed relation fails 42703. A destination collision preserves original schema/name/rows/edges/revisions with no committed invalidation.

A reader attached before publication sees a complete old view; a reader attached after sees a complete new view, never a new name key pointing to an old-name mutable descriptor. TX selects visibility. A stored view follows refs; a held prepared original `SELECT id FROM a.t` becomes invalid on relevant DDL and reanalysis can fail rather than rewriting SQL to the new names. Exact collision/rename diagnostics and positions are feature-specific pinned-oracle fixtures.

### W5 — Documented RESTRICT diagnostic (failure)

Products is referenced by `orders_product_no_fkey` on orders. `DROP TABLE products RESTRICT` yields:

- severity `ERROR`, SQLSTATE `2BP01`;
- primary `cannot drop table products because other objects depend on it`;
- DETAIL `constraint orders_product_no_fkey on table orders depends on table products`;
- HINT `Use DROP ... CASCADE to drop the dependent objects too.`

These strings are specified in manual §5.15. Products, orders, FK, rows, indexes and both graph directions remain unchanged. Do not infer Schema/Table/Constraint or position presence from that DETAIL; the eventual exact SQL fixture captures their conditional values/absence independently. Multi-blocker diagnostics follow complete closure reasons; do not assert an invented portable graph traversal order.

### W6 — CASCADE, multiple targets and ownership (positive/failure)

Dropping products CASCADE removes products and its referring FK but preserves orders identity and rows. With v→products and w→v, both dependent views disappear recursively, while unrelated same-name relations in another schema remain. `DROP TABLE products,orders` without CASCADE succeeds if all blockers are inside explicit/automatic closure. Units assert the exact deletion set, incident edge removals and zero surviving dangling addresses.

Synthetic graph witnesses cover cycles/shared descendants; AUTO/x direct removal preserves owners; INTERNAL view-rule promotion differs from AUTO FK containment; direct INTERNAL/e member removal is rejected with owner-directed diagnostics; P/S requires a parent in closure and does not promote unrelated parents; pinned descriptors resist CASCADE. Multiple edge kinds retain every restriction. Unit synthetic graphs specify algorithm invariants, not fake acceptance for unimplemented SQL kinds; feature owners later add real SQL and exact diagnostics.

### W7 — Eager versus deferred routines (positive/failure)

A string-bodied SQL routine with user-defined argument type and body-only table reference has type/property edges, no body-table edge. Table DROP RESTRICT may succeed with the routine surviving; invoking it with missing table fails (for this table lookup, 42P01 with feature-pinned message/context); same-name table recreation may restore invocation via fresh resolution. Dropping its argument type remains dependency-aware. Equivalent BEGIN ATOMIC binding records the table address: table RESTRICT fails 2BP01 and CASCADE removes the routine. `check_function_bodies=off` admits deferred validation but adds no guessed body dependency. Units assert exact eager/external edge sets and absence of string/dynamic-SQL scans.

### W8 — Prepared triggers and fixed result (interleaving/failure)

Hold `SELECT v FROM t` under a,b with a.t=11/b.t=22: first use yields 11; change path to b,a, next use yields 22 with `v/INT4`. Relevant definition/statistics/environment changes force lazy analysis; invalid original relation/column text fails 42P01/42703, never stale-pointer execution. Dropping/recreating cannot validate the old ref. Compatible selected-column definitions retain original parameter count/types and fixed output contract.

Hold `SELECT * FROM t` on t(v integer), then add a column. Next use fails 0A000, primary `cached plan must not change result type`, rather than silently changing result metadata. This fixed-result expectation has research-only plancache corroboration and requires pinned-18.0 confirmation in PREPARED-REBIND. Unit shape comparison includes supported type/typmod/collation and the frozen driver label contract. Failed rebind installs nothing; returned old cursor metadata remains immutable. A prepared cache is not a persistent dependency: DROP RESTRICT cannot fail solely because a statement is prepared.

### W9 — Prepared nontrigger shadowing (interleaving)

Path a,b initially has only b.t=22. Prepare and execute `SELECT v FROM t`: 22. Create a.t=11 without changing path or b.t definition: the same held statement still yields 22. A fresh direct statement yields 11. Rename b.t or change path to force reanalysis: unchanged prepared unqualified source now resolves a.t and yields 11. This rejects unconditional generation invalidation, negative-name dependencies and the old bind-on-every-use implementation. Both backends get these independently specified outcomes.

### W10 — Publication and concurrent graph changes (interleaving/failure)

Stage create/rename/drop, inspect its coherent private catalog/rows, then discard/rollback or cancel validation: committed authority and ChangeSet remain unchanged. Successful publication shows complete old/new descriptor/name/edge/storage batches. A prepared executor's validity/read capture uses one coherent view, never an object revision from a different catalog read.

Two stages claim the same absent uniqueness key: first publishes; second returns CatalogConflict before any mutation. Two stages alter the same observed revision conflict similarly. Unrelated keys/addresses may publish without a global-generation conflict. A drop stage preflights products while another stage adds a NORMAL dependent: publishing the drop must detect changed reverse-edge preconditions, not leave a dangling edge or evade RESTRICT. TX owns scheduling and diagnostic mapping; no automatic retry/serialization level is prescribed. Reusing a publication object fails its single-use lifecycle, and discard emits no committed events.

### W11 — Native replacement and retained readers (interleaving/failure)

Capture ordered cursor `[(1),(2)]`, insert 3 elsewhere, then Restore replacement containing 99 or Reset. Old cursor still returns exactly 1,2 and its original metadata; new readers see only `[(99)]` or missing user table after Reset. Old-epoch pending stage fails ErrCatalogReplaced with `stuntgres: catalog replaced before publication` and cannot resurrect data. Epoch-invalid prepared sources reanalyze; old cursors do not.

Corrupt/duplicate/dependency-invalid/read-failing/canceled Restore leaves old sentinel data, epoch and otherwise eligible private stage intact. Reader/writer/context/native-driver errors stay ordinary Go errors. Dump captures one consistent view and performs writer calls without the target lock. Feature-qualified dump/restore round trips preserve logical metadata/data/dependency relationships, not generated numeric OIDs. Reset retains initialized supported builtin namespaces while clearing user objects.

## 10. Acceptance coverage and observed design completion

| Literal requirement | Settled contract | Independent witnesses |
|---|---|---|
| Identity versus names; normalization | §§2–3 refs, epoch/revision/SubID, input-only normalization | W1, W4, W11 |
| Qualified/unqualified names, schema/search-path scopes, DDL/reference distinction | §3 resolver/context/creation and class-specific keys | W2–W3, W9 |
| Authoritative descriptor/catalog ownership and population/read interfaces | §§2,4 immutable view/provider and exact same-view metadata | W1, W4, W6, W10 |
| Create/alter/drop, indexes, dependency kinds, RESTRICT/CASCADE boundaries | §5 staged transitions and fixed-point closure | W4–W7, W10 |
| Invalidation and stale prepared rebinding | §§6–7 selective dependencies, fixed parameters/results and committed changes | W8–W9, W11 |
| Publication hooks without alternate catalog/binder or MVCC | §7 lifecycle, preconditions, co-publication/discard/replacement | W10–W11 |
| Clean-cutover callsites and session-sized backlog symbols | §8 bounded owners, concrete current callers and obsolete paths | §8 exit witnesses |
| Positive/failure/interleaving diagnostics; deferred routine limits | §9 exact documented RESTRICT fields, error ownership, oracle-capture boundary, eager/string distinction | W1–W11 |

Artifact completeness is established by authorship and inspection against this matrix and the frozen plan, not source-text assertions or baseline smoke. Design smoke commands/evidence are empty as allowed by `kind=design` and frozen `smoke_commands=[]`. Full gates belong to the helper and are not claimed here.

Prewalk: the starting session read the selected record, policies, research, frozen plan and relevant worktree code and initialized a concrete plan before its first deliverable write. Configured @smol routing is externally owned. No routing metadata proving a handoff, same-configuration resolution, or disabled override was supplied at authorship; no model/configuration was changed and no handoff is claimed. This absence is not a catalog-design blocker.
