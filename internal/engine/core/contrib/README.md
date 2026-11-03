# internal/engine/core/contrib

The contrib function runtimes: amcheck's B-tree/GIN checks and `verify_heapam`,
pageinspect's BRIN and heap attribute captures, `pgrowlocks`, pgstattuple's
tuple, page and index statistics, tablefunc's `crosstab`, `connectby` and
`normal_rand` validation, dblink's local SQL construction and primary-key metadata, and generic file functions. It is a leaf below
core (ENGINE-SPLIT §3, L14.5); the SQL-independent models are in `index`,
`storage`, `exec`, `functions` and `sqltype`.

## Layering

- Imports only packages below core: `access`, `authorization`, `catalog`,
  `exec`, `extension`, `functions`, `ids`, `index`, `lock`, `plan`, `regobj`,
  `session`, `storage`, `stored`, `visibility`, `parser`, `sqlstate`,
  `sqltype`. It never imports core, a family or the root.
- Core reaches it only from `contribHost`, whose `exec.ContribRuntime`
  methods call the entry functions with the statement host. Contrib keeps no
  state, so `Contrib()` stays a pointer conversion of the statement scope.

## Contract

- `Amcheck`, `VerifyHeapam`, `Pageinspect`, `PageinspectHeap`, `Pgrowlocks`,
  `PgstattupleStats` (pgstatindex, pgstathashindex), `Pgstattuple`, `Pgvisibility`,
  `DblinkBuildSQLDelete`, `DblinkBuildSQLInsert`, `DblinkBuildSQLUpdate`, `DblinkGetPkey`, `TablefuncCrosstab`, `Connectby`, `GenericFileList`, `GenericFileRead` and
  `GenericFileTmpdir` are the bodies of `exec.ContribRuntime`; the
  `Validate…` functions check an installed declaration against current
  authority before argument evaluation.
  `ValidatePgpArmorHeaders` admits the retained pgcrypto source against current
  owner, epoch, sealed declaration, OUT generation and EXECUTE authority before
  evaluating its child, including NULL input. Pure header parsing and private
  cursor advancement stay in `sqltype`/`functions`; no catalog snapshot or
  evaluation context is retained by that cursor.
- `PgstattupleTargetCheckable` is the descriptor-only kind, TEMP, validity and
  access-method dispatch of the pgstattuple inquiries.

## Host and authority

- `Host[A]` is the statement a call runs in; core implements it on
  `statementHost` (the shared leaf method set). It gives the operation, the
  I/O environments, the Engine.Mu and owner pairs, the routine, write and
  callable authorizations, the catalog domain, epoch and private catalog and
  storage reads, sequence generators, the statement's own private writes, the
  data view of a relation, the lock manager and ticket, the temporary
  relation contribution wait, the TEMP footprint, the `exec.NestedSQL`
  capability, and these core-owned steps: the pgrowlocks and `verify_heapam`
  readmission fences, pgstattuple's owner cohort and its captures, amcheck's
  maintenance-owner checks (`CheckBtreeAsOwner`, `CheckGinAsOwner`), the generic file authority
  and the catalog inquiry snapshot.
- `Authority` is that authorization, held by value: `Access` for the catalog,
  roles and ACL view, `Check` for privilege requirements, and the
  `access.Environment` of those checks. The package is generic over it, as
  `core/tsearch` is, so capturing it never allocates. Core's
  `AuthorizationView` implements it.

## Locks

- Calls are entered with no lock held. `Host.ReacquireEngine` takes
  `Engine.Mu` and `Host.ReleaseEngineLocked` releases it;
  `Host.ReacquireOwnerEngine` takes the transaction owner's mutex, then
  `Engine.Mu`, and `Host.ReleaseOwnerEngineLocked` releases both. The host
  methods and functions named …Locked run inside those windows.
  Within those windows, callers use the already-locked catalog projection;
  `Engine.CatalogRead` is an unlocked entry point that acquires Engine.Mu itself
  and must not be called recursively.
- `LockHeapStatisticsCohort` takes every live owner's mutex in session-token
  order, then `Engine.Mu`. Lock waits, nested SQL, the maintenance-owner check
  and every model computation run with nothing held; each temporary relation
  contribution is released on every exit.

## Behaviour

### dblink local relations

