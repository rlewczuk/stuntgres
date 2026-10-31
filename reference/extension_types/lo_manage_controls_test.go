package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func lmControlSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	lmSetup(t, p, b, db, "lo", "v", "UPDATE OR DELETE", "ROW")
	loRun(t, p, b, db, fixture.Exec("INSERT INTO lm VALUES(2,910202,0)", 1))
	lmCreate(t, p, b, db, 910203, "deadbeef")
}

func lmControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	lmRows(t, p, b, db, lmRow(int64(910201), 0), []any{int64(2), int64(910202), int64(0)})
	lmBytes(t, p, b, db, 910201, "00ff")
	lmBytes(t, p, b, db, 910202, "6162")
	lmBytes(t, p, b, db, 910203, "deadbeef")
}

func lmControlMutate(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, prepared bool) {
	t.Helper()
	lmRun(t, p, b, db, prepared, fixture.Exec("UPDATE lm SET v=910203 WHERE id=1", 1))
	lmRun(t, p, b, db, prepared, fixture.Exec("DELETE FROM lm WHERE id=2", 1))
	lmRows(t, p, b, db, lmRow(int64(910203), 0))
	lmMissing(t, p, b, db, 910201)
	lmMissing(t, p, b, db, 910202)
	lmBytes(t, p, b, db, 910203, "deadbeef")
}

func TestReferenceLoManageControls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmControlSeed(t, p, b, c)
			lmControlVerify(t, p, b, c)
			if b.PG {
				lmControlMutate(t, p, b, c, prepared)
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			retained, err := c.QueryContext(p.Ctx, "SELECT id,n FROM lm ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			statement, err := c.PrepareContext(p.Ctx, "UPDATE lm SET v=910203 WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: "UPDATE lm SET v=910203 WHERE id=1"}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			lmControlVerify(t, p, b, c)
			lmControlMutate(t, p, b, held, false)
			for _, id := range []int64{1, 2} {
				if !retained.Next() {
					t.Fatal("retained row missing", retained.Err())
				}
				var got, n int64
				if err := retained.Scan(&got, &n); err != nil {
					t.Fatal(err)
				}
				if got != id || n != 0 {
					t.Fatal("retained row", got, n)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained row end", retained.Err())
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}

			target := fixture.NewFixture(t, false, true)
			tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			lmControlVerify(t, target, tb, target.Memory)
			bad := dump.String() + "\nCREATE TABLE lm_bad(v lo); INSERT INTO lm_bad VALUES ('bad');"
			d := loInputDiagnostic("bad", "22P02")
			d.Position = strconv.Itoa(strings.LastIndex(bad, "'bad'") + 1)
			fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
			lmControlVerify(t, target, tb, target.Memory)
			lmControlMutate(t, target, tb, target.Memory, prepared)
		})
	})
	t.Run("authentic_identity", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "lm_native")
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION lo CASCADE"); err != nil {
					t.Error(err)
					return
				}
				if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS lm_native CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			loRun(t, p, b, c, fixture.Exec("CREATE SCHEMA lm_native", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER FUNCTION lo_manage() RENAME TO real_cleanup", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER FUNCTION real_cleanup() SET SCHEMA lm_native", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE FUNCTION lo_manage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lm_decoy(id integer,v lo)", 0))
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lm_decoy VALUES(1,910201)", 1))
			loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER decoy BEFORE UPDATE ON lm_decoy FOR EACH ROW EXECUTE FUNCTION lo_manage(v)", 0))
			loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE UPDATE OR DELETE ON lm FOR EACH ROW EXECUTE FUNCTION lm_native.real_cleanup(v)", 0))
			var dump bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm_decoy SET v=910202", 1))
			lmBytes(t, p, b, c, 910201, "00ff")
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
			lmMissing(t, p, b, c, 910201)
			lmRows(t, p, b, c, lmRow(int64(910202), 0))
			lmBytes(t, p, b, c, 910202, "6162")
			// Exact scalar error also follows installed identity after rename.
			s := lmError("SELECT lm_native.real_cleanup()", "XX000", "lo_manage: not fired by trigger manager")
			s.SelectRows = true
			lmRun(t, p, b, c, prepared, s)
		})
	})
}

func TestReferenceLoManageFeatureSmoke(t *testing.T) {
	lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=v", 1))
		lmBytes(t, p, b, c, 910201, "00ff")
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		lmRun(t, p, b, tx, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
		lmMissing(t, p, b, tx, 910201)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		lmRows(t, p, b, c, lmRow(int64(910201), 0))
		lmBytes(t, p, b, c, 910201, "00ff")
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			lmRows(t, p, b, c, lmRow(int64(910201), 0))
			lmBytes(t, p, b, c, 910201, "00ff")
		}
		lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
		lmMissing(t, p, b, c, 910201)
		lmBytes(t, p, b, c, 910202, "6162")
		lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm", 1))
		lmRows(t, p, b, c)
		lmMissing(t, p, b, c, 910202)
	})
}
