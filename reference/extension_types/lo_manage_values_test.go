package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoManageValues(t *testing.T) {
	t.Run("installation", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			loInstall(t, p, b, c)
			lmRun(t, p, b, c, prepared, fixture.Select("SELECT proname,pronargs=0 AS zero_args,prorettype=2279 AS trigger_result,provolatile='v' AS volatile,NOT proisstrict AS nonstrict,NOT prosecdef AS invoker,proparallel='u' AS unsafe,prosrc,probin FROM pg_proc WHERE oid='lo_manage'::regproc", []string{"proname", "zero_args", "trigger_result", "volatile", "nonstrict", "invoker", "unsafe", "prosrc", "probin"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{"lo_manage", true, true, true, true, true, true, "lo_manage", "$libdir/lo"}}))
		})
	})
	for _, typ := range []string{"lo", "oid"} {
		t.Run(typ, func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, typ, "v", "UPDATE OR DELETE", "ROW")
				lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910201 WHERE id=1", 1))
				lmRows(t, p, b, c, lmRow(int64(910201), 0))
				lmBytes(t, p, b, c, 910201, "00ff")
				lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET n=n+1 WHERE id=1", 1))
				lmRows(t, p, b, c, lmRow(int64(910201), 1))
				lmBytes(t, p, b, c, 910201, "00ff")
				lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=$1::oid WHERE id=$2", 1, "910202", int64(1)))
				lmRows(t, p, b, c, lmRow(int64(910202), 1))
				lmMissing(t, p, b, c, 910201)
				lmBytes(t, p, b, c, 910202, "6162")
				lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm WHERE id=1", 1))
				lmRows(t, p, b, c)
				lmMissing(t, p, b, c, 910202)
			})
		})
	}
	t.Run("dangling", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910203", 1))
			lmMissing(t, p, b, c, 910201)
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=v", 1))
			lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "42704", "large object 910203 does not exist"))
			lmRows(t, p, b, c, lmRow(int64(910203), 0))
		})
	})
	t.Run("unsigned_and_zero", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			lmCreate(t, p, b, c, 4294967295, "fe")
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v='4294967295'::oid", 1))
			lmMissing(t, p, b, c, 910201)
			lmRows(t, p, b, c, lmRow(int64(4294967295), 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=0::oid", 1))
			lmMissing(t, p, b, c, 4294967295)
			lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "42704", "large object 0 does not exist"))
			lmRows(t, p, b, c, lmRow(int64(0), 0))
		})
	})
	t.Run("held", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// One held statement consumes each distinct bound reference.
			lmHeldValues(t, p, b, c)
		})
	})
}

func lmHeldValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Cleanup is registered independently because this leaf retains one statement.
	defer func() {
		_, err := c.ExecContext(p.Ctx, "SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid IN (910201::oid,910202::oid)")
		if err != nil {
			t.Error(err)
		}
	}()
	lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
	s, err := c.PrepareContext(p.Ctx, "UPDATE lm SET v=$1::oid WHERE id=$2")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := fixture.HeldStatement{Executor: c, Statement: s, SQL: "UPDATE lm SET v=$1::oid WHERE id=$2"}
	lmRows(t, p, b, c, lmRow(int64(910201), 0))
	lmBytes(t, p, b, c, 910201, "00ff")
	for _, oid := range []string{"910201", "910202", "910202"} {
		loRun(t, p, b, h, fixture.Exec(h.SQL, 1, oid, int64(1)))
		if oid == "910201" {
			lmBytes(t, p, b, c, 910201, "00ff")
		} else {
			lmMissing(t, p, b, c, 910201)
			lmBytes(t, p, b, c, 910202, "6162")
		}
	}
	d, err := c.PrepareContext(p.Ctx, "DELETE FROM lm WHERE id=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	loRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: d, SQL: "DELETE FROM lm WHERE id=$1"}, fixture.Exec("DELETE FROM lm WHERE id=$1", 1, int64(1)))
	lmMissing(t, p, b, c, 910202)
	lmRows(t, p, b, c)
}

func TestReferenceLoManageNulls(t *testing.T) {
	lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		lmSetup(t, p, b, c, "lo", "v,'ignored',NULL", "UPDATE OR DELETE", "ROW")
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=NULL", 1))
		lmMissing(t, p, b, c, 910201)
		lmRows(t, p, b, c, lmRow(nil, 0))
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=NULL", 1))
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
		lmBytes(t, p, b, c, 910202, "6162")
		lmRows(t, p, b, c, lmRow(int64(910202), 0))
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=NULL", 1))
		lmMissing(t, p, b, c, 910202)
		lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm", 1))
		lmRows(t, p, b, c)
	})
	t.Run("literal_null_column", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
			loRun(t, p, b, c, fixture.Exec(`ALTER TABLE lm RENAME COLUMN v TO "null"`, 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE DELETE ON lm FOR EACH ROW EXECUTE FUNCTION lo_manage(NULL)", 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm", 1))
			lmMissing(t, p, b, c, 910201)
		})
	})
}
