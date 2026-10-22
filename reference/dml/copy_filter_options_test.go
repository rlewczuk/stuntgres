package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterOptions(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_options(id int)")
		for _, tc := range []struct{ name, options, code, message, token string }{
			{"action_missing", "ON_ERROR", "42601", "on_error requires a parameter", ""},
			{"verbosity_missing", "LOG_VERBOSITY", "42601", "log_verbosity requires a parameter", ""},
			{"limit_missing", "ON_ERROR ignore,REJECT_LIMIT", "42601", "reject_limit requires a numeric value", ""},
			{"action_unknown", "ON_ERROR bogus", "22023", "COPY ON_ERROR \"bogus\" not recognized", "ON_ERROR"},
			{"action_bool", "ON_ERROR true", "22023", "COPY ON_ERROR \"true\" not recognized", "ON_ERROR"},
			{"action_numeric", "ON_ERROR 1", "22023", "COPY ON_ERROR \"1\" not recognized", "ON_ERROR"},
			{"action_star", "ON_ERROR *", "22023", "COPY ON_ERROR \"*\" not recognized", "ON_ERROR"},
			{"action_list", "ON_ERROR (stop,ignore)", "22023", "COPY ON_ERROR \"stop.ignore\" not recognized", "ON_ERROR"},
			{"verbosity_unknown", "LOG_VERBOSITY bogus", "22023", "COPY LOG_VERBOSITY \"bogus\" not recognized", "LOG_VERBOSITY"},
			{"verbosity_bool", "LOG_VERBOSITY false", "22023", "COPY LOG_VERBOSITY \"false\" not recognized", "LOG_VERBOSITY"},
			{"verbosity_numeric", "LOG_VERBOSITY 2", "22023", "COPY LOG_VERBOSITY \"2\" not recognized", "LOG_VERBOSITY"},
			{"verbosity_star", "LOG_VERBOSITY *", "22023", "COPY LOG_VERBOSITY \"*\" not recognized", "LOG_VERBOSITY"},
			{"verbosity_list", "LOG_VERBOSITY (silent,verbose)", "22023", "COPY LOG_VERBOSITY \"silent.verbose\" not recognized", "LOG_VERBOSITY"},
			{"duplicate_action", "ON_ERROR stop,ON_ERROR ignore", "42601", "conflicting or redundant options", "ON_ERROR ignore"},
			{"duplicate_verbosity", "LOG_VERBOSITY default,LOG_VERBOSITY silent", "42601", "conflicting or redundant options", "LOG_VERBOSITY silent"},
			{"duplicate_limit", "ON_ERROR ignore,REJECT_LIMIT 1,REJECT_LIMIT 2", "42601", "conflicting or redundant options", "REJECT_LIMIT 2"},
			{"limit_requires_ignore", "REJECT_LIMIT 2", "22023", "COPY REJECT_LIMIT requires ON_ERROR to be set to IGNORE", ""},
			{"limit_with_stop", "ON_ERROR stop,REJECT_LIMIT 2", "22023", "COPY REJECT_LIMIT requires ON_ERROR to be set to IGNORE", ""},
			{"limit_zero", "ON_ERROR ignore,REJECT_LIMIT 0", "22023", "REJECT_LIMIT (0) must be greater than zero", ""},
			{"limit_negative", "ON_ERROR ignore,REJECT_LIMIT -1", "22023", "REJECT_LIMIT (-1) must be greater than zero", ""},
			{"limit_quoted_negative", "ON_ERROR ignore,REJECT_LIMIT '-1'", "22023", "REJECT_LIMIT (-1) must be greater than zero", ""},
			{"limit_overflow", "ON_ERROR ignore,REJECT_LIMIT 9223372036854775808", "22003", "value \"9223372036854775808\" is out of range for type bigint", ""},
			{"limit_fraction", "ON_ERROR ignore,REJECT_LIMIT 1.5", "22P02", "invalid input syntax for type bigint: \"1.5\"", ""},
			{"limit_exponent", "ON_ERROR ignore,REJECT_LIMIT 1e2", "22P02", "invalid input syntax for type bigint: \"1e2\"", ""},
			{"limit_malformed", "ON_ERROR ignore,REJECT_LIMIT 'bad'", "22P02", "invalid input syntax for type bigint: \"bad\"", ""},
			{"limit_bool", "ON_ERROR ignore,REJECT_LIMIT true", "22P02", "invalid input syntax for type bigint: \"true\"", ""},
			{"limit_star", "ON_ERROR ignore,REJECT_LIMIT *", "42601", "reject_limit requires a numeric value", ""},
			{"limit_list", "ON_ERROR ignore,REJECT_LIMIT (a,b)", "42601", "reject_limit requires a numeric value", ""},
			{"first_invalid_before_duplicate", "ON_ERROR bogus,ON_ERROR stop", "22023", "COPY ON_ERROR \"bogus\" not recognized", "ON_ERROR bogus"},
			{"binary_ignore", "FORMAT binary,ON_ERROR ignore", "42601", "only ON_ERROR STOP is allowed in BINARY mode", ""},
			{"binary_ignore_before_format", "ON_ERROR ignore,FORMAT binary", "42601", "only ON_ERROR STOP is allowed in BINARY mode", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "COPY copy_options FROM STDIN WITH (" + tc.options + ")"
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
				if tc.token != "" {
					want.Position = copyFilterPosition(q, tc.token)
				}
				copyFilterFailure(t, c, q, "", want)
				copyFilterIDs(t, c, "copy_options")
				copyFilterNotices(t, *notices)
			})
		}
		for _, options := range []string{"ON_ERROR 'IgNoRe',REJECT_LIMIT '2',LOG_VERBOSITY 'SiLeNt'", "REJECT_LIMIT 9223372036854775807,ON_ERROR ignore,LOG_VERBOSITY silent", "ON_ERROR stop,LOG_VERBOSITY verbose", "LOG_VERBOSITY silent"} {
			t.Run("accepted/"+options, func(t *testing.T) {
				copyFilterImport(t, c, "COPY copy_options FROM STDIN WITH ("+options+")", "", 0)
				copyFilterNotices(t, *notices)
			})
		}
		for _, options := range []string{"ON_ERROR stop", "ON_ERROR ignore", "ON_ERROR bogus", "REJECT_LIMIT 1,ON_ERROR ignore", "ON_ERROR stop,LOG_VERBOSITY verbose"} {
			t.Run("to/"+options, func(t *testing.T) {
				q := "COPY copy_options TO STDOUT WITH (" + options + ")"
				var output bytes.Buffer
				r, err := c.to(q, &output)
				copyTextDiagnostic(t, r, err, fixture.Diagnostic{Code: "22023", Message: "COPY ON_ERROR cannot be used with COPY TO", Position: copyFilterPosition(q, "ON_ERROR")})
				copyFilterEqual(t, output.String(), "")
			})
		}
		for _, mode := range []string{"default", "silent", "verbose"} {
			c.output(t, "COPY copy_options TO STDOUT WITH (LOG_VERBOSITY "+mode+")", "", 0)
		}
		q := "COPY copy_options TO STDOUT WHERE TRUE"
		var output bytes.Buffer
		r, err := c.to(q, &output)
		copyTextDiagnostic(t, r, err, fixture.Diagnostic{Code: "42601", Message: "WHERE clause not allowed with COPY TO", Position: copyFilterPosition(q, "WHERE")})
		copyFilterEqual(t, output.String(), "")
	})
}

