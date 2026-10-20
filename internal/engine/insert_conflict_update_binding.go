package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func (e *Engine) bindInsertConflictUpdate(b *binding, binder *expressionBinder, s *parser.Insert) error {
	if s.Conflict == nil || !s.Conflict.Update {
		return nil
	}
	c := b.insert.conflict
	c.width = len(b.relation.Columns)
	binder.target.relation, binder.target.ref = b.relation, b.ref
	binder.target.name, binder.target.alias = b.relation.Name(), s.Alias.Text
	binder.params.updateParameters = true
	defer func() { binder.params.updateParameters = false; binder.params.updateInputParameters = nil }()
	// Conflict UPDATE owns a fresh lexical namespace: the rule's formal
	// OLD/NEW entries remain diagnostic candidates, not executable columns.
	ruleNamespace, previousNamespace, previousHidden := binder.target.ruleNamespace, binder.queryNamespace, binder.queryHidden
	if ruleNamespace != nil {
		binder.target.ruleNamespace = nil
		binder.queryHidden = append(append([]queryQualifier(nil), previousHidden...),
			queryQualifier{name: "old", original: "old", lateralForbidden: true},
			queryQualifier{name: "new", original: "new", lateralForbidden: true})
	}
	defer func() {
		binder.target.ruleNamespace, binder.queryNamespace, binder.queryHidden = ruleNamespace, previousNamespace, previousHidden
	}()
	target := binder.mutationTargetNamespace()
	binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	excluded := rebaseQueryNamespace(target, len(b.relation.Columns))
	excluded.qualifiers[0] = queryQualifier{name: "excluded", columns: excluded.columns, lateralForbidden: true}
	namespace := queryNamespace{
		columns:    append(target.columns, excluded.columns...),
		qualifiers: append(target.qualifiers, excluded.qualifiers...),
		frame:      append(append([]catalog.Column(nil), target.frame...), target.frame...),
	}
	binder.queryNamespace = &namespace
	binder.aggregateClause = "UPDATE"
	action := &boundUpdate{assignments: make([]expr.Expr, len(s.Conflict.Assignments))}
	actionBinding := *b
	actionBinding.targets = nil
	if err := e.bindUpdateAssignments(&actionBinding, binder, s.Conflict.Assignments, action); err != nil {
		return err
	}
	if s.Conflict.Where != nil {
		predicate, err := binder.bindBoolean(s.Conflict.Where, filterContext, "WHERE")
		if err != nil {
			return err
		}
		action.predicate = predicate
	}
	c.update = action
	c.updateTargets = actionBinding.targets
	c.actionReads = append([]int(nil), binder.target.uses...)
	c.sources = binder.querySources
	return nil
}

func (c *boundInsertConflict) preflightUpdate(evaluation *EvalContext) error {
	if c.update == nil {
		return nil
	}
	action, err := preflightUpdateWithPlanning(evaluation, c.update, true)
	if err != nil {
		return err
	}
	c.update = action
	return nil
}
