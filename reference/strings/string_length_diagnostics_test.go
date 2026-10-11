package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringLengthFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
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
	}
}

func TestReferenceStringLengthDiagnostics(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, signature string }{
			{"char_length(1)", "char_length(integer)"},
			{"character_length(NULL::integer)", "character_length(integer)"},
			{"length(true)", "length(boolean)"}, {"octet_length(1)", "octet_length(integer)"},
			{"bit_length(1)", "bit_length(integer)"},
			{"strpos('abc',1)", "strpos(unknown, integer)"},
			{"position(1 IN 'abc')", "pg_catalog.position(unknown, integer)"},
			{"char_length('abc'::bytea)", "char_length(bytea)"},
			{"strpos('abc'::bytea,'a'::bytea)", "strpos(bytea, bytea)"},
			{"char_length()", "char_length()"},
			{"strpos('abc')", "strpos(unknown)"},
			{"length('a','b','c')", "length(unknown, unknown, unknown)"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + len([]rune(prefix)))
					stringLengthFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, name := range []string{"bit_length", "char_length", "character_length", "length", "octet_length", "strpos", "pg_catalog.position"} {
			t.Run(name+"/argument-limit", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
				d.Position = "8"
				stringLengthFailure(t, p, b, c, "SELECT "+name+"("+strings.Repeat("'a',", 100)+"'a')", d, true)
			})
		}
		d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ","`)
		d.Position = "22"
		stringLengthFailure(t, p, b, c, "SELECT position('abc','b')", d, true)
		// Matching scalar VARIADIC spelling does not require an array overload.
		for _, tc := range []struct {
			call string
			want int64
		}{
			{"char_length(VARIADIC 'abc'::text)", 3},
			{"strpos('abc',VARIADIC 'b'::text)", 2},
		} {
			t.Run(tc.call, func(t *testing.T) {
				stringLengthBoth(t, p, b, c, fixture.Select("SELECT "+tc.call+" AS v", []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}))
			})
		}
	})
}

func TestReferenceStringLengthCollationInputs(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE sl_coll(id int,a text COLLATE "C",b text COLLATE "POSIX")`, 0),
			fixture.Exec("INSERT INTO sl_coll VALUES(1,'abc','b'),(2,'abc',''),(3,NULL,''),(4,'','')", 4),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, call := range []string{"strpos(a,b)", "position(b IN a)", "strpos(btrim(a||b),'')", "strpos(lower(a||b),'')"} {
			for _, id := range []string{"1", "2", "4"} {
				t.Run(call+"/"+id, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for string comparison")
					if strings.Contains(call, "lower") {
						d.Message = "could not determine which collation to use for lower() function"
					}
					d.Hint = "Use the COLLATE clause to set the collation explicitly."
					stringLengthFailure(t, p, b, c, "SELECT "+call+" FROM sl_coll WHERE id="+id, d, false)
				})
			}
			stringLengthBoth(t, p, b, c, fixture.Select("SELECT "+call+" AS v FROM sl_coll WHERE id=3", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		}
		stringLengthBoth(t, p, b, c, fixture.Select(`SELECT strpos(a COLLATE "C",b) AS v,position(b IN (a COLLATE "C")) AS p,char_length(a||b) AS n,octet_length(a||b) AS bytes FROM sl_coll WHERE id=1`, []string{"v", "p", "n", "bytes"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(4), int64(4)}}))
		for _, q := range []string{`SELECT strpos('a' COLLATE "C",'b' COLLATE "POSIX")`, `SELECT strpos(NULL::text COLLATE "C",'b' COLLATE "POSIX")`} {
			d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
			d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
			stringLengthFailure(t, p, b, c, q, d, true)
		}
		stringLengthBoth(t, p, b, c, fixture.Select(`SELECT strpos((a||b) COLLATE "C",'b') AS v FROM sl_coll WHERE id=1`, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}

func TestReferenceStringLengthAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sl_reader"})
	stringLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sl_auth(v text)", 0), fixture.Exec("INSERT INTO sl_auth VALUES('é🙂')", 1),
			fixture.Exec("CREATE ROLE sl_reader", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// Native public already grants USAGE; the PG fixture resets that ACL.
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO sl_reader", 0))
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, empty := range []bool{false, true} {
			if empty {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sl_auth", 1))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE sl_reader", 0))
			for _, q := range []string{"SELECT char_length(v),strpos(v,'🙂') FROM sl_auth", "SELECT char_length(NULL::text),strpos(NULL::text,'') FROM sl_auth"} {
				stringLengthFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sl_auth"), false)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			want := [][]any{{"é🙂"}}
			if empty {
				want = nil
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM sl_auth", []string{"v"}, []string{"TEXT"}, want))
		}
	})
}
