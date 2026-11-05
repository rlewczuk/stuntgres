package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableDiagnostics(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		for _, nullIndex := range []int{-1, 0, 1, 2, 3, 4} {
			args := []string{"'id'", "'doc'", "'xt_src'", "'/doc/a'", "'id=1'"}
			if nullIndex >= 0 {
				args[nullIndex] = "NULL::text"
			}
			call := "xpath_table(" + strings.Join(args, ",") + ")"
			name := fmt.Sprintf("context_null_%d", nullIndex+1)
			recordDefinition := xml2Error("42601", `a column definition list is required for functions returning "record"`)
			recordDefinition.Position = "15"
			xtFailure(t, p, b, c, name+"_missing_record_definition", "SELECT * FROM "+call, recordDefinition, true)
			if nullIndex < 0 {
				xtFailure(t, p, b, c, name+"_target_list", "SELECT "+call,
					xml2Error("0A000", "materialize mode required, but it is not allowed in this context"), false)
			} else {
				xtBoth(t, p, b, c, name+"_target_list", fixture.Select("SELECT "+call, []string{"xpath_table"}, []string{"RECORD"}, nil))
			}
		}
		for _, tc := range []struct {
			name, relation, key, doc, path, criteria, definitions string
			want                                                  fixture.Diagnostic
		}{
			{"compile_valid_document", "xt_src", "id", "doc", "[", "id=3", "id integer,a text", xml2Compile("Invalid expression")},
			{"compile_consumed_tail", "xt_src", "id", "doc", "/doc/a|[", "id=1", "id integer,a text,b text", xml2Compile("Invalid expression")},
			{"literal_pipe_in_string", "xt_src", "id", "doc", "concat('a|b','c')", "id=3", "id integer,a text,b text", xml2Compile("Unfinished literal")},
			{"absent_relation", "xt_absent", "id", "doc", "/doc/a", "true", "id integer,a text", xtInternal(xml2Error("42P01", `relation "xt_absent" does not exist`), "SELECT id, doc FROM xt_absent WHERE true", "21")},
			{"utf8_absent_relation", "xt_absent", "'é前'", "doc", "/doc/a", "true", "id text,a text", xtInternal(xml2Error("42P01", `relation "xt_absent" does not exist`), "SELECT 'é前', doc FROM xt_absent WHERE true", "23")},
			{"absent_key", "xt_src", "missing_xt_key", "doc", "/doc/a", "true", "id integer,a text", xtInternal(xml2Error("42703", `column "missing_xt_key" does not exist`), "SELECT missing_xt_key, doc FROM xt_src WHERE true", "8")},
			{"criteria_eof", "xt_src", "id", "doc", "/doc/a", "id =", "id integer,a text", xtInternal(xml2Error("42601", "syntax error at end of input"), "SELECT id, doc FROM xt_src WHERE id =", "38")},
			{"criteria_token", "xt_src", "id", "doc", "/doc/a", "id = )", "id integer,a text", xtInternal(xml2Error("42601", `syntax error at or near ")"`), "SELECT id, doc FROM xt_src WHERE id = )", "39")},
			{"source_unpositioned", "xt_src", "1/0", "doc", "[", "id=1", "id integer,a text", xtStatement(xml2Error("22012", "division by zero"), "SELECT 1/0, doc FROM xt_src WHERE id=1")},
			{"source_width_empty", "xt_src", "id, id", "doc", "/doc/a", "false", "id integer,a text", xtWidthDiagnostic()},
			{"empty_element_integer", "xt_src", "id", "doc", "/doc/a", "id=2", "id integer,a integer", xml2Error("22P02", `invalid input syntax for type integer: ""`)},
			{"nonnumeric_key", "xt_src", "'not-an-integer'", "doc", "/doc/b", "id=1", "id integer,a integer", xml2Error("22P02", `invalid input syntax for type integer: "not-an-integer"`)},
			{"integer_overflow", "xt_src", "id", "'<doc><a>2147483648</a></doc>'", "/doc/a", "id=1", "id integer,a integer", xml2Error("22003", `value "2147483648" is out of range for type integer`)},
			{"varchar_typmod", "xt_src", "id", "doc", "/doc/a", "id=1", "id integer,a varchar(1)", xml2Error("22001", "value too long for type character varying(1)")},
		} {
			xtFailure(t, p, b, c, tc.name, xtQuery(tc.key, tc.doc, tc.relation, tc.path, tc.criteria, tc.definitions), tc.want, false)
		}
		xtExec(t, p, b, c, "CREATE TABLE xt_accum(seq integer,id integer,doc text)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_accum VALUES(1,5,'<doc>'),(2,3,'<doc/>')", 2))
		// Explicit source ordering distinguishes per-invocation error accumulation.
		xtFailure(t, p, b, c, "malformed_then_valid_accumulation", xtQuery("id", "doc", "(SELECT id,doc FROM xt_accum ORDER BY seq) AS s", "[", "true", "id integer,a text"), xml2Compile("line 1: Premature end of data in tag doc line 1\n<doc>\n     ^\nInvalid expression"), false)
		xtEvaluationDiagnostics(t, p, b, c)
		xtExec(t, p, b, c,
			"CREATE DOMAIN xt_not_null AS text NOT NULL",
			"CREATE DOMAIN xt_positive AS integer CONSTRAINT xt_positive_check CHECK(VALUE>0)")
		notNull := xml2Error("23502", "domain xt_not_null does not allow null values")
		notNull.Schema, notNull.DataType = "public", "xt_not_null"
		xtFailure(t, p, b, c, "null_spare_domain", xtQuery("id", "doc", "xt_src", "/doc/b", "id=1", "id integer,a integer,spare xt_not_null"), notNull, false)
		xtFailure(t, p, b, c, "null_document_domain", xtQuery("id", "doc", "xt_src", "[", "id=4", "id integer,a xt_not_null"), notNull, false)
		check := xml2Error("23514", `value for domain xt_positive violates check constraint "xt_positive_check"`)
		check.Schema, check.DataType, check.Constraint = "public", "xt_positive", "xt_positive_check"
		xtFailure(t, p, b, c, "check_domain", xtQuery("id", "'<doc><a>-1</a></doc>'", "xt_src", "/doc/a", "id=1", "id integer,a xt_positive"), check, false)
		xtBoth(t, p, b, c, "empty_source_no_domain_input", fixture.Select(xtQuery("id", "doc", "xt_src", "[", "false", "id integer,a integer,spare xt_not_null"), []string{"id", "a", "spare"}, []string{"INT4", "INT4", "TEXT"}, nil))
	})
}

