package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2ScalarValues(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct {
			doc, path string
			s, n, b   any
		}{
			{"<r><a>12</a><a>7</a></r>", "/r/a", "12", float64(12), true},
			{"<r><a>12</a><a>7</a></r>", "a", "12", float64(12), true},
			{"<r><a>12</a></r>", "r/a", "", nil, false},
			{"<r><a>12</a></r>", "/missing", "", nil, false},
			{"<r><a/></r>", "/r/a", "", nil, true},
			{"<r n=\"19\"/>", "@n", "19", float64(19), true},
			{"<r>A<b>B</b><![CDATA[C]]>D</r>", ".", "ABCD", nil, true},
			{"<r><!--23--><?p 17?><a>5</a></r>", "comment()", "23", float64(23), true},
			{"<r><!--23--><?p 17?><a>5</a></r>", "processing-instruction('p')", "17", float64(17), true},
			{"<r><a><b>6</b></a><b>9</b></r>", ".//b", "6", float64(6), true},
			{"<!DOCTYPE r [<!ENTITY e \"A<b>B</b>\">]><r>&e;C</r>", ".", "ABC", nil, true},
			{"<!DOCTYPE r [<!ENTITY e \"A<b>B</b>\">]><r>&e;C</r>", "b", "B", nil, true},
			{"<!DOCTYPE r [<!ENTITY e \"A<b>B</b>\">]><r>&e;C</r>", "count(/r/*)", "1", float64(1), true},
		} {
			xml2Both(t, p, b, c, xml2Scalars(tc.doc, tc.path, tc.s, tc.n, tc.b))
		}
		xml2Held(t, p, b, c,
			xml2Scalars("<r><a>12</a></r>", "a", "12", float64(12), true),
			xml2Scalars("<r><a>12</a></r>", "/missing", "", nil, false),
			xml2Scalars("<r>", "[", nil, nil, false),
			xml2Scalars("<r><a>12</a></r>", "a", "12", float64(12), true))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_inputs(id integer, doc text, path text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_inputs VALUES(2,'<r><a/></r>','a'),(1,'<r><a>12</a></r>','a'),(3,'<r/>','/missing')", 3))
		xml2Both(t, p, b, c, fixture.Select("SELECT id,xpath_string(doc,path) AS s,xpath_number(doc,path) AS n,xpath_bool(doc,path) AS b FROM xml2_inputs ORDER BY id",
			[]string{"id", "s", "n", "b"}, []string{"INT4", "TEXT", "FLOAT4", "BOOL"}, [][]any{{int64(1), "12", float64(12), true}, {int64(2), "", nil, true}, {int64(3), "", nil, false}}))
	})
}
