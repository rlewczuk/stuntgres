package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUUIDOsspDiagnostics(t *testing.T) {
	uuidOsspCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE uuid_ossp_sentinel(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_ossp_sentinel VALUES(7)", 1))
		for _, tc := range []struct {
			q, sig string
			pos    int
			args   []any
		}{
			{"SELECT uuid_generate_v4(NULL)", "uuid_generate_v4(unknown)", 8, nil},
			{"SELECT uuid_generate_v4('x')", "uuid_generate_v4(unknown)", 8, nil},
			{"SELECT uuid_generate_v4(1)", "uuid_generate_v4(integer)", 8, nil},
			{"SELECT uuid_generate_v4('x'::text)", "uuid_generate_v4(text)", 8, nil},
			{"/*é*/ SELECT uuid_generate_v4(NULL)", "uuid_generate_v4(unknown)", 14, nil},
			{"SELECT public.uuid_generate_v4(NULL)", "public.uuid_generate_v4(unknown)", 8, nil},
			{"SELECT \"UUID_GENERATE_V4\"()", "UUID_GENERATE_V4()", 8, nil},
			{"SELECT uuid_generate_v4(DISTINCT NULL)", "uuid_generate_v4(unknown)", 8, nil},
			{"SELECT uuid_generate_v4($1)", "uuid_generate_v4(unknown)", 8, []any{nil}},
			{"SELECT uuid_generate_v4($1)", "uuid_generate_v4(unknown)", 8, []any{"x"}},
			{"SELECT uuid_generate_v4($1)", "uuid_generate_v4(unknown)", 8, []any{1}},
		} {
			t.Run(tc.q+"/"+strings.Join([]string{tc.sig}, ""), func(t *testing.T) { uuidOsspFailure(t, p, c, tc.q, uuidOsspMissing(tc.sig, tc.pos), tc.args...) })
		}
		for _, tc := range []struct{ q, code, msg string }{
			{"SELECT uuid_generate_v4() FILTER(WHERE TRUE)", "42809", "FILTER specified, but uuid_generate_v4 is not an aggregate function"},
			{"SELECT uuid_generate_v4() OVER()", "42809", "OVER specified, but uuid_generate_v4 is not a window function nor an aggregate function"},
			{"SELECT uuid_generate_v4(" + strings.Repeat("NULL,", 100) + "NULL)", "54023", "cannot pass more than 100 arguments to a function"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
				d.Position = "8"
				uuidOsspFailure(t, p, c, tc.q, d)
			})
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM uuid_ossp_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		uuidOsspRead(t, p, c, r, pg, "SELECT uuid_generate_v4() AS u", 1, 1)
	})
}
