package reference_test

import "testing"

func TestReferencePGDependReviewStandaloneUnique(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependReviewBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE standalone_unique(a INT,b INT)", "CREATE UNIQUE INDEX standalone_unique_index ON standalone_unique(a) INCLUDE(b)")
		table := pgDependRelation(t, b, "standalone_unique")
		index := pgDependRelation(t, b, "standalone_unique_index")
		ns := pgDependNamespace(t, b, table)
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		pgDependBag(t, p, b, 1259, index, pgDependEdge(1259, index, 0, 1259, table, 1, "a"), pgDependEdge(1259, index, 0, 1259, table, 2, "a"))
		for _, prepared := range []bool{false, true} {
			step := referenceSelect("SELECT conname FROM pg_catalog.pg_constraint WHERE conrelid=$1", []string{"conname"}, []string{"NAME"}, [][]any{}, table)
			step.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO standalone_unique VALUES(1,10),(2,20)", 2))
		pgDependExec(t, p, b, "DROP INDEX standalone_unique_index")
		pgDependBag(t, p, b, 1259, index)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,b FROM standalone_unique ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
	})
}

func TestReferencePGDependReviewSetDomainTargets(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_review_set")
	p := newReferenceScenario(t)
	pgDependReviewBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SCHEMA pgdep_review_set", "SET search_path TO pgdep_review_set,public", "CREATE DOMAIN d AS INT", "CREATE TABLE set_left(a d)", "CREATE TABLE set_right(a d)",
			"CREATE VIEW set_vars AS SELECT a FROM set_left UNION ALL SELECT a FROM set_right",
			"CREATE VIEW set_casts AS SELECT NULL::d AS a UNION ALL SELECT NULL::d AS a")
		left := pgDependRelation(t, b, "set_left")
		right := pgDependRelation(t, b, "set_right")
		ns := pgDependNamespace(t, b, left)
		domain := pgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", left)
		pgDependBag(t, p, b, 1247, domain, pgDependEdge(1247, domain, 0, 2615, ns, 0, "n"))
		for _, table := range []int64{left, right} {
			pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"), pgDependEdge(1259, table, 1, 1247, domain, 0, "n"))
		}
		vars := pgDependRelation(t, b, "set_vars")
		casts := pgDependRelation(t, b, "set_casts")
		varsRule := pgDependRule(t, b, vars)
		castsRule := pgDependRule(t, b, casts)
		for _, view := range []int64{vars, casts} {
			pgDependBag(t, p, b, 1259, view, pgDependEdge(1259, view, 0, 2615, ns, 0, "n"), pgDependEdge(1259, view, 1, 1247, domain, 0, "n"))
		}
		// Same-domain Vars preserve the view's declared output type but do not
		// invent a rule-level target-type reference. Actual casts do reference d.
		pgDependBag(t, p, b, 2618, varsRule, pgDependEdge(2618, varsRule, 0, 1259, vars, 0, "i"), pgDependEdge(2618, varsRule, 0, 1259, left, 1, "n"), pgDependEdge(2618, varsRule, 0, 1259, right, 1, "n"))
		pgDependBag(t, p, b, 2618, castsRule, pgDependEdge(2618, castsRule, 0, 1259, casts, 0, "i"), pgDependEdge(2618, castsRule, 0, 1247, domain, 0, "n"))
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO set_left VALUES(1)", 1))
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO set_right VALUES(2)", 1))
		for _, prepared := range []bool{false, true} {
			step := referenceSelect("SELECT a FROM set_vars ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			step.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
			step = referenceSelect("SELECT a FROM set_casts", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}})
			step.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, step)
		}
	})
}
