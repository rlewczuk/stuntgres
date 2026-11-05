package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFileFDWPlacement(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		for _, option := range []string{"filename", "program", "format", "header", "delimiter", "quote", "escape", "null", "encoding", "force_not_null", "force_null"} {
			for _, context := range []int{3118, 1249, 2328, 1417, 1418} {
				column := option == "force_null" || option == "force_not_null"
				if (column && context == 1249) || (!column && context == 3118) {
					continue
				}
				t.Run(fmt.Sprintf("%s_%d", option, context), func(t *testing.T) {
					d := fdwDiagnostic("HV00D", `invalid option "`+option+`"`)
					if context != 3118 && context != 1249 {
						d.Hint = "There are no valid options in this context."
					}
					s := fixture.ErrorDiagnostic(fileFDWCall([]string{option + "=true"}, context), d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
		for _, tc := range []struct{ option, hint string }{
			{"filenam", `Perhaps you meant the option "filename".`},
			{"reject_limi", `Perhaps you meant the option "reject_limit".`},
			{"unrelated_option_zzzzzz", ""},
		} {
			d := fdwDiagnostic("HV00D", `invalid option "`+tc.option+`"`)
			d.Hint = tc.hint
			run(fixture.ErrorDiagnostic(fileFDWCall([]string{tc.option + "=x"}, 3118), d))
		}
		for _, q := range []string{
			"CREATE SERVER ff_rejected FOREIGN DATA WRAPPER file_fdw OPTIONS(filename '/x')",
			"CREATE USER MAPPING FOR CURRENT_USER SERVER ff_server OPTIONS(filename '/x')",
			"ALTER FOREIGN DATA WRAPPER file_fdw OPTIONS(ADD filename '/x')",
		} {
			d := fdwDiagnostic("HV00D", `invalid option "filename"`)
			d.Hint = "There are no valid options in this context."
			run(fixture.ErrorDiagnostic(q, d))
		}
		// This is the explicitly bounded native surface, not a claim that
		// released PostgreSQL rejects these otherwise valid stock options.
		if !b.PG {
			for _, option := range []string{"default", "on_error", "log_verbosity", "reject_limit"} {
				d := fdwDiagnostic("HV00D", `invalid option "`+option+`"`)
				d.Hint = `Perhaps you meant the option "` + option + `".`
				run(fixture.ErrorDiagnostic(fileFDWCall([]string{"filename=/x", option + "=x"}, 3118), d))
			}
		}
	})
}

func TestReferenceFileFDWDiagnostics(t *testing.T) {
	cases := []struct{ name, options, code, primary string }{
		{"missing", "", "HV002", "either filename or program is required for file_fdw foreign tables"},
		{"both", "filename '/x',program 'not-a-real-filefdw-program'", "42601", "conflicting or redundant options"},
		{"format_before_missing", "format 'CSV'", "22023", `COPY format "CSV" not recognized`},
		{"format", "filename '/x',format 'invalid'", "22023", `COPY format "invalid" not recognized`},
		{"header_one", "filename '/x',header '1'", "42601", `header requires a Boolean value or "match"`},
		{"header_zero", "filename '/x',header '0'", "42601", `header requires a Boolean value or "match"`},
		{"header_invalid", "filename '/x',header 'sometimes'", "42601", `header requires a Boolean value or "match"`},
		{"encoding", "filename '/x',encoding 'not-an-encoding'", "22023", `argument to option "encoding" must be a valid encoding name`},
		{"delimiter_empty", "filename '/x',delimiter ''", "0A000", "COPY delimiter must be a single one-byte character"},
		{"delimiter_multibyte", "filename '/x',delimiter 'é'", "0A000", "COPY delimiter must be a single one-byte character"},
		{"quote_empty", "filename '/x',format 'csv',quote ''", "0A000", "COPY quote must be a single one-byte character"},
		{"quote_multibyte", "filename '/x',format 'csv',quote 'é'", "0A000", "COPY quote must be a single one-byte character"},
		{"escape_empty", "filename '/x',format 'csv',escape ''", "0A000", "COPY escape must be a single one-byte character"},
		{"escape_multibyte", "filename '/x',format 'csv',escape 'é'", "0A000", "COPY escape must be a single one-byte character"},
		{"text_quote", "filename '/x',quote '!'", "0A000", "COPY QUOTE requires CSV mode"},
		{"text_escape", "filename '/x',escape '!'", "0A000", "COPY ESCAPE requires CSV mode"},
		{"mode_before_quote_length", "filename '/x',quote 'é'", "0A000", "COPY QUOTE requires CSV mode"},
		{"mode_before_escape_length", "filename '/x',escape 'é'", "0A000", "COPY ESCAPE requires CSV mode"},
		{"binary_header", "filename '/x',format 'binary',header 'true'", "0A000", "cannot specify HEADER in BINARY mode"},
		{"binary_header_match", "filename '/x',format 'binary',header 'match'", "0A000", "cannot specify HEADER in BINARY mode"},
		{"binary_delimiter", "filename '/x',format 'binary',delimiter ';'", "42601", "cannot specify DELIMITER in BINARY mode"},
		{"binary_null", "filename '/x',format 'binary',null 'NIL'", "42601", "cannot specify NULL in BINARY mode"},
		{"binary_competing", "filename '/x',format 'binary',header 'true',delimiter ';'", "42601", "cannot specify DELIMITER in BINARY mode"},
		{"newline_delimiter", "filename '/x',delimiter E'\\n'", "22023", "COPY delimiter cannot be newline or carriage return"},
		{"newline_null", "filename '/x',null E'\\r'", "22023", "COPY null representation cannot use newline or carriage return"},
		{"text_backslash", "filename '/x',delimiter E'\\\\'", "22023", `COPY delimiter cannot be "\"`},
		{"text_dot", "filename '/x',delimiter '.'", "22023", `COPY delimiter cannot be "."`},
		{"text_lowercase", "filename '/x',delimiter 'a'", "22023", `COPY delimiter cannot be "a"`},
		{"text_digit", "filename '/x',delimiter '1'", "22023", `COPY delimiter cannot be "1"`},
		{"same_quote", "filename '/x',format 'csv',delimiter ';',quote ';'", "22023", "COPY delimiter and quote must be different"},
		{"null_delimiter", "filename '/x',format 'csv',delimiter ';',null 'a;b'", "22023", "COPY delimiter character must not appear in the NULL specification"},
		{"null_quote", `filename '/x',format 'csv',null 'a"b'`, "22023", "CSV quote character must not appear in the NULL specification"},
		{"ddl_duplicate", "filename '/x',format 'csv',format 'text'", "42710", `option "format" provided more than once`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				fileFDWInstall(run)
				options := ""
				if tc.options != "" {
					options = " OPTIONS(" + tc.options + ")"
				}
				run(fileFDWError("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server"+options, tc.code, tc.primary))
				fileFDWAbsent(run)
			})
		})
	}
}

