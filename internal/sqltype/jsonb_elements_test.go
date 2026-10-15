package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestJSONBElementsCursorValuesAndOwnership(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, name := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		result := types[JSONB]
		if name == "jsonb_array_elements_text" {
			result = types[Text]
		}
		plan, err := BindJSONBElements(name, types[JSONB].Usage(), result.Usage())
		if err != nil {
			t.Fatal(err)
		}
		input := jsonFunctionUnitInput(t, types[JSONB], `[1,1.00,1.230e-5,true,false,"a\nb","",null,[2,{"z":1,"a":2}],{"zz":0,"a":1,"a":2}]`)
		cursor, err := plan.Open(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		other, err := plan.Open(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		want := []any{"1", "1.00", "0.00001230", "true", "false", `"a\nb"`, `""`, "null", `[2, {"a": 2, "z": 1}]`, `{"a": 2, "zz": 0}`}
		if result.ID() == Text {
			want[5], want[6], want[7] = "a\nb", "", nil
		}
		var retained Value
		for i, w := range want {
			v, ok, err := cursor.Next(ctx)
			if err != nil || !ok || v.Type() != result {
				t.Fatal(v, ok, err)
			}
			var got any
			if !v.IsNull() {
				got, err = result.Output(ctx, v)
			}
			if err != nil || !reflect.DeepEqual(got, w) {
				t.Fatalf("row%d=%#v want=%#v error=%v", i, got, w, err)
			}
			if i == 8 {
				retained = v
			}
		}
		if v, ok, err := cursor.Next(ctx); err != nil || ok || !v.IsNull() || v.Type() != result {
			t.Fatal("EOF", v, ok, err)
		}
		cursor.Close()
		if _, _, err := cursor.Next(ctx); err == nil {
			t.Fatal("closed cursor accepted")
		}
		if _, ok, err := other.Next(ctx); err != nil || !ok {
			t.Fatal("shared invocation state", err)
		}
		if result.ID() == JSONB {
			out, err := result.Output(ctx, retained)
			if err != nil || out != `[2, {"a": 2, "z": 1}]` {
				t.Fatal("retained output", out, err)
			}
		}
		for _, v := range []Value{Null(types[JSONB]), jsonFunctionUnitInput(t, types[JSONB], `[]`)} {
			c, err := plan.Open(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			v, ok, err := c.Next(ctx)
			c.Close()
			if err != nil || ok || !v.IsNull() {
				t.Fatal(v, ok, err)
			}
		}
	}
}

func TestJSONBElementsErrorsCancellationAndBinding(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, name := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		result := types[JSONB]
		if name == "jsonb_array_elements_text" {
			result = types[Text]
		}
		plan, err := BindJSONBElements(name, types[JSONB].Usage(), result.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{`{}`, `{"x":1}`, `null`, `0`, `""`, `true`, `false`} {
			c, err := plan.Open(ctx, jsonFunctionUnitInput(t, types[JSONB], text))
			var d *sqlstate.Error
			message := "cannot extract elements from a scalar"
			if strings.HasPrefix(text, "{") {
				message = "cannot extract elements from an object"
			}
			if c.result != nil || !errors.As(err, &d) || d.Code != "22023" || d.Message != message || d.Position != "" {
				t.Fatal(c, err)
			}
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		v := jsonFunctionUnitInput(t, types[JSONB], `[1]`)
		if _, err := plan.Open(canceled, v); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		c, err := plan.Open(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.Next(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		c.Close()
		large := jsonFunctionUnitInput(t, types[JSONB], `[{"x":"`+strings.Repeat("x", 100000)+`"}]`)
		c, err = plan.Open(ctx, large)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := c.Next(&groupingCancelContext{Context: ctx, at: 5}); ok || !errors.Is(err, context.Canceled) {
			t.Fatal("partial render", ok, err)
		}
		c.Close()
		if _, err := plan.Open(ctx, Null(types[JSON])); err == nil {
			t.Fatal("foreign input accepted")
		}
	}
	for _, tc := range []struct {
		name          string
		input, result Usage
	}{
		{"other", types[JSONB].Usage(), types[JSONB].Usage()},
		{"jsonb_array_elements", Usage{}, types[JSONB].Usage()},
		{"jsonb_array_elements", types[JSON].Usage(), types[JSONB].Usage()},
		{"jsonb_array_elements", types[JSONB].Usage(), types[Text].Usage()},
		{"jsonb_array_elements_text", types[JSONB].Usage(), types[JSONB].Usage()},
	} {
		if _, err := BindJSONBElements(tc.name, tc.input, tc.result); err == nil {
			t.Fatal("invalid binding accepted", tc)
		}
	}
}
