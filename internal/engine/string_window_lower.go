package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func (b *expressionBinder) LowerStringWindowSpecs(root plan.Node, specs []plan.WindowSpec, calls []plan.WindowCall, companions [][]expr.Expr) (plan.Node, error) {
	var requests []expr.Expr
	for _, spec := range specs {
		requests = append(requests, spec.Partition...)
		for _, key := range spec.Order {
			requests = append(requests, key.Expression)
		}
	}
	for _, call := range calls {
		if call.Argument != nil {
			requests = append(requests, call.Argument)
		}
		if call.Ordinal != nil {
			requests = append(requests, call.Ordinal)
		}
		if call.DefaultValue != nil {
			requests = append(requests, call.DefaultValue)
		}
	}
	originalWidth := len(requests)
	requests, templates := analyze.QuerySetPeerClosure(requests, companions)
	root, err := b.LowerQuerySets(root, requests, companions, false, templates)
	requests = requests[:originalWidth]
	if err != nil {
		return nil, err
	}
	offset := 0
	for i := range specs {
		specs[i].Partition = append([]expr.Expr(nil), requests[offset:offset+len(specs[i].Partition)]...)
		offset += len(specs[i].Partition)
		specs[i].Order = append([]plan.SortKey(nil), specs[i].Order...)
		for j := range specs[i].Order {
			specs[i].Order[j].Expression = requests[offset]
			offset++
		}
	}
	for i := range calls {
		if calls[i].Argument != nil {
			calls[i].Argument = requests[offset]
			offset++
		}
		if calls[i].Ordinal != nil {
			calls[i].Ordinal = requests[offset]
			offset++
		}
		if calls[i].DefaultValue != nil {
			calls[i].DefaultValue = requests[offset]
			offset++
		}
	}
	return root, nil
}
