//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaNestedConditionalTables(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"nco"})
			p := fixture.NewPair(t)
			fixture.SchemaReferenceCleanup(t, "ncf", "ncs", "ncl", "ncb")
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, record, s) }
					// Notice pools use a subtest-specific native DSN. Provision roles
					// on this same physical connection, not the parent's pair pool.
					run(fixture.Exec("CREATE USER nco", 0))
					run(fixture.Exec("SET search_path TO public", 0))
					for _, tc := range []struct {
						schema, query string
						skipped       int
					}{
						{"ncf", "CREATE SCHEMA ncf AUTHORIZATION nco CREATE TABLE IF NOT EXISTS t(id INT NOT NULL) CREATE TABLE clone(LIKE t)", 0},
						{"ncs", "CREATE SCHEMA ncs AUTHORIZATION nco CREATE TABLE t(id INT NOT NULL) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE IF NOT EXISTS t(LIKE unavailable) CREATE TABLE clone(LIKE t)", 2},
					} {
						notices = nil
						run(fixture.Exec(tc.query, 0))
						var want []fixture.Diagnostic
						for range tc.skipped {
							want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`})
						}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal("full conditional notice records", notices, want)
						}
						bootstrap := "postgres"
						if postgres {
							bootstrap = "stuntgres"
						}
						run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{bootstrap}}))
						run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
						for _, table := range []string{"t", "clone"} {
							run(fixture.Select("SELECT * FROM "+tc.schema+"."+table, []string{"id"}, []string{"INT4"}, nil))
							if postgres {
								var owner string
								var notnull bool
								if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT r.rolname,a.attnotnull FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname=$1 AND c.relname=$2 AND a.attname='id'", tc.schema, table).Scan(&owner, &notnull); err != nil || owner != "nco" || !notnull {
									t.Fatal("conditional owner/NOT NULL", owner, notnull, err)
								}
							}
						}
						// Ordinary owner execution independently witnesses native ownership,
						// not merely bootstrap visibility or oracle catalog spelling.
						run(fixture.Exec("SET SESSION AUTHORIZATION nco", 0))
						run(fixture.Exec("INSERT INTO "+tc.schema+".t VALUES(7)", 1))
						run(fixture.Exec("INSERT INTO "+tc.schema+".clone VALUES(9)", 1))
						run(fixture.Select("SELECT * FROM "+tc.schema+".t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(fixture.Select("SELECT * FROM "+tc.schema+".clone", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						notNull := fixture.ImplicitError("INSERT INTO "+tc.schema+".clone VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "id" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: tc.schema, Table: "clone", Column: "id"})
						notNull.ErrorStage = "execute"
						run(notNull)
						run(fixture.Select("SELECT * FROM "+tc.schema+".clone", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					}
					run(fixture.RoleReferenceError("CREATE SCHEMA ncl CREATE TABLE t(id INT) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE bad(id INT,id TEXT)", "42701", `column "id" specified more than once`, "", prepared, "execute"))
					run(fixture.SchemaReferenceCreationError("CREATE TABLE ncl.probe(id INT)", "3F000", `schema "ncl" does not exist`, prepared, "14"))
					malformed := fixture.ImplicitError("CREATE SCHEMA ncb CREATE TABLE IF NOT EXISTS t(id INT,)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "55"})
					malformed.ErrorStage = "execute"
					if prepared {
						malformed.ErrorStage = "prepare"
					}
					run(malformed)
					run(fixture.SchemaReferenceCreationError("CREATE TABLE ncb.probe(id INT)", "3F000", `schema "ncb" does not exist`, prepared, "14"))
				})
			}
		})
	}
}
