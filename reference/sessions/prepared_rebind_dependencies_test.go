package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePreparedRebindDependencies(t *testing.T) {
	for _, source := range []string{"view", "nested_view", "unused_cte"} {
		t.Run(source, func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				preparedRebindSetup(t, p, b, c)
				run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
				run(fixture.Exec("CREATE TABLE rb_late.dep(v INT)", 0))
				run(fixture.Exec("INSERT INTO rb_late.dep VALUES(70)", 1))
				run(fixture.Exec("CREATE VIEW rb_late.inner_view AS SELECT v FROM rb_late.dep", 0))
				run(fixture.Exec("CREATE VIEW rb_late.outer_view AS SELECT v FROM rb_late.inner_view", 0))
				query := "SELECT t.v AS a,d.v AS b FROM t CROSS JOIN rb_late.inner_view AS d"
				if source == "nested_view" {
					query = "SELECT t.v AS a,d.v AS b FROM t CROSS JOIN rb_late.outer_view AS d"
				} else if source == "unused_cte" {
					query = "WITH unused AS (SELECT v FROM rb_late.dep) SELECT v FROM t"
				}
				routes := preparedRebindRoutes(t, p, b, c, query, "PREPARE rb_dep AS "+query, "EXECUTE rb_dep")
				read := func(outer, inner int64) {
					names, types, want := []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{outer, inner}}
					if source == "unused_cte" {
						names, types, want = []string{"v"}, []string{"INT4"}, [][]any{{outer}}
					}
					for _, r := range routes {
						preparedRebindRead(t, p, b, r, names, types, want)
					}
				}
				read(7, 70)
				preparedRebindShadow(t, p, b, c)
				read(7, 70)
				// Changing an underlying source definition invalidates the body
				// even when the recursively rewritten view's own shape is unchanged.
				run(fixture.Exec("ALTER TABLE rb_late.dep ADD unused INT", 0))
				read(9, 70)
				if source != "unused_cte" {
					run(fixture.Exec("CREATE OR REPLACE VIEW rb_late.inner_view AS SELECT v+20 AS v FROM rb_late.dep", 0))
					read(9, 90)
					// A stored view retains its catalog-owned source binding.
					run(fixture.Exec("CREATE TABLE rb_early.dep(v INT)", 0))
					run(fixture.Exec("INSERT INTO rb_early.dep VALUES(999)", 1))
					read(9, 90)
				}
			})
		})
	}
}

type preparedRebindCapture struct {
	scalar time.Time
	array  time.Time
}

func preparedRebindCaptureRead(t *testing.T, p *fixture.Pair, b fixture.Backend, r preparedRebindRoute, sequence, value int64) preparedRebindCapture {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	rows, err := r.QueryContext(ctx, r.SQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"captured", "array_capture", "n", "typ", "v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 5 {
		t.Fatal(types, err)
	}
	for i, typ := range []string{"TIMESTAMP", "TIMESTAMP", "INT8", "REGTYPE", "INT4"} {
		if types[i].DatabaseTypeName() != typ {
			t.Fatal(i, types[i], typ)
		}
	}
	if !rows.Next() {
		t.Fatal("missing capture row", rows.Err())
	}
	var capture preparedRebindCapture
	var n, v int64
	var typ string
	if err := rows.Scan(&capture.scalar, &capture.array, &n, &typ, &v); err != nil || n != sequence || v != value || typ != "integer" {
		t.Fatal(capture, n, v, typ, err)
	}
	if !capture.scalar.Equal(capture.array) {
		t.Fatal("scalar and typed-array literal did not share analysis time", capture)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra capture row", rows.Err())
	}
	return capture
}

// Observe actual public statement time advancing; no fixed sleep or inferred
// wall-clock scheduling is used to establish a new capture opportunity.
func preparedRebindAdvanceClock(t *testing.T, p *fixture.Pair, c *sql.Conn, after time.Time) time.Time {
	t.Helper()
	for {
		var now time.Time
		if err := c.QueryRowContext(p.Ctx, "SELECT LOCALTIMESTAMP").Scan(&now); err != nil {
			t.Fatal(err)
		}
		if now.After(after) {
			return now
		}
		if err := p.Ctx.Err(); err != nil {
			t.Fatal("clock did not advance", err)
		}
	}
}

