package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func compositeReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func compositeReferenceSetup(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
	}
}

func compositeReferenceRows(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, query string, expected [][]any, prepared bool, args ...any) {
	t.Helper()
	s := referenceSelect(query, []string{"v"}, []string{""}, expected, args...)
	s.prepared = prepared
	s.checkMetadata = enumReferenceMetadata(b.pg, true)
	enumReferenceRunStep(t, p.ctx, c, b.recorder, s)
}

func compositeReferenceDiagnostic(code, message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func compositeReferenceLiteralPosition(q string) string {
	return strconv.Itoa(utf8.RuneCountInString(q[:strings.IndexByte(q, '\'')]) + 1)
}

// Complete records and failed-handle behavior are asserted independently on
// every route. Errorf keeps discovery running to expose all unsettled fields.
func compositeReferenceError(t *testing.T, p *referencePair, c *sql.Conn, q, preparedStage string, want referenceDiagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			t.Run("prepared="+strconv.FormatBool(prepared)+"/query="+strconv.FormatBool(queryMode), func(t *testing.T) {
				stage := "execute"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.ctx, q)
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
							rows, err = stmt.QueryContext(p.ctx, args...)
						} else {
							rows, err = c.QueryContext(p.ctx, q, args...)
						}
						if rows != nil {
							rows.Close()
							t.Error("expected failure exposed cursor")
						}
					} else {
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(p.ctx, args...)
						} else {
							result, err = c.ExecContext(p.ctx, q, args...)
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
				if got := referenceExtractDiagnostic(t, err); got != want {
					t.Errorf("query=%q diagnostic=%#v want=%#v", q, got, want)
				}
			})
		}
	}
}
