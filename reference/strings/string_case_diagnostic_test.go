package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringCaseFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%v", q, prepared), func(t *testing.T) {
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
				if analysis {
					s.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func TestReferenceStringCaseDiagnostics(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringCaseSetup(t, p, b, c)
		for _, tc := range []struct{ call, signature string }{
			{"lower(1)", "lower(integer)"}, {"upper(true)", "upper(boolean)"}, {"casefold(b)", "casefold(bytea)"},
			{"initcap('a','b')", "initcap(unknown, unknown)"}, {"upper()", "upper()"},
			{"lower(NULL::integer)", "lower(integer)"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "15"
			stringCaseFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.call+" FROM sc", d, true)
		}
		d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
		d.Position = "15"
		stringCaseFailure(t, p, b, c, "/*é🙂*/ SELECT upper("+strings.Repeat("'a',", 100)+"'a') FROM sc", d, true)
		d = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
		d.Position = "33"
		stringCaseFailure(t, p, b, c, "/*é🙂*/ SELECT lower(t) FROM sc; garbage", d, true)
		// Whole-input failure must not publish the earlier INSERT.
		d = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
		d.Position = "39"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO sc(id,t) VALUES(9,'AbC'); garbage", d)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM sc WHERE id=9", []string{"id"}, []string{"INT4"}, nil))
	})
}

func TestReferenceStringCaseCollation(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE sc_conflict(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0),
			fixture.Exec("INSERT INTO sc_conflict VALUES(1,'A','B'),(2,NULL,'B')", 2),
			fixture.Exec("CREATE TABLE sc_inherited(id INT,s TEXT COLLATE pg_c_utf8,f TEXT COLLATE pg_unicode_fast)", 0),
			fixture.Exec("INSERT INTO sc_inherited VALUES(1,'Straße','Straße')", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, op := range []string{"lower", "upper", "casefold", "initcap"} {
			name := op
			if op == "casefold" {
				name = "lower"
			} // PostgreSQL18.0 spelling, not newer source.
			d := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for "+name+"() function")
			d.Hint = "Use the COLLATE clause to set the collation explicitly."
			stringCaseFailure(t, p, b, c, "SELECT "+op+"(a||b) FROM sc_conflict WHERE id=1", d, false)
			stringCaseBoth(t, p, b, c, fixture.Select("SELECT "+op+"(a||b) AS v FROM sc_conflict WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		}
		stringCaseBoth(t, p, b, c, fixture.Select(`SELECT casefold((a||b) COLLATE "C") AS v FROM sc_conflict WHERE id=1`, []string{"v"}, []string{"TEXT"}, [][]any{{"ab"}}))
		d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
		d.Position = "37"
		stringCaseFailure(t, p, b, c, `SELECT lower('A' COLLATE "C" || 'B' COLLATE "POSIX") FROM sc_conflict`, d, true)
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT upper(s) AS s,upper(f) AS f,upper(lower(f)) AS nested FROM sc_inherited", []string{"s", "f", "nested"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"STRAßE", "STRASSE", "STRASSE"}}))
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT 'É' COLLATE pg_unicode_fast LIKE 'É' AS yes,'É' COLLATE pg_unicode_fast LIKE 'é' AS no FROM sc_inherited", []string{"yes", "no"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		for _, q := range []string{"SELECT 'É' COLLATE pg_unicode_fast ILIKE 'é' AS v FROM sc_inherited", "SELECT 'É' ILIKE 'é' COLLATE pg_c_utf8 AS v FROM sc_inherited"} {
			if b.PG {
				stringCaseBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			} else {
				d := fixture.ControlsReferenceDiagnostic("0A000", "ILIKE is unsupported for this collation")
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "ILIKE")])) + 1)
				stringCaseFailure(t, p, b, c, q, d, true)
			}
		}
		for _, q := range []string{`SELECT f ILIKE 'straße' COLLATE "C" AS v FROM sc_inherited`, `SELECT f COLLATE "C" ILIKE 'straße' AS v FROM sc_inherited`} {
			stringCaseBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		}
	})
}

func TestReferenceStringCaseNamespaceAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sc_reader"})
	stringCaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
		defer c.ExecContext(p.Ctx, "SET search_path=public")
		stringCaseSetup(t, p, b, c)
		for _, q := range []string{"CREATE SCHEMA sc_empty", "CREATE SCHEMA sc_private", "CREATE TABLE sc_private.t(v TEXT)", "INSERT INTO sc_private.t VALUES('AbC')", "CREATE ROLE sc_reader"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 1
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, count))
			if strings.HasPrefix(q, "CREATE SCHEMA ") {
				name := strings.TrimPrefix(q, "CREATE SCHEMA ")
				t.Cleanup(func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
						t.Error(err)
					}
				})
			}
		}
		d := fixture.ControlsReferenceDiagnostic("3F000", `schema "sc_missing" does not exist`)
		d.Position = "8"
		stringCaseFailure(t, p, b, c, "SELECT sc_missing.lower(t) FROM sc", d, true)
		d = fixture.ControlsReferenceDiagnostic("42883", "function sc_empty.lower(text) does not exist")
		d.Position = "8"
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		stringCaseFailure(t, p, b, c, "SELECT sc_empty.lower(t) FROM sc", d, true)
		if b.PG {
			// The oracle fixture recreates public without its default PUBLIC USAGE.
			// Restore schema visibility only; table reads must still fail42501.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO sc_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
		q := "SELECT lower(t) AS v FROM sc WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE sc_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		d = fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema sc_private")
		d.Position = "22"
		stringCaseFailure(t, p, b, c, "SELECT lower(v) FROM sc_private.t", d, true)
		d = fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sc")
		stringCaseFailure(t, p, b, c, q, d, false)
		s := fixture.ErrorDiagnostic(q, d)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"straße"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=sc_empty", 0))
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT pg_catalog.upper('ß' COLLATE pg_catalog.pg_unicode_fast) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"SS"}}))
		stringCaseBoth(t, p, b, c, fixture.Select("SELECT lower('ABC') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=sc_empty,pg_catalog", 0))
		stringCaseBoth(t, p, b, c, fixture.Select(fmt.Sprintf("SELECT %s('ABC') AS v", "lower"), []string{"v"}, []string{"TEXT"}, [][]any{{"abc"}}))
	})
}
