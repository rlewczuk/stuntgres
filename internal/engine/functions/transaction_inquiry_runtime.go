package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"time"
)

// TransactionInquiryRuntime borrows the executing statement's canonical
// transaction authorities for one inquiry. Nodes retain no runtime state.
type TransactionInquiryRuntime interface {
	CheckTransactionInquiryContext() error
	TransactionAge(uint32) (int32, error)
	RequireTransactionIdentityScope() error
	MultiTransactionAge(uint32) (int32, error)
	CurrentTransactionID(bool) (uint64, error)
	CurrentSnapshot() (*sqltype.Snapshot, error)
	TransactionStatus(uint64) (string, bool, error)
	TransactionMembers(uint32, *sqltype.Descriptor) ([]sqltype.Value, error)
	CommitTimestamp(uint32) (time.Time, uint32, bool, error)
	LastCommittedTransaction() (uint32, time.Time, uint32, bool, error)
}

func transactionInquiryRuntime(ctx expr.Context) (TransactionInquiryRuntime, error) {
	runtime, ok := ctx.(TransactionInquiryRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing transaction inquiry runtime")
	}
	return runtime, nil
}
