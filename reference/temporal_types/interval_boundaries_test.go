package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalGenericTypmodNativeRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := []struct {
		mask int
		out  string
	}{{4, "1 year"}, {2, "1 year 1 mon"}, {8, "1 year 1 mon 2 days"}, {1024, "1 year 1 mon 2 days 03:00:00"}, {2048, "1 year 1 mon 2 days 03:04:00"}, {6, "1 year 1 mon"}, {1032, "1 year 1 mon 2 days 03:00:00"}, {3080, "1 year 1 mon 2 days 03:04:00"}, {3072, "1 year 1 mon 2 days 03:04:00"}}
	for _, backend := range p.Backends() {
		for i, tc := range cases {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(fmt.Sprintf(`CREATE TABLE g%d(v "interval"(%d,2))`, i, tc.mask), 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO g%d VALUES('13 months 2 days 3:04:05.6789')", i), 1))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(fmt.Sprintf("SELECT v FROM g%d", i), []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(`CREATE TABLE ivdefault(id INTEGER,v "interval"(4,2) DEFAULT '13 months')`, 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO ivdefault(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(`CREATE VIEW ivgenericview AS SELECT v FROM g0`, 0))
		for _, q := range []string{"SELECT v FROM ivdefault", "SELECT v FROM ivgenericview"} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 year"}}))
		}
	}

}

func TestReferenceIntervalBoundaryDiagnosticsAndAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, input, usage, code, primary, sentinel string }{
		{"iso_first", "PT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_later", "P1YT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_date_first", "P1000000000000001Y", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_date_later", "P1Y1000000000000001M", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_month", "P2-1000000000000001-1", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_day", "P2-1-1000000000000001", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_minute", "PT1:1000000000000001:1", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_second", "PT1:1:1000000000000001", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"year_month", "1 1-2", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"year_month_year", "1 1-2", "INTERVAL YEAR", "22007", "invalid input syntax for type interval", "00:00:00"},
		{"year_month_minute", "1 1-2", "INTERVAL MINUTE TO SECOND(2)", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"precision", "2562047788:00:54.775807", "INTERVAL SECOND(2)", "22008", "interval out of range", "00:00:01"},
		{"original_iso_leading", " PT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_trailing", "PT1S ", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_hex_leading", " P1DT0x1p0S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_tab", "\tPT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_newline", "PT1S\n", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_nbsp", "\u00a0PT1S", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_verbose_nbsp", "\u00a01 second", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_infinity_nbsp", "\u00a0infinity", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_iso_nbsp_trailing", "PT1S\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_verbose_nbsp_trailing", "1 second\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"original_infinity_nbsp_trailing", "infinity\u00a0", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"precision_offset_positive", "9223372036854775804 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"precision_offset_negative", "-9223372036854775804 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"precision_offset_closest_positive", "9223372036854775803 microseconds", "INTERVAL SECOND(5)", "22008", "interval out of range", "00:00:01"},
		{"clock_signed_minimum", "-2562047788:00:54.775808", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"clock_signed_positive", "+2562047788:00:54.775808", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"clock_unsigned_overflow", "2562047788:00:54.775808", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE iv(v "+tc.usage+")", 0))
					run(fixture.Exec("INSERT INTO iv VALUES('1 second')", 1))
					message := tc.primary
					if tc.code != "22008" {
						message += ": \"" + tc.input + "\""
					}
					base := fixture.Diagnostic{Code: tc.code, Message: message}
					sentinel := func() {
						run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.sentinel}}))
					}
					for _, prepared := range []bool{false, true} {
						for _, literal := range []struct{ q, pos string }{{"INSERT INTO iv VALUES('" + tc.input + "')", "23"}, {"INSERT INTO iv VALUES('1 day'),('" + tc.input + "')", "33"}} {
							want := base
							want.Position = literal.pos
							s := fixture.ImplicitError(literal.q, want)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							run(s)
							sentinel()
						}
						for _, parameter := range []struct {
							q, context string
							args       []any
						}{
							{"INSERT INTO iv VALUES($1)", "unnamed portal parameter $1 = '...'", []any{tc.input}},
							{"INSERT INTO iv VALUES($1),($2)", "unnamed portal parameter $2 = '...'", []any{"1 day", tc.input}},
						} {
							s := fixture.ImplicitError(parameter.q, base, parameter.args...)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if tc.code != "22008" {
								pg := *s.Diagnostic
								pg.Context = parameter.context
								s.Diagnostic = &pg
							}
							run(s)
							sentinel()
						}
					}
					q := "SELECT INTERVAL '" + tc.input + "'"
					if tc.code == "22008" {
						precision := "2"
						if tc.usage == "INTERVAL SECOND(5)" {
							precision = "5"
						}
						q += " SECOND(" + precision + ")"
					}
					q += " FROM iv"
					for _, prepared := range []bool{false, true} {
						want := base
						want.Position = "17"
						s := fixture.ImplicitError(q, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						sentinel()
					}
				})
			}
		})
	}
}

