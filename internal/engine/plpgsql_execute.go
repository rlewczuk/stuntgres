package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	result, returned, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
	if err != nil {
		return sqltype.Value{}, err
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
	if !returned {
		diagnostic := sqlError("2F005", "control reached end of function without RETURN").(*sqlstate.Error)
		diagnostic.Context = "PL/pgSQL function " + plRoutineDiagnosticIdentity(read, d)
		return sqltype.Value{}, diagnostic
	}
	return result, nil
}

func (s *StatementScope) executePLBlock(d *catalog.Procedure, f *proc.PLFrame, block *proc.PLBlock, read catalog.Read) (sqltype.Value, bool, error) {
	f.Enter(block)
	defer f.Leave(block)
	for _, initializer := range block.Declarations {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
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
			value, _, err = s.evaluatePLExpression(f, initializer.Query, read, nil)
			f.Scopes[len(f.Scopes)-1] = block
		} else {
			value = sqltype.Null(variable.Usage.Type.BaseType())
		}
		if err == nil {
			value, err = s.assignPLVariable(f, initializer.Slot, value)
		}
		if err != nil {
			return sqltype.Value{}, false, plRuntimeError(err, read, d, initializer.Expression, "during statement block local variable initialization", false)
		}
	}
	if len(block.Handlers) == 0 {
		return s.executePLStatements(d, f, block.Statements, read)
	}
	checkpoint, err := s.beginPLException()
	if err != nil {
		return sqltype.Value{}, false, err
	}
	value, returned, bodyErr := s.executePLStatements(d, f, block.Statements, read)
	if err := s.endPLException(checkpoint, bodyErr != nil, bodyErr); err != nil {
		return sqltype.Value{}, false, err
	}
	if bodyErr == nil {
		return value, returned, nil
	}
	var cleanup *plExceptionCleanupError
	var diagnostic *sqlstate.Error
	if errors.As(bodyErr, &cleanup) || !errors.As(bodyErr, &diagnostic) {
		return sqltype.Value{}, false, bodyErr
	}
	handler := block.MatchException(diagnostic)
	if handler == nil {
		return sqltype.Value{}, false, bodyErr
	}
	previous := f.EnterHandler(block.HandlerScope, diagnostic)
	defer f.LeaveHandler(block.HandlerScope, previous)
	return s.executePLStatements(d, f, handler.Statements, read)
}

func (s *StatementScope) executePLStatements(d *catalog.Procedure, f *proc.PLFrame, statements []proc.PLInstruction, read catalog.Read) (sqltype.Value, bool, error) {
	for _, instruction := range statements {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		if instruction.Block != nil {
			value, returned, err := s.executePLBlock(d, f, instruction.Block, read)
			if err != nil || returned {
				return value, returned, err
			}
			continue
		}
		statement := instruction.Syntax
		if statement.Rethrow {
			if diagnostic := f.HandledError(); diagnostic != nil {
				return sqltype.Value{}, false, diagnostic
			}
			diagnostic := sqlError("0Z002", "RAISE without parameters cannot be used outside an exception handler").(*sqlstate.Error)
			line := strings.Count(d.SQL.Source[:statement.Pos.Offset], "\n") + 1
			diagnostic.Context = fmt.Sprintf("PL/pgSQL function %s line %d at RAISE", plRoutineDiagnosticIdentity(read, d), line)
			return sqltype.Value{}, false, diagnostic
		}
		if statement.Return {
			if statement.Expression == nil {
				return sqltype.Value{}, true, nil
			}
			value, _, err := s.evaluatePLExpression(f, instruction.Query, read, nil)
			expressionError := err != nil
			if err == nil {
				value, err = s.convertPLValue(value, d.SQL.Result)
			}
			if err != nil {
				return sqltype.Value{}, false, plRuntimeError(err, read, d, statement.Expression, "at RETURN", expressionError)
			}
			return value, true, nil
		}
		if len(instruction.Target.Steps) != 0 {
			if _, err := s.assignPLTarget(f, instruction.Target, statement.Target, instruction.Query, read); err != nil {
				return sqltype.Value{}, false, plAssignmentError(err, read, d, statement)
			}
			continue
		}
		value, _, err := s.evaluatePLExpression(f, instruction.Query, read, nil)
		expressionError := err != nil
		if err == nil {
			value, err = s.assignPLVariable(f, instruction.Target.Slot, value)
		}
		if err != nil {
			var diagnostic *sqlstate.Error
			var domainError *plan.AssignmentDomainError
			if !expressionError && !errors.As(err, &domainError) && errors.As(err, &diagnostic) && strings.HasPrefix(diagnostic.Context, "SQL function ") {
				copy := *diagnostic
				start := statement.Pos.Offset
				end := parser.ExpressionStart(statement.Expression).Offset + len(parser.PLExpressionSource(d.SQL.Source, statement.Expression))
				copy.Context += "\nPL/pgSQL assignment \"" + strings.TrimSpace(d.SQL.Source[start:end]) + "\""
				err = sqlstate.WithDiagnostic(err, &copy)
			}
			if expressionError && errors.As(err, &diagnostic) {
				copy := *diagnostic
				start := statement.Pos.Offset
				end := parser.ExpressionStart(statement.Expression).Offset + len(parser.PLExpressionSource(d.SQL.Source, statement.Expression))
				assignment := strings.TrimSpace(d.SQL.Source[start:end])
				if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
					copy.InternalPosition = strconv.Itoa(n - statement.Pos.Character + 1)
					copy.InternalQuery, copy.Position = assignment, ""
				}
				var planning *plExpressionPlanningError
				if errors.As(err, &planning) && copy.Context == "" && copy.Code == "22012" {
					copy.Context = "PL/pgSQL assignment \"" + assignment + "\""
				}
				err = sqlstate.WithDiagnostic(err, &copy)
			}
			return sqltype.Value{}, false, plRuntimeError(err, read, d, statement.Expression, "at assignment", expressionError)
		}
	}
	return sqltype.Value{}, false, nil
}

