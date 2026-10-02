package engine

import (
	"context"
	"database/sql/driver"
	"sort"
)

// Snapshot retains immutable committed rows at the statement's capture point.
// A nil selection means every captured row, without an index allocation.
type Snapshot struct {
	rows       [][]driver.Value
	projection []int
	selection  []int
}

func (s *Snapshot) Len() int {
	if s.selection != nil {
		return len(s.selection)
	}
	return len(s.rows)
}
func (s *Snapshot) Values(index int, dst []driver.Value) {
	if s.selection != nil {
		index = s.selection[index]
	}
	row := s.rows[index]
	for i, c := range s.projection {
		dst[i] = row[c]
	}
}
func compare(a, b driver.Value) int {
	switch a := a.(type) {
	case int64:
		v := b.(int64)
		if a < v {
			return -1
		}
		if a > v {
			return 1
		}
	case string:
		v := b.(string)
		if a < v {
			return -1
		}
		if a > v {
			return 1
		}
	case bool:
		v := b.(bool)
		if !a && v {
			return -1
		}
		if a && !v {
			return 1
		}
	}
	return 0
}
func (s *Snapshot) selectRows(ctx context.Context, predicate int, value driver.Value, order int, desc bool) error {
	if predicate >= 0 || order >= 0 {
		s.selection = make([]int, 0, len(s.rows))
		for i, row := range s.rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if predicate >= 0 && (value == nil || row[predicate] == nil || compare(row[predicate], value) != 0) {
				continue
			}
			s.selection = append(s.selection, i)
		}
	}
	if order >= 0 {
		sort.SliceStable(s.selection, func(i, j int) bool {
			a, b := s.rows[s.selection[i]][order], s.rows[s.selection[j]][order]
			if a == nil {
				return b != nil && desc
			}
			if b == nil {
				return !desc
			}
			c := compare(a, b)
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	return ctx.Err()
}
