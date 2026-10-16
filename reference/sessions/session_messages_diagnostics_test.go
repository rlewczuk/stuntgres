package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesDiagnostics(t *testing.T) {
	t.Run("payload-bytes-and-preparation", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN bytes")
			for _, payload := range []string{strings.Repeat("a", 7999), strings.Repeat("é", 3999) + "a"} {
				sessionMessageSQL(t, p, b, s, "NOTIFY bytes, '"+payload+"'")
				sessionMessageReceive(t, p, b, r, "bytes", payload)
			}
			want := fixture.ControlsReferenceDiagnostic("22023", "payload string too long")
			for _, payload := range []string{strings.Repeat("a", 8000), strings.Repeat("é", 4000)} {
				q := "NOTIFY bytes, '" + payload + "'"
				fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.ErrorDiagnostic(q, want))
				stmt, err := s.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("oversized literal must prepare without execution:", err)
				}
				held := fixture.HeldStatement{Executor: s, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				sessionMessageHealth(t, p, b, s)
			}
			sessionMessageSQL(t, p, b, s, "BEGIN")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.ErrorDiagnostic("NOTIFY bytes, '"+strings.Repeat("a", 8000)+"'", want))
			aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.ErrorDiagnostic("NOTIFY bytes, 'aborted'", aborted))
			sessionMessageSQL(t, p, b, s, "ROLLBACK")
			sessionMessageSQL(t, p, b, s, "NOTIFY bytes, 'healthy'")
			sessionMessageReceive(t, p, b, r, "bytes", "healthy")
		})
	})

	t.Run("syntax-and-empty-identifiers", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for _, c := range []struct{ q, message, position string }{
				{"LISTEN \"\"", "zero-length delimited identifier at or near \"\"\"\"", "8"},
				{"NOTIFY \"\"", "zero-length delimited identifier at or near \"\"\"\"", "8"},
				{"UNLISTEN \"\"", "zero-length delimited identifier at or near \"\"\"\"", "10"},
				{"NOTIFY bytes, $1", "syntax error at or near \"$1\"", "15"},
				{"NOTIFY bytes, 1", "syntax error at or near \"1\"", "15"},
				{"PREPARE p AS LISTEN bytes", "syntax error at or near \"LISTEN\"", "14"},
				{"PREPARE p AS NOTIFY bytes", "syntax error at or near \"NOTIFY\"", "14"},
				{"PREPARE p AS UNLISTEN bytes", "syntax error at or near \"UNLISTEN\"", "14"},
			} {
				want := fixture.ControlsReferenceDiagnostic("42601", c.message)
				want.Position = c.position
				fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.ErrorDiagnostic(c.q, want))
				stmt, err := s.PrepareContext(p.Ctx, c.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("malformed Prepare exposed statement", c.q)
				}
				fixture.AssertDiagnostic(t, err, &want)
				sessionMessageHealth(t, p, b, s)
			}
		})
	})

	t.Run("identifier-notices-and-spelling", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var notices []fixture.Diagnostic
			_, s, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			r := sessionMessageConn(t, p, b)
			for _, c := range []struct{ original, clipped string }{
				{strings.Repeat("a", 64), strings.Repeat("a", 63)},
				{strings.Repeat("é", 32), strings.Repeat("é", 31)},
			} {
				sessionMessageSQL(t, p, b, r, "LISTEN \""+c.clipped+"\"")
				notices = nil
				fixture.RunStep(t, p.Ctx, s, recorder, fixture.Exec("NOTIFY \""+c.original+"\", 'clipped'", 0))
				want := fixture.Diagnostic{
					Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622",
					Message: "identifier \"" + c.original + "\" will be truncated to \"" + c.clipped + "\"",
				}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatalf("truncation diagnostics = %#v, want %#v", notices, []fixture.Diagnostic{want})
				}
				sessionMessageReceive(t, p, b, r, c.clipped, "clipped")
			}
			for _, q := range []string{"LISTEN MiXeD", "LISTEN \"MiXeD\"", "LISTEN \"雪\""} {
				sessionMessageSQL(t, p, b, r, q)
			}
			for _, c := range []struct{ q, channel string }{
				{"NOTIFY MIXED, 'case'", "mixed"}, {"NOTIFY \"MiXeD\", 'case'", "MiXeD"}, {"NOTIFY \"雪\", 'case'", "雪"},
			} {
				fixture.RunStep(t, p.Ctx, s, recorder, fixture.Exec(c.q, 0))
				sessionMessageReceive(t, p, b, r, c.channel, "case")
			}
		})
	})
}
