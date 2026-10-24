package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func settingColumns(typ *sqltype.Descriptor, names ...string) []catalog.Column {
	columns := make([]catalog.Column, len(names))
	for i, name := range names {
		columns[i] = catalog.Column{Name: name, Usage: typ.Usage(), NullabilityKnown: true}
	}
	return columns
}

// describeSettingCall uses the same inference and literal binding as relations.
type settingCallBinding struct {
	parameters   analyze.ParameterInfo
	arguments    []expr.Expr
	routine      ids.ObjectRef
	dependencies []catalog.ExpressionDependency
}

// The caller holds e.mu over descriptor lookup and binding.
func (e *Engine) describeSettingCall(ctx context.Context, s *parser.SettingCall, input *sqltype.InputEnvironment, op *session.Operation) (settingCallBinding, error) {
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
	info, err := binder.Finish()
	input.EnumMemberSafe = binder.Input.EnumMemberSafe
	input.TypeDiagnosticName = binder.Input.TypeDiagnosticName
	return settingCallBinding{parameters: info, arguments: bound, routine: routine, dependencies: binder.Dependencies}, err
}
func (e *Engine) executeSettingCall(op *session.Operation, s *parser.SettingCall, count int, importFrame FrameImporter, input sqltype.InputEnvironment, outputEnvironment sqltype.OutputEnvironment) (Result, error) {
	e.mu.Lock()
	binding, err := e.describeSettingCall(op.Ctx, s, &input, op)
	typ, typeOK := (e.catalog.read(e.catalog.state)).LookupType(sqltype.Text)
	read := e.catalog.read(e.catalog.state)
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Ctx, Parameters: frame, Input: input, Output: outputEnvironment, Host: hostOf(scope), Catalog: read}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineChecks: binding.dependencies, RoutineRecipe: true}}
	if scope != nil {
		scope.domains.inputCatalog = read
	}
	for _, usage := range info.Types {
		if sqltype.ContainsDomainConstraints(usage.Type) {
			input.DomainChecks = &evaluation
			break
		}
	}
	if count != 0 {
		if importFrame == nil {
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err := importFrame(op.Ctx, info, frame, input); err != nil {
			return Result{}, err
		}
		for i, v := range frame {
			if v.Type() != info.Types[i].Type {
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
		}
	}
	for _, expression := range bound {
		if err := view.ValidateExpressionJoinExecution(&evaluation, expression, read); err != nil {
			return Result{}, err
		}
	}
	invoke := true
	if s.Name == "current_setting" {
		knownNull, err := exec.ExecutionKnownNullArgument(&evaluation, bound[0])
		if err != nil {
			return Result{}, err
		}
		invoke = !knownNull
	}
	if invoke {
		if err := exec.AdmitRoutineReference(&evaluation, binding.routine, nil, "function"); err != nil {
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
	if current.Catalog.Owner() != read.Owner() || current.Catalog.Epoch() != read.Epoch() {
		e.mu.Unlock()
		return Result{}, ids.ErrCatalogReplaced
	}
	evaluation.RoutineAuthority = &current
	if invoke {
		if err := exec.AdmitRoutineReference(&evaluation, binding.routine, nil, "function"); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	validInputs := true
	for _, expression := range bound {
		plan.WalkExpression(expression, func(node expr.Expr) {
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(node.ResultUsage().Type.ID())}
			if provider, exists := read.Descriptor(address); exists {
				validInputs = validInputs && current.Catalog.Get(address) == provider
			}
		})
	}
	if !validInputs {
		e.mu.Unlock()
		return Result{}, catalog.Conflict{}
	}
	owner := op.Session.Settings
	locked := true
	unlock := func() { op.Session.Mu.Unlock(); owner.Mu.Unlock(); e.mu.Unlock() }
	defer func() {
		if locked {
			unlock()
		}
	}()
	owner.Mu.Lock()
	op.Session.Mu.Lock()
	if err := owner.AdmitLocked(domainOf(e), op); err != nil {
		return Result{}, err
	}
	var output sqltype.Value
	if s.Name == "current_setting" {
		if output, err = owner.CurrentSettingLocked(op.Ctx, e.settings, typ, inputs[0]); err != nil {
			return Result{}, err
		}
	} else {
		d, input, local, err := owner.SetConfigInputLocked(e.settings, inputs)
		if err != nil {
			return Result{}, err
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
