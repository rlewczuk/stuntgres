# PG18-ARCH-CATALOG-IDENTITY — Object identity, namespaces, and dependencies

## 1. Decision, scope and evidence

Use **one engine-owned descriptor authority**: names are indexes, storage follows identity, reads are immutable, DDL is privately staged, and prepared sources track object-sensitive validity. Reuse the parser/binder/executor/control seams; no alternate catalog, binder or DSN parser. This is design only: no SQL/code changes or runtime verification are claimed. SESSION, AUTHORIZATION, TX and LOCKING own settings, access algorithms, visibility/MVCC and scheduling. Cluster storage and downstream SQL syntax remain outside this part.

PostgreSQL facts come first from the **18 manuals**: [schemas §5.10](https://www.postgresql.org/docs/18/ddl-schemas.html), [search_path/check_function_bodies](https://www.postgresql.org/docs/18/runtime-config-client.html), [dependencies §5.15](https://www.postgresql.org/docs/18/ddl-depend.html), [pg_depend §52.18](https://www.postgresql.org/docs/18/catalog-pg-depend.html), [OID types](https://www.postgresql.org/docs/18/datatype-oid.html), [identifier syntax](https://www.postgresql.org/docs/18/sql-syntax-lexical.html#SQL-SYNTAX-IDENTIFIERS), [PREPARE Notes](https://www.postgresql.org/docs/18/sql-prepare.html), [diagnostic fields](https://www.postgresql.org/docs/18/protocol-error-fields.html), [SQLSTATEs](https://www.postgresql.org/docs/18/errcodes-appendix.html). These pages identify 18.6; the reference fixture remains **18.0 UTF8/C**, `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Only minor-specific differences require PG18-REF-ORACLE-18-6. Local PostgreSQL is 20devel, research-only; Ramsql is not a semantic oracle.

Release C corroboration: [REL_18_0 namespace.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/namespace.c) (`preprocessNamespacePath`, `LookupExplicitNamespace`, `RangeVarGetCreationNamespace`), [dependency.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/dependency.c) (`findDependentObjects`, `reportDependentObjects`), [plancache.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/cache/plancache.c) (`RevalidateCachedQuery`), [tupdesc.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/common/tupdesc.c) (`equalRowTypes`). The last two establish fixed-result count/name/type/typmod/collation checks, not runtime observations here.

Current worktree seams, **not release semantic evidence**:
- `internal/engine/engine.go` already owns state/providers/identity-keyed storage (the descriptors, `State`, `Read` and provider registry now live in [`internal/engine/catalog`](../../internal/engine/catalog/README.md)). `New`, `CatalogRead`, `describe`, `Execute`, `namespaceForName`, `resolveRelation` implement bounded public/pg_catalog behavior; Engine.tables no longer exists.
- `internal/engine/catalog.go` already has address/ref/epoch/revision, descriptors/name indexes, providers, create stages, publication and replacement. Dependency graph, ALTER/DROP, signature keys and exact catalog projections remain extensions.
- `internal/parser/ast.go` already uses ObjectName for CreateTable/Insert/Select; Identifier retains decoded Text/Pos, not quoting provenance. Preserve input normalization and whole-input rejection.
- `conn.go` PrepareContext discards inferred binding/types, retaining AST/count; both `stmt.go` execution methods re-enter Execute. Selective caching/fixed-result validation remain future work.
- `internal/engine/control.go` Reset/Restore use ReplaceCatalog; Dump captures descriptors/storage before I/O and quotes schema/name separately. Preserve Driver/exact opaque-DSN isolation, root native validation/release-before-I/O and snapshot/rows retention.
- `internal/sqlstate/error.go` and `reference/fixture/fixture.go` already support full diagnostic records. Feature owners add exact fixtures and held-plan interleavings, not another transport.

## 2. Identity and ownership

The identity types and the per-engine `Domain` live in `internal/engine/ids`; see its [README](../../internal/engine/ids/README.md).

ClassID is the owning catalog OID, not object kind. SubID is zero except stable column attribute numbers; dropped slots are never renumbered/reused. Rename/schema move preserves ref/storage/column identity and changes immutable descriptor revision; drop/recreate gets a different ref. Stored bound expressions/views retain identities, while late-bound text resolves again. Feature-owned storage/projection mappings are not persistent column identity.

**Engine policies**, not universal PostgreSQL promises: all internal reads/refs/stages/plans belong to one engine even when numeric IDs coincide across Drivers. Allocate nonzero OIDs without reuse within an epoch; skip feature-registered fixed builtins, allow aborted gaps and fail exhaustion before mutation, without wrapping OIDs/epochs/revisions. Exposed OIDs omit engine/epoch; generated identities are tested relationally, fixed builtin IDs exactly. PostgreSQL's 4-byte OID is not a database-wide uniqueness guarantee.

Engine holds one `*ids.Domain`, allocated once by `newEngine` and preserved across Reset/Restore; reads carry the catalog owner record that holds it, not the engine ([catalog README](../../internal/engine/catalog/README.md)). The nonzero-size token gives distinct live engines distinct comparable pointer identities; no UUID, global allocator/registry or second catalog. Only owner-bound engine/read/stage construction stamps refs. Copying a complete ref preserves provenance; reconstructing epoch/address does not. Nil owner is invalid at consumption; zero refs represent absence/error or the exclusive pending-temp result, never authority.

Instance attachment remains standard-library Driver behavior: the **same Driver + identical exact opaque DSN** selects one Engine/owner across multiple `sql.Open` pools and all their connections. Different DSNs or Driver instances select independent Engines/owners, even with coincident addresses. Closing a connection/pool does not erase the instance; tokens are never per connection. A DSN can simply name the instance; no syntax/parser is added. Internal `DatabaseName` qualification is a separate native attachment property, not DSN parsing.

At every ref-consuming boundary, reject invalid/foreign reads and nil/mismatched ref owner first with ordinary internal Go errors; then require ref/view/current engine epoch equality (old local lifetime gives ErrCatalogReplaced), and check address/existence/class-owned SubID against that coherent view. Descriptor/storage lookup, Alter/PlanDrop, stage/provider validation, temp/context/NamespaceAccess handoffs and retained plan dependencies must not strip provenance before validation or adopt a foreign ref by its coincident address. Same-epoch retained/private-stage reads check their own descriptors, not later committed state; publication separately checks current touched preconditions. Captured old-epoch reads/cursors remain readable but cannot authorize new execution/mutation.

Engine alone owns address, name/uniqueness, forward/reverse edge indexes and identity-keyed storage. Features own descriptor content, bound definitions and pure validation. Published descriptors, retained catalog projections and cursor metadata never mutate under a reader. Shared identities/pg_shdepend require a separately assigned cluster owner/read domain, not per-engine duplication.

## 3. Names and resolution

```go
type Identifier struct { Text string; Pos Position }
type ObjectName struct { Parts []Identifier }
type PathEntry struct { Name string; User bool; Temporary bool }
type CreationTarget struct { Schema ObjectRef; PendingTemporary bool }
type ResolutionContext struct {
    DatabaseName string
    CurrentUser string
    ExplicitPath []PathEntry
    TempSchema ObjectRef
    EnvironmentRevision uint64
    Access NamespaceAccess
}
type NamespaceAccess interface {
    CanUse(ObjectRef) bool
    CheckCreate(ObjectRef) error
}
ResolveReference(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) (ObjectRef, error)
ResolveCreation(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) (CreationTarget, error)
LookupCandidates(view CatalogRead, rc ResolutionContext, kind LookupKind,
    name parser.ObjectName) ([]ObjectRef, error)
```

Parts has one to three individually normalized components: unquoted ASCII fold, quoted decoding/case preservation, UTF8-safe 63-byte truncation **once at input**, with component positions/original SQL retained. Never split decoded text on dots or concatenate a lookup key; `"A.B"."t"` has two components. Reg* features reuse tokenization, not one universal ObjectName grammar for signatures/types. Existing bounded grammar supports one/two-part names; later owners enable remaining forms.

SESSION supplies coherent context/environment; AUTHORIZATION supplies access/diagnostics; native attachment supplies DatabaseName without parsing DSNs. LookupKind distinguishes Relation, Type, Routine, Operator. Creation returns either a schema ref or `PendingTemporary=true` with zero Schema, never both. The creation caller asks the session/temp owner to initialize that destination, refreshes the coherent view/context and resolves again before allocating/staging the object; it never substitutes public. Reference lookup never initializes temp. Routine/operator candidates feed their feature signature/coercion binder; exact routine DDL supplies ordered input-type identities. DROP/ALTER ownership checks occur after resolution.

| Operation | Rule |
|---|---|
| Qualified reference | Explicit schema, no fallback; check existence/USAGE. Missing relation diagnostics are command-specific, normally 42P01. |
| Unqualified reference | Expand special path entries; ignore missing/non-USAGE schemas; deduplicate by identity; first matching relation/type. |
| Implicit path | pg_catalog first unless explicitly positioned; implicit session temp before it for relation/type only, never unqualified routine/operator. Explicit positions are respected. |
| pg_temp | Decoded spelling aliases this session's temp namespace in path entries and explicit qualification, regardless of quoting. Reference lookup does not allocate; creation can return PendingTemporary for owner initialization. |
| Unqualified CREATE | First effective explicitly selected destination: skip missing/non-USAGE ordinary schemas, but an uninitialized explicit pg_temp reserves the destination if no earlier usable schema won. Implicit lookup schemas never select creation. Check CREATE on the selected ordinary schema; denial never falls through. No usable/pending destination: 3F000. |
| Qualified CREATE | Explicit schema existence then CREATE, not generic lookup USAGE; missing ordinary schema: 3F000. Qualified pg_temp delegates absent-temp initialization to its owner, not ordinary missing-schema failure. |
| Three-part name | Attached database only; different database: 0A000, never cross-attachment. |

Classify PathEntry specials by **decoded spelling**, not quoting: `$user` expands CurrentUser in search_path, including `"$user"`; qualified `"$user".t` is an ordinary literal schema lookup. `pg_temp` aliases temp both in search_path and qualification, including quoted forms. Other quoted spelling/case rules remain unchanged. For path `missing,pg_temp,public` with no temp, references skip missing/uninitialized entries without allocation, while CREATE reserves temp rather than falling through to public; an earlier usable ordinary schema would win instead. User authorization **“Use PostgreSQL 18 rules”** supersedes exactly the prior quoted-literal special policy and missing-explicit-temp creation fallthrough. REL_18_0 namespace.c supplies the resolved semantics; no code implementation is claimed.

Relations/sequences/indexes/views/materialized views/foreign tables share schema/name uniqueness. Routine keys add ordered input types; operator keys add operands. NameKey is a feature-owned immutable comparable encoding, centrally validated; objects without namespaces use their class-global key, not fabricated public. Quoting/truncation collisions undergo normal uniqueness checks.

## 4. Descriptors and catalog projections

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

These specify the complete target contract; current CatalogRead (`catalog.Read`) is a concrete retained owner/state view, and current providers supply Validate only. Extend existing seams, do not introduce a parallel interface/store merely to match pseudocode. A class descriptor owns its subobject lookup; ObjectKind discriminates feature kinds within that class. Returned data is borrowed immutable for the retained read lifetime; stage reads coherently overlay the same authority.

Registration occurs at initialization, rejects duplicate class IDs and has no publication side effects. Provider validation is pure; typed CatalogRows uses existing engine values and projects exact supported columns from the supplied view. SQL pg_class/pg_attribute/pg_namespace/pg_depend are projections, not separately writable/synthetic stores. Apply access filtering only where that SQL surface requires it, not an invented universal raw-catalog filter. Cache by view/ref revision. Initialize public/pg_catalog and supported fixed builtins through validation; unsupported objects/providers remain absent. No provider callback during final publication.

Current inquiry ownership does not relocate this descriptor authority:
`internal/engine/functions` owns immutable CatalogUserName/CatalogForeignKeys,
ObjectAddress, NamespaceInquiry/CurrentSchemas, VisibilityInquiry, IndexProperty,
CommentInquiry, Deparse and Identity nodes, their binders and node protocols.
The attributed linkage inventory and provider-local source cursors live there;
engine source admission/layout/scheduling remains here. Finite
CatalogInquiryRuntime/SessionInquiryRuntime operations borrow captured
roles/catalog/comments/paths through *EvalContext, with locks and authorization
recapture at the engine owner. They expose typed values/IDs/result descriptors,
not CatalogRead or engine structures. Actual address resolution, index-definition
interpretation, lazy routine admission, deparse engines and serial ownership
lookup remain engine-owned. No new catalog projection or permission policy is
introduced; Item7 final verification is pending.


## 5. Staged DDL and deletion

```go
type DependencyKind byte // n, a, i, P, S, e, x
type DependencyEdge struct {
    Dependent ObjectAddress
    Referenced ObjectAddress
    Kind DependencyKind
}
type DependencyInput struct {
    Dependent ObjectRef
    Referenced ObjectRef
    Kind DependencyKind
}
// DropMode: Restrict or Cascade; future methods on CatalogStage:
Create(d Descriptor, edges []DependencyInput) error
Alter(ref ObjectRef, replacement Descriptor, edges []DependencyInput) error
PlanDrop(targets []ObjectRef, mode DropMode) (DropPlan, error)
ApplyDrop(plan DropPlan) error
```

Edges point dependent → referenced, with both indexes; deduplicate identical edges, retain different kinds. Whole-object deletion includes subobjects. Persistent edges describe definitions, never prepared caches. Pin protection is descriptor policy; omitted edges to pinned objects are intentional. Alter replaces all outgoing edges for its changed object/subobject scope without changing identity. Reject dangling/invalid/cross-owner endpoints, uniqueness collisions and unsupported changes centrally. Stage associated index/sequence/constraint changes from feature-owned refs, not matching names. Bound deparse uses current descriptor names but preserves declared view labels; prepared SQL is not rewritten.

Raw DependencyEdge/index addresses remain compact and engine-local. Stage input retains complete endpoint refs: validate owner, stage/base epoch, existence and subobjects in one coherent stage view, including endpoints created in that batch, **then** translate to stored addresses. Reject foreign/ownerless endpoints without adding edges or emitting events; never remint a foreign address as local. Namespace/signature/feature binding refs follow the same validation boundary before becoming local descriptor keys. Raw addresses returned by CatalogRead are local to that view, not portable credentials; local graph traversal and deliberate complete replacement may copy validated raw graph data.

| Transition | Private stage → one publication | Failure |
|---|---|---|
| CREATE | Resolve schema, allocate, build descriptor/storage/edges, validate → new identity/indexes/metadata/storage | No committed fragment/event; allocator gap allowed |
| ALTER/rename/move | Fetch ref/revision, replace descriptor/keys/edges and associated changes → same identity/new revision | Original names/location/rows/edges/revisions intact |
| DROP | Resolve all targets, compute closure/permissions/reasons, prepare removals → entire closure/storage/incident edges gone | RESTRICT/ownership/cancellation leaves graph/data intact |

Use fixed-point worklist/visited address-and-reason sets, with whole objects subsuming subobjects, cycles/shared descendants and complete preflight. Keep explicit, automatic, normal-cascade and reverse-promotion reasons. A NORMAL deletion requiring CASCADE blocks RESTRICT unless another explicit/automatic reason permits inclusion. Hypothetical CASCADE closure membership and reverse INTERNAL promotion alone are not automatic permission. DropPlan is stage/precondition-bound, never replayed against changed authority without validation.

| Edge | Referenced object removed | Direct dependent removal / boundary |
|---|---|---|
| NORMAL n | Requires CASCADE unless otherwise permitted by explicit/automatic closure | Independent removal allowed |
| AUTO a | Automatic even under RESTRICT | Allowed; never promote containing owner |
| INTERNAL i | Owner removal includes implementation; dependency-driven implementation removal promotes owner | Direct implementation-only removal rejected with owner-directed diagnostic |
| Partition P/S | Any parent removal includes dependent | At least one partition parent must be removed; no unrelated-parent promotion. P only chooses preferred diagnostic parent. |
| EXTENSION e | Member follows extension-owner semantics | Ordinary direct member-only removal rejected; preserve membership/serialization distinction |
| AutoExtension x | Automatic on extension removal | Independent removal allowed; not extension membership |

Edge semantics combine: any valid automatic reason can allow RESTRICT inclusion, but **all** ownership/partition co-drop restrictions remain. Extension scripts/internal cleanup have feature-owned lifecycle exceptions, not generic permission bypasses. Pin protection applies even to CASCADE. Unsatisfied normal reasons yield 2BP01 before mutation. FK AUTO containment in orders plus NORMAL reference to products means products CASCADE removes FK, **not orders**. A view's INTERNAL ON SELECT rule promotes deletion to the view, recursively removing dependent views; never generic backward containment traversal.

String/deferred routines record externally bound signature/property edges only; optional creation validation adds no durable body-table edges. Never scan strings/dynamic SQL to invent dependencies. SQL-standard BEGIN ATOMIC records addresses actually bound at definition; routine owners supply execution/revalidation.

## 6. Prepared validity and reanalysis

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
// Engine methods reuse the existing binder:
Prepare(ctx context.Context, view CatalogRead, rc ResolutionContext,
    source string, ast parser.Statement) (*BoundPlan, error)
ExecutePrepared(ctx context.Context, view CatalogRead, rc ResolutionContext,
    plan *BoundPlan, args []driver.NamedValue) (Result, error)
```

Session owns original SQL/parameters/initial result contract and replaceable binding. Validity checks engine, epoch, ref existence, definition/statistics and relevant environment. Path changes/referenced DDL/statistics trigger lazy analysis; earlier-path same-name creation alone does **not** (PREPARE Notes). Unrelated catalog/row inserts do not invalidate definitions. Relevant namespace removal/replacement invalidates users. Conservative relevant role/access tracking is engine policy; PostgreSQL role/row-security reanalysis is conditional on row-security dependence.

Foreign plan/ref ownership is an error, not a reanalysis/adoption trigger. Local epoch invalidation may trigger source reanalysis, which mints a complete current owner-bound binding through the shared resolver.

State: Unbound → Valid → NeedsReanalysis → Valid or RebindError; Close prevents reuse. Analyze unchanged original text with one current coherent view/context. Install only a complete successful binding; failure never executes stale pointers or mutates the public contract. Later use can reanalyze again, not automatically retry a statement. Public database/sql and SQL PREPARE retain parameter count/types and fixed result count/labels/type identity/typmod/collation when supported: incompatible shape gives **0A000**, `cached plan must not change result type`. Do not re-infer fixed parameters/NumInput. Direct statements analyze fresh; returned cursor metadata stays immutable. Prepared validity is ephemeral, never pg_depend and never a DROP RESTRICT blocker. Stored views follow identity; reanalyzed prepared text may fail or bind a new shadow.

Inquiry nodes never retain a live Session/StatementScope or cache executable
authority. A new execution borrows fresh captured identity/path/catalog authority;
held prepared invalidation and native replacement fences remain engine policy.
Current-schema execution caches, schemaInquiryPathLocked, pending TEMP
staging/publication and FK/rule/view expression-authority overrides stay at their
existing owners. Node-local capture/render/replay/retag recipes preserve stored
literal pairing, logical identities and snapshot-demand manifests; deparse's
stored catalog demand remains unconditional despite bare-NULL analysis demand
suppression. Keyword identity retains parser.IdentityExpr/CaptureIdentity,
distinct from ordinary function lookup/capture. This ownership cutover does not
alter the prepared/public metadata contract above.


## 7. Publication and native replacement

```go
type PublicationToken struct { Epoch CatalogEpoch }
type ChangeSet struct {
    Definitions []ObjectAddress
    Statistics []ObjectAddress
    Namespaces []ObjectAddress
    Replacement bool
}
// Engine-owned opaque CatalogStage / PreparedCatalogPublication:
BeginCatalogStage(base CatalogRead) *CatalogStage
ValidateCatalogStage(ctx context.Context, stage *CatalogStage) (*PreparedCatalogPublication, error)
PublishCatalog(prepared *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error)
DiscardCatalogStage(stage *CatalogStage)
ReplaceCatalog(ctx context.Context, source *Engine, replacement CatalogRead) (ChangeSet, error)
```

Stage is owner-private overlay, not another authority. Record observed descriptor revisions, uniqueness keys, graph neighborhoods and transaction-supplied storage preconditions, including dependents inserted after DROP preflight. TX selects base visibility, retention, savepoints/rollback and timing; LOCKING schedules. No MVCC/isolation/retry algorithm is specified.

Open → Validated → Published, or Open/Validated → Discarded. Validation freezes/preallocates the batch and calls pure providers outside final publication. Further mutation requires revalidation. Publish is single-use; Discard idempotent; invalid lifecycle is ordinary internal error. Under publication boundary recheck epoch/touched preconditions, then co-install rows/descriptors/keys/edges/indexes and invalidation: no I/O/callback/fallible partial mutation after final preflight. Definition changes include deletions, statistics only actual statistics, namespaces relevant changes, deduplicated. Private discard/failure emits no committed ChangeSet; validity checks cannot miss dropped refs if event delivery is deferred.

Touched conflict returns internal CatalogConflict before mutation, mapped by TX, never invented SQLSTATE/retry. Unrelated changes need not conflict solely due to global generation. Reset/Restore validates complete replacement then advances epoch and emits Replacement; Restore never merges. Isolated replacement identities install in target's new lifetime, not as cross-engine refs. Reset retains initialized supported builtins and clears user objects/data. Old-epoch stage fails ordinary ErrCatalogReplaced, `stuntgres: catalog replaced before publication`; failed/read-failing/canceled Restore preserves data/epoch/stage eligibility. CONTROL-TRANSACTIONS consumes fencing; SESSION cleans abandoned private transactions, not engine catalogs. Native/context/I/O errors remain ordinary Go errors.

Replacement is the deliberate complete-state transfer exception, not foreign-ref authorization: validate source contents in their source view, preserve the target owner, and install only a complete target state at the new epoch. Address-only descriptors/graph indexes need no token rewrite. Future stored definitions containing ObjectRef (including columns) must be rebuilt/reminted to the target owner/new epoch before publication. Source refs continue to authorize only their source Engine, never the target, even if numeric components coincide. Failed replacement preserves owner as well as epoch/state.

Retained reads/cursors preserve old state; new readers see replacement. Dump captures one view then performs I/O outside locks, quotes components separately and uses feature serializers/dependency ordering; unsupported kinds cannot silently disappear from a complete dump.

## 8. Clean cutover and bounded owners

No caller/test changes in this part. Current CAT-CORE ObjectName/descriptor/storage/create/replacement subset is retained, not a pending name-map migration. Extend `catalog.go` and `engine.go` describe/Execute for graph/session/projections; migrate `conn.go` PrepareContext/execute and **both** stmt.go methods together. Migrate direct Engine Describe/Execute test helpers without weaker assertions; preserve Driver/native controls/snapshot lifetimes. Remove obsolete prepared rebind-every-use path, no aliases/shims or second binder.

| Backlog owner | Bounded extension | Witness |
|---|---|---|
| PG18-CAT-CORE | Preserve baseline; deferred owner-provenance correction before DDL/session/temp/prepared consumers | W1, W11 |
| PG18-DDL-SCHEMA / PG18-DDL-SEARCH-PATH | Schema DDL and coherent creation/reference/session/access context | W2–W3 |
| PG18-DDL-TABLE-CORE | Public targets, closure/preflight/removals | W5–W6 |
| PG18-DDL-ALTER-RENAME / PG18-DDL-ALTER-SCHEMA | Stable relation/column identities and associated staged moves | W4 |
| PG18-DDL-VIEW / PG18-DDL-FOREIGN-KEY | Real definitions/edges/serializers; INTERNAL view vs AUTO FK containment | W4–W6 |
| PG18-ROUTINE-DROP / PG18-ROUTINE-SQL-BODIES | Signature-specific drops; external/eager/deferred edges and execution | W7 |
| PG18-DRIVER-PREPARED / PG18-DRIVER-PREPARED-REBIND | Retained source/lifecycle/parameters/result and selective binder validity | W8–W9 |
| PG18-CAT-PG-CLASS / PG18-CAT-PG-ATTRIBUTE / PG18-CAT-PG-NAMESPACE / PG18-CAT-PG-DEPEND | Each owns exact same-view supported projections, not a store | W1, W4, W6, W10 |
| PG18-DRIVER-ERROR-FIELDS | Preserve full transport/reference machinery; feature-specific diagnostics; remove inappropriate unconditional public annotation | W3, W5, W8 |
| PG18-TX-CORE | Initial assigned INSERT/SELECT consumes hooks; later DDL owners compose them | W10 |
| PG18-CONTROL-TRANSACTIONS / PG18-CONTROL-SCRIPTS | Epoch fencing; serializers/replacement, no lock across I/O | W11 |

SESSION/TX-VISIBILITY/AUTHORIZATION/LOCKING compose their own contracts. Partition/extension edge kinds stay dormant until their actual feature owners create objects; enum registration does not implement SQL.

## 9. Independently expected acceptance

These are **future predetermined unit/public/reference outcomes**, not observed SQL. Assert each backend against independent expectations before parity. Use held physical sql.Conn/sql.Stmt for interleavings; per-step prepare/close is insufficient. Compare bags/multiplicities without ORDER BY, generated IDs relationally. Diagnostic fixtures independently record severity, five-character SQLSTATE, primary, DETAIL/HINT and conditional Schema/Table/Column/Datatype/Constraint/Position. Position is one-based original-source characters, not bytes; internal position needs internal query. Do not infer optional fields from prose or copy PostgreSQL C locations. Owning features capture unspecified fields/positions/wording from pinned 18.0 before final fixtures.

| Witness | Expected positive/error/interleaving outcomes |
|---|---|
| **W1 identity/normalization** | CREATE public.t(id integer), INSERT 1 affects 1; qualified/unqualified SELECT both return [(1)], id/INT4 and one ref. Separate Drivers/exact DSNs isolate; reconnect same engine retains data. FOO and quoted "foo" collide (42P07); "Foo" differs; dotted quoted schema is one component; 63-byte truncation collisions reject normally. Rename preserves ref, drop/recreate differs; unit exhaustion/fixed-ID/deleted-slot boundaries never depend on another allocator sequence. |
| **W2 schema shadowing** | a.t(v)=11/b.t(v)=22; a,b returns 11, b,a returns 22, a.t always 11 with v/INT4. CREATE under b,a targets b even when a has same name; missing,a ignores missing. Held sessions have independent paths. Units cover implicit/explicit pg_catalog/temp placement, no temp routine/operator candidates and deduplication. Quoted/unquoted decoded `$user` path expands the current usable user schema; qualified `"$user".t` remains literal. Quoted/unquoted decoded pg_temp aliases the same session temp in path/qualification. With path missing,pg_temp,public and no temp, reference lookup leaves temp absent; CREATE initializes through its owner and places the object in temp, not public. An earlier usable ordinary schema wins; sessions never share temp refs. |
| **W3 resolution failures** | No usable/pending creation destination or missing explicit ordinary creation schema: 3F000 (expected primaries `no schema has been selected to create in`, `schema "missing" does not exist`, release-source expectations to confirm at implementation). Qualified missing pg_temp initializes through its owner rather than ordinary missing-schema failure. Missing relation 42P01, column 42703, duplicate relation 42P07, access/CREATE denial 42501, different database 0A000. Ignore inaccessible path entry, but chosen-schema CREATE denial cannot fall through. All failures/cancellation preserve committed state/events; context error stays ordinary. |
| **W4 rename/move** | View v binds a.t(id)=1; relation/column rename and move to b preserve refs/edges/view declared output/data. New names work; old relation/column text fails 42P01/42703. Collision preserves names/location/rows/edges/revisions. Readers see complete old/new immutable view, TX choosing visibility. Prepared original SELECT id FROM a.t reanalysis may fail; SQL is not rewritten. |
| **W5 RESTRICT** | Products referenced by orders_product_no_fkey on orders: DROP products RESTRICT gives ERROR/2BP01, primary `cannot drop table products because other objects depend on it`, DETAIL `constraint orders_product_no_fkey on table orders depends on table products`, HINT `Use DROP ... CASCADE to drop the dependent objects too.` Documented §5.15 strings; products/orders/FK/rows/indexes/edges intact. Optional fields need oracle fixture; multiple blockers have no invented portable order. |
| **W6 CASCADE/ownership** | Products CASCADE removes products/FK, preserves orders ref/rows. v→products/w→v both disappear, unrelated schema names survive. DROP products,orders without CASCADE succeeds with blockers within explicit/automatic closure. Unit exact deletion set/no dangling edges, cycles/shared descendants, multi-edge union, AUTO/x independent deletion, INTERNAL owner promotion without automatic permission, direct INTERNAL/e rejection, P/S co-parent requirement/no unrelated promotion and pinned protection. Synthetic graphs do not claim SQL support. |
| **W7 routines** | String SQL routine with user argument type/body table retains type/property edges, no body-table edge. Table RESTRICT may succeed with routine surviving; invocation fails 42P01 while missing, same-name recreation may restore it. Argument type remains dependency-aware. BEGIN ATOMIC records table edge: table RESTRICT 2BP01, CASCADE removes routine. check_function_bodies=off defers string validation without guessed edges; units assert exact edge sets/no scans. |
| **W8 prepared triggers/fixed result** | Held SELECT v FROM t under a,b returns 11; path b,a next returns 22, v/INT4. Relevant DDL/statistics/environment forces analysis; invalid original text 42P01/42703, no stale refs after drop/recreate. Retain parameters and compatible output; SELECT * then ADD column gives 0A000/`cached plan must not change result type`. Unit count/name/type/typmod/collation checks, failure installs nothing, old cursor metadata intact. Prepared alone never blocks DROP. |
| **W9 prepared nontrigger** | Path a,b only b.t=22: held statement returns 22; create a.t=11 without changing path/b.t: still 22, fresh direct SELECT 11. Rename b.t or change path triggers unchanged source to resolve a.t=11. Reject global invalidation, negative-name dependencies and binding every prepared use. |
| **W10 publication** | Private stage reads coherent; discard/rollback/canceled validation emits no committed fragment/event. Successful batch is completely old/new, validity/read capture one view. Same absent key or same observed revision stages: first publishes, second CatalogConflict before mutation; unrelated keys may publish. New NORMAL dependent after DROP preflight changes reverse-edge precondition and must prevent dangling/remissed RESTRICT. Single-use publication enforced, no prescribed retry/isolation level. |
| **W11 replacement** | Ordered cursor [(1),(2)] retains exactly those rows/metadata after INSERT 3 and Restore [(99)] or Reset; new readers see replacement/absence. Old-epoch stage ErrCatalogReplaced cannot resurrect data; prepared reanalyzes, old cursor survives. Corrupt/duplicate/dependency-invalid/read-failing/canceled Restore retains sentinel/epoch/eligible stage. Ordinary native/I/O/context errors; Dump never holds target lock over writer. Round trip logical data/metadata/edges, not generated OIDs; Reset retains supported builtins. |

**W1 internal provenance unit witness (not PostgreSQL parity):** create independent A/B Engines, each with first user relation `public.t(id integer)`; insert 11 into A and 22 into B. Resolve refA/refB with each own valid read; assert nonzero equal addresses and equal epochs, without pinning an OID, but different complete refs/owners. B.ValidateObjectRef(readB, refA) and the reverse must reject foreign refs, not missing objects or ErrCatalogReplaced; each own ref succeeds. Rejections preserve state/read identity, epoch, descriptors/revisions, rows and committed events; SELECT still returns [(11)]/[(22)]. Reject nil-owner refs and foreign reads. A B stage rejects either foreign dependency endpoint before translation, accepts local/newly staged endpoints and valid local SubIDs, and rejects invalid SubIDs, without rejected-edge fragments/events.

**W1 public multi-instance witness:** one Driver, opaque DSNs `instance-a`/`instance-b`, two simultaneously open pools per DSN and at least two held physical connections per instance. CREATE/INSERT t(11) through A1; A2 and the second A pool read [(11)], id/INT4. Before B creates t, SELECT there fails 42P01; B creates its own t(22), visible across B connections/pools, while A still returns [(11)]. Hold an A cursor, then native Reset/Restore B: only B's new readers change; A's rows and held cursor metadata remain intact. Closing/reopening a pool with the same Driver/DSN retains committed instance data. A second Driver using `instance-a` starts independently. This complements, not proves, the internal provenance unit witness.

**W11 owner witness:** successful target replacement from a populated source preserves target token, advances target epoch once and yields newly resolved target-owner refs; old target refs give ErrCatalogReplaced, source refs give foreign-reference error and still validate in source. Reset preserves target token too. Failed/canceled Restore preserves owner/epoch/ref/stage eligibility. Future embedded stored-definition refs are all target-owner/new-epoch after transfer; retained old cursors remain immutable.

Acceptance mapping: identity/normalization §§2–3→W1/W4/W11; namespace/DDL/reference §3→W2/W3/W9; authority/population §4→W1/W4/W6/W10; transitions/dependencies §5→W4–W7/W10; invalidation §6→W8/W9/W11; publication without MVCC §7→W10/W11; clean-cutover/backlog §8; diagnostics/discovery limits §§5/9. Feature-smoke commands: **none**—no catalog feature is implemented by this revision, and baseline smoke cannot prove proposed behavior. Root runs full gates and independent design review separately.

## 10. Revision history

Prior rationale remains: one identity authority, non-reused engine IDs, immutable descriptors, selective prepared validity and atomic publication, with no alternate store/binder/MVCC. Compatible revision supersedes obsolete name-map/string-AST/SQLSTATE-only seam descriptions and frozen-plan/prewalk/completion assertions with current implementation boundaries, release citations and reason-sensitive multi-edge deletion. Compaction preserves W1–W11 and backlog owners. Historical special-name conflict: prior §3 lines 77/98–99 and plan Approach 2 required quoted specials to remain literal and missing pg_temp creation to fall through. On 2026-10-04 the user explicitly selected **“Use PostgreSQL 18 rules”** for that exact conflict; §3 now supersedes only those rules with decoded-spelling specials and owner-mediated pending temporary creation. Qualified `$user` stays literal; reference lookup stays nonallocating. All other scope/acceptance and identity/dependency/prepared/publication decisions remain. This resolves design precedence, not the deferred implementation differences.

2026-10-04 M1 compatible remedy: make the existing engine-bound invariant concrete with one opaque nonzero-size owner token per Engine, coherent owner/epoch/address validation and staged ref-to-address dependency conversion. Preserve SQL-visible OIDs and deliberate complete replacement, reminting future embedded refs into target ownership. Current ObjectRef/ValidateObjectRef lacks this provenance; coincident populated-engine acceptance is inferred possible from current control flow, **not executed or fixed in code**. PG18-CAT-CORE deferred remediation precedes dependent consumers (shared todo-conflict.md). User's multiple-instance/multiple-connection requirement is explicit through the existing exact-DSN/Driver attachment seam, not new parsing or per-connection tokens. Prior namespace authorization and unaffected W1–W11 remain unchanged; root needs fresh full verification/smoke/review after these document changes.