func TestReferencePreparedRebindCaptures(t *testing.T) {
	for _, routeName := range []string{"go", "sql"} {
		t.Run(routeName, func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				preparedRebindSetup(t, p, b, c)
				run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
				run(fixture.Exec("CREATE SEQUENCE rb_late.q START 10", 0))
				query := "SELECT TIMESTAMP 'now' AS captured,('{now}'::timestamp[])[1] AS array_capture,nextval('rb_late.q') AS n,'int4'::regtype AS typ,t.* FROM t"
				var r preparedRebindRoute
				if routeName == "go" {
					r = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
				} else {
					run(fixture.Exec("PREPARE rb_capture AS "+query, 0))
					r = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_capture"}
				}
				first := preparedRebindCaptureRead(t, p, b, r, 10, 7)
				preparedRebindAdvanceClock(t, p, c, first.scalar)
				run(fixture.Exec("CREATE TABLE rb_late.unrelated(v INT)", 0))
				run(fixture.Exec("UPDATE rb_late.t SET v=8", 1))
				stable := preparedRebindCaptureRead(t, p, b, r, 11, 8)
				if !stable.scalar.Equal(first.scalar) || !stable.array.Equal(first.array) {
					t.Fatal("DML or unrelated existing-namespace table refreshed captures", first, stable)
				}
				// Shape rejection must precede nextval and publish no new group.
				run(fixture.Exec("ALTER TABLE rb_late.t ADD unused INT", 0))
				preparedRebindFailure(t, p, b, r, sqlPreparedDiagnostic("0A000", "cached plan must not change result type"))
				run(sqlPreparedScalar("SELECT last_value FROM rb_late.q", "last_value", "INT8", int64(11)))
				run(fixture.Exec("ALTER TABLE rb_late.t DROP unused", 0))
				lower := preparedRebindAdvanceClock(t, p, c, first.scalar)
				second := preparedRebindCaptureRead(t, p, b, r, 12, 8)
				if second.scalar.Before(lower) || !second.scalar.After(first.scalar) || !second.array.After(first.array) {
					t.Fatal("successful dependency analysis did not refresh complete temporal group", first, second, lower)
				}
				preparedRebindShadow(t, p, b, c)
				stable = preparedRebindCaptureRead(t, p, b, r, 13, 8)
				if !stable.scalar.Equal(second.scalar) || !stable.array.Equal(second.array) {
					t.Fatal("shadow creation refreshed captures", second, stable)
				}
				run(fixture.Exec("SET search_path TO rb_late,rb_early", 0))
				lower = preparedRebindAdvanceClock(t, p, c, second.scalar)
				third := preparedRebindCaptureRead(t, p, b, r, 14, 8)
				if third.scalar.Before(lower) || !third.scalar.After(second.scalar) || !third.array.After(second.array) {
					t.Fatal("effective path change did not refresh captures", second, third, lower)
				}
			})
		})
		t.Run(routeName+"/values", func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				preparedRebindSetup(t, p, b, c)
				query := "VALUES(TIMESTAMP 'now',('{now}'::timestamp[])[1])"
				var r preparedRebindRoute
				if routeName == "go" {
					r = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
				} else {
					sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE rb_values AS "+query, 0))
					r = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_values"}
				}
				read := func() time.Time {
					rows, err := r.QueryContext(p.Ctx, r.SQL)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					var scalar, array time.Time
					if !rows.Next() {
						t.Fatal("missing VALUES capture", rows.Err())
					}
					if err := rows.Scan(&scalar, &array); err != nil || !scalar.Equal(array) {
						t.Fatal(scalar, array, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra VALUES capture", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					preparedRebindRead(t, p, b, r, []string{"column1", "column2"}, []string{"TIMESTAMP", "TIMESTAMP"}, [][]any{{fixture.Normalize(scalar), fixture.Normalize(array)}})
					return scalar
				}
				first := read()
				preparedRebindAdvanceClock(t, p, c, first)
				if stable := read(); !stable.Equal(first) {
					t.Fatal("unchanged VALUES analysis refreshed literals", first, stable)
				}
				sqlPreparedStep(t, p, b, c, fixture.Exec("SET search_path TO rb_late,rb_early", 0))
				lower := preparedRebindAdvanceClock(t, p, c, first)
				if fresh := read(); fresh.Before(lower) || !fresh.After(first) {
					t.Fatal("VALUES effective-path invalidation did not refresh both literal captures", first, fresh, lower)
				}
			})
		})
	}
}
