package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryConversionShapeCheck(relation string) fixture.Step {
	row := []any{"[-2:-1][3:4]", int64(4), float64(-1), float64(1), float64(1), float64(-2)}
	return fixture.Select("SELECT array_dims(a) AS dims,cardinality(a) AS n,radius((a[-2][3]).c) AS first,radius((a[-2][4]).c) AS second,radius((a[-1][3]).c) AS third,radius((a[-1][4]).c) AS last FROM "+relation, []string{"dims", "n", "first", "second", "third", "last"}, []string{"TEXT", "INT4", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{row})
}

func TestReferenceGeometryConversionCarrierShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE geom_conversion_shape_record AS (c circle)", 0))
			run(fixture.Exec("CREATE TABLE geom_conversion_shape(a geom_conversion_shape_record[])", 0))
			run(fixture.Exec("INSERT INTO geom_conversion_shape VALUES(array_fill(ROW('<(0,0),1>'::circle)::geom_conversion_shape_record,ARRAY[2,2],ARRAY[-2,3]))", 1))
			run(fixture.Exec("UPDATE geom_conversion_shape SET a[-2][3]=ROW(circle(point '(0,0)',-1))::geom_conversion_shape_record,a[-1][4]=ROW(circle(point '(0,0)',-2))::geom_conversion_shape_record", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW geom_conversion_shape_snapshot AS SELECT a FROM geom_conversion_shape", 1))
			verify := func(run func(fixture.Step)) {
				for _, relation := range []string{"geom_conversion_shape", "geom_conversion_shape_snapshot"} {
					s := geometryConversionShapeCheck(relation)
					run(s)
					run(fixture.Prepared(s))
				}
			}
			verify(run)
			if b.PG {
				return
			} // Native exact snapshot metadata has no PG API equivalent.
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
			verify(run) // In particular, read the materialized snapshot before any REFRESH.
			target := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			verify(func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
			if p.Postgres != nil {
				// Match the existing geometry control convention: the borrowed
				// oracle replay must not publish objects into the next backend.
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
				pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
				// Deep constructor SQL preserves the real table payload and
				// bounds; PostgreSQL still has no native snapshot-control API.
				pgRun(geometryConversionShapeCheck("geom_conversion_shape"))
				pgRun(fixture.MaterializedViewUnpopulated("geom_conversion_shape_snapshot"))
				pgRun(fixture.Exec("REFRESH MATERIALIZED VIEW geom_conversion_shape_snapshot", 0))
				pgRun(geometryConversionShapeCheck("geom_conversion_shape_snapshot"))
			}
		})
	}
}
