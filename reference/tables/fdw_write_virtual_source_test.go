package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWriteVirtualSourceIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader) {
	t.Helper()
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.virtual_target(id INTEGER,v INTEGER,a INTEGER[])", 0)
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_virtual_target(id INTEGER,v INTEGER,a INTEGER[]) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'virtual_target')", 0))
	run(fixture.Exec("CREATE TABLE fdw_virtual_matches(k INTEGER,v INTEGER GENERATED ALWAYS AS(k+10) VIRTUAL)", 0))
	run(fixture.Exec("INSERT INTO fdw_virtual_matches(k) VALUES(1)", 1))
	run(fixture.Exec("CREATE TABLE fdw_virtual_links(x INTEGER)", 0))
	run(fixture.Exec("INSERT INTO fdw_virtual_links VALUES(1),(2),(3)", 3))
	// The zero is genuine: z fails only when demanded on a present source row.
	// A null-extended row must yield NULL even for virtual constant c=1.
	run(fixture.Exec("CREATE TABLE fdw_virtual_bad(a INTEGER,c INTEGER GENERATED ALWAYS AS(1) VIRTUAL,z INTEGER GENERATED ALWAYS AS(1/a) VIRTUAL)", 0))
	run(fixture.Exec("INSERT INTO fdw_virtual_bad(a) VALUES(0)", 1))
	base := [][]any{{int64(1), int64(10), int64(1), int64(2)}, {int64(2), int64(20), int64(1), int64(2)}, {int64(3), int64(30), int64(1), int64(2)}}
	updated := func(value any) [][]any {
		return [][]any{{int64(1), value, int64(1), int64(2)}, {int64(2), int64(20), int64(1), int64(2)}, {int64(3), int64(30), int64(1), int64(2)}}
	}
	withoutFirst := [][]any{{int64(2), int64(20), int64(1), int64(2)}, {int64(3), int64(30), int64(1), int64(2)}}
	projection := func(query string, rows [][]any) fixture.Step {
		return fixture.Select(query, []string{"id", "v", "source_value"}, []string{"INT4", "INT4", "INT4"}, rows)
	}
	failure := func(query string) fixture.Step {
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		step.SelectRows, step.ErrorStage = true, "execute"
		return step
	}
	for _, tc := range []struct {
		name     string
		step     fixture.Step
		expected [][]any
		fails    bool
	}{
		{"UpdateReturnOnly", projection("UPDATE fdw_virtual_target f SET v=99 FROM fdw_virtual_matches m WHERE f.id=m.k RETURNING f.id,f.v,m.v AS source_value", [][]any{{int64(1), int64(99), int64(11)}}), updated(int64(99)), false},
		{"DeleteReturnOnly", projection("DELETE FROM fdw_virtual_target f USING fdw_virtual_matches m WHERE f.id=m.k RETURNING f.id,f.v,m.v AS source_value", [][]any{{int64(1), int64(10), int64(11)}}), withoutFirst, false},
		{"UpdatePredicateOnly", fixture.Exec("UPDATE fdw_virtual_target f SET v=99 FROM fdw_virtual_matches m WHERE f.id=m.k AND m.v=11", 1), updated(int64(99)), false},
		{"DeletePredicateOnly", fixture.Exec("DELETE FROM fdw_virtual_target f USING fdw_virtual_matches m WHERE f.id=m.k AND m.v=11", 1), withoutFirst, false},
		{"UpdateAssignmentOnly", fixture.Exec("UPDATE fdw_virtual_target f SET v=m.v FROM fdw_virtual_matches m WHERE f.id=m.k", 1), updated(int64(11)), false},
		{"SourceTreeOnly", fixture.Exec("UPDATE fdw_virtual_target f SET v=77 FROM fdw_virtual_matches m JOIN fdw_virtual_links l ON m.v=l.x+10 WHERE f.id=l.x", 1), updated(int64(77)), false},
		{"ArrayTargetOnly", fixture.Exec("UPDATE fdw_virtual_target f SET a[m.v-10]=9 FROM fdw_virtual_matches m WHERE f.id=m.k", 1), [][]any{{int64(1), int64(10), int64(9), int64(2)}, {int64(2), int64(20), int64(1), int64(2)}, {int64(3), int64(30), int64(1), int64(2)}}, false},
		{"CorrelatedNullUpdate", projection("UPDATE fdw_virtual_target f SET v=(SELECT g.c) FROM fdw_virtual_links l LEFT JOIN fdw_virtual_bad g ON l.x=1 WHERE f.id=l.x RETURNING f.id,f.v,(SELECT g.c) AS source_value", [][]any{{int64(1), int64(1), int64(1)}, {int64(2), nil, nil}, {int64(3), nil, nil}}), [][]any{{int64(1), int64(1), int64(1), int64(2)}, {int64(2), nil, int64(1), int64(2)}, {int64(3), nil, int64(1), int64(2)}}, false},
		{"CorrelatedNullDelete", projection("DELETE FROM fdw_virtual_target f USING fdw_virtual_links l LEFT JOIN fdw_virtual_bad g ON l.x=1 WHERE f.id=l.x AND (SELECT g.c) IS NULL RETURNING f.id,f.v,(SELECT g.c) AS source_value", [][]any{{int64(2), int64(20), nil}, {int64(3), int64(30), nil}}), [][]any{{int64(1), int64(10), int64(1), int64(2)}}, false},
		{"DemandedFailureUpdate", failure("UPDATE fdw_virtual_target f SET v=99 FROM fdw_virtual_links l LEFT JOIN fdw_virtual_bad g ON l.x=3 WHERE f.id=l.x RETURNING f.id,(SELECT g.z) AS source_value"), base, true},
		{"DemandedFailureDelete", failure("DELETE FROM fdw_virtual_target f USING fdw_virtual_links l LEFT JOIN fdw_virtual_bad g ON l.x=3 WHERE f.id=l.x RETURNING f.id,(SELECT g.z) AS source_value"), base, true},
	} {
		for _, prepared := range []bool{false, true} {
			name := tc.name + "/direct"
			if prepared {
				name = tc.name + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
				fdwSourceExec(t, p, b, reader, run, "TRUNCATE fdw_source.virtual_target", 0)
				fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.virtual_target VALUES(1,10,ARRAY[1,2]),(2,20,ARRAY[1,2]),(3,30,ARRAY[1,2])", 3)
				mutations := 0
				if tc.fails && !b.PG {
					reader.writer.writeHook = func(_ context.Context, name string) error {
						if name == "ExecForeignUpdate" || name == "ExecForeignDelete" {
							mutations++
						}
						return nil
					}
					defer func() { reader.writer.writeHook = nil }()
				}
				step := tc.step
				step.Prepared = prepared
				if !tc.fails && step.SelectRows {
					step.Bag = true
				}
				run(step)
				if tc.fails && !b.PG && mutations == 0 {
					t.Fatal("demanded RETURNING failure did not follow actual provider mutation")
				}
				fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT id,v,a[1] AS a1,a[2] AS a2 FROM fdw_source.virtual_target ORDER BY id", []string{"id", "v", "a1", "a2"}, []string{"INT4", "INT4", "INT4", "INT4"}, tc.expected))
			})
		}
	}
}
