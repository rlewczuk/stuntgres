package reference_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsProtocol(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgss_wire")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			observer, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			run := func(t *testing.T, step fixture.Step) {
				t.Helper()
				fixture.RunStep(t, f.Ctx, observer, b.Recorder, step)
			}
			for _, q := range []string{"SET pg_stat_statements.track = none", "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE", "CREATE SCHEMA pgss_wire", "CREATE EXTENSION pg_stat_statements SCHEMA pgss_wire VERSION '1.12'", "CREATE TABLE public.pgss_protocol_data(k integer)", "INSERT INTO public.pgss_protocol_data VALUES(1),(2)", "CREATE SEQUENCE public.pgss_protocol_seq", "CREATE TABLE public.pgss_protocol_copy(k integer)"} {
				if _, err := observer.ExecContext(f.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			defer observer.ExecContext(f.Ctx, "DROP EXTENSION IF EXISTS pg_stat_statements CASCADE")
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
			simple := func(t *testing.T, q string) []*pgconn.Result {
				t.Helper()
				results, err := wire.Exec(f.Ctx, q).ReadAll()
				if err != nil {
					t.Fatal(q, err)
				}
				return results
			}
			simple(t, "SET pg_stat_statements.track_utility=off; SET pg_stat_statements.track_planning=off; SET pg_stat_statements.track=top")
			counts := func(t *testing.T, pattern string, groups, calls, rows int64) {
				t.Helper()
				q := "SELECT count(*) AS groups,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_wire.pg_stat_statements WHERE query LIKE $1 AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
				step := fixture.Select(q, []string{"groups", "calls", "rows"}, []string{"INT8", "INT8", "INT8"}, [][]any{{groups, calls, rows}}, pattern)
				run(t, step)
				run(t, fixture.Prepared(step))
			}
			batchPattern := "SELECT k AS pgss_wire_batch FROM public.pgss_protocol_data WHERE k=%"
			counts(t, batchPattern, 0, 0, 0)
			results := simple(t, "SELECT k AS pgss_wire_batch FROM public.pgss_protocol_data WHERE k=1; SELECT k AS pgss_wire_batch FROM public.pgss_protocol_data WHERE k=2")
			if len(results) != 2 {
				t.Fatalf("simple batch completions=%d want2", len(results))
			}
			for i, r := range results {
				if r.CommandTag.String() != "SELECT 1" || len(r.Rows) != 1 || len(r.Rows[0]) != 1 || string(r.Rows[0][0]) != []string{"1", "2"}[i] {
					t.Fatalf("simple batch result %d: %+v", i, r)
				}
			}
			counts(t, batchPattern, 1, 2, 2)
			counts(t, "SELECT k AS pgss_wire_batch FROM public.pgss_protocol_data WHERE k=$1", 1, 2, 2)

			q := "SELECT nextval('public.pgss_protocol_seq') AS pgss_wire_volatile FROM public.pgss_protocol_data ORDER BY k"
			pattern := "SELECT nextval% AS pgss_wire_volatile FROM public.pgss_protocol_data ORDER BY k"
			counts(t, pattern, 0, 0, 0)
			simple(t, "BEGIN")
			pgssWireSend(t, wire, &pgproto3.Parse{Name: "pgss_body", Query: q}, &pgproto3.Describe{ObjectType: 'S', Name: "pgss_body"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ParameterDescription{ParameterOIDs: []uint32{}})
			m := pgssWireReceive(t, f.Ctx, wire)
			description, ok := m.(*pgproto3.RowDescription)
			if !ok || len(description.Fields) != 1 || string(description.Fields[0].Name) != "pgss_wire_volatile" || description.Fields[0].DataTypeOID != 20 || description.Fields[0].Format != 0 {
				t.Fatalf("Describe metadata %#v", m)
			}
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, pattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Bind{DestinationPortal: "pgss_portal", PreparedStatement: "pgss_body"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, pattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Execute{Portal: "pgss_portal", MaxRows: 1}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.DataRow{Values: [][]byte{[]byte("1")}})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.PortalSuspended{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, pattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Execute{Portal: "pgss_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.DataRow{Values: [][]byte{[]byte("2")}})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, pattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Execute{Portal: "pgss_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CommandComplete{CommandTag: []byte("SELECT 0")})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			// ExecutorEnd runs when the portal is disposed, not when an
			// Execute message finishes. Exhausted reuse must not publish early.
			counts(t, pattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: "pgss_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, pattern, 1, 1, 2)
			// A normally closed suspended portal is also successful: charge
			// its one actually processed row, without draining the second row.
			partial := "SELECT nextval('public.pgss_protocol_seq') AS pgss_wire_partial FROM public.pgss_protocol_data pgss_partial"
			partialPattern := "SELECT nextval% AS pgss_wire_partial FROM public.pgss_protocol_data pgss_partial"
			counts(t, partialPattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Parse{Name: "pgss_partial_body", Query: partial}, &pgproto3.Bind{DestinationPortal: "pgss_partial_portal", PreparedStatement: "pgss_partial_body"}, &pgproto3.Execute{Portal: "pgss_partial_portal", MaxRows: 1}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.DataRow{Values: [][]byte{[]byte("3")}})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.PortalSuspended{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, partialPattern, 0, 0, 0)
			pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: "pgss_partial_portal"}, &pgproto3.Sync{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
			counts(t, partialPattern, 1, 1, 1)
			simple(t, "COMMIT")
			counts(t, pattern, 1, 1, 2)
			counts(t, partialPattern, 1, 1, 1)
			run(t, fixture.Select("SELECT last_value,is_called FROM public.pgss_protocol_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))

			simple(t, "SET pg_stat_statements.track_utility=on")
			copySQL := "COPY public.pgss_protocol_copy FROM STDIN"
			counts(t, copySQL, 0, 0, 0)
			tag, err := wire.CopyFrom(f.Ctx, strings.NewReader("7\n8\n"), copySQL)
			if err != nil || tag.String() != "COPY 2" {
				t.Fatalf("wire COPY tag=%s error=%v", tag, err)
			}
			counts(t, copySQL, 1, 1, 2)
			pgssWireSend(t, wire, &pgproto3.Query{String: copySQL})
			m = pgssWireReceive(t, f.Ctx, wire)
			copyIn, ok := m.(*pgproto3.CopyInResponse)
			if !ok || copyIn.OverallFormat != 0 || !reflect.DeepEqual(copyIn.ColumnFormatCodes, []uint16{0}) {
				t.Fatalf("CopyIn metadata %#v", m)
			}
			pgssWireSend(t, wire, &pgproto3.CopyData{Data: []byte("9\n")}, &pgproto3.CopyFail{Message: "pgss wire canceled"})
			m = pgssWireReceive(t, f.Ctx, wire)
			failure, ok := m.(*pgproto3.ErrorResponse)
			if !ok {
				t.Fatalf("CopyFail response %#v", m)
			}
			want := fixture.ControlsReferenceDiagnostic("57014", "COPY from stdin failed: pgss wire canceled")
			want.Context = "COPY pgss_protocol_copy, line 2"
			fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &want)
			pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'I'})
			counts(t, copySQL, 1, 1, 2)
			run(t, fixture.Select("SELECT k FROM public.pgss_protocol_copy ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			t.Run("unused_initialized_close", func(t *testing.T) {
				simple(t, "SET pg_stat_statements.track_utility=off; SET pg_stat_statements.track_planning=on; BEGIN")
				query := "SELECT nextval('public.pgss_protocol_seq') AS pgss_wire_unused FROM public.pgss_protocol_data pgss_unused"
				planCounts := func(groups, plans, calls, rows int64) {
					t.Helper()
					step := fixture.Select("SELECT count(*) AS groups,COALESCE(sum(plans),0)::bigint AS plans,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_wire.pg_stat_statements WHERE query LIKE $1 AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"groups", "plans", "calls", "rows"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{groups, plans, calls, rows}}, "SELECT nextval% AS pgss_wire_unused FROM public.pgss_protocol_data pgss_unused")
					run(t, step)
					run(t, fixture.Prepared(step))
				}
				planCounts(0, 0, 0, 0)
				pgssWireSend(t, wire, &pgproto3.Parse{Name: "pgss_unused_body", Query: query}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				planCounts(0, 0, 0, 0)
				pgssWireSend(t, wire, &pgproto3.Bind{DestinationPortal: "pgss_unused_portal", PreparedStatement: "pgss_unused_body"}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.BindComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				planCounts(1, 1, 0, 0)
				run(t, fixture.Select("SELECT last_value,is_called FROM public.pgss_protocol_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
				// REL_18_6 PortalStart initializes a plain SELECT at Bind.
				// Normal PortalCleanup calls ExecutorEnd even without Execute:
				// one successful disposal, zero rows and no volatile work.
				pgssWireSend(t, wire, &pgproto3.Close{ObjectType: 'P', Name: "pgss_unused_portal"}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.CloseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				planCounts(1, 1, 1, 0)
				simple(t, "COMMIT")
				planCounts(1, 1, 1, 0)
				run(t, fixture.Select("SELECT last_value,is_called FROM public.pgss_protocol_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(3), true}}))
			})
			t.Run("failed_static_bind", func(t *testing.T) {
				simple(t, "SET pg_stat_statements.track_utility=off; SET pg_stat_statements.track_planning=on; BEGIN")
				query := "SELECT 1/0 AS pgss_wire_failed_plan"
				planCounts := func() {
					t.Helper()
					step := fixture.Select("SELECT count(*) AS groups,COALESCE(sum(plans),0)::bigint AS plans,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_wire.pg_stat_statements WHERE query LIKE $1 AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"groups", "plans", "calls", "rows"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(0), int64(0), int64(0), int64(0)}}, "SELECT % AS pgss_wire_failed_plan")
					run(t, step)
					run(t, fixture.Prepared(step))
				}
				planCounts()
				pgssWireSend(t, wire, &pgproto3.Parse{Name: "pgss_failed_definition", Query: query}, &pgproto3.Describe{ObjectType: 'S', Name: "pgss_failed_definition"}, &pgproto3.Sync{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ParseComplete{})
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ParameterDescription{ParameterOIDs: []uint32{}})
				message := pgssWireReceive(t, f.Ctx, wire)
				description, ok := message.(*pgproto3.RowDescription)
				if !ok || len(description.Fields) != 1 || string(description.Fields[0].Name) != "pgss_wire_failed_plan" || description.Fields[0].DataTypeOID != 23 || description.Fields[0].Format != 0 {
					t.Fatalf("syntax-only Describe metadata %#v", message)
				}
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'T'})
				planCounts()
				pgssWireSend(t, wire, &pgproto3.Bind{DestinationPortal: "pgss_failed_portal", PreparedStatement: "pgss_failed_definition"}, &pgproto3.Sync{})
				message = pgssWireReceive(t, f.Ctx, wire)
				failure, ok := message.(*pgproto3.ErrorResponse)
				if !ok {
					t.Fatalf("static Bind error %#v", message)
				}
				want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &want)
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'E'})
				planCounts()
				simple(t, "ROLLBACK")
				pgssWireSend(t, wire, &pgproto3.Execute{Portal: "pgss_failed_portal"}, &pgproto3.Sync{})
				message = pgssWireReceive(t, f.Ctx, wire)
				failure, ok = message.(*pgproto3.ErrorResponse)
				if !ok {
					t.Fatalf("failed Bind left a usable portal %#v", message)
				}
				want = fixture.ControlsReferenceDiagnostic("34000", `portal "pgss_failed_portal" does not exist`)
				fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &want)
				pgssWireWant(t, f.Ctx, wire, &pgproto3.ReadyForQuery{TxStatus: 'I'})
				planCounts()
				recovered := simple(t, "SELECT 7 AS pgss_wire_recovered")
				if len(recovered) != 1 || recovered[0].CommandTag.String() != "SELECT 1" || len(recovered[0].Rows) != 1 || len(recovered[0].Rows[0]) != 1 || string(recovered[0].Rows[0][0]) != "7" {
					t.Fatalf("Sync/recovery after planning error %#v", recovered)
				}
			})
		})
	}
}

func pgssWireSend(t *testing.T, c *pgconn.PgConn, messages ...pgproto3.FrontendMessage) {
	t.Helper()
	for _, m := range messages {
		c.Frontend().Send(m)
	}
	if err := c.Frontend().Flush(); err != nil {
		t.Fatal(err)
	}
}

func pgssWireReceive(t *testing.T, ctx context.Context, c *pgconn.PgConn) pgproto3.BackendMessage {
	t.Helper()
	for {
		m, err := c.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.(*pgproto3.ParameterStatus); ok {
			continue
		}
		return m
	}
}

func pgssWireWant(t *testing.T, ctx context.Context, c *pgconn.PgConn, want pgproto3.BackendMessage) {
	t.Helper()
	got := pgssWireReceive(t, ctx, c)
	if description, ok := want.(*pgproto3.ParameterDescription); ok {
		actual, ok := got.(*pgproto3.ParameterDescription)
		if !ok || len(actual.ParameterOIDs) != len(description.ParameterOIDs) {
			t.Fatalf("parameter description %#v want %#v", got, want)
		}
		for i, oid := range description.ParameterOIDs {
			if actual.ParameterOIDs[i] != oid {
				t.Fatalf("parameter description %#v want %#v", got, want)
			}
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire response %#v want %#v", got, want)
	}
}
