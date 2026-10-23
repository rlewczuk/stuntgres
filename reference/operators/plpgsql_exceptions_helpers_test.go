package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plpgsqlExceptionDefine(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, typ, body string) {
	t.Helper()
	userOperatorExec(t, p, b, c, "CREATE FUNCTION "+name+"() RETURNS "+typ+" LANGUAGE plpgsql AS $body$"+body+"$body$")
}

func plpgsqlExceptionSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE ex_log(v integer)",
		"CREATE FUNCTION ex_put(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_log VALUES(x); SELECT x$$")
}

const plpgsqlExceptionRollbackBody = `DECLARE sink integer; x integer := 10; BEGIN
 sink := ex_put(1);
 BEGIN
  sink := ex_put(2); x := x+1; sink := 1/0; sink := ex_put(99);
 EXCEPTION WHEN division_by_zero THEN
  sink := (SELECT count(*) FROM ex_log); x := x + sink - 1; sink := ex_put(3);
 END;
 sink := ex_put(4); RETURN x;
END`

func plpgsqlExceptionRows(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, values ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT v FROM ex_log ORDER BY v", []string{"v"}, []string{"INT4"}, rows))
}

func plpgsqlExceptionDivision(function string) fixture.Diagnostic {
	d := plpgsqlRuntimeDiagnostic("22012", "division by zero", function, "assignment")
	d.Context = "PL/pgSQL assignment \"x := 1/0\"\n" + d.Context
	return d
}
