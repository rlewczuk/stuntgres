package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

type regexpSignature struct {
	name, typ                 string
	formals, values, sqltypes []string
	value                     any
	set                       bool
}

// These are actual release overloads, not optional/default argument inventions.
func regexpSignatures() []regexpSignature {
	var all []regexpSignature
	add := func(name, typ string, formals, values, sqltypes []string, value any, set bool) {
		all = append(all, regexpSignature{name, typ, formals, values, sqltypes, value, set})
	}
	for n := 2; n <= 4; n++ {
		add("regexp_count", "INT4", []string{"string", "pattern", "start", "flags"}[:n], []string{"'aba'", "'a'", "1", "''"}[:n], []string{"text", "text", "integer", "text"}[:n], int64(2), false)
	}
	for n := 2; n <= 7; n++ {
		add("regexp_instr", "INT4", []string{"string", "pattern", "start", "\"N\"", "endoption", "flags", "subexpr"}[:n], []string{"'aba'", "'(a)'", "1", "1", "0", "''", "1"}[:n], []string{"text", "text", "integer", "integer", "integer", "text", "integer"}[:n], int64(1), false)
	}
	for _, c := range []struct {
		name, typ string
		value     any
		set       bool
	}{
		{"regexp_like", "BOOL", true, false}, {"regexp_match", "_TEXT", "{a}", false}, {"regexp_matches", "_TEXT", "{a}", true}, {"regexp_split_to_array", "_TEXT", "{a,b}", false}, {"regexp_split_to_table", "TEXT", "a", true},
	} {
		for n := 2; n <= 3; n++ {
			source, pattern := "'a'", "'(a)'"
			if strings.Contains(c.name, "split") {
				source, pattern = "'a,b'", "','"
			}
			add(c.name, c.typ, []string{"string", "pattern", "flags"}[:n], []string{source, pattern, "''"}[:n], []string{"text", "text", "text"}[:n], c.value, c.set)
		}
	}
	for _, c := range []struct{ f, v, types []string }{
		{[]string{"string", "pattern", "replacement"}, []string{"'aba'", "'a'", "'X'"}, []string{"text", "text", "text"}},
		{[]string{"string", "pattern", "replacement", "flags"}, []string{"'aba'", "'a'", "'X'", "''"}, []string{"text", "text", "text", "text"}},
		{[]string{"string", "pattern", "replacement", "start"}, []string{"'aba'", "'a'", "'X'", "1"}, []string{"text", "text", "text", "integer"}},
		{[]string{"string", "pattern", "replacement", "start", "\"N\""}, []string{"'aba'", "'a'", "'X'", "1", "1"}, []string{"text", "text", "text", "integer", "integer"}},
		{[]string{"string", "pattern", "replacement", "start", "\"N\"", "flags"}, []string{"'aba'", "'a'", "'X'", "1", "1", "''"}, []string{"text", "text", "text", "integer", "integer", "text"}},
	} {
		add("regexp_replace", "TEXT", c.f, c.v, c.types, "Xba", false)
	}
	for n := 2; n <= 6; n++ {
		add("regexp_substr", "TEXT", []string{"string", "pattern", "start", "\"N\"", "flags", "subexpr"}[:n], []string{"'aba'", "'(a)'", "1", "1", "''", "1"}[:n], []string{"text", "text", "integer", "integer", "text", "integer"}[:n], "a", false)
	}
	return all
}

func regexpSignatureRows(c regexpSignature) [][]any {
	if c.name == "regexp_split_to_table" {
		return [][]any{{"a"}, {"b"}}
	}
	return [][]any{{c.value}}
}

func TestReferenceRegexpSignatures(t *testing.T) {
	regexpNamespace(t)
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range regexpSignatures() {
			for _, prefix := range []string{"", "pg_catalog."} {
				q := "SELECT " + prefix + c.name + "(" + strings.Join(c.values, ",") + ") AS result"
				regexpRoutes(t, p, b, regexpRows(b, q, []string{"result"}, []string{c.typ}, regexpSignatureRows(c)))
				from := "SELECT r.result FROM " + prefix + c.name + "(" + strings.Join(c.values, ",") + ") AS r(result)"
				regexpRoutes(t, p, b, regexpRows(b, from, []string{"result"}, []string{c.typ}, regexpSignatureRows(c)))
			}
		}
		for _, typ := range []string{"text", "varchar(8)", "char(3)", "name"} {
			regexpRoutes(t, p, b, regexpOne(b, fmt.Sprintf("regexp_like('a'::%s,'^a$')", typ), "BOOL", true))
		}
	})
}

