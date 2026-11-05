package enginetests_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineReplacementCompatibility(t *testing.T) {
	t.Run("kind", func(t *testing.T) {
		for _, tc := range []struct { kind catalog.RoutineKind; noun string }{{catalog.ProcedureRoutine, "procedure"}, {catalog.AggregateRoutine, "aggregate function"}} {
			previous := &catalog.Procedure{ObjectName: "replacement_target", RoutineKind: tc.kind}
			got := catalog.CheckRoutineReplacementKind(previous, catalog.FunctionRoutine)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot change routine kind", Detail: "\"replacement_target\" is a " + tc.noun + "."}
			if !reflect.DeepEqual(got, want) { t.Fatal("exact kind compatibility diagnostic", got, want) }
			if catalog.CheckRoutineReplacementKind(previous, tc.kind) != nil || previous.RoutineKind != tc.kind { t.Fatal("kind comparison mutated or rejected the incumbent") }
		}
	})
	byID := func() map[sqltype.ID]*sqltype.Descriptor {
		out := make(map[sqltype.ID]*sqltype.Descriptor)
		for _, typ := range sqltype.Builtins() { out[typ.ID()] = typ }
		return out
	}
	types, foreign := byID(), byID()
	formals := []proc.Formal{
		{Mode: "t", Name: "bufferid", Usage: types[sqltype.Int4].Usage()},
		{Mode: "t", Name: "os_page_num", Usage: types[sqltype.Int8].Usage()},
		{Mode: "t", Name: "numa_node", Usage: types[sqltype.Int4].Usage()},
	}
	layout := func(formals []proc.Formal) *proc.Definition {
		t.Helper()
		definition := &proc.Definition{ReturnsSet: true}
		if err := definition.SetFormals(formals); err != nil { t.Fatal(err) }
		tuple, err := definition.OutputTuple()
		if err != nil { t.Fatal(err) }
		definition.Result = tuple.Usage()
		return definition
	}
	old := &catalog.Procedure{ObjectName: "replacement_target", RoutineKind: catalog.FunctionRoutine, Result: 2249, SQL: layout(slices.Clone(formals))}
	original := old.SQL.Result.Type
	savedFormals := slices.Clone(old.SQL.Formals)
	for _, tc := range []struct {
		name string
		change func([]proc.Formal)
	}{
		{"name", func(fields []proc.Formal) { fields[1].Name = "changed" }},
		{"type", func(fields []proc.Formal) { fields[1].Usage = types[sqltype.Int4].Usage() }},
		{"resident_generation", func(fields []proc.Formal) { fields[1].Usage = foreign[sqltype.Int8].Usage() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := slices.Clone(formals)
			tc.change(fields)
			got := catalog.CheckRoutineReplacementResult(old, 2249, true, layout(fields))
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "cannot change return type of existing function", Detail: "Row type defined by OUT parameters is different."}
			if !reflect.DeepEqual(got, want) { t.Fatal("exact declaration-owned tuple mismatch", got, want) }
		})
	}
	for _, tc := range []struct { name string; result ids.OID; set bool; next *proc.Definition; detail string }{
		{"record_to_native_anonymous", 2249, true, nil, "Row type defined by OUT parameters is different."},
		{"result_before_tuple", 23, true, nil, ""},
		{"set_before_tuple", 2249, false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := catalog.CheckRoutineReplacementResult(old, tc.result, tc.set, tc.next)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "cannot change return type of existing function", Detail: tc.detail}
			if !reflect.DeepEqual(got, want) { t.Fatal("result/set/tuple diagnostic precedence", got, want) }
		})
	}
	if err := catalog.CheckRoutineReplacementResult(old, 2249, true, layout(slices.Clone(formals))); err != nil { t.Fatal("equivalent owned OUT tuple rejected", err) }
	anonymous := &catalog.Procedure{ObjectName: "replacement_target", RoutineKind: catalog.FunctionRoutine, Result: 2249, SQL: &proc.Definition{ReturnsSet: true, Result: types[2249].Usage()}}
	if err := catalog.CheckRoutineReplacementResult(anonymous, 2249, true, nil); err != nil { t.Fatal("runtime-defined RECORD must reach membership checks", err) }
	fixed := &catalog.Procedure{ObjectName: "replacement_target", RoutineKind: catalog.FunctionRoutine, Result: 2249, Builtin: &catalog.BuiltinRoutineDeclaration{ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 20, 23}, "bufferid", "os_page_num", "numa_node")}}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "cannot change return type of existing function", Detail: "Row type defined by OUT parameters is different."}
	if got := catalog.CheckRoutineReplacementResult(fixed, 2249, true, nil); !reflect.DeepEqual(got, want) { t.Fatal("fixed native OUT tuple adopted anonymous result", got, want) }
	if old.SQL.Result.Type != original || !reflect.DeepEqual(old.SQL.Formals, savedFormals) { t.Fatal("compatibility comparison mutated incumbent declaration ownership") }
}
