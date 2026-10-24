package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Expectations are independent PG18.6 observations from root artifact263,
// including same-key pre-SRX versus cross-encoding post-SRX and index races.
func TestReferenceCollationTransactionCreateAdmission(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("private_source_namespace/held=%v", held), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "boundary_private")
			fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
				defer cancel()
				c := fixture.SequenceCoreConn(t, ctx, b.DB)
				peer := fixture.SequenceCoreConn(t, ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, ctx, x, b.Recorder, s) }
				tx, err := c.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(tx, fixture.Exec("CREATE SCHEMA boundary_private", 0))
				run(tx, fixture.Exec(`CREATE COLLATION boundary_private.source FROM pg_catalog."C"`, 0))
				run(tx, fixture.Exec("SAVEPOINT source_ready", 0))
				q := "CREATE COLLATION boundary_private.copy FROM boundary_private.source"
				var execute fixture.Executor = tx
				if held {
					stmt, err := tx.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					execute = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
				}
				for range 2 {
					run(execute, fixture.Exec(q, 0))
					run(tx, fixture.Select("SELECT upper('é' COLLATE boundary_private.copy) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
					run(tx, fixture.Select("SELECT collname,collprovider,collencoding,collisdeterministic,collcollate,collctype,collversion FROM pg_collation WHERE collname IN ('source','copy') ORDER BY collname", []string{"collname", "collprovider", "collencoding", "collisdeterministic", "collcollate", "collctype", "collversion"}, []string{"NAME", "CHAR", "INT4", "BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{{"copy", "c", int64(-1), true, "C", "C", nil}, {"source", "c", int64(-1), true, "C", "C", nil}}))
					run(peer, fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='boundary_private'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(peer, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname IN ('source','copy')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(tx, fixture.Exec("ROLLBACK TO source_ready", 0))
					run(tx, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname='copy'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				run(execute, fixture.Exec(q, 0))
				run(tx, fixture.Exec("RELEASE source_ready", 0))
				run(tx, fixture.Exec("DROP COLLATION boundary_private.source", 0))
				run(tx, fixture.Select("SELECT upper('é' COLLATE boundary_private.copy) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(peer, fixture.Select("SELECT upper('é' COLLATE boundary_private.copy) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
			})
		})
	}
	for _, kind := range []string{"same_duplicate", "same_skip", "missing_from", "invalid_options", "cross_duplicate", "cross_skip", "reverse_duplicate", "reverse_skip", "concurrent", "concurrent_skip", "concurrent_rollback", "concurrent_skip_rollback"} {
		for _, held := range []bool{false, true} {
			for _, route := range []string{"autocommit", "sql", "go"} {
				t.Run(fmt.Sprintf("%s/held=%v/%s", kind, held, route), func(t *testing.T) {
					fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
						defer cancel()
						var notices []fixture.Diagnostic
						_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						holder := fixture.SequenceCoreConn(t, ctx, b.DB)
						peer := fixture.SequenceCoreConn(t, ctx, b.DB)
						run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, ctx, x, recorder, s) }
						defer holder.ExecContext(context.Background(), "ROLLBACK")
						defer c.ExecContext(context.Background(), "ROLLBACK")
						run(c, fixture.Exec("SET search_path=public,pg_catalog", 0))
						run(holder, fixture.Exec("SET search_path=public,pg_catalog", 0))
						concurrent := strings.HasPrefix(kind, "concurrent")
						reverse := strings.HasPrefix(kind, "reverse")
						post := concurrent || reverse || strings.HasPrefix(kind, "cross")
						skip := strings.Contains(kind, "skip")
						incumbent := `CREATE COLLATION boundary_same FROM pg_catalog."C"`
						if reverse {
							incumbent = "CREATE COLLATION boundary_same (provider=libc,locale='C')"
						}
						if !concurrent {
							run(c, fixture.Exec(incumbent, 0))
						}
						original := fixture.DefinitionIdentityRows(t, ctx, c, "SELECT * FROM pg_collation WHERE collname='boundary_same'")
						deps := fixture.DefinitionIdentityRows(t, ctx, c, "SELECT d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_collation c ON c.oid=d.objid WHERE d.classid=3456 AND c.collname='boundary_same' ORDER BY d.refclassid,d.refobjid,d.refobjsubid,d.deptype")
						q := `CREATE COLLATION boundary_same FROM pg_catalog."C"`
						if strings.HasPrefix(kind, "cross") {
							q = "CREATE COLLATION boundary_same (provider=libc,locale='C')"
						}
						if skip {
							q = strings.Replace(q, "CREATE COLLATION ", "CREATE COLLATION IF NOT EXISTS ", 1)
						}
						if kind == "missing_from" {
							q = "CREATE COLLATION boundary_fresh FROM boundary_missing"
						}
						if kind == "invalid_options" {
							q = "CREATE COLLATION boundary_fresh (locale='C',lc_collate='C')"
						}
						notices = nil
						var stmt *sql.Stmt
						if held {
							var err error
							stmt, err = c.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal("quiet Prepare", err)
							}
							defer stmt.Close()
						}
						if len(notices) != 0 {
							t.Fatal("Prepare emitted notices", notices)
						}
						var namespace int64
						if err := c.QueryRowContext(ctx, "SELECT oid::bigint FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil {
							t.Fatal(err)
						}
						run(holder, fixture.Exec("BEGIN", 0))
						holderCreate := `CREATE COLLATION boundary_hold FROM pg_catalog."C"`
						if concurrent {
							holderCreate = `CREATE COLLATION boundary_same FROM pg_catalog."C"`
						}
						run(holder, fixture.Exec(holderCreate, 0))
						var waiter fixture.Executor = c
						var tx *sql.Tx
						if route == "go" {
							var err error
							tx, err = c.BeginTx(ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							waiter = tx
						} else if route == "sql" {
							run(c, fixture.Exec("BEGIN", 0))
						}
						if route != "autocommit" {
							run(waiter, fixture.Exec("SAVEPOINT waiting", 0))
						}
						if !post {
							run(waiter, fixture.Exec("SET lock_timeout='100ms'", 0))
						}
						var execute fixture.Executor = waiter
						if stmt != nil {
							if tx != nil {
								stmt = tx.StmtContext(ctx, stmt)
								defer stmt.Close()
							}
							execute = fixture.HeldStatement{Executor: waiter, Statement: stmt, SQL: q}
						}
						call := func(worker context.Context) fixture.RelationOutcome {
							r, e := execute.ExecContext(worker, q)
							return fixture.RelationOutcome{Result: r, Err: e}
						}
						var outcome fixture.RelationOutcome
						if post {
							join := fixture.DefinitionLockCall(t, ctx, p, b, c, holder, q, "relation", "ShareRowExclusiveLock", 0, 3456, call)
							end := "COMMIT"
							if strings.HasSuffix(kind, "rollback") {
								end = "ROLLBACK"
							}
							run(holder, fixture.Exec(end, 0))
							outcome = join()
						} else {
							// Join BEFORE releasing the unrelated holder. A lock timeout is not a
							// substitute for the independently required semantic error/skip.
							join := fixture.LockOperation(t, ctx, call)
							outcome = join()
							run(holder, fixture.Select("SELECT collencoding FROM pg_collation WHERE collname='boundary_hold'", []string{"collencoding"}, []string{"INT4"}, [][]any{{int64(-1)}}))
							run(peer, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname='boundary_hold'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							if b.PG {
								var granted bool
								if err := holder.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='relation' AND relation=3456 AND mode='ShareRowExclusiveLock' AND granted)").Scan(&granted); err != nil || !granted {
									t.Fatal("live holder SRX", granted, err)
								}
							}
						}
						success := skip && !concurrent || strings.HasSuffix(kind, "rollback")
						want := fixture.ControlsReferenceDiagnostic("42710", `collation "boundary_same" already exists`)
						if kind == "missing_from" {
							want = fixture.ControlsReferenceDiagnostic("42704", `collation "boundary_missing" for encoding "UTF8" does not exist`)
						}
						if kind == "invalid_options" {
							want = fixture.ControlsReferenceDiagnostic("42601", "conflicting or redundant options")
							want.Detail = "LOCALE cannot be specified together with LC_COLLATE or LC_CTYPE."
						}
						if concurrent {
							want = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_collation_name_enc_nsp_index"`)
							want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_collation", "pg_collation_name_enc_nsp_index"
							want.Detail = fmt.Sprintf("Key (collname, collencoding, collnamespace)=(boundary_same, -1, %d) already exists.", namespace)
						}
						if success {
							if outcome.Err != nil {
								t.Fatal("Execute", outcome.Err)
							}
							fixture.LockAffected(t, outcome.Result, 0)
						} else {
							fixture.AssertDiagnostic(t, outcome.Err, &want)
							if outcome.Result != nil {
								t.Fatal("failed Execute returned result")
							}
						}
						var wantNotices []fixture.Diagnostic
						if skip && !concurrent {
							wantNotices = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `collation "boundary_same" already exists, skipping`}}
						}
						if !reflect.DeepEqual(notices, wantNotices) {
							t.Fatalf("Execute notices %#v want %#v", notices, wantNotices)
						}
						if route != "autocommit" && !success {
							run(waiter, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
							run(waiter, fixture.Exec("ROLLBACK TO waiting", 0))
						}
						if concurrent {
							run(waiter, fixture.Select("SELECT collencoding FROM pg_collation WHERE collname='boundary_same'", []string{"collencoding"}, []string{"INT4"}, [][]any{{int64(-1)}}))
							peerCount := int64(1)
							if strings.HasSuffix(kind, "rollback") && route != "autocommit" {
								peerCount = 0
							}
							run(peer, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname='boundary_same'", []string{"n"}, []string{"INT8"}, [][]any{{peerCount}}))
						} else {
							fixture.DefinitionIdentityEqual(t, fixture.DefinitionIdentityRows(t, ctx, waiter, "SELECT * FROM pg_collation WHERE collname='boundary_same'"), original)
							fixture.DefinitionIdentityEqual(t, fixture.DefinitionIdentityRows(t, ctx, waiter, "SELECT d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_collation c ON c.oid=d.objid WHERE d.classid=3456 AND c.collname='boundary_same' ORDER BY d.refclassid,d.refobjid,d.refobjsubid,d.deptype"), deps)
						}
						run(waiter, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE collname='boundary_fresh'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						if tx != nil {
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else if route == "sql" {
							run(c, fixture.Exec("ROLLBACK", 0))
						}
						if !post {
							run(holder, fixture.Exec("ROLLBACK", 0))
						}
						run(c, fixture.Exec("RESET lock_timeout", 0))
						run(c, fixture.Exec(`CREATE COLLATION boundary_reuse FROM pg_catalog."C"`, 0))
						run(c, fixture.Select("SELECT upper('é' COLLATE boundary_reuse) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
					})
				})
			}
		}
	}
}
