package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func insteadSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	insteadSetupTable(t, p, b, c, "CREATE TABLE io_base(id integer PRIMARY KEY,v integer)")
}
func insteadSetupTable(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		table,
		"CREATE TABLE io_log(op text,id integer,ov integer,nv integer)",
		"CREATE VIEW io_view AS SELECT id,v FROM io_base",
		`CREATE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN
 IF TG_OP='INSERT' THEN INSERT INTO io_base VALUES(NEW.id,NEW.v+1);
 ELSIF TG_OP='UPDATE' THEN UPDATE io_base SET id=NEW.id,v=NEW.v+1 WHERE id=OLD.id;
 ELSE DELETE FROM io_base WHERE id=OLD.id; END IF;
 INSERT INTO io_log VALUES(TG_OP,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v);
 IF TG_OP='DELETE' THEN OLD.v:=999; RETURN OLD; END IF;
 NEW.v:=NEW.v+100; RETURN NEW; END$$`,
		"CREATE TRIGGER body INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_body()")
}
func insteadLog(q string, rows ...[]any) fixture.Step {
	s := fixture.Select(q, []string{"op", "id", "ov", "nv"}, []string{"TEXT", "INT4", "INT4", "INT4"}, rows)
	s.Bag = true
	return s
}
func TestReferenceTriggerInsteadRows(t *testing.T) {
	for _, query := range []bool{false, true} {
		t.Run(map[bool]string{false: "counts", true: "returning"}[query], func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				if query {
					run(triggerRows("INSERT INTO io_view VALUES(1,10) RETURNING id,v", []any{int64(1), int64(110)}))
				} else {
					run(fixture.Exec("INSERT INTO io_view VALUES(1,10)", 1))
				}
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(11)}))
				if query {
					run(triggerRows("UPDATE io_view SET v=20 RETURNING id,v", []any{int64(1), int64(120)}))
				} else {
					run(fixture.Exec("UPDATE io_view SET v=20", 1))
				}
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(21)}))
				if query {
					run(triggerRows("DELETE FROM io_view RETURNING id,v", []any{int64(1), int64(21)}))
				} else {
					run(fixture.Exec("DELETE FROM io_view", 1))
				}
				run(triggerRows("SELECT id,v FROM io_base"))
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"INSERT", int64(1), nil, int64(10)}, []any{"UPDATE", int64(1), int64(11), int64(20)}, []any{"DELETE", int64(1), int64(21), nil}))
				userOperatorExec(t, p, b, c, "CREATE VIEW io_outer AS SELECT id,v FROM io_view", "CREATE TABLE io_identity(rel text)", "CREATE FUNCTION io_ident() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_identity VALUES(TG_TABLE_NAME); RETURN NEW; END$$", "CREATE TRIGGER a INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_ident()")
				run(triggerRows("INSERT INTO io_outer VALUES(3,30) RETURNING id,v", []any{int64(3), int64(130)}))
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(3), int64(31)}))
				run(fixture.Select("SELECT rel FROM io_identity", []string{"rel"}, []string{"TEXT"}, [][]any{{"io_view"}}))
				run(triggerRows("INSERT INTO io_view(id) VALUES(4) RETURNING id,v", []any{int64(4), nil}))
				run(triggerRows("INSERT INTO io_view VALUES(5,NULL) RETURNING id,v", []any{int64(5), nil}))
				run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(3), int64(31)}, []any{int64(4), nil}, []any{int64(5), nil}))
				run(insteadLog("SELECT op,id,ov,nv FROM io_log WHERE id IN (4,5)", []any{"INSERT", int64(4), nil, nil}, []any{"INSERT", int64(5), nil, nil}))
				// DEFAULT VALUES supplies a complete null NEW without inventing a base-table key.
				userOperatorExec(t, p, b, c, "CREATE TABLE io_default_new(id integer,v integer)", "CREATE VIEW io_default_null AS SELECT NULL::integer AS id,NULL::integer AS v", "CREATE FUNCTION io_default_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_default_new VALUES(NEW.id,NEW.v); RETURN NEW; END$$", "CREATE TRIGGER body INSTEAD OF INSERT ON io_default_null FOR EACH ROW EXECUTE FUNCTION io_default_body()")
				if query {
					run(triggerRows("INSERT INTO io_default_null DEFAULT VALUES RETURNING id,v", []any{nil, nil}))
				} else {
					run(fixture.Exec("INSERT INTO io_default_null DEFAULT VALUES", 1))
				}
				run(triggerRows("SELECT id,v FROM io_default_new", []any{nil, nil}))
				run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(3), int64(31)}, []any{int64(4), nil}, []any{int64(5), nil}))
			})
		})
	}
	for _, shape := range []struct{ name, sql string }{
		{"computed", "SELECT id,v+10 AS v FROM io_base"},
		{"joined", "SELECT b.id,b.v FROM io_base b JOIN (VALUES(1)) s(n) ON true"},
		{"aggregate", "SELECT min(id) AS id,sum(v)::integer AS v FROM io_base"},
		{"constant", "SELECT 1 AS id,10 AS v"},
	} {
		t.Run(shape.name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,10)", 1))
				userOperatorExec(t, p, b, c, "CREATE VIEW io_shape AS "+shape.sql, "CREATE TRIGGER body INSTEAD OF INSERT OR UPDATE OR DELETE ON io_shape FOR EACH ROW EXECUTE FUNCTION io_body()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				old := int64(10)
				if shape.name == "computed" {
					old = 20
				}
				run(triggerRows("UPDATE io_shape SET v=20 RETURNING id,v", []any{int64(1), int64(120)}))
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"UPDATE", int64(1), old, int64(20)}))
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(21)}))
				run(fixture.Exec("DELETE FROM io_log", 1))
				del := int64(21)
				if shape.name == "computed" {
					del = 31
				}
				if shape.name == "constant" {
					del = 10
				}
				run(triggerRows("DELETE FROM io_shape RETURNING id,v", []any{int64(1), del}))
				run(triggerRows("SELECT id,v FROM io_base"))
				run(triggerRows("INSERT INTO io_shape VALUES(2,30) RETURNING id,v", []any{int64(2), int64(130)}))
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(2), int64(31)}))
			})
		})
	}
	t.Run("event-boundary", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "DROP TRIGGER body ON io_view", "CREATE TRIGGER body INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_body()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO io_view VALUES(1,10)", 1))
			run(triggerRows("UPDATE io_view SET v=30 RETURNING id,v", []any{int64(1), int64(30)}))
			run(triggerRows("DELETE FROM io_view RETURNING id,v", []any{int64(1), int64(30)}))
			run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"INSERT", int64(1), nil, int64(10)}))
			userOperatorExec(t, p, b, c, "CREATE VIEW io_checked AS SELECT id,v FROM io_view WHERE v>0 WITH CASCADED CHECK OPTION")
			run(triggerRows("INSERT INTO io_checked VALUES(2,-10) RETURNING id,v", []any{int64(2), int64(90)}))
			run(triggerRows("SELECT id,v FROM io_base", []any{int64(2), int64(-9)}))
			userOperatorExec(t, p, b, c, "CREATE RULE io_rule AS ON DELETE TO io_view DO INSTEAD DELETE FROM io_base WHERE id=OLD.id")
			run(fixture.Exec("DELETE FROM io_view", 1))
			run(triggerRows("SELECT id,v FROM io_base"))
			userOperatorExec(t, p, b, c, "CREATE VIEW io_insert_only AS SELECT id,v FROM io_base UNION ALL SELECT id,v FROM io_base", "CREATE TRIGGER body INSTEAD OF INSERT ON io_insert_only FOR EACH ROW EXECUTE FUNCTION io_body()")
			run(triggerRows("INSERT INTO io_insert_only VALUES(8,80) RETURNING id,v", []any{int64(8), int64(180)}))
			for _, op := range []string{"update", "delete from"} {
				q := op + " io_insert_only"
				verb, event := "delete from", "DELETE"
				if op == "update" {
					q += " SET v=90"
					verb = "update"
					event = "UPDATE"
				}
				d := fixture.ControlsReferenceDiagnostic("55000", "cannot "+verb+` view "io_insert_only"`)
				d.Detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
				d.Hint = "To enable " + map[string]string{"UPDATE": "updating", "DELETE": "deleting from"}[event] + " the view, provide an INSTEAD OF " + event + " trigger or an unconditional ON " + event + " DO INSTEAD rule."
				run(fixture.ErrorDiagnostic(q, d))
			}
			run(triggerRows("SELECT id,v FROM io_base", []any{int64(8), int64(81)}))
		})
	})
}

