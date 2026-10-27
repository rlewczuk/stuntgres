package proc

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRoutineProcedureOutputFrame(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		formals      []string
		inputs       []int64
		outputs      []int64
		null         bool
	}{
		{"function_return", "BEGIN answer:=7;n:=8;RETURN;END", []string{"o", "o"}, nil, []int64{7, 8}, false},
		{"function_fallthrough", "BEGIN answer:=n+1;n:=n+2;END", []string{"o", "b"}, []int64{10}, []int64{11, 12}, false},
		{"function_null", "BEGIN RETURN;END", []string{"o", "o"}, nil, nil, true},
		{"sole", "BEGIN answer:=7;RETURN;END", []string{"o"}, nil, []int64{7}, false},
		{"fallthrough", "BEGIN BEGIN answer:=n+1;n:=n+2;END;END", []string{"o", "b"}, []int64{10}, []int64{11, 12}, false},
		{"null", "BEGIN RETURN;END", []string{"o"}, nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newWalkHost(t)
			procedure := tc.name != "function_return" && tc.name != "function_fallthrough" && tc.name != "function_null"
			d := &Definition{Procedure: procedure, Kind: PLpgSQLBody, Name: "output_frame", Source: tc.source}
			formals := make([]Formal, len(tc.formals))
			for i, mode := range tc.formals {
				name := "answer"
				if i == 1 {
					name = "n"
				}
				formals[i] = Formal{Mode: mode, Name: name, Usage: h.integer.Usage()}
			}
			if err := d.SetFormals(formals); err != nil {
				t.Fatal(err)
			}
			tuple, err := d.OutputTuple()
			if err != nil {
				t.Fatal(err)
			}
			d.Result = tuple.Usage()
			raw, err := parser.ParsePLpgSQL(tc.source, PLReturnPolicy(d.Result, len(d.OutputFormals), procedure))
			if err != nil {
				t.Fatal(err)
			}
			d.PL, err = CompilePL(d, raw, procedure, plUnitCollation(t), plUnitResolver(t, h.integer.Usage()))
			if err != nil {
				t.Fatal(err)
			}
			frame := NewFrame(d.Inputs)
			defer frame.Close()
			for i, n := range tc.inputs {
				v, err := sqltype.Integer(h.integer, n)
				if err != nil {
					t.Fatal(err)
				}
				if err = frame.Bind(i, v, d.Inputs[i]); err != nil {
					t.Fatal(err)
				}
			}
			run := &PLRun{Host: h, Definition: d}
			value, err := run.Invoke(frame, sqltype.CollationRef{})
			if err != nil {
				t.Fatal(err)
			}
			if value.Type() != tuple || value.IsNull() || value.CompositeLen() != len(d.OutputFormals) || run.Frame.Values != nil || run.Frame.Scopes != nil {
				t.Fatal("output tuple/frame ownership", value, run.Frame)
			}
			if tc.null {
				if !value.CompositeField(0).IsNull() {
					t.Fatal("unassigned OUT not typed NULL")
				}
			} else {
				for i, n := range tc.outputs {
					got, err := value.CompositeField(i).Integer()
					if err != nil || got != n {
						t.Fatal(got, n, err)
					}
				}
			}
			second, err := run.Invoke(frame, sqltype.CollationRef{})
			if err != nil || !sqltype.SameDatum(value, second) {
				t.Fatal("repeated frame overwrote detached output", err)
			}
			for i, n := range tc.inputs {
				got, err := frame.Parameters[i].Integer()
				if err != nil || got != n {
					t.Fatal("procedure copied output into caller frame", got, err)
				}
			}
			if d.PL.Variables[0].Name != "answer" || d.Formals[0].Usage != h.integer.Usage() {
				t.Fatal("invocation changed published program")
			}
		})
	}
	for _, canceled := range []bool{false, true} {
		h := newWalkHost(t)
		d := &Definition{Procedure: true, Kind: PLpgSQLBody, Name: "failed_frame", Source: "BEGIN answer:=7;answer:=1/0;END"}
		if err := d.SetFormals([]Formal{{Mode: "o", Name: "answer", Usage: h.integer.Usage()}}); err != nil {
			t.Fatal(err)
		}
		tuple, err := d.OutputTuple()
		if err != nil {
			t.Fatal(err)
		}
		d.Result = tuple.Usage()
		raw, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, 1, true))
		if err != nil {
			t.Fatal(err)
		}
		d.PL, err = CompilePL(d, raw, true, plUnitCollation(t), plUnitResolver(t, h.integer.Usage()))
		if err != nil {
			t.Fatal(err)
		}
		if canceled {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.ctx = ctx
			h.onEvaluate = func(*PLFrame) { cancel() }
		}
		input := NewFrame(nil)
		run := &PLRun{Host: h, Definition: d}
		value, err := run.Invoke(input, sqltype.CollationRef{})
		input.Close()
		if err == nil || value.Type() != nil || run.Frame.Values != nil || run.Frame.Scopes != nil {
			t.Fatal("failed output/frame publication", value, err)
		}
		if canceled && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if !canceled && walkState(err) != "22012" {
			t.Fatal(err)
		}
	}
	h := newWalkHost(t)
	child, err := sqltype.Composite(98000, "child", "child", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "n", Usage: h.integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	d := &Definition{Procedure: true, Kind: PLpgSQLBody, Name: "composite_frame", Source: "BEGIN RETURN;END"}
	if err = d.SetFormals([]Formal{{Mode: "b", Name: "item", Usage: child.Usage()}}); err != nil {
		t.Fatal(err)
	}
	tuple, err := d.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	d.Result = tuple.Usage()
	raw, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, 1, true))
	if err != nil {
		t.Fatal(err)
	}
	d.PL, err = CompilePL(d, raw, true, plUnitCollation(t), plUnitResolver(t, h.integer.Usage()))
	if err != nil {
		t.Fatal(err)
	}
	n, err := sqltype.Integer(h.integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	member, err := sqltype.CompositeValue(child, []sqltype.Value{n})
	if err != nil {
		t.Fatal(err)
	}
	input := NewFrame(d.Inputs)
	if err = input.Bind(0, member, child.Usage()); err != nil {
		t.Fatal(err)
	}
	value, err := (&PLRun{Host: h, Definition: d}).Invoke(input, sqltype.CollationRef{})
	input.Close()
	if err != nil || value.Type() != tuple || value.CompositeLen() != 1 || !sqltype.SameDatum(value.CompositeField(0), member) {
		t.Fatal("sole composite OUT collapsed or borrowed frame", err)
	}
}

