package pgcatalog

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestRoutineResultFormalCatalog(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	oids, err := sqltype.Array(1028, "_oid", types[sqltype.Oid])
	if err != nil {
		t.Fatal(err)
	}
	modes, err := sqltype.Array(1002, "_char", types[sqltype.Char])
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(1007, "_int4", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                 string
		formals              []proc.Formal
		wantTypes, wantModes string
	}{
		{"all input", []proc.Formal{{Mode: "i", Usage: types[sqltype.Int4].Usage()}}, "", ""},
		{"outputs", []proc.Formal{{Mode: "i", Usage: types[sqltype.Int4].Usage()}, {Mode: "o", Usage: types[sqltype.Text].Usage()}, {Mode: "b", Usage: types[sqltype.Int8].Usage()}}, "{23,25,20}", "{i,o,b}"},
		{"table", []proc.Formal{{Mode: "i", Usage: types[sqltype.Int4].Usage()}, {Mode: "t", Name: "a", Usage: types[sqltype.Int4].Usage()}, {Mode: "t", Name: "b", Usage: types[sqltype.Text].Usage()}}, "{23,23,25}", "{i,t,t}"},
		{"variadic", []proc.Formal{{Mode: "v", Usage: array.Usage()}}, "{1007}", "{v}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &catalog.Procedure{SQL: &proc.Definition{}}
			if err := d.SQL.SetFormals(tc.formals); err != nil {
				t.Fatal(err)
			}
			variadic, err := ProcedureVariadic(d)
			if err != nil {
				t.Fatal(err)
			}
			all, mode, err := procedureFormalArrays(context.Background(), d, oids, modes, variadic, sqltype.Value{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantTypes == "" {
				if !all.IsNull() || !mode.IsNull() || all.Type() != oids || mode.Type() != modes {
					t.Fatal("all-IN metadata must be typed NULL")
				}
				return
			}
			gotTypes, err := oids.Output(context.Background(), all)
			if err != nil {
				t.Fatal(err)
			}
			gotModes, err := modes.Output(context.Background(), mode)
			if err != nil {
				t.Fatal(err)
			}
			if gotTypes != tc.wantTypes || gotModes != tc.wantModes {
				t.Fatal("formal order/types/modes", gotTypes, gotModes)
			}
			if all.ArrayDimension(0).Lower != 1 || mode.ArrayDimension(0).Lower != 1 {
				t.Fatal("formal arrays must be one-based")
			}
		})
	}
	if AttributeNumber(ids.ProcedureClassID, "proallargtypes", 21) != 21 || AttributeNumber(ids.ProcedureClassID, "proargmodes", 22) != 22 {
		t.Fatal("released argument attribute numbers")
	}
}

func TestRoutineFormalArrayTypePresence(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	oids, err := sqltype.Array(1028, "_oid", types[sqltype.Oid])
	if err != nil {
		t.Fatal(err)
	}
	modes, err := sqltype.Array(1002, "_char", types[sqltype.Char])
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(1007, "_int4", types[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []bool{false, true} {
		d := &catalog.Procedure{SQL: &proc.Definition{}, Display: catalog.RoutineDisplay{OmitAllArgTypes: omitted}}
		if err := d.SQL.SetFormals([]proc.Formal{{Mode: "v", Usage: array.Usage()}}); err != nil {
			t.Fatal(err)
		}
		all, mode, err := procedureFormalArrays(context.Background(), d, oids, modes, 23, sqltype.Value{})
		if err != nil || all.Type() != oids || all.IsNull() != omitted {
			t.Fatal("declaration-owned type array presence", omitted, all, err)
		}
		text, err := modes.Output(context.Background(), mode)
		if err != nil || text != "{v}" {
			t.Fatal("type omission changed argument modes", text, err)
		}
		if !omitted {
			text, err = oids.Output(context.Background(), all)
			if err != nil || text != "{1007}" {
				t.Fatal("ordinary variadic type metadata changed", text, err)
			}
		}
		d.SQL.FormalInputs = nil
		if _, _, err := procedureFormalArrays(context.Background(), d, oids, modes, 23, sqltype.Value{}); err == nil {
			t.Fatal("type-array presence bypassed formal validation", omitted)
		}
	}
}

func TestRoutineProcedureDefaultCount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		declaration catalog.Procedure
		want        int64
	}{
		{"fixed", catalog.Procedure{}, 0},
		{"builtin", catalog.Procedure{Builtin: &catalog.BuiltinRoutineDeclaration{Defaults: 2}}, 2},
		{"installed", catalog.Procedure{Installed: catalog.InstalledRoutineDeclaration{Defaults: 5}}, 5},
		{"SQL formals", catalog.Procedure{SQL: &proc.Definition{Formals: []proc.Formal{{Mode: "o"}, {Mode: "i"}, {Mode: "b", HasDefault: true}, {Mode: "i", HasDefault: true}}}}, 2},
		{"variadic default", catalog.Procedure{SQL: &proc.Definition{Formals: []proc.Formal{{Mode: "o"}, {Mode: "v", HasDefault: true}}}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := procedureDefaultCount(&tc.declaration); got != tc.want {
				t.Fatal("declaration default count", got, tc.want)
			}
		})
	}
	if got := AttributeNumber(ids.ProcedureClassID, "pronargdefaults", 18); got != 18 {
		t.Fatal("released default-count attribute number", got)
	}
}
