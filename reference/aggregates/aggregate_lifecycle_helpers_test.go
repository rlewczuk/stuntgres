package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func lifecycleExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		createAggregateRun(t, p, b, c, fixture.Exec(q, 0))
	}
}
func lifecycleSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	lifecycleExec(t, p, b, c,
		"CREATE FUNCTION al_add(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
		"CREATE FUNCTION al_add8(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+$2$$",
		"CREATE AGGREGATE al_sum(integer) (SFUNC=al_add,STYPE=int4,INITCOND='0')",
		"CREATE AGGREGATE al_sum(bigint) (SFUNC=al_add8,STYPE=int8,INITCOND='100')",
		"CREATE TABLE al_input(x integer)")
	createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO al_input VALUES(1),(2),(3)", 3))
}
func lifecycleValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	for _, tc := range []struct {
		q, typ string
		v      int64
	}{
		{"SELECT " + name + "(x) AS v FROM al_input", "INT4", 6},
		{"SELECT " + name + "(x) AS v FROM al_input WHERE FALSE", "INT4", 0},
		{"SELECT al_sum(x::bigint) AS v FROM al_input", "INT8", 106},
		{"SELECT al_sum(x::bigint) AS v FROM al_input WHERE FALSE", "INT8", 100},
	} {
		createAggregateBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.v}}))
	}
}
func lifecycleOID(t *testing.T, p *fixture.Pair, c *sql.Conn, signature string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", signature).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func lifecycleError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
func lifecycleRestrict(q, detail string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function al_dst.al_renamed(integer) because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	s.ErrorStage = "execute"
	return s
}
func lifecycleFacts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, id int64) {
	t.Helper()
	q := "SELECT p.oid=$1::oid AS identity,a.aggfnoid=p.oid AS self,a.aggtransfn='al_add(integer,integer)'::regprocedure::oid AS support,a.aggtranstype=23::oid AS state,p.prorettype=23::oid AS result FROM (SELECT oid,prorettype FROM pg_proc WHERE oid=$2::regprocedure::oid) p JOIN pg_aggregate a ON a.aggfnoid=p.oid"
	createAggregateBoth(t, p, b, c, fixture.Select(q, []string{"identity", "self", "support", "state", "result"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}}, id, name+"(integer)"))
}
