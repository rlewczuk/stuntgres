package reference_test

import (
	"bytes"
	"database/sql/driver"
	"github.com/lib/pq"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningControlsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			db := backend.DB
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, db, backend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SEQUENCE s START 101 CACHE 1", 0),
				fixture.Exec("CREATE TABLE t(id INT NOT NULL DEFAULT 9 CHECK(id>0),b BYTEA,n NUMERIC(8,2) DEFAULT 12.30,v VARCHAR(7) DEFAULT 'word')", 0),
				fixture.Exec("COMMENT ON TABLE t IS 'retained'", 0),
				fixture.Exec("CREATE VIEW live AS SELECT id,b FROM t", 0),
				fixture.Exec(`INSERT INTO t(id,b) VALUES(1,'\x0102'),(2,'\x0304'),(2,'\x0304')`, 3),
			} {
				run(s)
			}
			var connector driver.Connector = p.NativeConnector
			if backend.PG {
				var err error
				connector, err = pq.NewConnector(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := connector.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			exec := func(q string) {
				t.Helper()
				if _, e := raw.(driver.ExecerContext).ExecContext(ctx, q, nil); e != nil {
					t.Fatal(e)
				}
			}
			exec("BEGIN")
			saved, err := raw.(driver.QueryerContext).QueryContext(ctx, `UPDATE t SET b='\x0506' WHERE id=2 RETURNING old.b,new.b,nextval('s')`, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer saved.Close()
			drain := func() {
				t.Helper()
				if !reflect.DeepEqual(saved.Columns(), []string{"b", "b", "nextval"}) {
					t.Fatal(saved.Columns())
				}
				dst := make([]driver.Value, 3)
				for _, draw := range []int64{101, 102} {
					if err = saved.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{[]byte{3, 4}, []byte{5, 6}, draw}) {
						t.Fatal(dst, err)
					}
					dst[0].([]byte)[0] = 99
					dst[1].([]byte)[0] = 99
				}
				if err = saved.Next(dst); err != io.EOF {
					t.Fatal(err)
				}
				if err = saved.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// lib/pq's streaming connection must finish its result before another
			// command. Native eager owned output is retained through replacement.
			if backend.PG {
				drain()
			}
			var private bytes.Buffer
			if !backend.PG {
				if err = stuntgres.Dump(ctx, p.Native, &private); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(private.String(), "0506") || !strings.Contains(private.String(), "0304") {
					t.Fatal("private replacement leaked", private.String())
				}
			}
			exec("ROLLBACK")
			run(fixture.Select("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\x01\x02"}, {int64(2), "\x03\x04"}, {int64(2), "\x03\x04"}}))
			exec("BEGIN")
			exec(`UPDATE t SET b='\x0708' WHERE id=2 RETURNING old.b,new.b`)
			exec("COMMIT")
			if !backend.PG {
				var dump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{"retained", "NOT NULL", "DEFAULT", "CHECK", "live"} {
					if !strings.Contains(dump.String(), text) {
						t.Fatal("missing persistent object", text, dump.String())
					}
				}
				if err = stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Select("SELECT id,b FROM live ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\x01\x02"}, {int64(2), "\x07\x08"}, {int64(2), "\x07\x08"}}))
			run(fixture.Select("UPDATE t SET id=id WHERE id=1 RETURNING old.n,new.n,old.v,new.v", []string{"n", "n", "v", "v"}, []string{"NUMERIC", "NUMERIC", "VARCHAR", "VARCHAR"}, [][]any{{"12.30", "12.30", "word", "word"}}))
			rows, err := db.QueryContext(ctx, "UPDATE t SET id=id WHERE FALSE RETURNING old.n,new.n,old.v,new.v")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range []int{0, 1} {
				if precision, scale, known := metadata[i].DecimalSize(); !known || precision != 8 || scale != 2 {
					t.Fatal(i, precision, scale, known)
				}
			}
			for _, i := range []int{2, 3} {
				if length, known := metadata[i].Length(); !known || length != 7 {
					t.Fatal(i, length, known)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("empty restored metadata query", rows.Err())
			}
			rows.Close()
			if !backend.PG {
				drain()
			}
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(103)}}))
			if !backend.PG {
				if err = stuntgres.Restore(ctx, p.Native, strings.NewReader("UPDATE t SET id=1 RETURNING *;")); err == nil {
					t.Fatal("Restore accepted mutation history")
				}
			}
			run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}))
			run(fixture.Exec("INSERT INTO t(b) VALUES(NULL)", 1))
			run(fixture.Select("SELECT id FROM t WHERE id=9", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}
