package functions

import "stuntgres/internal/sqltype"

type BlockingKind uint8

const (
	OrdinaryBlockingInquiry BlockingKind = iota
	SafeSnapshotBlockingInquiry
)

func (k BlockingKind) Name() string {
	switch k {
	case OrdinaryBlockingInquiry:
		return "pg_blocking_pids"
	case SafeSnapshotBlockingInquiry:
		return "pg_safe_snapshot_blocking_pids"
	}
	return ""
}

// BlockingRuntime reads live authority; recipes never retain lock or SSI state.
type BlockingRuntime interface {
	BlockingInquiry(BlockingKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
}