func TestReferenceFileFDWValidatorCalls(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		for _, entries := range [][]string{{"filename=/x"}, {"filename=a=b"}, {"filename="}, {"program="}, {"filename=/x", "format=csv", "header=MaTcH"}, {"filename=/x", "header=ON"}, {"filename=/x", "header=off"}, {"filename=/x", "format=binary", "header=false"}} {
			run(fileFDWVoid(fileFDWCall(entries, 3118)))
		}
		for _, context := range []int{1249, 2328, 1417, 1418, 0} {
			run(fileFDWVoid(fileFDWCall(nil, context)))
		}
		for _, entries := range [][]string{{"force_null"}, {"force_not_null"}, {"force_null=true", "force_not_null=on"}, {"force_null=OFF", "force_not_null=False"}} {
			run(fileFDWVoid(fileFDWCall(entries, 1249)))
		}
		for _, q := range []string{"SELECT file_fdw_validator(NULL::text[],3118::oid) AS validated", "SELECT file_fdw_validator(ARRAY['filename=/x'],NULL::oid) AS validated", "SELECT file_fdw_validator(NULL::text[],NULL::oid) AS validated"} {
			run(fixture.Select(q, []string{"validated"}, []string{"VOID"}, [][]any{{nil}}))
		}
		// Parameterized SQL is exercised through a genuinely prepared statement
		// in the prepared route, with the same independent VOID/NULL contracts.
		run(fileFDWVoid("SELECT file_fdw_validator(ARRAY[$1::text],$2::oid) AS validated", "filename=a=b", int64(3118)))
		run(fixture.Select("SELECT file_fdw_validator($1::text[],$2::oid) AS validated", []string{"validated"}, []string{"VOID"}, [][]any{{nil}}, nil, int64(3118)))
		for _, tc := range []struct{ query, code, primary string }{
			{fileFDWCall(nil, 3118), "HV002", "either filename or program is required for file_fdw foreign tables"},
			{"SELECT file_fdw_validator(ARRAY[NULL]::text[],3118::oid)", "22004", "null array element not allowed in this context"},
			{fileFDWCall([]string{"filename"}, 3118), "42601", "filename requires a parameter"},
			{fileFDWCall([]string{"filename=/x", "filename=/y"}, 3118), "42601", "conflicting or redundant options"},
			{fileFDWCall([]string{"filename=/x", "format=csv", "format=text"}, 3118), "42601", "conflicting or redundant options"},
			{fileFDWCall([]string{"force_null=1"}, 1249), "42601", "force_null requires a Boolean value"},
			{fileFDWCall([]string{"force_not_null=0"}, 1249), "42601", "force_not_null requires a Boolean value"},
			{"SELECT file_fdw_handler()", "0A000", "cannot display a value of type fdw_handler"},
		} {
			s := fileFDWError(tc.query, tc.code, tc.primary)
			run(s)
		}
		duplicateForce := fdwDiagnostic("42601", "conflicting or redundant options")
		duplicateForce.Hint = `Option "force_null" supplied more than once for a column.`
		run(fixture.ErrorDiagnostic(fileFDWCall([]string{"force_null=true", "force_null=false"}, 1249), duplicateForce))
	})
}
