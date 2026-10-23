package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ca_owner", "ca_reader"})
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
		for _, q := range []string{
			"CREATE ROLE ca_owner", "CREATE ROLE ca_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO ca_owner", "GRANT USAGE ON SCHEMA public TO ca_reader",
			"CREATE FUNCTION ca_support(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
			"REVOKE EXECUTE ON FUNCTION ca_support(integer,integer) FROM PUBLIC",
			"SET SESSION AUTHORIZATION ca_owner",
		} {
			exec(q)
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		denied := fixture.ImplicitError("CREATE AGGREGATE ca_owned(integer) (SFUNC=ca_support,STYPE=int4,INITCOND='0')", fixture.Diagnostic{Code: "42501", Message: "permission denied for function ca_support"})
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ca_support(integer,integer) TO ca_owner", "SET SESSION AUTHORIZATION ca_owner", "CREATE AGGREGATE ca_owned(integer) (SFUNC=ca_support,STYPE=int4,INITCOND='0')", "REVOKE EXECUTE ON FUNCTION ca_owned(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ca_owned(integer) TO ca_reader", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION ca_reader"} {
			exec(q)
		}
		const q = "SELECT ca_owned(x) AS v FROM(VALUES(1),(2),(3)) s(x) WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		value := func(limit, want int64) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(q, want, limit))
		}
		value(3, 6)
		value(0, 0)
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ca_support(integer,integer) FROM ca_owner", "SET SESSION AUTHORIZATION ca_reader"} {
			exec(sql)
		}
		for _, limit := range []int64{3, 0} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function ca_support"}, limit)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION ca_support(integer,integer) TO ca_owner", "SET SESSION AUTHORIZATION ca_reader"} {
			exec(sql)
		}
		value(3, 6)
		for _, sql := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION ca_owned(integer) FROM ca_reader", "SET SESSION AUTHORIZATION ca_reader"} {
			exec(sql)
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate ca_owned"}, 0)
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		exec("RESET SESSION AUTHORIZATION")
		for _, sql := range []string{
			"CREATE TABLE ca_private(v integer)",
			"CREATE FUNCTION ca_body_step(integer,integer) RETURNS integer BEGIN ATOMIC SELECT $1+$2+v FROM ca_private; END",
			"REVOKE EXECUTE ON FUNCTION ca_body_step(integer,integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ca_body_step(integer,integer) TO ca_owner",
			"GRANT SELECT ON ca_private TO ca_owner",
			"SET SESSION AUTHORIZATION ca_owner",
			"CREATE AGGREGATE ca_body(integer) (SFUNC=ca_body_step,STYPE=int4,INITCOND='0')",
			"RESET SESSION AUTHORIZATION",
		} {
			exec(sql)
		}
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ca_private VALUES(10)", 1))
		exec("SET SESSION AUTHORIZATION ca_reader")
		bodyQ := "SELECT ca_body(x) AS v FROM(VALUES(1)) s(x)"
		bodyDenied := fixture.ImplicitError(bodyQ, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ca_private", Context: "SQL function \"ca_body_step\" statement 1"})
		bodyDenied.SelectRows = true
		bodyDenied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, bodyDenied)
		exec("RESET SESSION AUTHORIZATION")
		exec("GRANT SELECT ON ca_private TO ca_reader")
		exec("SET SESSION AUTHORIZATION ca_reader")
		createAggregateBoth(t, p, b, c, createAggregateValue(bodyQ, int64(11)))
		schemaDenied := fixture.ImplicitError("CREATE AGGREGATE ca_schema_denied(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema public"})
		schemaDenied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, schemaDenied)
		exec("RESET SESSION AUTHORIZATION")
		for _, sql := range []string{
			"CREATE DOMAIN ca_private_type AS integer",
			"CREATE FUNCTION ca_type_step(ca_private_type,ca_private_type) RETURNS ca_private_type LANGUAGE SQL AS $$SELECT ($1+$2)::ca_private_type$$",
			"REVOKE USAGE ON TYPE ca_private_type FROM PUBLIC",
			"GRANT CREATE ON SCHEMA public TO ca_reader",
			"SET SESSION AUTHORIZATION ca_reader",
		} {
			exec(sql)
		}
		typeQuery := "CREATE AGGREGATE ca_type_denied(ca_private_type) (SFUNC=ca_type_step,STYPE=ca_private_type,INITCOND='0')"
		typeDenied := fixture.ImplicitError(typeQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for type ca_private_type"})
		typeDenied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, typeDenied)
		exec("RESET SESSION AUTHORIZATION")
		exec("GRANT USAGE ON TYPE ca_private_type TO ca_reader")
		exec("SET SESSION AUTHORIZATION ca_reader")
		createAggregateRun(t, p, b, c, fixture.Exec(typeQuery, 0))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_type_denied(x)::integer AS v FROM(VALUES(1::ca_private_type),(2::ca_private_type),(3::ca_private_type)) s(x)", int64(6)))
		exec("RESET SESSION AUTHORIZATION")
		createAggregateAliasAuthority(t, p, b, c)
	})
}

