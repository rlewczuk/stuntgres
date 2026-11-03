package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankSignatures(t *testing.T) {
	// REL_18_6 pg_proc.dat and pg_proc.h defaults. These are actual
	// executable pg_proc rows, not the broader identity-only regprocedure map.
	rows := [][]any{
		{int64(3703), "ts_rank", int64(700), "1021 3614 3615 23", "ts_rank_wttf", "i", float64(1)},
		{int64(3704), "ts_rank", int64(700), "1021 3614 3615", "ts_rank_wtt", "i", float64(1)},
		{int64(3705), "ts_rank", int64(700), "3614 3615 23", "ts_rank_ttf", "i", float64(1)},
		{int64(3706), "ts_rank", int64(700), "3614 3615", "ts_rank_tt", "i", float64(1)},
		{int64(3707), "ts_rank_cd", int64(700), "1021 3614 3615 23", "ts_rankcd_wttf", "i", float64(1)},
		{int64(3708), "ts_rank_cd", int64(700), "1021 3614 3615", "ts_rankcd_wtt", "i", float64(1)},
		{int64(3709), "ts_rank_cd", int64(700), "3614 3615 23", "ts_rankcd_ttf", "i", float64(1)},
		{int64(3710), "ts_rank_cd", int64(700), "3614 3615", "ts_rankcd_tt", "i", float64(1)},
		{int64(3743), "ts_headline", int64(25), "3734 25 3615 25", "ts_headline_byid_opt", "i", float64(100)},
		{int64(3744), "ts_headline", int64(25), "3734 25 3615", "ts_headline_byid", "i", float64(100)},
		{int64(3754), "ts_headline", int64(25), "25 3615 25", "ts_headline_opt", "s", float64(100)},
		{int64(3755), "ts_headline", int64(25), "25 3615", "ts_headline", "s", float64(100)},
	}
	for i := range rows {
		rows[i] = append(rows[i], true, "s", false, "f", int64(12), int64(0), int64(0), nil, nil, nil, nil, nil)
	}
	p := fixture.NewScenario(t)
	s := fixture.Select("SELECT oid::bigint,proname,prorettype::bigint,proargtypes::text,prosrc,provolatile,procost::double precision,proisstrict,proparallel,proretset,prokind,prolang::bigint,pronargdefaults::bigint,provariadic::bigint,proargnames::text,proallargtypes::text,proargmodes::text,proargdefaults::text,proconfig::text FROM pg_catalog.pg_proc WHERE oid IN (3703,3704,3705,3706,3707,3708,3709,3710,3743,3744,3754,3755) ORDER BY oid",
		[]string{"oid", "proname", "prorettype", "proargtypes", "prosrc", "provolatile", "procost", "proisstrict", "proparallel", "proretset", "prokind", "prolang", "pronargdefaults", "provariadic", "proargnames", "proallargtypes", "proargmodes", "proargdefaults", "proconfig"},
		[]string{"INT8", "NAME", "INT8", "TEXT", "TEXT", "CHAR", "FLOAT8", "BOOL", "CHAR", "BOOL", "CHAR", "INT8", "INT8", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, rows)
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	for _, expression := range []struct {
		sql string
		oid int64
	}{
		{"ts_rank('cat:1'::tsvector,'cat'::tsquery)", 700},
		{"ts_rank_cd(NULL::tsvector,'cat'::tsquery)", 700},
		{"ts_headline('english'::regconfig,'cat','cat'::tsquery)", 25},
		{"ts_headline(NULL::text,'cat'::tsquery)", 25},
	} {
		s := fixture.Select("SELECT pg_typeof("+expression.sql+")::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{expression.oid}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	// The release has genuine JSON overloads; this part must not publish
	// them natively. This is the same full-membership distinction as the
	// builtin inventory, not an assertion that PostgreSQL lacks JSON.
	for _, b := range p.Backends() {
		want := int64(0)
		if b.PG {
			want = 8
		}
		s := fixture.Select("SELECT count(*)::bigint AS excluded FROM pg_catalog.pg_proc WHERE oid IN (4201,4202,4203,4204,4205,4206,4207,4208)", []string{"excluded"}, []string{"INT8"}, [][]any{{want}})
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
}
