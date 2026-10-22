package exec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestQueryBranchPruning(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	rows := make([][]sqltype.Value, 8)
	for i := range rows {
		v, err := sqltype.Integer(integer, int64(i))
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{v}
	}
	for _, c := range []struct {
		name      string
		selection QueryBranchSelection
		scan      bool
		want      []int
	}{
		{"all", QueryBranchSelection{}, false, []int{0, 1, 2, 3, 4, 5, 6, 7}},
		{"empty", QueryBranchSelection{Applied: true}, false, nil},
		{"intervals", QueryBranchSelection{Applied: true, Intervals: []QueryBranchInterval{{1, 3}, {5, 7}}}, false, []int{1, 2, 5, 6}},
		{"scan_intersection", QueryBranchSelection{Applied: true, Intervals: []QueryBranchInterval{{1, 3}, {5, 7}}}, true, []int{2, 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			child := Stage{Source: &Source{Anonymous: rows}, scanRange: c.scan, scanStart: 2, scanEnd: 6}
			cursor := queryBranchCursor{selection: c.selection}
			defer child.Close()
			for range 2 {
				var got []int
				for {
					i, present, err := cursor.next(context.Background(), &child)
					if err != nil {
						t.Fatal(err)
					}
					if !present {
						break
					}
					raw := child.Index(i)
					value, err := child.Source.Row(raw)[0].Integer()
					if err != nil || value != int64(raw) {
						t.Fatal("raw row identity changed", value, raw, err)
					}
					got = append(got, raw)
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Fatal("interval producer", got, c.want)
				}
				if _, err := child.Rewind(context.Background()); err != nil {
					t.Fatal(err)
				}
				cursor.position = 0
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := cursor.next(ctx, &child); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled selection", err)
			}
		})
	}
}
