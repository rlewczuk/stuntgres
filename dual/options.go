package dual

import (
	"database/sql/driver"
	"io"
	"time"
)

// defaultReferenceTimeout matches the cmd/stuntgres --reference-timeout default.
const defaultReferenceTimeout = 30 * time.Second

// OrderPolicy decides whether a result set that matches the reference only as
// a multiset (same rows, different order) adds a response_diff line. Such a set
// is always flagged with order_only.
type OrderPolicy int

const (
	// OrderByClause counts an order-only difference iff the query text has a
	// top-level ORDER BY in any of its statements.
	OrderByClause OrderPolicy = iota
	// OrderAlways counts every order-only difference.
	OrderAlways
	// OrderNever never counts an order-only difference.
	OrderNever
)

// Call names one mirrored driver call; it is the record's call field.
type Call string

const (
	CallConnect      Call = "connect"
	CallPing         Call = "ping"
	CallPrepare      Call = "prepare"
	CallExec         Call = "exec"
	CallQuery        Call = "query"
	CallStmtExec     Call = "stmt_exec"
	CallStmtQuery    Call = "stmt_query"
	CallStmtClose    Call = "stmt_close"
	CallBegin        Call = "begin"
	CallCommit       Call = "commit"
	CallRollback     Call = "rollback"
	CallResetSession Call = "reset_session"
	CallIsValid      Call = "is_valid"
	CallClose        Call = "close"
)

// Value is one non-NULL result value handed to Options.Normalize.
type Value struct {
	Reference bool         // the reference side produced it
	Column    string       // native column name
	Type      string       // normalization key: native type name, else the reference's
	Value     driver.Value // never nil
}

// Options configures a dual connector. The zero value is usable.
type Options struct {
	// Log receives one JSON record per line; nil discards records.
	Log io.Writer
	// ReferenceTimeout bounds every reference call; <= 0 means 30s.
	ReferenceTimeout time.Duration
	// Order is the order-only policy; the zero value is OrderByClause.
	Order OrderPolicy
	// Normalize canonicalizes non-NULL values before the defaults;
	// ok=false keeps the default rendering. It must be goroutine-safe.
	Normalize func(Value) (text string, ok bool)
	// ErrorFields extracts SQLSTATE and message before the defaults;
	// ok=false (or an empty code) keeps the default. It must be goroutine-safe.
	ErrorFields func(err error) (code, message string, ok bool)
	// CompareErrorDetails also compares Detail, Hint and Position.
	CompareErrorDetails bool
	// Uncompared calls are mirrored and logged, never diffed.
	Uncompared []Call
}

func (o *Options) uncompared(call Call) bool {
	for _, c := range o.Uncompared {
		if c == call {
			return true
		}
	}
	return false
}
