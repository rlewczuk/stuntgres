package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ValidateDefinition is a publication/replacement shape check, not an invocation
// lookup. Reached commands separately check their own statement association.
func (p *RoutineProgram) ValidateDefinition(d *catalog.Procedure) error {
	if d.SQL == nil || !d.SQL.Standard || len(p.actions) != len(d.SQL.Statements) {
		return fmt.Errorf("stuntgres: invalid SQL-standard routine program")
	}
	for i, statement := range d.SQL.Statements {
		if p.actions[i].syntax != statement || p.actions[i].presentation == nil {
			return fmt.Errorf("stuntgres: mismatched SQL-standard command capture")
		}
		if inputs := p.actions[i].presentation.routineInputs; len(inputs) != len(d.SQL.Inputs) {
			return fmt.Errorf("stuntgres: mismatched frozen routine formal namespace")
		} else {
			for j, input := range inputs {
				if input != d.SQL.Inputs[j] {
					return fmt.Errorf("stuntgres: mismatched frozen routine formal usage")
				}
			}
		}
	}
	return nil
}

func validateRoutineActionShape(action RuleAction) error {
	syntax, merging := action.syntax.(*parser.Merge)
	if !merging {
		if action.binding.Merge != nil {
			return fmt.Errorf("stuntgres: unexpected routine MERGE capture")
		}
		return nil
	}
	merge := action.binding.Merge
	if merge == nil || merge.Source == nil || merge.On == nil || action.binding.Relation == nil || len(merge.Arms) != len(syntax.Arms) {
		return fmt.Errorf("stuntgres: incomplete routine MERGE capture")
	}
	for i, arm := range merge.Arms {
		if arm.Match != syntax.Arms[i].Match || arm.Action != syntax.Arms[i].Action || arm.Match > parser.MergeNotMatchedBySource || arm.Action > parser.MergeNothingAction {
			return fmt.Errorf("stuntgres: mismatched routine MERGE arm")
		}
		switch arm.Action {
		case parser.MergeUpdateAction:
			if arm.Input.Relation == nil || arm.Input.Update == nil || len(arm.Input.Targets) != len(arm.Input.Update.Assignments) {
				return fmt.Errorf("stuntgres: incomplete routine MERGE UPDATE capture")
			}
		case parser.MergeInsertAction:
			if arm.Input.Relation == nil || !syntax.Arms[i].DefaultValues && (len(arm.Input.Expressions) != 1 || len(arm.Input.Expressions[0]) != len(arm.Input.Targets)) {
				return fmt.Errorf("stuntgres: incomplete routine MERGE INSERT capture")
			}
		}
		for _, slot := range arm.Input.Targets {
			if arm.Input.Relation == nil || slot < 0 || slot >= len(arm.Input.Relation.Attributes) {
				return fmt.Errorf("stuntgres: invalid routine MERGE assignment target")
			}
		}
	}
	return nil
}

func validateRoutineActionScalars(action RuleAction, read catalog.Read) error {
	if action.presentation == nil || action.presentation.query == nil {
		return fmt.Errorf("stuntgres: missing routine command presentation")
	}
	if _, selecting := action.syntax.(*parser.Select); selecting {
		if action.binding.Query == nil {
			return fmt.Errorf("stuntgres: missing routine SELECT capture")
		}
		return validateRoutineQueryScalars(action.binding.Query.Root, action.presentation.routineInputs, read)
	}
	if insert, ok := action.syntax.(*parser.Insert); ok && insert.Source != nil && action.binding.Query != nil {
		return validateRoutineQueryScalars(action.binding.Query.Root, action.presentation.routineInputs, read)
	}
	project, ok := action.presentation.query.Root.(*plan.ProjectNode)
	if !ok {
		return fmt.Errorf("stuntgres: missing routine mutation frame")
	}
	count := 0
	var failure error
	VisitRuleActionExpressions(action.binding, func(bound expr.Expr) {
		if failure != nil {
			return
		}
		if bound == nil || count >= len(project.Expressions) || project.Expressions[count] == nil || bound.ResultUsage() != project.Expressions[count].ResultUsage() {
			failure = fmt.Errorf("stuntgres: mismatched routine mutation scalar capture")
			return
		}
		count++
		failure = validateRoutineScalar(bound, action.presentation.routineInputs, read)
	})
	if failure != nil {
		return failure
	}
	if count != len(project.Expressions) {
		return fmt.Errorf("stuntgres: extra routine mutation scalar capture")
	}
	return nil
}

func validateRoutinePresentation(read catalog.Read, presentation *View) error {
	if presentation.query == nil || presentation.query.Root == nil {
		return fmt.Errorf("stuntgres: missing routine query capture")
	}
	if err := presentation.ValidateLiterals(read); err != nil {
		return err
	}
	if err := ValidateViewLeaves(read, presentation); err != nil {
		return err
	}
	return validateRoutineScalars(presentation, read)
}

