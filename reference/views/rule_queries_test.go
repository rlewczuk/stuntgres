package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleConditionsQueryShapes(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held,
			fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT,v INT)", 0), fixture.Exec("CREATE TABLE input(id INT,v INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,10),(2,20)", 2), fixture.Exec("INSERT INTO input VALUES(1,100),(2,200)", 2), fixture.Exec("CREATE RULE r AS ON UPDATE TO src DO INSERT INTO log SELECT OLD.id,NEW.v", 0), fixture.Exec("UPDATE src AS s SET v=i.v FROM input AS i WHERE s.id=i.id", 2), ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(100)}, []any{int64(2), int64(200)}), ruleInts("SELECT id,v FROM log ORDER BY id", []any{int64(1), int64(100)}, []any{int64(2), int64(200)}), fixture.Exec("CREATE RULE d AS ON DELETE TO src DO INSERT INTO log SELECT OLD.id,OLD.v+1", 0), fixture.Exec("DELETE FROM src AS s USING input AS i WHERE s.id=i.id AND i.id=1", 1), ruleInts("SELECT id,v FROM src", []any{int64(2), int64(200)}), ruleInts("SELECT id,v FROM log ORDER BY id,v", []any{int64(1), int64(100)}, []any{int64(1), int64(101)}, []any{int64(2), int64(200)}))
	})
	t.Run("post_substitution_order", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held,
				fixture.Exec("CREATE SEQUENCE rule_values", 0),
				fixture.Exec("CREATE TABLE src(id INT,v INT DEFAULT nextval('rule_values'))", 0),
				fixture.Exec("CREATE TABLE log(id INT,v INT)", 0),
				fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO log SELECT NEW.id,NEW.v+10 AS v ORDER BY v LIMIT 1", 0),
				fixture.Exec("INSERT INTO src(id) VALUES(1),(2)", 2),
				ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(1)}, []any{int64(2), int64(2)}),
				ruleInts("SELECT id,v FROM log", []any{int64(1), int64(13)}),
				ruleRows("SELECT last_value FROM rule_values", []string{"last_value"}, []string{"INT8"}, []any{int64(4)}),
				fixture.Exec("DROP RULE r ON src", 0),
				fixture.Exec("CREATE RULE u AS ON UPDATE TO src DO INSERT INTO log SELECT OLD.id,NEW.v ORDER BY NEW.v DESC LIMIT 1", 0),
				fixture.Exec("UPDATE src SET v=v+10", 2),
				ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(12)}),
				ruleInts("SELECT id,v FROM log ORDER BY id", []any{int64(1), int64(13)}, []any{int64(2), int64(12)}))
		})
	})
	for _, tc := range []struct {
		name, query      string
		second, frontier int64
	}{
		{"explicit_macro", "INSERT INTO src VALUES(1,nextval('macro_seq'))", 2, 2},
		{"default_macro", "INSERT INTO src(id) VALUES(1)", 2, 2},
		{"bounded_values_subquery", "INSERT INTO src VALUES(1,nextval('macro_seq')) ORDER BY 1 LIMIT 1", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held,
					fixture.Exec("CREATE SEQUENCE macro_seq", 0),
					fixture.Exec("CREATE TABLE src(id INT,v BIGINT DEFAULT nextval('macro_seq'))", 0),
					fixture.Exec("CREATE TABLE log(id INT,v BIGINT,w BIGINT)", 0),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO log VALUES(NEW.id,NEW.v,NEW.v)", 0),
					fixture.Exec(tc.query, 1),
					ruleRows("SELECT id,v,w FROM log", []string{"id", "v", "w"}, []string{"INT4", "INT8", "INT8"}, []any{int64(1), int64(1), tc.second}),
					ruleRows("SELECT id,v FROM src", []string{"id", "v"}, []string{"INT4", "INT8"}),
					ruleRows("SELECT last_value FROM macro_seq", []string{"last_value"}, []string{"INT8"}, []any{tc.frontier}))
			})
		})
	}
	t.Run("select_producer_demand", func(t *testing.T) {
		t.Run("repeated_output", func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held,
					fixture.Exec("CREATE SEQUENCE producer_seq", 0),
					fixture.Exec("CREATE TABLE input(id INT)", 0),
					fixture.Exec("INSERT INTO input VALUES(1),(2)", 2),
					fixture.Exec("CREATE TABLE src(id INT,v BIGINT)", 0),
					fixture.Exec("CREATE TABLE log(id INT,v BIGINT,w BIGINT)", 0),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO log SELECT NEW.id,NEW.v,NEW.v", 0),
					fixture.Exec("INSERT INTO src SELECT id,nextval('producer_seq') FROM input", 2),
					ruleRows("SELECT id,v,w FROM log ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "INT8", "INT8"}, []any{int64(1), int64(1), int64(1)}, []any{int64(2), int64(2), int64(2)}),
					ruleRows("SELECT id,v FROM src", []string{"id", "v"}, []string{"INT4", "INT8"}),
					ruleRows("SELECT last_value FROM producer_seq", []string{"last_value"}, []string{"INT8"}, []any{int64(2)}))
			})
		})
		t.Run("unused_volatile", func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held,
					fixture.Exec("CREATE SEQUENCE producer_seq", 0),
					fixture.Exec("CREATE TABLE input(id INT)", 0),
					fixture.Exec("INSERT INTO input VALUES(1),(2)", 2),
					fixture.Exec("CREATE TABLE src(id INT,v BIGINT)", 0),
					fixture.Exec("CREATE TABLE markers(label TEXT)", 0),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO markers VALUES('once')", 0),
					fixture.Exec("INSERT INTO src SELECT id,nextval('producer_seq') FROM input", 2),
					ruleRows("SELECT label FROM markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}),
					ruleRows("SELECT id,v FROM src", []string{"id", "v"}, []string{"INT4", "INT8"}),
					ruleRows("SELECT last_value FROM producer_seq", []string{"last_value"}, []string{"INT8"}, []any{int64(2)}))
			})
		})
		t.Run("unused_error", func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held,
					fixture.Exec("CREATE TABLE input(id INT)", 0),
					fixture.Exec("INSERT INTO input VALUES(1),(2)", 2),
					fixture.Exec("CREATE TABLE src(id INT,v INT)", 0),
					fixture.Exec("CREATE TABLE markers(label TEXT)", 0),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO markers VALUES('once')", 0),
					fixture.Exec("INSERT INTO src SELECT id,1/(id-id) FROM input", 2),
					ruleRows("SELECT label FROM markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}),
					ruleInts("SELECT id,v FROM src"))
			})
		})
		t.Run("unused_bounds", func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held,
					fixture.Exec("CREATE SEQUENCE producer_seq", 0),
					fixture.Exec("CREATE TABLE input(id INT)", 0),
					fixture.Exec("INSERT INTO input VALUES(1),(2)", 2),
					fixture.Exec("CREATE TABLE src(id INT,v BIGINT)", 0),
					fixture.Exec("CREATE TABLE markers(label TEXT)", 0),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO markers VALUES('once')", 0),
					fixture.Exec("INSERT INTO src SELECT id,nextval('producer_seq') FROM input WHERE false", 0),
					fixture.Exec("INSERT INTO src SELECT id,1/(id-id) FROM input LIMIT 0", 0),
					ruleRows("SELECT label FROM markers", []string{"label"}, []string{"TEXT"}),
					ruleRows("SELECT last_value,is_called FROM producer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), false}),
					fixture.Exec("INSERT INTO src SELECT id,nextval('producer_seq') FROM input ORDER BY id LIMIT 1", 1),
					fixture.Exec("INSERT INTO src SELECT id,1/(id-id) FROM input ORDER BY id LIMIT 1", 1),
					ruleRows("SELECT label FROM markers", []string{"label"}, []string{"TEXT"}, []any{"once"}, []any{"once"}),
					ruleRows("SELECT id,v FROM src", []string{"id", "v"}, []string{"INT4", "INT8"}),
					ruleRows("SELECT last_value,is_called FROM producer_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), true}))
			})
		})
	})
}

