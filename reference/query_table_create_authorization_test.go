package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceQueryTableCreateAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"qc_owner", "qc_reader"})
	schemaReferenceCleanup(t, "qc_private")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(referenceExec("CREATE USER qc_owner", 0))
			run(referenceExec("CREATE USER qc_reader", 0))
			run(referenceExec("CREATE SCHEMA qc_private AUTHORIZATION qc_owner", 0))
			if b.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION qc_owner", 0))
			run(referenceExec("CREATE TABLE qc_acl_source(i INTEGER)", 0))
			run(referenceExec("INSERT INTO qc_acl_source VALUES(7)", 1))
			run(referenceExec("CREATE TABLE qc_private.qc_owned AS SELECT i FROM qc_acl_source", 1))
			heldQuery := "CREATE TABLE qc_acl_held AS SELECT i FROM qc_acl_source"
			held, err := conn.PrepareContext(p.ctx, heldQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec("SET SESSION AUTHORIZATION qc_reader", 0))
			for _, body := range []string{
				"SELECT i FROM qc_acl_source", "SELECT i FROM qc_acl_source WHERE false",
				"SELECT i FROM qc_acl_source LIMIT 0", "SELECT FROM qc_acl_source",
				"SELECT EXISTS(SELECT i FROM qc_acl_source) AS e",
			} {
				t.Run(body, func(t *testing.T) {
					s := referenceImplicitError("CREATE TABLE qc_acl_denied AS "+body, referenceDiagnostic{Code: "42501", Message: "permission denied for table qc_acl_source"})
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, conn, b.recorder, s)
					queryTableCreateAbsent(t, func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }, b.pg, "qc_acl_denied")
				})
			}
			t.Run("held_source_denial", func(t *testing.T) {
				s := referenceImplicitError(heldQuery, referenceDiagnostic{Code: "42501", Message: "permission denied for table qc_acl_source"})
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: heldQuery}, b.recorder, s)
				queryTableCreateAbsent(t, func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }, b.pg, "qc_acl_held")
			})
			t.Run("source_namespace_nodata", func(t *testing.T) {
				q := "CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_private.qc_owned WITH NO DATA"
				want := controlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
				want.Position = "51"
				referenceRunStep(t, p.ctx, conn, b.recorder, referenceErrorDiagnostic(q, want))
				queryTableCreateAbsent(t, func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }, b.pg, "qc_namespace_denied")
			})
			t.Run("schema_denial", func(t *testing.T) {
				q := "CREATE TABLE qc_private.qc_denied AS SELECT 1 AS i"
				want := controlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
				referenceRunStep(t, p.ctx, conn, b.recorder, referenceErrorDiagnostic(q, want))
			})
			t.Run("skip_denied_schema", func(t *testing.T) {
				var notices []referenceDiagnostic
				_, noticeConn, recorder := noticeReferencePool(t, p, b.pg, &notices)
				referenceRunStep(t, p.ctx, noticeConn, recorder, referenceExec("SET SESSION AUTHORIZATION qc_reader", 0))
				defer noticeConn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				q := "CREATE TABLE IF NOT EXISTS qc_private.qc_owned AS SELECT 1 AS i"
				referenceRunStep(t, p.ctx, noticeConn, recorder, referenceExec(q, 0))
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_owned" already exists, skipping`}
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
					tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					want := controlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
					if b.pg && q == "CREATE TABLE qc_namespace_denied AS SELECT i FROM qc_private.qc_owned" {
						want = controlsReferenceDiagnostic("42501", "permission denied for schema qc_private")
						want.Position = "51"
					}
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic(q, want))
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT i FROM qc_acl_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceSelect("SELECT i FROM qc_private.qc_owned", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceQueryTableCreateNoDataAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"qc_nd_owner", "qc_nd_reader"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(referenceExec("CREATE USER qc_nd_owner", 0))
			run(referenceExec("CREATE USER qc_nd_reader", 0))
			if b.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION qc_nd_owner", 0))
			run(referenceExec("CREATE TABLE qc_nd_source(i INTEGER)", 0))
			run(referenceExec("INSERT INTO qc_nd_source VALUES(7)", 1))
			run(referenceExec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
			run(referenceExec("CREATE TABLE qc_nd_acl AS SELECT i FROM qc_nd_source WITH NO DATA", 0))
			run(referenceSelect("SELECT * FROM qc_nd_acl", []string{"i"}, []string{"INT4"}, nil))
			var notices []referenceDiagnostic
			_, noticeConn, recorder := noticeReferencePool(t, p, b.pg, &notices)
			referenceRunStep(t, p.ctx, noticeConn, recorder, referenceExec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
			defer noticeConn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			referenceRunStep(t, p.ctx, noticeConn, recorder, referenceExec("CREATE TABLE IF NOT EXISTS qc_nd_acl AS SELECT i FROM qc_nd_source", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_nd_acl" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("skip notices %#v want %#v", notices, want)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT i FROM qc_nd_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			// Genuine PostgreSQL ACL facility: CTAS population does not require
			// INSERT on its freshly created target, unlike a later ordinary INSERT.
			if b.pg {
				run(referenceExec("SET SESSION AUTHORIZATION qc_nd_reader", 0))
				run(referenceExec("BEGIN", 0))
				defer conn.ExecContext(context.Background(), "ROLLBACK")
				run(referenceExec("ALTER DEFAULT PRIVILEGES REVOKE INSERT ON TABLES FROM qc_nd_reader", 0))
				run(referenceExec("CREATE TABLE qc_no_insert AS SELECT 9 AS i", 1))
				run(referenceSelect("SELECT i FROM qc_no_insert", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
				run(referenceImplicitError("INSERT INTO qc_no_insert VALUES(10)", referenceDiagnostic{Code: "42501", Message: "permission denied for table qc_no_insert"}))
				run(referenceExec("ROLLBACK", 0))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			}
		})
	}
}
