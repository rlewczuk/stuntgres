package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesLocks(t *testing.T) {
	for _, sequence := range []bool{false, true} {
		for _, revoke := range []bool{false, true} {
			for _, action := range []string{"release", "cancel", "timeout", "rename", "replace", "savepoint"} {
				t.Run(fmt.Sprintf("sequence=%v/revoke=%v/%s", sequence, revoke, action), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tx_lock_reader"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
							exec := func(c *sql.Conn, q string) { fixture.LockExec(t, p.Ctx, c, q, 0) }
							exec(holder, "CREATE ROLE tx_lock_reader")
							kind, priv := "TABLE", "SELECT"
							if sequence {
								kind, priv = "SEQUENCE", "USAGE"
								exec(holder, "CREATE SEQUENCE tx_lock START 100")
							} else {
								exec(holder, "CREATE TABLE tx_lock(a INT)")
							}
							exec(holder, "GRANT USAGE ON SCHEMA public TO tx_lock_reader")
							if !sequence {
								fixture.LockExec(t, p.Ctx, holder, "INSERT INTO tx_lock VALUES(7)", 1)
							}
							var originalOID int64
							if err := holder.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_lock'").Scan(&originalOID); err != nil || originalOID <= 0 {
								t.Fatal("original identity", originalOID, err)
							}
							q := "GRANT " + priv + " ON " + kind + " tx_lock TO tx_lock_reader"
							if revoke {
								exec(holder, q)
								q = "REVOKE " + priv + " ON " + kind + " tx_lock FROM tx_lock_reader"
							}
							utility, err := waiter.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer utility.Close()
							var hp, wp int
							var oid int64
							if b.PG {
								hp = fixture.LockPID(t, p.Ctx, holder)
								wp = fixture.LockPID(t, p.Ctx, waiter)
								oid = fixture.LockOID(t, p.Ctx, "tx_lock")
							}
							exec(holder, "BEGIN")
							defer holder.ExecContext(p.Ctx, "ROLLBACK")
							if action == "savepoint" {
								exec(holder, "SAVEPOINT lock_mark")
							}
							if sequence {
								exec(holder, "ALTER SEQUENCE tx_lock RENAME TO tx_lock_holding")
								exec(holder, "ALTER SEQUENCE tx_lock_holding RENAME TO tx_lock")
							} else {
								exec(holder, "LOCK TABLE tx_lock IN ACCESS EXCLUSIVE MODE")
							}
							exec(waiter, "BEGIN")
							defer waiter.ExecContext(p.Ctx, "ROLLBACK")
							if action == "timeout" {
								exec(waiter, "SET LOCAL lock_timeout='500ms'")
							}
							if action == "cancel" {
								fixture.RelationLockActiveCancel(t, p, b, waiter, q, func(ctx context.Context) fixture.RelationOutcome {
									r, e := utility.ExecContext(ctx)
									return fixture.RelationOutcome{Result: r, Err: e}
								})
								exec(waiter, "ROLLBACK")
								exec(holder, "ROLLBACK")
								probe := transactionalPrivilegeRelationACLProbe("tx_lock", "tx_lock_reader", "SELECT")
								if sequence {
									probe = transactionalPrivilegeRelationACLProbe("tx_lock", "tx_lock_reader", "USAGE")
								}
								fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select(probe, []string{"v"}, []string{"BOOL"}, [][]any{{revoke}}))
								fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
								return
							}
							finished := make(chan struct{})
							join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								r, e := utility.ExecContext(ctx)
								close(finished)
								return fixture.RelationOutcome{Result: r, Err: e}
							})
							if action != "timeout" {
								fixture.RelationLockPending(t, p, b, wp, hp, oid, "AccessShareLock", finished)
							}
							switch action {
							case "rename":
								exec(holder, "ALTER "+kind+" tx_lock RENAME TO tx_lock_old")
								exec(holder, "COMMIT")
							case "replace":
								exec(holder, "DROP "+kind+" tx_lock")
								if sequence {
									exec(holder, "CREATE SEQUENCE tx_lock START 200")
								} else {
									exec(holder, "CREATE TABLE tx_lock(a INT)")
									fixture.LockExec(t, p.Ctx, holder, "INSERT INTO tx_lock VALUES(9)", 1)
								}
								// A queued revoke must remove a genuine grant on the replacement, not
								// appear correct merely because its new target had no recipient ACL.
								if revoke {
									exec(holder, "GRANT "+priv+" ON "+kind+" tx_lock TO tx_lock_reader")
								}
								exec(holder, "COMMIT")
							case "savepoint":
								exec(holder, "ROLLBACK TO lock_mark")
							case "release":
								exec(holder, "COMMIT")
							}
							outcome := join()
							switch action {
							case "rename":
								want := fixture.ControlsReferenceDiagnostic("42P01", `relation "tx_lock" does not exist`)
								fixture.AssertDiagnostic(t, outcome.Err, &want)
							case "timeout":
								want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
								fixture.AssertDiagnostic(t, outcome.Err, &want)
							default:
								if outcome.Err != nil {
									t.Fatal("queued Execute", outcome.Err)
								}
								fixture.LockAffected(t, outcome.Result, 0)
							}
							if outcome.Err == nil {
								exec(waiter, "COMMIT")
							} else {
								exec(waiter, "ROLLBACK")
							}
							exec(holder, "ROLLBACK")
							name := "tx_lock"
							want := !revoke
							if action == "rename" {
								name = "tx_lock_old"
								want = revoke
							}
							if action == "timeout" {
								want = revoke
							}
							probe := transactionalPrivilegeRelationACLProbe(name, "tx_lock_reader", "SELECT")
							if sequence {
								probe = transactionalPrivilegeRelationACLProbe(name, "tx_lock_reader", "USAGE")
							}
							fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select(probe, []string{"v"}, []string{"BOOL"}, [][]any{{want}}))
							entries := [][]any(nil)
							if want {
								entries = [][]any{{priv, false, true}}
							}
							acl := "SELECT a.privilege_type,a.is_grantable,a.grantor=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owner_origin FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.relnamespace='public'::regnamespace AND c.relname='" + name + "' AND r.rolname='tx_lock_reader'"
							fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select(acl, []string{"privilege_type", "is_grantable", "owner_origin"}, []string{"TEXT", "BOOL", "BOOL"}, entries))
							var currentOID int64
							if err := waiter.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", name).Scan(&currentOID); err != nil || currentOID <= 0 {
								t.Fatal("current identity", currentOID, err)
							}
							if (currentOID != originalOID) != (action == "replace") {
								t.Fatal("relation admission identity", originalOID, currentOID, action)
							}
							exec(waiter, "SET ROLE tx_lock_reader")
							query := "SELECT a FROM " + name
							if sequence {
								query = "SELECT nextval('" + name + "') AS v"
							}
							if want {
								value := int64(7)
								if action == "replace" {
									value = 9
								}
								column, typ := "a", "INT4"
								if sequence {
									column, typ = "v", "INT8"
									value = 100
									if action == "replace" {
										value = 200
									}
								}
								fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select(query, []string{column}, []string{typ}, [][]any{{value}}))
							} else {
								noun := "table"
								if sequence {
									noun = "sequence"
								}
								denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + noun + " " + name})
								denied.SelectRows = true
								fixture.RunStep(t, p.Ctx, waiter, b.Recorder, denied)
							}
							exec(waiter, "RESET ROLE")
							fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
						})
					}
				})
			}
		}
	}
}

