package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type fdwLateCreateResult struct {
	result sql.Result
	err    error
}

// Observe real completion or an actual PostgreSQL object wait. A channel sent
// before Exec is not evidence of database admission, and elapsed time is not a
// lock witness. Native uses its independent final-publication conflict contract.
func fdwObserveLateCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int, done <-chan fdwLateCreateResult) (fdwLateCreateResult, bool) {
	t.Helper()
	if !b.PG {
		select {
		case result := <-done:
			return result, false
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
	}
	for {
		select {
		case result := <-done:
			return result, false
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		default:
		}
		if b.PG {
			var waiting bool
			err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock') AND $2=ANY(pg_blocking_pids($1))", waiter, holder).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				return fdwLateCreateResult{}, true
			}
		}
		runtime.Gosched()
	}
}

func TestReferenceFDWLateDependencies(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, _ func(fixture.Step)) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		var holder, waiter int
		if b.PG {
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
				t.Fatal(err)
			}
			if err := peer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&waiter); err != nil {
				t.Fatal(err)
			}
		}
		for _, edge := range []string{"wrapper_server", "server_mapping", "server_table"} {
			for _, mode := range []string{"RESTRICT", "CASCADE"} {
				if !t.Run(edge+"/"+mode, func(t *testing.T) {
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
					prefix := "fdw_late_" + edge + "_" + strings.ToLower(mode)
					wrapper, server, table := prefix+"_w", prefix+"_s", prefix+"_t"
					t.Cleanup(func() {
						for _, q := range []string{"ROLLBACK", "DROP SERVER IF EXISTS " + server + " CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS " + wrapper + " CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Errorf("cleanup %s: %v", q, err)
							}
						}
					})
					run(fixture.Exec("CREATE FOREIGN DATA WRAPPER "+wrapper+" NO HANDLER NO VALIDATOR", 0))
					if edge != "wrapper_server" {
						run(fixture.Exec("CREATE SERVER "+server+" FOREIGN DATA WRAPPER "+wrapper, 0))
					}
					var wrapperID, serverID int64
					if err := peer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_foreign_data_wrapper WHERE fdwname=$1", wrapper).Scan(&wrapperID); err != nil {
						t.Fatal(err)
					}
					if edge != "wrapper_server" {
						if err := peer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_foreign_server WHERE srvname=$1", server).Scan(&serverID); err != nil {
							t.Fatal(err)
						}
					}
					drop := "DROP SERVER " + server + " " + mode
					create := "CREATE USER MAPPING FOR PUBLIC SERVER " + server + " OPTIONS(tag 'late')"
					if edge == "wrapper_server" {
						drop = "DROP FOREIGN DATA WRAPPER " + wrapper + " " + mode
						create = "CREATE SERVER " + server + " FOREIGN DATA WRAPPER " + wrapper + " OPTIONS(tag 'late')"
					} else if edge == "server_table" {
						create = "CREATE FOREIGN TABLE " + table + "(id integer) SERVER " + server + " OPTIONS(tag 'late')"
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(drop, 0))
					before := reader.snapshot()
					workerContext, cancel := context.WithCancel(p.Ctx)
					started, done := make(chan struct{}), make(chan fdwLateCreateResult, 1)
					joined := false
					defer func() {
						cancel()
						if !joined {
							// Cancel/join before case cleanup returns either borrower.
							<-done
						}
					}()
					go func() {
						close(started)
						result, err := peer.ExecContext(workerContext, create)
						done <- fdwLateCreateResult{result, err}
					}()
					<-started
					created, blocked := fdwObserveLateCreate(t, p, b, waiter, holder, done)
					if !blocked {
						joined = true
					}
					if b.PG {
						// This is deliberately a safe rollback oracle. Do not infer
						// PostgreSQL's unexercised dropped-parent COMMIT outcome.
						run(fixture.Exec("ROLLBACK", 0))
						if blocked {
							created = <-done
							joined = true
						}
						t.Logf("oracle rollback boundary: incoming create waited behind retirement=%t", blocked)
					}
					fixture.DeleteReferenceCount(t, created.result, created.err, 0)
					if !b.PG {
						result, err := c.ExecContext(p.Ctx, "COMMIT")
						if result != nil {
							t.Fatal("native retirement published despite a committed late dependency", drop, result, err)
						}
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog publication conflict")
						run(fixture.Exec("ROLLBACK", 0))
					}
					if edge == "wrapper_server" {
						if err := peer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_foreign_server WHERE srvname=$1", server).Scan(&serverID); err != nil {
							t.Fatal(err)
						}
					}
					check := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, step) }
					check(fixture.Select("SELECT oid=$1::oid AS same_wrapper,fdwhandler=0 AS no_handler,fdwvalidator=0 AS no_validator FROM pg_foreign_data_wrapper WHERE fdwname=$2", []string{"same_wrapper", "no_handler", "no_validator"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, wrapperID, wrapper))
					check(fixture.Select("SELECT oid=$1::oid AS same_server,srvfdw=$2::oid AS same_wrapper FROM pg_foreign_server WHERE srvname=$3", []string{"same_server", "same_wrapper"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, serverID, wrapperID, server))
					check(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1417::oid AND objid=$1::oid AND refclassid=2328::oid AND refobjid=$2::oid AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, serverID, wrapperID))
					if edge == "server_mapping" {
						check(fixture.Select("SELECT umuser=0 AS public_mapping,umoptions::text AS options FROM pg_user_mapping WHERE umserver=$1::oid", []string{"public_mapping", "options"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{tag=late}"}}, serverID))
						check(fixture.Select("SELECT srvname FROM pg_user_mappings WHERE srvname=$1", []string{"srvname"}, []string{"NAME"}, [][]any{{server}}, server))
						check(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_user_mapping m ON m.oid=d.objid WHERE d.classid=1418::oid AND d.refclassid=1417::oid AND d.refobjid=$1::oid AND m.umserver=$1::oid AND d.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, serverID))
					} else if edge == "server_table" {
						check(fixture.Select("SELECT c.relkind,f.ftserver=$1::oid AS same_server,f.ftoptions::text AS options FROM pg_class c JOIN pg_foreign_table f ON f.ftrelid=c.oid WHERE c.relname=$2", []string{"relkind", "same_server", "options"}, []string{"CHAR", "BOOL", "TEXT"}, [][]any{{"f", true, "{tag=late}"}}, serverID, table))
						check(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid=1259::oid AND d.refclassid=1417::oid AND d.refobjid=$1::oid AND c.relname=$2 AND d.deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, serverID, table))
					} else {
						check(fixture.Select("SELECT srvoptions::text AS options FROM pg_foreign_server WHERE srvname=$1", []string{"options"}, []string{"TEXT"}, [][]any{{"{tag=late}"}}, server))
					}
					if !b.PG && reader.snapshot() != before {
						t.Fatal("metadata retirement invoked source callbacks", before, reader.snapshot())
					}
				}) {
					t.FailNow()
				}
			}
		}
	})
}