func TestRoutineSoleOutputResultPolicy(t *testing.T) {
	for _, procedure := range []bool{false, true} {
		t.Run(map[bool]string{false: "function", true: "procedure"}[procedure], func(t *testing.T) {
			h := newWalkHost(t)
			d := &Definition{Procedure: procedure, Kind: PLpgSQLBody, Name: "sole_output", Source: "BEGIN answer:=7;RETURN;END", Result: h.integer.Usage()}
			if err := d.SetFormals([]Formal{{Mode: "o", Name: "answer", Usage: h.integer.Usage()}}); err != nil {
				t.Fatal(err)
			}
			if d.DerivedResult() != procedure {
				t.Fatal("sole OUT result policy", procedure)
			}
			if procedure {
				tuple, err := d.OutputTuple()
				if err != nil {
					t.Fatal(err)
				}
				d.Result = tuple.Usage()
			}
			raw, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, 1, procedure))
			if err != nil {
				t.Fatal(err)
			}
			d.PL, err = CompilePL(d, raw, procedure, plUnitCollation(t), plUnitResolver(t, h.integer.Usage()))
			if err != nil {
				t.Fatal(err)
			}
			frame := NewFrame(d.Inputs)
			defer frame.Close()
			run := &PLRun{Host: h, Definition: d}
			value, err := run.Invoke(frame, sqltype.CollationRef{})
			if err != nil || value.Type() != d.Result.Type || run.Frame.Values != nil || run.Frame.Scopes != nil {
				t.Fatal("result/frame authority", value, err)
			}
			if procedure {
				if value.CompositeLen() != 1 || !d.MatchesOutputTuple(value.Type()) {
					t.Fatal("sole procedure OUT collapsed", value)
				}
				value = value.CompositeField(0)
			}
			got, err := value.Integer()
			if err != nil || got != 7 {
				t.Fatal("sole OUT value", got, err)
			}
		})
	}
}