func TestReferenceTriggerInsteadReturns(t *testing.T) {
	for _, mode := range []string{"none", "zero", "one", "many", "null-before", "null-after"} {
		t.Run(mode, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				body := ""
				switch mode {
				case "zero":
					body = "UPDATE io_base SET v=99 WHERE false;"
				case "one", "null-after":
					body = "INSERT INTO io_base VALUES(NEW.id,NEW.v);"
				case "many":
					body = "INSERT INTO io_base VALUES(NEW.id,NEW.v),(NEW.id+10,NEW.v);"
				}
				if mode == "null-after" {
					body += "INSERT INTO io_log VALUES('kept',NEW.id,NULL,NEW.v);"
				}
				ret := "NEW"
				if mode == "null-before" || mode == "null-after" {
					ret = "NULL"
				}
				userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN "+body+" RETURN "+ret+"; END$$")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				rows := [][]any{{int64(1), int64(10)}}
				n := int64(1)
				if ret == "NULL" {
					rows = nil
					n = 0
				}
				run(triggerRows("INSERT INTO io_view VALUES(1,10) RETURNING id,v", rows...))
				audit := [][]any(nil)
				if mode == "null-after" {
					audit = [][]any{{"kept", int64(1), nil, int64(10)}}
				}
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", audit...))
				want := [][]any(nil)
				if mode == "one" || mode == "null-after" || mode == "many" {
					want = append(want, []any{int64(1), int64(10)})
				}
				if mode == "many" {
					want = append(want, []any{int64(11), int64(10)})
				}
				run(triggerRows("SELECT id,v FROM io_base ORDER BY id", want...))
				run(fixture.Exec("DELETE FROM io_base", int64(len(want))))
				run(fixture.Exec("INSERT INTO io_view VALUES(1,10)", n))
				run(triggerRows("SELECT id,v FROM io_base ORDER BY id", want...))
				if mode == "null-after" {
					audit = append(audit, []any{"kept", int64(1), nil, int64(10)})
				}
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", audit...))
				run(triggerRows("UPDATE io_view SET v=v WHERE false RETURNING id,v"))
				run(fixture.Exec("DELETE FROM io_view WHERE false", 0))
				run(triggerRows("INSERT INTO io_view SELECT 9,9 WHERE false RETURNING id,v"))
			})
		})
	}
	t.Run("mixed-chain", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE FUNCTION io_gate() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=2 THEN RETURN NULL; END IF; RETURN NEW; END$$", "CREATE FUNCTION io_late() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_log VALUES('late',NEW.id,NULL,NEW.v); RETURN NEW; END$$", "CREATE TRIGGER middle INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_gate()", "CREATE TRIGGER z INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_late()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO io_view VALUES(1,10),(2,20),(3,30)", 2))
			run(fixture.Exec("ROLLBACK", 0))
			s := triggerRows("INSERT INTO io_view VALUES(1,10),(2,20),(3,30) RETURNING id,v", []any{int64(1), int64(110)}, []any{int64(3), int64(130)})
			s.Bag = true
			run(s)
			run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(21)}, []any{int64(3), int64(31)}))
			run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"INSERT", int64(1), nil, int64(10)}, []any{"INSERT", int64(2), nil, int64(20)}, []any{"INSERT", int64(3), nil, int64(30)}, []any{"late", int64(1), nil, int64(110)}, []any{"late", int64(3), nil, int64(130)}))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
			run(fixture.Exec("DELETE FROM io_view", 0))
			run(triggerRows("DELETE FROM io_view RETURNING id,v"))
		})
	})
}

