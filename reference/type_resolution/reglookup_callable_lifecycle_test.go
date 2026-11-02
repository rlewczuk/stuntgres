package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegLookupCallableLifecycle(t *testing.T) {
	regLookupScenario(t, []string{"call_left", "call_right", "call_moved", "call_private"}, []string{"call_owner", "call_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c,
			"CREATE SCHEMA call_left", "CREATE SCHEMA call_right", "CREATE SCHEMA call_moved",
			"CREATE FUNCTION call_left.call_same(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION call_right.call_same(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR call_left.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)",
			"CREATE OPERATOR call_right.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)",
			"SET search_path TO call_left,call_right,public")
		leftF := regLookupIdentity(t, p, c, "SELECT 'call_left.call_same(integer)'::regprocedure::oid::bigint")
		rightF := regLookupIdentity(t, p, c, "SELECT 'call_right.call_same(integer)'::regprocedure::oid::bigint")
		leftO := regLookupIdentity(t, p, c, "SELECT 'call_left.#~#(integer,integer)'::regoperator::oid::bigint")
		rightO := regLookupIdentity(t, p, c, "SELECT 'call_right.#~#(integer,integer)'::regoperator::oid::bigint")
		if leftF == rightF || leftO == rightO {
			t.Fatal("distinct definitions share identity")
		}
		literalSQL := "SELECT to_regproc('call_same')::oid::bigint AS r,to_regprocedure('call_same(integer)')::oid::bigint AS p,to_regoper('#~#')::oid::bigint AS o,to_regoperator('#~#(int,int)')::oid::bigint AS s"
		parameterSQL := "SELECT to_regproc($1)::oid::bigint AS r,to_regprocedure($2)::oid::bigint AS p,to_regoper($3)::oid::bigint AS o,to_regoperator($4)::oid::bigint AS s"
		literal := regLookupHeld(t, p, c, literalSQL)
		defer literal.Statement.Close()
		parameter := regLookupHeld(t, p, c, parameterSQL)
		defer parameter.Statement.Close()
		checkpoint := func(f, o any) {
			t.Helper()
			for _, route := range []struct {
				held fixture.HeldStatement
				args []any
			}{
				{literal, nil}, {parameter, []any{"call_same", "call_same(integer)", "#~#", "#~#(int,int)"}},
			} {
				s := fixture.Select(route.held.SQL, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{f, f, o, o}}, route.args...)
				regObjectBoth(t, p, b, c, s)
				regObjectRunStep(t, p, b, route.held, s)
			}
		}
		checkpoint(leftF, leftO)
		regObjectExec(t, p, b, c, "SET search_path TO call_right,call_left,public")
		checkpoint(rightF, rightO)
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_left.call_same(integer)')::oid::bigint AS f,to_regoperator('call_left.#~#(integer,integer)')::oid::bigint AS o", []string{"f", "o"}, []string{"INT8", "INT8"}, [][]any{{leftF, leftO}}))

		// Masking is by exact ordered signature, not name. A distinct overload
		// in the later namespace still makes bare-name lookup ambiguous.
		regObjectExec(t, p, b, c, "SET search_path TO call_left,call_right,public",
			"CREATE FUNCTION call_right.call_same(bigint) RETURNS bigint LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR call_right.#~# (LEFTARG=bigint,RIGHTARG=bigint,FUNCTION=pg_catalog.int8pl)")
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regproc('call_same') AS r,to_regoper('#~#') AS o", []string{"r", "o"}, []string{"REGPROC", "REGOPER"}, [][]any{{nil, nil}}))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_same(integer)')::oid::bigint AS f,to_regoperator('#~#(int,int)')::oid::bigint AS o", []string{"f", "o"}, []string{"INT8", "INT8"}, [][]any{{leftF, leftO}}))
		regObjectExec(t, p, b, c, "DROP FUNCTION call_right.call_same(bigint)", "DROP OPERATOR call_right.#~#(bigint,bigint)",
			"SET search_path TO call_left,public", "ALTER FUNCTION call_left.call_same(integer) RENAME TO call_renamed")
		// Function rename changes the lookup result, not the underlying identity.
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_left.call_renamed(integer)')::oid::bigint AS f", []string{"f"}, []string{"INT8"}, [][]any{{leftF}}))
		checkpoint(nil, leftO)
		regObjectExec(t, p, b, c, "ALTER FUNCTION call_left.call_renamed(integer) SET SCHEMA call_moved", "ALTER OPERATOR call_left.#~#(integer,integer) SET SCHEMA call_moved")
		checkpoint(nil, nil)
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_moved.call_renamed(integer)')::oid::bigint AS f,to_regoperator('call_moved.#~#(int,int)')::oid::bigint AS o", []string{"f", "o"}, []string{"INT8", "INT8"}, [][]any{{leftF, leftO}}))
		regObjectExec(t, p, b, c, "DROP FUNCTION call_moved.call_renamed(integer)", "DROP OPERATOR call_moved.#~#(integer,integer)")
		checkpoint(nil, nil)
		regObjectExec(t, p, b, c, "CREATE FUNCTION call_left.call_same(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "CREATE OPERATOR call_left.#~# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)")
		newF := regLookupIdentity(t, p, c, "SELECT 'call_left.call_same(integer)'::regprocedure::oid::bigint")
		newO := regLookupIdentity(t, p, c, "SELECT 'call_left.#~#(integer,integer)'::regoperator::oid::bigint")
		if newF == leftF || newO == leftO {
			t.Fatal("recreated target reused dropped identity")
		}
		checkpoint(newF, newO)

		// Implicit pg_catalog precedence can be changed only by positioning it.
		regObjectExec(t, p, b, c,
			"CREATE FUNCTION call_left.now() RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION call_left.call_unary(double precision) RETURNS double precision LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR call_left.||/ (RIGHTARG=double precision,FUNCTION=call_left.call_unary)")
		shadowF := regLookupIdentity(t, p, c, "SELECT 'call_left.now()'::regprocedure::oid::bigint")
		shadowO := regLookupIdentity(t, p, c, "SELECT 'call_left.||/(NONE,double precision)'::regoperator::oid::bigint")
		for _, tc := range []struct {
			path string
			f, o int64
		}{
			{"call_left,public", 1299, 597}, {"call_left,pg_catalog", shadowF, shadowO}, {"pg_catalog,call_left", 1299, 597},
		} {
			regObjectExec(t, p, b, c, "SET search_path TO "+tc.path)
			regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('now()')::oid::bigint AS f,to_regoperator('||/(NONE,float8)')::oid::bigint AS o", []string{"f", "o"}, []string{"INT8", "INT8"}, [][]any{{tc.f, tc.o}}))
		}
		regObjectExec(t, p, b, c, "SET search_path TO call_left,public", "CREATE DOMAIN call_left.call_domain AS integer",
			"CREATE FUNCTION call_left.call_exact(call_left.call_domain) RETURNS integer LANGUAGE SQL AS 'SELECT $1::integer'",
			"CREATE FUNCTION call_left.call_array(integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION call_left.call_default(integer DEFAULT 7) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION call_left.call_variadic(VARIADIC integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_exact(call_domain)')::oid='call_left.call_exact(call_left.call_domain)'::regprocedure::oid AS domain,to_regprocedure('call_array(int4[])')::oid='call_left.call_array(integer[])'::regprocedure::oid AS array,to_regprocedure('call_variadic(integer[])')::oid='call_left.call_variadic(integer[])'::regprocedure::oid AS variadic", []string{"domain", "array", "variadic"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regprocedure('call_exact(integer)') AS d,to_regprocedure('call_array(integer)') AS a,to_regprocedure('call_default()') AS omitted,to_regprocedure('call_variadic(integer)') AS expanded,to_regprocedure('call_variadic(integer,integer)') AS many", []string{"d", "a", "omitted", "expanded", "many"}, []string{"REGPROCEDURE", "REGPROCEDURE", "REGPROCEDURE", "REGPROCEDURE", "REGPROCEDURE"}, [][]any{{nil, nil, nil, nil, nil}}))
		regObjectExec(t, p, b, c, "CREATE TEMP TABLE call_temp_type(v integer)",
			"CREATE FUNCTION pg_temp.call_temp_target(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE OPERATOR pg_temp.#@# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)",
			"CREATE FUNCTION call_left.call_temp_operand(pg_temp.call_temp_type) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
			"SET search_path TO pg_temp,call_left,public")
		regObjectBoth(t, p, b, c, callableSelect(b, "SELECT to_regproc('call_temp_target') AS r,to_regprocedure('call_temp_target(integer)') AS p,to_regoper('#@#') AS o,to_regoperator('#@#(int,int)') AS s", []string{"r", "p", "o", "s"}, []string{"REGPROC", "REGPROCEDURE", "REGOPER", "REGOPERATOR"}, [][]any{{nil, nil, nil, nil}}))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT to_regprocedure('call_temp_operand(call_temp_type)')::oid='call_left.call_temp_operand(pg_temp.call_temp_type)'::regprocedure::oid AS operand,to_regprocedure('pg_temp.call_temp_target(integer)')::oid='pg_temp.call_temp_target(integer)'::regprocedure::oid AS explicit", []string{"operand", "explicit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		tempRoutine := regLookupIdentity(t, p, c, "SELECT 'pg_temp.call_temp_target(integer)'::regprocedure::oid::bigint")
		tempOperator := regLookupIdentity(t, p, c, "SELECT 'pg_temp.#@#(integer,integer)'::regoperator::oid::bigint")
		literalTemp := "SELECT to_regproc('pg_temp.call_temp_target')::oid::bigint AS r,to_regprocedure('pg_temp.call_temp_target(integer)')::oid::bigint AS p,to_regoper('pg_temp.#@#')::oid::bigint AS o,to_regoperator('pg_temp.#@#(int,int)')::oid::bigint AS s"
		parameterTemp := "SELECT to_regproc($1)::oid::bigint AS r,to_regprocedure($2)::oid::bigint AS p,to_regoper($3)::oid::bigint AS o,to_regoperator($4)::oid::bigint AS s"
		for _, route := range []struct {
			query string
			args  []any
		}{
			{literalTemp, nil},
			{parameterTemp, []any{"pg_temp.call_temp_target", "pg_temp.call_temp_target(integer)", "pg_temp.#@#", "pg_temp.#@#(int,int)"}},
		} {
			heldTemp := regLookupHeld(t, p, c, route.query)
			defer heldTemp.Statement.Close()
			s := fixture.Select(route.query, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{tempRoutine, tempRoutine, tempOperator, tempOperator}}, route.args...)
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, heldTemp, s)
			if route.args != nil {
				regObjectRunStep(t, p, b, heldTemp, fixture.Select(route.query, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{nil, nil, nil, nil}}, "call_temp_target", "call_temp_target(integer)", "#@#", "#@#(int,int)"))
				regObjectRunStep(t, p, b, heldTemp, s)
			}
			heldTemp.Statement.Close()
		}
		callablePrivileges(t, p, b, c)
	})
}

