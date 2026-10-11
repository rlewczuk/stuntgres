package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringReplaceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func stringReplaceBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func stringReplaceValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}))
}

func TestReferenceStringReplaceOverlay(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, want string }{
			{"overlay('Txxxxas' PLACING 'hom' FROM 2 FOR 4)", "Thomas"},
			{"overlay('abcdef','XY',3)", "abXYef"}, {"overlay('abcdef','XY',3,2)", "abXYef"},
			{"overlay('abcdef','XY',3,0)", "abXYcdef"}, {"overlay('abcdef','XY',3,-1)", "abXYbcdef"},
			{"overlay('abcdef','XY',2,-4)", "aXYabcdef"}, {"overlay('abcdef','X',7)", "abcdefX"},
			{"overlay('abcdef','X',9)", "abcdefX"}, {"overlay('abcdef','X',2,99)", "aX"},
			{"overlay('','🙂',1)", "🙂"}, {"overlay('abcdef','',3)", "abcdef"}, {"overlay('abcdef','',3,2)", "abef"},
			{"overlay('Aé🙂Z','Ω',2)", "AΩ🙂Z"}, {"overlay('Aé🙂Z','Ω🙂',2)", "AΩ🙂Z"},
			{`overlay(U&'a\0301b','X',2,1)`, "aXb"}, {"overlay('abc','X',2147483647,0)", "abcX"},
			{"overlay('abc','X',1,-2147483648)", "Xabc"}, {"overlay('abc','X  '::char(3),2)", "aXc"},
			{"overlay('ab  '::char(4),'X',3)", "abX"}, {"overlay('ab  '::varchar(4),'X',3)", "abX "},
			{"overlay('abc','X',2::smallint,1::smallint)", "aXc"}, {"overlay('abc','X','2','1')", "aXc"},
			{"overlay('abc','X',2::bigint::int,1::numeric::int)", "aXc"},
			{"\"overlay\"('abc','X',2)", "aXc"}, {"pg_catalog.overlay('abc','X',2,1)", "aXc"},
			{"overlay('abc'::name,'X'::name,2)", "aXc"},
		} {
			t.Run(tc.expr, func(t *testing.T) { stringReplaceValue(t, p, b, c, tc.expr, "TEXT", tc.want) })
		}
		for _, tc := range []struct {
			expr     string
			position int
		}{
			{"overlay('abc','X',VARIADIC 2)", 26},
			{"overlay('abc','X',2,VARIADIC 1)", 28},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "VARIADIC"`)
				d.Position = fixture.Position(tc.position)
				stringReplaceFailure(t, p, b, c, "SELECT "+tc.expr+" AS v", d, true)
			})
		}
		for _, expr := range []string{"overlay('abc'::bytea,'X',2)", "overlay('abc','X'::bytea,2,1)", "overlay('abc'::bytea PLACING 'X'::bytea FROM 2 FOR 1)"} {
			t.Run(expr, func(t *testing.T) { stringReplaceValue(t, p, b, c, expr, "BYTEA", "aXc") })
		}
	})
}

func TestReferenceStringReplaceReplace(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"'abcdefabcdef','cd','XX'", "abXXefabXXef"}, {"'aaaa','aa','X'", "XX"}, {"'aaa','aa','X'", "Xa"},
			{"'aaa','a','aa'", "aaaaaa"}, {"'ababa','aba','X'", "Xba"}, {"'ababa','a',''", "bb"},
			{"'abc','','X'", "abc"}, {"'','','X'", ""}, {"'','a','X'", ""}, {"'abc','z','X'", "abc"},
			{"'é🙂é','é','Ω'", "Ω🙂Ω"}, {`U&'e\0301é','é','X'`, "e\u0301X"}, {"'a.b','.','X'", "aXb"},
			{"'ab  '::char(4),' ','X'", "ab"}, {"'ab  '::varchar(4),' ','X'", "abXX"},
			{"'abc','b  '::char(3),'X  '::char(3)", "aXc"}, {"'abc'::name,'b'::name,'X'::name", "aXc"},
			{"'abc','b',VARIADIC 'X'", "aXc"},
		} {
			t.Run(tc.args, func(t *testing.T) { stringReplaceValue(t, p, b, c, "replace("+tc.args+")", "TEXT", tc.want) })
		}
		stringReplaceValue(t, p, b, c, "pg_catalog.replace('abc','b','X')", "TEXT", "aXc")
	})
}

func TestReferenceStringReplaceTranslate(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"'12345','143','ax'", "a2x5"}, {"'abcabc','abc','XY'", "XYXY"}, {"'abc','abc',''", ""},
			{"'abc','','XYZ'", "abc"}, {"'abc','a','XYZ'", "Xbc"}, {"'','abc','XYZ'", ""},
			{"'abc','aba','XYZ'", "XYc"}, {"'abc','aba','X'", "Xc"}, {"'ab','ab','ba'", "ba"},
			{"'é🙂éZ','é🙂','Ω'", "ΩΩZ"}, {"'abc','ac','🙂é'", "🙂bé"}, {`U&'a\0301b',U&'\0301',''`, "ab"},
			{"'ab  '::char(4),' ','X'", "ab"}, {"'ab  '::varchar(4),' ','X'", "abXX"},
			{"'abc','b  '::char(3),'X  '::char(3)", "aXc"}, {"'abc'::name,'b'::name,'X'::name", "aXc"},
			{"'abc','b',VARIADIC 'X'", "aXc"},
		} {
			t.Run(tc.args, func(t *testing.T) { stringReplaceValue(t, p, b, c, "translate("+tc.args+")", "TEXT", tc.want) })
		}
		stringReplaceValue(t, p, b, c, "pg_catalog.translate('abc','b','X')", "TEXT", "aXc")
	})
}

func TestReferenceStringReplacePrefix(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			source, prefix string
			want           bool
		}{
			{"'alphabet'", "'alph'", true}, {"'alphabet'", "'Alphabet'", false}, {"'abc'", "''", true},
			{"''", "''", true}, {"''", "'a'", false}, {"'a'", "'ab'", false}, {"'é🙂x'", "'é🙂'", true},
			{`U&'e\0301x'`, "'é'", false}, {`U&'e\0301x'`, "'e'", true},
			{"'ab'::char(4)", "'ab '::text", false}, {"'ab  '::varchar(4)", "'ab '::text", true},
			{"'abc'::name", "'a'::name", true}, {"'abc'", "'a  '::char(3)", true},
		} {
			t.Run(tc.source+"/"+tc.prefix, func(t *testing.T) {
				q := "SELECT starts_with(" + tc.source + "," + tc.prefix + ") AS f," + tc.source + " ^@ " + tc.prefix + " AS p," + tc.source + " OPERATOR(pg_catalog.^@) " + tc.prefix + " AS q"
				stringReplaceBoth(t, p, b, c, fixture.Select(q, []string{"f", "p", "q"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, tc.want}}))
			})
		}
		stringReplaceValue(t, p, b, c, "starts_with('abc',VARIADIC 'a')", "BOOL", true)
		stringReplaceValue(t, p, b, c, "pg_catalog.starts_with('abc','a')", "BOOL", true)
	})
}

func TestReferenceStringReplaceSignatures(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, family := range []struct {
			name, typ string
			args      []string
		}{
			{"overlay", "TEXT", []string{"'abc'", "'X'", "2"}}, {"overlay", "TEXT", []string{"'abc'", "'X'", "2", "1"}},
			{"replace", "TEXT", []string{"'abc'", "'b'", "'X'"}}, {"translate", "TEXT", []string{"'abc'", "'b'", "'X'"}},
			{"starts_with", "BOOL", []string{"'abc'", "'a'"}}, {"^@", "BOOL", []string{"'abc'", "'a'"}},
		} {
			for i := range family.args {
				for _, typed := range []bool{false, true} {
					args := append([]string(nil), family.args...)
					args[i] = "NULL"
					if typed {
						typ := "text"
						if family.name == "overlay" && i >= 2 {
							typ = "integer"
						}
						args[i] += "::" + typ
					}
					expr := family.name + "(" + strings.Join(args, ",") + ")"
					if family.name == "^@" {
						expr = args[0] + " ^@ " + args[1]
					}
					t.Run(expr, func(t *testing.T) { stringReplaceValue(t, p, b, c, expr, family.typ, nil) })
				}
			}
		}
		for _, expr := range []string{"overlay(NULL::text,'X',0)", "overlay(NULL::text,'X',2147483647,1)"} {
			stringReplaceValue(t, p, b, c, expr, "TEXT", nil)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN sr_char AS char(4)", 0))
		stringReplaceValue(t, p, b, c, "replace('ab'::sr_char,'a','X')", "TEXT", "Xb")
		// Internal CHAR comes from the existing catalog provider; its general
		// public cast syntax is outside the character prerequisite's admission.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sr_char_seed(v text)", 0))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT replace('arc'::name,relkind,'X'::name) AS r,translate('arc'::name,relkind,'X'::name) AS t,overlay('abc',relkind,2) AS o,starts_with('rabbit',relkind) AS f,'rabbit' ^@ relkind AS p FROM pg_catalog.pg_class WHERE relname='sr_char_seed'", []string{"r", "t", "o", "f", "p"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"aXc", "aXc", "arc", true, true}}))
	})
}
