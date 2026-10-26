package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func shmemDefinitions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	defer conn.ExecContext(context.Background(), "RESET quote_all_identifiers")
	// Independently observed PostgreSQL 18.6 default, pretty and pg_views text.
	for _, tc := range []struct {
		setting string
		definitions []string
	}{
		{"off", []string{
			" SELECT name,\n    off,\n    size,\n    allocated_size\n   FROM pg_get_shmem_allocations() pg_get_shmem_allocations(name, off, size, allocated_size);",
			" SELECT name,\n    numa_node,\n    size\n   FROM pg_get_shmem_allocations_numa() pg_get_shmem_allocations_numa(name, numa_node, size);",
		}},
		{"on", []string{
			" SELECT \"name\",\n    \"off\",\n    \"size\",\n    \"allocated_size\"\n   FROM \"pg_get_shmem_allocations\"() \"pg_get_shmem_allocations\"(\"name\", \"off\", \"size\", \"allocated_size\");",
			" SELECT \"name\",\n    \"numa_node\",\n    \"size\"\n   FROM \"pg_get_shmem_allocations_numa\"() \"pg_get_shmem_allocations_numa\"(\"name\", \"numa_node\", \"size\");",
		}},
	} {
		pgSequenceExec(t, p, b, conn, "SET quote_all_identifiers="+tc.setting)
		for i, name := range shmemNames {
			for _, pretty := range []bool{false, true} {
				s := fixture.Select("SELECT pg_get_viewdef($1::regclass,$2::boolean)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{tc.definitions[i]}}, "pg_catalog."+name, pretty)
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
			}
			s := fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.definitions[i]}}, name)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
		}
	}
}
