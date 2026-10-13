package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func jsonFunctionUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}

func TestJSONTypeScalarKindsLengthsAndStrictness(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{JSON, JSONB} {
		family := "json"
		if id == JSONB {
			family = "jsonb"
		}
		kind, err := BindBinary(family+"_typeof", types[Text].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		length, err := BindBinary(family+"_array_length", types[Int4].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ input, want string }{{`{}`, "object"}, {`[]`, "array"}, {`"null"`, "string"}, {`-1.2e3`, "number"}, {`true`, "boolean"}, {`false`, "boolean"}, {`null`, "null"}} {
			v := jsonFunctionUnitInput(t, types[id], tc.input)
			got, err := kind.Evaluate(ctx, v)
			if err != nil || got.typ != types[Text] || got.null || got.text != tc.want {
				t.Fatal(tc.input, got, err)
			}
		}
		for _, tc := range []struct {
			input string
			want  int64
		}{{`[]`, 0}, {`[1,2,3,{"f1":1,"f2":[5,6]},4]`, 5}, {`[null,[],{},"a,b",[1,2]]`, 5}} {
			got, err := length.Evaluate(ctx, jsonFunctionUnitInput(t, types[id], tc.input))
			if err != nil || got.typ != types[Int4] || got.integer != tc.want || got.null {
				t.Fatal(tc.input, got, err)
			}
		}
		for _, plan := range []BinaryPlan{kind, length} {
			got, err := plan.Evaluate(ctx, Null(types[id]))
			if err != nil || got.typ != plan.result.Type || !got.null || !plan.Immutable() {
				t.Fatal(got, err)
			}
		}
		for _, input := range []string{`{}`, `0`, `""`, `true`, `false`, `null`} {
			message := "cannot get array length of a scalar"
			if input == `{}` {
				message = "cannot get array length of a non-array"
			}
			got, err := length.Evaluate(ctx, jsonFunctionUnitInput(t, types[id], input))
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message}
			var diagnostic *sqlstate.Error
			if got != (Value{}) || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(input, got, err)
			}
		}
	}
	kind, _ := BindBinary("json_typeof", types[Text].Usage(), types[JSON].Usage())
	got, err := kind.Evaluate(ctx, jsonFunctionUnitInput(t, types[JSON], `1e131072`))
	if err != nil || got.text != "number" {
		t.Fatal(got, err)
	}
	length, _ := BindBinary("json_array_length", types[Int4].Usage(), types[JSON].Usage())
	got, err = length.Evaluate(ctx, jsonFunctionUnitInput(t, types[JSON], `["\u0000",1e131072]`))
	if err != nil || got.integer != 2 {
		t.Fatal(got, err)
	}
}

