package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoMissing(signature string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Position = fixture.Position(position)
	return d
}

func sslInfoFailure(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	// RunStep asserts the entire diagnostic and that no result/Rows/Stmt escapes.
	// Direct SQL exposes analysis failure at execute; explicit Prepare fails there,
	// before any parameter value can affect the signature's unknown argument type.
	for _, queryMode := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, d, args...)
			s.SelectRows = queryMode
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
	}
}

func TestReferenceSSLInfoDiagnostics(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET search_path TO ssl_part,public", 0))
		run(fixture.Exec("CREATE TABLE ssl_diagnostic_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO ssl_diagnostic_sentinel VALUES(7)", 1))
		for _, fn := range sslInfoFunctions {
			t.Run(fn.name, func(t *testing.T) {
				for _, arg := range []struct{ sql, typ string }{
					{"NULL", "unknown"}, {"1", "integer"}, {"'x'::text", "text"},
				} {
					t.Run(arg.typ, func(t *testing.T) {
						sslInfoFailure(t, p, c, b.Recorder, "SELECT "+fn.name+"("+arg.sql+")", sslInfoMissing(fn.name+"("+arg.typ+")", 8))
					})
				}
				for _, arg := range []struct {
					name  string
					value any
				}{{"nil", nil}, {"text", "x"}, {"integer", int64(1)}} {
					t.Run("parameter_"+arg.name, func(t *testing.T) {
						sslInfoFailure(t, p, c, b.Recorder, "SELECT "+fn.name+"($1)", sslInfoMissing(fn.name+"(unknown)", 8), arg.value)
					})
				}
			})
		}
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_part.ssl_is_used(NULL)", sslInfoMissing("ssl_part.ssl_is_used(unknown)", 8))
		sslInfoFailure(t, p, c, b.Recorder, "/*é*/ SELECT ssl_is_used(NULL)", sslInfoMissing("ssl_is_used(unknown)", 14))
		for _, tc := range []struct{ name, query, code, message string }{
			{"filter", "SELECT ssl_is_used() FILTER(WHERE TRUE)", "42809", "FILTER specified, but ssl_is_used is not an aggregate function"},
			{"over", "SELECT ssl_is_used() OVER()", "42809", "OVER specified, but ssl_is_used is not a window function nor an aggregate function"},
			{"101_arguments", "SELECT ssl_is_used(" + strings.Repeat("NULL,", 100) + "NULL)", "54023", "cannot pass more than 100 arguments to a function"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = "8"
				sslInfoFailure(t, p, c, b.Recorder, tc.query, d)
			})
		}
		run(fixture.Select("SELECT id FROM ssl_diagnostic_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(sslInfoSelect("SELECT " + sslInfoProjection))
		run(fixture.Prepared(sslInfoSelect("SELECT " + sslInfoProjection)))
	})
}
