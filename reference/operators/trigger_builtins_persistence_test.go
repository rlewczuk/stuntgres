package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBuiltinsPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tb_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA tb_saved",
			"CREATE TEXT SEARCH DICTIONARY tb_saved.reject (TEMPLATE=pg_catalog.simple,Accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION tb_saved.live (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION tb_saved.live ALTER MAPPING FOR asciiword WITH tb_saved.reject",
			`CREATE TABLE tb_saved."Explicit Table"(id integer PRIMARY KEY,"Title ' Q" text,v tsvector)`,
			`CREATE TRIGGER "a ' vector" BEFORE INSERT OR UPDATE ON tb_saved."Explicit Table" FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','Title '' Q')`,
			`CREATE TRIGGER z BEFORE UPDATE ON tb_saved."Explicit Table" FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()`,
			"CREATE TABLE tb_saved.row_config(id integer PRIMARY KEY,cfg regconfig,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_saved.row_config FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`INSERT INTO tb_saved."Explicit Table" VALUES(1,'Rats',NULL)`, 1))
		run(fixture.Exec(`UPDATE tb_saved."Explicit Table" SET v='manual:9' WHERE id=1`, 1))
		run(fixture.Exec("INSERT INTO tb_saved.row_config VALUES(1,'tb_saved.live','Rats',NULL)", 1))
		explicitSQL := `UPDATE tb_saved."Explicit Table" SET "Title ' Q"=$1 WHERE id=1 RETURNING id,v::text AS vector`
		rowSQL := "UPDATE tb_saved.row_config SET title=$1 WHERE id=1 RETURNING id,v::text AS vector"
		explicit, err := c.PrepareContext(p.Ctx, explicitSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer explicit.Close()
		row, err := c.PrepareContext(p.Ctx, rowSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer row.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			step(builtinVectorRows(`SELECT id,v::text AS vector FROM tb_saved."Explicit Table" ORDER BY id`, []any{int64(1), "'manual':9"}))
			step(builtinVectorRows("SELECT id,v::text AS vector FROM tb_saved.row_config ORDER BY id", []any{int64(1), ""}))
			step(fixture.Select("SELECT tgfoid::bigint,tgnargs::integer FROM pg_trigger WHERE tgrelid='tb_saved.row_config'::regclass", []string{"tgfoid", "tgnargs"}, []string{"INT8", "INT4"}, [][]any{{int64(3753), int64(3)}}))
			step(fixture.Exec("BEGIN", 0))
			for _, call := range []struct {
				q      string
				stmt   *sql.Stmt
				vector string
			}{{explicitSQL, explicit, "'cats':1"}, {rowSQL, row, ""}} {
				s := builtinVectorRows(call.q, []any{int64(1), call.vector})
				s.Args = []any{"Cats"}
				if conn == c {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: call.stmt, SQL: call.q}, backend.Recorder, s)
				} else {
					step(fixture.Prepared(s))
				}
			}
			step(fixture.Exec(`UPDATE tb_saved."Explicit Table" SET id=id`, 0))
			step(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION tb_saved.live ALTER MAPPING FOR asciiword WITH pg_catalog.simple", 0))
			s := builtinVectorRows(rowSQL, []any{int64(1), "'cats':1"})
			s.Args = []any{"Cats"}
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: row, SQL: rowSQL}, backend.Recorder, s)
			} else {
				step(fixture.Prepared(s))
			}
			step(fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tb_saved", check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Replacement without Reset must also retain stored, manually saved vectors.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(fc, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord})
		run(fixture.Exec("CREATE TABLE tb_saved.sentinel(n integer)", 0))
		run(fixture.Exec("INSERT INTO tb_saved.sentinel VALUES(42)", 1))
		retained, err := c.QueryContext(p.Ctx, `SELECT id,v::text AS vector FROM tb_saved."Explicit Table"`)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		preserved := func() {
			check(c, b)
			run(fixture.Select("SELECT n FROM tb_saved.sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='tb_staged_prefix'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		bad := dump.String() + ";CREATE TABLE tb_saved.tb_staged_prefix(n integer);CREATE TRIGGER bad BEFORE INSERT ON tb_saved.row_config FOR EACH ROW EXECUTE FUNCTION tb_saved.missing();"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("late invalid trigger accepted")
		}
		preserved()
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserved()
		reading, stop := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(dump.Bytes()), Cancel: stop})
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserved()
		if !retained.Next() {
			t.Fatal("retained row missing", retained.Err())
		}
		var id int64
		var vector string
		if err := retained.Scan(&id, &vector); err != nil || id != 1 || vector != "'manual':9" {
			t.Fatal(id, vector, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained row cardinality", retained.Err())
		}
	})
}
