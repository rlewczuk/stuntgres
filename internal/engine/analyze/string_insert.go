package analyze

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func (b *Binder) lowerStringInsert(out *Binding) error {
	row := out.Expressions[0]
	found := false
	for _, expr := range row {
		found = found || exec.QuerySetDepth(expr) != 0
	}
	if !found {
		return nil
	}
	projection := make([]expr.Expr, 0, len(row))
	targets := make([]int, 0, len(out.Targets))
	var partials []*plan.ArrayAssignmentTarget
	if len(out.ArrayTargets) != 0 {
		partials = make([]*plan.ArrayAssignmentTarget, 0, len(out.ArrayTargets))
	}
	schema := make(plan.QuerySchema, 0, len(row))
	for i, expr := range row {
		// DEFAULT remains demanded by the write scheduler once per emitted row.
		if expr == nil {
			continue
		}
		projection = append(projection, expr)
		targets = append(targets, out.Targets[i])
		schema = append(schema, out.Relation.Columns[out.Targets[i]])
		if partials != nil {
			partials = append(partials, out.ArrayTargets[i])
		}
	}
	root, err := b.LowerQuerySets(&plan.SingletonNode{}, projection, nil, true)
	if err != nil {
		return err
	}
	out.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: root, Expressions: projection, Frame: b.FrameID()}}
	out.CommandQuery = true
	out.Targets, out.ArrayTargets = targets, partials
	out.Expressions = nil
	out.QuerySources = b.Query.Sources
	return nil
}
