package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependLifecycleTypeReconstruction(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgdep_retype")
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_retype", "SET search_path TO pgdep_retype,public", "CREATE DOMAIN d1 AS INT", "CREATE DOMAIN d2 AS d1", "CREATE TABLE t(a d1,b INT DEFAULT 2,CONSTRAINT ck CHECK(a>0))", "CREATE INDEX ix ON t(a)")
		table := fixture.PgDependRelation(t, b, "t")
		ns := fixture.PgDependNamespace(t, b, table)
		idx := fixture.PgDependRelation(t, b, "ix")
		d1 := fixture.PgDependID(t, b, "SELECT refobjid FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND objsubid=1 AND refclassid=1247", table)
		d2 := fixture.PgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=1247 AND refobjid=$1 AND deptype='n'", d1)
		fixture.PgDependExec(t, p, b, "ALTER TABLE t ALTER a TYPE d2")
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"), fixture.PgDependEdge(1259, table, 1, 1247, d2, 0, "n"))
		oldIndex := idx
		idx = fixture.PgDependRelation(t, b, "ix")
		if idx != oldIndex {
			fixture.PgDependBag(t, p, b, 1259, oldIndex)
		}
		fixture.PgDependBag(t, p, b, 1259, idx, fixture.PgDependEdge(1259, idx, 0, 1259, table, 1, "a"))
		ck := fixture.PgDependConstraint(t, b, table, "ck")
		fixture.PgDependBag(t, p, b, 2606, ck, fixture.PgDependEdge(2606, ck, 0, 1259, table, 1, "a"), fixture.PgDependEdge(2606, ck, 0, 1259, table, 1, "n"))
		fixture.PgDependExec(t, p, b, "ALTER TABLE t ALTER a TYPE INT")
		fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
		fixture.PgDependExec(t, p, b, "DROP DOMAIN d1 CASCADE")
		fixture.PgDependBag(t, p, b, 1247, d1)
		fixture.PgDependBag(t, p, b, 1247, d2)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{}))
	})
}
func TestReferencePGDependExpressionCookingBranches(t *testing.T) {
	for _, truth := range []string{"TRUE", "FALSE"} {
		for _, owner := range []string{"default", "generated", "check", "index", "view"} {
			t.Run(truth+"/"+owner, func(t *testing.T) {
				p := fixture.NewScenario(t)
				fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
					fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_branch_provider(a INT)")
					provider := fixture.PgDependRelation(t, b, "pgdep_branch_provider")
					expr := "CASE WHEN " + truth + " THEN 'pgdep_branch_provider'::regclass ELSE NULL::regclass END"
					consumer := "(" + expr + ") IS NOT NULL"
					if owner == "check" {
						consumer = "(" + consumer + ") AND a>0"
					}
					fixture.PgDependExec(t, p, b, pgDependOwnerStatements(owner, "pgdep_branch", consumer)...)
					table := fixture.PgDependRelation(t, b, "pgdep_branch")
					class, id := pgDependOwnerIdentity(t, b, owner, table)
					want := [][]any{pgDependOwnerAuto(t, b, owner, class, id, table)}
					if owner == "check" {
						want = [][]any{fixture.PgDependEdge(class, id, 0, 1259, table, 1, "a"), fixture.PgDependEdge(class, id, 0, 1259, table, 1, "n")}
					}
					want = append(want, fixture.PgDependEdge(class, id, 0, 1259, provider, 0, "n"))
					fixture.PgDependBag(t, p, b, class, id, want...)
				})
			})
		}
	}
}
