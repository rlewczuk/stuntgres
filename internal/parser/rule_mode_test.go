package parser

import (
	"strings"
	"testing"
)

func TestRuleModeGrammar(t *testing.T) {
	for _, tc := range []struct {
		action string
		kind   AlterTableActionKind
	}{
		{"ENABLE RULE", EnableRule}, {"DISABLE RULE", DisableRule}, {"ENABLE REPLICA RULE", EnableReplicaRule}, {"ENABLE ALWAYS RULE", EnableAlwaysRule},
	} {
		q := "ALTER TABLE IF EXISTS public.src " + tc.action + " \"select\""
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		s, ok := statements[0].(*AlterTable)
		if !ok || !s.IfExists || len(s.Actions) != 1 || len(s.Name.Parts) != 2 {
			t.Fatal(statements)
		}
		a := s.Actions[0]
		if a.Action != tc.kind || a.Rule.Text != "select" || a.Rule.Pos.Offset != strings.Index(q, "\"select\"") || a.Pos.Offset != strings.Index(q, tc.action) {
			t.Fatal(a)
		}
	}
	statements, err := Parse("ALTER TABLE src DISABLE RULE a, ENABLE ALWAYS RULE b")
	if err != nil {
		t.Fatal(err)
	}
	if actions := statements[0].(*AlterTable).Actions; len(actions) != 2 || actions[0].Action != DisableRule || actions[1].Action != EnableAlwaysRule {
		t.Fatal(actions)
	}
	for _, q := range []string{"ALTER TABLE src DISABLE REPLICA RULE r", "ALTER TABLE src ENABLE RULE IF EXISTS r", "ALTER TABLE src ENABLE RULE ALL", "ALTER TABLE src DISABLE RULE r trailing", "ALTER TABLE src ENABLE SOMETIMES RULE r", "ALTER TABLE src \"enable\" RULE r"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted incomplete/unsupported syntax", q)
		}
	}
}
