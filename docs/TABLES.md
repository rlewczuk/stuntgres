# Native table and catalog lifecycle

[README](../README.md) · [Compatibility limits](DIALECT.md)

PostgreSQL 18 documentation is the authority for SQL syntax and standard SQL
semantics. This page describes native catalog/storage implementation contracts
and compatibility limits, not a supported-syntax inventory or a claim of complete
PostgreSQL compatibility.

## Tablespace assignment boundary

Named default-space assignment retains existing relation/provider identities and
storage generations. It takes the normal owner-authorized ACCESS EXCLUSIVE grant
and passes ordinary Finish context/authority fences; it does not publish a
replacement descriptor, copy rows, invalidate held consumers or append dump SQL.
Partitioned parents need no dummy store. The assignment is root-only even with
star/without ONLY; any supported companion action keeps its own inheritance
propagation and the single existing atomic graph publication. Ordinary-inheritance
mixed actions remain supported; mixed partition-edge ALTER and partitioned-index
creation retain their explicit prerequisite boundaries.
Root action preparation follows written order: each action's kind check and
TYPE's original-column, target, collation, USING binding and assignment-coercion
checks precede later destination lookup/CREATE/duplicate checks. TYPE admission
and transformation completion are separate: a raw USING/admission error never
stages sequence AS, but an admitted TYPE's completion error still runs the actual
identity sequence-AS prephase before returning. Its private catalog/generator
changes are discarded; only that physical caller's already-discarded cache tail
survives, without changing peer caches or currval/lastval.
The pg_global movement restriction is deferred until execution: before rewritten
rows are evaluated, or after old-row CHECK/NOT NULL scans when no rewrite is
required. Failed movement never publishes staged companion changes.


Bulk same-default-space assignment validates typed roles and both real builtin
endpoints before returning, without catalog selection or locks on individual
relations. It cannot witness native distinct-space selection or physical movement.
Nonshared named pg_global and every bulk pg_global endpoint are invalid. No
placement descriptor field, new provider, user tablespace or physical filesystem
operation is implied. Default identity remains pg_class.reltablespace 0 and NULL
tablespace summaries; canonical Dump/Restore need no new placement statement.


## Foreign metadata and dependencies

Wrappers, servers and exact `(server,mapped-role)` user mappings are immutable
common catalog descriptors; foreign tables retain normal relation/row-type/
attribute/owner/ACL/default/CHECK identity plus server and ordered table/column
options, with no physical storage root. SQL/MED utilities and IMPORT use normal
statement/transaction/savepoint staging and conflict/authority fences.
Existing admitted metadata actions reuse common column/default/type/constraint,
rename, schema and owner machinery. Type changes recook defaults/CHECKs without
fetching or rewriting remote rows. Unsupported local index/UNIQUE/PK/FK,
row-lock, trigger and hierarchy combinations do not create stores or pretend
callbacks succeeded. Selected foreign writes instead use the registered
transactional provider contract, without physical storage. Consult the PostgreSQL 18 SQL command reference
for syntax; this is not a second SQL grammar manual.
Foreign LIKE accepts all ordinary option spellings. As in PostgreSQL, copied
INDEXES, IDENTITY, STORAGE and COMPRESSION properties have no effect on a foreign
destination: no index or identity sequence is created. Supported logical
properties, including defaults, generated expressions and source PK-derived
NOT NULL, still copy. Stored generated values come from the provider; virtual
generation uses the existing local expression and requests only its base inputs.
Constraint rename uses `ALTER TABLE name RENAME CONSTRAINT ...`; the
`ALTER FOREIGN TABLE ... RENAME CONSTRAINT` spelling is a syntax error.

Options preserve declaration order: ADD appends, SET replaces in place, DROP
removes. Repeated operations see their sequential intermediate list; a later
error aborts the complete table/column statement. Validators receive the complete
resulting list and exact object kind, outside mutexes, before fenced publication.
Changing a validator does not revalidate untouched dependent objects.

Normal dependency edges bind server→wrapper, mapping→server, table→server/schema/
types and views→table/columns. CASCADE removes local metadata, never source rows.
A named mapping's shared owner dependency targets its mapped role; PUBLIC has
no principal dependency. DROP OWNED includes that role's mappings. REASSIGN OWNED
explicitly skips mappings: role/server/OID/key/options and the old-role DROP
blocker remain unchanged, even if the recipient already has a mapping. Server
ownership governs mapping management, not a transferable mapping owner.

Wrapper→registered handler/validator dependencies use the actual installed
procedure identities. Retirement observes incoming SQL/MED edges for every
removed wrapper, server and registered procedure in the captured closure,
including providers reached through schema or CASCADE retirement. Direct,
retained-message and explicit-transaction publication check changed committed
edges against the final candidate, so the retiring transaction's own removals
or valid rewires do not create false conflicts. Provider validation remains
unchanged; the targeted fence runs before it and again at final publication.

When another session commits a new surviving dependent after DROP analysis,
native retirement fails before publication with the ordinary error
`stuntgres: catalog publication conflict` (no SQLSTATE), preserving the committed
providers and new dependent. This applies to both RESTRICT and CASCADE: CASCADE
does not silently expand and replay an old closure at COMMIT. The caller decides
whether to retry. It is an optimistic coherence guarantee, not a promise of
PostgreSQL object-lock timing. Public transactional DROP SCHEMA remains outside
the supported utility boundary.

Wrapper/server USAGE and owner changes use existing authorization/GRANT/REVOKE
machinery. Exact effective-user mapping wins; PUBLIC is absent-only fallback,
not an inherited-role lookup or option merge.

Raw `pg_catalog.pg_user_mapping` has four columns: `oid OID`, `umuser OID`,
`umserver OID`, and nullable `umoptions TEXT[]`. The first three catalog
attributes are NOT NULL; options have dimension1 and bootstrap C collation950.
Mapping OIDs identify actual command-owned rows, `umserver` identifies the exact
foreign server, and `umuser` identifies the exact role, with **0 for PUBLIC**.
Options retain ordered `keyword=value` elements, including empty-string values
and escaping; a mapping without options has SQL NULL, not an empty array.

Raw catalog SELECT is denied to PUBLIC by default, even for `WHERE FALSE`.
Native reads use the ordinary captured relation ACL, not a special superuser-only
gate. Native SQL GRANT/REVOKE on catalog sources remains unsupported: it reports
`0A000`, `statement is not supported`, without changing the ACL or permitting
previously denied reads. PostgreSQL permits an explicit SELECT grant to expose raw
options; that oracle behavior does not expand native catalog-grant admission.
The separate public `pg_user_mappings` view exposes identities but redacts
options using current execution authority, including held prepared
calls: superusers see all options; an exact mapped current user needs server
USAGE or ownership; a server owner sees PUBLIC options, not another named user's
secrets. Membership in a mapped role alone does not disclose that role's options.
Catalog inspection neither invokes a provider nor establishes a connection.
Mapping commands still own row creation, alteration and removal.

The five SQL/MED catalogs and pg_attribute.attfdwoptions expose actual descriptors;
existing pg_get_acl reads stored wrapper/server ACLs. Merely having static
PostgreSQL has_server_privilege/has_foreign_data_wrapper_privilege identities
does not make those otherwise unsupported SQL inquiry functions executable.

