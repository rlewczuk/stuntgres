package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTParameters(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		style := `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p" xmlns:q="urn:s">` + xsltTextOutput + `<xsl:param name="p" select="'default'"/><xsl:param name="p:q" select="'nsdefault'"/><xsl:variable name="v" select="'V'"/><xsl:template match="/"><xsl:value-of select="$p"/>|<xsl:value-of select="$p:q"/>|<xsl:value-of select="$v"/></xsl:template></xsl:stylesheet>`
		for _, tc := range []struct{ name, params, want string }{
			{"defaults", "", "default|nsdefault|V"},
			{"ignored_tail", "ignored", "default|nsdefault|V"},
			{"quoted", "p='given'", "given|nsdefault|V"},
			{"expression", "p=1+2", "3|nsdefault|V"},
			{"nodeset", "p=hello", "|nsdefault|V"},
			{"root_context", "p=name(/*)", "r|nsdefault|V"},
			{"position", "p=position()", "1|nsdefault|V"},
			{"size", "p=last()", "1|nsdefault|V"},
			{"equals_in_value", "p='a=b'", "a=b|nsdefault|V"},
			{"many_pairs", "a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9,j=10,k=11,p='many'", "many|nsdefault|V"},
			{"ignored_after_pair", "p=1,ignored", "1|nsdefault|V"},
			{"trailing_comma", "p=1,", "1|nsdefault|V"},
			{"untrimmed_name", " p='spaced'", "default|nsdefault|V"},
			{"qualified_name", "p:q='Q'", "default|Q|V"},
			{"clark_name", "{urn:p}q='Q'", "default|Q|V"},
			{"unqualified_distinct", "q='Q'", "default|nsdefault|V"},
			{"variable_not_parameter", "v='override'", "default|nsdefault|V"},
			{"previous_user_value", "a=2,p=$a+1", "3|nsdefault|V"},
			{"declared_variable_invalid_ignored", "v=[", "default|nsdefault|V"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := xsltParamValue(`<r xmlns:q="urn:s"><q:n>N</q:n></r>`, style, tc.params, tc.want)
				xml2Both(t, p, b, c, s)
				xml2Held(t, p, b, c, s)
			})
		}
		// Preserve the exact XML-output witnesses too: the newline belongs to
		// serialization, not parameter expression evaluation.
		for _, tc := range []struct{ name, body, params, want string }{
			{"previous_value_xml", `<xsl:value-of select="$p"/>`, "a=2,p=$a+1", "3\n"},
			{"invalid_variable_xml", `<xsl:value-of select="$v"/>`, "v=[", "V\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(`<xsl:output omit-xml-declaration="yes"/>`, `<xsl:param name="p"/><xsl:variable name="v" select="'V'"/>`, tc.body)
				s := xsltParamValue(`<r><n>B</n><n>A</n></r>`, style, tc.params, tc.want)
				xml2Both(t, p, b, c, s)
				xml2Held(t, p, b, c, s)
			})
		}
		stmt, err := c.PrepareContext(p.Ctx, xsltThreeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xsltThreeQuery}
		for _, tc := range []struct{ name, params, detail string }{
			{"empty_expression", "p=", "Invalid expression"},
			{"comma_is_not_csv", "p='x,y'", "Unfinished literal"},
			{"duplicate", "p=1,p=2", ""},
			{"unbound_name", "bad:name='X'", ""},
			{"separate_global_environment", "p=$v", "Undefined variable"},
			{"separate_namespace_environment", "p=/r/q:n", "Undefined namespace prefix"},
			{"invalid_path", "p=/*/name()", "Invalid expression"},
			{"unused_is_still_evaluated", "unused=[", "Invalid expression"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				want := xsltDiagnostic("apply", tc.detail)
				xml2Failure(t, p, b, c, xsltThreeQuery, want, false, `<r/>`, style, tc.params)
				bad := fixture.ErrorDiagnostic(xsltThreeQuery, want, `<r/>`, style, tc.params)
				bad.SelectRows, bad.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, bad)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, xsltParamValue(`<r/>`, style, "p='healthy'", "healthy|nsdefault|V"))
			})
		}
		// Parameters are evaluated afresh on the source root, not retained from a
		// previous execution, and the two-argument declaration keeps its default.
		xml2Held(t, p, b, c,
			xsltParamValue(`<one/>`, style, "p=name(/*)", "one|nsdefault|V"),
			xsltParamValue(`<two/>`, style, "p=name(/*)", "two|nsdefault|V"),
			xsltParamValue(`<r/>`, style, nil, nil))
		xml2Both(t, p, b, c, xsltValue(`<r/>`, style, "default|nsdefault|V"))
	})
}
