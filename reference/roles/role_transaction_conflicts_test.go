package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalConflicts(t *testing.T) {
	for _, kind := range []string{"alter", "alter_noop", "alter_changed_back", "alter_deleted", "create", "grant", "revoke", "options"} {
		for _, commit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/commit=%v/held=%v", kind, commit, held), func(t *testing.T) {
					roleTransactionT2Cases(t, []string{"rf_role", "rf_member", "rf_other", "rf_new", "rf_unrelated"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, apid := roleTransactionT2Conn(t, p, b)
						w, wpid := roleTransactionT2Conn(t, p, b)
						run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						for _, q := range []string{"CREATE ROLE rf_role", "CREATE ROLE rf_member", "CREATE ROLE rf_other", "CREATE TABLE rf_sentinel(i INT)"} {
							run(a, fixture.Exec(q, 0))
						}
						if kind == "revoke" || kind == "options" {
							run(a, fixture.Exec("GRANT rf_role TO rf_member", 0))
						}
						run(a, fixture.Exec("BEGIN", 0))
						holder, waiter := "ALTER ROLE rf_role LOGIN", "ALTER ROLE rf_role CREATEDB"
						switch kind {
						case "alter_noop":
							holder = "ALTER ROLE rf_role NOLOGIN"
						case "alter_deleted":
							holder = "DROP ROLE rf_role"
						case "create":
							holder, waiter = "CREATE ROLE rf_new", "CREATE ROLE rf_new LOGIN"
						case "grant":
							holder, waiter = "GRANT rf_role TO rf_member", "GRANT rf_role TO rf_other"
						case "revoke":
							holder, waiter = "REVOKE rf_role FROM rf_member", "GRANT rf_role TO rf_other"
						case "options":
							holder, waiter = "GRANT rf_role TO rf_member WITH INHERIT FALSE", "GRANT rf_role TO rf_member WITH SET FALSE"
						}
						run(a, fixture.Exec(holder, 0))
						if kind == "alter_changed_back" {
							run(a, fixture.Exec("ALTER ROLE rf_role NOLOGIN", 0))
						}
						run(w, fixture.Exec("BEGIN", 0))
						join, completed := roleTransactionT2Worker(t, p, w, waiter, held)
						t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
						roleTransactionT2Wait(t, p, b, wpid, apid, completed)
						// Disjoint shared-role and database work must not join this queue or be overwritten.
						run(b.DB, fixture.Exec("CREATE ROLE rf_unrelated CREATEDB", 0))
						run(b.DB, fixture.Exec("INSERT INTO rf_sentinel VALUES(7)", 1))
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(a, fixture.Exec(end, 0))
						outcome := join()
						conflict := commit && (kind == "alter" || kind == "alter_noop" || kind == "alter_changed_back" || kind == "alter_deleted" || kind == "create")
						if conflict {
							want := fixture.ControlsReferenceDiagnostic("XX000", "tuple concurrently updated")
							if kind == "alter_deleted" {
								want.Message = "tuple concurrently deleted"
							}
							if kind == "create" {
								want = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_authid_rolname_index"`)
								want.Detail, want.Schema, want.Table, want.Constraint = "Key (rolname)=(rf_new) already exists.", "pg_catalog", "pg_authid", "pg_authid_rolname_index"
							}
							if outcome.Result != nil {
								t.Fatal("conflicting utility returned result")
							}
							fixture.AssertDiagnostic(t, outcome.Err, &want)
							run(w, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
							run(w, fixture.Exec("ROLLBACK", 0))
						} else {
							roleTransactionT2Success(t, outcome)
							run(w, fixture.Exec("COMMIT", 0))
						}
						run(a, roleTransactionExists("rf_unrelated", true))
						run(a, fixture.Select("SELECT i FROM rf_sentinel", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if kind == "create" {
							run(a, fixture.Select("SELECT rolcanlogin FROM pg_roles WHERE rolname='rf_new'", []string{"rolcanlogin"}, []string{"BOOL"}, [][]any{{!commit}}))
						} else if kind == "alter_deleted" {
							run(a, roleTransactionExists("rf_role", !commit))
						} else if kind == "alter" || kind == "alter_noop" || kind == "alter_changed_back" {
							run(a, fixture.Select("SELECT rolcanlogin,rolcreatedb FROM pg_roles WHERE rolname='rf_role'", []string{"rolcanlogin", "rolcreatedb"}, []string{"BOOL", "BOOL"}, [][]any{{commit && kind == "alter", !commit}}))
						} else if kind == "options" {
							run(a, fixture.Select("SELECT pg_has_role('rf_member','rf_role','USAGE') AS inherit,pg_has_role('rf_member','rf_role','SET') AS can_set", []string{"inherit", "can_set"}, []string{"BOOL", "BOOL"}, [][]any{{!commit, false}}))
						} else {
							member := commit && kind == "grant" || !commit && kind == "revoke"
							run(a, fixture.Select("SELECT pg_has_role('rf_member','rf_role','MEMBER') AS member,pg_has_role('rf_other','rf_role','MEMBER') AS other", []string{"member", "other"}, []string{"BOOL", "BOOL"}, [][]any{{member, true}}))
						}
						run(w, fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
					})
				})
			}
		}
	}
	roleTransactionConflictControls(t)
	roleTransactionConflictSavepoint(t)
}

