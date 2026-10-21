package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL18.6 expectations independently specified and observed by the root.
// AssertDiagnostic compares the entire portable logical diagnostic record.
func uuidOsspTimeDiagnostics(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE uuid_time_sentinel(id integer)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_time_sentinel VALUES(7)", 1))
	for _, name := range uuidOsspTimeNames {
		for _, tc := range []struct {
			q, signature string
			position     int
			args         []any
		}{
			{"SELECT " + name + "(NULL)", name + "(unknown)", 8, nil},
			{"SELECT " + name + "('x')", name + "(unknown)", 8, nil},
			{"SELECT " + name + "(1)", name + "(integer)", 8, nil},
			{"SELECT " + name + "('x'::text)", name + "(text)", 8, nil},
			{"SELECT " + name + "(NULL::uuid)", name + "(uuid)", 8, nil},
			{"/*é*/ SELECT " + name + "(NULL)", name + "(unknown)", 14, nil},
			{"SELECT public." + name + "(NULL)", "public." + name + "(unknown)", 8, nil},
			{"SELECT \"" + strings.ToUpper(name) + "\"()", strings.ToUpper(name) + "()", 8, nil},
			{"SELECT " + name + "(DISTINCT NULL)", name + "(unknown)", 8, nil},
			{"SELECT " + name + "($1)", name + "(unknown)", 8, []any{nil}},
			{"SELECT " + name + "($1)", name + "(unknown)", 8, []any{"x"}},
			{"SELECT " + name + "($1)", name + "(unknown)", 8, []any{1}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				uuidOsspFailure(t, p, c, tc.q, uuidOsspMissing(tc.signature, tc.position), tc.args...)
			})
		}
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT " + name + "() FILTER(WHERE TRUE)", "42809", "FILTER specified, but " + name + " is not an aggregate function"},
			{"SELECT " + name + "() OVER()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function"},
			{"SELECT " + name + "(" + strings.Repeat("NULL,", 100) + "NULL)", "54023", "cannot pass more than 100 arguments to a function"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = "8"
				uuidOsspFailure(t, p, c, tc.q, d)
			})
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM uuid_time_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		uuidOsspTimeRead(t, p, c, r, pg, "SELECT "+name+"() AS u", 1, 1, name == "uuid_generate_v1mc")
	}
}

func TestReferenceUUIDOsspTimeDiagnostics(t *testing.T) {
	uuidOsspCase(t, true, uuidOsspTimeDiagnostics)
}
