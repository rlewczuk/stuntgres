package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func queryIntUnitType(t *testing.T, id ID) *Descriptor {
	t.Helper()
	d, err := QueryInt(id, "query_int", "public.query_int")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestQueryIntInput(t *testing.T) {
	d := queryIntUnitType(t, 45000)
	ctx := context.Background()
	for _, tc := range []struct{ text, want string }{
		{" 1 ", "1"}, {"-", "0"}, {"010", "8"}, {"09", "0"}, {"018", "1"}, {"-010", "-8"}, {"-0", "0"},
		{"2147483647", "2147483647"}, {"-2147483648", "-2147483648"},
		{strings.Repeat("0", 15), "0"}, {"-" + strings.Repeat("0", 14), "0"},
		{strings.Repeat("(", 128) + "1" + strings.Repeat(")", 128), "1"},
	} {
		v, err := d.Input(ctx, tc.text, -1)
		if err != nil || v.Type() != d || v.IsNull() || !v.validQueryInt() {
			t.Fatalf("input %q value=%#v err=%v", tc.text, v, err)
		}
		text, err := d.Output(ctx, v)
		if err != nil || text != tc.want {
			t.Fatalf("input %q output=%q want=%q err=%v", tc.text, text, tc.want, err)
		}
	}
	for _, text := range []string{"", " ", "\t1", "1\n", "1\r", "\u00a01", "+1", "1 2", "1&&2", "1||2", "1&", "!", "()", "(1", "1)", "1a", "0x10", "2147483648", "-2147483649", strings.Repeat("0", 16), "-" + strings.Repeat("0", 15), strings.Repeat("!", 17) + "1"} {
		v, err := d.Input(ctx, text, -1)
		code, message := "42601", "syntax error"
		if text == strings.Repeat("!", 17)+"1" {
			code, message = "54001", "statement too complex"
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Detail != "" || diagnostic.Hint != "" || v.Type() != nil {
			t.Fatalf("input %q value=%#v diagnostic=%#v err=%v", text, v, diagnostic, err)
		}
	}
	if _, err := d.Input(ctx, "1", 2); err == nil {
		t.Fatal("unowned modifier admitted")
	}
}

func TestQueryIntOutput(t *testing.T) {
	d := queryIntUnitType(t, 45000)
	ctx := context.Background()
	for _, tc := range []struct{ input, want string }{
		{"1&(2|3)", "1 & ( 2 | 3 )"}, {"1|2&3", "1 | 2 & 3"}, {"(1|2)&3", "( 1 | 2 ) & 3"},
		{"!!1", "!( !1 )"}, {"!(!1|2)", "!( !1 | 2 )"}, {"!(1&2)", "!( 1 & 2 )"},
		{"1|2|3", "( 1 | 2 ) | 3"}, {"1|(2|3)", "1 | ( 2 | 3 )"}, {"1&1", "1 & 1"}, {"3&2&1", "3 & 2 & 1"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		text, err := d.Output(ctx, v)
		if err != nil || text != tc.want {
			t.Fatalf("output %q got=%q want=%q err=%v", tc.input, text, tc.want, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.want+"'" {
			t.Fatalf("literal %q err=%v", literal, err)
		}
		copy, err := d.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		again, err := d.Output(ctx, copy)
		if err != nil || again != tc.want {
			t.Fatalf("canonical reinput %q err=%v", again, err)
		}
	}
	nots := "!1"
	for i := 1; i < 16; i++ {
		nots = "!( " + nots + " )"
	}
	v, err := d.Input(ctx, strings.Repeat("!", 16)+"1", -1)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := d.Output(ctx, v); err != nil || text != nots {
		t.Fatalf("sixteen NOTs %q err=%v", text, err)
	}
	for _, n := range []int{15, 16, 17, 32} {
		want := "1 | 1"
		for i := 1; i < n; i++ {
			want = "( " + want + " ) | 1"
		}
		v, err := d.Input(ctx, strings.Repeat("1|", n)+"1", -1)
		if err != nil {
			t.Fatal(err)
		}
		if text, err := d.Output(ctx, v); err != nil || text != want {
			t.Fatalf("flat OR%d %q err=%v", n, text, err)
		}
	}
}

func TestQueryIntIdentity(t *testing.T) {
	ctx := context.Background()
	d, same, foreign := queryIntUnitType(t, 45000), queryIntUnitType(t, 45000), queryIntUnitType(t, 45001)
	if !d.IsQueryInt() || !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "QUERY_INT" || d.PostgreSQLTypeLength() != -1 {
		t.Fatal("extension base identity")
	}
	v, err := d.Input(ctx, "1&(2|3)", -1)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]queryIntNode(nil), v.queryInt.nodes...)
	retagged, err := v.Retag(same)
	if err != nil || retagged.queryInt != v.queryInt || retagged.Type() != same {
		t.Fatal("same identity must share immutable payload", err)
	}
	mapped, err := v.RemapQueryInt(foreign)
	if err != nil || mapped.queryInt != v.queryInt || mapped.Type() != foreign || !reflect.DeepEqual(before, v.queryInt.nodes) {
		t.Fatal("authorized import mutated/copied datum", err)
	}
	for _, value := range []Value{v, Null(d)} {
		if _, err := value.Retag(foreign); err == nil {
			t.Fatal("ordinary retag changed OID")
		}
		if _, err := value.Retag(&Descriptor{id: d.ID(), name: "query_int", sql: "public.query_int"}); err == nil {
			t.Fatal("ordinary retag accepted wrong family")
		}
		if remapped, err := value.RemapQueryInt(foreign); err != nil || remapped.IsNull() != value.IsNull() {
			t.Fatal("import NULL/value", err)
		}
	}
	for _, bad := range []Value{{typ: d}, {typ: d, queryInt: &queryIntPayload{}}, {typ: d, queryInt: &queryIntPayload{nodes: []queryIntNode{{op: '!', right: 0}}}}} {
		if _, err := bad.Retag(same); err == nil {
			t.Fatal("invalid payload retagged")
		}
		if _, err := d.Output(ctx, bad); err == nil {
			t.Fatal("invalid payload rendered")
		}
	}
	for _, context := range []CoercionContext{Implicit, Assignment} {
		if _, err := PlanCoercion((&Descriptor{id: Text, name: "text", sql: "TEXT"}).Usage(), d.Usage(), context); err == nil {
			t.Fatal("typed TEXT implicit input")
		}
	}
	text := &Descriptor{id: Text, name: "text", sql: "TEXT"}
	plan, err := PlanCoercion(text.Usage(), d.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := plan.Apply(ctx, String(text, "010"), InputEnvironment{}, OutputEnvironment{}); err != nil || imported.Type() != d {
		t.Fatal("explicit input", err)
	}
	if _, err := Array(45002, "_query_int", d); err == nil {
		t.Fatal("query_int array storage admitted")
	}
}

func TestQueryIntCoercion(t *testing.T) {
	ctx := context.Background()
	query := queryIntUnitType(t, 45000)
	value, err := query.Input(ctx, "1&(2|3)", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, character := range []*Descriptor{
		{id: Text, name: "text", sql: "TEXT"},
		{id: VarChar, name: "varchar", sql: "VARCHAR"},
	} {
		for _, mode := range []CoercionContext{Assignment, Explicit} {
			plan, err := PlanCoercion(query.Usage(), character.Usage(), mode)
			if err != nil || plan.method != viaIO {
				t.Fatal("character output plan", character.Name(), mode, plan, err)
			}
			for _, source := range []Value{value, Null(query)} {
				got, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
				if err != nil || got.Type() != character || got.IsNull() != source.IsNull() {
					t.Fatal("character output identity/NULL", character.Name(), mode, got, err)
				}
				if !got.IsNull() {
					text, err := got.Text()
					if err != nil || text != "1 & ( 2 | 3 )" {
						t.Fatal("canonical character assignment", text, err)
					}
				}
			}
		}
		for _, mode := range []CoercionContext{Implicit, Assignment} {
			if _, err := PlanCoercion(character.Usage(), query.Usage(), mode); err == nil {
				t.Fatal("character input admitted", character.Name(), mode)
			}
		}
		if _, err := PlanCoercion(query.Usage(), character.Usage(), Implicit); err == nil {
			t.Fatal("implicit character output admitted", character.Name())
		}
		plan, err := PlanCoercion(character.Usage(), query.Usage(), Explicit)
		if err != nil {
			t.Fatal("explicit character input", err)
		}
		for _, source := range []Value{String(character, "010"), Null(character)} {
			got, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
			if err != nil || got.Type() != query || got.IsNull() != source.IsNull() {
				t.Fatal("explicit input identity/NULL", got, err)
			}
			if !got.IsNull() {
				text, err := query.Output(ctx, got)
				if err != nil || text != "8" {
					t.Fatal("explicit input canonical value", text, err)
				}
			}
		}
	}
}

// Deterministic cancellation at codec checkpoints, not sleeps or a huge input.
// A fresh instance is used for each call and never shared across goroutines.
type queryIntCancelContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
}

func (c *queryIntCancelContext) Err() error {
	c.calls++
	if c.calls == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestQueryIntCancellation(t *testing.T) {
	d := queryIntUnitType(t, 45000)
	for _, input := range []string{strings.Repeat("1&", 4096) + "1", strings.Repeat("(", 4096) + "1" + strings.Repeat(")", 4096)} {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &queryIntCancelContext{Context: base, cancel: cancel}
		v, err := d.Input(ctx, input, -1)
		if !errors.Is(err, context.Canceled) || v.Type() != nil || ctx.calls != 3 {
			t.Fatal("input cancellation", ctx.calls, v, err)
		}
	}
	v, err := d.Input(context.Background(), strings.Repeat("1|", 4096)+"1", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, render := range []func(context.Context, Value) (string, error){d.Output, d.SQLLiteral} {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &queryIntCancelContext{Context: base, cancel: cancel}
		text, err := render(ctx, v)
		if !errors.Is(err, context.Canceled) || text != "" || ctx.calls != 3 {
			t.Fatal("output cancellation", ctx.calls, text, err)
		}
	}
}
