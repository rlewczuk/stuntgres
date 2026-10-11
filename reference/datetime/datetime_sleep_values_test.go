package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Independently specified expectations verified against pinned PostgreSQL 18.0.
// No expectation is inferred from the native implementation.
func datetimeSleepScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'", "SET statement_timeout='2s'"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeSleepValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, null bool, args ...any) {
	t.Helper()
	var want any = ""
	if null {
		want = nil
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"result"}, []string{"VOID"}, [][]any{{want}}, args...))
}

func TestReferenceDatetimeSleepValues(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, expr := range []string{
			"pg_sleep(0)", "pg_sleep(-0.1)", "pg_sleep('NaN'::float8)", "pg_sleep('-Infinity'::float8)",
			"pg_sleep_for(interval '0')", "pg_sleep_for(interval '-1 second')", "pg_sleep_for(interval '-infinity')",
			"pg_sleep_until(timestamptz '2000-01-01 00:00:00+00')", "pg_sleep_until(timestamptz '-infinity')",
			"pg_catalog.pg_sleep(0)", `"pg_sleep"(0)`, "PG_SLEEP(0)", "pg_sleep(0::numeric)", "pg_sleep(0::float4)",
			"pg_sleep_until(date '2000-01-01')", "pg_sleep_until(timestamp '2000-01-01')",
		} {
			t.Run(expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT "+expr+" AS result", []string{"result"}, []string{"VOID"}, [][]any{{""}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, f := range []struct {
			name, typ string
			arg       any
		}{
			{"pg_sleep", "float8", float64(0)}, {"pg_sleep_for", "interval", "0 seconds"}, {"pg_sleep_until", "timestamptz", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		} {
			t.Run(f.name+"-parameters", func(t *testing.T) {
				q := "SELECT " + f.name + "($1) AS result"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				datetimeSleepValue(t, p, b, held, q, false, f.arg)
				datetimeSleepValue(t, p, b, held, q, true, nil)
				datetimeSleepValue(t, p, b, c, "SELECT "+f.name+"(NULL) AS result", true)
				datetimeSleepValue(t, p, b, c, "SELECT "+f.name+"(NULL::"+f.typ+") AS result", true)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+f.name+"(NULL::"+f.typ+") AS result WHERE false", []string{"result"}, []string{"VOID"}, [][]any{}))
			})
		}
		for _, expr := range []string{"pg_sleep(600)", "pg_sleep_for(interval '10 minutes')", "pg_sleep_until(timestamptz 'infinity')"} {
			t.Run(expr+"-prepare-does-not-wait", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
				defer cancel()
				stmt, err := c.PrepareContext(ctx, "SELECT "+expr)
				if err != nil {
					t.Fatal("Prepare evaluated delay", err)
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

func TestReferenceDatetimeSleepTiming(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q   string
			arg any
		}{
			{"SELECT pg_sleep($1) AS result", float64(0.075)},
			{"SELECT pg_sleep_for($1) AS result", "75 milliseconds"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
					var executor fixture.Executor = c
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, tc.q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
					}
					for range 2 {
						start := time.Now()
						datetimeSleepValue(t, p, b, executor, tc.q, false, tc.arg)
						if elapsed := time.Since(start); elapsed < 75*time.Millisecond {
							t.Fatalf("sleep completed early: %s < 75ms", elapsed)
						}
					}
				})
			}
		}
		t.Run("until-execution-clock", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			q := "SELECT pg_sleep_until($1) AS result"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for range 2 {
				target := time.Now().Add(100 * time.Millisecond).Truncate(time.Microsecond)
				datetimeSleepValue(t, p, b, held, q, false, target)
				if time.Now().Before(target) {
					t.Fatal("until returned before the captured absolute target")
				}
			}
		})
	})
}

func TestReferenceDatetimeSleepCalendar(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, zone := range []string{"UTC", "America/New_York"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
			for _, expr := range []string{"pg_sleep_for(interval '-1 month 1 day')", "pg_sleep_for(interval '-1 day 1 second')", "pg_sleep_until(timestamptz '2000-01-01 01:00:00+01')"} {
				datetimeSleepValue(t, p, b, c, "SELECT "+expr+" AS result", false)
			}
		}
	})
}
