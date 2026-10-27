package proc

import (
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const plInitializerAction = "during statement block local variable initialization"

// Invoke runs an ordinary routine or DO body in a fresh frame. The host owns
// routine admission, depth, the diagnostic identity and the root operation.
func (r *PLRun) Invoke(input *Frame, collation sqltype.CollationRef) (sqltype.Value, error) {
	d := r.Definition
	if d.PL == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing checked PL program")
	}
	if collation.ID != 0 && !collation.Valid() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid PL input collation")
	}
	f := NewPLFrame(d.PL, d, input, collation)
	defer f.Close()
	r.Frame = f
	result, flow, err := r.Block(d.PL.Root)
	if err != nil {
		return sqltype.Value{}, err
	}
	if flow.Kind == PLExit && flow.Target == d.PL.Root.Control {
		return sqltype.Value{}, r.missingReturn()
	}
	if flow.Kind != PLNormal && flow.Kind != PLReturn {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unconsumed PL control transfer")
	}
	if d.PL.Procedure {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, err
		}
		if len(d.OutputFormals) != 0 {
			fields := make([]sqltype.Value, len(d.OutputFormals))
			for i, formal := range d.OutputFormals {
				fields[i] = f.Values[formal]
			}
			return sqltype.OwnedCompositeValue(d.Result.Type, fields)
		}
		return sqltype.Value{}, nil
	}
	if len(d.OutputFormals) == 1 {
		if d.Result.Type.IsDeferredRecord() {
			return sqltype.RecordValue(d.Result.Type, f.Values[d.OutputFormals[0]])
		}
		return f.Values[d.OutputFormals[0]], nil
	}
	if d.Void() {
		return sqltype.VoidValue(d.Result.Type), nil
	}
	if flow.Kind != PLReturn {
		return sqltype.Value{}, r.missingReturn()
	}
	return result, nil
}

func (r *PLRun) missingReturn() error {
	diagnostic := plError("2F005", "control reached end of function without RETURN")
	diagnostic.Context = "PL/pgSQL function " + r.Host.Identity()
	return diagnostic
}

// Trigger runs a trigger body in the caller's trigger frame and validates its
// returned row against the relation's current row type.
func (r *PLRun) Trigger(rowType sqltype.ID) (sqltype.Value, error) {
	result, flow, err := r.Block(r.Definition.PL.Root)
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := "PL/pgSQL function " + r.Host.Identity()
	if flow.Kind != PLReturn {
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2F005", Message: "control reached end of trigger procedure without RETURN", Context: identity}
	}
	shape, ok := r.Host.LookupType(rowType)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: trigger row type disappeared")
	}
	result, err = TriggerReturn(shape, result)
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		copy.Context = identity + " during function exit"
		err = &copy
	}
	return result, err
}

