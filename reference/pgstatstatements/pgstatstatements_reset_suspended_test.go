package reference_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetInFlight(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgss_reset_wire")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			observer, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, f.Ctx, observer, b.Recorder, step) }
			for _, q := range []string{"SET pg_stat_statements.track=none", "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE", "CREATE SCHEMA pgss_reset_wire", "CREATE EXTENSION pg_stat_statements SCHEMA pgss_reset_wire VERSION '1.12'", "CREATE TABLE public.pgss_reset_wire_data(k integer)", "CREATE SEQUENCE public.pgss_reset_wire_seq"} {
				run(fixture.Exec(q, 0))
			}
			defer observer.ExecContext(f.Ctx, "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE")
			run(fixture.Exec("INSERT INTO public.pgss_reset_wire_data VALUES(1),(2)", 2))
			wire, err := pgconn.Connect(f.Ctx, f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			defer wire.Close(context.Background())
			if deadline, ok := f.Ctx.Deadline(); ok {
				if err := wire.Conn().SetDeadline(deadline); err != nil {
					t.Fatal(err)
				}
			}
			simple := func(q string) {
				t.Helper()
				if _, err := wire.Exec(f.Ctx, q).ReadAll(); err != nil {
					t.Fatal(q, err)
				}
			}
			counts := func(pattern string, calls, rows int64) {
				t.Helper()
				run(fixture.Select("SELECT COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_reset_wire.pg_stat_statements WHERE query LIKE $1", []string{"calls", "rows"}, []string{"INT8", "INT8"}, [][]any{{calls, rows}}, pattern))
			}
			simple("SET pg_stat_statements.track_utility=off; SET pg_stat_statements.track_planning=off; SET pg_stat_statements.track=top; BEGIN")
			reset := "SELECT pgss_reset_wire.pg_stat_statements_reset()"
			pgssWireSend(t, wire, &pgproto3.Parse{Name: "reset", Query: reset}, &pgproto3.Bind{DestinationPortal: "reset_portal", PreparedStatement: "reset"}, &pgproto3.Execute{Portal: "reset_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
			message := pgssWireReceive(t, f.Ctx, wire)
			row, ok := message.(*pgproto3.DataRow)
			if !ok || len(row.Values) != 1 || len(row.Values[0]) == 0 {
				t.Fatalf("actual reset row: %#v", message)
			}
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			run(fixture.Select("SELECT count(*) AS n FROM pgss_reset_wire.pg_stat_statements", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			pgssWireSend(t, wire, &pgproto3.Execute{Portal: "reset_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CommandComplete{CommandTag: []byte("SELECT 0")})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(reset, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: "reset_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(reset, 1, 1)
			simple("COMMIT")
			counts(reset, 1, 1)
			const body = "SELECT nextval('public.pgss_reset_wire_seq') AS pgss_reset_suspended FROM public.pgss_reset_wire_data"
			const pattern = "SELECT nextval% AS pgss_reset_suspended FROM public.pgss_reset_wire_data"
			for i, minmax := range []bool{false, true} {
				// Independently reseed the public baseline for each reset mode.
				run(fixture.Select("SELECT pgss_reset_wire.pg_stat_statements_reset() IS NOT NULL AS reset", []string{"reset"}, []string{"BOOL"}, [][]any{{true}}))
				simple(body)
				counts(pattern, 1, 2)
				other := "SELECT nextval('public.pgss_reset_wire_seq') AS pgss_reset_other FROM public.pgss_reset_wire_data pgss_reset_other"
				otherPattern := "SELECT nextval% AS pgss_reset_other FROM public.pgss_reset_wire_data pgss_reset_other"
				simple(other)
				counts(otherPattern, 1, 2)
				var qid int64
				if err := observer.QueryRowContext(f.Ctx, "SELECT queryid FROM pgss_reset_wire.pg_stat_statements WHERE query LIKE $1", pattern).Scan(&qid); err != nil {
					t.Fatal(err)
				}
				simple("BEGIN")
				name := []string{"old_full", "old_minmax"}[i]
				pgssWireSend(t, wire, &pgproto3.Parse{Name: name, Query: body}, &pgproto3.Bind{DestinationPortal: name, PreparedStatement: name}, &pgproto3.Execute{Portal: name, MaxRows: 1}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
				if _, ok := pgssWireReceive(t, f.Ctx, wire).(*pgproto3.DataRow); !ok {
					t.Fatal("missing suspended row")
				}
				pgssWireWant(t, f.Ctx, wire, &pgproto3.PortalSuspended{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				peerName := name + "_peer"
				pgssWireSend(t, wire, &pgproto3.Parse{Name: peerName, Query: other}, &pgproto3.Bind{DestinationPortal: peerName, PreparedStatement: peerName}, &pgproto3.Execute{Portal: peerName, MaxRows: 1}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
				if _, ok := pgssWireReceive(t, f.Ctx, wire).(*pgproto3.DataRow); !ok {
					t.Fatal("missing nonmatching suspended row")
				}
				pgssWireWant(t, f.Ctx, wire, &pgproto3.PortalSuspended{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				run(fixture.Select("SELECT pgss_reset_wire.pg_stat_statements_reset(0,0,$1::bigint,$2::boolean) IS NOT NULL AS reset", []string{"reset"}, []string{"BOOL"}, [][]any{{true}}, qid, minmax))
				if !minmax {
					counts(pattern, 0, 0)
				} else {
					counts(pattern, 1, 2)
				}
				counts(otherPattern, 1, 2)
				pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: name}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				// REL_18_6 pgss_store on later successful ExecutorEnd recreates
				// removed history, unlike native replacement's private fences.
				calls, rows := int64(1), int64(1)
				if minmax {
					calls, rows = 2, 3
				}
				counts(pattern, calls, rows)
				run(fixture.Select("SELECT min_exec_time>0 AND max_exec_time>=min_exec_time AS completed FROM pgss_reset_wire.pg_stat_statements WHERE queryid=$1", []string{"completed"}, []string{"BOOL"}, [][]any{{true}}, qid))
				pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: peerName}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				counts(otherPattern, 2, 3)
				simple("COMMIT")
				counts(pattern, calls, rows)
				counts(otherPattern, 2, 3)
			}
		})
	}
}
