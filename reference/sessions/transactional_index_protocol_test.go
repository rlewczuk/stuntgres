//go:build !noservertests

package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const transactionalIndexWireSQL = `CREATE INDEX "flyway_schema_history_s_idx" ON "public"."flyway_schema_history" ("success")`
const transactionalIndexWireCount = `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history_s_idx'`

func TestReferenceTransactionalIndexProtocol(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			defer func() {
				serverWantCode(t, w.query(t, "ROLLBACK"), "")
				serverWantCode(t, peer.query(t, "ROLLBACK"), "")
			}()
			command := func(w *serverWire, q, tag string, status byte) {
				frames := w.query(t, q)
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, tag)
				serverWantReady(t, frames, status)
			}
			checkIndex := func(w *serverWire) string {
				serverScalar(t, w, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_index i ON i.indexrelid=c.oid WHERE n.nspname='public' AND c.relname='flyway_schema_history_s_idx' AND c.relkind='i' AND i.indrelid='public.flyway_schema_history'::regclass AND NOT i.indisunique AND i.indisvalid AND i.indisready`, "1")
				serverWantValues(t, w.query(t, `SELECT schemaname,tablename,indexname,tablespace,indexdef FROM pg_indexes WHERE schemaname='public' AND indexname='flyway_schema_history_s_idx'`), [][]string{{"public", "flyway_schema_history", "flyway_schema_history_s_idx", "<NULL>", "CREATE INDEX flyway_schema_history_s_idx ON public.flyway_schema_history USING btree (success)"}})
				serverScalar(t, w, `SELECT pg_get_indexdef('public.flyway_schema_history_s_idx'::regclass)`, "CREATE INDEX flyway_schema_history_s_idx ON public.flyway_schema_history USING btree (success)")
				frames := w.query(t, `SELECT 'public.flyway_schema_history_s_idx'::regclass::oid::bigint`)
				serverWantCode(t, frames, "")
				values := serverValues(t, frames)
				if len(values) != 1 || len(values[0]) != 1 || values[0][0] == "0" {
					t.Fatalf("invalid index identity: %v", values)
				}
				return values[0][0]
			}
			for _, route := range []string{"simple", "held_before", "held_inside"} {
				for _, private := range []bool{false, true} {
					for _, commit := range []bool{false, true} {
						name := fmt.Sprintf("index_%s_%t_%t", route, private, commit)
						if !private {
							command(w, `CREATE TABLE public.flyway_schema_history(success BOOLEAN NOT NULL)`, "CREATE TABLE", 'I')
							command(w, `INSERT INTO public.flyway_schema_history VALUES(true),(false)`, "INSERT 0 2", 'I')
						}
						if route == "held_before" {
							frames := serverCycle(t, w, serverParse(name, transactionalIndexWireSQL), serverDescribe('S', name))
							serverWantTags(t, frames, "1tnZ")
							serverWantReady(t, frames, 'I')
							serverScalar(t, w, transactionalIndexWireCount, "0")
							serverScalar(t, peer, transactionalIndexWireCount, "0")
						}
						if route == "simple" {
							command(w, "BEGIN", "BEGIN", 'T')
						} else {
							frames := serverCycle(t, w, serverParse(name+"_begin", "BEGIN"), serverBind(name+"_begin", name+"_begin", nil, nil), serverExecute(name+"_begin", 0))
							serverWantTags(t, frames, "12CZ")
							serverWantCompletions(t, frames, "BEGIN")
							serverWantReady(t, frames, 'T')
						}
						if private {
							command(w, transactionalSchemaWireFlyway, "CREATE TABLE", 'T')
							command(w, `INSERT INTO public.flyway_schema_history(installed_rank,description,type,script,installed_by,execution_time,success) VALUES(1,'first','SQL','V1.sql','tester',3,true),(2,'second','SQL','V2.sql','tester',4,false)`, "INSERT 0 2", 'T')
						}
						if route != "simple" {
							var frames []serverFrame
							if route == "held_inside" {
								frames = serverCycle(t, w, serverParse(name, transactionalIndexWireSQL), serverDescribe('S', name), serverBind(name, name, nil, nil), serverDescribe('P', name))
								serverWantTags(t, frames, "1tn2nZ")
							} else {
								frames = serverCycle(t, w, serverBind(name, name, nil, nil), serverDescribe('P', name))
								serverWantTags(t, frames, "2nZ")
							}
							serverWantReady(t, frames, 'T')
							serverScalar(t, w, transactionalIndexWireCount, "0")
							serverScalar(t, peer, transactionalIndexWireCount, "0")
							serverScalar(t, w, `SELECT count(*) FROM pg_locks WHERE locktype='relation' AND mode='ShareLock' AND relation IN (SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history')`, "0")
							frames = serverCycle(t, w, serverExecute(name, 0))
							serverWantTags(t, frames, "CZ")
							serverWantCompletions(t, frames, "CREATE INDEX")
							serverWantReady(t, frames, 'T')
						} else {
							command(w, transactionalIndexWireSQL, "CREATE INDEX", 'T')
						}
						oid := checkIndex(w)
						serverWantValues(t, w.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"t"}})
						serverScalar(t, peer, transactionalIndexWireCount, "0")
						serverScalar(t, peer, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname='flyway_schema_history_s_idx'`, "0")
						if private {
							serverScalar(t, peer, `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'`, "0")
						} else {
							serverWantValues(t, peer.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"t"}})
						}
						if commit {
							command(w, "COMMIT", "COMMIT", 'I')
							if got := checkIndex(peer); got != oid {
								t.Fatalf("commit identity %s want %s", got, oid)
							}
							serverWantValues(t, peer.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"t"}})
						} else {
							command(w, "ROLLBACK", "ROLLBACK", 'I')
							serverScalar(t, peer, transactionalIndexWireCount, "0")
							if private {
								serverScalar(t, peer, `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'`, "0")
							} else {
								serverWantValues(t, peer.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"t"}})
							}
						}
						if commit || !private {
							command(w, "DROP TABLE public.flyway_schema_history", "DROP TABLE", 'I')
						}
					}
				}
			}
			command(w, `CREATE TABLE public.flyway_schema_history(success BOOLEAN NOT NULL)`, "CREATE TABLE", 'I')
			command(w, `INSERT INTO public.flyway_schema_history VALUES(true),(false)`, "INSERT 0 2", 'I')
			command(w, `CREATE TABLE index_wire_later(v INT)`, "CREATE TABLE", 'I')
			command(w, "BEGIN", "BEGIN", 'T')
			command(w, transactionalIndexWireSQL, "CREATE INDEX", 'T')
			command(w, `INSERT INTO public.flyway_schema_history VALUES(true)`, "INSERT 0 1", 'T')
			command(w, "SAVEPOINT keep", "SAVEPOINT", 'T')
			command(w, `CREATE INDEX index_wire_later_idx ON index_wire_later(v)`, "CREATE INDEX", 'T')
			command(w, `INSERT INTO index_wire_later VALUES(7)`, "INSERT 0 1", 'T')
			command(w, "ROLLBACK TO keep", "ROLLBACK", 'T')
			checkIndex(w)
			serverScalar(t, w, `SELECT count(*) FROM public.flyway_schema_history`, "3")
			serverScalar(t, w, `SELECT count(*) FROM index_wire_later`, "0")
			serverScalar(t, w, `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='index_wire_later_idx'`, "0")
			command(w, `CREATE INDEX index_wire_later_idx ON index_wire_later(v)`, "CREATE INDEX", 'T')
			command(w, "ROLLBACK TO keep", "ROLLBACK", 'T')
			command(w, `CREATE INDEX index_wire_later_idx ON index_wire_later(v)`, "CREATE INDEX", 'T')
			command(w, "RELEASE keep", "RELEASE", 'T')
			command(w, "ROLLBACK", "ROLLBACK", 'I')
			serverScalar(t, peer, transactionalIndexWireCount, "0")
			serverScalar(t, peer, `SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='index_wire_later_idx'`, "0")
			serverWantValues(t, peer.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"t"}})
			command(peer, "BEGIN", "BEGIN", 'T')
			command(peer, `INSERT INTO public.flyway_schema_history VALUES(true)`, "INSERT 0 1", 'T')
			command(w, "BEGIN", "BEGIN", 'T')
			command(w, "SAVEPOINT recover", "SAVEPOINT", 'T')
			command(w, "SET LOCAL lock_timeout='100ms'", "SET", 'T')
			frames := serverCycle(t, w, serverParse("timeout", transactionalIndexWireSQL), serverDescribe('S', "timeout"), serverBind("timeout", "timeout", nil, nil))
			serverWantTags(t, frames, "1tn2Z")
			serverWantReady(t, frames, 'T')
			frames = serverCycle(t, w, serverExecute("timeout", 0))
			serverWantTags(t, frames, "EZ")
			serverNotificationError(t, frames, "55P03", "canceling statement due to lock timeout", "")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "SELECT 1")
			serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantReady(t, frames, 'E')
			command(w, "ROLLBACK TO recover", "ROLLBACK", 'T')
			serverScalar(t, w, transactionalIndexWireCount, "0")
			command(peer, "COMMIT", "COMMIT", 'I')
			command(w, transactionalIndexWireSQL, "CREATE INDEX", 'T')
			checkIndex(w)
			serverScalar(t, peer, transactionalIndexWireCount, "0")
			command(w, "COMMIT", "COMMIT", 'I')
			checkIndex(peer)
			command(peer, `INSERT INTO public.flyway_schema_history VALUES(false)`, "INSERT 0 1", 'I')
			serverWantValues(t, peer.query(t, `SELECT success FROM public.flyway_schema_history ORDER BY success`), [][]string{{"f"}, {"f"}, {"t"}, {"t"}})
			command(w, "DROP TABLE public.flyway_schema_history", "DROP TABLE", 'I')
			command(w, "DROP TABLE index_wire_later", "DROP TABLE", 'I')
		})
	}
}
