package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathNamespaces(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ path, doc, mapping, want string }{
			{"/q:r/q:a", `<doc:r xmlns:doc="urn:x"><doc:a>x</doc:a></doc:r>`, `ARRAY[ARRAY['q','urn:x']]`, `<doc:a xmlns:doc="urn:x">x</doc:a>`},
			{"/q:r", `<r xmlns="urn:b"/>`, `ARRAY[ARRAY['q','urn:a'],ARRAY['q','urn:b']]`, `<r xmlns="urn:b"/>`},
			{"/q:r", `<r xmlns="urn:x"/>`, `'[0:0][-1:0]={{q,urn:x}}'::text[]`, `<r xmlns="urn:x"/>`},
			{"/r/@xml:lang", `<r xml:lang="en"/>`, `ARRAY[ARRAY['xml','urn:wrong']]`, `en`},
			{"/r/@xml:lang", `<r xml:lang="en"/>`, `ARRAY[ARRAY['xml','']]`, `en`},
			{"/r/@a", `<r a="v"/>`, `ARRAY[ARRAY['not a NCName','urn:x']]`, `v`},
			{"/q:r/b:child", `<r xmlns="urn:d" xmlns:a="urn:a" xmlns:z="urn:z"><a:child a:x="v"><leaf/></a:child></r>`, `ARRAY[ARRAY['q','urn:d'],ARRAY['b','urn:a']]`, `<a:child xmlns:a="urn:a" xmlns="urn:d" a:x="v"><leaf/></a:child>`},
			{"/q:r/b:child/q:leaf", `<r xmlns="urn:d" xmlns:a="urn:a" xmlns:z="urn:z"><a:child a:x="v"><leaf/></a:child></r>`, `ARRAY[ARRAY['q','urn:d'],ARRAY['b','urn:a']]`, `<leaf xmlns="urn:d"/>`},
			{`/q:r/namespace::a`, `<r xmlns="urn:d" xmlns:a="urn:a"/>`, `ARRAY[ARRAY['q','urn:d']]`, `urn:a`},
			{`/q:r/namespace::*[name()=""]`, `<r xmlns="urn:d"/>`, `ARRAY[ARRAY['q','urn:d']]`, `urn:d`},
			{`/r/namespace::*[name()="xml"]`, `<r/>`, `'{}'::text[]`, `http://www.w3.org/XML/1998/namespace`},
			{`/r/namespace::q`, `<r xmlns:q="urn:q?x=1&amp;y=2"/>`, `'{}'::text[]`, `urn:q?x=1&amp;#38;y=2`},
			{`/r/namespace::q`, `<r xmlns:q="urn:q?x=1&#38;y=2"/>`, `'{}'::text[]`, `urn:q?x=1&amp;#38;y=2`},
		} {
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(tc.path, tc.doc, tc.mapping), "TEXT", tc.want))
		}
		for _, tc := range []struct {
			path, doc, mapping string
			present            bool
			want               any
		}{
			{"/r", `<r xmlns="urn:d"/>`, `'{}'::text[]`, false, nil},
			{"/q:r", `<r/>`, `ARRAY[ARRAY['q','']]`, false, nil},
			{"/q:r", `<r xmlns=""/>`, `ARRAY[ARRAY['q','']]`, false, nil},
			{"//q:r", `<z xmlns="urn:d"><r xmlns=""/></z>`, `ARRAY[ARRAY['q','']]`, false, nil},
			{"/r/@q:a", `<r a="v"/>`, `ARRAY[ARRAY['q','']]`, false, nil},
			{`/r/namespace::*[name()=""]`, `<r/>`, `'{}'::text[]`, false, nil},
			{`/r/namespace::*[name()=""]`, `<r xmlns=""/>`, `'{}'::text[]`, true, ""},
			{`//*[local-name()="r"]/namespace::*[name()=""]`, `<z xmlns="urn:d"><r xmlns=""/></z>`, `'{}'::text[]`, true, ""},
		} {
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, xpathSingleton(tc.path, tc.doc, tc.mapping), "TEXT", tc.want))
			args := xpathSQL(tc.path) + "," + xpathSQL(tc.doc) + "::xml," + tc.mapping
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT xpath_exists("+args+") AS v", "BOOL", tc.present))
			count := int64(0)
			if tc.present {
				count = 1
			}
			fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT cardinality(xpath("+args+")) AS v", "INT4", count))
		}
		// Shared matching helpers retain XMLTABLE and XMLEXISTS semantics.
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, `SELECT XMLEXISTS('/r' PASSING ('<r xmlns=""/>'::xml)) AS v`, "BOOL", true))
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, `SELECT v FROM XMLTABLE(XMLNAMESPACES('urn:x' AS q), '/q:r' PASSING ('<r xmlns="urn:x"/>'::xml) COLUMNS v text PATH 'name(.)') AS t`, "TEXT", "r"))
	})
}
