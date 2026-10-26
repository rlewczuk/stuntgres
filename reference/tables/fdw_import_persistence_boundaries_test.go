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
				inquiry, name, table, key, acl, ownerColumn := "has_server_privilege", "fdw_live", "pg_foreign_server", "srvname", "srvacl", "srvowner"
				if kind == "wrapper" {
					inquiry, name, table, key, acl, ownerColumn = "has_foreign_data_wrapper_privilege", wrapper, "pg_foreign_data_wrapper", "fdwname", "fdwacl", "fdwowner"
				}
				var owner string
				if kind == "relation" || kind == "column" {
					if err := c.QueryRowContext(p.Ctx, "SELECT relowner::regrole::text FROM pg_class WHERE oid='fdw_import.import_a'::regclass").Scan(&owner); err != nil {
						t.Fatal(err)
					}
				} else if err := c.QueryRowContext(p.Ctx, "SELECT "+ownerColumn+"::regrole::text FROM "+table+" WHERE "+key+"=$1", name).Scan(&owner); err != nil {
					t.Fatal(err)
				}
				checkACL := func() {
					if kind == "relation" || kind == "column" {
						run(fixture.Select("SELECT has_table_privilege('fdw_reader','fdw_import.import_a','SELECT') AS whole,has_column_privilege('fdw_reader','fdw_import.import_a','id','SELECT') AS id,has_column_privilege('fdw_reader','fdw_import.import_a','label','SELECT') AS label", []string{"whole", "id", "label"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{kind == "relation", true, kind == "relation"}}))
						relationACL, columnACL := any("{"+owner+"=arwdDxtm/"+owner+",fdw_reader=r/"+owner+"}"), any(nil)
						if kind == "column" {
							relationACL, columnACL = nil, "{fdw_reader=r/"+owner+"}"
						}
						run(fixture.Select("SELECT relacl::text AS acl FROM pg_class WHERE oid='fdw_import.import_a'::regclass", []string{"acl"}, []string{"TEXT"}, [][]any{{relationACL}}))
						run(fixture.Select("SELECT attacl::text AS acl FROM pg_attribute WHERE attrelid='fdw_import.import_a'::regclass AND attname='id'", []string{"acl"}, []string{"TEXT"}, [][]any{{columnACL}}))
						return
					}
					run(fixture.Select("SELECT "+inquiry+"('fdw_reader',$1::text,'USAGE') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, name))
					s := fixture.Select("SELECT unnest("+acl+")::text AS acl FROM "+table+" WHERE "+key+"=$1", []string{"acl"}, []string{"TEXT"}, [][]any{{owner + "=U/" + owner}, {"fdw_reader=U/" + owner}}, name)
					s.Bag = true
					run(s)
				}
				checkACL()
				fdwImportValues(t, p, b, c, run)
				if b.PG {
					return
				} // Native controls have no PostgreSQL API counterpart.
				graph := fdwGraphSnapshot(t, p.Ctx, c)
				r.setHook(func(context.Context, string) error { return errors.New("ACL Dump called provider") })
				defer r.setHook(nil)
				before := r.snapshot()
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Len() == 0 || r.snapshot() != before {
					t.Fatal("ACL Dump omitted bytes or called provider")
				}
				fdwAssertGraph(t, p.Ctx, c, graph)
				r.setHook(nil)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				checkACL()
				fdwAssertGraph(t, p.Ctx, c, graph)
				fdwImportValues(t, p, b, c, run)
			})
		})
	}
}
