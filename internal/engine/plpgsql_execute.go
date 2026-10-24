package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// All three finite handler routes reach this executor/compiler, not a scalar
// pseudo-interface. The caller owns routine admission, depth and root operation.
func (s *StatementScope) invokePLRoutine(d *catalog.Procedure, input *proc.Frame, read catalog.Read, collation sqltype.CollationRef) (sqltype.Value, error) {
	if d.SQL.PL == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing checked PL program")
	}
	if collation.ID != 0 && !collation.Valid() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid PL input collation")
	}
	f := proc.NewPLFrame(d.SQL.PL, d.SQL, input, collation)
	defer f.Close()
	result, flow, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	if flow.Kind == proc.PLExit && flow.Target == d.SQL.PL.Root.Control {
		diagnostic := sqlError("2F005", "control reached end of function without RETURN").(*sqlstate.Error)
		diagnostic.Context = "PL/pgSQL function " + plRoutineDiagnosticIdentity(read, d)
		return sqltype.Value{}, diagnostic
	}
	if flow.Kind != proc.PLNormal && flow.Kind != proc.PLReturn {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unconsumed PL control transfer")
	}
	if d.SQL.PL.Procedure {
		return sqltype.Value{}, nil
	}
	if len(d.SQL.OutputFormals) == 1 {
		return f.Values[d.SQL.OutputFormals[0]], nil
	}
	if d.SQL.Void() {
		return sqltype.VoidValue(d.SQL.Result.Type), nil
	}
	if flow.Kind != proc.PLReturn {
		diagnostic := sqlError("2F005", "control reached end of function without RETURN").(*sqlstate.Error)
		diagnostic.Context = "PL/pgSQL function " + plRoutineDiagnosticIdentity(read, d)
		return sqltype.Value{}, diagnostic
	}
	return result, nil
}

func (s *StatementScope) executePLBlock(d *catalog.Procedure, f *proc.PLFrame, block *proc.PLBlock, read catalog.Read) (value sqltype.Value, flow proc.PLFlow, err error) {
	f.Enter(block)
	defer f.Leave(block)
	defer func() {
		if err == nil && flow.Kind == proc.PLExit && flow.Target == block.Control && block.Control != nil && !block.Control.Loop && !block.FunctionScope {
			flow = proc.PLFlow{}
		}
	}()
	for _, initializer := range block.Declarations {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		variable := f.Program.Variables[initializer.Slot]
		f.Values[initializer.Slot] = sqltype.Value{}
		if variable.Record && initializer.Expression == nil {
			continue
		}
		value := sqltype.Value{}
		var err error
		if initializer.Expression != nil {
			f.Scopes[len(f.Scopes)-1] = initializer.Scope
			value, _, _, err = s.evaluatePLExpression(f, initializer.Query, read, nil)
			f.Scopes[len(f.Scopes)-1] = block
		} else {
			value = sqltype.Null(variable.Usage.Type.BaseType())
		}
		if err == nil {
			value, err = s.assignPLVariable(f, initializer.Slot, value)
		}
		if err != nil {
			origin := block.Syntax.Pos
			if initializer.Expression != nil {
				origin = initializer.Start
			}
			return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, initializer.Start, initializer.End, origin, "during statement block local variable initialization", false)
		}
	}
	if len(block.Handlers) == 0 {
		return s.executePLStatements(d, f, block.Statements, read)
	}
	checkpoint, err := s.beginPLException()
	if err != nil {
		return sqltype.Value{}, proc.PLFlow{}, err
	}
	value, flow, bodyErr := s.executePLStatements(d, f, block.Statements, read)
	if err := s.endPLException(checkpoint, bodyErr != nil, bodyErr); err != nil {
		return sqltype.Value{}, proc.PLFlow{}, err
	}
	if bodyErr == nil {
		return value, flow, nil
	}
	var cleanup *proc.PLExceptionCleanupError
	var diagnostic *sqlstate.Error
	if errors.As(bodyErr, &cleanup) || !errors.As(bodyErr, &diagnostic) {
		return sqltype.Value{}, proc.PLFlow{}, bodyErr
	}
	handler := block.MatchException(diagnostic)
	if handler == nil {
		return sqltype.Value{}, proc.PLFlow{}, bodyErr
	}
	previous := f.EnterHandler(block.HandlerScope, diagnostic)
	defer f.LeaveHandler(block.HandlerScope, previous)
	return s.executePLStatements(d, f, handler.Statements, read)
}

