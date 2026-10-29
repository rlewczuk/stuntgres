package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// VisitDefinitionExpressions visits checked owning roots, not quoted SQL source.
// Query roots include nested queries/CTEs through the existing query walker.
func VisitDefinitionExpressions(d catalog.Descriptor, visit func(expr.Expr)) error {
	query := func(root plan.Node) { plan.VisitCTEQuery(root, func(plan.Node) {}, visit, true) }
	switch d := d.(type) {
	case *catalog.PublicationRelation:
		if p := PolicyOf(d.Filter); p != nil {
			visit(p.expression)
		}
	case *catalog.Relation:
		if v := ViewOf(d); v != nil {
			query(v.Query().Root)
		}
		for _, def := range d.ColumnMeta.Defaults {
			visit(def.Expression)
		}
		for _, generation := range d.ColumnMeta.Generations {
			visit(generation.Expression)
		}
		for _, check := range d.Constraints.Check {
			visit(check.Expression)
		}
		if d.SQLIndex != nil {
			for _, key := range d.SQLIndex.Keys {
				if key.Derived != nil {
					visit(key.Derived.Execution)
				}
			}
			if d.SQLIndex.Predicate != nil {
				visit(d.SQLIndex.Predicate.Execution)
			}
		}
		for _, policy := range d.Security.Policies {
			if p := PolicyOf(policy.Using); p != nil {
				visit(p.expression)
			}
			if p := PolicyOf(policy.Check); p != nil {
				visit(p.expression)
			}
		}
		for _, trigger := range TriggersOf(d) {
			visit(trigger.parts.When)
		}
		for _, rule := range RulesOf(d) {
			visit(rule.condition)
			for _, action := range rule.actions {
				VisitRuleActionExpressions(action.binding, visit)
				if action.binding.Query != nil {
					query(action.binding.Query.Root)
				}
			}
		}
	case *catalog.Type:
		if d.DomainDefault != nil {
			visit(d.DomainDefault.Expression)
		}
		for _, check := range d.DomainChecks {
			visit(check.Expression)
		}
	case *catalog.Procedure:
		if layout := d.RoutineDefinition(); layout != nil && d.SQLDefaults != nil {
			for slot := range layout.Inputs {
				if node, ok := d.SQLDefaults.Default(slot); ok {
					visit(node)
				}
			}
		}
		if d.SQL != nil && d.SQL.Standard {
			program, err := RoutineProgramOf(d)
			if err != nil {
				return err
			}
			for i := range program.Len() {
				binding := program.Action(i).Binding()
				VisitRuleActionExpressions(binding, visit)
				if binding.Query != nil {
					query(binding.Query.Root)
				}
			}
		}
	}
	return nil
}