// Block runs one lexical block: initializers before the exception boundary,
// then the body, then at most one matching handler outside the failed mark.
func (r *PLRun) Block(block *PLBlock) (value sqltype.Value, flow PLFlow, err error) {
	f := r.Frame
	f.Enter(block)
	defer f.Leave(block)
	defer func() {
		if err == nil && flow.Kind == PLExit && flow.Target == block.Control && block.Control != nil && !block.Control.Loop && !block.FunctionScope {
			flow = PLFlow{}
		}
	}()
	for _, initializer := range block.Declarations {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		variable := f.Program.Variables[initializer.Slot]
		f.Values[initializer.Slot] = sqltype.Value{}
		if variable.Record && initializer.Expression == nil {
			continue
		}
		value := sqltype.Value{}
		var err error
		origin := block.Syntax.Pos
		if initializer.Expression != nil {
			origin = initializer.Start
			f.Scopes[len(f.Scopes)-1] = initializer.Scope
			value, _, err = r.Host.Evaluate(f, initializer.Query, PLOrigin{Position: initializer.Start, Action: plInitializerAction})
			f.Scopes[len(f.Scopes)-1] = block
		} else {
			value = sqltype.Null(variable.Usage.Type.BaseType())
		}
		if err == nil {
			_, err = r.AssignVariable(initializer.Slot, value, PLOrigin{Position: origin, Action: plInitializerAction})
		}
		if err != nil {
			return sqltype.Value{}, PLFlow{}, PLRuntimeError(err, r.Definition, r.Host.Identity, initializer.Start, initializer.End, origin, plInitializerAction, false)
		}
	}
	if len(block.Handlers) == 0 {
		return r.Statements(block.Statements)
	}
	checkpoint, err := r.Host.BeginException()
	if err != nil {
		return sqltype.Value{}, PLFlow{}, err
	}
	value, flow, bodyErr := r.Statements(block.Statements)
	if err := r.Host.EndException(checkpoint, bodyErr != nil, bodyErr); err != nil {
		return sqltype.Value{}, PLFlow{}, err
	}
	if bodyErr == nil {
		return value, flow, nil
	}
	var cleanup *PLExceptionCleanupError
	var diagnostic *sqlstate.Error
	if errors.As(bodyErr, &cleanup) || !errors.As(bodyErr, &diagnostic) {
		return sqltype.Value{}, PLFlow{}, bodyErr
	}
	handler := block.MatchException(diagnostic)
	if handler == nil {
		return sqltype.Value{}, PLFlow{}, bodyErr
	}
	previous := f.EnterHandler(block.HandlerScope, diagnostic)
	defer f.LeaveHandler(block.HandlerScope, previous)
	return r.Statements(handler.Statements)
}

// Statements runs a statement list in the current lexical scope until a
// non-normal flow or an error.
func (r *PLRun) Statements(statements []PLInstruction) (sqltype.Value, PLFlow, error) {
	f := r.Frame
	for _, instruction := range statements {
		if err := r.Host.Context().Err(); err != nil {
			return sqltype.Value{}, PLFlow{}, err
		}
		var value sqltype.Value
		var flow PLFlow
		var err error
		switch {
		case instruction.Block != nil:
			value, flow, err = r.Block(instruction.Block)
		case instruction.If != nil || instruction.Case != nil:
			value, flow, err = r.conditional(instruction)
		case instruction.IntegerFor != nil:
			value, flow, err = r.executePLIntegerFor(instruction)
		case instruction.Foreach != nil:
			value, flow, err = r.foreach(instruction)
		case instruction.SQL != nil:
			err = r.Host.Command(r, f, instruction)
		case instruction.Into != nil:
			err = r.Host.Capture(r, f, instruction)
		case instruction.QueryLoop != nil:
			value, flow, err = r.forQuery(instruction)
		case instruction.Cursor != nil:
			value, flow, err = r.Host.Cursor(r, f, instruction)
		case instruction.Loop != nil || instruction.While != nil:
			value, flow, err = r.executePLLoop(instruction)
		case instruction.Transfer != nil:
			value, flow, err = r.transfer(instruction)
		case instruction.Raise != nil || instruction.Assert != nil:
			err = r.Host.Message(f, instruction)
		case instruction.Syntax.Rethrow:
			err = r.rethrow(instruction)
		case instruction.Syntax.Return:
			if value, err = r.returnValue(instruction); err == nil {
				flow = PLFlow{Kind: PLReturn}
			}
		default:
			err = r.assign(instruction)
		}
		if err != nil || flow.Kind != PLNormal {
			return value, flow, err
		}
	}
	return sqltype.Value{}, PLFlow{}, nil
}

func (r *PLRun) rethrow(instruction PLInstruction) error {
	if diagnostic := r.Frame.HandledError(); diagnostic != nil {
		return diagnostic
	}
	diagnostic := plError("0Z002", "RAISE without parameters cannot be used outside an exception handler")
	line := strings.Count(r.Definition.Source[:instruction.Syntax.Pos.Offset], "\n") + 1
	diagnostic.Context = fmt.Sprintf("PL/pgSQL function %s line %d at RAISE", r.Host.Identity(), line)
	return diagnostic
}

