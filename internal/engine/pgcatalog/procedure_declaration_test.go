package pgcatalog

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestProcedureDeclarationProjection(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	array, err := sqltype.Array(1007, "_int4", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name    string
		formals []proc.Formal
		types   []ids.OID
		modes   []string
		inputs  []int
	}{
		{"all-in", []proc.Formal{{Mode: "i", Usage: types[sqltype.Int4].Usage()}}, []ids.OID{23}, []string{"i"}, []int{0}},
		{"mixed", []proc.Formal{{Mode: "i", Name: "a", Usage: types[sqltype.Int4].Usage()}, {Mode: "o", Name: "b", Usage: types[sqltype.Text].Usage()}, {Mode: "b", Name: "c", Usage: types[sqltype.Int4].Usage(), HasDefault: true}}, []ids.OID{23, 25, 23}, []string{"i", "o", "b"}, []int{0, -1, 1}},
		{"out-only", []proc.Formal{{Mode: "o", Usage: types[sqltype.Int4].Usage()}}, []ids.OID{23}, []string{"o"}, []int{-1}},
		{"table", []proc.Formal{{Mode: "t", Usage: types[sqltype.Text].Usage()}}, []ids.OID{25}, []string{"t"}, []int{-1}},
		{"variadic", []proc.Formal{{Mode: "v", Usage: array.Usage()}}, []ids.OID{1007}, []string{"v"}, []int{0}},
		{"zero", nil, nil, nil, nil},
	} {
		t.Run(x.name, func(t *testing.T) {
			d := &catalog.Procedure{SQL: &proc.Definition{}}
			if err := d.SQL.SetFormals(x.formals); err != nil {
				t.Fatal(err)
			}
			var gotTypes []ids.OID
			var gotModes []string
			var gotInputs []int
			err := VisitProcedureFormals(context.Background(), d, sqltype.Value{}, func(f ProcedureFormal) error {
				if f.Index != len(gotTypes) {
					t.Fatal("ordinal", f)
				}
				gotTypes = append(gotTypes, f.Type)
				gotModes = append(gotModes, f.Mode)
				gotInputs = append(gotInputs, f.Input)
				return nil
			})
			if err != nil || !reflect.DeepEqual(gotTypes, x.types) || !reflect.DeepEqual(gotModes, x.modes) || !reflect.DeepEqual(gotInputs, x.inputs) {
				t.Fatal(gotTypes, gotModes, gotInputs, err)
			}
		})
	}
	for _, signature := range []catalog.ProcedureSignature{"bad", "23,", "0", "23,,25"} {
		d := &catalog.Procedure{Signature: signature}
		if err := VisitProcedureFormals(context.Background(), d, sqltype.Value{}, func(ProcedureFormal) error { return nil }); err == nil {
			t.Fatal("bad signature", signature)
		}
	}
	fixed := catalog.NewRoutineOutputs([]ids.OID{25, 3802}, "from_json", "key", "value")
	d := &catalog.Procedure{Signature: "3802", Builtin: &catalog.BuiltinRoutineDeclaration{Outputs: fixed}}
	var got []ProcedureFormal
	if err := VisitProcedureFormals(context.Background(), d, sqltype.Value{}, func(f ProcedureFormal) error { got = append(got, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Type != 3802 || got[0].Input != 0 || got[1].Type != 25 || got[1].Input != -1 || got[1].Mode != "o" || got[2].Type != 3802 {
		t.Fatal(got)
	}
	d.Signature = "3802,23"
	if err := VisitProcedureFormals(context.Background(), d, sqltype.Value{}, func(ProcedureFormal) error { return nil }); err == nil {
		t.Fatal("fixed output input mismatch")
	}
}
