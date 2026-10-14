package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowValueAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE ROLE wv_reader", 0),
		fixture.Exec("CREATE TABLE wv_secret(id INT,v INT,n INT)", 0),
		fixture.Exec("INSERT INTO wv_secret VALUES(1,10,1)", 1),
		fixture.Exec("GRANT SELECT(id) ON wv_secret TO wv_reader", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	if b.PG {
		queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO wv_reader", 0))
	}
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION wv_reader", 0))
	for _, tc := range []struct {
		column, expression string
		value              int64
	}{
		{"v", "first_value(v)", 10}, {"n", "nth_value(1,n)", 1},
	} {
		t.Run(tc.column, func(t *testing.T) {
			q := "SELECT " + tc.expression + " OVER(ORDER BY id) AS x FROM public.wv_secret"
			for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for table wv_secret"})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			}
			queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT("+tc.column+") ON wv_secret TO wv_reader", 0))
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			success := fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{tc.value}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
			queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT("+tc.column+") ON wv_secret FROM wv_reader", 0))
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table wv_secret"})
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT("+tc.column+") ON wv_secret TO wv_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, success)
			queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT("+tc.column+") ON wv_secret FROM wv_reader", 0))
		})
	}
	q := "SELECT DISTINCT nth_value(1,(SELECT n FROM public.wv_secret)) OVER() AS x ORDER BY nth_value(1,(SELECT n FROM public.wv_secret)) OVER()"
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table wv_secret"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}
