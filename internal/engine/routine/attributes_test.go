package routine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRoutineAttributeNormalization(t *testing.T) {
	original := catalog.Procedure{RoutineKind: catalog.FunctionRoutine, Volatility: expr.Volatile, Parallel: 'u', Cost: 100, Rows: 1000, Configuration: catalog.NewRoutineConfiguration([]string{"work_mem=64kB"})}
	candidate := original
	attributes := []parser.RoutineAttribute{{Kind: "immutable"}, {Kind: "strict"}, {Kind: "security", Value: "definer"}, {Kind: "parallel", Value: "restricted"}, {Kind: "cost", Value: "+7.5"}, {Kind: "rows", Value: "12"}}
	if err := normalizeRoutineAttributes(nil, nil, &candidate, attributes, true); err != nil {
		t.Fatal(err)
	}
	if candidate.Volatility != expr.Immutable || !candidate.Strict || !candidate.SecurityDefiner || candidate.Parallel != 'r' || candidate.Cost != 7.5 || candidate.Rows != 12 || candidate.Configuration != original.Configuration {
		t.Fatal("complete private candidate", candidate)
	}
	patch := candidate
	if err := normalizeRoutineAttributes(nil, nil, &patch, []parser.RoutineAttribute{{Kind: "strict", Value: "false"}}, true); err != nil {
		t.Fatal(err)
	}
	if patch.Strict || patch.Cost != candidate.Cost || patch.Volatility != candidate.Volatility || patch.Configuration != candidate.Configuration || !candidate.Strict {
		t.Fatal("ALTER patch changed unmentioned/retained fields")
	}
	invoker := candidate
	if err := normalizeRoutineAttributes(nil, nil, &invoker, []parser.RoutineAttribute{{Kind: "security", Value: "invoker"}}, true); err != nil {
		t.Fatal(err)
	}
	if invoker.SecurityDefiner || !candidate.SecurityDefiner || invoker.Configuration != candidate.Configuration || invoker.Volatility != candidate.Volatility {
		t.Fatal("normalized invoker must clear only the private candidate's security mode")
	}
	for _, tc := range []struct {
		attributes              []parser.RoutineAttribute
		set                     bool
		code, message, position string
	}{
		{[]parser.RoutineAttribute{{Kind: "rows", Value: "1"}, {Kind: "rows", Value: "2", Pos: parser.Position{Character: 42}}}, false, "42601", "conflicting or redundant options", "42"},
		{[]parser.RoutineAttribute{{Kind: "cost", Value: "-0"}}, true, "22023", "COST must be positive", ""},
		{[]parser.RoutineAttribute{{Kind: "rows", Value: "2"}}, false, "22023", "ROWS is not applicable when function does not return a set", ""},
		{[]parser.RoutineAttribute{{Kind: "parallel", Value: "arbitrary"}}, true, "42601", `parameter "parallel" must be SAFE, RESTRICTED, or UNSAFE`, ""},
	} {
		private := candidate
		err := normalizeRoutineAttributes(nil, nil, &private, tc.attributes, tc.set)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatal("semantic validation", err, diagnostic)
		}
		if original.Cost != 100 || original.Strict || original.Configuration.At(0) != "work_mem=64kB" || candidate.Cost != 7.5 {
			t.Fatal("rejected candidate modified publication")
		}
	}
	procedure := catalog.Procedure{RoutineKind: catalog.ProcedureRoutine}
	err := normalizeRoutineAttributes(nil, nil, &procedure, []parser.RoutineAttribute{{Kind: "cost", Value: "-1", Pos: parser.Position{Character: 9}}}, false)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P13" || diagnostic.Position != "9" {
		t.Fatal("procedure complete-option rejection", err)
	}
}