All three dblink constructors resolve the TEXT relation name at invocation and admit
the exact installed routine plus table-level SELECT authority through
`dblinkRelation`. They capture live logical columns, visibility-driven schema
qualification and output settings; none performs network execution.
The shared name resolver uses `ClassInput.ResolveRelationOpenName` for released
`get_rel_from_relname`/`table_openrv` semantics: an absent explicit schema is
`3F000`, an admitted schema's absent relation is `42P01`, and denied schema
USAGE remains `42501`. Parsing, caller search_path and TEMP selection share the
regclass resolver, but ordinary regclass input (including COPY) keeps `42P01`
for an absent explicit schema. All constructors and `DblinkGetPkey` use this
same invocation-time relation-open boundary.
Post-wait locked admission follows `table_openrv`: indexes (including represented
partitioned indexes) and composite relations fail with their exact `42809`
kind Detail before table SELECT, key/count or array validation. Views, sequences,
foreign tables and partitioned tables acquire no statistics-style kind exclusion.
Scoped AccessShare contributions are released on every exit. Metadata and ACL
capture takes Owner.Mu before Engine.Mu; name resolution, waits and formatting
run outside both, with exact descriptor/epoch and authority readmission after
waiting. `DblinkDeleteSQL` consumes only detached immutable metadata and typed
vector/array payloads, validating the used prefix before target cardinality.
It shares identifier and literal policies with their existing owners.

`DblinkGetPkey` shares that admission and releases its contribution before
returning detached nominal values. It reads only Primary constraint Attributes
in declared order, resolves each stable attribute to its current live column,
and emits INT4 key ordinals with raw TEXT column names. No-PK still requires
SELECT; strict NULL skips relation lookup but not current routine admission.
There is no tuple capture, remote call, Prepare-time name lookup or retained
descriptor/authority. Emission requires a concrete nominal composite, never an
anonymous RECORD or a domain lookalike. Normal source ownership carries the
actual declaration's metadata on empty results.

INSERT and UPDATE share `dblink_source.go`: used-key and flattened-array validation,
the released explicit SELECT with physical dropped-slot NULL placeholders, and
physical-to-live typed tuple access.
Ordinary nested SQL owns conversion/equality/collation, RLS, views, inheritance
and MVCC. There is no LIMIT or raw storage lookup: its consumer drains all rows
and terminal errors, retaining only the first typed row and saturated cardinality.
`ActivateContribRoutineCommand` installs the genuine declaration's lexical
VOLATILE policy; the nested request carries the authentic INSERT or UPDATE caller.
`NestedRequest.FreshCommand` obtains a fresh READ COMMITTED
command view without reinstalling the stable caller's roots. Stable transaction
isolation retains its committed frontier. Cursor cleanup precedes exact outer
policy, View, snapshot, own-write and RLS scratch restoration on every exit.
Positionless source failures stack one actual SPI SELECT context frame;
positioned failures retain internal-query location. Both builders capture formatted
relation spelling before source execution, alongside the source SELECT and its
predicates. After complete source execution and lexical restoration, it reads the
refreshed host output environment for column identifiers and typed tuple values.
Escaped source setting changes remain effective; they do not re-quote the captured
relation name. Formatting includes all live columns and first-occurrence target
replacements with ordinary typed output quoting. UPDATE uses target keys for both
SET replacements and WHERE predicates, preserving every vector occurrence in WHERE;
source keys are used only to select the local row.


### pgstattuple

`pgstatindex_capture.go` authenticates the two pgstatindex declarations and
the pgstathashindex declaration, and
checks ordinary EXECUTE before child evaluation/STRICT NULL and again after
index AccessShare admission. Table ownership or SELECT is not authority. Text
resolves a relation name at invocation; regclass carries supplied OID identity.
Admission releases replacement reads around waits, rejoins them, and fences
catalog domain/epoch, descriptors, parent storage and candidate roots. Capture
borrows the effective root under Owner.Mu then Engine.Mu. B-tree captures also
borrow immutable versions/ends and exact private outputs; hash statistics only
sample bucket lengths, without private projection traversal. Model computation
runs outside mutexes, without callbacks, parent ownership switching or parent
Share locking. Exact selected AM (403 or 405) and ordinary index kind precede
foreign TEMP and validity checks. Every exit releases its supplied-index lock;
final descriptor/storage lifetime and cancellation fences precede results.

`pgstattuple_capture.go` owns exact/approximate tuple and page inquiries. It
uses the same sealed provider, routine/schema ACLs and scoped AccessShare
fences. Record STRICT NULLs still require runtime EXECUTE; scalar constant
NULLs use the shared strict-fold policy. Kind/AM errors use the authorized
descriptor, never a peer's private catalog.

