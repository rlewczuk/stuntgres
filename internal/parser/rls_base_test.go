package parser

import "testing"

func TestRLSBaseGrammar(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", "IF EXISTS t"} {
		ast, err := Parse("ALTER TABLE " + target + " ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		if err != nil {
			t.Fatal(target, err)
		}
		s, ok := ast[0].(*AlterTable)
		if !ok || len(s.Actions) != 4 || s.IfExists != (target == "IF EXISTS t") {
			t.Fatal(target, s)
		}
		for i, want := range []AlterTableActionKind{EnableRowSecurity, ForceRowSecurity, DisableRowSecurity, NoForceRowSecurity} {
			if s.Actions[i].Action != want || s.Actions[i].Pos.Character == 0 {
				t.Fatal(target, s.Actions)
			}
		}
	}
	for _, action := range []struct {
		sql  string
		kind AlterTableActionKind
	}{
		{"ENABLE", EnableRowSecurity}, {"DISABLE", DisableRowSecurity},
		{"FORCE", ForceRowSecurity}, {"NO FORCE", NoForceRowSecurity},
	} {
		ast, err := Parse("ALTER TABLE IF EXISTS rls_absent " + action.sql + " ROW LEVEL SECURITY")
		if err != nil {
			t.Fatal(action.sql, err)
		}
		s, ok := ast[0].(*AlterTable)
		if !ok || !s.IfExists || len(s.Actions) != 1 || s.Actions[0].Action != action.kind {
			t.Fatal(action.sql, s)
		}
	}
	for _, q := range []string{"ALTER TABLE t ENABLE ROW SECURITY", "ALTER TABLE t NO ENABLE ROW LEVEL SECURITY", "ALTER TABLE t ENABLE ROW LEVEL SECURITY garbage", "ALTER TABLE t ENABLE ROW LEVEL SECURITY,", "CREATE TABLE prefix(id INT); ALTER TABLE t FORCE ROW SECURITY"} {
		if ast, err := Parse(q); err == nil || ast != nil {
			t.Fatal("accepted invalid full input", q, ast, err)
		}
	}
}
