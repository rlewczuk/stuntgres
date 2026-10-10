package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceMaterializedViewAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"mv_owner", "mv_reader"})
	schemaReferenceCleanup(t, "mv_private")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(referenceExec("CREATE USER mv_owner", 0))
			run(referenceExec("CREATE USER mv_reader", 0))
			run(referenceExec("CREATE SCHEMA mv_private AUTHORIZATION mv_owner", 0))
			if b.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION mv_owner", 0))
			materializedViewSeed(run)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_owned AS SELECT i,s FROM mv_base", 3))
			run(referenceExec("SET SESSION AUTHORIZATION mv_reader", 0))
			s := referenceImplicitError("SELECT * FROM mv_owned", referenceDiagnostic{Code: "42501", Message: "permission denied for materialized view mv_owned"})
			s.selectRows = true
			run(s)
			run(referenceImplicitError("DROP MATERIALIZED VIEW mv_owned", referenceDiagnostic{Code: "42501", Message: "must be owner of materialized view mv_owned"}))
			run(referenceImplicitError("CREATE MATERIALIZED VIEW mv_denied AS SELECT i,s FROM mv_base", referenceDiagnostic{Code: "42501", Message: "permission denied for table mv_base"}))
			queryTableCreateAbsent(t, run, b.pg, "mv_denied")
			run(referenceExec("CREATE MATERIALIZED VIEW mv_reader_no AS SELECT i,s FROM mv_base WITH NO DATA", 0))
			run(materializedViewUnpopulated("mv_reader_no"))
			run(referenceImplicitError("CREATE MATERIALIZED VIEW mv_private.mv_denied AS SELECT 1 AS i", referenceDiagnostic{Code: "42501", Message: "permission denied for schema mv_private"}))
			for _, q := range []string{"INSERT INTO mv_owned VALUES(9,'z')", "UPDATE mv_owned SET i=9", "DELETE FROM mv_owned"} {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for materialized view mv_owned"}))
			}
			run(referenceExec("CREATE MATERIALIZED VIEW mv_reader_owned AS SELECT 9 AS i", 1))
			run(referenceImplicitError("DROP MATERIALIZED VIEW mv_reader_owned,mv_owned", referenceDiagnostic{Code: "42501", Message: "must be owner of materialized view mv_owned"}))
			run(referenceSelect("SELECT i FROM mv_reader_owned", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			// GRANT is PostgreSQL's existing facility, not scope for native SQL GRANT.
			// Native package ACL witnesses will cover equivalent private authority.
			if b.pg {
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceExec("GRANT SELECT ON mv_owned TO mv_reader", 0))
				run(referenceExec("SET SESSION AUTHORIZATION mv_reader", 0))
				run(materializedViewBag("mv_owned"))
				run(referencePrepared(materializedViewBag("mv_owned")))
				s = referenceImplicitError("SELECT * FROM mv_base", referenceDiagnostic{Code: "42501", Message: "permission denied for table mv_base"})
				s.selectRows = true
				run(s)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION mv_owner", 0))
			for _, q := range []string{"INSERT INTO mv_owned VALUES(9,'z')", "UPDATE mv_owned SET i=9", "DELETE FROM mv_owned"} {
				t.Run("read_only/"+q, func(t *testing.T) {
					tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					command := strings.Fields(q)[0]
					referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError(q, referenceDiagnostic{Code: "25006", Message: "cannot execute " + command + " in a read-only transaction"}))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				})
			}
			run(materializedViewBag("mv_owned"))
			run(materializedViewBag("mv_base"))
			// Schema-owner authority is distinct from MV ownership.
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_private.mv_other_owner AS SELECT 1 AS i", 1))
			run(referenceExec("SET SESSION AUTHORIZATION mv_owner", 0))
			run(referenceExec("DROP MATERIALIZED VIEW mv_private.mv_other_owner", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceMaterializedViewRestrictedSearchPath(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			_, run := queryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(referenceExec("SET search_path=public", 0))
			run(referenceExec("CREATE MATERIALIZED VIEW public.mv_path AS SELECT CURRENT_SCHEMA AS ns,i FROM mv_base", 3))
			s := referenceSelect("SELECT ns,i FROM mv_path", []string{"ns", "i"}, []string{"NAME", "INT4"}, [][]any{{"pg_catalog", int64(1)}, {"pg_catalog", int64(1)}, {"pg_catalog", int64(2)}})
			s.bag = true
			run(s)
			run(referenceSelect("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
			run(referenceSelect("SELECT CURRENT_SCHEMA AS ns", []string{"ns"}, []string{"NAME"}, [][]any{{"public"}}))
		})
	}
}
