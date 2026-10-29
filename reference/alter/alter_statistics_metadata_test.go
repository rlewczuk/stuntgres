package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticsMetadata(relation string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT attname,attstattarget,attoptions FROM pg_attribute WHERE attrelid='"+relation+"'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attstattarget", "attoptions"}, []string{"NAME", "INT2", "_TEXT"}, rows)
}
func statisticsError(query, code, message string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
}

func TestReferenceAlterStatisticsTargets(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer)", 0))
	p.Step(t, statisticsMetadata("statistics_t", []any{"a", nil, nil}))
	for i, target := range []string{"0", "1", "10000", "DEFAULT", "3", "-1"} {
		s := fixture.Exec("ALTER TABLE public.statistics_t ALTER COLUMN a SET STATISTICS "+target, 0)
		s.Prepared = i%2 == 1
		p.Step(t, s)
		var want any
		if target != "DEFAULT" && target != "-1" {
			var n int64
			if _, err := fmt.Sscan(target, &n); err != nil {
				t.Fatal(err)
			}
			want = n
		}
		p.Step(t, statisticsMetadata("statistics_t", []any{"a", want, nil}))
	}
	for _, tc := range []struct{ column, target, code, message string }{
		{"a", "-2", "22023", "statistics target -2 is too low"},
		{"missing", "3", "42703", `column "missing" of relation "statistics_t" does not exist`},
		{"ctid", "3", "0A000", `cannot alter system column "ctid"`},
	} {
		p.Step(t, statisticsError("ALTER TABLE statistics_t ALTER "+tc.column+" SET STATISTICS "+tc.target, tc.code, tc.message))
		p.Step(t, statisticsMetadata("statistics_t", []any{"a", nil, nil}))
	}
	p.Step(t, fixture.Exec("CREATE TABLE statistics_generated(a integer,v integer GENERATED ALWAYS AS(a) VIRTUAL,s integer GENERATED ALWAYS AS(a) STORED)", 0))
	p.Step(t, statisticsError("ALTER TABLE statistics_generated ALTER v SET STATISTICS 3", "0A000", `cannot alter statistics on virtual generated column "v"`))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_generated ALTER s SET STATISTICS 3", 0))
	p.Step(t, statisticsMetadata("statistics_generated", []any{"a", nil, nil}, []any{"v", nil, nil}, []any{"s", int64(3), nil}))
	p.Step(t, fixture.Exec(`CREATE TABLE "Statistics Quoted"("a""\name" integer)`, 0))
	p.Step(t, fixture.Prepared(fixture.Exec(`ALTER TABLE ONLY "public"."Statistics Quoted" ALTER "a""\name" SET STATISTICS 7`, 0)))
	p.Step(t, statisticsMetadata(`"Statistics Quoted"`, []any{"a\"\\name", int64(7), nil}))
	p.Step(t, fixture.Exec("ALTER TABLE IF EXISTS statistics_absent ALTER a SET STATISTICS 4", 0))
	for _, b := range p.Backends() {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 10001", 0))
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
		if len(notices) != 1 || notices[0] != want {
			t.Fatalf("%s warning: %+v", b.Name, notices)
		}
		fixture.RunStep(t, p.Ctx, c, rec, statisticsMetadata("statistics_t", []any{"a", int64(10000), nil}))
	}
}

// This is the complete release DBL_MAX %f literal, independently specified.
const statisticsBounds = `Valid values are between "-1.000000" and "179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368.000000".`

func TestReferenceAlterStatisticsOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer)", 0))
	for i, tc := range []struct {
		action  string
		options any
	}{
		{"SET (n_distinct=2,n_distinct_inherited=-0.5)", "{n_distinct=2,n_distinct_inherited=-0.5}"},
		{"SET (n_distinct=2.5)", "{n_distinct_inherited=-0.5,n_distinct=2.5}"},
		{"SET (n_distinct=0)", "{n_distinct_inherited=-0.5,n_distinct=0}"},
		{"RESET (n_distinct,bogus,n_distinct)", "{n_distinct_inherited=-0.5}"},
		{"RESET (n_distinct_inherited)", nil},
		{"SET (n_distinct='-1')", "{n_distinct=-1}"},
	} {
		s := fixture.Exec("ALTER TABLE statistics_t ALTER a "+tc.action, 0)
		s.Prepared = i%2 == 1
		p.Step(t, s)
		p.Step(t, statisticsMetadata("statistics_t", []any{"a", nil, tc.options}))
	}
	for _, tc := range []struct{ action, code, message, detail string }{
		{"SET (bogus=1)", "22023", `unrecognized parameter "bogus"`, ""},
		{"SET (n_distinct=1,n_distinct=2)", "22023", `parameter "n_distinct" specified more than once`, ""},
		{"SET (n_distinct='bad')", "22023", `invalid value for floating point option "n_distinct": bad`, ""},
		{"SET (n_distinct)", "22023", `invalid value for floating point option "n_distinct": true`, ""},
		{"SET (n_distinct=-2)", "22023", `value -2 out of bounds for option "n_distinct"`, statisticsBounds},
		{"SET (n_distinct='NaN')", "22023", `invalid value for floating point option "n_distinct": NaN`, ""},
		{"SET (n_distinct='1e400')", "22023", `invalid value for floating point option "n_distinct": 1e400`, ""},
		{"SET (n_distinct='1e-400')", "22023", `invalid value for floating point option "n_distinct": 1e-400`, ""},
		{"SET (n_distinct='-Infinity')", "22023", `value -Infinity out of bounds for option "n_distinct"`, statisticsBounds},
		{"SET (n_distinct='Infinity')", "22023", `value Infinity out of bounds for option "n_distinct"`, statisticsBounds},
		{"SET (n_distinct_inherited=-2)", "22023", `value -2 out of bounds for option "n_distinct_inherited"`, statisticsBounds},
		{"RESET (n_distinct=1)", "42601", "RESET must not include values for parameters", ""},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("ALTER TABLE statistics_t ALTER a "+tc.action, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "execute"
			}
			p.Step(t, s)
			p.Step(t, statisticsMetadata("statistics_t", []any{"a", nil, "{n_distinct=-1}"}))
		}
	}
	p.Step(t, fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 3, ALTER a SET(n_distinct=2,n_distinct_inherited=-0.5)", 0))
	sentinel := statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2,n_distinct_inherited=-0.5}"})
	for _, prepared := range []bool{false, true} {
		for _, option := range []string{
			"foo.n_distinct=2", "foo.n_distinct_inherited=-0.5", "foo.bogus=1",
			"n_distinct='bad',foo.n_distinct_inherited=2",
			"foo.n_distinct_inherited=2,n_distinct='bad'",
			"n_distinct_inherited='bad',foo.n_distinct=2",
			"foo.n_distinct=2,n_distinct_inherited='bad'",
			"bogus=1,foo.n_distinct=2",
			"n_distinct=1,n_distinct=2,foo.n_distinct_inherited=2",
			"n_distinct=-2,foo.bogus=1",
		} {
			query := "ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=4), ALTER a SET(" + option + ")"
			step := statisticsError(query, "22023", `unrecognized parameter namespace "foo"`)
			step.Prepared = prepared
			if prepared {
				step.ErrorStage = "execute"
			}
			p.Step(t, step)
			p.Step(t, sentinel)
		}
		for _, tc := range []struct{ options, message string }{
			{`"bad=name"=2,foo.n_distinct=2`, `invalid option name "bad=name": must not contain "="`},
			{`foo.n_distinct=2,"bad=name"=2`, `unrecognized parameter namespace "foo"`},
			{`n_distinct='bad',"bad=name"=2`, `invalid option name "bad=name": must not contain "="`},
			{`"bad=name"=2,n_distinct='bad'`, `invalid option name "bad=name": must not contain "="`},
		} {
			query := "ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=4), ALTER a SET(" + tc.options + ")"
			step := statisticsError(query, "22023", tc.message)
			step.Prepared = prepared
			if prepared {
				step.ErrorStage = "execute"
			}
			p.Step(t, step)
			p.Step(t, sentinel)
		}
		step := fixture.Exec("ALTER TABLE statistics_t ALTER a RESET(foo.n_distinct,foo.n_distinct_inherited,foo.bogus)", 0)
		step.Prepared = prepared
		p.Step(t, step)
		p.Step(t, sentinel)
		failure := statisticsError("ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a RESET(foo.n_distinct=1)", "42601", "RESET must not include values for parameters")
		failure.Prepared = prepared
		if prepared {
			failure.ErrorStage = "execute"
		}
		p.Step(t, failure)
		p.Step(t, sentinel)
	}
}

func TestReferenceAlterStatisticsHierarchy(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE TABLE statistics_p(a integer)", "ALTER TABLE statistics_p ALTER a SET STATISTICS 7, ALTER a SET (n_distinct=3)", "CREATE TABLE statistics_c() INHERITS(statistics_p)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	// Creation does not copy per-column statistics configuration.
	p.Step(t, statisticsMetadata("statistics_c", []any{"a", nil, nil}))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_p * ALTER a SET STATISTICS 4, ALTER a SET (n_distinct=2,n_distinct_inherited=-0.5)", 0))
	p.Step(t, statisticsMetadata("statistics_p", []any{"a", int64(4), "{n_distinct=2,n_distinct_inherited=-0.5}"}))
	p.Step(t, statisticsMetadata("statistics_c", []any{"a", int64(4), nil}))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE ONLY statistics_p ALTER a SET STATISTICS 1, ALTER a RESET (n_distinct)", 0)))
	p.Step(t, statisticsMetadata("statistics_p", []any{"a", int64(1), "{n_distinct_inherited=-0.5}"}))
	p.Step(t, statisticsMetadata("statistics_c", []any{"a", int64(4), nil}))
	for _, q := range []string{"CREATE TABLE statistics_part(a integer) PARTITION BY RANGE(a)", "CREATE TABLE statistics_leaf PARTITION OF statistics_part FOR VALUES FROM(0) TO(10)", "ALTER TABLE statistics_part ALTER a SET STATISTICS 3, ALTER a SET(n_distinct_inherited=2)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, statisticsMetadata("statistics_part", []any{"a", int64(3), "{n_distinct_inherited=2}"}))
	p.Step(t, statisticsMetadata("statistics_leaf", []any{"a", int64(3), nil}))
}
