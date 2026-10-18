package engine

import "stuntgres/internal/engine/expr"

func (b *expressionBinder) lowerStringWindowSpecs(root LogicalNode, specs []boundWindowSpec, calls []windowCall, companions [][]expr.Expr) (LogicalNode, error) {
	var requests []expr.Expr
	for _, spec := range specs {
		requests = append(requests, spec.partition...)
		for _, key := range spec.order {
			requests = append(requests, key.expression)
		}
	}
	for _, call := range calls {
		if call.argument != nil {
			requests = append(requests, call.argument)
		}
		if call.ordinal != nil {
			requests = append(requests, call.ordinal)
		}
		if call.defaultValue != nil {
			requests = append(requests, call.defaultValue)
		}
	}
	originalWidth := len(requests)
	requests, templates := querySetPeerClosure(requests, companions)
	root, err := b.lowerQuerySets(root, requests, companions, false, templates)
	requests = requests[:originalWidth]
	if err != nil {
		return nil, err
	}
	offset := 0
	for i := range specs {
		specs[i].partition = append([]expr.Expr(nil), requests[offset:offset+len(specs[i].partition)]...)
		offset += len(specs[i].partition)
		specs[i].order = append([]sortKey(nil), specs[i].order...)
		for j := range specs[i].order {
			specs[i].order[j].expression = requests[offset]
			offset++
		}
	}
	for i := range calls {
		if calls[i].argument != nil {
			calls[i].argument = requests[offset]
			offset++
		}
		if calls[i].ordinal != nil {
			calls[i].ordinal = requests[offset]
			offset++
		}
		if calls[i].defaultValue != nil {
			calls[i].defaultValue = requests[offset]
			offset++
		}
	}
	return root, nil
}
