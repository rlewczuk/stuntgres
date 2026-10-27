package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released stock rmgrlist.h identities are oracle expectations, not native data.
func recoveryManagersDemand(pg bool) fixture.Step {
	const query = "SELECT rm_id,rm_name,rm_builtin FROM pg_get_wal_resource_managers() ORDER BY rm_id"
	if !pg {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "pg_get_wal_resource_managers physical WAL resource manager registry is unsupported in embedded mode"})
		s.SelectRows, s.ErrorStage = true, "execute"
		return s
	}
	names := []string{"XLOG", "Transaction", "Storage", "CLOG", "Database", "Tablespace", "MultiXact", "RelMap", "Standby", "Heap2", "Heap", "Btree", "Hash", "Gin", "Gist", "Sequence", "SPGist", "BRIN", "CommitTs", "ReplicationOrigin", "Generic", "LogicalMessage"}
	rows := make([][]any, len(names))
	for i, name := range names { rows[i] = []any{int64(i), name, true} }
	return fixture.Select(query, []string{"rm_id", "rm_name", "rm_builtin"}, []string{"INT4", "TEXT", "BOOL"}, rows)
}

func TestReferenceRecoveryInfoResourceManagers(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM pg_get_wal_resource_managers() LIMIT 0", []string{"rm_id", "rm_name", "rm_builtin"}, []string{"INT4", "TEXT", "BOOL"}, nil),
			fixture.Select("SELECT * FROM pg_catalog.pg_get_wal_resource_managers() AS r(id,name,builtin) LIMIT 0", []string{"id", "name", "builtin"}, []string{"INT4", "TEXT", "BOOL"}, nil),
			fixture.Select("SELECT * FROM pg_get_wal_resource_managers() WITH ORDINALITY LIMIT 0", []string{"rm_id", "rm_name", "rm_builtin", "ordinality"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, nil),
			fixture.Select("SELECT pg_get_wal_resource_managers() LIMIT 0", []string{"pg_get_wal_resource_managers"}, []string{"RECORD"}, nil),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			recoveryHeld(t, p, b, c, s)
		}
		demand := recoveryManagersDemand(b.PG)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, demand)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(demand))
		// Successful preparation precedes repeated execute-stage physical failure.
		recoveryHeld(t, p, b, c, demand)
	})
}

func TestReferenceRecoveryInfoCatalog(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		query := "SELECT oid::bigint,proname::text,proisstrict,provolatile::text,proparallel::text,proretset,pronargs::bigint,pronargdefaults::bigint,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,prorows,prosrc FROM pg_catalog.pg_proc WHERE oid IN (3810,3820,3821,3830,6224) ORDER BY oid"
		rows := [][]any{
			{int64(3810), recoveryNames[0], true, "v", "s", false, int64(0), int64(0), int64(16), "", nil, nil, nil, float64(0), recoveryNames[0]},
			{int64(3820), recoveryNames[1], true, "v", "s", false, int64(0), int64(0), int64(3220), "", nil, nil, nil, float64(0), recoveryNames[1]},
			{int64(3821), recoveryNames[2], true, "v", "s", false, int64(0), int64(0), int64(3220), "", nil, nil, nil, float64(0), recoveryNames[2]},
			{int64(3830), recoveryNames[3], true, "v", "s", false, int64(0), int64(0), int64(1184), "", nil, nil, nil, float64(0), recoveryNames[3]},
			{int64(6224), recoveryNames[4], true, "v", "s", true, int64(0), int64(0), int64(2249), "", "{23,25,16}", "{o,o,o}", "{rm_id,rm_name,rm_builtin}", float64(50), recoveryNames[4]},
		}
		s := fixture.Select(query, []string{"oid", "proname", "proisstrict", "provolatile", "proparallel", "proretset", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "prorows", "prosrc"}, []string{"INT8", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "INT8", "INT8", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "FLOAT4", "TEXT"}, rows)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		for i, name := range recoveryNames {
			oid := []int64{3810, 3820, 3821, 3830, 6224}[i]
			s := fixture.Select("SELECT 'pg_catalog."+name+"()'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{oid}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}
