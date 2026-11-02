package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func fdwParallelHierarchy(t *testing.T, b fixture.Backend, r *fdwParallelReader, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE TABLE fdw_parallel_parent(id integer)", 0))
	before := r.parallelCounts()
	for _, q := range []string{
		"CREATE FOREIGN TABLE fdw_parallel_foreign_child() INHERITS(fdw_parallel_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')",
		"CREATE TABLE fdw_parallel_local_child() INHERITS(fdw_data)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO fdw_parallel_local_child VALUES(7,2,'child')", 1))
	run(fdwParallelIDs("SELECT id FROM ONLY fdw_parallel_local_child", 7))
	run(fixture.Exec("DROP FOREIGN TABLE fdw_parallel_foreign_child", 0))
	if !b.PG && r.parallelCounts() != before {
		t.Fatal("hierarchy metadata or local-only scan entered coordinator", before, r.parallelCounts())
	}
}
