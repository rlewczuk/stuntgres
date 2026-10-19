package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWWriteTransactions(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		t.Run("Smoke", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
			run(fixture.Exec("INSERT INTO fdw_data VALUES(9,'nine',true)", 1))
			run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=9", [][]any{{int64(9), "nine", true}}))
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=9", nil))
			run(fixture.Exec("ROLLBACK", 0))
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=9", nil))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO fdw_data VALUES(10,'ten',false)", 1))
			run(fixture.Exec("COMMIT", 0))
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=10", [][]any{{int64(10), "ten", false}}))
		})
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_write", 0))
		run(fixture.Exec("UPDATE fdw_data SET label='temporary' WHERE id=1", 1))
		run(fixture.Exec("SAVEPOINT after_write", 0))
		run(fixture.Exec("DELETE FROM fdw_data WHERE id=2", 1))
		run(fixture.Exec("ROLLBACK TO after_write", 0))
		run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=2", [][]any{{int64(2), nil, false}}))
		run(fixture.Exec("ROLLBACK TO before_write", 0))
		run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=1", [][]any{{int64(1), "one", true}}))
		run(fixture.Exec("RELEASE before_write", 0))
		run(fixture.Exec("COMMIT", 0))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id<3 ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_data VALUES(20,'chained-commit',true)", 1))
		run(fixture.Exec("COMMIT AND CHAIN", 0))
		run(fixture.Exec("INSERT INTO fdw_data VALUES(21,'chained-rollback',false)", 1))
		run(fixture.Exec("ROLLBACK AND CHAIN", 0))
		run(fixture.Exec("COMMIT", 0))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=20 OR id=21 ORDER BY id", [][]any{{int64(20), "chained-commit", true}}))
		t.Run("HeldCommit", func(t *testing.T) {
			fdwWriteSharedHeld(func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) })
		})
		t.Run("PreparedOriginReuse", func(t *testing.T) {
			fdwWritePreparedOriginReuse(t, p, b, c, reader)
		})
		if !b.PG {
			fdwWriteNativeTransactions(t, p, b, c, reader, run)
			fdwWriteParticipatingCursor(t, p, b, c, reader, run)
			t.Run("ParticipatingHoldSettlement", func(t *testing.T) {
				holdRun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
				fdwWriteHeldParticipants(t, p, b, c, reader, holdRun)
			})
			t.Run("ProviderContractErrors", func(t *testing.T) { fdwWriteProviderContractErrors(t, p, b, c, reader) })
			fdwWriteSessionClose(t, p, b, reader)
			fdwWriteFinalFence(t, p, b, c, reader, run)
		}
	})
}

func TestReferenceFDWWriteConstraints(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_permissive(id integer NOT NULL CHECK(id>0),label text NOT NULL,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("INSERT INTO fdw_permissive VALUES(-1,NULL,NULL),(NULL,'null-id',true)", 2))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id<0 OR id IS NULL ORDER BY id NULLS LAST", [][]any{{int64(-1), nil, nil}, {nil, "null-id", true}}))
		run(fixture.Exec("INSERT INTO fdw_permissive VALUES(NULL,NULL,NULL)", 1))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id IS NULL AND label IS NULL", [][]any{{nil, nil, nil}}))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.generated(id integer,g integer,v integer)", 0)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_generated(id integer,g integer GENERATED ALWAYS AS(id+10) STORED,v integer GENERATED ALWAYS AS(id+20) VIRTUAL) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'generated')", 0))
		run(fixture.Exec("INSERT INTO fdw_generated(id) VALUES(1)", 1))
		// Core computes STORED values before dispatch. postgres_fdw nevertheless
		// transmits DEFAULT for generated attributes; the registered SQL writer
		// stores the callback's actual full tuple instead. Ordinary remote
		// columns have no defaults, so the provider distinction is observable.
		var stored any = int64(11)
		if b.PG {
			stored = nil
		}
		fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT id,g,v FROM fdw_source.generated", []string{"id", "g", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), stored, nil}}))
		run(fixture.Select("SELECT id,g,v FROM fdw_generated", []string{"id", "g", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), stored, int64(21)}}))
		// The expression exists only on the foreign descriptor. Its runtime
		// error proves local STORED generation even when postgres_fdw would
		// omit that computed attribute from its outgoing remote parameters.
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.generation_error(id integer,g integer)", 0)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_generation_error(id integer,g integer GENERATED ALWAYS AS(id/(id-id)) STORED) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'generation_error')", 0))
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO fdw_generation_error(id) VALUES($1)", fdwDiagnostic("22012", "division by zero"), int64(2))))
		fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT id,g FROM fdw_source.generation_error", []string{"id", "g"}, []string{"INT4", "INT4"}, nil))
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.generation_error VALUES(3,99)", 1)
		run(fixture.Prepared(fixture.ErrorDiagnostic("UPDATE fdw_generation_error SET id=$1 WHERE id=3", fdwDiagnostic("22012", "division by zero"), int64(4))))
		fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT id,g FROM fdw_source.generation_error", []string{"id", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(99)}}))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.strict(id integer NOT NULL CHECK(id>0),label text NOT NULL,flag boolean)", 0)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_strict(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'strict')", 0))
		fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_strict VALUES(-1,'bad',true)", nil)
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.strict", nil))
	})
}

