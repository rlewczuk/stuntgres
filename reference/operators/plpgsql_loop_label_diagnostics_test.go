package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLLoopQuotedLabelDiagnostics(t *testing.T) {
	// PostgreSQL's stmt_exit/check_labels messages put literal double quotes
	// around decoded identifier contents; they do not apply string escaping.
	for _, label := range []struct{ name, token, decoded, otherToken, otherDecoded string }{
		{"backslash", `"é\b"`, `é\b`, `"a""b"`, `a"b`},
		{"embedded_quote", `"a""b"`, `a"b`, `"é\b"`, `é\b`},
	} {
		for _, tc := range []struct{ name, body, message, positionToken string }{
			{"unknown_exit", "BEGIN LOOP EXIT " + label.token + "; END LOOP; END", `there is no label "` + label.decoded + `" attached to any block or loop enclosing this statement`, label.token},
			{"unknown_continue", "BEGIN LOOP CONTINUE " + label.token + "; END LOOP; END", `there is no label "` + label.decoded + `" attached to any block or loop enclosing this statement`, label.token},
			{"block_continue", "BEGIN <<" + label.token + ">> BEGIN LOOP CONTINUE " + label.token + "; END LOOP; END; END", `block label "` + label.decoded + `" cannot be used in CONTINUE`, label.token},
			{"end_unlabeled", "BEGIN LOOP EXIT; END LOOP " + label.token + "; END", `end label "` + label.decoded + `" specified for unlabeled block`, label.token},
			{"end_mismatch", "BEGIN <<" + label.otherToken + ">> LOOP EXIT; END LOOP " + label.token + "; END", `end label "` + label.decoded + `" differs from block's label "` + label.otherDecoded + `"`, label.token},
		} {
			for _, entry := range []string{"create", "do"} {
				t.Run(label.name+"/"+tc.name+"/"+entry, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							q := "DO $$" + tc.body + "$$"
							if entry == "create" {
								q = "CREATE FUNCTION loop_syntax() RETURNS void LANGUAGE plpgsql AS $$" + tc.body + "$$"
							}
							// The final occurrence is the referenced/closing label, not
							// an opening declaration. Count characters, not UTF8 bytes.
							offset := strings.LastIndex(q, tc.positionToken)
							if offset < 0 {
								t.Fatal("diagnostic position witness missing")
							}
							want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
							want.Position = fixture.Position(utf8.RuneCountInString(q[:offset]) + 1)
							plpgsqlError(t, p, c, q, false, want)
							if entry == "create" {
								plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='loop_syntax'", "INT8", int64(0))
								userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_syntax() RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP EXIT; END LOOP; RETURN 7; END$$")
								plpgsqlValue(t, p, b, c, "SELECT loop_syntax() AS v", "INT4", int64(7))
							}
						})
					}
				})
			}
		}
	}
}
