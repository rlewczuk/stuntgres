package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomFailedInsertRetainsDraw(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE random_fail_source(id INT)", 0), fixture.Exec("INSERT INTO random_fail_source VALUES(1)", 1), fixture.Exec("CREATE TABLE random_fail_sink(id INT)", 0), fixture.Exec("INSERT INTO random_fail_sink VALUES(99)", 1)} {
		p.Step(t, s)
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.Ctx, c, b, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
			values := randomRead(t, p.Ctx, c, b, "SELECT random(2,3)", []string{"random"}, []string{"INT4"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
			stmt, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal("prepare invoked provider", e)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, prepared := range []bool{false, true} {
				randomReseed(t, p, c, b, .25)
				var executor fixture.Executor = c
				if prepared {
					executor = held
				}
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "lower bound must be less than or equal to upper bound"})
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
				if draw() != expected {
					t.Fatal("failed INSERT rewound inner")
				}
				sentinel := fixture.Select("SELECT id FROM random_fail_sink ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}})
				sentinel.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal("sentinel int64 scan", err)
					}
					if rowIndex != 0 || id != 99 {
						t.Fatal("sentinel typed value", rowIndex, id)
					}
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, sentinel)
			}
		})
	}
}
