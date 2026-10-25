package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryConversionRecipeSelect(b fixture.Backend, source string) fixture.Step {
	row := []any{"[1:2]", "<(1,2),-1>", "<(3,4),-2>", float64(-1), float64(-2)}
	return geometryConversionSelect(b, "SELECT array_dims(a) AS dims,(a[1]).c AS first,(a[2]).c AS second,radius((a[1]).c) AS r1,radius((a[2]).c) AS r2 FROM "+source, []string{"dims", "first", "second", "r1", "r2"}, []string{"TEXT", "CIRCLE", "CIRCLE", "FLOAT8", "FLOAT8"}, [][]any{row})
}

func TestReferenceGeometryConversionStoredRecipes(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		const recipe = "ARRAY[ROW(circle(point '(1,2)',-1))::geom_recipe_record,ROW(circle(point '(3,4)',-2))::geom_recipe_record]"
		run(fixture.Exec("CREATE TYPE geom_recipe_record AS (c circle)", 0))
		run(fixture.Exec("CREATE TABLE geom_recipe_table(id integer NOT NULL,a geom_recipe_record[] DEFAULT "+recipe+")", 0))
		run(fixture.Exec("CREATE VIEW geom_recipe_view AS SELECT "+recipe+" AS a", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW geom_recipe_snapshot AS SELECT "+recipe+" AS a", 1))
		run(fixture.Exec("INSERT INTO geom_recipe_table(id) VALUES(1)", 1))
		probe := func(run func(fixture.Step), backend fixture.Backend) {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO geom_recipe_table(id) VALUES(80)", 1))
			s := geometryConversionRecipeSelect(backend, "geom_recipe_table WHERE id=80")
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("ROLLBACK", 0))
		}
		verify := func(run func(fixture.Step), backend fixture.Backend) {
			for _, source := range []string{"geom_recipe_table WHERE id=1", "geom_recipe_view", "geom_recipe_snapshot"} {
				s := geometryConversionRecipeSelect(backend, source)
				run(s)
				run(fixture.Prepared(s))
			}
			probe(run, backend)
		}
		verify(run, b)
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
		verify(run, b) // Persisted materialized data is read without REFRESH.
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
				t.Fatal(err)
			}
			pg := fixture.Backend{PG: true}
			pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
			pgRun(fixture.MaterializedViewUnpopulated("geom_recipe_snapshot"))
			pgRun(fixture.Exec("REFRESH MATERIALIZED VIEW geom_recipe_snapshot", 0))
			// Portable constructor SQL preserves persisted rows as well as
			// view/default definitions; REFRESH uses the same real payload.
			verify(pgRun, pg)
		}
	})
}
