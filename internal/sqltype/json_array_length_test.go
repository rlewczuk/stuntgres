package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestJSONArrayLengthProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{JSON, JSONB} {
		name := "json_array_length"
		if id == JSONB {
			name = "jsonb_array_length"
		}
		p, err := BindBinary(name, types[Int4].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			source  string
			count   int64
			message string
		}{
			{"[]", 0, ""}, {` [null,[1,2],{"x":[3]},"a,b][\\\""] `, 4, ""},
			{"{}", 0, "cannot get array length of a non-array"},
			{"null", 0, "cannot get array length of a scalar"},
		} {
			t.Run(name+"/"+tc.source, func(t *testing.T) {
				v, err := types[id].Input(ctx, tc.source, -1)
				if err != nil {
					t.Fatal(err)
				}
				before := v
				got, err := p.Evaluate(ctx, v)
				if tc.message != "" {
					var record *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message}
					if !errors.As(err, &record) || *record != want || got != (Value{}) {
						t.Fatalf("got=%+v/%v want=%+v", record, got, want)
					}
				} else {
					n, intErr := got.Integer()
					if err != nil || intErr != nil || n != tc.count || got.Type() != types[Int4] {
						t.Fatal(got, err, intErr)
					}
				}
				if v != before {
					t.Fatal("provider mutated input")
				}
				out, err := types[id].Output(ctx, v)
				if err != nil {
					t.Fatal(err)
				}
				if id == JSON && out != tc.source {
					t.Fatal("retained source changed", out)
				}
				if id == JSONB {
					fresh, err := types[id].Input(ctx, out, -1)
					if err != nil {
						t.Fatal(err)
					}
					compare, err := compareJSONB(ctx, &v.jsonb.root, &fresh.jsonb.root, false)
					if err != nil || compare != 0 {
						t.Fatal("immutable payload changed", compare, err)
					}
				}
			})
		}
		null, err := p.Evaluate(ctx, Null(types[id]))
		if err != nil || !null.IsNull() || null.Type() != types[Int4] {
			t.Fatal(null, err)
		}
		if !p.Immutable() || p.Name() != name || p.ResultUsage() != types[Int4].Usage() {
			t.Fatal(p)
		}
	}
	p, err := BindBinary("json_array_length", types[Int4].Usage(), types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`["\u0000","\ud800",1e1000000]`, strings.Repeat("[", 1024) + "0" + strings.Repeat("]", 1024)} {
		v, err := types[JSON].Input(ctx, source, -1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(ctx, v)
		want := int64(3)
		if source[1] == '[' {
			want = 1
		}
		n, intErr := got.Integer()
		if err != nil || intErr != nil || n != want || v.text != source {
			t.Fatal(got, err, intErr)
		}
	}
}

func TestJSONArrayLengthDescriptorContracts(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	for _, id := range []ID{JSON, JSONB} {
		name := "json_array_length"
		if id == JSONB {
			name = "jsonb_array_length"
		}
		for _, args := range [][]Usage{nil, {types[id].Usage(), types[id].Usage()}, {types[Text].Usage()}, {{Type: types[id], Typmod: 0}}, {{}}} {
			if _, err := BindBinary(name, types[Int4].Usage(), args...); err == nil {
				t.Fatal("accepted invalid arguments", args)
			}
		}
		for _, result := range []Usage{types[Int8].Usage(), {Type: types[Int4], Typmod: 0}, {}} {
			if _, err := BindBinary(name, result, types[id].Usage()); err == nil {
				t.Fatal("accepted invalid result", result)
			}
		}
		p, err := BindBinary(name, types[Int4].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		v, err := foreign[id].Input(ctx, "[]", -1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(ctx, v); err == nil {
			t.Fatal("accepted foreign argument owner")
		}
		if _, err := p.Evaluate(ctx); err == nil {
			t.Fatal("accepted missing value")
		}
		if _, err := p.Evaluate(ctx, Null(types[Text])); err == nil {
			t.Fatal("accepted wrong NULL descriptor")
		}
		if id == JSONB {
			if _, err := p.Evaluate(ctx, Value{typ: types[id]}); err == nil {
				t.Fatal("missing payload produced success")
			}
			if _, err := p.Evaluate(ctx, Value{typ: types[id], jsonb: &jsonbPayload{root: jsonbNode{kind: 255}}}); err == nil {
				t.Fatal("invalid root produced success")
			}
		}
	}
	for _, source := range []string{"", "[", "[[]", "[] []"} {
		if _, err := jsonArrayLength(ctx, source); err == nil {
			t.Fatal("invalid retained source produced success", source)
		}
	}
	if _, err := Integer(types[Int4], int64(1)<<31); err == nil {
		t.Fatal("count overflow truncated")
	}
}

func TestJSONArrayLengthCancellationAndAllocations(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{JSON, JSONB} {
		name := "json_array_length"
		if id == JSONB {
			name = "jsonb_array_length"
		}
		p, err := BindBinary(name, types[Int4].Usage(), types[id].Usage())
		if err != nil {
			t.Fatal(err)
		}
		v, err := types[id].Input(ctx, `[[],{},null]`, -1)
		if err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		for _, arg := range []Value{v, Null(types[id])} {
			got, err := p.Evaluate(canceled, arg)
			if !errors.Is(err, context.Canceled) || got != (Value{}) {
				t.Fatal(got, err)
			}
		}
		if allocs := testing.AllocsPerRun(100, func() {
			got, err := p.Evaluate(ctx, v)
			if err != nil || got.integer != 3 {
				panic("invalid length")
			}
		}); allocs != 0 {
			t.Fatalf("%s allocations=%v want zero", name, allocs)
		}
	}
	p, err := BindBinary("json_array_length", types[Int4].Usage(), types[JSON].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`["` + strings.Repeat(`\u0041`, 4096) + `"]`,
		"[" + strings.Repeat(" ", 32768) + "0]",
		"[" + strings.Repeat("1", 32768) + "]",
		strings.Repeat("[", 4096) + "0" + strings.Repeat("]", 4096),
	} {
		v, err := types[JSON].Input(ctx, source, -1)
		if err != nil {
			t.Fatal(err)
		}
		calibration := &groupingCancelContext{Context: ctx, at: int(^uint(0) >> 1)}
		if _, err := p.Evaluate(calibration, v); err != nil || calibration.calls < 6 {
			t.Fatal("missing cancellation checkpoints", err, calibration.calls)
		}
		active := &groupingCancelContext{Context: ctx, at: 5}
		got, err := p.Evaluate(active, v)
		if !errors.Is(err, context.Canceled) || got != (Value{}) || v.text != source {
			t.Fatal("partial result or changed source", got, err)
		}
		if allocs := testing.AllocsPerRun(10, func() {
			if _, err := p.Evaluate(ctx, v); err != nil {
				panic(err)
			}
		}); allocs != 0 {
			t.Fatalf("retained scan allocations=%v want zero", allocs)
		}
	}
}
