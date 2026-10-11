package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringReplaceFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool) {
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

func TestReferenceStringReplaceDiagnostics(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, signature string }{
			{"replace(1,'a','b')", "replace(integer, unknown, unknown)"},
			{"replace(NULL,1,'X')", "replace(unknown, integer, unknown)"},
			{"overlay(NULL,'X',1::bigint)", "overlay(unknown, unknown, bigint)"},
			{"overlay('abc','X','2'::text)", "overlay(unknown, unknown, text)"},
			{"overlay('abc','X',2::numeric)", "overlay(unknown, unknown, numeric)"},
			{"overlay('abc','X',2::double precision)", "overlay(unknown, unknown, double precision)"},
			{"overlay('abc'::text,'X'::bytea,2)", "overlay(text, bytea, integer)"},
			{"overlay()", "overlay()"}, {"overlay('a')", "overlay(unknown)"},
			{"overlay('a','b')", "overlay(unknown, unknown)"},
			{"overlay('a','b',1,2,3)", "overlay(unknown, unknown, integer, integer, integer)"},
			{"replace()", "replace()"}, {"replace('a')", "replace(unknown)"}, {"replace('a','b')", "replace(unknown, unknown)"},
			{"replace('a','b','c','d')", "replace(unknown, unknown, unknown, unknown)"},
			{"translate()", "translate()"}, {"translate('a')", "translate(unknown)"}, {"translate('a','b')", "translate(unknown, unknown)"},
			{"translate('a','b','c','d')", "translate(unknown, unknown, unknown, unknown)"},
			{"starts_with()", "starts_with()"}, {"starts_with('a')", "starts_with(unknown)"},
			{"starts_with('a','b','c')", "starts_with(unknown, unknown, unknown)"},
			{"\"REPLACE\"('a','b','c')", "REPLACE(unknown, unknown, unknown)"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + len([]rune(prefix)))
					stringReplaceFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, family := range []struct {
			name  string
			arity int
		}{{"replace", 3}, {"translate", 3}, {"starts_with", 2}} {
			for slot := range family.arity {
				for _, wrong := range []struct{ sql, typ string }{{"1", "integer"}, {"true", "boolean"}, {"'a'::bytea", "bytea"}} {
					args, types := make([]string, family.arity), make([]string, family.arity)
					for i := range args {
						args[i] = "'a'"
						types[i] = "unknown"
					}
					args[slot] = wrong.sql
					types[slot] = wrong.typ
					call := family.name + "(" + strings.Join(args, ",") + ")"
					t.Run(call, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("42883", "function "+family.name+"("+strings.Join(types, ", ")+") does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						d.Position = "8"
						stringReplaceFailure(t, p, b, c, "SELECT "+call, d, true)
					})
				}
			}
		}
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			for _, tc := range []struct {
				expr, signature string
				position        int
			}{
				{"1 ^@ 'a'", "integer ^@ unknown", 10}, {"NULL::integer ^@ 'a'", "integer ^@ unknown", 22},
				{"1 OPERATOR(pg_catalog.^@) 'a'", "integer pg_catalog.^@ unknown", 10},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
					d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(tc.position + len([]rune(prefix)))
					stringReplaceFailure(t, p, b, c, prefix+"SELECT "+tc.expr, d, true)
				})
			}
		}
		for _, tc := range []struct {
			expr, code, message string
			analysis            bool
			position            string
		}{
			{"overlay('abc','X',0)", "22011", "negative substring length not allowed", false, ""},
			{"overlay('abc','X',-1)", "22011", "negative substring length not allowed", false, ""},
			{"overlay('abc','X',0,-2147483648)", "22011", "negative substring length not allowed", false, ""},
			{"overlay('abc','X',2147483647,1)", "22003", "integer out of range", false, ""},
			{"overlay('abc','X','bad')", "22P02", `invalid input syntax for type integer: "bad"`, true, "26"},
			{"overlay('abc','X','2147483648')", "22003", `value "2147483648" is out of range for type integer`, true, "26"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = tc.position
					if tc.analysis {
						d.Position = fixture.Position(26 + len([]rune(prefix)))
					}
					stringReplaceFailure(t, p, b, c, prefix+"SELECT "+tc.expr, d, tc.analysis)
				})
			}
		}
		t.Run("parameter-conversion", func(t *testing.T) {
			q := "SELECT overlay($1,$2,$3,$4) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			d.Context = "unnamed portal parameter $3 = '...'"
			s := fixture.ErrorDiagnostic(q, d, "abc", "X", "bad", 1)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		})
	})
}

func TestReferenceStringReplaceCollation(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE sr_coll(id int,a text COLLATE "C",b text COLLATE "POSIX")`, 0),
			fixture.Exec("INSERT INTO sr_coll VALUES(1,'abc','a'),(2,'',''),(3,NULL,'a'),(4,'abc','')", 4),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		d := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for string comparison")
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		for _, expr := range []string{"starts_with(a,b)", "a ^@ b", "starts_with((a||b),'a')"} {
			for _, id := range []string{"1", "2", "4"} {
				t.Run(expr+"/"+id, func(t *testing.T) {
					stringReplaceFailure(t, p, b, c, "SELECT "+expr+" FROM sr_coll WHERE id="+id, d, false)
				})
			}
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v FROM sr_coll WHERE id=3", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
		}
		t.Run("replace-conflict", func(t *testing.T) {
			stringReplaceFailure(t, p, b, c, "SELECT replace(a,b,'X') FROM sr_coll WHERE id=1", d, false)
		})
		for _, tc := range []struct {
			id   string
			want any
		}{{"2", ""}, {"3", nil}, {"4", "abc"}} {
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT replace(a,b,'X') AS v FROM sr_coll WHERE id="+tc.id, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct {
			expr, typ string
			rows      [][]any
		}{
			{"translate(a,b,'X')", "TEXT", [][]any{{"Xbc"}, {""}, {nil}, {"abc"}}},
			{"overlay(a,b,1,1)", "TEXT", [][]any{{"abc"}, {""}, {nil}, {"bc"}}},
			{`starts_with(a COLLATE "C",b)`, "BOOL", [][]any{{true}, {true}, {nil}, {true}}},
			{`starts_with((a||b) COLLATE "C",'a')`, "BOOL", [][]any{{true}, {false}, {nil}, {true}}},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				stringReplaceBoth(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS v FROM sr_coll ORDER BY id", []string{"v"}, []string{tc.typ}, tc.rows))
			})
		}
		for _, expr := range []string{`starts_with(NULL::text COLLATE "C",'a' COLLATE "POSIX")`, `replace('' COLLATE "C",'' COLLATE "POSIX",'X')`, `translate(NULL::text COLLATE "C",'a' COLLATE "POSIX",'X')`, `overlay(NULL::text COLLATE "C",'a' COLLATE "POSIX",1)`} {
			t.Run(expr, func(t *testing.T) {
				q := "SELECT " + expr
				d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
				d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
				stringReplaceFailure(t, p, b, c, q, d, true)
			})
		}
	})
}
