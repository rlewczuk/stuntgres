package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
)

func TestReferenceUnicodeEscapesSourceDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			peer := db
			if !backend.pg {
				peer = p.native
			}
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(x TEXT NOT NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			run(referenceExec("CREATE TABLE source_rows(x TEXT)", 0))
			run(referenceExec("INSERT INTO source_rows VALUES ('source')", 1))
			check := func(q string, diag referenceDiagnostic) {
				t.Helper()
				for _, op := range []string{"exec", "query", "prepare"} {
					s := referenceErrorDiagnostic(q, diag)
					s.selectRows, s.prepared, s.errorStage = op == "query", op == "prepare", "execute"
					if s.prepared {
						s.errorStage = "prepare"
					}
					run(s)
					for _, pool := range []*sql.DB{db, peer} {
						for _, sentinel := range []referenceStep{
							referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}),
							referenceSelect("SELECT * FROM source_rows", []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}),
						} {
							sentinel.nonContext = true
							referenceRunStep(t, p.ctx, pool, backend.recorder, sentinel)
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
					check(prefix+"INSERT INTO t VALUES ("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint})
				}
				n, _ := strconv.Atoi(tc.position)
				check("INSERT INTO t VALUES ('valid'),("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n + 10), Hint: tc.hint})
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
				check(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint})
			}
			missing := referenceImplicitError("SELECT * FROM fresh", referenceDiagnostic{Code: "42P01", Message: `relation "fresh" does not exist`, Position: "15"})
			missing.selectRows, missing.errorStage, missing.nonContext = true, "execute", true
			referenceRunStep(t, p.ctx, peer, backend.recorder, missing)
		})
	}
}
