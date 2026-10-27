package enginetests

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func hstoreConstructBuiltins() (text, array, boolean *sqltype.Descriptor) {
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case 25:
			text = d
		case 1009:
			array = d
		case 16:
			boolean = d
		}
	}
	return
}
func TestHstoreConstructTyped(t *testing.T) {
	ctx := context.Background()
	h := hstoreUnitType(t, 65000)
	text, array, boolean := hstoreConstructBuiltins()
	input := func(s string) sqltype.Value {
		t.Helper()
		v, err := array.Input(ctx, s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	output := func(v sqltype.Value, err error, want string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.Output(ctx, v)
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	for _, tc := range []struct {
		k, v string
		null bool
		want string
	}{
		{"a", "1", false, `"a"=>"1"`}, {"a", "", true, `"a"=>NULL`}, {"a", "NULL", false, `"a"=>"NULL"`}, {"", "", false, `""=>""`}, {`a"b\c`, `x"y\z`, false, `"a\"b\\c"=>"x\"y\\z"`}, {"a,b=> c", "雪 β", false, `"a,b=> c"=>"雪 β"`},
	} {
		value := sqltype.String(text, tc.v)
		if tc.null {
			value = sqltype.Null(text)
		}
		v, err := h.HstoreFromText(ctx, sqltype.String(text, tc.k), value)
		output(v, err, tc.want)
		fetched, err := v.HstoreFetch(ctx, h, sqltype.String(text, tc.k))
		if err != nil || fetched.IsNull() != tc.null {
			t.Fatal("fetch", err)
		}
		if !tc.null {
			got, _ := fetched.Text()
			if got != tc.v {
				t.Fatal("literal key/value changed", got)
			}
		}
		for _, defined := range []bool{false, true} {
			got, err := v.HstoreExists(ctx, h, boolean, sqltype.String(text, tc.k), defined)
			b, _ := got.Boolean()
			if err != nil || b != (!defined || !tc.null) {
				t.Fatal("presence versus NULL", err)
			}
		}
		missing, err := v.HstoreFetch(ctx, h, sqltype.String(text, "absent"))
		if err != nil || !missing.IsNull() {
			t.Fatal("missing lookup", err)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"{}", ""}, {"{a,1,b,NULL}", `"a"=>"1", "b"=>NULL`}, {"{{a,1},{b,2}}", `"a"=>"1", "b"=>"2"`}, {"[0:3]={a,1,b,2}", `"a"=>"1", "b"=>"2"`}, {"[0:1][-1:0]={{a,1},{b,2}}", `"a"=>"1", "b"=>"2"`},
	} {
		v, err := h.HstoreFromArray(ctx, input(tc.input))
		output(v, err, tc.want)
	}
	for _, tc := range []struct {
		k, v, want string
		null       bool
	}{
		{"{}", "{}", "", false}, {"{a,b}", "{1,2}", `"a"=>"1", "b"=>"2"`, false}, {"[0:1]={a,b}", "[0:1]={1,2}", `"a"=>"1", "b"=>"2"`, false}, {"{a,b}", "", `"a"=>NULL, "b"=>NULL`, true},
	} {
		values := sqltype.Null(array)
		if !tc.null {
			values = input(tc.v)
		}
		v, err := h.HstoreFromArrays(ctx, input(tc.k), values)
		output(v, err, tc.want)
	}
	for _, construct := range []func() (sqltype.Value, error){func() (sqltype.Value, error) {
		return h.HstoreFromText(ctx, sqltype.Null(text), sqltype.String(text, "1"))
	}, func() (sqltype.Value, error) { return h.HstoreFromArray(ctx, sqltype.Null(array)) }, func() (sqltype.Value, error) { return h.HstoreFromArrays(ctx, sqltype.Null(array), input("{{1}}")) }} {
		v, err := construct()
		if err != nil || !v.IsNull() || v.Type() != h {
			t.Fatal("whole NULL", err)
		}
	}
	duplicate, err := h.HstoreFromArray(ctx, input("{a,1,a,2}"))
	if err != nil {
		t.Fatal(err)
	}
	export, err := duplicate.HstoreArray(ctx, h, array, false)
	if err != nil || export.ArrayLen() != 2 {
		t.Fatal("duplicate uniqueness", err)
	}
	got, _ := export.ArrayElement(1).Text()
	if got != "1" && got != "2" {
		t.Fatal("invented duplicate value", got)
	}
}

func TestHstoreConstructShapes(t *testing.T) {
	ctx := context.Background()
	h := hstoreUnitType(t, 65000)
	_, array, _ := hstoreConstructBuiltins()
	input := func(s string) sqltype.Value {
		t.Helper()
		v, err := array.Input(ctx, s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		k, v, code, message string
		paired              bool
	}{
		{"{a}", "", "2202E", "array must have even number of elements", false},
		{"{{a,1,x}}", "", "2202E", "array must have two columns", false},
		{"{{{a,1}}}", "", "2202E", "wrong number of array subscripts", false},
		{"{{a}}", "{1}", "2202E", "wrong number of array subscripts", true},
		{"{a}", "{{1}}", "2202E", "wrong number of array subscripts", true},
		{"{a,b}", "{1}", "2202E", "arrays must have same bounds", true},
		{"[0:0]={a}", "[1:1]={1}", "2202E", "arrays must have same bounds", true},
		{"{}", "{1}", "2202E", "arrays must have same bounds", true},
		{"{NULL,1}", "", "22004", "null value not allowed for hstore key", false},
		{"{NULL}", "{1}", "22004", "null value not allowed for hstore key", true},
		{"{NULL}", "", "2202E", "array must have even number of elements", false},
		{"{NULL,b}", "{1}", "2202E", "arrays must have same bounds", true},
	} {
		var v sqltype.Value
		var err error
		if tc.paired {
			v, err = h.HstoreFromArrays(ctx, input(tc.k), input(tc.v))
		} else {
			v, err = h.HstoreFromArray(ctx, input(tc.k))
		}
		var d *sqlstate.Error
		if v.Type() != nil || !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message {
			t.Fatal(tc, v, err)
		}
	}
}

func TestHstoreConstructLookupOwnership(t *testing.T) {
	ctx := context.Background()
	h := hstoreUnitType(t, 65000)
	text, array, boolean := hstoreConstructBuiltins()
	v, err := h.Input(ctx, `a=>1,b=>NULL,c=>"NULL"`, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ keys, want string }{
		{"{c,a,missing,b,NULL,a}", `{"NULL",1,NULL,NULL,NULL,1}`}, {"{{b,a},{a,c}}", `{{NULL,1},{1,"NULL"}}`}, {"[0:1]={a,missing}", "[0:1]={1,NULL}"}, {"{{{a,NULL}}}", "{{{1,NULL}}}"}, {"{}", "{}"},
	} {
		keys, err := array.Input(ctx, tc.keys, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.HstoreFetchArray(ctx, h, array, keys)
		if err != nil {
			t.Fatal(err)
		}
		out, err := array.Output(ctx, got)
		if err != nil || out != tc.want {
			t.Fatal(out, tc.want, err)
		}
		if got.ArrayRank() != keys.ArrayRank() {
			t.Fatal("rank")
		}
		for i := range keys.ArrayRank() {
			if got.ArrayDimension(i) != keys.ArrayDimension(i) {
				t.Fatal("bounds")
			}
		}
		if got.ArrayRank() > 0 {
			dims := got.ArrayDimensions()
			dims[0].Lower = 99
			if got.ArrayDimension(0).Lower == 99 {
				t.Fatal("dimensions borrowed")
			}
		}
	}
	// External array construction freezes buffers before a typed constructor consumes them.
	dimensions := []sqltype.ArrayDimension{{Lower: 0, Length: 2}}
	elements := []sqltype.Value{sqltype.String(text, "a"), sqltype.String(text, "1")}
	original, err := sqltype.ArrayValue(array, dimensions, elements)
	if err != nil {
		t.Fatal(err)
	}
	built, err := h.HstoreFromArray(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	elements[0] = sqltype.String(text, "mutated")
	dimensions[0].Lower = 99
	out, err := h.Output(ctx, built)
	if err != nil || out != `"a"=>"1"` || original.ArrayDimension(0).Lower != 0 {
		t.Fatal("borrowed input", err)
	}
	foreign := hstoreUnitType(t, h.ID())
	key := sqltype.String(text, "a")
	for _, source := range []sqltype.Value{v, sqltype.Null(h)} {
		for _, f := range []func() (sqltype.Value, error){func() (sqltype.Value, error) { return source.HstoreFetch(ctx, foreign, key) }, func() (sqltype.Value, error) { return source.HstoreFetchArray(ctx, foreign, array, original) }, func() (sqltype.Value, error) { return source.HstoreExists(ctx, foreign, boolean, key, false) }} {
			if got, err := f(); err == nil || got.Type() != nil {
				t.Fatal("foreign source generation accepted")
			}
		}
	}
	foreignArray := *array
	if _, err := v.HstoreFetchArray(ctx, h, &foreignArray, original); err == nil {
		t.Fatal("foreign result generation")
	}
	if _, err := v.HstoreExists(ctx, h, text, key, false); err == nil {
		t.Fatal("wrong predicate result")
	}
	if _, err := text.HstoreFromText(ctx, key, key); err == nil {
		t.Fatal("wrong constructor result")
	}
	if _, err := h.HstoreFromArray(ctx, key); err == nil {
		t.Fatal("non-array input")
	}
	if _, err := v.HstoreFetch(ctx, h, sqltype.Null(array)); err == nil {
		t.Fatal("wrong NULL key")
	}
	for _, f := range []func() (sqltype.Value, error){func() (sqltype.Value, error) { return v.HstoreFetch(ctx, h, sqltype.Null(text)) }, func() (sqltype.Value, error) { return v.HstoreExists(ctx, h, boolean, sqltype.Null(text), false) }, func() (sqltype.Value, error) { return v.HstoreFetchArray(ctx, h, array, sqltype.Null(array)) }} {
		got, err := f()
		if err != nil || !got.IsNull() {
			t.Fatal("strict NULL", err)
		}
	}
}

func TestHstoreConstructCancellation(t *testing.T) {
	h := hstoreUnitType(t, 65000)
	text, array, boolean := hstoreConstructBuiltins()
	background := context.Background()
	input, err := array.Input(background, "{"+strings.Repeat("a,1,", 512)+"a,1}", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err := h.Input(background, "a=>1", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, remaining := range []int{1, 8, 515} {
		ctx, cancel := context.WithCancel(background)
		reached := &hstoreCancelContext{Context: ctx, cancel: cancel, remaining: remaining}
		got, err := h.HstoreFromArray(reached, input)
		cancel()
		if !errors.Is(err, context.Canceled) || got.Type() != nil {
			t.Fatal("partial canceled constructor", remaining, err)
		}
	}
	ctx, cancel := context.WithCancel(background)
	cancel()
	key := sqltype.String(text, "a")
	for _, f := range []func() (sqltype.Value, error){func() (sqltype.Value, error) { return h.HstoreFromText(ctx, key, key) }, func() (sqltype.Value, error) { return h.HstoreFromArrays(ctx, input, input) }, func() (sqltype.Value, error) { return v.HstoreFetch(ctx, h, key) }, func() (sqltype.Value, error) { return v.HstoreExists(ctx, h, boolean, key, false) }, func() (sqltype.Value, error) { return v.HstoreFetchArray(ctx, h, array, input) }} {
		got, err := f()
		if !errors.Is(err, context.Canceled) || got.Type() != nil {
			t.Fatal("canceled operation published", err)
		}
	}
}