// Computation sharing must not collapse the independently selected alias ACLs.
func createAggregateAliasAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	exec := func(q string) { createAggregateRun(t, p, b, c, fixture.Exec(q, 0)) }
	targets := []struct {
		name, signature, control, typ string
		value                         any
	}{
		{"bool_and", "boolean", "every(x>1)", "BOOL", false},
		{"every", "boolean", "bool_and(x>1)", "BOOL", false},
		{"var_samp", "integer", "variance(x)::integer", "INT4", int64(2)},
		{"variance", "integer", "var_samp(x)::integer", "INT4", int64(2)},
		{"stddev_samp", "integer", "stddev(x)::integer", "INT4", int64(1)},
		{"stddev", "integer", "stddev_samp(x)::integer", "INT4", int64(1)},
	}
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error("alias ACL cleanup identity", err)
			return
		}
		for _, target := range targets {
			for _, q := range []string{
				"GRANT EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") TO PUBLIC",
				"REVOKE EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") FROM ca_reader CASCADE",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("alias ACL cleanup", q, err)
				}
			}
		}
	}()
	for _, target := range targets {
		exec("REVOKE EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") FROM PUBLIC")
		exec("GRANT EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") TO ca_reader")
	}
	exec("SET SESSION AUTHORIZATION ca_reader")
	const body = "SELECT bool_and(x>1) AS b,every(x>1) AS e,bool_and(x>1) AS b2,var_samp(x)::integer AS v,variance(x)::integer AS va,stddev_samp(x)::integer AS s,stddev(x)::integer AS sa FROM(VALUES(1),(3)) t(x)"
	const query = body + " WHERE $1::boolean"
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	exec("CREATE VIEW ca_alias_rights AS " + body)
	columns := []string{"b", "e", "b2", "v", "va", "s", "sa"}
	types := []string{"BOOL", "BOOL", "BOOL", "INT4", "INT4", "INT4", "INT4"}
	values := [][]any{{false, false, false, int64(2), int64(2), int64(1), int64(1)}}
	allowed := func() {
		for _, populated := range []bool{true, false} {
			want := values
			if !populated {
				want = [][]any{{nil, nil, nil, nil, nil, nil, nil}}
			}
			step := fixture.Select(query, columns, types, want, populated)
			createAggregateBoth(t, p, b, c, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT * FROM ca_alias_rights", columns, types, values))
	}
	allowed()
	for _, target := range targets {
		exec("RESET SESSION AUTHORIZATION")
		exec("REVOKE EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") FROM ca_reader")
		exec("SET SESSION AUTHORIZATION ca_reader")
		for _, populated := range []bool{true, false} {
			denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate " + target.name}, populated)
			denied.SelectRows, denied.ErrorStage = true, "execute"
			createAggregateBoth(t, p, b, c, denied)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		}
		deniedView := fixture.ImplicitError("SELECT * FROM ca_alias_rights", fixture.Diagnostic{Code: "42501", Message: "permission denied for aggregate " + target.name})
		deniedView.SelectRows, deniedView.ErrorStage = true, "execute"
		createAggregateBoth(t, p, b, c, deniedView)
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+target.control+" AS v FROM(VALUES(1),(3)) t(x)", []string{"v"}, []string{target.typ}, [][]any{{target.value}}))
		exec("RESET SESSION AUTHORIZATION")
		exec("GRANT EXECUTE ON FUNCTION pg_catalog." + target.name + "(" + target.signature + ") TO ca_reader")
		exec("SET SESSION AUTHORIZATION ca_reader")
		allowed()
	}
}

func TestReferenceCreateAggregateCancellation(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ca_sleep_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT CASE WHEN pg_sleep(CASE WHEN $2>1 THEN 30 ELSE 0 END) IS NULL THEN $1+$2 ELSE $1+$2 END$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_sleep(integer) (SFUNC=ca_sleep_step,STYPE=int4,INITCOND='0')", 0),
			fixture.Exec("CREATE TABLE ca_cancel_source(x integer)", 0),
			fixture.Exec("INSERT INTO ca_cancel_source VALUES(1),(2)", 2),
			fixture.Exec("CREATE TABLE ca_cancel_destination(v integer)", 0))
		const q = "INSERT INTO ca_cancel_destination SELECT ca_sleep(x ORDER BY x) FROM ca_cancel_source WHERE x<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, prepared := range []bool{false, true} {
			fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL function \"ca_sleep_step\" statement 1", nil, func(ctx context.Context) fixture.RelationOutcome {
				var r sql.Result
				var err error
				if prepared {
					r, err = stmt.ExecContext(ctx, 2)
				} else {
					r, err = c.ExecContext(ctx, q, 2)
				}
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ca_cancel_destination", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 1))
			createAggregateRun(t, p, b, c, createAggregateValue("SELECT v FROM ca_cancel_destination", int64(1)), fixture.Exec("DELETE FROM ca_cancel_destination", 1))
		}
	})
}
