package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const xsltUnfinishedStyle = "line 1: Namespace prefix xsl on stylesheet is not defined\n<xsl:stylesheet\n               ^\nline 1: Couldn't find end of Start Tag stylesheet line 1\n<xsl:stylesheet\n               ^"
const xsltOpenSource = "line 1: Premature end of data in tag r line 1\n<r>\n   ^"
const xsltOpenConvertedSource = "line 1: Premature end of data in tag r line 1\n?><r>\n     ^"

func xsltDiagnostic(stage, detail string) fixture.Diagnostic {
	code, message := "10608", "failed to apply stylesheet"
	switch stage {
	case "source":
		code, message = "2200M", "error parsing XML document"
	case "style":
		code, message = "2200M", "error parsing stylesheet as XML document"
	case "compile":
		message = "failed to parse stylesheet"
	}
	d := xml2Error(code, message)
	d.Detail = detail
	return d
}

func TestReferenceXML2XSLTDiagnostics(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		good := xsltSmokeStyle(xsltTextOutput, "", `T`)
		stmt, err := c.PrepareContext(p.Ctx, xsltThreeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xsltThreeQuery}
		for _, tc := range []struct{ name, source, style, params, stage, detail string }{
			{"source_empty", "", good, "", "source", ""},
			{"source_bom_only", "\ufeff", good, "", "source", "line 1: Start tag expected, '<' not found\n\ufeff\n^"},
			{"source_decl_only", `<?xml version="1.0"?>`, good, "", "source", "line 1: Start tag expected, '<' not found\n<?xml version=\"1.0\"?>\n                     ^"},
			{"source_before_style", `<r>`, `<xsl:stylesheet`, "p=", "source", xsltOpenSource},
			{"style_empty", `<r/>`, "", "", "style", ""},
			{"style_bom_only", `<r/>`, "\ufeff", "", "style", "line 1: Start tag expected, '<' not found\n\ufeff\n^"},
			{"style_decl_only", `<r/>`, `<?xml version="1.0"?>`, "", "style", "line 1: Start tag expected, '<' not found\n<?xml version=\"1.0\"?>\n                     ^"},
			{"style_before_params", `<r/>`, `<xsl:stylesheet`, "p=", "style", xsltUnfinishedStyle},
			{"compile_before_params", `<r/>`, `<bad/>`, "p=", "compile", ""},
			{"invalid_select", `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="["/>`), "", "compile", "Invalid expression"},
			{"undefined_variable", `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:value-of select="$missing"/>`), "", "apply", "Undefined variable"},
			{"parameter_expression", `<r/>`, good, "unused=[", "apply", "Invalid expression"},
			{"terminate", `<r/>`, xsltSmokeStyle(xsltTextOutput, "", `<xsl:message terminate="yes">M</xsl:message>T`), "", "apply", ""},
			{"namespaced_method", `<r/>`, `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:p="urn:p"><xsl:output method="p:unknown"/><xsl:template match="/">T</xsl:template></xsl:stylesheet>`, "", "apply", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				want := xsltDiagnostic(tc.stage, tc.detail)
				xml2Failure(t, p, b, c, xsltThreeQuery, want, false, tc.source, tc.style, tc.params)
				bad := fixture.ErrorDiagnostic(xsltThreeQuery, want, tc.source, tc.style, tc.params)
				bad.SelectRows, bad.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, bad)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, xsltParamValue(`<r/>`, good, "", "T"))
			})
		}
		for _, tc := range []struct{ name, declarations, body, stage, detail string }{
			{"duplicate_global", `<xsl:variable name="v" select="1"/><xsl:variable name="v" select="2"/>`, `<xsl:value-of select="$v"/>`, "compile", ""},
			{"comment_then_undefined", ``, `<xsl:comment>A--B</xsl:comment><xsl:value-of select="$missing"/>`, "apply", "Undefined variable"},
			{"denied_document_then_undefined", ``, `<xsl:value-of select="document('file:///stuntgres-xslt-no-such-file.xml')"/><xsl:value-of select="$missing"/>`, "apply", "Undefined variable"},
			{"global_cycle", `<xsl:variable name="a" select="$b"/><xsl:variable name="b" select="$a"/>`, `<xsl:value-of select="$a"/>`, "apply", "Undefined variable\nUndefined variable\nUndefined variable\nUndefined variable"},
			{"bad_match", `<xsl:template match="[">X</xsl:template>`, `T`, "compile", ""},
			{"bad_select", ``, `<xsl:value-of select="["/>`, "compile", "Invalid expression"},
			{"scalar_apply", ``, `<xsl:apply-templates select="1"/>`, "apply", ""},
			{"late_attribute", ``, `<e>T<xsl:attribute name="a">V</xsl:attribute></e>`, "apply", ""},
			{"comment_hyphens", ``, `<xsl:comment>A--B</xsl:comment>T`, "apply", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(xsltTextOutput, tc.declarations, tc.body)
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic(tc.stage, tc.detail), false, `<r/>`, style)
				xml2Both(t, p, b, c, xsltValue(`<r/>`, good, "T"))
			})
		}
		for _, tc := range []struct{ name, body, stage string }{
			{"xml_late_attribute", `<e>T<xsl:attribute name="a">V</xsl:attribute></e>`, "apply"},
			{"xml_comment_hyphens", `<xsl:comment>A--B</xsl:comment>T`, "apply"},
			{"bad_avt", `<e a="{"/>`, "compile"},
			{"bad_dynamic_qname", `<xsl:element name="{'1bad'}"/>`, "apply"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(`<xsl:output omit-xml-declaration="yes"/>`, "", tc.body)
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic(tc.stage, ""), false, `<r/>`, style)
			})
		}
		for _, tc := range []struct{ name, body, params, stage string }{
			{"dynamic_bad_qname", `<xsl:element name="{$p}"/>`, "p='bad name'", "apply"},
			{"static_bad_qname", `<xsl:element name="1bad"/>`, "", "compile"},
			{"comment_final_hyphen", `<xsl:comment>A-</xsl:comment>`, "", "apply"},
			{"pi_terminator", `<xsl:processing-instruction name="p">A?&gt;B</xsl:processing-instruction>`, "", "apply"},
			{"dynamic_sort_order", `<xsl:for-each select="r/n"><xsl:sort select="." order="{$p}"/><xsl:value-of select="."/></xsl:for-each>`, "p='sideways'", "apply"},
			{"dynamic_sort_type", `<xsl:for-each select="r/n"><xsl:sort select="." data-type="{$p}"/><xsl:value-of select="."/></xsl:for-each>`, "p='mystery'", "apply"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				style := xsltSmokeStyle(`<xsl:output omit-xml-declaration="yes"/>`, `<xsl:param name="p"/><xsl:variable name="v" select="'V'"/>`, tc.body)
				xml2Failure(t, p, b, c, xsltThreeQuery, xsltDiagnostic(tc.stage, ""), false, `<r><n>B</n><n>A</n></r>`, style, tc.params)
			})
		}
		for _, tc := range []struct{ label, tail, preview string }{
			{"ASCII", "éABCD", ""},
			{"ISO-2022-KR", "éABCD", "0xC3 0xA9 0x41 0x42"},
			{"ISO-2022-CN", "éABCD", "0xC3 0xA9 0x41 0x42"},
			{"UTF-7", "+3AA-ABCD", "0x2B 0x33 0x41 0x41"},
		} {
			t.Run("cross_stage_"+tc.label, func(t *testing.T) {
				header := `<?xml version="1.0" encoding="` + tc.label + `"?>`
				detail := ""
				if tc.preview != "" {
					detail = xsltConversionDetail(tc.preview)
				}
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("compile", detail), false, header+`<r/>`+tc.tail, `<bad/>`)
				prefix := detail
				if prefix != "" {
					prefix += "\n"
				}
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("style", prefix+xsltUnfinishedStyle), false, header+`<r/>`+tc.tail, `<xsl:stylesheet`)
				xml2Failure(t, p, b, c, xsltTwoQuery, xsltDiagnostic("source", prefix+xsltOpenConvertedSource), false, header+`<r>`+tc.tail, good)
			})
		}
		for _, label := range []string{"ISO-2022-KR", "ISO-2022-CN"} {
			source := `<?xml version="1.0" encoding="` + label + `"?><r>é`
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("shortEOF_%s_prepared_%t", label, prepared), func(t *testing.T) {
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, prepareErr := c.PrepareContext(p.Ctx, xsltTwoQuery)
						if prepareErr != nil {
							t.Fatal("parameterized prepare failed", prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx, source, good)
					} else {
						rows, err = c.QueryContext(p.Ctx, xsltTwoQuery, source, good)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("short EOF failure published Rows")
					}
					if err == nil {
						t.Fatal("short EOF unexpectedly succeeded")
					}
					got := fixture.ExtractDiagnostic(t, err)
					got.Detail = xsltBoundedEOFDetail(t, b.PG, source, got.Detail)
					want := xsltDiagnostic("source", xsltConversionDetail("0xC3 0xA9 0x00")+"\n"+xsltOpenConvertedSource)
					if got != want {
						t.Fatalf("short EOF full diagnostic=%#v want=%#v", got, want)
					}
				})
			}
		}
		xml2Both(t, p, b, c, xsltValue(`<r/>`, good, "T"))
	})
}

