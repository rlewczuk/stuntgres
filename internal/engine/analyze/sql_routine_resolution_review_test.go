package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineUnknownCategorySelection(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	for _, tc := range []struct {
		name   string
		inputs [2][2]sqltype.ID
		want   int
	}{
		{"conflict before string", [2][2]sqltype.ID{{sqltype.Int4, sqltype.Text}, {sqltype.Bool, sqltype.Int4}}, 2},
		{"conflict after string", [2][2]sqltype.ID{{sqltype.Text, sqltype.Int4}, {sqltype.Int4, sqltype.Bool}}, 2},
		{"no category survivors", [2][2]sqltype.ID{{sqltype.Text, sqltype.Int4}, {sqltype.Int4, sqltype.Text}}, 2},
		{"string winner", [2][2]sqltype.ID{{sqltype.Int4, sqltype.Text}, {sqltype.Int4, sqltype.Int4}}, 1},
		{"preferred numeric winner", [2][2]sqltype.ID{{sqltype.Float8, sqltype.Text}, {sqltype.Numeric, sqltype.Text}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				candidates := make([]SQLCallable, 2)
				for i := range candidates {
					j := i
					if reverse {
						j = 1 - i
					}
					candidates[i].Inputs = []sqltype.Usage{types[tc.inputs[j][0]].Usage(), types[tc.inputs[j][1]].Usage()}
				}
				got := SelectSQLCallables(catalog.Read{}, candidates, []ComparisonOperand{{}, {}}, nil)
				if len(got) != tc.want {
					t.Fatalf("reverse=%v: got %d candidates, want %d", reverse, len(got), tc.want)
				}
				if tc.want == 1 && (got[0].Inputs[0].Type != types[tc.inputs[0][0]] || got[0].Inputs[1].Type != types[tc.inputs[0][1]]) {
					t.Fatalf("reverse=%v: wrong winner %v", reverse, got[0].Inputs)
				}
			}
		})
	}
}
