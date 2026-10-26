package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtxtqueryPreparedAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"lx_authority_owner", "lx_authority_reader"})
	ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE lx_authority_owner",
			"CREATE ROLE lx_authority_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO lx_authority_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		var before, after int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.ltxtquery'::regtype::oid").Scan(&before); err != nil { t.Fatal(err) }
		query := "SELECT $1::text::public.ltxtquery AS v"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		checkHeld := func() {
			for _, tc := range []struct { input, want any }{
				{"foo*@%", "foo%@*"}, {nil, nil}, {"a|b&c", "a | b & c"},
			} {
				s := ltxtquerySelect(b, query, []string{"v"}, [][]any{{tc.want}}, tc.input)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ltxtqueryMetadata(t, b.PG, rows, []string{"v"}, nil) }
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		checkHeld()
		run(fixture.Exec("ALTER TYPE public.ltxtquery RENAME TO lx_authority_query", 0))
		// PostgreSQL retains the cast's resolved type identity. Authority changes
		// require fresh executable analysis, not resolution of obsolete spelling.
		run(fixture.Exec("ALTER TYPE public.lx_authority_query OWNER TO lx_authority_owner", 0))
		checkHeld()
		run(fixture.Exec("REVOKE USAGE ON TYPE public.lx_authority_query FROM PUBLIC", 0))
		checkHeld()
		run(fixture.Exec("GRANT USAGE ON TYPE public.lx_authority_query TO lx_authority_reader", 0))
		checkHeld()
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.lx_authority_query'::regtype::oid").Scan(&after); err != nil { t.Fatal(err) }
		if before <= 0 || before != after { t.Fatal("rename/authority change replaced type identity", before, after) }
		run(fixture.Select("SELECT pg_get_userbyid(typowner) AS owner FROM pg_type WHERE oid='public.lx_authority_query'::regtype::oid", []string{"owner"}, []string{"NAME"}, [][]any{{"lx_authority_owner"}}))

		// Type USAGE governs declarations, not ordinary casts/value reads.
		// A retained declaration must enforce current privileges each execution.
		run(fixture.Exec("SET ROLE lx_authority_reader", 0))
		declaration := "CREATE TABLE public.lx_authority_probe(v lx_authority_query)"
		declare, err := c.PrepareContext(p.Ctx, declaration)
		if err != nil { t.Fatal(err) }
		defer declare.Close()
		heldDeclaration := fixture.HeldStatement{Executor: c, Statement: declare, SQL: declaration}
		fixture.RunStep(t, p.Ctx, heldDeclaration, b.Recorder, fixture.Exec(declaration, 0))
		run(fixture.Exec("DROP TABLE public.lx_authority_probe", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE public.lx_authority_query FROM lx_authority_reader", 0))
		run(fixture.Exec("SET ROLE lx_authority_reader", 0))
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for type lx_authority_query")
		denied := fixture.ErrorDiagnostic(declaration, want)
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, heldDeclaration, b.Recorder, denied)
		run(ltxtquerySelect(b, "SELECT $1::text::lx_authority_query AS v", []string{"v"}, [][]any{{"foo%@*"}}, "foo*@%"))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON TYPE public.lx_authority_query TO lx_authority_reader", 0))
		run(fixture.Exec("SET ROLE lx_authority_reader", 0))
		fixture.RunStep(t, p.Ctx, heldDeclaration, b.Recorder, fixture.Exec(declaration, 0))
		run(fixture.Exec("DROP TABLE public.lx_authority_probe", 0))
		run(fixture.Exec("RESET ROLE", 0))
	})
}
