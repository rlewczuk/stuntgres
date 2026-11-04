package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Unquoted NumericOnly setting values: PostgreSQL flattens FCONST text verbatim
// (only a '+' sign drops, '-' prefixes) and each setting converts that text.
type settingsFractionalTarget struct {
	name, prior, bounds, unitsHint string
}

var settingsFractionalTargets = []settingsFractionalTarget{
	{"random_page_cost", "3", "(0 .. 1.79769e+308)", ""},
	{"vacuum_cost_delay", "7ms", "(0 ms .. 100 ms)", `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
	{"geqo_threshold", "9", "(2 .. 2147483647)", ""},
	{"lock_timeout", "42ms", "(0 ms .. 2147483647 ms)", `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
}

// Outcomes per target: "=V" shows V; "range:N" is the out-of-range display N;
// "invalid", "invalid:units" and "invalid:integer" are invalid-value errors
// without hint, with the units hint and with the integer-range hint.
var settingsFractionalRows = []struct {
	spelling, text string
	outcomes       [4]string
}{
	{"1.5", "1.5", [4]string{"=1.5", "=1500us", "=2", "=2ms"}},
	{"1.50", "1.50", [4]string{"=1.5", "=1500us", "=2", "=2ms"}},
	{"2.5", "2.5", [4]string{"=2.5", "=2500us", "=2", "=2ms"}},
	{"3.5", "3.5", [4]string{"=3.5", "=3500us", "=4", "=4ms"}},
	{".5", ".5", [4]string{"=0.5", "=500us", "range:0", "=0"}},
	{"5.", "5.", [4]string{"=5", "=5ms", "=5", "=5ms"}},
	{"1e2", "1e2", [4]string{"=100", "=100ms", "=100", "=100ms"}},
	{"1E2", "1E2", [4]string{"=100", "=100ms", "=100", "=100ms"}},
	{"1.5E-3", "1.5E-3", [4]string{"=0.0015", "=1.5us", "range:0", "=0"}},
	{"+2.5e+1", "2.5e+1", [4]string{"=25", "=25ms", "=25", "=25ms"}},
	{"6e4", "6e4", [4]string{"=60000", "range:60000 ms", "=60000", "=1min"}},
	{"+1.5", "1.5", [4]string{"=1.5", "=1500us", "=2", "=2ms"}},
	{"-1.5", "-1.5", [4]string{"range:-1.5", "range:-1.5 ms", "range:-2", "range:-2 ms"}},
	{"+.5", ".5", [4]string{"=0.5", "=500us", "range:0", "=0"}},
	{"-.5", "-.5", [4]string{"range:-0.5", "range:-0.5 ms", "invalid", "invalid"}},
	{"-0.0", "-0.0", [4]string{"=-0", "=-0", "range:0", "=0"}},
	{"1_000.5", "1_000.5", [4]string{"invalid", "invalid:units", "invalid", "invalid:units"}},
	{"2.1234567", "2.1234567", [4]string{"=2.12346", "=2123.46us", "=2", "=2ms"}},
	{"1e308", "1e308", [4]string{"=1e+308", "range:1e+308 ms", "invalid:integer", "invalid:integer"}},
	{"1e309", "1e309", [4]string{"invalid", "invalid", "invalid", "invalid"}},
	{"1e-320", "1e-320", [4]string{"invalid", "invalid", "invalid", "invalid"}},
	{"2147483648.5", "2147483648.5", [4]string{"=2.14748e+09", "range:2.14748e+09 ms", "invalid:integer", "invalid:integer"}},
}

var settingsFractionalForms = []string{"SET %s = %s", "SET %s TO %s", "SET SESSION %s = %s", "SET SESSION %s TO %s"}

func settingsFractionalShow(postgres bool, name, want string) []fixture.Step {
	return []fixture.Step{
		localReferenceSelect(postgres, "SHOW "+name, name, want),
		fixture.Select("SELECT current_setting('"+name+"')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{want}}),
	}
}

func settingsFractionalFailure(target settingsFractionalTarget, query, value, outcome string) fixture.Step {
	invalid := fmt.Sprintf(`invalid value for parameter "%s": "%s"`, target.name, value)
	switch outcome {
	case "invalid":
		return settingsReferenceError(query, "22023", invalid, "", "")
	case "invalid:units":
		return settingsReferenceError(query, "22023", invalid, target.unitsHint, "")
	case "invalid:integer":
		return settingsReferenceError(query, "22023", invalid, "Value exceeds integer range.", "")
	}
	display := strings.TrimPrefix(outcome, "range:")
	return settingsReferenceError(query, "22023", fmt.Sprintf(`%s is outside the valid range for parameter "%s" %s`, display, target.name, target.bounds), "", "")
}

func TestReferenceSettingsFractionalValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				a, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				run := func(s fixture.Step) {
					t.Helper()
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
				}
				for i, row := range settingsFractionalRows {
					for j, target := range settingsFractionalTargets {
						run(fixture.Exec("SET "+target.name+" TO '"+target.prior+"'", 0))
						query := fmt.Sprintf(settingsFractionalForms[(i+j)%len(settingsFractionalForms)], target.name, row.spelling)
						want := target.prior
						if outcome := row.outcomes[j]; strings.HasPrefix(outcome, "=") {
							run(fixture.Exec(query, 0))
							want = outcome[1:]
						} else {
							run(settingsFractionalFailure(target, query, row.text, outcome))
						}
						for _, s := range settingsFractionalShow(backend.PG, target.name, want) {
							run(s)
						}
					}
					// String and placeholder settings receive the flattened text.
					for j, name := range []string{"application_name", "fractional.rate"} {
						run(fixture.Exec("SET "+name+" TO 'prior'", 0))
						run(fixture.Exec(fmt.Sprintf(settingsFractionalForms[(i+j)%len(settingsFractionalForms)], name, row.spelling), 0))
						for _, s := range settingsFractionalShow(backend.PG, name, row.text) {
							run(s)
						}
					}
				}
				for _, tc := range []struct {
					name, value string
					setting     string
					unit        any
				}{{"random_page_cost", "1.25", "1.25", nil}, {"vacuum_cost_delay", "1.25", "1.25", "ms"}, {"geqo_threshold", "1500.5", "1500", nil}, {"lock_timeout", "1.25", "1", "ms"}} {
					run(fixture.Exec("SET "+tc.name+" TO "+tc.value, 0))
					run(fixture.Select("SELECT setting, unit FROM pg_settings WHERE name = '"+tc.name+"'", []string{"setting", "unit"}, []string{"TEXT", "TEXT"}, [][]any{{tc.setting, tc.unit}}))
				}
				run(fixture.Exec("RESET ALL", 0))
			}
		})
	}
}

