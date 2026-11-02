package contrib

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Host is the statement a contrib function runs in. Core implements it on its
// statement host; every call reads the live statement, never a copy.
//
// Contrib functions are entered with no lock held. ReacquireEngine takes
// Engine.Mu and ReleaseEngineLocked releases it; ReacquireOwnerEngine takes
// the transaction owner's mutex, then Engine.Mu, and ReleaseOwnerEngineLocked
// releases both. The methods named …Locked need Engine.Mu. Lock waits, nested
// SQL and the maintenance-owner check run with nothing held.
type Host[A Authority] interface {
	// NestedSQL runs crosstab's and connectby's source SQL.
	exec.NestedSQL
	Context() context.Context
	Operation() *session.Operation
	Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment)
	// ActivateContribRoutineCommand installs an authenticated routine's lexical
	// command policy. The caller restores it after all nested cursors close.
	ActivateContribRoutineCommand(*catalog.Procedure) func()
	// Active fails once the statement is finished or its operation is no
	// longer valid.
	Active() error
	ReacquireEngine()
	ReleaseEngineLocked()
	ReacquireOwnerEngine()
	ReleaseOwnerEngineLocked()

	// RoutineAuthorizationLocked is the executing routine's authorization;
	// WriteAuthorizationLocked the statement's admitted query authority;
	// CallableAuthorizationLocked the routine authority under a restricted or
	// effective user, else the query authority.
	RoutineAuthorizationLocked() (A, error)
	WriteAuthorizationLocked() (A, error)
	CallableAuthorizationLocked() (A, error)
	// ResolveRelationName resolves a utility-qualified relation name in auth.
	ResolveRelationName(auth A, name parser.ObjectName) (ids.ObjectRef, error)
	// ClassInput resolves regclass text in auth over path.
	ClassInput(auth A, path []ids.ObjectAddress) regobj.ClassInput
	// TypeDiagnosticName names types as auth's search path displays them.
	TypeDiagnosticName(auth A) (func(sqltype.ID) string, error)

	CatalogDomain() *ids.Domain
	CatalogEpochLocked() ids.CatalogEpoch
	// PendingCatalog is the statement's private catalog journal; OwnerCatalog
	// the transaction owner's.
	PendingCatalog() *session.Journal
	OwnerCatalog() *session.Journal
	PrivateCatalogLocked(changes *session.Journal) catalog.Read
	// PrivateStorageLocked is the relation's storage over PendingCatalog;
	// JournalStorageLocked over changes.
	PrivateStorageLocked(address ids.ObjectAddress) *storage.Relation
	JournalStorageLocked(changes *session.Journal, address ids.ObjectAddress) *storage.Relation
	EngineStorageLocked() *storage.Store
	SequenceGeneratorLocked(address ids.ObjectAddress) *storage.SequenceGenerator
	// RelationAllocationLocked borrows the existing genuine index allocation roots.
	RelationAllocationLocked(view catalog.Read, relation *catalog.Relation) storage.Allocation
	CommitSequenceLocked() ids.CommitSeq
	// ColumnProjectionRoots is the statement's own private writes and deletes
	// of the relation.
	ColumnProjectionRoots(address ids.ObjectAddress) ([]storage.RowVersion, *visibility.PrivateDeletes)
	TransactionRef() ids.TxRef
	// MarkTemporaryTargetLocked records the sticky transaction TEMP footprint
	// of an opened target.
	MarkTemporaryTargetLocked(ref ids.ObjectRef)
	// RelationDataViewLocked is the statement's data view of ref: the
	// statement's own, or a local capture over it (local) that the caller
	// releases.
	RelationDataViewLocked(ref ids.ObjectRef) (view *visibility.View, local bool, err error)

	LockManager() *lock.Manager
	Ticket() *lock.Ticket
	// AcquireRelationContribution waits for a temporary relation lock
	// contribution with nothing held, dropping the replacement read around a
	// queued wait and rejoining it for an implicit transaction. The caller
	// releases the contribution with LockManager().ReleaseRelation.
	AcquireRelationContribution(ref ids.ObjectRef, mode lock.Mode, contribution *lock.RelationContribution) error
	// PgrowlocksReadmitLocked and VerifyHeapamReadmitLocked fence a waited
	// target: the lexical name and the incarnation never rebind.
	PgrowlocksReadmitLocked(auth A, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error
	VerifyHeapamReadmitLocked(auth A, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error

	// LockHeapStatisticsCohort locks every live transaction owner in session
	// token order, then Engine.Mu, retrying until the cohort is current;
	// UnlockHeapStatisticsCohortLocked releases them in reverse.
	LockHeapStatisticsCohort() ([]*session.TransactionOwner, error)
	UnlockHeapStatisticsCohortLocked(owners []*session.TransactionOwner)
	// HeapStatisticsFactsLocked and HeapStatisticsPrivateWritesLocked capture
	// the cohort's and prepared transactions' versions of store.
	HeapStatisticsFactsLocked(owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) []storage.HeapTupleFact
	HeapStatisticsPrivateWritesLocked(owners []*session.TransactionOwner, store *storage.Relation, address ids.ObjectAddress, writes []storage.RowVersion) []storage.RowVersion
	// PgvisibilityHorizonLocked shares the same cohort/Engine capture window.
	PgvisibilityHorizonLocked(owners []*session.TransactionOwner) storage.VisibilityHorizon
	// PgvisibilityCatalogBytes enters with no mutex and consumes a real
	// registered catalog producer, without inheriting SQL projection filters.
	PgvisibilityCatalogBytes(ref ids.ObjectRef, target *catalog.Relation) (int64, error)

	// CheckBtreeAsOwner runs the B-tree checks in parent's restricted
	// maintenance-owner environment.
	CheckBtreeAsOwner(parent *catalog.Relation, capture index.AmcheckCapture, options index.AmcheckOptions) error
	CheckGinAsOwner(parent *catalog.Relation, capture index.AmcheckCapture) error
	// GenericFileAuthority is the routine principal's pg_read_server_files
	// membership and the data and log directories, after settings admission.
	GenericFileAuthority() (dataDirectory, logDirectory string, privileged bool, err error)
	// InquiryCatalog is the owner-validated catalog inquiry snapshot.
	InquiryCatalog() (catalog.Read, error)
}

// Authority is the operation's authorization, held by value: capturing it
// never allocates. It is the access.Environment of its privilege checks.
type Authority interface {
	access.Environment
	Access() access.View
	Check(access.Requirement) error
}

func sqlError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
