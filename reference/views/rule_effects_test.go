package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleEffects(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT,note TEXT DEFAULT 'seed')", 0), fixture.Exec("CREATE TABLE log(id INT,v INT,note TEXT)", 0), fixture.Exec("CREATE TABLE markers(label TEXT)", 0), fixture.Exec("CREATE TABLE counts(n BIGINT)", 0),
			fixture.Exec("CREATE RULE audit AS ON INSERT TO src DO INSERT INTO log VALUES(NEW.id,NEW.v,NEW.note)", 0), fixture.Exec("CREATE RULE marker AS ON INSERT TO src DO ALSO INSERT INTO markers VALUES('once')", 0), fixture.Exec("CREATE RULE observe AS ON INSERT TO src DO ALSO INSERT INTO counts SELECT count(*) FROM src", 0),
			fixture.Exec("INSERT INTO src(id,v) VALUES(1,10),(2,20)", 2),
			ruleRows("SELECT id,v,note FROM src ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), "seed"}, []any{int64(2), int64(20), "seed"}),
			ruleRows("SELECT id,v,note FROM log ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), "seed"}, []any{int64(2), int64(20), "seed"}),
			// VALUES is a query producer retained by the rule action even without NEW.
			ruleRows("SELECT label FROM markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}), ruleRows("SELECT n FROM counts", []string{"n"}, []string{"INT8"}, []any{int64(4)}),
			fixture.Exec("INSERT INTO src(id,v,note) VALUES($1,$2,$3)", 1, 3, 30, nil), fixture.Exec("INSERT INTO src(id,v) SELECT 4,40", 1),
			ruleRows("SELECT id,v,note FROM log ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), "seed"}, []any{int64(2), int64(20), "seed"}, []any{int64(3), int64(30), nil}, []any{int64(4), int64(40), "seed"}),
			fixture.Exec("DROP RULE audit ON src", 0), fixture.Exec("DROP RULE marker ON src", 0), fixture.Exec("DROP RULE observe ON src", 0), fixture.Exec("CREATE RULE no_insert AS ON INSERT TO src DO ALSO NOTHING", 0), fixture.Exec("INSERT INTO src VALUES(5,50,'five')", 1), fixture.Exec("CREATE RULE no_update AS ON UPDATE TO src DO INSTEAD NOTHING", 0), fixture.Exec("CREATE RULE no_delete AS ON DELETE TO src DO INSTEAD NOTHING", 0), fixture.Exec("UPDATE src SET v=0", 0), fixture.Exec("DELETE FROM src", 0), ruleRows("SELECT count(*) AS n FROM src", []string{"n"}, []string{"INT8"}, []any{int64(5)}))
		// The original SELECT producer and qualification also constrain actions.
		run(fixture.Exec("CREATE TABLE query_src(id INT,v INT)", 0),
			fixture.Exec("CREATE TABLE query_markers(label TEXT)", 0),
			fixture.Exec("CREATE RULE marker AS ON INSERT TO query_src DO INSERT INTO query_markers VALUES('once')", 0),
			fixture.Exec("INSERT INTO query_src SELECT id,v FROM src WHERE id<3", 2),
			ruleInts("SELECT id,v FROM query_src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}),
			ruleRows("SELECT label FROM query_markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}),
			fixture.Exec("INSERT INTO query_src SELECT id,v FROM src WHERE false", 0),
			ruleRows("SELECT label FROM query_markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}))
	})
}

func TestReferenceRuleConditions(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT,note TEXT)", 0), fixture.Exec("CREATE TABLE changes(id INT,old_v INT,new_v INT,note TEXT)", 0), fixture.Exec("INSERT INTO src VALUES(1,10,'x'),(2,20,'y'),(3,NULL,'z')", 3),
			fixture.Exec("CREATE RULE changed AS ON UPDATE TO src WHERE NEW.v<>OLD.v DO ALSO INSERT INTO changes VALUES(OLD.id,OLD.v,NEW.v,NEW.note)", 0), fixture.Exec("UPDATE src SET v=v+5 WHERE id=1", 1), fixture.Exec("UPDATE src SET note='new' WHERE id=2", 1), fixture.Exec("UPDATE src SET v=0 WHERE id=99", 0), fixture.Exec("UPDATE src SET v=30 WHERE id=3", 1),
			ruleRows("SELECT id,old_v,new_v,note FROM changes", []string{"id", "old_v", "new_v", "note"}, []string{"INT4", "INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), int64(15), "x"}),
			fixture.Exec("CREATE RULE deleted AS ON DELETE TO src DO INSERT INTO changes VALUES(OLD.id,OLD.v,NULL,OLD.note)", 0), fixture.Exec("DELETE FROM src WHERE id=2", 1),
			ruleRows("SELECT id,old_v,new_v,note FROM changes ORDER BY id", []string{"id", "old_v", "new_v", "note"}, []string{"INT4", "INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), int64(15), "x"}, []any{int64(2), int64(20), nil, "new"}),
			fixture.Exec("CREATE TABLE residual(id INT,v INT)", 0), fixture.Exec("CREATE RULE positive AS ON INSERT TO residual WHERE NEW.v>0 DO INSTEAD NOTHING", 0), fixture.Exec("CREATE RULE negative AS ON INSERT TO residual WHERE NEW.v< -5 DO INSTEAD NOTHING", 0), fixture.Exec("INSERT INTO residual VALUES(1,1),(2,-1),(3,NULL),(4,-10)", 2), ruleInts("SELECT id,v FROM residual ORDER BY id", []any{int64(2), int64(-1)}, []any{int64(3), nil}),
			fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE TABLE redirect(id INT,v INT)", 0), fixture.Exec("CREATE RULE route AS ON INSERT TO redirect DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("INSERT INTO redirect VALUES(1,10),(2,20)", 2), ruleInts("SELECT id,v FROM redirect"), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}), fixture.Exec("CREATE RULE update_route AS ON UPDATE TO sink DO INSTEAD INSERT INTO redirect VALUES(NEW.id,NEW.v)", 0), fixture.Exec("UPDATE sink SET v=v+1 WHERE id=1", 0), ruleInts("SELECT id,v FROM sink ORDER BY id,v", []any{int64(1), int64(10)}, []any{int64(1), int64(11)}, []any{int64(2), int64(20)}))
	})
}

func TestReferenceRuleOrdering(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE log(label TEXT,n BIGINT)", 0), fixture.Exec("CREATE SEQUENCE order_seq", 0), fixture.Exec("CREATE RULE z AS ON INSERT TO src DO INSERT INTO log VALUES('z',nextval('order_seq'))", 0), fixture.Exec("CREATE RULE a AS ON INSERT TO src DO (INSERT INTO log VALUES('a1',nextval('order_seq')); INSERT INTO log VALUES('a2',nextval('order_seq')))", 0), fixture.Exec("INSERT INTO src VALUES(1)", 1), fixture.Exec("ALTER RULE z ON src RENAME TO \"0_z\"", 0), fixture.Exec("INSERT INTO src VALUES(2)", 1), ruleRows("SELECT label,n FROM log ORDER BY n", []string{"label", "n"}, []string{"TEXT", "INT8"}, []any{"a1", int64(1)}, []any{"a2", int64(2)}, []any{"z", int64(3)}, []any{"z", int64(4)}, []any{"a1", int64(5)}, []any{"a2", int64(6)}))
	})
}
