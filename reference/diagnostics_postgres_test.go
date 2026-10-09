//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestReferenceConditionalErrorFields(t *testing.T) {
	p := newReferencePair(t)
	assertPostgres := func(query string) referenceDiagnostic {
		t.Helper()
		_, err := p.postgres.ExecContext(p.ctx, query)
		if err == nil {
			t.Fatalf("query succeeded, want diagnostic: %s", query)
		}
		return referenceExtractDiagnostic(t, fmt.Errorf("wrapped: %w", err))
	}
	for _, tc := range []struct {
		name, query string
		want        referenceDiagnostic
	}{
		{"column", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', COLUMN='only_column'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Column: "only_column", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"schema", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', SCHEMA='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Schema: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"table", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', TABLE='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Table: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"datatype", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', DATATYPE='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", DataType: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"constraint", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', CONSTRAINT='only_object'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Constraint: "only_object", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"detail", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', DETAIL='facts'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Detail: "facts", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
		{"hint", `DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='23505', MESSAGE='independent fields', HINT='advice'; END $$;`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: "independent fields", Hint: "advice", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assertPostgres(tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("conditional diagnostic = %#v, want %#v", got, tc.want)
			}
		})
	}
	internal := assertPostgres(`DO $$ BEGIN EXECUTE 'SELECT 1 +'; END $$;`)
	if internal.Severity != "ERROR" || internal.SeverityUnlocalized != "ERROR" ||
		internal.Code != "42601" || internal.Message != "syntax error at end of input" ||
		internal.Position != "" || internal.Detail != "" || internal.Hint != "" ||
		internal.Schema != "" || internal.Table != "" || internal.Column != "" ||
		internal.DataType != "" || internal.Constraint != "" ||
		internal.InternalQuery != "SELECT 1 +" || internal.InternalPosition != "11" || internal.Context == "" {
		t.Fatalf("internal query diagnostic = %#v", internal)
	}
}

func TestReferencePermissionAndCancellationFields(t *testing.T) {
	p := newReferencePair(t)
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT); CREATE ROLE error_fields_denied NOLOGIN; GRANT USAGE ON SCHEMA public TO error_fields_denied"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := referenceAdmin.ExecContext(cleanupCtx, "DROP OWNED BY error_fields_denied; DROP ROLE IF EXISTS error_fields_denied"); err != nil {
			t.Errorf("drop reference role: %v", err)
		}
	}()
	roleConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer roleConn.Close()
	if _, err := roleConn.ExecContext(p.ctx, "SET ROLE error_fields_denied"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := roleConn.ExecContext(cleanupCtx, "RESET ROLE"); err != nil {
			t.Errorf("reset reference role: %v", err)
		}
	}()
	rows, err := roleConn.QueryContext(p.ctx, "SELECT id FROM diagnostic_rows")
	if rows != nil {
		rows.Close()
	}
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", err), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
		Message: "permission denied for table diagnostic_rows",
	})
	cancelConn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelConn.Close()
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout='50ms'"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := cancelConn.ExecContext(cleanupCtx, "SET statement_timeout=0"); err != nil {
			t.Errorf("reset reference statement timeout: %v", err)
		}
	}()
	timeoutCtx, timeoutCancel := context.WithTimeout(p.ctx, 5*time.Second)
	_, err = cancelConn.ExecContext(timeoutCtx, "SELECT pg_sleep(10)")
	timeoutCancel()
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", err), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to statement timeout",
	})
	if _, err := cancelConn.ExecContext(p.ctx, "SET statement_timeout=0"); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := cancelConn.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	queryCtx, queryCancel := context.WithTimeout(p.ctx, 8*time.Second)
	defer queryCancel()
	result := make(chan error, 1)
	go func() {
		_, queryErr := cancelConn.ExecContext(queryCtx, "SELECT pg_sleep(10)")
		result <- queryErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	active := false
	for time.Now().Before(deadline) {
		var waitEvent sql.NullString
		if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT wait_event FROM pg_stat_activity WHERE pid=$1 AND state='active'", pid).Scan(&waitEvent); err == nil && waitEvent.Valid && waitEvent.String == "PgSleep" {
			active = true
			break
		}
	}
	if !active {
		queryCancel()
		<-result
		t.Fatal("timed out waiting for reference query to enter PgSleep")
	}
	var canceled bool
	if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
		queryCancel()
		<-result
		t.Fatalf("cancel backend: canceled=%v error=%v", canceled, err)
	}
	userErr := <-result
	queryCancel()
	referenceAssertDiagnostic(t, fmt.Errorf("wrapped: %w", userErr), &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to user request",
	})
}
