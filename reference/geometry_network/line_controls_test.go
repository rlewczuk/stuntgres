package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func lineControlRows(low bool) [][]any {
	precise, sub := "{1,2,0.10000000000000002}", "{1,2,5e-324}"
	if low {
		precise, sub = "{1,2,0.1}", "{1,2,4.94065645841247e-324}"
	}
	return [][]any{{int64(1), "{2,4,6}"}, {int64(2), precise}, {int64(3), sub}, {int64(4), "{1,-0,-0}"}, {int64(5), "{Infinity,1,NaN}"}, {int64(6), nil}, {int64(7), "{2,4,6}"}}
}

func lineRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	lineMetadata(t, false, rows, []string{"INT4", "LINE"}, []bool{false, true})
	var got [][]any
	for rows.Next() {
		var id int64
		var v any
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{id, v})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained %#v want %#v", got, want)
	}
}

// Roll back successful probes as well as CHECK failures so deterministic dumps
// remain independent of the number of replay verifications.
func lineControlProbe(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO line_controls(id) VALUES(80)", 1))
	run(lineSelect(b, "SELECT l FROM line_controls WHERE id=80", []string{"l"}, []string{"LINE"}, [][]any{{"{2,4,6}"}}))
	run(fixture.Exec("INSERT INTO line_controls VALUES(-1,'{3,6,9}')", 1))
	run(lineSelect(b, "SELECT l FROM line_controls WHERE id=-1", []string{"l"}, []string{"LINE"}, [][]any{{"{3,6,9}"}}))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"line_controls\" violates check constraint \"line_controls_check\"")
	d.Schema = "public"
	d.Table = "line_controls"
	d.Constraint = "line_controls_check"
	d.Detail = "Failing row contains (-2, {2,4,7})."
	run(fixture.ErrorDiagnostic("INSERT INTO line_controls VALUES(-2,'{2,4,7}')", d))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Select("SELECT count(*) AS n FROM line_controls WHERE id IN (-2,-1,80)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	s := fixture.Select("SELECT id,eq FROM line_control_view WHERE id IN (1,2,6) ORDER BY id", []string{"id", "eq"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(6), nil}})
	run(s)
	run(fixture.Prepared(s))
}

func TestReferenceLineControls(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE line_controls(id integer NOT NULL,l line DEFAULT line '{2,4,6}',CHECK(id>0 OR l=line '{1,2,3}'))", 0))
		run(fixture.Exec("CREATE VIEW line_control_view AS SELECT id,l,NULL::line AS n,pg_catalog.line_eq(l,line '{1,2,3}') AS eq FROM line_controls", 0))
		run(fixture.Exec("INSERT INTO line_controls VALUES(1,'{2,4,6}'),(2,'{1,2,0.10000000000000002}'),(3,'{1,2,5e-324}'),(4,'{1,-0,-0}'),(5,'{Infinity,1,NaN}'),(6,NULL)", 6))
		run(fixture.Exec("INSERT INTO line_controls(id) VALUES(7)", 1))
		checks := []fixture.Step{lineSelect(b, "SELECT id,l FROM line_controls ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, lineControlRows(false)), lineSelect(b, "SELECT l,n FROM line_control_view WHERE id=1", []string{"l", "n"}, []string{"LINE", "LINE"}, [][]any{{"{2,4,6}", nil}})}
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		lineControlProbe(t, b, run)
		if b.PG {
			return
		} // Host controls have no PostgreSQL API equivalent.
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO line_controls VALUES(99,'{987654321,1,0}')", 1))
		var dump bytes.Buffer
		err := stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollback := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollback != nil {
			t.Fatalf("dump %v rollback %v", err, rollback)
		}
		if strings.Contains(dump.String(), "987654321") {
			t.Fatal("uncommitted row in dump")
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT id,l FROM line_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET extra_float_digits=3", 0))
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		lineControlProbe(t, b, run)
		lineRetained(t, retained, lineControlRows(true))
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE line_sentinel(i integer)", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			t.Helper()
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=3", 0))
			for _, s := range checks {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
			}
			lineControlProbe(t, b, func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
		}
		verify()
		old, err := target.Memory.QueryContext(target.Ctx, "SELECT id,l FROM line_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		bad := "CREATE TABLE line_staged(l line); INSERT INTO line_staged VALUES('{1,2,3}'); INSERT INTO line_staged VALUES('bad');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type line: \"bad\"")
		d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		sentinel := errors.New("line control I/O")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("read %v", err)
		}
		if err := stuntgres.Dump(target.Ctx, target.Native, boxFailWriter{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("write %v", err)
		}
		cancelled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(cancelled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel %v", err)
		}
		verify()
		lineRetained(t, old, lineControlRows(false))
		var redump bytes.Buffer
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("nondeterministic replay")
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("actual dump PostgreSQL replay %v", err)
			}
			pg := fixture.Backend{PG: true}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, lineSelect(pg, "SELECT id,l FROM line_controls ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, lineControlRows(false)))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, lineSelect(pg, "SELECT l,n FROM line_control_view WHERE id=1", []string{"l", "n"}, []string{"LINE", "LINE"}, [][]any{{"{2,4,6}", nil}}))
			lineControlProbe(t, pg, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW line_control_view; DROP TABLE line_controls"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
