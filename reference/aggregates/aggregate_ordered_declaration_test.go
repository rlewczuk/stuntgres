package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateOrderedModesSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, kind := range []string{"n", "o", "h"} {
		signature, options := "integer", ""
		if kind != "n" {
			signature = "integer ORDER BY integer"
		}
		if kind == "h" {
			options = ",HYPOTHETICAL"
		}
		for _, final := range []bool{false, true} {
			name, finalOption := "ua_decl_mode_"+kind, ""
			if final {
				name += "_final"
				finalOption = ",FINALFUNC=pg_catalog.int4pl"
				if kind == "n" {
					finalOption = ",FINALFUNC=pg_catalog.abs"
				}
			}
			createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE "+name+"("+signature+") (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0'"+finalOption+options+")", 0))
		}
		if kind == "n" {
			continue
		}
		for _, tc := range []struct{ suffix, final, moving string }{
			{"rs", "read_only", "shareable"},
			{"sw", "shareable", "read_write"},
			{"wr", "read_write", "read_only"},
		} {
			createAggregateRun(t, p, b, c, fixture.Prepared(fixture.Exec("CREATE AGGREGATE ua_decl_mode_"+kind+"_"+tc.suffix+"("+signature+") (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC_MODIFY="+tc.final+",MFINALFUNC_MODIFY="+tc.moving+options+")", 0)))
		}
	}
}

func aggregateOrderedModesCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) {
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
	}
	for _, kind := range []string{"n", "o", "h"} {
		mode := "w"
		if kind == "n" {
			mode = "r"
		}
		for _, final := range []bool{false, true} {
			name := "ua_decl_mode_" + kind
			if final {
				name += "_final"
			}
			run(fixture.Select("SELECT a.aggkind::text AS kind,a.aggfinalmodify::text AS final_mode,a.aggmfinalmodify::text AS moving_mode,a.aggfinalfn=0::oid AS nofinal,a.aggmfinalfn=0::oid AS nomovingfinal FROM(SELECT oid FROM pg_proc WHERE proname='"+name+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"kind", "final_mode", "moving_mode", "nofinal", "nomovingfinal"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{kind, mode, mode, !final, true}}))
			call, want := name+"(10) WITHIN GROUP(ORDER BY x)", int64(6)
			if kind == "n" {
				call = name + "(x)"
			} else if final {
				want = 16
			}
			run(createAggregateValue("SELECT "+call+" AS v FROM(VALUES(1),(2),(3)) s(x)", want))
		}
		if kind == "n" {
			continue
		}
		for _, tc := range []struct{ suffix, final, moving string }{{"rs", "r", "s"}, {"sw", "s", "w"}, {"wr", "w", "r"}} {
			run(fixture.Select("SELECT a.aggfinalmodify::text AS final_mode,a.aggmfinalmodify::text AS moving_mode,a.aggfinalfn=0::oid AS nofinal,a.aggmfinalfn=0::oid AS nomovingfinal FROM(SELECT oid FROM pg_proc WHERE proname='ua_decl_mode_"+kind+"_"+tc.suffix+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"final_mode", "moving_mode", "nofinal", "nomovingfinal"}, []string{"TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{tc.final, tc.moving, true, true}}))
		}
	}
}

func TestReferenceAggregateOrderedModes(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateOrderedModesSetup(t, p, b, c)
		aggregateOrderedModesCheck(t, p, c, b.Recorder)
	})
}

func aggregateOrderedStrictDeclarationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE AGGREGATE ua_seed_check(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", 0),
		fixture.Prepared(fixture.Exec("CREATE AGGREGATE ua_mseed_check(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=integer,MINITCOND='0')", 0)))
}

func aggregateOrderedStrictDeclarationCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, name := range []string{"ua_seed_check", "ua_mseed_check"} {
		for _, prepared := range []bool{false, true} {
			s := createAggregateValue("SELECT "+name+"('not a transition input') WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, db, recorder, s)
		}
	}
	catalog := fixture.Select("SELECT a.aggtransfn='pg_catalog.int4pl(integer,integer)'::regprocedure AS step,a.aggmtransfn='pg_catalog.int4pl(integer,integer)'::regprocedure AS moving_step,a.aggminvtransfn='pg_catalog.int4mi(integer,integer)'::regprocedure AS inverse,a.agginitval,a.aggminitval FROM(SELECT oid FROM pg_proc WHERE proname='ua_mseed_check') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"step", "moving_step", "inverse", "agginitval", "aggminitval"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, true, "0", "0"}})
	fixture.RunStep(t, p.Ctx, db, recorder, catalog)
	fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(catalog))
}

