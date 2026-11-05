package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTBinding(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		style := xsltSmokeStyle(xsltTextOutput, `<xsl:param name="p" select="'D'"/>`, `<xsl:value-of select="/r"/><xsl:value-of select="$p"/>`)
		for _, args := range [][3]any{{nil, style, ""}, {`<r/>`, nil, ""}, {`<r/>`, style, nil}, {nil, nil, nil}, {nil, `<bad/>`, "p="}, {`<r>`, nil, "p="}, {`<r>`, `<bad/>`, nil}} {
			xml2Both(t, p, b, c, xsltParamValue(args[0], args[1], args[2], nil))
		}
		for _, args := range [][2]any{{nil, style}, {`<r/>`, nil}, {nil, nil}} {
			xml2Both(t, p, b, c, xsltValue(args[0], args[1], nil))
		}
		xml2Both(t, p, b, c, xsltValue([]byte(nil), style, nil))
		xml2Both(t, p, b, c, xsltValue(`<r/>`, []byte(nil), nil))
		xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("source", ""), false, []byte{}, style)
		xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("style", ""), false, `<r/>`, []byte{})
		xml2Held(t, p, b, c, xsltValue(`<r>A</r>`, style, "AD"), xsltValue(`<r>B</r>`, style, "BD"), xsltValue(nil, style, nil), xsltValue(`<r>C</r>`, style, "CD"))
		xml2Held(t, p, b, c, xsltParamValue(`<r>A</r>`, style, "p='X'", "AX"), xsltParamValue(`<r>B</r>`, style, "p='Y'", "BY"), xsltParamValue(`<r>C</r>`, style, nil, nil), xsltParamValue(`<r>D</r>`, style, "", "DD"))
		for _, query := range []string{
			"SELECT xslt_process($1::varchar,$2::varchar) AS v",
			"SELECT xslt_process($1::character(16),$2::text) AS v",
		} {
			xml2Both(t, p, b, c, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"AD"}}, `<r>A</r>`, style))
		}
		for _, tc := range []struct{ query, signature string }{
			{"SELECT xslt_process()", "xslt_process()"},
			{"SELECT xslt_process('<r/>')", "xslt_process(unknown)"},
			{"SELECT xslt_process(1,2)", "xslt_process(integer, integer)"},
			{"SELECT xslt_process('<r/>','<bad/>',1)", "xslt_process(unknown, unknown, integer)"},
			{"SELECT xslt_process('<r/>','<bad/>','','')", "xslt_process(unknown, unknown, unknown, unknown)"},
			{"SELECT xslt_process(source => '<r/>',style => '<bad/>')", "xslt_process(source => unknown, style => unknown)"},
			{"SELECT xslt_process(VARIADIC ARRAY['<r/>','<bad/>'])", "xslt_process(text[])"},
		} {
			xml2Failure(t, p, b, c, tc.query, xml2Missing(tc.signature), true)
		}
		for _, tc := range []struct{ query, message string }{
			{"SELECT xslt_process(DISTINCT '<r/>','<bad/>')", "DISTINCT specified, but xslt_process is not an aggregate function"},
			{"SELECT xslt_process('<r/>','<bad/>') FILTER (WHERE true)", "FILTER specified, but xslt_process is not an aggregate function"},
			{"SELECT xslt_process('<r/>','<bad/>') OVER ()", "OVER specified, but xslt_process is not a window function nor an aggregate function"},
		} {
			d := xml2Error("42809", tc.message)
			d.Position = "8"
			xml2Failure(t, p, b, c, tc.query, d, true)
		}
		// The two declarations differ in volatility, even when params is empty.
		xml2Failure(t, p, b, c, "SELECT xslt_process('<r>','<bad/>') AS v WHERE false", xsltDiagnostic("source", xsltOpenSource), false)
		xml2Both(t, p, b, c, fixture.Select("SELECT xslt_process('<r>','<bad/>','') AS v WHERE false", []string{"v"}, []string{"TEXT"}, nil))
		for _, call := range []string{"xslt_process('<r>','<bad/>')", "xslt_process('<r>','<bad/>','')"} {
			xml2Both(t, p, b, c, fixture.Select("SELECT CASE WHEN false THEN "+call+" ELSE 'safe' END AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"safe"}}))
		}
		// Selected nonstrict casts execute before the selected STRICT function.
		for _, q := range []string{
			"CREATE TYPE xslt_input AS (v integer)",
			"CREATE FUNCTION xslt_cast(xslt_input) RETURNS text LANGUAGE SQL IMMUTABLE CALLED ON NULL INPUT AS 'SELECT ''<r>C</r>''::text'",
			"CREATE CAST (xslt_input AS text) WITH FUNCTION xslt_cast(xslt_input) AS IMPLICIT",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		xml2Both(t, p, b, c, fixture.Select("SELECT xslt_process(NULL::xslt_input,$1::text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"CD"}}, style))
	})
}

func TestReferenceXML2XSLTMetadata(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		q := "SELECT p.pronargs::integer AS arity,p.proargtypes::text AS args,p.prorettype::integer AS result,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.proretset,p.prorows,p.provariadic::integer AS variadic,p.pronargdefaults::integer AS defaults,p.proargnames IS NULL AS unnamed,p.proargdefaults IS NULL AS no_defaults,l.lanname,p.prosrc,p.probin,p.prokind::text AS kind FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='public' AND p.proname='xslt_process' ORDER BY p.pronargs"
		xml2Both(t, p, b, c, fixture.Select(q,
			[]string{"arity", "args", "result", "proisstrict", "volatility", "parallel", "proretset", "prorows", "variadic", "defaults", "unnamed", "no_defaults", "lanname", "prosrc", "probin", "kind"},
			[]string{"INT4", "TEXT", "INT4", "BOOL", "TEXT", "TEXT", "BOOL", "FLOAT4", "INT4", "INT4", "BOOL", "BOOL", "NAME", "TEXT", "TEXT", "TEXT"},
			[][]any{{int64(2), "25 25", int64(25), true, "i", "s", false, float64(0), int64(0), int64(0), true, true, "c", "xslt_process", "$libdir/pgxml", "f"}, {int64(3), "25 25 25", int64(25), true, "v", "s", false, float64(0), int64(0), int64(0), true, true, "c", "xslt_process", "$libdir/pgxml", "f"}}))
		style := xsltSmokeStyle(xsltTextOutput, "", `T`)
		for _, empty := range []bool{false, true} {
			q := "SELECT xslt_process($1::text,$2::text) AS two,xslt_process($1::text,$2::text,'') AS three"
			want := [][]any{{"T", "T"}}
			if empty {
				q += " WHERE false"
				want = nil
			}
			s := fixture.Select(q, []string{"two", "three"}, []string{"TEXT", "TEXT"}, want, `<r/>`, style)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 2 {
					t.Fatal("XSLT metadata", meta, err)
				}
				fixture.ScalarAssertMetadata(t, b.PG, meta, 0, 1)
			}
			xml2Both(t, p, b, c, s)
		}
	})
}
