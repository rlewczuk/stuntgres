package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckBtreeTemporary(t *testing.T) {
	for _, family := range []string{"bt_index_check", "bt_index_parent_check"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-%t", family, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"bt_temp_reader"})
				heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn) {
					caller, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer caller.Close()
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						for _, c := range []*sql.Conn{caller, owner} {
							for _, query := range []string{"ROLLBACK", "RESET ROLE"} {
								if _, err := c.ExecContext(ctx, query); err != nil {
									t.Error("B-tree temporary cleanup", query, err)
								}
							}
						}
					}()
					run := func(c *sql.Conn, step fixture.Step) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					}
					for _, query := range []string{
						"CREATE EXTENSION amcheck",
						"CREATE ROLE bt_temp_reader",
						"GRANT USAGE ON SCHEMA public TO bt_temp_reader",
						"CREATE TABLE bt_persistent(v INT)",
						"CREATE INDEX bt_persistent_idx ON bt_persistent(v)",
						"CREATE TEMP TABLE bt_checked_tmp(v INT)",
						"CREATE INDEX bt_checked_idx ON bt_checked_tmp(v)",
						"CREATE INDEX bt_checked_hash ON bt_checked_tmp USING hash(v)",
						"CREATE TEMP SEQUENCE bt_checked_sequence",
					} {
						run(owner, fixture.Exec(query, 0))
					}
					run(owner, fixture.Exec("INSERT INTO bt_checked_tmp VALUES(7),(9)", 2))
					run(owner, fixture.Exec("INSERT INTO bt_persistent VALUES(1),(2)", 2))
					// Creation and identity lookup occur before every tested transaction.
					// Owning either session's unused TEMP objects is not itself access.
					run(caller, fixture.Exec("CREATE TEMP TABLE bt_unused_tmp(v INT)", 0))
					oids := make(map[string]int64)
					for _, name := range []string{"bt_checked_tmp", "bt_checked_idx", "bt_checked_hash", "bt_checked_sequence", "bt_persistent_idx"} {
						var oidsValue int64
						if err := owner.QueryRowContext(p.Ctx, "SELECT '"+name+"'::regclass::oid::bigint").Scan(&oidsValue); err != nil {
							t.Fatal("numeric relation identity", name, err)
						}
						oids[name] = oidsValue
					}
					query := "SELECT public." + family + "($1::oid::regclass,$2::boolean) AS ok"
					statements := make(map[*sql.Conn]*sql.Stmt)
					if prepared {
						for _, c := range []*sql.Conn{owner, caller} {
							stmt, err := c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							statements[c] = stmt
						}
					}
					invoke := func(c *sql.Conn, step fixture.Step) {
						t.Helper()
						var executor fixture.Executor = c
						if prepared {
							executor = fixture.HeldStatement{Executor: c, Statement: statements[c], SQL: query}
						}
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
					}
					lockFree := func() {
						t.Helper()
						run(owner, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE locktype='relation' AND relation IN ($1::oid,$2::oid,$3::oid,$4::oid) AND granted", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, oids["bt_checked_tmp"], oids["bt_checked_idx"], oids["bt_checked_hash"], oids["bt_checked_sequence"]))
					}
					finish := func(c, observer *sql.Conn, phase string, accessed bool) {
						t.Helper()
						gid := fmt.Sprintf("bt_temp_%s_%t_%s", family, prepared, phase)
						fixture.PreparedReferenceCleanup(t, b, gid)
						if accessed {
							run(c, fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", heapDiagnostic("0A000", "cannot PREPARE a transaction that has operated on temporary objects")))
							fixture.PreparedReferenceAbsent(t, p, observer, gid)
							run(c, fixture.Exec("ROLLBACK", 0))
						} else {
							run(c, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
							fixture.PreparedReferenceOne(t, p, observer, gid)
							run(observer, fixture.Exec("ROLLBACK PREPARED '"+gid+"'", 0))
						}
						fixture.PreparedReferenceAbsent(t, p, observer, gid)
						lockFree()
					}
					denied := func(phase string) {
						t.Helper()
						run(caller, fixture.Exec("BEGIN", 0))
						run(caller, fixture.Exec("SAVEPOINT bt_attempt", 0))
						invoke(caller, heapError(query, heapDiagnostic("42501", "permission denied for function "+family), oids["bt_checked_idx"], true))
						run(caller, fixture.Exec("ROLLBACK TO bt_attempt", 0))
						finish(caller, owner, phase, false)
					}
					run(caller, fixture.Exec("SET ROLE bt_temp_reader", 0))
					denied("denied")
					run(owner, fixture.Exec("GRANT EXECUTE ON FUNCTION public."+family+"(regclass,boolean) TO bt_temp_reader", 0))
					// Strict NULL with a non-NULL foreign TEMP identity never opens it.
					run(caller, fixture.Exec("BEGIN", 0))
					invoke(caller, amcheckSelect(query, true, oids["bt_checked_idx"], nil))
					finish(caller, owner, "strict_null", false)
					// Cancel actual first-relation waits, not merely a pre-canceled
					// caller. Neither heap-open nor wrong-kind target-open has yet
					// happened, so rollback to the savepoint must permit PREPARE.
					for _, target := range []string{"bt_checked_idx", "bt_checked_tmp"} {
						run(owner, fixture.Exec("BEGIN", 0))
						run(owner, fixture.Exec("LOCK TABLE pg_temp.bt_checked_tmp IN ACCESS EXCLUSIVE MODE", 0))
						run(caller, fixture.Exec("BEGIN", 0))
						run(caller, fixture.Exec("SAVEPOINT bt_attempt", 0))
						var executor fixture.Executor = caller
						if prepared {
							executor = fixture.HeldStatement{Executor: caller, Statement: statements[caller], SQL: query}
						}
						fixture.RelationLockActiveCancel(t, p, b, caller, query, func(ctx context.Context) fixture.RelationOutcome {
							result, err := executor.ExecContext(ctx, query, oids[target], true)
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						run(caller, fixture.Exec("ROLLBACK TO bt_attempt", 0))
						run(owner, fixture.Exec("ROLLBACK", 0))
						finish(caller, owner, "cancel_"+target, false)
					}
					for _, foreign := range []bool{false, true} {
						c, observer := owner, caller
						if foreign {
							c, observer = caller, owner
						}
						for _, target := range []string{"bt_checked_idx", "bt_checked_hash", "bt_checked_tmp", "bt_checked_sequence"} {
							run(c, fixture.Exec("BEGIN", 0))
							run(c, fixture.Exec("SAVEPOINT bt_attempt", 0))
							var diagnostic fixture.Diagnostic
							switch target {
							case "bt_checked_idx":
								if foreign {
									diagnostic = heapDiagnostic("0A000", "cannot access temporary tables of other sessions")
									diagnostic.Detail = `Index "bt_checked_idx" is associated with temporary relation.`
								}
							case "bt_checked_hash":
								diagnostic = heapDiagnostic("0A000", `expected "btree" index as targets for verification`)
								diagnostic.Detail = `Relation "bt_checked_hash" is a hash index.`
							default:
								diagnostic = heapDiagnostic("42809", `"`+target+`" is not an index`)
							}
							if diagnostic.Code == "" {
								invoke(c, amcheckSelect(query, false, oids[target], true))
								lockFree()
							} else {
								invoke(c, heapError(query, diagnostic, oids[target], true))
							}
							// The relation-open footprint survives both successful work
							// and rejected kind/method/foreign storage after ROLLBACK TO.
							run(c, fixture.Exec("ROLLBACK TO bt_attempt", 0))
							lockFree()
							finish(c, observer, fmt.Sprintf("%t_%s", foreign, target), true)
						}
					}
					run(owner, fixture.Exec("REVOKE EXECUTE ON FUNCTION public."+family+"(regclass,boolean) FROM bt_temp_reader", 0))
					denied("revoked")
					run(caller, fixture.Exec("RESET ROLE", 0))
					// A real persistent verification still permits preparation while
					// the owning physical session retains all its unused TEMP objects.
					run(owner, fixture.Exec("BEGIN", 0))
					invoke(owner, amcheckSelect(query, false, oids["bt_persistent_idx"], true))
					finish(owner, caller, "unused", false)
					run(owner, fixture.Select("SELECT v FROM pg_temp.bt_checked_tmp ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					invoke(owner, amcheckSelect(query, false, oids["bt_persistent_idx"], true))
					lockFree()
				})
			})
		}
	}
}
