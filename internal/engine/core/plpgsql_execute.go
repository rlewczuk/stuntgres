package core

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// All three finite handler routes reach this entry, not a scalar
// pseudo-interface. The caller owns routine admission, depth and root operation;
// this entry publishes the routine's diagnostic identity around the walk.
func (s *StatementScope) invokePLRoutine(d *catalog.Procedure, input *proc.Frame, read catalog.Read, collation sqltype.CollationRef) (sqltype.Value, error) {
	identity, err := s.plDiagnosticIdentityFor(d, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	previousIdentity := s.sqlRoutine.plDiagnostic
	s.sqlRoutine.plDiagnostic = identity
	defer func() { s.sqlRoutine.plDiagnostic = previousIdentity }()
	return s.newPLRun(d, nil, read).Invoke(input, collation)
}

// Admission and the real executor handoff are shared by scalar expressions and
// bound-argument row projections. Destinations never participate in binding the
// argument projection, so every output is evaluated before formal conversion.
func (s *StatementScope) executePLExpressionQuery(f *proc.PLFrame, statement *parser.Select, assignment *analyze.PLAssignment) (Result, error) {
	// PL scalar cardinality is checked by its consumer, not SQL functions'
	// one-column first-row demand. Only plArgumentRow requests a first row.
	s.sqlRoutine.scalar = false
	s.sqlRoutine.plPlanning = true
	s.sqlRoutine.assignment = assignment
	admission := statement
	if assignment != nil && assignment.Raw != nil {
		copy := *statement
		copy.Targets = append(slices.Clone(statement.Targets), assignment.Raw)
		admission = &copy
	}
	if err := s.AcquireRelation(admission, true); err != nil {
		return Result{}, &proc.PLExpressionPlanningError{Err: err}
	}
	if admission != statement {
		s.QueryLockAdmission.SelectQuery = statement
		if root := s.QueryLockAdmission.Queries[admission]; root != nil {
			root.SelectQuery = statement
			s.QueryLockAdmission.Queries[statement] = root
		}
	}
	result, err := s.Engine().executeStatement(s, statement, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		for i := range dst {
			dst[i] = f.Parameter(i)
		}
		return nil
	})
	if err != nil {
		if s.sqlRoutine.plPlanning {
			return Result{}, &proc.PLExpressionPlanningError{Err: err}
		}
		return Result{}, err
	}
	return result, nil
}

func (s *StatementScope) evaluatePLExpression(f *proc.PLFrame, statement *parser.Select, read catalog.Read, assignment *analyze.PLAssignment, origin plNoticeOrigin) (value sqltype.Value, usage sqltype.Usage, present bool, err error) {
	noticeStart := len(s.Op.Notices)
	restore := s.ActivateSQLRoutine(nil, read)
	defer func() {
		err = restore(err)
		s.appendPLNoticeContext(noticeStart, origin)
	}()
	s.sqlRoutine.lexical = f
	result, err := s.executePLExpressionQuery(f, statement, assignment)
	if err != nil {
		return value, usage, false, err
	}
	if result.Rows == nil {
		return value, usage, false, fmt.Errorf("stuntgres: PL expression has no output")
	}
	defer func() {
		if closeErr := result.Rows.Close(); err == nil {
			err = closeErr
		}
	}()
	for _, rows := range result.RowSets {
		defer rows.Close()
	}
	schema := result.Rows.Schema()
	if len(schema) != 1 {
		return value, usage, false, SQLError("42601", "query returned more than one column")
	}
	usage = schema[0].Usage
	var row [1]sqltype.Value
	present, err = result.Rows.Next(s.executionContext(), row[:])
	if err != nil {
		return value, usage, false, err
	}
	if !present {
		return sqltype.Null(usage.Type), usage, false, nil
	}
	value = row[0]
	more, err := result.Rows.Next(s.executionContext(), row[:])
	if err != nil {
		return value, usage, false, err
	}
	if more {
		return sqltype.Value{}, usage, false, &proc.PLCardinalityError{Err: SQLError("21000", "query returned more than one row")}
	}
	return value, usage, true, nil
}

