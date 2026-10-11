package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiteralPublicDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			if err := f.B.PingContext(p.Ctx); err != nil {
				t.Fatal(err)
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, f.A, f.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			run(fixture.Exec("CREATE TABLE failures(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO failures VALUES ('seed')", 1))
			sentinel := fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			for _, tc := range stringLiteralErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					position := tc.position
					if prefix != "" && position != "" {
						n, _ := strconv.Atoi(position)
						position = strconv.Itoa(n + 6)
					}
					for _, op := range []string{"exec", "query", "prepare"} {
						s := fixture.ImplicitError(prefix+"INSERT INTO t VALUES ("+tc.source, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
						s.SelectRows = op == "query"
						s.Prepared = op == "prepare"
						s.ErrorStage = "execute"
						if s.Prepared {
							s.ErrorStage = "prepare"
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
					s := fixture.ImplicitError("INSERT INTO t VALUES ('valid'),("+tc.source, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					run(sentinel)
					fixture.RunStep(t, p.Ctx, f.B, f.Recorder, sentinel)
					legacyPosition := tc.position
					if legacyPosition != "" {
						n, _ := strconv.Atoi(legacyPosition)
						legacyPosition = strconv.Itoa(n + 17)
					}
					legacy := fixture.ImplicitError("INSERT INTO failures VALUES ('valid'),("+tc.source, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: legacyPosition, Hint: tc.hint})
					legacy.Prepared = prepared
					legacy.ErrorStage = "execute"
					if prepared {
						legacy.ErrorStage = "prepare"
					}
					run(legacy)
					run(fixture.Select("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
				}
			}
		})
	}
}

// Raw SQL bytes cross different native preflight and pq protocol boundaries.
// PostgreSQL records below were captured independently on pinned PostgreSQL18.0.
func TestReferenceStringLiteralNativeSQLBytes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			fixture.RunStep(t, p.Ctx, f.A, f.Recorder, fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			fixture.RunStep(t, p.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			sentinel := fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}})
			for _, tc := range []struct{ raw, message string }{
				{"\xff", "invalid UTF-8 in SQL"},
				{"\x00", "NUL in SQL"},
			} {
				for _, prepared := range []bool{false, true} {
					query := "INSERT INTO t VALUES (E'" + tc.raw + "')"
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = "25"
					if backend.PG {
						want = fixture.ControlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`)
						if tc.raw == "\x00" {
							want = fixture.ControlsReferenceDiagnostic("08P01", "invalid message format")
							if prepared {
								want.Message = "insufficient data left in message"
							}
						}
					}
					var err error
					if prepared {
						stmt, failure := f.A.PrepareContext(p.Ctx, query)
						err = failure
						if stmt != nil {
							stmt.Close()
							t.Fatal("raw SQL byte failure exposed a statement")
						}
					} else {
						result, failure := f.A.ExecContext(p.Ctx, query)
						err = failure
						if result != nil {
							t.Fatal("raw SQL byte failure exposed a result")
						}
					}
					fixture.AssertDiagnostic(t, err, &want)
					if err := f.A.PingContext(p.Ctx); err != nil {
						t.Fatalf("same owner unhealthy after raw SQL bytes: %v", err)
					}
					fixture.RunStep(t, p.Ctx, f.A, f.Recorder, sentinel)
					fixture.RunStep(t, p.Ctx, f.B, f.Recorder, sentinel)
				}
			}
		})
	}
}
