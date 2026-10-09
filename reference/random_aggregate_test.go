package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func TestReferenceRandomCountStageEmptyAndGroups(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE counts(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO counts VALUES(1),(2),(2)", 3))
	p.step(t, referenceExec("CREATE TABLE no_counts(id INT)", 0))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.ctx, c, backend, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
			for _, vector := range []referenceStep{
				referenceSelect("SELECT random(count(*),count(*)) FROM counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
				referenceSelect("SELECT random(count(*),count(*)) FROM no_counts", []string{"random"}, []string{"INT8"}, [][]any{{int64(0)}}),
				referenceSelect("SELECT id,random(count(*),count(*)) FROM counts GROUP BY id ORDER BY id", []string{"id", "random"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			} {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						randomReseed(t, p, c, backend, .25)
						step := vector
						step.prepared = prepared
						step.selectRows = query
						if !query {
							step.affected = int64(len(vector.rows))
						}
						referenceRunStep(t, p.ctx, c, backend.recorder, step)
						if draw() != first {
							t.Fatal("equal count bounds consumed draw")
						}
					}
				}
			}
		})
	}
}
