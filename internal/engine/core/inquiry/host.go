package inquiry

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CatalogHost is the statement a catalog-object inquiry function runs in:
// type, role-name, tablespace, size, file, partition, publication, comment,
// deparse, object address, index property and visibility inquiry. Core
// implements it on its statement host; every call reads the live statement.
//
// Inquiry functions are entered with no lock held. ReacquireEngine takes
// Engine.Mu and ReleaseEngineLocked releases it; the methods named …Locked
// need Engine.Mu.
type CatalogHost interface {
	Operation() *session.Operation
	ReacquireEngine()
	ReleaseEngineLocked()

	// CommentSnapshot is the statement's retained inquiry catalog and its
	// role snapshot, captured once at query-source admission; the catalog has
	// no state before that capture.
	CommentSnapshot() (catalog.Read, authorization.Read)
	// RoleDomain is the engine's role domain, which owns the inquiry roles.
	RoleDomain() *authorization.Domain
	// InquiryCatalog is the retained inquiry catalog once its owner and role
	// domain match the engine's.
	InquiryCatalog() (catalog.Read, error)
	// VisibilitySnapshot is the execution frontier's visibility catalog with
	// its relation/type path and its TEMP-filtered conversion path.
	VisibilitySnapshot() (catalog.Read, []ids.ObjectAddress, []ids.ObjectAddress)
	// DeparsePath is the inquiry snapshot's presentation path.
	DeparsePath() []ids.ObjectAddress
	// DeparseInputs is the statement's deparse snapshot for table.Deparse.
	DeparseInputs() table.DeparseInputs
	// DeparseView is the deparse authority's access view as captured.
	DeparseView() access.View
	// LookupAddress is the statement's object address snapshot.
	LookupAddress() regobj.AddressLookup
	// AdmitInquiryBody admits a SQL routine inquiry body once per statement.
	AdmitInquiryBody(c *exec.Context, provenance expr.Info) error
	// InitializeInquiryIndexMethod runs the genuine zero-argument AM handler
	// factory and returns its consumed native property authority.
	InitializeInquiryIndexMethod(catalog.Read, *catalog.AccessMethod) (catalog.IndexMethod, error)

	CatalogEpochLocked() ids.CatalogEpoch
	PendingCatalog() *session.Journal
	PrivateCatalogLocked(changes *session.Journal) catalog.Read
	// RelationAllocationLocked captures the allocation roots of relation in
	// view over the statement's private and transaction overlays.
	RelationAllocationLocked(view catalog.Read, relation *catalog.Relation) storage.Allocation
	// AggregateAllocationsLocked admits a database or tablespace size inquiry
	// under the invoker's authority and captures the allocated relations of
	// every registered target runtime.
	AggregateAllocationsLocked(ctx context.Context, kind functions.SizeKind, arg sqltype.Value, view catalog.Read) ([]storage.Allocation, bool, error)
	// ImportSystemCollationsLocked runs pg_import_system_collations' checks
	// for the namespace OID and reports that the import is unsupported.
	ImportSystemCollationsLocked(namespace uint32) error
}

// SessionHost is the statement a session or transaction inquiry function
// runs in: identities, schema paths, TEMP namespaces, notification channels,
// transaction ages, IDs, snapshots, status, multixact members and commit
// timestamps. The lock rules are CatalogHost's; CurrentSnapshotLocked also
// takes the session mutex, inside Engine.Mu.
type SessionHost interface {
	ReacquireEngine()
	ReleaseEngineLocked()

	// ExpressionIdentity is the identity snapshot c's expressions report: the
	// statement's, the admitted query expression's, or the acting
	// foreign-key action user's as current user.
	ExpressionIdentity(c *exec.Context) (exec.IdentitySnapshot, error)
	// InquiryCacheable reports that no foreign-key or rule action authority
	// acts, so schema results may be cached on the statement.
	InquiryCacheable() bool
	// CurrentSchemaResult and SearchPathResults are the statement's caches of
	// current_schema() and current_schemas(false/true).
	CurrentSchemaResult() *sqltype.Value
	SearchPathResults() *[2]sqltype.Value
	// CurrentSchemaPathLocked is the explicit schema path in its catalog;
	// SearchPathsLocked adds the effective relation path.
	CurrentSchemaPathLocked() (catalog.Read, []ids.ObjectAddress, error)
	SearchPathsLocked() (catalog.Read, [2][]ids.ObjectAddress, error)
	// TemporaryNamespaceLocked is the session's own TEMP namespace (or the
	// zero address) under the namespace authorization;
	// NamespaceSessionLocked is that authorization's catalog and session.
	TemporaryNamespaceLocked() (ids.ObjectAddress, error)
	NamespaceSessionLocked() (catalog.Read, *session.Session, error)
	// ListeningChannelsLocked and NotificationQueueUsageLocked validate the
	// operation, then read the session's channels and the queue usage.
	ListeningChannelsLocked() (map[string]struct{}, error)
	NotificationQueueUsageLocked() (float64, error)

	TransactionRef() ids.TxRef
	CatalogDomain() *ids.Domain
	CatalogEpochLocked() ids.CatalogEpoch
	LockManager() *lock.Manager
	Ticket() *lock.Ticket
	VisibilityLocked() *visibility.Registry
	// TransactionAgeLocked fences the transaction against the current catalog
	// epoch and measures xid's age.
	TransactionAgeLocked(xid uint32) (int32, error)
	AssignTopTransactionXIDLocked() error
	// CurrentSnapshotLocked validates the operation for publication and the
	// transaction's catalog epoch, then returns the statement's snapshot,
	// nil when there is none.
	CurrentSnapshotLocked() (*sqltype.Snapshot, error)
}

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
