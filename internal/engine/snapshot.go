package engine

import (
	"context"
	"sort"

	"stuntgres/internal/sqltype"
)

// Snapshot retains immutable committed rows and their captured count. A nil
// selection means every captured row, without an index allocation.
type Snapshot struct {
	rows [][]sqltype.Value
	projection []int
	selection []int
}

func (s *Snapshot) Len() int {
	if s.selection != nil { return len(s.selection) }
	return len(s.rows)
}
func (s *Snapshot) Values(index int, dst []sqltype.Value) {
	if s.selection != nil { index = s.selection[index] }
	row := s.rows[index]
	for i, column := range s.projection { dst[i] = row[column] }
}

func (s *Snapshot) selectRows(ctx context.Context, predicate int, value sqltype.Value, equality sqltype.Equality, order int, ordering sqltype.Comparison, desc bool) error {
	if predicate >= 0 || order >= 0 {
		s.selection = make([]int, 0, len(s.rows))
		for i, row := range s.rows {
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
			a, b := s.rows[s.selection[i]][order], s.rows[s.selection[j]][order]
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
