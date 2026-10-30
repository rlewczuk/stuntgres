package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesSourceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			peer := db
			if !backend.PG {
				peer = p.Native
			}
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(x TEXT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			run(fixture.Exec("CREATE TABLE source_rows(x TEXT)", 0))
			run(fixture.Exec("INSERT INTO source_rows VALUES ('source')", 1))
			check := func(q string, diag fixture.Diagnostic, postgres *fixture.Diagnostic) {
				t.Helper()
				if backend.PG && postgres != nil {
					diag = *postgres
				}
				for _, op := range []string{"exec", "query", "prepare"} {
					s := fixture.ErrorDiagnostic(q, diag)
					s.SelectRows, s.Prepared, s.ErrorStage = op == "query", op == "prepare", "execute"
					if s.Prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					for _, pool := range []*sql.DB{db, peer} {
						for _, sentinel := range []fixture.Step{
							fixture.Select("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}),
							fixture.Select("SELECT * FROM source_rows", []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}),
						} {
							sentinel.NonContext = true
							fixture.RunStep(t, p.Ctx, pool, backend.Recorder, sentinel)
						}
					}
				}
			}
			for _, tc := range unicodeEscapeErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					n, _ := strconv.Atoi(tc.position)
					if prefix != "" {
						n += 6
					}
					check(prefix+"INSERT INTO t VALUES ("+tc.source, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.source))
				}
				n, _ := strconv.Atoi(tc.position)
				check("INSERT INTO t VALUES ('valid'),("+tc.source, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n + 10), Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.source))
			}
			for _, tc := range []struct{ q, message, position, hint string }{
				{`CREATE TABLE U&"a""\061"(x TEXT)`, "invalid Unicode escape", "19", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`SELECT * FROM U&"a""😀\061"`, "invalid Unicode escape", "22", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`CREATE TABLE U&""(x TEXT)`, `zero-length delimited identifier at or near "U&"""`, "14", ""},
				{`CREATE TABLE U&"abc`, `unterminated quoted identifier at or near "U&"abc"`, "14", ""},
				{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, "invalid Unicode escape", "58", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'a''😀\061')`, "invalid Unicode escape", "62", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`, "invalid Unicode escape", "67", `Unicode escapes must be \XXXX or \+XXXXXX.`},
			} {
				check(tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.q))
			}
			missing := fixture.ImplicitError("SELECT * FROM fresh", fixture.Diagnostic{Code: "42P01", Message: `relation "fresh" does not exist`, Position: "15"})
			missing.SelectRows, missing.ErrorStage, missing.NonContext = true, "execute", true
			fixture.RunStep(t, p.Ctx, peer, backend.Recorder, missing)
		})
	}
}
