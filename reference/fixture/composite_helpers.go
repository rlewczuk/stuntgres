package fixture

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func CompositeReferenceScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func CompositeReferenceSetup(t *testing.T, p *Pair, b Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		RunStep(t, p.Ctx, c, b.Recorder, Exec(q, 0))
	}
}

func CompositeReferenceRows(t *testing.T, p *Pair, b Backend, c Executor, query string, expected [][]any, prepared bool, args ...any) {
	t.Helper()
	s := Select(query, []string{"v"}, []string{""}, expected, args...)
	s.Prepared = prepared
	s.CheckMetadata = EnumReferenceMetadata(b.PG, true)
	EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
}

func CompositeReferenceDiagnostic(code, message string) Diagnostic {
	return Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func CompositeReferenceLiteralPosition(q string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.IndexByte(q, '\'')]) + 1)
}

// Complete records and failed-handle behavior are asserted independently on
// every route. Errorf keeps discovery running to expose all unsettled fields.
func CompositeReferenceError(t *testing.T, p *Pair, c *sql.Conn, q, preparedStage string, want Diagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			t.Run("prepared="+strconv.FormatBool(prepared)+"/query="+strconv.FormatBool(queryMode), func(t *testing.T) {
				stage := "execute"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						stage = "prepare"
						if stmt != nil {
							stmt.Close()
							t.Error("failed Prepare exposed statement")
						}
					} else {
						defer stmt.Close()
					}
				}
				if err == nil {
					if queryMode {
						var rows *sql.Rows
						if prepared {
							rows, err = stmt.QueryContext(p.Ctx, args...)
						} else {
							rows, err = c.QueryContext(p.Ctx, q, args...)
						}
						if rows != nil {
							rows.Close()
							t.Error("expected failure exposed cursor")
						}
					} else {
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(p.Ctx, args...)
						} else {
							result, err = c.ExecContext(p.Ctx, q, args...)
						}
						if result != nil {
							t.Error("expected failure exposed result")
						}
					}
				}
				if err == nil {
					t.Fatal("expected SQL error", q)
				}
				wantStage := "execute"
				if prepared {
					wantStage = preparedStage
				}
				if stage != wantStage {
					t.Errorf("query=%q stage=%q want=%q", q, stage, wantStage)
				}
				if got := ExtractDiagnostic(t, err); got != want {
					t.Errorf("query=%q diagnostic=%#v want=%#v", q, got, want)
				}
			})
		}
	}
}