func xtWidthDiagnostic() fixture.Diagnostic {
	d := xml2Error("22023", "expression returning multiple columns is not valid in parameter list")
	d.Detail = "Expected two columns in SPI result, got 3."
	return d
}

// Expectations follow the released invocation-wide LEGACY error buffer.
// Independently specified combined envelopes were confirmed by the pinned
// PostgreSQL checkpoint; fixture comparison exposes every logical field.
func xtEvaluationDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const definitions = "id integer,unresolved text,invalid text"
	const accumulated = "Undefined namespace prefix\nInvalid expression"
	const malformed = "line 1: Premature end of data in tag doc line 1\n<doc>\n     ^\n"
	const namespace = "line 1: Namespace prefix p on r is not defined\n<p:r>8</p:r>\n    ^\n"
	for _, tc := range []struct {
		name, document, relation, paths, criteria, columns, detail string
	}{
		{"f1_evaluation_then_compile", "doc", "xt_src", "/p:doc|[", "id=3", definitions, accumulated},
		{"f1_compile_before_evaluation", "doc", "xt_src", "[|/p:doc", "id=3", definitions, "Invalid expression"},
		{"f1_repeated_evaluation_then_compile", "doc", "xt_src", "/p:doc|/q:doc|[", "id=3", "id integer,first text,second text,invalid text", "Undefined namespace prefix\n" + accumulated},
		{"f1_prior_document_parser_evaluation_compile", "doc", "(SELECT id,doc FROM xt_accum ORDER BY seq) AS s", "/p:doc|[", "true", definitions, malformed + accumulated},
		{"f1_same_document_parser_evaluation_compile", xtQuote("<p:r>8</p:r>"), "xt_src", "/p:r|[", "id=3", definitions, namespace + accumulated},
	} {
		xtFailure(t, p, b, c, tc.name, xtQuery("id", tc.document, tc.relation, tc.paths, tc.criteria, tc.columns), xml2Compile(tc.detail), false)
	}
	xtFailure(t, p, b, c, "f1_fresh_invocation_isolation", xtQuery("id", "doc", "xt_src", "[", "id=3", "id integer,a text"), xml2Compile("Invalid expression"), false)
	t.Run("f1_held_invocation_isolation", func(t *testing.T) {
		const query = "SELECT * FROM xpath_table($1::text,$2::text,$3::text,$4::text,$5::text) AS x(id integer,a text,b text) ORDER BY id,a,b"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		fail := func(paths, detail string) {
			t.Helper()
			step := fixture.ErrorDiagnostic(query, xml2Compile(detail), "id", "doc", "xt_src", paths, "id=3")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		healthy := fixture.Select(query, []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"},
			[][]any{{int64(1), "10", "7"}, {int64(1), "20", nil}}, "id", "doc", "xt_src", "/doc/a|/doc/b", "id=1")
		fail("/p:doc|[", accumulated)
		fail("[|/p:doc", "Invalid expression")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, healthy)
		// Suppressed evaluation failures remain nonfatal and do not become
		// notices; their per-iteration diagnostics must not leak to the next call.
		suppressed := fixture.Select(query, []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"},
			[][]any{{int64(1), nil, "10"}, {int64(1), nil, "20"}}, "id", "doc", "xt_src", "/p:doc|/doc/a", "id=1")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, suppressed)
		fail("/p:doc|[", accumulated)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, healthy)
		fail("[", "Invalid expression")
	})
}
