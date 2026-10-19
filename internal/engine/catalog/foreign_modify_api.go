package catalog

import "context"

// ForeignModifyOperation identifies an independently optional write capability.
type ForeignModifyOperation uint8

const (
	ForeignInsert ForeignModifyOperation = iota
	ForeignUpdate
	ForeignDelete
)

// Plans and target descriptors are reusable immutable provider data. They must
// not retain execution contexts, mutable row buffers, or transaction handles.
type ForeignModifyPlan struct{ Private any }
type ForeignUpdateTargets struct {
	Names   []string
	Private any
}
type ForeignRowIdentity = ForeignRow

type ForeignModifyRequest struct {
	ForeignRequest
	Operation ForeignModifyOperation
	Plan      ForeignModifyPlan
	Targets   ForeignUpdateTargets
}

// ForeignInsertRequest is COPY's insertion lifecycle, not an ordinary DML plan.
type ForeignInsertRequest struct {
	ForeignRequest
	Copy bool
}
type ForeignTruncateRequest struct {
	Tables                   []ForeignRequest
	Cascade, RestartIdentity bool
	Transaction              ForeignTransaction
}

type ForeignUpdateTargetProvider interface {
	AddForeignUpdateTargets(context.Context, ForeignRequest, ForeignModifyOperation) (ForeignUpdateTargets, error)
}
type ForeignModifyPlanner interface {
	PlanForeignModify(context.Context, ForeignRequest, ForeignModifyOperation, ForeignUpdateTargets) (ForeignModifyPlan, error)
}
type ForeignModifyBeginner interface {
	BeginForeignModify(context.Context, ForeignModifyRequest) (any, error)
}
type ForeignModifyEnder interface {
	EndForeignModify(context.Context, ForeignModifyRequest, any) error
}

// Mutation results are actual full tuples. Present=false means no affected row;
// a present empty or all-NULL tuple is success. Errors always veto the result.
// Buffers remain valid until the next callback, including teardown.
type ForeignInserter interface {
	ExecForeignInsert(context.Context, ForeignModifyRequest, any, ForeignRow) (ForeignRow, bool, error)
}
type ForeignUpdater interface {
	ExecForeignUpdate(context.Context, ForeignModifyRequest, any, ForeignRowIdentity, ForeignRow) (ForeignRow, bool, error)
}
type ForeignDeleter interface {
	ExecForeignDelete(context.Context, ForeignModifyRequest, any, ForeignRowIdentity, ForeignRow) (ForeignRow, bool, error)
}

// Batching requires both optional interfaces and a positive maximum size.
// RETURNING and applicable view CHECK OPTION use single-row insertion.
// The returned successful tuple vector determines count and cannot exceed input.
type ForeignBatchSizer interface {
	GetForeignModifyBatchSize(context.Context, ForeignModifyRequest, any) (int, error)
}
type ForeignBatchInserter interface {
	ExecForeignBatchInsert(context.Context, ForeignModifyRequest, any, []ForeignRow) ([]ForeignRow, error)
}
type ForeignTruncater interface {
	ExecForeignTruncate(context.Context, ForeignTruncateRequest) error
}
type ForeignInsertBeginner interface {
	BeginForeignInsert(context.Context, ForeignInsertRequest) (any, error)
}
type ForeignInsertEnder interface {
	EndForeignInsert(context.Context, ForeignInsertRequest, any) error
}

// Identity scans share the ordinary Begin/ReScan/End lifecycle. Identity cells
// are opaque standard driver values, separate from the visible row and NEW key.
// Byte cells compare by content. Other cells must be runtime-comparable and
// retain typed Go equality; noncomparable driver extensions fail admission
// before mutation. Comparable extensions are not normalized or coerced.
type ForeignIdentityScan interface {
	IterateForeignIdentity(context.Context) (ForeignRow, ForeignRowIdentity, bool, error)
}

type ForeignTransactionCapabilities struct{ StatementRollback, TransactionRollback, Savepoints bool }
type ForeignTransactionProvider interface {
	ForeignTransactionCapabilities(context.Context, ForeignRequest) (ForeignTransactionCapabilities, error)
	BeginForeignTransaction(context.Context, ForeignRequest) (ForeignTransaction, error)
}

// The provider performs genuine transactional effects, never compensation.
// Commit errors may mean an unknown outcome. No callbacks are retried and no
// distributed atomicity is promised across providers or local publication.
// Accepted callbacks must not return driver.ErrBadConn or driver.ErrSkip.
// Direct/wrapped/joined matches become ordinary provider-contract errors with
// original text but no sentinel unwrap/Is/SQLState, preventing database/sql replay.
// Every other error is preserved unchanged, including context/I/O diagnostics.
type ForeignTransaction interface {
	BeginStatement(context.Context) error
	EndStatement(context.Context, bool) error
	Commit(context.Context) error
	Rollback(context.Context) error
}
type ForeignTransactionSavepoints interface {
	Savepoint(context.Context, string) error
	RollbackTo(context.Context, string) error
	Release(context.Context, string) error
}
