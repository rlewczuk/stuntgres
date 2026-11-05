package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2KRCNUTF7FeatureSmoke(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		var sourceSteps, styleSteps []fixture.Step
		for _, tc := range xml2KRCNUTF7Cases() {
			source := xml2RawValues(xml2RawDocument(tc.label, tc.body), tc.valid, tc.text, tc.length, true)
			style := xml2KRCNUTF7StyleValues(xml2KRCNUTF7Style(tc.label, tc.body), tc.valid, tc.text, tc.length, true)
			xml2Both(t, p, b, c, source)
			xml2Both(t, p, b, c, style)
			sourceSteps = append(sourceSteps, source)
			styleSteps = append(styleSteps, style)
		}
		// Consume all three families plus mixed UTF7, supplementary Unicode,
		// CN alternate/SS2 and the three source-backed CNS1 supplement facts on
		// real held statements. No source inspection or transformation claim.
		sourceSteps = append(sourceSteps,
			xml2RawValues(xml2KRCNUTF7Malformed()[0], false, nil, nil, false),
			xml2RawValues(nil, nil, nil, nil, nil),
			xml2RawValues(xml2RawDocument("UTF-7", "A+AOk-B+-C"), true, "AéB+C", float64(5), true))
		styleSteps = append(styleSteps,
			xml2KRCNUTF7StyleValues(xml2KRCNUTF7Style("UTF-7", "+AA-"), true, nil, nil, false),
			xml2KRCNUTF7StyleValues(xml2KRCNUTF7Style("ISO-2022-KR", "\x1b$)C\x0eGQ\x0f"), false, "한", float64(1), true))
		xml2Held(t, p, b, c, sourceSteps...)
		xml2Held(t, p, b, c, styleSteps...)
		cn := xml2RawDocument("ISO-2022-CN", "\x1b$)A\x0eVP\x0f")
		xml2Both(t, p, b, c, xml2Scalars(xml2KRCNUTF7Malformed()[0], "[", nil, nil, false))
		xml2Failure(t, p, b, c, "SELECT xpath_string($1::text,$2::text)", xml2Compile("Invalid expression"), false, cn, "[")
		xml2Both(t, p, b, c, xml2RawValues(cn, false, "中", float64(1), true))
	})
}
