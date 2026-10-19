package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func batchUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatal("full independently specified diagnostic", err, want)
	}
}

func TestDriverBatchDetachedPrincipalAuthority(t *testing.T) {
	for _, finish := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(finish, func(t *testing.T) {
			e := New()
			owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TABLE t(v INT)")
			sessionSQL(t, owner, "INSERT INTO t VALUES(7)")
			table := catalogRef(t, e, e.CatalogRead(), "t")
			original := owner.authorization.state
			m := batchUnitStart(t, owner, context.Background())
			defer m.end(context.Canceled)
			t.Cleanup(func() {
				e.mu.Lock()
				pending := e.prepared["batch_detached"] != nil
				e.mu.Unlock()
				if pending {
					if err := controlsExecute(t, peer, "ROLLBACK PREPARED 'batch_detached'"); err != nil {
						t.Error("prepared cleanup", err)
					}
				}
			})
			for _, q := range []string{"CREATE ROLE detached LOGIN", "GRANT SELECT ON t TO detached", "SET ROLE detached", "BEGIN", "PREPARE TRANSACTION 'batch_detached'"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			if err := m.end(nil); err != nil {
				t.Fatal(err)
			}
			entry := e.prepared["batch_detached"]
			if entry == nil || entry.catalog == nil || entry.catalog.roles == nil || owner.authorization.state.CurrentUser != entry.owner || owner.authorization.state.SessionUser != original.SessionUser || owner.transactions.catalog != nil {
				t.Fatal("prepared role authority was lost or retained by session")
			}
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			auth, err := op.Authorization()
			op.Done()
			if err != nil || auth.RoleRecord != e.roles.published || auth.State.CurrentUser != entry.owner || auth.MissingCurrent != entry.owner {
				t.Fatal("numeric authority did not admit missing effective name", err)
			}
			if _, exists := auth.Roles.Lookup("detached"); exists {
				t.Fatal("detached private role name leaked into new command")
			}
			batchUnitDiagnostic(t, auth.Check(AccessRequirement{Object: table, Privileges: authorization.SelectPrivilege}), "42501", "permission denied for table t")
			unadmitted := auth
			unadmitted.MissingCurrent = authorization.PrincipalRef{}
			if _, err := unadmitted.Effective(table, false); err == nil {
				t.Fatal("unadmitted missing actor acquired numeric authority")
			}
			if err := unadmitted.Check(AccessRequirement{Object: table, Privileges: authorization.SelectPrivilege}); err == nil {
				t.Fatal("unadmitted missing actor bypassed privilege preflight")
			}
			foreign := auth
			foreign.State.CurrentUser = New().roles.domain.Principal(entry.owner.ID())
			if _, err := foreign.Effective(table, false); err == nil {
				t.Fatal("foreign numeric principal bypassed authority")
			}
			missing := fmt.Sprintf("invalid role OID: %d", entry.owner.ID())
			batchUnitDiagnostic(t, controlsExecute(t, owner, "SELECT current_user"), "42704", missing)
			batchUnitDiagnostic(t, controlsExecute(t, owner, "SELECT v FROM t"), "42501", "permission denied for table t")
			transactionRows(t, owner, "SELECT 1", 1)
			controlsRequire(t, peer, finish+" PREPARED 'batch_detached'")
			if e.prepared["batch_detached"] != nil {
				t.Fatal("prepared completion retained registry")
			}
			if finish == "COMMIT" {
				role, exists := e.Roles().Lookup("detached")
				if !exists || role.Ref != entry.owner {
					t.Fatal("commit changed numeric role identity")
				}
				result := sessionSQL(t, owner, "SELECT current_user")
				if got := tuples(result); len(got) != 1 || len(got[0]) != 1 || got[0][0] != "detached" {
					t.Fatal("published name consumer", got)
				}
				if err := result.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, owner, "SELECT v FROM t", 7)
			} else {
				batchUnitDiagnostic(t, controlsExecute(t, owner, "SELECT current_user"), "42704", missing)
				batchUnitDiagnostic(t, controlsExecute(t, owner, "SELECT v FROM t"), "42501", "permission denied for table t")
			}
			controlsRequire(t, owner, "SET ROLE NONE")
			if owner.authorization.state != original {
				t.Fatal("NONE recovered a fabricated identity")
			}
			transactionRows(t, owner, "SELECT v FROM t", 7)
			lockEmpty(t, e)
		})
	}
}
