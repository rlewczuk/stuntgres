package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineProceduresOptionSyntax(t *testing.T) {
	const prefix = "CREATE PROCEDURE pc_bad() LANGUAGE SQL AS 'SELECT 1'"
	// These expectations describe released option syntax, independently of the
	// native parser. A complete prohibited option is a DDL error; an incomplete
	// option or unknown token must fail the actual protocol Prepare itself.
	for _, tc := range []struct {
		name, query, at string
	}{
		{"unknown_suffix", prefix + " nonsense", "nonsense"},
		{"stray_number", prefix + " 123", "123"},
		{"bare_cost", prefix + " COST", ""},
		{"bare_rows", prefix + " ROWS", ""},
		{"bare_support", prefix + " SUPPORT", ""},
		{"bare_parallel", prefix + " PARALLEL", ""},
		{"incomplete_called_null", prefix + " CALLED ON NULL", ""},
		{"incomplete_returns_null", prefix + " RETURNS NULL ON NULL", ""},
		{"nonnumeric_cost", prefix + " COST nope", "nope"},
		{"nonnumeric_rows", prefix + " ROWS nope", "nope"},
		{"called_missing_on", prefix + " CALLED NULL INPUT", "NULL"},
		{"junk_after_cost", prefix + " COST 1 nonsense", "nonsense"},
		{"junk_after_strict", prefix + " STRICT nonsense", "nonsense"},
		{"as_before_language", "CREATE PROCEDURE pc_bad() AS 'SELECT 1' LANGUAGE SQL nonsense", "nonsense"},
		{"as_before_language_incomplete", "CREATE PROCEDURE pc_bad() AS 'SELECT 1' LANGUAGE SQL COST", ""},
		{"pre_as_unknown", "CREATE PROCEDURE pc_bad() LANGUAGE SQL nonsense AS 'SELECT 1'", "nonsense"},
		{"pre_as_incomplete_cost", "CREATE PROCEDURE pc_bad() LANGUAGE SQL COST AS 'SELECT 1'", "AS"},
		{"pre_as_incomplete_null", "CREATE PROCEDURE pc_bad() LANGUAGE SQL CALLED ON NULL AS 'SELECT 1'", "AS"},
		{"pre_as_junk_after_strict", "CREATE PROCEDURE pc_bad() LANGUAGE SQL STRICT nonsense AS 'SELECT 1'", "nonsense"},
		{"standard_pre_body_junk", "CREATE PROCEDURE pc_bad() LANGUAGE SQL COST 1 nonsense BEGIN ATOMIC SELECT 1; END", "nonsense"},
		{"standard_post_body_junk", "CREATE PROCEDURE pc_bad() LANGUAGE SQL BEGIN ATOMIC SELECT 1; END nonsense", "nonsense"},
		{"multibyte_token_position", "/* café 雪 */ " + prefix + " nonsense", "nonsense"},
		{"multibyte_end_position", "/* café 雪 */ " + prefix + " COST", ""},
		{"body_effect_not_executed", "CREATE PROCEDURE pc_bad() LANGUAGE SQL AS 'INSERT INTO pc_option_effect VALUES(99); SELECT 1' nonsense", "nonsense"},
		{"valid_prefix_invalid_suffix", "CREATE PROCEDURE pc_option_prefix() LANGUAGE SQL AS 'INSERT INTO pc_option_effect VALUES(88)'; INSERT INTO pc_option_effect VALUES(77); " + prefix + " nonsense", "nonsense"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE pc_option_effect(v integer)")
				message := "syntax error at end of input"
				position := utf8.RuneCountInString(tc.query) + 1
				if tc.at != "" {
					message = `syntax error at or near "` + tc.at + `"`
					position = proceduresOptionPosition(t, tc.query, tc.at)
				}
				// resultsError checks direct Exec and real PrepareContext, including
				// the stage and absence of a returned Result/Stmt, and all fields of
				// the diagnostic (including the empty optional fields).
				resultsError(t, p, c, tc.query, false, "prepare", proceduresDiagnostic("42601", message, position))
				proceduresOptionRecovery(t, p, b, c)
			})
		})
	}

	for _, tc := range []struct {
		name, query, at string
	}{
		{"complete_cost", prefix + " COST 1", "COST"},
		{"complete_rows", prefix + " ROWS 1", "ROWS"},
		{"complete_support", prefix + " SUPPORT pc_support", "SUPPORT"},
		{"complete_parallel", prefix + " PARALLEL SAFE", "PARALLEL"},
		{"complete_strict", prefix + " STRICT", "STRICT"},
		{"complete_called_null", prefix + " CALLED ON NULL INPUT", "CALLED"},
		{"complete_returns_null", prefix + " RETURNS NULL ON NULL INPUT", "RETURNS"},
		{"complete_signed_cost", prefix + " COST -1.5", "COST"},
		{"complete_signed_rows", prefix + " ROWS +2", "ROWS"},
		{"complete_qualified_support", prefix + " SUPPORT public.pc_support", "SUPPORT"},
		{"complete_parallel_identifier", prefix + " PARALLEL arbitrary", "PARALLEL"},
		{"complete_not_leakproof", prefix + " NOT LEAKPROOF", "NOT"},
		{"complete_as_before_language", "CREATE PROCEDURE pc_bad() AS 'SELECT 1' LANGUAGE SQL COST 1", "COST"},
		{"complete_pre_as", "CREATE PROCEDURE pc_bad() LANGUAGE SQL STRICT AS 'INSERT INTO pc_option_effect VALUES(99); SELECT 1'", "STRICT"},
		{"complete_standard", "CREATE PROCEDURE pc_bad() LANGUAGE SQL COST 1 BEGIN ATOMIC INSERT INTO pc_option_effect VALUES(99); SELECT 1; END", "COST"},
		{"complete_multibyte_position", "/* café 雪 */ " + prefix + " COST 1", "COST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE pc_option_effect(v integer)")
				// Hold an actual prepared DDL statement without executing it, then
				// inspect catalog and data to prove Prepare has no publication or
				// body effects. Close it before the shared direct/prepared checks.
				stmt, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_bad'", 0)
				proceduresCount(t, p, b, c, "pc_option_effect", 0)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				want := proceduresDiagnostic("42P13", "invalid attribute in procedure definition", proceduresOptionPosition(t, tc.query, tc.at))
				resultsError(t, p, c, tc.query, false, "execute", want)
				proceduresOptionRecovery(t, p, b, c)
			})
		})
	}
}

// PostgreSQL diagnostic positions count source characters, not UTF-8 bytes.
func proceduresOptionPosition(t *testing.T, query, token string) int {
	t.Helper()
	at := strings.Index(query, token)
	if at < 0 {
		t.Fatalf("missing diagnostic token %q in %q", token, query)
	}
	return utf8.RuneCountInString(query[:at]) + 1
}

func proceduresOptionRecovery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	proceduresCount(t, p, b, c, "pg_proc WHERE proname IN ('pc_bad','pc_option_prefix')", 0)
	proceduresCount(t, p, b, c, "pc_option_effect", 0)
	// The corrected same-name definition must be usable on the same connection,
	// including a real body effect and declared output metadata on both routes.
	resultsExec(t, p, b, c, "CREATE PROCEDURE pc_bad(OUT answer integer) LANGUAGE SQL AS 'INSERT INTO pc_option_effect VALUES(7); SELECT 7'")
	proceduresBoth(t, p, b, c, "CALL pc_bad(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
	resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_option_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
}
