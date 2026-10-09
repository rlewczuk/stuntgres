package reference_test

import "testing"

func TestReferenceStringLiteralSourceSQLBytes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			peer := db
			if !backend.pg {
				peer = p.native
			}
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			sentinel := referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			sentinel.nonContext = true
			for _, tc := range []struct{ raw, message string }{
				{"\xff", "invalid UTF-8 in SQL"},
				{"\x00", "NUL in SQL"},
			} {
				for _, prepared := range []bool{false, true} {
					query := "INSERT INTO t VALUES (E'" + tc.raw + "')"
					want := controlsReferenceDiagnostic("42601", tc.message)
					want.Position = "25"
					if backend.pg {
						want = controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`)
						if tc.raw == "\x00" {
							want = controlsReferenceDiagnostic("08P01", "invalid message format")
							if prepared {
								want.Message = "insufficient data left in message"
							}
						}
					}
					var err error
					if prepared {
						stmt, failure := db.Prepare(query)
						err = failure
						if stmt != nil {
							stmt.Close()
							t.Fatal("raw SQL byte failure exposed a statement")
						}
					} else {
						result, failure := db.Exec(query)
						err = failure
						if result != nil {
							t.Fatal("raw SQL byte failure exposed a result")
						}
					}
					referenceAssertDiagnostic(t, err, &want)
					if err := db.Ping(); err != nil {
						t.Fatalf("same owner unhealthy after raw SQL bytes: %v", err)
					}
					referenceRunStep(t, p.ctx, db, backend.recorder, sentinel)
					referenceRunStep(t, p.ctx, peer, backend.recorder, sentinel)
				}
			}
		})
	}
}

func TestReferenceStringLiteralSourceDollarDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			for _, tc := range []struct{ q, code, message, position, postgresMessage string }{
				{"INSERT INTO t VALUES$$v$$", "42601", `syntax error at or near "VALUES$$v$$"`, "15", ""},
				{"INSERT INTO t VALUES ($0)", "42P02", "there is no parameter $0", "23", ""},
				{"INSERT INTO t VALUES ($000)", "42P02", "there is no parameter $0", "23", ""},
				{"INSERT INTO t VALUES ($1junk)", "42601", "invalid parameter", "23", `trailing junk after parameter at or near "$1junk"`},
				{"INSERT INTO t VALUES ($9tag$)", "42601", "invalid parameter", "23", `trailing junk after parameter at or near "$9tag$"`},
				{"INSERT INTO t VALUES ($2147483648)", "42601", "parameter number too large", "23", `parameter number too large at or near "$2147483648"`},
				{"INSERT INTO t VALUES ('valid'),($Tag$a$tag$)", "42601", `unterminated dollar-quoted string at or near "$Tag$a$tag$)"`, "33", ""},
				{"CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES ($$a)", "42601", `unterminated dollar-quoted string at or near "$$a)"`, "55", ""},
			} {
				// These three PostgreSQL messages are independently observed
				// full records in tests-1.md. Keep native diagnostics unchanged;
				// message differences are not parity-normalized.
				message := tc.message
				if backend.pg && tc.postgresMessage != "" {
					message = tc.postgresMessage
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: message, Position: tc.position})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
				}
			}
			for _, name := range []string{"fresh", "phantom"} {
				s := referenceError("SELECT * FROM "+name, "42P01")
				s.selectRows = true
				run(s)
			}
		})
	}
}