func roleTransactionConflictControls(t *testing.T) {
	t.Helper()
	for _, kind := range []string{"cancel", "lock_timeout", "statement_timeout"} {
		t.Run(kind, func(t *testing.T) {
			roleTransactionT2Cases(t, []string{"rf_control", "rf_actor", "rf_peer"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, apid := roleTransactionT2Conn(t, p, b)
				w, wpid := roleTransactionT2Conn(t, p, b)
				run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE rf_control", "CREATE ROLE rf_actor", "CREATE ROLE rf_peer"} {
					run(a, fixture.Exec(q, 0))
				}
				query := "GRANT rf_control TO rf_peer"
				stmt, err := w.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				run(a, fixture.Exec("BEGIN", 0))
				run(a, fixture.Exec("GRANT rf_control TO rf_actor", 0))
				run(w, fixture.Exec("BEGIN", 0))
				if kind != "cancel" {
					run(w, fixture.Exec("SET "+kind+"='2s'", 0))
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				completed := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(completed)
					r, err := stmt.ExecContext(ctx)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
				roleTransactionT2Wait(t, p, b, wpid, apid, completed)
				if kind == "cancel" {
					if b.PG {
						var signaled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wpid).Scan(&signaled); err != nil || !signaled {
							t.Fatal("cancel signal", signaled, err)
						}
					} else {
						cancel()
					}
				}
				outcome := join()
				if outcome.Result != nil {
					t.Fatal("canceled utility returned result")
				}
				if kind == "cancel" && !b.PG {
					if !errors.Is(outcome.Err, context.Canceled) {
						t.Fatal(outcome.Err)
					}
				} else {
					want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					if kind == "statement_timeout" {
						want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
					}
					if kind == "cancel" {
						want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					}
					fixture.AssertDiagnostic(t, outcome.Err, &want)
				}
				if kind == "cancel" {
					run(w, fixture.Exec("ROLLBACK", 0))
					run(w, fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
				} else {
					// Do not run setup/probes on an armed timeout owner.
					w.Raw(func(any) error { return driver.ErrBadConn })
				}
				run(a, fixture.Exec("ROLLBACK", 0))
				run(b.DB, fixture.Exec(query, 0))
				run(b.DB, fixture.Select("SELECT pg_has_role('rf_actor','rf_control','MEMBER') AS actor,pg_has_role('rf_peer','rf_control','MEMBER') AS peer", []string{"actor", "peer"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
			})
		})
	}
}

func roleTransactionConflictSavepoint(t *testing.T) {
	t.Helper()
	t.Run("savepoint_release", func(t *testing.T) {
		roleTransactionT2Cases(t, []string{"rf_mark", "rf_member", "rf_peer"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, apid := roleTransactionT2Conn(t, p, b)
			w, wpid := roleTransactionT2Conn(t, p, b)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			for _, q := range []string{"CREATE ROLE rf_mark", "CREATE ROLE rf_member", "CREATE ROLE rf_peer", "BEGIN", "SAVEPOINT child", "GRANT rf_mark TO rf_member"} {
				run(a, q)
			}
			join, completed := roleTransactionT2Worker(t, p, w, "GRANT rf_mark TO rf_peer", true)
			t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
			roleTransactionT2Wait(t, p, b, wpid, apid, completed)
			run(a, "ROLLBACK TO child")
			roleTransactionT2Success(t, join())
			run(a, "ROLLBACK TO child")
			run(a, "GRANT rf_mark TO rf_member")
			run(a, "RELEASE child")
			join, completed = roleTransactionT2Worker(t, p, w, "REVOKE rf_mark FROM rf_peer", false)
			t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
			roleTransactionT2Wait(t, p, b, wpid, apid, completed)
			run(a, "COMMIT")
			roleTransactionT2Success(t, join())
			fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT pg_has_role('rf_member','rf_mark','MEMBER') AS member,pg_has_role('rf_peer','rf_mark','MEMBER') AS peer", []string{"member", "peer"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		})
	})
}