type plExpressionPlanningError struct{ error }

func (e *plExpressionPlanningError) Unwrap() error { return e.error }

func (s *StatementScope) evaluatePLExpression(f *proc.PLFrame, statement *parser.Select, read catalog.Read, assignment *analyze.PLAssignment) (value sqltype.Value, present bool, err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical = f
	s.sqlRoutine.plPlanning = true
	s.sqlRoutine.assignment = assignment
	s.sqlRoutine.scalar = false
	admission := statement
	if assignment != nil && assignment.Raw != nil {
		copy := *statement
		copy.Targets = append(slices.Clone(statement.Targets), assignment.Raw)
		admission = &copy
	}
	if err = s.acquireRelation(admission, true); err != nil {
		return value, false, &plExpressionPlanningError{err}
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
			return value, false, &plExpressionPlanningError{err}
		}
		return value, false, err
	}
	if result.Rows == nil {
		return value, false, fmt.Errorf("stuntgres: PL expression has no output")
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
		return value, false, sqlError("42601", "query returned more than one column")
	}
	row := make([]sqltype.Value, 1)
	present, err = result.Rows.Next(s.op.Context(), row)
	if err != nil {
		return value, false, err
	}
	if !present {
		return sqltype.Null(schema[0].Usage.Type), false, nil
	}
	value = row[0]
	more, err := result.Rows.Next(s.op.Context(), row)
	if err != nil {
		return value, false, err
	}
	if more {
		return sqltype.Value{}, false, &plCardinalityError{sqlError("21000", "query returned more than one row")}
	}
	return value, true, nil
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}}
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
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}}
		evaluation.Input.DomainChecks = &evaluation
		if err := target.CheckDomainInput(s.op.Context(), candidate, evaluation.Input); err != nil {
			return sqltype.Value{}, &plan.AssignmentDomainError{Err: err}
		}
	}
	return target.Wrap(candidate)
}

func (s *StatementScope) assignPLVariable(f *proc.PLFrame, slot int, value sqltype.Value) (sqltype.Value, error) {
	variable := f.Program.Variables[slot]
	var err error
	if variable.Record {
		if value.IsNull() {
			value = sqltype.Value{}
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
	f.Values[slot] = value
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

func plRuntimeError(err error, read catalog.Read, d *catalog.Procedure, raw parser.Expr, action string, expression bool) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	copy := *original
	source := parser.PLExpressionSource(d.SQL.Source, raw)
	offset := 0
	characterOffset := 0
	if raw != nil {
		start := parser.ExpressionStart(raw)
		offset, characterOffset = start.Offset, start.Character-1
	}
	if expression && copy.Position != "" {
		if n, e := strconv.Atoi(copy.Position); e == nil {
			copy.InternalPosition = strconv.Itoa(n - characterOffset)
			copy.InternalQuery = source
			copy.Position = ""
		}
	}
	var planning *plExpressionPlanningError
	if expression && copy.Context == "" && (copy.Code == "21000" || errors.As(err, &planning) && (copy.Code == "55000" || copy.Code == "42703" && copy.InternalPosition == "" || copy.Code == "22012")) {
		copy.Context = "PL/pgSQL expression \"" + source + "\""
	}
	line := strings.Count(d.SQL.Source[:offset], "\n") + 1
	context := fmt.Sprintf("PL/pgSQL function %s line %d %s", plRoutineDiagnosticIdentity(read, d), line, action)
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return sqlstate.WithDiagnostic(err, &copy)
}