func (s *StatementScope) convertPLValue(value sqltype.Value, target sqltype.Usage) (sqltype.Value, error) {
	if !value.IsNull() && value.Type() == target.Type && target.Typmod < 0 && !target.Type.IsDomain() {
		return value, nil
	}
	if target.Type.BaseType().IsComposite() && (value.IsNull() || value.Type().BaseType().IsComposite()) {
		shape := target.Type.BaseType()
		if value.IsNull() {
			return s.finishPLComposite(sqltype.Null(shape), target.Type)
		}
		fields := make([]sqltype.Value, shape.CompositeLen())
		for i := range fields {
			source := sqltype.Null(shape.CompositeMember(i).Usage.Type.BaseType())
			if i < value.CompositeLen() {
				source = value.CompositeField(i)
			}
			converted, err := s.convertPLTargetValue(source, shape.CompositeMember(i).Usage)
			if err != nil {
				return sqltype.Value{}, err
			}
			fields[i] = converted
		}
		candidate, err := sqltype.OwnedCompositeValue(shape, fields)
		if err != nil {
			return sqltype.Value{}, err
		}
		return s.finishPLComposite(candidate, target.Type)
	}
	return s.convertPLTargetValue(value, target)
}

// Partial targets and destination row members use selected assignment casts
// and absent-path textual conversion, including composite-valued leaves.
func (s *StatementScope) convertPLTargetValue(value sqltype.Value, target sqltype.Usage) (sqltype.Value, error) {
	if !value.IsNull() && value.Type() == target.Type && target.Typmod < 0 && !target.Type.IsDomain() {
		return value, nil
	}
	if value.IsNull() {
		value = sqltype.Null(target.Type.BaseType())
	}
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return sqltype.Value{}, err
	}
	binder := NewExpressionBinder(s.executionContext(), e, s.Input, s.Op, nil)
	binder.Query.Auth = &auth
	node, selected, err := binder.PLAssignment(value, target)
	e.Mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.executionContext(), Host: HostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	evaluation.Input.DomainChecks = &evaluation
	if selected {
		return node.Eval(&evaluation)
	}
	text, err := value.Type().OutputWithEnvironment(s.executionContext(), value, s.Output)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Type.InputWithEnvironment(s.executionContext(), text, target.Typmod, evaluation.Input)
}

// The candidate has the physical base descriptor. Validate fresh reconstructed
// input before attaching a domain tag or publishing any activation slot.
func (s *StatementScope) finishPLComposite(candidate sqltype.Value, target *sqltype.Descriptor) (sqltype.Value, error) {
	if !target.IsDomain() {
		return candidate, nil
	}
	if target.HasDomainConstraints() {
		e := s.Engine()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			e.Mu.Unlock()
			return sqltype.Value{}, err
		}
		binder := NewExpressionBinder(s.executionContext(), e, s.Input, s.Op, nil)
		binder.Query.Auth = &auth
		err = binder.CaptureTypeDiagnosticInput(target)
		e.Mu.Unlock()
		if err != nil {
			return sqltype.Value{}, err
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.executionContext(), Host: HostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
		evaluation.Input.DomainChecks = &evaluation
		if err := target.CheckDomainInput(s.executionContext(), candidate, evaluation.Input); err != nil {
			return sqltype.Value{}, &plan.AssignmentDomainError{Err: err}
		}
	}
	return target.Wrap(candidate)
}

func (s *StatementScope) plRoutineDiagnosticIdentity(read catalog.Read, d *catalog.Procedure) string {
	if s.sqlRoutine.plDiagnostic != "" {
		return s.sqlRoutine.plDiagnostic
	}
	return formatPLDiagnosticIdentity(read, nil, nil, d)
}

// plIdentity defers the routine identity to the diagnostic formatter.
func (s *StatementScope) plIdentity(read catalog.Read, d *catalog.Procedure) func() string {
	return func() string { return s.plRoutineDiagnosticIdentity(read, d) }
}

func (s *StatementScope) plRuntimeError(err error, read catalog.Read, d *catalog.Procedure, start, end, origin parser.Position, action string, expression bool) error {
	return proc.PLRuntimeError(err, d.SQL, s.plIdentity(read, d), start, end, origin, action, expression)
}

func (s *StatementScope) plRuntimeQueryError(err error, read catalog.Read, d *catalog.Procedure, origin parser.Position, action string, query proc.PLDiagnosticQuery) error {
	return proc.PLRuntimeQueryError(err, d.SQL, s.plIdentity(read, d), origin, action, query)
}
