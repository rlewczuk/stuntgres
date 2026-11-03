package reference_test

import (
	"stuntgres/reference/fixture"
)

// These small batches are below autovacuum's default minimum tuple threshold
// and the GIN pending-list limit. Each scenario owns a fresh table and index.
func ginMaintSeed(run func(fixture.Step), off bool) {
	run(fixture.Exec("CREATE TABLE gin_maint_oracle(id integer,a integer[])", 0))
	q := "CREATE INDEX gin_maint_oracle_i ON gin_maint_oracle USING gin(a)"
	if off {
		q += " WITH(fastupdate=off)"
	}
	run(fixture.Exec(q, 0))
	run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(1,'{1,2,2}')", 1))
}

func ginMaintPositive(query string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"cleaned"}, []string{"BOOL"}, [][]any{{true}}, args...)
}

func ginMaintResult(query string, value any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"gin_clean_pending_list"}, []string{"INT8"}, [][]any{{value}}, args...)
}

const ginMaintCleanSQL = "SELECT gin_clean_pending_list('gin_maint_oracle_i'::regclass)"
const ginMaintPositiveSQL = ginMaintCleanSQL + " > 0 AS cleaned"
const ginMaintIDsSQL = "SELECT id FROM gin_maint_oracle WHERE a @> '{2}'::integer[] ORDER BY id"
