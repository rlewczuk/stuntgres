package control

import (
	"testing"

	"stuntgres/internal/parser"
)

// A field step in an INSERT target is not a subscript; it neither consumes
// the statement nor stops the remaining targets' subscript validation.
func TestRestoreAdmitsInsertTargetIndirection(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want Admission
		fail bool
	}{
		{sql: "INSERT INTO t (c.f, a[1]) VALUES (1, 2)", want: Admitted},
		{sql: "INSERT INTO t (c.f, a[$1]) VALUES (1, 2)", fail: true},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.sql, err)
		}
		answer, err := AdmitStatement[*hookRun](func(s *parser.Insert) error { return CheckInsert(s) })(new(hookRun), nil, statements[0])
		if tc.fail {
			if err == nil {
				t.Fatalf("%q admitted as %v, want an error", tc.sql, answer)
			}
			continue
		}
		if err != nil || answer != tc.want {
			t.Fatalf("%q = %v, %v; want %v", tc.sql, answer, err, tc.want)
		}
	}
}

func TestRestoreLanguageDDLAdmission(t *testing.T) {
	hooks := []Hook[struct{}]{{Name: "schema objects", Admit: AdmitSchemaObject[struct{}]}}
	for _, sql := range []string{
		"CREATE TRUSTED LANGUAGE replay_language HANDLER pg_catalog.plpgsql_call_handler",
		"CREATE OR REPLACE TRUSTED LANGUAGE replay_language HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator",
		"ALTER LANGUAGE sql RENAME TO replay_sql",
		"ALTER LANGUAGE replay_language OWNER TO CURRENT_USER",
		"DROP LANGUAGE replay_language CASCADE",
	} {
		statements, err := parser.Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
		answer, err := runAdmit(hooks, struct{}{}, nil, statements[0])
		if err != nil || answer != Admitted {
			t.Fatal("supported language lifecycle bypassed central restore admission", sql, answer, err)
		}
	}
	statements, err := parser.Parse("DO $$BEGIN END$$")
	if err != nil {
		t.Fatal(err)
	}
	if answer, err := runAdmit(hooks, struct{}{}, nil, statements[0]); err == nil || answer != NotHandled {
		t.Fatal("language admission authorized runtime execution", answer, err)
	}
}
