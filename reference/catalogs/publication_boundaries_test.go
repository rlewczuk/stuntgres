package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func publicationRelationBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	c := publicationConn(t, p, b)
	defer c.Close()
	run := func(s ...fixture.Step) { publicationRun(t, p, b, c, s...) }
	run(fixture.Exec("CREATE PUBLICATION publication_rt_p", 0), fixture.Exec("CREATE TABLE publication_rt_t(id integer)", 0), fixture.Exec("CREATE TEMP TABLE publication_rt_temp(id integer)", 0), fixture.Exec("CREATE UNLOGGED TABLE publication_rt_unlogged(id integer)", 0), fixture.Exec("CREATE VIEW publication_rt_view AS SELECT 1 AS id", 0), fixture.Exec("CREATE MATERIALIZED VIEW publication_rt_matview AS SELECT 1 AS id", 1), fixture.Exec("CREATE SEQUENCE publication_rt_seq", 0), fixture.Exec("CREATE FOREIGN DATA WRAPPER publication_rt_fdw", 0), fixture.Exec("CREATE SERVER publication_rt_server FOREIGN DATA WRAPPER publication_rt_fdw", 0), fixture.Exec("CREATE FOREIGN TABLE publication_rt_foreign(id integer) SERVER publication_rt_server", 0))
	defer func() {
		for _, q := range []string{"DROP SERVER IF EXISTS publication_rt_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS publication_rt_fdw CASCADE"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error("foreign relation cleanup", err)
			}
		}
	}()
	for _, tc := range []struct{ relation, name, kind string }{
		{"publication_rt_temp", "publication_rt_temp", "temporary tables"},
		{"publication_rt_unlogged", "publication_rt_unlogged", "unlogged tables"},
		{"publication_rt_view", "publication_rt_view", "views"},
		{"publication_rt_matview", "publication_rt_matview", "materialized views"},
		{"publication_rt_seq", "publication_rt_seq", "sequences"},
		{"publication_rt_foreign", "publication_rt_foreign", "foreign tables"},
		{"pg_catalog.pg_class", "pg_class", "system tables"},
	} {
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE "+tc.relation, "22023", `cannot add relation "`+tc.name+`" to publication`, "This operation is not supported for "+tc.kind+".", ""), publicationMembers("publication_rt_p"))
	}
	run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA pg_catalog", "22023", `cannot add schema "pg_catalog" to publication`, "This operation is not supported for system schemas.", ""))
	var temporary string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&temporary); err != nil {
		t.Fatal(err)
	}
	run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA "+temporary, "22023", `cannot add schema "`+temporary+`" to publication`, "Temporary schemas cannot be replicated.", ""))
	run(fixture.Exec("CREATE PUBLICATION publication_rt_schema FOR TABLES IN SCHEMA public", 0), publicationMembers("publication_rt_schema", []any{"public", "publication_rt_t"}))
	run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_t", 0), fixture.Exec("DROP PUBLICATION publication_rt_schema", 0))
}

func publicationNotices(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	var notices []fixture.Diagnostic
	_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
	run(fixture.Exec("CREATE PUBLICATION publication_rt_notice", 0))
	want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55000", Message: `"wal_level" is insufficient to publish logical changes`, Hint: `Set "wal_level" to "logical" before creating subscriptions.`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal("complete publication WAL warning", notices, want)
	}
	notices = nil
	run(fixture.Exec("DROP PUBLICATION publication_rt_notice", 0))
	if len(notices) != 0 {
		t.Fatal("unexpected DROP notice", notices)
	}
	run(fixture.Exec("DROP PUBLICATION IF EXISTS publication_rt_notice", 0))
	want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `publication "publication_rt_notice" does not exist, skipping`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal("complete missing publication notice", notices, want)
	}
}
