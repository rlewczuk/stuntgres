package proc

import (
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestRoutineParameterLayout(t *testing.T) {
	integer := routineTestType(t, sqltype.Int4)
	usage := integer.Usage()
	formals := []Formal{{Mode: "o", Name: "y", Usage: usage}, {Mode: "i", Name: "x", Usage: usage}, {Mode: "b", Name: "z", Usage: usage, HasDefault: true}}
	d := &Definition{}
	if err := d.SetFormals(formals); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.InputFormals, []int{1, 2}) || !reflect.DeepEqual(d.OutputFormals, []int{0, 2}) || !reflect.DeepEqual(d.FormalInputs, []int{-1, 0, 1}) || !reflect.DeepEqual(d.Names, []string{"x", "z"}) {
		t.Fatal("formal projections", d)
	}
	formals[1].Name = "changed"
	if d.Formals[1].Name != "x" {
		t.Fatal("published metadata aliases caller storage")
	}
	if d.OutputName() != "" {
		t.Fatal("multiple outputs acquired scalar name")
	}
	if err := d.SetFormals([]Formal{{Mode: "i", Name: "x", Usage: usage}, {Mode: "o", Name: "x", Usage: usage}}); err != nil {
		t.Fatal("pure IN/OUT name reuse", err)
	}
	if d.OutputName() != "x" {
		t.Fatal("sole output name", d.OutputName())
	}
	for _, fs := range [][]Formal{
		{{Mode: "i", Name: "x", Usage: usage}, {Mode: "b", Name: "x", Usage: usage}},
		{{Mode: "b", Name: "x", Usage: usage}, {Mode: "o", Name: "x", Usage: usage}},
		{{Mode: "i", Usage: usage, HasDefault: true}, {Mode: "o", Usage: usage}, {Mode: "i", Usage: usage}},
	} {
		if err := d.SetFormals(fs); err == nil {
			t.Fatal("invalid projection accepted", fs)
		}
	}
	procedure := &Definition{Procedure: true}
	if err := procedure.SetFormals([]Formal{{Mode: "o", Usage: usage}, {Mode: "b", Usage: usage, HasDefault: true}}); err != nil {
		t.Fatal("OUT before defaulted INOUT", err)
	}
	array, err := sqltype.Array(1007, "_int4", integer)
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range [][]Formal{
		{{Mode: "i", Usage: usage, HasDefault: true}, {Mode: "o", Usage: usage}},
		{{Mode: "v", Usage: array.Usage()}, {Mode: "o", Usage: usage}},
	} {
		before := procedure.Formals
		if err := procedure.SetFormals(fs); err == nil {
			t.Fatal("invalid procedure layout accepted")
		}
		if &procedure.Formals[0] != &before[0] {
			t.Fatal("failed declaration published projections")
		}
		function := &Definition{}
		if err := function.SetFormals(fs); err != nil {
			t.Fatal("procedure restriction leaked into function", err)
		}
	}
}
