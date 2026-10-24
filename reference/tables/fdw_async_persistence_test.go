package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAsyncPersistence(t *testing.T) {
	fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
		c.release()
		c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
		options := fixture.Select("SELECT srvname,srvoptions FROM pg_foreign_server WHERE srvname IN ('async_slow_server','async_ready_server') ORDER BY srvname", []string{"srvname", "srvoptions"}, []string{"NAME", "_TEXT"}, [][]any{{"async_ready_server", "{async_capable=true}"}, {"async_slow_server", "{async_capable=true}"}})
		if !c.b.PG {
			c.run(t, options)
			_, _, before := c.reader.counts()
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if _, _, after := c.reader.counts(); after != before {
				t.Fatal("Dump started real remote requests")
			}
			if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if _, _, after := c.reader.counts(); after != before {
				t.Fatal("Restore started real remote requests")
			}
			c.run(t, options)
			c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
			for _, registered := range []bool{false, true} {
				d := stuntgres.NewDriver()
				if registered {
					if err := c.reader.configure(d); err != nil {
						t.Fatal(err)
					}
				}
				connector, err := d.OpenConnector("async-restore")
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				defer db.Close()
				pool := fdwRecordedPool(t, connector)
				defer pool.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, pool, c.p.MemoryRecord, s) }
				run(fixture.Exec("CREATE TABLE sentinel(id integer)", 0))
				run(fixture.Exec("INSERT INTO sentinel VALUES(99)", 1))
				_, _, before := c.reader.counts()
				err = stuntgres.Restore(c.p.Ctx, db, bytes.NewReader(dump.Bytes()))
				if _, _, after := c.reader.counts(); after != before {
					t.Fatal("Restore opened remote SQL/HTTP frontier")
				}
				if !registered {
					if err == nil {
						t.Fatal("missing handler Restore succeeded")
					}
					run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				} else {
					if err != nil {
						t.Fatal(err)
					}
					run(options)
					run(fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
					bad := append(append([]byte{}, dump.Bytes()...), []byte("\nCREATE TABLE invalid(x no_async_type);\n")...)
					if err := stuntgres.Restore(c.p.Ctx, db, bytes.NewReader(bad)); err == nil {
						t.Fatal("invalid later Restore succeeded")
					}
					run(options)
					run(fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
				}
			}
		}
		c.idle(t, true)
	})
}