Heap capture borrows actual heads, deletion ends and immutable retained
predecessors, plus each matching owner's statement-published private roots
and detached prepared roots. The caller may additionally include its own
current stage. Peer statements linearize at the existing atomic owner merge:
capture sees a complete pre-merge or post-merge state without peer Admission.
TxRef and VersionID deduplication prevent double counting during PREPARE.
Dirty and Vacuum classification use actual creator/end provenance, including
Vacuum's different own/peer handling of inserted-and-ended versions.
Genuine discarded insertions retain their physical allocation as dead facts
until eligible maintenance; transaction rollback alone does not prune them.

Detached allocation models run after releasing every metadata mutex. Heap
pages pack actual aligned row allocations and item pointers into 8192-byte
blocks; full-scan approximate statistics report 100 percent for nonempty heaps.
No scan prunes roots or consults maintenance counters. VACUUM retires eligible
predecessors through its existing horizon/fence; rewrite and logical import
start new physical incarnations.
Index tuple/page capture uses current logical roots and already-produced private
outputs; no index archive is prepared, stamped, published or transported.
Physical estimates can shrink immediately after logical DELETE, unlike
PostgreSQL's retained on-page items. Heap logical history is unchanged.


### pg_freespacemap

`pgfreespace_capture.go` uses the caller's ordinary data view and CaptureSource,
not the all-owner physical tuple cohort. Scoped AccessShare waits outside locks;
Owner then Engine readmission fences descriptors, parent, store, generator and
provider authority. Sizing and producer iteration run outside both mutexes.
Invocation-local routine carriers use the admitted current descriptor and epoch;
retained source descriptors and their original lifetime proof remain unchanged.
Destination DEFAULT/CHECK demand joins the statement's initial dynamic-root
retention, not its outer routine-authority manifest.
Catalog heaps use their actual producer; genuine indexes borrow shared allocation
roots/private outputs. No target SELECT/ownership gate is added. Relation/kind
errors precede block bounds. Authorized wrapper domains are empty for missing
numeric OIDs and no-storage kinds. Inner scalar EXECUTE is required for non-NULL
input and for NULL when generic SQL-body parameter demand is preserved. The
identity-only validator admits that latter route without opening a relation.
Final identity/authority/cancellation fences precede results;
every path releases local views and only its own lock contribution. No physical
TEMP buffer policy is imported into a function that reads no physical buffers.

### pg_visibility

`pgvisibility_capture.go` validates current sealed identity and ordinary routine
and schema authority before and after scoped AccessShare admission. Descriptor
kind precedes block bounds; target SELECT/ownership is not an inquiry gate.
The shared owner cohort supplies genuine heads, ends, private predecessors and
detached prepared versions together with actual advertised snapshot protection.
Exact tuple-maintenance sampling runs outside owner/Engine mutexes. The prepared
owned fact vector is canonicalized at one boundary, then shared by detached
READ/check/basis consumers before final identity/cancellation readmission.
Raw xmax presence remains distinct from active ownership and effective freezing.
An ended raw header stops requiring maintenance only when its actual owners'
full-XID provenance precedes the captured successful-VACUUM frozen bound.
Unknown provenance and active contributions remain conservative. Ordinary
frozen xmin keeps its original SQL raw XID; no raw-XID value proves all-frozen.

Valid catalog heaps consume their registered immutable catalog/RoleRead producer,
without outer projection pruning or synthesized ownership. The shared checked
layout derives safe VM coverage and independent heap PD proof. Materialized
claims retain exact layout/fact basis; READ horizon changes cannot erase actual
marked claims before checks independently consume running-XID tuple truth.
Foreign-TEMP checks reject even empty heaps; READ restrictions still depend on
represented buffer access. Findings are exact physical VersionIDs, not exceptions.

`pgvisibility_truncate.go` acquires scoped AccessExclusive, readmits current
routine/schema/EXECUTE and target/store, then publishes explicit empty VM outside
statement/savepoint/transaction undo. It changes no tuple/header/MVCC state and
releases its own contribution before transaction end. Explicit invalidation
dominates later captures and DML until successful VACUUM publication. Checks use
scoped AccessShare; final cohort/root/horizon fences reject stale publication.
Manager sampling and checked layout work remain outside every owner/Engine mutex.
Every exit releases only the invocation's own relation contribution.

### pg_surgery

`pgsurgery.go` authenticates the installed freeze declaration and current
routine/schema authority, validates the immutable TID array, and opens the exact
target with a scoped RowExclusive contribution. After waiting, target descriptor,
heap incarnation, current ownership and routine authority are checked again under
the normal ordered owner cohort. Sorted block batches publish immutable headers
for actual versions only; notices are queued and delivered after all metadata
mutexes are released. Completed repair publication survives later cancellation,
statement errors and owner rollback. It does not settle lock tickets or release
another owner's relation, tuple or advisory resources.

