package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreProducers(t *testing.T) {
	t.Run("insert_query", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()", "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data SELECT x,5 FROM generate_series(1,3) AS s(x)", 2))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(3), int64(10)}))
		})
	})
	t.Run("conflict", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "CREATE TRIGGER u BEFORE UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data VALUES(1,5)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(1,7) ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING id,v", []any{int64(1), int64(17)}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data VALUES(1,8) ON CONFLICT DO NOTHING", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(17)}))
		})
	})
	t.Run("merge", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data VALUES(1,10),(2,20),(3,30)", 3))
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "CREATE TRIGGER b BEFORE DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()")
			s := fixture.Exec("MERGE INTO tr_data t USING (VALUES(1,5),(2,5),(4,5)) s(id,v) ON t.id=s.id WHEN MATCHED AND s.id=2 THEN DELETE WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", 2)
			if b.PG {
				s = fixture.Exec(s.Query, 0)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}, []any{int64(4), int64(10)}))
		})
	})
	t.Run("referential_action", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_parent(id integer PRIMARY KEY)", "CREATE TABLE tr_child(id integer PRIMARY KEY,pid integer REFERENCES tr_parent(id) ON UPDATE CASCADE ON DELETE CASCADE,v integer)", "CREATE FUNCTION tr_child_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := OLD.v+1; RETURN NEW; END$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_parent VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_child VALUES(1,1,10)", 1))
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE UPDATE ON tr_child FOR EACH ROW EXECUTE FUNCTION tr_child_fn()")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE tr_parent SET id=2", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,pid,v FROM tr_child", []string{"id", "pid", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(11)}}))
		})
	})
	t.Run("partition", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_part(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE tr_lo PARTITION OF tr_part FOR VALUES FROM(0) TO(10)", "CREATE FUNCTION tr_part_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+3; RETURN NEW; END$$", "CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tr_part FOR EACH ROW EXECUTE FUNCTION tr_part_fn()", "CREATE TABLE tr_hi PARTITION OF tr_part FOR VALUES FROM(10) TO(20)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_part VALUES(1,5),(11,5)", 2))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("SELECT id,v FROM tr_part ORDER BY id", []any{int64(1), int64(8)}, []any{int64(11), int64(8)}))
			userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_escape() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id := 11; RETURN NEW; END$$", "CREATE TRIGGER z BEFORE INSERT ON tr_lo FOR EACH ROW EXECUTE FUNCTION tr_escape()")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("INSERT INTO tr_part VALUES(2,5)", "23514"))
		})
	})
	t.Run("partition_checks", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tr_check(id integer,v integer CONSTRAINT tr_guard CHECK(v>0)) PARTITION BY RANGE(id)",
				"CREATE TABLE tr_check_lo PARTITION OF tr_check FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE tr_check_hi PARTITION OF tr_check FOR VALUES FROM(10) TO(20)",
				"CREATE FUNCTION tr_check_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=11; NEW.v:=-1; RETURN NEW; END$$",
				"CREATE FUNCTION tr_check_escape() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=11; RETURN NEW; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON tr_check_lo FOR EACH ROW EXECUTE FUNCTION tr_check_fail()")
			check := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"tr_check_lo\" violates check constraint \"tr_guard\"")
			check.Detail = "Failing row contains (11, -1)."
			check.Schema = "public"
			check.Table = "tr_check_lo"
			check.Constraint = "tr_guard"
			run(fixture.ErrorDiagnostic("INSERT INTO tr_check VALUES(1,1)", check))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO tr_check VALUES(1,1)", check)))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_check_lo FOR EACH ROW EXECUTE FUNCTION tr_check_escape()")
			selected := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"tr_check_lo\" violates partition constraint")
			selected.Detail = "Failing row contains (11, 1)."
			selected.Schema = "public"
			selected.Table = "tr_check_lo"
			run(fixture.ErrorDiagnostic("INSERT INTO tr_check VALUES(1,1)", selected))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO tr_check VALUES(1,1)", selected)))
			run(triggerRows("SELECT id,v FROM tr_check"))
		})
	})
	t.Run("leaf_only_visibility", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tr_leaf(id integer,v integer) PARTITION BY RANGE(id)",
				"CREATE TABLE tr_leaf_lo PARTITION OF tr_leaf FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE tr_leaf_hi PARTITION OF tr_leaf FOR VALUES FROM(10) TO(20)",
				"CREATE FUNCTION tr_leaf_visible() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=(SELECT count(*)::integer FROM tr_leaf); RETURN NEW; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON tr_leaf_lo FOR EACH ROW EXECUTE FUNCTION tr_leaf_visible()")
			run(fixture.Exec("INSERT INTO tr_leaf VALUES(1,99),(11,99),(2,99),(12,99),(3,99)", 5))
			run(triggerRows("SELECT id,v FROM tr_leaf ORDER BY id", []any{int64(1), int64(0)}, []any{int64(2), int64(2)}, []any{int64(3), int64(4)}, []any{int64(11), int64(99)}, []any{int64(12), int64(99)}))
			q := "INSERT INTO tr_leaf VALUES($1,99),($2,99)"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			userOperatorExec(t, p, b, c, "DROP FUNCTION tr_leaf_visible() CASCADE")
			s := fixture.Exec(q, 2)
			s.Args = []any{4, 14}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION tr_leaf_visible() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=(SELECT count(*)::integer FROM tr_leaf); RETURN NEW; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON tr_leaf_lo FOR EACH ROW EXECUTE FUNCTION tr_leaf_visible()")
			s = fixture.Exec(q, 2)
			s.Args = []any{5, 15}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			run(triggerRows("SELECT id,v FROM tr_leaf WHERE id IN(4,5,14,15) ORDER BY id", []any{int64(4), int64(99)}, []any{int64(5), int64(7)}, []any{int64(14), int64(99)}, []any{int64(15), int64(99)}))
		})
	})
	t.Run("partition_move", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			rows := func(query string, want ...[]any) fixture.Step {
				return fixture.Select(query, []string{"id", "v", "trace"}, []string{"INT4", "INT4", "TEXT"}, want)
			}
			// Generated partition definitions are an incumbent native boundary;
			// ordinary-table generation after BEFORE is covered by Ordering.
			if !b.PG {
				run(fixture.Error("CREATE TABLE tr_move_generated(id integer,g integer GENERATED ALWAYS AS(id+1) STORED) PARTITION BY RANGE(id)", "0A000"))
				run(fixture.Select("SELECT count(*) AS v FROM pg_class WHERE relname='tr_move_generated'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
				userOperatorExec(t, p, b, c, "CREATE TABLE tr_move_gen_parent(id integer,g integer) PARTITION BY RANGE(id)", "CREATE TABLE tr_move_gen_child(id integer,g integer GENERATED ALWAYS AS(id+1) STORED)")
				run(fixture.Error("ALTER TABLE tr_move_gen_parent ATTACH PARTITION tr_move_gen_child FOR VALUES FROM(0) TO(10)", "42804"))
				run(fixture.Select("SELECT relispartition FROM pg_class WHERE oid='tr_move_gen_child'::regclass", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
			}
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tr_move(id integer,v integer NOT NULL CHECK(v<30),trace text) PARTITION BY RANGE(id)",
				"CREATE TABLE tr_move_lo PARTITION OF tr_move FOR VALUES FROM(0) TO(10)",
				"CREATE TABLE tr_move_hi PARTITION OF tr_move FOR VALUES FROM(10) TO(20)",
				"CREATE TABLE tr_move_other PARTITION OF tr_move FOR VALUES FROM(20) TO(30)")
			run(fixture.Exec("INSERT INTO tr_move(id,v,trace) VALUES(1,5,'base'),(2,6,'base'),(3,7,'base'),(4,8,'base'),(5,9,'base'),(6,20,'base')", 6))
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION tr_move_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+3; NEW.trace := NEW.trace||':'||TG_OP||':'||TG_TABLE_NAME; RETURN NEW; END$$",
				"CREATE FUNCTION tr_move_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN CASE WHEN OLD.id=2 THEN NULL ELSE OLD END; END$$",
				"CREATE FUNCTION tr_move_delete_error() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v := 1/0; RETURN OLD; END$$",
				"CREATE FUNCTION tr_move_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.trace := NEW.trace||':'||TG_OP||':'||TG_TABLE_NAME||':'||(SELECT count(*)::text FROM tr_move_lo); NEW.v := CASE WHEN NEW.id=13 OR NEW.id=15 THEN NULL ELSE NEW.v*2 END; NEW.id := CASE WHEN NEW.id=14 THEN 24 ELSE NEW.id END; RETURN CASE WHEN NEW.id=13 THEN NULL ELSE NEW END; END$$",
				"CREATE TRIGGER u BEFORE UPDATE ON tr_move FOR EACH ROW EXECUTE FUNCTION tr_move_update()",
				"CREATE TRIGGER d BEFORE DELETE ON tr_move_lo FOR EACH ROW EXECUTE FUNCTION tr_move_delete()",
				"CREATE TRIGGER i BEFORE INSERT ON tr_move_hi FOR EACH ROW EXECUTE FUNCTION tr_move_insert()")
			run(rows("UPDATE tr_move SET v=7 WHERE id=2 RETURNING id,v,trace", []any{int64(2), int64(10), "base:UPDATE:tr_move_lo"}))
			run(rows("UPDATE tr_move SET id=11 WHERE id=1 RETURNING id,v,trace", []any{int64(11), int64(16), "base:UPDATE:tr_move_lo:INSERT:tr_move_hi:5"}))
			// A DELETE veto keeps the original row; an INSERT veto leaves the
			// source deletion but contributes no UPDATE count or RETURNING row.
			run(fixture.Exec("UPDATE tr_move SET id=12 WHERE id=2", 0))
			run(rows("UPDATE tr_move SET id=13 WHERE id=3 RETURNING id,v,trace"))
			run(fixture.Error("UPDATE tr_move SET id=14 WHERE id=4", "23514"))
			run(fixture.Error("UPDATE tr_move SET id=15 WHERE id=5", "23502"))
			run(fixture.Error("UPDATE tr_move SET id=16 WHERE id=6", "23514"))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER d BEFORE DELETE ON tr_move_lo FOR EACH ROW EXECUTE FUNCTION tr_move_delete_error()")
			run(fixture.Error("UPDATE tr_move SET id=13 WHERE id=2", "22012"))
			run(rows("SELECT id,v,trace FROM tr_move ORDER BY id",
				[]any{int64(2), int64(10), "base:UPDATE:tr_move_lo"},
				[]any{int64(4), int64(8), "base"},
				[]any{int64(5), int64(9), "base"},
				[]any{int64(6), int64(20), "base"},
				[]any{int64(11), int64(16), "base:UPDATE:tr_move_lo:INSERT:tr_move_hi:5"}))
		})
	})
	t.Run("partition_attach_detach", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_attach_parent(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE tr_attach_leaf(id integer,v integer)", "CREATE FUNCTION tr_attach_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$", "CREATE FUNCTION tr_attach_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*2; RETURN NEW; END$$", "CREATE TRIGGER a BEFORE INSERT ON tr_attach_parent FOR EACH ROW EXECUTE FUNCTION tr_attach_add(3)", "CREATE TRIGGER z BEFORE INSERT ON tr_attach_leaf FOR EACH ROW EXECUTE FUNCTION tr_attach_double()", "ALTER TABLE tr_attach_parent ATTACH PARTITION tr_attach_leaf FOR VALUES FROM(10) TO(20)")
			run(triggerRows("INSERT INTO tr_attach_parent VALUES(11,5) RETURNING id,v", []any{int64(11), int64(16)}))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_attach_parent FOR EACH ROW EXECUTE FUNCTION tr_attach_add(7)")
			run(triggerRows("INSERT INTO tr_attach_parent VALUES(12,5) RETURNING id,v", []any{int64(12), int64(24)}))
			userOperatorExec(t, p, b, c, "ALTER TABLE tr_attach_parent DETACH PARTITION tr_attach_leaf")
			run(triggerRows("INSERT INTO tr_attach_leaf VALUES(13,5) RETURNING id,v", []any{int64(13), int64(10)}))
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_attach_conflict(id integer,v integer)", "CREATE TRIGGER a BEFORE INSERT ON tr_attach_conflict FOR EACH ROW EXECUTE FUNCTION tr_attach_add(9)")
			run(fixture.Error("ALTER TABLE tr_attach_parent ATTACH PARTITION tr_attach_conflict FOR VALUES FROM(20) TO(30)", "42710"))
			run(triggerRows("INSERT INTO tr_attach_conflict VALUES(99,5) RETURNING id,v", []any{int64(99), int64(14)}))
		})
	})
	t.Run("ordinary_inheritance", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_inh_parent(id integer,v integer)", "CREATE TABLE tr_inh_child() INHERITS(tr_inh_parent)", "CREATE FUNCTION tr_inh_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+3; RETURN NEW; END$$", "CREATE FUNCTION tr_inh_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*2; RETURN NEW; END$$", "CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tr_inh_parent FOR EACH ROW EXECUTE FUNCTION tr_inh_add()")
			run(triggerRows("INSERT INTO tr_inh_child VALUES(21,5) RETURNING id,v", []any{int64(21), int64(5)}))
			userOperatorExec(t, p, b, c, "CREATE TRIGGER z BEFORE UPDATE ON tr_inh_child FOR EACH ROW EXECUTE FUNCTION tr_inh_double()")
			run(triggerRows("UPDATE tr_inh_parent SET v=3 WHERE id=21 RETURNING id,v", []any{int64(21), int64(6)}))
		})
	})
}

func TestReferenceTriggerCorePrepared(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		q := "INSERT INTO tr_data VALUES($1,$2) RETURNING id,v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run := func(id, value int64) {
			s := triggerRows(q, []any{id, value})
			s.Args = []any{id, 5}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
		}
		run(0, 5)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
		run(1, 16)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)")
		run(2, 24)
	})
}
