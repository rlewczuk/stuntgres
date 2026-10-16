package parser

import "testing"

func TestAlterTablespaceGrammar(t *testing.T) {
	for _, target := range []string{`t`, `t *`, `ONLY t`, `ONLY (t)`, `IF EXISTS t`} {
		statements, err := Parse(`ALTER TABLE ` + target + ` SET TABLESPACE "Space雪"`)
		if err != nil {
			t.Fatal(target, err)
		}
		s := statements[0].(*AlterTable)
		a := s.Actions[0]
		if len(s.Actions) != 1 || a.Action != SetTablespace || a.Tablespace.Text != "Space雪" || a.Tablespace.Pos.Character == 0 || a.Pos.Character == 0 {
			t.Fatalf("lost positioned action: %#v", s)
		}
	}
	statements, err := Parse(`ALTER TABLE t SET TABLESPACE pg_default, ADD COLUMN a INT`)
	if err != nil || len(statements[0].(*AlterTable).Actions) != 2 {
		t.Fatal(statements, err)
	}
	statements, err = Parse(`ALTER TABLE ALL IN TABLESPACE "Source" OWNED BY CURRENT_USER, CURRENT_ROLE, SESSION_USER, "current_user", bob SET TABLESPACE pg_default NOWAIT`)
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*AlterTableAllInTablespace)
	if s.Source.Text != "Source" || s.Destination.Text != "pg_default" || !s.Nowait || len(s.Owners) != 5 || s.Owners[0].Identity != "current" || s.Owners[1].Identity != "current" || s.Owners[2].Identity != "session" || s.Owners[3].Identity != "" || s.Owners[3].Name != "current_user" {
		t.Fatalf("lost bulk role identities: %#v", s)
	}
	for _, q := range []string{
		`ALTER TABLE t SET TABLESPACE pg_default NOWAIT`,
		`ALTER TABLE t SET TABLESPACE public.pg_default`,
		`ALTER TABLE t SET TABLESPACE 'pg_default'`,
		`ALTER TABLE t SET TABLESPACE $1`,
		`ALTER TABLE IF EXISTS ALL IN TABLESPACE pg_default SET TABLESPACE pg_default`,
		`ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default, ADD a INT`,
		`ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY bob, SET TABLESPACE pg_default`,
		`ALTER TABLE t SET TABLESPACE pg_default; SELECT FROM`,
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted malformed tablespace utility", q)
		}
	}
}
