package engine

import "fmt"

// Retention is an execution demand, independent of row-lock admission.
func queryHasVirtualReads(root LogicalNode) bool {
	found := false
	visitExecutableQuery(root, func(LogicalNode) {}, func(expr BoundExpr) {
		if lexicalHasVirtualReads(expr) {
			found = true
		}
	})
	return found
}

func boundHasVirtualReads(expr BoundExpr) bool {
	found := lexicalHasVirtualReads(expr)
	walkBoundExpression(expr, func(node BoundExpr) {
		if query, ok := node.(subqueryExpression); !found && ok && !query.cteDefinition {
			found = queryHasVirtualReads(query.queryPlan.Root)
		}
	})
	return found
}

func lexicalHasVirtualReads(expr BoundExpr) bool {
	found := false
	walkBoundExpression(expr, func(node BoundExpr) {
		if column, ok := node.(columnExpression); ok && column.virtual != nil && column.queryOccurrence != 0 {
			found = true
		}
	})
	return found
}

// Identity belongs to this frame, including when it is captured as Outer. The
// backing buffers are producer-owned and reused only when advancing this frame.
func (c *EvalContext) bindQueryRow(source *querySource, index int) {
	c.Row = source.row(index)
	c.bindQueryIdentity(source, index)
}

func (c *EvalContext) bindQueryIdentity(source *querySource, index int) {
	c.setQueryPresence(source, index)
	if c.query == nil || !c.query.virtualReads {
		return
	}
	if c.queryRowSource != source {
		c.queryOccurrences = appendQueryOccurrences(c.queryOccurrences[:0], source)
		c.queryRowSource = source
	}
	setQueryLineage(source, index)
	c.queryProvenance = appendQueryProvenance(c.queryProvenance[:0], source, index)
}

func (c *EvalContext) queryOperandAbsent(local int) (bool, error) {
	if c.query == nil || c.query.source == nil || local < 0 || len(c.queryOccurrences) != len(c.queryProvenance) {
		return false, fmt.Errorf("stuntgres: invalid virtual query identity")
	}
	bundle := c.query.source
	if bundle.sources != nil {
		if local >= len(bundle.sources) || bundle.sources[local] == nil {
			return false, fmt.Errorf("stuntgres: invalid virtual query source")
		}
	} else if local != 0 {
		return false, fmt.Errorf("stuntgres: invalid virtual query source")
	}
	source := querySourceAt(bundle, local)
	for source.input != nil {
		source = source.input
	}
	physical := source.occurrence
	if physical < 0 {
		return false, fmt.Errorf("stuntgres: invalid virtual query occurrence")
	}
	for i, occurrence := range c.queryOccurrences {
		if occurrence == physical {
			return c.queryProvenance[i] == absentQueryIndex, nil
		}
	}
	return false, fmt.Errorf("stuntgres: missing virtual query occurrence")
}

func (c *EvalContext) queryOperandSource(local int) (*querySource, int, error) {
	if c.query == nil || c.query.source == nil || local < 0 || len(c.queryOccurrences) != len(c.queryProvenance) {
		return nil, 0, fmt.Errorf("stuntgres: invalid virtual query identity")
	}
	bundle := c.query.source
	if bundle.sources != nil && (local >= len(bundle.sources) || bundle.sources[local] == nil) || bundle.sources == nil && local != 0 {
		return nil, 0, fmt.Errorf("stuntgres: invalid virtual query source")
	}
	source := querySourceAt(bundle, local)
	for source.input != nil {
		source = source.input
	}
	physical := source.occurrence
	for i, occurrence := range c.queryOccurrences {
		if occurrence != physical {
			continue
		}
		index := c.queryProvenance[i]
		if index != absentQueryIndex && bundle.locks != nil && occurrence >= 0 && occurrence < len(bundle.locks.leaves) {
			source = bundle.locks.leaves[occurrence].source
		}
		return source, index, nil
	}
	return nil, 0, fmt.Errorf("stuntgres: missing virtual query occurrence")
}