// Rebind uses the zero read only for pre-remap structural validation: original
// captures belong to the old catalog. Current identity is checked after remap.
func validateRoutineScalars(presentation *View, read catalog.Read) error {
	if presentation == nil || presentation.query == nil {
		return fmt.Errorf("stuntgres: missing routine query capture")
	}
	return validateRoutineQueryScalars(presentation.query.Root, presentation.routineInputs, read)
}

func validateRoutineQueryScalars(root plan.Node, inputs []sqltype.Usage, read catalog.Read) error {
	if root == nil {
		return fmt.Errorf("stuntgres: missing routine query node")
	}
	var failure error
	plan.QueryExpressions(root, func(bound expr.Expr) {
		if failure == nil {
			failure = validateRoutineScalar(bound, inputs, read)
		}
	})
	if failure != nil {
		return failure
	}
	left, right := plan.QueryInputs(root)
	if left != nil {
		if err := validateRoutineQueryScalars(left, inputs, read); err != nil {
			return err
		}
	}
	if right != nil {
		return validateRoutineQueryScalars(right, inputs, read)
	}
	return nil
}

func validateRoutineScalar(bound expr.Expr, inputs []sqltype.Usage, read catalog.Read) error {
	var failure error
	plan.WalkExpression(bound, func(node expr.Expr) {
		if failure != nil {
			return
		}
		switch n := node.(type) {
		case plan.ParameterExpr:
			if n.Number <= 0 || n.Number > len(inputs) || n.ResultUsage() != inputs[n.Number-1] {
				failure = fmt.Errorf("stuntgres: invalid frozen routine formal")
			}
		case plan.RangeConstructorExpr:
			failure = n.ValidateStoredProvider()
			if failure == nil && read.State() != nil {
				current, ok := read.Procedure(n.Ref.Address)
				if !ok || current.RangeConstructor != n.Procedure.RangeConstructor {
					failure = fmt.Errorf("invalid current range constructor capture")
				} else {
					failure = catalog.ValidateRangeConstructor(read, current)
				}
			}
		case plan.SQLRoutineExpr:
			failure = n.ValidateStoredProvider()
			if failure == nil && read.State() != nil {
				_, failure = currentRoutineCall(read, n)
			}
		case plan.SubqueryExpr:
			if n.QueryPlan == nil {
				failure = fmt.Errorf("stuntgres: missing routine subquery capture")
			} else {
				failure = validateRoutineQueryScalars(n.QueryPlan.Root, inputs, read)
			}
		}
	})
	return failure
}

// PostgreSQL records one normal edge per referenced subobject, and a relation
// edge only when no column edge of that relation supplies the same dependency.
// Keep richer per-action admission manifests separate from this published set.
func routineDependencySet(dependencies []catalog.ExpressionDependency) []catalog.ExpressionDependency {
	attributes := make(map[ids.ObjectAddress]bool)
	for _, dependency := range dependencies {
		a := dependency.Ref.Address
		if a.ClassID == ids.RelationClassID && a.SubID != 0 {
			a.SubID = 0
			attributes[a] = true
		}
	}
	seen := make(map[ids.ObjectRef]bool)
	next := dependencies[:0]
	for _, dependency := range dependencies {
		a := dependency.Ref.Address
		if a.SubID == 0 && attributes[a] || seen[dependency.Ref] {
			continue
		}
		seen[dependency.Ref] = true
		next = append(next, dependency)
	}
	return next
}

func (p *RoutineProgram) validateDependencySet() error {
	if len(p.actionDependencies) != len(p.actions) {
		return fmt.Errorf("stuntgres: incomplete routine dependency capture")
	}
	attributes := make(map[ids.ObjectAddress]bool)
	for _, dependencies := range p.actionDependencies {
		for _, dependency := range dependencies {
			a := dependency.Ref.Address
			if a.ClassID == ids.RelationClassID && a.SubID != 0 {
				a.SubID = 0
				attributes[a] = true
			}
		}
	}
	expected := make(map[ids.ObjectRef]bool)
	for _, dependencies := range p.actionDependencies {
		for _, dependency := range dependencies {
			a := dependency.Ref.Address
			if a.SubID == 0 && attributes[a] {
				continue
			}
			expected[dependency.Ref] = true
		}
	}
	for _, dependency := range p.dependencies {
		if !expected[dependency.Ref] {
			return fmt.Errorf("stuntgres: extra routine dependency capture")
		}
		delete(expected, dependency.Ref)
	}
	if len(expected) != 0 {
		return fmt.Errorf("stuntgres: missing routine dependency capture")
	}
	return nil
}
