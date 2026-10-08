package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type joinNode struct {
	querySchema
	left, right           LogicalNode
	predicate             BoundExpr
	leftWidth, rightWidth int
	merges                []BoundExpr
}

func preflightJoin(c *EvalContext, n *joinNode) (LogicalNode, error) {
	left, err := preflightQuery(c, n.left)
	if err != nil {
		return nil, err
	}
	right, err := preflightQuery(c, n.right)
	if err != nil {
		return nil, err
	}
	predicate := n.predicate
	changed := false
	if predicate != nil {
		predicate, changed, err = preflightCoercions(c, predicate)
		if err != nil {
			return nil, err
		}
	}
	var merges []BoundExpr
	for i, expression := range n.merges {
		folded, replacement, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		if replacement {
			if merges == nil {
				merges = append([]BoundExpr(nil), n.merges...)
			}
			merges[i] = folded
		}
	}
	if changed || merges != nil || left != n.left || right != n.right {
		copy := *n
		copy.left, copy.right, copy.predicate = left, right, predicate
		if merges != nil {
			copy.merges = merges
		}
		return &copy, nil
	}
	return n, nil
}

func openJoinNode(ctx context.Context, state *queryState, n *joinNode) (indexedStage, error) {
	left,err := openQueryNode(ctx,state,n.left)
	if err != nil { return indexedStage{},err }
	right,err := openQueryNode(ctx,state,n.right)
	if err != nil { left.Close();return indexedStage{},err }
	width := len(n.Schema())
	if n.leftWidth < 0 || n.rightWidth < 0 || n.leftWidth > width || n.rightWidth > width-n.leftWidth || len(n.merges) != width-n.leftWidth-n.rightWidth {
		left.Close();right.Close()
		return indexedStage{},fmt.Errorf("stuntgres: invalid join frame width")
	}
	frame := make([]sqltype.Value,width)
	source := &querySource{flatWidth:width,materialized:true}
	locking := state.locking || state.source.locks != nil
	if locking {
		source.occurrences = appendQueryOccurrences(source.occurrences,left.source)
		source.occurrences = appendQueryOccurrences(source.occurrences,right.source)
	}
	if state.source.locks != nil { source.joinLeft,source.joinRight = left.source,right.source }
	out := indexedStage{source:source,schema:n.Schema(),selection:[]int{}}
	if len(left.pending)+len(right.pending)>0 {
		out.pending = make([]queryPendingMark,len(left.pending)+len(right.pending))
		copy(out.pending,left.pending)
		copy(out.pending[len(left.pending):],right.pending)
	}
	out.finish = func() error { err := left.Close();other := right.Close();if err != nil { return err };return other }
	evaluation := EvalContext{Scope:1,Frame:1,Parameters:state.parameters,Row:frame,statementScope:state.source.scope,Input:state.input,Output:state.output}
	leftOccurrence,rightOccurrence := -1,0
	out.produce = func(ctx context.Context,out *indexedStage) (bool,error) {
		for {
			if err := ctx.Err(); err != nil { return false,err }
			if leftOccurrence < 0 {
				i,present,err := left.nextOccurrence(ctx)
				if err != nil || !present { return present,err }
				leftOccurrence,rightOccurrence = i,0
				row := left.source.row(left.index(i))
				if len(row) != n.leftWidth { return false,fmt.Errorf("stuntgres: invalid left join frame") }
				copy(frame,row)
			}
			present,err := right.ensure(ctx,rightOccurrence)
			if err != nil { return false,err }
			if !present { leftOccurrence = -1;continue }
			j := rightOccurrence
			rightOccurrence++
			row := right.source.row(right.index(j))
			if len(row) != n.rightWidth { return false,fmt.Errorf("stuntgres: invalid right join frame") }
			copy(frame[n.leftWidth:],row)
			evaluation.Context = ctx
			if n.predicate != nil {
				matches,err := updateMatches(&evaluation,n.predicate)
				if err != nil { return false,err }
				if !matches { continue }
			}
			for k,expr := range n.merges {
				value,err := expr.Eval(&evaluation)
				if err != nil { return false,err }
				if err := validateProducedValue(value,n.Schema()[n.leftWidth+n.rightWidth+k].Usage); err != nil { return false,err }
				frame[n.leftWidth+n.rightWidth+k] = value
			}
			maxInt := int(^uint(0)>>1)
			if source.flatCount == maxInt || len(source.flat) > maxInt-width { return false,fmt.Errorf("stuntgres: join result is too large") }
			source.flat = append(source.flat,frame...)
			if locking {
				source.provenance = appendQueryProvenance(source.provenance,left.source,left.index(leftOccurrence))
				source.provenance = appendQueryProvenance(source.provenance,right.source,right.index(j))
				if state.source.locks != nil {
					source.versions = appendQueryVersions(source.versions,left.source,left.index(leftOccurrence),state.source.locks)
					source.versions = appendQueryVersions(source.versions,right.source,right.index(j),state.source.locks)
					source.joinPairs = append(source.joinPairs,queryPair{left:left.index(leftOccurrence),right:right.index(j)})
				}
			}
			out.selection = append(out.selection,source.flatCount)
			source.flatCount++
			return true,ctx.Err()
		}
	}
	return out,nil
}
