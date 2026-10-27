package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

type typeDescribeBadValuer struct{ err error }

func (v typeDescribeBadValuer) Value() (driver.Value, error) { return nil, v.err }

func TestReferenceTypeDescribeCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				q := "SELECT pg_typeof(pg_sleep(600)) AS identity"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				fixture.SleepActiveCancel(t, p, b, c, q, nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
			})
		})
	}
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		q := "SELECT pg_typeof($1::integer) AS identity"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		// pq's pre-canceled admission races its watcher. Isolate each probe,
		// inspect any returned output, then retire the interrupted borrower.
		for _, route := range []string{"query", "prepare", "held"} {
			probe, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer probe.Close()
				if b.PG {
					defer probe.Raw(func(any) error { return driver.ErrBadConn })
				}
				var held *sql.Stmt
				if route == "held" {
					held, err = probe.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				switch route {
				case "query":
					rows, err := probe.QueryContext(ctx, q, int64(7))
					typeDescribePreCanceledRows(t, b.PG, rows, err)
				case "held":
					rows, err := held.QueryContext(ctx, int64(7))
					typeDescribePreCanceledRows(t, b.PG, rows, err)
				case "prepare":
					s, err := probe.PrepareContext(ctx, q)
					if s != nil {
						defer s.Close()
					}
					if !b.PG {
						if s != nil || !errors.Is(err, context.Canceled) {
							t.Fatalf("native pre-canceled prepare handle=%v err=%v", s, err)
						}
					} else if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, driver.ErrBadConn) {
						t.Fatalf("pq pre-canceled prepare=%v", err)
					} else if err == nil && s == nil {
						t.Fatal("successful prepare returned no handle")
					}
				}
			}()
		}
		ordinary := errors.New("type describe argument")
		var result any
		err = stmt.QueryRowContext(p.Ctx, typeDescribeBadValuer{ordinary}).Scan(&result)
		fixture.DriverOrdinaryError(t, err, ordinary)
		err = stmt.QueryRowContext(p.Ctx, int64(1), int64(2)).Scan(&result)
		if err == nil {
			t.Fatal("extra driver argument accepted")
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatalf("ordinary argument error has SQLSTATE: %v", err)
		}
		run(fixture.Exec("CREATE TABLE public.td_iteration(v integer)", 0))
		run(fixture.Exec("INSERT INTO public.td_iteration VALUES(1),(2),(3)", 3))
		iterCtx, stop := context.WithCancel(p.Ctx)
		defer stop()
		rows, err := c.QueryContext(iterCtx, "SELECT pg_typeof(v) AS identity FROM public.td_iteration ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("first row", rows.Err())
		}
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if fixture.Normalize(v) != "integer" {
			t.Fatal("first identity", v)
		}
		stop()
		for rows.Next() {
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			if fixture.Normalize(v) != "integer" {
				t.Fatal("prefix identity", v)
			}
		}
		if err := rows.Err(); err != nil && !errors.Is(err, context.Canceled) && !(b.PG && errors.Is(err, driver.ErrBadConn)) {
			t.Fatalf("iteration interruption=%v", err)
		}
	})
}

func typeDescribePreCanceledRows(t *testing.T, pg bool, rows *sql.Rows, err error) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if !pg {
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("native pre-canceled query rows=%v err=%v", rows, err)
		}
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("pq pre-canceled query=%v", err)
	}
	if rows == nil {
		if err == nil {
			t.Fatal("successful query returned no cursor")
		}
		return
	}
	// Snapshot once: the watcher may close the cursor between API calls.
	if columns, metadataErr := rows.ColumnTypes(); metadataErr == nil {
		if len(columns) != 1 {
			t.Fatalf("pre-canceled columns=%d", len(columns))
		}
		col := columns[0]
		length, lengthOK := col.Length()
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != "identity" || col.DatabaseTypeName() != "REGTYPE" || col.ScanType() != reflect.TypeFor[any]() || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable || nullableOK {
			t.Fatalf("pre-canceled metadata=%v", col)
		}
	} else if terminal := rows.Err(); !errors.Is(terminal, context.Canceled) && !errors.Is(terminal, driver.ErrBadConn) {
		t.Fatalf("pre-canceled metadata error=%v terminal=%v", metadataErr, terminal)
	}
	count := 0
	for rows.Next() {
		var v any
		if scanErr := rows.Scan(&v); scanErr != nil {
			if terminal := rows.Err(); errors.Is(terminal, context.Canceled) || errors.Is(terminal, driver.ErrBadConn) {
				break
			}
			t.Fatal(scanErr)
		}
		fixture.RegtypeRaw(t, true, v)
		if fixture.Normalize(v) != "integer" {
			t.Fatalf("pre-canceled prefix identity=%v", v)
		}
		count++
		if count > 1 {
			t.Fatal("pre-canceled query returned extra rows")
		}
	}
	terminal := rows.Err()
	if terminal != nil && !errors.Is(terminal, context.Canceled) && !errors.Is(terminal, driver.ErrBadConn) {
		t.Fatalf("pq pre-canceled iteration=%v", terminal)
	}
	if terminal == nil && count != 1 {
		t.Fatalf("clean EOF count=%d want1", count)
	}
}
