package engine

func (b *expressionBinder) lowerStringWindowSpecs(root LogicalNode, specs []boundWindowSpec, companions [][]BoundExpr) (LogicalNode, error) {
	var requests []BoundExpr
	for _, spec := range specs {
		requests = append(requests, spec.partition...)
		for _, key := range spec.order {
			requests = append(requests, key.expression)
		}
	}
	originalWidth := len(requests)
	requests, templates := stringSetPeerClosure(requests, companions)
	root, err := b.lowerStringSets(root, requests, companions, false, templates)
	requests = requests[:originalWidth]
	if err != nil {
		return nil, err
	}
	offset := 0
	for i := range specs {
		specs[i].partition = append([]BoundExpr(nil), requests[offset:offset+len(specs[i].partition)]...)
		offset += len(specs[i].partition)
		specs[i].order = append([]sortKey(nil), specs[i].order...)
		for j := range specs[i].order {
			specs[i].order[j].expression = requests[offset]
			offset++
		}
	}
	return root, nil
}
