package exec

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Retention is an execution demand, independent of row-lock admission.
func QueryHasIdentityReads(root plan.Node) bool {
	found := false
	plan.VisitExecutableQuery(root, func(plan.Node) {}, func(expr expr.Expr) {
		if lexicalHasIdentityReads(expr) {
			found = true
		}
	})
	return found
}

func BoundHasIdentityReads(bound expr.Expr) bool {
	found := lexicalHasIdentityReads(bound)
	plan.WalkExpression(bound, func(node expr.Expr) {
		if query, ok := node.(plan.SubqueryExpr); !found && ok && !query.CTEEdge {
			found = QueryHasIdentityReads(query.QueryPlan.Root)
		}
	})
	return found
}

func lexicalHasIdentityReads(bound expr.Expr) bool {
	found := false
	plan.WalkExpression(bound, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Virtual != nil && column.QueryOccurrence != 0 {
			found = true
		}
		if tableoid, ok := node.(plan.SystemAttributeExpr); ok && tableoid.QueryOccurrence != 0 {
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
	if c.Query == nil || !c.Query.IdentityReads {
		return
	}
	c.QueryRawXmax = c.Query.Source.RawXmax
	if locks := c.Query.Source.Locks; locks != nil && locks.RawXmax != nil {
		c.QueryRawXmax = locks.RawXmax
	}
	c.QueryProjectedVersions = ProjectedQueryVersions(source, index)
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
	_, index, err := source.queryMember(c.QueryOccurrences, c.QueryProvenance)
	return index == AbsentQueryIndex, err
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
	physical, index, err := source.queryMember(c.QueryOccurrences, c.QueryProvenance)
	if err != nil {
		return nil, 0, err
	}
	if index != AbsentQueryIndex && bundle.Locks != nil && physical >= 0 && physical < len(bundle.Locks.Leaves) {
		source = bundle.Locks.Leaves[physical].Source
	}
	return source, index, nil
}

// A logical hierarchy source has alternative physical members. Absence is the
// absence of every member, not the first sibling or the visible tuple's cells.
func (source *Source) queryMember(occurrences, provenance []int) (int, int, error) {
	found, physical := false, source.Occurrence
	for i, occurrence := range occurrences {
		member := occurrence == source.Occurrence
		if source.Occurrences != nil {
			member = false
			for _, candidate := range source.Occurrences {
				if occurrence == candidate {
					member = true
					break
				}
			}
		}
		if !member {
			continue
		}
		found, physical = true, occurrence
		if provenance[i] != AbsentQueryIndex {
			return occurrence, provenance[i], nil
		}
	}
	if found {
		return physical, AbsentQueryIndex, nil
	}
	return 0, 0, fmt.Errorf("stuntgres: missing virtual query occurrence")
}
