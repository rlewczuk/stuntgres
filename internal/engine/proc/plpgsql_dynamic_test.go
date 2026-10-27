package proc

import (
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"testing"
)

func TestPLpgSQLDynamicSQLValidation(t *testing.T) {
	const source = "DECLARE r record; a integer; b integer; BEGIN EXECUTE r.command USING r.a,r.b INTO a,b; CASE 1 WHEN 1 THEN RETURN 7; END CASE; END"
	original := conditionalUnitDefinition(t, source)
	before, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(before, original.PLSyntax) {
		t.Fatal("compile mutated admitted syntax", err)
	}
	for _, name := range []string{"missing", "query", "using", "targets", "strict", "mode", "coordinated_mode", "span", "source", "variant", "ledger"} {
		t.Run(name, func(t *testing.T) {
			d := conditionalUnitDefinition(t, source)
			body := d.PL.Root.Statements[0].Block
			in := &body.Statements[0]
			switch name {
			case "missing":
				in.Dynamic = nil
			case "query":
				in.Dynamic.Command = &parser.Select{}
			case "using":
				in.Dynamic.Using = nil
			case "targets":
				in.Dynamic.Targets[0].Slot = d.PL.FoundSlot
			case "strict":
				in.Dynamic.Strict = true
			case "mode":
				in.Dynamic.ReturnQuery = true
			case "coordinated_mode":
				in.Dynamic.ReturnQuery = true
				in.Syntax.Dynamic.ReturnQuery = true
				d.ReturnsSet = true
			case "span":
				in.Syntax.Dynamic.Command.Start.Character++
			case "source":
				in.Syntax.Dynamic.Using[0].Expression = parser.LiteralExpr{}
			case "variant":
				in.SQL = &PLSQL{}
			case "ledger":
				body.Statements[1].Case.SearchName = "__Case__Variable_0__"
			}
			if d.PL.ValidateDefinition(d) == nil {
				t.Fatal("accepted forged dynamic program", name)
			}
		})
	}
	copyProgram := *original.PL
	copyProgram.Variables = slices.Clone(original.PL.Variables)
	if !original.PL.SameLayout(&copyProgram) || copyProgram.Root != original.PL.Root {
		t.Fatal("immutable syntax-only layout not shared")
	}
	other := conditionalUnitDefinition(t, "DECLARE x integer; BEGIN EXECUTE 'SELECT 1' INTO x USING 2; RETURN x; END")
	if original.PL.SameLayout(other.PL) {
		t.Fatal("different dynamic operands equivalent")
	}
	for _, command := range []string{"EXECUTE 'SELECT 1' INTO a USING r.a", "EXECUTE 'SELECT 1' USING r.a INTO a", "EXECUTE 'SELECT 1' INTO r USING r.a"} {
		conditionalUnitDefinition(t, "DECLARE r record; a integer; BEGIN "+command+"; CASE 1 WHEN 1 THEN RETURN 7; END CASE; END")
	}
}
