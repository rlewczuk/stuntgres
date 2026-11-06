package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func segIntervalStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	segIntervalSeed(t, p, b, c)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE VIEW seg_interval_view AS "+segIntervalTableQuery(), 0))
	columns := []string{"id integer"}
	for i, name := range segIntervalNames {
		value := "('0 .. 1'::seg " + segIntervalOperators[i] + " '1 .. 2'::seg)"
		if i == 5 {
			value = "seg_overlap('0 .. 1'::seg,'1 .. 2'::seg)"
		}
		columns = append(columns, name+" boolean DEFAULT "+value)
	}
	run(fixture.Exec("CREATE TABLE seg_interval_defaults("+strings.Join(columns, ",")+")", 0))
	run(fixture.Exec("INSERT INTO seg_interval_defaults(id) VALUES (1)", 1))
	run(fixture.Exec("CREATE TABLE seg_interval_checked(id integer,a seg,b seg,CONSTRAINT seg_interval_overlap CHECK(a && b))", 0))
	run(fixture.Exec("INSERT INTO seg_interval_checked VALUES (1,'0 .. 1','1 .. 2')", 1))
}
func segIntervalStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, canonical bool) {
	t.Helper()
	table := segIntervalTableCheck(b)
	if canonical {
		table.Rows = segIntervalSeedRows()
		table.Rows[3][5] = true
	}
	segIntervalBoth(t, p, b, c, table)
	viewRows := table.Rows
	segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT id,"+strings.Join(segIntervalNames, ",")+" FROM seg_interval_view ORDER BY id", append([]string{"id"}, segIntervalNames...), append([]string{"INT4"}, segIntervalTypes...), viewRows, true))
	defaults := fixture.Select("SELECT "+strings.Join(segIntervalNames, ",")+" FROM seg_interval_defaults ORDER BY id", segIntervalNames, segIntervalTypes, [][]any{{false, false, true, false, false, true, false, false}})
	segIntervalBoth(t, p, b, c, defaults)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO seg_interval_defaults(id) VALUES (2)", 1))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT "+strings.Join(segIntervalNames, ",")+" FROM seg_interval_defaults WHERE id=2", segIntervalNames, segIntervalTypes, defaults.Rows))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM seg_interval_defaults WHERE id=2", 1))
	want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "seg_interval_checked" violates check constraint "seg_interval_overlap"`)
	want.Detail = "Failing row contains (2, 0 .. 1, 2 .. 3)."
	want.Schema = "public"
	want.Table = "seg_interval_checked"
	want.Constraint = "seg_interval_overlap"
	for _, step := range []fixture.Step{
		fixture.ErrorDiagnostic("INSERT INTO seg_interval_checked VALUES (2,'0 .. 1','2 .. 3')", want),
		fixture.ErrorDiagnostic("INSERT INTO seg_interval_checked VALUES (2,$1,$2)", want, "0 .. 1", "2 .. 3"),
	} {
		step.ErrorStage = "execute"
		segIntervalBoth(t, p, b, c, step)
	}
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT id,a::text AS a,b::text AS b FROM seg_interval_checked ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "0 .. 1", "1 .. 2"}}))
}
func TestReferenceSegIntervalStored(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		segIntervalStoredSetup(t, p, b, c)
		segIntervalStoredChecks(t, p, b, c, false)
		// Extension replacement is a shared SQL lifecycle witness, using original
		// valid spellings rather than lossy canonical output as input.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION seg CASCADE", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE seg_interval_data,seg_interval_defaults,seg_interval_checked", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION seg", 0))
		segIntervalStoredSetup(t, p, b, c)
		segIntervalStoredChecks(t, p, b, c, false)
	})
}
