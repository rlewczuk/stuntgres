package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTransactionalSchemaNamespaceConflicts(t *testing.T) {
	for _, tc := range []struct{ name, setup, first, second string }{
		{"table_same_name", "CREATE SCHEMA tx_ns", "CREATE TABLE tx_ns.same(v INT)", "CREATE TABLE tx_ns.same(v INT)"},
		{"schema_same_name", "", "CREATE SCHEMA tx_ns", "CREATE SCHEMA tx_ns"},
		{"namespace_drop", "CREATE SCHEMA tx_ns", "DROP SCHEMA tx_ns", "CREATE TABLE tx_ns.later(v INT)"},
		{"namespace_rename", "CREATE SCHEMA tx_ns", "ALTER SCHEMA tx_ns RENAME TO tx_ns_new", "CREATE TABLE tx_ns.later(v INT)"},
		{"rename_destination", "CREATE TABLE tx_ns_source(v INT)", "CREATE TABLE tx_ns_target(v INT)", "ALTER TABLE tx_ns_source RENAME TO tx_ns_target"},
		{"move_destination", "CREATE SCHEMA tx_ns; CREATE TABLE public.tx_ns_source(v INT)", "CREATE TABLE tx_ns.tx_ns_source(v INT)", "ALTER TABLE public.tx_ns_source SET SCHEMA tx_ns"},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprint(tc.name, "/commit=", commit), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tx_ns", "tx_ns_new")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						if tc.setup != "" {
							if _, err := a.ExecContext(p.Ctx, tc.setup); err != nil {
								t.Fatal(err)
							}
						}
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_ns_unrelated(v INT)", 0)
						ap, bp := transactionalSchemaPID(t, p, b, a), transactionalSchemaPID(t, p, b, c)
						var namespace int64
						if tc.name != "schema_same_name" {
							ns := "tx_ns"
							if tc.name == "rename_destination" {
								ns = "public"
							}
							if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname=$1", ns).Scan(&namespace); err != nil {
								t.Fatal(err)
							}
						}
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, a, tc.first, 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_ns_unrelated VALUES(1)", 1)
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						done := make(chan error, 1)
						joined := false
						defer func() {
							cancel()
							if !joined {
								<-done
							}
						}()
						go func() { _, err := c.ExecContext(ctx, tc.second); done <- err }()
						var result error
						completed, blocked := false, false
						for !completed && !blocked {
							select {
							case result = <-done:
								completed = true
								joined = true
							default:
							}
							if completed {
								break
							}
							if err := b.DB.QueryRowContext(ctx, "SELECT $1::integer = ANY(pg_blocking_pids($2::integer))", ap, bp).Scan(&blocked); err != nil {
								t.Fatal(err)
							}
						}
						wantBlocked := tc.name != "namespace_rename"
						if blocked != wantBlocked {
							t.Fatalf("queued=%t want=%t; early outcome=%v", blocked, wantBlocked, result)
						}
						fixture.LockExec(t, p.Ctx, b.DB, "INSERT INTO tx_ns_unrelated VALUES(2)", 1)
						q := "ROLLBACK"
						if commit {
							q = "COMMIT"
						}
						fixture.LockExec(t, p.Ctx, a, q, 0)
						if !completed {
							select {
							case result = <-done:
								joined = true
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							}
						}
						cancel()
						if commit && tc.name != "namespace_rename" {
							d := fixture.ControlsReferenceDiagnostic("23505", "")
							d.Schema = "pg_catalog"
							switch tc.name {
							case "table_same_name":
								d.Table, d.Constraint = "pg_type", "pg_type_typname_nsp_index"
								d.Detail = fmt.Sprintf("Key (typname, typnamespace)=(same, %d) already exists.", namespace)
							case "schema_same_name":
								d.Table, d.Constraint = "pg_namespace", "pg_namespace_nspname_index"
								d.Detail = "Key (nspname)=(tx_ns) already exists."
							case "rename_destination", "move_destination":
								d.Table, d.Constraint = "pg_class", "pg_class_relname_nsp_index"
								name := "tx_ns_target"
								if tc.name == "move_destination" {
									name = "tx_ns_source"
								}
								d.Detail = fmt.Sprintf("Key (relname, relnamespace)=(%s, %d) already exists.", name, namespace)
							case "namespace_drop":
								d = fixture.ControlsReferenceDiagnostic("3F000", `schema "tx_ns" does not exist`)
								d.Position = "14"
							}
							if d.Code == "23505" {
								d.Message = fmt.Sprintf("duplicate key value violates unique constraint %q", d.Constraint)
							}
							fixture.AssertDiagnostic(t, result, &d)
						} else {
							fixture.State(t, result, "")
						}
						var want [][]any
						switch tc.name {
						case "table_same_name":
							want = [][]any{{"tx_ns", "same"}}
						case "namespace_drop":
							if !commit {
								want = [][]any{{"tx_ns", "later"}}
							}
						case "namespace_rename":
							ns := "tx_ns"
							if commit {
								ns = "tx_ns_new"
							}
							want = [][]any{{ns, "later"}}
						case "rename_destination":
							want = [][]any{{"public", "tx_ns_target"}}
							if commit {
								want = [][]any{{"public", "tx_ns_source"}, {"public", "tx_ns_target"}}
							}
						case "move_destination":
							want = [][]any{{"tx_ns", "tx_ns_source"}}
							if commit {
								want = [][]any{{"public", "tx_ns_source"}, {"tx_ns", "tx_ns_source"}}
							}
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						var namespaces [][]any
						if tc.name != "rename_destination" && !(tc.name == "namespace_drop" && commit) {
							ns := "tx_ns"
							if tc.name == "namespace_rename" && commit {
								ns = "tx_ns_new"
							}
							namespaces = [][]any{{ns}}
						}
						run(fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname IN ('tx_ns','tx_ns_new') ORDER BY nspname", []string{"nspname"}, []string{"NAME"}, namespaces))
						filter := `c.relkind='r' AND ((n.nspname IN ('tx_ns','tx_ns_new')) OR (n.nspname='public' AND c.relname IN ('tx_ns_source','tx_ns_target')))`
						run(fixture.Select("SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE "+filter+" ORDER BY n.nspname,c.relname", []string{"nspname", "relname"}, []string{"NAME", "NAME"}, want))
						run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_type t ON t.oid=c.reltype AND t.typrelid=c.oid AND t.typnamespace=c.relnamespace WHERE "+filter, []string{"count"}, []string{"INT8"}, [][]any{{int64(len(want))}}))
						run(fixture.Select("SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname IN ('tx_ns','tx_ns_new') OR (n.nspname='public' AND t.typname IN ('tx_ns_source','_tx_ns_source','tx_ns_target','_tx_ns_target'))", []string{"count"}, []string{"INT8"}, [][]any{{int64(2 * len(want))}}))
						wantRows := [][]any{{int64(2)}}
						if commit {
							wantRows = [][]any{{int64(1)}, {int64(2)}}
						}
						run(fixture.Select("SELECT v FROM tx_ns_unrelated ORDER BY v", []string{"v"}, []string{"INT4"}, wantRows))
					})
				}
			})
		}
	}
}