func xsltConversionDetail(preview string) string {
	line := "input conversion failed due to input error, bytes " + preview
	return line + "\n" + line + "\nencoder error"
}

// This exception applies only to the exact short-EOF callback's fourth byte.
// The fault starts at a separately established physical input offset; bytes at
// offsets len(input) and len(input)+1 are respectively the supplied terminator
// and beyond the allocation's input contract. All three in-contract bytes,
// both callback occurrences, every other Detail byte and all diagnostic fields
// remain exact. No production read, padding, hardcoded residue or Detail scrub.
func xsltBoundedEOFDetail(t *testing.T, postgres bool, source, detail string) string {
	t.Helper()
	fault := strings.LastIndex(source, "é")
	if fault < 0 || source[:fault] != strings.TrimSuffix(source, "é") || fault+2 != len(source) || source[fault:] != "\xc3\xa9" {
		t.Fatal("short EOF fixture does not prove the physical input boundary")
	}
	actual := []byte(source[fault:])
	actual = append(actual, 0) // xmlReadMemory's one in-contract terminating NUL.
	lines := strings.Split(detail, "\n")
	if len(lines) != 6 {
		t.Fatalf("unexpected short EOF event structure %q", detail)
	}
	const prefix = "input conversion failed due to input error, bytes "
	for i := range 2 {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Fatalf("unexpected conversion callback %q", lines[i])
		}
		parts := strings.Split(strings.TrimPrefix(lines[i], prefix), " ")
		wantCount := len(actual)
		if postgres {
			wantCount = 4
		}
		if len(parts) != wantCount {
			t.Fatalf("callback preview width=%d want=%d", len(parts), wantCount)
		}
		for j, token := range parts {
			if len(token) != 4 || !strings.HasPrefix(token, "0x") {
				t.Fatalf("invalid byte token %q", token)
			}
			value, err := strconv.ParseUint(token[2:], 16, 8)
			if err != nil || fmt.Sprintf("0x%02X", value) != token {
				t.Fatalf("invalid callback byte %q", token)
			}
			if j < len(actual) {
				if byte(value) != actual[j] {
					t.Fatalf("input byte offset %d=%s want=0x%02X", fault+j, token, actual[j])
				}
			} else if !postgres || fault+j <= len(source) || j != 3 {
				t.Fatal("attempted to exclude an in-input or terminating byte")
			}
		}
		lines[i] = prefix + strings.Join(parts[:len(actual)], " ")
	}
	return strings.Join(lines, "\n")
}
