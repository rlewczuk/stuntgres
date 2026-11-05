package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionLifecycle(t *testing.T) {
	connectionCase(t, true, connectionLifecycle)
}

func connectionLifecycle(s *connectionScenario) {
	t := s.t
	s.list()
	firstApp, first := s.open(new("first"))
	_, second := s.open(new("second"))
	_, defaultPID := s.open(nil)
	if first == second || first == defaultPID || second == defaultPID {
		t.Fatal("handles share a physical session")
	}
	s.list("first", "second")
	s.remote(new("first"), first, "CREATE TEMP TABLE owned_state(v text); INSERT INTO owned_state VALUES('first'); SET application_name="+"'"+firstApp+"'")
	prepareBefore := s.activity()
	statement, err := s.c.PrepareContext(s.p.Ctx, "SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value")
	if err != nil {
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	s.unchangedActivity(prepareBefore)
	before := len(s.provider.Snapshot())
	s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", "first", s.conninfo("must_not_open", false)))
	s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", "first", "dbname='unterminated"))
	freshParse := connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "08001", "could not establish connection", "parse_retry", "dbname")
	freshParse.Diagnostic.Detail = `missing "=" after "dbname" in connection info string`
	s.run(freshParse)
	s.list("first", "second")
	if len(s.provider.Snapshot()) != before {
		t.Fatal("duplicate physically opened provider")
	}
	_, retryPID := s.open(new("parse_retry"))
	s.disconnect(new("parse_retry"), retryPID)
	if s.pid(firstApp) != first {
		t.Fatal("duplicate replaced genuine original")
	}
	s.remoteText(new("first"), first, "SELECT v FROM owned_state", "first")
	s.remoteText(new("second"), second, "SELECT count(*)::text FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname='owned_state'", "0")
	// A handle opened in a rolled-back local transaction/savepoint still exists.
	s.exec("BEGIN")
	s.exec("SAVEPOINT local_only")
	_, rollbackPID := s.open(new("survives_rollback"))
	s.exec("ROLLBACK TO SAVEPOINT local_only")
	s.exec("ROLLBACK")
	s.list("first", "second", "survives_rollback")
	s.remote(new("survives_rollback"), rollbackPID, "BEGIN; CREATE TEMP TABLE tx_state(v integer); INSERT INTO tx_state VALUES(7)")
	s.remoteText(new("survives_rollback"), rollbackPID, "SELECT v::text FROM tx_state", "7")
	s.remote(new("survives_rollback"), rollbackPID, "ROLLBACK")
	// Closing a real backend with uncommitted permanent effects rolls them back.
	if _, err := fixture.Admin.ExecContext(s.p.Ctx, "CREATE TABLE IF NOT EXISTS public.dbc_remote_effect(pid integer)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := fixture.Admin.ExecContext(ctx, "DROP TABLE IF EXISTS public.dbc_remote_effect"); err != nil {
			t.Error(err)
		}
	})
	s.remote(new("second"), second, fmt.Sprintf("BEGIN; INSERT INTO public.dbc_remote_effect VALUES(%d)", second))
	s.observerCount(second, 0)
	s.remote(new("second"), second, "COMMIT")
	s.observerCount(second, 1)
	s.remote(nil, defaultPID, fmt.Sprintf("CREATE TEMP TABLE default_state(v text); INSERT INTO default_state VALUES('preserved'); BEGIN; INSERT INTO public.dbc_remote_effect VALUES(%d)", defaultPID))
	s.remote(new("first"), first, fmt.Sprintf("BEGIN; INSERT INTO public.dbc_remote_effect VALUES(%d)", first))
	s.observerCount(first, 0)
	s.disconnect(new("first"), first)
	s.observerCount(first, 0)
	s.list("second", "survives_rollback")
	s.run(connectionError("SELECT dbc_ext.dblink_disconnect($1::text) AS value", "08003", `connection "first" not available`, "first"))
	s.remoteText(new("second"), second, "SELECT 'still-live'::text", "still-live")
	// New physical local sessions have no access to another session's handles.
	other, err := s.b.DB.Conn(s.p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, s.p.Ctx, other, s.b.Recorder, fixture.Select("SELECT dbc_ext.dblink_get_connections() AS value", []string{"value"}, []string{"_TEXT"}, [][]any{{nil}}))
	fixture.RunStep(t, s.p.Ctx, other, s.b.Recorder, connectionError("SELECT dbc_ext.dblink_disconnect('second') AS value", "08003", `connection "second" not available`))
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	// Failed default replacement preserves old actual backend/session state.
	s.run(parserFailure("dbname", "missing \"=\" after \"dbname\" in connection info string"))
	s.externalFailure("SELECT dbc_ext.dblink_connect($1::text) AS value", "host=/dbc_nonexistent_socket port=5432 dbname=stuntgres_reference connect_timeout=1")
	var alive int
	if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", defaultPID).Scan(&alive); err != nil || alive != 1 {
		t.Fatal("failed replacement lost default", alive, err)
	}
	s.remoteText(nil, defaultPID, "SELECT v FROM default_state", "preserved")
	s.observerCount(defaultPID, 0)
	_, replacement := s.open(nil)
	if replacement == defaultPID {
		t.Fatal("default replacement reused backend")
	}
	connectionGone(t, s.p.Ctx, defaultPID)
	s.observerCount(defaultPID, 0)
	if !s.b.PG {
		if _, err := s.wire(defaultPID).Execute(s.p.Ctx, "SELECT 1"); err == nil {
			t.Fatal("replaced actual wire executed")
		}
	}
	s.disconnect(nil, replacement)
	s.disconnect(new("second"), second)
	s.disconnect(new("survives_rollback"), rollbackPID)
	s.list()
}

func (s *connectionScenario) remoteText(name *string, pid int32, q, want string) {
	s.t.Helper()
	if s.b.PG {
		query, args := "SELECT value FROM dbc_ext.dblink($1::text) AS observed(value text)", []any{q}
		if name != nil {
			query, args = "SELECT value FROM dbc_ext.dblink($1::text,$2::text) AS observed(value text)", []any{*name, q}
		}
		step := connectionText(query, want, args...)
		fixture.RunStep(s.t, s.p.Ctx, s.c, s.b.Recorder, step)
		return
	}
	r, err := s.wire(pid).Execute(s.p.Ctx, q)
	if err != nil || len(r) != 1 || !r[0].Tuple || r[0].RowCount != 1 || len(r[0].Cells) != 1 || r[0].Cells[0].Null || r[0].Cells[0].Text != want {
		s.t.Fatal("actual remote state", r, err, "want", want)
	}
}
func (s *connectionScenario) observerCount(pid int32, want int) {
	s.t.Helper()
	var got int
	if err := fixture.Admin.QueryRowContext(s.p.Ctx, "SELECT count(*) FROM public.dbc_remote_effect WHERE pid=$1", pid).Scan(&got); err != nil || got != want {
		s.t.Fatal("independent transaction observer", got, want, err)
	}
}

func parserFailure(conninfo, detail string) fixture.Step {
	step := connectionError("SELECT dbc_ext.dblink_connect($1::text) AS value", "08001", "could not establish connection", conninfo)
	step.Diagnostic.Detail = detail
	return step
}