func (s *StatementScope) executePLStatements(d *catalog.Procedure, f *proc.PLFrame, statements []proc.PLInstruction, read catalog.Read) (sqltype.Value, proc.PLFlow, error) {
	for _, instruction := range statements {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		if instruction.Block != nil {
			value, flow, err := s.executePLBlock(d, f, instruction.Block, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		if instruction.If != nil || instruction.Case != nil {
			value, flow, err := s.executePLConditional(d, f, instruction, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		if instruction.QueryLoop != nil {
			value, flow, err := s.forPLQuery(d, f, instruction, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		if instruction.Cursor != nil {
			value, flow, err := s.executePLCursor(d, f, instruction, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		if instruction.Loop != nil {
			value, flow, err := s.executePLLoop(d, f, instruction.Loop, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		if instruction.Transfer != nil {
			value, flow, err := s.executePLTransfer(d, f, instruction, read)
			if err != nil || flow.Kind != proc.PLNormal {
				return value, flow, err
			}
			continue
		}
		statement := instruction.Syntax
		if statement.Rethrow {
			if diagnostic := f.HandledError(); diagnostic != nil {
				return sqltype.Value{}, proc.PLFlow{}, diagnostic
			}
			diagnostic := sqlError("0Z002", "RAISE without parameters cannot be used outside an exception handler").(*sqlstate.Error)
			line := strings.Count(d.SQL.Source[:statement.Pos.Offset], "\n") + 1
			diagnostic.Context = fmt.Sprintf("PL/pgSQL function %s line %d at RAISE", plRoutineDiagnosticIdentity(read, d), line)
			return sqltype.Value{}, proc.PLFlow{}, diagnostic
		}
		if statement.Return {
			if statement.Expression == nil {
				return sqltype.Value{}, proc.PLFlow{Kind: proc.PLReturn}, nil
			}
			value, _, _, err := s.evaluatePLExpression(f, instruction.Query, read, nil)
			expressionError := err != nil
			if err == nil {
				if d.SQL.Trigger {
					if !value.IsNull() && (value.Type() == nil || !value.Type().BaseType().IsComposite()) {
						err = sqlError("42804", "cannot return non-composite value from function returning composite type")
					}
				} else {
					value, err = s.convertPLValue(value, d.SQL.Result)
				}
			}
			if err != nil {
				return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, statement.ExpressionStart, statement.ExpressionEnd, statement.Pos, "at RETURN", expressionError)
			}
			return value, proc.PLFlow{Kind: proc.PLReturn}, nil
		}
		if len(instruction.Target.Steps) != 0 {
			if _, err := s.assignPLTarget(f, instruction.Target, statement.Target, instruction.Query, read); err != nil {
				return sqltype.Value{}, proc.PLFlow{}, proc.PLAssignmentError(err, d.SQL, plIdentity(read, d), statement)
			}
			continue
		}
		value, _, _, err := s.evaluatePLExpression(f, instruction.Query, read, nil)
		expressionError := err != nil
		if err == nil {
			value, err = s.assignPLVariable(f, instruction.Target.Slot, value)
		}
		if err != nil {
			var diagnostic *sqlstate.Error
			var domainError *plan.AssignmentDomainError
			query := proc.PLDiagnosticQuery{}
			triggerBinding := d.SQL.Trigger && proc.PLTriggerExpressionBinding(err)
			if !errors.As(err, &domainError) && errors.As(err, &diagnostic) && ((!expressionError || d.SQL.Trigger) && strings.HasPrefix(diagnostic.Context, "SQL function ") || triggerBinding) {
				callback := proc.PLAssignmentContext
				if triggerBinding {
					callback = proc.PLAssignmentBinding
				}
				query = proc.PLBodyDiagnostic(d.SQL.Source, statement.Pos, statement.ExpressionEnd, callback)
			} else if expressionError && !d.SQL.Trigger && proc.PLExpressionCallback(err) {
				query = proc.PLBodyDiagnostic(d.SQL.Source, statement.Pos, statement.ExpressionEnd, proc.PLAssignmentCallback)
			}
			return sqltype.Value{}, proc.PLFlow{}, plRuntimeQueryError(err, read, d, statement.Pos, "at assignment", query)
		}
	}
	return sqltype.Value{}, proc.PLFlow{}, nil
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
	if err := s.acquireRelation(admission, true); err != nil {
		return Result{}, &proc.PLExpressionPlanningError{Err: err}
	}
	if admission != statement {
		s.queryLockAdmission.SelectQuery = statement
		if root := s.queryLockAdmission.Queries[admission]; root != nil {
			root.SelectQuery = statement
			s.queryLockAdmission.Queries[statement] = root
		}
	}
	result, err := s.engine().executeStatement(s, statement, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, f.Values[:f.Program.FormalCount])
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

func (s *StatementScope) evaluatePLExpression(f *proc.PLFrame, statement *parser.Select, read catalog.Read, assignment *analyze.PLAssignment) (value sqltype.Value, usage sqltype.Usage, present bool, err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
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
		return value, usage, false, sqlError("42601", "query returned more than one column")
	}
	usage = schema[0].Usage
	var row [1]sqltype.Value
	present, err = result.Rows.Next(s.op.Context(), row[:])
	if err != nil {
		return value, usage, false, err
	}
	if !present {
		return sqltype.Null(usage.Type), usage, false, nil
	}
	value = row[0]
	more, err := result.Rows.Next(s.op.Context(), row[:])
	if err != nil {
		return value, usage, false, err
	}
	if more {
		return sqltype.Value{}, usage, false, &proc.PLCardinalityError{Err: sqlError("21000", "query returned more than one row")}
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
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return sqltype.Value{}, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.Query.Auth = &auth
	node, selected, err := binder.PLAssignment(value, target)
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	evaluation.Input.DomainChecks = &evaluation
	if selected {
		return node.Eval(&evaluation)
	}
	text, err := value.Type().OutputWithEnvironment(s.op.Context(), value, s.output)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Type.InputWithEnvironment(s.op.Context(), text, target.Typmod, evaluation.Input)
}

// The candidate has the physical base descriptor. Validate fresh reconstructed
// input before attaching a domain tag or publishing any activation slot.
func (s *StatementScope) finishPLComposite(candidate sqltype.Value, target *sqltype.Descriptor) (sqltype.Value, error) {
	if !target.IsDomain() {
		return candidate, nil
	}
	if target.HasDomainConstraints() {
		e := s.engine()
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err != nil {
			e.mu.Unlock()
			return sqltype.Value{}, err
		}
		binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		binder.Query.Auth = &auth
		err = binder.CaptureTypeDiagnosticInput(target)
		e.mu.Unlock()
		if err != nil {
			return sqltype.Value{}, err
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
		evaluation.Input.DomainChecks = &evaluation
		if err := target.CheckDomainInput(s.op.Context(), candidate, evaluation.Input); err != nil {
			return sqltype.Value{}, &plan.AssignmentDomainError{Err: err}
		}
	}
	return target.Wrap(candidate)
}

func (s *StatementScope) assignPLVariable(f *proc.PLFrame, slot int, value sqltype.Value) (sqltype.Value, error) {
	value, err := s.preparePLVariable(f, slot, value)
	if err == nil {
		f.Values[slot] = value
	}
	return value, err
}

// Whole-slot preparation is detached; its caller owns atomic publication.
func (s *StatementScope) preparePLVariable(f *proc.PLFrame, slot int, value sqltype.Value) (sqltype.Value, error) {
	variable := f.Program.Variables[slot]
	var err error
	if variable.Record {
		if value.IsNull() {
			if f.Program.Trigger && slot < 2 {
				value = sqltype.Null(f.Values[slot].Type())
			} else {
				value = sqltype.Value{}
			}
		} else if value.Type() == nil || !value.Type().BaseType().IsComposite() {
			return sqltype.Value{}, sqlError("0A000", "input of anonymous composite types is not implemented")
		}
	} else {
		value, err = s.convertPLValue(value, f.Usage(slot))
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if variable.NotNull && (value.Type() == nil || value.IsNull()) {
		return sqltype.Value{}, sqlError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	return value, nil
}

func plRoutineDiagnosticIdentity(read catalog.Read, d *catalog.Procedure) string {
	if d.Name() == "inline_code_block" && d.Address().ObjectID == 0 {
		return "inline_code_block"
	}
	var text strings.Builder
	text.WriteString(catalog.DependencyIdentifier(d.Name()))
	text.WriteByte('(')
	regobj.WriteRoutineArguments(&text, read, nil, d.Signature, true)
	text.WriteByte(')')
	return text.String()
}

// plIdentity defers the routine identity to the diagnostic formatter.
func plIdentity(read catalog.Read, d *catalog.Procedure) func() string {
	return func() string { return plRoutineDiagnosticIdentity(read, d) }
}

func plRuntimeError(err error, read catalog.Read, d *catalog.Procedure, start, end, origin parser.Position, action string, expression bool) error {
	return proc.PLRuntimeError(err, d.SQL, plIdentity(read, d), start, end, origin, action, expression)
}

func plRuntimeQueryError(err error, read catalog.Read, d *catalog.Procedure, origin parser.Position, action string, query proc.PLDiagnosticQuery) error {
	return proc.PLRuntimeQueryError(err, d.SQL, plIdentity(read, d), origin, action, query)
}
