# internal/engine/core/inquiry

The inquiry runtimes: the bodies of `exec.CatalogInquiryRuntime`
(`format_type`, `pg_basetype`, `pg_get_userbyid`, the tablespace inquiries,
the size and file functions, `pg_partition_*`, the publication inquiries, the
comment functions, the `pg_get_*def` deparse functions, object address,
identity and description, the index property functions and the `pg_*_is_visible`
functions), `exec.SessionInquiryRuntime` (session identities,
`current_schemas`, the TEMP namespace inquiries and the notification channel
reads) and `exec.TransactionInquiryRuntime` (transaction ages, IDs, the current
snapshot, status, multixact members and commit timestamps). It is a leaf below
core (ENGINE-SPLIT §3, L14.5); the catalog renderers are
[`engine/regobj`](../../regobj/README.md), [`engine/table`](../../table/README.md)
and [`engine/stored`](../../stored/README.md), and the visibility registry is
[`engine/visibility`](../../visibility/README.md).

## Layering

- Imports only packages below core and the leaf `core/catalogrt` (rule 6):
  `access`, `analyze`, `authorization`, `catalog`, `ddl`, `exec`, `expr`,
  `functions`, `ids`, `index`, `lock`, `partition`, `pgcatalog`,
  `publication`, `regobj`, `session`, `settings`, `storage`, `stored`, `table`,
  `visibility`, `parser`, `sqlstate`, `sqltype`. It never imports core,
  another leaf (except `catalogrt`), a family or the root.
- Core reaches it through one-line adapters (`inquiry_statement_host.go`):
  the methods of `catalogInquiryHost`, `sessionInquiryHost` and
  `transactionInquiryHost`, the `exec.Host` capability groups, call the entry
  functions with the statement host.
- Core keeps the statement's inquiry snapshot (`inquirySnapshot`) and its
  captures at query-source admission, lazy SQL-body admission
  (`routine_inquiry.go`), the size captures over the cluster's target
  runtimes, the deparse and object address snapshots over the captured deparse
  authority, the collation-import check, and the identity, schema-path, TEMP,
  notification and transaction state the session inquiries read.

## Contract

- One exported function per runtime method, named like it, taking the host
  first: `FormatType`, `SizeInquiry`, `PublicationTables`, `DeparseInquiry`,
  `IdentityValue`, `CurrentSnapshot`, … Each is that method's body.
- Catalog-object inquiry reads only the statement's retained snapshots: the
  comment catalog and its roles, the visibility catalog and paths, and the
  deparse path and authority. It never consults live settings, and it rejects
  a missing or foreign snapshot.

## Host

- `CatalogHost` (18 methods) and `SessionHost` (21) are the statement an
  inquiry runs in; core implements both on `statementHost` (the shared leaf
  method set, `inquiry_statement_host.go`). Shared with the other leaves:
  `Operation`, the Engine.Mu pair, `InquiryCatalog`, the catalog epoch and
  journals, `TransactionRef`, `CatalogDomain`, `LockManager` and `Ticket`.
- No authorization value crosses the seam. Where an inquiry needs
  authorization (database and tablespace size admission, collation import,
  schema paths, TEMP namespaces, foreign-key action identities) a host method
  answers under core's authority and returns catalog values.

## Locks

- Inquiries are entered with no lock held. `ReacquireEngine` takes `Engine.Mu`
  and `ReleaseEngineLocked` releases it; the host methods named …Locked run
  inside those windows.
  `CurrentSnapshotLocked` also takes `Session.Mu`, inside `Engine.Mu`.
- Multixact ages and members read the lock manager outside `Engine.Mu`;
  members are packed from the manager's snapshot clone with no mutex held.
  Size arithmetic runs after `Engine.Mu` is released.

## Behaviour

