package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func jsonbEachUnitPlan(t *testing.T, types map[ID]*Descriptor, name string) JSONEachPlan {
	t.Helper()
	value := types[JSONB]
	if name == "jsonb_each_text" {
		value = types[Text]
	}
	shape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: value.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BindJSONEach(name, types[JSONB].Usage(), shape.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestJSONBEachValuesAndPrivateCursors(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, name := range []string{"jsonb_each", "jsonb_each_text"} {
		t.Run(name, func(t *testing.T) {
			plan := jsonbEachUnitPlan(t, types, name)
			input := jsonFunctionUnitInput(t, types[JSONB], `{"zz":0,"a":1,"a":null,"b":"x\n","o":{"z":0,"a":2},"d":1.00}`)
			first, err := plan.Open(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			other, err := plan.Open(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			want := [][]any{{"a", "null"}, {"b", `"x\n"`}, {"d", "1.00"}, {"o", `{"a": 2, "z": 0}`}, {"zz", "0"}}
			if name == "jsonb_each_text" {
				want[0][1], want[1][1] = nil, "x\n"
			}
			var got [][]any
			var retained []Value
			for {
				row, present, err := first.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !present {
					if !row.IsNull() || row.Type() != plan.result.Type {
						t.Fatal("untyped exhaustion", row)
					}
					break
				}
				if row.Type() != plan.result.Type || row.CompositeLen() != 2 {
					t.Fatal("record shape", row)
				}
				k, v := row.CompositeField(0), row.CompositeField(1)
				if k.Type() != types[Text] || v.Type() != plan.result.Type.CompositeMember(1).Usage.Type {
					t.Fatal("foreign leaves", row)
				}
				key, _ := k.Text()
				var text any
				if !v.IsNull() {
					if name == "jsonb_each" {
						if v.jsonb == nil {
							t.Fatal("missing real JSONB payload")
						}
						text, err = v.Type().Output(ctx, v)
					} else {
						text, err = v.Text()
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if key == "o" && name == "jsonb_each" && &v.jsonb.root.fields[0] != &input.jsonb.root.fields[3].value.fields[0] {
					t.Fatal("nested payload copied")
				}
				got = append(got, []any{key, text})
				retained = append(retained, row)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%#v want=%#v", got, want)
			}
			first.Close()
			row, present, err := other.Next(ctx)
			key, _ := row.CompositeField(0).Text()
			if err != nil || !present || key != "a" {
				t.Fatal("cursor state shared", row, present, err)
			}
			if key, _ := retained[0].CompositeField(0).Text(); key != "a" {
				t.Fatal("retained row changed")
			}
			if _, _, err := first.Next(ctx); err == nil {
				t.Fatal("closed cursor accepted")
			}
			for _, v := range []Value{Null(types[JSONB]), jsonFunctionUnitInput(t, types[JSONB], `{}`)} {
				cursor, err := plan.Open(ctx, v)
				if err != nil {
					t.Fatal(err)
				}
				row, present, err := cursor.Next(ctx)
				cursor.Close()
				if err != nil || present || !row.IsNull() || row.Type() != plan.result.Type {
					t.Fatal("strict empty", row, present, err)
				}
			}
		})
	}
}

func TestJSONBEachValidationAndCancellation(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, name := range []string{"jsonb_each", "jsonb_each_text"} {
		plan := jsonbEachUnitPlan(t, types, name)
		for _, input := range []string{`[]`, `[1]`, `null`, `0`, `true`, `false`, `"x"`} {
			cursor, err := plan.Open(ctx, jsonFunctionUnitInput(t, types[JSONB], input))
			var diagnostic *sqlstate.Error
			if cursor.result != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "cannot call "+name+" on a non-object" || diagnostic.Position != "" {
				t.Fatal(cursor, err)
			}
		}
		for _, v := range []Value{Null(testTypes()[JSONB]), Null(types[JSON]), {typ: types[JSONB]}} {
			if _, err := plan.Open(ctx, v); err == nil {
				t.Fatal("invalid descriptor/payload accepted")
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		value := jsonFunctionUnitInput(t, types[JSONB], `{"a":1}`)
		if _, err := plan.Open(canceled, value); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		cursor, err := plan.Open(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := cursor.Next(canceled); present || !errors.Is(err, context.Canceled) {
			t.Fatal(present, err)
		}
		cursor.Close()
		large := jsonFunctionUnitInput(t, types[JSONB], `{"a":{"nested":"`+strings.Repeat("x", 100000)+`"}}`)
		cursor, err = plan.Open(ctx, large)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := cursor.Next(&groupingCancelContext{Context: ctx, at: 5}); present || !errors.Is(err, context.Canceled) || cursor.index != 0 {
			t.Fatal("partial render advanced", present, err)
		}
		row, present, err := cursor.Next(ctx)
		cursor.Close()
		if err != nil || !present || row.CompositeLen() != 2 {
			t.Fatal("render recovery", row, present, err)
		}
		for _, input := range []Usage{Usage{}, types[JSON].Usage(), {Type: types[JSONB], Typmod: 1}} {
			if _, err := BindJSONEach(name, input, plan.result); err == nil {
				t.Fatal("invalid input usage accepted")
			}
		}
		if _, err := BindJSONEach("other", types[JSONB].Usage(), plan.result); err == nil {
			t.Fatal("invalid name accepted")
		}
		if _, err := BindJSONEach(name, types[JSONB].Usage(), types[Text].Usage()); err == nil {
			t.Fatal("scalar result accepted")
		}
		for _, result := range []Usage{Usage{}, {Type: plan.result.Type, Typmod: 1}} {
			if _, err := BindJSONEach(name, types[JSONB].Usage(), result); err == nil {
				t.Fatal("invalid result usage accepted")
			}
		}
		valueType := types[JSONB]
		if name == "jsonb_each_text" {
			valueType = types[Text]
		}
		for _, members := range [][]CompositeMember{
			{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: types[JSON].Usage()}},
			{{Attribute: 1, Name: "key", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "value", Usage: valueType.Usage()}},
			{{Attribute: 1, Name: "wrong", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: valueType.Usage()}},
			{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "wrong", Usage: valueType.Usage()}},
			{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 3, Name: "value", Usage: valueType.Usage()}},
			{{Attribute: 1, Name: "key", Usage: Usage{Type: types[Text], Typmod: 2}}, {Attribute: 2, Name: "value", Usage: valueType.Usage()}},
			{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: Usage{Type: valueType, Typmod: 2}}},
		} {
			shape, err := RecordShape(members)
			if err != nil {
				continue
			} // The shape constructor may reject malformed layouts first.
			if _, err := BindJSONEach(name, types[JSONB].Usage(), shape.Usage()); err == nil {
				t.Fatal("invalid record admitted", members)
			}
		}
		if name == "jsonb_each" {
			shape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "key", Usage: types[Text].Usage()}, {Attribute: 2, Name: "value", Usage: testTypes()[JSONB].Usage()}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := BindJSONEach(name, types[JSONB].Usage(), shape.Usage()); err == nil {
				t.Fatal("foreign JSONB value generation admitted")
			}
		}
	}
}
