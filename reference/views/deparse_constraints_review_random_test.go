package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseConstraintsReviewRandomConsumers(t *testing.T) {
	for _, tc := range []struct{ name, call, compact, pretty string }{
		{"uniform", "random()", "random()", "random()"},
		{"normal0", "random_normal()", "random_normal()", "random_normal()"},
		{"normal1", "random_normal(5)", "random_normal((5)::double precision)", "random_normal(5::double precision)"},
		{"normal2", "random_normal(5,2)", "random_normal((5)::double precision, (2)::double precision)", "random_normal(5::double precision, 2::double precision)"},
		{"normal_zero", "random_normal(5,0)", "random_normal((5)::double precision, (0)::double precision)", "random_normal(5::double precision, 0::double precision)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, _ bool, run func(fixture.Step) fixture.Observed) {
				seed := func() {
					var ignored any
					if err := conn.QueryRowContext(context.Background(), "SELECT setseed(0.25)").Scan(&ignored); err != nil {
						t.Fatal(err)
					}
				}
				draw := func(call string) float64 {
					var v float64
					if err := conn.QueryRowContext(context.Background(), "SELECT "+call).Scan(&v); err != nil {
						t.Fatal(err)
					}
					return v
				}
				seed()
				v := draw(tc.call)
				marker := draw("random()")
				if math.IsNaN(v) || math.IsInf(v, 0) || (tc.name == "uniform" && (v < 0 || v >= 1)) {
					t.Fatal("provider range", v)
				}
				if tc.name == "normal_zero" && v != 5 {
					t.Fatal("zero deviation", v)
				}
				seed()
				run(fixture.Exec("CREATE TABLE original(id INT,p DOUBLE PRECISION,CONSTRAINT c CHECK("+tc.call+">p))", 0))
				for mode, call := range []string{tc.compact, tc.pretty} {
					expr := "(" + call + " > p)"
					if mode == 1 {
						expr = call + " > p"
					}
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).Rows[0][0].(string)
					run(fixture.Prepared(step))
					run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,p DOUBLE PRECISION,CONSTRAINT c%d %s)", mode, mode, actual), 0))
				}
				if got := draw(tc.call); got != v {
					t.Fatal("deparse/Prepare/CREATE consumed state", got, v)
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "c"
					if i > 0 {
						constraint = fmt.Sprintf("c%d", i-1)
					}
					held, err := conn.PrepareContext(context.Background(), "INSERT INTO "+name+" VALUES($1,$2)")
					if err != nil {
						t.Fatal(err)
					}
					for _, prepared := range []bool{false, true} {
						for _, item := range []struct {
							id     int
							p      any
							reject bool
						}{{1, v - 1, false}, {2, v + 1, true}, {3, nil, false}} {
							seed()
							var result sql.Result
							var err error
							if prepared {
								result, err = held.ExecContext(context.Background(), item.id, item.p)
							} else {
								result, err = conn.ExecContext(context.Background(), "INSERT INTO "+name+" VALUES($1,$2)", item.id, item.p)
							}
							if item.reject {
								if result != nil {
									t.Fatal("error exposed result")
								}
								detail := fmt.Sprintf("%d, %s", item.id, strconv.FormatFloat(item.p.(float64), 'g', -1, 64))
								{
									want := fixture.CheckViolation(name, constraint, detail)
									want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
									fixture.AssertDiagnostic(t, err, &want)
								}
							} else if err != nil {
								t.Fatal(err)
							} else if n, _ := result.RowsAffected(); n != 1 {
								t.Fatal(n)
							}
							if got := draw("random()"); got != marker {
								t.Fatal("call state mismatch", item, got, marker)
							}
						}
						{
							rows, e := conn.QueryContext(context.Background(), "SELECT id,p FROM "+name+" ORDER BY id")
							if e != nil {
								t.Fatal(e)
							}
							for _, want := range []struct {
								id int
								p  any
							}{{1, v - 1}, {3, nil}} {
								if !rows.Next() {
									t.Fatal(rows.Err())
								}
								var id int
								var p any
								if e := rows.Scan(&id, &p); e != nil || id != want.id || !reflect.DeepEqual(p, want.p) {
									t.Fatal(id, p, want, e)
								}
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal(rows.Err())
							}
							rows.Close()
						}
						run(fixture.Exec("DELETE FROM "+name, 2))
						deparseRandomAtomicity(t, conn, name, constraint, tc.name, tc.call, prepared, seed, draw, run)
					}
					held.Close()
				}
			})
		})
	}
}

