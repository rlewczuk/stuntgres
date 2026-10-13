package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func arraySearchUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func arraySearchUnitPlan(t *testing.T, types map[ID]*Descriptor, name string, array, result *Descriptor) ArraySearchPlan {
	t.Helper()
	p, err := BindArraySearch(name, array, result, types[Int4], array.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func arraySearchUnitState(t *testing.T, err error, code, message string) {
	t.Helper()
	d, ok := err.(*sqlstate.Error)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !ok || *d != want {
		t.Fatalf("diagnostic=%#v want %#v", err, want)
	}
}

func TestArraySearchValues(t *testing.T) {
	types := testTypes()
	a := arraySearchUnitInput(t, types[1007], "[-2:2]={7,NULL,7,8,7}")
	seven, _ := Integer(types[Int4], 7)
	start, _ := Integer(types[Int4], -1)
	nine, _ := Integer(types[Int4], 9)
	for _, tc := range []struct {
		name   string
		result *Descriptor
		args   []Value
		want   string
	}{
		{"array_position", types[Int4], []Value{a, seven}, "-2"},
		{"array_position", types[Int4], []Value{a, seven, start}, "0"},
		{"array_position", types[Int4], []Value{a, Null(types[Int4])}, "-1"},
		{"array_positions", types[1007], []Value{a, seven}, "{-2,0,2}"},
		{"array_positions", types[1007], []Value{a, Null(types[Int4])}, "{-1}"},
		{"array_remove", types[1007], []Value{a, seven}, "[-2:-1]={NULL,8}"},
		{"array_replace", types[1007], []Value{a, Null(types[Int4]), nine}, "[-2:2]={7,9,7,8,7}"},
	} {
		p := arraySearchUnitPlan(t, types, tc.name, types[1007], tc.result)
		got, err := p.Evaluate(context.Background(), tc.args, InputEnvironment{})
		if err != nil {
			t.Fatal(tc.name, err)
		}
		text, err := got.Type().Output(context.Background(), got)
		if err != nil || text != tc.want || got.Type() != tc.result {
			t.Fatal(tc.name, text, err)
		}
	}
	for _, op := range []string{"@>", "<@", "&&"} {
		p := arraySearchUnitPlan(t, types, op, types[1007], types[Bool])
		for _, tc := range []struct {
			a, b string
			want bool
		}{
			{"{1}", "{1,1}", true}, {"{NULL}", "{NULL}", false},
		} {
			got, err := p.Evaluate(context.Background(), []Value{arraySearchUnitInput(t, types[1007], tc.a), arraySearchUnitInput(t, types[1007], tc.b)}, InputEnvironment{})
			if err != nil || got != Boolean(types[Bool], tc.want) {
				t.Fatal(op, got, err)
			}
		}
	}
	p := arraySearchUnitPlan(t, types, "array_replace", types[1007], types[1007])
	matrix := arraySearchUnitInput(t, types[1007], "[0:1][-1:0]={{7,NULL},{7,8}}")
	got, err := p.Evaluate(context.Background(), []Value{matrix, seven, Null(types[Int4])}, InputEnvironment{})
	if err != nil || !reflect.DeepEqual(got.ArrayDimensions(), matrix.ArrayDimensions()) || !got.ArrayElement(0).IsNull() || !got.ArrayElement(2).IsNull() {
		t.Fatal(got, err)
	}
	// Foreign descriptor generations never become valid just because OIDs agree.
	foreign := testTypes()
	if _, err := p.Evaluate(context.Background(), []Value{Null(foreign[1007]), seven, nine}, InputEnvironment{}); err == nil {
		t.Fatal("foreign generation admitted")
	}
}

func TestArraySearchDemand(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	one, _ := Integer(types[Int4], 1)
	position := arraySearchUnitPlan(t, types, "array_position", types[1007], types[Int4])
	for _, text := range []string{"{}", "{1}"} {
		search := one
		if text == "{1}" {
			search = Null(types[Int4])
		}
		got, err := position.Evaluate(ctx, []Value{arraySearchUnitInput(t, types[1007], text), search, Null(types[Int4])}, InputEnvironment{})
		if err != nil || !got.IsNull() {
			t.Fatal(text, got, err)
		}
	}
	_, err := position.Evaluate(ctx, []Value{arraySearchUnitInput(t, types[1007], "{NULL}"), Null(types[Int4]), Null(types[Int4])}, InputEnvironment{})
	arraySearchUnitState(t, err, "22004", "initial position must not be null")
	_, err = position.Evaluate(ctx, []Value{arraySearchUnitInput(t, types[1007], "{{1}}"), one, Null(types[Int4])}, InputEnvironment{})
	arraySearchUnitState(t, err, "0A000", "searching for elements in multidimensional arrays is not supported")
	for _, name := range []string{"array_position", "array_positions", "array_remove", "array_replace", "@>", "<@", "&&"} {
		result := types[199]
		if name == "array_position" {
			result = types[Int4]
		}
		if name == "array_positions" {
			result = types[1007]
		}
		if name == "@>" || name == "<@" || name == "&&" {
			result = types[Bool]
		}
		p := arraySearchUnitPlan(t, types, name, types[199], result)
		for _, text := range []string{"{}", "{NULL}"} {
			a := arraySearchUnitInput(t, types[199], text)
			args := []Value{a, Null(types[JSON])}
			if p.Membership() {
				args[1] = a
			}
			if name == "array_replace" {
				args = append(args, Null(types[JSON]))
			}
			got, err := p.Evaluate(ctx, args, InputEnvironment{})
			if p.Membership() || text != "{}" {
				arraySearchUnitState(t, err, "42883", "could not identify an equality operator for type json")
			} else if err != nil || got.Type() != result {
				t.Fatal(name, got, err)
			}
			args[0] = Null(types[199])
			got, err = p.Evaluate(ctx, args, InputEnvironment{})
			if err != nil || !got.IsNull() {
				t.Fatal(name, got, err)
			}
		}
	}
	// Input collation is demanded only by actual non-NULL string equality.
	p, err := BindArraySearch("array_remove", types[1009], types[1009], types[Int4], types[Text].Usage().Collation, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate(ctx, []Value{arraySearchUnitInput(t, types[1009], "{NULL}"), Null(types[Text])}, InputEnvironment{})
	if err != nil || got.ArrayRank() != 0 {
		t.Fatal(got, err)
	}
	_, err = p.Evaluate(ctx, []Value{arraySearchUnitInput(t, types[1009], "{a}"), String(types[Text], "a")}, InputEnvironment{})
	d, ok := err.(interface{ SQLState() string })
	if !ok || d.SQLState() != "42P22" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id            ID
		input, search string
	}{
		{1014, `{"a  "}`, "a"}, {1231, "{NaN}", "NaN"}, {1022, "{NaN}", "NaN"}, {3807, `{"{\"a\":1,\"b\":2}"}`, `{"b":2,"a":1}`},
	} {
		array := types[tc.id]
		p := arraySearchUnitPlan(t, types, "array_position", array, types[Int4])
		search := arraySearchUnitInput(t, array.Element(), tc.search)
		got, err := p.Evaluate(ctx, []Value{arraySearchUnitInput(t, array, tc.input), search}, InputEnvironment{})
		if err != nil {
			t.Fatal(tc.id, err)
		}
		value, err := got.Integer()
		if err != nil || value != 1 {
			t.Fatal(tc.id, got, err)
		}
	}
}

func TestArraySearchImmutability(t *testing.T) {
	types := testTypes()
	a := arraySearchUnitInput(t, types[1007], "[-2:0]={1,2,1}")
	one, _ := Integer(types[Int4], 1)
	nine, _ := Integer(types[Int4], 9)
	for _, name := range []string{"array_remove", "array_replace"} {
		p := arraySearchUnitPlan(t, types, name, types[1007], types[1007])
		args := []Value{a, nine}
		if name == "array_replace" {
			args = append(args, one)
		}
		got, err := p.Evaluate(context.Background(), args, InputEnvironment{})
		if err != nil || got.array != a.array {
			t.Fatal("no-change copied payload", name, err)
		}
		args[1] = one
		if name == "array_replace" {
			args[2] = nine
		}
		got, err = p.Evaluate(context.Background(), args, InputEnvironment{})
		if err != nil || got.array == a.array {
			t.Fatal("changed result aliased source", name, err)
		}
		text, err := a.Type().Output(context.Background(), a)
		if err != nil || text != "[-2:0]={1,2,1}" {
			t.Fatal("mutated source", text, err)
		}
		// Exported dimensions are detached, including copy-on-change results.
		dimensions := got.ArrayDimensions()
		dimensions[0].Lower = 100
		if got.ArrayDimension(0).Lower != -2 || a.ArrayDimension(0).Lower != -2 {
			t.Fatal("mutable dimensions")
		}
	}
	p := arraySearchUnitPlan(t, types, "array_remove", types[1007], types[1007])
	all := arraySearchUnitInput(t, types[1007], "[9:10]={1,1}")
	empty, err := p.Evaluate(context.Background(), []Value{all, one}, InputEnvironment{})
	if err != nil || empty.IsNull() || empty.ArrayRank() != 0 || empty.ArrayLen() != 0 {
		t.Fatal(empty, err)
	}
	if empty.array.elements != nil || cap(empty.array.elements) != 0 {
		t.Fatalf("all-removed result retained survivor storage: len=%d cap=%d", len(empty.array.elements), cap(empty.array.elements))
	}
}

type arraySearchCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *arraySearchCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestArraySearchCancellation(t *testing.T) {
	types := testTypes()
	a := arraySearchUnitInput(t, types[1007], "{1,2,3,4,5,6,7,8}")
	nine, _ := Integer(types[Int4], 9)
	for _, name := range []string{"array_position", "array_positions", "array_remove", "array_replace", "@>", "<@", "&&"} {
		result := types[1007]
		if name == "array_position" {
			result = types[Int4]
		}
		if name == "@>" || name == "<@" || name == "&&" {
			result = types[Bool]
		}
		p := arraySearchUnitPlan(t, types, name, types[1007], result)
		args := []Value{a, nine}
		if name == "array_replace" {
			args = append(args, nine)
		}
		if p.Membership() {
			args[1] = arraySearchUnitInput(t, types[1007], "{9}")
		}
		if name == "<@" {
			args[0], args[1] = args[1], args[0]
		}
		ctx, cancel := context.WithCancel(context.Background())
		c := &arraySearchCancelContext{Context: ctx, cancel: cancel, remaining: 5}
		_, err := p.Evaluate(c, args, InputEnvironment{})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(name, err)
		}
	}
}
