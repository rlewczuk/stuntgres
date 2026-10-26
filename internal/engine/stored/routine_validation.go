package stored

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/analyze"
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

func validateRoutineModifiers(action RuleAction, read catalog.Read) error {
	var statements map[parser.Statement]bool
	err := VisitRuleModifiers(action.binding, func(definition *plan.CTEDefinition, mutation *analyze.Binding) error {
		presentation := action.modifierPresentations[definition.Statement]
		if presentation == nil || presentation.actionSyntax != definition.Statement || statements[definition.Statement] {
			return fmt.Errorf("stuntgres: mismatched routine modifier presentation")
		}
		if statements == nil {
			statements = make(map[parser.Statement]bool)
		}
		statements[definition.Statement] = true
		child := RuleAction{syntax: definition.Statement, binding: *mutation, presentation: presentation}
		if err := validateRoutineActionShape(child); err != nil {
			return err
		}
		if err := validateRoutineActionScalars(child, read); err != nil {
			return err
		}
		if read.State() == nil {
			return validateRoutineScalars(presentation, read)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(statements) != len(action.modifierPresentations) {
		return fmt.Errorf("stuntgres: extra routine modifier presentation")
	}
	var coverage *viewSourceCoverage
	if len(statements) != 0 && read.State() != nil {
		if err := validateRoutineSourceManifest(action); err != nil {
			return err
		}
		coverage = &viewSourceCoverage{leaves: action.presentation.sources, seen: make([]bool, len(action.presentation.sources))}
	}
	modifier := func(definition *plan.CTEDefinition) error {
		if !statements[definition.Statement] {
			return fmt.Errorf("stuntgres: orphan routine modifier scan")
		}
		return nil
	}
	check := func(presentation *View) error {
		if read.State() == nil {
			return nil // Full leaf/identity validation follows remapping.
		}
		return validateRoutinePresentation(read, presentation, modifier, coverage)
	}
	if err := check(action.presentation); err != nil {
		return err
	}
	for _, presentation := range action.modifierPresentations {
		if err := check(presentation); err != nil {
			return err
		}
	}
	if coverage != nil {
		for _, seen := range coverage.seen {
			if !seen {
				return fmt.Errorf("stuntgres: invalid routine source manifest")
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

func validateRoutinePresentation(read catalog.Read, presentation *View, modifier func(*plan.CTEDefinition) error, coverage *viewSourceCoverage) error {
	if presentation == nil || presentation.query == nil || presentation.query.Root == nil {
		return fmt.Errorf("stuntgres: missing routine query capture")
	}
	if err := presentation.ValidateLiterals(read); err != nil {
		return err
	}
	if err := validateViewLeaves(read, presentation, modifier, coverage); err != nil {
		return err
	}
	return validateRoutineScalars(presentation, read)
}

func validateRoutineSourceManifest(action RuleAction) error {
	if len(action.presentation.sources) != len(action.binding.QuerySources) {
		return fmt.Errorf("stuntgres: mismatched routine source manifest")
	}
	for occurrence, source := range action.binding.QuerySources {
		leaf := action.presentation.sources[occurrence]
		if source.Relation == nil {
			return fmt.Errorf("stuntgres: missing routine source descriptor")
		}
		attributes := source.FrozenAttributes
		if attributes == nil {
			attributes = source.Relation.Attributes
		}
		if leaf.Ref != source.Ref || leaf.Only != source.Only || leaf.AnalysisOnly != source.AnalysisOnly ||
			leaf.TableOIDRead != source.TableOIDRead || leaf.Mark != source.Mark ||
			!slices.Equal(leaf.Attributes, attributes) || len(leaf.Reads) != len(source.Uses) {
			return fmt.Errorf("stuntgres: mismatched routine source provenance")
		}
		for i, slot := range source.Uses {
			if slot < 0 || slot >= len(source.Relation.Attributes) || leaf.Reads[i] != source.Relation.Attributes[slot] {
				return fmt.Errorf("stuntgres: mismatched routine source reads")
			}
		}
	}
	return nil
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

type routineDependencyCoverage struct {
	ref     ids.ObjectRef
	purpose catalog.DependencyPurpose
}

// The complete manifest retains original stamps and both capture purposes.
// Column coverage suppresses whole-relation captures only within one purpose.
func routineDependencySet(dependencies []catalog.ExpressionDependency) []catalog.ExpressionDependency {
	attributes := make(map[routineDependencyCoverage]bool)
	for _, dependency := range dependencies {
		ref := dependency.Ref
		if ref.Address.ClassID == ids.RelationClassID && ref.Address.SubID != 0 {
			ref.Address.SubID = 0
			attributes[routineDependencyCoverage{ref, dependency.Purpose}] = true
		}
	}
	seen := make(map[catalog.ExpressionDependency]bool)
	next := dependencies[:0]
	for _, dependency := range dependencies {
		if dependency.Ref.Address.SubID == 0 && attributes[routineDependencyCoverage{dependency.Ref, dependency.Purpose}] || seen[dependency] {
			continue
		}
		seen[dependency] = true
		next = append(next, dependency)
	}
	return next
}

func (p *RoutineProgram) validateDependencySet() error {
	if len(p.actionDependencies) != len(p.actions) {
		return fmt.Errorf("stuntgres: incomplete routine dependency capture")
	}
	attributes := make(map[routineDependencyCoverage]bool)
	for i, dependencies := range p.actionDependencies {
		expected := make(map[catalog.ExpressionDependency]int)
		visitRoutineActionDependencies(p.actions[i], func(dependency catalog.ExpressionDependency) { expected[dependency]++ })
		for _, dependency := range dependencies {
			if expected[dependency] == 0 {
				return fmt.Errorf("stuntgres: extra action dependency capture")
			}
			expected[dependency]--
			ref := dependency.Ref
			if ref.Address.ClassID == ids.RelationClassID && ref.Address.SubID != 0 {
				ref.Address.SubID = 0
				attributes[routineDependencyCoverage{ref, dependency.Purpose}] = true
			}
		}
		for _, count := range expected {
			if count != 0 {
				return fmt.Errorf("stuntgres: missing action dependency capture")
			}
		}
	}
	expected := make(map[catalog.ExpressionDependency]bool)
	for _, dependencies := range p.actionDependencies {
		for _, dependency := range dependencies {
			if dependency.Ref.Address.SubID != 0 || !attributes[routineDependencyCoverage{dependency.Ref, dependency.Purpose}] {
				expected[dependency] = true
			}
		}
	}
	if len(expected) != len(p.dependencies) {
		return fmt.Errorf("stuntgres: mismatched routine dependency capture")
	}
	for _, dependency := range p.dependencies {
		if !dependency.Purpose.Valid() || dependency.Revision == 0 {
			return fmt.Errorf("stuntgres: invalid routine dependency capture")
		}
		if !expected[dependency] {
			return fmt.Errorf("stuntgres: extra routine dependency capture")
		}
		delete(expected, dependency)
	}
	return nil
}
