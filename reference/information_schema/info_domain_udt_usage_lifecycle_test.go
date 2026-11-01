package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageLifecycle(t *testing.T) {
	duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		var notices []fixture.Diagnostic
		_, noticeConn, noticeRecorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer noticeConn.Close()
		c, r = noticeConn, noticeRecorder
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE SCHEMA du_scope", "CREATE SCHEMA du_moved"} {
			run(fixture.Exec(q, 0))
		}
		where := "domain_schema IN ('du_scope','du_moved')"
		held := cduHold(t, p, c, duProjection(b.PG, where, nil).Query)
		defer held.Statement.Close()
		check := func(want [][]any) { duCheck(t, p, c, r, held, duProjection(b.PG, where, want)) }
		check(nil)
		for _, q := range []string{"CREATE DOMAIN du_scope.p AS integer", "CREATE DOMAIN du_scope.c AS du_scope.p", "CREATE DOMAIN du_scope.sibling AS text"} {
			run(fixture.Exec(q, 0))
		}
		want := [][]any{duRow(database, "pg_catalog", "int4", "du_scope", "p"), duRow(database, "du_scope", "p", "du_scope", "c"), duRow(database, "pg_catalog", "text", "du_scope", "sibling")}
		check(want)
		retained, err := b.DB.QueryContext(p.Ctx, held.SQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		duMetadata(t, columns, b.PG)
		run(fixture.Exec("ALTER DOMAIN du_scope.p RENAME TO renamed", 0))
		want = [][]any{duRow(database, "pg_catalog", "int4", "du_scope", "renamed"), duRow(database, "du_scope", "renamed", "du_scope", "c"), want[2]}
		check(want)
		run(fixture.Exec("ALTER DOMAIN du_scope.renamed SET SCHEMA du_moved", 0))
		want = [][]any{duRow(database, "pg_catalog", "int4", "du_moved", "renamed"), duRow(database, "du_moved", "renamed", "du_scope", "c"), want[2]}
		check(want)
		run(fixture.Exec("ALTER DOMAIN du_scope.c SET SCHEMA du_moved", 0))
		want = [][]any{want[0], duRow(database, "du_moved", "renamed", "du_moved", "c"), want[2]}
		check(want)
		duRetained(t, retained, b.PG, [][]any{duRow(database, "pg_catalog", "int4", "du_scope", "p"), duRow(database, "du_scope", "p", "du_scope", "c"), want[2]})
		duMetadata(t, columns, b.PG)
		run(fixture.Exec("DROP DOMAIN du_moved.c", 0))
		check([][]any{want[0], want[2]})
		run(fixture.Exec("CREATE DOMAIN du_moved.c AS du_moved.renamed", 0))
		check(want)
		run(fixture.Exec("DROP DOMAIN du_moved.renamed CASCADE", 0))
		check(want[2:])
		fixture.DomainReferenceNotice(t, &notices, "drop cascades to type du_moved.c", "")
		run(fixture.Select("SELECT typname FROM pg_catalog.pg_type WHERE typnamespace='du_moved'::regnamespace AND typtype='d'", []string{"typname"}, []string{"NAME"}, nil))
		run(fixture.Exec("DROP DOMAIN du_scope.sibling", 0))
		check(nil)
	})
}
