package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These values narrow real SQL-backed implementations to their actual host
// modification interfaces. They do not return canned foreign rows.
type ivForeignUpdateDelete struct {
	fdwWriterBase
	stuntgres.ForeignUpdater
	stuntgres.ForeignDeleter
	stuntgres.ForeignUpdateTargetProvider
}

func TestReferenceInfoViewsForeignCapabilities(t *testing.T) {
	for _, tc := range []struct{ name, update, insert string }{{"readonly", "NO", "NO"}, {"insert", "NO", "YES"}, {"update_delete", "YES", "NO"}, {"all", "YES", "YES"}} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "fdw_source")
			reader := newFDWSQLReader(t)
			writer := &fdwSQLWriter{fdwSQLReader: reader, batchSize: 2}
			reader.writer = writer
			p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				if err := reader.configure(d); err != nil {
					return err
				}
				base := fdwWriterBase{ForeignDataWrapper: writer, ForeignTransactionProvider: writer}
				var wrapper stuntgres.ForeignDataWrapper = writer
				switch tc.name {
				case "readonly":
					wrapper = fdwScanOnly{reader}
				case "insert":
					wrapper = fdwSingleWriter{fdwWriterBase: base, ForeignInserter: writer}
				case "update_delete":
					wrapper = ivForeignUpdateDelete{fdwWriterBase: base, ForeignUpdater: writer, ForeignDeleter: writer, ForeignUpdateTargetProvider: writer}
				}
				return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "iv_fdw_handler"}, Wrapper: wrapper})
			})
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer fdwCleanup(t, c)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					fdwLive(t, p, b, reader, run)
					for _, q := range []string{"CREATE VIEW iv_foreign AS SELECT id,label FROM fdw_data", "CREATE VIEW iv_foreign_inner AS SELECT id,id+1 AS computed FROM fdw_data", "CREATE VIEW iv_foreign_direct AS SELECT id FROM iv_foreign_inner", "CREATE VIEW iv_foreign_computed AS SELECT computed FROM iv_foreign_inner"} {
						run(fixture.Exec(q, 0))
					}
					query := "SELECT table_name,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='public' AND table_name=$1"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					check := func(update, insert string) {
						for _, name := range []string{"iv_foreign", "iv_foreign_direct", "iv_foreign_computed"} {
							u, i := update, insert
							if name == "iv_foreign_computed" {
								u, i = "NO", "NO"
							}
							s := fixture.Select(query, []string{"table_name", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{name, u, i, "NO", "NO", "NO"}}, name)
							run(s)
							run(fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					}
					// Shared SQL baseline uses real postgres_fdw or the all-write
					// SQL-backed native handler. Host registration has no PG API.
					check("YES", "YES")
					if !b.PG {
						run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER iv_fdw_handler", 0))
						reader.mu.Lock()
						reader.hook = func(context.Context, string) error { return fmt.Errorf("metadata must not execute a foreign callback") }
						before := reader.calls
						reader.mu.Unlock()
						defer func() { reader.mu.Lock(); reader.hook = nil; reader.mu.Unlock() }()
						check(tc.update, tc.insert)
						reader.mu.Lock()
						after := reader.calls
						reader.mu.Unlock()
						if after != before {
							t.Fatalf("metadata executed foreign callbacks: %d -> %d", before, after)
						}
					}
				})
			}
		})
	}
}
