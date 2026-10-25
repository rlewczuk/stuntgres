package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func movingNoResidue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ma_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_aggregate a LEFT JOIN pg_proc p ON p.oid=a.aggfnoid WHERE p.oid IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend d LEFT JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND p.oid IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func movingRejected(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, options, code, message string) {
	t.Helper()
	q := "CREATE AGGREGATE ma_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0'," + options + ")"
	lifecycleError(t, p, b, c, q, code, message)
	movingNoResidue(t, p, b, c)
	// A corrected same-name definition must not encounter half-published state.
	lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_rejected(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='0')", "DROP AGGREGATE ma_rejected(integer)")
}

func TestReferenceAggregateMovingOptions(t *testing.T) {
	t.Run("boolean_shapes", movingBooleanOptions)
	t.Run("seed_environment", movingSeedEnvironment)
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		movingSetup(t, p, b, c)
		for _, space := range []int64{-2147483647, -1, 0, 2147483647} {
			name := "ma_space"
			if space < 0 {
				name += "_n" + strconv.FormatInt(-space, 10)
			} else {
				name += "_p" + strconv.FormatInt(space, 10)
			}
			lifecycleExec(t, p, b, c, "CREATE AGGREGATE "+name+"(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int4,MINITCOND='000',MSSPACE="+strconv.FormatInt(space, 10)+")")
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggmtransspace,a.aggminitval FROM (SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"aggmtransspace", "aggminitval"}, []string{"INT4", "TEXT"}, [][]any{{space, "000"}}))
			createAggregateBoth(t, p, b, c, movingRows("SELECT id,"+name+"(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
		}
		lifecycleExec(t, p, b, c,
			"CREATE FUNCTION ma_option_final(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE AGGREGATE ma_flags(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,MSSPACE=0,MFINALFUNC_EXTRA,MFINALFUNC_MODIFY=shareable)",
			"CREATE AGGREGATE ma_duplicate(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.int4mi,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=int8,MSTYPE=int4,MSSPACE=1,MSSPACE=-9,MINITCOND='99',MINITCOND='000',MFINALFUNC=pg_catalog.abs,MFINALFUNC=ma_option_final,MFINALFUNC_EXTRA=true,MFINALFUNC_EXTRA=false,MFINALFUNC_MODIFY=read_write,MFINALFUNC_MODIFY=read_only)")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggmtransfn=0::oid AS forward,a.aggminvtransfn=0::oid AS inverse,a.aggmfinalfn=0::oid AS final,a.aggmtranstype=0::oid AS state,a.aggmtransspace,a.aggminitval,a.aggmfinalextra,a.aggmfinalmodify::text AS mode FROM (SELECT oid FROM pg_proc WHERE proname='ma_flags') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"forward", "inverse", "final", "state", "aggmtransspace", "aggminitval", "aggmfinalextra", "mode"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{true, true, true, true, int64(0), nil, true, "s"}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggmtransfn='pg_catalog.int4pl(integer,integer)'::regprocedure AS forward,a.aggminvtransfn='pg_catalog.int4mi(integer,integer)'::regprocedure AS inverse,a.aggmfinalfn='ma_option_final(integer)'::regprocedure AS final,a.aggmtranstype=23::oid AS state,a.aggmtransspace,a.aggminitval,a.aggmfinalextra,a.aggmfinalmodify::text AS mode FROM (SELECT oid FROM pg_proc WHERE proname='ma_duplicate') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"forward", "inverse", "final", "state", "aggmtransspace", "aggminitval", "aggmfinalextra", "mode"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{true, true, true, true, int64(-9), "000", false, "r"}}))
		createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_duplicate(x) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM ma_input ORDER BY id", int64(2), int64(5), int64(8), int64(12)))
	})
}

func TestReferenceAggregateMovingDiagnostics(t *testing.T) {
	t.Run("whole_input", movingWholeInput)
	for _, tc := range []struct{ name, options, code, message string }{
		{"missing_forward", "MSTYPE=int4", "42P13", "aggregate msfunc must be specified when mstype is specified"},
		{"missing_inverse", "MSTYPE=int4,MSFUNC=pg_catalog.int4pl", "42P13", "aggregate minvfunc must be specified when mstype is specified"},
		{"forward_without_state", "MSFUNC=pg_catalog.int4pl", "42P13", "aggregate msfunc must not be specified without mstype"},
		{"inverse_without_state", "MINVFUNC=pg_catalog.int4mi", "42P13", "aggregate minvfunc must not be specified without mstype"},
		{"final_without_state", "MFINALFUNC=pg_catalog.abs", "42P13", "aggregate mfinalfunc must not be specified without mstype"},
		{"space_without_state", "MSSPACE=-1", "42P13", "aggregate msspace must not be specified without mstype"},
		{"seed_without_state", "MINITCOND='0'", "42P13", "aggregate minitcond must not be specified without mstype"},
		{"bare_mode", "MFINALFUNC_MODIFY", "42601", "mfinalfunc_modify requires a parameter"},
		{"bad_mode", "MFINALFUNC_MODIFY=banana", "42601", "parameter \"mfinalfunc_modify\" must be READ_ONLY, SHAREABLE, or READ_WRITE"},
		{"quoted_mode", "MFINALFUNC_MODIFY='READ_ONLY'", "42601", "parameter \"mfinalfunc_modify\" must be READ_ONLY, SHAREABLE, or READ_WRITE"},
		{"bad_extra", "MFINALFUNC_EXTRA='banana'", "42601", "mfinalfunc_extra requires a Boolean value"},
		{"bare_space", "MSSPACE", "42601", "msspace requires an integer value"},
		{"bad_space", "MSSPACE='banana'", "42601", "msspace requires an integer value"},
		{"minimum_int_spelling", "MSSPACE=-2147483648", "42601", "msspace requires an integer value"},
		{"quoted_space", "MSSPACE='1'", "42601", "msspace requires an integer value"},
		{"bare_seed", "MINITCOND", "42601", "minitcond requires a parameter"},
		{"bad_seed", "MSTYPE=int4,MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MINITCOND='bad'", "22P02", "invalid input syntax for type integer: \"bad\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				movingRejected(t, p, b, c, tc.options, tc.code, tc.message)
			})
		})
	}
}

