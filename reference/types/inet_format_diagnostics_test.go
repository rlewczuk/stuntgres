package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceINETFormatResolution(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"host", "pg_catalog.host", `"host"`, `pg_catalog."host"`} {
			inetFormatBoth(t, p, b, fixture.Select("SELECT "+name+"('192.168.1.5/24') AS h", []string{"h"}, []string{"TEXT"}, [][]any{{"192.168.1.5"}}))
		}
		for _, expr := range []string{"abbrev('10.1.0.0/16'::inet)", "abbrev('10.1.0.0/16')", "abbrev($1)"} {
			args := []any(nil)
			if strings.Contains(expr, "$1") {
				args = []any{"10.1.0.0/16"}
			}
			inetFormatBoth(t, p, b, fixture.Select("SELECT "+expr+" AS a", []string{"a"}, []string{"TEXT"}, [][]any{{"10.1.0.0/16"}}, args...))
		}
		inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev(NULL) AS a,host(NULL) AS h,text(1) AS t,text(CIDR '10.1') AS c,INET '::1'::text AS x,CAST(INET '::1' AS text) AS y", []string{"a", "h", "t", "c", "x", "y"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, "1", "10.1.0.0/16", "::1/128", "::1/128"}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE nf_names(abbrev integer,host integer,family integer,masklen integer,broadcast integer,inet_same_family integer)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO nf_names VALUES(1,2,3,4,5,6)", 1))
		inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev,host,family,masklen,broadcast,inet_same_family FROM nf_names", []string{"abbrev", "host", "family", "masklen", "broadcast", "inet_same_family"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)}}))
	})
}

func TestReferenceINETFormatDiagnostics(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		inetFormatSyntaxDiagnostics(t, p, b)
		for _, name := range []string{"abbrev", "host", "family", "masklen", "broadcast", "inet_same_family"} {
			for _, arg := range []struct{ sql, signature string }{{"1", "integer"}, {"TRUE", "boolean"}, {"'x'::text", "text"}, {"", ""}, {"NULL::inet,NULL::inet,NULL::inet", "inet, inet, inet"}, {"x => NULL::inet", "x => inet"}} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + name + "(" + arg.sql + ")"
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+arg.signature+") does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
					s := fixture.ErrorDiagnostic(q, d)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		for _, tc := range []struct {
			q, typ, input string
			host          bool
		}{
			{"SELECT host('bad')", "inet", "bad", false}, {"SELECT abbrev('bad')", "inet", "bad", false}, {"SELECT host('192.168.0.1/24'::cidr)", "cidr", "192.168.0.1/24", true},
		} {
			d := inetDiagnostic(tc.typ, tc.input, tc.host)
			d.Position = fixture.Position(strings.Index(tc.q, "'") + 1)
			s := fixture.ErrorDiagnostic(tc.q, d)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE nf_atomic(id integer,h text)", 0))
		run(fixture.Exec("INSERT INTO nf_atomic VALUES(99,'saved')", 1))
		q := "INSERT INTO nf_atomic VALUES(1,host($1::inet)),(2,host($2::inet))"
		d := inetDiagnostic("inet", "bad", false)
		d.Context = "unnamed portal parameter $2 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d, "::1", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			inetFormatBoth(t, p, b, fixture.Select("SELECT id,h FROM nf_atomic", []string{"id", "h"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "saved"}}))
		}
		for _, name := range []string{"abbrev", "host", "text", "family", "masklen", "broadcast", "inet_same_family"} {
			expr := name + "(CAST('bad'::text AS inet)"
			if name == "inet_same_family" {
				expr += ",INET '::1'"
			}
			expr += ")"
			s := fixture.ErrorDiagnostic("SELECT "+expr, inetDiagnostic("inet", "bad", false))
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.Prepared = true
			run(s)
		}
		for _, expr := range []string{"inet_same_family(NULL::inet,CAST('bad'::text AS inet))", "inet_same_family(CAST('bad'::text AS inet),NULL::inet)"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT "+expr, inetDiagnostic("inet", "bad", false))
				s.SelectRows = true
				s.ErrorStage = "execute"
				s.Prepared = prepared
				run(s)
			}
		}
	})
}

func inetFormatSyntaxDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE nf_errors(ip inet,n integer)", 0))
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"abbrev", "host", "family", "masklen", "broadcast", "inet_same_family"} {
		args := "ip"
		sig := "inet"
		if name == "inet_same_family" {
			args = "ip,ip"
			sig = "inet, inet"
		}
		cases := []struct {
			q, code, message, hint, at string
			last                       bool
		}{
			{"SELECT public." + name + "(" + args + ") FROM nf_errors", "42883", "function public." + name + "(" + sig + ") does not exist", hint, "public", false},
			{`SELECT "` + strings.ToUpper(name) + `"(` + args + ") FROM nf_errors", "42883", "function " + strings.ToUpper(name) + "(" + sig + ") does not exist", hint, `"` + strings.ToUpper(name) + `"`, false},
			{"SELECT " + name + "(x => ip,x => ip) FROM nf_errors", "42601", `argument name "x" used more than once`, "", "x =>", true},
			{"SELECT " + name + "(x => ip,ip) FROM nf_errors", "42601", "positional argument cannot follow named argument", "", "ip)", false},
			{"SELECT " + name + "(" + strings.TrimSuffix(strings.Repeat("ip,", 101), ",") + ") FROM nf_errors", "54023", "cannot pass more than 100 arguments to a function", "", name, false},
			{"SELECT " + name + "(" + args + ") FILTER (WHERE missing_filter) FROM nf_errors", "42703", `column "missing_filter" does not exist`, "", "missing_filter", false},
			{"SELECT " + name + "(" + args + ") FILTER (WHERE n) FROM nf_errors", "42804", "argument of FILTER must be type boolean, not type integer", "", "n)", false},
			{"SELECT " + name + "(missing) FILTER (WHERE missing_filter) FROM nf_errors", "42703", `column "missing" does not exist`, "", "missing", false},
		}
		for _, dec := range []struct{ a, tail, label, kind string }{
			{"DISTINCT " + args, "", "DISTINCT", "an aggregate function"}, {args + " ORDER BY 1", "", "ORDER BY", "an aggregate function"}, {args, " FILTER (WHERE true)", "FILTER", "an aggregate function"}, {args, " OVER ()", "OVER", "a window function nor an aggregate function"},
		} {
			cases = append(cases, struct {
				q, code, message, hint, at string
				last                       bool
			}{"SELECT " + name + "(" + dec.a + ")" + dec.tail + " FROM nf_errors", "42809", dec.label + " specified, but " + name + " is not " + dec.kind, "", name, false})
		}
		for _, tc := range cases {
			pos := strings.Index(tc.q, tc.at)
			if tc.last {
				pos = strings.LastIndex(tc.q, tc.at)
			}
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Hint = tc.hint
			d.Position = fixture.Position(utf8.RuneCountInString(tc.q[:pos]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	}
}
