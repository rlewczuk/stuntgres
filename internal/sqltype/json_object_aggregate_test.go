package sqltype

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func jsonObjectUnitPlan(t *testing.T, key, input, result *Descriptor, strict bool) JSONObjectAggregatePlan {
	t.Helper()
	p, err := BindJSONObjectAggregate(key.Usage(), input.Usage(), result.Usage(), strict, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func jsonObjectUnitState(t *testing.T, p JSONObjectAggregatePlan) *JSONObjectAggregateState {
	t.Helper()
	s, err := p.Init(context.Background(), OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func jsonObjectUnitOutput(t *testing.T, p JSONObjectAggregatePlan, s *JSONObjectAggregateState, want string) Value {
	t.Helper()
	v, err := p.Finalize(context.Background(), s)
	if err != nil || v.null || v.typ != p.result.Type || v.text != want {
		t.Fatalf("object=%q/%v want=%q err=%v", v.text, v.null, want, err)
	}
	return v
}

func TestJSONObjectAggregatePresenceAndOwnership(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	key := String(types[Text], "a")
	one, _ := Integer(types[Int4], 1)
	for _, id := range []ID{JSON, JSONB} {
		for _, strict := range []bool{false, true} {
			p := jsonObjectUnitPlan(t, types[Text], types[Int4], types[id], strict)
			zero := jsonObjectUnitState(t, p)
			v, err := p.Finalize(ctx, zero)
			if err != nil || !v.null || v.typ != types[id] {
				t.Fatal("unseen result", v, err)
			}
			s := jsonObjectUnitState(t, p)
			if err = p.Advance(ctx, s, key, Null(types[Int4])); err != nil {
				t.Fatal(err)
			}
			want := `{ "a" : null }`
			if id == JSONB {
				want = `{"a": null}`
			}
			if strict {
				want = `{  }`
				if id == JSONB {
					want = `{}`
				}
			}
			v = jsonObjectUnitOutput(t, p, s, want)
			again, err := p.Finalize(ctx, s)
			if err != nil || again != v {
				t.Fatal("finalize identity", again, err)
			}
			if err = p.Advance(ctx, s, key, one); err == nil {
				t.Fatal("sealed state accepted input")
			}
			other := jsonObjectUnitState(t, p)
			if err = p.Advance(ctx, other, key, one); err != nil {
				t.Fatal(err)
			}
			populated := `{ "a" : 1 }`
			if id == JSONB {
				populated = `{"a": 1}`
			}
			jsonObjectUnitOutput(t, p, other, populated)
			if v.text != want {
				t.Fatal("other group mutated published result")
			}
			if err = p.Advance(ctx, jsonObjectUnitState(t, p), String(foreign[Text], "a"), one); err == nil {
				t.Fatal("foreign key accepted")
			}
			old, _ := Integer(foreign[Int4], 1)
			if err = p.Advance(ctx, jsonObjectUnitState(t, p), key, old); err == nil {
				t.Fatal("foreign value accepted")
			}
			if _, err = p.Finalize(ctx, nil); err == nil {
				t.Fatal("nil state accepted")
			}
			wrong := jsonObjectUnitPlan(t, types[Text], types[Int4], types[id], !strict)
			if _, err = wrong.Finalize(ctx, jsonObjectUnitState(t, p)); err == nil {
				t.Fatal("wrong plan accepted state")
			}
			bad := jsonObjectUnitState(t, p)
			err = p.Advance(ctx, bad, Null(types[Text]), Null(types[Int4]))
			var d *sqlstate.Error
			code, message := "22004", "null value not allowed for object key"
			if id == JSONB {
				code, message = "22023", "field name must not be null"
			}
			if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Position != "" || d.Detail != "" || d.Hint != "" || d.Context != "" {
				t.Fatal("NULL key diagnostic", err)
			}
			if out, finalErr := p.Finalize(ctx, bad); finalErr != err || out != (Value{}) {
				t.Fatal("failed state published", out, finalErr)
			}
			if nextErr := p.Advance(ctx, bad, key, one); nextErr != err {
				t.Fatal("failure not sticky", nextErr)
			}
		}
	}
	for _, tc := range []struct{ key, input, result Usage }{
		{Usage{}, types[Int4].Usage(), types[JSON].Usage()},
		{types[Text].Usage(), Usage{}, types[JSON].Usage()},
		{types[Text].Usage(), types[Int4].Usage(), types[Int4].Usage()},
		{types[Text].Usage(), types[Int4].Usage(), Usage{Type: types[JSON], Typmod: 2}},
		{types[JSON].Usage(), types[Int4].Usage(), foreign[JSON].Usage()},
		{types[Text].Usage(), foreign[Text].Usage(), types[JSON].Usage()},
	} {
		if _, err := BindJSONObjectAggregate(tc.key, tc.input, tc.result, false, false); err == nil {
			t.Fatal("invalid usages accepted", tc)
		}
	}
	domain, err := Domain(92001, "jo_key", "jo_key", types[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(key)
	if err != nil {
		t.Fatal(err)
	}
	p := jsonObjectUnitPlan(t, domain, types[Int4], types[JSON], false)
	s := jsonObjectUnitState(t, p)
	if err = p.Advance(ctx, s, wrapped, one); err != nil {
		t.Fatal(err)
	}
	jsonObjectUnitOutput(t, p, s, `{ "a" : 1 }`)
}

func TestJSONObjectAggregateStrictConversionAndDuplicates(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	key := String(types[Text], "a")
	one, _ := Integer(types[Int4], 1)
	array, err := ArrayValue(types[1007], []ArrayDimension{{Lower: -2, Length: 2}}, []Value{one, Null(types[Int4])})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{JSON, JSONB} {
		p := jsonObjectUnitPlan(t, types[1007], types[JSON], types[id], true)
		s := jsonObjectUnitState(t, p)
		if err = p.Advance(ctx, s, array, Null(types[JSON])); err != nil {
			t.Fatal("strict SQL NULL converted key", err)
		}
		empty := `{  }`
		if id == JSONB {
			empty = `{}`
		}
		jsonObjectUnitOutput(t, p, s, empty)
		j, _ := types[JSON].Input(ctx, " null ", -1)
		s = jsonObjectUnitState(t, p)
		err = p.Advance(ctx, s, array, j)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22023" || d.Message != "key value must be scalar, not array, composite, or json" || d.Position != "" {
			t.Fatal("converted-null skipped key validation", err)
		}
		for _, strict := range []bool{false, true} {
			p = jsonObjectUnitPlan(t, types[Text], types[JSON], types[id], strict)
			for _, reverse := range []bool{false, true} {
				s = jsonObjectUnitState(t, p)
				values := []string{"1", "null", "2", "null"}
				if reverse {
					values = []string{"null", "2", "null", "1"}
				}
				for _, text := range values {
					v, err := types[JSON].Input(ctx, text, -1)
					if err != nil {
						t.Fatal(err)
					}
					if err = p.Advance(ctx, s, key, v); err != nil {
						t.Fatal(err)
					}
				}
				want := `{ "a" : 1, "a" : null, "a" : 2, "a" : null }`
				if reverse {
					want = `{ "a" : null, "a" : 2, "a" : null, "a" : 1 }`
				}
				if id == JSONB {
					want = `{"a": null}`
					if strict {
						want = `{"a": 2}`
					}
					if reverse {
						want = `{"a": 1}`
					}
				}
				jsonObjectUnitOutput(t, p, s, want)
			}
		}
	}
	for _, tc := range []struct {
		input      ID
		text, want string
	}{
		{JSON, " null ", `{}`}, {JSONB, "null", `{}`},
		{JSON, `{"n":null}`, `{"a": {"n": null}}`}, {JSONB, `[null]`, `{"a": [null]}`},
		{JSON, `"null"`, `{"a": "null"}`},
	} {
		v, err := types[tc.input].Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		p := jsonObjectUnitPlan(t, types[Text], types[tc.input], types[JSONB], true)
		s := jsonObjectUnitState(t, p)
		if err = p.Advance(ctx, s, key, v); err != nil {
			t.Fatal(err)
		}
		jsonObjectUnitOutput(t, p, s, tc.want)
	}
	p := jsonObjectUnitPlan(t, types[Text], types[JSON], types[JSONB], true)
	s := jsonObjectUnitState(t, p)
	bad, _ := types[JSON].Input(ctx, `{"x":"\u0000"}`, -1)
	err = p.Advance(ctx, s, key, bad)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22P05" {
		t.Fatal("losing invalid input accepted", err)
	}
	good, _ := types[JSON].Input(ctx, "1", -1)
	if err2 := p.Advance(ctx, s, key, good); err2 != err {
		t.Fatal("later duplicate hid failure", err2)
	}
	if out, err2 := p.Finalize(ctx, s); out != (Value{}) || err2 != err {
		t.Fatal("conversion error finalized", out, err2)
	}
}

func TestJSONObjectAggregateBorrowedValuesAndCanonicalization(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	j, err := types[JSONB].Input(ctx, `{"z":1,"a":null}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	p := jsonObjectUnitPlan(t, types[Text], types[JSONB], types[JSONB], true)
	s := jsonObjectUnitState(t, p)
	for _, key := range []string{"é", "aa", "z", "b"} {
		if err = p.Advance(ctx, s, String(types[Text], key), j); err != nil {
			t.Fatal(err)
		}
	}
	if &s.writer.tree.frames[0].node.fields[0].value.fields[0] != &j.jsonb.root.fields[0] {
		t.Fatal("typed JSONB payload copied instead of borrowed")
	}
	before := append([]jsonbEntry(nil), j.jsonb.root.fields...)
	jsonObjectUnitOutput(t, p, s, `{"b": {"a": null, "z": 1}, "z": {"a": null, "z": 1}, "aa": {"a": null, "z": 1}, "é": {"a": null, "z": 1}}`)
	if !reflect.DeepEqual(j.jsonb.root.fields, before) {
		t.Fatal("borrowed JSONB payload mutated")
	}
	one, _ := Integer(types[Int4], 1)
	array, err := ArrayValue(types[1007], []ArrayDimension{{Lower: 5, Length: 2}}, []Value{one, Null(types[Int4])})
	if err != nil {
		t.Fatal(err)
	}
	shape, err := RecordShape([]CompositeMember{{Attribute: 1, Name: "n", Usage: types[1007].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := OwnedCompositeValue(shape, []Value{array})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{JSON, JSONB} {
		p := jsonObjectUnitPlan(t, types[Text], shape, types[id], false)
		s := jsonObjectUnitState(t, p)
		if err = p.Advance(ctx, s, String(types[Text], "a"), row); err != nil {
			t.Fatal(err)
		}
		want := `{ "a" : {"n":[1,null]} }`
		if id == JSONB {
			want = `{"a": {"n": [1, null]}}`
		}
		jsonObjectUnitOutput(t, p, s, want)
	}
	if row.CompositeField(0).array != array.array || array.ArrayDimension(0).Lower != 5 || !array.ArrayElement(1).null {
		t.Fatal("borrowed structured input mutated")
	}
}

func TestJSONObjectAggregateReachedLoopCancellation(t *testing.T) {
	types := testTypes()
	base := context.Background()
	for _, tc := range []struct {
		id     ID
		target string
		final  bool
	}{
		{JSON, ".writeJSONBString", false},
		{JSONB, ".(*jsonCreateWriter).value", false},
		{JSONB, ".sortJSONBFields", true},
		{JSONB, ".compactJSONBFields", true},
		{JSONB, ".jsonbOutput", true},
	} {
		p := jsonObjectUnitPlan(t, types[Text], types[1007], types[tc.id], true)
		s := jsonObjectUnitState(t, p)
		one, _ := Integer(types[Int4], 1)
		array, err := ArrayValue(types[1007], []ArrayDimension{{Lower: 1, Length: 2}}, []Value{one, Null(types[Int4])})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(base)
		probe := &jsonAggregateLoopContext{Context: ctx, cancel: cancel, target: tc.target}
		if tc.final {
			for _, key := range []string{"b", "a"} {
				if err = p.Advance(base, s, String(types[Text], key), array); err != nil {
					cancel()
					t.Fatal(err)
				}
			}
			out, failure := p.Finalize(probe, s)
			if !probe.entered || !errors.Is(failure, context.Canceled) || out != (Value{}) {
				cancel()
				t.Fatal("reached final cancellation", tc, probe.entered, out, failure)
			}
		} else {
			if err = p.Advance(probe, s, String(types[Text], "é\"\\"), array); !probe.entered || !errors.Is(err, context.Canceled) {
				cancel()
				t.Fatal("reached conversion cancellation", tc, probe.entered, err)
			}
		}
		cancel()
		if out, err := p.Finalize(base, s); out != (Value{}) || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled state published", out, err)
		}
	}
}

func TestJSONObjectAggregateRootNullCompaction(t *testing.T) {
	ctx := context.Background()
	fields := []jsonbEntry{
		{key: "a", ordinal: 3, value: jsonbNode{kind: jsonbNull}},
		{key: "bb", ordinal: 2, value: jsonbNode{kind: jsonbNull}},
		{key: "a", ordinal: 1, value: jsonbNode{kind: jsonbBoolean, boolean: true}},
		{key: "bb", ordinal: 0, value: jsonbNode{kind: jsonbString, text: "kept"}},
	}
	if err := sortJSONBFields(ctx, fields); err != nil {
		t.Fatal(err)
	}
	out, err := compactJSONBFields(ctx, fields, true)
	if err != nil || len(out) != 2 || out[0].key != "a" || !out[0].value.boolean || out[1].key != "bb" || out[1].value.text != "kept" {
		t.Fatal("null removed surviving duplicate", out, err)
	}
	for _, entry := range fields[len(out):] {
		if !reflect.DeepEqual(entry, jsonbEntry{}) {
			t.Fatal("discarded entry retained")
		}
	}
}