func TestReferenceAggregateMovingSignatures(t *testing.T) {
	for _, tc := range []struct{ name, options, code, message string }{
		{"forward_result", "MSFUNC=ma_wrong,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0'", "42804", "return type of transition function ma_wrong is not integer"},
		{"inverse_result", "MSFUNC=ma_step,MINVFUNC=ma_wrong,MSTYPE=int4,MINITCOND='0'", "42804", "return type of inverse transition function ma_wrong is not integer"},
		{"strict_mismatch", "MSFUNC=pg_catalog.int4pl,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0'", "42P13", "strictness of aggregate's forward and inverse transition functions must match"},
		{"missing_inverse", "MSFUNC=ma_step,MINVFUNC=ma_absent,MSTYPE=int4,MINITCOND='0'", "42883", "function ma_absent(integer, integer) does not exist"},
		{"inverse_arity", "MSFUNC=ma_step,MINVFUNC=ma_unary,MSTYPE=int4,MINITCOND='0'", "42883", "function ma_unary(integer, integer) does not exist"},
		{"set_forward", "MSFUNC=pg_catalog.generate_series,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0'", "42804", "function pg_catalog.generate_series(integer, integer) returns a set"},
		{"runtime_coercion", "MSFUNC=ma_wide,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0'", "42804", "function ma_wide(bigint, bigint) requires run-time type coercion"},
		{"strict_extra", "MSFUNC=ma_step,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC=pg_catalog.int4pl,MFINALFUNC_EXTRA", "42P13", "final function with extra arguments must not be declared STRICT"},
		{"result_mismatch", "MSFUNC=ma_step,MINVFUNC=ma_inverse,MSTYPE=int4,MINITCOND='0',MFINALFUNC=ma_big_final", "42P13", "moving-aggregate implementation returns type bigint, but plain implementation returns type integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lifecycleExec(t, p, b, c,
					"CREATE FUNCTION ma_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$",
					"CREATE FUNCTION ma_inverse(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1-$2$$",
					"CREATE FUNCTION ma_unary(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
					"CREATE FUNCTION ma_wrong(integer,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT ($1+$2)::bigint$$",
					"CREATE FUNCTION ma_wide(bigint,bigint) RETURNS integer LANGUAGE SQL AS $$SELECT ($1+$2)::integer$$",
					"CREATE FUNCTION ma_big_final(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1::bigint$$")
				movingRejected(t, p, b, c, tc.options, tc.code, tc.message)
			})
		})
	}
	t.Run("strict_seed", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lifecycleExec(t, p, b, c, "CREATE FUNCTION ma_final(date) RETURNS integer LANGUAGE SQL AS $$SELECT 0$$")
			movingRejected(t, p, b, c, "MSFUNC=pg_catalog.date_pli,MINVFUNC=pg_catalog.date_mii,MSTYPE=date,MFINALFUNC=ma_final", "42P13", "must not omit initial value when transition function is strict and transition type is not compatible with input type")
			lifecycleError(t, p, b, c, "CREATE AGGREGATE ma_zero(*) (SFUNC=pg_catalog.abs,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.abs,MINVFUNC=pg_catalog.abs,MSTYPE=int4,MFINALFUNC=pg_catalog.abs,MFINALFUNC_EXTRA)", "42P13", "must not omit initial value when transition function is strict and transition type is not compatible with input type")
			lifecycleError(t, p, b, c, "CREATE AGGREGATE ma_zero(*) (SFUNC=pg_catalog.abs,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.abs,MINVFUNC=pg_catalog.abs,MSTYPE=int4,MINITCOND='0',MFINALFUNC=pg_catalog.abs,MFINALFUNC_EXTRA)", "42P13", "final function with extra arguments must not be declared STRICT")
			lifecycleExec(t, p, b, c, "CREATE AGGREGATE ma_zero_seeded(*) (SFUNC=pg_catalog.abs,STYPE=int4,INITCOND='0',MSFUNC=pg_catalog.abs,MINVFUNC=pg_catalog.abs,MSTYPE=int4,MINITCOND='0')")
			createAggregateBoth(t, p, b, c, movingRows("SELECT id,ma_zero_seeded(*) OVER(ORDER BY id ROWS 1 PRECEDING) AS v FROM(VALUES(1),(2),(3)) s(id) ORDER BY id", int64(0), int64(0), int64(0)))
		})
	})
}