`DatabaseFilesInquiry` uses the ordinary catalog inquiry owner/role-domain
and replacement fences under `Engine.Mu`, then reads the transaction-private
catalog. `pgcatalog.ClassKind` distinguishes storage-less descriptors from
tables/indexes/sequences/materialized views/stored catalogs; allocated rows are
irrelevant. Forward stored calls report explicit `0A000` because PostgreSQL
filenodes and files do not exist here. Reverse absence returns typed REGCLASS
NULL, not a fabricated OID mapping. No storage capture, filesystem probe, new
mapping state or serialization is introduced. Common routine admission and
REGCLASS input retain EXECUTE and schema-USAGE precedence.

`SizeInquiry` fences the retained catalog's owner and replacement epoch,
captures typed allocation roots from the current matching private catalog and
transaction overlays under the shared cluster mutex (`RelationAllocationLocked`),
and counts outside that lock. Database/tablespace admission
(`AggregateAllocationsLocked`, in core) captures current invoker authority,
resolves existence before CONNECT/CREATE or inherited statistics privileges,
and selects registered target runtimes rather than treating the attached store
as the cluster. It borrows `RoutineAuthorizationLocked`, never rule
relation-write authority: effective/restricted invocation owners and legitimate
RI precedence remain the routine admission owner's responsibility. It retains
immutable vectors, not mutable heap headers or prepared numeric/ACL results.
The allocation model belongs to
[`storage`](../../storage/README.md#logical-allocation-estimates).

`FormatType` consumes the immutable visibility catalog and authorized
relation/type-compatible path, including the session's TEMP namespace.
`BaseType` needs only that catalog. Both are captured at the execution frontier
with schema USAGE filtering and normal implicit/explicit pg_catalog precedence.
The TEMP-filtered conversion/collation path keeps independent backing and is
not used for type formatting. Expression snapshot demand and retained
DEFAULT/generation/CHECK/view recipes select the capture; evaluation never
rereads session settings. COPY WHERE forwards its local binder's demand after
target and used-column authorization, before predicate preflight. Retained
write demand adds to that capture without replacing or clearing it. REGTYPE
results use ordinary retained row-output spelling, independently of formatter
TEXT.

Routine declaration inquiry composes the statement-owned catalog and presentation
settings with the shared formal projection and metadata-safe default/body
renderers. It never invokes the target or checks its EXECUTE privilege. Actual
Procedure descriptors are authoritative; release identity aliases do not create
catalog objects. Public CREATE OR REPLACE output is independent of native dump
SQL, while both consume the same retained declaration and wrapper metadata.
Language presentation uses the actual descriptor address from that same snapshot;
native internal/C/SQL declarations resolve their fixed language identities.
Renames and aliases never require a display-label match or target compilation.
Body presentation carries the querying statement's captured temporary-owner
authority, never the stored routine's principal. Qualified own-session TEMP
sources use `pg_temp`; foreign or absent temporary authority retains physical
namespace naming through the unchanged stored relation renderer.

Transaction-inquiry XID, status and age reads run under `Engine.Mu` behind the
transaction's catalog-epoch fence; commit-timestamp sampling runs under
`Engine.Mu` without it.

## Testing

- The runtimes' engine tests stay in core
  (`RUN='^Test(CatalogRelations|NamespaceIdentity|RelationSize|PublicationInquiry|DatabaseFiles)'`
  over `./internal/engine/core`); census-owned inquiry tests are in
  `core/enginetests/{tables,indexes,roles,stored,functions}`. Public scenarios
  are `RUN='^TestReference(Size|TypeDescribe|CatalogRelations|TablespaceInfo|DatabaseFiles|PartitionInfo|PublicationInquiry|Comment|RoutineDeparse|ObjectAddress|IndexProperties|NamespaceIdentity|NotificationInfo|LegacySnapshot|TransactionIdentity|CommitTime)'`.
- Under T7 this package has no `export_test.go`; engine tests that come here
  later go to `core/inquiry/enginetests`.
