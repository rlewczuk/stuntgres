package parser

import (
	"strings"
	"testing"
)

func TestTriggerModeGrammar(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		kind     AlterTableActionKind
		selector TriggerSelector
		name     string
	}{
		{`DISABLE TRIGGER a`, DisableTrigger, TriggerNamed, "a"},
		{`ENABLE TRIGGER ALL`, EnableTrigger, TriggerAll, ""},
		{`DISABLE TRIGGER USER`, DisableTrigger, TriggerUser, ""},
		{`ENABLE REPLICA TRIGGER "all"`, EnableReplicaTrigger, TriggerNamed, "all"},
		{`ENABLE ALWAYS TRIGGER "user"`, EnableAlwaysTrigger, TriggerNamed, "user"},
	} {
		q := "ALTER TABLE IF EXISTS ONLY public.t " + tc.sql
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, err)
		}
		s := statements[0].(*AlterTable)
		a := s.Actions[0]
		if !s.IfExists || !s.Only || a.Action != tc.kind || a.TriggerSelector != tc.selector || a.Trigger.Text != tc.name || a.Pos.Offset != strings.Index(q, tc.sql) {
			t.Fatal(s, a)
		}
		if tc.selector == TriggerNamed && a.Trigger.Pos.Offset <= a.Pos.Offset {
			t.Fatal("missing identifier provenance", a)
		}
	}
	statements, err := Parse("ALTER TABLE t DISABLE TRIGGER a, ENABLE ALWAYS TRIGGER b")
	if err != nil || len(statements[0].(*AlterTable).Actions) != 2 {
		t.Fatal(statements, err)
	}
	for _, q := range []string{
		"ALTER TABLE t ENABLE REPLICA TRIGGER ALL", "ALTER TABLE t ENABLE ALWAYS TRIGGER USER",
		"ALTER TABLE t DISABLE TRIGGER public.a", "ALTER TABLE t DISABLE TRIGGER a trailing",
		"ALTER TABLE t DISABLE REPLICA TRIGGER a", `ALTER TABLE t "enable" TRIGGER a`,
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted invalid syntax", q)
		}
	}
}
