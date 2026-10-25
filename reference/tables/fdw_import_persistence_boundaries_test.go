package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWImportPersistence(t *testing.T) {
	t.Run("graph", fdwImportPersistenceGraph)
	for _, kind := range []string{"wrapper", "server", "relation", "column"} {
		t.Run("acl-"+kind, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwImportSource(t, p, b, c, r, run)
				run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a,import_b) FROM SERVER fdw_live INTO fdw_import", 0))
				run(fixture.Exec("CREATE ROLE fdw_reader", 0))
				wrapper := "fdw_native"
				if b.PG {
					wrapper = "postgres_fdw"
				}
				q := map[string]string{
					"wrapper":  "GRANT USAGE ON FOREIGN DATA WRAPPER " + wrapper + " TO fdw_reader",
					"server":   "GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader",
					"relation": "GRANT SELECT ON fdw_import.import_a TO fdw_reader",
					"column":   "GRANT SELECT(id) ON fdw_import.import_a TO fdw_reader",
				}[kind]
				run(fixture.Exec(q, 0))
				checkACL:=func(){}
				if kind=="wrapper" || kind=="server" {
					inquiry,name,table,key,acl,ownerColumn:="has_server_privilege","fdw_live","pg_foreign_server","srvname","srvacl","srvowner"
					if kind=="wrapper" {inquiry,name,table,key,acl,ownerColumn="has_foreign_data_wrapper_privilege",wrapper,"pg_foreign_data_wrapper","fdwname","fdwacl","fdwowner"}
					var owner string
					if err:=c.QueryRowContext(p.Ctx,"SELECT "+ownerColumn+"::regrole::text FROM "+table+" WHERE "+key+"=$1",name).Scan(&owner);err!=nil {t.Fatal(err)}
					checkACL=func(){
						run(fixture.Select("SELECT "+inquiry+"('fdw_reader',$1::text,'USAGE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}},name))
						s:=fixture.Select("SELECT unnest("+acl+")::text AS acl FROM "+table+" WHERE "+key+"=$1",[]string{"acl"},[]string{"TEXT"},[][]any{{owner+"=U/"+owner},{"fdw_reader=U/"+owner}},name)
						s.Bag=true;run(s)
					}
					checkACL()
				}
				if b.PG {
					return
				} // Host-authorized native Dump's existing ACL limitation.
				if kind == "wrapper" || kind == "server" {
					var saved bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
						t.Fatal(err)
					}
					checkACL()
					return
				}
				graph := fdwGraphSnapshot(t, p.Ctx, c)
				r.setHook(func(context.Context, string) error { return errors.New("ACL preflight called provider") })
				defer r.setHook(nil)
				before := r.snapshot()
				var out bytes.Buffer
				out.WriteString("sentinel")
				want := "stuntgres: dump cannot serialize explicit relation or column ACL"
				fixture.TransactionOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, &out), want)
				if out.String() != "sentinel" || r.snapshot() != before {
					t.Fatal("ACL rejection emitted bytes or called provider")
				}
				fdwAssertGraph(t, p.Ctx, c, graph)
			})
		})
	}
}
