package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func shmemRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := "postgres"
	if b.PG { owner = "stuntgres" }
	acl := "{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner+"}"
	want := [][]any{
		{int64(4100), "pg_get_shmem_allocations_numa", owner, acl, int64(12), int64(2249), true, "v", true, "s", "{name,numa_node,size}", "{25,23,20}", "{o,o,o}", "pg_get_shmem_allocations_numa", float64(50)},
		{int64(5052), "pg_get_shmem_allocations", owner, acl, int64(12), int64(2249), true, "v", true, "s", "{name,off,size,allocated_size}", "{25,20,20,20}", "{o,o,o,o}", "pg_get_shmem_allocations", float64(50)},
	}
	s := fixture.Select("SELECT p.oid::bigint,p.proname::text,r.rolname::text,p.proacl::text,p.prolang::bigint,p.prorettype::bigint,p.proretset,p.provolatile::text,p.proisstrict,p.proparallel::text,p.proargnames::text,p.proallargtypes::text,p.proargmodes::text,p.prosrc,p.prorows FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_roles r ON r.oid=p.proowner WHERE p.oid IN (4100,5052) ORDER BY p.oid", []string{"oid","proname","rolname","proacl","prolang","prorettype","proretset","provolatile","proisstrict","proparallel","proargnames","proallargtypes","proargmodes","prosrc","prorows"}, []string{"INT8","TEXT","TEXT","TEXT","INT8","INT8","BOOL","TEXT","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","FLOAT4"}, want)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}
