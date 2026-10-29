package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// This is an independently specified SQL contract, not a topology comparison.
// The oracle must admit Partial/Finalize for each real SQL-support aggregate;
// native worker admission and simultaneous lexical ownership are proved by
// TestAggregateParallelInvocationOwnership through the production coordinator.
func TestReferenceAggregateParallelOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ac_ownership_owner", "ac_ownership_reader"})
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE ac_ownership_input(id integer,x integer,padding text)", 0),
			fixture.Exec("INSERT INTO ac_ownership_input SELECT n,CASE WHEN n>=2 AND n<=5 THEN n-1 ELSE NULL END,repeat('x',1024) FROM generate_series(1,16384) s(n)", 16384),
			fixture.Exec("CREATE ROLE ac_ownership_owner", 0),
			fixture.Exec("CREATE ROLE ac_ownership_reader", 0),
			fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ac_ownership_owner", 0),
			fixture.Exec("GRANT USAGE ON SCHEMA public TO ac_ownership_reader", 0),
			fixture.Exec("GRANT SELECT ON ac_ownership_input TO ac_ownership_reader", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_spelling() RETURNS text LANGUAGE SQL STABLE PARALLEL SAFE AS $$SELECT 'ac_ownership_input'::regclass::text||':'||'bigint'::regtype::text||':'||(current_user::regrole::text=current_user::text)::text$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_zero() RETURNS bigint LANGUAGE SQL STABLE PARALLEL SAFE AS $$SELECT CASE WHEN ac_ownership_spelling()='ac_ownership_input:bigint:true' THEN 0::bigint ELSE 100::bigint END$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT CASE WHEN $2 IS NULL THEN $1 ELSE coalesce($1,0)+$2::bigint*$2::bigint+ac_ownership_zero() END$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_merge(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT CASE WHEN $1 IS NULL THEN $2 WHEN $2 IS NULL THEN $1 ELSE $1+$2+ac_ownership_zero() END$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_int(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+ac_ownership_zero()$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_bool(bigint) RETURNS boolean LANGUAGE SQL AS $$SELECT $1+ac_ownership_zero()=30$$", 0),
			fixture.Exec("CREATE FUNCTION ac_ownership_numeric(bigint) RETURNS numeric LANGUAGE SQL AS $$SELECT ($1+ac_ownership_zero())::numeric$$", 0))
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		exec("SET SESSION AUTHORIZATION ac_ownership_owner")
		for _, final := range []string{"int", "bool", "numeric"} {
			exec("CREATE AGGREGATE ac_ownership_" + final + "_agg(integer) (SFUNC=ac_ownership_step,STYPE=bigint,COMBINEFUNC=ac_ownership_merge,FINALFUNC=ac_ownership_" + final + ",PARALLEL=SAFE)")
			exec("REVOKE EXECUTE ON FUNCTION ac_ownership_" + final + "_agg(integer) FROM PUBLIC")
			exec("GRANT EXECUTE ON FUNCTION ac_ownership_" + final + "_agg(integer) TO ac_ownership_reader")
		}
		exec("RESET SESSION AUTHORIZATION")
		if b.PG {
			for _, setting := range []string{"min_parallel_table_scan_size=0", "parallel_setup_cost=0", "parallel_tuple_cost=0"} {
				exec("SET " + setting)
			}
		}
		exec("SET SESSION AUTHORIZATION ac_ownership_reader")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ac_ownership_spelling() AS spelling,ac_ownership_zero() AS contribution", []string{"spelling", "contribution"}, []string{"TEXT", "INT8"}, [][]any{{"ac_ownership_input:bigint:true", int64(0)}}))
		for _, variant := range []struct {
			final, typ   string
			five, thirty any
		}{
			{"int", "INT8", int64(5), int64(30)},
			{"bool", "BOOL", false, true},
			{"numeric", "NUMERIC", "5", "30"},
		} {
			t.Run(variant.final, func(t *testing.T) {
				name := "ac_ownership_" + variant.final + "_agg"
				// Each variant changes shared support ACLs. Restore them even if
				// an assertion fails, so later variants keep their own contract.
				t.Cleanup(func() {
					for _, command := range []string{
						"RESET SESSION AUTHORIZATION",
						"GRANT EXECUTE ON FUNCTION ac_ownership_merge(bigint,bigint) TO PUBLIC",
						"GRANT EXECUTE ON FUNCTION ac_ownership_step(bigint,integer) TO PUBLIC",
						"GRANT EXECUTE ON FUNCTION " + name + "(integer) TO ac_ownership_reader",
						"SET SESSION AUTHORIZATION ac_ownership_reader",
					} {
						if _, err := c.ExecContext(context.Background(), command); err != nil {
							t.Error("restore ownership scenario authority:", command, err)
						}
					}
				})
				projection := name + "(x) FILTER(WHERE x IS NOT NULL)"
				q := "SELECT " + projection + " AS v FROM ac_ownership_input WHERE id<=$1"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				scalar := func(query string, want any, args ...any) fixture.Step {
					step := fixture.Select(query, []string{"v"}, []string{variant.typ}, [][]any{{want}}, args...)
					step.CheckMetadata = aggregateOwnershipMetadata(b.PG, variant.typ)
					return step
				}
				value := func(limit int, want any) fixture.Step { return scalar(q, want, limit) }
				for _, ceiling := range []int{0, 2, 7} {
					exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling))
					if ceiling == 2 && b.PG {
						aggregateOwnershipPartialPlan(t, p, c, "SELECT "+projection+" AS v FROM ac_ownership_input WHERE id<=16384")
					}
					for _, row := range []struct {
						limit int
						want  any
					}{{0, nil}, {3, variant.five}, {5, variant.thirty}, {16384, variant.thirty}, {0, nil}} {
						step := value(row.limit, row.want)
						createAggregateBoth(t, p, b, c, step)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					}
					createAggregateBoth(t, p, b, c, scalar("SELECT "+projection+" AS v FROM ac_ownership_input WHERE x IS NULL", nil))
					// Unlike FILTER's empty states, this reaches SFUNC with one
					// actual NULL input; its independently specified state is NULL.
					createAggregateBoth(t, p, b, c, scalar("SELECT "+name+"(x) AS v FROM ac_ownership_input WHERE id=1", nil))
					// The same handle also returns one SQL NULL for all-NULL input.
					fixture.RunStep(t, p.Ctx, held, b.Recorder, value(1, nil))
				}
				// Current support EXECUTE belongs to the aggregate owner. Revoking
				// unused combine cannot break serial execution on this held handle.
				for _, command := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ac_ownership_merge(bigint,bigint) FROM PUBLIC", "SET SESSION AUTHORIZATION ac_ownership_reader", "SET max_parallel_workers_per_gather=0"} {
					exec(command)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, value(16384, variant.thirty))
				createAggregateBoth(t, p, b, c, value(16384, variant.thirty))
				// A held statement can retain a serial generic plan after its
				// earlier ceiling-zero executions. Its separate EXPLAIN witness
				// does not establish reached COMBINEFUNC on this execution.
				// SFUNC is active for both serial and partial admission, including
				// empty input, so use the incumbent Authority contract here.
				for _, command := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ac_ownership_merge(bigint,bigint) TO PUBLIC", "REVOKE EXECUTE ON FUNCTION ac_ownership_step(bigint,integer) FROM PUBLIC", "SET SESSION AUTHORIZATION ac_ownership_reader", "SET max_parallel_workers_per_gather=2"} {
					exec(command)
				}
				for _, limit := range []int{0, 16384} {
					denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ac_ownership_step"}, limit)
					denied.SelectRows, denied.ErrorStage = true, "execute"
					createAggregateBoth(t, p, b, c, denied)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				}
				for _, command := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ac_ownership_step(bigint,integer) TO PUBLIC", "SET SESSION AUTHORIZATION ac_ownership_reader"} {
					exec(command)
				}
				for _, ceiling := range []int{0, 2} {
					exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, value(16384, variant.thirty))
					createAggregateBoth(t, p, b, c, value(16384, variant.thirty))
				}
				for _, command := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION " + name + "(integer) FROM ac_ownership_reader", "SET SESSION AUTHORIZATION ac_ownership_reader"} {
					exec(command)
				}
				denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate " + name}, 16384)
				denied.SelectRows, denied.ErrorStage = true, "execute"
				createAggregateBoth(t, p, b, c, denied)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				for _, command := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION " + name + "(integer) TO ac_ownership_reader", "SET SESSION AUTHORIZATION ac_ownership_reader"} {
					exec(command)
				}
				for _, ceiling := range []int{0, 2} {
					exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, value(16384, variant.thirty))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, value(0, nil))
					createAggregateBoth(t, p, b, c, value(16384, variant.thirty))
					createAggregateBoth(t, p, b, c, value(0, nil))
				}
			})
		}
		// Exact grouped all-NULL and empty-group witnesses are independent of
		// unordered partition boundaries and reuse the bigint variant.
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,ac_ownership_int_agg(x) AS v FROM (VALUES(1,1),(1,2),(2,3),(2,4),(NULL,NULL)) s(g,x) GROUP BY g ORDER BY g NULLS LAST", []string{"g", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(5)}, {int64(2), int64(25)}, {nil, nil}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT x,ac_ownership_int_agg(x) AS v FROM ac_ownership_input WHERE FALSE GROUP BY x", []string{"x", "v"}, []string{"INT4", "INT8"}, nil))
		exec("RESET SESSION AUTHORIZATION")
	})
}

