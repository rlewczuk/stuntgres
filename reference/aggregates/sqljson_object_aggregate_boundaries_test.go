package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSQLJSONObjectAggregateBoundaries(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const q = "SELECT json_objectagg('a':1 RETURNING jsonb) OVER() AS v"
		if b.PG {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": 1}`))
		} else {
			d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
			d.Position = jsonArrayAggregatePosition(q, "json_objectagg")
			jsonArrayAggregateError(t, p, b, q, d, true)
		}
		// Child and option analysis still precedes the native window-provider fence.
		bad := "/*é😀*/ SELECT json_objectagg('a':1 FORMAT JSON) OVER()"
		d := fixture.ControlsReferenceDiagnostic("42804", "cannot use non-string types with explicit FORMAT JSON clause")
		d.Position = jsonArrayAggregatePosition(bad, "FORMAT")
		jsonArrayAggregateError(t, p, b, bad, d, true)
		missing := "SELECT jsonb_objectagg('a',1)"
		d = fixture.ControlsReferenceDiagnostic("42883", "function jsonb_objectagg(unknown, integer) does not exist")
		d.Position = "8"
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		jsonArrayAggregateError(t, p, b, missing, d, true)
		for _, q := range []string{`SELECT pg_catalog.json_objectagg('a':1)`, `SELECT "json_objectagg"('a':1)`} {
			d = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ":"`)
			d.Position = jsonArrayAggregatePosition(q, ":")
			jsonArrayAggregateError(t, p, b, q, d, true)
		}
		for _, tc := range []struct{ q, typ, want string }{
			{"SELECT json_arrayagg(NULL) AS v", "JSON", "[]"},
			{"SELECT json_agg(NULL::int) AS v", "JSON", "[null]"},
			{"SELECT json_object_agg('a'::text,NULL::int) AS v", "JSON", `{ "a" : null }`},
			{"SELECT jsonb_object_agg_strict('a'::text,'null'::json) AS v", "JSONB", `{}`},
			{"SELECT json_object_agg_strict('a'::text,'null'::json) AS v", "JSON", `{ "a" : null }`},
		} {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(tc.q, tc.typ, tc.want))
		}
		for _, tc := range []struct{ q, message string }{
			{"CREATE TABLE sjo_default(v json DEFAULT json_objectagg('a':1))", "aggregate functions are not allowed in DEFAULT expressions"},
			{"CREATE TABLE sjo_check(v int CHECK(json_objectagg('a':v) IS NOT NULL))", "aggregate functions are not allowed in check constraints"},
			{"CREATE TABLE sjo_generated(v int,j json GENERATED ALWAYS AS(json_objectagg('a':v)) STORED)", "aggregate functions are not allowed in column generation expressions"},
		} {
			d = fixture.ControlsReferenceDiagnostic("42803", tc.message)
			d.Position = jsonArrayAggregatePosition(tc.q, "json_objectagg")
			jsonArrayAggregateError(t, p, b, tc.q, d, true)
		}
	})
}

func TestReferenceSQLJSONObjectAggregateCancellation(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sjo_cancel_seed(x int)", 0),
			fixture.Exec("INSERT INTO sjo_cancel_seed SELECT generate_series(1,64)", 64),
			fixture.Exec("CREATE TABLE sjo_cancel_destination(v jsonb)", 0),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		const q = "INSERT INTO sjo_cancel_destination SELECT json_objectagg((a.x*4096+b.x*64+c.x):a.x+b.x+c.x WITH UNIQUE KEYS RETURNING jsonb) FROM(SELECT x FROM sjo_cancel_seed ORDER BY x LIMIT $1) a CROSS JOIN(SELECT x FROM sjo_cancel_seed ORDER BY x LIMIT $1) b CROSS JOIN(SELECT x FROM sjo_cancel_seed ORDER BY x LIMIT $1) c"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, stop := context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
			defer stop()
			for _, ctx := range []context.Context{canceled, expired} {
				want := context.Canceled
				if ctx == expired {
					want = context.DeadlineExceeded
				}
				if result, err := stmt.ExecContext(ctx, int64(1)); result != nil || !errors.Is(err, want) {
					t.Fatalf("canceled held insert=%v/%v", result, err)
				}
				if rows, err := conn.QueryContext(ctx, "SELECT json_objectagg('a':1 WITH UNIQUE RETURNING jsonb)"); rows != nil || !errors.Is(err, want) {
					if rows != nil {
						rows.Close()
					}
					t.Fatalf("canceled query=%v/%v", rows, err)
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, provider := range []string{"JSONObjectAggregatePlan.Advance", "sortJSONBFields"} {
				fixture.BinaryConversionActiveCancel(t, p, b, conn, q, provider, func(ctx context.Context) fixture.RelationOutcome {
					var result sql.Result
					var err error
					if prepared {
						result, err = stmt.ExecContext(ctx, int64(64))
					} else {
						result, err = conn.ExecContext(ctx, q, int64(64))
					}
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, jsonArrayAggregateSelect("SELECT count(*) AS v FROM sjo_cancel_destination", "INT8", int64(0)))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(1)))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, jsonArrayAggregateSelect("SELECT v FROM sjo_cancel_destination", "JSONB", `{"4161": 3}`))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DELETE FROM sjo_cancel_destination", 1))
			}
		}
	})
}
