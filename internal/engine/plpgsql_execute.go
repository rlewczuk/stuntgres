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
	"stuntgres/internal/engine/expr"
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

// Preserve failed expression analysis separately from execution. Trigger
// assignment diagnostics name the complete assignment on a binding failure,
// while a successfully bound arithmetic failure names only the PL statement.
type plExpressionBindingError struct{ error }

func (e *plExpressionBindingError) Unwrap() error { return e.error }

func (*plExpressionBindingError) plCallback() bool { return true }

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
			return sqltype.Value{}, false, plRuntimeError(err, read, d, initializer.Start, initializer.End, origin, "during statement block local variable initialization", false)
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
		if instruction.If != nil || instruction.Case != nil {
			value, returned, err := s.executePLConditional(d, f, instruction, read)
			if err != nil || returned {
				return value, returned, err
			}
			continue
		}
		if instruction.Cursor != nil {
			value, returned, err := s.executePLCursor(d, f, instruction, read)
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
				return sqltype.Value{}, false, plRuntimeError(err, read, d, statement.ExpressionStart, statement.ExpressionEnd, statement.Pos, "at RETURN", expressionError)
			}
			return value, true, nil
		}
		if len(instruction.Target.Steps) != 0 {
			if _, err := s.assignPLTarget(f, instruction.Target, statement.Target, instruction.Query, read); err != nil {
				return sqltype.Value{}, false, plAssignmentError(err, read, d, statement)
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
			query := plDiagnosticQuery{}
			triggerBinding := d.SQL.Trigger && plTriggerExpressionBinding(err)
			if !errors.As(err, &domainError) && errors.As(err, &diagnostic) && ((!expressionError || d.SQL.Trigger) && strings.HasPrefix(diagnostic.Context, "SQL function ") || triggerBinding) {
				callback := plAssignmentContext
				if triggerBinding {
					callback = plAssignmentBinding
				}
				query = plBodyDiagnostic(d.SQL.Source, statement.Pos, statement.ExpressionEnd, callback)
			} else if expressionError && !d.SQL.Trigger && plExpressionCallback(err) {
				query = plBodyDiagnostic(d.SQL.Source, statement.Pos, statement.ExpressionEnd, plAssignmentCallback)
			}
			return sqltype.Value{}, false, plRuntimeQueryError(err, read, d, statement.Pos, "at assignment", query)
		}
	}
	return sqltype.Value{}, false, nil
}

type plExpressionPlanningError struct{ error }

func (e *plExpressionPlanningError) Unwrap() error { return e.error }

func (*plExpressionPlanningError) plCallback() bool { return true }

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
		return Result{}, &plExpressionPlanningError{err}
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
			return Result{}, &plExpressionPlanningError{err}
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
		return sqltype.Value{}, usage, false, &plCardinalityError{sqlError("21000", "query returned more than one row")}
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

type plDiagnosticCallback uint8

const (
	plNoCallback plDiagnosticCallback = iota
	plExpressionCallbackKind
	plSQLCallback
	plAssignmentCallback
	plAssignmentContext // Existing semantic-datum projection/conversion frame, no SPI position mapping.
	plAssignmentBinding // Trigger's actual binder: preserve its ASSIGN frame and source mapping.
	plQueryDetail       // Existing scalar assignment cardinality's unquoted "query: " frame.
)

type plDiagnosticPosition struct{ Start, End, Output int }

// Text is diagnostic provenance, never executable syntax. Positions maps
// declaration-reordered actuals or a CASE operator into a synthesized query.
type plDiagnosticQuery struct {
	Text      string
	Offset    int
	Positions []plDiagnosticPosition
	Callback  plDiagnosticCallback
}

func plBodyDiagnostic(source string, start, end parser.Position, callback plDiagnosticCallback) plDiagnosticQuery {
	return plDiagnosticQuery{Text: parser.PLExpressionSource(source, start, end), Offset: start.Character - 1, Callback: callback}
}

