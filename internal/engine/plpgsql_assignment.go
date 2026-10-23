package engine

import (
	"errors"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type plCardinalityError struct{ error }

func (e *plCardinalityError) Unwrap() error { return e.error }

// plTargetDatum retains one reached semantic datum and its detached publisher.
// Query assignments and fetched values share binding, authority and reconstruction.
type plTargetDatum struct {
	assignment analyze.PLAssignment
	current    sqltype.Value // May include earlier detached writes to the same slot.
	prefix     *plan.ArrayAssignmentTarget
	evaluation exec.Context
	expression expr.Expr
}

func (s *StatementScope) assignPLTarget(f *proc.PLFrame, checked proc.PLTarget, raw parser.Expr, query *parser.Select, read catalog.Read) (value sqltype.Value, err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical = f
	prepared, err := s.preparePLTargetDatum(f, checked, f.Values[checked.Slot], raw, nil)
	if err != nil {
		return value, err
	}
	value, _, _, err = s.evaluatePLExpression(f, query, read, &prepared.assignment)
	if err == nil {
		value, err = finishPLTarget(f, checked.Slot, prepared, value)
	}
	if err == nil {
		f.Values[checked.Slot] = value
	}
	return value, err
}

// preparePLTargetValue never publishes activation values. Bounds execute before
// the selected leaf conversion, and each ancestor is reconstructed exactly once.
func (s *StatementScope) preparePLTargetValue(f *proc.PLFrame, checked proc.PLTarget, value, current sqltype.Value, read catalog.Read) (candidate sqltype.Value, err error) {
	if len(checked.Steps) == 0 {
		return s.preparePLVariable(f, checked.Slot, value)
	}
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical = f
	prepared, err := s.preparePLTargetDatum(f, checked, current, nil, &value)
	if err != nil {
		return candidate, err
	}
	if err = exec.AdmitExpression(&prepared.evaluation, prepared.expression, prepared.evaluation.Catalog); err != nil {
		return candidate, err
	}
	candidate, err = prepared.expression.Eval(&prepared.evaluation)
	if err == nil {
		candidate, err = finishPLTarget(f, checked.Slot, prepared, candidate)
	}
	return candidate, err
}

func (s *StatementScope) preparePLTargetDatum(f *proc.PLFrame, checked proc.PLTarget, current sqltype.Value, raw parser.Expr, rhs *sqltype.Value) (*plTargetDatum, error) {
	variable := f.Program.Variables[checked.Slot]
	if current.Type() == nil {
		d := sqlError("55000", fmt.Sprintf("record %q is not assigned yet", variable.Name)).(*sqlstate.Error)
		d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
		return nil, &plan.PLAssignmentPlanningError{Err: d}
	}
	usage := f.Usage(checked.Slot)
	if variable.Record {
		usage = current.Type().Usage()
	}
	pos := parser.Position{}
	if raw != nil {
		pos = raw.SourcePosition()
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.PL, binder.Query.Auth = f, &auth
	if err := binder.CaptureTypeDiagnosticInput(usage.Type); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	prepared := &plTargetDatum{current: current, assignment: analyze.PLAssignment{Usage: usage, Current: current, Steps: checked.Steps, Name: variable.Name, Pos: pos, Raw: raw}}
	if checked.Steps[0].Field != nil {
		// Leading RECFIELD is the semantic datum; publish only this ancestor.
		prepared.prefix, err = binder.BindAssignmentTarget(catalog.Column{Name: variable.Name, Usage: usage}, checked.Steps[:1], pos)
		if err == nil {
			prepared.assignment.Usage = prepared.prefix.Destination
			index, ok := current.Type().BaseType().CompositeAttributeIndex(prepared.prefix.Attribute)
			if !ok {
				err = fmt.Errorf("stuntgres: retired PL assignment attribute")
			} else {
				prepared.assignment.Current = sqltype.Null(prepared.assignment.Usage.Type)
				if !current.IsNull() {
					prepared.assignment.Current = current.CompositeField(index)
				}
				prepared.assignment.Steps = checked.Steps[1:]
				prepared.prefix.DomainErrorOrigin = true
			}
		}
	}
	if err == nil && rhs != nil {
		child := plan.PLLocalExpr{Info: binder.Info(rhs.Type().Usage(), pos, expr.Properties{ParameterDependent: true}), Value: *rhs}
		prepared.expression, err = binder.PLAssignmentTargetExpression(child, &prepared.assignment)
	}
	e.mu.Unlock()
	if err != nil {
		return nil, &plan.PLAssignmentPlanningError{Err: err}
	}
	s.routineAuth = auth
	prepared.evaluation = exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	prepared.evaluation.Input.DomainChecks = &prepared.evaluation
	return prepared, nil
}

func finishPLTarget(f *proc.PLFrame, slot int, prepared *plTargetDatum, value sqltype.Value) (sqltype.Value, error) {
	if prepared.prefix != nil {
		var err error
		value, err = prepared.prefix.Apply(&prepared.evaluation, prepared.current, value)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	variable := f.Program.Variables[slot]
	if variable.NotNull && value.IsNull() {
		return sqltype.Value{}, sqlError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	return value, nil
}

func plAssignmentError(err error, read catalog.Read, d *catalog.Procedure, statement *parser.PLStatement) error {
	callback := plNoCallback
	var planning *plan.PLAssignmentPlanningError
	var domain *plan.AssignmentDomainError
	var projection *plan.PLAssignmentProjectionError
	var cardinality *plCardinalityError
	switch {
	case errors.As(err, &cardinality):
		callback = plQueryDetail
	case errors.As(err, &planning) && !errors.As(err, &domain) || errors.As(err, &projection):
		callback = plAssignmentContext
		if planning != nil {
			callback = plAssignmentCallback
		}
	}
	query := plDiagnosticQuery{}
	if callback != plNoCallback {
		query = plBodyDiagnostic(d.SQL.Source, statement.Pos, statement.ExpressionEnd, callback)
	}
	return plRuntimeQueryError(err, read, d, statement.Pos, "at assignment", query)
}

// Only the execution-private projection borrows the caller's diagnostic origin.
// The checked SQL body and its analyzed dependencies remain unchanged.
func plAssignmentProjection(root plan.Node) (plan.Node, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: invalid SQL routine assignment projection")
	}
	copy := *project
	copy.Expressions = slices.Clone(project.Expressions)
	for i, child := range copy.Expressions {
		copy.Expressions[i] = plan.PLAssignmentExpr{Info: expr.InfoOf(child), Child: child}
	}
	return &copy, nil
}
