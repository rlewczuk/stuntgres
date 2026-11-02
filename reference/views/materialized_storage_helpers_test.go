package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Every route owns a fresh scenario. A held route retains real Conn-owned
// statements across metadata changes, failures, transactions and recovery.
func materializedStorageRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	materializedStorageRoutesMode(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), held bool) {
		body(t, p, b, c, run)
	})
}

func materializedStorageRoutesMode(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			cleanup := fixture.AccessMethodReferenceCleanup(t, []string{"ms_heap2", "ms_heap3"})
			fixture.RoleReferenceCleanup(t, []string{"ms_owner", "ms_other", "ms_member", "ms_reader", "current_user"})
			fixture.SchemaReferenceCleanup(t, "ms_shadow")
			p := fixture.NewScenario(t)
			cleanup()
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					t.Run("scenario", func(t *testing.T) {
						statements := map[string]*sql.Stmt{}
						defer func() {
							for _, s := range statements {
								s.Close()
							}
						}()
						run := func(s fixture.Step) {
							t.Helper()
							var executor fixture.Executor = c
							if held {
								statement := statements[s.Query]
								if statement == nil {
									statement, err = c.PrepareContext(p.Ctx, s.Query)
									if err != nil {
										t.Fatalf("effect-free Prepare %q: %v", s.Query, err)
									}
									statements[s.Query] = statement
								}
								executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: s.Query}
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						}
						body(t, p, b, c, run, held)
					})
				})
			}
		})
	}
}

func materializedStorageSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE ms_source(id INT,note TEXT)", 0))
	run(fixture.Exec("INSERT INTO ms_source VALUES(2,'two'),(1,NULL),(1,NULL)", 3))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_mv AS SELECT id,note FROM ms_source", 3))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_empty AS SELECT id,note FROM ms_source WHERE false", 0))
	run(fixture.Exec("CREATE MATERIALIZED VIEW ms_no AS SELECT id,note FROM ms_source WITH NO DATA", 0))
	run(fixture.Exec("CREATE INDEX ms_a ON ms_mv(id)", 0))
	run(fixture.Exec("CREATE INDEX ms_b ON ms_mv(note) INCLUDE(id)", 0))
}

func materializedStorageRows() fixture.Step {
	s := fixture.Select("SELECT id,note FROM ms_mv", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), "two"}})
	s.Bag = true
	return s
}

func materializedStorageColumns(target any, options any, storage, compression string) fixture.Step {
	return fixture.Select("SELECT attname,attstattarget,attoptions,attstorage,attcompression FROM pg_attribute WHERE attrelid='ms_mv'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attstattarget", "attoptions", "attstorage", "attcompression"}, []string{"NAME", "INT2", "_TEXT", "CHAR", "CHAR"}, [][]any{{"id", target, options, "p", ""}, {"note", nil, nil, storage, compression}})
}

func materializedStorageOptions(options any) fixture.Step {
	return fixture.Select("SELECT reloptions FROM pg_class WHERE oid='ms_mv'::regclass", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{options}})
}

func materializedStorageCluster(a, b bool) fixture.Step {
	return fixture.Select("SELECT c.relname,i.indisclustered FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ms_mv'::regclass AND c.relname IN ('ms_a','ms_b') ORDER BY c.relname", []string{"relname", "indisclustered"}, []string{"NAME", "BOOL"}, [][]any{{"ms_a", a}, {"ms_b", b}})
}

func materializedStorageMethod(name string) fixture.Step {
	return fixture.Select("SELECT a.amname,c.reltablespace::bigint,c.relkind,c.relispopulated FROM pg_class c JOIN pg_am a ON a.oid=c.relam WHERE c.oid='ms_mv'::regclass", []string{"amname", "reltablespace", "relkind", "relispopulated"}, []string{"NAME", "INT8", "CHAR", "BOOL"}, [][]any{{name, int64(0), "m", true}})
}

func materializedStorageBaseline(run func(fixture.Step)) {
	run(materializedStorageColumns(nil, nil, "x", ""))
	run(materializedStorageOptions(nil))
	run(materializedStorageCluster(false, false))
	run(materializedStorageMethod("heap"))
	run(materializedStorageRows())
}

func materializedStorageRoles(run func(fixture.Step)) {
	for _, role := range []string{"ms_owner", "ms_other", "ms_member", "ms_reader"} {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ms_owner,ms_other,ms_member,ms_reader", 0))
	run(fixture.Exec("GRANT ms_owner TO ms_member", 0))
}
