package dml

import (
	"fmt"
	"sync/atomic"

	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CopyFromState belongs to one command. A soft input failure returns one present
// row so omitted defaults still run; admission consumes that pending rejection
// before WHERE, generation, routing or table constraints. Binary uses the same
// predicate admission but never creates pending conversion failures.
type CopyFromState struct {
	Options     copyfmt.Options
	Predicate   expr.Expr
	InputChecks sqltype.DomainCheckEvaluator
	Rejected    int64
	pending     bool
	Progress    *CopyProgress
}

// CopyProgress belongs to the actual COPY command, not its transaction.
// Atomic counters allow observers without reacquiring an engine lock from DML.
type CopyProgress struct {
	Processed, Excluded, Skipped atomic.Int64
}

func (s *CopyFromState) completed(n int64) {
	if s.Progress != nil {
		s.Progress.Processed.Add(n)
	}
}
func (s *CopyFromState) admit(evaluation *exec.Context) (bool, error) {
	if s.pending {
		s.pending = false
		s.Rejected++
		if s.Progress != nil {
			s.Progress.Skipped.Add(1)
		}
		if s.Options.RejectLimit > 0 && s.Rejected > s.Options.RejectLimit {
			return false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: fmt.Sprintf("skipped more than REJECT_LIMIT (%d) rows due to data type incompatibility", s.Options.RejectLimit)}
		}
		return false, nil
	}
	accepted, err := exec.UpdateMatches(evaluation, s.Predicate)
	if err == nil && !accepted && s.Progress != nil {
		s.Progress.Excluded.Add(1)
	}
	return accepted, err
}

// Success is called only by the statement's final success owner, after every
// terminal/publication veto. Notices remain operation-buffered; never call a
// client handler from the decoder or while admission locks are held.
func (s *CopyFromState) Success(op *session.Operation) {
	if s == nil || s.Rejected == 0 || s.Options.LogVerbosity == copyfmt.LogSilent {
		return
	}
	message := fmt.Sprintf("%d rows were skipped due to data type incompatibility", s.Rejected)
	if s.Rejected == 1 {
		message = "1 row was skipped due to data type incompatibility"
	}
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
}
