package reference_test

import "testing"

func TestReferencePGDependLifecycleTypeReconstruction(t *testing.T) {
	schemaReferenceCleanup(t, "pgdep_retype")
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE SCHEMA pgdep_retype", "SET search_path TO pgdep_retype,public", "CREATE DOMAIN d1 AS INT", "CREATE DOMAIN d2 AS d1", "CREATE TABLE t(a d1,b INT DEFAULT 2,CONSTRAINT ck CHECK(a>0))", "CREATE INDEX ix ON t(a)")
		table := pgDependRelation(t, b, "t")
		ns := pgDependNamespace(t, b, table)
		idx := pgDependRelation(t, b, "ix")
		d1 := pgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", table)
		d2 := pgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=1247 AND refobjid=$1 AND deptype='n'", d1)
		pgDependExec(t, p, b, "ALTER TABLE t ALTER a TYPE d2")
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"), pgDependEdge(1259, table, 1, 1247, d2, 0, "n"))
		oldIndex := idx
		idx = pgDependRelation(t, b, "ix")
		if idx != oldIndex {
			pgDependBag(t, p, b, 1259, oldIndex)
		}
		pgDependBag(t, p, b, 1259, idx, pgDependEdge(1259, idx, 0, 1259, table, 1, "a"))
		ck := pgDependConstraint(t, b, table, "ck")
		pgDependBag(t, p, b, 2606, ck, pgDependEdge(2606, ck, 0, 1259, table, 1, "a"), pgDependEdge(2606, ck, 0, 1259, table, 1, "n"))
		pgDependExec(t, p, b, "ALTER TABLE t ALTER a TYPE INT")
		pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		pgDependExec(t, p, b, "DROP DOMAIN d1 CASCADE")
		pgDependBag(t, p, b, 1247, d1)
		pgDependBag(t, p, b, 1247, d2)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{}))
	})
}
func TestReferencePGDependExpressionCookingBranches(t *testing.T) {
	for _, truth := range []string{"TRUE", "FALSE"} {
		for _, owner := range []string{"default", "generated", "check", "index", "view"} {
			t.Run(truth+"/"+owner, func(t *testing.T) {
				p := newReferenceScenario(t)
				pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
					pgDependExec(t, p, b, "CREATE TABLE pgdep_branch_provider(a INT)")
					provider := pgDependRelation(t, b, "pgdep_branch_provider")
					expr := "CASE WHEN " + truth + " THEN 'pgdep_branch_provider'::regclass ELSE NULL::regclass END"
					consumer := "(" + expr + ") IS NOT NULL"
					if owner == "check" {
						consumer = "(" + consumer + ") AND a>0"
					}
					pgDependExec(t, p, b, pgDependOwnerStatements(owner, "pgdep_branch", consumer)...)
					table := pgDependRelation(t, b, "pgdep_branch")
					class, id := pgDependOwnerIdentity(t, b, owner, table)
					want := [][]any{pgDependOwnerAuto(t, b, owner, class, id, table)}
					if owner == "check" {
						want = [][]any{pgDependEdge(class, id, 0, 1259, table, 1, "a"), pgDependEdge(class, id, 0, 1259, table, 1, "n")}
					}
					want = append(want, pgDependEdge(class, id, 0, 1259, provider, 0, "n"))
					pgDependBag(t, p, b, class, id, want...)
				})
			})
		}
	}
}