func TestReferenceTransactionalPrivilegesConflicts(t *testing.T) {
	transactionalPrivilegesExistingDefaultWriter(t)
	for _, defaults := range []bool{false, true} {
		for _, sameTarget := range []bool{false, true} {
			t.Run(fmt.Sprintf("defaults=%v/same=%v", defaults, sameTarget), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"tx_writer_a", "tx_writer_b"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						exec := func(x *sql.Conn, q string) { fixture.LockExec(t, p.Ctx, x, q, 0) }
						exec(a, "CREATE ROLE tx_writer_a")
						exec(a, "CREATE ROLE tx_writer_b")
						exec(a, "GRANT USAGE ON SCHEMA public TO tx_writer_a,tx_writer_b")
						exec(a, "CREATE TABLE tx_writer_one(a INT)")
						exec(a, "CREATE TABLE tx_writer_two(a INT)")
						q1 := "GRANT SELECT ON tx_writer_one TO tx_writer_a"
						q2 := "GRANT SELECT ON tx_writer_two TO tx_writer_b"
						if sameTarget {
							q2 = "GRANT SELECT ON tx_writer_one TO tx_writer_b"
						}
						if defaults {
							q1 = "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO tx_writer_a"
							q2 = "ALTER DEFAULT PRIVILEGES GRANT SELECT ON SEQUENCES TO tx_writer_b"
							if sameTarget {
								q2 = "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO tx_writer_b"
							}
						}
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							a.ExecContext(ctx, "ROLLBACK")
							c.ExecContext(ctx, "ROLLBACK")
							a.ExecContext(ctx, "RESET ROLE")
							c.ExecContext(ctx, "RESET ROLE")
							if !defaults {
								return
							}
							cleanup, err := b.DB.Conn(ctx)
							if err != nil {
								t.Error("default writer cleanup connection", err)
								return
							}
							defer cleanup.Close()
							for _, query := range []string{
								"ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM tx_writer_a",
								"ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM tx_writer_b",
								"ALTER DEFAULT PRIVILEGES REVOKE SELECT ON SEQUENCES FROM tx_writer_b",
							} {
								if _, err := cleanup.ExecContext(ctx, query); err != nil {
									t.Error("default writer cleanup", err)
								}
							}
						}()
						var creatorOID int64
						if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=current_user").Scan(&creatorOID); err != nil {
							t.Fatal(err)
						}
						var ap, cp int
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							cp = fixture.LockPID(t, p.Ctx, c)
						}
						exec(a, "BEGIN")
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						exec(c, "BEGIN")
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						exec(a, q1)
						third, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						fixture.LockExec(t, p.Ctx, third, "INSERT INTO tx_writer_two VALUES(9)", 1)
						third.Close()
						done := make(chan struct{})
						join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, e := c.ExecContext(ctx, q2)
							close(done)
							return fixture.RelationOutcome{Result: r, Err: e}
						})
						var out fixture.RelationOutcome
						if sameTarget {
							fixture.RelationLockPending(t, p, b, cp, ap, 0, "", done)
							exec(a, "COMMIT")
							out = join()
						} else {
							// Independent commands must finish while the first writer is
							// still private, not merely win a race against its COMMIT.
							out = join()
							if out.Err != nil {
								t.Fatal("independent writer before first commit", out.Err)
							}
							exec(a, "COMMIT")
						}
						if sameTarget {
							want := fixture.ControlsReferenceDiagnostic("XX000", "tuple concurrently updated")
							if defaults {
								want = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_default_acl_role_nsp_obj_index"`)
								want.Detail = fmt.Sprintf("Key (defaclrole, defaclnamespace, defaclobjtype)=(%d, 0, r) already exists.", creatorOID)
								want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_default_acl", "pg_default_acl_role_nsp_obj_index"
							}
							fixture.AssertDiagnostic(t, out.Err, &want)
							exec(c, "ROLLBACK")
						} else {
							if out.Err != nil {
								t.Fatal("independent writer", out.Err)
							}
							fixture.LockAffected(t, out.Result, 0)
							exec(c, "COMMIT")
						}
						target := "tx_writer_one"
						if defaults {
							exec(a, "CREATE TABLE tx_writer_after(a INT)")
							exec(a, "CREATE SEQUENCE tx_writer_sequence")
							target = "tx_writer_after"
						}
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(transactionalPrivilegeRelationACLProbe(target, "tx_writer_a", "SELECT"), []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
						probe := transactionalPrivilegeRelationACLProbe("tx_writer_two", "tx_writer_b", "SELECT")
						if sameTarget {
							probe = transactionalPrivilegeRelationACLProbe(target, "tx_writer_b", "SELECT")
						} else if defaults {
							probe = transactionalPrivilegeRelationACLProbe("tx_writer_sequence", "tx_writer_b", "SELECT")
						}
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(probe, []string{"v"}, []string{"BOOL"}, [][]any{{!sameTarget}}))
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT a FROM tx_writer_two", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
						exec(a, "SET ROLE tx_writer_a")
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT a FROM "+target, []string{"a"}, []string{"INT4"}, nil))
						exec(a, "RESET ROLE")
						exec(a, "SET ROLE tx_writer_b")
						if sameTarget {
							fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ImplicitError("SELECT a FROM "+target, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + target}))
						} else if defaults {
							fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value AS v FROM tx_writer_sequence", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
						} else {
							fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT a FROM tx_writer_two", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
						}
						exec(a, "RESET ROLE")
					})
				}
			})
		}
	}
}

