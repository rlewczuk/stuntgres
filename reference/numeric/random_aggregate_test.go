package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomCountStageEmptyAndGroups(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE counts(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO counts VALUES(1),(2),(2)", 3))
	p.Step(t, fixture.Exec("CREATE TABLE no_counts(id INT)", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, e := backend.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.Ctx, c, backend, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
					if err := rows.Scan(&value); err != nil {
						t.Fatal("aggregate continuation float64 scan", err)
					}
					if rowIndex != 0 {
						t.Fatal("aggregate continuation extra row", rowIndex)
					}
				})
				if len(values) != 1 || values[0][0] != value {
					t.Fatal("aggregate continuation typed/raw mismatch", values, value)
				}
				if math.IsNaN(value) || value < 0 || value >= 1 {
					t.Fatal("aggregate continuation range", value)
				}
				return value
			}
			randomReseed(t, p, c, backend, .25)
			first := draw()
			for _, vector := range []fixture.Step{
				fixture.Select("SELECT random(count(*),count(*)) FROM counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
				fixture.Select("SELECT random(count(*),count(*)) FROM no_counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(0)}}),
				fixture.Select("SELECT id,random(count(*),count(*)) FROM counts GROUP BY id ORDER BY id", []string{"id", "random"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						randomReseed(t, p, c, backend, .25)
						step := vector
						step.Prepared = prepared
						step.SelectRows = query
						if !query {
							step.Affected = int64(len(vector.Rows))
						}
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
						if draw() != first {
							t.Fatal("equal count bounds consumed draw")
						}
					}
				}
			}
		})
	}
}
