package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// routineCommandPolicy borrows the calling query's immutable visibility roots.
// Its lexical lifetime ends before the caller releases its view. It contains no
// authorization: every reached command acquires the effective actor afresh.
type routineCommandPolicy struct {
	active   bool
	readOnly bool
	base     *visibility.Data
	writes   map[ids.ObjectAddress][]storage.RowVersion
	deletes  map[ids.ObjectAddress]*visibility.PrivateDeletes
}

func (s *StatementScope) activateRoutineCommandPolicy(d *catalog.Procedure) func() {
	previous := s.sqlRoutine.policy
	policy := routineCommandPolicy{active: true, readOnly: d.Volatility != expr.Volatile, writes: s.OwnWrites, deletes: s.OwnDeletes}
	if s.View != nil {
		policy.base = s.View.Load()
	}
	s.sqlRoutine.policy = policy
	return func() { s.sqlRoutine.policy = previous }
}

// Optimizable commands must reach analysis before the readonly decision; in
// particular row marks and modifying CTEs belong to the executable binding.
func (s *StatementScope) routineUtilityEligibility(statement parser.Statement) error {
	if !s.sqlRoutine.policy.readOnly {
		return nil
	}
	switch statement.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return nil
	}
	return SQLError("0A000", RoutineCommandTag(statement)+" is not allowed in a non-volatile function")
}

func (s *StatementScope) routineReadOnlyEligibility(statement parser.Statement, read catalog.Read, sources []exec.Leaf, query *plan.Query) error {
	if !s.sqlRoutine.readOnlySPI && !s.sqlRoutine.policy.readOnly {
		return nil
	}
	if tag := routineReadOnlyViolation(statement, read, sources, query); tag != "" {
		return SQLError("0A000", tag+" is not allowed in a non-volatile function")
	}
	return nil
}
