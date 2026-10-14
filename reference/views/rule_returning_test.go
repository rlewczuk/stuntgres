package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleReturning(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE route AS ON INSERT TO src DO INSTEAD INSERT INTO sink SELECT NEW.id,NEW.v WHERE NEW.id>0 RETURNING id,v", 0), fixture.Select("INSERT INTO src VALUES($1,$2) RETURNING v+1 AS out", []string{"out"}, []string{"INT4"}, [][]any{{int64(11)}}, 1, 10), fixture.Select("INSERT INTO src VALUES($1,$2) RETURNING v+1 AS out", []string{"out"}, []string{"INT4"}, nil, -1, 20), fixture.Exec("INSERT INTO src VALUES(2,30)", 1), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(30)}))
	})
}

func TestReferenceRuleReturningErrors(t *testing.T) {
	cases := []struct{ name, sql, code, message string }{
		{"conditional", "CREATE RULE r AS ON INSERT TO src WHERE NEW.id>0 DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", "0A000", "RETURNING lists are not supported in conditional rules"},
		{"also", "CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", "0A000", "RETURNING lists are not supported in non-INSTEAD rules"},
		{"multiple", "CREATE RULE r AS ON INSERT TO src DO INSTEAD (INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v; INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v)", "0A000", "cannot have multiple RETURNING lists in a rule"},
		{"few", "CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id", "42P17", "RETURNING list has too few entries"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), ruleError(c.sql, c.code, c.message), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"))
			})
		})
	}
	t.Run("missing_provider", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD NOTHING", 0), fixture.ImplicitError("INSERT INTO src VALUES(1,10) RETURNING id", fixture.Diagnostic{Code: "0A000", Message: "cannot perform INSERT RETURNING on relation \"src\"", Hint: "You need an unconditional ON INSERT DO INSTEAD rule with a RETURNING clause."}), ruleInts("SELECT id,v FROM src"))
		})
	})
	t.Run("multiple_providers", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE a AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", 0), fixture.Exec("CREATE RULE z AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", 0), ruleError("INSERT INTO src VALUES(1,10) RETURNING id", "0A000", "cannot have RETURNING lists in multiple rules"), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"))
		})
	})
}

func TestReferenceRuleRestrictions(t *testing.T) {
	t.Run("recursive_preflight", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT,v INT)", 0), fixture.Exec("CREATE RULE a_prefix AS ON INSERT TO src DO INSERT INTO log VALUES(NEW.id,NEW.v)", 0), fixture.Exec("CREATE RULE z_cycle AS ON INSERT TO src WHERE false DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("CREATE RULE cycle AS ON INSERT TO sink DO INSERT INTO src VALUES(NEW.id,NEW.v)", 0), ruleError("INSERT INTO src VALUES(1,10)", "42P17", "infinite recursion detected in rules for relation \"src\""), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"), ruleInts("SELECT id,v FROM log"))
		})
	})
	for _, event := range []string{"INSERT", "UPDATE"} {
		t.Run(event, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), ruleError("INSERT INTO src VALUES(1,10) ON CONFLICT DO NOTHING", "0A000", "INSERT with ON CONFLICT clause cannot be used with table that has INSERT or UPDATE rules"), ruleError("INSERT INTO src VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v", "0A000", "INSERT with ON CONFLICT clause cannot be used with table that has INSERT or UPDATE rules"), ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"))
			})
		})
	}
	t.Run("legal_action_and_siblings", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE RULE a AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT DO NOTHING", 0), fixture.Exec("CREATE RULE z AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT DO NOTHING", 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(10)}))
		})
	})
	t.Run("nothing_conflict_edge", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD NOTHING", 0), fixture.Exec("INSERT INTO src VALUES(1,10) ON CONFLICT DO NOTHING", 0), ruleInts("SELECT id,v FROM src"))
		})
	})
}
