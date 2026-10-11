package engine

import (
	"stuntgres/internal/parser"
)

func (e *Engine) bindInsertConflictUpdate(b *binding, binder *expressionBinder, s *parser.Insert) error {
	if s.Conflict == nil || !s.Conflict.Update {
		return nil
	}
	c := b.conflict
	c.width = len(b.relation.columns)
	binder.relation, binder.ref = b.relation, b.ref
	binder.targetName, binder.targetAlias = b.relation.name, s.Alias.Text
	binder.updateParameters = true
	defer func() { binder.updateParameters = false; binder.updateInputParameters = nil }()
	target := binder.mutationTargetNamespace()
	binder.queryHidden = append(binder.queryHidden, target.qualifiers[0])
	excluded := rebaseQueryNamespace(target, len(b.relation.columns))
	excluded.qualifiers[0] = queryQualifier{name: "excluded", columns: excluded.columns, lateralForbidden: true}
	namespace := queryNamespace{
		columns:    append(target.columns, excluded.columns...),
		qualifiers: append(target.qualifiers, excluded.qualifiers...),
		frame:      append(append([]ColumnDescriptor(nil), target.frame...), target.frame...),
	}
	binder.queryNamespace = &namespace
	binder.aggregateClause = "UPDATE"
	action := &boundUpdate{assignments: make([]BoundExpr, len(s.Conflict.Assignments))}
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
	c.actionReads = append([]int(nil), binder.uses...)
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
