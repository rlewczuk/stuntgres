package plan

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Literal evaluation needs only the runtime prologue, not frame or SQL services.
type literalRuntime struct{ Runtime }

func (literalRuntime) BeginEvaluation() error { return nil }

func TestLiteralQueryOnlyPseudoNull(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, id := range []sqltype.ID{2277, 5078, 3831, 5080, 4537, 4538, 32} {
		u := types[id].Usage()
		value := sqltype.Null(u.Type)
		node := LiteralExpr{Info: expr.Info{Usage: u}, Value: value}
		got, err := node.Eval(literalRuntime{})
		if err != nil || !got.IsNull() || got.Type() != u.Type {
			t.Fatalf("pseudo %d: value %v, error %v", id, got, err)
		}
		if u.Type.Concrete() {
			t.Fatalf("pseudo %d gained concrete authority", id)
		}
		if err := sqltype.CheckStoredColumn("v", u); err == nil {
			t.Fatalf("pseudo %d gained storage authority", id)
		}
		copied := *u.Type
		for _, bad := range []LiteralExpr{
			{Info: expr.Info{Usage: u}, Value: sqltype.Null(types[sqltype.Text])},
			{Info: expr.Info{Usage: u, UnknownNull: true}, Value: value},
			{Info: expr.Info{Usage: sqltype.LookupOnly(id, u.Type.Name(), 0).Usage()}, Value: value},
			{Info: expr.Info{Usage: copied.Usage()}, Value: value},
			{Info: expr.Info{Usage: sqltype.Usage{Type: u.Type, Typmod: 0}}, Value: value},
			{Info: expr.Info{Usage: sqltype.Usage{Type: u.Type, Typmod: -1, Collation: sqltype.CollationRef{ID: 950}}}, Value: value},
			{Info: expr.Info{Usage: types[2281].Usage()}, Value: value},
			{Info: expr.Info{Usage: types[2280].Usage()}, Value: value},
		} {
			if _, err := bad.Eval(literalRuntime{}); err == nil {
				t.Fatalf("pseudo %d accepted invalid literal %#v", id, bad)
			}
		}
	}
}
