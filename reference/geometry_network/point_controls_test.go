package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pointControlRows(low bool) [][]any {
	precise := "(1.0000000000000002,0.10000000000000002)"
	if low {
		precise = "(1,0.1)"
	}
	return [][]any{{int64(1), "(-5,2)"}, {int64(2), precise}, {int64(3), "(-0,-0)"}, {int64(4), nil}, {int64(5), "(1,2)"}}
}

func pointControlChecks(b fixture.Backend) []fixture.Step {
	s := boxSelect(b, "SELECT id,p FROM point_controls ORDER BY id", []string{"id", "p"}, []string{"INT4", "POINT"}, pointControlRows(false))
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.BoxMetadata(t, b.PG, rows, []string{"INT4", "POINT"}, []bool{false, true})
	}
	return []fixture.Step{s, fixture.Select("SELECT x,same FROM point_control_view WHERE id=1", []string{"x", "same"}, []string{"FLOAT8", "BOOL"}, [][]any{{float64(-5), true}})}
}

func TestReferencePointControls(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE point_controls(id integer NOT NULL,p point DEFAULT point '1,2',CHECK(id>0 OR p ~= point '0,0'))", 0))
		run(fixture.Exec("CREATE VIEW point_control_view AS SELECT id,p[0] AS x,p ~= point '-5,2' AS same FROM point_controls", 0))
		run(fixture.Exec("INSERT INTO point_controls VALUES(1,'1,2'),(2,'1.0000000000000002,0.10000000000000002'),(3,'-0,-0'),(4,NULL)", 4))
		run(fixture.Exec("INSERT INTO point_controls(id) VALUES(5)", 1))
		var before *sql.Rows
		var err error
		if !b.PG {
			before, err = c.QueryContext(p.Ctx, "SELECT id,p FROM point_controls WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer before.Close()
		}
		run(fixture.Exec("UPDATE point_controls SET p[0]=-5 WHERE id=1", 1))
		for _, s := range pointControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		if b.PG {
			return
		} // Host logical controls have no PostgreSQL API equivalent.
		fixture.PointConsumeRetained(t, before, [][]any{{int64(1), "(1,2)"}})
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO point_controls VALUES(99,'987654321,1')", 1))
		var dump bytes.Buffer
		err = stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollbackErr != nil {
			t.Fatalf("Dump %v rollback %v", err, rollbackErr)
		}
		if strings.Contains(dump.String(), "987654321") {
			t.Fatal("Dump included uncommitted point")
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,p FROM point_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET extra_float_digits=3", 0))
		for _, s := range pointControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		fixture.PointConsumeRetained(t, old, pointControlRows(true))
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE point_control_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO point_control_sentinel VALUES(42)", 1))
		bad := "CREATE TABLE point_staged(p point); INSERT INTO point_staged VALUES('1,2'); INSERT INTO point_staged VALUES('bad');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type point: \"bad\"")
		d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM point_control_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=3", 0))
			for _, s := range pointControlChecks(b) {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
			}
		}
		verify()
		retained, err := target.Memory.QueryContext(target.Ctx, "SELECT id,p FROM point_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		verify()
		fixture.PointConsumeRetained(t, retained, pointControlRows(false))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("POINT replay changed deterministic dump")
		}
	})
}
