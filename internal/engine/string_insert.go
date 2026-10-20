package engine

import "stuntgres/internal/engine/expr"

func (b *expressionBinder) lowerStringInsert(out *binding) error {
	row := out.expressions[0]
	found := false
	for _, expr := range row {
		found = found || querySetDepth(expr) != 0
	}
	if !found {
		return nil
	}
	projection := make([]expr.Expr, 0, len(row))
	targets := make([]int, 0, len(out.targets))
	var partials []*arrayAssignmentTarget
	if len(out.arrayTargets) != 0 {
		partials = make([]*arrayAssignmentTarget, 0, len(out.arrayTargets))
	}
	schema := make(querySchema, 0, len(row))
	for i, expr := range row {
		// DEFAULT remains demanded by the write scheduler once per emitted row.
		if expr == nil {
			continue
		}
		projection = append(projection, expr)
		targets = append(targets, out.targets[i])
		schema = append(schema, out.relation.Columns[out.targets[i]])
		if partials != nil {
			partials = append(partials, out.arrayTargets[i])
		}
	}
	root, err := b.lowerQuerySets(&singletonNode{}, projection, nil, true)
	if err != nil {
		return err
	}
	out.query = &BoundQuery{Root: &projectNode{querySchema: schema, child: root, expressions: projection, frame: b.frameID()}}
	out.commandQuery = true
	out.targets, out.arrayTargets = targets, partials
	out.expressions = nil
	out.querySources = b.query.sources
	return nil
}
