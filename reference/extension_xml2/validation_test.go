package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2Validation(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmloption TO "+mode, 0))
			for _, tc := range []struct {
				doc               any
				content, document any
			}{
				{"", true, false}, {"plain text", true, false}, {"<a/><b/>", true, false},
				{"<r/>", true, true}, {"<r>", false, false}, {"<r>&unknown;</r>", false, false},
				{"<r a=\"1\" a=\"2\"/>", false, false},
				{"<!DOCTYPE r [<!ENTITY e 'ok'>]><r>&e;</r>", true, true},
				{"<!DOCTYPE r [<!ATTLIST r a CDATA #REQUIRED>]><r/>", true, true},
				{nil, nil, nil},
			} {
				want := tc.content
				if mode == "DOCUMENT" {
					want = tc.document
				}
				xml2Both(t, p, b, c, fixture.Select("SELECT xml_valid($1::text) AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{want}}, tc.doc))
			}
			// The scalar entry always parses DOCUMENT, independent of xmloption.
			xml2Both(t, p, b, c, xml2Scalars("plain text", ".", nil, nil, false))
		}
		q := "SELECT xml_valid($1::text) AS valid"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET xmloption TO CONTENT", 0))
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET xmloption TO "+mode, 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"valid"}, []string{"BOOL"}, [][]any{{mode == "CONTENT"}}, "plain text"))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(q, []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, "plain text"))
		}
		xml2ValidationWarnings(t, p, b)
	})
}
