package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatConversions(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, want string }{
			{"format('hello',NULL)", "hello"}, {"format('')", ""},
			{"format('%%')", "%"}, {"format('%%%%%%')", "%%%"},
			{"format('%%|%s|%L',NULL,NULL)", "%||NULL"},
			{"format('%s|%s|%L|%I',true,'x'::char(3),42,'Foo')", `t|x  |'42'|"Foo"`},
			{"format('%s|%I|%L','x'::char(3),'x'::char(3),'x'::char(3))", `x  |"x  "|'x  '`},
			{"format('%s|%s|%s|%s',false,5.0::numeric,'v  '::varchar(4),'n'::name)", "f|5.0|v  |n"},
			{`format('%s','{a,NULL,"NULL"}'::name[])`, `{a,NULL,"NULL"}`},
			{"FORMAT('%s','x')", "x"}, {`"format"('%s','x')`, "x"},
			{"pg_catalog.format('%s','x')", "x"},
			{"format('%s',format('%I','select'))", `"select"`},
			{"format('é🙂%s尾','x')", "é🙂x尾"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
	})
}

func TestReferenceFormatQuoting(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, setting := range []string{"on", "off"} {
			t.Run(setting, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET standard_conforming_strings="+setting, 0))
				for _, tc := range []struct{ call, want string }{
					{`format('%I|%I|%I|%I|%I','foo','select','abort','é','a"b')`, `foo|"select"|abort|"é"|"a""b"`},
					{`format('%I|%L','','')`, `""|''`},
					{`format('%L','O''Reilly')`, `'O''Reilly'`},
					{`format('%L',E'a\\b')`, `E'a\\b'`},
					{`format('%L',E'a''\\b')`, `E'a''\\b'`},
					{`format('%I','A` + strings.Repeat("x", 80) + `')`, `"A` + strings.Repeat("x", 80) + `"`},
				} {
					t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
				}
			})
		}
	})
}

func TestReferenceFormatPositionals(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, want string }{
			{"format('%3$s/%2$s/%s','one','two','three')", "three/two/three"},
			{"format('%2$s/%s/%1$s/%s','a','b','c')", "b/c/a/b"},
			{"format('%s%%/%s','a','b','unused')", "a%/b"},
			{"format('%1$s/%1$s/%s','a','b')", "a/a/b"},
			{"format('%0002$s/%s','a','b','c')", "b/c"},
			{"format('%3$s%%/%1$s/%s','a','b','c')", "c%/a/b"},
			{"format('|%*2$s|','foo',5,'bar')", "|  bar|"},
			{"format('|%1$*2$s|%s','x',5,'y')", "|    x|5"},
			{"format('%2$*s/%s',5,'x','y')", "    x/y"},
			{"format('%3$*2$s/%s/%1$s','a',5,'x','z')", "    x/z/a"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
	})
}

func TestReferenceFormatWidths(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, want string }{
			{"format('|%05s|%-5s|%--5s|','x','x','x')", "|    x|x    |x    |"},
			{"format('|%0s|%1s|','abc','abc')", "|abc|abc|"},
			{"format('|%3s|%1s|','é','😀')", "|  é|😀|"},
			{"format('|%2s|','é')", "|é|"},
			{"format('|%5I|%5L|','X','x')", `|  "X"|  'x'|`},
			{"format('|%-5I|%-5L|','X','x')", `|"X"  |'x'  |`},
			{"format('|%*s|',-5,'x')", "|x    |"},
			{"format('|%-*s|',5,'x')", "|x    |"},
			{"format('|%*s|',NULL,'x')", "|x|"},
			{"format('|%*s|',5::smallint,'x')", "|    x|"},
			{"format('|%*s|',5::bigint,'x')", "|    x|"},
			{"format('|%*s|','0x5','x')", "|    x|"},
			{"format('|%*s|',' +0b101 ','x')", "|    x|"},
			{"format('|%*s|','0o5','x')", "|    x|"},
			{"format('|%*s|','0_5','x')", "|    x|"},
			{"format('|%5s|%5L|',NULL,NULL)", "|     | NULL|"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
	})
}

func TestReferenceFormatNulls(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, call := range []string{"format(NULL)", "format(NULL::text,1,NULL)", "format(NULL::char(3),'unused')"} {
			t.Run(call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(call, nil)) })
		}
		for _, tc := range []struct{ call, want string }{
			{"format('%s',NULL)", ""}, {"format('%L',NULL)", "NULL"},
			{"format('|%*s|',NULL,NULL)", "||"}, {"format('literal',NULL)", "literal"},
		} {
			t.Run(tc.call, func(t *testing.T) { formatReferenceBoth(t, p, b, c, formatReferenceText(tc.call, tc.want)) })
		}
		formatReferenceFailure(t, p, b, c, "SELECT format('%I',NULL)", fixture.ControlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier"), false)
	})
}