func TestJSONKeysOrderDecodeAndCursorOwnership(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{JSON, JSONB} {
		name := "json_object_keys"
		want := []string{"zz", "b", "a", "aa", "b", "é", "aaa"}
		if id == JSONB {
			name, want = "jsonb_object_keys", []string{"a", "b", "aa", "zz", "é", "aaa"}
		}
		plan, err := BindJSONKeys(name, types[id].Usage(), types[Text].Usage())
		if err != nil {
			t.Fatal(err)
		}
		v := jsonFunctionUnitInput(t, types[id], `{"zz":0,"b":1,"a":2,"aa":3,"b":4,"é":5,"aaa":6}`)
		cursor, err := plan.Open(context.Background(), v)
		if err != nil {
			t.Fatal(err)
		}
		if id == JSONB && (cursor.payload != v.jsonb || cursor.keys != nil) {
			t.Fatal("copied canonical payload")
		}
		var got []string
		for {
			value, present, err := cursor.Next(context.Background())
			if err != nil || value.typ != types[Text] {
				t.Fatal(value, err)
			}
			if !present {
				if !value.null {
					t.Fatal("untyped/non-null EOF", value)
				}
				break
			}
			got = append(got, value.text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
		if _, present, err := cursor.Next(context.Background()); err != nil || present {
			t.Fatal(present, err)
		}
		cursor.Close()
		if cursor.result != nil || cursor.keys != nil || cursor.payload != nil {
			t.Fatal("Close retained document")
		}
		for _, empty := range []Value{Null(types[id]), jsonFunctionUnitInput(t, types[id], `{}`)} {
			cursor, err := plan.Open(context.Background(), empty)
			if err != nil {
				t.Fatal(err)
			}
			value, present, err := cursor.Next(context.Background())
			if err != nil || present || !value.null || value.typ != types[Text] {
				t.Fatal(value, present, err)
			}
			cursor.Close()
		}
		for _, input := range []string{`[]`, `0`, `""`, `true`, `false`, `null`} {
			cursor, err := plan.Open(context.Background(), jsonFunctionUnitInput(t, types[id], input))
			message := "cannot call " + name + " on a scalar"
			if input == `[]` {
				message = "cannot call " + name + " on an array"
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message}
			var diagnostic *sqlstate.Error
			if cursor.result != nil || cursor.payload != nil || cursor.keys != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(input, cursor, err)
			}
		}
		input := `{"a":1,"\u0061":2,"":3,"\"\\\n\t\uD83D\uDE00":4,"outer":{"hidden":5}}`
		want = []string{"a", "a", "", "\"\\\n\t😀", "outer"}
		if id == JSONB {
			want = []string{"", "a", "outer", "\"\\\n\t😀"}
		}
		cursor, err = plan.Open(context.Background(), jsonFunctionUnitInput(t, types[id], input))
		if err != nil {
			t.Fatal(err)
		}
		got = nil
		for {
			value, present, err := cursor.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			got = append(got, value.text)
		}
		cursor.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
	}
}

func TestJSONKeysEagerValidationWithoutNumericConversion(t *testing.T) {
	types := testTypes()
	plan, _ := BindJSONKeys("json_object_keys", types[JSON].Usage(), types[Text].Usage())
	cursor, err := plan.Open(context.Background(), jsonFunctionUnitInput(t, types[JSON], `{"x":1e131072}`))
	if err != nil {
		t.Fatal(err)
	}
	value, present, err := cursor.Next(context.Background())
	if err != nil || !present || value.text != "x" {
		t.Fatal(value, present, err)
	}
	cursor.Close()
	v := jsonFunctionUnitInput(t, types[JSON], `{"ok":1,"nested":{"x":"\u0000"}}`)
	cursor, err = plan.Open(context.Background(), v)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P05", Message: "unsupported Unicode escape sequence", Detail: `\u0000 cannot be converted to text.`, Context: `JSON data, line 1: {"ok":1,"nested":{"x":"\u0000...`}
	if !errors.As(err, &diagnostic) || *diagnostic != want || cursor.result != nil || cursor.keys != nil {
		t.Fatal("published eager error", cursor, err)
	}
}

func TestJSONTypeAndKeysDescriptorContracts(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, name := range []string{"json_typeof", "jsonb_typeof", "json_array_length", "jsonb_array_length"} {
		id, result := JSON, Text
		if strings.HasPrefix(name, "jsonb_") {
			id = JSONB
		}
		if strings.HasSuffix(name, "_length") {
			result = Int4
		}
		for _, args := range [][]Usage{nil, {types[Text].Usage()}, {types[id].Usage(), types[id].Usage()}} {
			if _, err := BindBinary(name, types[result].Usage(), args...); err == nil {
				t.Fatal("accepted signature", name, args)
			}
		}
		p, err := BindBinary(name, types[result].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(context.Background(), Null(fresh[id])); err == nil {
			t.Fatal("foreign descriptor value accepted")
		}
	}
	for _, tc := range []struct {
		name          string
		input, result Usage
	}{
		{"json_each", types[JSON].Usage(), types[Text].Usage()},
		{"json_object_keys", types[JSONB].Usage(), types[Text].Usage()},
		{"jsonb_object_keys", types[JSON].Usage(), types[Text].Usage()},
		{"json_object_keys", types[JSON].Usage(), types[Int4].Usage()},
		{"json_object_keys", Usage{}, types[Text].Usage()},
	} {
		if _, err := BindJSONKeys(tc.name, tc.input, tc.result); err == nil {
			t.Fatal("invalid keys contract accepted", tc)
		}
	}
	p, _ := BindJSONKeys("json_object_keys", types[JSON].Usage(), types[Text].Usage())
	if _, err := p.Open(context.Background(), Null(fresh[JSON])); err == nil {
		t.Fatal("foreign keys descriptor accepted")
	}
	if p.ResultUsage() != types[Text].Usage() || p.Name() != "json_object_keys" {
		t.Fatal(p)
	}
}

func TestJSONTypeAndKeysBoundedCancellation(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		name, input string
		keys        bool
	}{
		{"kind-long-token", `"` + strings.Repeat("x", 32768) + `"`, false},
		{"length-long-token", `["` + strings.Repeat("x", 32768) + `"]`, false},
		{"keys-long-key", `{"` + strings.Repeat("x", 32768) + `":1}`, true},
		{"keys-nested-string", `{"ok":1,"nested":["` + strings.Repeat(`\n`, 8192) + `"]}`, true},
		{"length-deep-stack", strings.Repeat("[", 4096) + `0` + strings.Repeat("]", 4096), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := jsonFunctionUnitInput(t, types[JSON], tc.input)
			keys, _ := BindJSONKeys("json_object_keys", types[JSON].Usage(), types[Text].Usage())
			name, result := "json_array_length", types[Int4]
			if strings.HasPrefix(tc.name, "kind") {
				name, result = "json_typeof", types[Text]
			}
			scalar, _ := BindBinary(name, result.Usage(), types[JSON].Usage())
			run := func(c context.Context) error {
				if tc.keys {
					cursor, err := keys.Open(c, v)
					if err != nil && (cursor.result != nil || cursor.keys != nil || cursor.payload != nil) {
						t.Fatal("cancellation published cursor")
					}
					cursor.Close()
					return err
				}
				got, err := scalar.Evaluate(c, v)
				if err != nil && got != (Value{}) {
					t.Fatal("cancellation published value")
				}
				return err
			}
			count := &groupingCancelContext{Context: ctx, at: int(^uint(0) >> 1)}
			if err := run(count); err != nil {
				t.Fatal(err)
			}
			if count.calls < 4 {
				t.Fatal("unbounded workload", count.calls)
			}
			for _, at := range []int{max(2, count.calls/2), count.calls} {
				cancel := &groupingCancelContext{Context: ctx, at: at}
				if err := run(cancel); !errors.Is(err, context.Canceled) || cancel.calls != at {
					t.Fatal("mid-work/publication cancellation", err, cancel.calls, at)
				}
			}
		})
	}
	for _, name := range []string{"jsonb_typeof", "jsonb_array_length"} {
		result := types[Text]
		if name == "jsonb_array_length" {
			result = types[Int4]
		}
		plan, _ := BindBinary(name, result.Usage(), types[JSONB].Usage())
		v := jsonFunctionUnitInput(t, types[JSONB], `[1]`)
		count := &groupingCancelContext{Context: ctx, at: 2}
		if got, err := plan.Evaluate(count, v); !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal("JSONB publication", got, err)
		}
	}
	keys, _ := BindJSONKeys("jsonb_object_keys", types[JSONB].Usage(), types[Text].Usage())
	v := jsonFunctionUnitInput(t, types[JSONB], `{"a":1}`)
	count := &groupingCancelContext{Context: ctx, at: 2}
	if cursor, err := keys.Open(count, v); !errors.Is(err, context.Canceled) || cursor.payload != nil || cursor.result != nil {
		t.Fatal("JSONB cursor publication", cursor, err)
	}
	cursor, err := keys.Open(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if value, present, err := cursor.Next(canceled); !errors.Is(err, context.Canceled) || present || value != (Value{}) || cursor.index != 0 {
		t.Fatal("canceled key advanced", value, present, err)
	}
}

func TestJSONTypeJSONBDirectRoot(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		input, kind string
		length      int64
	}{
		{`[]`, "array", 0},
		{`[{"nested":[1,2,3]}]`, "array", 1},
		{`null`, "null", -1},
		{`false`, "boolean", -1},
		{`1`, "number", -1},
		{`"s"`, "string", -1},
		{`{"zz":1,"a":2}`, "object", -1},
	} {
		value := jsonFunctionUnitInput(t, types[JSONB], tc.input)
		payload := value.jsonb
		// Cached display is not the operation's authority: providers must retain
		// and inspect the authenticated immutable root, not serialize or parse it.
		value.text = ""
		kind, _ := BindBinary("jsonb_typeof", types[Text].Usage(), types[JSONB].Usage())
		got, err := kind.Evaluate(context.Background(), value)
		if err != nil || got.text != tc.kind || value.jsonb != payload {
			t.Fatal(got, err)
		}
		if tc.length >= 0 {
			length, _ := BindBinary("jsonb_array_length", types[Int4].Usage(), types[JSONB].Usage())
			got, err := length.Evaluate(context.Background(), value)
			if err != nil || got.integer != tc.length || value.jsonb != payload {
				t.Fatal(got, err)
			}
		}
		if tc.kind == "object" {
			keys, _ := BindJSONKeys("jsonb_object_keys", types[JSONB].Usage(), types[Text].Usage())
			cursor, err := keys.Open(context.Background(), value)
			if err != nil || cursor.payload != payload || cursor.keys != nil {
				t.Fatal(cursor, err)
			}
			first, present, err := cursor.Next(context.Background())
			if err != nil || !present || first.text != "a" {
				t.Fatal(first, present, err)
			}
			cursor.Close()
		}
	}
}
