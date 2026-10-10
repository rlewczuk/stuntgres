package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceFixtureSessionAuthorizationReset(t *testing.T) {
	// The admin seam exists only in pinned mode. Fresh case/native pools are
	// exercised by the same definition even when Docker is disabled.
	contaminateAdmin := func(t *testing.T) {
		t.Helper()
		if !*referenceMode {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := referenceAdmin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Error("return contaminated admin", err)
			}
		}()
		// Model an owner-bearing replay returning its physical session without
		// clearing authorization, settings, objects or its aborted transaction.
		for _, query := range []string{
			"SET SESSION AUTHORIZATION postgres",
			"CREATE TABLE fixture_replay_owner(v INT)",
			"CREATE TEMP TABLE fixture_admin_temp(v INT)",
			"INSERT INTO fixture_admin_temp VALUES(19)",
			"PREPARE fixture_admin_plan AS SELECT 23::INT",
			"SELECT pg_advisory_lock(44223311)",
		} {
			if _, err := conn.ExecContext(ctx, query); err != nil {
				t.Fatal(query, err)
			}
		}
		var owner string
		if err := conn.QueryRowContext(ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='fixture_replay_owner'::regclass").Scan(&owner); err != nil || owner != "postgres" {
			t.Fatalf("replay owner: got %q, want postgres, error %v", owner, err)
		}
		var value int64
		if err := conn.QueryRowContext(ctx, "SELECT v FROM fixture_admin_temp").Scan(&value); err != nil || value != 19 {
			t.Fatalf("temp value=%d error=%v", value, err)
		}
		if err := conn.QueryRowContext(ctx, "EXECUTE fixture_admin_plan").Scan(&value); err != nil || value != 23 {
			t.Fatalf("prepared value=%d error=%v", value, err)
		}
		for _, query := range []string{
			"SET application_name='stale_fixture_admin'",
			"SET search_path=pg_catalog",
			"SET ROLE pg_read_all_data",
		} {
			if _, err := conn.ExecContext(ctx, query); err != nil {
				t.Fatal(query, err)
			}
		}
		var sessionUser, currentUser, application, path string
		if err := conn.QueryRowContext(ctx, "SELECT session_user,current_user,current_setting('application_name'),current_setting('search_path')").Scan(&sessionUser, &currentUser, &application, &path); err != nil {
			t.Fatal(err)
		}
		if sessionUser != "postgres" || currentUser != "pg_read_all_data" || application != "stale_fixture_admin" || path != "pg_catalog" {
			t.Fatalf("contamination did not take effect: %q %q %q %q", sessionUser, currentUser, application, path)
		}
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			t.Fatal(err)
		}
		_, err = conn.ExecContext(ctx, "SELECT 1/0")
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
	}
	checkReset := func(t *testing.T) {
		t.Helper()
		if !*referenceMode {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var sessionUser, currentUser, owner, application, path string
		var empty, noTemp, noPrepared, noAdvisory bool
		query := `SELECT session_user, current_user, pg_get_userbyid(nspowner),
			NOT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=pg_namespace.oid),
			current_setting('application_name'), current_setting('search_path'),
			NOT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=pg_my_temp_schema()),
			NOT EXISTS(SELECT 1 FROM pg_prepared_statements WHERE name='fixture_admin_plan'),
			NOT EXISTS(SELECT 1 FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory')
			FROM pg_namespace WHERE nspname='public'`
		if err := referenceAdmin.QueryRowContext(ctx, query).Scan(&sessionUser, &currentUser, &owner, &empty, &application, &path, &noTemp, &noPrepared, &noAdvisory); err != nil {
			t.Fatal(err)
		}
		if sessionUser != "stuntgres" || currentUser != "stuntgres" || owner != "stuntgres" || !empty {
			t.Fatalf("schema reset: session=%q current=%q owner=%q empty=%t", sessionUser, currentUser, owner, empty)
		}
		if application != "" || path != `"$user", public` || !noTemp || !noPrepared || !noAdvisory {
			t.Fatalf("session reset: app=%q path=%q noTemp=%t noPrepared=%t noAdvisory=%t", application, path, noTemp, noPrepared, noAdvisory)
		}
	}
	caseApplication := func(t *testing.T, p *referencePair, want string) {
		t.Helper()
		step := referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{want}})
		step.prepared = true
		p.step(t, step)
	}
	checkNativeControl := func(t *testing.T, p *referencePair) {
		t.Helper()
		for _, check := range []struct{ query, want string }{
			{"SELECT session_user", "postgres"},
			{"SELECT current_user", "postgres"},
			{"SHOW application_name", ""},
		} {
			var value string
			if err := p.native.QueryRowContext(p.ctx, check.query).Scan(&value); err != nil || value != check.want {
				t.Fatalf("native control %s value=%q want=%q error=%v", check.query, value, check.want, err)
			}
		}
	}
	if !t.Run("replay", func(t *testing.T) {
		p := newReferenceScenario(t)
		p.step(t, referenceExec("CREATE TABLE fixture_case_state(v INT)", 0))
		p.step(t, referenceExec("INSERT INTO fixture_case_state VALUES(7)", 1))
		p.step(t, referenceExec("SET application_name='stale_fixture_case'", 0))
		caseApplication(t, p, "stale_fixture_case")
		contaminateAdmin(t)
		checkNativeControl(t, p)
	}) {
		return
	}
	// Check after-case sanitation independently, then deliberately taint the
	// admin again so the next fixture's before-reset must also sanitize it.
	checkReset(t)
	t.Run("next_case", func(t *testing.T) {
		contaminateAdmin(t)
		p := newReferenceScenario(t)
		checkReset(t)
		caseApplication(t, p, "")
		checkNativeControl(t, p)
		p.step(t, referenceExec("CREATE TABLE fixture_case_state(v INT)", 0))
		p.step(t, referenceSelect("SELECT v FROM fixture_case_state", []string{"v"}, []string{"INT4"}, nil))
	})
}
