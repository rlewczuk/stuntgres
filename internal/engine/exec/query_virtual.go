package exec

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Retention is an execution demand, independent of row-lock admission.
func QueryHasVirtualReads(root plan.Node) bool {
	found := false
	plan.VisitExecutableQuery(root, func(plan.Node) {}, func(expr expr.Expr) {
		if lexicalHasVirtualReads(expr) {
			found = true
		}
	})
	return found
}

func BoundHasVirtualReads(bound expr.Expr) bool {
	found := lexicalHasVirtualReads(bound)
	plan.WalkExpression(bound, func(node expr.Expr) {
		if query, ok := node.(plan.SubqueryExpr); !found && ok && !query.CTEEdge {
			found = QueryHasVirtualReads(query.QueryPlan.Root)
		}
	})
	return found
}

func lexicalHasVirtualReads(bound expr.Expr) bool {
	found := false
	plan.WalkExpression(bound, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Virtual != nil && column.QueryOccurrence != 0 {
			found = true
		}
	})
	return found
}

// Identity belongs to this frame, including when it is captured as Outer. The
// backing buffers are producer-owned and reused only when advancing this frame.
func (c *Context) BindQueryRow(source *Source, index int) {
	c.Row = source.Row(index)
	c.BindQueryIdentity(source, index)
}

func (c *Context) BindQueryIdentity(source *Source, index int) {
	c.setQueryPresence(source, index)
	if c.Query == nil || !c.Query.VirtualReads {
		return
	}
	if c.queryRowSource != source {
		c.QueryOccurrences = AppendQueryOccurrences(c.QueryOccurrences[:0], source)
		c.queryRowSource = source
	}
	SetQueryLineage(source, index)
	c.QueryProvenance = AppendQueryProvenance(c.QueryProvenance[:0], source, index)
}

func (c *Context) queryOperandAbsent(local int) (bool, error) {
	if c.Query == nil || c.Query.Source == nil || local < 0 || len(c.QueryOccurrences) != len(c.QueryProvenance) {
		return false, fmt.Errorf("stuntgres: invalid virtual query identity")
	}
	bundle := c.Query.Source
	if bundle.Sources != nil {
		if local >= len(bundle.Sources) || bundle.Sources[local] == nil {
			return false, fmt.Errorf("stuntgres: invalid virtual query source")
		}
	} else if local != 0 {
		return false, fmt.Errorf("stuntgres: invalid virtual query source")
	}
	source := QuerySourceAt(bundle, local)
	for source.Input != nil {
		source = source.Input
	}
	physical := source.Occurrence
	if physical < 0 {
		return false, fmt.Errorf("stuntgres: invalid virtual query occurrence")
	}
	for i, occurrence := range c.QueryOccurrences {
		if occurrence == physical {
			return c.QueryProvenance[i] == AbsentQueryIndex, nil
		}
	}
	return false, fmt.Errorf("stuntgres: missing virtual query occurrence")
}

func (c *Context) queryOperandSource(local int) (*Source, int, error) {
	if c.Query == nil || c.Query.Source == nil || local < 0 || len(c.QueryOccurrences) != len(c.QueryProvenance) {
		return nil, 0, fmt.Errorf("stuntgres: invalid virtual query identity")
	}
	bundle := c.Query.Source
	if bundle.Sources != nil && (local >= len(bundle.Sources) || bundle.Sources[local] == nil) || bundle.Sources == nil && local != 0 {
		return nil, 0, fmt.Errorf("stuntgres: invalid virtual query source")
	}
	source := QuerySourceAt(bundle, local)
	for source.Input != nil {
		source = source.Input
	}
	physical := source.Occurrence
	for i, occurrence := range c.QueryOccurrences {
		if occurrence != physical {
			continue
		}
		index := c.QueryProvenance[i]
		if index != AbsentQueryIndex && bundle.Locks != nil && occurrence >= 0 && occurrence < len(bundle.Locks.Leaves) {
			source = bundle.Locks.Leaves[occurrence].Source
		}
		return source, index, nil
	}
	return nil, 0, fmt.Errorf("stuntgres: missing virtual query occurrence")
}
