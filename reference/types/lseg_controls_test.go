package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func lsegControlRows(low bool) [][]any {
	precise, subnormal := "[(0.10000000000000002,1),(0,0)]", "[(5e-324,1),(0,0)]"
	if low {
		precise, subnormal = "[(0.1,1),(0,0)]", "[(4.94065645841247e-324,1),(0,0)]"
	}
	return [][]any{{int64(1), "[(1,4),(3,2)]"}, {int64(2), "[(-5,-6),(8,9)]"}, {int64(3), precise}, {int64(4), "[(-0,-0),(-0,-0)]"}, {int64(5), subnormal}, {int64(6), "[(Infinity,NaN),(-Infinity,0)]"}, {int64(7), nil}, {int64(8), "[(1,4),(3,2)]"}, {int64(9), "[(3,2),(1,4)]"}}
}

func lsegConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	boxMetadata(t, false, rows, []string{"INT4", "LSEG"}, nil)
	got := make([][]any, 0)
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				t.Fatalf("native LSEG raw value %T", value)
			}
		}
		got = append(got, []any{id, value})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained LSEG rows %#v want %#v", got, want)
	}
}

func lsegControlChecks(b fixture.Backend) []fixture.Step {
	s := boxSelect(b, "SELECT id,s FROM lseg_controls ORDER BY id", []string{"id", "s"}, []string{"INT4", "LSEG"}, lsegControlRows(false))
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		boxMetadata(t, b.PG, rows, []string{"INT4", "LSEG"}, []bool{false, true})
	}
	return []fixture.Step{s, boxSelect(b, "SELECT endpoint FROM lseg_control_view WHERE id=2", []string{"endpoint"}, []string{"POINT"}, [][]any{{"(-5,-6)"}}), boxSelect(b, "SELECT s FROM lseg_control_empty", []string{"s"}, []string{"LSEG"}, nil)}
}

func TestReferenceLsegControls(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE lseg_controls(id integer NOT NULL,s lseg DEFAULT lseg '1,4,3,2',CHECK(id>0 OR s=lseg '1,1,0,0'))", 0))
		run(fixture.Exec("CREATE TABLE lseg_control_empty(s lseg NOT NULL)", 0))
		run(fixture.Exec("CREATE VIEW lseg_control_view AS SELECT id,s[0] AS endpoint FROM lseg_controls", 0))
		run(fixture.Exec("INSERT INTO lseg_controls VALUES(1,'1,4,3,2'),(2,'3,2,1,4'),(3,'0.10000000000000002,1,0,0'),(4,'-0,-0,-0,-0'),(5,'5e-324,1,0,0'),(6,'Infinity,NaN,-Infinity,0'),(7,NULL),(9,'3,2,1,4')", 8))
		run(fixture.Exec("INSERT INTO lseg_controls(id) VALUES(8)", 1))
		var before *sql.Rows
		var err error
		if !b.PG {
			before, err = c.QueryContext(p.Ctx, "SELECT id,s FROM lseg_controls WHERE id=2")
			if err != nil {
				t.Fatal(err)
			}
			defer before.Close()
		}
		run(fixture.Exec("UPDATE lseg_controls SET s[0]='(-5,-6)'::point,s[1]='(8,9)'::point WHERE id=2", 1))
		for _, s := range lsegControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		if b.PG {
			return
		} // Native controls have no PostgreSQL API equivalent.
		lsegConsumeRetained(t, before, [][]any{{int64(2), "[(3,2),(1,4)]"}})
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO lseg_controls VALUES(99,'987654321,1,0,0')", 1))
		var dump bytes.Buffer
		err = stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollbackErr != nil {
			t.Fatalf("Dump %v rollback %v", err, rollbackErr)
		}
		if strings.Contains(dump.String(), "987654321") {
			t.Fatal("Dump included uncommitted row")
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,s FROM lseg_controls ORDER BY id")
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
		for _, s := range lsegControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		lsegConsumeRetained(t, old, lsegControlRows(true))
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE lseg_control_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO lseg_control_sentinel VALUES(42)", 1))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			t.Helper()
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=3", 0))
			for _, s := range lsegControlChecks(b) {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(s))
			}
		}
		verify()
		retained, err := target.Memory.QueryContext(target.Ctx, "SELECT id,s FROM lseg_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		bad := "CREATE TABLE lseg_staged(s lseg); INSERT INTO lseg_staged VALUES('1,1,0,0'); INSERT INTO lseg_staged VALUES('bad');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type lseg: \"bad\"")
		d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		verify()
		sentinel := errors.New("lseg control I/O failure")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("Restore reader error %v", err)
		}
		if err := stuntgres.Dump(target.Ctx, target.Native, boxFailWriter{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("Dump writer error %v", err)
		}
		cancelled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(cancelled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Restore %v", err)
		}
		verify()
		lsegConsumeRetained(t, retained, lsegControlRows(false))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("LSEG replay changed deterministic dump")
		}
	})
}