Dump rejects user SQL/MED metadata before bytes/callbacks. Reset and ordinary
Restore remove it while preserving installed Driver callables and finalized
results. Foreign replay is explicitly rejected atomically. Auto-installed custom
handler namespaces preserve ordinary table/owner/comment dumps into configured
and unconfigured targets; renamed or retired configured identities fail preflight,
as do the existing unsupported explicit namespace ACLs. Executable code and
mapping secrets are never serialized. Live SQL cursor resources follow deferred
owner cleanup after replacement, not synchronous cross-session cancellation;
see [the callback contract](DRIVER.md#foreign-provider-registration).

## Atomic catalog and storage publication

Table utilities stage immutable definitions, storage and dependency closures
privately and publish through one fenced frontier. Failed binding, validation,
required cleanup, cancellation or final identity/authorization checks preserve the
original definitions and rows. Preparation is effect-free; utility preparation can
be metadata-only rather than semantic execution. Explicit-transaction permanent
creation/removal and selected standalone utilities retain native exclusions.
Read-only utility rejection precedes lookup, authorization and lock acquisition.

Relation identities, sparse stable attributes and dense evaluation/result slots
are distinct. Column compaction does not renumber surviving catalog identities;
new attributes never reuse retired identities in the same relation. Renames change
labels/revisions without rewriting rows or changing storage, ACL/comment addresses,
row-codec identity or stable dependency links. A compatible descriptor revision is
not replacement. Waiters retain exact original relation authority and refresh
metadata after grant; removal/recreation and Reset/Restore fence the old address
rather than retargeting by name or numeric OID.

`pg_attribute` projects these stable relation-owned identities and current
declaration/constraint facts. DROP installs immutable scalar-only attribute
history before removing live columns: the original type dependency can disappear,
but released preserved facts and the retired number remain visible. History is
not a live query column, ACL demand, constraint or row-codec member. Logical
Dump/Restore preserves active definitions and rank, not physical dropped history.
Attached partitions with active rank use ordinary CREATE plus ATTACH. Nonzero
inherited/merged ranks use a final genuine catalog-SQL repair block in the dump,
interpreted only by the isolated native Restore staging path. Public catalog
sources remain readonly; replay on PostgreSQL requires superuser catalog-update
authority. Relationships, locality, defaults, owners, ACLs and row data are not
changed by the rank repair.

Held statements rebind current metadata and enforce their cached result signatures.
Retained eager Rows keep old values, labels and schema across utility changes,
rollback and Reset/Restore. Zero-column relations and removal of the last column
retain physical row multiplicity without a dummy column.

Grouped removal preflights every explicit target before publication and deduplicates
actual addresses. Dependencies use real supported edges, not similar names or
prepared-handle history. Held rows and independent copied tables are not persistent
dependents. Catalog replacement reowns concrete descriptor/provider generations;
numeric inequality is not a replacement proof, since allocators can reuse numbers.

## Constraint staging, validation and ownership

Constraints own distinct stable definitions; index-backed constraints have real
reciprocal index ownership and enforcing roots. Index attachments are staged first;
physical builds follow index declaration order before historical CHECK scans.
Historical CHECK jobs follow declaration order, while ordinary DML enforces NOT
NULL first and CHECKs in alphabetical constraint-name order. Final rows and all
required constraints validate before publication.

Selected action lists retire old backing generations before naming indexed
replacements. Vacated names can be reused without stale suffixes; replacement gets
new definition/index identities. Each indexed removal classifies its own dependency
policy before the next retirement, so one later cascading action cannot authorize
an earlier independent removal. Late failure restores the entire old closure.

Invalid CHECK/NOT NULL/FK attachments skip only historical validation, not definition,
provider, type, key or authority checks; genuine subsequent writes enforce them.
Invalid NOT NULL still advertises enforced nonnull metadata despite historical NULLs.
Compatible same-attribute attachment preserves identity/name/validity. Promoting
validity requires its actual validation route. Fresh primary-key admission reads
original NOT NULL validity before same-statement transformations: a separate
successful repair/validation is required. Savepoints restore flags, shapes, rows,
roots, event state and lock contributions together.

Type changes bind original transformation expressions against statement-original
rows, then reconstruct dependent definitions against the completed destination
shape. Defaults coerce independently. Each transformed row passes final physical-
column NOT NULL checks and its affected CHECK queue before the next row is converted;
multicolumn CHECKs enter once. Grant-time live scans include prior own changes even
under Repeatable Read, without changing ordinary retained query snapshots. Failed
conversion leaves old identities, defaults, constraints, rows and roots intact.
Pure nullability changes do not rewrite values.

Primary and unique constraints reuse typed comparison and transaction key claims,
not rendered text. Real backing indexes cannot be directly removed even by cascading
removal. Rename retains reciprocal identity; affected type reconstruction can remint
constraints/backing definitions while preserving unaffected groups. Removing a key
can retire its backing group without removing surviving key NOT NULL definitions.
Current SELECT authority controls key disclosure, including for owners with revoked
SELECT; ownership is not a disclosure bypass. Native deferrable UNIQUE/PRIMARY KEY
and temporal-key facilities remain compatibility limits.

### Inheritance catalog projection

`pg_catalog.pg_inherits` is a four-column immutable projection, not a second
hierarchy owner. Each ordinary `Inheritance.Parents[i]` supplies the child's and
parent's `pg_class` OIDs with ordinal `i+1`. Table partition membership and
`SQLIndex.PartitionParent` each supply one immediate edge with ordinal one.
The indexed-table owner (`SQLIndex.Parent`) and reverse partition child list
do not supply extra rows. Actual index links are included even while the
partitioned index remains invalid/incomplete.

Captures use the caller's coherent catalog generation, including its private
transaction overlay. Existing CREATE/ATTACH/DETACH/DROP publication, rollback and
savepoint owners therefore determine metadata without separate row maintenance.
Renames and schema moves preserve endpoints; same-name recreation does not reuse
the old relation identity. Detaching a subtree severs immediate root links while
retaining descendant links. Logical Dump contains hierarchy/index DDL, not
catalog INSERTs; Restore reconstructs edges using the restored identities.
Already returned eager rows retain their old values across publication and
native Reset/Restore, including failed atomic replacement.

All four fields are nonnullable: OID, OID, INT4 and BOOL. Ordinary parent order
is meaningful; an unordered query otherwise returns a bag. There is no row OID,
transitive closure, extra endpoint privilege filter, or physical catalog-index
descriptor. `inhdetachpending` remains false because concurrent partition detach
is not implemented. Native writes and row locks on this catalog are unsupported.

### Constraint catalog projection

`pg_catalog.pg_constraint` has the released PostgreSQL 18 28-column order and
types. The first twenty columns are nonnullable; the eight vector/tree columns
are nullable by applicability. Captures enumerate actual relation-owned n/c/u/p/f/x
definitions from the caller's coherent catalog generation, including its private
transaction overlay. There is no independent constraint cache or undo registry.
Retained eager rows own immutable vectors and trees across rename, rollback,
drop and native Reset/Restore. Fresh or held reexecution observes current metadata.

Constraint OIDs are distinct from table and backing-index identities. Namespace
follows the owning relation. Standalone indexes produce no synthetic rows.
Key vectors preserve declaration order and stable sparse attributes; INCLUDE
payloads are omitted, CHECK variables are deduplicated in first-encounter order,
and exclusion expression keys use zero. Absent vectors are typed SQL NULL, not
empty arrays; populated vectors use one dimension with lower bound one.
Foreign-key metadata uses the actual referenced supporting index and its selected
comparison family, with pf/pp/ff operators in declared pair order, including
cross-width and normalized domain/varchar/enum inputs. Action/match codes and
explicit delete subsets retain their declarations.
Exclusion operator signatures are captured from the admitted index class input,
not guessed from the declared column type: CIDR uses INET, admitted OID aliases
use OID, VARCHAR uses TEXT, and enums use polymorphic anyenum. Domains retain
their base class's canonical identity. This metadata does not retag stored
values or change their existing equality, class admission, or diagnostics.

Validity reflects actual historical-validation state; admitted definitions remain
enforced. Deferral flags describe the exclusion declaration, not current
SET CONSTRAINTS mode. Inheritance and partition flags follow actual provenance;
nonindexed inherited constraints need not have a parent constraint OID.
Metadata does not add domain constraints, constraint triggers, temporal keys,
NOT ENFORCED declarations, or unsupported deferrable UNIQUE/PRIMARY KEY owners.
CHECK conbin is a typed captured native structural tree, not a replica of
PostgreSQL's internal nodeToString representation; use semantic deparse functions.


### Foreign-key final-image ownership

FK definitions retain stable child/parent attributes and the actual supporting-index
identity; they create no separate child index. Comparison belongs to that index's
owned family and input authority. Committed parent probes borrow its ordered root
for lower-bound/equal-run lookup; own/current-command overlays and deleted/shadowed
identities preserve the final image. There is no independent FK value cache. After
a parent wait, the exact physical-version locator is revalidated.

Immediate checks and referential actions share the canonical private write stage
and locks. Chains, diamonds, cycles and self references retain real row/version
transitions. Destination coercion, generation and local/final integrity checks all
complete before publication. RowsAffected and mutation output describe initiating
rows, not action counts; child defaults are never evaluated by Prepare or Dump.
Stable-isolation action candidates include current and retained matching children;
row grants crosscheck successors against the transaction frontier before action.
Post-frontier changes raise serialization failures without advancing the snapshot
or publishing partial work.

FK column retirement uses the common read-only dependency closure and incumbent
ordered consumer schedule. Inbound dependency removal removes definitions, not
child tables or data merely because their key provider disappears. Ordinary
inheritance does not propagate FK definitions. Definition-time REFERENCES rights
are separate from later DML privileges and current violation-disclosure authority.
Deferred, temporal-period and partition-hierarchy FKs remain native limits.

Logical replay emits a captured valid ancestor-to-descendant FK as genuine
qualified `ALTER TABLE ONLY ... ADD CONSTRAINT ... FOREIGN KEY` after both
physical row streams and the referenced table/key provider are ready. It does
not inline a forward reference into the ancestor CREATE, invent NOT VALID, or
place a global data barrier around unrelated definitions. An exact ordered
PRIMARY KEY reference uses PostgreSQL's omitted referenced-column list to retain
that provider; explicit references use the normal constructor's actual provider
selection. Native Restore admits this event only for freshly imported ordinary
ancestor/descendant relations with captured owner, stable key/full-type and
provider observations, then uses the existing pending FK binder and validates
all loaded physical rows before atomic publication. The genuine script uses its
existing trusted superuser reconstruction context without changing table owners
or granting ordinary SQL an ownership/REFERENCES exemption. Required roles must
already exist; role creation and ACL restoration are not added by this event.

### Exclusion events

Exclusion definitions own real nonunique integrity indexes and independent
constraint identities. They do not strengthen key-share/no-key-update eligibility.
Deferral events enlist only actual potential conflicts, not synthetic future-peer
obligations. Savepoints retain immutable event/mode/projection snapshots; repairing
or removing the actual row/version retires its event. Prepared transaction owners
retain the same dirty ticket through completion. Completion resources own waits,
deadlocks and cancellation; reads retain their ordinary MVCC view.

Replacement remaps providers and enforcing roots, but transient modes/events are
not serialized. Feature-local logical GiST INET equality and range overlap also
use these events. General GiST/SP-GiST, extension providers and physical
PostgreSQL RI/index/WAL mechanisms remain outside native capability.

## Default definitions and demand

Stored defaults are structurally immutable bound definitions, not necessarily
immutable-volatility expressions. They are closed and row/parameter-independent;
volatile admission is deliberately bounded. Cooked copying retains original
provider identity and analyzed temporal leaves without evaluation. Definition
identity is distinct from its owner and stable attribute. Replacing a default
creates a fresh identity; renames and surviving compaction preserve it.

Changing a default is metadata-only and affects future demand, not existing rows.
Create and Dump never evaluate dormant runtime errors. Consumed defaults are
preflighted after supplied/source input preflight and before executor authorization
and capture, even when a query source is empty. A destination supplied throughout
the command does not demand its default. One private schedule serves mixed rows,
but runtime expressions run only where consumed. Runtime default evaluation stops
at the first completed failing row; already drawn sequence values are not rewound.

Default-only planning can prune a decisive left Boolean constant and retains the
unfolded stored tree and all original admission checks. It does not create a general
query/UPDATE/DELETE short-circuit promise. Stable operations use the consuming
statement's captured settings, whereas typed temporal leaves retain creation-time
analysis capture. Assignment coercions and presentation remain separate.

Adding a column changes schema/storage atomically. Immutable/Stable defaults are
evaluated once for addition even without old rows; admitted volatile defaults draw
once per effective live row, never deleted/replaced predecessors. Future writes
use the retained expression. Stored view output shapes remain frozen, and native
eager cursors keep their old schema/data.

Simple structural NULL normalization omits ordinary default definitions without
evaluating arbitrary expressions; generated NULL definitions remain stored. Native
normalization currently also prunes domain-coerced NULL defaults where PostgreSQL
retains them. This producer/storage limit is not repaired by catalog projection.

## Identity and sequence links

An identity definition is keyed by stable column attribute and exact sequence
address, with a reciprocal internal-owned column address. Validation requires both
links, integral usage, nonnullability, no ordinary default and matching owner/schema.
Replacement remaps both links, including TEMP collisions, never associating by name.
Identity allocation/session memories are independent of transactional row failure;
private generator restart and metadata instead participate in rollback.

Same-construction sequence dependencies bind in one private catalog with genuine
namespace/provider stamps; incumbent runtime authorization remains live. Failure
installs neither table nor generator. Own-table reciprocal cleanup does not weaken
external view/CHECK dependency protection.

Identity alteration waits for the exact sequence operation lock and refreshes
committed options/state at grant while preserving staged changes and original table/
storage fences. Concurrent options cannot be overwritten by stale capture.
Cancellation leaves the old group usable. Integral type reconstruction preserves
reciprocal links, owner, ACL/comments and custom options, using the sequence owner's
conversion rules even for same-type actions. A private scalar generator serves
transformation/CHECK draws; failed publication restores scalar/catalog/data roots,
not physical-session memories or discarded cache tails. Peer unseen cache tails
are not flushed by a failed private conversion.

Prewait observations cover only the exact internal descriptor captured after grant;
new consumers, ownership changes and replacements still conflict. Ordinary
AUTO-owned sequence/default links are distinct from identity and are not transformed
merely because their column type changes. Independent sequences are not swept by
name during ownership transfer or removal.

## Generated recipe ownership

Stored and virtual generation share immutable analyzed and planned expressions.
Immutability admission checks surviving providers; virtual security/type admission
also checks the unsimplified tree. Selecting a member does not hide invalid unused
children during analysis/preflight. Recipes retain stable definition/attribute and
provider dependencies rather than cached owner OIDs.

Stored generation runs after completed base/default/identity inputs and simultaneous
assignments, before constraints and output. Reading stored cells does not reevaluate
the recipe or require SELECT on its inputs. Virtual storage uses typed physical NULL
placeholders; logical reads and constraints compute demanded values. Absent source
occurrences do not run recipes, while real all-NULL rows remain genuine input.
This presence authority survives joins, windows, bounds, correlation and mutation
sources rather than being inferred from payload cells.

Recipe replacement preserves kind, declared usage, stable attribute and constraints,
allocates a fresh definition identity and keeps only new dependencies. Stored
replacement evaluates each effective live row once, ignoring ended/deleted versions;
mixed transformations use the original row for type conversion and the completed
row for replacement generation. All indexes and incoming/outgoing FKs validate
before publication. Failure preserves recipe, cells and roots; savepoints restore
all together. Dropping a stored recipe removes dependencies without rewriting cells.
Virtual recipe removal and some PostgreSQL 18.0 virtual replacement combinations
retain explicit boundaries.

Preexisting virtual views resolve the logical relation's current captured recipe by
stable attribute and use its complete source tuple, not the old view projection.
Inheritance uses the named parent's recipe and mapped child inputs, with actual child
identity retained for provenance/locking. Rechecks borrow the matching occurrence's
latest complete tuple without live engine/name lookup or view reparse.

Virtual recipes cannot use user-defined types even in discarded branches. Native
user-function definitions remain external; virtual index/key/INCLUDE uses are
unsupported. Generated-column/dependency type changes and generation addition
retain separate limits. Dump stores explicit recipe kind; Restore recomputes stored
values and remaps owner-dependent cells while preserving virtual laziness. Unread
invalid virtual values are not demanded merely by a native roundtrip.

## Namespace catalog projection

`pg_catalog.pg_namespace` has four columns in released PostgreSQL 18 order:
`oid OID`, `nspname NAME`, `nspowner OID`, `nspacl ACLITEM[]`. Only the ACL is
nullable. The source retains relation2615 origins, attributes1–4, NAME's
collation950 and generation-owned types even for empty results.

Rows enumerate actual builtin, user and TEMP namespace descriptors without
filtering by owner, schema USAGE or search_path. Reading visible metadata
does not grant access to contained objects. Normal qualified/path resolution
still governs access to the source itself. `pg_class.relnamespace` and
`pg_roles.oid` identify the same namespace and owner authorities. Rename and
owner transfer preserve namespace identity; drop/recreate allocates a new one.

ACL NULL is distinct from an explicit empty array and from PUBLIC grants.
Stored U/C grant-option bits are projected exactly, not inferred from ownership.
Default-created schema ACLs normalize their actual entries by grantee identity;
initialized builtin ACL order is retained separately. Eager returned rows keep
captured names, ACL role spellings and types across later catalog replacement.
Every named ACL grantor/grantee must resolve in the captured role inventory;
domain-only or retired principal references cannot be projected as valid OIDs.

## Temporary namespace and session lifetime

TEMP belongs to a physical session, not a pooled borrower. Lookup does not allocate
a namespace; peer sessions never share data. Owned generator/default construction
uses the builder's exact namespace token. Commit actions stage empty roots or retire
the dependency closure once at outer completion; rollback/savepoints restore
catalog/action/data roots without rewinding sequence draws.

First use stages actual `pg_temp_N` and reserved `pg_toast_temp_N` descriptors
atomically, with distinct identities, bootstrap owner10 and NULL ACLs; schema
defaults do not apply. TEMP tables/sequences/views retain their creating role.
Only the table member supplies own-session USAGE/CREATE, search-path placement,
the `pg_temp` alias and temporary persistence. New pairs are creator-private
until commit and disappear on first-use rollback; preexisting pairs survive
rollback of newly created objects.
Both view preparation and execution retain the exact staged table-member authority,
so an ordinary creator can publish its first TEMP view without schema delegation.

Physical-session end aborts private work and retires the complete owned closure.
Surviving expression consumers project committed/private rows by stable attributes,
with their index candidates evolved from the latest staged layout. Catalog, surviving
storage and roots publish together. Physical retirement retries obsolete predecessors
instead of replacing newer state. Unrelated durable objects and peer TEMP remain.
Both empty namespace descriptors and names remain after physical close. Their
retired tokens are provenance, not active authority; a new session cannot adopt
them as its own table namespace. Persistent replacement excludes source TEMP
tokens and overlays the destination's exact live or retained empty pair.
Namespace-scoped default-privilege directives retire with the physical closure,
even though both empty namespace rows remain; captured directives remain immutable.
Qualified namespace lookup uses the captured effective actor's superuser status,
then normal schema USAGE. A bootstrap superuser resolving an absent child in a
retained namespace gets42P01; an ordinary peer lacking USAGE gets42501. Live peer
REGCLASS identity resolution is distinct from physical foreign-session scans.
Neither lookup changes own-session creation, persistence or namespace adoption.
Native physical stored-source/target admission separately requires the captured
namespace's exact physical-session token. Foreign TEMP storage is an execution
0A000 boundary, not a schema42501 or synthesized empty cursor. Direct/ONLY and
recursive stored-view scans, DML physical targets, table COPY and explicit TRUNCATE
use the same provenance guard. Metadata-only leaves stay excluded; inherited
foreign TEMP descendants remain skipped under persistent ancestor scans.
Executable view occurrences first use the existing captured owner/invoker source
column and row-mark authority. Nested invoker views retain the original invoker;
authorization is not cached or deduplicated by relation identity. Only after
that complete authorization pass may physical-source admission acquire data views.
Table COPY preserves column ACL, direction-specific read-only/relation-kind checks,
then physical storage admission, before options, defaults or stream I/O.


Dump excludes TEMP. Reset/Restore retains exact live owner descriptors, stores,
generators, roots, commit actions and builtin generations. Contextual TEMP references
retain their original engine, namespace token, nonreused address and epoch; immutable
manifests and both analyzed/execution expression trees remain authoritative. Retention
does not admit stale persistent consumers, foreign owners or retired providers.
Incompatible live user-type dependencies remain explicit errors. Rendering recognizes
the current owner's namespace from captured authority, not name matching.

Trusted native replay reconstructs supported named-owner domains/enums/composites using the existing
isolated destination flag, captured bootstrap authenticator and importer map.
Only schema CREATE denial is bypassed there; ordinary SQL permissions, base-type
access, ownership/ACL reconstruction and publication fences remain unchanged.

Temporary cleanup retains its namespace descriptor and transactional removal state.
Its live-cursor guard applies to actual relations in the complete removal closure;
exhaustion is not Close. It does not silently clear prepared names or persistent
sequence memories and is not a generic DROP/query-lifetime guard. Temporary views
participate in the same real dependency/physical-close closure. Moving objects into
or out of TEMP and broader permanent transactional DDL remain native limits.

## Inheritance, typed attachments and partition graphs

Inheritance stores immutable immediate-parent identities and per-attribute/constraint
origins. Child definitions remap stable attributes and retain actual local/other-parent
origins through diamonds. Physical stores remain independent. Parent scan metadata,
ACL/RLS and virtual recipes belong to the named parent; every physical root shares
one statement snapshot. Other sessions' TEMP descendant branches are excluded before
store selection or locking. Rowmarks use actual descendant storage/row identity.
UPDATE/DELETE consume the deduplicated physical closure unless ONLY excludes it;
INSERT adds rows only to the named physical relation. Named-parent authority,
result columns and diagnostic SELECT disclosure do not confer child table rights.
Physical generation/constraints/indexes use the complete child tuple; parent
assignments scatter by stable named-column layout without losing child-only data.
Physical relation+RowID, captured store/version and real grants survive waits,
FROM/USING, modifying CTEs, CURRENT OF and held prepared execution. Counts include
each selected physical row once; later child failure vetoes the whole statement.

Already-supported ordinary ALTER column/default/inheritable NN/CHECK/generated
actions use one staged graph publication. Local and other-parent contributions
survive recursive/ONLY distinctions; identity, RLS and physical key/index facts
stay with the named relation. PK-required NN is distinct from copying the PK.
Definition preparation uses actual ordered provider results once, before the
sole stage and row rewrite. Default ADD CONSTRAINT may require descendant
preparatory locks even when its compatible merge stops execution. Valid-root
SET NOT NULL stops without child locks/ownership; reached entries check ownership
before merging, but descendants below that stop are not visited. ADD COLUMN's
column merge stops independently of new inline NN/CHECK propagation. A real
descendant added during a lock wait is reconciled and admitted before staging;
replacement or changed defining authority is not treated as additive growth.
Native metadata-only SET/DROP DEFAULT and column/constraint rename likewise
finish descendant AX, current-owner and catalog-lifetime admission before
creating their sole stage. Reconciliation accepts the exact first-subclass
marker publication and actual new descendants, not subsequent defining
replacements. The final authentic private-transaction catalog/authorization
capture and stage creation share one Engine-mutex handoff; waits occur outside
that mutex. No partially prepared stage is rebased or patched to a newer base.
DEFAULT cooking uses each admitted physical column's full Usage and that
captured catalog. Rows/storage and inherited locality/fact origins stay intact.
An inherited NN's first direct SET localizes rather than rescans it. Completing
a local invalid inheritable NN must validate children too: ONLY with children
fails42P16, while childless completion neither changes identity nor invents
NoInherit status.
PRIMARY KEY USING INDEX prepares one unnamed NN per actual KEY (not INCLUDE),
using the same constructor even when a physical not-null flag already exists.
A compatible inherited NN becomes local without changing its OID or inherited
count, and that merge stops before descendant locks/ownership. A fresh NN instead
propagates independently of the root-only physical key; ONLY with children rejects
that new inheritable fact with 42P16. Index eligibility/key usage and ordered
attachment metadata are captured once and consumed by the sole stage. Later
definition bindings see its actual renamed index, not the previous namespace.
Attribute locality and explicit recipe authority are independent. ONLY changes
do not acquire child rights/locks merely to retag retained recipes. Dump derives
actual child default overrides/absence and generated recipe differences, preserving
attislocal/count and real provider/type identities. Provider-dependent defaults
retain their genuine deferred phase and physical ONLY targets.

For divergent generated components, replay declares compatible full-type NULL
recipes, restores every captured physical recipe after required declarations,
then reconstructs captured local CHECK definitions through genuine recursive ADD
before rows. Real merges restore local/diamond/other-parent origins. These are
isolated dependency-stage declarations, not final fallback recipes. Captured
validated ONLY SET NOT NULL NoInherit uses its exact declared name and immediate
paired canonical pg_constraint repair before any child can inherit it. The
importer fences actual imported NN/generation identities and rejects unpaired,
mismatched and primary-backed NN retagging atomically. Genuine PostgreSQL replay
requires superuser catalog-update authority; public native catalogs remain readonly.
Mixed-owner replay uses the existing trusted superuser script context for
captured recursive CHECK reconstruction and, only for affected fresh inherited
declarations, an exact captured OWNER transfer before physical recipes/data.
Roles must already exist. This does not relax public recursive owner checks.

Parent-column DROP can leave a child's NN/CHECK coninhcount unchanged after the
live parent fact disappears. Exact retired contributions retain the past source
column rank, constraint kind/name, fact OID and source lifetime, separately from
the current parent reference and physical own fact. They are not live expression
or type dependencies and are never copied to grandchildren as extra support.
Counts derive from live origins plus these actual retired identities. TYPE
cleanup does not recreate historical-only nonlocal facts as local constraints.
Logical import namespaces archived identities above retained occupancy; it does
not claim that a deleted provider or old rank exists in the new catalog.
Canonical superuser pg_constraint repair carries seven-field VALUES identities
and derives the count, with exact fresh imported own OID/key/recipe/validity,
current parent edge and before-state checks. Final history metadata repair follows
real table/ancestor constraint completion; genuine valid/invalid enforcement
retains its proper declaration/data phase. Public catalog UPDATE remains denied.
Released TYPE cleanup can also leave physical NOT NULL enforcement without a
pg_constraint NN row when a primary key had reused the nonlocal fact. This is
explicit physical provenance from actual cleanup, not an invented local NN
constraint. The physical flag survives PK removal and later TYPE changes;
missing-record DROP NOT NULL retains the released diagnostic. Logical replay
uses genuine constraint construction followed by narrowly paired canonical CTE
cleanup of the exact newly constructed NN row and its selected pg_depend edges,
retaining physical enforcement. Exact fresh relation/attribute/type/constructor
identity and trusted import authority are required; no names are guessed and
ordinary public catalog UPDATE/DELETE remains forbidden.
This PostgreSQL script replay requires superuser catalog-write authority. Its
single-use native import counterpart accepts only the exact fresh constructor,
selected NN OIDs, attribute/full type usage, current provider and captured
authority; malformed, reused, mismatched or retained-object repairs are rejected.
A keyless flag uses genuine childless ONLY SET NOT NULL followed by the no-PK
cleanup. With flag-only a and an unrelated captured PK(b), that repair precedes
the real deferred PK(b); b's NN remains intact. Separate backing shapes reuse
the existing index renderer and genuine PRIMARY KEY USING INDEX, preserving
KEY/INCLUDE/options/opclasses rather than flattening the definition.

Only captured valid self-FKs supported by that deferred primary move after its
construction and cleanup. Ordered references use genuine implicit primary
REFERENCES; permutations retain their exact local/referenced vectors. Actual
same-key competing UNIQUE/index providers are ordered after the PK/FK when needed
to preserve the captured supporting provider. Existing binders/renderers and real
dependency edges remain the owners, including external regclass consumers and
TYPE remint of INCLUDE payloads. Native supporting-index/validity receipts are
strictly paired to fresh logical import; ordinary FK binding, permission checks,
public catalog-write denial and unsupported feature boundaries do not change.


Truncation consumes the same graph, replacing selected physical stores and empty
index baselines without discarding definitions, edges, ownership, ACLs, comments,
generation or security flags. Virtual partition authorities have no phantom store.
Incoming FK closure uses actual definitions even for empty tables. Grant-time and
final graph reconciliation retain the same session-specific TEMP closure. Each
explicit/FK-cascade member retains its own authorization even if expansion already
selected its physical effect. Trigger callbacks remain unsupported. Registered
foreign truncation groups selected tables by server, admits all capabilities
before effects and passes RESTRICT/CASCADE/identity flags to the real provider.
Mixed local/remote completion follows the documented remote-first transaction
contract, not distributed atomicity.
See [atomic truncation lifetime](TRANSACTIONS.md#whole-table-generation-replacement).

Typed tables own independent stable attributes, storage and row codecs, plus one
NORMAL source-composite dependency. Attachment retains exact live ordered shape
and source backing admission; waits cannot adopt changed source generations.
Detachment preserves rows/local constraints/defaults and own row type. Existing
identity/generation and enforcing groups survive attachment. Source mutation
propagates through real origins, not names, with one source/table/inheritance/codec/
storage publication. Stored consumers of every propagated own-row provider remain
guards even for identical-usage actions. Surviving origin-backed definitions become
independently replayable local declarations when dead provider edges disappear.
Source ADD/DROP/RENAME and TYPE/COLLATION default to RESTRICT when typed tables
exist; CASCADE propagates atomically through roots and descendants. Scalar
storage-free source TYPE is descriptor-only, whereas propagated table TYPE uses
assignment coercions and enforces defaults/CHECKs and stored own-row guards.
Composite-array holders are executable and remain genuine stored TYPE consumers.

Typed lifecycle preparation releases incidental replacement-read admission outside
the mutex while preserving genuine provider holds and exact descriptor fences.
Successive TEMP construction can use transaction-private predecessors, without
reauthorizing successful construction at a later outer commit. Live TEMP typed
attachments to persistent types block Reset/Restore at both preallocation and final
installation. Live TEMP inheritance edges to persistent parents likewise block
replacement. Neither route retargets by name/OID; standalone TEMP type creation
remains unsupported.

Partition virtual roots/intermediates own descriptors; physical leaves own storage
and local enforcing roots. Typed bounds/providers and comparison authority are
captured before unlocked evaluation. A transient ordered endpoint projection supplies
overlap witnesses without becoming a second mutable hierarchy. One write stage
routes and validates all destinations; late failure publishes no earlier leaf rows.
Defaults and disclosure authority belong to the named target, while physical errors
identify the actual leaf. Scans capture all descendant prefixes at one frontier.

RANGE ATTACH preserves an existing candidate's storage, own row type, surviving
column order/stable attributes, defaults, local constraints and index identities.
Compatibility is by column name/type/typmod/collation, required NOT NULL and
same-name cooked CHECK definition, not physical slot equality. Capture-owned
layouts translate reads, nested routing, ancestor bounds and leaf constraints;
identity layouts borrow rows. Violation identity is the physical leaf, while
failing-row disclosure follows the named target's completed tuple and privileges.
The same captured mappings are used by subsequent CREATE PARTITION OF validation
of an attached incumbent DEFAULT: a leaf-to-parent vector and reusable scratch
translate each physical tuple before the existing cooked key evaluator runs.
Identity layouts borrow the tuple. Storage, visibility and scan/publication fences
are unchanged, and CREATE's violation diagnostic identifies the DEFAULT root.
Required NOT NULL constraints match by mapped attribute, not constraint name.
A validated parent requirement rejects a matching NOT VALID child constraint,
even when the candidate is empty; attachment neither validates that constraint
nor replaces its identity. Rejection preserves local provenance and historical
rows, while a valid differently named match becomes inherited.
A permanent parent rejects an ordinary temporary candidate using its existing
session-owned namespace identity. Partition membership cannot mix permanent and
temporary relations or temporary relations belonging to different sessions;
this does not enable temporary partition definitions.

The parent retains SHARE UPDATE EXCLUSIVE; candidate and incumbent DEFAULT roots
retain ACCESS EXCLUSIVE. Candidate descendants always receive ACCESS EXCLUSIVE.
Validated CHECK/NOT NULL facts can elide scans; sufficient DEFAULT exclusion also
elides descendant admission. Nullable CHECK comparisons alone do not establish an
explicit range's non-NULL key premise; DEFAULT complements already accept NULL.
NOT VALID facts do not prove historical rows. Local valid NO INHERIT facts remain
usable for scan proof. All unproven leaves share a current validation capture;
store, provider, end-root and catalog predecessors fence atomic publication.

An already admitted SELECT waiting on an old descendant retains its captured
branch topology across compatible ATTACH growth. A fresh SELECT sees the new
member. This is not a general INSERT, COPY or index-admission snapshot policy.

Ordinary DETACH removes only the selected immediate relation/index/constraint
ancestry. Child data, stable attributes, own row type, defaults, local definitions,
owners/ACLs, independent view/type/collation/REGCLASS/sequence dependencies and
logical index roots remain. Matched inherited CHECK/NOT NULL/unique facts become
local without new identities; descendants of a detached virtual root retain
their own ancestry. No replacement bound CHECK is added. Parent AX precedes
incumbent DEFAULT AX, candidate AX and descendant AX; real waits run without
Engine.mu. Canonical catalog overlays and Finish/COMMIT/savepoints own publication
and release. Decreased topology invalidates held query analysis normally, while
already returned eager rows remain snapshots.


Partition key constraints/indexes form real immediate-parent ancestry. Virtual
indexes have no storage. Explicit ALTER INDEX attachment promotes validity only
through complete valid immediate membership. Ordinary table attachment and
detachment preserve stored invalidity even when coverage becomes complete;
valid-but-incomplete descriptors remain invalid catalog state. Transaction-private
attachments/removals restore through savepoints; independent leaf groups remain independent.
Dump/Restore remaps actual ancestry, partial attachments, provenance and retained
TEMP collisions, not assumed numeric OID relationships. When PARTITION OF cannot
preserve physical column order, distinct NOT NULL names, default absence or NOT
VALID facts, export uses ordinary CREATE plus genuine ATTACH. The existing logical
dependency scheduler orders definitions, named-column physical rows, memberships,
incomplete ONLY indexes, saved index edges and NOT VALID facts by their real phase
dependencies, not a second synthetic graph. When a late index referenced by row
data requires membership first, the leased private importer may replay NOT VALID
CHECK on the exact captured physical member through the ordinary ALTER validators
and publication authority. Public hierarchy ADD CHECK remains excluded.
Preserved invalid-complete virtual indexes use the same event scheduler: a real
valid child can be detached, have its manual index declared, and be reattached as
a table without promoting its parent's flag. An invalid child instead attaches
explicitly as an index, bottom-up; attaching a table with an invalid intermediate
index would create an unwanted duplicate tree and is not that replay recipe.
An older equivalent unattached local UNIQUE/PRIMARY backing must not be stolen
by that table reattachment: declare the captured attached backing first, then
declare a physical local index and restore its constraint with ADD USING INDEX.
A virtual local constraint instead follows as one conventional UNIQUE/PRIMARY
definition, recursively constructing its own distinct subordinate backing tree.
The existing name-reservation authority maps generated subordinate names to
captured private names, then terminal renames serve original-name consumers.
Initial virtual membership waits for subtree topology/data readiness, not deferred
full index completion; final table completion still includes every captured index.
Private origins capture the exact operation, definition, target/subtree and
intended attached plain tree under its invalid-complete parent. Existing binders,
recursive producers, validators and publication own the effects; PK replay
requires already-present NOT NULL facts. Public hierarchy ADD remains excluded.
A childless invalid index alone uses a collision-reserved empty DEFAULT before
ONLY declaration, then ordinary DETACH/DROP teardown. The isolated importer
admits qualified unconditional DETACH and only exact captured empty DEFAULT
teardown identities, not arbitrary DROP scripts. Actual unchanged emitted SQL,
not catalog flag writes, is the persistent-state representation.
Gaps are logically reminted, not a physical backup. Unchanged SQL replays on PostgreSQL.
HASH definitions use real PostgreSQL extended scalar support functions and the
release's NULL-skipping, ordered uint64 row combiner, not grouping/index hashes.
Bounds are sparse positive-int32 modulus/remainder pairs; mixed moduli must form a
divisibility chain and disjoint residue sets. DEFAULT is forbidden. Existing scalar
hash classes include integers/OID aliases, bool/char/name, text/bpchar/bytea, floats,
numeric, temporal/interval, UUID, JSONB, enum and domains with owned deterministic
collations. Arrays, unowned/custom classes and external locale providers remain
outside the concrete hash-provider boundary.
Internal CHAR provider support preserves signed-byte hashing without admitting
internal CHAR user columns or general casts; those existing SQL boundaries remain.
Named-target INSERT, leaf/ancestor constraints and parent reads reuse the existing
hierarchy authority, including direct/prepared metadata, privacy and atomic failure.
Dump routes data through its highest HASH ancestor and orders complete subtree
definitions/memberships first. Restore remints actual member/reference identities,
then reroutes imported candidate rows before rebuilding affected physical indexes.
Rebuilt index snapshots own pre-remint row/version vectors: later candidate row-ID
allocation cannot overwrite the identities used by index transport. Immutable
scalar payloads remain shared; catalog/role/frontier publication fences stay intact.
The replacement snapshot also borrows original source-row authority for HASH
descendants with NOT VALID CHECKs, including unindexed leaves. Stable row/version
matching skips deleted versions. Same-leaf old rows keep unrelated unchanged
NOT VALID violations; material referenced-cell/literal/owner-input changes are
rechecked with the ordinary validator. Descriptor retagging alone is not a change.
Actual movement and future INSERTs continue enforcing all destination constraints.
Retained TEMP and published old snapshots are never rewritten. A reminted key with
no destination or an incompatible leaf-local constraint fails Restore atomically.
LIST definitions use a single admitted btree key and immutable typed value
membership, with NULL distinct from every nonnull value. Each bound expression
executes once; comparator equality normalizes repeated entries within one bound
and rejects ownership overlap between siblings. DEFAULT remains the live
complement, including NULL unless explicitly owned. Routing, direct-leaf ancestor
checks and captured parent scans reuse the same hierarchy and physical storage
owners as RANGE/HASH, including mixed nested strategies.
New definitions validate occupied DEFAULT descendants before atomic publication;
failures and cancellation leave relation/type/index/storage facts unchanged.
LIST values are cloned/remapped with descriptor generations, destination-owned
enum/domain/scalar references and captured collation comparison authority.
Public LIST/HASH ATTACH, row movement, other hierarchy mutations/conflict DML,
TEMP partition construction and nested schema partition construction remain limits.

## Logical indexes and copied definitions

Index roots are immutable ordered entries or equality buckets built from live rows.
Writes maintain roots at the same Finish/commit frontier as rows, with distinct
RowID/VersionID membership. Captured physical positions provide safe candidate
translation without a full-table identity recovery map. Current-only roots are not
substitutes for historical or own-write query visibility. Payload metadata stays
separate from enforcing key authority.

Expression indexes retain analyzed dependency trees and simplified execution trees.
Derived keys evaluate at Execute before committed or private rows are installed;
failed batches preserve predecessor rows/roots and are recoverable through savepoints.
Predicate qualification precedes derived-key evaluation. Provider-aware concurrent/
trusted-import enforcement shares tuple extraction/comparison/claims rather than
creating a second enforcement path.

Dependent type reconstruction rebinds old-default classes and validates explicit
providers; included payload requires storage validity, not key comparison. Unrelated
slot compaction preserves roots and attributes; real dependencies drive retirement.
Schema moves publish the complete index/owned-sequence closure after collision checks.
Maintenance rebuilds selected roots atomically without changing rows or definition/
version identities; failed waits/builds preserve prior roots. Detached `catalog.Read`
metadata retains full vectors, provider identity and analyzed expressions. Physical
index options/storage, WAL and optimizer parity are not implied. See
[B-tree design](design/PG18-INDEX-BTREE.md), [hash design](design/PG18-INDEX-HASH.md)
and [maintenance design](design/PG18-INDEX-MAINTENANCE.md).

Copied definitions receive fresh destination attributes, constraint/backing identities
and roots, not source rows, ACLs or automatic parent attachment. Cooked definitions
remap local attributes but retain genuine external providers. Copied identity owns
a fresh generator configuration, not the source's runtime state. Exact source and
provider incarnations are held through one publication, including private staged
sources during schema construction. Same-owner TEMP references retain contextual
provenance; copying them does not legitimize stale or foreign references. Absent
compression/storage/statistics and constraint-comment providers remain absent,
not successful simulations of PostgreSQL facilities.

Query-created tables likewise own new attributes/origins and finalized typed values,
not source constraints, defaults, ACLs, comments or persistent query provenance.
Native query-table storage rejects PG_NODE_TREE structural carriers. Trusted Restore
can reconstruct certain already stored catalog scalar usages without widening public
raw type/cast admission. Symbolic reference remapping never imports foreign numeric
identities as native authority.

## Row security and metadata

Row-security flags are independent immutable metadata bits, transaction/savepoint
aware and altered without rewriting rows or cloning policy expressions. Ordinary
owner bypass, FORCE and direct superuser/BYPASSRLS decisions do not grant ordinary
ACLs. Default-deny is real; applicable policy expressions requiring unavailable
native enforcement reject at Execute rather than becoming fabricated constants.
Disabling or removing the last policy does not discard the flags. Native controls
serialize protected raw rows/policies before restoring nondefault flags, under a
narrow replay whitelist. See [authorization](AUTHORIZATION.md#row-security-metadata-and-enforcement-limits).

`Engine.Comment` exposes immutable local metadata of a `catalog.Read` at stable class/object/SubID
addresses; `RoleRead` exposes shared role text separately. The description catalog
is derived, not dumped as inserted rows. Column comments retain sparse attribute
numbers through compaction, rename, supported type changes and schema moves.
Metadata waiters refresh at the captured identity; replacement still fences them.
Ownership transfer includes exact attached indexes, row codecs and owned sequences
atomically, preserving ACL provenance, comments, definitions, policy and data.
Descendant/root/leaf owners remain independent.

Comments, constraint/default deparsing and structural catalog trees never evaluate
stored expressions. Rendering preserves bound providers, coercion modes and grouping,
not source whitespace or PostgreSQL's private node bytes. Definitions retain original
unsized input separately from assignment sizing. Session-setting changes do not
retroactively revalidate CHECKs; subsequent demanded providers use fresh execution
capture. Native role/comment and unsupported catalog-object facilities remain
separate capability limits.

## Native Dump and Restore

Dump captures committed live state, not utility history, private mutation/tombstones,
mutable constraint/window state or cursors. It emits dependency-ordered definitions,
current names, explicit ordinary data, owner directives and canonical provider-owned
literals. Defaults that form legal cycles are deferred after their providers. Restore
stages through shared binders/codecs and validates the complete candidate before one
catalog replacement. Its narrow utility whitelist is not arbitrary PostgreSQL dump
or ALTER acceptance. Context, reader, parse, binding or late validation failure
preserves the target and already retained snapshots. Explicit ACL serialization
limits remain errors rather than silent loss.

FK import captures declarations and creator authority only for newly minted actual
table references; later ownership/elevation does not replace that authority. Pending
attachments resolve supporting keys and validate final rows without executing
cascades to repair input. Forward references/cycles work under those constraints.
Invalid definitions replay through bounded post-data attachments without automatic
promotion or trusted write bypass. Volatile CHECKs genuinely run during staged data
insertion and can reject history-dependent replay.

Materialized views keep stored rows/population separately from immutable producer
identity. Plain refresh replaces the producer bag; concurrent refresh retains
each old row matching every eligible UNIQUE key and full row image once, suppresses
matching new occurrences, and inserts every unmatched new occurrence. INCLUDE
does not add key equality; NULL key operands never match, even under NULLS NOT
DISTINCT. Identical non-NULL producer rows fail21000 before runtime23505 checks
against the effective replacement bag. Rows, population and logical index roots
publish atomically. Failure/cancellation keeps old storage; concurrent readers
retain coherent statement/RR roots rather than being blocked by target EXCLUSIVE.
Queued metadata changes retain defining authority but capture a compatible
intervening refresh's current storage at grant. Native explicit utility transaction
limits differ from PostgreSQL transactional refresh. PostgreSQL's generated
physical SPI Context is not a native diagnostic; stable logical fields remain
available, including owner-executed unique-key Detail.

Materialized snapshots use genuine versioned line comments with marker
`-- stuntgres-matview-v1 <base64>` and strict JSON fields `schema`, `name`,
`populated`, `rows`. Restore recognizes actual comments only, rejects duplicates,
malformed/incorrect targets and lossy/effectful row conversions, and never populates
through public DML or reevaluates the producer. Genuine extension dependency edges
retain owner authority and are remapped with the closure. PostgreSQL ignores snapshot
comments: actual dump replay restores definitions unpopulated, while native Restore
retains exact stale refreshed rows. This is deliberate logical persistence, not
physical heap/index swapping, WAL/crash durability, external pg_dump interchange,
wire-server APIs or extension ABI support.

Local comments share the same dump frontier as rows. Bootstrap overrides/removals
are replayed relative to the fresh baseline; role comments are retained separately
and excluded from database Dump/Restore. Restored live attribute numbers can be
reminted, so names/relationships and text roundtrip, not historical allocator gaps.
Inherited/typed/partition closure import similarly reowns real origin/provider links
beside exact retained TEMP identities. Exceptional remapping can change allocator-
relative diagnostic order; no separate persistent ordering authority is invented.

## Development and independent coverage

Run existing shared selectors with `make check` and the same `RUN` with `REFERENCE=1`
for independent pinned PostgreSQL expectations. Useful families include
`^TestReferenceAlterAddConstraintFeatureSmoke$`, `^TestReferenceForeignKeyFeatureSmoke$`,
`^TestReferenceTypedTable`, `^TestReferenceTemporaryTableFeatureSmoke$`,
`^TestReferenceGeneratedVirtual`, `^TestReferenceAlterGeneratedExpressionFeatureSmoke$`,
`^TestReferenceRangePartitionFeatureSmoke$`, `^TestReferenceUsingIndexFeatureSmoke$`,
`^TestReferenceExclusionFeatureSmoke$` and `^TestReferenceTable(Like|Core)`.
These are instructions, not exercised verification claims.

Native controls, retained snapshots, actual manager waits and real emitted-SQL
PostgreSQL replay are distinct proof phases. Raw adapter metadata and complete
backend-specific diagnostics are asserted before normalization. Public deadline/
effect callbacks alone do not prove queue order. See [testing](TESTING.md) and
[dual-mode development](DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition).

### Partitioned exclusion providers

Each local declaration covers every partition key of its target and descendants
with actual equality, matching canonical operator inputs, stable attributes and
effective collation. INCLUDE and expressions do not provide coverage. Independent
child-local constraints are scoped locally rather than requiring all ancestor keys.
Virtual indexes own metadata only; actual leaf providers enforce all-key conflict.
INET GiST equality and range overlap use genuine logical method/class identities.

CREATE and selected EXCLUDE ADD/DROP reuse staged recursive index production.
RANGE ATTACH preserves independent exclusions and clones a fresh inherited graph,
as PostgreSQL18.0 does; explicit matching exclusion index ATTACH fails42P17.
DETACH makes crossing providers local but retains the detached subtree's internal
edges. DROP follows inherited edges only. Catalogs expose actual immediate Parent,
local/inheritance and declaration deferral flags and canonical operator inputs.
Dump declares each local provider once, recreates inherited providers automatically
and restores current names with the existing rename scheduler. It never requires
PostgreSQL-forbidden exclusion index ATTACH. Restore rebinds destination providers.

ONLY ADD retains existing child gaps while future membership creates inherited
providers. Dump/Restore preserves those gaps, independent local constraints,
captured invalidity and data even when uncovered rows overlap. Complete-invalid
and childless-invalid roots use collision-reserved empty scratch partitions during
SQL replay; scratch relations disappear before captured DEFAULT membership and
row replay. The same dependency scheduler and logical creator/name barriers retain
canonical bytes without catalog repair or captured-child detach/reattach.

