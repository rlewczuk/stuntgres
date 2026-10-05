package engine

import (
	"context"
	"sort"
	"sync/atomic"

	"stuntgres/internal/sqltype"
)

// Snapshot retains immutable committed rows and their captured count. A nil
// selection means every captured row, without an index allocation.
type Snapshot struct {
	self       *Snapshot
	rows       [][]sqltype.Value
	projection []int
	selection  []int
	committed  []rowVersion
	own        []rowVersion
	view       *VisibilityView
	closed     atomic.Bool
}

func anonymousSnapshot(rows [][]sqltype.Value, projection []int) *Snapshot {
	s := &Snapshot{rows: rows, projection: projection}
	s.self = s
	return s
}
func versionSnapshot(view *VisibilityView, relation ObjectAddress, projection []int) *Snapshot {
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
func (s *Snapshot) Values(index int, dst []sqltype.Value) {
	if s.selection != nil {
		index = s.selection[index]
	}
	row := s.row(index)
	for i, c := range s.projection {
		dst[i] = row[c]
	}
}

func (s *Snapshot) selectRows(ctx context.Context, predicate int, value sqltype.Value, equality sqltype.Equality, order int, ordering sqltype.Comparison, desc bool) error {
	if predicate >= 0 || order >= 0 {
		s.selection = make([]int, 0, s.rowCount())
		for i := range s.rowCount() {
			row := s.row(i)
			if err := ctx.Err(); err != nil { return err }
			if predicate >= 0 {
				result, err := equality.Evaluate(ctx,row[predicate],value)
				if err != nil { return err }
				if result.IsNull() { continue }
				matches, err := result.Boolean()
				if err != nil { return err }
				if !matches { continue }
			}
			s.selection = append(s.selection, i)
		}
	}
	if order >= 0 {
		var sortErr error
		sort.SliceStable(s.selection, func(i, j int) bool {
			if sortErr != nil { return false }
			a, b := s.row(s.selection[i])[order], s.row(s.selection[j])[order]
			if a.IsNull() { return !b.IsNull() && desc }
			if b.IsNull() { return !desc }
			comparison, err := ordering.Compare(ctx, a, b)
			if err != nil { sortErr = err; return false }
			if desc { return comparison > 0 }
			return comparison < 0
		})
		if sortErr != nil { return sortErr }
	}
	return ctx.Err()
}
