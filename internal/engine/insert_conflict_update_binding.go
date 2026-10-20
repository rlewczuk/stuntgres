package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func bindInsertConflictUpdate(b *binding, binder *expressionBinder, s *parser.Insert) error {
	if s.Conflict == nil || !s.Conflict.Update {
		return nil
	}
	c := b.Insert.Conflict
	c.Width = len(b.Relation.Columns)
	binder.target.relation, binder.target.ref = b.Relation, b.Ref
	binder.target.name, binder.target.alias = b.Relation.Name(), s.Alias.Text
	binder.params.updateParameters = true
	defer func() { binder.params.updateParameters = false; binder.params.updateInputParameters = nil }()
	// Conflict UPDATE owns a fresh lexical namespace: the rule's formal
	// OLD/NEW entries remain diagnostic candidates, not executable columns.
	ruleNamespace, previousNamespace, previousHidden := binder.target.ruleNamespace, binder.query.namespace, binder.query.hidden
	if ruleNamespace != nil {
		binder.target.ruleNamespace = nil
		binder.query.hidden = append(append([]queryQualifier(nil), previousHidden...),
			queryQualifier{name: "old", original: "old", lateralForbidden: true},
			queryQualifier{name: "new", original: "new", lateralForbidden: true})
	}
	defer func() {
		binder.target.ruleNamespace, binder.query.namespace, binder.query.hidden = ruleNamespace, previousNamespace, previousHidden
	}()
	target := binder.mutationTargetNamespace()
	binder.query.hidden = append(binder.query.hidden, target.qualifiers[0])
	excluded := rebaseQueryNamespace(target, len(b.Relation.Columns))
	excluded.qualifiers[0] = queryQualifier{name: "excluded", columns: excluded.columns, lateralForbidden: true}
	namespace := queryNamespace{
		columns:    append(target.columns, excluded.columns...),
		qualifiers: append(target.qualifiers, excluded.qualifiers...),
		frame:      append(append([]catalog.Column(nil), target.frame...), target.frame...),
	}
	binder.query.namespace = &namespace
	binder.clause.aggregateClause = "UPDATE"
	action := &boundUpdate{Assignments: make([]expr.Expr, len(s.Conflict.Assignments))}
	actionBinding := *b
	actionBinding.Targets = nil
	if err := bindUpdateAssignments(&actionBinding, binder, s.Conflict.Assignments, action); err != nil {
		return err
	}
	if s.Conflict.Where != nil {
		predicate, err := binder.bindBoolean(s.Conflict.Where, filterContext, "WHERE")
		if err != nil {
			return err
		}
		action.Predicate = predicate
	}
	c.Update = action
	c.UpdateTargets = actionBinding.Targets
	c.ActionReads = append([]int(nil), binder.target.uses...)
	c.Sources = binder.query.sources
	return nil
}
