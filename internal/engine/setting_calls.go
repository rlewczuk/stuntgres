package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func settingColumns(typ *sqltype.Descriptor, names ...string) []ColumnDescriptor {
	columns := make([]ColumnDescriptor, len(names))
	for i, name := range names {
		columns[i] = ColumnDescriptor{Name: name, Usage: typ.Usage(), NullabilityKnown: true}
	}
	return columns
}

// describeSettingCall uses the same inference and literal binding as relations.
type settingCallBinding struct {
	parameters   ParameterInfo
	arguments    []expr.Expr
	routine      ids.ObjectRef
	dependencies []expressionDependency
}

// The caller holds e.mu over descriptor lookup and binding.
func (e *Engine) describeSettingCall(ctx context.Context, s *parser.SettingCall, input *sqltype.InputEnvironment, op *Operation) (settingCallBinding, error) {
	if !(s.Name == "current_setting" && len(s.Args) == 1 || s.Name == "set_config" && len(s.Args) == 3) {
		return settingCallBinding{}, sqlError("0A000", "unsupported setting function signature")
	}
	binder := newExpressionBinder(ctx, e, *input, op, nil)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: s.Name, Pos: s.Pos}}}
	if s.Schema != nil {
		name.Parts = []parser.Identifier{*s.Schema, {Text: s.Name, Pos: s.Pos}}
	}
	bound, routine, err := functions.BindSettingCall(binder, s.Name, name, s.Args)
	if err != nil {
		return settingCallBinding{}, err
	}
	info, err := binder.finish()
	input.EnumMemberSafe = binder.input.EnumMemberSafe
	input.TypeDiagnosticName = binder.input.TypeDiagnosticName
	return settingCallBinding{parameters: info, arguments: bound, routine: routine, dependencies: binder.dependencies}, err
}
func (e *Engine) executeSettingCall(op *Operation, s *parser.SettingCall, count int, importFrame FrameImporter, input sqltype.InputEnvironment, outputEnvironment sqltype.OutputEnvironment) (Result, error) {
	e.mu.Lock()
	binding, err := e.describeSettingCall(op.ctx, s, &input, op)
	typ, typeOK := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
	catalog := e.catalog.read(e.catalog.state)
	auth, authErr := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if authErr != nil {
		return Result{}, authErr
	}
	info, bound := binding.parameters, binding.arguments
	if !typeOK {
		return Result{}, fmt.Errorf("stuntgres: missing setting result type")
	}
	if count != info.Count {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, info.Count)
	}
	frame := make([]sqltype.Value, count)
	scope, _ := input.LockedDomainInput.(*StatementScope)
	evaluation := EvalContext{Context: op.ctx, Parameters: frame, Input: input, Output: outputEnvironment, statementScope: scope, catalog: catalog, routineAuthority: &auth, routineChecks: binding.dependencies, routineRecipe: true}
	if scope != nil {
		scope.domainInputCatalog = catalog
	}
	for _, usage := range info.Types {
		if sqltype.ContainsDomainChecks(usage.Type) {
			input.DomainChecks = &evaluation
			break
		}
	}
	if count != 0 {
		if importFrame == nil {
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err := importFrame(op.ctx, info, frame, input); err != nil {
			return Result{}, err
		}
		for i, v := range frame {
			if v.Type() != info.Types[i].Type {
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	for _, expression := range bound {
		if err := validateExpressionJoinExecution(&evaluation, expression, catalog); err != nil {
			return Result{}, err
		}
	}
	invoke := true
	if s.Name == "current_setting" {
		knownNull, err := executionKnownNullArgument(&evaluation, bound[0])
		if err != nil {
			return Result{}, err
		}
		invoke = !knownNull
	}
	if invoke {
		if err := admitRoutineReference(&evaluation, binding.routine, "function"); err != nil {
			return Result{}, err
		}
	}
	var inputs [3]sqltype.Value
	for i, v := range bound {
		inputs[i], err = v.Eval(&evaluation)
		if err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	current, captureErr := e.authorizationLocked(op)
	if captureErr != nil {
		e.mu.Unlock()
		return Result{}, captureErr
	}
	if current.catalog.Owner() != catalog.Owner() || current.catalog.Epoch() != catalog.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	evaluation.routineAuthority = &current
	if invoke {
		if err := admitRoutineReference(&evaluation, binding.routine, "function"); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	validInputs := true
	for _, expression := range bound {
		walkBoundExpression(expression, func(node expr.Expr) {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(node.ResultUsage().Type.ID())}
			if provider, exists := catalog.Descriptor(address); exists {
				validInputs = validInputs && current.catalog.Get(address) == provider
			}
		})
	}
	if !validInputs {
		e.mu.Unlock()
		return Result{}, CatalogConflict{}
	}
	owner := op.session.settings
	locked := true
	unlock := func() { op.session.mu.Unlock(); owner.mu.Unlock(); e.mu.Unlock() }
	defer func() {
		if locked {
			unlock()
		}
	}()
	owner.mu.Lock()
	op.session.mu.Lock()
	if err := publicationOperationLocked(e, op); err != nil {
		return Result{}, err
	}
	if owner.ended {
		return Result{}, fmt.Errorf("stuntgres: settings attachment ended")
	}
	output := sqltype.Null(typ)
	if s.Name == "current_setting" {
		if !inputs[0].IsNull() {
			name, err := inputs[0].Text()
			if err != nil {
				return Result{}, err
			}
			d, err := e.sessionSetting(owner, name, false)
			if err != nil {
				return Result{}, err
			}
			output = sqltype.String(typ, owner.value(d.Name))
		}
		if err := op.ctx.Err(); err != nil {
			return Result{}, err
		}
	} else {
		if inputs[0].IsNull() {
			return Result{}, sqlError("22004", "SET requires parameter name")
		}
		local := false
		if !inputs[2].IsNull() {
			local, err = inputs[2].Boolean()
			if err != nil {
				return Result{}, err
			}
		}
		name, err := inputs[0].Text()
		if err != nil {
			return Result{}, err
		}
		d, err := e.sessionSetting(owner, name, true)
		if err != nil {
			return Result{}, err
		}
		var input *string
		if !inputs[1].IsNull() {
			text, err := inputs[1].Text()
			if err != nil {
				return Result{}, err
			}
			input = &text
		}
		unlock()
		locked = false
		value, err := e.applySetting(op, owner, d, input, local)
		if err != nil {
			return Result{}, err
		}
		output = sqltype.String(typ, value)
	}
	columns := settingColumns(typ, s.Name)
	columns[0].Nullable = true
	return anonymousResult(columns, [][]sqltype.Value{{output}}, 1)
}
