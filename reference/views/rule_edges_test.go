package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleDDLNotices(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("DROP RULE IF EXISTS missing ON src", 0)} {
			s.Prepared = held
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "rule \"missing\" for relation \"src\" does not exist, skipping"}})
	})
}

func TestReferenceRuleSelectReturnRecursion(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE base(id INT)", 0), fixture.Exec("CREATE VIEW a AS SELECT id FROM base", 0), fixture.Exec("CREATE VIEW b AS SELECT id FROM a", 0), fixture.Exec("CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO a DO INSTEAD SELECT id FROM b", 0), ruleError("SELECT id FROM a", "42P17", "infinite recursion detected in rules for relation \"a\""))
	})
}

func TestReferenceRuleRestrictionsMergeMaterialized(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON UPDATE TO src DO NOTHING", 0), fixture.ImplicitError("MERGE INTO src USING (VALUES(1,10)) AS input(id,v) ON src.id=input.id WHEN NOT MATCHED THEN INSERT VALUES(input.id,input.v)", fixture.Diagnostic{Code: "0A000", Message: "cannot execute MERGE on relation \"src\"", Detail: "MERGE is not supported for relations with rules."}), ruleInts("SELECT id,v FROM src"), fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM src", 0), ruleError("CREATE RULE bad AS ON INSERT TO mv DO NOTHING", "0A000", "rules on materialized views are not supported"))
	})
}

func TestReferenceRuleDependenciesEdges(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), ruleRows("SELECT d.deptype FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid=2618 AND r.ev_class='src'::regclass::oid AND d.refclassid=1259 AND d.refobjid='src'::regclass::oid AND d.refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, []any{"a"}), ruleRows("SELECT DISTINCT d.refobjsubid FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid=2618 AND r.ev_class='src'::regclass::oid AND d.refclassid=1259 AND d.refobjid='sink'::regclass::oid AND d.deptype='n' ORDER BY d.refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, []any{int64(1)}, []any{int64(2)}), fixture.Exec("ALTER TABLE sink DROP COLUMN v CASCADE", 0), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}))
	})
}