func (r *PLRun) returnValue(instruction PLInstruction) (sqltype.Value, error) {
	statement := instruction.Syntax
	if statement.Expression == nil {
		return sqltype.Value{}, nil
	}
	at := PLOrigin{Position: statement.Pos, Action: "at RETURN"}
	value, _, err := r.Host.Evaluate(r.Frame, instruction.Query, at)
	expressionError := err != nil
	if err == nil {
		if r.Definition.Invocation == DMLTriggerInvocation || r.Definition.Result.Type.ID() == 2249 {
			if !value.IsNull() && (value.Type() == nil || !value.Type().BaseType().IsComposite()) {
				err = plError("42804", "cannot return non-composite value from function returning composite type")
			}
			if err == nil && r.Definition.Result.Type.IsDeferredRecord() {
				value, err = sqltype.RecordValue(r.Definition.Result.Type, value)
			}
		} else {
			value, err = r.Host.Convert(value, r.Definition.Result, at)
		}
	}
	if err != nil {
		return sqltype.Value{}, PLRuntimeError(err, r.Definition, r.Host.Identity, statement.ExpressionStart, statement.ExpressionEnd, statement.Pos, "at RETURN", expressionError)
	}
	return value, nil
}

func (r *PLRun) assign(instruction PLInstruction) error {
	f, d, statement := r.Frame, r.Definition, instruction.Syntax
	at := PLOrigin{Position: statement.Pos, Action: "at assignment"}
	if len(instruction.Target.Steps) != 0 {
		if err := r.Host.AssignTarget(f, instruction.Target, statement.Target, instruction.Query, at); err != nil {
			return PLAssignmentError(err, d, r.Host.Identity, statement)
		}
		return nil
	}
	value, _, err := r.Host.Evaluate(f, instruction.Query, at)
	expressionError := err != nil
	if err == nil {
		_, err = r.AssignVariable(instruction.Target.Slot, value, at)
	}
	if err == nil {
		return nil
	}
	var diagnostic *sqlstate.Error
	var domain interface{ PLAssignmentDomain() }
	query := PLDiagnosticQuery{}
	triggerBinding := d.Invocation == DMLTriggerInvocation && PLTriggerExpressionBinding(err) && plAssignmentExpressionCallback(err)
	if !errors.As(err, &domain) && errors.As(err, &diagnostic) && ((!expressionError || d.Invocation == DMLTriggerInvocation) && strings.HasPrefix(diagnostic.Context, "SQL function ") || triggerBinding) {
		callback := PLAssignmentContext
		if triggerBinding {
			callback = PLAssignmentBinding
		}
		query = PLBodyDiagnostic(d.Source, statement.Pos, statement.ExpressionEnd, callback)
	} else if expressionError && d.Invocation != DMLTriggerInvocation && plAssignmentExpressionCallback(err) {
		query = PLBodyDiagnostic(d.Source, statement.Pos, statement.ExpressionEnd, PLAssignmentCallback)
	}
	return PLRuntimeQueryError(err, d, r.Host.Identity, statement.Pos, "at assignment", query)
}

// AssignVariable converts and publishes a whole slot.
func (r *PLRun) AssignVariable(slot int, value sqltype.Value, at PLOrigin) (sqltype.Value, error) {
	value, err := r.PrepareVariable(slot, value, at)
	if err == nil {
		r.Frame.Values[slot] = value
	}
	return value, err
}

// PrepareVariable converts a whole-slot value without publishing it; its caller
// owns atomic publication.
func (r *PLRun) PrepareVariable(slot int, value sqltype.Value, at PLOrigin) (sqltype.Value, error) {
	f := r.Frame
	variable := f.Program.Variables[slot]
	var err error
	if variable.Record {
		if value.IsNull() {
			if f.Program.Invocation == DMLTriggerInvocation && slot < 2 {
				value = sqltype.Null(f.Values[slot].Type())
			} else {
				value = sqltype.Value{}
			}
		} else if value.Type() == nil || !value.Type().BaseType().IsComposite() {
			return sqltype.Value{}, plError("0A000", "input of anonymous composite types is not implemented")
		}
	} else {
		value, err = r.Host.Convert(value, f.Usage(slot), at)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if variable.NotNull && (value.Type() == nil || value.IsNull()) {
		return sqltype.Value{}, plError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	return value, nil
}
