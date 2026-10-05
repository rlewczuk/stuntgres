package engine

import (
	"context"
	"sort"
	"fmt"
	"stuntgres/internal/parser"
	"sync/atomic"

	"stuntgres/internal/sqltype"
)

// Snapshot retains immutable committed rows and their captured count. A nil
// selection means every captured row, without an index allocation.
type Snapshot struct {
	self       *Snapshot
	rows       [][]sqltype.Value
	projection []BoundExpr
	parameters []sqltype.Value
	evaluation EvalContext
	selection  []int
	committed  []rowVersion
	own        []rowVersion
	view       *VisibilityView
	closed     atomic.Bool
}

func anonymousSnapshot(rows [][]sqltype.Value, projection []int) *Snapshot {
	expressions := make([]BoundExpr,len(projection))
	for i,slot := range projection {
		var usage sqltype.Usage
		if len(rows)>0 { usage=rows[0][slot].Type().Usage() }
		expressions[i]=columnExpression{expressionInfo:expressionInfo{usage:usage,pos:parser.Position{Offset:-1},properties:ExprProperties{RowDependent:true}},scope:1,frame:1,slot:slot}
	}
	s := &Snapshot{rows: rows, projection: expressions}
	s.self = s
	return s
}
func versionSnapshot(view *VisibilityView, relation ObjectAddress, projection []BoundExpr) *Snapshot {
	retained := view.Retain()
	data := retained.read()
	s := &Snapshot{view: retained, committed: data.committed[relation], own: data.own[relation], projection: projection}
	s.self = s
	return s
}

// Close drops this result's ownership only, not another retained result/view.
func (s *Snapshot) Close() {
	if s != nil && s.self == s && s.closed.CompareAndSwap(false, true) {
		if s.view != nil {
			s.view.Release()
		}
		s.view = nil
		s.rows = nil
		s.committed = nil
		s.own = nil
		s.selection = nil
		s.projection = nil
		s.parameters = nil
		s.evaluation = EvalContext{}
	}
}
func (s *Snapshot) rowCount() int { return len(s.rows) + len(s.committed) + len(s.own) }
func (s *Snapshot) row(index int) []sqltype.Value {
	if s == nil || s.self != s || s.closed.Load() {
		panic("stuntgres: snapshot is closed or copied")
	}
	if index < len(s.rows) {
		return s.rows[index]
	}
	index -= len(s.rows)
	if index < len(s.committed) {
		return s.committed[index].values
	}
	return s.own[index-len(s.committed)].values
}

func (s *Snapshot) Len() int {
	if s == nil || s.self != s || s.closed.Load() {
		return 0
	}
	if s.selection != nil {
		return len(s.selection)
	}
	return s.rowCount()
}
func (s *Snapshot) Values(ctx context.Context,index int, dst []sqltype.Value) error {
	if s==nil || s.self!=s || s.closed.Load() || index<0 || index>=s.Len() || len(dst)!=len(s.projection) { return fmt.Errorf("stuntgres: invalid snapshot projection") }
	if s.selection != nil { index=s.selection[index] }
	s.evaluation = EvalContext{Context:ctx,Scope:1,Frame:1,Row:s.row(index),Parameters:s.parameters}
	for i, expression := range s.projection {
		value,err := expression.Eval(&s.evaluation)
		if err!=nil { return err }
		dst[i]=value
	}
	return ctx.Err()
}

func (s *Snapshot) selectRows(ctx context.Context, predicate BoundExpr, order int, ordering sqltype.Comparison, desc bool) error {
	if predicate != nil || order >= 0 {
		s.selection = make([]int, 0, s.rowCount())
		evaluation:=EvalContext{Context:ctx,Scope:1,Frame:1,Parameters:s.parameters}
		for i := range s.rowCount() {
			row := s.row(i)
			if err := ctx.Err(); err != nil {
				return err
			}
			if predicate != nil {
				evaluation.Row=row
				result,err := predicate.Eval(&evaluation)
				if err!=nil { return err }
				truth,err := TruthOf(result)
				if err!=nil { return err }
				if truth!=True { continue }
			}
			s.selection = append(s.selection, i)
		}
	}
	if order >= 0 {
		var sortErr error
		sort.SliceStable(s.selection, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			a, b := s.row(s.selection[i])[order], s.row(s.selection[j])[order]
			if a.IsNull() {
				return !b.IsNull() && desc
			}
			if b.IsNull() {
				return !desc
			}
			comparison, err := ordering.Compare(ctx, a, b)
			if err != nil {
				sortErr = err
				return false
			}
			if desc {
				return comparison > 0
			}
			return comparison < 0
		})
		if sortErr != nil {
			return sortErr
		}
	}
	return ctx.Err()
}
