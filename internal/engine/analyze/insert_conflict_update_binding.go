package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func bindInsertConflictUpdate(b *Binding, binder *Binder, s *parser.Insert) error {
	if s.Conflict == nil || !s.Conflict.Update {
		return nil
	}
	c := b.Insert.Conflict
	c.Width = len(b.Relation.Columns)
	binder.Target.Relation, binder.Target.Ref = b.Relation, b.Ref
	binder.Target.Name, binder.Target.alias = b.Relation.Name(), s.Alias.Text
	binder.Params.UpdateParameters = true
	defer func() { binder.Params.UpdateParameters = false; binder.Params.updateInputParameters = nil }()
	// Conflict UPDATE owns a fresh lexical namespace: the rule's formal
	// OLD/NEW entries remain diagnostic candidates, not executable columns.
	ruleNamespace, previousNamespace, previousHidden := binder.Target.RuleNamespace, binder.Query.Namespace, binder.Query.Hidden
	if ruleNamespace != nil {
		binder.Target.RuleNamespace = nil
		binder.Query.Hidden = append(append([]QueryQualifier(nil), previousHidden...),
			QueryQualifier{Name: "old", Original: "old", LateralForbidden: true},
			QueryQualifier{Name: "new", Original: "new", LateralForbidden: true})
	}
	defer func() {
		binder.Target.RuleNamespace, binder.Query.Namespace, binder.Query.Hidden = ruleNamespace, previousNamespace, previousHidden
	}()
	target := binder.mutationTargetNamespace()
	binder.Query.Hidden = append(binder.Query.Hidden, target.Qualifiers[0])
	excluded := rebaseQueryNamespace(target, len(b.Relation.Columns))
	excluded.Qualifiers[0] = QueryQualifier{Name: "excluded", Columns: excluded.Columns, LateralForbidden: true}
	namespace := QueryNamespace{
		Columns:    append(target.Columns, excluded.Columns...),
		Qualifiers: append(target.Qualifiers, excluded.Qualifiers...),
		Frame:      append(append([]catalog.Column(nil), target.Frame...), target.Frame...),
	}
	binder.Query.Namespace = &namespace
	binder.Clause.AggregateClause = "UPDATE"
	action := &UpdateBinding{Assignments: make([]expr.Expr, len(s.Conflict.Assignments))}
	actionBinding := *b
	actionBinding.Targets = nil
	if err := bindUpdateAssignments(&actionBinding, binder, s.Conflict.Assignments, action); err != nil {
		return err
	}
	if s.Conflict.Where != nil {
		predicate, err := binder.BindBoolean(s.Conflict.Where, FilterContext, "WHERE")
		if err != nil {
			return err
		}
		action.Predicate = predicate
	}
	c.Update = action
	c.UpdateTargets = actionBinding.Targets
	c.ActionReads = append([]int(nil), binder.Target.Uses...)
	c.Sources = binder.Query.Sources
	return nil
}