func TestReferenceFDWWriteDiagnostics(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		if b.PG {
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS file_fdw", 0))
			run(fixture.Exec("CREATE SERVER fdw_readonly FOREIGN DATA WRAPPER file_fdw", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_readonly_data(id integer,label text,flag boolean) SERVER fdw_readonly OPTIONS(filename '/dev/null')", 0))
		} else {
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_readonly_wrapper HANDLER fdw_scan_only_handler", 0))
			run(fixture.Exec("CREATE SERVER fdw_readonly FOREIGN DATA WRAPPER fdw_readonly_wrapper", 0))
			run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_readonly", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_readonly_data(id integer,label text,flag boolean) SERVER fdw_readonly OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		}
		for _, tc := range []struct{ query, message string }{
			{"INSERT INTO fdw_readonly_data VALUES(4,'four',true)", `cannot insert into foreign table "fdw_readonly_data"`},
			{"UPDATE fdw_readonly_data SET label='no'", `cannot update foreign table "fdw_readonly_data"`},
			{"DELETE FROM fdw_readonly_data", `cannot delete from foreign table "fdw_readonly_data"`},
			{"TRUNCATE fdw_readonly_data", `cannot truncate foreign table "fdw_readonly_data"`},
		} {
			t.Run(tc.query, func(t *testing.T) {
				result, err := c.ExecContext(p.Ctx, tc.query)
				if err == nil || result != nil {
					t.Fatalf("unsupported command result=%v err=%v", result, err)
				}
				want := fdwDiagnostic("0A000", tc.message)
				fixture.AssertDiagnostic(t, err, &want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.ErrorDiagnostic(tc.query, want)))
			})
		}
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
		run(fixture.Exec("CREATE TABLE fdw_mixed_local(id integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_mixed_local VALUES(7)", 1))
		fdwwant := fdwDiagnostic("0A000", `cannot truncate foreign table "fdw_readonly_data"`)
		run(fixture.ErrorDiagnostic("TRUNCATE fdw_mixed_local,fdw_readonly_data", fdwwant))
		run(fixture.Select("SELECT id FROM fdw_mixed_local", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if !b.PG {
			fdwWriteNativeDiagnostics(t, p, b, c, reader, run)
		}
	})
}

func TestReferenceFDWWriteConsumers(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fixture.Exec("INSERT INTO fdw_data SELECT id+10,label,flag FROM fdw_data WHERE id=1", 1))
		run(fixture.Select("WITH changed AS(UPDATE fdw_data SET label='cte' WHERE id=11 RETURNING id,label) SELECT id,label FROM changed", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "cte"}}))
		run(fixture.Exec("CREATE VIEW fdw_writable_view AS SELECT id,label,flag FROM fdw_data", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fdw_writable_view VALUES($1,$2,$3)", 1, int64(12), "view", false)))
		run(fixture.Exec("UPDATE fdw_writable_view SET label='updated-view' WHERE id=12", 1))
		run(fdwRows("DELETE FROM fdw_writable_view WHERE id=12 RETURNING id,label,flag", [][]any{{int64(12), "updated-view", false}}))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>3 ORDER BY id", [][]any{{int64(11), "cte", true}}))
	})
}
