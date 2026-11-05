package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2SetsSerialization(t *testing.T) {
	xml2SetsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct {
			name, doc, path, nodes, list string
		}{
			{"attribute", `<r a="A&amp;B"/>`, "/r/@a", ` a="A&amp;B"`, "A&B"},
			{"inherited_prefix", `<r xmlns:p="urn:p"><p:a/></r>`, "/*/*", "<p:a/>", ""},
			{"physical_namespace", `<r><p:a xmlns:p="urn:p"/></r>`, "/*/*", `<p:a xmlns:p="urn:p"/>`, ""},
			{"namespace_node", `<r xmlns:p="urn:p"/>`, "/*/namespace::p", "urn:p", "urn:p"},
			{"cdata", "<r><![CDATA[A&B]]></r>", "/r/text()", "<![CDATA[A&B]]>", "A&B"},
			{"text", "<r>A&amp;B&lt;雪&gt;</r>", "/r/text()", "A&amp;B&lt;雪&gt;", "A&B<雪>"},
			{"comment", "<r><!--A&B--></r>", "/r/comment()", "<!--A&B-->", "A&B"},
			{"pi", "<r><?p A&B?></r>", "/r/processing-instruction()", "<?p A&B?>", "A&B"},
			{"mixed", "<r>A&amp;B<!--c--><?p d?><a>雪</a></r>", "/r/node()", "A&amp;B<!--c--><?p d?><a>雪</a>", "A&B|c|d|雪"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2) AS value", tc.nodes, tc.doc, tc.path))
				xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_list($1,$2,'|') AS value", tc.list, tc.doc, tc.path))
				wrapped := "<o><i>" + tc.nodes + "</i></o>"
				if tc.name == "mixed" {
					wrapped = "<o><i>A&amp;B</i><i><!--c--></i><i><?p d?></i><i><a>雪</a></i></o>"
				}
				xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2,'o','i') AS value", wrapped, tc.doc, tc.path))
			})
		}
		for _, tc := range []struct{ root, item, want string }{
			{"x y", "i&", "<x y><i&><a/></i&></x y>"},
			{"", "i&", "<i&><a/></i&>"},
			{"x y", "", "<x y><a/></x y>"},
			{"", "", "<a/>"},
		} {
			xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2,$3,$4) AS value", tc.want, "<r><a/></r>", "/r/a", tc.root, tc.item))
		}
		// Exact bytes from pinned PostgreSQL 18.6: even documents without an
		// explicit encoding declaration dump the UTF-8 declaration.
		for _, tc := range []struct{ name, doc, want string }{
			{"implicit", "<r/>", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"},
			{"declaration", `<?xml version="1.0"?><r/>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r/>\n"},
			{"encoding", `<?xml version="1.0" encoding="UTF-8"?><r>雪</r>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r>雪</r>\n"},
			{"top_level", "<?p before?><!--head--><r/><!--tail--><?q after?>", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<?p before?>\n<!--head-->\n<r/>\n<!--tail-->\n<?q after?>\n"},
			{"dtd", `<!DOCTYPE r [<!ENTITY e "X">]><r>&e;</r>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE r [\n<!ENTITY e \"X\">\n]>\n<r>X</r>\n"},
			{"multiline", "<r>\n<a/>\n</r>", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<r>\n<a/>\n</r>\n"},
		} {
			t.Run("document_"+tc.name, func(t *testing.T) {
				xml2Both(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2) AS value", tc.want, tc.doc, "/"))
				xml2Held(t, p, b, c, xml2SetValue("SELECT xpath_nodeset($1,$2) AS value", tc.want, tc.doc, "/"))
			})
		}
	})
}
