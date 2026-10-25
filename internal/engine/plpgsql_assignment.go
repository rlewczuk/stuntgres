package engine

import (
	"fmt"

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

// plTargetDatum retains one reached semantic datum and its detached publisher.
// Query assignments and fetched values share binding, authority and reconstruction.
type plTargetDatum struct {
	assignment analyze.PLAssignment
	current    sqltype.Value // May include earlier detached writes to the same slot.
	prefix     *plan.ArrayAssignmentTarget
	evaluation exec.Context
	expression expr.Expr
}

func (s *StatementScope) assignPLTarget(f *proc.PLFrame, checked proc.PLTarget, raw parser.Expr, query *parser.Select, read catalog.Read, origin plNoticeOrigin) (value sqltype.Value, err error) {
	noticeStart := len(s.Op.Notices)
	restore := s.activateSQLRoutine(nil, read)
	defer func() {
		err = restore(err)
		s.appendPLNoticeContext(noticeStart, origin)
	}()
	s.sqlRoutine.lexical = f
	prepared, err := s.preparePLTargetDatum(f, checked, f.Values[checked.Slot], raw, nil)
	if err != nil {
		return value, err
	}
	value, _, _, err = s.evaluatePLExpression(f, query, read, &prepared.assignment, plNoticeOrigin{})
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
func (s *StatementScope) preparePLTargetValue(run *proc.PLRun, f *proc.PLFrame, checked proc.PLTarget, value, current sqltype.Value, read catalog.Read) (candidate sqltype.Value, err error) {
	if len(checked.Steps) == 0 {
		return run.PrepareVariable(checked.Slot, value, proc.PLOrigin{})
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
		d := SQLError("55000", fmt.Sprintf("record %q is not assigned yet", variable.Name)).(*sqlstate.Error)
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
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	binder := NewExpressionBinder(s.Op.Context(), e, s.Input, s.Op, nil)
	binder.PL, binder.Query.Auth = f, &auth
	if err := binder.CaptureTypeDiagnosticInput(usage.Type); err != nil {
		e.Mu.Unlock()
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
	e.Mu.Unlock()
	if err != nil {
		return nil, &plan.PLAssignmentPlanningError{Err: err}
	}
	s.RoutineAuth = auth
	prepared.evaluation = exec.Context{EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: HostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
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
		return sqltype.Value{}, SQLError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	return value, nil
}
