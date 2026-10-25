package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryTransformStoredChecks(t *testing.T, b fixture.Backend, storedBox string, run func(fixture.Step)) {
	t.Helper()
	columns := []string{"id", "p", "bt", "qt", "ct", "joined", "divided"}
	types := []string{"INT4", "POINT", "BOX", "PATH", "CIRCLE", "PATH", "POINT"}
	want := [][]any{
		{int64(1), "(-5,10)", "(2,3),(0,2)", "[(-4,2),(-8,6)]", "<(1,-0.5),1>", "[(1,2),(3,4),(9,9)]", "(2.5,7.5)"},
		{int64(2), nil, nil, "((-4,2),(-8,6))", nil, nil, nil},
		{int64(3), "(-5,10)", storedBox, nil, nil, nil, "(2.5,7.5)"},
	}
	for _, suffix := range []string{" ORDER BY id", " WHERE FALSE"} {
		rows := want
		if suffix == " WHERE FALSE" {
			rows = nil
		}
		s := geometryTransformSelect(b, "SELECT id,p,bt,qt,ct,joined,divided FROM geom_transform_view"+suffix, columns, types, rows)
		run(s)
		run(fixture.Prepared(s))
	}
	q := "SELECT id,p,bt,qt,ct,joined,divided FROM geom_transform_view WHERE id=$1"
	// Fresh prepared route plus replay writes exercise reconstructed expressions.
	run(fixture.Prepared(geometryTransformSelect(b, q, columns, types, [][]any{want[0]}, 1)))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_transform_stored(id,b,q,c) VALUES(7,'(2,1),(0,0)','[(1,2),(3,4)]','<(1,2),2>')", 1))
	newRow := append([]any(nil), want[0]...)
	newRow[0] = int64(7)
	run(geometryTransformSelect(b, q, columns, types, [][]any{newRow}, 7))
	run(fixture.Exec("UPDATE geom_transform_stored SET b[0]=point '(-5,-6)',q='((1,2),(3,4))',c=NULL WHERE id=7", 1))
	newRow = []any{int64(7), "(-5,10)", "(-5,-4),(0,2)", "((-4,2),(-8,6))", nil, nil, "(2.5,7.5)"}
	run(geometryTransformSelect(b, q, columns, types, [][]any{newRow}, 7))
	run(fixture.Exec("ROLLBACK", 0))
}

func TestReferenceGeometryTransformControls(t *testing.T) {
	geometryTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_transform_stored(id integer,p point DEFAULT (point '(1,2)' * point '(3,4)'),b box,q path,c circle,bt box GENERATED ALWAYS AS (b + point '(0,2)') STORED,qt path GENERATED ALWAYS AS (q * point '(0,2)') STORED,ct circle GENERATED ALWAYS AS (c / point '(0,2)') STORED)", 0))
		run(fixture.Exec("INSERT INTO geom_transform_stored(id,b,q,c) VALUES(1,'(2,1),(0,0)','[(1,2),(3,4)]','<(1,2),2>'),(3,'(3,4),(1,2)',NULL,NULL)", 2))
		run(fixture.Exec("INSERT INTO geom_transform_stored(id,p,q) VALUES(2,NULL,'((1,2),(3,4))')", 1))
		run(fixture.Exec("UPDATE geom_transform_stored SET b[0]=point '(-5,-6)' WHERE id=3", 1))
		run(fixture.Exec("CREATE VIEW geom_transform_view AS SELECT id,p,bt,qt,ct,q + path '[(9,9)]' AS joined,p / point '(1,1)' AS divided FROM geom_transform_stored", 0))
		geometryTransformStoredChecks(t, b, "(-5,-4),(1,4)", run)
		q := "SELECT qt AS v FROM geom_transform_stored WHERE id=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, id := range []int{1, 2, 3, 1} {
			var want any = "[(-4,2),(-8,6)]"
			if id == 2 {
				want = "((-4,2),(-8,6))"
			}
			if id == 3 {
				want = nil
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryTransformSelect(b, q, []string{"v"}, []string{"PATH"}, [][]any{{want}}, id))
		}
		stmt.Close()
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT qt AS v FROM geom_transform_stored WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("UPDATE geom_transform_stored SET q='[(8,9)]' WHERE id=1", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		geometryTransformStoredChecks(t, b, "(-5,-4),(1,4)", run)
		if !old.Next() {
			t.Fatal("lost retained PATH")
		}
		var got string
		if err := old.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "[(-4,2),(-8,6)]" {
			t.Fatalf("retained PATH %s", got)
		}
		cols, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != "PATH" {
			t.Fatalf("retained metadata %v", cols)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("invalid retained cursor")
		}
		old.Close()
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify := func() {
			geometryTransformStoredChecks(t, b, "(-5,-4),(1,4)", func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
		}
		verify()
		bad := "CREATE TABLE geom_transform_staged(p point); INSERT INTO geom_transform_staged VALUES(point '(1,2)' / point '(0,0)');"
		d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		verify()
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatalf("transform replay changed dump\nbefore:\n%s\nafter:\n%s", dump.Bytes(), redump.Bytes())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := p.Postgres.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			// Emitted SQL feeds the raw base BOX through PostgreSQL box_in,
			// which normalizes its corners before recomputing generated bt.
			// Native Restore above retains the exact raw component order.
			geometryTransformStoredChecks(t, fixture.Backend{PG: true}, "(1,4),(-5,-4)", func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
		}
	})
}
