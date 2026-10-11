package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func updateFromControlBody(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := updateFromRun(t, p, b, c)
	updateFromSeed(run)
	q := "UPDATE t x SET v=s.n,prev=x.v FROM s WHERE x.id=s.k RETURNING x.id,old.v,new.v,s.n"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	step := fixture.Select(q, []string{"id", "v", "v", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(101), int64(101)}, {int64(3), int64(30), int64(303), int64(303)}})
	step.Bag = true
	output := fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, step)
	committed := [][]any{{int64(1), int64(101), int64(10)}, {int64(2), int64(20), int64(0)}, {int64(3), int64(303), int64(30)}, {int64(4), int64(40), int64(0)}}
	run(updateFromTargets(committed))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("UPDATE t x SET v=0 FROM s WHERE x.id=s.k", 2))
	run(fixture.Exec("ROLLBACK", 0))
	run(updateFromTargets(committed))
	want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	run(fixture.ErrorDiagnostic("UPDATE t x SET v=s.n FROM s WHERE x.id=s.k RETURNING 1/(s.n-303)", want))
	run(updateFromTargets(committed))
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToUpper(dump.String()), "UPDATE ") {
			t.Fatal("dump contains mutation history")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(updateFromTargets(committed))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(v INT); UPDATE bad SET v=1 FROM s")); err == nil {
			t.Fatal("Restore accepted UPDATE")
		}
		run(updateFromTargets(committed))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(updateFromTargets(committed))
		fresh := fixture.Select(q, []string{"id", "v", "v", "n"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(101), int64(101), int64(101)}, {int64(3), int64(303), int64(303), int64(303)}})
		fresh.Bag = true
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fresh)
	}
	if len(output.Rows) != 2 || output.Rows[0][1] != int64(10) && output.Rows[0][1] != int64(30) {
		t.Fatal("eager OLD output lost ownership", output.Rows)
	}
}

func TestReferenceUpdateFromControls(t *testing.T) { updateFromReference(t, updateFromControlBody) }

func TestReferenceUpdateFromSmoke(t *testing.T) {
	t.Run("one_to_one_returning_rollback_atomicity_controls", func(t *testing.T) { updateFromReference(t, updateFromControlBody) })
	t.Run("direct_and_prepared_counts", TestReferenceUpdateFromOneToOne)
	t.Run("prepared", TestReferenceUpdateFromPrepared)
	t.Run("self_snapshot", TestReferenceUpdateFromSelfJoin)
	t.Run("physical_eligible_winners", TestReferenceUpdateFromMultipleMatches)
}