Target kind is checked before access-method identity and table ownership, including
for empty TID arrays. Partitioned indexes use their genuine parent descriptor;
a heap-compatible handler under another method OID still fails the exact heap-AM
identity check. Rejection publishes no heap headers.

Forced freezing is unsafe visibility repair: it may expose otherwise invisible
versions and does not validate payloads, constraints or indexes. The runtime owns
no physical page/WAL recovery mechanism and emits no unconditional warning.


### pageinspect

`pageinspect_capture.go` checks genuine immutable released routine identity,
current reciprocal membership and live superuser authority at the function's
NULL boundary. It does not require a live original pageinspect extension or infer
capability from a receiving extension. BRIN item decoding opens the exact
regclass descriptor with a temporary AccessShare contribution, waits outside
metadata locks, and readmits the same descriptor before capturing per-column
stored types. It never reads table rows or fabricates physical pages. The
execution context decodes caller-owned bytea outside metadata locks through
the SQL-independent native readers in `sqltype`. B-tree and FSM byte decoding
need no relation capture; physical relname page acquisition is not an engine provider
overload and local table contents are never synthesized into raw index pages.
Heap schema capture shares the same AccessShare wait/readmission seam. It
projects immutable length/alignment facts in physical slot order, merging live
columns with dropped-attribute history without resolving removed types.
Captures retain no rows, codecs or caller privileges; byte deformation executes
outside metadata locks. Sequence descriptors use their real physical columns.

### crosstab

The crosstab statement host validates the captured procedure against current
authority before argument evaluation, including strict-NULL calls. Nested source
SQL borrows the existing SQL-routine activation, caller catalog/visibility and
effective settings, with an isolated parameter frame. The whole SQL string is
prepared; only admitted non-volatile commands execute, and the last result owns
the pivot input. Each executed command has its own SQL-routine activation, so
expected analysis, relation admission and source authority from an earlier
command cannot authorize a later command. Commands share caller visibility,
catalog frontier and effective settings, not query scratch. Every exit closes
cursors and restores activation scratch. Parsing, typed codecs and source
execution run outside `Engine.Mu`.

### amcheck, connectby

The sole `StockAmcheck` installation retains `verify_heapam` at slot zero and
seven scalar B-tree overloads at slots one through seven. The appended slot
eight is the single STRICT volatile, parallel-unsafe GIN
`gin_index_check(regclass)` member. Shared index capture selects method and owner
dispatch with the expression's single `AmcheckKind`; GIN uses AccessShare on
heap then index, never the parent B-tree check's stronger Share mode. Heap
verification's record-set source and scalar VOID index expressions retain
distinct binding, transport, admission and runtime paths.

`connectby_runtime.go` executes raw hierarchy SQL through ordinary nested
parse/bind/execute activation under the caller's current catalog, retained
snapshot frontier, settings and authority. Runtime-named sources borrow the
outer snapshot's retained roots, not its fixed source-membership set. The first
recursive source/RLS capture installs an invocation-local view; later reads
validate and reuse it. Restoration releases that local view without changing
the outer view, including when the caller is DML. Each sibling query is fully
materialized and closed before restoring outer scratch. An explicit frame stack owns depth-first
traversal; only a completely successful traversal publishes rows. SQL-boundary
errors receive internal-query diagnostics; later descriptor, conversion and
cycle errors remain outside that boundary.
Routine capability comes from the sealed tablefunc installation's released
declaration, never the current extension member count or receiving extension.
Detached, renamed and foreign-member connectby routines remain genuine after
the original extension is retired. Retained typed carriers resolve current
owner/ACL through the ordinary routine validator, preserving domain/epoch,
EXECUTE admission and caller-owned record-shape fences.
Read-only SPI classification rejects utilities before their execution checks;
optimizable statements retain ordinary analysis before the nonvolatile-function
gate. Transaction commands and frontend COPY instead terminate the statement
list with no children, matching SPI's negative return status. Successful
read-only SELECT lists retain last-result semantics.
Setting function calls are ordinary SELECT expressions here too: their canonical
function identities retain live EXECUTE checks and setting effects, while SPI
readonly classification continues to inspect the analyzed SQL command and row
marks rather than prohibiting every volatile expression.
Connectby enables an invocation-private read-only SPI demand. The ordinary
description seam classifies the still-live canonical binding after analysis and
authority checks, omitting analysis-only occurrences. It consumes marks at every
derived/scalar/CTE query level and recursively follows stored-source occurrence
marks. Nested row locking is rejected before tuple-lock execution; unused locking
CTEs remain read-only, and ts_rewrite's separate gate is unchanged.
Stored expansion propagates and merges inherited tuple marks through
`stored.ViewLeafRowMark`, with a path-local recursion guard. Diagnostic marker
presence is distinct from locking strength: a retained stored-query boundary
marked by its caller contributes a preceding reference/COPY marker with no
locking strength, yielding the plain `SELECT` tag while still rejecting SPI
execution. Explicit locking, ordering, bounds, WITH and security-barrier metadata
retain these represented boundaries; inherited locking alone on a plain view
does not. Unrelated later strengths do not replace the first marker's tag.
The canonical bound query walk establishes parent-before-child scope order.
Within each level, `RowMark.Order` preserves the first written locking-clause
target order independently of FROM order and later strength upgrades. Orders
restart at query boundaries and are never globally sorted. Frozen-source
ownership comes from the retained local FROM graph, stopping at derived and
projection boundaries; source metadata is neither reordered nor mutated.

