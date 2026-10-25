package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type transactionalCommentKind struct{ name, target, probe string }

var transactionalCommentKinds = []transactionalCommentKind{
	{"table", "TABLE tx_comment", "obj_description('tx_comment'::regclass,'pg_class')"},
	{"column", "COLUMN tx_comment.a", "col_description('tx_comment'::regclass,1)"},
	{"foreign", "FOREIGN TABLE tx_comment_foreign", "obj_description('tx_comment_foreign'::regclass,'pg_class')"},
	{"foreign_column", "COLUMN tx_comment_foreign.a", "col_description('tx_comment_foreign'::regclass,1)"},
	{"schema", "SCHEMA tx_comment_schema", "obj_description('tx_comment_schema'::regnamespace,'pg_namespace')"},
	{"role", "ROLE tx_comment_role", "(SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='tx_comment_role')"},
	{"sequence", "SEQUENCE tx_comment_sequence", "obj_description('tx_comment_sequence'::regclass,'pg_class')"},
}

func transactionalCommentScenario(t *testing.T) *fixture.Pair {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"tx_comment_role", "tx_comment_actor", "tx_comment_renamed"})
	fixture.SchemaReferenceCleanup(t, "tx_comment_schema")
	fixture.SchemaReferenceCleanup(t, "tx_comment_private")
	return fixture.NewScenario(t)
}

func transactionalCommentSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE tx_comment(a INT,b TEXT)",
		"CREATE TABLE tx_comment_rows(a INT)",
		"CREATE SCHEMA tx_comment_schema",
		"CREATE ROLE tx_comment_role",
		"CREATE SEQUENCE tx_comment_sequence",
		"CREATE FOREIGN DATA WRAPPER tx_comment_fdw",
		"CREATE SERVER tx_comment_server FOREIGN DATA WRAPPER tx_comment_fdw",
		"CREATE FOREIGN TABLE tx_comment_foreign(a INT) SERVER tx_comment_server",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO tx_comment VALUES(7,'sentinel')", 1))
	for _, k := range transactionalCommentKinds {
		run(fixture.Exec("COMMENT ON "+k.target+" IS 'old'", 0))
	}
}

func transactionalCommentValue(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, k transactionalCommentKind, want any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT "+k.probe+" AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
}

func transactionalCommentCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, q := range []string{"DROP SERVER IF EXISTS tx_comment_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS tx_comment_fdw"} {
		if _, err := b.DB.ExecContext(ctx, q); err != nil {
			t.Error("foreign metadata cleanup", err)
		}
	}
}
