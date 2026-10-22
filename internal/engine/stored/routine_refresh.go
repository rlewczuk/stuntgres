package stored

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// RefreshRoutineCalls refreshes descriptor handles by captured identity only.
// A rename never reselects the callee or an overload from original SQL names.
func RefreshRoutineCalls(read catalog.Read, b analyze.Binding) (analyze.Binding, error) {
	mapper := NewRuleTreeMapper(read, nil, 0, nil, RuleTreeMapping{Leaf: func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok {
			return node, false, nil
		}
		current, err := currentRoutineCall(read, call)
		if err != nil {
			return nil, false, err
		}
		if current == call.Procedure {
			return node, false, nil
		}
		call.Procedure, call.Info.RoutineReference = current, current
		return call, true, nil
	}})
	return MapRuleBindingRecipes(b, mapper)
}

func currentRoutineCall(read catalog.Read, call plan.SQLRoutineExpr) (*catalog.Procedure, error) {
	if call.Procedure == nil || call.Ref.Address != call.Procedure.Address() || read.Owner() == nil || call.Ref.Domain() != read.Owner().Domain || call.Ref.Epoch != read.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	current, ok := read.Procedure(call.Ref.Address)
	if !ok || current.SQL == nil || current.SQL != call.Procedure.SQL || current.SQLDefaults != call.Procedure.SQLDefaults || current.Signature != call.Procedure.Signature || current.Result != call.Procedure.Result || current.RoutineKind != catalog.FunctionRoutine {
		return nil, ids.ErrCatalogReplaced
	}
	return current, nil
}

// Type evolution keeps logical identities and references, but every affected
// declaration and capture must own the destination descriptor generation.
// Unchanged declarations and payloads retain their immutable pointer identity.
func reownSQLRoutineTypes(view catalog.Read, state *catalog.State, typ func(*sqltype.Descriptor) *sqltype.Descriptor) error {
	type refresh struct {
		source         *catalog.Procedure
		definition     *proc.Definition
		defaults, body bool
	}
	var changes map[ids.ObjectAddress]*refresh
	var queue []ids.ObjectAddress
	for address, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		definition, err := reownRoutineDefinition(d.SQL, typ)
		if err != nil {
			return err
		}
		defaults, body := false, false
		if d.SQLDefaults != nil {
			payload, ok := d.SQLDefaults.(*RoutineDefaults)
			if !ok {
				return fmt.Errorf("stuntgres: invalid SQL routine default payload")
			}
			for _, node := range payload.entries {
				defaults = defaults || routineExpressionChanged(view, node, typ)
			}
		}
		if d.SQLBody != nil {
			payload, err := RoutineProgramOf(d)
			if err != nil {
				return err
			}
			for _, action := range payload.actions {
				body = body || routinePresentationChanged(view, action.presentation, typ)
				for _, presentation := range action.modifierPresentations {
					body = body || routinePresentationChanged(view, presentation, typ)
				}
			}
		}
		if definition != d.SQL || defaults || body {
			if changes == nil {
				changes = make(map[ids.ObjectAddress]*refresh)
			}
			changes[address] = &refresh{source: d, definition: definition, defaults: defaults, body: body}
			queue = append(queue, address)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	// Propagate changed callable carriers through the existing hard dependency
	// graph, without repeated whole-catalog scans or textual name resolution.
	type dependent struct {
		source   *catalog.Procedure
		defaults bool
	}
	reverse := make(map[ids.ObjectAddress][]dependent)
	for _, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.SQL == nil {
			continue
		}
		add := func(dependencies []catalog.ExpressionDependency, defaults bool) {
			for _, dependency := range dependencies {
				if dependency.Ref.Address.ClassID == ids.ProcedureClassID {
					reverse[dependency.Ref.Address] = append(reverse[dependency.Ref.Address], dependent{source: d, defaults: defaults})
				}
			}
		}
		if d.SQLDefaults != nil {
			add(d.SQLDefaults.Dependencies(), true)
		}
		if d.SQLBody != nil {
			add(d.SQLBody.Dependencies(), false)
		}
	}
	// The queue grows while visiting dependents, so its bound is not fixed.
	for i := 0; i < len(queue); i++ {
		for _, edge := range reverse[queue[i]] {
			address := edge.source.Address()
			change := changes[address]
			if change == nil {
				change = &refresh{source: edge.source, definition: edge.source.SQL}
				changes[address] = change
				queue = append(queue, address)
			}
			if edge.defaults {
				change.defaults = true
			} else {
				change.body = true
			}
		}
	}
	// Stabilize all affected callee handles before remapping either payload.
	for address, change := range changes {
		next := *change.source
		next.SQL = change.definition
		state.SetDescriptor(address, &next)
	}
	for address, change := range changes {
		current, _ := state.Procedure(address)
		if change.defaults {
			payload, err := change.source.SQLDefaults.Rebind(view, nil)
			if err != nil {
				return err
			}
			current.SQLDefaults = payload
		}
		if change.body {
			payload, err := change.source.SQLBody.Rebind(view, nil)
			if err != nil {
				return err
			}
			current.SQLBody = payload
		}
	}
	return nil
}

