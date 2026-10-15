package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNumericSeriesLifecycle(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const huge = "SELECT generate_series(0.1,1e100,0.1) AS v"
		const small = "SELECT generate_series($1,0.3::numeric,0.1) AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, small)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: small}
		for _, prepared := range []bool{false, true} {
			if b.PG {
				seriesServerCancel(t, p, b, c, huge, prepared)
			} else {
				for _, query := range []bool{false, true} {
					seriesNativeCancel(t, p, c, huge, prepared, query, "NumericSeriesCursor).Next")
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(small, []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.2", "0.3"), "0.1"))
		}
		if b.PG {
			return
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		rows, err := c.QueryContext(ctx, "SELECT generate_series(-0.1,0.1,0.1) AS v ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var saved string
		if err := rows.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		cancel()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("retained cancellation", rows.Err())
		}
		rows.Close()
		rows.Close()
		if saved != "-0.1" || len(cols) != 1 || cols[0].DatabaseTypeName() != "NUMERIC" {
			t.Fatal(saved, cols)
		}
	})
}

func numericSeriesAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, withCancel bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE numeric_series_sink(v numeric)", 0))
	run(fixture.Exec("INSERT INTO numeric_series_sink VALUES(99)", 1))
	run(fixture.Exec("CREATE TABLE numeric_series_input(id integer,step numeric)", 0))
	run(fixture.Exec("INSERT INTO numeric_series_input VALUES(1,0.1),(2,0)", 2))
	check := func() {
		run(fixture.Select("SELECT v FROM numeric_series_sink", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("99")))
		run(fixture.ImplicitError("SELECT * FROM numeric_series_failed", fixture.Diagnostic{Code: "42P01", Message: `relation "numeric_series_failed" does not exist`, Position: "15"}))
	}
	for _, special := range []struct{ input, message string }{{"0", "step size cannot equal zero"}, {"'NaN'::numeric", "step size cannot be NaN"}} {
		run(fixture.Exec("UPDATE numeric_series_input SET step="+special.input+" WHERE id=2", 1))
		for _, q := range []string{
			"INSERT INTO numeric_series_sink SELECT generate_series(0.1,0.2,step) FROM (SELECT step FROM numeric_series_input ORDER BY id) s",
			"CREATE TABLE numeric_series_failed AS SELECT generate_series(0.1,0.2,step) AS v FROM (SELECT step FROM numeric_series_input ORDER BY id) s",
		} {
			s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", special.message, ""))
			s.ErrorStage = "execute"
			seriesBoth(t, p, b, c, s)
			check()
		}
		// PostgreSQL can stream a prefix; native must publish no cursor.
		q := "SELECT generate_series(0.1,0.2,step) FROM (SELECT step FROM numeric_series_input ORDER BY id) s"
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var st *sql.Stmt
			var err error
			if prepared {
				st, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = st.QueryContext(p.Ctx)
				defer st.Close()
			} else {
				rows, err = c.QueryContext(p.Ctx, q)
			}
			if rows != nil {
				if !b.PG {
					rows.Close()
					t.Fatal("native published late-failure prefix")
				}
				for rows.Next() {
					var v string
					if e := rows.Scan(&v); e != nil {
						rows.Close()
						t.Fatal(e)
					}
				}
				err = rows.Err()
				rows.Close()
			}
			if st != nil {
				st.Close()
			}
			want := seriesDiagnostic(q, "", "22023", special.message, "")
			fixture.AssertDiagnostic(t, err, &want)
		}
	}
	if !withCancel {
		return
	}
	for _, q := range []string{
		"INSERT INTO numeric_series_sink SELECT generate_series(0.1,1e100,0.1)",
		"CREATE TABLE numeric_series_failed AS SELECT generate_series(0.1,1e100,0.1) AS v",
	} {
		for _, prepared := range []bool{false, true} {
			if b.PG {
				seriesServerCancel(t, p, b, c, q, prepared)
			} else {
				seriesNativeCancel(t, p, c, q, prepared, false, "NumericSeriesCursor).Next")
			}
			check()
		}
	}
}

func TestReferenceNumericSeriesAtomicity(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		numericSeriesAtomicity(t, p, b, c, true)
	})
}
