package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Source-owned failures are specified independently from REL_18_6 save-context
// branches and the owning ordinary reference diagnostic definitions. No SQLSTATE
// family is assumed soft. ProviderHardErrors specifies opposite-origin controls.
type copyFilterProviderFailure struct {
	name, bad, code, message, detail, prefix, display string
}
type copyFilterProviderFamily struct {
	name, typ, databaseType, first, last, firstValue, lastValue string
	failures                                                    []copyFilterProviderFailure
}

func copyFilterProviderFamilies() []copyFilterProviderFamily {
	word := strings.Repeat("x", 2047)
	return []copyFilterProviderFamily{
		{name: "array", typ: "int[]", databaseType: "_INT4", first: "{1}", last: "{3}", firstValue: "{1}", lastValue: "{3}", failures: []copyFilterProviderFailure{
			{name: "inverted", bad: "[1:0]={}", code: "2202E", message: "upper bound cannot be less than lower bound"},
			{name: "upper_max", bad: "[2147483646:2147483647]={1,2}", code: "54000", message: "array upper bound is too large: 2147483647"},
			{name: "bound_overflow", bad: "[2147483648:2147483648]={1}", code: "54000", message: "array bound is out of integer range"},
			{name: "length_overflow", bad: "[-2147483648:0]={}", code: "54000", message: "array size exceeds the maximum allowed (134217727)"},
			{name: "explicit_rank", bad: strings.Repeat("[1:1]", 7) + "={1}", code: "54000", message: "number of array dimensions exceeds the maximum allowed (6)"},
			{name: "contents_rank", bad: "{{{{{{{1}}}}}}}", code: "54000", message: "number of array dimensions exceeds the maximum allowed (6)"},
			{name: "structure", bad: "{1,,2}", code: "22P02", message: `malformed array literal: "{1,,2}"`, detail: `Unexpected "," character.`},
		}},
		{name: "int4range", typ: "int4range", databaseType: "INT4RANGE", first: "[1,2)", last: "[3,4)", firstValue: "[1,2)", lastValue: "[3,4)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[3,1)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
			{name: "canonical_overflow", bad: "[1,2147483647]", code: "22003", message: "integer out of range"},
		}},
		{name: "int8range", typ: "int8range", databaseType: "INT8RANGE", first: "[1,2)", last: "[3,4)", firstValue: "[1,2)", lastValue: "[3,4)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[3,1)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
			{name: "canonical_overflow", bad: "[1,9223372036854775807]", code: "22003", message: "bigint out of range"},
		}},
		{name: "numrange", typ: "numrange", databaseType: "NUMRANGE", first: "[1,2)", last: "[3,4)", firstValue: "[1,2)", lastValue: "[3,4)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[3,1)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "daterange", typ: "daterange", databaseType: "DATERANGE", first: "empty", last: "[2000-01-01,2000-01-02)", firstValue: "empty", lastValue: "[2000-01-01,2000-01-02)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[2000-01-02,2000-01-01)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "tsrange", typ: "tsrange", databaseType: "TSRANGE", first: "empty", last: "(,)", firstValue: "empty", lastValue: "(,)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[2000-01-02,2000-01-01)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "tstzrange", typ: "tstzrange", databaseType: "TSTZRANGE", first: "empty", last: "(,)", firstValue: "empty", lastValue: "(,)", failures: []copyFilterProviderFailure{
			{name: "reversed", bad: "[2000-01-02,2000-01-01)", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "int4multirange", typ: "int4multirange", databaseType: "INT4MULTIRANGE", first: "{[1,2)}", last: "{[3,4)}", firstValue: "{[1,2)}", lastValue: "{[3,4)}", failures: []copyFilterProviderFailure{
			{name: "reversed_child", bad: "{[3,1)}", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "int8multirange", typ: "int8multirange", databaseType: "INT8MULTIRANGE", first: "{[1,2)}", last: "{[3,4)}", firstValue: "{[1,2)}", lastValue: "{[3,4)}", failures: []copyFilterProviderFailure{
			{name: "reversed_child", bad: "{[3,1)}", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "nummultirange", typ: "nummultirange", databaseType: "NUMMULTIRANGE", first: "{[1,2)}", last: "{[3,4)}", firstValue: "{[1,2)}", lastValue: "{[3,4)}", failures: []copyFilterProviderFailure{
			{name: "reversed_child", bad: "{[3,1)}", code: "22000", message: "range lower bound must be less than or equal to range upper bound"},
		}},
		{name: "jsonpath", typ: "jsonpath", databaseType: "JSONPATH", first: "$", last: "$.x", firstValue: "$", lastValue: `$."x"`, failures: []copyFilterProviderFailure{
			{name: "last_scope", bad: "last", code: "42601", message: "LAST is allowed only in array subscripts"},
			{name: "current_scope", bad: "@ + 1", code: "42601", message: "@ is not allowed in root expressions"},
			{name: "grammar", bad: "$[", code: "42601", message: "syntax error at end of jsonpath input"},
			{name: "numeric_lexer", bad: "1.type()", code: "42601", message: `trailing junk after numeric literal at or near "1.t" of jsonpath input`},
			{name: "unicode_zero", bad: `"\u0000"`, code: "22P05", message: "unsupported Unicode escape sequence", detail: `\u0000 cannot be converted to text.`},
			{name: "unicode_escape", bad: `"\u000g"`, code: "42601", message: `invalid Unicode escape sequence at or near "\u000" of jsonpath input`},
			{name: "surrogate", bad: `"\ud83dX"`, code: "22P02", message: "invalid input syntax for type jsonpath", detail: "Unicode low surrogate must follow a high surrogate."},
			{name: "regex", bad: `$ ? (@ like_regex "(")`, code: "2201B", message: "invalid regular expression: parentheses () not balanced"},
			{name: "regex_flag", bad: `$ ? (@ like_regex "a" flag "a")`, code: "42601", message: "invalid input syntax for type jsonpath", detail: `Unrecognized flag character "a" in LIKE_REGEX predicate.`},
			{name: "expanded_flag", bad: `$ ? (@ like_regex "a" flag "x")`, code: "0A000", message: `XQuery "x" flag (expanded regular expressions) is not implemented`},
			{name: "decimal_arity", bad: "$.decimal(1,2,3)", code: "42601", message: "invalid input syntax for type jsonpath", detail: ".decimal() can only have an optional precision[,scale]."},
		}},
		{name: "bit", typ: "bit(2)", databaseType: "BIT", first: "01", last: "10", firstValue: "01", lastValue: "10", failures: []copyFilterProviderFailure{
			{name: "width", bad: "1", code: "22026", message: "bit string length 1 does not match type bit(2)"},
		}},
		{name: "jsonb", typ: "jsonb", databaseType: "JSONB", first: "1", last: "3", firstValue: "1", lastValue: "3", failures: []copyFilterProviderFailure{
			{name: "unicode_zero", bad: `"\u0000"`, code: "22P05", message: "unsupported Unicode escape sequence", detail: `\u0000 cannot be converted to text.`, prefix: `JSON data, line 1: "\u0000...`},
		}},
		{name: "tsvector", typ: "tsvector", databaseType: "TSVECTOR", first: "a", last: "b", firstValue: "'a'", lastValue: "'b'", failures: []copyFilterProviderFailure{
			{name: "position", bad: "a:0", code: "42601", message: `wrong position info in tsvector: "a:0"`},
			{name: "syntax", bad: "a:", code: "42601", message: `syntax error in tsvector: "a:"`},
			{name: "escape", bad: `a\`, code: "42601", message: `there is no escaped character: "a\"`},
			{name: "word_size", bad: word, code: "54000", message: "word is too long (2047 bytes, max 2046 bytes)", display: strings.Repeat("x", 100) + "..."},
		}},
		{name: "tsquery", typ: "tsquery", databaseType: "TSQUERY", first: "a", last: "b", firstValue: "'a'", lastValue: "'b'", failures: []copyFilterProviderFailure{
			{name: "operand", bad: "a &", code: "42601", message: `no operand in tsquery: "a &"`},
			{name: "syntax", bad: "a b", code: "42601", message: `syntax error in tsquery: "a b"`},
			{name: "escape", bad: `a\`, code: "42601", message: `there is no escaped character: "a\"`},
			{name: "distance", bad: "a <16385> b", code: "22023", message: "distance in phrase operator must be an integer value between zero and 16384 inclusive"},
			{name: "word_size", bad: word, code: "54000", message: `word is too long in tsquery: "` + word + `"`, display: strings.Repeat("x", 100) + "..."},
		}},
		{name: "query_int", typ: "query_int", databaseType: "QUERY_INT", first: "1", last: "3", firstValue: "1", lastValue: "3", failures: []copyFilterProviderFailure{
			{name: "syntax", bad: "a", code: "42601", message: "syntax error"},
			{name: "complexity", bad: strings.Repeat("!", 17) + "1", code: "54001", message: "statement too complex"},
		}},
		{name: "aclitem", typ: "aclitem", databaseType: "ACLITEM", first: "=r/copy_provider_owner", last: "=w/copy_provider_owner", firstValue: "=r/copy_provider_owner", lastValue: "=w/copy_provider_owner", failures: []copyFilterProviderFailure{
			{name: "grantee", bad: "copy_provider_absent=r/copy_provider_owner", code: "42704", message: `role "copy_provider_absent" does not exist`},
			{name: "grantor", bad: "=r/copy_provider_absent", code: "42704", message: `role "copy_provider_absent" does not exist`},
			{name: "identifier_ascii", bad: strings.Repeat("n", 64) + "=r/copy_provider_owner", code: "42622", message: "identifier too long", detail: "Identifier must be less than 64 characters."},
			{name: "identifier_utf8", bad: strings.Repeat("é", 32) + "=r/copy_provider_owner", code: "42622", message: "identifier too long", detail: "Identifier must be less than 64 characters."},
		}},
		{name: "line", typ: "line", databaseType: "LINE", first: "{1,-1,0}", last: "{1,-1,1}", firstValue: "{1,-1,0}", lastValue: "{1,-1,1}", failures: []copyFilterProviderFailure{
			{name: "lexical_overflow", bad: "{1e400,2,3}", code: "22003", message: `"1e400" is out of range for type double precision`},
		}},
		{name: "nested_bit", typ: "copy_provider_outer", first: `("(01)")`, last: `("(10)")`, firstValue: `("(01)")`, lastValue: `("(10)")`, failures: []copyFilterProviderFailure{
			{name: "width", bad: `("(1)")`, code: "22026", message: "bit string length 1 does not match type bit(2)"},
		}},
		{name: "domain_bit", typ: "copy_provider_bits", databaseType: "BIT", first: "01", last: "10", firstValue: "01", lastValue: "10", failures: []copyFilterProviderFailure{
			{name: "width", bad: "1", code: "22026", message: "bit string length 1 does not match type bit(2)"},
		}},
	}
}

func copyFilterProviderSetup(t *testing.T, c copyTextConnection, family copyFilterProviderFamily) {
	t.Helper()
	switch family.name {
	case "aclitem":
		c.exec(t, "CREATE ROLE copy_provider_owner")
	case "query_int":
		c.exec(t, "CREATE EXTENSION intarray")
	case "nested_bit":
		c.exec(t, "CREATE TYPE copy_provider_inner AS(b bit(2))")
		c.exec(t, "CREATE TYPE copy_provider_outer AS(i copy_provider_inner)")
	case "domain_bit":
		c.exec(t, "CREATE DOMAIN copy_provider_bits AS bit(2)")
	}
}
func (f copyFilterProviderFailure) shown() string {
	if f.display != "" {
		return f.display
	}
	return f.bad
}
func (f copyFilterProviderFailure) diagnostic(table string, line int) fixture.Diagnostic {
	d := copyFilterAliasDiagnostic(table, f.shown(), line, f.code, f.message)
	d.Detail = f.detail
	if f.prefix != "" {
		d.Context = f.prefix + "\n" + d.Context
	}
	return d
}
func copyFilterProviderType(t *testing.T, c copyTextConnection, family copyFilterProviderFamily) string {
	t.Helper()
	if c.pg {
		switch family.typ {
		case "tsquery":
			return "3615" // Not registered by the existing pgx type map.
		case "query_int", "copy_provider_outer":
			return strconv.FormatInt(int64(copyBinaryTypeOID(t, c, family.typ)), 10)
		}
	}
	return family.databaseType
}
func copyFilterProviderRows(t *testing.T, c copyTextConnection, family copyFilterProviderFamily, table string, accepted bool) {
	t.Helper()
	want := [][]any{{int64(99), family.firstValue}}
	output := "99\t" + family.firstValue + "\n"
	count := int64(1)
	if accepted {
		want = [][]any{{int64(1), family.firstValue}, {int64(3), family.lastValue}, {int64(99), family.firstValue}}
		output = "1\t" + family.firstValue + "\n3\t" + family.lastValue + "\n" + output
		count = 3
	}
	c.selectRows(t, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", copyFilterProviderType(t, c, family)}, want)
	// Expected COPY text is independently escaped, not exported native goldens.
	output = strings.ReplaceAll(output, "\\", "\\\\")
	c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", output, count)
}

func TestReferenceCopyFilterProviderInputs(t *testing.T) {
	for _, family := range copyFilterProviderFamilies() {
		t.Run(family.name, func(t *testing.T) {
			if family.name == "aclitem" {
				fixture.RoleReferenceCleanup(t, []string{"copy_provider_owner"})
			}
			copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
				copyFilterProviderSetup(t, c, family)
				for _, format := range []string{"text", "csv"} {
					for _, failure := range family.failures {
						t.Run(format+"/"+failure.name, func(t *testing.T) {
							*notices = (*notices)[:0]
							table := "copy_provider_input_" + family.name
							c.exec(t, "CREATE TABLE "+table+"(id int,v "+family.typ+")")
							defer c.exec(t, "DROP TABLE "+table)
							c.exec(t, "INSERT INTO "+table+" VALUES(99,"+fixture.RegtypeLiteral(family.first)+")")
							input := copyFilterAliasRow(format, 1, family.first) + copyFilterAliasRow(format, 2, failure.bad) + copyFilterAliasRow(format, 3, family.last)
							for _, stop := range []string{"", ",ON_ERROR stop"} {
								copyFilterFailure(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+stop+")", input, failure.diagnostic(table, 2))
								copyFilterProviderRows(t, c, family, table, false)
								copyFilterNotices(t, *notices)
							}
							copyFilterImport(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,LOG_VERBOSITY verbose)", input, 2)
							copyFilterProviderRows(t, c, family, table, true)
							copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"`+failure.shown()+`"`, 2), copyFilterSummary(1))
						})
					}
				}
			})
		})
	}
}

// Keep ordinary COPY limit diagnostics independent from the original provider's
// code, Detail and inner Context: N+1 is the command owner's new22P02 failure.
func copyFilterProviderLimitDiagnostic(table string, failure copyFilterProviderFailure, line int) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "22P02", Message: "skipped more than REJECT_LIMIT (1) rows due to data type incompatibility", Context: fmt.Sprintf("COPY %s, line %d, column v: \"%s\"", table, line, failure.shown())}
}
