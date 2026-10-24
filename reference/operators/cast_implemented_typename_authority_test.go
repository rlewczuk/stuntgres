package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 parse_func.c permits a typename shortcut for binary and eligible IO
// pathways, not an arbitrary declared cast function. CAST still uses that
// function. The held phases preserve the existing backend-specific lifetime
// contract in TestReferenceCastDefinitionPreparedLifecycle.
func TestReferenceCastImplementedDeclaredTypeNameAuthority(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			castDefinitionCleanup(t, p, b, [2]string{"text", "bytea"})
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TYPE cast_review_enum AS ENUM ('red','blue')",
				"CREATE DOMAIN cast_review_domain AS cast_review_enum",
				"CREATE FUNCTION cast_review_override(cast_review_enum) RETURNS text LANGUAGE SQL AS $$SELECT CASE WHEN $1 IS NULL THEN NULL::text ELSE 'override'::text END$$",
			} {
				run(fixture.Exec(q, 0))
			}
			var held []fixture.HeldStatement
			var denied []fixture.Step
			for _, source := range []string{"cast_review_enum", "cast_review_domain"} {
				q := "SELECT text($1::" + source + ") AS v"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				exec := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				held = append(held, exec)
				fixture.RunStep(t, p.Ctx, exec, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}, "red"))
				failure := fixture.ImplicitError(q, fixture.Diagnostic{
					Code: "42883", Message: "function text(" + source + ") does not exist",
					Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8",
				}, "red")
				failure.SelectRows, failure.ErrorStage = true, "execute"
				denied = append(denied, failure)
			}
			run(fixture.Exec("CREATE CAST (cast_review_enum AS text) WITH FUNCTION cast_review_override(cast_review_enum)", 0))
			// The differently named adapter is callable only through CAST, not by
			// pretending it was the requested function text(enum) or text(domain).
			run(fixture.Select("SELECT CAST($1::cast_review_enum AS text) AS enum_v,CAST($1::cast_review_domain AS text) AS domain_v", []string{"enum_v", "domain_v"}, []string{"TEXT", "TEXT"}, [][]any{{"override", "override"}}, "red"))
			for i, exec := range held {
				run(denied[i])
				if b.PG {
					// No pg_cast dependency invalidates PostgreSQL's analyzed IO node.
					fixture.RunStep(t, p.Ctx, exec, b.Recorder, fixture.Select(exec.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}, "red"))
				} else {
					// Native compares captured pair presence/pointer and reanalyzes;
					// the domain witness must also capture its underlying enum pair.
					fixture.RunStep(t, p.Ctx, exec, b.Recorder, denied[i])
				}
			}
			run(fixture.Exec("DROP CAST (cast_review_enum AS text)", 0))
			for _, exec := range held {
				fixture.RunStep(t, p.Ctx, exec, b.Recorder, fixture.Select(exec.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"blue"}}, "blue"))
				run(fixture.Select(exec.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}, "red"))
			}
			run(fixture.Exec("CREATE CAST (cast_review_enum AS text) WITH INOUT", 0))
			run(fixture.Select("SELECT text($1::cast_review_enum) AS v,CAST($1::cast_review_enum AS text) AS c", []string{"v", "c"}, []string{"TEXT", "TEXT"}, [][]any{{"red", "red"}}, "red"))
			run(fixture.Exec("DROP CAST (cast_review_enum AS text)", 0))
			// A real varlena binary declaration is safe in both backends. The
			// literal backslash-x bytes distinguish it from bytea's input codec.
			run(fixture.Exec("CREATE CAST (text AS bytea) WITHOUT FUNCTION", 0))
			binary := fixture.Select("SELECT bytea($1::text) AS v,CAST($1::text AS bytea) AS c", []string{"v", "c"}, []string{"BYTEA", "BYTEA"}, [][]any{{`\x4142`, `\x4142`}}, `\x4142`)
			run(binary)
			run(fixture.Exec(`CREATE VIEW cast_review_binary AS SELECT bytea('\x4142'::text) AS v`, 0))
			run(fixture.Exec("DROP CAST (text AS bytea)", 0))
			run(fixture.Exec("CREATE CAST (text AS bytea) WITH INOUT", 0))
			io := binary
			io.Rows = [][]any{{"AB", "AB"}}
			run(io)
			// Stored typename selection keeps its analyzed binary method rather
			// than reinterpreting the frozen recipe through the new IO edge.
			run(fixture.Select("SELECT v FROM cast_review_binary", []string{"v"}, []string{"BYTEA"}, [][]any{{`\x4142`}}))
			run(fixture.Exec("DROP CAST (text AS bytea)", 0))
		})
	}
}