func TestReferenceIntervalISOAlternativeExponentWidth(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
		var populated bool
		for _, tc := range []struct{ input, out string }{{"P00021015e0", "2 years 10 mons 15 days"}, {"P00021015.0e0", "2 years 10 mons 15 days"}, {"PT001020e0", "00:10:20"}, {"PT001020.0e0", "00:10:20"}} {
			for _, prepared := range []bool{false, true} {
				for _, parameterized := range []bool{false, true} {
					if populated {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DELETE FROM iv", 1))
					}
					s := fixture.Exec("INSERT INTO iv VALUES('"+tc.input+"')", 1)
					if parameterized {
						s = fixture.Exec("INSERT INTO iv VALUES($1)", 1, tc.input)
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					populated = true
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
				}
			}
		}
	}
}

func TestReferenceIntervalPrecisionOverflowSessionIsolation(t *testing.T) {
	for _, tc := range []struct{ name, usage, input string }{
		{"rounded_result", "INTERVAL SECOND(2)", "2562047788:00:54.775807"},
		{"offset_positive", "INTERVAL SECOND(5)", "9223372036854775804 microseconds"},
		{"offset_negative", "INTERVAL SECOND(5)", "-9223372036854775804 microseconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE iv(v "+tc.usage+")", 0))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO iv VALUES('1 second')", 1))
				a, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				b, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("SET IntervalStyle=sql_standard", 0))
				fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Exec("SET IntervalStyle=iso_8601", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("INSERT INTO iv VALUES($1),($2)", fixture.Diagnostic{Code: "22008", Message: "interval out of range"}, "1 day", tc.input)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"sql_standard"}}))
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"0:00:01"}}))
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"PT1S"}}))
				}
			}
		})
	}
}

func TestReferenceIntervalOriginalBoundaryAndPrecisionControls(t *testing.T) {
	for _, tc := range []struct{ name, input, usage, qualifier, out string }{
		{"verbose_leading", " 1 second", "INTERVAL", "", "00:00:01"},
		{"verbose_trailing", "1 second ", "INTERVAL", "", "00:00:01"},
		{"verbose_c_whitespace", "\t1 second\n", "INTERVAL", "", "00:00:01"},
		{"verbose_all_c_whitespace", " \t1 second\r\n\f\v", "INTERVAL", "", "00:00:01"},
		{"infinity_leading", " infinity", "INTERVAL", "", "infinity"},
		{"infinity_trailing", "infinity ", "INTERVAL", "", "infinity"},
		{"negative_infinity_c_whitespace", "\t-infinity\n", "INTERVAL", "", "-infinity"},
		{"offset_positive_neighbor", "9223372036854775802 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "2562047788:00:54.7758"},
		{"offset_negative_neighbor", "-9223372036854775803 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-2562047788:00:54.7758"},
		{"offset_positive_multiple", "9223372036854775800 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "2562047788:00:54.7758"},
		{"offset_negative_multiple", "-9223372036854775800 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-2562047788:00:54.7758"},
		{"unrestricted_positive", "9223372036854775804 microseconds", "INTERVAL", "", "2562047788:00:54.775804"},
		{"unrestricted_negative", "-9223372036854775804 microseconds", "INTERVAL", "", "-2562047788:00:54.775804"},
		{"precision_six_positive", "9223372036854775804 microseconds", "INTERVAL SECOND(6)", " SECOND(6)", "2562047788:00:54.775804"},
		{"precision_six_negative", "-9223372036854775804 microseconds", "INTERVAL SECOND(6)", " SECOND(6)", "-2562047788:00:54.775804"},
		{"restriction_before_offset_positive", "9223372036854775804 microseconds", "\"interval\"(8,5)", " DAY", "00:00:00"},
		{"restriction_before_offset_negative", "-9223372036854775804 microseconds", "\"interval\"(8,5)", " DAY", "00:00:00"},
		{"positive_tie", "5 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "00:00:00.00001"},
		{"negative_tie", "-5 microseconds", "INTERVAL SECOND(5)", " SECOND(5)", "-00:00:00.00001"},
		{"precision_positive_infinity", "infinity", "INTERVAL SECOND(5)", " SECOND(5)", "infinity"},
		{"precision_negative_infinity", "-infinity", "INTERVAL SECOND(5)", " SECOND(5)", "-infinity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE iv(v "+tc.usage+")", 0))
					var previous int64
					for _, prepared := range []bool{false, true} {
						for _, parameterized := range []bool{false, true} {
							run(fixture.Exec("DELETE FROM iv", previous))
							s := fixture.Exec("INSERT INTO iv VALUES('"+tc.input+"')", 1)
							if parameterized {
								s = fixture.Exec("INSERT INTO iv VALUES($1)", 1, tc.input)
							}
							s.Prepared = prepared
							run(s)
							previous = 1
							run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
							s = fixture.Select("SELECT INTERVAL '"+tc.input+"'"+tc.qualifier+" AS v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}})
							s.Prepared = prepared
							run(s)
						}
					}
				})
			}
		})
	}
}