func TestReferenceRuleOrderingCountAuthority(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held,
			fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE z AS ON INSERT TO src DO INSTEAD INSERT INTO sink SELECT NEW.id,NEW.v WHERE false", 0), fixture.Exec("CREATE RULE a AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 0), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(10)}), fixture.Exec("ALTER RULE z ON src RENAME TO \"0_z\"", 0), fixture.Exec("INSERT INTO src VALUES(2,20)", 1), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
	})
}

func TestReferenceRuleReturningMutationProviders(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held,
			fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), fixture.Exec("INSERT INTO sink VALUES(1,20)", 1), fixture.Exec("CREATE RULE u AS ON UPDATE TO src DO INSTEAD UPDATE sink SET v=NEW.v WHERE id=OLD.id RETURNING id,v", 0), ruleRows("UPDATE src SET v=v+5 RETURNING v+1 AS out", []string{"out"}, []string{"INT4"}, []any{int64(16)}), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(15)}), fixture.Exec("CREATE RULE d AS ON DELETE TO src DO INSTEAD DELETE FROM sink WHERE id=OLD.id RETURNING id,v", 0), ruleRows("DELETE FROM src RETURNING v AS out", []string{"out"}, []string{"INT4"}, []any{int64(15)}), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleInts("SELECT id,v FROM sink"))
	})
}

