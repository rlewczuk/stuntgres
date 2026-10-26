package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLqueryBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, input, output string }{
		{"label", strings.Repeat("a", 1000), strings.Repeat("a", 1000)},
		{"label_modifiers", strings.Repeat("a", 1000)+"*@%", strings.Repeat("a", 1000)+"%@*"},
		{"items", strings.Repeat("a.", 65534)+"a", strings.Repeat("a.", 65534)+"a"},
		{"level", strings.Repeat("a|", 4093)+"a", strings.Repeat("a|", 4093)+"a"},
		// Discovery proposals for released atoi on the pinned linux/amd64 ABI;
		// these must be settled by the oracle before production is implemented.
		{"decimal_wrap_zero", "a{4294967296}", "a{0}"},
		{"decimal_wrap_one", "a{4294967297}", "a{1}"},
		{"decimal_high_wrap", "a{0,4294967297}", "a{,1}"},
		{"decimal_leading_zero_run", "a{"+strings.Repeat("0", 4096)+"1}", "a{1}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lqueryInstall(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE lq_boundary(v lquery)", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("INSERT INTO lq_boundary VALUES ($1)", 1, tc.input)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				s := lquerySelect(b, "SELECT v FROM lq_boundary", []string{"v"}, [][]any{{tc.output}, {tc.output}})
				s.Bag = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		})
	}
	for _, tc := range []struct{ name, input, code, message, detail string }{
		{"long_label", strings.Repeat("a", 1001), "42622", "label string is too long", "Label length is 1001, must be at most 1000, at character 1002."},
		{"items_overflow", strings.Repeat("a.", 65535)+"a", "54000", "number of lquery items (65536) exceeds the maximum allowed (65535)", ""},
		{"item_precedence", "@."+strings.Repeat("a.", 65534)+"a", "54000", "number of lquery items (65536) exceeds the maximum allowed (65535)", ""},
		{"level_overflow", strings.Repeat("a|", 4094)+"a", "54000", "lquery level is too large", "Total size of level exceeds the maximum allowed (65535 bytes)."},
		{"variant_overflow", strings.Repeat("a|", 65535)+"a", "54000", "lquery level has too many variants", "Number of variants exceeds the maximum allowed (65535)."},
		{"decimal_signed_overflow", "a{2147483648}", "54000", "lquery syntax error", "Low limit (-2147483648) exceeds the maximum allowed (65535), at character 3."},
		{"decimal_long_overflow", "a{"+strings.Repeat("9", 40)+"}", "54000", "lquery syntax error", "Low limit (-1) exceeds the maximum allowed (65535), at character 3."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lqueryInstall(t, p, b, c)
				for _, prepared := range []bool{false, true} {
					want := queryIntParameterDiagnostic(tc.code, tc.message, tc.input, 1)
					want.Detail = tc.detail
					lqueryError(t, p, b, c, "SELECT $1::lquery AS v", prepared, true, false, want, tc.input)
				}
			})
		})
	}
}
