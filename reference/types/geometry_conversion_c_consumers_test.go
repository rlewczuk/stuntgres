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

func TestReferenceGeometryConversionCConsumers(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE geom_c_source(id integer NOT NULL,b box,c circle,p path)", 0))
		run(fixture.Exec("CREATE VIEW geom_c_view AS SELECT id,polygon(b) AS polygon_box,polygon(p) AS polygon_path,polygon(c) AS polygon_circle,polygon(4,c) AS polygon_points,polygon(box '(1,1),(0,0)') AS constant_box,polygon(path '((0,0),(1,1),(2,0))') AS constant_path FROM geom_c_source", 0))
		run(fixture.Exec("CREATE TABLE geom_c_default(id integer NOT NULL,b polygon DEFAULT polygon(box '(1,1),(0,0)'),p polygon DEFAULT polygon(path '((0,0),(1,1),(2,0))'),c polygon DEFAULT polygon(circle '<(0,0),1>'),n polygon DEFAULT polygon(4,circle '<(0,0),1>'))", 0))
		run(fixture.Exec("CREATE TABLE geom_c_checked(id integer NOT NULL,p path,CONSTRAINT geom_c_check CHECK(polygon(p) ~= polygon '((0,0),(1,1),(2,0))'))", 0))
		var nullExprs, nullNames, nullTypes []string
		var nullRow []any
		for _, tc := range geometryConversionCCases {
			for mask := 1; mask < 1<<len(tc.formals); mask++ {
				name := fmt.Sprintf("%s_%d", tc.name, mask)
				nullExprs = append(nullExprs, geometryConversionBNullExpression(tc, mask)+" AS "+name)
				nullNames = append(nullNames, name)
				nullTypes = append(nullTypes, "POLYGON")
				nullRow = append(nullRow, nil)
			}
		}
		run(fixture.Exec("CREATE VIEW geom_c_nulls AS SELECT "+strings.Join(nullExprs, ","), 0))
		insert := func(run func(fixture.Step), id int) {
			run(fixture.Exec(fmt.Sprintf("INSERT INTO geom_c_source VALUES(%d,box '(1,1),(0,0)',circle '<(0,0),1>',path '((0,0),(1,1),(2,0))')", id), 1))
			run(fixture.Exec(fmt.Sprintf("INSERT INTO geom_c_default(id) VALUES(%d)", id), 1))
			run(fixture.Exec(fmt.Sprintf("INSERT INTO geom_c_checked VALUES(%d,path '((0,0),(1,1),(2,0))')", id), 1))
		}
		insert(run, 1)
		verify := func(run func(fixture.Step), backend, persistedOrigin fixture.Backend) {
			names := []string{"polygon_box", "polygon_path", "polygon_circle", "polygon_points", "constant_box", "constant_path"}
			types := []string{"POLYGON", "POLYGON", "POLYGON", "POLYGON", "POLYGON", "POLYGON"}
			row := []any{geometryConversionCCases[0].want, geometryConversionCCases[1].want, geometryConversionCCases[2].want, geometryConversionCCases[3].want, geometryConversionCCases[0].want, geometryConversionCCases[1].want}
			check := func(id int) {
				defaults := geometryConversionCProjection(backend, fmt.Sprintf("SELECT b,p,c,n FROM geom_c_default WHERE id=%d", id), []string{"b", "p", "c", "n"}, types[:4], [][]any{row[:4]})
				if id == 1 {
					// Dump emits existing non-generated cells as literal values:
					// their exact bits belong to the source, not the replay server.
					// Future row2 evaluates the restored default on the backend.
					persisted := make([]any, 4)
					for i := range persisted {
						persisted[i] = geometryConversionCExpected(persistedOrigin, row[i])
					}
					defaults.Rows = [][]any{persisted}
				}
				for _, s := range []fixture.Step{
					geometryConversionCProjection(backend, fmt.Sprintf("SELECT %s FROM geom_c_view WHERE id=%d", strings.Join(names, ","), id), names, types, [][]any{row}),
					defaults,
					geometryConversionSelect(backend, "SELECT * FROM geom_c_nulls", nullNames, nullTypes, [][]any{nullRow}),
				} {
					run(s)
					run(fixture.Prepared(s))
				}
			}
			check(1)
			d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_c_checked\" violates check constraint \"geom_c_check\"")
			d.Schema, d.Table, d.Constraint = "public", "geom_c_checked", "geom_c_check"
			d.Detail = "Failing row contains (9, ((9,9)))."
			run(fixture.ErrorDiagnostic("INSERT INTO geom_c_checked VALUES(9,path '((9,9))')", d))
			run(fixture.Exec("BEGIN", 0))
			insert(run, 2)
			check(2)
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT count(*) AS n FROM geom_c_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT count(*) AS n FROM geom_c_checked", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		verify(run, b, b)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(run, b, b)
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }, b, b)
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
				t.Fatalf("portable SQL replay: %v", err)
			}
			verify(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }, fixture.Backend{PG: true}, b)
		}
	})
}

// This is the held-statement recovery witness, not the separate executable smoke.
func TestReferenceGeometryConversionCFeatureSmoke(t *testing.T) {
	geometryConversionCScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT polygon($1::integer,$2::circle) AS v", "SELECT polygon($1::path) AS v"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				if strings.Contains(q, "integer") {
					run(geometryConversionCSelect(b, q, geometryConversionCCircle(4, 0, 0, 1), 4, "<(0,0),1>"))
					run(boxError(q, "22023", "must request at least 2 points", "", "", false, true, 1, "<(0,0),1>"))
					run(geometryConversionCSelect(b, q, nil, 0, []byte(nil)))
					run(boxError(q, "0A000", "cannot convert circle with radius zero to polygon", "", "", false, true, 0, "<(0,0),1e-6>"))
					run(geometryConversionCSelect(b, q, geometryConversionCCircle(2, 0, 0, 1), 2, "<(0,0),1>"))
				} else {
					run(geometryConversionCSelect(b, q, "((2,3))", "((2,3))"))
					run(boxError(q, "22023", "open path cannot be converted to polygon", "", "", false, true, "[(0,0),(1,1),(0,0)]"))
					run(geometryConversionCSelect(b, q, nil, []byte(nil)))
					run(geometryConversionCSelect(b, q, "((0,0),(1,1),(0,0))", "((0,0),(1,1),(0,0))"))
				}
			})
		}
	})
}