func callablePrivileges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectExec(t, p, b, c, "CREATE ROLE call_owner", "CREATE ROLE call_reader", "CREATE SCHEMA call_private AUTHORIZATION call_owner",
		"SET ROLE call_owner", "CREATE FUNCTION call_private.secret(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "RESET ROLE",
		"CREATE OPERATOR call_private.#?# (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4pl)",
		"REVOKE ALL ON FUNCTION call_private.secret(integer) FROM PUBLIC", "SET search_path TO call_missing_schema,call_private,public")
	id := regLookupIdentity(t, p, c, "SELECT 'call_private.secret(integer)'::regprocedure::oid::bigint")
	op := regLookupIdentity(t, p, c, "SELECT 'call_private.#?#(integer,integer)'::regoperator::oid::bigint")
	q := "SELECT to_regproc($1)::oid::bigint AS r,to_regprocedure($2)::oid::bigint AS p,to_regoper($3)::oid::bigint AS o,to_regoperator($4)::oid::bigint AS s"
	held := regLookupHeld(t, p, c, q)
	defer held.Statement.Close()
	regObjectExec(t, p, b, c, "SET ROLE call_reader")
	defer c.ExecContext(p.Ctx, "RESET ROLE")
	regObjectRunStep(t, p, b, held, fixture.Select(q, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{nil, nil, nil, nil}}, "secret", "secret(integer)", "#?#", "#?#(int,int)"))
	for _, tc := range []struct{ name, family, qualified, valid, output string }{
		{"to_regproc", "REGPROC", "call_private.secret", "boolin", "boolin"},
		{"to_regprocedure", "REGPROCEDURE", "call_private.secret(integer)", "abs(integer)", "abs(integer)"},
		{"to_regoper", "REGOPER", "call_private.#?#", "||/", "||/"},
		{"to_regoperator", "REGOPERATOR", "call_private.#?#(int,int)", "+(int,int)", "+(integer,integer)"},
	} {
		t.Run(tc.name+"/denied_schema", func(t *testing.T) {
			callableHardRoutes(t, p, b, c, tc.name, tc.qualified, tc.valid, tc.output, tc.family, callableDiagnostic("42501", "permission denied for schema call_private"))
		})
	}
	regObjectExec(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA call_private TO call_reader", "SET ROLE call_reader")
	regObjectRunStep(t, p, b, held, fixture.Select(q, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{id, id, op, op}}, "secret", "secret(integer)", "#?#", "#?#(int,int)"))
	t.Run("target_execution_denied", func(t *testing.T) {
		regObjectBoth(t, p, b, c, callableErrorStep("SELECT call_private.secret(1)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function secret")))
	})
	regObjectExec(t, p, b, c, "RESET ROLE")
	// Ordinary admission of the called builtin is independent of lookup-target
	// EXECUTE. Use a non-NULL argument: PostgreSQL can replace a strict call
	// with constant NULL during planning, so that would not exercise admission.
	// Always restore this shared catalog ACL before role cleanup.
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "RESET ROLE; GRANT EXECUTE ON FUNCTION pg_catalog.to_regproc(text) TO PUBLIC"); err != nil {
			t.Error(err)
		}
	}()
	regObjectExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.to_regproc(text) FROM PUBLIC", "SET ROLE call_reader")
	t.Run("builtin_execution_denied", func(t *testing.T) {
		regObjectBoth(t, p, b, c, callableErrorStep("SELECT to_regproc('boolin'::text)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function to_regproc")))
	})
	regObjectExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.to_regproc(text) TO PUBLIC", "SET ROLE call_reader")
	regObjectRunStep(t, p, b, held, fixture.Select(q, []string{"r", "p", "o", "s"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{id, id, op, op}}, "secret", "secret(integer)", "#?#", "#?#(int,int)"))
	regObjectExec(t, p, b, c, "RESET ROLE")
}
