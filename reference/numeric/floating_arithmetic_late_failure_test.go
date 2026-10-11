package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFloatingArithmeticLateFailures(t *testing.T) {
	for _, tc := range []struct {
		name, expr, secondA, secondB, code, message string
		first, secondValueA, secondValueB           float64
	}{
		{"division", "a/b", "1", "0", "22012", "division by zero", 2, 1, 0},
		{"overflow", "a*b", "1e308", "2", "22003", "value out of range: overflow", 8, 1e308, 2},
		{"underflow", "a*b", "1e-200", "1e-200", "22003", "value out of range: underflow", 8, 1e-200, 1e-200},
		{"power_domain", "a^b", "-2", "0.5", "2201F", "a negative number raised to a non-integer power yields a complex result", 16, -2, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			referenceFloatingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { referenceFloatingRun(t, p, b, s) }
				run(fixture.Exec("CREATE TABLE late(id INT,a DOUBLE PRECISION,b DOUBLE PRECISION)", 0))
				run(fixture.Exec("INSERT INTO late VALUES(1,'4','2'),(2,'"+tc.secondA+"','"+tc.secondB+"')", 2))
				run(fixture.Exec("CREATE TABLE destination(id INT,d DOUBLE PRECISION)", 0))
				run(fixture.Exec("INSERT INTO destination VALUES(99,'99')", 1))
				source := fixture.Select("SELECT id,a,b FROM late ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "FLOAT8", "FLOAT8"}, [][]any{{int64(1), float64(4), float64(2)}, {int64(2), tc.secondValueA, tc.secondValueB}})
				sentinel := fixture.Select("SELECT id,d FROM destination ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(99)}})
				for _, prepared := range []bool{false, true} {
					// This independent successful prefix proves the ordered source's first
					// expression is valid, rather than an error before any row can evaluate.
					referenceScalarFloatQuery(t, p.Ctx, b.DB, b.Recorder, referenceScalarFloatCase{tc.name, "SELECT " + tc.expr + " AS result FROM late WHERE id=1", "FLOAT8", tc.first, 0}, prepared)
					query := "SELECT " + tc.expr + " AS result FROM late ORDER BY id"
					func() {
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, e := b.DB.PrepareContext(p.Ctx, query)
							if e != nil {
								t.Fatal("runtime failure moved to Prepare", e)
							}
							defer stmt.Close()
							rows, err = stmt.QueryContext(p.Ctx)
						} else {
							rows, err = b.DB.QueryContext(p.Ctx, query)
						}
						if !b.PG && rows != nil {
							rows.Close()
							t.Fatal("late native SELECT failure exposed cursor")
						}
						if rows != nil {
							defer rows.Close()
							names, e := rows.Columns()
							if e != nil || len(names) != 1 || names[0] != "result" {
								t.Fatal(names, e)
							}
							meta, e := rows.ColumnTypes()
							if e != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "FLOAT8" {
								t.Fatal(meta, e)
							}
							count := 0
							for rows.Next() {
								var value float64
								if e := rows.Scan(&value); e != nil {
									t.Fatal(e)
								}
								if count != 0 || !referenceScalarFloatMatches(value, tc.first, 0) {
									t.Fatal("unexpected successful stream prefix", count, value)
								}
								count++
							}
							err = rows.Err()
						}
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
						fixture.AssertDiagnostic(t, err, &want)
					}()
					for _, query := range []string{"INSERT INTO destination SELECT id," + tc.expr + " FROM late ORDER BY id", "UPDATE late SET a=" + tc.expr} {
						step := fixture.ImplicitError(query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
						step.Prepared, step.ErrorStage = prepared, "execute"
						run(step)
						run(sentinel)
						run(source)
					}
				}
			})
		})
	}
}