func TestReferenceCopyFilterBinding(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_bind(id int,keep bool,g int GENERATED ALWAYS AS (id+1) STORED)")
		for _, tc := range []struct{ name, predicate, code, message, detail, token string }{
			{"generated", "g>0", "42P10", "generated columns are not supported in COPY FROM WHERE conditions", "Column \"g\" is a generated column.", ""},
			{"generated_dead", "FALSE AND g>0", "42P10", "generated columns are not supported in COPY FROM WHERE conditions", "Column \"g\" is a generated column.", ""},
			{"generated_whole_row", "copy_bind IS NOT NULL", "42P10", "generated columns are not supported in COPY FROM WHERE conditions", "Column \"g\" is a generated column.", ""},
			{"unknown", "absent>0", "42703", "column \"absent\" does not exist", "", "absent"},
			{"boolean", "id", "42804", "argument of WHERE must be type boolean, not type integer", "", "id"},
			{"boolean_input", "'bad'", "22P02", "invalid input syntax for type boolean: \"bad\"", "", "'bad'"},
			{"subquery", "id IN (SELECT 1)", "0A000", "cannot use subquery in COPY FROM WHERE condition", "", "IN"},
			{"aggregate", "sum(id)>0", "42803", "aggregate functions are not allowed in COPY FROM WHERE conditions", "", "sum"},
			{"window", "row_number() OVER ()>0", "42P20", "window functions are not allowed in COPY FROM WHERE conditions", "", "row_number"},
			{"srf", "generate_series(1,2)>0", "0A000", "set-returning functions are not allowed in COPY FROM WHERE conditions", "", "generate_series"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "COPY copy_bind(id,keep) FROM STDIN WHERE " + tc.predicate
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail}
				if tc.token != "" {
					want.Position = copyFilterPosition(q, tc.token)
				}
				copyFilterFailure(t, c, q, "1\ttrue\n", want)
				copyFilterIDs(t, c, "copy_bind")
				copyFilterNotices(t, *notices)
			})
		}
		t.Run("relation_before_options", func(t *testing.T) {
			q := "COPY copy_absent FROM STDIN WITH (ON_ERROR bogus) WHERE TRUE"
			copyFilterFailure(t, c, q, "", fixture.Diagnostic{Code: "42P01", Message: "relation \"copy_absent\" does not exist"})
		})
		t.Run("filter_binding_before_options", func(t *testing.T) {
			q := "COPY copy_bind(id,keep) FROM STDIN WITH (ON_ERROR bogus) WHERE absent>0"
			copyFilterFailure(t, c, q, "", fixture.Diagnostic{Code: "42703", Message: "column \"absent\" does not exist", Position: copyFilterPosition(q, "absent")})
		})
		t.Run("generated_before_copy_targets", func(t *testing.T) {
			copyFilterFailure(t, c, "COPY copy_bind(absent) FROM STDIN WHERE FALSE AND g>0", "", fixture.Diagnostic{Code: "42P10", Message: "generated columns are not supported in COPY FROM WHERE conditions", Detail: "Column \"g\" is a generated column."})
		})
		copyFilterImport(t, c, "COPY copy_bind(id,keep) FROM STDIN WHERE 'true'", "1\ttrue\n", 1)
		copyFilterIDs(t, c, "copy_bind", 1)
	})
}
