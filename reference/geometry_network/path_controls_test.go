package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pathControlRows(low bool) [][]any {
	precise := "[(0.10000000000000002,-0)]"
	if low {
		precise = "[(0.1,-0)]"
	}
	return [][]any{{int64(1), "[(1,2),(3,4)]"}, {int64(2), "((1,2),(3,4))"}, {int64(3), precise}, {int64(4), nil}, {int64(5), "[(5,6)]"}}
}

func pathControlProbe(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO path_controls(id) VALUES(80)", 1))
	run(pathSelect(b, "SELECT p FROM path_controls WHERE id=80", []string{"p"}, []string{"PATH"}, [][]any{{"[(5,6)]"}}))
	run(fixture.Exec("INSERT INTO path_controls VALUES(-1,'((9,8))')", 1))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"path_controls\" violates check constraint \"path_controls_check\"")
	d.Schema, d.Table, d.Constraint = "public", "path_controls", "path_controls_check"
	d.Detail = "Failing row contains (-2, [(1,2),(3,4)])."
	run(fixture.ErrorDiagnostic("INSERT INTO path_controls VALUES(-2,'[(1,2),(3,4)]')", d))
	run(fixture.Exec("ROLLBACK", 0))
}

func TestReferencePathControls(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE path_controls(id integer NOT NULL,p path DEFAULT path '[5,6]',CHECK(id>0 OR p=path '[1,2]'))", 0))
		run(fixture.Exec("CREATE VIEW path_control_view AS SELECT id,p,NULL::path AS n,p=path '((99,98),(97,96))' AS eq FROM path_controls", 0))
		run(fixture.Exec("INSERT INTO path_controls VALUES(1,'[(1,2),(3,4)]'),(2,'((1,2),(3,4))'),(3,'[(0.10000000000000002,-0)]'),(4,NULL)", 4))
		run(fixture.Exec("INSERT INTO path_controls(id) VALUES(5)", 1))
		checks := []fixture.Step{
			pathSelect(b, "SELECT id,p FROM path_controls ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, pathControlRows(false)),
			pathSelect(b, "SELECT p,n FROM path_control_view WHERE id=1", []string{"p", "n"}, []string{"PATH", "PATH"}, [][]any{{"[(1,2),(3,4)]", nil}}),
			fixture.Select("SELECT id,eq FROM path_control_view ORDER BY id", []string{"id", "eq"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}, {int64(3), false}, {int64(4), nil}, {int64(5), false}}),
		}
		for _, s := range checks {
			run(s)
			run(fixture.Prepared(s))
		}
		pathControlProbe(t, b, run)
		if b.PG {
			return
		} // Host replacement has no PostgreSQL API equivalent.
		run(fixture.Exec("SET extra_float_digits=0", 0))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,p FROM path_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		names, err := old.Columns()
		if err != nil {
			t.Fatal(err)
		}
		cols, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pathMetadata(t, false, old, []string{"INT4", "PATH"}, []bool{false, true})
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
		pathControlProbe(t, b, run)
		var got [][]any
		for old.Next() {
			var id int64
			var value any
			if err := old.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{id, value})
		}
		if err := old.Err(); err != nil {
			t.Fatal(err)
		}
		old.Close()
		if !reflect.DeepEqual(got, pathControlRows(true)) || !reflect.DeepEqual(names, []string{"id", "p"}) || cols[1].DatabaseTypeName() != "PATH" {
			t.Fatalf("retained rows %#v metadata %#v", got, names)
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("SET extra_float_digits=3", 0))
			for _, s := range checks {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
			}
			pathControlProbe(t, b, func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
		}
		verify()
		bad := "CREATE TABLE path_staged(p path); INSERT INTO path_staged VALUES('[1,2]'); INSERT INTO path_staged VALUES('[]');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type path: \"[]\"")
		d.Position = fixture.Position(strings.Index(bad, "'[]'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		verify()
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatalf("logical replay changed exact coordinates or stored objects\nbefore:\n%s\nafter:\n%s", dump.Bytes(), redump.Bytes())
		}
	})
}

func TestReferencePathFeatureSmoke(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE path_smoke(id integer,p path)", 0))
		q := "INSERT INTO path_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, arg := range []any{"[(1,2),(3,4)]", "((1,2),(3,4))", "[5,6]", nil} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, arg))
		}
		run(pathSelect(b, "SELECT id,p FROM path_smoke ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, [][]any{{int64(1), "[(1,2),(3,4)]"}, {int64(2), "((1,2),(3,4))"}, {int64(3), "[(5,6)]"}, {int64(4), nil}}))
		run(fixture.Select("SELECT path '[(1,2),(3,4)]'=path '((9,8),(7,6))' AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type path: \"[]\"")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, 5, "[]")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Select("SELECT count(*) AS n FROM path_smoke", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, "(7,8)"))
	})
}
