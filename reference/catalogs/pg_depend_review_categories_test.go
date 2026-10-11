package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependReviewStandaloneUnique(t *testing.T) {
	p := fixture.NewScenario(t)
	pgDependReviewBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE standalone_unique(a INT,b INT)", "CREATE UNIQUE INDEX standalone_unique_index ON standalone_unique(a) INCLUDE(b)")
		table := fixture.PgDependRelation(t, b, "standalone_unique")
		index := fixture.PgDependRelation(t, b, "standalone_unique_index")
		ns := fixture.PgDependNamespace(t, b, table)
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		fixture.PgDependBag(t, p, b, 1259, index, fixture.PgDependEdge(1259, index, 0, 1259, table, 1, "a"), fixture.PgDependEdge(1259, index, 0, 1259, table, 2, "a"))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT conname FROM pg_catalog.pg_constraint WHERE conrelid=$1", []string{"conname"}, []string{"NAME"}, [][]any{}, table)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO standalone_unique VALUES(1,10),(2,20)", 2))
		fixture.PgDependExec(t, p, b, "DROP INDEX standalone_unique_index")
		fixture.PgDependBag(t, p, b, 1259, index)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,b FROM standalone_unique ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	})
}

func TestReferencePGDependReviewSetDomainTargets(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_review_set")
	p := fixture.NewScenario(t)
	pgDependReviewBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_review_set", "SET search_path TO pgdep_review_set,public", "CREATE DOMAIN d AS INT", "CREATE TABLE set_left(a d)", "CREATE TABLE set_right(a d)",
			"CREATE VIEW set_vars AS SELECT a FROM set_left UNION ALL SELECT a FROM set_right",
			"CREATE VIEW set_casts AS SELECT NULL::d AS a UNION ALL SELECT NULL::d AS a")
		left := fixture.PgDependRelation(t, b, "set_left")
		right := fixture.PgDependRelation(t, b, "set_right")
		ns := fixture.PgDependNamespace(t, b, left)
		domain := fixture.PgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", left)
		fixture.PgDependBag(t, p, b, 1247, domain, fixture.PgDependEdge(1247, domain, 0, 2615, ns, 0, "n"))
		for _, table := range []int64{left, right} {
			fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, table, 1, 1247, domain, 0, "n"))
		}
		vars := fixture.PgDependRelation(t, b, "set_vars")
		casts := fixture.PgDependRelation(t, b, "set_casts")
		varsRule := fixture.PgDependRule(t, b, vars)
		castsRule := fixture.PgDependRule(t, b, casts)
		for _, view := range []int64{vars, casts} {
			fixture.PgDependBag(t, p, b, 1259, view, fixture.PgDependEdge(1259, view, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, view, 1, 1247, domain, 0, "n"))
		}
		// Same-domain Vars preserve the view's declared output type but do not
		// invent a rule-level target-type reference. Actual casts do reference d.
		fixture.PgDependBag(t, p, b, 2618, varsRule, fixture.PgDependEdge(2618, varsRule, 0, 1259, vars, 0, "i"), fixture.PgDependEdge(2618, varsRule, 0, 1259, left, 1, "n"), fixture.PgDependEdge(2618, varsRule, 0, 1259, right, 1, "n"))
		fixture.PgDependBag(t, p, b, 2618, castsRule, fixture.PgDependEdge(2618, castsRule, 0, 1259, casts, 0, "i"), fixture.PgDependEdge(2618, castsRule, 0, 1247, domain, 0, "n"))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO set_left VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO set_right VALUES(2)", 1))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT a FROM set_vars ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			step = fixture.Select("SELECT a FROM set_casts", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}})
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
	})
}