func TestReferenceRegexpNamedArguments(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range regexpSignatures() {
			var named []string
			for i := len(c.formals) - 1; i >= 0; i-- {
				named = append(named, c.formals[i]+" => "+c.values[i])
			}
			for _, args := range []string{strings.Join(named, ","), c.values[0] + ", " + strings.Join(named[:len(named)-1], ",")} {
				q := "SELECT pg_catalog." + c.name + "(" + args + ") AS result"
				regexpRoutes(t, p, b, regexpRows(b, q, []string{"result"}, []string{c.typ}, regexpSignatureRows(c)))
			}
		}
		for _, c := range []struct{ q, code, msg, token string }{
			{`SELECT regexp_like(string=>'a',string=>'a',pattern=>'a')`, "42601", `argument name "string" used more than once`, "string=>'a',pattern"},
			{`SELECT regexp_like(string=>'a','a')`, "42601", `positional argument cannot follow named argument`, "'a')"},
			{`SELECT /* é🙂 */ regexp_instr(string=>'a',pattern=>'a',start=>1,n=>1)`, "42883", `function regexp_instr(string => unknown, pattern => unknown, start => integer, n => integer) does not exist`, "regexp_instr"},
			{`SELECT regexp_like(source=>'a',pattern=>'a')`, "42883", `function regexp_like(source => unknown, pattern => unknown) does not exist`, "regexp_like"},
		} {
			hint := ""
			if c.code == "42883" {
				hint = similarFunctionHint
			}
			regexpRoutes(t, p, b, regexpBindError(c.q, c.code, c.msg, hint, c.token))
		}
	})
}

func TestReferenceRegexpInference(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		regexpWrongSignatureRoles(t, p, b)
		for _, c := range regexpSignatures() {
			placeholders := make([]string, len(c.values))
			args := make([]any, len(c.values))
			for i, v := range c.values {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
				if c.sqltypes[i] == "integer" {
					args[i] = 1
					if c.formals[i] == "endoption" {
						args[i] = 0
					}
				} else {
					args[i] = strings.Trim(v, "'")
				}
			}
			// UNKNOWN fourth parameter prefers the flags TEXT overload, regardless of
			// the Go argument's type; an explicit SQL cast selects the start overload.
			if c.name == "regexp_replace" && len(c.values) == 4 && c.formals[3] == "start" {
				placeholders[3] += "::integer"
			}
			q := "SELECT " + c.name + "(" + strings.Join(placeholders, ",") + ") AS result"
			regexpRoutes(t, p, b, regexpRows(b, q, []string{"result"}, []string{c.typ}, regexpSignatureRows(c), args...))
		}
		regexpRoutes(t, p, b, regexpOne(b, `regexp_replace('abcabc','b','X','4','0')`, "TEXT", "abcaXc"))
		regexpRoutes(t, p, b, regexpRuntimeError(`SELECT regexp_replace($1,$2,$3,$4) AS result`, "22023", `invalid regular expression option: "1"`, `If you meant to use regexp_replace() with a start parameter, cast the fourth argument to integer explicitly.`, "aba", "a", "X", 1))
		parameterQuery := `SELECT regexp_count($1,'a',$1)`
		missing := regexpBindError(parameterQuery, "42P02", `there is no parameter $1`, "", "$1,")
		if !b.PG {
			// The native no-argument adapter still analyzes the inconsistent
			// occurrence types, unlike pq's simple-query missing-parameter
			// admission. The provided-argument proof below remains shared.
			missing = regexpBindError(parameterQuery, "42P08", `inconsistent types deduced for parameter $1`, "", "$1)")
			missing.Diagnostic.Detail = "text versus integer"
		}
		missing.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, missing)
		conflict := regexpBindError(parameterQuery, "42P08", `inconsistent types deduced for parameter $1`, "", "$1)")
		conflict.Diagnostic.Detail = "text versus integer"
		conflict.Args = []any{"a"}
		regexpRoutes(t, p, b, conflict)
		for _, c := range []struct{ q, code, msg, token string }{
			{`SELECT regexp_like(1,'a')`, "42883", `function regexp_like(integer, unknown) does not exist`, "regexp_like"},
			{`SELECT /* é漢 */ pg_catalog.regexp_like('a'::bytea,'a')`, "42883", `function pg_catalog.regexp_like(bytea, unknown) does not exist`, "pg_catalog"},
			{`SELECT regexp_count('a')`, "42883", `function regexp_count(unknown) does not exist`, "regexp_count"},
			{`SELECT regexp_replace('a','a','x',1,'g')`, "22P02", `invalid input syntax for type integer: "g"`, "'g'"},
			{`SELECT regexp_count('a','a','oops')`, "22P02", `invalid input syntax for type integer: "oops"`, "'oops'"},
			{`SELECT regexp_count('a','a','2147483648')`, "22003", `value "2147483648" is out of range for type integer`, "'2147483648'"},
		} {
			hint := ""
			if c.code == "42883" {
				hint = similarFunctionHint
			}
			regexpRoutes(t, p, b, regexpBindError(c.q, c.code, c.msg, hint, c.token))
		}
	})
}

func TestReferenceRegexpMetadata(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range regexpSignatures() {
			q := "SELECT " + c.name + "(" + strings.Join(c.values, ",") + ")"
			regexpRoutes(t, p, b, regexpRows(b, q, []string{c.name}, []string{c.typ}, regexpSignatureRows(c)))
			q += " WHERE false"
			regexpRoutes(t, p, b, regexpRows(b, q, []string{c.name}, []string{c.typ}, nil))
			values := append([]string(nil), c.values...)
			values[0] = "NULL::text"
			q = "SELECT " + c.name + "(" + strings.Join(values, ",") + ") AS nullable_result"
			rows := [][]any{{nil}}
			if c.set {
				rows = nil
			}
			regexpRoutes(t, p, b, regexpRows(b, q, []string{"nullable_result"}, []string{c.typ}, rows))
		}
	})
}
