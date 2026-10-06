//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestReferenceDeleteCorePrivileges specifies release-oracle effects independently.
// GRANT/REVOKE are fixture-only PostgreSQL SQL, not native supported syntax.
// TestDeleteCoreAuthorization supplies the native internal SetObjectACL counterpart.
func TestReferenceDeleteCorePrivileges(t *testing.T) {
	const role = "delete_privileges_actor"
	original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(21)}, {int64(3), int64(30)}}
	survivors := [][]any{{int64(1), int64(10)}, {int64(3), int64(30)}}
	wantDenied := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42501", Message: "permission denied for table t",
	}
	setup := func(t *testing.T, columnSelect bool) (*referencePair, *sql.Conn, *sql.Conn) {
		t.Helper()
		// Register role cleanup before pair cleanup drops its schema and grants.
		roleReferenceCleanup(t, []string{role})
		p := newReferencePair(t)
		admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
		for _, query := range []string{
			"CREATE USER " + role,
			"CREATE TABLE t(id INT,v INT)",
			"GRANT USAGE ON SCHEMA public TO " + role,
			"GRANT DELETE ON TABLE t TO " + role,
		} {
			referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(query, 0))
		}
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("INSERT INTO t VALUES(1,10),(2,20),(2,21),(3,30)", 4))
		if columnSelect {
			referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("GRANT SELECT(id) ON TABLE t TO "+role, 0))
		}
		referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+role, 0))
		t.Cleanup(func() {
			referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
		})
		return p, admin, actor
	}
	rows := func(t *testing.T, p *referencePair, admin *sql.Conn, want [][]any) {
		t.Helper()
		// Only the administrator observes both columns; the actor never receives
		// SELECT merely so that the fixture can inspect survivors.
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceSelect(
			"SELECT id,v FROM t ORDER BY v", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
	}
	denied := func(query string, prepared bool) referenceStep {
		s := referenceErrorDiagnostic(query, wantDenied)
		s.prepared, s.errorStage = prepared, "execute"
		return s
	}

	t.Run("delete_only", func(t *testing.T) {
		for _, mode := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(mode.name, func(t *testing.T) {
				for _, tc := range []struct {
					name, query string
					count       int64
					remaining   [][]any
					denied      bool
				}{
					{"no_where", "DELETE FROM t", 4, nil, false},
					{"constant_true", "DELETE FROM t WHERE TRUE", 4, nil, false},
					{"constant_false", "DELETE FROM t WHERE FALSE", 0, original, false},
					{"predicate_denied", "DELETE FROM t WHERE id=2", 0, original, true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						p, admin, actor := setup(t, false)
						rows(t, p, admin, original)
						s := referenceExec(tc.query, tc.count)
						s.prepared = mode.prepared
						if tc.denied {
							s = denied(tc.query, mode.prepared)
						}
						referenceRunStep(t, p.ctx, actor, p.postgresRecord, s)
						rows(t, p, admin, tc.remaining)
					})
				}
			})
		}
	})

	t.Run("column_select", func(t *testing.T) {
		for _, mode := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(mode.name, func(t *testing.T) {
				p, admin, actor := setup(t, true)
				rows(t, p, admin, original)
				s := referenceExec("DELETE FROM t AS d WHERE d.id=2", 2)
				s.prepared = mode.prepared
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, s)
				rows(t, p, admin, survivors)
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, denied("DELETE FROM t WHERE v=10", mode.prepared))
				rows(t, p, admin, survivors)
			})
		}
	})

	t.Run("prepared_revoke", func(t *testing.T) {
		for _, tc := range []struct {
			name, revoke string
		}{
			{"delete", "REVOKE DELETE ON TABLE t FROM " + role},
			{"column_select", "REVOKE SELECT(id) ON TABLE t FROM " + role},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p, admin, actor := setup(t, true)
				rows(t, p, admin, original)
				// One physical actor connection and one actual statement handle are
				// retained through successful execution, admin revoke, and denial.
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, p.postgresRecord)
				stmt, err := actor.PrepareContext(ctx, "DELETE FROM t AS d WHERE d.id=$1")
				if err != nil {
					t.Fatalf("authorized Prepare: %v", err)
				}
				t.Cleanup(func() {
					if err := stmt.Close(); err != nil {
						t.Errorf("held statement close: %v", err)
					}
				})
				result, err := stmt.ExecContext(ctx, 2)
				if err != nil {
					t.Fatalf("authorized held Execute: %v", err)
				}
				referenceLockAffected(t, result, 2)
				rows(t, p, admin, survivors)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(tc.revoke, 0))
				result, err = stmt.ExecContext(ctx, 1)
				if result != nil {
					t.Fatal("revoked held Execute exposed a result")
				}
				// The error can only be at execution: no second Prepare occurs.
				referenceCheckDiagnostic(t, err, denied("DELETE FROM t AS d WHERE d.id=$1", false), "execute")
				rows(t, p, admin, survivors)
			})
		}
	})
}
