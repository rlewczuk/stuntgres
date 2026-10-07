package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type joinNode struct {
	querySchema
	left, right LogicalNode
	predicate BoundExpr
	leftWidth, rightWidth int
	merges []BoundExpr
}

func preflightJoin(c *EvalContext, n *joinNode) (LogicalNode,error) {
	left,err := preflightQuery(c,n.left)
	if err != nil { return nil,err }
	right,err := preflightQuery(c,n.right)
	if err != nil { return nil,err }
	predicate := n.predicate
	changed := false
	if predicate != nil {
		predicate,changed,err = preflightCoercions(c,predicate)
		if err != nil { return nil,err }
	}
	var merges []BoundExpr
	for i, expression := range n.merges {
		folded,replacement,err := preflightCoercions(c,expression)
		if err != nil { return nil,err }
		if replacement {
			if merges == nil { merges = append([]BoundExpr(nil),n.merges...) }
			merges[i] = folded
		}
	}
	if changed || merges != nil || left != n.left || right != n.right {
		copy := *n
		copy.left,copy.right,copy.predicate = left,right,predicate
		if merges != nil { copy.merges = merges }
		return &copy,nil
	}
	return n,nil
}

func openJoinNode(ctx context.Context,state *queryState,n *joinNode) (indexedStage,error) {
	left,err := openQueryNode(ctx,state,n.left)
	if err != nil { return indexedStage{},err }
	defer left.Close()
	right,err := openQueryNode(ctx,state,n.right)
	if err != nil { return indexedStage{},err }
	defer right.Close()
	width := len(n.Schema())
	if n.leftWidth < 0 || n.rightWidth < 0 || n.leftWidth > width || n.rightWidth > width-n.leftWidth || len(n.merges) != width-n.leftWidth-n.rightWidth {
		return indexedStage{},fmt.Errorf("stuntgres: invalid join frame width")
	}
	frame := make([]sqltype.Value,width)
	out := &querySource{flatWidth:width,materialized:true}
	evaluation := EvalContext{Context:ctx,Scope:1,Frame:1,Parameters:state.parameters,Row:frame,sequenceScope:state.source.scope}
	maxInt := int(^uint(0)>>1)
	for i := range left.count() {
		lrow := left.source.row(left.index(i))
		if len(lrow) != n.leftWidth { return indexedStage{},fmt.Errorf("stuntgres: invalid left join frame") }
		copy(frame,lrow)
		for j := range right.count() {
			// A nonproductive pair loop must remain cancellable.
			if err := ctx.Err(); err != nil { return indexedStage{},err }
			rrow := right.source.row(right.index(j))
			if len(rrow) != n.rightWidth { return indexedStage{},fmt.Errorf("stuntgres: invalid right join frame") }
			copy(frame[n.leftWidth:],rrow)
			if n.predicate != nil {
				value,err := n.predicate.Eval(&evaluation)
				if err != nil { return indexedStage{},err }
				truth,err := TruthOf(value)
				if err != nil { return indexedStage{},err }
				if truth != True { continue }
			}
			for k,expression := range n.merges {
				value,err := expression.Eval(&evaluation)
				if err != nil { return indexedStage{},err }
				slot := n.leftWidth+n.rightWidth+k
				if value.Type() != n.Schema()[slot].Usage.Type { return indexedStage{},fmt.Errorf("stuntgres: invalid merged join value") }
				frame[slot] = value
			}
			if out.flatCount == maxInt || len(out.flat) > maxInt-width { return indexedStage{},fmt.Errorf("stuntgres: join result is too large") }
			out.flat = append(out.flat,frame...)
			out.flatCount++
		}
	}
	return indexedStage{source:out,schema:n.Schema()},nil
}
