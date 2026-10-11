package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"qc_owner", "qc_reader"})
	fixture.SchemaReferenceCleanup(t, "qc_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER qc_owner", 0))
			run(fixture.Exec("CREATE USER qc_reader", 0))
			run(fixture.Exec("CREATE SCHEMA qc_private AUTHORIZATION qc_owner", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION qc_owner", 0))
			run(fixture.Exec("CREATE TABLE qc_acl_source(i INTEGER)", 0))
			run(fixture.Exec("INSERT INTO qc_acl_source VALUES(7)", 1))
			run(fixture.Exec("CREATE TABLE qc_private.qc_owned AS SELECT i FROM qc_acl_source", 1))
			heldQuery := "CREATE TABLE qc_acl_held AS SELECT i FROM qc_acl_source"
			held, err := conn.PrepareContext(p.Ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("SET SESSION AUTHORIZATION qc_reader", 0))
			for _, body := range []string{
				"SELECT i FROM qc_acl_source", "SELECT i FROM qc_acl_source WHERE false",
				"SELECT i FROM qc_acl_source LIMIT 0", "SELECT FROM qc_acl_source",
				"SELECT EXISTS(SELECT i FROM qc_acl_source) AS e",
			} {
				t.Run(body, func(t *testing.T) {
					s := fixture.ImplicitError("CREATE TABLE qc_acl_denied AS "+body, fixture.Diagnostic{Code: "42501", Message: "permission denied for table qc_acl_source"})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }, b.PG, "qc_acl_denied")
				})
			}
			t.Run("held_source_denial", func(t *testing.T) {
				s := fixture.ImplicitError(heldQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table qc_acl_source"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: heldQuery}, b.Recorder, s)
				fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }, b.PG, "qc_acl_held")
			})
			t.Run("source_namespace_nodata", func(t *testing.T) {
				q := "CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_private.qc_owned WITH NO DATA"
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
				want.Position = "51"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ErrorDiagnostic(q, want))
				fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }, b.PG, "qc_namespace_denied")
			})
			t.Run("schema_denial", func(t *testing.T) {
				q := "CREATE TABLE qc_private.qc_denied AS SELECT 1 AS i"
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.ErrorDiagnostic(q, want))
			})
			t.Run("skip_denied_schema", func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("SET SESSION AUTHORIZATION qc_reader", 0))
				defer noticeConn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				q := "CREATE TABLE IF NOT EXISTS qc_private.qc_owned AS SELECT 1 AS i"
				fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec(q, 0))
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_owned" already exists, skipping`}
				if len(notices) != 1 || notices[0] != want {
					t.Fatalf("denied-schema skip notices %#v want %#v", notices, want)
				}
			})
			for _, q := range []string{
				"CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_private.qc_owned",
				"CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_acl_source",
				"CREATE TABLE qc_private.qc_ro_schema AS SELECT 1 AS i",
			} {
				t.Run("read_only/"+q, func(t *testing.T) {
					tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
					if b.PG && q == "CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_private.qc_owned" {
						want = fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
						want.Position = "51"
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic(q, want))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i FROM qc_acl_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT i FROM qc_private.qc_owned", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceQueryTableCreateNoDataAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"qc_nd_owner", "qc_nd_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER qc_nd_owner", 0))
			run(fixture.Exec("CREATE USER qc_nd_reader", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION qc_nd_owner", 0))
			run(fixture.Exec("CREATE TABLE qc_nd_source(i INTEGER)", 0))
			run(fixture.Exec("INSERT INTO qc_nd_source VALUES(7)", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
			run(fixture.Exec("CREATE TABLE qc_nd_acl AS SELECT i FROM qc_nd_source WITH NO DATA", 0))
			run(fixture.Select("SELECT * FROM qc_nd_acl", []string{"i"}, []string{"INT4"}, nil))
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
			defer noticeConn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE TABLE IF NOT EXISTS qc_nd_acl AS SELECT i FROM qc_nd_source", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_nd_acl" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("skip notices %#v want %#v", notices, want)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i FROM qc_nd_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			// Genuine PostgreSQL ACL facility: CTAS population does not require
			// INSERT on its freshly created target, unlike a later ordinary INSERT.
			if b.PG {
				run(fixture.Exec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
				run(fixture.Exec("BEGIN", 0))
				defer conn.ExecContext(context.Background(), "ROLLBACK")
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE INSERT ON TABLES FROM qc_nd_reader", 0))
				run(fixture.Exec("CREATE TABLE qc_no_insert AS SELECT 9 AS i", 1))
				run(fixture.Select("SELECT i FROM qc_no_insert", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
				run(fixture.ImplicitError("INSERT INTO qc_no_insert VALUES(10)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table qc_no_insert"}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