func TestReferenceSettingsFractionalSyntaxAndTypes(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fractional_user"})
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				a, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				run := func(s fixture.Step) {
					t.Helper()
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
				}
				unchanged := func() {
					t.Helper()
					for _, s := range settingsFractionalShow(backend.PG, "random_page_cost", "2") {
						run(s)
					}
				}
				run(fixture.Exec("SET random_page_cost = 2", 0))
				// The whole statement is rejected before any assignment runs.
				for _, tc := range []struct{ tail, message, position string }{
					{"= 1.5abc", `trailing junk after numeric literal at or near "1.5abc"`, "24"},
					{"= 1e", `trailing junk after numeric literal at or near "1e"`, "24"},
					{"= 1e+", `trailing junk after numeric literal at or near "1e+"`, "24"},
					{"= .5e", `trailing junk after numeric literal at or near ".5e"`, "24"},
					{"= 1._5", `trailing junk after numeric literal at or near "1._5"`, "24"},
					{"= 1.5_", `trailing junk after numeric literal at or near "1.5_"`, "24"},
					{"= 1.5 2", `syntax error at or near "2"`, "28"},
					{"= 1.5 + 1", `syntax error at or near "+"`, "28"},
					{"= - -1.5", `syntax error at or near "-"`, "26"},
					{"= 1..5", `syntax error at or near ".."`, "25"},
					{"= 1.5.5", `syntax error at or near ".5"`, "27"},
					{"= 1.5::float8", `syntax error at or near "::"`, "27"},
					{"= (1.5)", `syntax error at or near "("`, "24"},
					{"= 1.5; x", `syntax error at or near "x"`, "29"},
					{"TO -", "syntax error at end of input", "26"},
					// SET NAMES accepts no NumericOnly value.
					{"NAMES 1.5", `syntax error at or near "1.5"`, "11"},
					{"SESSION NAMES -1.5", `syntax error at or near "-"`, "19"},
					{"LOCAL NAMES 2e3", `syntax error at or near "2e3"`, "17"},
				} {
					query := "SET random_page_cost " + tc.tail
					if strings.Contains(tc.tail, "NAMES") {
						query = "SET " + tc.tail
					}
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: tc.message, Position: tc.position})
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					unchanged()
				}
				// Valid numeric syntax still reaches setting-specific rules.
				for _, s := range []fixture.Step{
					settingsReferenceError("SET random_page_cost = 1.5, 2.5", "22023", "SET random_page_cost takes only one argument", "", ""),
					settingsReferenceError("SET enable_seqscan = 1.0", "22023", `parameter "enable_seqscan" requires a Boolean value`, "", ""),
					settingsReferenceError("SET bytea_output = 1.5", "22023", `invalid value for parameter "bytea_output": "1.5"`, "Available values: escape, hex.", ""),
					settingsReferenceError("SET work_mem = 1.5", "22023", `2 kB is outside the valid range for parameter "work_mem" (64 kB .. 2147483647 kB)`, "", ""),
					settingsReferenceError("SET log_statement_sample_rate = 1.5", "22023", `1.5 is outside the valid range for parameter "log_statement_sample_rate" (0 .. 1)`, "", ""),
					settingsReferenceError("SET block_size = 8192.5", "55P02", `parameter "block_size" cannot be changed`, "", ""),
				} {
					run(s)
					unchanged()
				}
				// A rejected placeholder list creates no placeholder.
				run(settingsReferenceError("SET fractional.rate = 1.5, -2.5e1", "22023", "SET fractional.rate takes only one argument", "", ""))
				run(fixture.Select("SELECT current_setting('fractional.rate', true)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}}))
				run(fixture.Exec("SET work_mem = 1024.5", 0))
				run(localReferenceSelect(backend.PG, "SHOW work_mem", "work_mem", "1MB"))
				run(fixture.Exec("SET search_path = 1.5, public", 0))
				run(localReferenceSelect(backend.PG, "SHOW search_path", "search_path", "1.5, public"))
				run(fixture.Exec("SET log_statement_sample_rate = 0.5", 0))
				run(localReferenceSelect(backend.PG, "SHOW log_statement_sample_rate", "log_statement_sample_rate", "0.5"))
				// Numeric spelling does not bypass SET privileges.
				run(fixture.Exec("CREATE ROLE fractional_user", 0))
				run(fixture.Exec("SET ROLE fractional_user", 0))
				run(settingsReferenceError("SET log_statement_sample_rate = 0.25", "42501", `permission denied to set parameter "log_statement_sample_rate"`, "", ""))
				run(fixture.Exec("SET random_page_cost = 0.25", 0))
				run(fixture.Exec("RESET ROLE", 0))
				run(localReferenceSelect(backend.PG, "SHOW log_statement_sample_rate", "log_statement_sample_rate", "0.5"))
				run(localReferenceSelect(backend.PG, "SHOW random_page_cost", "random_page_cost", "0.25"))
				run(fixture.Exec("RESET ALL", 0))
				run(fixture.Exec("DROP ROLE fractional_user", 0))
			}
		})
	}
}
