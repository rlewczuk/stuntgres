package parser

import "testing"

func TestCollationUtilityGrammar(t *testing.T) {
	for _, tc := range []struct{ sql, kind string }{
		{`CREATE COLLATION IF NOT EXISTS s."Mixed" FROM pg_catalog."C"`, "create"},
		{`CREATE COLLATION s.c (PROVIDER=builtin,LOCALE='C.UTF8',DETERMINISTIC=true,VERSION='future!?')`, "options"},
		{`CREATE COLLATION c (FROM=pg_catalog."POSIX")`, "source_option"},
		{`ALTER COLLATION s.c REFRESH VERSION`, "refresh"},
		{`ALTER COLLATION s.c RENAME TO "Next"`, "rename"},
		{`ALTER COLLATION s.c SET SCHEMA next_schema`, "schema"},
		{`ALTER COLLATION pg_catalog."C" SET SCHEMA pg_temp`, "schema"},
		{`ALTER COLLATION pg_temp.c SET SCHEMA public`, "schema"},
		{`ALTER COLLATION refresh SET SCHEMA version`, "schema"},
		{`ALTER COLLATION s.c OWNER TO CURRENT_USER`, "owner"},
		{`DROP COLLATION IF EXISTS s.c,public.d CASCADE`, "drop"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			statements, err := Parse(tc.sql)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			switch s := statements[0].(type) {
			case *CreateCollation:
				if tc.kind == "create" && (!s.IfNotExists || s.Name.Parts[1].Text != "Mixed" || s.From == nil || s.From.Parts[1].Text != "C") {
					t.Fatal(s)
				}
				if tc.kind == "options" && (len(s.Options) != 4 || s.Options[1].Value != "C.UTF8" || s.Options[3].Value != "future!?") {
					t.Fatal(s)
				}
				if tc.kind == "source_option" && (len(s.Options) != 1 || s.Options[0].Name.Text != "from" || s.Options[0].Source == nil || s.Options[0].Source.Parts[1].Text != "POSIX") {
					t.Fatal(s)
				}
			case *AlterCollation:
				if s.Action != tc.kind {
					t.Fatal(s)
				}
			case *DropCollation:
				if !s.IfExists || len(s.Names) != 2 || s.Mode != DropCascade {
					t.Fatal(s)
				}
			default:
				t.Fatalf("unexpected utility %T", s)
			}
		})
	}
	statements, err := Parse(`CREATE COLLATION α ("Bad"='C',"Bad"='POSIX')`)
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateCollation)
	if len(create.Options) != 2 || create.Options[0].Name.Text != "Bad" || create.Options[0].Name.Pos.Character != 21 || create.Options[1].Name.Text != "Bad" {
		t.Fatal("option spelling/location lost", create)
	}
	for _, sql := range []string{`ALTER COLLATION c REFRESH other`, `ALTER COLLATION c unknown`, `CREATE COLLATION c (LOCALE=)`, `DROP COLLATION c; extra`} {
		if _, err := Parse(sql); err == nil {
			t.Fatal("incomplete/unsupported utility admitted", sql)
		}
	}
}
