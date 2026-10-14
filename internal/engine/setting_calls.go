package engine

import (
	"context"
	"fmt"
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
// The caller holds e.mu over descriptor lookup and binding.
func (e *Engine) describeSettingCall(ctx context.Context, s *parser.SettingCall, input *sqltype.InputEnvironment, op *Operation) (ParameterInfo, []BoundExpr, error) {
	if !(s.Name == "current_setting" && len(s.Args) == 1 || s.Name == "set_config" && len(s.Args) == 3) {
		return ParameterInfo{}, nil, sqlError("0A000", "unsupported setting function signature")
	}
	binder := newExpressionBinder(ctx, e, *input, op, nil)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: s.Name, Pos: s.Pos}}}
	if s.Schema != nil {
		name.Parts = []parser.Identifier{*s.Schema, {Text: s.Name, Pos: s.Pos}}
	}
	visible, err := binder.builtinVisible(name, functionLookup)
	if err != nil {
		return ParameterInfo{}, nil, err
	}
	if !visible {
		return ParameterInfo{}, nil, binder.functionSignatureError(parser.FunctionExpr{Name: name, Args: s.Args, Pos: name.Parts[0].Pos})
	}
	bound := make([]BoundExpr, len(s.Args))
	view := CatalogRead{owner: e, state: e.state}
	for i, v := range s.Args {
		id := sqltype.Text
		if i == 2 {
			id = sqltype.Bool
		}
		typ, ok := view.LookupType(id)
		if !ok {
			return ParameterInfo{}, nil, fmt.Errorf("stuntgres: missing setting argument type")
		}
		var err error
		bound[i], err = binder.bind(v, expressionPolicy{context: assignmentContext, expected: typ.Usage(), mode: sqltype.Implicit})
		if err != nil {
			return ParameterInfo{}, nil, err
		}
	}
	info, err := binder.finish()
	input.EnumMemberSafe = binder.input.EnumMemberSafe
	input.TypeDiagnosticName = binder.input.TypeDiagnosticName
	return info, bound, err
}
func (e *Engine) executeSettingCall(op *Operation, s *parser.SettingCall, count int, importFrame FrameImporter, input sqltype.InputEnvironment, outputEnvironment sqltype.OutputEnvironment) (Result, error) {
	e.mu.Lock()
	info, bound, err := e.describeSettingCall(op.ctx, s, &input, op)
	typ, typeOK := (CatalogRead{owner: e, state: e.state}).LookupType(sqltype.Text)
	catalog := CatalogRead{owner: e, state: e.state}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if !typeOK {
		return Result{}, fmt.Errorf("stuntgres: missing setting result type")
	}
	if count != info.Count {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, info.Count)
	}
	frame := make([]sqltype.Value, count)
	scope, _ := input.LockedDomainInput.(*StatementScope)
	evaluation := EvalContext{Context: op.ctx, Parameters: frame, Input: input, Output: outputEnvironment, statementScope: scope, catalog: catalog}
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
	if current.catalog.owner != catalog.owner || current.catalog.Epoch() != catalog.Epoch() {
		e.mu.Unlock()
		return Result{}, ErrCatalogReplaced
	}
	validInputs := true
	for _, expression := range bound {
		walkBoundExpression(expression, func(node BoundExpr) {
			address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(node.ResultUsage().Type.ID())}
			if provider, exists := catalog.Descriptor(address); exists {
				validInputs = validInputs && current.catalog.state.descriptors[address] == provider
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
