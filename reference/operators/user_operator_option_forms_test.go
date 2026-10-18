package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorOptionExtraction(t *testing.T) {
	cases := []struct{ name, options, code, message string }{
		{"missing_left", "LEFTARG,RIGHTARG=int4,FUNCTION=int4pl", "42601", "leftarg requires a parameter"},
		{"numeric_left", "LEFTARG=7,RIGHTARG=int4,FUNCTION=int4pl", "42601", "argument of leftarg must be a type name"},
		{"symbolic_left", "LEFTARG=OPERATOR(public.+),RIGHTARG=int4,FUNCTION=int4pl", "42601", "argument of leftarg must be a type name"},
		{"missing_right", "LEFTARG=int4,RIGHTARG,FUNCTION=int4pl", "42601", "rightarg requires a parameter"},
		{"missing_function", "LEFTARG=int4,RIGHTARG=int4,FUNCTION,FUNCTION=int4pl", "42601", "function requires a parameter"},
		{"numeric_function", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=7,FUNCTION=int4pl", "42601", "argument of function must be a name"},
		{"numeric_procedure", "LEFTARG=int4,RIGHTARG=int4,PROCEDURE=7,PROCEDURE=int4pl", "42601", "argument of procedure must be a name"},
		{"missing_commutator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,COMMUTATOR", "42601", "commutator requires a parameter"},
		{"numeric_commutator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,COMMUTATOR=7", "42601", "argument of commutator must be a name"},
		{"invalid_string_commutator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl,COMMUTATOR='abc'", "42602", `"abc" is not a valid operator name`},
		{"invalid_string_negator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,NEGATOR='abc'", "42602", `"abc" is not a valid operator name`},
		{"missing_negator", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,NEGATOR", "42601", "negator requires a parameter"},
		{"missing_restrict", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT", "42601", "restrict requires a parameter"},
		{"numeric_restrict", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT=7", "42601", "argument of restrict must be a name"},
		{"missing_join", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,JOIN", "42601", "join requires a parameter"},
		{"numeric_join", "LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,JOIN=7", "42601", "argument of join must be a name"},
		{"single_component_type", "LEFTARG='pg_catalog.int4',RIGHTARG=int4,FUNCTION=int4pl", "42704", `type "pg_catalog.int4" does not exist`},
		{"single_component_function", "LEFTARG=int4,RIGHTARG=int4,FUNCTION='pg_catalog.int4pl'", "42883", "function pg_catalog.int4pl(integer, integer) does not exist"},
		{"type_before_postfix", "LEFTARG=userop_missing_type,FUNCTION=int4um", "42704", `type "userop_missing_type" does not exist`},
		{"function_before_type", "LEFTARG=userop_missing_type", "42P13", "operator function must be specified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE userop_option_sentinel(v integer)", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_option_sentinel VALUES(42)", 1))
				userOperatorError(t, p, b, c, "CREATE OPERATOR public.#%# ("+tc.options+")", tc.code, tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#%#'", "INT8", int64(0))
				userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_option_sentinel", "INT4", int64(42))
			})
		})
	}
}

func TestReferenceUserOperatorOptionBooleanForms(t *testing.T) {
	for _, attribute := range []string{"HASHES", "MERGES"} {
		for _, value := range []string{"'t'", "'yes'", "'no'", "'tru'", "'of'", "'1'", "2", "-1", "1.0"} {
			t.Run(attribute+"="+value, func(t *testing.T) {
				userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq)")
					message := "hashes requires a Boolean value"
					if attribute == "MERGES" {
						message = "merges requires a Boolean value"
					}
					userOperatorError(t, p, b, c, "CREATE OPERATOR public.#%# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,"+attribute+"="+value+")", "42601", message, "", "", "", false)
					if value == "2" || value == "-1" || value == "1.0" {
						message = "argument of hashes must be a name"
						if attribute == "MERGES" {
							message = "argument of merges must be a name"
						}
					}
					userOperatorError(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET ("+attribute+"="+value+")", "42601", message, "", "", "", false)
					userOperatorValue(t, p, b, c, "SELECT NOT oprcanhash AND NOT oprcanmerge AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
					userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#%#'", "INT8", int64(0))
					userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#=#) 2 AS v", "BOOL", true)
				})
			})
		}
	}
}

func TestReferenceUserOperatorOptionAcceptedForms(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE OPERATOR public.#~# (LEFTARG='int4',RIGHTARG='int4',FUNCTION='int4pl')",
			"CREATE OPERATOR public.##~ (RIGHTARG='text',PROCEDURE='lower')",
			"CREATE OPERATOR public.#/# (LEFTARG=userop_missing_type,LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
			"CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION='int4eq',COMMUTATOR='#=#',RESTRICT='eqsel',JOIN='eqjoinsel',HASHES,MERGES=1)")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT OPERATOR(public.##~) 'ABC'::text AS v", "TEXT", "abc")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#/#) 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT oprcom=oid AND oprcanhash AND oprcanmerge AND oprrest::oid=101 AND oprjoin::oid=105 AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
		for _, tc := range []struct {
			value string
			want  bool
		}{{"'TrUe'", true}, {"'ON'", true}, {"true", true}, {"on", true}, {"1", true}, {"'FaLsE'", false}, {"'OFF'", false}, {"false", false}, {"off", false}, {"0", false}} {
			userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,HASHES="+tc.value+",MERGES="+tc.value+")")
			userOperatorValue(t, p, b, c, "SELECT oprcanhash=$1::bool AND oprcanmerge=$1::bool AS v FROM pg_operator WHERE oid='public.#?#(int4,int4)'::regoperator", "BOOL", true, tc.want)
			if tc.value == "0" || tc.value == "1" {
				userOperatorError(t, p, b, c, "ALTER OPERATOR public.#?# (int4,int4) SET (HASHES="+tc.value+",MERGES="+tc.value+")", "42601", "argument of hashes must be a name", "", "", "", false)
			} else {
				userOperatorExec(t, p, b, c, "ALTER OPERATOR public.#?# (int4,int4) SET (HASHES="+tc.value+",MERGES="+tc.value+")")
			}
			if !tc.want {
				userOperatorExec(t, p, b, c, "ALTER OPERATOR public.#?# (int4,int4) SET (HASHES=on,MERGES=on)")
			}
			userOperatorValue(t, p, b, c, "SELECT oprcanhash AND oprcanmerge AS v FROM pg_operator WHERE oid='public.#?#(int4,int4)'::regoperator", "BOOL", true)
			userOperatorExec(t, p, b, c, "DROP OPERATOR public.#?# (int4,int4)")
		}
	})
}

