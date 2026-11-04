package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func originStatusDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, object string) {
	t.Helper()
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + object})
	s.SelectRows, s.ErrorStage = true, "execute"
	originStateRun(t, p, b, e, s)
	originStateRun(t, p, b, e, fixture.Prepared(s))
}

func TestReferencePGReplicationOriginStatusPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"status_actor", "status_repl", "status_group"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "status_privilege")
			id := originStateCreate(t, p, b, b.DB, "status_privilege")
			originStatusAdvance(t, p, b, b.DB, "status_privilege", "0/2")
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			exec := func(e fixture.Executor, q string) { originStateRun(t, p, b, e, fixture.Exec(q, 0)) }
			originStatusCleanup(t, actor, "RESET SESSION AUTHORIZATION")
			originStatusCleanup(t, admin,
				"REVOKE SELECT(local_id,external_id,remote_lsn,local_lsn) ON pg_catalog.pg_replication_origin_status FROM status_actor,status_repl,status_group",
				"REVOKE ALL ON pg_catalog.pg_replication_origin_status FROM status_actor,status_repl,status_group",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_actor,status_repl,status_group",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_replication_origin_oid(text) FROM status_actor")
			exec(admin, "CREATE ROLE status_actor LOGIN")
			exec(admin, "CREATE ROLE status_repl LOGIN REPLICATION")
			exec(admin, "CREATE ROLE status_group")
			exec(admin, "CREATE TABLE status_priv_sentinel(n integer)")
			originStateRun(t, p, b, admin, fixture.Exec("INSERT INTO status_priv_sentinel VALUES(41)", 1))
			originStatusInitial(t, p, b, admin)
			stmt, err := actor.PrepareContext(p.Ctx, originStatusSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: originStatusSelect}
			want := [][]any{{id, "status_privilege", "0/2", "0/0"}}
			for _, role := range []string{"status_actor", "status_repl"} {
				exec(actor, "SET SESSION AUTHORIZATION "+role)
				originStatusDenied(t, p, b, actor, originStatusSelect, "view pg_replication_origin_status")
				originStatusDenied(t, p, b, held, originStatusSelect, "view pg_replication_origin_status")
				exec(actor, "RESET SESSION AUTHORIZATION")
			}
			exec(actor, "SET SESSION AUTHORIZATION status_actor")
			for _, membership := range []string{"pg_read_all_stats", "pg_monitor"} {
				exec(admin, "GRANT "+membership+" TO status_actor")
				originStatusDenied(t, p, b, held, originStatusSelect, "view pg_replication_origin_status")
				exec(admin, "REVOKE "+membership+" FROM status_actor")
			}
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_origin_status TO status_actor")
			originStatusDenied(t, p, b, actor, originStatusSelect, "function pg_show_replication_origin_status")
			originStatusDenied(t, p, b, held, originStatusSelect, "function pg_show_replication_origin_status")
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_replication_origin_oid(text) TO status_actor")
			originStatusDenied(t, p, b, held, originStatusSelect, "function pg_show_replication_origin_status")
			originStatusDenied(t, p, b, actor, originStatusSelect+" LIMIT 0", "function pg_show_replication_origin_status")
			originStatusRows(t, p, b, actor, originStatusSelect+" WHERE FALSE", nil)
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() TO status_actor")
			originStatusRows(t, p, b, actor, originStatusSelect, want)
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_actor")
			originStatusDenied(t, p, b, held, originStatusSelect, "function pg_show_replication_origin_status")
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() TO status_actor")
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			exec(admin, "REVOKE SELECT ON pg_catalog.pg_replication_origin_status FROM status_actor")
			originStatusDenied(t, p, b, actor, originStatusSelect, "view pg_replication_origin_status")
			originStatusDenied(t, p, b, held, originStatusSelect, "view pg_replication_origin_status")
			exec(admin, "GRANT SELECT(external_id) ON pg_catalog.pg_replication_origin_status TO status_actor")
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT external_id FROM pg_replication_origin_status WHERE external_id='status_privilege' ORDER BY external_id", []string{"external_id"}, []string{"TEXT"}, [][]any{{"status_privilege"}})
				s.Prepared = prepared
				originStateRun(t, p, b, actor, s)
			}
			originStatusDenied(t, p, b, actor, "SELECT external_id FROM pg_replication_origin_status WHERE remote_lsn='0/2'", "view pg_replication_origin_status")
			originStatusDenied(t, p, b, actor, "SELECT external_id FROM pg_replication_origin_status ORDER BY local_id", "view pg_replication_origin_status")
			originStatusDenied(t, p, b, actor, "SELECT * FROM pg_replication_origin_status", "view pg_replication_origin_status")
			exec(admin, "REVOKE SELECT(external_id) ON pg_catalog.pg_replication_origin_status FROM status_actor")
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_origin_status TO status_group")
			exec(admin, "GRANT status_group TO status_actor")
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			exec(admin, "REVOKE status_group FROM status_actor")
			originStatusDenied(t, p, b, held, originStatusSelect, "view pg_replication_origin_status")
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_origin_status TO status_actor")
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			exec(actor, "RESET SESSION AUTHORIZATION")
			originStatusInitial(t, p, b, admin)
			originStateRun(t, p, b, admin, originStateScalar("SELECT n AS v FROM status_priv_sentinel", "INT4", int64(41)))
			originStatusRows(t, p, b, admin, originStatusSelect, want)
		})
	}
}

func TestReferencePGReplicationOriginStatusDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			originStateRun(t, p, b, c, fixture.Exec("CREATE TABLE status_diag_sentinel(n integer)", 0))
			originStateRun(t, p, b, c, fixture.Exec("INSERT INTO status_diag_sentinel VALUES(41)", 1))
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT missing FROM pg_replication_origin_status", "42703", `column "missing" does not exist`, "8"},
				{"SELECT missing FROM pg_catalog.pg_replication_origin_status WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
				{"SELECT s.missing FROM pg_catalog.pg_replication_origin_status s", "42703", "column s.missing does not exist", "8"},
				{"SELECT * FROM pg_catalog.pg_replication_origin_status_missing", "42P01", `relation "pg_catalog.pg_replication_origin_status_missing" does not exist`, "15"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows = true
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					originStateRun(t, p, b, c, s)
				}
			}
			for _, tc := range []struct{ query, code, message, detail, hint string }{
				{"INSERT INTO pg_replication_origin_status VALUES(1,'x','0/1','0/0')", "55000", `cannot insert into view "pg_replication_origin_status"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."},
				{"UPDATE pg_replication_origin_status SET remote_lsn='0/2'", "55000", `cannot update view "pg_replication_origin_status"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."},
				{"DELETE FROM pg_replication_origin_status", "55000", `cannot delete from view "pg_replication_origin_status"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable deleting from the view, provide an INSTEAD OF DELETE trigger or an unconditional ON DELETE DO INSTEAD rule."},
				{"TRUNCATE pg_replication_origin_status", "42809", `"pg_replication_origin_status" is not a table`, "", ""},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.code == "55000" {
						s.ErrorStage = "prepare"
					}
					originStateRun(t, p, b, c, s)
				}
			}
			copyPool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
			copyConn := fixture.SequenceCoreConn(t, p.Ctx, copyPool)
			result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_replication_origin_status FROM STDIN", strings.NewReader("1\tx\t0/1\t0/0\n"))
			wantCopy := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_replication_origin_status"`)
			wantCopy.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			fixture.AssertDiagnostic(t, err, &wantCopy)
			if result.Count != 0 || result.CommandTag != "" {
				t.Fatal(result)
			}
			result, err = fixture.CopyTo(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_replication_origin_status TO STDOUT", io.Discard)
			wantCopy = fixture.ControlsReferenceDiagnostic("42809", `cannot copy from view "pg_replication_origin_status"`)
			wantCopy.Hint = "Try the COPY (SELECT ...) TO variant."
			fixture.AssertDiagnostic(t, err, &wantCopy)
			if result.Count != 0 || result.CommandTag != "" {
				t.Fatal(result)
			}
			for _, route := range []string{"direct", "held"} {
				t.Run("pre_canceled_"+route, func(t *testing.T) {
					// Retire the borrower after each interrupted probe: pq may
					// have closed its physical transport on cancellation.
					probe := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					var rows *sql.Rows
					var err error
					if route == "held" {
						stmt, prepareErr := probe.PrepareContext(p.Ctx, originStatusSelect)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = probe.QueryContext(ctx, originStatusSelect)
					}
					originStatusCanceledRead(t, rows, err, b.PG)
				})
			}
			originStatusRows(t, p, b, c, originStatusSelect, nil)
			originStateRun(t, p, b, c, originStateScalar("SELECT n AS v FROM status_diag_sentinel", "INT4", int64(41)))
		})
	}
}

// pq may finish a pre-canceled command before observing cancellation. Native
// admission rejects it synchronously. Successful pq results still obey the
// complete empty-view shape and must be drained before reusing the connection.
func originStatusCanceledRead(t *testing.T, rows *sql.Rows, err error, pg bool) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if !pg || err != nil {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return
	}
	if rows == nil {
		t.Fatal("successful pq query returned no rows handle")
	}
	originStatusMetadata(t, rows, true)
	if rows.Next() {
		t.Fatal("pre-canceled read fabricated status")
	}
	if err := rows.Err(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
