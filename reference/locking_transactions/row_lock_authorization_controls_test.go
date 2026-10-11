package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockAuthorizationControl(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"row_lock_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		record := backend.Recorder
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE ROLE row_lock_reader", 0))
		if backend.PG {
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT USAGE ON SCHEMA public TO row_lock_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT SELECT(id) ON t TO row_lock_reader", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("SET SESSION AUTHORIZATION row_lock_reader", 0))
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, clause := range fixture.RowClauses {
			step := fixture.ImplicitError("SELECT id FROM t "+clause, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, record, step)
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT UPDATE(v) ON t TO row_lock_reader", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("SET SESSION AUTHORIZATION row_lock_reader", 0))
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT id FROM t "+clause, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
				if prepared {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, conn, record, step)
			}
		}
	}
}

func TestReferenceRowLockSequenceAuthorizationControl(t *testing.T) {
	if !*fixture.Mode {
		return
	} // Adjacent sequence ACL oracle, not table grants.
	p := fixture.NewPair(t)
	conn, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	record := p.PostgresRecord
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE SEQUENCE s", 0))
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE ROLE row_lock_reader", 0))
	t.Cleanup(func() { fixture.Admin.Exec("DROP OWNED BY row_lock_reader; DROP ROLE row_lock_reader") })
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT USAGE ON SCHEMA public TO row_lock_reader", 0))
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT SELECT ON SEQUENCE s TO row_lock_reader", 0))
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SELECT last_value FROM s "+clause, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence s"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step = fixture.Prepared(step)
			}
			fixture.RunStep(t, p.Ctx, conn, record, step)
		}
	}
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT UPDATE ON SEQUENCE s TO row_lock_reader", 0))
	fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("SET SESSION AUTHORIZATION row_lock_reader", 0))
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("SELECT last_value FROM s "+clause, fixture.Diagnostic{Code: "42809", Message: `cannot lock rows in sequence "s"`})
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step = fixture.Prepared(step)
			}
			fixture.RunStep(t, p.Ctx, conn, record, step)
		}
	}
}
