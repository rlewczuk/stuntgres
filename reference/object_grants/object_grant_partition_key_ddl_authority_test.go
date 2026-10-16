package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A populated DEFAULT scan is an actual key consumer, unlike an empty
// declaration. Its exact authority/error frontier is independently oracle-first.
func TestReferenceObjectGrantPartitionKeyDDLAuthority(t *testing.T) {
	for _, key := range []struct{ name, typ, function, target, one, two string }{
		{"json", "JSON", "json_array_length", "pg_catalog.json_array_length(json)", "[1]", "[1,2]"},
		{"text", "TEXT", "bit_length", "pg_catalog.bit_length(text)", "a", "ab"},
	} {
		t.Run(key.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_key_ddl"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				targets := []string{key.target}
				if key.name == "text" {
					targets = append(targets, "pg_catalog.octet_length(text)")
				}
				defer objectGrantRoutineCleanup(t, p, c, targets)
				run(fixture.Exec("CREATE SCHEMA og_key_ddl AUTHORIZATION og_object_other", 0))
				fixture.RoleGrantActor(run, "og_object_other", func() {
					run(fixture.Exec("CREATE TABLE og_key_ddl.root(id INT,v "+key.typ+") PARTITION BY RANGE(("+key.function+"(v)))", 0))
					run(fixture.Exec("CREATE TABLE og_key_ddl.fallback PARTITION OF og_key_ddl.root DEFAULT", 0))
					run(fixture.Exec("INSERT INTO og_key_ddl.root VALUES(1,$1),(2,$2)", 2, key.one, key.two))
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+key.target+" FROM PUBLIC,og_object_other", 0))
				prepare := func(query string) fixture.HeldStatement {
					t.Helper()
					var held fixture.HeldStatement
					fixture.RoleGrantActor(run, "og_object_other", func() {
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						held = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					})
					return held
				}
				denial := func(query, name string) fixture.Step {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name})
					s.ErrorStage = "execute"
					return s
				}
				absent := func(name string) {
					run(fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='og_key_ddl' AND c.relname=$1", []string{"relname"}, []string{"NAME"}, nil, name))
				}
				rows := func() {
					for _, table := range []string{"root", "fallback"} {
						run(fixture.Select("SELECT id,v::TEXT AS value FROM og_key_ddl."+table+" ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), key.one}, {int64(2), key.two}}))
					}
				}
				const first = "CREATE TABLE og_key_ddl.future_a PARTITION OF og_key_ddl.root FOR VALUES FROM(100) TO(200)"
				held := prepare(first)
				defer held.Statement.Close()
				// PostgreSQL's relation cache can retain an inlined child graph.
				// Native's established fresh-authority contract retains the original
				// wrapper, just as for warm index/held SQL-wrapper programs.
				// Neither outcome licenses skipping the surviving child below.
				fixture.RoleGrantActor(run, "og_object_other", func() {
					if key.name == "text" && b.PG {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(first, 0))
					} else {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, denial(first, key.function))
					}
					rows()
				})
				if key.name == "json" || !b.PG {
					absent("future_a")
				}
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+key.target+" TO og_object_other", 0))
				fixture.RoleGrantActor(run, "og_object_other", func() {
					if key.name == "json" || !b.PG {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(first, 0))
					}
					rows()
					run(fixture.Select("SELECT id FROM og_key_ddl.future_a", []string{"id"}, []string{"INT4"}, nil))
				})
				if key.name == "text" {
					const child = "pg_catalog.octet_length(text)"
					const second = "CREATE TABLE og_key_ddl.future_b PARTITION OF og_key_ddl.root FOR VALUES FROM(200) TO(300)"
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+child+" FROM PUBLIC,og_object_other", 0))
					other := prepare(second)
					defer other.Statement.Close()
					fixture.RoleGrantActor(run, "og_object_other", func() {
						fixture.RunStep(t, p.Ctx, other, b.Recorder, denial(second, "octet_length"))
						rows()
					})
					absent("future_b")
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+child+" TO og_object_other", 0))
					fixture.RoleGrantActor(run, "og_object_other", func() {
						fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Exec(second, 0))
						rows()
						run(fixture.Select("SELECT id FROM og_key_ddl.future_b", []string{"id"}, []string{"INT4"}, nil))
					})
				}
			})
		})
	}
}
