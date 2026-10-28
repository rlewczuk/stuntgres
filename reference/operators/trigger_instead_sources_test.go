package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTriggerInsteadLogicalIdentity(t *testing.T) {
	for _, op := range []string{"UPDATE", "DELETE"} {
		for _, grow := range []bool{false, true} {
			name := op
			if grow {
				name += "-growing"
			}
			t.Run(name, func(t *testing.T) {
				afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
					insteadSetup(t, p, b, c)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,10)", 1))
					userOperatorExec(t, p, b, c, "CREATE VIEW io_dup AS SELECT id,v FROM io_base UNION ALL SELECT id,v FROM io_base")
					extra := ""
					if grow {
						extra = "INSERT INTO io_base VALUES(100+(SELECT count(*)::integer FROM io_log),50);"
					}
					userOperatorExec(t, p, b, c, "CREATE FUNCTION io_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_log VALUES(TG_OP,OLD.id,OLD.v,NEW.v); "+extra+" RETURN COALESCE(NEW,OLD); END$$", "CREATE TRIGGER body INSTEAD OF UPDATE OR DELETE ON io_dup FOR EACH ROW EXECUTE FUNCTION io_occurrence()")
					run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
					q := "DELETE FROM io_dup RETURNING id,v"
					v := int64(10)
					var nv any = nil
					if op == "UPDATE" {
						q = "UPDATE io_dup SET v=20 RETURNING id,v"
						v = 20
						nv = int64(20)
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(q[:len(q)-len(" RETURNING id,v")], 2))
					run(fixture.Exec("ROLLBACK", 0))
					s := triggerRows(q, []any{int64(1), v}, []any{int64(1), v})
					s.Bag = true
					run(s)
					run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{op, int64(1), int64(10), nv}, []any{op, int64(1), int64(10), nv}))
					want := [][]any{{int64(1), int64(10)}}
					if grow {
						want = append(want, []any{int64(101), int64(50)}, []any{int64(102), int64(50)})
					}
					run(triggerRows("SELECT id,v FROM io_base ORDER BY id", want...))
					run(fixture.Exec("DELETE FROM io_log", 2))
					empty := "DELETE FROM io_dup WHERE false"
					if op == "UPDATE" {
						empty = "UPDATE io_dup SET v=20 WHERE false"
					}
					run(fixture.Exec(empty, 0))
				})
			})
		}
	}
}

func TestReferenceTriggerInsteadSourceFrames(t *testing.T) {
	t.Run("defaults-once", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE io_defaults START 10")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			s := triggerRows("INSERT INTO io_view(id,v) SELECT id,nextval('io_defaults')::integer FROM (VALUES(1),(2)) s(id) RETURNING id,v", []any{int64(1), int64(110)}, []any{int64(2), int64(111)})
			s.Bag = true
			run(s)
			run(triggerRows("INSERT INTO io_view VALUES(3,nextval('io_defaults')::integer) RETURNING id,v", []any{int64(3), int64(112)}))
			run(triggerRows("INSERT INTO io_view VALUES(4,NULL) RETURNING id,v", []any{int64(4), nil}))
			run(triggerRows("INSERT INTO io_view(id) VALUES(5) RETURNING id,v", []any{int64(5), nil}))
			run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(12)}, []any{int64(3), int64(13)}, []any{int64(4), nil}, []any{int64(5), nil}))
			run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"INSERT", int64(1), nil, int64(10)}, []any{"INSERT", int64(2), nil, int64(11)}, []any{"INSERT", int64(3), nil, int64(12)}, []any{"INSERT", int64(4), nil, nil}, []any{"INSERT", int64(5), nil, nil}))
			run(fixture.Select("SELECT last_value FROM io_defaults", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
		})
	})
	t.Run("input-and-simultaneous", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			s := triggerRows("INSERT INTO io_view SELECT s.id,s.v+$1 FROM (VALUES(1,9),(2,19)) s(id,v) RETURNING id,v", []any{int64(1), int64(110)}, []any{int64(2), int64(120)})
			s.Args = []any{1}
			s.Bag = true
			run(s)
			run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
			run(fixture.Exec("DELETE FROM io_base", 2))
			run(fixture.Exec("INSERT INTO io_base VALUES(1,10)", 1))
			run(triggerRows("UPDATE io_view SET (id,v)=(v,id) RETURNING id,v", []any{int64(10), int64(101)}))
			run(triggerRows("SELECT id,v FROM io_base", []any{int64(10), int64(2)}))
			run(triggerRows("WITH changed AS (UPDATE io_view SET v=7 RETURNING id,v) SELECT id,v FROM changed", []any{int64(10), int64(107)}))
			run(triggerRows("SELECT id,v FROM io_base", []any{int64(10), int64(8)}))
		})
	})
	for _, op := range []string{"UPDATE", "DELETE"} {
		t.Run(op+"-sources", func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,10)", 1))
				userOperatorExec(t, p, b, c, "CREATE TABLE io_source(sid integer,delta integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_source VALUES(7,3),(8,4)", 2))
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_log VALUES(TG_OP,OLD.id,OLD.v,NEW.v); RETURN COALESCE(NEW,OLD); END$$")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				q := "UPDATE io_view t SET v=t.v+s.delta FROM io_source s WHERE t.id=1 RETURNING t.id,t.v,s.sid,s.delta,(SELECT s.delta+1) AS correlated"
				rows := [][]any{{int64(1), int64(13), int64(7), int64(3), int64(4)}, {int64(1), int64(14), int64(8), int64(4), int64(5)}}
				var n1, n2 any = int64(13), int64(14)
				if op == "DELETE" {
					q = "DELETE FROM io_view t USING io_source s WHERE t.id=1 RETURNING t.id,t.v,s.sid,s.delta,(SELECT s.delta+1) AS correlated"
					rows[0][1] = int64(10)
					rows[1][1] = int64(10)
					n1 = nil
					n2 = nil
				}
				s := fixture.Select(q, []string{"id", "v", "sid", "delta", "correlated"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, rows)
				s.Bag = true
				run(s)
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{op, int64(1), int64(10), n1}, []any{op, int64(1), int64(10), n2}))
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(10)}))
				run(fixture.Exec(q[:len(q)-len(" RETURNING t.id,t.v,s.sid,s.delta,(SELECT s.delta+1) AS correlated")], 2))
			})
		})
	}
	t.Run("demand-complete-old", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,10),(2,20)", 2))
			userOperatorExec(t, p, b, c, "CREATE VIEW io_aggregate AS SELECT 9 AS id,sum(v)::integer AS v FROM io_base", "CREATE VIEW io_constant AS SELECT 8 AS id,40 AS v", "CREATE FUNCTION io_old() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_log VALUES(TG_OP,OLD.id,OLD.v,NEW.v); RETURN COALESCE(NEW,OLD); END$$", "CREATE TRIGGER body INSTEAD OF UPDATE OR DELETE ON io_aggregate FOR EACH ROW EXECUTE FUNCTION io_old()", "CREATE TRIGGER body INSTEAD OF UPDATE OR DELETE ON io_constant FOR EACH ROW EXECUTE FUNCTION io_old()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("UPDATE io_aggregate SET id=id WHERE id=9", 1))
			run(fixture.Exec("DELETE FROM io_constant WHERE id=8", 1))
			run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"UPDATE", int64(9), int64(30), int64(30)}, []any{"DELETE", int64(8), int64(40), nil}))
		})
	})
}
