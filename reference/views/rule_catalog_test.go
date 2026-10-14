package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleDDL(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE other(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO other DO NOTHING", 0), ruleError("CREATE RULE r AS ON INSERT TO src DO NOTHING", "42710", "rule \"r\" for relation \"src\" already exists"), fixture.Exec("CREATE RULE z AS ON DELETE TO src DO NOTHING", 0), ruleError("ALTER RULE r ON src RENAME TO z", "42710", "rule \"z\" for relation \"src\" already exists"), ruleError("ALTER RULE missing ON src RENAME TO absent", "42704", "rule \"missing\" for relation \"src\" does not exist"))
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='src'::regclass::oid AND rulename='r'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v+1)", 0), fixture.Exec("ALTER RULE r ON src RENAME TO renamed", 0))
		var after int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='src'::regclass::oid AND rulename='renamed'").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if oid != after {
			t.Fatalf("rule identity changed: %d -> %d", oid, after)
		}
		run(ruleRows("SELECT rulename,ev_type,ev_enabled,is_instead FROM pg_rewrite WHERE ev_class='src'::regclass::oid ORDER BY rulename", []string{"rulename", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "CHAR", "CHAR", "BOOL"}, []any{"renamed", "3", "O", true}, []any{"z", "4", "O", false}), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(11)}), fixture.Exec("DROP RULE renamed ON src", 0), ruleError("DROP RULE missing ON src", "42704", "rule \"missing\" for relation \"src\" does not exist"))
	})
}

func TestReferenceRuleDependencies(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held,
			fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink2(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink1 VALUES(NEW.id,NEW.v)", 0), fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.id,NEW.v)", 0), fixture.Exec("DROP TABLE sink1", 0), fixture.ImplicitError("DROP TABLE sink2", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table sink2 because other objects depend on it", Detail: "rule r on table src depends on table sink2", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), fixture.Exec("DROP TABLE sink2 CASCADE", 0), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}), fixture.Exec("INSERT INTO src VALUES(2,20)", 1), ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
	})
}

func TestReferenceRuleSelectReturn(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE base(id INT)", 0), fixture.Exec("INSERT INTO base VALUES(1)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM base", 0))
		stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var relation, rule int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT ev_class::bigint,oid::bigint FROM pg_rewrite WHERE ev_class='v'::regclass::oid").Scan(&relation, &rule); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT id+10 AS id FROM base", 0), ruleRows("SELECT id FROM v", []string{"id"}, []string{"INT4"}, []any{int64(11)}))
		rows, err := stmt.QueryContext(p.Ctx)
		fixture.DeleteReferenceConsume(t, b.PG, rows, err, ruleRows("", []string{"id"}, []string{"INT4"}, []any{int64(11)}))
		var newRelation, newRule int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT ev_class::bigint,oid::bigint FROM pg_rewrite WHERE ev_class='v'::regclass::oid").Scan(&newRelation, &newRule); err != nil {
			t.Fatal(err)
		}
		if relation != newRelation || rule != newRule {
			t.Fatal("SELECT replacement changed relation/rule identity")
		}
		run(fixture.ImplicitError("CREATE RULE bad AS ON SELECT TO base DO INSTEAD SELECT id FROM base", fixture.Diagnostic{Code: "42809", Message: "relation \"base\" cannot have ON SELECT rules", Detail: "This operation is not supported for tables."}), ruleError("ALTER RULE \"_RETURN\" ON v RENAME TO x", "42P17", "renaming an ON SELECT rule is not allowed"), fixture.ImplicitError("DROP RULE \"_RETURN\" ON v CASCADE", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop rule _RETURN on view v because view v requires it", Hint: "You can drop view v instead."}), ruleRows("SELECT id FROM v", []string{"id"}, []string{"INT4"}, []any{int64(11)}))
	})
}
