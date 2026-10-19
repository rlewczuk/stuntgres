package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWWriteBatch(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		if b.PG {
			run(fixture.Exec("ALTER SERVER fdw_live OPTIONS(ADD batch_size '2')", 0))
		}
		run(fixture.Exec("INSERT INTO fdw_data VALUES(4,'four',true),(5,'five',false),(6,NULL,NULL),(7,'seven',true),(8,'eight',false)", 5))
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>3 ORDER BY id", [][]any{{int64(4), "four", true}, {int64(5), "five", false}, {int64(6), nil, nil}, {int64(7), "seven", true}, {int64(8), "eight", false}}))
		run(fdwRows("INSERT INTO fdw_data VALUES(9,'nine',true),(10,'ten',false) RETURNING id,label,flag", [][]any{{int64(9), "nine", true}, {int64(10), "ten", false}}))
		fdwSourceExec(t, p, b, reader, run, "ALTER TABLE fdw_source.data ADD CONSTRAINT positive_id CHECK(id>0)", 0)
		if result, err := c.ExecContext(p.Ctx, "INSERT INTO fdw_data VALUES(11,'eleven',true),(12,'twelve',false),(-1,'invalid',true)"); err == nil || result != nil {
			t.Fatalf("failed batch result=%v err=%v", result, err)
		}
		fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>10 ORDER BY id", nil))
		if !b.PG {
			fdwWriteNativeBatch(t, p, b, c, reader, run)
		}
	})
}

func TestReferenceFDWWriteCopy(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		stream, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		t.Run("Smoke", func(t *testing.T) {
			result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY fdw_data FROM STDIN", strings.NewReader("4\tfour\tt\n5\t\\N\tf\n"))
			if err != nil || result != (stuntgres.CopyResult{Count: 2, CommandTag: "COPY 2"}) {
				t.Fatalf("COPY result=%+v err=%v", result, err)
			}
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>3 ORDER BY id", [][]any{{int64(4), "four", true}, {int64(5), nil, false}}))
			result, err = fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY fdw_data FROM STDIN", strings.NewReader("6\tsix\tt\ninvalid\tbad\tf\n"))
			if err == nil || result != (stuntgres.CopyResult{}) {
				t.Fatalf("failed COPY result=%+v err=%v", result, err)
			}
			fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>5 ORDER BY id", nil))
		})
		fdwWriteCopyFormats(t, p, b, c, stream, reader, run)
	})
}

func TestReferenceFDWWriteTruncate(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, reader, run)
		fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.other(id integer,label text,flag boolean)", 0)
		fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.other VALUES(9,'nine',true)", 1)
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_other_data(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'other')", 0))
		t.Run("Smoke", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("TRUNCATE fdw_data,fdw_other_data CONTINUE IDENTITY RESTRICT", 0))
			for _, table := range []string{"data", "other"} {
				fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source."+table+" ORDER BY id", nil))
			}
		})
		fdwWriteTruncateGroups(t, p, b, c, reader, run)
	})
}
