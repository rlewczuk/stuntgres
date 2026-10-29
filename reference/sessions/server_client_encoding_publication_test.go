//go:build !noservertests

package reference_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingQueryHeapPreservation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			writer := serverAuthenticated(t, dsn)
			command := func(w *serverWire, ready byte, query string, tags ...string) {
				t.Helper()
				frames := clientEncodingCommand(t, w, query, tags...)
				serverWantTags(t, frames, strings.Repeat("C", len(tags))+"Z")
				serverWantReady(t, frames, ready)
			}
			command(writer, 'I', `CREATE TABLE ce_heap_publication(v TEXT)`, "CREATE TABLE")
			command(writer, 'I', `INSERT INTO ce_heap_publication VALUES('Я')`, "INSERT 0 1")
			peer, err := b.DB.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			fixture.RunStep(t, f.Ctx, peer, b.Recorder, fixture.Exec(`CREATE TEMP TABLE ce_heap_peer_temp(v TEXT)`, 0))
			fixture.RunStep(t, f.Ctx, peer, b.Recorder, fixture.Exec(`INSERT INTO ce_heap_peer_temp VALUES('peer-kept')`, 1))
			const query = `SELECT v FROM ce_heap_publication ORDER BY v`
			stmt, err := peer.PrepareContext(f.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: query}
			read := func(want string) {
				t.Helper()
				step := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{want}})
				fixture.RunStep(t, f.Ctx, peer, b.Recorder, step)
				fixture.RunStep(t, f.Ctx, held, b.Recorder, step)
			}
			fresh := func(want string) {
				t.Helper()
				w := serverAuthenticated(t, dsn)
				defer w.Close()
				frames := w.query(t, query)
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "TDCZ")
				serverWantReady(t, frames, 'I')
				serverWantCompletions(t, frames, "SELECT 1")
				serverWantRaw(t, frames, [][][]byte{{[]byte(want)}})
				columns := serverColumns(t, frames)
				if len(columns) != 1 || columns[0].name != "v" || columns[0].oid != 25 || columns[0].format != 0 || columns[0].typmod != -1 {
					t.Fatal("fresh physical reader lost TEXT metadata", columns)
				}
			}
			newOwner := func() (*serverWire, string) {
				t.Helper()
				w := serverAuthenticated(t, dsn)
				command(w, 'I', `CREATE COLLATION pg_temp.ce_heap_coll FROM pg_catalog."C"`, "CREATE COLLATION")
				command(w, 'I', `CREATE TEMP TABLE ce_heap_owned(v TEXT)`, "CREATE TABLE")
				command(w, 'I', `INSERT INTO ce_heap_owned VALUES('retire')`, "INSERT 0 1")
				frames := w.query(t, `SELECT pg_my_temp_schema()::regnamespace::text`)
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "TDCZ")
				serverWantReady(t, frames, 'I')
				rows := serverRawRows(t, frames)
				if len(rows) != 1 || len(rows[0]) != 1 || len(rows[0][0]) == 0 {
					t.Fatal("missing actual owner TEMP namespace")
				}
				return w, string(rows[0][0])
			}
			closeOwner := func(w *serverWire, namespace string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
				defer cancel()
				w.Close()
				if b.PG {
					serverPipelineWaitGone(t, ctx, f.RawPool(t, b), w.pid)
				} else {
					serverPipelineWaitClosed(t, ctx, filepath.Join(f.Logdir, "server.log"), w.pid)
				}
				// Namespace tuples may remain; only the ending owner's objects
				// must retire. The generated namespace is observed, never pinned.
				fixture.RunStep(t, f.Ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='ce_heap_owned'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
				fixture.RunStep(t, f.Ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_collation c JOIN pg_namespace n ON n.oid=c.collnamespace WHERE n.nspname=$1 AND c.collname='ce_heap_coll'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
				fixture.RunStep(t, f.Ctx, peer, b.Recorder, fixture.Select(`SELECT v FROM ce_heap_peer_temp`, []string{"v"}, []string{"TEXT"}, [][]any{{"peer-kept"}}))
			}

			owner, namespace := newOwner()
			read("Я")
			command(writer, 'T', `BEGIN`, "BEGIN")
			command(writer, 'T', `TRUNCATE ce_heap_publication; INSERT INTO ce_heap_publication VALUES('фоо')`, "TRUNCATE TABLE", "INSERT 0 1")
			command(writer, 'I', `COMMIT`, "COMMIT")
			read("фоо")
			command(writer, 'I', `TRUNCATE ce_heap_publication; INSERT INTO ce_heap_publication VALUES('committed-simple')`, "TRUNCATE TABLE", "INSERT 0 1")
			read("committed-simple")
			command(writer, 'T', `BEGIN`, "BEGIN")
			command(writer, 'T', `TRUNCATE ce_heap_publication; INSERT INTO ce_heap_publication VALUES('not-published')`, "TRUNCATE TABLE", "INSERT 0 1")
			command(writer, 'I', `ROLLBACK`, "ROLLBACK")
			read("committed-simple")
			command(owner, 'T', `BEGIN`, "BEGIN")
			command(owner, 'T', `TRUNCATE ce_heap_publication; INSERT INTO ce_heap_publication VALUES('close-must-rollback')`, "TRUNCATE TABLE", "INSERT 0 1")
			closeOwner(owner, namespace)
			read("committed-simple")
			fresh("committed-simple")

			owner, namespace = newOwner()
			command(writer, 'I', `TRUNCATE ce_heap_publication; INSERT INTO ce_heap_publication VALUES('фоо')`, "TRUNCATE TABLE", "INSERT 0 1")
			closeOwner(owner, namespace)
			read("фоо")
			fresh("фоо")
		})
	}
}