func TestReferenceTriggerInsteadChains(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		insteadSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_base VALUES(1,5)", 1))
		userOperatorExec(t, p, b, c, "DROP TRIGGER body ON io_view",
			"CREATE TABLE io_frame(name text,op text,ov integer,nv integer,timing text,level text,rel text,sch text,oid_ok boolean,nargs integer,arg text)",
			`CREATE FUNCTION io_chain() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_frame VALUES(TG_NAME,TG_OP,OLD.v,NEW.v,TG_WHEN,TG_LEVEL,TG_TABLE_NAME,TG_TABLE_SCHEMA,TG_RELID='io_view'::regclass,TG_NARGS,TG_ARGV[0]); IF TG_OP='DELETE' THEN OLD.v:=999; RETURN OLD; END IF; IF TG_NAME='a' THEN NEW.v:=NEW.v+3; ELSE NEW.v:=NEW.v*2; END IF; RETURN NEW; END$$`,
			"CREATE TRIGGER z INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_chain('zarg')", "CREATE TRIGGER a INSTEAD OF INSERT OR UPDATE OR DELETE ON io_view FOR EACH ROW EXECUTE FUNCTION io_chain('aarg')")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(triggerRows("INSERT INTO io_view VALUES(2,5) RETURNING id,v", []any{int64(2), int64(16)}))
		run(triggerRows("UPDATE io_view SET v=5 RETURNING id,v", []any{int64(1), int64(16)}))
		run(triggerRows("DELETE FROM io_view RETURNING id,v", []any{int64(1), int64(5)}))
		rows := [][]any{}
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			for _, name := range []string{"a", "z"} {
				var ov, nv any = int64(5), int64(5)
				if op == "INSERT" {
					ov = nil
				}
				if op == "DELETE" {
					nv = nil
				} else if name == "z" {
					nv = int64(8)
				}
				rows = append(rows, []any{name, op, ov, nv, "INSTEAD OF", "ROW", "io_view", "public", true, int64(1), name + "arg"})
			}
		}
		s := fixture.Select("SELECT name,op,ov,nv,timing,level,rel,sch,oid_ok,nargs,arg FROM io_frame", []string{"name", "op", "ov", "nv", "timing", "level", "rel", "sch", "oid_ok", "nargs", "arg"}, []string{"TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "INT4", "TEXT"}, rows)
		s.Bag = true
		run(s)
		run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(5)}))
	})
}