func reownRoutineDefinition(source *proc.Definition, typ func(*sqltype.Descriptor) *sqltype.Descriptor) (*proc.Definition, error) {
	next := source
	detach := func() {
		if next == source {
			copy := *source
			next = &copy
		}
	}
	for i, formal := range source.Formals {
		mapped := typ(formal.Usage.Type)
		if mapped == nil || !mapped.Concrete() {
			return nil, fmt.Errorf("stuntgres: missing SQL routine formal generation")
		}
		if mapped != formal.Usage.Type {
			detach()
			if next.Formals == nil || &next.Formals[0] == &source.Formals[0] {
				next.Formals = slices.Clone(source.Formals)
			}
			next.Formals[i].Usage.Type = mapped
		}
	}
	for i, formal := range source.InputFormals {
		if next.Formals[formal].Usage != source.Inputs[i] {
			detach()
			if &next.Inputs[0] == &source.Inputs[0] {
				next.Inputs = slices.Clone(source.Inputs)
			}
			next.Inputs[i] = next.Formals[formal].Usage
		}
	}
	result := typ(source.Result.Type)
	if result == nil || !result.Concrete() {
		return nil, fmt.Errorf("stuntgres: missing SQL routine result generation")
	}
	if result != source.Result.Type {
		detach()
		next.Result.Type = result
	}
	return next, nil
}

func routineExpressionChanged(view catalog.Read, node expr.Expr, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if node == nil {
		return false
	}
	changed := false
	analyze.WalkQueryArgumentExpression(node, func(child expr.Expr) {
		changed = changed || child.ResultUsage().Type != typ(child.ResultUsage().Type)
		captured, err := exec.ExpressionRoutineDescriptor(child)
		if err != nil {
			changed = true
		} else if captured != nil {
			current, _ := view.Procedure(captured.Address())
			changed = changed || !exec.SameRoutineDeclaration(current, captured)
		}
	})
	return changed
}

func routinePresentationChanged(view catalog.Read, presentation *View, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if presentation == nil || presentation.query == nil {
		return true
	}
	if analyze.QueryDescriptorsChanged(presentation.query.Root, typ) || analyze.QueryRoutineDeclarationsChanged(view, presentation.query.Root) {
		return true
	}
	for _, usage := range presentation.routineInputs {
		if usage.Type != typ(usage.Type) {
			return true
		}
	}
	for _, usage := range presentation.castUsages {
		if usage.Type != typ(usage.Type) {
			return true
		}
	}
	for _, composite := range presentation.composites {
		if composite.Base.Type != typ(composite.Base.Type) {
			return true
		}
	}
	return false
}
