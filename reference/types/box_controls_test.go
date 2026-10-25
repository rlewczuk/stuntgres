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

func boxControlRows(low bool) [][]any {
	precise, subnormal := "(0.10000000000000002,1),(0,0)", "(5e-324,1),(0,0)"
	if low {
		precise, subnormal = "(0.1,1),(0,0)", "(4.94065645841247e-324,1),(0,0)"
	}
	return [][]any{
		{int64(1), "(3,4),(1,2)"}, {int64(2), "(-5,-6),(8,9)"}, {int64(3), precise},
		{int64(4), "(-0,-0),(-0,-0)"}, {int64(5), subnormal},
		{int64(6), "(Infinity,NaN),(-Infinity,0)"}, {int64(7), nil}, {int64(8), "(3,4),(1,2)"},
	}
}

func boxControlChecks(b fixture.Backend) []fixture.Step {
	values := boxSelect(b, "SELECT id,b FROM box_controls ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOX"}, boxControlRows(false))
	values.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.BoxMetadata(t, b.PG, rows, values.Types, []bool{false, true})
	}
	return []fixture.Step{
		values,
		boxSelect(b, "SELECT hi FROM box_control_view WHERE id=2", []string{"hi"}, []string{"POINT"}, [][]any{{"(-5,-6)"}}),
		boxSelect(b, "SELECT b FROM box_control_empty", []string{"b"}, []string{"BOX"}, nil),
	}
}

func boxConsumeRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	fixture.BoxMetadata(t, false, rows, []string{"INT4", "BOX"}, []bool{false, true})
	got := make([][]any, 0)
	for rows.Next() {
		var id int64
		var value any
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				t.Fatalf("native retained BOX cell %T", value)
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
		t.Fatalf("retained BOX rows %#v want %#v", got, want)
	}
}

type boxFailReader struct{ err error }

func (r boxFailReader) Read([]byte) (int, error) { return 0, r.err }

type boxFailWriter struct{ err error }

func (w boxFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReferenceBoxControls(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE box_controls(id integer NOT NULL,b box DEFAULT box '1,4,3,2',CHECK(id>0 OR b=box '1,1,0,0'))", 0))
		run(fixture.Exec("CREATE TABLE box_control_empty(b box NOT NULL)", 0))
		run(fixture.Exec("CREATE VIEW box_control_view AS SELECT id,b[0] AS hi FROM box_controls", 0))
		run(fixture.Exec("INSERT INTO box_controls VALUES(1,'1,4,3,2'),(2,'3,4,1,2'),(3,'0.10000000000000002,1,0,0'),(4,'-0,-0,-0,-0'),(5,'5e-324,1,0,0'),(6,'Infinity,NaN,-Infinity,0'),(7,NULL)", 7))
		run(fixture.Exec("INSERT INTO box_controls(id) VALUES(8)", 1))
		run(fixture.Exec("UPDATE box_controls SET b[0]='(-5,-6)'::point,b[1]='(8,9)'::point WHERE id=2", 1))
		for _, s := range boxControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		if b.PG {
			return // PostgreSQL has no native Reset/Dump/Restore API.
		}
		run(fixture.Exec("SET extra_float_digits=0", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO box_controls VALUES(99,'987654321,1,0,0')", 1))
		var dump bytes.Buffer
		err := stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollbackErr != nil {
			t.Fatalf("Dump %v rollback %v", err, rollbackErr)
		}
		if strings.Contains(dump.String(), "987654321") {
			t.Fatal("Dump included an uncommitted row")
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT id,b FROM box_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		fixture.BoxMetadata(t, false, retained, []string{"INT4", "BOX"}, []bool{false, true})
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET extra_float_digits=3", 0))
		for _, s := range boxControlChecks(b) {
			run(s)
			run(fixture.Prepared(s))
		}
		boxConsumeRetained(t, retained, boxControlRows(true))
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE box_control_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO box_control_sentinel VALUES(42)", 1))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			t.Helper()
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=3", 0))
			for _, s := range boxControlChecks(b) {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(s))
			}
		}
		verify()
		old, err := target.Memory.QueryContext(target.Ctx, "SELECT id,b FROM box_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		bad := "CREATE TABLE box_staged(b box); INSERT INTO box_staged VALUES('1,1,0,0'); INSERT INTO box_staged VALUES('bad');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type box: \"bad\"")
		d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		verify()
		sentinel := errors.New("box control I/O failure")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("Restore I/O error %v", err)
		}
		if err := stuntgres.Dump(target.Ctx, target.Native, boxFailWriter{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("Dump I/O error %v", err)
		}
		cancelled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(cancelled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Restore %v", err)
		}
		verify()
		boxConsumeRetained(t, old, boxControlRows(false))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=0", 0))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same/fresh replay or failed Restore changed deterministic BOX dump")
		}
		// Ordinary SQL input must still normalize; exact native replay authority
		// must not leak into a connection after Restore.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, boxSelect(b, "SELECT box '(-5,-6),(8,9)' AS b", []string{"b"}, []string{"BOX"}, [][]any{{"(8,9),(-5,-6)"}}))
	})
}

func TestReferenceBoxFeatureSmoke(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE box_smoke(id integer NOT NULL,b box)", 0))
		run(fixture.Exec("INSERT INTO box_smoke VALUES(1,'(1,4),(3,2)')", 1))
		q := "INSERT INTO box_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, "(3,2),(1,4)"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, nil))
		query := "SELECT id,b,b[0] AS hi,b[1] AS lo FROM box_smoke ORDER BY id"
		want := [][]any{{int64(1), "(3,4),(1,2)", "(3,4)", "(1,2)"}, {int64(2), "(3,4),(1,2)", "(3,4)", "(1,2)"}, {int64(3), nil, nil, nil}}
		run(boxSelect(b, query, []string{"id", "b", "hi", "lo"}, []string{"INT4", "BOX", "POINT", "POINT"}, want))
		run(fixture.Select("SELECT box '2,2,0,0'=box '14,11,10,10' AS area,box '2,2,0,0'~=box '14,11,10,10' AS same", []string{"area", "same"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(fixture.Exec("UPDATE box_smoke SET b[0]='(-5,-6)'::point WHERE id=1", 1))
		want[0] = []any{int64(1), "(-5,-6),(1,2)", "(-5,-6)", "(1,2)"}
		s := boxSelect(b, query, []string{"id", "b", "hi", "lo"}, []string{"INT4", "BOX", "POINT", "POINT"}, want)
		run(s)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
