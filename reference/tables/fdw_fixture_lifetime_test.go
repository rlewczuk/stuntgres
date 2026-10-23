package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWFixtureCleanupLifetime(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		fdwMetadata(run)
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_readonly_wrapper", 0))
		if b.PG {
			// Native fdwLive installed its real registered wrapper already;
			// PostgreSQL's live source uses postgres_fdw instead.
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_native", 0))
		}
		for _, server := range []string{"fdw_second", "fdw_truncate_second", "fdw_readonly"} {
			run(fixture.Exec("CREATE SERVER "+server+" FOREIGN DATA WRAPPER fdw_meta", 0))
		}
		const servers = "SELECT count(*) FROM pg_foreign_server WHERE srvname IN ('fdw_server','fdw_live','fdw_second','fdw_truncate_second','fdw_readonly')"
		const wrappers = "SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname IN ('fdw_meta','fdw_readonly_wrapper','fdw_native')"
		run(fixture.Select(servers, []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Select(wrappers, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		run(fixture.Exec("CREATE TABLE fdw_cleanup_transaction(id integer)", 0))
		run(fixture.Exec("GRANT SELECT,INSERT ON fdw_cleanup_transaction TO fdw_reader", 0))
		// SET ROLE precedes BEGIN so rollback alone cannot restore the owner.
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_cleanup_transaction VALUES(1)", 1))
		phaseCtx, cancel := context.WithCancel(p.Ctx)
		cancel()
		// Observe cancellation at database/sql's acquisition boundary, without
		// passing canceled work into the held transaction's driver session.
		// lib/pq may accept that Exec and then retire the physical connection.
		canceledConn, err := b.DB.Conn(phaseCtx)
		if canceledConn != nil {
			if closeErr := canceledConn.Close(); closeErr != nil {
				t.Errorf("unexpected canceled-phase connection close: %v", closeErr)
			}
		}
		if canceledConn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled phase connection=%v error=%T:%v", canceledConn, err, err)
		}
		fixture.TransactionOrdinary(t, err, context.Canceled.Error())
		// The same teardown action must not borrow the canceled phase lifetime.
		fdwCleanup(t, c)
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{owner}}))
		run(fixture.Select("SELECT count(*) FROM fdw_cleanup_transaction", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(servers, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(wrappers, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_data'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// The same borrowed owner connection is usable outside the old phase.
		run(fixture.Exec("INSERT INTO fdw_cleanup_transaction VALUES(2)", 1))
		run(fixture.Select("SELECT id FROM fdw_cleanup_transaction", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("DROP TABLE fdw_cleanup_transaction", 0))
	})
}
