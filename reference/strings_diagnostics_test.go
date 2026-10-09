package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceStringLiteralPublicDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			if err := f.b.PingContext(p.ctx); err != nil {
				t.Fatal(err)
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, f.a, f.recorder, s) }
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			run(referenceExec("CREATE TABLE failures(label TEXT)", 0))
			run(referenceExec("INSERT INTO failures VALUES ('seed')", 1))
			sentinel := referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			for _, tc := range stringLiteralErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					position := tc.position
					if prefix != "" && position != "" {
						n, _ := strconv.Atoi(position)
						position = strconv.Itoa(n + 6)
					}
					for _, op := range []string{"exec", "query", "prepare"} {
						s := referenceImplicitError(prefix+"INSERT INTO t VALUES ("+tc.source, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
						s.selectRows = op == "query"
						s.prepared = op == "prepare"
						s.errorStage = "execute"
						if s.prepared {
							s.errorStage = "prepare"
						}
						run(s)
					}
				}
				position := tc.position
				if position != "" {
					n, _ := strconv.Atoi(position)
					position = strconv.Itoa(n + 10)
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO t VALUES ('valid'),("+tc.source, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(sentinel)
					referenceRunStep(t, p.ctx, f.b, f.recorder, sentinel)
					legacyPosition := tc.position
					if legacyPosition != "" {
						n, _ := strconv.Atoi(legacyPosition)
						legacyPosition = strconv.Itoa(n + 17)
					}
					legacy := referenceImplicitError("INSERT INTO failures VALUES ('valid'),("+tc.source, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: legacyPosition, Hint: tc.hint})
					legacy.prepared = prepared
					legacy.errorStage = "execute"
					if prepared {
						legacy.errorStage = "prepare"
					}
					run(legacy)
					run(referenceSelect("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
				}
			}
		})
	}
}

// Raw SQL bytes cross different native preflight and pq protocol boundaries.
// PostgreSQL records below were captured independently on pinned PostgreSQL18.0.
func TestReferenceStringLiteralNativeSQLBytes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			referenceRunStep(t, p.ctx, f.a, f.recorder, referenceExec("CREATE TABLE t(label TEXT)", 0))
			referenceRunStep(t, p.ctx, f.a, f.recorder, referenceExec("INSERT INTO t VALUES ('seed')", 1))
			sentinel := referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
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
						stmt, failure := f.a.PrepareContext(p.ctx, query)
						err = failure
						if stmt != nil {
							stmt.Close()
							t.Fatal("raw SQL byte failure exposed a statement")
						}
					} else {
						result, failure := f.a.ExecContext(p.ctx, query)
						err = failure
						if result != nil {
							t.Fatal("raw SQL byte failure exposed a result")
						}
					}
					referenceAssertDiagnostic(t, err, &want)
					if err := f.a.PingContext(p.ctx); err != nil {
						t.Fatalf("same owner unhealthy after raw SQL bytes: %v", err)
					}
					referenceRunStep(t, p.ctx, f.a, f.recorder, sentinel)
					referenceRunStep(t, p.ctx, f.b, f.recorder, sentinel)
				}
			}
		})
	}
}
