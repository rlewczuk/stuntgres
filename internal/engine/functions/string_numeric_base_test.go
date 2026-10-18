package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringNumericBaseWidthRuntime(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Int8} {
		integer, text := types[id], types[sqltype.Text]
		value, err := sqltype.Integer(integer, -1)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := sqltype.BindString("to_hex", text.Usage(), text.Usage(), []sqltype.Usage{integer.Usage()}, [4]bool{}, false)
		if err != nil {
			t.Fatal(err)
		}
		var order []int
		node := String{Info: expr.Info{Usage: text.Usage()}, Plan: plan, Args: []expr.Expr{
			stringUnitArgument{Info: expr.Info{Usage: integer.Usage()}, value: value, order: &order},
		}}
		got, err := node.Eval(&stringUnitContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := got.Text()
		want := "ffffffff"
		if id == sqltype.Int8 {
			want = "ffffffffffffffff"
		}
		if err != nil || actual != want {
			t.Fatal(actual, err)
		}
	}
}
func TestStringNumericBaseMD5Runtime(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, tc := range []struct {
		id           sqltype.ID
		source, want string
		null         bool
	}{
		{sqltype.Text, "abc", "900150983cd24fb0d6963f7d28e17f72", false},
		{sqltype.Char, "a", "0cc175b9c0f1b6a831c399e269772661", false},
		{sqltype.Char, "", "", true},
	} {
		source, text := types[tc.id], types[sqltype.Text]
		value := sqltype.String(source, tc.source)
		if tc.null {
			value = sqltype.Null(source)
		}
		plan, err := sqltype.BindString("md5", text.Usage(), text.Usage(), []sqltype.Usage{source.Usage()}, [4]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		var order []int
		node := String{Info: expr.Info{Usage: text.Usage()}, Plan: plan, Textual: [4]bool{true}, Args: []expr.Expr{
			stringUnitArgument{Info: expr.Info{Usage: source.Usage()}, value: value, order: &order},
		}}
		got, err := node.Eval(&stringUnitContext{Context: context.Background()})
		if err != nil || got.Type() != text || got.IsNull() != tc.null {
			t.Fatal(got, err)
		}
		if !tc.null {
			actual, err := got.Text()
			if err != nil || actual != tc.want {
				t.Fatal(actual, err)
			}
		}
	}
}