func TestReferenceAggregateOrderedStrictDeclaration(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, moving := range []bool{false, true} {
			options, retry := "", ",INITCOND='0'"
			if moving {
				options = ",INITCOND='0',MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=integer"
				retry = ",MINITCOND='0'"
			}
			ddl := "CREATE AGGREGATE ua_rejected(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer" + options
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(ddl+")", fixture.Diagnostic{Code: "42P13", Message: "must not omit initial value when transition function is strict and transition type is not compatible with input type"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				aggregateOrderedNoResidue(t, p, b, c)
				repaired := fixture.Exec(ddl+retry+")", 0)
				repaired.Prepared = prepared
				createAggregateRun(t, p, b, c, repaired)
				createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ua_rejected('prefix') WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x)", int64(6)))
				createAggregateRun(t, p, b, c, fixture.Exec("DROP AGGREGATE ua_rejected(text ORDER BY integer)", 0))
			}
		}
		aggregateOrderedStrictDeclarationSetup(t, p, b, c)
		aggregateOrderedStrictDeclarationCheck(t, p, c, b.Recorder)
	})
}

func aggregateOrderedMovingSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ua_moving_finish(integer,text) RETURNS text LANGUAGE SQL AS $$SELECT $2||$1::text$$", 0),
		fixture.Exec("CREATE FUNCTION ua_moving_extra_finish(integer,text,integer) RETURNS text LANGUAGE SQL AS $$SELECT $2||$1::text||';extra='||($3 IS NULL)::text$$", 0))
	for _, extra := range []bool{false, true} {
		name, support, flags := "ua_moving_plain", "ua_moving_finish", ""
		if extra {
			name, support, flags = "ua_moving_extra", "ua_moving_extra_finish", ",FINALFUNC_EXTRA,MFINALFUNC_EXTRA"
		}
		for _, prepared := range []bool{false, true} {
			actualName := name
			if prepared {
				actualName += "_prepared"
			}
			s := fixture.Exec("CREATE AGGREGATE "+actualName+"(text ORDER BY integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0',FINALFUNC="+support+",MSFUNC=pg_catalog.int4pl,MINVFUNC=pg_catalog.int4mi,MSTYPE=integer,MINITCOND='0',MFINALFUNC="+support+",FINALFUNC_MODIFY=read_only,MFINALFUNC_MODIFY=read_only"+flags+")", 0)
			s.Prepared = prepared
			createAggregateRun(t, p, b, c, s)
		}
	}
}

func aggregateOrderedMovingCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) {
		fixture.RunStep(t, p.Ctx, db, recorder, s)
		fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(s))
	}
	for _, extra := range []bool{false, true} {
		name, support, signature, suffix := "ua_moving_plain", "ua_moving_finish", "integer,text", ""
		if extra {
			name, support, signature, suffix = "ua_moving_extra", "ua_moving_extra_finish", "integer,text,integer", ";extra=true"
		}
		for _, prepared := range []bool{false, true} {
			actualName := name
			if prepared {
				actualName += "_prepared"
			}
			for _, tc := range []struct{ condition, value string }{{"TRUE", "p:6"}, {"FALSE", "p:0"}} {
				run(aggregateFinalText("SELECT "+actualName+"('p:') WITHIN GROUP(ORDER BY x) AS v FROM(VALUES(1),(2),(3)) s(x) WHERE "+tc.condition, tc.value+suffix))
			}
			run(fixture.Select("SELECT a.aggfinalfn='"+support+"("+signature+")'::regprocedure AS final,a.aggmfinalfn='"+support+"("+signature+")'::regprocedure AS moving_final,a.aggfinalextra,a.aggmfinalextra,a.aggfinalmodify::text AS final_mode,a.aggmfinalmodify::text AS moving_mode,a.aggmtransfn='pg_catalog.int4pl(integer,integer)'::regprocedure AS moving_step,a.aggminvtransfn='pg_catalog.int4mi(integer,integer)'::regprocedure AS inverse FROM(SELECT oid FROM pg_proc WHERE proname='"+actualName+"') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"final", "moving_final", "aggfinalextra", "aggmfinalextra", "final_mode", "moving_mode", "moving_step", "inverse"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{true, true, extra, extra, "r", "r", true, true}}))
		}
	}
}

func TestReferenceAggregateOrderedMovingSupport(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateOrderedMovingSetup(t, p, b, c)
		aggregateOrderedMovingCheck(t, p, c, b.Recorder)
		for _, name := range []string{"ua_moving_plain", "ua_moving_extra"} {
			q := "SELECT " + name + "('p:') WITHIN GROUP(ORDER BY x) OVER() FROM(VALUES(1),(2),(3)) s(x)"
			orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "0A000", "OVER is not supported for ordered-set aggregate "+name, name, ""))
		}
	})
}