func transactionalPrivilegesExistingDefaultWriter(t *testing.T) {
	t.Run("existing-default-row", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				c.ExecContext(ctx, "ROLLBACK")
				peer.ExecContext(ctx, "ROLLBACK")
				c.ExecContext(ctx, "RESET ROLE")
				if _, err := c.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES REVOKE ALL ON TABLES FROM og_object_reader,og_object_other"); err != nil {
					t.Error("default writer cleanup", err)
				}
			}()
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT INSERT ON TABLES TO og_object_reader", 0))
			var cp, pp int
			if b.PG {
				cp = fixture.LockPID(t, p.Ctx, c)
				pp = fixture.LockPID(t, p.Ctx, peer)
			}
			run(fixture.Exec("BEGIN", 0))
			fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO og_object_reader", 0))
			done := make(chan struct{})
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				r, e := peer.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO og_object_other")
				close(done)
				return fixture.RelationOutcome{Result: r, Err: e}
			})
			fixture.RelationLockPending(t, p, b, pp, cp, 0, "", done)
			run(fixture.Exec("COMMIT", 0))
			out := join()
			want := fixture.ControlsReferenceDiagnostic("XX000", "tuple concurrently updated")
			fixture.AssertDiagnostic(t, out.Err, &want)
			if out.Result != nil {
				t.Fatal("failed default writer returned a result")
			}
			fixture.LockExec(t, p.Ctx, peer, "ROLLBACK", 0)
			run(fixture.Exec("CREATE TABLE tx_existing_default(a INT)", 0))
			run(fixture.Exec("INSERT INTO tx_existing_default VALUES(7)", 1))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
			run(fixture.Exec("SET ROLE og_object_reader", 0))
			run(fixture.Select("SELECT a FROM tx_existing_default", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("INSERT INTO tx_existing_default VALUES(9)", 1))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE og_object_other", 0))
			run(fixture.RoleGrantDenied("SELECT a FROM tx_existing_default", "tx_existing_default"))
			run(fixture.Exec("RESET ROLE", 0))
		})
	})
}

// These cases inspect the represented explicit ACL; actual recipient reads and
// sequence uses above independently establish the effective privilege outcome.
func transactionalPrivilegeRelationACLProbe(name, grantee, privilege string) string {
	return "SELECT count(*) > 0 AS v FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.relnamespace='public'::regnamespace AND c.relname='" + name + "' AND r.rolname='" + grantee + "' AND a.privilege_type='" + privilege + "'"
}
