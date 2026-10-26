package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPseudoFamilyBinding(t *testing.T) {
	b,_ := tableOIDBinder(t)
	usage := func(id sqltype.ID) sqltype.Usage { return b.Type(id).Usage() }
	for _, tc := range []struct{declared,actual []sqltype.ID; result,want sqltype.ID; valid bool}{
		{[]sqltype.ID{2283},[]sqltype.ID{sqltype.Int4},2283,sqltype.Int4,true},
		{[]sqltype.ID{2283,2283},[]sqltype.ID{sqltype.Int4,sqltype.Text},2283,0,false},
		{[]sqltype.ID{2277,2283},[]sqltype.ID{1007,sqltype.Int4},2283,sqltype.Int4,true},
		{[]sqltype.ID{5077,5077},[]sqltype.ID{sqltype.Int4,sqltype.Numeric},5077,sqltype.Numeric,true},
		{[]sqltype.ID{5077,5077},[]sqltype.ID{0,0},5077,sqltype.Text,true},
		{[]sqltype.ID{2283,5077,5077},[]sqltype.ID{sqltype.Int4,sqltype.Int4,sqltype.Numeric},2283,sqltype.Int4,true},
		{[]sqltype.ID{2776},[]sqltype.ID{1007},2776,0,false},
	} {
		d := &proc.Definition{Result:usage(tc.result)}
		operands := make([]functions.Argument,len(tc.actual))
		candidate := SQLCallable{Procedure:&catalog.Procedure{SQL:d},InputMap:make([]plan.RoutineInput,len(tc.actual))}
		for i,id := range tc.declared { d.Inputs = append(d.Inputs,usage(id)); candidate.Inputs = append(candidate.Inputs,usage(id)); candidate.InputMap[i] = plan.RoutineInput{Start:i,Count:1} }
		for i,id := range tc.actual { if id != 0 { operands[i].Usage = usage(id) } }
		valid := b.probeRoutineSignature(operands,&candidate)
		if valid != tc.valid { t.Fatalf("%+v: applicability %v",tc,valid) }
		if valid && (candidate.Signature.Result.Type == nil || candidate.Signature.Result.Type.ID() != tc.want || !candidate.Signature.ValidFor(d)) { t.Fatalf("%+v: signature %#v",tc,candidate.Signature) }
		for i,id := range tc.declared { if d.Inputs[i].Type.ID() != id { t.Fatal("probe mutated declaration") } }
	}
}

func TestPseudoInternalApplicability(t *testing.T) {
	b,_ := tableOIDBinder(t)
	internal := b.Type(2281).Usage()
	for _, actual := range []sqltype.Usage{{},b.Type(sqltype.Unknown).Usage(),b.Type(sqltype.Int4).Usage(),internal} {
		candidate := SQLCallable{Procedure:&catalog.Procedure{Result:32},Inputs:[]sqltype.Usage{internal}}
		valid := b.ProbeSQLCallable(parser.FunctionExpr{},[]functions.Argument{{Usage:actual}},&candidate)
		want := actual.Type == internal.Type
		if valid != want { t.Fatalf("INTERNAL actual %v: applicability %v want %v",actual,valid,want) }
		if valid && (len(candidate.Targets) != 1 || candidate.Targets[0] != internal) { t.Fatal("lost INTERNAL selected target",candidate.Targets) }
		if internal.Type.Concrete() { t.Fatal("signature lookup granted INTERNAL datum authority") }
	}
}


func TestPseudoInternalMetadataSelection(t *testing.T) {
	b, _ := tableOIDBinder(t)
	for _, tc := range []struct {
		name      string
		signature catalog.ProcedureSignature
		message   string
	}{
		{"pg_ddl_command_recv", "2281", `functions accepting type "internal" cannot be called explicitly`},
		{"array_append_support", "2281", `functions accepting type "internal" cannot be called explicitly`},
		{"internal_in", "2275", `functions returning type "internal" cannot be called explicitly`},
	} {
		if !catalog.HasBuiltinInternalRoutine(tc.name) {
			t.Fatalf("%s bypasses selected-call admission", tc.name)
		}
		identity, ok := catalog.BuiltinRoutineIdentityBySignature(tc.name, tc.signature)
		if !ok {
			t.Fatalf("%s missing released signature", tc.name)
		}
		result, ok := catalog.BuiltinRoutineResultByOID(identity.ID)
		if !ok {
			t.Fatalf("%s missing released result", tc.name)
		}
		d := &catalog.Procedure{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID},
			ObjectName: tc.name,
			Signature: tc.signature,
			Result: result,
			RoutineKind: catalog.FunctionRoutine,
		}
		raw := parser.FunctionExpr{Args: []parser.Expr{parser.ParameterExpr{Number: 1}}}
		candidate, valid := SQLCallableShape(b.BindingCatalog(), nil, d, raw)
		if !valid || len(candidate.Inputs) != 1 {
			t.Fatalf("%s has no metadata candidate", tc.name)
		}
		actual := candidate.Inputs[0]
		if tc.name == "internal_in" {
			actual = sqltype.Usage{} // Genuine unknown string can bind CSTRING.
		}
		if !b.ProbeSQLCallable(raw, []functions.Argument{{Usage: actual}}, &candidate) {
			t.Fatalf("%s rejected lawful actual", tc.name)
		}
		pos := parser.Position{Character: 43}
		diagnostic, ok := candidate.internalCallError(pos).(*sqlstate.Error)
		if !ok || diagnostic.Code != "0A000" || diagnostic.Message != tc.message || diagnostic.Position != "43" {
			t.Fatalf("%s safety diagnostic: %#v", tc.name, diagnostic)
		}
		if d.Builtin != nil || d.Implementation != nil || d.SQL != nil {
			t.Fatalf("%s metadata gained executable authority", tc.name)
		}
		if tc.name != "internal_in" && b.ProbeSQLCallable(raw, []functions.Argument{{}}, &candidate) {
			t.Fatalf("%s admitted UNKNOWN to INTERNAL", tc.name)
		}
	}
	if catalog.HasBuiltinInternalRoutine("pseudo_missing") || catalog.HasBuiltinInternalRoutine("lower") {
		t.Fatal("non-INTERNAL names gained metadata admission")
	}
}
