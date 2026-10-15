package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func jsonEachUnitPlan(t *testing.T, types map[ID]*Descriptor, name string) JSONEachPlan {
	t.Helper()
	value := types[JSON]
	if name == "json_each_text" {
		value = types[Text]
	}
	shape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: value.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BindJSONEach(name, types[JSON].Usage(), shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestJSONEachValuesAndPrivateCursors(t *testing.T) {
	types := testTypes()
	input := `{"a":1,"a":2,"\u0062":"x\n","empty":"","n":null,"s":"null","num":1.00e+02,"bool":false,"obj": { "z" : 1 },"arr":[1,  2],"huge":1e999999}`
	for _, name := range []string{"json_each", "json_each_text"} {
		t.Run(name, func(t *testing.T) {
			p := jsonEachUnitPlan(t, types, name)
			v := jsonFunctionUnitInput(t, types[JSON], input)
			first, err := p.Open(context.Background(), v)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			other, err := p.Open(context.Background(), v)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			want := [][]any{{"a", "1"}, {"a", "2"}, {"b", `"x\n"`}, {"empty", `""`}, {"n", "null"}, {"s", `"null"`}, {"num", "1.00e+02"}, {"bool", "false"}, {"obj", `{ "z" : 1 }`}, {"arr", `[1,  2]`}, {"huge", "1e999999"}}
			if name == "json_each_text" {
				want[2][1], want[3][1], want[4][1], want[5][1] = "x\n", "", nil, "null"
			}
			var retained []Value
			var got [][]any
			for {
				row, present, err := first.Next(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !present {
					if !row.IsNull() || row.Type() != p.ResultUsage().Type {
						t.Fatal("untyped exhaustion", row)
					}
					break
				}
				if row.Type() != p.ResultUsage().Type || row.CompositeLen() != 2 {
					t.Fatal("record shape", row)
				}
				key, value := row.CompositeField(0), row.CompositeField(1)
				if key.Type() != types[Text] || value.Type() != p.result.Type.CompositeMember(1).Usage.Type {
					t.Fatal("foreign record leaves", row)
				}
				k, _ := key.Text()
				var text any
				if !value.IsNull() {
					if name == "json_each" {
						text, err = value.JSONText()
					} else {
						text, err = value.Text()
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				got = append(got, []any{k, text})
				retained = append(retained, row)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%#v want=%#v", got, want)
			}
			first.Close()
			row, present, err := other.Next(context.Background())
			if err != nil || !present {
				t.Fatal("shared cursor position", row, err)
			}
			key, _ := row.CompositeField(0).Text()
			if key != "a" {
				t.Fatal("shared cursor index", key)
			}
			if key, _ := retained[0].CompositeField(0).Text(); key != "a" {
				t.Fatal("retained row mutated")
			}
			if _, _, err := first.Next(context.Background()); err == nil {
				t.Fatal("closed cursor accepted")
			}
			if stored, _ := v.JSONText(); stored != input {
				t.Fatal("input mutated")
			}
			for _, v := range []Value{Null(types[JSON]), jsonFunctionUnitInput(t, types[JSON], `{}`)} {
				cursor, err := p.Open(context.Background(), v)
				if err != nil {
					t.Fatal(err)
				}
				value, present, err := cursor.Next(context.Background())
				cursor.Close()
				if err != nil || present || !value.IsNull() || value.Type() != p.result.Type {
					t.Fatal("strict empty cursor", value, present, err)
				}
			}
		})
	}
}

func TestJSONEachEagerErrorsAndCancellation(t *testing.T) {
	types := testTypes()
	for _, name := range []string{"json_each", "json_each_text"} {
		p := jsonEachUnitPlan(t, types, name)
		for _, tc := range []struct{ input, code, message string }{
			{`[]`, "22023", "cannot deconstruct an array as an object"},
			{`[1]`, "22023", "cannot deconstruct an array as an object"},
			{`null`, "22023", "cannot deconstruct a scalar"},
			{`1`, "22023", "cannot deconstruct a scalar"},
			{`true`, "22023", "cannot deconstruct a scalar"},
			{`"s"`, "22023", "cannot deconstruct a scalar"},
			{`{"ok":1,"late":{"v":"\u0000"}}`, "22P05", "unsupported Unicode escape sequence"},
			{`{"ok":1,"late":["\ud800"]}`, "22P02", "invalid input syntax for type json"},
		} {
			cursor, err := p.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], tc.input))
			var d *sqlstate.Error
			if cursor.result != nil || !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Position != "" {
				t.Fatalf("%s: cursor=%v diagnostic=%v", tc.input, cursor, err)
			}
		}
		for _, input := range []string{
			`{"a":"` + strings.Repeat(`\u0041`, 10000) + `"}`,
			`{"a":` + strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000) + "}",
			"{" + strings.Repeat(`"a":1,`, 10000) + `"last":2}`,
		} {
			v := jsonFunctionUnitInput(t, types[JSON], input)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for _, ctx := range []context.Context{ctx, &groupingCancelContext{Context: context.Background(), at: 5}} {
				cursor, err := p.Open(ctx, v)
				if cursor.result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("partial canceled scan", cursor, err)
				}
			}
		}
		cursor, err := p.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], `{"a":1}`))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, present, err := cursor.Next(ctx); present || !errors.Is(err, context.Canceled) {
			t.Fatal("iteration cancellation", present, err)
		}
		cursor.Close()
		fresh := testTypes()
		if _, err := p.Open(context.Background(), Null(fresh[JSON])); err == nil {
			t.Fatal("foreign JSON generation accepted")
		}
		if _, err := BindJSONEach(name, types[Text].Usage(), p.result); err == nil {
			t.Fatal("TEXT input admitted")
		}
		if _, err := BindJSONEach(name, types[JSON].Usage(), types[Text].Usage()); err == nil {
			t.Fatal("scalar result admitted")
		}
		if _, err := BindJSONEach("jsonb_each", types[JSON].Usage(), p.result); err == nil {
			t.Fatal("JSON input admitted for JSONB signature")
		}
	}
}
