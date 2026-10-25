package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/ngrok/sqlmw"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair (id DEFAULT 7 NOT NULL,note DEFAULT 'ok',CONSTRAINT positive CHECK(id>0))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		run(fixture.Exec("CREATE TABLE detached OF pair", 0))
		run(fixture.Exec("ALTER TABLE detached NOT OF", 0))
		run(fixture.Exec("INSERT INTO detached VALUES(22,'det')", 1))
		run(fixture.Exec("CREATE TABLE copied(LIKE t)", 0))
		run(fixture.Exec("CREATE TYPE empty_pair AS ()", 0))
		run(fixture.Exec("CREATE TABLE empty_t OF empty_pair", 0))
		run(fixture.Exec(`CREATE TYPE "Pé" AS ("Id" integer)`, 0))
		run(fixture.Exec(`CREATE TABLE "Té" OF "Pé"`, 0))
		run(fixture.Exec(`INSERT INTO "Té" VALUES(31)`, 1))
		run(fixture.Exec("CREATE TABLE holder(v pair)", 0))
		run(fixture.Exec("INSERT INTO holder VALUES('(41,nest)')", 1))
		state := []fixture.Step{
			typedTablePairRows("t", [][]any{{int64(11), "hey"}}),
			typedTablePairRows("detached", [][]any{{int64(22), "det"}}),
			typedTablePairRows("copied", nil),
			fixture.TypedTableAttachment("t", "pair", true),
			fixture.TypedTableAttachment("detached", "pair", false),
			fixture.TypedTableAttachment("copied", "pair", false),
			fixture.TypedTableAttachment("empty_t", "empty_pair", true),
			fixture.Select(`SELECT * FROM "Té"`, []string{"Id"}, []string{"INT4"}, [][]any{{int64(31)}}),
			fixture.Select("SELECT 't'::regtype::oid<>'pair'::regtype::oid AS distinct_row_type", []string{"distinct_row_type"}, []string{"BOOL"}, [][]any{{true}}),
		}
		for _, s := range state {
			run(s)
		}
		fixture.CompositeReferenceRows(t, p, b, c, "SELECT v FROM holder", [][]any{{"(41,nest)"}}, prepared)
		if b.PG {
			return
		}
		var held driver.Rows
		heldConn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer heldConn.Close()
		if err := heldConn.Raw(func(raw any) error {
			var e error
			held, e = raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT * FROM t", nil)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		freshDriver := stuntgres.NewDriver()
		fresh := fixture.DriverPool(t, freshDriver, "typed-fresh")
		freshConnector, err := freshDriver.OpenConnector("typed-fresh")
		if err != nil {
			t.Fatal(err)
		}
		wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: freshConnector}, fixture.Interceptor{})
		recordedConnector, err := wrapped.(driver.DriverContext).OpenConnector("")
		if err != nil {
			t.Fatal(err)
		}
		freshRecorded := sql.OpenDB(recordedConnector)
		defer freshRecorded.Close()
		for _, pools := range []struct{ control, observe *sql.DB }{{fresh, freshRecorded}, {p.Native, b.DB}} {
			target, observe := pools.control, pools.observe
			if err := stuntgres.Reset(p.Ctx, target); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			checkState := func() {
				for _, s := range state {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, observe, p.MemoryRecord, s)
				}
				fixture.TableDDLMetadata(t, p.Ctx, observe, false, prepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, []bool{false, true}, 1)
				fixture.CompositeReferenceRows(t, p, b, observe, "SELECT v FROM holder", [][]any{{"(41,nest)"}}, prepared)
			}
			checkState()
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, target, &redump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, target); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(redump.Bytes())); err != nil {
				t.Fatal(err)
			}
			checkState()
			for _, suffix := range []string{"; CREATE TABLE late OF missing", "; CREATE TABLE late OF pair(id integer)", "; CREATE TABLE t(id integer)", "; INSERT INTO t VALUES(-1,'bad')", "; INSERT INTO t VALUES('bad','late')"} {
				if err := stuntgres.Restore(p.Ctx, target, strings.NewReader(dump.String()+suffix)); err == nil {
					t.Fatalf("invalid restore accepted %q", suffix)
				}
				checkState()
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, target, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			checkState()
			if err := stuntgres.Restore(p.Ctx, target, typedBrokenReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			checkState()
			fixture.RunStep(t, p.Ctx, observe, p.MemoryRecord, fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
			fixture.RunStep(t, p.Ctx, observe, p.MemoryRecord, typedTablePairRows("t", [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
		}
		values := make([]driver.Value, 2)
		if err := held.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{int64(11), "hey"}) {
			t.Fatal(values, err)
		}
		if err := held.Next(values); err != io.EOF {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(held.Columns(), []string{"id", "note"}) {
			t.Fatal(held.Columns())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual native emitted typed SQL replay", err)
			}
			for _, s := range state {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, typedTablePairRows("t", [][]any{{int64(7), "ok"}, {int64(11), "hey"}}))
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

type typedBrokenReader struct{}

func (typedBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
