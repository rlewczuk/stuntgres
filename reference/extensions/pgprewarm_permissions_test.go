package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pw_reader", "pw_owner", "pw_group"})
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE ROLE pw_reader", "CREATE ROLE pw_owner", "CREATE ROLE pw_group", "CREATE SCHEMA pw_target", "CREATE TABLE pw_target.heap(k integer)", "GRANT USAGE ON SCHEMA pw_part,pw_target TO pw_reader,pw_owner,pw_group", "ALTER TABLE pw_part.heap OWNER TO pw_owner", "CREATE INDEX heap_idx ON pw_part.heap(k)", "CREATE VIEW pw_part.denied_view AS SELECT k FROM pw_part.heap")
		pwInsert(t, p, b, c, "pw_part.heap", 1)
		pwInsert(t, p, b, c, "pw_target.heap", 1)
		q := "SELECT pw_part.pg_prewarm($1::regclass) AS warmed"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		h := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		phase := "no_relation_select"
		asReader := func() { bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION pw_reader") }
		admin := func(queries ...string) {
			bcDDL(t, p, b, c, "RESET ROLE", "RESET SESSION AUTHORIZATION")
			bcDDL(t, p, b, c, queries...)
			phase = strings.Join(queries, ";")
			asReader()
		}
		probe := func(s fixture.Step) {
			target, _ := s.Args[0].(string)
			if target == "" { target = "NULL" }
			for _, route := range []string{"direct", "fresh", "held"} {
				t.Run(phase+"/"+target+"/"+route, func(t *testing.T) {
					switch route {
					case "direct":
						bcRun(t, p, b, c, s)
					case "fresh":
						bcRun(t, p, b, c, fixture.Prepared(s))
					case "held":
						fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
					}
				})
			}
		}
		deniedTarget := func() {
			probe(pwError(q, "42501", "permission denied for table heap", "pw_part.heap"))
			probe(pwError(q, "42501", "permission denied for index heap_idx", "pw_part.heap_idx"))
		}
		asReader()
		// PUBLIC EXECUTE is real, but neither column-only SELECT nor monitor
		// membership supplies the required whole-relation SELECT privilege.
		deniedTarget()
		admin("GRANT SELECT(k) ON pw_part.heap TO pw_reader")
		deniedTarget()
		admin("GRANT pg_monitor TO pw_reader")
		deniedTarget()
		admin("GRANT SELECT ON pw_part.heap TO pw_group", "GRANT pw_group TO pw_reader")
		probe(pwCount(q, 1, "pw_part.heap"))
		probe(pwCount(q, pwExtent(t, p, b, c, "pw_part.heap_idx", "main", 1), "pw_part.heap_idx"))
		admin("GRANT pw_group TO pw_reader WITH INHERIT FALSE")
		deniedTarget()
		bcDDL(t, p, b, c, "SET ROLE pw_group")
		probe(pwCount(q, 1, "pw_part.heap"))
		admin("GRANT pw_group TO pw_reader WITH INHERIT TRUE", "REVOKE SELECT ON pw_part.heap FROM pw_group")
		deniedTarget()
		admin("GRANT SELECT ON pw_part.heap TO pw_reader")
		probe(pwCount(q, 1, "pw_part.heap"))
		admin("REVOKE EXECUTE ON FUNCTION "+pwSignature+" FROM PUBLIC")
		for _, target := range []any{nil, "pw_part.heap"} { probe(pwError(q, "42501", "permission denied for function pg_prewarm", target)) }
		admin("GRANT EXECUTE ON FUNCTION "+pwSignature+" TO pw_reader")
		probe(pwCount(q, 1, "pw_part.heap"))
		admin("REVOKE SELECT ON pw_part.heap FROM pw_reader")
		deniedTarget()
		// ACL precedes storage kind, fork existence, and invalid endpoints.
		pwBodyError(t, p, b, c, pwError(pwCall, "42501", "permission denied for view denied_view", "pw_part.denied_view", "buffer", "init", int64(-1), int64(-1)))
		admin("GRANT SELECT ON pw_part.heap TO pw_reader", "REVOKE USAGE ON SCHEMA pw_part FROM pw_reader,pw_group")
		s := pwError(q, "42501", "permission denied for schema pw_part", "pw_part.heap")
		s.Diagnostic.Position = fixture.Position(strings.Index(q, "pw_part.pg_prewarm")+1)
		bcBoth(t, p, b, c, s)
		// Keep the routine namespace usable while denying only the distinct
		// target schema: ordinary regclass input owns this bound diagnostic.
		admin("GRANT USAGE ON SCHEMA pw_part TO pw_reader", "GRANT SELECT ON pw_target.heap TO pw_reader", "REVOKE USAGE ON SCHEMA pw_target FROM pw_reader,pw_group")
		heldSchema := pwError(q, "42501", "permission denied for schema pw_target", "pw_target.heap")
		heldSchema.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSchema)
		admin("GRANT USAGE ON SCHEMA pw_target TO pw_reader")
		probe(pwCount(q, 1, "pw_target.heap"))
		probe(pwCount(q, 1, "pw_part.heap"))
		// A real configuration clause prevents scalar SQL inlining in both
		// modes; fully qualified body names keep this an authority witness.
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION "+pwSignature+" TO pw_owner", "CREATE FUNCTION pw_part.definer() RETURNS bigint LANGUAGE SQL SECURITY DEFINER SET search_path=pg_catalog AS 'SELECT pw_part.pg_prewarm(''pw_part.heap'')'", "ALTER FUNCTION pw_part.definer() OWNER TO pw_owner", "CREATE FUNCTION pw_part.invoker() RETURNS bigint LANGUAGE SQL SET search_path=pg_catalog AS 'SELECT pw_part.pg_prewarm(''pw_part.heap'')'", "SET SESSION AUTHORIZATION pw_reader")
		bcBoth(t, p, b, c, pwCount("SELECT pw_part.definer() AS warmed", 1))
		bcBoth(t, p, b, c, pwCount("SELECT pw_part.invoker() AS warmed", 1))
		admin("REVOKE SELECT ON pw_part.heap FROM pw_reader", "REVOKE SELECT(k) ON pw_part.heap FROM pw_reader")
		bcBoth(t, p, b, c, pwCount("SELECT pw_part.definer() AS warmed", 1))
		// REL_18_6 inline_function rejects non-NULL proconfig, so this denial
		// belongs to the genuine nested SQL statement, not an inline expression.
		s = pwError("SELECT pw_part.invoker() AS warmed", "42501", "permission denied for table heap")
		s.Diagnostic.Context = "SQL function \"invoker\" statement 1"
		bcBoth(t, p, b, c, s)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "DROP EXTENSION pg_prewarm CASCADE", "GRANT CREATE ON SCHEMA pw_part TO pw_reader", "SET SESSION AUTHORIZATION pw_reader")
		s = pwError("CREATE EXTENSION pg_prewarm SCHEMA pw_part", "42501", "permission denied to create extension \"pg_prewarm\"")
		s.SelectRows = false
		s.Diagnostic.Hint = "Must be superuser to create this extension."
		bcRun(t, p, b, c, s)
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION")
	})
}
