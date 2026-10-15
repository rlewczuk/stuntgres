package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonElementsUnitPlan(t *testing.T, types map[ID]*Descriptor, name string) JSONElementsPlan {
	t.Helper()
	result := types[JSON]
	if name == "json_array_elements_text" {
		result = types[Text]
	}
	plan, err := BindJSONElements(name, types[JSON].Usage(), result.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestJSONElementsScalarSpansAndPrivateCursors(t *testing.T) {
	types := testTypes()
	const input = ` [ 1.00e+02, "x\n", null, "null", { "a":1,"a":2 }, [2,  false], 1e999999 ] `
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		p := jsonElementsUnitPlan(t, types, name)
		v := jsonFunctionUnitInput(t, types[JSON], input)
		cursor, err := p.Open(context.Background(), v)
		if err != nil {
			t.Fatal(err)
		}
		other, err := p.Open(context.Background(), v)
		if err != nil {
			t.Fatal(err)
		}
		want := []any{"1.00e+02", `"x\n"`, "null", `"null"`, `{ "a":1,"a":2 }`, `[2,  false]`, "1e999999"}
		if name == "json_array_elements_text" {
			want[1], want[2], want[3] = "x\n", nil, "null"
		}
		var got []any
		var retained Value
		for {
			value, present, err := cursor.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if value.Type() != p.ResultUsage().Type {
				t.Fatal("foreign result type", value)
			}
			if !present {
				if !value.IsNull() {
					t.Fatal("nonnull exhaustion", value)
				}
				break
			}
			if len(got) == 0 {
				retained = value
			}
			var text any
			if !value.IsNull() {
				if name == "json_array_elements" {
					text, err = value.JSONText()
				} else {
					text, err = value.Text()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			got = append(got, text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got=%#v want=%#v", name, got, want)
		}
		cursor.Close()
		if cursor.text != "" || cursor.members != nil || cursor.result != nil {
			t.Fatal("Close retains invocation")
		}
		if _, _, err := cursor.Next(context.Background()); err == nil {
			t.Fatal("closed cursor accepted")
		}
		value, present, err := other.Next(context.Background())
		other.Close()
		if err != nil || !present || value != retained {
			t.Fatal("shared cursor position or mutated retained scalar", value, err)
		}
		if stored, _ := v.JSONText(); stored != input {
			t.Fatal("input mutated")
		}
		for _, empty := range []Value{Null(types[JSON]), jsonFunctionUnitInput(t, types[JSON], `[]`)} {
			cursor, err := p.Open(context.Background(), empty)
			if err != nil {
				t.Fatal(err)
			}
			value, present, err := cursor.Next(context.Background())
			cursor.Close()
			if err != nil || present || !value.IsNull() || value.Type() != p.result.Type {
				t.Fatal("strict empty result", value, present, err)
			}
		}
	}
}

func TestJSONElementsEagerUnicodeAndRootErrors(t *testing.T) {
	types := testTypes()
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		p := jsonElementsUnitPlan(t, types, name)
		for _, tc := range []struct{ input, kind string }{{`{}`, "non-array"}, {`null`, "scalar"}, {`1`, "scalar"}, {`true`, "scalar"}, {`"x"`, "scalar"}} {
			cursor, err := p.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], tc.input))
			var d *sqlstate.Error
			if cursor.result != nil || !errors.As(err, &d) || d.Code != "22023" || d.Message != "cannot call "+name+" on a "+tc.kind || d.Position != "" {
				t.Fatal("root diagnostic", cursor, err)
			}
		}
		for _, input := range []string{`["\u0000"]`, `["\ud800"]`, `["ok",{"late":"\u0000"}]`, `[{"\u0000":1}]`, `[["\udc00"]]`} {
			cursor, err := p.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], input))
			if name == "json_array_elements" {
				if err != nil {
					t.Fatal("raw mode decoded Unicode", err)
				}
				cursor.Close()
			} else {
				var d *sqlstate.Error
				if cursor.result != nil || !errors.As(err, &d) || (d.Code != "22P05" && d.Code != "22P02") {
					t.Fatal("partial text invocation", cursor, err)
				}
			}
		}
	}
}

func TestJSONElementsBindingsAndCancellation(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		p := jsonElementsUnitPlan(t, types, name)
		for _, tc := range []struct {
			name          string
			input, result Usage
		}{
			{"jsonb_array_elements", types[JSON].Usage(), p.result},
			{name, types[Text].Usage(), p.result},
			{name, Usage{}, p.result},
			{name, types[JSON].Usage(), types[Int4].Usage()},
			{name, types[JSON].Usage(), Usage{}},
		} {
			if _, err := BindJSONElements(tc.name, tc.input, tc.result); err == nil {
				t.Fatal("invalid binding accepted", tc)
			}
		}
		if name == "json_array_elements" {
			if _, err := BindJSONElements(name, types[JSON].Usage(), fresh[JSON].Usage()); err == nil {
				t.Fatal("mixed result generation accepted")
			}
		}
		if _, err := p.Open(context.Background(), Null(fresh[JSON])); err == nil {
			t.Fatal("foreign input generation accepted")
		}
		v := jsonFunctionUnitInput(t, types[JSON], `[{"nested":"`+strings.Repeat(`\u0041`, 10000)+`"}]`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, ctx := range []context.Context{ctx, &groupingCancelContext{Context: context.Background(), at: 5}} {
			cursor, err := p.Open(ctx, v)
			if cursor.result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("partial canceled scan", cursor, err)
			}
		}
		cursor, err := p.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], `[1]`))
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := cursor.Next(ctx); present || !errors.Is(err, context.Canceled) {
			t.Fatal("iteration cancellation", present, err)
		}
		cursor.Close()
	}
}
