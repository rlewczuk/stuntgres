package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatioAllIndexesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statio_reader"})
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin, reader, peer := vacuumConn(t, p, b), vacuumConn(t, p, b), vacuumConn(t, p, b)
		statioExec(t, p, b, admin, "CREATE ROLE statio_reader", "CREATE SCHEMA statio_secret", "REVOKE ALL ON SCHEMA statio_secret FROM PUBLIC", "CREATE TABLE statio_secret.statio_hidden(k integer)", "CREATE INDEX statio_hidden_i ON statio_secret.statio_hidden(k)")
		t.Cleanup(func() {
			copyProgressExec(t, p.Ctx, admin, "GRANT SELECT ON pg_catalog.pg_statio_all_indexes TO PUBLIC")
			copyProgressExec(t, p.Ctx, admin, "DROP SCHEMA statio_secret CASCADE")
		})
		q := "SELECT * FROM pg_catalog.pg_statio_all_indexes WHERE relname='statio_hidden'"
		before := statioRows(t, p, b, admin, q)
		if len(before) != 1 {
			t.Fatal("hidden index missing", before)
		}
		for _, c := range []*sql.Conn{reader, peer} {
			statioExec(t, p, b, c, "SET ROLE statio_reader")
			if got := statioRows(t, p, b, c, q); !reflect.DeepEqual(got, before) {
				t.Fatal("underlying ACL filtered/redacted", got, before)
			}
			vacuumRun(t, p, b, c, fixture.ImplicitError("SELECT k FROM statio_secret.statio_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema statio_secret", Position: "15"}))
		}
		stmt, err := reader.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		statioExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_statio_all_indexes FROM PUBLIC")
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_statio_all_indexes"}
		if rows, err := stmt.QueryContext(p.Ctx); err == nil {
			rows.Close()
			t.Fatal("held prepared ACL cached")
		} else if got := fixture.ExtractDiagnostic(t, err); got != want {
			t.Fatal("held diagnostic", got, want)
		}
		for _, c := range []*sql.Conn{reader, peer} {
			vacuumRun(t, p, b, c, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: want.Message}))
		}
		statioExec(t, p, b, admin, "GRANT SELECT ON pg_catalog.pg_statio_all_indexes TO PUBLIC", "GRANT pg_read_all_stats TO statio_reader")
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		statioMetadata(t, rows, b.PG)
		if !rows.Next() {
			t.Fatal("restored prepared has no row")
		}
		rows.Close()
		if got := statioRows(t, p, b, reader, q); !reflect.DeepEqual(got, before) {
			t.Fatal("stats role changed disclosure", got)
		}
		statioExec(t, p, b, reader, "RESET ROLE")
		statioExec(t, p, b, peer, "RESET ROLE")
	})
}

func TestReferenceStatioAllIndexesReadOnly(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_sentinel(k integer)")
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO statio_sentinel VALUES(99)", 1))
		for _, populated := range []bool{false, true} {
			if populated {
				statioExec(t, p, b, c, "CREATE INDEX statio_sentinel_i ON statio_sentinel(k)")
			}
			before := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE relname='statio_sentinel'")
			for _, tc := range []struct{ q, verb, action, gerund string }{
				{"INSERT INTO pg_statio_all_indexes(relid) VALUES(1)", "insert into", "INSERT", "inserting into"},
				{"INSERT INTO pg_statio_all_indexes(relid) SELECT relid FROM pg_statio_all_indexes WHERE false", "insert into", "INSERT", "inserting into"},
				{"INSERT INTO pg_statio_all_indexes(relid) SELECT relid FROM pg_statio_all_indexes", "insert into", "INSERT", "inserting into"},
				{"UPDATE pg_statio_all_indexes SET idx_blks_hit=0 WHERE false", "update", "UPDATE", "updating"},
				{"UPDATE pg_statio_all_indexes SET idx_blks_hit=0 WHERE relname='statio_sentinel'", "update", "UPDATE", "updating"},
				{"DELETE FROM pg_statio_all_indexes WHERE false", "delete from", "DELETE", "deleting from"},
				{"DELETE FROM pg_statio_all_indexes", "delete from", "DELETE", "deleting from"},
				{"TRUNCATE pg_statio_all_indexes", "", "", ""},
			} {
				want := fixture.Diagnostic{Code: "55000", Message: "cannot " + tc.verb + " view \"pg_statio_all_indexes\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."}
				if tc.action == "" {
					want = fixture.Diagnostic{Code: "42809", Message: "\"pg_statio_all_indexes\" is not a table"}
				}
				for _, qualified := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						q := tc.q
						if qualified {
							q = strings.ReplaceAll(q, "pg_statio_all_indexes", "pg_catalog.pg_statio_all_indexes")
						}
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && tc.action != "" {
							s.ErrorStage = "prepare"
						}
						vacuumRun(t, p, b, c, s)
						if got := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE relname='statio_sentinel'"); !reflect.DeepEqual(got, before) {
							t.Fatal("rejected mutation changed counters", got, before)
						}
					}
				}
			}
			// No index-reading sentinel query is interleaved with counter snapshots.
			statioExec(t, p, b, c, "SET enable_indexscan=off", "SET enable_bitmapscan=off")
			vacuumRun(t, p, b, c, fixture.Select("SELECT k FROM statio_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		}
	})
}
