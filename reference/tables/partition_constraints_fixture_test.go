package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Every new witness has the same case/route/backend shape. Selecting /././postgres
// runs only the independently expected oracle body, without changing the definition.
func partitionConstraintCase(t *testing.T, name string, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(route.name, func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					body(t, p, b, route.prepared)
				})
			})
		}
	})
}

func partitionConstraintRun(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func partitionConstraintMissing(query, table, kind, column string) fixture.Step {
	return referenceRangeError(query, "0A000", "unique constraint on partitioned table must include all partitioning columns", fixture.Diagnostic{
		Detail: kind + " constraint on table \"" + table + "\" lacks column \"" + column + "\" which is part of the partition key.",
	})
}

func partitionConstraintDuplicate(query, leaf, index, detail string, args ...any) fixture.Step {
	s := referenceRangeError(query, "23505", "duplicate key value violates unique constraint \""+index+"\"", fixture.Diagnostic{
		Detail: detail, Schema: "public", Table: leaf, Constraint: index,
	})
	s.Args = args
	return s
}

func partitionConstraintTree(run func(fixture.Step), declaration string) {
	run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT,"+declaration+") PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pc_b PARTITION OF pc_root FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("CREATE TABLE pc_def PARTITION OF pc_root DEFAULT", 0))
}

func partitionConstraintIndexState(name string, valid bool) fixture.Step {
	return fixture.Select("SELECT indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='"+name+"'::regclass::OID",
		[]string{"indisunique", "indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, valid, true, true}})
}

func partitionConstraintEdge(child, parent, table string) fixture.Step {
	return fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::OID AND objid='"+child+"'::regclass::OID AND ((refobjid='"+parent+"'::regclass::OID AND deptype='P') OR (refobjid='"+table+"'::regclass::OID AND deptype='S')) ORDER BY deptype",
		[]string{"deptype"}, []string{"CHAR"}, [][]any{{"P"}, {"S"}})
}

func partitionConstraintManualTree(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root FOR VALUES FROM(1) TO(3)", 0))
	run(fixture.Exec("CREATE TABLE pc_b PARTITION OF pc_root FOR VALUES FROM(3) TO(5)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON ONLY pc_root(k,payload)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pc_a_idx ON pc_a(k,payload)", 0))
	run(fixture.Exec("CREATE UNIQUE INDEX pc_b_idx ON pc_b(k,payload)", 0))
}
