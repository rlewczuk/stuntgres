//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceSchemaNestedConditionalTables(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"nco"})
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "ncf", "ncs", "ncl", "ncb")
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []referenceDiagnostic
					_, conn, record := noticeReferencePool(t, p, postgres, &notices)
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, record, s) }
					// Notice pools use a subtest-specific native DSN. Provision roles
					// on this same physical connection, not the parent's pair pool.
					run(referenceExec("CREATE USER nco", 0))
					run(referenceExec("SET search_path TO public", 0))
					for _, tc := range []struct {
						schema, query string
						skipped       int
					}{
						{"ncf", "CREATE SCHEMA ncf AUTHORIZATION nco CREATE TABLE IF NOT EXISTS t(id INT NOT NULL) CREATE TABLE clone(LIKE t)", 0},
						{"ncs", "CREATE SCHEMA ncs AUTHORIZATION nco CREATE TABLE t(id INT NOT NULL) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE IF NOT EXISTS t(LIKE unavailable) CREATE TABLE clone(LIKE t)", 2},
					} {
						notices = nil
						run(referenceExec(tc.query, 0))
						var want []referenceDiagnostic
						for range tc.skipped {
							want = append(want, referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`})
						}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal("full conditional notice records", notices, want)
						}
						bootstrap := "postgres"
						if postgres {
							bootstrap = "stuntgres"
						}
						run(referenceSelect("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{bootstrap}}))
						run(referenceSelect("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
						for _, table := range []string{"t", "clone"} {
							run(referenceSelect("SELECT * FROM "+tc.schema+"."+table, []string{"id"}, []string{"INT4"}, nil))
							if postgres {
								var owner string
								var notnull bool
								if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT r.rolname,a.attnotnull FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname=$1 AND c.relname=$2 AND a.attname='id'", tc.schema, table).Scan(&owner, &notnull); err != nil || owner != "nco" || !notnull {
									t.Fatal("conditional owner/NOT NULL", owner, notnull, err)
								}
							}
						}
						// Ordinary owner execution independently witnesses native ownership,
						// not merely bootstrap visibility or oracle catalog spelling.
						run(referenceExec("SET SESSION AUTHORIZATION nco", 0))
						run(referenceExec("INSERT INTO "+tc.schema+".t VALUES(7)", 1))
						run(referenceExec("INSERT INTO "+tc.schema+".clone VALUES(9)", 1))
						run(referenceSelect("SELECT * FROM "+tc.schema+".t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(referenceSelect("SELECT * FROM "+tc.schema+".clone", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						notNull := referenceImplicitError("INSERT INTO "+tc.schema+".clone VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: tc.schema, Table: "clone", Column: "id"})
						notNull.errorStage = "execute"
						run(notNull)
						run(referenceSelect("SELECT * FROM "+tc.schema+".clone", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					}
					run(roleReferenceError("CREATE SCHEMA ncl CREATE TABLE t(id INT) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE bad(id INT,id TEXT)", "42701", `column "id" specified more than once`, "", prepared, "execute"))
					run(schemaReferenceCreationError("CREATE TABLE ncl.probe(id INT)", "3F000", `schema "ncl" does not exist`, prepared, "14"))
					malformed := referenceImplicitError("CREATE SCHEMA ncb CREATE TABLE IF NOT EXISTS t(id INT,)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "55"})
					malformed.errorStage = "execute"
					if prepared {
						malformed.errorStage = "prepare"
					}
					run(malformed)
					run(schemaReferenceCreationError("CREATE TABLE ncb.probe(id INT)", "3F000", `schema "ncb" does not exist`, prepared, "14"))
				})
			}
		})
	}
}
