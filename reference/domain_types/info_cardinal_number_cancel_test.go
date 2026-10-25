package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCardinalNumberPrecancel(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, read := range []bool{false, true} {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			accepted := func(err error) bool {
				return errors.Is(err, context.Canceled) || backend.PG && errors.Is(err, driver.ErrBadConn)
			}
			if !read {
				result, err := conn.ExecContext(ctx, "INSERT INTO public.info_cardinal_number_witness VALUES (99,$1)", int64(7))
				if result != nil || !accepted(err) {
					t.Fatalf("precanceled INSERT result=%v error=%v", result, err)
				}
				return
			}
			want := infoCardinalNumberStored(backend.PG)
			rows, err := conn.QueryContext(ctx, want.Query)
			if rows == nil {
				if !accepted(err) {
					t.Fatalf("precanceled read=%v", err)
				}
				return
			}
			defer rows.Close()
			if !backend.PG || err != nil {
				t.Fatalf("precanceled read exposed cursor: %v", err)
			}
			// pq admission can race database/sql's context watcher; validate any
			// emitted prefix and require the complete seed only on clean EOF.
			columns, err := rows.ColumnTypes()
			if err != nil {
				if accepted(rows.Err()) {
					return
				}
				t.Fatal(err)
			}
			infoCardinalNumberColumnTypes(t, columns, true)
			n := 0
			for rows.Next() {
				var value any
				if err := rows.Scan(&value); err != nil {
					if accepted(err) {
						return
					}
					t.Fatal(err)
				}
				if n >= len(want.Rows) {
					t.Fatal("precanceled read returned extra row")
				}
				want.CheckRaw(t, 0, n, value)
				n++
			}
			if err := rows.Err(); err != nil {
				if !accepted(err) {
					t.Fatal(err)
				}
			} else if n != len(want.Rows) {
				t.Fatalf("completed read rows=%d want %d", n, len(want.Rows))
			}
		}()
	}
}