`amcheck_*.go` captures exact target/parent and current routine authority, then
acquires heap followed by index outside metadata locks. A non-index target has
no heap identity and is rejected only after locking the target itself. Method,
foreign-TEMP and validity checks follow both locks. Only queued acquisition
drops the replacement reservation; every readmission retains original identities
and checks current EXECUTE without advancing checkability ahead of lock admission.
Each admitted heap/target open records the existing sticky transaction TEMP
footprint before checkability, including numeric identities and rejected foreign
TEMP attempts. Savepoint rollback does not erase it; EXECUTE denial, strict NULL,
and cancellation before that open do not create it. This PREPARE TRANSACTION
accounting is distinct from physical-session `temp_buffers` initialization.
Immutable baseline and private-output captures feed `index.CheckBtree` without
callbacks under metadata locks. Basic checks use AccessShare and parent checks
Share, released at invocation exit while retaining preexisting transaction grants.
A serial operation-local table-owner frame supplies restricted initial lookup and
detached effective settings across nested SQL/PL activation; every exit restores
caller authority and presentation. Sequence, large-object and pgrowlocks callable
admission/readmission honors this restricted owner ahead of enclosing RI/rule
actors, while ordinary writes retain their existing RI/rule authority.

Native logical checks cover order, parent association, demanded heap membership,
root search and visible uniqueness. They do not claim physical page/downlink,
WAL/LSN or private-overlay ordered-tree verification; see
[`index`](../../index/README.md). Installed member identity and arbitrary current ACL
provenance use the shared controls codec. A retained persistent regclass literal
still fails ordinary replacement fencing rather than adopting a restored index.

### verify_heapam

`VerifyHeapam` supplies the distinct heap-verification runtime.
It resolves exact regclass authority, borrows and releases only its added
AccessShare contribution, readmits the same descriptor/storage after waiting,
and scans immutable unfiltered captures outside metadata locks. Sequence
targets use sequence state. A final incarnation fence precedes returning the
owned records; no user constraints, codecs or physical block mapping run.

### Generic files

Generic file runtimes borrow live expression-principal roles and logical path
settings in Engine -> Settings -> Session lock order. Shared lexical validation
performs no host I/O. Read length checks surround path authorization in released
order; stat/read then reject their physical mechanism. Tmpdir uses the retained,
owner-validated catalog inquiry snapshot for typed tablespace existence before
rejecting physical listing. Nodes retain neither authority nor physical state.

## Testing

- Dblink provider, metadata, detached-construction and retained-carrier ownership
  units use `RUN='^Test(DblinkBuildSQLDelete|DblinkInsert|DblinkUpdate|DblinkGetPkey)'`
  in this package, core and its binder enginetests.
- Installed surgery target, publication and provider ownership units use
  `RUN='^TestPgsurgeryFreeze'` in core and its enginetests. Target units invoke
  the installed host against published descriptors, retaining original heap
  roots across kind and method rejection before ownership checks.
- The runtimes' engine tests stay in core until their census owners move them
  (`RUN='^Test(Amcheck|Connectby|GenericFile|Pageinspect|Pgrowlocks|Pgstat|Tablefunc|VerifyHeapam)'`
  over `./internal/engine/core`; core's README lists the per-family
  selectors). The connectby return and source descriptor validators are
  internal units here (`RUN='^TestConnectby(Return|Source)Descriptor'`).
- `connectby_lifecycle_test.go` (core) matches the frames `.Connectby[...]`
  and `.connectbyQuery[...]`; renaming either function changes it.
- Public scenarios are
  `RUN='^TestReference(Amcheck|Pageinspect|Pgrowlocks|Pgstatindex|Tablefunc|Connectby|GenericFile)'`.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/contrib/enginetests`.
