package reference_test

import (
	"database/sql/driver"
	"io"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneViewsCursorCapture(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev='IST'"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", "02:00:00", false}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET timezone_abbreviations='India'", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", "05:30:00", false}}))
		if b.PG {
			return
		} // Native eager cursor lifetime, not a pq lazy-stream claim.
		conn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.Raw(func(raw any) error {
			exec, queryer := raw.(driver.ExecerContext), raw.(driver.QueryerContext)
			for _, sql := range []string{"SET TIME ZONE 'UTC'", "SET timezone_abbreviations='Default'", "SET IntervalStyle='postgres'"} {
				if _, err := exec.ExecContext(p.Ctx, sql, nil); err != nil {
					return err
				}
			}
			old, err := queryer.QueryContext(p.Ctx, query, nil)
			if err != nil {
				return err
			}
			defer old.Close()
			for _, sql := range []string{"SET timezone_abbreviations='India'", "SET IntervalStyle='iso_8601'"} {
				if _, err := exec.ExecContext(p.Ctx, sql, nil); err != nil {
					return err
				}
			}
			check := func(rows driver.Rows, interval string) error {
				if !reflect.DeepEqual(rows.Columns(), []string{"abbrev", "utc_offset", "is_dst"}) {
					t.Fatal(rows.Columns())
				}
				metadata := rows.(driver.RowsColumnTypeDatabaseTypeName)
				for i, name := range []string{"TEXT", "INTERVAL", "BOOL"} {
					if metadata.ColumnTypeDatabaseTypeName(i) != name {
						t.Fatal("raw schema changed", i)
					}
				}
				values := make([]driver.Value, 3)
				if err := rows.Next(values); err != nil {
					return err
				}
				if !reflect.DeepEqual(values, []driver.Value{"IST", interval, false}) {
					t.Fatal(values)
				}
				if err := rows.Next(values); err != io.EOF {
					t.Fatal(err)
				}
				return rows.Close()
			}
			if err := check(old, "02:00:00"); err != nil {
				return err
			}
			fresh, err := queryer.QueryContext(p.Ctx, query, nil)
			if err != nil {
				return err
			}
			defer fresh.Close()
			return check(fresh, "PT5H30M")
		}); err != nil {
			t.Fatal(err)
		}
	})
}
