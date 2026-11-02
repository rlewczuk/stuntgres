package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsInFlightEviction(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgss_inflight")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			observer, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			run := func(step fixture.Step) {
				t.Helper()
				fixture.RunStep(t, f.Ctx, observer, b.Recorder, step)
			}
			both := func(step fixture.Step) {
				t.Helper()
				run(step)
				run(fixture.Prepared(step))
			}
			run(fixture.Exec("SET pg_stat_statements.track = none", 0))
			run(fixture.Exec("DROP EXTENSION IF EXISTS pg_stat_statements CASCADE", 0))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := observer.ExecContext(ctx, "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE"); err != nil {
					t.Error("extension cleanup:", err)
				}
			}()
			for _, q := range []string{
				"CREATE SCHEMA pgss_inflight",
				"CREATE EXTENSION pg_stat_statements SCHEMA pgss_inflight VERSION '1.12'",
				"CREATE TABLE public.pgss_inflight_data(k integer)",
				"CREATE TABLE public.pgss_inflight_pressure(k integer)",
				"CREATE SEQUENCE public.pgss_inflight_seq",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO public.pgss_inflight_data VALUES(1),(2)", 2))
			run(fixture.Exec("INSERT INTO public.pgss_inflight_pressure VALUES(7)", 1))
			both(fixture.Select("SHOW pg_stat_statements.max", []string{"pg_stat_statements.max"}, []string{"TEXT"}, [][]any{{"5000"}}))

			worker, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()
			for _, q := range []string{"SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track=top"} {
				fixture.RunStep(t, f.Ctx, worker, b.Recorder, fixture.Exec(q, 0))
			}
			wire, err := pgconn.Connect(f.Ctx, f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := wire.Close(ctx); err != nil {
					t.Error("portal connection cleanup:", err)
				}
			}()
			if deadline, ok := f.Ctx.Deadline(); ok {
				if err := wire.Conn().SetDeadline(deadline); err != nil {
					t.Fatal(err)
				}
			}
			simple := func(q, tag string) {
				t.Helper()
				results, err := wire.Exec(f.Ctx, q).ReadAll()
				if err != nil || len(results) != 1 || results[0].CommandTag.String() != tag || len(results[0].Rows) != 0 {
					t.Fatalf("wire %q: results=%+v error=%v, want %s without rows", q, results, err, tag)
				}
			}
			for _, q := range []string{"SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track=top"} {
				simple(q, "SET")
			}

			const query = "SELECT nextval('public.pgss_inflight_seq') AS pgss_inflight_value FROM public.pgss_inflight_data pgss_inflight_target"
			const normalized = "SELECT nextval($1) AS pgss_inflight_value FROM public.pgss_inflight_data pgss_inflight_target"
			const pattern = "SELECT nextval% AS pgss_inflight_value FROM public.pgss_inflight_data pgss_inflight_target"
			const scope = " AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
			counts := func(groups, calls, rows int64) {
				t.Helper()
				both(fixture.Select("SELECT count(*) AS groups,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_inflight.pg_stat_statements WHERE query LIKE $1"+scope,
					[]string{"groups", "calls", "rows"}, []string{"INT8", "INT8", "INT8"}, [][]any{{groups, calls, rows}}, pattern))
			}
			sequence := func() {
				t.Helper()
				both(fixture.Select("SELECT last_value,is_called FROM public.pgss_inflight_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
			}
			counts(0, 0, 0)
			// First successful execution is nonsticky usage 2. Its literal is
			// normalized at admission; the later absent-entry terminal has no
			// jumble state and must instead retain the cleaned original source.
			results, err := wire.Exec(f.Ctx, query).ReadAll()
			if err != nil || len(results) != 1 {
				t.Fatalf("initial full execution: results=%+v error=%v", results, err)
			}
			full := results[0]
			if full.CommandTag.String() != "SELECT 2" || len(full.FieldDescriptions) != 1 || string(full.FieldDescriptions[0].Name) != "pgss_inflight_value" || full.FieldDescriptions[0].DataTypeOID != 20 || full.FieldDescriptions[0].Format != 0 || len(full.Rows) != 2 || len(full.Rows[0]) != 1 || len(full.Rows[1]) != 1 {
				t.Fatalf("initial full execution metadata/rows/tag: %+v", full)
			}
			first, second := string(full.Rows[0][0]), string(full.Rows[1][0])
			if !(first == "1" && second == "2" || first == "2" && second == "1") {
				t.Fatalf("initial full execution bag=(%q,%q), want {1,2}", first, second)
			}
			counts(1, 1, 2)
			var queryID int64
			if err := observer.QueryRowContext(f.Ctx, "SELECT queryid FROM pgss_inflight.pg_stat_statements WHERE query=$1"+scope, normalized).Scan(&queryID); err != nil {
				t.Fatal("initial normalized identity:", err)
			}
			if queryID == 0 {
				t.Fatal("initial target queryid is zero")
			}
			entry := func(source string, calls, rows int64) {
				t.Helper()
				both(fixture.Select("SELECT query,queryid=$2 AS same_identity,toplevel,plans,calls,rows,total_plan_time=0 AND min_plan_time=0 AND max_plan_time=0 AND mean_plan_time=0 AND stddev_plan_time=0 AS no_planning FROM pgss_inflight.pg_stat_statements WHERE query LIKE $1"+scope,
					[]string{"query", "same_identity", "toplevel", "plans", "calls", "rows", "no_planning"}, []string{"TEXT", "BOOL", "BOOL", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{source, true, true, int64(0), calls, rows, true}}, pattern, queryID))
				before := pgssInFlightTiming(t, f.Ctx, observer, queryID, calls, rows, false)
				if after := pgssInFlightTiming(t, f.Ctx, observer, queryID, calls, rows, true); after != before {
					t.Fatalf("disabled direct/prepared observers changed execution timing: %v/%v", before, after)
				}
			}
			entry(normalized, 1, 2)

			simple("BEGIN", "BEGIN")
			pgssWireSend(t, wire, &pgproto3.Parse{Name: "pgss_inflight_body", Query: query}, &pgproto3.Bind{DestinationPortal: "pgss_inflight_portal", PreparedStatement: "pgss_inflight_body"}, &pgproto3.Execute{Portal: "pgss_inflight_portal", MaxRows: 1}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.DataRow{Values: [][]byte{[]byte("3")}})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.PortalSuspended{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(1, 1, 2)
			sequence()
			var dealloc int64
			var reset time.Time
			if err := observer.QueryRowContext(f.Ctx, "SELECT dealloc,stats_reset FROM pgss_inflight.pg_stat_statements_info").Scan(&dealloc, &reset); err != nil {
				t.Fatal("before pressure:", err)
			}
			// Every effective FROM alias is a released PG18 jumble field.
			// Twice-completed controls have usage 3, strictly above target 2
			// under every shared nonsticky decay. No tied victim is prescribed.
			for i := range 5100 {
				alias := fmt.Sprintf("pgss_inflight_hot_%04d", i)
				q := "SELECT " + alias + ".k AS pgss_inflight_control FROM public.pgss_inflight_pressure " + alias
				step := fixture.Select(q, []string{"pgss_inflight_control"}, []string{"INT4"}, [][]any{{int64(7)}})
				fixture.RunStep(t, f.Ctx, worker, b.Recorder, step)
				fixture.RunStep(t, f.Ctx, worker, b.Recorder, step)
			}
			both(fixture.Select("SELECT dealloc>$1 AS deallocated,stats_reset=$2 AS not_reset FROM pgss_inflight.pg_stat_statements_info", []string{"deallocated", "not_reset"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, dealloc, reset))
			both(fixture.Select("SELECT count(*)>0 AND count(*)<=5000 AS bounded FROM pgss_inflight.pg_stat_statements", []string{"bounded"}, []string{"BOOL"}, [][]any{{true}}))
			both(fixture.Select("SELECT count(*)>0 AND count(DISTINCT queryid)=count(*) AND bool_and(calls=2 AND rows=2) AS distinct_hot_controls FROM pgss_inflight.pg_stat_statements WHERE query LIKE 'SELECT pgss_inflight_hot_% AS pgss_inflight_control FROM public.pgss_inflight_pressure %'"+scope, []string{"distinct_hot_controls"}, []string{"BOOL"}, [][]any{{true}}))
			// This absence is established before normal disposal: the old 1/2
			// history must be gone, while the real executor is still suspended.
			counts(0, 0, 0)
			sequence()
			pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: "pgss_inflight_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(1, 1, 1)
			entry(query, 1, 1)
			sequence()
			simple("COMMIT", "COMMIT")
			counts(1, 1, 1)
			entry(query, 1, 1)
			sequence()
			both(fixture.Select("SELECT count(*)<=5000 AS bounded FROM pgss_inflight.pg_stat_statements", []string{"bounded"}, []string{"BOOL"}, [][]any{{true}}))
		})
	}
}

func pgssInFlightTiming(t *testing.T, ctx context.Context, observer *sql.Conn, queryID, calls, rows int64, prepared bool) [5]float64 {
	t.Helper()
	q := "SELECT calls,rows,total_exec_time,min_exec_time,max_exec_time,mean_exec_time,stddev_exec_time FROM pgss_inflight.pg_stat_statements WHERE queryid=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND toplevel"
	var result *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := observer.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.QueryContext(ctx, queryID)
	} else {
		result, err = observer.QueryContext(ctx, q, queryID)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	columns, err := result.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"calls", "rows", "total_exec_time", "min_exec_time", "max_exec_time", "mean_exec_time", "stddev_exec_time"}
	types := []string{"INT8", "INT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}
	if len(columns) != len(names) {
		t.Fatalf("timing metadata columns=%d want=%d", len(columns), len(names))
	}
	for i, column := range columns {
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] {
			t.Fatalf("timing column %d=(%s,%s) want=(%s,%s)", i, column.Name(), column.DatabaseTypeName(), names[i], types[i])
		}
	}
	if !result.Next() {
		t.Fatalf("missing target timing queryid=%d: %v", queryID, result.Err())
	}
	var gotCalls, gotRows int64
	var timing [5]float64
	if err := result.Scan(&gotCalls, &gotRows, &timing[0], &timing[1], &timing[2], &timing[3], &timing[4]); err != nil {
		t.Fatal(err)
	}
	if gotCalls != calls || gotRows != rows {
		t.Fatalf("timing calls/rows=%d/%d want=%d/%d", gotCalls, gotRows, calls, rows)
	}
	for i, value := range timing {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			t.Fatalf("timing[%d]=%g, want finite nonnegative milliseconds", i, value)
		}
	}
	if !pgssTimeClose(timing[3], timing[0]/float64(calls)) || timing[1] > timing[3] || timing[3] > timing[2] {
		t.Fatalf("incoherent timing total/min/max/mean/stddev=%v calls=%d", timing, calls)
	}
	if calls == 1 && (timing[0] != timing[1] || timing[0] != timing[2] || timing[0] != timing[3] || timing[4] != 0) {
		t.Fatalf("single-sample timing=%v, want total=min=max=mean and stddev=0", timing)
	}
	if result.Next() {
		t.Fatal("multiple target timing entries")
	}
	if err := result.Err(); err != nil {
		t.Fatal(err)
	}
	return timing
}
