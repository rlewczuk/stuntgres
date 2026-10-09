package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func TestReferenceRandomFailedInsertRetainsDraw(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE random_fail_source(id INT)", 0), referenceExec("INSERT INTO random_fail_source VALUES(1)", 1), referenceExec("CREATE TABLE random_fail_sink(id INT)", 0), referenceExec("INSERT INTO random_fail_sink VALUES(99)", 1)} {
		p.step(t, s)
	}
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.ctx, c, b, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
					if err := rows.Scan(&value); err != nil {
						t.Fatal("failure continuation float64 scan", err)
					}
					if rowIndex != 0 {
						t.Fatal("failure continuation extra row", rowIndex)
					}
				})
				if len(values) != 1 || values[0][0] != value {
					t.Fatal("failure continuation typed/raw mismatch", values, value)
				}
				if math.IsNaN(value) || value < 0 || value >= 1 {
					t.Fatal("failure continuation range", value)
				}
				return value
			}
			randomReseed(t, p, c, b, .25)
			var inner int64
			values := randomRead(t, p.ctx, c, b, "SELECT random(2,3)", []string{"random"}, []string{"INT4"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
				if err := rows.Scan(&inner); err != nil {
					t.Fatal("inner int64 scan", err)
				}
				if rowIndex != 0 {
					t.Fatal("inner extra row", rowIndex)
				}
			})
			if len(values) != 1 || values[0][0] != inner {
				t.Fatal("inner typed/raw mismatch", values, inner)
			}
			if inner < 2 || inner > 3 {
				t.Fatal(inner)
			}
			expected := draw()
			q := "INSERT INTO random_fail_sink SELECT random(random(2,3),1) FROM random_fail_source"
			stmt, e := c.PrepareContext(p.ctx, q)
			if e != nil {
				t.Fatal("prepare invoked provider", e)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			for _, prepared := range []bool{false, true} {
				randomReseed(t, p, c, b, .25)
				var executor referenceExecutor = c
				if prepared {
					executor = held
				}
				step := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: "lower bound must be less than or equal to upper bound"})
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, executor, b.recorder, step)
				if draw() != expected {
					t.Fatal("failed INSERT rewound inner")
				}
				sentinel := referenceSelect("SELECT id FROM random_fail_sink ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}})
				sentinel.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal("sentinel int64 scan", err)
					}
					if rowIndex != 0 || id != 99 {
						t.Fatal("sentinel typed value", rowIndex, id)
					}
				}
				referenceRunStep(t, p.ctx, c, b.recorder, sentinel)
			}
		})
	}
}
