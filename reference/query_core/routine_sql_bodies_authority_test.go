package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineSQLBodiesFrozenAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"body_frozen_reader"})
	sqlBodiesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE ROLE body_frozen_reader",
			"GRANT USAGE ON SCHEMA public TO body_frozen_reader",
			"CREATE TABLE body_frozen_private(v integer)",
			"CREATE FUNCTION body_frozen_callee(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE FUNCTION body_frozen_read() RETURNS bigint BEGIN ATOMIC SELECT body_frozen_callee(v) FROM body_frozen_private; END",
			"CREATE FUNCTION body_frozen_callee(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+2000$$",
			"REVOKE EXECUTE ON FUNCTION body_frozen_callee(bigint) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION body_frozen_callee(bigint) TO body_frozen_reader",
			"GRANT SELECT ON body_frozen_private TO body_frozen_reader")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO body_frozen_private VALUES(8)", 1))
		routineExec(t, p, b, c, "SET SESSION AUTHORIZATION body_frozen_reader")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		q := "SELECT body_frozen_read() AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		value := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(1008)}}))
		}
		denied := func() {
			s := fixture.Error(q, "42501")
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		value()
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON body_frozen_private FROM body_frozen_reader", "SET SESSION AUTHORIZATION body_frozen_reader")
		denied()
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON body_frozen_private TO body_frozen_reader", "SET SESSION AUTHORIZATION body_frozen_reader")
		value()
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION body_frozen_callee(bigint) FROM body_frozen_reader", "SET SESSION AUTHORIZATION body_frozen_reader")
		denied()
		routineValue(t, p, b, c, "SELECT body_frozen_callee(8) AS v", "v", "INT8", int64(2008))
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION body_frozen_callee(bigint) TO body_frozen_reader", "SET SESSION AUTHORIZATION body_frozen_reader")
		value()
	})
}
