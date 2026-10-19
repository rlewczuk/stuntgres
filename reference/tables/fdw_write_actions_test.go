package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The same statements and independently specified results exercise the
// registered provider and PostgreSQL's genuine loopback postgres_fdw.
func TestReferenceFDWWriteActions(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		t.Run("Smoke", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("INSERT INTO fdw_data VALUES(4,'four',true)", 1))
			run(fixture.Prepared(fixture.Exec("UPDATE fdw_data SET label=$1 WHERE id=$2", 1, "two", int64(2))))
			run(fixture.Exec("DELETE FROM fdw_data WHERE id=3", 1))
			want := [][]any{{int64(1), "one", true}, {int64(2), "two", false}, {int64(4), "four", true}}
			run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", want))
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", want))
		})
		run(fixture.Exec("UPDATE fdw_data SET label='absent' WHERE id=99", 0))
		run(fixture.Prepared(fixture.Exec("DELETE FROM fdw_data WHERE id=$1", 0, int64(99))))
		q := "INSERT INTO fdw_data VALUES($1,$2,$3)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, id := range []int64{5, 6} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, id, nil, false))
		}
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>4 ORDER BY id", [][]any{{int64(5), nil, false}, {int64(6), nil, false}}))
		if !b.PG {
			fdwWritePreparedContext(t, p, b, c, reader)
		}
	})
}

func fdwWriteSourceRows(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *fdwSQLReader, step fixture.Step) {
	t.Helper()
	var source fixture.Executor = reader.source
	if b.PG {
		source = b.DB
	}
	step.NonContext = true
	fixture.RunStep(t, p.Ctx, source, &fixture.Recorder{}, step)
}

func TestReferenceFDWWriteIdentity(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.data VALUES(12,'alternate',true),(7,'same',false),(8,'same',false)", 3)
		run(fixture.Exec("UPDATE fdw_data SET id=22,label='changed' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM fdw_data WHERE id=7", 1))
		run(fixture.Exec("CREATE TABLE fdw_matches(k integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_matches VALUES(8),(8)", 2))
		run(fixture.Exec("UPDATE fdw_data f SET label='once' FROM fdw_matches m WHERE f.id=m.k", 1))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>3 ORDER BY id", [][]any{{int64(8), "once", false}, {int64(12), "alternate", true}, {int64(22), "changed", false}}))
		run(fixture.Exec("DELETE FROM fdw_data f USING fdw_matches m WHERE f.id=m.k", 1))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=8", nil))
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.identity_rows(rid integer,id integer,label text,flag boolean)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.identity_rows VALUES(101,7,'duplicate',false),(102,7,'duplicate',false)", 2)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_duplicates(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'identity_rows')", 0))
		run(fixture.Exec("UPDATE fdw_duplicates SET id=12 WHERE id=7", 2))
		fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT rid,id,label FROM fdw_source.identity_rows ORDER BY rid", []string{"rid", "id", "label"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(101), int64(12), "duplicate"}, {int64(102), int64(12), "duplicate"}}))
		run(fixture.Exec("DELETE FROM fdw_duplicates WHERE id=12", 2))
		fdwWriteSourceRows(t, p, b, reader, fixture.Select("SELECT rid,id FROM fdw_source.identity_rows", []string{"rid", "id"}, []string{"INT4", "INT4"}, nil))
		if !b.PG {
			fdwWriteNativeIdentity(t, p, b, c, reader)
		}
	})
}

func TestReferenceFDWWriteReturning(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		run(fdwRows("INSERT INTO fdw_data VALUES(4,'four',true) RETURNING id,label,flag", [][]any{{int64(4), "four", true}}))
		run(fixture.Prepared(fdwRows("UPDATE fdw_data SET label='two' WHERE id=2 RETURNING id,label,flag", [][]any{{int64(2), "two", false}})))
		run(fdwRows("DELETE FROM fdw_data WHERE id=3 RETURNING id,label,flag", [][]any{{int64(3), "three", nil}}))
		run(fixture.Prepared(fdwRows("UPDATE fdw_data SET label='none' WHERE id=99 RETURNING id,label,flag", nil)))
		run(fixture.Select("UPDATE fdw_data SET id=12 WHERE id=2 RETURNING old.id AS before,new.id AS after", []string{"before", "after"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(12)}}))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(4), "four", true}, {int64(12), "two", false}}))
		t.Run("CombinedSourceFrame", func(t *testing.T) {
			fdwWriteCombinedSourceFrame(t, p, b, c, reader)
		})
		t.Run("VirtualSourceIdentity", func(t *testing.T) {
			fdwWriteVirtualSourceIdentity(t, p, b, c, reader)
		})
		if b.PG {
			fdwWritePostgresTrigger(t, p, b, c, reader, run)
		} else {
			fdwWriteNativeReturning(t, p, b, c, reader, run)
		}
		fdwWriteViewChecks(t, p, b, c, reader, run)
	})
}
