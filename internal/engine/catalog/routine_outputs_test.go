package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestRoutineOutputDeclarationOwnership(t *testing.T) {
	types := []ids.OID{23, 25}
	names := []string{"input", "number", "label"}
	outputs := NewRoutineOutputs(types, names...)
	types[0], names[0], names[2] = 20, "changed", "changed"
	if outputs.Len() != 2 || outputs.Type(0) != 23 || outputs.Type(1) != 25 || outputs.Name(0) != "input" || outputs.Name(2) != "label" || outputs.Inputs() != 1 {
		t.Fatal("declaration retained mutable input backing")
	}
	if !outputs.MatchesInputs("26") || outputs.MatchesInputs("") || outputs.MatchesInputs("26,25") {
		t.Fatal("fixed output/input shape agreement")
	}
	installed := InstalledRoutineDeclaration{Outputs: outputs}
	copy := installed
	if copy != installed || copy.Outputs != outputs {
		t.Fatal("installed identity is not comparable/shared")
	}
	foreign := installed
	foreign.Outputs = NewRoutineOutputs([]ids.OID{23, 25}, "input", "number", "label")
	if foreign == installed {
		t.Fatal("foreign declaration acquired installed identity")
	}
	p := Procedure{Installed: installed}
	if p.OutputDeclaration() != nil {
		t.Fatal("uninstalled payload granted a native declaration")
	}
	p.Builtin = &BuiltinRoutineDeclaration{Outputs: outputs}
	if p.OutputDeclaration() != outputs {
		t.Fatal("builtin declaration not retained")
	}
	for _, tc := range []struct {
		types []ids.OID
		names []string
	}{
		{nil, nil}, {[]ids.OID{23}, nil}, {[]ids.OID{0}, []string{"result"}}, {[]ids.OID{23}, []string{"input", ""}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("malformed fixed output declaration admitted")
				}
			}()
			NewRoutineOutputs(tc.types, tc.names...)
		}()
	}
}
