package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatsXactBlocksPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xact_blocks_reader", "xact_blocks_group"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE xact_blocks_reader", "CREATE ROLE xact_blocks_group", "GRANT USAGE ON SCHEMA public TO xact_blocks_reader"} {
			statsExec(t, p, b, c, query, 0)
		}
		statsCreate(t, p, b, c, "xact_acl")
		statsExec(t, p, b, c, "INSERT INTO xact_acl VALUES(1),(2)", 2)
		statsExec(t, p, b, c, "CREATE TABLE xact_acl_nulls(target oid)", 0)
		statsExec(t, p, b, c, "INSERT INTO xact_acl_nulls VALUES(NULL)", 1)
		statsExec(t, p, b, c, "GRANT SELECT ON xact_acl_nulls TO xact_blocks_reader", 0)
		oid := xactBlockOID(t, p, c, "xact_acl")
		peer := statActivityConn(t, p, b)
		statsExec(t, p, b, peer, "BEGIN", 0)
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		xactBlockBegin(t, p, b, c)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_xact_blocks_fetched(oid) TO PUBLIC", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_xact_blocks_hit(oid) TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Errorf("ACL cleanup %s: %v", query, err)
				}
			}
		}()
		// A peer's real read does not supply this physical backend's pending data.
		beforePeer := xactBlockRead(t, p, c, nil, oid)
		xactBlockRows(t, p, b, peer, "SELECT id FROM xact_acl ORDER BY id", 1, 2)
		xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), beforePeer)
		const targetSQL = "SELECT id FROM xact_acl"
		targetStmt, err := c.PrepareContext(p.Ctx, targetSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer targetStmt.Close()
		targetHeld := fixture.HeldStatement{Executor: c, Statement: targetStmt, SQL: targetSQL}
		statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_has_role(current_user,'pg_monitor','USAGE') AS monitor,pg_has_role(current_user,'pg_read_all_stats','USAGE') AS stats,has_table_privilege(current_user,'xact_acl','SELECT') AS can_select", []string{"monitor", "stats", "can_select"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}))
		// Numeric getters have no relation-open/SELECT privilege admission.
		for _, name := range xactBlockNames {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT has_function_privilege(current_user,'pg_catalog."+name+"(oid)','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
		}
		statsExec(t, p, b, c, "RESET ROLE", 0)
		base := xactBlockRead(t, p, c, nil, oid)
		xactBlockRows(t, p, b, c, "SELECT id FROM xact_acl ORDER BY id", 1, 2)
		own := xactBlockRead(t, p, c, nil, oid)
		xactBlockAdvanced(t, b, base, own)
		statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
		xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), own)
		xactBlockWant(t, xactBlockRead(t, p, c, nil, 0), xactBlockPair{})
		for _, prepared := range []bool{false, true} {
			statsExec(t, p, b, c, "SAVEPOINT denied_read", 0)
			denied := fixture.ErrorDiagnostic(targetSQL, activityDiagnostic("42501", "permission denied for table xact_acl", 0))
			denied.SelectRows, denied.ErrorStage = true, "execute"
			if prepared {
				fixture.RunStep(t, p.Ctx, targetHeld, b.Recorder, denied)
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			}
			statsExec(t, p, b, c, "ROLLBACK TO denied_read", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), own)
		}
		statsExec(t, p, b, c, "RESET ROLE", 0)
		for index, name := range xactBlockNames {
			identity := "pg_catalog." + name + "(oid)"
			query := "SELECT pg_catalog." + name + "($1::oid)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			// A stored nullable OID remains a runtime Var: unlike a literal or
			// custom-bound NULL, it cannot erase the selected invocation.
			runtimeSQL := "SELECT pg_catalog." + name + "(target) FROM xact_acl_nulls"
			runtimeStmt, err := c.PrepareContext(p.Ctx, runtimeSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer runtimeStmt.Close()
			runtimeHeld := fixture.HeldStatement{Executor: c, Statement: runtimeStmt, SQL: runtimeSQL}
			value := own.fetched
			if index == 1 {
				value = own.hit
			}
			allow := func() {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{name}, []string{"INT8"}, [][]any{{value}}, oid))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{name}, []string{"INT8"}, [][]any{{value}}, oid))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{name}, []string{"INT8"}, [][]any{{nil}}, nil))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(runtimeSQL, []string{name}, []string{"INT8"}, [][]any{{nil}}))
				fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, fixture.Select(runtimeSQL, []string{name}, []string{"INT8"}, [][]any{{nil}}))
			}
			deny := func() {
				for _, prepared := range []bool{false, true} {
					statsExec(t, p, b, c, "SAVEPOINT denied_getter", 0)
					step := fixture.ErrorDiagnostic(query, activityDiagnostic("42501", "permission denied for function "+name, 0), oid)
					step.SelectRows, step.ErrorStage = true, "execute"
					if prepared {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}
					statsExec(t, p, b, c, "ROLLBACK TO denied_getter", 0)
					// Fresh parameter execution is custom-bound, so NULL can
					// erase this strict call before executor ACL admission.
					nullStep := fixture.Select(query, []string{name}, []string{"INT8"}, [][]any{{nil}}, nil)
					nullStep.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, nullStep)
					for _, argument := range []string{"NULL", "NULL::oid"} {
						literal := fixture.Select("SELECT pg_catalog."+name+"("+argument+")", []string{name}, []string{"INT8"}, [][]any{{nil}})
						literal.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, literal)
					}
					statsExec(t, p, b, c, "SAVEPOINT denied_runtime_null", 0)
					runtimeDenied := fixture.ErrorDiagnostic(runtimeSQL, activityDiagnostic("42501", "permission denied for function "+name, 0))
					runtimeDenied.SelectRows, runtimeDenied.ErrorStage = true, "execute"
					if prepared {
						fixture.RunStep(t, p.Ctx, runtimeHeld, b.Recorder, runtimeDenied)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, runtimeDenied)
					}
					statsExec(t, p, b, c, "ROLLBACK TO denied_runtime_null", 0)
				}
			}
			statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
			allow()
			statsExec(t, p, b, c, "RESET ROLE", 0)
			statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM PUBLIC", 0)
			statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
			deny()
			statsExec(t, p, b, c, "RESET ROLE", 0)
			for _, grantee := range []string{"xact_blocks_reader", "xact_blocks_group"} {
				statsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO "+grantee, 0)
				if grantee == "xact_blocks_group" {
					statsExec(t, p, b, c, "GRANT xact_blocks_group TO xact_blocks_reader", 0)
				}
				statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
				allow()
				statsExec(t, p, b, c, "RESET ROLE", 0)
				if grantee == "xact_blocks_group" {
					statsExec(t, p, b, c, "REVOKE xact_blocks_group FROM xact_blocks_reader", 0)
				} else {
					statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+grantee, 0)
				}
				statsExec(t, p, b, c, "SET ROLE xact_blocks_reader", 0)
				deny()
				statsExec(t, p, b, c, "RESET ROLE", 0)
				if grantee == "xact_blocks_group" {
					statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+grantee, 0)
				}
			}
			statsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION "+identity+" TO PUBLIC", 0)
		}
		statsExec(t, p, b, c, "ROLLBACK", 0)
	})
}