func TestReferenceRuleAtomicitySavepoint(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT NOT NULL)", 0), fixture.Exec("CREATE TABLE prior(id INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NULL)", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, s := range []fixture.Step{fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO prior VALUES(9)", 1), fixture.Exec("SAVEPOINT before_rule", 0), fixture.ImplicitError("INSERT INTO src VALUES(1,10)", fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"sink\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "sink", Column: "v"}), fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}), fixture.Exec("ROLLBACK TO before_rule", 0), fixture.Exec("COMMIT", 0)} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		ruleRun(t, p, b, held, ruleRows("SELECT id FROM prior", []string{"id"}, []string{"INT4"}, []any{int64(9)}), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"))
	})
}

func TestReferenceRuleFeatureSmoke(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT,note TEXT DEFAULT 'seed')", 0), fixture.Exec("CREATE TABLE audit(id INT,v INT,note TEXT)", 0), fixture.Exec("CREATE RULE audit AS ON INSERT TO src DO INSERT INTO audit VALUES(NEW.id,NEW.v,NEW.note)", 0), fixture.Exec("INSERT INTO src(id,v) VALUES(1,10),(2,20)", 2), ruleRows("SELECT id,v,note FROM audit ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), "seed"}, []any{int64(2), int64(20), "seed"}), fixture.Exec("CREATE TABLE routed(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE skip AS ON INSERT TO routed WHERE NEW.v>0 DO INSTEAD NOTHING", 0), fixture.Exec("INSERT INTO routed VALUES(1,1),(2,-1),(3,NULL)", 2), ruleInts("SELECT id,v FROM routed ORDER BY id", []any{int64(2), int64(-1)}, []any{int64(3), nil}), fixture.Exec("DROP RULE skip ON routed", 0), fixture.Exec("CREATE RULE route AS ON INSERT TO routed DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", 0), ruleRows("INSERT INTO routed VALUES(4,10) RETURNING v+1 AS out", []string{"out"}, []string{"INT4"}, []any{int64(11)}), fixture.Exec("CREATE VIEW v AS SELECT id FROM src", 0), fixture.Exec("CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT id+10 AS id FROM src", 0), ruleRows("SELECT id FROM v ORDER BY id", []string{"id"}, []string{"INT4"}, []any{int64(11)}, []any{int64(12)}), fixture.Exec("CREATE RULE loop AS ON INSERT TO sink DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), ruleError("INSERT INTO routed VALUES(5,50)", "42P17", "infinite recursion detected in rules for relation \"sink\""), ruleInts("SELECT id,v FROM sink", []any{int64(4), int64(10)}), fixture.Exec("DROP RULE loop ON sink", 0), fixture.Exec("ALTER RULE route ON routed RENAME TO renamed", 0), fixture.Exec("INSERT INTO routed VALUES(6,60)", 1), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(4), int64(10)}, []any{int64(6), int64(60)}))
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
			ruleRun(t, p, b, held, ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(4), int64(10)}, []any{int64(6), int64(60)}), ruleRows("SELECT id,v,note FROM audit ORDER BY id", []string{"id", "v", "note"}, []string{"INT4", "INT4", "TEXT"}, []any{int64(1), int64(10), "seed"}, []any{int64(2), int64(20), "seed"}))
		}
		ruleRun(t, p, b, held, fixture.Exec("INSERT INTO routed VALUES(7,70)", 1), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(4), int64(10)}, []any{int64(6), int64(60)}, []any{int64(7), int64(70)}))
	})
}