func plRuntimeError(err error, read catalog.Read, d *catalog.Procedure, start, end, origin parser.Position, action string, expression bool) error {
	query := plDiagnosticQuery{}
	if expression && plExpressionCallback(err) && (!d.SQL.Trigger || plTriggerExpressionBinding(err)) {
		query = plBodyDiagnostic(d.SQL.Source, start, end, plExpressionCallbackKind)
	}
	return plRuntimeQueryError(err, read, d, origin, action, query)
}

func plRuntimeQueryError(err error, read catalog.Read, d *catalog.Procedure, origin parser.Position, action string, query plDiagnosticQuery) error {
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	line := strings.Count(d.SQL.Source[:origin.Offset], "\n") + 1
	context := fmt.Sprintf("PL/pgSQL function %s line %d %s", plRoutineDiagnosticIdentity(read, d), line, action)
	return plFormatError(err, query, context)
}

// This is the single SPI/query and PL action formatter. Eligibility is the
// actual producer phase and ORIGINAL positive Position, never SQLSTATE, a
// previously mapped InternalPosition, existing Context, or source-text guesses.
func plFormatError(err error, query plDiagnosticQuery, action string) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	next := *original
	if query.Callback != plNoCallback {
		position, _ := strconv.Atoi(original.Position)
		positioned := position > 0 && query.Callback != plAssignmentContext && query.Callback != plQueryDetail
		if positioned {
			mapped := position - query.Offset
			for _, span := range query.Positions {
				if position >= span.Start && position < span.End {
					mapped = position - span.Start + span.Output
					break
				}
			}
			next.InternalPosition, next.InternalQuery, next.Position = strconv.Itoa(mapped), query.Text, ""
		}
		if !positioned || query.Callback == plAssignmentBinding {
			prefix := "PL/pgSQL expression "
			switch query.Callback {
			case plSQLCallback:
				prefix = "SQL statement "
			case plAssignmentCallback, plAssignmentContext, plAssignmentBinding:
				prefix = "PL/pgSQL assignment "
			case plQueryDetail:
				prefix = "query: "
			}
			if next.Context != "" {
				next.Context += "\n"
			}
			if query.Callback == plQueryDetail {
				next.Context += prefix + query.Text
			} else {
				next.Context += prefix + "\"" + query.Text + "\""
			}
		}
	}
	if action != "" {
		if next.Context != "" {
			next.Context += "\n"
		}
		next.Context += action
	}
	return sqlstate.WithDiagnostic(err, &next)
}

// Every real handoff records the CURRENT route, including complex execution.
// An outer SPI/planning marker therefore shadows an inner callee's simple
// route instead of accidentally inheriting it through errors.As.
type plExpressionExecutionError struct {
	error
	simple bool
}

func (e *plExpressionExecutionError) Unwrap() error    { return e.error }
func (e *plExpressionExecutionError) plCallback() bool { return !e.simple }

func plExpressionCallback(err error) bool {
	var phase interface{ plCallback() bool }
	return !errors.As(err, &phase) || phase.plCallback()
}

// Only the current owner's binding boundary grants the trigger traceback.
// An inner trigger's binding failure under an outer execution/planning callback
// must not make that outer callback look like its own lexical binder.
func plTriggerExpressionBinding(err error) bool {
	var phase interface{ plCallback() bool }
	if !errors.As(err, &phase) {
		return false
	}
	switch current := phase.(type) {
	case *plExpressionBindingError:
		return true
	case *plExpressionPlanningError:
		_, binding := current.error.(*plExpressionBindingError)
		return binding
	default:
		return false
	}
}

func plSimpleExpression(root plan.Node) bool {
	project, ok := root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 || project.HasTargetSet {
		return false
	}
	if _, ok := project.Child.(*plan.SingletonNode); !ok {
		return false
	}
	simple := true
	plan.WalkExpression(project.Expressions[0], func(node expr.Expr) {
		if _, subquery := node.(plan.SubqueryExpr); subquery || node.Properties().StageDependent {
			simple = false
		}
	})
	return simple
}
