package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func sepgsqlAuditClient(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var notices []fixture.Diagnostic
	c := fixture.CopyNoticeConnection(t, p, false, &notices, nil)
	exec := func(query string) {
		t.Helper()
		if _, err := c.ExecContext(p.Ctx, query); err != nil {
			t.Fatal(query, err)
		}
	}
	var database string
	if err := c.QueryRowContext(p.Ctx, "SELECT quote_ident(current_database())").Scan(&database); err != nil {
		t.Fatal(err)
	}
	exec("SET client_min_messages=log")
	exec("SELECT sepgsql_setcon('" + sepgsqlR + "')")
	notices = nil
	var count int64
	if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM sepgsql_items").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	want := fixture.Diagnostic{
		Severity: "LOG", SeverityUnlocalized: "LOG", Code: "00000",
		Message: fmt.Sprintf("SELinux: denied { select } scontext=%s tcontext=%s tclass=db_table name=%q permissive=1", sepgsqlR, sepgsqlS, database+".public.sepgsql_items"),
	}
	found := false
	for _, notice := range notices {
		if notice.Message == want.Message {
			found = true
			if notice != want {
				t.Fatalf("LOG diagnostic: got %#v want %#v", notice, want)
			}
		}
	}
	if !found {
		t.Fatalf("client_min_messages=log omitted real denial: %#v", notices)
	}
	exec("SET client_min_messages=warning")
	notices = nil
	if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM sepgsql_items").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for _, notice := range notices {
		if notice.Severity == "LOG" {
			t.Fatalf("warning threshold leaked LOG: %#v", notice)
		}
	}

	for _, query := range []string{"SET sepgsql.permissive=off", "RESET sepgsql.permissive", "SELECT set_config('sepgsql.permissive','off',false)"} {
		_, err := c.ExecContext(p.Ctx, query)
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P02", Message: "parameter \"sepgsql.permissive\" cannot be changed now"})
	}
	exec("BEGIN")
	exec("SET LOCAL sepgsql.debug_audit=on")
	exec("SAVEPOINT audit_mark")
	exec("SET LOCAL sepgsql.debug_audit=off")
	exec("ROLLBACK TO audit_mark")
	var value string
	if err := c.QueryRowContext(p.Ctx, "SHOW sepgsql.debug_audit").Scan(&value); err != nil || value != "on" {
		t.Fatal("savepoint audit setting", value, err)
	}
	exec("COMMIT")
	if err := c.QueryRowContext(p.Ctx, "SHOW sepgsql.debug_audit").Scan(&value); err != nil || value != "off" {
		t.Fatal("transaction-local audit setting escaped", value, err)
	}
}
