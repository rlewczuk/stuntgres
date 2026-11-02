package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2Binding(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, signature string }{
			{"SELECT xml_valid('<r/>')", "xml_valid(unknown)"},
			{"SELECT xpath_string('<r/>','.')", "xpath_string(unknown, unknown)"},
			{"SELECT xpath_number('<r/>','.')", "xpath_number(unknown, unknown)"},
			{"SELECT xpath_bool('<r/>','.')", "xpath_bool(unknown, unknown)"},
		} {
			xml2Failure(t, p, b, c, tc.q, xml2Missing(tc.signature), true)
		}
		xml2Install(t, p, b, c)
		// Uncast parameters must infer the installed TEXT formal types.
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1) AS v,xpath_string($1,$2) AS s,xpath_number($1,$2) AS n,xpath_bool($1,$2) AS b",
			[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, [][]any{{true, "12", float64(12), true}}, "<r>12</r>", "."))
		for _, args := range [][2]any{{nil, "["}, {"<r>", nil}, {nil, nil}} {
			xml2Both(t, p, b, c, xml2Scalars(args[0], args[1], nil, nil, nil))
		}
		xml2Both(t, p, b, c, fixture.Select("SELECT CASE WHEN false THEN xpath_string('<r/>','[') ELSE 'safe' END AS s, CASE WHEN true THEN 7::real ELSE xpath_number('<r/>','[') END AS n, CASE WHEN false THEN xpath_bool('<r/>','[') ELSE false END AS b",
			[]string{"s", "n", "b"}, []string{"TEXT", "FLOAT4", "BOOL"}, [][]any{{"safe", float64(7), false}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_binding(id integer, doc text, path text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_binding VALUES(1,'<r>12</r>','.'),(2,'<r>','['),(3,'<r/>','[')", 3))
		xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid(doc) AS v,xpath_string(doc,path) AS s,xpath_number(doc,path) AS n,xpath_bool(doc,path) AS b FROM xml2_binding WHERE false",
			[]string{"v", "s", "n", "b"}, []string{"BOOL", "TEXT", "FLOAT4", "BOOL"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO pg_catalog", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT public.xml_valid('<r/>') AS v,public.xpath_string('<r>12</r>','.') AS s",
			[]string{"v", "s"}, []string{"BOOL", "TEXT"}, [][]any{{true, "12"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public", 0))
		xml2Both(t, p, b, c, fixture.Select("SELECT id,coalesce(xpath_number(doc,path),0::real) AS n FROM xml2_binding WHERE id<3 ORDER BY id",
			[]string{"id", "n"}, []string{"INT4", "FLOAT4"}, [][]any{{int64(1), float64(12)}, {int64(2), float64(0)}}))
		xml2BindingErrors(t, p, b, c)
		xml2Demand(t, p, b, c)
		xml2Metadata(t, p, b, c)
		xml2SelectedCoercion(t, p, b, c)
		xml2ByteRepresentations(t, p, b, c)
	})
}