func TestReferenceUserOperatorOptionAlterExtraction(t *testing.T) {
	for _, tc := range []struct{ option, message string }{
		{"FUNCTION=7", "argument of function must be a name"},
		{"PROCEDURE=7", "argument of procedure must be a name"},
		{"LEFTARG=7", "argument of leftarg must be a name"},
		{"RIGHTARG=7", "argument of rightarg must be a name"},
		{"userop_unknown=7", "argument of userop_unknown must be a name"},
		{"FUNCTION", `operator attribute "function" cannot be changed`},
		{"LEFTARG", `operator attribute "leftarg" cannot be changed`},
		{"COMMUTATOR", "commutator requires a parameter"},
		{"COMMUTATOR=NONE", "commutator requires a parameter"},
		{"NEGATOR", "negator requires a parameter"},
		{"NEGATOR=NONE", "negator requires a parameter"},
		{"COMMUTATOR=7", "argument of commutator must be a name"},
		{"NEGATOR=7", "argument of negator must be a name"},
		{"RESTRICT=7", "argument of restrict must be a name"},
		{"JOIN=7", "argument of join must be a name"},
		{"HASHES=1", "argument of hashes must be a name"},
		{"MERGES=0", "argument of merges must be a name"},
	} {
		t.Run(tc.option, func(t *testing.T) {
			userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,RESTRICT=eqsel,JOIN=eqjoinsel)")
				userOperatorError(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET ("+tc.option+")", "42601", tc.message, "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT oprrest::oid=101 AND oprjoin::oid=105 AND NOT oprcanhash AND NOT oprcanmerge AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
				userOperatorExec(t, p, b, c, "ALTER OPERATOR public.#=# (int4,int4) SET (RESTRICT,JOIN=NONE,HASHES,MERGES)", "ALTER OPERATOR public.#=# (int4,int4) SET (RESTRICT='eqsel',JOIN='eqjoinsel')")
				userOperatorValue(t, p, b, c, "SELECT oprrest::oid=101 AND oprjoin::oid=105 AND oprcanhash AND oprcanmerge AS v FROM pg_operator WHERE oid='public.#=#(int4,int4)'::regoperator", "BOOL", true)
			})
		})
	}
}
