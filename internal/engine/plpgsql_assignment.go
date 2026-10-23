package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

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

func (s *StatementScope) assignPLTarget(f *proc.PLFrame, checked proc.PLTarget, raw parser.Expr, query *parser.Select, read catalog.Read) (value sqltype.Value, err error) {
	variable := f.Program.Variables[checked.Slot]
	current := f.Values[checked.Slot]
	if current.Type() == nil {
		d := sqlError("55000", fmt.Sprintf("record %q is not assigned yet", variable.Name)).(*sqlstate.Error)
		d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
		return value, &plan.PLAssignmentPlanningError{Err: d}
	}
	usage := f.Usage(checked.Slot)
	if variable.Record {
		usage = current.Type().Usage()
	}
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical = f
	pos := raw.SourcePosition()
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return value, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.PL, binder.Query.Auth = f, &auth
	if err := binder.CaptureTypeDiagnosticInput(usage.Type); err != nil {
		e.mu.Unlock()
		return value, err
	}
	datum, datumUsage, steps := current, usage, checked.Steps
	var prefix *plan.ArrayAssignmentTarget
	if steps[0].Field != nil {
		// PL's leading RECFIELD is the assignment datum, not the whole slot.
		// Its detached publisher will reconstruct only this outer ancestor.
		prefix, err = binder.BindAssignmentTarget(catalog.Column{Name: variable.Name, Usage: usage}, steps[:1], pos)
		if err == nil {
			datumUsage = prefix.Destination
			index, ok := current.Type().BaseType().CompositeAttributeIndex(prefix.Attribute)
			if !ok {
				err = fmt.Errorf("stuntgres: retired PL assignment attribute")
			} else {
				datum = sqltype.Null(datumUsage.Type)
				if !current.IsNull() {
					datum = current.CompositeField(index)
				}
				steps = steps[1:]
				prefix.DomainErrorOrigin = true
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return value, &plan.PLAssignmentPlanningError{Err: err}
	}
	s.routineAuth = auth
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}}
	evaluation.Input.DomainChecks = &evaluation
	assignment := analyze.PLAssignment{Usage: datumUsage, Current: datum, Steps: steps, Name: variable.Name, Pos: pos, Raw: raw}
	value, _, err = s.evaluatePLExpression(f, query, read, &assignment)
	if err != nil {
		return sqltype.Value{}, err
	}
	if prefix != nil {
		// The query already converted/mutated the semantic datum. Never replay
		// the remaining path, its bounds, RHS or domain input at publication.
		value, err = prefix.Apply(&evaluation, current, value)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if variable.NotNull && value.IsNull() {
		return sqltype.Value{}, sqlError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	f.Values[checked.Slot] = value
	return value, nil
}

func plAssignmentError(err error, read catalog.Read, d *catalog.Procedure, statement *parser.PLStatement) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	copy := *original
	start := statement.Pos
	end := parser.ExpressionStart(statement.Expression).Offset + len(parser.PLExpressionSource(d.SQL.Source, statement.Expression))
	source := strings.TrimSpace(d.SQL.Source[start.Offset:end])
	var planning *plan.PLAssignmentPlanningError
	var domain *plan.AssignmentDomainError
	var projection *plan.PLAssignmentProjectionError
	var cardinality *plCardinalityError
	if errors.As(err, &cardinality) {
		if copy.Context != "" {
			copy.Context += "\n"
		}
		copy.Context += "query: " + source
	} else if (errors.As(err, &planning) && !errors.As(err, &domain)) || errors.As(err, &projection) {
		if planning != nil && copy.Position != "" {
			if n, e := strconv.Atoi(copy.Position); e == nil {
				copy.InternalPosition = strconv.Itoa(n - start.Character + 1)
				copy.InternalQuery = source
				copy.Position = ""
			}
		} else {
			if copy.Context != "" {
				copy.Context += "\n"
			}
			copy.Context += "PL/pgSQL assignment \"" + source + "\""
		}
	}
	line := strings.Count(d.SQL.Source[:start.Offset], "\n") + 1
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += fmt.Sprintf("PL/pgSQL function %s line %d at assignment", plRoutineDiagnosticIdentity(read, d), line)
	return sqlstate.WithDiagnostic(err, &copy)
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
