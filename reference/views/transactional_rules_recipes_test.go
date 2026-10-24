package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesRecipes(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "private_rules")
	transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := transactionalRulesRun(t, p, b, c, nil)
		base(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
		q := "CREATE RULE r AS ON INSERT TO src WHERE abs(NEW.v)>1 DO ALSO INSERT INTO private_sink SELECT NEW.id,(abs(NEW.v))+v FROM private_source"
		handles := transactionalRulesHandles(t, p, c, held, q, transactionalRulesInsert)
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec("CREATE SCHEMA private_rules", 0))
		run(fixture.Exec("CREATE TABLE private_rules.original(id INT,oldv INT)", 0))
		run(fixture.Exec("ALTER TABLE private_rules.original RENAME COLUMN oldv TO v", 0))
		run(fixture.Exec("ALTER TABLE private_rules.original SET SCHEMA public", 0))
		run(fixture.Exec("ALTER TABLE original RENAME TO private_sink", 0))
		run(fixture.Exec("CREATE TABLE private_source(v INT)", 0))
		run(fixture.Exec("INSERT INTO private_source VALUES(7)", 1))
		run(fixture.Exec(q, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 1, -10))
		run(fixture.Exec(transactionalRulesInsert, 1, 2, 1))
		run(transactionalRulesRows("private_sink", []any{int64(1), int64(17)}))
		run(fixture.Exec("SAVEPOINT recipe", 0))
		run(fixture.Exec("ALTER RULE r ON src RENAME TO later", 0))
		run(fixture.Exec("DROP RULE later ON src", 0))
		run(fixture.Exec("ROLLBACK TO recipe", 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 3, -20))
		run(transactionalRulesRows("private_sink", []any{int64(1), int64(17)}, []any{int64(3), int64(27)}))
		run(fixture.Exec("RELEASE recipe", 0))
		finish(false)
		base(transactionalRulesNames())
		base(transactionalRulesRows("src"))
		base(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('private_sink','private_source')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
	t.Run("returning-and-events", func(t *testing.T) {
		transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			transactionalRulesSetup(base)
			create := "CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink1 VALUES(NEW.id,abs(NEW.v)) RETURNING id,v"
			update := "CREATE RULE u AS ON UPDATE TO sink1 DO ALSO INSERT INTO sink2 VALUES(OLD.id,abs(NEW.v))"
			remove := "CREATE RULE d AS ON DELETE TO sink1 DO ALSO INSERT INTO sink2 VALUES(OLD.id,OLD.v+1)"
			insert := "INSERT INTO src VALUES($1,$2) RETURNING id,v"
			handles := transactionalRulesHandles(t, p, c, held, create, update, remove, insert)
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, handles)
			run(fixture.Exec(create, 0))
			run(fixture.Exec(update, 0))
			run(fixture.Exec(remove, 0))
			run(fixture.Select(insert, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1, -10))
			run(transactionalRulesRows("src"))
			run(transactionalRulesRows("sink1", []any{int64(1), int64(10)}))
			run(fixture.Exec("UPDATE sink1 SET v=-20 WHERE id=1", 1))
			run(fixture.Exec("DELETE FROM sink1 WHERE id=1", 1))
			run(fixture.Select("SELECT id,v FROM sink2 ORDER BY v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-19)}, {int64(1), int64(20)}}))
			finish(true)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id,v FROM sink2 ORDER BY v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-19)}, {int64(1), int64(20)}}))
		})
	})
}

func TestReferenceTransactionalRulesSelectReturn(t *testing.T) {
	transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := transactionalRulesRun(t, p, b, c, nil)
		for _, q := range []string{"CREATE TABLE base(id INT)", "CREATE TABLE log(id INT)", "CREATE VIEW v AS SELECT id FROM base WITH LOCAL CHECK OPTION", "CREATE RULE keep AS ON INSERT TO v DO ALSO INSERT INTO log VALUES(NEW.id)"} {
			base(fixture.Exec(q, 0))
		}
		base(fixture.Exec("INSERT INTO base VALUES(1)", 1))
		identity := transactionalViewsIdentity(t, p, c, "v")
		q := "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT id+10 AS id FROM renamed_source"
		reader := "SELECT id FROM v ORDER BY id"
		// A genuinely held reader always participates, independently of utility route.
		handles := transactionalRulesHandles(t, p, c, true, reader)
		for query, h := range transactionalRulesHandles(t, p, c, held, q) {
			handles[query] = h
		}
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec("CREATE TABLE new_source(id INT)", 0))
		run(fixture.Exec("INSERT INTO new_source VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE new_source RENAME TO renamed_source", 0))
		run(fixture.Exec(q, 0))
		want := fixture.Select(reader, []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}})
		run(want)
		fixture.RunStep(t, p.Ctx, e, b.Recorder, want)
		if got := transactionalViewsIdentity(t, p, e, "v"); !reflect.DeepEqual(got, identity) {
			t.Fatal("view identity changed", identity, got)
		}
		run(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='v'::regclass ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, [][]any{{"_RETURN"}, {"keep"}}))
		run(fixture.Select("SELECT check_option FROM information_schema.views WHERE table_schema='public' AND table_name='v'", []string{"check_option"}, []string{"VARCHAR"}, [][]any{{"LOCAL"}}))
		finish(false)
		base(fixture.Select(reader, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: handles[reader], SQL: reader}, b.Recorder, fixture.Select(reader, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		base(fixture.ImplicitError("DROP RULE \"_RETURN\" ON v", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop rule _RETURN on view v because view v requires it", Hint: "You can drop view v instead."}))
		base(fixture.Exec("INSERT INTO v VALUES(2)", 1))
		base(fixture.Select("SELECT id FROM log", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
}
