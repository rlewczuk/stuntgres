package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoManageBoundaries(t *testing.T) {
	for _, target := range []string{"n=n+1", "v=v"} {
		t.Run("targets_"+target, func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
				loRun(t, p, b, c, fixture.Exec("CREATE FUNCTION lm_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.v := 910202::oid; RETURN NEW; END $$", 0))
				loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER aa_rewrite BEFORE UPDATE ON lm FOR EACH ROW EXECUTE FUNCTION lm_rewrite()", 0))
				lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET "+target, 1))
				if target == "v=v" {
					lmMissing(t, p, b, c, 910201)
					lmRows(t, p, b, c, lmRow(int64(910202), 0))
				} else {
					lmBytes(t, p, b, c, 910201, "00ff")
					lmRows(t, p, b, c, lmRow(int64(910202), 1))
				}
				lmBytes(t, p, b, c, 910202, "6162")
			})
		})
	}
	t.Run("dropped_attribute", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			loRun(t, p, b, c, fixture.Exec("DROP TABLE lm", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lm(gone integer,id integer,v lo,n integer)", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER TABLE lm DROP COLUMN gone", 0))
			loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE UPDATE ON lm FOR EACH ROW EXECUTE FUNCTION lo_manage(v)", 0))
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lm VALUES(1,910201,0)", 1))
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910202", 1))
			lmRows(t, p, b, c, lmRow(int64(910202), 0))
			lmMissing(t, p, b, c, 910201)
		})
	})
	t.Run("when_and_insert", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "INSERT", "ROW")
			lmBytes(t, p, b, c, 910201, "00ff")
			loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_skip BEFORE UPDATE ON lm FOR EACH ROW WHEN(false) EXECUTE FUNCTION lo_manage(v)", 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("INSERT INTO lm VALUES(2,910202,0)", 1))
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=910202 WHERE id=1", 1))
			lmRows(t, p, b, c, lmRow(int64(910202), 0), []any{int64(2), int64(910202), int64(0)})
			lmBytes(t, p, b, c, 910201, "00ff")
			lmBytes(t, p, b, c, 910202, "6162")
		})
	})
	t.Run("truncate_drop", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			lmRun(t, p, b, c, prepared, fixture.Exec("TRUNCATE lm", 0))
			lmRows(t, p, b, c)
			lmBytes(t, p, b, c, 910201, "00ff")
			loRun(t, p, b, c, fixture.Exec("INSERT INTO lm VALUES(1,910202,0)", 1))
			lmRun(t, p, b, c, prepared, fixture.Exec("DROP TABLE lm", 0))
			loRun(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='lm'", []string{"relname"}, []string{"NAME"}, [][]any{}))
			lmBytes(t, p, b, c, 910202, "6162")
		})
	})
	for _, together := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared_reference", true: "shared_statement_atomicity"}[together], func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
				loRun(t, p, b, c, fixture.Exec("INSERT INTO lm VALUES(2,910201,0)", 1))
				if together {
					lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "42704", "large object 910201 does not exist"))
					lmRows(t, p, b, c, lmRow(int64(910201), 0), []any{int64(2), int64(910201), int64(0)})
					lmBytes(t, p, b, c, 910201, "00ff")
				} else {
					lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm WHERE id=1", 1))
					lmMissing(t, p, b, c, 910201)
					lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm WHERE id=2", "42704", "large object 910201 does not exist"))
					lmRows(t, p, b, c, []any{int64(2), int64(910201), int64(0)})
				}
			})
		})
	}
}

func TestReferenceLoManageAfter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		update     string
		rewrite    bool
		shared     bool
		want       []any
		oldMissing bool
	}{
		{name: "changed", update: "UPDATE lm SET v=910202 WHERE id=1", want: lmRow(int64(910202), 0), oldMissing: true},
		{name: "equal", update: "UPDATE lm SET v=v WHERE id=1", want: lmRow(int64(910201), 0)},
		{name: "to_null", update: "UPDATE lm SET v=NULL WHERE id=1", want: lmRow(nil, 0), oldMissing: true},
		{name: "unmentioned_before_rewrite", update: "UPDATE lm SET n=n+1 WHERE id=1", rewrite: true, want: lmRow(int64(910202), 1)},
		{name: "mentioned_before_rewrite", update: "UPDATE lm SET v=v WHERE id=1", rewrite: true, want: lmRow(int64(910202), 0), oldMissing: true},
		{name: "failing_unlink_atomicity", update: "UPDATE lm SET v=910202", shared: true, want: lmRow(int64(910201), 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
				loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
				loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup AFTER UPDATE OR DELETE ON lm FOR EACH ROW EXECUTE FUNCTION lo_manage(v)", 0))
				if tc.rewrite {
					loRun(t, p, b, c, fixture.Exec("CREATE FUNCTION lm_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.v := 910202::oid; RETURN NEW; END $$", 0))
					loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER aa_rewrite BEFORE UPDATE ON lm FOR EACH ROW EXECUTE FUNCTION lm_rewrite()", 0))
				}
				if tc.shared {
					loRun(t, p, b, c, fixture.Exec("INSERT INTO lm VALUES(2,910201,0)", 1))
					lmRun(t, p, b, c, prepared, lmError(tc.update, "42704", "large object 910201 does not exist"))
					lmRows(t, p, b, c, tc.want, []any{int64(2), int64(910201), int64(0)})
				} else {
					lmRun(t, p, b, c, prepared, fixture.Exec(tc.update, 1))
					lmRows(t, p, b, c, tc.want)
				}
				if tc.oldMissing {
					lmMissing(t, p, b, c, 910201)
				} else {
					lmBytes(t, p, b, c, 910201, "00ff")
				}
				lmBytes(t, p, b, c, 910202, "6162")
			})
		})
	}
}
