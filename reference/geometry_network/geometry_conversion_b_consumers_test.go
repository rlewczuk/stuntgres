package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGeometryConversionBConsumers(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_b_source(id integer NOT NULL,b box,c circle,l lseg,p polygon)", 0))
		run(fixture.Exec("CREATE VIEW geom_b_view AS SELECT id,lseg(b) AS lseg_box,lseg(point '(-1,0)',point '(1,0)') AS lseg_points,path(p) AS path_polygon,point(23.4,-44.5) AS coordinates,point(b) AS box_center,point(c) AS circle_center,point(l) AS lseg_center,point(p) AS polygon_center FROM geom_b_source", 0))
		run(fixture.Exec("CREATE TABLE geom_b_default(id integer NOT NULL,v point DEFAULT point(23.4,-44.5),CONSTRAINT geom_b_check CHECK(point(lseg '[(0,0),(2,2)]') ~= point '(1,1)'))", 0))
		var nullExprs, nullNames, nullTypes []string
		var nullRow []any
		for _, tc := range geometryConversionBCases {
			for mask := 1; mask < 1<<len(tc.formals); mask++ {
				name := fmt.Sprintf("%s_%d", tc.name, mask)
				nullExprs = append(nullExprs, geometryConversionBNullExpression(tc, mask)+" AS "+name)
				nullNames = append(nullNames, name)
				nullTypes = append(nullTypes, tc.typ)
				nullRow = append(nullRow, nil)
			}
		}
		run(fixture.Exec("CREATE VIEW geom_b_nulls AS SELECT "+strings.Join(nullExprs, ","), 0))
		run(fixture.Exec("CREATE VIEW geom_b_original_recipe AS SELECT point('(1,2)') AS v", 0))
		run(fixture.Exec("CREATE FUNCTION public.point(text) RETURNS point LANGUAGE SQL AS 'SELECT point ''(9,9)'''", 0))
		insert := func(run func(fixture.Step), id int) {
			run(fixture.Exec(fmt.Sprintf("INSERT INTO geom_b_source VALUES(%d,box '(1,0),(-1,0)','<(0,0),2>'::circle,lseg '[(-1,0),(1,0)]',polygon '((0,0),(1,1),(2,0))')", id), 1))
			run(fixture.Exec(fmt.Sprintf("INSERT INTO geom_b_default(id) VALUES(%d)", id), 1))
		}
		insert(run, 1)
		verify := func(run func(fixture.Step), backend fixture.Backend) {
			var names, types []string
			var row []any
			for _, tc := range geometryConversionBCases {
				names = append(names, tc.name)
				types = append(types, tc.typ)
				row = append(row, tc.want)
			}
			for _, s := range []fixture.Step{
				geometryConversionSelect(backend, "SELECT "+strings.Join(names, ",")+" FROM geom_b_view WHERE id=1", names, types, [][]any{row}),
				geometryConversionSelect(backend, "SELECT v FROM geom_b_default WHERE id=1", []string{"v"}, []string{"POINT"}, [][]any{{"(23.4,-44.5)"}}),
				geometryConversionSelect(backend, "SELECT * FROM geom_b_nulls", nullNames, nullTypes, [][]any{nullRow}),
				geometryConversionSelect(backend, "SELECT v FROM geom_b_original_recipe", []string{"v"}, []string{"POINT"}, [][]any{{"(1,2)"}}),
			} {
				run(s)
				run(fixture.Prepared(s))
			}
			// New data after restoration must execute stored expressions, not merely
			// reproduce the values that happened to be present in the dump.
			run(fixture.Exec("BEGIN", 0))
			insert(run, 2)
			run(geometryConversionSelect(backend, "SELECT "+strings.Join(names, ",")+" FROM geom_b_view WHERE id=2", names, types, [][]any{row}))
			run(geometryConversionSelect(backend, "SELECT v FROM geom_b_default WHERE id=2", []string{"v"}, []string{"POINT"}, [][]any{{"(23.4,-44.5)"}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT count(*) AS n FROM geom_b_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		verify(run, b)
		if b.PG {
			return
		} // Explicit native host controls; the SQL body above is shared.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT coordinates FROM geom_b_view WHERE id=1")
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
		verify(run, b)
		retained := geometryConversionSelect(b, "", []string{"coordinates"}, []string{"POINT"}, [][]any{{"(23.4,-44.5)"}})
		retained.CheckMetadata(t, old)
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var value any
		if err := old.Scan(&value); err != nil {
			t.Fatal(err)
		}
		retained.CheckRaw(t, 0, 0, value)
		if fixture.Normalize(value) != "(23.4,-44.5)" {
			t.Fatalf("retained: %v", value)
		}
		if old.Next() || old.Err() != nil {
			t.Fatalf("retained exhaustion: %v", old.Err())
		}
		old.Close()
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }, b)
		if p.Postgres != nil {
			defer func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("emitted SQL replay: %v", err)
			}
			verify(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }, fixture.Backend{PG: true})
		}
	})
}

func TestReferenceGeometryConversionBFeatureSmoke(t *testing.T) {
	geometryConversionBScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		s := geometryConversionBCombined(b)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		for _, tc := range []struct {
			q, typ string
			args   [][]any
			wants  []any
		}{
			{"SELECT point($1::float8,$2::float8) AS v", "POINT", [][]any{{23.4, -44.5}, {nil, -44.5}, {1, 2}}, []any{"(23.4,-44.5)", nil, "(1,2)"}},
			{"SELECT path($1::polygon) AS v", "PATH", [][]any{{"((0,0),(0,0),(6,0))"}, {"((2,3))"}, {nil}}, []any{"((0,0),(0,0),(6,0))", "((2,3))", nil}},
		} {
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
			for i, args := range tc.args {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.wants[i]}}, args...))
				if tc.typ == "POINT" && i == 1 {
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type double precision: \"bad\"")
					d.Context = "unnamed portal parameter $1 = '...'"
					s := fixture.ErrorDiagnostic(tc.q, d, "bad", 2)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
		}
		q := "SELECT point($1::polygon) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, boxError(q, "22003", "value out of range: overflow", "", "", false, true, "((1e308,0),(-1e308,0))"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, geometryConversionSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{"(2,3)"}}, "((2,3))"))
	})
}
