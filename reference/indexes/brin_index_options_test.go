package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBrinIndexOptions(t *testing.T) {
	for _, tc := range []struct{ options, message, detail string }{
		{"pages_per_range=0", `value 0 out of bounds for option "pages_per_range"`, `Valid values are between "1" and "131072".`},
		{"pages_per_range=131073", `value 131073 out of bounds for option "pages_per_range"`, `Valid values are between "1" and "131072".`},
		{"pages_per_range='bad'", `invalid value for integer option "pages_per_range": bad`, ""},
		{"pages_per_range", `invalid value for integer option "pages_per_range": true`, ""},
		{"autosummarize='bad'", `invalid value for boolean option "autosummarize": bad`, ""},
		{"pages_per_range=2,pages_per_range=3", `parameter "pages_per_range" specified more than once`, ""},
		{"pages_per_range='bad',pages_per_range=3", `invalid value for integer option "pages_per_range": bad`, ""},
		{"fillfactor=80", `unrecognized parameter "fillfactor"`, ""},
		{"unknown=1", `unrecognized parameter "unknown"`, ""},
		{"toast.pages_per_range=2", `unrecognized parameter namespace "toast"`, ""},
		{`"a=b"=1`, `invalid option name "a=b": must not contain "="`, ""},
	} {
		t.Run(tc.options, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				brinIndexSeed(run)
				want := fixture.Diagnostic{Code: "22023", Message: tc.message, Detail: tc.detail}
				hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX retry ON t USING brin(n) WITH("+tc.options+")", want)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE INDEX retry ON t USING brin(n) WITH(pages_per_range=2,autosummarize=on)", 0))
				if tc.options != "toast.pages_per_range=2" {
					hashIndexSemanticFailure(t, p, b, run, "ALTER INDEX retry SET ("+tc.options+")", want)
				}
				run(fixture.Select("SELECT reloptions::text FROM pg_class WHERE relname='retry'", []string{"reloptions"}, []string{"TEXT"}, [][]any{{"{pages_per_range=2,autosummarize=on}"}}))
				run(brinIndexIDs("n=4", 3, 6))
			})
		})
	}
	for _, options := range []string{"pages_per_range=1", "pages_per_range=131072", "pages_per_range=2.0", "autosummarize=on", "autosummarize=off", "autosummarize=true", "autosummarize=false", "autosummarize=1", "autosummarize=0", "autosummarize='y'", "autosummarize='n'"} {
		t.Run("valid-"+options, func(t *testing.T) {
			brinIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				brinIndexSeed(run)
				run(fixture.Exec("CREATE INDEX bn ON t USING brin(n) WITH("+options+")", 0))
				run(brinIndexIDs("n>=4", 2, 3, 5, 6))
				run(fixture.Exec("ALTER INDEX bn SET(pages_per_range=1,autosummarize=on)", 0))
				run(fixture.Exec("REINDEX INDEX bn", 0))
				run(fixture.Exec("ALTER INDEX bn RESET(pages_per_range,autosummarize)", 0))
				run(fixture.Exec("REINDEX INDEX bn", 0))
				run(brinIndexIDs("n>=4", 2, 3, 5, 6))
			})
		})
	}
}
