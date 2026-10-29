package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBeforeStatementRouting(t *testing.T) {
	for _, inherit := range []bool{false, true} {
		name := "partition"
		if inherit {
			name = "inheritance"
		}
		t.Run(name, func(t *testing.T) {
			beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				userOperatorExec(t, p, b, c, "CREATE TABLE bst_route_log(seq integer GENERATED ALWAYS AS IDENTITY,rel text,op text)", "CREATE FUNCTION bst_route() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_route_log(rel,op) VALUES(TG_TABLE_NAME,TG_OP); RETURN NULL; END$$")
				if inherit {
					userOperatorExec(t, p, b, c, "CREATE TABLE bst_parent(id integer,v integer)", "CREATE TABLE bst_lo() INHERITS(bst_parent)", "CREATE TABLE bst_hi() INHERITS(bst_parent)")
					run(fixture.Exec("INSERT INTO bst_lo VALUES(1,10)", 1))
					run(fixture.Exec("INSERT INTO bst_hi VALUES(11,20)", 1))
				} else {
					userOperatorExec(t, p, b, c, "CREATE TABLE bst_parent(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE bst_lo PARTITION OF bst_parent FOR VALUES FROM(0) TO(10)", "CREATE TABLE bst_hi PARTITION OF bst_parent FOR VALUES FROM(10) TO(30)")
					run(fixture.Exec("INSERT INTO bst_parent VALUES(1,10),(11,20)", 2))
				}
				for _, rel := range []string{"bst_parent", "bst_lo", "bst_hi"} {
					userOperatorExec(t, p, b, c, "CREATE TRIGGER same BEFORE INSERT OR UPDATE OR DELETE ON "+rel+" EXECUTE FUNCTION bst_route()")
				}
				run(fixture.Exec("UPDATE bst_parent SET v=v+1", 2))
				run(fixture.Exec("UPDATE bst_parent SET v=v WHERE id=$1", 0, 99))
				run(fixture.Exec("UPDATE bst_parent SET v=v WHERE id=$1", 0, 98))
				if !inherit {
					run(fixture.Exec("UPDATE bst_parent SET id=12 WHERE id=1", 1))
				}
				run(fixture.Exec("UPDATE bst_lo SET v=v", map[bool]int64{false: 0, true: 1}[inherit]))
				want := [][]any{{"bst_parent", "UPDATE"}, {"bst_parent", "UPDATE"}, {"bst_parent", "UPDATE"}}
				if !inherit {
					want = append(want, []any{"bst_parent", "UPDATE"})
				}
				want = append(want, []any{"bst_lo", "UPDATE"})
				run(fixture.Select("SELECT rel,op FROM bst_route_log ORDER BY seq", []string{"rel", "op"}, []string{"TEXT", "TEXT"}, want))
				run(fixture.Select("SELECT c.relname,t.tgname,t.tgparentid=0 AS independent FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relname IN('bst_parent','bst_lo','bst_hi') ORDER BY c.relname", []string{"relname", "tgname", "independent"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"bst_hi", "same", true}, {"bst_lo", "same", true}, {"bst_parent", "same", true}}))
			})
		})
	}
}
func TestReferenceTriggerBeforeStatementViewForeign(t *testing.T) {
	beforeStatementRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE bst_base(id integer,v integer)", "CREATE VIEW bst_view AS SELECT id,v FROM bst_base",
			"CREATE TABLE bst_view_log(seq integer GENERATED ALWAYS AS IDENTITY,rel text,phase text,op text,n bigint)",
			"CREATE FUNCTION bst_view_statement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_view_log(rel,phase,op,n) VALUES(TG_TABLE_NAME,TG_LEVEL,TG_OP,(SELECT count(*) FROM bst_base)); RETURN NULL; END$$",
			"CREATE FUNCTION bst_instead() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_view_log(rel,phase,op,n) VALUES(TG_TABLE_NAME,TG_LEVEL,TG_OP,(SELECT count(*) FROM bst_base)); IF TG_OP='INSERT' THEN INSERT INTO bst_base VALUES(NEW.id,NEW.v); RETURN NEW; ELSIF TG_OP='UPDATE' THEN UPDATE bst_base SET v=NEW.v WHERE id=OLD.id; RETURN NEW; ELSE DELETE FROM bst_base WHERE id=OLD.id; RETURN OLD; END IF; END$$",
			"CREATE TRIGGER s BEFORE INSERT OR UPDATE OR DELETE ON bst_base EXECUTE FUNCTION bst_view_statement()",
			"CREATE TRIGGER s BEFORE INSERT OR UPDATE OR DELETE ON bst_view EXECUTE FUNCTION bst_view_statement()",
			"CREATE TRIGGER r INSTEAD OF INSERT OR UPDATE OR DELETE ON bst_view FOR EACH ROW EXECUTE FUNCTION bst_instead()")
		run(fixture.Exec("INSERT INTO bst_view VALUES($1,$2)", 1, 1, 10))
		run(fixture.Exec("INSERT INTO bst_view VALUES($1,$2)", 1, 2, 20))
		run(fixture.Exec("UPDATE bst_view SET v=v+1", 2))
		run(fixture.Exec("DELETE FROM bst_view", 2))
		run(fixture.Exec("UPDATE bst_view SET v=v WHERE false", 0))
		run(fixture.Exec("DELETE FROM bst_view WHERE false", 0))
		want := [][]any{{"bst_view", "STATEMENT", "INSERT", int64(0)}, {"bst_view", "ROW", "INSERT", int64(0)}, {"bst_base", "STATEMENT", "INSERT", int64(0)}, {"bst_view", "STATEMENT", "INSERT", int64(1)}, {"bst_view", "ROW", "INSERT", int64(1)}, {"bst_base", "STATEMENT", "INSERT", int64(1)}, {"bst_view", "STATEMENT", "UPDATE", int64(2)}, {"bst_view", "ROW", "UPDATE", int64(2)}, {"bst_base", "STATEMENT", "UPDATE", int64(2)}, {"bst_view", "ROW", "UPDATE", int64(2)}, {"bst_base", "STATEMENT", "UPDATE", int64(2)}, {"bst_view", "STATEMENT", "DELETE", int64(2)}, {"bst_view", "ROW", "DELETE", int64(2)}, {"bst_base", "STATEMENT", "DELETE", int64(2)}, {"bst_view", "ROW", "DELETE", int64(1)}, {"bst_base", "STATEMENT", "DELETE", int64(1)}, {"bst_view", "STATEMENT", "UPDATE", int64(0)}, {"bst_view", "STATEMENT", "DELETE", int64(0)}}
		run(fixture.Select("SELECT rel,phase,op,n FROM bst_view_log ORDER BY seq", []string{"rel", "phase", "op", "n"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, want))
		userOperatorExec(t, p, b, c, "DROP TRIGGER r ON bst_view", "TRUNCATE bst_view_log")
		run(fixture.Exec("INSERT INTO bst_view VALUES(3,30)", 1))
		run(fixture.Select("SELECT rel,phase,op,n FROM bst_view_log ORDER BY seq", []string{"rel", "phase", "op", "n"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"bst_base", "STATEMENT", "INSERT", int64(0)}}))
	})
}
