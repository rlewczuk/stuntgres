package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// This is a shared public scenario, not a native forcing hook. PostgreSQL's
// ordinary serial aggsplit uses the transition and existing final routine, but
// does not require its owner's unused combine/transport privileges.
func aggregateCombineStringAuthority(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		const aggregate = "pg_catalog.string_agg(text,text)"
		var originalOwner string
		var originalRawACL sql.NullString
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_userbyid(proowner),proacl::text FROM pg_proc WHERE oid=3538").Scan(&originalOwner, &originalRawACL); err != nil {
			t.Fatal(err)
		}
		supports := []struct {
			name, signature string
			oid             int
			owner           string
			rawACL          sql.NullString
		}{
			{name: "string_agg_transfn", signature: "internal,text,text", oid: 3535},
			{name: "string_agg_finalfn", signature: "internal", oid: 3536},
			{name: "string_agg_combine", signature: "internal,internal", oid: 6299},
			{name: "string_agg_serialize", signature: "internal", oid: 6300},
			{name: "string_agg_deserialize", signature: "bytea,internal", oid: 6301},
		}
		for i := range supports {
			s := &supports[i]
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_userbyid(proowner),proacl::text FROM pg_proc WHERE oid=$1::oid", s.oid).Scan(&s.owner, &s.rawACL); err != nil {
				t.Fatal(err)
			}
		}
		exec("CREATE ROLE ac_string_owner")
		exec("CREATE ROLE ac_string_reader")
		exec("BEGIN")
		// Schema reset cannot undo builtin mutations. Exact original owner plus
		// complete nullable proacl determines every effective grantee, grantor and
		// grant option, including implicit defaults and unrelated grants. Check
		// those originals for the aggregate and all five supports after rollback.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cleanup := func(q string) {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Errorf("builtin string authority cleanup %s: %v", q, err)
				}
			}
			cleanup("ROLLBACK")
			cleanup("RESET SESSION AUTHORIZATION")
			cleanup("RESET ROLE")
			for _, s := range supports {
				var owner string
				var raw sql.NullString
				if err := c.QueryRowContext(ctx, "SELECT pg_get_userbyid(proowner),proacl::text FROM pg_proc WHERE oid=$1::oid", s.oid).Scan(&owner, &raw); err != nil || owner != s.owner || raw != s.rawACL {
					t.Errorf("builtin string %s owner/ACL rollback: got %q/%#v, want %q/%#v, error %v", s.name, owner, raw, s.owner, s.rawACL, err)
				}
			}
			var owner string
			var raw sql.NullString
			if err := c.QueryRowContext(ctx, "SELECT pg_get_userbyid(proowner),proacl::text FROM pg_proc WHERE oid=3538").Scan(&owner, &raw); err != nil || owner != originalOwner || raw != originalRawACL {
				t.Errorf("builtin string owner/ACL rollback: got %q/%#v, want %q/%#v, error %v", owner, raw, originalOwner, originalRawACL, err)
			}
		}()
		for _, q := range []string{
			"GRANT CREATE ON SCHEMA pg_catalog TO ac_string_owner",
			"GRANT USAGE ON SCHEMA public TO ac_string_reader",
			"ALTER FUNCTION " + aggregate + " OWNER TO ac_string_owner",
			"CREATE TABLE ac_string_rights(id integer,v text)",
			"GRANT SELECT ON ac_string_rights TO ac_string_reader",
		} {
			exec(q)
		}
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ac_string_rights VALUES(1,'x'),(2,'x')", 2))
		for _, s := range supports {
			exec("REVOKE EXECUTE ON FUNCTION pg_catalog." + s.name + "(" + s.signature + ") FROM PUBLIC")
		}
		for _, q := range []string{
			"GRANT EXECUTE ON FUNCTION pg_catalog.string_agg_transfn(internal,text,text) TO ac_string_owner",
			"GRANT EXECUTE ON FUNCTION pg_catalog.string_agg_finalfn(internal) TO ac_string_owner",
			"SET SESSION AUTHORIZATION ac_string_reader",
			"SET max_parallel_workers_per_gather=0",
		} {
			exec(q)
		}
		const query = "SELECT string_agg(v,','::text) AS v FROM ac_string_rights WHERE id<=$1"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		for _, limit := range []int{0, 2} {
			var expected any
			if limit != 0 {
				expected = "x,x"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{expected}}, limit))
		}
		for _, s := range supports[:2] {
			exec("RESET SESSION AUTHORIZATION")
			function := "pg_catalog." + s.name + "(" + s.signature + ")"
			exec("REVOKE EXECUTE ON FUNCTION " + function + " FROM ac_string_owner")
			exec("SET SESSION AUTHORIZATION ac_string_reader")
			for _, limit := range []int{0, 2} {
				failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + s.name}, limit)
				failure.SelectRows, failure.ErrorStage = true, "execute"
				exec("SAVEPOINT ac_string_denial")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
				exec("ROLLBACK TO SAVEPOINT ac_string_denial")
				exec("RELEASE SAVEPOINT ac_string_denial")
				for _, prepared := range []bool{false, true} {
					failure.Prepared = prepared
					exec("SAVEPOINT ac_string_denial")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					exec("ROLLBACK TO SAVEPOINT ac_string_denial")
					exec("RELEASE SAVEPOINT ac_string_denial")
				}
			}
			exec("RESET SESSION AUTHORIZATION")
			exec("GRANT EXECUTE ON FUNCTION " + function + " TO ac_string_owner")
			exec("SET SESSION AUTHORIZATION ac_string_reader")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"x,x"}}, 2))
		}
	})
}