func TestReferenceDeparseConstraintsReviewRandomPresenceConsumers(t *testing.T) {
	for _, call := range []string{"random()", "random_normal()"} {
		t.Run(call, func(t *testing.T) {
			fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, _ bool, run func(fixture.Step) fixture.Observed) {
				seed := func() {
					var ignored any
					if err := conn.QueryRowContext(context.Background(), "SELECT setseed(0.25)").Scan(&ignored); err != nil {
						t.Fatal(err)
					}
				}
				draw := func(call string) float64 {
					var v float64
					if err := conn.QueryRowContext(context.Background(), "SELECT "+call).Scan(&v); err != nil {
						t.Fatal(err)
					}
					return v
				}
				seed()
				value := draw(call)
				marker := draw("random()")
				seed()
				run(fixture.Exec("CREATE TABLE original(id INT,CONSTRAINT c CHECK("+call+" IS NOT NULL))", 0))
				for mode, expr := range []string{"(" + call + " IS NOT NULL)", call + " IS NOT NULL"} {
					q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
					step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
					actual := run(step).Rows[0][0].(string)
					run(fixture.Prepared(step))
					run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,CONSTRAINT copied %s)", mode, actual), 0))
				}
				if got := draw(call); got != value {
					t.Fatal("presence construction effect", got, value)
				}
				for _, name := range []string{"original", "copy0", "copy1"} {
					for _, prepared := range []bool{false, true} {
						seed()
						step := fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1)
						step.Prepared = prepared
						run(step)
						if got := draw("random()"); got != marker {
							t.Fatal("presence call effect", got, marker)
						}
						run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Exec("DELETE FROM "+name, 1))
					}
				}
			})
		})
	}
}

func deparseRandomAtomicity(t *testing.T, conn *sql.Conn, name, constraint, kind, call string, prepared bool, seed func(), draw func(string) float64, run func(fixture.Step) fixture.Observed) {
	// Fixed thresholds isolate statement atomicity and attempted-row effects.
	good, bad := float64(-1), float64(2)
	if kind != "uniform" {
		good, bad = math.Inf(-1), math.Inf(1)
	}
	if kind == "normal_zero" {
		good, bad = 4, 5
	}
	goodSQL, badSQL := strconv.FormatFloat(good, 'g', -1, 64), strconv.FormatFloat(bad, 'g', -1, 64)
	if math.IsInf(good, -1) {
		goodSQL = "'-Infinity'"
		badSQL = "'Infinity'"
	}
	seed()
	draw(call)
	draw(call)
	afterBatch := draw("random()")
	seed()
	query := "INSERT INTO " + name + " VALUES(4," + goodSQL + "),(5," + badSQL + "),(6," + goodSQL + ")"
	var r sql.Result
	var e error
	if prepared {
		s, err := conn.PrepareContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		r, e = s.ExecContext(context.Background())
		s.Close()
	} else {
		r, e = conn.ExecContext(context.Background(), query)
	}
	if r != nil {
		t.Fatal("batch exposed result")
	}
	err := e
	detail := "5, " + strconv.FormatFloat(bad, 'g', -1, 64)
	if math.IsInf(bad, 1) {
		detail = "5, Infinity"
	}
	{
		want := fixture.CheckViolation(name, constraint, detail)
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		fixture.AssertDiagnostic(t, err, &want)
	}
	if got := draw("random()"); got != afterBatch {
		t.Fatal("failed batch effect frontier", got, afterBatch)
	}
	run(fixture.Select("SELECT id,p FROM "+name+" ORDER BY id", []string{"id", "p"}, []string{"INT4", "FLOAT8"}, [][]any{}))
	seed()
	run(fixture.Exec("INSERT INTO "+name+" VALUES(1,"+goodSQL+")", 1))
	seed()
	draw(call)
	afterUpdate := draw("random()")
	seed()
	query = "UPDATE " + name + " SET p=" + badSQL + " WHERE id=1"
	if prepared {
		s, e := conn.PrepareContext(context.Background(), query)
		if e != nil {
			t.Fatal(e)
		}
		_, err = s.ExecContext(context.Background())
		s.Close()
	} else {
		_, err = conn.ExecContext(context.Background(), query)
	}
	detail = "1, " + strconv.FormatFloat(bad, 'g', -1, 64)
	if math.IsInf(bad, 1) {
		detail = "1, Infinity"
	}
	{
		want := fixture.CheckViolation(name, constraint, detail)
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		fixture.AssertDiagnostic(t, err, &want)
	}
	if got := draw("random()"); got != afterUpdate {
		t.Fatal("failed update effect frontier", got, afterUpdate)
	}
	run(fixture.Select("SELECT id,p FROM "+name+" ORDER BY id", []string{"id", "p"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), good}}))
	run(fixture.Exec("DELETE FROM "+name, 1))
}