func aggregateOwnershipPartialPlan(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "EXPLAIN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "Partial Aggregate") || !strings.Contains(plan.String(), "Finalize Aggregate") {
		t.Fatal("oracle did not admit actual SQL partial/final aggregation: ", plan.String())
	}
}

func aggregateOwnershipMetadata(pg bool, kind string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 {
			t.Fatal(meta, err)
		}
		column := meta[0]
		if column.Name() != "v" || column.DatabaseTypeName() != kind {
			t.Fatal(column.Name(), column.DatabaseTypeName())
		}
		wantScan := reflect.TypeFor[int64]()
		if kind == "BOOL" {
			wantScan = reflect.TypeFor[bool]()
		}
		if kind == "NUMERIC" {
			wantScan = reflect.TypeFor[string]()
			if pg {
				wantScan = reflect.TypeFor[any]()
			}
		}
		if column.ScanType() != wantScan {
			t.Fatal("scan type", column.ScanType(), wantScan)
		}
		if nullable, known := column.Nullable(); nullable || known {
			t.Fatal("computed nullability", nullable, known)
		}
		if length, known := column.Length(); length != 0 || known {
			t.Fatal("scalar length", length, known)
		}
		precision, scale, known := column.DecimalSize()
		if pg && kind == "NUMERIC" {
			if !known || precision != 65535 || scale != 65531 {
				t.Fatal("oracle unconstrained numeric metadata", precision, scale, known)
			}
		} else if known {
			t.Fatal("unexpected scalar decimal metadata", precision, scale, known)
		}
	}
}
