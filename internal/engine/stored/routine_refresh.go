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

// ValidateRoutineCallIdentities checks frozen calls against current declarations
// without replacing their captured handles. A handle and its lifetime manifest
// belong to the same original binding; relabeling must not mix generations.
func ValidateRoutineCallIdentities(read catalog.Read, b analyze.Binding) error {
	mapper := NewRuleTreeMapper(read, nil, 0, nil, RuleTreeMapping{Leaf: func(node expr.Expr) (expr.Expr, bool, error) {
		call, ok := node.(plan.SQLRoutineExpr)
		if !ok {
			return node, false, nil
		}
		_, err := currentRoutineCall(read, call)
		return node, false, err
	}})
	_, err := MapRuleBindingRecipes(b, mapper)
	return err
}

func currentRoutineCall(read catalog.Read, call plan.SQLRoutineExpr) (*catalog.Procedure, error) {
	if call.Procedure == nil || call.Ref.Address != call.Procedure.Address() || read.Owner() == nil || call.Ref.Domain() != read.Owner().Domain || call.Ref.Epoch != read.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	current, ok := read.Procedure(call.Ref.Address)
	if !ok || current.SQL == nil || !exec.SameRoutineDeclaration(current, call.Procedure) || current.RoutineKind != catalog.FunctionRoutine {
		return nil, ids.ErrCatalogReplaced
	}
	derived, err := call.Collate(func(info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
		return analyze.CollatedInfo(read, info, inputs...)
	})
	if err != nil {
		return nil, err
	}
	if derived.InputCollation != call.InputCollation {
		return nil, fmt.Errorf("stuntgres: mismatched SQL routine input collation")
	}
	if call.InputCollation.ID != 0 {
		if err := ValidateUsageCollation(read, sqltype.Usage{Collation: call.InputCollation}); err != nil {
			return nil, err
		}
	}
	return current, nil
}

// Type evolution keeps logical identities and references, but every affected
// declaration and capture must own the destination descriptor generation.
// Unchanged declarations and payloads retain their immutable pointer identity.
func reownSQLRoutineTypes(view catalog.Read, state *catalog.State, typ func(*sqltype.Descriptor) *sqltype.Descriptor, remap *catalog.RoutineRemap) error {
	type refresh struct {
		source         *catalog.Procedure
		definition     *proc.Definition
		defaults, body bool
	}
	var changes map[ids.ObjectAddress]*refresh
	var queue []ids.ObjectAddress
	for address, descriptor := range state.Descriptors() {
		d, ok := descriptor.(*catalog.Procedure)
		if !ok {
			continue
		}
		if d.RangeConstructor != nil {
			if typ(d.RangeConstructor.Type) != d.RangeConstructor.Type {
				if changes == nil {
					changes = make(map[ids.ObjectAddress]*refresh)
				}
				changes[address] = &refresh{source: d}
				queue = append(queue, address)
			}
			continue
		}
		if d.SQL == nil {
			continue
		}
		definition, err := remap.Definition(d.SQL)
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
		mapped, err := remap.Procedure(change.source)
		if err != nil {
			return err
		}
		if change.source.RangeConstructor != nil {
			state.SetDescriptor(address, mapped)
			continue
		}
		next := *mapped
		next.SQL = change.definition
		state.SetDescriptor(address, &next)
	}
	if err := remap.Declarations(); err != nil {
		return err
	}
	for address, change := range changes {
		current, _ := state.Procedure(address)
		if change.defaults {
			payload, err := remap.Defaults(change.source.SQLDefaults)
			if err != nil {
				return err
			}
			current.SQLDefaults = payload
		}
		if change.body {
			payload, err := remap.Body(change.source.SQLBody)
			if err != nil {
				return err
			}
			current.SQLBody = payload
		}
	}
	return remap.Finish()
}

func reownRoutineDefinition(source *proc.Definition, usage func(sqltype.Usage) (sqltype.Usage, error), programs map[*proc.PLProgram]*proc.PLProgram) (*proc.Definition, error) {
	next := source
	detach := func() {
		if next == source {
			copy := *source
			next = &copy
		}
	}
	for i, formal := range source.Formals {
		mapped, err := usage(formal.Usage)
		if err != nil {
			return nil, err
		}
		if mapped.Type == nil || !mapped.Type.Concrete() {
			return nil, fmt.Errorf("stuntgres: missing SQL routine formal generation")
		}
		if mapped != formal.Usage {
			detach()
			if next.Formals == nil || &next.Formals[0] == &source.Formals[0] {
				next.Formals = slices.Clone(source.Formals)
			}
			next.Formals[i].Usage = mapped
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
	result, err := usage(source.Result)
	if err != nil {
		return nil, err
	}
	if result.Type == nil || (!result.Type.Concrete() && !(source.Trigger && result.Type.ID() == 2279)) {
		return nil, fmt.Errorf("stuntgres: missing SQL routine result generation")
	}
	if result != source.Result {
		detach()
		next.Result = result
	}
	if source.PL != nil {
		program, found := programs[source.PL]
		if !found {
			program = source.PL
			for i, variable := range source.PL.Variables {
				if variable.Record {
					continue
				}
				mapped, err := usage(variable.Usage)
				if err != nil {
					return nil, err
				}
				if mapped.Type == nil || !mapped.Type.Concrete() {
					return nil, fmt.Errorf("stuntgres: missing PL local type generation")
				}
				if mapped != variable.Usage {
					if program == source.PL {
						copy := *source.PL
						copy.Variables = slices.Clone(source.PL.Variables)
						program = &copy
					}
					program.Variables[i].Usage = mapped
				}
			}
			programs[source.PL] = program
		}
		if program != source.PL {
			detach()
			next.PL = program
		}
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
		if earth, ok := child.(plan.InstalledEarthExpr); ok {
			changed = changed || earth.Plan.Earth() != typ(earth.Plan.Earth())
		}
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
