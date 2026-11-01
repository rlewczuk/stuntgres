package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func armorSourceUnit(t *testing.T, input string, null bool, order *[]int) PgpArmorHeaders {
	t.Helper()
	types := formatUnitTypes()
	text := types[sqltype.Text]
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "key", Usage: text.Usage()}, {Attribute: 2, Name: "value", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	value := sqltype.String(text, input)
	if null {
		value = sqltype.Null(text)
	}
	return PgpArmorHeaders{Info: expr.Info{Usage: shape.Usage()}, Arg: binaryUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: value, order: order}}
}

func TestPgcryptoArmorCursorOwnership(t *testing.T) {
	var order []int
	n := armorSourceUnit(t, "-----BEGIN PGP MESSAGE-----\ndup: first\ndup: second\n: \n\n!bad body!\n=ZZZZ\n-----END PGP MESSAGE-----\n", false, &order)
	ctx := &binaryUnitContext{Context: context.Background()}
	a, err := n.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := n.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{0, 0}) {
		t.Fatal("child not evaluated once per open", order)
	}
	next := func(cursor *expr.SourceCursor, key, value string) {
		t.Helper()
		row, present, err := cursor.Next(context.Background())
		if err != nil || !present || row.Type() != n.Usage.Type || row.CompositeLen() != 2 {
			t.Fatal(row, present, err)
		}
		k, _ := row.CompositeField(0).Text()
		v, _ := row.CompositeField(1).Text()
		if k != key || v != value {
			t.Fatal(k, v)
		}
	}
	next(&a, "dup", "first")
	next(&a, "dup", "second")
	next(&b, "dup", "first")
	a.Close()
	if a.State != nil || a.Value.Type() != nil || !a.Exhausted {
		t.Fatal("close retains input", a)
	}
	next(&b, "dup", "second")
	next(&b, "", "")
	row, present, err := b.Next(context.Background())
	if err != nil || present || !row.IsNull() || row.Type() != n.Usage.Type || b.Value.Type() != nil {
		t.Fatal("typed exhaustion/release", row, present, err)
	}
	c, err := n.OpenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Next(canceled); !errors.Is(err, context.Canceled) || c.Value.Type() != nil || !c.Exhausted {
		t.Fatal("canceled cursor retains input", err)
	}
}

func TestPgcryptoArmorSourceValidation(t *testing.T) {
	ctx := &binaryUnitContext{Context: context.Background()}
	for _, tc := range []struct {
		input string
		null  bool
		rows  int
	}{
		{"", true, 0},
		{"-----BEGIN PGP MESSAGE-----\n\n-----END PGP MESSAGE-----\n", false, 0},
		{"-----BEGIN PGP MESSAGE-----\nfoo: bar\n-----END PGP MESSAGE-----\n", false, 1},
	} {
		var order []int
		n := armorSourceUnit(t, tc.input, tc.null, &order)
		cursor, err := n.OpenSource(ctx)
		if err != nil {
			t.Fatal(tc, err)
		}
		count := 0
		for {
			_, present, err := cursor.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			count++
		}
		if count != tc.rows || len(order) != 1 {
			t.Fatal(tc, count, order)
		}
		if admitted, set := n.SourceAdmission(); !admitted || !set || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.IntrinsicVolatility() != expr.Immutable {
			t.Fatal("source protocol")
		}
		rebuilt, err := n.Rebuild(n.Info, n.Children())
		if err != nil || !sqltype.SameRecordUsage(rebuilt.ResultUsage(), n.ResultUsage()) {
			t.Fatal(rebuilt, err)
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing child accepted")
		}
	}
	var order []int
	n := armorSourceUnit(t, "-----BEGIN PGP MESSAGE-----\nfoo: bar\ninvalid later header\n\n-----END PGP MESSAGE-----\n", false, &order)
	cursor, err := n.OpenSource(ctx)
	if err == nil || cursor.Type != nil || cursor.Advance != nil {
		t.Fatal("invalid later header published a cursor", cursor, err)
	}
}

func TestPgcryptoArmorTypedCarrier(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		name      string
		ids       []sqltype.ID
		signature string
		result    sqltype.ID
	}{
		{"armor", []sqltype.ID{sqltype.Bytea}, "17", sqltype.Text},
		{"armor", []sqltype.ID{sqltype.Bytea, 1009, 1009}, "17,1009,1009", sqltype.Text},
		{"dearmor", []sqltype.ID{sqltype.Text}, "25", sqltype.Bytea},
	} {
		args := make([]Argument, len(tc.ids))
		for i, id := range tc.ids {
			args[i].Usage = types[id].Usage()
		}
		recipe, resolution := ResolvePgcrypto(tc.name, args, pgcryptoUnitTypes(types))
		if resolution != Resolved || recipe.SQL != tc.signature || recipe.Result != tc.result {
			t.Fatal(tc, recipe, resolution)
		}
		children := make([]expr.Expr, len(args))
		var order []int
		for i, id := range tc.ids {
			children[i] = binaryUnitArgument{Info: expr.Info{Usage: types[id].Usage()}, value: sqltype.Null(types[id]), order: &order, index: i}
		}
		call, err := NewPgcrypto(expr.Info{Usage: types[tc.result].Usage()}, tc.name, children)
		if err != nil || call.Validate() != nil || call.Plan.Signature() != recipe {
			t.Fatal(call, err)
		}
		value, err := call.Eval(&binaryUnitContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != types[tc.result] || len(order) != len(children) {
			t.Fatal(value, order, err)
		}
		failure := errors.New("demanded armor child")
		children[len(children)-1] = binaryUnitArgument{Info: expr.Info{Usage: types[tc.ids[len(tc.ids)-1]].Usage()}, err: failure, order: &order}
		if _, err := call.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if _, err := call.Reconstruct(expr.Info{Usage: types[sqltype.Bool].Usage()}, children); err == nil {
			t.Fatal("wrong result admitted")
		}
	}
}
