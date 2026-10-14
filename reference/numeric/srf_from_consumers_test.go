package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSRFFromConsumers(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE srf_from_input(id integer,hi integer)", 0),
			fixture.Exec("INSERT INTO srf_from_input VALUES(1,2),(2,1),(3,0)", 3),
			fixture.Exec("CREATE VIEW srf_from_view AS SELECT id,v,n FROM srf_from_input s LEFT JOIN LATERAL generate_series(1,s.hi) WITH ORDINALITY g(v,n) ON true", 0),
			fixture.Exec("CREATE TABLE srf_from_data AS SELECT * FROM srf_from_view", 4),
			fixture.Exec("CREATE TABLE srf_from_inserted(id integer,v integer,n bigint)", 0),
			fixture.Exec("INSERT INTO srf_from_inserted SELECT * FROM srf_from_view", 4),
			fixture.Exec("CREATE VIEW srf_from_mixed AS SELECT * FROM ROWS FROM(generate_series(1,3),string_to_table('x,y',','),generate_subscripts('[0:1]={NULL,NULL}'::integer[],1)) WITH ORDINALITY g(v,s,i,n)", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, table := range []string{"srf_from_view", "srf_from_data", "srf_from_inserted"} {
			seriesBoth(t, p, b, c, fixture.Select("SELECT * FROM "+table+" ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(1)}, {int64(3), nil, nil}}))
		}
		seriesBoth(t, p, b, c, fixture.Select("SELECT * FROM srf_from_mixed ORDER BY n", []string{"v", "s", "i", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, [][]any{{int64(1), "x", int64(0), int64(1)}, {int64(2), "y", int64(1), int64(2)}, {int64(3), nil, nil, int64(3)}}))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE srf_from_sink(v integer)", 0),
			fixture.Exec("INSERT INTO srf_from_sink VALUES(99)", 1),
			fixture.Exec("CREATE TABLE srf_from_steps(id integer,step integer)", 0),
			fixture.Exec("INSERT INTO srf_from_steps VALUES(1,1),(2,0)", 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, q := range []string{
			"INSERT INTO srf_from_sink SELECT v FROM (SELECT step FROM srf_from_steps ORDER BY id) s,generate_series(1,2,s.step) g(v)",
			"CREATE TABLE srf_from_failed AS SELECT v FROM (SELECT step FROM srf_from_steps ORDER BY id) s,generate_series(1,2,s.step) g(v)",
		} {
			t.Run(q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM srf_from_sink", []string{"v"}, []string{"INT4"}, seriesRows(99)))
					const missing = "SELECT * FROM srf_from_failed"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(missing, seriesDiagnostic(missing, "srf_from_failed", "42P01", `relation "srf_from_failed" does not exist`, "")))
				}
			})
		}
	})
}

func TestReferenceSRFFromLifecycle(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const heldSQL = "SELECT * FROM generate_series(1,2) WITH ORDINALITY g(v,n) ORDER BY n"
		st, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: heldSQL}
		const huge = "SELECT * FROM generate_series(1::bigint,9223372036854775807::bigint) g(v)"
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				if b.PG {
					seriesServerCancel(t, p, b, c, huge, prepared)
				} else {
					seriesNativeCancel(t, p, c, huge, prepared, true)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(heldSQL, []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
			})
		}
		if b.PG {
			return
		}
		for _, expired := range []bool{false, true} {
			var ctx context.Context
			var cancel context.CancelFunc
			want := error(context.Canceled)
			if expired {
				ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(p.Ctx)
				cancel()
			}
			rows, err := c.QueryContext(ctx, huge)
			cancel()
			if rows != nil {
				rows.Close()
				t.Fatal("published pre-canceled FROM cursor")
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		rows, err := c.QueryContext(ctx, heldSQL)
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
		var v, n int64
		if err := rows.Scan(&v, &n); err != nil {
			t.Fatal(err)
		}
		cancel()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal(rows.Err())
		}
		rows.Close()
		rows.Close()
		if v != 1 || n != 1 || len(cols) != 2 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != "INT4" || cols[1].Name() != "n" || cols[1].DatabaseTypeName() != "INT8" {
			t.Fatal("retained rows/schema changed", v, n, cols)
		}
	})
}
