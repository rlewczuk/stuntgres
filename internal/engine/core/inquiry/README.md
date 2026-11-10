# internal/engine/core/inquiry

Catalog, session and transaction inquiry runtime bodies below core. Core owns
SQL-call precedence, analyzer/coercion authority and statement snapshot capture.
The leaf depends only on lower packages and `core/catalogrt`, never core.

## Contract

- One exported function per runtime method, named like it, taking the host
  first: `FormatType`, `SizeInquiry`, `PublicationTables`, `DeparseInquiry`,
  `IdentityValue`, `CurrentSnapshot`, … Each is that method's body.
- Catalog-object inquiry reads only the statement's retained snapshots: the
  comment catalog and its roles, the visibility catalog and paths, and the
  deparse path and authority. It never consults live settings, and it rejects
  a missing or foreign snapshot.

## Host

- `CatalogHost` and `SessionHost` are the statement an
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

## Testing

Engine-host tests live in core and `core/enginetests/functions` or `pgcatalog`.
Public inquiry behavior belongs to reference packages.
