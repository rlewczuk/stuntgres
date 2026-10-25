package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func partitionExclusionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func partitionExclusionCase(t *testing.T, name string, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(name, func(t *testing.T) {
				partitionExclusionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					body(t, p, b, prepared)
				})
			})
		}
	})
}

const partitionExclusionKeys = "tenant inet_ops WITH =,during WITH &&"

func partitionExclusionTree(run func(fixture.Step), flags string) {
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+") "+flags+") PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
	run(fixture.Exec("CREATE TABLE pe_other PARTITION OF pe_root DEFAULT", 0))
}

func partitionExclusionConflict(query, table, constraint, proposed, existing string, args ...any) fixture.Step {
	return fixture.ErrorDiagnostic(query, fixture.ExclusionReferenceConflict(table, constraint, "tenant, during", proposed, existing), args...)
}

func partitionExclusionIDs(ids ...int64) fixture.Step {
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id}
	}
	return fixture.Select("SELECT id FROM pe_root ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

// PostgreSQL 18.0 oracle: the local root has connoinherit=true, while
// automatically inherited providers have connoinherit=false.
func partitionExclusionGraph() fixture.Step {
	return fixture.Select("SELECT t.relname,c.conname,c.contype,c.conislocal,c.coninhcount,c.connoinherit,c.condeferrable,c.condeferred,i.relkind,pc.conname AS parent FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid LEFT JOIN pg_constraint pc ON pc.oid=c.conparentid WHERE t.relname IN ('pe_root','pe_low','pe_other') AND c.contype='x' ORDER BY t.relname,c.conname",
		[]string{"relname", "conname", "contype", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred", "relkind", "parent"},
		[]string{"NAME", "NAME", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "CHAR", "NAME"},
		[][]any{{"pe_low", "pe_low_tenant_during_excl", "x", false, int64(1), false, false, false, "i", "pe_ex"}, {"pe_other", "pe_other_tenant_during_excl", "x", false, int64(1), false, false, false, "i", "pe_ex"}, {"pe_root", "pe_ex", "x", true, int64(0), true, false, false, "I", nil}})
}
